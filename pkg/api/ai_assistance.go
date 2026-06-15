package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
	projectID := s.deriveAIDraftProjectID(req)
	policy := buildAIPolicyResponse(projectID, time.Now().UTC())
	provider := normalizeAIProvider(req.Provider)
	if !policy.Enabled {
		writeError(w, http.StatusForbidden, "AI assistance is disabled by policy")
		return
	}
	if !stringSliceContainsFold(policy.AllowedProviders, provider) {
		writeError(w, http.StatusBadRequest, "AI provider is not allowed by policy")
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
		Draft:           draft,
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
	projectID := normalizeProjectID(firstNonEmpty(req.ProjectID, script.ProjectID))
	policy := buildAIPolicyResponse(projectID, time.Now().UTC())
	if !policy.Enabled {
		writeError(w, http.StatusForbidden, "AI assistance is disabled by policy")
		return
	}
	if !stringSliceContainsFold(policy.AllowedProviders, provider) {
		writeError(w, http.StatusBadRequest, "AI provider is not allowed by policy")
		return
	}
	if projectID == "" {
		projectID = normalizeProjectID(script.ProjectID)
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
		PreviewScript: preview,
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
	projectID := s.deriveAIExplainProjectID(req)
	provider := normalizeAIProvider(req.Provider)
	policy := buildAIPolicyResponse(projectID, time.Now().UTC())
	if !policy.Enabled {
		writeError(w, http.StatusForbidden, "AI assistance is disabled by policy")
		return
	}
	if !stringSliceContainsFold(policy.AllowedProviders, provider) {
		writeError(w, http.StatusBadRequest, "AI provider is not allowed by policy")
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
	sessions, err := s.listAuthoringSessions(projectID)
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
		StatusCounts:  counts,
		NextActions:   aiReviewQueueNextActions(items),
	})
}

