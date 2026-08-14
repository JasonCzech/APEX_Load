package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/storage"
)

// importSessionCapacityRetryAfterSeconds is the hint sent with a 429 when
// protected import session capacity is exhausted. Sessions are short-lived, so
// a short retry is usually enough.
const importSessionCapacityRetryAfterSeconds = 5

const importSourceRetentionPendingCode = "import_source_retention_pending"

// importSessionClaimStatus maps a protected-import-session claim failure to its
// HTTP status. It is shared by every format that uses the commit service, so
// the classification is a cross-importer contract rather than a per-handler
// decision.
//
// Capacity exhaustion is transient and leaves the caller's session intact, so
// it must not read as an internal error — that told operators to file a bug
// when retrying was the correct response. Missing, expired, out-of-scope, and
// deleted-project failures deliberately collapse to one 404 so a caller cannot
// probe which of those applies.
func importSessionClaimStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, importsession.ErrSessionClaimed):
		return http.StatusConflict
	case errors.Is(err, importsession.ErrSessionCapacity):
		return http.StatusTooManyRequests
	case errors.Is(err, os.ErrNotExist),
		errors.Is(err, importsession.ErrSessionExpired),
		errors.Is(err, importsession.ErrSessionScope),
		errors.Is(err, importsession.ErrProjectDeleted):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// importCommitSpec contains the source-specific behavior for the otherwise
// identical create/replace workflow used by structured web API importers.
// Keeping this internal preserves every existing route and wire contract while
// ensuring binding, validation, preflight, persistence, and audit behavior do
// not drift between formats.
type importCommitSpec struct {
	sourceProfile         string
	auditProfile          string
	displayName           string
	allowedReplaceModes   []string
	actionKey             func(dsl.Action) string
	diff                  func(existing *dsl.Script, imported *dsl.Script, replaceMode string, actionKey func(dsl.Action) string) openAPIImportDiff
	merge                 func(existing *dsl.Script, imported *dsl.Script, actionKey func(dsl.Action) string) *dsl.Script
	mapCompatibleResponse func(map[string]any) map[string]any
}

// sharedImportCommitRequest adds protected-candidate-only overrides without
// changing any existing required request fields. The embedded compatibility
// request retains the established OpenAPI/Postman/AsyncAPI wire contract.
type sharedImportCommitRequest struct {
	openAPIImportCommitRequest
	SecretBindings      map[string]string `json:"secret_bindings,omitempty"`
	ParameterizationIDs []string          `json:"parameterization_selected_ids,omitempty"`
}

func (spec importCommitSpec) validate() error {
	if strings.TrimSpace(spec.sourceProfile) == "" ||
		strings.TrimSpace(spec.auditProfile) == "" ||
		strings.TrimSpace(spec.displayName) == "" {
		return errors.New("import commit source, audit profile, and display name are required")
	}
	if len(spec.allowedReplaceModes) == 0 ||
		spec.actionKey == nil ||
		spec.diff == nil ||
		spec.merge == nil ||
		spec.mapCompatibleResponse == nil {
		return fmt.Errorf("%s import commit specification is incomplete", spec.displayName)
	}
	seenModes := make(map[string]struct{}, len(spec.allowedReplaceModes))
	for _, rawMode := range spec.allowedReplaceModes {
		mode := strings.ToLower(strings.TrimSpace(rawMode))
		if mode == "" {
			return fmt.Errorf("%s import commit replace modes contain an empty value", spec.displayName)
		}
		if _, duplicate := seenModes[mode]; duplicate {
			return fmt.Errorf("%s import commit replace modes contain duplicate %q", spec.displayName, mode)
		}
		seenModes[mode] = struct{}{}
	}
	if _, ok := seenModes["full"]; !ok {
		return fmt.Errorf("%s import commit replace modes must include full", spec.displayName)
	}
	return nil
}

func normalizeImportCommitModes(req openAPIImportCommitRequest, spec importCommitSpec) (string, string, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "create"
	}
	replaceMode := strings.ToLower(strings.TrimSpace(req.ReplaceMode))
	if replaceMode == "" {
		replaceMode = "full"
	}
	if mode != "replace" && strings.TrimSpace(req.TargetScriptID) != "" {
		mode = "replace"
	}
	if mode != "create" && mode != "replace" {
		return "", "", errors.New("mode must be create or replace")
	}
	allowed := make([]string, 0, len(spec.allowedReplaceModes))
	allowedSet := make(map[string]struct{}, len(spec.allowedReplaceModes))
	for _, rawMode := range spec.allowedReplaceModes {
		mode := strings.ToLower(strings.TrimSpace(rawMode))
		allowed = append(allowed, mode)
		allowedSet[mode] = struct{}{}
	}
	if _, ok := allowedSet[replaceMode]; !ok {
		return "", "", fmt.Errorf("replace_mode must be %s", strings.Join(allowed, " or "))
	}
	return mode, replaceMode, nil
}

func (spec importCommitSpec) writeResponse(w http.ResponseWriter, status int, payload map[string]any) {
	writeJSON(w, status, spec.mapCompatibleResponse(payload))
}

func protectedImportCreateScriptID(profile, sessionID string) string {
	identity := "apexload:protected-import-create:v1:" +
		strings.ToLower(strings.TrimSpace(profile)) + ":" +
		strings.TrimSpace(sessionID)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
}

