package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
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
	ListScripts(projectID string, folderID *string) ([]*dsl.Script, error)
	MoveScript(id string, folderID *string) error
}

type testPersistence interface {
	SaveTest(test *storage.TestRecord) error
	ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error)
	ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*storage.TestRecord, error)
	UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error
	DeleteTest(id string) error
	MoveTest(id string, folderID *string) error
}

type scenarioPersistence interface {
	SaveScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) error
	GetScenario(id string) (*storage.Scenario, []storage.ScenarioScript, error)
	ListScenarios() ([]*storage.Scenario, error)
	DeleteScenario(id string) error
	SaveScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) error
	ListScenarioDataPoolBindings(scenarioID string) ([]storage.ScenarioDataPoolBinding, error)
}

type dataPoolPersistence interface {
	ListDataPoolRows(poolID string) ([]*storage.SharedDataPoolRow, error)
}

type releasePersistence interface {
	GetRelease(id string) (*storage.Release, error)
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
	tests         map[string]*ManagedTest
	scripts       map[string]*StoredScript
	scenarios     map[string]*StoredScenario
	archives      map[string]*storage.RunArchiveRecord
	mu            sync.RWMutex
	hub           *Hub
	maxTests      int
	settings      SystemSettings
	scriptStore   scriptPersistence
	testStore     testPersistence
	scenarioStore scenarioPersistence
	dataPoolStore dataPoolPersistence
	releaseStore  releasePersistence
	historyStore  historyPersistence
	archiveStore  archivePersistence
	settingsStore settingsPersistence
}

type ManagedTest struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	Status           string                    `json:"status"`
	ApplicationID    string                    `json:"application_id,omitempty"`
	ReleaseID        string                    `json:"release_id,omitempty"`
	ScenarioID       string                    `json:"scenario_id,omitempty"`
	ProjectID        string                    `json:"project_id,omitempty"`
	FolderID         *string                   `json:"folder_id,omitempty"`
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
	Latency       *NetworkLatency              `json:"latency,omitempty"`
}

type NetworkLatency struct {
	Targets []LatencyTarget `json:"targets"`
}

type LatencyTarget struct {
	Name        string    `json:"name"`
	Endpoint    string    `json:"endpoint"`
	LatencyMs   float64   `json:"latency_ms"`
	IP          string    `json:"ip,omitempty"`
	Hops        int       `json:"hops,omitempty"`
	LastSuccess bool      `json:"last_success"`
	Timestamp   time.Time `json:"timestamp"`
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
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	Protocol            string       `json:"protocol"`
	Version             string       `json:"version"`
	ProjectID           string       `json:"project_id,omitempty"`
	FolderID            *string      `json:"folder_id,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
	ScenarioCount       int          `json:"scenario_count,omitempty"`
	AssociatedReleases  []ReleaseRef `json:"associated_releases,omitempty"`
	ScenarioRunReleases []ReleaseRef `json:"scenario_run_releases,omitempty"`
	LastRunAt           *time.Time   `json:"last_run_at,omitempty"`
	Script              *dsl.Script  `json:"script"`
}

type StoredScenario struct {
	ID                 string                            `json:"id"`
	Name               string                            `json:"name"`
	Duration           string                            `json:"duration"`
	ScenarioMode       string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout     bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline   bool                              `json:"resource_baseline,omitempty"`
	GoalConfig         *GoalConfig                       `json:"goal_config,omitempty"`
	RuntimeSettings    *RuntimeSettingsOverride          `json:"runtime_settings,omitempty"`
	Scripts            []ScenarioScriptRef               `json:"scripts"`
	DataPools          []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	CreatedAt          time.Time                         `json:"created_at"`
	UpdatedAt          time.Time                         `json:"updated_at"`
	ScriptCount        int                               `json:"script_count"`
	ResultCount        int                               `json:"result_count,omitempty"`
	AssociatedReleases []ReleaseRef                      `json:"associated_releases,omitempty"`
	LastRunAt          *time.Time                        `json:"last_run_at,omitempty"`
}

type ScenarioScriptRef struct {
	Order          int    `json:"order"`
	Name           string `json:"name,omitempty"`
	ScriptID       string `json:"script_id"`
	ScriptName     string `json:"script_name,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
	VUsers         int    `json:"vusers"`
	RampUp         string `json:"ramp_up,omitempty"`
	RampDown       string `json:"ramp_down,omitempty"`
	Initialization string `json:"initialization,omitempty"`
}

type ReleaseRef struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ApplicationID string `json:"application_id"`
}

