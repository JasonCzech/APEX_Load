package correlate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	maxCorrelationCandidatesPerObservation = 1024
	maxCorrelationCandidatesPerAnalysis    = 4096
	maxCorrelationObservationsPerAnalysis  = 4096
	maxCorrelationEvidenceBytes            = 16 << 20
	maxCorrelationAnalysisEvidenceBytes    = 64 << 20
	maxCorrelationStructuredFields         = 8192
	maxCorrelationJSONTokens               = 32768
	maxCorrelationJSONNodes                = 32768
	maxCorrelationJSONDepth                = 128
	maxCorrelationJSONConstructionBytes    = 16 << 20
	maxCorrelationRegexOrdinal             = protocol.MaxExtractionEntries - 1
	// Keep generated HTTP extractors inside the runtime/DSL expression ceiling.
	// This mirrors protocol/http.MaxExtractorExpressionBytes without importing
	// the HTTP package back through dsl and creating a package cycle.
	maxCorrelationExtractorExpressionBytes = 64 << 10
	// Reserve room for deterministic uniquification suffixes in the extraction
	// result key; StoreExtraction accounts for key and value together.
	maxCorrelationExtractionNameSuffixBytes = 16
)

// maxCorrelationSearchBytes is a deterministic CPU-work ceiling for cross-step
// value scanning, measured in scanned request-surface bytes (unique candidate
// values x later-observation surfaces). It is sized so realistic recordings
// (hundreds of candidates over megabytes of surfaces) never approach it;
// exhaustion truncates best-effort analysis with a warning; checked analysis
// converts that warning to ErrCorrelationWorkLimit.
// A var (not const) so tests can lower it deterministically.
var maxCorrelationSearchBytes int64 = 8 << 30

// ErrCorrelationWorkLimit identifies deterministic complexity rejection or an
// incomplete checked analysis. The explicitly best-effort
// DetectCheckedWithWarnings API reports bounded truncation as warnings instead.
var ErrCorrelationWorkLimit = errors.New("correlation analysis work limit exceeded")

type Observation struct {
	ID          int
	RequestURL  string
	RequestBody string
	// RequestBodyTransformed is true when RequestBody is the wire representation
	// of a differently encoded editable field (currently an HTTP base64 body).
	// Diagnosis may inspect it, but must not emit offsets that a client could
	// apply to the encoded DSL body.
	RequestBodyTransformed bool
	RequestHeaders         map[string]string
	// EditableRequest* are proof bits computed inside the trusted replay boundary:
	// true means the private wire field exactly matched its authored DSL surface.
	// Imported/generic observations default false and therefore remain manual-only.
	EditableRequestURL     bool
	EditableRequestBody    bool
	EditableRequestHeaders map[string]bool
	ResponseBody           string
	ResponseHeaders        map[string]string
	// ResponseStatus is the replay HTTP status (0 if unknown). It lets the
	// diagnosis flag a step the server rejected (>=400) even when no dynamic
	// value visibly rotated — e.g. a custom anti-replay token re-sent verbatim.
	ResponseStatus int
	// ResponseSuccess carries the replay engine's assertion-aware outcome. Nil
	// preserves status-only behavior for legacy/imported observations; true means
	// an expected 4xx/5xx response must not be described as a server rejection.
	ResponseSuccess *bool
}

type Correlation struct {
	Name        string
	Source      string
	Expression  string
	ExtractType string
	Convert     string
	SaveOffset  int
	SaveLength  int
	FoundInID   int
	UsedInIDs   []int
	Value       string
	SampleValue string
	// RuleID identifies which detection rule produced this correlation, so the
	// dashboard and migration review can explain *why* a value was flagged.
	RuleID string
	// Confidence is a 0..1 ranking combining the rule's base confidence with
	// runtime signals (reuse count, value entropy). Higher = more certain this
	// is a genuine dynamic value that must be correlated.
	Confidence float64
	// Reasons are human-readable factors that contributed to the confidence.
	Reasons []string
}

type Candidate struct {
	Name        string
	Value       string
	Source      string
	ExtractType string
	Expression  string
	Convert     string
	// RuleID and BaseConfidence are assigned by the rule that emitted the
	// candidate; Reason is a short description of the matched pattern.
	RuleID         string
	BaseConfidence float64
	Reason         string
}

var (
	jsonStringTokenPattern = regexp.MustCompile(`(?i)"([A-Za-z0-9_]*(?:token|access_token|id_token|refresh_token|session_id|sessionid|sid|csrf|_token|xsrf|auth|id)[A-Za-z0-9_]*)"\s*:\s*"([^"]{6,})"`)
	jsonNumberIDPattern    = regexp.MustCompile(`(?i)"([A-Za-z0-9_]*(?:id)[A-Za-z0-9_]*)"\s*:\s*(\d{4,})(?:\s*[,}\]]|\s*$)`)
	htmlInputPatterns      = []struct {
		re         *regexp.Regexp
		nameGroup  int
		valueGroup int
	}{
		{regexp.MustCompile(`(?i)<input[^>]*\bname="([A-Za-z0-9_.\-\[\]]*(?:csrf|xsrf|token|nonce|state|viewstate|authenticity)[A-Za-z0-9_.\-\[\]]*)"[^>]*\bvalue="([^"]{4,})"`), 1, 2},
		{regexp.MustCompile(`(?i)<input[^>]*\bvalue="([^"]{4,})"[^>]*\bname="([A-Za-z0-9_.\-\[\]]*(?:csrf|xsrf|token|nonce|state|viewstate|authenticity)[A-Za-z0-9_.\-\[\]]*)"`), 2, 1},
	}
	nonWordCorrelationChars = regexp.MustCompile(`[^a-z0-9]+`)
	// ASP.NET WebForms hidden state fields (__VIEWSTATE, __EVENTVALIDATION, …).
	aspNetHiddenPattern    = regexp.MustCompile(`(?i)<input[^>]*\bname="(__[A-Za-z0-9]+)"[^>]*\bvalue="([^"]*)"`)
	xmlElementTokenPattern = regexp.MustCompile(`(?is)<(?:[A-Za-z0-9_.-]+:)?([A-Za-z0-9_.-]*(?:token|session|csrf|xsrf|nonce|state|messageid|relatesto|correlationid|correlation|requestid|id)[A-Za-z0-9_.-]*)\b[^>]*>([^<]{6,})</(?:[A-Za-z0-9_.-]+:)?([A-Za-z0-9_.-]+)>`)
	xmlAttrTokenPattern    = regexp.MustCompile(`(?is)\b(?:[A-Za-z0-9_.-]+:)?([A-Za-z0-9_.-]*(?:token|session|csrf|xsrf|nonce|state|messageid|relatesto|correlationid|correlation|requestid|id)[A-Za-z0-9_.-]*)\s*=\s*"([^"]{6,})"`)
	auraTokenPattern       = regexp.MustCompile(`(?i)(?:^|[&{,"\s])aura\.token["']?\s*[:=]\s*["']?([^"&',}\s]{6,})`)
	auraFWUIDPattern       = regexp.MustCompile(`(?i)"fwuid"\s*:\s*"([^"]{6,})"`)
	// auraAppPattern only matches values shaped like a Salesforce Aura app
	// descriptor — a markup:// reference or a "namespace:name" descriptor — so a
	// generic "app":"SomeName" field in unrelated JSON (analytics, feature flags)
	// is not mis-detected as an Aura correlation. See TestAuraAppPatternRequiresDescriptorShape.
	auraAppPattern    = regexp.MustCompile(`(?i)"app"\s*:\s*"(markup://[^"]+|[A-Za-z][\w$.-]*:[A-Za-z][\w$./-]*)"`)
	auraLoadedPattern = regexp.MustCompile(`(?i)"loaded"\s*:\s*(\{[^}]{6,}\})`)
	// Keep the JSON and form spellings separate so every replay regex has the
	// selected value in capture group 1. The HTTP regex extractor always returns
	// group 1; an alternation with one capture per branch would return an empty
	// value for the second branch.
	auraPageURIJSONPattern = regexp.MustCompile(`(?i)"pageURI"\s*:\s*"([^"]{4,})"`)
	auraPageURIFormPattern = regexp.MustCompile(`(?i)aura\.pageURI\s*=\s*([^&\s]{4,})`)
	auraPageScopeIDPattern = regexp.MustCompile(`(?i)"pageScopeId"\s*:\s*"([^"]{6,})"`)
	// The field name is deliberately non-capturing: generated regex extractors
	// must return the ViewState value as capture group 1.
	visualforcePattern    = regexp.MustCompile(`(?i)<input[^>]*\bname="(?:(?:com\.salesforce\.visualforce\.)?ViewState(?:MAC|Version|CSRF)?)"[^>]*\bvalue="([^"]{4,})"`)
	confirmationPattern   = regexp.MustCompile(`(?i)(?:^|[&{,"\s])_CONFIRMATIONTOKEN["']?\s*[:=]\s*["']?([^"&',}\s]{6,})`)
	frontdoorSIDPattern   = regexp.MustCompile(`(?i)(?:^|[?&{,"\s])sid(?:_Client(?:_id)?)?["']?\s*[:=]\s*["']?([A-Za-z0-9!._-]{8,})`)
	salesforceCSHCPattern = regexp.MustCompile(`(?i)(?:^|[?&{,"\s])cshc["']?\s*[:=]\s*["']?([^"&',}\s]{6,})`)
	// salesforceNextRecordsURLPattern matches the SOQL REST Query API pagination
	// cursor: the response carries {"done":false,"nextRecordsUrl":"/services/data/
	// vXX/query/01g...-2000",...} and the client must GET that relative URL to page.
	// It is extracted via JSONPath and reused verbatim as the next request's target.
	salesforceNextRecordsURLPattern = regexp.MustCompile(`(?i)"nextRecordsUrl"\s*:\s*"([^"]+)"`)
	// salesforceBulkContentURLPattern matches the Bulk API 2.0 ingest job-create
	// response field {"id":"750...","contentUrl":"services/data/vXX/jobs/ingest/
	// 750.../batches",...}. contentUrl is the relative path the client PUTs the CSV
	// batch to, and it must be reused as the upload request target. It is keyed
	// narrowly on the exact field name so generic url fields are not over-matched.
	salesforceBulkContentURLPattern = regexp.MustCompile(`(?i)"contentUrl"\s*:\s*"([^"]+)"`)
)

func Detect(observations []Observation) []Correlation {
	correlations, _, _ := DetectCheckedWithWarnings(observations)
	return correlations
}

// DetectChecked is the fail-closed form of correlation detection. Any
// truncation means the returned prefix is not a complete analysis, so checked
// callers receive ErrCorrelationWorkLimit and no correlations. Callers that
// intentionally support degraded, best-effort results must use
// DetectCheckedWithWarnings and surface its warnings.
func DetectChecked(observations []Observation) ([]Correlation, error) {
	correlations, warnings, err := DetectCheckedWithWarnings(observations)
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrCorrelationWorkLimit, strings.Join(warnings, "; "))
	}
	return correlations, nil
}

