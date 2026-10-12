package livetable

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

// Snapshot captures only committed state, including all protected receipts and
// per-client sequence tombstones. The caller supplies encryption and an atomic
// chunk manifest before compacting the corresponding durable journal prefix.
func (t *Table) Snapshot() (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.unavailable {
		return Snapshot{}, t.checkServing()
	}
	payload, err := json.Marshal(t.s)
	if err != nil {
		return Snapshot{}, err
	}
	if len(payload) > t.s.Limits.MaxSnapshotBytes {
		return Snapshot{}, fail(CodeCapacity, "snapshot exceeds configured byte budget")
	}
	return Snapshot{FormatVersion: formatVersion, TableID: t.s.ID, RunID: t.s.RunID, Revision: t.s.Revision, Payload: payload}, nil
}

func Restore(snapshot Snapshot, committer Committer) (*Table, error) {
	return RestoreWithClock(snapshot, committer, time.Now)
}

// RestoreWithClock supplies the replacement controller's local clock. Existing
// acquisitions cannot be reissued until a full configured lease duration has
// elapsed locally, even if this controller's wall clock is ahead. This is a
// conservative reassignment hold, not a renewal of the old execution right:
// stored tokens/deadlines remain unchanged and late acknowledgments still fail.
func RestoreWithClock(snapshot Snapshot, committer Committer, now func() time.Time) (*Table, error) {
	if snapshot.FormatVersion != formatVersion || !validName(snapshot.TableID) || !validName(snapshot.RunID) {
		return nil, fail(CodeInvalid, "unsupported or invalid snapshot identity")
	}
	// The hard envelope is also enforced before decoding untrusted state. A
	// deployment should impose a smaller aggregate encrypted/decompressed cap.
	if len(snapshot.Payload) > 512<<20 {
		return nil, fail(CodeCapacity, "snapshot exceeds recovery envelope")
	}
	var s state
	if err := strictDecode(snapshot.Payload, &s); err != nil {
		return nil, fail(CodeInvalid, "invalid snapshot encoding")
	}
	if s.ID != snapshot.TableID || s.RunID != snapshot.RunID || s.Revision != snapshot.Revision {
		return nil, fail(CodeConflict, "snapshot identity or revision mismatch")
	}
	if now == nil {
		now = time.Now
	}
	t := &Table{s: s, committer: committer, now: now, pending: map[string]*pending{}, pendingClients: map[string]string{}, leaseDeadlines: map[string]time.Time{}, recoveryHolds: map[string]string{}}
	if err := t.validateRecovered(); err != nil {
		return nil, err
	}
	if len(snapshot.Payload) > t.s.Limits.MaxSnapshotBytes {
		return nil, fail(CodeCapacity, "snapshot exceeds configured byte budget")
	}
	for id, r := range t.s.Records {
		if r.Lease != nil {
			t.leaseDeadlines[id] = r.Lease.ExpiresAt
			t.recoveryHolds[id] = r.Lease.Token
		}
	}
	t.recoveryHoldUntil = now().Add(t.s.Limits.MaxLeaseDuration)
	t.recoveryReceiptUntil = now().Add(t.s.Limits.ReceiptRetention)
	t.recoveryReceipts = map[string]bool{}
	for key := range t.s.Receipts {
		t.recoveryReceipts[key] = true
	}
	return t, nil
}

