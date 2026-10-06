package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// leasedJobRunner runs durable, leased jobs on the execution leader with a
// bounded number of slots. A job is claimed with an owner token and a lease,
// the lease is renewed while it runs, and the job kind records its terminal
// state under the execution lease term, so a cancelled, overtaken or demoted
// owner records nothing. Shutdown leaves the lease to expire for another
// controller; a panic becomes the job's terminal failure instead of an
// endless re-claim. Root-cause analyses and feature evaluation jobs share it.
type leasedJobRunner[T any] struct {
	name  string
	lease time.Duration
	slots int
	idOf  func(*T) string
	claim func(owner string, now time.Time, lease time.Duration) (*T, error)
	renew func(id, owner string, now time.Time, lease time.Duration) (bool, error)
	// run performs the job and records its outcome. When ctx ends because of
	// shutdown it returns without recording, leaving the lease to expire.
	run func(ctx context.Context, job *T, owner string, term executionLeaseTerm) error
	// fail records a terminal failure for a job whose run panicked.
	fail func(job *T, owner string, term executionLeaseTerm, message string) error

	once    sync.Once
	wake    chan struct{}
	slotCh  chan struct{}
	running sync.Map
}

func (j *leasedJobRunner[T]) init() {
	j.once.Do(func() {
		j.wake = make(chan struct{}, 1)
		j.slotCh = make(chan struct{}, max(j.slots, 1))
	})
}

// wakeUp asks the loop to look for queued work now instead of at its tick.
func (j *leasedJobRunner[T]) wakeUp() {
	j.init()
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

// start runs the claim loop in the server's background group and returns its
// stop function.
func (j *leasedJobRunner[T]) start(s *Server) context.CancelFunc {
	j.init()
	ctx, cancel := context.WithCancel(context.Background())
	s.launchBackground(func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-j.wake:
			}
			j.fill(s, ctx)
		}
	})
	return cancel
}

// fill starts jobs while slots are free and work is queued.
func (j *leasedJobRunner[T]) fill(s *Server, ctx context.Context) {
	j.init()
	for ctx.Err() == nil {
		select {
		case j.slotCh <- struct{}{}:
		default:
			return
		}
		claimed := make(chan bool, 1)
		started := s.launchBackground(func() {
			signalled := false
			defer func() {
				// Even a panicking job must release the fill loop.
				if !signalled {
					claimed <- false
				}
				<-j.slotCh
			}()
			if _, err := j.runNext(s, ctx, func() { signalled = true; claimed <- true }); err != nil {
				slog.Warn(j.name+" failed", "error", err)
			}
		})
		if !started {
			<-j.slotCh
			return
		}
		if !<-claimed {
			return
		}
	}
}

// runNext claims the next queued job and runs it. claimed is called once a
// job is held. It reports whether one ran.
func (j *leasedJobRunner[T]) runNext(s *Server, ctx context.Context, claimed func()) (bool, error) {
	if s.runner == nil || !s.runner.isExecutionLeader() {
		return false, nil
	}
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		return false, nil
	}
	owner := uuid.NewString()
	job, err := j.claim(owner, time.Now(), j.lease)
	if err != nil || job == nil {
		return false, err
	}
	if claimed != nil {
		claimed()
	}
	id := j.idOf(job)
	jobCtx, cancel := context.WithCancel(ctx)
	j.running.Store(id, cancel)
	// A long job can outlast one lease; renew it while the job runs so another
	// controller does not start over.
	renewed := make(chan struct{})
	go func() {
		ticker := time.NewTicker(j.lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-renewed:
				return
			case <-ticker.C:
				ok, err := j.renew(id, owner, time.Now(), j.lease)
				if err != nil {
					// The lease survives a missed renewal; retry at the next tick.
					slog.Warn("failed to renew "+j.name+" lease", "job_id", id, "error", err)
					continue
				}
				if !ok {
					// Cancelled or overtaken: nothing this job records would stand.
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(renewed)
		j.running.Delete(id)
		cancel()
	}()
	return true, j.runRecovered(jobCtx, job, owner, term)
}

func (j *leasedJobRunner[T]) runRecovered(ctx context.Context, job *T, owner string, term executionLeaseTerm) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error(j.name+" panicked", "job_id", j.idOf(job), "panic", recovered)
			err = j.fail(job, owner, term, "the job stopped unexpectedly")
		}
	}()
	return j.run(ctx, job, owner, term)
}

// cancelRunning stops a job this controller is running, if any. The durable
// cancellation is recorded by the caller; this only ends the local work.
func (j *leasedJobRunner[T]) cancelRunning(id string) {
	if running, ok := j.running.Load(id); ok {
		running.(context.CancelFunc)()
	}
}

// fencedLease returns the execution-lease fence a completion must hold.
func (s *Server) fencedLease(term executionLeaseTerm) (string, string, uint64) {
	if s.runner != nil && s.runner.durableExecutionFence {
		return executionLeaseName, term.ownerID, term.generation
	}
	return "", "", 0
}
