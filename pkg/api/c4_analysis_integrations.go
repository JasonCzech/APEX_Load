package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const rootCauseSchemaVersion = "apexload.root_cause.v1"

type releaseSLAWaiverPersistence interface {
	SaveReleaseSLAWaiver(waiver *storage.ReleaseSLAWaiver) error
	ListReleaseSLAWaivers(releaseID string) ([]*storage.ReleaseSLAWaiver, error)
}

type externalResultImportPersistence interface {
	SaveExternalResultImport(result *storage.ExternalResultImport) error
	ListExternalResultImports(filter storage.ExternalResultImportFilter) ([]*storage.ExternalResultImport, error)
}

type APMIntegrationProfilesResponse struct {
	GeneratedAt        time.Time               `json:"generated_at"`
	ConfiguredCount    int                     `json:"configured_count"`
	Profiles           []APMIntegrationProfile `json:"profiles"`
	GenericCorrelation APMCorrelationContract  `json:"generic_correlation"`
}

type APMIntegrationProfile struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Status            string   `json:"status"`
	Capabilities      []string `json:"capabilities"`
	SupportedSignals  []string `json:"supported_signals"`
	CorrelationFields []string `json:"correlation_fields"`
	RequiredSettings  []string `json:"required_settings,omitempty"`
	DryRunEndpoint    string   `json:"dry_run_endpoint,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
	SetupActions      []string `json:"setup_actions,omitempty"`
}

type APMCorrelationContract struct {
	Status            string   `json:"status"`
	Propagation       string   `json:"propagation"`
	CorrelationFields []string `json:"correlation_fields"`
	Evidence          []string `json:"evidence,omitempty"`
}

type InfrastructureMonitorPacksResponse struct {
	GeneratedAt time.Time                   `json:"generated_at"`
	Packs       []InfrastructureMonitorPack `json:"packs"`
	Evidence    []string                    `json:"evidence,omitempty"`
}

type InfrastructureMonitorPack struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name"`
	Status       string                       `json:"status"`
	Source       string                       `json:"source"`
	ResourceType string                       `json:"resource_type"`
	Signals      []string                     `json:"signals"`
	Profiles     []MonitoringReadinessProfile `json:"profiles,omitempty"`
	Evidence     []string                     `json:"evidence,omitempty"`
	SetupActions []string                     `json:"setup_actions,omitempty"`
}

type ReportProfilesResponse struct {
	GeneratedAt     time.Time                 `json:"generated_at"`
	DefaultProfile  string                    `json:"default_profile"`
	Profiles        []RunArchiveReportProfile `json:"profiles"`
	DownloadFormats []string                  `json:"download_formats"`
}

type RunRootCauseSummary struct {
	SchemaVersion string                 `json:"schema_version"`
	TestID        string                 `json:"test_id"`
	GeneratedAt   time.Time              `json:"generated_at"`
	Status        string                 `json:"status"`
	Confidence    string                 `json:"confidence"`
	Summary       string                 `json:"summary"`
	Signals       []RootCauseSignal      `json:"signals,omitempty"`
	TopFindings   []RootCauseFinding     `json:"top_findings,omitempty"`
	NextActions   []string               `json:"next_actions,omitempty"`
	Evidence      []string               `json:"evidence,omitempty"`
	Report        map[string]interface{} `json:"report,omitempty"`
}

type RootCauseSignal struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Value    string  `json:"value,omitempty"`
	Severity string  `json:"severity,omitempty"`
	Score    float64 `json:"score,omitempty"`
}

type RootCauseFinding struct {
	Category string   `json:"category"`
	Severity string   `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Evidence []string `json:"evidence,omitempty"`
	Score    float64  `json:"score"`
}

type ReleaseSLAGovernanceResponse struct {
	GeneratedAt   time.Time                   `json:"generated_at"`
	ReleaseID     string                      `json:"release_id"`
	ReleaseName   string                      `json:"release_name,omitempty"`
	ApplicationID string                      `json:"application_id,omitempty"`
	ProjectID     string                      `json:"project_id,omitempty"`
	Owner         ReleaseSLAGovernanceOwner   `json:"owner"`
	Status        string                      `json:"status"`
	Summary       ReleaseSLAGovernanceSummary `json:"summary"`
	BreachHistory []ReleaseSLAGovernanceRun   `json:"breach_history,omitempty"`
	Waivers       []*storage.ReleaseSLAWaiver `json:"waivers,omitempty"`
	AuditTrail    []*storage.AuditEvent       `json:"audit_trail,omitempty"`
	NextActions   []string                    `json:"next_actions,omitempty"`
}

type ReleaseSLAGovernanceOwner struct {
	Type        string `json:"type"`
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"display_name"`
}

type ReleaseSLAGovernanceSummary struct {
	TotalRuns        int `json:"total_runs"`
	ArchivedRuns     int `json:"archived_runs"`
	PassedRuns       int `json:"passed_runs"`
	FailedRuns       int `json:"failed_runs"`
	WaivedBreaches   int `json:"waived_breaches"`
	UnwaivedBreaches int `json:"unwaived_breaches"`
}

