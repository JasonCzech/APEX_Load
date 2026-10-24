package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// ScriptRepairRequest names one import finding on a saved script and the
// operator-supplied repair for it. The script and its revision come from the
// path and the If-Match tag, never from the body.
type ScriptRepairRequest struct {
	Finding    string                `json:"finding"`
	ActionPath string                `json:"action_path"`
	Operation  ImportRepairOperation `json:"operation"`
}

// ScriptRepairResponse reports a repair on a saved script the way an import
// repair reports one on a candidate: the findings, readiness and fidelity
// report evaluated from the repaired script. ContentRevision is the revision
// the repair was computed from.
//
// Preview also returns the disclosure-safe script before and after the
// repair. Both are the repair projection, so their difference is the repair
// alone; the editor's authoring projection of the same revision is another
// projection and would not diff cleanly against the result. Apply omits both:
// it returns the persisted script with its new revision and its validation,
// so an editor adopts both without reading them again. It carries that script
// once, as a save does, so a large script's receipt stays within the client's
// response cap; one over it would read as an unknown outcome after the save.
type ScriptRepairResponse struct {
	ScriptID                  string                     `json:"script_id"`
	ContentRevision           string                     `json:"content_revision"`
	Script                    *dsl.Script                `json:"script,omitempty"`
	Before                    *dsl.Script                `json:"before,omitempty"`
	Findings                  []importreview.Finding     `json:"findings"`
	Readiness                 importreview.Readiness     `json:"readiness"`
	SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	FidelityReport            map[string]any             `json:"fidelity_report"`
	Stored                    *StoredScript              `json:"stored,omitempty"`
	Validation                *scriptValidationResponse  `json:"validation,omitempty"`
}

const scriptRepairMaxBodyBytes = 1 << 20

// scriptRevisionsUnavailableCode marks the one 503 that is a lasting
// capability gap rather than a transient failure: this controller's store
// keeps no script content revisions, so no saved-script repair can run.
// writeError masks every other 5xx, including one sent after an apply was
// saved, so a client tells them apart only by this code.
const scriptRevisionsUnavailableCode = "script_revisions_unavailable"

func writeScriptRevisionsUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "durable script content revisions unavailable",
		"code":  scriptRevisionsUnavailableCode,
	})
}

// storedScriptRepairOperations are the import repairs a saved script accepts.
// restore_body would write an operator-supplied protected body into durable
// storage outside an import session, and allocation preview reads data rows
// rather than repairing a finding, so both stay import-only.
var storedScriptRepairOperations = map[string]bool{
	"select_tls":           true,
	"replace_proxy_secret": true,
	"bind_variable":        true,
	"add_parameter":        true,
	"add_extractor":        true,
	"inspect":              true,
}

func (s *Server) handlePreviewScriptRepair(w http.ResponseWriter, r *http.Request) {
	s.processScriptRepair(w, r, false)
}

func (s *Server) handleApplyScriptRepair(w http.ResponseWriter, r *http.Request) {
	s.processScriptRepair(w, r, true)
}

