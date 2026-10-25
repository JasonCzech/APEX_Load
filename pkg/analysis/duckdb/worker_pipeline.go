package duckdb

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// PipelinePlan is approved parent resource geometry. It grants no artifact,
// project or SQL authority; API/storage authorization remains independent.
// StagingBytes reserves the API's pre-worker copies in addition to worker input.
type PipelinePlan struct {
	InputBytes, OutputBytes, StagingBytes int64
	// Zero selects the approved published defaults. Smaller parent-approved
	// profiles reserve only their declared memory/spill and never expand later.
	MemoryBytes, SpillBytes int64
	Runtime                 time.Duration
}
type NativePipeline struct {
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelFunc
	limits             workerLimits
	stagingBytes       int64
	attempts           map[*workerAttempt]struct{}
	parentIO           map[*NativeIOLease]struct{}
	released           bool
	releaseTimedOut    bool
	pendingPublication *NativePublicationLease
	pendingQuiescence  *NativeQuiescenceLease
	quiescenceStarted  bool
	monitorStop        chan struct{}
	monitorStopOnce    sync.Once
	publicationStarted bool
	writersSealed      bool
	releaseGate        func()
	stagingDirs        []string
	monitorDone        chan struct{}
	monitorLease       *NativeIOLease
	monitorStopped     bool
	resourceErr        error
	continuationOnce   sync.Once
	wake               chan struct{}
	stagingClean       bool
	done               chan struct{}
}
type nativePipelineKey struct{}

var nativePipelineGate = make(chan struct{}, 1)

// NativeBusy reports a retained parent reservation, including unresolved reap
// and publication. API admission consults it before any other heavy analysis.
func NativeBusy() bool { return len(nativePipelineGate) != 0 }

// ReserveNativePipeline must precede download/materialization. The returned
// context is the only way Engine operations borrow its coherent reservation.
func ReserveNativePipeline(ctx context.Context, parent string, plan PipelinePlan) (context.Context, *NativePipeline, error) {
	if !Available() {
		return ctx, nil, ErrUnavailable
	}
	if workerContextError(ctx) != nil || plan.InputBytes < 12 || plan.InputBytes > 20<<30 || plan.OutputBytes < 12 || plan.OutputBytes > 20<<30 || plan.StagingBytes < 0 || plan.StagingBytes > 20<<30 {
		return ctx, nil, errWorkerProtocol
	}
	if ctx.Value(nativePipelineKey{}) != nil {
		return ctx, nil, errWorkerOwnership
	}
	limits, err := workerPipelineLimits(plan)
	if err != nil {
		return ctx, nil, err
	}
	select {
	case nativePipelineGate <- struct{}{}:
	default:
		return ctx, nil, ErrBusy
	}
	release := sync.OnceFunc(func() { <-nativePipelineGate })
	probeLimits := limits
	probeLimits.InputBytes, err = workerAdd(probeLimits.InputBytes, plan.StagingBytes)
	if err != nil || workerAdmissionHeadroom(parent, workerBootstrap{Limits: probeLimits}) != nil || workerContextError(ctx) != nil {
		release()
		return ctx, nil, errWorkerResourceLimit
	}
	childCtx, cancel := context.WithTimeout(ctx, time.Duration(limits.RuntimeMS)*time.Millisecond)
	p := &NativePipeline{ctx: childCtx, cancel: cancel, limits: limits, stagingBytes: plan.StagingBytes, attempts: map[*workerAttempt]struct{}{}, parentIO: map[*NativeIOLease]struct{}{}, releaseGate: release, wake: make(chan struct{}, 1), stagingClean: true, done: make(chan struct{})}
	p.releaseGate = sync.OnceFunc(func() { release(); close(p.done) })
	p.monitorDone = make(chan struct{})
	p.monitorStop = make(chan struct{})
	go p.watchResources(parent, workerObservePipelineResources)
	return context.WithValue(childCtx, nativePipelineKey{}, p), p, nil
}

