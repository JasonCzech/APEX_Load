package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReferencedSecretNamesCoversConfigActionsAndNestedValues(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{
				DSN:       "{{secret.DATABASE_DSN}}",
				DSNSecret: "legacy.database-dsn",
			},
			Kafka: dsl.KafkaConfig{SASLPasswordSecret: "KAFKA_PASSWORD"},
		},
		Actions: []dsl.Action{{
			Headers: map[string]string{
				"Authorization": "Bearer {{ secret.API_TOKEN }}",
				"X-Legacy":      "{{ secret.db.password-key }}",
			},
			Params: map[string]any{
				"nested": []any{"{{secret.API_TOKEN}}", "{{secret.CLIENT_SECRET}}"},
			},
		}},
	}
	got, err := ReferencedSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"API_TOKEN", "CLIENT_SECRET", "DATABASE_DSN", "KAFKA_PASSWORD", "db.password-key", "legacy.database-dsn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReferencedSecretNames() = %v, want %v", got, want)
	}
}

func TestReferencedSecretNamesNeverTreatsJavaVMArgumentsAsProjectSecrets(t *testing.T) {
	script := &dsl.Script{
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			AdditionalVMParameters: []string{
				"-Dlabel={{secret.JAVA_VM_ARG_MUST_NOT_RESOLVE}}",
			},
		}},
		Actions: []dsl.Action{{
			Name: "Ordinary action value",
			Type: "custom_call",
			Params: map[string]any{
				"additional_vm_parameters": "{{secret.ACTION_VALUE_STILL_RESOLVES}}",
			},
		}},
	}
	names, err := ReferencedSecretNames(script)
	if err != nil {
		t.Fatalf("ReferencedSecretNames: %v", err)
	}
	if len(names) != 1 || names[0] != "ACTION_VALUE_STILL_RESOLVES" {
		t.Fatalf("referenced names = %v, want only the ordinary action value", names)
	}
}

func TestBase64WireBodySecretPreflightAndTransport(t *testing.T) {
	const secretName = "BASE64_BODY_PASSWORD"
	const secretValue = "runtime-base64-secret-4d91"
	wireTemplate := `{"password":"{{secret.` + secretName + `}}","note":"unchanged"}`
	storedBody := base64.StdEncoding.EncodeToString([]byte(wireTemplate))
	var calls atomic.Int64
	var received bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		received.Reset()
		_, _ = received.ReadFrom(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	script := &dsl.Script{
		Name:     "base64 wire credential",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: server.URL},
		Actions: []dsl.Action{{
			Name:         "submit",
			Type:         "request",
			Method:       http.MethodPost,
			Target:       "/login",
			Body:         storedBody,
			BodyEncoding: "base64",
		}},
	}
	names, err := ReferencedSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{secretName}) {
		t.Fatalf("ReferencedSecretNames() = %v, want decoded body secret", names)
	}

	missing := SecretResolverFunc(func(string) (string, bool) { return "", false })
	if _, err := ReplayScript(context.Background(), script, ReplayOptions{
		SecretResolver: missing, IgnoreThinkTime: true,
	}); err == nil || !strings.Contains(err.Error(), secretName) {
		t.Fatalf("missing-secret replay error = %v, want %s preflight failure", err, secretName)
	}
	if calls.Load() != 0 {
		t.Fatalf("missing-secret replay reached transport %d time(s)", calls.Load())
	}

	resolver := SecretResolverFunc(func(name string) (string, bool) {
		return secretValue, name == secretName
	})
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		SecretResolver: resolver, IgnoreThinkTime: true,
	})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if report == nil || !report.Success {
		t.Fatalf("ReplayScript report = %#v", report)
	}
	if calls.Load() != 1 {
		t.Fatalf("resolved replay transport calls = %d, want 1", calls.Load())
	}
	wantWire := `{"password":"` + secretValue + `","note":"unchanged"}`
	if received.String() != wantWire {
		t.Fatalf("wire body = %q, want %q", received.String(), wantWire)
	}
	if script.Actions[0].Body != storedBody {
		t.Fatal("runtime mutated the stored base64 body")
	}
}

