package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func decodeAnalysisSettings(t *testing.T, reply *httptest.ResponseRecorder) ProjectAnalysisSettingsResponse {
	t.Helper()
	var response ProjectAnalysisSettingsResponse
	if err := json.Unmarshal(reply.Body.Bytes(), &response); err != nil {
		t.Fatalf("%v: %s", err, reply.Body.String())
	}
	return response
}

// setProjectAnalysis saves a project's analysis settings through the route.
func setProjectAnalysis(t *testing.T, server *Server, project string, rootCause storage.ProjectRootCauseSettings) ProjectAnalysisSettingsResponse {
	t.Helper()
	path := "/api/v1/projects/" + project + "/analysis-settings"
	current := decodeAnalysisSettings(t, timeslotRequest(t, server, http.MethodGet, path, nil))
	reply := timeslotRequest(t, server, http.MethodPut, path, map[string]any{"expected_revision": current.Settings.Revision, "root_cause": rootCause})
	if reply.Code != http.StatusOK {
		t.Fatalf("save analysis settings: %d %s", reply.Code, reply.Body.String())
	}
	return decodeAnalysisSettings(t, reply)
}

func TestProjectAnalysisSettingsCombineWithTheDeploymentLists(t *testing.T) {
	server, store := rcaTestServer(t)
	t.Setenv(envRCAV2Projects, "")
	project := storage.DefaultProjectID
	path := "/api/v1/projects/" + project + "/analysis-settings"

	initial := decodeAnalysisSettings(t, timeslotRequest(t, server, http.MethodGet, path, nil))
	if initial.Settings.Revision != 0 || initial.Effective.RootCause.Enabled || initial.ForcedByEnv.RootCause.Enabled {
		t.Fatalf("initial settings %+v", initial)
	}
	if rcaV2ProjectEnabled(store, project) {
		t.Fatal("root cause enabled with nothing set")
	}
	// A deployment list turns a flag on, and the response says so.
	t.Setenv(envRCAV2PresentProjects, project)
	forced := decodeAnalysisSettings(t, timeslotRequest(t, server, http.MethodGet, path, nil))
	if !forced.ForcedByEnv.RootCause.Present || !forced.Effective.RootCause.Present || forced.Settings.RootCause.Present {
		t.Fatalf("env-forced settings %+v", forced)
	}
	t.Setenv(envRCAV2PresentProjects, "")

	// Automatic analysis needs analyses to be computed at all.
	saved := setProjectAnalysis(t, server, project, storage.ProjectRootCauseSettings{AutoAnalyze: true})
	if saved.Settings.Revision != 1 || saved.Settings.UpdatedAt.IsZero() || !saved.Settings.RootCause.AutoAnalyze || saved.Effective.RootCause.AutoAnalyze {
		t.Fatalf("auto analyze without enabled: %+v", saved)
	}
	saved = setProjectAnalysis(t, server, project, storage.ProjectRootCauseSettings{Enabled: true, Present: true, AutoAnalyze: true})
	if saved.Settings.Revision != 2 || !saved.Effective.RootCause.AutoAnalyze || !rcaV2ProjectEnabled(store, project) || !rcaV2PresentEnabled(store, project) {
		t.Fatalf("enabled settings: %+v", saved)
	}
	// An unchanged save keeps the revision.
	if again := setProjectAnalysis(t, server, project, storage.ProjectRootCauseSettings{Enabled: true, Present: true, AutoAnalyze: true}); again.Settings.Revision != 2 {
		t.Fatalf("unchanged save bumped the revision: %+v", again)
	}
	// A save against an older revision is refused.
	if reply := timeslotRequest(t, server, http.MethodPut, path, map[string]any{"expected_revision": 1, "root_cause": map[string]bool{"enabled": false}}); reply.Code != http.StatusConflict {
		t.Fatalf("stale save: %d %s", reply.Code, reply.Body.String())
	}
	if reply := timeslotRequest(t, server, http.MethodPut, path, map[string]any{"root_cause": map[string]bool{"enabled": false}}); reply.Code != http.StatusBadRequest {
		t.Fatalf("save without a revision: %d %s", reply.Code, reply.Body.String())
	}
	// The general project update never changes them.
	stored, err := store.GetProject(project)
	if err != nil {
		t.Fatal(err)
	}
	update := timeslotRequest(t, server, http.MethodPut, "/api/v1/projects/"+project, map[string]any{"name": stored.Name, "settings": map[string]any{"analysis": map[string]any{"root_cause": map[string]bool{"enabled": false}}}})
	if update.Code != http.StatusOK {
		t.Fatalf("project update: %d %s", update.Code, update.Body.String())
	}
	if after := decodeAnalysisSettings(t, timeslotRequest(t, server, http.MethodGet, path, nil)); after.Settings.Revision != 2 || !after.Effective.RootCause.Enabled {
		t.Fatalf("project update changed analysis settings: %+v", after)
	}
	// The analysis response carries the gates.
	response := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/v2", nil).Body.Bytes())
	if !response.Presented || !response.AnalysisEnabled || !response.AutoAnalyze {
		t.Fatalf("analysis response gates %+v", response)
	}
}

