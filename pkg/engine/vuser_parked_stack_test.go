package engine

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const parkedStackProtocolName = "parked-stack-test"

// parkedStackExecuted receives one value per Execute of the registered
// parked-stack protocol, and parkedStackEnded counts its teardowns, which the
// single and group engines run when a VUser ends.
var (
	parkedStackExecuted = make(chan struct{}, 1<<16)
	parkedStackEnded    atomic.Int64
)

// A parked-stack case runs parkedVUsers VUsers on parkedProcs Ps. Each P
// keeps up to 64 dead goroutines with their starting-size stacks (8 KB here,
// up to 16 KB under the race detector), which the baseline counts and new
// VUsers reuse, so a VUser can count up to one starting stack too little.
// Pinning the Ps bounds that error on any machine: 256 cached stacks over
// 1024 VUsers is at most 2 KB (4 KB under the race detector), well clear of
// the gap between the 8 KB result and a 16 KB regression (16 and 32 KB under
// the race detector).
const (
	parkedProcs  = 4
	parkedVUsers = 1024
)

// pinParkedProcs sets GOMAXPROCS to parkedProcs for the test. Shrinking it
// purges the dropped Ps' dead goroutines, whose stacks the next GC frees.
func pinParkedProcs(t *testing.T) {
	t.Helper()
	previous := runtime.GOMAXPROCS(parkedProcs)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
}

func init() {
	protocol.Register(parkedStackProtocolName, func() protocol.Protocol {
		return &stackGrowingProtocol{executed: parkedStackExecuted, ended: &parkedStackEnded}
	})
}

// stackGrowingProtocol grows the calling goroutine's stack to 32 KB in every
// Execute, as a large protocol call does.
type stackGrowingProtocol struct {
	stubProtocol
	executed chan struct{}
	ended    *atomic.Int64
}

func (p *stackGrowingProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	growStack(40)
	p.executed <- struct{}{}
	return &protocol.Result{Success: true, StartTime: time.Now()}, nil
}

func (p *stackGrowingProtocol) Teardown(ctx context.Context) error {
	if p.ended != nil {
		p.ended.Add(1)
	}
	return p.stubProtocol.Teardown(ctx)
}

// growStack uses about 20 KB of stack.
//
//go:noinline
func growStack(depth int) byte {
	var pad [480]byte
	pad[depth%len(pad)] = byte(depth)
	if depth == 0 {
		return pad[0]
	}
	return growStack(depth-1) + pad[(depth*7)%len(pad)]
}

// parkedStackScript runs one stack-growing action and then parks:
//   - "action", "init", "teardown": in that action's think time in the phase.
//     Teardown runs on its own bounded context, which the test's cancel
//     cannot end, so the case waits out its think time: 8 s leaves room for
//     every VUser to park and be measured under the race detector before the
//     first one ends.
//   - "nested", "nested-init": in the think time of an action inside a
//     control_loop and a control_if, in the actions or in init.
//   - "pacing": in the pacing before the second iteration.
func parkedStackScript(phase string) *dsl.Script {
	park := dsl.Action{
		Name: "grow", Type: "request", Target: "/",
		ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 10 * 60 * 1000},
	}
	script := &dsl.Script{
		Name:     "parked stack",
		Protocol: parkedStackProtocolName,
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
	}
	switch phase {
	case "init":
		script.Init = []dsl.Action{park}
	case "teardown":
		park.ThinkTime.MeanMs = 8000
		script.Teardown = []dsl.Action{park}
	case "nested", "nested-init":
		branch := dsl.Action{Name: "branch", Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{park}}}
		nested := []dsl.Action{{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{branch}}}}
		if phase == "nested-init" {
			script.Init = nested
		} else {
			script.Actions = nested
		}
	case "pacing":
		park.ThinkTimeAfter = false
		script.Config.RunLogic.Iterations = 2
		script.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 10 * 60 * 1000}
		script.Actions = []dsl.Action{park}
	default:
		script.Actions = []dsl.Action{park}
	}
	return script
}

func stackInuse() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.StackInuse
}