type ReleaseSLAGovernanceRun struct {
	TestID          string     `json:"test_id"`
	TestName        string     `json:"test_name,omitempty"`
	Status          string     `json:"status"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	SLABreached     bool       `json:"sla_breached"`
	SLADetails      []string   `json:"sla_details,omitempty"`
	TelemetryStatus string     `json:"telemetry_status,omitempty"`
	Completeness    string     `json:"completeness,omitempty"`
}

type createReleaseSLAWaiverRequest struct {
	Scope      string     `json:"scope,omitempty"`
	Reason     string     `json:"reason"`
	ApprovedBy string     `json:"approved_by,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type ExternalResultImportRequest struct {
	Provider   string         `json:"provider"`
	Name       string         `json:"name,omitempty"`
	ProjectID  string         `json:"project_id,omitempty"`
	ReleaseID  string         `json:"release_id,omitempty"`
	ScenarioID string         `json:"scenario_id,omitempty"`
	Source     string         `json:"source,omitempty"`
	Summary    map[string]any `json:"summary,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	Results    map[string]any `json:"results,omitempty"`
}

func (s *Server) handleListAPMIntegrationProfiles(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	profiles := buildAPMIntegrationProfiles(settings)
	configured := 0
	for _, profile := range profiles {
		if profile.Status == "configured" || profile.Status == "supported" {
			configured++
		}
	}
	writeJSON(w, http.StatusOK, APMIntegrationProfilesResponse{
		GeneratedAt:     time.Now().UTC(),
		ConfiguredCount: configured,
		Profiles:        profiles,
		GenericCorrelation: APMCorrelationContract{
			Status:            "supported",
			Propagation:       "W3C trace-context",
			CorrelationFields: []string{"traceparent", "trace_id", "span_id", "correlation_id", "dependency_type", "dependency_target"},
			Evidence:          []string{"pkg/api/archive.go", "docs/COMPETITIVE_COMPARISON.md#gap-analysis"},
		},
	})
}

func buildAPMIntegrationProfiles(settings SystemSettings) []APMIntegrationProfile {
	dynatraceConfigured := false
	for _, webhook := range settings.Notifications.Webhooks {
		if webhook.Enabled && strings.EqualFold(webhook.Format, notificationFormatDynatrace) {
			dynatraceConfigured = true
			break
		}
	}
	statusFor := func(id string) string {
		if id == "opentelemetry" {
			return "supported"
		}
		if id == "dynatrace" && dynatraceConfigured {
			return "configured"
		}
		return "available"
	}
	baseCapabilities := []string{"trace_correlation", "dependency_tags", "run_lifecycle_events", "archived_external_metrics"}
	baseSignals := []string{"transactions", "errors", "resource_samples", "interval_summaries", "server_telemetry"}
	baseFields := []string{"traceparent", "trace_id", "span_id", "correlation_id"}
	profiles := []APMIntegrationProfile{
		{
			ID:                "opentelemetry",
			Name:              "OpenTelemetry",
			Status:            statusFor("opentelemetry"),
			Capabilities:      append([]string{"native_otlp_correlation"}, baseCapabilities...),
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			Evidence:          []string{"pkg/api/archive.go", "docs/RUNTIME_TELEMETRY_SLA_PLAN.md"},
		},
		{
			ID:                "dynatrace",
			Name:              "Dynatrace",
			Status:            statusFor("dynatrace"),
			Capabilities:      append([]string{"problem_event_webhook", "entity_selector", "dry_run_payload_export"}, baseCapabilities...),
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"notifications.webhooks[].format=dynatrace", "notifications.webhooks[].api_token", "notifications.webhooks[].entity_selector"},
			DryRunEndpoint:    "/api/v1/integrations/dynatrace/dry-run",
			Evidence:          []string{"pkg/api/notifications.go", "docs/COMPETITIVE_COMPARISON.md#gap-analysis"},
			SetupActions:      []string{"Preview Dynatrace Events API v2 payloads with the dry-run endpoint before enabling delivery.", "Enable a Dynatrace notification webhook in system settings.", "Pass W3C trace-context headers from scripts to the service under test."},
		},
		{
			ID:                "datadog",
			Name:              "Datadog",
			Status:            statusFor("datadog"),
			Capabilities:      baseCapabilities,
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"external metrics or OpenTelemetry collector route"},
			Evidence:          []string{"pkg/api/external_metrics.go", "pkg/api/archive.go"},
			SetupActions:      []string{"Route APEX Load OpenTelemetry signals through a Datadog-enabled collector.", "Map Datadog trace IDs into archived transaction trace fields."},
		},
		{
			ID:                "newrelic",
			Name:              "New Relic",
			Status:            statusFor("newrelic"),
			Capabilities:      baseCapabilities,
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"external metrics or OpenTelemetry collector route"},
			Evidence:          []string{"pkg/api/external_metrics.go", "pkg/api/archive.go"},
			SetupActions:      []string{"Route APEX Load OpenTelemetry signals through a New Relic-enabled collector.", "Map span and transaction attributes into New Relic entities."},
		},
		{
			ID:                "appdynamics",
			Name:              "AppDynamics",
			Status:            statusFor("appdynamics"),
			Capabilities:      baseCapabilities,
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"external metrics or OpenTelemetry collector route"},
			Evidence:          []string{"pkg/api/external_metrics.go", "pkg/api/archive.go"},
			SetupActions:      []string{"Route APEX Load OpenTelemetry signals through an AppDynamics-capable collector.", "Map dependency target names to AppDynamics application nodes."},
		},
	}
	return profiles
}

func (s *Server) handleListInfrastructureMonitorPacks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, InfrastructureMonitorPacksResponse{
		GeneratedAt: time.Now().UTC(),
		Packs: []InfrastructureMonitorPack{
			{
				ID:           "kubernetes",
				Name:         "Kubernetes",
				Status:       "supported",
				Source:       "generator inventory, fleet capacity, resource samples",
				ResourceType: "cluster",
				Signals:      []string{"pod saturation", "active slots", "reserved slots", "generator health", "version mismatch"},
				Evidence:     []string{"/api/v1/system/inventory", "/api/v1/fleet/alerts", "/api/v1/fleet/capacity"},
			},
			{
				ID:           "linux-hosts",
				Name:         "Linux Hosts",
				Status:       "supported",
				Source:       "archived resource samples",
				ResourceType: "host",
				Signals:      []string{"cpu_process_pct", "cpu_free_pct", "mem_process_mb", "net_sent_kbps", "net_recv_kbps", "disk_read_kbps", "disk_write_kbps"},
				Evidence:     []string{"/api/v1/tests/{id}/archive/resources", "pkg/api/archive.go"},
			},
			{
				ID:           "jvm",
				Name:         "JVM",
				Status:       "integration_contract",
				Source:       "external metrics",
				ResourceType: "runtime",
				Signals:      []string{"heap used", "gc pause", "thread count", "class loading", "jvm error rate"},
				Evidence:     []string{"pkg/api/external_metrics.go", "/api/v1/tests/{id}/archive/root-cause"},
				SetupActions: []string{"Publish JVM metrics into archive resource external_metrics with a jvm.* prefix."},
			},
			{
				ID:           "database",
				Name:         "Database",
				Status:       "integration_contract",
				Source:       "external metrics and dependency telemetry",
				ResourceType: "database",
				Signals:      []string{"query latency", "connection pool saturation", "lock waits", "deadlocks", "replica lag", "Oracle wait and session metrics"},
				Profiles:     []MonitoringReadinessProfile{oracleDBMonitoringProfile()},
				Evidence:     []string{"pkg/api/archive.go", "/api/v1/tests/{id}/archive/root-cause"},
				SetupActions: []string{"Map database dependency targets and publish db.* external metrics.", "For Oracle Database, publish the oracle.db.* canonical metric contract through a Prometheus-format endpoint before connecting SaaS monitoring."},
			},
			{
				ID:           "web-server",
				Name:         "Web Server",
				Status:       "integration_contract",
				Source:       "server telemetry and external metrics",
				ResourceType: "service",
				Signals:      []string{"server processing", "transfer time", "5xx rate", "connection reuse", "response payload size"},
				Evidence:     []string{"pkg/api/runner.go", "pkg/api/archive.go"},
				SetupActions: []string{"Enable server telemetry extraction in protocol actions and export web.* external metrics when available."},
			},
			{
				ID:           "queue-brokers",
				Name:         "Queue Brokers",
				Status:       "supported",
				Source:       "JMS/gRPC protocol telemetry and external metrics",
				ResourceType: "messaging",
				Signals:      []string{"message latency", "consumer lag", "messages sent", "messages received", "broker saturation"},
				Evidence:     []string{"pkg/api/runner.go", "docs/PROTOCOL_TELEMETRY_PARITY_PLAN.md"},
				SetupActions: []string{"Publish broker metrics with queue.* or broker.* prefixes for root-cause correlation."},
			},
		},
		Evidence: []string{"docs/COMPETITIVE_COMPARISON.md#k6", "docs/COMPETITIVE_COMPARISON.md#support-matrix"},
	})
}

func (s *Server) handleListReportProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ReportProfilesResponse{
		GeneratedAt:     time.Now().UTC(),
		DefaultProfile:  "engineer",
		Profiles:        buildRunArchiveReportProfiles(),
		DownloadFormats: []string{"html", "json", "csv", "pdf", "jsonl", "parquet"},
	})
}

func (s *Server) handleGetRunArchiveRootCause(w http.ResponseWriter, r *http.Request) {
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build root-cause summary: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buildRunRootCauseSummary(r.PathValue("id"), report))
}

func buildRunRootCauseSummary(testID string, report *RunArchiveReport) RunRootCauseSummary {
	summary := RunRootCauseSummary{
		SchemaVersion: rootCauseSchemaVersion,
		TestID:        strings.TrimSpace(testID),
		GeneratedAt:   time.Now().UTC(),
		Status:        "healthy",
		Confidence:    "medium",
		Evidence:      []string{"/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/report"},
		Report: map[string]interface{}{
			"profile": "engineer",
		},
	}
	if report == nil || report.Manifest == nil {
		summary.Status = "unknown"
		summary.Confidence = "low"
		summary.Summary = "No archived report evidence is available for root-cause correlation."
		return summary
	}
	addSignal := func(name string, status string, value string, severity string, score float64) {
		summary.Signals = append(summary.Signals, RootCauseSignal{Name: name, Status: status, Value: value, Severity: severity, Score: score})
	}
	addFinding := func(category string, severity string, title string, detail string, evidence []string, score float64) {
		summary.TopFindings = append(summary.TopFindings, RootCauseFinding{
			Category: category,
			Severity: severity,
			Title:    title,
			Detail:   detail,
			Evidence: evidence,
			Score:    score,
		})
	}

	if report.Manifest.SLABreached {
		addSignal("SLA", "breached", strings.Join(report.Manifest.SLADetails, "; "), "critical", 95)
		addFinding("sla", "critical", "Release SLA breached", firstNonEmpty(strings.Join(report.Manifest.SLADetails, "; "), "The run archive marked one or more SLA checks as breached."), []string{"manifest.sla_breached", "manifest.sla_details"}, 95)
	} else {
		addSignal("SLA", "passed", "No archived SLA breach", "healthy", 5)
	}
	if report.Overview.Errors > 0 || report.Overview.ErrorPct > 0 {
		severity := "warning"
		score := math.Min(90, 45+report.Overview.ErrorPct)
		if report.Overview.ErrorPct >= 5 {
			severity = "critical"
		}
		addSignal("Errors", "elevated", fmt.Sprintf("%d errors, %.2f%%", report.Overview.Errors, report.Overview.ErrorPct), severity, score)
		addFinding("errors", severity, "Transaction errors contributed to the run outcome", fmt.Sprintf("Archived transactions include %d errors across %.2f%% of completed requests.", report.Overview.Errors, report.Overview.ErrorPct), []string{"overview.errors", "error_patterns"}, score)
	}
	if len(report.ErrorPatterns) > 0 {
		pattern := report.ErrorPatterns[0]
		addFinding("errors", "warning", "Dominant error pattern detected", fmt.Sprintf("%q occurred %d time(s).", pattern.Sample, pattern.Count), []string{"error_patterns[0]"}, 60)
	}
	if telemetry := report.Overview.Telemetry; telemetry != nil {
		addSignal("Telemetry", telemetry.Status, fmt.Sprintf("%d samples", telemetry.Samples), telemetrySeverity(telemetry.Status), telemetryScore(telemetry.Status))
		if telemetry.Status == "breached" || telemetry.Status == "degraded" {
			addFinding("telemetry", telemetrySeverity(telemetry.Status), "Telemetry health degraded during the run", telemetryRootCauseDetail(telemetry), []string{"overview.telemetry"}, telemetryScore(telemetry.Status))
		}
	}
	if report.ResourcePeaks.CPUProcessPct >= 80 {
		severity := "warning"
		if report.ResourcePeaks.CPUProcessPct >= 90 {
			severity = "critical"
		}
		addSignal("Generator CPU", "saturated", fmt.Sprintf("%.1f%% process CPU", report.ResourcePeaks.CPUProcessPct), severity, report.ResourcePeaks.CPUProcessPct)
		addFinding("resource", severity, "Generator resource saturation detected", fmt.Sprintf("Peak generator process CPU reached %.1f%%.", report.ResourcePeaks.CPUProcessPct), []string{"resource_peaks.cpu_process_pct"}, report.ResourcePeaks.CPUProcessPct)
	}
	if oracle := report.OracleDB; oracle != nil && oracle.Samples > 0 {
		severity := oracleDBSignalSeverity(oracle)
		status := oracle.Status
		if severity == "critical" {
			status = "saturated"
		} else if severity == "warning" && status == "ready" {
			status = "degraded"
		}
		addSignal("Oracle DB", status, fmt.Sprintf("%d resource sample(s), %d metric(s)", oracle.Samples, len(oracle.MetricStats)), severity, 55)
		if findingSeverity, title, detail, score, ok := oracleDBRootCauseFinding(oracle); ok {
			addFinding("oracle_db", findingSeverity, title, detail, []string{"oracle_db", "resource_samples.external_metrics", "summary_timeline"}, score)
		}
	}
	if report.Overview.ThroughputMissedIntervals > 0 {
		addSignal("Load model", "missed_target", fmt.Sprintf("%d interval(s)", report.Overview.ThroughputMissedIntervals), "warning", 55)
		addFinding("load_model", "warning", "Configured throughput target was missed", fmt.Sprintf("The run missed configured throughput in %d archived interval(s); max throughput gap was %.1f%%.", report.Overview.ThroughputMissedIntervals, report.Overview.MaxThroughputGapPct), []string{"overview.throughput_missed_intervals", "summary_timeline"}, 55)
	}
	if browser := report.Overview.Browser; browser != nil {
		if browser.AvgLCPMs > 2500 || browser.AvgINPMs > 200 || browser.AvgCLSScore > 0.1 {
			addSignal("Browser UX", "degraded", fmt.Sprintf("LCP %.0f ms, INP %.0f ms, CLS %.3f", browser.AvgLCPMs, browser.AvgINPMs, browser.AvgCLSScore), "warning", 58)
			addFinding("browser", "warning", "Browser canary experience degraded", "Archived browser diagnostics exceeded common Core Web Vitals guidance for one or more client-side metrics.", []string{"overview.browser", "browser_by_device"}, 58)
		} else if browser.Samples > 0 {
			addSignal("Browser UX", "healthy", fmt.Sprintf("%d samples", browser.Samples), "healthy", 5)
		}
	}
	if report.Completeness.Status != "" && report.Completeness.Status != "complete" {
		addFinding("reporting", "warning", "Archive evidence is incomplete", fmt.Sprintf("Report completeness is %s at %.0f%%.", report.Completeness.Status, report.Completeness.ScorePct), []string{"completeness"}, 35)
	}

	sort.SliceStable(summary.TopFindings, func(left, right int) bool {
		return summary.TopFindings[left].Score > summary.TopFindings[right].Score
	})
	if len(summary.TopFindings) > 6 {
		summary.TopFindings = summary.TopFindings[:6]
	}
	critical := false
	warning := false
	categories := map[string]struct{}{}
	for _, finding := range summary.TopFindings {
		categories[finding.Category] = struct{}{}
		if finding.Severity == "critical" {
			critical = true
		}
		if finding.Severity == "warning" {
			warning = true
		}
	}
	switch {
	case critical:
		summary.Status = "breached"
	case warning:
		summary.Status = "degraded"
	default:
		summary.Status = "healthy"
	}
	summary.Confidence = rootCauseConfidence(report)
	summary.Summary = rootCauseNarrative(summary.Status, summary.TopFindings)
	summary.NextActions = rootCauseNextActions(categories)
	if len(summary.NextActions) == 0 {
		summary.NextActions = []string{"Use the engineer report profile to review timeline, action, and resource evidence before closing the release gate."}
	}
	return summary
}

func telemetrySeverity(status string) string {
	switch status {
	case "breached":
		return "critical"
	case "degraded":
		return "warning"
	default:
		return "healthy"
	}
}

func telemetryScore(status string) float64 {
	switch status {
	case "breached":
		return 80
	case "degraded":
		return 50
	default:
		return 5
	}
}

func telemetryRootCauseDetail(telemetry *RunArchiveTelemetrySummary) string {
	if telemetry == nil {
		return ""
	}
	parts := []string{}
	if telemetry.DroppedResults > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped result(s)", telemetry.DroppedResults))
	}
	if telemetry.WebsocketDroppedMessages > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped websocket message(s)", telemetry.WebsocketDroppedMessages))
	}
	if telemetry.MaxSampleAgeMs > 0 {
		parts = append(parts, fmt.Sprintf("max sample age %.0f ms", telemetry.MaxSampleAgeMs))
	}
	if len(telemetry.ConditionCounts) > 0 {
		keys := make([]string, 0, len(telemetry.ConditionCounts))
		for key := range telemetry.ConditionCounts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts = append(parts, "conditions: "+strings.Join(keys, ", "))
	}
	if len(parts) == 0 {
		return "Telemetry health status was " + telemetry.Status + " for one or more intervals."
	}
	return strings.Join(parts, "; ") + "."
}

func rootCauseConfidence(report *RunArchiveReport) string {
	if report == nil {
		return "low"
	}
	if report.Completeness.ScorePct >= 90 && report.Overview.IntervalSummaries > 0 && report.Overview.Transactions > 0 {
		return "high"
	}
	if report.Completeness.ScorePct < 60 {
		return "low"
	}
	return "medium"
}

func rootCauseNarrative(status string, findings []RootCauseFinding) string {
	if len(findings) == 0 {
		return "No root-cause findings were detected from the archived run evidence."
	}
	lead := findings[0]
	switch status {
	case "breached":
		return "The strongest release-blocking signal is " + strings.ToLower(lead.Title) + "."
	case "degraded":
		return "The run needs review; the strongest signal is " + strings.ToLower(lead.Title) + "."
	default:
		return "The run appears healthy across archived SLA, telemetry, resource, and browser evidence."
	}
}

func rootCauseNextActions(categories map[string]struct{}) []string {
	actions := []string{}
	if _, ok := categories["sla"]; ok {
		actions = append(actions, "Review SLA thresholds and waiver status before promoting the release.")
	}
	if _, ok := categories["errors"]; ok {
		actions = append(actions, "Open the transaction archive and group failures by action, dependency target, and trace ID.")
	}
	if _, ok := categories["telemetry"]; ok {
		actions = append(actions, "Inspect collector lag, dropped results, and websocket fan-out before trusting live dashboards.")
	}
	if _, ok := categories["resource"]; ok {
		actions = append(actions, "Compare generator CPU and memory peaks against the fleet capacity envelope for this protocol mix.")
	}
	if _, ok := categories["oracle_db"]; ok {
		actions = append(actions, "Review Oracle wait, lock, session, and IO metrics beside transaction P95 latency before assigning the bottleneck.")
	}
	if _, ok := categories["load_model"]; ok {
		actions = append(actions, "Compare achieved throughput against configured staged-ramp and arrival-rate goals.")
	}
	if _, ok := categories["browser"]; ok {
		actions = append(actions, "Review browser diagnostics by device profile and isolate client-side regressions from backend latency.")
	}
	return actions
}

func (s *Server) handleGetReleaseSLAGovernance(w http.ResponseWriter, r *http.Request) {
	releaseID := strings.TrimSpace(r.PathValue("id"))
	response, err := s.buildReleaseSLAGovernance(releaseID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleCreateReleaseSLAWaiver(w http.ResponseWriter, r *http.Request) {
	releaseID := strings.TrimSpace(r.PathValue("id"))
	var req createReleaseSLAWaiverRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Scope = strings.TrimSpace(req.Scope)
	req.Reason = strings.TrimSpace(req.Reason)
	req.ApprovedBy = strings.TrimSpace(req.ApprovedBy)
	if req.Scope == "" {
		req.Scope = "release"
	}
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "waiver reason is required")
		return
	}
	projectID, _, _, err := s.releaseContext(releaseID)
	if err != nil && s.store != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if req.ApprovedBy == "" {
		_, subject, displayName := s.resolveAuditActor(r)
		req.ApprovedBy = firstNonEmpty(displayName, subject)
	}
	now := time.Now().UTC()
	waiver := &storage.ReleaseSLAWaiver{
		ID:         uuid.New().String(),
		ReleaseID:  releaseID,
		ProjectID:  projectID,
		Scope:      req.Scope,
		Reason:     req.Reason,
		ApprovedBy: req.ApprovedBy,
		Status:     "active",
		ExpiresAt:  req.ExpiresAt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if waiver.ExpiresAt != nil && waiver.ExpiresAt.Before(now) {
		waiver.Status = "expired"
	}
	if err := s.saveReleaseSLAWaiver(waiver); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save SLA waiver: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "release.sla_waiver.create", "success", "release", releaseID, projectID, map[string]any{
		"waiver_id":  waiver.ID,
		"scope":      waiver.Scope,
		"reason":     waiver.Reason,
		"expires_at": waiver.ExpiresAt,
	})
	governance, err := s.buildReleaseSLAGovernance(releaseID)
	if err != nil {
		writeJSON(w, http.StatusCreated, map[string]any{"waiver": waiver})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"waiver": waiver, "governance": governance})
}

func (s *Server) buildReleaseSLAGovernance(releaseID string) (ReleaseSLAGovernanceResponse, error) {
	if releaseID == "" {
		return ReleaseSLAGovernanceResponse{}, fmt.Errorf("release id is required")
	}
	projectID, releaseName, applicationID, err := s.releaseContext(releaseID)
	if err != nil && s.store != nil {
		return ReleaseSLAGovernanceResponse{}, err
	}
	waivers, err := s.listReleaseSLAWaivers(releaseID)
	if err != nil {
		return ReleaseSLAGovernanceResponse{}, err
	}
	activeWaivers := 0
	for _, waiver := range waivers {
		if waiver != nil && waiver.Status == "active" {
			activeWaivers++
		}
	}
	tests := s.runner.ListTestsByContext("", releaseID, "")
	sort.SliceStable(tests, func(left, right int) bool {
		return managedTestCompletedAt(tests[left]).After(managedTestCompletedAt(tests[right]))
	})
	response := ReleaseSLAGovernanceResponse{
		GeneratedAt:   time.Now().UTC(),
		ReleaseID:     releaseID,
		ReleaseName:   releaseName,
		ApplicationID: applicationID,
		ProjectID:     projectID,
		Owner: ReleaseSLAGovernanceOwner{
			Type:        "project",
			ID:          projectID,
			DisplayName: "Project administrators",
		},
		Waivers: waivers,
	}
	for _, test := range tests {
		if test == nil {
			continue
		}
		response.Summary.TotalRuns++
		run := ReleaseSLAGovernanceRun{
			TestID:      test.ID,
			TestName:    test.Name,
			Status:      test.Status,
			CompletedAt: test.EndedAt,
		}
		if record, ok := s.runner.GetRunArchive(test.ID); ok {
			report, err := buildRunArchiveReport(record)
			if err != nil {
				return ReleaseSLAGovernanceResponse{}, fmt.Errorf("failed to build archive report for %s: %w", test.ID, err)
			}
			response.Summary.ArchivedRuns++
			if report.Manifest != nil {
				run.SLABreached = report.Manifest.SLABreached
				run.SLADetails = append([]string(nil), report.Manifest.SLADetails...)
			}
			if report.Overview.Telemetry != nil {
				run.TelemetryStatus = report.Overview.Telemetry.Status
			}
			run.Completeness = report.Completeness.Status
		} else if test.Status == "FAILED" {
			run.SLABreached = true
			run.SLADetails = []string{firstNonEmpty(test.Error, "Run failed before archive evidence was available.")}
		}
		if run.SLABreached {
			response.Summary.FailedRuns++
			response.BreachHistory = append(response.BreachHistory, run)
		} else if test.Status == "COMPLETE" {
			response.Summary.PassedRuns++
		}
	}
	if activeWaivers > 0 {
		response.Summary.WaivedBreaches = response.Summary.FailedRuns
	} else {
		response.Summary.UnwaivedBreaches = response.Summary.FailedRuns
	}
	switch {
	case response.Summary.TotalRuns == 0:
		response.Status = "no_runs"
		response.NextActions = []string{"Run at least one release-scoped scenario before evaluating the release SLA gate."}
	case response.Summary.UnwaivedBreaches > 0:
		response.Status = "failed"
		response.NextActions = []string{"Investigate breach history and create an approved waiver only when release governance accepts the risk."}
	case response.Summary.WaivedBreaches > 0:
		response.Status = "waived"
		response.NextActions = []string{"Review waiver expiration and audit trail before promotion."}
	default:
		response.Status = "passed"
		response.NextActions = []string{"Export executive or audit report profiles for release evidence."}
	}
	response.AuditTrail = s.releaseAuditTrail(releaseID)
	return response, nil
}

func (s *Server) releaseContext(releaseID string) (projectID string, releaseName string, applicationID string, err error) {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return "", "", "", fmt.Errorf("release id is required")
	}
	if s.store == nil {
		return storage.DefaultProjectID, releaseID, "", nil
	}
	release, err := s.store.GetRelease(releaseID)
	if err != nil || release == nil {
		return "", "", "", fmt.Errorf("release not found")
	}
	projectID = storage.DefaultProjectID
	if application, appErr := s.store.GetApplication(release.ApplicationID); appErr == nil && application != nil {
		projectID = normalizeProjectID(application.ProjectID)
	}
	return projectID, release.Name, release.ApplicationID, nil
}

func (s *Server) saveReleaseSLAWaiver(waiver *storage.ReleaseSLAWaiver) error {
	if store, ok := s.store.(releaseSLAWaiverPersistence); ok {
		return store.SaveReleaseSLAWaiver(waiver)
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	releaseID := strings.TrimSpace(waiver.ReleaseID)
	copied := *waiver
	s.slaWaivers[releaseID] = append(s.slaWaivers[releaseID], &copied)
	return nil
}

func (s *Server) listReleaseSLAWaivers(releaseID string) ([]*storage.ReleaseSLAWaiver, error) {
	if store, ok := s.store.(releaseSLAWaiverPersistence); ok {
		return store.ListReleaseSLAWaivers(releaseID)
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	waivers := s.slaWaivers[strings.TrimSpace(releaseID)]
	out := make([]*storage.ReleaseSLAWaiver, 0, len(waivers))
	now := time.Now().UTC()
	for _, waiver := range waivers {
		if waiver == nil {
			continue
		}
		copied := *waiver
		if copied.ExpiresAt != nil && copied.ExpiresAt.Before(now) && copied.Status == "active" {
			copied.Status = "expired"
		}
		out = append(out, &copied)
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].CreatedAt.After(out[right].CreatedAt)
	})
	return out, nil
}

func (s *Server) releaseAuditTrail(releaseID string) []*storage.AuditEvent {
	if s.store == nil {
		return nil
	}
	events, err := s.store.ListAuditEvents(storage.AuditEventFilter{
		ResourceType: "release",
		ResourceID:   strings.TrimSpace(releaseID),
		Limit:        20,
	})
	if err != nil {
		return nil
	}
	return events
}

func managedTestCompletedAt(test *ManagedTest) time.Time {
	if test == nil {
		return time.Time{}
	}
	if test.EndedAt != nil {
		return *test.EndedAt
	}
	if test.StartedAt != nil {
		return *test.StartedAt
	}
	return test.CreatedAt
}

func (s *Server) handleImportExternalResults(w http.ResponseWriter, r *http.Request) {
	var req ExternalResultImportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	provider := normalizeExternalResultProvider(req.Provider)
	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider must be one of apexload, k6, jmeter, or gatling")
		return
	}
	raw := req.Result
	if len(raw) == 0 {
		raw = req.Results
	}
	summary := normalizeExternalResultSummary(req.Summary)
	if len(summary) == 0 && len(raw) > 0 {
		summary = deriveExternalResultSummary(provider, raw)
	}
	if len(summary) == 0 {
		summary = map[string]any{"status": "imported"}
	}
	summary["provider"] = provider
	projectID := normalizeProjectID(req.ProjectID)
	if projectID == storage.DefaultProjectID && strings.TrimSpace(req.ReleaseID) != "" && s.store != nil {
		if resolvedProjectID, _, _, err := s.releaseContext(req.ReleaseID); err == nil {
			projectID = resolvedProjectID
		}
	}
	result := &storage.ExternalResultImport{
		ID:         uuid.New().String(),
		Provider:   provider,
		Name:       firstNonEmpty(strings.TrimSpace(req.Name), provider+" import"),
		ProjectID:  projectID,
		ReleaseID:  strings.TrimSpace(req.ReleaseID),
		ScenarioID: strings.TrimSpace(req.ScenarioID),
		Source:     strings.TrimSpace(req.Source),
		Summary:    summary,
		RawResult:  raw,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.saveExternalResultImport(result); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save external result import: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "results.external_import.create", "success", "external_result_import", result.ID, result.ProjectID, map[string]any{
		"provider":   result.Provider,
		"release_id": result.ReleaseID,
		"source":     result.Source,
		"summary":    result.Summary,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"import": result})
}

func (s *Server) handleListExternalResultImports(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, 100)
	filter := storage.ExternalResultImportFilter{
		ProjectID: strings.TrimSpace(r.URL.Query().Get("project_id")),
		ReleaseID: strings.TrimSpace(r.URL.Query().Get("release_id")),
		Provider:  normalizeExternalResultProvider(r.URL.Query().Get("provider")),
		Limit:     limit,
		Offset:    offset,
	}
	imports, err := s.listExternalResultImports(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list external result imports: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imports": imports,
		"count":   len(imports),
		"limit":   limit,
		"offset":  offset,
	})
}

func normalizeExternalResultProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "apexload", "apex-load", "apex_load":
		return "apexload"
	case "k6":
		return "k6"
	case "jmeter", "apache-jmeter":
		return "jmeter"
	case "gatling":
		return "gatling"
	default:
		return ""
	}
}

func normalizeExternalResultSummary(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[strings.TrimSpace(key)] = value
	}
	if _, ok := out["status"]; !ok {
		if errorsValue, ok := numericFromMap(out, "errors"); ok && errorsValue > 0 {
			out["status"] = "failed"
		} else {
			out["status"] = "passed"
		}
	}
	return out
}

func deriveExternalResultSummary(provider string, raw map[string]any) map[string]any {
	switch provider {
	case "k6":
		return deriveK6Summary(raw)
	case "gatling":
		return deriveGatlingSummary(raw)
	case "jmeter":
		return deriveJMeterSummary(raw)
	default:
		return map[string]any{"status": "imported"}
	}
}

func deriveK6Summary(raw map[string]any) map[string]any {
	metrics := mapFromAny(raw["metrics"])
	transactions, _ := metricField(metrics, "http_reqs", "count")
	avg, _ := metricField(metrics, "http_req_duration", "avg")
	p95, _ := metricField(metrics, "http_req_duration", "p(95)", "p95")
	if p95 == 0 {
		if percentiles := mapFromAny(mapFromAny(metrics["http_req_duration"])["percentiles"]); len(percentiles) > 0 {
			p95, _ = numberFromAny(percentiles["95"])
		}
	}
	errorRate, _ := metricField(metrics, "http_req_failed", "rate")
	errors, _ := metricField(metrics, "http_req_failed", "fails")
	if errors == 0 && transactions > 0 && errorRate > 0 {
		errors = transactions * errorRate
	}
	errorPct := errorRate
	if errorPct > 0 && errorPct <= 1 {
		errorPct *= 100
	}
	return externalSummaryMap(transactions, errors, errorPct, avg, p95)
}

func deriveGatlingSummary(raw map[string]any) map[string]any {
	stats := mapFromAny(raw["stats"])
	requests := mapFromAny(stats["numberOfRequests"])
	transactions, _ := numberFromAny(requests["total"])
	errors, _ := numberFromAny(requests["ko"])
	avg, _ := numberFromAny(mapFromAny(stats["meanResponseTime"])["total"])
	p95, _ := numberFromAny(mapFromAny(stats["percentiles3"])["total"])
	errorPct := 0.0
	if transactions > 0 && errors > 0 {
		errorPct = errors / transactions * 100
	}
	return externalSummaryMap(transactions, errors, errorPct, avg, p95)
}

func deriveJMeterSummary(raw map[string]any) map[string]any {
	source := raw
	if aggregate := mapFromAny(raw["aggregate"]); len(aggregate) > 0 {
		source = aggregate
	}
	transactions, _ := firstNumeric(source, "samples", "sample_count", "transactions")
	errors, _ := firstNumeric(source, "errors", "error_count", "failed")
	avg, _ := firstNumeric(source, "avg", "average", "avg_response_ms", "mean")
	p95, _ := firstNumeric(source, "p95", "pct95", "95th", "p95_response_ms")
	errorPct, ok := firstNumeric(source, "error_pct", "error_percentage")
	if !ok && transactions > 0 && errors > 0 {
		errorPct = errors / transactions * 100
	}
	return externalSummaryMap(transactions, errors, errorPct, avg, p95)
}

func externalSummaryMap(transactions float64, errors float64, errorPct float64, avg float64, p95 float64) map[string]any {
	summary := map[string]any{
		"transactions":    int64(math.Round(transactions)),
		"errors":          int64(math.Round(errors)),
		"error_pct":       errorPct,
		"avg_response_ms": avg,
		"p95_response_ms": p95,
		"status":          "passed",
	}
	if errors > 0 || errorPct > 0 {
		summary["status"] = "failed"
	}
	return summary
}

func metricField(metrics map[string]any, metric string, fields ...string) (float64, bool) {
	return firstNumeric(mapFromAny(metrics[metric]), fields...)
}

func firstNumeric(values map[string]any, fields ...string) (float64, bool) {
	for _, field := range fields {
		if value, ok := numberFromAny(values[field]); ok {
			return value, true
		}
	}
	return 0, false
}

func numericFromMap(values map[string]any, field string) (float64, bool) {
	return numberFromAny(values[field])
}

func numberFromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := json.Number(strings.TrimSpace(typed)).Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func mapFromAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func (s *Server) saveExternalResultImport(result *storage.ExternalResultImport) error {
	if store, ok := s.store.(externalResultImportPersistence); ok {
		return store.SaveExternalResultImport(result)
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	copied := *result
	s.resultImports[result.ID] = &copied
	return nil
}

func (s *Server) listExternalResultImports(filter storage.ExternalResultImportFilter) ([]*storage.ExternalResultImport, error) {
	if store, ok := s.store.(externalResultImportPersistence); ok {
		return store.ListExternalResultImports(filter)
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	projectID := normalizeProjectID(filter.ProjectID)
	out := make([]*storage.ExternalResultImport, 0, len(s.resultImports))
	for _, result := range s.resultImports {
		if result == nil {
			continue
		}
		if filter.ProjectID != "" && normalizeProjectID(result.ProjectID) != projectID {
			continue
		}
		if filter.ReleaseID != "" && result.ReleaseID != filter.ReleaseID {
			continue
		}
		if filter.Provider != "" && result.Provider != filter.Provider {
			continue
		}
		copied := *result
		out = append(out, &copied)
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].CreatedAt.After(out[right].CreatedAt)
	})
	if filter.Offset > len(out) {
		return []*storage.ExternalResultImport{}, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	end := filter.Offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[filter.Offset:end], nil
}

func parseLimitOffsetQuery(r *http.Request, defaultLimit int) (int, int) {
	limit := defaultLimit
	offset := 0
	if parsed, err := parsePositiveIntQuery(r.URL.Query().Get("limit")); err == nil && parsed > 0 {
		limit = parsed
	}
	if parsed, err := parsePositiveIntQuery(r.URL.Query().Get("offset")); err == nil && parsed >= 0 {
		offset = parsed
	}
	return limit, offset
}

func parsePositiveIntQuery(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("negative value")
	}
	return value, nil
}
