package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

type stubSystemSettingsService struct {
	getValue     SystemSettings
	getCalls     int
	updateCalls  int
	requested    SystemSettings
	rawBody      []byte
	expectedRev  string
	updateResult systemSettingsUpdateResult
	updateErr    error
}

func withCurrentSystemSettingsIfMatch(req *http.Request, server *Server) *http.Request {
	req.Header.Set("If-Match", systemSettingsETag(systemSettingsRevision(server.runner.GetSystemSettings())))
	return req
}

func (s *stubSystemSettingsService) Get() SystemSettings {
	s.getCalls++
	return cloneSystemSettings(s.getValue)
}

func (s *stubSystemSettingsService) Update(requested SystemSettings, rawBody []byte, expectedRevision string, _ systemSettingsBeforeCommit) (systemSettingsUpdateResult, error) {
	s.updateCalls++
	s.requested = cloneSystemSettings(requested)
	s.rawBody = append([]byte(nil), rawBody...)
	s.expectedRev = expectedRevision
	return s.updateResult, s.updateErr
}

func TestSystemSettingsHandlersDelegateToDomainService(t *testing.T) {
	returned := defaultSystemSettings(13)
	service := &stubSystemSettingsService{
		getValue: returned,
		updateResult: systemSettingsUpdateResult{
			systemSettingsUpdatePlan: systemSettingsUpdatePlan{Previous: defaultSystemSettings(10)},
			Updated:                  returned,
		},
	}
	server := &Server{settingsService: service}

	getResponse := httptest.NewRecorder()
	server.handleGetSystemSettings(getResponse, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	if getResponse.Code != http.StatusOK || service.getCalls != 1 {
		t.Fatalf("GET response = %d, calls = %d; want 200 and one service call", getResponse.Code, service.getCalls)
	}
	var got SystemSettings
	if err := json.Unmarshal(getResponse.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.General.MaxConcurrentTests != 13 {
		t.Fatalf("GET max concurrent = %d, want delegated value 13", got.General.MaxConcurrentTests)
	}
	var getDocument map[string]any
	if err := json.Unmarshal(getResponse.Body.Bytes(), &getDocument); err != nil {
		t.Fatalf("decode GET document: %v", err)
	}
	if gotRevision, _ := getDocument["revision"].(string); gotRevision != systemSettingsRevision(returned) {
		t.Fatalf("GET revision = %q, want %q", gotRevision, systemSettingsRevision(returned))
	}
	if gotETag := getResponse.Header().Get("ETag"); gotETag != systemSettingsETag(systemSettingsRevision(returned)) {
		t.Fatalf("GET ETag = %q, want %q", gotETag, systemSettingsETag(systemSettingsRevision(returned)))
	}
	if _, exposed := getDocument["_revision"]; exposed {
		t.Fatal("GET response exposed the internal persisted revision field")
	}

	requestBody := []byte(`{"general":{"max_concurrent_tests":17}}`)
	putResponse := httptest.NewRecorder()
	putRequest := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(requestBody))
	putRequest.Header.Set("If-Match", getResponse.Header().Get("ETag"))
	server.handleUpdateSystemSettings(putResponse, putRequest)
	if putResponse.Code != http.StatusOK || service.updateCalls != 1 {
		t.Fatalf("PUT response = %d, calls = %d, body=%s", putResponse.Code, service.updateCalls, putResponse.Body.String())
	}
	if service.requested.General.MaxConcurrentTests != 17 || !bytes.Equal(service.rawBody, requestBody) ||
		service.expectedRev != systemSettingsRevision(returned) {
		t.Fatalf("delegated update = max:%d raw:%q", service.requested.General.MaxConcurrentTests, service.rawBody)
	}
	if err := json.Unmarshal(putResponse.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if got.General.MaxConcurrentTests != 13 {
		t.Fatalf("PUT response max concurrent = %d, want service result 13", got.General.MaxConcurrentTests)
	}
}

func TestSystemSettingsUpdateRequiresClientRevision(t *testing.T) {
	service := &stubSystemSettingsService{getValue: defaultSystemSettings(10)}
	server := &Server{settingsService: service}
	response := httptest.NewRecorder()
	server.handleUpdateSystemSettings(
		response,
		httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", strings.NewReader(`{}`)),
	)

	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want 428 body=%s", response.Code, response.Body.String())
	}
	if service.updateCalls != 0 {
		t.Fatalf("domain service called %d times without a precondition, want 0", service.updateCalls)
	}
}

func TestSystemSettingsStaleClientPreconditionsDoNotOverwriteNewerSections(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 10, Addr: ":8080"})

	firstGet := httptest.NewRecorder()
	server.handleGetSystemSettings(firstGet, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	initialETag := firstGet.Header().Get("ETag")
	var initialDocument map[string]any
	if err := json.Unmarshal(firstGet.Body.Bytes(), &initialDocument); err != nil {
		t.Fatalf("decode initial settings: %v", err)
	}
	initialRevision, _ := initialDocument["revision"].(string)
	if initialETag == "" || initialRevision == "" {
		t.Fatalf("initial response omitted concurrency tokens: ETag=%q revision=%q", initialETag, initialRevision)
	}

	newer := server.runner.GetSystemSettings()
	newer.General.MaxConcurrentTests = 17
	newerBody, err := json.Marshal(map[string]any{"general": newer.General})
	if err != nil {
		t.Fatal(err)
	}
	newerRequest := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(newerBody))
	newerRequest.Header.Set("If-Match", initialETag)
	newerResponse := httptest.NewRecorder()
	server.handleUpdateSystemSettings(newerResponse, newerRequest)
	if newerResponse.Code != http.StatusOK {
		t.Fatalf("newer update status = %d body=%s", newerResponse.Code, newerResponse.Body.String())
	}

	stale := server.runner.GetSystemSettings()
	maxErrorRate := 8.5
	stale.Thresholds.MaxErrorRatePct = &maxErrorRate
	staleBody, err := json.Marshal(map[string]any{"thresholds": stale.Thresholds})
	if err != nil {
		t.Fatal(err)
	}
	staleRequest := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(staleBody))
	staleRequest.Header.Set("If-Match", initialETag)
	staleResponse := httptest.NewRecorder()
	server.handleUpdateSystemSettings(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match status = %d, want 412 body=%s", staleResponse.Code, staleResponse.Body.String())
	}

	bodyPrecondition, err := json.Marshal(map[string]any{
		"expected_revision": initialRevision,
		"thresholds":        stale.Thresholds,
	})
	if err != nil {
		t.Fatal(err)
	}
	bodyResponse := httptest.NewRecorder()
	server.handleUpdateSystemSettings(
		bodyResponse,
		httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(bodyPrecondition)),
	)
	if bodyResponse.Code != http.StatusConflict {
		t.Fatalf("stale expected_revision status = %d, want 409 body=%s", bodyResponse.Code, bodyResponse.Body.String())
	}

	after := server.runner.GetSystemSettings()
	if after.General.MaxConcurrentTests != 17 {
		t.Fatalf("stale updates restored max concurrent tests to %d", after.General.MaxConcurrentTests)
	}
	if after.Thresholds.MaxErrorRatePct != nil {
		t.Fatalf("stale updates changed threshold to %#v", after.Thresholds.MaxErrorRatePct)
	}
}

