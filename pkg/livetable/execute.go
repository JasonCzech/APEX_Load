package livetable

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

func (t *Table) Execute(ctx context.Context, r Request) (result Result, resultErr error) {
	t.activeAttempts.Add(1)
	started := time.Now()
	defer func() {
		t.observeOperation(time.Since(started), resultErr)
		t.observeEvidence(r.Operation, result, resultErr)
		t.activeAttempts.Add(-1)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	t.mu.Lock()
	if err := ctx.Err(); err != nil {
		t.mu.Unlock()
		return Result{}, err
	}
	if err := t.checkServing(); err != nil {
		t.mu.Unlock()
		return Result{}, err
	}
	if err := t.validateRequest(r); err != nil {
		t.mu.Unlock()
		return Result{}, err
	}
	// Copy request ownership at the boundary. A caller must not mutate request
	// maps concurrently with dispatch, but may safely reuse them after return.
	if requestBytes(r) > t.s.Limits.MaxRequestBytes {
		t.mu.Unlock()
		return Result{}, fail(CodeCapacity, "request byte limit exceeded")
	}
	b, _ := json.Marshal(r)
	if len(b) > t.s.Limits.MaxRequestBytes {
		t.mu.Unlock()
		return Result{}, fail(CodeCapacity, "encoded request byte limit exceeded")
	}
	_ = json.Unmarshal(b, &r)
	digest, _ := requestDigest(r)
	now := t.now().UTC()
	if err := t.expire(ctx, now); err != nil {
		t.mu.Unlock()
		return Result{}, err
	}
	if !isRead(r.Operation) {
		key := requestKey(r.ClientID, r.Sequence)
		if old, ok := t.s.Receipts[key]; ok {
			protected := t.receiptProtected(old, now)
			t.mu.Unlock()
			if old.Digest != digest {
				return Result{}, fail(CodeConflict, "operation identity was reused with a different request")
			}
			if !protected {
				return Result{}, fail(CodeExpired, "operation retry retention has expired")
			}
			if old.Error != nil {
				return cloneResult(old.Result), cloneError(old.Error)
			}
			return cloneResult(old.Result), nil
		}
		if old, ok := t.pending[key]; ok {
			if old.digest != digest {
				t.mu.Unlock()
				return Result{}, fail(CodeConflict, "pending operation identity was reused with a different request")
			}
			t.mu.Unlock()
			return t.await(ctx, old, false)
		}
		if r.Sequence <= t.s.Clients[r.ClientID] {
			t.mu.Unlock()
			return Result{}, fail(CodeExpired, "operation sequence was retired and cannot execute again")
		}
		if _, ok := t.pendingClients[r.ClientID]; ok {
			t.mu.Unlock()
			return Result{}, fail(CodeSequence, "serialize outstanding mutations for this client")
		}
		if _, ok := t.s.Clients[r.ClientID]; !ok && len(t.s.Clients)+len(t.pendingClients) >= t.s.Limits.MaxClients {
			t.mu.Unlock()
			return Result{}, fail(CodeCapacity, "client retirement budget exhausted")
		}
		if err := t.receiptCapacity(now, r); err != nil {
			t.mu.Unlock()
			return Result{}, err
		}
	}
	// Older eligible waiters run before a competing newly admitted consumer.
	if err := t.fulfill(ctx, now); err != nil {
		t.mu.Unlock()
		return Result{}, err
	}
	result, d, err := t.evaluate(r, now)
	if err != nil {
		t.mu.Unlock()
		return Result{}, err
	}
	if result.Status == StatusEmpty && r.Wait > 0 {
		if len(t.waiters) >= t.s.Limits.MaxWaiters {
			t.mu.Unlock()
			return Result{}, fail(CodeCapacity, "waiter budget exhausted")
		}
		p := &pending{request: r, digest: digest, deadline: now.Add(r.Wait), done: make(chan struct{})}
		t.waiters = append(t.waiters, p)
		if !isRead(r.Operation) {
			key := requestKey(r.ClientID, r.Sequence)
			t.pending[key] = p
			t.pendingClients[r.ClientID] = key
		}
		t.mu.Unlock()
		return t.await(ctx, p, true)
	}
	result, err = t.commitResult(ctx, r, digest, result, d, nil, now)
	if err == nil {
		// This caller's result is already committed. A later waiter's ambiguous
		// commit closes serving, but cannot replace this exact receipt/outcome.
		_ = t.fulfill(ctx, now)
	}
	t.mu.Unlock()
	return cloneResult(result), err
}

func cloneError(e *Error) *Error {
	if e == nil {
		return nil
	}
	copy := *e
	return &copy
}

func (t *Table) commitResult(ctx context.Context, r Request, digest string, result Result, d transition, outcomeErr *Error, now time.Time) (Result, error) {
	if resultBytes(result) > t.s.Limits.MaxResultBytes {
		return Result{}, fail(CodeCapacity, "result byte limit exceeded before mutation")
	}
	if isRead(r.Operation) {
		encoded, _ := json.Marshal(result)
		if len(encoded) > t.s.Limits.MaxResultBytes {
			return Result{}, fail(CodeCapacity, "encoded result byte limit exceeded")
		}
		return result, outcomeErrOrNil(outcomeErr)
	}
	result.Revision = t.s.Revision + 1
	encoded, _ := json.Marshal(result)
	if len(encoded) > t.s.Limits.MaxResultBytes {
		return Result{}, fail(CodeCapacity, "encoded result byte limit exceeded before mutation")
	}
	rec := &receipt{ClientID: r.ClientID, Sequence: r.Sequence, Digest: digest, ExpiresAt: now.Add(t.s.Limits.ReceiptRetention), Result: cloneResult(result), Error: cloneError(outcomeErr)}
	rec.Bytes = int64(len(encoded) + len(r.ClientID) + len(digest) + 256)
	if outcomeErr != nil {
		rec.Bytes += int64(len(outcomeErr.Message) + 64)
	}
	for key, old := range t.s.Receipts {
		if !t.receiptProtected(old, now) {
			d.Retire = append(d.Retire, key)
		}
	}
	receiptCount, receiptBytes := t.receiptReservationUsage(now, r, &d, rec.Bytes, false)
	if receiptCount > t.s.Limits.MaxReceipts || receiptBytes > t.s.Limits.MaxReceiptBytes {
		return Result{}, fail(CodeCapacity, "protected operation receipt or lease settlement budget exhausted")
	}
	clients := len(t.s.Clients)
	if _, ok := t.s.Clients[r.ClientID]; !ok {
		clients++
	}
	cursors := len(t.s.Cursors)
	for cursor := range d.Cursors {
		if _, ok := t.s.Cursors[cursor]; !ok {
			cursors++
		}
	}
	// Keep every admitted state checkpointable. These conservative metadata
	// reservations include JSON keys, sequence tombstones, queue order and
	// structure overhead; data costs already include JSON string escaping.
	settlementClients := t.s.Counts.Leased
	if d.Counts != nil {
		settlementClients = d.Counts.Leased
	}
	for key, pending := range t.pending {
		if key != requestKey(r.ClientID, r.Sequence) && pending.request.Operation == OpLease {
			settlementClients++
		}
	}
	checkpointBound := d.DataBytes + receiptBytes + (int64(clients+len(t.pendingClients))+int64(settlementClients))*2048 + int64(cursors)*2048 + int64(receiptCount)*1024 + int64(len(t.s.Columns))*2048 + 16384
	if checkpointBound > int64(t.s.Limits.MaxSnapshotBytes) {
		return Result{}, fail(CodeCapacity, "checkpoint reservation budget exhausted")
	}
	d.Receipt = rec
	if err := t.commit(ctx, d, now); err != nil {
		return Result{}, err
	}
	return result, outcomeErrOrNil(outcomeErr)
}

func outcomeErrOrNil(e *Error) error {
	if e == nil {
		return nil
	}
	return e
}

func (t *Table) commit(ctx context.Context, d transition, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.s.Revision == ^uint64(0) {
		return fail(CodeCapacity, "table revision exhausted")
	}
	payload, err := json.Marshal(t.compactTransition(d))
	if err != nil {
		return fail(CodeInvalid, "transition encoding failed")
	}
	if len(payload) > t.s.Limits.MaxEntryBytes {
		return fail(CodeCapacity, "transition byte budget exceeded")
	}
	entry := Entry{TableID: t.s.ID, PreviousRevision: t.s.Revision, Revision: t.s.Revision + 1, At: now, Payload: payload}
	if t.committer != nil {
		if err = t.committer.Commit(ctx, entry); err != nil {
			t.unavailable = true
			uncertain := &Error{Code: CodeUnavailable, Message: "durable commit failed; authoritative recovery is required", OutcomeUnknown: true}
			t.failWaiters(uncertain)
			return uncertain
		}
	}
	t.apply(d, entry.Revision)
	return nil
}

func (t *Table) apply(d transition, revision uint64) {
	if t.leaseDeadlines == nil {
		t.leaseDeadlines = map[string]time.Time{}
	}
	if d.DiscardAll {
		t.s.Records = map[string]record{}
		t.s.Order = nil
		for col := range t.s.Data {
			t.s.Data[col] = nil
		}
		t.leaseDeadlines = map[string]time.Time{}
	}
	if d.ReleaseOnClose {
		for id, rec := range t.s.Records {
			if rec.Lease != nil {
				rec.Lease = nil
				rec.Row.Version++
				t.s.Records[id] = rec
			}
		}
		t.leaseDeadlines = map[string]time.Time{}
	}
	for _, patch := range d.RecordPatches {
		rec := t.s.Records[patch.ID]
		rec.Row.Version = patch.Version
		rec.Lease, rec.Attempts, rec.Quarantined = patch.Lease, patch.Attempts, patch.Quarantined
		d.Upsert = append(d.Upsert, rec)
	}
	for _, id := range d.Delete {
		delete(t.s.Records, id)
		delete(t.leaseDeadlines, id)
	}
	for _, rec := range d.Upsert {
		t.s.Records[rec.Row.ID] = rec
		if rec.Lease == nil {
			delete(t.leaseDeadlines, rec.Row.ID)
		} else {
			t.leaseDeadlines[rec.Row.ID] = rec.Lease.ExpiresAt
		}
	}
	t.s.Order = append(t.s.Order, d.Append...)
	if len(t.s.Order) > 1024 && len(t.s.Order) > 2*len(t.s.Records) {
		order := make([]string, 0, len(t.s.Records))
		for _, id := range t.s.Order {
			if _, exists := t.s.Records[id]; exists {
				order = append(order, id)
			}
		}
		t.s.Order = order
	}
	for col, cells := range d.ColumnData {
		t.s.Data[col] = cells
	}
	for _, splice := range d.ColumnSplices {
		if _, materialized := d.ColumnData[splice.Column]; !materialized {
			t.s.Data[splice.Column] = splice.apply(t.s.Data[splice.Column])
		}
	}
	if d.Columns != nil {
		t.s.Columns = d.Columns
	}
	for cursor, value := range d.Cursors {
		t.s.Cursors[cursor] = value
	}
	for _, key := range d.Retire {
		if rec, ok := t.s.Receipts[key]; ok {
			t.s.ReceiptBytes -= rec.Bytes
			delete(t.s.Receipts, key)
			delete(t.recoveryReceipts, key)
		}
	}
	if rec := d.Receipt; rec != nil {
		t.s.Receipts[requestKey(rec.ClientID, rec.Sequence)] = *rec
		t.s.ReceiptBytes += rec.Bytes
		t.s.Clients[rec.ClientID] = rec.Sequence
	}
	if d.Counts != nil {
		t.s.Counts = *d.Counts
	}
	t.s.DataBytes = d.DataBytes
	t.s.Fields = d.Fields
	t.s.Revision = revision
	if d.Close {
		t.s.Closed = true
	}
}

func (t *Table) expire(ctx context.Context, now time.Time) error {
	if t.s.Profile != ProfileNative {
		return nil
	}
	d := transition{FormatVersion: formatVersion}
	counts := t.s.Counts
	commitExpired := func() error {
		if len(d.Upsert) == 0 {
			return nil
		}
		d.Counts = &counts
		d.DataBytes, d.Fields = t.measureData(&d)
		if err := t.commit(ctx, d, now); err != nil {
			return err
		}
		d = transition{FormatVersion: formatVersion}
		return nil
	}
	ids := make([]string, 0)
	for id, deadline := range t.leaseDeadlines {
		if !deadline.After(now) {
			if token, held := t.recoveryHolds[id]; held && t.s.Records[id].Lease != nil && t.s.Records[id].Lease.Token == token && t.now().Before(t.recoveryHoldUntil) {
				continue
			}
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		rec := t.s.Records[id]
		if rec.Lease == nil || rec.Lease.ExpiresAt.After(now) {
			continue
		}
		rec = copyRecord(rec)
		rec.Lease = nil
		rec.Row.Version++
		counts.Leased--
		if rec.Attempts >= t.s.Limits.MaxDeliveryAttempts {
			rec.Quarantined = true
			counts.Quarantined++
		} else {
			counts.Available++
		}
		d.Upsert = append(d.Upsert, rec)
		if len(d.Upsert) == 128 {
			if err := commitExpired(); err != nil {
				return err
			}
		}
	}
	return commitExpired()
}

func (t *Table) fulfill(ctx context.Context, now time.Time) error {
	for i := 0; i < len(t.waiters); {
		p := t.waiters[i]
		var result Result
		var d transition
		var err error
		if !p.deadline.After(now) {
			d = transition{FormatVersion: formatVersion, DataBytes: t.s.DataBytes, Fields: t.s.Fields}
			result = Result{Status: StatusEmpty}
			result, err = t.commitResult(ctx, p.request, p.digest, result, d, fail(CodeDeadline, "table wait deadline expired"), now)
		} else {
			result, d, err = t.evaluate(p.request, now)
			if err == nil && result.Status == StatusEmpty {
				i++
				continue
			}
			if err == nil {
				result, err = t.commitResult(ctx, p.request, p.digest, result, d, nil, now)
			}
		}
		if t.unavailable {
			return err
		}
		t.finishPending(i, p, result, err)
	}
	return nil
}

func (t *Table) finishPending(index int, p *pending, result Result, err error) {
	p.result = cloneResult(result)
	p.err = err
	if !isRead(p.request.Operation) {
		delete(t.pending, requestKey(p.request.ClientID, p.request.Sequence))
		delete(t.pendingClients, p.request.ClientID)
	}
	t.waiters = append(t.waiters[:index], t.waiters[index+1:]...)
	close(p.done)
}

func (t *Table) failWaiters(err error) {
	for len(t.waiters) > 0 {
		t.finishPending(0, t.waiters[0], Result{}, err)
	}
}

func (t *Table) await(ctx context.Context, p *pending, initiator bool) (Result, error) {
	started := time.Now()
	defer func() { t.observeWait(time.Since(started)) }()
	for {
		t.mu.Lock()
		delay := p.deadline.Sub(t.now())
		for id, deadline := range t.leaseDeadlines {
			until := deadline.Sub(t.now())
			if token, held := t.recoveryHolds[id]; held && t.s.Records[id].Lease != nil && t.s.Records[id].Lease.Token == token && t.now().Before(t.recoveryHoldUntil) {
				until = max(until, t.recoveryHoldUntil.Sub(t.now()))
			}
			if until < delay {
				delay = until
			}
		}
		t.mu.Unlock()
		if delay < time.Millisecond {
			delay = time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-p.done:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return cloneResult(p.result), p.err
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if !initiator {
				return Result{}, ctx.Err()
			}
			t.mu.Lock()
			select {
			case <-p.done:
				t.mu.Unlock()
				return cloneResult(p.result), p.err
			default:
			}
			for i, candidate := range t.waiters {
				if candidate != p {
					continue
				}
				code := CodeOf(ctx.Err())
				d := transition{FormatVersion: formatVersion, DataBytes: t.s.DataBytes, Fields: t.s.Fields}
				// Persist a canceled wait's no-effect receipt, but never create an
				// unbounded background transaction after the caller deadline.
				settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				result, err := t.commitResult(settleCtx, p.request, p.digest, Result{Status: StatusEmpty}, d, fail(code, "table wait canceled before fulfillment"), t.now().UTC())
				cancel()
				if !t.unavailable {
					t.finishPending(i, p, result, err)
				}
				break
			}
			t.mu.Unlock()
			return cloneResult(p.result), p.err
		case <-timer.C:
			t.mu.Lock()
			if t.checkServing() == nil {
				if err := t.expire(ctx, t.now().UTC()); err == nil {
					_ = t.fulfill(ctx, t.now().UTC())
				}
			}
			t.mu.Unlock()
		}
	}
}

func (t *Table) observeOperation(elapsed time.Duration, err error) {
	t.opCount.Add(1)
	if err != nil {
		t.opErrors.Add(1)
		var typed *Error
		if errors.As(err, &typed) && typed.OutcomeUnknown {
			t.opUnknown.Add(1)
		}
	}
	ns := uint64(max(elapsed, 0))
	t.opLatencyTotal.Add(ns)
	for old := t.opLatencyMax.Load(); ns > old; old = t.opLatencyMax.Load() {
		if t.opLatencyMax.CompareAndSwap(old, ns) {
			break
		}
	}
	bounds := [7]time.Duration{time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, time.Second, 10 * time.Second}
	bucket := 7
	for i, upper := range bounds {
		if elapsed <= upper {
			bucket = i
			break
		}
	}
	t.opLatencyBuckets[bucket].Add(1)
}

func (t *Table) receiptProtected(r receipt, now time.Time) bool {
	return r.ExpiresAt.After(now) || (t.recoveryReceipts[requestKey(r.ClientID, r.Sequence)] && t.now().Before(t.recoveryReceiptUntil))
}

func requestBytes(r Request) int {
	n := len(r.ClientID) + len(r.OwnerID) + len(r.RecordID) + len(r.LeaseToken) + len(r.Cursor)
	for _, c := range r.Columns {
		n += len(c)
	}
	for k, v := range r.Values {
		n += len(k) + len(v)
	}
	for k, v := range r.Expected {
		n += len(k) + len(v)
	}
	return n
}
func resultBytes(r Result) int {
	n := 0
	add := func(row Row) {
		n += len(row.ID)
		for k, v := range row.Values {
			n += len(k) + len(v)
		}
	}
	if r.Row != nil {
		add(*r.Row)
	}
	for _, row := range r.Rows {
		add(row)
	}
	for _, c := range r.Cells {
		n += len(c.Value) + 16
	}
	return n
}

// Close settles leases and rejects new work in a single committed transition.
// Released records remain in the immutable final snapshot. discard=true marks
// every remaining record discarded instead. Close is idempotent.
func (t *Table) Close(ctx context.Context, discardOption ...bool) error {
	discard := len(discardOption) > 0 && discardOption[0]
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.s.Closed {
		return nil
	}
	if t.unavailable {
		return t.checkServing()
	}
	d := transition{FormatVersion: formatVersion, Close: true, DiscardAll: discard}
	counts := t.s.Counts
	if t.s.Profile == ProfileNative {
		for _, id := range sortedRecordKeys(t.s.Records) {
			rec := t.s.Records[id]
			if discard {
				d.Delete = append(d.Delete, id)
				if rec.Lease != nil {
					counts.Leased--
				} else if rec.Quarantined {
					counts.Quarantined--
				} else {
					counts.Available--
				}
				counts.Discarded++
			} else if rec.Lease != nil {
				rec = copyRecord(rec)
				rec.Lease = nil
				rec.Row.Version++
				d.Upsert = append(d.Upsert, rec)
				counts.Leased--
				counts.Available++
			}
		}
	} else if discard {
		d.ColumnData = map[string][]Cell{}
		for _, col := range t.s.Columns {
			d.ColumnData[col] = nil
		}
	}
	d.Counts = &counts
	d.DataBytes, d.Fields = t.measureData(&d)
	if err := t.commit(ctx, d, t.now().UTC()); err != nil {
		return err
	}
	t.failWaiters(fail(CodeClosed, "table is closed"))
	return nil
}
