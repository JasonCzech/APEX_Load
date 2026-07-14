package api

// handlers_import_enterprise — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var enterpriseImportGate = make(chan struct{}, 2)

func (s *Server) handleImportEnterpriseArchive(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readEnterpriseArchiveImportRequest(w, r)
	if !ok {
		return
	}

	select {
	case enterpriseImportGate <- struct{}{}:
		defer func() { <-enterpriseImportGate }()
	case <-r.Context().Done():
		writeError(w, http.StatusRequestTimeout, "enterprise archive conversion canceled")
		return
	}
	// Run the conversion
	result, err := enterprisearchive.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	for _, script := range result.Scripts {
		if script != nil {
			script.ProjectID = normalizeProjectID(importReq.ProjectID)
		}
	}
	attachSalesforceReviewToEnterpriseArchiveResult(result, importReq)

	writeEnterpriseArchiveImportResponse(w, http.StatusOK, result, importReq.ReadinessPolicy)
}

func (s *Server) handleValidateEnterpriseArchiveImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readEnterpriseArchiveImportRequest(w, r)
	if !ok {
		return
	}

	result, err := enterprisearchive.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	for _, script := range result.Scripts {
		if script != nil {
			script.ProjectID = normalizeProjectID(importReq.ProjectID)
		}
	}
	attachSalesforceReviewToEnterpriseArchiveResult(result, importReq)

	writeEnterpriseArchiveImportResponse(w, http.StatusOK, result, importReq.ReadinessPolicy)
}

func attachSalesforceReviewToEnterpriseArchiveResult(result *enterprisearchive.ConvertResult, importReq *enterpriseArchiveImportRequest) {
	if result == nil || importReq == nil {
		return
	}
	scripts := result.Scripts
	if len(scripts) == 0 && result.Script != nil {
		scripts = []*dsl.Script{result.Script}
	}
	for _, script := range scripts {
		_ = attachMuleSoftReview(script, "enterprise_script_archive", nil)
		_ = attachAgentforceReview(script, "enterprise_script_archive", nil, false)
	}
	if !importReq.SalesforceNative && len(importReq.HARData) == 0 {
		return
	}
	reviewScript := result.Script
	if reviewScript == nil && len(scripts) > 0 {
		reviewScript = scripts[0]
	}
	if reviewScript == nil {
		return
	}
	review, err := salesforce.Analyze(reviewScript, salesforce.AnalyzeOptions{
		BuildNative: importReq.SalesforceNative,
		HARData:     importReq.HARData,
	})
	if err != nil {
		result.Warnings = append(result.Warnings, "Salesforce review failed: "+err.Error())
		return
	}
	if review == nil || (!review.Detected && !importReq.SalesforceNative) {
		return
	}
	for _, script := range scripts {
		preserved := ensureScriptImportPreserved(script, "enterprise_script_archive")
		preserved["salesforce_review"] = review
	}
}

func (s *Server) handleEnterpriseArchiveSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix":             enterprisearchive.GetRTSSupportMatrix(),
		"notes":                      enterprisearchive.GetRTSSupportMatrixNotes(),
		"tested_examples":            enterprisearchive.GetRTSSupportMatrixExamples(),
		"trueclient_script_types":    trueclient.ScriptTypeScopes(),
		"trueclient_step_matrix":     trueclient.StepScopes(),
		"trueclient_step_coverage":   trueclient.StepCoverage(),
		"web2_profile_capabilities":  trueclient.Web2ProfileCapabilityScopes(),
		"web2_fixture_contract":      trueclient.Web2FixtureContracts(),
		"protocol_support_matrix":    protocolBundleSupportMatrix(),
		"web_http_function_matrix":   enterprisearchive.WebHTTPFunctionCoverage(),
		"java_lr_function_matrix":    enterprisearchive.JavaLRFunctionCoverage(),
		"java_import_surface_matrix": enterprisearchive.JavaImportSurfaceCoverage(),
		"java_http_idiom_matrix":     enterprisearchive.JavaHTTPIdiomCoverage(),
		"java_jms_idiom_matrix":      enterprisearchive.JavaJMSIdiomCoverage(),
		"java_jdbc_idiom_matrix":     enterprisearchive.JavaJDBCIdiomCoverage(),
		"source_protocol_matrix": []map[string]any{
			{
				"canonical_source_protocol": "java_over_http",
				"display_name":              "Java Vuser HTTP",
				"apexload_protocol":         "http",
				"status":                    "supported",
				"promotion_gate":            "static Java web/HTTP idioms recognized by the importer",
				"notes":                     "Recorded Java-over-HTTP traffic targets native http_request actions, not the java_http serialized-object codec.",
			},
			{
				"canonical_source_protocol": "java_vuser",
				"display_name":              "Java Vuser JMS",
				"apexload_protocol":         "jms-solace / jms-ibmmq / jms-servicebus",
				"status":                    "supported",
				"promotion_gate":            "JMS provider, destination, and broker runtime configuration verified",
				"notes":                     "JMS idioms convert to native JMS DSL with mock enabled until real broker settings are supplied.",
			},
			{
				"canonical_source_protocol": "java_jdbc",
				"display_name":              "Java Vuser JDBC",
				"apexload_protocol":         "database",
				"status":                    "supported",
				"promotion_gate":            "database DSN secret and SQL intent verified",
				"notes":                     "Recoverable JDBC statements convert to native database actions; dynamic SQL remains review-only.",
			},
			{
				"canonical_source_protocol": "java_soap",
				"display_name":              "Java Vuser SOAP",
				"apexload_protocol":         "soap",
				"status":                    "partial",
				"promotion_gate":            "static endpoint and SOAP envelope recovered",
				"notes":                     "Recoverable Java SOAP calls promote to the native SOAP/HTTP runtime; dynamic clients remain named review.",
			},
			{
				"canonical_source_protocol": "java_jms_solace",
				"display_name":              "Java JMS/Solace",
				"apexload_protocol":         "jms-solace",
				"status":                    "supported",
				"promotion_gate":            "approved Solace JMS runtime settings or governed sidecar runner policy",
				"notes":                     "Legacy Java_protocols Solace projects route through the unified Java JMS lane and preserve XML-body templating.",
			},
		},
	})
}

func readEnterpriseArchiveImportRequest(w http.ResponseWriter, r *http.Request) (*enterpriseArchiveImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, enterpriseArchiveImportMaxBodyBytes)

	contentType := r.Header.Get("Content-Type")
	var data []byte
	var harData []byte

	if strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-zip-compressed") ||
		strings.HasPrefix(contentType, "application/octet-stream") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		data = body
	} else {
		if err := r.ParseMultipartForm(enterpriseArchiveImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form (max 100MB): "+err.Error())
			return nil, false
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' field in multipart form: "+err.Error())
			return nil, false
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		data = body
		if harFile, _, err := r.FormFile("har_file"); err == nil {
			defer harFile.Close()
			harBody, readErr := io.ReadAll(harFile)
			if readErr != nil {
				writeError(w, http.StatusBadRequest, "failed to read uploaded HAR file: "+readErr.Error())
				return nil, false
			}
			harData = harBody
		}
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}

	return &enterpriseArchiveImportRequest{
		Data:             data,
		HARData:          harData,
		ProjectID:        firstNonEmpty(r.FormValue("project_id"), r.URL.Query().Get("project_id")),
		SalesforceNative: parseBoolDefault(firstNonEmpty(r.FormValue("salesforce_native"), r.URL.Query().Get("salesforce_native")), false),
		Opts: enterprisearchive.ConvertOptions{
			ScriptName:           r.FormValue("name"),
			BaseURL:              r.FormValue("base_url"),
			IncludeAssertions:    r.FormValue("include_assertions") == "true",
			PreserveTransactions: parseBoolDefault(r.FormValue("preserve_transactions"), true),
			AllowJavaVuserRunner: parseBoolDefault(firstNonEmpty(r.FormValue("allow_java_vuser_runner"), r.URL.Query().Get("allow_java_vuser_runner")), false),
		},
		ReadinessPolicy: enterprisearchive.ParseRuntimeSettingMigrationPolicy(r.FormValue("migration_block_statuses")),
	}, true
}

