package salesforce

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type AnalyzeOptions struct {
	BuildNative bool
	HARData     []byte
}

type Review struct {
	Detected              bool                        `json:"detected"`
	Domains               []DomainObservation         `json:"domains,omitempty"`
	AuraEndpoints         []EndpointObservation       `json:"aura_endpoints,omitempty"`
	LightningEndpoints    []EndpointObservation       `json:"lightning_endpoints,omitempty"`
	LoginFlow             []EndpointObservation       `json:"login_flow,omitempty"`
	UIAPIEndpoints        []EndpointObservation       `json:"ui_api_endpoints,omitempty"`
	RestEndpoints         []EndpointObservation       `json:"rest_endpoints,omitempty"`
	ApexRestEndpoints     []EndpointObservation       `json:"apex_rest_endpoints,omitempty"`
	CompositeEndpoints    []EndpointObservation       `json:"composite_endpoints,omitempty"`
	GraphQLEndpoints      []EndpointObservation       `json:"graphql_endpoints,omitempty"`
	BulkEndpoints         []EndpointObservation       `json:"bulk_endpoints,omitempty"`
	AgentforceEndpoints   []EndpointObservation       `json:"agentforce_endpoints,omitempty"`
	Transactions          []TransactionCandidate      `json:"transactions,omitempty"`
	Parameterizations     []ParameterizationCandidate `json:"parameterizations,omitempty"`
	CorrelationConfidence string                      `json:"correlation_confidence,omitempty"`
	HARMatchCoverage      MatchCoverage               `json:"har_match_coverage"`
	Blockers              []string                    `json:"blockers,omitempty"`
	NextActions           []string                    `json:"next_actions,omitempty"`
	NativePreview         *NativePreview              `json:"native_preview,omitempty"`
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
	Name         string   `json:"name"`
	Source       string   `json:"source"`
	RequestCount int      `json:"request_count"`
	AuraActions  int      `json:"aura_actions,omitempty"`
	UIAPICalls   int      `json:"ui_api_calls,omitempty"`
	FlowKinds    []string `json:"flow_kinds,omitempty"`
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

// ParameterizationCandidate is a record/object identifier discovered inside an
// Aura `message` request payload that a load test should likely drive from a
// data source (one identifier per VUser) rather than replay as a fixed literal.
// These are surfaced for review and are not auto-applied; the value comes from a
// user-supplied dataset via the parameterization-review flow.
type ParameterizationCandidate struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ActionSeq   int    `json:"action_seq,omitempty"`
	ParamKey    string `json:"param_key"`
	SampleValue string `json:"sample_value,omitempty"`
	Kind        string `json:"kind"`
	Column      string `json:"column"`
	Template    string `json:"template"`
}

