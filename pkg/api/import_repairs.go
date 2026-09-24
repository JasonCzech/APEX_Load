package api

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
		writeError(w, 400, "Invalid repair request")
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
		writeError(w, 400, "Invalid repair request")
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
	candidate, claim, err := s.claimProtectedImportCandidateSession(r, enterpriseArchiveSaveProfile, id, req.ProjectID)
	if err != nil {
		writeError(w, 409, "Candidate is stale, expired, unavailable, or outside this project")
		return
	}
	defer clearProtectedImportCandidate(candidate)
	defer s.releaseImportCandidateClaim(claim)
	if _, batched := scriptBatch(candidate.Script); batched {
		writeError(w, 409, "Use the protected batch repair workflow for this candidate")
		return
	}
	lease := s.startImportCandidateClaimLease(claim)
	defer lease.stop()
	before := repairDisclosure(candidate.Script)
	draft := cloneScriptDefinition(candidate.Script)
	allocation, err := applyImportRepair(draft, req)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	findings, err := importreview.RefreshDependencies(draft)
	if err != nil {
		writeError(w, 400, "Cannot traverse candidate")
		return
	}
	requirements := discoverSecretBindingRequirements(draft, 0)
	unresolvedBindings := len(requirements)
	if s.runner != nil {
		_, missing := replaySecretRefs(draft, s.runner.secretResolverForProject(draft.ProjectID))
		unresolvedBindings += len(missing)
	}
	readiness, err := importreview.EvaluateScript(draft, importreview.EvaluationInput{ValidationIssues: dsl.ValidateScriptIssuesWithPreservedImportCredentials(draft), UnresolvedBindings: unresolvedBindings})
	if err != nil {
		writeError(w, 400, "Cannot evaluate candidate")
		return
	}
	// Fidelity and readiness are rebuilt from the same surviving requirements.
	ensureCredentialPreservingImportMetadata(draft, enterpriseArchiveSaveProfile)
	preserved := ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	preserved["guided_repair_candidate"] = true
	report := map[string]any{}
	reportBytes, _ := json.Marshal(preserved["fidelity_report"])
	_ = json.Unmarshal(reportBytes, &report)
	if report == nil {
		report = map[string]any{}
	}
	report["promotion_status"] = map[bool]string{true: "promotable", false: "blocked"}[readiness.PromotionReady]
	report["blockers"] = readiness.BlockingCodes
	report["warnings"] = readiness.ReviewCodes
	secretGated := 0
	for _, f := range findings {
		if f.Code == "restored_body_required" {
			secretGated++
		}
	}
	report["secret_gated"] = secretGated
	preserved["fidelity_report"] = report
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
				target = &dsl.Action{}
				b, _ := json.Marshal(ref.Request)
				_ = json.Unmarshal(b, target)
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
			// Only replace references in this selected consumer's executable fields.
			raw, _ := json.Marshal(target)
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			metadata := value["metadata"]
			delete(value, "metadata")
			raw, _ = json.Marshal(value)
			reference := regexp.MustCompile(`\{\{\s*(var|param)\.` + regexp.QuoteMeta(name) + `\s*\}\}`)
			if !reference.Match(raw) {
				return nil, errors.New("Selected consumer no longer references this variable")
			}
			raw = reference.ReplaceAll(raw, []byte("{{${1}."+op.Producer+"}}"))
			_ = json.Unmarshal(raw, &value)
			value["metadata"] = metadata
			raw, _ = json.Marshal(value)
			_ = json.Unmarshal(raw, target)
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
		target.Metadata["migration_change"] = "Operator selected TLS " + op.TLSMin + "–" + op.TLSMax + "; intentional migration, not exact LR parity."
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

func requiresExactRepairDraft(s *dsl.Script) bool {
	if _, ok := scriptBatch(s); ok {
		return true
	}
	if s == nil || s.Metadata == nil || s.Metadata.Import == nil {
		return false
	}
	value, _ := s.Metadata.Import.Preserved["guided_repair_candidate"].(bool)
	return value
}
