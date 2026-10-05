package duckdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func workerPipelineTestFixture(t *testing.T) (*NativePipeline, *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	released := &atomic.Int32{}
	p := &NativePipeline{ctx: ctx, cancel: cancel, attempts: map[*workerAttempt]struct{}{}, stagingClean: true, done: make(chan struct{})}
	p.releaseGate = sync.OnceFunc(func() { released.Add(1); close(p.done) })
	return p, released
}
func TestNativePublicationLeaseCannotResurrectReleasedAdmission(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	if err := p.Release(); err != nil || released.Load() != 1 {
		t.Fatal("release failed", err)
	}
	if lease, err := p.RetainPublication(); lease != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("publication resurrected released capacity", err)
	}
}
func TestNativePublicationLeaseSingleOwnerRetainsCapacityThroughUnknownOutcome(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	lease, err := p.RetainPublication()
	if err != nil {
		t.Fatal(err)
	}
	if other, err := p.RetainPublication(); other != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("duplicate publication owner", err)
	}
	if err = p.Release(); !errors.Is(err, errWorkerOwnership) || released.Load() != 0 {
		t.Fatal("unknown publication freed admission", err)
	}
	lease.Resolve()
	lease.Resolve()
	if released.Load() != 1 {
		t.Fatal("single resolution did not release exactly once")
	}
	if other, err := p.RetainPublication(); other != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("publication restarted after lease end", err)
	}
}
func TestNativePublicationCannotBeginWhilePhysicalAttemptRemains(t *testing.T) {
	p, _ := workerPipelineTestFixture(t)
	p.attempts[&workerAttempt{}] = struct{}{}
	if lease, err := p.RetainPublication(); lease != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("SQL phase began before physical output closure", err)
	}
}

func TestNativePipelineProfilesKeepApprovedSmallerBudgets(t *testing.T) {
	for _, tc := range []struct {
		name          string
		plan          PipelinePlan
		memory, spill int64
		runtime       time.Duration
		bad           bool
	}{
		{name: "defaults", memory: 2 << 30, spill: 8 << 30, runtime: 15 * time.Minute},
		{name: "smaller", plan: PipelinePlan{MemoryBytes: 128 << 20, SpillBytes: 256 << 20, Runtime: time.Second}, memory: 128 << 20, spill: 256 << 20, runtime: time.Second},
		{name: "negative-memory", plan: PipelinePlan{MemoryBytes: -1}, bad: true},
		{name: "expanded-spill", plan: PipelinePlan{SpillBytes: (8 << 30) + 1}, bad: true},
		{name: "too-long", plan: PipelinePlan{Runtime: 15*time.Minute + time.Millisecond}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workerPipelineLimits(tc.plan)
			if tc.bad {
				if !errors.Is(err, errWorkerResourceLimit) {
					t.Fatal("invalid profile accepted", err)
				}
				return
			}
			if err != nil || got.MemoryBytes != tc.memory || got.SpillBytes != tc.spill || got.RuntimeMS != tc.runtime.Milliseconds() {
				t.Fatal("approved geometry changed", got, err)
			}
		})
	}
}
func TestNativePipelineRetainsBlockedStagingReaderAndCleansBeforeAdmissionRelease(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	p.monitorDone = make(chan struct{})
	directory := filepath.Join(t.TempDir(), "owned-stage")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "input.parquet"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	p.stagingDirs = []string{directory}
	p.stagingClean = false
	result := make(chan error, 1)
	go func() { result <- p.Release() }()
	select {
	case err := <-result:
		if !errors.Is(err, errWorkerOwnership) {
			t.Fatal("blocked physical reader reported drained", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Release failed its bounded ownership window")
	}
	if released.Load() != 0 {
		t.Fatal("physical reader lost admission")
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal("owned scratch removed before reader join", err)
	}
	close(p.monitorDone)
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for released.Load() == 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("owned continuation did not finish actual reader join")
		}
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("admission released before scratch was removed", err)
	}
	if err := p.Release(); err != nil || released.Load() != 1 {
		t.Fatal("repeated release duplicated admission", err, released.Load())
	}
}
func TestNativePipelineWatchdogCancelsBeforeNativeLaunch(t *testing.T) {
	p, _ := workerPipelineTestFixture(t)
	p.monitorDone = make(chan struct{})
	entered := make(chan struct{})
	go p.watchResources("unused", func(*NativeIOLease, string, []string, int64, bool) error {
		close(entered)
		return errWorkerResourceLimit
	})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("pre-worker watchdog never observed resources")
	}
	select {
	case <-p.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("pre-worker resource breach did not revoke staging")
	}
	<-p.monitorDone
	if !errors.Is(p.Err(), errWorkerResourceLimit) {
		t.Fatal("fixed resource class missing", p.Err())
	}
	if err := p.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeParentIOFenceBlocksPublicationAndRelease(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	lease, err := p.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	if pub, err := p.RetainPublication(); pub != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("publication preceded physical I/O join", err)
	}
	if err = p.Release(); !errors.Is(err, errWorkerOwnership) || released.Load() != 0 {
		t.Fatal("unjoined I/O released admission", err)
	}
	lease.Joined()
	lease.Joined()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for released.Load() == 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("joined I/O did not release owned continuation")
		}
	}
	if released.Load() != 1 {
		t.Fatal("repeated I/O join released twice")
	}
}

