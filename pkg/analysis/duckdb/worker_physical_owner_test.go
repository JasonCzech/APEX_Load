package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func workerWaitPhysicalRelease(t *testing.T, a *workerAttempt) {
	t.Helper()
	if err := a.release(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerPanickingDescriptorCloseRetainsExactFileAndAdmission(t *testing.T) {
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	file, err := a.root.Open(a.bootstrap.Handles[0].RelativeName)
	if err != nil {
		t.Fatal(err)
	}
	a.trackDescriptor(file)
	defer func() {
		// Separately trusted fixture cleanup closes the real file first. Runtime
		// never reinterprets a second Close as proof of the panicking first call.
		if err := file.Close(); err != nil {
			t.Error(err)
			return
		}
		a.mu.Lock()
		a.descriptors[file].joined = true
		a.notifyPhysicalLocked()
		a.mu.Unlock()
		workerWaitPhysicalRelease(t, a)
	}()
	calls := 0
	a.mu.Lock()
	a.closeDescriptorHook = func(owner io.Closer) error {
		if owner == file {
			calls++
			panic("unknown close result")
		}
		return owner.Close()
	}
	a.mu.Unlock()
	if err := a.closeDescriptor(file); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("panic did not return stable unknown", err)
	}
	if err := a.release(); !errors.Is(err, errWorkerOwnership) || admission.releases.Load() != 0 {
		t.Fatal("panicking descriptor released enclosing admission", err)
	}
	if err := a.closeDescriptor(file); !errors.Is(err, errWorkerOwnership) || calls != 1 {
		t.Fatal("unknown close was retried or forgiven", err)
	}
	a.mu.Lock()
	owner := a.descriptors[file]
	retained := owner != nil && owner.descriptor == file && !owner.joined
	a.mu.Unlock()
	if !retained {
		t.Fatal("panicking exact physical owner was discarded")
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("fixture file was not still physically owned", err)
	}
}

func TestWorkerFailedParentFileCloseRetainsExactOwnerAndScratch(t *testing.T) {
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	file, err := a.root.Open("input-0.parquet")
	if err != nil { // The manifest's approved input name is authoritative.
		file, err = a.root.Open(a.bootstrap.Handles[0].RelativeName)
	}
	if err != nil {
		t.Fatal(err)
	}
	a.trackDescriptor(file)
	a.mu.Lock()
	a.closeDescriptorHook = func(owner io.Closer) error {
		if owner == file {
			return errWorkerOwnership
		}
		return owner.Close()
	}
	a.mu.Unlock()
	if !errors.Is(a.closeDescriptor(file), errWorkerOwnership) {
		t.Fatal("failed close was hidden")
	}
	if !errors.Is(a.release(), errWorkerOwnership) || admission.releases.Load() != 0 {
		t.Fatal("unjoined file released admission")
	}
	a.mu.Lock()
	owner := a.descriptors[file]
	a.mu.Unlock()
	if owner == nil || owner.descriptor != file || owner.joined {
		t.Fatal("exact failed physical descriptor lost")
	}
	if _, err = os.Stat(a.path); err != nil {
		t.Fatal("scratch removed before physical proof", err)
	}
	// A separately trusted physical success is explicitly simulated here. No
	// production retry interprets os.ErrClosed or cancellation as this proof.
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	owner.joined = true
	owner.err = nil
	a.closeDescriptorHook = nil
	a.notifyPhysicalLocked()
	a.mu.Unlock()
	workerWaitPhysicalRelease(t, a)
	if admission.releases.Load() != 1 {
		t.Fatal("actual reconciliation did not release once")
	}
}

type workerBlockedVerificationReader struct {
	reader  io.Reader
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (reader *workerBlockedVerificationReader) Read(body []byte) (int, error) {
	reader.once.Do(func() { close(reader.entered) })
	<-reader.resume
	return reader.reader.Read(body)
}

func TestWorkerBlockedParentVerificationReturnsBoundedAndRetainsDescriptor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, admission := workerRuntimeTestAttempt(t, ctx)
	entered, resume := make(chan struct{}), make(chan struct{})
	resumeOnce := sync.Once{}
	unblock := func() { resumeOnce.Do(func() { close(resume) }) }
	defer unblock()
	a.verifyReadHook = func(input io.Reader) io.Reader {
		return &workerBlockedVerificationReader{reader: input, entered: entered, resume: resume}
	}
	finished := make(chan error, 1)
	go func() {
		_, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testCallerProofWindow: 30 * time.Millisecond})
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("verification did not enter retained descriptor")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, errWorkerOwnership) {
			t.Fatal("blocked read failed to retain ownership", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked hash trapped caller")
	}
	a.mu.Lock()
	retained := false
	for descriptor, owner := range a.descriptors {
		if descriptor != a.root && descriptor != a.directory && !owner.joined {
			retained = true
		}
	}
	a.mu.Unlock()
	if !retained || admission.releases.Load() != 0 {
		t.Fatal("blocked verification descriptor or charge lost")
	}
	unblock()
	// Admit the same cleanup continuation, then snapshot its immutable join
	// channel under the mutex used by release's first allocation. A direct field
	// read races the parent supervisor beginning that exact continuation.
	if err := a.release(); err != nil && !errors.Is(err, errWorkerOwnership) {
		t.Fatal(err)
	}
	a.mu.Lock()
	cleanupDone := a.cleanupDone
	a.mu.Unlock()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	select {
	case <-cleanupDone:
	case <-deadline.C:
		t.Fatal("verification continuation did not physically drain")
	}
	if admission.releases.Load() != 1 {
		t.Fatal("actual verification and descriptor closure did not release once")
	}
}

