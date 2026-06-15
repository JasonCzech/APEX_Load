package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const browserToHTTPSchemaVersion = "apexload.browser_to_http_preview.v1"

type BrowserToHTTPPreviewRequest struct {
	Name                    string                                `json:"name,omitempty"`
	ProjectID               string                                `json:"project_id,omitempty"`
	ReplaySessionID         string                                `json:"replay_session_id,omitempty"`
	SourceBrowserScript     *dsl.Script                           `json:"source_browser_script,omitempty"`
	NetworkEntries          []protocol.NetworkEntry               `json:"network_entries,omitempty"`
	CorrelationObservations []BrowserToHTTPCorrelationObservation `json:"correlation_observations,omitempty"`
	IncludeStaticAssets     bool                                  `json:"include_static_assets,omitempty"`
	Approval                BrowserToHTTPApproval                 `json:"approval,omitempty"`
}

type BrowserToHTTPPreviewResponse struct {
	SchemaVersion       string                        `json:"schema_version"`
	GeneratedAt         time.Time                     `json:"generated_at"`
	ReplaySessionID     string                        `json:"replay_session_id,omitempty"`
	SourceBrowserScript *dsl.Script                   `json:"source_browser_script,omitempty"`
	GeneratedScript     *dsl.Script                   `json:"generated_script,omitempty"`
	Warnings            []string                      `json:"warnings,omitempty"`
	Review              BrowserToHTTPReview           `json:"review"`
	Governance          BrowserToHTTPGovernance       `json:"governance"`
	Diff                BrowserToHTTPDraftDiff        `json:"diff"`
	ScenarioTemplate    BrowserToHTTPScenarioTemplate `json:"scenario_template"`
}

type BrowserToHTTPReview struct {
	Status                string                   `json:"status"`
	PromotionStatus       string                   `json:"promotion_status"`
	SourceBrowserActions  int                      `json:"source_browser_actions"`
	CapturedRequests      int                      `json:"captured_requests"`
	GeneratedHTTPActions  int                      `json:"generated_http_actions"`
	SkippedStaticAssets   int                      `json:"skipped_static_assets"`
	CorrelationEngine     string                   `json:"correlation_engine,omitempty"`
	CorrelationCandidates []BrowserToHTTPCandidate `json:"correlation_candidates,omitempty"`
	DataSourceCandidates  []BrowserToHTTPCandidate `json:"data_source_candidates,omitempty"`
	AuthFindings          []BrowserToHTTPFinding   `json:"auth_findings,omitempty"`
	CookieFindings        []BrowserToHTTPFinding   `json:"cookie_findings,omitempty"`
	BlockingFindings      []BrowserToHTTPFinding   `json:"blocking_findings,omitempty"`
	Findings              []string                 `json:"findings,omitempty"`
	NextActions           []string                 `json:"next_actions,omitempty"`
	Evidence              BrowserToHTTPEvidence    `json:"evidence"`
}

type BrowserToHTTPCandidate struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Value    string `json:"value,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	Action   string `json:"action,omitempty"`
}

type BrowserToHTTPFinding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Evidence string `json:"evidence,omitempty"`
	Action   string `json:"action,omitempty"`
}

type BrowserToHTTPEvidence struct {
	ReplaySessionID         string   `json:"replay_session_id,omitempty"`
	NetworkEntries          int      `json:"network_entries"`
	CorrelationObservations int      `json:"correlation_observations,omitempty"`
	SourceBrowserScriptID   string   `json:"source_browser_script_id,omitempty"`
	SourceBrowserScriptName string   `json:"source_browser_script_name,omitempty"`
	RequiredArtifacts       []string `json:"required_artifacts,omitempty"`
}

type BrowserToHTTPApproval struct {
	Status      string   `json:"status,omitempty"`
	ApprovedBy  string   `json:"approved_by,omitempty"`
	ApprovedAt  string   `json:"approved_at,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type BrowserToHTTPGovernance struct {
	Status           string                 `json:"status"`
	ApprovalRequired bool                   `json:"approval_required"`
	ApprovalStatus   string                 `json:"approval_status"`
	ApprovedBy       string                 `json:"approved_by,omitempty"`
	ApprovedAt       string                 `json:"approved_at,omitempty"`
	RequiredEvidence []string               `json:"required_evidence,omitempty"`
	Blockers         []BrowserToHTTPFinding `json:"blockers,omitempty"`
	NextActions      []string               `json:"next_actions,omitempty"`
}

