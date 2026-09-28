package javasidecar

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

const maxPooledRuns = 1024

type pooledRunKey struct {
	owner            *ownerGeneration
	execution, group string
}

// PooledRun is a programmatic pilot lease, scoped to one execution/group and
// the currently claimed generator epoch. The HTTP controller wraps it with
// separate wire tokens; this handle stays inside the runner. Neither API grants
// production qualification.
type PooledRun struct {
	server           *Server
	owner            *ownerGeneration
	ledger           *javarunner.HostLedger
	supervisor       *javarunner.HostSupervisor
	profile          javarunner.HostProfile
	execution, group string
	mu               sync.Mutex
	deadline         time.Time
	timer            *time.Timer
	timerSeq         uint64
	closing          bool
	closed           chan struct{}
	cleanupErr       error // published by closing closed
	// Network-control identity is published/read under server.pooledMu. The
	// bounded reservation replay cache uses a separate lock, never blocking renew.
	controlID, controlToken, controlProfile string
	controlDecision                         PooledControlDecision
	controlMu                               sync.Mutex
	controlReplies                          map[string]pooledControlReply
	preparations                            map[string]*pooledPreparation
}

// OpenPooledRun accepts only a frozen, operator-qualified selection. No user
// classes are initialized here. Reopening an execution/group in the same epoch
// is rejected even after successful closure; it must never replay Java state.
func (s *Server) OpenPooledRun(ctx context.Context, ownerID, epoch, execution, group string, budget javarunner.HostBudget, selection javarunner.PooledSelection) (*PooledRun, error) {
	provider, err := selection.LaunchProvider()
	if err != nil {
		return nil, err
	}
	backend, err := javarunner.NewLocalHostBackend(provider)
	if err != nil {
		return nil, err
	}
	d := selection.Decision()
	return s.openPooledRun(ctx, ownerID, epoch, execution, group, budget, d.ConfigurationSHA256,
		javarunner.HostProfile{SlotsPerHost: d.SlotsPerHost, MemoryBytesPerHost: d.HostMemoryBytes}, backend)
}

// The backend seam is private: external callers cannot bypass qualification by
// supplying a provisioner or a fabricated resource/configuration identity.
func (s *Server) openPooledRun(ctx context.Context, ownerID, epoch, execution, group string, budget javarunner.HostBudget, configuration string, profile javarunner.HostProfile, backend javarunner.HostProvisioner) (*PooledRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.operator.Enabled || s.hostAdmission == nil || !executionIDPattern.MatchString(execution) || !executionIDPattern.MatchString(group) || profile.SlotsPerHost < 2 || profile.SlotsPerHost > javarunner.MaxPooledSlotsPerHost || profile.MemoryBytesPerHost <= 0 {
		return nil, javarunner.ErrHostState
	}
	if s.containmentUnavailable() {
		return nil, javarunner.ErrProcessContainmentFailed
	}
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	owner, code := s.currentOwnerLocked(ownerID, epoch)
	if code != "" {
		return nil, javarunner.ErrHostFence
	}
	// Publish under the same admission lock that shutdown closes before taking
	// its cleanup snapshot. Otherwise shutdown could miss a just-created run.
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if s.draining.Load() {
		return nil, javarunner.ErrHostFence
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.pooledMu.Lock()
	defer s.pooledMu.Unlock()
	key := pooledRunKey{owner, execution, group}
	if s.pooledRuns[key] != nil {
		return nil, javarunner.ErrHostIdentity
	}
	if len(s.pooledRuns) >= maxPooledRuns {
		return nil, javarunner.ErrHostCapacity
	}
	ledger, err := s.hostAdmission.NewHostLedger(javarunner.HostBinding{OwnerID: owner.ownerID, OwnerEpoch: owner.epoch, ExecutionID: execution, GroupID: group, ConfigurationSHA256: configuration}, budget, s.runtimeLeaseTTL)
	if err != nil {
		return nil, err
	}
	supervisor, err := javarunner.NewHostSupervisor(ledger, backend, true)
	if err != nil {
		return nil, err
	}
	run := &PooledRun{server: s, owner: owner, ledger: ledger, supervisor: supervisor, profile: profile, execution: execution, group: group, closed: make(chan struct{}), deadline: time.Now().Add(s.runtimeLeaseTTL)}
	s.pooledRuns[key] = run
	run.mu.Lock()
	run.armLocked()
	run.mu.Unlock()
	return run, nil
}

func (r *PooledRun) begin(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.server.containmentUnavailable() {
		return nil, javarunner.ErrProcessContainmentFailed
	}
	owner, code := r.server.beginOwnerOperation(r.owner.ownerID, r.owner.epoch)
	if code != "" {
		return nil, javarunner.ErrHostFence
	}
	r.mu.Lock()
	expired := r.closing || !time.Now().Before(r.deadline)
	r.mu.Unlock()
	if expired {
		owner.operations.Done()
		r.startCleanup()
		return nil, javarunner.ErrHostFence
	}
	return owner.operations.Done, nil
}

func (r *PooledRun) Reserve(ctx context.Context, vusers int, prepareFor time.Duration) ([]javarunner.HostAllocation, error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	return r.ledger.Reserve(r.owner.epoch, vusers, r.profile, prepareFor)
}

func (r *PooledRun) Prepare(ctx context.Context, reservation string, values map[string]map[string]string) error {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return r.supervisor.Prepare(ctx, r.owner.epoch, reservation, values)
}

func (r *PooledRun) Commit(ctx context.Context, reservation string) error {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return r.supervisor.Commit(ctx, r.owner.epoch, reservation)
}

// Abort seals the reservation. The independent supervisor performs containment
// cleanup; this acknowledgement is not proof that prepared hosts have stopped.
func (r *PooledRun) Abort(ctx context.Context, reservation string) error {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return r.ledger.Abort(r.owner.epoch, reservation)
}

func (r *PooledRun) Invoke(ctx context.Context, host, slot, method string, params map[string]string) (javarunner.PooledFrame, error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return javarunner.PooledFrame{}, err
	}
	defer finish()
	return r.supervisor.Invoke(ctx, r.owner.epoch, host, slot, method, params)
}

