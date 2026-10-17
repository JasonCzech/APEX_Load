package paramize

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Generated values are identifiers and time stamps a client makes up for each
// request: trace and message IDs, a request's time stamp. A recording or
// collection keeps the one value it saw, so every virtual user and iteration
// would send it again. Each becomes a parameter the engine generates every
// iteration. A value used in several places becomes one parameter, so those
// places still agree with each other.

const (
	// generatedConfidence is for a value whose field names a per-request
	// identifier or a time stamp; it arrives selected.
	generatedConfidence = 0.85
	// generatedReviewConfidence is for a value that may be a fixed resource
	// ID, a session or a meaningful date; it waits for the operator.
	generatedReviewConfidence = 0.6
	// generatedKeyWindow bounds how far back a value's field name is read.
	generatedKeyWindow = 512

	generatedRuleUUID      = "generated.uuid"
	generatedRuleHex       = "generated.hex"
	generatedRuleTimestamp = "generated.timestamp"
)

var (
	generatedUUIDPattern      = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)
	generatedHexPattern       = regexp.MustCompile(`[0-9A-Fa-f]{16,64}`)
	generatedTimestampPattern = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-][0-9]{2}:?[0-9]{2})?`)

	// The field a value belongs to, read back from the text just before it.
	generatedAttributeKey = regexp.MustCompile(`([A-Za-z_][\w.\-:]*)\s*=\s*["'][^"'<>]*$`)
	generatedElementKey   = regexp.MustCompile(`<(?:[A-Za-z_][\w.\-]*:)?([A-Za-z_][\w.\-]*)(?:\s[^<>]*)?>[^<>]*$`)
	generatedJSONKey      = regexp.MustCompile(`"([^"\\]{1,128})"\s*:\s*"[^"\\]*$`)
	generatedPairKey      = regexp.MustCompile(`(?:^|[?&;,\s])([A-Za-z0-9_.\-\[\]]{1,128})=[^&;,#\s"']*$`)
)

// generatedIdentifierStems name a value a client makes up per request.
var generatedIdentifierStems = []string{"trace", "span", "message", "msg", "request", "correlation", "nonce", "idempotency"}

// generatedReferenceStems name an ID a client may make up, or may take from
// something the target already holds (a conversation, a payment
// transaction, an event), so generating one waits for the operator.
var generatedReferenceStems = []string{"conversation", "transaction", "event"}

// generatedIdentifierSuffixes complete a stem written as one word
// (requestid, traceparent); a bare stem matches too, a longer word such as
// requested does not.
var generatedIdentifierSuffixes = map[string]bool{"": true, "s": true, "id": true, "ids": true, "uuid": true, "guid": true, "key": true, "parent": true, "state": true, "no": true, "num": true, "number": true}

// generatedIdentifierWords and generatedReferenceWords match only as whole words.
var (
	generatedIdentifierWords = map[string]bool{"req": true, "rqid": true}
	generatedReferenceWords  = map[string]bool{"txn": true}
)

// generatedSessionWords name a value a server may also hold, so generating
// a new one can break the request.
var generatedSessionWords = map[string]bool{"session": true, "sessionid": true, "ses": true, "sid": true}

type generatedKeyRole struct {
	identifier bool
	reference  bool
	session    bool
	timeStamp  bool
}

// confident reports a field named only for a value made up per request.
func (role generatedKeyRole) confident() bool {
	return role.identifier && !role.reference && !role.session
}

func generatedStemWord(word string, stems []string) bool {
	for _, stem := range stems {
		if strings.HasPrefix(word, stem) && generatedIdentifierSuffixes[word[len(stem):]] {
			return true
		}
	}
	return false
}

