package har

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// Request input kinds: what a request value that changed between the two
// recordings most likely is, and so how replay should obtain it.
const (
	DiffInputTimestamp   = "client_timestamp"
	DiffInputCounter     = "client_counter"
	DiffInputGeneratedID = "client_generated_id"
	DiffInputServerValue = "server_value"
	DiffInputUnknown     = "unknown_origin"
	// DiffInputNotChecked is a value whose origin was not searched for: it is
	// too short to match reliably, or the response search budget ran out.
	DiffInputNotChecked = "origin_not_checked"
)

const (
	// diffInputMinValueBytes skips values too short to tell apart from noise
	// (single digits, short counters such as Aura's r=).
	diffInputMinValueBytes = 4
	// diffInputMinSearchBytes is the shortest value searched for in earlier
	// responses; a shorter one matches unrelated text too easily.
	diffInputMinSearchBytes = 8
	// diffInputMaxBodyBytes bounds the request body a field scan parses.
	diffInputMaxBodyBytes = 4 << 20
	// diffInputMaxFieldsPerRequest bounds the fields one request contributes.
	diffInputMaxFieldsPerRequest = 4096
	// diffInputMaxJSONDepth bounds nested JSON a field scan descends.
	diffInputMaxJSONDepth = 32
	// diffInputMaxFieldBytes bounds a reported field name.
	diffInputMaxFieldBytes = 200
	// diffInputMaxChanges bounds the distinct changed values one pair records.
	diffInputMaxChanges = 20000
	// diffInputMaxComparisonValues bounds the comparison field values kept (as
	// hashes) to recognize values the recordings only sent in another order.
	diffInputMaxComparisonValues = 1 << 20
	// diffInputMaxResponseScanBytes bounds both the response text joined for
	// origin searches and the bytes those searches read in total.
	diffInputMaxResponseScanBytes = int64(256 << 20)
	// maxDiffUnhandledInputs bounds the inputs a report lists.
	maxDiffUnhandledInputs = 50
	// maxDiffInputEntries bounds the baseline entries one input names.
	maxDiffInputEntries = 10
)

// DiffRequestInput is a request field whose value changed between the two
// recordings. Values stay private to the import: the report carries the field,
// how often it changed and where, and what it most likely is.
type DiffRequestInput struct {
	// Field names the request surface: query:<name>, header:<name>,
	// form:<name> or body, followed by a JSON path inside a JSON value. Array
	// positions read [] and identifier-shaped object keys read *.
	Field string `json:"field"`
	Kind  string `json:"kind"`
	// Requests counts the baseline requests that send a changed value.
	Requests int `json:"requests"`
	// DistinctValues counts the different baseline values among them.
	DistinctValues  int    `json:"distinct_values"`
	BaselineEntries []int  `json:"baseline_entries,omitempty"`
	Reason          string `json:"reason"`

	values   map[string]*diffInputValue
	requests map[int]bool
}

// diffInputValue is one changed baseline value: the requests that send it
// and what it most likely is.
type diffInputValue struct {
	requests []int
	kind     string
}

// ChangedRequestInputs reports whether the pair has any request field whose
// value changed between the recordings.
func (r *DiffReport) ChangedRequestInputs() bool {
	return r != nil && len(r.requestInputs) > 0
}

// diffInputActionable reports a kind worth a warning: every kind but a client
// counter, which replays harmlessly in most applications.
func diffInputActionable(kind string) bool {
	return kind != DiffInputCounter
}

// ResolveUnhandledInputs lists, as UnhandledInputs, the request inputs that
// changed between the recordings and whose baseline value the generated script
// still sends as recorded (sent reports that). Those requests replay a value
// from another session.
func (r *DiffReport) ResolveUnhandledInputs(sent func(value string) bool) {
	if r == nil || sent == nil {
		return
	}
	r.UnhandledInputs = nil
	r.UnhandledInputsTruncated = false
	for _, input := range r.requestInputs {
		kept := map[string]*diffInputValue{}
		for value, recorded := range input.values {
			if sent(value) {
				kept[value] = recorded
			}
		}
		if len(kept) == 0 {
			continue
		}
		r.UnhandledInputs = append(r.UnhandledInputs, diffInputSummary(input.Field, kept))
	}
	// Inputs that need attention come first, so counters never push them
	// past the list limit.
	sort.SliceStable(r.UnhandledInputs, func(i, j int) bool {
		if left, right := diffInputActionable(r.UnhandledInputs[i].Kind), diffInputActionable(r.UnhandledInputs[j].Kind); left != right {
			return left
		}
		if r.UnhandledInputs[i].Requests != r.UnhandledInputs[j].Requests {
			return r.UnhandledInputs[i].Requests > r.UnhandledInputs[j].Requests
		}
		return r.UnhandledInputs[i].Field < r.UnhandledInputs[j].Field
	})
	if len(r.UnhandledInputs) > maxDiffUnhandledInputs {
		r.UnhandledInputs = r.UnhandledInputs[:maxDiffUnhandledInputs]
		r.UnhandledInputsTruncated = true
	}
}

