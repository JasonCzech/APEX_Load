package api

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func prometheusRequestFixture() PrometheusConnectionRequest {
	username, password := "PROM_USERNAME", "PROM_PASSWORD"
	return PrometheusConnectionRequest{Name: "Target metrics", CollectionMode: "collector", CollectorAgentID: "prom-collector", Config: prometheus.Config{Endpoint: "https://prom.example.com", AuthType: "basic"}, UsernameSecret: &username, PasswordSecret: &password}
}

// Catalog and generated-contract presence alone cannot prove runtime wiring.
// Check the actual server mux so every connection/inspection method is reachable.
func TestPrometheusConnectionRoutesAreRegistered(t *testing.T) {
	server, _ := newTimeslotServer(t)
	values := strings.NewReplacer("{id}", "proj-a", "{connection_id}", "connection", "{operation_id}", "operation")
	for _, route := range prometheusAPIRouteCatalog.Routes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			request := httptest.NewRequest(route.Method, values.Replace(route.Path), nil)
			_, pattern := server.mux.Handler(request)
			if want := route.Method + " " + route.Path; pattern != want {
				t.Fatalf("runtime route pattern = %q, want %q", pattern, want)
			}
		})
	}
}

func TestPrometheusConnectionCRUDWriteOnlyReferencesAndRevision(t *testing.T) {
	server, durable := newTimeslotServer(t)
	base := "/api/v1/projects/proj-a/monitoring/prometheus-connections"
	req := prometheusRequestFixture()
	created := timeslotRequest(t, server, http.MethodPost, base, req)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "PROM_") {
		t.Fatal("secret references disclosed")
	}
	var connection PrometheusConnectionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &connection); err != nil {
		t.Fatal(err)
	}
	if !connection.HasUsername || !connection.HasPassword || connection.Revision != 1 {
		t.Fatalf("projection: %+v", connection)
	}
	path := base + "/" + connection.ID
	if got := timeslotRequest(t, server, http.MethodGet, strings.Replace(path, "proj-a", "other", 1), nil); got.Code != 404 {
		t.Fatalf("cross-project get: %d", got.Code)
	}
	req.Revision = 1
	req.Name = "Updated"
	req.UsernameSecret = nil
	req.PasswordSecret = nil
	updated := timeslotRequest(t, server, http.MethodPut, path, req)
	if updated.Code != 200 {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}
	saved, err := durable.(storage.PrometheusConnectionStore).GetPrometheusConnection(connection.ID)
	if err != nil || saved.PasswordSecretName != "PROM_PASSWORD" || saved.Revision != 2 {
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
func TestPrometheusCredentialErrorsDoNotDiscloseNames(t *testing.T) {
	_, err := resolvePrometheusCredentials(context.Background(), engine.SecretResolverFunc(func(string) (string, bool) { return "", false }), &storage.PrometheusConnectionSnapshot{PasswordSecretName: "SECRET_PRIVATE_NAME"})
	if err == nil || strings.Contains(err.Error(), "SECRET_PRIVATE_NAME") {
		t.Fatal("credential reference disclosed")
	}
}

func TestPrometheusDirectSelectedInspectionHonorsOperationDeadline(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expiry        time.Duration
		parentTimeout time.Duration
		wantRequests  int
	}{{"twenty_queries_fit_one_minute", time.Minute, time.Minute, 1}, {"persisted_expiry_is_shorter", 30 * time.Second, time.Minute, 0}, {"parent_deadline_is_shorter", time.Minute, 30 * time.Second, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			server, durable := newTimeslotServer(t)
			if server.appDynamicsCancel != nil {
				server.appDynamicsCancel()
			}
			connections := durable.(storage.PrometheusConnectionStore)
			connection := &storage.PrometheusConnection{ID: "inspection-deadline", ProjectID: "proj-a", Name: "deadline", CollectionMode: "direct", Config: prometheus.Config{Endpoint: "https://prom.example"}}
			if err := connections.CreatePrometheusConnection(connection); err != nil {
				t.Fatal(err)
			}
			selection := prometheus.Selection{ProjectID: connection.ProjectID}
			for index := range 20 {
				id := fmt.Sprintf("query_%02d", index)
				selection.Queries = append(selection.Queries, prometheus.Query{ID: id, Name: id, Expression: "vector(1)"})
			}
			payload, err := json.Marshal(prometheusInspectionWork{Connection: connection.Snapshot(), Selection: &selection})
			if err != nil {
				t.Fatal(err)
			}
			store := durable.(storage.PrometheusInspectionStore)
			now := time.Now().UTC()
			op := &storage.PrometheusInspection{ID: "inspection-deadline", ProjectID: connection.ProjectID, ConnectionID: connection.ID, ConnectionRevision: connection.Revision, Kind: "test", PayloadJSON: string(payload), CreatedAt: now, ExpiresAt: now.Add(tc.expiry)}
			if err := store.CreatePrometheusInspection(op); err != nil {
				t.Fatal(err)
			}
			if claimed, err := store.ClaimPrometheusInspection(op.ID, "owner", now, op.ExpiresAt); err != nil || !claimed {
				t.Fatal(claimed, err)
			}
			requests := 0
			server.prometheusClient = func(config prometheus.Config, credentials prometheus.Credentials) (*prometheus.Client, error) {
				if config.RequestsPerMinute != 30 {
					t.Fatal("inspection must use the actual allocated executor quota", config.RequestsPerMinute)
				}
				return prometheus.NewClientWithHTTPClient(config, credentials, &http.Client{Transport: prometheusAPIRoundTrip(func(*http.Request) (*http.Response, error) {
					requests++
					// Stop at the first probe: the regression concerns admission of
					// the complete 48-second plan, rather than actual quota sleeping.
					return nil, errors.New("fixture stops after the first authorized probe")
				})})
			}
			parent, cancel := context.WithTimeout(context.Background(), tc.parentTimeout)
			defer cancel()
			server.executeDirectPrometheusInspection(parent, store, op, "owner")
			if requests != tc.wantRequests {
				t.Fatalf("authorized probe count = %d, want %d", requests, tc.wantRequests)
			}
		})
	}
}

