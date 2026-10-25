package duckdb

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Descriptors enter an attempt registry immediately after open, under an
// already-admitted operation fence. Close runs once in a retained physical
// owner: neither an error nor a blocked Close can disappear into a local defer.
type workerDescriptorOwner struct {
	descriptor io.Closer
	once       sync.Once
	done       chan struct{}
	err        error
	joined     bool
}

func (a *workerAttempt) trackDescriptor(descriptor io.Closer) *workerDescriptorOwner {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.descriptors == nil {
		a.descriptors = map[io.Closer]*workerDescriptorOwner{}
	}
	if owner := a.descriptors[descriptor]; owner != nil {
		return owner
	}
	owner := &workerDescriptorOwner{descriptor: descriptor, done: make(chan struct{})}
	a.descriptors[descriptor] = owner
	return owner
}
func (a *workerAttempt) requestDescriptorClose(descriptor io.Closer) *workerDescriptorOwner {
	owner := a.trackDescriptor(descriptor)
	owner.once.Do(func() {
		go func() {
			a.mu.Lock()
			closeFn := a.closeDescriptorHook
			a.mu.Unlock()
			var err error
			func() {
				defer func() {
					if recover() != nil {
						err = errWorkerOwnership
					}
				}()
				if closeFn != nil {
					err = closeFn(descriptor)
				} else {
					err = descriptor.Close()
				}
			}()
			a.mu.Lock()
			owner.err = err
			owner.joined = err == nil
			close(owner.done)
			a.notifyPhysicalLocked()
			a.mu.Unlock()
		}()
	})
	return owner
}

// Used only inside an already-retained physical owner, never as a public
// waiter. Nested platform closure must wait for the leaf's actual result, so a
// bounded caller timeout cannot permanently convert pending Close into failure.
func (a *workerAttempt) awaitDescriptorClose(descriptor io.Closer) error {
	owner := a.requestDescriptorClose(descriptor)
	<-owner.done
	return owner.err
}

func (a *workerAttempt) closeDescriptor(descriptor io.Closer) error {
	owner := a.requestDescriptorClose(descriptor)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-owner.done:
		return owner.err
	case <-timer.C:
		return errWorkerOwnership
	}
}
func (a *workerAttempt) notifyPhysicalLocked() {
	if a.physicalWake != nil {
		select {
		case a.physicalWake <- struct{}{}:
		default:
		}
	}
}
func (a *workerAttempt) beginPhysicalOperation() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.released || a.releaseRequested {
		return errWorkerOwnership
	}
	if a.parentOperations >= workerMaxParentOperations || len(a.descriptors) >= workerMaxParentDescriptorRecords-workerMaxParentOperations {
		return errWorkerResourceLimit
	}
	a.parentOperations++
	return nil
}
func (a *workerAttempt) endPhysicalOperation() {
	a.mu.Lock()
	a.parentOperations--
	a.notifyPhysicalLocked()
	a.mu.Unlock()
}
func (a *workerAttempt) descriptorsJoinedLocked(excludeCore bool) bool {
	for descriptor, owner := range a.descriptors {
		if excludeCore && (descriptor == a.root || descriptor == a.directory) {
			continue
		}
		if !owner.joined {
			return false
		}
	}
	return true
}

// One attempt can retain only a bounded number of concurrently entered parent
// operations. Joined records remain immutable, preventing repeated Close from
// becoming fake proof; further opens fail before an unbounded registry grows.
const workerMaxParentOperations = 64
const workerMaxParentDescriptorRecords = 32768

var workerAttemptQuarantine sync.Map

// workerReleaseWait bounds the public waiter of an attempt or pipeline
// release. Cleanup continues past it, still charged, but a release that
// returns early fails a caller that checks it, such as an import's
// ReleaseOutputs after normalization. Windows can refuse to delete the
// executable a worker just ran for well over a second, so the waiter allows
// the same proof window as a running attempt.
func workerReleaseWait() time.Duration {
	if wait := workerReleaseWaitOverride.Load(); wait > 0 {
		return time.Duration(wait)
	}
	return workerProofWindow
}

// workerReleaseWaitOverride shortens workerReleaseWait for tests that hold
// cleanup back on purpose. An abandoned attempt's release may read it after
// its test, so it is atomic.
var workerReleaseWaitOverride atomic.Int64

