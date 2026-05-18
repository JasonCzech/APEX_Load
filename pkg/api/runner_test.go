package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type metricsStoreSpy struct {
	savedHistoryTestID    string
	savedHistoryTimestamp time.Time
	savedHistoryMetrics   any
	saveHistoryCalls      int
	savedScripts          []*dsl.Script
	deletedScriptIDs      []string
	savedTests            []*storage.TestRecord
	deletedTestIDs        []string
	listedScripts         []*dsl.Script
	listedTests           []*storage.TestRecord
	projectsByID          map[string]*storage.Project
	releasesByID          map[string]*storage.Release
	historyByTestID       map[string][]any
	archiveByTestID       map[string]*storage.RunArchiveRecord
	schedulesByID         map[string]*storage.ScenarioSchedule
	occurrencesByID       map[string]*storage.ScenarioScheduleOccurrence
	approvalsByID         map[string]*storage.ScenarioScheduleApproval
	leasesByName          map[string]*storage.SchedulerLease
	auditEvents           []*storage.AuditEvent
	usageSnapshots        []*storage.UsageAccountingSnapshot
}

var runnerTestProtocolCounter atomic.Uint64

type provenanceInventoryBackend struct{}

type scheduleExecutionBackend struct {
	started chan string
}

func (provenanceInventoryBackend) Run(_ *TestRunner, _ *ManagedTest) {}

func (provenanceInventoryBackend) SystemInventory(_ context.Context) []GeneratorInventoryEntry {
	return []GeneratorInventoryEntry{{Target: "generator-1", Status: "healthy", AvailableCapacity: 50}}
}

func (b *scheduleExecutionBackend) Run(_ *TestRunner, test *ManagedTest) {
	if b != nil && b.started != nil {
		b.started <- test.ID
	}
}

func (s *metricsStoreSpy) SaveScript(script *dsl.Script) error {
	s.savedScripts = append(s.savedScripts, script)
	return nil
}
func (s *metricsStoreSpy) GetScript(id string) (*dsl.Script, error) { return nil, nil }
func (s *metricsStoreSpy) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	return s.listedScripts, nil
}
func (s *metricsStoreSpy) DeleteScript(id string) error {
	s.deletedScriptIDs = append(s.deletedScriptIDs, id)
	return nil
}
func (s *metricsStoreSpy) MoveScript(id string, folderID *string) error { return nil }
func (s *metricsStoreSpy) SaveTest(test *storage.TestRecord) error {
	s.savedTests = append(s.savedTests, test)
	return nil
}
func (s *metricsStoreSpy) GetTest(id string) (*storage.TestRecord, error) { return nil, nil }
func (s *metricsStoreSpy) ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error) {
	return s.listedTests, nil
}
func (s *metricsStoreSpy) UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error {
	return nil
}
func (s *metricsStoreSpy) DeleteTest(id string) error {
	s.deletedTestIDs = append(s.deletedTestIDs, id)
	return nil
}
func (s *metricsStoreSpy) MoveTest(id string, folderID *string) error { return nil }
func (s *metricsStoreSpy) ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*storage.TestRecord, error) {
	return s.listedTests, nil
}
func (s *metricsStoreSpy) SaveApplication(application *storage.Application) error { return nil }
func (s *metricsStoreSpy) GetApplication(id string) (*storage.Application, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListApplications() ([]*storage.Application, error) { return nil, nil }
func (s *metricsStoreSpy) DeleteApplication(id string) error                 { return nil }
func (s *metricsStoreSpy) SaveRelease(release *storage.Release) error        { return nil }
func (s *metricsStoreSpy) GetRelease(id string) (*storage.Release, error) {
	if s.releasesByID != nil {
		release, ok := s.releasesByID[id]
		if !ok {
			return nil, errors.New("release not found")
		}
		return release, nil
	}
	return &storage.Release{ID: id, ApplicationID: storage.LegacyApplicationID, Name: id}, nil
}
func (s *metricsStoreSpy) ListReleases(applicationID string, includeArchived bool) ([]*storage.Release, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ArchiveRelease(id string) error { return nil }
func (s *metricsStoreSpy) DeleteRelease(id string) error  { return nil }
func (s *metricsStoreSpy) SaveScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) error {
	return nil
}
func (s *metricsStoreSpy) GetScenario(id string) (*storage.Scenario, []storage.ScenarioScript, error) {
	return nil, nil, nil
}
func (s *metricsStoreSpy) ListScenarios() ([]*storage.Scenario, error) { return nil, nil }
func (s *metricsStoreSpy) DeleteScenario(id string) error              { return nil }
func (s *metricsStoreSpy) SaveScenarioSchedule(schedule *storage.ScenarioSchedule) error {
	if s.schedulesByID == nil {
		s.schedulesByID = make(map[string]*storage.ScenarioSchedule)
	}
	clone := *schedule
	if schedule.LastRunAt != nil {
		lastRunAt := *schedule.LastRunAt
		clone.LastRunAt = &lastRunAt
	}
	if schedule.NextRunAt != nil {
		nextRunAt := *schedule.NextRunAt
		clone.NextRunAt = &nextRunAt
	}
	s.schedulesByID[schedule.ID] = &clone
	return nil
}
func (s *metricsStoreSpy) GetScenarioSchedule(id string) (*storage.ScenarioSchedule, error) {
	if s.schedulesByID == nil {
		return nil, nil
	}
	return s.schedulesByID[id], nil
}
func (s *metricsStoreSpy) ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, error) {
	out := []*storage.ScenarioSchedule{}
	for _, schedule := range s.schedulesByID {
		if schedule.ScenarioID == scenarioID {
			out = append(out, schedule)
		}
	}
	return out, nil
}
func (s *metricsStoreSpy) ListAllScenarioSchedules() ([]*storage.ScenarioSchedule, error) {
	out := make([]*storage.ScenarioSchedule, 0, len(s.schedulesByID))
	for _, schedule := range s.schedulesByID {
		out = append(out, schedule)
	}
	return out, nil
}
func (s *metricsStoreSpy) DeleteScenarioSchedule(id string) error {
	delete(s.schedulesByID, id)
	return nil
}
func (s *metricsStoreSpy) SaveScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence) error {
	if s.occurrencesByID == nil {
		s.occurrencesByID = make(map[string]*storage.ScenarioScheduleOccurrence)
	}
	clone := *occurrence
	if occurrence.MaterializedAt != nil {
		materializedAt := *occurrence.MaterializedAt
		clone.MaterializedAt = &materializedAt
	}
	s.occurrencesByID[occurrence.ID] = &clone
	return nil
}
func (s *metricsStoreSpy) GetScenarioScheduleOccurrence(id string) (*storage.ScenarioScheduleOccurrence, error) {
	if s.occurrencesByID == nil {
		return nil, nil
	}
	return s.occurrencesByID[id], nil
}
func (s *metricsStoreSpy) GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*storage.ScenarioScheduleOccurrence, error) {
	for _, occurrence := range s.occurrencesByID {
		if occurrence.ScheduleID == scheduleID && occurrence.PlannedAt.Equal(plannedAt) {
			return occurrence, nil
		}
	}
	return nil, nil
}
func (s *metricsStoreSpy) ListScenarioScheduleOccurrences(scheduleID string) ([]*storage.ScenarioScheduleOccurrence, error) {
	out := []*storage.ScenarioScheduleOccurrence{}
	for _, occurrence := range s.occurrencesByID {
		if occurrence.ScheduleID == scheduleID {
			out = append(out, occurrence)
		}
	}
	return out, nil
}
func (s *metricsStoreSpy) ListAllScenarioScheduleOccurrences() ([]*storage.ScenarioScheduleOccurrence, error) {
	out := make([]*storage.ScenarioScheduleOccurrence, 0, len(s.occurrencesByID))
	for _, occurrence := range s.occurrencesByID {
		out = append(out, occurrence)
	}
	return out, nil
}
func (s *metricsStoreSpy) SaveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval) error {
	if s.approvalsByID == nil {
		s.approvalsByID = make(map[string]*storage.ScenarioScheduleApproval)
	}
	clone := *approval
	if approval.ResolvedAt != nil {
		resolvedAt := *approval.ResolvedAt
		clone.ResolvedAt = &resolvedAt
	}
	s.approvalsByID[approval.ID] = &clone
	return nil
}
func (s *metricsStoreSpy) GetScenarioScheduleApproval(id string) (*storage.ScenarioScheduleApproval, error) {
	if s.approvalsByID == nil {
		return nil, nil
	}
	return s.approvalsByID[id], nil
}
func (s *metricsStoreSpy) ListScenarioScheduleApprovals(scheduleID string) ([]*storage.ScenarioScheduleApproval, error) {
	out := []*storage.ScenarioScheduleApproval{}
	for _, approval := range s.approvalsByID {
		if approval.ScheduleID == scheduleID {
			out = append(out, approval)
		}
	}
	return out, nil
}
func (s *metricsStoreSpy) TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error) {
	if s.leasesByName == nil {
		s.leasesByName = make(map[string]*storage.SchedulerLease)
	}
	if existing, ok := s.leasesByName[name]; ok && existing.ExpiresAt.After(now) && existing.OwnerID != ownerID {
		return false, nil
	}
	s.leasesByName[name] = &storage.SchedulerLease{Name: name, OwnerID: ownerID, AcquiredAt: now, ExpiresAt: now.Add(ttl), UpdatedAt: now}
	return true, nil
}
func (s *metricsStoreSpy) GetSchedulerLease(name string) (*storage.SchedulerLease, error) {
	if s.leasesByName == nil {
		return nil, nil
	}
	return s.leasesByName[name], nil
}
func (s *metricsStoreSpy) SaveScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) error {
	return nil
}
func (s *metricsStoreSpy) ListScenarioDataPoolBindings(scenarioID string) ([]storage.ScenarioDataPoolBinding, error) {
	return nil, nil
}
func (s *metricsStoreSpy) DeleteScenarioDataPoolBinding(id string) error { return nil }
func (s *metricsStoreSpy) SaveDataPool(pool *storage.SharedDataPool) error {
	return nil
}
func (s *metricsStoreSpy) GetDataPool(id string) (*storage.SharedDataPool, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListDataPools(includeArchived bool) ([]*storage.SharedDataPool, error) {
	return nil, nil
}
func (s *metricsStoreSpy) DeleteDataPool(id string) error { return nil }
func (s *metricsStoreSpy) SaveDataPoolRow(row *storage.SharedDataPoolRow) error {
	return nil
}
func (s *metricsStoreSpy) ListDataPoolRows(poolID string) ([]*storage.SharedDataPoolRow, error) {
	return nil, nil
}
func (s *metricsStoreSpy) DeleteDataPoolRow(id string) error { return nil }
func (s *metricsStoreSpy) SaveProject(project *storage.Project) error {
	if s.projectsByID == nil {
		s.projectsByID = make(map[string]*storage.Project)
	}
	clone := *project
	if len(project.Settings.RedactionRules) > 0 {
		clone.Settings.RedactionRules = append([]engine.RedactionRule(nil), project.Settings.RedactionRules...)
	}
	s.projectsByID[project.ID] = &clone
	return nil
}
func (s *metricsStoreSpy) GetProject(id string) (*storage.Project, error) {
	if s.projectsByID == nil {
		return nil, nil
	}
	project, ok := s.projectsByID[id]
	if !ok {
		return nil, nil
	}
	clone := *project
	if len(project.Settings.RedactionRules) > 0 {
		clone.Settings.RedactionRules = append([]engine.RedactionRule(nil), project.Settings.RedactionRules...)
	}
	return &clone, nil
}
func (s *metricsStoreSpy) ListProjects() ([]*storage.Project, error) { return nil, nil }
func (s *metricsStoreSpy) DeleteProject(id string) error             { return nil }
func (s *metricsStoreSpy) SaveFolder(folder *storage.Folder) error   { return nil }
func (s *metricsStoreSpy) GetFolder(id string) (*storage.Folder, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListFolders(projectID string, category string) ([]*storage.Folder, error) {
	return nil, nil
}
func (s *metricsStoreSpy) MoveFolder(id string, newParentID *string) error { return nil }
func (s *metricsStoreSpy) DeleteFolder(id string) error                    { return nil }
func (s *metricsStoreSpy) SaveHistory(testID string, timestamp time.Time, metrics any) error {
	s.savedHistoryTestID = testID
	s.savedHistoryTimestamp = timestamp
	s.savedHistoryMetrics = metrics
	s.saveHistoryCalls++
	return nil
}
func (s *metricsStoreSpy) GetHistory(testID string) ([]any, error) {
	if s.historyByTestID == nil {
		return nil, nil
	}
	return s.historyByTestID[testID], nil
}
func (s *metricsStoreSpy) SaveRunArchive(record *storage.RunArchiveRecord) error { return nil }
func (s *metricsStoreSpy) GetRunArchive(testID string) (*storage.RunArchiveRecord, error) {
	if s.archiveByTestID == nil {
		return nil, nil
	}
	return s.archiveByTestID[testID], nil
}
func (s *metricsStoreSpy) SaveSetting(key string, value string) error { return nil }
func (s *metricsStoreSpy) GetSetting(key string) (string, error)      { return "", nil }
func (s *metricsStoreSpy) SaveServiceAccount(account *storage.ServiceAccount) error {
	return nil
}
func (s *metricsStoreSpy) GetServiceAccount(id string) (*storage.ServiceAccount, error) {
	return nil, nil
}
func (s *metricsStoreSpy) GetServiceAccountByKeyHash(keyHash string) (*storage.ServiceAccount, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListServiceAccounts() ([]*storage.ServiceAccount, error) {
	return nil, nil
}
func (s *metricsStoreSpy) RotateServiceAccountKey(id string, keyHash string, updatedAt time.Time) error {
	return nil
}
func (s *metricsStoreSpy) UpdateServiceAccountLastUsed(id string, lastUsedAt time.Time) error {
	return nil
}
func (s *metricsStoreSpy) UpdateServiceAccountActive(id string, active bool, updatedAt time.Time) error {
	return nil
}
func (s *metricsStoreSpy) DeleteServiceAccount(id string) error { return nil }
func (s *metricsStoreSpy) SaveAuditEvent(event *storage.AuditEvent) error {
	if event != nil {
		clone := *event
		if event.Details != nil {
			clone.Details = map[string]any{}
			for key, value := range event.Details {
				clone.Details[key] = value
			}
		}
		s.auditEvents = append(s.auditEvents, &clone)
	}
	return nil
}
func (s *metricsStoreSpy) ListAuditEvents(filter storage.AuditEventFilter) ([]*storage.AuditEvent, error) {
	out := make([]*storage.AuditEvent, 0, len(s.auditEvents))
	for _, event := range s.auditEvents {
		if event == nil {
			continue
		}
		if filter.ProjectID != "" && event.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Action != "" && event.Action != filter.Action {
			continue
		}
		out = append(out, event)
	}
	return out, nil
}
func (s *metricsStoreSpy) SaveUsageAccountingSnapshot(snapshot *storage.UsageAccountingSnapshot) error {
	if snapshot != nil {
		clone := *snapshot
		s.usageSnapshots = append(s.usageSnapshots, &clone)
	}
	return nil
}
func (s *metricsStoreSpy) ListUsageAccountingSnapshots(projectID string, limit int, offset int) ([]*storage.UsageAccountingSnapshot, error) {
	return nil, nil
}
func (s *metricsStoreSpy) Close() error { return nil }

func registerRunnerTestProtocol(t *testing.T, prefix string, factory func() protocol.Protocol) string {
	t.Helper()
	name := prefix + "-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + "-" + strconv.FormatUint(runnerTestProtocolCounter.Add(1), 10)
	protocol.Register(name, factory)
	return name
}

type archiveStubProtocol struct{}

func (p *archiveStubProtocol) Name() string                                   { return "archive-stub" }
func (p *archiveStubProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *archiveStubProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{
		ActionName:       action.Name,
		StartTime:        time.Now(),
		Duration:         5 * time.Millisecond,
		BytesSent:        128,
		BytesReceived:    512,
		StatusCode:       200,
		Success:          true,
		CorrelationID:    "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		DependencyType:   "http",
		DependencyTarget: "archive.example.test",
		Extractions:      map[string]string{"soap_operation": "CreateOrder"},
	}, nil
}
func (p *archiveStubProtocol) Teardown(_ context.Context) error    { return nil }
func (p *archiveStubProtocol) HealthCheck(_ context.Context) error { return nil }

type archiveRecorderStub struct {
	finalizeRecord   *storage.RunArchiveRecord
	finalizeErr      error
	intervalCalls    int
	resourceCalls    int
	recordedLive     *LiveMetrics
	recordedAt       time.Time
	recordedVUsers   int64
	recordedResource *engine.SystemResources
}

func (s *archiveRecorderStub) RecordResult(result *protocol.Result) error { return nil }
func (s *archiveRecorderStub) RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error {
	s.resourceCalls++
	s.recordedAt = timestamp
	s.recordedVUsers = activeVUsers
	s.recordedResource = resources
	return nil
}
func (s *archiveRecorderStub) RecordIntervalSummary(live *LiveMetrics) error {
	s.intervalCalls++
	s.recordedLive = live
	return nil
}
func (s *archiveRecorderStub) Finalize(status string, completedAt time.Time, errStr string, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error) {
	return s.finalizeRecord, s.finalizeErr
}

func TestCreateTestPreservesRuntimeSettingsAndGroups(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
			Log: dsl.LogConfig{Level: "standard"},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	iterations := 7
	targetAverageTPS := 300.0
	p95LatencyMs := 850.0
	minVUsers := 15
	maxVUsers := 120
	pacingDelay := 1500
	test, err := runner.CreateTest(CreateTestRequest{
		Name:             "Checkout Replay",
		Duration:         "90s",
		StartDelay:       "30s",
		ScenarioMode:     "goal_oriented",
		ScriptShakeout:   true,
		ResourceBaseline: true,
		GoalConfig: &GoalConfig{
			TargetAverageTPS: &targetAverageTPS,
			P95LatencyMs:     &p95LatencyMs,
			MinVUsers:        &minVUsers,
			MaxVUsers:        &maxVUsers,
			StopOnSLABreach:  true,
			SLABreachWindow:  "45s",
		},
		Groups: []CreateGroupEntry{{
			Name:         "Group A",
			ScriptID:     "script-1",
			VUsers:       25,
			RampUp:       "15s",
			RampDown:     "10s",
			Iterations:   &iterations,
			InitStrategy: "at_once",
		}},
		RuntimeSettings: &RuntimeSettingsOverride{
			Iterations:       &iterations,
			Pacing:           &dsl.PacingConfig{Type: "fixed_delay", DelayMs: pacingDelay},
			ThinkTime:        &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 750},
			LogLevel:         "extended",
			AutoTransactions: "per_iteration",
			BaseURL:          "https://pr-123.example.com",
			Env:              map[string]string{"target_namespace": "pr-123"},
			RedactionRules:   []engine.RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}},
			ErrorHandling: &dsl.ErrorHandlingConfig{
				ContinueOnError: true,
				SnapshotOnError: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if test.RuntimeSettings == nil {
		t.Fatal("expected runtime settings to be preserved on managed test")
	}
	if test.RuntimeSettings.Iterations == nil || *test.RuntimeSettings.Iterations != iterations {
		t.Fatalf("expected iterations %d, got %#v", iterations, test.RuntimeSettings.Iterations)
	}
	if test.RuntimeSettings.LogLevel != "extended" {
		t.Fatalf("expected log level extended, got %q", test.RuntimeSettings.LogLevel)
	}
	if test.RuntimeSettings.Pacing == nil || test.RuntimeSettings.Pacing.Type != "fixed_delay" || test.RuntimeSettings.Pacing.DelayMs != pacingDelay {
		t.Fatalf("expected pacing fixed_delay 1500ms, got %#v", test.RuntimeSettings.Pacing)
	}
	if test.RuntimeSettings.ThinkTime == nil || test.RuntimeSettings.ThinkTime.Type != "fixed" || test.RuntimeSettings.ThinkTime.MeanMs != 750 {
		t.Fatalf("expected think time fixed 750ms, got %#v", test.RuntimeSettings.ThinkTime)
	}
	if test.RuntimeSettings.AutoTransactions != "per_iteration" {
		t.Fatalf("expected auto transactions per_iteration, got %q", test.RuntimeSettings.AutoTransactions)
	}
	if test.RuntimeSettings.BaseURL != "https://pr-123.example.com" {
		t.Fatalf("expected base_url override, got %q", test.RuntimeSettings.BaseURL)
	}
	if test.RuntimeSettings.Env["target_namespace"] != "pr-123" {
		t.Fatalf("expected env override target_namespace=pr-123, got %#v", test.RuntimeSettings.Env)
	}
	if test.RuntimeSettings.ErrorHandling == nil || !test.RuntimeSettings.ErrorHandling.SnapshotOnError || !test.RuntimeSettings.ErrorHandling.ContinueOnError {
		t.Fatal("expected continue_on_error and snapshot_on_error overrides to be preserved")
	}
	if len(test.RuntimeSettings.RedactionRules) != 1 || test.RuntimeSettings.RedactionRules[0].Pattern != `customer-[0-9]+` {
		t.Fatalf("expected redaction rule to be preserved, got %#v", test.RuntimeSettings.RedactionRules)
	}
	if test.ScenarioMode != "goal_oriented" {
		t.Fatalf("expected scenario mode goal_oriented, got %q", test.ScenarioMode)
	}
	if !test.ScriptShakeout {
		t.Fatal("expected script shakeout to be preserved on managed test")
	}
	if !test.ResourceBaseline {
		t.Fatal("expected resource baseline to be preserved on managed test")
	}
	if test.GoalConfig == nil {
		t.Fatal("expected goal config to be preserved on managed test")
	}
	if test.GoalConfig.TargetAverageTPS == nil || *test.GoalConfig.TargetAverageTPS != targetAverageTPS {
		t.Fatalf("expected target average TPS %v, got %#v", targetAverageTPS, test.GoalConfig.TargetAverageTPS)
	}
	if test.GoalConfig.P95LatencyMs == nil || *test.GoalConfig.P95LatencyMs != p95LatencyMs {
		t.Fatalf("expected p95 latency %v, got %#v", p95LatencyMs, test.GoalConfig.P95LatencyMs)
	}
	if test.GoalConfig.MinVUsers == nil || *test.GoalConfig.MinVUsers != minVUsers {
		t.Fatalf("expected min vusers %d, got %#v", minVUsers, test.GoalConfig.MinVUsers)
	}
	if test.GoalConfig.MaxVUsers == nil || *test.GoalConfig.MaxVUsers != maxVUsers {
		t.Fatalf("expected max vusers %d, got %#v", maxVUsers, test.GoalConfig.MaxVUsers)
	}
	if !test.GoalConfig.StopOnSLABreach {
		t.Fatal("expected stop_on_sla_breach to be preserved")
	}
	if test.GoalConfig.SLABreachWindow != "45s" {
		t.Fatalf("expected SLA breach window 45s, got %q", test.GoalConfig.SLABreachWindow)
	}
	if len(test.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(test.Groups))
	}
	if test.Groups[0].RampDown != "10s" {
		t.Fatalf("expected ramp down 10s, got %q", test.Groups[0].RampDown)
	}
	if test.Groups[0].Iterations == nil || *test.Groups[0].Iterations != iterations {
		t.Fatalf("expected group iterations %d, got %#v", iterations, test.Groups[0].Iterations)
	}
	if test.Groups[0].Initialization != "at_once" {
		t.Fatalf("expected initialization at_once, got %q", test.Groups[0].Initialization)
	}
	if test.DurationStr != "90s" {
		t.Fatalf("expected duration string 90s, got %q", test.DurationStr)
	}
	if test.StartDelay != "30s" {
		t.Fatalf("expected start delay 30s, got %q", test.StartDelay)
	}
}

func TestCreateTestInheritsProjectRedactionRules(t *testing.T) {
	hub := NewHub()
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {
			ID:   "project-1",
			Name: "Payments",
			Settings: storage.ProjectSettings{
				RedactionRules: []engine.RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}},
			},
		},
	}}
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:        "script-project-redaction",
		Name:      "Project Redaction Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Actions:   []dsl.Action{{Name: "lookup", Type: "GET", Target: "/orders/customer-1234"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Project Rules",
		Duration: "30s",
		ScriptID: "script-project-redaction",
		VUsers:   3,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if test.ProjectID != "project-1" {
		t.Fatalf("project id = %q, want project-1", test.ProjectID)
	}
	if test.RuntimeSettings == nil || len(test.RuntimeSettings.RedactionRules) != 1 {
		t.Fatalf("runtime settings = %#v, want inherited project redaction rule", test.RuntimeSettings)
	}
	if len(test.groupSpecs) != 1 || test.groupSpecs[0].RedactionConfig == nil {
		t.Fatalf("group specs = %#v, want redaction config", test.groupSpecs)
	}
	if got := test.groupSpecs[0].RedactionConfig.ScrubText("customer-1234 ok"); got != "[REDACTED] ok" {
		t.Fatalf("scrubbed text = %q, want inherited project rule applied", got)
	}
}

