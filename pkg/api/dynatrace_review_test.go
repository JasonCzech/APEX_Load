package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDynatraceReviewSelectedCapabilitiesResolveOnlyRequiredCredentials(t *testing.T) {
	snapshot := &storage.DynatraceConnectionSnapshot{ProjectID: "project", Config: dynatrace.Config{EnvironmentURL: "https://sample.live.dynatrace.com", PlatformURL: "https://sample.apps.dynatrace.com", PlatformAuthType: dynatrace.AuthToken}, AllowedCapabilities: dynatrace.Capabilities{Metrics: true, Logs: true}, EnvironmentTokenSecret: "environment", PlatformTokenSecret: "unavailable-platform"}
	resolver := engine.SecretResolverFunc(func(name string) (string, bool) {
		if name != "environment" {
			t.Fatalf("unused credential resolved: %s", name)
		}
		return "token", true
	})
	selection := &dynatrace.Selection{ProjectID: "project", Metrics: []dynatrace.MetricSelection{{Name: "calls"}}}
	credentials, err := resolveNativeDynatraceCredentials(context.Background(), resolver, snapshot, selection)
	if err != nil || credentials.EnvironmentToken != "token" || credentials.PlatformToken != "" {
		t.Fatalf("selected credentials %+v %v", credentials, err)
	}
	classicDiscovery := dynatraceOperationSnapshot(snapshot, "discover", dynatrace.DiscoveryRequest{Kind: "metrics"})
	credentials, err = resolveNativeDynatraceCredentials(t.Context(), resolver, classicDiscovery)
	if err != nil || credentials.EnvironmentToken != "token" || credentials.PlatformToken != "" || !snapshot.AllowedCapabilities.Logs {
		t.Fatalf("Classic discovery resolved unrelated credentials or mutated connection: %+v %v", credentials, err)
	}
	platformResolver := engine.SecretResolverFunc(func(name string) (string, bool) {
		if name != "unavailable-platform" {
			t.Fatalf("unused Environment credential resolved: %s", name)
		}
		return "platform-token", true
	})
	platformDiscovery := dynatraceOperationSnapshot(snapshot, "discover", dynatrace.DiscoveryRequest{Kind: "smartscape"})
	credentials, err = resolveNativeDynatraceCredentials(t.Context(), platformResolver, platformDiscovery)
	if err != nil || credentials.PlatformToken != "platform-token" || credentials.EnvironmentToken != "" {
		t.Fatalf("Smartscape discovery resolved unrelated credentials: %+v %v", credentials, err)
	}
}

func TestDynatraceReviewQuotaUsesIndependentPrincipals(t *testing.T) {
	server, _, job, _ := apiDynatraceNativeFixture(t)
	snapshot := *job.DynatraceSnapshot
	snapshot.Config.PlatformURL = "https://fixture.apps.dynatrace.com"
	snapshot.Config.PlatformAuthType = dynatrace.AuthToken
	snapshot.AllowedCapabilities.Logs = true
	credentials := dynatrace.Credentials{EnvironmentToken: "same-environment", PlatformToken: "unused-platform", VersionIdentity: "bundle-version-a"}
	quotas := server.store.(interface {
		AllocateDynatraceQuota(string, string, time.Time) (bool, error)
	})
	// An unused Platform origin must not prevent Environment-only collection.
	platformKey := dynatrace.RequestQuotaIdentity(snapshot.Config, credentials, true)
	for _, executor := range []string{"other-platform-a", "other-platform-b"} {
		if admitted, err := quotas.AllocateDynatraceQuota(platformKey, executor, time.Now().UTC()); err != nil || !admitted {
			t.Fatalf("platform reservation %v %v", admitted, err)
		}
	}
	if _, err := server.dynatraceQuotaConfig(context.Background(), &snapshot, credentials, "environment-a", job.Source.Dynatrace); err != nil {
		t.Fatal(err)
	}
	classicDiscovery := dynatraceOperationSnapshot(&snapshot, "discover", dynatrace.DiscoveryRequest{Kind: "metrics"})
	if _, err := server.dynatraceQuotaConfig(t.Context(), classicDiscovery, credentials, "environment-a"); err != nil {
		t.Fatalf("unused Platform lease blocked Classic discovery: %v", err)
	}
	credentials.PlatformToken, credentials.VersionIdentity = "different-platform", "bundle-version-b"
	if _, err := server.dynatraceQuotaConfig(context.Background(), &snapshot, credentials, "environment-b", job.Source.Dynatrace); err != nil {
		t.Fatal(err)
	}
	credentials.VersionIdentity = "bundle-version-c"
	if _, err := server.dynatraceQuotaConfig(context.Background(), &snapshot, credentials, "environment-c", job.Source.Dynatrace); err == nil {
		t.Fatal("unrelated credential changes split the Environment quota")
	}
}

