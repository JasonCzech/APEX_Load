package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/agentforce"
	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/brokerconfig"
	"github.com/jasonczech/apexload/pkg/convert/eventhubscapture"
	"github.com/jasonczech/apexload/pkg/convert/mulesoft"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/raml"
	"github.com/jasonczech/apexload/pkg/convert/wsdl"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

type openAPIImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      openapi.ConvertOptions
}

type wsdlImportRequest struct {
	Data      []byte
	RootFile  string
	Files     map[string]string
	ProjectID string
	Name      string
	Opts      wsdl.ConvertOptions
}

type postmanImportRequest struct {
	Data                []byte
	CollectionFileName  string
	EnvironmentData     []byte
	EnvironmentFileName string
	ProjectID           string
	Name                string
	Opts                postman.ConvertOptions
}

type asyncAPIImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      asyncapi.ConvertOptions
}

type ramlImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Files     map[string][]byte
	Opts      raml.ConvertOptions
}

func (s *Server) handleDevWebSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "devweb")
}

func (s *Server) handleImportOpenAPI(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readOpenAPIImportRequest(w, r)
	if !ok {
		return
	}
	result, err := openapi.Convert(openapi.ImportRequest{
		Name:      importReq.Name,
		ProjectID: importReq.ProjectID,
		Content:   string(importReq.Data),
		Options:   importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	draft := importDraft{
		Source:           "openapi",
		SourceScriptName: importReq.Name,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         buildOpenAPIReviewSections(result),
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings
	preparedCredentialReview, credentialReviewErr := prepareImportCredentialReview(
		[]*dsl.Script{result.Script}, protectedRequirements, nil,
	)
	if credentialReviewErr != nil {
		result.Warnings = append(result.Warnings, "Protected credential bindings could not be prepared; the generated secret references remain available for manual configuration.")
	}
	sessionID, expiresAt, sessionErr := s.registerImportCandidateSessionWithRequirements(
		r,
		"openapi",
		result.Script,
		protectedRequirements,
		importCandidateSource{Bytes: importReq.Data, Format: "openapi"},
	)
	reusesCandidateForDraftAttestation := attachImportCandidateDraftAttestation(
		result.Script,
		"openapi",
		sessionID,
		sessionErr,
	)
	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Result:                   result,
		Script:                   result.Script,
		Warnings:                 result.Warnings,
		Stats:                    result.Stats,
		Summary:                  result.Summary,
		Diagnostics:              result.Unsupported,
		UnsupportedElements:      result.Unsupported.Strings(),
		Correlations:             result.Correlations,
		ReviewSections:           augmented.Sections,
		SourceReview:             result.SourceReview,
		Parameterization:         buildImportParameterization(result.Script, openAPICorrelationsToPreApplied(result.Correlations)),
		Preflight:                augmented.Preflight,
		ProtectedRequirements:    protectedRequirements,
		PreparedCredentialReview: preparedCredentialReview,
		CredentialReviewPrepared: true,
		SkipDraftAttestation:     reusesCandidateForDraftAttestation,
	})
	addImportCandidateSession(response, sessionID, expiresAt, sessionErr)
	writeScriptImportResponse(w, http.StatusOK, response)
}

// attachImportCandidateDraftAttestation lets the editable legacy save path and
// the protected commit path consume the same one-shot candidate. Without this
// binding, response construction allocates a second protected candidate solely
// for the legacy attestation and a successful import leaves that extra capacity
// slot occupied until expiry.
func attachImportCandidateDraftAttestation(
	script *dsl.Script,
	profile string,
	sessionID string,
	sessionErr error,
) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionErr != nil || sessionID == "" || script == nil {
		return false
	}
	fingerprint, err := fingerprintImportDraftCredentials(script)
	if err == nil && fingerprint.HasRawCredentials {
		preserved := ensureScriptImportPreserved(script, profile)
		preserved[importDraftAttestationMetadataKey] =
			importDraftProtectedTokenPrefix + sessionID
	}
	// Even when the script has no raw credential, the caller has already
	// allocated its only protected candidate and no draft attestation is
	// necessary. If fingerprinting fails, the protected commit remains usable;
	// allocating a second candidate would not make the malformed legacy draft
	// attestable.
	return true
}

func (s *Server) handleOpenAPISupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "openapi")
}

type openAPIImportCommitRequest struct {
	Script          dsl.Script `json:"script"`
	ImportSessionID string     `json:"import_session_id,omitempty"`
	ProjectID       string     `json:"project_id,omitempty"`
	Name            *string    `json:"name,omitempty"`
	FolderID        *string    `json:"folder_id,omitempty"`
	Mode            string     `json:"mode,omitempty"`
	TargetScriptID  string     `json:"target_script_id,omitempty"`
	ReplaceMode     string     `json:"replace_mode,omitempty"`
	DryRun          bool       `json:"dry_run,omitempty"`
}

type openAPIImportDiff struct {
	Mode                 string   `json:"mode"`
	ReplaceMode          string   `json:"replace_mode,omitempty"`
	Added                int      `json:"added"`
	Updated              int      `json:"updated"`
	Removed              int      `json:"removed"`
	Preserved            int      `json:"preserved"`
	PreservedDataSources int      `json:"preserved_data_sources,omitempty"`
	PreservedParameters  int      `json:"preserved_parameters,omitempty"`
	NewColumns           []string `json:"new_columns,omitempty"`
	Items                []string `json:"items,omitempty"`
}