func writeEnterpriseArchiveImportResponse(w http.ResponseWriter, status int, result *enterprisearchive.ConvertResult, readinessPolicy enterprisearchive.RuntimeSettingMigrationPolicy) {
	if result == nil {
		writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{}))
		return
	}
	unsupportedElements := uniqueSortedStrings(result.Stats.UnsupportedFuncs)
	warnings := enterpriseArchiveImportWarnings(result)
	summary := buildEnterpriseArchiveImportSummary(result, unsupportedElements, readinessPolicy)
	persistEnterpriseArchiveImportSummary(result, summary)
	reviewSections := buildEnterpriseArchiveImportReviewSections(warnings, result, unsupportedElements, summary)
	repairSuggestions := buildEnterpriseArchiveImportRepairSuggestions(result, unsupportedElements, summary)
	protocolCompat := buildEnterpriseArchiveProtocolCompatReport(result, unsupportedElements, summary)
	persistProtocolCompatReport(result.Script, "enterprise_script_archive", protocolCompat)
	for _, script := range result.Scripts {
		report := protocolCompat
		if len(result.Scripts) > 1 {
			// Each split script (HTTP / JDBC / JMS / SOAP) gets its own
			// step/data-source/runtime-parity counts instead of the aggregate
			// first-lane report being copied onto every script.
			report = protocolCompatReportForScript(protocolCompat, script)
		}
		persistProtocolCompatReport(script, "enterprise_script_archive", report)
	}

	scripts := result.Scripts
	if len(scripts) == 0 && result.Script != nil {
		scripts = []*dsl.Script{result.Script}
	}
	parameterization := buildImportParameterization(result.Script, nil)
	if len(scripts) > 1 {
		attachSourceReviewToScripts(scripts, "enterprise_script_archive", "", summary.SourceReview)
		parameterization = buildMultiScriptParameterization(scripts)
	}

	writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Scripts:             scripts,
		Script:              result.Script,
		Warnings:            warnings,
		Stats:               result.Stats,
		UnsupportedElements: unsupportedElements,
		ReviewSections:      reviewSections,
		RepairSuggestions:   repairSuggestions,
		Summary:             summary,
		DetectedProtocol:    result.DetectedProtocol,
		FidelityReport:      summary.FidelityReport,
		ReviewArtifact:      result.ReviewArtifact,
		SourceReview:        summary.SourceReview,
		TrueClient2Compat:   summary.TrueClient2CompatReport,
		ProtocolCompat:      protocolCompat,
		Parameterization:    parameterization,
	}))
}

func enterpriseArchiveImportWarnings(result *enterprisearchive.ConvertResult) []string {
	if result == nil {
		return nil
	}
	warnings := append([]string(nil), result.Warnings...)
	return uniqueSortedStrings(warnings)
}

func buildEnterpriseArchiveImportRepairSuggestions(result *enterprisearchive.ConvertResult, unsupportedElements []string, summary enterpriseArchiveImportSummary) []scriptImportRepairSuggestion {
	suggestions := make([]scriptImportRepairSuggestion, 0)
	if result == nil {
		return suggestions
	}

	for _, element := range unsupportedElements {
		normalized := strings.ToLower(element)
		description := "Review the preserved source construct and replace it with equivalent APEX Load DSL behavior before relying on this script."
		action := "Open the generated script in the visual editor, inspect preserved metadata, and either remove the unsupported construct or model it with supported actions."
		if strings.Contains(normalized, "evaluate javascript") {
			description = "This source JavaScript statement could not be decomposed into a supported browser action."
			action = "Replace the JavaScript with browser_click, browser_fill, browser_select, browser_wait, browser_swipe, or a supported assertion, then validate with browser replay."
			if enterpriseArchiveWarningsContain(result, "window.scrollto(") {
				description = "This source JavaScript scroll statement could not be decomposed safely into a supported browser action."
				action = "Replace the JavaScript with browser_swipe and any required browser_wait actions, then validate the resulting scroll behavior with browser replay."
			}
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "unsupported:" + stableReviewKey(element),
			Category:    "unsupported",
			Severity:    "blocker",
			Title:       "Replace unsupported " + element,
			Description: description,
			Action:      action,
		})
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		suggestions = append(suggestions, browserImportRepairSuggestions(result.Script)...)
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		suggestions = append(suggestions, trueClientWeb2RepairSuggestions(summary)...)
	}

	if result.DetectedProtocol != nil && result.DetectedProtocol.ImportMode != enterprisearchive.ImportModeExecutable {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "protocol:review-artifact",
			Category:    "protocol",
			Severity:    "blocker",
			Title:       "Resolve protocol import gate",
			Description: fmt.Sprintf("%s was classified as %s and is not ready to save as an executable APEX Load script.", firstNonEmpty(result.DetectedProtocol.DisplayName, result.DetectedProtocol.CanonicalSourceProtocol), result.DetectedProtocol.ImportMode),
			Action:      "Save the review artifact, collect the required protocol evidence, and rerun import after the protocol is confirmed or a runner bridge is available.",
		})
	}

	if len(summary.RuntimeFiles) > 0 || summary.MappedRuntimeFile != "" {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:review-preserved-settings",
			Category:    "runtime",
			Severity:    "info",
			Title:       "Review preserved runtime settings",
			Description: "The source archive included source runtime metadata that may not all execute natively in APEX Load yet.",
			Action:      "Compare the preserved runtime file and project settings against the generated APEX Load config before promoting the script.",
		})
	}

	if summary.MigrationReadiness != nil && summary.MigrationReadiness.Blocked {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:migration-readiness",
			Category:    "runtime",
			Severity:    "blocker",
			Title:       "Resolve blocking runtime settings before promotion",
			Description: "The selected migration block policy matched imported runtime settings that are not fully executable in APEX Load yet.",
			Action:      "Review the blocking runtime settings, adjust the generated APEX Load config where possible, or relax the migration block policy before saving the script.",
		})
	}

	if summary.SalesforceReview != nil && len(summary.SalesforceReview.Blockers) > 0 {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "salesforce:native-preview-blockers",
			Category:    "salesforce",
			Severity:    "blocker",
			Title:       "Resolve Salesforce native preview blockers",
			Description: "The exact replay script was imported, but the Salesforce native preview needs additional evidence or correlation review before promotion.",
			Action:      "Attach a HAR captured from the same source recording, review Salesforce transaction grouping and correlations, then rerun the native preview.",
		})
	}

	return suggestions
}

