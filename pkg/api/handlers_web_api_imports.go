package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/agentforce"
	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/brokerconfig"
	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/eventhubscapture"
	"github.com/jasonczech/apexload/pkg/convert/mulesoft"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/raml"
	"github.com/jasonczech/apexload/pkg/convert/wsdl"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type openAPIImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      openapi.ConvertOptions
}

type wsdlImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      wsdl.ConvertOptions
}

type postmanImportRequest struct {
	Data            []byte
	EnvironmentData []byte
	ProjectID       string
	Name            string
	Opts            postman.ConvertOptions
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
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix":  devweb.GetSupportMatrix(),
		"notes":           devweb.GetSupportMatrixNotes(),
		"tested_examples": devweb.GetSupportMatrixExamples(),
	})
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
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "openapi", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	reviewSections := appendImportDiagnosticReviewSections(buildOpenAPIReviewSections(result), result.Unsupported)
	if muleReview := mulesoft.Analyze(result.Script); muleReview.Detected {
		if result.Script != nil {
			ensureScriptImportPreserved(result.Script, "openapi")["mulesoft_review"] = muleReview
		}
		reviewSections = append(reviewSections, mulesoftReviewSection(muleReview))
	}
	reviewSections = attachAgentforceReview(result.Script, "openapi", reviewSections, agentforceTransformRequested(r))
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      reviewSections,
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func (s *Server) handleRAMLSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": raml.GetSupportMatrix(),
		"notes":          raml.GetSupportMatrixNotes(),
	})
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
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "raml", importReq.Name, nil)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	reviewSections := []scriptImportReviewSection{{
		Key:      "raml_import",
		Title:    "RAML Import Review",
		Severity: "info",
		Summary:  "RAML resources and methods were converted to HTTP DSL actions.",
		Items:    ramlReviewItems(result),
	}}
	muleReview := mulesoft.Analyze(result.Script)
	if muleReview.Detected {
		// The RAML review section already surfaces Anypoint Client ID Enforcement
		// (the converter emits its own policy hint); drop the duplicate from the
		// MuleSoft section so the same guidance is not shown twice.
		if ramlSummaryReportsPolicy(result.Summary, "client_id_enforcement") {
			dropMulesoftClientIDEnforcement(muleReview)
		}
		if mulesoftReviewHasContent(muleReview) {
			if result.Script != nil {
				ensureScriptImportPreserved(result.Script, "raml")["mulesoft_review"] = muleReview
			}
			reviewSections = append(reviewSections, mulesoftReviewSection(muleReview))
		}
	}
	reviewSections = attachAgentforceReview(result.Script, "raml", reviewSections, agentforceTransformRequested(r))

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      reviewSections,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func ramlSummaryReportsPolicy(summary raml.ReviewSummary, policy string) bool {
	for _, p := range summary.Policies {
		if p.Policy == policy {
			return true
		}
	}
	return false
}

// dropMulesoftClientIDEnforcement removes the client_id_enforcement policy and
// its credential blocker from a MuleSoft review (used when another section
// already reports the same Anypoint policy).
func dropMulesoftClientIDEnforcement(review *mulesoft.Review) {
	policies := review.PolicyFindings[:0]
	for _, p := range review.PolicyFindings {
		if p.Policy != "client_id_enforcement" {
			policies = append(policies, p)
		}
	}
	review.PolicyFindings = policies
	blockers := review.Blockers[:0]
	for _, b := range review.Blockers {
		if !strings.Contains(b, "client_id/client_secret") {
			blockers = append(blockers, b)
		}
	}
	review.Blockers = blockers
}

func mulesoftReviewHasContent(review *mulesoft.Review) bool {
	return review != nil && (len(review.Domains) > 0 || len(review.PolicyFindings) > 0 || len(review.AuthFindings) > 0 || len(review.Blockers) > 0)
}

