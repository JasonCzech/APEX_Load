package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestC4ProfileCatalogEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{})

	apmReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/apm/profiles", nil)
	apmResp := httptest.NewRecorder()
	server.mux.ServeHTTP(apmResp, apmReq)
	if apmResp.Code != http.StatusOK {
		t.Fatalf("apm profiles status = %d body=%s", apmResp.Code, apmResp.Body.String())
	}
	var apmPayload APMIntegrationProfilesResponse
	if err := json.NewDecoder(apmResp.Body).Decode(&apmPayload); err != nil {
		t.Fatalf("decode apm profiles: %v", err)
	}
	if len(apmPayload.Profiles) < 5 {
		t.Fatalf("profiles len = %d, want named APM integrations", len(apmPayload.Profiles))
	}
	apmProfiles := map[string]APMIntegrationProfile{}
	for _, profile := range apmPayload.Profiles {
		apmProfiles[profile.ID] = profile
	}
	if apmProfiles["dynatrace"].DryRunEndpoint != "/api/v1/integrations/dynatrace/dry-run" || !stringSliceContains(apmProfiles["dynatrace"].Capabilities, "dry_run_payload_export") {
		t.Fatalf("dynatrace profile = %#v, want dry-run capability and endpoint", apmProfiles["dynatrace"])
	}
	if apmPayload.GenericCorrelation.Status != "supported" || len(apmPayload.GenericCorrelation.CorrelationFields) == 0 {
		t.Fatalf("generic correlation = %#v, want supported trace fields", apmPayload.GenericCorrelation)
	}

	packReq := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/packs", nil)
	packResp := httptest.NewRecorder()
	server.mux.ServeHTTP(packResp, packReq)
	if packResp.Code != http.StatusOK {
		t.Fatalf("monitor packs status = %d body=%s", packResp.Code, packResp.Body.String())
	}
	var packPayload InfrastructureMonitorPacksResponse
	if err := json.NewDecoder(packResp.Body).Decode(&packPayload); err != nil {
		t.Fatalf("decode monitor packs: %v", err)
	}
	packs := map[string]InfrastructureMonitorPack{}
	for _, pack := range packPayload.Packs {
		packs[pack.ID] = pack
	}
	for _, id := range []string{"kubernetes", "linux-hosts", "jvm", "database", "web-server", "queue-brokers"} {
		if packs[id].ID == "" {
			t.Fatalf("missing monitor pack %q in %#v", id, packPayload.Packs)
		}
	}
	for _, id := range []string{"cloudwatch", "azure-monitor"} {
		if packs[id].Status != "preview" || len(packs[id].SetupActions) == 0 {
			t.Fatalf("cloud pack %s must retain preview status and setup requirements: %#v", id, packs[id])
		}
	}
	databasePack := packs["database"]
	if len(databasePack.Profiles) == 0 || databasePack.Profiles[0].ID != "oracle-database" {
		t.Fatalf("database pack profiles = %#v, want Oracle child profile", databasePack.Profiles)
	}
	oracleProfile := databasePack.Profiles[0]
	for _, metric := range []string{"oracle.db.cpu_pct", "oracle.db.session_active", "oracle.db.wait_time_ms", "oracle.db.lock_waits", "oracle.db.parse_rate", "oracle.db.physical_reads", "oracle.db.redo_mb_s"} {
		if !stringSliceContains(oracleProfile.RequiredMetrics, metric) || !stringSliceContains(oracleProfile.MissingMetrics, metric) {
			t.Fatalf("oracle profile metrics = required %#v missing %#v, want %q", oracleProfile.RequiredMetrics, oracleProfile.MissingMetrics, metric)
		}
	}
	if len(oracleProfile.ExampleExternalMetrics) == 0 || oracleProfile.ExampleExternalMetrics[0].Format != "prometheus" {
		t.Fatalf("oracle examples = %#v, want Prometheus external metric examples", oracleProfile.ExampleExternalMetrics)
	}

	profilesReq := httptest.NewRequest(http.MethodGet, "/api/v1/reports/profiles", nil)
	profilesResp := httptest.NewRecorder()
	server.mux.ServeHTTP(profilesResp, profilesReq)
	if profilesResp.Code != http.StatusOK {
		t.Fatalf("report profiles status = %d body=%s", profilesResp.Code, profilesResp.Body.String())
	}
	var profiles ReportProfilesResponse
	if err := json.NewDecoder(profilesResp.Body).Decode(&profiles); err != nil {
		t.Fatalf("decode report profiles: %v", err)
	}
	if profiles.DefaultProfile != "engineer" || len(profiles.Profiles) != 3 {
		t.Fatalf("report profiles = %#v, want executive/engineer/audit catalog", profiles)
	}
	if !stringSliceContains(profiles.DownloadFormats, "pdf") {
		t.Fatalf("download formats = %#v, want pdf", profiles.DownloadFormats)
	}
}