func TestPrometheusCredentialsNeverUseDevelopmentPlaceholdersOrDiscloseReferences(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "1")
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, providerError := range []error{secretstore.ErrNotFound, errors.New("PRIVATE_PROM_REFERENCE credential-do-not-log provider failure")} {
		runner := &TestRunner{projectSecretStore: &devSecretStore{err: providerError}}
		credentials, err := resolvePrometheusCredentials(context.Background(), runner.prometheusSecretResolverForProject("private-project"), &storage.PrometheusConnectionSnapshot{TokenSecretName: "PRIVATE_PROM_REFERENCE"})
		if err == nil || credentials.Token != "" || strings.Contains(err.Error(), "PRIVATE_PROM_REFERENCE") || strings.Contains(err.Error(), "credential-do-not-log") {
			t.Fatal("missing or failed provider produced unsafe credentials", err)
		}
	}
	if strings.Contains(logs.String(), "PRIVATE_PROM_REFERENCE") || strings.Contains(logs.String(), "credential-do-not-log") {
		t.Fatal("monitoring credential resolution disclosed protected data")
	}
	runner := &TestRunner{projectSecretStore: &devSecretStore{value: "configured-token"}}
	credentials, err := resolvePrometheusCredentials(context.Background(), runner.prometheusSecretResolverForProject("private-project"), &storage.PrometheusConnectionSnapshot{TokenSecretName: "PRIVATE_PROM_REFERENCE"})
	if err != nil || credentials.Token != "configured-token" {
		t.Fatal("configured credential was not resolved", err)
	}
}

