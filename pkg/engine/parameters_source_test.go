package engine

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func sourceParameterScript(plan string, p dsl.Parameter) *dsl.Script {
	return &dsl.Script{Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile, SourcePlanID: plan, SourceGroupID: "group"}}, Parameters: []dsl.Parameter{p}}
}
func sourceCounter() dsl.Parameter {
	return dsl.Parameter{Name: "partition", Type: "counter", Scope: "source_engine", ScopeID: "node", Start: 0, End: 5, Step: 1, Update: "each_iteration", OnExhaustion: "wrap"}
}
func boundSourceRuntime(s *dsl.Script, pool *sourceParameterState, id int) *ParameterRuntime {
	r := NewParameterRuntime(s.Parameters, nil, id)
	r.sourceState, r.sourcePlan = pool, s.Config.JMeter.SourcePlanID
	return r
}
func TestSourceParametersConcurrentWrappingAndIterationCache(t *testing.T) {
	s := sourceParameterScript("plan", sourceCounter())
	pool, err := newSourceParameterState([]*dsl.Script{s, s})
	if err != nil {
		t.Fatal(err)
	}
	counts := make([]int, 6)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := boundSourceRuntime(s, pool, id)
			r.Prime()
			for iteration := 0; iteration < 60; iteration++ {
				if iteration > 0 {
					r.RefreshIteration()
				}
				value, ok := r.ResolveNamed("partition")
				again, _ := r.ResolveNamed("partition")
				if !ok || value != again {
					t.Error("iteration assignment changed")
					return
				}
				n, e := strconv.Atoi(value)
				if e != nil || n < 0 || n > 5 {
					t.Error("invalid partition")
					return
				}
				mu.Lock()
				counts[n]++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	for _, n := range counts {
		if n != 1000 {
			t.Fatalf("lost or duplicated allocations: %v", counts)
		}
	}
}
func TestSourceParameterSessionAndPlanIsolation(t *testing.T) {
	p := sourceCounter()
	a, b := sourceParameterScript("a", p), sourceParameterScript("b", p)
	pool, err := newSourceParameterState([]*dsl.Script{a, b})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*dsl.Script{a, b} {
		r := boundSourceRuntime(s, pool, 0)
		r.Prime()
		if got, _ := r.ResolveNamed(p.Name); got != "0" {
			t.Fatal(got)
		}
	}
	fresh, _ := newSourceParameterState([]*dsl.Script{a})
	r := boundSourceRuntime(a, fresh, 0)
	r.Prime()
	if got, _ := r.ResolveNamed(p.Name); got != "0" {
		t.Fatal(got)
	}
	bad := *a
	bad.Parameters = append([]dsl.Parameter(nil), a.Parameters...)
	bad.Parameters[0].End = 7
	if _, err := newSourceParameterState([]*dsl.Script{a, &bad}); err == nil {
		t.Fatal("accepted conflicting identity")
	}
	unbound := NewParameterRuntime(a.Parameters, nil, 0)
	if _, ok := unbound.ResolveNamed(p.Name); ok {
		t.Fatal("silently used per-user fallback")
	}
}

func TestSourceParameterPoolUsesSessionDefinition(t *testing.T) {
	p := sourceCounter()
	s := sourceParameterScript("plan", p)
	pool, err := newSourceParameterState([]*dsl.Script{s})
	if err != nil {
		t.Fatal(err)
	}
	p.Start, p.End = 50, 60
	if got, ok := pool.next("plan", p); !ok || got != "0" {
		t.Fatal("caller changed shared session bounds")
	}
	s.Config.JMeter.SourcePlanID = ""
	if _, err := newSourceParameterState([]*dsl.Script{s}); err == nil {
		t.Fatal("accepted unnamed session")
	}
}
func TestSourceParameterJavaReferenceVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/jmeter_parameter_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Random []struct {
			Seed   int64
			Bound  int32
			Values []int32
		}
		Counter      []int `json:"jmeter_counter"`
		JMeterRandom []int `json:"jmeter_random"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors.Random {
		r := newJavaRandom(v.Seed)
		for i, want := range v.Values {
			if got := r.nextInt(v.Bound); got != want {
				t.Fatalf("seed=%d bound=%d index=%d got=%d want=%d", v.Seed, v.Bound, i, got, want)
			}
		}
	}
	seed := int64(42)
	for _, tc := range []struct {
		p    dsl.Parameter
		want []int
	}{{sourceCounter(), vectors.Counter}, {dsl.Parameter{Name: "partition", Type: "random_number", Scope: "source_engine", ScopeID: "random", RandomAlgorithm: "java48", Min: 0, Max: 5, Seed: &seed, Update: "each_iteration"}, vectors.JMeterRandom}} {
		for _, scope := range []string{"source_engine", "vuser"} {
			p := tc.p
			p.Scope = scope
			s := sourceParameterScript("plan", p)
			pool, e := newSourceParameterState([]*dsl.Script{s})
			if e != nil {
				t.Fatal(e)
			}
			r := boundSourceRuntime(s, pool, 0)
			r.Prime()
			for i, want := range tc.want {
				if i > 0 {
					r.RefreshIteration()
				}
				got, ok := r.ResolveNamed(p.Name)
				if !ok || got != strconv.Itoa(want) {
					t.Fatalf("%s %s iteration%d got%s want%d", p.Type, scope, i, got, want)
				}
			}
			if scope == "vuser" {
				other := boundSourceRuntime(s, pool, 1)
				other.Prime()
				if got, _ := other.ResolveNamed(p.Name); got != strconv.Itoa(tc.want[0]) {
					t.Fatal("per-user sequence shared")
				}
			}
		}
	}
}

func TestSourceParameterJavaBoundaryBehavior(t *testing.T) {
	// CounterConfig wraps signed long addition instead of saturating. In shared
	// mode Long.MIN_VALUE is also its initialization sentinel.
	p := sourceCounter()
	p.Start, p.End = math.MaxInt64, math.MaxInt64
	state := &parameterState{}
	for i := 0; i < 3; i++ {
		if got, ok := jmeterCounterValue(p, state); !ok || got != strconv.FormatInt(math.MaxInt64, 10) {
			t.Fatalf("sentinel wrap: %s", got)
		}
	}
	p.Scope = "vuser"
	state = &parameterState{}
	jmeterCounterValue(p, state)
	if got, _ := jmeterCounterValue(p, state); got != strconv.FormatInt(math.MinInt64, 10) {
		t.Fatalf("per-user overflow: %s", got)
	}
	// RandomVariableConfig clamps overlarge and overflowing ranges to MAX_INT.
	for _, bounds := range [][2]int{{0, math.MaxInt64}, {math.MinInt64, math.MaxInt64}} {
		p := dsl.Parameter{Min: bounds[0], Max: bounds[1]}
		a, b := newJavaRandom(42), newJavaRandom(42)
		for i := 0; i < 50; i++ {
			got, ok := javaRandomValue(p, a)
			want := strconv.FormatInt(int64(p.Min)+int64(b.nextInt(math.MaxInt32)), 10)
			if !ok || got != want {
				t.Fatalf("range clamp: %s != %s", got, want)
			}
		}
	}
	if _, ok := javaRandomValue(dsl.Parameter{Min: 2, Max: 1}, newJavaRandom(1)); ok {
		t.Fatal("accepted reversed range")
	}
}