func (s *Server) applyProtectedImportFolderOverride(candidate *dsl.Script, override *string) (int, error) {
	if candidate == nil {
		return 0, nil
	}
	folderID := ""
	if override != nil {
		folderID = strings.TrimSpace(*override)
		if folderID == "" {
			candidate.FolderID = nil
			return 0, nil
		}
	} else {
		if candidate.FolderID == nil {
			return 0, nil
		}
		folderID = strings.TrimSpace(*candidate.FolderID)
		if folderID == "" {
			candidate.FolderID = nil
			return 0, nil
		}
	}
	if s == nil || s.store == nil {
		return http.StatusServiceUnavailable, errors.New("script folder storage is unavailable")
	}
	folder, err := s.store.GetFolder(folderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return http.StatusNotFound, errors.New("script folder is unavailable for this project")
		}
		return http.StatusInternalServerError, errors.New("failed to validate script folder")
	}
	if folder == nil || normalizeProjectID(folder.ProjectID) != normalizeProjectID(candidate.ProjectID) {
		return http.StatusNotFound, errors.New("script folder is unavailable for this project")
	}
	if !strings.EqualFold(strings.TrimSpace(folder.Category), "scripts") {
		return http.StatusConflict, errors.New("destination must be a script folder")
	}
	candidate.FolderID = &folderID
	return 0, nil
}

func finalizeImportCommitScript(
	script *dsl.Script,
	sourceProfile string,
	preserveImportedCredentials bool,
) (*dsl.Script, *ReplayPreflight, error) {
	finalized, err := requestimport.FinalizeScript(script, nil, requestimport.FinalizeOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to finalize imported script: %w", err)
	}
	script = finalized.Script
	if !preserveImportedCredentials {
		// This drops only the trust attestation, which decides whether preserved
		// JVM argv and trusted reconciliation are allowed. It must never decide
		// whether the stored row keeps working credential values: the ordinary
		// durable path in preparePersistentLibraryScriptWithImportTrust persists
		// the executable script too, so an import that loses this marker still
		// commits a runnable script instead of a silently masked one.
		stripCredentialPreservingImportPolicy(script)
	}
	preflight := buildReplayPreflight(script, nil, nil, validationWarnings(script))
	attachImportReplayPreflight(script, sourceProfile, preflight)
	return script, preflight, nil
}

// applySuggestedImportCommitSecretBindings keeps every request body on the
// shared byte-preserving credential path. maskScriptSecrets is intentionally
// broad because it also projects arbitrary metadata and free text; it must not
// reinterpret JSON, XML, form, multipart, GraphQL, encoded, or raw wire bytes.
//
// Structured projectors rewrite only exact credential occurrences. Unsafe
// typed/mixed content is rejected or retained as a disclosure-safe blocked
// draft, while unrecognized raw/file bytes remain byte-identical.
func applySuggestedImportCommitSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	sourceProfile string,
) (*dsl.Script, []SecretBindingRequirement, error) {
	if script == nil {
		return nil, nil, errors.New("script is required")
	}
	working := cloneScriptDefinition(script)
	if working == nil {
		return nil, nil, errors.New("failed to clone import script")
	}
	_ = scriptIndex

	dynamicOnly := cloneScriptDefinition(working)
	if dynamicOnly == nil {
		return nil, nil, errors.New("failed to analyze import script variables")
	}
	// Script parameters are static import material. Only a prior extractor or
	// OAuth output proves that a {{var.*}} credential is dynamic at this action.
	dynamicOnly.Parameters = nil
	dynamicReadiness := requestimport.AnalyzeVariableReadiness(dynamicOnly, nil)

	reservedNames, err := engine.ReferencedSecretNames(working)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect existing secret references: %w", err)
	}
	registry := credentialpolicy.NewNameRegistry(reservedNames...)
	protectedBodies := make(map[string]string)
	if err := dslwalk.Walk(working, func(ref dslwalk.SurfaceRef) error {
		body, ok, err := decodeImportCommitProjectionBody(ref)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		options := credentialpolicy.Options{
			Mode:        credentialpolicy.Preserve,
			Source:      credentialpolicy.SourceRef{Format: sourceProfile, Path: ref.Path, Pointer: "/body"},
			RequestKey:  ref.Path,
			NamePrefix:  firstNonEmpty(ref.Name(), "request"),
			Registry:    registry,
			ProvenVars:  importCommitProvenBodyVariables(body.text(), ref.Path, dynamicReadiness),
			TargetField: "body",
		}
		projected, result, err := credentialpolicy.ProjectRequestBody(
			body.wire,
			importCommitSurfaceContentType(ref),
			options,
		)
		for index := range result.Requirements {
			result.Requirements[index].Value = ""
		}
		if err != nil {
			return fmt.Errorf("project request body at %s: %w", ref.Path, err)
		}
		if result.Disposition == credentialpolicy.Rejected || projected == nil {
			codes := importCommitCredentialFindingCodes(result, credentialpolicy.Rejected)
			if len(codes) == 0 {
				codes = []string{"credential_body_projection_rejected"}
			}
			return fmt.Errorf("request body at %s was rejected: %s", ref.Path, strings.Join(codes, ", "))
		}
		if result.Disposition == credentialpolicy.BlockedDraft {
			applyImportCommitBodyReviewGate(ref, sourceProfile, result)
		}
		if len(result.PreservedTargets) > 0 {
			ref.SetMetadata(requestimport.MergePreservedCredentialTargetsMetadata(
				ref.Metadata(),
				result.PreservedTargets,
			))
		}
		protectedBodies[ref.Path] = body.encode(projected)
		// Keep the body out of the generic free-text projection. It is restored
		// by stable traversal path after every other executable field is masked
		// and auto-bound through the existing compatibility workflow.
		setImportCommitSurfaceBody(ref, "")
		return nil
	}); err != nil {
		return nil, nil, err
	}

	// The import already contains the operator-supplied executable values.
	// Keep non-body fields intact as well; projecting them through
	// applySuggestedSecretBindings would replace headers, auth, and URLs with
	// unconfigured {{secret.*}} references and make the committed script
	// unrunnable until the operator re-entered the same values.
	if len(protectedBodies) == 0 {
		return working, nil, nil
	}
	restored := 0
	if err := dslwalk.Walk(working, func(ref dslwalk.SurfaceRef) error {
		body, ok := protectedBodies[ref.Path]
		if !ok {
			return nil
		}
		setImportCommitSurfaceBody(ref, body)
		restored++
		return nil
	}); err != nil {
		return nil, nil, err
	}
	if restored != len(protectedBodies) {
		return nil, nil, errors.New("request body projection lost an action surface")
	}
	return working, nil, nil
}

