package testexec

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type fakeSink struct{ id string }

func (fakeSink) RecordResult(*protocol.Result) error { return nil }

func TestIsStartableTestStatus(t *testing.T) {
	for _, s := range []string{"QUEUED", "PENDING", "RESERVED", "SCHEDULED", "SHAKEOUT_REVIEW"} {
		if !IsStartableTestStatus(s) {
			t.Errorf("IsStartableTestStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"RUNNING", "COMPLETED", "FAILED", "STOPPING", "", "queued"} {
		if IsStartableTestStatus(s) {
			t.Errorf("IsStartableTestStatus(%q) = true, want false", s)
		}
	}
}

func TestAllowsLifecycleControlRejectsUnknownAndUnnormalizedValues(t *testing.T) {
	for _, test := range []struct {
		status  string
		control LifecycleControl
	}{
		{status: "", control: ControlStart},
		{status: "running", control: ControlPause},
		{status: "RUNNING ", control: ControlPause},
		{status: "RUNNING", control: "restart"},
		{status: "UNKNOWN", control: ControlAbort},
	} {
		if AllowsLifecycleControl(test.status, test.control) {
			t.Errorf("AllowsLifecycleControl(%q, %q) = true, want false", test.status, test.control)
		}
	}
}

func TestStartableTestStatusError(t *testing.T) {
	err := StartableTestStatusError("RUNNING")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if got := err.Error(); got == "" || !contains(got, "RUNNING") {
		t.Errorf("error = %q, want it to mention the status", got)
	}
}

func TestAbsInt(t *testing.T) {
	cases := map[int]int{0: 0, 5: 5, -5: 5, -1: 1, 100: 100, -100: 100}
	for in, want := range cases {
		if got := AbsInt(in); got != want {
			t.Errorf("AbsInt(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestCloneGroupSpecsWithResultSink(t *testing.T) {
	sinkA := engine.ResultSink(fakeSink{id: "a"})
	sinkB := engine.ResultSink(fakeSink{id: "b"})
	orig := []engine.GroupConfig{{Name: "g1", ResultSink: sinkA}, {Name: "g2", ResultSink: sinkA}}
	cloned := CloneGroupSpecsWithResultSink(orig, sinkB)

	if len(cloned) != len(orig) {
		t.Fatalf("len(cloned) = %d, want %d", len(cloned), len(orig))
	}
	for i := range cloned {
		if cloned[i].Name != orig[i].Name {
			t.Errorf("cloned[%d].Name = %q, want %q", i, cloned[i].Name, orig[i].Name)
		}
		if cloned[i].ResultSink != sinkB {
			t.Errorf("cloned[%d].ResultSink not re-pointed to sinkB", i)
		}
	}
	// original must be untouched
	for i := range orig {
		if orig[i].ResultSink != sinkA {
			t.Errorf("original group %d mutated", i)
		}
	}
	// empty / nil input
	if got := CloneGroupSpecsWithResultSink(nil, sinkB); len(got) != 0 {
		t.Errorf("CloneGroupSpecsWithResultSink(nil) len = %d, want 0", len(got))
	}
}

func TestResourceBaselineWindows(t *testing.T) {
	pre, post := ResourceBaselineWindows(false)
	if pre != 0 || post != 0 {
		t.Errorf("disabled = (%v, %v), want (0, 0)", pre, post)
	}
	pre, post = ResourceBaselineWindows(true)
	if pre != 5*time.Minute || post != 5*time.Minute {
		t.Errorf("enabled = (%v, %v), want (5m, 5m)", pre, post)
	}
}

func TestStartDelayWindow(t *testing.T) {
	cases := map[string]time.Duration{
		"":        0,
		"0s":      0,
		"-5s":     0,
		"garbage": 0,
		"30s":     30 * time.Second,
		"2m":      2 * time.Minute,
	}
	for in, want := range cases {
		if got := StartDelayWindow(in); got != want {
			t.Errorf("StartDelayWindow(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAppendRunError(t *testing.T) {
	if got := AppendRunError("existing", "p", nil); got != "existing" {
		t.Errorf("nil err should pass through: got %q", got)
	}
	if got := AppendRunError("", "stage", errors.New("boom")); got != "stage: boom" {
		t.Errorf("empty existing: got %q, want %q", got, "stage: boom")
	}
	if got := AppendRunError("first: a", "second", errors.New("b")); got != "first: a; second: b" {
		t.Errorf("accumulate: got %q, want %q", got, "first: a; second: b")
	}
	secret := "short-secret"
	got := AppendRunError("", "archive", errors.New("upload https://user:"+secret+"@archive.example/run failed"))
	if contains(got, secret) {
		t.Errorf("credential-bearing error was retained: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
