package credentialpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
)

// ProjectJSON projects sensitive string members without decoding the document
// into maps. This retains formatting, member order, duplicate occurrences,
// numeric spellings, and every nonsensitive byte.
func ProjectJSON(payload []byte, options Options) ([]byte, Result, error) {
	projected, result, _, err := projectJSONWithQueries(payload, options)
	return projected, result, err
}

// projectJSONWithQueries uses the same duplicate-preserving parser for
// projection and GraphQL-envelope discovery. Comparing decoded top-level keys
// avoids raw-regex bypasses such as "qu\u0065ry", while retaining every
// duplicate query occurrence in source order.
func projectJSONWithQueries(payload []byte, options Options) ([]byte, Result, []string, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, Result{}, nil, err
	}
	if len(payload) > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"import_credential_review_incomplete",
				), nil, nil
		}
		return nil, rejected(options.Source, "credential_json_limit_exceeded"), nil, nil
	}
	if !utf8.Valid(payload) {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"credential_json_invalid_utf8",
				), nil, nil
		}
		return nil, rejected(options.Source, "credential_json_invalid_utf8"), nil, nil
	}
	parser := jsonProjectionParser{
		payload: payload,
		options: options,
		result:  exactResult(),
	}
	parser.skipSpace()
	if parser.index == len(payload) {
		return append([]byte(nil), payload...), blocked(options.Source, "credential_json_empty"), nil, nil
	}
	if err := parser.parseValue("", "", 0, false); err != nil {
		if options.Mode == Preserve {
			result := parser.result
			result.Merge(preservedOpaquePayloadResult(
				payload,
				options,
				"credential_json_malformed",
			))
			return append([]byte(nil), payload...), result, nil, nil
		}
		if containsSensitiveNameHintWithOptions(payload, options) {
			return nil, rejected(options.Source, "credential_json_malformed_sensitive"), nil, nil
		}
		if options.KnownCredentialMatcher != nil {
			return nil, rejected(options.Source, "credential_json_known_value_malformed"), nil, nil
		}
		return append([]byte(nil), payload...), blocked(options.Source, "credential_json_malformed"), nil, nil
	}
	parser.skipSpace()
	if parser.index != len(payload) {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"credential_json_trailing_value",
				), nil, nil
		}
		if containsSensitiveNameHintWithOptions(payload[parser.index:], options) ||
			len(parser.replacements) > 0 ||
			options.KnownCredentialMatcher != nil {
			return nil, rejected(options.Source, "credential_json_trailing_value_sensitive"), nil, nil
		}
		return append([]byte(nil), payload...), blocked(options.Source, "credential_json_trailing_value"), nil, nil
	}
	if parser.result.Disposition == Rejected {
		return nil, parser.result, nil, nil
	}
	return applyReplacements(payload, parser.replacements), parser.result, parser.graphQLQueries, nil
}

type jsonProjectionParser struct {
	payload        []byte
	index          int
	values         int
	options        Options
	replacements   []replacement
	result         Result
	graphQLQueries []string
}

