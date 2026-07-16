package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	ciRunMetadataPrefix = "ci:v1:"
	ciRunSchemaVersion  = 1
	ciRunMaxNameBytes   = 256
	ciRunMaxVariables   = 128
	ciRunMaxValueBytes  = 16 << 10
)

type ciRunExecutionRequest struct {
	VUsers    int               `json:"vusers"`
	Duration  string            `json:"duration"`
	RampUp    string            `json:"ramp_up,omitempty"`
	BaseURL   string            `json:"base_url,omitempty"`
	Variables map[string]string `json:"variables,omitempty"`
}

type ciRunGoalsRequest struct {
	P95LatencyMs             *float64 `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64 `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64 `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64 `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int     `json:"max_error_count,omitempty"`
	RequireSLA               bool     `json:"require_sla"`
	RequireResults           bool     `json:"require_results"`
}

func (g ciRunGoalsRequest) evaluated() bool {
	return g.P95LatencyMs != nil || g.MaxErrorPct != nil || g.MaxAvgServerProcessingMs != nil || g.MaxAvgTransferMs != nil || g.MaxErrorCount != nil
}

func (g ciRunGoalsRequest) goalConfig() *GoalConfig {
	if !g.evaluated() {
		return nil
	}
	return &GoalConfig{P95LatencyMs: g.P95LatencyMs, MaxErrorPct: g.MaxErrorPct, MaxAvgServerProcessingMs: g.MaxAvgServerProcessingMs, MaxAvgTransferMs: g.MaxAvgTransferMs, MaxErrorCount: g.MaxErrorCount}
}

type ciRunSource struct {
	Provider          string `json:"provider"`
	ServerURL         string `json:"server_url"`
	APIURL            string `json:"api_url,omitempty"`
	Repository        string `json:"repository"`
	RepositoryID      string `json:"repository_id"`
	Ref               string `json:"ref"`
	SHA               string `json:"sha"`
	WorkflowRef       string `json:"workflow_ref"`
	RunID             string `json:"run_id"`
	RunAttempt        string `json:"run_attempt"`
	Actor             string `json:"actor,omitempty"`
	EventName         string `json:"event_name"`
	Environment       string `json:"environment,omitempty"`
	RunnerEnvironment string `json:"runner_environment,omitempty"`
	HeadRef           string `json:"head_ref,omitempty"`
	HeadSHA           string `json:"head_sha,omitempty"`
	HeadRepository    string `json:"head_repository,omitempty"`
	HeadRepositoryID  string `json:"head_repository_id,omitempty"`
	ScriptPath        string `json:"script_path,omitempty"`
}

type ciRunCreateRequest struct {
	ProjectID    string                `json:"project_id"`
	Name         string                `json:"name,omitempty"`
	ReleaseID    string                `json:"release_id,omitempty"`
	ScriptPath   string                `json:"script_path,omitempty"`
	ScriptID     string                `json:"script_id,omitempty"`
	Script       json.RawMessage       `json:"script,omitempty"`
	Execution    ciRunExecutionRequest `json:"execution"`
	Goals        ciRunGoalsRequest     `json:"goals"`
	Source       ciRunSource           `json:"source"`
	PublishCheck *bool                 `json:"publish_check,omitempty"`
}

func (r ciRunCreateRequest) shouldPublishCheck() bool {
	return r.PublishCheck == nil || *r.PublishCheck
}

type ciRunMetadata struct {
	SchemaVersion       int         `json:"schema_version"`
	Source              ciRunSource `json:"source"`
	ScriptPath          string      `json:"script_path,omitempty"`
	ScriptIdentity      string      `json:"script_identity"`
	RequireSLA          bool        `json:"require_sla"`
	RequireResults      bool        `json:"require_results"`
	PublishCheck        bool        `json:"publish_check"`
	CheckInstallationID int64       `json:"check_installation_id,omitempty"`
	PolicyRevision      int64       `json:"policy_revision"`
	IdempotencySHA      string      `json:"idempotency_sha256"`
}

type ciRunResponse struct {
	SchemaVersion  int      `json:"schema_version"`
	TestID         string   `json:"test_id"`
	Status         string   `json:"status"`
	Outcome        string   `json:"outcome"`
	SLAEvaluated   bool     `json:"sla_evaluated"`
	SLABreached    bool     `json:"sla_breached"`
	SLADetails     []string `json:"sla_details,omitempty"`
	Transactions   int64    `json:"transactions"`
	Errors         int64    `json:"errors"`
	TPS            float64  `json:"tps"`
	DurationMs     int64    `json:"duration_ms"`
	ResultsPresent bool     `json:"results_present"`
	StatusURL      string   `json:"status_url"`
	ReportURL      string   `json:"report_url"`
	CheckURL       string   `json:"check_url,omitempty"`
	Error          string   `json:"error,omitempty"`
}

func resolveCIRunCreateProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req ciRunCreateRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return "", http.StatusBadRequest, "project_id is required"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func (s *Server) handleCreateCIRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req ciRunCreateRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ProjectID = normalizeProjectID(req.ProjectID)
	project, err := s.store.GetProject(req.ProjectID)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	githubPolicy := normalizeGitHubProjectPolicy(project.Settings.GitHub)
	policy := githubPolicy.Actions
	if !policy.Enabled {
		writeError(w, http.StatusForbidden, "GitHub Actions execution is disabled for this project")
		return
	}
	if claims, ok := ciClaimsFromContext(r.Context()); ok {
		if err := claims.matchCIRunSource(req.ProjectID, req.Source); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		// Persist and enforce the cryptographically verified claims, never
		// caller-supplied environment or runner metadata.
		req.Source.Environment = claims.Environment
		req.Source.RunnerEnvironment = claims.RunnerEnvironment
	} else if _, ok := AuthenticatedServiceAccountFromContext(r.Context()); ok {
		verifiedSource, verifyErr := s.verifyAPIKeyCISource(r.Context(), githubPolicy, req.Source)
		if verifyErr != nil {
			writeError(w, http.StatusForbidden, "GitHub API-key fallback verification failed: "+verifyErr.Error())
			return
		}
		req.Source = verifiedSource
	} else if _, ok := AuthenticatedUserFromContext(r.Context()); ok {
		writeError(w, http.StatusForbidden, "controller-backed CI runs require GitHub OIDC or a ci_runner API key")
		return
	}
	if err := validateCIRunSource(policy, req.Source); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	idempotencyKey, err := validateCIRunIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	script, scriptIdentity, inline, err := s.resolveCIRunScript(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateCIRunRequest(req, policy, script); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	testID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-run:"+req.ProjectID+":"+idempotencyKey)).String()
	metadata := ciRunMetadata{SchemaVersion: ciRunSchemaVersion, Source: req.Source, ScriptPath: firstNonEmpty(req.ScriptPath, req.Source.ScriptPath), ScriptIdentity: scriptIdentity, RequireSLA: req.Goals.RequireSLA, RequireResults: req.Goals.RequireResults, PublishCheck: req.shouldPublishCheck(), CheckInstallationID: githubPolicy.AppInstallationID, PolicyRevision: policy.Revision, IdempotencySHA: sha256String(idempotencyKey)}
	existing, existingOK := s.runner.GetTest(testID)
	if existingOK {
		if err := validateExistingCIRun(existing, req.ProjectID, metadata); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	var checkClient *githubClient
	intentHasNoTest := false
	if metadata.PublishCheck {
		checkClient, err = s.validateCICheckPublishing(r.Context(), githubPolicy, req.Source)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// The outbox intent is durable before test materialization, so a crash
		// cannot leave an accepted controller-backed run without its required
		// GitHub Check. Normal failures before materialization clean it up.
		if err := s.saveCIGitHubCheckIntent(testID, req.ProjectID, metadata); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist required GitHub Check intent: "+err.Error())
			return
		}
		intentHasNoTest = !existingOK
		defer func() {
			if intentHasNoTest {
				// If materialization won immediately before an unexpected panic,
				// preserve the outbox record for reconciliation.
				if _, exists := s.runner.GetTest(testID); exists {
					return
				}
				s.deleteCIGitHubCheckIntent(testID)
			}
		}()
	}
	if existingOK {
		if metadata.PublishCheck {
			if _, err := s.createCIGitHubCheck(r.Context(), checkClient, githubPolicy, existing, metadata); err != nil {
				writeError(w, http.StatusBadGateway, "failed to recover required GitHub Check: "+err.Error())
				return
			}
		}
		response, _ := s.buildCIRunResponse(r.Context(), existing)
		writeJSON(w, http.StatusAccepted, response)
		return
	}
	// Compile every CI run from the exact script object that passed policy
	// validation. This also closes the script_id read/validate/compile race: an
	// editor can update the persistent library script concurrently, but that
	// update cannot change the already-approved CI execution snapshot.
	script = cloneScriptDefinition(script)
	applyCIRuntimeTargetPolicy(script, policy)
	script.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("apexload-ci-script:"+testID)).String()
	script.ProjectID = req.ProjectID
	script.FolderID = nil
	if inline {
		script.GeneratedBy = "github-actions-ci-inline"
	} else {
		script.GeneratedBy = "github-actions-ci-library-snapshot"
	}
	stored, storeErr := s.runner.StoreScriptDurable(script)
	if storeErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to snapshot CI script: "+storeErr.Error())
		return
	}
	effectiveScriptID := stored.ID
	defer func() {
		if _, cleanupErr := s.runner.DeleteScriptDurableInProject(stored.ID, req.ProjectID); cleanupErr != nil {
			slogWarnCIScriptCleanup(stored.ID, cleanupErr)
		}
	}()
	materializationKey, err := encodeCIRunMetadata(metadata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode CI run metadata")
		return
	}
	create := CreateTestRequest{
		Name: firstNonEmpty(strings.TrimSpace(req.Name), defaultCIRunName(req.Source, script)), RequestedTestID: testID, MaterializationKey: materializationKey,
		Duration: strings.TrimSpace(req.Execution.Duration), ReleaseID: strings.TrimSpace(req.ReleaseID), ProjectID: req.ProjectID,
		ScriptID: effectiveScriptID, VUsers: req.Execution.VUsers, RampUp: strings.TrimSpace(req.Execution.RampUp), GoalConfig: req.Goals.goalConfig(),
		RuntimeSettings: &RuntimeSettingsOverride{BaseURL: strings.TrimSpace(req.Execution.BaseURL), Env: cloneCIStringMap(req.Execution.Variables)},
	}
	test, err := s.runner.CreateTest(create)
	if err != nil {
		if existing, ok := s.runner.GetTest(testID); ok {
			if validationErr := validateExistingCIRun(existing, req.ProjectID, metadata); validationErr == nil {
				intentHasNoTest = false
				if metadata.PublishCheck {
					if _, checkErr := s.createCIGitHubCheck(r.Context(), checkClient, githubPolicy, existing, metadata); checkErr != nil {
						writeError(w, http.StatusBadGateway, "failed to recover required GitHub Check: "+checkErr.Error())
						return
					}
				}
				response, _ := s.buildCIRunResponse(r.Context(), existing)
				writeJSON(w, http.StatusAccepted, response)
				return
			}
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	intentHasNoTest = false
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if actorType == "user" || actorType == "service_account" {
		s.runner.AttributeTestCreated(test.ID, actorSubject, actorDisplayName)
	}
	if metadata.PublishCheck {
		if _, err := s.createCIGitHubCheck(r.Context(), checkClient, githubPolicy, test, metadata); err != nil {
			_ = s.runner.AbortTest(test.ID)
			writeError(w, http.StatusBadGateway, "failed to create required GitHub Check: "+err.Error())
			return
		}
	}
	if err := s.runner.StartTest(test.ID); err != nil {
		_ = s.runner.AbortTest(test.ID)
		if latest, ok := s.runner.GetTest(test.ID); ok {
			_, _ = s.buildCIRunResponse(r.Context(), latest)
		}
		writeError(w, http.StatusBadRequest, "failed to start CI run: "+err.Error())
		return
	}
	if actorType == "user" || actorType == "service_account" {
		s.runner.AttributeTestStarted(test.ID, actorSubject, actorDisplayName)
	}
	test, _ = s.runner.GetTest(test.ID)
	response, _ := s.buildCIRunResponse(r.Context(), test)
	s.recordAuditEvent(r, "ci.run.create", "success", "test", test.ID, test.ProjectID, map[string]any{"repository": req.Source.Repository, "workflow_ref": req.Source.WorkflowRef, "run_id": req.Source.RunID, "run_attempt": req.Source.RunAttempt, "script_identity": scriptIdentity, "publish_check": metadata.PublishCheck})
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) handleGetCIRun(w http.ResponseWriter, r *http.Request) {
	test, ok := s.runner.GetTest(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "CI run not found")
		return
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "CI run not found")
		return
	}
	if claims, ok := ciClaimsFromContext(r.Context()); ok {
		if err := claims.matchCIRunSource(test.ProjectID, metadata.Source); err != nil {
			writeError(w, http.StatusForbidden, "CI token is not bound to this run")
			return
		}
	}
	response, syncErr := s.buildCIRunResponse(r.Context(), test)
	if syncErr != nil && metadata.PublishCheck {
		response.Error = firstNonEmpty(response.Error, "GitHub Check update failed: "+syncErr.Error())
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleAbortCIRun(w http.ResponseWriter, r *http.Request) {
	test, ok := s.runner.GetTest(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "CI run not found")
		return
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "CI run not found")
		return
	}
	if claims, ok := ciClaimsFromContext(r.Context()); ok {
		if err := claims.matchCIRunSource(test.ProjectID, metadata.Source); err != nil {
			writeError(w, http.StatusForbidden, "CI token is not bound to this run")
			return
		}
	}
	if !ciRunTerminal(test.Status) {
		if err := s.runner.AbortTest(test.ID); err != nil {
			writeError(w, http.StatusConflict, "failed to abort CI run: "+err.Error())
			return
		}
		_, actorSubject, actorDisplayName := s.resolveAuditActor(r)
		s.runner.AttributeTestStopped(test.ID, actorSubject, actorDisplayName)
	}
	latest, _ := s.runner.GetTest(test.ID)
	response, _ := s.buildCIRunResponse(r.Context(), latest)
	s.recordAuditEvent(r, "ci.run.abort", "success", "test", test.ID, test.ProjectID, map[string]any{"repository": metadata.Source.Repository, "run_id": metadata.Source.RunID})
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) resolveCIRunScript(req ciRunCreateRequest) (*dsl.Script, string, bool, error) {
	raw := strings.TrimSpace(string(req.Script))
	hasInline := raw != "" && raw != "null"
	hasID := strings.TrimSpace(req.ScriptID) != ""
	if hasInline == hasID {
		return nil, "", false, fmt.Errorf("exactly one of script or script_id is required")
	}
	if hasInline {
		script, err := dsl.Parse(req.Script)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid inline script: %w", err)
		}
		digest := sha256.Sum256(req.Script)
		return script, "inline:sha256:" + hex.EncodeToString(digest[:]), true, nil
	}
	stored, ok := s.runner.GetScript(req.ScriptID)
	if !ok || stored == nil || stored.Script == nil {
		return nil, "", false, fmt.Errorf("script_id was not found")
	}
	if normalizeProjectID(stored.ProjectID) != normalizeProjectID(req.ProjectID) {
		return nil, "", false, fmt.Errorf("script_id does not belong to project_id")
	}
	return cloneScriptDefinition(stored.Script), "script_id:" + stored.ID, false, nil
}

