package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	aiAuthoringContractSchemaVersion = "apexload.ai_authoring_contract.v1"
	aiPolicySchemaVersion            = "apexload.ai_policy.v1"
	aiDraftSchemaVersion             = "apexload.ai_draft.v1"
	aiRepairSchemaVersion            = "apexload.ai_repair.v1"
	aiExplainSchemaVersion           = "apexload.ai_explain.v1"
	aiRootCauseSchemaVersion         = "apexload.ai_root_cause.v1"
	aiReviewQueueSchemaVersion       = "apexload.ai_review_queue.v1"
	defaultAIProvider                = "deterministic-local"
	mockedAIProvider                 = "mocked-provider"
)

type AIAuthoringContractResponse struct {
	SchemaVersion       string               `json:"schema_version"`
	GeneratedAt         time.Time            `json:"generated_at"`
	Status              string               `json:"status"`
	AllowedSources      []string             `json:"allowed_sources"`
	RequiredEvidence    []string             `json:"required_evidence"`
	ApprovalGates       []string             `json:"approval_gates"`
	AuditEvents         []string             `json:"audit_events"`
	EndPoints           []AIContractEndpoint `json:"endpoints"`
	PromotionRule       string               `json:"promotion_rule"`
	DeterminismContract string               `json:"determinism_contract"`
	PolicyEndpoint      string               `json:"policy_endpoint"`
	ReviewQueueEndpoint string               `json:"review_queue_endpoint"`
}

type AIContractEndpoint struct {
	Method   string   `json:"method"`
	Path     string   `json:"path"`
	Purpose  string   `json:"purpose"`
	Evidence []string `json:"evidence,omitempty"`
}

type AIPolicyResponse struct {
	SchemaVersion         string                     `json:"schema_version"`
	GeneratedAt           time.Time                  `json:"generated_at"`
	ProjectID             string                     `json:"project_id,omitempty"`
	Enabled               bool                       `json:"enabled"`
	Status                string                     `json:"status"`
	DefaultProvider       string                     `json:"default_provider"`
	AllowedProviders      []string                   `json:"allowed_providers"`
	RedactionPosture      string                     `json:"redaction_posture"`
	ArtifactRetentionDays int                        `json:"artifact_retention_days"`
	DisabledProjects      []string                   `json:"disabled_projects,omitempty"`
	ProjectOverride       *AIProjectPolicyOverride   `json:"project_override,omitempty"`
	AuditEvents           []string                   `json:"audit_events"`
	Controls              []AIProjectPolicyControl   `json:"controls"`
	ExportEndpoints       []string                   `json:"export_endpoints"`
	ProviderCapabilities  []AIProviderCapabilityCard `json:"provider_capabilities"`
}

type AIProjectPolicyOverride struct {
	ProjectID string `json:"project_id"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason,omitempty"`
}

type AIProjectPolicyControl struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type AIProviderCapabilityCard struct {
	Provider     string   `json:"provider"`
	Status       string   `json:"status"`
	Capabilities []string `json:"capabilities"`
	Notes        []string `json:"notes,omitempty"`
}