// Replay is a recovery operation, not a serving path. Call before exposing a
// restored table to generators. It never invokes Committer or invents choices.
func (t *Table) Replay(entry Entry) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.waiters) > 0 {
		return fail(CodeConflict, "cannot replay while operations wait")
	}
	if t.s.Closed {
		return fail(CodeClosed, "closed snapshot cannot accept later journal entries")
	}
	if entry.TableID != t.s.ID || entry.PreviousRevision != t.s.Revision || entry.Revision != t.s.Revision+1 || entry.Revision == 0 {
		return fail(CodeConflict, "journal is not contiguous with checkpoint")
	}
	if len(entry.Payload) > t.s.Limits.MaxEntryBytes {
		return fail(CodeCapacity, "journal entry exceeds recovery byte budget")
	}
	var d transition
	if err := strictDecode(entry.Payload, &d); err != nil || d.FormatVersion != formatVersion {
		return fail(CodeInvalid, "invalid journal transition")
	}
	if d.Receipt != nil && (d.Receipt.Sequence <= t.s.Clients[d.Receipt.ClientID] || d.Receipt.Result.Revision != entry.Revision) {
		return fail(CodeConflict, "journal receipt sequence is inconsistent")
	}
	if err := t.validateTransitionPatches(d); err != nil {
		return err
	}
	// Recovery favors complete validation over hot-path performance; rejecting a
	// corrupt transition cannot leave the caller with a partially applied state.
	b, _ := json.Marshal(t.s)
	var copyState state
	if err := json.Unmarshal(b, &copyState); err != nil {
		return err
	}
	candidate := &Table{s: copyState}
	candidate.apply(d, entry.Revision)
	if err := candidate.validateRecovered(); err != nil {
		return err
	}
	t.s = candidate.s
	t.leaseDeadlines = map[string]time.Time{}
	t.recoveryHolds = map[string]string{}
	t.recoveryReceipts = map[string]bool{}
	for key := range t.s.Receipts {
		t.recoveryReceipts[key] = true
	}
	for id, r := range t.s.Records {
		if r.Lease != nil {
			t.leaseDeadlines[id] = r.Lease.ExpiresAt
			t.recoveryHolds[id] = r.Lease.Token
		}
	}
	return nil
}

func strictDecode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fail(CodeInvalid, "unexpected trailing JSON")
	}
	return nil
}

func (t *Table) validateRecovered() error {
	l, err := normalizeLimits(t.s.Limits)
	if err != nil {
		return err
	}
	if l != t.s.Limits {
		return fail(CodeInvalid, "snapshot requires fully specified limits")
	}
	if t.s.Profile != ProfileNative && t.s.Profile != ProfileVTS {
		return fail(CodeInvalid, "invalid recovered profile")
	}
	if !validName(t.s.ID) || !validName(t.s.RunID) || len(t.s.Columns) == 0 || len(t.s.Columns) > l.MaxColumns {
		return fail(CodeInvalid, "invalid recovered table schema")
	}
	seen := map[string]bool{}
	for _, col := range t.s.Columns {
		if !validName(col) || seen[col] {
			return fail(CodeInvalid, "invalid recovered columns")
		}
		seen[col] = true
	}
	if t.s.Records == nil || t.s.Data == nil || t.s.Cursors == nil || t.s.Clients == nil || t.s.Receipts == nil {
		return fail(CodeInvalid, "incomplete recovered state")
	}
	if len(t.s.Records) > l.MaxRows || len(t.s.Order) > 2*l.MaxRows+1024 || len(t.s.Clients) > l.MaxClients || len(t.s.Cursors) > l.MaxClients || len(t.s.Receipts) > l.MaxReceipts {
		return fail(CodeCapacity, "recovered state exceeds configured limits")
	}
	ordered := map[string]bool{}
	for _, id := range t.s.Order {
		if _, exists := t.s.Records[id]; !exists {
			continue
		}
		if ordered[id] {
			return fail(CodeInvalid, "recovered queue contains duplicate live identity")
		}
		ordered[id] = true
	}
	var available, leased, quarantined uint64
	for id, r := range t.s.Records {
		if t.s.Profile != ProfileNative || id != r.Row.ID || !validName(id) || !ordered[id] || r.Row.Version == 0 {
			return fail(CodeInvalid, "invalid recovered record")
		}
		if err := t.validateValues(r.Row.Values); err != nil {
			return err
		}
		if r.Lease != nil {
			if r.Quarantined || r.Lease.RecordID != id || !validName(r.Lease.Token) || !validName(r.Lease.ClientID) || r.Lease.ExpiresAt.After(r.Lease.MaxExpiresAt) || r.Attempts == 0 || r.Attempts != r.Lease.DeliveryAttempt {
				return fail(CodeInvalid, "invalid recovered lease")
			}
			leased++
		} else if r.Quarantined {
			quarantined++
		} else {
			available++
		}
	}
	for col, cells := range t.s.Data {
		if !seen[col] || len(cells) > l.MaxRows {
			return fail(CodeInvalid, "invalid recovered column")
		}
		if t.s.Profile == ProfileNative && len(cells) > 0 {
			return fail(CodeInvalid, "native snapshot contains column data")
		}
		for _, cell := range cells {
			if len(cell.Value) > l.MaxFieldBytes || !cell.Present && cell.Value != "" {
				return fail(CodeInvalid, "invalid recovered cell")
			}
		}
	}
	if t.s.Profile == ProfileVTS {
		for _, col := range t.s.Columns {
			if _, ok := t.s.Data[col]; !ok {
				return fail(CodeInvalid, "missing recovered column")
			}
		}
	}
	dataBytes, fields := t.measureData(nil)
	if dataBytes != t.s.DataBytes || fields != t.s.Fields || dataBytes > l.MaxBytes || fields > l.MaxFields {
		return fail(CodeCapacity, "recovered data accounting is inconsistent")
	}
	var receiptBytes int64
	for key, r := range t.s.Receipts {
		if !validName(r.ClientID) || r.Sequence == 0 || key != requestKey(r.ClientID, r.Sequence) || r.Sequence > t.s.Clients[r.ClientID] || len(r.Digest) != 64 || r.Result.Revision > t.s.Revision || r.ExpiresAt.IsZero() || r.Bytes < 0 {
			return fail(CodeInvalid, "invalid recovered operation receipt")
		}
		encoded, _ := json.Marshal(r.Result)
		expected := int64(len(encoded) + len(r.ClientID) + len(r.Digest) + 256)
		if r.Error != nil {
			expected += int64(len(r.Error.Message) + 64)
		}
		if expected != r.Bytes || len(encoded) > l.MaxResultBytes {
			return fail(CodeInvalid, "recovered receipt accounting is inconsistent")
		}
		receiptBytes += r.Bytes
	}
	for client, seq := range t.s.Clients {
		if !validName(client) || seq == 0 {
			return fail(CodeInvalid, "invalid retired client sequence")
		}
	}
	for cursor := range t.s.Cursors {
		if !validName(cursor) {
			return fail(CodeInvalid, "invalid recovered cursor")
		}
	}
	if receiptBytes != t.s.ReceiptBytes || receiptBytes > l.MaxReceiptBytes {
		return fail(CodeCapacity, "recovered receipt budget is inconsistent")
	}
	if t.s.Profile == ProfileNative {
		c := t.s.Counts
		if c.Available != available || c.Leased != leased || c.Quarantined != quarantined || c.Seeded+c.Pushed != available+leased+quarantined+c.Acknowledged+c.Popped+c.Discarded {
			return fail(CodeInvalid, "recovered queue conservation is inconsistent")
		}
	}
	return nil
}