// UnhandledRequestCount counts the distinct baseline requests that send an
// unhandled input include accepts (every listed input when include is nil).
func (r *DiffReport) UnhandledRequestCount(include func(DiffRequestInput) bool) int {
	if r == nil {
		return 0
	}
	requests := map[int]bool{}
	for _, input := range r.UnhandledInputs {
		if include != nil && !include(input) {
			continue
		}
		for id := range input.requests {
			requests[id] = true
		}
	}
	return len(requests)
}

// ScriptValues is what a script's requests send, read with the same field
// model as the pair diff, so a value the script has templated (or replaced)
// is absent and one it still sends as recorded is present.
type ScriptValues struct {
	fields map[string]bool
	// opaque indexes the request text whose fields could not be read even
	// with templates neutralized (a body or a JSON form value); a value it
	// holds counts as sent.
	opaque diffTokenIndex
	// opaqueText joins that text for values the token index cannot answer,
	// searched within remaining bytes; past them a value counts as sent.
	opaqueText string
	remaining  *int64
	// answers caches what Sends and Prepare found, so a value is searched
	// for once.
	answers map[string]bool
}

// RequestInputValues lists the distinct baseline values of the request
// inputs that changed, for ScriptValues.Prepare.
func (r *DiffReport) RequestInputValues() []string {
	if r == nil {
		return nil
	}
	seen := map[string]bool{}
	var values []string
	for _, input := range r.requestInputs {
		for value := range input.values {
			if !seen[value] {
				seen[value] = true
				values = append(values, value)
			}
		}
	}
	sort.Strings(values)
	return values
}

// diffPreparePrefixBytes is the prefix Prepare matches candidates on; a
// shorter value is left to Sends.
const diffPreparePrefixBytes = 4

// Prepare answers Sends for values in one pass over the opaque text, so the
// values the token index cannot answer share a single search instead of
// spending it once each.
func (v ScriptValues) Prepare(values []string) {
	if v.opaqueText == "" || v.answers == nil {
		return
	}
	byPrefix := map[string][]string{}
	owners := map[string][]string{}
	var pending []string
	for _, value := range values {
		if _, done := v.answers[value]; done {
			continue
		}
		if v.fields[value] {
			v.answers[value] = true
			continue
		}
		if found, _ := v.opaque.lookup(value); found {
			v.answers[value] = true
			continue
		}
		forms := diffSearchForms(value)
		short := false
		for _, form := range forms {
			if len(form) < diffPreparePrefixBytes {
				short = true
			}
		}
		if short {
			continue
		}
		for _, form := range forms {
			if _, seen := owners[form]; !seen {
				byPrefix[form[:diffPreparePrefixBytes]] = append(byPrefix[form[:diffPreparePrefixBytes]], form)
			}
			owners[form] = append(owners[form], value)
		}
		pending = append(pending, value)
	}
	if len(pending) == 0 {
		return
	}
	*v.remaining -= int64(len(v.opaqueText))
	if *v.remaining < 0 {
		// Past the budget every value counts as sent (Sends answers yes).
		return
	}
	text := v.opaqueText
	for at := 0; at+diffPreparePrefixBytes <= len(text); at++ {
		candidates := byPrefix[text[at:at+diffPreparePrefixBytes]]
		for _, form := range candidates {
			if strings.HasPrefix(text[at:], form) {
				for _, value := range owners[form] {
					v.answers[value] = true
				}
			}
		}
	}
	for _, value := range pending {
		if !v.answers[value] {
			v.answers[value] = false
		}
	}
}

// diffSearchForms is value as the opaque text may hold it: as is, and query
// escaped when that differs.
func diffSearchForms(value string) []string {
	if escaped := url.QueryEscape(value); escaped != value {
		return []string{value, escaped}
	}
	return []string{value}
}

