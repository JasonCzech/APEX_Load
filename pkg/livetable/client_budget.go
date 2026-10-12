package livetable

// Each acquisition reserves two additional identities for the engine's shared
// renewal and settlement lanes. Reservations survive recovery and prevent
// ordinary producers from exhausting the client high-water map mid-lease.
func leaseClientReservation(r record) int {
	if r.Lease == nil {
		return 0
	}
	return 2 - len(r.ControlClients)
}
func (t *Table) clientCapacity(r Request, next *transition) error {
	if t.clientReservationUsage(r, next) > t.s.Limits.MaxClients {
		return fail(CodeCapacity, "client retirement and lease control budget exhausted")
	}
	return nil
}

func (t *Table) clientReservationUsage(r Request, next *transition) int {
	clients := len(t.s.Clients)
	pendingClients := map[string]bool{}
	for client := range t.pendingClients {
		if _, ok := t.s.Clients[client]; !ok {
			pendingClients[client] = true
		}
	}
	if _, ok := t.s.Clients[r.ClientID]; !ok {
		pendingClients[r.ClientID] = true
	}
	clients += len(pendingClients)
	reserved := 0
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
	for id, rec := range t.s.Records {
		if deleted[id] {
			continue
		}
		if replacement, ok := changed[id]; ok {
			rec = replacement
		}
		reserved += leaseClientReservation(rec)
	}
	for key, p := range t.pending {
		if key != requestKey(r.ClientID, r.Sequence) && p.request.Operation == OpLease {
			reserved += 2
		}
	}
	if next == nil {
		if r.Operation == OpLease {
			reserved += 2
		}
		// Only a valid acquisition owner can spend its reservation. Invalid tokens
		// cannot open identities in somebody else's reserved capacity.
		if isLeaseControl(r.Operation) {
			rec, ok := t.s.Records[r.RecordID]
			if ok && owned(r, rec.Lease) {
				if r.Operation == OpAck || r.Operation == OpRelease {
					reserved -= leaseClientReservation(rec)
				} else if _, exists := t.s.Clients[r.ClientID]; !exists && len(rec.ControlClients) < 2 {
					reserved--
				}
			}
		}
	}
	return clients + reserved
}
