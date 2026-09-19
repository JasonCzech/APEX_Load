package enterprisearchive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
)

// A minimized, synthetic reproduction of the NEXT31 archive's parameterized
// authority, grouped page/form requests, and .prm/.dat-backed runtime values.
func parameterizedURLArchive(t *testing.T, host, source string) []byte {
	t.Helper()
	return buildSourceZip(t, map[string]string{
		"sample.usr": "[General]\nType=Web\n[Actions]\nAction1=Action.c\n",
		"Action.c":   source,
		"sample.prm": `[parameter:server_url]
Type=Table
Table=hosts.dat
ColumnName=server_url
Delimiter=","
SelectNextRow=Sequential
GenerateNewVal=EachIteration
`,
		"hosts.dat": "server_url\n" + host + "\n",
	})
}

func TestParameterizedURLProvenanceGroupedFormReplay(t *testing.T) {
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flow/form.jsp" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		switch r.Method {
		case "GET":
			gets.Add(1)
			io.WriteString(w, "token=[a b&c]")
		case "POST":
			posts.Add(1)
			body, _ := io.ReadAll(r.Body)
			if string(body) != "token=a+b%26c&field=first&field=second" {
				t.Errorf("form bytes = %q", body)
			}
			io.WriteString(w, "ok")
		default:
			t.Errorf("method = %s", r.Method)
		}
	}))
	defer server.Close()
	source := `Action(){
lr_start_transaction("Submit");
web_reg_save_param("token","LB=token=[","RB=]",LAST);
web_url("form.jsp","URL=http://{server_url}/flow/form.jsp",LAST);
web_submit_form("form.jsp_2",ITEMDATA,
"Name=token","Value={token}",ENDITEM,
"Name=field","Value=first",ENDITEM,
"Name=field","Value=second",ENDITEM,LAST);
lr_end_transaction("Submit",LR_AUTO);
return 0;
}`
	result, err := ConvertZip(parameterizedURLArchive(t, strings.TrimPrefix(server.URL, "http://"), source), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
		t.Fatalf("blockers = %v", result.FidelityReport.Blockers)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("validation = %v", issues)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_url", "actions[1].params.requests[0]", "converted")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_submit_form", "actions[1].params.requests[1]", "converted")
	sourceRetained := false
	for _, file := range result.SourceReview.SourceFiles {
		if file.Path == "Action.c" {
			sourceRetained = strings.Contains(file.Content, `URL=http://{server_url}/flow/form.jsp`)
		}
	}
	if !sourceRetained {
		t.Fatal("source review lost the original LoadRunner URL spelling")
	}
	count := 0
	err = dslwalk.Walk(result.Script, func(ref dslwalk.SurfaceRef) error {
		if ref.Kind != dslwalk.HTTPSequenceRequestSurface {
			return nil
		}
		count++
		metadata := ref.Metadata()
		if metadata["original_url"] != "http://{{param.server_url}}/flow/form.jsp" {
			t.Errorf("provenance URL = %v", metadata["original_url"])
		}
		wantLine := 4
		if count == 2 {
			wantLine = 5
		}
		if metadata["source_file"] != "Action.c" || metadata["source_line"] != wantLine {
			t.Errorf("source mapping = %v", metadata)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("child requests = %d", count)
	}
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || gets.Load() != 1 || posts.Load() != 1 {
		t.Fatalf("replay success=%v GET=%d POST=%d", report.Success, gets.Load(), posts.Load())
	}
}

func TestSourceURLTemplatesAndMalformedURLsRemainDistinct(t *testing.T) {
	for _, tt := range []struct {
		name, url string
		blocked   bool
	}{
		{"host", "https://{server_url}/page", false},
		{"port", "https://example.test:{port}/page", false},
		{"embedded_host", "https://{region}.example.test/page", false},
		{"query", "https://{server_url}/page?token={token}", false},
		{"malformed_escape", "https://{server_url}/bad%zz", true},
		{"literal_space", "https://{server_url}/bad path", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseActionFile("Action.c", fmt.Sprintf(`Action(){web_url("page","URL=%s",LAST);return 0;}`, tt.url))
			if err != nil {
				t.Fatal(err)
			}
			result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{BaseURL: "https://example.test"})
			if err != nil {
				t.Fatal(err)
			}
			findings, err := importreview.FindingsFromScript(result.Script)
			if err != nil {
				t.Fatal(err)
			}
			malformed := false
			for _, f := range findings {
				if f.Code == "credential_url_malformed" {
					malformed = true
					if f.ActionPath != "actions[0]" {
						t.Errorf("finding path = %s", f.ActionPath)
					}
				}
			}
			if malformed != tt.blocked {
				t.Fatalf("malformed=%v want=%v", malformed, tt.blocked)
			}
		})
	}
}

func TestParameterizedRecordedResourceURLs(t *testing.T) {
	source := `Action(){web_url("asset","URL=http://{server_url}/asset.js","Resource=1",LAST);return 0;}`
	result, err := ConvertZip(parameterizedURLArchive(t, "example.test", source), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
		t.Fatalf("blockers = %v", result.FidelityReport.Blockers)
	}
	if len(result.RecordedResourceURLs) != 1 || result.RecordedResourceURLs[0] != "http://{{param.server_url}}/asset.js" {
		t.Fatalf("resource URLs = %v", result.RecordedResourceURLs)
	}
}

func TestMakeRelativeRequiresURLComponentBoundary(t *testing.T) {
	for _, tt := range []struct{ raw, base, want string }{
		{"https://example.test/one", "https://example.test", "/one"},
		{"https://example.test.evil.test/one", "https://example.test", "https://example.test.evil.test/one"},
		{"https://example.test:8443/one", "https://example.test", "https://example.test:8443/one"},
		{"https://example.test@other.test/one", "https://example.test", "https://example.test@other.test/one"},
		{"http://{server_url}:8080/one", "http://{server_url}", "http://{server_url}:8080/one"},
		{"http://{server_url}/one", "http://{server_url}", "/one"},
		{"https://example.test", "https://example.test", "/"},
		{"https://example.test/one?x=a%2Fb", "https://example.test", "/one?x=a%2Fb"},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			if got := makeRelative(tt.raw, tt.base); got != tt.want {
				t.Fatalf("target = %q want %q", got, tt.want)
			}
		})
	}
}
