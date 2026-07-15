package controller

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func testScript() *dsl.Script {
	return &dsl.Script{
		Name:     "test",
		Protocol: "http",
		Actions:  []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	}
}

func TestNew(t *testing.T) {
	c := New(5)
	if c.maxTests != 5 {
		t.Errorf("maxTests = %d, want 5", c.maxTests)
	}
	if c.maxRetained != defaultMaxRetainedTests {
		t.Errorf("maxRetained = %d, want %d", c.maxRetained, defaultMaxRetainedTests)
	}
}

func TestNew_DefaultMax(t *testing.T) {
	c := New(0)
	if c.maxTests != 10 {
		t.Errorf("maxTests = %d, want 10 (default)", c.maxTests)
	}
}

func TestCreateTest(t *testing.T) {
	c := New(10)
	id, err := c.CreateTest(TestConfig{
		Name:     "my-test",
		Script:   testScript(),
		VUsers:   10,
		Duration: time.Minute,
	})

	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
}

func TestCreateTest_MaxConcurrentReached(t *testing.T) {
	c := New(1)

	id, _ := c.CreateTest(TestConfig{Name: "first", Script: testScript(), VUsers: 1, Duration: time.Minute})
	c.SetStatus(id, StatusRunning)

	_, err := c.CreateTest(TestConfig{Name: "second", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err == nil {
		t.Fatal("expected error when max concurrent tests reached")
	}
}

func TestGetTest(t *testing.T) {
	c := New(10)
	id, _ := c.CreateTest(TestConfig{Name: "find-me", Script: testScript(), VUsers: 1, Duration: time.Minute})

	test, err := c.GetTest(id)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if test.Config.Name != "find-me" {
		t.Errorf("name = %q, want %q", test.Config.Name, "find-me")
	}
	if test.Status != StatusQueued {
		t.Errorf("status = %q, want %q", test.Status, StatusQueued)
	}
}

func TestGetTest_NotFound(t *testing.T) {
	c := New(10)
	_, err := c.GetTest("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for unknown test ID")
	}
}

func TestSetStatus(t *testing.T) {
	c := New(10)
	id, _ := c.CreateTest(TestConfig{Name: "status-test", Script: testScript(), VUsers: 1, Duration: time.Minute})

	if err := c.SetStatus(id, StatusRunning); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	test, _ := c.GetTest(id)
	if test.Status != StatusRunning {
		t.Errorf("status = %q, want %q", test.Status, StatusRunning)
	}
	if test.StartedAt.IsZero() {
		t.Error("StartedAt should be set when status is RUNNING")
	}
}

func TestSetStatus_Complete(t *testing.T) {
	c := New(10)
	id, _ := c.CreateTest(TestConfig{Name: "complete-test", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err := c.SetCancel(id, func() {}); err != nil {
		t.Fatal(err)
	}

	c.SetStatus(id, StatusRunning)
	c.SetStatus(id, StatusComplete)

	test, _ := c.GetTest(id)
	if test.Status != StatusComplete {
		t.Errorf("status = %q, want %q", test.Status, StatusComplete)
	}
	if test.EndedAt.IsZero() {
		t.Error("EndedAt should be set when status is COMPLETE")
	}
	if c.tests[id].Cancel != nil {
		t.Error("terminal test retained its cancellation closure")
	}
}

func TestCreateTestBoundsHistoryAndEvictsOnlyTerminalRuns(t *testing.T) {
	c := New(2)
	c.maxRetained = 2
	first, err := c.CreateTest(TestConfig{Name: "first", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.CreateTest(TestConfig{Name: "second", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateTest(TestConfig{Name: "overflow", Script: testScript(), VUsers: 1, Duration: time.Minute}); err == nil {
		t.Fatal("CreateTest exceeded the retained-test bound without a terminal eviction candidate")
	}
	if err := c.SetStatus(first, StatusFailed); err != nil {
		t.Fatal(err)
	}
	third, err := c.CreateTest(TestConfig{Name: "third", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err != nil {
		t.Fatalf("CreateTest did not evict terminal history: %v", err)
	}
	if len(c.tests) != 2 {
		t.Fatalf("retained tests = %d, want 2", len(c.tests))
	}
	if _, err := c.GetTest(first); err == nil {
		t.Fatal("old terminal test was not evicted")
	}
	if _, err := c.GetTest(second); err != nil {
		t.Fatal("queued test was evicted")
	}
	if _, err := c.GetTest(third); err != nil {
		t.Fatal("new test was not retained")
	}
}

func TestSetStatus_NotFound(t *testing.T) {
	c := New(10)
	err := c.SetStatus("unknown-id", StatusRunning)
	if err == nil {
		t.Fatal("expected error for unknown test ID")
	}
}

func TestAbortTest(t *testing.T) {
	c := New(10)
	id, _ := c.CreateTest(TestConfig{Name: "abort-test", Script: testScript(), VUsers: 1, Duration: time.Minute})

	cancelled := false
	_, cancel := context.WithCancel(context.Background())
	if err := c.SetCancel(id, func() {
		cancelled = true
		cancel()
	}); err != nil {
		t.Fatalf("SetCancel: %v", err)
	}

	if err := c.AbortTest(id); err != nil {
		t.Fatalf("AbortTest: %v", err)
	}

	test, _ := c.GetTest(id)
	if test.Status != StatusAborted {
		t.Errorf("status = %q, want %q", test.Status, StatusAborted)
	}
	if !cancelled {
		t.Error("cancel function should have been called")
	}
	if test.EndedAt.IsZero() {
		t.Error("EndedAt should be set after abort")
	}
	if c.tests[id].Cancel != nil {
		t.Error("aborted test retained its cancellation closure")
	}
}

func TestAbortTestIsIdempotentAndPreservesTerminalTime(t *testing.T) {
	c := New(1)
	id, err := c.CreateTest(TestConfig{Name: "idempotent-abort", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AbortTest(id); err != nil {
		t.Fatal(err)
	}
	first, err := c.GetTest(id)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := c.AbortTest(id); err != nil {
		t.Fatalf("duplicate AbortTest: %v", err)
	}
	second, err := c.GetTest(id)
	if err != nil {
		t.Fatal(err)
	}
	if !second.EndedAt.Equal(first.EndedAt) {
		t.Fatalf("duplicate abort changed ended_at: first=%v second=%v", first.EndedAt, second.EndedAt)
	}
}

func TestSetCancelCancelsLateAndDuplicateExecutions(t *testing.T) {
	t.Run("abort before hook installation", func(t *testing.T) {
		c := New(1)
		id, err := c.CreateTest(TestConfig{Name: "late", Script: testScript(), VUsers: 1, Duration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AbortTest(id); err != nil {
			t.Fatal(err)
		}
		canceled := false
		if err := c.SetCancel(id, func() { canceled = true }); err == nil {
			t.Fatal("late SetCancel succeeded for an aborted test")
		}
		if !canceled {
			t.Fatal("late execution cancellation hook was not invoked")
		}
	})

	t.Run("duplicate hook", func(t *testing.T) {
		c := New(1)
		id, err := c.CreateTest(TestConfig{Name: "duplicate", Script: testScript(), VUsers: 1, Duration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		firstCanceled := false
		if err := c.SetCancel(id, func() { firstCanceled = true }); err != nil {
			t.Fatal(err)
		}
		secondCanceled := false
		if err := c.SetCancel(id, func() { secondCanceled = true }); err == nil {
			t.Fatal("duplicate SetCancel succeeded")
		}
		if !secondCanceled {
			t.Fatal("rejected duplicate execution was not canceled")
		}
		if firstCanceled {
			t.Fatal("rejected duplicate canceled the adopted execution")
		}
		if err := c.AbortTest(id); err != nil {
			t.Fatal(err)
		}
		if !firstCanceled {
			t.Fatal("adopted execution was not canceled on abort")
		}
	})

	t.Run("nil hook", func(t *testing.T) {
		c := New(1)
		id, err := c.CreateTest(TestConfig{Name: "nil", Script: testScript(), VUsers: 1, Duration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.SetCancel(id, nil); err == nil {
			t.Fatal("SetCancel accepted a nil cancellation hook")
		}
	})
}

func TestCreateTestRejectsInvalidConfig(t *testing.T) {
	valid := TestConfig{Name: "valid", Script: testScript(), VUsers: 1, Duration: time.Minute}
	tests := map[string]TestConfig{
		"missing name":     {Script: testScript(), VUsers: 1, Duration: time.Minute},
		"missing script":   {Name: "missing", VUsers: 1, Duration: time.Minute},
		"zero vusers":      {Name: "zero", Script: testScript(), Duration: time.Minute},
		"zero duration":    {Name: "zero", Script: testScript(), VUsers: 1},
		"negative ramp-up": {Name: "negative", Script: testScript(), VUsers: 1, Duration: time.Minute, RampUp: -time.Second},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := New(1).CreateTest(cfg); err == nil {
				t.Fatal("CreateTest accepted invalid config")
			}
		})
	}
	if _, err := New(1).CreateTest(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestGetAndListReturnDeepSnapshots(t *testing.T) {
	c := New(10)
	script := testScript()
	id, err := c.CreateTest(TestConfig{Name: "snapshot", Script: script, VUsers: 1, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	script.Name = "caller-mutated"
	got, _ := c.GetTest(id)
	got.Config.Name = "mutated"
	got.Config.Script.Name = "mutated"
	listed := c.ListTests()
	listed[0].Config.Script.Actions[0].Name = "mutated"

	again, _ := c.GetTest(id)
	if again.Config.Name != "snapshot" || again.Config.Script.Name != "test" || again.Config.Script.Actions[0].Name != "a" {
		t.Fatalf("canonical test was mutated through a returned snapshot: %+v", again)
	}
}

func TestAbortCallbackRunsOutsideControllerLock(t *testing.T) {
	c := New(1)
	id, _ := c.CreateTest(TestConfig{Name: "abort-reentrant", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err := c.SetCancel(id, func() { _, _ = c.GetTest(id) }); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.AbortTest(id) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("AbortTest deadlocked while invoking a reentrant cancel callback")
	}
}

func TestSetStatusRejectsIllegalAndOverCapacityTransitions(t *testing.T) {
	c := New(1)
	first, _ := c.CreateTest(TestConfig{Name: "first", Script: testScript(), VUsers: 1, Duration: time.Minute})
	second, _ := c.CreateTest(TestConfig{Name: "second", Script: testScript(), VUsers: 1, Duration: time.Minute})
	if err := c.SetStatus(first, StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := c.SetStatus(second, StatusRunning); err == nil {
		t.Fatal("expected active-test admission limit")
	}
	if err := c.SetStatus(first, StatusComplete); err != nil {
		t.Fatal(err)
	}
	if err := c.SetStatus(first, StatusRunning); err == nil {
		t.Fatal("expected terminal test resurrection to be rejected")
	}
}

func TestAbortTest_NotFound(t *testing.T) {
	c := New(10)
	err := c.AbortTest("unknown-id")
	if err == nil {
		t.Fatal("expected error for unknown test ID")
	}
}

func TestListTests(t *testing.T) {
	c := New(10)
	c.CreateTest(TestConfig{Name: "test-1", Script: testScript(), VUsers: 1, Duration: time.Minute})
	c.CreateTest(TestConfig{Name: "test-2", Script: testScript(), VUsers: 1, Duration: time.Minute})

	tests := c.ListTests()
	if len(tests) != 2 {
		t.Errorf("ListTests count = %d, want 2", len(tests))
	}
}

func TestListTests_Empty(t *testing.T) {
	c := New(10)
	tests := c.ListTests()
	if len(tests) != 0 {
		t.Errorf("ListTests count = %d, want 0", len(tests))
	}
}

func TestStatusTransitions(t *testing.T) {
	c := New(10)
	id, _ := c.CreateTest(TestConfig{Name: "transitions", Script: testScript(), VUsers: 1, Duration: time.Minute})

	transitions := []TestStatus{
		StatusInitializing,
		StatusRunning,
		StatusRampingDown,
		StatusComplete,
	}

	for _, status := range transitions {
		if err := c.SetStatus(id, status); err != nil {
			t.Fatalf("SetStatus(%q): %v", status, err)
		}
		test, _ := c.GetTest(id)
		if test.Status != status {
			t.Errorf("status = %q, want %q", test.Status, status)
		}
	}
}
