package api

// Persistence seam for the TestRunner: bootstrap loaders for stored scripts,
// scenarios, tests, history and archives; reconcile; save/delete/update of
// records and run status; in-memory<->storage record conversion; ownership
// attribution; and usage-accounting snapshot capture/cadence.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	durableTransitionAttempts   = 3
	durableTransitionRetryDelay = 10 * time.Millisecond
)

// retryDurableTransition absorbs short storage interruptions without allowing a
// terminal state to become visible only in memory. The bound is intentionally
// small: execution goroutines may wait briefly for durability, but cannot hang
// indefinitely behind an unavailable database.
func retryDurableTransition(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		if err := operation(); err != nil {
			lastErr = err
			if attempt+1 < durableTransitionAttempts {
				time.Sleep(durableTransitionRetryDelay)
			}
			continue
		}
		return nil
	}
	return lastErr
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
			ID:              script.ID,
			RefKey:          script.RefKey,
			Name:            script.Name,
			Protocol:        script.Protocol,
			Version:         script.Version,
			ProjectID:       script.ProjectID,
			FolderID:        script.FolderID,
			CreatedAt:       script.CreatedAt,
			AuthoringHealth: buildStoredScriptAuthoringHealth(script),
			Script:          script,
		}
	}
}

func (tr *TestRunner) reloadStoredScripts() error {
	if tr == nil || tr.scriptStore == nil {
		return nil
	}
	scripts, err := tr.scriptStore.ListScripts("", nil)
	if err != nil {
		return err
	}
	loaded := make(map[string]*StoredScript, len(scripts))
	for _, script := range scripts {
		if script == nil {
			continue
		}
		loaded[script.ID] = &StoredScript{
			ID:              script.ID,
			RefKey:          script.RefKey,
			Name:            script.Name,
			Protocol:        script.Protocol,
			Version:         script.Version,
			ProjectID:       script.ProjectID,
			FolderID:        script.FolderID,
			CreatedAt:       script.CreatedAt,
			AuthoringHealth: buildStoredScriptAuthoringHealth(script),
			Script:          script,
		}
	}
	tr.mu.Lock()
	for _, script := range tr.scripts {
		if script != nil {
			tr.removeRefKeyLocked(script.RefKey)
		}
	}
	tr.scripts = loaded
	for _, script := range loaded {
		tr.observeAndIndexRefKeyLocked(script.RefKey, script.ID)
	}
	tr.mu.Unlock()
	return nil
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

// reloadStoredScenarios refreshes the in-memory scenario/schedule view from
// shared storage so an HA leader observes CRUD performed by another replica.
func (tr *TestRunner) reloadStoredScenarios() error {
	if tr == nil || tr.scenarioStore == nil {
		return nil
	}
	scenarios, err := tr.scenarioStore.ListScenarios()
	if err != nil {
		return err
	}
	loaded := make(map[string]*StoredScenario, len(scenarios))
	tr.mu.RLock()
	for _, scenario := range scenarios {
		if scenario == nil {
			continue
		}
		full, scripts, loadErr := tr.scenarioStore.GetScenario(scenario.ID)
		if loadErr != nil {
			tr.mu.RUnlock()
			return loadErr
		}
		loaded[scenario.ID] = tr.buildStoredScenario(full, scripts)
	}
	tr.mu.RUnlock()
	tr.mu.Lock()
	for _, scenario := range tr.scenarios {
		if scenario != nil {
			tr.removeRefKeyLocked(scenario.RefKey)
		}
	}
	tr.scenarios = loaded
	for _, scenario := range loaded {
		tr.observeAndIndexRefKeyLocked(scenario.RefKey, scenario.ID)
	}
	tr.mu.Unlock()
	return nil
}

func (tr *TestRunner) buildStoredScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) *StoredScenario {
	if scenario == nil {
		return nil
	}
	refs := make([]ScenarioScriptRef, 0, len(scripts))
	projectID, singleProject := tr.scenarioProjectIDFromScriptsLocked(scripts)
	for _, script := range scripts {
		ref := ScenarioScriptRef{
			Order:           script.Order,
			Name:            script.Name,
			ScriptID:        script.ScriptID,
			Role:            script.Role,
			VUsers:          script.VUsers,
			RampUp:          script.RampUp,
			RampDown:        script.RampDown,
			Stages:          cloneLoadStages(script.Stages),
			Iterations:      script.Iterations,
			Initialization:  script.Initialization,
			RuntimeSettings: decodeStoredRuntimeSettings(script.RuntimeSettings),
			Assignment:      decodeStoredGeneratorAssignment(script.Assignment),
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
	schedules := []*storage.ScenarioSchedule{}
	if tr.scenarioStore != nil {
		if storedSchedules, err := tr.scenarioStore.ListScenarioSchedules(scenario.ID); err == nil {
			for _, schedule := range storedSchedules {
				schedules = append(schedules, cloneScenarioSchedule(schedule))
			}
		}
	}
	derivedProjectID := ""
	if singleProject {
		derivedProjectID = projectID
	}
	return &StoredScenario{
		ID:               scenario.ID,
		RefKey:           scenario.RefKey,
		Name:             scenario.Name,
		ProjectID:        derivedProjectID,
		Duration:         scenario.Duration,
		StartDelay:       scenario.StartDelay,
		ScenarioMode:     normalizeScenarioMode(scenario.ScenarioMode),
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		Assignment:       decodeStoredGeneratorAssignment(scenario.Assignment),
		GoalConfig:       decodeStoredGoalConfig(scenario.GoalConfig),
		RuntimeSettings:  decodeStoredRuntimeSettings(scenario.RuntimeSettings),
		Scripts:          refs,
		Schedules:        schedules,
		DataPools:        dataPools,
		CreatedAt:        scenario.CreatedAt,
		UpdatedAt:        scenario.UpdatedAt,
		ScriptCount:      len(refs),
	}
}

func (tr *TestRunner) scenarioProjectIDFromScriptsLocked(scripts []storage.ScenarioScript) (string, bool) {
	projectID := ""
	for _, scenarioScript := range scripts {
		stored, ok := tr.scripts[scenarioScript.ScriptID]
		if !ok || stored == nil {
			continue
		}
		candidate := normalizeProjectID(stored.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", false
		}
	}
	if projectID == "" {
		return "", false
	}
	return projectID, true
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
		// Marking an interrupted run FAILED is a leader-only mutation: a booting
		// non-leader (HA standby) must not clobber the active leader's live runs
		// in the shared database. The eventual leader reconciles on acquisition.
		if tr.isExecutionLeader() {
			tr.reconcileStoredActiveTest(managedTest)
		}
		tr.loadStoredTestHistory(managedTest)
		tr.loadStoredTestArchive(managedTest)
		tr.tests[record.ID] = managedTest
	}
}

// reloadStoredTests refreshes the in-memory test map from the database when this
// replica acquires execution leadership. Unlike loadStoredTests (boot, single
// threaded), it runs concurrently with API readers, so map mutation is locked.
// Tests this replica is actively running are left untouched; everything else is
// re-hydrated so a failover leader sees runs and queued work created elsewhere.
func (tr *TestRunner) reloadStoredTests() error {
	if tr.testStore == nil {
		return nil
	}
	records, err := tr.testStore.ListTests("", nil)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		seen[record.ID] = struct{}{}
		tr.mu.RLock()
		existing, ok := tr.tests[record.ID]
		liveLocally := ok && existing.cancel != nil
		tr.mu.RUnlock()
		if liveLocally {
			continue
		}
		managedTest := managedTestFromRecord(record)
		if tr.holdsExecutionLeadership() {
			tr.reconcileStoredActiveTest(managedTest)
		}
		tr.loadStoredTestHistory(managedTest)
		tr.loadStoredTestArchive(managedTest)
		tr.mu.Lock()
		// Re-check liveness under the write lock: startManagedTestRun may have begun
		// a run for this ID in the window since the RLock above. Overwriting it with
		// the DB-hydrated copy (cancel == nil) would detach the running goroutine's
		// cancel func, so a later Stop/Abort could never stop the run.
		if current, ok := tr.tests[record.ID]; !ok || current.cancel == nil {
			if ok && current != nil && current.RefKey != managedTest.RefKey {
				tr.removeRefKeyLocked(current.RefKey)
			}
			tr.tests[record.ID] = managedTest
			tr.observeAndIndexRefKeyLocked(managedTest.RefKey, managedTest.ID)
		}
		tr.mu.Unlock()
	}
	tr.mu.Lock()
	for id, current := range tr.tests {
		if _, ok := seen[id]; ok || (current != nil && current.cancel != nil) {
			continue
		}
		if current != nil {
			tr.removeRefKeyLocked(current.RefKey)
		}
		delete(tr.tests, id)
	}
	tr.mu.Unlock()
	return nil
}

