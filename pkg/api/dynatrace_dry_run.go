package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const dynatraceCorrelationManifestSchemaVersion = "apexload.dynatrace_correlation.v1"

type DynatraceDryRunRequest struct {
	Event          NotificationEvent           `json:"event,omitempty"`
	Webhook        NotificationWebhookSettings `json:"webhook,omitempty"`
	TestID         string                      `json:"test_id,omitempty"`
	EventType      string                      `json:"event_type,omitempty"`
	Status         string                      `json:"status,omitempty"`
	Severity       string                      `json:"severity,omitempty"`
	Summary        string                      `json:"summary,omitempty"`
	Conditions     []string                    `json:"conditions,omitempty"`
	Timestamp      *time.Time                  `json:"timestamp,omitempty"`
	EntitySelector string                      `json:"entity_selector,omitempty"`
	TimeoutMinutes int                         `json:"timeout_minutes,omitempty"`
}

type DynatraceDryRunResponse struct {
	GeneratedAt         time.Time                    `json:"generated_at"`
	DeliveryDisabled    bool                         `json:"delivery_disabled"`
	Endpoint            string                       `json:"endpoint,omitempty"`
	Payload             map[string]any               `json:"payload"`
	CorrelationManifest DynatraceCorrelationManifest `json:"correlation_manifest"`
	RequiredSettings    []string                     `json:"required_settings,omitempty"`
	Notes               []string                     `json:"notes,omitempty"`
}

type DynatraceCorrelationManifest struct {
	SchemaVersion      string            `json:"schema_version"`
	GeneratedAt        time.Time         `json:"generated_at"`
	TestID             string            `json:"test_id,omitempty"`
	TestName           string            `json:"test_name,omitempty"`
	ProjectID          string            `json:"project_id,omitempty"`
	ReleaseID          string            `json:"release_id,omitempty"`
	ScenarioID         string            `json:"scenario_id,omitempty"`
	Status             string            `json:"status,omitempty"`
	EventType          string            `json:"event_type,omitempty"`
	EventProperties    map[string]string `json:"event_properties,omitempty"`
	TraceIDs           []string          `json:"trace_ids,omitempty"`
	SpanIDs            []string          `json:"span_ids,omitempty"`
	DependencyTargets  []string          `json:"dependency_targets,omitempty"`
	ArchiveReportPath  string            `json:"archive_report_path,omitempty"`
	ArchiveRootCause   string            `json:"archive_root_cause_path,omitempty"`
	EvidenceIncomplete bool              `json:"evidence_incomplete,omitempty"`
	EvidenceNote       string            `json:"evidence_note,omitempty"`
}

func (s *Server) handleDynatraceDryRun(w http.ResponseWriter, r *http.Request) {
	var req DynatraceDryRunRequest
	if r.Body != nil {
		defer r.Body.Close()
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}
	event := s.dynatraceDryRunEvent(req)
	webhook := s.dynatraceDryRunWebhook(req)
	payload := buildDynatraceNotificationPayload(webhook, event)
	properties, _ := payload["properties"].(map[string]string)
	writeJSON(w, http.StatusOK, DynatraceDryRunResponse{
		GeneratedAt:         time.Now().UTC(),
		DeliveryDisabled:    true,
		Endpoint:            strings.TrimSpace(webhook.URL),
		Payload:             payload,
		CorrelationManifest: s.buildDynatraceCorrelationManifest(event, properties),
		RequiredSettings: []string{
			"notifications.webhooks[].format=dynatrace",
			"notifications.webhooks[].api_token",
			"notifications.webhooks[].entity_selector",
		},
		Notes: []string{
			"Dry run only: no Dynatrace request is sent.",
			"Use the correlation manifest to validate run, trace, and dependency identity before enabling SaaS delivery.",
		},
	})
}

