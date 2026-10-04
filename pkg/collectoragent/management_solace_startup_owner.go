package collectoragent

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

const solaceStartupOwnersMax = 16
const solaceStartupOpenGrace = 5 * time.Second
const solaceStartupCloseGrace = time.Second

var errSolaceStartupOwnerUnproven = errors.New("Solace startup lock ownership remains unproven")

// The process-local registry is an exact continuation owner. A request timeout,
// provider logout or durable session quarantine does not close this descriptor.
// One path cannot create a replacement owner while its first physical result is
// unknown. Kernel lock ownership and broker-session proof remain independent.
var solaceStartupOwners = struct {
	sync.Mutex
	byPath map[string]*solaceStartupOwner
}{byPath: make(map[string]*solaceStartupOwner)}

type solaceStartupOwner struct {
	mu               sync.Mutex
	path             string
	file             *os.File
	release          func() error
	closeFile        func(*os.File) error
	operationDone    chan struct{}
	closeDone        chan struct{}
	closeOnce        sync.Once
	operationErr     error
	operationUnknown bool
	closeErr         error
	joined           bool
}

func reserveSolaceStartupOwner(path string, closeFile func(*os.File) error) (*solaceStartupOwner, error) {
	if path == "" || closeFile == nil {
		return nil, errSolaceStartupOwnerUnproven
	}
	solaceStartupOwners.Lock()
	defer solaceStartupOwners.Unlock()
	if len(solaceStartupOwners.byPath) >= solaceStartupOwnersMax || solaceStartupOwners.byPath[path] != nil {
		return nil, errSolaceStartupOwnerUnproven
	}
	o := &solaceStartupOwner{path: path, closeFile: closeFile, operationDone: make(chan struct{}), closeDone: make(chan struct{})}
	solaceStartupOwners.byPath[path] = o
	return o, nil
}
func (o *solaceStartupOwner) ownFile(file *os.File) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if file == nil || o.file != nil {
		panic("invalid Solace startup exact file ownership")
	}
	o.file = file
}
func (o *solaceStartupOwner) ownRelease(release func() error) {
	if release == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.release != nil {
		panic("duplicate Solace startup release owner")
	}
	o.release = release
}
func (o *solaceStartupOwner) completeOperation(err error, unknown bool) {
	o.mu.Lock()
	o.operationErr, o.operationUnknown = err, unknown
	close(o.operationDone)
	o.mu.Unlock()
}
func (o *solaceStartupOwner) requestClose() {
	o.closeOnce.Do(func() {
		go func() {
			<-o.operationDone // Never close/release beneath an entered Open/lock.
			o.mu.Lock()
			file, release, closeFile, unknown := o.file, o.release, o.closeFile, o.operationUnknown
			o.mu.Unlock()
			var releaseErr, closeErr error
			if release != nil && !unknown {
				releaseErr = solaceStartupInvoke(release)
			}
			if file != nil && !unknown {
				closeErr = solaceStartupInvoke(func() error { return closeFile(file) })
			}
			joined := !unknown && releaseErr == nil && closeErr == nil
			resultErr := errors.Join(releaseErr, closeErr)
			if !joined {
				resultErr = errors.Join(errSolaceStartupOwnerUnproven, resultErr)
			}
			o.mu.Lock()
			o.closeErr, o.joined = resultErr, joined
			o.mu.Unlock()
			if joined {
				solaceStartupOwners.Lock()
				if solaceStartupOwners.byPath[o.path] == o {
					delete(solaceStartupOwners.byPath, o.path)
				}
				solaceStartupOwners.Unlock()
			}
			close(o.closeDone)
		}()
	})
}
func solaceStartupInvoke(callback func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errSolaceStartupOwnerUnproven
		}
	}()
	return callback()
}
func (o *solaceStartupOwner) Close() error {
	if o == nil {
		return errSolaceStartupOwnerUnproven
	}
	o.requestClose()
	timer := time.NewTimer(solaceStartupCloseGrace)
	defer timer.Stop()
	select {
	case <-o.closeDone:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.closeErr
	case <-timer.C:
		return errSolaceStartupOwnerUnproven
	}
}

// The retained worker owns allocation/validation/locking/quarantine binding.
// Only a complete, still-authorized constructor can hand its exact cleanup to
// Run. Any early caller return leaves that same worker and admission retained.
func runSolaceStartupOwned(ctx context.Context, owner *solaceStartupOwner, setup func(context.Context, *solaceStartupOwner) error) (func() error, error) {
	if ctx == nil || owner == nil || setup == nil {
		return nil, errSolaceStartupOwnerUnproven
	}
	deadline := time.Now().Add(solaceStartupOpenGrace)
	if authored, ok := ctx.Deadline(); ok && authored.Before(deadline) {
		deadline = authored
	}
	workCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		var err error
		unknown := false
		defer func() {
			if recover() != nil {
				unknown = true
				err = errSolaceStartupOwnerUnproven
			}
			owner.completeOperation(err, unknown)
			if err != nil || unknown {
				owner.requestClose()
			}
			result <- err
		}()
		if err = managementContextError(workCtx); err != nil {
			return
		}
		err = setup(workCtx, owner)
		if err == nil {
			err = managementContextError(workCtx)
		}
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case err := <-result:
		if err != nil {
			return nil, errors.Join(err, owner.Close())
		}
		if err = managementContextError(workCtx); err != nil {
			return nil, errors.Join(err, owner.Close())
		}
		owner.mu.Lock()
		file := owner.file
		owner.mu.Unlock()
		if file == nil {
			if err := owner.Close(); err != nil {
				return nil, err
			}
			return func() error { return nil }, nil
		}
		return owner.Close, nil
	case <-ctx.Done():
		owner.requestClose()
		return nil, errors.Join(ctx.Err(), errSolaceStartupOwnerUnproven)
	case <-timer.C:
		owner.requestClose()
		return nil, errors.Join(context.DeadlineExceeded, errSolaceStartupOwnerUnproven)
	}
}