func TestReferencedSecretNamesSkipsProvenanceAndReviewOnlyActions(t *testing.T) {
	script := &dsl.Script{
		ID:            "{{secret.IDENTITY_ID}}",
		RefKey:        "{{secret.IDENTITY_REF}}",
		Name:          "{{secret.DISPLAY_NAME}}",
		Version:       "{{secret.DISPLAY_VERSION}}",
		Protocol:      "http",
		GeneratedFrom: "{{secret.PROVENANCE_SOURCE}}",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
			"credential": "{{secret.PROVENANCE_TOKEN}}",
			"dsn_secret": "PROVENANCE_DSN",
		}}},
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{DSNSecret: "EXECUTABLE_DSN"},
		},
		Actions: []dsl.Action{
			{
				Name:   "Source review",
				Type:   "source_review",
				Target: "{{secret.REVIEW_TARGET}}",
				Params: map[string]any{
					"dsn_secret": "REVIEW_DSN",
					"nested":     "{{secret.REVIEW_NESTED}}",
				},
			},
			{
				Name:   "Request",
				Type:   "request",
				Target: "/resource",
				Headers: map[string]string{
					"Authorization": "Bearer {{secret.EXECUTABLE_TOKEN}}",
				},
				Params: map[string]any{
					"payload": map[string]any{
						"type":  "source_review",
						"value": "{{secret.EXECUTABLE_PAYLOAD_TOKEN}}",
					},
				},
			},
		},
	}

	got, err := ReferencedSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"EXECUTABLE_DSN", "EXECUTABLE_PAYLOAD_TOKEN", "EXECUTABLE_TOKEN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReferencedSecretNames() = %v, want %v", got, want)
	}

	dedicated, err := dedicatedLogicalSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dedicated, map[string]struct{}{"EXECUTABLE_DSN": {}}) {
		t.Fatalf("dedicatedLogicalSecretNames() = %v", dedicated)
	}
}

// Action metadata secrets MUST be collected: ReferencedSecretNames drives the
// runtime RedactionConfig and the fail-closed missing-secret preflight, and
// kafka/jms copy metadata onto the wire (TestJMSPublishWithMetadataAndNoPropertiesConfig
// asserts source_transaction is delivered as a JMS property). Suppressing the
// "configure this secret" PROMPT for provenance keys is a UI concern handled in
// pkg/api, not here. An earlier version of this test asserted the opposite and
// would have let a wire-bound credential go unredacted.
func TestReferencedSecretNamesCollectsActionMetadataSecretsForRedaction(t *testing.T) {
	script := &dsl.Script{
		Protocol: "http",
		Actions: []dsl.Action{
			{
				Name:   "HTTP request",
				Type:   "request",
				Target: "/orders",
				Headers: map[string]string{
					"Authorization": "Bearer {{secret.EXECUTABLE_TOKEN}}",
				},
				Metadata: map[string]any{
					"original_url":       "https://service.test:{{secret.METADATA_URL_PORT}}/path",
					"source_transaction": "{{secret.METADATA_TRANSACTION_NAME}}",
				},
			},
		},
	}

	got, err := ReferencedSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"EXECUTABLE_TOKEN", "METADATA_TRANSACTION_NAME", "METADATA_URL_PORT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReferencedSecretNames() = %v, want %v (metadata secrets must stay redactable)", got, want)
	}
}

func TestSnapshotReferencedSecretsFailsClosedForEveryStaticReference(t *testing.T) {
	tests := map[string]*dsl.Script{
		"dedicated config": {
			Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{SASLPasswordSecret: "KAFKA_PASSWORD"}},
		},
		"action template": {
			Actions: []dsl.Action{{Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"}}},
		},
		"control-only template": {
			Actions: []dsl.Action{{
				Type:    "control_if",
				Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "{{secret.FEATURE_SECRET}}", Op: "exists"}},
			}},
		},
	}
	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := snapshotReferencedSecrets(script, SecretResolverFunc(func(string) (string, bool) {
				return "", false
			}))
			if err == nil || !strings.Contains(err.Error(), "required project secret") {
				t.Fatalf("missing static secret error = %v", err)
			}
		})
	}
}

