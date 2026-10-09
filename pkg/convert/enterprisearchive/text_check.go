package enterprisearchive

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

// web_reg_find (and the deprecated web_find) become HTTP text assertions:
// text_contains/text_not_contains for literal text and text_matches/
// text_not_matches for the text flags and prefix/suffix forms, over the
// surface Search= names.

// sourceTextCheck is a web_reg_find text check compiled for APEX.
type sourceTextCheck struct {
	value           string // literal text, or an RE2 pattern when pattern is true
	pattern         bool
	caseInsensitive bool
}

// alnumClasses maps the ALNUM flags to the class "^" matches.
var alnumClasses = map[string]string{
	"ALNUM": "[A-Za-z0-9]", "ALNUMIC": "[A-Za-z0-9]", "ALNUMLC": "[a-z0-9]", "ALNUMUC": "[A-Z0-9]",
}

// compileSourceTextCheck turns Text= (or TextPfx=/TextSfx=) and its flags
// into a literal or an RE2 pattern. Parameters stay {{...}} templates; the
// runtime matches their values literally. It returns a reason when the check
// cannot be expressed.
func compileSourceTextCheck(fn ParsedFunction) (sourceTextCheck, string) {
	text, flags := sourceFlaggedArgument(fn, "Text")
	if text == "" {
		prefix, prefixFlags := sourceFlaggedArgument(fn, "TextPfx")
		suffix, suffixFlags := sourceFlaggedArgument(fn, "TextSfx")
		if prefix == "" && suffix == "" {
			return sourceTextCheck{}, "it has no Text, TextPfx or TextSfx argument"
		}
		check := sourceTextCheck{pattern: true}
		for _, flag := range append(prefixFlags, suffixFlags...) {
			if strings.EqualFold(strings.TrimSpace(flag), "IC") {
				check.caseInsensitive = true
			} else if strings.TrimSpace(flag) != "" {
				return sourceTextCheck{}, fmt.Sprintf("TextPfx/TextSfx uses the /%s flag, which is not expressible yet", strings.TrimSpace(flag))
			}
		}
		// LoadRunner finds text that starts with the prefix and ends with the
		// suffix; the shortest such span is enough to decide a match.
		check.value = quoteTextCheckLiteral(convertParamReferences(prefix)) + "(?s:.*?)" + quoteTextCheckLiteral(convertParamReferences(suffix))
		return check, ""
	}
	check := sourceTextCheck{}
	var digits, binary, regex bool
	alnum := ""
	for _, raw := range flags {
		flag := strings.ToUpper(strings.TrimSpace(raw))
		switch {
		case flag == "":
		case flag == "IC":
			check.caseInsensitive = true
		case flag == "DIG":
			digits = true
		case alnumClasses[flag] != "":
			alnum = alnumClasses[flag]
		case flag == "BIN":
			binary = true
		case flag == "REGEXP":
			regex = true
		default:
			return sourceTextCheck{}, fmt.Sprintf("it uses the unknown text flag /%s", strings.TrimSpace(raw))
		}
	}
	text = convertParamReferences(text)
	if regex {
		if digits || alnum != "" || binary {
			return sourceTextCheck{}, "it combines /REGEXP with wildcard or binary flags"
		}
		if _, err := regexp.Compile(text); err != nil {
			return sourceTextCheck{}, fmt.Sprintf("its /REGEXP pattern is not RE2-compatible (%v)", err)
		}
		check.value, check.pattern = text, true
		return check, ""
	}
	if binary {
		decoded, err := bodyencoding.DecodeCEscapes(text)
		if err != nil {
			return sourceTextCheck{}, fmt.Sprintf("its /BIN text has an escape the importer does not recognize (%v)", err)
		}
		for _, b := range decoded {
			if b >= 0x80 {
				return sourceTextCheck{}, "its /BIN text has bytes above 0x7F, which an RE2 text check cannot match exactly"
			}
		}
		text = string(decoded)
	}
	if !digits && alnum == "" && !binary {
		check.value = text
		return check, ""
	}
	var pattern strings.Builder
	for _, span := range splitTemplateSpans(text) {
		if span.template {
			pattern.WriteString(span.text)
			continue
		}
		for _, r := range span.text {
			switch {
			case digits && r == '#':
				pattern.WriteString("[0-9]")
			case alnum != "" && r == '^':
				pattern.WriteString(alnum)
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&pattern, `\x%02X`, r)
			default:
				pattern.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
	}
	check.value, check.pattern = pattern.String(), true
	return check, ""
}

type templateSpan struct {
	text     string
	template bool
}

// splitTemplateSpans splits text into literal spans and {{...}} templates.
func splitTemplateSpans(text string) []templateSpan {
	var spans []templateSpan
	for text != "" {
		open := strings.Index(text, "{{")
		if open < 0 {
			return append(spans, templateSpan{text: text})
		}
		end := strings.Index(text[open:], "}}")
		if end < 0 {
			return append(spans, templateSpan{text: text})
		}
		if open > 0 {
			spans = append(spans, templateSpan{text: text[:open]})
		}
		spans = append(spans, templateSpan{text: text[open : open+end+2], template: true})
		text = text[open+end+2:]
	}
	return spans
}

// quoteTextCheckLiteral quotes literal spans for RE2 and keeps templates.
func quoteTextCheckLiteral(text string) string {
	var quoted strings.Builder
	for _, span := range splitTemplateSpans(text) {
		if span.template {
			quoted.WriteString(span.text)
		} else {
			quoted.WriteString(regexp.QuoteMeta(span.text))
		}
	}
	return quoted.String()
}

// sourceTextCheckSource maps web_reg_find's Search= to a text assertion
// source. Body and Noresource read the main body; ALL reads headers and body.
func sourceTextCheckSource(search string) string {
	switch strings.ToLower(strings.TrimSpace(search)) {
	case "headers", "header":
		return "headers"
	case "all":
		return "all"
	default:
		return ""
	}
}

// convertFindAssertionWithFindings converts a text check and returns the
// review findings for anything it cannot reproduce exactly.
func convertFindAssertionWithFindings(fn ParsedFunction) (*dsl.Assertion, *dsl.Extractor, SourceFunctionSupportMode, string, []importreview.Finding) {
	check, reason := compileSourceTextCheck(fn)
	if reason != "" {
		finding := promotionFinding(fn, textCheckUnsupportedReasonCode,
			fmt.Sprintf("%s %q cannot be checked because %s, so the check is not executed.", fn.Name, fn.StepName, reason))
		return nil, nil, SourceFunctionSupportReviewOnly, fmt.Sprintf("line %d: %s '%s' was not imported because %s; it requires review", fn.LineNumber, fn.Name, fn.StepName, reason), []importreview.Finding{finding}
	}

	mode := SourceFunctionSupportExecutable
	search := firstNonEmptyArg(fn, "Search")
	source := sourceTextCheckSource(search)
	var warnings []string
	var findings []importreview.Finding
	if source == "all" {
		// ALL also spans downloaded resources, which a request's assertions
		// do not read; the headers and body are checked exactly.
		mode = SourceFunctionSupportReviewOnly
		warnings = append(warnings, fmt.Sprintf("line %d: web_reg_find Search=ALL is checked against the main response's headers and body; downloaded resources require review", fn.LineNumber))
		findings = append(findings, promotionFinding(fn, textCheckNarrowedReasonCode,
			fmt.Sprintf("%s %q searches headers, body and resources (Search=ALL); APEX checks the main response's headers and body only.", fn.Name, fn.StepName)))
	}

	var assertion *dsl.Assertion
	failMode := strings.ToLower(firstNonEmptyArg(fn, "Fail"))
	saveCount := firstNonEmptyArg(fn, "SaveCount")
	// OpenText semantics are special when SaveCount is present: omitting Fail
	// makes this a non-failing count operation, so no assertion is added.
	if saveCount == "" || failMode != "" {
		assertionType := "text_contains"
		if check.pattern {
			assertionType = "text_matches"
		}
		if failMode == "found" {
			assertionType = strings.Replace(assertionType, "text_", "text_not_", 1)
		}
		if source == "" && !check.pattern {
			// Keep the long-standing body forms for plain body checks.
			assertionType = map[string]string{"text_contains": "body_contains", "text_not_contains": "body_not_contains"}[assertionType]
		}
		assertion = &dsl.Assertion{Type: assertionType, Value: check.value, CaseInsensitive: check.caseInsensitive, Source: source}
	}

	var saveCountExtractor *dsl.Extractor
	if saveCount != "" {
		if name := canonicalSourceParameterName(saveCount); name != "" {
			expression := check.value
			if !check.pattern {
				expression = regexp.QuoteMeta(check.value)
			}
			saveCountExtractor = &dsl.Extractor{
				Name:       name,
				Source:     map[string]string{"": "body", "headers": "header", "all": "all"}[source],
				Type:       "regex_count",
				Expression: caseInsensitiveRegexPrefix(check.caseInsensitive) + expression,
			}
		}
	}

	return assertion, saveCountExtractor, mode, strings.Join(warnings, "; "), findings
}