type BrowserToHTTPDraftDiff struct {
	SourceProtocol       string   `json:"source_protocol,omitempty"`
	GeneratedProtocol    string   `json:"generated_protocol,omitempty"`
	SourceBrowserActions int      `json:"source_browser_actions"`
	GeneratedHTTPActions int      `json:"generated_http_actions"`
	CapturedRequests     int      `json:"captured_requests"`
	EndpointCoveragePct  int      `json:"endpoint_coverage_pct"`
	Hosts                []string `json:"hosts,omitempty"`
	Methods              []string `json:"methods,omitempty"`
	Notes                []string `json:"notes,omitempty"`
}

type BrowserToHTTPCorrelationObservation struct {
	ID              int               `json:"id,omitempty"`
	RequestURL      string            `json:"request_url,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
}

type BrowserToHTTPScenarioTemplate struct {
	Name     string                       `json:"name"`
	Duration string                       `json:"duration"`
	Groups   []BrowserToHTTPScenarioGroup `json:"groups"`
	Notes    []string                     `json:"notes,omitempty"`
}

type BrowserToHTTPScenarioGroup struct {
	Name        string `json:"name"`
	ScriptID    string `json:"script_id"`
	Protocol    string `json:"protocol"`
	VUsers      int    `json:"vusers"`
	Purpose     string `json:"purpose"`
	ClaimPolicy string `json:"claim_policy,omitempty"`
}

func (s *Server) handlePreviewBrowserToHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	var req BrowserToHTTPPreviewRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	entries, skippedStatic, err := s.browserToHTTPNetworkEntries(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(entries) == 0 {
		writeError(w, http.StatusBadRequest, "browser-to-HTTP preview requires replay network entries")
		return
	}

	harData, err := browserNetworkEntriesToHAR(entries)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to build HAR evidence: "+err.Error())
		return
	}
	includeAssertions := true
	inferExtractors := true
	includeStaticAssets := req.IncludeStaticAssets
	result, err := har.Convert(harData, har.ConvertOptions{
		ScriptName:          firstNonEmpty(req.Name, browserHTTPDraftName(req.SourceBrowserScript)),
		IncludeAssertions:   &includeAssertions,
		InferExtractors:     &inferExtractors,
		IncludeStaticAssets: &includeStaticAssets,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to generate HTTP draft: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(firstNonEmpty(req.ProjectID, sourceScriptProjectID(req.SourceBrowserScript)))
		result.Script.GeneratedBy = "apexload-browser-to-http-converter"
		result.Script.GeneratedFrom = "browser_replay"
	}
	warnings := append([]string(nil), result.Warnings...)
	warnings = append(warnings, validateScript(result.Script)...)
	review := buildBrowserToHTTPReview(req, result.Script, entries, skippedStatic)
	governance := buildBrowserToHTTPGovernance(req, review)
	diff := buildBrowserToHTTPDraftDiff(req.SourceBrowserScript, result.Script, entries)
	if result.Script != nil {
		attachBrowserToHTTPMetadata(result.Script, req, len(entries), review, governance, diff)
	}
	response := BrowserToHTTPPreviewResponse{
		SchemaVersion:       browserToHTTPSchemaVersion,
		GeneratedAt:         time.Now().UTC(),
		ReplaySessionID:     req.ReplaySessionID,
		SourceBrowserScript: req.SourceBrowserScript,
		GeneratedScript:     result.Script,
		Warnings:            uniqueSortedStrings(warnings),
		Review:              review,
		Governance:          governance,
		Diff:                diff,
		ScenarioTemplate:    buildBrowserToHTTPScenarioTemplate(req.SourceBrowserScript, result.Script),
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) browserToHTTPNetworkEntries(req BrowserToHTTPPreviewRequest) ([]protocol.NetworkEntry, int, error) {
	entries := append([]protocol.NetworkEntry(nil), req.NetworkEntries...)
	if strings.TrimSpace(req.ReplaySessionID) != "" {
		session, ok := s.browserReplay.Get(strings.TrimSpace(req.ReplaySessionID))
		if !ok {
			if len(entries) == 0 {
				return nil, 0, fmt.Errorf("browser replay session not found")
			}
		} else {
			for _, event := range session.Events {
				entries = append(entries, event.Network...)
				if event.Snapshot != nil {
					entries = append(entries, event.Snapshot.NetworkEntries...)
				}
			}
		}
	}
	filtered := make([]protocol.NetworkEntry, 0, len(entries))
	skippedStatic := 0
	seen := make(map[string]struct{})
	for _, entry := range entries {
		entry.URL = strings.TrimSpace(entry.URL)
		entry.Method = strings.ToUpper(strings.TrimSpace(entry.Method))
		if entry.Method == "" {
			entry.Method = http.MethodGet
		}
		if entry.URL == "" || !isHTTPURL(entry.URL) {
			continue
		}
		if !req.IncludeStaticAssets && isStaticBrowserResource(entry.ResourceType, entry.URL) {
			skippedStatic++
			continue
		}
		key := entry.Method + " " + entry.URL
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, entry)
	}
	return filtered, skippedStatic, nil
}

func browserNetworkEntriesToHAR(entries []protocol.NetworkEntry) ([]byte, error) {
	now := time.Now().UTC()
	harEntries := make([]map[string]any, 0, len(entries))
	for index, entry := range entries {
		status := entry.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		duration := entry.DurationMs
		if duration <= 0 {
			duration = 1
		}
		harEntries = append(harEntries, map[string]any{
			"startedDateTime": now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
			"time":            duration,
			"request": map[string]any{
				"method":      firstNonEmpty(entry.Method, http.MethodGet),
				"url":         entry.URL,
				"httpVersion": "HTTP/1.1",
				"headers":     []any{},
				"queryString": []any{},
				"cookies":     []any{},
				"headersSize": -1,
				"bodySize":    0,
			},
			"response": map[string]any{
				"status":      status,
				"statusText":  http.StatusText(status),
				"httpVersion": "HTTP/1.1",
				"headers":     []any{},
				"cookies":     []any{},
				"content": map[string]any{
					"size":     entry.BytesReceived,
					"mimeType": "",
					"text":     "",
				},
				"redirectURL": "",
				"headersSize": -1,
				"bodySize":    entry.BytesReceived,
			},
			"cache": map[string]any{},
			"timings": map[string]any{
				"send":    0,
				"wait":    duration,
				"receive": 0,
			},
		})
	}
	payload := map[string]any{
		"log": map[string]any{
			"version": "1.2",
			"creator": map[string]any{
				"name":    "apexload-browser-to-http",
				"version": "1.0.0",
			},
			"entries": harEntries,
		},
	}
	return json.Marshal(payload)
}

func buildBrowserToHTTPReview(req BrowserToHTTPPreviewRequest, generated *dsl.Script, entries []protocol.NetworkEntry, skippedStatic int) BrowserToHTTPReview {
	review := BrowserToHTTPReview{
		Status:               "ready_for_review",
		PromotionStatus:      "draft_requires_approval",
		SourceBrowserActions: countBrowserActions(req.SourceBrowserScript),
		CapturedRequests:     len(entries),
		GeneratedHTTPActions: countScriptActions(generated),
		SkippedStaticAssets:  skippedStatic,
		CorrelationEngine:    "apexload-correlate",
		Evidence: BrowserToHTTPEvidence{
			ReplaySessionID:         strings.TrimSpace(req.ReplaySessionID),
			NetworkEntries:          len(entries),
			CorrelationObservations: len(req.CorrelationObservations),
			SourceBrowserScriptID:   sourceScriptID(req.SourceBrowserScript),
			SourceBrowserScriptName: sourceScriptName(req.SourceBrowserScript),
			RequiredArtifacts: []string{
				"browser replay session id",
				"network capture or HAR",
				"HTTP draft validation result",
				"correlation review decision",
				"approval record",
			},
		},
		Findings: []string{
			fmt.Sprintf("%d replay network request(s) were converted into an HTTP draft.", len(entries)),
			"Generated HTTP scripts still require validation, replay, correlation review, data-source fit, and approval before density use.",
		},
		NextActions: []string{
			"Review conversion blockers and generated HTTP actions.",
			"Validate the HTTP draft against the target environment.",
			"Approve the draft only after correlation and authentication handling are reviewed.",
		},
	}
	review.CorrelationCandidates = uniqueBrowserToHTTPCandidates(append(browserToHTTPCorrelationCandidates(entries), browserToHTTPCorrelateCandidates(req)...))
	review.DataSourceCandidates = browserToHTTPDataSourceCandidates(entries)
	review.AuthFindings = []BrowserToHTTPFinding{{
		Severity: "info",
		Summary:  "Replay network summaries do not include request headers or bodies.",
		Evidence: "browser_replay.events[].network_entries",
		Action:   "Use detailed HAR capture or replay snapshots before approving auth headers, CSRF tokens, or request body variables.",
	}}
	review.CookieFindings = []BrowserToHTTPFinding{{
		Severity: "info",
		Summary:  "Cookie state is not inferred from compact network entries.",
		Evidence: "browser_replay.events[].network_entries",
		Action:   "Validate generated HTTP auth and cookie handling with a protocol replay before load-model assembly.",
	}}
	if len(entries) == 0 || review.GeneratedHTTPActions == 0 {
		review.Status = "blocked"
		review.PromotionStatus = "blocked"
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "No convertible replay network requests were available.",
			Action:   "Run browser replay with network capture enabled, then retry conversion.",
		})
	} else if len(review.CorrelationCandidates) > 0 || len(review.DataSourceCandidates) > 0 {
		review.Status = "needs_repair"
		review.PromotionStatus = "draft_blocked"
		review.Findings = append(review.Findings, "Dynamic URL values were detected and should be parameterized or correlated before approval.")
	}
	if strings.TrimSpace(req.ReplaySessionID) == "" {
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "No durable browser replay session id was attached.",
			Evidence: "replay_session_id",
			Action:   "Run or attach a browser replay session before approving the generated HTTP draft.",
		})
	}
	if len(req.CorrelationObservations) == 0 {
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "warning",
			Summary:  "No request/response bodies were provided for deterministic correlation detection.",
			Evidence: "correlation_observations",
			Action:   "Attach detailed HAR or replay observations so APEX can run the correlation engine before approval.",
		})
	}
	if len(review.BlockingFindings) > 0 && review.PromotionStatus == "draft_requires_approval" {
		review.PromotionStatus = "draft_blocked"
	}
	if skippedStatic > 0 {
		review.Findings = append(review.Findings, fmt.Sprintf("%d static browser asset request(s) were skipped for protocol-load focus.", skippedStatic))
	}
	return review
}

func browserToHTTPCorrelateCandidates(req BrowserToHTTPPreviewRequest) []BrowserToHTTPCandidate {
	observations := make([]correlate.Observation, 0, len(req.CorrelationObservations))
	for index, observation := range req.CorrelationObservations {
		id := observation.ID
		if id == 0 {
			id = index + 1
		}
		observations = append(observations, correlate.Observation{
			ID:              id,
			RequestURL:      observation.RequestURL,
			RequestBody:     observation.RequestBody,
			RequestHeaders:  observation.RequestHeaders,
			ResponseBody:    observation.ResponseBody,
			ResponseHeaders: observation.ResponseHeaders,
		})
	}
	correlations := correlate.Detect(observations)
	candidates := make([]BrowserToHTTPCandidate, 0, len(correlations))
	for _, correlation := range correlations {
		candidates = append(candidates, BrowserToHTTPCandidate{
			Name:     correlation.Name,
			Source:   correlation.Source,
			Value:    correlation.SampleValue,
			Evidence: fmt.Sprintf("response %d reused by request(s) %v via %s confidence %.2f", correlation.FoundInID, correlation.UsedInIDs, correlation.RuleID, correlation.Confidence),
			Action:   "Add an extractor at the source response and replace downstream uses with " + "{{var." + correlation.Name + "}} before approval.",
		})
	}
	return candidates
}

func browserToHTTPCorrelationCandidates(entries []protocol.NetworkEntry) []BrowserToHTTPCandidate {
	candidates := make([]BrowserToHTTPCandidate, 0)
	for _, entry := range entries {
		parsed, err := url.Parse(entry.URL)
		if err != nil {
			continue
		}
		for key, values := range parsed.Query() {
			for _, value := range values {
				if looksDynamicValue(key, value) {
					candidates = append(candidates, BrowserToHTTPCandidate{
						Name:     key,
						Source:   "query",
						Value:    value,
						Evidence: entry.Method + " " + parsed.Path,
						Action:   "Promote this value into a correlation extractor or data-source parameter before approving the HTTP draft.",
					})
				}
			}
		}
	}
	return uniqueBrowserToHTTPCandidates(candidates)
}

func browserToHTTPDataSourceCandidates(entries []protocol.NetworkEntry) []BrowserToHTTPCandidate {
	candidates := make([]BrowserToHTTPCandidate, 0)
	for _, entry := range entries {
		parsed, err := url.Parse(entry.URL)
		if err != nil {
			continue
		}
		for key, values := range parsed.Query() {
			if !looksParameterName(key) {
				continue
			}
			for _, value := range values {
				candidates = append(candidates, BrowserToHTTPCandidate{
					Name:     key,
					Source:   "query",
					Value:    value,
					Evidence: entry.Method + " " + parsed.Path,
					Action:   "Bind this value to a data source or generated parameter before scale-out execution.",
				})
			}
		}
	}
	return uniqueBrowserToHTTPCandidates(candidates)
}

func uniqueBrowserToHTTPCandidates(candidates []BrowserToHTTPCandidate) []BrowserToHTTPCandidate {
	seen := make(map[string]struct{})
	out := make([]BrowserToHTTPCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidate.Source + ":" + candidate.Name + ":" + candidate.Value + ":" + candidate.Evidence
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Evidence < out[j].Evidence
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func buildBrowserToHTTPScenarioTemplate(source *dsl.Script, generated *dsl.Script) BrowserToHTTPScenarioTemplate {
	browserID := "{{browser_script_id}}"
	if source != nil && strings.TrimSpace(source.ID) != "" {
		browserID = source.ID
	}
	httpID := "{{generated_http_script_id}}"
	if generated != nil && strings.TrimSpace(generated.ID) != "" {
		httpID = generated.ID
	}
	name := "Browser Canary Plus Protocol Load"
	if source != nil && strings.TrimSpace(source.Name) != "" {
		name = source.Name + " Canary Plus HTTP Load"
	}
	return BrowserToHTTPScenarioTemplate{
		Name:     name,
		Duration: "10m",
		Groups: []BrowserToHTTPScenarioGroup{
			{Name: "browser_canary", ScriptID: browserID, Protocol: "browser", VUsers: 1, Purpose: "UX and migration evidence", ClaimPolicy: "diagnostic_canary_only"},
			{Name: "protocol_load", ScriptID: httpID, Protocol: "http", VUsers: 50, Purpose: "Protocol-load density after validation and approval"},
		},
		Notes: []string{
			"Keep browser VUsers at canary density unless benchmark evidence is attached.",
			"Validate and replay the generated HTTP script before using this scenario template.",
		},
	}
}

func buildBrowserToHTTPGovernance(req BrowserToHTTPPreviewRequest, review BrowserToHTTPReview) BrowserToHTTPGovernance {
	governance := BrowserToHTTPGovernance{
		Status:           "approval_required",
		ApprovalRequired: true,
		ApprovalStatus:   firstNonEmpty(strings.ToLower(strings.TrimSpace(req.Approval.Status)), "not_requested"),
		ApprovedBy:       strings.TrimSpace(req.Approval.ApprovedBy),
		ApprovedAt:       strings.TrimSpace(req.Approval.ApprovedAt),
		RequiredEvidence: append([]string(nil), review.Evidence.RequiredArtifacts...),
		Blockers:         append([]BrowserToHTTPFinding(nil), review.BlockingFindings...),
		NextActions: []string{
			"Resolve conversion blockers and replay the generated HTTP draft.",
			"Review correlation candidates, auth headers, cookies, and data-source candidates.",
			"Record approval only after validation evidence is attached.",
		},
	}
	if len(review.CorrelationCandidates) > 0 {
		governance.Blockers = append(governance.Blockers, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "Correlation candidates require review before density promotion.",
			Evidence: "review.correlation_candidates",
			Action:   "Apply extractors and variable substitutions or explicitly waive each candidate.",
		})
	}
	if len(review.DataSourceCandidates) > 0 {
		governance.Blockers = append(governance.Blockers, BrowserToHTTPFinding{
			Severity: "warning",
			Summary:  "Data-source candidates require parameterization review.",
			Evidence: "review.data_source_candidates",
			Action:   "Bind user/order/customer values to governed data sources before scale-out.",
		})
	}
	switch strings.ToLower(strings.TrimSpace(req.Approval.Status)) {
	case "approved":
		if governance.ApprovedBy == "" {
			governance.Status = "approval_invalid"
			governance.Blockers = append(governance.Blockers, BrowserToHTTPFinding{
				Severity: "blocker",
				Summary:  "Approval status is approved but approved_by is missing.",
				Evidence: "approval.approved_by",
				Action:   "Record the approving reviewer before promotion.",
			})
		} else if len(governance.Blockers) == 0 {
			governance.Status = "approved_for_validation"
			governance.ApprovalRequired = false
			governance.NextActions = []string{"Run the approved HTTP draft in a protocol validation scenario before density use."}
		} else {
			governance.Status = "approved_with_blockers"
		}
	case "rejected":
		governance.Status = "rejected"
	default:
		if len(governance.Blockers) > 0 {
			governance.Status = "blocked"
		}
	}
	return governance
}

func buildBrowserToHTTPDraftDiff(source *dsl.Script, generated *dsl.Script, entries []protocol.NetworkEntry) BrowserToHTTPDraftDiff {
	diff := BrowserToHTTPDraftDiff{
		SourceProtocol:       scriptProtocol(source),
		GeneratedProtocol:    scriptProtocol(generated),
		SourceBrowserActions: countBrowserActions(source),
		GeneratedHTTPActions: countScriptActions(generated),
		CapturedRequests:     len(entries),
		Hosts:                browserToHTTPHosts(entries),
		Methods:              browserToHTTPMethods(entries),
	}
	if len(entries) > 0 {
		diff.EndpointCoveragePct = int(float64(diff.GeneratedHTTPActions)/float64(len(entries))*100 + 0.5)
		if diff.EndpointCoveragePct > 100 {
			diff.EndpointCoveragePct = 100
		}
	}
	diff.Notes = []string{
		fmt.Sprintf("%d browser action(s) remain as canary evidence; %d HTTP action(s) were generated for protocol replay.", diff.SourceBrowserActions, diff.GeneratedHTTPActions),
		"Coverage is based on compact replay network entries after static asset filtering.",
	}
	return diff
}

func attachBrowserToHTTPMetadata(script *dsl.Script, req BrowserToHTTPPreviewRequest, requestCount int, review BrowserToHTTPReview, governance BrowserToHTTPGovernance, diff BrowserToHTTPDraftDiff) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	script.Metadata.Import = &dsl.ImportMetadata{
		Source:           "browser_replay",
		SourceScriptName: browserHTTPDraftName(req.SourceBrowserScript),
		Preserved: map[string]any{
			"source_profile":               "web2_browser_profile_to_http",
			"replay_session_id":            req.ReplaySessionID,
			"captured_network_requests":    requestCount,
			"source_browser_script_id":     sourceScriptID(req.SourceBrowserScript),
			"source_browser_script_name":   sourceScriptName(req.SourceBrowserScript),
			"conversion_requires_approval": true,
			"browser_to_http_review":       review,
			"browser_to_http_governance":   governance,
			"browser_to_http_diff":         diff,
		},
	}
}

func scriptProtocol(script *dsl.Script) string {
	if script == nil {
		return ""
	}
	return strings.TrimSpace(script.Protocol)
}

func browserToHTTPHosts(entries []protocol.NetworkEntry) []string {
	seen := map[string]struct{}{}
	for _, entry := range entries {
		parsed, err := url.Parse(entry.URL)
		if err != nil || parsed.Host == "" {
			continue
		}
		seen[parsed.Host] = struct{}{}
	}
	hosts := make([]string, 0, len(seen))
	for host := range seen {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func browserToHTTPMethods(entries []protocol.NetworkEntry) []string {
	seen := map[string]struct{}{}
	for _, entry := range entries {
		method := strings.ToUpper(strings.TrimSpace(entry.Method))
		if method == "" {
			method = http.MethodGet
		}
		seen[method] = struct{}{}
	}
	methods := make([]string, 0, len(seen))
	for method := range seen {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

func countBrowserActions(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	count := 0
	for _, action := range append(append([]dsl.Action{}, script.Init...), append(script.Actions, script.Teardown...)...) {
		if strings.HasPrefix(action.Type, "browser_") {
			count++
		}
	}
	return count
}

func countScriptActions(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return len(script.Init) + len(script.Actions) + len(script.Teardown)
}

func browserHTTPDraftName(source *dsl.Script) string {
	if source != nil && strings.TrimSpace(source.Name) != "" {
		return source.Name + " HTTP Draft"
	}
	return "Generated HTTP Draft"
}

func sourceScriptID(source *dsl.Script) string {
	if source == nil {
		return ""
	}
	return strings.TrimSpace(source.ID)
}

func sourceScriptName(source *dsl.Script) string {
	if source == nil {
		return ""
	}
	return strings.TrimSpace(source.Name)
}

func sourceScriptProjectID(source *dsl.Script) string {
	if source == nil {
		return ""
	}
	return strings.TrimSpace(source.ProjectID)
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func isStaticBrowserResource(resourceType string, rawURL string) bool {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "stylesheet", "script", "image", "font", "media":
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	for _, suffix := range []string{".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf", ".mp4", ".webm"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func looksDynamicValue(key string, value string) bool {
	return paramize.LooksDynamicValue(key, value)
}

func looksParameterName(key string) bool {
	return paramize.LooksParameterName(key)
}
