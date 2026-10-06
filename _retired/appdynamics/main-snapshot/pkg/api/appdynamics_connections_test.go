package api

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func appDynamicsRequestFixture() AppDynamicsConnectionRequest {
	id, secret := "APD_CLIENT_ID", "APD_CLIENT_SECRET"
	return AppDynamicsConnectionRequest{Name: "APM", Enabled: false, CollectionMode: "collector", CollectorAgentID: "appd-collector", Config: appdynamics.Config{Endpoint: "https://apm.example.com", AccountName: "account", AuthType: "oauth"}, ClientIDSecret: &id, ClientSecretSecret: &secret}
}

func TestAppDynamicsCapabilityProjectionPreservesIndependentDiscoveryHealth(t *testing.T) {
	caps := sanitizeAppDynamicsCapabilities(appdynamics.Capabilities{
		MetricAPI: "metric-data-v2",
		Status: map[string]string{
			"controller": "available", "discovery": "forbidden", "metrics": "available_no_data",
			"analytics": "unlicensed", "internal": "available", "snapshots": "secret-value",
		},
	})
	if caps.Status["discovery"] != "forbidden" || caps.Status["controller"] != "available" || caps.Status["metrics"] != "available_no_data" || caps.Status["analytics"] != "unlicensed" {
		t.Fatalf("independent capability outcomes lost: %+v", caps.Status)
	}
	if _, ok := caps.Status["internal"]; ok {
		t.Fatal("internal capability key disclosed")
	}
	if _, ok := caps.Status["snapshots"]; ok {
		t.Fatal("unapproved capability status disclosed")
	}
}