func (s *Server) handleCommitOpenAPIImport(w http.ResponseWriter, r *http.Request) {
	s.handleImportCommit(w, r, importCommitSpec{
		sourceProfile:         "openapi",
		auditProfile:          "openapi",
		displayName:           "OpenAPI",
		allowedReplaceModes:   []string{"full", "merge_by_operation_id"},
		actionKey:             openAPIActionKey,
		diff:                  diffImportByKey,
		merge:                 mergeImportScriptByKey,
		mapCompatibleResponse: identityImportCommitResponse,
	})
}

func (s *Server) handleRAMLSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "raml")
}

func (s *Server) handleImportRAML(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readRAMLImportRequest(w, r)
	if !ok {
		return
	}
	result, err := raml.Convert(raml.ImportRequest{
		Name:      importReq.Name,
		ProjectID: importReq.ProjectID,
		Content:   string(importReq.Data),
		Files:     importReq.Files,
		Options:   importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	draft := importDraft{
		Source:           "raml",
		SourceScriptName: importReq.Name,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		Unsupported:      result.Unsupported,
		Sections: []scriptImportReviewSection{{
			Key:      "raml_import",
			Title:    "RAML Import Review",
			Severity: "info",
			Summary:  "RAML resources and methods were converted to HTTP DSL actions.",
			Items:    ramlReviewItems(result),
		}},
		Warnings:   result.Warnings,
		Accounting: result.Accounting(),
	}
	// The RAML review section already surfaces Anypoint Client ID Enforcement
	// (the converter emits its own policy hint), so drop the duplicate from the
	// MuleSoft section rather than show the same guidance twice.
	if ramlSummaryReportsPolicy(result.Summary, "client_id_enforcement") {
		draft.SuppressedMuleSoftPolicies = []string{"client_id_enforcement"}
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                 result,
		Script:                 result.Script,
		Warnings:               result.Warnings,
		Stats:                  result.Stats,
		Summary:                result.Summary,
		Diagnostics:            result.Unsupported,
		UnsupportedElements:    result.Unsupported.Strings(),
		ReviewSections:         augmented.Sections,
		CredentialRequirements: result.SecretRequirements,
		Parameterization:       buildImportParameterization(result.Script, nil),
		Preflight:              augmented.Preflight,
	})
}

func ramlSummaryReportsPolicy(summary raml.ReviewSummary, policy string) bool {
	for _, p := range summary.Policies {
		if p.Policy == policy {
			return true
		}
	}
	return false
}

// dropMulesoftPolicies removes named Anypoint policies, and the credential
// blockers they raise, from a MuleSoft review. It is used when another review
// section already reports the same policy from the converter's own summary.
func dropMulesoftPolicies(review *mulesoft.Review, policies ...string) {
	if review == nil || len(policies) == 0 {
		return
	}
	dropped := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		dropped[policy] = struct{}{}
	}
	kept := review.PolicyFindings[:0]
	for _, finding := range review.PolicyFindings {
		if _, drop := dropped[finding.Policy]; !drop {
			kept = append(kept, finding)
		}
	}
	review.PolicyFindings = kept
	if _, drop := dropped["client_id_enforcement"]; !drop {
		return
	}
	blockers := review.Blockers[:0]
	for _, blocker := range review.Blockers {
		if !strings.Contains(blocker, "client_id/client_secret") {
			blockers = append(blockers, blocker)
		}
	}
	review.Blockers = blockers
}

func mulesoftReviewHasContent(review *mulesoft.Review) bool {
	return review != nil && (len(review.Domains) > 0 || len(review.PolicyFindings) > 0 || len(review.AuthFindings) > 0 || len(review.Blockers) > 0)
}

// attachMuleSoftReview runs the MuleSoft/Anypoint analyzer and, when the draft
// carries Anypoint signals, preserves the review on the script and returns the
// review section appended to the supplied sections. suppressedPolicies drops
// policies a caller's own section already reports, so the same Anypoint finding
// is not shown twice.
func attachMuleSoftReview(script *dsl.Script, source string, sections []scriptImportReviewSection, suppressedPolicies ...string) []scriptImportReviewSection {
	review := mulesoft.Analyze(script)
	if !review.Detected {
		return sections
	}
	dropMulesoftPolicies(review, suppressedPolicies...)
	if !mulesoftReviewHasContent(review) {
		return sections
	}
	if script != nil {
		ensureScriptImportPreserved(script, source)["mulesoft_review"] = review
	}
	return append(sections, mulesoftReviewSection(review))
}

func ramlReviewItems(result *raml.ConvertResult) []string {
	items := []string{"OAuth2 security schemes create an http_oauth2_token init action when an accessTokenUri is present."}
	for _, policy := range result.Summary.Policies {
		items = append(items, fmt.Sprintf("Policy: %s — %s", policy.Policy, policy.Detail))
	}
	return append(items, result.Summary.Resources...)
}