// TestAnalysisGatesAreReadOnlyThroughTheResolver keeps every analysis gate
// on one path: a new reader of a deployment list must go through
// projectAnalysisGates, or a project's own settings would not apply to it.
func TestAnalysisGatesAreReadOnlyThroughTheResolver(t *testing.T) {
	names := []string{"APEXLOAD_RCA_V2_PROJECTS", "APEXLOAD_RCA_V2_PRESENT_PROJECTS", "APEXLOAD_RCA_AUTO_ANALYZE_PROJECTS", "APEXLOAD_RCA_AUTO_ENRICH_PROJECTS", "APEXLOAD_BUSINESS_KEY_PROJECTS"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "project_analysis_settings.go" {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if strings.Contains(string(body), `"`+name+`"`) {
				t.Errorf("%s reads %s directly; read it through projectAnalysisGates", file, name)
			}
		}
	}
}

func TestRCAAutomaticAnalysisIsQueuedOnceWhenTheArchiveAttaches(t *testing.T) {
	server, store := rcaTestServer(t)
	project := storage.DefaultProjectID
	server.autoQueueRCAAnalysis("rca-run")
	if analyses, _ := store.LatestRCAAnalyses(project, []string{"rca-run"}); analyses["rca-run"].Latest != nil {
		t.Fatal("queued without automatic analysis on")
	}
	t.Setenv(envRCAV2Projects, "")
	setProjectAnalysis(t, server, project, storage.ProjectRootCauseSettings{AutoAnalyze: true})
	server.autoQueueRCAAnalysis("rca-run")
	if analyses, _ := store.LatestRCAAnalyses(project, []string{"rca-run"}); analyses["rca-run"].Latest != nil {
		t.Fatal("queued with automatic analysis on but analyses off")
	}
	setProjectAnalysis(t, server, project, storage.ProjectRootCauseSettings{Enabled: true, AutoAnalyze: true})
	server.autoQueueRCAAnalysis("rca-run")
	analyses, err := store.LatestRCAAnalyses(project, []string{"rca-run"})
	latest := analyses["rca-run"].Latest
	if err != nil || latest == nil || latest.Origin != storage.RCAOriginAutomatic || latest.State != storage.RCAStateQueued {
		t.Fatalf("automatic analysis: %+v %v", latest, err)
	}
	// Another attach of the same archive (a refresh, a recovered
	// publication) finds the analysis of identical inputs.
	server.autoQueueRCAAnalysis("rca-run")
	if again, _ := store.LatestRCAAnalyses(project, []string{"rca-run"}); again["rca-run"].Latest.ID != latest.ID {
		t.Fatalf("a second attach queued another analysis: %+v", again["rca-run"].Latest)
	}
	// A request for the same inputs answers with the queued analysis, now a
	// requested one so it is not left behind other automatic analyses.
	requested := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
	if requested.AnalysisID != latest.ID || requested.Origin != storage.RCAOriginRequested {
		t.Fatalf("request for identical inputs: %+v", requested)
	}
}