// Sends reports whether the script still sends value as recorded. Where it
// cannot tell (the search budget is spent), it answers yes, so a value is
// reported rather than taken for handled.
func (v ScriptValues) Sends(value string) bool {
	if v.fields[value] {
		return true
	}
	if v.opaqueText == "" {
		return false
	}
	if answer, done := v.answers[value]; done {
		return answer
	}
	// The index answers only that a value is there: a short value, or one
	// inside a longer token, is not indexed on its own, so a miss falls
	// through to the search.
	if found, _ := v.opaque.lookup(value); found {
		return true
	}
	*v.remaining -= int64(len(v.opaqueText))
	if *v.remaining < 0 {
		return true
	}
	answer := false
	for _, form := range diffSearchForms(value) {
		if strings.Contains(v.opaqueText, form) {
			answer = true
			break
		}
	}
	if v.answers != nil {
		v.answers[value] = answer
	}
	return answer
}

// ScriptRequestValues reads what the script's requests send. JSON the
// import's own rewrites made invalid (a number replaced by an unquoted
// template) is read with its templates neutralized, in a body or a form or
// query value; text that still cannot be read is indexed whole, so a value it
// holds is never taken for handled.
func ScriptRequestValues(script *dsl.Script) ScriptValues {
	remaining := diffInputMaxResponseScanBytes
	values := ScriptValues{fields: map[string]bool{}, opaque: newDiffTokenIndex(), remaining: &remaining, answers: map[string]bool{}}
	if script == nil {
		return values
	}
	var opaque strings.Builder
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		action := ref.Action
		if action == nil && ref.Request != nil {
			// A request group's request in its JSON-shaped form, as a script
			// holds it after a JSON round trip.
			decoded, ok := decodeGroupRequest(ref.Request)
			if !ok {
				return nil
			}
			action = &decoded
		}
		if action == nil || strings.TrimSpace(action.Target) == "" {
			return nil
		}
		headers := make(map[string]string, len(action.Headers)+len(action.HeaderList))
		for name, value := range action.Headers {
			headers[name] = value
		}
		for _, header := range action.HeaderList {
			if header.Enabled == nil || *header.Enabled {
				headers[header.Name] = header.Value
			}
		}
		requestFieldValues(action.Target, headers, action.Body, func(_, value string) {
			values.fields[value] = true
		}, func(text string) {
			if int64(opaque.Len()+len(text)) > diffInputMaxResponseScanBytes {
				return
			}
			values.opaque.add(text, 0)
			opaque.WriteString(text)
			opaque.WriteByte(0)
		})
		return nil
	})
	values.opaqueText = opaque.String()
	return values
}