func validateCIRunRequest(req ciRunCreateRequest, policy storage.GitHubActionsPolicy, script *dsl.Script) error {
	if script == nil {
		return fmt.Errorf("script is required")
	}
	if len(strings.TrimSpace(req.Name)) > ciRunMaxNameBytes {
		return fmt.Errorf("name exceeds %d bytes", ciRunMaxNameBytes)
	}
	if req.Execution.VUsers <= 0 {
		return fmt.Errorf("execution.vusers must be greater than zero")
	}
	if req.Execution.VUsers > policy.MaxVUsers {
		return fmt.Errorf("execution.vusers exceeds the project GitHub Actions policy maximum of %d", policy.MaxVUsers)
	}
	duration, err := time.ParseDuration(strings.TrimSpace(req.Execution.Duration))
	if err != nil || duration <= 0 {
		return fmt.Errorf("execution.duration must be a positive Go duration")
	}
	maxDuration, ok := checkedDuration(policy.MaxDurationMinutes, time.Minute)
	if !ok || maxDuration <= 0 || duration > maxDuration {
		return fmt.Errorf("execution.duration exceeds the project GitHub Actions policy maximum of %d minutes", policy.MaxDurationMinutes)
	}
	if req.Execution.RampUp != "" {
		if ramp, err := time.ParseDuration(req.Execution.RampUp); err != nil || ramp < 0 || ramp > duration {
			return fmt.Errorf("execution.ramp_up must be a non-negative duration no longer than execution.duration")
		}
	}
	if len(req.Execution.Variables) > ciRunMaxVariables {
		return fmt.Errorf("execution.variables exceeds %d entries", ciRunMaxVariables)
	}
	for key, value := range req.Execution.Variables {
		trimmed := strings.TrimSpace(key)
		lower := strings.ToLower(trimmed)
		if trimmed == "" || len(trimmed) > 128 || strings.ContainsAny(trimmed, "\r\n=\x00") {
			return fmt.Errorf("execution.variables contains an invalid key")
		}
		if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "api_key") {
			return fmt.Errorf("execution.variables is for non-secret overrides; %q must be a controller-managed project secret", key)
		}
		if len(value) > ciRunMaxValueBytes || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("execution.variables[%q] is invalid or too large", key)
		}
	}
	if req.Goals.RequireSLA && !req.Goals.evaluated() {
		return fmt.Errorf("goals.require_sla is true but no SLA threshold was supplied")
	}
	if err := validateCIGoals(req.Goals); err != nil {
		return err
	}
	if err := validateCIScriptHasNoPlaintextSecrets(script); err != nil {
		return err
	}
	if err := validateCIScriptAssets(script); err != nil {
		return err
	}
	return validateCIScriptPolicy(policy, script, req.Execution.BaseURL)
}

