package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRebindScriptSecretReferenceUsesObservedAuthoredRevisionWithoutEnrichedTimestamp(t *testing.T) {
	s, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := authoringAPIScript("rebind-authoring")
	script.Actions[0].Target = "/{{secret.OLD_TOKEN}}"
	if _, err := s.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	projection, tag := requireAuthoringAPIProjection(t, s, script.ID)
	body, err := json.Marshal(projection.Script)
	if err != nil {
		t.Fatal(err)
	}
	var draft dsl.Script
	if err := json.Unmarshal(body, &draft); err != nil {
		t.Fatal(err)
	}
	if !draft.UpdatedAt.IsZero() {
		t.Fatal("authoring projection accidentally acquired context timestamp")
	}
	draft.Name = "Unsaved caller draft"
	draft.Actions[0].Target = "/unsaved/{{secret.OLD_TOKEN}}"
	request := map[string]any{"script": draft, "old_name": "OLD_TOKEN", "new_name": "NEW_TOKEN"}
	reply := authoringAPIRequest(t, s, http.MethodPost, "/api/v1/scripts/secret-references/rebind", request, tag)
	if reply.Code != 200 || reply.Header().Get("ETag") != "" || !strings.Contains(reply.Body.String(), "{{secret.NEW_TOKEN}}") {
		t.Fatal("qualified pure transform", reply.Code, reply.Body.String())
	}
	var transformed struct {
		Script map[string]json.RawMessage `json:"script"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &transformed); err != nil {
		t.Fatal(err)
	}
	for _, contextField := range []string{"created_at", "updated_at", "archived_at", "activity", "content_revision", "authoring_projection"} {
		if _, exists := transformed.Script[contextField]; exists {
			t.Fatal("pure authored transform introduced context or authority", contextField)
		}
	}
	var transformedDocument ScriptAuthoringDocument
	projectedBody, err := json.Marshal(transformed.Script)
	if err != nil || json.Unmarshal(projectedBody, &transformedDocument) != nil || transformedDocument.Name != draft.Name || transformedDocument.Actions[0].Target != "/unsaved/{{secret.NEW_TOKEN}}" {
		t.Fatal("pure transform discarded caller edits", string(projectedBody), err)
	}
	// The legacy contract still returns its supplied timestamps unchanged.
	legacyDraft, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	qualifiedLegacy := authoringAPIRequest(t, s, http.MethodPost, "/api/v1/scripts/secret-references/rebind", map[string]any{"script": legacyDraft, "old_name": "OLD_TOKEN", "new_name": "NEW_TOKEN"}, "")
	if qualifiedLegacy.Code != http.StatusOK || qualifiedLegacy.Header().Get("ETag") != "" {
		t.Fatal("legacy helper changed", qualifiedLegacy.Code, qualifiedLegacy.Body.String())
	}
	var legacyTransformed struct {
		Script dsl.Script `json:"script"`
	}
	if json.Unmarshal(qualifiedLegacy.Body.Bytes(), &legacyTransformed) != nil || !legacyTransformed.Script.CreatedAt.Equal(legacyDraft.CreatedAt) || !legacyTransformed.Script.UpdatedAt.Equal(legacyDraft.UpdatedAt) {
		t.Fatal("legacy helper lost existing clocks")
	}
	after, _ := requireAuthoringAPIProjection(t, s, script.ID)
	if after.ContentRevision != projection.ContentRevision || after.Script.Actions[0].Target != "/{{secret.OLD_TOKEN}}" {
		t.Fatal("pure transformation changed durable script")
	}
	// Legacy compatibility remains timestamp-gated and cannot be inferred from
	// the missing timestamp in a new projection.
	legacy := authoringAPIRequest(t, s, http.MethodPost, "/api/v1/scripts/secret-references/rebind", request, "")
	if legacy.Code != 409 {
		t.Fatal("unqualified new draft used legacy compatibility", legacy.Code)
	}
	winner, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	winner.Name = "Concurrent authored winner"
	if err := store.SaveScript(winner); err != nil {
		t.Fatal(err)
	}
	stale := authoringAPIRequest(t, s, http.MethodPost, "/api/v1/scripts/secret-references/rebind", request, tag)
	if stale.Code != 412 || strings.Contains(stale.Body.String(), "changed_locations") {
		t.Fatal("stale transform accepted", stale.Code, stale.Body.String())
	}
	for _, bad := range []string{"W/" + tag, "*", tag + "," + tag, `"script-authoring-v2:scr-v1-` + strings.Repeat("a", 64) + `"`} {
		reply := authoringAPIRequest(t, s, http.MethodPost, "/api/v1/scripts/secret-references/rebind", request, bad)
		if reply.Code != 400 {
			t.Fatal("malformed revision accepted", bad, reply.Code)
		}
	}
}