func TestSystemSettingsRevisionDoesNotFingerprintWriteOnlyTokenValue(t *testing.T) {
	first := defaultSystemSettings(10)
	first.Notifications.Webhooks = []NotificationWebhookSettings{{
		Name: "Dynatrace", URL: "https://dynatrace.example.test/api/v2/events/ingest",
		Format: notificationFormatDynatrace, APIToken: "first-secret-token",
	}}
	second := cloneSystemSettings(first)
	second.Notifications.Webhooks[0].APIToken = "second-secret-token"

	if firstRevision, secondRevision := systemSettingsRevision(first), systemSettingsRevision(second); firstRevision != secondRevision {
		t.Fatalf("write-only token values produced distinguishable revisions: %q != %q", firstRevision, secondRevision)
	}
	second.Notifications.Webhooks[0].APIToken = ""
	if firstRevision, emptyRevision := systemSettingsRevision(first), systemSettingsRevision(second); firstRevision == emptyRevision {
		t.Fatalf("token presence did not change the redacted representation revision: %q", firstRevision)
	}
}

func TestSystemSettingsNotificationCredentialChangeDetectionIsOrderIndependent(t *testing.T) {
	previous := NotificationsSettings{Webhooks: []NotificationWebhookSettings{
		{Name: "First", URL: "https://first.example.test/events", Format: notificationFormatDynatrace, APIToken: "first-secret"},
		{Name: "Second", URL: "https://second.example.test/events", Format: notificationFormatDynatrace, APIToken: "second-secret"},
	}}
	reordered := NotificationsSettings{Webhooks: []NotificationWebhookSettings{
		{Name: "Renamed Second", URL: "https://second.example.test/events", Format: notificationFormatDynatrace, APIToken: "second-secret"},
		{Name: "Renamed First", URL: "https://first.example.test/events", Format: notificationFormatDynatrace, APIToken: "first-secret"},
	}}
	if systemSettingsNotificationCredentialsChanged(previous, reordered) {
		t.Fatal("display-name edits and webhook reordering were treated as a credential change")
	}
	if got := systemSettingsNotificationCredentialCount(previous); got != 2 {
		t.Fatalf("credential count = %d, want 2", got)
	}

	rotated := cloneNotificationsSettings(reordered)
	rotated.Webhooks[0].APIToken = "rotated-secret"
	if !systemSettingsNotificationCredentialsChanged(previous, rotated) {
		t.Fatal("token rotation was not detected as a credential change")
	}
	removed := cloneNotificationsSettings(previous)
	removed.Webhooks = removed.Webhooks[:1]
	if !systemSettingsNotificationCredentialsChanged(previous, removed) {
		t.Fatal("credential removal was not detected as a credential change")
	}
}

