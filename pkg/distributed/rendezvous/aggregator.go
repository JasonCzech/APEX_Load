package rendezvous

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Aggregator has one bounded exchange loop per locally active round, regardless
// of VUser count. Empty exchanges watch the round; they never occupy a live
// table request slot. Every retry retains the same round and participant IDs.
type Aggregator struct {
	mu         sync.Mutex
	client     Client
	rounds     map[string]*localRound
	population int
}
type localRound struct {
	key      string
	round    uint64
	refs     int
	seen     map[uint64]bool
	pending  []uint64
	done     chan struct{}
	wake     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	finished bool
	outcome  Outcome
	err      error
}

func NewAggregator(client Client, population int) *Aggregator {
	return &Aggregator{client: client, population: population, rounds: map[string]*localRound{}}
}

func (a *Aggregator) Wait(ctx context.Context, key string, round, id uint64) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if a == nil || a.client == nil || a.population < 1 || a.population > MaxPopulation || round == 0 || len(key) == 0 || len(key) > 128 {
		return Outcome{}, ErrInvalid
	}
	mapKey := roundKey(key, round)
	a.mu.Lock()
	r := a.rounds[mapKey]
	if r == nil {
		if len(a.rounds) >= MaxActiveRounds {
			a.mu.Unlock()
			return Outcome{}, ErrCapacity
		}
		r = &localRound{key: key, round: round, seen: map[uint64]bool{}, done: make(chan struct{}), wake: make(chan struct{}, 1)}
		r.ctx, r.cancel = context.WithCancel(context.Background())
		a.rounds[mapKey] = r
		go a.pump(r)
	}
	if !r.seen[id] {
		if len(r.seen) >= a.population {
			a.mu.Unlock()
			return Outcome{}, ErrCapacity
		}
		r.seen[id] = true
		r.pending = append(r.pending, id)
	}
	r.refs++
	select {
	case r.wake <- struct{}{}:
	default:
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		r.refs--
		if r.refs == 0 {
			r.cancel()
			if a.rounds[mapKey] == r {
				delete(a.rounds, mapKey)
			}
		}
	}()
	select {
	case <-r.done:
		return r.outcome, r.err
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}

func (a *Aggregator) pump(r *localRound) {
	finish := func(out Outcome, err error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if !r.finished {
			r.finished = true
			r.outcome = out
			r.err = err
			close(r.done)
		}
	}
	for {
		a.mu.Lock()
		count := min(len(r.pending), MaxBatch)
		batch := Batch{Key: r.key, Round: r.round, Arrivals: append([]uint64(nil), r.pending[:count]...)}
		a.mu.Unlock()
		callCtx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		out, err := a.client.Exchange(callCtx, batch)
		cancel()
		if r.ctx.Err() != nil {
			return
		}
		if err == nil {
			if out.State != "waiting" && out.State != "released" && out.State != "timed_out" {
				finish(Outcome{}, ErrInvalid)
				return
			}
			a.mu.Lock()
			r.pending = r.pending[count:]
			more := len(r.pending) > 0
			a.mu.Unlock()
			if out.State != "waiting" {
				finish(out, nil)
				return
			}
			if more {
				continue
			}
		} else if errors.Is(err, ErrInvalid) || errors.Is(err, ErrRetired) || errors.Is(err, ErrCapacity) {
			finish(Outcome{}, err)
			return
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return
		case <-r.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
