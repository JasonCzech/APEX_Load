package engine

import (
	"context"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDataProvider_SeedRandom_Reproducible(t *testing.T) {
	sources := []dsl.DataSource{{
		Name:           "users",
		Strategy:       "random",
		FirstRowHeader: true,
		InlineData:     "id\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n",
	}}

	draw := func(seed int64) []string {
		dp, err := NewDataProvider(sources)
		if err != nil {
			t.Fatalf("NewDataProvider: %v", err)
		}
		dp.SeedRandom(seed)
		out := make([]string, 0, 25)
		for i := 0; i < 25; i++ {
			out = append(out, dp.GetRow("users", 0)["users.id"])
		}
		return out
	}

	a := draw(42)
	b := draw(42)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed gave different sequences:\n%v\n%v", a, b)
	}
	if reflect.DeepEqual(a, draw(99)) {
		t.Fatal("different seeds should (almost surely) differ across 25 draws")
	}
}

func TestReplayScript_DeterministicWithSeed(t *testing.T) {
	var captured []string
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Name == "draw" {
			captured = append(captured, action.Target)
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	script := &dsl.Script{
		Name:     "deterministic-replay",
		Protocol: protocolName,
		Actions: []dsl.Action{{
			Name: "loop",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 10,
				Steps: []dsl.Action{{
					Name:   "draw",
					Type:   "GET",
					Method: "GET",
					Target: "/r/{{fn.random(1,1000000)}}",
				}},
			},
		}},
	}

	run := func(seed int64) []string {
		captured = nil
		s := seed
		if _, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, Seed: &s}); err != nil {
			t.Fatalf("ReplayScript: %v", err)
		}
		return append([]string(nil), captured...)
	}

	a := run(12345)
	if len(a) != 10 {
		t.Fatalf("expected 10 draws, got %d: %v", len(a), a)
	}
	b := run(12345)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed produced different draws:\n%v\n%v", a, b)
	}
	if reflect.DeepEqual(a, run(12346)) {
		t.Fatal("different seed should produce different draws")
	}
}