// assertParkedStacksShrink starts vusers VUsers that each grow their stack in
// one action and then park, and requires the GC to shrink the parked stacks.
// Go halves a goroutine stack only while less than a quarter of it is in use,
// so one large frame on the way to the wait keeps every idle VUser's stack at
// 16 or 32 KB. ended counts the VUsers that have stopped: an ended VUser
// frees its stack, which would pass for a shrunk one.
func assertParkedStacksShrink(t *testing.T, vusers int, executed chan struct{}, ended *atomic.Int64, start func()) {
	t.Helper()
	runtime.GC()
	runtime.GC()
	before := stackInuse()
	start()
	deadline := time.After(10 * time.Second)
	for range vusers {
		select {
		case <-executed:
		case <-deadline:
			t.Fatal("VUsers did not execute their action")
		}
	}
	// Parked stacks shrink to 8 KB. The race detector's larger frames can
	// leave some at 16 KB, so it only requires them to leave 32 KB.
	limit := uint64(12 << 10)
	if raceDetectorEnabled {
		limit = 20 << 10
	}
	// Each GC halves a parked stack at most once, and a VUser that has not
	// parked yet keeps its stack; retry until every VUser has parked.
	var perVUser uint64
	for range 50 {
		runtime.GC()
		runtime.GC()
		inuse := stackInuse()
		if n := ended.Load(); n > 0 {
			t.Fatalf("%d VUsers stopped before their parked stacks were measured", n)
		}
		perVUser = (inuse - min(before, inuse)) / uint64(vusers)
		if perVUser < limit {
			t.Logf("parked VUser stack = %d KB", perVUser>>10)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("parked VUser stack = %d KB, want under %d KB: a frame on the way to the wait is too large for the GC to shrink the stack", perVUser>>10, limit>>10)
}

func TestParkedVUserStackShrinks(t *testing.T) {
	pinParkedProcs(t)
	// The teardown case's think time must fit in the teardown context.
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "30s")
	for _, phase := range []string{"action", "nested", "pacing", "init", "nested-init", "teardown"} {
		t.Run(phase, func(t *testing.T) {
			const vusers = parkedVUsers
			var ended atomic.Int64
			proto := &stackGrowingProtocol{executed: make(chan struct{}, vusers)}
			script := parkedStackScript(phase)
			metrics := NewMetricsCollector(16)
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			defer func() {
				cancel()
				wg.Wait()
			}()
			assertParkedStacksShrink(t, vusers, proto.executed, &ended, func() {
				for id := range vusers {
					wg.Go(func() {
						defer ended.Add(1)
						vu := &VUser{ID: id + 1, TotalVUsers: vusers, Proto: proto, Script: script, Metrics: metrics}
						vu.Run(ctx)
					})
				}
			})
		})
	}
}

// Group VUsers start from a larger goroutine frame and sleep through the
// group engine's pause-aware clock.
func TestParkedGroupVUserStackShrinks(t *testing.T) {
	pinParkedProcs(t)
	for _, phase := range []string{"action", "pacing", "init"} {
		t.Run(phase, func(t *testing.T) {
			const vusers = parkedVUsers
			// A failed earlier run can leave values behind.
			for len(parkedStackExecuted) > 0 {
				<-parkedStackExecuted
			}
			parkedStackEnded.Store(0)
			mge, err := NewMultiGroup(MultiGroupConfig{
				Groups:       []GroupConfig{{Name: "parked", Script: parkedStackScript(phase), VUsers: vusers}},
				Duration:     time.Minute,
				PostBaseline: time.Millisecond,
			})
			if err != nil {
				t.Fatalf("NewMultiGroup: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			defer func() {
				cancel()
				<-done
			}()
			assertParkedStacksShrink(t, vusers, parkedStackExecuted, &parkedStackEnded, func() {
				go func() {
					defer close(done)
					_, _ = mge.Run(ctx)
				}()
			})
		})
	}
}

// Single-engine VUsers start from Engine.Run's goroutine frame. apexctl's local
// and CI runs and the pkg/perfevidence footprint harness use it; controller
// runs, local mode included, use the group engine.
func TestParkedEngineVUserStackShrinks(t *testing.T) {
	pinParkedProcs(t)
	for _, phase := range []string{"action", "pacing", "init"} {
		t.Run(phase, func(t *testing.T) {
			const vusers = parkedVUsers
			// A failed earlier run can leave values behind.
			for len(parkedStackExecuted) > 0 {
				<-parkedStackExecuted
			}
			parkedStackEnded.Store(0)
			eng, err := New(Config{Script: parkedStackScript(phase), VUsers: vusers, Duration: time.Minute})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			defer func() {
				cancel()
				<-done
			}()
			assertParkedStacksShrink(t, vusers, parkedStackExecuted, &parkedStackEnded, func() {
				go func() {
					defer close(done)
					_, _ = eng.Run(ctx)
				}()
			})
		})
	}
}
