package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func auraResolutionMessage(value string, layers int) string {
	params, _ := json.Marshal(map[string]any{"accountId": value, "apiToken": "{{secret.TOKEN}}", "kept": "literal", "n": 42})
	for i := 0; i < layers; i++ {
		params, _ = json.Marshal(map[string]any{"payload": string(params)})
	}
	body, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "{{var.action_id}}", "descriptor": "synthetic", "params": json.RawMessage(params)}}})
	return string(body)
}
func decodeAuraResolutionParams(message string, layers int) (map[string]any, string, bool) {
	var envelope struct {
		Actions []struct {
			ID     string
			Params map[string]any
		}
	}
	if json.Unmarshal([]byte(message), &envelope) != nil || len(envelope.Actions) != 1 {
		return nil, "", false
	}
	params := envelope.Actions[0].Params
	for i := 0; i < layers; i++ {
		nested, ok := params["payload"].(string)
		if !ok {
			return nil, "", false
		}
		var next map[string]any
		if json.Unmarshal([]byte(nested), &next) != nil {
			return nil, "", false
		}
		params = next
	}
	return params, envelope.Actions[0].ID, true
}
func structuredAuraAction(body, encoding string) dsl.Action {
	return dsl.Action{Name: "Structured Aura batch", Type: "request", Method: "POST", Target: "/aura", Body: body, BodyEncoding: encoding, Metadata: map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportAuraJSONTemplateValueEncoding}, Assertions: []dsl.Assertion{{Type: "aura_actions_success"}}}
}

func TestReplayStructuredAuraEscapesNestedValuesContextAndLiteralTemplateDataOnRealHTTP(t *testing.T) {
	const layers = 3
	const secret = "synthetic-token quote\" slash\\ amp& plus+ 雪"
	const value = "quote\" slash\\ amp& plus+ 雪\n {{param.literal_data}}"
	const fwuid = "framework\" slash\\ amp& plus+ 雪\n"
	for _, tc := range []struct {
		name          string
		form, encoded bool
	}{{name: "direct JSON"}, {name: "form and context", form: true}, {name: "base64 form", form: true, encoded: true}} {
		t.Run(tc.name, func(t *testing.T) {
			var accepted, rejected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/bootstrap" {
					_ = json.NewEncoder(w).Encode(map[string]string{"value": value, "fwuid": fwuid, "id": "synthetic-fresh-1;a"})
					return
				}
				wire, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					rejected.Add(1)
					w.WriteHeader(400)
					return
				}
				message := string(wire)
				if tc.form {
					values, err := url.ParseQuery(message)
					var context map[string]string
					if err != nil || len(values["message"]) != 1 || json.Unmarshal([]byte(values.Get("aura.context")), &context) != nil || context["fwuid"] != fwuid || context["mode"] != "PROD" || values.Get("aura.token") != secret || !strings.HasPrefix(message, "untouched=%2f&") {
						rejected.Add(1)
						w.WriteHeader(400)
						return
					}
					message = values.Get("message")
				}
				params, id, ok := decodeAuraResolutionParams(message, layers)
				if !ok || id != "synthetic-fresh-1;a" || params["accountId"] != value || params["apiToken"] != secret || params["kept"] != "literal" || params["n"] != float64(42) {
					rejected.Add(1)
					w.WriteHeader(400)
					return
				}
				accepted.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"actions": []any{map[string]any{"id": id, "state": "SUCCESS"}}})
			}))
			defer server.Close()
			registerAuraFixtureHTTPProtocol.Do(func() {
				protocol.Register(auraFixtureHTTPProtocol, func() protocol.Protocol { return &httpprotocol.HTTP{} })
			})
			body, encoding := auraResolutionMessage("{{var.account_value}}", layers), ""
			if tc.form {
				context, _ := json.Marshal(map[string]string{"fwuid": "{{var.fwuid}}", "mode": "PROD"})
				body = "untouched=%2f&message=" + url.QueryEscape(body) + "&aura.context=" + url.QueryEscape(string(context)) + "&aura.token=" + url.QueryEscape("{{secret.TOKEN}}")
			}
			if tc.encoded {
				body = base64.StdEncoding.EncodeToString([]byte(body))
				encoding = "base64"
			}
			script := &dsl.Script{Protocol: auraFixtureHTTPProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{
				{Name: "Bootstrap structured values", Type: "request", Method: "GET", Target: "/bootstrap", Extractors: []dsl.Extractor{{Name: "account_value", Type: "jsonpath", Source: "body", Expression: "$.value", OnMiss: "fail"}, {Name: "fwuid", Type: "jsonpath", Source: "body", Expression: "$.fwuid", OnMiss: "fail"}, {Name: "action_id", Type: "jsonpath", Source: "body", Expression: "$.id", OnMiss: "fail"}}}, structuredAuraAction(body, encoding),
			}}
			resolver := SecretResolverFunc(func(name string) (string, bool) { return secret, name == "TOKEN" })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			report, err := ReplayScript(ctx, script, ReplayOptions{SecretResolver: resolver, IgnoreThinkTime: true})
			if err != nil {
				t.Fatal(err)
			}
			if !report.Success || len(report.Events) != 2 || accepted.Load() != 1 || rejected.Load() != 0 {
				t.Fatalf("exact structured replay failed: success=%t events=%d accepted=%d rejected=%d", report.Success, len(report.Events), accepted.Load(), rejected.Load())
			}
			event := report.Events[1]
			encoded, _ := json.Marshal(event.ProtocolEvidence["aura"])
			var outcome aura.Report
			if json.Unmarshal(encoded, &outcome) != nil || !outcome.Success || outcome.MatchedActions != 1 || event.StatusCode != 200 {
				t.Fatal("strict assertion lost actual resolved action identity")
			}
			evidence, _ := json.Marshal(report)
			if strings.Contains(string(evidence), secret) || strings.Contains(string(evidence), url.QueryEscape(secret)) {
				t.Fatal("replay report exposed the resolved credential")
			}
		})
	}
}

