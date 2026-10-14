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
	projected, _, result, queries, err := projectJSONSpans(payload, options)
	return projected, result, queries, err
}

// projectJSONSpans is projectJSONWithQueries that also returns the byte spans
// of payload it replaced, sorted, so a caller holding the document in another
// encoding can apply them there. Spans are nil when nothing was replaced.
func projectJSONSpans(payload []byte, options Options) ([]byte, []replacement, Result, []string, error) {
	projected, result, queries, err := projectJSONDocument(payload, options)
	if projected == nil {
		return nil, nil, result, queries, err
	}
	return projected.bytes, projected.spans, result, queries, err
}

// projectedJSON is a projected document and the spans it replaced.
type projectedJSON struct {
	bytes []byte
	spans []replacement
}

func projectJSONDocument(payload []byte, options Options) (*projectedJSON, Result, []string, error) {
	unchanged := func() *projectedJSON { return &projectedJSON{bytes: append([]byte(nil), payload...)} }
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, Result{}, nil, err
	}
	if len(payload) > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return unchanged(),
				preservedOpaquePayloadResult(
					payload,
					options,
					"import_credential_review_incomplete",
				), nil, nil
		}
		return nil, rejected(options.Source, "credential_json_limit_exceeded"), nil, nil
	}
	if !reserveStructuredRedactionBytes(options, len(payload)) {
		return nil, rejected(options.Source, "credential_structured_work_limit_exceeded"), nil, nil
	}
	if !utf8.Valid(payload) {
		if options.Mode == Preserve {
			return unchanged(),
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
		return unchanged(), blocked(options.Source, "credential_json_empty"), nil, nil
	}
	if err := parser.parseValue("", "", options.jsonDepthBase, false); err != nil {
		if options.Mode == Preserve {
			result := parser.result
			result.Merge(preservedOpaquePayloadResult(
				payload,
				options,
				"credential_json_malformed",
			))
			return unchanged(), result, nil, nil
		}
		if options.Mode == Redact && parser.result.Disposition == Rejected {
			return nil, parser.result, nil, nil
		}
		if options.Mode == Redact && options.nestedJSON {
			return nil, rejected(options.Source, "credential_nested_json_rejected"), nil, nil
		}
		if containsSensitiveNameHintWithOptions(payload, options) {
			return nil, rejected(options.Source, "credential_json_malformed_sensitive"), nil, nil
		}
		if options.KnownCredentialMatcher != nil {
			return nil, rejected(options.Source, "credential_json_known_value_malformed"), nil, nil
		}
		return unchanged(), blocked(options.Source, "credential_json_malformed"), nil, nil
	}
	parser.skipSpace()
	if parser.index != len(payload) {
		if options.Mode == Preserve {
			return unchanged(),
				preservedOpaquePayloadResult(
					payload,
					options,
					"credential_json_trailing_value",
				), nil, nil
		}
		if options.Mode == Redact && options.nestedJSON {
			return nil, rejected(options.Source, "credential_nested_json_rejected"), nil, nil
		}
		if containsSensitiveNameHintWithOptions(payload[parser.index:], options) ||
			len(parser.replacements) > 0 ||
			options.KnownCredentialMatcher != nil {
			return nil, rejected(options.Source, "credential_json_trailing_value_sensitive"), nil, nil
		}
		return unchanged(), blocked(options.Source, "credential_json_trailing_value"), nil, nil
	}
	if parser.result.Disposition == Rejected {
		return nil, parser.result, nil, nil
	}
	if options.Mode == Redact && !structuredRedactionOutputWithinLimit(payload, parser.replacements, options.Limits.MaxBytes) {
		return nil, rejected(options.Source, "credential_structured_output_limit_exceeded"), nil, nil
	}
	projected := &projectedJSON{bytes: applyReplacements(payload, parser.replacements)}
	if len(parser.replacements) > 0 {
		projected.spans = parser.replacements
	}
	return projected, parser.result, parser.graphQLQueries, nil
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

// unquotedRuntimeTemplate reports the template expression that starts at the
// parser's position when it stands for a whole runtime value: one the caller
// trusts, or a variable a prior response extractor proved (a correlated
// number such as "ProjectId":{{var.corr_project_id}}). dynamic marks the
// latter, which a sensitive member still records like a quoted one.
func (parser *jsonProjectionParser) unquotedRuntimeTemplate() (template string, dynamic bool, ok bool) {
	rest := parser.payload[parser.index:]
	if !bytes.HasPrefix(rest, []byte("{{")) {
		return "", false, false
	}
	end := bytes.Index(rest[2:], []byte("}}"))
	if end < 0 {
		return "", false, false
	}
	template = string(rest[:2+end+2])
	if _, trusted := parser.options.TrustedWholeValues[strings.TrimSpace(template)]; trusted {
		return template, false, true
	}
	if kind, _ := ClassifyWholeTemplate(template, parser.options.ProvenVars); kind == TemplateDynamic {
		return template, true, true
	}
	return "", false, false
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
	if !reserveStructuredRedactionValues(parser.options, 1) {
		parser.result.Merge(rejected(parser.options.Source, "credential_structured_value_limit_exceeded"))
		return errors.New("structured value limit exceeded")
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
	if template, dynamic, ok := parser.unquotedRuntimeTemplate(); ok {
		// A converter may write a template where a value goes (a refreshed
		// client timestamp or a correlated id stays a number). It is a runtime
		// reference, not a literal, so it is kept as it is.
		parser.index += len(template)
		if dynamic && sensitiveName != "" {
			_, item, err := projectSensitiveScalar(
				template,
				sensitiveName,
				parser.options.Source,
				TargetRef{
					RequestKey: parser.options.RequestKey,
					Field:      firstNonEmpty(parser.options.TargetField, "json"),
					Pointer:    pointer,
					Index:      valueIndex,
				},
				valueIndex,
				parser.options,
			)
			if err != nil {
				return err
			}
			parser.result.Merge(item)
		}
		return nil
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
		// Recognized encoded JSON is a structured layer, not one large scalar.
		// Project its semantic leaves before consulting the known-value matcher.
		jsonLayer := parser.options.Mode == Redact && sensitiveName == "" && nestedJSONObjectOrArray(decoded)
		if parser.options.Mode == Redact && sensitiveName == "" && captureGraphQLQuery && !wholeStructuredRuntimeTemplate(decoded) {
			queryJSON, work, classifyErr := InspectQueryValueStructure(decoded, parser.options.structuredRedactionBudget.remainingBytes)
			if !reserveStructuredRedactionBytes(parser.options, work) || classifyErr != nil {
				parser.result.Merge(rejected(parser.options.Source, "credential_structured_work_limit_exceeded"))
				return nil
			}
			jsonLayer = queryJSON
		}
		if jsonLayer {
			if parser.options.nestedJSONStringDepth >= maxRedactionJSONStringDepth {
				parser.result.Merge(rejected(parser.options.Source, "credential_nested_json_depth_limit_exceeded"))
				return nil
			}
			nestedOptions := parser.options
			nestedOptions.nestedJSONStringDepth++
			nestedOptions.jsonDepthBase = depth + 1
			nestedOptions.nestedJSON = true
			var spans []replacement
			projected, item, nestedErr := projectRequestBody([]byte(decoded), "application/json", nestedOptions, &spans)
			if nestedErr != nil {
				return nestedErr
			}
			if item.Disposition == Rejected {
				parser.result.Merge(item)
				return nil
			}
			if projected == nil {
				parser.result.Merge(rejected(parser.options.Source, "credential_nested_json_rejected"))
				return nil
			}
			parser.result.Merge(item)
			if string(projected) != decoded {
				if !reserveJSONEncodedRedaction(parser.options, projected) {
					parser.result.Merge(rejected(parser.options.Source, "credential_structured_output_limit_exceeded"))
					return nil
				}
				// Write only the bytes the projection replaced, as in a form
				// value: re-encoding the whole string escaped HTML characters
				// around a credential, so the projection no longer lined up with
				// the recording.
				if spliced, ok := spliceJSONString(string(parser.payload[valueStart:end]), decoded, string(projected), spans); ok {
					for _, span := range spliced {
						parser.replacements = append(parser.replacements, replacement{start: valueStart + span.start, end: valueStart + span.end, value: span.value})
					}
					return nil
				}
				encoded, marshalErr := json.Marshal(string(projected))
				if marshalErr != nil {
					return errors.New("projected nested JSON string could not be encoded")
				}
				parser.replacements = append(parser.replacements, replacement{start: valueStart, end: end, value: encoded})
			}
			return nil
		}
		if captureGraphQLQuery {
			parser.graphQLQueries = append(parser.graphQLQueries, decoded)
			executableQuery := false
			if sensitiveName == "" && parser.options.KnownCredentialMatcher != nil {
				var inspectErr error
				executableQuery, inspectErr = graphQLExecutableSource([]byte(decoded), parser.options)
				if errors.Is(inspectErr, errGraphQLWorkLimit) {
					parser.result.Merge(rejected(parser.options.Source, "credential_structured_work_limit_exceeded"))
					return nil
				}
				if errors.Is(inspectErr, errGraphQLTokenLimit) {
					parser.result.Merge(rejected(parser.options.Source, "credential_graphql_value_limit_exceeded"))
					return nil
				}
			}
			if sensitiveName == "" && parser.options.KnownCredentialMatcher != nil && executableQuery {
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
				if parser.options.Mode == Redact {
					code := jsonUnicodeEscapeCode(parser.payload[parser.index+1 : parser.index+5])
					if code >= 0xd800 && code <= 0xdbff {
						lowStart := parser.index + 5
						if lowStart+6 > len(parser.payload) || parser.payload[lowStart] != '\\' || parser.payload[lowStart+1] != 'u' ||
							!validLowJSONSurrogate(parser.payload[lowStart+2:lowStart+6]) {
							parser.result.Merge(rejected(parser.options.Source, "credential_json_invalid_surrogate"))
							return 0, "", errors.New("JSON surrogate is malformed")
						}
						parser.index += 11
						continue
					}
					if code >= 0xdc00 && code <= 0xdfff {
						parser.result.Merge(rejected(parser.options.Source, "credential_json_invalid_surrogate"))
						return 0, "", errors.New("JSON surrogate is malformed")
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

var jsonPointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

func escapeJSONPointer(value string) string {
	return jsonPointerEscaper.Replace(value)
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func isJSONIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}
