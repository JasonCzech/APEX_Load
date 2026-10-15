package javarunner

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// HostLedger is the in-memory admission authority for one experimental run/group.
// It is not a process supervisor: its owner must execute CleanupRequired intents
// immediately and confirm descendant termination through the containment backend.
// A broker with disposable pods needs durable storage before using this model.
type HostLedger struct {
	mu           sync.Mutex
	binding      HostBinding
	budget       HostBudget
	capacity     *HostCapacity
	prefix       string
	sequence     uint64
	admissions   int
	leaseUntil   time.Time
	retired      bool
	profile      *HostProfile
	now          func() time.Time
	hosts        map[string]*hostRecord
	reservations map[string][]string
}

type HostBinding struct {
	OwnerID, OwnerEpoch, ExecutionID, GroupID, ConfigurationSHA256 string
}

type HostBudget struct {
	MaxHosts, MaxVUsers int
	MaxMemoryBytes      int64
}

// MaxPooledSlotsPerHost matches the bounded Java host lifetime admission ceiling
// (PooledHost.java MAX_SLOTS). Slots are never reused, so this caps lifetime
// admissions per JVM, not only concurrent VUsers. The operator's resource
// profile chooses the working size below this ceiling.
const MaxPooledSlotsPerHost = 1024

type HostProfile struct {
	SlotsPerHost       int
	MemoryBytesPerHost int64
}
type HostState string

const (
	HostReserved    HostState = "reserved"
	HostPreparing   HostState = "preparing"
	HostReady       HostState = "ready"
	HostCommitted   HostState = "committed"
	HostDraining    HostState = "draining"
	HostQuarantined HostState = "quarantined"
	HostReaped      HostState = "reaped"
)

type HostAllocation struct {
	ID, ReservationID string
	Binding           HostBinding
	State             HostState
	SlotIDs           []string
	OccupiedSlots     int
	MemoryBytes       int64
	ContainmentID     string
	CleanupRequired   bool
	FailureCode       string
}
type HostUsage struct {
	Hosts, VUsers int
	MemoryBytes   int64
}
type hostRecord struct {
	HostAllocation
	closed       map[string]bool
	prepareUntil time.Time
	charge       *hostCharge
}

var (
	ErrHostFence    = errors.New("pooled_owner_fenced")
	ErrHostCapacity = errors.New("pooled_capacity_unavailable")
	ErrHostState    = errors.New("pooled_host_state_invalid")
	ErrHostIdentity = errors.New("pooled_host_identity_invalid")
)

// NewHostLedger enforces only a single run/group budget. Runner integration
// must use NewHostLedgerWithCapacity for aggregate admission across participants.
func NewHostLedger(binding HostBinding, budget HostBudget, lease time.Duration) (*HostLedger, error) {
	return newHostLedger(binding, budget, lease, time.Now)
}

// NewHostLedgerWithCapacity adds a runner-wide admission authority to the
// per-run budget. Keep this capacity shared across groups and owner epochs;
// replacing an owner must not discard its unresolved cleanup reservations.
func NewHostLedgerWithCapacity(binding HostBinding, budget HostBudget, lease time.Duration, capacity *HostCapacity) (*HostLedger, error) {
	if capacity == nil || !validHostBudget(capacity.budget) {
		return nil, ErrHostState
	}
	l, err := NewHostLedger(binding, budget, lease)
	if err != nil {
		return nil, err
	}
	l.capacity = capacity
	return l, nil
}

