//go:build jms_servicebus_real

package jms

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/jasonczech/apexload/pkg/identitypolicy"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
)

var (
	serviceBusClients      = newServiceBusClientPool()
	serviceBusShardCounter atomic.Uint64
)

func newServiceBusAdapter(cfg config) brokerAdapter {
	conn, err := resolveServiceBusConn(cfg)
	if err != nil {
		return serviceBusConfigErrorAdapter{err: err}
	}
	shards := serviceBusShardCount(cfg.Properties)
	shard := int(serviceBusShardCounter.Add(1)-1) % shards
	handle, err := serviceBusClients.acquire(conn, serviceBusPoolKey(conn, shard))
	if err != nil {
		return serviceBusConfigErrorAdapter{err: err}
	}
	return &serviceBusAdapter{
		cfg:       cfg,
		conn:      conn,
		handle:    handle,
		receivers: make(map[string]*azservicebus.Receiver),
		inflight:  make(map[string]inflightMessage),
		sessions:  make(map[string]*azservicebus.SessionReceiver),
	}
}

type serviceBusAdapter struct {
	cfg       config
	conn      serviceBusConn
	handle    *serviceBusPoolEntry
	mu        sync.Mutex
	receivers map[string]*azservicebus.Receiver
	// inflight holds peek-locked messages awaiting explicit settlement (jms_settle
	// / jms_renew_lock), keyed by a per-VUser settlement token. The SDK settles via
	// the *ReceivedMessage on the SAME receiver it came from, so both are stored.
	inflight map[string]inflightMessage
	// sessions holds accepted SessionReceivers keyed by a per-VUser session token
	// (jms_session_accept), reused for session consume + session-state get/set.
	sessions   map[string]*azservicebus.SessionReceiver
	sessionSeq atomic.Uint64
	// sessionReservations closes the race between the preflight limit check and
	// the blocking SDK AcceptSession call.
	sessionReservations int
	inflightBytes       int64
	closed              bool
}

const (
	maxServiceBusReceivers        = 128
	maxServiceBusReceiverKeyBytes = 64 << 10
	maxServiceBusInflightMessages = 1024
	maxServiceBusInflightBytes    = int64(8 << 20)
	maxServiceBusSessions         = 64
	maxServiceBusSenders          = 128
	maxServiceBusSenderKeyBytes   = 64 << 10
	maxServiceBusPoolEntries      = 256
	maxServiceBusPoolKeyBytes     = 128 << 10
)

// sbMessageSettler is the message-settlement method set shared by *Receiver and
// *SessionReceiver, so an in-flight message settles through whichever it came
// from. (RenewMessageLock is NOT here: session messages are locked by the session
// lock, not per-message, so lock renewal is carried as a closure on inflightMessage.)
type sbMessageSettler interface {
	CompleteMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.CompleteMessageOptions) error
	AbandonMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.AbandonMessageOptions) error
	DeadLetterMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.DeadLetterOptions) error
	DeferMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.DeferMessageOptions) error
}

