package api

import draftreview "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importsession"
)

// ImportRepairOperation describes an operator supplied replacement, never an
// acknowledgement. Body values travel only through encrypted candidate storage.
type ImportRepairOperation struct {
	Type         string         `json:"type"`
	Body         *string        `json:"body,omitempty"`
	SecretName   string         `json:"secret_name,omitempty"`
	Variable     string         `json:"variable,omitempty"`
	Producer     string         `json:"producer,omitempty"`
	Parameter    *dsl.Parameter `json:"parameter,omitempty"`
	ResponsePath string         `json:"response_path,omitempty"`
	Extractor    *dsl.Extractor `json:"extractor,omitempty"`
	TLSMin       string         `json:"tls_min,omitempty"`
	TLSMax       string         `json:"tls_max,omitempty"`
	VUsers       int            `json:"vusers,omitempty"`
	Iterations   int            `json:"iterations,omitempty"`
}
type ImportRepairRequest struct {
	BatchID       string                 `json:"batch_id,omitempty"`
	PreviewDigest string                 `json:"preview_digest,omitempty"`
	Operations    []JMeterBatchOperation `json:"operations,omitempty"`
	CandidateID   string                 `json:"candidate_id"`
	Revision      string                 `json:"revision"`
	ProjectID     string                 `json:"project_id"`
	Finding       string                 `json:"finding"`
	ActionPath    string                 `json:"action_path"`
	Operation     ImportRepairOperation  `json:"operation"`
}
type ImportRepairResponse struct {
	SecretBindingRequirements []SecretBindingRequirement          `json:"secret_binding_requirements"`
	CandidateID               string                              `json:"candidate_id"`
	Revision                  string                              `json:"revision"`
	Script                    *dsl.Script                         `json:"script"`
	Before                    *dsl.Script                         `json:"before"`
	Findings                  []importreview.Finding              `json:"findings"`
	Readiness                 importreview.Readiness              `json:"readiness"`
	Allocation                []engine.ParameterAllocationPreview `json:"allocation,omitempty"`
	ExpiresAt                 time.Time                           `json:"expires_at"`
	FidelityReport            map[string]any                      `json:"fidelity_report"`
}
type importRepairReceipt struct {
	Digest   string               `json:"digest"`
	Response ImportRepairResponse `json:"response"`
}

var repairVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const repairReceiptProfile = "enterprise_repair_receipt"

func repairReceiptID(id string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("apex-import-repair:"+id)).String()
}