func classifyGeneratedKey(key string) generatedKeyRole {
	var role generatedKeyRole
	timeWord := false
	for _, word := range parameterNameWords(key) {
		switch {
		case generatedSessionWords[word] || strings.HasPrefix(word, "session"):
			role.session = true
		case generatedIdentifierWords[word] || generatedStemWord(word, generatedIdentifierStems):
			role.identifier = true
		case generatedReferenceWords[word] || generatedStemWord(word, generatedReferenceStems):
			role.reference = true
		case strings.HasPrefix(word, "stamp") || strings.HasPrefix(word, "timestamp") || word == "sent":
			role.timeStamp = true
		case word == "time" || word == "date" || word == "at":
			timeWord = true
		}
	}
	// requestTime, messageDate, eventAt: when this request or message was made.
	if timeWord && role.identifier {
		role.timeStamp = true
	}
	return role
}

type generatedToken struct {
	start, end int
	rule       string
}

// generatedTokens finds the generated-looking values in text, outside
// templates and not cut out of a longer word.
func generatedTokens(text string, budget *suggestionRegexBudget) ([]generatedToken, error) {
	if text == "" {
		return nil, nil
	}
	templates := templateSpans(text)
	outside := func(start, end int) bool {
		for _, span := range templates {
			if start < span[1] && end > span[0] {
				return false
			}
		}
		return true
	}
	// An ID glued to letters or digits is part of a longer word; one after a
	// separator such as _, ~ or - (msg_<uuid>) stands alone.
	wordByte := func(index int) bool {
		if index < 0 || index >= len(text) {
			return false
		}
		b := text[index]
		return b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
	}
	digitByte := func(index int) bool {
		return index >= 0 && index < len(text) && text[index] >= '0' && text[index] <= '9'
	}
	var tokens []generatedToken
	uuids, err := budget.findAllSubmatchIndexes(generatedUUIDPattern, text)
	if err != nil {
		return nil, err
	}
	for _, match := range uuids {
		if !wordByte(match[0]-1) && !wordByte(match[1]) && outside(match[0], match[1]) {
			tokens = append(tokens, generatedToken{start: match[0], end: match[1], rule: generatedRuleUUID})
		}
	}
	hexRuns, err := budget.findAllSubmatchIndexes(generatedHexPattern, text)
	if err != nil {
		return nil, err
	}
	for _, match := range hexRuns {
		run := text[match[0]:match[1]]
		if wordByte(match[0]-1) || wordByte(match[1]) || !outside(match[0], match[1]) ||
			!strings.ContainsAny(run, "0123456789") || !strings.ContainsAny(run, "abcdefABCDEF") {
			continue
		}
		overlapsUUID := false
		for _, token := range tokens {
			if match[0] < token.end && match[1] > token.start {
				overlapsUUID = true
				break
			}
		}
		if !overlapsUUID {
			tokens = append(tokens, generatedToken{start: match[0], end: match[1], rule: generatedRuleHex})
		}
	}
	stamps, err := budget.findAllSubmatchIndexes(generatedTimestampPattern, text)
	if err != nil {
		return nil, err
	}
	for _, match := range stamps {
		if !digitByte(match[0]-1) && !digitByte(match[1]) && outside(match[0], match[1]) {
			tokens = append(tokens, generatedToken{start: match[0], end: match[1], rule: generatedRuleTimestamp})
		}
	}
	// Replacing a value also rewrites it inside a longer one in the same
	// field, so a value another token contains is left to that token.
	kept := make([]generatedToken, 0, len(tokens))
	for _, token := range tokens {
		value := text[token.start:token.end]
		inside := false
		for _, other := range tokens {
			otherValue := text[other.start:other.end]
			if len(otherValue) > len(value) && strings.Contains(otherValue, value) {
				inside = true
				break
			}
		}
		if !inside {
			kept = append(kept, token)
		}
	}
	tokens = kept
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].start < tokens[j].start })
	return tokens, nil
}

