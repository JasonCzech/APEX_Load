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
	"time"
)

var errWorkerOwnership = errors.New("native worker ownership unresolved")

// A lease is acquired before scratch allocation/staging. Its caller owns the
// publication transaction; the supervisor cannot grant database authority.
type workerAdmission interface {
	reserve(context.Context, workerBootstrap) (func(), error)
}
type workerAttempt struct {
	mu                                     sync.Mutex
	bootstrap                              workerBootstrap
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	path                                   string
	root                                   *os.Root
	directory                              *os.File
	releaseAdmission                       func()
	privatePipeline                        *NativePipeline
	started, reaped, pipesJoined, released bool
	supervisorStarted                      bool
	cancelAcknowledged                     bool
	outputReaders                          map[*workerOutputReader]struct{}
	readers                                int
	parentOperations                       int
	descriptors                            map[io.Closer]*workerDescriptorOwner
	closeDescriptorHook                    func(io.Closer) error
	verifyReadHook                         func(io.Reader) io.Reader
	releaseRequested                       bool
	cleanupOnce                            sync.Once
	cleanupDone                            chan struct{}
	physicalWake                           chan struct{}
	cleanupRemoveErr                       error
	preparationCancel                      context.CancelFunc
}

func newWorkerAttempt(ctx context.Context, parent string, b workerBootstrap, admission workerAdmission) (*workerAttempt, error) {
	if b.validate() != nil || admission == nil {
		return nil, errWorkerProtocol
	}
	if err := workerContextError(ctx); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(b.Limits.RuntimeMS)*time.Millisecond)
	release, err := admission.reserve(ctx, b)
	if err != nil {
		cancel()
		return nil, err
	}
	if release == nil {
		cancel()
		return nil, errWorkerOwnership
	}
	// The registry exists before any scratch/root/file owner can be opened.
	a := &workerAttempt{bootstrap: b, ctx: ctx, cancel: cancel, releaseAdmission: release}
	failed := true
	defer func() {
		if failed {
			_ = a.release()
		}
	}()

	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errWorkerProtocol
	}
	path, err := workerCreateScratchDirectory(parent, "native-"+b.Identity.AttemptID+"-", a)
	a.path = path
	if err != nil {
		return nil, errWorkerOwnership
	}

	if os.Chmod(path, 0700) != nil {
		return nil, errWorkerProtocol
	}
	if writeWorkerAttemptOwner(path, b.Identity.AttemptID, false, nil) != nil {
		return nil, errWorkerProtocol
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errWorkerProtocol
	}
	a.root = root
	a.trackDescriptor(root)
	directory, err := os.Open(path)
	if err != nil {
		return nil, errWorkerProtocol
	}
	a.directory = directory
	a.trackDescriptor(directory)
	// Freeze the manifest; the parent caller cannot change approved handles.
	state, err := newWorkerProtocolState(b)
	if err != nil {
		return nil, err
	}
	a.bootstrap = state.bootstrap
	failed = false
	return a, nil
}