// diffNeutralizeJSONTemplates replaces each {{...}} template outside a JSON
// string with 0, so a body whose number a template replaced decodes again; a
// template inside a string is part of that string and stays.
func diffNeutralizeJSONTemplates(body string) string {
	var out strings.Builder
	out.Grow(len(body))
	inString, escaped := false, false
	for index := 0; index < len(body); index++ {
		c := body[index]
		if inString {
			out.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out.WriteByte(c)
			continue
		}
		if c == '{' && index+1 < len(body) && body[index+1] == '{' {
			if end := strings.Index(body[index:], "}}"); end >= 0 {
				out.WriteByte('0')
				index += end + 1
				continue
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

// decodeGroupRequest decodes a JSON-shaped request group request; false when
// it does not decode, and the walk passes over it.
func decodeGroupRequest(request map[string]any) (dsl.Action, bool) {
	var decoded dsl.Action
	raw, err := json.Marshal(request)
	if err != nil {
		return decoded, false
	}
	return decoded, json.Unmarshal(raw, &decoded) == nil
}

// diffRequestInputs pairs baseline and comparison requests (by method, host,
// path and query-key set, in order) and records every field whose value
// changed between them. truncated reports that it stopped at its limit of
// distinct changed values, leaving later requests uncompared. It stops with
// ctx, checked before each request.
func diffRequestInputs(ctx context.Context, baseline, comparison []sample.HARSourceObservation) (inputs []DiffRequestInput, truncated bool, err error) {
	if len(baseline) == 0 || len(comparison) == 0 {
		return nil, false, nil
	}
	// A baseline value the comparison sends in the same field somewhere is
	// stable across sessions; it differs only because the two recordings
	// issued the same requests in another order. Kept as hashes.
	comparisonValues := map[string]map[uint64]bool{}
	stored := 0
	byKey := make(map[string][]int, len(comparison))
	for index, observation := range comparison {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		key := diffRequestPairKey(observation)
		byKey[key] = append(byKey[key], index)
		requestFieldValues(observation.RequestURL, observation.RequestHeaders, observation.RequestBody, func(field, value string) {
			if stored >= diffInputMaxComparisonValues {
				return
			}
			if comparisonValues[field] == nil {
				comparisonValues[field] = map[uint64]bool{}
			}
			comparisonValues[field][diffValueHash(value)] = true
			stored++
		}, nil)
	}
	responses := &diffResponseScan{observations: baseline}
	changed := map[string]map[string]*diffInputValue{}
	distinct := 0
	for index, observation := range baseline {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if truncated {
			break
		}
		key := diffRequestPairKey(observation)
		queue := byKey[key]
		if len(queue) == 0 {
			continue
		}
		counterpart := comparison[queue[0]]
		byKey[key] = queue[1:]
		comparisonFields := map[string][]string{}
		requestFieldValues(counterpart.RequestURL, counterpart.RequestHeaders, counterpart.RequestBody, func(field, value string) {
			comparisonFields[field] = append(comparisonFields[field], value)
		}, nil)
		seen := map[string]int{}
		requestFieldValues(observation.RequestURL, observation.RequestHeaders, observation.RequestBody, func(field, value string) {
			ordinal := seen[field]
			seen[field] = ordinal + 1
			others := comparisonFields[field]
			if ordinal >= len(others) {
				return
			}
			other := others[ordinal]
			if value == other || len(value) < diffInputMinValueBytes || comparisonValues[field][diffValueHash(value)] {
				return
			}
			values := changed[field]
			if values == nil {
				values = map[string]*diffInputValue{}
				changed[field] = values
			}
			recorded := values[value]
			if recorded == nil {
				// Only a new value counts against the limit: repeats of one
				// already recorded cost no more than their request id.
				if distinct >= diffInputMaxChanges {
					truncated = true
					return
				}
				distinct++
				recorded = &diffInputValue{kind: diffInputKind(value, other, responses, index)}
				values[value] = recorded
			}
			if last := len(recorded.requests) - 1; last < 0 || recorded.requests[last] != observation.ID {
				recorded.requests = append(recorded.requests, observation.ID)
			}
		}, nil)
	}
	inputs = make([]DiffRequestInput, 0, len(changed))
	for field, values := range changed {
		input := diffInputSummary(field, values)
		input.values = values
		inputs = append(inputs, input)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Field < inputs[j].Field })
	return inputs, truncated, nil
}

// diffInputSummary describes field from its changed values: the distinct
// requests and values, the first baseline entries in order, and the kind
// most of its requests have.
func diffInputSummary(field string, values map[string]*diffInputValue) DiffRequestInput {
	input := DiffRequestInput{Field: field, DistinctValues: len(values), requests: map[int]bool{}}
	kinds := map[string]int{}
	for _, recorded := range values {
		kinds[recorded.kind] += len(recorded.requests)
		for _, id := range recorded.requests {
			input.requests[id] = true
		}
	}
	input.Requests = len(input.requests)
	entries := make([]int, 0, len(input.requests))
	for id := range input.requests {
		entries = append(entries, id)
	}
	slices.Sort(entries)
	if len(entries) > maxDiffInputEntries {
		entries = entries[:maxDiffInputEntries]
	}
	input.BaselineEntries = entries
	input.Kind = diffInputMainKind(kinds)
	input.Reason = diffInputReason(input.Kind)
	return input
}

func diffValueHash(value string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum64()
}

// diffRequestPairKey groups requests the way diffAligner groups producers:
// method, host, path, then the query-key set, which tells Aura batches apart
// by the action names in their query.
func diffRequestPairKey(observation sample.HARSourceObservation) string {
	return diffRequestKey(observation) + "?" + diffQueryKeySet(observation.RequestURL)
}

// diffIgnoredHeaders are request headers that describe the browser or the
// transport rather than the application's state, or that replay regenerates.
var diffIgnoredHeaders = map[string]bool{
	"accept": true, "accept-encoding": true, "accept-language": true, "cache-control": true,
	"connection": true, "content-length": true, "cookie": true, "dnt": true, "host": true,
	"if-modified-since": true, "if-none-match": true, "origin": true, "pragma": true,
	"priority": true, "te": true, "upgrade-insecure-requests": true, "user-agent": true,
}

// requestFieldValues visits each field value a request sends: query
// parameters, application headers, and form or JSON body fields, descending
// into JSON values. Field names carry occurrence order through visit order.
func requestFieldValues(rawURL string, headers map[string]string, body string, visit func(field, value string), opaque func(text string)) {
	budget := diffInputMaxFieldsPerRequest
	emit := func(field, value string) {
		if budget <= 0 {
			return
		}
		budget--
		if len(field) > diffInputMaxFieldBytes {
			field = strings.ToValidUTF8(field[:diffInputMaxFieldBytes], "")
		}
		visit(field, value)
	}
	if _, query, found := strings.Cut(rawURL, "?"); found {
		query, _, _ = strings.Cut(query, "#")
		diffFormFields("query:", query, emit, opaque)
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "" || strings.HasPrefix(lower, ":") || strings.HasPrefix(lower, "sec-") || diffIgnoredHeaders[lower] {
			continue
		}
		value := headers[name]
		if lower == "content-type" {
			// A multipart boundary changes on every request and replay makes its own.
			value, _, _ = strings.Cut(value, ";")
		}
		emit("header:"+lower, strings.TrimSpace(value))
	}
	if body == "" || len(body) > diffInputMaxBodyBytes {
		return
	}
	if decoded, ok := diffDecodeTemplatedJSON(body); ok {
		diffJSONFields("body", decoded, 0, emit)
		return
	}
	if diffJSONShaped(body) || strings.ContainsAny(body, "\r\n") || !strings.Contains(body, "=") {
		// JSON that does not decode, or a body that is no form: its fields
		// cannot be read. A script's templated body is still text it sends.
		if opaque != nil && strings.Contains(body, "{{") {
			opaque(body)
		}
		return
	}
	diffFormFields("form:", body, emit, opaque)
}

// diffJSONShaped reports text that starts like a JSON object or array.
func diffJSONShaped(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[')
}

// diffDecodeTemplatedJSON decodes JSON text, retrying with its templates
// neutralized when a template made it invalid (diffNeutralizeJSONTemplates).
func diffDecodeTemplatedJSON(text string) (any, bool) {
	if decoded, ok := diffDecodeJSON(text); ok {
		return decoded, true
	}
	if !strings.Contains(text, "{{") || !diffJSONShaped(text) {
		return nil, false
	}
	return diffDecodeJSON(diffNeutralizeJSONTemplates(text))
}

func diffFormFields(prefix, encoded string, emit func(field, value string), opaque func(text string)) {
	for _, pair := range strings.Split(encoded, "&") {
		if pair == "" {
			continue
		}
		rawName, rawValue, _ := strings.Cut(pair, "=")
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			continue
		}
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			continue
		}
		field := prefix + name
		if decoded, ok := diffDecodeTemplatedJSON(value); ok {
			diffJSONFields(field, decoded, 0, emit)
			continue
		}
		if opaque != nil && diffJSONShaped(value) && strings.Contains(value, "{{") {
			opaque(value)
		}
		emit(field, value)
	}
}

// diffDecodeJSON decodes text that is a JSON object or array, keeping numbers
// as written so long identifiers keep every digit.
func diffDecodeJSON(text string) (any, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return nil, false
	}
	return decoded, true
}

// diffIdentifierKey matches the shapes of object keys that are themselves
// values (record ids, UUIDs, hex or numeric ids); diffIdentifierSegment also
// requires the digits an identifier carries, so field names such as
// addressLine1Txt stay as they are.
var diffIdentifierKey = regexp.MustCompile(`^(?:[A-Za-z0-9]{15}|[A-Za-z0-9]{18}|[0-9a-fA-F-]{16,}|[0-9]{6,})$`)

// diffIdentifierSegment reports an object key that is a value, not a name: an
// identifier shape with at least five digits.
func diffIdentifierSegment(key string) bool {
	if !diffIdentifierKey.MatchString(key) {
		return false
	}
	digits := 0
	for index := 0; index < len(key); index++ {
		if key[index] >= '0' && key[index] <= '9' {
			digits++
		}
	}
	return digits >= 5
}

// diffJSONFields visits the scalar leaves of a JSON value. Array positions are
// folded into [] so a reordered or resized array keeps its field names, and
// identifier-shaped keys into *; the visit order still pairs the n-th leaf of
// each.
func diffJSONFields(path string, value any, depth int, emit func(field, value string)) {
	if depth > diffInputMaxJSONDepth {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			segment := key
			if diffIdentifierSegment(key) {
				segment = "*"
			}
			diffJSONFields(path+"."+segment, typed[key], depth+1, emit)
		}
	case []any:
		for _, item := range typed {
			diffJSONFields(path+"[]", item, depth+1, emit)
		}
	case string:
		emit(path, typed)
	case json.Number:
		emit(path, typed.String())
	}
}

