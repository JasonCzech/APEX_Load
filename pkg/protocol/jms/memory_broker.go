// Package jms provides a provider-neutral JMS protocol implementation.
package jms

import (
	"fmt"
	"strings"
	"sync"
)

var broker = &memoryBroker{
	queues:              make(map[string][]message),
	topics:              make(map[string]map[string][]message),
	serviceBusHistory:   make(map[string][]message),
	refs:                make(map[string]int),
	queueBytes:          make(map[string]int64),
	topicBytes:          make(map[string]map[string]int64),
	serviceBusHistBytes: make(map[string]int64),
}

type memoryBroker struct {
	mu     sync.Mutex
	queues map[string][]message
	// topics models JMS topic fan-out: topic name -> subscription name -> that
	// subscription's independent message queue. A topic publish appends a copy to
	// every registered subscription; each subscription consumes its own copy with
	// an independent cursor.
	topics     map[string]map[string][]message
	queueBytes map[string]int64
	topicBytes map[string]map[string]int64
	// serviceBusHistory is a bounded topic log used only by the Service Bus
	// mock. It materializes a durable action-level subscription on first use.
	serviceBusHistory   map[string][]message
	serviceBusHistBytes map[string]int64
	totalBytes          int64
	// seqLast is a monotonic broker sequence assigned to published messages that
	// carry none, so consumers can detect duplicate / out-of-order delivery (real
	// brokers assign sequence numbers; the mock mirrors that for fidelity). A
	// redelivered message keeps its original sequence (it is non-zero), so a
	// rollback redelivery is not mistaken for a new message.
	seqLast int64
	refs    map[string]int
}

const (
	maxJMSDestinationKeys   = 2048
	maxJMSQueueDepth        = 10000
	maxJMSQueueBytes        = 16 << 20
	maxJMSRetainedBytes     = 256 << 20
	maxJMSResourceKeyBytes  = 1 << 20
	maxJMSResourceKeyLength = 16 << 10
)

func mockJMSKey(namespace, destination string) string {
	if namespace == "" {
		return destination
	}
	return namespace + "\x00" + destination
}

func (b *memoryBroker) acquire(namespace string) {
	if namespace == "" {
		return
	}
	b.mu.Lock()
	b.refs[namespace]++
	b.mu.Unlock()
}

func (b *memoryBroker) release(namespace string) {
	if namespace == "" {
		return
	}
	prefix := namespace + "\x00"
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	b.refs[namespace]--
	if b.refs[namespace] > 0 {
		return
	}
	delete(b.refs, namespace)
	for destination := range b.queues {
		if strings.HasPrefix(destination, prefix) {
			b.totalBytes -= b.queueBytes[destination]
			delete(b.queues, destination)
			delete(b.queueBytes, destination)
		}
	}
	for topic := range b.topics {
		if strings.HasPrefix(topic, prefix) {
			for _, retained := range b.topicBytes[topic] {
				b.totalBytes -= retained
			}
			delete(b.topics, topic)
			delete(b.topicBytes, topic)
		}
	}
	for topic := range b.serviceBusHistory {
		if strings.HasPrefix(topic, prefix) {
			b.totalBytes -= b.serviceBusHistBytes[topic]
			delete(b.serviceBusHistory, topic)
			delete(b.serviceBusHistBytes, topic)
		}
	}
	if b.totalBytes < 0 {
		b.totalBytes = 0
	}
}

func boundedMessageQueue(queue []message) []message {
	bounded, _ := boundedMessageQueueStats(queue)
	return bounded
}

func boundedMessageQueueStats(queue []message) ([]message, int64) {
	bytes := 0
	keepFrom := len(queue)
	for keepFrom > 0 && len(queue)-keepFrom < maxJMSQueueDepth {
		size := messageSize(queue[keepFrom-1])
		if bytes+size > maxJMSQueueBytes {
			break
		}
		bytes += size
		keepFrom--
	}
	if keepFrom == 0 {
		return queue, int64(bytes)
	}
	retained := make([]message, len(queue)-keepFrom)
	copy(retained, queue[keepFrom:])
	return retained, int64(bytes)
}

