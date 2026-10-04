package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestAuraTemplateContractInvertsOnlyOldOccurrences(t *testing.T) {
	old := "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"recordId":"business","echo":"{{var.shared}}"}}]}`) + "&aura.token={{var.shared}}&aura.context=" + url.QueryEscape(`{"fwuid":"{{var.shared}}"}`)
	old = strings.ReplaceAll(old, url.QueryEscape("{{var.shared}}"), "{{var.shared}}")
	const runtimeValue = "literal+plus%2Fand%26percent"
	for _, policy := range []string{"", dsl.ImportURLFormTemplateValueEncodingQueryComponent, dsl.ImportAuraJSONTemplateValueEncoding} {
		for _, encoded := range []bool{false, true} {
			t.Run(policy+"/base64_"+map[bool]string{true: "true", false: "false"}[encoded], func(t *testing.T) {
				body, encoding := old, ""
				if encoded {
					body, encoding = base64.StdEncoding.EncodeToString([]byte(body)), "base64"
				}
				contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, encoding, policy, nil, aurapayload.MaxWorkBytes)
				if err != nil {
					t.Fatal(err)
				}
				inspection, _ := aurapayload.Inspect(body, encoding)
				var edits []aurapayload.Edit
				for _, leaf := range inspection.Leaves {
					if leaf.Key == "recordId" {
						edits = append(edits, aurapayload.Edit{Route: leaf.Route, Shape: inspection.Shape, Expected: leaf.Value, Replacement: "{{var.shared}}"})
					}
				}
				body, err = aurapayload.Replace(body, encoding, edits)
				if err != nil {
					t.Fatal(err)
				}
				vu := &VUser{Variables: map[string]string{"shared": runtimeValue}}
				action := structuredAuraAction(body, encoding)
				action.Metadata[dsl.ImportAuraTemplateContractMetadataKey] = contract
				resolved := vu.resolveAction(action)
				if vu.bodyResolutionError != nil {
					t.Fatal(vu.bodyResolutionError)
				}
				wire := []byte(resolved.Body)
				decoded, encodedBody, err := dsl.DecodeBodyEncoding(string(resolved.Body), resolved.BodyEncoding, aurapayload.MaxBodyBytes)
				if err != nil || resolved.BodyEncoding != encoding {
					t.Fatalf("resolved body encoding changed or failed: encoding=%q err=%v", resolved.BodyEncoding, err)
				}
				if encodedBody {
					wire = decoded
				}
				form, err := url.ParseQuery(string(wire))
				if err != nil {
					t.Fatal(err)
				}
				var message struct {
					Actions []struct{ Params map[string]string }
				}
				var ctx map[string]string
				if json.Unmarshal([]byte(form.Get("message")), &message) != nil || json.Unmarshal([]byte(form.Get("aura.context")), &ctx) != nil {
					t.Fatal("resolved form is invalid")
				}
				wantOld := runtimeValue
				if policy == "" {
					wantOld, _ = url.QueryUnescape(runtimeValue)
				}
				if form.Get("aura.token") != wantOld || ctx["fwuid"] != wantOld || message.Actions[0].Params["echo"] != wantOld || message.Actions[0].Params["recordId"] != runtimeValue {
					t.Fatal("consumer-specific old/new semantics changed")
				}
			})
		}
	}
}

