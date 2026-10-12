package livetable

import (
	"encoding/json"
	"time"
)

func isLeaseControl(op Operation) bool {
	return op == OpAck || op == OpRelease || op == OpRenew
}

// New ordinary operations leave a small share for renewals. Each active lease
// additionally reserves its own final settlement, which renewal cannot consume.
// These are reservations inside the configured total, never extra capacity.
func (t *Table) renewalReservation() (int, int64) {
	if t.s.Profile != ProfileNative {
		return 0, 0
	}
	count := min(8, t.s.Limits.MaxReceipts/16)
	if count == 0 {
		return 0, 0
	}
	return count, min(t.s.Limits.MaxReceiptBytes/8, int64(count)*t.maximumReceiptBytes())
}

func (t *Table) maximumReceiptBytes() int64 {
	return int64(t.s.Limits.MaxResultBytes) + 1024
}

// Settlement returns no more than the acquired row and its lease. The extra
// allowance covers the complete result envelope, maximum-width revisions,
// timestamps, client ID and receipt metadata. No data is copied into a retained
// reservation; it is derived from the current validated acquisition.
func (t *Table) settlementReceiptBytes(rec record) int64 {
	row, _ := json.Marshal(rec.Row)
	lease, _ := json.Marshal(rec.Lease)
	return min(t.maximumReceiptBytes(), int64(len(row)+len(lease)+1024))
}

func (t *Table) receiptCapacity(now time.Time, request Request) error {
	outcomeBytes := t.maximumReceiptBytes()
	if isLeaseControl(request.Operation) {
		if rec, ok := t.s.Records[request.RecordID]; ok && rec.Lease != nil {
			outcomeBytes = t.settlementReceiptBytes(rec)
		}
	}
	count, bytes := t.receiptReservationUsage(now, request, nil, outcomeBytes, true)
	if count > t.s.Limits.MaxReceipts || bytes > t.s.Limits.MaxReceiptBytes {
		return fail(CodeCapacity, "protected operation receipt or lease settlement budget exhausted")
	}
	if request.Wait > 0 {
		// A waiting request may need a durable cancellation receipt even when
		// no producer arrives. Reserve recovery space before accepting it.
		bound := t.s.DataBytes + bytes + int64(t.clientReservationUsage(request, nil))*2048 + int64(len(t.s.Cursors))*2048 + int64(count)*1024 + int64(len(t.s.Columns))*2048 + 16384
		if bound > int64(t.s.Limits.MaxSnapshotBytes) {
			return fail(CodeCapacity, "checkpoint reservation budget exhausted")
		}
	}
	return nil
}

func (t *Table) receiptReservationUsage(now time.Time, request Request, next *transition, outcomeBytes int64, admission bool) (int, int64) {
	count := 1
	bytes := outcomeBytes
	for _, rec := range t.s.Receipts {
		if t.receiptProtected(rec, now) {
			count++
			bytes += rec.Bytes
		}
	}
	// Completion replaces its own pending reservation. Other accepted waits
	// retain their maximum-sized outcome even when a control call commits first.
	key := requestKey(request.ClientID, request.Sequence)
	for pendingKey, pending := range t.pending {
		if pendingKey == key {
			continue
		}
		count++
		bytes += t.maximumReceiptBytes()
		if pending.request.Operation == OpLease {
			count++
			bytes += t.maximumReceiptBytes()
		}
	}
	if t.s.Profile == ProfileNative {
		changed := map[string]record{}
		deleted := map[string]bool{}
		if next != nil {
			for _, rec := range next.Upsert {
				changed[rec.Row.ID] = rec
			}
			for _, id := range next.Delete {
				deleted[id] = true
			}
		}
		// At admission, acknowledgement and release may consume the matching
		// acquisition's reservation. Ownership is also checked by evaluation;
		// malformed or stale requests cannot publish a transition.
		settles := admission && (request.Operation == OpAck || request.Operation == OpRelease)
		for id := range t.leaseDeadlines {
			rec := t.s.Records[id]
			if replacement, ok := changed[id]; ok {
				rec = replacement
				delete(changed, id)
			}
			if deleted[id] || rec.Lease == nil || (settles && id == request.RecordID && owned(request, rec.Lease) && rec.Lease.Token == request.LeaseToken) {
				continue
			}
			count++
			bytes += t.settlementReceiptBytes(rec)
		}
		for _, rec := range changed {
			if rec.Lease != nil {
				count++
				bytes += t.settlementReceiptBytes(rec)
			}
		}
		if admission && request.Operation == OpLease {
			count++
			bytes += t.maximumReceiptBytes()
		}
		if admission && !isLeaseControl(request.Operation) {
			reservedCount, reservedBytes := t.renewalReservation()
			count += reservedCount
			bytes += reservedBytes
		}
	}
	return count, bytes
}
