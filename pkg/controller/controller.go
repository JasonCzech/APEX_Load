package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
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

	running := 0
	for _, t := range c.tests {
		if t.Status == StatusRunning || t.Status == StatusInitializing {
			running++
		}
	}
	if running >= c.maxTests {
		return "", fmt.Errorf("max concurrent tests (%d) reached", c.maxTests)
	}

	id := uuid.New().String()
	c.tests[id] = &TestRun{
		ID:     id,
		Config: cfg,
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
	return t, nil
}

func (c *Controller) SetStatus(id string, status TestStatus) error {
	c.testsMu.Lock()
	defer c.testsMu.Unlock()
	t, ok := c.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	t.Status = status
	if status == StatusRunning {
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
	defer c.testsMu.Unlock()
	t, ok := c.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if t.Cancel != nil {
		t.Cancel()
	}
	t.Status = StatusAborted
	t.EndedAt = time.Now()
	return nil
}

func (c *Controller) ListTests() []*TestRun {
	c.testsMu.RLock()
	defer c.testsMu.RUnlock()
	runs := make([]*TestRun, 0, len(c.tests))
	for _, t := range c.tests {
		runs = append(runs, t)
	}
	return runs
}