func (s *Server) handlePreviewImportRepair(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req ImportRepairRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, 400, "invalid repair request: "+err.Error())
		return
	}
	s.processImportRepair(w, r, false, req)
}
func (s *Server) handleApplyImportRepair(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req ImportRepairRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, 400, "invalid repair request: "+err.Error())
		return
	}
	s.processImportRepair(w, r, true, req)
}
func (s *Server) handleImportRepair(w http.ResponseWriter, r *http.Request, apply bool) {
	if apply {
		s.handleApplyImportRepair(w, r)
	} else {
		s.handlePreviewImportRepair(w, r)
	}
}
func (s *Server) processImportRepair(w http.ResponseWriter, r *http.Request, apply bool, req ImportRepairRequest) {
	if req.BatchID != "" {
		if req.CandidateID != "" || req.Operation.Type != "" {
			writeError(w, 400, "Choose a batch or single-script repair")
			return
		}
		s.processJMeterBatchRepair(w, r, apply, req)
		return
	}
	if req.CandidateID == "" || req.Revision != req.CandidateID || !strings.HasPrefix(req.CandidateID, importDraftProtectedTokenPrefix) {
		writeError(w, 409, "A current protected candidate and revision are required; re-import if expired")
		return
	}
	id := strings.TrimPrefix(req.CandidateID, importDraftProtectedTokenPrefix)
	manager, err := s.protectedImportSessionManager()
	if err != nil {
		writeError(w, 503, "Protected repair storage is unavailable")
		return
	}
	_, actor, _ := s.resolveAuditActor(r)
	scope := importsession.Scope{ID: repairReceiptID(id), Profile: repairReceiptProfile, ProjectID: normalizeProjectID(req.ProjectID), Actor: actor}
	payload, _ := json.Marshal(req)
	digestBytes := sha256.Sum256(payload)
	clear(payload)
	digest := hex.EncodeToString(digestBytes[:])
	if b, _, err := manager.Open(r.Context(), scope, importCandidateSessionMaxBytes); err == nil {
		defer clear(b)
		var receipt importRepairReceipt
		if json.Unmarshal(b, &receipt) != nil || receipt.Digest != digest || !apply {
			writeError(w, 409, "Candidate revision has already been replaced")
			return
		}
		writeJSON(w, 200, receipt.Response)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeError(w, 409, "Repair receipt is expired or unavailable")
		return
	}
	// Preview never replaces the candidate, so it reads without the consume
	// lease. Holding that lease would make a concurrent save or credential
	// binding of the same draft fail while this inspection runs.
	var candidate *protectedImportCandidate
	var claim *importsession.Claim
	if apply {
		candidate, claim, err = s.claimProtectedImportCandidateSession(r, enterpriseArchiveSaveProfile, id, req.ProjectID)
	} else {
		candidate, err = s.openProtectedImportCandidateSession(r, enterpriseArchiveSaveProfile, id, req.ProjectID)
	}
	if err != nil {
		writeError(w, 409, "Candidate is stale, expired, unavailable, or outside this project")
		return
	}
	defer clearProtectedImportCandidate(candidate)
	if claim != nil {
		defer s.releaseImportCandidateClaim(claim)
	}
	if _, batched := scriptBatch(candidate.Script); batched {
		writeError(w, 409, "Use the protected batch repair workflow for this candidate")
		return
	}
	var lease *importCandidateClaimLease
	if claim != nil {
		lease = s.startImportCandidateClaimLease(claim)
		defer lease.stop()
	}
	before := repairDisclosure(candidate.Script)
	draft := cloneScriptDefinition(candidate.Script)
	allocation, err := applyImportRepair(draft, req)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var resolver engine.SecretResolver
	if s.runner != nil {
		resolver = s.runner.secretResolverForProject(draft.ProjectID)
	}
	findings, requirements, readiness, report, err := evaluateRepairedScript(draft, resolver)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	ensureCredentialPreservingImportMetadata(draft, enterpriseArchiveSaveProfile)
	preserved := ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	preserved["guided_repair_candidate"] = true
	// The review reads the preserved preflight before it inspects a candidate
	// again, so it must describe the repaired draft, not the conversion. The
	// stored candidate and the response are both built from this draft below,
	// which keeps the save-time digest of the submitted script matching.
	attachImportReplayPreflight(draft, enterpriseArchiveSaveProfile, repairReplayPreflight(draft, candidate.Script, resolver))
	response := ImportRepairResponse{SecretBindingRequirements: requirements, CandidateID: req.CandidateID, Revision: req.Revision, Before: before, Script: repairDisclosure(draft), Findings: findings, Readiness: readiness, Allocation: allocation, ExpiresAt: candidate.ExpiresAt, FidelityReport: report}
	if !apply || req.Operation.Type == "preview_allocation" || req.Operation.Type == "inspect" {
		writeJSON(w, 200, response)
		return
	}
	if err := lease.assertOwned(); err != nil {
		writeError(w, 409, "Candidate lease was lost")
		return
	}
	newID := uuid.NewString()
	token := importDraftProtectedTokenPrefix + newID
	preserved[importDraftAttestationMetadataKey] = token
	envelope := importCandidateEnvelope{Version: importCandidateSessionVersion, Profile: enterpriseArchiveSaveProfile, ProjectID: scope.ProjectID, Script: *draft, RawSource: candidate.RawSource, RawSourceFormat: candidate.RawSourceFormat, CreatedAt: time.Now().UTC(), ExpiresAt: candidate.ExpiresAt}
	encoded, err := marshalImportCandidateEnvelope(envelope, candidate.CredentialRequirements)
	if err != nil {
		writeError(w, 500, "Cannot encode repaired candidate")
		return
	}
	defer clear(encoded)
	err = manager.Save(r.Context(), importsession.SessionDescriptor{ID: newID, Profile: enterpriseArchiveSaveProfile, ProjectID: scope.ProjectID, Actor: actor, ExpiresAt: candidate.ExpiresAt}, encoded, importCandidateSessionMaxBytes)
	if err != nil {
		writeError(w, 503, "Cannot secure repaired candidate")
		return
	}
	response.CandidateID = token
	response.Revision = token
	response.Script = repairDisclosure(draft)
	receiptBytes, err := json.Marshal(importRepairReceipt{Digest: digest, Response: response})
	if err != nil {
		writeError(w, 500, "Cannot encode repair receipt")
		return
	}
	defer clear(receiptBytes)
	if err = manager.Save(r.Context(), importsession.SessionDescriptor{ID: scope.ID, Profile: scope.Profile, ProjectID: scope.ProjectID, Actor: actor, ExpiresAt: candidate.ExpiresAt}, receiptBytes, importCandidateSessionMaxBytes); err != nil {
		writeError(w, 503, "Cannot persist repair receipt; retry")
		return
	}
	// The receipt fences the old identity even if lease completion is retried.
	if err = s.completeImportCandidateClaim(r.Context(), claim); err != nil {
		writeError(w, 503, "Repair persisted; retry this same operation to retrieve it")
		return
	}
	writeJSON(w, 200, response)
}