func (s *Server) handleGetRunArchiveAIRootCause(w http.ResponseWriter, r *http.Request) {
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	report, err := buildRunArchiveReport(record)
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

func (s *Server) buildAIDraftScript(req AIDraftRequest, provider string, projectID string) (*dsl.Script, string, []AICitation, error) {
	sourceType := normalizeAISourceType(req.SourceType)
	if sourceType == "" {
		return nil, "", nil, fmt.Errorf("source_type is required")
	}
	switch sourceType {
	case "openapi", "har", "sample_request", "user_story", "existing_script":
	default:
		return nil, "", nil, fmt.Errorf("unsupported AI source_type %q", req.SourceType)
	}

	if sourceType == "existing_script" {
		if strings.TrimSpace(req.ExistingScriptID) == "" {
			return nil, "", nil, fmt.Errorf("existing_script_id is required for existing_script source")
		}
		stored, ok := s.runner.GetScript(strings.TrimSpace(req.ExistingScriptID))
		if !ok || stored == nil || stored.Script == nil {
			return nil, "", nil, fmt.Errorf("existing script not found")
		}
		draft := cloneScriptDefinition(stored.Script)
		draft.ID = uuid.New().String()
		draft.Name = firstNonEmpty(req.Name, stored.Name+" AI Draft")
		draft.ProjectID = normalizeProjectID(firstNonEmpty(projectID, stored.ProjectID))
		attachAIProvenance(draft, provider, sourceType, firstNonEmpty(req.SourceName, stored.Name))
		return draft, sourceType, []AICitation{{Label: "Existing script", Path: "/api/v1/scripts/" + stored.ID}}, nil
	}

	baseURL, method, target, actionName := aiDraftEndpointFromRequest(sourceType, req)
	if req.Name == "" {
		req.Name = "AI " + strings.ReplaceAll(sourceType, "_", " ") + " draft"
	}
	draftAction := dsl.SkeletonAction(method)
	draftAction.Name = actionName
	draftAction.Method = method
	draftAction.Target = target
	draftAction.Headers = map[string]string{
		"Accept": "application/json",
	}
	draftAction.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
	draftAction.Metadata = map[string]any{
		"ai_source_type": sourceType,
		"review_hint":    "Verify path, auth, body examples, and data source bindings before replay.",
	}
	draft := &dsl.Script{
		ID:            uuid.New().String(),
		Name:          strings.TrimSpace(req.Name),
		Version:       "1.0.0",
		Protocol:      "http",
		ProjectID:     projectID,
		GeneratedBy:   "apexload-ai-" + provider,
		GeneratedFrom: "ai_generated",
		Config: dsl.ScriptConfig{
			BaseURL: baseURL,
		},
		Actions: []dsl.Action{draftAction},
		DataSources: []dsl.DataSource{
			{
				Name:           "ai_seed_data",
				Type:           "csv",
				InlineData:     "id,name\n1,example\n",
				Strategy:       "sequential",
				FirstRowHeader: true,
			},
		},
		Parameters: []dsl.Parameter{
			{Name: "sample_id", Type: "file", DataSource: "ai_seed_data", Column: "id", Selection: "sequential", OnExhaustion: "wrap"},
		},
	}
	attachAIProvenance(draft, provider, sourceType, firstNonEmpty(req.SourceName, sourceType))
	return draft, sourceType, []AICitation{
		{Label: "AI authoring contract", Path: "/api/v1/ai/authoring/contract"},
		{Label: "Draft validation", Path: "/api/v1/scripts/validate"},
	}, nil
}

func (s *Server) deriveAIDraftProjectID(req AIDraftRequest) string {
	if projectID := normalizeProjectID(req.ProjectID); projectID != "" {
		return projectID
	}
	if strings.TrimSpace(req.ExistingScriptID) == "" {
		return ""
	}
	stored, ok := s.runner.GetScript(strings.TrimSpace(req.ExistingScriptID))
	if !ok || stored == nil {
		return ""
	}
	return normalizeProjectID(stored.ProjectID)
}

func (s *Server) deriveAIExplainProjectID(req AIExplainRequest) string {
	if projectID := normalizeProjectID(req.ProjectID); projectID != "" {
		return projectID
	}
	if req.Script != nil {
		return normalizeProjectID(req.Script.ProjectID)
	}
	targetType := normalizeAITargetType(req.TargetType)
	targetID := strings.TrimSpace(req.TargetID)
	if targetID == "" {
		return ""
	}
	switch targetType {
	case "script":
		if stored, ok := s.runner.GetScript(targetID); ok && stored != nil {
			return normalizeProjectID(stored.ProjectID)
		}
	case "scenario":
		if scenario, ok := s.runner.GetScenario(targetID); ok && scenario != nil {
			return normalizeProjectID(scenario.ProjectID)
		}
	case "report":
		if test, ok := s.runner.GetTest(targetID); ok && test != nil {
			return normalizeProjectID(test.ProjectID)
		}
	}
	return ""
}

func aiDraftEndpointFromRequest(sourceType string, req AIDraftRequest) (string, string, string, string) {
	baseURL := "https://example.test"
	method := "GET"
	target := "/"
	actionName := "AI generated request"
	if len(req.SourcePayload) > 0 {
		switch sourceType {
		case "openapi":
			baseURL, method, target, actionName = endpointFromOpenAPIPayload(req.SourcePayload, baseURL, method, target, "OpenAPI generated request")
		case "har":
			baseURL, method, target, actionName = endpointFromHARPayload(req.SourcePayload, baseURL, method, target, "HAR generated request")
		case "sample_request":
			baseURL, method, target, actionName = endpointFromSamplePayload(req.SourcePayload, baseURL, method, target, "Sample generated request")
		}
	}
	text := strings.TrimSpace(firstNonEmpty(req.Text, req.Prompt))
	if text != "" {
		if parsedMethod, parsedTarget, parsedBaseURL := endpointFromText(text); parsedTarget != "" {
			method = parsedMethod
			target = parsedTarget
			if parsedBaseURL != "" {
				baseURL = parsedBaseURL
			}
			actionName = strings.ToUpper(method) + " " + target
		} else if sourceType == "user_story" {
			target = "/user-story-flow"
			actionName = "User story acceptance flow"
		}
	}
	return baseURL, method, target, actionName
}

func endpointFromOpenAPIPayload(raw json.RawMessage, fallbackBaseURL string, fallbackMethod string, fallbackTarget string, fallbackName string) (string, string, string, string) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	baseURL := fallbackBaseURL
	if servers, ok := payload["servers"].([]any); ok && len(servers) > 0 {
		if server, ok := servers[0].(map[string]any); ok {
			if rawURL, ok := server["url"].(string); ok && strings.TrimSpace(rawURL) != "" {
				baseURL = strings.TrimSpace(rawURL)
			}
		}
	}
	paths, _ := payload["paths"].(map[string]any)
	keys := make([]string, 0, len(paths))
	for path := range paths {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for _, path := range keys {
		methods, _ := paths[path].(map[string]any)
		for _, candidate := range []string{"get", "post", "put", "patch", "delete"} {
			if _, ok := methods[candidate]; ok {
				return baseURL, strings.ToUpper(candidate), path, "OpenAPI " + strings.ToUpper(candidate) + " " + path
			}
		}
	}
	return baseURL, fallbackMethod, fallbackTarget, fallbackName
}

func endpointFromHARPayload(raw json.RawMessage, fallbackBaseURL string, fallbackMethod string, fallbackTarget string, fallbackName string) (string, string, string, string) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	logPayload, _ := payload["log"].(map[string]any)
	entries, _ := logPayload["entries"].([]any)
	if len(entries) == 0 {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	entry, _ := entries[0].(map[string]any)
	request, _ := entry["request"].(map[string]any)
	method, _ := request["method"].(string)
	rawURL, _ := request["url"].(string)
	baseURL, target := splitAbsoluteURL(rawURL)
	if method == "" {
		method = fallbackMethod
	}
	if baseURL == "" {
		baseURL = fallbackBaseURL
	}
	if target == "" {
		target = fallbackTarget
	}
	return baseURL, strings.ToUpper(method), target, "HAR " + strings.ToUpper(method) + " " + target
}

func endpointFromSamplePayload(raw json.RawMessage, fallbackBaseURL string, fallbackMethod string, fallbackTarget string, fallbackName string) (string, string, string, string) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	method, _ := payload["method"].(string)
	target, _ := payload["target"].(string)
	rawURL, _ := payload["url"].(string)
	baseURL, parsedTarget := splitAbsoluteURL(rawURL)
	if method == "" {
		method = fallbackMethod
	}
	if target == "" {
		target = parsedTarget
	}
	if target == "" {
		target = fallbackTarget
	}
	if baseURL == "" {
		baseURL = fallbackBaseURL
	}
	return baseURL, strings.ToUpper(method), target, "Sample " + strings.ToUpper(method) + " " + target
}

