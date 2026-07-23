package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func combinedJavaVuserGovernanceDraft(projectID string) *dsl.Script {
	const source = "public class Actions { public int action() { return 0; } }"
	sourceManifest := enterprisearchive.BuildJavaVuserSourceManifest(map[string]string{
		"Actions.java": source,
	})
	return &dsl.Script{
		Name:          "Combined Java Vuser",
		Version:       "1.0.0",
		Protocol:      "java_vuser",
		ProjectID:     projectID,
		GeneratedBy:   "apexload-enterprise-script-archive-java-importer",
		GeneratedFrom: "enterprise_script_archive",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				"source_archive_ref": "archive-java-1",
				"fidelity_report": &enterprisearchive.ImportFidelityReport{
					PromotionStatus: enterprisearchive.ImportPromotionPromotable,
				},
				"source_review": &enterprisearchive.SourceReview{
					SourceFiles: []enterprisearchive.SourceReviewFile{{
						Path:          "Actions.java",
						Language:      "java",
						Content:       source,
						OriginalBytes: len([]byte(source)),
					}},
					TotalSourceFiles: 1,
				},
				"review_artifact": &enterprisearchive.ReviewArtifact{
					Metadata: map[string]any{
						enterprisearchive.JavaVuserSourceManifestMetadataKey: sourceManifest,
					},
				},
			},
		}},
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			HarnessJar:      "runners/java_vuser/apexload-java-vuser-runner.jar",
			MaxProcesses:    25,
			ActionTimeoutMs: 30000,
		}},
		Init: []dsl.Action{{
			Name: "Java init", Type: "java_vuser_run", Target: "Actions",
			Params: map[string]any{"method": "init"},
		}},
		Actions: []dsl.Action{{
			Name: "Java action", Type: "java_vuser_run", Target: "Actions",
			Params: map[string]any{"method": "action"},
		}},
		Teardown: []dsl.Action{{
			Name: "Java end", Type: "java_vuser_run", Target: "Actions",
			Params: map[string]any{"method": "end"},
		}},
	}
}

func approveCombinedJavaVuserThroughAPI(t *testing.T, server *Server, projectID string) *authoringSessionApprovalResponse {
	t.Helper()
	return approveJavaVuserDraftThroughAPI(t, server, projectID, combinedJavaVuserGovernanceDraft(projectID))
}

func approveJavaVuserDraftThroughAPI(t *testing.T, server *Server, projectID string, draft *dsl.Script) *authoringSessionApprovalResponse {
	t.Helper()
	approveResp := attemptJavaVuserDraftApprovalThroughAPI(t, server, projectID, "enterprise_script_archive", draft)
	if approveResp.Code != http.StatusOK {
		t.Fatalf("approve authoring session status = %d body=%s", approveResp.Code, approveResp.Body.String())
	}
	var approved authoringSessionApprovalResponse
	if err := json.Unmarshal(approveResp.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode authoring approval: %v", err)
	}
	if approved.Script == nil || approved.Script.Script == nil {
		t.Fatalf("approved response has no script: %#v", approved)
	}
	return &approved
}

func attemptJavaVuserDraftApprovalThroughAPI(t *testing.T, server *Server, projectID, source string, draft *dsl.Script) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"project_id": projectID,
		"source":     source,
		"name":       "Combined Java approval",
		"status":     "ready",
		"draft":      draft,
	})
	if err != nil {
		t.Fatalf("marshal authoring session: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create authoring session status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode authoring session: %v", err)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/approve", nil)
	approveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approveResp, approveReq)
	return approveResp
}

func newJavaVuserGovernanceServer(t *testing.T, projectID string) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedAuthoringScriptProjects(t, store, projectID)
	return NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
}

