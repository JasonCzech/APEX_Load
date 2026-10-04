package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

const auraRecordedFramework = "recorded framework+branch/123456"

func auraImportedCorrelationCapture(t *testing.T, encodedSource bool) []byte {
	t.Helper()
	const token = "recorded token+branch/123456"
	const fwuid = auraRecordedFramework
	inner, _ := json.Marshal(map[string]string{"recordId": "record-business", "echo": token})
	message, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "fixture;a", "descriptor": "fixture", "params": map[string]string{"input": string(inner), "accountId": "account-business"}}}})
	ctx, _ := json.Marshal(map[string]string{"fwuid": fwuid, "mode": "PROD"})
	response, _ := json.Marshal(map[string]string{"access_token": token, "fwuid": fwuid})
	mime := "application/json"
	if encodedSource {
		response = []byte("aura.token=" + url.QueryEscape(token) + "&aura.context=" + url.QueryEscape(string(ctx)))
		mime = "application/x-www-form-urlencoded"
	}
	headers := []any{map[string]string{"name": "X-Encoded", "value": url.QueryEscape(token)}}
	if !encodedSource {
		headers = append(headers, map[string]string{"name": "X-Raw", "value": token})
	}
	body := "untouched=%2f&message=" + url.QueryEscape(string(message)) + "&aura.context=" + url.QueryEscape(string(ctx)) + "&aura.token=" + url.QueryEscape(token)
	entry := func(method, path string, headers []any, body string, response []byte, mime string) map[string]any {
		request := map[string]any{"method": method, "url": "https://fixture.my.salesforce.com" + path, "httpVersion": "HTTP/1.1", "headers": headers}
		if body != "" {
			request["postData"] = map[string]string{"mimeType": "application/x-www-form-urlencoded", "text": body}
		}
		return map[string]any{"startedDateTime": "2026-10-04T00:00:00Z", "time": 1, "request": request, "response": map[string]any{"status": 200, "httpVersion": "HTTP/1.1", "headers": []any{}, "content": map[string]string{"mimeType": mime, "text": string(response)}}}
	}
	capture, err := json.Marshal(map[string]any{"log": map[string]any{"version": "1.2", "entries": []any{
		entry("GET", "/bootstrap", []any{}, "", response, mime),
		entry("POST", "/aura?access_token="+url.QueryEscape(token), headers, body, []byte(`{"actions":[{"id":"fixture;a","state":"SUCCESS"}]}`), "application/json"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return capture
}

// This uses both public import producers, real inferred extractors, two guided
// business edits, rotating bootstrap outputs, and the real loopback transport.
// It deliberately keeps encoded URL/header consumers beside decoded JSON ones.
func TestAuraParameterizedImportedCorrelationsReplay(t *testing.T) {
	for _, producer := range []string{"HAR", "Salesforce native"} {
		for _, encodedSource := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/encoded_source_%t", producer, encodedSource), func(t *testing.T) {
				capture := auraImportedCorrelationCapture(t, encodedSource)
				infer := producer == "HAR"
				converted, err := har.Convert(capture, har.ConvertOptions{InferExtractors: &infer})
				if err != nil {
					t.Fatal(err)
				}
				script := converted.Script
				if producer == "Salesforce native" {
					review, err := salesforce.Analyze(script, salesforce.AnalyzeOptions{BuildNative: true, HARData: capture})
					if err != nil || review.NativePreview == nil || review.NativePreview.Script == nil {
						t.Fatalf("native import failed: %v", err)
					}
					script = review.NativePreview.Script
				}
				if len(script.Actions) != 2 || len(script.Actions[0].Extractors) == 0 || !strings.Contains(script.Actions[1].Body, "{{var.") {
					t.Fatal("fixture did not produce automatic correlations")
				}
				// Native import recognizes framework IDs in an aura.context form
				// response; its JSON bootstrap extractor does not recognize bare fwuid.
				// Verify the pre-edit producer contract before expecting rotation.
				rotateFramework := producer != "Salesforce native" || encodedSource
				capturedForm, err := url.ParseQuery(script.Actions[1].Body)
				if err != nil {
					t.Fatal(err)
				}
				var capturedContext map[string]string
				if json.Unmarshal([]byte(capturedForm.Get("aura.context")), &capturedContext) != nil || strings.Contains(capturedContext["fwuid"], "{{var.") != rotateFramework {
					t.Fatal("fixture framework correlation support changed")
				}
				if !rotateFramework && capturedContext["fwuid"] != auraRecordedFramework {
					t.Fatal("native JSON bootstrap did not preserve captured framework context")
				}
				extractors := append([]dsl.Extractor(nil), script.Actions[0].Extractors...)
				variables := map[string]string{}
				const recordValue = "business+literal%2F quote\" slash\\ 雪 {{param.literal}}"
				const accountValue = "account+literal%26 quote\" 雪"
				for _, edit := range []struct{ key, value string }{{"recordId", recordValue}, {"accountId", accountValue}} {
					advice := paramize.Suggest(script, paramize.Options{EnableDataParams: true})
					var selected *paramize.Suggestion
					for i := range advice.Suggestions {
						s := &advice.Suggestions[i]
						if len(s.Locations) == 1 && strings.HasSuffix(s.Locations[0].DecodedFieldPath, "."+edit.key) {
							selected = s
							break
						}
					}
					if selected == nil {
						t.Fatalf("no %s guided suggestion", edit.key)
					}
					variables[selected.VarName] = edit.value
					script, _, err = paramize.ApplySuggestions(script, advice.Suggestions, map[string]bool{selected.ID: true})
					if err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(script.Actions[0].Extractors, extractors) {
					t.Fatal("business edits changed correlation extractor conversions")
				}
				var cycles, accepted, rejected atomic.Int32
				var lastMismatch atomic.Value
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					token := fmt.Sprintf("fresh token+branch/%d123456", cycles.Load())
					fwuid := fmt.Sprintf("fresh framework+branch/%d123456", cycles.Load())
					if r.URL.Path == "/bootstrap" {
						cycle := cycles.Add(1)
						token = fmt.Sprintf("fresh token+branch/%d123456", cycle)
						fwuid = fmt.Sprintf("fresh framework+branch/%d123456", cycle)
						if encodedSource {
							ctx, _ := json.Marshal(map[string]string{"fwuid": fwuid, "mode": "PROD"})
							_, _ = io.WriteString(w, "aura.token="+url.QueryEscape(token)+"&aura.context="+url.QueryEscape(string(ctx)))
						} else {
							_ = json.NewEncoder(w).Encode(map[string]string{"access_token": token, "fwuid": fwuid})
						}
						return
					}
					if !rotateFramework {
						fwuid = auraRecordedFramework
					}
					wire, _ := io.ReadAll(r.Body)
					form, formErr := url.ParseQuery(string(wire))
					var envelope struct {
						Actions []struct {
							ID     string
							Params map[string]string
						}
					}
					var ctx map[string]string
					var inner map[string]string
					valid := formErr == nil && json.Unmarshal([]byte(form.Get("message")), &envelope) == nil && len(envelope.Actions) == 1
					if valid {
						valid = json.Unmarshal([]byte(envelope.Actions[0].Params["input"]), &inner) == nil && json.Unmarshal([]byte(form.Get("aura.context")), &ctx) == nil
					}
					valid = valid && form.Get("aura.token") == token && ctx["fwuid"] == fwuid && inner["echo"] == token && inner["recordId"] == recordValue && envelope.Actions[0].Params["accountId"] == accountValue && r.URL.Query().Get("access_token") == token && r.Header.Get("X-Encoded") == url.QueryEscape(token)
					if !encodedSource {
						valid = valid && r.Header.Get("X-Raw") == token
					}
					if !valid {
						lastMismatch.Store(fmt.Sprintf("token=%t fwuid=%t echo=%t record=%t query=%t encoded_header=%t", form.Get("aura.token") == token, ctx["fwuid"] == fwuid, inner["echo"] == token, inner["recordId"] == recordValue, r.URL.Query().Get("access_token") == token, r.Header.Get("X-Encoded") == url.QueryEscape(token)))
						rejected.Add(1)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					accepted.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"actions": []any{map[string]string{"id": envelope.Actions[0].ID, "state": "SUCCESS"}}})
				}))
				defer server.Close()
				registerAuraFixtureHTTPProtocol.Do(func() {
					protocol.Register(auraFixtureHTTPProtocol, func() protocol.Protocol { return &httpprotocol.HTTP{} })
				})
				script.Protocol = auraFixtureHTTPProtocol
				script.Config.BaseURL = server.URL
				for i := range script.Actions {
					parsed, _ := url.Parse(script.Actions[i].Target)
					script.Actions[i].Target = parsed.RequestURI()
				}
				script.Actions[1].Assertions = []dsl.Assertion{{Type: "aura_actions_success"}}
				for cycle := 0; cycle < 2; cycle++ {
					report, err := ReplayScript(context.Background(), script, ReplayOptions{Variables: variables, IgnoreThinkTime: true})
					if err != nil || report == nil || !report.Success {
						t.Fatalf("import/edit/replay failed: err=%v accepted=%d rejected=%d consumers=%v", err, accepted.Load(), rejected.Load(), lastMismatch.Load())
					}
				}
				bootstrap, batch := script.Actions[0], script.Actions[1]
				second := batch
				second.Name += " second"
				script.Actions = []dsl.Action{bootstrap, {Name: "Concurrent Aura consumers", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{batch, second}}}}
				report, err := ReplayScript(context.Background(), script, ReplayOptions{Variables: variables, IgnoreThinkTime: true})
				if err != nil || report == nil || !report.Success {
					t.Fatalf("concurrent imported consumers failed: err=%v accepted=%d rejected=%d consumers=%v", err, accepted.Load(), rejected.Load(), lastMismatch.Load())
				}
				if accepted.Load() != 4 || rejected.Load() != 0 {
					t.Fatalf("rotating-token consumers: accepted=%d rejected=%d", accepted.Load(), rejected.Load())
				}
			})
		}
	}
}