var (
	errRepairTraversal  = errors.New("Cannot traverse candidate")
	errRepairEvaluation = errors.New("Cannot evaluate candidate")
)

// evaluateRepairedScript rebuilds what a repair can change from the repaired
// draft: dependency findings, secret-binding requirements, readiness and the
// preserved fidelity report, all from the same surviving requirements. Every
// repair route shares it so the same script reports the same findings, and an
// editor save of an imported script reuses it (refreshSavedImportEvaluation). It
// does not mark the draft a guided-repair candidate; only an import candidate
// is one, and its caller sets that. The returned error is the message to
// answer with.
//
// The persisted report is evaluated from the script alone. Project-secret
// state never enters it: a secret configured later, or a secret store that
// fails while the repair runs, must not become a durable promotion blocker,
// and launch admission checks secrets when a run starts. The resolver only
// adds missing project secrets to the returned readiness, so a preview or an
// apply still shows them; a nil resolver skips that check.
func evaluateRepairedScript(draft *dsl.Script, resolver engine.SecretResolver) ([]importreview.Finding, []SecretBindingRequirement, importreview.Readiness, map[string]any, error) {
	findings, err := importreview.RefreshDependencies(draft)
	if err != nil {
		return nil, nil, importreview.Readiness{}, nil, errRepairTraversal
	}
	requirements := discoverSecretBindingRequirements(draft, 0)
	input := importreview.EvaluationInput{ValidationIssues: dsl.ValidateScriptIssuesWithPreservedImportCredentials(draft), UnresolvedBindings: len(requirements)}
	persisted, err := importreview.EvaluateScript(draft, input)
	if err != nil {
		return nil, nil, importreview.Readiness{}, nil, errRepairEvaluation
	}
	readiness := persisted
	if resolver != nil {
		if _, missing := replaySecretRefs(draft, resolver); len(missing) > 0 {
			input.UnresolvedBindings += len(missing)
			if readiness, err = importreview.EvaluateScript(draft, input); err != nil {
				return nil, nil, importreview.Readiness{}, nil, errRepairEvaluation
			}
		}
	}
	preserved := ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	report := map[string]any{}
	reportBytes, _ := json.Marshal(preserved["fidelity_report"])
	_ = json.Unmarshal(reportBytes, &report)
	if report == nil {
		report = map[string]any{}
	}
	secretGated := 0
	for _, f := range findings {
		if f.Code == "restored_body_required" {
			secretGated++
		}
	}
	// A repair can clear only the step gates readiness evaluates, so the
	// converter's archive-level blockers (protocol classification, Java
	// lifecycle, trailing contributors, C parse failures, a real Solace
	// adapter, dropped bodies) survive it unchanged.
	blockers := append([]string(nil), persisted.BlockingCodes...)
	for _, line := range fidelityReportLines(report["blockers"]) {
		if line == secretGatedFidelityBlocker || recomputedFidelityBlocker.MatchString(line) {
			continue
		}
		blockers = append(blockers, line)
	}
	if secretGated > 0 {
		blockers = append(blockers, secretGatedFidelityBlocker)
	}
	blockers = uniqueSortedStrings(blockers)
	// Review codes (a placeholder producer, for example) are not blockers;
	// the converter never counts them, and none refuses a replay. So review
	// codes alone never turn a report blocked: a report blocked before the
	// repair stays blocked while any remains, and any other report, including
	// a script saved without one, which launch does not hold, does not become
	// blocked over them.
	priorStatus, _ := report["promotion_status"].(string)
	reviewHeld := len(persisted.ReviewCodes) > 0 && strings.EqualFold(strings.TrimSpace(priorStatus), "blocked")
	blocked := len(blockers) > 0 || len(persisted.ValidationIssues) > 0 || reviewHeld
	report["promotion_status"] = map[bool]string{true: "blocked", false: "promotable"}[blocked]
	report["blockers"] = blockers
	report["warnings"] = persisted.ReviewCodes
	// The converter omits a zero count (ImportFidelityReport.SecretGated is
	// omitempty), so a repaired report does too.
	if secretGated > 0 {
		report["secret_gated"] = secretGated
	} else {
		delete(report, "secret_gated")
	}
	preserved["fidelity_report"] = report
	return findings, requirements, readiness, report, nil
}