// templateSpans returns the byte ranges of {{...}} templates in text.
func templateSpans(text string) [][2]int {
	var spans [][2]int
	for offset := 0; offset < len(text); {
		open := strings.Index(text[offset:], "{{")
		if open < 0 {
			break
		}
		start := offset + open
		close := strings.Index(text[start+2:], "}}")
		if close < 0 {
			spans = append(spans, [2]int{start, len(text)})
			break
		}
		end := start + 2 + close + 2
		spans = append(spans, [2]int{start, end})
		offset = end
	}
	return spans
}

// generatedValueKey names the field holding the value that starts at offset:
// an XML attribute or element, a JSON key or a key=value pair.
func generatedValueKey(text string, offset int) string {
	windowStart := offset - generatedKeyWindow
	if windowStart < 0 {
		windowStart = 0
	}
	before := text[windowStart:offset]
	for _, pattern := range []*regexp.Regexp{generatedAttributeKey, generatedJSONKey, generatedElementKey, generatedPairKey} {
		if match := pattern.FindStringSubmatch(before); match != nil {
			return match[1]
		}
	}
	return ""
}

// generatedCollector groups the occurrences of one recorded value under one
// suggestion and one parameter.
type generatedCollector struct {
	names   *nameRegistry
	claimed map[string]bool
	order   []string
	grouped map[string]*generatedGroup
	seen    map[string]bool
}

type generatedGroup struct {
	suggestion Suggestion
	// occurrences counts substitutions, separately from deduplicated fields.
	// Several copies in one body/header must still produce the same value.
	occurrences int
	// confident stays true while every occurrence sits in a field named for a
	// per-request identifier or time stamp.
	confident bool
	key       string
	place     string
	// base is the name the parameter is claimed under when it is emitted.
	base string
}

func newGeneratedCollector(names *nameRegistry, claimed map[string]bool) *generatedCollector {
	return &generatedCollector{names: names, claimed: claimed, grouped: map[string]*generatedGroup{}, seen: map[string]bool{}}
}

// add records one occurrence of value in field. place describes the field
// for the rationale ("idp-trace-id header", "messageId body field").
func (c *generatedCollector) add(rule, value, key, place, section string, index int, action *dsl.Action, field string, occurrences int) {
	for literal := range c.claimed {
		// A value inside a correlated or secret literal belongs to that pass.
		if strings.Contains(literal, value) {
			return
		}
	}
	role := classifyGeneratedKey(key)
	confident := false
	switch rule {
	case generatedRuleUUID:
		confident = role.confident()
	case generatedRuleHex:
		// A bare hex run is only an identifier when its field says so.
		if !role.identifier && !role.reference && !role.session {
			return
		}
		confident = role.confident()
	case generatedRuleTimestamp:
		confident = role.timeStamp
	}
	if group := c.grouped[value]; group != nil {
		// Even two occurrences in one field can have different roles.
		group.confident = group.confident && confident
	}
	locationKey := fmt.Sprintf("%s|%s|%d|%s", value, section, index, field)
	if c.seen[locationKey] {
		return
	}
	c.seen[locationKey] = true
	location := Location{
		Section: section, ActionIndex: index, ActionName: action.Name, Field: field,
		SearchText: value,
	}
	if group, ok := c.grouped[value]; ok {
		group.occurrences += occurrences
		group.suggestion.Locations = append(group.suggestion.Locations, location)
		return
	}
	base := key
	if base == "" {
		base = map[string]string{generatedRuleUUID: "generated_id", generatedRuleHex: "generated_id", generatedRuleTimestamp: "generated_time"}[rule]
	}
	c.grouped[value] = &generatedGroup{
		occurrences: occurrences,
		confident:   confident,
		key:         key,
		place:       place,
		base:        base,
		suggestion: Suggestion{
			Kind:         KindGenerated,
			LiteralValue: value,
			SampleValue:  truncateValue(value),
			Locations:    []Location{location},
			RuleID:       rule,
		},
	}
	c.order = append(c.order, value)
}