// DetectCheckedWithWarnings is the explicitly best-effort form of detection:
// per-response candidate truncation and analysis-budget exhaustion preserve
// the completed prefix and record degradation warnings.
func DetectCheckedWithWarnings(observations []Observation) ([]Correlation, []string, error) {
	if err := validateCorrelationObservations(observations); err != nil {
		return nil, nil, err
	}
	checkedValues := map[string]bool{}
	searches := make([]observationSearch, len(observations))
	searchSizes := make([]int64, len(observations))
	for index, observation := range observations {
		searches[index] = newObservationSearch(observation)
		searchSizes[index] = searches[index].searchBytes()
	}
	var warnings []string
	correlations := make([]Correlation, 0)
	totalCandidates := 0
	var searchBytes int64
	truncated := false
scan:
	for index, observation := range observations {
		if observation.ResponseBody == "" && len(observation.ResponseHeaders) == 0 {
			continue
		}
		candidates, candidatesTruncated, err := candidatesCheckedWithTruncation(observation.ResponseBody, observation.ResponseHeaders)
		if err != nil {
			return nil, nil, err
		}
		if candidatesTruncated {
			warnings = append(warnings, fmt.Sprintf(
				"correlation candidate discovery for observation %d was incomplete after retaining %d candidate(s) (per-observation cap %d); later values in that response were not analyzed",
				observation.ID, len(candidates), maxCorrelationCandidatesPerObservation,
			))
		}
		totalCandidates += len(candidates)
		if totalCandidates > maxCorrelationCandidatesPerAnalysis {
			truncated = true
			break
		}
		for _, candidate := range candidates {
			if candidate.Value == "" || checkedValues[candidate.Value] {
				continue
			}
			checkedValues[candidate.Value] = true
			usedIn := make([]int, 0)
			for later := index + 1; later < len(observations); later++ {
				cost := searchSizes[later]
				if cost > maxCorrelationSearchBytes-searchBytes {
					truncated = true
					break scan
				}
				searchBytes += cost
				if searches[later].Contains(candidate.Value) {
					usedIn = append(usedIn, observations[later].ID)
				}
			}
			if len(usedIn) == 0 {
				continue
			}
			confidence, reasons := scoreConfidence(candidate, len(usedIn))
			correlations = append(correlations, Correlation{
				Name:        candidate.Name,
				Source:      candidate.Source,
				Expression:  candidate.Expression,
				ExtractType: candidate.ExtractType,
				Convert:     candidate.Convert,
				FoundInID:   observation.ID,
				UsedInIDs:   usedIn,
				Value:       candidate.Value,
				SampleValue: Truncate(candidate.Value, 80),
				RuleID:      candidate.RuleID,
				Confidence:  confidence,
				Reasons:     reasons,
			})
		}
	}
	if truncated {
		warnings = append(warnings, "correlation analysis exceeded its deterministic work budget; remaining candidates and observations were skipped")
	}
	// Rank by confidence (desc) so the most certain correlations surface first,
	// falling back to source order for stable, deterministic output.
	sort.SliceStable(correlations, func(i, j int) bool {
		if correlations[i].Confidence != correlations[j].Confidence {
			return correlations[i].Confidence > correlations[j].Confidence
		}
		if correlations[i].FoundInID == correlations[j].FoundInID {
			return correlations[i].Name < correlations[j].Name
		}
		return correlations[i].FoundInID < correlations[j].FoundInID
	})
	uniquifyCorrelationNames(correlations)
	return correlations, warnings, nil
}

type observationSearch struct {
	rawSurfaces     []string
	decodedSurfaces []string
	formValues      []string
}

func newObservationSearch(observation Observation) observationSearch {
	search := observationSearch{
		rawSurfaces: make([]string, 0, 2+len(observation.RequestHeaders)),
	}
	search.addSurface(observation.RequestURL)
	search.addSurface(observation.RequestBody)
	for _, headerValue := range observation.RequestHeaders {
		search.addSurface(headerValue)
	}
	search.addFormValues(observation.RequestBody)
	return search
}

func (search *observationSearch) addSurface(raw string) {
	if raw == "" {
		return
	}
	search.rawSurfaces = append(search.rawSurfaces, raw)
	if decoded := urlDecoded(raw); decoded != raw {
		search.decodedSurfaces = append(search.decodedSurfaces, decoded)
	}
}

func (search *observationSearch) addFormValues(body string) {
	if formParseTooWide(body) {
		// The raw surface is still searched; only the parsed form projection is
		// skipped for surfaces too wide to materialize.
		return
	}
	values, err := url.ParseQuery(body)
	if err != nil || len(values) == 0 {
		return
	}
	for key, vals := range values {
		if key != "" {
			search.formValues = append(search.formValues, key)
		}
		for _, val := range vals {
			if val != "" {
				search.formValues = append(search.formValues, val)
			}
		}
	}
}

func (search observationSearch) Contains(value string) bool {
	if value == "" {
		return false
	}
	for _, surface := range search.rawSurfaces {
		if strings.Contains(surface, value) {
			return true
		}
	}
	for _, surface := range search.decodedSurfaces {
		if strings.Contains(surface, value) {
			return true
		}
	}
	for _, formValue := range search.formValues {
		if strings.Contains(formValue, value) {
			return true
		}
	}
	return false
}

func (search observationSearch) searchBytes() int64 {
	var total int64
	for _, surface := range search.rawSurfaces {
		total += int64(len(surface))
	}
	for _, surface := range search.decodedSurfaces {
		total += int64(len(surface))
	}
	for _, surface := range search.formValues {
		total += int64(len(surface))
	}
	return total
}

func correlationObservationBytes(observation Observation) int64 {
	total := int64(len(observation.RequestURL) + len(observation.RequestBody) + len(observation.ResponseBody))
	for key, value := range observation.RequestHeaders {
		total += int64(len(key) + len(value))
	}
	for key, value := range observation.ResponseHeaders {
		total += int64(len(key) + len(value))
	}
	return total
}

// formParseTooWide bounds url.ParseQuery materialization: parsing splits on
// every '&', so a surface with an enormous separator count (ordinary HTML can
// legitimately carry thousands of '&' bytes that are never form-parsed) is
// simply not form-parsed. Raw substring search still covers such surfaces, so
// this is a per-surface skip, never an analysis failure.
func formParseTooWide(body string) bool {
	return strings.Count(body, "&") >= maxCorrelationStructuredFields
}

// headerValueSplitTooWide bounds the strings.Split materialization of a
// newline-joined repeated header value the same way.
func headerValueSplitTooWide(value string) bool {
	return strings.Count(value, "\n") >= maxCorrelationStructuredFields
}

func validateCorrelationObservations(observations []Observation) error {
	if len(observations) > maxCorrelationObservationsPerAnalysis {
		return fmt.Errorf("%w: observation count exceeds %d", ErrCorrelationWorkLimit, maxCorrelationObservationsPerAnalysis)
	}
	var evidenceBytes int64
	seenIDs := make(map[int]struct{}, len(observations))
	for _, observation := range observations {
		if _, duplicate := seenIDs[observation.ID]; duplicate {
			return fmt.Errorf("%w: duplicate observation id %d", ErrCorrelationWorkLimit, observation.ID)
		}
		seenIDs[observation.ID] = struct{}{}
		observationBytes := correlationObservationBytes(observation)
		if observationBytes > maxCorrelationEvidenceBytes {
			return fmt.Errorf("%w: observation evidence exceeds %d bytes", ErrCorrelationWorkLimit, maxCorrelationEvidenceBytes)
		}
		if observationBytes > maxCorrelationAnalysisEvidenceBytes-evidenceBytes {
			return fmt.Errorf("%w: aggregate evidence exceeds %d bytes", ErrCorrelationWorkLimit, maxCorrelationAnalysisEvidenceBytes)
		}
		evidenceBytes += observationBytes
	}
	return nil
}

func uniquifyCorrelationNames(correlations []Correlation) {
	used := map[string]struct{}{}
	nextSuffix := map[string]int{}
	for i := range correlations {
		base := strings.TrimSpace(correlations[i].Name)
		if base == "" {
			base = "corr_value"
		}
		name := base
		for {
			if _, exists := used[name]; !exists {
				break
			}
			nextSuffix[base]++
			if nextSuffix[base] < 2 {
				nextSuffix[base] = 2
			}
			name = fmt.Sprintf("%s_%d", base, nextSuffix[base])
		}
		if nextSuffix[base] == 0 {
			nextSuffix[base] = 1
		}
		correlations[i].Name = name
		used[name] = struct{}{}
	}
}

func Candidates(responseBody string, responseHeaders map[string]string) []Candidate {
	candidates, _ := CandidatesChecked(responseBody, responseHeaders)
	return candidates
}

func CandidatesChecked(responseBody string, responseHeaders map[string]string) ([]Candidate, error) {
	candidates, _, err := candidatesCheckedWithTruncation(responseBody, responseHeaders)
	return candidates, err
}

// candidatesCheckedWithTruncation retains the best-effort public behavior while
// carrying an explicit completeness bit for fail-closed detection, diff, and
// repair callers. Candidate length alone cannot prove truncation: a response
// may contain exactly the configured cap and no omitted value.
func candidatesCheckedWithTruncation(responseBody string, responseHeaders map[string]string) ([]Candidate, bool, error) {
	responseBytes := int64(len(responseBody))
	for key, value := range responseHeaders {
		responseBytes += int64(len(key) + len(value))
	}
	if responseBytes > maxCorrelationEvidenceBytes {
		return nil, false, fmt.Errorf("%w: response evidence exceeds %d bytes", ErrCorrelationWorkLimit, maxCorrelationEvidenceBytes)
	}
	candidates := make([]Candidate, 0, maxCorrelationCandidatesPerObservation+1)
	truncated := false
	// A response that yields more candidates than the per-observation cap keeps
	// the earliest ones (families run in fixed priority order) and drops the
	// rest, instead of failing the whole analysis. Internally retain one sentinel
	// beyond the cap so exactly-at-cap results are not mislabeled incomplete.
	appendFamily := func(family []Candidate, familyTruncated bool) bool {
		truncated = truncated || familyTruncated
		remaining := maxCorrelationCandidatesPerObservation + 1 - len(candidates)
		if remaining <= 0 {
			if len(family) > 0 {
				truncated = true
			}
			return true
		}
		if len(family) > remaining {
			family = family[:remaining]
			truncated = true
		}
		candidates = append(candidates, family...)
		return len(candidates) > maxCorrelationCandidatesPerObservation
	}
	finish := func() ([]Candidate, bool, error) {
		if len(candidates) > maxCorrelationCandidatesPerObservation {
			candidates = candidates[:maxCorrelationCandidatesPerObservation]
			truncated = true
		}
		return candidates, truncated, nil
	}
	if appendFamily(salesforceCandidates(responseBody, responseHeaders), false) {
		return finish()
	}
	if appendFamily(formCandidates(responseBody), false) {
		return finish()
	}
	aspNetFamily, aspNetTruncated := aspNetHiddenCandidatesWithTruncation(responseBody)
	if appendFamily(aspNetFamily, aspNetTruncated) {
		return finish()
	}
	if appendFamily(htmlInputCandidates(responseBody), false) {
		return finish()
	}
	jsonFamily, jsonTruncated, err := jsonCandidatesCheckedWithTruncation(responseBody)
	if err != nil {
		return nil, false, err
	}
	if appendFamily(jsonFamily, jsonTruncated) {
		return finish()
	}
	xmlFamily, xmlTruncated := xmlCandidatesWithTruncation(responseBody)
	if appendFamily(xmlFamily, xmlTruncated) {
		return finish()
	}
	headerTruncated := headerCandidateDiscoveryTruncated(responseHeaders)
	appendFamily(headerCandidates(responseHeaders), headerTruncated)
	return finish()
}

func headerCanProduceCandidates(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "set-cookie" ||
		lower == "location" ||
		isJMSHeaderName(lower) ||
		isWebSocketHeaderName(lower) ||
		dynamicHeaderName(lower)
}

