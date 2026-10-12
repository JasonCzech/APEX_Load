// Package rendezvous coordinates explicitly distributed, fixed-membership
// barriers. It carries no target data and never changes membership after loss.
package rendezvous

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	MaxPopulation    = 100000
	MaxContracts     = 32
	MaxActiveRounds  = 32
	RetainedRounds   = 64
	MaxBatch         = 256
	MaxSnapshotBytes = 2 << 20
)

var (
	ErrInvalid     = errors.New("invalid distributed rendezvous request")
	ErrRetired     = errors.New("distributed rendezvous round outcome was retired")
	ErrCapacity    = errors.New("distributed rendezvous capacity exhausted")
	ErrUnavailable = errors.New("distributed rendezvous owner unavailable")
)

type MemberRange struct {
	First uint64 `json:"first"`
	Count int    `json:"count"`
}
type Contract struct {
	Key       string `json:"key"`
	Target    int    `json:"target"`
	TimeoutMS int    `json:"timeout_ms"`
}
type Batch struct {
	Key      string   `json:"key"`
	Round    uint64   `json:"round"`
	Arrivals []uint64 `json:"arrivals,omitempty"`
}
type Outcome struct {
	State    string    `json:"state"`
	Arrived  int       `json:"arrived"`
	Target   int       `json:"target"`
	Deadline time.Time `json:"deadline"`
}
type Round struct {
	Key    string `json:"key"`
	Number uint64 `json:"number"`
	Outcome
	Members []byte `json:"members,omitempty"`
}
type Snapshot struct {
	Version   int               `json:"version"`
	Revision  uint64            `json:"revision"`
	Members   []MemberRange     `json:"members"`
	Contracts []Contract        `json:"contracts"`
	High      map[string]uint64 `json:"high"`
	Rounds    map[string]Round  `json:"rounds"`
}
type SaveFunc func(context.Context, uint64, Snapshot) error
type Client interface {
	Exchange(context.Context, Batch) (Outcome, error)
}
type ClientFunc func(context.Context, Batch) (Outcome, error)

func (f ClientFunc) Exchange(ctx context.Context, b Batch) (Outcome, error) { return f(ctx, b) }

type Coordinator struct {
	mu          sync.Mutex
	state       Snapshot
	save        SaveFunc
	now         func() time.Time
	unavailable bool
	population  int
}

func New(members []MemberRange, contracts []Contract, save SaveFunc) (*Coordinator, error) {
	return Restore(Snapshot{Version: 1, Members: members, Contracts: contracts, High: map[string]uint64{}, Rounds: map[string]Round{}}, save)
}