func TestSnapshotReferencedSecretsUsesOneOverallResolutionDeadline(t *testing.T) {
	const secretValue = "resolution-budget-secret-value"
	script := &dsl.Script{Actions: []dsl.Action{{
		Headers: map[string]string{
			"X-First":  "{{secret.FIRST}}",
			"X-Second": "{{secret.SECOND}}",
			"X-Third":  "{{secret.THIRD}}",
		},
	}}}
	var calls atomic.Int64
	resolver := ContextSecretResolverFunc(func(ctx context.Context, _ string) (string, bool) {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return secretValue, false
		case <-time.After(time.Second):
			return secretValue, true
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := snapshotReferencedSecretsContext(ctx, script, resolver)
	if err == nil || !strings.Contains(err.Error(), "overall deadline") {
		t.Fatalf("snapshot error = %v, want overall deadline", err)
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Fatalf("resolution error disclosed secret value: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("secret snapshot took %s despite shared deadline", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want fail-fast after the first deadline", got)
	}
}

func TestNormalizeLogicalSecretNameCanonicalizesCompatibleAliases(t *testing.T) {
	tests := map[string]string{
		"LOGIN_PASSWORD":  "LOGIN_PASSWORD",
		"login.password":  "LOGIN_PASSWORD",
		"login-password":  "LOGIN_PASSWORD",
		" mixed.Name-id ": "MIXED_NAME_ID",
	}
	for input, want := range tests {
		got, err := NormalizeLogicalSecretName(input)
		if err != nil {
			t.Fatalf("NormalizeLogicalSecretName(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeLogicalSecretName(%q) = %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"", "BAD NAME", "BAD/NAME", strings.Repeat("A", MaxLogicalSecretNameBytes+1)} {
		if _, err := NormalizeLogicalSecretName(input); err == nil {
			t.Fatalf("NormalizeLogicalSecretName(%q) unexpectedly succeeded", input)
		}
	}
}

func TestReplayAndShakeoutUseExplicitSecretResolver(t *testing.T) {
	const secretValue = "runtime-only-value"
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if got := action.Headers["Authorization"]; got != "Bearer "+secretValue {
			t.Fatalf("Authorization = %q, want resolved secret", got)
		}
		return &protocol.Result{Success: true, ActionName: action.Name, StartTime: time.Now()}, nil
	})
	script := &dsl.Script{
		Name:     "resolver-injection",
		Protocol: protocolName,
		Actions: []dsl.Action{{
			Name:    "request",
			Type:    "GET",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		return secretValue, name == "API_TOKEN"
	})
	if _, err := ReplayScript(context.Background(), script, ReplayOptions{SecretResolver: resolver}); err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if err := RunScriptShakeoutWithSecretResolver(context.Background(), "shakeout", script, resolver); err != nil {
		t.Fatalf("RunScriptShakeoutWithSecretResolver: %v", err)
	}
}

func TestNewUsesExplicitSecretResolver(t *testing.T) {
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		return "configured", name == "TOKEN"
	})
	instance, err := New(Config{
		Script: &dsl.Script{
			Name:     "load",
			Protocol: "http",
			Actions:  []dsl.Action{{Name: "request", Type: "GET", Target: "/?token={{secret.TOKEN}}"}},
		},
		VUsers:         1,
		Duration:       time.Second,
		SecretResolver: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := instance.secrets.Resolve("TOKEN"); !ok || value != "configured" {
		t.Fatalf("explicit resolver = (%q, %t)", value, ok)
	}
}

func TestExecutionEntryPointsRejectMissingStaticSecretBeforeLaunch(t *testing.T) {
	script := &dsl.Script{
		Name:     "missing-runtime-secret",
		Protocol: "protocol-must-not-be-inspected",
		Actions: []dsl.Action{{
			Name:    "request",
			Type:    "GET",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	}
	missing := SecretResolverFunc(func(string) (string, bool) { return "", false })
	assertMissing := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "API_TOKEN") || strings.Contains(err.Error(), "protocol init") {
			t.Fatalf("preflight error = %v, want missing API_TOKEN before protocol initialization", err)
		}
	}

	_, err := New(Config{Script: script, VUsers: 1, Duration: time.Second, SecretResolver: missing})
	assertMissing(t, err)
	_, err = NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups:   []GroupConfig{{Name: "group", Script: script, VUsers: 1, SecretResolver: missing}},
	})
	assertMissing(t, err)
	_, err = ReplayScript(context.Background(), script, ReplayOptions{SecretResolver: missing})
	assertMissing(t, err)
	err = RunScriptShakeoutWithSecretResolver(context.Background(), "group", script, missing)
	assertMissing(t, err)

	if _, err := New(Config{
		Script:         &dsl.Script{Name: "secret-free", Protocol: "http"},
		VUsers:         1,
		Duration:       time.Second,
		SecretResolver: missing,
	}); err != nil {
		t.Fatalf("secret-free engine construction failed: %v", err)
	}
}

func TestSnapshotReferencedSecretsResolvesEachNameOnceUnderConcurrency(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{{
		Headers: map[string]string{
			"Authorization": "Bearer {{secret.TOKEN}}",
			"X-Optional":    "{{secret.MISSING}}",
		},
	}}}
	var calls atomic.Int32
	delegate := SecretResolverFunc(func(name string) (string, bool) {
		calls.Add(1)
		time.Sleep(time.Millisecond)
		switch name {
		case "TOKEN":
			return "configured", true
		case "MISSING":
			return "also-configured", true
		}
		return "", false
	})

	cached := CacheSecretResolverForRun(delegate)
	if got := CacheSecretResolverForRun(cached); got != cached {
		t.Fatal("already cached resolver was wrapped a second time")
	}
	snapshot, values, err := snapshotReferencedSecrets(script, cached)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, []string{"also-configured", "configured"}) {
		t.Fatalf("resolved values = %v", values)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider calls after snapshot = %d, want one per referenced name", got)
	}

	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if value, ok := snapshot.Resolve("TOKEN"); !ok || value != "configured" {
				t.Errorf("TOKEN = (%q, %t)", value, ok)
			}
			if value, ok := snapshot.Resolve("MISSING"); !ok || value != "also-configured" {
				t.Errorf("MISSING = (%q, %t)", value, ok)
			}
		}()
	}
	wait.Wait()
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider calls after concurrent resolution = %d, want 2", got)
	}
}

