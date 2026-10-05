// Package clientids finds UUIDs a recorded client made up itself and gives
// each replayed iteration fresh values instead of the recorded literals.
//
// A UUID in a request that no recorded response contains (not in any body,
// header, cookie, redirect or decoded JWT, static assets included) was created
// by the client, so there is nothing to correlate from. Replaying the recorded
// literal makes every virtual user present the same session, request or event.
// Not every such value is generated, though: tenant, device and install ids are
// client-side constants that the server may check. Only values with evidence
// of generation are parameterized:
//
//   - a key that carries three or more values during the capture, none of
//     them also used under another key (log, event and request ids), gets one
//     uuid parameter that is fresh at every reference, unless its name marks a
//     value kept for a whole session or flow (session, conversation,
//     transaction, correlation, trace, span);
//   - otherwise a key whose name marks a per-session or per-request value
//     (session, request, trace, correlation, transaction, nonce, event, ...),
//     or that carries more than one value, gets one uuid parameter per
//     recorded value, fresh each iteration, or once per virtual user when the
//     init or teardown phase uses it (init runs once, so the value it
//     established must not change under later iterations).
//
// Identity and configuration names, single values with neutral names, and
// path segments without a key are left as recorded and reported.
package clientids

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// MaxPerIterationParameters bounds how many per-value parameters one import
// adds; further values are left as recorded and reported.
const MaxPerIterationParameters = 32

var (
	// uuidPattern has no boundaries of its own: consuming the separator would
	// hide the second of two adjacent UUIDs ("a,b"). uuidSpans checks them.
	uuidPattern = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)
	jwtPattern  = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}\.(eyJ[A-Za-z0-9_-]+)\.`)
	keyPattern  = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]{0,63})\\*"?\s*[:=]\s*\\*"?$`)
)

// generatedWords mark a value created per session, request or event.
var generatedWords = map[string]bool{
	"session": true, "request": true, "trace": true, "span": true, "correlation": true,
	"transaction": true, "conversation": true, "nonce": true, "event": true,
	"idempotency": true, "interaction": true, "activity": true, "message": true,
}

// flowWords mark a value kept across the requests of one session or flow, so
// each recorded value keeps its own parameter even when a key rotates.
var flowWords = map[string]bool{
	"session": true, "conversation": true, "transaction": true, "correlation": true, "trace": true, "span": true,
}

// identityWords mark a client-side constant the server may recognize.
var identityWords = map[string]bool{
	"tenant": true, "subscription": true, "install": true, "installation": true, "device": true,
	"app": true, "application": true, "client": true, "site": true, "org": true, "organization": true,
	"workspace": true, "project": true, "environment": true, "env": true, "manifest": true,
	"product": true, "user": true, "account": true,
}