func TestApplyRuntimeOverridesAppliesExtendedOverrideFields(t *testing.T) {
	headless := false
	touchMode := true
	defaultHeadless := true
	defaultTouchMode := false
	iterations := 4
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL:       "https://default.example.com",
			Browser:       dsl.BrowserConfig{Headless: &defaultHeadless, DeviceProfile: "desktop_1080p", TouchMode: &defaultTouchMode, Orientation: "landscape"},
			RunLogic:      dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "none"},
			Pacing:        dsl.PacingConfig{Type: "none"},
			ThinkTime:     dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1000},
			ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false, SnapshotOnError: false},
			Log:           dsl.LogConfig{Level: "standard"},
		},
		Actions: []dsl.Action{{
			Name:    "runtime-step",
			Type:    "GET",
			Target:  "/namespaces/{{env.target_namespace}}/orders",
			Body:    "namespace={{env.target_namespace}}",
			Headers: map[string]string{"X-Namespace": "{{env.target_namespace}}"},
		}},
	}

	err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{
		Iterations:       &iterations,
		Pacing:           &dsl.PacingConfig{Type: "fixed_interval", IntervalMs: 2500},
		ThinkTime:        &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 750},
		Browser:          &RuntimeBrowserOverride{Headless: &headless, DeviceProfile: "iphone_se", TouchMode: &touchMode, Orientation: "portrait"},
		LogLevel:         "extended",
		AutoTransactions: "per_iteration",
		BaseURL:          "https://pr-123.example.com",
		Env:              map[string]string{"target_namespace": "pr-123"},
		ErrorHandling:    &dsl.ErrorHandlingConfig{ContinueOnError: true, SnapshotOnError: true},
	})
	if err != nil {
		t.Fatalf("applyRuntimeOverrides returned error: %v", err)
	}

	if script.Config.RunLogic.Iterations != 4 {
		t.Fatalf("iterations = %d, want 4", script.Config.RunLogic.Iterations)
	}
	if script.Config.BaseURL != "https://pr-123.example.com" {
		t.Fatalf("base_url = %q, want https://pr-123.example.com", script.Config.BaseURL)
	}
	if script.Config.Pacing.Type != "fixed_interval" || script.Config.Pacing.IntervalMs != 2500 {
		t.Fatalf("pacing = %#v, want fixed_interval 2500ms", script.Config.Pacing)
	}
	if script.Config.ThinkTime.Type != "fixed" || script.Config.ThinkTime.MeanMs != 750 {
		t.Fatalf("think_time = %#v, want fixed 750ms", script.Config.ThinkTime)
	}
	if script.Config.Browser.Headless == nil || *script.Config.Browser.Headless != false {
		t.Fatalf("browser.headless = %#v, want false", script.Config.Browser.Headless)
	}
	if script.Config.Browser.DeviceProfile != "iphone_se" {
		t.Fatalf("browser.device_profile = %q, want iphone_se", script.Config.Browser.DeviceProfile)
	}
	if script.Config.Browser.TouchMode == nil || *script.Config.Browser.TouchMode != true {
		t.Fatalf("browser.touch_mode = %#v, want true", script.Config.Browser.TouchMode)
	}
	if script.Config.Browser.Orientation != "portrait" {
		t.Fatalf("browser.orientation = %q, want portrait", script.Config.Browser.Orientation)
	}
	if script.Config.Log.Level != "extended" {
		t.Fatalf("log level = %q, want extended", script.Config.Log.Level)
	}
	if script.Config.RunLogic.AutoTransactions != "per_iteration" {
		t.Fatalf("auto transactions = %q, want per_iteration", script.Config.RunLogic.AutoTransactions)
	}
	if !script.Config.ErrorHandling.ContinueOnError || !script.Config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error handling = %#v, want continue_on_error and snapshot_on_error", script.Config.ErrorHandling)
	}
	if len(script.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(script.Actions))
	}
	if script.Actions[0].Target != "/namespaces/pr-123/orders" {
		t.Fatalf("target = %q, want /namespaces/pr-123/orders", script.Actions[0].Target)
	}
	if script.Actions[0].Body != "namespace=pr-123" {
		t.Fatalf("body = %q, want namespace=pr-123", script.Actions[0].Body)
	}
	if script.Actions[0].Headers["X-Namespace"] != "pr-123" {
		t.Fatalf("header X-Namespace = %q, want pr-123", script.Actions[0].Headers["X-Namespace"])
	}
}

func TestCompileManagedTestGroupLockedAppliesEphemeralTargetOverridesWithoutMutatingStoredScript(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-ephemeral-target",
		Name:     "Ephemeral Target Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://default.example.com",
		},
		Actions: []dsl.Action{{
			Name:    "runtime-step",
			Type:    "GET",
			Target:  "/namespaces/{{env.target_namespace}}/orders",
			Body:    "namespace={{env.target_namespace}}",
			Headers: map[string]string{"X-Namespace": "{{env.target_namespace}}"},
		}},
	})

	_, engineGroup, err := runner.compileManagedTestGroupLocked(0, 1, CreateGroupEntry{
		ScriptID: "script-ephemeral-target",
		VUsers:   5,
	}, &RuntimeSettingsOverride{
		BaseURL: "https://pr-123.example.com",
		Env:     map[string]string{"target_namespace": "pr-123"},
	}, nil)
	if err != nil {
		t.Fatalf("compileManagedTestGroupLocked returned error: %v", err)
	}

	if engineGroup.Script == nil {
		t.Fatal("expected engine group script")
	}
	if engineGroup.Script.Config.BaseURL != "https://pr-123.example.com" {
		t.Fatalf("compiled base_url = %q, want https://pr-123.example.com", engineGroup.Script.Config.BaseURL)
	}
	if engineGroup.Script.Actions[0].Target != "/namespaces/pr-123/orders" {
		t.Fatalf("compiled target = %q, want /namespaces/pr-123/orders", engineGroup.Script.Actions[0].Target)
	}
	if engineGroup.Script.Actions[0].Body != "namespace=pr-123" {
		t.Fatalf("compiled body = %q, want namespace=pr-123", engineGroup.Script.Actions[0].Body)
	}
	if engineGroup.Script.Actions[0].Headers["X-Namespace"] != "pr-123" {
		t.Fatalf("compiled header X-Namespace = %q, want pr-123", engineGroup.Script.Actions[0].Headers["X-Namespace"])
	}

	stored, ok := runner.GetScript("script-ephemeral-target")
	if !ok {
		t.Fatal("expected stored script to remain available")
	}
	if stored.Script.Config.BaseURL != "https://default.example.com" {
		t.Fatalf("stored base_url = %q, want https://default.example.com", stored.Script.Config.BaseURL)
	}
	if stored.Script.Actions[0].Target != "/namespaces/{{env.target_namespace}}/orders" {
		t.Fatalf("stored target = %q, want template to remain intact", stored.Script.Actions[0].Target)
	}
	if stored.Script.Actions[0].Body != "namespace={{env.target_namespace}}" {
		t.Fatalf("stored body = %q, want template to remain intact", stored.Script.Actions[0].Body)
	}
	if stored.Script.Actions[0].Headers["X-Namespace"] != "{{env.target_namespace}}" {
		t.Fatalf("stored header X-Namespace = %q, want template to remain intact", stored.Script.Actions[0].Headers["X-Namespace"])
	}
}

func TestCompileManagedTestGroupLockedDerivesRampFromStages(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-staged-load",
		Name:     "Staged Load Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "GET", Target: "/orders"}},
	})

	testGroup, engineGroup, err := runner.compileManagedTestGroupLocked(0, 1, CreateGroupEntry{
		ScriptID: "script-staged-load",
		Stages: []storage.LoadStage{
			{Name: "Warmup", Type: "ramp_up", Duration: "30s", TargetVUsers: 20},
			{Name: "Hold", Type: "hold", Duration: "2m", TargetVUsers: 20},
			{Name: "Cooldown", Type: "ramp_down", Duration: "15s", TargetVUsers: 1},
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("compileManagedTestGroupLocked returned error: %v", err)
	}
	if testGroup.VUsers != 20 || testGroup.RampUp != "30s" || testGroup.RampDown != "15s" {
		t.Fatalf("test group = %#v, want stage-derived vusers/ramp", testGroup)
	}
	if len(testGroup.Stages) != 3 {
		t.Fatalf("stages = %#v, want preserved staged model", testGroup.Stages)
	}
	if engineGroup.VUsers != 20 || engineGroup.RampUp != 30*time.Second || engineGroup.RampDown != 15*time.Second {
		t.Fatalf("engine group = %#v, want compatibility ramp durations", engineGroup)
	}
}

func TestCompileManagedTestGroupLockedAppliesHeadlessOverrideWithoutMutatingStoredScript(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	defaultHeadless := true
	defaultTouchMode := false
	runner.StoreScript(&dsl.Script{
		ID:       "script-browser-headless-override",
		Name:     "Browser Script",
		Protocol: "browser",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			Browser: dsl.BrowserConfig{Headless: &defaultHeadless, DeviceProfile: "desktop_1080p", TouchMode: &defaultTouchMode, Orientation: "landscape"},
		},
		Actions: []dsl.Action{{Name: "open-home", Type: "browser_navigate", Target: "/"}},
	})

	headless := false
	touchMode := true
	_, engineGroup, err := runner.compileManagedTestGroupLocked(0, 1, CreateGroupEntry{
		ScriptID: "script-browser-headless-override",
		VUsers:   2,
	}, &RuntimeSettingsOverride{
		Browser: &RuntimeBrowserOverride{Headless: &headless, DeviceProfile: "iphone_se", TouchMode: &touchMode, Orientation: "portrait"},
	}, nil)
	if err != nil {
		t.Fatalf("compileManagedTestGroupLocked returned error: %v", err)
	}
	if engineGroup.Script == nil {
		t.Fatal("expected engine group script")
	}
	if engineGroup.Script.Config.Browser.Headless == nil || *engineGroup.Script.Config.Browser.Headless != false {
		t.Fatalf("compiled browser.headless = %#v, want false", engineGroup.Script.Config.Browser.Headless)
	}
	if engineGroup.Script.Config.Browser.DeviceProfile != "iphone_se" {
		t.Fatalf("compiled browser.device_profile = %q, want iphone_se", engineGroup.Script.Config.Browser.DeviceProfile)
	}
	if engineGroup.Script.Config.Browser.TouchMode == nil || *engineGroup.Script.Config.Browser.TouchMode != true {
		t.Fatalf("compiled browser.touch_mode = %#v, want true", engineGroup.Script.Config.Browser.TouchMode)
	}
	if engineGroup.Script.Config.Browser.Orientation != "portrait" {
		t.Fatalf("compiled browser.orientation = %q, want portrait", engineGroup.Script.Config.Browser.Orientation)
	}

	stored, ok := runner.GetScript("script-browser-headless-override")
	if !ok {
		t.Fatal("expected stored script to remain available")
	}
	if stored.Script.Config.Browser.Headless == nil || *stored.Script.Config.Browser.Headless != true {
		t.Fatalf("stored browser.headless = %#v, want true", stored.Script.Config.Browser.Headless)
	}
	if stored.Script.Config.Browser.DeviceProfile != "desktop_1080p" {
		t.Fatalf("stored browser.device_profile = %q, want desktop_1080p", stored.Script.Config.Browser.DeviceProfile)
	}
	if stored.Script.Config.Browser.TouchMode == nil || *stored.Script.Config.Browser.TouchMode != false {
		t.Fatalf("stored browser.touch_mode = %#v, want false", stored.Script.Config.Browser.TouchMode)
	}
	if stored.Script.Config.Browser.Orientation != "landscape" {
		t.Fatalf("stored browser.orientation = %q, want landscape", stored.Script.Config.Browser.Orientation)
	}
}

func TestCompileManagedTestGroupLockedBuildsCustomRedactionConfig(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-redaction-rules",
		Name:     "Redaction Rule Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "runtime-step", Type: "GET", Target: "/orders"}},
	})

	_, engineGroup, err := runner.compileManagedTestGroupLocked(0, 1, CreateGroupEntry{
		ScriptID: "script-redaction-rules",
		VUsers:   2,
	}, &RuntimeSettingsOverride{
		RedactionRules: []engine.RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}},
	}, nil)
	if err != nil {
		t.Fatalf("compileManagedTestGroupLocked returned error: %v", err)
	}
	if engineGroup.RedactionConfig == nil {
		t.Fatal("expected engine redaction config")
	}
	if got := engineGroup.RedactionConfig.ScrubText(`customer-1234 ok`); got != `[REDACTED] ok` {
		t.Fatalf("scrubbed text = %q, want custom rule applied", got)
	}
}

func TestCompileManagedTestGroupLockedRejectsInvalidRedactionRule(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-invalid-redaction-rules",
		Name:     "Invalid Redaction Rule Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "runtime-step", Type: "GET", Target: "/orders"}},
	})

	_, _, err := runner.compileManagedTestGroupLocked(0, 1, CreateGroupEntry{
		ScriptID: "script-invalid-redaction-rules",
		VUsers:   2,
	}, &RuntimeSettingsOverride{
		RedactionRules: []engine.RedactionRule{{Name: "broken", Pattern: `[`}},
	}, nil)
	if err == nil {
		t.Fatal("expected invalid redaction rule to fail")
	}
}

func TestRunArtifactsFinalizePersistsArchiveRecord(t *testing.T) {
	record := &storage.RunArchiveRecord{TestID: "test-1", ArchivePath: "/tmp/archive"}
	recorder := &archiveRecorderStub{finalizeRecord: record}
	persisted := 0
	artifacts := &runArtifacts{
		recorder: recorder,
		saveRecord: func(saved *storage.RunArchiveRecord) error {
			if saved != record {
				t.Fatalf("expected persisted record %p, got %p", record, saved)
			}
			persisted++
			return nil
		},
	}

	archiveRecord, finalStatus, finalError := artifacts.finalize(nil, "COMPLETE", time.Now(), "")

	if archiveRecord != record {
		t.Fatalf("expected archive record %p, got %p", record, archiveRecord)
	}
	if finalStatus != "COMPLETE" || finalError != "" {
		t.Fatalf("unexpected finalize outcome status=%q error=%q", finalStatus, finalError)
	}
	if persisted != 1 {
		t.Fatalf("expected archive record persistence once, got %d", persisted)
	}
}

