package enterprisearchive

import (
	"strings"
	"testing"
)

func TestLegacyActionParserAcceptsBoundedLargeLines(t *testing.T) {
	largeValue := strings.Repeat("x", 96*1024)
	parsed, err := parseActionFileLegacy("Action.c", `Action()
{
web_custom_request("large", "Body=`+largeValue+`", LAST);
}`)
	if err != nil {
		t.Fatalf("parseActionFileLegacy: %v", err)
	}
	if len(parsed.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(parsed.Functions))
	}
}

func TestLegacyActionParserIgnoresTerminatorTextInsideStrings(t *testing.T) {
	parsed, err := parseActionFileLegacy("Action.c", `Action()
{
web_custom_request("request",
    "Body=literal ); text",
    "URL=https://example.test/orders",
    LAST);
}`)
	if err != nil {
		t.Fatalf("parseActionFileLegacy: %v", err)
	}
	if len(parsed.Functions) != 1 || !strings.Contains(parsed.Functions[0].RawBlock, "URL=https://example.test/orders") {
		t.Fatalf("parsed functions = %#v, request terminated inside string", parsed.Functions)
	}
}