var (
	diffDigitRun     = regexp.MustCompile(`[0-9]+`)
	diffGeneratedID  = regexp.MustCompile(`^(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{16,64})$`)
	diffEpochSeconds = regexp.MustCompile(`^1[0-9]{9}$`)
	diffEpochMillis  = regexp.MustCompile(`^1[0-9]{12}$`)
)

// diffInputKind tells what a value that changed from baseline to other most
// likely is: a client timestamp, a value an earlier baseline response
// returned, a counter, a client-generated identifier, one with no captured
// origin, or one whose origin was not searched for.
func diffInputKind(baseline, other string, responses *diffResponseScan, index int) string {
	if (diffEpochSeconds.MatchString(baseline) && diffEpochSeconds.MatchString(other)) ||
		(diffEpochMillis.MatchString(baseline) && diffEpochMillis.MatchString(other)) {
		return DiffInputTimestamp
	}
	// A value an earlier response returns is a server value even when it looks
	// like a counter (ORD-104233 and ORD-104871).
	found, checked := responses.before(index, baseline)
	switch {
	case found:
		return DiffInputServerValue
	case diffSameShapeSmallNumbers(baseline, other):
		return DiffInputCounter
	case len(baseline) == len(other) && diffGeneratedID.MatchString(baseline) && diffGeneratedID.MatchString(other):
		return DiffInputGeneratedID
	case !checked:
		return DiffInputNotChecked
	default:
		return DiffInputUnknown
	}
}