func TestAppDynamicsConnectionCRUDWriteOnlyReferencesAndRevision(t *testing.T) {
	server, durable := newTimeslotServer(t)
	base := "/api/v1/projects/proj-a/monitoring/appdynamics-connections"
	req := appDynamicsRequestFixture()
	created := timeslotRequest(t, server, http.MethodPost, base, req)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "APD_CLIENT") {
		t.Fatal("secret references disclosed")
	}
	var connection AppDynamicsConnectionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &connection); err != nil {
		t.Fatal(err)
	}
	if !connection.HasClientID || !connection.HasClientSecret || connection.Revision != 1 {
		t.Fatalf("projection: %+v", connection)
	}
	path := base + "/" + connection.ID
	if got := timeslotRequest(t, server, http.MethodGet, strings.Replace(path, "proj-a", "other", 1), nil); got.Code != 404 {
		t.Fatalf("cross-project get: %d", got.Code)
	}
	req.Revision = 1
	req.Name = "Updated"
	req.ClientIDSecret = nil
	req.ClientSecretSecret = nil
	updated := timeslotRequest(t, server, http.MethodPut, path, req)
	if updated.Code != 200 {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}
	saved, err := durable.(storage.AppDynamicsConnectionStore).GetAppDynamicsConnection(connection.ID)
	if err != nil || saved.ClientSecretSecretName != "APD_CLIENT_SECRET" || saved.Revision != 2 {
		t.Fatalf("preservation %+v %v", saved, err)
	}
	if got := timeslotRequest(t, server, http.MethodPut, path, req); got.Code != 409 {
		t.Fatalf("stale update: %d", got.Code)
	}
	if got := timeslotRequest(t, server, http.MethodDelete, path, nil); got.Code != 400 {
		t.Fatalf("missing delete revision: %d", got.Code)
	}
	if got := timeslotRequest(t, server, http.MethodDelete, path+"?revision=1", nil); got.Code != 409 {
		t.Fatalf("stale delete: %d", got.Code)
	}
	if got := timeslotRequest(t, server, http.MethodDelete, path+"?revision=2", nil); got.Code != 204 {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
}
func TestAppDynamicsCollectorInspectionEnrollmentLeaseAndHealthCAS(t *testing.T) {
	signingPublic, signingKey, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(signingKey.Seed()))
	server, durable := newTimeslotServer(t)
	server.runner.projectSecretStore = &devSecretStore{value: "resolved-sensitive-value"}
	now := time.Now().UTC()
	encryptionKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := base64.StdEncoding.EncodeToString(encryptionKey.PublicKey().Bytes())
	if err = durable.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "appd-collector", ProjectID: "proj-a", Name: "collector", Health: "healthy", LastSeenAt: &now, TokenHash: hashAPIKey("collector-token"), AllowedMetricPacks: []string{"appdynamics"}}); err != nil {
		t.Fatal(err)
	}
	req := appDynamicsRequestFixture()
	base := "/api/v1/projects/proj-a/monitoring/appdynamics-connections"
	created := timeslotRequest(t, server, http.MethodPost, base, req)
	var conn AppDynamicsConnectionResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &conn) != nil {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	path := base + "/" + conn.ID
	if got := timeslotRequest(t, server, http.MethodPost, path+"/test", AppDynamicsInspectionRequest{CollectionMode: "direct"}); got.Code != 400 {
		t.Fatalf("transport override: %d", got.Code)
	}
	if got := timeslotRequest(t, server, http.MethodPost, path+"/test", nil); got.Code != 409 {
		t.Fatalf("unenrolled destination: %d %s", got.Code, got.Body.String())
	}
	if err = durable.(storage.RunMonitorStore).SaveCollectorRunReadiness("proj-a", "appd-collector", []string{"appdynamics"}, []string{req.Config.Endpoint + "#" + publicKey}, now); err != nil {
		t.Fatal(err)
	}
	if got := timeslotRequest(t, server, http.MethodPost, path+"/test", nil); got.Code != 409 {
		t.Fatalf("legacy pack advertisement authorized typed inspection: %d", got.Code)
	}
	if err = durable.(storage.RunMonitorStore).SaveCollectorRunReadiness("proj-a", "appd-collector", []string{"appdynamics", collectoragent.AppDynamicsBatchContract}, []string{req.Config.Endpoint + "#" + publicKey}, now); err != nil {
		t.Fatal(err)
	}
	if got := timeslotRequest(t, server, http.MethodPost, path+"/test", AppDynamicsInspectionRequest{CollectorPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}); got.Code != 400 {
		t.Fatalf("substitute identity accepted %d", got.Code)
	}
	queued := timeslotRequest(t, server, http.MethodPost, path+"/test", nil)
	var operation AppDynamicsOperationResponse
	if queued.Code != 202 || json.Unmarshal(queued.Body.Bytes(), &operation) != nil {
		t.Fatalf("queue: %d %s", queued.Code, queued.Body.String())
	}
	if operation.ExpiresAt.Sub(operation.CreatedAt) > time.Minute || strings.Contains(queued.Body.String(), "APD_CLIENT") {
		t.Fatal("unsafe operation projection")
	}
	if err = durable.(storage.RunMonitorStore).SaveCollectorRunReadiness("proj-a", "appd-collector", []string{"appdynamics"}, []string{req.Config.Endpoint + "#" + publicKey}, now); err != nil {
		t.Fatal(err)
	}
	legacyPoll := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/appd-collector/inspections", "collector-token", nil)
	if legacyPoll.Code != 200 || strings.TrimSpace(legacyPoll.Body.String()) != "[]" {
		t.Fatalf("typed operation granted to downgraded collector: %d %s", legacyPoll.Code, legacyPoll.Body.String())
	}
	if err = durable.(storage.RunMonitorStore).SaveCollectorRunReadiness("proj-a", "appd-collector", []string{"appdynamics", collectoragent.AppDynamicsBatchContract}, []string{req.Config.Endpoint + "#" + publicKey}, now); err != nil {
		t.Fatal(err)
	}
	polled := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/appd-collector/inspections", "collector-token", nil)
	var envelopes []collectoragent.SignedInspectionJob
	if polled.Code != 200 || json.Unmarshal(polled.Body.Bytes(), &envelopes) != nil || len(envelopes) != 1 {
		t.Fatalf("poll: %d %s", polled.Code, polled.Body.String())
	}
	job, err := collectoragent.VerifyInspectionJob(envelopes[0], signingPublic, "appd-collector", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(envelopes[0].Payload, "resolved-sensitive-value") || strings.Contains(envelopes[0].Payload, "run_id") {
		t.Fatal("inspection has leaked secret or fabricated run")
	}
	credentialJob := collectoragent.RunJob{ID: "inspection:" + job.ID, ProjectID: job.ProjectID, CollectorID: job.CollectorID, SourceID: job.ConnectionID, SourceRevision: job.ConnectionRevision, Pack: "appdynamics-inspection", Destination: job.Config.Endpoint, LeaseExpiresAt: job.LeaseExpiresAt, Credentials: job.Credentials}
	credentials, err := collectoragent.OpenCredentials(credentialJob, encryptionKey.Bytes(), time.Now())
	if err != nil || credentials.AppDynamics.ClientSecret != "resolved-sensitive-value" {
		t.Fatalf("sealed credential %v", err)
	}
	// Changing a profile while inspection is in flight cannot verify the new revision.
	req.Revision = conn.Revision
	req.Name = "new revision"
	req.ClientIDSecret = nil
	req.ClientSecretSecret = nil
	if got := timeslotRequest(t, server, http.MethodPut, path, req); got.Code != 200 {
		t.Fatalf("update %d %s", got.Code, got.Body.String())
	}
	result := collectoragent.InspectionResult{Job: envelopes[0], Capabilities: appdynamics.Capabilities{Metrics: true, MetricAPI: "enabled"}}
	resultPath := "/api/v1/monitoring/collectors/appd-collector/inspections/" + job.ID + "/result"
	for i := 0; i < 2; i++ {
		if got := collectorRequest(t, server, http.MethodPost, resultPath, "collector-token", result); got.Code != 200 {
			t.Fatalf("result %d %s", got.Code, got.Body.String())
		}
	}
	profile, err := durable.(storage.AppDynamicsConnectionStore).GetAppDynamicsConnection(conn.ID)
	if err != nil || profile.VerifiedRevision != 0 || profile.Revision != 2 {
		t.Fatalf("stale verification published %+v %v", profile, err)
	}
	got := timeslotRequest(t, server, http.MethodGet, path+"/operations/"+operation.ID, nil)
	if got.Code != 200 || strings.Contains(got.Body.String(), "APD_CLIENT") || !strings.Contains(got.Body.String(), `"status":"complete"`) {
		t.Fatalf("projection %d %s", got.Code, got.Body.String())
	}
	if got := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/appd-collector/inspections", "wrong", nil); got.Code != 401 {
		t.Fatalf("bad token %d", got.Code)
	}
}
func TestAppDynamicsCredentialErrorsDoNotDiscloseNames(t *testing.T) {
	_, err := resolveAppDynamicsCredentials(context.Background(), engine.SecretResolverFunc(func(string) (string, bool) { return "", false }), &storage.AppDynamicsConnectionSnapshot{ClientSecretSecretName: "SECRET_PRIVATE_NAME"})
	if err == nil || strings.Contains(err.Error(), "SECRET_PRIVATE_NAME") {
		t.Fatal("credential reference disclosed")
	}
}