func TestStructuredAuraResolutionMirrorsNondeterministicValuesAndRedactsSecrets(t *testing.T) {
	body := auraResolutionMessage("{{fn.uuid()}}", 3)
	vu := &VUser{Variables: map[string]string{"action_id": "synthetic;a"}, SecretResolver: SecretResolverFunc(func(string) (string, bool) { return "synthetic-sensitive &+雪", true })}
	action := vu.resolveAction(structuredAuraAction(body, ""))
	actual, id, ok := decodeAuraResolutionParams(string(action.Body), 3)
	safe, safeID, safeOK := decodeAuraResolutionParams(string(action.RedactedBody), 3)
	if !ok || !safeOK || id != safeID || actual["accountId"] != safe["accountId"] || actual["accountId"] == "{{fn.uuid()}}" || safe["apiToken"] != "[REDACTED]" || actual["apiToken"] != "synthetic-sensitive &+雪" {
		t.Fatal("actual/redacted pair re-evaluated nondeterministic data or disclosed a secret")
	}
	if vu.structuredBodyResolvedAction != action || vu.bodyResolutionError != nil {
		t.Fatal("successful selected resolution was not bound to its exact protocol action")
	}
}

func TestStructuredAuraAdmissionAndUnresolvedGatesPreventTraffic(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	registerAuraFixtureHTTPProtocol.Do(func() {
		protocol.Register(auraFixtureHTTPProtocol, func() protocol.Protocol { return &httpprotocol.HTTP{} })
	})
	for _, tc := range []struct {
		name, body, encoding string
		missingSecret        bool
	}{
		{name: "duplicate member", body: `{"actions":[{"id":"a","descriptor":"fixture","params":{"name":"one","name":"two"}}]}`},
		{name: "unsupported Windows1252", body: `{"actions":[{"id":"a","descriptor":"fixture","params":{}}]}`, encoding: "windows-1252"},
		{name: "invalid base64", body: "invalid encoded source", encoding: "base64"},
		{name: "templated reserved form name", body: "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{}}]}`) + "&" + url.QueryEscape("{{var.reserved}}") + "=value"},
		{name: "missing parameter", body: `{"actions":[{"id":"a","descriptor":"fixture","params":{"name":"{{param.missing}}"}}]}`},
		{name: "encoded missing secret preflight", body: "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"name":"{{secret.MISSING}}"}}]}`), missingSecret: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.missingSecret {
				t.Setenv(allowUnresolvedSecretsEnv, "true")
			}
			script := &dsl.Script{Protocol: auraFixtureHTTPProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{structuredAuraAction(tc.body, tc.encoding)}}
			report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, SecretResolver: SecretResolverFunc(func(string) (string, bool) { return "", false })})
			if err == nil && (report == nil || report.Success) {
				t.Fatal("selected malformed or unresolved body was admitted")
			}
			if requests.Load() != 0 {
				t.Fatal("selected invalid body reached the local target")
			}
		})
	}
}