func enterpriseArchiveWarningsContain(result *enterprisearchive.ConvertResult, needle string) bool {
	if result == nil {
		return false
	}
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), needle) {
			return true
		}
	}
	return false
}

func buildEnterpriseArchiveImportReviewSections(warnings []string, result *enterprisearchive.ConvertResult, unsupportedElements []string, summary enterpriseArchiveImportSummary) []scriptImportReviewSection {
	sections := make([]scriptImportReviewSection, 0, 4)
	if result == nil {
		return sections
	}

	if result.DetectedProtocol != nil {
		items := []string{
			"Source protocol: " + firstNonEmpty(result.DetectedProtocol.DisplayName, result.DetectedProtocol.CanonicalSourceProtocol),
			"Import mode: " + string(result.DetectedProtocol.ImportMode),
			"Confidence: " + string(result.DetectedProtocol.Confidence),
		}
		if result.DetectedProtocol.APEXLoadProtocol != "" {
			items = append(items, "APEX Load protocol: "+result.DetectedProtocol.APEXLoadProtocol)
		}
		if result.DetectedProtocol.Bundle != "" {
			items = append(items, "Bundle: "+result.DetectedProtocol.Bundle)
		}
		for _, signal := range result.DetectedProtocol.Signals {
			items = append(items, "Signal: "+signal)
		}
		for _, conflict := range result.DetectedProtocol.Conflicts {
			items = append(items, "Conflict: "+conflict)
		}
		severity := "info"
		if result.DetectedProtocol.ImportMode != enterprisearchive.ImportModeExecutable || len(result.DetectedProtocol.Conflicts) > 0 {
			severity = "warning"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "detected_protocol",
			Title:    "Detected Source Protocol",
			Severity: severity,
			Summary:  "Protocol-aware routing classified this archive before conversion.",
			Items:    items,
		})
	}

	if result.ReviewArtifact != nil {
		items := []string{
			"Artifact ID: " + result.ReviewArtifact.ID,
			"Status: " + result.ReviewArtifact.Status,
		}
		for _, evidence := range result.ReviewArtifact.RequiredEvidence {
			items = append(items, "Required evidence: "+evidence)
		}
		for _, action := range result.ReviewArtifact.NextActions {
			items = append(items, "Next action: "+action)
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "review_artifact",
			Title:    "Protocol Review Artifact",
			Severity: "warning",
			Summary:  result.ReviewArtifact.Summary,
			Items:    items,
		})
	}

	if summary.FidelityReport != nil {
		severity := "info"
		if summary.FidelityReport.PromotionStatus == enterprisearchive.ImportPromotionBlocked || len(summary.FidelityReport.Blockers) > 0 {
			severity = "blocker"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "import_fidelity",
			Title:    "Import Fidelity",
			Severity: severity,
			Summary:  enterpriseArchiveFidelitySummary(summary.FidelityReport),
			Items:    enterpriseArchiveFidelityItems(summary.FidelityReport),
		})
	}

	if len(warnings) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "warnings",
			Title:    "Conversion Warnings",
			Severity: "warning",
			Summary:  "Review converter warnings before saving or replaying the imported draft.",
			Items:    warnings,
		})
	}

	if len(unsupportedElements) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "unsupported_elements",
			Title:    "Unsupported Source Elements",
			Severity: "blocker",
			Summary:  "These source constructs were preserved for review but were not converted into executable APEX Load actions.",
			Items:    unsupportedElements,
		})
	}

	preservedItems := make([]string, 0)
	if summary.ProjectFile != "" {
		preservedItems = append(preservedItems, "Project file: "+summary.ProjectFile)
	}
	if summary.MappedRuntimeFile != "" {
		preservedItems = append(preservedItems, "Mapped runtime file: "+summary.MappedRuntimeFile)
	}
	for _, parameterFile := range summary.ParameterFiles {
		preservedItems = append(preservedItems, "Parameter file: "+parameterFile)
	}
	for _, runtimeFile := range summary.RuntimeFiles {
		preservedItems = append(preservedItems, "Runtime file: "+runtimeFile)
	}
	for _, projectSection := range summary.ProjectSections {
		preservedItems = append(preservedItems, "Project section: "+projectSection)
	}
	if len(preservedItems) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "preserved_metadata",
			Title:    "Preserved Source Metadata",
			Severity: "info",
			Summary:  "Source project metadata was saved with the generated script for audit and repair workflows.",
			Items:    preservedItems,
		})
	}

	if len(summary.RuntimeSettingsParity) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_parity",
			Title:    "Runtime Settings Parity",
			Severity: runtimeSettingsParitySeverity(summary.RuntimeSettingsParity),
			Summary:  runtimeSettingsParitySummary(summary.RuntimeSettingsStatusCounts),
			Items:    runtimeSettingsParityItems(summary.RuntimeSettingsParity),
		})
	}

	if summary.MigrationReadiness != nil {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_migration_readiness",
			Title:    "Migration Readiness",
			Severity: migrationReadinessSeverity(summary.MigrationReadiness),
			Summary:  summary.MigrationReadiness.Summary,
			Items:    migrationReadinessItems(summary.MigrationReadiness),
		})
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		sections = append(sections, trueClientWeb2ReviewSections(summary)...)
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "browser_import_assumptions",
			Title:    "Browser Import Assumptions",
			Severity: "info",
			Summary:  "Mobile browser-profile archives map supported steps into browser DSL and require replay validation for selectors, waits, and device behavior; Web and Web 2.0 browser profiles are tracked as separate review profiles.",
			Items: []string{
				"Legacy desktop browser-profile archives remain out of scope; Web and Web 2.0 source profiles require guided review.",
				"Simple JavaScript fill/click statements are decomposed when possible; other JavaScript is preserved as review guidance.",
				"Selector fidelity should be checked with browser replay before production use.",
			},
		})
	}

	if result.DetectedProtocol != nil && (result.DetectedProtocol.CanonicalSourceProtocol == "oracle_web" || result.DetectedProtocol.CanonicalSourceProtocol == "siebel_web") {
		sections = append(sections, scriptImportReviewSection{
			Key:      "oracle_ebusiness_review",
			Title:    "Oracle E-Business Review Pack",
			Severity: "warning",
			Summary:  "Oracle-Web and Siebel-Web imports execute through HTTP but require enterprise correlation and session-state review before promotion.",
			Items: []string{
				"Review ICX/OA Framework or Siebel session identifiers and correlation candidates.",
				"Validate login, navigation, and form-post replay evidence against the target environment.",
				"Confirm Oracle E-Business runtime settings, data fit, and environment-specific tokens before saving reusable assets.",
			},
		})
	}

	if summary.SalesforceReview != nil {
		severity := "info"
		if len(summary.SalesforceReview.Blockers) > 0 {
			severity = "blocker"
		} else if summary.SalesforceReview.HARMatchCoverage.HARProvided && summary.SalesforceReview.HARMatchCoverage.CoveragePct < 80 {
			severity = "warning"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "salesforce_review",
			Title:    "Salesforce Native Review",
			Severity: severity,
			Summary:  salesforceReviewSummary(summary.SalesforceReview),
			Items:    salesforceReviewItems(summary.SalesforceReview),
		})
	}
	if summary.MuleSoftReview != nil {
		sections = append(sections, mulesoftReviewSection(summary.MuleSoftReview))
	}
	if summary.AgentforceReview != nil {
		sections = append(sections, agentforceReviewSection(summary.AgentforceReview, nil))
	}

	return sections
}