func endpointFromText(text string) (string, string, string) {
	fields := strings.Fields(text)
	for i, field := range fields {
		method := strings.ToUpper(strings.Trim(field, " \t\r\n:"))
		if !isHTTPMethod(method) {
			continue
		}
		for _, next := range fields[i+1:] {
			candidate := strings.Trim(next, "'\"`")
			if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
				baseURL, target := splitAbsoluteURL(candidate)
				return method, target, baseURL
			}
			if strings.HasPrefix(candidate, "/") {
				return method, candidate, ""
			}
		}
		return method, "", ""
	}
	return "GET", "", ""
}

func splitAbsoluteURL(raw string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ""
	}
	target := parsed.EscapedPath()
	if target == "" {
		target = "/"
	}
	if parsed.RawQuery != "" {
		target += "?" + parsed.RawQuery
	}
	return parsed.Scheme + "://" + parsed.Host, target
}

func attachAIProvenance(script *dsl.Script, provider string, sourceType string, sourceName string) {
	if script == nil {
		return
	}
	script.GeneratedBy = "apexload-ai-" + normalizeAIProvider(provider)
	script.GeneratedFrom = "ai_generated"
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	script.Metadata.Import.Source = "ai_generated"
	script.Metadata.Import.SourceScriptName = strings.TrimSpace(sourceName)
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved["ai_contract_version"] = aiAuthoringContractSchemaVersion
	script.Metadata.Import.Preserved["ai_source_type"] = sourceType
	script.Metadata.Import.Preserved["ai_provider"] = normalizeAIProvider(provider)
	script.Metadata.Import.Preserved["diff_reviewed"] = false
	script.Metadata.Import.Preserved["replay_status"] = "not_run"
	script.Metadata.Import.Preserved["validation_status"] = "pending"
}