func messageSize(msg message) int {
	size := len(msg.Body) + len(msg.ID) + len(msg.CorrelationID) + len(msg.Destination) +
		len(msg.SessionID) + len(msg.PartitionKey) + len(msg.SettlementToken) +
		len(msg.Type) + len(msg.DeliveryMode) + len(msg.sourceTopic) + len(msg.sourceSubscription)
	for key, value := range msg.Headers {
		size += len(key) + len(value)
	}
	for key, value := range msg.Properties {
		size += len(key) + len(value)
	}
	for key, value := range msg.MapBody {
		size += len(key) + len(value)
	}
	for _, value := range msg.StreamBody {
		size += len(value)
	}
	return size
}

func (b *memoryBroker) resourceKeyCountLocked() int {
	count := len(b.queues) + len(b.topics)
	for topic := range b.serviceBusHistory {
		if _, exists := b.topics[topic]; !exists {
			count++
		}
	}
	for _, subscriptions := range b.topics {
		count += len(subscriptions)
	}
	return count
}

func (b *memoryBroker) resourceKeyBytesLocked() int {
	total := 0
	for destination := range b.queues {
		total += len(destination)
	}
	for topic, subscriptions := range b.topics {
		total += len(topic)
		for subscription := range subscriptions {
			total += len(subscription)
		}
	}
	for topic := range b.serviceBusHistory {
		if _, exists := b.topics[topic]; !exists {
			total += len(topic)
		}
	}
	return total
}

func (b *memoryBroker) validateNewResourceKeysLocked(keys ...string) error {
	additional := 0
	for _, key := range keys {
		if len(key) > maxJMSResourceKeyLength {
			return fmt.Errorf("mock JMS resource key exceeds %d bytes", maxJMSResourceKeyLength)
		}
		additional += len(key)
	}
	if additional > maxJMSResourceKeyBytes-b.resourceKeyBytesLocked() {
		return fmt.Errorf("mock JMS resource key capacity exceeded (%d bytes)", maxJMSResourceKeyBytes)
	}
	return nil
}

func (b *memoryBroker) ensureAccountingLocked() {
	if b.serviceBusHistory == nil {
		b.serviceBusHistory = make(map[string][]message)
	}
	if b.queueBytes != nil && b.topicBytes != nil && b.serviceBusHistBytes != nil {
		return
	}
	// Some focused tests seed queue/topic accounting and totalBytes directly.
	// Initialize the new history ledger without discarding that existing state.
	if b.queueBytes != nil && b.topicBytes != nil && b.serviceBusHistBytes == nil {
		b.serviceBusHistBytes = make(map[string]int64, len(b.serviceBusHistory))
		for topic, queue := range b.serviceBusHistory {
			bounded, retained := boundedMessageQueueStats(queue)
			b.serviceBusHistory[topic] = bounded
			b.serviceBusHistBytes[topic] = retained
			b.totalBytes += retained
		}
		return
	}
	b.queueBytes = make(map[string]int64, len(b.queues))
	b.topicBytes = make(map[string]map[string]int64, len(b.topics))
	b.serviceBusHistBytes = make(map[string]int64, len(b.serviceBusHistory))
	b.totalBytes = 0
	for destination, queue := range b.queues {
		bounded, retained := boundedMessageQueueStats(queue)
		b.queues[destination] = bounded
		b.queueBytes[destination] = retained
		b.totalBytes += retained
	}
	for topic, subscriptions := range b.topics {
		b.topicBytes[topic] = make(map[string]int64, len(subscriptions))
		for subscription, queue := range subscriptions {
			bounded, retained := boundedMessageQueueStats(queue)
			b.topics[topic][subscription] = bounded
			b.topicBytes[topic][subscription] = retained
			b.totalBytes += retained
		}
	}
	for topic, queue := range b.serviceBusHistory {
		bounded, retained := boundedMessageQueueStats(queue)
		b.serviceBusHistory[topic] = bounded
		b.serviceBusHistBytes[topic] = retained
		b.totalBytes += retained
	}
}