type actionRef struct {
	Phase       string
	Index       int
	Sequence    int
	Action      *dsl.Action
	URL         string
	Method      string
	Kind        string
	FlowKind    string
	Descriptor  string
	Object      string
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

var (
	auraDescriptorPattern = regexp.MustCompile(`(?i)"descriptor"\s*:\s*"([^"]+)"`)
	jmesIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// salesforceIDPattern matches a canonical 15- or 18-character Salesforce
	// record/object identifier.
	salesforceIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{15}(?:[A-Za-z0-9]{3})?$`)
	// idLikeValuePattern matches identifier-shaped values for keys that end in
	// "id" but are not full Salesforce IDs (e.g. external ids).
	idLikeValuePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{6,}$`)
)

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
		Detected:            detectsSalesforce(refs, entries),
		Domains:             domainObservations(refs, entries),
		AuraEndpoints:       endpointObservations(refs, "aura"),
		LightningEndpoints:  endpointObservations(refs, "lightning"),
		LoginFlow:           endpointObservations(refs, "login"),
		UIAPIEndpoints:      endpointObservations(refs, "ui_api"),
		RestEndpoints:       endpointObservations(refs, "rest_api"),
		ApexRestEndpoints:   endpointObservations(refs, "apex_rest"),
		CompositeEndpoints:  endpointObservations(refs, "composite"),
		GraphQLEndpoints:    endpointObservations(refs, "graphql"),
		BulkEndpoints:       endpointObservations(refs, "bulk_data"),
		AgentforceEndpoints: endpointObservations(refs, "agentforce"),
		Transactions:        transactionCandidates(refs),
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
	if opts.BuildNative && len(entries) > 0 && review.HARMatchCoverage.CoveragePct < 80 {
		review.Blockers = append(review.Blockers, "Salesforce native preview HAR coverage is below 80%; attach a HAR from the same recording that covers the missing imported requests.")
	}
	if review.Detected && !hasRecognizedSalesforceEndpoints(review) {
		review.Blockers = append(review.Blockers, "No Salesforce Aura or API endpoints were detected; validate that the capture includes the Salesforce transaction traffic.")
	}

	correlations := detectCorrelations(entries)
	review.CorrelationConfidence = correlationConfidence(correlations, len(entries))
	review.Parameterizations = auraMessageParameterizations(refs, correlations)
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
	appendRef := func(phase string, index int, action *dsl.Action, inheritedTransaction string) {
		method := strings.ToUpper(strings.TrimSpace(action.Method))
		if method == "" {
			method = strings.ToUpper(strings.TrimSpace(action.Type))
		}
		if method == "" || strings.TrimSpace(action.Target) == "" || isEngineAction(action.Type) {
			return
		}
		resolved := resolveActionURL(script.Config.BaseURL, action.Target)
		evidence := salesforceActionEvidence(action, resolved)
		descriptor := extractAuraDescriptor(evidence)
		refs = append(refs, actionRef{
			Phase:       phase,
			Index:       index,
			Sequence:    seq,
			Action:      action,
			URL:         resolved,
			Method:      method,
			Kind:        classifySalesforceAction(action, resolved),
			FlowKind:    classifySalesforceFlow(evidence, descriptor),
			Descriptor:  descriptor,
			Object:      classifySalesforceObject(evidence, descriptor),
			Transaction: convertutil.FirstNonEmpty(metadataString(action.Metadata, "source_transaction"), metadataString(action.Metadata, "source_transaction"), inheritedTransaction),
		})
		seq++
	}
	appendRefs := func(phase string, actions []dsl.Action) {
		for index := range actions {
			action := &actions[index]
			if strings.EqualFold(action.Type, "http_sequence") {
				transaction := convertutil.FirstNonEmpty(metadataString(action.Metadata, "source_transaction"), metadataString(action.Metadata, "source_transaction"))
				children := salesforceSequenceChildren(*action)
				for childIndex := range children {
					child := children[childIndex]
					if child.Metadata == nil {
						child.Metadata = map[string]any{}
					}
					if child.Metadata["source_transaction"] == nil && transaction != "" {
						child.Metadata["source_transaction"] = transaction
					}
					appendRef(phase, index, &child, transaction)
				}
				continue
			}
			appendRef(phase, index, action, "")
		}
	}
	appendRefs("init", script.Init)
	appendRefs("action", script.Actions)
	appendRefs("teardown", script.Teardown)
	return refs
}

func isEngineAction(actionType string) bool {
	switch actionType {
	case "rendezvous", "error_message", "source_message", "source_runtime", "source_transaction", "source_timer", "source_metric", "source_parameter", "source_json", "source_xml", "source_database", "source_secret", "source_external", "source_review", "custom_call":
		return true
	default:
		return false
	}
}

func salesforceSequenceChildren(action dsl.Action) []dsl.Action {
	if action.Params == nil {
		return nil
	}
	rawRequests, ok := action.Params["requests"]
	if !ok {
		return nil
	}
	var values []any
	switch typed := rawRequests.(type) {
	case []any:
		values = typed
	case []map[string]any:
		values = make([]any, len(typed))
		for index := range typed {
			values[index] = typed[index]
		}
	case []dsl.Action:
		return append([]dsl.Action(nil), typed...)
	default:
		return nil
	}
	children := make([]dsl.Action, 0, len(values))
	for _, value := range values {
		payload, err := json.Marshal(value)
		if err != nil {
			continue
		}
		var child dsl.Action
		if err := json.Unmarshal(payload, &child); err == nil && strings.TrimSpace(child.Target) != "" {
			children = append(children, child)
		}
	}
	return children
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
		bestID := 0
		bestScore := -1
		for _, entry := range entries {
			if used[entry.ID] {
				continue
			}
			if !strings.EqualFold(ref.Method, entry.Method) {
				continue
			}
			if !equivalentURL(ref.URL, entry.URL) {
				continue
			}
			if score := harMatchScore(ref, entry); score > bestScore {
				bestScore = score
				bestID = entry.ID
			}
		}
		if bestID != 0 {
			matches[refIndex] = bestID
			used[bestID] = true
		}
	}
	return matches
}

func harMatchScore(ref actionRef, entry harEntry) int {
	score := 1
	actionBody := ""
	if ref.Action != nil {
		actionBody = ref.Action.Body
	}
	refBody := canonicalRequestBody(actionBody)
	entryBody := canonicalRequestBody(entry.RequestBody)
	if refBody != "" || entryBody != "" {
		if refBody != "" && refBody == entryBody {
			score += 100
		}
	}
	if ref.Descriptor != "" && strings.Contains(canonicalRequestBody(entry.RequestBody), strings.ToLower(ref.Descriptor)) {
		score += 25
	}
	if ref.Kind != "" && ref.Kind == classifyHARRequest(entry) {
		score += 10
	}
	return score
}