func headerCandidateDiscoveryTruncated(headers map[string]string) bool {
	for key, value := range headers {
		if headerValueSplitTooWide(value) {
			if headerCanProduceCandidates(key) {
				return true
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "location":
			for _, line := range strings.Split(value, "\n") {
				parsed, err := url.Parse(strings.TrimSpace(line))
				// Each '&' in RawQuery can materialize another url.Values entry.
				if err == nil && formParseTooWide(parsed.RawQuery) {
					return true
				}
			}
		}
	}
	return false
}

func appendCandidatesBounded(dst []Candidate, src ...Candidate) []Candidate {
	for _, candidate := range src {
		if !candidateFitsRuntimeLimits(candidate) {
			continue
		}
		if len(dst) >= maxCorrelationCandidatesPerObservation+1 {
			break
		}
		dst = append(dst, candidate)
	}
	return dst
}

func candidateFitsRuntimeLimits(candidate Candidate) bool {
	if len(candidate.Expression) > maxCorrelationExtractorExpressionBytes {
		return false
	}
	return CorrelationExtractionValueFits(candidate.Name, candidate.Value)
}

// CorrelationExtractionEntryBytes returns the exact result-map bytes consumed
// by one generated extraction entry when both its name and expected value fit
// the protocol's per-entry limits.
func CorrelationExtractionEntryBytes(name, value string) (int, bool) {
	if len(name) == 0 || len(name) > protocol.MaxExtractionKeyBytes ||
		len(value) > protocol.MaxExtractionValueBytes {
		return 0, false
	}
	entryBytes := len(name) + len(value)
	return entryBytes, entryBytes <= protocol.MaxExtractionBytes
}

// CorrelationExtractionValueFits reports whether a generated variable name and
// value can coexist in the protocol extraction store, including room for the
// deterministic suffixes added when candidate names collide.
func CorrelationExtractionValueFits(name, value string) bool {
	entryBytes, ok := CorrelationExtractionEntryBytes(name, value)
	if !ok || len(name)+maxCorrelationExtractionNameSuffixBytes > protocol.MaxExtractionKeyBytes {
		return false
	}
	return entryBytes <= protocol.MaxExtractionBytes-maxCorrelationExtractionNameSuffixBytes
}

func remainingCandidateMatchLimit(candidates []Candidate) int {
	remaining := maxCorrelationCandidatesPerObservation + 1 - len(candidates)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func ObservationContains(observation Observation, value string) bool {
	return newObservationSearch(observation).Contains(value)
}

func BuildVarMap(correlations []Correlation) map[string]string {
	m := make(map[string]string, len(correlations))
	for _, corr := range correlations {
		value := corr.Value
		if value == "" {
			value = corr.SampleValue
		}
		m[value] = "{{var." + corr.Name + "}}"
	}
	return m
}

func ApplyVars(value string, vars map[string]string) string {
	replacements := make(map[string]string, len(vars))
	for raw, replacement := range vars {
		if raw != "" {
			replacements[raw] = replacement
		}
	}
	return applyLiteralReplacements(value, replacements)
}

func ApplyVarsWithEncodedValues(value string, vars map[string]string, encodedValues map[string]bool) string {
	replacements := make(map[string]string, len(vars)*2)
	for raw, replacement := range vars {
		if raw != "" {
			replacements[raw] = replacement
		}
	}
	// Exact raw-value mappings win if an encoded spelling of one value happens
	// to equal another correlation's literal value.
	rawValues := make([]string, 0, len(vars))
	for raw := range vars {
		if raw != "" {
			rawValues = append(rawValues, raw)
		}
	}
	sort.Strings(rawValues)
	for _, raw := range rawValues {
		if !encodedValues[raw] {
			continue
		}
		replacement := vars[raw]
		encoded := QueryEncodedFormOf(raw)
		if encoded != "" {
			if _, exists := replacements[encoded]; !exists {
				replacements[encoded] = replacement
			}
		}
	}
	return applyLiteralReplacements(value, replacements)
}

// applyLiteralReplacements performs one non-recursive replacement pass. A
// replacement may itself contain another raw correlation value (variable names
// such as corr_access_token make that quite plausible); strings.NewReplacer
// never scans inserted output, so generated {{var.*}} templates remain intact.
func applyLiteralReplacements(value string, replacements map[string]string) string {
	if value == "" || len(replacements) == 0 {
		return value
	}
	replacer := newLiteralReplacer(replacements)
	if replacer == nil {
		return value
	}
	return replacer.Replace(value)
}

// ApplyVarsWithOutputLimit performs the same deterministic, non-recursive
// replacement as ApplyVars while retaining at most maxOutputBytes. The full
// input is still scanned so every retained byte has passed through the
// replacer; discarded suffix bytes can never reappear in a caller's output.
func ApplyVarsWithOutputLimit(value string, vars map[string]string, maxOutputBytes int) (string, bool) {
	if maxOutputBytes < 0 {
		maxOutputBytes = 0
	}
	replacements := make(map[string]string, len(vars))
	for raw, replacement := range vars {
		if raw != "" {
			replacements[raw] = replacement
		}
	}
	replacer := newLiteralReplacer(replacements)
	if replacer == nil {
		if len(value) <= maxOutputBytes {
			return value, false
		}
		return value[:maxOutputBytes], true
	}
	writer := boundedReplacementWriter{limit: maxOutputBytes}
	_, _ = replacer.WriteString(&writer, value)
	return writer.builder.String(), writer.truncated
}

func newLiteralReplacer(replacements map[string]string) *strings.Replacer {
	values := make([]string, 0, len(replacements))
	for raw := range replacements {
		if raw != "" {
			values = append(values, raw)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	if len(values) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(values)*2)
	for _, raw := range values {
		pairs = append(pairs, raw, replacements[raw])
	}
	return strings.NewReplacer(pairs...)
}

type boundedReplacementWriter struct {
	builder   strings.Builder
	limit     int
	truncated bool
}

func (writer *boundedReplacementWriter) Write(value []byte) (int, error) {
	originalBytes := len(value)
	writer.writeString(string(value))
	return originalBytes, nil
}

func (writer *boundedReplacementWriter) WriteString(value string) (int, error) {
	originalBytes := len(value)
	writer.writeString(value)
	return originalBytes, nil
}

func (writer *boundedReplacementWriter) writeString(value string) {
	remaining := writer.limit - writer.builder.Len()
	if remaining <= 0 {
		if value != "" {
			writer.truncated = true
		}
		return
	}
	if len(value) > remaining {
		writer.builder.WriteString(value[:remaining])
		writer.truncated = true
		return
	}
	writer.builder.WriteString(value)
}

func EncodedFormsOf(value string) []string {
	forms := make([]string, 0, 4)
	for _, encoded := range []string{
		url.QueryEscape(value),
		url.PathEscape(value),
		strings.ReplaceAll(url.QueryEscape(value), "+", "%20"),
		strings.ReplaceAll(url.QueryEscape(value), "%2B", "+"),
	} {
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

// QueryEncodedFormOf returns the one encoded spelling the DSL's url_encode
// conversion can reproduce exactly. EncodedFormsOf is intentionally broader
// for discovery (it also recognizes path escaping and common form variants),
// but those spellings are not interchangeable when a script is rewritten.
func QueryEncodedFormOf(value string) string {
	encoded := url.QueryEscape(value)
	if encoded == "" || encoded == value {
		return ""
	}
	return encoded
}

func DetectEncodedValueUsage(value string, surfaces []string) bool {
	encodedForms := EncodedFormsOf(value)
	if len(encodedForms) == 0 {
		return false
	}
	for _, surface := range surfaces {
		for _, encoded := range encodedForms {
			if strings.Contains(surface, encoded) {
				return true
			}
		}
	}
	return false
}

// EncodedReuseExtractorConvert returns the extractor conversion needed for a
// variable that will replace a URL/form-encoded request literal. Correlation
// values are compared in their decoded form: an unconverted source therefore
// needs url_encode, while a url_decode source already captures encoded text and
// must drop that conversion to avoid double-encoding. Other conversion chains
// cannot be represented by the DSL's single Convert field.
func EncodedReuseExtractorConvert(sourceConvert string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(sourceConvert)) {
	case "":
		return "url_encode", true
	case "url_decode":
		return "", true
	case "url_encode":
		return "url_encode", true
	default:
		return "", false
	}
}

func ApplyVarsToHeadersWithEncodedValues(headers map[string]string, vars map[string]string, encodedValues map[string]bool) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	for key, value := range headers {
		headers[key] = ApplyVarsWithEncodedValues(value, vars, encodedValues)
	}
	return headers
}

func ApplyVarsToHeaders(headers map[string]string, vars map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	for key, value := range headers {
		headers[key] = ApplyVars(value, vars)
	}
	return headers
}

func Slug(value string) string {
	slug := nonWordCorrelationChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		return "value"
	}
	return slug
}

func Truncate(value string, maxLen int) string {
	if value == "" {
		return value
	}
	if maxLen <= 0 {
		return "..."
	}
	truncated := boundedtext.TruncateUTF8(value, maxLen)
	if truncated == value {
		return value
	}
	return truncated + "..."
}

func salesforceCandidates(responseBody string, responseHeaders map[string]string) []Candidate {
	candidates := make([]Candidate, 0)
	if !looksLikeFormEncodedBody(responseBody) {
		candidates = appendCandidatesBounded(candidates, salesforceRegexCandidates(responseBody)...)
		if len(candidates) > maxCorrelationCandidatesPerObservation {
			return candidates
		}
	}
	keys := make([]string, 0, len(responseHeaders))
	for key := range responseHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seenHeaderRegex := make(map[string]bool)
	for _, key := range keys {
		if strings.EqualFold(strings.TrimSpace(key), "Set-Cookie") {
			// Set-Cookie has its own grammar-aware candidate path below. Broad
			// Salesforce patterns (especially sid=) otherwise win family
			// priority and replace the precise leading-cookie extractor with an
			// unanchored regex that can capture an attribute or another header.
			continue
		}
		value := responseHeaders[key]
		header := key + ": " + value
		for _, candidate := range salesforceRegexCandidatesForSource(header, "header") {
			identity := candidate.ExtractType + "\x00" + candidate.Expression
			if seenHeaderRegex[identity] {
				// The HTTP header regex runs over one globally flattened surface.
				// A later map key with the same expression would still extract the
				// first key's value, so omit the non-replayable duplicate.
				continue
			}
			seenHeaderRegex[identity] = true
			candidates = appendCandidatesBounded(candidates, candidate)
		}
		if len(candidates) > maxCorrelationCandidatesPerObservation {
			return candidates
		}
	}
	return candidates
}

func looksLikeFormEncodedBody(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || !strings.Contains(trimmed, "=") || formParseTooWide(trimmed) {
		return false
	}
	// url.ParseQuery is far too permissive — it accepts almost any '%'-free text,
	// so an ordinary JSON/XML body containing '=' (e.g. base64 padding like
	// "token":"dGVzdA==") parses as a single key/value pair and would wrongly
	// suppress Salesforce body scanning. Exclude obviously structured bodies, and
	// require the parsed keys to look like real form field names.
	switch trimmed[0] {
	case '{', '[', '<':
		return false
	}
	values, err := url.ParseQuery(trimmed)
	if err != nil || len(values) == 0 {
		return false
	}
	for key := range values {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, `{}[]"':<>`) {
			return false
		}
	}
	return true
}

func salesforceRegexCandidates(body string) []Candidate {
	return salesforceRegexCandidatesForSource(body, "body")
}

func salesforceRegexCandidatesForSource(body string, source string) []Candidate {
	candidates := make([]Candidate, 0)
	addMatches := func(pattern *regexp.Regexp, name, ruleID, reason string, confidence float64) {
		limit := remainingCandidateMatchLimit(candidates)
		if limit == 0 {
			return
		}
		for matchIndex, match := range pattern.FindAllStringSubmatch(body, limit) {
			value := firstCapturedValue(match)
			if value == "" {
				continue
			}
			extractType, expression, replayable := regexExtractorForMatch(source, pattern.String(), matchIndex+1)
			if !replayable {
				continue
			}
			candidates = appendCandidatesBounded(candidates, Candidate{
				Name:           "corr_" + Slug(name),
				Value:          value,
				Source:         source,
				ExtractType:    extractType,
				Expression:     expression,
				RuleID:         ruleID,
				BaseConfidence: confidence,
				Reason:         reason,
			})
		}
	}
	addMatches(auraTokenPattern, "salesforce_aura_token", RuleSalesforceAuraToken, "Salesforce aura.token value", 0.95)
	addMatches(auraFWUIDPattern, "salesforce_aura_fwuid", RuleSalesforceAuraContextFWUID, "Salesforce Aura context fwuid", 0.94)
	addMatches(auraAppPattern, "salesforce_aura_app", RuleSalesforceAuraAppDescriptor, "Salesforce Aura app descriptor", 0.9)
	addMatches(auraLoadedPattern, "salesforce_aura_loaded", RuleSalesforceAuraLoaded, "Salesforce Aura loaded map", 0.88)
	addMatches(auraPageURIJSONPattern, "salesforce_aura_page_uri", RuleSalesforceAuraPageURI, "Salesforce Aura page URI", 0.86)
	addMatches(auraPageURIFormPattern, "salesforce_aura_page_uri", RuleSalesforceAuraPageURI, "Salesforce Aura page URI", 0.86)
	addMatches(auraPageScopeIDPattern, "salesforce_page_scope_id", RuleSalesforcePageScopeID, "Salesforce pageScopeId value", 0.85)
	addMatches(visualforcePattern, "salesforce_viewstate", RuleSalesforceViewState, "Salesforce Visualforce ViewState field", 0.94)
	addMatches(confirmationPattern, "salesforce_confirmation_token", RuleSalesforceConfirmationToken, "Salesforce confirmation token", 0.93)
	addMatches(frontdoorSIDPattern, "salesforce_frontdoor_sid", RuleSalesforceFrontdoorSID, "Salesforce frontdoor sid/session value", 0.85)
	addMatches(salesforceCSHCPattern, "salesforce_cshc", RuleSalesforceCSHC, "Salesforce cshc login/session handoff value", 0.92)
	// addJSONPath emits a clean JSONPath extractor (not a brittle regex one) for
	// values that live at a well-known top-level JSON field, so the generated
	// extractor reads $.<field> at replay rather than re-running the match regex.
	addJSONPath := func(pattern *regexp.Regexp, name, ruleID, expression, reason string, confidence float64) {
		limit := remainingCandidateMatchLimit(candidates)
		if limit == 0 {
			return
		}
		matches := pattern.FindAllStringSubmatch(body, limit)
		for matchIndex, match := range matches {
			value := firstCapturedValue(match)
			if value == "" {
				continue
			}
			extractType := "regex"
			extractorExpression := pattern.String()
			jsonKey := strings.TrimPrefix(expression, "$.")
			if source == "body" && len(matches) == 1 {
				// A JSONPath is faithful only when the matched spelling is the exact
				// top-level key and JSON decoding yields the candidate value. The
				// discovery regex is intentionally case-insensitive and sees raw escape
				// bytes, while JSONPath is case-sensitive and returns decoded strings.
				if decoded, ok := exactTopLevelJSONString(body, jsonKey); ok {
					value = decoded
					extractType = "jsonpath"
					extractorExpression = expression
				}
			}
			if extractType != "jsonpath" {
				// Duplicate JSON keys cannot be represented faithfully by JSONPath,
				// and header extraction supports regex rather than JSONPath. Malformed,
				// nested, case-variant, or escape-sensitive fields likewise retain the
				// byte-exact regex contract.
				var replayable bool
				extractType, extractorExpression, replayable = regexExtractorForMatch(source, pattern.String(), matchIndex+1)
				if !replayable {
					continue
				}
			}
			candidates = appendCandidatesBounded(candidates, Candidate{
				Name:           "corr_" + Slug(name),
				Value:          value,
				Source:         source,
				ExtractType:    extractType,
				Expression:     extractorExpression,
				RuleID:         ruleID,
				BaseConfidence: confidence,
				Reason:         reason,
			})
		}
	}
	addJSONPath(salesforceNextRecordsURLPattern, "salesforce_soql_next_records_url", RuleSalesforceNextRecordsURL, "$.nextRecordsUrl", "Salesforce SOQL pagination nextRecordsUrl cursor", 0.9)
	addJSONPath(salesforceBulkContentURLPattern, "salesforce_bulk_content_url", RuleSalesforceBulkContentURL, "$.contentUrl", "Salesforce Bulk API ingest contentUrl upload target", 0.9)
	return candidates
}

func exactTopLevelJSONString(body, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	valid, err := validateCorrelationJSONShape(body)
	if err != nil || !valid {
		return "", false
	}
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(body))
	if err := decoder.Decode(&decoded); err != nil {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}
	value, ok := decoded[key].(string)
	return value, ok
}