func newAIDraftAuthoringSession(draft *dsl.Script, sourceType string, provider string, health AuthoringHealth) *storage.AuthoringSession {
	now := time.Now()
	reviewState := map[string]any{
		"ai_contract_version": aiAuthoringContractSchemaVersion,
		"ai_source_type":      sourceType,
		"ai_provider":         normalizeAIProvider(provider),
		"diff_reviewed":       false,
		"replay_status":       "not_run",
		"validation_status":   health.Status,
	}
	session := &storage.AuthoringSession{
		ID:             uuid.New().String(),
		ProjectID:      normalizeProjectID(draft.ProjectID),
		Source:         "ai_generated",
		Name:           authoringSessionName(draft.Name, draft),
		Status:         "blocked",
		Draft:          cloneScriptDefinition(draft),
		ReviewState:    reviewState,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}
	updateAuthoringSessionReviewReadiness(session, health)
	return session
}

func (s *Server) resolveAIRepairScript(req AIRepairRequest) (*dsl.Script, AIRepairTarget, error) {
	if req.Script != nil {
		target := AIRepairTarget{Type: "draft", Name: req.Script.Name}
		return req.Script, target, nil
	}
	if strings.TrimSpace(req.SessionID) != "" {
		session, err := s.loadAuthoringSession(strings.TrimSpace(req.SessionID))
		if err != nil || session == nil || session.Draft == nil {
			return nil, AIRepairTarget{}, fmt.Errorf("authoring session draft not found")
		}
		return session.Draft, AIRepairTarget{Type: "authoring_session", ID: session.ID, Name: session.Name}, nil
	}
	if strings.TrimSpace(req.ScriptID) != "" {
		stored, ok := s.runner.GetScript(strings.TrimSpace(req.ScriptID))
		if !ok || stored == nil || stored.Script == nil {
			return nil, AIRepairTarget{}, fmt.Errorf("script not found")
		}
		return stored.Script, AIRepairTarget{Type: "script", ID: stored.ID, Name: stored.Name}, nil
	}
	return nil, AIRepairTarget{}, fmt.Errorf("script, script_id, or session_id is required")
}

func buildAIRepairSuggestions(script *dsl.Script, validation scriptValidationResponse, categories []string) []AIRepairSuggestion {
	wanted := aiCategorySet(categories)
	suggestions := []AIRepairSuggestion{}
	add := func(category string, severity string, title string, summary string, action string, evidence []string, previewPath string) {
		if len(wanted) > 0 && !wanted[category] {
			return
		}
		id := fmt.Sprintf("ai-repair-%s-%02d", strings.ReplaceAll(category, "_", "-"), len(suggestions)+1)
		suggestions = append(suggestions, AIRepairSuggestion{
			ID:          id,
			Category:    category,
			Severity:    severity,
			Title:       title,
			Summary:     summary,
			Action:      action,
			Evidence:    evidence,
			PreviewPath: previewPath,
		})
	}
	for _, issue := range validation.Issues {
		add("validation", "blocker", "Fix structural validation issue", issue, "Update the draft DSL so validation passes before replay or promotion.", []string{"/api/v1/scripts/validate"}, "")
	}
	if validation.DataSourceFit != nil {
		for _, item := range validation.DataSourceFit.Items {
			if item.Status == "blocked" || item.Status == "needs_review" {
				add("data_source", severityForStatus(item.Status), "Repair data source fit", item.Summary, "Add the missing data source/column or adjust unique-row usage before high-volume execution.", []string{"data_source_fit"}, "data_sources")
			}
		}
	}
	if script != nil {
		for index, action := range script.Actions {
			path := fmt.Sprintf("actions[%d]", index)
			if len(action.Assertions) == 0 && actionAcceptsResponseAssertions(action) {
				add("assertions", "warning", "Add response assertion", "Action "+action.Name+" has no response assertion.", "Add at least one status_code or content assertion so replay can prove expected behavior.", []string{path + ".assertions"}, path+".assertions")
			}
			if actionNeedsCorrelation(action) {
				add("correlation", "warning", "Add correlation extractor", "Action "+action.Name+" appears to use session/token data without an extractor.", "Capture the dynamic token from an earlier response and replace the hard-coded value with {{var.name}}.", []string{path + ".headers", path + ".body"}, path+".extractors")
			}
			if actionHasHardCodedSensitiveData(action) {
				add("hard_coded_data", "warning", "Parameterize hard-coded sensitive data", "Action "+action.Name+" contains token, session, password, or bearer-like literals.", "Move the value into parameters, data sources, or env variables before sharing or approving.", []string{path + ".headers", path + ".body"}, path)
			}
			if script.Protocol == "browser" && browserActionNeedsWaitRepair(action) {
				add("unstable_waits", "warning", "Add stable browser wait", "Browser action "+action.Name+" has no explicit readiness wait or uses a brittle selector.", "Add a visible/network-idle wait and prefer stable data-testid or role selectors.", []string{path + ".selector", path + ".wait_for"}, path+".wait_for")
			}
			if script.Protocol == "browser" && scriptHasWeb2ImportProfile(script) && browserActionNeedsSelectorRepair(action) {
				add("selector", "warning", "Repair Web 2.0 selector", "Browser action "+action.Name+" needs selector review against imported object descriptors and replay DOM evidence.", "Open the Object Descriptors dialog, choose a stable candidate, and rerun browser replay before approval.", []string{path + ".selector", "metadata.import.preserved.trueclient_archive_inspection"}, path+".selector")
			}
		}
	}
	for _, entry := range validation.RuntimeSettingsParity {
		status := strings.ToLower(string(entry.Status))
		if status == "blocked" || status == "partial" || status == "compatibility_only" {
			add("runtime_parity", severityForStatus(status), "Review runtime setting parity", entry.Concept+" is "+status+".", "Use the runtime settings report to decide whether to remove, replace, or explicitly accept this setting.", []string{"/api/v1/runtime-settings/support-matrix"}, "config")
		}
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		if repairSeverityRank(suggestions[i].Severity) == repairSeverityRank(suggestions[j].Severity) {
			return suggestions[i].ID < suggestions[j].ID
		}
		return repairSeverityRank(suggestions[i].Severity) > repairSeverityRank(suggestions[j].Severity)
	})
	return suggestions
}

