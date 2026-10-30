package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A request that keeps an imported credential is sent wherever its target
// resolves to, so the parameters and data sources that target interpolates
// are part of the context the editor cannot change. Editing the "host"
// parameter used to save and send the kept token to the new host.
func TestRetainedCredentialReferencedParameterEditsAreRefused(t *testing.T) {
	const credential = "Bearer retained-reference-synthetic-token"
	server, _, _, projectID := newProjectSecretAPIServer(t)
	source := &dsl.Script{
		ID: "retained-reference-script", Name: "Retained reference", Version: "1",
		Protocol: "http", ProjectID: projectID,
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Parameters: []dsl.Parameter{
			{Name: "host", Type: "list", Values: []string{"origin.example.test"}, Update: "once"},
			{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"},
		},
		DataSources: []dsl.DataSource{
			{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true},
			{Name: "regions", Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true},
		},
		Actions: []dsl.Action{
			{
				Name: "orders", Type: "request", Method: http.MethodGet, Target: "https://{{param.host}}/orders",
				Headers: map[string]string{"Authorization": credential},
			},
			{
				Name: "audit", Type: "request", Method: http.MethodGet, Target: "https://{{data.hosts.host}}/audit",
				Headers: map[string]string{"Authorization": credential},
			},
			{Name: "cart", Type: "request", Method: http.MethodGet, Target: "https://origin.example.test/cart"},
		},
	}
	if _, _, err := server.runner.CreateImportedScriptDurableReportingReplay(source, "test"); err != nil {
		t.Fatalf("create imported script: %v", err)
	}

	// save PUTs the script as an editor that cannot read the kept credential
	// sends it back: the disclosure projection of the stored script, edited.
	save := func(edit func(*dsl.Script)) *httptest.ResponseRecorder {
		t.Helper()
		draft := maskScriptSecrets(server.runner.scripts[source.ID].Script)
		if draft == nil {
			t.Fatal("maskScriptSecrets returned no projected script")
		}
		edit(draft)
		payload, err := json.Marshal(map[string]any{"script": draft})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "retained-reference-synthetic-token") {
			t.Fatalf("masked draft carries the kept credential: %s", payload)
		}
		put := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+source.ID, bytes.NewReader(payload))
		put.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, put)
		return recorder
	}
	stored := func() *dsl.Script {
		t.Helper()
		return server.runner.scripts[source.ID].Script
	}

	for name, edit := range map[string]func(*dsl.Script){
		"parameter feeding the target": func(script *dsl.Script) {
			script.Parameters[0].Values = []string{"attacker.test"}
		},
		"data source feeding the target": func(script *dsl.Script) {
			script.DataSources[0].InlineData = "host\nattacker.test\n"
		},
	} {
		got := save(edit)
		if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "retained imported credentials") {
			t.Fatalf("%s: status = %d; body=%s", name, got.Code, got.Body.String())
		}
		if strings.Contains(got.Body.String(), "retained-reference-synthetic-token") {
			t.Fatalf("%s: refusal disclosed the kept credential: %s", name, got.Body.String())
		}
		current := stored()
		if current.Parameters[0].Values[0] != "origin.example.test" ||
			current.DataSources[0].InlineData != "host\norigin.example.test\n" ||
			current.Actions[0].Headers["Authorization"] != credential {
			t.Fatalf("%s: refused save changed the stored script: %#v", name, current)
		}
	}

	// Parameters and data sources no kept-credential request reads, and
	// requests without a kept credential, still save.
	for name, edit := range map[string]func(*dsl.Script){
		"parameter nothing references": func(script *dsl.Script) {
			script.Parameters[1].Values = []string{"east"}
		},
		"data source nothing references": func(script *dsl.Script) {
			script.DataSources[1].InlineData = "region\neast\n"
		},
		"added parameter": func(script *dsl.Script) {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: "tenant", Type: "list", Values: []string{"t1"}, Update: "once"})
		},
		"request without the kept credential": func(script *dsl.Script) {
			script.Actions[2].Headers = map[string]string{"X-Region": "{{param.region}}"}
		},
	} {
		if got := save(edit); got.Code != http.StatusOK {
			t.Fatalf("%s: status = %d; body=%s", name, got.Code, got.Body.String())
		}
	}
	if got := stored().Actions[0].Headers["Authorization"]; got != credential {
		t.Fatalf("accepted saves changed the kept Authorization = %q", got)
	}
}