func newHostLedger(binding HostBinding, budget HostBudget, lease time.Duration, now func() time.Time) (*HostLedger, error) {
	digest, err := hex.DecodeString(binding.ConfigurationSHA256)
	if err != nil || len(digest) != 32 || binding.OwnerID == "" || binding.OwnerEpoch == "" || binding.ExecutionID == "" || binding.GroupID == "" ||
		!validHostBudget(budget) || lease <= 0 || lease > time.Minute {
		return nil, ErrHostState
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return &HostLedger{binding: binding, budget: budget, prefix: hex.EncodeToString(nonce[:]), leaseUntil: now().Add(lease), now: now, hosts: make(map[string]*hostRecord), reservations: make(map[string][]string)}, nil
}

func (l *HostLedger) nextID() string { l.sequence++; return fmt.Sprintf("%s-%d", l.prefix, l.sequence) }

// Reserve charges prepared slots and full host memory atomically. It never
// packs new slots into a draining/fragmented host. Profile qualification and
// configuration hashing must already have happened at the operator boundary.
func (l *HostLedger) Reserve(epoch string, vusers int, p HostProfile, prepareFor time.Duration) ([]HostAllocation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return nil, err
	}
	if vusers < 1 || vusers > l.budget.MaxVUsers || p.SlotsPerHost < 1 || p.SlotsPerHost > l.budget.MaxVUsers || p.SlotsPerHost > MaxPooledSlotsPerHost || p.MemoryBytesPerHost <= 0 || prepareFor <= 0 || prepareFor > time.Hour {
		return nil, ErrHostState
	}
	if l.profile != nil && *l.profile != p {
		return nil, ErrHostIdentity
	}
	n := (vusers-1)/p.SlotsPerHost + 1
	u := l.usage()
	if n > l.budget.MaxHosts-u.Hosts || vusers > l.budget.MaxVUsers-u.VUsers || int64(n) > (l.budget.MaxMemoryBytes-u.MemoryBytes)/p.MemoryBytesPerHost || len(l.hosts)+n > 100000 || vusers > 1_000_000-l.admissions || l.sequence > math.MaxUint64-uint64(1+n+vusers) {
		return nil, ErrHostCapacity
	}
	var charges []*hostCharge
	if l.capacity != nil {
		var err error
		charges, _, err = l.capacity.reserve(vusers, p)
		if err != nil {
			return nil, err
		}
	}
	reservation := l.nextID()
	l.admissions += vusers
	profile := p
	l.profile = &profile
	var result []HostAllocation
	for remaining := vusers; remaining > 0; {
		slots := min(remaining, p.SlotsPerHost)
		remaining -= slots
		h := &hostRecord{HostAllocation: HostAllocation{ID: l.nextID(), ReservationID: reservation, Binding: l.binding, State: HostReserved, OccupiedSlots: slots, MemoryBytes: p.MemoryBytesPerHost}, closed: make(map[string]bool), prepareUntil: l.now().Add(prepareFor)}
		if charges != nil {
			h.charge = charges[len(result)]
		}
		for i := 0; i < slots; i++ {
			h.SlotIDs = append(h.SlotIDs, l.nextID())
		}
		l.hosts[h.ID] = h
		l.reservations[reservation] = append(l.reservations[reservation], h.ID)
		result = append(result, snapshotHost(h))
	}
	return result, nil
}

// BeginPrepare must precede every external provisioning side effect. An abort
// before this transition can release capacity; after it, even an unknown process
// identity retains a quarantine reservation until cleanup is established.
func (l *HostLedger) BeginPrepare(epoch, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return err
	}
	h := l.hosts[id]
	if h == nil || h.State != HostReserved {
		return ErrHostState
	}
	h.State = HostPreparing
	return nil
}

// BindContainment records the exact backend identity, including for a delayed
// provisioning completion after expiry. It cannot make an expired host ready.
func (l *HostLedger) BindContainment(id, identity string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire()
	h := l.hosts[id]
	if h == nil || identity == "" || len(identity) > 1024 || (h.State != HostPreparing && h.State != HostQuarantined) || h.ContainmentID != "" && h.ContainmentID != identity {
		return ErrHostIdentity
	}
	h.ContainmentID = identity
	return nil
}

func (l *HostLedger) MarkReady(epoch, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return err
	}
	h := l.hosts[id]
	if h == nil || h.State != HostPreparing || h.ContainmentID == "" {
		return ErrHostState
	}
	h.State = HostReady
	return nil
}

// Commit is an all-host barrier. A successful return is the earliest point at
// which the owner may invoke user initialization, subject to the continuing lease.
func (l *HostLedger) Commit(epoch, reservation string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return err
	}
	ids := l.reservations[reservation]
	if len(ids) == 0 {
		return ErrHostIdentity
	}
	for _, id := range ids {
		if l.hosts[id].State != HostReady {
			return ErrHostState
		}
	}
	for _, id := range ids {
		l.hosts[id].State = HostCommitted
	}
	return nil
}