func enterpriseArchiveFidelitySummary(report *enterprisearchive.ImportFidelityReport) string {
	if report == nil {
		return ""
	}
	status := firstNonEmpty(report.PromotionStatus, "unknown")
	return fmt.Sprintf("Promotion status: %s; converted %d of %d detected source function(s), with %d dropped body/bodies and %d secret-gated payload(s).", status, report.ConvertedFunctions, report.ExpectedFunctions, report.DroppedBodies, report.SecretGated)
}

func enterpriseArchiveFidelityItems(report *enterprisearchive.ImportFidelityReport) []string {
	if report == nil {
		return nil
	}
	items := []string{
		fmt.Sprintf("Source files inventoried: %d", len(report.SourceFiles)),
		fmt.Sprintf("Converted actions: %d", report.ConvertedActions),
		fmt.Sprintf("Dropped request bodies: %d", report.DroppedBodies),
		fmt.Sprintf("Secret-gated payloads: %d", report.SecretGated),
	}
	if len(report.ParameterParity) > 0 {
		items = append(items, fmt.Sprintf("Parameter parity: expected=%d converted=%d data_sources=%d", report.ParameterParity["expected_parameters"], report.ParameterParity["converted_parameters"], report.ParameterParity["converted_data_sources"]))
	}
	runtimeStatuses := make([]string, 0, len(report.RuntimeParity))
	for status := range report.RuntimeParity {
		runtimeStatuses = append(runtimeStatuses, status)
	}
	sort.Strings(runtimeStatuses)
	for _, status := range runtimeStatuses {
		count := report.RuntimeParity[status]
		items = append(items, fmt.Sprintf("Runtime parity %s: %d", status, count))
	}
	for _, blocker := range report.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, warning := range report.Warnings {
		items = append(items, "Warning: "+warning)
	}
	return items
}