func TestNormalizeCreateTestGroupsUsesSingleScriptFallback(t *testing.T) {
	groups, err := normalizeCreateTestGroups(CreateTestRequest{
		ScriptID: "script-1",
		VUsers:   25,
		RampUp:   "15s",
	})
	if err != nil {
		t.Fatalf("normalizeCreateTestGroups returned error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].ScriptID != "script-1" {
		t.Fatalf("expected script-1, got %q", groups[0].ScriptID)
	}
	if groups[0].VUsers != 25 {
		t.Fatalf("expected 25 vusers, got %d", groups[0].VUsers)
	}
	if groups[0].RampUp != "15s" {
		t.Fatalf("expected ramp-up 15s, got %q", groups[0].RampUp)
	}
	if groups[0].RampDown != "" {
		t.Fatalf("expected empty ramp-down, got %q", groups[0].RampDown)
	}
}

func TestAppendRunErrorBuildsMessage(t *testing.T) {
	msg := appendRunError("", "archive finalize failed", errors.New("disk full"))
	if msg != "archive finalize failed: disk full" {
		t.Fatalf("unexpected message: %q", msg)
	}

	msg = appendRunError("engine failed", "archive metadata save failed", errors.New("permission denied"))
	if msg != "engine failed; archive metadata save failed: permission denied" {
		t.Fatalf("unexpected appended message: %q", msg)
	}
}

func TestResourceBaselineWindows(t *testing.T) {
	pre, post := resourceBaselineWindows(false)
	if pre != 0 || post != 0 {
		t.Fatalf("expected disabled baseline windows to be zero, got pre=%s post=%s", pre, post)
	}

	pre, post = resourceBaselineWindows(true)
	if pre != 5*time.Minute || post != 5*time.Minute {
		t.Fatalf("expected enabled baseline windows to be 5m, got pre=%s post=%s", pre, post)
	}
}

func TestStartDelayWindow(t *testing.T) {
	if got := startDelayWindow(""); got != 0 {
		t.Fatalf("expected empty start delay to be zero, got %v", got)
	}
	if got := startDelayWindow("45s"); got != 45*time.Second {
		t.Fatalf("expected 45s start delay, got %v", got)
	}
	if got := startDelayWindow("bad-value"); got != 0 {
		t.Fatalf("expected invalid start delay to normalize to 0, got %v", got)
	}
}

func TestBuildLiveMetricsFromSnapshot(t *testing.T) {
	timestamp := time.Date(2026, time.May, 10, 8, 30, 0, 0, time.UTC)
	resources := &engine.SystemResources{CPUProcessPct: 12.5, MemProcessMb: 256}

	live := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{
		Timestamp:              timestamp,
		ActiveVUsers:           18,
		TPS:                    24.5,
		TargetTPS:              30,
		ThroughputGapPct:       18.3333333333,
		ThroughputTargetMissed: true,
		Transactions:           40,
		Errors:                 5,
		AvgDuration:            250 * time.Millisecond,
		P50:                    200 * time.Millisecond,
		P90:                    350 * time.Millisecond,
		P95:                    400 * time.Millisecond,
		P99:                    600 * time.Millisecond,
		ByAction: map[string]*engine.ActionMetrics{
			"login": {
				Name:             "login",
				DependencyType:   "http",
				DependencyTarget: "api.example.test",
				LastErrorMessage: "HTTP 503: backend timeout 12345",
				LastStatusCode:   503,
				LastErrorAt:      timestamp.Add(2 * time.Second),
				AvgDNSMs:         8,
				AvgConnectMs:     14,
				AvgTLSMs:         6,
				AvgTTFBMs:        74,
				Count:            10,
				Errors:           1,
				AvgDuration:      150 * time.Millisecond,
				P50:              120 * time.Millisecond,
				P90:              240 * time.Millisecond,
				P95:              320 * time.Millisecond,
				P99:              500 * time.Millisecond,
			},
		},
		ByDependencyTarget: map[string]*engine.ActionMetrics{
			"api.example.test": {
				Name:             "api.example.test",
				DependencyType:   "http",
				DependencyTarget: "api.example.test",
				Count:            12,
				Errors:           2,
				AvgDuration:      180 * time.Millisecond,
				P50:              140 * time.Millisecond,
				P90:              260 * time.Millisecond,
				P95:              330 * time.Millisecond,
				P99:              470 * time.Millisecond,
				ServerTelemetry: &engine.ServerTelemetrySummary{
					Samples:               12,
					AvgServerProcessingMs: 58,
					AvgTransferMs:         16,
				},
			},
		},
		BySOAPOperation: map[string]*engine.ActionMetrics{
			"CreateOrder": {
				DependencyType:   "soap",
				DependencyTarget: "checkout.example.test#CreateOrder",
				Count:            8,
				Errors:           1,
				AvgDuration:      190 * time.Millisecond,
				P50:              170 * time.Millisecond,
				P90:              230 * time.Millisecond,
				P99:              310 * time.Millisecond,
				ServerTelemetry: &engine.ServerTelemetrySummary{
					Samples:               8,
					AvgServerProcessingMs: 55,
					AvgTransferMs:         15,
					RequestPayloadHistogram: map[string]int64{
						"1-4 KB": 3,
					},
					ResponsePayloadHistogram: map[string]int64{
						"4-16 KB": 8,
					},
					AvgMessageLatencyMs:       42,
					MaxMessageLatencyMs:       61,
					MessagesSent:              8,
					MessagesReceived:          6,
					AvgMessagesSentPerSec:     11,
					AvgMessagesReceivedPerSec: 8.5,
					AvgConsumerLagMs:          12,
					MaxConsumerLagMs:          18,
					RequestSizeClasses: map[string]int64{
						"small": 6,
					},
					ResponseSizeClasses: map[string]int64{
						"medium": 8,
					},
				},
			},
		},
		ByGroup: map[string]*engine.GroupMetrics{
			"buyers": {
				Transactions:           25,
				Errors:                 3,
				TPS:                    15.5,
				TargetTPS:              20,
				ThroughputGapPct:       22.5,
				ThroughputTargetMissed: true,
				AvgDuration:            300 * time.Millisecond,
				P50:                    210 * time.Millisecond,
				P90:                    420 * time.Millisecond,
				P99:                    900 * time.Millisecond,
			},
		},
		Browser: &engine.BrowserMetricsSummary{
			Samples:              2,
			AvgFCPMs:             120,
			AvgLCPMs:             240,
			AvgCLSScore:          0.03,
			AvgINPMs:             55,
			AvgBlockingResources: 2,
			AvgLongTaskCount:     1.5,
			AvgJSHeapUsedMB:      18,
			AvgBrowserProcessCPU: 22,
			AvgBrowserProcessMem: 256,
		},
		BrowserByDevice: map[string]*engine.BrowserDeviceMetrics{
			"iphone_se": {
				DeviceProfile:        "iphone_se",
				DevicePlatform:       "ios",
				DeviceTier:           "low",
				BrowserEngine:        "webkit",
				Samples:              2,
				AvgFCPMs:             120,
				AvgLCPMs:             240,
				AvgCLSScore:          0.03,
				AvgINPMs:             55,
				AvgBlockingResources: 2,
				AvgLongTaskCount:     1.5,
				AvgJSHeapUsedMB:      18,
				AvgBrowserProcessCPU: 22,
				AvgBrowserProcessMem: 256,
			},
		},
		Resources: resources,
	}, map[string]int{"buyers": 12})

	if live.Timestamp != timestamp {
		t.Fatalf("expected timestamp %s, got %s", timestamp, live.Timestamp)
	}
	if live.ActiveVUsers != 18 {
		t.Fatalf("expected 18 active vusers, got %d", live.ActiveVUsers)
	}
	if live.TargetTPS != 30 {
		t.Fatalf("expected target TPS 30, got %v", live.TargetTPS)
	}
	if !live.ThroughputTargetMissed {
		t.Fatal("expected throughput target miss to be mapped")
	}
	if live.AvgResponseMs != 250 {
		t.Fatalf("expected avg response 250ms, got %v", live.AvgResponseMs)
	}
	if live.ErrorPct != 12.5 {
		t.Fatalf("expected error pct 12.5, got %v", live.ErrorPct)
	}
	if live.Resources != resources {
		t.Fatal("expected resources pointer to be preserved")
	}
	if live.Browser == nil || live.Browser.Samples != 2 {
		t.Fatalf("expected browser summary to be mapped, got %#v", live.Browser)
	}
	if live.Browser.AvgBrowserProcessCPU != 22 || live.Browser.AvgBrowserProcessMem != 256 {
		t.Fatalf("expected browser process metrics to be mapped, got %#v", live.Browser)
	}
	if live.BrowserByDevice == nil || live.BrowserByDevice["iphone_se"] == nil {
		t.Fatalf("expected browser-by-device metrics to be mapped, got %#v", live.BrowserByDevice)
	}
	if live.BrowserByDevice["iphone_se"].BrowserEngine != "webkit" {
		t.Fatalf("expected browser engine to be mapped, got %#v", live.BrowserByDevice["iphone_se"])
	}
	if live.BrowserByDevice["iphone_se"].DevicePlatform != "ios" {
		t.Fatalf("expected device platform to be mapped, got %#v", live.BrowserByDevice["iphone_se"])
	}
	if live.SOAPOperations == nil || live.SOAPOperations["CreateOrder"] == nil {
		t.Fatalf("expected SOAP operations to be mapped, got %#v", live.SOAPOperations)
	}
	if live.SOAPOperations["CreateOrder"].DependencyTarget != "checkout.example.test#CreateOrder" {
		t.Fatalf("expected SOAP operation dependency target to be mapped, got %#v", live.SOAPOperations["CreateOrder"])
	}
	if live.SOAPOperations["CreateOrder"].ServerTelemetry == nil || live.SOAPOperations["CreateOrder"].ServerTelemetry.AvgConsumerLagMs != 12 {
		t.Fatalf("expected SOAP operation consumer lag telemetry to be mapped, got %#v", live.SOAPOperations["CreateOrder"].ServerTelemetry)
	}
	if live.SOAPOperations["CreateOrder"].ServerTelemetry.AvgMessageLatencyMs != 42 || live.SOAPOperations["CreateOrder"].ServerTelemetry.MessagesSent != 8 {
		t.Fatalf("expected SOAP operation messaging telemetry to be mapped, got %#v", live.SOAPOperations["CreateOrder"].ServerTelemetry)
	}
	if live.SOAPOperations["CreateOrder"].ServerTelemetry.RequestSizeClasses["small"] != 6 {
		t.Fatalf("expected SOAP operation request size classes to be mapped, got %#v", live.SOAPOperations["CreateOrder"].ServerTelemetry.RequestSizeClasses)
	}
	if live.SOAPOperations["CreateOrder"].ServerTelemetry.RequestPayloadHistogram["1-4 KB"] != 3 {
		t.Fatalf("expected SOAP operation request payload histogram to be mapped, got %#v", live.SOAPOperations["CreateOrder"].ServerTelemetry.RequestPayloadHistogram)
	}
	if live.SOAPOperations["CreateOrder"].ServerTelemetry.ResponsePayloadHistogram["4-16 KB"] != 8 {
		t.Fatalf("expected SOAP operation response payload histogram to be mapped, got %#v", live.SOAPOperations["CreateOrder"].ServerTelemetry.ResponsePayloadHistogram)
	}

	login, ok := live.Transactions["login"]
	if !ok {
		t.Fatal("expected login transaction metrics")
	}
	if login.Count != 10 || login.Errors != 1 {
		t.Fatalf("unexpected login metrics: %+v", login)
	}
	if login.LastErrorMessage != "HTTP 503: backend timeout 12345" || login.LastStatusCode != 503 || !login.LastErrorAt.Equal(timestamp.Add(2*time.Second)) {
		t.Fatalf("expected login last error metadata, got %+v", login)
	}
	if login.AvgDNSMs != 8 || login.AvgConnectMs != 14 || login.AvgTLSMs != 6 || login.AvgTTFBMs != 74 {
		t.Fatalf("expected login timing phases to be mapped, got %+v", login)
	}
	if login.AvgMs != 150 || login.P50Ms != 120 || login.P90Ms != 240 || login.P95Ms != 320 || login.P99Ms != 500 {
		t.Fatalf("unexpected converted login percentiles: %+v", login)
	}
	if live.DependencyHealth == nil || live.DependencyHealth["api.example.test"] == nil {
		t.Fatalf("expected dependency health metrics to be mapped, got %#v", live.DependencyHealth)
	}
	if live.DependencyHealth["api.example.test"].P95Ms != 330 || live.DependencyHealth["api.example.test"].ServerTelemetry == nil || live.DependencyHealth["api.example.test"].ServerTelemetry.AvgServerProcessingMs != 58 {
		t.Fatalf("expected dependency health p95 and telemetry to be mapped, got %#v", live.DependencyHealth["api.example.test"])
	}
	if live.DependencyMap == nil {
		t.Fatal("expected live dependency map")
	}
	if len(live.DependencyMap.Nodes) != 2 || len(live.DependencyMap.Edges) != 1 {
		t.Fatalf("dependency map = %#v, want source and one dependency edge", live.DependencyMap)
	}
	dependencyNode := live.DependencyMap.Nodes[1]
	if dependencyNode.ID != "dependency:api.example.test" || dependencyNode.DependencyType != "http" || dependencyNode.Status != "danger" {
		t.Fatalf("dependency node = %#v, want HTTP danger node", dependencyNode)
	}
	if dependencyNode.Transactions != 12 || dependencyNode.Errors != 2 || dependencyNode.ErrorPct != float64(2)/12*100 {
		t.Fatalf("dependency node metrics = %#v", dependencyNode)
	}
	dependencyEdge := live.DependencyMap.Edges[0]
	if dependencyEdge.Source != "stormrunner" || dependencyEdge.Target != dependencyNode.ID || dependencyEdge.P95Ms != 330 {
		t.Fatalf("dependency edge = %#v, want source-to-dependency edge", dependencyEdge)
	}

	buyers, ok := live.GroupMetrics["buyers"]
	if !ok {
		t.Fatal("expected buyers group metrics")
	}
	if buyers.VUsers != 12 {
		t.Fatalf("expected buyers vusers 12, got %d", buyers.VUsers)
	}
	if buyers.TargetTPS != 20 {
		t.Fatalf("expected buyers target TPS 20, got %v", buyers.TargetTPS)
	}
	if !buyers.ThroughputTargetMissed {
		t.Fatal("expected buyers throughput target miss to be mapped")
	}
	if buyers.Transactions != 25 || buyers.Errors != 3 {
		t.Fatalf("unexpected buyers counts: %+v", buyers)
	}
	if buyers.AvgResponseMs != 300 || buyers.P50Ms != 210 || buyers.P90Ms != 420 || buyers.P99Ms != 900 {
		t.Fatalf("unexpected converted buyers latencies: %+v", buyers)
	}
}

func TestUpdateLiveErrorPatternsTracksFirstLatestAndSnapshotQuery(t *testing.T) {
	firstSeen := time.Date(2026, time.May, 18, 10, 0, 0, 0, time.UTC)
	latestSeen := firstSeen.Add(30 * time.Second)
	state := updateLiveErrorPatterns(nil, &LiveMetrics{
		Timestamp: firstSeen,
		Transactions: map[string]*TxMetrics{
			"GET /orders/123": {
				Name:             "GET /orders/123",
				Count:            10,
				Errors:           2,
				LastErrorMessage: "HTTP 503 backend timeout order 123",
				LastStatusCode:   503,
				LastErrorAt:      firstSeen,
			},
		},
		GroupMetrics: map[string]*GroupLiveMetrics{
			"buyers": {Name: "buyers", Errors: 2},
		},
	}, "test/error run")
	state = updateLiveErrorPatterns(state, &LiveMetrics{
		Timestamp: latestSeen,
		Transactions: map[string]*TxMetrics{
			"GET /orders/456": {
				Name:             "GET /orders/456",
				Count:            5,
				Errors:           1,
				LastErrorMessage: "HTTP 503 backend timeout order 456",
				LastStatusCode:   503,
				LastErrorAt:      latestSeen,
			},
		},
		GroupMetrics: map[string]*GroupLiveMetrics{
			"buyers": {Name: "buyers", Errors: 1},
			"admins": {Name: "admins", Errors: 1},
		},
	}, "test/error run")

	patterns := liveErrorPatternsList(state)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %#v, want one normalized pattern", patterns)
	}
	pattern := patterns[0]
	if pattern.Count != 3 || !pattern.FirstSeen.Equal(firstSeen) || !pattern.LatestSeen.Equal(latestSeen) {
		t.Fatalf("pattern timeline/count = %#v", pattern)
	}
	if pattern.StatusCode != 503 || pattern.ErrorPct != 20 {
		t.Fatalf("pattern status/error pct = %#v", pattern)
	}
	if strings.Join(pattern.Actions, ",") != "GET /orders/123,GET /orders/456" {
		t.Fatalf("pattern actions = %#v", pattern.Actions)
	}
	if strings.Join(pattern.AffectedGroups, ",") != "admins,buyers" {
		t.Fatalf("pattern groups = %#v", pattern.AffectedGroups)
	}
	if pattern.SnapshotURL != "/api/v1/tests/test%2Ferror%20run/archive/transactions?limit=20&success=false" {
		t.Fatalf("snapshot url = %q", pattern.SnapshotURL)
	}
}

func TestBuildLiveCapacityStatusAggregatesDemandByProtocolAndRegion(t *testing.T) {
	current := &ManagedTest{
		ID:          "active",
		Status:      "RUNNING",
		TotalVUsers: 1200,
		Groups: []TestGroup{
			{Name: "buyers", Protocol: "http", VUsers: 900},
			{Name: "canaries", Protocol: "browser", VUsers: 300},
		},
		Assignment: &GeneratorAssignmentSettings{
			Mode:          generatorAssignmentModeRegionWeighted,
			RegionWeights: map[string]int{"east": 2, "west": 1},
		},
		CapacityAdvisory: &CapacityAdvisory{
			RecommendedGenerators: 4,
			AvailableGenerators:   3,
			ShortfallGenerators:   1,
			RequiresOverride:      true,
		},
	}
	queued := &ManagedTest{
		ID:          "queued",
		Status:      "PENDING",
		TotalVUsers: 200,
		Groups:      []TestGroup{{Name: "workers", Protocol: "grpc", VUsers: 200}},
		Assignment:  &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto},
	}
	rejected := &ManagedTest{
		ID:          "rejected",
		Status:      "QUEUED",
		TotalVUsers: 100,
		Groups:      []TestGroup{{Name: "publishers", Protocol: "jms", VUsers: 100}},
		Assignment:  &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{"gen-a", "gen-b"}},
		CapacityAdvisory: &CapacityAdvisory{
			RequiresOverride: true,
		},
	}

	status := buildLiveCapacityStatus(current, &LiveMetrics{ActiveVUsers: 1000}, []*ManagedTest{current, queued, rejected})
	if status == nil {
		t.Fatal("expected live capacity status")
	}
	if status.AllocatedVUsers != 1000 || status.AvailableGenerators != 3 || status.RecommendedGenerators != 4 || !status.Saturated {
		t.Fatalf("capacity status headline = %#v", status)
	}
	if status.QueuedVUsers != 300 || status.RejectedVUsers != 1300 {
		t.Fatalf("queued/rejected = %d/%d, want 300/1300", status.QueuedVUsers, status.RejectedVUsers)
	}
	if status.ByProtocol["http"].AllocatedVUsers != 900 || status.ByProtocol["browser"].AllocatedVUsers != 300 || status.ByProtocol["grpc"].QueuedVUsers != 200 || status.ByProtocol["jms"].RejectedVUsers != 100 {
		t.Fatalf("protocol breakdown = %#v", status.ByProtocol)
	}
	if status.ByRegion["east"].AllocatedVUsers != 800 || status.ByRegion["west"].AllocatedVUsers != 400 || status.ByRegion["auto"].QueuedVUsers != 200 {
		t.Fatalf("region breakdown = %#v", status.ByRegion)
	}
	if status.ByRegion["gen-a"].RejectedVUsers != 50 || status.ByRegion["gen-b"].RejectedVUsers != 50 {
		t.Fatalf("specific-host rejected breakdown = %#v", status.ByRegion)
	}
}

func TestBuildThresholdAlerts(t *testing.T) {
	p99Threshold := 750.0
	errorThreshold := 2.5
	serverThreshold := 200.0
	transferThreshold := 90.0
	timestamp := time.Date(2026, time.May, 10, 9, 0, 0, 0, time.UTC)

	alerts := buildThresholdAlerts(&ManagedTest{ID: "test-1", Name: "Checkout"}, &LiveMetrics{
		Timestamp:              timestamp,
		P99Ms:                  900,
		ErrorPct:               4.2,
		TPS:                    180,
		TargetTPS:              250,
		ThroughputGapPct:       28,
		ThroughputTargetMissed: true,
		Transactions: map[string]*TxMetrics{
			"POST /checkout": {
				Name:             "POST /checkout",
				DependencyType:   "soap",
				DependencyTarget: "checkout.example.com#SubmitOrder",
				ServerTelemetry: &ServerTelemetrySummary{
					Samples:               8,
					AvgServerProcessingMs: 245,
					AvgTransferMs:         110,
				},
			},
		},
	}, ThresholdSettings{
		MaxP99LatencyMs:          &p99Threshold,
		MaxErrorRatePct:          &errorThreshold,
		MaxAvgServerProcessingMs: &serverThreshold,
		MaxAvgTransferMs:         &transferThreshold,
	})

	if len(alerts) != 5 {
		t.Fatalf("expected 5 threshold alerts, got %d", len(alerts))
	}

	alertsByMetric := make(map[string]map[string]any, len(alerts))
	for _, alert := range alerts {
		metric, ok := alert["metric"].(string)
		if !ok {
			t.Fatalf("expected alert metric to be a string, got %#v", alert["metric"])
		}
		alertsByMetric[metric] = alert
	}

	p99Alert := alertsByMetric["p99_ms"]
	if p99Alert["threshold"] != p99Threshold {
		t.Fatalf("expected p99 threshold %v, got %#v", p99Threshold, p99Alert["threshold"])
	}
	if p99Alert["test_id"] != "test-1" || p99Alert["test_name"] != "Checkout" {
		t.Fatalf("unexpected p99 alert identity: %#v", p99Alert)
	}

	errorAlert := alertsByMetric["error_pct"]
	if errorAlert["threshold"] != errorThreshold {
		t.Fatalf("expected error threshold %v, got %#v", errorThreshold, errorAlert["threshold"])
	}
	if errorAlert["action"] != "stop_requested" {
		t.Fatalf("expected stop_requested action, got %#v", errorAlert["action"])
	}
	throughputAlert := alertsByMetric["throughput_gap_pct"]
	if throughputAlert["threshold"] != 250.0 {
		t.Fatalf("expected throughput target 250, got %#v", throughputAlert["threshold"])
	}
	if throughputAlert["value"] != 180.0 {
		t.Fatalf("expected actual throughput 180, got %#v", throughputAlert["value"])
	}
	if throughputAlert["gap_pct"] != 28.0 {
		t.Fatalf("expected throughput gap 28, got %#v", throughputAlert["gap_pct"])
	}
	serverAlert := alertsByMetric["avg_server_processing_ms"]
	if serverAlert["threshold"] != serverThreshold {
		t.Fatalf("expected server threshold %v, got %#v", serverThreshold, serverAlert["threshold"])
	}
	if serverAlert["transaction"] != "POST /checkout" {
		t.Fatalf("expected server alert to identify POST /checkout, got %#v", serverAlert["transaction"])
	}
	transferAlert := alertsByMetric["avg_transfer_ms"]
	if transferAlert["threshold"] != transferThreshold {
		t.Fatalf("expected transfer threshold %v, got %#v", transferThreshold, transferAlert["threshold"])
	}
	if !shouldStopForErrorThreshold(&LiveMetrics{ErrorPct: 4.2}, ThresholdSettings{MaxErrorRatePct: &errorThreshold}) {
		t.Fatal("expected error threshold helper to request stop")
	}
	if shouldStopForErrorThreshold(&LiveMetrics{ErrorPct: 1.0}, ThresholdSettings{MaxErrorRatePct: &errorThreshold}) {
		t.Fatal("did not expect stop below error threshold")
	}
}

func TestInlineCSVFromDataPoolRows(t *testing.T) {
	csvText, columns, err := inlineCSVFromDataPoolRows([]*storage.SharedDataPoolRow{{
		ID:     "row-1",
		PoolID: "pool-1",
		Data:   map[string]string{"zip": "10001", "customer_id": "C-100"},
	}, {
		ID:     "row-2",
		PoolID: "pool-1",
		Data:   map[string]string{"zip": "10002", "customer_id": "C-101"},
	}})
	if err != nil {
		t.Fatalf("inlineCSVFromDataPoolRows returned error: %v", err)
	}
	if strings.Join(columns, ",") != "customer_id,zip" {
		t.Fatalf("unexpected columns: %#v", columns)
	}
	want := "customer_id,zip\nC-100,10001\nC-101,10002\n"
	if csvText != want {
		t.Fatalf("unexpected csv:\n%s", csvText)
	}
}

func TestInlineCSVFromDataPoolRowsRequiresColumns(t *testing.T) {
	_, _, err := inlineCSVFromDataPoolRows([]*storage.SharedDataPoolRow{{ID: "row-1", PoolID: "pool-1"}})
	if err == nil {
		t.Fatal("expected empty data pool rows to fail")
	}
}