func formFieldRegex(fieldName string) string {
	return fmt.Sprintf(`(?i)(?:^|&)%s=([^&]*)`, regexp.QuoteMeta(fieldName))
}

func formEncodedJSONStringFieldRegex(formField, jsonField string) string {
	return fmt.Sprintf(`(?i)(?:^|&)%s=[^&]*(?:%%22|")%s(?:%%22|")\s*(?:%%3A|:)\s*(?:%%22|")((?:%%[0-1][0-9A-Fa-f]|%%2[013-9A-Fa-f]|%%[3-9A-Fa-f][0-9A-Fa-f]|[^&%%"'])*)(?:%%22|")`,
		regexp.QuoteMeta(formField), regexp.QuoteMeta(jsonField))
}

func salesforceFormContextCandidates(fieldName, decodedValue string) []Candidate {
	candidates := make([]Candidate, 0)
	addStringField := func(pattern *regexp.Regexp, jsonField, name, ruleID, reason string, confidence float64) {
		limit := remainingCandidateMatchLimit(candidates)
		if limit == 0 {
			return
		}
		matches := pattern.FindAllStringSubmatch(decodedValue, limit+1)
		// The raw form extractor deliberately selects one JSON field per
		// aura.context occurrence. Duplicate keys inside a single context cannot
		// be assigned a faithful nested ordinal, so omit them rather than emit two
		// candidates whose extractor would return the same (usually last) value.
		if len(matches) != 1 {
			return
		}
		value := firstCapturedValue(matches[0])
		if value == "" {
			return
		}
		candidates = appendCandidatesBounded(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    "regex",
			Expression:     formEncodedJSONStringFieldRegex(fieldName, jsonField),
			Convert:        "url_decode",
			RuleID:         ruleID,
			BaseConfidence: confidence,
			Reason:         reason,
		})
	}
	addStringField(auraFWUIDPattern, "fwuid", "salesforce_aura_fwuid", RuleSalesforceAuraContextFWUID, "Salesforce Aura context fwuid", 0.94)
	addStringField(auraAppPattern, "app", "salesforce_aura_app", RuleSalesforceAuraAppDescriptor, "Salesforce Aura app descriptor", 0.9)
	addStringField(auraPageScopeIDPattern, "pageScopeId", "salesforce_page_scope_id", RuleSalesforcePageScopeID, "Salesforce pageScopeId value", 0.85)
	addStringField(auraPageURIJSONPattern, "pageURI", "salesforce_aura_page_uri", RuleSalesforceAuraPageURI, "Salesforce Aura page URI", 0.86)
	return candidates
}

func formCandidates(body string) []Candidate {
	if formParseTooWide(body) {
		return nil
	}
	values, err := url.ParseQuery(body)
	if err != nil || len(values) == 0 {
		return nil
	}
	candidates := make([]Candidate, 0)
	contextRegexMatches := make(map[string][][]int)
	add := func(fieldName, name, value, ruleID string, confidence float64, reason string, ordinal, total int) {
		if strings.TrimSpace(value) == "" || remainingCandidateMatchLimit(candidates) == 0 {
			return
		}
		extractType := "regex"
		expression := formFieldRegex(fieldName)
		if total > 1 {
			if ordinal > maxCorrelationRegexOrdinal {
				// The replay runtime cannot address this occurrence; omit just
				// this candidate rather than aborting the analysis.
				return
			}
			extractType = "regex_ordinal"
			expression = encodeRegexOrdinalExpression(expression, ordinal)
		}
		candidates = appendCandidatesBounded(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    extractType,
			Expression:     expression,
			Convert:        "url_decode",
			RuleID:         ruleID,
			BaseConfidence: confidence,
			Reason:         reason,
		})
	}
	occurrencesByKey := exactRawFormOccurrenceIndex(body)
	keys := make([]string, 0, len(occurrencesByKey))
	for key := range occurrencesByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fieldOccurrences := occurrencesByKey[key]
		if len(fieldOccurrences) == 0 {
			continue
		}
		for valueIndex, occurrence := range fieldOccurrences {
			value := occurrence.value
			if len(candidates) > maxCorrelationCandidatesPerObservation {
				return candidates
			}
			switch key {
			case "aura.token":
				add(key, "salesforce_aura_token", value, RuleSalesforceAuraToken, 0.95, "Salesforce aura.token form field", valueIndex+1, len(fieldOccurrences))
			case "aura.context":
				contextCandidates := salesforceFormContextCandidates(key, value)
				replayable := contextCandidates[:0]
				for _, candidate := range contextCandidates {
					ordinal, exact, overflow := formContextRegexOrdinal(body, occurrence, candidate, contextRegexMatches)
					if overflow || !exact {
						// Beyond the replayable ordinal range: omit just this
						// candidate.
						continue
					}
					if ordinal > 1 {
						candidate.ExtractType = "regex_ordinal"
						candidate.Expression = encodeRegexOrdinalExpression(candidate.Expression, ordinal)
					}
					replayable = append(replayable, candidate)
				}
				candidates = appendCandidatesBounded(candidates, replayable...)
			case "aura.pageuri":
				add(key, "salesforce_aura_page_uri", value, RuleSalesforceAuraPageURI, 0.86, "Salesforce aura.pageURI form field", valueIndex+1, len(fieldOccurrences))
			case "com.salesforce.visualforce.viewstate", "viewstatemac", "viewstateversion", "viewstatecsrf":
				add(key, "salesforce_viewstate", value, RuleSalesforceViewState, 0.94, "Salesforce Visualforce ViewState form field", valueIndex+1, len(fieldOccurrences))
			case "_confirmationtoken":
				add(key, "salesforce_confirmation_token", value, RuleSalesforceConfirmationToken, 0.93, "Salesforce confirmation token form field", valueIndex+1, len(fieldOccurrences))
			case "sid", "sid_client", "sid_client_id":
				add(key, "salesforce_frontdoor_sid", value, RuleSalesforceFrontdoorSID, 0.85, "Salesforce frontdoor sid form field", valueIndex+1, len(fieldOccurrences))
			case "cshc":
				add(key, "salesforce_cshc", value, RuleSalesforceCSHC, 0.92, "Salesforce cshc login/session handoff form field", valueIndex+1, len(fieldOccurrences))
			}
		}
	}
	return candidates
}

type rawFormOccurrence struct {
	value      string
	valueStart int
	valueEnd   int
}

// exactRawFormOccurrenceIndex makes one wire-order pass over the form body.
// ParseQuery decodes keys as well as values, but generated extractors can only
// match the literal ASCII Salesforce field spellings below. Retain occurrences
// whose wire key is identical to its decoded key modulo case; encoded keys such
// as aura%2Etoken therefore remain review-only. Key-only fields are excluded
// because every generated regex requires '=', while empty key=value fields are
// retained because they do consume a replay-side regex ordinal.
func exactRawFormOccurrenceIndex(body string) map[string][]rawFormOccurrence {
	index := make(map[string][]rawFormOccurrence)
	for fieldStart := 0; fieldStart <= len(body); {
		fieldEnd := strings.IndexByte(body[fieldStart:], '&')
		if fieldEnd < 0 {
			fieldEnd = len(body)
		} else {
			fieldEnd += fieldStart
		}
		field := body[fieldStart:fieldEnd]
		rawKey, rawValue, hasEquals := strings.Cut(field, "=")
		if !hasEquals {
			if fieldEnd == len(body) {
				break
			}
			fieldStart = fieldEnd + 1
			continue
		}
		key, err := url.QueryUnescape(rawKey)
		normalizedKey := strings.ToLower(key)
		if err != nil || !correlationFormKey(normalizedKey) || !strings.EqualFold(rawKey, key) {
			if fieldEnd == len(body) {
				break
			}
			fieldStart = fieldEnd + 1
			continue
		}
		value, err := url.QueryUnescape(rawValue)
		if err == nil {
			valueStart := fieldStart + len(rawKey) + 1
			index[normalizedKey] = append(index[normalizedKey], rawFormOccurrence{
				value:      value,
				valueStart: valueStart,
				valueEnd:   fieldEnd,
			})
		}
		if fieldEnd == len(body) {
			break
		}
		fieldStart = fieldEnd + 1
	}
	return index
}

