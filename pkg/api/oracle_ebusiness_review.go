package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

type oracleEBusinessReviewRequest struct {
	ProjectID  string                         `json:"project_id,omitempty"`
	Name       string                         `json:"name,omitempty"`
	Package    string                         `json:"package,omitempty"`
	SourceText string                         `json:"source_text,omitempty"`
	Files      []oracleEBusinessReviewFile    `json:"files,omitempty"`
	Script     *dsl.Script                    `json:"script,omitempty"`
	Metadata   map[string]any                 `json:"metadata,omitempty"`
	Options    oracleEBusinessReviewOptions   `json:"options,omitempty"`
	Templates  []oracleEBusinessTemplateInput `json:"templates,omitempty"`
}

type oracleEBusinessReviewOptions struct {
	RequireReplayEvidence bool `json:"require_replay_evidence,omitempty"`
	BlockNCAExecution     bool `json:"block_nca_execution,omitempty"`
}

type oracleEBusinessReviewFile struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type oracleEBusinessTemplateInput struct {
	ID string `json:"id,omitempty"`
}

type oracleEBusinessReviewResponse struct {
	Package                string                                `json:"package"`
	Status                 string                                `json:"status"`
	ProjectID              string                                `json:"project_id,omitempty"`
	Name                   string                                `json:"name,omitempty"`
	SourceSummary          oracleEBusinessSourceSummary          `json:"source_summary"`
	CorrelationCandidates  []oracleEBusinessCorrelationCandidate `json:"correlation_candidates"`
	DataSourceFindings     []oracleEBusinessDataSourceFinding    `json:"data_source_findings"`
	Findings               []oracleEBusinessFinding              `json:"findings"`
	ReviewSections         []scriptImportReviewSection           `json:"review_sections"`
	RepairSuggestions      []scriptImportRepairSuggestion        `json:"repair_suggestions"`
	ScenarioTemplates      []oracleEBusinessScenarioTemplate     `json:"scenario_templates"`
	ExecutionPosture       oracleEBusinessExecutionPosture       `json:"execution_posture"`
	NextActions            []string                              `json:"next_actions"`
	SupportMatrix          []ProtocolBundleSupportEntry          `json:"support_matrix"`
	DetectedPackageSignals map[string]int                        `json:"detected_package_signals"`
	RequiredEvidence       []string                              `json:"required_evidence"`
	Metadata               map[string]any                        `json:"metadata,omitempty"`
}

type oracleEBusinessSourceSummary struct {
	Files          int      `json:"files"`
	Bytes          int      `json:"bytes"`
	Actions        int      `json:"actions,omitempty"`
	DetectedHosts  []string `json:"detected_hosts,omitempty"`
	SourceProfiles []string `json:"source_profiles,omitempty"`
}

type oracleEBusinessCorrelationCandidate struct {
	ID                string   `json:"id"`
	Package           string   `json:"package"`
	Name              string   `json:"name"`
	Token             string   `json:"token"`
	Source            string   `json:"source"`
	ExtractorType     string   `json:"extractor_type"`
	Count             int      `json:"count"`
	Evidence          []string `json:"evidence"`
	RecommendedAction string   `json:"recommended_action"`
	Blocking          bool     `json:"blocking"`
}

type oracleEBusinessDataSourceFinding struct {
	ID                string   `json:"id"`
	Field             string   `json:"field"`
	Status            string   `json:"status"`
	Count             int      `json:"count"`
	Evidence          []string `json:"evidence"`
	RecommendedAction string   `json:"recommended_action"`
}

type oracleEBusinessFinding struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Category string   `json:"category"`
	Summary  string   `json:"summary"`
	Detail   string   `json:"detail,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

type oracleEBusinessExecutionPosture struct {
	Posture            string   `json:"posture"`
	RunnerRequirement  string   `json:"runner_requirement"`
	CanExecuteNatively bool     `json:"can_execute_natively"`
	BlocksExecution    bool     `json:"blocks_execution"`
	Reasons            []string `json:"reasons,omitempty"`
}

type oracleEBusinessScenarioTemplate struct {
	ID          string   `json:"id"`
	Package     string   `json:"package"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Flow        []string `json:"flow"`
	Parameters  []string `json:"parameters,omitempty"`
	Evidence    []string `json:"evidence,omitempty"`
}