func TestSnapshotReferencedSecretsEnforcesRunBounds(t *testing.T) {
	script := &dsl.Script{Actions: make([]dsl.Action, maxCachedSecrets+1)}
	for index := range script.Actions {
		script.Actions[index].Value = fmt.Sprintf("{{secret.SECRET_%04d}}", index)
	}
	_, _, err := snapshotReferencedSecrets(script, SecretResolverFunc(func(string) (string, bool) {
		return "configured", true
	}))
	if err == nil || !strings.Contains(err.Error(), "logical secrets") {
		t.Fatalf("snapshotReferencedSecrets error = %v", err)
	}
}

func TestRuntimeSecretValueRedactionCoversArbitraryProtocolErrors(t *testing.T) {
	const secretValue = "short-value"
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return nil, errors.New("remote echoed " + secretValue + " without a credential label")
	})
	script := &dsl.Script{
		Name:     "resolver-error-redaction",
		Protocol: protocolName,
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "POST",
			Target: "/",
			Value:  "{{secret.API_TOKEN}}",
		}},
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		return secretValue, name == "API_TOKEN"
	})
	report, err := ReplayScript(context.Background(), script, ReplayOptions{SecretResolver: resolver})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if len(report.Events) != 1 || strings.Contains(report.Events[0].ErrorMessage, secretValue) ||
		!strings.Contains(report.Events[0].ErrorMessage, redactedPayloadValue) {
		t.Fatalf("replay error = %q", report.Events[0].ErrorMessage)
	}
	if err := RunScriptShakeoutWithSecretResolver(context.Background(), "shakeout", script, resolver); err == nil {
		t.Fatal("shakeout returned nil error")
	} else if strings.Contains(err.Error(), secretValue) || !strings.Contains(err.Error(), redactedPayloadValue) {
		t.Fatalf("shakeout error = %q", err)
	}
}

func TestReplaySecretConditionEvidenceAndFinalVariablesNeverExposeValues(t *testing.T) {
	const secretValue = "condition-project-secret-value"
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StartTime: time.Now()}, nil
	})
	script := &dsl.Script{
		Name:     "secret-condition-evidence",
		Protocol: protocolName,
		Actions: []dsl.Action{{
			Name: "Secret branch",
			Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{secret.API_TOKEN}}", Op: "exists"},
				Steps: []dsl.Action{{
					Name: "Store secret alias",
					Type: "source_parameter",
					Params: map[string]any{
						"function": "lr_save_string",
						"args":     []any{"{{secret.API_TOKEN}}", "public_alias"},
					},
				}, {
					Name: "Store sensitive runtime variable",
					Type: "source_parameter",
					Params: map[string]any{
						"function": "lr_save_string",
						"args":     []any{"response-extracted-token", "access_token"},
					},
				}, {
					Name: "Store ordinary variable",
					Type: "source_parameter",
					Params: map[string]any{
						"function": "lr_save_string",
						"args":     []any{"visible-display-name", "display_name"},
					},
				}},
			},
		}},
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		return secretValue, name == "API_TOKEN"
	})
	var condition *ConditionEvidence
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		SecretResolver: resolver,
		OnProgress: func(event ReplayProgressEvent) {
			if event.Condition != nil {
				copy := *event.Condition
				condition = &copy
			}
		},
	})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if condition == nil || condition.Left != "{{secret.API_TOKEN}}" || !condition.Result || condition.Branch != "then" {
		t.Fatalf("condition evidence = %#v", condition)
	}
	if got := report.FinalVariables["public_alias"]; got != redactedPayloadValue {
		t.Fatalf("final public_alias = %q, want redacted", got)
	}
	if got := report.FinalVariables["access_token"]; got != redactedPayloadValue {
		t.Fatalf("final access_token = %q, want redacted", got)
	}
	if got := report.FinalVariables["display_name"]; got != "visible-display-name" {
		t.Fatalf("final display_name = %q, want ordinary value preserved", got)
	}
	payload, err := json.Marshal(map[string]any{"condition": condition, "report": report})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), secretValue) {
		t.Fatalf("serialized replay evidence exposed the configured secret: %s", payload)
	}
}

func TestRenderedSourceMessageRedactsResolvedProjectSecret(t *testing.T) {
	const secretValue = "message-project-secret-value"
	vu := &VUser{
		SecretResolver: SecretResolverFunc(func(name string) (string, bool) {
			return secretValue, name == "API_TOKEN"
		}),
		RedactionConfig: WithSecretValueRedaction(nil, []string{secretValue}),
	}
	message := vu.renderErrorMessage(dsl.Action{
		Value: "credential={{secret.API_TOKEN}}",
	})
	if strings.Contains(message, secretValue) || !strings.Contains(message, redactedPayloadValue) {
		t.Fatalf("rendered source message = %q", message)
	}
}

