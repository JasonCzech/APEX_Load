package engine

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const parkedStackProtocolName = "parked-stack-test"

// parkedStackExecuted receives one value per Execute of the registered
// parked-stack protocol.
var parkedStackExecuted = make(chan struct{}, 1024)

func init() {
	protocol.Register(parkedStackProtocolName, func() protocol.Protocol {
		return &stackGrowingProtocol{executed: parkedStackExecuted}
	})
}

// stackGrowingProtocol grows the calling goroutine's stack to 32 KB in every
// Execute, as a large protocol call does.
type stackGrowingProtocol struct {
	stubProtocol
	executed chan struct{}
}

func (p *stackGrowingProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	growStack(40)
	p.executed <- struct{}{}
	return &protocol.Result{Success: true, StartTime: time.Now()}, nil
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

func parkedStackScript() *dsl.Script {
	return &dsl.Script{
		Name:     "parked stack",
		Protocol: parkedStackProtocolName,
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{
			Name: "grow", Type: "request", Target: "/",
			ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 10 * 60 * 1000},
		}},
	}
}

func stackInuse() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.StackInuse
}

// assertParkedStacksShrink starts vusers VUsers that each grow their stack in
// one action and then park in think time, and requires the GC to shrink the
// parked stacks. Go halves a goroutine stack only while less than a quarter
// of it is in use, so one large frame on the way to the think time keeps
// every idle VUser's stack at 32 KB.
func assertParkedStacksShrink(t *testing.T, vusers int, executed chan struct{}, start func()) {
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
	// Each GC halves a parked stack at most once, and a VUser that has not
	// parked yet keeps its stack; retry until every VUser has parked.
	var perVUser uint64
	for range 50 {
		runtime.GC()
		runtime.GC()
		perVUser = (stackInuse() - min(before, stackInuse())) / uint64(vusers)
		if perVUser < 12<<10 {
			t.Logf("parked VUser stack = %d KB", perVUser>>10)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("parked VUser stack = %d KB, want under 12 KB: a frame on the think-time path is too large for the GC to shrink the stack", perVUser>>10)
}

func TestParkedVUserStackShrinks(t *testing.T) {
	const vusers = 200
	proto := &stackGrowingProtocol{executed: make(chan struct{}, vusers)}
	script := parkedStackScript()
	metrics := NewMetricsCollector(16)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	assertParkedStacksShrink(t, vusers, proto.executed, func() {
		for id := range vusers {
			wg.Go(func() {
				vu := &VUser{ID: id + 1, TotalVUsers: vusers, Proto: proto, Script: script, Metrics: metrics}
				vu.Run(ctx)
			})
		}
	})
}

// Group VUsers start from a larger goroutine frame and sleep through the
// group engine's pause-aware clock.
func TestParkedGroupVUserStackShrinks(t *testing.T) {
	const vusers = 300
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:       []GroupConfig{{Name: "parked", Script: parkedStackScript(), VUsers: vusers}},
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
	assertParkedStacksShrink(t, vusers, parkedStackExecuted, func() {
		go func() {
			defer close(done)
			_, _ = mge.Run(ctx)
		}()
	})
}