func TestNormalizeScenarioDataPoolBindingsAcceptsPopStrategy(t *testing.T) {
	bindings, err := normalizeScenarioDataPoolBindings("scenario-1", []storage.ScenarioDataPoolBinding{{
		PoolID:            "pool-1",
		Alias:             "customers",
		AccessMode:        "consume",
		SelectionStrategy: "pop",
	}})
	if err != nil {
		t.Fatalf("normalizeScenarioDataPoolBindings returned error: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("binding count = %d, want 1", len(bindings))
	}
	if bindings[0].SelectionStrategy != "pop" {
		t.Fatalf("selection strategy = %q, want pop", bindings[0].SelectionStrategy)
	}
}

func TestPublishLiveMetricsUpdatesStatePersistsHistoryAndBroadcasts(t *testing.T) {
	hub := NewHub()
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, hub, store)
	timestamp := time.Date(2026, time.May, 10, 10, 15, 0, 0, time.UTC)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	live := &LiveMetrics{Timestamp: timestamp, ActiveVUsers: 12, TotalTx: 44}

	runner.publishLiveMetrics(testRun, live, nil)

	if testRun.LiveMetrics != live {
		t.Fatal("expected live metrics pointer to be stored on test")
	}
	if len(testRun.History) != 1 || testRun.History[0] != live {
		t.Fatalf("expected live metrics to be appended to history, got %#v", testRun.History)
	}
	if store.saveHistoryCalls != 1 {
		t.Fatalf("expected SaveHistory to be called once, got %d", store.saveHistoryCalls)
	}
	if store.savedHistoryTestID != "test-1" || store.savedHistoryTimestamp != timestamp || store.savedHistoryMetrics != live {
		t.Fatalf("unexpected SaveHistory inputs: %+v %+v %#v", store.savedHistoryTestID, store.savedHistoryTimestamp, store.savedHistoryMetrics)
	}

	select {
	case msg := <-hub.broadcast:
		var payload struct {
			Type      string       `json:"type"`
			TestID    string       `json:"test_id"`
			TestName  string       `json:"test_name"`
			Timestamp time.Time    `json:"timestamp"`
			Data      *LiveMetrics `json:"data"`
		}
		if err := json.Unmarshal(msg, &payload); err != nil {
			t.Fatalf("failed to decode broadcast payload: %v", err)
		}
		if payload.Type != "metrics_update" || payload.TestID != "test-1" || payload.TestName != "Checkout" {
			t.Fatalf("unexpected broadcast identity: %#v", payload)
		}
		if payload.Timestamp != timestamp {
			t.Fatalf("expected timestamp %s, got %s", timestamp, payload.Timestamp)
		}
		if payload.Data == nil || payload.Data.ActiveVUsers != 12 || payload.Data.TotalTx != 44 {
			t.Fatalf("unexpected broadcast metrics payload: %#v", payload.Data)
		}
		if payload.Data.Telemetry == nil {
			t.Fatal("expected telemetry payload to be populated")
		}
		if payload.Data.Telemetry.HistoryPoints != 1 {
			t.Fatalf("expected history points = 1, got %#v", payload.Data.Telemetry)
		}
		if payload.Data.Telemetry.PublishedAt.IsZero() {
			t.Fatal("expected published timestamp in telemetry payload")
		}
	default:
		t.Fatal("expected metrics_update broadcast")
	}
}

func TestPublishLiveMetricsTrimsInMemoryHistory(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	testRun.History = make([]*LiveMetrics, 0, maxManagedTestHistory)
	for i := 0; i < maxManagedTestHistory; i++ {
		testRun.History = append(testRun.History, &LiveMetrics{Timestamp: time.Unix(int64(i), 0)})
	}

	newest := &LiveMetrics{Timestamp: time.Unix(999999, 0)}
	runner.publishLiveMetrics(testRun, newest, nil)

	if got := len(testRun.History); got != maxManagedTestHistory {
		t.Fatalf("history length = %d, want %d", got, maxManagedTestHistory)
	}
	if testRun.History[len(testRun.History)-1] != newest {
		t.Fatal("expected newest metrics to remain at tail of trimmed history")
	}
	if oldest := testRun.History[0].Timestamp; !oldest.Equal(time.Unix(1, 0)) {
		t.Fatalf("expected oldest retained timestamp %s, got %s", time.Unix(1, 0), oldest)
	}
	if newest.Telemetry == nil || newest.Telemetry.HistoryPoints != maxManagedTestHistory {
		t.Fatalf("expected telemetry history points = %d, got %#v", maxManagedTestHistory, newest.Telemetry)
	}
	select {
	case <-hub.broadcast:
	default:
		t.Fatal("expected metrics_update broadcast")
	}
}

func TestPublishLiveMetricsBroadcastsTelemetryDiagnosticOnDegradation(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	live := &LiveMetrics{
		Timestamp:    time.Now(),
		ActiveVUsers: 4,
		TotalTx:      10,
		Telemetry: &TelemetryHealth{
			DroppedResults: 3,
		},
	}

	runner.publishLiveMetrics(testRun, live, nil)

	var metricsPayload struct {
		Type string `json:"type"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &metricsPayload); err != nil {
			t.Fatalf("failed to decode metrics payload: %v", err)
		}
		if metricsPayload.Type != "metrics_update" {
			t.Fatalf("expected metrics_update first, got %#v", metricsPayload)
		}
	default:
		t.Fatal("expected metrics_update broadcast")
	}

	var diagnosticPayload struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &diagnosticPayload); err != nil {
			t.Fatalf("failed to decode diagnostic payload: %v", err)
		}
		if diagnosticPayload.Type != "telemetry_diagnostic" {
			t.Fatalf("expected telemetry_diagnostic second, got %#v", diagnosticPayload)
		}
		if diagnosticPayload.Data == nil || diagnosticPayload.Data.Severity != "danger" {
			t.Fatalf("expected danger severity, got %#v", diagnosticPayload.Data)
		}
		if !strings.Contains(diagnosticPayload.Data.Summary, "collector dropped 3 metric samples") {
			t.Fatalf("unexpected diagnostic summary: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Conditions) != 1 || diagnosticPayload.Data.Conditions[0] != "collector_drops" {
			t.Fatalf("unexpected diagnostic conditions: %#v", diagnosticPayload.Data)
		}
	default:
		t.Fatal("expected telemetry_diagnostic broadcast payload")
	}
}

func TestPublishLiveMetricsUsesConfiguredTelemetryBudgets(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	settings := runner.GetSystemSettings()
	sampleAgeBudget := 100.0
	generatorBudget := 50.0
	transportBudget := 60.0
	fanoutBudget := 70.0
	browserBudget := 80.0
	settings.Thresholds.MaxTelemetrySampleAgeMs = &sampleAgeBudget
	settings.Thresholds.MaxTelemetryGeneratorLagMs = &generatorBudget
	settings.Thresholds.MaxTelemetryTransportLagMs = &transportBudget
	settings.Thresholds.MaxTelemetryControllerFanoutMs = &fanoutBudget
	settings.Thresholds.MaxTelemetryBrowserRenderLagMs = &browserBudget
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	now := time.Now()
	testRun := &ManagedTest{ID: "test-1", Name: "Telemetry Budget"}
	live := &LiveMetrics{
		Timestamp: now,
		Telemetry: &TelemetryHealth{
			CollectedAt:              now.Add(-250 * time.Millisecond),
			ControllerReceivedAt:     now.Add(-200 * time.Millisecond),
			GeneratorProcessingLagMs: 75,
			TransportLagMs:           90,
			BrowserRenderLagMs:       120,
		},
	}

	runner.publishLiveMetrics(testRun, live, nil)

	select {
	case <-hub.broadcast:
	default:
		t.Fatal("expected metrics_update broadcast")
	}

	var diagnosticPayload struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &diagnosticPayload); err != nil {
			t.Fatalf("failed to decode diagnostic payload: %v", err)
		}
	default:
		t.Fatal("expected telemetry_diagnostic broadcast payload")
	}
	if diagnosticPayload.Type != "telemetry_diagnostic" {
		t.Fatalf("expected telemetry_diagnostic, got %#v", diagnosticPayload)
	}
	if diagnosticPayload.Data == nil || diagnosticPayload.Data.Severity != "warning" {
		t.Fatalf("expected warning diagnostic, got %#v", diagnosticPayload.Data)
	}
	expectedConditions := []string{"stale_sample", "generator_lag", "transport_lag", "controller_fanout_lag", "browser_render_lag"}
	if strings.Join(diagnosticPayload.Data.Conditions, ",") != strings.Join(expectedConditions, ",") {
		t.Fatalf("conditions = %#v, want %#v", diagnosticPayload.Data.Conditions, expectedConditions)
	}
	if !strings.Contains(diagnosticPayload.Data.Summary, "sample age") || !strings.Contains(diagnosticPayload.Data.Summary, "browser render lag") {
		t.Fatalf("unexpected diagnostic summary: %s", diagnosticPayload.Data.Summary)
	}
	if testRun.TelemetryHealth == nil || testRun.TelemetryHealth.Status != "degraded" || !testRun.TelemetryHealth.BreachActive {
		t.Fatalf("expected degraded run telemetry health, got %#v", testRun.TelemetryHealth)
	}
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.TelemetryStatus == nil || testRun.LiveMetrics.TelemetryStatus.Status != "degraded" {
		t.Fatalf("expected live metrics telemetry status, got %#v", testRun.LiveMetrics)
	}
}

func TestPublishLiveMetricsDispatchesTelemetryBreachNotifications(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.notifier = newNotificationDispatcher(server.Client())
	settings := runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{
		Webhooks: []NotificationWebhookSettings{{
			Name:    "Telemetry Alerts",
			Enabled: true,
			URL:     server.URL,
			Format:  notificationFormatSlack,
			Events:  []string{"telemetry_breached"},
		}},
	}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	testRun := &ManagedTest{ID: "test-1", Name: "Telemetry Breach", ProjectID: "project-7"}
	runner.publishLiveMetrics(testRun, &LiveMetrics{
		Timestamp: time.Now(),
		Telemetry: &TelemetryHealth{DroppedResults: 3},
	}, nil)

	messageTypes := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case msg := <-hub.broadcast:
			var envelope struct {
				Type      string              `json:"type"`
				EventType string              `json:"event_type"`
				Data      *TelemetryRunHealth `json:"data"`
			}
			if err := json.Unmarshal(msg, &envelope); err != nil {
				t.Fatalf("failed to decode broadcast payload: %v", err)
			}
			messageTypes = append(messageTypes, envelope.Type)
			if envelope.Type == "telemetry_health_changed" {
				if envelope.EventType != "telemetry_breached" || envelope.Data == nil || envelope.Data.Status != "breached" {
					t.Fatalf("unexpected telemetry health event: %#v", envelope)
				}
			}
		default:
			t.Fatalf("expected telemetry health broadcast, got %#v", messageTypes)
		}
	}
	if strings.Join(messageTypes, ",") != "metrics_update,telemetry_diagnostic,telemetry_health_changed" {
		t.Fatalf("message types = %#v, want metrics/diagnostic/health change", messageTypes)
	}

	select {
	case payload := <-received:
		text, _ := payload["text"].(string)
		if !strings.Contains(text, "Stormrunner Telemetry Breached") || !strings.Contains(text, "Telemetry Breach") {
			t.Fatalf("payload text = %#v", payload["text"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected telemetry breach webhook delivery")
	}
}

func TestUpdateTelemetryRunHealthTracksSustainedBreachAndRecovery(t *testing.T) {
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	state := telemetryDiagnosticState{}
	warningDiagnostic := &TelemetryDiagnostic{
		Severity:   "warning",
		Summary:    "sample age 250 ms exceeds the 100 ms telemetry budget",
		Conditions: []string{"stale_sample"},
	}

	first := updateTelemetryRunHealth(&state, warningDiagnostic, "stale_sample", now)
	if first == nil || first.Status != "degraded" || first.SustainedBreach || first.BreachDurationSec != 0 {
		t.Fatalf("first health = %#v, want degraded unsustained breach", first)
	}
	if first.BreachStartedAt == nil || !first.BreachStartedAt.Equal(now) {
		t.Fatalf("breach started at = %#v, want %s", first.BreachStartedAt, now)
	}

	sustainedAt := now.Add(telemetrySustainedBreachWindow + time.Second)
	sustained := updateTelemetryRunHealth(&state, warningDiagnostic, "stale_sample", sustainedAt)
	if sustained == nil || sustained.Status != "breached" || !sustained.SustainedBreach {
		t.Fatalf("sustained health = %#v, want sustained breach", sustained)
	}
	if sustained.BreachDurationSec != int((telemetrySustainedBreachWindow + time.Second).Seconds()) {
		t.Fatalf("breach duration = %d, want sustained window plus one second", sustained.BreachDurationSec)
	}
	if !sustained.LastTransitionAt.Equal(sustainedAt) {
		t.Fatalf("last transition at = %s, want %s", sustained.LastTransitionAt, sustainedAt)
	}

	recoveredAt := sustainedAt.Add(time.Second)
	recovered := updateTelemetryRunHealth(&state, &TelemetryDiagnostic{Severity: "healthy", Summary: "live telemetry healthy"}, "healthy", recoveredAt)
	if recovered == nil || recovered.Status != "healthy" || recovered.BreachActive || recovered.BreachStartedAt != nil {
		t.Fatalf("recovered health = %#v, want healthy with cleared breach", recovered)
	}
	if !recovered.LastTransitionAt.Equal(recoveredAt) {
		t.Fatalf("recovery transition at = %s, want %s", recovered.LastTransitionAt, recoveredAt)
	}
}

func TestUpdateTelemetryRunHealthBreachesImmediatelyOnDanger(t *testing.T) {
	now := time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC)
	health := updateTelemetryRunHealth(&telemetryDiagnosticState{}, &TelemetryDiagnostic{
		Severity:   "danger",
		Summary:    "collector dropped 3 metric samples",
		Conditions: []string{"collector_drops"},
	}, "collector_drops", now)

	if health == nil || health.Status != "breached" || health.SustainedBreach {
		t.Fatalf("health = %#v, want immediate unsustained breach for danger", health)
	}
	if health.BreachStartedAt == nil || !health.BreachStartedAt.Equal(now) {
		t.Fatalf("breach started at = %#v, want %s", health.BreachStartedAt, now)
	}
}

func TestPublishLiveMetricsBroadcastsSampledTransactionDiagnostics(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	live := &LiveMetrics{
		Timestamp: time.Now(),
		TotalTx:   18,
		Transactions: map[string]*TxMetrics{
			"Checkout": {
				Name:             "Checkout",
				DependencyType:   "http",
				DependencyTarget: "payments.example.test",
				CorrelationIDs:   []string{"corr-checkout-1"},
				CorrelationCount: 3,
				Count:            10,
				Errors:           3,
				AvgMs:            420,
				P99Ms:            880,
			},
			"Search": {
				Name:             "Search",
				DependencyType:   "http",
				DependencyTarget: "catalog.example.test",
				CorrelationIDs:   []string{"corr-search-1"},
				CorrelationCount: 1,
				Count:            8,
				AvgMs:            1225,
				P99Ms:            2400,
			},
		},
		Telemetry: &TelemetryHealth{},
	}

	runner.publishLiveMetrics(testRun, live, nil)

	var metricsPayload struct {
		Type string `json:"type"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &metricsPayload); err != nil {
			t.Fatalf("failed to decode metrics payload: %v", err)
		}
		if metricsPayload.Type != "metrics_update" {
			t.Fatalf("expected metrics_update first, got %#v", metricsPayload)
		}
	default:
		t.Fatal("expected metrics_update broadcast")
	}

	var diagnosticPayload struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &diagnosticPayload); err != nil {
			t.Fatalf("failed to decode diagnostic payload: %v", err)
		}
		if diagnosticPayload.Type != "telemetry_diagnostic" {
			t.Fatalf("expected telemetry_diagnostic second, got %#v", diagnosticPayload)
		}
		if diagnosticPayload.Data == nil || diagnosticPayload.Data.Severity != "danger" {
			t.Fatalf("expected danger severity, got %#v", diagnosticPayload.Data)
		}
		if !strings.Contains(diagnosticPayload.Data.Summary, "transaction error hotspots") || !strings.Contains(diagnosticPayload.Data.Summary, "slow transaction hotspots") {
			t.Fatalf("unexpected diagnostic summary: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Samples) != 2 {
			t.Fatalf("expected 2 diagnostic samples, got %#v", diagnosticPayload.Data)
		}
		if diagnosticPayload.Data.Samples[0].Transaction != "Checkout" || diagnosticPayload.Data.Samples[0].Issue != "error" {
			t.Fatalf("unexpected first diagnostic sample: %#v", diagnosticPayload.Data.Samples[0])
		}
		if diagnosticPayload.Data.Samples[1].Transaction != "Search" || diagnosticPayload.Data.Samples[1].DependencyTarget != "catalog.example.test" {
			t.Fatalf("unexpected second diagnostic sample: %#v", diagnosticPayload.Data.Samples[1])
		}
	default:
		t.Fatal("expected telemetry_diagnostic broadcast payload")
	}
}

func TestPublishLiveMetricsBroadcastsJMSBrokerPressureDiagnostic(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "JMS Checkout"}
	live := &LiveMetrics{
		Timestamp: time.Now(),
		TotalTx:   12,
		Transactions: map[string]*TxMetrics{
			"RequestReplyQuote": {
				Name:             "RequestReplyQuote",
				DependencyType:   "jms_queue",
				DependencyTarget: "quotes.rpc",
				CorrelationIDs:   []string{"corr-quote-1"},
				Count:            6,
				AvgMs:            320,
				P99Ms:            540,
				ServerTelemetry: &ServerTelemetrySummary{
					Samples:                   6,
					AvgMessageLatencyMs:       60,
					AvgConsumerLagMs:          280,
					AvgMessagesSentPerSec:     30,
					AvgMessagesReceivedPerSec: 12,
				},
			},
		},
		Telemetry: &TelemetryHealth{},
	}

	runner.publishLiveMetrics(testRun, live, nil)

	select {
	case <-hub.broadcast:
	default:
		t.Fatal("expected metrics_update broadcast")
	}

	var diagnosticPayload struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &diagnosticPayload); err != nil {
			t.Fatalf("failed to decode diagnostic payload: %v", err)
		}
		if diagnosticPayload.Type != "telemetry_diagnostic" {
			t.Fatalf("expected telemetry_diagnostic second, got %#v", diagnosticPayload)
		}
		if diagnosticPayload.Data == nil || diagnosticPayload.Data.Severity != "warning" {
			t.Fatalf("expected warning severity, got %#v", diagnosticPayload.Data)
		}
		if !strings.Contains(diagnosticPayload.Data.Summary, "JMS broker pressure hotspots") {
			t.Fatalf("unexpected diagnostic summary: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Conditions) != 1 || diagnosticPayload.Data.Conditions[0] != "jms_broker_pressure" {
			t.Fatalf("unexpected diagnostic conditions: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Samples) != 1 || diagnosticPayload.Data.Samples[0].Issue != "broker" {
			t.Fatalf("unexpected diagnostic samples: %#v", diagnosticPayload.Data)
		}
		if diagnosticPayload.Data.Samples[0].AvgConsumerLagMs != 280 || diagnosticPayload.Data.Samples[0].AvgMessagesSentPerSec != 30 {
			t.Fatalf("unexpected broker sample metrics: %#v", diagnosticPayload.Data.Samples[0])
		}
	default:
		t.Fatal("expected telemetry_diagnostic broadcast payload")
	}
}

func TestPublishLiveMetricsBroadcastsJMSDeadLetterDiagnostic(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "JMS DLQ Monitor"}
	live := &LiveMetrics{
		Timestamp: time.Now(),
		TotalTx:   7,
		Transactions: map[string]*TxMetrics{
			"ConsumeOrderDLQ": {
				Name:             "ConsumeOrderDLQ",
				DependencyType:   "jms_dead_letter_queue",
				DependencyTarget: "orders.dlq",
				CorrelationIDs:   []string{"corr-dlq-1"},
				Count:            7,
				AvgMs:            185,
				P99Ms:            300,
			},
		},
		Telemetry: &TelemetryHealth{},
	}

	runner.publishLiveMetrics(testRun, live, nil)

	select {
	case <-hub.broadcast:
	default:
		t.Fatal("expected metrics_update broadcast")
	}

	var diagnosticPayload struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &diagnosticPayload); err != nil {
			t.Fatalf("failed to decode diagnostic payload: %v", err)
		}
		if diagnosticPayload.Type != "telemetry_diagnostic" {
			t.Fatalf("expected telemetry_diagnostic second, got %#v", diagnosticPayload)
		}
		if diagnosticPayload.Data == nil || diagnosticPayload.Data.Severity != "warning" {
			t.Fatalf("expected warning severity, got %#v", diagnosticPayload.Data)
		}
		if !strings.Contains(diagnosticPayload.Data.Summary, "dead-letter destinations") {
			t.Fatalf("unexpected diagnostic summary: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Conditions) != 1 || diagnosticPayload.Data.Conditions[0] != "jms_dead_letter_activity" {
			t.Fatalf("unexpected diagnostic conditions: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Samples) != 1 || diagnosticPayload.Data.Samples[0].Issue != "dead_letter" {
			t.Fatalf("unexpected diagnostic samples: %#v", diagnosticPayload.Data)
		}
		if diagnosticPayload.Data.Samples[0].DependencyTarget != "orders.dlq" || diagnosticPayload.Data.Samples[0].Count != 7 {
			t.Fatalf("unexpected dead-letter sample metrics: %#v", diagnosticPayload.Data.Samples[0])
		}
	default:
		t.Fatal("expected telemetry_diagnostic broadcast payload")
	}
}