func mulesoftReviewSection(review *mulesoft.Review) scriptImportReviewSection {
	severity := "info"
	if len(review.Blockers) > 0 {
		severity = "warning"
	}
	items := make([]string, 0)
	for _, domain := range review.Domains {
		items = append(items, fmt.Sprintf("Anypoint domain: %s (%s, %d request(s))", domain.Host, domain.Kind, domain.Count))
	}
	for _, policy := range review.PolicyFindings {
		items = append(items, fmt.Sprintf("Policy: %s — %s", policy.Policy, policy.Detail))
	}
	for _, auth := range review.AuthFindings {
		items = append(items, fmt.Sprintf("Auth: %s — %s", auth.Type, auth.Detail))
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	summary := "MuleSoft/Anypoint signals were not detected."
	if review.Detected {
		summary = fmt.Sprintf("Detected MuleSoft/Anypoint signals across %d domain(s) with %d policy and %d auth finding(s).", len(review.Domains), len(review.PolicyFindings), len(review.AuthFindings))
	}
	return scriptImportReviewSection{
		Key:      "mulesoft_review",
		Title:    "MuleSoft / Anypoint Review",
		Severity: severity,
		Summary:  summary,
		Items:    items,
	}
}

// attachAgentforceReview runs the Agentforce analyzer and, when an Agent API
// conversation is detected, preserves the review on the script and returns the
// review section appended to the supplied sections. When transform is true it
// also rewrites the script in place to be replay-ready (token acquisition,
// session correlation, SSE, end-session teardown) and records the applied
// changes; transform is opt-in so default imports stay non-destructive.
func attachAgentforceReview(script *dsl.Script, source string, sections []scriptImportReviewSection, transform bool) []scriptImportReviewSection {
	review := agentforce.Analyze(script)
	if !review.Detected {
		return sections
	}
	var changes []agentforce.Change
	if transform && script != nil {
		changes = agentforce.Transform(script)
	}
	if script != nil {
		preserved := ensureScriptImportPreserved(script, source)
		preserved["agentforce_review"] = review
		if len(changes) > 0 {
			preserved["agentforce_transform"] = changes
		}
	}
	return append(sections, agentforceReviewSection(review, changes))
}

func agentforceReviewSection(review *agentforce.Review, changes []agentforce.Change) scriptImportReviewSection {
	severity := "info"
	if len(review.Blockers) > 0 {
		severity = "warning"
	}
	items := make([]string, 0)
	for _, step := range review.Lifecycle {
		items = append(items, fmt.Sprintf("Lifecycle: %s — %s %s", step.Step, step.Method, step.Path))
	}
	for _, sessionVar := range review.SessionVars {
		items = append(items, fmt.Sprintf("Correlation: extract %s via %s %q", sessionVar.Name, sessionVar.Type, sessionVar.Expression))
	}
	for _, hint := range review.Assertions {
		items = append(items, "Assertion: "+hint.Detail)
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	for _, change := range changes {
		if change.ActionName != "" {
			items = append(items, fmt.Sprintf("Applied (%s): %s — %s", change.Step, change.ActionName, change.Detail))
		} else {
			items = append(items, fmt.Sprintf("Applied (%s): %s", change.Step, change.Detail))
		}
	}
	streaming := "non-streaming"
	if review.Streaming {
		streaming = "streaming (SSE)"
	}
	summary := fmt.Sprintf("Detected an Agentforce Agent API conversation (%s) across %d lifecycle step(s).", streaming, len(review.Lifecycle))
	if len(changes) > 0 {
		summary += fmt.Sprintf(" Applied %d replay-readiness change(s).", len(changes))
	}
	return scriptImportReviewSection{
		Key:      "agentforce_review",
		Title:    "Agentforce Agent API Review",
		Severity: severity,
		Summary:  summary,
		Items:    items,
	}
}

// agentforceTransformRequested reports whether the caller opted into the
// Agentforce replay-readiness transform via ?agentforce_transform=true. It reads
// the query string (independent of the JSON/multipart body) so every import entry
// point can honor it uniformly.
func agentforceTransformRequested(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agentforce_transform"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) handleImportWSDL(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readWSDLImportRequest(w, r)
	if !ok {
		return
	}
	result, err := wsdl.Convert(wsdl.ImportRequest{
		Name:      importReq.Name,
		ProjectID: importReq.ProjectID,
		Content:   string(importReq.Data),
		RootFile:  importReq.RootFile,
		Files:     importReq.Files,
		Options:   importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	draft := importDraft{
		Source:           "wsdl",
		SourceScriptName: importReq.Name,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         buildWSDLReviewSections(result),
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings
	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                 result,
		Script:                 result.Script,
		Warnings:               result.Warnings,
		Stats:                  result.Stats,
		Summary:                result.Summary,
		Diagnostics:            result.Unsupported,
		UnsupportedElements:    result.Unsupported.Strings(),
		ReviewSections:         augmented.Sections,
		SourceReview:           result.SourceReview,
		CredentialRequirements: result.SecretRequirements,
		Parameterization:       buildImportParameterization(result.Script, nil),
		Preflight:              augmented.Preflight,
	})
}

func (s *Server) handleImportPostman(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readPostmanImportRequest(w, r)
	if !ok {
		return
	}
	result, err := postman.Convert(postman.ImportRequest{
		Name:                importReq.Name,
		ProjectID:           importReq.ProjectID,
		Content:             string(importReq.Data),
		CollectionFileName:  importReq.CollectionFileName,
		EnvironmentContent:  string(importReq.EnvironmentData),
		EnvironmentFileName: importReq.EnvironmentFileName,
		Options:             importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	s.writePostmanImportResponse(w, r, result, importReq, agentforceTransformRequested(r))
}

func (s *Server) handleValidatePostmanImport(w http.ResponseWriter, r *http.Request) {
	s.handleImportPostman(w, r)
}

func (s *Server) writePostmanImportResponse(w http.ResponseWriter, r *http.Request, result *postman.ConvertResult, importReq *postmanImportRequest, agentforceTransform bool) {
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	draft := importDraft{
		Source:           "postman",
		SourceScriptName: importReq.Name,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         buildPostmanReviewSections(result),
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.augmentWithAgentforceTransform(agentforceTransform)
	result.Warnings = augmented.Warnings
	preparedCredentialReview, credentialReviewErr := prepareImportCredentialReview(
		[]*dsl.Script{result.Script}, protectedRequirements, nil,
	)
	if credentialReviewErr != nil {
		result.Warnings = append(result.Warnings, "Protected credential bindings could not be prepared; the generated secret references remain available for manual configuration.")
	}
	protectedSource := postman.BuildProtectedSourceBundle(
		importReq.CollectionFileName,
		importReq.Data,
		importReq.EnvironmentFileName,
		importReq.EnvironmentData,
	)
	defer clear(protectedSource)
	sessionID, expiresAt, sessionErr := s.registerImportCandidateSessionWithRequirements(
		r,
		"postman",
		result.Script,
		protectedRequirements,
		importCandidateSource{Bytes: protectedSource, Format: "postman"},
	)
	reusesCandidateForDraftAttestation := attachImportCandidateDraftAttestation(
		result.Script,
		"postman",
		sessionID,
		sessionErr,
	)
	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Result:                   result,
		Script:                   result.Script,
		Warnings:                 result.Warnings,
		Stats:                    result.Stats,
		Summary:                  result.Summary,
		Diagnostics:              result.Unsupported,
		UnsupportedElements:      result.Unsupported.Strings(),
		ReviewSections:           augmented.Sections,
		SourceReview:             result.SourceReview,
		Correlations:             result.Correlations,
		Parameterization:         buildImportParameterization(result.Script, postmanCorrelationsToPreApplied(result.Correlations)),
		Preflight:                augmented.Preflight,
		ProtectedRequirements:    protectedRequirements,
		PreparedCredentialReview: preparedCredentialReview,
		CredentialReviewPrepared: true,
		SkipDraftAttestation:     reusesCandidateForDraftAttestation,
	})
	addImportCandidateSession(response, sessionID, expiresAt, sessionErr)
	writeScriptImportResponse(w, http.StatusOK, response)
}

func (s *Server) handleSavePostmanImport(w http.ResponseWriter, r *http.Request) {
	s.handleImportCommit(w, r, importCommitSpec{
		sourceProfile:         "postman",
		auditProfile:          "postman",
		displayName:           "Postman",
		allowedReplaceModes:   []string{"full", "merge_by_item"},
		actionKey:             postmanActionKey,
		diff:                  diffImportByKey,
		merge:                 mergeImportScriptByKey,
		mapCompatibleResponse: identityImportCommitResponse,
	})
}

func (s *Server) handlePostmanSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "postman")
}

func (s *Server) handleAsyncAPISupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "asyncapi")
}

