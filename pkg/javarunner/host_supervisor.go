package javarunner

import (
	"context"
	"errors"
	"sync"
	"time"
	"unicode/utf8"
)

// ErrHostNotStarted is a backend assertion that provisioning completed without
// creating any process/container. All other failed creates retain reservations.
var ErrHostNotStarted = errors.New("pooled_host_not_started")

// ManagedHost is an internal backend contract, not an externally trusted API.
// Terminate may return nil ONLY after positive root AND descendant termination
// evidence for Identity. Done indicates loss of usability, never cleanup proof.
// Identity must be stable and cached; Identity/Done must never wait on execution
// or network I/O. Other methods must honor context cancellation; lease cleanup
// never waits on Invoke.
// Ready must prepare slots without loading or initializing user classes. Invoke
// returns a true closure receipt only after acknowledged cooperative cancellation;
// it must bound ordinary interrupt grace to two seconds, independently of Terminate.
type ManagedHost interface {
	Identity() string
	Ready(context.Context, map[string]map[string]string) error
	Commit(context.Context) error
	Invoke(context.Context, string, string, map[string]string) (PooledFrame, bool, error)
	CloseSlot(context.Context, string) error
	Terminate(context.Context) error
	Done() <-chan struct{}
}

// HostProvisioner must return a handle even on failure if it created a boundary
// whose identity is known. A late return after cancellation is still owned and
// reaped by the supervisor. Unknown creation outcomes remain quarantined.
type HostProvisioner interface {
	Provision(context.Context, HostAllocation) (ManagedHost, error)
}

type supervisedHost struct {
	runtime  ManagedHost
	identity string
	stopOnce sync.Once
}

// HostSupervisor joins one run/group ledger to its containment backend. A
// separate monitor enforces lease/preparation expiry during blocked invocations.
// This in-memory owner is not a durable Kubernetes broker or a fleet quota.
type HostSupervisor struct {
	ledger           *HostLedger
	backend          HostProvisioner
	qualifiedCleanup bool
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	closed           bool
	hosts            map[string]*supervisedHost
	phases           map[string]bool
	operations       sync.WaitGroup
	cleanups         sync.WaitGroup
	closeOnce        sync.Once
	closedDone       chan struct{}
	watchDone        chan struct{}
	cleanupTimeout   time.Duration
}

func NewHostSupervisor(ledger *HostLedger, backend HostProvisioner, qualifiedCleanup bool) (*HostSupervisor, error) {
	if ledger == nil || backend == nil {
		return nil, ErrHostState
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &HostSupervisor{ledger: ledger, backend: backend, qualifiedCleanup: qualifiedCleanup, ctx: ctx, cancel: cancel, hosts: make(map[string]*supervisedHost), phases: make(map[string]bool), closedDone: make(chan struct{}), watchDone: make(chan struct{}), cleanupTimeout: 5 * time.Second}
	go s.watch()
	return s, nil
}

func (s *HostSupervisor) operation(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, nil, ErrHostFence
	}
	s.operations.Add(1)
	s.mu.Unlock()
	linked, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return linked, func() { stop(); cancel(); s.operations.Done() }, nil
}