func TestSystemSettingsTokenOnlyRotationInvalidatesStaleNonblankWriter(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "token-revision.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	initial := server.runner.GetSystemSettings()
	initial.Notifications.Webhooks = []NotificationWebhookSettings{{
		Name: "Dynatrace", URL: "https://dynatrace.example.test/api/v2/events/ingest",
		Format: notificationFormatDynatrace, APIToken: "old-secret-token",
	}}
	if _, err := server.runner.UpdateSystemSettings(initial); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	getResponse := httptest.NewRecorder()
	server.handleGetSystemSettings(getResponse, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	initialETag := getResponse.Header().Get("ETag")

	replica := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	replicaGet := httptest.NewRecorder()
	replica.handleGetSystemSettings(replicaGet, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	if replicaETag := replicaGet.Header().Get("ETag"); replicaETag != initialETag {
		t.Fatalf("HA replicas exposed different durable revisions: %q != %q", initialETag, replicaETag)
	}
	rotated := replica.runner.GetSystemSettings()
	rotated.Notifications.Webhooks[0].APIToken = "new-secret-token"
	rotatedBody, err := json.Marshal(rotated)
	if err != nil {
		t.Fatal(err)
	}
	rotateRequest := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(rotatedBody))
	rotateRequest.Header.Set("If-Match", initialETag)
	rotateResponse := httptest.NewRecorder()
	replica.handleUpdateSystemSettings(rotateResponse, rotateRequest)
	if rotateResponse.Code != http.StatusOK {
		t.Fatalf("token rotation status = %d body=%s", rotateResponse.Code, rotateResponse.Body.String())
	}
	if replacementETag := rotateResponse.Header().Get("ETag"); replacementETag == "" || replacementETag == initialETag {
		t.Fatalf("token-only rotation did not advance ETag: before=%q after=%q", initialETag, replacementETag)
	}

	stale := cloneSystemSettings(initial)
	stale.General.MetricsIntervalSeconds = 7
	staleBody, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	staleRequest := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(staleBody))
	staleRequest.Header.Set("If-Match", initialETag)
	staleResponse := httptest.NewRecorder()
	server.handleUpdateSystemSettings(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale token writer status = %d, want 412 body=%s", staleResponse.Code, staleResponse.Body.String())
	}
	if got := server.runner.GetSystemSettings().Notifications.Webhooks[0].APIToken; got != "new-secret-token" {
		t.Fatalf("stale writer restored token %q, want rotated token", got)
	}
}

func TestSystemSettingsUpdateRejectsUnknownTopLevelField(t *testing.T) {
	service := &stubSystemSettingsService{}
	server := &Server{settingsService: service}
	response := httptest.NewRecorder()
	server.handleUpdateSystemSettings(
		response,
		httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", strings.NewReader(`{"threshholds":{}}`)),
	)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "threshholds") {
		t.Fatalf("response does not identify unknown field: %s", response.Body.String())
	}
	if service.updateCalls != 0 {
		t.Fatalf("domain service called %d times for invalid input, want 0", service.updateCalls)
	}
}

func TestSystemSettingsUpdateRejectsUnknownNestedFieldWithoutResettingSection(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 10, Addr: ":8080"})
	stored := server.runner.GetSystemSettings()
	maxErrorRate := 2.5
	stored.Thresholds.MaxErrorRatePct = &maxErrorRate
	if _, err := server.runner.UpdateSystemSettings(stored); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	response := httptest.NewRecorder()
	server.mux.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", strings.NewReader(`{"thresholds":{"max_eror_rate_pct":5}}`)),
	)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "max_eror_rate_pct") {
		t.Fatalf("response does not identify unknown field: %s", response.Body.String())
	}
	after := server.runner.GetSystemSettings()
	if after.Thresholds.MaxErrorRatePct == nil || *after.Thresholds.MaxErrorRatePct != maxErrorRate {
		t.Fatalf("misspelled nested update reset stored threshold: %#v", after.Thresholds.MaxErrorRatePct)
	}
}