func (parser *jsonProjectionParser) parseValue(
	sensitiveName,
	pointer string,
	depth int,
	captureGraphQLQuery bool,
) error {
	if depth > parser.options.Limits.MaxDepth {
		if parser.options.Mode == Preserve {
			parser.result.Merge(preserved(parser.options.Source, "import_credential_review_incomplete"))
		} else {
			parser.result.Merge(rejected(parser.options.Source, "credential_json_depth_limit_exceeded"))
		}
		return errors.New("JSON depth limit exceeded")
	}
	parser.values++
	valueIndex := parser.values - 1
	if parser.values > parser.options.Limits.MaxValues {
		if parser.options.Mode == Preserve {
			parser.result.Merge(preserved(parser.options.Source, "import_credential_review_incomplete"))
		} else {
			parser.result.Merge(rejected(parser.options.Source, "credential_json_value_limit_exceeded"))
		}
		return errors.New("JSON value limit exceeded")
	}
	parser.skipSpace()
	if parser.index >= len(parser.payload) {
		return errors.New("JSON value is missing")
	}
	valueStart := parser.index
	switch parser.payload[parser.index] {
	case '{':
		if sensitiveName != "" {
			if parser.options.Mode == Preserve {
				item := preserved(parser.options.Source, "import_credential_preserved")
				item.PreservedTargets = append(item.PreservedTargets, TargetRef{
					RequestKey: parser.options.RequestKey,
					Field:      firstNonEmpty(parser.options.TargetField, "json"),
					Pointer:    pointer,
					Index:      valueIndex,
					Occurrence: valueIndex,
				})
				parser.result.Merge(item)
			} else {
				parser.result.Merge(rejected(parser.options.Source, "credential_json_sensitive_object_unsupported"))
			}
		}
		return parser.parseObject(pointer, depth+1)
	case '[':
		if sensitiveName != "" {
			if parser.options.Mode == Preserve {
				item := preserved(parser.options.Source, "import_credential_preserved")
				item.PreservedTargets = append(item.PreservedTargets, TargetRef{
					RequestKey: parser.options.RequestKey,
					Field:      firstNonEmpty(parser.options.TargetField, "json"),
					Pointer:    pointer,
					Index:      valueIndex,
					Occurrence: valueIndex,
				})
				parser.result.Merge(item)
			} else {
				parser.result.Merge(rejected(parser.options.Source, "credential_json_sensitive_array_unsupported"))
			}
		}
		return parser.parseArray(pointer, depth+1)
	case '"':
		end, decoded, err := parser.parseString()
		if err != nil {
			return err
		}
		if captureGraphQLQuery {
			parser.graphQLQueries = append(parser.graphQLQueries, decoded)
			if sensitiveName == "" &&
				parser.options.KnownCredentialMatcher != nil &&
				graphQLExecutableSource([]byte(decoded), parser.options) {
				projectedQuery, queryResult, err := projectKnownGraphQLScalars(
					[]byte(decoded),
					parser.options,
				)
				if err != nil {
					return err
				}
				parser.result.Merge(queryResult)
				if queryResult.Disposition != Rejected &&
					!bytes.Equal(projectedQuery, []byte(decoded)) {
					encoded, err := json.Marshal(string(projectedQuery))
					if err != nil {
						return errors.New("projected GraphQL JSON string could not be encoded")
					}
					parser.replacements = append(parser.replacements, replacement{
						start: valueStart,
						end:   end,
						value: encoded,
					})
				}
			} else if sensitiveName == "" &&
				parser.options.KnownCredentialMatcher != nil &&
				knownCredentialScalarMatch(parser.options, decoded) {
				encoded, err := json.Marshal(credential.Redacted)
				if err != nil {
					return errors.New("projected JSON query string could not be encoded")
				}
				parser.replacements = append(parser.replacements, replacement{
					start: valueStart,
					end:   end,
					value: encoded,
				})
				return nil
			}
		}
		if sensitiveName == "" &&
			!captureGraphQLQuery &&
			knownCredentialScalarMatch(parser.options, decoded) {
			encoded, err := json.Marshal(credential.Redacted)
			if err != nil {
				return errors.New("projected JSON string could not be encoded")
			}
			parser.replacements = append(parser.replacements, replacement{
				start: valueStart,
				end:   end,
				value: encoded,
			})
			return nil
		}
		if sensitiveName == "" {
			return nil
		}
		// Keep the exact encoded string content in the protected requirement.
		// Runtime interpolation occurs after JSON serialization, so storing the
		// wire-ready escape sequence preserves quotes, slashes, and Unicode.
		wireValue := string(parser.payload[valueStart+1 : end-1])
		projected, item, err := projectSensitiveScalar(
			wireValue,
			sensitiveName,
			parser.options.Source,
			TargetRef{
				RequestKey: parser.options.RequestKey,
				Field:      firstNonEmpty(parser.options.TargetField, "json"),
				Pointer:    pointer,
				Index:      parser.values - 1,
			},
			parser.values-1,
			parser.options,
		)
		if err != nil {
			return err
		}
		if parser.options.Mode != Preserve ||
			preserveRequiresCredentialReplacement(item) {
			encoded, err := json.Marshal(projected)
			if err != nil {
				return errors.New("projected JSON string could not be encoded")
			}
			parser.replacements = append(parser.replacements, replacement{
				start: valueStart,
				end:   end,
				value: encoded,
			})
		}
		parser.result.Merge(item)
		return nil
	case 't':
		if err := parser.consumeLiteral("true"); err != nil {
			return err
		}
	case 'f':
		if err := parser.consumeLiteral("false"); err != nil {
			return err
		}
	case 'n':
		if err := parser.consumeLiteral("null"); err != nil {
			return err
		}
	default:
		if err := parser.consumeNumber(); err != nil {
			return err
		}
	}
	if knownCredentialScalarBytesMatch(
		parser.options,
		parser.payload[valueStart:parser.index],
	) {
		parser.result.Merge(rejected(
			parser.options.Source,
			"credential_json_known_non_string_unsupported",
		))
	}
	if sensitiveName != "" {
		if parser.options.Mode == Preserve {
			item := preserved(parser.options.Source, "import_credential_preserved")
			item.PreservedTargets = append(item.PreservedTargets, TargetRef{
				RequestKey: parser.options.RequestKey,
				Field:      firstNonEmpty(parser.options.TargetField, "json"),
				Pointer:    pointer,
				Index:      valueIndex,
				Occurrence: valueIndex,
			})
			parser.result.Merge(item)
		} else {
			parser.result.Merge(rejected(parser.options.Source, "credential_json_sensitive_non_string_unsupported"))
		}
	}
	return nil
}