func (l *HostLedger) Renew(epoch string, lease time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return err
	}
	if lease <= 0 || lease > time.Minute {
		return ErrHostState
	}
	l.leaseUntil = l.now().Add(lease)
	return nil
}

// CanInvoke is checked by the host adapter before every submission, including
// initialization. A surviving slot in a sealed host remains usable until it
// closes or host cleanup begins. The supervisor independently enforces expiry.
func (l *HostLedger) CanInvoke(epoch, host, slot string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return err
	}
	h := l.hosts[host]
	if h == nil {
		return ErrHostIdentity
	}
	if h.State == HostQuarantined || h.State == HostReaped || h.CleanupRequired {
		return ErrPooledHostReaped
	}
	if (h.State != HostCommitted && h.State != HostDraining) || h.closed[slot] {
		return ErrHostState
	}
	for _, id := range h.SlotIDs {
		if id == slot {
			return nil
		}
	}
	return ErrHostIdentity
}

// CloseSlot requires completion plus operator-qualified cooperative cleanup.
// Unknown cleanup quarantines the entire host without releasing occupancy.
// Closing a known-clean slot seals its host and releases only that slot's charge.
func (l *HostLedger) CloseSlot(epoch, host, slot string, qualifiedCleanup bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return err
	}
	h := l.hosts[host]
	if h == nil {
		return ErrHostIdentity
	}
	found := false
	for _, id := range h.SlotIDs {
		if id == slot {
			found = true
			break
		}
	}
	if !found {
		return ErrHostIdentity
	}
	if h.State != HostCommitted && h.State != HostDraining {
		return ErrHostState
	}
	if h.closed[slot] {
		return nil
	}
	if !qualifiedCleanup {
		l.quarantine(h, "pooled_cleanup_uncertain")
		return nil
	}
	h.closed[slot] = true
	h.OccupiedSlots--
	h.charge.closeSlot()
	h.State = HostDraining
	if h.OccupiedSlots == 0 {
		h.CleanupRequired = true
	}
	return nil
}

// Abort and expiry preserve the first failure. Callers must never interpret
// object disappearance, a delete acknowledgment, or a requested kill as proof.
func (l *HostLedger) Abort(epoch, reservation string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if epoch != l.binding.OwnerEpoch {
		return ErrHostFence
	}
	l.expire()
	ids := l.reservations[reservation]
	if len(ids) == 0 {
		return ErrHostIdentity
	}
	for _, id := range ids {
		l.quarantine(l.hosts[id], "pooled_preparation_aborted")
	}
	return nil
}