// Inspect is a bounded, coherent committed view that also works after Close.
// expectedRevision==0 selects the current revision. Rows include quarantined
// and leased native records; after Close all surviving leases are released.
func (t *Table) Inspect(offset, limit int, expectedRevision uint64) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.unavailable {
		return Result{}, t.checkServing()
	}
	if expectedRevision != 0 && expectedRevision != t.s.Revision {
		return Result{}, fail(CodeConflict, "table revision changed")
	}
	if offset < 0 || limit <= 0 || limit > t.s.Limits.MaxRows || limit > t.s.Limits.MaxScanCells/len(t.s.Columns) {
		return Result{}, fail(CodeInvalid, "inspection range exceeds supported bounds")
	}
	result := Result{Status: StatusOK, Revision: t.s.Revision, Columns: append([]string(nil), t.s.Columns...)}
	if t.s.Profile == ProfileNative {
		result.Count = int64(len(t.s.Records))
		position := 0
		for _, id := range t.s.Order {
			r, ok := t.s.Records[id]
			if !ok {
				continue
			}
			if position >= offset && len(result.Rows) < limit {
				row := copyRow(r.Row)
				row.DeliveryAttempts = r.Attempts
				row.State = "available"
				if r.Lease != nil {
					row.State = "leased"
				}
				if r.Quarantined {
					row.State = "quarantined"
				}
				result.Rows = append(result.Rows, row)
			}
			position++
			if len(result.Rows) == limit {
				break
			}
		}
	} else {
		count := 0
		for _, cells := range t.s.Data {
			if len(cells) > count {
				count = len(cells)
			}
		}
		result.Count = int64(count)
		end := offset + limit
		if end > count {
			end = count
		}
		for i := offset; i < end; i++ {
			result.Rows = append(result.Rows, t.vtsRow(i+1, t.s.Columns))
		}
	}
	if len(result.Rows) == 0 {
		result.Status = StatusEmpty
	}
	if resultBytes(result) > t.s.Limits.MaxResultBytes {
		return Result{}, fail(CodeCapacity, "inspection result byte budget exceeded")
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) > t.s.Limits.MaxResultBytes {
		return Result{}, fail(CodeCapacity, "encoded inspection byte budget exceeded")
	}
	return result, nil
}