func TestRunArchiveRootCauseSummarizesSLAAndTelemetryFindings(t *testing.T) {
	server := NewServer(ServerConfig{})
	releaseID := "release-c4-root"
	started := time.Date(2026, time.May, 19, 9, 0, 0, 0, time.UTC)
	completed := started.Add(5 * time.Minute)
	archive := writeReleaseTrendArchiveFixture(t, releaseID, "run-root", "Root Cause Run", started, completed, true)

	server.runner.mu.Lock()
	server.runner.tests["run-root"] = &ManagedTest{
		ID:        "run-root",
		Name:      "Root Cause Run",
		Status:    "COMPLETE",
		ReleaseID: releaseID,
		CreatedAt: started,
		StartedAt: ptrTime(started),
		EndedAt:   ptrTime(completed),
		Archive:   archive,
	}
	server.runner.archives["run-root"] = archive
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-root/archive/root-cause", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("root-cause status = %d body=%s", resp.Code, resp.Body.String())
	}
	var summary RunRootCauseSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode root-cause summary: %v", err)
	}
	if summary.Status != "breached" {
		t.Fatalf("root-cause status = %q, want breached: %#v", summary.Status, summary)
	}
	categories := map[string]bool{}
	for _, finding := range summary.TopFindings {
		categories[finding.Category] = true
	}
	for _, category := range []string{"sla", "errors", "telemetry"} {
		if !categories[category] {
			t.Fatalf("missing finding category %q in %#v", category, summary.TopFindings)
		}
	}
	if len(summary.NextActions) == 0 {
		t.Fatalf("expected next actions in root-cause summary")
	}
}