// ServerUUIDs collects the normalized UUIDs a HAR's responses contain: every
// entry's body (static assets included, base64 decoded), headers, cookies,
// redirect URL and the payloads of JWTs among them. It reports false when too
// few textual responses kept their bodies to judge what the server sent.
func ServerUUIDs(har []byte) (map[string]bool, bool, error) {
	var parsed struct {
		Log struct {
			Entries []struct {
				Response struct {
					Status  int `json:"status"`
					Content struct {
						Text     *string `json:"text"`
						Encoding string  `json:"encoding"`
						MimeType string  `json:"mimeType"`
					} `json:"content"`
					Headers     []struct{ Name, Value string } `json:"headers"`
					Cookies     []struct{ Name, Value string } `json:"cookies"`
					RedirectURL string                         `json:"redirectURL"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(har, []byte("\uFEFF")), &parsed); err != nil {
		return nil, false, err
	}
	found := map[string]bool{}
	textual, captured := 0, 0
	for _, entry := range parsed.Log.Entries {
		response := entry.Response
		body := ""
		if response.Content.Text != nil {
			body = *response.Content.Text
			if strings.EqualFold(response.Content.Encoding, "base64") {
				if decoded, err := base64.StdEncoding.DecodeString(body); err == nil {
					body = string(decoded)
				}
			}
		}
		if response.Status >= 200 && response.Status < 300 && response.Status != 204 && textualMIME(response.Content.MimeType) {
			textual++
			if body != "" {
				captured++
			}
		}
		collectUUIDs(found, body)
		collectUUIDs(found, response.RedirectURL)
		for _, header := range response.Headers {
			collectUUIDs(found, header.Value)
		}
		for _, cookie := range response.Cookies {
			collectUUIDs(found, cookie.Value)
		}
	}
	return found, textual > 0 && captured*2 >= textual, nil
}

func textualMIME(mime string) bool {
	mime = strings.ToLower(mime)
	for _, kind := range []string{"json", "javascript", "html", "xml", "text/"} {
		if strings.Contains(mime, kind) {
			return true
		}
	}
	return false
}

// collectUUIDs records the UUIDs in text and in the payloads of its JWTs.
func collectUUIDs(found map[string]bool, text string) {
	if text == "" {
		return
	}
	for _, span := range uuidSpans(text) {
		found[strings.ToLower(text[span[0]:span[1]])] = true
	}
	for _, match := range jwtPattern.FindAllStringSubmatch(text, -1) {
		if payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(match[1], "=")); err == nil {
			for _, span := range uuidSpans(string(payload)) {
				found[strings.ToLower(string(payload[span[0]:span[1]]))] = true
			}
		}
	}
}

// uuidSpans returns the byte ranges of the UUIDs in text that are not part of
// a longer run of hex digits.
func uuidSpans(text string) [][2]int {
	var spans [][2]int
	for _, match := range uuidPattern.FindAllStringIndex(text, -1) {
		if match[0] > 0 && isHex(text[match[0]-1]) || match[1] < len(text) && isHex(text[match[1]]) {
			continue
		}
		spans = append(spans, [2]int{match[0], match[1]})
	}
	return spans
}

// Parameter is one uuid parameter added to the script.
type Parameter struct {
	Name        string
	Update      string // once, each_iteration or each_occurrence
	Key         string // the request key whose values it replaces
	Values      int    // recorded values it replaces
	Occurrences int
}

// Report describes what Apply changed.
type Report struct {
	Parameters []Parameter
	// LeftAsRecorded counts client-made UUIDs kept as recorded: identity or
	// configuration names, single values with neutral names, keyless path
	// segments, mixed case, or values also used where they cannot be rewritten
	// (credential-located or encoded fields, assertions, extractors, ...).
	LeftAsRecorded int
}

// Warning summarizes the report for the import review, or "" when nothing
// client-made was found.
func (r Report) Warning() string {
	if len(r.Parameters) == 0 && r.LeftAsRecorded == 0 {
		return ""
	}
	var perIteration, perUser, perUse []string
	values := 0
	for _, parameter := range r.Parameters {
		values += parameter.Values
		switch parameter.Update {
		case "each_occurrence":
			perUse = append(perUse, parameter.Name)
		case "once":
			perUser = append(perUser, parameter.Name)
		default:
			perIteration = append(perIteration, parameter.Name)
		}
	}
	var parts []string
	if len(r.Parameters) > 0 {
		var groups []string
		if len(perIteration) > 0 {
			groups = append(groups, fmt.Sprintf("%s each iteration (set a parameter's update to once to keep one value per virtual user)", strings.Join(listed(perIteration), ", ")))
		}
		if len(perUser) > 0 {
			groups = append(groups, fmt.Sprintf("%s once per virtual user, because the init or teardown phase uses it", strings.Join(listed(perUser), ", ")))
		}
		if len(perUse) > 0 {
			groups = append(groups, fmt.Sprintf("%s at every reference, because its key took a new value for each record", strings.Join(listed(perUse), ", ")))
		}
		parts = append(parts, fmt.Sprintf("%d client-generated UUID value(s) that no recorded response contains now get fresh values from uuid parameters: %s.", values, strings.Join(groups, "; ")))
	}
	if r.LeftAsRecorded > 0 {
		other := ""
		if len(r.Parameters) > 0 {
			other = "other "
		}
		parts = append(parts, fmt.Sprintf("%d %sclient-made UUID value(s) that no recorded response contains were left as recorded because they look like fixed identifiers (tenant, device, install, ...), appear once without a generated-id name, or are also used where they cannot be rewritten (a credential field, an assertion, an extractor, ...); review them if each virtual user should send its own.", r.LeftAsRecorded, other))
	}
	return strings.Join(parts, " ")
}

func listed(names []string) []string {
	const shown = 8
	if len(names) <= shown {
		return names
	}
	return append(append([]string(nil), names[:shown]...), fmt.Sprintf("and %d more", len(names)-shown))
}

type occurrence struct {
	key    string
	header bool
	setup  bool // in the init or teardown phase
}

type candidate struct {
	literal     string
	normalized  string
	first       int
	occurrences []occurrence
	unsafe      bool
	upper       bool
	mixed       bool
}

// Apply replaces client-generated UUID literals in the script's request
// targets, bodies and headers with uuid parameters. serverUUIDs holds the
// normalized UUIDs the recorded responses contain (see ServerUUIDs).
func Apply(script *dsl.Script, serverUUIDs map[string]bool) Report {
	report := Report{}
	if script == nil {
		return report
	}
	candidates := map[string]*candidate{}
	order := 0
	setup := false
	note := func(literal, key string, header, unsafe bool) {
		normalized := strings.ToLower(literal)
		if serverUUIDs[normalized] {
			return // correlation territory, not client-made
		}
		c := candidates[normalized]
		if c == nil {
			c = &candidate{literal: literal, normalized: normalized, first: order, upper: literal != normalized}
			candidates[normalized] = c
			order++
		}
		if literal != c.literal {
			c.mixed = true
		}
		c.unsafe = c.unsafe || unsafe
		c.occurrences = append(c.occurrences, occurrence{key: key, header: header, setup: setup})
	}
	scan := func(text, headerName string, unsafe bool) {
		previousKey, previousEnd := "", -1
		for _, span := range uuidSpans(text) {
			literal := text[span[0]:span[1]]
			key := keyBefore(text[:span[0]])
			if separator := strings.TrimSpace(text[max(previousEnd, 0):span[0]]); key == "" && previousEnd >= 0 && (separator == "," || separator == "%2C" || separator == "%2c") {
				key = previousKey // a later member of a delimited list under one key
			}
			previousKey, previousEnd = key, span[1]
			if key == "" && headerName != "" && strings.TrimSpace(strings.Trim(text, `"`)) == literal {
				key = headerName
			}
			note(literal, key, headerName != "", unsafe)
		}
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		setup = ref.Phase == dslwalk.Init || ref.Phase == dslwalk.Teardown
		if ref.Action == nil {
			if raw, err := json.Marshal(ref.Request); err == nil {
				scan(string(raw), "", true)
			}
			return nil
		}
		action := ref.Action
		// Fields an import credential locator points at stay with the
		// credential workflow, as do encoded or contract-bound bodies.
		located := credentialLocatedFields(action)
		safeBody := action.BodyFile == "" && action.BodyEncoding == "" && !located.body &&
			action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] == nil &&
			action.Metadata[dsl.ImportAuraTemplateContractMetadataKey] == nil
		scan(action.Target, "", located.target)
		scan(action.Body, "", !safeBody)
		for name, value := range action.Headers {
			scan(value, name, located.allHeaders || located.headerNames[strings.ToLower(name)])
		}
		for index, header := range action.HeaderList {
			scan(header.Value, header.Name, located.allHeaders || located.headers[index])
		}
		// Anything else that executes keeps its literal, so a value also used
		// there must not change in the request either.
		rest := *action
		rest.Metadata, rest.Target, rest.Body, rest.Headers, rest.HeaderList = nil, "", "", nil, nil
		rest.Params = nil
		if raw, err := json.Marshal(rest); err == nil {
			scan(string(raw), "", true)
		}
		if raw, err := json.Marshal(action.Params); err == nil && dsl.CanonicalActionType(action.Type) != "http_sequence" {
			scan(string(raw), "", true)
		}
		return nil
	})
	setup = false
	if raw, err := json.Marshal(script.Config); err == nil {
		scan(string(raw), "", true)
	}
	if len(candidates) == 0 {
		return report
	}

	list := make([]*candidate, 0, len(candidates))
	for _, c := range candidates {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].first < list[j].first })

	// Per key: its distinct values and whether every one was used once.
	byKey := map[string][]*candidate{}
	for _, c := range list {
		key := c.occurrences[0].key
		byKey[key] = append(byKey[key], c)
	}
	names := newNames(script)
	replacements := map[string]string{}
	perIteration := 0
	perUseGroups := map[string]*Parameter{}
	for _, c := range list {
		key := c.occurrences[0].key
		siblings := byKey[key]
		words := keyWords(c.occurrences)
		switch {
		case c.unsafe || c.mixed || key == "" || hasAny(words, identityWords) && !hasAny(words, generatedWords):
			report.LeftAsRecorded++
			continue
		case len(siblings) >= 3 && !hasAny(words, flowWords) && onlyUnderKey(siblings, key) && sameCase(siblings):
			group := perUseGroups[key]
			if group == nil {
				group = &Parameter{Name: names.take(key + "_each_use"), Update: "each_occurrence", Key: key}
				perUseGroups[key] = group
				script.Parameters = append(script.Parameters, uuidParameter(group.Name, group.Update, c.upper))
			}
			group.Values++
			group.Occurrences += len(c.occurrences)
			replacements[c.literal] = group.Name
		case hasAny(words, generatedWords) || len(siblings) > 1:
			if perIteration >= MaxPerIterationParameters {
				report.LeftAsRecorded++
				continue
			}
			perIteration++
			update := "each_iteration"
			if inSetup(c.occurrences) {
				update = "once"
			}
			parameter := Parameter{Name: names.take(key), Update: update, Key: key, Values: 1, Occurrences: len(c.occurrences)}
			report.Parameters = append(report.Parameters, parameter)
			script.Parameters = append(script.Parameters, uuidParameter(parameter.Name, parameter.Update, c.upper))
			replacements[c.literal] = parameter.Name
		default:
			report.LeftAsRecorded++
		}
	}
	groupNames := make([]string, 0, len(perUseGroups))
	for key := range perUseGroups {
		groupNames = append(groupNames, key)
	}
	sort.Strings(groupNames)
	for _, key := range groupNames {
		report.Parameters = append(report.Parameters, *perUseGroups[key])
	}
	if len(replacements) == 0 {
		return report
	}
	replace := func(text string) string {
		var out strings.Builder
		last := 0
		for _, span := range uuidSpans(text) {
			if name, ok := replacements[text[span[0]:span[1]]]; ok {
				out.WriteString(text[last:span[0]])
				out.WriteString("{{param." + name + "}}")
				last = span[1]
			}
		}
		if last == 0 {
			return text
		}
		out.WriteString(text[last:])
		return out.String()
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		action := ref.Action
		if action == nil {
			return nil
		}
		action.Target = replace(action.Target)
		action.Body = replace(action.Body)
		for name, value := range action.Headers {
			action.Headers[name] = replace(value)
		}
		for index := range action.HeaderList {
			action.HeaderList[index].Value = replace(action.HeaderList[index].Value)
		}
		return nil
	})
	return report
}