func TestCombinedJavaVuserAuthoringSessionCreateIsIdempotent(t *testing.T) {
	const (
		projectID      = "project-java-idempotent-create"
		idempotencyKey = "enterprise-archive-review:item-1:7:0"
	)
	server := newJavaVuserGovernanceServer(t, projectID)
	requestBody, err := json.Marshal(map[string]any{
		"project_id":      projectID,
		"source":          "source_review_artifact",
		"name":            "Combined Java approval",
		"status":          "reviewing",
		"idempotency_key": idempotencyKey,
		"draft":           combinedJavaVuserGovernanceDraft(projectID),
		"review_state": map[string]any{
			"review_artifact": map[string]any{"id": "java-review-1"},
		},
	})
	if err != nil {
		t.Fatalf("marshal authoring request: %v", err)
	}

	create := func(body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	first := create(requestBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
	}
	second := create(requestBody)
	if second.Code != http.StatusOK {
		t.Fatalf("idempotent retry status=%d body=%s", second.Code, second.Body.String())
	}

	var firstSession, secondSession storage.AuthoringSession
	if err := json.Unmarshal(first.Body.Bytes(), &firstSession); err != nil {
		t.Fatalf("decode first session: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondSession); err != nil {
		t.Fatalf("decode second session: %v", err)
	}
	if firstSession.ID == "" || firstSession.ID != secondSession.ID {
		t.Fatalf("session IDs first=%q second=%q, want one stable session", firstSession.ID, secondSession.ID)
	}
	if _, leaked := firstSession.ReviewState[authoringSessionIdempotencyReviewKey]; leaked {
		t.Fatalf("server idempotency envelope leaked in API response: %#v", firstSession.ReviewState)
	}
	sessions, err := server.listAuthoringSessions(projectID)
	if err != nil {
		t.Fatalf("listAuthoringSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != firstSession.ID {
		t.Fatalf("sessions=%#v, want one idempotently-created session", sessions)
	}
	envelope, _ := sessions[0].ReviewState[authoringSessionIdempotencyReviewKey].(map[string]any)
	if strings.TrimSpace(fmt.Sprint(envelope["key_hash"])) != authoringSessionIdempotencyKeyHash(idempotencyKey) ||
		strings.TrimSpace(fmt.Sprint(envelope["request_hash"])) == "" {
		t.Fatalf("persisted session has invalid idempotency evidence: %#v", sessions[0].ReviewState)
	}

	updateBody, err := json.Marshal(map[string]any{
		"project_id": projectID,
		"source":     "source_review_artifact",
		"name":       "Edited after create",
		"status":     "reviewing",
		"draft":      combinedJavaVuserGovernanceDraft(projectID),
	})
	if err != nil {
		t.Fatalf("marshal authoring update: %v", err)
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/authoring/sessions/"+firstSession.ID, bytes.NewReader(updateBody))
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update idempotent session status=%d body=%s", updateResp.Code, updateResp.Body.String())
	}
	retryAfterEdit := create(requestBody)
	if retryAfterEdit.Code != http.StatusOK {
		t.Fatalf("idempotent retry after edit status=%d body=%s", retryAfterEdit.Code, retryAfterEdit.Body.String())
	}

	var changed map[string]any
	if err := json.Unmarshal(requestBody, &changed); err != nil {
		t.Fatalf("decode request for key-reuse test: %v", err)
	}
	changed["name"] = "Different review"
	changedBody, err := json.Marshal(changed)
	if err != nil {
		t.Fatalf("marshal changed request: %v", err)
	}
	conflict := create(changedBody)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "different authoring-session request") {
		t.Fatalf("changed key reuse status=%d body=%s, want conflict", conflict.Code, conflict.Body.String())
	}
}

func TestJavaVuserApprovalEvidenceProjectionPreservesOnlyGovernanceEnvelope(t *testing.T) {
	script := combinedJavaVuserGovernanceDraft("project-java")
	script.Metadata.Import.Preserved["ordinary"] = map[string]any{
		"session_id": "session-cookie-value",
	}
	const sessionID = "f4edbc2a-4f27-4b28-b09b-a5fcdd2bb83f"
	stampJavaVuserAuthoringApproval(script, sessionID)

	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	evidence, ok := readJavaVuserAuthoringApproval(projected)
	if !ok || evidence.sessionID != sessionID || evidence.contentHash == "" {
		t.Fatalf("approval evidence = %#v, want intact server governance envelope", evidence)
	}
	ordinary, _ := projected.Metadata.Import.Preserved["ordinary"].(map[string]any)
	if ordinary["session_id"] != maskedScriptSecret {
		t.Fatalf("ordinary session_id = %#v, want %q", ordinary["session_id"], maskedScriptSecret)
	}
}

func TestApprovedCombinedJavaVuserSurvivesPersistenceAndLaunchPreflight(t *testing.T) {
	const projectID = "project-java-approval"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)

	persisted, found, err := server.runner.GetScriptDurable(approved.Script.ID)
	if err != nil || !found || persisted == nil {
		t.Fatalf("GetScriptDurable: found=%v script=%#v err=%v", found, persisted, err)
	}
	evidence, ok := readJavaVuserAuthoringApproval(persisted.Script)
	if !ok || evidence.sessionID == "" || evidence.sessionID == maskedScriptSecret {
		t.Fatalf("persisted approval evidence = %#v, want durable authoring session provenance", evidence)
	}
	if conflict := server.runner.javaVuserApprovalConflict(persisted.Script); conflict != "" {
		t.Fatalf("persisted approved script was rejected: %s", conflict)
	}

	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	setPreflightJavaVuserInventory(server.runner, protocol.FeatureJavaVuserPersistentLifecycleV1)
	preflight, err := server.runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name:      "Approved combined Java",
		ProjectID: projectID,
		Duration:  "5m",
		ScriptID:  approved.Script.ID,
		VUsers:    1,
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch: %v", err)
	}
	if !preflight.StartNowAllowed || preflight.Decision != "start_now" {
		t.Fatalf("preflight = %#v, want approved combined script to pass launch governance", preflight)
	}
}

