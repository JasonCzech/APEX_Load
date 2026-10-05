package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScriptAuthoringRouterPreservesStrongRepresentationAcrossIndependentActivity(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	id := "authoring-activity-stability"
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript(id)}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var saved StoredScript
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AuthoringProjection == nil {
		t.Fatal("missing final mutation projection")
	}
	var canonical bytes.Buffer
	if err := json.NewEncoder(&canonical).Encode(saved.AuthoringProjection); err != nil {
		t.Fatal(err)
	}
	read := func() *httptest.ResponseRecorder {
		t.Helper()
		response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+id+"?view=authoring", nil, "")
		if response.Code != http.StatusOK {
			t.Fatalf("authoring GET: %d %s", response.Code, response.Body.String())
		}
		var projection ScriptAuthoringProjection
		decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&projection); err != nil {
			t.Fatalf("strong representation includes independent context: %v", err)
		}
		tag, err := scriptAuthoringETag(projection.ContentRevision)
		if err != nil || response.Header().Get("ETag") != tag || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("projection/tag admission differs")
		}
		if !bytes.Equal(response.Body.Bytes(), canonical.Bytes()) {
			t.Fatalf("router altered canonical representation: %s", response.Body.String())
		}
		return response
	}
	first := read()
	for _, query := range []string{"view=authoring&view=default", "view=authoring&view=authoring", "view=default&view=authoring", "view="} {
		refused := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+id+"?"+query, nil, "")
		if refused.Code != http.StatusBadRequest || refused.Header().Get("ETag") != "" {
			t.Fatalf("ambiguous view gained strong representation: %s status=%d tag=%q", query, refused.Code, refused.Header().Get("ETag"))
		}
	}
	legacy := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+id, nil, "")
	var before struct {
		Activity *storage.ObjectActivitySummary `json:"activity"`
	}
	if legacy.Code != http.StatusOK || legacy.Header().Get("ETag") != "" || json.Unmarshal(legacy.Body.Bytes(), &before) != nil || before.Activity == nil || before.Activity.Creator == nil || before.Activity.LastModifier != nil {
		t.Fatalf("legacy creator enrichment missing: %s", legacy.Body.String())
	}
	eventRequest := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+id, nil)
	server.recordAuditEvent(eventRequest, "script.notes.update", "success", "script", id, saved.ProjectID, nil)
	events, err := store.ListAuditEvents(storage.AuditEventFilter{ProjectID: saved.ProjectID, ResourceType: "script", ResourceID: id, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Action == "script.notes.update" {
			found = true
		}
	}
	if !found {
		t.Fatal("independent audit change was not durable")
	}
	second := read()
	if second.Header().Get("ETag") != first.Header().Get("ETag") || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("audit changed body without changing strong tag")
	}
	legacy = authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+id, nil, "")
	var after struct {
		Activity *storage.ObjectActivitySummary `json:"activity"`
	}
	if legacy.Code != http.StatusOK || legacy.Header().Get("ETag") != "" || json.Unmarshal(legacy.Body.Bytes(), &after) != nil || after.Activity == nil || after.Activity.LastModifier == nil || after.Activity.LastModifier.Action != "modify" {
		t.Fatalf("legacy activity enrichment was suppressed: %s", legacy.Body.String())
	}
}