func (b *memoryBroker) setQueueLocked(destination string, queue []message, retained int64) {
	b.totalBytes += retained - b.queueBytes[destination]
	b.queues[destination] = queue
	b.queueBytes[destination] = retained
}

func (b *memoryBroker) setTopicQueueLocked(topic, subscription string, queue []message, retained int64) {
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[string][]message)
	}
	if b.topicBytes[topic] == nil {
		b.topicBytes[topic] = make(map[string]int64)
	}
	b.totalBytes += retained - b.topicBytes[topic][subscription]
	b.topics[topic][subscription] = queue
	b.topicBytes[topic][subscription] = retained
}

func (b *memoryBroker) setServiceBusHistoryLocked(topic string, queue []message, retained int64) {
	b.totalBytes += retained - b.serviceBusHistBytes[topic]
	b.serviceBusHistory[topic] = queue
	b.serviceBusHistBytes[topic] = retained
}

// assignSequenceLocked stamps a monotonic sequence on a message that doesn't
// already carry one. The caller must hold b.mu.
func (b *memoryBroker) assignSequenceLocked(msg *message) {
	if msg.SequenceNumber == 0 {
		b.seqLast++
		msg.SequenceNumber = b.seqLast
	}
}

// cloneMessageValue returns a copy of msg with its reference fields (maps, body)
// deep-copied so callers cannot mutate shared broker state.
func cloneMessageValue(msg message) message {
	out := msg
	out.Headers = cloneStringMap(msg.Headers)
	out.Properties = cloneStringMap(msg.Properties)
	if msg.MapBody != nil {
		out.MapBody = cloneStringMap(msg.MapBody)
	}
	if msg.StreamBody != nil {
		out.StreamBody = append([]string(nil), msg.StreamBody...)
	}
	if msg.Body != nil {
		out.Body = append([]byte(nil), msg.Body...)
	}
	return out
}

func (b *memoryBroker) publish(destination string, msg message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	if _, exists := b.queues[destination]; !exists {
		if b.resourceKeyCountLocked() >= maxJMSDestinationKeys {
			return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
		}
		if err := b.validateNewResourceKeysLocked(destination); err != nil {
			return err
		}
	}
	msgBytes := int64(messageSize(msg))
	if msgBytes > maxJMSQueueBytes {
		return fmt.Errorf("mock JMS message exceeds per-destination capacity (%d bytes)", maxJMSQueueBytes)
	}
	if msgBytes > maxJMSRetainedBytes-b.totalBytes {
		return fmt.Errorf("mock JMS aggregate retention capacity exceeded (%d bytes)", maxJMSRetainedBytes)
	}
	b.assignSequenceLocked(&msg)
	queue, retained := boundedMessageQueueStats(append(b.queues[destination], msg))
	delta := retained - b.queueBytes[destination]
	if delta > maxJMSRetainedBytes-b.totalBytes {
		return fmt.Errorf("mock JMS aggregate retention capacity exceeded (%d bytes)", maxJMSRetainedBytes)
	}
	b.setQueueLocked(destination, queue, retained)
	return nil
}

func (b *memoryBroker) consume(destination string) (message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	queue := b.queues[destination]
	if len(queue) == 0 {
		return message{}, false
	}
	msg := queue[0]
	copy(queue, queue[1:])
	queue[len(queue)-1] = message{}
	queue = queue[:len(queue)-1]
	b.setQueueLocked(destination, queue, b.queueBytes[destination]-int64(messageSize(msg)))
	return msg, true
}

func (b *memoryBroker) hasMessages(destination string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queues[destination]) > 0
}