func (s *Server) handleImportAsyncAPI(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readAsyncAPIImportRequest(w, r)
	if !ok {
		return
	}
	result, err := asyncapi.Convert(asyncapi.ImportRequest{
		Name:      importReq.Name,
		ProjectID: importReq.ProjectID,
		Content:   string(importReq.Data),
		Options:   importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	draft := importDraft{
		Source:           "asyncapi",
		SourceScriptName: importReq.Name,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		Scripts:          result.Scripts,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         buildAsyncAPIReviewSections(result),
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings
	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                 result,
		Scripts:                result.Scripts,
		Script:                 result.Script,
		Warnings:               result.Warnings,
		Stats:                  result.Stats,
		Summary:                result.Summary,
		Diagnostics:            result.Unsupported,
		UnsupportedElements:    result.Unsupported.Strings(),
		ReviewSections:         augmented.Sections,
		SourceReview:           result.SourceReview,
		CredentialRequirements: result.SecretRequirements,
		Parameterization:       buildMultiScriptParameterization(result.Scripts),
		Preflight:              augmented.Preflight,
	})
}

func (s *Server) handleEventHubsCaptureSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "eventhubs_capture")
}

// handleImportEventHubsCapture imports an Event Hubs Capture Avro file (binary)
// into a native eventhubs producer script. Accepts a multipart file upload or a
// raw binary body.
func (s *Server) handleImportEventHubsCapture(w http.ResponseWriter, r *http.Request) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return
	}
	data, name, projectID := upload.Data, upload.Name, upload.ProjectID

	maxEvents := 0
	if v := strings.TrimSpace(upload.Value("max_events")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxEvents = n
		}
	}

	result, err := eventhubscapture.Convert(eventhubscapture.ConvertRequest{
		Name:      name,
		ProjectID: projectID,
		EventHub:  strings.TrimSpace(upload.Value("event_hub")),
		Data:      data,
		MaxEvents: maxEvents,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	draft := importDraft{
		Source:           "eventhubs_capture",
		SourceScriptName: name,
		ProjectID:        projectID,
		Script:           result.Script,
		Sections:         buildEventHubsCaptureReviewSections(result),
		Warnings:         result.Warnings,
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings
	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:           result,
		Script:           result.Script,
		Warnings:         result.Warnings,
		Stats:            result.Stats,
		Summary:          result.Summary,
		ReviewSections:   augmented.Sections,
		Parameterization: buildImportParameterization(result.Script, nil),
		Preflight:        augmented.Preflight,
	})
}

