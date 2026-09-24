package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// samplerScheduleState is session-owned. It implements the pinned TST 2.6
// admission calculations, not its thread-stop escalation or Java monitor wake
// ordering. Engine admission remains blocked until those integration contracts
// and producer lifecycles are implemented and verified.
type samplerScheduleState struct {
	mu      sync.Mutex
	entries map[string]*samplerScheduleEntry
}
type samplerScheduleEntry struct {
	definition          dsl.SamplerSchedule
	started             bool
	startSecond, second int64
	lastMillis          int64
	sent                int64
	rate                float64
	waiters             map[chan struct{}]struct{}
}
type samplerAdmission struct {
	Delay    time.Duration
	Complete bool
}

func newSamplerScheduleState(scripts []*dsl.Script) (*samplerScheduleState, error) {
	state := &samplerScheduleState{entries: map[string]*samplerScheduleEntry{}}
	for _, script := range scripts {
		if script == nil || len(script.Config.SamplerSchedules) == 0 {
			continue
		}
		profile := script.Config.JMeter
		if profile == nil || profile.Profile != dsl.JMeterMessagingProfile || profile.SourcePlanID == "" {
			return nil, fmt.Errorf("sampler schedules require a named source session")
		}
		for _, schedule := range script.Config.SamplerSchedules {
			if issues := dsl.ValidateSamplerSchedule(schedule); len(issues) > 0 {
				return nil, fmt.Errorf("invalid sampler schedule: %s", issues[0])
			}
			key := profile.SourcePlanID + "\x00" + schedule.ID
			if old := state.entries[key]; old != nil {
				if !reflect.DeepEqual(old.definition, schedule) {
					return nil, fmt.Errorf("conflicting sampler schedule %q", schedule.ID)
				}
				continue
			}
			schedule.Segments = append([]dsl.SamplerRateSegment(nil), schedule.Segments...)
			state.entries[key] = &samplerScheduleEntry{definition: schedule}
		}
	}
	return state, nil
}

// attempt takes an active wall-clock coordinate in milliseconds. Its epoch must
// be positive and stable across a session; a pause extension freezes this clock
// rather than accumulating permits. waiting excludes the current caller, as in
// VariableThroughputTimer.getDelay. The caller serializes waiter registration
// and wakeup with attempts. No future permits are reserved here.
func (s *samplerScheduleState) attempt(plan, id string, activeMillis int64, waiting int) (samplerAdmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attemptLocked(plan, id, activeMillis, waiting)
}

func (s *samplerScheduleState) attemptLocked(plan, id string, activeMillis int64, waiting int) (samplerAdmission, error) {
	e := s.entries[plan+"\x00"+id]
	if e == nil {
		return samplerAdmission{}, fmt.Errorf("unknown source sampler schedule %q", id)
	}
	if activeMillis < 1000 || waiting < 0 {
		return samplerAdmission{}, fmt.Errorf("invalid sampler clock or waiter count")
	}
	second := activeMillis / 1000
	if !e.started {
		e.started, e.startSecond = true, second
	}
	if activeMillis < e.lastMillis {
		return samplerAdmission{}, fmt.Errorf("sampler active clock moved backwards")
	}
	e.lastMillis = activeMillis
	if second != e.second {
		e.second, e.sent = second, 0
		e.rate = samplerRateForSecond(e.definition, second-e.startSecond)
	}
	if e.rate < 0 {
		return samplerAdmission{Complete: true}, nil
	}
	delay := samplerDelayMillis(activeMillis%1000, e.sent, waiting, e.rate)
	if delay == 0 {
		e.sent++
	}
	return samplerAdmission{Delay: time.Duration(delay) * time.Millisecond}, nil
}

// Segment endpoints belong to the preceding segment, including the final
// endpoint. Consequently completion is observed at duration+1 seconds. Do not
// change this to half-open intervals or integrated fractional token budgets.
func samplerRateForSecond(schedule dsl.SamplerSchedule, second int64) float64 {
	for _, row := range schedule.Segments {
		if second <= int64(row.DurationSeconds) {
			return row.StartRPS + float64(second)*(row.EndRPS-row.StartRPS)/float64(row.DurationSeconds)
		}
		second -= int64(row.DurationSeconds)
	}
	return -1
}

func samplerDelayMillis(millis, sent int64, waiting int, rate float64) int64 {
	if float64(millis) >= float64(sent)*(1000/rate) {
		return 0
	}
	// Java narrows double to int by truncating and saturating, not wrapping.
	delay := 1 + 1000*(float64(waiting)+1)/rate
	if delay >= math.MaxInt32 {
		return math.MaxInt32
	}
	return int64(delay)
}

var errSamplerScheduleComplete = errors.New("source sampler schedule completed")

// The engine clock adapter must freeze both NowMillis and Wait during pause.
// Wake represents Java monitor notification; cancellation must always unblock.
type samplerScheduleClock interface {
	NowMillis() int64
	Wait(context.Context, time.Duration, <-chan struct{}) error
}

// wait applies timers in source order, as JMeter invokes Timer.delay methods.
// It does not spawn workers, reserve future starts, or own thread-group stops.
// Engine integration must handle errSamplerScheduleComplete at the applicable
// thread-group boundary; treating it as an ordinary failed sample is incorrect.
func (s *samplerScheduleState) wait(ctx context.Context, plan string, ids []string, clock samplerScheduleClock) error {
	if clock == nil {
		return fmt.Errorf("sampler clock is required")
	}
	s.mu.Lock()
	seen := map[string]bool{}
	for _, id := range ids {
		if s.entries[plan+"\x00"+id] == nil || seen[id] {
			s.mu.Unlock()
			return fmt.Errorf("unknown or repeated sampler schedule %q", id)
		}
		seen[id] = true
	}
	s.mu.Unlock()
	for _, id := range ids {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			s.mu.Lock()
			e := s.entries[plan+"\x00"+id]
			result, err := s.attemptLocked(plan, id, clock.NowMillis(), len(e.waiters))
			if err != nil {
				s.mu.Unlock()
				return err
			}
			if result.Complete {
				s.mu.Unlock()
				return errSamplerScheduleComplete
			}
			if result.Delay == 0 {
				// Java notify chooses an unspecified waiter. Buffered wakeups avoid
				// losing a notification between registration and entering Wait.
				for wake := range e.waiters {
					select {
					case wake <- struct{}{}:
						goto notified
					default:
					}
				}
			notified:
				s.mu.Unlock()
				break
			}
			wake := make(chan struct{}, 1)
			if e.waiters == nil {
				e.waiters = map[chan struct{}]struct{}{}
			}
			e.waiters[wake] = struct{}{}
			s.mu.Unlock()
			err = clock.Wait(ctx, result.Delay, wake)
			s.mu.Lock()
			delete(e.waiters, wake)
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