// diffSameShapeSmallNumbers reports values that differ only in short
// numbers, such as Aura action ids ("12;a" and "15;a"): at most four digits,
// or up to seven in a value too short to search for (a long session's
// "12345;a").
func diffSameShapeSmallNumbers(baseline, other string) bool {
	if diffDigitRun.ReplaceAllString(baseline, "0") != diffDigitRun.ReplaceAllString(other, "0") {
		return false
	}
	maxRun := 4
	if len(baseline) < diffInputMinSearchBytes && len(other) < diffInputMinSearchBytes {
		maxRun = 7
	}
	for _, run := range append(diffDigitRun.FindAllString(baseline, -1), diffDigitRun.FindAllString(other, -1)...) {
		if len(run) > maxRun {
			return false
		}
	}
	return diffDigitRun.MatchString(baseline)
}

func diffInputMainKind(kinds map[string]int) string {
	best, bestCount := DiffInputUnknown, 0
	for _, kind := range []string{DiffInputServerValue, DiffInputTimestamp, DiffInputCounter, DiffInputGeneratedID, DiffInputUnknown, DiffInputNotChecked} {
		if kinds[kind] > bestCount {
			best, bestCount = kind, kinds[kind]
		}
	}
	return best
}

func diffInputReason(kind string) string {
	switch kind {
	case DiffInputTimestamp:
		return "a client timestamp: replay should send the current time"
	case DiffInputCounter:
		return "a client counter that restarts in each session; replaying the recorded one is usually harmless"
	case DiffInputGeneratedID:
		return "an identifier the client generates: replay should generate a fresh one"
	case DiffInputServerValue:
		return "an earlier response returns it, but no correlation extracts it: add one"
	case DiffInputNotChecked:
		return "the capture's responses were not searched for it (it is short, or the search budget ran out): find its source before replay"
	default:
		return "no earlier response in the capture returns it (a cookie, script or the browser may set it): find its source before replay"
	}
}

// diffResponseScan answers whether a value appears in a baseline response
// before a given request. The application responses (static assets are left
// out) are read on first use, up to diffInputMaxResponseScanBytes in all; a
// response that does not fit is skipped and remembered. Each response's
// tokens (runs between quotes, spaces and URL or markup delimiters) are
// indexed by the first response that holds them, so a value made of token
// characters is answered by lookup; any other value is searched for once in
// the joined text, within the same byte budget.
type diffResponseScan struct {
	observations []sample.HARSourceObservation
	built        bool
	joined       string
	joinedBuilt  bool
	joinedSize   int
	ends         []int // ends[i] is the end offset of response i in joined
	// included marks the responses within the budget.
	included []bool
	// firstSkipped is the first response left out to fit the budget, or
	// len(observations) when none was.
	firstSkipped int
	// tokens maps each indexed token to the first response holding it.
	tokens    diffTokenIndex
	first     map[string]diffResponseHit
	remaining int64
}