// Cleanup is a retained continuation outside the attempt mutex. Its public
// waiter is bounded; the exact owner stays charged while OS/filesystem calls
// block or return an unknown outcome. There is no repeated-Close fake proof.
func (a *workerAttempt) release() error {
	a.mu.Lock()
	if a.released {
		a.mu.Unlock()
		return nil
	}
	if a.cleanupDone == nil {
		a.cleanupDone = make(chan struct{})
		a.physicalWake = make(chan struct{}, 1)
	}
	a.releaseRequested = true
	if a.cancel != nil {
		a.cancel()
	}
	done := a.cleanupDone
	a.mu.Unlock()
	a.cleanupOnce.Do(func() { workerAttemptQuarantine.Store(a, a); go a.cleanupPhysical() })
	wait := workerReleaseWait()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		a.mu.Lock()
		parentOperations, readers, started, reaped, pipesJoined, joined, removeErr := a.parentOperations, a.readers, a.started, a.reaped, a.pipesJoined, a.descriptorsJoinedLocked(false), a.cleanupRemoveErr
		a.mu.Unlock()
		workerLogUnresolved(a, "release_timeout", "parent_operations", parentOperations, "readers", readers, "started", started, "reaped", reaped, "pipes_joined", pipesJoined, "descriptors_joined", joined, "remove_error", removeErr, "waited", wait)
		return errWorkerOwnership
	}
}
func (a *workerAttempt) cleanupPhysical() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// Windows refuses to delete a file that an exiting worker, an indexer or
	// antivirus still holds for a moment. Retry the scratch removal with a
	// short backoff so one such refusal does not outlast release's bounded
	// waiter, which a full ticker period always would.
	removeRetry := 25 * time.Millisecond
	for {
		a.mu.Lock()
		ready := a.parentOperations == 0 && a.readers == 0 && (!a.started || a.reaped && a.pipesJoined) && a.descriptorsJoinedLocked(true)
		root, directory, path := a.root, a.directory, a.path
		a.mu.Unlock()
		if ready {
			if directory != nil {
				a.requestDescriptorClose(directory)
			}
			if root != nil {
				a.requestDescriptorClose(root)
			}
			a.mu.Lock()
			closed := a.descriptorsJoinedLocked(false)
			a.mu.Unlock()
			if closed {
				if path != "" {
					removeErr := os.RemoveAll(path)
					a.mu.Lock()
					a.cleanupRemoveErr = removeErr
					a.mu.Unlock()
					if removeErr != nil {
						retry := time.NewTimer(removeRetry)
						select {
						case <-retry.C:
						case <-a.physicalWake:
						}
						retry.Stop()
						removeRetry = min(2*removeRetry, time.Second)
						continue
					}
				}
				a.mu.Lock()
				cancel, release := a.preparationCancel, a.releaseAdmission
				a.mu.Unlock()
				// A containing admission callback is itself a retained physical
				// continuation. Never let it hold the public lifecycle mutex.
				if cancel != nil {
					cancel()
				}
				if release != nil {
					release()
				}
				a.mu.Lock()
				a.released = true
				close(a.cleanupDone)
				a.mu.Unlock()
				workerAttemptQuarantine.Delete(a)
				return
			}
		}
		select {
		case <-ticker.C:
		case <-a.physicalWake:
		}
	}
}

// Both attempts and staging leases own directory validation descriptors. A
// created path is returned even on validation failure, so cleanup retains it.
type workerScratchOwner interface {
	TrackOwner(io.Closer)
	CloseOwner(io.Closer) error
}

func (a *workerAttempt) TrackOwner(owner io.Closer)       { a.trackDescriptor(owner) }
func (a *workerAttempt) CloseOwner(owner io.Closer) error { return a.closeDescriptor(owner) }

// A bounded internal walk/verification owns the only remaining local reference
// after successful Close. Drop only these explicitly ended records, while
// failed/pending owners and core roots remain retained. This is not a public
// reconciliation or a way to reinterpret a failed Close.
func (a *workerAttempt) forgetJoinedOwner(descriptor io.Closer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if descriptor == a.root || descriptor == a.directory {
		return
	}
	if owner := a.descriptors[descriptor]; owner != nil && owner.joined {
		delete(a.descriptors, descriptor)
	}
}