// sbReceiveSettler adds ReceiveMessages so ConsumeAdvanced can read from either a
// plain receiver or a session receiver uniformly.
type sbReceiveSettler interface {
	sbMessageSettler
	ReceiveMessages(context.Context, int, *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
}

type sbReplyReceiver interface {
	ReceiveMessages(context.Context, int, *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	CompleteMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.CompleteMessageOptions) error
	AbandonMessage(context.Context, *azservicebus.ReceivedMessage, *azservicebus.AbandonMessageOptions) error
}

type inflightMessage struct {
	received *azservicebus.ReceivedMessage
	settler  sbMessageSettler
	// renew extends the lock; RenewMessageLock for plain receivers, RenewSessionLock
	// for session receivers.
	renew func(context.Context) error
	// lockedUntil is the broker-side peek-lock expiry. After it passes the lock is
	// lost server-side and the token can never be settled, so the entry is evicted
	// to avoid pinning the message (and its body) until VUser Teardown.
	lockedUntil  time.Time
	retained     int64
	sessionToken string
}

// lockedUntilOf returns a received message's peek-lock expiry, or the zero time
// when the broker did not supply one.
func lockedUntilOf(rm *azservicebus.ReceivedMessage) time.Time {
	if rm != nil && rm.LockedUntil != nil {
		return *rm.LockedUntil
	}
	return time.Time{}
}

// evictExpiredInflightLocked drops peek-locked entries whose broker lock has
// already expired. Called lazily on each new peek-lock insert so a script that
// peek-locks without settling (or whose lock expires before the settle decision)
// cannot grow a.inflight without bound. The caller must hold a.mu.
func (a *serviceBusAdapter) evictExpiredInflightLocked(now time.Time) {
	for tok, e := range a.inflight {
		if !e.lockedUntil.IsZero() && now.After(e.lockedUntil) {
			a.inflightBytes -= e.retained
			delete(a.inflight, tok)
		}
	}
	if a.inflightBytes < 0 {
		a.inflightBytes = 0
	}
}

func serviceBusReceivedMessageSize(rm *azservicebus.ReceivedMessage) int64 {
	if rm == nil {
		return 0
	}
	size := int64(len(rm.Body) + len(rm.MessageID))
	if rm.CorrelationID != nil {
		size += int64(len(*rm.CorrelationID))
	}
	for key, value := range rm.ApplicationProperties {
		size += int64(len(key))
		if text, ok := value.(string); ok {
			size += int64(len(text))
		}
	}
	return size
}

// admitInflightBatchLocked either retains the complete received batch or none
// of it. The caller can then explicitly abandon every fetched lock on failure.
func (a *serviceBusAdapter) admitInflightBatchLocked(entries map[string]inflightMessage, now time.Time) error {
	if a.closed {
		return fmt.Errorf("Service Bus adapter is closed")
	}
	if a.inflight == nil {
		a.inflight = make(map[string]inflightMessage)
	}
	a.evictExpiredInflightLocked(now)
	newCount := 0
	newBytes := int64(0)
	for token, entry := range entries {
		if existing, ok := a.inflight[token]; ok {
			newBytes += entry.retained - existing.retained
			continue
		}
		newCount++
		newBytes += entry.retained
	}
	if len(a.inflight)+newCount > maxServiceBusInflightMessages || newBytes > maxServiceBusInflightBytes-a.inflightBytes {
		return fmt.Errorf("Service Bus unsettled delivery capacity exceeded (%d messages, %d bytes)", maxServiceBusInflightMessages, maxServiceBusInflightBytes)
	}
	for token, entry := range entries {
		if existing, ok := a.inflight[token]; ok {
			a.inflightBytes -= existing.retained
		}
		a.inflight[token] = entry
		a.inflightBytes += entry.retained
	}
	return nil
}

func (a *serviceBusAdapter) deleteInflightLocked(token string) {
	if entry, ok := a.inflight[token]; ok {
		a.inflightBytes -= entry.retained
		delete(a.inflight, token)
	}
	if a.inflightBytes < 0 {
		a.inflightBytes = 0
	}
}

func serviceBusMapKeyBytes[V any](values map[string]V) int {
	total := 0
	for key := range values {
		total += len(key)
	}
	return total
}

func (a *serviceBusAdapter) receiverAdmissionErrorLocked(key string) error {
	if a.closed {
		return fmt.Errorf("Service Bus adapter is closed")
	}
	if _, exists := a.receivers[key]; exists {
		return nil
	}
	if len(a.receivers) >= maxServiceBusReceivers || len(key) > maxServiceBusReceiverKeyBytes-serviceBusMapKeyBytes(a.receivers) {
		return fmt.Errorf("Service Bus receiver capacity exceeded (%d handles, %d key bytes)", maxServiceBusReceivers, maxServiceBusReceiverKeyBytes)
	}
	return nil
}

func (a *serviceBusAdapter) Publish(ctx context.Context, destination string, msg message) (message, error) {
	sender, err := a.handle.sender(destination)
	if err != nil {
		return message{}, err
	}
	outgoing := normalizeServiceBusOutgoingMessage(destination, msg)
	sbMsg := toServiceBusMessage(destination, outgoing)
	// Scheduled enqueue goes through ScheduleMessages so the broker returns a
	// sequence number the script can later cancel (jms_cancel_scheduled).
	if !outgoing.ScheduledEnqueueAt.IsZero() {
		seqs, err := sender.ScheduleMessages(ctx, []*azservicebus.Message{sbMsg}, outgoing.ScheduledEnqueueAt, nil)
		if err != nil {
			return message{}, err
		}
		if len(seqs) > 0 {
			outgoing.SequenceNumber = seqs[0]
		}
		return outgoing, nil
	}
	if err := sender.SendMessage(ctx, sbMsg, nil); err != nil {
		return message{}, err
	}
	return outgoing, nil
}

func (a *serviceBusAdapter) Consume(ctx context.Context, destination string, timeout time.Duration) (message, error) {
	receiver, err := a.receiver(destination)
	if err != nil {
		return message{}, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	messages, err := receiver.ReceiveMessages(receiveCtx, 1, nil)
	if err != nil {
		return message{}, fmt.Errorf("no JMS message received from %q within %s: %w", destination, timeout, err)
	}
	if len(messages) == 0 {
		return message{}, fmt.Errorf("no JMS message received from %q within %s", destination, timeout)
	}
	converted := fromServiceBusMessage(destination, messages[0])
	if serviceBusReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties) != "receive_and_delete" {
		if err := receiver.CompleteMessage(ctx, messages[0], nil); err != nil {
			return converted, fmt.Errorf("complete Service Bus message from %q: %w", destination, err)
		}
	}
	return converted, nil
}

func (a *serviceBusAdapter) RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error) {
	if request.CorrelationID == "" {
		request.CorrelationID = protocol.FirstNonEmpty(request.ID, newJMSMessageID())
	}
	if request.Headers == nil {
		request.Headers = make(map[string]string)
	}
	request.Headers["reply_to"] = serviceBusReplyToDestination(replyDestination)
	sent, err := a.Publish(ctx, requestDestination, request)
	if err != nil {
		return message{sendAttempted: true}, message{}, err
	}
	sent.sent = true
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// Use a dedicated peek-lock receiver so an uncorrelated reply (e.g. another
	// VUser's reply on a shared reply queue) can be ABANDONED back to the queue
	// rather than consumed-and-lost. Consume's auto-complete would silently delete
	// it. Only the message whose correlation id matches this request is completed.
	receiver, err := a.peekLockReceiver(replyDestination)
	if err != nil {
		return sent, message{}, err
	}
	reply, err := serviceBusAwaitCorrelatedReply(ctx, receiver, replyDestination, request, timeout)
	if err != nil {
		return sent, reply, err
	}
	return sent, reply, nil
}