const (
	// diffInputMaxResponseTokens bounds the response tokens indexed.
	diffInputMaxResponseTokens = 1 << 21
	// diffInputMaxTokenBytes bounds an indexed token; a longer value is
	// searched for instead.
	diffInputMaxTokenBytes = 512
)

type diffResponseHit struct {
	index    int  // first response holding the value, -1 when none
	searched bool // false when the search budget ran out first
}

// before reports whether value appears in a response earlier than request
// index, and whether that could be told: a skipped response before index may
// hold the value, and a search may run out of budget.
func (s *diffResponseScan) before(index int, value string) (found, checked bool) {
	if len(value) < diffInputMinSearchBytes {
		return false, false
	}
	s.build()
	hit, cached := s.first[value]
	if !cached {
		hit = s.firstResponse(value)
		s.first[value] = hit
	}
	if hit.index >= 0 && hit.index < index {
		return true, true
	}
	if !hit.searched {
		return false, false
	}
	return false, s.firstSkipped >= index
}

func (s *diffResponseScan) build() {
	if s.built {
		return
	}
	s.built = true
	s.first = map[string]diffResponseHit{}
	s.remaining = diffInputMaxResponseScanBytes
	s.firstSkipped = len(s.observations)
	s.tokens = newDiffTokenIndex()
	s.included = make([]bool, len(s.observations))
	var total int64
	for index, observation := range s.observations {
		if diffStaticResponse(observation.ResponseHeaders) {
			continue
		}
		size := int64(diffResponseSize(observation))
		if total+size > diffInputMaxResponseScanBytes {
			s.firstSkipped = min(s.firstSkipped, index)
			continue
		}
		total += size
		s.included[index] = true
		// Tokens are cut from the observation's own strings.
		s.tokens.add(observation.ResponseBody, index)
		for _, value := range observation.ResponseHeaders {
			s.tokens.add(value, index)
		}
	}
	s.joinedSize = int(total)
}

// diffResponseSize is the bytes a response takes in the joined search text.
func diffResponseSize(observation sample.HARSourceObservation) int {
	size := len(observation.ResponseBody) + 1
	for name, value := range observation.ResponseHeaders {
		size += len(name) + len(value) + 3
	}
	return size
}

// joinResponses builds the joined search text on the first search that needs
// it, in one allocation.
func (s *diffResponseScan) joinResponses() {
	if s.joinedBuilt {
		return
	}
	s.joinedBuilt = true
	var builder strings.Builder
	builder.Grow(s.joinedSize)
	s.ends = make([]int, len(s.observations))
	for index, observation := range s.observations {
		if s.included[index] {
			builder.WriteString(observation.ResponseBody)
			for name, value := range observation.ResponseHeaders {
				builder.WriteString("\n")
				builder.WriteString(name)
				builder.WriteString(": ")
				builder.WriteString(value)
			}
			builder.WriteString("\x00")
		}
		s.ends[index] = builder.Len()
	}
	s.joined = builder.String()
}

// diffTokenDelimiter reports a byte that ends a response token.
func diffTokenDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\x00', '"', '\'', '<', '>', '{', '}', '[', ']', '(', ')', ',', '=', '&', '?', '#', ':', ';', '\\':
		return true
	}
	return false
}

// diffTokenIndex maps the tokens of indexed text to the first id that holds
// them. A token is text between quotes, spaces and URL or markup delimiters;
// its percent-decoded form, and the pieces of both cut at path and dotted-name
// separators, are indexed too, so a value found inside a path, an encoded URL
// or a long href is answered by lookup.
type diffTokenIndex struct {
	tokens   map[string]int
	complete bool
}

func newDiffTokenIndex() diffTokenIndex {
	return diffTokenIndex{tokens: map[string]int{}, complete: true}
}

// diffTokenMaxDecodeDepth bounds how many layers of percent-encoding the
// index decodes.
const diffTokenMaxDecodeDepth = 3

// add indexes text under id; false once the token limit stops the index.
func (x *diffTokenIndex) add(text string, id int) bool {
	return x.addDecoded(text, id, 0)
}

