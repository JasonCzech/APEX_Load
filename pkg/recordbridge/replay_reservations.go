package recordbridge

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultReplayReservationLimit           = 1024
	defaultReplayReservationLimitPerProject = 100
)

var (
	errLocalReplayIDConflict      = errors.New("replay_session_id is already in use")
	errLocalReplayCapacity        = errors.New("too many replay sessions are in flight")
	errLocalReplayProjectCapacity = errors.New("project replay session capacity is exhausted")
	errLocalReplayReservationLost = errors.New("distributed replay session reservation was lost")
)

// localReplayReservations preserves the standalone bridge's admission limits
// independently of the manager's configurable retained-record budget. Terminal
// records keep their IDs reserved without consuming in-flight capacity.
type localReplayReservations struct {
	mu       sync.Mutex
	sessions map[string]*localReplayReservation
	now      func() time.Time
}

type localReplayReservation struct {
	owner     *localReplayReservations
	id        string
	project   string
	retained  bool
	expiresAt time.Time
	failure   error
	done      chan struct{}
}

func newLocalReplayReservations() *localReplayReservations {
	return &localReplayReservations{sessions: make(map[string]*localReplayReservation), now: time.Now}
}

func (o *localReplayReservations) reserve(_ context.Context, requested, project string) (string, func(), SessionLease, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	project = normalizeProjectID(project)
	active, projectActive := 0, 0
	now := o.now()
	for id, reservation := range o.sessions {
		if reservation.retained {
			if !now.Before(reservation.expiresAt) {
				delete(o.sessions, id)
			}
			continue
		}
		active++
		if reservation.project == project {
			projectActive++
		}
	}
	if requested != "" && o.sessions[requested] != nil {
		return "", nil, nil, errLocalReplayIDConflict
	}
	if projectActive >= defaultReplayReservationLimitPerProject {
		return "", nil, nil, errLocalReplayProjectCapacity
	}
	if active >= defaultReplayReservationLimit {
		return "", nil, nil, errLocalReplayCapacity
	}
	id := requested
	for id == "" || o.sessions[id] != nil {
		id = uuid.NewString()
	}
	reservation := &localReplayReservation{owner: o, id: id, project: project, done: make(chan struct{})}
	o.sessions[id] = reservation
	return id, reservation.releaseActive, reservation, nil
}

func (r *localReplayReservation) Done() <-chan struct{} { return r.done }

func (r *localReplayReservation) failLocked(err error) {
	if r.failure == nil {
		if err == nil {
			err = errLocalReplayReservationLost
		}
		r.failure = err
		close(r.done)
	}
}

func (r *localReplayReservation) Fail(err error) {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	r.failLocked(err)
}

func (r *localReplayReservation) FailureOrExpired(now time.Time) error {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	if r.retained && !now.Before(r.expiresAt) {
		r.failLocked(errLocalReplayReservationLost)
	}
	return r.failure
}

func (r *localReplayReservation) RetainForTerminal(ttl time.Duration) error {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	if r.retained && !r.owner.now().Before(r.expiresAt) {
		r.failLocked(errLocalReplayReservationLost)
	}
	if r.failure != nil {
		return r.failure
	}
	if r.owner.sessions[r.id] != r {
		r.failLocked(errLocalReplayReservationLost)
		return r.failure
	}
	if !r.retained {
		r.retained = true
		r.expiresAt = r.owner.now().Add(ReplaySessionTerminalTTL(ttl))
	}
	return nil
}

func (r *localReplayReservation) releaseActive() {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	if !r.retained && r.owner.sessions[r.id] == r {
		delete(r.owner.sessions, r.id)
	}
}

func (r *localReplayReservation) ReleaseRetainedID() {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	if r.retained && r.owner.sessions[r.id] == r {
		delete(r.owner.sessions, r.id)
	}
}

func (r *localReplayReservation) ReleaseAll() {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	if r.owner.sessions[r.id] == r {
		delete(r.owner.sessions, r.id)
	}
}