func (tr *TestRunner) reconcileStoredActiveTest(test *ManagedTest) {
	if test == nil {
		return
	}
	switch test.Status {
	case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
	default:
		return
	}
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousEndedAt := cloneTimePtr(test.EndedAt)
	previousError := test.Error
	now := time.Now().UTC()
	test.Status = "FAILED"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.EndedAt = &now
	if test.Error == "" {
		test.Error = "controller restarted before the run completed"
	}
	if err := tr.persistStoredTestStatus(test.ID, test.Status, &now, test.Error); err != nil {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.EndedAt = previousEndedAt
		test.Error = previousError
		slog.Error("failed to persist recovered test state", "test_id", test.ID, "status", test.Status, "error", err)
		return
	}
	// The hydrated row is deliberately not in tr.tests yet. Release from the
	// explicit durable binding and retain that slot ID for retry so startup
	// reconciliation cannot leak capacity merely because map insertion happens
	// later in the load loop.
	if test.TimeslotID != nil {
		slotID := strings.TrimSpace(*test.TimeslotID)
		if err := tr.releaseCapacityReservationBinding(test.ID, slotID); err != nil {
			tr.enqueueCapacityReservationReleaseRetry(test.ID, slotID)
		}
	}
}

func managedTestFromRecord(record *storage.TestRecord) *ManagedTest {
	managedTest := &ManagedTest{
		ID:                    record.ID,
		RefKey:                record.RefKey,
		Name:                  record.Name,
		Status:                record.Status,
		ApplicationID:         record.ApplicationID,
		ReleaseID:             record.ReleaseID,
		ScenarioID:            record.ScenarioID,
		ProjectID:             record.ProjectID,
		FolderID:              record.FolderID,
		Groups:                decodeStoredGroups(record.Groups),
		Assignment:            decodeStoredGeneratorAssignment(record.Assignment),
		ScenarioMode:          normalizeScenarioMode(record.ScenarioMode),
		ScriptShakeout:        record.ScriptShakeout,
		ResourceBaseline:      record.ResourceBaseline,
		StartDelay:            record.StartDelay,
		GoalConfig:            decodeStoredGoalConfig(record.GoalConfig),
		RuntimeSettings:       decodeStoredRuntimeSettings(record.RuntimeSettings),
		TotalVUsers:           record.TotalVUsers,
		Duration:              time.Duration(record.Duration),
		DurationStr:           time.Duration(record.Duration).String(),
		CreatedAt:             record.StartTime,
		ScheduledStartAt:      cloneTimePtr(record.ScheduledStartAt),
		TimeslotID:            cloneStringPtr(record.TimeslotID),
		PendingSince:          record.PendingSince,
		BaselinePinned:        record.BaselinePinned,
		BaselinePinnedAt:      record.BaselinePinnedAt,
		CreatedBySubject:      record.CreatedBySubject,
		CreatedByDisplayName:  record.CreatedByDisplayName,
		StartedBySubject:      record.StartedBySubject,
		StartedByDisplayName:  record.StartedByDisplayName,
		StoppedBySubject:      record.StoppedBySubject,
		StoppedByDisplayName:  record.StoppedByDisplayName,
		ExecutionPlanApproval: decodeStoredExecutionPlanApproval(record.ExecutionPlanApproval),
		Error:                 record.Error,
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
	var history []any
	var err error
	if limited, ok := tr.historyStore.(limitedHistoryPersistence); ok {
		history, err = limited.GetHistoryLimit(test.ID, maxManagedTestHistory)
	} else {
		history, err = tr.historyStore.GetHistory(test.ID)
	}
	if err != nil {
		// Best-effort at startup, but never swallow silently: a dialect break
		// here (e.g. a Postgres-incompatible query) would otherwise blank every
		// run's history with no trace.
		slog.Warn("failed to load stored test history", "test_id", test.ID, "error", err)
		return
	}
	for _, entry := range history {
		switch metrics := entry.(type) {
		case storage.IntervalSummaryRecord:
			test.History = append(test.History, intervalSummaryRecordToLiveMetrics(metrics))
		case *storage.IntervalSummaryRecord:
			if metrics != nil {
				test.History = append(test.History, intervalSummaryRecordToLiveMetrics(*metrics))
			}
		case map[string]any:
			// This is a bit lossy due to any -> map conversion,
			// but works for basic history display.
			test.History = append(test.History, mapToLiveMetrics(metrics))
		}
	}
	if len(test.History) > maxManagedTestHistory {
		test.History = append([]*LiveMetrics(nil), test.History[len(test.History)-maxManagedTestHistory:]...)
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
	if !runArchiveRecordAvailable(archive) {
		return
	}
	test.Archive = archive
	tr.archives[test.ID] = archive
}

func storedScenarioToStorage(stored *StoredScenario) *storage.Scenario {
	if stored == nil {
		return nil
	}
	return &storage.Scenario{
		ID:               stored.ID,
		RefKey:           stored.RefKey,
		Name:             stored.Name,
		Duration:         stored.Duration,
		StartDelay:       stored.StartDelay,
		ScenarioMode:     stored.ScenarioMode,
		ScriptShakeout:   stored.ScriptShakeout,
		ResourceBaseline: stored.ResourceBaseline,
		Assignment:       generatorAssignmentForStorage(stored.Assignment),
		GoalConfig:       goalConfigForStorage(stored.GoalConfig),
		RuntimeSettings:  runtimeSettingsForStorage(stored.RuntimeSettings),
		CreatedAt:        stored.CreatedAt,
		UpdatedAt:        stored.UpdatedAt,
	}
}

func storedScenarioRefsToStorage(stored *StoredScenario) []storage.ScenarioScript {
	if stored == nil || len(stored.Scripts) == 0 {
		return nil
	}
	scripts := make([]storage.ScenarioScript, 0, len(stored.Scripts))
	for _, ref := range stored.Scripts {
		scripts = append(scripts, storage.ScenarioScript{
			ScenarioID:      stored.ID,
			Order:           ref.Order,
			Name:            ref.Name,
			ScriptID:        ref.ScriptID,
			Role:            ref.Role,
			VUsers:          ref.VUsers,
			RampUp:          ref.RampUp,
			RampDown:        ref.RampDown,
			Stages:          cloneLoadStages(ref.Stages),
			Iterations:      ref.Iterations,
			Initialization:  ref.Initialization,
			RuntimeSettings: runtimeSettingsForStorage(ref.RuntimeSettings),
			Assignment:      generatorAssignmentForStorage(ref.Assignment),
		})
	}
	return scripts
}

func cloneScenarioScriptRefs(in []ScenarioScriptRef) []ScenarioScriptRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]ScenarioScriptRef, len(in))
	copy(out, in)
	for i := range out {
		out[i].Stages = cloneLoadStages(in[i].Stages)
		if in[i].Iterations != nil {
			iterations := *in[i].Iterations
			out[i].Iterations = &iterations
		}
		out[i].RuntimeSettings = cloneRuntimeSettings(in[i].RuntimeSettings)
		out[i].Assignment = cloneGeneratorAssignmentSettings(in[i].Assignment)
	}
	return out
}

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