func applyAIRepairPreview(script *dsl.Script, suggestions []AIRepairSuggestion) *dsl.Script {
	if script == nil || len(suggestions) == 0 {
		return nil
	}
	preview := cloneScriptDefinition(script)
	for index := range preview.Actions {
		action := &preview.Actions[index]
		if len(action.Assertions) == 0 && actionAcceptsResponseAssertions(*action) {
			action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: 200})
		}
		if preview.Protocol == "browser" && action.WaitFor == nil && strings.TrimSpace(action.Selector) != "" {
			action.WaitFor = &dsl.WaitCondition{Type: "visible", Value: action.Selector, TimeoutMs: 5000}
		}
	}
	attachAIProvenance(preview, defaultAIProvider, "repair", "ai_repair_preview")
	preview.Metadata.Import.Preserved["ai_repair_preview"] = true
	return preview
}

func scriptHasWeb2ImportProfile(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return false
	}
	preserved := script.Metadata.Import.Preserved
	sourceType := strings.ToLower(strings.TrimSpace(stringValue(preserved["source_script_type"])))
	sourceProfile := strings.ToLower(strings.TrimSpace(stringValue(preserved["source_profile"])))
	return strings.Contains(sourceType, "truclient 2.0") || strings.Contains(sourceType, "truclient2") || strings.Contains(sourceProfile, "truclient2_web")
}

func browserActionNeedsSelectorRepair(action dsl.Action) bool {
	switch action.Type {
	case "browser_click", "browser_fill", "browser_select", "browser_tap", "browser_long_press", "browser_swipe":
	default:
		return false
	}
	selector := strings.TrimSpace(firstNonEmpty(action.Selector, action.Target))
	if selector == "" {
		return true
	}
	lower := strings.ToLower(selector)
	return !(strings.Contains(lower, "data-testid") || strings.Contains(lower, "aria-label") || strings.HasPrefix(selector, "#") || strings.Contains(lower, "[role="))
}

