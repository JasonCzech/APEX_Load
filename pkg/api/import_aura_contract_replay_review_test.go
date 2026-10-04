package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

var registerPublicAuraContractReviewHTTP sync.Once

func TestAPIApplyAuraContractReturnedScriptRemainsReplayable(t *testing.T) {
	const token = "rotated token+branch/123456"
	const literal = "{{secret.previously_encoded_literal}}"
	var accepted atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
			w.WriteHeader(400)
			return
		}
		var message struct {
			Actions []struct {
				Params map[string]string `json:"params"`
			} `json:"actions"`
		}
		if err := json.Unmarshal([]byte(r.Form.Get("message")), &message); err != nil || len(message.Actions) != 1 {
			t.Error("returned script did not send valid Aura JSON")
			w.WriteHeader(400)
			return
		}
		if r.Form.Get("aura.token") != token || message.Actions[0].Params["echo"] != literal || message.Actions[0].Params["recordId"] != "synthetic-record" {
			t.Error("public apply projection changed old encoded token or literal semantics")
			w.WriteHeader(400)
			return
		}
		accepted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"actions":[{"id":"fixture;a","state":"SUCCESS","returnValue":{"ok":true}}]}`))
	}))
	defer target.Close()
	message, err := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "fixture;a", "descriptor": "fixture://save", "params": map[string]string{"recordId": "synthetic-record", "echo": literal}}}})
	if err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{Name: "Public Aura contract", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: target.URL}, Actions: []dsl.Action{{Name: "Save", Type: "POST", Target: "/aura", Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, Body: "message=" + url.QueryEscape(string(message)) + "&aura.token={{var.existing_correlation}}", Assertions: []dsl.Assertion{{Type: "aura_actions_success"}}}}}
	advice := buildTrustedImportParameterization(script)
	var id string
	for _, suggestion := range advice.Suggestions {
		if len(suggestion.Locations) == 1 && strings.HasSuffix(suggestion.Locations[0].DecodedFieldPath, ".recordId") {
			id = suggestion.ID
		}
	}
	if id == "" {
		t.Fatal("business field not selectable")
	}
	response, applied := postParameterizeApply(t, NewServer(ServerConfig{}), importParameterizeApplyRequest{Script: script, SelectedIDs: []string{id}})
	if response.Code != http.StatusOK || applied.AppliedCount != 1 || applied.Script == nil {
		t.Fatalf("apply failed: status=%d", response.Code)
	}
	contract, err := aurapayload.ReadTemplateContract(applied.Script.Actions[0].Metadata[dsl.ImportAuraTemplateContractMetadataKey])
	if err != nil || contract == nil {
		t.Fatalf("public apply response invalidated contract: %v", err)
	}
	references, err := engine.ReferencedSecretNames(applied.Script)
	if err != nil || len(references) != 0 {
		t.Fatalf("structural contract/literal created secret requirements: names=%v err=%v", references, err)
	}
	const runtimeProtocol = "api-aura-contract-review-http"
	registerPublicAuraContractReviewHTTP.Do(func() { protocol.Register(runtimeProtocol, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	applied.Script.Protocol = runtimeProtocol
	report, err := engine.ReplayScript(context.Background(), applied.Script, engine.ReplayOptions{IgnoreThinkTime: true, Variables: map[string]string{"existing_correlation": url.QueryEscape(token)}})
	if err != nil || report == nil || !report.Success || accepted.Load() != 1 {
		t.Fatalf("returned apply script cannot replay: err=%v accepted=%d", err, accepted.Load())
	}
}