// approveSourceGeometry is private parent-only preflight. A small initial
// reservation owns metadata descriptors; actual bytes are admitted before any
// source hashing/staging. Borrowed API reservations cannot use this expansion.
func (p *NativePipeline) approveSourceGeometry(parent string, input, output int64) error {
	return p.admitGeometry(parent, input, output, nil)
}

// AdmitGeometry is called by an authorized parent after owned metadata preflight
// and before downloading any bytes. It grants only bounded resource geometry;
// no artifact, project, executable, SQL or source identity is inferred from it.
func (p *NativePipeline) AdmitGeometry(parent string, input, output, staging int64) error {
	if staging < 0 || staging > 20<<30 {
		return errWorkerResourceLimit
	}
	return p.admitGeometry(parent, input, output, &staging)
}
func (p *NativePipeline) admitGeometry(parent string, input, output int64, approvedStaging *int64) error {
	if p == nil {
		return errWorkerOwnership
	}
	p.mu.Lock()
	if p.released || p.writersSealed || p.publicationStarted || len(p.attempts) != 0 || workerContextError(p.ctx) != nil || input < 12 || input > 20<<30 || output < 12 || output > 20<<30 {
		p.mu.Unlock()
		return errWorkerOwnership
	}
	before, beforeStaging := p.limits, p.stagingBytes
	p.mu.Unlock()
	staging := beforeStaging
	if approvedStaging != nil {
		staging = *approvedStaging
	}
	limits := before
	limits.InputBytes, limits.OutputBytes = input, output
	probe := limits
	var err error
	probe.InputBytes, err = workerAdd(input, staging)
	// Filesystem/process probes never hold the lifecycle mutex; a blocked probe
	// cannot trap cancellation or physical Release of the admitted metadata lane.
	if err != nil || workerAdmissionHeadroom(parent, workerBootstrap{Limits: probe}) != nil {
		return errWorkerResourceLimit
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released || p.writersSealed || p.publicationStarted || len(p.attempts) != 0 || workerContextError(p.ctx) != nil || p.limits != before || p.stagingBytes != beforeStaging {
		return errWorkerOwnership
	}
	p.limits, p.stagingBytes = limits, staging
	return nil
}

// Done closes only after actual child/reader/writer joins, publication
// reconciliation and private staging removal have released parent admission.
func (p *NativePipeline) Done() <-chan struct{} {
	if p == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return p.done
}
func (p *NativePipeline) WritersQuiescent() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.attempts) == 0 && len(p.parentIO) == 0
}

// SealWriters atomically observes actual artifact/child joins and permanently
// refuses new artifact I/O, geometry changes and child admission. The caller
// must cross this boundary before persisting any durable writers-quiesced seal.
// Cancellation and metadata reconciliation remain allowed; a failed SQL call
// cannot reopen the same pipeline's physical writer admission.
func (p *NativePipeline) SealWriters() error {
	if p == nil {
		return errWorkerOwnership
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.attempts) != 0 || len(p.parentIO) != 0 {
		return errWorkerOwnership
	}
	p.writersSealed = true
	return nil
}

