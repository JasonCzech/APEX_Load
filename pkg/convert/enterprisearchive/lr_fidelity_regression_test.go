package enterprisearchive

import (
	"context"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExtraResourcesTerminateItemDataFourShapes(t *testing.T) {
	for _, resources := range []string{`EXTRARES, LAST`, `EXTRARES, "Url=https://example.test/one.js", ENDITEM, LAST`, `EXTRARES, "Url=/one.js", "Referer=", ENDITEM, LAST`, `EXTRARES, "Url=/one.js", ENDITEM, "Url=/two.js", "Referer=https://example.test/page", ENDITEM, LAST`} {
		t.Run(resources, func(t *testing.T) {
			source := fmt.Sprintf(`Action(){web_submit_data("form","Action=https://example.test/form","Method=POST","EncType=application/x-www-form-urlencoded",ITEMDATA,"Name=a","Value=one",ENDITEM,"Name=a","Value=two " "words {known}",ENDITEM,%s);return 0;}`, resources)
			parsed, err := ParseActionFile("Action.c", source)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			a := result.Script.Actions[0]
			if metadataString(a.Metadata, "source_itemdata_encoding") == "review_required" {
				t.Fatal("EXTRARES falsely blocked form")
			}
			fields := parseFormBodyFields(a.Body)
			if len(fields) != 2 || fields[0].Value != "one" || fields[1].Value != "two+words+{{var.known}}" {
				t.Fatalf("ordered fields=%+v", fields)
			}
			expected := strings.Count(resources, `"Url=`)
			actual := 0
			if refs, ok := a.Params["resources"].([]any); ok {
				actual = len(refs)
			}
			if actual != expected {
				t.Fatalf("resources=%d want %d", actual, expected)
			}
		})
	}
}
func TestTransactionDelimiterDoesNotSplitPunctuation(t *testing.T) {
	names, err := splitProjectTransactionOrder(`"One, with comma__*delimiter*__Two;semi__*delimiter*__Three|pipe"`)
	if err != nil || len(names) != 3 || names[0] != "One, with comma" || names[1] != "Two;semi" || names[2] != "Three|pipe" {
		t.Fatalf("names=%q err=%v", names, err)
	}
}
func TestEncryptedProxyAndActiveAgentFidelity(t *testing.T) {
	cfg := ParseRuntimeCfg(`[WEB]
UseCustomAgent=1
CustomUserAgent=Active-Agent
BrowserUserAgent=Dormant-Agent
BrowserAcceptLanguage=en-US,en;q=0.9
ProxyUseProxy=1
ProxyHTTPHost=proxy.example.test
ProxyHTTPPort=8080
ProxyUserName=user
ProxyPassword=opaque-encrypted-value
ProxyPasswordIsEncrypted=true
`)
	if !cfg.ProxyPasswordEncrypted || strings.Contains(cfg.ProxyPass, "opaque-encrypted-value") {
		t.Fatal("encrypted credential treated as plaintext")
	}
	config := cfg.ToScriptConfig()
	if config.BrowserEmulation == nil || config.BrowserEmulation.CustomUserAgent != "Active-Agent" {
		t.Fatalf("active UA=%+v", config.BrowserEmulation)
	}
}
func TestCommentContinuationDoesNotCreateProducer(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{"sample.usr": "[General]\nType=Multi\nActiveTypes=QTWeb\n[Actions]\nAction=Action.c\n", "Action.c": `Action(){
// disabled producer \
web_reg_save_param("hidden","LB=x","RB=y",LAST);
web_url("consumer","URL=https://example.test/{hidden}",LAST);
return 0;
}`})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	findings, _ := importreview.FindingsFromScript(result.Script)
	found := false
	for _, f := range findings {
		if f.Code == "missing_producer:hidden" {
			found = true
		}
	}
	if !found {
		t.Fatal("commented producer incorrectly considered reachable")
	}
	_ = dslwalk.Walk(result.Script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil && len(ref.Action.Extractors) > 0 {
			t.Error("commented registration reactivated")
		}
		return nil
	})
}

func TestImportedFormResourceCorrelationAndVerificationReplay(t *testing.T) {
	var resources atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/seed":
			io.WriteString(w, "ERROR token=[a b&c]")
		case "/form":
			b, _ := io.ReadAll(r.Body)
			if string(b) != "field=first&field=a+b%26c" {
				t.Errorf("form bytes=%q", b)
			}
			w.Header().Set("Content-Type", "application/json")
			http.SetCookie(w, &http.Cookie{Name: "resource_cookie", Value: "ready"})
			io.WriteString(w, `{}`)
		case "/asset":
			resources.Add(1)
			if r.URL.Query().Get("token") != "a b&c" {
				t.Errorf("resource correlation=%q", r.URL.RawQuery)
			}
			if _, err := r.Cookie("resource_cookie"); err != nil {
				t.Error("resource cookie absent")
			}
			io.WriteString(w, "ERROR")
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	init, _ := ParseActionFile("vuser_init.c", `vuser_init(){web_reg_save_param("token","LB=token=[","RB=]",LAST);web_url("seed","URL=`+server.URL+`/seed",LAST);return 0;}`)
	action, _ := ParseActionFile("Action.c", `Action(){
web_convert_param("encoded","SourceString={token}","SourceEncoding=PLAIN","TargetEncoding=URL",LAST);
web_global_verification("ID=error","Text=ERROR","Search=Body","Fail=Found",LAST);
lr_start_transaction("Submit");
web_submit_data("form","Action=`+server.URL+`/form","Method=POST",ITEMDATA,"Name=field","Value=first",ENDITEM,"Name=field","Value={token}",ENDITEM,EXTRARES,"Url=`+server.URL+`/asset?token={encoded}",ENDITEM,LAST);
lr_end_transaction("Submit",LR_AUTO);return 0;}`)
	init.Phase = "init"
	result, err := Convert([]*ParsedAction{init, action}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result.Script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(true)
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Success || resources.Load() != 1 {
		t.Fatalf("verification/resource outcome: success=%v resources=%d", report.Success, resources.Load())
	}
	initOK, verificationFailed, transactionFailed := false, false, false
	for _, event := range report.Events {
		if event.Phase == "init" && strings.HasSuffix(event.Target, "/seed") {
			initOK = event.Success
		}
		if strings.Contains(event.ErrorMessage, "source verification") {
			verificationFailed = true
		}
		if event.SourceTransaction == "Submit" && !event.Success {
			transactionFailed = true
		}
	}
	if !initOK || !verificationFailed || !transactionFailed {
		t.Fatalf("init=%v verification=%v transaction=%v events=%+v", initOK, verificationFailed, transactionFailed, report.Events)
	}
}

func TestGlobalVerificationUnknownFailureModeStaysBlocked(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action(){web_global_verification("ID=check","Text=error","Fail=Unexpected",LAST);return 0;}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := importreview.FindingsFromScript(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, f := range findings {
		if f.Gate == importreview.GateExecution {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("unsupported failure semantics silently enabled: %+v", result.Script.Actions)
	}
}
