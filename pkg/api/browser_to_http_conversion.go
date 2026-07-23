package api

import (
	"encoding/json"
	"errors"
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
	Status                string                              `json:"status"`
	PromotionStatus       string                              `json:"promotion_status"`
	SourceBrowserActions  int                                 `json:"source_browser_actions"`
	CapturedRequests      int                                 `json:"captured_requests"`
	GeneratedHTTPActions  int                                 `json:"generated_http_actions"`
	SkippedStaticAssets   int                                 `json:"skipped_static_assets"`
	CorrelationEngine     string                              `json:"correlation_engine,omitempty"`
	CorrelationCandidates []BrowserToHTTPCorrelationCandidate `json:"correlation_candidates,omitempty"`
	DataSourceCandidates  []BrowserToHTTPCandidate            `json:"data_source_candidates,omitempty"`
	AuthFindings          []BrowserToHTTPFinding              `json:"auth_findings,omitempty"`
	CookieFindings        []BrowserToHTTPFinding              `json:"cookie_findings,omitempty"`
	BlockingFindings      []BrowserToHTTPFinding              `json:"blocking_findings,omitempty"`
	Findings              []string                            `json:"findings,omitempty"`
	NextActions           []string                            `json:"next_actions,omitempty"`
	Evidence              BrowserToHTTPEvidence               `json:"evidence"`
}

