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

	c.SetStatus(id, StatusRunning)
	c.SetStatus(id, StatusComplete)

	test, _ := c.GetTest(id)
	if test.Status != StatusComplete {
		t.Errorf("status = %q, want %q", test.Status, StatusComplete)
	}
	if test.EndedAt.IsZero() {
		t.Error("EndedAt should be set when status is COMPLETE")
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
