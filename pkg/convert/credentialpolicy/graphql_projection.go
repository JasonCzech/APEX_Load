package credentialpolicy

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

type graphQLToken struct {
	kind  byte
	text  string
	start int
	end   int
}

const (
	graphQLTokenName    byte = 'N'
	graphQLTokenString  byte = 'S'
	graphQLTokenOther   byte = 'V'
	graphQLTokenComment byte = 'C'
)

// scanGraphQLCredentialLiteral performs a bounded lexical scan of GraphQL
// source. GraphQL permits arbitrary ignored whitespace, commas, and # comments,
// so substring-distance checks are not a safe way to identify argument values.
// A lexer is sufficient here: the projector does not translate source, it only
// needs to reject credential-bearing inline values and variable defaults
// that cannot be safely rewritten without a source/schema-aware AST.
func scanGraphQLCredentialLiteral(query []byte, options Options) (bool, string) {
	tokens, err := lexGraphQLWithOptions(query, options)
	if err != nil {
		if errors.Is(err, errGraphQLWorkLimit) {
			return false, "credential_structured_work_limit_exceeded"
		}
		if errors.Is(err, errGraphQLTokenLimit) {
			return false, "credential_graphql_value_limit_exceeded"
		}
		if containsSensitiveNameHintWithOptions(query, options) {
			return false, "credential_graphql_malformed_sensitive"
		}
		return false, ""
	}
	parenthesisDepth := 0
	for index, token := range tokens {
		switch token.kind {
		case '(':
			parenthesisDepth++
		case ')':
			if parenthesisDepth > 0 {
				parenthesisDepth--
			}
		}
		if token.kind != graphQLTokenName {
			continue
		}
		if _, sensitive := governedSensitiveName(token.text, options); !sensitive {
			continue
		}
		previous := previousSignificantGraphQLToken(tokens, index)
		if previous >= 0 && tokens[previous].kind == '$' {
			if graphQLSensitiveVariableHasDefault(tokens, index) {
				return true, ""
			}
			continue
		}
		colon := nextSignificantGraphQLToken(tokens, index)
		if colon >= len(tokens) || tokens[colon].kind != ':' {
			continue
		}
		valueIndex := nextSignificantGraphQLToken(tokens, colon)
		if valueIndex >= len(tokens) {
			continue
		}
		valueKind := tokens[valueIndex].kind
		// A runtime variable is the only inline credential value whose
		// provenance can be established outside the query source. Every literal
		// kind—including numbers, booleans, enums, null, lists, and objects—is
		// unsafe to rewrite without an AST and schema type.
		if valueKind == '$' {
			continue
		}
		if valueKind == graphQLTokenString || parenthesisDepth > 0 {
			return true, ""
		}
		// Outside an argument/default parenthesis, name:name is a field alias.
		// Other value token kinds are not valid alias targets and therefore
		// represent malformed credential-bearing source that must fail closed.
		if valueKind != graphQLTokenName {
			return true, ""
		}
	}
	return false, ""
}