// secretGatedFidelityBlocker is the converter's blocker for lr_unmask bodies
// (enterprisearchive.BuildImportFidelityReport). A repaired report keeps it
// only while a body still has to be restored.
const secretGatedFidelityBlocker = "One or more lr_unmask request bodies require governed secret resolution before executable promotion."

// recomputedFidelityBlocker matches a fidelity blocker that restates a step
// gate: the converter's "<code> at <path>" line or a code an earlier repair
// wrote. Readiness evaluates every surviving gate again, so such a line is
// replaced rather than carried; every other line is a converter sentence.
var recomputedFidelityBlocker = regexp.MustCompile(`^\S+( at \S+)?$`)

// fidelityReportLines reads a list of strings from a fidelity report decoded
// from JSON.
func fidelityReportLines(value any) []string {
	values, _ := value.([]any)
	lines := make([]string, 0, len(values))
	for _, value := range values {
		if line, ok := value.(string); ok && strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// repairReplayPreflight rebuilds the preserved preflight of a repaired draft
// the way the conversion built the one it replaces (ImportDraft.Augment,
// finalizeImportCommitScript): without a governed resolver, so project-secret
// state never makes it invalid or fails its secrets check, and with the
// prior's converter warnings carried (carriedConverterWarnings).
//
// Built that way, though, its missing secrets are the ones the legacy
// environment probe cannot find, which includes every secret configured only
// in the project store. A repair that adds a reference, such as a proxy
// secret binding, would then list a configured secret as missing beside the
// readiness that resolved it. So when the repair has the resolver its
// readiness counts unresolved bindings with (evaluateRepairedScript), the
// missing secrets, their configure-later warning and the secrets check's
// detail come from that resolver. A nil resolver keeps the probe.
func repairReplayPreflight(draft, prior *dsl.Script, resolver engine.SecretResolver) *ReplayPreflight {
	preflight := buildReplayPreflight(draft, nil, validateScript(draft), carriedConverterWarnings(prior))
	if resolver == nil {
		return preflight
	}
	required, missing := replaySecretRefs(draft, resolver)
	warnings := make([]string, 0, len(preflight.Warnings))
	for _, line := range preflight.Warnings {
		if !strings.HasPrefix(line, unconfiguredSecretsWarning) {
			warnings = append(warnings, line)
		}
	}
	if len(missing) > 0 {
		warnings = append(warnings, unconfiguredSecretsWarning+strings.Join(missing, ", "))
	}
	preflight.RequiredSecrets, preflight.MissingSecrets, preflight.Warnings = required, missing, warnings
	for index := range preflight.Checks {
		if preflight.Checks[index].ID == "secrets" {
			preflight.Checks[index].Detail = replaySecretDetail(required, missing)
		}
	}
	return preflight
}

// unconfiguredSecretsWarning begins the configure-later line BuildReplayPreflight
// writes for missing secrets when it has no resolver
// (pkg/internal/importreview/replay_preflight.go).
const unconfiguredSecretsWarning = "secret(s) require project configuration before execution: "

// carriedConverterWarnings returns the converter warnings the prior preserved
// preflight was built from. BuildReplayPreflight appends its own messaging,
// placeholder, data-source and secret lines after them, and those are derived
// again from the repaired draft; carrying the old ones would keep reporting a
// problem the repair fixed. A line is dropped when the prior draft generates
// it now, or when it has a generated line's form, which also covers a
// preflight stamped before the draft last changed.
func carriedConverterWarnings(prior *dsl.Script) []string {
	previous := preservedReplayPreflight(prior)
	if previous == nil || len(previous.Warnings) == 0 {
		return nil
	}
	generated := make(map[string]struct{})
	for _, line := range buildReplayPreflight(prior, nil, nil, nil).Warnings {
		generated[line] = struct{}{}
	}
	var carried []string
	for _, line := range previous.Warnings {
		if _, ok := generated[line]; ok || replayPreflightGeneratedWarning(line) {
			continue
		}
		carried = append(carried, line)
	}
	return carried
}

// replayPreflightGeneratedWarnings matches the warning lines BuildReplayPreflight
// writes itself (pkg/internal/importreview/replay_preflight.go).
var replayPreflightGeneratedWarnings = []*regexp.Regexp{
	regexp.MustCompile(`^variable\(s\) with no seed or preceding extractor are sent as placeholder text: `),
	regexp.MustCompile(`^configure project secret\(s\) before replay: `),
	regexp.MustCompile(`^` + regexp.QuoteMeta(unconfiguredSecretsWarning)),
	regexp.MustCompile(`^(file )?data source ".*" (has no |contains a generated OpenAPI placeholder|requires an import file binding)`),
	regexp.MustCompile(`^parameter ".*" references missing data source ".*"$`),
	regexp.MustCompile(`^config\.jms\.mock is enabled; replay uses the in-memory JMS broker$`),
}

func replayPreflightGeneratedWarning(line string) bool {
	for _, pattern := range replayPreflightGeneratedWarnings {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

// preservedReplayPreflight reads a script's preserved replay preflight, held
// as the typed value in memory or as decoded JSON once stored.
func preservedReplayPreflight(script *dsl.Script) *ReplayPreflight {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return nil
	}
	value := script.Metadata.Import.Preserved["replay_preflight"]
	if typed, ok := value.(*ReplayPreflight); ok {
		return typed
	}
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var preflight ReplayPreflight
	if json.Unmarshal(raw, &preflight) != nil {
		return nil
	}
	return &preflight
}

// hasExecutionCode reports whether code gates execution of the surface that
// carries this metadata.
func hasExecutionCode(metadata map[string]any, code string) bool {
	if dslwalk.MetadataString(metadata, "execution_blocker") == code {
		return true
	}
	for _, c := range dslwalk.MetadataStrings(metadata, "execution_blockers") {
		if c == code {
			return true
		}
	}
	return false
}

func repairDisclosure(script *dsl.Script) *dsl.Script {
	value := credentialPreservingImportResponseValue(script)
	b, _ := json.Marshal(value)
	defer clear(b)
	var projected dsl.Script
	_ = json.Unmarshal(b, &projected)
	_ = dslwalk.Walk(&projected, func(ref dslwalk.SurfaceRef) error {
		if dslwalk.MetadataBool(ref.Metadata(), "restored_body_protected") {
			if ref.Action != nil {
				ref.Action.Body = "[REDACTED]"
			} else if ref.Request != nil {
				ref.Request["body"] = "[REDACTED]"
			}
		}
		return nil
	})
	return &projected
}

func applyImportRepair(script *dsl.Script, req ImportRepairRequest) ([]engine.ParameterAllocationPreview, error) {
	op := req.Operation
	if err := engine.ValidateParameterAllocationTopology(script); op.Type == "preview_allocation" && err != nil {
		return nil, err
	}
	if op.Type == "inspect" {
		return nil, nil
	}
	if op.Type == "preview_allocation" {
		if op.VUsers < 1 || op.Iterations < 0 {
			return nil, errors.New("VUsers must be positive and iterations non-negative (zero selects duration mode)")
		}
		// Import preview must not open caller-controlled local paths.
		for _, ds := range script.DataSources {
			if strings.TrimSpace(ds.Path) != "" && strings.TrimSpace(ds.InlineData) == "" {
				return nil, errors.New("Allocation preview requires embedded data rows")
			}
		}
		provider, err := engine.NewDataProvider(script.DataSources)
		if err != nil {
			return nil, errors.New("Cannot load allocation data")
		}
		_, preview, err := engine.CompileParameterAllocation(script.Parameters, provider, engine.ParameterAllocation{Population: op.VUsers, Iterations: op.Iterations})
		return preview, err
	}
	var target *dsl.Action
	var targetRequest map[string]any
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Path == req.ActionPath {
			target = ref.Action
			if target == nil && ref.Request != nil {
				target = repairRequestAction(ref.Request)
				targetRequest = ref.Request
			}
		}
		return nil
	})
	if target == nil {
		return nil, errors.New("Select an existing action finding")
	}
	findings, _ := importreview.FindingsFromScript(script)
	found := false
	for _, f := range findings {
		if f.ActionPath == req.ActionPath && f.Code == req.Finding {
			found = true
		}
	}
	if !found {
		return nil, errors.New("Finding is no longer present")
	}
	resolved := func(c string) bool { return c == req.Finding }
	switch op.Type {
	case "restore_body":
		if req.Finding != "restored_body_required" {
			return nil, errors.New("This finding does not identify a restorable body")
		}
		if op.Body == nil || len(*op.Body) > 8<<20 {
			return nil, errors.New("Supply a restored body of at most 8 MiB")
		}
		if dslwalk.MetadataBool(target.Metadata, "masked_body") {
			resolved = func(c string) bool { return c == req.Finding || c == "import_review_required" }
		}
		target.Body = *op.Body
		target.BodyFile = ""
		target.BodyEncoding = ""
		target.Metadata["restored_body_protected"] = true
		for _, k := range []string{"body_gated_by_secret", "source_secret_ref", "source_secret_function", "masked_body", "masked_body_raw", "masked_body_calls"} {
			delete(target.Metadata, k)
		}
	case "replace_proxy_secret":
		if req.Finding != "encrypted_proxy_password" || !logicalSecretNamePattern.MatchString(op.SecretName) {
			return nil, errors.New("Select a valid project-secret name for the encrypted proxy requirement")
		}
		script.Config.HTTP.Proxy.Password = "{{secret." + op.SecretName + "}}"
	case "bind_variable", "add_parameter", "add_extractor":
		if !strings.HasPrefix(req.Finding, "missing_producer:") && !strings.HasPrefix(req.Finding, "conditional_producer:") {
			return nil, errors.New("Select a variable dependency finding")
		}
		name := strings.SplitN(req.Finding, ":", 2)[1]
		switch op.Type {
		case "add_parameter":
			if op.Parameter == nil || op.Parameter.Name != name {
				return nil, errors.New("Parameter must supply the missing variable")
			}
			for _, p := range script.Parameters {
				if strings.EqualFold(p.Name, name) {
					return nil, errors.New("Parameter already exists")
				}
			}
			script.Parameters = append(script.Parameters, *op.Parameter)
		case "bind_variable":
			if !repairVariableName.MatchString(op.Producer) || op.Producer == name {
				return nil, errors.New("Choose a different producer name")
			}
			// Only replace references in this selected consumer's executable
			// fields: every field but its metadata, and the metadata entries
			// replay reads (varflow.MetadataReads), such as a header a Kafka
			// send delivers. Import evidence and every other entry keep the
			// recorded text. A control step's bodies and an http_sequence's
			// requests are steps of their own, with their own findings and
			// import evidence, so they are set aside and restored unchanged.
			nested := detachNestedSteps(target)
			raw, _ := json.Marshal(target)
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			metadata, _ := value["metadata"].(map[string]any)
			delete(value, "metadata")
			raw, _ = json.Marshal(value)
			reference := regexp.MustCompile(`\{\{\s*(var|param)\.` + regexp.QuoteMeta(name) + `\s*\}\}`)
			parameterReference := regexp.MustCompile(`\{\{\s*(param)\.` + regexp.QuoteMeta(name) + `\s*\}\}`)
			replacement := "{{${1}." + op.Producer + "}}"
			rebound := reference.Match(raw)
			raw = reference.ReplaceAll(raw, []byte(replacement))
			for key, entry := range metadata {
				pattern := parameterReference
				if vars, params := varflow.MetadataReads(*target, key); vars {
					pattern = reference
				} else if !params {
					continue
				}
				if entry, changed := rebindTemplateReferences(entry, pattern, replacement); changed {
					metadata[key] = entry
					rebound = true
				}
			}
			if !rebound {
				nested.restore(target)
				return nil, errors.New("Selected consumer no longer references this variable")
			}
			_ = json.Unmarshal(raw, &value)
			value["metadata"] = metadata
			raw, _ = json.Marshal(value)
			_ = json.Unmarshal(raw, target)
			nested.restore(target)
		case "add_extractor":
			if op.Extractor == nil || op.Extractor.Name != name || op.Extractor.Expression == "" {
				return nil, errors.New("Provide an extractor for the missing variable")
			}
			preceding := true
			var response *dsl.Action
			var responseRequest map[string]any
			_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
				if ref.Path == req.ActionPath {
					preceding = false
				}
				if preceding && ref.Path == op.ResponsePath {
					response = ref.Action
					if response == nil && ref.Request != nil {
						response = &dsl.Action{}
						b, _ := json.Marshal(ref.Request)
						_ = json.Unmarshal(b, response)
						responseRequest = ref.Request
					}
				}
				return nil
			})
			if response == nil || response.Target == "" || response.Type == "http_sequence" {
				return nil, errors.New("Select a preceding response")
			}
			response.Extractors = append(response.Extractors, *op.Extractor)
			if responseRequest != nil {
				b, _ := json.Marshal(response.Extractors)
				var es any
				_ = json.Unmarshal(b, &es)
				responseRequest["extractors"] = es
			}
		}
	case "select_tls":
		if !strings.Contains(req.Finding, "tls") && !strings.Contains(req.Finding, "ssl") {
			return nil, errors.New("Select a TLS requirement")
		}
		if (op.TLSMin != "1.2" && op.TLSMin != "1.3") || (op.TLSMax != "1.2" && op.TLSMax != "1.3") || op.TLSMax < op.TLSMin {
			return nil, errors.New("Select a supported TLS 1.2/1.3 range")
		}
		script.Config.HTTP.TLS.MinVersion = op.TLSMin
		script.Config.HTTP.TLS.MaxVersion = op.TLSMax
		delete(script.Config.WebRuntime.AdditionalAttributes, "web_set_sockets_option.SSL_VERSION")
		change := "Operator selected TLS " + op.TLSMin + "–" + op.TLSMax + "; intentional migration, not exact LR parity."
		target.Metadata["migration_change"] = change
		// The TLS range is script-wide, so it resolves every step gated on the
		// same requirement, not only the one the operator selected. The
		// selected step is cleared after the switch, like every repair target.
		_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
			if ref.Path == req.ActionPath || !hasExecutionCode(ref.Metadata(), req.Finding) {
				return nil
			}
			gated := dsl.Action{Metadata: ref.Metadata()}
			gated.Metadata["migration_change"] = change
			importreview.RemoveActionCodes(&gated, resolved)
			ref.SetMetadata(gated.Metadata)
			return nil
		})
	default:
		return nil, fmt.Errorf("Unsupported repair operation %q", op.Type)
	}
	importreview.RemoveActionCodes(target, resolved)
	if targetRequest != nil {
		b, _ := json.Marshal(target)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for k := range targetRequest {
			delete(targetRequest, k)
		}
		for k, v := range m {
			targetRequest[k] = v
		}
	}
	if op.Type == "bind_variable" {
		fs, _ := importreview.AnalyzeDependencies(script)
		for _, f := range fs {
			if f.ActionPath == req.ActionPath && f.Code == "missing_producer:"+op.Producer {
				return nil, errors.New("Selected producer does not exist before this request")
			}
		}
	}
	if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) > 0 {
		return nil, errors.New("Repair creates an invalid draft; check the supplied operation fields")
	}
	return nil, nil
}