const (
	serviceBusReplyBackoff      = 50 * time.Millisecond
	serviceBusSettlementTimeout = 5 * time.Second
	serviceBusCloseTimeout      = 5 * time.Second
)

func serviceBusAwaitCorrelatedReply(ctx context.Context, receiver sbReplyReceiver, replyDestination string, request message, timeout time.Duration) (message, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	abandonCounts := make(map[string]int)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		receiveCtx, cancel := context.WithTimeout(ctx, remaining)
		messages, rerr := receiver.ReceiveMessages(receiveCtx, 10, nil)
		cancel()
		if rerr != nil {
			if errors.Is(rerr, context.DeadlineExceeded) && time.Now().After(deadline) {
				break
			}
			return message{}, fmt.Errorf("receive Service Bus reply from %q: %w", replyDestination, rerr)
		}
		if len(messages) == 0 {
			continue
		}
		matchIndex := -1
		var reply message
		for idx, sbMsg := range messages {
			candidate := fromServiceBusMessage(replyDestination, sbMsg)
			if messaging.CorrelationMatches(request.CorrelationID, candidate.CorrelationID) {
				matchIndex, reply = idx, candidate
				break
			}
		}
		if matchIndex >= 0 {
			settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), serviceBusSettlementTimeout)
			var settleErrs []error
			if err := receiver.CompleteMessage(settleCtx, messages[matchIndex], nil); err != nil {
				settleErrs = append(settleErrs, fmt.Errorf("complete Service Bus reply from %q: %w", replyDestination, err))
			}
			for idx, other := range messages {
				if idx == matchIndex {
					continue
				}
				if _, err := serviceBusAbandonForeignReply(settleCtx, receiver, replyDestination, other, abandonCounts); err != nil {
					settleErrs = append(settleErrs, err)
				}
			}
			settleCancel()
			if err := errors.Join(settleErrs...); err != nil {
				return reply, err
			}
			return reply, nil
		}

		sawFreshForeign, sawForeignChurn, err := serviceBusAbandonForeignBatchWithin(
			ctx, receiver, replyDestination, messages, abandonCounts, serviceBusSettlementTimeout,
		)
		if err != nil {
			return message{}, err
		}
		// Back off only when this batch was pure redelivery churn (every foreign
		// reply was one we have already abandoned), to avoid a tight
		// receive→abandon loop. The replies are always released above regardless.
		if !sawFreshForeign && sawForeignChurn {
			sleep := serviceBusReplyBackoff
			if remaining := time.Until(deadline); remaining > 0 && remaining < sleep {
				sleep = remaining
			}
			select {
			case <-ctx.Done():
				return message{}, ctx.Err()
			case <-time.After(sleep):
			}
		}
	}
	return message{}, fmt.Errorf("no correlated JMS reply received from %q within %s", replyDestination, timeout)
}

func serviceBusAbandonForeignBatchWithin(
	ctx context.Context,
	receiver sbReplyReceiver,
	replyDestination string,
	messages []*azservicebus.ReceivedMessage,
	abandonCounts map[string]int,
	timeout time.Duration,
) (sawFresh bool, sawChurn bool, err error) {
	settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer settleCancel()
	for _, sbMsg := range messages {
		fresh, abandonErr := serviceBusAbandonForeignReply(settleCtx, receiver, replyDestination, sbMsg, abandonCounts)
		if abandonErr != nil {
			return sawFresh, sawChurn, abandonErr
		}
		if fresh {
			sawFresh = true
		} else {
			sawChurn = true
		}
	}
	return sawFresh, sawChurn, nil
}

// serviceBusAbandonForeignReply always releases a foreign reply's peek lock so
// the requester whose correlation ID matches can receive it immediately. The
// boolean reports whether this is the first observation of the logical message;
// callers use repeated observations to add a bounded backoff without ever holding
// another requester's reply until the broker lock expires.
func serviceBusAbandonForeignReply(ctx context.Context, receiver sbReplyReceiver, replyDestination string, sbMsg *azservicebus.ReceivedMessage, abandonCounts map[string]int) (fresh bool, err error) {
	key := serviceBusReplyMessageKey(sbMsg)
	fresh = abandonCounts[key] == 0
	if err := receiver.AbandonMessage(ctx, sbMsg, nil); err != nil {
		return false, fmt.Errorf("abandon uncorrelated Service Bus reply from %q: %w", replyDestination, err)
	}
	abandonCounts[key]++
	return fresh, nil
}

func serviceBusReplyMessageKey(sbMsg *azservicebus.ReceivedMessage) string {
	if sbMsg == nil {
		return "nil"
	}
	if sbMsg.MessageID != "" {
		return "message_id:" + sbMsg.MessageID
	}
	if sbMsg.SequenceNumber != nil {
		return "sequence:" + strconv.FormatInt(*sbMsg.SequenceNumber, 10)
	}
	if sbMsg.CorrelationID != nil && *sbMsg.CorrelationID != "" {
		return "correlation:" + *sbMsg.CorrelationID
	}
	return "lock_token:" + hex.EncodeToString(sbMsg.LockToken[:])
}