// projectKnownGraphQLScalars applies disclosure-only value equivalence after
// the ordinary GraphQL credential policy has classified the document. The
// lexer spans keep replacements inside string literals and comments; names,
// punctuation, aliases, variable references, and type syntax are never passed
// to the caller-provided matcher.
func projectKnownGraphQLScalars(
	query []byte,
	options Options,
) ([]byte, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, Result{}, err
	}
	if options.KnownCredentialMatcher == nil {
		return append([]byte(nil), query...), exactResult(), nil
	}
	if len(query) > options.Limits.MaxBytes {
		return nil, rejected(options.Source, "credential_graphql_limit_exceeded"), nil
	}
	tokens, err := lexGraphQLWithOptions(query, options)
	if err != nil {
		if errors.Is(err, errGraphQLWorkLimit) {
			return nil, rejected(options.Source, "credential_structured_work_limit_exceeded"), nil
		}
		if errors.Is(err, errGraphQLTokenLimit) {
			return nil,
				rejected(options.Source, "credential_graphql_value_limit_exceeded"),
				nil
		}
		return nil,
			rejected(options.Source, "credential_graphql_known_value_malformed"),
			nil
	}
	if !validKnownGraphQLLexicalStructure(query, tokens) {
		return nil,
			rejected(options.Source, "credential_graphql_known_value_malformed"),
			nil
	}
	replacements := make([]replacement, 0)
	for _, token := range tokens {
		switch token.kind {
		case graphQLTokenString:
			if !knownCredentialScalarMatch(options, token.text) {
				continue
			}
			encoded, err := json.Marshal(credential.Redacted)
			if err != nil {
				return nil, Result{}, errors.New("projected GraphQL string could not be encoded")
			}
			replacements = append(replacements, replacement{
				start: token.start,
				end:   token.end,
				value: encoded,
			})
		case graphQLTokenComment:
			if !knownCredentialScalarMatch(options, token.text) {
				continue
			}
			replacements = append(replacements, replacement{
				start: token.start,
				end:   token.end,
				value: []byte(credential.Redacted),
			})
		}
	}
	matched, limitCode := graphQLKnownNonStringValue(
		tokens,
		boundedKnownCredentialMatcher(options),
		options.Limits,
		graphQLExecutableDocument(tokens),
	)
	if limitCode != "" {
		return nil, rejected(options.Source, limitCode), nil
	}
	if matched {
		return nil,
			rejected(
				options.Source,
				"credential_graphql_known_non_string_unsupported",
			),
			nil
	}
	return applyReplacements(query, replacements), exactResult(), nil
}

func graphQLExecutableSource(query []byte, options Options) (bool, error) {
	tokens, err := lexGraphQLWithOptions(query, options)
	return err == nil && graphQLExecutableDocument(tokens), err
}

func graphQLExecutableDocument(tokens []graphQLToken) bool {
	index := significantGraphQLTokenAtOrAfter(tokens, 0)
	if index >= len(tokens) {
		return false
	}
	if tokens[index].kind == '{' {
		return true
	}
	if tokens[index].kind != graphQLTokenName {
		return false
	}
	switch strings.ToLower(tokens[index].text) {
	case "query", "mutation", "subscription", "fragment":
		return true
	default:
		return false
	}
}

func graphQLKnownNonStringValue(
	tokens []graphQLToken,
	matcher func(string) bool,
	limits Limits,
	executable bool,
) (bool, string) {
	budget := limits.MaxValues
	validationBudget := limits.MaxValues
	valueParentheses := make([]bool, 0)
	valueParenthesisDepth := 0
	valueCompositeDepth := 0
	for index, token := range tokens {
		switch token.kind {
		case '(':
			valueContext := executable ||
				graphQLSDLDirectiveApplicationParenthesis(tokens, index)
			valueParentheses = append(valueParentheses, valueContext)
			if valueContext {
				valueParenthesisDepth++
			}
			continue
		case ')':
			if len(valueParentheses) > 0 {
				last := len(valueParentheses) - 1
				if valueParentheses[last] && valueParenthesisDepth > 0 {
					valueParenthesisDepth--
				}
				valueParentheses = valueParentheses[:last]
			}
			continue
		case '[', '{':
			if valueParenthesisDepth > 0 {
				valueCompositeDepth++
			}
			continue
		case ']', '}':
			if valueParenthesisDepth > 0 && valueCompositeDepth > 0 {
				valueCompositeDepth--
			}
			continue
		case '=':
			valueIndex := nextSignificantGraphQLToken(tokens, index)
			if valueIndex >= len(tokens) {
				continue
			}
			next, matched, code := inspectKnownGraphQLValue(
				tokens,
				valueIndex,
				matcher,
				false,
				0,
				limits.MaxDepth,
				&budget,
			)
			if matched || code != "" {
				return matched, code
			}
			if !validGraphQLDefaultValueEnd(
				tokens,
				next,
				limits.MaxDepth,
				executable,
				&validationBudget,
			) {
				return false, "credential_graphql_known_value_malformed"
			}
			continue
		}
		if token.kind != graphQLTokenName ||
			valueParenthesisDepth == 0 ||
			valueCompositeDepth > 0 {
			continue
		}
		previous := previousSignificantGraphQLToken(tokens, index)
		if previous >= 0 && tokens[previous].kind == '$' {
			// The token following "$name:" is a type identity, not a value.
			continue
		}
		colon := nextSignificantGraphQLToken(tokens, index)
		if colon >= len(tokens) || tokens[colon].kind != ':' {
			continue
		}
		valueIndex := nextSignificantGraphQLToken(tokens, colon)
		if valueIndex >= len(tokens) {
			continue
		}
		next, matched, code := inspectKnownGraphQLValue(
			tokens,
			valueIndex,
			matcher,
			executable,
			0,
			limits.MaxDepth,
			&budget,
		)
		if matched || code != "" {
			return matched, code
		}
		if !validGraphQLArgumentValueEnd(tokens, next) {
			return false, "credential_graphql_known_value_malformed"
		}
	}
	return false, ""
}