// HasRequestUUIDs reports whether any request target, body or header in the
// script holds a UUID literal.
func HasRequestUUIDs(script *dsl.Script) bool {
	found := false
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		action := ref.Action
		if found || action == nil {
			return nil
		}
		found = len(uuidSpans(action.Target)) > 0 || len(uuidSpans(action.Body)) > 0
		for _, header := range action.HeaderList {
			found = found || len(uuidSpans(header.Value)) > 0
		}
		for _, value := range action.Headers {
			found = found || len(uuidSpans(value)) > 0
		}
		return nil
	})
	return found
}

// locatedFields lists the request fields an action's import credential
// locators point at.
type locatedFields struct {
	target, body, allHeaders bool
	headers                  map[int]bool
	headerNames              map[string]bool
}

func credentialLocatedFields(action *dsl.Action) locatedFields {
	located := locatedFields{headers: map[int]bool{}, headerNames: map[string]bool{}}
	raw, present := action.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !present {
		return located
	}
	// Typed in a fresh import, JSON-shaped after a reopen; read both alike.
	var targets []struct {
		Field string `json:"field"`
		Index int    `json:"index"` // omitted when 0, as the engine reads it
	}
	payload, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(payload, &targets) != nil {
		located.target, located.body, located.allHeaders = true, true, true
		return located
	}
	for _, target := range targets {
		field := strings.ToLower(target.Field)
		switch {
		case strings.Contains(field, "header"):
			if target.Index < 0 || target.Index >= len(action.HeaderList) {
				located.allHeaders = true
				continue
			}
			located.headers[target.Index] = true
			located.headerNames[strings.ToLower(action.HeaderList[target.Index].Name)] = true
		case strings.Contains(field, "body"):
			located.body = true
		case strings.Contains(field, "url") || strings.Contains(field, "query") || strings.Contains(field, "target"):
			located.target = true
		}
	}
	return located
}

