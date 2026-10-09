package enterprisearchive

import (
	"reflect"
	"strings"
	"testing"
)

func parseSingleRequest(t *testing.T, call string) ParsedFunction {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n\t"+call+"\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range parsed.Functions {
		if requestArgumentFunctions[fn.Name] {
			return fn
		}
	}
	t.Fatalf("no request function parsed from %s", call)
	return ParsedFunction{}
}

func TestClassifyRequestArguments(t *testing.T) {
	tests := []struct {
		name    string
		call    string
		body    []string
		gated   []string
		review  []string
		unknown []string
		agent   string
	}{
		{
			name: "mapped and inert attributes are quiet",
			call: `web_url("home", "URL=https://x.test/", "TargetFrame=", "Resource=0", "RecContentType=text/html", "Referer=", "Snapshot=t1.inf", "Mode=HTML", LAST);`,
		},
		{
			name: "body variants",
			call: `web_custom_request("b", "URL=https://x.test/", "Method=POST", "BodyBinary=\\x00", "BodyUnicode=a", "BodyFilePath=p.json", LAST);`,
			body: []string{"BodyBinary", "BodyUnicode", "BodyFilePath"},
		},
		{
			name:  "wire-changing attributes",
			call:  `web_custom_request("c", "URL=https://x.test/", "Method=POST", "ContentEncoding=gzip", "ExtraResBaseDir=https://cdn.test/", "Body=x", LAST);`,
			gated: []string{"ContentEncoding", "ExtraResBaseDir"},
		},
		{
			name:   "side-effect attributes",
			call:   `web_custom_request("r", "URL=https://x.test/", "Method=GET", "ResponseTime=rt", LAST);`,
			review: []string{"ResponseTime"},
		},
		{
			name:  "EncodeAtSign=No is gated, Yes matches the encoder",
			call:  `web_submit_data("s", "Action=https://x.test/", "Method=POST", "EncodeAtSign=NO", ITEMDATA, "Name=e", "Value=a@b.test", ENDITEM, LAST);`,
			gated: []string{"EncodeAtSign=No"},
		},
		{
			name: "EncodeAtSign=Yes",
			call: `web_submit_data("s", "Action=https://x.test/", "Method=POST", "EncodeAtSign=YES", ITEMDATA, "Name=e", "Value=a@b.test", ENDITEM, LAST);`,
		},
		{
			name:    "unknown attributes are reported, item data and headers are not",
			call:    `web_rest("p", "URL=https://x.test/", "Method=POST", "Frobnicate=1", "Body=x", HEADERS, "Name=X-Test", "Value=1", ENDHEADER, LAST);`,
			unknown: []string{"Frobnicate"},
		},
		{
			name:  "user agent is mapped",
			call:  `web_url("ua", "URL=https://x.test/", "UserAgent=Agent/1.0", LAST);`,
			agent: "Agent/1.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := classifyRequestArguments(parseSingleRequest(t, tt.call))
			check := func(label string, got, want []string) {
				if len(got) == 0 && len(want) == 0 {
					return
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %v, want %v", label, got, want)
				}
			}
			check("body", report.body, tt.body)
			check("gated", report.gated, tt.gated)
			check("review", report.review, tt.review)
			check("unknown", report.unknown, tt.unknown)
			if report.userAgnt != tt.agent {
				t.Errorf("user agent = %q, want %q", report.userAgnt, tt.agent)
			}
		})
	}
}

func TestConcatenateSourceBodySegments(t *testing.T) {
	fn := parseSingleRequest(t, `web_custom_request("p", "URL=https://x.test/", "Method=POST", "Body=a=1", "Body=&b=2", "Body=&c={P}", LAST);`)
	if fn.Body != "a=1&b=2&c={P}" {
		t.Fatalf("body = %q", fn.Body)
	}
	single := parseSingleRequest(t, `web_custom_request("p", "URL=https://x.test/", "Method=POST", "Body=only", LAST);`)
	if single.Body != "only" {
		t.Fatalf("single body = %q", single.Body)
	}
}

func TestDefaultCustomRequestContentType(t *testing.T) {
	tests := []struct {
		call string
		want string
	}{
		{`web_custom_request("p", "URL=https://x.test/", "Method=POST", "Body=x", LAST);`, "application/x-www-form-urlencoded"},
		{`web_custom_request("p", "URL=https://x.test/", "Method=POST", "EncType=", "Body=x", LAST);`, ""},
		{`web_custom_request("p", "URL=https://x.test/", "Method=POST", "EncType=text/xml", "Body=x", LAST);`, ""},
		{`web_custom_request("p", "URL=https://x.test/", "Method=PUT", "Body=x", LAST);`, ""},
		{`web_rest("p", "URL=https://x.test/", "Method=POST", "Body=x", LAST);`, ""},
	}
	for _, tt := range tests {
		if got := defaultCustomRequestContentType(parseSingleRequest(t, tt.call)); got != tt.want {
			t.Errorf("%s: default content type = %q, want %q", tt.call, got, tt.want)
		}
	}
}

func TestRequestArgumentReviewGatesAndWarnsOncePerScript(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_url("a", "URL=https://x.test/a", "Frobnicate=1", LAST);
	web_url("b", "URL=https://x.test/b", "Frobnicate=2", "Wibble=3", LAST);
	web_custom_request("c", "URL=https://x.test/c", "Method=POST", "EncType=application/octet-stream", "BodyBinary=\\q", LAST);
	web_url("d", "URL=https://x.test/d", "UserAgent=Agent/1.0", LAST);`)
	actions := result.Script.Actions
	if len(actions) != 4 {
		t.Fatalf("actions = %d, want 4", len(actions))
	}
	if got, _ := actions[1].Metadata["source_unmodelled_args"].([]string); !reflect.DeepEqual(got, []string{"Frobnicate", "Wibble"}) {
		t.Fatalf("source_unmodelled_args = %v", actions[1].Metadata["source_unmodelled_args"])
	}
	if blockers, _ := actions[2].Metadata["execution_blockers"].([]string); !reflect.DeepEqual(blockers, []string{requestBodyVariantReasonCode}) {
		t.Fatalf("a BodyBinary escape the importer cannot decode must keep the body gate, got %v", actions[2].Metadata["execution_blockers"])
	}
	if actions[3].Headers["User-Agent"] != "Agent/1.0" {
		t.Fatalf("User-Agent header = %v", actions[3].Headers)
	}
	warnings := 0
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "attributes the importer does not model") {
			warnings++
			if !strings.Contains(warning, "Frobnicate (2)") || !strings.Contains(warning, "Wibble (1)") {
				t.Fatalf("warning = %q", warning)
			}
		}
	}
	if warnings != 1 {
		t.Fatalf("want exactly one aggregated warning, got %d in %v", warnings, result.Warnings)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_custom_request", SourceFunctionSupportReviewOnly)
}