func attachMuleSoftReview(script *dsl.Script, source string, sections []scriptImportReviewSection) []scriptImportReviewSection {
	review := mulesoft.Analyze(script)
	if !review.Detected || !mulesoftReviewHasContent(review) {
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
		Options:   importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "wsdl", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      appendImportDiagnosticReviewSections(buildWSDLReviewSections(result), result.Unsupported),
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func (s *Server) handleImportPostman(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readPostmanImportRequest(w, r)
	if !ok {
		return
	}
	result, err := postman.Convert(postman.ImportRequest{
		Name:               importReq.Name,
		ProjectID:          importReq.ProjectID,
		Content:            string(importReq.Data),
		EnvironmentContent: string(importReq.EnvironmentData),
		Options:            importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	s.writePostmanImportResponse(w, result, importReq, agentforceTransformRequested(r))
}

func (s *Server) handleValidatePostmanImport(w http.ResponseWriter, r *http.Request) {
	s.handleImportPostman(w, r)
}

func (s *Server) writePostmanImportResponse(w http.ResponseWriter, result *postman.ConvertResult, importReq *postmanImportRequest, agentforceTransform bool) {
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "postman", importReq.Name, result.SourceReview)
	issues := validateScript(result.Script)
	preflight := buildReplayPreflight(result.Script, nil, issues, result.Warnings)
	attachImportReplayPreflight(result.Script, "postman", preflight)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	reviewSections := appendImportDiagnosticReviewSections(buildPostmanReviewSections(result), result.Unsupported)
	if muleReview := mulesoft.Analyze(result.Script); muleReview.Detected {
		if result.Script != nil {
			ensureScriptImportPreserved(result.Script, "postman")["mulesoft_review"] = muleReview
		}
		reviewSections = append(reviewSections, mulesoftReviewSection(muleReview))
	}
	reviewSections = attachAgentforceReview(result.Script, "postman", reviewSections, agentforceTransform)
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      reviewSections,
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
		Preflight:           preflight,
	}))
}