func graphQLSDLDirectiveApplicationParenthesis(
	tokens []graphQLToken,
	openIndex int,
) bool {
	directiveName := previousSignificantGraphQLToken(tokens, openIndex)
	if directiveName < 0 || tokens[directiveName].kind != graphQLTokenName {
		return false
	}
	at := previousSignificantGraphQLToken(tokens, directiveName)
	if at < 0 || tokens[at].kind != '@' {
		return false
	}
	beforeAt := previousSignificantGraphQLToken(tokens, at)
	return beforeAt < 0 ||
		tokens[beforeAt].kind != graphQLTokenName ||
		!strings.EqualFold(tokens[beforeAt].text, "directive")
}

func inspectKnownGraphQLValue(
	tokens []graphQLToken,
	index int,
	matcher func(string) bool,
	allowVariables bool,
	depth int,
	maxDepth int,
	budget *int,
) (int, bool, string) {
	if depth > maxDepth {
		return index, false, "credential_graphql_known_value_depth_limit_exceeded"
	}
	if budget == nil || *budget <= 0 {
		return index, false, "credential_graphql_known_value_work_limit_exceeded"
	}
	*budget--
	index = significantGraphQLTokenAtOrAfter(tokens, index)
	if index >= len(tokens) {
		return index, false, ""
	}
	token := tokens[index]
	switch token.kind {
	case graphQLTokenString:
		// String tokens are safely projected by their source spans.
		return index + 1, false, ""
	case '$':
		if !allowVariables {
			return index, false, "credential_graphql_known_value_malformed"
		}
		name := nextSignificantGraphQLToken(tokens, index)
		if name < len(tokens) && tokens[name].kind == graphQLTokenName {
			return name + 1, false, ""
		}
		return index, false, "credential_graphql_known_value_malformed"
	case '[':
		cursor := significantGraphQLTokenAtOrAfter(tokens, index+1)
		for cursor < len(tokens) && tokens[cursor].kind != ']' {
			next, matched, code := inspectKnownGraphQLValue(
				tokens,
				cursor,
				matcher,
				allowVariables,
				depth+1,
				maxDepth,
				budget,
			)
			if matched || code != "" {
				return next, matched, code
			}
			if next <= cursor {
				next = cursor + 1
			}
			cursor = significantGraphQLTokenAtOrAfter(tokens, next)
		}
		if cursor < len(tokens) {
			return cursor + 1, false, ""
		}
		return cursor, false, "credential_graphql_known_value_malformed"
	case '{':
		cursor := significantGraphQLTokenAtOrAfter(tokens, index+1)
		for cursor < len(tokens) && tokens[cursor].kind != '}' {
			if tokens[cursor].kind != graphQLTokenName {
				return cursor, false, "credential_graphql_known_value_malformed"
			}
			colon := nextSignificantGraphQLToken(tokens, cursor)
			if colon >= len(tokens) || tokens[colon].kind != ':' {
				return colon, false, "credential_graphql_known_value_malformed"
			}
			valueIndex := nextSignificantGraphQLToken(tokens, colon)
			next, matched, code := inspectKnownGraphQLValue(
				tokens,
				valueIndex,
				matcher,
				allowVariables,
				depth+1,
				maxDepth,
				budget,
			)
			if matched || code != "" {
				return next, matched, code
			}
			if next <= valueIndex {
				next = valueIndex + 1
			}
			cursor = significantGraphQLTokenAtOrAfter(tokens, next)
		}
		if cursor < len(tokens) {
			return cursor + 1, false, ""
		}
		return cursor, false, "credential_graphql_known_value_malformed"
	case graphQLTokenName:
		return index + 1, matcher(token.text), ""
	case graphQLTokenOther:
		if token.text == "..." {
			return index, false, "credential_graphql_known_value_malformed"
		}
		return index + 1, matcher(token.text), ""
	default:
		// A punctuation token cannot begin a GraphQL value.
		return index, false, "credential_graphql_known_value_malformed"
	}
}