type AIDraftRequest struct {
	ProjectID        string          `json:"project_id,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	SourceType       string          `json:"source_type"`
	SourceName       string          `json:"source_name,omitempty"`
	Name             string          `json:"name,omitempty"`
	Text             string          `json:"text,omitempty"`
	Prompt           string          `json:"prompt,omitempty"`
	SourcePayload    json.RawMessage `json:"source_payload,omitempty"`
	ExistingScriptID string          `json:"existing_script_id,omitempty"`
	CreateSession    *bool           `json:"create_session,omitempty"`
}

type AIDraftResponse struct {
	SchemaVersion   string                   `json:"schema_version"`
	GeneratedAt     time.Time                `json:"generated_at"`
	Provider        string                   `json:"provider"`
	ProjectID       string                   `json:"project_id,omitempty"`
	SourceType      string                   `json:"source_type"`
	Draft           *dsl.Script              `json:"draft"`
	Validation      scriptValidationResponse `json:"validation"`
	Guardrails      *AIGuardrailReport       `json:"guardrails,omitempty"`
	AuthoringHealth AuthoringHealth          `json:"authoring_health"`
	ReviewQueueItem *AIReviewQueueItem       `json:"review_queue_item,omitempty"`
	NextActions     []string                 `json:"next_actions,omitempty"`
	Citations       []AICitation             `json:"citations,omitempty"`
	Determinism     AIDeterminismDeclaration `json:"determinism"`
	Policy          AIWorkflowPolicySnapshot `json:"policy"`
	RepairHints     []AIRepairSuggestion     `json:"repair_hints,omitempty"`
}

type AIDeterminismDeclaration struct {
	Mode        string   `json:"mode"`
	FixtureKey  string   `json:"fixture_key"`
	StableInput []string `json:"stable_input"`
}

type AIWorkflowPolicySnapshot struct {
	Enabled             bool     `json:"enabled"`
	Provider            string   `json:"provider"`
	RedactionPosture    string   `json:"redaction_posture"`
	ArtifactRetention   string   `json:"artifact_retention"`
	RequiredEvidence    []string `json:"required_evidence"`
	PromotionRestricted bool     `json:"promotion_restricted"`
}

type AIRepairRequest struct {
	ProjectID  string      `json:"project_id,omitempty"`
	Provider   string      `json:"provider,omitempty"`
	ScriptID   string      `json:"script_id,omitempty"`
	SessionID  string      `json:"session_id,omitempty"`
	Script     *dsl.Script `json:"script,omitempty"`
	Categories []string    `json:"categories,omitempty"`
}

type AIRepairResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	GeneratedAt   time.Time                `json:"generated_at"`
	Provider      string                   `json:"provider"`
	ProjectID     string                   `json:"project_id,omitempty"`
	Target        AIRepairTarget           `json:"target"`
	Suggestions   []AIRepairSuggestion     `json:"suggestions"`
	PreviewScript *dsl.Script              `json:"preview_script,omitempty"`
	Validation    scriptValidationResponse `json:"validation"`
	Guardrails    *AIGuardrailReport       `json:"guardrails,omitempty"`
	NextActions   []string                 `json:"next_actions,omitempty"`
	Citations     []AICitation             `json:"citations,omitempty"`
	Determinism   AIDeterminismDeclaration `json:"determinism"`
}

type AIRepairTarget struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type AIRepairSuggestion struct {
	ID          string   `json:"id"`
	Category    string   `json:"category"`
	Severity    string   `json:"severity"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Action      string   `json:"action"`
	Evidence    []string `json:"evidence,omitempty"`
	PreviewPath string   `json:"preview_path,omitempty"`
}

type AIExplainRequest struct {
	ProjectID       string                   `json:"project_id,omitempty"`
	Provider        string                   `json:"provider,omitempty"`
	TargetType      string                   `json:"target_type"`
	TargetID        string                   `json:"target_id,omitempty"`
	Script          *dsl.Script              `json:"script,omitempty"`
	Scenario        *CreateScenarioRequest   `json:"scenario,omitempty"`
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	Report          *RunArchiveReport        `json:"report,omitempty"`
}

type AIExplainResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	GeneratedAt   time.Time                `json:"generated_at"`
	Provider      string                   `json:"provider"`
	ProjectID     string                   `json:"project_id,omitempty"`
	TargetType    string                   `json:"target_type"`
	TargetID      string                   `json:"target_id,omitempty"`
	Summary       string                   `json:"summary"`
	Sections      []AIExplainSection       `json:"sections"`
	Citations     []AICitation             `json:"citations,omitempty"`
	Limitations   []string                 `json:"limitations,omitempty"`
	NextActions   []string                 `json:"next_actions,omitempty"`
	Determinism   AIDeterminismDeclaration `json:"determinism"`
}