func TestWorkerApprovedExecutableSnapshotSurvivesOriginalPathReplacement(t *testing.T) {
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	path := filepath.Join(t.TempDir(), "installer-owned-executable")
	approved := []byte("approved immutable build bytes")
	if err := os.WriteFile(path, approved, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(approved)
	launchPath, err := workerSnapshotExecutable(a, path, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("different installer build"), 0700); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(launchPath)
	if err != nil || string(actual) != string(approved) || launchPath == path {
		t.Fatal("launch reopened mutable installer pathname", err)
	}
	if err = workerScratchQuotas(a); err != nil {
		t.Fatal("executable snapshot bypassed explicit scratch budget", err)
	}
	workerWaitPhysicalRelease(t, a)
}

func TestWorkerFailedOutputReaderCloseRetainsExactReaderAndFile(t *testing.T) {
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	a.started = true
	a.reaped = true
	a.pipesJoined = true
	content := []byte("PAR1fixture!")
	sum := sha256.Sum256(content)
	h := a.bootstrap.Handles[1]
	if err := os.WriteFile(filepath.Join(a.path, h.RelativeName), content, 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := a.openOutput(workerOutput{Handle: h.Handle, Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), Rows: 2})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.closeDescriptorHook = func(owner io.Closer) error {
		if owner == reader.file {
			return errWorkerOwnership
		}
		return owner.Close()
	}
	a.mu.Unlock()
	if !errors.Is(reader.Close(), errWorkerOwnership) {
		t.Fatal("failed output close hidden")
	}
	if !errors.Is(a.release(), errWorkerOwnership) || admission.releases.Load() != 0 {
		t.Fatal("failed reader released native charge")
	}
	a.mu.Lock()
	_, retained := a.outputReaders[reader]
	owner := a.descriptors[reader.file]
	a.mu.Unlock()
	if !retained || owner == nil || owner.descriptor != reader.file || owner.joined {
		t.Fatal("exact failed outward reader owner lost")
	}
	if err = reader.file.Close(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	owner.err = nil
	owner.joined = true
	a.notifyPhysicalLocked()
	a.mu.Unlock()
	reader.mu.Lock()
	reader.closeJoined = true
	reader.tryJoinedLocked()
	reader.mu.Unlock()
	workerWaitPhysicalRelease(t, a)
}

func TestWorkerReleaseRetriesTransientScratchRemovalWithinWaiter(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not bind root")
	}
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	// A non-writable subdirectory makes RemoveAll fail, standing in for a file
	// Windows briefly refuses to delete after the worker exits.
	blocked := filepath.Join(a.path, "held")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "file"), []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
	unblocked := time.AfterFunc(150*time.Millisecond, func() { _ = os.Chmod(blocked, 0o700) })
	defer unblocked.Stop()
	if err := a.release(); err != nil {
		t.Fatalf("release after a transient removal failure: %v", err)
	}
	if _, err := os.Stat(a.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scratch survived release", err)
	}
	if admission.releases.Load() != 1 {
		t.Fatal("admission not released once")
	}
}

func TestWorkerReleaseCallbackCannotTrapLifecycleMutex(t *testing.T) {
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	entered, resume := make(chan struct{}), make(chan struct{})
	var resumeOnce sync.Once
	unblock := func() { resumeOnce.Do(func() { close(resume) }) }
	defer unblock()
	a.mu.Lock()
	original := a.releaseAdmission
	a.releaseAdmission = func() { close(entered); <-resume; original() }
	a.mu.Unlock()
	if !errors.Is(a.release(), errWorkerOwnership) {
		t.Fatal("blocked enclosing release unexpectedly joined")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback did not enter")
	}
	second := make(chan error, 1)
	go func() { second <- a.release() }()
	select {
	case err := <-second:
		if !errors.Is(err, errWorkerOwnership) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback held public lifecycle mutex")
	}
	if admission.releases.Load() != 0 {
		t.Fatal("enclosing gate released before callback completion")
	}
	unblock()
	workerWaitPhysicalRelease(t, a)
}
