package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

func concurrentAuraReviewGroup(t *testing.T, representation, policy string) dsl.Action {
	t.Helper()
	body := `{"actions":[{"id":"a","descriptor":"fixture","params":{"literal":"{{var.first}}"}}]}`
	encoding := ""
	if representation == "direct JSON" {
		if policy != "active" {
			body = strings.ReplaceAll(body, "{{var.first}}", `\u007b\u007bvar.first\u007d\u007d`)
		}
	} else {
		body = "message=" + url.QueryEscape(body)
		if policy == "active" {
			body = strings.ReplaceAll(body, url.QueryEscape("{{var.first}}"), "{{var.first}}")
		}
		if representation == "base64 form" {
			body = base64.StdEncoding.EncodeToString([]byte(body))
			encoding = "base64"
		}
	}
	contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, encoding, "", nil, aurapayload.MaxWorkBytes)
	if err != nil || (len(contract.LiteralOccurrences) == 1) == (policy == "active") {
		t.Fatalf("fixture occurrence contract: %v, %v", contract, err)
	}
	if policy == "malformed" {
		contract.LiteralOccurrences[0].ContextID = strings.Repeat("0", 64)
	}
	producer := dsl.Action{Name: "Producer", Type: "GET", Target: "/producer", Extractors: []dsl.Extractor{{Name: "first", Source: "body", Type: "jsonpath", Expression: "$.first", OnMiss: "fail"}}}
	consumer := dsl.Action{Name: "Aura literal", Type: "POST", Target: "/aura", Body: body, BodyEncoding: encoding, Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}, Metadata: map[string]any{
		dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportAuraJSONTemplateValueEncoding,
		dsl.ImportAuraTemplateContractMetadataKey:         contract,
	}}
	return dsl.Action{Name: "Concurrent Aura", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{producer, consumer}}}
}

func TestConcurrentHTTPAuraContractsLoadAndReplay(t *testing.T) {
	for _, representation := range []string{"plain form", "base64 form", "direct JSON"} {
		for _, metadataPolicy := range []string{"canonical policy", "padded policy"} {
			for _, policy := range []string{"literal", "active", "malformed"} {
				for _, runtime := range []string{"load", "replay"} {
					t.Run(representation+"/"+metadataPolicy+"/"+policy+"/"+runtime, func(t *testing.T) {
						var traffic, accepted atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							traffic.Add(1)
							w.Header().Set("Content-Type", "application/json")
							if r.URL.Path == "/producer" {
								io.WriteString(w, `{"first":"fresh-output"}`)
								return
							}
							body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
							message := string(body)
							if representation != "direct JSON" {
								var form url.Values
								form, err = url.ParseQuery(message)
								message = form.Get("message")
							}
							var decoded struct {
								Actions []struct {
									Params map[string]string `json:"params"`
								} `json:"actions"`
							}
							if err != nil || json.Unmarshal([]byte(message), &decoded) != nil || len(decoded.Actions) != 1 || decoded.Actions[0].Params["literal"] != "{{var.first}}" {
								w.WriteHeader(http.StatusConflict)
								return
							}
							accepted.Add(1)
							io.WriteString(w, `{"ok":true}`)
						}))
						t.Cleanup(server.Close)
						group := concurrentAuraReviewGroup(t, representation, policy)
						if metadataPolicy == "padded policy" {
							children := group.Params["requests"].([]dsl.Action)
							children[1].Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] = " \t" + dsl.ImportAuraJSONTemplateValueEncoding + "\n "
						}
						var success bool
						var final string
						if runtime == "load" {
							vu := newConcurrentVU(t, server.URL)
							vu.Script.Actions = []dsl.Action{group}
							vu.Variables["first"] = "prior-output"
							result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{})
							if err != nil || result == nil {
								t.Fatalf("load group: result=%v err=%v", result, err)
							}
							success, final = result.Success, vu.Variables["first"]
						} else {
							script := &dsl.Script{Name: "Concurrent Aura contract", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{group}}
							report, err := ReplayScript(context.Background(), script, ReplayOptions{Variables: map[string]string{"first": "prior-output"}, IgnoreThinkTime: true})
							if err != nil && policy == "literal" {
								t.Fatal(err)
							}
							if report != nil {
								success, final = report.Success, report.FinalVariables["first"]
								if policy == "literal" && len(report.Events) != 2 {
									t.Fatalf("replay child events=%d", len(report.Events))
								}
							}
						}
						if policy == "literal" {
							if !success || traffic.Load() != 2 || accepted.Load() != 1 || final != "fresh-output" {
								t.Fatalf("literal contract did not execute intact: success=%t traffic=%d accepted=%d final=%q", success, traffic.Load(), accepted.Load(), final)
							}
						} else if success || traffic.Load() != 0 {
							t.Fatalf("invalid contract sent traffic: success=%t traffic=%d", success, traffic.Load())
						}
					})
				}
			}
		}
	}
}
