package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type TestRunner struct {
	tests    map[string]*ManagedTest
	scripts  map[string]*StoredScript
	mu       sync.RWMutex
	hub      *Hub
	maxTests int
	store    storage.Store
}

type ManagedTest struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Groups      []TestGroup `json:"groups"`
	TotalVUsers int        `json:"total_vusers"`
	Duration    time.Duration `json:"duration_ns"`
	DurationStr string     `json:"duration"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	LiveMetrics *LiveMetrics `json:"live_metrics,omitempty"`
	History     []*LiveMetrics `json:"history,omitempty"`
	Error       string     `json:"error,omitempty"`

	// deprecated single-script fields kept for backwards compat in JSON
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`

	multiEngine *engine.MultiGroupEngine
	cancel      context.CancelFunc
	groupSpecs  []engine.GroupConfig
}

type TestGroup struct {
	Name           string `json:"name"`
	ScriptID       string `json:"script_id"`
	ScriptName     string `json:"script_name"`
	Protocol       string `json:"protocol"`
	VUsers         int    `json:"vusers"`
	RampUp         string `json:"ramp_up,omitempty"`
	RampDown       string `json:"ramp_down,omitempty"`
	Initialization string `json:"initialization,omitempty"`
	rampUpDur      time.Duration
	script         *dsl.Script
}

type LiveMetrics struct {
	Timestamp    time.Time                   `json:"timestamp"`
	ActiveVUsers int64                       `json:"active_vusers"`
	TPS          float64                     `json:"tps"`
	AvgResponseMs float64                    `json:"avg_response_ms"`
	ErrorPct     float64                     `json:"error_pct"`
	TotalTx      int64                       `json:"total_transactions"`
	TotalErrors  int64                       `json:"total_errors"`
	P50Ms        float64                     `json:"p50_ms"`
	P90Ms        float64                     `json:"p90_ms"`
	P95Ms        float64                     `json:"p95_ms"`
	P99Ms        float64                     `json:"p99_ms"`
	Transactions map[string]*TxMetrics       `json:"transactions,omitempty"`
	GroupMetrics map[string]*GroupLiveMetrics `json:"groups,omitempty"`
	Resources    *engine.SystemResources     `json:"resources,omitempty"`
}