func TestSystemSettingsUpdateAcceptsEchoedResponseProjection(t *testing.T) {
	returned := defaultSystemSettings(10)
	returned.Notifications.Webhooks = []NotificationWebhookSettings{{
		Name:     "Dynatrace",
		Enabled:  true,
		URL:      "https://dynatrace.example.test/api/v2/events/ingest",
		Format:   notificationFormatDynatrace,
		Events:   []string{"threshold_alert"},
		APIToken: "stored-token",
	}}
	service := &stubSystemSettingsService{
		getValue: returned,
		updateResult: systemSettingsUpdateResult{
			systemSettingsUpdatePlan: systemSettingsUpdatePlan{Previous: returned},
			Updated:                  returned,
		},
	}
	server := &Server{settingsService: service}

	getResponse := httptest.NewRecorder()
	server.handleGetSystemSettings(getResponse, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", getResponse.Code, getResponse.Body.String())
	}
	if !bytes.Contains(getResponse.Body.Bytes(), []byte(`"has_api_token":true`)) {
		t.Fatalf("GET response omitted configured-token projection: %s", getResponse.Body.String())
	}

	putResponse := httptest.NewRecorder()
	putRequest := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(getResponse.Body.Bytes()))
	putRequest.Header.Set("If-Match", getResponse.Header().Get("ETag"))
	server.handleUpdateSystemSettings(
		putResponse,
		putRequest,
	)
	if putResponse.Code != http.StatusOK {
		t.Fatalf("echoed PUT status = %d body=%s", putResponse.Code, putResponse.Body.String())
	}
	if service.updateCalls != 1 {
		t.Fatalf("domain service calls = %d, want 1", service.updateCalls)
	}
	if got := service.requested.Notifications.Webhooks[0].APIToken; got != "" {
		t.Fatalf("response projection entered request settings as api token %q", got)
	}
}

func TestSystemSettingsHandlerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{name: "conflict", err: errSystemSettingsConflict, wantStatus: http.StatusPreconditionFailed, wantError: errSystemSettingsConflict.Error()},
		{name: "validation", err: errors.New("capacity settings are invalid"), wantStatus: http.StatusBadRequest, wantError: "capacity settings are invalid"},
		{name: "audit pre-commit", err: &systemSettingsBeforeCommitError{err: errors.New("audit unavailable")}, wantStatus: http.StatusServiceUnavailable, wantError: "internal server error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubSystemSettingsService{updateErr: test.err}
			server := &Server{settingsService: service}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", strings.NewReader(`{}`))
			request.Header.Set("If-Match", systemSettingsETag(systemSettingsRevision(service.getValue)))
			server.handleUpdateSystemSettings(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if payload.Error != test.wantError {
				t.Fatalf("error = %q, want %q", payload.Error, test.wantError)
			}
		})
	}
}