// Release revokes work immediately but cannot free admission before physical
// child/pipe/output-reader proof. An unresolved attempt remains charged even
// if its HTTP request/controller lease has ended.
func (p *NativePipeline) Release() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.cancel == nil || p.done == nil || p.releaseGate == nil {
		p.mu.Unlock()
		return errWorkerOwnership
	}
	p.released = true
	p.cancel()
	done := p.done
	// A pending publication or quiescence holds the pipeline on purpose until
	// the reconciler resolves it, which no wait here can shorten. A pipeline
	// still held after one full wait is stuck: the reconciler's later releases
	// keep the short wait and stay quiet, as the first timeout said why.
	short := p.pendingPublication != nil || p.pendingQuiescence != nil || p.releaseTimedOut
	p.mu.Unlock()
	p.retainReleaseContinuation()
	wait := workerReleaseWait()
	if short {
		wait = min(wait, time.Second)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		if short {
			return errWorkerOwnership
		}
		p.mu.Lock()
		p.releaseTimedOut = true
		attempts, parentIO, publication, quiescence, monitorRunning, stagingClean := len(p.attempts), len(p.parentIO), p.pendingPublication != nil, p.pendingQuiescence != nil, p.monitorDone != nil && !p.monitorStopped, p.stagingClean
		p.mu.Unlock()
		slog.Warn("native worker ownership unresolved", "cause", "pipeline_release_timeout", "attempts", attempts, "parent_io", parentIO, "publication_pending", publication, "quiescence_pending", quiescence, "monitor_running", monitorRunning, "staging_clean", stagingClean, "waited", wait)
		return errWorkerOwnership
	}
}

func (p *NativePipeline) mayReleaseGateLocked() bool {
	return p.released && len(p.attempts) == 0 && len(p.parentIO) == 0 && p.pendingPublication == nil && p.pendingQuiescence == nil && (p.monitorDone == nil || p.monitorStopped) && p.stagingClean
}
func (p *NativePipeline) notifyReleaseLocked() {
	p.tryWriterQuiescenceLocked()
	if p.wake != nil {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

var nativePipelineQuarantine sync.Map

func (p *NativePipeline) retainReleaseContinuation() {
	p.mu.Lock()
	if p.wake == nil {
		p.wake = make(chan struct{}, 1)
	}
	p.mu.Unlock()
	p.continuationOnce.Do(func() {
		nativePipelineQuarantine.Store(p, p)
		go func() {
			if p.monitorDone != nil {
				<-p.monitorDone
			}
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				p.mu.Lock()
				p.monitorStopped = true
				ready := p.released && len(p.attempts) == 0 && len(p.parentIO) == 0 && p.pendingPublication == nil && p.pendingQuiescence == nil
				directories := append([]string{}, p.stagingDirs...)
				p.mu.Unlock()
				if ready {
					// All physical owners have joined. Filesystem cleanup runs in this owned
					// continuation outside mu, so even an uncooperative remove cannot trap an
					// API Release/receipt resolver beyond its bounded ownership window.
					removed := true
					for _, directory := range directories {
						if os.RemoveAll(directory) != nil {
							removed = false
						}
					}
					if removed {
						p.mu.Lock()
						p.stagingClean = true
						p.releaseGate()
						p.mu.Unlock()
						nativePipelineQuarantine.Delete(p)
						return
					}
				}
				select {
				case <-p.wake:
				case <-ticker.C:
				}
			}
		}()
	})
}

func workerPipelineLimits(plan PipelinePlan) (workerLimits, error) {
	memory, spill, runtime := plan.MemoryBytes, plan.SpillBytes, plan.Runtime
	if memory == 0 {
		memory = 2 << 30
	}
	if spill == 0 {
		spill = 8 << 30
	}
	if runtime == 0 {
		runtime = 15 * time.Minute
	}
	if memory < 1 || memory > 2<<30 || spill < 1 || spill > 8<<30 || runtime < time.Millisecond || runtime > 15*time.Minute {
		return workerLimits{}, errWorkerResourceLimit
	}
	return workerLimits{Threads: 2, MemoryBytes: memory, SpillBytes: spill, InputBytes: plan.InputBytes, OutputBytes: plan.OutputBytes, RuntimeMS: runtime.Milliseconds()}, nil
}
func (p *NativePipeline) watchResources(parent string, probe func(*NativeIOLease, string, []string, int64, bool) error) {
	// A probe has one exact descriptor lease. It is distinct from artifact I/O,
	// but its physical join still precedes monitorDone and scratch/gate release.
	var lease *NativeIOLease
	defer func() {
		if lease != nil {
			lease.Joined()
			<-lease.Done()
		}
		close(p.monitorDone)
		p.mu.Lock()
		p.monitorStopped = true
		p.notifyReleaseLocked()
		p.mu.Unlock()
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.monitorStop:
			return
		case <-ticker.C:
			p.mu.Lock()
			dirs := append([]string{}, p.stagingDirs...)
			active := len(p.attempts) != 0
			quota, quotaErr := workerPipelineDiskBytes(p.limits, p.stagingBytes)
			lease = &NativeIOLease{pipeline: p, done: make(chan struct{})}
			p.monitorLease = lease
			p.mu.Unlock()
			err := quotaErr
			if err == nil {
				err = probe(lease, parent, dirs, quota, active)
			}
			lease.Joined()
			if err != nil {
				p.mu.Lock()
				p.resourceErr = errWorkerResourceLimit
				p.cancel()
				p.mu.Unlock()
				return
			}
			select {
			case <-lease.Done():
			case <-p.ctx.Done():
				return
			}
			p.mu.Lock()
			p.monitorLease = nil
			p.mu.Unlock()
			lease = nil
		}
	}
}

