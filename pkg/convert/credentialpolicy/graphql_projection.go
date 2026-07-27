package credentialpolicy

import (
	"errors"
	"strings"
)

type graphQLToken struct {
	kind byte
	text string
}

const (
	graphQLTokenName   byte = 'N'
	graphQLTokenString byte = 'S'
	graphQLTokenOther  byte = 'V'
)

// scanGraphQLCredentialLiteral performs a bounded lexical scan of GraphQL
// source. GraphQL permits arbitrary ignored whitespace, commas, and # comments,
// so substring-distance checks are not a safe way to identify argument values.
// A lexer is sufficient here: the projector does not translate source, it only
// needs to reject credential-bearing inline values and variable defaults
// that cannot be safely rewritten without a source/schema-aware AST.
func scanGraphQLCredentialLiteral(query []byte, options Options) (bool, string) {
	tokens, err := lexGraphQL(query, options.Limits.MaxValues)
	if err != nil {
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
		if index > 0 && tokens[index-1].kind == '$' {
			if graphQLSensitiveVariableHasDefault(tokens, index) {
				return true, ""
			}
			continue
		}
		if index+2 >= len(tokens) || tokens[index+1].kind != ':' {
			continue
		}
		valueKind := tokens[index+2].kind
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

func graphQLSensitiveVariableHasDefault(tokens []graphQLToken, nameIndex int) bool {
	if nameIndex+1 >= len(tokens) || tokens[nameIndex+1].kind != ':' {
		return false
	}
	bracketDepth := 0
	for index := nameIndex + 2; index < len(tokens); index++ {
		switch tokens[index].kind {
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '=':
			if bracketDepth == 0 && index+1 < len(tokens) {
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

var errGraphQLTokenLimit = errors.New("GraphQL token limit exceeded")

func lexGraphQL(query []byte, maxTokens int) ([]graphQLToken, error) {
	tokens := make([]graphQLToken, 0, min(len(query)/4, maxTokens))
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
			for index < len(query) && query[index] != '\r' && query[index] != '\n' {
				index++
			}
			continue
		}
		if index+3 <= len(query) &&
			query[index] == 0xef && query[index+1] == 0xbb && query[index+2] == 0xbf {
			index += 3
			continue
		}
		if graphQLNameStart(query[index]) {
			start := index
			index++
			for index < len(query) && graphQLNameContinue(query[index]) {
				index++
			}
			if err := appendToken(graphQLToken{
				kind: graphQLTokenName,
				text: string(query[start:index]),
			}); err != nil {
				return nil, err
			}
			continue
		}
		if query[index] == '"' || query[index] == '\'' {
			next, err := scanGraphQLString(query, index)
			if err != nil {
				return nil, err
			}
			if err := appendToken(graphQLToken{kind: graphQLTokenString}); err != nil {
				return nil, err
			}
			index = next
			continue
		}
		kind := query[index]
		switch kind {
		case '$', ':', '=', '[', ']', '(', ')', '{', '}', '!', '@', '|', '&':
			if err := appendToken(graphQLToken{kind: kind}); err != nil {
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
				kind: graphQLTokenOther,
				text: strings.TrimSpace(string(query[start:index])),
			}); err != nil {
				return nil, err
			}
		}
	}
	return tokens, nil
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
