package broker

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// Session identity outlives the process and broker/owner epochs. Reusing an
// execution/group/session after closure cannot transparently replay Java init.
// Tombstones are kept within the store's explicit, bounded lifetime inventory.
type sessionKey struct{ execution, group, session string }

func dedicatedSessionKey(b javarunner.HostBinding, session string) sessionKey {
	return sessionKey{b.ExecutionID, b.GroupID, session}
}

// ReserveDedicatedWave atomically admits the entire requested peak/increase.
// The caller must be authenticated and bind OwnerID/OwnerEpoch itself; this is
// an internal authority, not a wire API. Session IDs are unique per execution /
// group, even across configuration changes, restarts and closed hosts. A failed
// or ambiguous preparation is never retried with replacement session identities.
// Exactly one pending wave per binding is allowed; previous committed hosts
// remain charged and cannot be removed by a later failed capacity request.
func (s *Store) ReserveDedicatedWave(ctx context.Context, epoch string, binding javarunner.HostBinding, sessions []string, memoryBytes int64, lease, prepare time.Duration) ([]Record, error) {
	if !validBinding(binding) || memoryBytes <= 0 || len(sessions) < 1 || len(sessions) > maxRecords || lease < time.Millisecond || lease > time.Minute || prepare < time.Millisecond || prepare > time.Hour {
		return nil, ErrState
	}
	ids := make([]string, len(sessions))
	seen := map[string]bool{}
	for i, session := range sessions {
		if !name.MatchString(session) || seen[session] {
			return nil, ErrState
		}
		seen[session] = true
		id, e := randomID()
		if e != nil {
			return nil, e
		}
		ids[i] = id
	}
	var records []Record
	e := s.update(ctx, epoch, true, func(d *document) error {
		u := usage(d)
		n := len(sessions)
		for _, r := range d.Records {
			if r.SessionID != "" && r.Binding.ExecutionID == binding.ExecutionID && r.Binding.GroupID == binding.GroupID && seen[r.SessionID] {
				return ErrState
			}
			if r.Binding.OwnerID == binding.OwnerID {
				if r.State != javarunner.HostReaped && (r.BrokerEpoch != epoch || r.Binding.OwnerEpoch != binding.OwnerEpoch || r.State == javarunner.HostQuarantined) {
					return ErrCleanup
				}
			}
			if r.Binding == binding && (r.State == javarunner.HostReserved || r.State == javarunner.HostPreparing || r.State == javarunner.HostReady) {
				return ErrState
			}
		}
		// Divide instead of multiplying attacker-sized integers before admission.
		if n > maxRecords-len(d.Records) || n > d.Config.Budget.MaxHosts-u.Hosts || n > d.Config.Budget.MaxVUsers-u.VUsers || int64(n) > (d.Config.Budget.MaxMemoryBytes-u.MemoryBytes)/memoryBytes {
			return ErrCapacity
		}
		unique := map[string]bool{}
		for _, id := range ids {
			if _, exists := d.Records[id]; exists || unique[id] {
				return ErrState
			}
			unique[id] = true
		}
		now := s.now()
		for i, id := range ids {
			r := Record{ID: id, SessionID: sessions[i], BrokerEpoch: epoch, Binding: binding, State: javarunner.HostReserved, MemoryBytes: memoryBytes, VUserOccupied: true, PodName: "java-" + id, LeaseUntil: now.Add(lease), PrepareUntil: now.Add(prepare)}
			d.Records[id] = r
			records = append(records, r)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return records, nil
}

// AbortReservations retires only the immutable inventory of one wave. A delayed
// abort must never capture pending hosts from a subsequently admitted increase.
func (s *Store) AbortReservations(ctx context.Context, epoch string, binding javarunner.HostBinding, ids []string, reason string) error {
	if !validBinding(binding) || !name.MatchString(reason) || len(ids) < 1 || len(ids) > maxRecords {
		return ErrState
	}
	return s.update(ctx, epoch, true, func(d *document) error {
		seen := map[string]bool{}
		for _, id := range ids {
			r, ok := d.Records[id]
			if !ok || r.Binding != binding || r.BrokerEpoch != epoch || seen[id] {
				return ErrState
			}
			seen[id] = true
		}
		for _, id := range ids {
			r := d.Records[id]
			retire(&r, reason)
			d.Records[id] = r
		}
		return nil
	})
}