// generatedEachUseKey groups values recorded once each in the same kind of
// field, written the same way: a per-request ID such as X-Request-Id.
type generatedEachUseKey struct {
	rule, place, format string
	confident           bool
	// ordinal is the value's place among the same kind of values in its
	// request: a trace header's trace ID shares with the other trace IDs, its
	// span ID with the other span IDs.
	ordinal int
}

func (c *generatedCollector) suggestions() []Suggestion {
	// A value recorded once has no other use to stay equal to. Values recorded
	// once each in the same kind of field of different requests share one
	// parameter that is new each time it is sent: every request still sends its
	// own value, and a recording with an ID per request gets one parameter
	// rather than one per request. Two values in one request (a trace and a span
	// ID) stay apart, and a value used in several places keeps its own
	// parameter, new each iteration, so those uses stay equal.
	eachUse := map[generatedEachUseKey][]*generatedGroup{}
	ordinals := map[*generatedGroup]int{}
	kindOf := func(group *generatedGroup) (generatedEachUseKey, bool) {
		if group.occurrences != 1 {
			return generatedEachUseKey{}, false
		}
		shape := generatedParameter(group.suggestion.RuleID, "", group.suggestion.LiteralValue)
		return generatedEachUseKey{
			rule:      group.suggestion.RuleID,
			place:     group.place,
			format:    fmt.Sprintf("%s|%s|%d|%s", shape.Type, shape.Format, shape.Length, shape.Charset),
			confident: group.confident,
		}, true
	}
	keyOf := func(group *generatedGroup) (generatedEachUseKey, bool) {
		key, single := kindOf(group)
		key.ordinal = ordinals[group]
		return key, single
	}
	seenInAction := map[generatedEachUseKey]map[string]int{}
	for _, value := range c.order {
		group := c.grouped[value]
		key, single := kindOf(group)
		if !single {
			continue
		}
		location := group.suggestion.Locations[0]
		action := fmt.Sprintf("%s|%d", location.Section, location.ActionIndex)
		if seenInAction[key] == nil {
			seenInAction[key] = map[string]int{}
		}
		ordinals[group] = seenInAction[key][action]
		seenInAction[key][action]++
		key.ordinal = ordinals[group]
		eachUse[key] = append(eachUse[key], group)
	}
	out := make([]Suggestion, 0, len(c.order))
	emitted := map[generatedEachUseKey]bool{}
	for _, value := range c.order {
		group := c.grouped[value]
		suggestion := group.suggestion
		suggestion.Locations = append([]Location(nil), suggestion.Locations...)
		members := []*generatedGroup{group}
		key, single := keyOf(group)
		shared := single && len(eachUse[key]) > 1
		if shared {
			if emitted[key] {
				continue
			}
			emitted[key] = true
			members = eachUse[key]
			suggestion.Locations = suggestion.Locations[:0]
			for _, member := range members {
				suggestion.Locations = append(suggestion.Locations, member.suggestion.Locations...)
			}
		}
		varName := c.names.claim(group.base)
		suggestion.VarName = varName
		suggestion.Replacement = "{{var." + varName + "}}"
		for index := range suggestion.Locations {
			suggestion.Locations[index].ReplaceText = suggestion.Replacement
		}
		suggestion.Parameter = generatedParameter(suggestion.RuleID, varName, suggestion.LiteralValue)
		suggestion.Confidence = generatedReviewConfidence
		if group.confident {
			suggestion.Confidence = generatedConfidence
		}
		if shared {
			suggestion.Parameter.Update = "each_occurrence"
			suggestion.Rationale = generatedEachUseRationale(suggestion.RuleID, group.place, group.confident, len(members))
		} else {
			suggestion.Rationale = generatedRationale(suggestion.RuleID, group.place, group.confident, len(suggestion.Locations))
		}
		out = append(out, suggestion)
	}
	return out
}