func validGraphQLArgumentValueEnd(tokens []graphQLToken, index int) bool {
	index = significantGraphQLTokenAtOrAfter(tokens, index)
	if index >= len(tokens) {
		return false
	}
	if tokens[index].kind == ')' {
		return true
	}
	if tokens[index].kind != graphQLTokenName {
		return false
	}
	colon := nextSignificantGraphQLToken(tokens, index)
	return colon < len(tokens) && tokens[colon].kind == ':'
}

func validGraphQLDefaultValueEnd(
	tokens []graphQLToken,
	index int,
	maxDepth int,
	executable bool,
	budget *int,
) bool {
	for steps := 0; steps <= len(tokens); steps++ {
		if !consumeGraphQLValidationBudget(budget) {
			return false
		}
		index = significantGraphQLTokenAtOrAfter(tokens, index)
		if index >= len(tokens) {
			return false
		}
		switch tokens[index].kind {
		case ')', '}':
			return true
		case '$':
			if !executable {
				return false
			}
			next, ok := graphQLVariableDefinitionTail(
				tokens,
				index,
				budget,
			)
			if !ok {
				return false
			}
			index = next
		case graphQLTokenName:
			if executable {
				return false
			}
			next, ok := graphQLNamedInputDefinitionTail(
				tokens,
				index,
				budget,
			)
			if !ok {
				return false
			}
			index = next
		case '@':
			next, ok := graphQLDirectiveTail(
				tokens,
				index,
				maxDepth,
				budget,
			)
			if !ok {
				return false
			}
			index = next
		default:
			return false
		}
		index = significantGraphQLTokenAtOrAfter(tokens, index)
		if index < len(tokens) && tokens[index].kind == '=' {
			// The main scanner validates this following definition's default
			// independently, including its own complete follower production.
			return true
		}
	}
	return false
}

func graphQLVariableDefinitionTail(
	tokens []graphQLToken,
	index int,
	budget *int,
) (int, bool) {
	if !consumeGraphQLValidationBudget(budget) {
		return index, false
	}
	name := nextSignificantGraphQLToken(tokens, index)
	if name >= len(tokens) || tokens[name].kind != graphQLTokenName {
		return index, false
	}
	colon := nextSignificantGraphQLToken(tokens, name)
	if colon >= len(tokens) || tokens[colon].kind != ':' {
		return colon, false
	}
	return graphQLTypeReferenceEnd(
		tokens,
		nextSignificantGraphQLToken(tokens, colon),
		budget,
	)
}

func graphQLNamedInputDefinitionTail(
	tokens []graphQLToken,
	index int,
	budget *int,
) (int, bool) {
	if !consumeGraphQLValidationBudget(budget) {
		return index, false
	}
	if index >= len(tokens) || tokens[index].kind != graphQLTokenName {
		return index, false
	}
	colon := nextSignificantGraphQLToken(tokens, index)
	if colon >= len(tokens) || tokens[colon].kind != ':' {
		return colon, false
	}
	return graphQLTypeReferenceEnd(
		tokens,
		nextSignificantGraphQLToken(tokens, colon),
		budget,
	)
}