func uuidParameter(name, update string, upper bool) dsl.Parameter {
	parameter := dsl.Parameter{Name: name, Type: "uuid", Update: update}
	if upper {
		parameter.Format = "uppercase"
	}
	return parameter
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

// onlyUnderKey reports whether every occurrence of every value sits under
// key, so no value links two different fields.
func onlyUnderKey(values []*candidate, key string) bool {
	for _, value := range values {
		for _, occurrence := range value.occurrences {
			if occurrence.key != key {
				return false
			}
		}
	}
	return true
}

func inSetup(occurrences []occurrence) bool {
	for _, occurrence := range occurrences {
		if occurrence.setup {
			return true
		}
	}
	return false
}

func sameCase(values []*candidate) bool {
	for _, value := range values {
		if value.upper != values[0].upper {
			return false
		}
	}
	return true
}

// keyBefore returns the JSON, form or query key that immediately precedes a
// value, reading through JSON escapes and percent-encoded punctuation.
func keyBefore(prefix string) string {
	if len(prefix) > 96 {
		prefix = prefix[len(prefix)-96:]
	}
	prefix = strings.NewReplacer("%22", `"`, "%3A", ":", "%3a", ":", "%3D", "=", "%3d", "=", "%5C", `\`, "%5c", `\`).Replace(prefix)
	if match := keyPattern.FindStringSubmatch(prefix); match != nil {
		key := match[1]
		if index := strings.LastIndexAny(key, ".["); index >= 0 {
			key = key[index+1:]
		}
		return key
	}
	return ""
}

func keyWords(occurrences []occurrence) map[string]bool {
	words := map[string]bool{}
	for _, occurrence := range occurrences {
		for _, word := range splitWords(occurrence.key) {
			words[word] = true
		}
	}
	return words
}

func hasAny(words, set map[string]bool) bool {
	for word := range words {
		if set[word] {
			return true
		}
	}
	return false
}

// splitWords lowercases a key and splits it at separators and case changes.
func splitWords(key string) []string {
	var words []string
	var current []rune
	runes := []rune(key)
	flush := func() {
		if len(current) > 0 {
			words = append(words, strings.ToLower(string(current)))
			current = current[:0]
		}
	}
	for index, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if index > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[index-1]) ||
			unicode.IsUpper(runes[index-1]) && index+1 < len(runes) && unicode.IsLower(runes[index+1])) {
			flush()
		}
		current = append(current, r)
	}
	flush()
	return words
}

type nameSet map[string]bool

func newNames(script *dsl.Script) nameSet {
	names := nameSet{}
	for _, parameter := range script.Parameters {
		names[strings.ToLower(parameter.Name)] = true
	}
	for _, source := range script.DataSources {
		names[strings.ToLower(source.Name)] = true
	}
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		for _, extractor := range ref.Action.Extractors {
			names[strings.ToLower(extractor.Name)] = true
		}
		return nil
	})
	return names
}

// take returns a unique parameter name derived from a request key.
func (names nameSet) take(key string) string {
	base := strings.Join(splitWords(key), "_")
	if base == "" || !unicode.IsLetter(rune(base[0])) {
		base = "client_uuid"
	}
	if len(base) > 56 {
		base = base[:56]
	}
	name := base
	for suffix := 2; names[strings.ToLower(name)]; suffix++ {
		name = fmt.Sprintf("%s_%d", base, suffix)
	}
	names[strings.ToLower(name)] = true
	return name
}