// generatedRationale explains a suggestion without quoting the recorded
// value, so it can cross the import response boundary as written.
func generatedRationale(rule, place string, confident bool, occurrences int) string {
	what := map[string]string{
		generatedRuleUUID:      "a new UUID",
		generatedRuleHex:       "a new random hex value of the same length",
		generatedRuleTimestamp: "the current time in the same format",
	}[rule]
	recorded := map[string]string{
		generatedRuleUUID:      "the same recorded UUID",
		generatedRuleHex:       "the same recorded hex identifier",
		generatedRuleTimestamp: "the same recorded time",
	}[rule]
	sentence := fmt.Sprintf("The %s sends %s every time.", place, recorded)
	if occurrences == 2 {
		sentence = fmt.Sprintf("The %s and 1 other place send %s every time.", place, recorded)
	} else if occurrences > 2 {
		sentence = fmt.Sprintf("The %s and %d other places send %s every time.", place, occurrences-1, recorded)
	}
	text := fmt.Sprintf("%s Generate %s each iteration.", sentence, what)
	if !confident {
		switch rule {
		case generatedRuleTimestamp:
			text += " Leave it if the date means something to the target, such as a range or a birth date."
		default:
			text += " Leave it if the target expects this exact value, such as a conversation, account or session it already holds."
		}
	}
	return text
}

// generatedEachUseRationale explains one parameter shared by values each
// recorded once in the same kind of field.
func generatedEachUseRationale(rule, place string, confident bool, places int) string {
	what := map[string]string{
		generatedRuleUUID:      "a new UUID",
		generatedRuleHex:       "a new random hex value of the same length",
		generatedRuleTimestamp: "the current time in the same format",
	}[rule]
	recorded := map[string]string{
		generatedRuleUUID:      "UUID",
		generatedRuleHex:       "hex identifier",
		generatedRuleTimestamp: "time",
	}[rule]
	text := fmt.Sprintf("The %s holds a different recorded %s in each of %d places, and each is sent again as recorded. Generate %s each time one is sent.", place, recorded, places, what)
	if !confident {
		switch rule {
		case generatedRuleTimestamp:
			text += " Leave it if the dates mean something to the target, such as a range or a birth date."
		default:
			text += " Leave it if the target expects these exact values, such as conversations, accounts or sessions it already holds."
		}
	}
	return text
}

// generatedParameter is the parameter that makes the value each iteration.
func generatedParameter(rule, name, value string) *dsl.Parameter {
	switch rule {
	case generatedRuleUUID:
		param := &dsl.Parameter{Name: name, Type: "uuid", Update: "each_iteration"}
		if strings.ToUpper(value) == value && strings.ContainsAny(value, "ABCDEF") {
			param.Format = "uppercase"
		}
		return param
	case generatedRuleHex:
		charset := "0123456789abcdef"
		if strings.ToUpper(value) == value {
			charset = "0123456789ABCDEF"
		}
		return &dsl.Parameter{Name: name, Type: "random_string", Update: "each_iteration", Length: len(value), Charset: charset}
	default:
		return &dsl.Parameter{Name: name, Type: "date_time", Update: "each_iteration", Format: generatedTimestampLayout(value), Timezone: "UTC"}
	}
}

// generatedTimestampLayout is the Go layout that writes the current time the
// way value is written: the same fractional digits and zone style.
func generatedTimestampLayout(value string) string {
	layout := "2006-01-02T15:04:05"
	rest := value[len("2006-01-02T15:04:05"):]
	if strings.HasPrefix(rest, ".") {
		digits := 1
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			digits++
		}
		layout += "." + strings.Repeat("0", digits-1)
		rest = rest[digits:]
	}
	switch {
	case rest == "Z":
		layout += "Z07:00"
	case strings.Contains(rest, ":"):
		layout += "-07:00"
	case rest != "":
		layout += "-0700"
	}
	return layout
}

