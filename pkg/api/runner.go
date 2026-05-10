package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
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
	ID              string                   `json:"id"`
	Name            string                   `json:"name"`
	Status          string                   `json:"status"`
	Groups          []TestGroup              `json:"groups"`
	ScenarioMode    string                   `json:"scenario_mode,omitempty"`
	ScriptShakeout  bool                     `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                    `json:"resource_baseline,omitempty"`
	GoalConfig      *GoalConfig              `json:"goal_config,omitempty"`
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	TotalVUsers     int                      `json:"total_vusers"`
	Duration        time.Duration            `json:"duration_ns"`
	DurationStr     string                   `json:"duration"`
	CreatedAt       time.Time                `json:"created_at"`
	StartedAt       *time.Time               `json:"started_at,omitempty"`
	EndedAt         *time.Time               `json:"ended_at,omitempty"`
	LiveMetrics     *LiveMetrics             `json:"live_metrics,omitempty"`
	History         []*LiveMetrics           `json:"history,omitempty"`
	Error           string                   `json:"error,omitempty"`

	// deprecated single-script fields kept for backwards compat in JSON
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`

	multiEngine *engine.MultiGroupEngine
	cancel      context.CancelFunc
	groupSpecs  []engine.GroupConfig
	goalControl *goalControllerState
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
	Timestamp     time.Time                    `json:"timestamp"`
	ActiveVUsers  int64                        `json:"active_vusers"`
	TPS           float64                      `json:"tps"`
	AvgResponseMs float64                      `json:"avg_response_ms"`
	ErrorPct      float64                      `json:"error_pct"`
	TotalTx       int64                        `json:"total_transactions"`
	TotalErrors   int64                        `json:"total_errors"`
	P50Ms         float64                      `json:"p50_ms"`
	P90Ms         float64                      `json:"p90_ms"`
	P95Ms         float64                      `json:"p95_ms"`
	P99Ms         float64                      `json:"p99_ms"`
	Transactions  map[string]*TxMetrics        `json:"transactions,omitempty"`
	GroupMetrics  map[string]*GroupLiveMetrics `json:"groups,omitempty"`
	GoalStatus    *GoalStatus                  `json:"goal_status,omitempty"`
	Resources     *engine.SystemResources      `json:"resources,omitempty"`
}

type GoalStatus struct {
	Mode                 string     `json:"mode"`
	CurrentTPS           float64    `json:"current_tps"`
	CurrentTotalVUsers   int        `json:"current_total_vusers"`
	TargetAverageTPS     *float64   `json:"target_average_tps,omitempty"`
	MinVUsers            *int       `json:"min_vusers,omitempty"`
	MaxVUsers            *int       `json:"max_vusers,omitempty"`
	SuggestedTotalVUsers int        `json:"suggested_total_vusers,omitempty"`
	LastScaleDirection   string     `json:"last_scale_direction,omitempty"`
	LastScaleReason      string     `json:"last_scale_reason,omitempty"`
	LastScaleAt          *time.Time `json:"last_scale_at,omitempty"`
	LatencyBreached      bool       `json:"latency_breached,omitempty"`
	ErrorBreached        bool       `json:"error_breached,omitempty"`
	BreachActive         bool       `json:"breach_active,omitempty"`
	BreachDurationSec    int        `json:"breach_duration_sec,omitempty"`
	StopTriggered        bool       `json:"stop_triggered,omitempty"`
	StopReason           string     `json:"stop_reason,omitempty"`
}

type goalControllerState struct {
	breachStartedAt    *time.Time
	lastScaleAt        *time.Time
	lastScaleDirection string
	lastScaleReason    string
}