func TestPrometheusPreparationRechecksRevisionAndReadinessBeforeCredentialUse(t *testing.T) {
	for _, mode := range []string{"disabled", "unverified", "stale_revision", "cross_project"} {
		t.Run(mode, func(t *testing.T) {
			server, durable := newTimeslotServer(t)
			store := durable.(storage.PrometheusConnectionStore)
			connection := &storage.PrometheusConnection{ID: "guard", ProjectID: "proj-a", Name: "guard", Enabled: true, CollectionMode: "direct", Config: prometheus.Config{Endpoint: "https://prom.example.com", AuthType: "bearer"}, TokenSecretName: "DO_NOT_RESOLVE"}
			if err := store.CreatePrometheusConnection(connection); err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpdatePrometheusConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", prometheus.Capabilities{Metrics: true}, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			connection, _ = store.GetPrometheusConnection(connection.ID)
			snapshot := connection.Snapshot()
			if mode == "cross_project" {
				snapshot.ProjectID = "other-project"
			} else {
				switch mode {
				case "disabled":
					connection.Enabled = false
				case "unverified":
					connection.Config.TimeoutSeconds = 11
				case "stale_revision":
					connection.Name = "changed display metadata"
				}
				if ok, err := store.UpdatePrometheusConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
					t.Fatal(ok, err)
				}
				if mode != "stale_revision" {
					snapshot = connection.Snapshot()
				}
			}
			calls := 0
			server.prometheusClient = func(prometheus.Config, prometheus.Credentials) (*prometheus.Client, error) {
				calls++
				return nil, nil
			}
			source := storage.MonitorSource{ID: "s", ProjectID: "proj-a", ConnectionID: connection.ID, ExecutorMode: "direct", Prometheus: &prometheus.Selection{ProjectID: "proj-a", Queries: []prometheus.Query{{ID: "up", Name: "up", Expression: "up", Unit: "ratio"}}}}
			err := server.verifyPreparedPrometheus(context.Background(), source, snapshot, nil)
			if err == nil || !strings.Contains(err.Error(), "connection changed or is no longer ready") || calls != 0 {
				t.Fatal("stale preparation reached credential use or transport", err, calls)
			}
		})
	}
}

func TestPrometheusInspectionProjectionPreservesSafeStatusAndTruncation(t *testing.T) {
	projection := sanitizePrometheusInspectionProjection(prometheusInspectionProjection{Capabilities: prometheus.Capabilities{Metrics: true, BackendProfile: "prometheus", Status: map[string]string{"metrics": "ok", "freshness": "partial", "targets": "permission_denied", "alerts": "authentication_failed", "exemplars": "throttled", "password": "secret"}}, Entities: []monitorprovider.Entity{{Type: "truncation", Name: "More results exist", ParentID: "private-parent", EntityIDs: map[string]string{"secret": "private"}}}}, "project")
	if !projection.Truncated || projection.Capabilities.Status["metrics"] != "ok" || projection.Capabilities.Status["freshness"] != "partial" || projection.Capabilities.Status["targets"] != "permission_denied" || projection.Capabilities.Status["alerts"] != "authentication_failed" || projection.Capabilities.Status["exemplars"] != "throttled" || projection.Capabilities.Status["password"] != "" || projection.Entities[0].ParentID != "" || projection.Entities[0].EntityIDs != nil {
		t.Fatalf("safe capability or truncation projection %+v", projection)
	}
}

func TestPrometheusInspectionPreviewPreservesBoundedSafeWarningsAndInfos(t *testing.T) {
	warnings := []string{"query returned partial data password=fixture-private-value", "authorization: Bearer fixture-access-token", "line\nnext"}
	infos := []string{"mixed samples were omitted", strings.Repeat("é", 200)}
	for index := range 30 {
		warnings = append(warnings, fmt.Sprintf("warning %d", index))
		infos = append(infos, fmt.Sprintf("info %d", index))
	}
	projection := sanitizePrometheusInspectionProjection(prometheusInspectionProjection{Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "partial", Warnings: warnings, Infos: infos}}}, "project")
	coverage := projection.Coverage["cpu"]
	if coverage.Status != "partial" || len(coverage.Warnings) != 20 || len(coverage.Infos) != 20 || !strings.Contains(coverage.Warnings[0], "query returned partial data") || coverage.Infos[0] != "mixed samples were omitted" {
		t.Fatalf("preview annotations disappeared: %+v", coverage)
	}
	for _, value := range append(coverage.Warnings, coverage.Infos...) {
		if len(value) > 256 || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") || strings.Contains(value, "fixture-private-value") || strings.Contains(value, "fixture-access-token") {
			t.Fatalf("unsafe preview annotation %q", value)
		}
	}
}