func salesforceReviewSummary(review *salesforce.Review) string {
	if review == nil {
		return ""
	}
	if !review.Detected {
		return "Salesforce native review ran but did not detect Salesforce traffic."
	}
	coverage := review.HARMatchCoverage
	return fmt.Sprintf("Detected Salesforce traffic across %d domain(s); HAR matched %d of %d imported request(s) (%.1f%%).", len(review.Domains), coverage.MatchedRequests, coverage.ImportedRequests, coverage.CoveragePct)
}

func salesforceReviewItems(review *salesforce.Review) []string {
	if review == nil {
		return nil
	}
	items := make([]string, 0)
	for _, domain := range review.Domains {
		items = append(items, fmt.Sprintf("Domain: %s (%s, %d request(s))", domain.Host, domain.Kind, domain.Count))
	}
	if len(review.AuraEndpoints) > 0 {
		items = append(items, fmt.Sprintf("Aura endpoints detected: %d", len(review.AuraEndpoints)))
	}
	if len(review.Transactions) > 0 {
		items = append(items, fmt.Sprintf("Transaction candidates: %d", len(review.Transactions)))
		flowKinds := map[string]bool{}
		for _, transaction := range review.Transactions {
			for _, flowKind := range transaction.FlowKinds {
				if strings.TrimSpace(flowKind) != "" {
					flowKinds[flowKind] = true
				}
			}
		}
		if len(flowKinds) > 0 {
			ordered := make([]string, 0, len(flowKinds))
			for flowKind := range flowKinds {
				ordered = append(ordered, flowKind)
			}
			sort.Strings(ordered)
			items = append(items, "Salesforce flow kinds: "+strings.Join(ordered, ", "))
		}
	}
	if len(review.Parameterizations) > 0 {
		keySet := map[string]bool{}
		for _, param := range review.Parameterizations {
			if strings.TrimSpace(param.ParamKey) != "" {
				keySet[param.ParamKey] = true
			}
		}
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items = append(items, fmt.Sprintf("Aura message parameterization candidates: %d (keys: %s)", len(review.Parameterizations), strings.Join(keys, ", ")))
	}
	if review.CorrelationConfidence != "" {
		items = append(items, "Correlation confidence: "+review.CorrelationConfidence)
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	return items
}

func buildEnterpriseArchiveImportSummary(result *enterprisearchive.ConvertResult, unsupportedElements []string, readinessPolicy enterprisearchive.RuntimeSettingMigrationPolicy) enterpriseArchiveImportSummary {
	summary := enterpriseArchiveImportSummary{
		UnsupportedFunctionCount: len(unsupportedElements),
	}
	if result != nil && result.DetectedProtocol != nil {
		summary.DetectedProtocol = result.DetectedProtocol
		summary.ImportMode = string(result.DetectedProtocol.ImportMode)
	}
	if result != nil && result.ReviewArtifact != nil {
		summary.ReviewArtifact = result.ReviewArtifact
	}
	if result != nil && result.SourceReview != nil {
		summary.SourceReview = result.SourceReview
	}
	if result != nil && result.FidelityReport != nil {
		summary.FidelityReport = result.FidelityReport
	}

	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return summary
	}

	preserved := result.Script.Metadata.Import.Preserved
	if preserved == nil {
		return summary
	}

	summary.ProjectFile = stringValue(preserved["project_file"])
	summary.MappedRuntimeFile = stringValue(preserved["mapped_runtime_file"])
	summary.ProjectSections = sortedRecordKeys(preserved["project_settings"])
	summary.RuntimeFiles = sortedRecordKeys(preserved["runtime_files"])
	summary.ParameterFiles = stringSliceValue(preserved["parameter_files"])
	summary.RuntimeSettingsParity = runtimeSettingsParityValue(preserved["runtime_settings_parity"])
	summary.RuntimeSettingsStatusCounts = intMapValue(preserved["runtime_settings_status_counts"])
	summary.MigrationReadiness = enterpriseArchiveMigrationReadinessValue(preserved["runtime_settings_migration_readiness"])
	summary.TrueClientInspection = trueClientArchiveInspectionValue(preserved["trueclient_archive_inspection"])
	summary.SourceScriptType = stringValue(preserved["source_script_type"])
	summary.SourceProfile = stringValue(preserved["source_profile"])
	if summary.SourceReview == nil {
		summary.SourceReview = sourceReviewValue(preserved["source_review"])
	}
	if summary.MigrationReadiness == nil && len(summary.RuntimeSettingsParity) > 0 {
		summary.MigrationReadiness = enterprisearchive.BuildRTSMigrationReadiness(summary.RuntimeSettingsParity, readinessPolicy)
	}
	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		summary.Web2MigrationReadiness = buildWeb2MigrationReadinessReport(result, summary)
		summary.TrueClient2CompatReport = buildTrueClient2CompatReport(result, summary)
	}
	summary.SalesforceReview = salesforceReviewValue(preserved["salesforce_review"])
	summary.AgentforceReview = agentforceReviewValue(preserved["agentforce_review"])
	summary.MuleSoftReview = mulesoftReviewValue(preserved["mulesoft_review"])
	if summary.FidelityReport == nil {
		summary.FidelityReport = fidelityReportValue(preserved["fidelity_report"])
	}

	return summary
}