type goalControllerUpdate struct {
	Status         *GoalStatus
	ShouldScale    bool
	TargetTotal    int
	ScaleDirection string
	ScaleReason    string
	ShouldStop     bool
	StopReason     string
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
				groups := decodeStoredGroups(t.Groups)
				goalConfig := decodeStoredGoalConfig(t.GoalConfig)
				runtimeSettings := decodeStoredRuntimeSettings(t.RuntimeSettings)
				mt := &ManagedTest{
					ID:              t.ID,
					Name:            t.Name,
					Status:          t.Status,
					Groups:          groups,
					ScenarioMode:    normalizeScenarioMode(t.ScenarioMode),
					ScriptShakeout:  t.ScriptShakeout,
					ResourceBaseline: t.ResourceBaseline,
					GoalConfig:      goalConfig,
					RuntimeSettings: runtimeSettings,
					TotalVUsers:     t.TotalVUsers,
					Duration:        time.Duration(t.Duration),
					DurationStr:     time.Duration(t.Duration).String(),
					CreatedAt:       t.StartTime,
					Error:           t.Error,
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
	Name           string             `json:"name"`
	Duration       string             `json:"duration"`
	ScenarioMode   string             `json:"scenario_mode,omitempty"`
	ScriptShakeout bool               `json:"script_shakeout,omitempty"`
	ResourceBaseline bool             `json:"resource_baseline,omitempty"`
	GoalConfig     *GoalConfig        `json:"goal_config,omitempty"`
	Groups         []CreateGroupEntry `json:"groups,omitempty"`
	// Single-script shorthand (backwards compatible)
	ScriptID string `json:"script_id,omitempty"`
	VUsers   int    `json:"vusers,omitempty"`
	RampUp   string `json:"ramp_up,omitempty"`
	// Test-level runtime overrides (mirrors LR Controller override behavior)
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
}

type GoalConfig struct {
	TargetAverageTPS *float64 `json:"target_average_tps,omitempty"`
	P95LatencyMs     *float64 `json:"p95_latency_ms,omitempty"`
	MaxErrorPct      *float64 `json:"max_error_pct,omitempty"`
	MinVUsers        *int     `json:"min_vusers,omitempty"`
	MaxVUsers        *int     `json:"max_vusers,omitempty"`
	StopOnSLABreach  bool     `json:"stop_on_sla_breach,omitempty"`
	SLABreachWindow  string   `json:"sla_breach_window,omitempty"`
}

// RuntimeSettingsOverride allows overriding script-level runtime settings at test time,
// mirroring LoadRunner Controller's override capability.
type RuntimeSettingsOverride struct {
	Iterations       *int                     `json:"iterations,omitempty"`
	Pacing           *dsl.PacingConfig        `json:"pacing,omitempty"`
	ThinkTime        *dsl.ThinkTimeConfig     `json:"think_time,omitempty"`
	ErrorHandling    *dsl.ErrorHandlingConfig `json:"error_handling,omitempty"`
	LogLevel         string                   `json:"log_level,omitempty"`
	AutoTransactions string                   `json:"auto_transactions,omitempty"`
}

type CreateGroupEntry struct {
	Name         string `json:"name"`
	ScriptID     string `json:"script_id"`
	VUsers       int    `json:"vusers"`
	RampUp       string `json:"ramp_up,omitempty"`
	RampDown     string `json:"ramp_down,omitempty"`
	InitStrategy string `json:"initialization,omitempty"` // "at_runtime", "at_once"
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

	scenarioMode := normalizeScenarioMode(req.ScenarioMode)
	if scenarioMode == "goal_oriented" {
		if req.GoalConfig == nil || !hasGoalConfig(req.GoalConfig) {
			return nil, fmt.Errorf("goal-oriented scenarios require at least one goal or guardrail setting")
		}
		if req.GoalConfig.MinVUsers != nil && req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MinVUsers > *req.GoalConfig.MaxVUsers {
			return nil, fmt.Errorf("goal-oriented scenarios require min_vusers to be less than or equal to max_vusers")
		}
		if req.GoalConfig.StopOnSLABreach && req.GoalConfig.SLABreachWindow != "" {
			if _, err := time.ParseDuration(req.GoalConfig.SLABreachWindow); err != nil {
				return nil, fmt.Errorf("invalid sla_breach_window: %w", err)
			}
		}
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
		ID:              uuid.New().String(),
		Name:            testName,
		Status:          "QUEUED",
		Groups:          testGroups,
		ScenarioMode:    scenarioMode,
		ScriptShakeout:  req.ScriptShakeout,
		ResourceBaseline: req.ResourceBaseline,
		GoalConfig:      cloneGoalConfig(req.GoalConfig),
		RuntimeSettings: cloneRuntimeSettings(req.RuntimeSettings),
		TotalVUsers:     totalVUsers,
		Duration:        dur,
		DurationStr:     req.Duration,
		CreatedAt:       time.Now(),
		groupSpecs:      engineGroups,
	}

	// Backwards-compat fields
	if len(testGroups) == 1 {
		test.ScriptID = testGroups[0].ScriptID
		test.ScriptName = testGroups[0].ScriptName
	}

	tr.tests[test.ID] = test

	if tr.store != nil {
		tr.store.SaveTest(&storage.TestRecord{
			ID:              test.ID,
			Name:            test.Name,
			Status:          test.Status,
			ScenarioMode:    test.ScenarioMode,
			ScriptShakeout:  test.ScriptShakeout,
			ResourceBaseline: test.ResourceBaseline,
			TotalVUsers:     test.TotalVUsers,
			Duration:        int64(test.Duration),
			StartTime:       test.CreatedAt,
			Groups:          test.Groups,
			GoalConfig:      test.GoalConfig,
			RuntimeSettings: test.RuntimeSettings,
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

func (tr *TestRunner) ScaleVUsers(id string, groupName string, targetVUsers int) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}
	if test.multiEngine == nil {
		return fmt.Errorf("engine not initialized")
	}
	if targetVUsers < 0 {
		return fmt.Errorf("target vusers must be non-negative")
	}

	if err := test.multiEngine.ScaleVUsers(groupName, targetVUsers); err != nil {
		return err
	}

	// Update the test's group metadata
	tr.mu.Lock()
	totalVUsers := 0
	for i := range test.Groups {
		if test.Groups[i].Name == groupName {
			test.Groups[i].VUsers = targetVUsers
		}
		totalVUsers += test.Groups[i].VUsers
	}
	test.TotalVUsers = totalVUsers
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":          "vusers_changed",
		"test_id":       id,
		"group":         groupName,
		"target_vusers": targetVUsers,
		"total_vusers":  totalVUsers,
		"timestamp":     time.Now(),
	})

	return nil
}

func (tr *TestRunner) AdjustDuration(id string, newDuration time.Duration) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to adjust duration", test.Status)
	}
	if test.multiEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	if err := test.multiEngine.AdjustDuration(newDuration); err != nil {
		return err
	}

	tr.mu.Lock()
	test.Duration = newDuration
	test.DurationStr = newDuration.String()
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":         "duration_changed",
		"test_id":      id,
		"new_duration": newDuration.String(),
		"timestamp":    time.Now(),
	})

	return nil
}

