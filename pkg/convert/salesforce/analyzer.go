package salesforce

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
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
	AuraOutcomes          *AuraOutcomeReview          `json:"aura_outcomes,omitempty"`
	Warnings              []string                    `json:"warnings,omitempty"`
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
	Script              *dsl.Script                `json:"script,omitempty"`
	Correlations        []CorrelationCandidate     `json:"correlations,omitempty"`
	NativePathDomain    string                     `json:"native_path_domain,omitempty"`
	CorrelationCoverage *NativeCorrelationCoverage `json:"correlation_coverage,omitempty"`
	referenceCatalog    *nativeReferenceCatalog
}

type CorrelationCandidate struct {
	Name                      string                   `json:"name"`
	Source                    string                   `json:"source"`
	Expression                string                   `json:"expression"`
	FoundInSeq                int                      `json:"found_in_seq"`
	UsedInSeqs                []int                    `json:"used_in_seqs,omitempty"`
	RuleID                    string                   `json:"rule_id,omitempty"`
	Confidence                float64                  `json:"confidence,omitempty"`
	Reasons                   []string                 `json:"reasons,omitempty"`
	PreApplied                bool                     `json:"pre_applied,omitempty"`
	ReviewAction              string                   `json:"review_action,omitempty"`
	ProducerReference         *NativeRequestReference  `json:"producer_reference,omitempty"`
	ConsumerReferences        []NativeRequestReference `json:"consumer_references,omitempty"`
	ConsumerReferencesOmitted int                      `json:"consumer_references_omitted,omitempty"`
}

// ParameterizationCandidate is a record/object identifier discovered inside an
// Aura `message` request payload that a load test should likely drive from a
// data source (one identifier per VUser) rather than replay as a fixed literal.
// These are surfaced for review and are not auto-applied; the value comes from a
// user-supplied dataset via the parameterization-review flow.
type ParameterizationCandidate struct {
	Name             string   `json:"name"`
	Source           string   `json:"source"`
	Expression       string   `json:"expression"`
	ActionSeq        int      `json:"action_seq,omitempty"`
	ActionPath       string   `json:"action_path,omitempty"`
	DecodedFieldPath string   `json:"decoded_field_path,omitempty"`
	EncodingLayers   []string `json:"encoding_layers,omitempty"`
	ParamKey         string   `json:"param_key"`
	SampleValue      string   `json:"sample_value,omitempty"`
	Kind             string   `json:"kind"`
	Column           string   `json:"column"`
	Template         string   `json:"template"`
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
	ActionPath  string
}

