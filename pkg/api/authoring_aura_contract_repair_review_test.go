package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Use ordinary short business identifiers, as in the existing authoring API
// fixtures. Captured token-shaped aliases must follow secret binding policy.
const authoringAuraRepairSample = "abc12345"
const authoringAuraURLSample = "xyz98765"

func authoringAuraContractRepairDraft(t *testing.T, server *Server) *dsl.Script {
	t.Helper()
	body := "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"note":"`+authoringAuraRepairSample+` {{var.literal}}","recordId":"business"}}]}`)
	script := &dsl.Script{
		Name: "Authoring Aura contract", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://fixture.example.test"},
		Actions: []dsl.Action{
			{Name: "Bootstrap", Type: "GET", Target: "/bootstrap"},
			{Name: "Aura", Type: "POST", Target: "/aura?order_ref=" + authoringAuraRepairSample + "&url_ref=" + authoringAuraURLSample,
				Body: body, Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "X-Order-Ref": authoringAuraRepairSample, "X-URL-Ref": authoringAuraURLSample}},
		},
	}
	advice := buildTrustedImportParameterization(script)
	var selectedID string
	for _, suggestion := range advice.Suggestions {
		if len(suggestion.Locations) == 1 && strings.HasSuffix(suggestion.Locations[0].DecodedFieldPath, ".recordId") {
			selectedID = suggestion.ID
		}
	}
	if selectedID == "" {
		t.Fatal("fixture business field was not parameterizable")
	}
	response, applied := postParameterizeApply(t, server, importParameterizeApplyRequest{Script: script, SelectedIDs: []string{selectedID}})
	if response.Code != http.StatusOK || applied.Script == nil || applied.AppliedCount != 1 {
		t.Fatalf("public parameterization failed: status=%d", response.Code)
	}
	script = applied.Script
	action := &script.Actions[1]
	if action.Target != "/aura?order_ref="+authoringAuraRepairSample+"&url_ref="+authoringAuraURLSample || action.Headers["X-Order-Ref"] != authoringAuraRepairSample || action.Headers["X-URL-Ref"] != authoringAuraURLSample {
		t.Fatal("fixture business identifiers did not survive public projection")
	}
	inspection, err := aurapayload.Inspect(action.Body, action.BodyEncoding)
	if err != nil {
		t.Fatal(err)
	}
	var edits []aurapayload.Edit
	for _, leaf := range inspection.Leaves {
		if leaf.Key == "recordId" {
			edits = append(edits, aurapayload.Edit{Route: leaf.Route, Shape: inspection.Shape, Expected: leaf.Value, Replacement: "business"})
		}
	}
	if len(edits) != 1 {
		t.Fatal("fixture business field was not restorable")
	}
	action.Body, err = aurapayload.Replace(action.Body, action.BodyEncoding, edits)
	if err != nil {
		t.Fatal(err)
	}
	assertAuthoringAuraContractValid(t, script)
	// Prove the raw edit that the API previously applied would invalidate this
	// otherwise valid contract, rather than testing an already malformed draft.
	broken := cloneScriptDefinition(script)
	broken.Actions[1].Body = strings.ReplaceAll(broken.Actions[1].Body, authoringAuraRepairSample, "{{var.repaired}}")
	contract, err := aurapayload.ReadTemplateContract(broken.Actions[1].Metadata[dsl.ImportAuraTemplateContractMetadataKey])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := aurapayload.ResolveContextualWithWorkAccounting(broken.Actions[1].Body, broken.Actions[1].BodyEncoding, contract, func(value string, _ aurapayload.TemplateContext) (string, string) { return value, value }, aurapayload.MaxWorkBytes); err == nil {
		t.Fatal("fixture raw replacement did not invalidate the retained occurrence")
	}
	return script
}

func assertAuthoringAuraContractValid(t *testing.T, script *dsl.Script) {
	t.Helper()
	if err := dsl.ValidateScriptError(script); err != nil {
		t.Fatalf("authoring draft is invalid: %v", err)
	}
	action := script.Actions[1]
	contract, err := aurapayload.ReadTemplateContract(action.Metadata[dsl.ImportAuraTemplateContractMetadataKey])
	if err != nil || contract == nil || len(contract.LiteralOccurrences) != 1 {
		t.Fatalf("fixture lost its retained literal contract: %v", err)
	}
	if _, _, _, err := aurapayload.ResolveContextualWithWorkAccounting(action.Body, action.BodyEncoding, contract, func(value string, _ aurapayload.TemplateContext) (string, string) { return value, value }, aurapayload.MaxWorkBytes); err != nil {
		t.Fatalf("authoring draft contract is invalid: %v", err)
	}
}

