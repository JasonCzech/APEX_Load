package sample

// correlate_redact.go — split from converter.go (same package, behavior-preserving).

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func detectCorrelations(exchanges []exchange) []Correlation {
	observations := make([]correlate.Observation, 0, len(exchanges))
	for index, ex := range exchanges {
		observation := correlate.Observation{
			ID:             index + 1,
			RequestURL:     ex.URL,
			RequestBody:    ex.Body,
			RequestHeaders: ex.Headers,
		}
		if ex.Response != nil {
			observation.ResponseBody = ex.Response.Body
			observation.ResponseHeaders = ex.Response.Headers
		}
		observations = append(observations, observation)
	}
	shared := correlate.Detect(observations)
	correlations := make([]Correlation, 0, len(shared))
	for _, corr := range shared {
		correlations = append(correlations, Correlation{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			Value:       corr.Value,
			SampleValue: corr.SampleValue,
			FoundInStep: corr.FoundInID,
			UsedInSteps: corr.UsedInIDs,
		})
	}
	return correlations
}

func detectEncodedCorrelationValues(exchanges []exchange, correlations []Correlation) map[string]bool {
	encodedValues := map[string]bool{}
	if len(exchanges) == 0 || len(correlations) == 0 {
		return encodedValues
	}
	for _, corr := range correlations {
		value := corr.Value
		if value == "" {
			value = corr.SampleValue
		}
		if value == "" || len(corr.UsedInSteps) == 0 {
			continue
		}
		surfaces := make([]string, 0)
		for _, step := range corr.UsedInSteps {
			if step <= 0 || step > len(exchanges) {
				continue
			}
			surfaces = append(surfaces, exchangeSurfaces(exchanges[step-1])...)
		}
		if correlate.DetectEncodedValueUsage(value, surfaces) {
			encodedValues[value] = true
		}
	}
	return encodedValues
}

func exchangeSurfaces(ex exchange) []string {
	surfaces := []string{ex.URL, ex.Body}
	for _, value := range ex.Headers {
		surfaces = append(surfaces, value)
	}
	for _, header := range ex.HeaderList {
		surfaces = append(surfaces, header.Value)
	}
	return surfaces
}

func cloneHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneHeaderList(input dsl.HeaderList) dsl.HeaderList {
	if len(input) == 0 {
		return nil
	}
	cloned := make(dsl.HeaderList, len(input))
	copy(cloned, input)
	return cloned
}

func cloneMetadata(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func sanitizeHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return input
	}
	for key := range input {
		if paramize.SensitiveHeaderKey(key) && !isTemplatedValue(input[key]) {
			input[key] = "{{secret." + sanitizeIdentifier(strings.ToLower(key)) + "}}"
		}
	}
	return input
}

func sanitizeHeaderList(input dsl.HeaderList) dsl.HeaderList {
	if len(input) == 0 {
		return input
	}
	for index := range input {
		if paramize.SensitiveHeaderKey(input[index].Name) && !isTemplatedValue(input[index].Value) {
			input[index].Value = "{{secret." + sanitizeIdentifier(strings.ToLower(input[index].Name)) + "}}"
		}
	}
	return input
}

func sanitizeBody(body string) string {
	return paramize.SanitizeBodySecrets(body)
}

// redactSourceReviewSecrets scrubs credentials from the preserved source text
// shown in the Source Comparison panel. This runs regardless of the
// sanitize_sensitive option: that option controls whether the generated
// actions keep raw values, not whether secrets persist in script metadata.
// Returns the set of file names whose content was redacted.
func redactSourceReviewSecrets(files map[string]string, exchanges []exchange) map[string]bool {
	redacted := make(map[string]bool, len(files))
	for name, content := range files {
		next, changed := redactExchangeSecrets(content, exchanges)
		if strings.HasSuffix(strings.ToLower(name), ".har") {
			if scrubbed, err := scrub.HAR([]byte(next), scrub.Policy{}); err == nil {
				next = string(scrubbed)
				changed = true
			}
		}
		if changed {
			files[name] = next
			redacted[name] = true
		}
	}
	return redacted
}