func TestPublishLiveMetricsSamplesTelemetryDiagnosticUntilStateChanges(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}

	runner.publishLiveMetrics(testRun, &LiveMetrics{
		Timestamp: time.Now().Add(-3 * time.Second),
		Telemetry: &TelemetryHealth{DroppedResults: 1},
	}, nil)
	runner.publishLiveMetrics(testRun, &LiveMetrics{
		Timestamp: time.Now().Add(-3 * time.Second),
		Telemetry: &TelemetryHealth{DroppedResults: 2},
	}, nil)
	runner.publishLiveMetrics(testRun, &LiveMetrics{
		Timestamp: time.Now(),
		Telemetry: &TelemetryHealth{},
	}, nil)

	messageTypes := make([]string, 0, 7)
	var recoveryDiagnostic struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	var recoveryHealth struct {
		Type string              `json:"type"`
		Data *TelemetryRunHealth `json:"data"`
	}
	for i := 0; i < 7; i++ {
		select {
		case msg := <-hub.broadcast:
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(msg, &envelope); err != nil {
				t.Fatalf("failed to decode diagnostic payload: %v", err)
			}
			messageTypes = append(messageTypes, envelope.Type)
			if envelope.Type == "telemetry_diagnostic" {
				if err := json.Unmarshal(msg, &recoveryDiagnostic); err != nil {
					t.Fatalf("failed to decode diagnostic payload: %v", err)
				}
			}
			if envelope.Type == "telemetry_health_changed" {
				if err := json.Unmarshal(msg, &recoveryHealth); err != nil {
					t.Fatalf("failed to decode telemetry health payload: %v", err)
				}
			}
		default:
			t.Fatalf("expected 7 broadcast messages, got %d", len(messageTypes))
		}
	}
	if len(messageTypes) != 7 {
		t.Fatalf("expected 7 broadcast messages, got %#v", messageTypes)
	}
	joined := strings.Join(messageTypes, ",")
	if joined != "metrics_update,telemetry_diagnostic,telemetry_health_changed,metrics_update,metrics_update,telemetry_diagnostic,telemetry_health_changed" {
		t.Fatalf("unexpected broadcast sequence: %s", joined)
	}
	if recoveryDiagnostic.Type != "telemetry_diagnostic" || recoveryDiagnostic.Data == nil || recoveryDiagnostic.Data.Severity != "healthy" {
		t.Fatalf("expected final recovery diagnostic, got %#v", recoveryDiagnostic)
	}
	if recoveryHealth.Type != "telemetry_health_changed" || recoveryHealth.Data == nil || recoveryHealth.Data.Status != "healthy" {
		t.Fatalf("expected final recovery health transition, got %#v", recoveryHealth)
	}
}

func TestLoadStoredTestHistoryTrimsHistoryWindow(t *testing.T) {
	store := &metricsStoreSpy{historyByTestID: map[string][]any{"test-1": make([]any, 0, maxManagedTestHistory+5)}}
	for i := 0; i < maxManagedTestHistory+5; i++ {
		store.historyByTestID["test-1"] = append(store.historyByTestID["test-1"], map[string]any{
			"timestamp": time.Unix(int64(i), 0),
		})
	}
	runner := NewTestRunner(2, NewHub(), store)
	testRun := &ManagedTest{ID: "test-1"}

	runner.loadStoredTestHistory(testRun)

	if got := len(testRun.History); got != maxManagedTestHistory {
		t.Fatalf("history length = %d, want %d", got, maxManagedTestHistory)
	}
	if first := testRun.History[0].Timestamp; !first.Equal(time.Unix(5, 0)) {
		t.Fatalf("expected trimmed first timestamp %s, got %s", time.Unix(5, 0), first)
	}
	if last := testRun.History[len(testRun.History)-1].Timestamp; !last.Equal(time.Unix(int64(maxManagedTestHistory+4), 0)) {
		t.Fatalf("unexpected last timestamp %s", last)
	}
}

func TestLoadStoredTestHistoryBuildsLiveMetricsFromTypedIntervalSummaries(t *testing.T) {
	store := &metricsStoreSpy{historyByTestID: map[string][]any{
		"test-1": {
			storage.IntervalSummaryRecord{
				Timestamp:     time.Date(2026, time.May, 13, 17, 15, 0, 0, time.UTC),
				ActiveVUsers:  9,
				TPS:           22.5,
				AvgResponseMs: 180.5,
				ErrorPct:      2.5,
				TotalTx:       90,
				TotalErrors:   2,
				P50Ms:         140,
				P90Ms:         260,
				P95Ms:         310,
				P99Ms:         400,
				Browser: &storage.BrowserMetricsSummaryRecord{
					Samples:              2,
					AvgFCPMs:             120,
					AvgLCPMs:             240,
					AvgCLSScore:          0.031,
					AvgINPMs:             55,
					AvgBlockingResources: 2,
					AvgLongTaskCount:     1.5,
					AvgJSHeapUsedMB:      18,
					AvgBrowserProcessCPU: 22,
					AvgBrowserProcessMem: 256,
				},
				BrowserByDevice: map[string]*storage.BrowserDeviceMetricsRecord{
					"iphone_se": {
						DeviceProfile:        "iphone_se",
						DevicePlatform:       "ios",
						DeviceTier:           "low",
						BrowserEngine:        "webkit",
						Samples:              2,
						AvgFCPMs:             120,
						AvgLCPMs:             240,
						AvgCLSScore:          0.031,
						AvgINPMs:             55,
						AvgBlockingResources: 2,
						AvgLongTaskCount:     1.5,
						AvgJSHeapUsedMB:      18,
						AvgBrowserProcessCPU: 22,
						AvgBrowserProcessMem: 256,
					},
				},
				DroppedResults:           5,
				WebsocketDroppedMessages: 2,
				CollectedAt:              time.Date(2026, time.May, 13, 17, 14, 59, 0, time.UTC),
				PublishedAt:              time.Date(2026, time.May, 13, 17, 15, 0, 0, time.UTC),
				SampleAgeMs:              1000,
			},
		},
	}}
	runner := NewTestRunner(2, NewHub(), store)
	testRun := &ManagedTest{ID: "test-1"}

	runner.loadStoredTestHistory(testRun)

	if len(testRun.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(testRun.History))
	}
	if testRun.LiveMetrics == nil {
		t.Fatal("expected live metrics to be set from typed history")
	}
	if testRun.LiveMetrics.ActiveVUsers != 9 || testRun.LiveMetrics.TotalTx != 90 || testRun.LiveMetrics.P95Ms != 310 {
		t.Fatalf("unexpected live metrics from typed history: %#v", testRun.LiveMetrics)
	}
	if testRun.LiveMetrics.Telemetry == nil || testRun.LiveMetrics.Telemetry.DroppedResults != 5 || testRun.LiveMetrics.Telemetry.WebsocketDroppedMessages != 2 {
		t.Fatalf("expected telemetry to round-trip from typed history, got %#v", testRun.LiveMetrics.Telemetry)
	}
	if testRun.LiveMetrics.Browser == nil || testRun.LiveMetrics.Browser.AvgCLSScore != 0.031 {
		t.Fatalf("expected browser summary to round-trip from typed history, got %#v", testRun.LiveMetrics.Browser)
	}
	if testRun.LiveMetrics.Browser.AvgBrowserProcessCPU != 22 || testRun.LiveMetrics.Browser.AvgBrowserProcessMem != 256 {
		t.Fatalf("expected browser process metrics to round-trip from typed history, got %#v", testRun.LiveMetrics.Browser)
	}
	if testRun.LiveMetrics.BrowserByDevice == nil || testRun.LiveMetrics.BrowserByDevice["iphone_se"] == nil || testRun.LiveMetrics.BrowserByDevice["iphone_se"].DeviceTier != "low" {
		t.Fatalf("expected browser-by-device summary to round-trip from typed history, got %#v", testRun.LiveMetrics.BrowserByDevice)
	}
	if testRun.LiveMetrics.BrowserByDevice["iphone_se"].BrowserEngine != "webkit" {
		t.Fatalf("expected browser engine to round-trip from typed history, got %#v", testRun.LiveMetrics.BrowserByDevice["iphone_se"])
	}
	if testRun.LiveMetrics.BrowserByDevice["iphone_se"].DevicePlatform != "ios" {
		t.Fatalf("expected device platform to round-trip from typed history, got %#v", testRun.LiveMetrics.BrowserByDevice["iphone_se"])
	}
}

func TestBuildLiveMetricsForBroadcastOverridesTimestamp(t *testing.T) {
	snapshotTime := time.Date(2026, time.May, 10, 10, 15, 0, 0, time.UTC)
	broadcastTime := snapshotTime.Add(4 * time.Second)

	live := buildLiveMetricsForBroadcast(engine.MetricsSnapshot{
		Timestamp:    snapshotTime,
		ActiveVUsers: 7,
		TPS:          3.5,
		AvgDuration:  125 * time.Millisecond,
	}, map[string]int{"buyers": 7}, broadcastTime)

	if live.Timestamp != broadcastTime {
		t.Fatalf("expected broadcast timestamp %s, got %s", broadcastTime, live.Timestamp)
	}
	if live.ActiveVUsers != 7 {
		t.Fatalf("active vusers = %d, want 7", live.ActiveVUsers)
	}
	if live.TPS != 3.5 {
		t.Fatalf("TPS = %v, want 3.5", live.TPS)
	}
	if live.AvgResponseMs != 125 {
		t.Fatalf("avg response ms = %v, want 125", live.AvgResponseMs)
	}
	if live.Telemetry == nil {
		t.Fatal("expected telemetry to be initialized")
	}
	if !live.Telemetry.CollectedAt.Equal(snapshotTime) {
		t.Fatalf("expected collected at %s, got %s", snapshotTime, live.Telemetry.CollectedAt)
	}
	if !live.Telemetry.PublishedAt.Equal(broadcastTime) {
		t.Fatalf("expected published at %s, got %s", broadcastTime, live.Telemetry.PublishedAt)
	}
	if live.Telemetry.SampleAgeMs != 4000 {
		t.Fatalf("expected sample age ms 4000, got %v", live.Telemetry.SampleAgeMs)
	}
}

func TestApplyGoalControllerUpdateScalesGroupsAndUpdatesStatus(t *testing.T) {
	timestamp := time.Date(2026, time.May, 10, 11, 0, 0, 0, time.UTC)
	testRun := &ManagedTest{
		ID: "test-1",
		Groups: []TestGroup{
			{Name: "buyers", VUsers: 20},
			{Name: "admins", VUsers: 10},
		},
		goalControl: &goalControllerState{},
	}
	live := &LiveMetrics{Timestamp: timestamp}
	groupVUsers := map[string]int{"buyers": 20, "admins": 10}
	update := goalControllerUpdate{
		Status: &GoalStatus{
			CurrentTotalVUsers:   30,
			SuggestedTotalVUsers: 45,
		},
		ShouldScale:    true,
		TargetTotal:    45,
		ScaleDirection: "up",
		ScaleReason:    "tps below target",
	}

	var scaled []struct {
		group  string
		target int
	}
	applyGoalControllerUpdate(testRun, live, groupVUsers, update, func(groupName string, target int) error {
		scaled = append(scaled, struct {
			group  string
			target int
		}{group: groupName, target: target})
		return nil
	})

	if len(scaled) != 2 {
		t.Fatalf("expected 2 scale operations, got %d", len(scaled))
	}
	if totalGroupVUsers(groupVUsers) != 45 {
		t.Fatalf("expected scaled group total 45, got %d", totalGroupVUsers(groupVUsers))
	}
	if testRun.Groups[0].VUsers != groupVUsers["buyers"] || testRun.Groups[1].VUsers != groupVUsers["admins"] {
		t.Fatalf("expected group config vusers to track scaled map, got %+v vs %+v", testRun.Groups, groupVUsers)
	}
	if testRun.goalControl.lastScaleAt == nil || !testRun.goalControl.lastScaleAt.Equal(timestamp) {
		t.Fatalf("expected goal control lastScaleAt %s, got %#v", timestamp, testRun.goalControl.lastScaleAt)
	}
	if testRun.goalControl.lastScaleDirection != "up" || testRun.goalControl.lastScaleReason != "tps below target" {
		t.Fatalf("unexpected goal control metadata: %+v", testRun.goalControl)
	}
	if live.GoalStatus == nil {
		t.Fatal("expected goal status to be preserved")
	}
	if live.GoalStatus.LastScaleAt == nil || !live.GoalStatus.LastScaleAt.Equal(timestamp) {
		t.Fatalf("expected live goal status timestamp %s, got %#v", timestamp, live.GoalStatus.LastScaleAt)
	}
	if live.GoalStatus.LastScaleDirection != "up" || live.GoalStatus.LastScaleReason != "tps below target" {
		t.Fatalf("unexpected live goal status metadata: %+v", live.GoalStatus)
	}
	if live.GoalStatus.CurrentTotalVUsers != 45 || live.GoalStatus.SuggestedTotalVUsers != 45 {
		t.Fatalf("expected goal status totals to settle at 45, got %+v", live.GoalStatus)
	}
}

func TestRequestStopIfRunningTransitionsAndBroadcasts(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING", cancel: func() {}}
	runner.tests[testRun.ID] = testRun

	runner.requestStopIfRunning(testRun, "threshold stop failed")

	if testRun.Status != "STOPPING" {
		t.Fatalf("expected STOPPING status, got %q", testRun.Status)
	}

	select {
	case msg := <-hub.broadcast:
		var payload struct {
			Type   string `json:"type"`
			TestID string `json:"test_id"`
		}
		if err := json.Unmarshal(msg, &payload); err != nil {
			t.Fatalf("failed to decode broadcast payload: %v", err)
		}
		if payload.Type != "test_stopping" || payload.TestID != "test-1" {
			t.Fatalf("unexpected stop payload: %#v", payload)
		}
	default:
		t.Fatal("expected test_stopping broadcast")
	}
}

func TestRequestStopIfRunningIgnoresNonRunningTests(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "COMPLETE"}
	runner.tests[testRun.ID] = testRun

	runner.requestStopIfRunning(testRun, "goal-oriented stop failed")

	if testRun.Status != "COMPLETE" {
		t.Fatalf("expected COMPLETE status to remain unchanged, got %q", testRun.Status)
	}
	select {
	case msg := <-hub.broadcast:
		t.Fatalf("did not expect broadcast for non-running test, got %s", string(msg))
	default:
	}
	if shouldStopForGoalStatus(&LiveMetrics{}) {
		t.Fatal("did not expect empty live metrics to request goal stop")
	}
	if !shouldStopForGoalStatus(&LiveMetrics{GoalStatus: &GoalStatus{StopTriggered: true}}) {
		t.Fatal("expected goal stop helper to detect triggered status")
	}
}

func TestScriptAndTestCrudPersistThroughStoreHelpers(t *testing.T) {
	hub := NewHub()
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, hub, store)

	script := &dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
	}
	runner.StoreScript(script)
	if len(store.savedScripts) != 1 || store.savedScripts[0] != script {
		t.Fatalf("expected initial SaveScript call with stored script, got %#v", store.savedScripts)
	}

	updatedScript := &dsl.Script{
		ID:       "script-1",
		Name:     "Checkout v2",
		Protocol: "http",
		Version:  "1.1.0",
	}
	if _, ok := runner.UpdateScript("script-1", updatedScript); !ok {
		t.Fatal("expected UpdateScript to succeed")
	}
	if len(store.savedScripts) != 2 || store.savedScripts[1] != updatedScript {
		t.Fatalf("expected SaveScript to be called for update, got %#v", store.savedScripts)
	}
	if !runner.DeleteScript("script-1") {
		t.Fatal("expected DeleteScript to succeed")
	}
	if len(store.deletedScriptIDs) != 1 || store.deletedScriptIDs[0] != "script-1" {
		t.Fatalf("expected DeleteScript store call for script-1, got %#v", store.deletedScriptIDs)
	}

	runner.StoreScript(&dsl.Script{
		ID:       "script-2",
		Name:     "Search",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
		},
	})
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Queued Test",
		Duration: "30s",
		ScriptID: "script-2",
		VUsers:   5,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if len(store.savedTests) != 1 {
		t.Fatalf("expected one SaveTest call, got %d", len(store.savedTests))
	}
	if store.savedTests[0].ID != testRun.ID || store.savedTests[0].Status != "QUEUED" {
		t.Fatalf("unexpected saved test record: %#v", store.savedTests[0])
	}
	if !runner.DeleteTest(testRun.ID) {
		t.Fatal("expected DeleteTest to succeed for queued test")
	}
	if len(store.deletedTestIDs) != 1 || store.deletedTestIDs[0] != testRun.ID {
		t.Fatalf("expected DeleteTest store call for %s, got %#v", testRun.ID, store.deletedTestIDs)
	}
}