func (a *serviceBusAdapter) HealthCheck(ctx context.Context) error {
	if a.handle == nil {
		return fmt.Errorf("Service Bus client is not initialized")
	}
	if a.cfg.Destination != "" {
		_, err := a.handle.sender(a.cfg.Destination)
		return err
	}
	return nil
}

func (a *serviceBusAdapter) Teardown(ctx context.Context) error {
	a.mu.Lock()
	a.closed = true
	receivers := a.receivers
	a.receivers = make(map[string]*azservicebus.Receiver)
	sessions := a.sessions
	a.sessions = make(map[string]*azservicebus.SessionReceiver)
	a.inflight = make(map[string]inflightMessage)
	a.inflightBytes = 0
	handle := a.handle
	a.handle = nil
	a.mu.Unlock()

	closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), serviceBusCloseTimeout)
	defer closeCancel()
	var firstErr error
	for _, receiver := range receivers {
		if receiver != nil {
			if err := receiver.Close(closeCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, sr := range sessions {
		if sr != nil {
			if err := sr.Close(closeCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := serviceBusClients.release(closeCtx, handle); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (a *serviceBusAdapter) receiver(destination string) (*azservicebus.Receiver, error) {
	return a.receiverFor(destination, "")
}

// peekLockReceiver returns a cached PeekLock-mode receiver for the destination,
// independent of the configured ack mode. RequestReply needs PeekLock so an
// uncorrelated reply can be abandoned (returned to the queue) instead of being
// auto-completed and lost; a receive_and_delete ack mode would make that impossible.
func (a *serviceBusAdapter) peekLockReceiver(destination string) (*azservicebus.Receiver, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := destination + "|__peeklock"
	if receiver := a.receivers[key]; receiver != nil {
		return receiver, nil
	}
	if err := a.receiverAdmissionErrorLocked(key); err != nil {
		return nil, err
	}
	options := &azservicebus.ReceiverOptions{ReceiveMode: azservicebus.ReceiveModePeekLock}
	var (
		receiver *azservicebus.Receiver
		err      error
	)
	if topic, subscription, ok := splitServiceBusSubscription(destination); ok {
		receiver, err = a.handle.client.NewReceiverForSubscription(topic, subscription, options)
	} else {
		receiver, err = a.handle.client.NewReceiverForQueue(destination, options)
	}
	if err != nil {
		return nil, err
	}
	a.receivers[key] = receiver
	return receiver, nil
}

// Browse performs a non-destructive read via Service Bus PeekMessages (P2.3).
// Service Bus has no server-side browse selector (selectors are admin-plane
// subscription rules), so when a selector is supplied it is applied client-side
// over a widened peek window — best-effort, documented as such.
func (a *serviceBusAdapter) Browse(ctx context.Context, destination, selectorExpr string, max int, timeout time.Duration) ([]message, error) {
	receiver, err := a.receiverFor(destination, "")
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		max = 10
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var sel *selector
	if selectorExpr != "" {
		if sel, err = compileSelector(selectorExpr); err != nil {
			return nil, err
		}
	}
	peekCount := max
	if sel != nil {
		peekCount = max * 8
		if peekCount > 250 {
			peekCount = 250
		}
	}
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// PeekMessages with no FromSequenceNumber advances an internal cursor on the
	// (cached) receiver, so repeated jms_browse calls would walk forward off the
	// queue. Pin to sequence 0 (the head of available messages) so browse is
	// repeatable-from-head, matching JMS QueueBrowser semantics.
	fromSeq := int64(0)
	peeked, err := receiver.PeekMessages(receiveCtx, peekCount, &azservicebus.PeekMessagesOptions{FromSequenceNumber: &fromSeq})
	if err != nil {
		return nil, fmt.Errorf("peek Service Bus messages from %q: %w", destination, err)
	}
	out := make([]message, 0, len(peeked))
	for _, m := range peeked {
		converted := fromServiceBusMessage(destination, m)
		if sel != nil && !sel.matches(converted.Headers, converted.Properties) {
			continue
		}
		out = append(out, converted)
		if len(out) >= max {
			break
		}
	}
	return out, nil
}

// receiverFor returns a cached receiver for a destination + optional sub-queue
// (dead_letter / transfer_dead_letter). Receivers are cached per (destination,
// subQueue) so Teardown closes them all.
func (a *serviceBusAdapter) receiverFor(destination, subQueue string) (*azservicebus.Receiver, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := destination
	if subQueue != "" {
		key = destination + "|" + subQueue
	}
	if receiver := a.receivers[key]; receiver != nil {
		return receiver, nil
	}
	if err := a.receiverAdmissionErrorLocked(key); err != nil {
		return nil, err
	}
	options := &azservicebus.ReceiverOptions{ReceiveMode: serviceBusAZReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties)}
	switch subQueue {
	case "dead_letter":
		options.SubQueue = azservicebus.SubQueueDeadLetter
	case "transfer_dead_letter":
		options.SubQueue = azservicebus.SubQueueTransfer
	}
	var (
		receiver *azservicebus.Receiver
		err      error
	)
	if topic, subscription, ok := splitServiceBusSubscription(destination); ok {
		receiver, err = a.handle.client.NewReceiverForSubscription(topic, subscription, options)
	} else {
		receiver, err = a.handle.client.NewReceiverForQueue(destination, options)
	}
	if err != nil {
		return nil, err
	}
	a.receivers[key] = receiver
	return receiver, nil
}

// ConsumeAdvanced implements settlementAdapter: receive from an optional
// dead-letter sub-queue or an accepted session and, when PeekLock is set, defer
// settlement by stashing the message under a returned token instead of
// auto-completing.
func (a *serviceBusAdapter) ConsumeAdvanced(ctx context.Context, destination string, opts consumeOptions, timeout time.Duration) (message, error) {
	var (
		src       sbReceiveSettler
		renewLock func(ctx context.Context, rm *azservicebus.ReceivedMessage) error
	)
	if opts.SessionToken != "" {
		a.mu.Lock()
		sr := a.sessions[opts.SessionToken]
		a.mu.Unlock()
		if sr == nil {
			return message{}, fmt.Errorf("unknown session token %q (accept a session with jms_session_accept first)", opts.SessionToken)
		}
		src = sr
		renewLock = func(ctx context.Context, _ *azservicebus.ReceivedMessage) error { return sr.RenewSessionLock(ctx, nil) }
	} else {
		receiver, err := a.receiverFor(destination, opts.SubQueue)
		if err != nil {
			return message{}, err
		}
		src = receiver
		renewLock = func(ctx context.Context, rm *azservicebus.ReceivedMessage) error {
			return receiver.RenewMessageLock(ctx, rm, nil)
		}
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	messages, err := src.ReceiveMessages(receiveCtx, 1, nil)
	if err != nil {
		return message{}, fmt.Errorf("no JMS message received from %q within %s: %w", destination, timeout, err)
	}
	if len(messages) == 0 {
		return message{}, fmt.Errorf("no JMS message received from %q within %s", destination, timeout)
	}
	converted := fromServiceBusMessage(destination, messages[0])
	receiveAndDelete := serviceBusReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties) == "receive_and_delete"
	if opts.PeekLock && !receiveAndDelete {
		// Defer settlement: hand the caller a token to settle later.
		rm := messages[0]
		token := hex.EncodeToString(rm.LockToken[:])
		entry := inflightMessage{received: rm, settler: src, lockedUntil: lockedUntilOf(rm), retained: serviceBusReceivedMessageSize(rm), sessionToken: opts.SessionToken, renew: func(ctx context.Context) error { return renewLock(ctx, rm) }}
		a.mu.Lock()
		admitErr := a.admitInflightBatchLocked(map[string]inflightMessage{token: entry}, time.Now())
		a.mu.Unlock()
		if admitErr != nil {
			settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), serviceBusSettlementTimeout)
			abandonErr := src.AbandonMessage(settleCtx, rm, nil)
			settleCancel()
			if abandonErr != nil {
				return converted, errors.Join(admitErr, fmt.Errorf("abandon unadmitted Service Bus delivery: %w", abandonErr))
			}
			return converted, admitErr
		}
		converted.SettlementToken = token
		return converted, nil
	}
	if !receiveAndDelete {
		if err := src.CompleteMessage(ctx, messages[0], nil); err != nil {
			return converted, fmt.Errorf("complete Service Bus message from %q: %w", destination, err)
		}
	}
	return converted, nil
}

// AcceptSession implements settlementAdapter: open a session receiver (specific
// session, or the next available) and cache it under a returned token.
func (a *serviceBusAdapter) AcceptSession(ctx context.Context, destination, sessionID string) (string, string, error) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return "", "", fmt.Errorf("Service Bus adapter is closed")
	}
	if len(a.sessions)+a.sessionReservations >= maxServiceBusSessions {
		a.mu.Unlock()
		return "", "", fmt.Errorf("Service Bus session capacity exceeded (%d)", maxServiceBusSessions)
	}
	a.sessionReservations++
	a.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			a.mu.Lock()
			a.sessionReservations--
			a.mu.Unlock()
		}
	}()
	var (
		sr  *azservicebus.SessionReceiver
		err error
	)
	if topic, subscription, ok := splitServiceBusSubscription(destination); ok {
		if sessionID == "" {
			sr, err = a.handle.client.AcceptNextSessionForSubscription(ctx, topic, subscription, nil)
		} else {
			sr, err = a.handle.client.AcceptSessionForSubscription(ctx, topic, subscription, sessionID, nil)
		}
	} else {
		if sessionID == "" {
			sr, err = a.handle.client.AcceptNextSessionForQueue(ctx, destination, nil)
		} else {
			sr, err = a.handle.client.AcceptSessionForQueue(ctx, destination, sessionID, nil)
		}
	}
	if err != nil {
		return "", "", err
	}
	token := fmt.Sprintf("sbsession-%d", a.sessionSeq.Add(1))
	a.mu.Lock()
	a.sessionReservations--
	reserved = false
	if a.closed {
		a.mu.Unlock()
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), serviceBusCloseTimeout)
		closeErr := sr.Close(closeCtx)
		closeCancel()
		return "", "", errors.Join(fmt.Errorf("Service Bus adapter closed while accepting session"), closeErr)
	}
	a.sessions[token] = sr
	a.mu.Unlock()
	return token, sr.SessionID(), nil
}

