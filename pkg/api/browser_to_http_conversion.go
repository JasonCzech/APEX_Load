package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/convert/har"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

const browserToHTTPSchemaVersion = "stormrunner.browser_to_http_preview.v1"

type BrowserToHTTPPreviewRequest struct {
	Name                string                  `json:"name,omitempty"`
	ProjectID           string                  `json:"project_id,omitempty"`
	ReplaySessionID     string                  `json:"replay_session_id,omitempty"`
	SourceBrowserScript *dsl.Script             `json:"source_browser_script,omitempty"`
	NetworkEntries      []protocol.NetworkEntry `json:"network_entries,omitempty"`
	IncludeStaticAssets bool                    `json:"include_static_assets,omitempty"`
}

type BrowserToHTTPPreviewResponse struct {
	SchemaVersion       string                        `json:"schema_version"`
	GeneratedAt         time.Time                     `json:"generated_at"`
	ReplaySessionID     string                        `json:"replay_session_id,omitempty"`
	SourceBrowserScript *dsl.Script                   `json:"source_browser_script,omitempty"`
	GeneratedScript     *dsl.Script                   `json:"generated_script,omitempty"`
	Warnings            []string                      `json:"warnings,omitempty"`
	Review              BrowserToHTTPReview           `json:"review"`
	ScenarioTemplate    BrowserToHTTPScenarioTemplate `json:"scenario_template"`
}

type BrowserToHTTPReview struct {
	Status                string                   `json:"status"`
	SourceBrowserActions  int                      `json:"source_browser_actions"`
	CapturedRequests      int                      `json:"captured_requests"`
	GeneratedHTTPActions  int                      `json:"generated_http_actions"`
	SkippedStaticAssets   int                      `json:"skipped_static_assets"`
	CorrelationCandidates []BrowserToHTTPCandidate `json:"correlation_candidates,omitempty"`
	DataSourceCandidates  []BrowserToHTTPCandidate `json:"data_source_candidates,omitempty"`
	AuthFindings          []BrowserToHTTPFinding   `json:"auth_findings,omitempty"`
	CookieFindings        []BrowserToHTTPFinding   `json:"cookie_findings,omitempty"`
	BlockingFindings      []BrowserToHTTPFinding   `json:"blocking_findings,omitempty"`
	Findings              []string                 `json:"findings,omitempty"`
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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
	result, err := har.Convert(harData, har.ConvertOptions{
		ScriptName:        firstNonEmpty(req.Name, browserHTTPDraftName(req.SourceBrowserScript)),
		IncludeAssertions: &includeAssertions,
		InferExtractors:   &inferExtractors,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to generate HTTP draft: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(firstNonEmpty(req.ProjectID, sourceScriptProjectID(req.SourceBrowserScript)))
		result.Script.GeneratedBy = "stormrunner-browser-to-http-converter"
		result.Script.GeneratedFrom = "browser_replay"
		attachBrowserToHTTPMetadata(result.Script, req, len(entries))
	}
	warnings := append([]string(nil), result.Warnings...)
	warnings = append(warnings, validateScript(result.Script)...)
	review := buildBrowserToHTTPReview(req.SourceBrowserScript, result.Script, entries, skippedStatic)
	response := BrowserToHTTPPreviewResponse{
		SchemaVersion:       browserToHTTPSchemaVersion,
		GeneratedAt:         time.Now().UTC(),
		ReplaySessionID:     req.ReplaySessionID,
		SourceBrowserScript: req.SourceBrowserScript,
		GeneratedScript:     result.Script,
		Warnings:            uniqueSortedStrings(warnings),
		Review:              review,
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
				"name":    "stormrunner-browser-to-http",
				"version": "1.0.0",
			},
			"entries": harEntries,
		},
	}
	return json.Marshal(payload)
}

func buildBrowserToHTTPReview(source *dsl.Script, generated *dsl.Script, entries []protocol.NetworkEntry, skippedStatic int) BrowserToHTTPReview {
	review := BrowserToHTTPReview{
		Status:               "ready_for_review",
		SourceBrowserActions: countBrowserActions(source),
		CapturedRequests:     len(entries),
		GeneratedHTTPActions: countScriptActions(generated),
		SkippedStaticAssets:  skippedStatic,
		Findings: []string{
			fmt.Sprintf("%d replay network request(s) were converted into an HTTP draft.", len(entries)),
			"Generated HTTP scripts still require validation, replay, correlation review, data-source fit, and approval before density use.",
		},
	}
	review.CorrelationCandidates = browserToHTTPCorrelationCandidates(entries)
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
		review.BlockingFindings = append(review.BlockingFindings, BrowserToHTTPFinding{
			Severity: "blocker",
			Summary:  "No convertible replay network requests were available.",
			Action:   "Run browser replay with network capture enabled, then retry conversion.",
		})
	} else if len(review.CorrelationCandidates) > 0 || len(review.DataSourceCandidates) > 0 {
		review.Status = "needs_repair"
		review.Findings = append(review.Findings, "Dynamic URL values were detected and should be parameterized or correlated before approval.")
	}
	if skippedStatic > 0 {
		review.Findings = append(review.Findings, fmt.Sprintf("%d static browser asset request(s) were skipped for protocol-load focus.", skippedStatic))
	}
	return review
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

func attachBrowserToHTTPMetadata(script *dsl.Script, req BrowserToHTTPPreviewRequest, requestCount int) {
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
			"source_profile":               "vugen_web2_to_http",
			"replay_session_id":            req.ReplaySessionID,
			"captured_network_requests":    requestCount,
			"source_browser_script_id":     sourceScriptID(req.SourceBrowserScript),
			"source_browser_script_name":   sourceScriptName(req.SourceBrowserScript),
			"conversion_requires_approval": true,
		},
	}
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
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "token") || strings.Contains(lowerKey, "session") || strings.Contains(lowerKey, "csrf") || strings.Contains(lowerKey, "nonce") {
		return true
	}
	value = strings.TrimSpace(value)
	if len(value) >= 24 {
		return true
	}
	if strings.Count(value, "-") >= 4 {
		return true
	}
	return false
}

func looksParameterName(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"id", "user", "customer", "account", "order", "sku", "email", "zip"} {
		if lower == marker || strings.HasSuffix(lower, "_"+marker) || strings.Contains(lower, marker+"_") {
			return true
		}
	}
	return false
}