type workerCloseFaultOwner struct{ calls int }

func (owner *workerCloseFaultOwner) Close() error { owner.calls++; return errWorkerOwnership }
func TestNativeIOCloseFaultRetainsExactPhysicalOwner(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	lease, err := p.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	owner := &workerCloseFaultOwner{}
	if owner.Close() == nil {
		t.Fatal("fixture unexpectedly closed")
	}
	lease.RetainUnjoined(owner)
	lease.Joined()
	if p.WritersQuiescent() || len(lease.unjoined) != 1 || lease.unjoined[0] != owner || owner.calls != 1 {
		t.Fatal("failed close lost exact descriptor or forged proof")
	}
	if err = p.Release(); !errors.Is(err, errWorkerOwnership) || released.Load() != 0 {
		t.Fatal("failed close released reservation", err)
	}
	// Simulate a separately trusted physical reconciliation proof only in this
	// package test; production has no API that treats a repeated Close as proof.
	p.mu.Lock()
	lease.unjoined = nil
	p.mu.Unlock()
	lease.Joined()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("qualified join did not finish retained continuation")
	}
}

type workerBlockingCloseOwner struct {
	entered, resume chan struct{}
	once            sync.Once
}

func (owner *workerBlockingCloseOwner) Close() error {
	owner.once.Do(func() { close(owner.entered) })
	<-owner.resume
	return nil
}
func TestNativeIOBlockedCloseRetainsExactOwnerUntilActualCallback(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	lease, err := p.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	owner := &workerBlockingCloseOwner{entered: make(chan struct{}), resume: make(chan struct{})}
	var resumeOnce sync.Once
	unblock := func() { resumeOnce.Do(func() { close(owner.resume) }) }
	defer unblock()
	lease.TrackOwner(owner)
	if !errors.Is(lease.CloseOwner(owner), errWorkerOwnership) {
		t.Fatal("blocked Close forged success")
	}
	lease.Joined()
	if err = p.Release(); !errors.Is(err, errWorkerOwnership) || released.Load() != 0 {
		t.Fatal("blocked physical close released admission", err)
	}
	p.mu.Lock()
	retained := lease.physical[owner]
	p.mu.Unlock()
	if retained == nil || retained.descriptor != owner || retained.joined {
		t.Fatal("exact blocked owner was lost")
	}
	unblock()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("actual close callback did not release retained continuation")
	}
	if err = lease.CloseOwner(owner); err != nil {
		t.Fatal("repeated caller did not reuse exact close proof", err)
	}
	if released.Load() != 1 {
		t.Fatal("callback released admission twice")
	}
}