// SessionState implements settlementAdapter: get or set the session state blob.
func (a *serviceBusAdapter) SessionState(ctx context.Context, sessionToken string, state []byte, set bool) ([]byte, error) {
	a.mu.Lock()
	sr := a.sessions[sessionToken]
	a.mu.Unlock()
	if sr == nil {
		return nil, fmt.Errorf("unknown session token %q (accept a session with jms_session_accept first)", sessionToken)
	}
	if set {
		return nil, sr.SetSessionState(ctx, state, nil)
	}
	return sr.GetSessionState(ctx, nil)
}

func (a *serviceBusAdapter) CloseSession(ctx context.Context, sessionToken string) error {
	a.mu.Lock()
	sr := a.sessions[sessionToken]
	if sr != nil {
		delete(a.sessions, sessionToken)
		for token, entry := range a.inflight {
			if entry.sessionToken == sessionToken {
				a.deleteInflightLocked(token)
			}
		}
	}
	a.mu.Unlock()
	if sr == nil {
		return fmt.Errorf("unknown session token %q (already closed or never accepted)", sessionToken)
	}
	closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), serviceBusCloseTimeout)
	defer closeCancel()
	return sr.Close(closeCtx)
}

// ReceiveDeferred implements settlementAdapter: fetch deferred messages by
// sequence number. They are peek-locked, so each carries a settlement token.
func (a *serviceBusAdapter) ReceiveDeferred(ctx context.Context, destination string, sequenceNumbers []int64, timeout time.Duration) ([]message, error) {
	receiver, err := a.receiverFor(destination, "")
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	received, err := receiver.ReceiveDeferredMessages(receiveCtx, sequenceNumbers, nil)
	if err != nil {
		return nil, fmt.Errorf("receive deferred Service Bus messages from %q: %w", destination, err)
	}
	receiveAndDelete := serviceBusReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties) == "receive_and_delete"
	out := make([]message, 0, len(received))
	entries := make(map[string]inflightMessage, len(received))
	for _, rm := range received {
		rm := rm
		converted := fromServiceBusMessage(destination, rm)
		if !receiveAndDelete {
			token := hex.EncodeToString(rm.LockToken[:])
			entries[token] = inflightMessage{received: rm, settler: receiver, lockedUntil: lockedUntilOf(rm), retained: serviceBusReceivedMessageSize(rm), renew: func(ctx context.Context) error { return receiver.RenewMessageLock(ctx, rm, nil) }}
			converted.SettlementToken = token
		}
		out = append(out, converted)
	}
	if len(entries) > 0 {
		a.mu.Lock()
		admitErr := a.admitInflightBatchLocked(entries, time.Now())
		a.mu.Unlock()
		if admitErr != nil {
			settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), serviceBusSettlementTimeout)
			var abandonErrs []error
			for _, rm := range received {
				if err := receiver.AbandonMessage(settleCtx, rm, nil); err != nil {
					abandonErrs = append(abandonErrs, err)
				}
			}
			settleCancel()
			return nil, errors.Join(admitErr, errors.Join(abandonErrs...))
		}
	}
	return out, nil
}