// processScriptRepair runs an import guided repair on the durable executable
// copy of a saved script. The repair is bound to the editor's authoring
// revision: it is computed from that exact snapshot, and apply persists it
// only while the snapshot is still current. Preview persists nothing.
func (s *Server) processScriptRepair(w http.ResponseWriter, r *http.Request, apply bool) {
	w.Header().Set("Cache-Control", "no-store")
	stored, supported, admitted := s.loadScriptAuthoringRequest(w, r, r.PathValue("id"))
	if !admitted {
		return
	}
	if !supported || stored.AuthoringProjection == nil {
		writeScriptRevisionsUnavailable(w)
		return
	}
	revision, present, err := parseScriptAuthoringIfMatch(r.Header)
	if err != nil {
		writeError(w, http.StatusBadRequest, "If-Match must contain one strong script authoring v1 tag")
		return
	}
	if !present {
		writeError(w, http.StatusPreconditionRequired, "If-Match is required for script repairs")
		return
	}
	if revision != stored.ContentRevision {
		writeError(w, http.StatusPreconditionFailed, "script authored content changed; reload before repairing")
		return
	}
	if scriptRequiresJavaVuserAuthoringApproval(stored.Script) {
		writeError(w, http.StatusConflict, "Java Vuser scripts must be changed through local authoring-session review and approval")
		return
	}
	// Import findings, and the fidelity report a repair rewrites, belong to an
	// import's provenance. Evaluating any other script would stamp one on it.
	source := scriptImportSource(stored.Script)
	if source == "" {
		writeError(w, http.StatusConflict, "Guided repairs apply only to imported scripts")
		return
	}
	var req ScriptRepairRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, scriptRepairMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid repair request: "+err.Error())
		return
	}
	if err := storedScriptRepairOperationError(req.Operation.Type, apply); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Only a preview shows the script before and after the repair.
	var before *dsl.Script
	if !apply {
		before = repairDisclosure(stored.Script)
	}
	draft := cloneScriptDefinition(stored.Script)
	if _, err := applyImportRepair(draft, ImportRepairRequest{Finding: req.Finding, ActionPath: req.ActionPath, Operation: req.Operation}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resolver := s.runner.secretResolverForBinding(s.runner.secretBindingForScript(draft))
	findings, requirements, readiness, report, err := evaluateRepairedScript(draft, resolver)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A saved import keeps the preflight its review read. Rebuild it from the
	// repaired script, as an import repair does, so it never reports a gate
	// the repair cleared.
	if preservedReplayPreflight(stored.Script) != nil {
		attachImportReplayPreflight(draft, source, repairReplayPreflight(draft, stored.Script, resolver))
	}

	trustedImport := isCredentialPreservingImport(stored.Script)
	persisted := draft
	if trustedImport {
		// A save through PUT refuses any transport change while imported
		// credentials are kept. The repair may change only what its own
		// operation owns; anything else, such as a consumer edit that moves a
		// kept credential, is refused exactly as PUT would refuse it.
		candidate, allowance := storedRepairCredentialComparison(stored.Script, draft, req.Operation.Type)
		if err := validatePreservedImportCredentialEdits(candidate, allowance); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		// The same disclosure-safe binding gate as an ordinary save: a repair
		// cannot introduce a raw credential into durable storage. Like a save
		// (removeSubmittedDependencyCodes), the repair is judged and written
		// without the dependency codes evaluateRepairedScript just stamped on
		// it, and the write stamps them again once its own binding gate has
		// passed (UpdateScriptGuidedRepairDurable): disclosure may mask a long
		// variable name in one, and that server output is not a credential.
		persisted = cloneScriptDefinition(draft)
		if err := importreview.RemoveDependencyCodes(persisted); err != nil {
			writeError(w, http.StatusBadRequest, errRepairTraversal.Error())
			return
		}
		projected := maskScriptSecrets(persisted)
		if projected == nil {
			writeError(w, http.StatusBadRequest, "failed to project script for secret binding")
			return
		}
		if unresolved := discoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
			writeSecretBindingsRequired(w, unresolved)
			return
		}
	}
	if issues := validateStoredExecutionScript(draft); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}

	if findings == nil {
		findings = []importreview.Finding{}
	}
	if requirements == nil {
		requirements = []SecretBindingRequirement{}
	}
	response := ScriptRepairResponse{
		ScriptID: stored.ID, ContentRevision: stored.ContentRevision,
		Findings: findings, Readiness: readiness, SecretBindingRequirements: requirements, FidelityReport: report,
	}
	if !apply {
		response.Before, response.Script = before, repairDisclosure(draft)
		writeJSON(w, http.StatusOK, response)
		return
	}

	updated, ok, err := s.runner.UpdateScriptGuidedRepairDurable(stored.ID, persisted, scriptAuthoringPrecondition(stored), source, trustedImport)
	if errors.Is(err, errScriptAuthoringUnsupported) {
		writeScriptRevisionsUnavailable(w)
		return
	}
	if err != nil {
		writeScriptAuthoringMutationError(w, err)
		return
	}
	if !ok || updated == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	// Names and revisions only: a repair's values (parameter rows, extractor
	// expressions) never enter the audit trail.
	details := map[string]any{
		"name":                    disclosureSafeScriptName(updated.Name, updated.Script),
		"operation":               req.Operation.Type,
		"finding":                 req.Finding,
		"action_path":             req.ActionPath,
		"content_revision_before": stored.ContentRevision,
		"content_revision_after":  updated.ContentRevision,
	}
	switch req.Operation.Type {
	case "select_tls":
		details["tls_min"], details["tls_max"] = req.Operation.TLSMin, req.Operation.TLSMax
	case "replace_proxy_secret":
		details["secret_name"] = req.Operation.SecretName
	}
	s.recordAuditEvent(r, "script.repair.apply", "success", "script", updated.ID, updated.ProjectID, details)
	w.Header().Set("X-APEX-Script-Save-Mode", "conditioned")
	if !s.scriptLibraryAssignments(w, []*StoredScript{updated}) {
		return
	}
	binding := s.runner.secretBindingForScript(updated.Script)
	updatedResolver := s.runner.secretResolverForBinding(binding)
	validation := buildScriptValidationResponse(updated.ID, updated.Script, updatedResolver)
	validation.ValidatedContentRevision = updated.ContentRevision
	s.attachValidationContext(r, &validation, updated.ProjectID, updated.Script, binding, updatedResolver)
	response.Stored = viewStoredScript(updated)
	response.Validation = &validation
	writeJSON(w, http.StatusOK, response)
}

