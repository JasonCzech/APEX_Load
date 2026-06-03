package salesforce

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type AnalyzeOptions struct {
	BuildNative bool
	HARData     []byte
}

type Review struct {
	Detected              bool                   `json:"detected"`
	Domains               []DomainObservation    `json:"domains,omitempty"`
	AuraEndpoints         []EndpointObservation  `json:"aura_endpoints,omitempty"`
	LightningEndpoints    []EndpointObservation  `json:"lightning_endpoints,omitempty"`
	LoginFlow             []EndpointObservation  `json:"login_flow,omitempty"`
	Transactions          []TransactionCandidate `json:"transactions,omitempty"`
	CorrelationConfidence string                 `json:"correlation_confidence,omitempty"`
	HARMatchCoverage      MatchCoverage          `json:"har_match_coverage"`
	Blockers              []string               `json:"blockers,omitempty"`
	NextActions           []string               `json:"next_actions,omitempty"`
	NativePreview         *NativePreview         `json:"native_preview,omitempty"`
}

type DomainObservation struct {
	Host  string `json:"host"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type EndpointObservation struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path"`
	Host   string `json:"host,omitempty"`
	Count  int    `json:"count"`
}

type TransactionCandidate struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	RequestCount int    `json:"request_count"`
	AuraActions  int    `json:"aura_actions,omitempty"`
	UIAPICalls   int    `json:"ui_api_calls,omitempty"`
}

type MatchCoverage struct {
	HARProvided      bool    `json:"har_provided"`
	ImportedRequests int     `json:"imported_requests"`
	MatchedRequests  int     `json:"matched_requests"`
	CoveragePct      float64 `json:"coverage_pct"`
}

type NativePreview struct {
	Script       *dsl.Script            `json:"script,omitempty"`
	Correlations []CorrelationCandidate `json:"correlations,omitempty"`
}