func buildEventHubsCaptureReviewSections(result *eventhubscapture.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	severity := "info"
	if result.Stats.PartitionKeys == 0 || result.Stats.EmptyBodies > 0 {
		severity = "warning"
	}
	items := []string{
		fmt.Sprintf("Events read: %d", result.Stats.RecordsRead),
		fmt.Sprintf("Produce actions seeded: %d", result.Stats.ActionsEmitted),
		fmt.Sprintf("Partition keys captured: %d", result.Stats.PartitionKeys),
	}
	if result.Stats.EmptyBodies > 0 {
		items = append(items, fmt.Sprintf("Empty bodies: %d", result.Stats.EmptyBodies))
	}
	return []scriptImportReviewSection{{
		Key:      "eventhubs_capture",
		Title:    "Event Hubs Capture",
		Severity: severity,
		Summary:  "Captured events seeded as eventhubs_produce actions (mock mode; set a namespace and build -tags eventhubs_real to run live).",
		Items:    items,
	}}
}

func (s *Server) handleBrokerConfigSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "broker_config")
}

// handleImportBrokerConfig imports a standalone messaging broker-config artifact
// (IBM MQ MQSC script or JMS JNDI .properties file) into a JMS script skeleton
// seeded from the broker's queue/topic topology. Accepts a multipart file upload
// or a raw body; the format auto-detects but can be pinned via ?format=mqsc|jndi.
func (s *Server) handleImportBrokerConfig(w http.ResponseWriter, r *http.Request) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return
	}
	data, name, projectID := upload.Data, upload.Name, upload.ProjectID

	maxDest := 0
	if v := strings.TrimSpace(upload.Value("max_destinations")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxDest = n
		}
	}

	result, err := brokerconfig.Convert(brokerconfig.ConvertRequest{
		Name:            name,
		ProjectID:       projectID,
		Data:            data,
		Format:          strings.TrimSpace(upload.Value("format")),
		MaxDestinations: maxDest,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	draft := importDraft{
		Source:           "broker_config",
		SourceScriptName: name,
		ProjectID:        projectID,
		Script:           result.Script,
		Sections:         buildBrokerConfigReviewSections(result),
		Warnings:         result.Warnings,
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings
	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:           result,
		Script:           result.Script,
		Warnings:         result.Warnings,
		Stats:            result.Stats,
		Summary:          result.Summary,
		ReviewSections:   augmented.Sections,
		Parameterization: buildImportParameterization(result.Script, nil),
		Preflight:        augmented.Preflight,
	})
}

func buildBrokerConfigReviewSections(result *brokerconfig.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	severity := "info"
	summary := "JNDI bindings seeded as jms-solace destinations (executable with -tags jms_solace_real)."
	if result.Format == brokerconfig.FormatMQSC {
		severity = "warning"
		summary = "MQSC objects seeded as jms-ibmmq destinations; the IBM MQ runtime adapter is not built in, so execution is mock-only/review."
	}
	items := []string{
		fmt.Sprintf("Format: %s", result.Stats.Format),
		fmt.Sprintf("Queues discovered: %d", result.Stats.Queues),
		fmt.Sprintf("Topics discovered: %d", result.Stats.Topics),
		fmt.Sprintf("Actions seeded: %d", result.Stats.ActionsEmitted),
	}
	return []scriptImportReviewSection{{
		Key:      "broker_config",
		Title:    "Broker Config",
		Severity: severity,
		Summary:  summary,
		Items:    items,
	}}
}

func buildAsyncAPIReviewSections(result *asyncapi.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "asyncapi_operations",
			Title:    "AsyncAPI Operations",
			Severity: "info",
			Summary:  "Channels converted into Kafka produce/consume/request-reply actions.",
			Items:    result.Summary.OperationNames,
		},
	}
	if len(result.Summary.Topics) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "asyncapi_topics",
			Title:    "Kafka Topics",
			Severity: "info",
			Summary:  "Topics resolved from channel kafka bindings, addresses, and names.",
			Items:    result.Summary.Topics,
		})
	}
	if result.Summary.SecurityProtocol != "" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "asyncapi_security",
			Title:    "Security Review",
			Severity: "warning",
			Summary:  "Confirm the Kafka security protocol, SASL mechanism, and credentials before running against a real cluster.",
			Items:    []string{result.Summary.SecurityProtocol},
		})
	}
	if len(result.Summary.DataColumns) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "asyncapi_data_fit",
			Title:    "Payload Data Fit",
			Severity: "info",
			Summary:  "Generated CSV seed columns for scalar payload fields.",
			Items:    result.Summary.DataColumns,
		})
	}
	return sections
}

