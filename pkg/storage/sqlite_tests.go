package storage

// SQLite store — tests. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveTest(test *TestRecord) error {
	test.ApplicationID = normalizeApplicationID(test.ApplicationID)
	test.ReleaseID = normalizeReleaseID(test.ReleaseID)
	test.ProjectID = normalizeProjectID(test.ProjectID)
	groupsJSON, _ := json.Marshal(test.Groups)
	assignmentJSON, _ := json.Marshal(test.Assignment)
	runtimeSettingsJSON, _ := json.Marshal(test.RuntimeSettings)
	goalConfigJSON, _ := json.Marshal(test.GoalConfig)
	executionPlanApprovalJSON, _ := json.Marshal(test.ExecutionPlanApproval)

	query := `INSERT INTO tests (id, ref_key, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, shakeout_enabled, resource_baseline_enabled, assignment_json, total_vusers, duration_ns, start_time, scheduled_start_at, pending_since, baseline_pinned, baseline_pinned_at, end_time, error, created_by_subject, created_by_display_name, started_by_subject, started_by_display_name, stopped_by_subject, stopped_by_display_name, groups_json, runtime_settings_json, goal_config_json, execution_plan_approval_json, timeslot_id)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  ref_key=excluded.ref_key,
			  name=excluded.name, status=excluded.status, application_id=excluded.application_id, release_id=excluded.release_id, scenario_id=excluded.scenario_id,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  scenario_mode=excluded.scenario_mode, shakeout_enabled=excluded.shakeout_enabled,
			  resource_baseline_enabled=excluded.resource_baseline_enabled, assignment_json=excluded.assignment_json, total_vusers=excluded.total_vusers,
			  duration_ns=excluded.duration_ns, start_time=excluded.start_time, scheduled_start_at=excluded.scheduled_start_at, pending_since=excluded.pending_since,
			  baseline_pinned=excluded.baseline_pinned, baseline_pinned_at=excluded.baseline_pinned_at, end_time=excluded.end_time,
			  error=excluded.error, created_by_subject=excluded.created_by_subject, created_by_display_name=excluded.created_by_display_name,
			  started_by_subject=excluded.started_by_subject, started_by_display_name=excluded.started_by_display_name,
			  stopped_by_subject=excluded.stopped_by_subject, stopped_by_display_name=excluded.stopped_by_display_name, groups_json=excluded.groups_json,
			  runtime_settings_json=excluded.runtime_settings_json, goal_config_json=excluded.goal_config_json, execution_plan_approval_json=excluded.execution_plan_approval_json,
			  timeslot_id=excluded.timeslot_id`

	_, err := s.db.Exec(query, test.ID, test.RefKey, test.Name, test.Status, test.ApplicationID, test.ReleaseID, test.ScenarioID, test.ProjectID, test.FolderID, test.ScenarioMode, test.ScriptShakeout, test.ResourceBaseline, string(assignmentJSON), test.TotalVUsers, test.Duration, test.StartTime, test.ScheduledStartAt, test.PendingSince, test.BaselinePinned, test.BaselinePinnedAt, test.EndTime, test.Error, test.CreatedBySubject, test.CreatedByDisplayName, test.StartedBySubject, test.StartedByDisplayName, test.StoppedBySubject, test.StoppedByDisplayName, string(groupsJSON), string(runtimeSettingsJSON), string(goalConfigJSON), string(executionPlanApprovalJSON), test.TimeslotID)
	return err
}

func (s *SQLiteStore) GetTest(id string) (*TestRecord, error) {
	query := testSelectColumns() + ` FROM tests WHERE id = ?`
	test, err := scanTest(s.db.QueryRow(query, id))
	if err != nil {
		return nil, err
	}
	return &test, nil
}

func testSelectColumns() string {
	return `SELECT id, COALESCE(ref_key, ''), name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), total_vusers, duration_ns, start_time, scheduled_start_at, pending_since, COALESCE(baseline_pinned, 0), baseline_pinned_at, end_time, error, COALESCE(created_by_subject, ''), COALESCE(created_by_display_name, ''), COALESCE(started_by_subject, ''), COALESCE(started_by_display_name, ''), COALESCE(stopped_by_subject, ''), COALESCE(stopped_by_display_name, ''), groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, ''), COALESCE(execution_plan_approval_json, ''), timeslot_id`
}

