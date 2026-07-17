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

	const createCredential = "authoring-create-credential-never-store"
	const blockedReviewStateSecret = "blocked-authoring-review-state-never-serialize"
	blocked := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", map[string]any{
		"project_id": projectID, "source": "test", "name": "Blocked create", "status": "editing",
		"draft":        authoringSecretDraft(projectID, "Bearer "+createCredential, "safe-review"),
		"review_state": map[string]any{"password": blockedReviewStateSecret},
	})
	if blocked.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blocked create status = %d body=%s", blocked.Code, blocked.Body.String())
	}
	if strings.Contains(blocked.Body.String(), createCredential) {
		t.Fatalf("blocked create disclosed credential: %s", blocked.Body.String())
	}
	if strings.Contains(blocked.Body.String(), blockedReviewStateSecret) {
		t.Fatalf("blocked create disclosed review-state credential: %s", blocked.Body.String())
	}
	if sessions, err := store.ListAuthoringSessions(projectID); err != nil || len(sessions) != 0 {
		t.Fatalf("blocked create persisted sessions: %#v err=%v", sessions, err)
	}

	const createReviewSecret = "authoring-create-review-never-store"
	const createReviewStateSecret = "authoring-create-review-state-never-store"
	createdResponse := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", map[string]any{
		"project_id": projectID, "source": "test", "name": "Safe create", "status": "editing",
		"draft": authoringSecretDraft(projectID, "Bearer {{secret.LOGIN_TOKEN}}", createReviewSecret),
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
		t.Fatalf("safe create status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAuthoringDraftDisclosureSafe(t, persisted.Draft, createReviewSecret)
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
	if got := persisted.Draft.Actions[0].Headers["Authorization"]; got != "Bearer {{secret.LOGIN_TOKEN}}" {
		t.Fatalf("persisted Authorization = %q", got)
	}

	const updateCredential = "authoring-update-credential-never-store"
	blockedUpdate := authoringSecretRequest(t, server, http.MethodPut, "/api/v1/scripts/authoring/sessions/"+created.ID, map[string]any{
		"project_id": projectID, "source": "test", "name": "Blocked update", "status": "editing",
		"draft": authoringSecretDraft(projectID, "Bearer "+updateCredential, "safe-review"),
	})
	if blockedUpdate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blocked update status = %d body=%s", blockedUpdate.Code, blockedUpdate.Body.String())
	}
	if strings.Contains(blockedUpdate.Body.String(), updateCredential) {
		t.Fatalf("blocked update disclosed credential: %s", blockedUpdate.Body.String())
	}
	persisted, err = store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAuthoringDraftDisclosureSafe(t, persisted.Draft, updateCredential)

	const updateReviewSecret = "authoring-update-review-never-store"
	const updateReviewStateSecret = "authoring-update-review-state-never-store"
	updatedResponse := authoringSecretRequest(t, server, http.MethodPut, "/api/v1/scripts/authoring/sessions/"+created.ID, map[string]any{
		"project_id": projectID, "source": "test", "name": "Safe update", "status": "ready",
		"draft": authoringSecretDraft(projectID, "Bearer {{secret.ROTATED_TOKEN}}", updateReviewSecret),
		"review_state": map[string]any{
			"active_tab": "parameters",
			"nested":     map[string]any{"client_secret": updateReviewStateSecret},
		},
	})
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("safe update status = %d body=%s", updatedResponse.Code, updatedResponse.Body.String())
	}
	persisted, err = store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAuthoringDraftDisclosureSafe(t, persisted.Draft, updateReviewSecret)
	if encoded := updatedResponse.Body.String(); strings.Contains(encoded, updateReviewStateSecret) {
		t.Fatalf("authoring update response disclosed review-state credential: %s", encoded)
	}
	nested, _ := persisted.ReviewState["nested"].(map[string]any)
	if got := nested["client_secret"]; got != maskedScriptSecret {
		t.Fatalf("persisted nested review-state secret = %#v, want redacted", got)
	}
	if got := persisted.Draft.Actions[0].Headers["Authorization"]; got != "Bearer {{secret.ROTATED_TOKEN}}" {
		t.Fatalf("updated Authorization = %q", got)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{ProjectID: projectID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{blockedReviewStateSecret, createReviewStateSecret, updateReviewStateSecret} {
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

	const legacyCredential = "legacy-authoring-credential-never-promote"
	now := time.Now().UTC()
	session := &storage.AuthoringSession{
		ID: "legacy-unsafe-authoring", ProjectID: projectID, Source: "test", Name: "Legacy unsafe", Status: "ready",
		Draft:     authoringSecretDraft(projectID, "Bearer "+legacyCredential, "legacy-review"),
		CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}
	if err := store.SaveAuthoringSession(session); err != nil {
		t.Fatal(err)
	}
	approvePath := "/api/v1/scripts/authoring/sessions/" + session.ID + "/approve"
	blocked := httptest.NewRecorder()
	server.mux.ServeHTTP(blocked, httptest.NewRequest(http.MethodPost, approvePath, nil))
	if blocked.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsafe approval status = %d body=%s", blocked.Code, blocked.Body.String())
	}
	if strings.Contains(blocked.Body.String(), legacyCredential) {
		t.Fatalf("unsafe approval disclosed credential: %s", blocked.Body.String())
	}
	if _, exists := server.runner.GetScript(authoringApprovalScriptID(session.ID)); exists {
		t.Fatal("unsafe authoring draft was promoted")
	}

	const reviewSecret = "approval-review-secret-never-store"
	updated := authoringSecretRequest(t, server, http.MethodPut, "/api/v1/scripts/authoring/sessions/"+session.ID, map[string]any{
		"project_id": projectID, "source": "test", "name": "Safe approval", "status": "ready",
		"draft": authoringSecretDraft(projectID, "Bearer {{secret.APPROVAL_TOKEN}}", reviewSecret),
	})
	if updated.Code != http.StatusOK {
		t.Fatalf("safe update status = %d body=%s", updated.Code, updated.Body.String())
	}
	approvedResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(approvedResponse, httptest.NewRequest(http.MethodPost, approvePath, nil))
	if approvedResponse.Code != http.StatusOK {
		t.Fatalf("safe approval status = %d body=%s", approvedResponse.Code, approvedResponse.Body.String())
	}
	stored, exists := server.runner.GetScript(authoringApprovalScriptID(session.ID))
	if !exists || stored == nil {
		t.Fatal("safe authoring draft was not promoted")
	}
	assertAuthoringDraftDisclosureSafe(t, stored.Script, legacyCredential, reviewSecret)
	if got := stored.Script.Actions[0].Headers["Authorization"]; got != "Bearer {{secret.APPROVAL_TOKEN}}" {
		t.Fatalf("approved Authorization = %q", got)
	}
}

func TestAIDraftFromLegacyScriptCannotPersistPlaintextAuthoringSession(t *testing.T) {
	server := NewServer(ServerConfig{})
	const credential = "legacy-ai-source-credential-never-store"
	legacy := durableBoundaryScript("legacy-ai-source", "Bearer "+credential, "legacy-ai-review")
	seedLegacySecretScript(server, legacy)

	response := authoringSecretRequest(t, server, http.MethodPost, "/api/v1/ai/drafts", map[string]any{
		"project_id": legacy.ProjectID, "provider": mockedAIProvider, "source_type": "existing_script",
		"existing_script_id": legacy.ID, "name": "Safe AI copy",
	})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("AI draft status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), credential) {
		t.Fatalf("AI draft response disclosed legacy credential: %s", response.Body.String())
	}
	server.authoringMu.Lock()
	defer server.authoringMu.Unlock()
	if len(server.authoring) != 0 {
		t.Fatalf("unsafe AI authoring session was persisted: %#v", server.authoring)
	}
}