func readAsyncAPIImportRequest(w http.ResponseWriter, r *http.Request) (*asyncAPIImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req asyncapi.ImportRequest
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		// A bare AsyncAPI document posted as JSON is not an envelope: it carries
		// no content field but does carry an asyncapi version key. Fall through
		// and treat the body itself as the document.
		if strings.TrimSpace(req.Content) != "" || !strings.Contains(string(upload.Data), `"asyncapi"`) {
			return &asyncAPIImportRequest{
				Data:      []byte(req.Content),
				ProjectID: upload.jsonProjectID(req.ProjectID),
				Name:      req.Name,
				Opts:      req.Options,
			}, true
		}
	}
	return &asyncAPIImportRequest{
		Data:      upload.Data,
		ProjectID: upload.ProjectID,
		Name:      upload.bareArtifactName(),
		Opts:      asyncAPIOptionsFromValues(upload.Values()),
	}, true
}

func asyncAPIOptionsFromValues(values map[string][]string) asyncapi.ConvertOptions {
	get := func(keys ...string) string {
		for _, key := range keys {
			if raw := strings.TrimSpace(firstValue(values[key])); raw != "" {
				return raw
			}
		}
		return ""
	}
	opts := asyncapi.ConvertOptions{
		ScriptName:        get("script_name"),
		Brokers:           splitCSV(get("brokers", "broker")),
		Channels:          splitCSV(get("channels", "channel_filter")),
		SchemaRegistryURL: get("schema_registry_url"),
		SchemaSubject:     get("schema_subject", "subject"),
		Serialization:     get("serialization"),
	}
	if v := get("default_think_time_ms", "think_time_ms"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.DefaultThinkTimeMs = n
		}
	}
	if v := get("schema_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.SchemaID = n
		}
	}
	if v := get("mock"); v != "" {
		b := parseBoolDefault(v, true)
		opts.Mock = &b
	}
	if v := get("include_assertions"); v != "" {
		b := parseBoolDefault(v, true)
		opts.IncludeAssertions = &b
	}
	return opts
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func readOpenAPIImportRequest(w http.ResponseWriter, r *http.Request) (*openAPIImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req openapi.ImportRequest
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		// A bare OpenAPI document posted as JSON is not an envelope: it carries
		// no content field but does carry a paths object. Fall through and treat
		// the body itself as the spec, reading options from the query string.
		if strings.TrimSpace(req.Content) != "" || !strings.Contains(string(upload.Data), `"paths"`) {
			return &openAPIImportRequest{
				Data:      []byte(req.Content),
				ProjectID: upload.jsonProjectID(req.ProjectID),
				Name:      req.Name,
				Opts:      req.Options,
			}, true
		}
	}
	return &openAPIImportRequest{
		Data:      upload.Data,
		ProjectID: upload.ProjectID,
		Name:      upload.bareArtifactName(),
		Opts: openapi.ConvertOptions{
			BaseURL:    upload.Value("base_url"),
			ScriptName: upload.Value("script_name"),
		},
	}, true
}

func readRAMLImportRequest(w http.ResponseWriter, r *http.Request) (*ramlImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return nil, false
	}

	importReq := &ramlImportRequest{
		Data:      upload.Data,
		ProjectID: upload.ProjectID,
		Name:      upload.Name,
		Opts: raml.ConvertOptions{
			BaseURL:    upload.Value("base_url"),
			ScriptName: upload.Value("script_name"),
		},
	}
	// origName keeps the untrimmed filename so the zip branch below can tell a
	// name derived from an archive apart from a real RAML entry name.
	origName := upload.Filename
	if upload.Transport == importTransportJSON {
		var req raml.ImportRequest
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		importReq.Data = []byte(req.Content)
		importReq.ProjectID = upload.jsonProjectID(req.ProjectID)
		importReq.Name = req.Name
		importReq.Opts = req.Options
		origName = req.Name
	}

	if isZipArchive(importReq.Data) {
		mainContent, files, mainName, err := extractRAMLZip(importReq.Data)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "RAML zip: "+err.Error())
			return nil, false
		}
		importReq.Data = mainContent
		importReq.Files = files
		// Replace a zip-derived/empty script name with the main RAML entry name.
		// Check the ORIGINAL filename (the stripped Name no longer ends in .zip).
		if strings.TrimSpace(importReq.Name) == "" || strings.HasSuffix(strings.ToLower(origName), ".zip") {
			importReq.Name = strings.TrimSuffix(archiveingest.LogicalBase(mainName), archiveingest.LogicalExt(mainName))
		}
	}
	return importReq, true
}

// isZipArchive reports whether data begins with the ZIP local-file-header magic.
func isZipArchive(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04
}