// rowScanner abstracts *sql.Row and *sql.Rows so scan helpers work with both
// QueryRow and Query results.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTest scans a full test row (matching testSelectColumns) and decodes its
// JSON-encoded fields. Shared by GetTest, ListTests, and ListTestsByContext.
func scanTest(scanner rowScanner) (TestRecord, error) {
	var test TestRecord
	var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON, executionPlanApprovalJSON string
	if err := scanner.Scan(&test.ID, &test.RefKey, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.ScheduledStartAt, &test.PendingSince, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &test.CreatedBySubject, &test.CreatedByDisplayName, &test.StartedBySubject, &test.StartedByDisplayName, &test.StoppedBySubject, &test.StoppedByDisplayName, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON, &executionPlanApprovalJSON, &test.TimeslotID); err != nil {
		return test, err
	}
	json.Unmarshal([]byte(groupsJSON), &test.Groups)
	if assignmentJSON != "" && assignmentJSON != "null" {
		json.Unmarshal([]byte(assignmentJSON), &test.Assignment)
	}
	if runtimeSettingsJSON != "" && runtimeSettingsJSON != "null" {
		json.Unmarshal([]byte(runtimeSettingsJSON), &test.RuntimeSettings)
	}
	if goalConfigJSON != "" && goalConfigJSON != "null" {
		json.Unmarshal([]byte(goalConfigJSON), &test.GoalConfig)
	}
	if executionPlanApprovalJSON != "" && executionPlanApprovalJSON != "null" {
		json.Unmarshal([]byte(executionPlanApprovalJSON), &test.ExecutionPlanApproval)
	}
	return test, nil
}

func (s *SQLiteStore) ListTests(projectID string, folderID *string) ([]*TestRecord, error) {
	args := []any{}
	query := testSelectColumns() + ` FROM tests`
	var filters []string
	if projectID != "" {
		filters = append(filters, `project_id = ?`)
		args = append(args, projectID)
	}
	if folderID != nil {
		if *folderID == "" {
			filters = append(filters, `folder_id IS NULL`)
		} else {
			filters = append(filters, `folder_id = ?`)
			args = append(args, *folderID)
		}
	}
	if len(filters) > 0 {
		query += ` WHERE ` + strings.Join(filters, ` AND `)
	}
	query += ` ORDER BY start_time DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tests []*TestRecord
	for rows.Next() {
		test, err := scanTest(rows)
		if err != nil {
			return nil, err
		}
		tests = append(tests, &test)
	}
	return tests, nil
}

func (s *SQLiteStore) ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*TestRecord, error) {
	args := []any{}
	query := testSelectColumns() + ` FROM tests`
	var filters []string
	if applicationID != "" {
		filters = append(filters, `application_id = ?`)
		args = append(args, applicationID)
	}
	if releaseID != "" {
		filters = append(filters, `release_id = ?`)
		args = append(args, releaseID)
	}
	if scenarioID != "" {
		filters = append(filters, `scenario_id = ?`)
		args = append(args, scenarioID)
	}
	if len(filters) > 0 {
		query += ` WHERE ` + strings.Join(filters, ` AND `)
	}
	query += ` ORDER BY start_time DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tests []*TestRecord
	for rows.Next() {
		test, err := scanTest(rows)
		if err != nil {
			return nil, err
		}
		tests = append(tests, &test)
	}
	return tests, rows.Err()
}

func (s *SQLiteStore) UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error {
	_, err := s.db.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ? WHERE id = ?`, status, endTime, errStr, id)
	return err
}

func (s *SQLiteStore) DeleteTest(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM test_history WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM test_notes WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM test_interval_summaries WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM run_archives WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tests WHERE id = ?`, id); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLiteStore) MoveTest(id string, folderID *string) error {
	_, err := s.db.Exec(`UPDATE tests SET folder_id = ? WHERE id = ?`, folderID, id)
	return err
}