func (b *memoryBroker) hasMatching(destination string, match func(message) bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, msg := range b.queues[destination] {
		if match == nil || match(msg) {
			return true
		}
	}
	return false
}

// consumeMatching pops the first message satisfying the selector (a nil selector
// matches the head, preserving plain-FIFO behavior). Non-matching messages stay
// in the queue for other consumers.
func (b *memoryBroker) consumeMatching(destination string, sel *selector) (message, bool) {
	return b.consumeWhere(destination, func(msg message) bool {
		return sel.matches(msg.Headers, msg.Properties)
	})
}

func (b *memoryBroker) consumeWhere(destination string, match func(message) bool) (message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	queue := b.queues[destination]
	for i, msg := range queue {
		if match == nil || match(msg) {
			copy(queue[i:], queue[i+1:])
			queue[len(queue)-1] = message{}
			retainedQueue := queue[:len(queue)-1]
			b.setQueueLocked(destination, retainedQueue, b.queueBytes[destination]-int64(messageSize(msg)))
			return msg, true
		}
	}
	return message{}, false
}

// browse returns copies of the messages matching the selector without removing
// them (JMS QueueBrowser semantics). max <= 0 means no limit.
func (b *memoryBroker) browse(destination string, sel *selector, max int) []message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return browseMatching(b.queues[destination], sel, max)
}

// browseMatching returns deep copies of the messages in queue matching sel, so a
// browsed message can never alias (and thus mutate) broker state. max <= 0 means
// no limit.
func browseMatching(queue []message, sel *selector, max int) []message {
	var out []message
	for _, msg := range queue {
		if sel.matches(msg.Headers, msg.Properties) {
			out = append(out, cloneMessageValue(msg))
			if max > 0 && len(out) >= max {
				break
			}
		}
	}
	return out
}

// subscribe registers a topic subscription (idempotently) so subsequent topic
// publishes fan out to it. A subscription registered at connect time (Init, when
// config.jms.subscription is set) collects every later publish; one registered
// lazily on first consume only collects publishes after that point. The mock has
// no long-lived connection, so it does not model a durable subscriber collecting
// messages published before it ever registered.
func (b *memoryBroker) subscribe(topic, subscription string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	if subscriptions := b.topics[topic]; subscriptions != nil {
		if _, exists := subscriptions[subscription]; exists {
			return nil
		}
		if b.resourceKeyCountLocked() >= maxJMSDestinationKeys {
			return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
		}
		if err := b.validateNewResourceKeysLocked(subscription); err != nil {
			return err
		}
		subscriptions[subscription] = nil
		b.topicBytes[topic][subscription] = 0
		return nil
	}
	if _, retainedTopicExists := b.serviceBusHistory[topic]; retainedTopicExists {
		if b.resourceKeyCountLocked() >= maxJMSDestinationKeys {
			return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
		}
		if err := b.validateNewResourceKeysLocked(subscription); err != nil {
			return err
		}
		b.topics[topic] = map[string][]message{subscription: nil}
		b.topicBytes[topic] = map[string]int64{subscription: 0}
		return nil
	}
	// A new topic consumes one key and its first subscription consumes another.
	if b.resourceKeyCountLocked()+2 > maxJMSDestinationKeys {
		return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
	}
	if err := b.validateNewResourceKeysLocked(topic, subscription); err != nil {
		return err
	}
	b.topics[topic] = map[string][]message{subscription: nil}
	b.topicBytes[topic] = map[string]int64{subscription: 0}
	return nil
}