// extractRAMLZip reads a RAML zip upload, returning the main RAML content, the
// full file set (keyed by archive path, used to resolve !include), and the main
// entry name.
func extractRAMLZip(data []byte) ([]byte, map[string][]byte, string, error) {
	const maxRAMLZipEntries = 2000
	// archiveingest owns the shared entry-path policy and the zip-bomb guards
	// (entry count, per-entry and total decompressed bytes, compression ratio).
	// The map is keyed by the validated relative path, which is also what
	// resolves !include, so a traversal segment can no longer reach a lookup key.
	files, err := archiveingest.ReadZIP(data, archiveingest.Options{
		Limits: archiveingest.Limits{
			MaxEntries:    maxRAMLZipEntries,
			MaxEntryBytes: webAPIImportMaxBodyBytes,
			MaxTotalBytes: webAPIImportMaxBodyBytes,
		},
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading RAML archive: %w", err)
	}
	fileMap := make(map[string][]byte, len(files))
	for _, file := range files {
		fileMap[file.Name] = file.Data
	}
	mainName := pickMainRAML(fileMap)
	if mainName == "" {
		return nil, nil, "", fmt.Errorf("no .raml entry found in archive")
	}
	return fileMap[mainName], fileMap, mainName, nil
}

// pickMainRAML selects the root RAML document from a zip: a file named
// api.raml/root.raml wins, otherwise the shallowest .raml entry (alpha tie-break).
func pickMainRAML(files map[string][]byte) string {
	best := ""
	bestDepth := 1 << 30
	for name := range files {
		if !strings.HasSuffix(strings.ToLower(name), ".raml") {
			continue
		}
		depth := strings.Count(name, "/")
		base := strings.ToLower(path.Base(name))
		if base == "api.raml" || base == "root.raml" {
			depth = -1
		}
		if depth < bestDepth || (depth == bestDepth && (best == "" || name < best)) {
			bestDepth = depth
			best = name
		}
	}
	return best
}

func readWSDLImportRequest(w http.ResponseWriter, r *http.Request) (*wsdlImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req wsdl.ImportRequest
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &wsdlImportRequest{
			Data:      []byte(req.Content),
			RootFile:  req.RootFile,
			Files:     req.Files,
			ProjectID: upload.jsonProjectID(req.ProjectID),
			Name:      req.Name,
			Opts:      req.Options,
		}, true
	}

	importReq := &wsdlImportRequest{
		Data:      upload.Data,
		ProjectID: upload.ProjectID,
		Name:      upload.Name,
		Opts:      wsdlConvertOptionsFromValue(upload.Value),
	}
	// A WSDL upload may be a single document or an archive of a root document
	// plus its imported schemas. The archive is recognised by extension, and on
	// a raw body also by content type, since a bare POST need not name the file.
	zipUpload := strings.EqualFold(archiveingest.LogicalExt(upload.Filename), ".zip")
	if upload.Transport == importTransportRaw {
		contentType := r.Header.Get("Content-Type")
		zipUpload = zipUpload ||
			strings.HasPrefix(contentType, "application/zip") ||
			strings.HasPrefix(contentType, "application/x-zip-compressed")
	}
	if zipUpload {
		root, archiveFiles, err := wsdl.FilesFromZip(upload.Data)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "WSDL zip: "+err.Error())
			return nil, false
		}
		importReq.RootFile = root
		importReq.Files = archiveFiles
		importReq.Data = []byte(archiveFiles[root])
		importReq.Name = strings.TrimSuffix(path.Base(root), path.Ext(root))
	}
	return importReq, true
}

func wsdlConvertOptionsFromValue(value func(string) string) wsdl.ConvertOptions {
	return wsdl.ConvertOptions{
		BaseURL:     value("base_url"),
		ScriptName:  value("script_name"),
		SOAPVersion: value("soap_version"),
		ServiceName: value("service_name"),
		PortName:    value("port_name"),
		BindingName: value("binding_name"),
	}
}

func readPostmanImportRequest(w http.ResponseWriter, r *http.Request) (*postmanImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req postman.ImportRequest
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		// A bare collection posted as JSON is not an envelope: it carries no
		// content field but does carry an item array. Fall through and treat the
		// body itself as the collection.
		if strings.TrimSpace(req.Content) != "" || !strings.Contains(string(upload.Data), `"item"`) {
			return &postmanImportRequest{
				Data:                []byte(req.Content),
				CollectionFileName:  req.CollectionFileName,
				EnvironmentData:     []byte(req.EnvironmentContent),
				EnvironmentFileName: req.EnvironmentFileName,
				ProjectID:           upload.jsonProjectID(req.ProjectID),
				Name:                req.Name,
				Opts:                req.Options,
			}, true
		}
	}

	importReq := &postmanImportRequest{
		Data:               upload.Data,
		CollectionFileName: upload.Filename,
		ProjectID:          upload.ProjectID,
		Name:               upload.bareArtifactName(),
		Opts:               postmanConvertOptionsFromValue(upload.Value),
	}
	// A Postman environment is a second file, so it only travels on a multipart
	// upload; the other transports carry the collection alone.
	if upload.Transport == importTransportMultipart {
		environmentData, environmentFilename, ok := readPostmanEnvironmentMultipartFile(w, r)
		if !ok {
			return nil, false
		}
		importReq.EnvironmentData = environmentData
		importReq.EnvironmentFileName = environmentFilename
	}
	return importReq, true
}

func postmanConvertOptionsFromValue(value func(string) string) postman.ConvertOptions {
	opts := postman.ConvertOptions{
		BaseURL:    value("base_url"),
		ScriptName: value("script_name"),
	}
	if raw := strings.TrimSpace(value("include_assertions")); raw != "" {
		parsed := parseBoolDefault(raw, true)
		opts.IncludeAssertions = &parsed
	}
	if raw := strings.TrimSpace(value("sanitize_sensitive")); raw != "" {
		parsed := parseBoolDefault(raw, true)
		opts.SanitizeSensitive = &parsed
	}
	if raw := strings.TrimSpace(value("use_parameter_data_source")); raw != "" {
		parsed := parseBoolDefault(raw, true)
		opts.UseParameterDataSet = &parsed
	}
	if raw := strings.TrimSpace(value("default_think_time_ms")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			opts.DefaultThinkTimeMs = n
		}
	}
	return opts
}