func storedScriptRepairOperationError(operation string, apply bool) error {
	switch {
	case operation == "restore_body":
		return errors.New("Restoring a protected body is available only during import; import the source again to restore it")
	case operation == "inspect" && apply:
		return errors.New("inspect only previews a saved script's findings")
	case !storedScriptRepairOperations[operation]:
		return fmt.Errorf("Unsupported repair operation %q for a saved script", operation)
	}
	return nil
}

// storedRepairEvaluationKeys are the preserved evaluation outputs a repair
// rebuilds as a whole from the repaired script: the fidelity report and the
// review's preflight. No request sends either. Projection keeps their counts
// and secret names, but it still scrubs free text in them (carried converter
// warnings, gate issue text) that quotes or resembles a credential. Because
// the allowance takes the draft's metadata, such a masked fragment must not
// read as a kept credential whose context is the whole script. The shared
// context comparison already skips both as review evidence
// (importReviewEvidencePreservedKeys); leaving them out here too keeps the
// repair comparison independent of that list.
var storedRepairEvaluationKeys = []string{"fidelity_report", "replay_preflight"}

// storedRepairCredentialComparison returns the repaired draft and its
// allowance for the retained-credential check. The allowance is the prior
// script with exactly the transport fields operation may change taken from
// the draft, so the repair's own change passes and every other change to a
// kept credential's execution context is still refused. The dependency
// repairs own no transport field.
//
// The allowance also takes the draft's metadata, script-wide and on every
// step, where the repair core writes what replay neither sends nor resolves:
// import evidence, the review codes, flags and migration notes the repair and
// its evaluation restamp (unsentStepReviewMetadata), and every other entry
// that holds no {{param.*}} reference. Disclosure scrubs a review code as free
// text, so a long variable name in one reads as masked; when the repair
// clears the code before it, that masked code moves up into review_gate, and
// compared against the prior it would read as a new credential. A step's
// metadata entries that a repair may rewrite keep the prior's values instead
// (storedRepairComparesMetadata): a Kafka header or JMS property replay sends,
// or a {{param.*}} entry of a protocol step, which bind_variable may rewrite
// with the operator's producer name. Those entries are compared like every
// executable field, so a rebind that edits the text around a kept credential
// is refused by the same check PUT applies, as a rebind of a request field is.
//
// Both sides leave out the dependency review codes (missing_producer and
// conditional_producer), as they leave out the evaluation output: the
// repair's evaluation stamps them again from the repaired script
// (evaluateRepairedScript), so they are server output even in the
// review_gate or execution_blocker a Kafka or JMS send delivers, where a
// masked code the repair moves up would otherwise read as a new credential.
func storedRepairCredentialComparison(prior, draft *dsl.Script, operation string) (*dsl.Script, *dsl.Script) {
	candidate := cloneScriptDefinition(draft)
	allowance := cloneScriptDefinition(prior)
	evaluated := cloneScriptDefinition(draft)
	allowance.Metadata = evaluated.Metadata
	priorMetadata := map[string]map[string]any{}
	_ = dslwalk.Walk(allowance, func(ref dslwalk.SurfaceRef) error {
		priorMetadata[ref.Path] = ref.Metadata()
		return nil
	})
	type stepMetadata struct {
		values map[string]any
		read   []string
	}
	metadata := map[string]stepMetadata{}
	_ = dslwalk.Walk(evaluated, func(ref dslwalk.SurfaceRef) error {
		step := stepMetadata{values: ref.Metadata()}
		// A nested request is read as applyImportRepair reads one it rebinds.
		action := ref.Action
		if action == nil && ref.Request != nil {
			action = repairRequestAction(ref.Request)
		}
		if action != nil {
			for key, value := range step.values {
				if storedRepairComparesMetadata(*action, key, value, priorMetadata[ref.Path][key]) {
					step.read = append(step.read, key)
				}
			}
		}
		metadata[ref.Path] = step
		return nil
	})
	_ = dslwalk.Walk(allowance, func(ref dslwalk.SurfaceRef) error {
		step, found := metadata[ref.Path]
		if !found {
			return nil
		}
		recorded := priorMetadata[ref.Path]
		for _, key := range step.read {
			if value, kept := recorded[key]; kept {
				step.values[key] = value
			} else {
				delete(step.values, key)
			}
		}
		ref.SetMetadata(step.values)
		return nil
	})
	for _, script := range []*dsl.Script{candidate, allowance} {
		if script.Metadata != nil && script.Metadata.Import != nil {
			for _, key := range storedRepairEvaluationKeys {
				delete(script.Metadata.Import.Preserved, key)
			}
		}
		_ = importreview.RemoveDependencyCodes(script)
	}
	switch operation {
	case "select_tls":
		allowance.Config.HTTP.TLS.MinVersion = draft.Config.HTTP.TLS.MinVersion
		allowance.Config.HTTP.TLS.MaxVersion = draft.Config.HTTP.TLS.MaxVersion
		const legacyTLSAttribute = "web_set_sockets_option.SSL_VERSION"
		if _, kept := draft.Config.WebRuntime.AdditionalAttributes[legacyTLSAttribute]; !kept {
			delete(allowance.Config.WebRuntime.AdditionalAttributes, legacyTLSAttribute)
		}
	case "replace_proxy_secret":
		allowance.Config.HTTP.Proxy.Password = draft.Config.HTTP.Proxy.Password
	}
	return candidate, allowance
}