func persistEnterpriseArchiveImportSummary(result *enterprisearchive.ConvertResult, summary enterpriseArchiveImportSummary) {
	if result == nil {
		return
	}
	scripts := result.Scripts
	if len(scripts) == 0 && result.Script != nil {
		scripts = []*dsl.Script{result.Script}
	}
	for _, script := range scripts {
		if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
			continue
		}
		if script.Metadata.Import.Preserved == nil {
			script.Metadata.Import.Preserved = make(map[string]any)
		}
		if summary.MigrationReadiness != nil {
			script.Metadata.Import.Preserved["runtime_settings_migration_readiness"] = summary.MigrationReadiness
		}
		if summary.Web2MigrationReadiness != nil {
			script.Metadata.Import.Preserved["web2_migration_readiness"] = summary.Web2MigrationReadiness
		}
		if summary.TrueClient2CompatReport != nil {
			script.Metadata.Import.Preserved["trueclient2_compat_report"] = summary.TrueClient2CompatReport
		}
		if summary.SalesforceReview != nil {
			script.Metadata.Import.Preserved["salesforce_review"] = summary.SalesforceReview
		}
		if summary.AgentforceReview != nil {
			script.Metadata.Import.Preserved["agentforce_review"] = summary.AgentforceReview
		}
		if summary.MuleSoftReview != nil {
			script.Metadata.Import.Preserved["mulesoft_review"] = summary.MuleSoftReview
		}
		if summary.SourceReview != nil {
			script.Metadata.Import.Preserved["source_review"] = summary.SourceReview
		}
		if summary.FidelityReport != nil {
			script.Metadata.Import.Preserved["fidelity_report"] = summary.FidelityReport
		}
	}
}