func (s *Server) buildAIExplainResponse(req AIExplainRequest, provider string, projectID string) (AIExplainResponse, error) {
	targetType := normalizeAITargetType(req.TargetType)
	if targetType == "" {
		return AIExplainResponse{}, fmt.Errorf("target_type is required")
	}
	response := AIExplainResponse{
		SchemaVersion: aiExplainSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Provider:      provider,
		ProjectID:     projectID,
		TargetType:    targetType,
		TargetID:      strings.TrimSpace(req.TargetID),
		Determinism:   aiDeterminism("explain_"+targetType, []string{"target_type", "target_id", "inline_payload"}),
	}
	switch targetType {
	case "script":
		script := req.Script
		if script == nil {
			stored, ok := s.runner.GetScript(strings.TrimSpace(req.TargetID))
			if !ok || stored == nil || stored.Script == nil {
				return response, fmt.Errorf("script not found")
			}
			script = stored.Script
			response.TargetID = stored.ID
			response.ProjectID = firstNonEmpty(response.ProjectID, stored.ProjectID)
		}
		response.Summary, response.Sections, response.Citations, response.NextActions = explainScript(script, response.TargetID)
	case "scenario":
		if req.Scenario != nil {
			response.Summary, response.Sections, response.Citations, response.NextActions = explainScenarioDraft(*req.Scenario)
		} else {
			scenario, ok := s.runner.GetScenario(strings.TrimSpace(req.TargetID))
			if !ok || scenario == nil {
				return response, fmt.Errorf("scenario not found")
			}
			response.TargetID = scenario.ID
			response.ProjectID = firstNonEmpty(response.ProjectID, scenario.ProjectID)
			response.Summary, response.Sections, response.Citations, response.NextActions = explainStoredScenario(scenario)
		}
	case "runtime_settings":
		response.Summary, response.Sections, response.Citations, response.NextActions = explainRuntimeSettings()
	case "report":
		if req.Report != nil {
			response.Summary, response.Sections, response.Citations, response.NextActions = explainRunArchiveReport(req.Report, response.TargetID)
		} else {
			record, ok := s.runner.GetRunArchive(strings.TrimSpace(req.TargetID))
			if !ok {
				return response, fmt.Errorf("run archive not found")
			}
			report, err := buildRunArchiveReport(record)
			if err != nil {
				return response, err
			}
			response.Summary, response.Sections, response.Citations, response.NextActions = explainRunArchiveReport(report, response.TargetID)
		}
	default:
		return response, fmt.Errorf("unsupported target_type %q", req.TargetType)
	}
	response.Limitations = []string{"Explanation is grounded in local APEX Load scripts, scenarios, runtime support matrices, or archived reports only."}
	return response, nil
}

func explainScript(script *dsl.Script, id string) (string, []AIExplainSection, []AICitation, []string) {
	if script == nil {
		return "No script evidence is available.", nil, nil, []string{"Attach a script or target a stored script before requesting explanation."}
	}
	validation := buildScriptValidationResponse(id, script)
	assertions := 0
	extractors := 0
	for _, action := range script.Actions {
		assertions += len(action.Assertions)
		extractors += len(action.Extractors)
	}
	sections := []AIExplainSection{
		{Title: "Protocol", Status: "ready", Summary: fmt.Sprintf("%s script with %d action(s).", firstNonEmpty(script.Protocol, "unknown"), len(script.Actions)), Details: []string{"Base URL: " + firstNonEmpty(script.Config.BaseURL, "(not configured)")}, Evidence: []string{"script.protocol", "script.actions", "script.config.base_url"}},
		{Title: "Validation", Status: validation.AuthoringHealth.Status, Summary: fmt.Sprintf("Authoring health score is %d.", validation.AuthoringHealth.Score), Details: validation.Issues, Evidence: []string{"/api/v1/scripts/validate"}},
		{Title: "Assertions And Correlation", Status: "needs_review", Summary: fmt.Sprintf("%d assertion(s), %d extractor(s), %d data source(s).", assertions, extractors, len(script.DataSources)), Evidence: []string{"script.actions.assertions", "script.actions.extractors", "script.data_sources"}},
	}
	if validation.AIGuardrails != nil {
		sections = append(sections, AIExplainSection{Title: "AI Guardrails", Status: validation.AIGuardrails.Status, Summary: "AI-generated draft guardrails are enforced before promotion.", Evidence: []string{"ai_guardrails", "/api/v1/ai/authoring/contract"}})
	}
	return "This script is a " + firstNonEmpty(script.Protocol, "unknown") + " workflow with backend-computed validation and authoring health evidence.", sections, []AICitation{
		{Label: "Script validation", Path: "/api/v1/scripts/validate"},
		{Label: "Runtime settings report", Path: "/api/v1/scripts/{id}/runtime-settings/report"},
		{Label: "AI contract", Path: "/api/v1/ai/authoring/contract"},
	}, []string{"Validate, replay, review runtime settings, then approve through an authoring session before reuse."}
}