func validateCIGoals(goals ciRunGoalsRequest) error {
	if goals.P95LatencyMs != nil && *goals.P95LatencyMs < 0 {
		return fmt.Errorf("goals.p95_latency_ms must be non-negative")
	}
	if goals.MaxErrorPct != nil && (*goals.MaxErrorPct < 0 || *goals.MaxErrorPct > 100) {
		return fmt.Errorf("goals.max_error_pct must be between 0 and 100")
	}
	if goals.MaxAvgServerProcessingMs != nil && *goals.MaxAvgServerProcessingMs < 0 {
		return fmt.Errorf("goals.max_avg_server_processing_ms must be non-negative")
	}
	if goals.MaxAvgTransferMs != nil && *goals.MaxAvgTransferMs < 0 {
		return fmt.Errorf("goals.max_avg_transfer_ms must be non-negative")
	}
	if goals.MaxErrorCount != nil && *goals.MaxErrorCount < 0 {
		return fmt.Errorf("goals.max_error_count must be non-negative")
	}
	return nil
}

func validateCIRunSource(policy storage.GitHubActionsPolicy, source ciRunSource) error {
	if !strings.EqualFold(strings.TrimSpace(source.Provider), "github") {
		return fmt.Errorf("source.provider must be github")
	}
	if strings.TrimRight(strings.TrimSpace(source.ServerURL), "/") != policy.ServerURL {
		return fmt.Errorf("source.server_url is not trusted by project policy")
	}
	if source.APIURL != "" && !trustedGitHubAPIURL(policy.ServerURL, source.APIURL) {
		return fmt.Errorf("source.api_url is not trusted for the configured GitHub server")
	}
	identity := githubActionsOIDCClaims{Repository: source.Repository, RepositoryID: source.RepositoryID, Ref: source.Ref, SHA: source.SHA, WorkflowRef: source.WorkflowRef, RunID: source.RunID, RunAttempt: source.RunAttempt, Actor: source.Actor, EventName: source.EventName, Environment: source.Environment, RunnerEnvironment: source.RunnerEnvironment}
	if err := validateGitHubActionsIdentity(policy, identity); err != nil {
		return fmt.Errorf("source metadata is not allowed by project policy: %w", err)
	}
	if strings.EqualFold(source.EventName, "pull_request_target") {
		return fmt.Errorf("pull_request_target is prohibited for controller-backed execution")
	}
	if strings.EqualFold(source.EventName, "pull_request") {
		if !strings.EqualFold(strings.TrimSpace(source.HeadRepository), strings.TrimSpace(source.Repository)) || strings.TrimSpace(source.HeadRepositoryID) != strings.TrimSpace(source.RepositoryID) {
			return fmt.Errorf("fork pull requests cannot create controller-backed CI runs")
		}
	}
	return nil
}