// stageInput copies an already project-authorized regular source descriptor.
// No network/storage credentials or arbitrary source path go to the child.
func (a *workerAttempt) stageInput(id string, source *os.File) error {
	if source == nil {
		return errWorkerProtocol
	}
	if err := a.beginPhysicalOperation(); err != nil {
		return err
	}
	defer a.endPhysicalOperation()
	a.mu.Lock()
	started := a.started
	a.mu.Unlock()
	if started || workerContextError(a.ctx) != nil {
		return errWorkerOwnership
	}
	h, ok := a.bootstrap.handle(id, "input")
	if !ok {
		return errWorkerProtocol
	}
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != h.Bytes {
		return errWorkerProtocol
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return errWorkerProtocol
	}
	out, err := a.root.OpenFile(h.RelativeName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errWorkerProtocol
	}
	a.trackDescriptor(out)
	digest := sha256.New()
	n, copyErr := workerCopyContext(a.ctx, io.MultiWriter(out, digest), io.LimitReader(source, h.Bytes+1))
	after, statErr := source.Stat()
	finishErr := errors.Join(copyErr, out.Sync(), a.closeDescriptor(out), statErr)
	if finishErr != nil || n != h.Bytes || after == nil || !os.SameFile(before, after) || after.Size() != h.Bytes || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != h.SHA256 {
		return errWorkerProtocol
	}
	return nil
}
func workerCopyContext(ctx context.Context, writer io.Writer, reader io.Reader) (int64, error) {
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if err := workerContextError(ctx); err != nil {
			return total, err
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			if writeErr := writeWorkerBytes(writer, buffer[:n]); writeErr != nil {
				return total, writeErr
			}
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
}

// verifyFile checks the directory-relative descriptor and hash while owned.
// Native pathname reopening is separately guarded by before/after validation.
func workerOpenVerifiedFile(ctx context.Context, a *workerAttempt, root *os.Root, h workerHandle, size int64, hash string) (*os.File, error) {
	info, err := root.Lstat(h.RelativeName)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return nil, errWorkerProtocol
	}
	file, err := root.Open(h.RelativeName)
	if err != nil {
		return nil, errWorkerProtocol
	}
	a.trackDescriptor(file)
	keep := false
	defer func() {
		if !keep {
			_ = a.closeDescriptor(file)
		}
	}()
	before, err := file.Stat()
	if err != nil || !os.SameFile(info, before) || workerPrivateDescriptor(file) != nil {
		return nil, errWorkerProtocol
	}
	digest := sha256.New()
	var input io.Reader = io.LimitReader(file, size+1)
	a.mu.Lock()
	readHook := a.verifyReadHook
	a.mu.Unlock()
	if readHook != nil {
		input = readHook(input)
	}
	n, copyErr := workerCopyContext(ctx, digest, input)
	after, statErr := file.Stat()
	current, lstatErr := root.Lstat(h.RelativeName)
	if errors.Join(copyErr, statErr, lstatErr) != nil || n != size || after == nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != hash {
		return nil, errWorkerProtocol
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errWorkerProtocol
	}
	keep = true
	return file, nil
}
func workerVerifyFile(ctx context.Context, a *workerAttempt, root *os.Root, h workerHandle, size int64, hash string) error {
	file, err := workerOpenVerifiedFile(ctx, a, root, h, size, hash)
	if err != nil {
		return err
	}
	closeErr := a.closeDescriptor(file)
	if closeErr == nil {
		a.forgetJoinedOwner(file)
	}
	return closeErr
}

func (a *workerAttempt) verifyInputs(ctx context.Context) error {
	if err := a.beginPhysicalOperation(); err != nil {
		return err
	}
	defer a.endPhysicalOperation()
	for _, h := range a.bootstrap.Handles {
		if h.Role == "input" && workerVerifyFile(ctx, a, a.root, h, h.Bytes, h.SHA256) != nil {
			return errWorkerProtocol
		}
	}
	return nil
}

// An output reader participates in physical ownership. Close returns only
// after the descriptor closes; neither cancellation nor a DB lease frees it.
type workerOutputReader struct {
	file                         *os.File
	attempt                      *workerAttempt
	mu                           sync.Mutex
	once                         sync.Once
	err                          error
	closing, closeJoined, sealed bool
	activeReads                  int
	closeReturned, done          chan struct{}
}

