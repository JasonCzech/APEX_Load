package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type scriptPersistence interface {
	SaveScript(script *dsl.Script) error
	DeleteScript(id string) error
	ListScripts() ([]*dsl.Script, error)
}

type testPersistence interface {
	SaveTest(test *storage.TestRecord) error
	ListTests() ([]*storage.TestRecord, error)
	UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error
	DeleteTest(id string) error
}

type historyPersistence interface {
	SaveHistory(testID string, timestamp time.Time, metrics any) error
	GetHistory(testID string) ([]any, error)
}

type archivePersistence interface {
	SaveRunArchive(record *storage.RunArchiveRecord) error
	GetRunArchive(testID string) (*storage.RunArchiveRecord, error)
}

type settingsPersistence interface {
	SaveSetting(key string, value string) error
	GetSetting(key string) (string, error)
}

type TestRunner struct {
	tests             map[string]*ManagedTest
	scripts           map[string]*StoredScript
	archives          map[string]*storage.RunArchiveRecord
	mu                sync.RWMutex
	hub               *Hub
	maxTests          int
	settings          SystemSettings
	scriptStore       scriptPersistence
	testStore         testPersistence
	historyStore      historyPersistence
	archiveStore      archivePersistence
	settingsStore     settingsPersistence
}

type ManagedTest struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	Status           string                    `json:"status"`
	Groups           []TestGroup               `json:"groups"`
	CapacityAdvisory *CapacityAdvisory         `json:"capacity_advisory,omitempty"`
	ScenarioMode     string                    `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                      `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                      `json:"resource_baseline,omitempty"`
	GoalConfig       *GoalConfig               `json:"goal_config,omitempty"`
	RuntimeSettings  *RuntimeSettingsOverride  `json:"runtime_settings,omitempty"`
	TotalVUsers      int                       `json:"total_vusers"`
	Duration         time.Duration             `json:"duration_ns"`
	DurationStr      string                    `json:"duration"`
	CreatedAt        time.Time                 `json:"created_at"`
	StartedAt        *time.Time                `json:"started_at,omitempty"`
	EndedAt          *time.Time                `json:"ended_at,omitempty"`
	LiveMetrics      *LiveMetrics              `json:"live_metrics,omitempty"`
	History          []*LiveMetrics            `json:"history,omitempty"`
	Archive          *storage.RunArchiveRecord `json:"archive,omitempty"`
	Error            string                    `json:"error,omitempty"`

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
		tests:         make(map[string]*ManagedTest),
		scripts:       make(map[string]*StoredScript),
		archives:      make(map[string]*storage.RunArchiveRecord),
		hub:           hub,
		maxTests:      maxTests,
		scriptStore:   store,
		testStore:     store,
		historyStore:  store,
		archiveStore:  store,
		settingsStore: store,
	}
	tr.loadSystemSettings(maxTests)
	tr.loadStoredScripts()
	tr.loadStoredTests()

	return tr
}

func (tr *TestRunner) loadStoredScripts() {
	if tr.scriptStore == nil {
		return
	}
	scripts, err := tr.scriptStore.ListScripts()
	if err != nil {
		return
	}
	for _, script := range scripts {
		tr.scripts[script.ID] = &StoredScript{
			ID:        script.ID,
			Name:      script.Name,
			Protocol:  script.Protocol,
			Version:   script.Version,
			CreatedAt: script.CreatedAt,
			Script:    script,
		}
	}
}

func (tr *TestRunner) loadStoredTests() {
	if tr.testStore == nil {
		return
	}
	tests, err := tr.testStore.ListTests()
	if err != nil {
		return
	}
	for _, record := range tests {
		managedTest := managedTestFromRecord(record)
		tr.loadStoredTestHistory(managedTest)
		tr.loadStoredTestArchive(managedTest)
		tr.tests[record.ID] = managedTest
	}
}