func TestPrometheusInspectionProjectionRetainsSafeDiscoveryIdentityAndMetadata(t *testing.T) {
	projection := sanitizePrometheusInspectionProjection(prometheusInspectionProjection{Entities: []monitorprovider.Entity{
		{Type: "series", Name: "requests_total", EntityIDs: map[string]string{"job": "checkout", "instance": "192.0.2.10:9100", "token": "private-secret", "url": "https://private", "service": "https://private", "template": "{{secret.reference}}", "payload": "password=hidden"}},
		{Type: "metric_metadata", Name: "requests_total", EntityIDs: map[string]string{"type": "counter", "unit": "requests", "help": "unrestricted provider narrative"}},
		{Type: "unknown", Name: "unknown", EntityIDs: map[string]string{"job": "checkout"}},
	}}, "project")
	if !projection.Truncated || len(projection.Entities[0].EntityIDs) != 2 || projection.Entities[0].EntityIDs["job"] != "checkout" || projection.Entities[0].EntityIDs["instance"] != "192.0.2.10:9100" || len(projection.Entities[1].EntityIDs) != 2 || projection.Entities[1].EntityIDs["type"] != "counter" || projection.Entities[1].EntityIDs["unit"] != "requests" || projection.Entities[2].EntityIDs != nil {
		t.Fatalf("safe discovery projection %+v", projection)
	}
	payload, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"private-secret", "https://private", "secret.reference", "password=hidden", "unrestricted provider narrative"} {
		if strings.Contains(string(payload), excluded) {
			t.Fatalf("discovery disclosed excluded value %q", excluded)
		}
	}
}

func TestPrometheusConnectionViewerProjectionAndPrivateTrustAccess(t *testing.T) {
	server, durable := newTimeslotServer(t)
	req := prometheusRequestFixture()
	base := "/api/v1/projects/proj-a/monitoring/prometheus-connections"
	created := timeslotRequest(t, server, http.MethodPost, base, req)
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	enableTrustedHeaderAuth(t, server)
	request := func(method, role string, body any) *httptest.ResponseRecorder {
		r := scopedLibraryRequest(t, method, base, body)
		r.Header.Set("X-Auth-Request-User", "prom-"+role)
		r.Header.Set("X-APEXLoad-Roles", role)
		r.Header.Set("X-APEXLoad-Project-Roles", "proj-a="+role)
		w := httptest.NewRecorder()
		server.mux.ServeHTTP(w, r)
		return w
	}
	viewer := request(http.MethodGet, RoleViewer, nil)
	if viewer.Code != 200 || !strings.Contains(viewer.Body.String(), `"status":"disabled"`) || strings.Contains(viewer.Body.String(), `"config"`) || strings.Contains(viewer.Body.String(), "prom.example.com") {
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
	connections, err := durable.(storage.PrometheusConnectionStore).ListPrometheusConnections("proj-a")
	if err != nil || len(connections) != 2 {
		t.Fatalf("durable approvals %v %v", connections, err)
	}
}

// Direct managed/workload identity authenticates as the controller itself, so a
// project administrator must not be able to select or retarget that principal.
func TestPrometheusAmbientAzureIdentityRequiresSystemAdministrator(t *testing.T) {
	server, _ := newTimeslotServer(t)
	enableTrustedHeaderAuth(t, server)
	base := "/api/v1/projects/proj-a/monitoring/prometheus-connections"
	send := func(method, path, role string, body any) *httptest.ResponseRecorder {
		r := scopedLibraryRequest(t, method, path, body)
		r.Header.Set("X-Auth-Request-User", "prom-"+role)
		r.Header.Set("X-APEXLoad-Roles", role)
		r.Header.Set("X-APEXLoad-Project-Roles", "proj-a="+role)
		w := httptest.NewRecorder()
		server.mux.ServeHTTP(w, r)
		return w
	}
	created := func(w *httptest.ResponseRecorder) (string, int64) {
		t.Helper()
		var body struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision"`
		}
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.ID == "" {
			t.Fatalf("create %d %s", w.Code, w.Body.String())
		}
		return body.ID, body.Revision
	}
	azure := func(mode string) PrometheusConnectionRequest {
		cfg := prometheus.Config{Endpoint: "https://workspace-abcd.eastus.prometheus.monitor.azure.com", BackendProfile: prometheus.BackendAzureManaged, AuthType: prometheus.AuthAzure, AzureCredentialMode: mode, ClientID: "11111111-1111-1111-1111-111111111111"}
		if mode != "managed_identity" {
			cfg.AzureTenantID = "22222222-2222-2222-2222-222222222222"
		}
		return PrometheusConnectionRequest{Name: "Azure workspace", CollectionMode: "direct", Config: cfg}
	}
	ambientDenied := func(w *httptest.ResponseRecorder, what string) {
		t.Helper()
		if w.Code != 403 || !strings.Contains(w.Body.String(), "controller's Azure identity") {
			t.Fatalf("%s: %d %s", what, w.Code, w.Body.String())
		}
	}

	for _, mode := range []string{"managed_identity", "workload_identity"} {
		ambientDenied(send(http.MethodPost, base, RoleProjectAdmin, azure(mode)), "project admin created direct "+mode)
	}
	id, revision := created(send(http.MethodPost, base, RoleSystemAdmin, azure("managed_identity")))

	rename := azure("managed_identity")
	rename.Name, rename.Revision = "Renamed workspace", revision
	if w := send(http.MethodPut, base+"/"+id, RoleProjectAdmin, rename); w.Code != 200 {
		t.Fatalf("project admin rename with unchanged identity %d %s", w.Code, w.Body.String())
	}
	revision++
	retarget := azure("managed_identity")
	retarget.Config.ClientID, retarget.Revision = "33333333-3333-3333-3333-333333333333", revision
	ambientDenied(send(http.MethodPut, base+"/"+id, RoleProjectAdmin, retarget), "project admin changed client ID")
	remode := azure("workload_identity")
	remode.Revision = revision
	ambientDenied(send(http.MethodPut, base+"/"+id, RoleProjectAdmin, remode), "project admin changed credential mode and tenant")

	collector := azure("managed_identity")
	collector.CollectionMode, collector.CollectorAgentID = "collector", "prom-collector"
	collectorID, collectorRevision := created(send(http.MethodPost, base, RoleProjectAdmin, collector))
	direct := azure("managed_identity")
	direct.Revision = collectorRevision
	ambientDenied(send(http.MethodPut, base+"/"+collectorID, RoleProjectAdmin, direct), "project admin switched collector identity to direct")

	secret := "AZURE_PROMETHEUS_SECRET"
	principal := azure("service_principal")
	principal.ClientSecretSecret = &secret
	created(send(http.MethodPost, base, RoleProjectAdmin, principal))
}