// CancelScheduled implements settlementAdapter: cancel scheduled (future-enqueue)
// messages by sequence number.
func (a *serviceBusAdapter) CancelScheduled(ctx context.Context, destination string, sequenceNumbers []int64) error {
	sender, err := a.handle.sender(destination)
	if err != nil {
		return err
	}
	return sender.CancelScheduledMessages(ctx, sequenceNumbers, nil)
}

// Settle implements settlementAdapter: resolve a peek-locked message by token.
func (a *serviceBusAdapter) Settle(ctx context.Context, token string, disposition string, opts settleOptions) error {
	switch disposition {
	case "abandon", "dead_letter", "defer", "complete":
	default:
		return fmt.Errorf("unknown settle disposition %q", disposition)
	}
	a.mu.Lock()
	entry, ok := a.inflight[token]
	if ok {
		a.deleteInflightLocked(token)
	}
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown settlement_token %q (already settled or never peek-locked)", token)
	}
	var err error
	switch disposition {
	case "abandon":
		err = entry.settler.AbandonMessage(ctx, entry.received, nil)
	case "dead_letter":
		dlOpts := &azservicebus.DeadLetterOptions{}
		if opts.DeadLetterReason != "" {
			dlOpts.Reason = stringPtr(opts.DeadLetterReason)
		}
		if opts.DeadLetterDescription != "" {
			dlOpts.ErrorDescription = stringPtr(opts.DeadLetterDescription)
		}
		err = entry.settler.DeadLetterMessage(ctx, entry.received, dlOpts)
	case "defer":
		err = entry.settler.DeferMessage(ctx, entry.received, nil)
	case "complete":
		err = entry.settler.CompleteMessage(ctx, entry.received, nil)
	}
	if err != nil {
		return fmt.Errorf("settle Service Bus message (%s): %w", disposition, err)
	}
	return nil
}

// RenewLock implements settlementAdapter: extend the broker lock on a peek-locked
// message so a long-running settlement decision doesn't lose it.
func (a *serviceBusAdapter) RenewLock(ctx context.Context, token string) error {
	a.mu.Lock()
	entry, ok := a.inflight[token]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown settlement_token %q (already settled or never peek-locked)", token)
	}
	if entry.renew == nil {
		return fmt.Errorf("settlement_token %q has no renewable lock", token)
	}
	if err := entry.renew(ctx); err != nil {
		return fmt.Errorf("renew Service Bus message lock: %w", err)
	}
	return nil
}