// verifyAPIKeyCISource prevents the API-key fallback from turning caller-supplied
// GitHub metadata into an identity assertion. The controller GitHub App resolves
// the exact workflow run and independently binds the repository, workflow, ref,
// commit, attempt, event, actor, and same-repository pull-request provenance.
// Environment and runner claims are job-scoped and are not available from the
// workflow-run REST resource, so policies depending on either require OIDC.
func (s *Server) verifyAPIKeyCISource(ctx context.Context, projectPolicy storage.GitHubProjectPolicy, source ciRunSource) (ciRunSource, error) {
	projectPolicy = normalizeGitHubProjectPolicy(projectPolicy)
	policy := projectPolicy.Actions
	if len(policy.AllowedEnvironments) > 0 || policy.RequiredRunnerEnvironment != "" {
		return source, fmt.Errorf("project policy requires job-scoped environment or runner claims; use GitHub OIDC")
	}
	settings := s.currentGitHubSettings()
	if !settings.Enabled || !settings.appConfigured() {
		return source, fmt.Errorf("a configured controller GitHub App is required")
	}
	if projectPolicy.AppInstallationID <= 0 {
		return source, fmt.Errorf("a project GitHub App installation id is required")
	}
	if !sameURLOrigin(settings.WebBaseURL, source.ServerURL) {
		return source, fmt.Errorf("controller GitHub settings do not match source.server_url")
	}
	owner, repo, ok := splitOwnerRepo(source.Repository)
	if !ok {
		return source, fmt.Errorf("source.repository must be owner/repo")
	}
	client, err := s.githubInstallationClient(ctx, settings, projectPolicy.AppInstallationID, owner, repo)
	if err != nil {
		return source, fmt.Errorf("authenticate project GitHub App installation: %w", err)
	}
	run, err := client.GetWorkflowRun(ctx, owner, repo, source.RunID)
	if err != nil {
		return source, fmt.Errorf("resolve GitHub workflow run: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(source.EventName), "pull_request") {
		pulls, pullErr := client.ListPullRequestsForCommit(ctx, owner, repo, run.HeadSHA)
		if pullErr != nil {
			return source, fmt.Errorf("resolve GitHub workflow run pull request: %w", pullErr)
		}
		run.PullRequests = pulls
	}
	if err := validateGitHubWorkflowRunSource(run, source); err != nil {
		return source, err
	}
	// Never persist unverified job-scoped values supplied alongside a fallback
	// key. Empty policy fields make these values informational at best; clearing
	// them keeps audit records explicit about what the controller proved.
	source.Environment = ""
	source.RunnerEnvironment = ""
	return source, nil
}

func validateGitHubWorkflowRunSource(run *githubWorkflowRun, source ciRunSource) error {
	if run == nil {
		return fmt.Errorf("GitHub workflow run response is empty")
	}
	runID, err := strconv.ParseInt(strings.TrimSpace(source.RunID), 10, 64)
	if err != nil || runID <= 0 || run.ID != runID {
		return fmt.Errorf("GitHub workflow run id does not match CI source metadata")
	}
	if fmt.Sprintf("%d", run.Repository.ID) != strings.TrimSpace(source.RepositoryID) ||
		!strings.EqualFold(strings.TrimSpace(run.Repository.FullName), strings.TrimSpace(source.Repository)) {
		return fmt.Errorf("GitHub workflow run repository does not match CI source metadata")
	}
	if !strings.EqualFold(strings.TrimSpace(run.Event), strings.TrimSpace(source.EventName)) {
		return fmt.Errorf("GitHub workflow run event does not match CI source metadata")
	}
	if run.RunAttempt <= 0 || fmt.Sprintf("%d", run.RunAttempt) != strings.TrimSpace(source.RunAttempt) {
		return fmt.Errorf("GitHub workflow run attempt does not match CI source metadata")
	}
	if strings.TrimSpace(source.Actor) != "" && !strings.EqualFold(strings.TrimSpace(run.Actor.Login), strings.TrimSpace(source.Actor)) {
		return fmt.Errorf("GitHub workflow run actor does not match CI source metadata")
	}
	if err := validateGitHubWorkflowPath(run.Path, source); err != nil {
		return err
	}

	ref := strings.TrimSpace(source.Ref)
	if strings.EqualFold(strings.TrimSpace(source.EventName), "pull_request") {
		if strings.TrimSpace(source.HeadSHA) == "" || !strings.EqualFold(strings.TrimSpace(run.HeadSHA), strings.TrimSpace(source.HeadSHA)) {
			return fmt.Errorf("GitHub workflow run head commit does not match CI source metadata")
		}
		if run.HeadRepository.ID != run.Repository.ID || !strings.EqualFold(strings.TrimSpace(run.HeadRepository.FullName), strings.TrimSpace(run.Repository.FullName)) {
			return fmt.Errorf("fork pull requests cannot use the GitHub API-key fallback")
		}
		matched := false
		for _, pull := range run.PullRequests {
			if ref != fmt.Sprintf("refs/pull/%d/merge", pull.Number) {
				continue
			}
			if pull.Head.Repo.ID != run.Repository.ID || !strings.EqualFold(strings.TrimSpace(pull.Head.Repo.FullName), strings.TrimSpace(run.Repository.FullName)) {
				return fmt.Errorf("fork pull requests cannot use the GitHub API-key fallback")
			}
			if strings.TrimSpace(source.HeadSHA) == "" || !strings.EqualFold(strings.TrimSpace(pull.Head.SHA), strings.TrimSpace(source.HeadSHA)) {
				return fmt.Errorf("GitHub pull-request head commit does not match CI source metadata")
			}
			if strings.TrimSpace(pull.MergeCommitSHA) == "" || !strings.EqualFold(strings.TrimSpace(pull.MergeCommitSHA), strings.TrimSpace(source.SHA)) {
				return fmt.Errorf("GitHub pull-request merge commit does not match CI source metadata")
			}
			matched = true
			break
		}
		if !matched {
			return fmt.Errorf("GitHub workflow run pull request does not match source.ref")
		}
		return nil
	}

	if strings.TrimSpace(run.HeadSHA) == "" || !strings.EqualFold(strings.TrimSpace(run.HeadSHA), strings.TrimSpace(source.SHA)) {
		return fmt.Errorf("GitHub workflow run commit does not match CI source metadata")
	}
	branch := strings.TrimSpace(run.HeadBranch)
	if branch == "" || (ref != "refs/heads/"+branch && ref != "refs/tags/"+branch) {
		return fmt.Errorf("GitHub workflow run branch or tag does not match source.ref")
	}
	return nil
}

func validateGitHubWorkflowPath(runPath string, source ciRunSource) error {
	path := strings.TrimSpace(runPath)
	if at := strings.LastIndex(path, "@"); at >= 0 {
		path = path[:at]
	}
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	workflowRef := strings.TrimSpace(source.WorkflowRef)
	at := strings.LastIndex(workflowRef, "@")
	if path == "" || at <= 0 || at == len(workflowRef)-1 {
		return fmt.Errorf("GitHub workflow path is missing or invalid")
	}
	workflowPath, workflowSourceRef := workflowRef[:at], workflowRef[at+1:]
	expectedPath := strings.TrimSpace(source.Repository) + "/" + path
	if !strings.EqualFold(workflowPath, expectedPath) {
		return fmt.Errorf("GitHub workflow path does not match source.workflow_ref")
	}
	if workflowSourceRef != strings.TrimSpace(source.Ref) {
		return fmt.Errorf("GitHub workflow ref does not match source.ref")
	}
	return nil
}

func trustedGitHubAPIURL(serverURL, apiURL string) bool {
	if sameURLOrigin(apiURL, serverURL) {
		return true
	}
	server, serverErr := url.Parse(strings.TrimSpace(serverURL))
	api, apiErr := url.Parse(strings.TrimSpace(apiURL))
	if serverErr != nil || apiErr != nil {
		return false
	}
	// GitHub.com deliberately serves its REST API from a separate origin.
	// GHES uses the configured server origin (normally under /api/v3).
	return strings.EqualFold(server.Scheme, "https") && strings.EqualFold(server.Hostname(), "github.com") &&
		strings.EqualFold(api.Scheme, "https") && strings.EqualFold(api.Hostname(), "api.github.com") && api.Port() == ""
}

func (claims *ciBearerClaims) matchCIRunSource(projectID string, source ciRunSource) error {
	if claims == nil || normalizeProjectID(claims.ProjectID) != normalizeProjectID(projectID) {
		return fmt.Errorf("CI token project does not match")
	}
	checks := [][3]string{
		{"repository", claims.Repository, source.Repository}, {"repository_id", claims.RepositoryID, source.RepositoryID}, {"ref", claims.Ref, source.Ref}, {"sha", claims.SHA, source.SHA},
		{"workflow_ref", claims.WorkflowRef, source.WorkflowRef}, {"run_id", claims.RunID, source.RunID}, {"run_attempt", claims.RunAttempt, source.RunAttempt}, {"event_name", claims.EventName, source.EventName},
	}
	for _, check := range checks {
		if strings.TrimSpace(check[1]) != strings.TrimSpace(check[2]) {
			return fmt.Errorf("source.%s does not match the exchanged GitHub identity", check[0])
		}
	}
	return nil
}

func validateCIRunIdempotencyKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if len(key) < 16 || len(key) > 512 {
		return "", fmt.Errorf("Idempotency-Key must contain 16 to 512 characters")
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return "", fmt.Errorf("Idempotency-Key must contain visible ASCII characters only")
		}
	}
	return key, nil
}