func (r *PooledRun) CloseSlot(ctx context.Context, host, slot string) error {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return r.supervisor.CloseSlot(ctx, r.owner.epoch, host, slot)
}

func (r *PooledRun) Diagnostics(ctx context.Context, host string) (javarunner.HostDiagnostics, error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return javarunner.HostDiagnostics{}, err
	}
	defer finish()
	if !r.controlDecision.PreparationEnabled {
		return javarunner.HostDiagnostics{}, errPooledPreparationDisabled
	}
	return r.supervisor.Diagnostics(ctx, r.owner.epoch, host)
}

func (r *PooledRun) Renew(ctx context.Context) error {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || !time.Now().Before(r.deadline) {
		return javarunner.ErrHostFence
	}
	if err := r.ledger.Renew(r.owner.epoch, r.server.runtimeLeaseTTL); err != nil {
		return err
	}
	r.deadline = time.Now().Add(r.server.runtimeLeaseTTL)
	r.armLocked()
	return nil
}

func (r *PooledRun) armLocked() {
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timerSeq++
	sequence := r.timerSeq
	r.timer = time.AfterFunc(max(time.Until(r.deadline), time.Nanosecond), func() {
		r.mu.Lock()
		if r.closing || sequence != r.timerSeq {
			r.mu.Unlock()
			return
		}
		if time.Now().Before(r.deadline) {
			r.armLocked()
			r.mu.Unlock()
			return
		}
		// Mark closing while holding the same lock as renewal. A timer that
		// already observed expiry must never close a subsequently renewed lease.
		r.startCleanupLocked()
		r.mu.Unlock()
	})
}

func (r *PooledRun) startCleanup() {
	r.mu.Lock()
	r.startCleanupLocked()
	r.mu.Unlock()
}

func (r *PooledRun) startCleanupLocked() {
	if r.closing {
		return
	}
	r.closing = true
	r.timerSeq++
	if r.timer != nil {
		r.timer.Stop()
	}
	_ = r.ledger.Retire(r.owner.epoch)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), r.server.runtimeLeaseCleanupDeadline)
		defer cancel()
		r.cleanupErr = r.supervisor.Close(ctx)
		if r.cleanupErr != nil {
			r.server.finishProcessCapacity(nil, r.cleanupErr)
			r.server.startShutdown()
		}
		close(r.closed)
	}()
}

// Close starts runner-owned cleanup even if the caller has disconnected. A
// cancelled wait does not release reservations or cancel the cleanup worker.
func (r *PooledRun) Close(ctx context.Context) error {
	r.startCleanup()
	select {
	case <-r.closed:
		return r.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) selectedPooledRuns(owner *ownerGeneration) []*PooledRun {
	s.pooledMu.Lock()
	defer s.pooledMu.Unlock()
	var result []*PooledRun
	for key, run := range s.pooledRuns {
		if owner == nil || key.owner == owner {
			result = append(result, run)
		}
	}
	return result
}

func (s *Server) hasPooledRuns(owner *ownerGeneration) bool {
	for _, run := range s.selectedPooledRuns(owner) {
		select {
		case <-run.closed:
			if run.cleanupErr != nil {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func (s *Server) startPooledRunCleanup(owner *ownerGeneration) {
	for _, run := range s.selectedPooledRuns(owner) {
		run.startCleanup()
	}
}

func (s *Server) waitPooledRunCleanup(owner *ownerGeneration) error {
	var result error
	for _, run := range s.selectedPooledRuns(owner) {
		<-run.closed
		result = errors.Join(result, run.cleanupErr)
	}
	// Keep current-epoch tombstones to prevent initialization replay. Successful
	// old-epoch cleanup may be forgotten only after takeover has fenced its owner.
	if result == nil && owner != nil {
		s.forgetPooledRuns(owner)
	}
	return result
}

func (s *Server) forgetPooledRuns(owner *ownerGeneration) {
	s.pooledMu.Lock()
	defer s.pooledMu.Unlock()
	for key := range s.pooledRuns {
		if key.owner == owner {
			delete(s.pooledRuns, key)
		}
	}
}