type oracleEBusinessSupportResponse struct {
	SupportMatrix     []ProtocolBundleSupportEntry      `json:"support_matrix"`
	CorrelationPacks  []oracleEBusinessCorrelationPack  `json:"correlation_packs"`
	ScenarioTemplates []oracleEBusinessScenarioTemplate `json:"scenario_templates"`
	NCABoundary       oracleEBusinessExecutionPosture   `json:"nca_boundary"`
	Notes             []string                          `json:"notes"`
}

type oracleEBusinessCorrelationPack struct {
	Package     string   `json:"package"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	Tokens      []string `json:"tokens"`
	Actions     []string `json:"actions"`
}

type packagedAppTokenSpec struct {
	Package           string
	Token             string
	Source            string
	ExtractorType     string
	RecommendedAction string
	Blocking          bool
}

func (s *Server) handleOracleEBusinessSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, oracleEBusinessSupportResponse{
		SupportMatrix:     oracleEBusinessSupportEntries(),
		CorrelationPacks:  oracleEBusinessCorrelationPacks(),
		ScenarioTemplates: oracleEBusinessScenarioTemplates(),
		NCABoundary:       oracleEBusinessNCAPosture(),
		Notes: []string{
			"Oracle-Web and Siebel-Web are HTTP-backed package reviews; replay, correlation repair, data-source fit, and approval evidence are required before promotion.",
			"Oracle NCA is intentionally review-only until an Oracle Forms/NCA client runner is explicitly approved for a project.",
			"DevWeb, JMeter, and Web-HTTP/HTML retain their existing import/runtime paths and add Oracle E-Business caveats through this review layer.",
		},
	})
}

func (s *Server) handleReviewOracleEBusinessImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req oracleEBusinessReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.SourceText) == "" && len(req.Files) == 0 && req.Script == nil {
		writeError(w, http.StatusBadRequest, "oracle e-business review requires source_text, files, or script")
		return
	}
	writeJSON(w, http.StatusOK, buildOracleEBusinessReview(req))
}

func buildOracleEBusinessReview(req oracleEBusinessReviewRequest) oracleEBusinessReviewResponse {
	sourceText, summary := oracleEBusinessReviewText(req)
	signals := oracleEBusinessPackageSignals(sourceText)
	packageName := normalizeOracleEBusinessPackage(req.Package)
	if packageName == "" {
		packageName = classifyOracleEBusinessPackage(signals)
	}
	if packageName == "" {
		packageName = "web_http_html"
	}

	candidates := oracleEBusinessCorrelationCandidates(packageName, sourceText)
	dataFindings := oracleEBusinessDataSourceFindings(sourceText)
	findings := oracleEBusinessFindings(packageName, candidates, dataFindings, signals, summary)
	posture := oracleEBusinessExecutionPostureFor(packageName)
	status := oracleEBusinessReviewStatus(packageName, candidates, findings, posture, summary)
	sections := oracleEBusinessReviewSections(packageName, candidates, dataFindings, findings, posture)
	repair := oracleEBusinessRepairSuggestions(candidates, dataFindings, posture)

	return oracleEBusinessReviewResponse{
		Package:                packageName,
		Status:                 status,
		ProjectID:              normalizeProjectID(req.ProjectID),
		Name:                   strings.TrimSpace(req.Name),
		SourceSummary:          summary,
		CorrelationCandidates:  candidates,
		DataSourceFindings:     dataFindings,
		Findings:               findings,
		ReviewSections:         sections,
		RepairSuggestions:      repair,
		ScenarioTemplates:      templatesForOracleEBusinessPackage(packageName),
		ExecutionPosture:       posture,
		NextActions:            oracleEBusinessNextActions(status, packageName, posture),
		SupportMatrix:          oracleEBusinessSupportEntries(),
		DetectedPackageSignals: signals,
		RequiredEvidence:       oracleEBusinessRequiredEvidence(packageName, posture),
		Metadata:               req.Metadata,
	}
}

func oracleEBusinessReviewText(req oracleEBusinessReviewRequest) (string, oracleEBusinessSourceSummary) {
	parts := []string{req.SourceText}
	summary := oracleEBusinessSourceSummary{Files: len(req.Files)}
	for _, file := range req.Files {
		if strings.TrimSpace(file.Name) != "" {
			parts = append(parts, "FILE "+file.Name)
		}
		parts = append(parts, file.Content)
		summary.Bytes += len(file.Content)
	}
	if req.Script != nil {
		summary.Actions = len(req.Script.Init) + len(req.Script.Actions) + len(req.Script.Teardown)
		if req.Script.Metadata != nil && req.Script.Metadata.Import != nil {
			if req.Script.Metadata.Import.Source != "" {
				summary.SourceProfiles = append(summary.SourceProfiles, req.Script.Metadata.Import.Source)
			}
			if req.Script.Metadata.Import.SourceScriptName != "" {
				summary.SourceProfiles = append(summary.SourceProfiles, req.Script.Metadata.Import.SourceScriptName)
			}
		}
		parts = append(parts, req.Script.Name, req.Script.Protocol, req.Script.Config.BaseURL)
		appendActions := func(actions []dsl.Action) {
			for _, action := range actions {
				parts = append(parts, action.Name, action.Type, action.Target, action.Value, action.Body)
				for key, value := range action.Headers {
					parts = append(parts, key, value)
				}
				if len(action.Params) > 0 {
					if encoded, err := json.Marshal(action.Params); err == nil {
						parts = append(parts, string(encoded))
					}
				}
			}
		}
		appendActions(req.Script.Init)
		appendActions(req.Script.Actions)
		appendActions(req.Script.Teardown)
	}
	if summary.Bytes == 0 {
		for _, part := range parts {
			summary.Bytes += len(part)
		}
	}
	summary.DetectedHosts = detectedHosts(strings.Join(parts, "\n"))
	sort.Strings(summary.SourceProfiles)
	return strings.Join(parts, "\n"), summary
}

func oracleEBusinessPackageSignals(text string) map[string]int {
	return map[string]int{
		"oracle_web": countAnyToken(text, []string{"ICX_SESSION", "OA_HTML", "OAFunc", "oracle.apps", "p_instance", "fwk", "dbc", "_FORM"}),
		"siebel_web": countAnyToken(text, []string{"SWEC", "SWEExtSource", "SWECount", "SWEView", "SWEApplet", "SWEFrame", "SWEForm", "SWERowId", "SWEUserName", "SWEPassword"}),
		"oracle_nca": countAnyToken(text, []string{"nca_connect_server", "nca_set_window", "formsweb.cfg", "oracle.forms", "frmservlet", "webutil", "NCA"}),
		"devweb":     countAnyToken(text, []string{"load.initialize", "web.request", "load.action"}),
		"jmeter":     countAnyToken(text, []string{"jmeterTestPlan", "HTTPSamplerProxy", "ThreadGroup"}),
		"gatling":    countAnyToken(text, []string{"io.gatling", "scenario(", "exec(http("}),
	}
}

func classifyOracleEBusinessPackage(signals map[string]int) string {
	order := []string{"oracle_nca", "siebel_web", "oracle_web", "devweb", "jmeter", "gatling"}
	best := ""
	bestCount := 0
	for _, key := range order {
		if signals[key] > bestCount {
			best = key
			bestCount = signals[key]
		}
	}
	return best
}

func normalizeOracleEBusinessPackage(value string) string {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "-", "_"))) {
	case "oracle_web", "oracleweb", "oracle":
		return "oracle_web"
	case "siebel_web", "siebelweb", "siebel":
		return "siebel_web"
	case "oracle_nca", "nca":
		return "oracle_nca"
	case "web_http_html", "web", "http", "web_http":
		return "web_http_html"
	case "devweb":
		return "devweb"
	case "jmeter":
		return "jmeter"
	case "gatling":
		return "gatling"
	default:
		return ""
	}
}

func oracleEBusinessCorrelationCandidates(packageName, text string) []oracleEBusinessCorrelationCandidate {
	specs := oracleEBusinessTokenSpecs()
	candidates := make([]oracleEBusinessCorrelationCandidate, 0)
	for _, spec := range specs {
		if spec.Package != packageName && !(packageName == "web_http_html" && (spec.Package == "oracle_web" || spec.Package == "siebel_web")) {
			continue
		}
		count := countToken(text, spec.Token)
		if count == 0 {
			continue
		}
		candidates = append(candidates, oracleEBusinessCorrelationCandidate{
			ID:                strings.ReplaceAll(spec.Package+"_"+strings.ToLower(spec.Token), " ", "_"),
			Package:           spec.Package,
			Name:              displayTokenName(spec.Token),
			Token:             spec.Token,
			Source:            spec.Source,
			ExtractorType:     spec.ExtractorType,
			Count:             count,
			Evidence:          tokenEvidence(text, spec.Token, 3),
			RecommendedAction: spec.RecommendedAction,
			Blocking:          spec.Blocking,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Package == candidates[j].Package {
			return candidates[i].Token < candidates[j].Token
		}
		return candidates[i].Package < candidates[j].Package
	})
	return candidates
}

func oracleEBusinessTokenSpecs() []packagedAppTokenSpec {
	return []packagedAppTokenSpec{
		{Package: "oracle_web", Token: "ICX_SESSION", Source: "cookie", ExtractorType: "cookie", RecommendedAction: "Capture ICX_SESSION from Set-Cookie and bind dependent requests to the extracted value."},
		{Package: "oracle_web", Token: "JSESSIONID", Source: "cookie", ExtractorType: "cookie", RecommendedAction: "Capture JSESSIONID from Set-Cookie before Oracle HTML framework requests."},
		{Package: "oracle_web", Token: "_FORM", Source: "body", ExtractorType: "regex", RecommendedAction: "Extract Oracle form state values from the prior HTML response before submit actions."},
		{Package: "oracle_web", Token: "OAFunc", Source: "query", ExtractorType: "query", RecommendedAction: "Parameterize dynamic OA Framework function identifiers and verify responsibility-specific values."},
		{Package: "oracle_web", Token: "p_instance", Source: "query", ExtractorType: "query", RecommendedAction: "Correlate p_instance values from navigation responses before replay."},
		{Package: "oracle_web", Token: "OA_HTML", Source: "url", ExtractorType: "url", RecommendedAction: "Review Oracle OA_HTML paths and preserve base URL/environment parameters."},
		{Package: "siebel_web", Token: "SWEC", Source: "query", ExtractorType: "query", RecommendedAction: "Correlate Siebel command identifiers across navigation requests."},
		{Package: "siebel_web", Token: "SWEExtSource", Source: "query", ExtractorType: "query", RecommendedAction: "Parameterize dynamic SWEExtSource values generated by the Siebel session."},
		{Package: "siebel_web", Token: "SWECount", Source: "query", ExtractorType: "query", RecommendedAction: "Treat SWECount as dynamic navigation state and validate replay sequence."},
		{Package: "siebel_web", Token: "SWEView", Source: "query", ExtractorType: "query", RecommendedAction: "Keep view names explicit and pair them with row/app state correlations."},
		{Package: "siebel_web", Token: "SWEApplet", Source: "query", ExtractorType: "query", RecommendedAction: "Review applet-specific request flows before promotion."},
		{Package: "siebel_web", Token: "SWERowId", Source: "query", ExtractorType: "regex", RecommendedAction: "Correlate dynamic row IDs or map them to data-source rows."},
		{Package: "siebel_web", Token: "SWEUserName", Source: "body", ExtractorType: "parameter", RecommendedAction: "Bind login identity to approved test-user data sources."},
		{Package: "siebel_web", Token: "SWEPassword", Source: "body", ExtractorType: "secret", RecommendedAction: "Move password values to secret-backed parameters before replay."},
		{Package: "oracle_nca", Token: "nca_connect_server", Source: "source", ExtractorType: "review_only", RecommendedAction: "Preserve NCA connection details and route to Oracle Forms runner feasibility review.", Blocking: true},
		{Package: "oracle_nca", Token: "nca_set_window", Source: "source", ExtractorType: "review_only", RecommendedAction: "Preserve NCA window/object references; native HTTP execution is blocked.", Blocking: true},
		{Package: "oracle_nca", Token: "frmservlet", Source: "url", ExtractorType: "review_only", RecommendedAction: "Classify Oracle Forms traffic as NCA/client-runner candidate unless converted to supported web endpoints.", Blocking: true},
		{Package: "oracle_nca", Token: "formsweb.cfg", Source: "source", ExtractorType: "review_only", RecommendedAction: "Capture Forms client prerequisites for external-runner approval.", Blocking: true},
	}
}

func oracleEBusinessDataSourceFindings(text string) []oracleEBusinessDataSourceFinding {
	fields := []struct {
		field  string
		token  string
		action string
	}{
		{"username", "username", "Move user identities to a data source with project-approved credential scope."},
		{"password", "password", "Move passwords to secret-backed parameters; do not promote literal credentials."},
		{"responsibility", "responsibility", "Represent Oracle responsibility selection as data when the scenario spans roles."},
		{"search_key", "search", "Add search keys to a reusable data source and validate row availability before run."},
		{"row_id", "rowid", "Correlate row identifiers or bind them to stable fixture data before replay."},
	}
	out := make([]oracleEBusinessDataSourceFinding, 0)
	for _, field := range fields {
		count := countToken(text, field.token)
		if count == 0 {
			continue
		}
		out = append(out, oracleEBusinessDataSourceFinding{
			ID:                "data_" + field.field,
			Field:             field.field,
			Status:            "needs_review",
			Count:             count,
			Evidence:          tokenEvidence(text, field.token, 2),
			RecommendedAction: field.action,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

func oracleEBusinessFindings(packageName string, candidates []oracleEBusinessCorrelationCandidate, dataFindings []oracleEBusinessDataSourceFinding, signals map[string]int, summary oracleEBusinessSourceSummary) []oracleEBusinessFinding {
	findings := []oracleEBusinessFinding{{
		ID:       "package_profile",
		Severity: "info",
		Category: "classification",
		Summary:  fmt.Sprintf("Classified source as %s.", strings.ReplaceAll(packageName, "_", " ")),
	}}
	if summary.Bytes == 0 {
		findings = append(findings, oracleEBusinessFinding{ID: "empty_source", Severity: "error", Category: "source", Summary: "No source content was provided."})
	}
	if packageName == "oracle_nca" {
		findings = append(findings, oracleEBusinessFinding{
			ID:       "oracle_nca_review_only",
			Severity: "error",
			Category: "execution",
			Summary:  "Oracle NCA is review-only in Stormrunner.",
			Detail:   "NCA requires an Oracle Forms/client runner and cannot be promoted to the native Linux HTTP generator path.",
			Evidence: []string{"nca_boundary", "support_matrix:oeb-oracle-nca"},
		})
	}
	if packageName == "oracle_web" || packageName == "siebel_web" || packageName == "web_http_html" {
		if len(candidates) == 0 {
			findings = append(findings, oracleEBusinessFinding{
				ID:       "no_packaged_app_tokens",
				Severity: "warning",
				Category: "correlation",
				Summary:  "No Oracle/Siebel package correlation tokens were detected.",
				Detail:   "The script may still run as generic Web-HTTP/HTML, but package-specific parity requires replay evidence.",
			})
		} else {
			findings = append(findings, oracleEBusinessFinding{
				ID:       "correlation_review_required",
				Severity: "warning",
				Category: "correlation",
				Summary:  fmt.Sprintf("%d packaged-app correlation candidate(s) require review.", len(candidates)),
				Detail:   "Preview/apply repair should create extractors or data-source bindings before promotion.",
			})
		}
	}
	if len(dataFindings) > 0 {
		findings = append(findings, oracleEBusinessFinding{
			ID:       "data_source_fit_review",
			Severity: "warning",
			Category: "data",
			Summary:  fmt.Sprintf("%d data-source fit finding(s) require review.", len(dataFindings)),
		})
	}
	if signals["devweb"] > 0 || signals["jmeter"] > 0 || signals["gatling"] > 0 {
		findings = append(findings, oracleEBusinessFinding{
			ID:       "import_path_available",
			Severity: "info",
			Category: "import",
			Summary:  "Existing DevWeb/JMeter/Web/API import paths can carry this Oracle E-Business review metadata.",
		})
	}
	return findings
}

func oracleEBusinessExecutionPostureFor(packageName string) oracleEBusinessExecutionPosture {
	switch packageName {
	case "oracle_nca":
		return oracleEBusinessNCAPosture()
	case "gatling":
		return oracleEBusinessExecutionPosture{Posture: "review_only", RunnerRequirement: "import_review", CanExecuteNatively: false, BlocksExecution: true, Reasons: []string{"Gatling source is classified but not executed by the Go generator."}}
	default:
		return oracleEBusinessExecutionPosture{Posture: "review_then_executable", RunnerRequirement: "go_generator", CanExecuteNatively: true, BlocksExecution: false, Reasons: []string{"HTTP-backed execution requires correlation, data, replay, and approval evidence."}}
	}
}

func oracleEBusinessNCAPosture() oracleEBusinessExecutionPosture {
	return oracleEBusinessExecutionPosture{
		Posture:            "external_runner_required",
		RunnerRequirement:  "oracle_forms_client_runner",
		CanExecuteNatively: false,
		BlocksExecution:    true,
		Reasons:            []string{"Oracle NCA/Forms traffic depends on client protocol/runtime behavior outside the Linux HTTP generator path."},
	}
}

func oracleEBusinessReviewStatus(packageName string, candidates []oracleEBusinessCorrelationCandidate, findings []oracleEBusinessFinding, posture oracleEBusinessExecutionPosture, summary oracleEBusinessSourceSummary) string {
	if summary.Bytes == 0 {
		return "blocked"
	}
	if posture.BlocksExecution {
		return "review_only"
	}
	for _, finding := range findings {
		if finding.Severity == "error" {
			return "blocked"
		}
	}
	if len(candidates) > 0 || packageName == "oracle_web" || packageName == "siebel_web" {
		return "needs_correlation_review"
	}
	return "ready_for_review"
}

func oracleEBusinessReviewSections(packageName string, candidates []oracleEBusinessCorrelationCandidate, dataFindings []oracleEBusinessDataSourceFinding, findings []oracleEBusinessFinding, posture oracleEBusinessExecutionPosture) []scriptImportReviewSection {
	sections := []scriptImportReviewSection{{
		Key:      "package_profile",
		Title:    "Package Profile",
		Severity: "info",
		Summary:  fmt.Sprintf("Detected %s package posture: %s.", strings.ReplaceAll(packageName, "_", " "), posture.Posture),
		Items:    posture.Reasons,
	}}
	if len(candidates) > 0 {
		items := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			items = append(items, fmt.Sprintf("%s: %s (%d occurrence%s)", candidate.Token, candidate.RecommendedAction, candidate.Count, pluralSuffix(candidate.Count)))
		}
		sections = append(sections, scriptImportReviewSection{Key: "correlation_candidates", Title: "Correlation Candidates", Severity: "warning", Summary: "Packaged-app dynamic values require extractor or data binding review.", Items: items})
	}
	if len(dataFindings) > 0 {
		items := make([]string, 0, len(dataFindings))
		for _, finding := range dataFindings {
			items = append(items, fmt.Sprintf("%s: %s", finding.Field, finding.RecommendedAction))
		}
		sections = append(sections, scriptImportReviewSection{Key: "data_source_fit", Title: "Data Source Fit", Severity: "warning", Summary: "Package workflows should use governed data for users, responsibilities, searches, and row IDs.", Items: items})
	}
	if posture.BlocksExecution {
		sections = append(sections, scriptImportReviewSection{Key: "execution_boundary", Title: "Execution Boundary", Severity: "error", Summary: "This package cannot execute on native Linux generators.", Items: posture.Reasons})
	}
	if len(findings) == 0 {
		sections = append(sections, scriptImportReviewSection{Key: "review", Title: "Review", Severity: "info", Summary: "No package-specific findings were detected.", Items: []string{"Validate replay evidence before promotion."}})
	}
	return sections
}

func oracleEBusinessRepairSuggestions(candidates []oracleEBusinessCorrelationCandidate, dataFindings []oracleEBusinessDataSourceFinding, posture oracleEBusinessExecutionPosture) []scriptImportRepairSuggestion {
	suggestions := make([]scriptImportRepairSuggestion, 0, len(candidates)+len(dataFindings)+1)
	for _, candidate := range candidates {
		severity := "warning"
		if candidate.Blocking {
			severity = "error"
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          candidate.ID,
			Category:    "correlation",
			Severity:    severity,
			Title:       "Review " + candidate.Token,
			Description: candidate.RecommendedAction,
			Action:      "Create or confirm extractor/data binding before approval.",
		})
	}
	for _, finding := range dataFindings {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          finding.ID,
			Category:    "data",
			Severity:    "warning",
			Title:       "Review " + finding.Field + " data",
			Description: finding.RecommendedAction,
			Action:      "Map to a data source or secret-backed parameter.",
		})
	}
	if posture.BlocksExecution {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "execution_boundary",
			Category:    "execution",
			Severity:    "error",
			Title:       "External runner required",
			Description: strings.Join(posture.Reasons, " "),
			Action:      "Keep as review artifact until the required runner class is approved and available.",
		})
	}
	return suggestions
}

func oracleEBusinessNextActions(status, packageName string, posture oracleEBusinessExecutionPosture) []string {
	if posture.BlocksExecution {
		return []string{"Preserve source artifacts", "Review runner/client prerequisites", "Block promotion to native generator execution"}
	}
	actions := []string{"Apply or confirm correlation extractors", "Review parameter/data-source fit", "Replay against a controlled Oracle E-Business fixture", "Attach replay evidence before promotion"}
	if status == "ready_for_review" && (packageName == "devweb" || packageName == "jmeter" || packageName == "web_http_html") {
		actions = append(actions, "Use the existing import path for executable script generation")
	}
	return actions
}

func oracleEBusinessRequiredEvidence(packageName string, posture oracleEBusinessExecutionPosture) []string {
	if posture.BlocksExecution {
		return []string{"source preservation", "runner prerequisite review", "operator approval"}
	}
	evidence := []string{"validation report", "replay result", "correlation review", "data-source fit review", "approval"}
	if packageName == "oracle_web" || packageName == "siebel_web" {
		evidence = append(evidence, "packaged-app token review")
	}
	return evidence
}

func oracleEBusinessSupportEntries() []ProtocolBundleSupportEntry {
	all := protocolBundleSupportMatrix()
	entries := make([]ProtocolBundleSupportEntry, 0)
	for _, entry := range all {
		if entry.Bundle == "oracle_e_business" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func oracleEBusinessCorrelationPacks() []oracleEBusinessCorrelationPack {
	return []oracleEBusinessCorrelationPack{
		{Package: "oracle_web", DisplayName: "Oracle-Web", Status: "implemented", Tokens: []string{"ICX_SESSION", "JSESSIONID", "_FORM", "OAFunc", "p_instance", "OA_HTML"}, Actions: []string{"detect", "review", "repair_suggest", "block_until_replay_evidence"}},
		{Package: "siebel_web", DisplayName: "Siebel-Web", Status: "implemented", Tokens: []string{"SWEC", "SWEExtSource", "SWECount", "SWEView", "SWEApplet", "SWERowId", "SWEUserName", "SWEPassword"}, Actions: []string{"detect", "review", "repair_suggest", "block_until_replay_evidence"}},
		{Package: "oracle_nca", DisplayName: "Oracle NCA", Status: "review_only", Tokens: []string{"nca_connect_server", "nca_set_window", "frmservlet", "formsweb.cfg"}, Actions: []string{"classify", "preserve", "block_native_execution"}},
	}
}

func oracleEBusinessScenarioTemplates() []oracleEBusinessScenarioTemplate {
	return []oracleEBusinessScenarioTemplate{
		{ID: "oracle-ebs-login-responsibility-search", Package: "oracle_web", Name: "Oracle EBS Login, Responsibility, Search", Description: "HTTP-backed flow for login, responsibility selection, search, and logout.", Flow: []string{"login", "select responsibility", "open function", "search fixture data", "logout"}, Parameters: []string{"username", "password_secret", "responsibility", "search_key"}, Evidence: []string{"/api/v1/scripts/import/oracle-ebusiness/review"}},
		{ID: "oracle-ebs-create-update", Package: "oracle_web", Name: "Oracle EBS Create And Update", Description: "Parameterized create/update path with form-token correlation checkpoints.", Flow: []string{"login", "open create form", "submit create", "search created record", "submit update", "logout"}, Parameters: []string{"username", "password_secret", "responsibility", "record_payload"}, Evidence: []string{"/api/v1/scripts/import/oracle-ebusiness/review"}},
		{ID: "siebel-login-query-update", Package: "siebel_web", Name: "Siebel Login, Query, Update", Description: "Siebel-Web navigation flow with SWE state and row-id review gates.", Flow: []string{"login", "navigate view", "query row", "update field", "save", "logout"}, Parameters: []string{"SWEUserName", "SWEPassword_secret", "SWEView", "query_key", "update_value"}, Evidence: []string{"/api/v1/scripts/import/oracle-ebusiness/review"}},
		{ID: "oracle-nca-review-only", Package: "oracle_nca", Name: "Oracle NCA Review Artifact", Description: "Preserve Oracle Forms/NCA source, object references, and runner prerequisites without native execution.", Flow: []string{"classify source", "preserve NCA calls", "capture client prerequisites", "operator decision"}, Parameters: []string{"forms_url", "client_version", "runner_pool"}, Evidence: []string{"/api/v1/scripts/import/oracle-ebusiness/review", "/api/v1/protocol-bundles/support-matrix"}},
	}
}

func templatesForOracleEBusinessPackage(packageName string) []oracleEBusinessScenarioTemplate {
	templates := oracleEBusinessScenarioTemplates()
	if packageName == "" || packageName == "web_http_html" || packageName == "devweb" || packageName == "jmeter" {
		return templates
	}
	out := make([]oracleEBusinessScenarioTemplate, 0)
	for _, template := range templates {
		if template.Package == packageName {
			out = append(out, template)
		}
	}
	return out
}

func countAnyToken(text string, tokens []string) int {
	total := 0
	for _, token := range tokens {
		total += countToken(text, token)
	}
	return total
}

func countToken(text, token string) int {
	if strings.TrimSpace(text) == "" || strings.TrimSpace(token) == "" {
		return 0
	}
	return strings.Count(strings.ToLower(text), strings.ToLower(token))
}

func tokenEvidence(text, token string, limit int) []string {
	if limit <= 0 {
		limit = 1
	}
	lower := strings.ToLower(text)
	lowerToken := strings.ToLower(token)
	evidence := make([]string, 0, limit)
	searchStart := 0
	for len(evidence) < limit {
		index := strings.Index(lower[searchStart:], lowerToken)
		if index < 0 {
			break
		}
		index += searchStart
		start := index - 48
		if start < 0 {
			start = 0
		}
		end := index + len(token) + 48
		if end > len(text) {
			end = len(text)
		}
		evidence = append(evidence, strings.TrimSpace(text[start:end]))
		searchStart = index + len(token)
	}
	return evidence
}

func detectedHosts(text string) []string {
	seen := map[string]struct{}{}
	for _, marker := range []string{"http://", "https://"} {
		lower := strings.ToLower(text)
		start := 0
		for {
			index := strings.Index(lower[start:], marker)
			if index < 0 {
				break
			}
			index += start
			rest := text[index+len(marker):]
			end := len(rest)
			for i, r := range rest {
				if r == '/' || r == '?' || r == '"' || r == '\'' || r == ' ' || r == '\n' || r == '\r' || r == '\t' {
					end = i
					break
				}
			}
			host := strings.TrimSpace(rest[:end])
			if host != "" {
				seen[host] = struct{}{}
			}
			start = index + len(marker)
		}
	}
	hosts := make([]string, 0, len(seen))
	for host := range seen {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func displayTokenName(token string) string {
	return strings.ReplaceAll(token, "_", " ")
}