// generatedSuggestions proposes a generated parameter for each identifier or
// time stamp a request sends as a recorded literal.
func generatedSuggestions(script *dsl.Script, names *nameRegistry, claimed map[string]bool) ([]Suggestion, error) {
	collector := newGeneratedCollector(names, claimed)
	budget := &suggestionRegexBudget{}
	var scanErr error
	// scan reads one field. headerName is set for a header, whose own name
	// classifies a value unless the value names its part (a cookie's sid=).
	// leave is the rest of the field, outside text, where a value must not
	// also appear: a replacement rewrites the whole field.
	scan := func(text, leave, headerName, kind, section string, index int, action *dsl.Action, field string, replacementTexts []string) {
		if scanErr != nil {
			return
		}
		tokens, err := generatedTokens(text, budget)
		if err != nil {
			scanErr = err
			return
		}
		counts := map[string]int{}
		if replacementTexts == nil {
			replacementTexts = []string{text}
		}
		for _, token := range tokens {
			if leave != "" && strings.Contains(leave, text[token.start:token.end]) {
				continue
			}
			key := generatedValueKey(text, token.start)
			var place string
			switch {
			case headerName != "" && key == "":
				key, place = headerName, headerName+" header"
			case headerName != "":
				place = fmt.Sprintf("%s value in the %s header", key, headerName)
			case key == "":
				place = map[string]string{"query": "query string", "body": "request body"}[kind]
			default:
				place = map[string]string{"query": key + " query parameter", "body": key + " body field"}[kind]
			}
			value := text[token.start:token.end]
			count, counted := counts[value]
			if !counted {
				// Apply replaces every literal outside templates, including a
				// second copy that is not a separately recognized token, and
				// every occurrence in same-name ordered headers.
				for _, replacementText := range replacementTexts {
					if err := budget.chargeSearchBytes(len(replacementText)); err != nil {
						scanErr = err
						return
					}
					start := 0
					for _, span := range templateSpans(replacementText) {
						count += strings.Count(replacementText[start:span[0]], value)
						start = span[1]
					}
					count += strings.Count(replacementText[start:], value)
				}
				counts[value] = count
			}
			collector.add(token.rule, value, key, place, section, index, action, field, count)
		}
	}
	forEachAction(script, func(section string, index int, action *dsl.Action) {
		headerFields := map[string]string{}
		headerTexts := generatedHeaderReplacementTexts(action)
		for _, header := range actionHeaders(action, nil) {
			if credential.ImportCredentialHeader(header.name) {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(header.name))
			field := headerFields[name]
			if field == "" {
				field = "headers." + header.name
				headerFields[name] = field
			}
			scan(header.value, "", header.name, "header", section, index, action, field, headerTexts[name])
		}
		if query := strings.Index(action.Target, "?"); query >= 0 {
			// Path segments name resources, so only the query is read, and a
			// value the path also holds stays as recorded.
			scan(action.Target[query:], action.Target[:query], "", "query", section, index, action, "target", nil)
		}
		scan(action.Body, "", "", "body", section, index, action, "body", nil)
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return collector.suggestions(), nil
}

// A header location replaces every same-name value. Count the authoritative
// ordered list without deduplicating equal entries; its compatibility map is
// only a mirror. Index once per action, falling back to map values only for
// names without ordered entries, so token-free headers do not cause rescans.
func generatedHeaderReplacementTexts(action *dsl.Action) map[string][]string {
	values := make(map[string][]string, len(action.HeaderList)+len(action.Headers))
	orderedNames := make(map[string]bool, len(action.HeaderList))
	for _, header := range action.HeaderList {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		orderedNames[name] = true
		values[name] = append(values[name], header.Value)
	}
	for _, key := range sortedKeys(action.Headers) {
		name := strings.ToLower(strings.TrimSpace(key))
		if !orderedNames[name] {
			values[name] = append(values[name], action.Headers[key])
		}
	}
	return values
}