func (s *SQLiteStore) SaveHistory(testID string, timestamp time.Time, metrics any) error {
	record, err := intervalSummaryRecordFromMetrics(timestamp, metrics)
	if err == nil {
		browserJSON, err := marshalNullableJSON(record.Browser)
		if err != nil {
			return err
		}
		browserByDeviceJSON, err := marshalNullableJSON(record.BrowserByDevice)
		if err != nil {
			return err
		}
		latencyJSON, err := marshalNullableJSON(record.Latency)
		if err != nil {
			return err
		}
		var collectedAt any
		if !record.CollectedAt.IsZero() {
			collectedAt = record.CollectedAt
		}
		var publishedAt any
		if !record.PublishedAt.IsZero() {
			publishedAt = record.PublishedAt
		}
		var generatorPublishedAt any
		if !record.GeneratorPublishedAt.IsZero() {
			generatorPublishedAt = record.GeneratorPublishedAt
		}
		var controllerReceivedAt any
		if !record.ControllerReceivedAt.IsZero() {
			controllerReceivedAt = record.ControllerReceivedAt
		}
		_, err = s.db.Exec(`INSERT INTO test_interval_summaries (
			test_id, schema_version, timestamp, active_vusers, tps, avg_response_ms, error_pct,
			total_transactions, total_errors, p50_ms, p90_ms, p95_ms, p99_ms,
			browser_json, browser_by_device_json, latency_json,
			dropped_results, websocket_clients, websocket_dropped_messages, websocket_dropped_events,
			generator_published_at, controller_received_at, collected_at, published_at,
			generator_processing_lag_ms, transport_lag_ms, controller_fanout_lag_ms, sample_age_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			testID,
			record.SchemaVersion,
			record.Timestamp,
			record.ActiveVUsers,
			record.TPS,
			record.AvgResponseMs,
			record.ErrorPct,
			record.TotalTx,
			record.TotalErrors,
			record.P50Ms,
			record.P90Ms,
			record.P95Ms,
			record.P99Ms,
			browserJSON,
			browserByDeviceJSON,
			latencyJSON,
			record.DroppedResults,
			record.WebsocketClients,
			record.WebsocketDroppedMessages,
			record.WebsocketDroppedEvents,
			generatorPublishedAt,
			controllerReceivedAt,
			collectedAt,
			publishedAt,
			record.GeneratorProcessingLagMs,
			record.TransportLagMs,
			record.ControllerFanoutLagMs,
			record.SampleAgeMs,
		)
		return err
	}

	metricsJSON, _ := json.Marshal(metrics)
	_, err = s.db.Exec(`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, testID, timestamp, string(metricsJSON))
	return err
}

func (s *SQLiteStore) GetHistory(testID string) ([]any, error) {
	return s.GetHistoryLimit(testID, 0)
}

func (s *SQLiteStore) GetHistoryLimit(testID string, limit int) ([]any, error) {
	typedHistory, err := s.getTypedHistoryLimit(testID, limit)
	if err != nil {
		return nil, err
	}
	if len(typedHistory) > 0 {
		return typedHistory, nil
	}

	query := `SELECT metrics_json FROM test_history WHERE test_id = ? ORDER BY timestamp ASC`
	args := []any{testID}
	if limit > 0 {
		// PostgreSQL requires an alias on a subquery in FROM; SQLite does not.
		// rewriteSQL() does not synthesize one, so it must be written here or the
		// Postgres dialect rejects the query.
		query = `SELECT metrics_json FROM (
			SELECT timestamp, metrics_json FROM test_history
			WHERE test_id = ?
			ORDER BY timestamp DESC
			LIMIT ?
		) AS recent ORDER BY timestamp ASC`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []any
	for rows.Next() {
		var metricsJSON string
		if err := rows.Scan(&metricsJSON); err != nil {
			return nil, err
		}
		var metrics any
		json.Unmarshal([]byte(metricsJSON), &metrics)
		history = append(history, metrics)
	}
	return history, nil
}

func (s *SQLiteStore) getTypedHistory(testID string) ([]any, error) {
	return s.getTypedHistoryLimit(testID, 0)
}

func (s *SQLiteStore) getTypedHistoryLimit(testID string, limit int) ([]any, error) {
	query := `SELECT
		schema_version,
		timestamp,
		active_vusers,
		tps,
		avg_response_ms,
		error_pct,
		total_transactions,
		total_errors,
		p50_ms,
		p90_ms,
		p95_ms,
		p99_ms,
		browser_json,
		browser_by_device_json,
		latency_json,
		dropped_results,
		websocket_clients,
		websocket_dropped_messages,
		websocket_dropped_events,
		generator_published_at,
		controller_received_at,
		collected_at,
		published_at,
		generator_processing_lag_ms,
		transport_lag_ms,
		controller_fanout_lag_ms,
		sample_age_ms
	FROM test_interval_summaries WHERE test_id = ?`
	args := []any{testID}
	if limit > 0 {
		// Alias the derived table so the Postgres dialect accepts it (SQLite is
		// lenient; rewriteSQL() does not add one). See GetHistoryLimit above.
		query = `SELECT * FROM (` + query + ` ORDER BY timestamp DESC LIMIT ?) AS recent ORDER BY timestamp ASC`
		args = append(args, limit)
	} else {
		query += ` ORDER BY timestamp ASC`
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]any, 0)
	for rows.Next() {
		var record IntervalSummaryRecord
		var browserJSON sql.NullString
		var browserByDeviceJSON sql.NullString
		var latencyJSON sql.NullString
		var droppedResults sql.NullInt64
		var websocketClients sql.NullInt64
		var websocketDroppedMessages sql.NullInt64
		var websocketDroppedEvents sql.NullInt64
		var generatorPublishedAt sql.NullTime
		var controllerReceivedAt sql.NullTime
		var collectedAt sql.NullTime
		var publishedAt sql.NullTime
		var generatorProcessingLagMs sql.NullFloat64
		var transportLagMs sql.NullFloat64
		var controllerFanoutLagMs sql.NullFloat64
		var sampleAgeMs sql.NullFloat64
		var schemaVersion sql.NullString
		if err := rows.Scan(
			&schemaVersion,
			&record.Timestamp,
			&record.ActiveVUsers,
			&record.TPS,
			&record.AvgResponseMs,
			&record.ErrorPct,
			&record.TotalTx,
			&record.TotalErrors,
			&record.P50Ms,
			&record.P90Ms,
			&record.P95Ms,
			&record.P99Ms,
			&browserJSON,
			&browserByDeviceJSON,
			&latencyJSON,
			&droppedResults,
			&websocketClients,
			&websocketDroppedMessages,
			&websocketDroppedEvents,
			&generatorPublishedAt,
			&controllerReceivedAt,
			&collectedAt,
			&publishedAt,
			&generatorProcessingLagMs,
			&transportLagMs,
			&controllerFanoutLagMs,
			&sampleAgeMs,
		); err != nil {
			return nil, err
		}
		record.SchemaVersion = normalizeTelemetrySchemaVersion(schemaVersion.String, intervalSummarySchemaVersionV1)
		if browserJSON.Valid && strings.TrimSpace(browserJSON.String) != "" {
			if err := json.Unmarshal([]byte(browserJSON.String), &record.Browser); err != nil {
				return nil, err
			}
		}
		if browserByDeviceJSON.Valid && strings.TrimSpace(browserByDeviceJSON.String) != "" {
			if err := json.Unmarshal([]byte(browserByDeviceJSON.String), &record.BrowserByDevice); err != nil {
				return nil, err
			}
		}
		if latencyJSON.Valid && strings.TrimSpace(latencyJSON.String) != "" {
			if err := json.Unmarshal([]byte(latencyJSON.String), &record.Latency); err != nil {
				return nil, err
			}
		}
		if droppedResults.Valid {
			record.DroppedResults = droppedResults.Int64
		}
		if websocketClients.Valid {
			record.WebsocketClients = int(websocketClients.Int64)
		}
		if websocketDroppedMessages.Valid {
			record.WebsocketDroppedMessages = websocketDroppedMessages.Int64
		}
		if websocketDroppedEvents.Valid {
			record.WebsocketDroppedEvents = websocketDroppedEvents.Int64
		}
		if generatorPublishedAt.Valid {
			record.GeneratorPublishedAt = generatorPublishedAt.Time
		}
		if controllerReceivedAt.Valid {
			record.ControllerReceivedAt = controllerReceivedAt.Time
		}
		if collectedAt.Valid {
			record.CollectedAt = collectedAt.Time
		}
		if publishedAt.Valid {
			record.PublishedAt = publishedAt.Time
		}
		if generatorProcessingLagMs.Valid {
			record.GeneratorProcessingLagMs = generatorProcessingLagMs.Float64
		}
		if transportLagMs.Valid {
			record.TransportLagMs = transportLagMs.Float64
		}
		if controllerFanoutLagMs.Valid {
			record.ControllerFanoutLagMs = controllerFanoutLagMs.Float64
		}
		if sampleAgeMs.Valid {
			record.SampleAgeMs = sampleAgeMs.Float64
		}
		history = append(history, record)
	}
	return history, rows.Err()
}

func intervalSummaryRecordFromMetrics(timestamp time.Time, metrics any) (IntervalSummaryRecord, error) {
	type browserEnvelope struct {
		Samples              int64   `json:"samples"`
		AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
		AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
		AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
		AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
		AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
		AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
		AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
		AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
		AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
		AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
	}
	type browserDeviceEnvelope struct {
		DeviceProfile        string  `json:"device_profile,omitempty"`
		DevicePlatform       string  `json:"device_platform,omitempty"`
		DeviceTier           string  `json:"device_tier,omitempty"`
		BrowserEngine        string  `json:"browser_engine,omitempty"`
		Samples              int64   `json:"samples"`
		AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
		AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
		AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
		AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
		AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
		AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
		AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
		AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
		AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
		AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
	}
	type telemetryEnvelope struct {
		DroppedResults           int64     `json:"dropped_results,omitempty"`
		WebsocketClients         int       `json:"websocket_clients,omitempty"`
		WebsocketDroppedMessages int64     `json:"websocket_dropped_messages,omitempty"`
		WebsocketDroppedEvents   int64     `json:"websocket_dropped_events,omitempty"`
		GeneratorPublishedAt     time.Time `json:"generator_published_at,omitempty"`
		ControllerReceivedAt     time.Time `json:"controller_received_at,omitempty"`
		CollectedAt              time.Time `json:"collected_at,omitempty"`
		PublishedAt              time.Time `json:"published_at,omitempty"`
		GeneratorProcessingLagMs float64   `json:"generator_processing_lag_ms,omitempty"`
		TransportLagMs           float64   `json:"transport_lag_ms,omitempty"`
		ControllerFanoutLagMs    float64   `json:"controller_fanout_lag_ms,omitempty"`
		SampleAgeMs              float64   `json:"sample_age_ms,omitempty"`
	}
	type summaryEnvelope struct {
		Timestamp       time.Time                         `json:"timestamp"`
		ActiveVUsers    int64                             `json:"active_vusers"`
		TPS             float64                           `json:"tps"`
		AvgResponseMs   float64                           `json:"avg_response_ms"`
		ErrorPct        float64                           `json:"error_pct"`
		TotalTx         int64                             `json:"total_transactions"`
		TotalErrors     int64                             `json:"total_errors"`
		P50Ms           float64                           `json:"p50_ms"`
		P90Ms           float64                           `json:"p90_ms"`
		P95Ms           float64                           `json:"p95_ms"`
		P99Ms           float64                           `json:"p99_ms"`
		Browser         *browserEnvelope                  `json:"browser,omitempty"`
		BrowserByDevice map[string]*browserDeviceEnvelope `json:"browser_by_device,omitempty"`
		Latency         *NetworkLatencySummaryRecord      `json:"latency,omitempty"`
		Telemetry       *telemetryEnvelope                `json:"telemetry,omitempty"`
	}

	payload, err := json.Marshal(metrics)
	if err != nil {
		return IntervalSummaryRecord{}, err
	}
	var summary summaryEnvelope
	if err := json.Unmarshal(payload, &summary); err != nil {
		return IntervalSummaryRecord{}, err
	}
	record := IntervalSummaryRecord{
		SchemaVersion: intervalSummarySchemaVersion,
		Timestamp:     summary.Timestamp,
		ActiveVUsers:  summary.ActiveVUsers,
		TPS:           summary.TPS,
		AvgResponseMs: summary.AvgResponseMs,
		ErrorPct:      summary.ErrorPct,
		TotalTx:       summary.TotalTx,
		TotalErrors:   summary.TotalErrors,
		P50Ms:         summary.P50Ms,
		P90Ms:         summary.P90Ms,
		P95Ms:         summary.P95Ms,
		P99Ms:         summary.P99Ms,
	}
	if summary.Browser != nil {
		record.Browser = &BrowserMetricsSummaryRecord{
			Samples:              summary.Browser.Samples,
			AvgFCPMs:             summary.Browser.AvgFCPMs,
			AvgLCPMs:             summary.Browser.AvgLCPMs,
			AvgCLSScore:          summary.Browser.AvgCLSScore,
			AvgINPMs:             summary.Browser.AvgINPMs,
			AvgTTFBMs:            summary.Browser.AvgTTFBMs,
			AvgBlockingResources: summary.Browser.AvgBlockingResources,
			AvgLongTaskCount:     summary.Browser.AvgLongTaskCount,
			AvgJSHeapUsedMB:      summary.Browser.AvgJSHeapUsedMB,
			AvgBrowserProcessCPU: summary.Browser.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: summary.Browser.AvgBrowserProcessMem,
		}
	}
	if len(summary.BrowserByDevice) > 0 {
		record.BrowserByDevice = make(map[string]*BrowserDeviceMetricsRecord, len(summary.BrowserByDevice))
		for name, metrics := range summary.BrowserByDevice {
			if metrics == nil {
				continue
			}
			record.BrowserByDevice[name] = &BrowserDeviceMetricsRecord{
				DeviceProfile:        metrics.DeviceProfile,
				DevicePlatform:       metrics.DevicePlatform,
				DeviceTier:           metrics.DeviceTier,
				BrowserEngine:        metrics.BrowserEngine,
				Samples:              metrics.Samples,
				AvgFCPMs:             metrics.AvgFCPMs,
				AvgLCPMs:             metrics.AvgLCPMs,
				AvgCLSScore:          metrics.AvgCLSScore,
				AvgINPMs:             metrics.AvgINPMs,
				AvgTTFBMs:            metrics.AvgTTFBMs,
				AvgBlockingResources: metrics.AvgBlockingResources,
				AvgLongTaskCount:     metrics.AvgLongTaskCount,
				AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
				AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
				AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
			}
		}
	}
	if summary.Latency != nil {
		record.Latency = &NetworkLatencySummaryRecord{
			Targets: append([]NetworkLatencyTargetRecord(nil), summary.Latency.Targets...),
		}
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = timestamp
	}
	if summary.Telemetry != nil {
		record.DroppedResults = summary.Telemetry.DroppedResults
		record.WebsocketClients = summary.Telemetry.WebsocketClients
		record.WebsocketDroppedMessages = summary.Telemetry.WebsocketDroppedMessages
		record.WebsocketDroppedEvents = summary.Telemetry.WebsocketDroppedEvents
		record.GeneratorPublishedAt = summary.Telemetry.GeneratorPublishedAt
		record.ControllerReceivedAt = summary.Telemetry.ControllerReceivedAt
		record.CollectedAt = summary.Telemetry.CollectedAt
		record.PublishedAt = summary.Telemetry.PublishedAt
		record.GeneratorProcessingLagMs = summary.Telemetry.GeneratorProcessingLagMs
		record.TransportLagMs = summary.Telemetry.TransportLagMs
		record.ControllerFanoutLagMs = summary.Telemetry.ControllerFanoutLagMs
		record.SampleAgeMs = summary.Telemetry.SampleAgeMs
	}
	return record, nil
}

func marshalNullableJSON(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(encoded) == "null" || string(encoded) == "{}" {
		return nil, nil
	}
	return string(encoded), nil
}
