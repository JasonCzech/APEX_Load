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
	updateResult systemSettingsUpdateResult
	updateErr    error
}

func (s *stubSystemSettingsService) Get() SystemSettings {
	s.getCalls++
	return cloneSystemSettings(s.getValue)
}

func (s *stubSystemSettingsService) Update(requested SystemSettings, rawBody []byte, _ systemSettingsBeforeCommit) (systemSettingsUpdateResult, error) {
	s.updateCalls++
	s.requested = cloneSystemSettings(requested)
	s.rawBody = append([]byte(nil), rawBody...)
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

	requestBody := []byte(`{"general":{"max_concurrent_tests":17}}`)
	putResponse := httptest.NewRecorder()
	server.handleUpdateSystemSettings(putResponse, httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(requestBody)))
	if putResponse.Code != http.StatusOK || service.updateCalls != 1 {
		t.Fatalf("PUT response = %d, calls = %d, body=%s", putResponse.Code, service.updateCalls, putResponse.Body.String())
	}
	if service.requested.General.MaxConcurrentTests != 17 || !bytes.Equal(service.rawBody, requestBody) {
		t.Fatalf("delegated update = max:%d raw:%q", service.requested.General.MaxConcurrentTests, service.rawBody)
	}
	if err := json.Unmarshal(putResponse.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if got.General.MaxConcurrentTests != 13 {
		t.Fatalf("PUT response max concurrent = %d, want service result 13", got.General.MaxConcurrentTests)
	}
}

func TestSystemSettingsHandlerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{name: "conflict", err: errSystemSettingsConflict, wantStatus: http.StatusConflict, wantError: errSystemSettingsConflict.Error()},
		{name: "validation", err: errors.New("capacity settings are invalid"), wantStatus: http.StatusBadRequest, wantError: "capacity settings are invalid"},
		{name: "audit pre-commit", err: &systemSettingsBeforeCommitError{err: errors.New("audit unavailable")}, wantStatus: http.StatusServiceUnavailable, wantError: "internal server error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubSystemSettingsService{updateErr: test.err}
			server := &Server{settingsService: service}
			response := httptest.NewRecorder()
			server.handleUpdateSystemSettings(response, httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", strings.NewReader(`{}`)))
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
	result, err := service.Update(requested, body, func(plan systemSettingsUpdatePlan) error {
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
	result, err := service.Update(requested, body, func(plan systemSettingsUpdatePlan) error {
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
	_, err = service.Update(requested, body, func(systemSettingsUpdatePlan) error {
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
