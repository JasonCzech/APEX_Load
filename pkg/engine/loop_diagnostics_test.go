package engine

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/testlog"
)

func TestWarnLoopTruncation_DedupesPerKey(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	warnLoopTruncation(logger, "dedupe-test-loop", 5000, 1000)
	warnLoopTruncation(logger, "dedupe-test-loop", 5000, 1000)

	if got := strings.Count(buf.String(), "truncated"); got != 1 {
		t.Fatalf("expected exactly 1 warning (deduped), got %d; log=%q", got, buf.String())
	}
}

func TestControlLoop_FixedCountRunsFully(t *testing.T) {
	// Route the VUser's default-backed logger into a buffer so we can assert the
	// warning fires from the real Run path.
	var buf bytes.Buffer
	testlog.SetDefault(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	proto := &captureProtocol{}
	// A unique action name keeps the process-wide dedup key fresh for this test.
	loop := dsl.Action{
		Name: "fixed-count-truncation-test",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count: 2000, // > defaultControlLoopMaxIterations and no MaxIterations set
			Steps: []dsl.Action{click("step")},
		},
	}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{loop})
	vu.Run(context.Background())

	if len(proto.names) != 2000 {
		t.Fatalf("loop executed %d steps, want 2000", len(proto.names))
	}
	if strings.Contains(buf.String(), "truncated") {
		t.Fatalf("unexpected truncation warning, log was: %q", buf.String())
	}
}

func TestControlLoop_ConditionSafetyCapWarns(t *testing.T) {
	var buf bytes.Buffer
	testlog.SetDefault(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	proto := &captureProtocol{}
	loop := dsl.Action{
		Name: "condition-safety-cap-test",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Expr: "true"},
			Steps:     []dsl.Action{click("step")},
		},
	}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{loop})
	vu.Run(context.Background())

	if len(proto.names) != defaultControlLoopMaxIterations {
		t.Fatalf("loop executed %d steps, want %d (default safety cap)", len(proto.names), defaultControlLoopMaxIterations)
	}
	if !strings.Contains(buf.String(), "runtime safety cap") {
		t.Fatalf("expected a condition safety-cap warning, log was: %q", buf.String())
	}
}

func TestControlLoop_ExplicitMaxIterationsNoWarn(t *testing.T) {
	// When max_iterations is set, the bound is intentional — no truncation warning.
	var buf bytes.Buffer
	testlog.SetDefault(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	proto := &captureProtocol{}
	loop := dsl.Action{
		Name: "explicit-max-iterations-test",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count:         2000,
			MaxIterations: 2000,
			Steps:         []dsl.Action{click("step")},
		},
	}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{loop})
	vu.Run(context.Background())

	if len(proto.names) != 2000 {
		t.Fatalf("loop executed %d steps, want 2000 (explicit max_iterations)", len(proto.names))
	}
	if strings.Contains(buf.String(), "truncated") {
		t.Fatalf("did not expect a truncation warning, log was: %q", buf.String())
	}
}