func validateCIScriptHasNoPlaintextSecrets(script *dsl.Script) error {
	raw, err := json.Marshal(script)
	if err != nil {
		return fmt.Errorf("encode script for secret validation: %w", err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode script for secret validation: %w", err)
	}
	return walkCIScriptSecrets(value, "script")
}

func walkCIScriptSecrets(value any, fieldPath string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(strings.TrimSpace(key))
			nextPath := fieldPath + "." + key
			if stringValue, ok := child.(string); ok && strings.TrimSpace(stringValue) != "" {
				if isSensitiveScriptKey(lower) && !isSecretReferenceKey(lower) && !isCISecretTemplate(stringValue) {
					return fmt.Errorf("%s contains plaintext credential material; use a controller-managed {{secret.*}} reference", nextPath)
				}
				if (lower == "body" || lower == "value") && (strings.HasPrefix(strings.TrimSpace(stringValue), "{") || strings.HasPrefix(strings.TrimSpace(stringValue), "[")) {
					var embedded any
					if json.Unmarshal([]byte(stringValue), &embedded) == nil {
						if err := walkCIScriptSecrets(embedded, nextPath); err != nil {
							return err
						}
					}
				}
				if strings.Contains(stringValue, "://") && maskScriptURL(stringValue) != stringValue {
					return fmt.Errorf("%s embeds credentials in a URL; use a controller-managed {{secret.*}} reference", nextPath)
				}
			}
			if err := walkCIScriptSecrets(child, nextPath); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := walkCIScriptSecrets(child, fmt.Sprintf("%s[%d]", fieldPath, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func isCISecretTemplate(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "{{secret.") && strings.HasSuffix(trimmed, "}}") && strings.Count(trimmed, "{{") == 1
}

func validateCIScriptAssets(script *dsl.Script) error {
	if script == nil {
		return fmt.Errorf("script is required")
	}
	for label, values := range map[string][]string{
		"config.grpc.proto_files":           script.Config.GRPC.ProtoFiles,
		"config.java_vuser.classpath":       script.Config.JavaVuser.Classpath,
		"config.http.tls.client_cert_file":  {script.Config.HTTP.TLS.ClientCertFile},
		"config.http.tls.client_key_file":   {script.Config.HTTP.TLS.ClientKeyFile},
		"config.http.tls.ca_cert_file":      {script.Config.HTTP.TLS.CACertFile},
		"config.kafka.tls.client_cert_file": kafkaTLSAsset(script, "cert"),
		"config.kafka.tls.client_key_file":  kafkaTLSAsset(script, "key"),
		"config.kafka.tls.ca_cert_file":     kafkaTLSAsset(script, "ca"),
		"config.browser.har_artifact_dir":   {script.Config.Browser.HARArtifactDir},
		"config.java_vuser.runner_command":  {script.Config.JavaVuser.RunnerCommand},
		"config.java_vuser.harness_jar":     {script.Config.JavaVuser.HarnessJar},
		"config.java_vuser.work_dir":        {script.Config.JavaVuser.WorkDir},
	} {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return fmt.Errorf("%s cannot use runner/controller filesystem paths in a CI run; use a project-bound controller asset reference", label)
			}
		}
	}
	for index, certificate := range script.Config.Browser.ClientCertificates {
		if strings.TrimSpace(certificate.CertPath) != "" || strings.TrimSpace(certificate.KeyPath) != "" || strings.TrimSpace(certificate.PfxPath) != "" {
			return fmt.Errorf("config.browser.client_certificates[%d] cannot use filesystem paths in a CI run; use a project-bound controller asset reference", index)
		}
	}
	for _, source := range script.DataSources {
		if strings.TrimSpace(source.Path) != "" && strings.TrimSpace(source.StorageRef) == "" {
			return fmt.Errorf("inline CI scripts cannot reference runner-local data source paths; inline the data or use a controller-managed storage_ref")
		}
	}
	var visit func([]dsl.Action) error
	visit = func(actions []dsl.Action) error {
		for _, action := range actions {
			if strings.TrimSpace(action.BodyFile) != "" {
				return fmt.Errorf("inline CI scripts cannot reference runner-local body_file assets")
			}
			if strings.EqualFold(strings.TrimSpace(action.Type), "browser_set_files") && browserSetFilesHasPath(action) {
				return fmt.Errorf("browser_set_files cannot read filesystem paths in a CI run; use a project-bound controller asset reference")
			}
			if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
				children, err := ciHTTPSequenceChildren(action)
				if err != nil {
					return err
				}
				if err := visit(children); err != nil {
					return err
				}
			}
			if action.Control != nil {
				if err := visit(action.Control.Steps); err != nil {
					return err
				}
				if err := visit(action.Control.Else); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(script.Init); err != nil {
		return err
	}
	if err := visit(script.Actions); err != nil {
		return err
	}
	return visit(script.Teardown)
}

func kafkaTLSAsset(script *dsl.Script, kind string) []string {
	if script == nil || script.Config.Kafka.TLS == nil {
		return nil
	}
	switch kind {
	case "cert":
		return []string{script.Config.Kafka.TLS.ClientCertFile}
	case "key":
		return []string{script.Config.Kafka.TLS.ClientKeyFile}
	default:
		return []string{script.Config.Kafka.TLS.CACertFile}
	}
}

func browserSetFilesHasPath(action dsl.Action) bool {
	if strings.TrimSpace(action.Value) != "" {
		return true
	}
	for _, key := range []string{"file", "path", "files"} {
		if value, exists := action.Params[key]; exists && value != nil {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return true
				}
			case []string:
				if len(typed) > 0 {
					return true
				}
			case []any:
				if len(typed) > 0 {
					return true
				}
			default:
				return true
			}
		}
	}
	return false
}

func validateCIScriptPolicy(policy storage.GitHubActionsPolicy, script *dsl.Script, baseURLOverride string) error {
	protocol := strings.ToLower(strings.TrimSpace(script.Protocol))
	if !githubPolicyAllowsPattern(protocol, policy.AllowedProtocols, true) {
		return fmt.Errorf("script protocol %q is not allowed by project policy", protocol)
	}
	hosts, deferredTarget, err := collectCITargetHosts(script, baseURLOverride)
	if err != nil {
		return err
	}
	if len(hosts) == 0 && !deferredTarget {
		return fmt.Errorf("script target host could not be resolved; use a concrete policy-approved target")
	}
	for _, host := range hosts {
		if !githubPolicyAllowsPattern(host, policy.AllowedTargetHosts, true) {
			return fmt.Errorf("target host %q is not allowed by project policy", host)
		}
	}
	return nil
}

func applyCIRuntimeTargetPolicy(script *dsl.Script, policy storage.GitHubActionsPolicy) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	script.Metadata.RuntimePolicy = &dsl.ScriptRuntimePolicy{AllowedTargetHosts: append([]string(nil), policy.AllowedTargetHosts...)}
}