func getArchiveReport(t *testing.T, server *Server, id string) RunArchiveReport {
	t.Helper()
	reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+id+"/archive/report?include_release_trend=false", nil)
	var report RunArchiveReport
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &report) != nil {
		t.Fatalf("report: %d %s", reply.Code, reply.Body.String())
	}
	return report
}

func TestRCAReportSummaryFollowsTheRunsAnalyses(t *testing.T) {
	server, _ := rcaTestServer(t)
	if summary := getArchiveReport(t, server, "rca-run").RootCauseSummary; summary == nil || summary.State != rcaSummaryNotEnabled || summary.Presented {
		t.Fatalf("not presented: %+v", summary)
	}
	t.Setenv(envRCAV2PresentProjects, storage.DefaultProjectID)
	if summary := getArchiveReport(t, server, "rca-run").RootCauseSummary; summary == nil || summary.State != rcaSummaryNone || !summary.Presented || !summary.AnalysisEnabled {
		t.Fatalf("no analysis: %+v", summary)
	}
	first := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
	queued := getArchiveReport(t, server, "rca-run").RootCauseSummary
	if queued.State != storage.RCAStateQueued || queued.AnalysisID != "" || queued.Latest == nil || queued.Latest.AnalysisID != first.AnalysisID || queued.Latest.Origin != storage.RCAOriginRequested {
		t.Fatalf("queued: %+v", queued)
	}
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatalf("run: %v %v", ran, err)
	}
	done := getArchiveReport(t, server, "rca-run").RootCauseSummary
	if done.State != storage.RCAStateCompleted || done.AnalysisID != first.AnalysisID || done.Outcome != rca.OutcomeInsufficientEvidence || done.Validity != rca.ValidityUnknown || done.Latest != nil || done.CompletedAt == nil || done.ArchiveChanged {
		t.Fatalf("completed: %+v", done)
	}
	// A re-analysis in progress keeps the completed verdict beside it.
	second := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"enrich": true}).Body.Bytes())
	pending := getArchiveReport(t, server, "rca-run").RootCauseSummary
	if pending.State != storage.RCAStateQueued || pending.AnalysisID != first.AnalysisID || pending.Latest == nil || pending.Latest.AnalysisID != second.AnalysisID {
		t.Fatalf("re-analysis pending: %+v", pending)
	}
	// The batch endpoint summarizes every run in one read.
	reply := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/archive/reports?project_id="+storage.DefaultProjectID, map[string]any{"ids": []string{"rca-run", "rca-baseline"}})
	var batch struct {
		Reports map[string]RunArchiveReport `json:"reports"`
	}
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &batch) != nil {
		t.Fatalf("batch: %d %s", reply.Code, reply.Body.String())
	}
	if run := batch.Reports["rca-run"].RootCauseSummary; run == nil || run.AnalysisID != first.AnalysisID || run.Latest == nil {
		t.Fatalf("batch rca-run: %+v", run)
	}
	if baseline := batch.Reports["rca-baseline"].RootCauseSummary; baseline == nil || baseline.State != rcaSummaryNone {
		t.Fatalf("batch rca-baseline: %+v", baseline)
	}
	if batch.Reports["rca-run"].RootCauseView != nil {
		t.Fatal("the batch carries the full root-cause view")
	}
}

// notificationCapture records the Slack-format webhooks a server sends.
func notificationCapture(t *testing.T, server *Server, events ...string) chan map[string]any {
	t.Helper()
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	received := make(chan map[string]any, 16)
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(hook.Close)
	server.runner.notifier = newNotificationDispatcher(hook.Client())
	settings := server.runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{Enabled: true, URL: hook.URL, Format: notificationFormatSlack, Events: events}}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	return received
}