func Restore(s Snapshot, save SaveFunc) (*Coordinator, error) {
	body, err := json.Marshal(s)
	if err != nil || len(body) > MaxSnapshotBytes || s.Version != 1 || len(s.Members) == 0 || len(s.Members) > 256 || len(s.Contracts) == 0 || len(s.Contracts) > MaxContracts {
		return nil, ErrInvalid
	}
	var owned Snapshot
	if err = json.Unmarshal(body, &owned); err != nil {
		return nil, ErrInvalid
	}
	population := 0
	for i, r := range owned.Members {
		if r.Count < 1 || r.Count > MaxPopulation || r.First > math.MaxUint64-uint64(r.Count-1) {
			return nil, ErrInvalid
		}
		population += r.Count
		for _, prior := range owned.Members[:i] {
			if r.First <= prior.First+uint64(prior.Count-1) && prior.First <= r.First+uint64(r.Count-1) {
				return nil, ErrInvalid
			}
		}
	}
	if population > MaxPopulation {
		return nil, ErrCapacity
	}
	contracts := map[string]Contract{}
	for _, contract := range owned.Contracts {
		if contract.Key == "" || len(contract.Key) > 128 || contract.Target < 1 || contract.Target > population || contract.TimeoutMS < 1 || contract.TimeoutMS > 120000 {
			return nil, ErrInvalid
		}
		if _, ok := contracts[contract.Key]; ok {
			return nil, ErrInvalid
		}
		contracts[contract.Key] = contract
	}
	if owned.High == nil {
		owned.High = map[string]uint64{}
	}
	if owned.Rounds == nil {
		owned.Rounds = map[string]Round{}
	}
	if len(owned.High) > len(contracts) || len(owned.Rounds) > MaxContracts*RetainedRounds+MaxActiveRounds {
		return nil, ErrCapacity
	}
	for key := range owned.High {
		if _, ok := contracts[key]; !ok {
			return nil, ErrInvalid
		}
	}
	active := 0
	for key, round := range owned.Rounds {
		contract, ok := contracts[round.Key]
		if !ok || key != roundKey(round.Key, round.Number) || round.Number == 0 || round.Number > owned.High[round.Key] || round.Target != contract.Target || round.Arrived < 0 || round.Arrived > population || round.Deadline.IsZero() {
			return nil, ErrInvalid
		}
		switch round.State {
		case "waiting":
			active++
			if len(round.Members) != (population+7)/8 || round.Arrived >= round.Target {
				return nil, ErrInvalid
			}
			count := 0
			for i := 0; i < len(round.Members)*8; i++ {
				if round.Members[i/8]&(1<<uint(i%8)) != 0 {
					if i >= population {
						return nil, ErrInvalid
					}
					count++
				}
			}
			if count != round.Arrived {
				return nil, ErrInvalid
			}
		case "released":
			if len(round.Members) != 0 || round.Arrived < round.Target {
				return nil, ErrInvalid
			}
		case "timed_out":
			if len(round.Members) != 0 || round.Arrived >= round.Target {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
	}
	if active > MaxActiveRounds {
		return nil, ErrCapacity
	}
	return &Coordinator{state: owned, save: save, now: time.Now, population: population}, nil
}

func roundKey(key string, round uint64) string { return fmt.Sprintf("%s/%d", key, round) }
func (c *Coordinator) memberIndex(id uint64) int {
	offset := 0
	for _, r := range c.state.Members {
		if id >= r.First && id-r.First < uint64(r.Count) {
			return offset + int(id-r.First)
		}
		offset += r.Count
	}
	return -1
}

func (c *Coordinator) Exchange(ctx context.Context, b Batch) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if b.Round == 0 || len(b.Arrivals) > MaxBatch {
		return Outcome{}, ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unavailable {
		return Outcome{}, ErrUnavailable
	}
	if err := c.expire(ctx); err != nil {
		return Outcome{}, err
	}
	var contract Contract
	for _, candidate := range c.state.Contracts {
		if candidate.Key == b.Key {
			contract = candidate
			break
		}
	}
	if contract.Key == "" {
		return Outcome{}, ErrInvalid
	}
	indexes := make([]int, len(b.Arrivals))
	for i, id := range b.Arrivals {
		indexes[i] = c.memberIndex(id)
		if indexes[i] < 0 {
			return Outcome{}, ErrInvalid
		}
	}
	key := roundKey(b.Key, b.Round)
	r, exists := c.state.Rounds[key]
	if !exists && b.Round <= c.state.High[b.Key] {
		return Outcome{}, ErrRetired
	}
	if exists && r.State != "waiting" {
		return r.Outcome, nil
	}
	changed := false
	if !exists {
		if c.state.High[b.Key] == math.MaxUint64 || b.Round != c.state.High[b.Key]+1 || len(indexes) == 0 {
			return Outcome{}, ErrInvalid
		}
		active := 0
		for _, other := range c.state.Rounds {
			if other.State == "waiting" {
				active++
			}
		}
		if active >= MaxActiveRounds {
			return Outcome{}, ErrCapacity
		}
		r = Round{Key: b.Key, Number: b.Round, Outcome: Outcome{State: "waiting", Target: contract.Target, Deadline: c.now().UTC().Add(time.Duration(contract.TimeoutMS) * time.Millisecond)}, Members: make([]byte, (c.population+7)/8)}
		changed = true
	} else {
		r.Members = append([]byte(nil), r.Members...)
	}
	if !c.now().Before(r.Deadline) {
		r.State = "timed_out"
		r.Members = nil
		changed = true
	} else {
		for _, index := range indexes {
			mask := byte(1 << uint(index%8))
			if r.Members[index/8]&mask == 0 {
				r.Members[index/8] |= mask
				r.Arrived++
				changed = true
			}
		}
		if r.Arrived >= r.Target {
			r.State = "released"
			r.Members = nil
		}
	}
	if !changed {
		return r.Outcome, nil
	}
	if c.state.Revision == math.MaxUint64 {
		return Outcome{}, ErrCapacity
	}
	next := c.state
	next.Revision++
	next.Rounds = make(map[string]Round, len(c.state.Rounds)+1)
	next.High = make(map[string]uint64, len(c.state.High)+1)
	for k, v := range c.state.High {
		next.High[k] = v
	}
	if b.Round > next.High[b.Key] {
		next.High[b.Key] = b.Round
	}
	for k, v := range c.state.Rounds {
		high := next.High[v.Key]
		if v.State != "waiting" && high > RetainedRounds && v.Number <= high-RetainedRounds {
			continue
		}
		next.Rounds[k] = v
	}
	next.Rounds[key] = r
	body, err := json.Marshal(next)
	if err != nil || len(body) > MaxSnapshotBytes {
		return Outcome{}, ErrCapacity
	}
	if c.save != nil {
		if err = c.save(ctx, c.state.Revision, next); err != nil {
			c.unavailable = true
			return Outcome{}, ErrUnavailable
		}
	}
	c.state = next
	return r.Outcome, nil
}

func (c *Coordinator) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, _ := json.Marshal(c.state)
	var out Snapshot
	_ = json.Unmarshal(body, &out)
	return out
}

// A canceled local wait can leave no watcher. Expire abandoned rounds before
// admitting another one so canceled work cannot permanently consume capacity.
func (c *Coordinator) expire(ctx context.Context) error {
	next := c.state
	next.Rounds = nil
	for key, r := range c.state.Rounds {
		if r.State != "waiting" || c.now().Before(r.Deadline) {
			continue
		}
		if next.Rounds == nil {
			next.Rounds = make(map[string]Round, len(c.state.Rounds))
			for k, v := range c.state.Rounds {
				next.Rounds[k] = v
			}
		}
		r.State = "timed_out"
		r.Members = nil
		next.Rounds[key] = r
	}
	if next.Rounds == nil {
		return nil
	}
	if next.Revision == math.MaxUint64 {
		return ErrCapacity
	}
	next.Revision++
	if c.save != nil {
		if err := c.save(ctx, c.state.Revision, next); err != nil {
			c.unavailable = true
			return ErrUnavailable
		}
	}
	c.state = next
	return nil
}