func TestSecretValueRedactionIsRuntimeOnly(t *testing.T) {
	config := WithSecretValueRedaction(nil, []string{"secret-value"})
	if config == nil {
		t.Fatal("runtime redaction config is nil")
	}
	if len(config.Rules) != 0 {
		t.Fatalf("secret values were exposed as serializable rules: %#v", config.Rules)
	}
	if got := config.ScrubText("prefix secret-value suffix"); got != "prefix [REDACTED] suffix" {
		t.Fatalf("ScrubText() = %q", got)
	}
}

func TestSecretValueRedactionCoversReversibleEncodingsAndSuppressesScreenshots(t *testing.T) {
	const secret = "a/b?"
	config := WithSecretValueRedaction(nil, []string{secret})
	seen := make(map[string]struct{})
	for _, encoded := range runtimeSecretRedactionVariants(secret) {
		if _, duplicate := seen[encoded]; duplicate {
			continue
		}
		seen[encoded] = struct{}{}
		if got := config.ScrubText("prefix " + encoded + " suffix"); strings.Contains(got, encoded) || !strings.Contains(got, redactedPayloadValue) {
			t.Errorf("encoded secret %q was not redacted: %q", encoded, got)
		}
	}
	result := &protocol.Result{
		ScreenshotBase64: "opaque-image",
		Snapshot:         &protocol.ErrorSnapshot{ScreenshotBase64: "opaque-snapshot-image"},
	}
	scrubSensitiveResult(result, config)
	if result.ScreenshotBase64 != "" || result.Snapshot.ScreenshotBase64 != "" {
		t.Fatalf("opaque screenshots survived active runtime secrets: %#v", result)
	}
}

func TestSecretValueRedactionCoversEscapedAndCompositeNeutralEvidence(t *testing.T) {
	const secret = `a"b&c</d'e>`
	config := WithSecretValueRedaction(nil, []string{secret})
	jsonPlain := jsonEscapedSecret(secret, false)
	htmlNamed := escapeRuntimeSecretEntities(secret, "&quot;", "&#39;")
	xmlNamed := escapeRuntimeSecretEntities(secret, "&quot;", "&apos;")
	direct := map[string]string{
		"json_html_safe":       jsonEscapedSecret(secret, true),
		"json_html_safe_upper": upperJSONUnicodeEscapes(jsonEscapedSecret(secret, true)),
		"json_plain":           jsonPlain,
		"json_slash":           strings.ReplaceAll(jsonPlain, "/", `\/`),
		"html_decimal":         html.EscapeString(secret),
		"html_named":           htmlNamed,
		"xml_named":            xmlNamed,
		"html_hex":             escapeRuntimeSecretEntities(secret, "&#x22;", "&#x27;"),
	}
	evidence := make(map[string]any, len(direct)+4)
	for name, value := range direct {
		evidence[name] = value
	}
	evidence["neutral_json_query"] = url.QueryEscape(jsonPlain)
	evidence["neutral_html_base64"] = base64.StdEncoding.EncodeToString([]byte(htmlNamed))
	evidence["neutral_xml_base64"] = base64.StdEncoding.EncodeToString([]byte(xmlNamed))
	evidence["neutral_xml_wrapper"] = `<echo>` + xmlNamed + `</echo>`
	result := &protocol.Result{
		ResponseBody:     `{"echo":"` + jsonPlain + `","markup":"` + htmlNamed + `"}`,
		ProtocolEvidence: evidence,
	}
	scrubSensitiveResult(result, config)
	for name := range direct {
		if got := result.ProtocolEvidence[name]; got != redactedPayloadValue {
			t.Errorf("direct %s escaped spelling was not redacted: %#v", name, got)
		}
	}
	for _, name := range []string{"neutral_json_query", "neutral_html_base64", "neutral_xml_base64"} {
		if got := result.ProtocolEvidence[name]; got != redactedPayloadValue {
			t.Errorf("composite %s escaped spelling was not redacted: %#v", name, got)
		}
	}
	if got := result.ProtocolEvidence["neutral_xml_wrapper"]; got != `<echo>[REDACTED]</echo>` {
		t.Errorf("wrapped XML escaped spelling was not redacted: %#v", got)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), redactedPayloadValue) {
		t.Fatalf("neutral evidence was not redacted: %s", encoded)
	}
}

func TestReferencedSecretNamesRejectsMalformedReferences(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{{
		Body: "{{secret.}} {{secret.BAD NAME}} {{env.NOT_A_SECRET}}",
	}}}
	if _, err := ReferencedSecretNames(script); err == nil {
		t.Fatal("ReferencedSecretNames() accepted malformed secret references")
	}
}

type preInitSecretEvidenceProtocol struct {
	active func() bool
}

func (*preInitSecretEvidenceProtocol) Name() string { return "pre-init-secret-evidence" }
func (p *preInitSecretEvidenceProtocol) SetRuntimeSecretActivity(active func() bool) {
	p.active = active
}
func (p *preInitSecretEvidenceProtocol) Init(context.Context, map[string]any) error {
	if p.active == nil || !p.active() {
		return errors.New("runtime secret evidence gate was not active before protocol Init")
	}
	return nil
}
func (*preInitSecretEvidenceProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*preInitSecretEvidenceProtocol) Teardown(context.Context) error    { return nil }
func (*preInitSecretEvidenceProtocol) HealthCheck(context.Context) error { return nil }

