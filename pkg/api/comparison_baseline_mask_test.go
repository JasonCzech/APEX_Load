package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

// TestComparisonBaselinePinUnpinMaskRuntimeSecrets is a regression test for the
// masking gap where the pin/unpin comparison-baseline handlers returned a stored
// *ManagedTest verbatim, leaking runtime-override secrets that the canonical
// GET /tests/{id} read path already redacts.
func TestComparisonBaselinePinUnpinMaskRuntimeSecrets(t *testing.T) {
	server := recordingTestServer(t)

	const id = "run-mask-baseline"
	server.runner.mu.Lock()
	server.runner.tests[id] = &ManagedTest{
		ID:     id,
		Name:   "Baseline secret test",
		Status: "COMPLETE",
		RuntimeSettings: &RuntimeSettingsOverride{
			Env:  map[string]string{"API_TOKEN": "supersecret"},
			HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: "pw"}},
		},
		Groups: []TestGroup{
			{RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: "tok"}}}},
		},
	}
	server.runner.mu.Unlock()
	if err := server.store.SaveTest(&storage.TestRecord{
		ID:        id,
		Name:      "Baseline secret test",
		Status:    "COMPLETE",
		ProjectID: storage.DefaultProjectID,
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	check := func(t *testing.T, h http.HandlerFunc, label string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+id+"/comparison-baseline", nil)
		req.SetPathValue("id", id)
		resp := httptest.NewRecorder()
		h(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s = %d body=%s", label, resp.Code, resp.Body.String())
		}
		var got ManagedTest
		if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s decode: %v", label, err)
		}
		if got.RuntimeSettings == nil || got.RuntimeSettings.Env["API_TOKEN"] != scrub.Redacted {
			t.Fatalf("%s: env secret not redacted: %#v", label, got.RuntimeSettings)
		}
		if got.RuntimeSettings.HTTP == nil || got.RuntimeSettings.HTTP.Auth.Password != scrub.Redacted {
			t.Fatalf("%s: http password not redacted", label)
		}
		if len(got.Groups) == 0 || got.Groups[0].RuntimeSettings == nil ||
			got.Groups[0].RuntimeSettings.HTTP.Auth.Token != scrub.Redacted {
			t.Fatalf("%s: group token not redacted", label)
		}
	}

	check(t, server.handlePinComparisonBaseline, "pin")
	check(t, server.handleUnpinComparisonBaseline, "unpin")

	// Masking must operate on a copy: the stored secret stays intact.
	server.runner.mu.RLock()
	stored := server.runner.tests[id]
	envSecret := stored.RuntimeSettings.Env["API_TOKEN"]
	server.runner.mu.RUnlock()
	if envSecret != "supersecret" {
		t.Fatalf("masking mutated the stored test secret: %q", envSecret)
	}
}