func (r *workerOutputReader) Read(body []byte) (int, error) {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return 0, os.ErrClosed
	}
	r.activeReads++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.activeReads--; r.tryJoinedLocked(); r.mu.Unlock() }()
	return r.file.Read(body)
}
func (r *workerOutputReader) tryJoinedLocked() {
	if !r.closeJoined || r.activeReads != 0 || r.sealed {
		return
	}
	r.sealed = true
	r.attempt.mu.Lock()
	r.attempt.readers--
	delete(r.attempt.outputReaders, r)
	delete(r.attempt.descriptors, r.file) // actual Close plus every Read has joined
	r.attempt.notifyPhysicalLocked()
	r.attempt.mu.Unlock()
	close(r.done)
}
func (r *workerOutputReader) Close() error {
	r.once.Do(func() {
		r.mu.Lock()
		r.closing = true
		r.mu.Unlock()
		owner := r.attempt.requestDescriptorClose(r.file)
		go func() {
			<-owner.done
			r.mu.Lock()
			r.err = owner.err
			r.closeJoined = owner.err == nil
			r.tryJoinedLocked()
			close(r.closeReturned)
			r.mu.Unlock()
		}()
	})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-r.closeReturned:
		if r.err != nil {
			return r.err
		}
	case <-timer.C:
		return errWorkerOwnership
	}
	select {
	case <-r.done:
		return nil
	case <-timer.C:
		return errWorkerOwnership
	}
}

// The bounded wrapper never drops an exact late reader. A filesystem hash can
// block independently of context, while the admitted operation stays retained.
func (a *workerAttempt) openOutputBounded(out workerOutput, proofWindow time.Duration) (*workerOutputReader, error) {
	if proofWindow <= 0 {
		proofWindow = 6 * time.Second
	}
	type result struct {
		reader *workerOutputReader
		err    error
	}
	done := make(chan result, 1)
	go func() { r, err := a.openOutput(out); done <- result{r, err} }()
	deadline := time.Now().Add(time.Duration(a.bootstrap.Limits.RuntimeMS) * time.Millisecond)
	if actual, ok := a.ctx.Deadline(); ok && actual.Before(deadline) {
		deadline = actual
	}
	timer := time.NewTimer(max(time.Duration(0), time.Until(deadline)) + proofWindow)
	defer timer.Stop()
	ctxDone := a.ctx.Done()
	for {
		select {
		case finished := <-done:
			return finished.reader, finished.err
		case <-ctxDone:
			ctxDone = nil
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(proofWindow)
		case <-timer.C:
			a.cancel()
			go func() {
				late := <-done
				if late.reader != nil {
					_ = late.reader.Close()
				}
			}()
			return nil, errWorkerOwnership
		}
	}
}

func (a *workerAttempt) openOutput(out workerOutput) (*workerOutputReader, error) {
	if err := a.beginPhysicalOperation(); err != nil {
		return nil, err
	}
	defer a.endPhysicalOperation()
	a.mu.Lock()
	eligible := a.reaped && a.pipesJoined && !a.released && !a.releaseRequested
	a.mu.Unlock()
	if !eligible || workerContextError(a.ctx) != nil {
		return nil, errWorkerOwnership
	}
	var h workerHandle
	for _, candidate := range a.bootstrap.Handles {
		if candidate.Handle == out.Handle && candidate.Role != "input" {
			h = candidate
		}
	}
	if h.Handle == "" || out.Bytes > h.Bytes {
		return nil, errWorkerProtocol
	}
	file, err := workerOpenVerifiedFile(a.ctx, a, a.root, h, out.Bytes, out.SHA256)
	if err != nil {
		return nil, errWorkerProtocol
	}

	a.mu.Lock()
	if a.releaseRequested || a.released || workerContextError(a.ctx) != nil {
		a.mu.Unlock()
		_ = a.closeDescriptor(file)
		return nil, errWorkerOwnership
	}
	reader := &workerOutputReader{file: file, attempt: a, closeReturned: make(chan struct{}), done: make(chan struct{})}
	if a.outputReaders == nil {
		a.outputReaders = map[*workerOutputReader]struct{}{}
	}
	a.readers++
	a.outputReaders[reader] = struct{}{}
	a.mu.Unlock()
	return reader, nil
}

func (a *workerAttempt) nativePath(h workerHandle) string {
	return filepath.Join(a.path, h.RelativeName)
}

// A Go timer callback may not yet have updated Err even though its advertised
// deadline has elapsed. Admission/publication uses the actual bound as well.
func workerContextError(ctx context.Context) error {
	if ctx == nil {
		return errWorkerProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