func correlationFormKey(key string) bool {
	switch key {
	case "aura.token", "aura.context", "aura.pageuri",
		"com.salesforce.visualforce.viewstate", "viewstatemac", "viewstateversion", "viewstatecsrf",
		"_confirmationtoken", "sid", "sid_client", "sid_client_id",
		// Indexed so the salesforce.cshc rule can match a form/query body;
		// exactRawFormOccurrenceIndex drops every key not listed here.
		"cshc":
		return true
	default:
		return false
	}
}

// formContextRegexOrdinal maps one decoded aura.context candidate back to the
// exact match the replay-side raw regex will select. Discovery patterns are
// intentionally narrower than the wire regex (for example they require a
// six-byte fwuid), so counting only emitted candidates can miss an earlier
// runtime match and silently attach the wrong ordinal.
func formContextRegexOrdinal(body string, occurrence rawFormOccurrence, candidate Candidate, cache map[string][][]int) (ordinal int, exact bool, overflow bool) {
	matches, cached := cache[candidate.Expression]
	if !cached {
		pattern, err := regexp.Compile(candidate.Expression)
		if err != nil {
			return 0, false, false
		}
		matches = pattern.FindAllStringSubmatchIndex(body, maxCorrelationRegexOrdinal+1)
		cache[candidate.Expression] = matches
	}
	for index, match := range matches {
		if len(match) < 4 || match[2] < occurrence.valueStart || match[3] > occurrence.valueEnd {
			continue
		}
		ordinal = index + 1
		if ordinal > maxCorrelationRegexOrdinal {
			return 0, false, true
		}
		captured, err := url.QueryUnescape(body[match[2]:match[3]])
		if err != nil || captured != candidate.Value {
			return 0, false, false
		}
		return ordinal, true, false
	}
	if len(matches) > maxCorrelationRegexOrdinal && occurrence.valueStart >= matches[len(matches)-1][0] {
		return 0, false, true
	}
	return 0, false, false
}

func firstCapturedValue(match []string) string {
	if len(match) < 2 {
		return ""
	}
	// The HTTP regex extractor returns capture group 1 byte-for-byte. Preserve
	// that contract here: trimming a candidate can make detection rewrite the
	// trimmed literal even though replay later extracts surrounding whitespace.
	return match[1]
}

// regexExtractorForMatch preserves the runtime identity of repeated body
// matches. Header extraction does not support regex_ordinal; later matches of a
// shared header regex therefore fail closed instead of advertising an extractor
// that would return the first value.
func regexExtractorForMatch(source, expression string, ordinal int) (string, string, bool) {
	if ordinal <= 1 {
		return "regex", expression, true
	}
	if strings.EqualFold(strings.TrimSpace(source), "body") {
		if ordinal > maxCorrelationRegexOrdinal {
			return "", "", false
		}
		return "regex_ordinal", encodeRegexOrdinalExpression(expression, ordinal), true
	}
	return "", "", false
}

func decodedContains(raw, value string) bool {
	decoded := urlDecoded(raw)
	return decoded != raw && strings.Contains(decoded, value)
}

func formContainsValue(body, value string) bool {
	if formParseTooWide(body) {
		return false
	}
	values, err := url.ParseQuery(body)
	if err != nil || len(values) == 0 {
		return false
	}
	for key, vals := range values {
		if strings.Contains(key, value) {
			return true
		}
		for _, val := range vals {
			if strings.Contains(val, value) {
				return true
			}
		}
	}
	return false
}

func urlDecoded(raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}

func jsonCandidates(body string) []Candidate {
	candidates, _ := jsonCandidatesChecked(body)
	return candidates
}

func jsonCandidatesChecked(body string) ([]Candidate, error) {
	candidates, _, err := jsonCandidatesCheckedWithTruncation(body)
	if len(candidates) > maxCorrelationCandidatesPerObservation {
		candidates = candidates[:maxCorrelationCandidatesPerObservation]
	}
	return candidates, err
}

func jsonCandidatesCheckedWithTruncation(body string) ([]Candidate, bool, error) {
	if parsed, ok, truncated, err := jsonCandidatesFromParsedBodyAnalysis(body, newCorrelationJSONWalkBudget()); err != nil {
		return nil, false, err
	} else if ok {
		return parsed, truncated, nil
	}
	candidates := make([]Candidate, 0)
	truncated := false
	// Regex fallback scanning has its own match-work bound. Ask for one sentinel
	// beyond that bound so a window exhausted by broad false-positive key shapes
	// cannot silently hide a later real token.
	stringMatchLimit := remainingCandidateMatchLimit(candidates)
	stringMatches := jsonStringTokenPattern.FindAllStringSubmatch(body, stringMatchLimit+1)
	if len(stringMatches) > stringMatchLimit {
		stringMatches = stringMatches[:stringMatchLimit]
		truncated = true
	}
	stringOrdinals := make(map[string]int)
	for _, match := range stringMatches {
		if len(match) < 3 {
			continue
		}
		key := match[1]
		expression := jsonFallbackStringFieldRegex(key)
		stringOrdinals[expression]++
		if !dynamicJSONKey(key) {
			continue
		}
		extractType, extractorExpression, replayable := regexExtractorForMatch("body", expression, stringOrdinals[expression])
		if !replayable {
			continue
		}
		ruleID, base := classifyJSONKey(key)
		candidates = appendCandidatesBounded(candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          match[2],
			Source:         "body",
			ExtractType:    extractType,
			Expression:     extractorExpression,
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "JSON field " + key + " looks like a dynamic token/identifier",
		})
	}
	limit := remainingCandidateMatchLimit(candidates)
	if limit == 0 {
		return candidates, true, nil
	}
	numberMatches := jsonNumberIDPattern.FindAllStringSubmatch(body, limit+1)
	if len(numberMatches) > limit {
		numberMatches = numberMatches[:limit]
		truncated = true
	}
	numberOrdinals := make(map[string]int)
	for _, match := range numberMatches {
		if len(match) < 3 {
			continue
		}
		key := match[1]
		expression := jsonFallbackNumberFieldRegex(key)
		numberOrdinals[expression]++
		if !dynamicJSONIDKey(key) || !numericJSONIDValue(match[2]) {
			continue
		}
		extractType, extractorExpression, replayable := regexExtractorForMatch("body", expression, numberOrdinals[expression])
		if !replayable {
			continue
		}
		candidates = appendCandidatesBounded(candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          match[2],
			Source:         "body",
			ExtractType:    extractType,
			Expression:     extractorExpression,
			RuleID:         RuleJSONNumericID,
			BaseConfidence: 0.5,
			Reason:         "JSON numeric id field " + key,
		})
	}
	if len(candidates) > maxCorrelationCandidatesPerObservation {
		truncated = true
	}
	return candidates, truncated, nil
}

type correlationJSONWalkBudget struct {
	constructionBytes    int64
	maxConstructionBytes int64
	candidates           int
	maxCandidates        int
}

func newCorrelationJSONWalkBudget() *correlationJSONWalkBudget {
	return &correlationJSONWalkBudget{
		maxConstructionBytes: maxCorrelationJSONConstructionBytes,
		// Retain one sentinel candidate so an exactly-at-cap document can be
		// distinguished from a document with omitted candidates.
		maxCandidates: maxCorrelationCandidatesPerObservation + 1,
	}
}

func (budget *correlationJSONWalkBudget) chargeConstruction(size int64) error {
	if budget == nil || size < 0 || size > budget.maxConstructionBytes-budget.constructionBytes {
		return fmt.Errorf("%w: JSON candidate/path construction exceeds %d bytes", ErrCorrelationWorkLimit, maxCorrelationJSONConstructionBytes)
	}
	budget.constructionBytes += size
	return nil
}

func (budget *correlationJSONWalkBudget) chargeCandidate(path, key string) error {
	if budget == nil || budget.candidates >= budget.maxCandidates {
		return fmt.Errorf("%w: JSON candidate count exceeds %d", ErrCorrelationWorkLimit, maxCorrelationCandidatesPerObservation)
	}
	// Candidate names/reasons and escaped path/key spellings are newly retained.
	// Six bytes per key byte bounds JSON escaping (for example, \u00XX).
	if err := budget.chargeConstruction(256 + int64(len(path)) + 6*int64(len(key))); err != nil {
		return err
	}
	budget.candidates++
	return nil
}

// validateCorrelationJSONShape performs a streaming pass before Decode can
// materialize attacker-controlled maps/slices. Token, structural-node, and
// nesting limits are aggregate across the document; malformed or multiple JSON
// documents — and documents beyond those structural limits (legitimate large
// pages routinely exceed them) — return valid=false so the byte-faithful,
// bounded regex fallback runs instead of failing the analysis.
func validateCorrelationJSONShape(body string) (valid bool, err error) {
	if strings.TrimSpace(body) == "" {
		return false, nil
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	tokens := 0
	nodes := 0
	depth := 0
	roots := 0
	for {
		token, tokenErr := decoder.Token()
		if tokenErr == io.EOF {
			return depth == 0 && roots == 1, nil
		}
		if tokenErr != nil {
			return false, nil
		}
		tokens++
		if tokens > maxCorrelationJSONTokens {
			return false, nil
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				if depth == 0 {
					roots++
				}
				depth++
				nodes++
				if depth > maxCorrelationJSONDepth {
					return false, nil
				}
			case '}', ']':
				depth--
				if depth < 0 {
					return false, nil
				}
			}
		} else {
			if depth == 0 {
				roots++
			}
			nodes++
		}
		if nodes > maxCorrelationJSONNodes {
			return false, nil
		}
	}
}

func jsonCandidatesFromParsedBodyChecked(body string, budget *correlationJSONWalkBudget) ([]Candidate, bool, error) {
	candidates, valid, _, err := jsonCandidatesFromParsedBodyAnalysis(body, budget)
	if len(candidates) > maxCorrelationCandidatesPerObservation {
		candidates = candidates[:maxCorrelationCandidatesPerObservation]
	}
	return candidates, valid, err
}

func jsonCandidatesFromParsedBodyAnalysis(body string, budget *correlationJSONWalkBudget) ([]Candidate, bool, bool, error) {
	valid, err := validateCorrelationJSONShape(body)
	if err != nil || !valid {
		return nil, valid, false, err
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, false, nil
	}
	// Decode must consume exactly one JSON document. If an XSSI prefix, JSONP
	// wrapper, or trailing payload makes the body non-JSON, use replayable regex
	// fallbacks instead of advertising a JSONPath the runtime cannot evaluate.
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false, false, nil
	}
	candidates := make([]Candidate, 0, min(16, maxCorrelationCandidatesPerObservation))
	if err := walkJSONCandidates(value, "$", true, 0, &candidates, budget); err != nil {
		if errors.Is(err, ErrCorrelationWorkLimit) {
			// Deterministic walk budget exhausted mid-document: keep the bounded
			// prefix for best-effort callers and mark it incomplete for checked
			// detection/diff/repair callers.
			return candidates, true, true, nil
		}
		return nil, true, false, err
	}
	return candidates, true, false, nil
}