func TestAuthoringAuraContractCorrelationRepairsRemainAtomic(t *testing.T) {
	for _, test := range []struct {
		name                                 string
		apply, batch, extractorOnly, urlOnly bool
		blocked                              bool
	}{
		{name: "single preview requires manual review", blocked: true},
		{name: "single apply requires manual review", apply: true, blocked: true},
		{name: "batch apply requires manual review", apply: true, batch: true, blocked: true},
		{name: "URL and header apply remain supported", apply: true, urlOnly: true},
		{name: "extractor only apply remains supported", apply: true, extractorOnly: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			seedAuthoringScriptProjects(t, store, "project-authoring")
			server := NewServer(ServerConfig{Store: store})
			script := authoringAuraContractRepairDraft(t, server)
			createBody, err := json.Marshal(authoringSessionRequest{ProjectID: "project-authoring", Source: "har", Name: script.Name, Status: "reviewing", Draft: *script})
			if err != nil {
				t.Fatal(err)
			}
			createdResponse := httptest.NewRecorder()
			server.mux.ServeHTTP(createdResponse, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(createBody)))
			if createdResponse.Code != http.StatusCreated {
				t.Fatalf("contract-bearing session creation failed: status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
			}
			var created storage.AuthoringSession
			if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			before, err := store.GetAuthoringSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertAuthoringAuraContractValid(t, before.Draft)
			foundIndex := 1
			usedIndexes := []int{2}
			req := authoringCorrelationPreviewRequest{Name: "repaired", Expression: "$.csrf_token", SampleValue: authoringAuraRepairSample, FoundActionIndex: &foundIndex, UsedActionIndexes: &usedIndexes, Apply: test.apply, ExpectedUpdatedAt: created.UpdatedAt}
			if test.urlOnly {
				req.SampleValue = authoringAuraURLSample
			}
			if test.extractorOnly {
				replace := false
				req.ReplaceSampleValue = &replace
			}
			if test.batch {
				repair := req
				repair.Confidence = 0.99
				repair.Severity = "error"
				req.Repairs = []authoringCorrelationPreviewRequest{repair}
				req.AutoApplyHighConfidence = true
			}
			requestBody, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/correlations/preview", bytes.NewReader(requestBody)))
			if test.blocked && !test.batch {
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "manual structural review") {
					t.Fatalf("contextual raw repair was not rejected with manual advice: status=%d body=%s", response.Code, response.Body.String())
				}
			} else {
				if response.Code != http.StatusOK {
					t.Fatalf("correlation response failed: status=%d body=%s", response.Code, response.Body.String())
				}
				var result authoringCorrelationPreviewResponse
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if test.blocked {
					if result.Applied || result.AppliedCount != 0 || len(result.Items) != 1 || result.Items[0].Applied || !strings.Contains(result.Items[0].Error, "manual structural review") {
						t.Fatalf("batch claimed contextual raw repair was applied: %#v", result)
					}
				} else if !result.Applied {
					t.Fatal("supported correlation repair was not applied")
				}
			}
			after, err := store.GetAuthoringSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertAuthoringAuraContractValid(t, after.Draft)
			if after.Draft.Actions[1].Body != before.Draft.Actions[1].Body {
				t.Fatal("correlation repair changed the contextual body")
			}
			if test.blocked {
				if len(after.Draft.Actions[0].Extractors) != 0 || after.Draft.Actions[1].Target != before.Draft.Actions[1].Target || after.Draft.Actions[1].Headers["X-Order-Ref"] != before.Draft.Actions[1].Headers["X-Order-Ref"] {
					t.Fatal("rejected repair partially changed its extractor or request fields")
				}
			} else {
				if len(after.Draft.Actions[0].Extractors) != 1 {
					t.Fatal("supported repair lost its extractor")
				}
				if test.urlOnly && (!strings.Contains(after.Draft.Actions[1].Target, "url_ref={{var.repaired}}") || after.Draft.Actions[1].Headers["X-URL-Ref"] != "{{var.repaired}}") {
					t.Fatal("supported URL or header correlation was not repaired")
				}
				if test.extractorOnly && (after.Draft.Actions[1].Target != before.Draft.Actions[1].Target || after.Draft.Actions[1].Headers["X-Order-Ref"] != before.Draft.Actions[1].Headers["X-Order-Ref"]) {
					t.Fatal("extractor-only repair changed a request")
				}
			}
		})
	}
}