// publishTopic fans a message out to every registered subscription on the topic,
// each receiving its own deep copy. A topic with no subscriptions drops the
// message (standard topic semantics — only subscribers receive).
func (b *memoryBroker) publishTopic(topic string, msg message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	msgBytes := int64(messageSize(msg))
	if msgBytes > maxJMSQueueBytes {
		return fmt.Errorf("mock JMS message exceeds per-destination capacity (%d bytes)", maxJMSQueueBytes)
	}
	subscriptionCount := int64(len(b.topics[topic]))
	if subscriptionCount > 0 && msgBytes > (maxJMSRetainedBytes-b.totalBytes)/subscriptionCount {
		return fmt.Errorf("mock JMS topic fanout exceeds aggregate retention capacity (%d bytes)", maxJMSRetainedBytes)
	}
	b.assignSequenceLocked(&msg)
	for sub := range b.topics[topic] {
		queue, retained := boundedMessageQueueStats(append(b.topics[topic][sub], cloneMessageValue(msg)))
		b.setTopicQueueLocked(topic, sub, queue, retained)
	}
	return nil
}

func appendBoundedMessage(queue []message, msg message) ([]message, int64) {
	// Copy before appending so a rejected aggregate-capacity check cannot mutate
	// the backing array of the live broker queue.
	candidate := make([]message, len(queue), len(queue)+1)
	copy(candidate, queue)
	candidate = append(candidate, cloneMessageValue(msg))
	return boundedMessageQueueStats(candidate)
}

// publishServiceBusTopic records one bounded history copy and independently
// fans out to every currently materialized subscription. The history is used
// only when a Service Bus mock subscription is first referenced by an action.
func (b *memoryBroker) publishServiceBusTopic(topic string, msg message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()

	_, hasHistory := b.serviceBusHistory[topic]
	_, hasTopic := b.topics[topic]
	if !hasHistory && !hasTopic {
		if b.resourceKeyCountLocked() >= maxJMSDestinationKeys {
			return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
		}
		if err := b.validateNewResourceKeysLocked(topic); err != nil {
			return err
		}
	}
	msgBytes := int64(messageSize(msg))
	if msgBytes > maxJMSQueueBytes {
		return fmt.Errorf("mock JMS message exceeds per-destination capacity (%d bytes)", maxJMSQueueBytes)
	}
	b.assignSequenceLocked(&msg)

	history, historyBytes := appendBoundedMessage(b.serviceBusHistory[topic], msg)
	delta := historyBytes - b.serviceBusHistBytes[topic]
	type pendingQueue struct {
		subscription string
		messages     []message
		bytes        int64
	}
	pending := make([]pendingQueue, 0, len(b.topics[topic]))
	for subscription, queue := range b.topics[topic] {
		messages, retained := appendBoundedMessage(queue, msg)
		delta += retained - b.topicBytes[topic][subscription]
		pending = append(pending, pendingQueue{subscription: subscription, messages: messages, bytes: retained})
	}
	if delta > maxJMSRetainedBytes-b.totalBytes {
		return fmt.Errorf("mock JMS aggregate retention capacity exceeded (%d bytes)", maxJMSRetainedBytes)
	}
	b.setServiceBusHistoryLocked(topic, history, historyBytes)
	for _, queue := range pending {
		b.setTopicQueueLocked(topic, queue.subscription, queue.messages, queue.bytes)
	}
	return nil
}

// subscribeServiceBus materializes an action-level durable subscription from
// the bounded topic history. Registration is idempotent: repeated consumes do
// not replay history, while a distinct subscription receives an independent
// deep copy.
func (b *memoryBroker) subscribeServiceBus(topic, subscription string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	if subscriptions := b.topics[topic]; subscriptions != nil {
		if _, exists := subscriptions[subscription]; exists {
			return nil
		}
	}
	_, hasHistory := b.serviceBusHistory[topic]
	_, hasTopic := b.topics[topic]
	additionalKeys := 1
	keys := []string{subscription}
	if !hasHistory && !hasTopic {
		additionalKeys++
		keys = append([]string{topic}, keys...)
	}
	if b.resourceKeyCountLocked()+additionalKeys > maxJMSDestinationKeys {
		return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
	}
	if err := b.validateNewResourceKeysLocked(keys...); err != nil {
		return err
	}

	history := b.serviceBusHistory[topic]
	queue := make([]message, len(history))
	for i := range history {
		queue[i] = cloneMessageValue(history[i])
	}
	retained := b.serviceBusHistBytes[topic]
	if retained > maxJMSRetainedBytes-b.totalBytes {
		return fmt.Errorf("mock JMS aggregate retention capacity exceeded (%d bytes)", maxJMSRetainedBytes)
	}
	b.setTopicQueueLocked(topic, subscription, queue, retained)
	return nil
}