type AIExplainSection struct {
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Summary  string   `json:"summary"`
	Details  []string `json:"details,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

type AICitation struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type AIRootCauseResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	GeneratedAt   time.Time                `json:"generated_at"`
	Provider      string                   `json:"provider"`
	TestID        string                   `json:"test_id"`
	Status        string                   `json:"status"`
	Summary       string                   `json:"summary"`
	EvidenceBound bool                     `json:"evidence_bound"`
	RootCause     RunRootCauseSummary      `json:"root_cause"`
	Citations     []AICitation             `json:"citations,omitempty"`
	NextActions   []string                 `json:"next_actions,omitempty"`
	Limitations   []string                 `json:"limitations,omitempty"`
	Determinism   AIDeterminismDeclaration `json:"determinism"`
}

type AIReviewQueueResponse struct {
	SchemaVersion string              `json:"schema_version"`
	GeneratedAt   time.Time           `json:"generated_at"`
	ProjectID     string              `json:"project_id,omitempty"`
	Items         []AIReviewQueueItem `json:"items"`
	Count         int                 `json:"count"`
	Limit         int                 `json:"limit"`
	Offset        int                 `json:"offset"`
	StatusCounts  map[string]int      `json:"status_counts"`
	NextActions   []string            `json:"next_actions,omitempty"`
}

type AIReviewQueueItem struct {
	SessionID            string             `json:"session_id"`
	ProjectID            string             `json:"project_id,omitempty"`
	Name                 string             `json:"name"`
	Source               string             `json:"source"`
	SourceType           string             `json:"source_type,omitempty"`
	Provider             string             `json:"provider,omitempty"`
	Status               string             `json:"status"`
	ApprovalReady        bool               `json:"approval_ready"`
	BlockingHealthInputs []string           `json:"blocking_health_inputs,omitempty"`
	NextActions          []string           `json:"next_actions,omitempty"`
	Guardrails           *AIGuardrailReport `json:"guardrails,omitempty"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
}

func (s *Server) handleAIAuthoringContract(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildAIAuthoringContract(time.Now().UTC()))
}

func buildAIAuthoringContract(now time.Time) AIAuthoringContractResponse {
	return AIAuthoringContractResponse{
		SchemaVersion: aiAuthoringContractSchemaVersion,
		GeneratedAt:   now,
		Status:        "active",
		AllowedSources: []string{
			"openapi",
			"har",
			"sample_request",
			"user_story",
			"existing_script",
		},
		RequiredEvidence: []string{
			"ai provenance",
			"human diff review",
			"structural validation",
			"replay-before-save evidence",
			"authoring session approval",
		},
		ApprovalGates: []string{
			"AI drafts are saved only as authoring sessions.",
			"Reusable script promotion is blocked until validation, diff review, replay evidence, and approval are complete.",
			"Repair and explanation outputs cite local script, scenario, runtime, or archive evidence.",
		},
		AuditEvents: aiWorkflowAuditEvents(),
		EndPoints: []AIContractEndpoint{
			{Method: http.MethodGet, Path: "/api/v1/ai/authoring/contract", Purpose: "Publish the governed AI authoring contract.", Evidence: []string{"pkg/api/ai_assistance.go"}},
			{Method: http.MethodGet, Path: "/api/v1/ai/policy", Purpose: "Expose enabled state, provider allow-list, redaction posture, and retention controls.", Evidence: []string{"pkg/api/ai_assistance.go", "/api/v1/auth/audit-events/export"}},
			{Method: http.MethodPost, Path: "/api/v1/ai/drafts", Purpose: "Generate deterministic AI draft sessions from supported source types.", Evidence: []string{"pkg/api/ai_assistance_test.go"}},
			{Method: http.MethodPost, Path: "/api/v1/ai/repairs", Purpose: "Suggest deterministic repairs for validation, correlation, data, assertions, waits, and runtime parity.", Evidence: []string{"pkg/api/ai_assistance_test.go"}},
			{Method: http.MethodPost, Path: "/api/v1/ai/explain", Purpose: "Explain scripts, scenarios, runtime settings, and reports with citations.", Evidence: []string{"pkg/api/ai_assistance_test.go"}},
			{Method: http.MethodGet, Path: "/api/v1/ai/review-queue", Purpose: "List AI-generated or AI-repaired drafts waiting for gated promotion.", Evidence: []string{"pkg/api/ai_assistance_test.go"}},
			{Method: http.MethodGet, Path: "/api/v1/tests/{id}/archive/ai-root-cause", Purpose: "Wrap archived root-cause evidence in an AI-assistance response.", Evidence: []string{"pkg/api/c4_analysis_integrations.go"}},
		},
		PromotionRule:       "AI assistance cannot directly create reusable scripts; promotion must pass authoring session approval.",
		DeterminismContract: "The local provider is deterministic and fixture-backed so tests can verify stable output without external network calls.",
		PolicyEndpoint:      "/api/v1/ai/policy",
		ReviewQueueEndpoint: "/api/v1/ai/review-queue",
	}
}