// Err exposes the fixed resource class after a parent staging watchdog abort.
func (p *NativePipeline) Err() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resourceErr
}

func (p *NativePipeline) reserve(ctx context.Context, b workerBootstrap) (func(), error) {
	if p == nil || b.validate() != nil {
		return nil, errWorkerProtocol
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released || p.writersSealed || p.publicationStarted || p.pendingPublication != nil || len(p.attempts) != 0 || workerContextError(p.ctx) != nil || workerContextError(ctx) != nil {
		return nil, errWorkerOwnership
	}
	if b.Limits.Threads > p.limits.Threads || b.Limits.MemoryBytes > p.limits.MemoryBytes || b.Limits.SpillBytes > p.limits.SpillBytes || b.Limits.InputBytes > p.limits.InputBytes || b.Limits.OutputBytes > p.limits.OutputBytes || b.Limits.RuntimeMS > p.limits.RuntimeMS {
		return nil, errWorkerResourceLimit
	}
	// Registration occurs before scratch creation. The slot sentinel is replaced
	// by bindAttempt synchronously; no other attempt may stage concurrently.
	p.attempts[nil] = struct{}{}
	return sync.OnceFunc(func() {
		p.mu.Lock()
		delete(p.attempts, nil)
		p.notifyReleaseLocked()
		p.mu.Unlock()
	}), nil
}
func (p *NativePipeline) bindAttempt(a *workerAttempt) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.attempts[nil]; !ok || p.released || p.writersSealed || p.publicationStarted || workerContextError(p.ctx) != nil {
		return errWorkerOwnership
	}
	delete(p.attempts, nil)
	p.attempts[a] = struct{}{}
	a.releaseAdmission = sync.OnceFunc(func() {
		p.mu.Lock()
		delete(p.attempts, a)
		p.notifyReleaseLocked()
		p.mu.Unlock()
	})
	return nil
}
func workerPipelineForContext(ctx context.Context) *NativePipeline {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(nativePipelineKey{}).(*NativePipeline)
	return p
}
func workerPipelineAttempt(ctx context.Context, parent string, b workerBootstrap) (*workerAttempt, *NativePipeline, error) {
	p := workerPipelineForContext(ctx)
	owned := false
	if p == nil {
		var err error
		ctx, p, err = ReserveNativePipeline(ctx, parent, PipelinePlan{InputBytes: b.Limits.InputBytes, OutputBytes: b.Limits.OutputBytes, MemoryBytes: b.Limits.MemoryBytes, SpillBytes: b.Limits.SpillBytes, Runtime: time.Duration(b.Limits.RuntimeMS) * time.Millisecond})
		if err != nil {
			return nil, nil, err
		}
		owned = true
	}
	a, err := newWorkerAttempt(ctx, parent, b, p)
	if err != nil {
		if owned {
			_ = p.Release()
		}
		return nil, nil, err
	}
	if err = p.bindAttempt(a); err != nil {
		_ = a.release()
		if owned {
			_ = p.Release()
		}
		return nil, nil, err
	}
	if owned {
		a.privatePipeline = p
	}
	return a, p, nil
}
func workerFinishAttempt(a *workerAttempt) error {
	if a == nil {
		return nil
	}
	err := a.release()
	if a.privatePipeline != nil {
		err = errors.Join(err, a.privatePipeline.Release())
	}
	return err
}

