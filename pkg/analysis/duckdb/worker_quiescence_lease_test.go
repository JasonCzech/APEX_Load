package duckdb

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNativeQuiescenceRetainsGateAcrossActualIOJoinAndMetadataOutage(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	physical, err := p.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := p.RetainWriterQuiescence()
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Resolve(); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("premature metadata resolution discarded active writer", err)
	}
	select {
	case <-lease.PhysicalDone():
		t.Fatal("active artifact writer became proof")
	default:
	}
	if _, err = p.AcquireIO(); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("quiescence hold admitted a replacement writer", err)
	}
	if err = p.Release(); !errors.Is(err, errWorkerOwnership) || released.Load() != 0 {
		t.Fatal("unknown metadata/active writer freed gate", err)
	}
	physical.Joined()
	select {
	case <-lease.PhysicalDone():
	case <-time.After(time.Second):
		t.Fatal("actual writer join did not expose physical proof")
	}
	select {
	case <-p.Done():
		t.Fatal("physical proof incorrectly included gate release")
	default:
	}
	if released.Load() != 0 {
		t.Fatal("joined writers forgot unresolved durable proof")
	}
	// Model a store outage by retaining this exact hold through repeated attempts.
	if duplicate, err := p.RetainWriterQuiescence(); duplicate != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("duplicate metadata owner admitted", err)
	}
	if err = lease.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Resolve(); err != nil {
		t.Fatal("exact durable resolution was not idempotent", err)
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("durable proof resolution did not release physical continuation")
	}
	if released.Load() != 1 {
		t.Fatal("parent gate released more than once")
	}
	if _, err = p.AcquireIO(); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("durable resolution reopened writers", err)
	}
}
func TestNativeQuiescenceWaitsForActualMonitorDescriptorClosure(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	p.monitorDone = make(chan struct{})
	p.monitorStop = make(chan struct{})
	entered, resume := make(chan struct{}), make(chan struct{})
	var resumeOnce, enteredOnce sync.Once
	unblock := func() { resumeOnce.Do(func() { close(resume) }) }
	t.Cleanup(func() {
		unblock()
		p.cancel()
		select {
		case <-p.monitorDone:
		case <-time.After(time.Second):
			t.Error("exact controlled monitor failed cleanup join")
		}
	})
	go p.watchResources("unused", func(io *NativeIOLease, _ string, _ []string, _ int64, _ bool) error {
		enteredOnce.Do(func() { close(entered) })
		<-resume
		return nil
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("owned monitor did not enter")
	}
	lease, err := p.RetainWriterQuiescence()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.PhysicalDone():
		t.Fatal("blocked monitor became physical proof")
	default:
	}
	if err = lease.Resolve(); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("blocked monitor allowed durable hold release", err)
	}
	unblock()
	select {
	case <-lease.PhysicalDone():
	case <-time.After(time.Second):
		t.Fatal("actual monitor join did not finish quiescence")
	}
	if workerContextError(p.ctx) != nil {
		t.Fatal("quiescence monitor stop revoked success-path SQL context")
	}
	if released.Load() != 0 {
		t.Fatal("monitor proof prematurely freed admission")
	}
	if err = lease.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err = p.Release(); err != nil {
		t.Fatal(err)
	}
}
func TestNativeQuiescenceJoinsChildIdentityBeforeMetadataProof(t *testing.T) {
	p, _ := workerPipelineTestFixture(t)
	child := &workerAttempt{}
	p.attempts[child] = struct{}{}
	lease, err := p.RetainWriterQuiescence()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.PhysicalDone():
		t.Fatal("child entry was ignored")
	default:
	}
	p.mu.Lock()
	delete(p.attempts, child)
	p.notifyReleaseLocked()
	p.mu.Unlock()
	select {
	case <-lease.PhysicalDone():
	case <-time.After(time.Second):
		t.Fatal("exact child release did not expose proof")
	}
	if err = lease.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err = p.Release(); err != nil {
		t.Fatal(err)
	}
}
func TestNativeQuiescenceUnresolvedMetadataProtectsExactStaging(t *testing.T) {
	p, released := workerPipelineTestFixture(t)
	directory := filepath.Join(t.TempDir(), "exact-owned-staging")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	p.stagingDirs = []string{directory}
	p.stagingClean = false
	lease, err := p.RetainWriterQuiescence()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Release(); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("unresolved SQL hold returned released", err)
	}
	if _, err = os.Stat(directory); err != nil || released.Load() != 0 {
		t.Fatal("metadata outage deleted authoritative staging", err)
	}
	if err = lease.Resolve(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("resolved exact staging did not clean")
	}
	if _, err = os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("resolved exact staging remained", err)
	}
}
func TestNativeQuiescenceRefusesAlreadyReleasedOrForeignMetadataOwner(t *testing.T) {
	p, _ := workerPipelineTestFixture(t)
	if err := p.Release(); err != nil {
		t.Fatal(err)
	}
	if lease, err := p.RetainWriterQuiescence(); lease != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("released gate was resurrected", err)
	}
	other, _ := workerPipelineTestFixture(t)
	exact, err := other.RetainWriterQuiescence()
	if err != nil {
		t.Fatal(err)
	}
	foreign := &NativeQuiescenceLease{pipeline: other, physicalDone: make(chan struct{}), physicalJoined: true}
	if err = foreign.Resolve(); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("foreign metadata resolver cleared exact hold", err)
	}
	if err = exact.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err = other.Release(); err != nil {
		t.Fatal(err)
	}
	if (&NativeQuiescenceLease{}).PhysicalDone() != nil {
		t.Fatal("missing authority returned a closed physical proof")
	}
}

func TestNativeQuiescenceMustResolveDurableProofBeforePublicationAdmission(t *testing.T) {
	p, _ := workerPipelineTestFixture(t)
	lease, err := p.RetainWriterQuiescence()
	if err != nil {
		t.Fatal(err)
	}
	<-lease.PhysicalDone()
	if publication, err := p.RetainPublication(); publication != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("publication overtook unresolved durable physical proof", err)
	}
	// Only the trusted adapter's successful durable Confirm may drive this call.
	if err = lease.Resolve(); err != nil {
		t.Fatal(err)
	}
	publication, err := p.RetainPublication()
	if err != nil || publication == nil {
		t.Fatal("resolved durable proof blocked metadata publication", err)
	}
	if _, err = p.AcquireIO(); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("metadata transition reopened writer admission", err)
	}
	publication.Resolve()
	if err = p.Release(); err != nil {
		t.Fatal(err)
	}
}