type CorrelationCandidate struct {
	Name       string   `json:"name"`
	Source     string   `json:"source"`
	Expression string   `json:"expression"`
	FoundInSeq int      `json:"found_in_seq"`
	UsedInSeqs []int    `json:"used_in_seqs,omitempty"`
	RuleID     string   `json:"rule_id,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

type actionRef struct {
	Phase       string
	Index       int
	Sequence    int
	Action      *dsl.Action
	URL         string
	Method      string
	Kind        string
	Transaction string
	HARID       int
}

type harEntry struct {
	ID              int
	Method          string
	URL             string
	RequestBody     string
	RequestHeaders  map[string]string
	ResponseBody    string
	ResponseHeaders map[string]string
}

type harLog struct {
	Log struct {
		Entries []struct {
			Request struct {
				Method  string `json:"method"`
				URL     string `json:"url"`
				Headers []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"headers"`
				PostData *struct {
					Text string `json:"text"`
				} `json:"postData,omitempty"`
			} `json:"request"`
			Response struct {
				Headers []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"headers"`
				Content *struct {
					Text string `json:"text"`
				} `json:"content,omitempty"`
			} `json:"response"`
		} `json:"entries"`
	} `json:"log"`
}

func Analyze(script *dsl.Script, opts AnalyzeOptions) (*Review, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	refs := flattenRequestActions(script)
	entries, err := parseHAR(opts.HARData)
	if err != nil {
		return nil, err
	}
	matches := matchHAR(refs, entries)
	for index, harID := range matches {
		refs[index].HARID = harID
	}

	review := &Review{
		Detected:           detectsSalesforce(refs, entries),
		Domains:            domainObservations(refs, entries),
		AuraEndpoints:      endpointObservations(refs, "aura"),
		LightningEndpoints: endpointObservations(refs, "lightning"),
		LoginFlow:          endpointObservations(refs, "login"),
		Transactions:       transactionCandidates(refs),
		HARMatchCoverage: MatchCoverage{
			HARProvided:      len(entries) > 0,
			ImportedRequests: len(refs),
			MatchedRequests:  len(matches),
			CoveragePct:      coveragePct(len(matches), len(refs)),
		},
		NextActions: []string{
			"Validate the exact replay script before using the native Salesforce preview.",
			"Review Salesforce correlation extractors and HAR match coverage before approval.",
			"Replay the native preview against a controlled Salesforce sandbox and attach evidence.",
		},
	}
	if opts.BuildNative && len(entries) == 0 {
		review.Blockers = append(review.Blockers, "Salesforce native preview requires a HAR captured from the same recording for high-confidence correlation.")
	}
	if review.Detected && len(review.AuraEndpoints) == 0 {
		review.Blockers = append(review.Blockers, "No Salesforce Aura endpoints were detected; validate that the capture includes Lightning transaction traffic.")
	}

	correlations := detectCorrelations(entries)
	review.CorrelationConfidence = correlationConfidence(correlations, len(entries))
	if opts.BuildNative {
		preview, err := buildNativePreview(script, refs, entries, matches, correlations)
		if err != nil {
			return nil, err
		}
		review.NativePreview = preview
	}
	return review, nil
}

func flattenRequestActions(script *dsl.Script) []actionRef {
	refs := make([]actionRef, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	seq := 1
	appendRefs := func(phase string, actions []dsl.Action) {
		for index := range actions {
			action := &actions[index]
			method := strings.ToUpper(strings.TrimSpace(action.Method))
			if method == "" {
				method = strings.ToUpper(strings.TrimSpace(action.Type))
			}
			if method == "" || strings.TrimSpace(action.Target) == "" || isEngineAction(action.Type) {
				continue
			}
			resolved := resolveActionURL(script.Config.BaseURL, action.Target)
			refs = append(refs, actionRef{
				Phase:       phase,
				Index:       index,
				Sequence:    seq,
				Action:      action,
				URL:         resolved,
				Method:      method,
				Kind:        classifySalesforceAction(action, resolved),
				Transaction: metadataString(action.Metadata, "loadrunner_transaction"),
			})
			seq++
		}
	}
	appendRefs("init", script.Init)
	appendRefs("action", script.Actions)
	appendRefs("teardown", script.Teardown)
	return refs
}

func isEngineAction(actionType string) bool {
	switch actionType {
	case "rendezvous", "error_message", "loadrunner_message", "loadrunner_runtime", "loadrunner_transaction", "loadrunner_timer", "loadrunner_metric", "loadrunner_parameter", "loadrunner_json", "loadrunner_xml", "loadrunner_database", "loadrunner_secret", "loadrunner_external", "loadrunner_review", "custom_call":
		return true
	default:
		return false
	}
}

func resolveActionURL(baseURL, target string) string {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if strings.TrimSpace(baseURL) == "" {
		return target
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

func parseHAR(data []byte) ([]harEntry, error) {
	if len(data) == 0 || strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}
	var parsed harLog
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse Salesforce HAR: %w", err)
	}
	entries := make([]harEntry, 0, len(parsed.Log.Entries))
	for index, entry := range parsed.Log.Entries {
		out := harEntry{
			ID:              index + 1,
			Method:          strings.ToUpper(strings.TrimSpace(entry.Request.Method)),
			URL:             strings.TrimSpace(entry.Request.URL),
			RequestHeaders:  headerMap(entry.Request.Headers),
			ResponseHeaders: headerMap(entry.Response.Headers),
		}
		if entry.Request.PostData != nil {
			out.RequestBody = entry.Request.PostData.Text
		}
		if entry.Response.Content != nil {
			out.ResponseBody = entry.Response.Content.Text
		}
		entries = append(entries, out)
	}
	return entries, nil
}

func headerMap(headers []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}) map[string]string {
	out := make(map[string]string, len(headers))
	for _, header := range headers {
		if strings.TrimSpace(header.Name) == "" {
			continue
		}
		out[header.Name] = header.Value
	}
	return out
}

func matchHAR(refs []actionRef, entries []harEntry) map[int]int {
	matches := make(map[int]int)
	used := make(map[int]bool)
	for refIndex, ref := range refs {
		for _, entry := range entries {
			if used[entry.ID] {
				continue
			}
			if !strings.EqualFold(ref.Method, entry.Method) {
				continue
			}
			if equivalentURL(ref.URL, entry.URL) {
				matches[refIndex] = entry.ID
				used[entry.ID] = true
				break
			}
		}
	}
	return matches
}

func equivalentURL(left, right string) bool {
	if strings.TrimSpace(left) == strings.TrimSpace(right) {
		return true
	}
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Host, rightURL.Host) &&
		leftURL.EscapedPath() == rightURL.EscapedPath() &&
		leftURL.RawQuery == rightURL.RawQuery
}

func detectsSalesforce(refs []actionRef, entries []harEntry) bool {
	for _, ref := range refs {
		if salesforceHost(hostOf(ref.URL)) != "" || ref.Kind != "generic" {
			return true
		}
	}
	for _, entry := range entries {
		if salesforceHost(hostOf(entry.URL)) != "" {
			return true
		}
	}
	return false
}

func classifySalesforceAction(action *dsl.Action, rawURL string) string {
	parsed, _ := url.Parse(rawURL)
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.EscapedPath())
	body := strings.ToLower(action.Body)
	switch {
	case strings.Contains(host, "test.salesforce.com") || strings.Contains(host, "mobileauth.salesforce.com") || strings.Contains(path, "login") || strings.Contains(path, "oauth"):
		return "login"
	case strings.Contains(path, "/aura") || strings.Contains(body, "aura.context") || strings.Contains(body, "aura.token") || strings.Contains(body, "\"descriptor\""):
		return "aura"
	case strings.Contains(path, "/services/data/") && strings.Contains(path, "/ui-api/"):
		return "ui_api"
	case strings.Contains(path, "/services/data/"):
		return "rest_api"
	case strings.Contains(host, "lightning.force.com"):
		return "lightning"
	default:
		return "generic"
	}
}

func salesforceHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case strings.Contains(host, ".my.salesforce.com"):
		return "my_salesforce"
	case strings.Contains(host, ".lightning.force.com"):
		return "lightning"
	case host == "test.salesforce.com":
		return "login"
	case host == "mobileauth.salesforce.com":
		return "mobile_auth"
	default:
		return ""
	}
}

func domainObservations(refs []actionRef, entries []harEntry) []DomainObservation {
	counts := map[string]map[string]int{}
	add := func(rawURL string) {
		host := hostOf(rawURL)
		kind := salesforceHost(host)
		if host == "" || kind == "" {
			return
		}
		if counts[host] == nil {
			counts[host] = map[string]int{"count": 0}
			counts[host]["kind:"+kind] = 1
		}
		counts[host]["count"]++
	}
	for _, ref := range refs {
		add(ref.URL)
	}
	for _, entry := range entries {
		add(entry.URL)
	}
	out := make([]DomainObservation, 0, len(counts))
	for host, values := range counts {
		kind := ""
		for key := range values {
			if strings.HasPrefix(key, "kind:") {
				kind = strings.TrimPrefix(key, "kind:")
				break
			}
		}
		out = append(out, DomainObservation{Host: host, Kind: kind, Count: values["count"]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Host < out[j].Host
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func endpointObservations(refs []actionRef, kind string) []EndpointObservation {
	counts := map[string]EndpointObservation{}
	for _, ref := range refs {
		if ref.Kind != kind {
			continue
		}
		parsed, _ := url.Parse(ref.URL)
		key := ref.Method + " " + parsed.Host + " " + parsed.EscapedPath()
		current := counts[key]
		current.Method = ref.Method
		current.Host = parsed.Host
		current.Path = firstNonEmpty(parsed.EscapedPath(), "/")
		current.Count++
		counts[key] = current
	}
	out := make([]EndpointObservation, 0, len(counts))
	for _, item := range counts {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Path < out[j].Path
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func transactionCandidates(refs []actionRef) []TransactionCandidate {
	counts := map[string]TransactionCandidate{}
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Transaction)
		source := "loadrunner_transaction"
		if name == "" {
			name = ref.Kind
			source = "salesforce_classifier"
		}
		if name == "" || name == "generic" {
			name = "Salesforce HTTP Flow"
		}
		current := counts[source+":"+name]
		current.Name = name
		current.Source = source
		current.RequestCount++
		if ref.Kind == "aura" {
			current.AuraActions++
		}
		if ref.Kind == "ui_api" {
			current.UIAPICalls++
		}
		counts[source+":"+name] = current
	}
	out := make([]TransactionCandidate, 0, len(counts))
	for _, item := range counts {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestCount == out[j].RequestCount {
			return out[i].Name < out[j].Name
		}
		return out[i].RequestCount > out[j].RequestCount
	})
	return out
}

func buildNativePreview(script *dsl.Script, refs []actionRef, entries []harEntry, matches map[int]int, correlations []correlate.Correlation) (*NativePreview, error) {
	clone, err := cloneScript(script)
	if err != nil {
		return nil, err
	}
	clone.Name = strings.TrimSpace(script.Name) + " Salesforce Native Preview"
	clone.GeneratedBy = "apexload-salesforce-builder"
	clone.GeneratedFrom = "loadrunner+har"
	ensureMetadata(clone)["salesforce_native_preview"] = true

	cloneRefs := flattenRequestActions(clone)
	harByID := make(map[int]harEntry, len(entries))
	for _, entry := range entries {
		harByID[entry.ID] = entry
	}
	harIDToRef := make(map[int]*actionRef)
	for refIndex, harID := range matches {
		if refIndex >= len(cloneRefs) {
			continue
		}
		cloneRefs[refIndex].HARID = harID
		harIDToRef[harID] = &cloneRefs[refIndex]
	}

	for index := range cloneRefs {
		ref := &cloneRefs[index]
		if ref.Action.Metadata == nil {
			ref.Action.Metadata = map[string]any{}
		}
		ref.Action.Metadata["salesforce"] = map[string]any{
			"classification": ref.Kind,
			"sequence":       ref.Sequence,
			"har_match_id":   ref.HARID,
		}
		if ref.Kind == "aura" {
			addAssertionIfMissing(ref.Action, dsl.Assertion{Type: "body_not_contains", Value: `"state":"ERROR"`})
			addAssertionIfMissing(ref.Action, dsl.Assertion{Type: "body_not_contains", Value: `"state":"INCOMPLETE"`})
		}
	}

	vars := correlate.BuildVarMap(correlations)
	for _, corr := range correlations {
		foundRef := harIDToRef[corr.FoundInID]
		if foundRef == nil || foundRef.Action == nil {
			continue
		}
		addExtractorIfMissing(foundRef.Action, dsl.Extractor{
			Name:       corr.Name,
			Source:     corr.Source,
			Type:       corr.ExtractType,
			Expression: corr.Expression,
		})
	}
	for index := range cloneRefs {
		action := cloneRefs[index].Action
		action.Target = correlate.ApplyVars(action.Target, vars)
		action.Body = correlate.ApplyVars(action.Body, vars)
		action.Headers = correlate.ApplyVarsToHeaders(action.Headers, vars)
	}

	return &NativePreview{
		Script:       clone,
		Correlations: correlationSummaries(correlations),
	}, nil
}

func cloneScript(script *dsl.Script) (*dsl.Script, error) {
	data, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var clone dsl.Script
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

func ensureMetadata(script *dsl.Script) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "salesforce"}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	return script.Metadata.Import.Preserved
}

func detectCorrelations(entries []harEntry) []correlate.Correlation {
	observations := make([]correlate.Observation, 0, len(entries))
	for _, entry := range entries {
		observations = append(observations, correlate.Observation{
			ID:              entry.ID,
			RequestURL:      entry.URL,
			RequestBody:     entry.RequestBody,
			RequestHeaders:  entry.RequestHeaders,
			ResponseBody:    entry.ResponseBody,
			ResponseHeaders: entry.ResponseHeaders,
		})
	}
	return correlate.Detect(observations)
}

func correlationSummaries(correlations []correlate.Correlation) []CorrelationCandidate {
	out := make([]CorrelationCandidate, 0, len(correlations))
	for _, corr := range correlations {
		out = append(out, CorrelationCandidate{
			Name:       corr.Name,
			Source:     corr.Source,
			Expression: corr.Expression,
			FoundInSeq: corr.FoundInID,
			UsedInSeqs: append([]int(nil), corr.UsedInIDs...),
			RuleID:     corr.RuleID,
			Confidence: corr.Confidence,
			Reasons:    append([]string(nil), corr.Reasons...),
		})
	}
	return out
}

func correlationConfidence(correlations []correlate.Correlation, harEntries int) string {
	if len(correlations) == 0 {
		if harEntries == 0 {
			return "missing_har"
		}
		return "none_detected"
	}
	total := 0.0
	for _, corr := range correlations {
		total += corr.Confidence
	}
	avg := total / float64(len(correlations))
	switch {
	case avg >= 0.75:
		return "high"
	case avg >= 0.5:
		return "medium"
	default:
		return "low"
	}
}

func addExtractorIfMissing(action *dsl.Action, extractor dsl.Extractor) {
	for _, existing := range action.Extractors {
		if existing.Name == extractor.Name {
			return
		}
	}
	action.Extractors = append(action.Extractors, extractor)
}

func addAssertionIfMissing(action *dsl.Action, assertion dsl.Assertion) {
	for _, existing := range action.Assertions {
		if existing.Type == assertion.Type && existing.Value == assertion.Value {
			return
		}
	}
	action.Assertions = append(action.Assertions, assertion)
}

func hostOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func coveragePct(matches, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(matches) * 100 / float64(total)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