func graphQLTypeReferenceEnd(
	tokens []graphQLToken,
	index int,
	budget *int,
) (int, bool) {
	index = significantGraphQLTokenAtOrAfter(tokens, index)
	listDepth := 0
	for index < len(tokens) && tokens[index].kind == '[' {
		if !consumeGraphQLValidationBudget(budget) {
			return index, false
		}
		listDepth++
		index = significantGraphQLTokenAtOrAfter(tokens, index+1)
	}
	if index >= len(tokens) || tokens[index].kind != graphQLTokenName {
		return index, false
	}
	if !consumeGraphQLValidationBudget(budget) {
		return index, false
	}
	index = significantGraphQLTokenAtOrAfter(tokens, index+1)
	if index < len(tokens) && tokens[index].kind == '!' {
		if !consumeGraphQLValidationBudget(budget) {
			return index, false
		}
		index = significantGraphQLTokenAtOrAfter(tokens, index+1)
	}
	for listDepth > 0 {
		if !consumeGraphQLValidationBudget(budget) {
			return index, false
		}
		if index >= len(tokens) || tokens[index].kind != ']' {
			return index, false
		}
		index = significantGraphQLTokenAtOrAfter(tokens, index+1)
		if index < len(tokens) && tokens[index].kind == '!' {
			if !consumeGraphQLValidationBudget(budget) {
				return index, false
			}
			index = significantGraphQLTokenAtOrAfter(tokens, index+1)
		}
		listDepth--
	}
	return index, true
}

func graphQLDirectiveTail(
	tokens []graphQLToken,
	index int,
	maxDepth int,
	budget *int,
) (int, bool) {
	if !consumeGraphQLValidationBudget(budget) {
		return index, false
	}
	name := nextSignificantGraphQLToken(tokens, index)
	if name >= len(tokens) || tokens[name].kind != graphQLTokenName {
		return index, false
	}
	index = significantGraphQLTokenAtOrAfter(tokens, name+1)
	if index >= len(tokens) || tokens[index].kind != '(' {
		return index, true
	}
	cursor := significantGraphQLTokenAtOrAfter(tokens, index+1)
	arguments := 0
	for cursor < len(tokens) && tokens[cursor].kind != ')' {
		if !consumeGraphQLValidationBudget(budget) {
			return cursor, false
		}
		if tokens[cursor].kind != graphQLTokenName {
			return cursor, false
		}
		colon := nextSignificantGraphQLToken(tokens, cursor)
		if colon >= len(tokens) || tokens[colon].kind != ':' {
			return colon, false
		}
		valueIndex := nextSignificantGraphQLToken(tokens, colon)
		next, _, code := inspectKnownGraphQLValue(
			tokens,
			valueIndex,
			func(string) bool { return false },
			false,
			0,
			maxDepth,
			budget,
		)
		if code != "" || next <= valueIndex {
			return next, false
		}
		arguments++
		cursor = significantGraphQLTokenAtOrAfter(tokens, next)
	}
	if arguments > 0 &&
		cursor < len(tokens) &&
		tokens[cursor].kind == ')' {
		return significantGraphQLTokenAtOrAfter(tokens, cursor+1), true
	}
	return index, false
}

func consumeGraphQLValidationBudget(budget *int) bool {
	if budget == nil || *budget <= 0 {
		return false
	}
	*budget--
	return true
}