// protocolCompatReportForScript returns a copy of the aggregate report with the
// per-script-shaped fields (step / data-source / runtime-parity counts) recomputed
// from the given split script, so a JDBC/JMS script does not carry the first
// (HTTP) lane's counts in a multi-script import.
func protocolCompatReportForScript(base *protocolCompatReport, script *dsl.Script) *protocolCompatReport {
	if base == nil || script == nil {
		return base
	}
	clone := *base
	clone.MappedSteps = len(script.Init) + len(script.Actions) + len(script.Teardown)
	clone.DataSources = protocolDataSourceCount(script)
	if parity := protocolRuntimeParityFromScript(script); len(parity) > 0 {
		clone.RuntimeParity = parity
	}
	return &clone
}

func buildEnterpriseArchiveProtocolCompatReport(result *enterprisearchive.ConvertResult, unsupported []string, summary enterpriseArchiveImportSummary) *protocolCompatReport {
	if result == nil {
		return nil
	}
	importMode := "executable"
	canonical := "web_http_html"
	if result.DetectedProtocol != nil {
		importMode = string(result.DetectedProtocol.ImportMode)
		canonical = result.DetectedProtocol.CanonicalSourceProtocol
	}
	status := protocolPromotionStatus(importMode, len(unsupported), len(result.Warnings))
	nextActions := protocolNextActions(status, enterpriseArchiveProtocolValidationAction(canonical))
	if result.ReviewArtifact != nil && len(result.ReviewArtifact.NextActions) > 0 {
		nextActions = result.ReviewArtifact.NextActions
	}
	runtimeParity := summary.RuntimeSettingsStatusCounts
	if len(runtimeParity) == 0 {
		runtimeParity = protocolRuntimeParityFromScript(result.Script)
	}
	unsupportedFragments := enterpriseArchiveUnsupportedFragments(result, unsupported)
	return &protocolCompatReport{
		SourceFormat:         enterpriseArchiveProtocolSourceFormat(canonical),
		CanonicalProtocol:    canonical,
		ImportMode:           importMode,
		MappedSteps:          result.Stats.ConvertedActions,
		ReviewGatedSteps:     len(unsupported) + result.Stats.ReviewOnlyFunctions + result.Stats.CustomCallFunctions + result.Stats.ExternalRequiredFunctions,
		UnsupportedFragments: unsupportedFragments,
		RuntimeParity:        runtimeParity,
		DataSources:          protocolDataSourceCount(result.Script),
		CorrelationCoverage:  protocolCorrelationCoverage(result.Stats.Extractors, result.Stats.RawRequestActions),
		PromotionStatus:      status,
		NextActions:          nextActions,
	}
}

func enterpriseArchiveUnsupportedFragments(result *enterprisearchive.ConvertResult, unsupported []string) []string {
	fragments := append([]string(nil), unsupported...)
	if result != nil && result.ReviewArtifact != nil && len(result.ReviewArtifact.PreservedFunctions) > 0 {
		fragments = append(fragments, result.ReviewArtifact.PreservedFunctions...)
	}
	return uniqueSortedStrings(fragments)
}

func enterpriseArchiveProtocolSourceFormat(canonical string) string {
	switch canonical {
	case "devweb":
		return "DevWeb"
	case "jmeter":
		return "JMeter JMX"
	default:
		return "Enterprise Script Archive"
	}
}

func enterpriseArchiveProtocolValidationAction(canonical string) string {
	switch canonical {
	case "devweb":
		return "Validate converted DevWeb replay and review JavaScript/helper-module blockers before promotion."
	case "jmeter":
		return "Validate converted JMeter thread groups and scenario draft before promotion."
	default:
		return "Validate converted enterprise script archive replay and correlation behavior before promotion."
	}
}