type harEntry struct {
	ID                     int
	Method                 string
	URL                    string
	RequestBody            string
	RequestBodyTransformed bool
	RequestHeaders         map[string]string
	ResponseBody           string
	ResponseHeaders        map[string]string
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
	refs, err := flattenRequestActionsChecked(script, false)
	if err != nil {
		return nil, fmt.Errorf("inspect Salesforce request actions: %w", err)
	}
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
	review.AuraOutcomes = inspectAuraOutcomes(refs, entries)
	if outcomes := review.AuraOutcomes; outcomes != nil {
		if !outcomes.DetailsComplete {
			review.Warnings = append(review.Warnings, "Aura review detail retention was bounded; full inspected aggregate counts remain available with explicit omitted detail counts.")
		}
		if !outcomes.Complete {
			review.Warnings = append(review.Warnings, "Aura baseline analysis is incomplete; review missing responses, malformed or unmatched actions, and analysis limits before controlled replay.")
		}
		failed := outcomes.StateCounts[aura.Error] + outcomes.StateCounts[aura.Incomplete] + outcomes.StateCounts[aura.Aborted]
		if outcomes.RecordedFailureActions > 0 {
			review.Warnings = append(review.Warnings, fmt.Sprintf("%d Aura action(s) in %d request(s) already failed in the recording. Replay still requires success and labels these failures \"also failed in the recording\"; to accept the recorded outcome for all of them, set the script's HTTP setting \"Aura actions that failed in the recording\" to \"Accept the recorded outcome\".", outcomes.RecordedFailureActions, outcomes.RecordedFailureBatches))
		}
		if frameworkErrors := outcomes.IssueCounts[aura.InvalidSession] + outcomes.IssueCounts[aura.ClientOutOfSync] + outcomes.IssueCounts[aura.FrameworkError]; frameworkErrors > 0 {
			review.Warnings = append(review.Warnings, fmt.Sprintf("%d captured Aura response(s) were framework errors (invalid session: %d, client out of sync: %d, other: %d); the recording already failed at those requests, so repair or re-record it before controlled replay.", frameworkErrors, outcomes.IssueCounts[aura.InvalidSession], outcomes.IssueCounts[aura.ClientOutOfSync], outcomes.IssueCounts[aura.FrameworkError]))
		}
		if failed > outcomes.RecordedFailureActions {
			review.Warnings = append(review.Warnings, "Captured Aura responses contain unsuccessful action states that could not be matched to the imported request; review those baseline failures before controlled replay.")
		}
		if opts.BuildNative && outcomes.InvalidRequests > 0 {
			review.Blockers = append(review.Blockers, "Imported Aura requests could not be validated; native replay is gated until those request envelopes are repaired.")
		}
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

	correlations, correlationWarnings, err := detectCorrelationsChecked(entries)
	if err != nil {
		if !errors.Is(err, correlate.ErrCorrelationWorkLimit) {
			return nil, fmt.Errorf("detect Salesforce correlations: %w", err)
		}
		// A deterministic work limit must never discard the whole review: keep
		// detection/coverage/endpoint results and note the degraded analysis.
		correlations = nil
		correlationWarnings = append(correlationWarnings, err.Error())
	}
	for _, warning := range correlationWarnings {
		review.Blockers = append(review.Blockers, "Salesforce correlation analysis was incomplete: "+warning)
	}
	review.CorrelationConfidence = correlationConfidence(correlations, len(entries))
	var parameterWarnings []string
	review.Parameterizations, parameterWarnings = auraMessageParameterizationsChecked(refs, correlations)
	review.Warnings = append(review.Warnings, parameterWarnings...)
	if opts.BuildNative {
		preview, err := buildNativePreview(script, refs, entries, matches, correlations, review.AuraOutcomes.RecordedFailures())
		if err != nil {
			return nil, err
		}
		review.NativePreview = preview
		if warning := auraTokenProvenanceWarning(preview.Script, correlations); warning != "" {
			review.Warnings = append(review.Warnings, warning)
		}
		if preview.CorrelationCoverage != nil && len(correlationWarnings) > 0 {
			preview.referenceCatalog.base.Complete = false
			preview.CorrelationCoverage.Complete = false
		}
	}
	return review, nil
}

func flattenRequestActions(script *dsl.Script) []actionRef {
	refs, _ := flattenRequestActionsChecked(script, false)
	return refs
}

// flattenRequestActionsForRewrite is the mutable counterpart used by the native
// preview builder. http_sequence requests live inside Action.Params, so parsing
// them into detached dsl.Action values is not enough: mutations made through the
// returned refs must point at a slice stored back on the parent action.
func flattenRequestActionsForRewrite(script *dsl.Script) []actionRef {
	refs, _ := flattenRequestActionsChecked(script, true)
	return refs
}

func flattenRequestActionsChecked(script *dsl.Script, persistSequenceChildren bool) ([]actionRef, error) {
	if script == nil {
		return nil, nil
	}
	rootCount := len(script.Init) + len(script.Actions) + len(script.Teardown)
	if rootCount > dsl.MaxScriptActionDefinitions {
		return nil, fmt.Errorf("script action definitions exceed %d", dsl.MaxScriptActionDefinitions)
	}
	refs := make([]actionRef, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	seq := 1
	appendRef := func(phase string, index int, action *dsl.Action, inheritedTransaction, actionPath string) {
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
			ActionPath:  actionPath,
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
	definitions := 0
	var appendRefs func(string, []dsl.Action, string, int, string) error
	appendRefs = func(phase string, actions []dsl.Action, prefix string, depth int, inheritedTransaction string) error {
		if depth > dsl.MaxScriptActionDepth {
			return fmt.Errorf("action nesting exceeds %d levels", dsl.MaxScriptActionDepth)
		}
		for index := range actions {
			definitions++
			if definitions > dsl.MaxScriptActionDefinitions {
				return fmt.Errorf("script action definitions exceed %d", dsl.MaxScriptActionDefinitions)
			}
			action := &actions[index]
			actionPath := fmt.Sprintf("%s[%d]", prefix, index)
			transaction := convertutil.FirstNonEmpty(metadataString(action.Metadata, "source_transaction"), inheritedTransaction)
			if strings.EqualFold(action.Type, "http_sequence") {
				definitions += salesforceSequenceRequestCount(*action)
				if definitions > dsl.MaxScriptActionDefinitions {
					return fmt.Errorf("script action/request definitions exceed %d", dsl.MaxScriptActionDefinitions)
				}
				children := salesforceSequenceChildren(*action)
				exactChildIndexes := len(children) == salesforceSequenceRequestCount(*action)
				if persistSequenceChildren && len(children) > 0 {
					// Preserve mutable child pointers inside the returned native script.
					action.Params["requests"] = children
				}
				for childIndex := range children {
					child := &children[childIndex]
					if persistSequenceChildren {
						if child.Metadata == nil {
							child.Metadata = map[string]any{}
						}
						if child.Metadata["source_transaction"] == nil && transaction != "" {
							child.Metadata["source_transaction"] = transaction
						}
					}
					childPath := ""
					if persistSequenceChildren || exactChildIndexes {
						childPath = fmt.Sprintf("%s.params.requests[%d]", actionPath, childIndex)
					}
					appendRef(phase, index, child, transaction, childPath)
				}
			} else {
				appendRef(phase, index, action, transaction, actionPath)
			}
			if action.Control != nil {
				if len(action.Control.Cases)+len(action.Control.Branches) > dsl.MaxScriptActionDefinitions {
					return fmt.Errorf("control branch definitions exceed %d", dsl.MaxScriptActionDefinitions)
				}
				for _, branch := range action.Control.ActionBranches() {
					if err := appendRefs(phase, branch.Actions, actionPath+".control."+branch.Path, depth+1, transaction); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, phase := range []struct {
		name, path string
		actions    []dsl.Action
	}{{"init", "init", script.Init}, {"action", "actions", script.Actions}, {"teardown", "teardown", script.Teardown}} {
		if err := appendRefs(phase.name, phase.actions, phase.path, 1, ""); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func isEngineAction(actionType string) bool {
	switch actionType {
	case "rendezvous", "error_message", "source_message", "source_runtime", "source_transaction", "source_timer", "source_metric", "source_parameter", "source_json", "source_xml", "source_database", "source_secret", "source_external", "source_review", "custom_call":
		return true
	default:
		return false
	}
}

// Generic source navigation must not guess indexes after malformed request
// entries have been filtered out by the sequence adapter.
func salesforceSequenceRequestCount(action dsl.Action) int {
	if action.Params == nil {
		return 0
	}
	switch requests := action.Params["requests"].(type) {
	case []any:
		return len(requests)
	case []map[string]any:
		return len(requests)
	case []dsl.Action:
		return len(requests)
	default:
		return 0
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
	observations, err := sample.HARSourceObservations(string(data), true)
	if err != nil {
		return nil, fmt.Errorf("parse Salesforce HAR: %w", err)
	}
	entries := make([]harEntry, 0, len(observations))
	for _, observation := range observations {
		entries = append(entries, harEntry{
			ID:                     observation.ID,
			Method:                 strings.ToUpper(strings.TrimSpace(observation.Method)),
			URL:                    strings.TrimSpace(observation.RequestURL),
			RequestBody:            observation.RequestBody,
			RequestBodyTransformed: observation.RequestBodyTransformed,
			RequestHeaders:         observation.RequestHeaders,
			ResponseBody:           observation.ResponseBody,
			ResponseHeaders:        observation.ResponseHeaders,
		})
	}
	return entries, nil
}

// normalizedURL is the comparison form of a request URL: the trimmed raw text plus
// the case-folded scheme/host and the exact escaped path/query. Parsing is expensive
// relative to the rest of the matcher, so every URL is normalized exactly once.
type normalizedURL struct {
	raw    string
	parsed bool
	scheme string
	host   string
	path   string
	query  string
}

func parseNormalizedURL(raw string) normalizedURL {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil {
		return normalizedURL{raw: raw}
	}
	return normalizedURL{
		raw:    raw,
		parsed: true,
		scheme: strings.ToLower(parsed.Scheme),
		host:   strings.ToLower(parsed.Host),
		path:   parsed.EscapedPath(),
		query:  parsed.RawQuery,
	}
}

// equivalentNormalizedURL reports whether two normalized URLs address the same
// resource. Unparseable URLs only ever match their identical raw text.
func equivalentNormalizedURL(left, right normalizedURL) bool {
	if left.raw == right.raw {
		return true
	}
	if !left.parsed || !right.parsed {
		return false
	}
	return left.scheme == right.scheme &&
		left.host == right.host &&
		left.path == right.path &&
		left.query == right.query
}

// bucketKey returns a lookup key with the property that two normalized URLs are
// equivalent under equivalentNormalizedURL exactly when their keys are equal:
//   - Both parsed: identical raw text yields identical components (url.Parse is
//     deterministic), so raw equality is subsumed by component equality.
//   - Neither parsed: only raw equality can match, which is the key.
//   - Mixed: the keys differ by prefix, and identical raw text cannot produce
//     different parse outcomes, so the pair is genuinely non-equivalent.
func (n normalizedURL) bucketKey() string {
	if !n.parsed {
		return "raw\x00" + n.raw
	}
	return "url\x00" + n.scheme + "\x00" + n.host + "\x00" + n.path + "\x00" + n.query
}

// harCandidate caches everything the matcher needs from one HAR entry. The URL
// normalization, the canonical request body, and the Salesforce classification are
// pure functions of the entry, so computing them once per entry (rather than once
// per ref x entry pair) leaves matching results unchanged.
type harCandidate struct {
	entry harEntry
	url   normalizedURL
	body  string
	kind  string
}

func matchHAR(refs []actionRef, entries []harEntry) map[int]int {
	matches := make(map[int]int)
	if len(refs) == 0 || len(entries) == 0 {
		return matches
	}

	candidates := make([]harCandidate, len(entries))
	byURL := make(map[string][]int, len(entries))
	for index, entry := range entries {
		candidate := harCandidate{
			entry: entry,
			url:   parseNormalizedURL(entry.URL),
			body:  canonicalRequestBody(entry.RequestBody),
			kind:  classifyHARRequest(entry),
		}
		candidates[index] = candidate
		key := candidate.url.bucketKey()
		byURL[key] = append(byURL[key], index)
	}

	used := make(map[int]bool)
	for refIndex, ref := range refs {
		refURL := parseNormalizedURL(ref.URL)
		actionBody := ""
		if ref.Action != nil {
			actionBody = ref.Action.Body
		}
		refBody := canonicalRequestBody(actionBody)
		descriptor := ""
		if ref.Descriptor != "" {
			descriptor = strings.ToLower(ref.Descriptor)
		}
		bestID := 0
		bestScore := -1
		// Only URL-equivalent entries can match, so the scan is limited to the
		// bucket instead of every entry. Bucket order mirrors document order, so
		// the first-best tie-break is preserved.
		for _, index := range byURL[refURL.bucketKey()] {
			candidate := candidates[index]
			if used[candidate.entry.ID] {
				continue
			}
			if !strings.EqualFold(ref.Method, candidate.entry.Method) {
				continue
			}
			if !equivalentNormalizedURL(refURL, candidate.url) {
				continue
			}
			if score := harMatchScore(refBody, descriptor, ref.Kind, candidate); score > bestScore {
				bestScore = score
				bestID = candidate.entry.ID
			}
		}
		if bestID != 0 {
			matches[refIndex] = bestID
			used[bestID] = true
		}
	}
	return matches
}

// harMatchScore ranks one candidate entry against a ref whose body/descriptor have
// already been canonicalized by the caller.
func harMatchScore(refBody, lowerDescriptor, refKind string, candidate harCandidate) int {
	score := 1
	entryBody := candidate.body
	if refBody != "" || entryBody != "" {
		if refBody != "" && refBody == entryBody {
			score += 100
		}
	}
	if lowerDescriptor != "" && strings.Contains(entryBody, lowerDescriptor) {
		score += 25
	}
	if refKind != "" && refKind == candidate.kind {
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
	path := strings.ToLower(parsed.Path)
	body := strings.ToLower(action.Body)
	switch {
	// Concrete API surfaces are matched before the login heuristic so that data
	// or Apex resources whose names merely contain "login"/"oauth" (e.g. the
	// standard LoginHistory object, a Login_Event__c, or a LoginController Apex
	// class) are not misclassified as auth traffic.
	case strings.Contains(path, "/einstein/ai-agent"):
		return "agentforce"
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
	case isAuraEndpointPath(path) || hasAuraActionEnvelopeEvidence(action):
		return "aura"
	case strings.Contains(path, "/cometd") || strings.Contains(body, "/meta/handshake") || strings.Contains(body, "/meta/connect") || strings.Contains(body, "/meta/subscribe"):
		return "cometd"
	case isSalesforceLoginRequest(host, path):
		return "login"
	case strings.Contains(host, "lightning.force.com"):
		return "lightning"
	default:
		return "generic"
	}
}

func isAuraEndpointPath(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if strings.EqualFold(segment, "aura") {
			return true
		}
	}
	return false
}

func hasAuraActionEnvelopeEvidence(action *dsl.Action) bool {
	body, code := auraActionRequestBody(action)
	return code == "" && hasAuraEnvelopeEvidence(body)
}

func hasAuraEnvelopeEvidence(body string) bool {
	if len(body) > aura.MaxEnvelopeBytes {
		return false
	}
	// A valid raw or message-form envelope establishes structure without
	// relying on incidental business payload keys such as descriptor.
	if validateAuraRequest(body).Success {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		return false
	}
	if strings.Count(body, "&")+1 > aura.MaxFormFields {
		return false
	}
	values, _ := url.ParseQuery(body)
	_, message := values["message"]
	_, context := values["aura.context"]
	_, token := values["aura.token"]
	return message && (context || token)
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
	match := auraDescriptorPattern.FindStringSubmatch(evidence)
	if len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func classifySalesforceFlow(evidence, descriptor string) string {
	lower := strings.ToLower(evidence + " " + descriptor)
	switch {
	case containsAny(lower, "/einstein/ai-agent", "ai-agent", "agentforce", "copilot"):
		return "agent_conversation"
	case containsAny(lower, "/cometd", "/meta/handshake", "/meta/connect", "/meta/subscribe", "pushtopic", "changeevents", "/event/"):
		return "streaming_subscribe"
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

type correlationNameRegistry struct {
	used map[string]bool
}

func newCorrelationNameRegistry() *correlationNameRegistry {
	return &correlationNameRegistry{used: map[string]bool{}}
}

func (registry *correlationNameRegistry) reserve(name string) {
	name = strings.TrimSpace(name)
	if name != "" {
		registry.used[name] = true
	}
}

func (registry *correlationNameRegistry) claim(base string) string {
	base = correlate.Slug(base)
	if base == "" {
		base = "corr_value"
	}
	if !registry.used[base] {
		registry.used[base] = true
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if !registry.used[candidate] {
			registry.used[candidate] = true
			return candidate
		}
	}
}

func correlationsWithMappedSources(correlations []correlate.Correlation, refs map[int]*actionRef) []correlate.Correlation {
	out := make([]correlate.Correlation, 0, len(correlations))
	for _, corr := range correlations {
		ref := refs[corr.FoundInID]
		if ref == nil || ref.Action == nil {
			continue
		}
		out = append(out, corr)
	}
	return out
}

// prepareCorrelationNames reserves existing script extractors first, then gives
// each mapped correlation a unique variable name. The returned registry remains
// live so derived names (notably the mixed-use _encoded sibling) cannot collide
// with either a real correlation or an existing extractor.
func prepareCorrelationNames(correlations []correlate.Correlation, refs []actionRef) ([]correlate.Correlation, *correlationNameRegistry) {
	registry := newCorrelationNameRegistry()
	for _, ref := range refs {
		if ref.Action == nil {
			continue
		}
		for _, extractor := range ref.Action.Extractors {
			registry.reserve(extractor.Name)
		}
	}
	out := append([]correlate.Correlation(nil), correlations...)
	for index := range out {
		out[index].Name = registry.claim(out[index].Name)
	}
	return out, registry
}

func buildNativePreview(script *dsl.Script, refs []actionRef, entries []harEntry, matches map[int]int, correlations []correlate.Correlation, recordedAuraFailures map[string][]aura.RecordedOutcome) (*NativePreview, error) {
	clone, err := cloneScript(script)
	if err != nil {
		return nil, err
	}
	clone.Name = strings.TrimSpace(script.Name) + " Salesforce Native Preview"
	clone.GeneratedBy = "apexload-salesforce-builder"
	clone.GeneratedFrom = "enterprise_script_archive+har"
	ensureMetadata(clone)["salesforce_native_preview"] = true

	cloneRefs, err := flattenRequestActionsChecked(clone, true)
	if err != nil {
		return nil, fmt.Errorf("inspect native Salesforce request actions: %w", err)
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
			"classification":  ref.Kind,
			"flow_kind":       ref.FlowKind,
			"object":          ref.Object,
			"aura_descriptor": ref.Descriptor,
			"sequence":        ref.Sequence,
			"har_match_id":    ref.HARID,
		}
		if isAuraBatch(*ref) {
			if validateAuraActionRequest(ref.Action).Success {
				// Replace the legacy generated substring guards with validation of
				// the actual outgoing request IDs and every response action.
				removeLegacyAuraAssertions(ref.Action)
				assertion := dsl.Assertion{Type: "aura_actions_success"}
				if recorded := recordedAuraFailures[ref.ActionPath]; len(recorded) > 0 {
					assertion.Expected = aura.Baseline{Recorded: recorded}
				}
				addAssertionIfMissing(ref.Action, assertion)
			} else {
				const code = "salesforce_aura_request_requires_review"
				importreview.ApplyToAction(ref.Action, []importreview.Finding{{
					Code: code, ReasonCode: code, Severity: diag.Blocker,
					Detail:     "The imported Aura request envelope is malformed, ambiguous, or exceeds validation limits; repair it before native replay.",
					ActionPath: ref.ActionPath, Gate: importreview.GateExecution,
					Resolution: "replace_behavior_and_remove_gate",
				}})
			}
		}
	}

	// Correlations discovered in HAR-only traffic cannot be made executable in
	// this script: there is nowhere to attach their source extractor. Keep only
	// correlations whose source maps to a native action, then give every variable
	// a globally unique name (also avoiding extractors already in the script).
	mappedCorrelations := correlationsWithMappedSources(correlations, harIDToRef)
	namedCorrelations, correlationNames := prepareCorrelationNames(mappedCorrelations, cloneRefs)
	// Only high-confidence, source-mapped correlations participate in the rewrite
	// map. Lower-confidence candidates still participate in collision ownership:
	// otherwise a shorter automatic literal could rewrite part of a longer value
	// that the review intentionally left untouched.
	vars := make(map[string][]nativeLiteralProducer, len(namedCorrelations))
	applications := make(map[string]correlationApplicationStatus, len(namedCorrelations))
	plans := make([]nativeCorrelationPlan, 0, len(namedCorrelations))
	sourceValidator := correlate.NewExactQueryEncodingValidator()
	for _, corr := range namedCorrelations {
		foundRef := harIDToRef[corr.FoundInID]
		automatic := correlate.HighConfidence(corr)
		usage := correlationUsage(corr, harIDToRef, foundRef.Sequence)
		value := correlationValue(corr)
		queryEncoded := correlate.QueryEncodedFormOf(value)
		encodedConvert, encodedConvertSupported := correlate.EncodedReuseExtractorConvert(corr.Convert)
		if encodedConvertSupported && strings.EqualFold(strings.TrimSpace(corr.Convert), "url_decode") {
			encodedConvertSupported = correlationSourceUsesExactQueryEncoding(sourceValidator, entries, corr, queryEncoded)
		}
		plans = append(plans, nativeCorrelationPlan{
			correlation:      corr,
			foundRef:         foundRef,
			automatic:        automatic,
			usage:            usage,
			value:            value,
			queryEncoded:     queryEncoded,
			encodedConvert:   encodedConvert,
			encodedSupported: encodedConvertSupported,
		})
		status := correlationApplicationStatus{}
		if !automatic {
			status.reviewAction = "manual_review_required"
		}
		if usage.unsupportedEncoded || (usage.encoded && !encodedConvertSupported) {
			status.reviewAction = "manual_value_replacement_required"
		}
		applications[corr.Name] = status
	}

	ambiguousLiterals, ambiguousPlans, containment := nativeCorrelationReplacementCollisions(plans, joinedActionCorrelationSurfaces(cloneRefs))
	for planIndex := range ambiguousPlans {
		corr := plans[planIndex].correlation
		status := applications[corr.Name]
		status.reviewAction = "manual_value_replacement_required"
		applications[corr.Name] = status
	}

	// Resolve ownership and reserve extractor capacity before exposing any
	// replacement variable. A mixed raw+encoded correlation is atomic: if its
	// source action has room for only one of the two required extractors, leave
	// both spellings untouched rather than generating a dangling/invalid preview.
	// Existing source extractors count against the same DSL ceiling.
	sourceExtractorCounts := make(map[*dsl.Action]int, len(cloneRefs))
	for index := range cloneRefs {
		action := cloneRefs[index].Action
		if action != nil {
			sourceExtractorCounts[action] = len(action.Extractors)
		}
	}
	for index := range plans {
		plan := &plans[index]
		corr := plan.correlation
		status := applications[corr.Name]
		plan.rawMapped = plan.automatic && plan.usage.raw && plan.value != "" && !ambiguousLiterals[plan.value]
		plan.encodedMapped = plan.automatic && plan.usage.encoded && plan.encodedSupported && plan.queryEncoded != "" && !ambiguousLiterals[plan.queryEncoded]
		requiredExtractors := 0
		if plan.rawMapped || plan.encodedMapped {
			requiredExtractors = 1
		}
		if plan.rawMapped && plan.encodedMapped {
			requiredExtractors++
		}
		if plan.foundRef == nil || plan.foundRef.Action == nil || sourceExtractorCounts[plan.foundRef.Action]+requiredExtractors > dsl.MaxActionExtractors {
			plan.rawMapped = false
			plan.encodedMapped = false
			if requiredExtractors > 0 {
				status.reviewAction = "manual_value_replacement_required"
			}
			applications[corr.Name] = status
			continue
		}
		sourceExtractorCounts[plan.foundRef.Action] += requiredExtractors

		if plan.rawMapped {
			vars[plan.value] = append(vars[plan.value], nativeLiteralProducer{sequence: plan.foundRef.Sequence, template: "{{var." + corr.Name + "}}"})
		}
		if plan.encodedMapped {
			plan.encodedName = corr.Name
			if plan.rawMapped {
				plan.encodedName = correlationNames.claim(corr.Name + "_encoded")
			}
			vars[plan.queryEncoded] = append(vars[plan.queryEncoded], nativeLiteralProducer{sequence: plan.foundRef.Sequence, template: "{{var." + plan.encodedName + "}}"})
		}
		applications[corr.Name] = status
	}

	// Rewrite before attaching extractors: a contained literal can lose every
	// occurrence to its longer container, and an extractor whose variable is
	// never substituted must not be reported as applied.
	landed := make(map[string]bool, len(vars))
	for index := range cloneRefs {
		action := cloneRefs[index].Action
		actionVars := correlationVarsAvailableAfterSequence(vars, cloneRefs[index].Sequence)
		actionVars = withoutInnerVarsOfLiteralOuters(actionVars, containment, actionCorrelationSurfaces(action))
		action.Target = correlate.ApplyVars(action.Target, actionVars)
		action.Body = correlate.ApplyVars(action.Body, actionVars)
		action.Headers = correlate.ApplyVarsToHeaders(action.Headers, actionVars)
		action.HeaderList = applyCorrelationVarsToHeaderList(action.HeaderList, actionVars)
		if len(actionVars) == 0 {
			continue
		}
		rewritten := actionCorrelationSurfaces(action)
		for _, template := range actionVars {
			if landed[template] {
				continue
			}
			for _, surface := range rewritten {
				if strings.Contains(surface, template) {
					landed[template] = true
					break
				}
			}
		}
	}
	remaining := joinedActionCorrelationSurfaces(cloneRefs)

	for index := range plans {
		plan := &plans[index]
		corr := plan.correlation
		foundRef := plan.foundRef
		base := dsl.Extractor{
			Name:       corr.Name,
			Source:     corr.Source,
			Type:       corr.ExtractType,
			Expression: corr.Expression,
			SaveOffset: corr.SaveOffset,
			SaveLength: corr.SaveLength,
			Convert:    corr.Convert,
			OnMiss:     "fail",
		}
		status := applications[corr.Name]
		rawLanded := plan.rawMapped && landed["{{var."+corr.Name+"}}"]
		encodedLanded := plan.encodedMapped && landed["{{var."+plan.encodedName+"}}"]
		if rawLanded {
			// Raw reuse remains executable even when another consumer uses an
			// encoding that requires manual replacement.
			addExtractorIfMissing(foundRef.Action, base)
		}
		switch {
		case encodedLanded && plan.encodedName == corr.Name:
			// Value is only ever reused percent-encoded (e.g. aura.token inside a
			// form-urlencoded /aura body): extract it pre-encoded and substitute the
			// encoded occurrences with the variable.
			base.Convert = plan.encodedConvert
			addExtractorIfMissing(foundRef.Action, base)
		case encodedLanded:
			// Value is reused BOTH raw (e.g. a JSON body/header) AND percent-encoded
			// (a form body). A single variable can only resolve one way, so the
			// percent-encoded literals use a url_encode sibling variable.
			addExtractorIfMissing(foundRef.Action, dsl.Extractor{
				Name:       plan.encodedName,
				Source:     corr.Source,
				Type:       corr.ExtractType,
				Expression: corr.Expression,
				SaveOffset: corr.SaveOffset,
				SaveLength: corr.SaveLength,
				Convert:    plan.encodedConvert,
				OnMiss:     "fail",
			})
		}
		status.preApplied = rawLanded || encodedLanded
		if !status.preApplied && status.reviewAction == "" && nativePlanLiteralRemains(*plan, remaining) {
			status.reviewAction = "manual_value_replacement_required"
		}
		applications[corr.Name] = status
	}
	originalCorrelationRefs := make(map[int]*actionRef, len(harIDToRef))
	for id, ref := range harIDToRef {
		copied := *ref
		originalCorrelationRefs[id] = &copied
	}
	synthesizeBulkPollLoops(cloneRefs)
	collapseCometdConnectLoops(clone)

	return newNativeReferencePreview(clone, correlations, namedCorrelations, originalCorrelationRefs, applications), nil
}

// collapseCometdConnectLoops folds a contiguous run of CometD Bayeux
// /meta/connect long-poll requests into a single control_loop, so the native
// preview models the connect long-poll as one repeatable step the operator can
// scale (raise the count or swap in a condition) instead of N flattened POSTs.
// handshake and subscribe requests are left as-is. Runs after correlation/var
// substitution and rebuilds the action slices, so it must be the last preview
// transform (it invalidates the cloneRefs pointers).
func collapseCometdConnectLoops(script *dsl.Script) {
	script.Init = collapseCometdConnectRun(script.Init)
	script.Actions = collapseCometdConnectRun(script.Actions)
	script.Teardown = collapseCometdConnectRun(script.Teardown)
}

func collapseCometdConnectRun(actions []dsl.Action) []dsl.Action {
	out, _ := collapseCometdConnectRunInspected(actions)
	return out
}

// Keep at most the current signature and one pending signature. Every request
// is admitted/normalized once, including the first non-equivalent next request.
func collapseCometdConnectRunInspected(actions []dsl.Action) ([]dsl.Action, int) {
	out := make([]dsl.Action, 0, len(actions))
	inspections := 0
	pendingIndex := -1
	var pending dsl.Action
	pendingOK := false
	prepare := func(index int) (dsl.Action, bool) {
		if index == pendingIndex {
			pendingIndex = -1
			return pending, pendingOK
		}
		inspections++
		return cometdConnectComparisonAction(actions[index])
	}
	for i := 0; i < len(actions); {
		first, ok := prepare(i)
		if !ok {
			out = append(out, actions[i])
			i++
			continue
		}
		j := i + 1
		for j < len(actions) {
			candidate, admitted := prepare(j)
			if !admitted || !cometdConnectComparisonsEqual(first, candidate) {
				pendingIndex, pending, pendingOK = j, candidate, admitted
				break
			}
			j++
		}
		if j-i >= 2 && cometdConnectExtractorsAreEquivalent(actions[i:j]) {
			out = append(out, wrapCometdConnectLoop(actions[i], j-i))
		} else {
			out = append(out, actions[i:j]...)
		}
		i = j
	}
	return out, inspections
}

// cometdConnectExtractorsAreEquivalent keeps correlation timing intact when a
// recorded connect run is replaced by one repeated action. Moving an extractor
// that first appears on a later response onto the loop body would evaluate a
// fail-closed extractor too early, while unioning per-response extractors could
// also exceed the per-action DSL ceiling. Identical extractor lists already run
// on every recorded response, so repeating the first list preserves behavior.
func cometdConnectExtractorsAreEquivalent(actions []dsl.Action) bool {
	if len(actions) == 0 || len(actions[0].Extractors) > dsl.MaxActionExtractors {
		return false
	}
	baseline := actions[0].Extractors
	for actionIndex := 1; actionIndex < len(actions); actionIndex++ {
		candidate := actions[actionIndex].Extractors
		if len(candidate) != len(baseline) {
			return false
		}
		for extractorIndex := range baseline {
			if !reflect.DeepEqual(candidate[extractorIndex], baseline[extractorIndex]) {
				return false
			}
		}
	}
	return true
}

func isCometdConnectAction(action *dsl.Action) bool {
	if action == nil || actionClassification(action) != "cometd" {
		return false
	}
	// Only a bounded, unambiguous single connect
	// can become a repeated action. Channel text elsewhere is not evidence.
	if strings.TrimSpace(action.BodyEncoding) != "" {
		return false
	}
	message, ok := cometdConnectMessageBody(action.Body)
	if !ok {
		return false
	}
	_, _, ok = singleBayeuxConnectMessage(message)
	return ok
}

func actionClassification(action *dsl.Action) string {
	if action == nil || action.Metadata == nil {
		return ""
	}
	sf, ok := action.Metadata["salesforce"].(map[string]any)
	if !ok {
		return ""
	}
	kind, _ := sf["classification"].(string)
	return kind
}

// cometdConnectTimeoutMs is the per-action request timeout applied to a folded
// /meta/connect long-poll. Salesforce holds /meta/connect open up to ~110s
// (Bayeux advice timeout), so the default 60s client timeout would make every
// poll spuriously error; this gives headroom above the server hold.
const cometdConnectTimeoutMs = 130000

const cometdLoopIndexTemplate = "{{var.loop_index}}"

// wrapCometdConnectLoop wraps a single /meta/connect request in a control_loop
// that repeats it the recorded number of times. The count is a starting point —
// the long-poll is meant to run for the test duration, so the operator should
// raise the count or replace it with a condition. A per-action TimeoutMs above
// the server long-poll hold is set (when not already recorded) so each connect
// does not time out against the default client timeout.
func wrapCometdConnectLoop(connect dsl.Action, count int) dsl.Action {
	connect.Body = rewriteCometdConnectLoopID(connect.Body)
	if connect.TimeoutMs == 0 {
		connect.TimeoutMs = cometdConnectTimeoutMs
	}
	return dsl.Action{
		Name: strings.TrimSpace(connect.Name) + " (long-poll loop)",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count: count,
			Steps: []dsl.Action{connect},
		},
		Metadata: map[string]any{
			"salesforce": map[string]any{
				"flow_kind":   "streaming_subscribe",
				"synthesized": "cometd_connect_loop",
			},
		},
	}
}

func rewriteCometdConnectLoopID(body string) string {
	if len(body) > maxCometdConnectBodyBytes {
		return body
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		if rewritten, ok := rewriteBayeuxConnectMessageID(trimmed); ok {
			return rewritten
		}
		return body
	}
	if rewritten, ok := rewriteFormEncodedCometdMessage(body); ok {
		return rewritten
	}
	return body
}

func rewriteFormEncodedCometdMessage(body string) (string, bool) {
	message, ok := cometdConnectMessageBody(body)
	if !ok {
		return "", false
	}
	rewrittenMessage, ok := rewriteBayeuxConnectMessageID(message)
	if !ok {
		return "", false
	}
	encodedMessage := preserveTemplateBraces(url.QueryEscape(rewrittenMessage))
	return replaceCometdFormMessage(body, encodedMessage)
}

func preserveTemplateBraces(value string) string {
	value = strings.ReplaceAll(value, "%7B%7B", "{{")
	value = strings.ReplaceAll(value, "%7b%7b", "{{")
	value = strings.ReplaceAll(value, "%7D%7D", "}}")
	value = strings.ReplaceAll(value, "%7d%7d", "}}")
	return value
}

func rewriteBayeuxConnectMessageID(raw string) (string, bool) {
	message, array, ok := singleBayeuxConnectMessage(raw)
	if !ok || !rewriteSingleBayeuxConnectID(message) {
		return "", false
	}
	var value any = message
	if array {
		value = []map[string]any{message}
	}
	rewritten, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(rewritten), true
}

func rewriteSingleBayeuxConnectID(message map[string]any) bool {
	if channel, _ := message["channel"].(string); channel != "/meta/connect" {
		return false
	}
	if _, ok := message["id"]; !ok {
		return false
	}
	message["id"] = cometdLoopIndexTemplate
	return true
}

// Bulk API 2.0 poll-loop synthesis.
const (
	bulkStateVarPrefix    = "bulk_job_state"
	bulkPollMaxIterations = 60
	bulkPollIntervalMs    = 2000
)

// bulkStateVarFor returns the per-job-kind poll-state variable name. Ingest and
// query jobs must use distinct variables: VUser.Variables is a single map
// persisted across the whole run, so a shared "bulk_job_state" lets the first
// job's loop leave a terminal "JobComplete" that makes the second job's loop
// condition false on its first pass — the second poll loop (and its status GET)
// then never runs.
func bulkStateVarFor(kind string) string {
	if kind == "" {
		return bulkStateVarPrefix
	}
	return bulkStateVarPrefix + "_" + kind
}

// bulkPollConditionExprFor keeps polling while the job is non-terminal. The loop
// runs in do_while mode, so the body (status GET) always executes once and
// re-primes the state var before this predicate is first evaluated — including
// on VUser re-entry, where the var would otherwise still hold a terminal value
// from the previous iteration. The leading !has(...) clause remains as defensive
// null-safety (a bare comparison on an unset var errors, which the engine treats
// as false and would skip the poll).
func bulkPollConditionExprFor(stateVar string) string {
	return fmt.Sprintf(`!has(var.%[1]s) || (var.%[1]s != "JobComplete" && var.%[1]s != "Failed" && var.%[1]s != "Aborted")`, stateVar)
}

// synthesizeBulkPollLoops rewrites a recorded Bulk API 2.0 ingest/query status
// GET into a control_loop that polls until the job reaches a terminal state, so
// the native preview waits for completion under load instead of issuing a single
// recorded status check. It only fires when a matching Bulk job-create request
// is also present (so a stray job-status GET is not wrapped), and it is
// idempotent.
func synthesizeBulkPollLoops(refs []actionRef) {
	createKinds := map[string]bool{}
	for i := range refs {
		if kind := bulkJobCreateKind(&refs[i]); kind != "" {
			createKinds[kind] = true
		}
	}
	if len(createKinds) == 0 {
		return
	}
	for i := range refs {
		if kind := bulkJobStatusPollKind(&refs[i]); kind != "" && createKinds[kind] {
			wrapBulkStatusPollInLoop(refs[i].Action, kind)
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

// bulkJobCreateKind matches POST .../jobs/{ingest,query} (the job-create call,
// which has no job-id path segment) and returns the job kind.
func bulkJobCreateKind(ref *actionRef) string {
	if ref.Kind != "bulk_data" || ref.Action == nil || refMethod(ref) != "POST" {
		return ""
	}
	lower := strings.ToLower(ref.Action.Target)
	for _, kind := range []string{"ingest", "query"} {
		base := "/jobs/" + kind
		if strings.Contains(lower, base) && !strings.Contains(lower, base+"/") {
			return kind
		}
	}
	return ""
}

// bulkJobStatusPollKind matches the bare GET .../jobs/{ingest,query}/<id>
// job-status request, excluding upload and result-download endpoints.
func bulkJobStatusPollKind(ref *actionRef) string {
	if ref.Kind != "bulk_data" || ref.Action == nil || refMethod(ref) != "GET" {
		return ""
	}
	lower := strings.ToLower(ref.Action.Target)
	for _, suffix := range []string{"successfulresults", "failedresults", "unprocessedrecords", "/batches", "/results"} {
		if strings.Contains(lower, suffix) {
			return ""
		}
	}
	for _, kind := range []string{"ingest", "query"} {
		if strings.Contains(lower, "/jobs/"+kind+"/") {
			return kind
		}
	}
	return ""
}

// wrapBulkStatusPollInLoop rewrites a job-status GET in place into a control_loop
// that re-issues the GET (with a state extractor and a poll interval) until the
// job reaches a terminal state or the iteration cap is hit.
func wrapBulkStatusPollInLoop(action *dsl.Action, kind string) {
	if action == nil || strings.EqualFold(action.Type, "control_loop") {
		return
	}
	stateVar := bulkStateVarFor(kind)
	inner := *action
	addExtractorIfMissing(&inner, dsl.Extractor{
		Name:       stateVar,
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
			Condition:     &dsl.Condition{Expr: bulkPollConditionExprFor(stateVar)},
			Steps:         []dsl.Action{inner},
			MaxIterations: bulkPollMaxIterations,
			// do_while runs the status GET once before the terminal-state predicate
			// is checked, re-priming the state var each time the loop is entered.
			// Without it, the state var left at "JobComplete" by a prior VUser
			// iteration would make the condition immediately false and skip the
			// (only copy of the) status GET from iteration 2 onward under load.
			DoWhile: true,
		},
		Metadata: map[string]any{
			"salesforce": map[string]any{
				"flow_kind":   "bulk_data",
				"synthesized": "bulk_poll_loop",
			},
		},
	}
}

// correlationUse records how a correlated value is reused across later requests:
// raw (verbatim) and/or with the exact QueryEscape spelling produced by
// url_encode. The two are not mutually exclusive, which requires both a raw and
// a url_encode extractor. Unsupported encoded spellings remain review-only.
type correlationUse struct {
	raw                bool
	encoded            bool
	unsupportedEncoded bool
}

type nativeCorrelationPlan struct {
	correlation      correlate.Correlation
	foundRef         *actionRef
	automatic        bool
	usage            correlationUse
	value            string
	queryEncoded     string
	encodedConvert   string
	encodedSupported bool
	rawMapped        bool
	encodedMapped    bool
	encodedName      string
}

type correlationApplicationStatus struct {
	preApplied   bool
	reviewAction string
}

// nativeLiteralContainment records an automatic literal that occurs inside a
// longer claimed literal. Longest-first single-pass replacement keeps the inner
// value intact wherever the outer literal is rewritten too, so containment is
// not ambiguous globally; it only forbids rewriting the inner value in an
// action where an automatic outer literal stays literal. A review-only outer is
// never rewritten automatically, so the inner value is rewritten inside it, as
// the sample converter does (an outer holding a dynamic value is stale anyway).
type nativeLiteralContainment struct {
	inner, outer   string
	outerAutomatic bool
}

// nativeCollisionComparisonLimit bounds pairwise collision comparisons.
var nativeCollisionComparisonLimit = 4_000_000

func nativeCorrelationReplacementCollisions(plans []nativeCorrelationPlan, requestSurfaces string) (map[string]bool, map[int]bool, []nativeLiteralContainment) {
	type literalClaim struct {
		planIndex int
		literal   string
	}
	claims := make([]literalClaim, 0, len(plans)*2)
	for index, plan := range plans {
		seen := make(map[string]bool)
		add := func(literal string) {
			if literal == "" || seen[literal] {
				return
			}
			seen[literal] = true
			claims = append(claims, literalClaim{planIndex: index, literal: literal})
		}
		if plan.usage.raw {
			add(plan.value)
		}
		if plan.usage.encoded {
			add(plan.queryEncoded)
		}
		if plan.usage.unsupportedEncoded {
			for _, spelling := range unsupportedCorrelationEncodings(plan.value, plan.queryEncoded) {
				add(spelling)
			}
		}
	}

	ambiguousLiterals := make(map[string]bool)
	ambiguousPlans := make(map[int]bool)
	var containment []nativeLiteralContainment
	overlapBudget := paramize.NewLiteralOverlapSearchBudget()
	occurrences := overlapBudget.NewLiteralOccurrenceIndex(requestSurfaces)
	// Collision analysis is safety-critical. When a bound is exhausted at
	// claims[firstUnchecked], every pair involving an earlier claim has already
	// been compared, so those claims keep their checked outcome. Plans owning
	// any unchecked claim are suppressed whole rather than trusted partially.
	markUncheckedClaimsAmbiguous := func(firstUnchecked int) {
		unchecked := make(map[int]bool)
		for _, claim := range claims[firstUnchecked:] {
			unchecked[claim.planIndex] = true
		}
		for _, claim := range claims {
			if !unchecked[claim.planIndex] || !plans[claim.planIndex].automatic {
				continue
			}
			ambiguousLiterals[claim.literal] = true
			ambiguousPlans[claim.planIndex] = true
		}
	}
	comparisons := 0
	for leftIndex, left := range claims {
		for _, right := range claims[leftIndex+1:] {
			if left.planIndex == right.planIndex {
				continue
			}
			// Salesforce intentionally emits paired 18-character and SaveLength=15
			// variables from one response extractor. Their values overlap by design,
			// but longest-first replacement makes the two exact request forms safe.
			if nativeSalesforceIDVariantPlans(plans[left.planIndex], plans[right.planIndex]) {
				continue
			}
			leftAutomatic, rightAutomatic := plans[left.planIndex].automatic, plans[right.planIndex].automatic
			if !leftAutomatic && !rightAutomatic {
				continue
			}
			comparisons++
			if comparisons > nativeCollisionComparisonLimit {
				markUncheckedClaimsAmbiguous(leftIndex)
				return ambiguousLiterals, ambiguousPlans, containment
			}
			leftInRight := overlapBudget.Contains(right.literal, left.literal)
			rightInLeft := overlapBudget.Contains(left.literal, right.literal)
			if overlapBudget.Exhausted() {
				markUncheckedClaimsAmbiguous(leftIndex)
				return ambiguousLiterals, ambiguousPlans, containment
			}
			overlaps := false
			switch {
			case leftInRight && rightInLeft:
				// Two automatic producers of one spelling share it: each consumer
				// uses its closest preceding producer. A review-only owner must
				// keep its literal, so it still blocks the automatic one.
				overlaps = !leftAutomatic || !rightAutomatic
			case leftInRight && leftAutomatic:
				containment = append(containment, nativeLiteralContainment{inner: left.literal, outer: right.literal, outerAutomatic: rightAutomatic})
			case rightInLeft && rightAutomatic:
				containment = append(containment, nativeLiteralContainment{inner: right.literal, outer: left.literal, outerAutomatic: leftAutomatic})
			}
			// Containment is not the only unsafe shape: two literals whose
			// suffix/prefix merge really occurs on a rewrite-target surface would
			// be corrupted by single-pass replacement. Occurrence confirmation
			// keeps disjoint usage automatic.
			if !overlaps {
				overlaps = occurrences.PartiallyOverlap(left.literal, right.literal)
				if overlapBudget.Exhausted() {
					markUncheckedClaimsAmbiguous(leftIndex)
					return ambiguousLiterals, ambiguousPlans, containment
				}
			}
			if !overlaps {
				continue
			}
			ambiguousLiterals[left.literal] = true
			ambiguousLiterals[right.literal] = true
			ambiguousPlans[left.planIndex] = true
			ambiguousPlans[right.planIndex] = true
		}
	}
	return ambiguousLiterals, ambiguousPlans, containment
}

// nativePlanLiteralRemains reports whether any spelling of the plan's value is
// still literal after rewriting. A value whose only occurrences were inside a
// rewritten longer correlation needs no further replacement.
func nativePlanLiteralRemains(plan nativeCorrelationPlan, surfaces string) bool {
	for _, literal := range []string{plan.value, plan.queryEncoded} {
		if literal != "" && strings.Contains(surfaces, literal) {
			return true
		}
	}
	return false
}

// withoutInnerVarsOfLiteralOuters drops a contained variable from one action's
// replacement map when its automatic outer literal occurs in that action but is
// not being rewritten there (unmapped, or produced later). Rewriting the inner
// value would otherwise corrupt the surviving outer literal. A review-only
// outer does not hold its inner value back.
func withoutInnerVarsOfLiteralOuters(actionVars map[string]string, containment []nativeLiteralContainment, surfaces []string) map[string]string {
	if len(actionVars) == 0 || len(containment) == 0 {
		return actionVars
	}
	for changed := true; changed; {
		changed = false
		for _, contained := range containment {
			if !contained.outerAutomatic {
				continue
			}
			if _, inner := actionVars[contained.inner]; !inner {
				continue
			}
			if _, outer := actionVars[contained.outer]; outer {
				continue
			}
			for _, surface := range surfaces {
				if strings.Contains(surface, contained.outer) {
					delete(actionVars, contained.inner)
					changed = true
					break
				}
			}
		}
	}
	return actionVars
}

func nativeSalesforceIDVariantPlans(left, right nativeCorrelationPlan) bool {
	leftCorrelation := left.correlation
	rightCorrelation := right.correlation
	if leftCorrelation.FoundInID != rightCorrelation.FoundInID ||
		leftCorrelation.Source != rightCorrelation.Source ||
		leftCorrelation.ExtractType != rightCorrelation.ExtractType ||
		leftCorrelation.Expression != rightCorrelation.Expression ||
		leftCorrelation.Convert != rightCorrelation.Convert ||
		leftCorrelation.SaveOffset != rightCorrelation.SaveOffset {
		return false
	}
	if leftCorrelation.SaveLength == 15 && rightCorrelation.SaveLength == 0 {
		return len(left.value) == 15 && len(right.value) == 18 && strings.HasPrefix(right.value, left.value)
	}
	if rightCorrelation.SaveLength == 15 && leftCorrelation.SaveLength == 0 {
		return len(right.value) == 15 && len(left.value) == 18 && strings.HasPrefix(left.value, right.value)
	}
	return false
}

func correlationUsage(corr correlate.Correlation, refs map[int]*actionRef, sourceSequence int) correlationUse {
	use := correlationUse{}
	value := correlationValue(corr)
	if strings.TrimSpace(value) == "" {
		return use
	}
	queryEncoded := correlate.QueryEncodedFormOf(value)
	unsupportedEncoded := unsupportedCorrelationEncodings(value, queryEncoded)
	for _, harID := range corr.UsedInIDs {
		ref := refs[harID]
		if ref == nil || ref.Action == nil || ref.Sequence <= sourceSequence {
			continue
		}
		surfaces := actionCorrelationSurfaces(ref.Action)
		for _, candidate := range surfaces {
			if strings.Contains(candidate, value) {
				use.raw = true
			}
			if queryEncoded != "" && strings.Contains(candidate, queryEncoded) {
				use.encoded = true
			}
			for _, representation := range unsupportedEncoded {
				if strings.Contains(candidate, representation) {
					use.unsupportedEncoded = true
					break
				}
			}
		}
	}
	return use
}

func unsupportedCorrelationEncodings(value, queryEncoded string) []string {
	seen := map[string]bool{value: true, queryEncoded: true}
	forms := make([]string, 0, 8)
	appendForm := func(form string) {
		if form == "" || seen[form] {
			return
		}
		seen[form] = true
		forms = append(forms, form)
	}
	for _, form := range correlate.EncodedFormsOf(value) {
		appendForm(form)
		appendForm(lowerPercentEscapes(form))
	}
	appendForm(lowerPercentEscapes(queryEncoded))
	return forms
}

func lowerPercentEscapes(value string) string {
	encoded := []byte(value)
	for index := 0; index+2 < len(encoded); index++ {
		if encoded[index] != '%' {
			continue
		}
		for offset := 1; offset <= 2; offset++ {
			if encoded[index+offset] >= 'A' && encoded[index+offset] <= 'F' {
				encoded[index+offset] += 'a' - 'A'
			}
		}
		index += 2
	}
	return string(encoded)
}

// A url_decode source may drop its conversion for a query/form consumer only
// when the source capture already has QueryEscape's exact spelling. Equivalent
// decoded values are insufficient: %20 and '+' must not be interchanged.
func correlationSourceUsesExactQueryEncoding(validator *correlate.ExactQueryEncodingValidator, entries []harEntry, corr correlate.Correlation, queryEncoded string) bool {
	if queryEncoded == "" {
		return false
	}
	var source *harEntry
	for index := range entries {
		if entries[index].ID == corr.FoundInID {
			source = &entries[index]
			break
		}
	}
	if source == nil {
		return false
	}
	return validator.Matches(corr.FoundInID, source.ResponseBody, source.ResponseHeaders, corr, queryEncoded)
}

// correlationValue returns the concrete value a correlation substitutes, mirroring
// correlate.BuildVarMap (Value, falling back to the truncated SampleValue).
func correlationValue(corr correlate.Correlation) string {
	if corr.Value != "" {
		return corr.Value
	}
	return corr.SampleValue
}

// joinedActionCorrelationSurfaces concatenates every rewrite-target action
// surface with a NUL separator so one strings.Contains tests whether a merged
// literal spelling occurs on any single surface without matching across
// boundaries.
func joinedActionCorrelationSurfaces(refs []actionRef) string {
	var builder strings.Builder
	for index := range refs {
		for _, surface := range actionCorrelationSurfaces(refs[index].Action) {
			if surface == "" {
				continue
			}
			builder.WriteString(surface)
			builder.WriteByte(0)
		}
	}
	return builder.String()
}

func actionCorrelationSurfaces(action *dsl.Action) []string {
	if action == nil {
		return nil
	}
	surfaces := []string{action.Target, action.Body}
	for _, value := range action.Headers {
		surfaces = append(surfaces, value)
	}
	for _, header := range action.HeaderList {
		surfaces = append(surfaces, header.Value)
	}
	return surfaces
}

func applyCorrelationVarsToHeaderList(headers dsl.HeaderList, vars map[string]string) dsl.HeaderList {
	for index := range headers {
		if correlate.HeaderCorrelatable(headers[index].Name) {
			headers[index].Value = correlate.ApplyVars(headers[index].Value, vars)
		}
	}
	return headers
}

// nativeLiteralProducer is one automatic correlation able to supply a literal.
// Salesforce often issues the same value twice (the OAuth access token is also
// the sid cookie), so a literal can have several producers.
type nativeLiteralProducer struct {
	sequence int
	template string
}

// correlationVarsAvailableAfterSequence prevents an earlier native action from
// referencing a variable whose extractor is attached to a later response. HAR
// and native-script orders can diverge, and the same literal may also occur
// incidentally before its actual response source. When several producers
// supply the same literal, the closest preceding one wins (the first planned
// on a tie), matching a fresh value reissued later in the flow.
func correlationVarsAvailableAfterSequence(vars map[string][]nativeLiteralProducer, sequence int) map[string]string {
	if len(vars) == 0 {
		return nil
	}
	available := make(map[string]string, len(vars))
	for literal, producers := range vars {
		best := -1
		for index, producer := range producers {
			if producer.sequence < sequence && (best < 0 || producer.sequence > producers[best].sequence) {
				best = index
			}
		}
		if best >= 0 {
			available[literal] = producers[best].template
		}
	}
	return available
}

func harCorrelationSurfaces(entries []harEntry) map[int][]string {
	out := make(map[int][]string, len(entries))
	for _, entry := range entries {
		surfaces := []string{entry.URL, entry.RequestBody}
		for _, value := range entry.RequestHeaders {
			surfaces = append(surfaces, value)
		}
		out[entry.ID] = surfaces
	}
	return out
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

const (
	maxSalesforceShapeCandidates   = 4096
	maxSalesforceShapeCorrelations = 4096
	// Matches the shared detector's ceiling. Request surfaces are decoded once
	// per analysis (correlate.RequestSearch), so complete analysis of real
	// Lightning captures charges 1-3 GiB and scans in about a second; the old
	// 64 MiB ceiling stopped after a few dozen candidates.
	maxSalesforceShapeSearchBytes  = int64(8 << 30)
	maxSalesforceConstructionBytes = int64(16 << 20)
	maxSalesforceJSONTokens        = 8192
	maxSalesforceJSONDepth         = 128
)

// salesforceCorrelationBudget bounds the extra Salesforce-shape passes that
// run after the shared checked detector. In particular, composite/nested
// candidates otherwise rescan every later request and the Bulk locator pass is
// quadratic in the number of observations.
type salesforceCorrelationBudget struct {
	candidates           int
	correlations         int
	searchBytes          int64
	constructionBytes    int64
	maxCandidates        int
	maxCorrelations      int
	maxSearchBytes       int64
	maxConstructionBytes int64
	// requests caches each observation's searchable request surfaces for the
	// observation slice this budget's analysis searches.
	requests      *correlate.RequestSearch
	requestsFirst *correlate.Observation
	requestsSize  int
}

// requestSearch returns the cached surfaces for observations, rebuilding them
// when a different slice (not merely a different length) is passed.
func (budget *salesforceCorrelationBudget) requestSearch(observations []correlate.Observation) *correlate.RequestSearch {
	var first *correlate.Observation
	if len(observations) > 0 {
		first = &observations[0]
	}
	if budget.requests == nil || budget.requestsFirst != first || budget.requestsSize != len(observations) {
		budget.requests = correlate.NewRequestSearch(observations)
		budget.requestsFirst = first
		budget.requestsSize = len(observations)
	}
	return budget.requests
}

func newSalesforceCorrelationBudget() *salesforceCorrelationBudget {
	return &salesforceCorrelationBudget{
		maxCandidates:        maxSalesforceShapeCandidates,
		maxCorrelations:      maxSalesforceShapeCorrelations,
		maxSearchBytes:       maxSalesforceShapeSearchBytes,
		maxConstructionBytes: maxSalesforceConstructionBytes,
	}
}

func (budget *salesforceCorrelationBudget) chargeCandidates(count int) error {
	if budget == nil || count < 0 || count > budget.maxCandidates-budget.candidates {
		return fmt.Errorf("%w: Salesforce shape candidates exceed %d", correlate.ErrCorrelationWorkLimit, maxSalesforceShapeCandidates)
	}
	budget.candidates += count
	return nil
}

func (budget *salesforceCorrelationBudget) remainingCandidates() int {
	if budget == nil || budget.maxCandidates <= budget.candidates {
		return 0
	}
	return budget.maxCandidates - budget.candidates
}

func (budget *salesforceCorrelationBudget) chargeConstruction(bytes int64) error {
	if budget == nil || bytes < 0 || bytes > budget.maxConstructionBytes-budget.constructionBytes {
		return fmt.Errorf("%w: Salesforce candidate/path construction exceeds %d bytes", correlate.ErrCorrelationWorkLimit, maxSalesforceConstructionBytes)
	}
	budget.constructionBytes += bytes
	return nil
}

func salesforceCandidateCapacity(requested int, budget *salesforceCorrelationBudget) int {
	remaining := budget.remainingCandidates()
	if requested < remaining {
		return requested
	}
	return remaining
}

func (budget *salesforceCorrelationBudget) chargeCorrelation() error {
	if budget == nil || budget.correlations >= budget.maxCorrelations {
		return fmt.Errorf("%w: Salesforce shape correlations exceed %d", correlate.ErrCorrelationWorkLimit, maxSalesforceShapeCorrelations)
	}
	budget.correlations++
	return nil
}

func (budget *salesforceCorrelationBudget) chargeSearch(observation correlate.Observation) error {
	// A reuse query scans raw URL/body/headers, their query-decoded forms, and
	// parsed form values. Decoding cannot expand the bytes, and parsed form
	// keys/values are bounded by the body, so this is a conservative upper bound
	// for one search.
	cost := int64(2*len(observation.RequestURL) + 3*len(observation.RequestBody))
	for _, value := range observation.RequestHeaders {
		cost += int64(2 * len(value))
	}
	if cost < 0 || cost > budget.maxSearchBytes-budget.searchBytes {
		return fmt.Errorf("%w: Salesforce request-surface search exceeds %d bytes", correlate.ErrCorrelationWorkLimit, maxSalesforceShapeSearchBytes)
	}
	budget.searchBytes += cost
	return nil
}

func (budget *salesforceCorrelationBudget) chargeSearchBytes(cost int64) error {
	if cost < 0 || cost > budget.maxSearchBytes-budget.searchBytes {
		return fmt.Errorf("%w: Salesforce request-surface search exceeds %d bytes", correlate.ErrCorrelationWorkLimit, maxSalesforceShapeSearchBytes)
	}
	budget.searchBytes += cost
	return nil
}

// validateSalesforceJSONShape performs a streaming structural pass before any
// shape-specific decoder materializes maps/slices or recursively constructs
// JMESPath strings. A response may be large because it contains one large
// scalar, but node fan-out and nesting are capped independently so a <16 MiB
// adversarial JSON document cannot trigger unbounded candidate/path work.
// Documents beyond the caps (standard Aura bootstrap and 2000-record SOQL
// pages routinely are) return valid=false: that single response's shape pass
// is skipped rather than failing the whole analysis.
func validateSalesforceJSONShape(body string) (bool, error) {
	if strings.TrimSpace(body) == "" {
		return false, nil
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	tokens := 0
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return depth == 0, nil
		}
		if err != nil {
			return false, nil //nolint:nilerr // malformed JSON returns valid=false and this response's shape pass is skipped
		}
		tokens++
		if tokens > maxSalesforceJSONTokens {
			return false, nil
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delimiter {
		case '{', '[':
			depth++
			if depth > maxSalesforceJSONDepth {
				return false, nil
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return false, nil
			}
		}
	}
}

func detectCorrelations(entries []harEntry) []correlate.Correlation {
	correlations, _, _ := detectCorrelationsChecked(entries)
	return correlations
}

func detectCorrelationsChecked(entries []harEntry) ([]correlate.Correlation, []string, error) {
	observations := make([]correlate.Observation, 0, len(entries))
	for _, entry := range entries {
		observations = append(observations, correlate.Observation{
			ID:                     entry.ID,
			RequestURL:             entry.URL,
			RequestBody:            entry.RequestBody,
			RequestBodyTransformed: entry.RequestBodyTransformed,
			RequestHeaders:         entry.RequestHeaders,
			ResponseBody:           entry.ResponseBody,
			ResponseHeaders:        entry.ResponseHeaders,
		})
	}
	genericCorrelations, warnings, err := correlate.DetectCheckedWithWarnings(observations)
	if err != nil {
		if !errors.Is(err, correlate.ErrCorrelationWorkLimit) {
			return nil, nil, err
		}
		// Input pathological enough for the generic validator is not re-scanned
		// by the Salesforce-shape passes either: degrade to no correlations with
		// a note, keeping the surrounding review intact.
		return nil, append(warnings, err.Error()), nil
	}
	seenValues := make(map[string]bool)
	// Salesforce-shape-aware passes run first so their precise JMESPath extractors
	// pre-empt the generic nested-JSON candidate for the same value (dedup is by
	// value via seenValues): CometD clientId (channel-filtered array lookup),
	// composite referenceId (referenceId-scoped lookup), and Bulk query locator
	// (header cursor reused as a locator query parameter).
	budget := newSalesforceCorrelationBudget()
	correlations := make([]correlate.Correlation, 0)
	for _, phase := range []func([]correlate.Observation, map[string]bool, *salesforceCorrelationBudget) ([]correlate.Correlation, error){
		detectCometdCorrelations,
		detectCompositeCorrelations,
		detectBulkQueryLocatorCorrelations,
		detectNestedJSONCorrelations,
	} {
		phaseCorrelations, phaseErr := phase(observations, seenValues, budget)
		correlations = append(correlations, phaseCorrelations...)
		if phaseErr != nil {
			if !errors.Is(phaseErr, correlate.ErrCorrelationWorkLimit) {
				return nil, nil, phaseErr
			}
			// Budget exhaustion keeps every correlation found so far and stops
			// the remaining shape passes instead of failing the analysis.
			warnings = append(warnings, "Salesforce-shape correlation analysis was truncated: "+phaseErr.Error())
			break
		}
	}
	// Shape extractors pre-empt the generic candidate for the same value only
	// when they read it from the same or an earlier response. An earlier
	// generic producer (for example Set-Cookie sid before an Aura response
	// echoing sfdcSessionKey) must keep the value, or every request between
	// the two producers loses its variable.
	firstGeneric := make(map[string]correlate.Correlation, len(genericCorrelations))
	for _, corr := range genericCorrelations {
		if _, found := firstGeneric[corr.Value]; !found {
			firstGeneric[corr.Value] = corr
		}
	}
	for index, corr := range correlations {
		if generic, found := firstGeneric[corr.Value]; found && generic.FoundInID < corr.FoundInID {
			correlations[index] = generic
		}
	}
	for _, corr := range genericCorrelations {
		if seenValues[corr.Value] {
			continue
		}
		seenValues[corr.Value] = true
		correlations = append(correlations, corr)
		if len(correlations) >= maxSalesforceShapeCorrelations {
			warnings = append(warnings, fmt.Sprintf("combined Salesforce correlations were truncated at %d", maxSalesforceShapeCorrelations))
			break
		}
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
	return correlations, warnings, nil
}

// detectCometdCorrelations finds the Bayeux clientId returned by a CometD
// /meta/handshake response (a JSON array) that a LATER /meta/connect or
// /meta/subscribe request reuses, and emits a channel-filtered array-aware
// JMESPath extractor. The handshake response is array-rooted, so a "$.clientId"
// jsonpath would error at replay; the channel filter is also more robust than a
// positional [0].clientId when the response carries multiple messages.
func detectCometdCorrelations(observations []correlate.Observation, seenValues map[string]bool, budget *salesforceCorrelationBudget) ([]correlate.Correlation, error) {
	correlations := make([]correlate.Correlation, 0)
	for index, observation := range observations {
		candidates, err := cometdCandidatesChecked(observation.ResponseBody, budget)
		if err != nil {
			return correlations, err
		}
		for _, candidate := range candidates {
			if candidate.Value == "" {
				continue
			}
			reuses, err := salesforceCorrelatableReuses(observations, index, candidate, budget)
			if err != nil {
				return correlations, err
			}
			for _, reuse := range reuses {
				if seenValues[reuse.value] {
					continue
				}
				if err := budget.chargeCorrelation(); err != nil {
					return correlations, err
				}
				seenValues[reuse.value] = true
				correlations = append(correlations, salesforceCorrelationFromCandidate(candidate, observation, reuse))
			}
		}
	}
	return correlations, nil
}

// cometdCandidates parses a CometD handshake response (a JSON array of Bayeux
// messages) and emits the clientId from the /meta/handshake message.
func cometdCandidates(body string) []correlate.Candidate {
	candidates, _ := cometdCandidatesChecked(body, newSalesforceCorrelationBudget())
	return candidates
}

func cometdCandidatesChecked(body string, budget *salesforceCorrelationBudget) ([]correlate.Candidate, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return nil, nil
	}
	valid, err := validateSalesforceJSONShape(trimmed)
	if err != nil || !valid {
		return nil, err
	}
	var messages []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &messages); err != nil {
		return nil, nil //nolint:nilerr // a body that is not CometD JSON yields no candidates
	}
	for _, msg := range messages {
		if channel, _ := msg["channel"].(string); channel != "/meta/handshake" {
			continue
		}
		clientID, _ := msg["clientId"].(string)
		if clientID == "" {
			continue
		}
		if err := budget.chargeCandidates(1); err != nil {
			return nil, err
		}
		return []correlate.Candidate{{
			Name:           "corr_salesforce_cometd_client_id",
			Value:          clientID,
			Source:         "body",
			ExtractType:    "jmespath",
			Expression:     "[?channel=='/meta/handshake'].clientId | [0]",
			RuleID:         correlate.RuleSalesforceCometdClientID,
			BaseConfidence: 0.95,
			Reason:         "Salesforce CometD Bayeux clientId",
		}}, nil
	}
	return nil, nil
}

// detectCompositeCorrelations finds server-assigned record ids returned by a
// Salesforce /composite call (keyed by referenceId) that a LATER, separate
// request reuses, and emits a referenceId-scoped JMESPath extractor so the live
// id is correlated rather than the recorded literal. Intra-request @{ref.field}
// placeholders are resolved server-side and need no handling (the engine only
// expands {{...}} tokens, so @{...} passes through verbatim).
func detectCompositeCorrelations(observations []correlate.Observation, seenValues map[string]bool, budget *salesforceCorrelationBudget) ([]correlate.Correlation, error) {
	correlations := make([]correlate.Correlation, 0)
	for index, observation := range observations {
		if !strings.Contains(strings.ToLower(observation.RequestURL), "/composite") {
			continue
		}
		candidates, err := compositeCandidatesChecked(observation.ResponseBody, budget)
		if err != nil {
			return correlations, err
		}
		for _, candidate := range candidates {
			if candidate.Value == "" {
				continue
			}
			reuses, err := salesforceCorrelatableReuses(observations, index, candidate, budget)
			if err != nil {
				return correlations, err
			}
			for _, reuse := range reuses {
				if seenValues[reuse.value] {
					continue
				}
				if err := budget.chargeCorrelation(); err != nil {
					return correlations, err
				}
				seenValues[reuse.value] = true
				correlations = append(correlations, salesforceCorrelationFromCandidate(candidate, observation, reuse))
			}
		}
	}
	return correlations, nil
}

// compositeCandidates parses Salesforce composite-family response shapes and
// emits one candidate per subrequest/result that returns a record id, addressed
// by its referenceId so the extractor is unambiguous even when multiple
// subrequests each return an id.
func compositeCandidates(body string) []correlate.Candidate {
	candidates, _ := compositeCandidatesChecked(body, newSalesforceCorrelationBudget())
	return candidates
}

func compositeCandidatesChecked(body string, budget *salesforceCorrelationBudget) ([]correlate.Candidate, error) {
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	valid, err := validateSalesforceJSONShape(body)
	if err != nil || !valid {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil, nil //nolint:nilerr // a body that is not composite JSON yields no candidates
	}
	usedNames := map[string]int{}
	if entries, ok := decoded["compositeResponse"].([]any); ok {
		return flatCompositeCandidates(entries, usedNames, budget)
	}
	if entries, ok := decoded["results"].([]any); ok {
		return treeCompositeCandidates(entries, usedNames, budget)
	}
	if entries, ok := decoded["graphs"].([]any); ok {
		return graphCompositeCandidates(entries, usedNames, budget)
	}
	return nil, nil
}

func flatCompositeCandidates(entries []any, usedNames map[string]int, budget *salesforceCorrelationBudget) ([]correlate.Candidate, error) {
	candidates := make([]correlate.Candidate, 0, salesforceCandidateCapacity(len(entries), budget))
	usedExpressions := make(map[string]bool)
	for _, raw := range entries {
		sub, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ref, _ := sub["referenceId"].(string)
		if ref == "" || !safeCompositeReferenceID(ref) {
			continue
		}
		if status, ok := sub["httpStatusCode"].(float64); ok && status >= 300 {
			continue
		}
		bodyObj, ok := sub["body"].(map[string]any)
		if !ok {
			continue
		}
		id, _ := bodyObj["id"].(string)
		if id == "" {
			continue
		}
		if err := budget.chargeConstruction(512 + 6*int64(len(ref))); err != nil {
			return nil, err
		}
		if err := budget.chargeCandidates(1); err != nil {
			return nil, err
		}
		expression := "compositeResponse[?referenceId=='" + ref + "'].body.id | [0]"
		if usedExpressions[expression] {
			// The expression selects [0], so a duplicate referenceId can only
			// replay the first value. Suppress later values instead of emitting an
			// extractor that deterministically returns the wrong record id.
			continue
		}
		usedExpressions[expression] = true
		candidates = append(candidates, compositeRecordIDCandidate(
			usedNames,
			"salesforce_composite_"+ref,
			id,
			expression,
			"Salesforce composite subrequest '"+ref+"' record id",
		))
	}
	return candidates, nil
}

func treeCompositeCandidates(entries []any, usedNames map[string]int, budget *salesforceCorrelationBudget) ([]correlate.Candidate, error) {
	candidates := make([]correlate.Candidate, 0, salesforceCandidateCapacity(len(entries), budget))
	usedExpressions := make(map[string]bool)
	for _, raw := range entries {
		result, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ref, _ := result["referenceId"].(string)
		if ref == "" || !safeCompositeReferenceID(ref) {
			continue
		}
		id, _ := result["id"].(string)
		if id == "" {
			continue
		}
		if err := budget.chargeConstruction(384 + 6*int64(len(ref))); err != nil {
			return nil, err
		}
		if err := budget.chargeCandidates(1); err != nil {
			return nil, err
		}
		expression := "results[?referenceId=='" + ref + "'].id | [0]"
		if usedExpressions[expression] {
			continue
		}
		usedExpressions[expression] = true
		candidates = append(candidates, compositeRecordIDCandidate(
			usedNames,
			"salesforce_composite_tree_"+ref,
			id,
			expression,
			"Salesforce composite tree result '"+ref+"' record id",
		))
	}
	return candidates, nil
}

func graphCompositeCandidates(graphs []any, usedNames map[string]int, budget *salesforceCorrelationBudget) ([]correlate.Candidate, error) {
	candidates := make([]correlate.Candidate, 0, salesforceCandidateCapacity(len(graphs), budget))
	usedExpressions := make(map[string]bool)
	for _, rawGraph := range graphs {
		graph, ok := rawGraph.(map[string]any)
		if !ok {
			continue
		}
		graphID, _ := graph["graphId"].(string)
		if graphID == "" || !safeCompositeReferenceID(graphID) {
			continue
		}
		graphResponse, ok := graph["graphResponse"].(map[string]any)
		if !ok {
			continue
		}
		entries, ok := graphResponse["compositeResponse"].([]any)
		if !ok {
			continue
		}
		for _, raw := range entries {
			sub, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			ref, _ := sub["referenceId"].(string)
			if ref == "" || !safeCompositeReferenceID(ref) {
				continue
			}
			if status, ok := sub["httpStatusCode"].(float64); ok && status >= 300 {
				continue
			}
			bodyObj, ok := sub["body"].(map[string]any)
			if !ok {
				continue
			}
			id, _ := bodyObj["id"].(string)
			if id == "" {
				continue
			}
			if err := budget.chargeConstruction(768 + 6*int64(len(graphID)+len(ref))); err != nil {
				return nil, err
			}
			if err := budget.chargeCandidates(1); err != nil {
				return nil, err
			}
			expression := "graphs[?graphId=='" + graphID + "'].graphResponse.compositeResponse[] | [?referenceId=='" + ref + "'].body.id | [0]"
			if usedExpressions[expression] {
				continue
			}
			usedExpressions[expression] = true
			candidates = append(candidates, compositeRecordIDCandidate(
				usedNames,
				"salesforce_composite_graph_"+graphID+"_"+ref,
				id,
				expression,
				"Salesforce composite graph '"+graphID+"' subrequest '"+ref+"' record id",
			))
		}
	}
	return candidates, nil
}

func compositeRecordIDCandidate(usedNames map[string]int, nameSeed, id, expression, reason string) correlate.Candidate {
	// usedNames disambiguates ids that slug to the same name (e.g. "Acct-1" and
	// "Acct_1" both slug to ..._acct_1). Without this, the second extractor
	// would be dropped as a duplicate Name and both record ids would be rewritten
	// to the first referenceId's variable — a silent id mismatch.
	name := "corr_" + correlate.Slug(nameSeed)
	usedNames[name]++
	if n := usedNames[name]; n > 1 {
		name = fmt.Sprintf("%s_%d", name, n)
	}
	return correlate.Candidate{
		Name:           name,
		Value:          id,
		Source:         "body",
		ExtractType:    "jmespath",
		Expression:     expression,
		RuleID:         correlate.RuleSalesforceCompositeReference,
		BaseConfidence: 0.85,
		Reason:         reason,
	}
}

// safeCompositeReferenceID restricts referenceIds embedded in a JMESPath string
// literal to a quote-free charset so a crafted/unusual referenceId cannot break
// the generated expression. Real Salesforce referenceIds are simple identifiers.
func safeCompositeReferenceID(ref string) bool {
	for _, r := range ref {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func salesforceID15(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !salesforceIDPattern.MatchString(value) {
		return "", false
	}
	switch len(value) {
	case 15:
		return value, true
	case 18:
		return value[:15], true
	default:
		return "", false
	}
}

type salesforceCandidateReuse struct {
	value        string
	nameSuffix   string
	saveLength   int
	usedIn       []int
	reasonSuffix string
}

func salesforceCandidateReuses(observations []correlate.Observation, startIndex int, value string, budget *salesforceCorrelationBudget) ([]salesforceCandidateReuse, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	exactUsed := make([]int, 0)
	for later := startIndex + 1; later < len(observations); later++ {
		if err := budget.chargeSearch(observations[later]); err != nil {
			return nil, err
		}
		if budget.requestSearch(observations).Contains(later, value) {
			exactUsed = append(exactUsed, observations[later].ID)
		}
	}

	out := make([]salesforceCandidateReuse, 0, 2)
	if len(exactUsed) > 0 {
		out = append(out, salesforceCandidateReuse{
			value:  value,
			usedIn: exactUsed,
		})
	}

	id15, ok := salesforceID15(value)
	if !ok || len(value) != 18 || id15 == value {
		return out, nil
	}
	truncatedUsed := make([]int, 0)
	for later := startIndex + 1; later < len(observations); later++ {
		if err := budget.chargeSearch(observations[later]); err != nil {
			return nil, err
		}
		if observationContainsSalesforceID15(observations[later], id15) {
			truncatedUsed = append(truncatedUsed, observations[later].ID)
		}
	}
	if len(truncatedUsed) > 0 {
		nameSuffix := ""
		if len(exactUsed) > 0 {
			nameSuffix = "_15"
		}
		out = append(out, salesforceCandidateReuse{
			value:        id15,
			nameSuffix:   nameSuffix,
			saveLength:   15,
			usedIn:       truncatedUsed,
			reasonSuffix: " (18-character Salesforce id trimmed to 15-character request form)",
		})
	}
	return out, nil
}

// salesforceCorrelatableReuses applies the shared precision rules to a shape
// candidate: values that are not server state are never correlated, and weak
// heuristic values the client already sent before the producer are dropped.
func salesforceCorrelatableReuses(observations []correlate.Observation, index int, candidate correlate.Candidate, budget *salesforceCorrelationBudget) ([]salesforceCandidateReuse, error) {
	if !correlate.CandidateCanCorrelate(candidate) {
		return nil, nil
	}
	reuses, err := salesforceCandidateReuses(observations, index, candidate.Value, budget)
	if err != nil || len(reuses) == 0 || !correlate.ClientOriginCheckApplies(candidate.RuleID) {
		return reuses, err
	}
	kept := reuses[:0]
	for _, reuse := range reuses {
		sent, err := budget.requestSearch(observations).ClientSentThrough(index, reuse.value, budget.chargeSearchBytes)
		if err != nil {
			return nil, err
		}
		if !sent {
			kept = append(kept, reuse)
		}
	}
	return kept, nil
}

func observationContainsSalesforceID15(observation correlate.Observation, id15 string) bool {
	for _, surface := range observationRequestSurfaces(observation) {
		if containsBoundedAlnum(surface, id15) {
			return true
		}
		if decoded, err := url.QueryUnescape(surface); err == nil && decoded != surface && containsBoundedAlnum(decoded, id15) {
			return true
		}
	}
	return false
}

func observationRequestSurfaces(observation correlate.Observation) []string {
	surfaces := []string{observation.RequestURL, observation.RequestBody}
	for _, value := range observation.RequestHeaders {
		surfaces = append(surfaces, value)
	}
	return surfaces
}

func containsBoundedAlnum(value, needle string) bool {
	if needle == "" {
		return false
	}
	start := 0
	for {
		index := strings.Index(value[start:], needle)
		if index < 0 {
			return false
		}
		index += start
		beforeOK := index == 0 || !asciiAlnum(value[index-1])
		after := index + len(needle)
		afterOK := after == len(value) || !asciiAlnum(value[after])
		if beforeOK && afterOK {
			return true
		}
		start = index + 1
	}
}

func asciiAlnum(c byte) bool {
	return c >= '0' && c <= '9' ||
		c >= 'A' && c <= 'Z' ||
		c >= 'a' && c <= 'z'
}

func salesforceCorrelationFromCandidate(candidate correlate.Candidate, observation correlate.Observation, reuse salesforceCandidateReuse) correlate.Correlation {
	return correlate.Correlation{
		Name:        candidate.Name + reuse.nameSuffix,
		Source:      candidate.Source,
		Expression:  candidate.Expression,
		ExtractType: candidate.ExtractType,
		SaveLength:  reuse.saveLength,
		FoundInID:   observation.ID,
		UsedInIDs:   reuse.usedIn,
		Value:       reuse.value,
		SampleValue: correlate.Truncate(reuse.value, 80),
		RuleID:      candidate.RuleID,
		Confidence:  candidate.BaseConfidence,
		Reasons:     []string{candidate.Reason + reuse.reasonSuffix},
	}
}

func detectBulkQueryLocatorCorrelations(observations []correlate.Observation, seenValues map[string]bool, budget *salesforceCorrelationBudget) ([]correlate.Correlation, error) {
	correlations := make([]correlate.Correlation, 0)
	for index, observation := range observations {
		if !isBulkQueryResultsURL(observation.RequestURL) {
			continue
		}
		locator := bulkQueryLocatorHeader(observation.ResponseHeaders)
		if locator == "" || seenValues[locator] {
			continue
		}
		if err := budget.chargeCandidates(1); err != nil {
			return correlations, err
		}
		usedIn := make([]int, 0)
		for later := index + 1; later < len(observations); later++ {
			if err := budget.chargeSearch(observations[later]); err != nil {
				return correlations, err
			}
			if budget.requestSearch(observations).Contains(later, locator) {
				usedIn = append(usedIn, observations[later].ID)
			}
		}
		if len(usedIn) == 0 {
			continue
		}
		if err := budget.chargeCorrelation(); err != nil {
			return correlations, err
		}
		seenValues[locator] = true
		correlations = append(correlations, correlate.Correlation{
			Name:        "corr_salesforce_bulk_query_locator",
			Source:      "header",
			Expression:  "Sforce-Locator",
			ExtractType: "header",
			FoundInID:   observation.ID,
			UsedInIDs:   usedIn,
			Value:       locator,
			SampleValue: correlate.Truncate(locator, 80),
			RuleID:      correlate.RuleSalesforceBulkQueryLocator,
			Confidence:  0.9,
			Reasons:     []string{"Salesforce Bulk API query Sforce-Locator response header"},
		})
	}
	return correlations, nil
}

func isBulkQueryResultsURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return false
	}
	path := strings.ToLower(parsed.EscapedPath())
	return strings.Contains(path, "/services/data/") &&
		strings.Contains(path, "/jobs/query/") &&
		strings.HasSuffix(path, "/results")
}

func bulkQueryLocatorHeader(headers map[string]string) string {
	for key, value := range headers {
		if !strings.EqualFold(key, "Sforce-Locator") {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.EqualFold(value, "null") {
			return ""
		}
		return value
	}
	return ""
}

func detectNestedJSONCorrelations(observations []correlate.Observation, seenValues map[string]bool, budget *salesforceCorrelationBudget) ([]correlate.Correlation, error) {
	correlations := make([]correlate.Correlation, 0)
	for index, observation := range observations {
		candidates, err := nestedJSONCandidatesChecked(observation.ResponseBody, budget)
		if err != nil {
			return correlations, err
		}
		for _, candidate := range candidates {
			if candidate.Value == "" {
				continue
			}
			reuses, err := salesforceCorrelatableReuses(observations, index, candidate, budget)
			if err != nil {
				return correlations, err
			}
			for _, reuse := range reuses {
				if seenValues[reuse.value] {
					continue
				}
				if err := budget.chargeCorrelation(); err != nil {
					return correlations, err
				}
				seenValues[reuse.value] = true
				correlations = append(correlations, salesforceCorrelationFromCandidate(candidate, observation, reuse))
			}
		}
	}
	return correlations, nil
}

func nestedJSONCandidates(body string) []correlate.Candidate {
	candidates, _ := nestedJSONCandidatesChecked(body, newSalesforceCorrelationBudget())
	return candidates
}

func nestedJSONCandidatesChecked(body string, budget *salesforceCorrelationBudget) ([]correlate.Candidate, error) {
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	valid, err := validateSalesforceJSONShape(body)
	if err != nil || !valid {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, nil //nolint:nilerr // a body that is not one JSON document yields no candidates
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil
	}
	candidates := make([]correlate.Candidate, 0)
	var walk func(value any, path string, key string, depth int) error
	walk = func(value any, path string, key string, depth int) error {
		if depth > maxSalesforceJSONDepth {
			return fmt.Errorf("%w: Salesforce JSON nesting exceeds %d", correlate.ErrCorrelationWorkLimit, maxSalesforceJSONDepth)
		}
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for childKey := range typed {
				keys = append(keys, childKey)
			}
			sort.Strings(keys)
			for _, childKey := range keys {
				childValue := typed[childKey]
				// strconv.Quote can expand one input byte to as many as six output
				// bytes (\u00XX). Charge conservatively before constructing the path.
				if err := budget.chargeConstruction(int64(len(path)+4) + 6*int64(len(childKey))); err != nil {
					return err
				}
				if err := walk(childValue, appendJMESPath(path, childKey), childKey, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for index, childValue := range typed {
				indexText := strconv.Itoa(index)
				if err := budget.chargeConstruction(int64(len(path) + len(indexText) + 2)); err != nil {
					return err
				}
				if err := walk(childValue, path+"["+indexText+"]", key, depth+1); err != nil {
					return err
				}
			}
		case string:
			if len(typed) >= 6 && dynamicJSONKey(key) {
				if err := budget.chargeConstruction(256 + int64(len(path)) + 6*int64(len(key))); err != nil {
					return err
				}
				if err := budget.chargeCandidates(1); err != nil {
					return err
				}
				candidates = append(candidates, nestedJSONCandidate(key, typed, path, false))
			}
		case json.Number:
			if dynamicJSONKey(key) {
				value := typed.String()
				if len(value) >= 4 {
					if err := budget.chargeConstruction(256 + int64(len(path)+len(value)) + 6*int64(len(key))); err != nil {
						return err
					}
					if err := budget.chargeCandidates(1); err != nil {
						return err
					}
					candidates = append(candidates, nestedJSONCandidate(key, value, path, true))
				}
			}
		}
		return nil
	}
	if err := walk(decoded, "", "", 0); err != nil {
		return nil, err
	}
	return candidates, nil
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
		quotedKey, err := json.Marshal(key)
		if err != nil {
			return ""
		}
		segment = string(quotedKey)
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

const (
	maxAuraParamInspectionBytes = 64 << 20
	maxAuraParamCandidateBytes  = 128 << 10
	maxAuraParamCandidates      = 2048
)

// auraMessageParameterizations is retained for callers interested only in the
// candidates. Analyze uses the checked form so incomplete coverage is visible.
func auraMessageParameterizations(refs []actionRef, correlations []correlate.Correlation) []ParameterizationCandidate {
	candidates, _ := auraMessageParameterizationsChecked(refs, correlations)
	return candidates
}

func auraMessageParameterizationsChecked(refs []actionRef, correlations []correlate.Correlation) ([]ParameterizationCandidate, []string) {
	correlated := make(map[string]bool, len(correlations))
	for _, corr := range correlations {
		if corr.Value != "" {
			correlated[corr.Value] = true
		}
	}
	usedNames := map[string]bool{}
	out := make([]ParameterizationCandidate, 0)
	warnings := make([]string, 0)
	work, retainedBytes, omitted, uninspected, rejected, unsupported, unsafePaths := 0, 0, 0, 0, 0, 0, 0
	detailsFull := false
	for _, ref := range refs {
		if ref.Action == nil || ref.Action.Body == "" || ref.Kind != "aura" && ref.Kind != "" {
			continue
		}
		remaining := maxAuraParamInspectionBytes - work
		if len(ref.Action.Body) > remaining {
			uninspected++
			continue
		}
		inspection, err := aurapayload.InspectWithWorkLimit(ref.Action.Body, ref.Action.BodyEncoding, remaining)
		if err != nil {
			if bounded, ok := err.(*aurapayload.Error); ok {
				work += bounded.WorkBytes
				if bounded.Code == aurapayload.WorkLimit && remaining < aurapayload.MaxWorkBytes {
					uninspected++
					continue
				}
			}
			rejected++
			continue
		}
		work += inspection.WorkBytes
		for _, leaf := range inspection.Leaves {
			if !auraBusinessParamRoute(leaf.Route) {
				continue
			}
			if leaf.ValueType != "string" {
				if paramize.LooksAuraParameterName(leaf.Key) {
					unsupported++
				}
				continue
			}
			kind, ok := auraParamIdentifierKind(leaf.Key, leaf.Value)
			if !ok || correlated[leaf.Value] || strings.Contains(leaf.Value, "{{") {
				continue
			}
			if !paramize.SafeAuraParameterRoute(leaf.Route) {
				unsafePaths++
				continue
			}
			if detailsFull {
				omitted++
				continue
			}
			column := correlate.Slug(leaf.Key)
			if len(column) > 64 {
				column = column[:64]
			}
			if column == "" {
				column = "record_id"
			}
			name := "param_" + column
			for suffix := 2; usedNames[name]; suffix++ {
				name = fmt.Sprintf("param_%s_%d", column, suffix)
			}
			column = strings.TrimPrefix(name, "param_")
			expression := auraParamExpression(leaf.Route)
			candidate := ParameterizationCandidate{
				Name: name, Source: "aura_message", Expression: expression, ActionSeq: ref.Sequence, ActionPath: ref.ActionPath,
				DecodedFieldPath: leaf.DisplayPath, EncodingLayers: append([]string(nil), leaf.EncodingLayers...),
				ParamKey: leaf.Key, SampleValue: correlate.Truncate(leaf.Value, 80), Kind: kind, Column: column,
				Template: "{{data.salesforce_records." + column + "}}",
			}
			encoded, _ := json.Marshal(candidate)
			if len(out) >= maxAuraParamCandidates || len(encoded) > maxAuraParamCandidateBytes-retainedBytes {
				omitted++
				detailsFull = true
				continue
			}
			usedNames[name] = true
			retainedBytes += len(encoded)
			out = append(out, candidate)
		}
	}
	if rejected > 0 {
		warnings = append(warnings, fmt.Sprintf("Aura parameter inspection could not inspect %d request(s): malformed, ambiguous, unsupported body encoding or inspection limits. No candidates were produced from those requests.", rejected))
	}
	if uninspected > 0 {
		warnings = append(warnings, fmt.Sprintf("Aura parameter inspection omitted %d request(s) after its %d-byte aggregate work limit; coverage is incomplete.", uninspected, maxAuraParamInspectionBytes))
	}
	if omitted > 0 {
		warnings = append(warnings, fmt.Sprintf("Aura parameter review omitted %d candidate(s) after bounded detail retention; inspect those source fields manually.", omitted))
	}
	if unsafePaths > 0 {
		warnings = append(warnings, fmt.Sprintf("Aura parameter inspection excluded %d field(s) whose safe structural paths could not be published; review those source fields manually.", unsafePaths))
	}
	if unsupported > 0 {
		warnings = append(warnings, fmt.Sprintf("Aura parameter inspection left %d non-string business field(s) unchanged; structured substitution currently supports string values.", unsupported))
	}
	return out, warnings
}

func auraBusinessParamRoute(route []aurapayload.Segment) bool {
	if len(route) < 4 || route[0].Kind != aurapayload.KeySegment || route[0].Key != "actions" || route[1].Kind != aurapayload.IndexSegment || route[2].Kind != aurapayload.KeySegment || route[2].Key != "params" {
		return false
	}
	for _, segment := range route[3:] {
		if segment.Kind == aurapayload.KeySegment && credential.SensitiveName(segment.Key) {
			return false
		}
	}
	return true
}

// Keep the historical JMESPath display only when no decoding boundary exists.
// Nested JSON strings use the explicit DecodedFieldPath and EncodingLayers.
func auraParamExpression(route []aurapayload.Segment) string {
	path := ""
	for _, segment := range route {
		switch segment.Kind {
		case aurapayload.KeySegment:
			path = appendJMESPath(path, segment.Key)
		case aurapayload.IndexSegment:
			path = fmt.Sprintf("%s[%d]", path, segment.Index)
		case aurapayload.JSONStringSegment:
			return ""
		}
	}
	return path
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

func correlationSummaries(correlations []correlate.Correlation, refs map[int]*actionRef, applications map[string]correlationApplicationStatus) []CorrelationCandidate {
	out := make([]CorrelationCandidate, 0, len(correlations))
	for _, corr := range correlations {
		foundRef := refs[corr.FoundInID]
		if foundRef == nil {
			continue
		}
		usedInSeqs := make([]int, 0, len(corr.UsedInIDs))
		seenSeqs := map[int]bool{}
		for _, harID := range corr.UsedInIDs {
			usedRef := refs[harID]
			if usedRef == nil || seenSeqs[usedRef.Sequence] {
				continue
			}
			seenSeqs[usedRef.Sequence] = true
			usedInSeqs = append(usedInSeqs, usedRef.Sequence)
		}
		application := applications[corr.Name]
		reviewAction := application.reviewAction
		if !application.preApplied && reviewAction == "" {
			reviewAction = "manual_review_required"
		}
		out = append(out, CorrelationCandidate{
			Name:         corr.Name,
			Source:       corr.Source,
			Expression:   corr.Expression,
			FoundInSeq:   foundRef.Sequence,
			UsedInSeqs:   usedInSeqs,
			RuleID:       corr.RuleID,
			Confidence:   corr.Confidence,
			Reasons:      append([]string(nil), corr.Reasons...),
			PreApplied:   application.preApplied,
			ReviewAction: reviewAction,
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
	for index := range action.Extractors {
		if action.Extractors[index].Name == extractor.Name {
			// "If missing": only fill fields the existing extractor leaves unset.
			// Never overwrite an already-defined Source/Type/Expression (or the
			// other fields), so a converter-tuned extractor is not silently
			// replaced by a later same-named correlation carrying a generic
			// expression that matches a different span.
			if extractor.Convert != "" && action.Extractors[index].Convert == "" {
				action.Extractors[index].Convert = extractor.Convert
			}
			if extractor.Source != "" && action.Extractors[index].Source == "" {
				action.Extractors[index].Source = extractor.Source
			}
			if extractor.Type != "" && action.Extractors[index].Type == "" {
				action.Extractors[index].Type = extractor.Type
			}
			if extractor.Expression != "" && action.Extractors[index].Expression == "" {
				action.Extractors[index].Expression = extractor.Expression
			}
			if extractor.OnMiss != "" && action.Extractors[index].OnMiss == "" {
				action.Extractors[index].OnMiss = extractor.OnMiss
			}
			if extractor.SaveOffset != 0 && action.Extractors[index].SaveOffset == 0 {
				action.Extractors[index].SaveOffset = extractor.SaveOffset
			}
			if extractor.SaveLength != 0 && action.Extractors[index].SaveLength == 0 {
				action.Extractors[index].SaveLength = extractor.SaveLength
			}
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