func TestApprovedCombinedJavaVuserAuthoringEvidenceCannotBeDeleted(t *testing.T) {
	const projectID = "project-java-retained-approval"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	evidence, ok := readJavaVuserAuthoringApproval(approved.Script.Script)
	if !ok || evidence.sessionID == "" {
		t.Fatalf("approval evidence = %#v, want authoring session id", evidence)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/authoring/sessions/"+evidence.sessionID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusConflict ||
		!strings.Contains(deleteResp.Body.String(), "retained for approval verification") {
		t.Fatalf("delete approved evidence status=%d body=%s, want retained-evidence conflict", deleteResp.Code, deleteResp.Body.String())
	}

	persisted, found, err := server.runner.GetScriptDurable(approved.Script.ID)
	if err != nil || !found || persisted == nil {
		t.Fatalf("GetScriptDurable after delete attempt: found=%v script=%#v err=%v", found, persisted, err)
	}
	if conflict := server.runner.javaVuserApprovalConflict(persisted.Script); conflict != "" {
		t.Fatalf("retained approved script was rejected after delete attempt: %s", conflict)
	}
}

func TestApprovedCombinedJavaVuserRetryRejectsReusedLibraryScriptID(t *testing.T) {
	const projectID = "project-java-reused-approval-id"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	evidence, ok := readJavaVuserAuthoringApproval(approved.Script.Script)
	if !ok || evidence.sessionID == "" {
		t.Fatalf("approval evidence = %#v, want authoring session id", evidence)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+approved.Script.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete approved script status=%d body=%s", deleteResp.Code, deleteResp.Body.String())
	}

	replacement := &dsl.Script{
		ID:        approved.Script.ID,
		Name:      "Unrelated HTTP replacement",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://replacement.example.test"},
		Actions:   []dsl.Action{{Name: "Replacement", Type: "request", Target: "/"}},
	}
	createBody, err := json.Marshal(map[string]any{"project_id": projectID, "script": replacement})
	if err != nil {
		t.Fatalf("marshal replacement: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(createBody))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create replacement status=%d body=%s", createResp.Code, createResp.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+evidence.sessionID+"/approve", nil)
	retryResp := httptest.NewRecorder()
	server.mux.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusConflict ||
		!strings.Contains(retryResp.Body.String(), "no longer matches its Java Vuser library script") {
		t.Fatalf("retry approval status=%d body=%s, want reused-id conflict", retryResp.Code, retryResp.Body.String())
	}
}

func TestApprovedCombinedJavaVuserDefaultsVersionBeforeApprovalHash(t *testing.T) {
	const projectID = "project-java-default-version"
	server := newJavaVuserGovernanceServer(t, projectID)
	draft := combinedJavaVuserGovernanceDraft(projectID)
	draft.Version = ""

	approved := approveJavaVuserDraftThroughAPI(t, server, projectID, draft)
	if approved.Script.Script.Version != "1" {
		t.Fatalf("approved version = %q, want persistence default 1", approved.Script.Script.Version)
	}
	persisted, found, err := server.runner.GetScriptDurable(approved.Script.ID)
	if err != nil || !found || persisted == nil {
		t.Fatalf("GetScriptDurable: found=%v script=%#v err=%v", found, persisted, err)
	}
	if conflict := server.runner.javaVuserApprovalConflict(persisted.Script); conflict != "" {
		t.Fatalf("defaulted version invalidated approval hash: %s", conflict)
	}
}

func TestAuthoringApprovalRejectsImportedJavaVuserWithStrippedReviewEvidence(t *testing.T) {
	const projectID = "project-java-import-evidence"
	tests := []struct {
		name        string
		mutate      func(*dsl.Script)
		wantMessage string
	}{
		{
			name: "fidelity report removed",
			mutate: func(draft *dsl.Script) {
				delete(draft.Metadata.Import.Preserved, "fidelity_report")
			},
			wantMessage: "missing its fidelity report",
		},
		{
			name: "all client script provenance removed",
			mutate: func(draft *dsl.Script) {
				draft.GeneratedBy = ""
				draft.GeneratedFrom = ""
				draft.Metadata = nil
			},
			wantMessage: "missing its fidelity report",
		},
		{
			name: "combined draft relabeled as legacy one shot",
			mutate: func(draft *dsl.Script) {
				draft.GeneratedBy = ""
				draft.GeneratedFrom = ""
				draft.Metadata = nil
				draft.Init = nil
				draft.Teardown = nil
				draft.Actions = []dsl.Action{{
					Name: "Legacy lifecycle", Type: "java_vuser_run", Target: "Actions",
					Params: map[string]any{"method": "full_lifecycle"},
				}}
			},
			wantMessage: "missing its fidelity report",
		},
		{
			name: "fidelity status removed",
			mutate: func(draft *dsl.Script) {
				draft.Metadata.Import.Preserved["fidelity_report"] = map[string]any{}
			},
			wantMessage: "invalid fidelity-report status",
		},
		{
			name: "source review removed",
			mutate: func(draft *dsl.Script) {
				delete(draft.Metadata.Import.Preserved, "source_review")
			},
			wantMessage: "missing its preserved Java source review",
		},
		{
			name: "full-source manifest removed",
			mutate: func(draft *dsl.Script) {
				artifact := draft.Metadata.Import.Preserved["review_artifact"].(*enterprisearchive.ReviewArtifact)
				delete(artifact.Metadata, enterprisearchive.JavaVuserSourceManifestMetadataKey)
			},
			wantMessage: "missing a valid immutable full-source manifest",
		},
		{
			name: "full-source manifest digest changed",
			mutate: func(draft *dsl.Script) {
				artifact := draft.Metadata.Import.Preserved["review_artifact"].(*enterprisearchive.ReviewArtifact)
				manifest := artifact.Metadata[enterprisearchive.JavaVuserSourceManifestMetadataKey].(*enterprisearchive.JavaVuserSourceManifest)
				manifest.Digest = "sha256:" + strings.Repeat("0", 64)
			},
			wantMessage: "missing a valid immutable full-source manifest",
		},
		{
			name: "source review path no longer matches manifest",
			mutate: func(draft *dsl.Script) {
				draft.Metadata.Import.Preserved["source_review"] = &enterprisearchive.SourceReview{
					SourceFiles: []enterprisearchive.SourceReviewFile{{
						Path: "Replacement.java", Language: "java", Content: "public class Replacement {}",
					}},
					TotalSourceFiles: 1,
				}
			},
			wantMessage: "source-review paths do not match",
		},
		{
			name: "source review byte count stripped",
			mutate: func(draft *dsl.Script) {
				review := draft.Metadata.Import.Preserved["source_review"].(*enterprisearchive.SourceReview)
				review.SourceFiles[0].OriginalBytes = 0
			},
			wantMessage: "source-review byte counts do not match",
		},
		{
			name: "source review byte count changed",
			mutate: func(draft *dsl.Script) {
				review := draft.Metadata.Import.Preserved["source_review"].(*enterprisearchive.SourceReview)
				review.SourceFiles[0].OriginalBytes++
			},
			wantMessage: "source-review byte counts do not match",
		},
		{
			name: "source review counts partially stripped",
			mutate: func(draft *dsl.Script) {
				draft.Metadata.Import.Preserved["source_review"] = &enterprisearchive.SourceReview{
					SourceFiles: []enterprisearchive.SourceReviewFile{{
						Path: "Actions.java", Language: "java", Content: "public class Actions {}",
					}},
					TotalSourceFiles:   3,
					OmittedSourceFiles: []string{"runtime.cfg"},
				}
			},
			wantMessage: "source-review file counts are incomplete",
		},
		{
			name: "promotable status cannot hide concrete blocker",
			mutate: func(draft *dsl.Script) {
				draft.Metadata.Import.Preserved["fidelity_report"] = &enterprisearchive.ImportFidelityReport{
					PromotionStatus: enterprisearchive.ImportPromotionPromotable,
					Blockers:        []string{"Selected Actions class is not instantiable."},
				}
			},
			wantMessage: "Selected Actions class is not instantiable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newJavaVuserGovernanceServer(t, projectID)
			draft := combinedJavaVuserGovernanceDraft(projectID)
			test.mutate(draft)
			response := attemptJavaVuserDraftApprovalThroughAPI(t, server, projectID, "source_review_artifact", draft)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.wantMessage) {
				t.Fatalf("approval status = %d body=%s, want blocked imported evidence containing %q", response.Code, response.Body.String(), test.wantMessage)
			}
			if scripts := server.runner.ListScripts(); len(scripts) != 0 {
				t.Fatalf("stripped imported evidence persisted scripts: %#v", scripts)
			}
		})
	}
}