type serviceBusConfigErrorAdapter struct {
	err error
}

func (a serviceBusConfigErrorAdapter) Publish(context.Context, string, message) (message, error) {
	return message{}, a.err
}

func (a serviceBusConfigErrorAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, a.err
}

func (a serviceBusConfigErrorAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, a.err
}

func (a serviceBusConfigErrorAdapter) HealthCheck(context.Context) error {
	return a.err
}

func (a serviceBusConfigErrorAdapter) Teardown(context.Context) error {
	return nil
}

// settlementAdapter capability: surface the underlying config error rather than a
// misleading "requires build tag" message (this IS the jms_servicebus_real build).
func (a serviceBusConfigErrorAdapter) ConsumeAdvanced(context.Context, string, consumeOptions, time.Duration) (message, error) {
	return message{}, a.err
}

func (a serviceBusConfigErrorAdapter) Settle(context.Context, string, string, settleOptions) error {
	return a.err
}

func (a serviceBusConfigErrorAdapter) RenewLock(context.Context, string) error {
	return a.err
}

func (a serviceBusConfigErrorAdapter) AcceptSession(context.Context, string, string) (string, string, error) {
	return "", "", a.err
}

func (a serviceBusConfigErrorAdapter) SessionState(context.Context, string, []byte, bool) ([]byte, error) {
	return nil, a.err
}

func (a serviceBusConfigErrorAdapter) CloseSession(context.Context, string) error {
	return a.err
}

func (a serviceBusConfigErrorAdapter) ReceiveDeferred(context.Context, string, []int64, time.Duration) ([]message, error) {
	return nil, a.err
}

func (a serviceBusConfigErrorAdapter) CancelScheduled(context.Context, string, []int64) error {
	return a.err
}

type serviceBusClientPool struct {
	mu        sync.Mutex
	entries   map[string]*serviceBusPoolEntry
	newClient func(serviceBusConn) (*azservicebus.Client, error)
}

type serviceBusPoolEntry struct {
	key     string
	client  *azservicebus.Client
	refs    int
	mu      sync.Mutex
	senders map[string]*azservicebus.Sender
	closed  bool
}

func newServiceBusClientPool() *serviceBusClientPool {
	return &serviceBusClientPool{
		entries:   make(map[string]*serviceBusPoolEntry),
		newClient: newServiceBusClient,
	}
}

func (p *serviceBusClientPool) acquire(conn serviceBusConn, key string) (*serviceBusPoolEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entries[key]; entry != nil {
		entry.refs++
		return entry, nil
	}
	if len(p.entries) >= maxServiceBusPoolEntries || len(key) > maxServiceBusPoolKeyBytes-serviceBusMapKeyBytes(p.entries) {
		return nil, fmt.Errorf("Service Bus client pool capacity exceeded (%d clients, %d key bytes)", maxServiceBusPoolEntries, maxServiceBusPoolKeyBytes)
	}
	factory := p.newClient
	if factory == nil {
		factory = newServiceBusClient
	}
	client, err := factory(conn)
	if err != nil {
		return nil, err
	}
	entry := &serviceBusPoolEntry{
		key:     key,
		client:  client,
		refs:    1,
		senders: make(map[string]*azservicebus.Sender),
	}
	p.entries[key] = entry
	return entry, nil
}

func (p *serviceBusClientPool) release(ctx context.Context, entry *serviceBusPoolEntry) error {
	if entry == nil {
		return nil
	}
	p.mu.Lock()
	current := p.entries[entry.key]
	if current == nil {
		p.mu.Unlock()
		return nil
	}
	current.refs--
	if current.refs > 0 {
		p.mu.Unlock()
		return nil
	}
	delete(p.entries, entry.key)
	p.mu.Unlock()
	return current.close(ctx)
}

func (e *serviceBusPoolEntry) sender(destination string) (*azservicebus.Sender, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if sender := e.senders[destination]; sender != nil {
		return sender, nil
	}
	if e.closed {
		return nil, fmt.Errorf("Service Bus client pool entry is closed")
	}
	if len(e.senders) >= maxServiceBusSenders || len(destination) > maxServiceBusSenderKeyBytes-serviceBusMapKeyBytes(e.senders) {
		return nil, fmt.Errorf("Service Bus sender capacity exceeded (%d handles, %d key bytes)", maxServiceBusSenders, maxServiceBusSenderKeyBytes)
	}
	if e.client == nil {
		return nil, fmt.Errorf("Service Bus client is not initialized")
	}
	sender, err := e.client.NewSender(destination, nil)
	if err != nil {
		return nil, err
	}
	e.senders[destination] = sender
	return sender, nil
}