// FailHost records a backend failure without aborting unrelated hosts in the
// reservation. Only stable value-free reasons enter retained diagnostics.
func (l *HostLedger) FailHost(id, code string) error {
	switch code {
	case "pooled_host_transport_lost", "pooled_host_crashed", "pooled_control_queue_overflow", "pooled_interrupt_timeout", "pooled_cleanup_uncertain",
		"pooled_event_capture_invalid", "pooled_invocation_failed", "pooled_invocation_cancelled", "pooled_frame_invalid":
	default:
		return ErrHostState
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire()
	h := l.hosts[id]
	if h == nil {
		return ErrHostIdentity
	}
	l.quarantine(h, code)
	return nil
}

// ConfirmTermination is called only after positive backend evidence that every
// descendant has stopped. Empty/changed identities or unconfirmed cleanup keep
// all host/memory charges. This method is not an externally trusted assertion API.
func (l *HostLedger) ConfirmTermination(id, identity string, descendantsStopped bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire()
	h := l.hosts[id]
	if h == nil || identity == "" || h.ContainmentID != identity || !descendantsStopped {
		return ErrHostIdentity
	}
	if h.State == HostReaped {
		return nil
	}
	if !h.CleanupRequired {
		return ErrHostState
	}
	h.State = HostReaped
	h.OccupiedSlots = 0
	h.CleanupRequired = false
	h.charge.release()
	return nil
}

// ConfirmNotStarted is only valid for a completed failed provisioning operation
// that proved it never created a process/pod. An uncertain create must remain
// quarantined; absence in a list response is not such proof.
func (l *HostLedger) ConfirmNotStarted(id string, creationDefinitivelyFailed bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire()
	h := l.hosts[id]
	if h == nil || h.State != HostQuarantined || h.ContainmentID != "" || !creationDefinitivelyFailed {
		return ErrHostState
	}
	h.State = HostReaped
	h.OccupiedSlots = 0
	h.CleanupRequired = false
	h.charge.release()
	return nil
}

func (l *HostLedger) Snapshot() ([]HostAllocation, HostUsage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire()
	result := make([]HostAllocation, 0, len(l.hosts))
	for _, h := range l.hosts {
		result = append(result, snapshotHost(h))
	}
	return result, l.usage()
}

func (l *HostLedger) fence(epoch string) error {
	l.expire()
	if epoch != l.binding.OwnerEpoch || l.retired {
		return ErrHostFence
	}
	return nil
}
func (l *HostLedger) expire() {
	now := l.now()
	if !now.Before(l.leaseUntil) {
		l.retired = true
	}
	for _, h := range l.hosts {
		if l.retired {
			l.quarantine(h, "pooled_run_lease_expired")
		} else if (h.State == HostReserved || h.State == HostPreparing || h.State == HostReady) && !now.Before(h.prepareUntil) {
			l.quarantine(h, "pooled_preparation_expired")
		}
	}
}
func (l *HostLedger) quarantine(h *hostRecord, reason string) {
	if h.State == HostReaped {
		return
	}
	if h.FailureCode == "" {
		h.FailureCode = reason
	}
	if h.State == HostReserved {
		h.State = HostReaped
		h.OccupiedSlots = 0
		h.charge.release()
		return
	}
	h.State = HostQuarantined
	h.CleanupRequired = true
}
func (l *HostLedger) usage() (u HostUsage) {
	for _, h := range l.hosts {
		if h.State != HostReaped {
			u.Hosts++
			u.VUsers += h.OccupiedSlots
			u.MemoryBytes += h.MemoryBytes
		}
	}
	return u
}
func snapshotHost(h *hostRecord) HostAllocation {
	s := h.HostAllocation
	s.SlotIDs = append([]string(nil), s.SlotIDs...)
	return s
}

// Retire permanently fences a run owner and requests containment cleanup. It
// also closes direct ledger admission during supervisor shutdown.
func (l *HostLedger) Retire(epoch string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if epoch != l.binding.OwnerEpoch {
		return ErrHostFence
	}
	l.expire()
	l.retired = true
	for _, h := range l.hosts {
		l.quarantine(h, "pooled_run_closed")
	}
	return nil
}

func (l *HostLedger) preparationAllowed(epoch, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return err
	}
	if h := l.hosts[id]; h == nil || h.State != HostPreparing {
		return ErrHostState
	}
	return nil
}

// A backend becoming unusable during intentional cleanup is expected. Preserve
// that distinction atomically with lease expiry and the first failure reason.
func (l *HostLedger) runtimeLost(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire()
	h := l.hosts[id]
	if h == nil || h.State == HostReaped || h.CleanupRequired {
		return
	}
	l.quarantine(h, "pooled_host_transport_lost")
}

func (l *HostLedger) preparationDeadline(epoch, reservation string) (time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.fence(epoch); err != nil {
		return time.Time{}, err
	}
	ids := l.reservations[reservation]
	if len(ids) == 0 {
		return time.Time{}, ErrHostIdentity
	}
	deadline := l.hosts[ids[0]].prepareUntil
	for _, id := range ids {
		if l.hosts[id].prepareUntil.Before(deadline) {
			deadline = l.hosts[id].prepareUntil
		}
	}
	return deadline, nil
}

// PreparationDeadline reports the original reservation deadline. Replaying a
// control request must not give a reservation a fresh preparation window.
func (l *HostLedger) PreparationDeadline(epoch, reservation string) (time.Time, error) {
	return l.preparationDeadline(epoch, reservation)
}
func (l *HostLedger) ownerRetired() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire()
	return l.retired
}
