package correlate

import "testing"

func TestExactQueryEncodingValidatorBodyRegexUsesRuntimeMatchSemantics(t *testing.T) {
	const body = "token=wrong-value&token=target%2Fvalue"
	validator := NewExactQueryEncodingValidator()
	plain := Correlation{
		Source:      "body",
		ExtractType: "regex",
		Expression:  `(?:^|&)token=([^&]+)`,
	}
	if validator.Matches(1, body, nil, plain, "target%2Fvalue") {
		t.Fatal("plain regex accepted a later capture even though the runtime extracts the first match")
	}
	if !validator.Matches(1, body, nil, plain, "wrong-value") {
		t.Fatal("plain regex did not accept the runtime's first capture")
	}

	ordinal := plain
	ordinal.ExtractType = "regex_ordinal"
	ordinal.Expression = encodeRegexOrdinalExpression(plain.Expression, 2)
	if !validator.Matches(1, body, nil, ordinal, "target%2Fvalue") {
		t.Fatal("regex_ordinal did not validate its explicit second capture")
	}
}

func TestExactQueryEncodingValidatorHeaderExtractionMirrorsHTTPRuntime(t *testing.T) {
	headers := map[string]string{
		"X-Other": "unrelated",
		"X-Token": "first-value\ntarget%2Fvalue",
	}
	validator := NewExactQueryEncodingValidator()

	named := Correlation{Source: "header", ExtractType: "header", Expression: "x-token"}
	if !validator.Matches(2, "", headers, named, "first-value") {
		t.Fatal("named header validation did not accept the runtime's first occurrence")
	}
	if validator.Matches(2, "", headers, named, "first-value\ntarget%2Fvalue") {
		t.Fatal("named header validation compared the snapshot's joined occurrences instead of runtime Header.Get semantics")
	}

	regex := Correlation{
		Source:      "header",
		ExtractType: "regex",
		Expression:  `(?im)^X-Token:\s*([^\r\n]+)`,
	}
	if validator.Matches(2, "", headers, regex, "target%2Fvalue") {
		t.Fatal("header regex accepted a later occurrence instead of the runtime's first capture")
	}
	if !validator.Matches(2, "", headers, regex, "first-value") {
		t.Fatal("header regex did not match the first flattened header occurrence")
	}
}

func TestExactQueryEncodingValidatorFailsClosedOnWorkLimit(t *testing.T) {
	validator := NewExactQueryEncodingValidator()
	validator.maxSearchBytes = 3
	corr := Correlation{Source: "body", ExtractType: "regex", Expression: `(value)`}
	if validator.Matches(3, "value", nil, corr, "value") {
		t.Fatal("validator matched after exceeding its byte budget")
	}
	if !validator.Exhausted() {
		t.Fatal("validator did not report deterministic budget exhaustion")
	}
}

func TestExactQueryEncodingValidatorRejectsUnsupportedBodyExtractor(t *testing.T) {
	validator := NewExactQueryEncodingValidator()
	corr := Correlation{Source: "body", ExtractType: "jsonpath", Expression: "$.token"}
	if validator.Matches(4, `{"unrelated":"target%2Fvalue"}`, nil, corr, "target%2Fvalue") {
		t.Fatal("unsupported body extractor was authorized by an unrelated literal occurrence")
	}
}

func TestExactQueryEncodingValidatorBoundsFlattenedHeaderAllocation(t *testing.T) {
	validator := NewExactQueryEncodingValidator()
	validator.maxSearchBytes = 12
	corr := Correlation{Source: "header", ExtractType: "regex", Expression: `(?m)^Long-Header: (.+)`}
	headers := map[string]string{"Long-Header": "one\ntwo"}
	if validator.Matches(5, "", headers, corr, "one") {
		t.Fatal("validator matched after flattened headers exceeded its allocation budget")
	}
	if !validator.Exhausted() {
		t.Fatal("flattened-header allocation overflow did not exhaust the validator")
	}
}