func classifyHARRequest(entry harEntry) string {
	action := &dsl.Action{Method: entry.Method, Target: entry.URL, Body: entry.RequestBody}
	return classifySalesforceAction(action, entry.URL)
}

func canonicalRequestBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if values, err := url.ParseQuery(body); err == nil && len(values) > 0 && strings.Contains(body, "=") {
		return strings.ToLower(values.Encode())
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(body)); err == nil {
		return strings.ToLower(compact.String())
	}
	if decoded, err := url.QueryUnescape(body); err == nil && decoded != body {
		return strings.ToLower(strings.TrimSpace(decoded))
	}
	return strings.ToLower(body)
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
	// url.Parse returns (nil, err) for inputs with spaces, control bytes, or bad
	// percent-encoding — which an imported (untrusted) action.Target can contain.
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil {
		return "generic"
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.EscapedPath())
	body := strings.ToLower(action.Body)
	switch {
	// Concrete API surfaces are matched before the login heuristic so that data
	// or Apex resources whose names merely contain "login"/"oauth" (e.g. the
	// standard LoginHistory object, a Login_Event__c, or a LoginController Apex
	// class) are not misclassified as auth traffic.
	case strings.Contains(path, "/einstein/ai-agent"):
		return "agentforce"
	case strings.Contains(path, "/aura") || strings.Contains(body, "aura.context") || strings.Contains(body, "aura.token") || strings.Contains(body, "\"descriptor\""):
		return "aura"
	case strings.Contains(path, "/services/apexrest/"):
		return "apex_rest"
	case strings.Contains(path, "/services/data/") && strings.Contains(path, "/ui-api/"):
		return "ui_api"
	case strings.Contains(path, "/services/data/") && strings.Contains(path, "/graphql"):
		return "graphql"
	case strings.Contains(path, "/services/data/") && strings.Contains(path, "/composite"):
		return "composite"
	case strings.Contains(path, "/services/data/") && (strings.Contains(path, "/jobs/ingest") || strings.Contains(path, "/jobs/query")):
		return "bulk_data"
	case strings.Contains(path, "/services/data/"):
		return "rest_api"
	case isSalesforceLoginRequest(host, path):
		return "login"
	case strings.Contains(host, "lightning.force.com"):
		return "lightning"
	default:
		return "generic"
	}
}

// isSalesforceLoginRequest matches genuine Salesforce authentication traffic by
// host and anchored auth paths, rather than any path that merely contains the
// substring "login" or "oauth".
func isSalesforceLoginRequest(host, path string) bool {
	if host == "test.salesforce.com" || host == "login.salesforce.com" || strings.Contains(host, "mobileauth.salesforce.com") {
		return true
	}
	return strings.Contains(path, "/services/oauth2/") ||
		strings.Contains(path, "frontdoor.jsp") ||
		strings.Contains(path, "/secur/login") ||
		strings.Contains(path, "/.well-known/openid") ||
		strings.HasPrefix(path, "/login")
}

func salesforceActionEvidence(action *dsl.Action, rawURL string) string {
	values := []string{rawURL}
	if action != nil {
		values = append(values, action.Name, action.Target, action.Body)
		for key, value := range action.Metadata {
			values = append(values, key)
			switch typed := value.(type) {
			case string:
				values = append(values, typed)
			case fmt.Stringer:
				values = append(values, typed.String())
			}
		}
	}
	joined := strings.Join(values, " ")
	if decoded, err := url.QueryUnescape(joined); err == nil && decoded != "" {
		joined += " " + decoded
	}
	return joined
}