func stripCredentialPreservingImportPolicy(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return
	}
	delete(
		script.Metadata.Import.Preserved,
		dsl.ImportPreservedCredentialPolicyKey,
	)
}

func importCommitSurfaceBody(ref dslwalk.SurfaceRef) (string, bool) {
	if ref.Action != nil {
		return ref.Action.Body, strings.TrimSpace(ref.Action.Body) != ""
	}
	if ref.Request == nil {
		return "", false
	}
	body, ok := ref.Request["body"].(string)
	return body, ok && strings.TrimSpace(body) != ""
}

func setImportCommitSurfaceBody(ref dslwalk.SurfaceRef, body string) {
	if ref.Action != nil {
		ref.Action.Body = body
		return
	}
	if ref.Request != nil {
		ref.Request["body"] = body
	}
}

func importCommitSurfaceContentType(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		if len(ref.Action.HeaderList) > 0 {
			value := ""
			for _, header := range ref.Action.HeaderList {
				if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
					value = header.Value
				}
			}
			return value
		}
		return importCommitHeaderMapContentType(ref.Action.Headers)
	}
	if ref.Request == nil {
		return ""
	}
	if raw, exists := ref.Request["header_list"]; exists {
		payload, err := json.Marshal(raw)
		if err == nil {
			var headers dsl.HeaderList
			if json.Unmarshal(payload, &headers) == nil && len(headers) > 0 {
				value := ""
				for _, header := range headers {
					if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
						value = header.Value
					}
				}
				return value
			}
		}
	}
	raw, exists := ref.Request["headers"]
	if !exists {
		return ""
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var headers map[string]string
	if json.Unmarshal(payload, &headers) != nil {
		return ""
	}
	return importCommitHeaderMapContentType(headers)
}

func importCommitHeaderMapContentType(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	value := ""
	for _, key := range keys {
		if strings.EqualFold(strings.TrimSpace(key), "Content-Type") {
			value = headers[key]
		}
	}
	return value
}

func importCommitProvenBodyVariables(
	body string,
	actionPath string,
	readiness requestimport.VariableReadiness,
) map[string]struct{} {
	references := portableScriptTemplateReferences(body)
	if len(references) == 0 {
		return nil
	}
	missing := make(map[string]struct{}, len(readiness.ByAction[actionPath]))
	for _, name := range readiness.ByAction[actionPath] {
		missing[name] = struct{}{}
	}
	proven := make(map[string]struct{})
	for _, reference := range references {
		expression := strings.TrimSpace(reference[2 : len(reference)-2])
		name, ok := strings.CutPrefix(expression, "var.")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		if _, unavailable := missing[name]; unavailable {
			continue
		}
		proven[name] = struct{}{}
	}
	if len(proven) == 0 {
		return nil
	}
	return proven
}

func importCommitCredentialFindingCodes(
	result credentialpolicy.Result,
	disposition credentialpolicy.Disposition,
) []string {
	var codes []string
	for _, finding := range result.Findings {
		if finding.Disposition == disposition && strings.TrimSpace(finding.Code) != "" {
			codes = append(codes, finding.Code)
		}
	}
	sort.Strings(codes)
	return codes
}

func applyImportCommitBodyReviewGate(
	ref dslwalk.SurfaceRef,
	sourceProfile string,
	result credentialpolicy.Result,
) {
	codes := importCommitCredentialFindingCodes(result, credentialpolicy.BlockedDraft)
	if len(codes) == 0 {
		codes = importCommitCredentialFindingCodes(result, credentialpolicy.Rejected)
	}
	if len(codes) == 0 {
		codes = []string{"credential_body_review_required"}
	}
	findings := make([]importreview.Finding, 0, len(codes))
	for _, code := range codes {
		findings = append(findings, importreview.Finding{
			Code:       "import_review_required",
			Severity:   diag.Blocker,
			Element:    firstNonEmpty(ref.Name(), "request body"),
			Detail:     "the imported request body requires review before execution",
			Location:   ref.Path + ".body",
			ActionPath: ref.Path,
			Source:     sourceProfile,
			ReasonCode: code,
			SourceRef:  ref.Path + ".body",
			Resolution: "replace_behavior_and_remove_gate",
			Gate:       importreview.GateExecution,
		})
	}
	if ref.Action != nil {
		importreview.ApplyToAction(ref.Action, findings)
		return
	}
	action := dsl.Action{Name: ref.Name(), Metadata: ref.Metadata()}
	importreview.ApplyToAction(&action, findings)
	ref.SetMetadata(action.Metadata)
}