func readPostmanEnvironmentMultipartFile(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	if r == nil || r.MultipartForm == nil {
		return nil, "", true
	}
	for _, field := range []string{"environment", "environment_file"} {
		headers := r.MultipartForm.File[field]
		if len(headers) == 0 || headers[0] == nil {
			continue
		}
		file, err := headers[0].Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to open uploaded Postman environment: "+err.Error())
			return nil, "", false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded Postman environment: "+err.Error())
			return nil, "", false
		}
		return data, archiveingest.LogicalBase(headers[0].Filename), true
	}
	return nil, "", true
}

func buildOpenAPIReviewSections(result *openapi.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "openapi_operations",
			Title:    "OpenAPI Operations",
			Severity: "info",
			Summary:  "Converted operations available for replay after auth/data review.",
			Items:    result.Summary.OperationNames,
		},
	}
	if len(result.Summary.AuthSchemes) > 0 || len(result.Summary.OperationAuth) > 0 {
		items := result.Summary.OperationAuth
		if len(items) == 0 {
			items = result.Summary.AuthSchemes
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "openapi_auth",
			Title:    "Authentication Review",
			Severity: "warning",
			Summary:  "Each secured operation is listed with the exact OpenAPI security scheme it uses. Configure the generated secret or environment bindings before replay.",
			Items:    items,
		})
	}
	if len(result.Summary.DataColumns) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "openapi_data_fit",
			Title:    "Parameter Data Fit",
			Severity: "info",
			Summary:  "Generated CSV seed columns for path, query, and request-body placeholders.",
			Items:    result.Summary.DataColumns,
		})
	}
	return sections
}

func buildWSDLReviewSections(result *wsdl.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "wsdl_operations",
			Title:    "WSDL Operations",
			Severity: "info",
			Summary:  "Generated SOAP envelopes for review and replay.",
			Items:    result.Summary.Operations,
		},
	}
	if result.Summary.TargetNamespace != "" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "wsdl_namespace",
			Title:    "Namespace",
			Severity: "info",
			Summary:  "Target namespace preserved from the WSDL.",
			Items:    []string{result.Summary.TargetNamespace},
		})
	}
	endpointItems := []string{}
	if result.Summary.ServiceName != "" {
		endpointItems = append(endpointItems, "service: "+result.Summary.ServiceName)
	}
	if result.Summary.PortName != "" {
		endpointItems = append(endpointItems, "port: "+result.Summary.PortName)
	}
	if result.Summary.BindingName != "" {
		endpointItems = append(endpointItems, "binding: "+result.Summary.BindingName)
	}
	if result.Summary.Endpoint != "" {
		endpointItems = append(endpointItems, "endpoint: "+result.Summary.Endpoint)
	}
	if result.Summary.SOAPVersion != "" {
		endpointItems = append(endpointItems, "soap: "+result.Summary.SOAPVersion)
	}
	if result.Summary.BindingStyle != "" {
		endpointItems = append(endpointItems, "style: "+result.Summary.BindingStyle)
	}
	if result.Summary.BindingUse != "" {
		endpointItems = append(endpointItems, "use: "+result.Summary.BindingUse)
	}
	if len(endpointItems) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "wsdl_binding",
			Title:    "Selected Binding",
			Severity: "info",
			Summary:  "WSDL service, port, binding, and endpoint selected for replay.",
			Items:    endpointItems,
		})
	}
	if len(result.Summary.BindingAlternatives) > 0 {
		items := make([]string, 0, len(result.Summary.BindingAlternatives))
		for _, alternative := range result.Summary.BindingAlternatives {
			label := strings.TrimSpace(strings.Join([]string{
				alternative.ServiceName,
				alternative.PortName,
				alternative.BindingName,
				alternative.SOAPVersion,
				alternative.Endpoint,
			}, " "))
			if label != "" {
				items = append(items, label)
			}
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "wsdl_binding_alternatives",
			Title:    "Binding Alternatives",
			Severity: "warning",
			Summary:  "Additional SOAP bindings were found; use service_name, port_name, or binding_name to choose one explicitly.",
			Items:    items,
		})
	}
	return sections
}

func buildPostmanReviewSections(result *postman.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "postman_requests",
			Title:    "Postman Requests",
			Severity: "info",
			Summary:  "Converted Postman requests into HTTP actions for replay review.",
			Items:    result.Summary.Requests,
		},
	}
	if len(result.Summary.SourceFiles) > 0 {
		summary := "The converted script was derived from the uploaded Postman collection."
		if result.Summary.EnvironmentName != "" || len(result.Summary.SourceFiles) > 1 {
			summary = "The collection and selected Postman environment are treated as one protected source bundle; environment values take precedence during variable resolution."
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "postman_source_provenance",
			Title:    "Source Provenance",
			Severity: "info",
			Summary:  summary,
			Items:    append([]string(nil), result.Summary.SourceFiles...),
		})
	}
	if len(result.Summary.Variables) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "postman_variables",
			Title:    "Collection Variables",
			Severity: "info",
			Summary:  "Collection variables were converted into APEX script parameters.",
			Items:    result.Summary.Variables,
		})
	}
	if len(result.Summary.AuthSchemes) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "postman_auth",
			Title:    "Authentication Review",
			Severity: "warning",
			Summary:  "Postman auth settings were detected; review generated placeholders before replay.",
			Items:    result.Summary.AuthSchemes,
		})
	}
	return sections
}