func extractAuraDescriptor(evidence string) string {
	for _, match := range auraDescriptorPattern.FindAllStringSubmatch(evidence, -1) {
		if len(match) >= 2 && strings.TrimSpace(match[1]) != "" {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func classifySalesforceFlow(evidence, descriptor string) string {
	lower := strings.ToLower(evidence + " " + descriptor)
	switch {
	case containsAny(lower, "/einstein/ai-agent", "ai-agent", "agentforce", "copilot"):
		return "agent_conversation"
	// Endpoint/path-anchored API shapes are evaluated before the object-name
	// heuristics below: the request body almost always also mentions a business
	// object (Account/Contact/...), so checking object names first would steal
	// composite/bulk/apex/file/list requests away from their correct flow kind.
	case containsAny(lower, "/jobs/ingest", "/jobs/query", "/services/async/", "bulkapi"):
		return "bulk_data"
	case containsAny(lower, "/composite/graph", "/composite/batch", "/composite"):
		return "composite"
	case containsAny(lower, "/services/apexrest/"):
		return "apex_custom_api"
	case containsAny(lower, "listview", "list-records", "list-ui", "list_view", "list view"):
		return "list_view"
	case containsAny(lower, "contentversion", "contentdocument", ".content.force.com", ".file.force.com", "/sobjects/contentversion"):
		return "file_content"
	// Object/business-name heuristics.
	case containsAny(lower, "workorder", "work_order", "work order", "serviceappointment", "service appointment"):
		return "work_order"
	case containsAny(lower, "serviceaddress", "service address", "serviceavailability", "service availability", "getservices", "productoffer", "product offer", "rateplan", "rate plan", "cartreview"):
		return "service"
	case containsAny(lower, "account", "contact", "customer", "billing", "createguestrecords"):
		return "account"
	case containsAny(lower, "recordui", "recordwithfields", "recordid", "/ui-api/record", "/sobjects/"):
		return "record"
	case containsAny(lower, "submitorder", "checkout", "cartcheckout", "completeinteraction", "interaction"):
		return "order"
	case descriptor != "":
		return "apex_action"
	default:
		return ""
	}
}

func classifySalesforceObject(evidence, descriptor string) string {
	combined := evidence + " " + descriptor
	candidates := []struct {
		token  string
		object string
	}{
		{"WorkOrder", "WorkOrder"},
		{"ServiceAppointment", "ServiceAppointment"},
		{"ServiceAddress", "ServiceAddress"},
		{"Account", "Account"},
		{"Contact", "Contact"},
		{"Order", "Order"},
		{"Cart", "Cart"},
		{"Interaction", "Interaction"},
		{"Product", "Product"},
		{"RecordUi", "Record"},
		{"Record", "Record"},
	}
	lower := strings.ToLower(combined)
	for _, candidate := range candidates {
		if strings.Contains(lower, strings.ToLower(candidate.token)) {
			return candidate.object
		}
	}
	return ""
}

func salesforceHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case host == "api.salesforce.com":
		return "agentforce"
	case host == "api.gov.salesforce.com":
		return "agentforce_gov"
	case strings.Contains(host, ".sandbox.my.salesforce.com"):
		return "sandbox"
	case strings.Contains(host, ".my.salesforce.com"):
		return "my_salesforce"
	case strings.Contains(host, ".content.force.com") || strings.Contains(host, ".file.force.com") || strings.Contains(host, ".vf.force.com"):
		return "content"
	case strings.Contains(host, ".lightning.force.com"):
		return "lightning"
	case host == "login.salesforce.com" || host == "test.salesforce.com":
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

// hasRecognizedSalesforceEndpoints reports whether the review captured any
// recognized Salesforce surface (Lightning UI or API). It gates the
// missing-traffic blocker so API-only imports (REST/Composite/Agentforce) are
// not flagged for lacking Aura/Lightning UI traffic.
func hasRecognizedSalesforceEndpoints(review *Review) bool {
	return len(review.AuraEndpoints) > 0 ||
		len(review.LightningEndpoints) > 0 ||
		len(review.UIAPIEndpoints) > 0 ||
		len(review.RestEndpoints) > 0 ||
		len(review.ApexRestEndpoints) > 0 ||
		len(review.CompositeEndpoints) > 0 ||
		len(review.GraphQLEndpoints) > 0 ||
		len(review.BulkEndpoints) > 0 ||
		len(review.AgentforceEndpoints) > 0
}

func endpointObservations(refs []actionRef, kind string) []EndpointObservation {
	counts := map[string]EndpointObservation{}
	for _, ref := range refs {
		if ref.Kind != kind {
			continue
		}
		parsed, err := url.Parse(ref.URL)
		if err != nil || parsed == nil {
			continue
		}
		key := ref.Method + " " + parsed.Host + " " + parsed.EscapedPath()
		current := counts[key]
		current.Method = ref.Method
		current.Host = parsed.Host
		current.Path = convertutil.FirstNonEmpty(parsed.EscapedPath(), "/")
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
	type candidateAccumulator struct {
		candidate TransactionCandidate
		flowKinds map[string]bool
	}
	counts := map[string]*candidateAccumulator{}
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Transaction)
		source := "source_transaction"
		if name == "" {
			name = ref.Kind
			source = "salesforce_classifier"
		}
		if name == "" || name == "generic" {
			name = "Salesforce HTTP Flow"
		}
		key := source + ":" + name
		current := counts[key]
		if current == nil {
			current = &candidateAccumulator{flowKinds: map[string]bool{}}
			counts[key] = current
		}
		current.candidate.Name = name
		current.candidate.Source = source
		current.candidate.RequestCount++
		if ref.Kind == "aura" {
			current.candidate.AuraActions++
		}
		if ref.Kind == "ui_api" {
			current.candidate.UIAPICalls++
		}
		if ref.FlowKind != "" {
			current.flowKinds[ref.FlowKind] = true
		}
	}
	out := make([]TransactionCandidate, 0, len(counts))
	for _, item := range counts {
		flowKinds := make([]string, 0, len(item.flowKinds))
		for flowKind := range item.flowKinds {
			flowKinds = append(flowKinds, flowKind)
		}
		sort.Strings(flowKinds)
		item.candidate.FlowKinds = flowKinds
		out = append(out, item.candidate)
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
	clone.GeneratedFrom = "enterprise_script_archive+har"
	ensureMetadata(clone)["salesforce_native_preview"] = true

	cloneRefs := flattenRequestActions(clone)
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
			"classification":  ref.Kind,
			"flow_kind":       ref.FlowKind,
			"object":          ref.Object,
			"aura_descriptor": ref.Descriptor,
			"sequence":        ref.Sequence,
			"har_match_id":    ref.HARID,
		}
		if ref.Kind == "aura" {
			addAssertionIfMissing(ref.Action, dsl.Assertion{Type: "body_not_contains", Value: `"state":"ERROR"`})
			addAssertionIfMissing(ref.Action, dsl.Assertion{Type: "body_not_contains", Value: `"state":"INCOMPLETE"`})
		}
	}

	vars := correlate.BuildVarMap(correlations)
	encodedValues := make(map[string]bool)
	for _, corr := range correlations {
		foundRef := harIDToRef[corr.FoundInID]
		if foundRef == nil || foundRef.Action == nil {
			continue
		}
		usage := correlationUsage(corr, harIDToRef)
		value := correlationValue(corr)
		base := dsl.Extractor{
			Name:       corr.Name,
			Source:     corr.Source,
			Type:       corr.ExtractType,
			Expression: corr.Expression,
		}
		switch {
		case usage.encoded && !usage.raw:
			// Value is only ever reused percent-encoded (e.g. aura.token inside a
			// form-urlencoded /aura body): extract it pre-encoded and substitute the
			// encoded occurrences with the variable.
			base.Convert = "url_encode"
			addExtractorIfMissing(foundRef.Action, base)
			if value != "" {
				encodedValues[value] = true
			}
		case usage.encoded && usage.raw:
			// Value is reused BOTH raw (e.g. a JSON body/header) AND percent-encoded
			// (a form body). A single variable can only resolve one way, so emit a raw
			// extractor for the raw occurrences plus a url_encode sibling, and route
			// the percent-encoded literals to the sibling variable.
			addExtractorIfMissing(foundRef.Action, base)
			encodedName := corr.Name + "_encoded"
			addExtractorIfMissing(foundRef.Action, dsl.Extractor{
				Name:       encodedName,
				Source:     corr.Source,
				Type:       corr.ExtractType,
				Expression: corr.Expression,
				Convert:    "url_encode",
			})
			if value != "" {
				for _, encoded := range encodedFormsOf(value) {
					vars[encoded] = "{{var." + encodedName + "}}"
				}
			}
		default:
			// Value reused only raw (or not reused in a correlated surface): a single
			// raw extractor is sufficient.
			addExtractorIfMissing(foundRef.Action, base)
		}
	}
	for index := range cloneRefs {
		action := cloneRefs[index].Action
		action.Target = correlate.ApplyVarsWithEncodedValues(action.Target, vars, encodedValues)
		action.Body = correlate.ApplyVarsWithEncodedValues(action.Body, vars, encodedValues)
		action.Headers = correlate.ApplyVarsToHeadersWithEncodedValues(action.Headers, vars, encodedValues)
	}

	synthesizeBulkPollLoops(cloneRefs)

	return &NativePreview{
		Script:       clone,
		Correlations: correlationSummaries(correlations),
	}, nil
}

// Bulk API 2.0 ingest poll-loop synthesis.
const (
	bulkStateVar          = "bulk_job_state"
	bulkPollMaxIterations = 60
	bulkPollIntervalMs    = 2000
	// bulkPollConditionExpr keeps polling while the job is non-terminal. It is
	// null-safe for the first pass: the state var is unset until the loop body's
	// status GET runs, so !has(...) makes the condition true initially and the loop
	// always executes at least once (a bare comparison on an unset var errors and
	// the engine treats an errored condition as false, which would skip the poll).
	bulkPollConditionExpr = `!has(var.bulk_job_state) || (var.bulk_job_state != "JobComplete" && var.bulk_job_state != "Failed" && var.bulk_job_state != "Aborted")`
)

// synthesizeBulkPollLoops rewrites a recorded Bulk API 2.0 ingest status GET into
// a control_loop that polls until the job reaches a terminal state, so the native
// preview waits for completion under load instead of issuing a single recorded
// status check. It only fires when a Bulk ingest job-create request is also
// present (so a stray job-status GET is not wrapped), and it is idempotent.
func synthesizeBulkPollLoops(refs []actionRef) {
	hasCreate := false
	for i := range refs {
		if isBulkIngestCreate(&refs[i]) {
			hasCreate = true
			break
		}
	}
	if !hasCreate {
		return
	}
	for i := range refs {
		if isBulkIngestStatusPoll(&refs[i]) {
			wrapBulkStatusPollInLoop(refs[i].Action)
		}
	}
}

func refMethod(ref *actionRef) string {
	if ref.Method != "" {
		return strings.ToUpper(ref.Method)
	}
	if ref.Action != nil {
		return strings.ToUpper(ref.Action.Method)
	}
	return ""
}

// isBulkIngestCreate matches POST .../jobs/ingest (the job-create call, which has
// no job-id path segment).
func isBulkIngestCreate(ref *actionRef) bool {
	if ref.Kind != "bulk_data" || ref.Action == nil || refMethod(ref) != "POST" {
		return false
	}
	lower := strings.ToLower(ref.Action.Target)
	return strings.Contains(lower, "/jobs/ingest") && !strings.Contains(lower, "/jobs/ingest/")
}

// isBulkIngestStatusPoll matches the bare GET .../jobs/ingest/<id> job-status
// request, excluding the CSV upload (/batches) and result-download endpoints.
func isBulkIngestStatusPoll(ref *actionRef) bool {
	if ref.Kind != "bulk_data" || ref.Action == nil || refMethod(ref) != "GET" {
		return false
	}
	lower := strings.ToLower(ref.Action.Target)
	if !strings.Contains(lower, "/jobs/ingest/") {
		return false
	}
	for _, suffix := range []string{"successfulresults", "failedresults", "unprocessedrecords", "/batches"} {
		if strings.Contains(lower, suffix) {
			return false
		}
	}
	return true
}

// wrapBulkStatusPollInLoop rewrites a job-status GET in place into a control_loop
// that re-issues the GET (with a state extractor and a poll interval) until the
// job reaches a terminal state or the iteration cap is hit.
func wrapBulkStatusPollInLoop(action *dsl.Action) {
	if action == nil || strings.EqualFold(action.Type, "control_loop") {
		return
	}
	inner := *action
	addExtractorIfMissing(&inner, dsl.Extractor{
		Name:       bulkStateVar,
		Source:     "body",
		Type:       "jsonpath",
		Expression: "$.state",
		OnMiss:     "fail",
	})
	if inner.ThinkTime.Type == "" {
		inner.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: bulkPollIntervalMs}
	}
	inner.ThinkTimeAfter = true
	*action = dsl.Action{
		Name: strings.TrimSpace(inner.Name) + " (poll until JobComplete)",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Condition:     &dsl.Condition{Expr: bulkPollConditionExpr},
			Steps:         []dsl.Action{inner},
			MaxIterations: bulkPollMaxIterations,
		},
		Metadata: map[string]any{
			"salesforce": map[string]any{
				"flow_kind":   "bulk_data",
				"synthesized": "bulk_ingest_poll_loop",
			},
		},
	}
}