func (s *Server) handleAIPolicy(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.URL.Query().Get("project_id"))
	writeJSON(w, http.StatusOK, buildAIPolicyResponse(projectID, time.Now().UTC()))
}

// enforceAIPolicy verifies AI assistance is enabled and the provider is allowed by
// policy, writing the appropriate 403/400 and returning false when blocked. Shared
// by the draft/repair/explain handlers.
func enforceAIPolicy(w http.ResponseWriter, policy AIPolicyResponse, provider string) bool {
	if !policy.Enabled {
		writeError(w, http.StatusForbidden, "AI assistance is disabled by policy")
		return false
	}
	if !stringSliceContainsFold(policy.AllowedProviders, provider) {
		writeError(w, http.StatusBadRequest, "AI provider is not allowed by policy")
		return false
	}
	return true
}

func buildAIPolicyResponse(projectID string, now time.Time) AIPolicyResponse {
	allowed := aiAllowedProvidersFromEnv()
	redaction := strings.TrimSpace(os.Getenv("APEXLOAD_AI_REDACTION_POSTURE"))
	if redaction == "" {
		redaction = "standard-redaction"
	}
	retentionDays := 30
	if raw := strings.TrimSpace(os.Getenv("APEXLOAD_AI_ARTIFACT_RETENTION_DAYS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			retentionDays = parsed
		}
	}
	disabledProjects := aiDisabledProjectsFromEnv()
	enabled := !envBoolDisabled("APEXLOAD_AI_ENABLED")
	status := "enabled"
	override := (*AIProjectPolicyOverride)(nil)
	if projectID != "" && stringSliceContainsFold(disabledProjects, projectID) {
		enabled = false
		status = "disabled_for_project"
		override = &AIProjectPolicyOverride{ProjectID: projectID, Enabled: false, Reason: "project is listed in APEXLOAD_AI_DISABLED_PROJECTS"}
	}
	if !enabled && status == "enabled" {
		status = "disabled"
	}
	return AIPolicyResponse{
		SchemaVersion:         aiPolicySchemaVersion,
		GeneratedAt:           now,
		ProjectID:             projectID,
		Enabled:               enabled,
		Status:                status,
		DefaultProvider:       defaultAIProvider,
		AllowedProviders:      allowed,
		RedactionPosture:      redaction,
		ArtifactRetentionDays: retentionDays,
		DisabledProjects:      disabledProjects,
		ProjectOverride:       override,
		AuditEvents:           aiWorkflowAuditEvents(),
		Controls: []AIProjectPolicyControl{
			{Key: "promotion_gate", Status: "enforced", Summary: "AI output is restricted to draft sessions until validation, replay, diff review, and approval are complete."},
			{Key: "provider_allow_list", Status: "enforced", Summary: "Requests must use one of the configured allowed providers."},
			{Key: "redaction", Status: "enforced", Summary: "AI prompts and summaries must use the configured redaction posture before provider handoff."},
			{Key: "audit", Status: "enforced", Summary: "Draft, repair, explanation, and root-cause actions are auditable through the standard audit export."},
			{Key: "deterministic_fixtures", Status: "available", Summary: "The deterministic local provider and mocked provider keep generated output stable for golden tests."},
		},
		ExportEndpoints: []string{"/api/v1/auth/audit-events", "/api/v1/auth/audit-events/export"},
		ProviderCapabilities: []AIProviderCapabilityCard{
			{Provider: defaultAIProvider, Status: "enabled", Capabilities: []string{"draft_generation", "repair_suggestions", "explain", "root_cause_summary", "golden_fixtures"}, Notes: []string{"No external network dependency.", "Designed for deterministic test coverage."}},
			{Provider: mockedAIProvider, Status: "test_fixture", Capabilities: []string{"draft_generation", "repair_suggestions", "explain"}, Notes: []string{"Use in tests to prove provider contract stability."}},
		},
	}
}