// storedRepairComparesMetadata reports whether the retained-credential check
// compares the repaired metadata entry key of action with the prior's: an
// entry replay sends (a Kafka header or JMS property), or, in a protocol step,
// one whose repaired or prior value holds a {{param.*}} reference, which
// replay resolves and bind_variable may rewrite. A review entry the step does
// not send is never compared: the repair and its evaluation own it.
func storedRepairComparesMetadata(action dsl.Action, key string, repaired, prior any) bool {
	if unsentStepReviewMetadata(action, key) {
		return false
	}
	vars, params := varflow.MetadataReads(action, key)
	return vars || params && (metadataHoldsParameterReference(repaired) || metadataHoldsParameterReference(prior))
}

// metadataHoldsParameterReference reports whether a metadata value holds a
// {{param.*}} reference, read from its JSON as varflow reads a protocol step's
// metadata. A value that cannot be encoded is treated as holding one, so it
// stays compared.
func metadataHoldsParameterReference(value any) bool {
	if value == nil {
		return false
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return true
	}
	for _, ref := range varflow.References(string(payload)) {
		if ref.Namespace == varflow.NamespaceParam {
			return true
		}
	}
	return false
}

func scriptImportSource(script *dsl.Script) string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return ""
	}
	return strings.TrimSpace(script.Metadata.Import.Source)
}