// walkJSONCandidates descends the parsed body collecting correlation candidates.
// The `evaluable` flag tracks whether every object-key segment of `path` so far
// is one the replay-side lite JSONPath extractor (extractJSONPathValue) can
// resolve — i.e. a dot-identifier or an array index. A non-identifier key
// (hyphenated/dotted/etc.) is emitted by jsonPathChild as a bracket-quoted
// segment (`$["x-csrf-token"]`) that the extractor cannot evaluate, so any
// candidate under such a path would silently error at replay. We keep descending
// (a deeper subtree might still be useful for review) but do not EMIT candidates
// once the path is no longer evaluable, rather than emitting broken extractors.
func walkJSONCandidates(value any, path string, evaluable bool, depth int, candidates *[]Candidate, budget *correlationJSONWalkBudget) error {
	if depth > maxCorrelationJSONDepth {
		return fmt.Errorf("%w: JSON nesting exceeds %d", ErrCorrelationWorkLimit, maxCorrelationJSONDepth)
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > maxCorrelationStructuredFields {
			return fmt.Errorf("%w: JSON object exceeds %d fields", ErrCorrelationWorkLimit, maxCorrelationStructuredFields)
		}
		if err := budget.chargeConstruction(int64(len(typed)) * 16); err != nil {
			return err
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childEvaluable := evaluable && jsonPathIdentifier.MatchString(key)
			if err := budget.chargeConstruction(int64(len(path)+4) + 6*int64(len(key))); err != nil {
				return err
			}
			if err := walkJSONCandidates(typed[key], jsonPathChild(path, key), childEvaluable, depth+1, candidates, budget); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > maxCorrelationStructuredFields {
			return fmt.Errorf("%w: JSON array exceeds %d entries", ErrCorrelationWorkLimit, maxCorrelationStructuredFields)
		}
		for index, item := range typed {
			indexBytes := decimalIndexBytes(index)
			if err := budget.chargeConstruction(int64(len(path) + indexBytes + 2)); err != nil {
				return err
			}
			indexText := strconv.Itoa(index)
			if err := walkJSONCandidates(item, path+"["+indexText+"]", evaluable, depth+1, candidates, budget); err != nil {
				return err
			}
		}
	case string:
		key := jsonPathLastKey(path)
		if key == "" || len(typed) < 6 || !dynamicJSONKey(key) || !evaluable {
			return nil
		}
		if err := budget.chargeCandidate(path, key); err != nil {
			return err
		}
		ruleID, base := classifyJSONKey(key)
		*candidates = appendCandidatesBounded(*candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          typed,
			Source:         "body",
			ExtractType:    "jsonpath",
			Expression:     path,
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "JSON field " + key + " looks like a dynamic token/identifier",
		})
	case json.Number:
		key := jsonPathLastKey(path)
		number := typed.String()
		if key == "" || !dynamicJSONIDKey(key) || !numericJSONIDValue(number) || !evaluable {
			return nil
		}
		if err := budget.chargeCandidate(path, key); err != nil {
			return err
		}
		*candidates = appendCandidatesBounded(*candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          number,
			Source:         "body",
			ExtractType:    "jsonpath",
			Expression:     path,
			RuleID:         RuleJSONNumericID,
			BaseConfidence: 0.5,
			Reason:         "JSON numeric id field " + key,
		})
	}
	return nil
}

func decimalIndexBytes(value int) int {
	if value == 0 {
		return 1
	}
	digits := 0
	for value > 0 {
		digits++
		value /= 10
	}
	return digits
}

func jsonFallbackStringFieldRegex(key string) string {
	// The key spelling comes from this concrete wire match. Keep it
	// case-sensitive so differently-cased keys do not share a first-match regex.
	return fmt.Sprintf(`"%s"\s*:\s*"([^"]{6,})"`, regexp.QuoteMeta(key))
}

func jsonFallbackNumberFieldRegex(key string) string {
	return fmt.Sprintf(`"%s"\s*:\s*(\d{4,})(?:\s*[,}\]]|\s*$)`, regexp.QuoteMeta(key))
}

func numericJSONIDValue(value string) bool {
	return len(value) >= 4 && allDigits.MatchString(value)
}

// dynamicJSONIDKey is the numeric-ID subset of dynamicJSONKey. Tokenizing the
// field name prevents benign keys such as "valid" or "consider" from matching
// merely because their spelling contains the substring "id".
func dynamicJSONIDKey(key string) bool {
	tokens := correlationKeyTokens(key)
	for _, token := range tokens {
		if token == "id" || token == "sid" {
			return true
		}
	}
	switch strings.Join(tokens, "") {
	case "sessionid", "jmscorrelationid", "jmsmessageid", "messageid", "operationid", "subscriptionid", "correlationid":
		return true
	default:
		return false
	}
}

func dynamicJSONKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	tokens := correlationKeyTokens(key)
	compact := strings.Join(tokens, "")
	switch compact {
	case "accesstoken", "idtoken", "refreshtoken", "sessionid", "jmscorrelationid", "jmsmessageid", "messageid", "operationid", "subscriptionid", "correlationid":
		return true
	}
	for _, token := range tokens {
		switch token {
		case "token", "csrf", "xsrf", "nonce", "state", "session", "sid", "auth", "authorization", "bearer", "websocket", "correlation":
			return true
		case "id":
			return true
		}
	}
	return lower == "id" ||
		strings.HasSuffix(key, "Id") ||
		strings.HasSuffix(key, "ID") ||
		strings.HasSuffix(lower, "_id") ||
		strings.HasSuffix(lower, "-id") ||
		strings.HasSuffix(lower, ".id")
}

func correlationKeyTokens(key string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	runes := []rune(strings.TrimSpace(key))
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && current.Len() > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || nextLower {
				flush()
			}
		}
		current.WriteRune(unicode.ToLower(r))
	}
	flush()
	return tokens
}

func jsonPathChild(parent string, key string) string {
	if jsonPathIdentifier.MatchString(key) {
		return parent + "." + key
	}
	return parent + `["` + strings.ReplaceAll(key, `"`, `\"`) + `"]`
}

var jsonPathIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func jsonPathLastKey(path string) string {
	if strings.HasSuffix(path, `"]`) {
		start := strings.LastIndex(path, `["`)
		if start >= 0 {
			return strings.ReplaceAll(path[start+2:len(path)-2], `\"`, `"`)
		}
	}
	lastDot := strings.LastIndex(path, ".")
	if lastDot >= 0 && lastDot+1 < len(path) {
		key := path[lastDot+1:]
		if bracket := strings.Index(key, "["); bracket >= 0 {
			key = key[:bracket]
		}
		return key
	}
	return ""
}

// classifyJSONKey picks the most specific rule (and base confidence) for a JSON
// key, so CSRF/auth tokens rank above generic ids.
func classifyJSONKey(key string) (string, float64) {
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "jmscorrelationid"), strings.Contains(lower, "jmsmessageid"),
		strings.Contains(lower, "jms_correlation"), strings.Contains(lower, "jms_message"):
		return RuleJMSCorrelationID, 0.88
	case strings.Contains(lower, "websocket"), strings.Contains(lower, "subscriptionid"),
		strings.Contains(lower, "operationid"), strings.Contains(lower, "messageid"),
		strings.Contains(lower, "correlationid"), strings.Contains(lower, "correlation_id"):
		return RuleWebSocketID, 0.84
	case strings.Contains(lower, "csrf"), strings.Contains(lower, "xsrf"):
		return RuleJSONCSRFToken, 0.9
	case strings.Contains(lower, "access_token"), strings.Contains(lower, "id_token"),
		strings.Contains(lower, "refresh_token"), strings.Contains(lower, "token"):
		return RuleJSONAuthToken, 0.85
	case strings.Contains(lower, "session"), strings.Contains(lower, "sid"):
		return RuleJSONSessionID, 0.85
	default:
		return RuleJSONDynamicID, 0.6
	}
}

func htmlInputCandidates(body string) []Candidate {
	candidates := make([]Candidate, 0)
	ordinals := make(map[string]int)
	for _, pattern := range htmlInputPatterns {
		limit := remainingCandidateMatchLimit(candidates)
		if limit == 0 {
			return candidates
		}
		for _, match := range pattern.re.FindAllStringSubmatch(body, limit) {
			if len(match) <= pattern.valueGroup || len(match) <= pattern.nameGroup {
				continue
			}
			field := match[pattern.nameGroup]
			value := match[pattern.valueGroup]
			if value == "" {
				continue
			}
			expression := fmt.Sprintf(`(?i)<input[^>]*\bname="%s"[^>]*\bvalue="([^"]{4,})"`, regexp.QuoteMeta(field))
			if pattern.nameGroup > pattern.valueGroup {
				expression = fmt.Sprintf(`(?i)<input[^>]*\bvalue="([^"]{4,})"[^>]*\bname="%s"`, regexp.QuoteMeta(field))
			}
			identity := strings.ToLower(expression)
			ordinals[identity]++
			extractType, extractorExpression, replayable := regexExtractorForMatch("body", expression, ordinals[identity])
			if !replayable {
				continue
			}
			ruleID, base, reason := htmlInputCorrelationRule(field)
			candidates = appendCandidatesBounded(candidates, Candidate{
				Name:           "corr_" + Slug(field),
				Value:          value,
				Source:         "body",
				ExtractType:    extractType,
				Expression:     extractorExpression,
				RuleID:         ruleID,
				BaseConfidence: base,
				Reason:         reason,
			})
		}
	}
	return candidates
}

func htmlInputCorrelationRule(field string) (string, float64, string) {
	lower := strings.ToLower(strings.TrimSpace(field))
	if strings.Contains(lower, "visualforce.viewstate") {
		return RuleSalesforceViewState, 0.94, "Salesforce Visualforce ViewState hidden field"
	}
	return RuleHTMLHiddenToken, 0.9, "Hidden form field " + field + " (anti-CSRF/state token)"
}

// aspNetHiddenCandidates handles ASP.NET WebForms state fields, which are the
// classic correlation case for .NET apps and a frequent LoadRunner migration
// pain point. They are large opaque hidden inputs that change every request.
func aspNetHiddenCandidates(body string) []Candidate {
	candidates, _ := aspNetHiddenCandidatesWithTruncation(body)
	return candidates
}

func aspNetHiddenCandidatesWithTruncation(body string) ([]Candidate, bool) {
	candidates := make([]Candidate, 0)
	ordinals := make(map[string]int)
	matchLimit := maxCorrelationCandidatesPerObservation + 1
	matches := aspNetHiddenPattern.FindAllStringSubmatch(body, matchLimit+1)
	truncated := len(matches) > matchLimit
	if truncated {
		matches = matches[:matchLimit]
	}
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		field := match[1]
		value := match[2]
		if value == "" {
			continue
		}
		expression := fmt.Sprintf(`(?i)<input[^>]*\bname="%s"[^>]*\bvalue="([^"]+)"`, regexp.QuoteMeta(field))
		identity := strings.ToLower(expression)
		ordinals[identity]++
		extractType, extractorExpression, replayable := regexExtractorForMatch("body", expression, ordinals[identity])
		if !replayable {
			continue
		}
		candidates = appendCandidatesBounded(candidates, Candidate{
			Name:           "corr_" + Slug(field),
			Value:          value,
			Source:         "body",
			ExtractType:    extractType,
			Expression:     extractorExpression,
			RuleID:         RuleASPNetState,
			BaseConfidence: 0.95,
			Reason:         "ASP.NET state field " + field,
		})
	}
	if len(candidates) > maxCorrelationCandidatesPerObservation {
		truncated = true
	}
	return candidates, truncated
}

func xmlCandidates(body string) []Candidate {
	candidates, _ := xmlCandidatesWithTruncation(body)
	return candidates
}