func (s *Server) handleAIDraftGeneration(w http.ResponseWriter, r *http.Request) {
	var req AIDraftRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	projectID, err := s.bindAIDraftProject(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	policy := buildAIPolicyResponse(projectID, time.Now().UTC())
	provider := normalizeAIProvider(req.Provider)
	if !enforceAIPolicy(w, policy, provider) {
		return
	}
	draft, sourceType, citations, err := s.buildAIDraftScript(req, provider, projectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	validation := buildScriptValidationResponse("", draft)
	repairHints := buildAIRepairSuggestions(draft, validation, nil)
	var queueItem *AIReviewQueueItem
	if req.CreateSession == nil || *req.CreateSession {
		session := newAIDraftAuthoringSession(draft, sourceType, provider, validation.AuthoringHealth)
		if err := s.saveAuthoringSession(session); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		enrichAuthoringSession(session)
		item := aiReviewQueueItemFromSession(session)
		queueItem = &item
	}
	s.recordAuditEvent(r, "ai.draft.generate", "success", "ai_draft", draft.ID, draft.ProjectID, map[string]any{
		"source_type": sourceType,
		"provider":    provider,
		"session_id":  queueSessionID(queueItem),
	})
	writeJSON(w, http.StatusCreated, AIDraftResponse{
		SchemaVersion:   aiDraftSchemaVersion,
		GeneratedAt:     time.Now().UTC(),
		Provider:        provider,
		ProjectID:       draft.ProjectID,
		SourceType:      sourceType,
		Draft:           maskScriptSecrets(draft),
		Validation:      validation,
		Guardrails:      validation.AIGuardrails,
		AuthoringHealth: validation.AuthoringHealth,
		ReviewQueueItem: queueItem,
		NextActions:     authoringNextActions(validation.AuthoringHealth, authoringHealthInputsWithStatus(validation.AuthoringHealth, "blocker"), authoringHealthInputsWithStatus(validation.AuthoringHealth, "warning")),
		Citations:       citations,
		Determinism:     aiDeterminism(sourceType, []string{"source_type", "source_payload", "existing_script_id", "name"}),
		Policy:          aiPolicySnapshot(policy, provider),
		RepairHints:     repairHints,
	})
}

func (s *Server) handleAIRepairSuggestions(w http.ResponseWriter, r *http.Request) {
	var req AIRepairRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	provider := normalizeAIProvider(req.Provider)
	script, target, err := s.resolveAIRepairScript(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID, err := s.bindAIRepairProject(&req, script.ProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	policy := buildAIPolicyResponse(projectID, time.Now().UTC())
	if !enforceAIPolicy(w, policy, provider) {
		return
	}
	validation := buildScriptValidationResponse(req.ScriptID, script)
	suggestions := buildAIRepairSuggestions(script, validation, req.Categories)
	preview := applyAIRepairPreview(script, suggestions)
	s.recordAuditEvent(r, "ai.repair.suggest", "success", "ai_repair", target.ID, projectID, map[string]any{
		"target_type":      target.Type,
		"provider":         provider,
		"suggestion_count": len(suggestions),
	})
	writeJSON(w, http.StatusOK, AIRepairResponse{
		SchemaVersion: aiRepairSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Provider:      provider,
		ProjectID:     projectID,
		Target:        target,
		Suggestions:   suggestions,
		PreviewScript: maskScriptSecrets(preview),
		Validation:    validation,
		Guardrails:    validation.AIGuardrails,
		NextActions:   aiRepairNextActions(suggestions),
		Citations: []AICitation{
			{Label: "Validation contract", Path: "/api/v1/scripts/validate"},
			{Label: "Runtime settings support", Path: "/api/v1/runtime-settings/support-matrix"},
			{Label: "Authoring approval gate", Path: "/api/v1/scripts/authoring/sessions/{id}/approve"},
		},
		Determinism: aiDeterminism("repair", []string{"script", "script_id", "session_id", "categories"}),
	})
}

func (s *Server) handleAIExplain(w http.ResponseWriter, r *http.Request) {
	var req AIExplainRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	projectID, err := s.bindAIExplainProject(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider := normalizeAIProvider(req.Provider)
	policy := buildAIPolicyResponse(projectID, time.Now().UTC())
	if !enforceAIPolicy(w, policy, provider) {
		return
	}
	response, err := s.buildAIExplainResponse(req, provider, projectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "ai.explain", "success", "ai_explain", response.TargetID, response.ProjectID, map[string]any{
		"target_type": response.TargetType,
		"provider":    provider,
	})
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleAIReviewQueue(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.URL.Query().Get("project_id"))
	limit, offset := parseLimitOffsetQuery(r, defaultAuthoringSessionListLimit)
	if limit > maxAuthoringSessionListLimit {
		limit = maxAuthoringSessionListLimit
	}
	sessions, err := s.listAuthoringSessionsPage(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]AIReviewQueueItem, 0, len(sessions))
	for _, session := range sessions {
		enrichAuthoringSession(session)
		item := aiReviewQueueItemFromSession(session)
		if item.SessionID == "" {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	counts := map[string]int{"ready": 0, "needs_review": 0, "blocked": 0, "approved": 0}
	for _, item := range items {
		key := item.Status
		if item.Guardrails != nil && item.Guardrails.Status != "" {
			key = item.Guardrails.Status
		}
		if _, ok := counts[key]; !ok {
			counts[key] = 0
		}
		counts[key]++
	}
	writeJSON(w, http.StatusOK, AIReviewQueueResponse{
		SchemaVersion: aiReviewQueueSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		ProjectID:     projectID,
		Items:         items,
		Count:         len(items),
		Limit:         limit,
		Offset:        offset,
		StatusCounts:  counts,
		NextActions:   aiReviewQueueNextActions(items),
	})
}

func (s *Server) handleGetRunArchiveAIRootCause(w http.ResponseWriter, r *http.Request) {
	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	report, err := cachedRunArchiveReport(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build AI root-cause summary: "+err.Error())
		return
	}
	testID := strings.TrimSpace(r.PathValue("id"))
	rootCause := buildRunRootCauseSummary(testID, report)
	provider := normalizeAIProvider(r.URL.Query().Get("provider"))
	response := buildAIRootCauseResponse(testID, provider, rootCause)
	s.recordAuditEvent(r, "ai.root_cause.summarize", "success", "test", testID, "", map[string]any{
		"provider": provider,
		"status":   response.Status,
	})
	writeJSON(w, http.StatusOK, response)
}

func buildAIRootCauseResponse(testID string, provider string, rootCause RunRootCauseSummary) AIRootCauseResponse {
	actions := append([]string(nil), rootCause.NextActions...)
	if len(actions) == 0 {
		actions = []string{"Review the archive engineer profile before accepting the run outcome."}
	}
	return AIRootCauseResponse{
		SchemaVersion: aiRootCauseSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Provider:      normalizeAIProvider(provider),
		TestID:        strings.TrimSpace(testID),
		Status:        rootCause.Status,
		Summary:       rootCause.Summary,
		EvidenceBound: true,
		RootCause:     rootCause,
		Citations: []AICitation{
			{Label: "Archive report", Path: "/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/report"},
			{Label: "Deterministic root-cause engine", Path: "pkg/api/c4_analysis_integrations.go"},
			{Label: "AI workflow contract", Path: "/api/v1/ai/authoring/contract"},
		},
		NextActions: actions,
		Limitations: []string{
			"This summary is constrained to archived metrics, errors, resource, browser, and imported telemetry evidence.",
			"It does not infer causes outside local artifacts.",
		},
		Determinism: aiDeterminism("root_cause", []string{"archive_report", "root_cause_summary"}),
	}
}

func aiReviewQueueItemFromSession(session *storage.AuthoringSession) AIReviewQueueItem {
	if session == nil || !sessionIsAIReviewCandidate(session) {
		return AIReviewQueueItem{}
	}
	var guardrails *AIGuardrailReport
	sourceType := reviewStateString(session.ReviewState, "ai_source_type")
	provider := reviewStateString(session.ReviewState, "ai_provider")
	if session.Draft != nil {
		validation := buildScriptValidationResponse("", session.Draft)
		guardrails = validation.AIGuardrails
		if sourceType == "" {
			sourceType = preservedString(session.Draft, "ai_source_type")
		}
		if provider == "" {
			provider = preservedString(session.Draft, "ai_provider")
		}
	}
	return AIReviewQueueItem{
		SessionID:            session.ID,
		ProjectID:            session.ProjectID,
		Name:                 session.Name,
		Source:               session.Source,
		SourceType:           sourceType,
		Provider:             provider,
		Status:               session.Status,
		ApprovalReady:        session.ApprovalReady,
		BlockingHealthInputs: append([]string(nil), session.BlockingHealthInputs...),
		NextActions:          append([]string(nil), session.NextActions...),
		Guardrails:           guardrails,
		CreatedAt:            session.CreatedAt,
		UpdatedAt:            session.UpdatedAt,
	}
}

func sessionIsAIReviewCandidate(session *storage.AuthoringSession) bool {
	if session == nil {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(session.Source))
	if source == "ai_generated" || source == "ai_repaired" || strings.HasPrefix(source, "ai_") {
		return true
	}
	if reviewStateString(session.ReviewState, "ai_provider") != "" || reviewStateString(session.ReviewState, "ai_source_type") != "" {
		return true
	}
	return isAIGeneratedDraft(session.Draft)
}

func reviewStateString(state map[string]any, key string) string {
	if state == nil {
		return ""
	}
	value, ok := state[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func normalizeAIProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return defaultAIProvider
	}
	return provider
}

func normalizeAISourceType(sourceType string) string {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	sourceType = strings.ReplaceAll(sourceType, "-", "_")
	return sourceType
}

func normalizeAITargetType(targetType string) string {
	targetType = strings.ToLower(strings.TrimSpace(targetType))
	targetType = strings.ReplaceAll(targetType, "-", "_")
	return targetType
}

func aiAllowedProvidersFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_AI_ALLOWED_PROVIDERS"))
	if raw == "" {
		return []string{defaultAIProvider, mockedAIProvider}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if provider := normalizeAIProvider(part); provider != "" {
			out = append(out, provider)
		}
	}
	if len(out) == 0 {
		return []string{defaultAIProvider}
	}
	sort.Strings(out)
	return sortedUniqueStrings(out)
}

func aiDisabledProjectsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_AI_DISABLED_PROJECTS"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if projectID := normalizeProjectID(part); projectID != "" {
			out = append(out, projectID)
		}
	}
	sort.Strings(out)
	return sortedUniqueStrings(out)
}

func envBoolDisabled(key string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "0", "false", "no", "off", "disabled":
		return true
	default:
		return false
	}
}

func aiWorkflowAuditEvents() []string {
	return []string{
		"ai.draft.generate",
		"ai.repair.suggest",
		"ai.explain",
		"ai.root_cause.summarize",
		"script.authoring_session.approve",
		"auth.audit_events.export",
	}
}

func aiPolicySnapshot(policy AIPolicyResponse, provider string) AIWorkflowPolicySnapshot {
	return AIWorkflowPolicySnapshot{
		Enabled:             policy.Enabled,
		Provider:            provider,
		RedactionPosture:    policy.RedactionPosture,
		ArtifactRetention:   fmt.Sprintf("%d days", policy.ArtifactRetentionDays),
		RequiredEvidence:    []string{"provenance", "diff_review", "validation", "replay", "approval"},
		PromotionRestricted: true,
	}
}

func aiDeterminism(fixtureKey string, stableInput []string) AIDeterminismDeclaration {
	return AIDeterminismDeclaration{
		Mode:        "deterministic-local-provider",
		FixtureKey:  fixtureKey,
		StableInput: stableInput,
	}
}

func queueSessionID(item *AIReviewQueueItem) string {
	if item == nil {
		return ""
	}
	return item.SessionID
}

func aiCategorySet(categories []string) map[string]bool {
	if len(categories) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, category := range categories {
		category = strings.ToLower(strings.TrimSpace(category))
		category = strings.ReplaceAll(category, "-", "_")
		if category != "" {
			out[category] = true
		}
	}
	return out
}

func severityForStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "blocked", "blocker":
		return "blocker"
	case "partial", "compatibility_only", "needs_review":
		return "warning"
	default:
		return "info"
	}
}

func repairSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "blocker", "critical":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

func actionAcceptsResponseAssertions(action dsl.Action) bool {
	spec, ok := dsl.SpecForActionType(action.Type)
	if !ok {
		return true
	}
	return !spec.EngineAction()
}

func isHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func actionNeedsCorrelation(action dsl.Action) bool {
	if len(action.Extractors) > 0 {
		return false
	}
	combined := strings.ToLower(action.Body + " " + action.Value)
	for key, value := range action.Headers {
		combined += " " + strings.ToLower(key) + " " + strings.ToLower(value)
	}
	return strings.Contains(combined, "csrf") || strings.Contains(combined, "session") || strings.Contains(combined, "xsrf")
}

func actionHasHardCodedSensitiveData(action dsl.Action) bool {
	combined := strings.ToLower(action.Body + " " + action.Value)
	for key, value := range action.Headers {
		combined += " " + strings.ToLower(key) + " " + strings.ToLower(value)
	}
	return strings.Contains(combined, "bearer ") || strings.Contains(combined, "password") || strings.Contains(combined, "token=") || strings.Contains(combined, "session=")
}

func browserActionNeedsWaitRepair(action dsl.Action) bool {
	selector := strings.ToLower(strings.TrimSpace(action.Selector))
	if action.WaitFor == nil && selector != "" {
		return true
	}
	return strings.Contains(selector, ":nth-child") || strings.Contains(selector, " > div") || strings.Contains(selector, "xpath=/html")
}

func aiRepairNextActions(suggestions []AIRepairSuggestion) []string {
	if len(suggestions) == 0 {
		return []string{"No deterministic repair suggestions were found; validate and replay the draft before approval."}
	}
	actions := []string{"Review suggested changes in diff mode before applying them."}
	hasBlocker := false
	for _, suggestion := range suggestions {
		if suggestion.Severity == "blocker" {
			hasBlocker = true
			break
		}
	}
	if hasBlocker {
		actions = append(actions, "Resolve blocker suggestions and rerun validation before replay.")
	}
	actions = append(actions, "Attach replay evidence before approving any AI-repaired draft.")
	return actions
}

func aiReviewQueueNextActions(items []AIReviewQueueItem) []string {
	if len(items) == 0 {
		return []string{"No AI-generated or AI-repaired drafts are waiting for review."}
	}
	blocked := 0
	ready := 0
	for _, item := range items {
		if item.ApprovalReady {
			ready++
		}
		if len(item.BlockingHealthInputs) > 0 {
			blocked++
		}
	}
	return []string{
		fmt.Sprintf("Review %d AI draft(s); %d blocked and %d approval-ready.", len(items), blocked, ready),
		"Use validation, diff review, replay evidence, and authoring approval to promote reusable assets.",
	}
}