// OpenAPI-Responses: 200, 201
func (s *Server) handleImportCommit(w http.ResponseWriter, r *http.Request, spec importCommitSpec) {
	if err := spec.validate(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var sharedReq sharedImportCommitRequest
	req := &sharedReq.openAPIImportCommitRequest
	if !decodeJSONBody(w, r, &sharedReq) {
		return
	}
	script := &req.Script
	var candidateClaim *importsession.Claim
	var candidateLease *importCandidateClaimLease
	var candidateRequirements []credentialpolicy.Requirement
	var draftAttestationClaim *importDraftAttestationClaim
	// candidateSource is unredacted uploaded source. It is zeroed on every exit
	// path, including the error ones, so it does not outlive the request.
	var candidateSource []byte
	defer func() {
		clearCredentialRequirementValues(candidateRequirements)
		clear(candidateSource)
		if candidateClaim != nil {
			s.releaseImportCandidateClaim(candidateClaim)
		}
		if draftAttestationClaim != nil {
			s.releaseImportDraftAttestation(draftAttestationClaim)
		}
	}()
	if strings.TrimSpace(req.ImportSessionID) != "" {
		projectID := strings.TrimSpace(req.ProjectID)
		if projectID == "" {
			projectID = strings.TrimSpace(req.Script.ProjectID)
		}
		if authorized, scoped := authorizedProjectFromRequest(r); scoped {
			projectID = authorized
		}
		protectedCandidate, claim, err := s.claimProtectedImportCandidateSession(
			r,
			spec.sourceProfile,
			req.ImportSessionID,
			projectID,
		)
		if err != nil {
			// The status literals are repeated here rather than passed through
			// from importSessionClaimStatus because cmd/swaggergen infers each
			// route's documented responses from the writeError call sites.
			switch importSessionClaimStatus(err) {
			case http.StatusConflict:
				writeError(w, http.StatusConflict, "import session is already being committed")
			case http.StatusTooManyRequests:
				w.Header().Set("Retry-After", strconv.Itoa(importSessionCapacityRetryAfterSeconds))
				writeError(w, http.StatusTooManyRequests, "protected import session capacity is busy; retry shortly")
			case http.StatusNotFound:
				writeError(w, http.StatusNotFound, "import session not found or expired")
			default:
				slog.Error("failed to claim protected import session", "source", spec.sourceProfile, "error", err)
				writeError(w, http.StatusInternalServerError, "failed to open protected import session")
			}
			return
		}
		candidate := protectedCandidate.Script
		candidateRequirements = protectedCandidate.CredentialRequirements
		protectedCandidate.CredentialRequirements = nil
		candidateSource = protectedCandidate.RawSource
		protectedCandidate.RawSource = nil
		candidateClaim = claim
		candidateLease = s.startImportCandidateClaimLease(claim)
		defer candidateLease.stop()
		if req.Name != nil {
			candidate.Name = strings.TrimSpace(*req.Name)
		}
		candidate, err = applyProtectedImportCandidateOverrides(candidate, candidateRequirements, sharedReq)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		script = candidate
	} else {
		// The legacy client-submitted draft path remains supported, but caller
		// JSON is not importer trust. Only the opaque, one-shot attestation
		// embedded by an authenticated inspection response may authorize exact
		// executable credential preservation.
		if importDraftAttestationToken(script) != "" {
			claim, claimErr := s.claimImportDraftAttestation(r, script)
			if claimErr != nil || claim == nil || claim.Script == nil ||
				!strings.EqualFold(
					strings.TrimSpace(claim.Profile),
					strings.TrimSpace(spec.sourceProfile),
				) {
				if claim != nil {
					s.releaseImportDraftAttestation(claim)
				}
				writeError(w, http.StatusConflict, "credential-preserving import draft is invalid or expired; run the import again")
				return
			}
			draftAttestationClaim = claim
			candidateSource = claim.RawSource
			claim.RawSource = nil
			script = claim.Script
		} else if fingerprint, fingerprintErr := fingerprintImportDraftCredentials(script); fingerprintErr != nil || fingerprint.HasRawCredentials {
			writeError(w, http.StatusConflict, "credential-preserving import draft is missing its server capability; run the import again")
			return
		}
		// Whether the claim succeeded or not, the capability is request-scoped
		// and must never become durable provenance. An invalid/foreign marker
		// simply falls through to ordinary strict persistence below.
		stripImportDraftAttestation(script)
	}
	if status, err := s.applyProtectedImportFolderOverride(script, req.FolderID); err != nil {
		writeError(w, status, err.Error())
		return
	}
	script, err := replaceUnavailableImportCredentialValues(script)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// This handler is itself the authenticated importer boundary. Exact source
	// credentials are admitted only through a claimed server-owned candidate or
	// one-shot draft capability; value-free legacy drafts remain compatible.
	credentialPreservingImportCommit := true
	protectedImportCommit := candidateClaim != nil || draftAttestationClaim != nil
	protectedSourceCommitOwner := ""
	if len(candidateSource) > 0 {
		if _, retentionEnabled := s.store.(storage.ImportSourceRetentionStore); retentionEnabled {
			switch {
			case candidateClaim != nil:
				protectedSourceCommitOwner = protectedImportSourceCommitOwnerDigest(
					spec.sourceProfile,
					"session",
					candidateClaim.ID,
				)
			case draftAttestationClaim != nil:
				protectedSourceCommitOwner = protectedImportSourceCommitOwnerDigest(
					spec.sourceProfile,
					"draft_attestation",
					draftAttestationClaim.Token,
				)
			}
		}
	}
	safeScript, _, err := applySuggestedImportCommitSecretBindings(script, 0, spec.sourceProfile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to prepare safe secret references: "+err.Error())
		return
	}
	script = safeScript
	stripProtectedImportCredentialMetadata(script)
	// A commit fence is server-owned retry state. Imported or caller-submitted
	// metadata can never nominate its owner or script fingerprint.
	stripProtectedImportSourceCommitFence(script)

	mode, replaceMode, err := normalizeImportCommitModes(*req, spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if mode == "create" {
		if candidateClaim != nil {
			// The protected session is the durable create identity. If the script
			// write commits but claim completion is interrupted, a recovered claim
			// must address the same create-only row instead of allocating a second
			// script ID.
			script.ID = protectedImportCreateScriptID(spec.sourceProfile, candidateClaim.ID)
		} else if draftAttestationClaim != nil &&
			strings.TrimSpace(script.ID) == "" {
			// The attestation token is the legacy path's stable create identity.
			// A stale protected-claim recovery after an ambiguous response must
			// address the same create-only row.
			script.ID = protectedImportCreateScriptID(
				spec.sourceProfile,
				draftAttestationClaim.Token,
			)
		}
		script, preflight, err := finalizeImportCommitScript(
			script,
			spec.sourceProfile,
			credentialPreservingImportCommit,
		)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if protectedSourceCommitOwner != "" {
			projected, projectionErr := preparePersistentImportedLibraryScript(
				script,
				nil,
				spec.sourceProfile,
			)
			if projectionErr != nil {
				writeError(w, http.StatusBadRequest, projectionErr.Error())
				return
			}
			if strings.TrimSpace(projected.Version) == "" {
				projected.Version = "1"
			}
			scriptFingerprint, fingerprintErr := storage.ImportSourceScriptFingerprint(projected)
			if fingerprintErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to fingerprint imported script")
				return
			}
			fence, fenceErr := newProtectedImportSourceCommitFence(
				protectedSourceCommitOwner,
				spec.sourceProfile,
				candidateSource,
				scriptFingerprint,
			)
			if fenceErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to prepare protected import source commit")
				return
			}
			attachProtectedImportSourceCommitFence(projected, fence)
			script = projected
		}
		diff := openAPIImportDiff{Mode: "create", Added: len(script.Actions)}
		if req.DryRun {
			spec.writeResponse(w, http.StatusOK, map[string]any{"dry_run": true, "diff": diff, "preflight": preflight})
			return
		}
		if candidateLease != nil {
			if err := candidateLease.assertOwned(); err != nil {
				writeError(w, http.StatusConflict, "import session claim was lost before commit")
				return
			}
		}
		if draftAttestationClaim != nil {
			if err := s.assertImportDraftAttestationOwned(draftAttestationClaim); err != nil {
				writeError(w, http.StatusConflict, "import draft attestation lease was lost before commit")
				return
			}
		}
		var stored *StoredScript
		var createdNow bool
		if credentialPreservingImportCommit {
			stored, createdNow, err = s.runner.CreateImportedScriptDurableReportingReplay(
				script,
				spec.sourceProfile,
			)
		} else {
			stored, createdNow, err = s.runner.CreateScriptDurableReportingReplay(script)
		}
		if err != nil {
			if errors.Is(err, errScriptConcurrentChange) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			if errors.Is(err, errScriptCredentialBindingsRequired) {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
			return
		}
		if protectedImportCommit {
			// Retain before completing the claim. Completion releases the session
			// object that holds the only copy of the raw source, so the order here
			// is what makes retention possible at all rather than a preference.
			_, actor, _ := s.resolveAuditActor(r)
			if !s.retainProtectedImportSource(
				r.Context(),
				stored.Script,
				spec.sourceProfile,
				candidateSource,
				actor,
				!createdNow,
			) {
				s.recordAuditEvent(
					r,
					"script.import."+spec.auditProfile+".source_retention_pending",
					"partial",
					"script",
					stored.ID,
					stored.ProjectID,
					map[string]any{"committed": true},
				)
				// The script row is already durable, so report that state
				// explicitly and leave the claim releasable for a same-session
				// reconciliation retry. Consuming the claim here would destroy
				// the last source copy.
				retryInstruction := "retry with the same import_session_id"
				if draftAttestationClaim != nil {
					retryInstruction = "retry the same attested import draft"
				}
				spec.writeResponse(w, http.StatusServiceUnavailable, map[string]any{
					"error":      "script committed but protected import source retention is pending; " + retryInstruction,
					"code":       importSourceRetentionPendingCode,
					"committed":  true,
					"retryable":  true,
					"script_id":  stored.ID,
					"project_id": stored.ProjectID,
				})
				return
			}
			if candidateClaim != nil {
				candidateLease.stop()
				if err := s.completeImportCandidateClaim(r.Context(), candidateClaim); err != nil {
					slog.Error("failed to finalize committed import session", "source", spec.sourceProfile, "session_id", req.ImportSessionID, "error", err)
				}
				// Completion failure is ambiguous. Keep the claim fenced for
				// stale-claim reconciliation instead of releasing it as though
				// the durable commit had failed.
				candidateClaim = nil
			}
			if draftAttestationClaim != nil {
				if err := s.completeImportDraftAttestation(draftAttestationClaim); err != nil {
					slog.Error("failed to finalize committed import draft attestation", "source", spec.sourceProfile, "script_id", stored.ID, "error", err)
				}
				draftAttestationClaim = nil
			}
		}
		// A retry after an interrupted commit resolves to the same deterministic
		// script id and create-only persistence hands the existing row back
		// instead of erroring. Report that outcome so a client — or the CLI's
		// ambiguous-failure retry — can tell "I committed this" from "this was
		// already committed" rather than assuming the former. 201 stays the
		// first-write status; a replay is 200, matching the native Bruno commit.
		status := http.StatusCreated
		auditOutcome := "create"
		if !createdNow {
			status = http.StatusOK
			auditOutcome = "create.idempotent_replay"
		}
		s.recordAuditEvent(r, "script.import."+spec.auditProfile+"."+auditOutcome, "success", "script", stored.ID, stored.ProjectID, map[string]any{"name": disclosureSafeScriptName(stored.Name, stored.Script)})
		spec.writeResponse(w, status, map[string]any{
			"scripts":           maskStoredScripts([]*StoredScript{stored}),
			"script":            maskScriptSecrets(stored.Script),
			"stored_script":     maskStoredScript(stored),
			"replaced":          false,
			"created":           createdNow,
			"idempotent_replay": !createdNow,
			"dry_run":           false,
			"diff":              diff,
			"preflight":         preflight,
		})
		return
	}

	targetID := strings.TrimSpace(req.TargetScriptID)
	if targetID == "" {
		targetID = strings.TrimSpace(script.ID)
	}
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "target_script_id is required when replacing an existing "+spec.displayName+" import")
		return
	}
	existing, ok := s.runner.GetScript(targetID)
	if !ok || existing == nil || existing.Script == nil {
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	if protectedImportCommit &&
		normalizeProjectID(existing.ProjectID) != normalizeProjectID(script.ProjectID) {
		// A protected candidate is scoped to the project captured at
		// inspection time. Do not let replace-mode identity preservation
		// silently relocate that server-owned candidate into another project,
		// even for an otherwise cross-project-capable caller. Collapse the
		// mismatch with the ordinary missing-target response.
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, existing.ProjectID) {
		return
	}
	retentionOnlyReplay := false
	var sourceCommitPredecessors []string
	if protectedSourceCommitOwner != "" {
		fence, exists, fenceErr := readProtectedImportSourceCommitFence(existing.Script)
		if fenceErr != nil {
			writeError(w, http.StatusConflict, "target script has invalid protected import source commit state")
			return
		}
		if !exists || fence.OwnerDigest != protectedSourceCommitOwner {
			alreadyPublished, publishedErr := s.retainedImportSourceCommitOwnerWasPublished(
				existing.ID,
				existing.ProjectID,
				protectedSourceCommitOwner,
				spec.sourceProfile,
				candidateSource,
			)
			if publishedErr != nil {
				writeError(w, http.StatusConflict, "could not safely reconcile the protected import source commit")
				return
			}
			if alreadyPublished ||
				(exists && protectedImportSourceCommitFenceContainsOwner(
					fence,
					protectedSourceCommitOwner,
				)) {
				writeError(w, http.StatusConflict, "target script changed after the protected import replacement committed")
				return
			}
		}
		if exists {
			currentFingerprint, fingerprintErr := storage.ImportSourceScriptFingerprint(existing.Script)
			if fingerprintErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to fingerprint target script")
				return
			}
			fenceStillCurrent := currentFingerprint == fence.ScriptFingerprint
			if fence.OwnerDigest == protectedSourceCommitOwner {
				if !fenceStillCurrent ||
					!strings.EqualFold(fence.SourceProfile, spec.sourceProfile) ||
					fence.SourceSizeBytes != int64(len(candidateSource)) {
					writeError(w, http.StatusConflict, "target script changed after the protected import replacement committed")
					return
				}
				sourceSum := sha256.Sum256(candidateSource)
				if fence.SourceSHA256 != hex.EncodeToString(sourceSum[:]) {
					writeError(w, http.StatusConflict, "protected import source no longer matches the committed replacement")
					return
				}
				// The prior request crossed the script commit boundary. This
				// retry may only reconcile its retained source; it must never
				// project the old candidate over a later target revision.
				retentionOnlyReplay = true
			} else {
				sourceCommitPredecessors = append(
					sourceCommitPredecessors,
					fence.SupersededOwnerDigests...,
				)
				activeSourceMatches := s.retainedImportSourceCommitFenceMatchesActive(
					existing.ID,
					existing.ProjectID,
					fence,
				)
				if fenceStillCurrent && !activeSourceMatches {
					writeError(w, http.StatusConflict, "target script has another protected import source retention pending")
					return
				}
				if !fenceStillCurrent && !activeSourceMatches {
					sourceCommitPredecessors = append(
						sourceCommitPredecessors,
						fence.OwnerDigest,
					)
				}
			}
		}
	}
	expectedUpdatedAt := existing.Script.UpdatedAt
	expectedActionGraphDigest, err := scriptCorrelationActionGraphDigest(existing.Script)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint target script")
		return
	}

	next := script
	diff := spec.diff(existing.Script, script, replaceMode, spec.actionKey)
	diff.Mode = "replace"
	diff.ReplaceMode = replaceMode
	if replaceMode != "full" {
		next = spec.merge(existing.Script, script, spec.actionKey)
	}
	// Re-imports may replace content but may never relocate a script or erase
	// its durable identity/creation metadata.
	preserveReplacedScriptIdentity(next, existing.Script)
	next, preflight, err := finalizeImportCommitScript(
		next,
		spec.sourceProfile,
		credentialPreservingImportCommit,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	replacementAlreadyApplied := retentionOnlyReplay
	if credentialPreservingImportCommit {
		// Compare the exact trusted-import projection that persistence would
		// write. The stored first attempt contains the server-owned preservation
		// marker, while the recovered session candidate intentionally does not;
		// comparing the pre-projection candidate would therefore miss an
		// already-committed replacement and bump ref-key/version metadata again.
		projectedNext, projectionErr := preparePersistentImportedLibraryScript(
			next,
			existing.Script,
			spec.sourceProfile,
		)
		if projectionErr != nil {
			writeError(w, http.StatusBadRequest, projectionErr.Error())
			return
		}
		if !retentionOnlyReplay && protectedSourceCommitOwner != "" {
			if strings.TrimSpace(projectedNext.Version) == "" {
				projectedNext.Version = strings.TrimSpace(existing.Script.Version)
			}
			if strings.TrimSpace(projectedNext.Version) == "" {
				projectedNext.Version = "1"
			}
			scriptFingerprint, fingerprintErr := storage.ImportSourceScriptFingerprint(projectedNext)
			if fingerprintErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to fingerprint imported script")
				return
			}
			fence, fenceErr := newProtectedImportSourceCommitFence(
				protectedSourceCommitOwner,
				spec.sourceProfile,
				candidateSource,
				scriptFingerprint,
				sourceCommitPredecessors...,
			)
			if fenceErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to prepare protected import source commit")
				return
			}
			attachProtectedImportSourceCommitFence(projectedNext, fence)
			next = projectedNext
		} else if !retentionOnlyReplay {
			if protectedSourceCommitOwner == "" {
				existingFence, exists, fenceErr := readProtectedImportSourceCommitFence(existing.Script)
				if fenceErr != nil {
					writeError(w, http.StatusConflict, "target script has invalid protected import source commit state")
					return
				}
				if exists {
					attachProtectedImportSourceCommitFence(projectedNext, existingFence)
				}
			}
			replacementAlreadyApplied = equivalentCreateOnlyScript(
				existing.Script,
				projectedNext,
			)
		}
	}
	if req.DryRun {
		spec.writeResponse(w, http.StatusOK, map[string]any{"dry_run": true, "replaced": true, "diff": diff, "preflight": preflight})
		return
	}

	if candidateLease != nil {
		if err := candidateLease.assertOwned(); err != nil {
			writeError(w, http.StatusConflict, "import session claim was lost before commit")
			return
		}
	}
	if draftAttestationClaim != nil {
		if err := s.assertImportDraftAttestationOwned(draftAttestationClaim); err != nil {
			writeError(w, http.StatusConflict, "import draft attestation lease was lost before commit")
			return
		}
	}
	updated := existing
	updateOK := true
	if !replacementAlreadyApplied {
		if credentialPreservingImportCommit {
			updated, updateOK, err = s.runner.UpdateImportedScriptDurableInProjectIfUnchanged(
				targetID,
				next,
				authorizedProjectOr(r, existing.ProjectID),
				expectedUpdatedAt,
				expectedActionGraphDigest,
				spec.sourceProfile,
			)
		} else {
			updated, updateOK, err = s.runner.UpdateScriptDurableInProjectIfUnchanged(
				targetID,
				next,
				authorizedProjectOr(r, existing.ProjectID),
				expectedUpdatedAt,
				expectedActionGraphDigest,
			)
		}
		if err != nil {
			if errors.Is(err, errScriptConcurrentChange) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			if errors.Is(err, errScriptCredentialBindingsRequired) {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
			return
		}
	}
	if !updateOK {
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	if protectedImportCommit {
		// Replacement is a new committed source revision just as creation is.
		// Retain it before completing the one-shot claim that owns the only raw
		// upload copy.
		_, actor, _ := s.resolveAuditActor(r)
		if !s.retainProtectedImportSource(
			r.Context(),
			updated.Script,
			spec.sourceProfile,
			candidateSource,
			actor,
			replacementAlreadyApplied,
		) {
			s.recordAuditEvent(
				r,
				"script.import."+spec.auditProfile+".source_retention_pending",
				"partial",
				"script",
				updated.ID,
				updated.ProjectID,
				map[string]any{"committed": true, "replace_mode": replaceMode},
			)
			retryInstruction := "retry with the same import_session_id"
			if draftAttestationClaim != nil {
				retryInstruction = "retry the same attested import draft"
			}
			spec.writeResponse(w, http.StatusServiceUnavailable, map[string]any{
				"error":      "script replacement committed but protected import source retention is pending; " + retryInstruction,
				"code":       importSourceRetentionPendingCode,
				"committed":  true,
				"retryable":  true,
				"script_id":  updated.ID,
				"project_id": updated.ProjectID,
			})
			return
		}
		if candidateClaim != nil {
			candidateLease.stop()
			if err := s.completeImportCandidateClaim(r.Context(), candidateClaim); err != nil {
				slog.Error("failed to finalize committed import session", "source", spec.sourceProfile, "session_id", req.ImportSessionID, "error", err)
			}
			candidateClaim = nil
		}
		if draftAttestationClaim != nil {
			if err := s.completeImportDraftAttestation(draftAttestationClaim); err != nil {
				slog.Error("failed to finalize committed import draft attestation", "source", spec.sourceProfile, "script_id", updated.ID, "error", err)
			}
			draftAttestationClaim = nil
		}
	}
	s.recordAuditEvent(r, "script.import."+spec.auditProfile+".replace", "success", "script", updated.ID, updated.ProjectID, map[string]any{
		"name":         disclosureSafeScriptName(updated.Name, updated.Script),
		"replace_mode": replaceMode,
	})
	spec.writeResponse(w, http.StatusOK, map[string]any{
		"scripts":       maskStoredScripts([]*StoredScript{updated}),
		"script":        maskScriptSecrets(updated.Script),
		"stored_script": maskStoredScript(updated),
		"replaced":      true,
		"dry_run":       false,
		"diff":          diff,
		"preflight":     preflight,
	})
}

func applyProtectedImportCandidateOverrides(
	candidate *dsl.Script,
	protectedRequirements []credentialpolicy.Requirement,
	req sharedImportCommitRequest,
) (*dsl.Script, error) {
	if candidate == nil {
		return nil, errors.New("protected import candidate is invalid")
	}
	if len(req.SecretBindings) > 0 {
		protectedIDs := make(map[string]struct{}, len(protectedRequirements))
		for _, requirement := range protectedRequirements {
			if _, ok := credential.ParseReference(strings.TrimSpace(requirement.Replacement)); ok {
				protectedIDs[requirement.ID] = struct{}{}
			}
		}
		protectedSelections := make([]secretBindingSelection, 0, len(req.SecretBindings))
		legacySelections := make([]secretBindingSelection, 0, len(req.SecretBindings))
		for requirementID, secretName := range req.SecretBindings {
			selection := secretBindingSelection{
				RequirementID: requirementID,
				SecretName:    secretName,
			}
			if _, protected := protectedIDs[requirementID]; protected {
				protectedSelections = append(protectedSelections, selection)
			} else {
				legacySelections = append(legacySelections, selection)
			}
		}
		sort.Slice(protectedSelections, func(i, j int) bool {
			return protectedSelections[i].RequirementID < protectedSelections[j].RequirementID
		})
		sort.Slice(legacySelections, func(i, j int) bool {
			return legacySelections[i].RequirementID < legacySelections[j].RequirementID
		})
		if len(protectedSelections) > 0 {
			bound, unexpectedLegacy, _, err := applyProtectedImportCredentialBindings(candidate, protectedSelections)
			if err != nil || len(unexpectedLegacy) != 0 {
				if err == nil {
					err = errors.New("protected requirement id is stale or invalid")
				}
				return nil, fmt.Errorf("protected import credential bindings are stale or invalid: %w", err)
			}
			candidate = bound
		}
		if len(legacySelections) > 0 {
			bound, _, err := applySecretBindings(candidate, 0, legacySelections)
			if err != nil {
				return nil, fmt.Errorf("protected import credential bindings are stale or invalid: %w", err)
			}
			candidate = bound
		}
	}
	stripProtectedImportCredentialMetadata(candidate)
	if len(req.ParameterizationIDs) == 0 {
		return candidate, nil
	}
	selected := make(map[string]bool, len(req.ParameterizationIDs))
	for _, rawID := range req.ParameterizationIDs {
		id := strings.TrimSpace(rawID)
		if id == "" || selected[id] {
			return nil, errors.New("protected import parameterization selections contain an empty or duplicate id")
		}
		selected[id] = true
	}
	trusted := buildTrustedImportParameterization(candidate)
	available := make(map[string]struct{}, len(trusted.Suggestions))
	for _, suggestion := range trusted.Suggestions {
		available[suggestion.ID] = struct{}{}
	}
	for id := range selected {
		if _, ok := available[id]; !ok {
			return nil, errors.New("protected import parameterization selections are stale or unavailable")
		}
	}
	applied, _, err := paramize.ApplySuggestions(candidate, trusted.Suggestions, selected)
	if err != nil {
		return nil, fmt.Errorf("apply protected import parameterization: %w", err)
	}
	// The shared commit boundary immediately projects the complete candidate.
	// Deferring that projection keeps JSON request bodies on its byte-preserving
	// structured path; the selected suggestions themselves contain only trusted
	// runtime templates and introduce no caller-provided secret values.
	return applied, nil
}