func xmlCandidatesWithTruncation(body string) ([]Candidate, bool) {
	candidates := make([]Candidate, 0)
	elementOrdinals := make(map[string]int)
	attributeOrdinals := make(map[string]int)
	matchLimit := maxCorrelationCandidatesPerObservation + 1
	elementMatches := xmlElementTokenPattern.FindAllStringSubmatchIndex(body, matchLimit+1)
	truncated := len(elementMatches) > matchLimit
	if truncated {
		elementMatches = elementMatches[:matchLimit]
	}
	for _, match := range elementMatches {
		name := regexpIndexedSubmatch(body, match, 1)
		value := regexpIndexedSubmatch(body, match, 2)
		closingName := regexpIndexedSubmatch(body, match, 3)
		// XML element names are case-sensitive. The broad discovery regex is
		// case-insensitive for ecosystem tolerance, so explicitly reject a
		// mismatched close tag before assigning a runtime ordinal.
		if name == "" || name != closingName {
			continue
		}
		localName := localXMLName(name)
		expression := fmt.Sprintf(`(?s)<(?:[A-Za-z0-9_.-]+:)?%s\b[^>]*>([^<]{6,})</(?:[A-Za-z0-9_.-]+:)?%s>`, regexp.QuoteMeta(localName), regexp.QuoteMeta(localName))
		elementOrdinals[expression]++
		ordinal := elementOrdinals[expression]
		// Whitespace-only values are not useful candidates, but they still match
		// the replay expression and therefore consume an ordinal.
		if strings.TrimSpace(value) == "" {
			continue
		}
		extractType, extractorExpression, replayable := regexExtractorForMatch("body", expression, ordinal)
		if !replayable {
			continue
		}
		ruleID, base := classifyXMLName(name)
		candidates = appendCandidatesBounded(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    extractType,
			Expression:     extractorExpression,
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "XML/SOAP element " + name + " looks dynamic",
		})
	}
	if len(candidates) > maxCorrelationCandidatesPerObservation {
		return candidates, true
	}
	remaining := remainingCandidateMatchLimit(candidates)
	if remaining == 0 {
		return candidates, true
	}
	attributeMatches := xmlAttrTokenPattern.FindAllStringSubmatchIndex(body, remaining+1)
	if len(attributeMatches) > remaining {
		attributeMatches = attributeMatches[:remaining]
		truncated = true
	}
	for _, match := range attributeMatches {
		name := regexpIndexedSubmatch(body, match, 1)
		value := regexpIndexedSubmatch(body, match, 2)
		if name == "" {
			continue
		}
		localName := localXMLName(name)
		expression := fmt.Sprintf(`(?s)\b(?:[A-Za-z0-9_.-]+:)?%s\s*=\s*"([^"]{6,})"`, regexp.QuoteMeta(localName))
		attributeOrdinals[expression]++
		ordinal := attributeOrdinals[expression]
		if strings.TrimSpace(value) == "" {
			continue
		}
		extractType, extractorExpression, replayable := regexExtractorForMatch("body", expression, ordinal)
		if !replayable {
			continue
		}
		ruleID, base := classifyXMLName(name)
		candidates = appendCandidatesBounded(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    extractType,
			Expression:     extractorExpression,
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "XML/SOAP attribute " + name + " looks dynamic",
		})
	}
	if len(candidates) > maxCorrelationCandidatesPerObservation {
		truncated = true
	}
	return candidates, truncated
}

func regexpIndexedSubmatch(input string, match []int, group int) string {
	index := group * 2
	if index+1 >= len(match) || match[index] < 0 || match[index+1] < match[index] {
		return ""
	}
	return input[match[index]:match[index+1]]
}

func classifyXMLName(name string) (string, float64) {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "jmscorrelationid"), strings.Contains(lower, "jmsmessageid"):
		return RuleJMSCorrelationID, 0.88
	case strings.Contains(lower, "messageid"), strings.Contains(lower, "relatesto"),
		strings.Contains(lower, "correlationid"), strings.Contains(lower, "correlation"):
		return RuleXMLSOAPToken, 0.88
	case strings.Contains(lower, "token"), strings.Contains(lower, "session"),
		strings.Contains(lower, "csrf"), strings.Contains(lower, "xsrf"),
		strings.Contains(lower, "nonce"), strings.Contains(lower, "state"):
		return RuleXMLSOAPToken, 0.88
	default:
		return RuleXMLDynamicID, 0.6
	}
}

func localXMLName(name string) string {
	if _, after, ok := strings.Cut(name, ":"); ok {
		return after
	}
	return name
}

func headerCandidates(headers map[string]string) []Candidate {
	candidates := make([]Candidate, 0)
	// Deterministic key order so equal-confidence output is stable.
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Go's header runtime treats field names case-insensitively, while a generic
	// observation map can still contain case-variant keys. Compute physical
	// occurrence ordinals across those variants in the same sorted order used by
	// flattenHeaders; resetting at each map key can make a later candidate's
	// regex capture an earlier differently-cased field.
	headerValueCounts := make(map[string]int)
	unaddressableHeaders := make(map[string]bool)
	for _, key := range keys {
		lowerKey := strings.ToLower(key)
		if headerValueSplitTooWide(headers[key]) {
			unaddressableHeaders[lowerKey] = true
			continue
		}
		headerValueCounts[lowerKey] += len(strings.Split(headers[key], "\n"))
	}
	headerValueOrdinals := make(map[string]int)
	cookieOccurrences := make(map[string]int)
	for _, key := range keys {
		if len(candidates) > maxCorrelationCandidatesPerObservation {
			return candidates
		}
		lowerKey := strings.ToLower(key)
		if unaddressableHeaders[lowerKey] {
			// Skip only this header's occurrence split; a pathologically wide
			// repeated header must not abort the whole response analysis. Skip
			// case variants too because their replay ordinals cannot be proven
			// after omitting the wide occurrence set.
			continue
		}
		values := strings.Split(headers[key], "\n")
		baseOrdinal := headerValueOrdinals[lowerKey]
		for valueIndex, value := range values {
			if len(candidates) > maxCorrelationCandidatesPerObservation {
				return candidates
			}
			rawValue := value
			trimmedValue := strings.TrimSpace(rawValue)
			if trimmedValue == "" {
				continue
			}
			if lowerKey == "set-cookie" {
				candidates = appendCandidatesBounded(candidates, setCookieCandidates(key, rawValue, cookieOccurrences)...)
				continue
			}
			if lowerKey == "location" {
				locationMatches := locationCandidates(key, trimmedValue, baseOrdinal+valueIndex, headerValueCounts[lowerKey])
				candidates = appendCandidatesBounded(candidates, locationMatches...)
				if len(candidates) > maxCorrelationCandidatesPerObservation {
					return candidates
				}
				if len(locationMatches) == 0 && shouldCorrelateWholeLocation(trimmedValue) {
					extractType, expression, replayable := headerExtractorForOccurrence(key, baseOrdinal+valueIndex, headerValueCounts[lowerKey])
					if !replayable {
						continue
					}
					// A redirect target frequently carries a one-time code/state that the
					// next request must echo back. Capture the whole value for review.
					candidates = appendCandidatesBounded(candidates, Candidate{
						Name:           "corr_location",
						Value:          rawValue,
						Source:         "header",
						ExtractType:    extractType,
						Expression:     expression,
						RuleID:         RuleLocationRedirect,
						BaseConfidence: 0.6,
						Reason:         "Location redirect target may carry a one-time code/state",
					})
				}
				continue
			}
			jmsHeader := isJMSHeaderName(lowerKey)
			webSocketHeader := isWebSocketHeaderName(lowerKey)
			dynamicHeader := dynamicHeaderName(lowerKey) && len(trimmedValue) >= 4
			if !jmsHeader && !webSocketHeader && !dynamicHeader {
				continue
			}
			extractType, expression, replayable := headerExtractorForOccurrence(key, baseOrdinal+valueIndex, headerValueCounts[lowerKey])
			if !replayable {
				continue
			}
			if jmsHeader {
				candidates = appendCandidatesBounded(candidates, Candidate{
					Name:           "corr_" + Slug(lowerKey),
					Value:          rawValue,
					Source:         "header",
					ExtractType:    extractType,
					Expression:     expression,
					RuleID:         RuleJMSCorrelationID,
					BaseConfidence: 0.88,
					Reason:         "JMS correlation/message header " + key,
				})
				continue
			}
			if webSocketHeader {
				candidates = appendCandidatesBounded(candidates, Candidate{
					Name:           "corr_" + Slug(lowerKey),
					Value:          rawValue,
					Source:         "header",
					ExtractType:    extractType,
					Expression:     expression,
					RuleID:         RuleWebSocketID,
					BaseConfidence: 0.84,
					Reason:         "WebSocket correlation header " + key,
				})
				continue
			}
			candidates = appendCandidatesBounded(candidates, Candidate{
				Name:           "corr_" + Slug(lowerKey),
				Value:          rawValue,
				Source:         "header",
				ExtractType:    extractType,
				Expression:     expression,
				RuleID:         RuleDynamicHeader,
				BaseConfidence: 0.8,
				Reason:         "Response header " + key + " looks dynamic (token/auth/id)",
			})
		}
		headerValueOrdinals[lowerKey] = baseOrdinal + len(values)
	}
	return candidates
}

func headerExtractorForOccurrence(name string, zeroBasedOrdinal, total int) (string, string, bool) {
	if zeroBasedOrdinal < 0 || zeroBasedOrdinal > maxCorrelationRegexOrdinal {
		return "", "", false
	}
	if total <= 1 {
		return "header", name, true
	}
	escapedName := regexp.QuoteMeta(name)
	if zeroBasedOrdinal <= 0 {
		return "regex", fmt.Sprintf(`(?im)^%s: ([^\r\n]+)`, escapedName), true
	}
	return "regex", fmt.Sprintf(`(?im)(?:^%s: [^\r\n]*\r?\n(?:^[^\r\n]*\r?\n)*?){%d}^%s: ([^\r\n]+)`, escapedName, zeroBasedOrdinal, escapedName), true
}

func shouldCorrelateWholeLocation(value string) bool {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 12 {
		return false
	}
	candidate := trimmed
	if parsed, err := url.Parse(trimmed); err == nil {
		if parsed.RawQuery != "" {
			return true
		}
		candidate = lastPathSegment(parsed.Path)
		if len(candidate) < 12 {
			return false
		}
	}
	return looksDynamicLocationValue(candidate)
}

func lastPathSegment(pathValue string) string {
	pathValue = strings.Trim(pathValue, "/")
	if pathValue == "" {
		return ""
	}
	if idx := strings.LastIndex(pathValue, "/"); idx >= 0 {
		return pathValue[idx+1:]
	}
	return pathValue
}

func looksDynamicLocationValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(lower, "token") ||
		strings.Contains(lower, "code") ||
		strings.Contains(lower, "state") ||
		strings.Contains(lower, "session") ||
		strings.Contains(lower, "nonce") ||
		strings.Contains(lower, "csrf") ||
		strings.Contains(lower, "xsrf") {
		return true
	}
	hasLetter := false
	hasDigit := false
	for _, r := range lower {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

func locationCandidates(headerName string, value string, headerOrdinal, headerCount int) []Candidate {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil
	}
	if formParseTooWide(parsed.RawQuery) {
		return nil
	}
	params := parsed.Query()
	if len(params) == 0 {
		return nil
	}
	valuesByKey := exactLocationQueryValueIndex(parsed.RawQuery, params)
	keys := make([]string, 0, len(valuesByKey))
	for key := range valuesByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	candidates := make([]Candidate, 0, min(len(keys), maxCorrelationCandidatesPerObservation+1))
	for _, key := range keys {
		if len(candidates) > maxCorrelationCandidatesPerObservation {
			return candidates
		}
		indexed := valuesByKey[key]
		if !indexed.replayable || len(indexed.values) == 0 {
			continue
		}
		for valueOrdinal, queryValue := range indexed.values {
			if len(queryValue) < 4 {
				continue
			}
			expression, replayable := locationHeaderQueryRegex(headerName, key, headerOrdinal, headerCount, valueOrdinal)
			if !replayable {
				continue
			}
			candidates = appendCandidatesBounded(candidates, Candidate{
				Name:           "corr_location_" + Slug(key),
				Value:          queryValue,
				Source:         "header",
				ExtractType:    "regex",
				Expression:     expression,
				Convert:        "url_decode",
				RuleID:         RuleLocationParam,
				BaseConfidence: 0.78,
				Reason:         "Location query parameter " + key + " looks dynamic",
			})
		}
	}
	return candidates
}