// correlationUse records how a correlated value is reused across later requests:
// raw (verbatim) and/or percent-encoded (e.g. inside a form-urlencoded body). The
// two are not mutually exclusive — a value can appear raw in one request and
// encoded in another, which requires both a raw and a url_encode extractor.
type correlationUse struct {
	raw     bool
	encoded bool
}

func correlationUsage(corr correlate.Correlation, refs map[int]*actionRef) correlationUse {
	use := correlationUse{}
	value := correlationValue(corr)
	if strings.TrimSpace(value) == "" {
		return use
	}
	encodedForms := encodedFormsOf(value)
	for _, harID := range corr.UsedInIDs {
		ref := refs[harID]
		if ref == nil || ref.Action == nil {
			continue
		}
		for _, candidate := range actionCorrelationSurfaces(ref.Action) {
			if strings.Contains(candidate, value) {
				use.raw = true
			}
			for _, encoded := range encodedForms {
				if strings.Contains(candidate, encoded) {
					use.encoded = true
				}
			}
		}
	}
	return use
}

// correlationValue returns the concrete value a correlation substitutes, mirroring
// correlate.BuildVarMap (Value, falling back to the truncated SampleValue).
func correlationValue(corr correlate.Correlation) string {
	if corr.Value != "" {
		return corr.Value
	}
	return corr.SampleValue
}