func redactExchangeSecrets(content string, exchanges []exchange) (string, bool) {
	original := content
	for _, ex := range exchanges {
		content = redactSensitiveHeaderValues(content, ex.Headers)
		if ex.BodyEncoding == "" {
			content = redactBodySecrets(content, ex.Body)
		}
		if ex.Response != nil {
			content = redactSensitiveHeaderValues(content, ex.Response.Headers)
			content = redactBodySecrets(content, ex.Response.Body)
		}
	}
	// Catch-all for secret fields that did not round-trip through a parsed
	// exchange verbatim (re-serialized bodies, query strings, ...).
	content = paramize.SanitizeBodySecrets(content)
	return content, content != original
}

func redactSensitiveHeaderValues(content string, headers map[string]string) string {
	for key, value := range headers {
		if !paramize.SensitiveHeaderKey(key) || strings.TrimSpace(value) == "" {
			continue
		}
		content = replaceRawAndJSONEscaped(content, value, "[REDACTED]")
	}
	return content
}

func redactBodySecrets(content string, body string) string {
	if strings.TrimSpace(body) == "" {
		return content
	}
	sanitized := paramize.SanitizeBodySecrets(body)
	if sanitized == body {
		return content
	}
	return replaceRawAndJSONEscaped(content, body, sanitized)
}

// replaceRawAndJSONEscaped replaces both the literal value and its
// JSON-string-escaped form, so redaction reaches secrets embedded inside JSON
// containers such as HAR header entries and postData text.
func replaceRawAndJSONEscaped(content string, value string, replacement string) string {
	content = strings.ReplaceAll(content, value, replacement)
	escapedValue := jsonEscapeString(value)
	if escapedValue != "" && escapedValue != value {
		content = strings.ReplaceAll(content, escapedValue, jsonEscapeString(replacement))
	}
	return content
}

// jsonEscapeString returns the JSON string encoding of value without the
// surrounding quotes and without HTML escaping, matching how HAR files embed
// string values.
func jsonEscapeString(value string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	encoded := strings.TrimRight(buf.String(), "\n")
	if len(encoded) < 2 {
		return ""
	}
	return encoded[1 : len(encoded)-1]
}

func applyCorrelationVars(value string, vars map[string]string, encodedValues map[string]bool) string {
	return correlate.ApplyVarsWithEncodedValues(value, vars, encodedValues)
}

func applyCorrelationVarsToHeaders(headers map[string]string, vars map[string]string, encodedValues map[string]bool) map[string]string {
	return correlate.ApplyVarsToHeadersWithEncodedValues(headers, vars, encodedValues)
}

func applyCorrelationVarsToHeaderList(headers dsl.HeaderList, vars map[string]string, encodedValues map[string]bool) dsl.HeaderList {
	if len(headers) == 0 {
		return headers
	}
	for index := range headers {
		headers[index].Name = correlate.ApplyVarsWithEncodedValues(headers[index].Name, vars, encodedValues)
		headers[index].Value = correlate.ApplyVarsWithEncodedValues(headers[index].Value, vars, encodedValues)
	}
	return headers
}

func extractorTypeForCorrelation(corr Correlation) string {
	if corr.ExtractType != "" {
		return corr.ExtractType
	}
	return extractorTypeForSource(corr.Source)
}

func extractorTypeForSource(source string) string {
	if source == "header" {
		return "header"
	}
	return "jsonpath"
}

func sanitizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "value"
	}
	replacer := regexp.MustCompile(`[^A-Za-z0-9_]+`)
	value = replacer.ReplaceAllString(strings.ToLower(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "value"
	}
	return value
}

func isTemplatedValue(value string) bool {
	return strings.Contains(value, "{{") && strings.Contains(value, "}}")
}