func managedTestFromRecord(record *storage.TestRecord) *ManagedTest {
	managedTest := &ManagedTest{
		ID:               record.ID,
		Name:             record.Name,
		Status:           record.Status,
		Groups:           decodeStoredGroups(record.Groups),
		ScenarioMode:     normalizeScenarioMode(record.ScenarioMode),
		ScriptShakeout:   record.ScriptShakeout,
		ResourceBaseline: record.ResourceBaseline,
		GoalConfig:       decodeStoredGoalConfig(record.GoalConfig),
		RuntimeSettings:  decodeStoredRuntimeSettings(record.RuntimeSettings),
		TotalVUsers:      record.TotalVUsers,
		Duration:         time.Duration(record.Duration),
		DurationStr:      time.Duration(record.Duration).String(),
		CreatedAt:        record.StartTime,
		Error:            record.Error,
	}
	if record.EndTime != nil {
		managedTest.EndedAt = record.EndTime
	}
	return managedTest
}

func (tr *TestRunner) loadStoredTestHistory(test *ManagedTest) {
	if tr.historyStore == nil {
		return
	}
	history, err := tr.historyStore.GetHistory(test.ID)
	if err != nil {
		return
	}
	for _, entry := range history {
		if metrics, ok := entry.(map[string]any); ok {
			// This is a bit lossy due to any -> map conversion,
			// but works for basic history display.
			test.History = append(test.History, mapToLiveMetrics(metrics))
		}
	}
	if len(test.History) > 0 {
		test.LiveMetrics = test.History[len(test.History)-1]
	}
}

func (tr *TestRunner) loadStoredTestArchive(test *ManagedTest) {
	if tr.archiveStore == nil {
		return
	}
	archive, err := tr.archiveStore.GetRunArchive(test.ID)
	if err != nil {
		return
	}
	test.Archive = archive
	tr.archives[test.ID] = archive
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

	tr.saveStoredScript(script)

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
	tr.saveStoredScript(script)
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
	tr.deleteStoredScript(id)
	return true
}

