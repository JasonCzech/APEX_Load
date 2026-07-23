package correlate

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

const (
	regexOrdinalExpressionPrefix           = "__apexload_regex_ordinal__:"
	maxExactQueryEncodingSearchBytes int64 = 64 << 20
	maxExactQueryEncodingMatches           = 4096
)

type regexOrdinalExpression struct {
	Expression string `json:"expression"`
	Ordinal    int    `json:"ordinal"`
}

func encodeRegexOrdinalExpression(expression string, ordinal int) string {
	payload, err := json.Marshal(regexOrdinalExpression{Expression: expression, Ordinal: ordinal})
	if err != nil {
		return expression
	}
	return regexOrdinalExpressionPrefix + string(payload)
}

func decodeRegexOrdinalExpression(expression string) (string, int, bool) {
	if !strings.HasPrefix(expression, regexOrdinalExpressionPrefix) {
		return expression, 1, strings.TrimSpace(expression) != ""
	}
	var config regexOrdinalExpression
	if err := json.Unmarshal([]byte(strings.TrimPrefix(expression, regexOrdinalExpressionPrefix)), &config); err != nil {
		return "", 0, false
	}
	if strings.TrimSpace(config.Expression) == "" || config.Ordinal <= 0 {
		return "", 0, false
	}
	return config.Expression, config.Ordinal, true
}

type exactQueryRegexKey struct {
	sourceID   int
	surface    string
	expression string
}

type exactQueryRegexResult struct {
	values   []string
	overflow bool
	valid    bool
}

// ExactQueryEncodingValidator performs bounded, cached source checks for
// url_decode correlations. A source ID must identify one immutable response for
// the lifetime of the validator. Reusing the cached regex captures avoids one
// full response-body scan per correlation when many candidates share a source.
// Exhaustion fails closed: Matches returns false, so callers leave the consumer
// literal untouched and require manual review.
type ExactQueryEncodingValidator struct {
	regexResults     map[exactQueryRegexKey]exactQueryRegexResult
	flattenedHeaders map[int]string
	searchBytes      int64
	maxSearchBytes   int64
	maxMatches       int
	exhausted        bool
}

func NewExactQueryEncodingValidator() *ExactQueryEncodingValidator {
	return &ExactQueryEncodingValidator{
		regexResults:     make(map[exactQueryRegexKey]exactQueryRegexResult),
		flattenedHeaders: make(map[int]string),
		maxSearchBytes:   maxExactQueryEncodingSearchBytes,
		maxMatches:       maxExactQueryEncodingMatches,
	}
}

// Exhausted reports whether a prior check exceeded the validator's deterministic
// byte or match budget.
func (v *ExactQueryEncodingValidator) Exhausted() bool {
	return v != nil && v.exhausted
}

// Matches reports whether the correlation's source captures queryEncoded with
// exactly that wire spelling. Body regexes are evaluated with the same first or
// explicit-ordinal match semantics as the HTTP extractor runtime.
func (v *ExactQueryEncodingValidator) Matches(sourceID int, responseBody string, responseHeaders map[string]string, correlation Correlation, queryEncoded string) bool {
	if v == nil || queryEncoded == "" || v.exhausted {
		return false
	}
	source := correlation.Source
	if source == "" || source == "body" {
		extractType := correlation.ExtractType
		if extractType == "regex" || extractType == "regex_ordinal" {
			expression := correlation.Expression
			ordinal := 1
			if extractType == "regex_ordinal" {
				var ok bool
				expression, ordinal, ok = decodeRegexOrdinalExpression(expression)
				if !ok || ordinal > maxCorrelationRegexOrdinal {
					return false
				}
			}
			result := v.regexCaptures(sourceID, "body", responseBody, expression)
			if !result.valid || result.overflow {
				return false
			}
			return ordinal <= len(result.values) && result.values[ordinal-1] == queryEncoded
		}
		// The validator cannot safely approximate JSONPath, XPath, CSS, or
		// boundary extraction with a substring search: an unrelated occurrence
		// could otherwise authorize an encoded rewrite that the configured
		// extractor never produces. Generated url_decode candidates use bounded
		// regex/regex_ordinal sources, so unsupported source types fail closed.
		return false
	}
	if source != "header" {
		return false
	}

	extractType := correlation.ExtractType
	switch extractType {
	case "", "header":
		matches := 0
		value := ""
		for key, candidate := range responseHeaders {
			if strings.EqualFold(key, correlation.Expression) {
				matches++
				value, _, _ = strings.Cut(candidate, "\n")
			}
		}
		return matches == 1 && value == queryEncoded
	case "regex":
		flattened, ok := v.flattenHeaders(sourceID, responseHeaders)
		if !ok {
			return false
		}
		result := v.regexCaptures(sourceID, "headers", flattened, correlation.Expression)
		return result.valid && !result.overflow && len(result.values) > 0 && result.values[0] == queryEncoded
	default:
		return false
	}
}

func (v *ExactQueryEncodingValidator) regexCaptures(sourceID int, surface, body, expression string) exactQueryRegexResult {
	key := exactQueryRegexKey{sourceID: sourceID, surface: surface, expression: expression}
	if cached, ok := v.regexResults[key]; ok {
		return cached
	}
	if !v.charge(len(body)) {
		result := exactQueryRegexResult{overflow: true}
		v.regexResults[key] = result
		return result
	}
	compiled, err := regexp.Compile(expression)
	if err != nil {
		result := exactQueryRegexResult{}
		v.regexResults[key] = result
		return result
	}
	limit := v.maxMatches
	if limit <= 0 {
		v.exhausted = true
		result := exactQueryRegexResult{overflow: true}
		v.regexResults[key] = result
		return result
	}
	matches := compiled.FindAllStringSubmatch(body, limit+1)
	if len(matches) > limit {
		v.exhausted = true
		result := exactQueryRegexResult{overflow: true}
		v.regexResults[key] = result
		return result
	}
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, match[1])
		} else {
			values = append(values, "")
		}
	}
	result := exactQueryRegexResult{values: values, valid: true}
	v.regexResults[key] = result
	return result
}

func (v *ExactQueryEncodingValidator) flattenHeaders(sourceID int, headers map[string]string) (string, bool) {
	if flattened, ok := v.flattenedHeaders[sourceID]; ok {
		return flattened, true
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var flattenedBytes int64
	for _, key := range keys {
		forEachJoinedHeaderValue(headers[key], func(value string) bool {
			lineBytes := int64(len(key) + len(value) + 3)
			if lineBytes > v.maxSearchBytes-v.searchBytes-flattenedBytes {
				v.exhausted = true
				return false
			}
			flattenedBytes += lineBytes
			return true
		})
		if v.exhausted {
			return "", false
		}
	}
	var builder strings.Builder
	builder.Grow(int(flattenedBytes))
	for _, key := range keys {
		forEachJoinedHeaderValue(headers[key], func(value string) bool {
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(value)
			builder.WriteByte('\n')
			return true
		})
	}
	flattened := builder.String()
	v.flattenedHeaders[sourceID] = flattened
	return flattened, true
}

func forEachJoinedHeaderValue(joined string, visit func(string) bool) {
	remaining := joined
	for {
		value, rest, found := strings.Cut(remaining, "\n")
		if !visit(value) || !found {
			return
		}
		remaining = rest
	}
}

func (v *ExactQueryEncodingValidator) charge(bytes int) bool {
	if bytes < 0 || int64(bytes) > v.maxSearchBytes-v.searchBytes {
		v.exhausted = true
		return false
	}
	v.searchBytes += int64(bytes)
	return true
}