// Only a live reservation may create API scratch; ownership does not come from
// its mtime. The API must retain this directory until all attempts are reaped.
func (p *NativePipeline) CreateStagingDirectory(parent string) (string, error) {
	lease, err := p.AcquireIO()
	if err != nil {
		return "", err
	}
	// The admission lease exists before filesystem calls, which never hold mu.
	dir, err := workerCreateScratchDirectory(parent, "job-", lease)
	p.mu.Lock()
	if dir != "" {
		p.stagingDirs = append(p.stagingDirs, dir)
		p.stagingClean = false
	}
	p.mu.Unlock()
	lease.Joined()
	return dir, err
}

// An opaque parent I/O fence spans the actual read/write descriptor lifetime.
// It is completed only after physical Close/join proof, never by cancellation.
type NativeIOLease struct {
	pipeline      *NativePipeline
	once          sync.Once
	unjoined      []io.Closer
	owners        []io.Closer
	physical      map[io.Closer]*workerDescriptorOwner
	joinRequested bool
	done          chan struct{}
}

// TrackOwner retains each exact descriptor immediately after its open, while
// its opaque lease is still admitted. CloseOwner supplies actual join proof.
func (lease *NativeIOLease) TrackOwner(descriptor io.Closer) {
	if lease == nil || lease.pipeline == nil || descriptor == nil {
		return
	}
	p := lease.pipeline
	p.mu.Lock()
	defer p.mu.Unlock()
	lease.trackOwnerLocked(descriptor)
}
func (lease *NativeIOLease) trackOwnerLocked(descriptor io.Closer) *workerDescriptorOwner {
	if lease.physical == nil {
		lease.physical = map[io.Closer]*workerDescriptorOwner{}
	}
	if owner := lease.physical[descriptor]; owner != nil {
		return owner
	}
	owner := &workerDescriptorOwner{descriptor: descriptor, done: make(chan struct{})}
	lease.physical[descriptor] = owner
	lease.owners = append(lease.owners, descriptor)
	return owner
}

// CloseOwner keeps one actual Close in a retained goroutine. Its bounded waiter
// may report unresolved ownership; only the actual nil result seals the owner.
func (lease *NativeIOLease) CloseOwner(descriptor io.Closer) error {
	if lease == nil || lease.pipeline == nil || descriptor == nil {
		return errWorkerOwnership
	}
	p := lease.pipeline
	p.mu.Lock()
	owner := lease.trackOwnerLocked(descriptor)
	p.mu.Unlock()
	owner.once.Do(func() {
		go func() {
			var err error
			func() {
				defer func() {
					if recover() != nil {
						err = errWorkerOwnership
					}
				}()
				err = descriptor.Close()
			}()
			p.mu.Lock()
			owner.err = err
			owner.joined = err == nil
			close(owner.done)
			lease.tryJoinedLocked()
			p.mu.Unlock()
		}()
	})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-owner.done:
		return owner.err
	case <-timer.C:
		return errWorkerOwnership
	}
}