func TestDynatraceReviewPreparationGroupsIndependentAPIBudgets(t *testing.T) {
	server, _, job, _ := apiDynatraceNativeFixture(t)
	connections := server.store.(storage.DynatraceConnectionStore)
	connection, err := connections.GetDynatraceConnection(job.Source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	secondConnection := *connection
	secondConnection.ID = "same-environment-other-platform"
	secondConnection.Name = "Second SaaS profile"
	secondConnection.PlatformURL = "https://fixture.apps.dynatrace.com"
	secondConnection.PlatformAuthType = storage.DynatracePlatformAuthToken
	secondConnection.PlatformTokenSecret = "different-platform-ref"
	secondConnection.EnvironmentTokenSecret = "environment-alias"
	if err := connections.CreateDynatraceConnection(&secondConnection); err != nil {
		t.Fatal(err)
	}
	first, second := job.Source, job.Source
	selection := *first.Dynatrace
	selection.Metrics = nil
	for i := range 15 {
		selection.Metrics = append(selection.Metrics, dynatrace.MetricSelection{Name: string(rune('a' + i)), MetricSelector: "builtin:service.requestCount.total", Aggregation: "sum", Unit: "count"})
	}
	first.Dynatrace, second.Dynatrace = &selection, &selection
	second.ID, second.ConnectionID = "second-budget-source", secondConnection.ID
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: first.ID, Required: true}, {SourceID: second.ID, Required: true}}}
	resolver := engine.SecretResolverFunc(func(string) (string, bool) { return "same-upstream-token", true })
	if err := validateDynatraceCollectionBudgets(t.Context(), connections, resolver, job.ProjectID, bindings, []storage.MonitorSource{first, second}); err == nil {
		t.Fatal("unrelated Platform configuration or secret aliases split the shared Environment preparation budget")
	}
	resolver = engine.SecretResolverFunc(func(name string) (string, bool) { return name, true })
	if err := validateDynatraceCollectionBudgets(t.Context(), connections, resolver, job.ProjectID, bindings, []storage.MonitorSource{first, second}); err != nil {
		t.Fatalf("independent actual principals shared preparation budget: %v", err)
	}
}