func (s *Server) handleSavePostmanImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Script         dsl.Script `json:"script"`
		Mode           string     `json:"mode,omitempty"`
		TargetScriptID string     `json:"target_script_id,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	script := &req.Script
	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}
	preflight := buildReplayPreflight(script, nil, nil, validationWarnings(script))
	attachImportReplayPreflight(script, "postman", preflight)

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	targetID := strings.TrimSpace(req.TargetScriptID)
	replacing := mode == "replace" || targetID != ""
	if replacing {
		if targetID == "" {
			targetID = strings.TrimSpace(script.ID)
		}
		if targetID == "" {
			writeError(w, http.StatusBadRequest, "target_script_id is required when replacing an existing Postman import")
			return
		}
		updated, ok := s.runner.UpdateScript(targetID, script)
		if !ok {
			writeError(w, http.StatusNotFound, "target script not found")
			return
		}
		s.recordAuditEvent(r, "script.import.postman.replace", "success", "script", updated.ID, updated.ProjectID, map[string]any{"name": updated.Name})
		writeJSON(w, http.StatusOK, map[string]any{
			"scripts":       []*StoredScript{updated},
			"script":        updated.Script,
			"stored_script": updated,
			"replaced":      true,
			"preflight":     preflight,
		})
		return
	}

	stored := s.runner.StoreScript(script)
	s.recordAuditEvent(r, "script.import.postman.create", "success", "script", stored.ID, stored.ProjectID, map[string]any{"name": stored.Name})
	writeJSON(w, http.StatusCreated, map[string]any{
		"scripts":       []*StoredScript{stored},
		"script":        stored.Script,
		"stored_script": stored,
		"replaced":      false,
		"preflight":     preflight,
	})
}

func (s *Server) handleAsyncAPISupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": asyncapi.GetSupportMatrix(),
		"notes":          asyncapi.GetSupportMatrixNotes(),
	})
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
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "asyncapi", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      appendImportDiagnosticReviewSections(buildAsyncAPIReviewSections(result), result.Unsupported),
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func (s *Server) handleEventHubsCaptureSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": eventhubscapture.GetSupportMatrix(),
		"notes":          eventhubscapture.GetSupportMatrixNotes(),
	})
}

// handleImportEventHubsCapture imports an Event Hubs Capture Avro file (binary)
// into a native eventhubs producer script. Accepts a multipart file upload or a
// raw binary body.
func (s *Server) handleImportEventHubsCapture(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)

	var (
		data      []byte
		name      string
		projectID string
		ok        bool
	)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		var filename string
		data, filename, ok = readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return
		}
		name = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		projectID = r.FormValue("project_id")
	} else {
		data, ok = readImportBody(w, r)
		if !ok {
			return
		}
		filename := r.URL.Query().Get("filename")
		name = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		projectID = r.URL.Query().Get("project_id")
	}

	maxEvents := 0
	if v := strings.TrimSpace(r.URL.Query().Get("max_events")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxEvents = n
		}
	}

	result, err := eventhubscapture.Convert(eventhubscapture.ConvertRequest{
		Name:      name,
		ProjectID: projectID,
		EventHub:  strings.TrimSpace(r.URL.Query().Get("event_hub")),
		Data:      data,
		MaxEvents: maxEvents,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(projectID)
	}
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:           result,
		Script:           result.Script,
		Warnings:         result.Warnings,
		Stats:            result.Stats,
		Summary:          result.Summary,
		ReviewSections:   buildEventHubsCaptureReviewSections(result),
		Parameterization: buildImportParameterization(result.Script, nil),
	}))
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
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": brokerconfig.GetSupportMatrix(),
		"notes":          brokerconfig.GetSupportMatrixNotes(),
	})
}

// handleImportBrokerConfig imports a standalone messaging broker-config artifact
// (IBM MQ MQSC script or JMS JNDI .properties file) into a JMS script skeleton
// seeded from the broker's queue/topic topology. Accepts a multipart file upload
// or a raw body; the format auto-detects but can be pinned via ?format=mqsc|jndi.
func (s *Server) handleImportBrokerConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)

	var (
		data      []byte
		name      string
		projectID string
		ok        bool
	)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		var filename string
		data, filename, ok = readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return
		}
		name = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		projectID = r.FormValue("project_id")
	} else {
		data, ok = readImportBody(w, r)
		if !ok {
			return
		}
		filename := r.URL.Query().Get("filename")
		name = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		projectID = r.URL.Query().Get("project_id")
	}

	maxDest := 0
	if v := strings.TrimSpace(r.URL.Query().Get("max_destinations")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxDest = n
		}
	}

	result, err := brokerconfig.Convert(brokerconfig.ConvertRequest{
		Name:            name,
		ProjectID:       projectID,
		Data:            data,
		Format:          strings.TrimSpace(r.URL.Query().Get("format")),
		MaxDestinations: maxDest,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(projectID)
	}
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:           result,
		Script:           result.Script,
		Warnings:         result.Warnings,
		Stats:            result.Stats,
		Summary:          result.Summary,
		ReviewSections:   buildBrokerConfigReviewSections(result),
		Parameterization: buildImportParameterization(result.Script, nil),
	}))
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
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		data, ok := readImportBody(w, r)
		if !ok {
			return nil, false
		}
		var req asyncapi.ImportRequest
		if err := json.Unmarshal(data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" && strings.Contains(string(data), `"asyncapi"`) {
			return &asyncAPIImportRequest{
				Data:      data,
				ProjectID: r.URL.Query().Get("project_id"),
				Name:      r.URL.Query().Get("name"),
				Opts:      asyncapi.ConvertOptions{ScriptName: r.URL.Query().Get("script_name")},
			}, true
		}
		return &asyncAPIImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, filename, ok := readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		return &asyncAPIImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(filename, filepath.Ext(filename)),
			Opts:      asyncapi.ConvertOptions{ScriptName: r.FormValue("script_name")},
		}, true
	}
	data, ok := readImportBody(w, r)
	if !ok {
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &asyncAPIImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts:      asyncapi.ConvertOptions{ScriptName: r.URL.Query().Get("script_name")},
	}, true
}

func readOpenAPIImportRequest(w http.ResponseWriter, r *http.Request) (*openAPIImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		data, ok := readImportBody(w, r)
		if !ok {
			return nil, false
		}
		var req openapi.ImportRequest
		if err := json.Unmarshal(data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" && strings.Contains(string(data), `"paths"`) {
			return &openAPIImportRequest{
				Data:      data,
				ProjectID: r.URL.Query().Get("project_id"),
				Name:      r.URL.Query().Get("name"),
				Opts: openapi.ConvertOptions{
					BaseURL:    r.URL.Query().Get("base_url"),
					ScriptName: r.URL.Query().Get("script_name"),
				},
			}, true
		}
		return &openAPIImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, filename, ok := readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		return &openAPIImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(filename, filepath.Ext(filename)),
			Opts: openapi.ConvertOptions{
				BaseURL:    r.FormValue("base_url"),
				ScriptName: r.FormValue("script_name"),
			},
		}, true
	}
	data, ok := readImportBody(w, r)
	if !ok {
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &openAPIImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts: openapi.ConvertOptions{
			BaseURL:    r.URL.Query().Get("base_url"),
			ScriptName: r.URL.Query().Get("script_name"),
		},
	}, true
}

func readRAMLImportRequest(w http.ResponseWriter, r *http.Request) (*ramlImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")

	var (
		data      []byte
		projectID string
		name      string
		origName  string
		opts      raml.ConvertOptions
	)

	switch {
	case strings.HasPrefix(contentType, "application/json"):
		body, ok := readImportBody(w, r)
		if !ok {
			return nil, false
		}
		var req raml.ImportRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		data = []byte(req.Content)
		projectID = req.ProjectID
		name = req.Name
		origName = req.Name
		opts = req.Options
	case strings.HasPrefix(contentType, "multipart/form-data"):
		fileData, filename, ok := readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		data = fileData
		projectID = r.FormValue("project_id")
		name = strings.TrimSuffix(filename, filepath.Ext(filename))
		origName = filename
		opts = raml.ConvertOptions{BaseURL: r.FormValue("base_url"), ScriptName: r.FormValue("script_name")}
	default:
		body, ok := readImportBody(w, r)
		if !ok {
			return nil, false
		}
		filename := r.URL.Query().Get("filename")
		data = body
		projectID = r.URL.Query().Get("project_id")
		name = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		origName = filename
		opts = raml.ConvertOptions{BaseURL: r.URL.Query().Get("base_url"), ScriptName: r.URL.Query().Get("script_name")}
	}

	importReq := &ramlImportRequest{Data: data, ProjectID: projectID, Name: name, Opts: opts}
	if isZipArchive(data) {
		mainContent, files, mainName, err := extractRAMLZip(data)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "RAML zip: "+err.Error())
			return nil, false
		}
		importReq.Data = mainContent
		importReq.Files = files
		// Replace a zip-derived/empty script name with the main RAML entry name.
		// Check the ORIGINAL filename (the stripped `name` no longer ends in .zip).
		if strings.TrimSpace(importReq.Name) == "" || strings.HasSuffix(strings.ToLower(origName), ".zip") {
			importReq.Name = strings.TrimSuffix(filepath.Base(mainName), filepath.Ext(mainName))
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
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, "", err
	}
	files := make(map[string][]byte)
	// Guard against zip-bomb amplification: cap total decompressed bytes with a
	// shared remaining budget across all entries, plus an entry-count cap.
	remaining := int64(webAPIImportMaxBodyBytes)
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if len(files) >= maxRAMLZipEntries {
			return nil, nil, "", fmt.Errorf("archive has too many entries (>%d)", maxRAMLZipEntries)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, "", err
		}
		content, err := io.ReadAll(io.LimitReader(rc, remaining+1))
		rc.Close()
		if err != nil {
			return nil, nil, "", err
		}
		if int64(len(content)) > remaining {
			return nil, nil, "", fmt.Errorf("archive decompressed size exceeds the %d-byte limit", webAPIImportMaxBodyBytes)
		}
		remaining -= int64(len(content))
		files[f.Name] = content
	}
	mainName := pickMainRAML(files)
	if mainName == "" {
		return nil, nil, "", fmt.Errorf("no .raml entry found in archive")
	}
	return files[mainName], files, mainName, nil
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
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req wsdl.ImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &wsdlImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, filename, ok := readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		return &wsdlImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(filename, filepath.Ext(filename)),
			Opts: wsdl.ConvertOptions{
				BaseURL:     r.FormValue("base_url"),
				ScriptName:  r.FormValue("script_name"),
				SOAPVersion: r.FormValue("soap_version"),
			},
		}, true
	}
	data, ok := readImportBody(w, r)
	if !ok {
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &wsdlImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts: wsdl.ConvertOptions{
			BaseURL:     r.URL.Query().Get("base_url"),
			ScriptName:  r.URL.Query().Get("script_name"),
			SOAPVersion: r.URL.Query().Get("soap_version"),
		},
	}, true
}

func readPostmanImportRequest(w http.ResponseWriter, r *http.Request) (*postmanImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		data, ok := readImportBody(w, r)
		if !ok {
			return nil, false
		}
		var req postman.ImportRequest
		if err := json.Unmarshal(data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" && strings.Contains(string(data), `"item"`) {
			return &postmanImportRequest{
				Data:      data,
				ProjectID: r.URL.Query().Get("project_id"),
				Name:      r.URL.Query().Get("name"),
				Opts:      postmanConvertOptionsFromValue(r.URL.Query().Get),
			}, true
		}
		return &postmanImportRequest{Data: []byte(req.Content), EnvironmentData: []byte(req.EnvironmentContent), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, filename, ok := readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		environmentData, ok := readPostmanEnvironmentMultipartFile(w, r)
		if !ok {
			return nil, false
		}
		return &postmanImportRequest{
			Data:            data,
			EnvironmentData: environmentData,
			ProjectID:       r.FormValue("project_id"),
			Name:            strings.TrimSuffix(filename, filepath.Ext(filename)),
			Opts:            postmanConvertOptionsFromValue(r.FormValue),
		}, true
	}
	data, ok := readImportBody(w, r)
	if !ok {
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &postmanImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts:      postmanConvertOptionsFromValue(r.URL.Query().Get),
	}, true
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

func readPostmanEnvironmentMultipartFile(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r == nil || r.MultipartForm == nil {
		return nil, true
	}
	for _, field := range []string{"environment", "environment_file"} {
		headers := r.MultipartForm.File[field]
		if len(headers) == 0 || headers[0] == nil {
			continue
		}
		file, err := headers[0].Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to open uploaded Postman environment: "+err.Error())
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded Postman environment: "+err.Error())
			return nil, false
		}
		return data, true
	}
	return nil, true
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
	if len(result.Summary.AuthSchemes) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "openapi_auth",
			Title:    "Authentication Review",
			Severity: "warning",
			Summary:  "Auth placeholders were inferred from the OpenAPI security schemes.",
			Items:    result.Summary.AuthSchemes,
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