func (s *Server) dynatraceDryRunEvent(req DynatraceDryRunRequest) NotificationEvent {
	event := req.Event
	testID := firstNonEmpty(strings.TrimSpace(req.TestID), strings.TrimSpace(event.TestID))
	var test *ManagedTest
	if testID != "" {
		if found, ok := s.runner.GetTest(testID); ok {
			test = found
		}
	}
	if strings.TrimSpace(event.Type) == "" {
		event.Type = firstNonEmpty(strings.TrimSpace(req.EventType), "test_complete")
	}
	if strings.TrimSpace(event.TestID) == "" {
		event.TestID = testID
	}
	if test != nil {
		if strings.TrimSpace(event.TestName) == "" {
			event.TestName = test.Name
		}
		if strings.TrimSpace(event.ProjectID) == "" {
			event.ProjectID = test.ProjectID
		}
		if strings.TrimSpace(event.Status) == "" {
			event.Status = test.Status
		}
	}
	if strings.TrimSpace(event.TestName) == "" {
		event.TestName = firstNonEmpty(event.TestID, "Dynatrace Dry Run")
	}
	if strings.TrimSpace(req.Status) != "" {
		event.Status = strings.TrimSpace(req.Status)
	}
	if strings.TrimSpace(event.Status) == "" {
		event.Status = "DRY_RUN"
	}
	if strings.TrimSpace(req.Severity) != "" {
		event.Severity = strings.TrimSpace(req.Severity)
	}
	if strings.TrimSpace(req.Summary) != "" {
		event.Summary = strings.TrimSpace(req.Summary)
	}
	if len(req.Conditions) > 0 {
		event.Conditions = append([]string(nil), req.Conditions...)
	}
	if req.Timestamp != nil {
		event.Timestamp = *req.Timestamp
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return event
}

func (s *Server) dynatraceDryRunWebhook(req DynatraceDryRunRequest) NotificationWebhookSettings {
	webhook := req.Webhook
	if strings.TrimSpace(webhook.Format) == "" {
		webhook.Format = notificationFormatDynatrace
	}
	if strings.TrimSpace(webhook.EntitySelector) == "" {
		webhook.EntitySelector = strings.TrimSpace(req.EntitySelector)
	}
	if webhook.TimeoutMinutes == 0 {
		webhook.TimeoutMinutes = req.TimeoutMinutes
	}
	if strings.TrimSpace(webhook.URL) == "" || strings.TrimSpace(webhook.EntitySelector) == "" || webhook.TimeoutMinutes == 0 {
		if configured, ok := firstConfiguredDynatraceWebhook(s.runner.GetSystemSettings()); ok {
			if strings.TrimSpace(webhook.URL) == "" {
				webhook.URL = configured.URL
			}
			if strings.TrimSpace(webhook.EntitySelector) == "" {
				webhook.EntitySelector = configured.EntitySelector
			}
			if webhook.TimeoutMinutes == 0 {
				webhook.TimeoutMinutes = configured.TimeoutMinutes
			}
		}
	}
	webhook.APIToken = ""
	return webhook
}

func firstConfiguredDynatraceWebhook(settings SystemSettings) (NotificationWebhookSettings, bool) {
	for _, webhook := range settings.Notifications.Webhooks {
		if webhook.Enabled && strings.EqualFold(webhook.Format, notificationFormatDynatrace) {
			return webhook, true
		}
	}
	return NotificationWebhookSettings{}, false
}

func (s *Server) buildDynatraceCorrelationManifest(event NotificationEvent, properties map[string]string) DynatraceCorrelationManifest {
	manifest := DynatraceCorrelationManifest{
		SchemaVersion:   dynatraceCorrelationManifestSchemaVersion,
		GeneratedAt:     time.Now().UTC(),
		TestID:          strings.TrimSpace(event.TestID),
		TestName:        strings.TrimSpace(event.TestName),
		ProjectID:       strings.TrimSpace(event.ProjectID),
		Status:          strings.TrimSpace(event.Status),
		EventType:       strings.TrimSpace(event.Type),
		EventProperties: cloneStringMap(properties),
	}
	if manifest.TestID == "" {
		return manifest
	}
	if test, ok := s.runner.GetTest(manifest.TestID); ok && test != nil {
		manifest.ReleaseID = strings.TrimSpace(test.ReleaseID)
		manifest.ScenarioID = strings.TrimSpace(test.ScenarioID)
		if manifest.ProjectID == "" {
			manifest.ProjectID = strings.TrimSpace(test.ProjectID)
		}
		if manifest.TestName == "" {
			manifest.TestName = strings.TrimSpace(test.Name)
		}
	}
	if record, ok := s.runner.GetRunArchive(manifest.TestID); ok && record != nil {
		manifest.ArchiveReportPath = "/api/v1/tests/" + manifest.TestID + "/archive/report"
		manifest.ArchiveRootCause = "/api/v1/tests/" + manifest.TestID + "/archive/root-cause"
		traceIDs, spanIDs, dependencies, err := streamDynatraceArchiveIdentity(record, 100)
		if err != nil {
			manifest.EvidenceIncomplete = true
			manifest.EvidenceNote = "Archive correlation evidence could not be read completely: " + err.Error()
		} else {
			manifest.TraceIDs = traceIDs
			manifest.SpanIDs = spanIDs
			manifest.DependencyTargets = dependencies
		}
	}
	return manifest
}

func streamDynatraceArchiveIdentity(record *storage.RunArchiveRecord, limit int) ([]string, []string, []string, error) {
	lease, err := acquireArchiveMaterialized(context.Background(), record)
	if err != nil {
		return nil, nil, nil, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, nil, nil, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.TransactionsFile)
	if err != nil {
		return nil, nil, nil, err
	}
	defer file.Close()

	traces := newBoundedSortedStringSet(limit)
	spans := newBoundedSortedStringSet(limit)
	dependencies := newBoundedSortedStringSet(limit)
	scanner := newArchiveJSONLScanner(file)
	budget := newArchiveScanBudget(context.Background(), maxArchiveQueryRecords, maxArchiveQueryBytes)
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return traces.Values(), spans.Values(), dependencies.Values(), err
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var event TransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return traces.Values(), spans.Values(), dependencies.Values(), fmt.Errorf("decode archived transaction: %w", err)
		}
		traces.Add(event.TraceID)
		spans.Add(event.SpanID)
		dependencies.Add(event.DependencyTarget)
	}
	if err := scanner.Err(); err != nil {
		return traces.Values(), spans.Values(), dependencies.Values(), fmt.Errorf("scan archived transactions: %w", err)
	}
	return traces.Values(), spans.Values(), dependencies.Values(), nil
}