type archiveRecorder interface {
	engine.ResultSink
	RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error
	RecordIntervalSummary(live *LiveMetrics) error
	Finalize(status string, completedAt time.Time, errStr string) (*storage.RunArchiveRecord, error)
}

type runArtifacts struct {
	recorder   archiveRecorder
	saveRecord func(record *storage.RunArchiveRecord) error
}

func NewTestRunner(maxTests int, hub *Hub, store storage.Store) *TestRunner {
	tr := &TestRunner{
		tests:         make(map[string]*ManagedTest),
		scripts:       make(map[string]*StoredScript),
		scenarios:     make(map[string]*StoredScenario),
		archives:      make(map[string]*storage.RunArchiveRecord),
		hub:           hub,
		maxTests:      maxTests,
		scriptStore:   store,
		testStore:     store,
		scenarioStore: store,
		dataPoolStore: store,
		releaseStore:  store,
		historyStore:  store,
		archiveStore:  store,
		settingsStore: store,
	}
	tr.loadSystemSettings(maxTests)
	tr.loadStoredScripts()
	tr.loadStoredScenarios()
	tr.loadStoredTests()

	return tr
}

func (tr *TestRunner) loadStoredScripts() {
	if tr.scriptStore == nil {
		return
	}
	scripts, err := tr.scriptStore.ListScripts("", nil)
	if err != nil {
		return
	}
	for _, script := range scripts {
		tr.scripts[script.ID] = &StoredScript{
			ID:        script.ID,
			Name:      script.Name,
			Protocol:  script.Protocol,
			Version:   script.Version,
			ProjectID: script.ProjectID,
			FolderID:  script.FolderID,
			CreatedAt: script.CreatedAt,
			Script:    script,
		}
	}
}

func (tr *TestRunner) loadStoredScenarios() {
	if tr.scenarioStore == nil {
		return
	}
	scenarios, err := tr.scenarioStore.ListScenarios()
	if err != nil {
		return
	}
	for _, scenario := range scenarios {
		full, scripts, err := tr.scenarioStore.GetScenario(scenario.ID)
		if err != nil {
			full = scenario
		}
		tr.scenarios[scenario.ID] = tr.buildStoredScenario(full, scripts)
	}
}

func (tr *TestRunner) buildStoredScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) *StoredScenario {
	if scenario == nil {
		return nil
	}
	refs := make([]ScenarioScriptRef, 0, len(scripts))
	for _, script := range scripts {
		ref := ScenarioScriptRef{
			Order:          script.Order,
			Name:           script.Name,
			ScriptID:       script.ScriptID,
			VUsers:         script.VUsers,
			RampUp:         script.RampUp,
			RampDown:       script.RampDown,
			Initialization: script.Initialization,
		}
		if stored, ok := tr.scripts[script.ScriptID]; ok {
			ref.ScriptName = stored.Name
			ref.Protocol = stored.Protocol
			if ref.Name == "" {
				ref.Name = stored.Name
			}
		}
		refs = append(refs, ref)
	}
	dataPools := []storage.ScenarioDataPoolBinding{}
	if tr.scenarioStore != nil {
		if bindings, err := tr.scenarioStore.ListScenarioDataPoolBindings(scenario.ID); err == nil {
			dataPools = bindings
		}
	}
	return &StoredScenario{
		ID:               scenario.ID,
		Name:             scenario.Name,
		Duration:         scenario.Duration,
		ScenarioMode:     normalizeScenarioMode(scenario.ScenarioMode),
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		GoalConfig:       decodeStoredGoalConfig(scenario.GoalConfig),
		RuntimeSettings:  decodeStoredRuntimeSettings(scenario.RuntimeSettings),
		Scripts:          refs,
		DataPools:        dataPools,
		CreatedAt:        scenario.CreatedAt,
		UpdatedAt:        scenario.UpdatedAt,
		ScriptCount:      len(refs),
	}
}