type exactLocationQueryValues struct {
	values     []string
	replayable bool
}

// exactLocationQueryValueIndex verifies every relevant query occurrence in one
// wire-order pass. ParseQuery's successfully decoded key set is supplied so its
// malformed-semicolon/error behavior remains authoritative. Generated regexes
// intentionally capture wire values and cannot replay percent-encoded keys.
func exactLocationQueryValueIndex(rawQuery string, parsed url.Values) map[string]*exactLocationQueryValues {
	index := make(map[string]*exactLocationQueryValues)
	for key := range parsed {
		if dynamicLocationParamName(key) {
			index[key] = &exactLocationQueryValues{replayable: true}
		}
	}
	if len(index) == 0 {
		return index
	}
	for fieldStart := 0; fieldStart <= len(rawQuery); {
		fieldEnd := strings.IndexByte(rawQuery[fieldStart:], '&')
		if fieldEnd < 0 {
			fieldEnd = len(rawQuery)
		} else {
			fieldEnd += fieldStart
		}
		field := rawQuery[fieldStart:fieldEnd]
		rawKey, rawValue, hasEquals := strings.Cut(field, "=")
		decodedKey, err := url.QueryUnescape(rawKey)
		if err == nil {
			if values := index[decodedKey]; values != nil {
				switch {
				case rawKey != decodedKey:
					values.replayable = false
				case !hasEquals || rawValue == "":
					// The runtime regex also ignores key-only and empty fields, so
					// neither consumes a value ordinal.
				case strings.IndexFunc(rawValue, unicode.IsSpace) >= 0:
					// The runtime capture class stops at whitespace, so a literal
					// space in the wire value would replay as a truncated prefix.
					values.replayable = false
				default:
					decodedValue, decodeErr := url.QueryUnescape(rawValue)
					if decodeErr != nil {
						values.replayable = false
					} else {
						values.values = append(values.values, decodedValue)
					}
				}
			}
		}
		if fieldEnd == len(rawQuery) {
			break
		}
		fieldStart = fieldEnd + 1
	}
	return index
}

func setCookieCandidates(headerName string, value string, occurrences map[string]int) []Candidate {
	// RFC 6265 gives each physical Set-Cookie field exactly one leading
	// cookie-pair. Every later name=value segment is an attribute (including
	// unknown extension attributes), not another cookie. Treating those
	// attributes as cookies can generate a replay extractor for a value that a
	// user agent never stores or sends.
	replayName, replayValue, replayable := parseSetCookieExtractorPair(value)
	ordinal := 0
	if replayable {
		ordinal = occurrences[replayName]
		occurrences[replayName] = ordinal + 1
	}
	cookieName, cookieValue, ok := parseSetCookiePair(value)
	if !ok || cookieValue == "" {
		return nil
	}
	// Candidate validation is intentionally stricter than the replay regex:
	// malformed recorded cookies must not become automatic correlations. The
	// occurrence counter above nevertheless mirrors every line the generated
	// regex can consume. Otherwise an empty or malformed earlier field can shift
	// the regex ordinal and make a later valid candidate extract the wrong value.
	if !replayable || replayName != cookieName || replayValue != cookieValue {
		return nil
	}
	if len(cookieValue) < 4 {
		return nil
	}
	ruleID, base := RuleSetCookieOther, 0.55
	if isSessionCookieName(cookieName) {
		ruleID, base = RuleSetCookieSession, 0.8
	}
	// Cookie names are case-sensitive. In particular, SID and sid can coexist
	// and must not share an occurrence ordinal or a case-insensitive extractor.
	expression, replayable := setCookieExtractorForOccurrence(headerName, cookieName, ordinal)
	if !replayable {
		return nil
	}
	return []Candidate{{
		Name:           "corr_cookie_" + Slug(cookieName),
		Value:          cookieValue,
		Source:         "header",
		ExtractType:    "regex",
		Expression:     expression,
		RuleID:         ruleID,
		BaseConfidence: base,
		Reason:         "Set-Cookie " + cookieName,
	}}
}

// parseSetCookieExtractorPair mirrors the deliberately permissive value grammar
// used by setCookieExtractorForOccurrence. It exists solely for ordinal
// accounting: strict candidate eligibility remains parseSetCookiePair's job.
// In particular, an unmatched optional quote is not a valid cookie, but the
// replay regex can still consume it and therefore a later occurrence must count
// it when selecting its ordinal.
func parseSetCookieExtractorPair(line string) (string, string, bool) {
	line = strings.TrimLeft(line, " \t")
	name, remainder, found := strings.Cut(line, "=")
	if !found || !validCookieName(name) {
		return "", "", false
	}
	remainder = strings.TrimLeft(remainder, " \t")
	if strings.HasPrefix(remainder, `"`) {
		remainder = remainder[1:]
	}
	valueEnd := 0
	for valueEnd < len(remainder) {
		switch remainder[valueEnd] {
		case '"', ';', ',', ' ', '\t', '\r', '\n', '\f':
			goto valueComplete
		default:
			valueEnd++
		}
	}

valueComplete:
	if valueEnd == 0 {
		return "", "", false
	}
	value := remainder[:valueEnd]
	remainder = remainder[valueEnd:]
	if strings.HasPrefix(remainder, `"`) {
		remainder = remainder[1:]
	}
	remainder = strings.TrimLeft(remainder, " \t")
	if remainder != "" && remainder[0] != ';' {
		return "", "", false
	}
	return name, value, true
}

func parseSetCookiePair(line string) (string, string, bool) {
	line = strings.TrimLeft(line, " \t")
	pair, _, _ := strings.Cut(line, ";")
	pair = strings.TrimRight(pair, " \t")
	name, rawValue, found := strings.Cut(pair, "=")
	if !found || name != strings.TrimSpace(name) || !validCookieName(name) {
		return "", "", false
	}
	rawValue = strings.TrimSpace(rawValue)
	if len(rawValue) >= 2 && rawValue[0] == '"' {
		if rawValue[len(rawValue)-1] != '"' {
			return "", "", false
		}
		rawValue = rawValue[1 : len(rawValue)-1]
	} else if strings.Contains(rawValue, `"`) {
		return "", "", false
	}
	for index := 0; index < len(rawValue); index++ {
		if !validCookieOctet(rawValue[index]) {
			return "", "", false
		}
	}
	return name, rawValue, true
}

func validCookieName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		char := name[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func validCookieOctet(char byte) bool {
	return char == 0x21 ||
		char >= 0x23 && char <= 0x2b ||
		char >= 0x2d && char <= 0x3a ||
		char >= 0x3c && char <= 0x5b ||
		char >= 0x5d && char <= 0x7e
}

func setCookieExtractorForOccurrence(headerName, cookieName string, zeroBasedOrdinal int) (string, bool) {
	if zeroBasedOrdinal < 0 || zeroBasedOrdinal > maxCorrelationRegexOrdinal {
		return "", false
	}
	header := `(?i:` + regexp.QuoteMeta(headerName) + `)`
	cookie := regexp.QuoteMeta(cookieName)
	// The header name is case-insensitive, but the cookie name is not. Capture
	// the stored value without optional RFC quotes so it matches the value sent
	// later in the Cookie request header.
	prefix := fmt.Sprintf(`^%s:[ \t]*%s=[ \t]*`, header, cookie)
	capture := prefix + `"?([^";\s,]+)"?(?:[ \t]*;|[ \t]*$)`
	if zeroBasedOrdinal <= 0 {
		return `(?m)` + capture, true
	}
	// Count only prior same-name lines that the final capture grammar could
	// actually consume. A prefix-only repetition also counts `sid=`; after an
	// omitted empty value, that shifts every later ordinal by one.
	prior := prefix + `"?[^";\s,]+"?(?:[ \t]*;[^\r\n]*|[ \t]*)\r?\n`
	return fmt.Sprintf(`(?m)(?:%s(?:^[^\r\n]*\r?\n)*?){%d}%s`, prior, zeroBasedOrdinal, capture), true
}

func locationHeaderQueryRegex(headerName, queryKey string, headerOrdinal, headerCount, valueOrdinal int) (string, bool) {
	if headerOrdinal < 0 || headerOrdinal > maxCorrelationRegexOrdinal || valueOrdinal < 0 || valueOrdinal > maxCorrelationRegexOrdinal {
		return "", false
	}
	header := `(?i:` + regexp.QuoteMeta(headerName) + `)`
	key := regexp.QuoteMeta(queryKey)
	var expression strings.Builder
	expression.WriteString(`(?m)`)
	if headerCount > 1 && headerOrdinal > 0 {
		// [ \t]* (not \s*) so an empty header value cannot swallow its own
		// newline and make one counted iteration consume two physical lines.
		// Allow unrelated flattened headers between case-variant map keys while
		// still counting only Location occurrences.
		fmt.Fprintf(&expression, `(?:^%s:[ \t]*[^\r\n]*\r?\n(?:^[^\r\n]*\r?\n)*?){%d}`, header, headerOrdinal)
	}
	// Anchor on the first '?' (the true query start) and only advance across
	// whole '&'-separated fields, so `key=` matches solely at top-level field
	// starts. Scanning the whole line for `[?&]key=` also matched inside the
	// path or inside another parameter's unencoded nested-URL value.
	fmt.Fprintf(&expression, `^%s:[ \t]*[^?\r\n]*?\?`, header)
	if valueOrdinal > 0 {
		fmt.Fprintf(&expression, `(?:(?:[^&#\r\n]*&)*?%s=[^&#\s]+&){%d}`, key, valueOrdinal)
	}
	fmt.Fprintf(&expression, `(?:[^&#\r\n]*&)*?%s=([^&#\s]+)`, key)
	return expression.String(), true
}

func isSessionCookieName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "session") || strings.Contains(lower, "sid") ||
		strings.Contains(lower, "auth") || strings.Contains(lower, "csrf") ||
		strings.Contains(lower, "xsrf") || strings.Contains(lower, "token")
}

func dynamicHeaderName(lowerKey string) bool {
	return strings.Contains(lowerKey, "token") ||
		strings.Contains(lowerKey, "cookie") ||
		strings.Contains(lowerKey, "location") ||
		strings.Contains(lowerKey, "id") ||
		strings.Contains(lowerKey, "auth") ||
		strings.Contains(lowerKey, "api-key") ||
		strings.Contains(lowerKey, "apikey") ||
		strings.Contains(lowerKey, "secret") ||
		strings.HasSuffix(lowerKey, "-key") ||
		strings.HasSuffix(lowerKey, "_key") ||
		strings.Contains(lowerKey, "csrf") ||
		strings.Contains(lowerKey, "xsrf")
}

func dynamicLocationParamName(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "code") ||
		strings.Contains(lower, "state") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "session") ||
		strings.Contains(lower, "csrf") ||
		strings.Contains(lower, "xsrf") ||
		strings.Contains(lower, "nonce") ||
		strings.Contains(lower, "id")
}

func isJMSHeaderName(lowerKey string) bool {
	return strings.Contains(lowerKey, "jmscorrelationid") ||
		strings.Contains(lowerKey, "jms-correlation-id") ||
		strings.Contains(lowerKey, "jmsmessageid") ||
		strings.Contains(lowerKey, "jms-message-id")
}

func isWebSocketHeaderName(lowerKey string) bool {
	return strings.Contains(lowerKey, "websocket-correlation") ||
		strings.Contains(lowerKey, "websocket-message") ||
		strings.Contains(lowerKey, "ws-correlation") ||
		strings.Contains(lowerKey, "ws-message")
}