func TestAppDynamicsConnectionIdentityChangesAndTrustProjection(t *testing.T) {
	original := &storage.AppDynamicsConnection{ID: "c", ProjectID: "p", Name: "name", Config: appdynamics.Config{Endpoint: "https://old.example.com", ContextPath: "/controller", AccountName: "account", AuthType: "oauth", CAPEM: "private-ca-material", ApprovedCIDRs: []string{"10.0.0.0/8"}, EventsEndpoint: "https://events.example.com", EventsAccountName: "events"}, ClientIDSecretName: "original-client", ClientSecretSecretName: "original-secret", EventsAPIKeySecretName: "original-events-key"}
	public := appDynamicsConnectionResponse(original)
	body, _ := json.Marshal(public)
	if strings.Contains(string(body), "private-ca-material") || strings.Contains(string(body), "10.0.0.0/8") || !public.HasCustomCA {
		t.Fatal("trust configuration disclosed")
	}
	same := *original
	empty := ""
	applyAppDynamicsConnection(&same, AppDynamicsConnectionRequest{Name: "new name", Config: public.Config, ClientIDSecret: &empty, ClientSecretSecret: &empty})
	if same.ClientSecretSecretName != "original-secret" || same.Config.CAPEM != "private-ca-material" || len(same.Config.ApprovedCIDRs) != 1 {
		t.Fatal("blank same-identity edit did not preserve protected configuration")
	}
	changed := *original
	changedConfig := public.Config
	changedConfig.Endpoint = "https://new.example.com"
	changedConfig.EventsAccountName = "different"
	applyAppDynamicsConnection(&changed, AppDynamicsConnectionRequest{Name: "new origin", Config: changedConfig, ClientIDSecret: &empty, ClientSecretSecret: &empty})
	if changed.ClientIDSecretName != "" || changed.ClientSecretSecretName != "" || changed.EventsAPIKeySecretName != "" || changed.Config.CAPEM != "" || len(changed.Config.ApprovedCIDRs) != 0 {
		t.Fatal("identity change reused prior credentials or private trust")
	}
	server, _ := newTimeslotServer(t)
	req := appDynamicsRequestFixture()
	req.Enabled = true
	got := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitoring/appdynamics-connections", req)
	if got.Code != 409 {
		t.Fatalf("unverified enable accepted %d", got.Code)
	}
}
func TestAppDynamicsConnectionViewerProjectionAndPrivateTrustAccess(t *testing.T) {
	server, durable := newTimeslotServer(t)
	req := appDynamicsRequestFixture()
	base := "/api/v1/projects/proj-a/monitoring/appdynamics-connections"
	created := timeslotRequest(t, server, http.MethodPost, base, req)
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	enableTrustedHeaderAuth(t, server)
	request := func(method, role string, body any) *httptest.ResponseRecorder {
		r := scopedLibraryRequest(t, method, base, body)
		r.Header.Set("X-Auth-Request-User", "appd-"+role)
		r.Header.Set("X-APEXLoad-Roles", role)
		r.Header.Set("X-APEXLoad-Project-Roles", "proj-a="+role)
		w := httptest.NewRecorder()
		server.mux.ServeHTTP(w, r)
		return w
	}
	viewer := request(http.MethodGet, RoleViewer, nil)
	if viewer.Code != 200 || !strings.Contains(viewer.Body.String(), `"status":"disabled"`) || strings.Contains(viewer.Body.String(), `"config"`) || strings.Contains(viewer.Body.String(), "apm.example.com") {
		t.Fatalf("viewer projection %d %s", viewer.Code, viewer.Body.String())
	}
	req.Config.ApprovedCIDRs = []string{"10.0.0.0/8"}
	denied := request(http.MethodPost, RoleProjectAdmin, req)
	if denied.Code != 403 {
		t.Fatalf("project admin broadened private egress %d %s", denied.Code, denied.Body.String())
	}
	approved := request(http.MethodPost, RoleSystemAdmin, req)
	if approved.Code != 201 || strings.Contains(approved.Body.String(), "10.0.0.0/8") {
		t.Fatalf("system admin approval %d %s", approved.Code, approved.Body.String())
	}
	connections, err := durable.(storage.AppDynamicsConnectionStore).ListAppDynamicsConnections("proj-a")
	if err != nil || len(connections) != 2 {
		t.Fatalf("durable approvals %v %v", connections, err)
	}
}