// Prepare uses ledger-owned allocations only. BeginPrepare precedes every
// external provisioning side effect. A failed host aborts the whole reservation.
func (s *HostSupervisor) Prepare(ctx context.Context, epoch, reservation string, contexts map[string]map[string]string) error {
	ctx, finish, err := s.operation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	releasePhase, err := s.phase(reservation)
	if err != nil {
		return err
	}
	defer releasePhase()
	allocations, err := s.reservation(epoch, reservation, HostReserved)
	if err != nil {
		return err
	}
	deadline, err := s.ledger.preparationDeadline(epoch, reservation)
	if err != nil {
		return err
	}
	prepareCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ctx = prepareCtx
	// Validate and freeze the complete slot context before the first side effect.
	expectedCount := 0
	for _, a := range allocations {
		expectedCount += len(a.SlotIDs)
	}
	if len(contexts) != expectedCount {
		return ErrHostIdentity
	}
	frozen := make(map[string]map[string]string, expectedCount)
	for _, a := range allocations {
		for _, slot := range a.SlotIDs {
			values, ok := contexts[slot]
			if !ok || !validHostContext(values) {
				return ErrHostIdentity
			}
			copied := make(map[string]string, len(values))
			for k, v := range values {
				copied[k] = v
			}
			frozen[slot] = copied
		}
	}
	for _, a := range allocations {
		if err = s.ledger.BeginPrepare(epoch, a.ID); err != nil {
			break
		}
		var runtime ManagedHost
		runtime, err = s.backend.Provision(ctx, a)
		if runtime != nil {
			owned := &supervisedHost{runtime: runtime, identity: runtime.Identity()}
			bindErr := s.ledger.BindContainment(a.ID, owned.identity)
			s.mu.Lock()
			s.hosts[a.ID] = owned
			closed := s.closed
			s.mu.Unlock()
			if bindErr != nil && err == nil {
				err = bindErr
			}
			// Start death observation even for failed or delayed creation.
			go s.observe(a.ID, owned)
			if err == nil {
				err = s.ledger.preparationAllowed(epoch, a.ID)
			}
			if closed || err != nil || ctx.Err() != nil {
				_ = s.ledger.FailHost(a.ID, "pooled_host_transport_lost")
				s.cleanup(a.ID, owned)
				if err == nil {
					err = ErrHostFence
				}
			}
		} else if err == nil {
			err = ErrHostState
		}
		if err != nil {
			_ = s.ledger.FailHost(a.ID, "pooled_host_transport_lost")
			if runtime == nil && errors.Is(err, ErrHostNotStarted) {
				_ = s.ledger.ConfirmNotStarted(a.ID, true)
			}
			break
		}
		slotContexts := make(map[string]map[string]string, len(a.SlotIDs))
		for _, slot := range a.SlotIDs {
			slotContexts[slot] = frozen[slot]
		}
		if err = runtime.Ready(ctx, slotContexts); err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		if s.runtime(a.ID) == nil {
			err = ErrHostIdentity
			break
		}
		if err = s.ledger.MarkReady(epoch, a.ID); err != nil {
			break
		}
	}
	if err != nil {
		_ = s.ledger.Abort(epoch, reservation)
		s.reconcile()
		return err
	}
	return nil
}

// Commit first opens each runtime's gate while the ledger still rejects every
// invocation. Only after all hosts acknowledge does the ledger commit atomically.
func (s *HostSupervisor) Commit(ctx context.Context, epoch, reservation string) error {
	ctx, finish, err := s.operation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	releasePhase, err := s.phase(reservation)
	if err != nil {
		return err
	}
	defer releasePhase()
	allocations, err := s.reservation(epoch, reservation, HostReady)
	if err != nil {
		return err
	}
	for _, a := range allocations {
		runtime := s.runtime(a.ID)
		if runtime == nil {
			err = ErrHostState
			break
		}
		if err = runtime.Commit(ctx); err != nil {
			break
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = s.ledger.Commit(epoch, reservation)
	}
	if err != nil {
		_ = s.ledger.Abort(epoch, reservation)
		s.reconcile()
	}
	return err
}

func (s *HostSupervisor) Invoke(ctx context.Context, epoch, host, slot, method string, parameters map[string]string) (PooledFrame, error) {
	ctx, finish, err := s.operation(ctx)
	if err != nil {
		return PooledFrame{}, err
	}
	defer finish()
	if err = s.ledger.CanInvoke(epoch, host, slot); err != nil {
		return PooledFrame{}, err
	}
	runtime := s.runtime(host)
	if runtime == nil {
		return PooledFrame{}, ErrPooledHostReaped
	}
	result, closed, err := runtime.Invoke(ctx, slot, method, parameters)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if closed && s.qualifiedCleanup {
			_ = s.ledger.CloseSlot(epoch, host, slot, true)
		} else {
			_ = s.ledger.FailHost(host, "pooled_interrupt_timeout")
		}
		s.reconcile()
	}
	return result, err
}