type BrowserToHTTPCandidate struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Value    string `json:"value,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	Action   string `json:"action,omitempty"`
}

// BrowserToHTTPCorrelationCandidate intentionally omits the captured value.
// Correlation evidence can contain credentials and session tokens, so preview
// responses identify a candidate structurally without echoing its literal.
type BrowserToHTTPCorrelationCandidate struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
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
	ReplaySessionResolved   bool     `json:"replay_session_resolved"`
	ReplaySessionStatus     string   `json:"replay_session_status,omitempty"`
	ReplaySessionSuccessful bool     `json:"replay_session_successful"`
	ReplayNetworkEntries    int      `json:"replay_network_entries,omitempty"`
	NetworkDurable          bool     `json:"network_durable"`
	CorrelationDurable      bool     `json:"correlation_durable"`
	Durable                 bool     `json:"durable"`
	VerificationStatus      string   `json:"verification_status"`
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
	var replaySession *BrowserReplaySession
	if replayID := strings.TrimSpace(req.ReplaySessionID); replayID != "" && s.browserReplay != nil {
		replaySession, _ = s.browserReplay.Get(replayID)
		// The route is authorized against the declared/source project before the
		// handler runs. Treat a session from any other project exactly like an
		// unknown ID so the body reference cannot be used as an existence oracle.
		sourceProjectID := ""
		if req.SourceBrowserScript != nil {
			sourceProjectID = req.SourceBrowserScript.ProjectID
		}
		requestProjectID, projectErr := reconcileProjectIDCandidates(req.ProjectID, sourceProjectID)
		if projectErr == nil && replaySession != nil && normalizeProjectID(replaySession.ProjectID) != normalizeProjectID(requestProjectID) {
			replaySession = nil
		}
	}
	if err := s.bindBrowserToHTTPProject(&req, replaySession); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, skippedStatic, replayEvidence, err := s.browserToHTTPNetworkEntries(req, replaySession)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(entries) == 0 {
		writeError(w, http.StatusBadRequest, "browser-to-HTTP preview requires replay network entries")
		return
	}
	uploadedCorrelationCandidates, err := browserToHTTPCorrelateCandidates(req)
	var correlationWarnings []string
	if err != nil {
		if !errors.Is(err, correlate.ErrCorrelationWorkLimit) {
			writeError(w, http.StatusInternalServerError, "failed to analyze browser-to-HTTP correlation evidence")
			return
		}
		// A deterministic work limit must not kill the conversion preview:
		// proceed without correlation hints and surface the degradation.
		uploadedCorrelationCandidates = nil
		correlationWarnings = append(correlationWarnings, "browser-to-HTTP correlation evidence exceeded deterministic work limits; correlation candidate analysis was skipped")
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
		protectBrowserToHTTPCorrelationQueryValues(result.Script)
	}
	warnings := append([]string(nil), result.Warnings...)
	warnings = append(warnings, correlationWarnings...)
	warnings = append(warnings, validateScript(result.Script)...)
	review := buildBrowserToHTTPReview(req, result.Script, entries, skippedStatic, replayEvidence, uploadedCorrelationCandidates)
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

func (s *Server) bindBrowserToHTTPProject(req *BrowserToHTTPPreviewRequest, replaySession *BrowserReplaySession) error {
	if req == nil {
		return nil
	}
	sourceProjectID := ""
	if req.SourceBrowserScript != nil {
		sourceProjectID = req.SourceBrowserScript.ProjectID
	}
	replayProjectID := ""
	if replaySession != nil {
		replayProjectID = replaySession.ProjectID
	}
	projectID, err := reconcileProjectIDCandidates(req.ProjectID, sourceProjectID, replayProjectID)
	if err != nil {
		return fmt.Errorf("browser-to-HTTP project mismatch: %w", err)
	}
	projectID = normalizeProjectID(projectID)
	req.ProjectID = projectID
	if req.SourceBrowserScript != nil {
		req.SourceBrowserScript.ProjectID = projectID
	}
	return nil
}

type browserToHTTPReplayEvidence struct {
	SessionResolved   bool
	SessionStatus     string
	SessionSuccessful bool
	NetworkEntries    int
	AllEntriesBound   bool
}

func (s *Server) browserToHTTPNetworkEntries(req BrowserToHTTPPreviewRequest, session *BrowserReplaySession) ([]protocol.NetworkEntry, int, browserToHTTPReplayEvidence, error) {
	postedEntries := append([]protocol.NetworkEntry(nil), req.NetworkEntries...)
	sessionEntries := make([]protocol.NetworkEntry, 0)
	replayEvidence := browserToHTTPReplayEvidence{}
	if strings.TrimSpace(req.ReplaySessionID) != "" {
		if session == nil {
			if len(postedEntries) == 0 {
				return nil, 0, replayEvidence, fmt.Errorf("browser replay session not found")
			}
		} else {
			replayEvidence.SessionResolved = true
			replayEvidence.SessionStatus = strings.TrimSpace(session.Status)
			replayEvidence.SessionSuccessful = session.Status == BrowserReplayCompleted && session.FinalReport != nil && session.FinalReport.Success
			for _, event := range session.Events {
				sessionEntries = append(sessionEntries, event.Network...)
				if event.Snapshot != nil {
					sessionEntries = append(sessionEntries, event.Snapshot.NetworkEntries...)
				}
			}
		}
	}
	filteredSessionEntries, skippedSessionStatic := filterBrowserToHTTPNetworkEntries(sessionEntries, req.IncludeStaticAssets)
	filteredPostedEntries, skippedPostedStatic := filterBrowserToHTTPNetworkEntries(postedEntries, req.IncludeStaticAssets)
	replayEvidence.NetworkEntries = len(filteredSessionEntries)

	// Session evidence is canonical. Uploaded entries with the same request
	// fingerprint are discarded rather than allowed to replace the session's
	// status/headers/timing/truncation fields. Uploaded requests that are absent
	// from the session remain available for an explicitly unverified preview.
	sessionRequestKeys := make(map[string]struct{}, len(filteredSessionEntries))
	for _, entry := range filteredSessionEntries {
		sessionRequestKeys[browserNetworkRequestKey(entry)] = struct{}{}
	}
	unboundPosted := make([]protocol.NetworkEntry, 0, len(filteredPostedEntries))
	for _, entry := range filteredPostedEntries {
		if _, shadowsSession := sessionRequestKeys[browserNetworkRequestKey(entry)]; shadowsSession {
			continue
		}
		unboundPosted = append(unboundPosted, entry)
	}
	entries := append(append([]protocol.NetworkEntry(nil), filteredSessionEntries...), unboundPosted...)
	filtered, _ := filterBrowserToHTTPNetworkEntries(entries, req.IncludeStaticAssets)
	skippedStatic := skippedSessionStatic + skippedPostedStatic
	if replayEvidence.SessionResolved && len(filtered) > 0 && len(filteredSessionEntries) > 0 {
		sessionKeys := make(map[string]struct{}, len(filteredSessionEntries))
		for _, entry := range filteredSessionEntries {
			sessionKeys[browserNetworkDedupeKey(entry)] = struct{}{}
		}
		replayEvidence.AllEntriesBound = true
		for _, entry := range filtered {
			if _, ok := sessionKeys[browserNetworkDedupeKey(entry)]; !ok {
				replayEvidence.AllEntriesBound = false
				break
			}
		}
	}
	return filtered, skippedStatic, replayEvidence, nil
}

func filterBrowserToHTTPNetworkEntries(entries []protocol.NetworkEntry, includeStaticAssets bool) ([]protocol.NetworkEntry, int) {
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
		if !includeStaticAssets && isStaticBrowserResource(entry.ResourceType, entry.URL) {
			skippedStatic++
			continue
		}
		key := browserNetworkDedupeKey(entry)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, entry)
	}
	return filtered, skippedStatic
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
		requestBodySize := len(entry.RequestBody)
		requestHeaders := browserHARHeaderEntries(entry.RequestHeaders)
		request := map[string]any{
			"method":      firstNonEmpty(entry.Method, http.MethodGet),
			"url":         entry.URL,
			"httpVersion": "HTTP/1.1",
			"headers":     requestHeaders,
			"queryString": []any{},
			"cookies":     []any{},
			"headersSize": -1,
			"bodySize":    requestBodySize,
		}
		if entry.RequestBody != "" {
			postData := map[string]any{
				"mimeType": browserHeaderValue(entry.RequestHeaders, "content-type"),
				"text":     entry.RequestBody,
			}
			if entry.RequestBodyEncoding != "" {
				postData["encoding"] = entry.RequestBodyEncoding
			}
			request["postData"] = postData
		}
		harEntries = append(harEntries, map[string]any{
			"startedDateTime": now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
			"time":            duration,
			"request":         request,
			"response": map[string]any{
				"status":      status,
				"statusText":  http.StatusText(status),
				"httpVersion": "HTTP/1.1",
				"headers":     browserHARHeaderEntries(entry.ResponseHeaders),
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

func browserNetworkDedupeKey(entry protocol.NetworkEntry) string {
	payload, _ := json.Marshal(entry)
	return string(payload)
}

func browserNetworkRequestKey(entry protocol.NetworkEntry) string {
	headersJSON, _ := json.Marshal(entry.RequestHeaders)
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(entry.Method)),
		strings.TrimSpace(entry.URL),
		entry.RequestBody,
		entry.RequestBodyEncoding,
		fmt.Sprint(entry.RequestBodyTruncated),
		string(headersJSON),
	}, "\x00")
}

func browserHARHeaderEntries(headers map[string]string) []any {
	if len(headers) == 0 {
		return []any{}
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]any, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, map[string]any{
			"name":  key,
			"value": headers[key],
		})
	}
	return entries
}

func browserHeaderValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func buildBrowserToHTTPReview(req BrowserToHTTPPreviewRequest, generated *dsl.Script, entries []protocol.NetworkEntry, skippedStatic int, replayEvidence browserToHTTPReplayEvidence, uploadedCorrelationCandidates []BrowserToHTTPCorrelationCandidate) BrowserToHTTPReview {
	networkDurable := replayEvidence.SessionResolved && replayEvidence.SessionSuccessful && replayEvidence.AllEntriesBound
	correlationDurable := false // No server-held browser correlation artifact exists yet.
	verificationStatus := "unverified_uploaded_evidence"
	if networkDurable {
		verificationStatus = "unverified_correlation_evidence"
	} else if strings.TrimSpace(req.ReplaySessionID) != "" && !replayEvidence.SessionResolved {
		verificationStatus = "unverified_replay_session_unresolved"
	} else if replayEvidence.SessionResolved && !replayEvidence.SessionSuccessful {
		verificationStatus = "unverified_replay_session_incomplete_or_failed"
	} else if replayEvidence.SessionResolved && replayEvidence.NetworkEntries > 0 {
		verificationStatus = "unverified_mixed_evidence"
	}
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
			ReplaySessionResolved:   replayEvidence.SessionResolved,
			ReplaySessionStatus:     replayEvidence.SessionStatus,
			ReplaySessionSuccessful: replayEvidence.SessionSuccessful,
			ReplayNetworkEntries:    replayEvidence.NetworkEntries,
			NetworkDurable:          networkDurable,
			CorrelationDurable:      correlationDurable,
			Durable:                 networkDurable && correlationDurable,
			VerificationStatus:      verificationStatus,
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
	review.CorrelationCandidates = uniqueBrowserToHTTPCorrelationCandidates(append(browserToHTTPCorrelationCandidates(entries), uploadedCorrelationCandidates...))
	review.DataSourceCandidates = browserToHTTPDataSourceCandidates(entries)
	review.AuthFindings = []BrowserToHTTPFinding{{
		Severity: "info",
		Summary:  "Replay network summaries may still omit response bodies.",
		Evidence: "browser_replay.events[].network_entries",
		Action:   "Validate generated HTTP auth, cookies, CSRF tokens, request bodies, and response-derived correlations with protocol replay or full HAR evidence before approval.",
	}}
	review.CookieFindings = []BrowserToHTTPFinding{{
		Severity: "info",
		Summary:  "Cookie state is captured only when present in request headers; browser cookie jar semantics are not inferred from compact entries.",
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
	} else if !replayEvidence.SessionResolved {
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "The attached browser replay session could not be resolved; posted network evidence is unverified.",
			Evidence: "replay_session_id",
			Action:   "Run a new browser replay and attach its live session evidence before approving the generated HTTP draft.",
		})
	} else if !replayEvidence.SessionSuccessful {
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "The browser replay session is not a completed successful replay.",
			Evidence: "replay_session_id",
			Action:   "Complete a successful browser replay before approving the generated HTTP draft.",
		})
	} else if !replayEvidence.AllEntriesBound {
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "One or more converted network entries are not bound to the resolved browser replay session.",
			Evidence: "network_entries",
			Action:   "Use network evidence retained by the resolved browser replay session before approving the generated HTTP draft.",
		})
	}
	if len(req.CorrelationObservations) == 0 {
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "No durable request/response correlation evidence is attached.",
			Evidence: "correlation_observations",
			Action:   "Capture correlation observations in a server-held browser replay artifact before approval.",
		})
	} else {
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "Uploaded correlation observations are advisory and are not bound to the browser replay session.",
			Evidence: "correlation_observations",
			Action:   "Use server-held correlation evidence bound to the completed replay before approval.",
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

func browserToHTTPCorrelateCandidates(req BrowserToHTTPPreviewRequest) ([]BrowserToHTTPCorrelationCandidate, error) {
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
	correlations, err := correlate.DetectChecked(observations)
	if err != nil {
		return nil, err
	}
	candidates := make([]BrowserToHTTPCorrelationCandidate, 0, len(correlations))
	for _, correlation := range correlations {
		candidates = append(candidates, BrowserToHTTPCorrelationCandidate{
			Name:     correlation.Name,
			Source:   correlation.Source,
			Evidence: fmt.Sprintf("response %d reused by request(s) %v via %s confidence %.2f", correlation.FoundInID, correlation.UsedInIDs, correlation.RuleID, correlation.Confidence),
			Action:   "Add an extractor at the source response and replace downstream uses with " + "{{var." + correlation.Name + "}} before approval.",
		})
	}
	return candidates, nil
}

func browserToHTTPCorrelationCandidates(entries []protocol.NetworkEntry) []BrowserToHTTPCorrelationCandidate {
	candidates := make([]BrowserToHTTPCorrelationCandidate, 0)
	for _, entry := range entries {
		parsed, err := url.Parse(entry.URL)
		if err != nil {
			continue
		}
		for key, values := range parsed.Query() {
			for _, value := range values {
				if looksDynamicValue(key, value) {
					candidates = append(candidates, BrowserToHTTPCorrelationCandidate{
						Name:     key,
						Source:   "query",
						Evidence: entry.Method + " " + parsed.Path,
						Action:   "Promote this value into a correlation extractor or data-source parameter before approving the HTTP draft.",
					})
				}
			}
		}
	}
	return uniqueBrowserToHTTPCorrelationCandidates(candidates)
}

// protectBrowserToHTTPCorrelationQueryValues prevents a generated, persistable
// draft from carrying live session/CSRF/nonce literals copied from replay
// traffic. The structural review still identifies the candidate, while the
// draft requires an explicit governed secret binding until an extractor is
// added from full response evidence.
func protectBrowserToHTTPCorrelationQueryValues(script *dsl.Script) {
	if script == nil {
		return
	}
	protect := func(actions []dsl.Action) {
		for index := range actions {
			action := &actions[index]
			parsed, err := url.Parse(action.Target)
			if err != nil || parsed.RawQuery == "" {
				continue
			}
			parts := strings.Split(parsed.RawQuery, "&")
			changed := false
			for partIndex, part := range parts {
				rawKey, rawValue, found := strings.Cut(part, "=")
				if !found {
					continue
				}
				key, keyErr := url.QueryUnescape(rawKey)
				value, valueErr := url.QueryUnescape(rawValue)
				if keyErr != nil || valueErr != nil || !looksDynamicValue(key, value) {
					continue
				}
				name := correlate.Slug(key)
				if name == "" {
					name = "correlation_value"
				}
				parts[partIndex] = rawKey + "={{secret." + name + "}}"
				changed = true
			}
			if changed {
				parsed.RawQuery = strings.Join(parts, "&")
				action.Target = parsed.String()
			}
		}
	}
	protect(script.Init)
	protect(script.Actions)
	protect(script.Teardown)
}

func uniqueBrowserToHTTPCorrelationCandidates(candidates []BrowserToHTTPCorrelationCandidate) []BrowserToHTTPCorrelationCandidate {
	seen := make(map[string]struct{})
	out := make([]BrowserToHTTPCorrelationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidate.Source + ":" + candidate.Name + ":" + candidate.Evidence
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
				// A field can look like both business data (for example
				// session_id) and a live correlation value. Correlation wins:
				// returning the same literal in this value-bearing list would bypass
				// the disclosure-safe correlation-candidate projection above.
				if looksDynamicValue(key, value) {
					continue
				}
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
	clientApprovalClaim := strings.TrimSpace(req.Approval.Status) != "" ||
		strings.TrimSpace(req.Approval.ApprovedBy) != "" ||
		strings.TrimSpace(req.Approval.ApprovedAt) != "" ||
		strings.TrimSpace(req.Approval.Notes) != "" || len(req.Approval.EvidenceIDs) > 0
	approvalStatus := "not_requested"
	if clientApprovalClaim {
		approvalStatus = "unverified_client_claim"
	}
	governance := BrowserToHTTPGovernance{
		Status:           "approval_required",
		ApprovalRequired: true,
		ApprovalStatus:   approvalStatus,
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
	if clientApprovalClaim {
		// Preview payloads are editor-controlled and are not an approval ledger.
		// Never copy claimed identity/timestamps into durable script metadata or
		// allow them to clear approval_required.
		governance.Blockers = append(governance.Blockers, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "The submitted approval is not backed by authenticated server approval state.",
			Evidence: "approval",
			Action:   "Record approval through a server-managed approval workflow before promotion.",
		})
		governance.Status = "approval_unverified"
	} else if len(governance.Blockers) > 0 {
		governance.Status = "blocked"
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