type CreateTestRequest struct {
	Name             string             `json:"name"`
	Duration         string             `json:"duration"`
	ScenarioMode     string             `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool               `json:"script_shakeout,omitempty"`
	ResourceBaseline bool               `json:"resource_baseline,omitempty"`
	GoalConfig       *GoalConfig        `json:"goal_config,omitempty"`
	Groups           []CreateGroupEntry `json:"groups,omitempty"`
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

type StartTestOptions struct {
	OverrideCapacityShortfall bool `json:"override_capacity_shortfall,omitempty"`
}

type CapacityShortfallError struct {
	TestID   string
	Advisory *CapacityAdvisory
	Message  string
}

type DraftCapacityEstimate struct {
	ScenarioMode     string            `json:"scenario_mode,omitempty"`
	TotalVUsers      int               `json:"total_vusers"`
	Duration         string            `json:"duration"`
	ResourceBaseline bool              `json:"resource_baseline,omitempty"`
	Groups           []TestGroup       `json:"groups"`
	CapacityAdvisory *CapacityAdvisory `json:"capacity_advisory,omitempty"`
}

func (e *CapacityShortfallError) Error() string {
	if e == nil {
		return "capacity shortfall"
	}
	if e.Message != "" {
		return e.Message
	}
	return "insufficient load generator capacity"
}

func (tr *TestRunner) EstimateDraftCapacity(req CreateTestRequest) (*DraftCapacityEstimate, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	preview, err := tr.buildManagedTestLocked(req, "draft-estimate", "DRAFT")
	if err != nil {
		return nil, err
	}

	return &DraftCapacityEstimate{
		ScenarioMode:     preview.ScenarioMode,
		TotalVUsers:      preview.TotalVUsers,
		Duration:         preview.DurationStr,
		ResourceBaseline: preview.ResourceBaseline,
		Groups:           preview.Groups,
		CapacityAdvisory: cloneCapacityAdvisory(preview.CapacityAdvisory),
	}, nil
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
	maxConcurrent := tr.settings.General.MaxConcurrentTests
	if running >= maxConcurrent {
		return nil, fmt.Errorf("max concurrent tests (%d) reached", maxConcurrent)
	}

	test, err := tr.buildManagedTestLocked(req, uuid.New().String(), "QUEUED")
	if err != nil {
		return nil, err
	}

	tr.tests[test.ID] = test

	tr.saveManagedTestRecord(test)

	tr.broadcastEvent("test_created", test)
	return test, nil
}

func (tr *TestRunner) buildManagedTestLocked(req CreateTestRequest, id string, status string) (*ManagedTest, error) {
	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}

	scenarioMode, err := validateScenarioRequest(req)
	if err != nil {
		return nil, err
	}

	groups, err := normalizeCreateTestGroups(req)
	if err != nil {
		return nil, err
	}

	testGroups, engineGroups, totalVUsers, err := tr.compileManagedTestGroupsLocked(groups, req.RuntimeSettings)
	if err != nil {
		return nil, err
	}

	testName := deriveManagedTestName(req.Name, testGroups)

	test := &ManagedTest{
		ID:               id,
		Name:             testName,
		Status:           status,
		Groups:           testGroups,
		CapacityAdvisory: estimateCapacityAdvisory(tr.settings, testGroups, totalVUsers, dur, req.ResourceBaseline, req.RuntimeSettings),
		ScenarioMode:     scenarioMode,
		ScriptShakeout:   req.ScriptShakeout,
		ResourceBaseline: req.ResourceBaseline,
		GoalConfig:       cloneGoalConfig(req.GoalConfig),
		RuntimeSettings:  cloneRuntimeSettings(req.RuntimeSettings),
		TotalVUsers:      totalVUsers,
		Duration:         dur,
		DurationStr:      req.Duration,
		CreatedAt:        time.Now(),
		groupSpecs:       engineGroups,
	}

	if len(testGroups) == 1 {
		test.ScriptID = testGroups[0].ScriptID
		test.ScriptName = testGroups[0].ScriptName
	}

	return test, nil
}

func validateScenarioRequest(req CreateTestRequest) (string, error) {
	scenarioMode := normalizeScenarioMode(req.ScenarioMode)
	if scenarioMode != "goal_oriented" {
		return scenarioMode, nil
	}

	if req.GoalConfig == nil || !hasGoalConfig(req.GoalConfig) {
		return "", fmt.Errorf("goal-oriented scenarios require at least one goal or guardrail setting")
	}
	if req.GoalConfig.MinVUsers != nil && req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MinVUsers > *req.GoalConfig.MaxVUsers {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be less than or equal to max_vusers")
	}
	if req.GoalConfig.StopOnSLABreach && req.GoalConfig.SLABreachWindow != "" {
		if _, err := time.ParseDuration(req.GoalConfig.SLABreachWindow); err != nil {
			return "", fmt.Errorf("invalid sla_breach_window: %w", err)
		}
	}

	return scenarioMode, nil
}

func normalizeCreateTestGroups(req CreateTestRequest) ([]CreateGroupEntry, error) {
	if len(req.Groups) > 0 {
		return req.Groups, nil
	}
	if req.ScriptID != "" {
		return []CreateGroupEntry{{
			ScriptID: req.ScriptID,
			VUsers:   req.VUsers,
			RampUp:   req.RampUp,
		}}, nil
	}
	return nil, fmt.Errorf("at least one group (or script_id) is required")
}

func (tr *TestRunner) compileManagedTestGroupsLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride) ([]TestGroup, []engine.GroupConfig, int, error) {
	testGroups := make([]TestGroup, 0, len(groups))
	engineGroups := make([]engine.GroupConfig, 0, len(groups))
	totalVUsers := 0

	for i, group := range groups {
		testGroup, engineGroup, err := tr.compileManagedTestGroupLocked(i, len(groups), group, runtimeSettings)
		if err != nil {
			return nil, nil, 0, err
		}
		testGroups = append(testGroups, testGroup)
		engineGroups = append(engineGroups, engineGroup)
		totalVUsers += testGroup.VUsers
	}

	return testGroups, engineGroups, totalVUsers, nil
}

func (tr *TestRunner) compileManagedTestGroupLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride) (TestGroup, engine.GroupConfig, error) {
	script, ok := tr.scripts[group.ScriptID]
	if !ok {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script not found: %s", index, group.ScriptID)
	}

	vusers := group.VUsers
	if vusers <= 0 {
		vusers = 10
	}

	rampUp, err := parseOptionalGroupDuration(group.RampUp, index, "ramp_up")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	rampDown, err := parseOptionalGroupDuration(group.RampDown, index, "ramp_down")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}

	name := group.Name
	if name == "" {
		name = script.Name
		if totalGroups > 1 {
			name = fmt.Sprintf("%s (#%d)", script.Name, index+1)
		}
	}

	initStrategy := group.InitStrategy
	if initStrategy == "" {
		initStrategy = "at_runtime"
	}

	testGroup := TestGroup{
		Name:           name,
		ScriptID:       group.ScriptID,
		ScriptName:     script.Name,
		Protocol:       script.Protocol,
		VUsers:         vusers,
		RampUp:         group.RampUp,
		RampDown:       group.RampDown,
		Initialization: initStrategy,
		rampUpDur:      rampUp,
		script:         script.Script,
	}

	scriptCopy := *script.Script
	if runtimeSettings != nil {
		applyRuntimeOverrides(&scriptCopy, runtimeSettings)
	}

	engineGroup := engine.GroupConfig{
		Name:         name,
		Script:       &scriptCopy,
		VUsers:       vusers,
		RampUp:       rampUp,
		RampDown:     rampDown,
		InitStrategy: initStrategy,
	}

	return testGroup, engineGroup, nil
}

func parseOptionalGroupDuration(value string, index int, fieldName string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("group[%d]: invalid %s: %w", index, fieldName, err)
	}
	return duration, nil
}

func deriveManagedTestName(requestName string, groups []TestGroup) string {
	if requestName != "" {
		return requestName
	}
	if len(groups) == 1 {
		return groups[0].ScriptName
	}
	return fmt.Sprintf("Multi-group (%d groups)", len(groups))
}

func (tr *TestRunner) GetTest(id string) (*ManagedTest, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	t, ok := tr.tests[id]
	if ok && t.Status == "QUEUED" {
		tr.refreshCapacityAdvisoryLocked(t)
	}
	return t, ok
}

func (tr *TestRunner) ListTests() []*ManagedTest {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) GetRunArchive(id string) (*storage.RunArchiveRecord, bool) {
	tr.mu.RLock()
	record, ok := tr.archives[id]
	tr.mu.RUnlock()
	if ok {
		return record, true
	}
	record, ok = tr.loadRunArchiveFromStore(id)
	if !ok {
		return nil, false
	}
	tr.mu.Lock()
	tr.archives[id] = record
	if test, exists := tr.tests[id]; exists {
		test.Archive = record
	}
	tr.mu.Unlock()
	return record, true
}

func (tr *TestRunner) GetRunArchiveManifest(id string) (*RunArchiveManifest, error) {
	record, ok := tr.GetRunArchive(id)
	if !ok {
		return nil, fmt.Errorf("archive not found: %s", id)
	}
	return loadRunArchiveManifest(record.ManifestPath)
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
	archivePath := ""
	if archive, exists := tr.archives[id]; exists {
		archivePath = archive.ArchivePath
	} else if test.Archive != nil {
		archivePath = test.Archive.ArchivePath
	}
	delete(tr.tests, id)
	delete(tr.archives, id)
	tr.deleteStoredTest(id)
	if archivePath != "" {
		if err := os.RemoveAll(archivePath); err != nil {
			slog.Warn("failed to remove archive directory", "test_id", id, "archive_path", archivePath, "error", err)
		}
	}
	return true
}

func (tr *TestRunner) StartTest(id string) error {
	return tr.StartTestWithOptions(id, StartTestOptions{})
}

func (tr *TestRunner) StartTestWithOptions(id string, opts StartTestOptions) error {
	tr.mu.Lock()
	test, err := tr.prepareTestStartLocked(id, opts)
	tr.mu.Unlock()
	if err != nil {
		return err
	}

	go tr.runTest(test)
	return nil
}

func (tr *TestRunner) prepareTestStartLocked(id string, opts StartTestOptions) (*ManagedTest, error) {
	maxConcurrent := tr.settings.General.MaxConcurrentTests
	if tr.countActiveTestsLocked() >= maxConcurrent {
		return nil, fmt.Errorf("max concurrent tests (%d) reached", maxConcurrent)
	}

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "QUEUED" {
		return nil, fmt.Errorf("test is %s, must be QUEUED to start", test.Status)
	}

	tr.refreshCapacityAdvisoryLocked(test)
	if err := buildCapacityShortfallStartError(id, test.CapacityAdvisory, opts); err != nil {
		return nil, err
	}

	test.Status = "INITIALIZING"
	return test, nil
}

func (tr *TestRunner) countActiveTestsLocked() int {
	active := 0
	for _, test := range tr.tests {
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			active++
		}
	}
	return active
}

func buildCapacityShortfallStartError(id string, advisory *CapacityAdvisory, opts StartTestOptions) error {
	if advisory == nil || !advisory.RequiresOverride || opts.OverrideCapacityShortfall {
		return nil
	}
	cloned := cloneCapacityAdvisory(advisory)
	return &CapacityShortfallError{
		TestID:   id,
		Advisory: cloned,
		Message:  fmt.Sprintf("capacity advisory recommends %d load generators but only %d are currently available", cloned.RecommendedGenerators, cloned.AvailableGenerators),
	}
}

func (tr *TestRunner) refreshCapacityAdvisoryLocked(test *ManagedTest) {
	if test == nil {
		return
	}
	groups := tr.hydrateCapacityGroupsLocked(test.Groups)
	test.CapacityAdvisory = estimateCapacityAdvisory(tr.settings, groups, test.TotalVUsers, test.Duration, test.ResourceBaseline, test.RuntimeSettings)
}

func (tr *TestRunner) loadRunArchiveFromStore(id string) (*storage.RunArchiveRecord, bool) {
	if tr.archiveStore == nil {
		return nil, false
	}
	record, err := tr.archiveStore.GetRunArchive(id)
	if err != nil {
		return nil, false
	}
	return record, true
}

func (tr *TestRunner) saveStoredScript(script *dsl.Script) {
	if tr.scriptStore != nil {
		tr.scriptStore.SaveScript(script)
	}
}

func (tr *TestRunner) deleteStoredScript(id string) {
	if tr.scriptStore != nil {
		tr.scriptStore.DeleteScript(id)
	}
}

func buildManagedTestRecord(test *ManagedTest) *storage.TestRecord {
	if test == nil {
		return nil
	}
	return &storage.TestRecord{
		ID:               test.ID,
		Name:             test.Name,
		Status:           test.Status,
		ScenarioMode:     test.ScenarioMode,
		ScriptShakeout:   test.ScriptShakeout,
		ResourceBaseline: test.ResourceBaseline,
		TotalVUsers:      test.TotalVUsers,
		Duration:         int64(test.Duration),
		StartTime:        test.CreatedAt,
		Groups:           test.Groups,
		GoalConfig:       test.GoalConfig,
		RuntimeSettings:  test.RuntimeSettings,
	}
}

func (tr *TestRunner) saveManagedTestRecord(test *ManagedTest) {
	if tr.testStore != nil {
		tr.testStore.SaveTest(buildManagedTestRecord(test))
	}
}

func (tr *TestRunner) deleteStoredTest(id string) {
	if tr.testStore != nil {
		tr.testStore.DeleteTest(id)
	}
}

func (tr *TestRunner) saveLiveMetricsHistory(testID string, timestamp time.Time, live *LiveMetrics) {
	if tr.historyStore != nil {
		tr.historyStore.SaveHistory(testID, timestamp, live)
	}
}

func (tr *TestRunner) updateStoredTestStatus(testID string, status string, endTime *time.Time, errStr string) {
	if tr.testStore != nil {
		tr.testStore.UpdateTestStatus(testID, status, endTime, errStr)
	}
}

func (tr *TestRunner) saveRunArchiveRecord(record *storage.RunArchiveRecord) error {
	if tr.archiveStore == nil || record == nil {
		return nil
	}
	return tr.archiveStore.SaveRunArchive(record)
}

func (tr *TestRunner) hydrateCapacityGroupsLocked(groups []TestGroup) []TestGroup {
	if len(groups) == 0 {
		return nil
	}
	hydrated := make([]TestGroup, len(groups))
	for i := range groups {
		hydrated[i] = groups[i]
		if hydrated[i].script != nil {
			continue
		}
		if script, ok := tr.scripts[hydrated[i].ScriptID]; ok {
			hydrated[i].script = script.Script
			if hydrated[i].Protocol == "" {
				hydrated[i].Protocol = script.Protocol
			}
			if hydrated[i].ScriptName == "" {
				hydrated[i].ScriptName = script.Name
			}
		}
	}
	return hydrated
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
			tr.failTestBeforeRun(test, err.Error())
			return
		}
	}
	archiveWriter, err := newRunArchiveWriter(test)
	if err != nil {
		tr.failTestBeforeRun(test, fmt.Sprintf("initialize archive writer: %v", err))
		return
	}
	mge, err := tr.buildRunEngine(test, archiveWriter)
	if err != nil {
		tr.failTestBeforeRun(test, err.Error())
		return
	}

	ctx, metricsDone, metricsStopped := tr.startManagedTestRun(test, mge, archiveWriter)

	result, err := mge.Run(ctx)

	close(metricsDone)
	<-metricsStopped

	endTime := time.Now()
	finalStatus := "COMPLETE"
	finalError := ""
	if err != nil {
		finalStatus = "FAILED"
		finalError = err.Error()
	}

	archiveRecord, finalStatus, finalError := tr.finalizeRunArchive(archiveWriter, finalStatus, endTime, finalError)
	tr.completeTestRun(test, finalStatus, endTime, finalError, archiveRecord)

	slog.Info("test finished",
		"id", test.ID,
		"status", test.Status,
		"groups", len(test.Groups),
		"transactions", result.TotalTransactions,
		"errors", result.TotalErrors,
		"duration", result.Duration,
	)
}

func (tr *TestRunner) buildRunEngine(test *ManagedTest, archiveWriter *runArchiveWriter) (*engine.MultiGroupEngine, error) {
	preBaseline, postBaseline := resourceBaselineWindows(test.ResourceBaseline)
	return engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:       cloneGroupSpecsWithResultSink(test.groupSpecs, archiveWriter),
		Duration:     test.Duration,
		PreBaseline:  preBaseline,
		PostBaseline: postBaseline,
	})
}

func cloneGroupSpecsWithResultSink(groupSpecs []engine.GroupConfig, resultSink engine.ResultSink) []engine.GroupConfig {
	cloned := make([]engine.GroupConfig, len(groupSpecs))
	copy(cloned, groupSpecs)
	for i := range cloned {
		cloned[i].ResultSink = resultSink
	}
	return cloned
}

func resourceBaselineWindows(enabled bool) (time.Duration, time.Duration) {
	if !enabled {
		return 0, 0
	}
	const baselineWindow = 5 * time.Minute
	return baselineWindow, baselineWindow
}

func buildLiveMetricsFromSnapshot(snap engine.MetricsSnapshot, groupVUsers map[string]int) *LiveMetrics {
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

	return live
}

func buildThresholdAlerts(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings) []map[string]any {
	alerts := make([]map[string]any, 0, 2)
	if thresholds.MaxP99LatencyMs != nil && live.P99Ms > *thresholds.MaxP99LatencyMs {
		alerts = append(alerts, map[string]any{
			"type":      "threshold_alert",
			"test_id":   test.ID,
			"test_name": test.Name,
			"timestamp": live.Timestamp,
			"metric":    "p99_ms",
			"value":     live.P99Ms,
			"threshold": *thresholds.MaxP99LatencyMs,
		})
	}
	if thresholds.MaxErrorRatePct != nil && live.ErrorPct > *thresholds.MaxErrorRatePct {
		alerts = append(alerts, map[string]any{
			"type":      "threshold_alert",
			"test_id":   test.ID,
			"test_name": test.Name,
			"timestamp": live.Timestamp,
			"metric":    "error_pct",
			"value":     live.ErrorPct,
			"threshold": *thresholds.MaxErrorRatePct,
			"action":    "stop_requested",
		})
	}
	return alerts
}

func shouldStopForErrorThreshold(live *LiveMetrics, thresholds ThresholdSettings) bool {
	return thresholds.MaxErrorRatePct != nil && live.ErrorPct > *thresholds.MaxErrorRatePct
}

func shouldStopForGoalStatus(live *LiveMetrics) bool {
	return live.GoalStatus != nil && live.GoalStatus.StopTriggered
}

func (tr *TestRunner) publishLiveMetrics(test *ManagedTest, live *LiveMetrics, archiveWriter *runArchiveWriter) {
	tr.mu.Lock()
	test.LiveMetrics = live
	test.History = append(test.History, live)
	tr.mu.Unlock()

	tr.saveLiveMetricsHistory(test.ID, live.Timestamp, live)
	if archiveWriter != nil {
		if err := archiveWriter.RecordIntervalSummary(live); err != nil {
			slog.Warn("failed to record interval summary", "test_id", test.ID, "error", err)
		}
		if err := archiveWriter.RecordResourceSample(live.Timestamp, live.ActiveVUsers, live.Resources); err != nil {
			slog.Warn("failed to record resource sample", "test_id", test.ID, "error", err)
		}
	}

	tr.hub.BroadcastJSON(map[string]any{
		"type":      "metrics_update",
		"test_id":   test.ID,
		"test_name": test.Name,
		"timestamp": live.Timestamp,
		"data":      live,
	})
}

func applyGoalControllerUpdate(test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int, update goalControllerUpdate, scaleVUsers func(groupName string, target int) error) {
	live.GoalStatus = update.Status
	if !update.ShouldScale {
		return
	}

	targets := distributeTargetVUsers(test.Groups, groupVUsers, update.TargetTotal)
	for _, group := range test.Groups {
		target, exists := targets[group.Name]
		if !exists || groupVUsers[group.Name] == target {
			continue
		}
		if err := scaleVUsers(group.Name, target); err != nil {
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

func (tr *TestRunner) requestStopIfRunning(test *ManagedTest, warningMessage string) {
	tr.mu.RLock()
	status := test.Status
	tr.mu.RUnlock()
	if status != "RUNNING" {
		return
	}
	if err := tr.StopTest(test.ID); err != nil {
		slog.Warn(warningMessage, "test_id", test.ID, "error", err)
	}
}

func (tr *TestRunner) startManagedTestRun(test *ManagedTest, mge *engine.MultiGroupEngine, archiveWriter *runArchiveWriter) (context.Context, chan struct{}, chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	test.cancel = cancel
	test.multiEngine = mge

	now := time.Now()
	test.StartedAt = &now
	test.Status = "RUNNING"
	tr.broadcastEvent("test_started", test)

	metricsDone := make(chan struct{})
	metricsStopped := make(chan struct{})
	test.goalControl = &goalControllerState{}
	go func() {
		tr.broadcastMetrics(test, mge, archiveWriter, metricsDone)
		close(metricsStopped)
	}()

	return ctx, metricsDone, metricsStopped
}

func (tr *TestRunner) failTestBeforeRun(test *ManagedTest, message string) {
	now := time.Now()
	tr.mu.Lock()
	test.Status = "FAILED"
	test.Error = message
	test.EndedAt = &now
	tr.mu.Unlock()
	tr.updateStoredTestStatus(test.ID, "FAILED", &now, message)
	tr.broadcastEvent("test_failed", test)
}

func (tr *TestRunner) finalizeRunArchive(archiveWriter *runArchiveWriter, finalStatus string, endTime time.Time, finalError string) (*storage.RunArchiveRecord, string, string) {
	archiveRecord, archiveErr := archiveWriter.Finalize(finalStatus, endTime, finalError)
	if archiveErr != nil {
		return archiveRecord, "FAILED", appendRunError(finalError, "archive finalize failed", archiveErr)
	}
	if err := tr.saveRunArchiveRecord(archiveRecord); err != nil {
		return archiveRecord, "FAILED", appendRunError(finalError, "archive metadata save failed", err)
	}
	return archiveRecord, finalStatus, finalError
}

func (tr *TestRunner) completeTestRun(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) {
	tr.mu.Lock()
	test.Status = finalStatus
	test.Error = finalError
	test.EndedAt = &endTime
	test.Archive = archiveRecord
	if archiveRecord != nil {
		tr.archives[test.ID] = archiveRecord
	}
	tr.mu.Unlock()
	tr.updateStoredTestStatus(test.ID, finalStatus, &endTime, finalError)
	if finalStatus == "FAILED" {
		tr.broadcastEvent("test_failed", test)
		return
	}
	tr.broadcastEvent("test_complete", test)
}

func appendRunError(existing string, prefix string, err error) string {
	if err == nil {
		return existing
	}
	message := prefix + ": " + err.Error()
	if existing == "" {
		return message
	}
	return existing + "; " + message
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

func (tr *TestRunner) broadcastMetrics(test *ManagedTest, mge *engine.MultiGroupEngine, archiveWriter *runArchiveWriter, done chan struct{}) {
	groupVUsers := make(map[string]int)
	for _, g := range test.Groups {
		groupVUsers[g.Name] = g.VUsers
	}

	for {
		interval := tr.GetSystemSettings().General.MetricsIntervalSeconds
		if interval < 1 {
			interval = 1
		}

		select {
		case <-done:
			return
		case <-time.After(time.Duration(interval) * time.Second):
			snap, ok := mge.LatestSnapshot()
			if !ok {
				continue
			}

			live := buildLiveMetricsFromSnapshot(snap, groupVUsers)

			if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
				currentTotalVUsers := totalGroupVUsers(groupVUsers)
				update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
				applyGoalControllerUpdate(test, live, groupVUsers, update, mge.ScaleVUsers)
			}

			tr.publishLiveMetrics(test, live, archiveWriter)

			settings := tr.GetSystemSettings()
			for _, alert := range buildThresholdAlerts(test, live, settings.Thresholds) {
				tr.hub.BroadcastJSON(alert)
			}

			if shouldStopForErrorThreshold(live, settings.Thresholds) {
				tr.requestStopIfRunning(test, "threshold stop failed")
			}

			if shouldStopForGoalStatus(live) {
				tr.requestStopIfRunning(test, "goal-oriented stop failed")
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