func (tr *TestRunner) loadStoredTests() {
	if tr.testStore == nil {
		return
	}
	tests, err := tr.testStore.ListTests("", nil)
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
		ApplicationID:    record.ApplicationID,
		ReleaseID:        record.ReleaseID,
		ScenarioID:       record.ScenarioID,
		ProjectID:        record.ProjectID,
		FolderID:         record.FolderID,
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
	script.ProjectID = normalizeProjectID(script.ProjectID)

	stored := &StoredScript{
		ID:        script.ID,
		Name:      script.Name,
		Protocol:  script.Protocol,
		Version:   script.Version,
		ProjectID: script.ProjectID,
		FolderID:  script.FolderID,
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

func (tr *TestRunner) ReplayScript(id string, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	tr.mu.RLock()
	stored, ok := tr.scripts[id]
	tr.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("script not found")
	}
	return tr.ReplayScriptDefinition(stored.Script, opts)
}

func (tr *TestRunner) ReplayScriptDefinition(script *dsl.Script, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	return engine.ReplayScript(context.Background(), script, opts)
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

func (tr *TestRunner) ListScriptsFiltered(projectID string, folderID *string) []*StoredScript {
	projectID = normalizeProjectID(projectID)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		if s.ProjectID != projectID {
			continue
		}
		if folderID != nil {
			if *folderID == "" {
				if s.FolderID != nil && *s.FolderID != "" {
					continue
				}
			} else if s.FolderID == nil || *s.FolderID != *folderID {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func (tr *TestRunner) MoveScript(id string, folderID *string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	script, ok := tr.scripts[id]
	if !ok {
		return false
	}
	script.FolderID = folderID
	if script.Script != nil {
		script.Script.FolderID = folderID
	}
	if tr.scriptStore != nil {
		_ = tr.scriptStore.MoveScript(id, folderID)
	}
	return true
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
	if script.ProjectID == "" {
		script.ProjectID = existing.ProjectID
	}
	if script.FolderID == nil {
		script.FolderID = existing.FolderID
	}
	existing.ProjectID = script.ProjectID
	existing.FolderID = script.FolderID
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

func (tr *TestRunner) StoreScenario(req CreateScenarioRequest) (*StoredScenario, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.storeScenarioLocked(uuid.New().String(), req)
}

func (tr *TestRunner) UpdateScenario(id string, req CreateScenarioRequest) (*StoredScenario, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if _, ok := tr.scenarios[id]; !ok {
		return nil, false, nil
	}
	scenario, err := tr.storeScenarioLocked(id, req)
	return scenario, true, err
}

func (tr *TestRunner) storeScenarioLocked(id string, req CreateScenarioRequest) (*StoredScenario, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("scenario name is required")
	}
	if strings.TrimSpace(req.Duration) == "" {
		return nil, fmt.Errorf("scenario duration is required")
	}
	if _, err := validateScenarioRequest(CreateTestRequest{
		ScenarioMode: req.ScenarioMode,
		GoalConfig:   req.GoalConfig,
	}); err != nil {
		return nil, err
	}
	groups, err := normalizeCreateTestGroups(CreateTestRequest{Groups: req.Groups})
	if err != nil {
		return nil, err
	}
	if _, _, _, err := tr.compileManagedTestGroupsLocked(groups, req.RuntimeSettings); err != nil {
		return nil, err
	}
	now := time.Now()
	existing := tr.scenarios[id]
	createdAt := now
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	scenario := &storage.Scenario{
		ID:               id,
		Name:             req.Name,
		Duration:         req.Duration,
		ScenarioMode:     normalizeScenarioMode(req.ScenarioMode),
		ScriptShakeout:   req.ScriptShakeout,
		ResourceBaseline: req.ResourceBaseline,
		GoalConfig:       req.GoalConfig,
		RuntimeSettings:  req.RuntimeSettings,
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	scripts := make([]storage.ScenarioScript, 0, len(groups))
	for i, group := range groups {
		scripts = append(scripts, storage.ScenarioScript{
			ScenarioID:     id,
			Order:          i,
			Name:           group.Name,
			ScriptID:       group.ScriptID,
			VUsers:         group.VUsers,
			RampUp:         group.RampUp,
			RampDown:       group.RampDown,
			Initialization: group.InitStrategy,
		})
	}
	dataPools, err := normalizeScenarioDataPoolBindings(id, req.DataPools)
	if err != nil {
		return nil, err
	}
	if tr.scenarioStore != nil {
		if err := tr.scenarioStore.SaveScenario(scenario, scripts); err != nil {
			return nil, err
		}
		if err := tr.scenarioStore.SaveScenarioDataPoolBindings(id, dataPools); err != nil {
			return nil, err
		}
	}
	stored := tr.buildStoredScenario(scenario, scripts)
	stored.DataPools = dataPools
	tr.scenarios[id] = stored
	return stored, nil
}

func normalizeScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) ([]storage.ScenarioDataPoolBinding, error) {
	out := make([]storage.ScenarioDataPoolBinding, 0, len(bindings))
	seenAliases := map[string]struct{}{}
	now := time.Now()
	for _, binding := range bindings {
		binding.ScenarioID = scenarioID
		binding.PoolID = strings.TrimSpace(binding.PoolID)
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.AccessMode = strings.TrimSpace(binding.AccessMode)
		binding.SelectionStrategy = strings.TrimSpace(binding.SelectionStrategy)
		if binding.PoolID == "" {
			return nil, fmt.Errorf("data pool id is required")
		}
		if binding.Alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		aliasKey := strings.ToLower(binding.Alias)
		if _, ok := seenAliases[aliasKey]; ok {
			return nil, fmt.Errorf("duplicate data pool binding alias %q", binding.Alias)
		}
		seenAliases[aliasKey] = struct{}{}
		if binding.ID == "" {
			binding.ID = uuid.New().String()
		}
		if binding.AccessMode == "" {
			binding.AccessMode = "read_only"
		}
		switch binding.AccessMode {
		case "read_only", "read_write", "consume":
		default:
			return nil, fmt.Errorf("unsupported data pool access mode %q", binding.AccessMode)
		}
		if binding.SelectionStrategy == "" {
			binding.SelectionStrategy = "sequential"
		}
		switch binding.SelectionStrategy {
		case "sequential", "random", "unique":
		default:
			return nil, fmt.Errorf("unsupported data pool selection strategy %q", binding.SelectionStrategy)
		}
		if binding.CreatedAt.IsZero() {
			binding.CreatedAt = now
		}
		binding.UpdatedAt = now
		out = append(out, binding)
	}
	return out, nil
}

func (tr *TestRunner) GetScenario(id string) (*StoredScenario, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	scenario, ok := tr.scenarios[id]
	if !ok {
		return nil, false
	}
	return tr.enrichScenarioSummaryLocked(scenario), true
}

func (tr *TestRunner) ListScenarios() []*StoredScenario {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		out = append(out, tr.enrichScenarioSummaryLocked(scenario))
	}
	return out
}

func (tr *TestRunner) DeleteScenario(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if _, ok := tr.scenarios[id]; !ok {
		return false
	}
	for _, test := range tr.tests {
		if test.ScenarioID == id {
			return false
		}
	}
	delete(tr.scenarios, id)
	if tr.scenarioStore != nil {
		_ = tr.scenarioStore.DeleteScenario(id)
	}
	return true
}

func (tr *TestRunner) ListScriptsWithUsage() []*StoredScript {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, script := range tr.scripts {
		out = append(out, tr.enrichScriptSummaryLocked(script))
	}
	return out
}

func (tr *TestRunner) enrichScriptSummaryLocked(script *StoredScript) *StoredScript {
	if script == nil {
		return nil
	}
	copy := *script
	scenarioSet := map[string]bool{}
	releaseSet := map[string]ReleaseRef{}
	scenarioRunReleaseSet := map[string]ReleaseRef{}
	var lastRun *time.Time
	for _, scenario := range tr.scenarios {
		for _, scenarioScript := range scenario.Scripts {
			if scenarioScript.ScriptID == script.ID {
				scenarioSet[scenario.ID] = true
			}
		}
	}
	for _, test := range tr.tests {
		usesScript := false
		for _, group := range test.Groups {
			if group.ScriptID == script.ID {
				usesScript = true
				break
			}
		}
		if !usesScript {
			continue
		}
		if test.ReleaseID != "" {
			releaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
			if test.ScenarioID != "" {
				scenarioRunReleaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
			}
		}
		if lastRun == nil || test.CreatedAt.After(*lastRun) {
			t := test.CreatedAt
			lastRun = &t
		}
	}
	copy.ScenarioCount = len(scenarioSet)
	copy.AssociatedReleases = releaseRefsFromSet(releaseSet)
	copy.ScenarioRunReleases = releaseRefsFromSet(scenarioRunReleaseSet)
	copy.LastRunAt = lastRun
	return &copy
}

func (tr *TestRunner) enrichScenarioSummaryLocked(scenario *StoredScenario) *StoredScenario {
	if scenario == nil {
		return nil
	}
	copy := *scenario
	copy.Scripts = append([]ScenarioScriptRef(nil), scenario.Scripts...)
	releaseSet := map[string]ReleaseRef{}
	var lastRun *time.Time
	resultCount := 0
	for _, test := range tr.tests {
		if test.ScenarioID != scenario.ID {
			continue
		}
		if test.Status == "COMPLETE" || test.Status == "FAILED" || test.Status == "ABORTED" {
			resultCount++
		}
		if test.ReleaseID != "" {
			releaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
		}
		if lastRun == nil || test.CreatedAt.After(*lastRun) {
			t := test.CreatedAt
			lastRun = &t
		}
	}
	copy.ResultCount = resultCount
	copy.AssociatedReleases = releaseRefsFromSet(releaseSet)
	copy.LastRunAt = lastRun
	return &copy
}

func (tr *TestRunner) releaseRefLocked(id string) ReleaseRef {
	ref := ReleaseRef{ID: id, Name: id}
	if tr.releaseStore == nil {
		return ref
	}
	release, err := tr.releaseStore.GetRelease(id)
	if err != nil || release == nil {
		return ref
	}
	return ReleaseRef{ID: release.ID, Name: release.Name, ApplicationID: release.ApplicationID}
}

func releaseRefsFromSet(set map[string]ReleaseRef) []ReleaseRef {
	out := make([]ReleaseRef, 0, len(set))
	for _, release := range set {
		out = append(out, release)
	}
	return out
}

type CreateTestRequest struct {
	Name             string                            `json:"name"`
	Duration         string                            `json:"duration"`
	ApplicationID    string                            `json:"application_id,omitempty"`
	ReleaseID        string                            `json:"release_id,omitempty"`
	ScenarioID       string                            `json:"scenario_id,omitempty"`
	ProjectID        string                            `json:"project_id,omitempty"`
	FolderID         *string                           `json:"folder_id,omitempty"`
	ScenarioMode     string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                              `json:"resource_baseline,omitempty"`
	GoalConfig       *GoalConfig                       `json:"goal_config,omitempty"`
	Groups           []CreateGroupEntry                `json:"groups,omitempty"`
	DataPools        []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	// Single-script shorthand (backwards compatible)
	ScriptID string `json:"script_id,omitempty"`
	VUsers   int    `json:"vusers,omitempty"`
	RampUp   string `json:"ramp_up,omitempty"`
	// Test-level runtime overrides (mirrors LR Controller override behavior)
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
}

type CreateScenarioRequest struct {
	Name             string                            `json:"name"`
	Duration         string                            `json:"duration"`
	ScenarioMode     string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                              `json:"resource_baseline,omitempty"`
	GoalConfig       *GoalConfig                       `json:"goal_config,omitempty"`
	RuntimeSettings  *RuntimeSettingsOverride          `json:"runtime_settings,omitempty"`
	Groups           []CreateGroupEntry                `json:"groups"`
	DataPools        []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
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
	if req.ScenarioID != "" {
		scenario, ok := tr.scenarios[req.ScenarioID]
		if !ok {
			return nil, fmt.Errorf("scenario not found: %s", req.ScenarioID)
		}
		req = createTestRequestFromScenario(req, scenario)
	}

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

	sharedDataSources, err := tr.buildSharedDataSourcesLocked(req.DataPools)
	if err != nil {
		return nil, err
	}

	testGroups, engineGroups, totalVUsers, err := tr.compileManagedTestGroupsWithDataSourcesLocked(groups, req.RuntimeSettings, sharedDataSources)
	if err != nil {
		return nil, err
	}

	testName := deriveManagedTestName(req.Name, testGroups)

	if req.ReleaseID == "" && status != "DRAFT" {
		req.ReleaseID = storage.LegacyReleaseID
	}
	applicationID := req.ApplicationID
	if req.ReleaseID != "" && tr.releaseStore != nil {
		if release, err := tr.releaseStore.GetRelease(req.ReleaseID); err == nil && release != nil {
			applicationID = release.ApplicationID
		}
	}
	if applicationID == "" && req.ReleaseID != "" {
		applicationID = storage.LegacyApplicationID
	}
	projectID := normalizeProjectID(req.ProjectID)

	test := &ManagedTest{
		ID:               id,
		Name:             testName,
		Status:           status,
		ApplicationID:    applicationID,
		ReleaseID:        req.ReleaseID,
		ScenarioID:       req.ScenarioID,
		ProjectID:        projectID,
		FolderID:         req.FolderID,
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

func createTestRequestFromScenario(req CreateTestRequest, scenario *StoredScenario) CreateTestRequest {
	req.Name = firstNonEmpty(req.Name, scenario.Name)
	req.Duration = firstNonEmpty(req.Duration, scenario.Duration)
	req.ScenarioMode = firstNonEmpty(req.ScenarioMode, scenario.ScenarioMode)
	if !req.ScriptShakeout {
		req.ScriptShakeout = scenario.ScriptShakeout
	}
	if !req.ResourceBaseline {
		req.ResourceBaseline = scenario.ResourceBaseline
	}
	if req.GoalConfig == nil {
		req.GoalConfig = cloneGoalConfig(scenario.GoalConfig)
	}
	if req.RuntimeSettings == nil {
		req.RuntimeSettings = cloneRuntimeSettings(scenario.RuntimeSettings)
	}
	if len(req.DataPools) == 0 {
		req.DataPools = append([]storage.ScenarioDataPoolBinding(nil), scenario.DataPools...)
	}
	if len(req.Groups) == 0 && req.ScriptID == "" {
		req.Groups = make([]CreateGroupEntry, 0, len(scenario.Scripts))
		for _, script := range scenario.Scripts {
			req.Groups = append(req.Groups, CreateGroupEntry{
				Name:         script.Name,
				ScriptID:     script.ScriptID,
				VUsers:       script.VUsers,
				RampUp:       script.RampUp,
				RampDown:     script.RampDown,
				InitStrategy: script.Initialization,
			})
		}
	}
	return req
}

func firstNonEmpty(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
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
	return tr.compileManagedTestGroupsWithDataSourcesLocked(groups, runtimeSettings, nil)
}

func (tr *TestRunner) compileManagedTestGroupsWithDataSourcesLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) ([]TestGroup, []engine.GroupConfig, int, error) {
	testGroups := make([]TestGroup, 0, len(groups))
	engineGroups := make([]engine.GroupConfig, 0, len(groups))
	totalVUsers := 0

	for i, group := range groups {
		testGroup, engineGroup, err := tr.compileManagedTestGroupLocked(i, len(groups), group, runtimeSettings, sharedDataSources)
		if err != nil {
			return nil, nil, 0, err
		}
		testGroups = append(testGroups, testGroup)
		engineGroups = append(engineGroups, engineGroup)
		totalVUsers += testGroup.VUsers
	}

	return testGroups, engineGroups, totalVUsers, nil
}

func (tr *TestRunner) compileManagedTestGroupLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) (TestGroup, engine.GroupConfig, error) {
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
	if len(sharedDataSources) > 0 {
		scriptCopy.DataSources = append(cloneDataSources(scriptCopy.DataSources), sharedDataSources...)
	}
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

func (tr *TestRunner) buildSharedDataSourcesLocked(bindings []storage.ScenarioDataPoolBinding) ([]dsl.DataSource, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if tr.dataPoolStore == nil {
		return nil, fmt.Errorf("data pool storage is not configured")
	}
	sources := make([]dsl.DataSource, 0, len(bindings))
	for _, binding := range bindings {
		alias := strings.TrimSpace(binding.Alias)
		if alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		rows, err := tr.dataPoolStore.ListDataPoolRows(binding.PoolID)
		if err != nil {
			return nil, fmt.Errorf("load data pool %q: %w", alias, err)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("data pool %q has no rows", alias)
		}
		inlineData, columns, err := inlineCSVFromDataPoolRows(rows)
		if err != nil {
			return nil, fmt.Errorf("prepare data pool %q: %w", alias, err)
		}
		strategy := binding.SelectionStrategy
		if strategy == "" {
			strategy = "sequential"
		}
		sources = append(sources, dsl.DataSource{
			Name:           alias,
			Type:           "csv",
			InlineData:     inlineData,
			Strategy:       strategy,
			FirstRowHeader: true,
			Columns:        columns,
		})
	}
	return sources, nil
}

func inlineCSVFromDataPoolRows(rows []*storage.SharedDataPoolRow) (string, []string, error) {
	columnSet := map[string]struct{}{}
	for _, row := range rows {
		for key := range row.Data {
			key = strings.TrimSpace(key)
			if key != "" {
				columnSet[key] = struct{}{}
			}
		}
	}
	if len(columnSet) == 0 {
		return "", nil, fmt.Errorf("data pool rows must include at least one column")
	}
	columns := make([]string, 0, len(columnSet))
	for key := range columnSet {
		columns = append(columns, key)
	}
	sort.Strings(columns)

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(columns); err != nil {
		return "", nil, err
	}
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, column := range columns {
			values[i] = row.Data[column]
		}
		if err := writer.Write(values); err != nil {
			return "", nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", nil, err
	}
	return buf.String(), columns, nil
}

func cloneDataSources(sources []dsl.DataSource) []dsl.DataSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]dsl.DataSource, len(sources))
	copy(out, sources)
	return out
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

func (tr *TestRunner) ListTestsFiltered(projectID string, folderID *string) []*ManagedTest {
	projectID = normalizeProjectID(projectID)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t.ProjectID != projectID {
			continue
		}
		if folderID != nil {
			if *folderID == "" {
				if t.FolderID != nil && *t.FolderID != "" {
					continue
				}
			} else if t.FolderID == nil || *t.FolderID != *folderID {
				continue
			}
		}
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) ListTestsByContext(applicationID string, releaseID string, scenarioID string) []*ManagedTest {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if applicationID != "" && t.ApplicationID != applicationID {
			continue
		}
		if releaseID != "" && t.ReleaseID != releaseID {
			continue
		}
		if scenarioID != "" && t.ScenarioID != scenarioID {
			continue
		}
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) MoveTest(id string, folderID *string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok {
		return false
	}
	test.FolderID = folderID
	if tr.testStore != nil {
		_ = tr.testStore.MoveTest(id, folderID)
	}
	return true
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
		ApplicationID:    test.ApplicationID,
		ReleaseID:        test.ReleaseID,
		ScenarioID:       test.ScenarioID,
		ProjectID:        normalizeProjectID(test.ProjectID),
		FolderID:         test.FolderID,
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

func normalizeProjectID(projectID string) string {
	if projectID == "" {
		return storage.DefaultProjectID
	}
	return projectID
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

func (tr *TestRunner) PauseTest(id string) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to pause", test.Status)
	}
	if test.multiEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	if err := test.multiEngine.Pause(); err != nil {
		return err
	}

	tr.mu.Lock()
	test.Status = "PAUSED"
	tr.mu.Unlock()

	tr.broadcastEvent("test_paused", test)

	return nil
}

func (tr *TestRunner) ResumeTest(id string) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "PAUSED" {
		return fmt.Errorf("test is %s, must be PAUSED to resume", test.Status)
	}
	if test.multiEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	if err := test.multiEngine.Resume(); err != nil {
		return err
	}

	tr.mu.Lock()
	test.Status = "RUNNING"
	tr.mu.Unlock()

	tr.broadcastEvent("test_resumed", test)

	return nil
}

func (tr *TestRunner) runTest(test *ManagedTest) {
	if test.ScriptShakeout {
		if err := tr.runScriptShakeout(test); err != nil {
			tr.failTestBeforeRun(test, err.Error())
			return
		}
	}
	artifacts, err := newRunArtifacts(test, tr.saveRunArchiveRecord)
	if err != nil {
		tr.failTestBeforeRun(test, fmt.Sprintf("initialize archive writer: %v", err))
		return
	}
	mge, err := tr.buildRunEngine(test, artifacts.resultSink())
	if err != nil {
		tr.failTestBeforeRun(test, err.Error())
		return
	}

	ctx, metricsDone, metricsStopped := tr.startManagedTestRun(test, mge, artifacts)

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

	archiveRecord, finalStatus, finalError := artifacts.finalize(finalStatus, endTime, finalError)
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

func newRunArtifacts(test *ManagedTest, saveRecord func(record *storage.RunArchiveRecord) error) (*runArtifacts, error) {
	recorder, err := newRunArchiveWriter(test)
	if err != nil {
		return nil, err
	}
	return &runArtifacts{recorder: recorder, saveRecord: saveRecord}, nil
}

func (a *runArtifacts) resultSink() engine.ResultSink {
	if a == nil {
		return nil
	}
	return a.recorder
}

func (a *runArtifacts) recordLiveMetrics(test *ManagedTest, live *LiveMetrics) {
	if a == nil || a.recorder == nil {
		return
	}
	if err := a.recorder.RecordIntervalSummary(live); err != nil {
		slog.Warn("failed to record interval summary", "test_id", test.ID, "error", err)
	}
	if err := a.recorder.RecordResourceSample(live.Timestamp, live.ActiveVUsers, live.Resources); err != nil {
		slog.Warn("failed to record resource sample", "test_id", test.ID, "error", err)
	}
}

func (a *runArtifacts) finalize(finalStatus string, endTime time.Time, finalError string) (*storage.RunArchiveRecord, string, string) {
	if a == nil || a.recorder == nil {
		return nil, finalStatus, finalError
	}
	archiveRecord, archiveErr := a.recorder.Finalize(finalStatus, endTime, finalError)
	if archiveErr != nil {
		return archiveRecord, "FAILED", appendRunError(finalError, "archive finalize failed", archiveErr)
	}
	if a.saveRecord != nil {
		if err := a.saveRecord(archiveRecord); err != nil {
			return archiveRecord, "FAILED", appendRunError(finalError, "archive metadata save failed", err)
		}
	}
	return archiveRecord, finalStatus, finalError
}

func (tr *TestRunner) buildRunEngine(test *ManagedTest, resultSink engine.ResultSink) (*engine.MultiGroupEngine, error) {
	preBaseline, postBaseline := resourceBaselineWindows(test.ResourceBaseline)
	return engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:       cloneGroupSpecsWithResultSink(test.groupSpecs, resultSink),
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

func buildLiveMetricsForBroadcast(snap engine.MetricsSnapshot, groupVUsers map[string]int, now time.Time) *LiveMetrics {
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)
	live.Timestamp = now
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

func (tr *TestRunner) publishLiveMetrics(test *ManagedTest, live *LiveMetrics, artifacts *runArtifacts) {
	tr.mu.Lock()
	test.LiveMetrics = live
	test.History = append(test.History, live)
	tr.mu.Unlock()

	tr.saveLiveMetricsHistory(test.ID, live.Timestamp, live)
	artifacts.recordLiveMetrics(test, live)

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

func (tr *TestRunner) startManagedTestRun(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts) (context.Context, chan struct{}, chan struct{}) {
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
		tr.broadcastMetrics(test, mge, artifacts, metricsDone)
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

func (tr *TestRunner) broadcastMetrics(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts, done chan struct{}) {
	groupVUsers := make(map[string]int)
	for _, g := range test.Groups {
		groupVUsers[g.Name] = g.VUsers
	}

	intervalSec := tr.GetSystemSettings().General.MetricsIntervalSeconds
	if intervalSec < 1 {
		intervalSec = 1
	}
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	lastLatencyCheck := time.Time{}

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			snap, ok := mge.LatestSnapshot()
			if !ok {
				continue
			}

			live := buildLiveMetricsForBroadcast(snap, groupVUsers, time.Now())

			if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
				currentTotalVUsers := totalGroupVUsers(groupVUsers)
				update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
				applyGoalControllerUpdate(test, live, groupVUsers, update, mge.ScaleVUsers)
			}

			// Throttle network latency pings to every 5 seconds
			if time.Since(lastLatencyCheck) >= 5*time.Second {
				go tr.measureNetworkLatency(test, live)
				lastLatencyCheck = time.Now()
			}

			tr.publishLiveMetrics(test, live, artifacts)
		}
	}
}

func (tr *TestRunner) processMetricsSnapshot(test *ManagedTest, snap engine.MetricsSnapshot, groupVUsers map[string]int, scaleVUsers func(groupName string, target int) error, artifacts *runArtifacts) {
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)

	if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
		currentTotalVUsers := totalGroupVUsers(groupVUsers)
		update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
		applyGoalControllerUpdate(test, live, groupVUsers, update, scaleVUsers)
	}

	tr.publishLiveMetrics(test, live, artifacts)

	// Measure network latency in background if not already measured for this interval
	go tr.measureNetworkLatency(test, live)

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

func (tr *TestRunner) measureNetworkLatency(test *ManagedTest, live *LiveMetrics) {
	// Identify unique base URLs across all groups
	endpoints := make(map[string]string)
	for _, g := range test.Groups {
		if g.script != nil && g.script.Config.BaseURL != "" {
			u, err := url.Parse(g.script.Config.BaseURL)
			if err == nil {
				endpoints[u.Host] = g.script.Config.BaseURL
			}
		}
	}

	if len(endpoints) == 0 {
		return
	}

	latency := &NetworkLatency{
		Targets: make([]LatencyTarget, 0, len(endpoints)),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for host, raw := range endpoints {
		wg.Add(1)
		go func(h, r string) {
			defer wg.Done()
			target := tr.measureEndpointLatency(h, r)
			mu.Lock()
			latency.Targets = append(latency.Targets, target)
			mu.Unlock()
		}(host, raw)
	}
	wg.Wait()

	tr.mu.Lock()
	live.Latency = latency
	tr.mu.Unlock()

	// Broadcast the latency update specifically if needed,
	// though it will be part of the next metrics broadcast too.
	tr.hub.BroadcastJSON(map[string]any{
		"type":    "latency_update",
		"test_id": test.ID,
		"latency": latency,
	})
}

func (tr *TestRunner) measureEndpointLatency(host, endpoint string) LatencyTarget {
	target := LatencyTarget{
		Name:      host,
		Endpoint:  endpoint,
		Timestamp: time.Now(),
	}

	start := time.Now()

	// We'll use a simple TCP dial to measure base R/T latency to the host.
	// This is more reliable than ICMP ping which is often blocked.
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	// Default to port 80 or 443 if not specified
	dialHost := host
	if !strings.Contains(host, ":") {
		if strings.HasPrefix(endpoint, "https") {
			dialHost += ":443"
		} else {
			dialHost += ":80"
		}
	}

	conn, err := dialer.Dial("tcp", dialHost)
	if err != nil {
		target.LastSuccess = false
		return target
	}
	defer conn.Close()

	target.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
	target.LastSuccess = true

	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		target.IP = addr.IP.String()
	}

	// Note: hop count/route info typically requires raw sockets or external tools.
	// We'll leave it as 0 for now or implement a basic traceroute later if requested.

	return target
}