type boundedSortedStringSet struct {
	limit  int
	values map[string]struct{}
}

func newBoundedSortedStringSet(limit int) *boundedSortedStringSet {
	return &boundedSortedStringSet{limit: limit, values: make(map[string]struct{}, max(0, limit))}
}

func (set *boundedSortedStringSet) Add(value string) {
	value = strings.TrimSpace(value)
	if value == "" || set.limit <= 0 {
		return
	}
	if _, exists := set.values[value]; exists {
		return
	}
	if len(set.values) < set.limit {
		set.values[value] = struct{}{}
		return
	}
	largest := ""
	for existing := range set.values {
		if existing > largest {
			largest = existing
		}
	}
	if value < largest {
		delete(set.values, largest)
		set.values[value] = struct{}{}
	}
}

func (set *boundedSortedStringSet) Values() []string {
	return firstSortedSetValues(set.values, set.limit)
}

func traceIDSet(transactions []TransactionEvent) map[string]struct{} {
	values := make(map[string]struct{})
	for _, tx := range transactions {
		if strings.TrimSpace(tx.TraceID) != "" {
			values[tx.TraceID] = struct{}{}
		}
	}
	return values
}

func spanIDSet(transactions []TransactionEvent) map[string]struct{} {
	values := make(map[string]struct{})
	for _, tx := range transactions {
		if strings.TrimSpace(tx.SpanID) != "" {
			values[tx.SpanID] = struct{}{}
		}
	}
	return values
}

func dependencyTargetSet(transactions []TransactionEvent) map[string]struct{} {
	values := make(map[string]struct{})
	for _, tx := range transactions {
		if strings.TrimSpace(tx.DependencyTarget) != "" {
			values[tx.DependencyTarget] = struct{}{}
		}
	}
	return values
}

func firstSortedSetValues(values map[string]struct{}, limit int) []string {
	if len(values) == 0 || limit == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