// encodedFormsOf returns the distinct percent-encoded representations of value that
// differ from the raw value (query- and path-escaped).
func encodedFormsOf(value string) []string {
	forms := make([]string, 0, 2)
	for _, encoded := range []string{url.QueryEscape(value), url.PathEscape(value)} {
		if encoded == "" || encoded == value {
			continue
		}
		seen := false
		for _, existing := range forms {
			if existing == encoded {
				seen = true
				break
			}
		}
		if !seen {
			forms = append(forms, encoded)
		}
	}
	return forms
}

func actionCorrelationSurfaces(action *dsl.Action) []string {
	if action == nil {
		return nil
	}
	surfaces := []string{action.Target, action.Body}
	for _, value := range action.Headers {
		surfaces = append(surfaces, value)
	}
	return surfaces
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
	seenValues := make(map[string]bool)
	correlations := detectNestedJSONCorrelations(observations, seenValues)
	for _, corr := range correlate.Detect(observations) {
		if seenValues[corr.Value] {
			continue
		}
		seenValues[corr.Value] = true
		correlations = append(correlations, corr)
	}
	sort.SliceStable(correlations, func(i, j int) bool {
		if correlations[i].Confidence != correlations[j].Confidence {
			return correlations[i].Confidence > correlations[j].Confidence
		}
		if correlations[i].FoundInID == correlations[j].FoundInID {
			return correlations[i].Name < correlations[j].Name
		}
		return correlations[i].FoundInID < correlations[j].FoundInID
	})
	return correlations
}