func TestStructuredBodyResolvedActionCannotBypassOtherFieldsOrLaterLegacyBody(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"value": "{{param.literal}}"}}
	selected := structuredAuraAction(`{"actions":[{"id":"a","descriptor":"fixture","params":{"name":"{{var.value}}"}}]}`, "")
	selected.Headers = map[string]string{"X-Required": "{{param.missing}}"}
	resolved := vu.resolveAction(selected)
	if !actionContainsUnresolvedParameterExceptBody(resolved, true) {
		t.Fatal("structural body exemption hid an unresolved header")
	}
	legacy := vu.resolveAction(dsl.Action{Type: "request", Body: "{{param.missing}}"})
	if vu.structuredBodyResolvedAction != nil || !actionContainsUnresolvedParameter(legacy) {
		t.Fatal("a later legacy action inherited structured-body scan suppression")
	}
}

func TestReferencedSecretsIncludesEncodedStructuredLeavesAndContext(t *testing.T) {
	body := "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"payload":"{\"value\":\"{{secret.INNER}}\"}"}}]}`) + "&aura.context=" + url.QueryEscape(`{"fwuid":"{{secret.CONTEXT}}"}`) + "&aura.token=" + url.QueryEscape("{{secret.TOKEN}}")
	script := &dsl.Script{Actions: []dsl.Action{structuredAuraAction(base64.StdEncoding.EncodeToString([]byte(body)), "base64")}}
	names, err := ReferencedSecretNames(script)
	if err != nil || strings.Join(names, ",") != "CONTEXT,INNER,TOKEN" {
		t.Fatal("encoded structured credential references were absent from static preflight")
	}
}

func TestStructuredAuraRepeatedLargeValuesFailBeforeUnboundedExpansionAndTraffic(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()
	transport := &httpprotocol.HTTP{}
	if err := transport.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	defer transport.Teardown(context.Background())
	// The source is small, but an unchecked interpolation would expand one
	// decoded leaf to 64 MiB before the codec sees either projection.
	large := strings.Repeat("x", 512<<10)
	value := strings.Repeat("{{var.large}}", 128)
	params, _ := json.Marshal(map[string]string{"accountId": value})
	body := `{"actions":[{"id":"a","descriptor":"fixture","params":` + string(params) + `}]}`
	vu := &VUser{Proto: transport, Variables: map[string]string{"large": large}}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	action := vu.resolveAction(structuredAuraAction(body, ""))
	runtime.ReadMemStats(&after)
	var typed *aurapayload.Error
	if !errors.As(vu.bodyResolutionError, &typed) || typed.Code != aurapayload.ByteLimit {
		t.Fatal("repeated large value was not rejected by the structured interpolation bound")
	}
	// Include both builders, parser and rendering overhead. This fails the
	// former 64 MiB-per-projection expansion while allowing bounded growth.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 128<<20 {
		t.Fatalf("bounded resolution allocated %d bytes", allocated)
	}
	result, err := vu.safeProtoExecute(context.Background(), action)
	if err == nil || result != nil || requests.Load() != 0 || string(action.Body) != body {
		t.Fatal("oversized structured expansion reached transport or changed original source")
	}
}

func TestStructuredAuraOccurrenceBudgetRejectsBeforeEvaluatingValues(t *testing.T) {
	calls := 0
	vu := &VUser{SecretResolver: SecretResolverFunc(func(string) (string, bool) {
		calls++
		return "synthetic", true
	})}
	params, _ := json.Marshal(map[string]string{"accountId": strings.Repeat("{{secret.TOKEN}}", aurapayload.MaxTemplateOccurrences+1)})
	body := `{"actions":[{"id":"a","descriptor":"fixture","params":` + string(params) + `}]}`
	vu.resolveAction(structuredAuraAction(body, ""))
	var typed *aurapayload.Error
	if !errors.As(vu.bodyResolutionError, &typed) || typed.Code != aurapayload.TokenLimit || calls != 0 {
		t.Fatal("too many authored occurrences were evaluated before rejection")
	}
}

func TestReferencedSecretsStructuredOccurrenceBudgetIsSharedAcrossDecodedLeaves(t *testing.T) {
	params, _ := json.Marshal(map[string]string{
		"accountId": strings.Repeat("{{secret.FIRST}}", aurapayload.MaxTemplateOccurrences/2),
		"recordId":  strings.Repeat("{{secret.SECOND}}", aurapayload.MaxTemplateOccurrences/2+1),
	})
	body := `{"actions":[{"id":"a","descriptor":"fixture","params":` + string(params) + `}]}`
	script := &dsl.Script{Actions: []dsl.Action{structuredAuraAction(body, "")}}
	names, err := ReferencedSecretNames(script)
	var typed *aurapayload.Error
	if !errors.As(err, &typed) || typed.Code != aurapayload.TokenLimit || names != nil {
		t.Fatal("selected static preflight returned partial names or exceeded its shared occurrence budget")
	}
}
