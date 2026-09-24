package engine

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// sourceParameterState belongs to one engine session, never a process-global
// cache. Only sequence state is shared; every VUser retains its iteration's
// assigned value in its own ParameterRuntime cache.
type sourceParameterState struct {
	mu      sync.Mutex
	entries map[string]*sourceParameterEntry
}
type sourceParameterEntry struct {
	definition dsl.Parameter
	state      parameterState
}

func newSourceParameterState(scripts []*dsl.Script) (*sourceParameterState, error) {
	s := &sourceParameterState{entries: make(map[string]*sourceParameterEntry)}
	for _, script := range scripts {
		if script == nil {
			continue
		}
		for _, p := range script.Parameters {
			if p.Scope != "source_engine" {
				continue
			}
			if script.Config.JMeter == nil || script.Config.JMeter.Profile != dsl.JMeterMessagingProfile || script.Config.JMeter.SourcePlanID == "" || p.ScopeID == "" || p.Format != "" || p.Update != "each_iteration" || (p.Type != "counter" && (p.Type != "random_number" || p.RandomAlgorithm != "java48")) {
				return nil, fmt.Errorf("unsupported shared source parameter %q", p.Name)
			}
			key := script.Config.JMeter.SourcePlanID + "\x00" + p.ScopeID
			if old := s.entries[key]; old != nil {
				if !reflect.DeepEqual(old.definition, p) {
					return nil, fmt.Errorf("conflicting shared source parameter identity %q", p.Name)
				}
				continue
			}
			seed := time.Now().UnixNano()
			if p.Seed != nil {
				seed = *p.Seed
			}
			s.entries[key] = &sourceParameterEntry{definition: p, state: parameterState{javaRandom: newJavaRandom(seed)}}
		}
	}
	return s, nil
}

func (s *sourceParameterState) next(plan string, p dsl.Parameter) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[plan+"\x00"+p.ScopeID]
	if e == nil {
		return "", false
	}
	if e.definition.Type == "counter" {
		return jmeterCounterValue(e.definition, &e.state)
	}
	return javaRandomValue(e.definition, e.state.javaRandom)
}

// Java long addition wraps. JMeter CounterConfig checks only the upper bound
// (including its Long.MIN_VALUE initialization sentinel), unlike APEX's generic
// counter, which supports descending ranges and saturating arithmetic.
func jmeterCounterValue(p dsl.Parameter, state *parameterState) (string, bool) {
	if p.Format != "" {
		return "", false
	}
	if !state.seeded || state.counter > p.End || (p.Scope == "source_engine" && int64(state.counter) == math.MinInt64) {
		state.counter, state.seeded = p.Start, true
	}
	value := state.counter
	state.counter = int(int64(state.counter) + int64(p.Step))
	return strconv.Itoa(value), true
}

// javaRandom implements java.util.Random's specified 48-bit algorithm and
// nextInt rejection arithmetic. Access is confined to a VUser or pool mutex.
type javaRandom struct{ seed uint64 }

func newJavaRandom(seed int64) *javaRandom {
	return &javaRandom{seed: (uint64(seed) ^ 0x5DEECE66D) & ((1 << 48) - 1)}
}
func (r *javaRandom) next31() int32 {
	r.seed = (r.seed*0x5DEECE66D + 0xB) & ((1 << 48) - 1)
	return int32(r.seed >> 17)
}
func (r *javaRandom) nextInt(bound int32) int32 {
	if bound&-bound == bound {
		return int32((int64(bound) * int64(r.next31())) >> 31)
	}
	for {
		bits := r.next31()
		value := bits % bound
		if bits-value+(bound-1) >= 0 {
			return value
		}
	}
}
func javaRandomValue(p dsl.Parameter, r *javaRandom) (string, bool) {
	if r == nil || p.Min > p.Max || p.Format != "" {
		return "", false
	}
	// Match RandomVariableConfig's signed-long range calculation and clamp.
	rangeSize := int64(p.Max) - int64(p.Min) + 1
	if rangeSize <= 0 || rangeSize > math.MaxInt32 {
		rangeSize = math.MaxInt32
	}
	return strconv.FormatInt(int64(p.Min)+int64(r.nextInt(int32(rangeSize))), 10), true
}