func TestDynatraceReviewNativeCollectorRoutesAreRegistered(t *testing.T) {
	server, _ := newTimeslotServer(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/monitoring/collectors/missing/dynatrace-operations"},
		{http.MethodPost, "/api/v1/monitoring/collectors/missing/dynatrace-operations/missing/result"},
		{http.MethodPost, "/api/v1/monitoring/collectors/missing/jobs/missing/scan-reservations"},
	} {
		response := timeslotRequest(t, server, route.method, route.path, nil)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"error":"monitoring collector not found"`) {
			t.Fatalf("native collector route %s %s: %d %s", route.method, route.path, response.Code, response.Body.String())
		}
	}
}

func TestDynatraceReviewApprovedSourcePreviewAuthorization(t *testing.T) {
	server, store, job, _ := apiDynatraceNativeFixture(t)
	server.runner.leadership = &executionLeadership{now: time.Now} // Queue fixtures without network work.
	t.Setenv(devFakeSecretsEnv, "true")
	enableTrustedHeaderAuth(t, server)
	base := "/api/v1/projects/proj-a/monitoring/dynatrace-connections/" + job.Source.ConnectionID + "/operations"
	request := func(method, path, role string, body any) *httptest.ResponseRecorder {
		r := scopedLibraryRequest(t, method, path, body)
		r.Header.Set("X-Auth-Request-User", "preview-"+role)
		r.Header.Set("X-APEXLoad-Project-Roles", "proj-a="+role)
		w := httptest.NewRecorder()
		server.mux.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		name, role string
		body       DynatraceOperationRequest
		status     int
	}{
		{"editor saved source", RoleEditor, DynatraceOperationRequest{Kind: "preview", SourceID: job.Source.ID}, 202},
		{"viewer", RoleViewer, DynatraceOperationRequest{Kind: "preview", SourceID: job.Source.ID}, 403},
		{"editor arbitrary query", RoleEditor, DynatraceOperationRequest{Kind: "preview", Selection: job.Source.Dynatrace}, 403},
		{"editor verify", RoleEditor, DynatraceOperationRequest{Kind: "verify"}, 403},
		{"editor discovery", RoleEditor, DynatraceOperationRequest{Kind: "discover"}, 403},
		{"source query override", RoleEditor, DynatraceOperationRequest{Kind: "preview", SourceID: job.Source.ID, Selection: job.Source.Dynatrace}, 400},
		{"source options override", RoleEditor, DynatraceOperationRequest{Kind: "preview", SourceID: job.Source.ID, Options: dynatrace.DiscoveryRequest{Limit: 1}}, 400},
		{"foreign source", RoleEditor, DynatraceOperationRequest{Kind: "preview", SourceID: "foreign"}, 409},
		{"admin arbitrary preview", RoleProjectAdmin, DynatraceOperationRequest{Kind: "preview", Selection: job.Source.Dynatrace}, 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := request(http.MethodPost, base, tc.role, tc.body)
			if response.Code != tc.status {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
			if tc.status != 202 {
				return
			}
			var queued DynatraceOperationResponse
			if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
				t.Fatal(err)
			}
			op, err := server.store.(storage.DynatraceOperationStore).GetDynatraceOperation(queued.ID)
			if err != nil {
				t.Fatal(err)
			}
			var work dynatraceOperationWork
			if err := json.Unmarshal([]byte(op.PayloadJSON), &work); err != nil {
				t.Fatal(err)
			}
			if tc.body.SourceID != "" && (work.SourceID != job.Source.ID || work.SourceRevision != job.Source.Revision || work.Selection.Metrics[0] != job.Source.Dynatrace.Metrics[0]) {
				t.Fatalf("approved source not frozen: %+v", work)
			}
			wantRead := 403
			if tc.body.SourceID != "" {
				wantRead = 200
			}
			read := request(http.MethodGet, base+"/"+queued.ID, RoleEditor, nil)
			if read.Code != wantRead {
				t.Fatalf("editor read %d %s", read.Code, read.Body.String())
			}
			adminRead := request(http.MethodGet, base+"/"+queued.ID, RoleProjectAdmin, nil)
			if adminRead.Code != 200 || strings.Contains(adminRead.Body.String(), "protected-environment-ref") {
				t.Fatalf("admin read %d %s", adminRead.Code, adminRead.Body.String())
			}
		})
	}
	queued := request(http.MethodPost, base, RoleEditor, DynatraceOperationRequest{Kind: "preview", SourceID: job.Source.ID})
	var op DynatraceOperationResponse
	if queued.Code != 202 || json.Unmarshal(queued.Body.Bytes(), &op) != nil {
		t.Fatalf("queue %d %s", queued.Code, queued.Body.String())
	}
	source := job.Source
	source.Enabled = false
	if _, err := store.SaveMonitorSource(source, source.Revision); err != nil {
		t.Fatal(err)
	}
	if read := request(http.MethodGet, base+"/"+op.ID, RoleEditor, nil); read.Code != 409 {
		t.Fatalf("revoked source read %d %s", read.Code, read.Body.String())
	}
	if create := request(http.MethodPost, base, RoleEditor, DynatraceOperationRequest{Kind: "preview", SourceID: source.ID}); create.Code != 409 {
		t.Fatalf("disabled source preview %d %s", create.Code, create.Body.String())
	}
}