type TxMetrics struct {
	Name   string  `json:"name"`
	Count  int64   `json:"count"`
	Errors int64   `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P90Ms  float64 `json:"p90_ms"`
	P99Ms  float64 `json:"p99_ms"`
}

type GroupLiveMetrics struct {
	Name          string  `json:"name"`
	VUsers        int     `json:"vusers"`
	Transactions  int64   `json:"transactions"`
	Errors        int64   `json:"errors"`
	TPS           float64 `json:"tps"`
	AvgResponseMs float64 `json:"avg_response_ms"`
	P50Ms         float64 `json:"p50_ms"`
	P90Ms         float64 `json:"p90_ms"`
	P99Ms         float64 `json:"p99_ms"`
}

type StoredScript struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Protocol  string      `json:"protocol"`
	Version   string      `json:"version"`
	CreatedAt time.Time   `json:"created_at"`
	Script    *dsl.Script `json:"script"`
}

func NewTestRunner(maxTests int, hub *Hub, store storage.Store) *TestRunner {
	tr := &TestRunner{
		tests:    make(map[string]*ManagedTest),
		scripts:  make(map[string]*StoredScript),
		hub:      hub,
		maxTests: maxTests,
		store:    store,
	}

	// Load existing scripts from store
	if store != nil {
		scripts, err := store.ListScripts()
		if err == nil {
			for _, s := range scripts {
				tr.scripts[s.ID] = &StoredScript{
					ID:        s.ID,
					Name:      s.Name,
					Protocol:  s.Protocol,
					Version:   s.Version,
					CreatedAt: s.CreatedAt,
					Script:    s,
				}
			}
		}

		// Load recent tests
		tests, err := store.ListTests()
		if err == nil {
			for _, t := range tests {
				mt := &ManagedTest{
					ID:          t.ID,
					Name:        t.Name,
					Status:      t.Status,
					TotalVUsers: t.TotalVUsers,
					Duration:    time.Duration(t.Duration),
					Error:       t.Error,
					// Groups need to be reconstructed if we want full detail in history
				}
				if t.EndTime != nil {
					mt.EndedAt = t.EndTime
				}
				// Load history for this test
				hist, err := store.GetHistory(t.ID)
				if err == nil {
					for _, h := range hist {
						if m, ok := h.(map[string]any); ok {
							// This is a bit lossy due to any -> map conversion, 
							// but works for basic history display
							mt.History = append(mt.History, mapToLiveMetrics(m))
						}
					}
					if len(mt.History) > 0 {
						mt.LiveMetrics = mt.History[len(mt.History)-1]
					}
				}
				tr.tests[t.ID] = mt
			}
		}
	}

	return tr
}

// Helper to convert map back to LiveMetrics (since GetHistory returns any)
func mapToLiveMetrics(m map[string]any) *LiveMetrics {
	// Re-marshal to JSON and unmarshal to struct to handle all fields robustly
	data, err := json.Marshal(m)
	if err != nil {
		return &LiveMetrics{}
	}
	var lm LiveMetrics
	if err := json.Unmarshal(data, &lm); err != nil {
		return &LiveMetrics{}
	}
	return &lm
}

func (tr *TestRunner) StoreScript(script *dsl.Script) *StoredScript {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if script.ID == "" {
		script.ID = uuid.New().String()
	}

	stored := &StoredScript{
		ID:        script.ID,
		Name:      script.Name,
		Protocol:  script.Protocol,
		Version:   script.Version,
		CreatedAt: time.Now(),
		Script:    script,
	}

	if tr.store != nil {
		tr.store.SaveScript(script)
	}

	tr.scripts[stored.ID] = stored
	return stored
}

func (tr *TestRunner) GetScript(id string) (*StoredScript, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	s, ok := tr.scripts[id]
	return s, ok
}

func (tr *TestRunner) ListScripts() []*StoredScript {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		out = append(out, s)
	}
	return out
}

func (tr *TestRunner) UpdateScript(id string, script *dsl.Script) (*StoredScript, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	existing, ok := tr.scripts[id]
	if !ok {
		return nil, false
	}
	existing.Name = script.Name
	existing.Protocol = script.Protocol
	existing.Version = script.Version
	existing.Script = script
	if tr.store != nil {
		tr.store.SaveScript(script)
	}
	return existing, true
}

func (tr *TestRunner) DeleteScript(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	_, ok := tr.scripts[id]
	if !ok {
		return false
	}
	delete(tr.scripts, id)
	if tr.store != nil {
		tr.store.DeleteScript(id)
	}
	return true
}

type CreateTestRequest struct {
	Name     string             `json:"name"`
	Duration string             `json:"duration"`
	Groups   []CreateGroupEntry `json:"groups,omitempty"`
	// Single-script shorthand (backwards compatible)
	ScriptID string `json:"script_id,omitempty"`
	VUsers   int    `json:"vusers,omitempty"`
	RampUp   string `json:"ramp_up,omitempty"`
	// Test-level runtime overrides (mirrors LR Controller override behavior)
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
}

// RuntimeSettingsOverride allows overriding script-level runtime settings at test time,
// mirroring LoadRunner Controller's override capability.
type RuntimeSettingsOverride struct {
	Iterations      *int                     `json:"iterations,omitempty"`
	Pacing          *dsl.PacingConfig        `json:"pacing,omitempty"`
	ThinkTime       *dsl.ThinkTimeConfig     `json:"think_time,omitempty"`
	ErrorHandling   *dsl.ErrorHandlingConfig `json:"error_handling,omitempty"`
	LogLevel        string                   `json:"log_level,omitempty"`
	AutoTransactions string                  `json:"auto_transactions,omitempty"`
}

type CreateGroupEntry struct {
	Name           string `json:"name"`
	ScriptID       string `json:"script_id"`
	VUsers         int    `json:"vusers"`
	RampUp         string `json:"ramp_up,omitempty"`
	RampDown       string `json:"ramp_down,omitempty"`
	InitStrategy   string `json:"initialization,omitempty"` // "at_runtime", "at_once"
}

func (tr *TestRunner) CreateTest(req CreateTestRequest) (*ManagedTest, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	running := 0
	for _, t := range tr.tests {
		if t.Status == "RUNNING" || t.Status == "INITIALIZING" {
			running++
		}
	}
	if running >= tr.maxTests {
		return nil, fmt.Errorf("max concurrent tests (%d) reached", tr.maxTests)
	}

	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}

	// Normalize: single-script shorthand → groups array
	groups := req.Groups
	if len(groups) == 0 && req.ScriptID != "" {
		groups = []CreateGroupEntry{{
			ScriptID: req.ScriptID,
			VUsers:   req.VUsers,
			RampUp:   req.RampUp,
		}}
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("at least one group (or script_id) is required")
	}

	testGroups := make([]TestGroup, 0, len(groups))
	engineGroups := make([]engine.GroupConfig, 0, len(groups))
	totalVUsers := 0

	for i, g := range groups {
		script, ok := tr.scripts[g.ScriptID]
		if !ok {
			return nil, fmt.Errorf("group[%d]: script not found: %s", i, g.ScriptID)
		}

		vusers := g.VUsers
		if vusers <= 0 {
			vusers = 10
		}

		var rampUp time.Duration
		if g.RampUp != "" {
			rampUp, err = time.ParseDuration(g.RampUp)
			if err != nil {
				return nil, fmt.Errorf("group[%d]: invalid ramp_up: %w", i, err)
			}
		}

		name := g.Name
		if name == "" {
			name = script.Name
			if len(groups) > 1 {
				name = fmt.Sprintf("%s (#%d)", script.Name, i+1)
			}
		}

		var rampDown time.Duration
		if g.RampDown != "" {
			rampDown, err = time.ParseDuration(g.RampDown)
			if err != nil {
				return nil, fmt.Errorf("group[%d]: invalid ramp_down: %w", i, err)
			}
		}

		initStrategy := g.InitStrategy
		if initStrategy == "" {
			initStrategy = "at_runtime"
		}

		testGroups = append(testGroups, TestGroup{
			Name:           name,
			ScriptID:       g.ScriptID,
			ScriptName:     script.Name,
			Protocol:       script.Protocol,
			VUsers:         vusers,
			RampUp:         g.RampUp,
			RampDown:       g.RampDown,
			Initialization: initStrategy,
			rampUpDur:      rampUp,
			script:         script.Script,
		})

		// Apply test-level runtime overrides to a copy of the script
		scriptCopy := *script.Script
		if req.RuntimeSettings != nil {
			applyRuntimeOverrides(&scriptCopy, req.RuntimeSettings)
		}

		engineGroups = append(engineGroups, engine.GroupConfig{
			Name:         name,
			Script:       &scriptCopy,
			VUsers:       vusers,
			RampUp:       rampUp,
			RampDown:     rampDown,
			InitStrategy: initStrategy,
		})

		totalVUsers += vusers
	}

	testName := req.Name
	if testName == "" {
		if len(testGroups) == 1 {
			testName = testGroups[0].ScriptName
		} else {
			testName = fmt.Sprintf("Multi-group (%d groups)", len(testGroups))
		}
	}

	test := &ManagedTest{
		ID:          uuid.New().String(),
		Name:        testName,
		Status:      "QUEUED",
		Groups:      testGroups,
		TotalVUsers: totalVUsers,
		Duration:    dur,
		DurationStr: req.Duration,
		CreatedAt:   time.Now(),
		groupSpecs:  engineGroups,
	}

	// Backwards-compat fields
	if len(testGroups) == 1 {
		test.ScriptID = testGroups[0].ScriptID
		test.ScriptName = testGroups[0].ScriptName
	}

	tr.tests[test.ID] = test

	if tr.store != nil {
		tr.store.SaveTest(&storage.TestRecord{
			ID:          test.ID,
			Name:        test.Name,
			Status:      test.Status,
			TotalVUsers: test.TotalVUsers,
			Duration:    int64(test.Duration),
			StartTime:   test.CreatedAt,
			Groups:      test.Groups,
		})
	}

	tr.broadcastEvent("test_created", test)
	return test, nil
}

func (tr *TestRunner) GetTest(id string) (*ManagedTest, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	t, ok := tr.tests[id]
	return t, ok
}

func (tr *TestRunner) ListTests() []*ManagedTest {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) DeleteTest(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok {
		return false
	}
	if test.Status == "RUNNING" || test.Status == "INITIALIZING" || test.Status == "STOPPING" {
		return false // Can't delete active tests
	}
	delete(tr.tests, id)
	if tr.store != nil {
		tr.store.DeleteTest(id)
	}
	return true
}

func (tr *TestRunner) StartTest(id string) error {
	tr.mu.Lock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "QUEUED" {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s, must be QUEUED to start", test.Status)
	}
	test.Status = "INITIALIZING"
	tr.mu.Unlock()

	go tr.runTest(test)
	return nil
}

func (tr *TestRunner) StopTest(id string) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.cancel != nil {
		test.cancel()
	}
	test.Status = "STOPPING"
	tr.broadcastEvent("test_stopping", test)
	return nil
}

func (tr *TestRunner) AbortTest(id string) error {
	return tr.StopTest(id)
}

func (tr *TestRunner) runTest(test *ManagedTest) {
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:   test.groupSpecs,
		Duration: test.Duration,
	})
	if err != nil {
		test.Status = "FAILED"
		test.Error = err.Error()
		now := time.Now()
		test.EndedAt = &now
		tr.broadcastEvent("test_failed", test)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	test.cancel = cancel
	test.multiEngine = mge

	now := time.Now()
	test.StartedAt = &now
	test.Status = "RUNNING"
	tr.broadcastEvent("test_started", test)

	metricsDone := make(chan struct{})
	go tr.broadcastMetrics(test, mge, metricsDone)

	result, err := mge.Run(ctx)

	close(metricsDone)

	endTime := time.Now()
	test.EndedAt = &endTime

	if err != nil {
		test.Status = "FAILED"
		test.Error = err.Error()
		tr.broadcastEvent("test_failed", test)
	} else {
		test.Status = "COMPLETE"
	}
	test.EndedAt = &now
	if tr.store != nil {
		tr.store.UpdateTestStatus(test.ID, test.Status, test.EndedAt, test.Error)
	}
	tr.broadcastEvent("test_complete", test)

	slog.Info("test finished",
		"id", test.ID,
		"status", test.Status,
		"groups", len(test.Groups),
		"transactions", result.TotalTransactions,
		"errors", result.TotalErrors,
		"duration", result.Duration,
	)
}

func (tr *TestRunner) broadcastMetrics(test *ManagedTest, mge *engine.MultiGroupEngine, done chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	groupVUsers := make(map[string]int)
	for _, g := range test.Groups {
		groupVUsers[g.Name] = g.VUsers
	}

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			snap, ok := mge.LatestSnapshot()
			if !ok {
				continue
			}

			live := &LiveMetrics{
				Timestamp:     snap.Timestamp,
				ActiveVUsers:  snap.ActiveVUsers,
				TPS:           snap.TPS,
				AvgResponseMs: float64(snap.AvgDuration.Microseconds()) / 1000.0,
				TotalTx:       snap.Transactions,
				TotalErrors:   snap.Errors,
				P50Ms:         float64(snap.P50.Microseconds()) / 1000.0,
				P90Ms:         float64(snap.P90.Microseconds()) / 1000.0,
				P95Ms:         float64(snap.P95.Microseconds()) / 1000.0,
				P99Ms:         float64(snap.P99.Microseconds()) / 1000.0,
				Transactions:  make(map[string]*TxMetrics),
				GroupMetrics:  make(map[string]*GroupLiveMetrics),
				Resources:     snap.Resources,
			}

			if live.TotalTx > 0 {
				live.ErrorPct = float64(live.TotalErrors) / float64(live.TotalTx) * 100
			}

			for name, am := range snap.ByAction {
				live.Transactions[name] = &TxMetrics{
					Name:   name,
					Count:  am.Count,
					Errors: am.Errors,
					AvgMs:  float64(am.AvgDuration.Microseconds()) / 1000.0,
					P50Ms:  float64(am.P50.Microseconds()) / 1000.0,
					P90Ms:  float64(am.P90.Microseconds()) / 1000.0,
					P99Ms:  float64(am.P99.Microseconds()) / 1000.0,
				}
			}

			for name, gm := range snap.ByGroup {
				live.GroupMetrics[name] = &GroupLiveMetrics{
					Name:          name,
					VUsers:        groupVUsers[name],
					Transactions:  gm.Transactions,
					Errors:        gm.Errors,
					TPS:           gm.TPS,
					AvgResponseMs: float64(gm.AvgDuration.Microseconds()) / 1000.0,
					P50Ms:         float64(gm.P50.Microseconds()) / 1000.0,
					P90Ms:         float64(gm.P90.Microseconds()) / 1000.0,
					P99Ms:         float64(gm.P99.Microseconds()) / 1000.0,
				}
			}

			tr.mu.Lock()
			test.LiveMetrics = live
			test.History = append(test.History, live)
			tr.mu.Unlock()

			if tr.store != nil {
				tr.store.SaveHistory(test.ID, live.Timestamp, live)
			}

			tr.hub.BroadcastJSON(map[string]any{
				"type":      "metrics_update",
				"test_id":   test.ID,
				"test_name": test.Name,
				"timestamp": live.Timestamp,
				"data":      live,
			})
		}
	}
}

func (tr *TestRunner) broadcastEvent(eventType string, test *ManagedTest) {
	tr.hub.BroadcastJSON(map[string]any{
		"type":      eventType,
		"test_id":   test.ID,
		"test_name": test.Name,
		"status":    test.Status,
		"timestamp": time.Now(),
	})
}

// applyRuntimeOverrides merges test-level runtime settings into a script config,
// mirroring LoadRunner Controller's override behavior (test-level wins where specified).
func applyRuntimeOverrides(script *dsl.Script, overrides *RuntimeSettingsOverride) {
	if overrides == nil {
		return
	}
	if overrides.Iterations != nil {
		script.Config.RunLogic.Iterations = *overrides.Iterations
	}
	if overrides.Pacing != nil {
		script.Config.Pacing = *overrides.Pacing
	}
	if overrides.ThinkTime != nil {
		script.Config.ThinkTime = *overrides.ThinkTime
	}
	if overrides.ErrorHandling != nil {
		script.Config.ErrorHandling = *overrides.ErrorHandling
	}
	if overrides.LogLevel != "" {
		script.Config.Log.Level = overrides.LogLevel
	}
	if overrides.AutoTransactions != "" {
		script.Config.RunLogic.AutoTransactions = overrides.AutoTransactions
	}
}