// RetainUnjoined remains the trusted external adapter's exact failed-close
// receipt. It is never cleared merely by a cancellation or Joined call.
func (lease *NativeIOLease) RetainUnjoined(owner io.Closer) {
	if lease == nil || lease.pipeline == nil || owner == nil {
		return
	}
	p := lease.pipeline
	p.mu.Lock()
	defer p.mu.Unlock()
	lease.unjoined = append(lease.unjoined, owner)
}
func (p *NativePipeline) AcquireIO() (*NativeIOLease, error) {
	if p == nil {
		return nil, errWorkerOwnership
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released || p.writersSealed || p.publicationStarted || workerContextError(p.ctx) != nil {
		return nil, errWorkerOwnership
	}
	if len(p.parentIO) >= 128 {
		return nil, errWorkerResourceLimit
	}
	if p.parentIO == nil {
		p.parentIO = map[*NativeIOLease]struct{}{}
	}
	lease := &NativeIOLease{pipeline: p, done: make(chan struct{})}
	p.parentIO[lease] = struct{}{}
	return lease, nil
}
func NativePipelineIO(ctx context.Context) (*NativeIOLease, error) {
	p := workerPipelineForContext(ctx)
	if p == nil {
		return nil, nil
	}
	return p.AcquireIO()
}
func (lease *NativeIOLease) Joined() {
	if lease == nil || lease.pipeline == nil {
		return
	}
	p := lease.pipeline
	p.mu.Lock()
	defer p.mu.Unlock()
	lease.joinRequested = true
	lease.tryJoinedLocked()
}
func (lease *NativeIOLease) tryJoinedLocked() {
	if !lease.joinRequested || len(lease.unjoined) != 0 {
		return
	}
	for _, owner := range lease.physical {
		if !owner.joined {
			return
		}
	}
	lease.once.Do(func() {
		delete(lease.pipeline.parentIO, lease)
		lease.owners = nil
		if lease.done != nil {
			close(lease.done)
		}
		lease.pipeline.notifyReleaseLocked()
	})
}
func (lease *NativeIOLease) pipelineDiskBytes() (int64, error) {
	if lease == nil || lease.pipeline == nil {
		return 0, errWorkerOwnership
	}
	p := lease.pipeline
	p.mu.Lock()
	defer p.mu.Unlock()
	return workerPipelineDiskBytes(p.limits, p.stagingBytes)
}

func (lease *NativeIOLease) Done() <-chan struct{} {
	if lease == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return lease.done
}

// One opaque single-owner lease is acquired before the publication call. A
// released pipeline cannot regain admission, and a stale/duplicate resolver
// cannot clear another publication. Exactly one publication exists per import.
type NativePublicationLease struct {
	pipeline *NativePipeline
	once     sync.Once
}

func (p *NativePipeline) RetainPublication() (*NativePublicationLease, error) {
	if p == nil {
		return nil, errWorkerOwnership
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released || p.publicationStarted || p.pendingPublication != nil || p.pendingQuiescence != nil || len(p.attempts) != 0 || len(p.parentIO) != 0 || workerContextError(p.ctx) != nil {
		return nil, errWorkerOwnership
	}
	lease := &NativePublicationLease{pipeline: p}
	p.pendingPublication = lease
	p.writersSealed = true
	p.publicationStarted = true
	return lease, nil
}
func (lease *NativePublicationLease) Resolve() {
	if lease == nil || lease.pipeline == nil {
		return
	}
	lease.once.Do(func() {
		p := lease.pipeline
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.pendingPublication != lease {
			return
		}
		p.pendingPublication = nil
		if p.mayReleaseGateLocked() {
			p.releaseGate()
		}
		p.notifyReleaseLocked()
	})
}

func (lease *NativeIOLease) forgetJoinedOwner(descriptor io.Closer) {
	if lease == nil || lease.pipeline == nil {
		return
	}
	p := lease.pipeline
	p.mu.Lock()
	defer p.mu.Unlock()
	if owner := lease.physical[descriptor]; owner == nil || !owner.joined {
		return
	}
	delete(lease.physical, descriptor)
	for i, owner := range lease.owners {
		if owner == descriptor {
			copy(lease.owners[i:], lease.owners[i+1:])
			lease.owners[len(lease.owners)-1] = nil
			lease.owners = lease.owners[:len(lease.owners)-1]
			break
		}
	}
}