func intervalSummaryRecordToLiveMetrics(record storage.IntervalSummaryRecord) *LiveMetrics {
	live := &LiveMetrics{
		Timestamp:       record.Timestamp,
		ActiveVUsers:    record.ActiveVUsers,
		TPS:             record.TPS,
		AvgResponseMs:   record.AvgResponseMs,
		ErrorPct:        record.ErrorPct,
		TotalTx:         record.TotalTx,
		TotalErrors:     record.TotalErrors,
		P50Ms:           record.P50Ms,
		P90Ms:           record.P90Ms,
		P95Ms:           record.P95Ms,
		P99Ms:           record.P99Ms,
		Browser:         browserSummaryRecordToLiveMetrics(record.Browser),
		BrowserByDevice: browserDeviceSummaryRecordToLiveMetrics(record.BrowserByDevice),
		Latency:         networkLatencyRecordToLiveMetrics(record.Latency),
	}
	if record.DroppedResults > 0 || record.WebsocketClients > 0 || record.WebsocketDroppedMessages > 0 || record.WebsocketDroppedEvents > 0 || !record.CollectedAt.IsZero() || !record.GeneratorPublishedAt.IsZero() || !record.ControllerReceivedAt.IsZero() || !record.PublishedAt.IsZero() || record.SampleAgeMs > 0 || record.GeneratorProcessingLagMs > 0 || record.TransportLagMs > 0 || record.ControllerFanoutLagMs > 0 {
		live.Telemetry = &TelemetryHealth{
			DroppedResults:           record.DroppedResults,
			WebsocketClients:         record.WebsocketClients,
			WebsocketDroppedMessages: record.WebsocketDroppedMessages,
			WebsocketDroppedEvents:   record.WebsocketDroppedEvents,
			GeneratorPublishedAt:     record.GeneratorPublishedAt,
			ControllerReceivedAt:     record.ControllerReceivedAt,
			CollectedAt:              record.CollectedAt,
			PublishedAt:              record.PublishedAt,
			GeneratorProcessingLagMs: record.GeneratorProcessingLagMs,
			TransportLagMs:           record.TransportLagMs,
			ControllerFanoutLagMs:    record.ControllerFanoutLagMs,
			SampleAgeMs:              record.SampleAgeMs,
		}
	}
	return live
}