// repairRequestAction decodes a nested request (an http_sequence entry) as
// the action a repair edits and reads in its place.
func repairRequestAction(request map[string]any) *dsl.Action {
	action := &dsl.Action{}
	raw, _ := json.Marshal(request)
	_ = json.Unmarshal(raw, action)
	return action
}

// nestedSteps are the steps a control step or an http_sequence carries, set
// aside while a repair rewrites only the step's own fields.
type nestedSteps struct {
	control *dsl.ControlFlow
	params  map[string]any
}

// detachNestedSteps gives action a control without its bodies and params
// without its requests, as varflow reads a control or sequence shell, and
// returns the originals for restore.
func detachNestedSteps(action *dsl.Action) nestedSteps {
	var nested nestedSteps
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_if", "control_switch", "control_loop", "control_parallel":
		if action.Control == nil {
			break
		}
		nested.control = action.Control
		shell := *action.Control
		shell.Steps, shell.Else, shell.Default = nil, nil, nil
		shell.Cases = append([]dsl.SwitchCase(nil), action.Control.Cases...)
		for index := range shell.Cases {
			shell.Cases[index].Steps = nil
		}
		shell.Branches = append([]dsl.ParallelBranch(nil), action.Control.Branches...)
		for index := range shell.Branches {
			shell.Branches[index].Steps = nil
		}
		action.Control = &shell
	case "http_sequence":
		if _, present := action.Params["requests"]; !present {
			break
		}
		nested.params = action.Params
		shell := make(map[string]any, len(action.Params))
		for key, value := range action.Params {
			if key != "requests" {
				shell[key] = value
			}
		}
		action.Params = shell
	}
	return nested
}