type workerPipelinePanicCloser struct{ calls atomic.Int32 }

func (c *workerPipelinePanicCloser) Close() error { c.calls.Add(1); panic("unknown physical outcome") }
func TestNativeIOPanickingCloseKeepsExactOwnerAndAdmission(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	lease, err := p.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	c := &workerPipelinePanicCloser{}
	lease.TrackOwner(c)
	if err := lease.CloseOwner(c); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("panic did not become unknown physical outcome", err)
	}
	lease.Joined()
	if err := p.Release(); !errors.Is(err, errWorkerOwnership) || released.Load() != 0 {
		t.Fatal("unknown Close released parent admission", err)
	}
	if err := lease.CloseOwner(c); !errors.Is(err, errWorkerOwnership) || c.calls.Load() != 1 {
		t.Fatal("unknown Close was retried or forgiven", err)
	}
	p.mu.Lock()
	owner := lease.physical[c]
	retained := owner != nil && owner.descriptor == c && !owner.joined
	p.mu.Unlock()
	if !retained {
		t.Fatal("panicking exact descriptor lost its parent")
	}
	// The fixture has no descriptor; this independently trusted cleanup is not
	// an exported runtime recovery API or proof inferred from a second Close.
	p.mu.Lock()
	owner.joined = true
	lease.tryJoinedLocked()
	p.mu.Unlock()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("fixture parent cleanup did not join")
	}
}

func TestNativeWriterSealRefusesNewPhysicalAdmissionThroughPublicationResolution(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	b := workerRuntimeTestBootstrap(t)
	p.limits = b.Limits
	if err := p.SealWriters(); err != nil {
		t.Fatal(err)
	}
	refuse := func() {
		t.Helper()
		if io, err := p.AcquireIO(); io != nil || !errors.Is(err, errWorkerOwnership) {
			t.Fatal("durably sealable pipeline reopened artifact I/O", err)
		}
		if release, err := p.reserve(context.Background(), b); release != nil || !errors.Is(err, errWorkerOwnership) {
			t.Fatal("durably sealable pipeline reopened a child slot", err)
		}
		if err := p.AdmitGeometry(t.TempDir(), 100, 100, 0); !errors.Is(err, errWorkerOwnership) {
			t.Fatal("durably sealable pipeline changed physical geometry", err)
		}
	}
	refuse()
	publication, err := p.RetainPublication()
	if err != nil {
		t.Fatal("physical seal blocked metadata ownership", err)
	}
	refuse() // Includes a retained ambiguous SQL outcome.
	publication.Resolve()
	refuse() // A terminal receipt never reopens the same physical authority.
	if released.Load() != 0 {
		t.Fatal("metadata resolution released still-owned parent")
	}
	if err := p.Release(); err != nil || released.Load() != 1 {
		t.Fatal("sealed pipeline did not release once", err)
	}
}

func TestNativeWriterSealRequiresActualChildAndIOJoins(t *testing.T) {
	p, _ := workerPipelineTestFixture(t)
	io, err := p.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SealWriters(); !errors.Is(err, errWorkerOwnership) || p.writersSealed {
		t.Fatal("unjoined artifact I/O became durable proof", err)
	}
	io.Joined()
	child := &workerAttempt{}
	p.attempts[child] = struct{}{}
	if err := p.SealWriters(); !errors.Is(err, errWorkerOwnership) || p.writersSealed {
		t.Fatal("unjoined child became durable proof", err)
	}
	delete(p.attempts, child) // Fixture-only supplied physical join.
	if err := p.SealWriters(); err != nil || !p.writersSealed {
		t.Fatal("actual joins did not close admission", err)
	}
	if err := p.Release(); err != nil {
		t.Fatal(err)
	}
}