var registerPreInitSecretEvidenceProtocol sync.Once

func TestResolvedSecretsGateProtocolEvidenceBeforeInitAcrossRuntimeModes(t *testing.T) {
	registerPreInitSecretEvidenceProtocol.Do(func() {
		protocol.Register("pre-init-secret-evidence", func() protocol.Protocol { return &preInitSecretEvidenceProtocol{} })
	})
	script := &dsl.Script{
		Name:     "pre-init secret evidence",
		Protocol: "pre-init-secret-evidence",
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/?token={{secret.API_TOKEN}}"}},
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		return "pre-init-secret-value", name == "API_TOKEN"
	})
	if report, err := ReplayScript(context.Background(), script, ReplayOptions{SecretResolver: resolver, IgnoreThinkTime: true}); err != nil || report == nil || !report.Success {
		t.Fatalf("ReplayScript = report %#v err %v", report, err)
	}
	if err := RunScriptShakeoutWithSecretResolver(context.Background(), "pre-init", script, resolver); err != nil {
		t.Fatalf("RunScriptShakeoutWithSecretResolver: %v", err)
	}
	load, err := New(Config{Script: script, VUsers: 1, Duration: 20 * time.Millisecond, SecretResolver: resolver})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := load.Run(context.Background()); err != nil {
		t.Fatalf("Engine.Run: %v", err)
	}
}

func TestReplayRedactsResolverBackedBasicAuthCompositeEcho(t *testing.T) {
	const encodedCredentials = "YTpi"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic "); got != encodedCredentials {
			t.Errorf("Authorization payload = %q, want %q", got, encodedCredentials)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("neutral echo " + encodedCredentials))
	}))
	defer server.Close()

	script := &dsl.Script{
		Name: "basic auth composite", Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: server.URL,
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: "{{secret.BASIC_USER}}", Password: "{{secret.BASIC_PASSWORD}}",
			}},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: http.MethodGet, Target: "/"}},
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		switch name {
		case "BASIC_USER":
			return "a", true
		case "BASIC_PASSWORD":
			return "b", true
		default:
			return "", false
		}
	})
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		SecretResolver:  resolver,
		IgnoreThinkTime: true,
	})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(encodedCredentials)) {
		t.Fatalf("neutral response disclosed resolver-backed Basic credentials: %s", encoded)
	}
}