func TestAuthoringApprovalAllowsBoundedJavaSourcePreviewBoundToFullManifest(t *testing.T) {
	const projectID = "project-java-bounded-review"

	tests := []struct {
		name  string
		files map[string]string
		check func(*testing.T, *enterprisearchive.SourceReview)
	}{
		{
			name: "source larger than bounded preview",
			files: map[string]string{
				"Actions.java": "public class Actions { public int action() { return 0; } }\n/*" +
					strings.Repeat("review filler ", 5000) + "*/",
			},
			check: func(t *testing.T, review *enterprisearchive.SourceReview) {
				t.Helper()
				if len(review.SourceFiles) != 1 || !review.SourceFiles[0].Truncated {
					t.Fatalf("source review = %#v, want one truncated bounded preview", review)
				}
			},
		},
		{
			name: "more Java files than bounded preview",
			files: func() map[string]string {
				files := map[string]string{
					"Actions.java": "public class Actions { public int action() { return 0; } }",
				}
				for index := 0; index < 22; index++ {
					name := fmt.Sprintf("Helper%02d.java", index)
					files[name] = fmt.Sprintf("public class Helper%02d {}", index)
				}
				return files
			}(),
			check: func(t *testing.T, review *enterprisearchive.SourceReview) {
				t.Helper()
				if len(review.OmittedSourceFiles) == 0 || review.TotalSourceFiles <= len(review.SourceFiles) {
					t.Fatalf("source review = %#v, want explicitly omitted bounded-preview files", review)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newJavaVuserGovernanceServer(t, projectID)
			draft := combinedJavaVuserGovernanceDraft(projectID)
			review := enterprisearchive.BuildSourceReview(test.files, nil)
			test.check(t, review)
			draft.Metadata.Import.Preserved["source_review"] = review
			draft.Metadata.Import.Preserved["review_artifact"] = &enterprisearchive.ReviewArtifact{
				Metadata: map[string]any{
					enterprisearchive.JavaVuserSourceManifestMetadataKey: enterprisearchive.BuildJavaVuserSourceManifest(test.files),
				},
			}

			response := attemptJavaVuserDraftApprovalThroughAPI(t, server, projectID, "enterprise_script_archive", draft)
			if response.Code != http.StatusOK {
				t.Fatalf("bounded Java preview approval status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAuthoringApprovalAllowsHandAuthoredJavaWithoutImportFidelity(t *testing.T) {
	const projectID = "project-java-hand-authored"
	server := newJavaVuserGovernanceServer(t, projectID)
	draft := combinedJavaVuserGovernanceDraft(projectID)
	draft.GeneratedBy = ""
	draft.GeneratedFrom = ""
	draft.Metadata = nil

	response := attemptJavaVuserDraftApprovalThroughAPI(t, server, projectID, "manual", draft)
	if response.Code != http.StatusOK {
		t.Fatalf("hand-authored approval status = %d body=%s, want approval without import fidelity evidence", response.Code, response.Body.String())
	}
}

func TestJavaVuserPUTCannotSupplyOrInvalidateApprovalEvidence(t *testing.T) {
	const projectID = "project-java-update"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)

	t.Run("unchanged approved script retains server evidence", func(t *testing.T) {
		originalEvidence, ok := readJavaVuserAuthoringApproval(approved.Script.Script)
		if !ok {
			t.Fatal("approved response has no approval evidence")
		}
		incoming := cloneScriptDefinition(approved.Script.Script)
		writeJavaVuserAuthoringApproval(incoming, javaVuserApprovalEvidence{
			sessionID:   "fbc3b494-0f47-4e97-9805-939cad94db74",
			contentHash: "sha256:" + strings.Repeat("0", sha256HexLength),
		})
		body, err := json.Marshal(map[string]any{"script": incoming})
		if err != nil {
			t.Fatalf("marshal update: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+approved.Script.ID, bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("PUT status = %d body=%s", resp.Code, resp.Body.String())
		}
		persisted, found, err := server.runner.GetScriptDurable(approved.Script.ID)
		if err != nil || !found {
			t.Fatalf("GetScriptDurable: found=%v err=%v", found, err)
		}
		persistedEvidence, ok := readJavaVuserAuthoringApproval(persisted.Script)
		if !ok || persistedEvidence != originalEvidence {
			t.Fatalf("persisted evidence = %#v, want server-owned %#v", persistedEvidence, originalEvidence)
		}
		if conflict := server.runner.javaVuserApprovalConflict(persisted.Script); conflict != "" {
			t.Fatalf("unchanged update lost approval: %s", conflict)
		}
	})

	t.Run("content change with copied evidence is rejected", func(t *testing.T) {
		edited := cloneScriptDefinition(approved.Script.Script)
		edited.Actions[0].Target = "DifferentActions"
		body, err := json.Marshal(map[string]any{"script": edited})
		if err != nil {
			t.Fatalf("marshal update: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+approved.Script.ID, bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("PUT status = %d body=%s, want 409", resp.Code, resp.Body.String())
		}
	})

	t.Run("metadata omission is rejected", func(t *testing.T) {
		edited := cloneScriptDefinition(approved.Script.Script)
		edited.Metadata = nil
		body, err := json.Marshal(map[string]any{"script": edited})
		if err != nil {
			t.Fatalf("marshal update: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+approved.Script.ID, bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("PUT status = %d body=%s, want 409", resp.Code, resp.Body.String())
		}
	})

	t.Run("protocol replacement is rejected", func(t *testing.T) {
		replacement := &dsl.Script{
			ID:        approved.Script.ID,
			Name:      "HTTP replacement",
			Version:   "1.0.0",
			Protocol:  "http",
			ProjectID: projectID,
			Config:    dsl.ScriptConfig{BaseURL: "https://replacement.example.test"},
			Actions:   []dsl.Action{{Name: "Replacement", Type: "request", Target: "/"}},
		}
		body, err := json.Marshal(map[string]any{"script": replacement})
		if err != nil {
			t.Fatalf("marshal replacement: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+approved.Script.ID, bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("PUT status = %d body=%s, want 409", resp.Code, resp.Body.String())
		}
	})
}

func TestJavaVuserPUTCannotConvertOrdinaryScriptWithCopiedApproval(t *testing.T) {
	const projectID = "project-java-convert"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	ordinary, err := server.runner.StoreScriptDurable(&dsl.Script{
		Name: "Ordinary", Version: "1.0.0", Protocol: "http", ProjectID: projectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Home", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}

	forged := cloneScriptDefinition(approved.Script.Script)
	forged.ID = ordinary.ID
	forged.Name = ordinary.Name
	body, err := json.Marshal(map[string]any{"script": forged})
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+ordinary.ID, bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("PUT status = %d body=%s, want 409", resp.Code, resp.Body.String())
	}
	persisted, found, err := server.runner.GetScriptDurable(ordinary.ID)
	if err != nil || !found {
		t.Fatalf("GetScriptDurable: found=%v err=%v", found, err)
	}
	if persisted.Protocol != "http" {
		t.Fatalf("ordinary script protocol = %q, want unchanged http", persisted.Protocol)
	}
}

func TestJavaVuserLegacyRunnerCannotBypassAuthoringApproval(t *testing.T) {
	const projectID = "project-java-legacy-governance"
	server := newJavaVuserGovernanceServer(t, projectID)

	t.Run("direct create is rejected", func(t *testing.T) {
		legacy := &dsl.Script{
			Name:      "Unreviewed legacy Java",
			Version:   "1.0.0",
			Protocol:  "java_vuser",
			ProjectID: projectID,
			Actions: []dsl.Action{{
				Name:   "Legacy lifecycle",
				Type:   "java_vuser_run",
				Target: "UnreviewedActions",
				Params: map[string]any{"method": "full_lifecycle"},
			}},
		}
		body, err := json.Marshal(map[string]any{"script": legacy})
		if err != nil {
			t.Fatalf("marshal direct create: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "authoring-session review") {
			t.Fatalf("POST status = %d body=%s, want authoring approval conflict", resp.Code, resp.Body.String())
		}
	})

	t.Run("approved phase script cannot be rewritten as legacy", func(t *testing.T) {
		approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
		rewritten := cloneScriptDefinition(approved.Script.Script)
		rewritten.Init = nil
		rewritten.Teardown = nil
		rewritten.Actions = []dsl.Action{{
			Name:   "Legacy lifecycle",
			Type:   "java_vuser_run",
			Target: "DifferentActions",
			Params: map[string]any{"method": "full_lifecycle"},
		}}
		body, err := json.Marshal(map[string]any{"script": rewritten})
		if err != nil {
			t.Fatalf("marshal legacy rewrite: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+approved.Script.ID, bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("PUT status = %d body=%s, want 409", resp.Code, resp.Body.String())
		}
	})
}

func TestUntrustedDurableWritersCannotForgeJavaVuserApprovalOrGrandfathering(t *testing.T) {
	const projectID = "project-java-durable-boundary"
	server := newJavaVuserGovernanceServer(t, projectID)
	legacy := &dsl.Script{
		ID:        "new-legacy-java",
		Name:      "New legacy Java",
		Version:   "1.0.0",
		Protocol:  "java_vuser",
		ProjectID: projectID,
		Actions: []dsl.Action{{
			Name:   "Legacy lifecycle",
			Type:   "java_vuser_run",
			Target: "UnreviewedActions",
			Params: map[string]any{"method": "full_lifecycle"},
		}},
	}
	if _, err := server.runner.CreateScriptDurable(legacy); err == nil || !strings.Contains(err.Error(), "authoring-session review") {
		t.Fatalf("CreateScriptDurable error = %v, want Java authoring boundary", err)
	}
	if _, err := server.runner.StoreScriptDurable(legacy); err == nil || !strings.Contains(err.Error(), "authoring-session review") {
		t.Fatalf("StoreScriptDurable error = %v, want Java authoring boundary", err)
	}

	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	persisted, found, err := server.runner.GetScriptDurable(approved.Script.ID)
	if err != nil || !found || persisted == nil {
		t.Fatalf("GetScriptDurable: found=%v script=%#v err=%v", found, persisted, err)
	}
	evidence, ok := readJavaVuserAuthoringApproval(persisted.Script)
	if !ok {
		t.Fatal("approved script has no authoring evidence")
	}
	forged := cloneScriptDefinition(persisted.Script)
	forged.Actions[0].Target = "ForgedRemoteActions"
	writeJavaVuserAuthoringApproval(forged, javaVuserApprovalEvidence{
		sessionID:   evidence.sessionID,
		contentHash: hashLibraryScript(forged),
	})
	if _, _, err := server.runner.UpdateScriptDurableIfUnchanged(forged, persisted.Script); err == nil ||
		!strings.Contains(err.Error(), "local authoring-session review") {
		t.Fatalf("UpdateScriptDurableIfUnchanged error = %v, want local Java authoring boundary", err)
	}

	nonJavaReplacement := &dsl.Script{
		ID:        persisted.ID,
		Name:      "Remote protocol replacement",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: persisted.ProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://replacement.example.test"},
		Actions:   []dsl.Action{{Name: "Replacement", Type: "request", Target: "/"}},
	}
	if _, _, err := server.runner.UpdateScriptDurableIfUnchanged(nonJavaReplacement, persisted.Script); err == nil ||
		!strings.Contains(err.Error(), "local authoring-session review") {
		t.Fatalf("Java-to-HTTP UpdateScriptDurableIfUnchanged error = %v, want local Java authoring boundary", err)
	}
	forgedExpected := cloneScriptDefinition(persisted.Script)
	forgedExpected.Protocol = "http"
	forgedExpected.Init = nil
	forgedExpected.Actions = nonJavaReplacement.Actions
	forgedExpected.Teardown = nil
	if _, _, err := server.runner.UpdateScriptDurableIfUnchanged(nonJavaReplacement, forgedExpected); err == nil ||
		!strings.Contains(err.Error(), "local authoring-session review") {
		t.Fatalf("Java-to-HTTP update with forged expected version error = %v, want authoritative Java boundary", err)
	}
	if _, err := server.runner.StoreScriptDurable(nonJavaReplacement); err == nil ||
		!strings.Contains(err.Error(), "local authoring-session review") {
		t.Fatalf("Java-to-HTTP StoreScriptDurable error = %v, want local Java authoring boundary", err)
	}
}

func TestJavaVuserExternalRunnerRepairSuggestionIsApprovalGuidance(t *testing.T) {
	result := &enterprisearchive.ConvertResult{
		Script: combinedJavaVuserGovernanceDraft("project-java"),
		FidelityReport: &enterprisearchive.ImportFidelityReport{
			PromotionStatus: enterprisearchive.ImportPromotionPromotable,
		},
		DetectedProtocol: &enterprisearchive.DetectedProtocol{
			CanonicalSourceProtocol: "java_vuser",
			APEXLoadProtocol:        "java_vuser",
			DisplayName:             "LoadRunner Java Vuser",
			ImportMode:              enterprisearchive.ImportModeExternalRunnerRequired,
		},
	}
	suggestions := buildEnterpriseArchiveImportRepairSuggestions(result, nil, enterpriseArchiveImportSummary{})
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want one Java approval suggestion", suggestions)
	}
	suggestion := suggestions[0]
	if suggestion.Severity != "warning" || suggestion.Title != "Approve the combined Java Vuser draft" {
		t.Fatalf("suggestion = %#v, want warning approval guidance", suggestion)
	}
	if strings.Contains(strings.ToLower(suggestion.Description), "not ready to save") ||
		!strings.Contains(strings.ToLower(suggestion.Description), "not conversion loss") {
		t.Fatalf("description = %q, want complete-draft guidance without conversion blocker copy", suggestion.Description)
	}
	if !strings.Contains(suggestion.Action, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!strings.Contains(suggestion.Action, protocol.FeatureJavaVuserSessionFrameTokenV1) {
		t.Fatalf("action = %q, want both persistent-lifecycle and authenticated frame-token capabilities", suggestion.Action)
	}
}

func TestBlockedJavaVuserRepairSuggestionRequiresFidelityResolution(t *testing.T) {
	result := &enterprisearchive.ConvertResult{
		Script: combinedJavaVuserGovernanceDraft("project-java"),
		FidelityReport: &enterprisearchive.ImportFidelityReport{
			PromotionStatus: enterprisearchive.ImportPromotionBlocked,
			Blockers:        []string{"Selected Actions class has no public zero-argument action method."},
		},
		DetectedProtocol: &enterprisearchive.DetectedProtocol{
			CanonicalSourceProtocol: "java_vuser",
			APEXLoadProtocol:        "java_vuser",
			DisplayName:             "LoadRunner Java Vuser",
			ImportMode:              enterprisearchive.ImportModeExternalRunnerRequired,
		},
	}
	suggestions := buildEnterpriseArchiveImportRepairSuggestions(result, nil, enterpriseArchiveImportSummary{})
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want one fidelity blocker", suggestions)
	}
	suggestion := suggestions[0]
	if suggestion.Severity != "blocker" || suggestion.Title != "Resolve combined Java Vuser fidelity blockers" {
		t.Fatalf("suggestion = %#v, want blocked fidelity guidance", suggestion)
	}
	text := strings.ToLower(suggestion.Description + " " + suggestion.Action)
	if strings.Contains(text, "review and approve it") ||
		!strings.Contains(text, "not ready for authoring approval") ||
		!strings.Contains(text, "resolve every named fidelity blocker") {
		t.Fatalf("suggestion = %#v, want review-only fidelity guidance", suggestion)
	}
}

func TestAuthoringApprovalRejectsFidelityBlockedCombinedJavaVuser(t *testing.T) {
	const projectID = "project-java-blocked-approval"
	server := newJavaVuserGovernanceServer(t, projectID)
	draft := combinedJavaVuserGovernanceDraft(projectID)
	draft.Metadata.Import.Preserved["fidelity_report"] = &enterprisearchive.ImportFidelityReport{
		PromotionStatus: enterprisearchive.ImportPromotionBlocked,
		Blockers:        []string{"Selected Actions class has no public zero-argument action method."},
	}
	body, err := json.Marshal(map[string]any{
		"project_id": projectID,
		"source":     "enterprise_script_archive",
		"name":       "Blocked Java review",
		"status":     "reviewing",
		"draft":      draft,
	})
	if err != nil {
		t.Fatalf("marshal authoring session: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create authoring session status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode authoring session: %v", err)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/approve", nil)
	approveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approveResp, approveReq)
	if approveResp.Code != http.StatusBadRequest ||
		!strings.Contains(approveResp.Body.String(), "import fidelity blocker") ||
		!strings.Contains(approveResp.Body.String(), "public zero-argument action") {
		t.Fatalf("approve status = %d body=%s, want fidelity blocker", approveResp.Code, approveResp.Body.String())
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("blocked approval persisted scripts: %#v", scripts)
	}
}