// restore puts the nested steps back, as recorded, into action, whose own
// fields a repair may have rewritten. A rewrite changes only text, so the
// cases and branches keep their order.
func (nested nestedSteps) restore(action *dsl.Action) {
	if nested.control != nil && action.Control != nil {
		action.Control.Steps, action.Control.Else, action.Control.Default = nested.control.Steps, nested.control.Else, nested.control.Default
		for index := range min(len(action.Control.Cases), len(nested.control.Cases)) {
			action.Control.Cases[index].Steps = nested.control.Cases[index].Steps
		}
		for index := range min(len(action.Control.Branches), len(nested.control.Branches)) {
			action.Control.Branches[index].Steps = nested.control.Branches[index].Steps
		}
	}
	if nested.params != nil {
		if action.Params == nil {
			action.Params = make(map[string]any, 1)
		}
		action.Params["requests"] = nested.params["requests"]
	}
}

// rebindTemplateReferences replaces reference in every string of a decoded
// JSON value, in place for objects and arrays, and reports whether any
// string changed.
func rebindTemplateReferences(value any, reference *regexp.Regexp, replacement string) (any, bool) {
	changed := false
	switch typed := value.(type) {
	case string:
		if reference.MatchString(typed) {
			return reference.ReplaceAllString(typed, replacement), true
		}
	case map[string]any:
		for key, item := range typed {
			if item, rebound := rebindTemplateReferences(item, reference, replacement); rebound {
				typed[key] = item
				changed = true
			}
		}
	case []any:
		for index, item := range typed {
			if item, rebound := rebindTemplateReferences(item, reference, replacement); rebound {
				typed[index] = item
				changed = true
			}
		}
	}
	return value, changed
}

var requiresExactRepairDraft = draftreview.RequiresExactRepairDraft