// TestReplayRedactsResolverBackedActionAuthCompositeEcho is the action-level
// twin of TestReplayRedactsResolverBackedBasicAuthCompositeEcho. A per-action
// auth block (dsl.Action.Auth, "override global auth") is a second, independent
// surface — the OpenAPI importer emits credentials there, not on the script
// config — and the two must not drift apart.
//
// Both directions of the origin rule are asserted against the same echoed
// payload: a composite built from the project secret store is not the operator's
// to read and stays scrubbed even where the operator turned
// script.Config.Log.RedactRunEvidence OFF, while one built from script literals
// is already in front of them and is disclosed exactly there. The credentials are
// one character each so the outcome measures the exact-value matcher rather than
// the generic high-entropy scrubber.
//
// The setting is a TRI-STATE and the script-literal rows walk all three, because
// unset and explicitly-on are two different readings of the same field and only
// the unset row can catch a regression in that reading. The store-managed rows
// deliberately stop at unset and off: with the setting on, the script-sourced
// half registers this same composite too, so a store-managed "explicitly on" row
// is a duplicate of the unset row that no mutation can distinguish. What origin
// buys under redaction-on is HasRuntimeSecretMaterial (screenshot and HAR
// suppression), which a replay report cannot observe — that half of the contract
// is pinned in script_sourced_redaction_contract_test.go.
func TestReplayRedactsResolverBackedActionAuthCompositeEcho(t *testing.T) {
	const encodedCredentials = "YTpi"
	newScript := func(baseURL string, auth *dsl.AuthConfig, redactRunEvidence *bool) *dsl.Script {
		logConfig := dsl.LogConfig{}
		if redactRunEvidence != nil {
			redact := *redactRunEvidence
			logConfig.RedactRunEvidence = &redact
		}
		return &dsl.Script{
			Name: "action auth composite", Protocol: "http",
			Config: dsl.ScriptConfig{
				BaseURL: baseURL,
				Log:     logConfig,
			},
			Actions: []dsl.Action{{
				Name: "request", Type: "request", Method: http.MethodGet, Target: "/",
				Auth: auth,
			}},
		}
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		switch name {
		case "BASIC_USER":
			return "a", true
		case "BASIC_PASSWORD":
			return "b", true
		default:
			return "", false
		}
	})
	redactOn, redactOff := true, false

	for _, testCase := range []struct {
		name string
		auth *dsl.AuthConfig
		// redactRunEvidence is nil for "the operator never touched the setting",
		// which is the DEFAULT and reads as visible. The three states are
		// distinct on purpose: with a plain bool an omitted field silently meant
		// "explicitly off", so "every configuration" was only ever one of them.
		// The sibling URL-userinfo test uses the same tri-state.
		redactRunEvidence *bool
		disclose          bool
	}{{
		// The shape pkg/convert/openapi emits for an HTTP `basic` scheme: a
		// non-secret username beside a store-managed password. The spaced
		// spelling is what interpolateSecretReferences resolves, so the origin
		// test has to recognize it too. Setting unset = the default.
		name: "secret store password with run evidence redaction unset",
		auth: &dsl.AuthConfig{Type: "basic", Username: "a", Password: "{{ secret.BASIC_PASSWORD }}"},
	}, {
		// Either half being store-managed is enough: the composite embeds it.
		name: "secret store username",
		auth: &dsl.AuthConfig{Type: "basic", Username: "{{secret.BASIC_USER}}", Password: "b"},
	}, {
		// A store-managed composite stays scrubbed even when the operator has
		// turned script-sourced run-evidence redaction OFF. The setting reaches
		// script-sourced material only.
		name:              "secret store password with run evidence redaction off",
		auth:              &dsl.AuthConfig{Type: "basic", Username: "a", Password: "{{ secret.BASIC_PASSWORD }}"},
		redactRunEvidence: &redactOff,
	}, {
		// Script literals with the setting explicitly on: the operator asked for
		// tidy run output, so their own credential is scrubbed from it. Nothing
		// is removed from the script itself.
		name:              "script literal with run evidence redaction on",
		auth:              &dsl.AuthConfig{Type: "basic", Username: "a", Password: "b"},
		redactRunEvidence: &redactOn,
	}, {
		// The same literals with the setting unset take the visible-by-default
		// behavior.
		name:     "script literal with run evidence redaction unset",
		auth:     &dsl.AuthConfig{Type: "basic", Username: "a", Password: "b"},
		disclose: true,
	}, {
		name:              "script literal with run evidence redaction off",
		auth:              &dsl.AuthConfig{Type: "basic", Username: "a", Password: "b"},
		redactRunEvidence: &redactOff,
		disclose:          true,
	}} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic "); got != encodedCredentials {
					t.Errorf("Authorization payload = %q, want %q", got, encodedCredentials)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("neutral echo " + encodedCredentials))
			}))
			defer server.Close()

			script := newScript(server.URL, testCase.auth, testCase.redactRunEvidence)
			report, err := ReplayScript(context.Background(), script, ReplayOptions{
				SecretResolver:  resolver,
				IgnoreThinkTime: true,
			})
			if err != nil {
				t.Fatalf("ReplayScript: %v", err)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case testCase.disclose && !bytes.Contains(encoded, []byte(encodedCredentials)):
				t.Fatalf("script-authored Basic credentials were scrubbed from the operator's own evidence: %s", encoded)
			case !testCase.disclose && bytes.Contains(encoded, []byte(encodedCredentials)):
				t.Fatalf("neutral response disclosed resolver-backed per-action Basic credentials: %s", encoded)
			}
			// The executable auth block is untouched in every configuration.
			if got := script.Actions[0].Auth; got.Username != testCase.auth.Username ||
				got.Password != testCase.auth.Password {
				t.Fatalf("replay rewrote the executable auth block: %#v", got)
			}
		})
	}
}

