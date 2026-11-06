package engine

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func scheduleScript(plan string, peak float64) *dsl.Script {
	return &dsl.Script{Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile, SourcePlanID: plan, SourceGroupID: "group"}, SamplerSchedules: []dsl.SamplerSchedule{{ID: "timer", Segments: []dsl.SamplerRateSegment{{StartRPS: 1, EndRPS: peak, DurationSeconds: 300}, {StartRPS: peak, EndRPS: peak, DurationSeconds: 3600}}}}}}
}

func TestSamplerSchedulePinnedOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/jmeter_schedule_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Rates  [][3]float64
		Delays [][5]float64
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Rates) != 7804 || len(vectors.Delays) != 448 {
		t.Fatal("incomplete oracle")
	}
	for _, v := range vectors.Rates {
		got := samplerRateForSecond(scheduleScript("plan", v[0]).Config.SamplerSchedules[0], int64(v[1]))
		if got != v[2] {
			t.Fatalf("rate %v: got %.17g", v, got)
		}
	}
	for _, v := range vectors.Delays {
		got := samplerDelayMillis(int64(v[3]), int64(v[1]), int(v[2]), v[0])
		if got != int64(v[4]) {
			t.Fatalf("delay %v: got %d", v, got)
		}
	}
}

// javaTSTGetDelay transliterates TST 2.6 VariableThroughputTimer.getDelay,
// with the fields ReferenceSchedules.java sets by reflection passed in:
//
//	msecPerReq = 1000 / rps
//	if (millisSinceLastSecond < cntSent * msecPerReq)
//	    return (int) (1 + 1000.0 * (cntDelayed + 1) / rps);
//	return 0;
//
// Go float64 arithmetic is IEEE 754 binary64 like Java double, including
// 1000/0 = +Inf, 0 * +Inf = NaN and every comparison with NaN being false;
// javaDoubleToInt adds Java's (int) narrowing. Unlike samplerDelayMillis it
// keeps Java's comparison direction and has no zero-rate branch, so it is an
// independent derivation of the expected values.
func javaTSTGetDelay(millisSinceLastSecond, cntSent int64, cntDelayed int, rps float64) int64 {
	msecPerReq := 1000 / rps
	if float64(millisSinceLastSecond) < float64(cntSent)*msecPerReq {
		return javaDoubleToInt(1 + 1000.0*float64(cntDelayed+1)/rps)
	}
	return 0
}

// javaDoubleToInt is Java's double-to-int narrowing (JLS 5.1.3): NaN becomes
// 0, values outside the int range saturate, others truncate toward zero.
func javaDoubleToInt(value float64) int64 {
	switch {
	case math.IsNaN(value):
		return 0
	case value >= math.MaxInt32:
		return math.MaxInt32
	case value <= math.MinInt32:
		return math.MinInt32
	}
	return int64(value)
}