// addDecoded indexes text that depth layers of percent-decoding produced.
func (x *diffTokenIndex) addDecoded(text string, id, depth int) bool {
	for start := 0; start < len(text); {
		for start < len(text) && diffTokenDelimiter(text[start]) {
			start++
		}
		end := start
		for end < len(text) && !diffTokenDelimiter(text[end]) {
			end++
		}
		if end-start >= diffInputMinSearchBytes {
			token := text[start:end]
			if !x.addToken(token, id) {
				return false
			}
			if strings.Contains(token, "%") && depth < diffTokenMaxDecodeDepth {
				// Percent-encoding hides delimiters (%22, %3F, %3D, %26), so the
				// decoded text is tokenized again, down to a bounded depth for
				// text encoded more than once.
				if decoded, err := url.PathUnescape(token); err == nil && decoded != token && !x.addDecoded(decoded, id, depth+1) {
					return false
				}
			}
		}
		start = end
	}
	return true
}

// addToken indexes a token (when short enough) and its pieces.
func (x *diffTokenIndex) addToken(token string, id int) bool {
	if len(token) <= diffInputMaxTokenBytes && !x.put(token, id) {
		return false
	}
	for pieceStart := 0; pieceStart < len(token); {
		pieceEnd := pieceStart
		for pieceEnd < len(token) && !diffTokenSeparator(token[pieceEnd]) {
			pieceEnd++
		}
		if length := pieceEnd - pieceStart; length >= diffInputMinSearchBytes && length <= diffInputMaxTokenBytes && length < len(token) {
			if !x.put(token[pieceStart:pieceEnd], id) {
				return false
			}
		}
		pieceStart = pieceEnd + 1
	}
	return true
}

func (x *diffTokenIndex) put(token string, id int) bool {
	if x.tokens == nil {
		*x = newDiffTokenIndex()
	}
	if _, seen := x.tokens[token]; seen {
		return true
	}
	if len(x.tokens) >= diffInputMaxResponseTokens {
		x.complete = false
		return false
	}
	x.tokens[token] = id
	return true
}

// lookup reports whether a value is indexed; answered is false when the index
// cannot tell (a value with delimiters, or an index the limit stopped).
func (x *diffTokenIndex) lookup(value string) (found, answered bool) {
	_, found, answered = x.first(value)
	return found, answered
}

// first is the first id holding value, under the same rules as lookup.
func (x *diffTokenIndex) first(value string) (id int, found, answered bool) {
	if !x.complete || !diffTokenValue(value) {
		return 0, false, false
	}
	id, found = x.tokens[value]
	return id, found, true
}

// diffTokenSeparator reports a byte that separates the pieces of a response
// token (a path, a dotted name, an encoded or joined value).
func diffTokenSeparator(c byte) bool {
	switch c {
	case '/', '.', '%', '+', '|', '~', '!', '*', '@', '$':
		return true
	}
	return false
}

// diffTokenValue reports a value the token index can answer: no delimiter
// bytes, and short enough to have been indexed.
func diffTokenValue(value string) bool {
	if len(value) > diffInputMaxTokenBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		if diffTokenDelimiter(value[index]) {
			return false
		}
	}
	return true
}

func (s *diffResponseScan) firstResponse(value string) diffResponseHit {
	forms := []string{value}
	if escaped := strings.Trim(strconv.Quote(value), `"`); escaped != value {
		forms = append(forms, escaped)
	}
	if encoded := url.QueryEscape(value); encoded != value {
		forms = append(forms, encoded)
	}
	best := -1
	searched := true
	for _, form := range forms {
		if at, found, answered := s.tokens.first(form); answered {
			if found && (best < 0 || at < best) {
				best = at
			}
			continue
		}
		s.joinResponses()
		s.remaining -= int64(len(s.joined))
		if s.remaining < 0 {
			searched = false
			continue
		}
		if offset := strings.Index(s.joined, form); offset >= 0 {
			at := sort.Search(len(s.ends), func(i int) bool { return s.ends[i] > offset })
			if best < 0 || at < best {
				best = at
			}
		}
	}
	return diffResponseHit{index: best, searched: searched || best >= 0}
}

// diffStaticResponse reports a script, style, image, font or media response:
// application state comes from documents and API responses, not assets.
func diffStaticResponse(headers map[string]string) bool {
	for name, value := range headers {
		if !strings.EqualFold(name, "content-type") {
			continue
		}
		lower := strings.ToLower(value)
		for _, static := range []string{"javascript", "ecmascript", "text/css", "image/", "font/", "video/", "audio/", "application/wasm"} {
			if strings.Contains(lower, static) {
				return true
			}
		}
	}
	return false
}