func browserSummaryRecordToLiveMetrics(record *storage.BrowserMetricsSummaryRecord) *BrowserLiveMetrics {
	if record == nil {
		return nil
	}
	return &BrowserLiveMetrics{
		Samples:              record.Samples,
		AvgFCPMs:             record.AvgFCPMs,
		AvgLCPMs:             record.AvgLCPMs,
		AvgCLSScore:          record.AvgCLSScore,
		AvgINPMs:             record.AvgINPMs,
		AvgTTFBMs:            record.AvgTTFBMs,
		AvgBlockingResources: record.AvgBlockingResources,
		AvgLongTaskCount:     record.AvgLongTaskCount,
		AvgJSHeapUsedMB:      record.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: record.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: record.AvgBrowserProcessMem,
	}
}

func browserDeviceSummaryRecordToLiveMetrics(records map[string]*storage.BrowserDeviceMetricsRecord) map[string]*BrowserDeviceLiveMetrics {
	if len(records) == 0 {
		return nil
	}
	result := make(map[string]*BrowserDeviceLiveMetrics, len(records))
	for name, record := range records {
		if record == nil {
			continue
		}
		result[name] = &BrowserDeviceLiveMetrics{
			DeviceProfile:        record.DeviceProfile,
			DevicePlatform:       record.DevicePlatform,
			DeviceTier:           record.DeviceTier,
			BrowserEngine:        record.BrowserEngine,
			Samples:              record.Samples,
			AvgFCPMs:             record.AvgFCPMs,
			AvgLCPMs:             record.AvgLCPMs,
			AvgCLSScore:          record.AvgCLSScore,
			AvgINPMs:             record.AvgINPMs,
			AvgTTFBMs:            record.AvgTTFBMs,
			AvgBlockingResources: record.AvgBlockingResources,
			AvgLongTaskCount:     record.AvgLongTaskCount,
			AvgJSHeapUsedMB:      record.AvgJSHeapUsedMB,
			AvgBrowserProcessCPU: record.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: record.AvgBrowserProcessMem,
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func networkLatencyRecordToLiveMetrics(record *storage.NetworkLatencySummaryRecord) *NetworkLatency {
	if record == nil {
		return nil
	}
	latency := &NetworkLatency{Targets: make([]LatencyTarget, 0, len(record.Targets))}
	for _, target := range record.Targets {
		latency.Targets = append(latency.Targets, LatencyTarget{
			Name:           target.Name,
			Endpoint:       target.Endpoint,
			LatencyMs:      target.LatencyMs,
			ProbeTotalMs:   target.ProbeTotalMs,
			DNSMs:          target.DNSMs,
			ConnectMs:      target.ConnectMs,
			TLSMs:          target.TLSMs,
			TTFBMs:         target.TTFBMs,
			HTTPStatus:     target.HTTPStatus,
			ProbeMethod:    target.ProbeMethod,
			IP:             target.IP,
			Hops:           target.Hops,
			LastSuccess:    target.LastSuccess,
			JitterMs:       target.JitterMs,
			SuccessRatePct: target.SuccessRatePct,
			SampleCount:    target.SampleCount,
			RouteStatus:    target.RouteStatus,
			Error:          target.Error,
			Timestamp:      target.Timestamp,
		})
	}
	return latency
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

func scenarioRefsFromSet(set map[string]ScenarioRef) []ScenarioRef {
	out := make([]ScenarioRef, 0, len(set))
	for _, scenario := range set {
		out = append(out, scenario)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
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

// forgetProject removes successfully deleted project data from the controller's
// in-memory indexes so deleted assets are not served until the next restart.
func (tr *TestRunner) forgetProject(projectID string) {
	projectID = normalizeProjectID(projectID)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for id, script := range tr.scripts {
		if script != nil && normalizeProjectID(script.ProjectID) == projectID {
			tr.removeRefKeyLocked(script.RefKey)
			delete(tr.scripts, id)
		}
	}
	for id, scenario := range tr.scenarios {
		if scenario != nil && normalizeProjectID(scenario.ProjectID) == projectID {
			tr.removeRefKeyLocked(scenario.RefKey)
			delete(tr.scenarios, id)
		}
	}
	for id, test := range tr.tests {
		if test != nil && normalizeProjectID(test.ProjectID) == projectID {
			tr.removeRefKeyLocked(test.RefKey)
			delete(tr.tests, id)
			delete(tr.archives, id)
			forgetRunArchiveCache(id)
			deleteGroupMetricsHistoryCache(id)
		}
	}
}

func (tr *TestRunner) saveStoredScript(script *dsl.Script) error {
	if tr.scriptStore != nil {
		return tr.scriptStore.SaveScript(script)
	}
	return nil
}

func (tr *TestRunner) deleteStoredScript(id string) error {
	if tr.scriptStore != nil {
		return tr.scriptStore.DeleteScript(id)
	}
	return nil
}

func buildManagedTestRecord(test *ManagedTest) *storage.TestRecord {
	if test == nil {
		return nil
	}
	return &storage.TestRecord{
		ID:                    test.ID,
		RefKey:                test.RefKey,
		Name:                  test.Name,
		Status:                test.Status,
		ApplicationID:         test.ApplicationID,
		ReleaseID:             test.ReleaseID,
		ScenarioID:            test.ScenarioID,
		ProjectID:             normalizeProjectID(test.ProjectID),
		FolderID:              test.FolderID,
		ScenarioMode:          test.ScenarioMode,
		ScriptShakeout:        test.ScriptShakeout,
		ResourceBaseline:      test.ResourceBaseline,
		StartDelay:            test.StartDelay,
		Assignment:            test.Assignment,
		TotalVUsers:           test.TotalVUsers,
		Duration:              int64(test.Duration),
		StartTime:             test.CreatedAt,
		ScheduledStartAt:      cloneTimePtr(test.ScheduledStartAt),
		TimeslotID:            cloneStringPtr(test.TimeslotID),
		PendingSince:          test.PendingSince,
		BaselinePinned:        test.BaselinePinned,
		BaselinePinnedAt:      test.BaselinePinnedAt,
		CreatedBySubject:      test.CreatedBySubject,
		CreatedByDisplayName:  test.CreatedByDisplayName,
		StartedBySubject:      test.StartedBySubject,
		StartedByDisplayName:  test.StartedByDisplayName,
		StoppedBySubject:      test.StoppedBySubject,
		StoppedByDisplayName:  test.StoppedByDisplayName,
		ExecutionPlanApproval: test.ExecutionPlanApproval,
		Groups:                test.Groups,
		GoalConfig:            test.GoalConfig,
		RuntimeSettings:       test.RuntimeSettings,
	}
}

func (tr *TestRunner) saveManagedTestRecord(test *ManagedTest) error {
	if tr.testStore != nil {
		return tr.testStore.SaveTest(buildManagedTestRecord(test))
	}
	return nil
}

func (tr *TestRunner) AttributeTestCreated(id string, subject string, displayName string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousSubject := test.CreatedBySubject
	previousDisplayName := test.CreatedByDisplayName
	test.CreatedBySubject = strings.TrimSpace(subject)
	test.CreatedByDisplayName = strings.TrimSpace(displayName)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.CreatedBySubject = previousSubject
		test.CreatedByDisplayName = previousDisplayName
		slog.Warn("failed to persist test creator attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) AttributeTestStarted(id string, subject string, displayName string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousSubject := test.StartedBySubject
	previousDisplayName := test.StartedByDisplayName
	test.StartedBySubject = strings.TrimSpace(subject)
	test.StartedByDisplayName = strings.TrimSpace(displayName)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.StartedBySubject = previousSubject
		test.StartedByDisplayName = previousDisplayName
		slog.Warn("failed to persist test starter attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) AttributeTestStopped(id string, subject string, displayName string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousSubject := test.StoppedBySubject
	previousDisplayName := test.StoppedByDisplayName
	test.StoppedBySubject = strings.TrimSpace(subject)
	test.StoppedByDisplayName = strings.TrimSpace(displayName)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.StoppedBySubject = previousSubject
		test.StoppedByDisplayName = previousDisplayName
		slog.Warn("failed to persist test stopper attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) AttributeTestExecutionPlanApproval(id string, approval *storage.ExecutionPlanApproval) {
	snapshot := runArchiveExecutionPlanApprovalSnapshot(approval)
	if snapshot == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousApproval := test.ExecutionPlanApproval
	test.ExecutionPlanApproval = snapshot
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.ExecutionPlanApproval = previousApproval
		slog.Warn("failed to persist execution-plan approval attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) deleteStoredTest(id string) error {
	if tr.testStore != nil {
		return tr.testStore.DeleteTest(id)
	}
	return nil
}

func (tr *TestRunner) saveLiveMetricsHistory(testID string, timestamp time.Time, live *LiveMetrics) error {
	if tr.historyStore != nil {
		return tr.historyStore.SaveHistory(testID, timestamp, live)
	}
	return nil
}

func (tr *TestRunner) updateStoredTestStatus(testID string, status string, endTime *time.Time, errStr string) error {
	if err := tr.persistStoredTestStatus(testID, status, endTime, errStr); err != nil {
		return err
	}
	// A terminal status is visible to admission only after the durable test row
	// commits. Releasing the reservation first would let new work consume capacity
	// while a failover controller still sees the run as active.
	if err := tr.releaseCapacityReservationForTerminalStatus(testID, status); err != nil {
		tr.mu.RLock()
		slotID := ""
		if test := tr.tests[testID]; test != nil && test.TimeslotID != nil {
			slotID = strings.TrimSpace(*test.TimeslotID)
		}
		tr.mu.RUnlock()
		tr.enqueueCapacityReservationReleaseRetry(testID, slotID)
	}
	return nil
}

// persistStoredTestStatus writes only the test transition. Callers that hold
// tr.mu use this helper and release secondary resources after unlocking, avoiding
// the recursive tr.mu read taken by releaseCapacityReservationForTerminalStatus.
func (tr *TestRunner) persistStoredTestStatus(testID string, status string, endTime *time.Time, errStr string) error {
	if tr.testStore == nil {
		return nil
	}
	return retryDurableTransition(func() error {
		return tr.testStore.UpdateTestStatus(testID, status, endTime, errStr)
	})
}

func (tr *TestRunner) saveRunArchiveRecord(record *storage.RunArchiveRecord) error {
	if tr.archiveStore == nil || record == nil {
		return nil
	}
	return tr.archiveStore.SaveRunArchive(record)
}

func (tr *TestRunner) captureUsageSnapshotForTerminalRun(testID string, projectID string) {
	if tr == nil || tr.usageStore == nil {
		return
	}
	projectID = normalizeProjectID(projectID)
	if _, err := tr.captureUsageSnapshotForCadence(projectID); err != nil {
		slog.Warn("failed to save usage accounting snapshot", "test_id", testID, "project_id", projectID, "error", err)
	}
}

func (tr *TestRunner) captureUsageSnapshotForCadence(projectID string) (*storage.UsageAccountingSnapshot, error) {
	if tr == nil || tr.usageStore == nil {
		return nil, nil
	}
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	tr.mu.RLock()
	tests := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		tests = append(tests, test)
	}
	tr.mu.RUnlock()
	return tr.saveUsageAccountingSnapshotFromTests(projectID, tests)
}

func (tr *TestRunner) saveUsageAccountingSnapshotFromTests(projectID string, tests []*ManagedTest) (*storage.UsageAccountingSnapshot, error) {
	usage := usageAccountingFromTests(projectID, tests)
	payload, err := json.Marshal(usage)
	if err != nil {
		return nil, fmt.Errorf("encode usage accounting snapshot: %w", err)
	}
	now := time.Now().UTC()
	snapshot := &storage.UsageAccountingSnapshot{
		ID:          uuid.NewString(),
		ProjectID:   projectID,
		GeneratedAt: usage.GeneratedAt,
		PayloadJSON: string(payload),
		CreatedAt:   now,
	}
	if err := tr.usageStore.SaveUsageAccountingSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (tr *TestRunner) UsageAccountingSnapshotInterval() time.Duration {
	if tr == nil {
		return 0
	}
	seconds := tr.GetSystemSettings().General.UsageSnapshotIntervalSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (tr *TestRunner) RunUsageAccountingSnapshotCadence(ctx context.Context, ticks <-chan time.Time) {
	if tr == nil || ticks == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if _, err := tr.captureUsageSnapshotForCadence(""); err != nil {
				slog.Warn("failed to save cadence usage accounting snapshot", "error", err)
			}
		}
	}
}