func (tr *TestRunner) runTest(test *ManagedTest) {
	if test.ScriptShakeout {
		if err := tr.runScriptShakeout(test); err != nil {
			now := time.Now()
			tr.mu.Lock()
			test.Status = "FAILED"
			test.Error = err.Error()
			test.EndedAt = &now
			tr.mu.Unlock()
			if tr.store != nil {
				tr.store.UpdateTestStatus(test.ID, test.Status, test.EndedAt, test.Error)
			}
			tr.broadcastEvent("test_failed", test)
			return
		}
	}
	baselineDuration := 5 * time.Minute
	preBaseline := time.Duration(0)
	postBaseline := time.Duration(0)
	if test.ResourceBaseline {
		preBaseline = baselineDuration
		postBaseline = baselineDuration
	}

	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:       test.groupSpecs,
		Duration:     test.Duration,
		PreBaseline:  preBaseline,
		PostBaseline: postBaseline,
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
	test.goalControl = &goalControllerState{}
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

func (tr *TestRunner) runScriptShakeout(test *ManagedTest) error {
	for _, group := range test.groupSpecs {
		if group.Script == nil {
			return fmt.Errorf("script shakeout failed for %s: script is missing", group.Name)
		}

		shakeoutScript := *group.Script
		shakeoutScript.Config.RunLogic.Iterations = 1

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := engine.RunScriptShakeout(ctx, group.Name, &shakeoutScript)
		cancel()
		if err != nil {
			return fmt.Errorf("script shakeout failed for %s: %w", group.Name, err)
		}
	}

	return nil
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

			if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
				currentTotalVUsers := totalGroupVUsers(groupVUsers)
				update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
				live.GoalStatus = update.Status

				if update.ShouldScale {
					targets := distributeTargetVUsers(test.Groups, groupVUsers, update.TargetTotal)
					for _, group := range test.Groups {
						target, exists := targets[group.Name]
						if !exists || groupVUsers[group.Name] == target {
							continue
						}
						if err := mge.ScaleVUsers(group.Name, target); err != nil {
							slog.Warn("goal-oriented scale failed", "test_id", test.ID, "group", group.Name, "target_vusers", target, "error", err)
							live.GoalStatus.LastScaleReason = fmt.Sprintf("scale failed: %v", err)
							break
						}
						groupVUsers[group.Name] = target
						for i := range test.Groups {
							if test.Groups[i].Name == group.Name {
								test.Groups[i].VUsers = target
								break
							}
						}
					}
					now := live.Timestamp
					test.goalControl.lastScaleAt = &now
					test.goalControl.lastScaleDirection = update.ScaleDirection
					test.goalControl.lastScaleReason = update.ScaleReason
					live.GoalStatus.LastScaleAt = &now
					live.GoalStatus.LastScaleDirection = update.ScaleDirection
					live.GoalStatus.LastScaleReason = update.ScaleReason
					live.GoalStatus.CurrentTotalVUsers = totalGroupVUsers(groupVUsers)
					live.GoalStatus.SuggestedTotalVUsers = live.GoalStatus.CurrentTotalVUsers
				}
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

			if live.GoalStatus != nil && live.GoalStatus.StopTriggered && test.Status == "RUNNING" {
				if err := tr.StopTest(test.ID); err != nil {
					slog.Warn("goal-oriented stop failed", "test_id", test.ID, "error", err)
				}
			}
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

func cloneRuntimeSettings(overrides *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if overrides == nil {
		return nil
	}
	clone := *overrides
	if overrides.Iterations != nil {
		iterations := *overrides.Iterations
		clone.Iterations = &iterations
	}
	if overrides.Pacing != nil {
		pacing := *overrides.Pacing
		clone.Pacing = &pacing
	}
	if overrides.ThinkTime != nil {
		thinkTime := *overrides.ThinkTime
		clone.ThinkTime = &thinkTime
	}
	if overrides.ErrorHandling != nil {
		errorHandling := *overrides.ErrorHandling
		clone.ErrorHandling = &errorHandling
	}
	return &clone
}

func cloneGoalConfig(config *GoalConfig) *GoalConfig {
	if config == nil {
		return nil
	}
	clone := *config
	if config.TargetAverageTPS != nil {
		value := *config.TargetAverageTPS
		clone.TargetAverageTPS = &value
	}
	if config.P95LatencyMs != nil {
		value := *config.P95LatencyMs
		clone.P95LatencyMs = &value
	}
	if config.MaxErrorPct != nil {
		value := *config.MaxErrorPct
		clone.MaxErrorPct = &value
	}
	if config.MinVUsers != nil {
		value := *config.MinVUsers
		clone.MinVUsers = &value
	}
	if config.MaxVUsers != nil {
		value := *config.MaxVUsers
		clone.MaxVUsers = &value
	}
	return &clone
}

func normalizeScenarioMode(mode string) string {
	if mode == "goal_oriented" {
		return mode
	}
	return "vuser_driven"
}

func hasGoalConfig(config *GoalConfig) bool {
	if config == nil {
		return false
	}
	return config.TargetAverageTPS != nil ||
		config.P95LatencyMs != nil ||
		config.MaxErrorPct != nil ||
		config.MinVUsers != nil ||
		config.MaxVUsers != nil ||
		config.StopOnSLABreach ||
		config.SLABreachWindow != ""
}

func evaluateGoalController(now time.Time, currentTotalVUsers int, live *LiveMetrics, config *GoalConfig, state *goalControllerState) goalControllerUpdate {
	status := &GoalStatus{
		Mode:                 "goal_oriented",
		CurrentTPS:           live.TPS,
		CurrentTotalVUsers:   currentTotalVUsers,
		TargetAverageTPS:     config.TargetAverageTPS,
		MinVUsers:            config.MinVUsers,
		MaxVUsers:            config.MaxVUsers,
		SuggestedTotalVUsers: currentTotalVUsers,
	}
	if state != nil {
		status.LastScaleAt = state.lastScaleAt
		status.LastScaleDirection = state.lastScaleDirection
		status.LastScaleReason = state.lastScaleReason
	}

	latencyBreached := config.P95LatencyMs != nil && live.P95Ms > *config.P95LatencyMs
	errorBreached := config.MaxErrorPct != nil && live.ErrorPct > *config.MaxErrorPct
	status.LatencyBreached = latencyBreached
	status.ErrorBreached = errorBreached
	status.BreachActive = latencyBreached || errorBreached

	if status.BreachActive {
		if state != nil && state.breachStartedAt == nil {
			started := now
			state.breachStartedAt = &started
		}
		if state != nil && state.breachStartedAt != nil {
			status.BreachDurationSec = int(now.Sub(*state.breachStartedAt).Seconds())
		}
	} else if state != nil {
		state.breachStartedAt = nil
	}

	if config.TargetAverageTPS != nil && shouldEvaluateScale(now, state) {
		minTPS := *config.TargetAverageTPS * 0.95
		maxTPS := *config.TargetAverageTPS * 1.05
		desiredTotal := currentTotalVUsers
		scaleDirection := ""
		scaleReason := ""

		if live.TPS < minTPS {
			deltaRatio := (*config.TargetAverageTPS - live.TPS) / math.Max(*config.TargetAverageTPS, 1)
			step := goalScaleStep(currentTotalVUsers, deltaRatio)
			desiredTotal = currentTotalVUsers + step
			scaleDirection = "up"
			scaleReason = "below_target_tps"
		} else if live.TPS > maxTPS {
			deltaRatio := (live.TPS - *config.TargetAverageTPS) / math.Max(live.TPS, 1)
			step := goalScaleStep(currentTotalVUsers, deltaRatio)
			desiredTotal = currentTotalVUsers - step
			scaleDirection = "down"
			scaleReason = "above_target_tps"
		}

		desiredTotal = clampGoalVUsers(desiredTotal, config)
		status.SuggestedTotalVUsers = desiredTotal
		if desiredTotal != currentTotalVUsers {
			return goalControllerUpdate{
				Status:         status,
				ShouldScale:    true,
				TargetTotal:    desiredTotal,
				ScaleDirection: scaleDirection,
				ScaleReason:    scaleReason,
			}
		}
	}

	if config.StopOnSLABreach && status.BreachActive {
		window := goalBreachWindow(config)
		if time.Duration(status.BreachDurationSec)*time.Second >= window {
			status.StopTriggered = true
			status.StopReason = goalStopReason(latencyBreached, errorBreached)
			return goalControllerUpdate{
				Status:     status,
				ShouldStop: true,
				StopReason: status.StopReason,
			}
		}
	}

	return goalControllerUpdate{Status: status}
}

func shouldEvaluateScale(now time.Time, state *goalControllerState) bool {
	if state == nil || state.lastScaleAt == nil {
		return true
	}
	return now.Sub(*state.lastScaleAt) >= 5*time.Second
}

func goalScaleStep(currentTotalVUsers int, deltaRatio float64) int {
	base := currentTotalVUsers
	if base < 1 {
		base = 1
	}
	step := int(math.Ceil(float64(base) * math.Min(math.Max(deltaRatio, 0.05), 0.25)))
	if step < 1 {
		return 1
	}
	return step
}

func clampGoalVUsers(value int, config *GoalConfig) int {
	if config == nil {
		if value < 0 {
			return 0
		}
		return value
	}
	if config.MinVUsers != nil && value < *config.MinVUsers {
		value = *config.MinVUsers
	}
	if config.MaxVUsers != nil && value > *config.MaxVUsers {
		value = *config.MaxVUsers
	}
	if value < 0 {
		return 0
	}
	return value
}

func goalBreachWindow(config *GoalConfig) time.Duration {
	if config == nil || config.SLABreachWindow == "" {
		return 30 * time.Second
	}
	window, err := time.ParseDuration(config.SLABreachWindow)
	if err != nil || window <= 0 {
		return 30 * time.Second
	}
	return window
}

func goalStopReason(latencyBreached bool, errorBreached bool) string {
	switch {
	case latencyBreached && errorBreached:
		return "latency and error SLA breached"
	case latencyBreached:
		return "p95 latency SLA breached"
	case errorBreached:
		return "error rate SLA breached"
	default:
		return "SLA breached"
	}
}

func totalGroupVUsers(groupVUsers map[string]int) int {
	total := 0
	for _, vusers := range groupVUsers {
		total += vusers
	}
	return total
}

func distributeTargetVUsers(groups []TestGroup, current map[string]int, desiredTotal int) map[string]int {
	if len(groups) == 0 {
		return nil
	}
	if desiredTotal < 0 {
		desiredTotal = 0
	}

	totalCurrent := 0
	for _, group := range groups {
		totalCurrent += current[group.Name]
	}

	targets := make(map[string]int, len(groups))
	if totalCurrent <= 0 {
		remaining := desiredTotal
		for idx, group := range groups {
			share := 0
			if len(groups)-idx > 0 {
				share = remaining / (len(groups) - idx)
			}
			targets[group.Name] = share
			remaining -= share
		}
		return targets
	}

	type allocation struct {
		name      string
		base      int
		remainder float64
	}
	allocations := make([]allocation, 0, len(groups))
	assigned := 0
	for _, group := range groups {
		exact := float64(desiredTotal) * float64(current[group.Name]) / float64(totalCurrent)
		base := int(math.Floor(exact))
		allocations = append(allocations, allocation{name: group.Name, base: base, remainder: exact - float64(base)})
		assigned += base
	}

	remaining := desiredTotal - assigned
	for i := 0; i < len(allocations) && remaining > 0; i++ {
		bestIdx := i
		for j := i + 1; j < len(allocations); j++ {
			if allocations[j].remainder > allocations[bestIdx].remainder {
				bestIdx = j
			}
		}
		allocations[i], allocations[bestIdx] = allocations[bestIdx], allocations[i]
		allocations[i].base++
		remaining--
	}

	for _, allocation := range allocations {
		targets[allocation.name] = allocation.base
	}
	return targets
}

func decodeStoredGroups(value any) []TestGroup {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var groups []TestGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil
	}
	return groups
}

func decodeStoredRuntimeSettings(value any) *RuntimeSettingsOverride {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var overrides RuntimeSettingsOverride
	if err := json.Unmarshal(data, &overrides); err != nil {
		return nil
	}
	return cloneRuntimeSettings(&overrides)
}

func decodeStoredGoalConfig(value any) *GoalConfig {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var config GoalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil
	}
	return cloneGoalConfig(&config)
}