// The pinned oracle (ReferenceSchedules.java) never sets rps to 0 and its
// profiles start at 1 rps. This extends it to zero-rate segments: the Java
// transliteration above is first checked against every pinned delay vector,
// then evaluated at rps = 0 on the oracle's own grid (cntSent, cntDelayed and
// millis as ReferenceSchedules.java loops them), and samplerDelayMillis must
// agree. For the rate side, a 0 -> 18 rps segment over 60 s follows the same
// fromVal + second * (toVal - fromVal) / duration expression the pinned rate
// vectors fix, with the endpoint (second 60) still in the segment and -1 after.
// These zero-rate values are derived from the ported algorithm, not produced
// by running the plugin.
func TestSamplerScheduleZeroRateExtendsPinnedOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/jmeter_schedule_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Delays [][5]float64
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Delays) == 0 {
		t.Fatal("incomplete oracle")
	}
	for _, v := range vectors.Delays {
		if got := javaTSTGetDelay(int64(v[3]), int64(v[1]), int(v[2]), v[0]); got != int64(v[4]) {
			t.Fatalf("transliteration disagrees with the Java oracle at %v: got %d", v, got)
		}
	}
	rps := 0.0
	cases := 0
	for _, sent := range []int64{0, 1, 42, 100} {
		for _, waiting := range []int{0, 1, 50, 100} {
			for _, millis := range []int64{0, 1, 499, 999} {
				want := javaTSTGetDelay(millis, sent, waiting, rps)
				// Spelled out: nothing sent admits (NaN compare is false);
				// anything sent waits (int)(+Inf) = Integer.MAX_VALUE.
				if spelled := map[bool]int64{true: 0, false: math.MaxInt32}[sent == 0]; want != spelled {
					t.Fatalf("derivation at sent=%d waiting=%d millis=%d: %d, want %d", sent, waiting, millis, want, spelled)
				}
				if got := samplerDelayMillis(millis, sent, waiting, rps); got != want {
					t.Fatalf("0 rps delay at sent=%d waiting=%d millis=%d: got %d, Java %d", sent, waiting, millis, got, want)
				}
				cases++
			}
		}
	}
	if cases != 64 {
		t.Fatalf("zero-rate grid has %d cases", cases)
	}
	warmUp := dsl.SamplerSchedule{ID: "timer", Segments: []dsl.SamplerRateSegment{{StartRPS: 0, EndRPS: 18, DurationSeconds: 60}}}
	for second := int64(0); second <= 61; second++ {
		want := -1.0
		if second <= 60 {
			fromVal, toVal, duration := 0.0, 18.0, 60.0
			want = fromVal + float64(second)*(toVal-fromVal)/duration
		}
		if got := samplerRateForSecond(warmUp, second); got != want {
			t.Fatalf("0 -> 18 rps at second %d: got %.17g, want %.17g", second, got, want)
		}
	}
	if samplerRateForSecond(warmUp, 0) != 0 || samplerRateForSecond(warmUp, 60) != 18 {
		t.Fatal("the warm-up segment must start at 0 rps and end at 18 rps")
	}
}

// Java's NaN admission is not specific to 0 rps: msecPerReq = 1000/rps is
// +Inf for every rate below 1000/MaxFloat64 (the smallest rate whose division
// stays finite, derived here from the ported expression, not hardcoded), so
// the transliterated getDelay and samplerDelayMillis must agree on both sides
// of that threshold, at 0, at -0 and at ordinary rates, over the zero-rate
// grid. ValidateSamplerSchedule accepts every finite rate in [0, 1e7], so these
// tiny positive rates reach the engine.
func TestSamplerScheduleOverflowingIntervalMatchesJava(t *testing.T) {
	threshold := 1000 / math.MaxFloat64
	below := math.Nextafter(threshold, 0)
	if !math.IsInf(1000/below, 1) || math.IsInf(1000/threshold, 0) {
		t.Fatalf("threshold %.17g does not separate overflowing intervals", threshold)
	}
	rates := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, 5e-307, below, threshold, math.Nextafter(threshold, 1), 1e-300, 1e-6, 0.5, 18}
	for _, rps := range rates {
		for _, sent := range []int64{0, 1, 42, 100} {
			for _, waiting := range []int{0, 1, 50, 100} {
				for _, millis := range []int64{0, 1, 499, 999} {
					want := javaTSTGetDelay(millis, sent, waiting, rps)
					if got := samplerDelayMillis(millis, sent, waiting, rps); got != want {
						t.Fatalf("%.17g rps delay at sent=%d waiting=%d millis=%d: got %d, Java %d", rps, sent, waiting, millis, got, want)
					}
				}
			}
		}
	}
	// Spelled out below the threshold: nothing sent admits, anything sent
	// waits Integer.MAX_VALUE.
	if samplerDelayMillis(0, 0, 0, 5e-307) != 0 || samplerDelayMillis(0, 1, 0, 5e-307) != math.MaxInt32 {
		t.Fatal("an overflowing interval must admit the first sample and hold the rest")
	}
}