func (e *serviceBusPoolEntry) close(ctx context.Context) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	senders := e.senders
	e.senders = make(map[string]*azservicebus.Sender)
	client := e.client
	e.client = nil
	e.mu.Unlock()
	closeTimeout := serviceBusCloseTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < closeTimeout {
			closeTimeout = remaining
		}
	}
	closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer closeCancel()
	var firstErr error
	for _, sender := range senders {
		if sender != nil {
			if err := sender.Close(closeCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if client != nil {
		if err := client.Close(closeCtx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func newServiceBusClient(conn serviceBusConn) (*azservicebus.Client, error) {
	if conn.ConnectionString != "" {
		return azservicebus.NewClientFromConnectionString(conn.ConnectionString, nil)
	}
	namespace, err := identitypolicy.ValidateAzureMessagingNamespace(conn.FullyQualifiedNS)
	if err != nil {
		return nil, err
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	return azservicebus.NewClient(namespace, credential, nil)
}

func serviceBusAZReceiveMode(ack string, props map[string]string) azservicebus.ReceiveMode {
	if serviceBusReceiveMode(ack, props) == "receive_and_delete" {
		return azservicebus.ReceiveModeReceiveAndDelete
	}
	return azservicebus.ReceiveModePeekLock
}

func normalizeServiceBusOutgoingMessage(destination string, msg message) message {
	if msg.ID == "" {
		msg.ID = newJMSMessageID()
	}
	if msg.Destination == "" {
		msg.Destination = destination
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	return msg
}

func toServiceBusMessage(destination string, msg message) *azservicebus.Message {
	appProps := make(map[string]any, len(msg.Properties)+len(msg.Headers))
	for k, v := range msg.Properties {
		appProps[k] = v
	}
	for k, v := range msg.Headers {
		if isServiceBusReservedHeader(k) {
			continue
		}
		appProps[k] = v
	}
	out := &azservicebus.Message{
		ApplicationProperties: appProps,
		Body:                  msg.Body,
		MessageID:             stringPtr(msg.ID),
		CorrelationID:         stringPtr(msg.CorrelationID),
		ReplyTo:               stringPtr(firstMatchingString(msg.Headers, "reply_to", "ReplyTo")),
		To:                    stringPtr(destination),
	}
	if contentType := protocol.FirstNonEmpty(
		firstMatchingString(msg.Headers, "Content-Type", "content-type", "content_type"),
		firstMatchingString(msg.Properties, "Content-Type", "content-type", "content_type"),
	); contentType != "" {
		out.ContentType = &contentType
	}
	// JMS message type (JMSType) maps to the Service Bus Subject (P2.3).
	if msg.Type != "" {
		out.Subject = stringPtr(msg.Type)
	}
	// Azure Service Bus has no native message priority and does not honor JMS
	// priority ordering. Carry it as a JMSPriority application property for
	// parity/visibility only — do NOT claim priority delivery on Service Bus.
	if msg.Priority != nil {
		out.ApplicationProperties["JMSPriority"] = int64(*msg.Priority)
	}
	// P2.4 advanced send fields: session routing, partition co-location, scheduled
	// enqueue, and TTL.
	if msg.SessionID != "" {
		out.SessionID = stringPtr(msg.SessionID)
	}
	if msg.PartitionKey != "" {
		out.PartitionKey = stringPtr(msg.PartitionKey)
	}
	if !msg.ScheduledEnqueueAt.IsZero() {
		enqueue := msg.ScheduledEnqueueAt
		out.ScheduledEnqueueTime = &enqueue
	}
	if msg.TimeToLive > 0 {
		ttl := msg.TimeToLive
		out.TimeToLive = &ttl
	}
	return out
}

func fromServiceBusMessage(destination string, msg *azservicebus.ReceivedMessage) message {
	out := message{
		received:    true,
		ID:          msg.MessageID,
		Destination: destination,
		Headers:     make(map[string]string),
		Properties:  make(map[string]string),
		Body:        msg.Body,
		CreatedAt:   time.Now(),
	}
	if msg.CorrelationID != nil {
		out.CorrelationID = *msg.CorrelationID
	}
	if msg.To != nil && *msg.To != "" {
		out.Destination = *msg.To
	}
	if msg.ContentType != nil {
		out.Headers["Content-Type"] = *msg.ContentType
	}
	// Restore the JMS message type from the Service Bus Subject so the type
	// survives a publish->consume round-trip (P2.3).
	if msg.Subject != nil && *msg.Subject != "" {
		out.Type = *msg.Subject
	}
	if msg.ReplyTo != nil {
		out.Headers["reply_to"] = *msg.ReplyTo
	}
	if msg.EnqueuedTime != nil {
		out.CreatedAt = *msg.EnqueuedTime
	}
	if msg.SessionID != nil {
		out.SessionID = *msg.SessionID
	}
	if msg.PartitionKey != nil {
		out.PartitionKey = *msg.PartitionKey
	}
	if msg.SequenceNumber != nil {
		out.SequenceNumber = *msg.SequenceNumber
	}
	for k, v := range msg.ApplicationProperties {
		out.Properties[k] = anyToString(v)
	}
	if msg.DeliveryCount > 0 {
		out.Properties["delivery_count"] = strconv.FormatUint(uint64(msg.DeliveryCount), 10)
	}
	if msg.DeadLetterReason != nil {
		out.Properties["dead_letter_reason"] = *msg.DeadLetterReason
	}
	if msg.DeadLetterErrorDescription != nil {
		out.Properties["dead_letter_error_description"] = *msg.DeadLetterErrorDescription
	}
	return out
}

func isServiceBusReservedHeader(key string) bool {
	return firstMatchingString(map[string]string{key: "x"}, "Content-Type", "content-type", "content_type", "reply_to", "ReplyTo") != ""
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