func TestReleaseSLAGovernancePersistsWaiversAndAuditEvents(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now()
	if err := store.SaveProject(&storage.Project{ID: "project-c4", Name: "C4 Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	app := &storage.Application{ID: "app-c4", ProjectID: "project-c4", Name: "Checkout", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveApplication(app); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	release := &storage.Release{ID: "release-c4", ApplicationID: app.ID, Name: "2026.05", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})
	started := time.Date(2026, time.May, 19, 10, 0, 0, 0, time.UTC)
	completed := started.Add(4 * time.Minute)
	archive := writeReleaseTrendArchiveFixture(t, release.ID, "run-sla", "SLA Run", started, completed, true)
	server.runner.mu.Lock()
	server.runner.tests["run-sla"] = &ManagedTest{
		ID:        "run-sla",
		Name:      "SLA Run",
		Status:    "COMPLETE",
		ReleaseID: release.ID,
		ProjectID: app.ProjectID,
		CreatedAt: started,
		StartedAt: ptrTime(started),
		EndedAt:   ptrTime(completed),
		Archive:   archive,
	}
	server.runner.archives["run-sla"] = archive
	server.runner.mu.Unlock()

	initialReq := httptest.NewRequest(http.MethodGet, "/api/v1/releases/"+release.ID+"/sla-governance", nil)
	initialResp := httptest.NewRecorder()
	server.mux.ServeHTTP(initialResp, initialReq)
	if initialResp.Code != http.StatusOK {
		t.Fatalf("initial governance status = %d body=%s", initialResp.Code, initialResp.Body.String())
	}
	var initial ReleaseSLAGovernanceResponse
	if err := json.NewDecoder(initialResp.Body).Decode(&initial); err != nil {
		t.Fatalf("decode initial governance: %v", err)
	}
	if initial.Status != "failed" || initial.Summary.UnwaivedBreaches != 1 {
		t.Fatalf("initial governance = %#v, want failed unwaived breach", initial)
	}

	createBody := bytes.NewBufferString(`{"reason":"Accepted for release-candidate dry run","approved_by":"release manager"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/releases/"+release.ID+"/sla-governance/waivers", createBody)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create waiver status = %d body=%s", createResp.Code, createResp.Body.String())
	}

	finalReq := httptest.NewRequest(http.MethodGet, "/api/v1/releases/"+release.ID+"/sla-governance", nil)
	finalResp := httptest.NewRecorder()
	server.mux.ServeHTTP(finalResp, finalReq)
	if finalResp.Code != http.StatusOK {
		t.Fatalf("final governance status = %d body=%s", finalResp.Code, finalResp.Body.String())
	}
	var final ReleaseSLAGovernanceResponse
	if err := json.NewDecoder(finalResp.Body).Decode(&final); err != nil {
		t.Fatalf("decode final governance: %v", err)
	}
	if final.Status != "waived" || len(final.Waivers) != 1 || final.Summary.WaivedBreaches != 1 {
		t.Fatalf("final governance = %#v, want waived breach with persisted waiver", final)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "release.sla_waiver.create", ResourceID: release.ID, Limit: 5})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events len = %d, want waiver audit event", len(events))
	}
}

func TestReleaseFamilyTrendAndGovernanceAggregateApplicationMembers(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &storage.Project{ID: "family-governance-project", Name: "Family governance", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	applications := []*storage.Application{
		{ID: "family-governance-a", ProjectID: project.ID, Name: "A", CreatedAt: now, UpdatedAt: now},
		{ID: "family-governance-b", ProjectID: project.ID, Name: "B", CreatedAt: now, UpdatedAt: now},
	}
	for _, application := range applications {
		if err := store.SaveApplication(application); err != nil {
			t.Fatalf("SaveApplication(%s): %v", application.ID, err)
		}
	}
	created, err := store.CreateProjectReleaseFamily(project.ID, "2608", applications[0].ID, now)
	if err != nil {
		t.Fatalf("CreateProjectReleaseFamily: %v", err)
	}
	members, err := store.ListReleaseFamilyMembers(created.GroupID)
	if err != nil || len(members) != 2 {
		t.Fatalf("ListReleaseFamilyMembers = %#v, err=%v", members, err)
	}
	server := NewServer(ServerConfig{Store: store})
	for index, member := range members {
		started := now.Add(time.Duration(index) * time.Hour)
		completed := started.Add(5 * time.Minute)
		runID := "family-governance-run-" + member.ApplicationID
		archive := writeReleaseTrendArchiveFixture(t, member.ID, runID, runID, started, completed, index == 1)
		server.runner.mu.Lock()
		server.runner.tests[runID] = &ManagedTest{
			ID: runID, Name: runID, Status: "COMPLETE", ReleaseID: member.ID,
			ApplicationID: member.ApplicationID, ProjectID: project.ID,
			CreatedAt: started, StartedAt: ptrTime(started), EndedAt: ptrTime(completed), Archive: archive,
		}
		server.runner.archives[runID] = archive
		server.runner.mu.Unlock()
	}

	trendRuns, err := server.collectReleaseTrendRuns(created.GroupID, false)
	if err != nil || len(trendRuns) != 2 {
		t.Fatalf("family trend runs = %#v, err=%v", trendRuns, err)
	}
	physicalTrendRequest := httptest.NewRequest(http.MethodGet, "/api/v1/releases/"+members[1].ID+"/trend", nil)
	physicalTrendRequest.SetPathValue("id", members[1].ID)
	physicalTrendResponse := httptest.NewRecorder()
	server.handleGetReleaseTrend(physicalTrendResponse, physicalTrendRequest)
	if physicalTrendResponse.Code != http.StatusOK {
		t.Fatalf("physical member trend status = %d body=%s", physicalTrendResponse.Code, physicalTrendResponse.Body.String())
	}
	var trend ReleaseTrend
	if err := json.Unmarshal(physicalTrendResponse.Body.Bytes(), &trend); err != nil {
		t.Fatalf("decode physical member trend: %v", err)
	}
	if trend.ReleaseID != created.GroupID || trend.RunCount != 2 {
		t.Fatalf("physical member trend = %#v, want canonical family identity and both runs", trend)
	}
	waiver := &storage.ReleaseSLAWaiver{
		ID: "family-waiver", ReleaseID: members[1].ID, ProjectID: project.ID,
		Scope: "release", Reason: "Accepted family risk", ApprovedBy: "Reviewer", Status: "active",
		CreatedAt: now.Add(2 * time.Hour), UpdatedAt: now.Add(2 * time.Hour),
	}
	if err := store.SaveReleaseSLAWaiver(waiver); err != nil {
		t.Fatalf("SaveReleaseSLAWaiver: %v", err)
	}
	governance, err := server.buildReleaseSLAGovernance(created.GroupID)
	if err != nil {
		t.Fatalf("buildReleaseSLAGovernance: %v", err)
	}
	if governance.ReleaseID != created.GroupID || governance.Summary.TotalRuns != 2 || governance.Summary.WaivedBreaches != 1 || len(governance.Waivers) != 1 || governance.Waivers[0].ReleaseID != created.GroupID {
		t.Fatalf("family governance = %#v", governance)
	}
}

func TestExternalResultImportPersistsK6Summary(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-c4", Name: "C4", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveApplication(&storage.Application{ID: "application-c4", ProjectID: "project-c4", Name: "C4", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if err := store.SaveRelease(&storage.Release{ID: "release-c4", ApplicationID: "application-c4", ProjectID: "project-c4", Name: "C4", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})

	body := bytes.NewBufferString(`{
		"provider":"k6",
		"name":"Checkout k6 smoke",
		"project_id":"project-c4",
		"release_id":"release-c4",
		"source":"ci://build/42",
		"result":{
			"metrics":{
				"http_reqs":{"count":42},
				"http_req_failed":{"rate":0.047619,"fails":2},
				"http_req_duration":{"avg":118.2,"p(95)":240.5}
			}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/results/import", body)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("import status = %d body=%s", resp.Code, resp.Body.String())
	}
	var created struct {
		Import storage.ExternalResultImport `json:"import"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created import: %v", err)
	}
	if created.Import.Provider != "k6" || created.Import.Summary["transactions"].(float64) != 42 {
		t.Fatalf("created import = %#v, want k6 summary", created.Import)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/results/imports?project_id=project-c4&provider=k6", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list imports status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listed struct {
		Imports []storage.ExternalResultImport `json:"imports"`
		Count   int                            `json:"count"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode listed imports: %v", err)
	}
	if listed.Count != 1 || listed.Imports[0].Summary["errors"].(float64) != 2 {
		t.Fatalf("listed imports = %#v, want persisted k6 error summary", listed)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "results.external_import.create", ProjectID: "project-c4", Limit: 5})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events len = %d, want external import audit event", len(events))
	}
}
