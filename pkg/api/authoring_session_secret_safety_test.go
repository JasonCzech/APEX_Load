package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func authoringSecretDraft(projectID, authorization, reviewSecret string) *dsl.Script {
	return &dsl.Script{
		Name: "Secret-safe authoring draft", Version: "1.0.0", Protocol: "http", ProjectID: projectID,
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "test", Preserved: map[string]any{
			"source_review": map[string]any{"authorization": "Bearer " + reviewSecret},
		}}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			Headers: map[string]string{"Authorization": authorization},
		}},
	}
}

func authoringSecretRequest(t *testing.T, server *Server, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func assertAuthoringDraftDisclosureSafe(t *testing.T, draft *dsl.Script, forbidden ...string) {
	t.Helper()
	payload, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(string(payload), value) {
			t.Fatalf("durable authoring draft contains %q: %s", value, payload)
		}
	}
	if requirements := discoverSecretBindingRequirements(draft, 0); len(requirements) != 0 {
		t.Fatalf("durable authoring draft contains executable redactions: %#v payload=%s", requirements, payload)
	}
}

func TestAuthoringSessionCreateAndUpdateEnforceSecretBindingBoundary(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const projectID = "project-authoring-secrets"
	seedAuthoringScriptProjects(t, store, projectID)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	const createCredential = "authoring-create-credential-preserved"
	const createReviewSecret = "authoring-create-review-preserved"
	const createReviewStateSecret = "authoring-create-review-state-never-store"
	createdResponse := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", map[string]any{
		"project_id": projectID, "source": "test", "name": "Unredacted draft create", "status": "editing",
		"draft": authoringSecretDraft(projectID, "Bearer "+createCredential, createReviewSecret),
		"review_state": map[string]any{
			"active_tab":            "runtime",
			"approval_ready":        true,
			"selected_action_index": 2,
			"review_filters": map[string]any{
				"section": "actions",
				"labels":  []any{"credential-review", "correlation"},
			},
			"password": createReviewStateSecret,
		},
	})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if encoded := createdResponse.Body.String(); strings.Contains(encoded, createReviewStateSecret) {
		t.Fatalf("authoring create response disclosed review-state credential: %s", encoded)
	}
	if got := persisted.ReviewState["password"]; got != maskedScriptSecret {
		t.Fatalf("persisted review-state password = %#v, want redacted", got)
	}
	if got := persisted.ReviewState["active_tab"]; got != "runtime" {
		t.Fatalf("persisted review-state active_tab = %#v, want runtime", got)
	}
	if got := persisted.ReviewState["approval_ready"]; got != true {
		t.Fatalf("persisted review-state approval_ready = %#v, want true", got)
	}
	if got := persisted.ReviewState["selected_action_index"]; got != float64(2) {
		t.Fatalf("persisted review-state selected_action_index = %#v, want 2", got)
	}
	filters, _ := persisted.ReviewState["review_filters"].(map[string]any)
	if filters["section"] != "actions" {
		t.Fatalf("persisted review-state filters = %#v, want ordinary workflow metadata preserved", filters)
	}
	if got := persisted.Draft.Actions[0].Headers["Authorization"]; got != "Bearer "+createCredential {
		t.Fatalf("persisted Authorization = %q, want unredacted literal", got)
	}
	if draftJSON, _ := json.Marshal(persisted.Draft); !strings.Contains(string(draftJSON), createReviewSecret) {
		t.Fatalf("persisted source_review lost unredacted value: %s", draftJSON)
	}

	const updateCredential = "authoring-update-credential-preserved"
	const updateReviewSecret = "authoring-update-review-preserved"
	const updateReviewStateSecret = "authoring-update-review-state-never-store"
	updatedResponse := authoringSecretRequest(t, server, http.MethodPut, "/api/v1/scripts/authoring/sessions/"+created.ID, map[string]any{
		"project_id": projectID, "source": "test", "name": "Unredacted draft update", "status": "ready",
		"draft": authoringSecretDraft(projectID, "Bearer "+updateCredential, updateReviewSecret),
		"review_state": map[string]any{
			"active_tab": "parameters",
			"nested":     map[string]any{"client_secret": updateReviewStateSecret},
		},
	})
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", updatedResponse.Code, updatedResponse.Body.String())
	}
	persisted, err = store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if encoded := updatedResponse.Body.String(); strings.Contains(encoded, updateReviewStateSecret) {
		t.Fatalf("authoring update response disclosed review-state credential: %s", encoded)
	}
	nested, _ := persisted.ReviewState["nested"].(map[string]any)
	if got := nested["client_secret"]; got != maskedScriptSecret {
		t.Fatalf("persisted nested review-state secret = %#v, want redacted", got)
	}
	if got := persisted.Draft.Actions[0].Headers["Authorization"]; got != "Bearer "+updateCredential {
		t.Fatalf("updated Authorization = %q, want unredacted literal", got)
	}
	if draftJSON, _ := json.Marshal(persisted.Draft); !strings.Contains(string(draftJSON), updateReviewSecret) {
		t.Fatalf("updated source_review lost unredacted value: %s", draftJSON)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{ProjectID: projectID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{createReviewStateSecret, updateReviewStateSecret} {
		if strings.Contains(string(encodedEvents), forbidden) {
			t.Fatalf("authoring audit events disclosed review-state credential %q: %s", forbidden, encodedEvents)
		}
	}
}

func TestAuthoringSessionApprovalPromotesOnlyDisclosureSafeDraft(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const projectID = "project-authoring-approval-secrets"
	seedAuthoringScriptProjects(t, store, projectID)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	const literalCredential = "authoring-approval-credential-preserved"
	const reviewSecret = "approval-review-secret-preserved"
	now := time.Now().UTC()
	session := &storage.AuthoringSession{
		ID: "authoring-unredacted-approval", ProjectID: projectID, Source: "test", Name: "Unredacted approval", Status: "ready",
		Draft:     authoringSecretDraft(projectID, "Bearer "+literalCredential, reviewSecret),
		CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}
	if err := store.SaveAuthoringSession(session); err != nil {
		t.Fatal(err)
	}
	approvePath := "/api/v1/scripts/authoring/sessions/" + session.ID + "/approve"
	approvedResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(approvedResponse, httptest.NewRequest(http.MethodPost, approvePath, nil))
	if approvedResponse.Code != http.StatusOK {
		t.Fatalf("approval status = %d body=%s", approvedResponse.Code, approvedResponse.Body.String())
	}
	stored, exists := server.runner.GetScript(authoringApprovalScriptID(session.ID))
	if !exists || stored == nil {
		t.Fatal("authoring draft was not promoted")
	}
	if got := stored.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+literalCredential {
		t.Fatalf("approved Authorization = %q, want %q", got, "Bearer "+literalCredential)
	}
	if draftJSON, _ := json.Marshal(stored.Script); !strings.Contains(string(draftJSON), reviewSecret) {
		t.Fatalf("approved script lost source_review: %s", draftJSON)
	}
}

func TestAIDraftFromLegacyScriptCannotPersistPlaintextAuthoringSession(t *testing.T) {
	server := NewServer(ServerConfig{})
	const credential = "legacy-ai-source-credential-preserved"
	legacy := durableBoundaryScript("legacy-ai-source", "Bearer "+credential, "legacy-ai-review")
	seedLegacySecretScript(server, legacy)

	response := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/ai/drafts", map[string]any{
		"project_id": legacy.ProjectID, "provider": mockedAIProvider, "source_type": "existing_script",
		"existing_script_id": legacy.ID, "name": "Unredacted local AI copy",
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("AI draft status = %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), credential) {
		t.Fatalf("local AI draft response stripped credential: %s", response.Body.String())
	}
	server.authoringMu.Lock()
	defer server.authoringMu.Unlock()
	if len(server.authoring) != 1 {
		t.Fatalf("expected 1 local AI authoring session, got: %#v", server.authoring)
	}
}