// TestReplayRedactsResolverBackedURLUserinfoCompositeEcho is the third sibling
// of the two tests above and the one that keeps them from drifting.
//
// Nothing in the engine writes an Authorization header for a target such as
// https://user:password@host/path — net/http synthesises "Basic
// base64(user:password)" from the URL's userinfo inside Client.send. That makes
// URL userinfo an auth surface with no auth block, and the exact-value matcher
// cannot derive base64 of the PAIR from the bare password, so it needs the same
// composite registration dsl.Action.Auth gets.
//
// Both directions of the origin rule are asserted, as in the action-auth twin:
// a composite built from the project secret store is not the operator's to read
// and must be scrubbed in EVERY configuration, while one built from script
// literals is scrubbed only when the operator turns
// script.Config.Log.RedactRunEvidence on. One-character credentials keep the
// outcome a measurement of the exact-value matcher rather than the entropy
// scrubber.
func TestReplayRedactsResolverBackedURLUserinfoCompositeEcho(t *testing.T) {
	const encodedCredentials = "dXNyOmI=" // base64("usr:b")
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		switch name {
		case "URL_USER":
			return "usr", true
		case "BASIC_PASSWORD":
			return "b", true
		default:
			return "", false
		}
	})
	redactOn, redactOff := true, false

	for _, testCase := range []struct {
		name string
		// redactRunEvidence is nil for "the operator never touched the setting",
		// which is the DEFAULT and reads as visible.
		redactRunEvidence *bool
		userinfo          string
		disclose          bool
	}{{
		// The shape the finding reproduced: a literal username beside a
		// store-managed password. The spaced spelling is what
		// interpolateSecretReferences resolves, so the origin test — which runs
		// on the authored URL, the only place origin survives — has to read the
		// userinfo out of a URL net/url itself refuses to parse.
		name:     "secret store password",
		userinfo: "usr:{{ secret.BASIC_PASSWORD }}",
	}, {
		// Either half being store-managed is enough: the composite embeds it.
		name:     "secret store username",
		userinfo: "{{secret.URL_USER}}:b",
	}, {
		name:     "both halves from the secret store",
		userinfo: "{{secret.URL_USER}}:{{secret.BASIC_PASSWORD}}",
	}, {
		// A store-managed composite stays scrubbed even with script-sourced run
		// evidence redaction turned OFF. The setting reaches script-sourced
		// material only.
		name:              "secret store password with run evidence redaction off",
		redactRunEvidence: &redactOff,
		userinfo:          "usr:{{ secret.BASIC_PASSWORD }}",
	}, {
		// The default, and the state of an unset field: the operator asked for
		// nothing, so their own credential remains visible in run output.
		// URL userinfo is an auth surface with no auth block, so it has to obey
		// the setting exactly as dsl.Action.Auth does.
		name:     "script literal with run evidence redaction unset",
		userinfo: "usr:b",
		disclose: true,
	}, {
		name:              "script literal with run evidence redaction on",
		redactRunEvidence: &redactOn,
		userinfo:          "usr:b",
	}, {
		name:              "script literal with run evidence redaction off",
		redactRunEvidence: &redactOff,
		userinfo:          "usr:b",
		disclose:          true,
	}} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic "); got != encodedCredentials {
					t.Errorf("Authorization payload = %q, want %q", got, encodedCredentials)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("neutral echo " + encodedCredentials))
			}))
			defer server.Close()

			target := strings.Replace(server.URL, "http://", "http://"+testCase.userinfo+"@", 1) + "/"
			logConfig := dsl.LogConfig{}
			if testCase.redactRunEvidence != nil {
				redact := *testCase.redactRunEvidence
				logConfig.RedactRunEvidence = &redact
			}
			script := &dsl.Script{
				Name: "url userinfo composite", Protocol: "http",
				Config: dsl.ScriptConfig{Log: logConfig},
				Actions: []dsl.Action{{
					Name: "request", Type: "request", Method: http.MethodGet, Target: target,
				}},
			}
			report, err := ReplayScript(context.Background(), script, ReplayOptions{
				SecretResolver:  resolver,
				IgnoreThinkTime: true,
			})
			if err != nil {
				t.Fatalf("ReplayScript: %v", err)
			}
			if len(report.Snapshots) != 1 {
				t.Fatalf("report.Snapshots = %d entries, want 1", len(report.Snapshots))
			}
			// Assert the whole echoed payload, not just its absence. base64 of
			// a bare half can be a PREFIX of base64 of the pair ("dXNy" for
			// "usr" inside "dXNyOmI="), so a containment check alone passes on
			// a partial mask that still discloses the rest of the composite.
			want := "neutral echo [REDACTED]"
			if testCase.disclose {
				want = "neutral echo " + encodedCredentials
			}
			if got := report.Snapshots[0].ResponseBody; got != want {
				t.Fatalf("snapshot response body = %q, want %q", got, want)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case testCase.disclose && !bytes.Contains(encoded, []byte(encodedCredentials)):
				t.Fatalf("script-authored URL userinfo was scrubbed from the operator's own evidence: %s", encoded)
			case !testCase.disclose && bytes.Contains(encoded, []byte(encodedCredentials)):
				t.Fatalf("neutral response disclosed resolver-backed URL userinfo credentials: %s", encoded)
			}
		})
	}
}

// Only import provenance keys (original_*, source_*) are exempt. Other action
// metadata IS sent on the wire — kafka.buildRecord and jms.buildMessage copy
// metadata into headers/properties — so those secret names must stay in
// ReferencedSecretNames or the runtime RedactionConfig will not scrub them.
func TestReferencedSecretNamesKeepsNonProvenanceActionMetadataSecrets(t *testing.T) {
	script := &dsl.Script{
		Protocol: "kafka",
		Actions: []dsl.Action{
			{
				Name:   "produce",
				Type:   "kafka_produce",
				Target: "orders",
				Metadata: map[string]any{
					"original_url": "https://service.test/{{secret.PROVENANCE_ONLY}}",
					"api_key":      "{{secret.KAFKA_API_KEY}}",
				},
			},
		},
	}

	got, err := ReferencedSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"KAFKA_API_KEY", "PROVENANCE_ONLY"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReferencedSecretNames() = %v, want %v (every metadata secret must be redactable)", got, want)
	}
}