func TestTestRunnerSystemSettingsServicePreservesOmittedSectionsAfterHARefresh(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "settings-service.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	writer := NewTestRunner(2, NewHub(), store)
	if _, err := writer.UpdateSystemSettings(writer.GetSystemSettings()); err != nil {
		t.Fatalf("persist initial settings: %v", err)
	}
	staleRunner := NewTestRunner(2, NewHub(), store)
	staleRequest := staleRunner.GetSystemSettings()
	staleRequest.General.MetricsIntervalSeconds = 7

	latest := writer.GetSystemSettings()
	latest.Prometheus = PrometheusSettings{Enabled: true, BaseURL: "https://prometheus.example.test", TimeoutSeconds: 9}
	latest.ClientApp = ClientAppSettings{Availability: clientAppEnabled}
	latest.ClientDownloads = ClientDownloadsSettings{Enabled: false}
	latest.ScriptImports = ScriptImportSettings{AllowSourceSecretMigration: true}
	if _, err := writer.UpdateSystemSettings(latest); err != nil {
		t.Fatalf("persist newer replica settings: %v", err)
	}

	body := marshalSystemSettingsWithoutSections(t, staleRequest, "prometheus", "client_app", "client_downloads", "script_imports")
	var requested SystemSettings
	if err := json.Unmarshal(body, &requested); err != nil {
		t.Fatalf("decode partial request: %v", err)
	}
	service := newTestRunnerSystemSettingsService(staleRunner)
	result, err := service.Update(requested, body, systemSettingsRevision(writer.GetSystemSettings()), func(plan systemSettingsUpdatePlan) error {
		if plan.PolicyChanged {
			t.Fatal("omitted migration policy was treated as a policy change")
		}
		if !reflect.DeepEqual(plan.Previous.ClientDownloads, latest.ClientDownloads) {
			t.Fatalf("pre-commit snapshot was not refreshed from HA store: %#v", plan.Previous.ClientDownloads)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("service update: %v", err)
	}
	if result.Updated.General.MetricsIntervalSeconds != 7 {
		t.Fatalf("requested general update was not applied: %#v", result.Updated.General)
	}
	if !reflect.DeepEqual(result.Updated.Prometheus, latest.Prometheus) ||
		!reflect.DeepEqual(result.Updated.ClientApp, latest.ClientApp) ||
		!reflect.DeepEqual(result.Updated.ClientDownloads, latest.ClientDownloads) ||
		!reflect.DeepEqual(result.Updated.ScriptImports, latest.ScriptImports) {
		t.Fatalf("omitted sections were not preserved: updated=%#v latest=%#v", result.Updated, latest)
	}
}

func TestTestRunnerSystemSettingsServiceAbortsWhenPreCommitAuditFails(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	requested := runner.GetSystemSettings()
	requested.ScriptImports.AllowSourceSecretMigration = true
	body, err := json.Marshal(requested)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("required audit unavailable")
	service := newTestRunnerSystemSettingsService(runner)
	result, err := service.Update(requested, body, systemSettingsRevision(runner.GetSystemSettings()), func(plan systemSettingsUpdatePlan) error {
		if !plan.PolicyChanged || !plan.Candidate.ScriptImports.AllowSourceSecretMigration {
			t.Fatalf("pre-commit plan = %#v, want migration policy change", plan)
		}
		return wantErr
	})
	var beforeCommitErr *systemSettingsBeforeCommitError
	if !errors.As(err, &beforeCommitErr) || !errors.Is(err, wantErr) {
		t.Fatalf("service error = %v, want wrapped pre-commit error", err)
	}
	if !result.PolicyChanged {
		t.Fatal("failed result lost policy-change metadata")
	}
	if runner.GetSystemSettings().ScriptImports.AllowSourceSecretMigration {
		t.Fatal("settings mutated after pre-commit audit failure")
	}
}

func TestTestRunnerSystemSettingsServiceReturnsRevisionConflict(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	requested := runner.GetSystemSettings()
	requested.General.MaxConcurrentTests = 5
	body, err := json.Marshal(requested)
	if err != nil {
		t.Fatal(err)
	}
	service := newTestRunnerSystemSettingsService(runner)
	_, err = service.Update(requested, body, systemSettingsRevision(runner.GetSystemSettings()), func(systemSettingsUpdatePlan) error {
		newer := runner.GetSystemSettings()
		newer.General.MaxConcurrentTests = 3
		_, updateErr := runner.UpdateSystemSettings(newer)
		return updateErr
	})
	if !errors.Is(err, errSystemSettingsConflict) {
		t.Fatalf("service error = %v, want revision conflict", err)
	}
	if got := runner.GetSystemSettings().General.MaxConcurrentTests; got != 3 {
		t.Fatalf("conflicting service update overwrote newer value: %d", got)
	}
}

func TestSystemSettingsServiceKeepsServerConstructionCompatible(t *testing.T) {
	constructed := NewServer(ServerConfig{MaxConcurrent: 3})
	if constructed.settingsService == nil {
		t.Fatal("NewServer did not install the TestRunner-backed settings service")
	}
	if got := constructed.systemSettingsDomainService().Get().General.MaxConcurrentTests; got != 3 {
		t.Fatalf("constructed service max concurrent = %d, want 3", got)
	}
	constructed.runner = NewTestRunner(6, NewHub(), nil)
	if got := constructed.systemSettingsDomainService().Get().General.MaxConcurrentTests; got != 6 {
		t.Fatalf("service did not follow a compatible runner replacement: %d", got)
	}

	runner := NewTestRunner(4, NewHub(), nil)
	legacyLiteral := &Server{runner: runner}
	if got := legacyLiteral.systemSettingsDomainService().Get().General.MaxConcurrentTests; got != 4 {
		t.Fatalf("legacy Server literal service max concurrent = %d, want 4", got)
	}
}

func marshalSystemSettingsWithoutSections(t *testing.T, settings SystemSettings, sections ...string) []byte {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, section := range sections {
		delete(fields, section)
	}
	body, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