func (parser *jsonProjectionParser) parseObject(pointer string, depth int) error {
	parser.index++
	parser.skipSpace()
	if parser.consumeByte('}') {
		return nil
	}
	seen := make(map[string]struct{})
	memberIndex := 0
	for {
		parser.skipSpace()
		if parser.index >= len(parser.payload) || parser.payload[parser.index] != '"' {
			return errors.New("JSON object key is malformed")
		}
		_, key, err := parser.parseString()
		if err != nil {
			return err
		}
		if _, duplicate := seen[key]; duplicate {
			parser.result.Merge(blocked(parser.options.Source, "credential_json_duplicate_key"))
		}
		seen[key] = struct{}{}
		parser.skipSpace()
		if !parser.consumeByte(':') {
			return errors.New("JSON object colon is missing")
		}
		childPointer := pointer + "/" + escapeJSONPointer(key)
		sensitiveName := ""
		if governedName, sensitive := governedSensitiveName(key, parser.options); sensitive {
			sensitiveName = governedName
		}
		captureGraphQLQuery := pointer == "" && strings.EqualFold(key, "query")
		if err := parser.parseValue(sensitiveName, childPointer, depth, captureGraphQLQuery); err != nil {
			return err
		}
		memberIndex++
		parser.skipSpace()
		if parser.consumeByte('}') {
			return nil
		}
		if !parser.consumeByte(',') {
			return errors.New("JSON object delimiter is malformed")
		}
	}
}

func (parser *jsonProjectionParser) parseArray(pointer string, depth int) error {
	parser.index++
	parser.skipSpace()
	if parser.consumeByte(']') {
		return nil
	}
	index := 0
	for {
		if err := parser.parseValue("", pointer+"/"+strconv.Itoa(index), depth, false); err != nil {
			return err
		}
		index++
		parser.skipSpace()
		if parser.consumeByte(']') {
			return nil
		}
		if !parser.consumeByte(',') {
			return errors.New("JSON array delimiter is malformed")
		}
	}
}