func TestPrometheusConnectionIdentityChangesAndProtectedTrustProjection(t *testing.T) {
	original := &storage.PrometheusConnection{ID: "c", ProjectID: "p", Name: "name", Config: prometheus.Config{Endpoint: "https://old.example.com", AuthType: "basic", CAPEM: "private-ca-material", ApprovedCIDRs: []string{"10.0.0.0/8"}}, UsernameSecretName: "original-user", PasswordSecretName: "original-password", ClientCertificateSecretName: "original-cert", ClientKeySecretName: "original-key"}
	public := prometheusConnectionResponse(original)
	body, _ := json.Marshal(public)
	if strings.Contains(string(body), "original-") || strings.Contains(string(body), "private-ca-material") || strings.Contains(string(body), "10.0.0.0/8") || !public.HasCustomCA || !public.HasClientKey {
		t.Fatal("protected connection fields disclosed")
	}
	same := *original
	empty := ""
	applyPrometheusConnection(&same, PrometheusConnectionRequest{Name: "display", Config: public.Config, UsernameSecret: &empty, PasswordSecret: &empty})
	if same.PasswordSecretName != "original-password" || same.Config.CAPEM != "private-ca-material" || len(same.Config.ApprovedCIDRs) != 1 {
		t.Fatal("blank credentials lost")
	}
	changed := *original
	cfg := public.Config
	cfg.Endpoint = "https://new.example.com"
	applyPrometheusConnection(&changed, PrometheusConnectionRequest{Name: "new", Config: cfg})
	if changed.UsernameSecretName != "" || changed.PasswordSecretName != "" || changed.ClientCertificateSecretName != "" || changed.ClientKeySecretName != "" || changed.Config.CAPEM != "" || len(changed.Config.ApprovedCIDRs) != 0 {
		t.Fatal("credentials followed changed origin")
	}
	server, _ := newTimeslotServer(t)
	req := prometheusRequestFixture()
	req.Enabled = true
	response := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitoring/prometheus-connections", req)
	if response.Code != 409 {
		t.Fatal("unverified enabled", response.Code)
	}
}
func TestPrometheusCollectorInspectionEnrollmentSealingRevisionAndIdempotency(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(key.Seed()))
	server, durable := newTimeslotServer(t)
	server.runner.projectSecretStore = &devSecretStore{value: "resolved-private-value"}
	now := time.Now().UTC()
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	public := base64.StdEncoding.EncodeToString(decrypt.PublicKey().Bytes())
	if err := durable.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "prom-collector", ProjectID: "proj-a", Name: "collector", Health: "healthy", LastSeenAt: &now, TokenHash: hashAPIKey("collector-token"), AllowedMetricPacks: []string{"prometheus-query"}}); err != nil {
		t.Fatal(err)
	}
	req := prometheusRequestFixture()
	base := "/api/v1/projects/proj-a/monitoring/prometheus-connections"
	created := timeslotRequest(t, server, http.MethodPost, base, req)
	var connection PrometheusConnectionResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &connection) != nil {
		t.Fatal(created.Code, created.Body.String())
	}
	path := base + "/" + connection.ID
	if got := timeslotRequest(t, server, http.MethodPost, path+"/test", nil); got.Code != 409 {
		t.Fatal("unenrolled target accepted", got.Code)
	}
	if err := durable.(storage.RunMonitorStore).SaveCollectorRunReadiness("proj-a", "prom-collector", []string{"prometheus-query", collectoragent.PrometheusBatchContract}, []string{req.Config.Endpoint + "#" + public}, now); err != nil {
		t.Fatal(err)
	}
	queued := timeslotRequest(t, server, http.MethodPost, path+"/test", nil)
	var op PrometheusOperationResponse
	if queued.Code != 202 || json.Unmarshal(queued.Body.Bytes(), &op) != nil {
		t.Fatal(queued.Code, queued.Body.String())
	}
	polled := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/prom-collector/inspections", "collector-token", nil)
	var envelopes []collectoragent.SignedInspectionJob
	if polled.Code != 200 || json.Unmarshal(polled.Body.Bytes(), &envelopes) != nil || len(envelopes) != 1 {
		t.Fatal(polled.Code, polled.Body.String())
	}
	job, err := collectoragent.VerifyInspectionJob(envelopes[0], pub, "prom-collector", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(envelopes[0].Payload, "resolved-private-value") || strings.Contains(envelopes[0].Payload, "PROM_PASSWORD") {
		t.Fatal("credential leaked")
	}
	credJob := collectoragent.RunJob{ID: "inspection:" + job.ID, ProjectID: job.ProjectID, CollectorID: job.CollectorID, SourceID: job.ConnectionID, SourceRevision: job.ConnectionRevision, Pack: "prometheus-inspection", Destination: job.Prometheus.Config.Endpoint, LeaseExpiresAt: job.LeaseExpiresAt, Credentials: job.Credentials}
	credentials, err := collectoragent.OpenCredentials(credJob, decrypt.Bytes(), time.Now())
	if err != nil || credentials.Prometheus.Password != "resolved-private-value" {
		t.Fatal(credentials, err)
	}
	req.Revision = 1
	req.Name = "revised"
	req.UsernameSecret, req.PasswordSecret = nil, nil
	if got := timeslotRequest(t, server, http.MethodPut, path, req); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	result := collectoragent.InspectionResult{Job: envelopes[0], PrometheusCapabilities: &prometheus.Capabilities{Metrics: true, BackendProfile: "prometheus", Status: map[string]string{"discovery": "forbidden"}}}
	resultPath := "/api/v1/monitoring/collectors/prom-collector/inspections/" + job.ID + "/result"
	for i := 0; i < 2; i++ {
		if got := collectorRequest(t, server, http.MethodPost, resultPath, "collector-token", result); got.Code != 200 {
			t.Fatal(got.Code, got.Body.String())
		}
	}
	profile, err := durable.(storage.PrometheusConnectionStore).GetPrometheusConnection(connection.ID)
	if err != nil || profile.VerifiedRevision != 0 || profile.Revision != 2 {
		t.Fatal("late verification blessed edited profile", profile, err)
	}
	if got := timeslotRequest(t, server, http.MethodGet, path+"/operations/"+op.ID, nil); got.Code != 200 || strings.Contains(got.Body.String(), "PROM_PASSWORD") || !strings.Contains(got.Body.String(), `"status":"complete"`) {
		t.Fatal(got.Code, got.Body.String())
	}
}