func explainScenarioDraft(req CreateScenarioRequest) (string, []AIExplainSection, []AICitation, []string) {
	totalVUsers := 0
	for _, group := range req.Groups {
		totalVUsers += group.VUsers
	}
	return "This scenario draft coordinates " + strconv.Itoa(len(req.Groups)) + " group(s) for " + firstNonEmpty(req.Duration, "(duration missing)") + ".", []AIExplainSection{
		{Title: "Load Model", Status: "needs_review", Summary: fmt.Sprintf("%d total VUser(s) across %d group(s).", totalVUsers, len(req.Groups)), Evidence: []string{"scenario.groups", "scenario.duration"}},
		{Title: "Validation", Status: "needs_review", Summary: "Run scenario validation to compile load plan, capacity fit, data fit, and runtime settings review.", Evidence: []string{"/api/v1/scenarios/validate-load-model"}},
	}, []AICitation{{Label: "Scenario validation", Path: "/api/v1/scenarios/validate-load-model"}}, []string{"Run scenario validation before save or execution."}
}

func explainStoredScenario(scenario *StoredScenario) (string, []AIExplainSection, []AICitation, []string) {
	totalVUsers := 0
	for _, ref := range scenario.Scripts {
		totalVUsers += ref.VUsers
	}
	return "Scenario " + scenario.Name + " coordinates stored script groups with release-ready validation hooks.", []AIExplainSection{
		{Title: "Composition", Status: "ready", Summary: fmt.Sprintf("%d script group(s), %d total VUser(s).", len(scenario.Scripts), totalVUsers), Evidence: []string{"scenario.scripts"}},
		{Title: "Runtime Settings", Status: "needs_review", Summary: "Runtime settings are explainable through the scenario report endpoint.", Evidence: []string{"/api/v1/scenarios/" + scenario.ID + "/runtime-settings/report"}},
	}, []AICitation{{Label: "Scenario runtime report", Path: "/api/v1/scenarios/" + scenario.ID + "/runtime-settings/report"}}, []string{"Export the runtime settings report for migration or release review."}
}

func explainRuntimeSettings() (string, []AIExplainSection, []AICitation, []string) {
	support := buildRuntimeSettingsSupportResponse(time.Now().UTC())
	counts := support.StatusCounts
	return "APEX Load classifies runtime settings with a code-owned matrix across supported protocols.", []AIExplainSection{
		{Title: "Coverage", Status: "ready", Summary: fmt.Sprintf("%d runtime setting entries across %d protocol family value(s).", len(support.SupportMatrix), len(support.Protocols)), Evidence: []string{"/api/v1/runtime-settings/support-matrix"}},
		{Title: "Risk", Status: "needs_review", Summary: fmt.Sprintf("%d blocked, %d partial, %d compatibility-only entries.", counts["blocked"], counts["partial"], counts["compatibility_only"]), Evidence: []string{"runtime_settings.status_counts"}},
	}, []AICitation{{Label: "Runtime settings support", Path: "/api/v1/runtime-settings/support-matrix"}}, []string{"Review compatibility-only entries before claiming execution parity."}
}

func explainRunArchiveReport(report *RunArchiveReport, testID string) (string, []AIExplainSection, []AICitation, []string) {
	if report == nil {
		return "No report evidence is available.", nil, nil, []string{"Attach an archive report or target a run archive."}
	}
	root := buildRunRootCauseSummary(testID, report)
	return root.Summary, []AIExplainSection{
			{Title: "Outcome", Status: root.Status, Summary: fmt.Sprintf("%d transaction(s), %d error(s), %.2f%% error rate.", report.Overview.Transactions, report.Overview.Errors, report.Overview.ErrorPct), Evidence: []string{"overview.transactions", "overview.errors", "overview.error_pct"}},
			{Title: "Root Cause", Status: root.Status, Summary: root.Summary, Evidence: []string{"/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/root-cause"}},
			{Title: "Completeness", Status: report.Completeness.Status, Summary: fmt.Sprintf("Archive completeness %.0f%%.", report.Completeness.ScorePct), Evidence: []string{"completeness"}},
		}, []AICitation{
			{Label: "Archive report", Path: "/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/report"},
			{Label: "Root cause", Path: "/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/root-cause"},
		}, root.NextActions
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