func collectCITargetHosts(script *dsl.Script, baseURLOverride string) ([]string, bool, error) {
	set := map[string]struct{}{}
	deferredTarget := false
	add := func(raw, label string, required bool) error {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		if isCISecretTemplate(raw) {
			deferredTarget = true
			return nil
		}
		if strings.Contains(raw, "{{") || strings.Contains(raw, "}}") {
			return fmt.Errorf("%s constructs a dynamic target; use a concrete policy-approved target", label)
		}
		hosts, err := protocol.TargetReferenceHosts(raw)
		if err != nil {
			if required {
				return fmt.Errorf("%s is not a valid concrete target: %w", label, err)
			}
			return nil
		}
		for _, host := range hosts {
			set[host] = struct{}{}
		}
		return nil
	}
	addDSN := func(raw, label string) error {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		if isCISecretTemplate(raw) {
			deferredTarget = true
			return nil
		}
		if strings.Contains(raw, "{{") || strings.Contains(raw, "}}") {
			return fmt.Errorf("%s constructs a dynamic target; use a controller-managed DSN secret", label)
		}
		hosts, err := protocol.TargetDSNHosts(script.Config.Database.Driver, raw)
		if err != nil {
			return fmt.Errorf("%s is not a valid concrete target: %w", label, err)
		}
		for _, host := range hosts {
			set[host] = struct{}{}
		}
		return nil
	}
	baseURL := firstNonEmpty(strings.TrimSpace(baseURLOverride), script.Config.BaseURL)
	if err := add(baseURL, "base_url", true); err != nil {
		return nil, false, err
	}
	if err := addDSN(script.Config.Database.DSN, "config.database.dsn"); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(script.Config.Database.DSNSecret) != "" {
		deferredTarget = true
	}
	for _, entry := range []struct{ raw, label string }{
		{script.Config.JMS.BrokerURL, "config.jms.broker_url"}, {script.Config.EventHubs.Namespace, "config.eventhubs.namespace"},
		{script.Config.EventHubs.ConnectionString, "config.eventhubs.connection_string"},
		{script.Config.Network.Host, "config.network.host"}, {script.Config.Network.Address, "config.network.address"},
		{script.Config.Kafka.SchemaRegistryURL, "config.kafka.schema_registry_url"}, {script.Config.HTTP.Proxy.URL, "config.http.proxy.url"},
		{script.Config.Browser.Proxy.Server, "config.browser.proxy.server"},
	} {
		if err := add(entry.raw, entry.label, true); err != nil {
			return nil, false, err
		}
	}
	if strings.TrimSpace(script.Config.EventHubs.ConnectionStringSecret) != "" {
		deferredTarget = true
	}
	for i, broker := range script.Config.Kafka.Brokers {
		if err := add(broker, fmt.Sprintf("config.kafka.brokers[%d]", i), true); err != nil {
			return nil, false, err
		}
	}
	for i, metric := range script.Config.Observability.ExternalMetrics {
		if err := add(metric.URL, fmt.Sprintf("config.observability.external_metrics[%d].url", i), true); err != nil {
			return nil, false, err
		}
	}
	protocol := strings.ToLower(strings.TrimSpace(script.Protocol))
	var visit func([]dsl.Action) error
	visit = func(actions []dsl.Action) error {
		for index, action := range actions {
			target := strings.TrimSpace(action.Target)
			actionType := strings.ToLower(strings.TrimSpace(action.Type))
			// JMS destinations, Kafka topics, Event Hub names, gRPC methods, and
			// database action targets are logical resources rather than network
			// endpoints. Their concrete connection endpoints come from config and
			// are enforced again by the protocol immediately before connection.
			outboundTarget := strings.Contains(target, "://") || protocol == "http" || protocol == "network" || actionType == "browser_navigate" || actionType == "browser_open_window"
			if outboundTarget && target != "" {
				if err := add(target, fmt.Sprintf("action[%d].target", index), protocol == "network" || strings.Contains(target, "://")); err != nil {
					return err
				}
			}
			switch actionType {
			case "http_oauth2_token":
				if err := add(ciActionParamString(action.Params, "token_url"), fmt.Sprintf("action[%d].params.token_url", index), true); err != nil {
					return err
				}
			case "browser_open_window":
				if target == "" {
					if err := add(ciActionParamString(action.Params, "url"), fmt.Sprintf("action[%d].params.url", index), true); err != nil {
						return err
					}
				}
			case "http_sequence":
				children, err := ciHTTPSequenceChildren(action)
				if err != nil {
					return err
				}
				if err := visit(children); err != nil {
					return err
				}
			}
			if action.Control != nil {
				if err := visit(action.Control.Steps); err != nil {
					return err
				}
				if err := visit(action.Control.Else); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(script.Init); err != nil {
		return nil, false, err
	}
	if err := visit(script.Actions); err != nil {
		return nil, false, err
	}
	if err := visit(script.Teardown); err != nil {
		return nil, false, err
	}
	hosts := make([]string, 0, len(set))
	for host := range set {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts, deferredTarget, nil
}

func ciActionParamString(params map[string]any, key string) string {
	if len(params) == 0 {
		return ""
	}
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func ciHTTPSequenceChildren(action dsl.Action) ([]dsl.Action, error) {
	if action.Params == nil {
		return nil, fmt.Errorf("http_sequence %q requires params.requests", action.Name)
	}
	raw, exists := action.Params["requests"]
	if !exists {
		return nil, fmt.Errorf("http_sequence %q requires params.requests", action.Name)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("http_sequence %q params.requests is invalid: %w", action.Name, err)
	}
	var children []dsl.Action
	if err := json.Unmarshal(encoded, &children); err != nil || len(children) == 0 {
		return nil, fmt.Errorf("http_sequence %q params.requests must contain child requests", action.Name)
	}
	return children, nil
}

func encodeCIRunMetadata(metadata ciRunMetadata) (string, error) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return ciRunMetadataPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCIRunMetadata(value string) (ciRunMetadata, error) {
	if !strings.HasPrefix(value, ciRunMetadataPrefix) {
		return ciRunMetadata{}, fmt.Errorf("not a CI run")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, ciRunMetadataPrefix))
	if err != nil {
		return ciRunMetadata{}, err
	}
	var metadata ciRunMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return ciRunMetadata{}, err
	}
	if metadata.SchemaVersion != ciRunSchemaVersion {
		return ciRunMetadata{}, fmt.Errorf("unsupported CI run metadata schema")
	}
	return metadata, nil
}

func validateExistingCIRun(test *ManagedTest, projectID string, expected ciRunMetadata) error {
	if test == nil || normalizeProjectID(test.ProjectID) != normalizeProjectID(projectID) {
		return fmt.Errorf("idempotency key is already bound to a different project run")
	}
	actual, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil || actual.IdempotencySHA != expected.IdempotencySHA || actual.ScriptIdentity != expected.ScriptIdentity || actual.Source.RepositoryID != expected.Source.RepositoryID || actual.Source.WorkflowRef != expected.Source.WorkflowRef || actual.Source.RunID != expected.Source.RunID || actual.Source.RunAttempt != expected.Source.RunAttempt {
		return fmt.Errorf("idempotency key is already bound to different CI run metadata")
	}
	return nil
}

func (s *Server) buildCIRunResponse(ctx context.Context, test *ManagedTest) (ciRunResponse, error) {
	response := ciRunResponse{SchemaVersion: ciRunSchemaVersion}
	if test == nil {
		response.Outcome = "execution_error"
		response.Error = "CI run is unavailable"
		return response, nil
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil {
		return response, err
	}
	response.TestID = test.ID
	response.Status = strings.ToUpper(strings.TrimSpace(test.Status))
	response.StatusURL = ciPublicURL("/api/v1/ci/runs/" + url.PathEscape(test.ID))
	response.ReportURL = ciPublicURL("/tests/" + url.PathEscape(test.ID))
	response.SLAEvaluated = test.GoalConfig != nil
	if test.LiveMetrics != nil {
		response.Transactions = test.LiveMetrics.TotalTx
		response.Errors = test.LiveMetrics.TotalErrors
		response.TPS = test.LiveMetrics.TPS
		response.SLABreached, response.SLADetails = ciLiveSLABreach(test.LiveMetrics.GoalStatus)
	}
	if ciRunTerminal(response.Status) {
		if manifest, manifestErr := s.runner.GetRunArchiveManifest(test.ID); manifestErr == nil && manifest != nil {
			response.Transactions = manifest.TransactionCount
			response.SLABreached = manifest.SLABreached
			response.SLADetails = append([]string(nil), manifest.SLADetails...)
		}
	}
	response.ResultsPresent = response.Transactions > 0
	if test.StartedAt != nil {
		end := time.Now().UTC()
		if test.EndedAt != nil {
			end = test.EndedAt.UTC()
		}
		if end.After(*test.StartedAt) {
			response.DurationMs = end.Sub(test.StartedAt.UTC()).Milliseconds()
		}
	}
	response.Outcome = ciRunOutcome(response.Status, response.SLABreached, response.ResultsPresent, metadata)
	response.Error = strings.TrimSpace(test.Error)
	checkURL, syncErr := s.syncCIGitHubCheck(ctx, test, metadata, response)
	response.CheckURL = checkURL
	return response, syncErr
}

func ciRunOutcome(status string, breached, resultsPresent bool, metadata ciRunMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE":
		if breached {
			return "sla_breached"
		}
		if metadata.RequireSLA && !resultsPresent {
			return "execution_error"
		}
		if metadata.RequireResults && !resultsPresent {
			return "execution_error"
		}
		return "passed"
	case "FAILED":
		return "execution_error"
	case "ABORTED":
		return "cancelled"
	case "QUEUED", "SCHEDULED", "INITIALIZING":
		return "queued"
	default:
		return "running"
	}
}

func ciRunTerminal(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

func ciLiveSLABreach(status *GoalStatus) (bool, []string) {
	if status == nil {
		return false, nil
	}
	details := []string{}
	checks := []struct {
		breached bool
		detail   string
	}{
		{status.LatencyBreached, "P95 latency breached threshold"}, {status.ErrorBreached, "Error percentage breached threshold"},
		{status.ErrorCountBreached, "Total error count breached threshold"}, {status.ServerProcessingBreached, "Server processing time breached threshold"}, {status.TransferBreached, "Transfer time breached threshold"},
	}
	for _, check := range checks {
		if check.breached {
			details = append(details, check.detail)
		}
	}
	return len(details) > 0 || status.BreachActive, details
}

func ciPublicURL(relative string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_PUBLIC_URL")), "/")
	return base + relative
}

func defaultCIRunName(source ciRunSource, script *dsl.Script) string {
	name := "GitHub Actions"
	if script != nil && strings.TrimSpace(script.Name) != "" {
		name += " — " + strings.TrimSpace(script.Name)
	}
	if source.RunID != "" {
		name += " (run " + source.RunID + "/" + firstNonEmpty(source.RunAttempt, "1") + ")"
	}
	return name
}

func sameURLOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil || leftURL.Host == "" || rightURL.Host == "" {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) && strings.EqualFold(leftURL.Host, rightURL.Host)
}

func cloneCIStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func slogWarnCIScriptCleanup(scriptID string, err error) {
	slog.Warn("failed to remove ephemeral CI script snapshot", "script_id", scriptID, "error", err)
}
