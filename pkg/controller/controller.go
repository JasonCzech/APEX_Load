package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type Controller struct {
	tests    map[string]*TestRun
	testsMu  sync.RWMutex
	maxTests int
}

type TestRun struct {
	ID        string
	Config    TestConfig
	Status    TestStatus
	StartedAt time.Time
	EndedAt   time.Time
	Cancel    context.CancelFunc
}

type TestConfig struct {
	Name     string
	Script   *dsl.Script
	VUsers   int
	Duration time.Duration
	RampUp   time.Duration
}

type TestStatus string

const (
	StatusQueued       TestStatus = "QUEUED"
	StatusInitializing TestStatus = "INITIALIZING"
	StatusRunning      TestStatus = "RUNNING"
	StatusRampingDown  TestStatus = "RAMPING_DOWN"
	StatusComplete     TestStatus = "COMPLETE"
	StatusFailed       TestStatus = "FAILED"
	StatusAborted      TestStatus = "ABORTED"
)

func New(maxConcurrent int) *Controller {
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	return &Controller{
		tests:    make(map[string]*TestRun),
		maxTests: maxConcurrent,
	}
}

func (c *Controller) CreateTest(cfg TestConfig) (string, error) {
	c.testsMu.Lock()
	defer c.testsMu.Unlock()

	if c.activeTestsLocked("") >= c.maxTests {
		return "", fmt.Errorf("max concurrent tests (%d) reached", c.maxTests)
	}

	id := uuid.New().String()
	clonedConfig, err := cloneTestConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("clone test config: %w", err)
	}
	c.tests[id] = &TestRun{
		ID:     id,
		Config: clonedConfig,
		Status: StatusQueued,
	}

	slog.Info("test created", "id", id, "name", cfg.Name, "vusers", cfg.VUsers)
	return id, nil
}

func (c *Controller) GetTest(id string) (*TestRun, error) {
	c.testsMu.RLock()
	defer c.testsMu.RUnlock()
	t, ok := c.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	return cloneTestRun(t), nil
}

func (c *Controller) SetStatus(id string, status TestStatus) error {
	c.testsMu.Lock()
	defer c.testsMu.Unlock()
	t, ok := c.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if t.Status == status {
		return nil
	}
	if !validStatusTransition(t.Status, status) {
		return fmt.Errorf("invalid test status transition %s -> %s", t.Status, status)
	}
	if !isActiveStatus(t.Status) && isActiveStatus(status) && c.activeTestsLocked(id) >= c.maxTests {
		return fmt.Errorf("max concurrent tests (%d) reached", c.maxTests)
	}
	t.Status = status
	if status == StatusRunning && t.StartedAt.IsZero() {
		t.StartedAt = time.Now()
	}
	if status == StatusComplete || status == StatusFailed || status == StatusAborted {
		t.EndedAt = time.Now()
	}
	slog.Info("test status changed", "id", id, "status", status)
	return nil
}

func (c *Controller) AbortTest(id string) error {
	c.testsMu.Lock()
	t, ok := c.tests[id]
	if !ok {
		c.testsMu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	if !validStatusTransition(t.Status, StatusAborted) {
		c.testsMu.Unlock()
		return fmt.Errorf("invalid test status transition %s -> %s", t.Status, StatusAborted)
	}
	cancel := t.Cancel
	t.Status = StatusAborted
	t.EndedAt = time.Now()
	c.testsMu.Unlock()
	// A cancel callback is external code. It must not run while the controller
	// mutex is held because it may synchronously call back into the controller.
	if cancel != nil {
		cancel()
	}
	return nil
}

// SetCancel installs the execution cancellation hook without exposing the
// controller's canonical mutable TestRun pointer.
func (c *Controller) SetCancel(id string, cancel context.CancelFunc) error {
	c.testsMu.Lock()
	defer c.testsMu.Unlock()
	t, ok := c.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	t.Cancel = cancel
	return nil
}

func (c *Controller) ListTests() []*TestRun {
	c.testsMu.RLock()
	defer c.testsMu.RUnlock()
	runs := make([]*TestRun, 0, len(c.tests))
	for _, t := range c.tests {
		runs = append(runs, cloneTestRun(t))
	}
	return runs
}

func (c *Controller) activeTestsLocked(excludeID string) int {
	active := 0
	for id, test := range c.tests {
		if id != excludeID && isActiveStatus(test.Status) {
			active++
		}
	}
	return active
}

func isActiveStatus(status TestStatus) bool {
	return status == StatusInitializing || status == StatusRunning || status == StatusRampingDown
}

func isTerminalStatus(status TestStatus) bool {
	return status == StatusComplete || status == StatusFailed || status == StatusAborted
}

func validStatusTransition(from, to TestStatus) bool {
	if from == to {
		return true
	}
	if isTerminalStatus(from) {
		return false
	}
	switch from {
	case StatusQueued:
		return to == StatusInitializing || to == StatusRunning || to == StatusFailed || to == StatusAborted
	case StatusInitializing:
		return to == StatusRunning || to == StatusFailed || to == StatusAborted
	case StatusRunning:
		return to == StatusRampingDown || to == StatusComplete || to == StatusFailed || to == StatusAborted
	case StatusRampingDown:
		return to == StatusComplete || to == StatusFailed || to == StatusAborted
	default:
		return false
	}
}

func cloneTestRun(test *TestRun) *TestRun {
	if test == nil {
		return nil
	}
	clone := *test
	clone.Cancel = nil
	if config, err := cloneTestConfig(test.Config); err == nil {
		clone.Config = config
	} else {
		// JSON cloning can only fail for a future unsupported DSL value. Preserve
		// scalar isolation and fail closed on the mutable script pointer.
		clone.Config.Script = nil
	}
	return &clone
}

func cloneTestConfig(config TestConfig) (TestConfig, error) {
	clone := config
	if config.Script == nil {
		return clone, nil
	}
	encoded, err := json.Marshal(config.Script)
	if err != nil {
		return TestConfig{}, err
	}
	var script dsl.Script
	if err := json.Unmarshal(encoded, &script); err != nil {
		return TestConfig{}, err
	}
	clone.Script = &script
	return clone, nil
}