func TestSamplerScheduleSharedAdmissionAndIsolation(t *testing.T) {
	a, b := scheduleScript("a", 84), scheduleScript("b", 42)
	pool, err := newSamplerScheduleState([]*dsl.Script{a, a, b})
	if err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := pool.attempt("a", "timer", 1000000, 0)
			if e != nil {
				t.Error(e)
			}
			if r.Delay == 0 && !r.Complete {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("first millisecond admitted %d", admitted.Load())
	}
	// No borrowing capacity from another plan, or a different run.
	for _, state := range []*samplerScheduleState{pool} {
		r, e := state.attempt("b", "timer", 1000000, 0)
		if e != nil || r.Delay != 0 {
			t.Fatal(r, e)
		}
	}
	fresh, _ := newSamplerScheduleState([]*dsl.Script{a})
	r, e := fresh.attempt("a", "timer", 1000000, 0)
	if e != nil || r.Delay != 0 {
		t.Fatal(r, e)
	}
	// Slow sends do not carry unused previous-second capacity forward.
	r, e = pool.attempt("a", "timer", 1010000, 0)
	if e != nil || r.Delay != 0 {
		t.Fatal(r, e)
	}
	r, e = pool.attempt("a", "timer", 1010000, 0)
	if e != nil || r.Delay == 0 {
		t.Fatal(r, e)
	}
	// A frozen active clock cannot produce a resume catch-up burst.
	for range 100 {
		r, e = pool.attempt("a", "timer", 1010000, 0)
		if e != nil || r.Delay == 0 {
			t.Fatal(r, e)
		}
	}
}

func TestSamplerScheduleEndpointAndValidation(t *testing.T) {
	s := scheduleScript("a", 84)
	pool, _ := newSamplerScheduleState([]*dsl.Script{s})
	pool.attempt("a", "timer", 1000000, 0)
	r, e := pool.attempt("a", "timer", 4900000, 0)
	if e != nil || r.Complete {
		t.Fatal("endpoint must still be active", r, e)
	}
	r, e = pool.attempt("a", "timer", 4901000, 0)
	if e != nil || !r.Complete {
		t.Fatal("completion", r, e)
	}
	for _, c := range []struct {
		plan    string
		ms      int64
		waiting int
	}{{"unknown", 4901000, 0}, {"a", 0, 0}, {"a", 4901000, -1}, {"a", 1000000, 0}} {
		if _, e = pool.attempt(c.plan, "timer", c.ms, c.waiting); e == nil {
			t.Fatal("accepted invalid attempt", c)
		}
	}
	// Definitions are copied into the session.
	s.Config.SamplerSchedules[0].Segments[0].EndRPS = 99
	if pool.entries["a\x00timer"].definition.Segments[0].EndRPS != 84 {
		t.Fatal("mutable schedule")
	}
	conflict := scheduleScript("a", 42)
	if _, e = newSamplerScheduleState([]*dsl.Script{s, conflict}); e == nil {
		t.Fatal("accepted conflicting scope")
	}
	s.Config.JMeter = nil
	if _, e = newSamplerScheduleState([]*dsl.Script{s}); e == nil {
		t.Fatal("accepted missing source identity")
	}
}

func TestSamplerScheduleVirtualFullDuration(t *testing.T) {
	for _, peak := range []float64{42, 84} {
		script := scheduleScript("plan", peak)
		state, err := newSamplerScheduleState([]*dsl.Script{script})
		if err != nil {
			t.Fatal(err)
		}
		// A virtual worker advances by the reference-requested delay. This checks
		// the complete schedule without sleeping or allocating extra workers.
		now := int64(1000000)
		starts := 0
		for {
			result, err := state.attempt("plan", "timer", now, 0)
			if err != nil {
				t.Fatal(err)
			}
			if result.Complete {
				break
			}
			if result.Delay == 0 {
				starts++
			} else {
				now += result.Delay.Milliseconds()
			}
			if starts > 400000 {
				t.Fatal("unbounded schedule")
			}
		}
		if now < 4901000 || now > 4903000 {
			t.Fatalf("completion clock %d", now)
		}
		if starts < 100000 {
			t.Fatalf("lost plateau: %d starts", starts)
		}
	}
}