func (s *HostSupervisor) CloseSlot(ctx context.Context, epoch, host, slot string) error {
	ctx, finish, err := s.operation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err = s.ledger.CanInvoke(epoch, host, slot); err != nil {
		return err
	}
	runtime := s.runtime(host)
	if runtime == nil {
		return ErrPooledHostReaped
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err = runtime.CloseSlot(closeCtx, slot)
	if err != nil || !s.qualifiedCleanup {
		_ = s.ledger.FailHost(host, "pooled_cleanup_uncertain")
		s.reconcile()
		if err != nil {
			return err
		}
		return errors.New("pooled_cleanup_uncertain")
	}
	err = s.ledger.CloseSlot(epoch, host, slot, true)
	s.reconcile()
	return err
}

func (s *HostSupervisor) reservation(epoch, id string, state HostState) ([]HostAllocation, error) {
	if epoch != s.ledger.binding.OwnerEpoch {
		return nil, ErrHostFence
	}
	allocations, _ := s.ledger.Snapshot()
	var selected []HostAllocation
	for _, a := range allocations {
		if a.ReservationID == id {
			if a.State != state {
				return nil, ErrHostState
			}
			selected = append(selected, a)
		}
	}
	if len(selected) == 0 {
		return nil, ErrHostIdentity
	}
	return selected, nil
}
func (s *HostSupervisor) runtime(id string) ManagedHost {
	s.mu.Lock()
	h := s.hosts[id]
	s.mu.Unlock()
	if h == nil {
		return nil
	}
	if h.runtime.Identity() != h.identity {
		_ = s.ledger.FailHost(id, "pooled_cleanup_uncertain")
		s.cleanup(id, h)
		return nil
	}
	return h.runtime
}

func (s *HostSupervisor) observe(id string, h *supervisedHost) {
	select {
	case <-h.runtime.Done():
		s.ledger.runtimeLost(id)
		s.cleanup(id, h)
	case <-s.ctx.Done():
		return
	}
}
func (s *HostSupervisor) watch() {
	defer close(s.watchDone)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.reconcile()
		case <-s.ctx.Done():
			s.reconcile()
			return
		}
	}
}
func (s *HostSupervisor) reconcile() {
	allocations, _ := s.ledger.Snapshot()
	for _, a := range allocations {
		if a.CleanupRequired {
			s.mu.Lock()
			h := s.hosts[a.ID]
			s.mu.Unlock()
			if h != nil {
				s.cleanup(a.ID, h)
			}
		}
	}
	if s.ledger.ownerRetired() {
		s.cancel()
	}
}
func (s *HostSupervisor) cleanup(id string, h *supervisedHost) {
	h.stopOnce.Do(func() {
		s.cleanups.Add(1)
		go func() {
			defer s.cleanups.Done()
			ctx, cancel := context.WithTimeout(context.Background(), s.cleanupTimeout)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				if h.runtime.Identity() != h.identity {
					result <- ErrHostIdentity
					return
				}
				err := h.runtime.Terminate(ctx)
				if err == nil && h.runtime.Identity() != h.identity {
					err = ErrHostIdentity
				}
				result <- err
			}()
			select {
			case err := <-result:
				if err == nil {
					err = s.ledger.ConfirmTermination(id, h.identity, true)
					if err == nil {
						s.mu.Lock()
						if s.hosts[id] == h {
							delete(s.hosts, id)
						}
						s.mu.Unlock()
					}
				}
				if err != nil {
					_ = s.ledger.FailHost(id, "pooled_cleanup_uncertain")
				}
			case <-ctx.Done():
				// No disappearance/timeout inference: keep the entire reservation charged.
				_ = s.ledger.FailHost(id, "pooled_cleanup_uncertain")
			}
		}()
	})
}

// Close fences admission and begins cleanup before waiting for in-flight work.
// A deadline leaves the supervisor owning unresolved cleanup; it does not release
// capacity. Call Close again to wait. Persistent quarantine is reported as failure.
func (s *HostSupervisor) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		_ = s.ledger.Retire(s.ledger.binding.OwnerEpoch)
		s.cancel()
		s.reconcile()
		go func() { s.operations.Wait(); <-s.watchDone; s.reconcile(); s.cleanups.Wait(); close(s.closedDone) }()
	})
	select {
	case <-s.closedDone:
		_, usage := s.ledger.Snapshot()
		if usage.Hosts != 0 {
			return ErrProcessContainmentFailed
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *HostSupervisor) phase(reservation string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phases[reservation] {
		return nil, ErrHostState
	}
	s.phases[reservation] = true
	return func() { s.mu.Lock(); delete(s.phases, reservation); s.mu.Unlock() }, nil
}

func validHostContext(values map[string]string) bool {
	if len(values) > 6 {
		return false
	}
	for k, v := range values {
		switch k {
		case "APEXLOAD_VUSER_ID", "APEXLOAD_GROUP_NAME", "APEXLOAD_SCENARIO_ID", "APEXLOAD_IGNORE_THINK_TIME", "APEXLOAD_THINK_TIME_LIMIT_MS", "APEXLOAD_JAVA_VUSER_EVENT_FILE":
		default:
			return false
		}
		if len(v) > 65536 || !utf8.ValidString(v) {
			return false
		}
	}
	return true
}