func TestAppDynamicsPrivateTrustCannotFollowChangedAnalyticsOrigin(t *testing.T) {
	server, _ := newTimeslotServer(t)
	enableTrustedHeaderAuth(t, server)
	connection := &storage.AppDynamicsConnection{Config: appdynamics.Config{Endpoint: "https://controller.example.com", AccountName: "account", EventsEndpoint: "https://events.old.example.com", EventsAccountName: "analytics", ApprovedCIDRs: []string{"10.0.0.0/8"}}}
	config := connection.Config
	config.ApprovedCIDRs = nil
	config.EventsEndpoint = "https://events.new.example.com"
	for _, role := range []string{RoleProjectAdmin, RoleSystemAdmin} {
		request := scopedLibraryRequest(t, http.MethodPut, "/api/v1/projects/proj-a/monitoring/appdynamics-connections/c", nil)
		request.Header.Set("X-Auth-Request-User", "trust-"+role)
		request.Header.Set("X-APEXLoad-Roles", role)
		// Exercise exactly the role context used by the API's trust authorization.
		user := &AuthenticatedUser{GlobalRoles: map[string]struct{}{role: {}}}
		request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, user))
		allowed := server.appDynamicsTrustAllowed(request, AppDynamicsConnectionRequest{Config: config}, connection)
		if allowed != (role == RoleSystemAdmin) {
			t.Fatalf("preserved private CIDRs follow a new Analytics origin for %s: %v", role, allowed)
		}
	}
}

func TestAppDynamicsCIGoalsPreserveTerminalRules(t *testing.T) {
	goals := ciRunGoalsRequest{MonitorRules: []analysis.MonitorRule{{SourceID: "apm", Metric: "calls", Unit: "count", Statistic: "sum", Comparator: "lte", Threshold: 10}}}
	if !goals.evaluated() || goals.loadGoalsEvaluated() {
		t.Fatal("terminal monitoring criterion entered load control")
	}
	configuration := goals.goalConfig()
	if configuration == nil || len(configuration.MonitorRules) != 1 {
		t.Fatal("CI monitor criterion was discarded")
	}
	configuration.MonitorRules[0].Metric = "edited"
	if goals.MonitorRules[0].Metric != "calls" {
		t.Fatal("CI goal projection aliases monitoring rules")
	}
	if err := validateCIGoals(goals); err != nil {
		t.Fatal(err)
	}
	goals.MonitorRules[0].Unit = ""
	if err := validateCIGoals(goals); err == nil {
		t.Fatal("invalid monitoring criterion accepted")
	}
}