func detectNestedJSONCorrelations(observations []correlate.Observation, seenValues map[string]bool) []correlate.Correlation {
	correlations := make([]correlate.Correlation, 0)
	for index, observation := range observations {
		candidates := nestedJSONCandidates(observation.ResponseBody)
		for _, candidate := range candidates {
			if candidate.Value == "" || seenValues[candidate.Value] {
				continue
			}
			usedIn := make([]int, 0)
			for later := index + 1; later < len(observations); later++ {
				if correlate.ObservationContains(observations[later], candidate.Value) {
					usedIn = append(usedIn, observations[later].ID)
				}
			}
			if len(usedIn) == 0 {
				continue
			}
			seenValues[candidate.Value] = true
			correlations = append(correlations, correlate.Correlation{
				Name:        candidate.Name,
				Source:      candidate.Source,
				Expression:  candidate.Expression,
				ExtractType: candidate.ExtractType,
				FoundInID:   observation.ID,
				UsedInIDs:   usedIn,
				Value:       candidate.Value,
				SampleValue: correlate.Truncate(candidate.Value, 80),
				RuleID:      candidate.RuleID,
				Confidence:  candidate.BaseConfidence,
				Reasons:     []string{candidate.Reason},
			})
		}
	}
	return correlations
}

func nestedJSONCandidates(body string) []correlate.Candidate {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil
	}
	candidates := make([]correlate.Candidate, 0)
	var walk func(value any, path string, key string)
	walk = func(value any, path string, key string) {
		switch typed := value.(type) {
		case map[string]any:
			for childKey, childValue := range typed {
				walk(childValue, appendJMESPath(path, childKey), childKey)
			}
		case []any:
			for index, childValue := range typed {
				walk(childValue, fmt.Sprintf("%s[%d]", path, index), key)
			}
		case string:
			if len(typed) >= 6 && dynamicJSONKey(key) {
				candidates = append(candidates, nestedJSONCandidate(key, typed, path, false))
			}
		case float64:
			if dynamicJSONKey(key) {
				value := strconv.FormatFloat(typed, 'f', -1, 64)
				if len(value) >= 4 {
					candidates = append(candidates, nestedJSONCandidate(key, value, path, true))
				}
			}
		}
	}
	walk(decoded, "", "")
	return candidates
}

func nestedJSONCandidate(key, value, expression string, numeric bool) correlate.Candidate {
	ruleID := correlate.RuleJSONDynamicID
	confidence := 0.62
	reason := "Nested JSON field " + key + " looks like a dynamic identifier"
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "csrf"), strings.Contains(lower, "xsrf"):
		ruleID = correlate.RuleJSONCSRFToken
		confidence = 0.9
	case strings.Contains(lower, "access_token"), strings.Contains(lower, "id_token"), strings.Contains(lower, "refresh_token"), strings.Contains(lower, "token"):
		ruleID = correlate.RuleJSONAuthToken
		confidence = 0.84
	case strings.Contains(lower, "session"), strings.Contains(lower, "sid"):
		ruleID = correlate.RuleJSONSessionID
		confidence = 0.84
	case numeric:
		ruleID = correlate.RuleJSONNumericID
		confidence = 0.52
		reason = "Nested JSON numeric id field " + key
	}
	return correlate.Candidate{
		Name:           "corr_" + correlate.Slug(key),
		Value:          value,
		Source:         "body",
		ExtractType:    "jmespath",
		Expression:     expression,
		RuleID:         ruleID,
		BaseConfidence: confidence,
		Reason:         reason,
	}
}

func appendJMESPath(path, key string) string {
	segment := key
	if !jmesIdentifierPattern.MatchString(key) {
		segment = strconv.Quote(key)
	}
	if path == "" {
		return segment
	}
	return path + "." + segment
}

func dynamicJSONKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	return containsAny(lower, "token", "access_token", "id_token", "refresh_token", "session", "sessionid", "sid", "csrf", "xsrf", "auth", "nonce", "state", "recordid", "record_id", "interactionid", "interaction_id", "workorderid", "work_order_id", "accountid", "account_id", "contactid", "contact_id", "orderid", "order_id", "cartid", "cart_id", "id")
}

// auraMessageParameterizations cracks open the Aura `message` envelope of each
// /aura request and proposes data-source parameterization for record/object
// identifiers carried in action params. Values that are already correlated
// (produced by an earlier response) are skipped — those are handled
// deterministically by the correlation pass instead.
func auraMessageParameterizations(refs []actionRef, correlations []correlate.Correlation) []ParameterizationCandidate {
	correlated := make(map[string]bool, len(correlations))
	for _, corr := range correlations {
		if corr.Value != "" {
			correlated[corr.Value] = true
		}
	}
	seenValues := map[string]bool{}
	usedNames := map[string]bool{}
	out := make([]ParameterizationCandidate, 0)
	for _, ref := range refs {
		if ref.Action == nil {
			continue
		}
		message := auraMessageBody(ref.Action.Body)
		if message == "" {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(message), &decoded); err != nil {
			continue
		}
		actions, ok := auraMessageActions(decoded)
		if !ok {
			continue
		}
		for actionIndex, actionValue := range actions {
			params, ok := auraActionParams(actionValue)
			if !ok {
				continue
			}
			base := fmt.Sprintf("actions[%d].params", actionIndex)
			walkAuraParams(params, base, "", func(key, value, expression string) {
				kind, ok := auraParamIdentifierKind(key, value)
				if !ok || correlated[value] || seenValues[value] {
					return
				}
				seenValues[value] = true
				column := correlate.Slug(key)
				if column == "" {
					column = "record_id"
				}
				name := "param_" + column
				for i := 2; usedNames[name]; i++ {
					name = fmt.Sprintf("param_%s_%d", column, i)
				}
				usedNames[name] = true
				out = append(out, ParameterizationCandidate{
					Name:        name,
					Source:      "aura_message",
					Expression:  expression,
					ActionSeq:   ref.Sequence,
					ParamKey:    key,
					SampleValue: correlate.Truncate(value, 80),
					Kind:        kind,
					Column:      column,
					Template:    "{{data.salesforce_records." + column + "}}",
				})
			})
		}
	}
	return out
}

// auraMessageBody returns the decoded Aura `message` payload from a request
// body. Lightning posts /aura as application/x-www-form-urlencoded with a
// url-encoded JSON `message` field; recorded/imported bodies may also carry the
// raw JSON directly. Returns "" when no Aura message envelope is present.
func auraMessageBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	// Raw-JSON Aura bodies start with '{'; form-encoded bodies (message=...&aura.token=...)
	// never do. Detect raw JSON first so url.ParseQuery does not mis-split a JSON
	// string value that happens to contain a "message=" / "&...=" substring.
	if strings.HasPrefix(trimmed, "{") {
		if strings.Contains(trimmed, `"actions"`) {
			return trimmed
		}
		return ""
	}
	if values, err := url.ParseQuery(trimmed); err == nil {
		if msg := strings.TrimSpace(values.Get("message")); msg != "" {
			return msg
		}
	}
	if strings.Contains(trimmed, `"actions"`) {
		return trimmed
	}
	return ""
}

func auraMessageActions(decoded any) ([]any, bool) {
	obj, ok := decoded.(map[string]any)
	if !ok {
		return nil, false
	}
	actions, ok := obj["actions"].([]any)
	return actions, ok
}

func auraActionParams(actionValue any) (any, bool) {
	obj, ok := actionValue.(map[string]any)
	if !ok {
		return nil, false
	}
	params, ok := obj["params"]
	return params, ok
}

// walkAuraParams visits every string leaf in an Aura action params tree,
// reporting the leaf key, value, and JMESPath expression rooted at the params
// node.
func walkAuraParams(value any, path, key string, visit func(key, value, expression string)) {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			walkAuraParams(child, appendJMESPath(path, childKey), childKey, visit)
		}
	case []any:
		for index, child := range typed {
			walkAuraParams(child, fmt.Sprintf("%s[%d]", path, index), key, visit)
		}
	case string:
		visit(key, typed, path)
	}
}

// auraParamIdentifierKind classifies an Aura param value as a Salesforce
// record/object identifier worth parameterizing, or reports ok=false.
func auraParamIdentifierKind(key, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if salesforceIDPattern.MatchString(value) {
		return "record_id", true
	}
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
	if (strings.HasSuffix(normalized, "id") || strings.Contains(normalized, "recordid")) && idLikeValuePattern.MatchString(value) {
		return "object_id", true
	}
	return "", false
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

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