// requeueTopic re-appends a rolled-back message to a specific subscription's
// queue for redelivery. Unlike publishTopic it targets only the subscription the
// message was consumed from, so a transacted/client rollback redelivers to that
// subscription rather than fanning out to every subscriber.
func (b *memoryBroker) requeueTopic(topic, subscription string, msg message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	msgBytes := int64(messageSize(msg))
	if msgBytes > maxJMSQueueBytes {
		return fmt.Errorf("mock JMS message exceeds per-destination capacity (%d bytes)", maxJMSQueueBytes)
	}
	if b.topics[topic] == nil {
		if b.resourceKeyCountLocked()+2 > maxJMSDestinationKeys {
			return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
		}
		if err := b.validateNewResourceKeysLocked(topic, subscription); err != nil {
			return err
		}
	} else if _, exists := b.topics[topic][subscription]; !exists {
		if b.resourceKeyCountLocked() >= maxJMSDestinationKeys {
			return fmt.Errorf("mock JMS destination capacity exceeded (%d keys)", maxJMSDestinationKeys)
		}
		if err := b.validateNewResourceKeysLocked(subscription); err != nil {
			return err
		}
	}
	if msgBytes > maxJMSRetainedBytes-b.totalBytes {
		return fmt.Errorf("mock JMS aggregate retention capacity exceeded (%d bytes)", maxJMSRetainedBytes)
	}
	queue, retained := boundedMessageQueueStats(append(b.topics[topic][subscription], cloneMessageValue(msg)))
	delta := retained
	if b.topicBytes[topic] != nil {
		delta -= b.topicBytes[topic][subscription]
	}
	if delta > maxJMSRetainedBytes-b.totalBytes {
		return fmt.Errorf("mock JMS aggregate retention capacity exceeded (%d bytes)", maxJMSRetainedBytes)
	}
	b.setTopicQueueLocked(topic, subscription, queue, retained)
	return nil
}

// consumeTopic pops the first message matching the selector from a subscription's
// independent queue.
func (b *memoryBroker) consumeTopic(topic, subscription string, sel *selector) (message, bool) {
	return b.consumeTopicWhere(topic, subscription, func(msg message) bool {
		return sel.matches(msg.Headers, msg.Properties)
	})
}

func (b *memoryBroker) consumeTopicWhere(topic, subscription string, match func(message) bool) (message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureAccountingLocked()
	if b.topics[topic] == nil {
		return message{}, false
	}
	queue := b.topics[topic][subscription]
	for i, m := range queue {
		if match == nil || match(m) {
			copy(queue[i:], queue[i+1:])
			queue[len(queue)-1] = message{}
			retainedQueue := queue[:len(queue)-1]
			b.setTopicQueueLocked(topic, subscription, retainedQueue, b.topicBytes[topic][subscription]-int64(messageSize(m)))
			return m, true
		}
	}
	return message{}, false
}

func (b *memoryBroker) hasTopicMatching(topic, subscription string, match func(message) bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, msg := range b.topics[topic][subscription] {
		if match == nil || match(msg) {
			return true
		}
	}
	return false
}

// browseTopic returns deep copies of a subscription's matching messages without
// consuming them.
func (b *memoryBroker) browseTopic(topic, subscription string, sel *selector, max int) []message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.topics[topic] == nil {
		return nil
	}
	return browseMatching(b.topics[topic][subscription], sel, max)
}
