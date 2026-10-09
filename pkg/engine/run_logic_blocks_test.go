package engine

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// countingProtocol counts executed actions by target.
type countingProtocol struct {
	stubProtocol
	mu     sync.Mutex
	counts map[string]int
	order  []string
}

func (p *countingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.counts == nil {
		p.counts = map[string]int{}
	}
	p.counts[action.Target]++
	p.order = append(p.order, action.Target)
	return &protocol.Result{ActionName: action.Name, StartTime: time.Now(), Success: true, StatusCode: 200}, nil
}

func runLogicScript(blocks []dsl.RunLogicBlock, iterations int) *dsl.Script {
	tag := func(name, group string) dsl.Action {
		return dsl.Action{Name: name, Type: "GET", Method: "GET", Target: "/" + name, Metadata: map[string]any{dsl.RunLogicActionMetadataKey: group}}
	}
	return &dsl.Script{Name: "logic", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.test", RunLogic: dsl.RunLogicConfig{Iterations: iterations, Blocks: blocks}}, Actions: []dsl.Action{
		tag("browse_1", "Browse"), tag("browse_2", "Browse"), tag("buy", "Buy"), tag("logout", "Logout"),
	}}
}

func TestRunLogicPercentageBlockLandsWithinTolerance(t *testing.T) {
	const iterations = 2000
	script := runLogicScript([]dsl.RunLogicBlock{
		{Mode: dsl.RunLogicPercentage, Children: []dsl.RunLogicChild{{Action: "Browse", Weight: 70}, {Action: "Buy", Weight: 30}}},
		{Children: []dsl.RunLogicChild{{Action: "Logout"}}},
	}, iterations)
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatal(issues)
	}
	proto := &countingProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	browse, buy, logout := proto.counts["/browse_1"], proto.counts["/buy"], proto.counts["/logout"]
	if logout != iterations || proto.counts["/browse_2"] != browse || browse+buy != iterations {
		t.Fatalf("counts = %v", proto.counts)
	}
	// 70/30 over 2000 passes: the standard deviation is about 20, so 5
	// percentage points (100 passes) is far outside chance.
	if share := float64(browse) / iterations; math.Abs(share-0.70) > 0.05 {
		t.Fatalf("Browse ran in %.1f%% of iterations, want 70%% ± 5", share*100)
	}
}

func TestRunLogicSequentialAndNestedBlocks(t *testing.T) {
	script := runLogicScript([]dsl.RunLogicBlock{{Children: []dsl.RunLogicChild{
		{Block: &dsl.RunLogicBlock{Iterations: 2, Children: []dsl.RunLogicChild{{Action: "Buy"}}}},
		{Action: "Browse"},
		{Action: "logout"},
	}}}, 1)
	proto := &countingProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	want := []string{"/buy", "/buy", "/browse_1", "/browse_2", "/logout"}
	if len(proto.order) != len(want) {
		t.Fatalf("order = %v, want %v", proto.order, want)
	}
	for i := range want {
		if proto.order[i] != want[i] {
			t.Fatalf("order = %v, want %v", proto.order, want)
		}
	}
}

func TestRunLogicWithoutBlocksIsUnchanged(t *testing.T) {
	script := runLogicScript(nil, 1)
	proto := &countingProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	if len(proto.order) != 4 || proto.order[0] != "/browse_1" || proto.order[3] != "/logout" {
		t.Fatalf("a script without blocks runs its actions in order: %v", proto.order)
	}
}

func TestRunLogicReplayIsDeterministic(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	script := runLogicScript([]dsl.RunLogicBlock{{Mode: dsl.RunLogicRandom, Iterations: 5, Children: []dsl.RunLogicChild{{Action: "Browse"}, {Action: "Buy"}, {Action: "Logout"}}}}, 1)
	script.Protocol = protocolName
	var orders []string
	for run := 0; run < 2; run++ {
		report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
		if err != nil {
			t.Fatal(err)
		}
		order := ""
		for _, event := range report.Events {
			order += event.ActionName + ","
		}
		orders = append(orders, order)
	}
	if orders[0] != orders[1] || orders[0] == "" {
		t.Fatalf("replay must pick the same random children every time: %v", orders)
	}
}