func (parser *jsonProjectionParser) parseString() (int, string, error) {
	start := parser.index
	if !parser.consumeByte('"') {
		return 0, "", errors.New("JSON string is missing")
	}
	for parser.index < len(parser.payload) {
		value := parser.payload[parser.index]
		switch value {
		case '"':
			parser.index++
			var decoded string
			if err := json.Unmarshal(parser.payload[start:parser.index], &decoded); err != nil {
				return 0, "", errors.New("JSON string escape is malformed")
			}
			return parser.index, decoded, nil
		case '\\':
			parser.index++
			if parser.index >= len(parser.payload) {
				return 0, "", errors.New("JSON string escape is incomplete")
			}
			if parser.payload[parser.index] == 'u' {
				if parser.index+4 >= len(parser.payload) {
					return 0, "", errors.New("JSON unicode escape is incomplete")
				}
				for _, digit := range parser.payload[parser.index+1 : parser.index+5] {
					if !isHexDigit(digit) {
						return 0, "", errors.New("JSON unicode escape is malformed")
					}
				}
				parser.index += 5
				continue
			}
			if !bytes.ContainsRune([]byte(`"\/bfnrt`), rune(parser.payload[parser.index])) {
				return 0, "", errors.New("JSON escape is malformed")
			}
			parser.index++
		default:
			if value < 0x20 {
				return 0, "", errors.New("JSON string contains a control byte")
			}
			parser.index++
		}
	}
	return 0, "", errors.New("JSON string is unterminated")
}

func (parser *jsonProjectionParser) consumeLiteral(literal string) error {
	if !bytes.HasPrefix(parser.payload[parser.index:], []byte(literal)) {
		return fmt.Errorf("JSON literal is malformed")
	}
	parser.index += len(literal)
	if parser.index < len(parser.payload) && isJSONIdentifierByte(parser.payload[parser.index]) {
		return errors.New("JSON literal boundary is malformed")
	}
	return nil
}

func (parser *jsonProjectionParser) consumeNumber() error {
	start := parser.index
	if parser.consumeByte('-') && parser.index >= len(parser.payload) {
		return errors.New("JSON number is incomplete")
	}
	if parser.consumeByte('0') {
		if parser.index < len(parser.payload) && parser.payload[parser.index] >= '0' && parser.payload[parser.index] <= '9' {
			return errors.New("JSON number has a leading zero")
		}
	} else {
		if parser.index >= len(parser.payload) || parser.payload[parser.index] < '1' || parser.payload[parser.index] > '9' {
			return errors.New("JSON number is malformed")
		}
		for parser.index < len(parser.payload) && parser.payload[parser.index] >= '0' && parser.payload[parser.index] <= '9' {
			parser.index++
		}
	}
	if parser.consumeByte('.') {
		fractionStart := parser.index
		for parser.index < len(parser.payload) && parser.payload[parser.index] >= '0' && parser.payload[parser.index] <= '9' {
			parser.index++
		}
		if parser.index == fractionStart {
			return errors.New("JSON fraction is incomplete")
		}
	}
	if parser.index < len(parser.payload) && (parser.payload[parser.index] == 'e' || parser.payload[parser.index] == 'E') {
		parser.index++
		if parser.index < len(parser.payload) && (parser.payload[parser.index] == '+' || parser.payload[parser.index] == '-') {
			parser.index++
		}
		exponentStart := parser.index
		for parser.index < len(parser.payload) && parser.payload[parser.index] >= '0' && parser.payload[parser.index] <= '9' {
			parser.index++
		}
		if parser.index == exponentStart {
			return errors.New("JSON exponent is incomplete")
		}
	}
	if parser.index == start {
		return errors.New("JSON value is malformed")
	}
	return nil
}

func (parser *jsonProjectionParser) skipSpace() {
	for parser.index < len(parser.payload) {
		switch parser.payload[parser.index] {
		case ' ', '\t', '\r', '\n':
			parser.index++
		default:
			return
		}
	}
}

func (parser *jsonProjectionParser) consumeByte(value byte) bool {
	if parser.index >= len(parser.payload) || parser.payload[parser.index] != value {
		return false
	}
	parser.index++
	return true
}

func escapeJSONPointer(value string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(value)
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func isJSONIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}
