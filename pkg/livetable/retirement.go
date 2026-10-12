package livetable

import (
	"context"
	"sort"
	"strings"
)

func validScope(runID, scope, scopeID string) bool {
	if scope == "project" {
		return runID == "" && validName(scopeID)
	}
	return (scope == "" || scope == "run") && validName(runID) && scopeID == ""
}
func validOwnerScope(prefix string) bool {
	return len(prefix) >= 3 && len(prefix) <= 192 && strings.HasSuffix(prefix, "/") && validName(prefix) && !strings.HasPrefix(prefix, "/")
}
func inOwnerScope(id, prefix string) bool { return strings.HasPrefix(id, prefix) }

// SetAuthorizer installs a caller-owned local authorization check. It runs under
// the table actor lock, must not block or call the table, and is checked again
// for wait fulfillment. Set it before publishing a recovered project instance.
func (t *Table) SetAuthorizer(authorize func(Request) error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.authorize = authorize
}

// RevokeOwnerScope is trusted maintenance after the attachment has been durably
// revoked. It settles only leases belonging to this namespace and cancels its
// waiters. The caller must fence all later operations from this namespace.
func (t *Table) RevokeOwnerScope(ctx context.Context, prefix string) error {
	if !validOwnerScope(prefix) {
		return fail(CodeInvalid, "invalid owner scope")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkServing(); err != nil {
		return err
	}
	for n := 0; n < len(t.waiters); {
		p := t.waiters[n]
		if inOwnerScope(p.request.ClientID, prefix) || inOwnerScope(p.request.OwnerID, prefix) {
			t.finishPending(n, p, Result{}, fail(CodeClosed, "attachment detached"))
		} else {
			n++
		}
	}
	ids := []string{}
	for id, rec := range t.s.Records {
		if rec.Lease != nil && inOwnerScope(rec.Lease.ClientID, prefix) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += 128 {
		d := transition{FormatVersion: formatVersion}
		counts := t.s.Counts
		for _, id := range ids[start:min(start+128, len(ids))] {
			rec := copyRecord(t.s.Records[id])
			rec.Lease = nil
			rec.ControlClients = nil
			rec.Row.Version++
			counts.Leased--
			if rec.Attempts >= t.s.Limits.MaxDeliveryAttempts {
				rec.Quarantined = true
				counts.Quarantined++
			} else {
				counts.Available++
			}
			d.Upsert = append(d.Upsert, rec)
		}
		d.Counts = &counts
		d.DataBytes, d.Fields = t.measureData(&d)
		if err := t.commit(ctx, d, t.now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// RetireOwnerScope removes bounded deduplication/cursor metadata after the
// caller's durable tombstone has passed its advertised retry window. The scope
// must never be reused. This compact journal transition replays the resolved
// namespace removal; it does not depend on a recovery clock.
func (t *Table) RetireOwnerScope(ctx context.Context, prefix string) error {
	if !validOwnerScope(prefix) {
		return fail(CodeInvalid, "invalid owner scope")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkServing(); err != nil {
		return err
	}
	for _, rec := range t.s.Records {
		if rec.Lease != nil && inOwnerScope(rec.Lease.ClientID, prefix) {
			return fail(CodeConflict, "owner scope still has leases")
		}
	}
	for _, p := range t.waiters {
		if inOwnerScope(p.request.ClientID, prefix) || inOwnerScope(p.request.OwnerID, prefix) {
			return fail(CodeConflict, "owner scope still has waiters")
		}
	}
	found := false
	for id := range t.s.Clients {
		found = found || inOwnerScope(id, prefix)
	}
	for id := range t.s.Cursors {
		found = found || inOwnerScope(id, prefix)
	}
	if !found {
		return nil
	}
	return t.commit(ctx, transition{FormatVersion: formatVersion, RetireScope: prefix, DataBytes: t.s.DataBytes, Fields: t.s.Fields}, t.now().UTC())
}
func (t *Table) applyRetireScope(prefix string) {
	for key, rec := range t.s.Receipts {
		if inOwnerScope(rec.ClientID, prefix) {
			t.s.ReceiptBytes -= rec.Bytes
			delete(t.s.Receipts, key)
			delete(t.recoveryReceipts, key)
		}
	}
	for key := range t.s.Clients {
		if inOwnerScope(key, prefix) {
			delete(t.s.Clients, key)
		}
	}
	for key := range t.s.Cursors {
		if inOwnerScope(key, prefix) {
			delete(t.s.Cursors, key)
		}
	}
}