func expectNotification(t *testing.T, received chan map[string]any) map[string]any {
	t.Helper()
	select {
	case payload := <-received:
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook")
	}
	return nil
}

func expectNoNotification(t *testing.T, received chan map[string]any) {
	t.Helper()
	select {
	case payload := <-received:
		t.Fatalf("unexpected webhook %v", payload)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestRCAAnalysisCompletedIsAnnouncedOncePerAnalysis(t *testing.T) {
	server, _ := rcaTestServer(t)
	received := notificationCapture(t, server, notificationEventRCAAnalysisCompleted)
	queue := func(body map[string]any) RCAAnalysisResponse {
		t.Helper()
		queued := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", body).Body.Bytes())
		if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
			t.Fatalf("run: %v %v", ran, err)
		}
		return queued
	}
	// A project that does not present root cause announces nothing.
	queue(map[string]any{})
	expectNoNotification(t, received)
	t.Setenv(envRCAV2PresentProjects, storage.DefaultProjectID)
	analysis := queue(map[string]any{"enrich": true})
	payload := expectNotification(t, received)
	text := fmt.Sprint(payload)
	if !strings.Contains(text, "Root-Cause Analysis Completed") || !strings.Contains(text, "too little evidence") || !strings.Contains(text, "analysis:"+analysis.AnalysisID) || !strings.Contains(text, "section=diagnostics") {
		t.Fatalf("announcement %v", payload)
	}
	expectNoNotification(t, received)
}

func TestRunValidityIsAnnouncedOncePerVerdict(t *testing.T) {
	server, _ := rcaTestServer(t)
	received := notificationCapture(t, server, notificationEventRunValidity)
	record := server.runner.archives["rca-run"]
	// The fixture archives no generator samples (validity unknown): nothing.
	server.notifyRunValidity("rca-run")
	expectNoNotification(t, received)
	server.archiveReadCaches().reports.deleteTest("rca-run")
	if _, err := server.archiveReadCaches().reports.getOrLoad(runArchiveReportCacheKey(record), func() (*RunArchiveReport, error) {
		return &RunArchiveReport{Validity: &rca.Validity{Verdict: rca.ValidityInvalid, Reasons: []string{"generator-1 was OOM-killed"}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.archiveReadCaches().reports.deleteTest("rca-run") })
	server.notifyRunValidity("rca-run")
	payload := expectNotification(t, received)
	if text := fmt.Sprint(payload); !strings.Contains(text, "Run Validity") || !strings.Contains(text, "run validity invalid: generator-1 was OOM-killed") {
		t.Fatalf("validity announcement %v", payload)
	}
	// Another attach of the same archive is not announced again.
	server.notifyRunValidity("rca-run")
	expectNoNotification(t, received)
}

func TestRCAEventsTagGrafanaAndClassifyDynatrace(t *testing.T) {
	completed := NotificationEvent{Type: notificationEventRCAAnalysisCompleted, TestID: "run", TestName: "Run", Status: rca.OutcomeSupported, Timestamp: time.Now()}
	if tags := fmt.Sprint(buildGrafanaNotificationPayload(completed)["tags"]); !strings.Contains(tags, "rca:supported") {
		t.Fatalf("grafana tags %s", tags)
	}
	validity := NotificationEvent{Type: notificationEventRunValidity, TestID: "run", TestName: "Run", Status: rca.ValidityInvalid, Severity: "danger", Timestamp: time.Now()}
	if tags := fmt.Sprint(buildGrafanaNotificationPayload(validity)["tags"]); !strings.Contains(tags, "validity:invalid") {
		t.Fatalf("grafana tags %s", tags)
	}
	if kind := buildDynatraceNotificationPayload(NotificationWebhookSettings{}, validity)["eventType"]; kind != "ERROR_EVENT" {
		t.Fatalf("invalid run event type %v", kind)
	}
	validity.Severity = "warning"
	if kind := buildDynatraceNotificationPayload(NotificationWebhookSettings{}, validity)["eventType"]; kind != "CUSTOM_ALERT" {
		t.Fatalf("suspect run event type %v", kind)
	}
	if kind := buildDynatraceNotificationPayload(NotificationWebhookSettings{}, completed)["eventType"]; kind != "CUSTOM_INFO" {
		t.Fatalf("completed analysis event type %v", kind)
	}
	for _, event := range []string{notificationEventRCAAnalysisCompleted, notificationEventRunValidity} {
		if _, ok := supportedNotificationEvents[event]; !ok {
			t.Fatalf("%s is not a supported event", event)
		}
	}
}

func TestCIRootCauseLineAndCheckRow(t *testing.T) {
	for _, tc := range []struct {
		root *ciRunRootCause
		want string
	}{
		{nil, ""},
		{&ciRunRootCause{State: "completed", Outcome: rca.OutcomeSupported, TopHypothesis: "Pool exhaustion", Confidence: "high"}, "Pool exhaustion (high confidence)"},
		{&ciRunRootCause{State: "completed", Outcome: rca.OutcomeNoSupportedHypothesis}, "no cause supported"},
		{&ciRunRootCause{State: "completed", Outcome: rca.OutcomeInsufficientEvidence}, "insufficient evidence"},
		{&ciRunRootCause{State: "none"}, "not analyzed"},
		// An automatic analysis is still to be queued.
		{&ciRunRootCause{State: "none", AutoAnalyze: true}, "analysis pending"},
		{&ciRunRootCause{State: "queued"}, "analysis queued"},
	} {
		if got := ciRootCauseText(tc.root); got != tc.want {
			t.Errorf("ciRootCauseText(%+v) = %q, want %q", tc.root, got, tc.want)
		}
	}
	summary := ciGitHubCheckSummary(&ManagedTest{ID: "run"}, ciRunResponse{Status: "COMPLETE", Outcome: "passed",
		RootCause: &ciRunRootCause{State: "completed", Outcome: rca.OutcomeSupported, TopHypothesis: "A | B", Confidence: "medium"}}, ciRunMetadata{})
	if !strings.Contains(summary, "| Root cause | A / B (medium confidence) |") {
		t.Fatalf("check summary:\n%s", summary)
	}
}

func TestCIRunRootCauseFollowsTheProjectsGates(t *testing.T) {
	server, _ := rcaTestServer(t)
	test := &ManagedTest{ID: "rca-run", ProjectID: storage.DefaultProjectID}
	if got := server.ciRunRootCause(context.Background(), test); got != nil {
		t.Fatalf("not presented: %+v", got)
	}
	t.Setenv(envRCAV2PresentProjects, storage.DefaultProjectID)
	t.Setenv(envRCAAutoAnalyzeProjects, storage.DefaultProjectID)
	if got := server.ciRunRootCause(context.Background(), test); got == nil || got.State != rcaSummaryNone || !got.AutoAnalyze || !strings.HasSuffix(got.ReportURL, "/#/report/rca-run?section=diagnostics") {
		t.Fatalf("awaiting analysis: %+v", got)
	}
	timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatalf("run: %v %v", ran, err)
	}
	if got := server.ciRunRootCause(context.Background(), test); got == nil || got.State != storage.RCAStateCompleted || got.Outcome != rca.OutcomeInsufficientEvidence || got.AnalysisID == "" || !strings.Contains(got.ReportURL, "analysis="+got.AnalysisID) {
		t.Fatalf("completed: %+v", got)
	}
}

func TestRCAWindowLinksOpenTheRunsProviders(t *testing.T) {
	server, store := baselineTestServer(t)
	linkFixtureConnections(t, store)
	project := storage.DefaultProjectID
	elkPath := "/api/v1/projects/" + project + "/monitoring/connection-links/elk/elk-links"
	dtPath := "/api/v1/projects/" + project + "/monitoring/connection-links/dynatrace/dt-links"
	// A window has no trace.
	if reply := timeslotRequest(t, server, http.MethodPut, dtPath, map[string]any{"revision": 0, "window_template": "https://abc123.live.dynatrace.com/ui/{trace_id}"}); reply.Code != http.StatusBadRequest {
		t.Fatalf("trace in a window template: %d %s", reply.Code, reply.Body.String())
	}
	// A run window names no entity either.
	if reply := timeslotRequest(t, server, http.MethodPut, dtPath, map[string]any{"revision": 0, "window_template": "https://abc123.live.dynatrace.com/ui/entity/{entity_id}?from={from_utc}"}); reply.Code != http.StatusBadRequest {
		t.Fatalf("window template with an entity: %d %s", reply.Code, reply.Body.String())
	}
	if reply := timeslotRequest(t, server, http.MethodPut, dtPath, map[string]any{"revision": 0, "window_template": "https://evil.example.com/?from={from_utc}"}); reply.Code != http.StatusBadRequest {
		t.Fatalf("window template off the connection's hosts: %d %s", reply.Code, reply.Body.String())
	}
	// Kibana's default window view is the trace default without the query;
	// Dynatrace has none until one is saved.
	saved := timeslotRequest(t, server, http.MethodPut, elkPath, map[string]any{"revision": 0, "ui_url": "https://kibana.example.test"})
	var elk ConnectionLinksResponse
	if saved.Code != http.StatusOK || json.Unmarshal(saved.Body.Bytes(), &elk) != nil || elk.WindowTemplate != "https://kibana.example.test/app/discover#/?_g=(time:(from:'{from_utc}',to:'{to_utc}'))" {
		t.Fatalf("Kibana window default: %d %s", saved.Code, saved.Body.String())
	}
	var dynatrace ConnectionLinksResponse
	if reply := timeslotRequest(t, server, http.MethodGet, dtPath, nil); json.Unmarshal(reply.Body.Bytes(), &dynatrace) != nil || dynatrace.WindowTemplate != "" {
		t.Fatalf("Dynatrace window default: %s", reply.Body.String())
	}
	if reply := timeslotRequest(t, server, http.MethodPut, dtPath, map[string]any{"revision": 0, "window_template": "https://abc123.live.dynatrace.com/ui/problems?from={from_utc}&to={to_utc}"}); reply.Code != http.StatusOK {
		t.Fatalf("Dynatrace window template: %d %s", reply.Code, reply.Body.String())
	}

	server.runner.mu.Lock()
	server.runner.tests["window-run"] = &ManagedTest{ID: "window-run", ProjectID: project, Status: "COMPLETE",
		DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt-links"}, ELKCapture: &ELKCaptureConfig{ConnectionID: "elk-links"}}
	server.runner.mu.Unlock()
	path := "/api/v1/tests/window-run/archive/root-cause/links"
	for _, query := range []string{"", "?from=2026-10-09T10:00:00Z", "?from=2026-10-09T11:00:00Z&to=2026-10-09T10:00:00Z", "?from=2026-10-01T10:00:00Z&to=2026-10-09T10:00:00Z"} {
		if reply := timeslotRequest(t, server, http.MethodGet, path+query, nil); reply.Code != http.StatusBadRequest {
			t.Errorf("window %q: %d %s", query, reply.Code, reply.Body.String())
		}
	}
	reply := timeslotRequest(t, server, http.MethodGet, path+"?from=2026-10-09T10:00:00Z&to=2026-10-09T10:05:00Z", nil)
	var links RCAWindowLinks
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &links) != nil || len(links.Links) != 2 {
		t.Fatalf("window links: %d %s", reply.Code, reply.Body.String())
	}
	for _, link := range links.Links {
		if !strings.Contains(link.URL, "2026-10-09T10%3A00%3A00Z") && !strings.Contains(link.URL, "2026-10-09T10:00:00Z") {
			t.Errorf("%s link does not carry the window: %s", link.Provider, link.URL)
		}
	}
	if reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/missing/archive/root-cause/links?from=2026-10-09T10:00:00Z&to=2026-10-09T10:05:00Z", nil); reply.Code != http.StatusNotFound {
		t.Fatalf("missing run: %d", reply.Code)
	}
}

func TestRCACoverageNamesEachMonitorSourceAndTrimsThemFirst(t *testing.T) {
	sources := []rcaEvidenceSource{{SourceID: "b-ok", Pack: "prometheus", State: "completed"}, {SourceID: "z-gap", Pack: "elastic-query", State: "failed", CoverageReason: "query_failed", Required: true}, {SourceID: "a-ok", Pack: "prometheus", State: "completed"}}
	got := rcaCoverageSources(sources)
	if len(got) != 3 || got[0].ID != "z-gap" || got[0].Reason != "query_failed" || !got[0].Required || got[1].ID != "a-ok" || got[2].ID != "b-ok" {
		t.Fatalf("coverage sources %+v", got)
	}
	many := make([]rcaEvidenceSource, maxRCACoverageSources+10)
	for i := range many {
		many[i] = rcaEvidenceSource{SourceID: fmt.Sprintf("s-%03d", i)}
	}
	if capped := rcaCoverageSources(many); len(capped) != maxRCACoverageSources {
		t.Fatalf("coverage sources were not capped: %d", len(capped))
	}
	assessment := &rca.Assessment{Coverage: []rca.Coverage{{Evidence: "monitor_sources", Status: rca.CoveragePartial, Sources: rcaCoverageSources(many)}}, Validity: rca.Validity{Windows: []rca.ValidityWindow{{Verdict: rca.ValiditySuspect}}}}
	calls := 0
	trimRCAAssessment(assessment, func() bool { calls++; return len(assessment.Coverage[0].Sources) == 0 })
	if len(assessment.Coverage[0].Sources) != 0 || len(assessment.Validity.Windows) != 1 {
		t.Fatalf("trim did not cut sources before validity windows: %d sources, %d windows", len(assessment.Coverage[0].Sources), len(assessment.Validity.Windows))
	}
}

// TestRCAReportLinkMatchesTheDashboardRoute keeps the server's report route
// in step with appRouting's root-cause parameters.
func TestRCAReportLinkMatchesTheDashboardRoute(t *testing.T) {
	link := rcaReportLink("run-1", "analysis-1", "hypothesis-1")
	if !regexp.MustCompile(`^#/report/run-1\?section=diagnostics&analysis=analysis-1&hypothesis=hypothesis-1$`).MatchString(link) {
		t.Fatalf("link %s", link)
	}
}

// TestLocalRunAnnouncesItsAttachedArchiveOnce: a local run's archive is
// published before the run is terminal in memory, so finalization cannot
// attach it; the completion path attaches it and runs the archive-attached
// hook (automatic root-cause analysis among its consumers) exactly once.
func TestLocalRunAnnouncesItsAttachedArchiveOnce(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = store.Close()
	})
	announced := make(chan string, 4)
	server.runner.archiveAttachedHook = func(testID string) { announced <- testID }
	server.runner.StoreScript(&dsl.Script{ID: "attach-hook", Name: "Attach hook", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: target.URL}, Actions: []dsl.Action{{Name: "home", Type: "request", Method: "GET", Target: "/"}}})
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "Attach hook", ScriptID: "attach-hook", VUsers: 1, Duration: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.runner.StartTest(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForTestStatusWithin(t, server.runner, run.ID, time.Minute, "COMPLETE")
	select {
	case id := <-announced:
		if id != run.ID {
			t.Fatalf("announced %s, want %s", id, run.ID)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the completed run's archive was never announced")
	}
	select {
	case id := <-announced:
		t.Fatalf("the archive was announced twice (%s)", id)
	case <-time.After(time.Second):
	}
	if current, ok := server.runner.GetTestSummary(run.ID); !ok || current.Archive == nil {
		t.Fatalf("the completed run has no attached archive: %+v", current)
	}
}