func TestSamplerScheduleDiscontinuousSegments(t *testing.T) {
	schedule := dsl.SamplerSchedule{Segments: []dsl.SamplerRateSegment{{StartRPS: 1, EndRPS: 2, DurationSeconds: 2}, {StartRPS: 100, EndRPS: 200, DurationSeconds: 2}}}
	for second, want := range []float64{1, 1.5, 2, 150, 200, -1} {
		if got := samplerRateForSecond(schedule, int64(second)); got != want {
			t.Fatalf("second %d: %g != %g", second, got, want)
		}
	}
}

func TestSamplerScheduleRejectsClockRegressionWithinSecond(t *testing.T) {
	state, err := newSamplerScheduleState([]*dsl.Script{scheduleScript("plan", 42)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.attempt("plan", "timer", 1000999, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = state.attempt("plan", "timer", 1000000, 0); err == nil {
		t.Fatal("accepted subsecond clock regression")
	}
}

type virtualSamplerClock struct {
	now   int64
	waits int
}

func (c *virtualSamplerClock) NowMillis() int64 { return c.now }
func (c *virtualSamplerClock) Wait(ctx context.Context, delay time.Duration, wake <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.waits++
	c.now += delay.Milliseconds()
	return nil
}

type canceledSamplerClock struct{ entered chan struct{} }

func (c *canceledSamplerClock) NowMillis() int64 { return 1000000 }
func (c *canceledSamplerClock) Wait(ctx context.Context, delay time.Duration, wake <-chan struct{}) error {
	close(c.entered)
	<-ctx.Done()
	return ctx.Err()
}
func TestSamplerScheduleWaitCancellationAndCompletion(t *testing.T) {
	state, _ := newSamplerScheduleState([]*dsl.Script{scheduleScript("p", 42)})
	state.attempt("p", "timer", 1000000, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &canceledSamplerClock{entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- state.wait(ctx, "p", []string{"timer"}, clock) }()
	select {
	case <-clock.entered:
	case <-time.After(time.Second):
		t.Fatal("did not wait")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not cancel")
	}
	if len(state.entries["p\x00timer"].waiters) != 0 {
		t.Fatal("canceled waiter leaked")
	}
	completed := &virtualSamplerClock{now: 4901000}
	if err := state.wait(context.Background(), "p", []string{"timer"}, completed); !errors.Is(err, errSamplerScheduleComplete) {
		t.Fatal(err)
	}
}
func TestSamplerScheduleWaitPreservesTimerOrder(t *testing.T) {
	script := scheduleScript("p", 42)
	second := script.Config.SamplerSchedules[0]
	second.ID = "second"
	script.Config.SamplerSchedules = append(script.Config.SamplerSchedules, second)
	state, _ := newSamplerScheduleState([]*dsl.Script{script})
	clock := &virtualSamplerClock{now: 1000000}
	ids := []string{"timer", "second"}
	for range 2 {
		if err := state.wait(context.Background(), "p", ids, clock); err != nil {
			t.Fatal(err)
		}
	}
	if clock.waits != 1 || clock.now != 1001001 {
		t.Fatalf("unexpected clock: %+v", clock)
	}
	// Both timer counters advance, even though only the first needed to wait.
	for _, id := range ids {
		if state.entries["p\x00"+id].sent != 1 {
			t.Fatal("timer skipped", id)
		}
	}
	before := state.entries["p\x00timer"].sent
	for _, bad := range [][]string{{"timer", "unknown"}, {"timer", "timer"}} {
		if err := state.wait(context.Background(), "p", bad, clock); err == nil {
			t.Fatal("accepted invalid timers")
		}
	}
	if state.entries["p\x00timer"].sent != before {
		t.Fatal("invalid timer list consumed permits")
	}
}

type observingSamplerClock struct {
	now     atomic.Int64
	entered chan time.Duration
	woke    chan struct{}
}

func (c *observingSamplerClock) NowMillis() int64 { return c.now.Load() }
func (c *observingSamplerClock) Wait(ctx context.Context, delay time.Duration, wake <-chan struct{}) error {
	select {
	case c.entered <- delay:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wake:
		c.woke <- struct{}{}
		return nil
	}
}
func TestSamplerScheduleWaiterCountAndNotification(t *testing.T) {
	state, _ := newSamplerScheduleState([]*dsl.Script{scheduleScript("p", 42)})
	state.attempt("p", "timer", 1000000, 0)
	clock := &observingSamplerClock{entered: make(chan time.Duration, 4), woke: make(chan struct{}, 4)}
	clock.now.Store(1000000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for i := 1; i <= 2; i++ {
		go func() { done <- state.wait(ctx, "p", []string{"timer"}, clock) }()
		select {
		case delay := <-clock.entered:
			if delay != time.Duration(1+1000*i)*time.Millisecond {
				t.Fatal(delay)
			}
		case <-time.After(time.Second):
			t.Fatal("waiter missing")
		}
	}
	// One new admission must notify a registered waiter, even when the
	// notification races with the waiter entering its select.
	clock.now.Store(1001000)
	if err := state.wait(context.Background(), "p", []string{"timer"}, &virtualSamplerClock{now: 1001000}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-clock.woke:
	case <-time.After(time.Second):
		t.Fatal("notification lost")
	}
	cancel()
	for range 2 {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("waiter leaked")
		}
	}
	if len(state.entries["p\x00timer"].waiters) != 0 {
		t.Fatal("waiter registry leaked")
	}
}

// TST 2.6 at 0 requests/s: msecPerReq is +Inf, so the first call of a second
// compares against NaN and is admitted, and later calls wait
// Integer.MAX_VALUE for a notification.
func TestSamplerScheduleZeroRateSecondMatchesJava(t *testing.T) {
	if got := samplerDelayMillis(0, 0, 0, 0); got != 0 {
		t.Fatalf("first call in a 0 rps second: delay %d, want admission", got)
	}
	if got := samplerDelayMillis(500, 1, 3, 0); got != math.MaxInt32 {
		t.Fatalf("later call in a 0 rps second: delay %d, want MaxInt32", got)
	}
	script := &dsl.Script{Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile, SourcePlanID: "plan", SourceGroupID: "group"}, SamplerSchedules: []dsl.SamplerSchedule{{ID: "timer", Segments: []dsl.SamplerRateSegment{{StartRPS: 0, EndRPS: 18, DurationSeconds: 60}}}}}}
	state, err := newSamplerScheduleState([]*dsl.Script{script})
	if err != nil {
		t.Fatalf("0 rps warm-up schedule refused: %v", err)
	}
	if r, err := state.attempt("plan", "timer", 1000000, 0); err != nil || r.Delay != 0 || r.Complete {
		t.Fatalf("first 0 rps admission: %+v %v", r, err)
	}
	if r, err := state.attempt("plan", "timer", 1000100, 0); err != nil || r.Delay != math.MaxInt32*time.Millisecond {
		t.Fatalf("second 0 rps call: %+v %v", r, err)
	}
	// One second later the ramp is at 0.3 rps and admits again.
	if r, err := state.attempt("plan", "timer", 1001000, 0); err != nil || r.Delay != 0 {
		t.Fatalf("ramp second: %+v %v", r, err)
	}
}