func TestNewTestRunnerLoadsPersistedScriptsAndTests(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"test_id":"test-1"}`), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	archive := &storage.RunArchiveRecord{TestID: "test-1", ArchivePath: archiveDir, ManifestPath: manifestPath}
	store := &metricsStoreSpy{
		listedScripts: []*dsl.Script{{
			ID:        "script-1",
			Name:      "Checkout",
			Protocol:  "http",
			Version:   "1.0.0",
			CreatedAt: time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC),
		}},
		listedTests: []*storage.TestRecord{{
			ID:               "test-1",
			Name:             "Recovered Run",
			Status:           "COMPLETE",
			ScenarioMode:     "goal_oriented",
			ScriptShakeout:   true,
			ResourceBaseline: true,
			TotalVUsers:      25,
			Duration:         int64(45 * time.Second),
			StartTime:        time.Date(2026, time.May, 10, 12, 1, 0, 0, time.UTC),
			Groups: []TestGroup{{
				Name:       "buyers",
				ScriptID:   "script-1",
				ScriptName: "Checkout",
				Protocol:   "http",
				VUsers:     25,
			}},
		}},
		historyByTestID: map[string][]any{
			"test-1": {map[string]any{
				"timestamp":          time.Date(2026, time.May, 10, 12, 1, 30, 0, time.UTC),
				"active_vusers":      float64(20),
				"tps":                float64(12.5),
				"avg_response_ms":    float64(150),
				"error_pct":          float64(0),
				"total_transactions": float64(100),
				"total_errors":       float64(0),
			}},
		},
		archiveByTestID: map[string]*storage.RunArchiveRecord{"test-1": archive},
	}

	runner := NewTestRunner(2, NewHub(), store)

	script, ok := runner.GetScript("script-1")
	if !ok || script.Name != "Checkout" {
		t.Fatalf("expected persisted script to be loaded, got %#v ok=%v", script, ok)
	}

	testRun, ok := runner.GetTest("test-1")
	if !ok {
		t.Fatal("expected persisted test to be loaded")
	}
	if testRun.Name != "Recovered Run" || testRun.Status != "COMPLETE" {
		t.Fatalf("unexpected loaded test metadata: %#v", testRun)
	}
	if len(testRun.History) != 1 || testRun.LiveMetrics == nil {
		t.Fatalf("expected persisted history to be loaded, got history=%#v live=%#v", testRun.History, testRun.LiveMetrics)
	}
	if testRun.Archive != archive {
		t.Fatalf("expected persisted archive to be attached, got %#v", testRun.Archive)
	}
	if cachedArchive, ok := runner.GetRunArchive("test-1"); !ok || cachedArchive != archive {
		t.Fatalf("expected archive cache lookup to return persisted archive, got %#v ok=%v", cachedArchive, ok)
	}
}

func TestNewTestRunnerSkipsPersistedArchiveWhenManifestMissing(t *testing.T) {
	archiveDir := t.TempDir()
	archive := &storage.RunArchiveRecord{
		TestID:       "test-1",
		ArchivePath:  archiveDir,
		ManifestPath: filepath.Join(archiveDir, "manifest.json"),
	}
	store := &metricsStoreSpy{
		listedTests: []*storage.TestRecord{{
			ID:          "test-1",
			Name:        "Recovered Run",
			Status:      "COMPLETE",
			TotalVUsers: 1,
		}},
		archiveByTestID: map[string]*storage.RunArchiveRecord{"test-1": archive},
	}

	runner := NewTestRunner(2, NewHub(), store)

	testRun, ok := runner.GetTest("test-1")
	if !ok {
		t.Fatal("expected persisted test to be loaded")
	}
	if testRun.Archive != nil {
		t.Fatalf("expected missing manifest archive to be skipped, got %#v", testRun.Archive)
	}
	if cachedArchive, ok := runner.GetRunArchive("test-1"); ok || cachedArchive != nil {
		t.Fatalf("expected GetRunArchive to report missing archive, got %#v ok=%v", cachedArchive, ok)
	}
}

func TestBroadcastEventDoesNotDeadlockWhenHoldingRunnerLock(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	done := make(chan struct{})

	go func() {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		runner.broadcastEvent("test_created", &ManagedTest{
			ID:        "test-1",
			Name:      "Checkout",
			Status:    "QUEUED",
			ProjectID: storage.DefaultProjectID,
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcastEvent deadlocked while runner lock was held")
	}
}

func TestProcessMetricsSnapshotBroadcastsAlertAndStopRequest(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	maxErrorPct := 5.0
	runner.settings.Thresholds.MaxErrorRatePct = &maxErrorPct

	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING"}
	testRun.cancel = func() {}
	runner.tests[testRun.ID] = testRun

	runner.processMetricsSnapshot(testRun, engine.MetricsSnapshot{
		Timestamp:    time.Date(2026, time.May, 10, 13, 0, 0, 0, time.UTC),
		ActiveVUsers: 10,
		Transactions: 20,
		Errors:       2,
		TPS:          15,
		AvgDuration:  200 * time.Millisecond,
	}, map[string]int{}, func(string, int) error { return nil }, nil)

	if testRun.Status != "STOPPING" {
		t.Fatalf("expected test status STOPPING, got %q", testRun.Status)
	}
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.ErrorPct != 10 {
		t.Fatalf("expected live metrics with 10%% error rate, got %#v", testRun.LiveMetrics)
	}

	messageTypes := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		select {
		case msg := <-hub.broadcast:
			var payload struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(msg, &payload); err != nil {
				t.Fatalf("failed to decode broadcast payload: %v", err)
			}
			messageTypes = append(messageTypes, payload.Type)
		default:
			t.Fatalf("expected 5 broadcast messages, got %d", len(messageTypes))
		}
	}

	if messageTypes[0] != "metrics_update" || messageTypes[1] != "telemetry_diagnostic" || messageTypes[2] != "telemetry_health_changed" || messageTypes[3] != "threshold_alert" || messageTypes[4] != "test_stopping" {
		t.Fatalf("unexpected broadcast order: %#v", messageTypes)
	}
}

func TestEvaluateGoalControllerPlansScaleUpWithinGuardrails(t *testing.T) {
	targetTPS := 300.0
	minVUsers := 10
	maxVUsers := 120
	now := time.Date(2026, time.May, 9, 12, 0, 0, 0, time.UTC)

	update := evaluateGoalController(now, 40, &LiveMetrics{
		TPS:      180,
		P95Ms:    320,
		ErrorPct: 0.4,
	}, &GoalConfig{
		TargetAverageTPS: &targetTPS,
		MinVUsers:        &minVUsers,
		MaxVUsers:        &maxVUsers,
	}, &goalControllerState{})

	if !update.ShouldScale {
		t.Fatal("expected goal controller to request scaling")
	}
	if update.TargetTotal <= 40 {
		t.Fatalf("expected target total to increase above 40, got %d", update.TargetTotal)
	}
	if update.TargetTotal > maxVUsers {
		t.Fatalf("expected target total to respect max guardrail %d, got %d", maxVUsers, update.TargetTotal)
	}
	if update.ScaleDirection != "up" {
		t.Fatalf("expected scale direction up, got %q", update.ScaleDirection)
	}
	if update.Status == nil || update.Status.SuggestedTotalVUsers != update.TargetTotal {
		t.Fatalf("expected suggested total to match target total, got %#v", update.Status)
	}
	if update.ShouldStop {
		t.Fatal("did not expect SLA stop for healthy latency and error metrics")
	}
}

func TestEvaluateGoalControllerStopsOnSustainedSLABreach(t *testing.T) {
	p95LatencyMs := 900.0
	maxErrorPct := 2.0
	now := time.Date(2026, time.May, 9, 12, 0, 45, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 55, &LiveMetrics{
		TPS:      260,
		P95Ms:    1200,
		ErrorPct: 3.2,
	}, &GoalConfig{
		P95LatencyMs:    &p95LatencyMs,
		MaxErrorPct:     &maxErrorPct,
		StopOnSLABreach: true,
		SLABreachWindow: "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop {
		t.Fatal("expected sustained SLA breach to trigger stop")
	}
	if update.StopReason == "" {
		t.Fatal("expected stop reason to be populated")
	}
	if update.Status == nil || !update.Status.StopTriggered {
		t.Fatalf("expected goal status to mark stop triggered, got %#v", update.Status)
	}
	if !update.Status.LatencyBreached || !update.Status.ErrorBreached {
		t.Fatalf("expected both SLA breach flags to be true, got %#v", update.Status)
	}
	if update.ShouldScale {
		t.Fatal("did not expect scaling decision to take precedence over stop")
	}
}

func TestEvaluateGoalControllerStopsOnServerTelemetrySLABreach(t *testing.T) {
	maxServerProcessingMs := 300.0
	maxTransferMs := 100.0
	now := time.Date(2026, time.May, 9, 12, 0, 45, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 55, &LiveMetrics{
		TPS: 260,
		Transactions: map[string]*TxMetrics{
			"POST /checkout": {
				Name: "POST /checkout",
				ServerTelemetry: &ServerTelemetrySummary{
					AvgServerProcessingMs: 420,
					AvgTransferMs:         140,
				},
			},
		},
	}, &GoalConfig{
		MaxAvgServerProcessingMs: &maxServerProcessingMs,
		MaxAvgTransferMs:         &maxTransferMs,
		StopOnSLABreach:          true,
		SLABreachWindow:          "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop {
		t.Fatal("expected sustained server telemetry SLA breach to trigger stop")
	}
	if update.Status == nil || !update.Status.ServerProcessingBreached || !update.Status.TransferBreached {
		t.Fatalf("expected goal status to mark server telemetry breaches, got %#v", update.Status)
	}
	if update.StopReason != "server processing and transfer SLA breached" {
		t.Fatalf("expected server telemetry stop reason, got %q", update.StopReason)
	}
}

func TestGetTestIncludesPreviousRunComparisonBaseline(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	baselineCompletedAt := time.Date(2026, time.May, 10, 10, 0, 0, 0, time.UTC)
	runner.tests["baseline"] = &ManagedTest{
		ID:         "baseline",
		Name:       "Checkout Run",
		Status:     "COMPLETE",
		ScenarioID: "scenario-1",
		CreatedAt:  baselineCompletedAt.Add(-5 * time.Minute),
		EndedAt:    &baselineCompletedAt,
		History: []*LiveMetrics{{
			Timestamp: baselineCompletedAt,
			Transactions: map[string]*TxMetrics{
				"POST /checkout": {
					Name: "POST /checkout",
					ServerTelemetry: &ServerTelemetrySummary{
						AvgServerProcessingMs: 220,
						AvgTransferMs:         85,
					},
				},
			},
			DependencyHealth: map[string]*TxMetrics{
				"api.example.test": {
					Name:             "api.example.test",
					DependencyType:   "http",
					DependencyTarget: "api.example.test",
					Count:            12,
					Errors:           1,
					AvgMs:            180,
					P50Ms:            150,
					P90Ms:            240,
					P95Ms:            300,
					P99Ms:            360,
				},
			},
		}},
	}
	currentCreatedAt := baselineCompletedAt.Add(10 * time.Minute)
	runner.tests["current"] = &ManagedTest{
		ID:         "current",
		Name:       "Checkout Run",
		Status:     "RUNNING",
		ScenarioID: "scenario-1",
		CreatedAt:  currentCreatedAt,
	}

	testRun, ok := runner.GetTest("current")
	if !ok {
		t.Fatal("expected current test to be found")
	}
	if testRun.ComparisonBaseline == nil {
		t.Fatal("expected comparison baseline to be populated")
	}
	if testRun.ComparisonBaseline.SourceTestID != "baseline" {
		t.Fatalf("expected baseline source test id baseline, got %q", testRun.ComparisonBaseline.SourceTestID)
	}
	if testRun.ComparisonBaseline.Policy != "latest_successful" {
		t.Fatalf("expected latest_successful baseline policy, got %q", testRun.ComparisonBaseline.Policy)
	}
	if testRun.ComparisonBaseline.Transactions["POST /checkout"] == nil {
		t.Fatal("expected baseline transaction metrics to be available")
	}
	if testRun.ComparisonBaseline.DependencyHealth["api.example.test"] == nil {
		t.Fatal("expected baseline dependency health metrics to be available")
	}
}

func TestGetTestPrefersPinnedComparisonBaseline(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	olderCompletedAt := time.Date(2026, time.May, 10, 9, 0, 0, 0, time.UTC)
	newerCompletedAt := olderCompletedAt.Add(30 * time.Minute)
	pinnedAt := newerCompletedAt.Add(5 * time.Minute)
	runner.tests["baseline-pinned"] = &ManagedTest{
		ID:               "baseline-pinned",
		Name:             "Checkout Run",
		Status:           "COMPLETE",
		ScenarioID:       "scenario-1",
		CreatedAt:        olderCompletedAt.Add(-5 * time.Minute),
		EndedAt:          &olderCompletedAt,
		BaselinePinned:   true,
		BaselinePinnedAt: &pinnedAt,
		History: []*LiveMetrics{{
			Timestamp: olderCompletedAt,
			Transactions: map[string]*TxMetrics{
				"POST /checkout": {Name: "POST /checkout", Count: 10, AvgMs: 150, P50Ms: 130, P90Ms: 180, P99Ms: 220},
			},
		}},
	}
	runner.tests["baseline-latest"] = &ManagedTest{
		ID:         "baseline-latest",
		Name:       "Checkout Run",
		Status:     "COMPLETE",
		ScenarioID: "scenario-1",
		CreatedAt:  newerCompletedAt.Add(-5 * time.Minute),
		EndedAt:    &newerCompletedAt,
		History: []*LiveMetrics{{
			Timestamp: newerCompletedAt,
			Transactions: map[string]*TxMetrics{
				"POST /checkout": {Name: "POST /checkout", Count: 10, AvgMs: 120, P50Ms: 100, P90Ms: 140, P99Ms: 180},
			},
		}},
	}
	runner.tests["current"] = &ManagedTest{
		ID:         "current",
		Name:       "Checkout Run",
		Status:     "RUNNING",
		ScenarioID: "scenario-1",
		CreatedAt:  newerCompletedAt.Add(10 * time.Minute),
	}

	testRun, ok := runner.GetTest("current")
	if !ok {
		t.Fatal("expected current test to be found")
	}
	if testRun.ComparisonBaseline == nil {
		t.Fatal("expected comparison baseline to be populated")
	}
	if testRun.ComparisonBaseline.SourceTestID != "baseline-pinned" {
		t.Fatalf("expected pinned baseline to win, got %q", testRun.ComparisonBaseline.SourceTestID)
	}
	if testRun.ComparisonBaseline.Policy != "pinned" {
		t.Fatalf("expected pinned baseline policy, got %q", testRun.ComparisonBaseline.Policy)
	}
}

func TestGetTestPrefersLatestSuccessfulComparisonBaselineOverFailedRun(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	successCompletedAt := time.Date(2026, time.May, 10, 9, 0, 0, 0, time.UTC)
	failedCompletedAt := successCompletedAt.Add(30 * time.Minute)
	runner.tests["baseline-success"] = &ManagedTest{
		ID:         "baseline-success",
		Name:       "Checkout Run",
		Status:     "COMPLETE",
		ScenarioID: "scenario-1",
		CreatedAt:  successCompletedAt.Add(-5 * time.Minute),
		EndedAt:    &successCompletedAt,
		History: []*LiveMetrics{{
			Timestamp: successCompletedAt,
			Transactions: map[string]*TxMetrics{
				"POST /checkout": {Name: "POST /checkout", Count: 10, AvgMs: 120},
			},
		}},
	}
	runner.tests["baseline-failed"] = &ManagedTest{
		ID:         "baseline-failed",
		Name:       "Checkout Run",
		Status:     "FAILED",
		ScenarioID: "scenario-1",
		CreatedAt:  failedCompletedAt.Add(-5 * time.Minute),
		EndedAt:    &failedCompletedAt,
		History: []*LiveMetrics{{
			Timestamp: failedCompletedAt,
			Transactions: map[string]*TxMetrics{
				"POST /checkout": {Name: "POST /checkout", Count: 10, AvgMs: 250},
			},
		}},
	}
	runner.tests["current"] = &ManagedTest{
		ID:         "current",
		Name:       "Checkout Run",
		Status:     "RUNNING",
		ScenarioID: "scenario-1",
		CreatedAt:  failedCompletedAt.Add(10 * time.Minute),
	}

	testRun, ok := runner.GetTest("current")
	if !ok {
		t.Fatal("expected current test to be found")
	}
	if testRun.ComparisonBaseline == nil {
		t.Fatal("expected comparison baseline to be populated")
	}
	if testRun.ComparisonBaseline.SourceTestID != "baseline-success" {
		t.Fatalf("expected latest successful baseline to win, got %q", testRun.ComparisonBaseline.SourceTestID)
	}
	if testRun.ComparisonBaseline.Policy != "latest_successful" {
		t.Fatalf("expected latest_successful baseline policy, got %q", testRun.ComparisonBaseline.Policy)
	}
}

func TestPinComparisonBaselineUnpinsComparableRuns(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	baselineCompletedAt := time.Date(2026, time.May, 10, 10, 0, 0, 0, time.UTC)
	otherPinnedAt := baselineCompletedAt.Add(-time.Minute)
	failedEndedAt := baselineCompletedAt.Add(10 * time.Minute)
	runner.tests["baseline-a"] = &ManagedTest{ID: "baseline-a", Name: "Checkout Run", Status: "COMPLETE", ScenarioID: "scenario-1", CreatedAt: baselineCompletedAt.Add(-5 * time.Minute), EndedAt: &baselineCompletedAt}
	runner.tests["baseline-b"] = &ManagedTest{ID: "baseline-b", Name: "Checkout Run", Status: "FAILED", ScenarioID: "scenario-1", CreatedAt: baselineCompletedAt.Add(5 * time.Minute), EndedAt: &failedEndedAt, BaselinePinned: true, BaselinePinnedAt: &otherPinnedAt}
	runner.tests["other-scope"] = &ManagedTest{ID: "other-scope", Name: "Search Run", Status: "COMPLETE", ScenarioID: "scenario-2", CreatedAt: baselineCompletedAt, EndedAt: &baselineCompletedAt, BaselinePinned: true, BaselinePinnedAt: &otherPinnedAt}

	pinned, err := runner.PinComparisonBaseline("baseline-a")
	if err != nil {
		t.Fatalf("PinComparisonBaseline returned error: %v", err)
	}
	if !pinned.BaselinePinned || pinned.BaselinePinnedAt == nil {
		t.Fatalf("expected pinned baseline metadata, got %#v", pinned)
	}
	if !runner.tests["baseline-a"].BaselinePinned {
		t.Fatal("expected target baseline to remain pinned")
	}
	if runner.tests["baseline-b"].BaselinePinned || runner.tests["baseline-b"].BaselinePinnedAt != nil {
		t.Fatalf("expected comparable baseline to be unpinned, got %#v", runner.tests["baseline-b"])
	}
	if !runner.tests["other-scope"].BaselinePinned {
		t.Fatal("expected other scope baseline pin to remain intact")
	}
	if len(store.savedTests) != 2 {
		t.Fatalf("expected comparable runs to be persisted, got %d saves", len(store.savedTests))
	}

	updated, err := runner.UnpinComparisonBaseline("baseline-a")
	if err != nil {
		t.Fatalf("UnpinComparisonBaseline returned error: %v", err)
	}
	if updated.BaselinePinned || updated.BaselinePinnedAt != nil {
		t.Fatalf("expected baseline to be unpinned, got %#v", updated)
	}
}

func TestEvaluateGoalControllerStopsOnMaxErrorCount(t *testing.T) {
	maxErrorCount := 10
	now := time.Date(2026, time.May, 9, 12, 1, 0, 0, time.UTC)

	update := evaluateGoalController(now, 40, &LiveMetrics{
		TPS:         210,
		P95Ms:       300,
		ErrorPct:    0.5,
		TotalErrors: 11,
	}, &GoalConfig{
		MaxErrorCount:   &maxErrorCount,
		StopOnMaxErrors: true,
	}, &goalControllerState{})

	if !update.ShouldStop {
		t.Fatal("expected max error count breach to trigger stop")
	}
	if update.StopReason != "max error count reached" {
		t.Fatalf("expected max error count stop reason, got %q", update.StopReason)
	}
	if update.Status == nil || !update.Status.ErrorCountBreached {
		t.Fatalf("expected goal status to mark error-count breach, got %#v", update.Status)
	}
	if update.ShouldScale {
		t.Fatal("did not expect scaling when hard stop threshold is reached")
	}
}

func TestEvaluateGoalControllerStopTakesPrecedenceOverScaling(t *testing.T) {
	targetTPS := 300.0
	p95LatencyMs := 900.0
	maxErrorPct := 2.0
	now := time.Date(2026, time.May, 9, 12, 2, 0, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 40, &LiveMetrics{
		TPS:      150,
		P95Ms:    1200,
		ErrorPct: 3.2,
	}, &GoalConfig{
		TargetAverageTPS: &targetTPS,
		P95LatencyMs:     &p95LatencyMs,
		MaxErrorPct:      &maxErrorPct,
		StopOnSLABreach:  true,
		SLABreachWindow:  "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop {
		t.Fatal("expected sustained SLA stop to take precedence over scaling")
	}
	if update.ShouldScale {
		t.Fatal("did not expect scaling to take precedence over stop")
	}
}

func TestValidateScenarioRequestRequiresMaxErrorCountWhenEnabled(t *testing.T) {
	_, err := validateScenarioRequest(CreateTestRequest{
		Duration:     "5m",
		ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{
			StopOnMaxErrors: true,
		},
	})

	if err == nil {
		t.Fatal("expected stop_on_max_errors without max_error_count to fail validation")
	}
}

func TestValidateScenarioRequestRequiresSLAThresholdWhenStopOnBreachEnabled(t *testing.T) {
	_, err := validateScenarioRequest(CreateTestRequest{
		Duration:     "5m",
		ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{
			StopOnSLABreach: true,
		},
	})

	if err == nil {
		t.Fatal("expected stop_on_sla_breach without SLA thresholds to fail validation")
	}
}

func TestValidateScenarioRequestRejectsInvalidGoalGuardrails(t *testing.T) {
	floatPtr := func(value float64) *float64 { return &value }
	intPtr := func(value int) *int { return &value }

	tests := []struct {
		name string
		goal GoalConfig
	}{
		{
			name: "non-positive p95 latency",
			goal: GoalConfig{P95LatencyMs: floatPtr(0)},
		},
		{
			name: "negative max error pct",
			goal: GoalConfig{MaxErrorPct: floatPtr(-1)},
		},
		{
			name: "max error pct above 100",
			goal: GoalConfig{MaxErrorPct: floatPtr(101)},
		},
		{
			name: "non-positive min vusers",
			goal: GoalConfig{MinVUsers: intPtr(0)},
		},
		{
			name: "non-positive max vusers",
			goal: GoalConfig{MaxVUsers: intPtr(0)},
		},
		{
			name: "invalid sla breach window",
			goal: GoalConfig{SLABreachWindow: "later"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateScenarioRequest(CreateTestRequest{
				Duration:     "5m",
				ScenarioMode: "goal_oriented",
				GoalConfig:   &tt.goal,
			})

			if err == nil {
				t.Fatalf("expected %s to fail validation", tt.name)
			}
		})
	}
}

func TestValidateScenarioRequestRejectsInvalidStartDelay(t *testing.T) {
	_, err := validateScenarioRequest(CreateTestRequest{
		Duration:   "5m",
		StartDelay: "later",
	})

	if err == nil {
		t.Fatal("expected invalid start_delay to fail validation")
	}
}

func TestCompileManagedTestGroupsRejectsInvalidGroupIterations(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
	})
	zero := 0

	_, _, _, err := runner.compileManagedTestGroupsLocked([]CreateGroupEntry{{
		ScriptID:   "script-1",
		VUsers:     10,
		Iterations: &zero,
	}}, nil)

	if err == nil {
		t.Fatal("expected invalid group iterations to fail compilation")
	}
}

func TestCompileManagedTestGroupsRejectsInvalidInitializationStrategy(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-init-strategy",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
	})

	_, _, _, err := runner.compileManagedTestGroupsLocked([]CreateGroupEntry{{
		ScriptID:     "script-init-strategy",
		VUsers:       10,
		InitStrategy: "before_all",
	}}, nil)

	if err == nil {
		t.Fatal("expected invalid initialization strategy to fail compilation")
	}
}

func TestDistributeTargetVUsersPreservesTotal(t *testing.T) {
	targets := distributeTargetVUsers([]TestGroup{
		{Name: "buyers", VUsers: 20},
		{Name: "admins", VUsers: 10},
	}, map[string]int{
		"buyers": 20,
		"admins": 10,
	}, 45)

	if targets["buyers"]+targets["admins"] != 45 {
		t.Fatalf("expected distributed targets to sum to 45, got %d", targets["buyers"]+targets["admins"])
	}
	if targets["buyers"] <= targets["admins"] {
		t.Fatalf("expected buyers to retain larger share, got buyers=%d admins=%d", targets["buyers"], targets["admins"])
	}
}

func TestStartTestFailsWhenScriptShakeoutFails(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-bad",
		Name:     "Broken Script",
		Protocol: "definitely-not-supported",
		Version:  "1.0.0",
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:           "Broken Scenario",
		Duration:       "30s",
		ScriptShakeout: true,
		ScriptID:       "script-bad",
		VUsers:         25,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	failed := waitForTestStatus(t, runner, test.ID, "FAILED")
	if failed.Error == "" {
		t.Fatal("expected shakeout failure to populate test error")
	}
	if !strings.Contains(strings.ToLower(failed.Error), "shakeout") {
		t.Fatalf("expected shakeout failure error, got %q", failed.Error)
	}
	if failed.StartedAt != nil {
		t.Fatal("expected failed shakeout to halt before the main run starts")
	}
}

func TestStartTestBlocksWhenCapacityShortfallRequiresOverride(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-capacity",
		Name:     "Capacity Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Blocked Scenario",
		Duration: "30s",
		ScriptID: "script-capacity",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	err = runner.StartTest(test.ID)
	if err == nil {
		t.Fatal("expected StartTest to fail when capacity shortfall requires override")
	}
	var capacityErr *CapacityShortfallError
	if !errors.As(err, &capacityErr) {
		t.Fatalf("expected CapacityShortfallError, got %T", err)
	}
	if capacityErr.Advisory == nil || !capacityErr.Advisory.RequiresOverride {
		t.Fatalf("expected advisory requiring override, got %#v", capacityErr.Advisory)
	}
	queued, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("expected queued test %s to remain in runner", test.ID)
	}
	if queued.Status != "QUEUED" {
		t.Fatalf("test status = %s, want QUEUED", queued.Status)
	}
}

func TestStartTestAllowsOverrideOnCapacityShortfall(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-capacity-override",
		Name:     "Capacity Override Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Override Scenario",
		Duration: "30s",
		ScriptID: "script-capacity-override",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTestWithOptions(test.ID, StartTestOptions{OverrideCapacityShortfall: true}); err != nil {
		t.Fatalf("StartTestWithOptions returned error: %v", err)
	}

	started := waitForStatusAny(t, runner, test.ID, 5*time.Second, "INITIALIZING", "RUNNING", "COMPLETE")
	if started.Status == "QUEUED" {
		t.Fatalf("expected override start to leave QUEUED state, got %s", started.Status)
	}
}

func TestStartTestQueuesWhenMaxConcurrentTestsReached(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-concurrency",
		Name:     "Concurrency Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := runner.CreateTest(CreateTestRequest{
		Name:     "First Scenario",
		Duration: "30s",
		ScriptID: "script-concurrency",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := runner.CreateTest(CreateTestRequest{
		Name:     "Second Scenario",
		Duration: "30s",
		ScriptID: "script-concurrency",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests[first.ID].Status = "RUNNING"
	runner.mu.Unlock()

	err = runner.StartTest(second.ID)
	if err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}
	queued, ok := runner.GetTest(second.ID)
	if !ok {
		t.Fatalf("expected queued test %s to remain in runner", second.ID)
	}
	if queued.Status != "PENDING" {
		t.Fatalf("test status = %s, want PENDING", queued.Status)
	}
}

func TestCreateTestFailsWhenProjectMaxConcurrentTestsReached(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {
			ID:   "project-1",
			Name: "Payments",
			Settings: storage.ProjectSettings{
				MaxConcurrentTests: 1,
			},
		},
	}}
	hub := NewHub()
	runner := NewTestRunner(10, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:        "script-project-create-limit",
		Name:      "Project Create Limit Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := runner.CreateTest(CreateTestRequest{
		Name:     "First Project Scenario",
		Duration: "30s",
		ScriptID: "script-project-create-limit",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests[first.ID].Status = "RUNNING"
	runner.mu.Unlock()

	second, err := runner.CreateTest(CreateTestRequest{
		Name:     "Second Project Scenario",
		Duration: "30s",
		ScriptID: "script-project-create-limit",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}
	if second.Status != "QUEUED" {
		t.Fatalf("second status = %s, want QUEUED", second.Status)
	}
}

func TestStartTestFailsWhenProjectMaxConcurrentTestsReached(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {
			ID:   "project-1",
			Name: "Payments",
			Settings: storage.ProjectSettings{
				MaxConcurrentTests: 1,
			},
		},
		"project-2": {
			ID:   "project-2",
			Name: "Orders",
			Settings: storage.ProjectSettings{
				MaxConcurrentTests: 1,
			},
		},
	}}
	hub := NewHub()
	runner := NewTestRunner(10, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:        "script-project-start-limit",
		Name:      "Project Start Limit Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	runner.StoreScript(&dsl.Script{
		ID:        "script-other-project",
		Name:      "Other Project Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-2",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := runner.CreateTest(CreateTestRequest{
		Name:     "First Project Scenario",
		Duration: "30s",
		ScriptID: "script-project-start-limit",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := runner.CreateTest(CreateTestRequest{
		Name:     "Second Project Scenario",
		Duration: "30s",
		ScriptID: "script-project-start-limit",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}
	other, err := runner.CreateTest(CreateTestRequest{
		Name:     "Other Project Scenario",
		Duration: "30s",
		ScriptID: "script-other-project",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(other) returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests[first.ID].Status = "RUNNING"
	runner.mu.Unlock()

	err = runner.StartTest(second.ID)
	if err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}
	queued, ok := runner.GetTest(second.ID)
	if !ok {
		t.Fatalf("expected queued test %s to remain in runner", second.ID)
	}
	if queued.Status != "PENDING" {
		t.Fatalf("test status = %s, want PENDING", queued.Status)
	}

	runner.mu.Lock()
	prepared, err := runner.prepareTestStartLocked(other.ID, StartTestOptions{})
	runner.mu.Unlock()
	if err != nil {
		t.Fatalf("prepareTestStartLocked(other) returned error: %v", err)
	}
	if prepared.Status != "INITIALIZING" {
		t.Fatalf("other project status = %s, want INITIALIZING", prepared.Status)
	}
}

func TestStartTestQueuesWhenProjectVUserLimitReached(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {
			ID:   "project-1",
			Name: "Payments",
			Settings: storage.ProjectSettings{
				MaxVUsers: 15,
			},
		},
	}}
	hub := NewHub()
	runner := NewTestRunner(10, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:        "script-project-vuser-limit",
		Name:      "Project VUser Limit Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := runner.CreateTest(CreateTestRequest{Name: "First", Duration: "30s", ScriptID: "script-project-vuser-limit", VUsers: 10})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := runner.CreateTest(CreateTestRequest{Name: "Second", Duration: "30s", ScriptID: "script-project-vuser-limit", VUsers: 10})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests[first.ID].Status = "RUNNING"
	runner.mu.Unlock()

	if err := runner.StartTest(second.ID); err != nil {
		t.Fatalf("StartTest(second) returned error: %v", err)
	}
	pending, ok := runner.GetTest(second.ID)
	if !ok {
		t.Fatalf("expected pending test %s", second.ID)
	}
	if pending.Status != "PENDING" {
		t.Fatalf("second status = %s, want PENDING", pending.Status)
	}
	if pending.PendingSince == nil || pending.PendingSince.IsZero() {
		t.Fatal("expected pending_since to be recorded")
	}
	if pending.QueuePosition != 1 {
		t.Fatalf("queue position = %d, want 1", pending.QueuePosition)
	}
	if !strings.Contains(pending.PendingReason, "project VUser capacity") {
		t.Fatalf("pending reason = %q, want project VUser capacity detail", pending.PendingReason)
	}
}

func TestNextPromotablePendingTestPrefersUnusedReservedProjectShare(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(10, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:        "script-regular-fair-share",
		Name:      "Regular Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "regular-project",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	runner.StoreScript(&dsl.Script{
		ID:        "script-critical-fair-share",
		Name:      "Critical Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "critical-project",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 3
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "critical-project", ReservedGenerators: 1}}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	regular, err := runner.CreateTest(CreateTestRequest{Name: "Regular", Duration: "30s", ScriptID: "script-regular-fair-share", VUsers: 10})
	if err != nil {
		t.Fatalf("CreateTest(regular) returned error: %v", err)
	}
	critical, err := runner.CreateTest(CreateTestRequest{Name: "Critical", Duration: "30s", ScriptID: "script-critical-fair-share", VUsers: 10})
	if err != nil {
		t.Fatalf("CreateTest(critical) returned error: %v", err)
	}

	older := time.Now().Add(-5 * time.Minute)
	newer := time.Now().Add(-1 * time.Minute)
	runner.mu.Lock()
	runner.tests[regular.ID].Status = "PENDING"
	runner.tests[regular.ID].PendingSince = &older
	runner.tests[critical.ID].Status = "PENDING"
	runner.tests[critical.ID].PendingSince = &newer
	next := runner.nextPromotablePendingTestLocked()
	runner.mu.Unlock()

	if next == nil || next.ID != critical.ID {
		t.Fatalf("next promotable pending test = %#v, want reserved project test %s", next, critical.ID)
	}
}

func TestPromotePendingTestsWritesSystemAuditEvent(t *testing.T) {
	store := &metricsStoreSpy{}
	hub := NewHub()
	runner := NewTestRunner(10, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:        "script-promotion-audit",
		Name:      "Promotion Audit Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-promotion",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{Name: "Pending Promotion", Duration: "1ms", ScriptID: "script-promotion-audit", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	runner.mu.Lock()
	runner.tests[testRun.ID].Status = "PENDING"
	runner.tests[testRun.ID].PendingReason = "ready to start when the queue advances"
	runner.mu.Unlock()

	runner.promotePendingTests()

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "test.pending.promote", ProjectID: "project-promotion", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
	if events[0].ActorType != "system" || events[0].ResourceID != testRun.ID || events[0].Status != "success" {
		t.Fatalf("audit event = %#v, want system promotion success for pending test", events[0])
	}
	if events[0].Details["reason"] != "fair_share_capacity_available" {
		t.Fatalf("audit details = %#v, want promotion reason", events[0].Details)
	}
}

func TestNextPromotablePendingTestBalancesActiveProjectLoad(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(10, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:        "script-project-a-fair-share",
		Name:      "Project A Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-a",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	runner.StoreScript(&dsl.Script{
		ID:        "script-project-b-fair-share",
		Name:      "Project B Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-b",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	active, err := runner.CreateTest(CreateTestRequest{Name: "Active A", Duration: "30s", ScriptID: "script-project-a-fair-share", VUsers: 10})
	if err != nil {
		t.Fatalf("CreateTest(active) returned error: %v", err)
	}
	pendingA, err := runner.CreateTest(CreateTestRequest{Name: "Pending A", Duration: "30s", ScriptID: "script-project-a-fair-share", VUsers: 10})
	if err != nil {
		t.Fatalf("CreateTest(pendingA) returned error: %v", err)
	}
	pendingB, err := runner.CreateTest(CreateTestRequest{Name: "Pending B", Duration: "30s", ScriptID: "script-project-b-fair-share", VUsers: 10})
	if err != nil {
		t.Fatalf("CreateTest(pendingB) returned error: %v", err)
	}

	older := time.Now().Add(-5 * time.Minute)
	newer := time.Now().Add(-1 * time.Minute)
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.tests[pendingA.ID].Status = "PENDING"
	runner.tests[pendingA.ID].PendingSince = &older
	runner.tests[pendingB.ID].Status = "PENDING"
	runner.tests[pendingB.ID].PendingSince = &newer
	next := runner.nextPromotablePendingTestLocked()
	runner.mu.Unlock()

	if next == nil || next.ID != pendingB.ID {
		t.Fatalf("next promotable pending test = %#v, want less-loaded project test %s", next, pendingB.ID)
	}
}

func TestReconcilePendingTestsPromotesQueuedWork(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(10, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-reconcile-pending",
		Name:     "Reconcile Pending Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	pendingSince := time.Now().Add(-2 * time.Minute)
	pending := &ManagedTest{
		ID:           "pending-1",
		Name:         "Pending Scenario",
		Status:       "PENDING",
		TotalVUsers:  1,
		Duration:     30 * time.Second,
		DurationStr:  "30s",
		CreatedAt:    time.Now().Add(-5 * time.Minute),
		PendingSince: &pendingSince,
		Groups: []TestGroup{{
			Name:       "g1",
			ScriptID:   "script-reconcile-pending",
			ScriptName: "Reconcile Pending Script",
			Protocol:   "http",
			VUsers:     1,
		}},
	}
	runner.mu.Lock()
	runner.tests[pending.ID] = pending
	runner.mu.Unlock()

	runner.ReconcilePendingTests()

	promoted := waitForStatusAny(t, runner, pending.ID, 5*time.Second, "INITIALIZING", "RUNNING", "COMPLETE")
	if promoted.Status == "PENDING" {
		t.Fatalf("expected reconcile to promote pending test, got %s", promoted.Status)
	}
}

func TestCompleteTestRunPromotesPendingTests(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {
			ID:   "project-1",
			Name: "Payments",
			Settings: storage.ProjectSettings{
				MaxConcurrentTests: 1,
			},
		},
	}}
	hub := NewHub()
	runner := NewTestRunner(10, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:        "script-project-promotion",
		Name:      "Project Promotion Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := runner.CreateTest(CreateTestRequest{Name: "First", Duration: "30s", ScriptID: "script-project-promotion", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := runner.CreateTest(CreateTestRequest{Name: "Second", Duration: "30s", ScriptID: "script-project-promotion", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests[first.ID].Status = "RUNNING"
	runner.mu.Unlock()

	if err := runner.StartTest(second.ID); err != nil {
		t.Fatalf("StartTest(second) returned error: %v", err)
	}
	pending, _ := runner.GetTest(second.ID)
	if pending.Status != "PENDING" {
		t.Fatalf("pending status = %s, want PENDING", pending.Status)
	}

	endTime := time.Now()
	runner.completeTestRun(first, "COMPLETE", endTime, "", nil)

	promoted := waitForStatusAny(t, runner, second.ID, 5*time.Second, "INITIALIZING", "RUNNING", "COMPLETE")
	if promoted.Status == "PENDING" || promoted.Status == "QUEUED" {
		t.Fatalf("expected pending test to be promoted, got %s", promoted.Status)
	}
}

func TestGetTestRefreshesCapacityAdvisoryUsingLiveAvailability(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-refresh-capacity",
		Name:     "Refresh Capacity Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	t.Setenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS", "5")
	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Refresh Advisory Scenario",
		Duration: "30s",
		ScriptID: "script-refresh-capacity",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if test.CapacityAdvisory == nil || test.CapacityAdvisory.AvailableGenerators != 5 {
		t.Fatalf("initial available generators = %#v, want 5", test.CapacityAdvisory)
	}

	t.Setenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS", "1")
	refreshed, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("expected test %s", test.ID)
	}
	if refreshed.CapacityAdvisory == nil {
		t.Fatal("expected refreshed advisory")
	}
	if refreshed.CapacityAdvisory.AvailableGenerators != 1 {
		t.Fatalf("refreshed available generators = %d, want 1", refreshed.CapacityAdvisory.AvailableGenerators)
	}
	if !refreshed.CapacityAdvisory.RequiresOverride {
		t.Fatal("expected refreshed advisory to require override after live capacity shrank")
	}
}

func TestRunTestCreatesArchiveBundle(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)
	t.Setenv("STORMRUNNER_SAMPLE_FLAG", "enabled")
	t.Setenv("STORMRUNNER_SECRET_TOKEN", "hidden")

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := registerRunnerTestProtocol(t, "archive-stub", func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	settings := runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 1
	maxErrorPct := 2.5
	settings.Thresholds.MaxErrorRatePct = &maxErrorPct
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	runner.StoreScript(&dsl.Script{
		ID:       "script-archive",
		Name:     "Archive Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:       "Archive Run",
		Duration:   "2200ms",
		ScriptID:   "script-archive",
		VUsers:     1,
		StartDelay: "150ms",
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	completed := waitForTestStatusWithin(t, runner, test.ID, 20*time.Second, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected completed test to include archive metadata")
	}
	if completed.Archive.TransactionCount == 0 {
		t.Fatal("expected archive to record transaction events")
	}
	if completed.Archive.IntervalSummaryCount == 0 {
		t.Fatal("expected archive to record interval summaries")
	}

	manifest, err := runner.GetRunArchiveManifest(test.ID)
	if err != nil {
		t.Fatalf("GetRunArchiveManifest returned error: %v", err)
	}
	if manifest.TransactionCount != completed.Archive.TransactionCount {
		t.Fatalf("expected manifest transaction count %d, got %d", completed.Archive.TransactionCount, manifest.TransactionCount)
	}
	if manifest.TransactionsParquetFile == "" || manifest.ResourceSamplesParquetFile == "" || manifest.IntervalSummariesParquetFile == "" {
		t.Fatalf("expected manifest parquet files to be populated, got %#v", manifest)
	}
	if manifest.Provenance.ProjectID != storage.DefaultProjectID {
		t.Fatalf("manifest provenance project_id = %q, want %q", manifest.Provenance.ProjectID, storage.DefaultProjectID)
	}
	if manifest.Provenance.StartDelay != "150ms" {
		t.Fatalf("unexpected provenance timing flags: %#v", manifest.Provenance)
	}
	if manifest.Provenance.ExecutionSettings.General.MetricsIntervalSeconds != 1 {
		t.Fatalf("manifest execution settings = %#v, want metrics interval 1", manifest.Provenance.ExecutionSettings)
	}
	if manifest.Provenance.ControllerEnv["STORMRUNNER_SAMPLE_FLAG"] != "enabled" {
		t.Fatalf("manifest controller env = %#v, want STORMRUNNER_SAMPLE_FLAG", manifest.Provenance.ControllerEnv)
	}
	if _, found := manifest.Provenance.ControllerEnv["STORMRUNNER_SECRET_TOKEN"]; found {
		t.Fatalf("expected secret-like env vars to be excluded, got %#v", manifest.Provenance.ControllerEnv)
	}
	if manifest.Provenance.Groups[0].ScriptID != "script-archive" {
		t.Fatalf("manifest groups = %#v, want script-archive", manifest.Provenance.Groups)
	}

	for _, name := range []string{"manifest.json", "transactions.jsonl", "interval_summaries.jsonl", "transactions.parquet", "resources.parquet", "interval_summaries.parquet"} {
		if _, err := os.Stat(filepath.Join(completed.Archive.ArchivePath, name)); err != nil {
			t.Fatalf("expected archive file %s to exist: %v", name, err)
		}
	}
}

func TestBuildRunArchiveProvenanceCapturesExecutionSnapshots(t *testing.T) {
	t.Setenv("STORMRUNNER_SAMPLE_FLAG", "enabled")
	t.Setenv("STORMRUNNER_API_TOKEN", "hidden")

	runner := NewTestRunner(2, NewHub(), nil)
	settings := runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 5
	maxErrorPct := 1.5
	settings.Thresholds.MaxErrorRatePct = &maxErrorPct
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	backend := provenanceInventoryBackend{}
	test := &ManagedTest{
		ID:               "test-provenance",
		Name:             "Checkout",
		Status:           "RUNNING",
		ProjectID:        storage.DefaultProjectID,
		ScenarioID:       "scenario-1",
		ScenarioMode:     "goal_oriented",
		ScriptShakeout:   true,
		ResourceBaseline: true,
		StartDelay:       "3s",
		TotalVUsers:      25,
		DurationStr:      "1m0s",
		Groups:           []TestGroup{{Name: "buyers", ScriptID: "script-1", ScriptName: "Checkout", Protocol: "http", VUsers: 25, RunLogicGoals: &RunLogicGoalSnapshot{GlobalIterations: 4, MaxTransactions: 40}}},
		Assignment:       &GeneratorAssignmentSettings{Mode: "specific_hosts", SelectedTargets: []string{"generator-1"}},
		GoalConfig:       &GoalConfig{MaxErrorPct: &maxErrorPct},
		CreatedAt:        time.Date(2026, time.May, 13, 12, 0, 0, 0, time.UTC),
		StartedAt:        func() *time.Time { ts := time.Date(2026, time.May, 13, 12, 0, 3, 0, time.UTC); return &ts }(),
		executionBackend: backend,
	}

	provenance := buildRunArchiveProvenance(runner, test)
	if !provenance.ResourceBaseline || provenance.StartDelay != "3s" {
		t.Fatalf("unexpected provenance timing fields: %#v", provenance)
	}
	if provenance.ExecutionSettings.General.MetricsIntervalSeconds != 5 {
		t.Fatalf("execution settings = %#v, want metrics interval 5", provenance.ExecutionSettings)
	}
	if len(provenance.GeneratorInventory) != 1 || provenance.GeneratorInventory[0].Target != "generator-1" {
		t.Fatalf("generator inventory = %#v, want generator-1", provenance.GeneratorInventory)
	}
	if len(provenance.Groups) != 1 || provenance.Groups[0].RunLogicGoals == nil || provenance.Groups[0].RunLogicGoals.GlobalIterations != 4 || provenance.Groups[0].RunLogicGoals.MaxTransactions != 40 {
		t.Fatalf("provenance groups = %#v, want run logic goal snapshot", provenance.Groups)
	}
	if provenance.ControllerEnv["STORMRUNNER_SAMPLE_FLAG"] != "enabled" {
		t.Fatalf("controller env = %#v, want STORMRUNNER_SAMPLE_FLAG", provenance.ControllerEnv)
	}
	if _, found := provenance.ControllerEnv["STORMRUNNER_API_TOKEN"]; found {
		t.Fatalf("expected secret-like env vars to be filtered, got %#v", provenance.ControllerEnv)
	}
}

func TestRunTestIncludesConfiguredStartDelay(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := registerRunnerTestProtocol(t, "start-delay", func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-start-delay",
		Name:     "Start Delay Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:       "Delayed Run",
		Duration:   "1100ms",
		StartDelay: "900ms",
		ScriptID:   "script-start-delay",
		VUsers:     1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	startedAt := time.Now()
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	completed := waitForTestStatus(t, runner, test.ID, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected completed test to include archive metadata")
	}
	if completed.EndedAt == nil {
		t.Fatal("expected completed test to have ended_at populated")
	}
	if completed.EndedAt.Sub(startedAt) < 1800*time.Millisecond {
		t.Fatalf("expected run duration to include start delay, got %v", completed.EndedAt.Sub(startedAt))
	}
}

func TestNextScenarioScheduleRun(t *testing.T) {
	schedule := &storage.ScenarioSchedule{CronExpr: "*/15 * * * *"}
	next, err := nextScenarioScheduleRun(schedule, time.Date(2026, time.May, 16, 10, 7, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("nextScenarioScheduleRun returned error: %v", err)
	}
	want := time.Date(2026, time.May, 16, 10, 15, 0, 0, time.UTC)
	if next == nil || !next.Equal(want) {
		t.Fatalf("next run = %v, want %s", next, want)
	}
}

func TestNextScenarioScheduleRunHonorsBlackoutAndAvailabilityWindows(t *testing.T) {
	schedule := &storage.ScenarioSchedule{
		CronExpr: "0 * * * *",
		Timezone: "UTC",
		BlackoutWindows: []storage.ScenarioScheduleWindow{{
			Name:       "maintenance",
			DaysOfWeek: []int{int(time.Saturday)},
			StartTime:  "10:00",
			EndTime:    "12:00",
		}},
		AvailabilityWindows: []storage.ScenarioScheduleWindow{{
			Name:       "business-hours",
			DaysOfWeek: []int{int(time.Saturday)},
			StartTime:  "09:00",
			EndTime:    "17:00",
		}},
	}
	next, err := nextScenarioScheduleRun(schedule, time.Date(2026, time.May, 16, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("nextScenarioScheduleRun returned error: %v", err)
	}
	want := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	if next == nil || !next.Equal(want) {
		t.Fatalf("next run = %v, want %s", next, want)
	}
}

func TestScenarioSchedulerRunDueSchedulesCreatesAndStartsRun(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-schedule",
		Name:     "Scheduled Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Scheduled Checkout",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-schedule",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID:              "schedule-1",
		ScenarioID:      scenario.ID,
		Name:            "Lunch Run",
		CronExpr:        "*/5 * * * *",
		RunNameTemplate: "Daily Smoke",
		Enabled:         true,
		NextRunAt:       &due,
		CreatedAt:       now.Add(-time.Hour),
		UpdatedAt:       now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}

	select {
	case testID := <-backend.started:
		if testID == "" {
			t.Fatal("expected scheduled run to start a test")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduled test to start")
	}

	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if len(runner.tests) != 1 {
		t.Fatalf("runner.tests length = %d, want 1", len(runner.tests))
	}
	storedScenario, ok := runner.scenarios[scenario.ID]
	if !ok || len(storedScenario.Schedules) != 1 {
		t.Fatalf("scenario schedules = %#v, want 1 stored schedule", storedScenario)
	}
	updated := storedScenario.Schedules[0]
	if updated.LastRunTestID == "" {
		t.Fatal("expected LastRunTestID to be recorded")
	}
	if updated.LastRunAt == nil || !updated.LastRunAt.Equal(now) {
		t.Fatalf("LastRunAt = %v, want %s", updated.LastRunAt, now)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(now) {
		t.Fatalf("NextRunAt = %v, want value after %s", updated.NextRunAt, now)
	}
	if updated.LastError != "" {
		t.Fatalf("LastError = %q, want empty", updated.LastError)
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "materialized" || occurrences[0].TestID != updated.LastRunTestID {
		t.Fatalf("occurrences = %#v, want one materialized occurrence for %s", occurrences, updated.LastRunTestID)
	}
	auditEvents, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "scenario.schedule.occurrence.materialized", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(auditEvents) != 1 || auditEvents[0].ResourceID != occurrences[0].ID || auditEvents[0].Details["reason_code"] != "started" {
		t.Fatalf("audit events = %#v, want materialized occurrence audit", auditEvents)
	}
}

func TestScenarioSchedulerReconcilesMaterializedOccurrenceAfterRestart(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-schedule-restart",
		Name:     "Scheduled Restart Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Restart Scenario",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-schedule-restart",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	materializedAt := now.Add(-30 * time.Second)
	schedule := &storage.ScenarioSchedule{
		ID:              "schedule-restart",
		ScenarioID:      scenario.ID,
		Revision:        1,
		Name:            "Restart Safe Run",
		CronExpr:        "*/5 * * * *",
		RunNameTemplate: "Restart Smoke",
		Enabled:         true,
		NextRunAt:       &due,
		CreatedAt:       now.Add(-time.Hour),
		UpdatedAt:       now.Add(-time.Hour),
	}
	if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
		ID:               "occurrence-restart",
		ScheduleID:       schedule.ID,
		ScenarioID:       scenario.ID,
		ScheduleRevision: 1,
		PlannedAt:        due,
		MaterializedAt:   &materializedAt,
		Status:           "materialized",
		ReasonCode:       "started",
		TestID:           "test-already-started",
		CreatedAt:        materializedAt,
		UpdatedAt:        materializedAt,
	}); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence returned error: %v", err)
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}

	select {
	case testID := <-backend.started:
		t.Fatalf("unexpected duplicate scheduled test start: %s", testID)
	default:
	}
	storedSchedules, ok := runner.ListScenarioSchedules(scenario.ID)
	if !ok || len(storedSchedules) != 1 {
		t.Fatalf("stored schedules = %#v ok=%v, want one", storedSchedules, ok)
	}
	updated := storedSchedules[0]
	if updated.LastRunTestID != "test-already-started" {
		t.Fatalf("LastRunTestID = %q, want existing test", updated.LastRunTestID)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(now) {
		t.Fatalf("NextRunAt = %v, want advanced after restart reconciliation", updated.NextRunAt)
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].ID != "occurrence-restart" {
		t.Fatalf("occurrences = %#v, want original materialized occurrence only", occurrences)
	}
}

func TestScenarioSchedulerLeasePreventsStandbyControllerMaterialization(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-schedule-lease",
		Name:     "Scheduled Lease Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Lease Scenario",
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-schedule-lease", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID:              "schedule-lease",
		ScenarioID:      scenario.ID,
		Name:            "Lease Run",
		CronExpr:        "*/5 * * * *",
		RunNameTemplate: "Lease Smoke",
		Enabled:         true,
		MissedRunPolicy: "catch_up_once",
		NextRunAt:       &due,
		CreatedAt:       now.Add(-time.Hour),
		UpdatedAt:       now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()
	if acquired, err := store.TryAcquireSchedulerLease("scenario-scheduler", "controller-a", now, time.Minute); err != nil || !acquired {
		t.Fatalf("pre-acquire lease acquired=%v err=%v, want active controller-a lease", acquired, err)
	}

	standby := newScenarioScheduler(runner)
	standby.ownerID = "controller-b"
	standby.leaseTTL = time.Minute
	standby.now = func() time.Time { return now.Add(10 * time.Second) }
	if err := standby.RunDueSchedules(); err != nil {
		t.Fatalf("standby RunDueSchedules returned error: %v", err)
	}
	select {
	case testID := <-backend.started:
		t.Fatalf("unexpected standby materialization: %s", testID)
	default:
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 0 {
		t.Fatalf("occurrences = %#v, want none while standby lacks lease", occurrences)
	}

	active := newScenarioScheduler(runner)
	active.ownerID = "controller-b"
	active.leaseTTL = time.Minute
	active.now = func() time.Time { return now.Add(2 * time.Minute) }
	if err := active.RunDueSchedules(); err != nil {
		t.Fatalf("active RunDueSchedules returned error: %v", err)
	}
	occurrences, err = store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "materialized" || occurrences[0].TestID == "" {
		t.Fatalf("occurrence = %+v, want one materialized occurrence after lease takeover", occurrences[0])
	}
}

func TestScenarioSchedulerBlocksDueSchedulePendingApproval(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-schedule-approval",
		Name:     "Scheduled Approval Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Approval Scenario",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-schedule-approval",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID:                 "schedule-approval",
		ScenarioID:         scenario.ID,
		Name:               "Production Run",
		CronExpr:           "*/5 * * * *",
		RunNameTemplate:    "Production Smoke",
		Enabled:            true,
		ApprovalPolicy:     "production",
		NextRequiredAction: "approval_required",
		PendingApprovalID:  "approval-1",
		NextRunAt:          &due,
		CreatedAt:          now.Add(-time.Hour),
		UpdatedAt:          now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}

	select {
	case testID := <-backend.started:
		t.Fatalf("unexpected scheduled test start: %s", testID)
	default:
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "blocked" || occurrences[0].ReasonCode != "approval_required" || occurrences[0].ApprovalID != "approval-1" {
		t.Fatalf("occurrences = %#v, want approval-blocked occurrence", occurrences)
	}
	auditEvents, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "scenario.schedule.occurrence.blocked", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(auditEvents) != 1 || auditEvents[0].Status != "blocked" || auditEvents[0].Details["approval_id"] != "approval-1" {
		t.Fatalf("audit events = %#v, want blocked approval occurrence audit", auditEvents)
	}
}

func TestScenarioSchedulerAppliesMissedRunPolicies(t *testing.T) {
	for _, tc := range []struct {
		name               string
		policy             string
		wantStatus         string
		wantReason         string
		wantRequiredAction string
	}{
		{name: "skip", policy: "skip", wantStatus: "skipped", wantReason: "missed_run_skipped"},
		{name: "operator-decision", policy: "require_operator_decision", wantStatus: "blocked", wantReason: "missed_run_requires_decision", wantRequiredAction: "operator_decision_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &metricsStoreSpy{}
			runner := NewTestRunner(2, NewHub(), store)
			backend := &scheduleExecutionBackend{started: make(chan string, 1)}
			runner.setExecutionBackend(backend)
			runner.StoreScript(&dsl.Script{
				ID:       "script-missed-" + tc.name,
				Name:     "Missed Policy Script",
				Protocol: "http",
				Version:  "1.0.0",
				Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
			})
			scenario, err := runner.StoreScenario(CreateScenarioRequest{
				Name:     "Missed Policy Scenario",
				Duration: "1m",
				Groups: []CreateGroupEntry{{
					ScriptID: "script-missed-" + tc.name,
					VUsers:   1,
				}},
			})
			if err != nil {
				t.Fatalf("StoreScenario returned error: %v", err)
			}
			now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
			missed := now.Add(-10 * time.Minute)
			schedule := &storage.ScenarioSchedule{
				ID:              "schedule-missed-" + tc.name,
				ScenarioID:      scenario.ID,
				Name:            "Missed Run",
				CronExpr:        "*/5 * * * *",
				RunNameTemplate: "Missed Smoke",
				Enabled:         true,
				MissedRunPolicy: tc.policy,
				NextRunAt:       &missed,
				CreatedAt:       now.Add(-time.Hour),
				UpdatedAt:       now.Add(-time.Hour),
			}
			runner.mu.Lock()
			if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
				runner.mu.Unlock()
				t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
			}
			runner.mu.Unlock()

			scheduler := newScenarioScheduler(runner)
			scheduler.now = func() time.Time { return now }
			if err := scheduler.RunDueSchedules(); err != nil {
				t.Fatalf("RunDueSchedules returned error: %v", err)
			}
			select {
			case testID := <-backend.started:
				t.Fatalf("unexpected scheduled test start: %s", testID)
			default:
			}
			occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
			if err != nil {
				t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
			}
			if len(occurrences) != 1 || occurrences[0].Status != tc.wantStatus || occurrences[0].ReasonCode != tc.wantReason {
				t.Fatalf("occurrences = %#v, want %s/%s", occurrences, tc.wantStatus, tc.wantReason)
			}
			storedSchedules, ok := runner.ListScenarioSchedules(scenario.ID)
			if !ok || len(storedSchedules) != 1 {
				t.Fatalf("stored schedules = %#v ok=%v, want one", storedSchedules, ok)
			}
			if storedSchedules[0].NextRequiredAction != tc.wantRequiredAction {
				t.Fatalf("next required action = %q, want %q", storedSchedules[0].NextRequiredAction, tc.wantRequiredAction)
			}
		})
	}
}

func TestScenarioSchedulerCatchUpAllMaterializesEachMissedOccurrence(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(8, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 10)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-catch-up-all",
		Name:     "Catch Up Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Catch Up Scenario",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-catch-up-all",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	now := time.Date(2026, time.May, 16, 12, 20, 0, 0, time.UTC)
	firstMissed := time.Date(2026, time.May, 16, 12, 5, 0, 0, time.UTC)
	schedule := &storage.ScenarioSchedule{
		ID:              "schedule-catch-up-all",
		ScenarioID:      scenario.ID,
		Revision:        1,
		Name:            "Catch Up All",
		CronExpr:        "*/5 * * * *",
		RunNameTemplate: "Catch Up Smoke",
		Enabled:         true,
		MissedRunPolicy: "catch_up_all",
		NextRunAt:       &firstMissed,
		CreatedAt:       now.Add(-time.Hour),
		UpdatedAt:       now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}

	started := map[string]bool{}
	for len(started) < 4 {
		select {
		case testID := <-backend.started:
			started[testID] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for catch-up starts, got %d", len(started))
		}
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 4 {
		t.Fatalf("occurrences = %#v, want four materialized catch-up occurrences", occurrences)
	}
	for _, occurrence := range occurrences {
		if occurrence.Status != "materialized" || occurrence.ReasonCode != "started" || occurrence.TestID == "" {
			t.Fatalf("occurrence = %#v, want materialized started occurrence", occurrence)
		}
	}
	storedSchedules, ok := runner.ListScenarioSchedules(scenario.ID)
	if !ok || len(storedSchedules) != 1 {
		t.Fatalf("stored schedules = %#v ok=%v, want one", storedSchedules, ok)
	}
	if storedSchedules[0].NextRunAt == nil || !storedSchedules[0].NextRunAt.After(now) {
		t.Fatalf("NextRunAt = %v, want catch-up advanced beyond now", storedSchedules[0].NextRunAt)
	}
	auditEvents, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "scenario.schedule.occurrence.materialized", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(auditEvents) != 4 {
		t.Fatalf("audit events = %#v, want four materialized audits", auditEvents)
	}
}

func TestScenarioScheduleConflictDetectionBlocksMaterialization(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-schedule-conflict": {
			ID:   "project-schedule-conflict",
			Name: "Schedule Conflict",
			Settings: storage.ProjectSettings{
				MaxVUsers: 5,
			},
		},
	}}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:        "script-schedule-conflict",
		Name:      "Schedule Conflict Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-schedule-conflict",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Conflict Scenario",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-schedule-conflict",
			VUsers:   10,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	created, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{Name: "Conflict Schedule", CronExpr: "*/5 * * * *"})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule returned error: %v", err)
	}
	if created.ConflictSummary == "" || created.NextRequiredAction != "resolve_conflict" {
		t.Fatalf("created conflict = %q action=%q, want conflict resolution required", created.ConflictSummary, created.NextRequiredAction)
	}
	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	created.NextRunAt = &due
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(created); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}
	select {
	case testID := <-backend.started:
		t.Fatalf("unexpected scheduled test start: %s", testID)
	default:
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(created.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "blocked" || occurrences[0].ReasonCode != "schedule_conflict" {
		t.Fatalf("occurrences = %#v, want schedule_conflict block", occurrences)
	}
}

func TestScenarioScheduleConflictDetectionBlocksArchivedRelease(t *testing.T) {
	archivedAt := time.Date(2026, time.May, 16, 8, 0, 0, 0, time.UTC)
	store := &metricsStoreSpy{releasesByID: map[string]*storage.Release{
		"rel-archived": {
			ID:            "rel-archived",
			ApplicationID: storage.LegacyApplicationID,
			Name:          "Archived Release",
			ArchivedAt:    &archivedAt,
		},
	}}
	runner := NewTestRunner(2, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-release-conflict",
		Name:     "Release Conflict Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Release Conflict Scenario",
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-release-conflict", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	created, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{Name: "Archived Release Schedule", CronExpr: "*/5 * * * *", ReleaseID: "rel-archived"})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule returned error: %v", err)
	}
	if !strings.Contains(created.ConflictSummary, "Release conflict") || created.NextRequiredAction != "resolve_conflict" {
		t.Fatalf("created conflict = %q action=%q, want archived release conflict", created.ConflictSummary, created.NextRequiredAction)
	}
}

func TestScenarioScheduleMaterializationBlocksEnvironmentUnavailable(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-env-conflict",
		Name:     "Environment Conflict Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Environment Conflict Scenario",
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-env-conflict", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	created, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{Name: "Environment Schedule", CronExpr: "*/5 * * * *"})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule returned error: %v", err)
	}
	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	created.NextRunAt = &due
	created.AvailabilityWindows = []storage.ScenarioScheduleWindow{{Name: "overnight", StartTime: "00:00", EndTime: "01:00", Timezone: "UTC"}}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(created); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}
	select {
	case testID := <-backend.started:
		t.Fatalf("unexpected scheduled test start: %s", testID)
	default:
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(created.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "blocked" || occurrences[0].ReasonCode != "environment_unavailable" {
		t.Fatalf("occurrences = %#v, want environment_unavailable block", occurrences)
	}
	if !strings.Contains(occurrences[0].ConflictSummary, "Environment availability conflict") {
		t.Fatalf("conflict summary = %q, want environment availability conflict", occurrences[0].ConflictSummary)
	}
}

func TestScenarioScheduleMaintenanceWindowBlocksMaterialization(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-maintenance-conflict",
		Name:     "Maintenance Conflict Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Maintenance Conflict Scenario",
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-maintenance-conflict", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID:                 "schedule-maintenance-conflict",
		ScenarioID:         scenario.ID,
		Name:               "Maintenance Schedule",
		CronExpr:           "*/5 * * * *",
		RunNameTemplate:    "Maintenance Smoke",
		Enabled:            true,
		MissedRunPolicy:    "catch_up_once",
		MaintenanceWindows: []storage.ScenarioScheduleWindow{{Name: "shared-maintenance", StartTime: "11:00", EndTime: "13:00", Timezone: "UTC"}},
		NextRunAt:          &due,
		CreatedAt:          now.Add(-time.Hour),
		UpdatedAt:          now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}
	select {
	case testID := <-backend.started:
		t.Fatalf("unexpected scheduled test start: %s", testID)
	default:
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "blocked" || occurrences[0].ReasonCode != "environment_unavailable" {
		t.Fatalf("occurrences = %#v, want maintenance window environment_unavailable block", occurrences)
	}
}

func TestDeleteTestRemovesArchiveBundle(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := registerRunnerTestProtocol(t, "archive-delete", func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-delete-archive",
		Name:     "Delete Archive Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Delete Archive Run",
		Duration: "2200ms",
		ScriptID: "script-delete-archive",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	completed := waitForTestStatus(t, runner, test.ID, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected archive metadata after completion")
	}
	archivePath := completed.Archive.ArchivePath
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("expected archive directory to exist before delete: %v", err)
	}

	if !runner.DeleteTest(test.ID) {
		t.Fatal("expected DeleteTest to succeed for completed run")
	}
	if _, ok := runner.GetTest(test.ID); ok {
		t.Fatal("expected deleted test to be removed from runner")
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("expected archive directory to be removed, stat err=%v", err)
	}
	if _, ok := runner.GetRunArchive(test.ID); ok {
		t.Fatal("expected archive metadata to be removed after delete")
	}
}

func TestDeleteTestRejectsPausedRuns(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := registerRunnerTestProtocol(t, "archive-delete-paused", func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-delete-paused",
		Name:     "Delete Paused Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Delete Paused Run",
		Duration: "10s",
		ScriptID: "script-delete-paused",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	waitForStatusAny(t, runner, test.ID, 5*time.Second, "RUNNING", "COMPLETE")
	if err := runner.PauseTest(test.ID); err != nil {
		t.Fatalf("PauseTest returned error: %v", err)
	}
	waitForTestStatus(t, runner, test.ID, "PAUSED")

	if runner.DeleteTest(test.ID) {
		t.Fatal("expected DeleteTest to reject paused run")
	}
	if stillPresent, ok := runner.GetTest(test.ID); !ok {
		t.Fatal("expected paused test to remain in runner after delete rejection")
	} else if stillPresent.Status != "PAUSED" {
		t.Fatalf("expected paused test to remain paused, got %q", stillPresent.Status)
	}

	if err := runner.ResumeTest(test.ID); err != nil {
		t.Fatalf("ResumeTest returned error: %v", err)
	}
	if err := runner.StopTest(test.ID); err != nil {
		t.Fatalf("StopTest returned error: %v", err)
	}
	waitForStatusAny(t, runner, test.ID, 5*time.Second, "FAILED", "COMPLETE")
	if !runner.DeleteTest(test.ID) {
		t.Fatal("expected DeleteTest to succeed once run is no longer active")
	}
	if _, ok := runner.GetTest(test.ID); ok {
		t.Fatal("expected test to be removed after terminal delete")
	}
	if _, ok := runner.GetRunArchive(test.ID); ok {
		t.Fatal("expected archive metadata to be removed after terminal delete")
	}
	if len(hub.broadcast) == 0 {
		// The lifecycle path should have emitted pause/resume/stop events; if this ever changes,
		// the test should fail here rather than silently ignoring a missing active run path.
		t.Fatal("expected lifecycle broadcasts during pause-delete-resume flow")
	}
}

func TestRunnerCapturesUsageSnapshotWhenRunCompletes(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	test := &ManagedTest{
		ID:          "usage-terminal",
		Name:        "Usage Terminal",
		Status:      "RUNNING",
		ProjectID:   "project-a",
		TotalVUsers: 4,
		Duration:    time.Minute,
		Groups:      []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 4}},
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	runner.completeTestRun(test, "COMPLETE", time.Now().UTC(), "", nil)

	if len(store.usageSnapshots) != 1 {
		t.Fatalf("usage snapshots = %#v, want one terminal-run snapshot", store.usageSnapshots)
	}
	snapshot := store.usageSnapshots[0]
	if snapshot.ProjectID != "project-a" || snapshot.ID == "" || snapshot.PayloadJSON == "" {
		t.Fatalf("snapshot = %#v, want project-scoped persisted payload", snapshot)
	}
	var usage UsageAccountingResponse
	if err := json.Unmarshal([]byte(snapshot.PayloadJSON), &usage); err != nil {
		t.Fatalf("unmarshal snapshot usage: %v", err)
	}
	if usage.ProjectID != "project-a" || usage.Totals.RunCount != 1 || usage.Totals.VUserSeconds != 240 {
		t.Fatalf("usage snapshot payload = %#v, want completed project usage", usage)
	}
}

func TestRunnerCapturesUsageSnapshotOnCadenceTick(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	test := &ManagedTest{
		ID:          "usage-cadence",
		Name:        "Usage Cadence",
		Status:      "COMPLETE",
		ProjectID:   "project-b",
		TotalVUsers: 3,
		Duration:    2 * time.Minute,
		Groups:      []TestGroup{{Name: "grpc", Protocol: "grpc", VUsers: 3}},
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		runner.RunUsageAccountingSnapshotCadence(ctx, ticks)
		close(done)
	}()

	ticks <- time.Now()

	deadline := time.Now().Add(time.Second)
	for len(store.usageSnapshots) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if len(store.usageSnapshots) != 1 {
		t.Fatalf("usage snapshots = %#v, want one cadence snapshot", store.usageSnapshots)
	}
	var usage UsageAccountingResponse
	if err := json.Unmarshal([]byte(store.usageSnapshots[0].PayloadJSON), &usage); err != nil {
		t.Fatalf("unmarshal snapshot usage: %v", err)
	}
	if usage.ProjectID != "" || usage.Totals.RunCount != 1 || usage.Totals.VUserSeconds != 360 {
		t.Fatalf("cadence snapshot usage = %#v, want all-project usage", usage)
	}
}

func waitForTestStatus(t *testing.T, runner *TestRunner, testID string, want string) *ManagedTest {
	return waitForTestStatusWithin(t, runner, testID, 12*time.Second, want)
}

func waitForTestStatusWithin(t *testing.T, runner *TestRunner, testID string, timeout time.Duration, want string) *ManagedTest {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		test, ok := runner.GetTest(testID)
		if !ok {
			t.Fatalf("test %s not found", testID)
		}
		if test.Status == want {
			return test
		}
		time.Sleep(25 * time.Millisecond)
	}

	test, _ := runner.GetTest(testID)
	t.Fatalf("timed out waiting for test %s to reach %s, current status %s", testID, want, test.Status)
	return nil
}

func waitForStatusAny(t *testing.T, runner *TestRunner, testID string, timeout time.Duration, want ...string) *ManagedTest {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		test, ok := runner.GetTest(testID)
		if !ok {
			t.Fatalf("test %s not found", testID)
		}
		for _, status := range want {
			if test.Status == status {
				return test
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	test, _ := runner.GetTest(testID)
	t.Fatalf("timed out waiting for test %s to reach one of %v, current status %s", testID, want, test.Status)
	return nil
}