func validKnownGraphQLLexicalStructure(
	query []byte,
	tokens []graphQLToken,
) bool {
	stack := make([]byte, 0, 16)
	for _, token := range tokens {
		switch token.kind {
		case graphQLTokenString:
			if token.start < 0 || token.start >= len(query) ||
				query[token.start] != '"' {
				return false
			}
		case graphQLTokenOther:
			if token.text != "..." && !validGraphQLNumberToken(token.text) {
				return false
			}
		case '(', '[', '{':
			stack = append(stack, token.kind)
		case ')', ']', '}':
			if len(stack) == 0 ||
				!matchingGraphQLDelimiter(stack[len(stack)-1], token.kind) {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

func validGraphQLNumberToken(value string) bool {
	if value == "" {
		return false
	}
	payload := []byte(value)
	if !graphQLNumberStart(payload, 0) {
		return false
	}
	end, err := graphQLNumberEnd(payload, 0)
	return err == nil && end == len(payload)
}

func matchingGraphQLDelimiter(open, close byte) bool {
	switch open {
	case '(':
		return close == ')'
	case '[':
		return close == ']'
	case '{':
		return close == '}'
	default:
		return false
	}
}

func significantGraphQLTokenAtOrAfter(tokens []graphQLToken, index int) int {
	for index < len(tokens) && tokens[index].kind == graphQLTokenComment {
		index++
	}
	return index
}

func graphQLSensitiveVariableHasDefault(tokens []graphQLToken, nameIndex int) bool {
	colon := nextSignificantGraphQLToken(tokens, nameIndex)
	if colon >= len(tokens) || tokens[colon].kind != ':' {
		return false
	}
	bracketDepth := 0
	for index := nextSignificantGraphQLToken(tokens, colon); index < len(tokens); index = nextSignificantGraphQLToken(tokens, index) {
		switch tokens[index].kind {
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '=':
			if bracketDepth == 0 &&
				nextSignificantGraphQLToken(tokens, index) < len(tokens) {
				// GraphQL defaults are constant inline values. Even when the
				// source type is numeric, boolean, enum, list, or object, turning
				// it into a string reference would change wire semantics.
				return true
			}
		case '$', ')', '{':
			if bracketDepth == 0 {
				return false
			}
		}
	}
	return false
}

func previousSignificantGraphQLToken(tokens []graphQLToken, index int) int {
	for index--; index >= 0; index-- {
		if tokens[index].kind != graphQLTokenComment {
			return index
		}
	}
	return -1
}

func nextSignificantGraphQLToken(tokens []graphQLToken, index int) int {
	for index++; index < len(tokens); index++ {
		if tokens[index].kind != graphQLTokenComment {
			return index
		}
	}
	return len(tokens)
}

var errGraphQLTokenLimit = errors.New("GraphQL token limit exceeded")
var errGraphQLWorkLimit = errors.New("GraphQL scan work limit exceeded")

// Redact shares allocation and scan allowances across every nested query and
// repeated lexer pass. Other projection modes retain their per-document limits.
func lexGraphQLWithOptions(query []byte, options Options) ([]graphQLToken, error) {
	if options.Mode != Redact {
		return lexGraphQL(query, options.Limits.MaxValues)
	}
	if !reserveStructuredRedactionBytes(options, len(query)) {
		return nil, errGraphQLWorkLimit
	}
	maxTokens := min(options.Limits.MaxValues, options.structuredRedactionBudget.remainingValues)
	return lexGraphQLWithReservation(query, maxTokens, func() bool {
		return reserveStructuredRedactionValues(options, 1)
	})
}

func lexGraphQL(query []byte, maxTokens int) ([]graphQLToken, error) {
	return lexGraphQLWithReservation(query, maxTokens, nil)
}

func lexGraphQLWithReservation(query []byte, maxTokens int, reserve func() bool) ([]graphQLToken, error) {
	tokens := make([]graphQLToken, 0, min(len(query)/4, maxTokens))
	admitToken := func() bool {
		return len(tokens) < maxTokens && (reserve == nil || reserve())
	}
	appendToken := func(token graphQLToken) error {
		if len(tokens) >= maxTokens {
			return errGraphQLTokenLimit
		}
		tokens = append(tokens, token)
		return nil
	}
	for index := 0; index < len(query); {
		switch query[index] {
		case ' ', '\t', '\r', '\n', ',':
			index++
			continue
		case '#':
			if !admitToken() {
				return nil, errGraphQLTokenLimit
			}
			start := index + 1
			index = start
			for index < len(query) && query[index] != '\r' && query[index] != '\n' {
				index++
			}
			if err := appendToken(graphQLToken{
				kind:  graphQLTokenComment,
				text:  string(query[start:index]),
				start: start,
				end:   index,
			}); err != nil {
				return nil, err
			}
			continue
		}
		if index+3 <= len(query) &&
			query[index] == 0xef && query[index+1] == 0xbb && query[index+2] == 0xbf {
			index += 3
			continue
		}
		if !admitToken() {
			return nil, errGraphQLTokenLimit
		}
		if graphQLNumberStart(query, index) {
			start := index
			next, err := graphQLNumberEnd(query, index)
			if err != nil {
				return nil, err
			}
			index = next
			if err := appendToken(graphQLToken{
				kind:  graphQLTokenOther,
				text:  string(query[start:index]),
				start: start,
				end:   index,
			}); err != nil {
				return nil, err
			}
			continue
		}
		if graphQLNameStart(query[index]) {
			start := index
			index++
			for index < len(query) && graphQLNameContinue(query[index]) {
				index++
			}
			if err := appendToken(graphQLToken{
				kind:  graphQLTokenName,
				text:  string(query[start:index]),
				start: start,
				end:   index,
			}); err != nil {
				return nil, err
			}
			continue
		}
		if query[index] == '"' || query[index] == '\'' {
			start := index
			next, err := scanGraphQLString(query, index)
			if err != nil {
				return nil, err
			}
			if err := appendToken(graphQLToken{
				kind:  graphQLTokenString,
				text:  graphQLStringSemanticValue(query[start:next]),
				start: start,
				end:   next,
			}); err != nil {
				return nil, err
			}
			index = next
			continue
		}
		kind := query[index]
		switch kind {
		case '$', ':', '=', '[', ']', '(', ')', '{', '}', '!', '@', '|', '&':
			if err := appendToken(graphQLToken{
				kind:  kind,
				start: index,
				end:   index + 1,
			}); err != nil {
				return nil, err
			}
			index++
		default:
			start := index
			index++
			for index < len(query) &&
				!graphQLIgnoredByte(query[index]) &&
				!graphQLPunctuation(query[index]) &&
				!graphQLNameStart(query[index]) &&
				query[index] != '"' && query[index] != '\'' {
				index++
			}
			if err := appendToken(graphQLToken{
				kind:  graphQLTokenOther,
				text:  strings.TrimSpace(string(query[start:index])),
				start: start,
				end:   index,
			}); err != nil {
				return nil, err
			}
		}
	}
	return tokens, nil
}

func graphQLNumberStart[T ~string | ~[]byte](query T, index int) bool {
	if index < 0 || index >= len(query) {
		return false
	}
	if query[index] >= '0' && query[index] <= '9' {
		return true
	}
	return query[index] == '-' &&
		index+1 < len(query) &&
		query[index+1] >= '0' && query[index+1] <= '9'
}

func graphQLNumberEnd[T ~string | ~[]byte](query T, index int) (int, error) {
	start := index
	if query[index] == '-' {
		index++
	}
	switch {
	case index >= len(query):
		return 0, errors.New("GraphQL number is incomplete")
	case query[index] == '0':
		index++
		if index < len(query) && query[index] >= '0' && query[index] <= '9' {
			return 0, errors.New("GraphQL number has a leading zero")
		}
	case query[index] >= '1' && query[index] <= '9':
		for index < len(query) && query[index] >= '0' && query[index] <= '9' {
			index++
		}
	default:
		return 0, errors.New("GraphQL number is malformed")
	}
	if index < len(query) && query[index] == '.' {
		index++
		fractionStart := index
		for index < len(query) &&
			query[index] >= '0' && query[index] <= '9' {
			index++
		}
		if index == fractionStart {
			return 0, errors.New("GraphQL number fraction is incomplete")
		}
	}
	if index < len(query) && (query[index] == 'e' || query[index] == 'E') {
		index++
		if index < len(query) &&
			(query[index] == '+' || query[index] == '-') {
			index++
		}
		exponentStart := index
		for index < len(query) &&
			query[index] >= '0' && query[index] <= '9' {
			index++
		}
		if index == exponentStart {
			return 0, errors.New("GraphQL number exponent is incomplete")
		}
	}
	if index < len(query) &&
		(graphQLNameContinue(query[index]) ||
			query[index] == '.' ||
			query[index] == '+' ||
			query[index] == '-' ||
			query[index] == '!') {
		return 0, errors.New("GraphQL number continuation is malformed")
	}
	if index <= start {
		return 0, errors.New("GraphQL number is empty")
	}
	return index, nil
}

func graphQLStringSemanticValue(token []byte) string {
	if len(token) >= 6 &&
		string(token[:3]) == `"""` &&
		string(token[len(token)-3:]) == `"""` {
		value := string(token[3 : len(token)-3])
		value = strings.ReplaceAll(value, `\"""`, `"""`)
		return graphQLBlockStringValue(value)
	}
	if len(token) >= 2 && token[0] == '"' {
		var decoded string
		if json.Unmarshal(token, &decoded) == nil {
			return decoded
		}
	}
	if len(token) >= 2 && token[0] == '\'' {
		if decoded, err := strconv.Unquote(string(token)); err == nil {
			return decoded
		}
	}
	if len(token) >= 2 {
		return string(token[1 : len(token)-1])
	}
	return string(token)
}

func graphQLBlockStringValue(value string) string {
	lines := graphQLBlockStringLines(value)
	commonIndent := -1
	for index := 1; index < len(lines); index++ {
		indent, blank := graphQLBlockStringIndent(lines[index])
		if blank {
			continue
		}
		if commonIndent < 0 || indent < commonIndent {
			commonIndent = indent
		}
	}
	if commonIndent > 0 {
		for index := 1; index < len(lines); index++ {
			if len(lines[index]) <= commonIndent {
				lines[index] = ""
				continue
			}
			lines[index] = lines[index][commonIndent:]
		}
	}
	for len(lines) > 0 {
		_, blank := graphQLBlockStringIndent(lines[0])
		if !blank {
			break
		}
		lines = lines[1:]
	}
	for len(lines) > 0 {
		_, blank := graphQLBlockStringIndent(lines[len(lines)-1])
		if !blank {
			break
		}
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func graphQLBlockStringLines(value string) []string {
	lines := make([]string, 0, strings.Count(value, "\n")+1)
	start := 0
	for index := 0; index < len(value); index++ {
		if value[index] != '\r' && value[index] != '\n' {
			continue
		}
		lines = append(lines, value[start:index])
		if value[index] == '\r' &&
			index+1 < len(value) &&
			value[index+1] == '\n' {
			index++
		}
		start = index + 1
	}
	return append(lines, value[start:])
}

func graphQLBlockStringIndent(line string) (indent int, blank bool) {
	for indent < len(line) &&
		(line[indent] == ' ' || line[indent] == '\t') {
		indent++
	}
	return indent, indent == len(line)
}

func scanGraphQLString(query []byte, start int) (int, error) {
	quote := query[start]
	if quote == '"' && start+3 <= len(query) &&
		query[start] == '"' && query[start+1] == '"' && query[start+2] == '"' {
		for index := start + 3; index < len(query); {
			if index+4 <= len(query) && query[index] == '\\' &&
				query[index+1] == '"' && query[index+2] == '"' && query[index+3] == '"' {
				index += 4
				continue
			}
			if index+3 <= len(query) &&
				query[index] == '"' && query[index+1] == '"' && query[index+2] == '"' {
				return index + 3, nil
			}
			index++
		}
		return 0, errors.New("GraphQL block string is unterminated")
	}
	for index := start + 1; index < len(query); index++ {
		switch query[index] {
		case '\\':
			index++
			if index >= len(query) {
				return 0, errors.New("GraphQL string escape is incomplete")
			}
		case quote:
			return index + 1, nil
		case '\r', '\n':
			return 0, errors.New("GraphQL string contains a line break")
		}
	}
	return 0, errors.New("GraphQL string is unterminated")
}

func graphQLNameStart(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z'
}

func graphQLNameContinue(value byte) bool {
	return graphQLNameStart(value) || value >= '0' && value <= '9'
}

func graphQLIgnoredByte(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', ',', '#':
		return true
	default:
		return false
	}
}

func graphQLPunctuation(value byte) bool {
	switch value {
	case '$', ':', '=', '[', ']', '(', ')', '{', '}', '!', '@', '|', '&':
		return true
	default:
		return false
	}
}