func TestAuraPromotionPreservesEscapedLiteralOccurrences(t *testing.T) {
	inner := `{"literal":"\u007b\u007bvar.shared\u007d\u007d","secretText":"\u007b\u007bsecret.LITERAL_ONLY\u007d\u007d","functionText":"\u007b\u007bfn.uuid()\u007d\u007d"}`
	message, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "a", "descriptor": "fixture", "params": map[string]string{"recordId": "business", "accountId": "other-business", "raw": "{{var.shared}}", "literal": "{{var.shared}}", "secretText": "{{secret.LITERAL_ONLY}}", "functionText": "{{fn.uuid()}}", "input": inner}}}})
	for _, policy := range []string{"", dsl.ImportURLFormTemplateValueEncodingQueryComponent} {
		for _, base64Body := range []bool{false, true} {
			t.Run(policy+"/base64_"+fmt.Sprint(base64Body), func(t *testing.T) {
				body := "message=" + url.QueryEscape(string(message)) + "&aura.token={{var.shared}}"
				body = strings.Replace(body, "raw%22%3A%22"+url.QueryEscape("{{var.shared}}"), "raw%22%3A%22{{var.shared}}", 1)
				encoding := ""
				if base64Body {
					body, encoding = base64.StdEncoding.EncodeToString([]byte(body)), "base64"
				}
				action := dsl.Action{Type: "POST", Target: "/aura", Body: body, BodyEncoding: encoding, Metadata: map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: policy}}
				script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{action}}
				variables := map[string]string{"shared": "active-value"}
				for _, key := range []string{"recordId", "accountId"} {
					advice := paramize.Suggest(script, paramize.Options{EnableDataParams: true})
					selected := map[string]bool{}
					for _, suggestion := range advice.Suggestions {
						if len(suggestion.Locations) == 1 && strings.HasSuffix(suggestion.Locations[0].DecodedFieldPath, "."+key) {
							selected[suggestion.ID] = true
							variables[suggestion.VarName] = "fresh business+%2F literal"
						}
					}
					if len(selected) != 1 {
						t.Fatal("business advice missing")
					}
					var err error
					script, _, err = paramize.ApplySuggestions(script, advice.Suggestions, selected)
					if err != nil {
						t.Fatal(err)
					}
				}
				names, err := ReferencedSecretNames(script)
				if err != nil || len(names) != 0 {
					t.Fatalf("preserved literal added a credential requirement: names=%v err=%v", names, err)
				}
				var calls int
				vu := &VUser{Script: script, Variables: variables, SecretResolver: SecretResolverFunc(func(string) (string, bool) { calls++; return "must-not-resolve", true })}
				resolved := vu.resolveAction(script.Actions[0])
				wire := []byte(resolved.Body)
				decoded, encodedBody, err := dsl.DecodeBodyEncoding(string(resolved.Body), resolved.BodyEncoding, aurapayload.MaxBodyBytes)
				if err != nil || resolved.BodyEncoding != encoding {
					t.Fatalf("resolved body encoding changed or failed: encoding=%q err=%v", resolved.BodyEncoding, err)
				}
				if encodedBody {
					wire = decoded
				}
				form, err := url.ParseQuery(string(wire))
				if err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					Actions []struct{ Params map[string]string }
				}
				var decodedInner map[string]string
				if vu.bodyResolutionError != nil || json.Unmarshal([]byte(form.Get("message")), &envelope) != nil || len(envelope.Actions) != 1 || json.Unmarshal([]byte(envelope.Actions[0].Params["input"]), &decodedInner) != nil {
					t.Fatal("promoted literal payload failed structural resolution", vu.bodyResolutionError)
				}
				params := envelope.Actions[0].Params
				if calls != 0 || params["raw"] != "active-value" || form.Get("aura.token") != "active-value" || params["recordId"] != "fresh business+%2F literal" || params["accountId"] != "fresh business+%2F literal" || params["literal"] != "{{var.shared}}" || params["secretText"] != "{{secret.LITERAL_ONLY}}" || params["functionText"] != "{{fn.uuid()}}" || decodedInner["literal"] != "{{var.shared}}" || decodedInner["secretText"] != "{{secret.LITERAL_ONLY}}" || decodedInner["functionText"] != "{{fn.uuid()}}" {
					t.Fatalf("escaped occurrence activated or fresh occurrence froze: secret_calls=%d", calls)
				}
			})
		}
	}
}

func TestAuraMalformedTemplateContractPreventsResolutionAndTraffic(t *testing.T) {
	var traffic, secretCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { traffic.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	registerAuraFixtureHTTPProtocol.Do(func() {
		protocol.Register(auraFixtureHTTPProtocol, func() protocol.Protocol { return &httpprotocol.HTTP{} })
	})
	body := "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"apiToken":"{{secret.SYNTHETIC}}"}}]}`) + "&aura.token={{var.old}}"
	contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, "", "", nil, aurapayload.MaxWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	contract.FormWireOccurrences[len(contract.FormWireOccurrences)-1].ExpressionSHA256 = strings.Repeat("a", 64)
	action := structuredAuraAction(body, "")
	action.Metadata[dsl.ImportAuraTemplateContractMetadataKey] = contract
	script := &dsl.Script{Protocol: auraFixtureHTTPProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{action}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{SecretResolver: SecretResolverFunc(func(string) (string, bool) { secretCalls.Add(1); return "synthetic", true })})
	if err == nil && (report == nil || report.Success) {
		t.Fatal("malformed occurrence contract was admitted")
	}
	if traffic.Load() != 0 || secretCalls.Load() != 0 {
		t.Fatalf("malformed contract had side effects: requests=%d secret_resolutions=%d", traffic.Load(), secretCalls.Load())
	}
}
