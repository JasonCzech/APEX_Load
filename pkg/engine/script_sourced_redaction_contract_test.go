package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// runEvidenceRedactionModes drives every origin-contract test through both
// states of the operator setting script.Config.Log.RedactRunEvidence.
//
// The contract each mode pins, in one place so the individual tests do not each
// restate it:
//
//   - redaction on: a script-sourced value is scrubbed out of the run's OUTPUT.
//   - redaction off: the same value survives in that output.
//   - an unset field uses the default-off behavior.
//   - either way: a platform-managed {{secret.NAME}} value is scrubbed, and the
//     durable script keeps every credential byte-exact and runnable.
var runEvidenceRedactionModes = []struct {
	name   string
	redact bool
}{
	{name: "redaction_on", redact: true},
	{name: "redaction_off", redact: false},
}

// setRunEvidenceRedaction stamps the operator setting onto a fixture script.
// Each call takes the address of its own copy so two fixtures can never alias
// one another's setting.
func setRunEvidenceRedaction(script *dsl.Script, redact bool) *dsl.Script {
	if script == nil {
		return nil
	}
	value := redact
	script.Config.Log.RedactRunEvidence = &value
	return script
}

// assertScriptSourcedRunEvidence states the per-value contract once. evidence is
// whatever output surface the caller captured.
func assertScriptSourcedRunEvidence(t *testing.T, label, evidence, value string, redact bool) {
	t.Helper()
	switch {
	case redact && strings.Contains(evidence, value):
		t.Fatalf("%s: script-sourced value %q survived output with redaction on: %q", label, value, evidence)
	case !redact && !strings.Contains(evidence, value):
		t.Fatalf("%s: script-sourced value %q was scrubbed from output with redaction off: %q", label, value, evidence)
	}
}

// runShapedVUser builds a VUser THE WAY A RUN BUILDS ONE.
//
// Every standard entry point — engine.New, NewMultiGroup, ReplayScript,
// RunScriptShakeoutWithSecretResolver — performs the retained-credential locator
// walk exactly once for the whole run, through runtimeSecretValuesForScript, and
// hands the result to every VUser it creates. A fixture that assembles a bare
// &VUser{Script: ...} instead gets the lazy per-VUser walk, which is a shape no
// run ever constructs.
//
// The distinction is not cosmetic: this suite was green while both dynamic
// script-sourced producers were dead on every real run, precisely because its
// fixtures took the lazy path. Going through the real chokepoint means a fixture
// can only be built out of what production actually produces.
func runShapedVUser(t *testing.T, script *dsl.Script) *VUser {
	t.Helper()
	_, _, importedCredentials, err := runtimeSecretValuesForScript(script, nil, nil)
	if err != nil {
		t.Fatalf("runtimeSecretValuesForScript: %v", err)
	}
	if importedCredentials == nil {
		t.Fatal("the run chokepoint handed back no retained-credential inspection")
	}
	return &VUser{
		Script:                       script,
		Variables:                    map[string]string{},
		importedCredentialInspection: importedCredentials,
	}
}

// importedCredentialVariableScript returns an imported script whose Authorization
// header references {{var.<name>}}. That reference is what used to mark the
// variable as an "imported credential" and pull its runtime value into the
// redaction matcher.
func importedCredentialVariableScript(name string) *dsl.Script {
	return credentialPreservingRuntimeScript(dsl.Action{
		Name:   "authorized call",
		Type:   "GET",
		Method: "GET",
		Target: "https://api.example.test/orders",
		HeaderList: dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer {{var." + name + "}}",
		}},
	})
}

// END-TO-END GUARD. Every other test in this file assembles a VUser itself, so
// none of them can see a break in the wiring BETWEEN a run entry point and the
// VUser it builds. This one drives ReplayScript — one of the four standard entry
// points — and measures the report the operator actually reads.
//
// It exists because exactly that break shipped: the entry points marked the
// retained-credential walk done without handing the VUser what the walk produced,
// which left the import-template producer inert on every real run while the
// hand-assembled fixtures kept passing. Any equivalent break fails here.
//
// The evidence it measures is harvested by an EARLIER action than the
// credential-governed one, deliberately. The per-action resolved-credential scope
// (resolved_imported_credential_redaction.go) covers the credential-bearing action
// itself from the moment that action is resolved, so measuring that action alone
// would keep passing with the run-wide import-template producer completely dead.
// Only the run-wide producer, which must be installed before the first action
// executes, can cover the earlier event.
func TestReplayReportFollowsRunEvidenceSettingForTemplateCredential(t *testing.T) {
	const (
		variableName = "opaque_handle"
		handle       = "replay-handle-4471"
	)
	// A short, hyphenated canary: a 24+ character opaque run would be removed by
	// the origin-independent bare-token rule and the assertion would measure
	// nothing.
	if scrubSensitiveTextWithConfig(handle, nil) != handle {
		t.Fatalf("canary %q is removed by an origin-independent scrubber", handle)
	}
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			var delivered atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				echoed := r.URL.Query().Get("handle")
				if token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); token == handle {
					delivered.Store(true)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"echoed":"` + echoed + `"}`))
			}))
			defer server.Close()

			script := setRunEvidenceRedaction(
				importedCredentialVariableScript(variableName),
				mode.redact,
			)
			script.Actions[0].Target = server.URL + "/orders"
			// An ordinary earlier call that happens to echo the same runtime value
			// back into run evidence. Nothing about it is credential-governed.
			script.Actions = append([]dsl.Action{{
				Name:   "earlier call that echoes the credential",
				Type:   "GET",
				Method: "GET",
				Target: server.URL + "/echo?handle={{var." + variableName + "}}",
				Extractors: []dsl.Extractor{{
					Name:       "echoed",
					Source:     "body",
					Type:       "jsonpath",
					Expression: "$.echoed",
				}},
			}}, script.Actions...)
			requireImportedCredentialVariableReference(t, script, variableName)

			report, err := ReplayScript(context.Background(), script, ReplayOptions{
				Variables:       map[string]string{variableName: handle},
				SecretResolver:  emptySecretResolver,
				IgnoreThinkTime: true,
			})
			if err != nil {
				t.Fatalf("ReplayScript: %v", err)
			}
			// Premises: the credential really reached the wire, and the run really
			// harvested it back into evidence. Without both, the redacting mode
			// would pass vacuously.
			if !delivered.Load() {
				t.Fatal("premise failed: the credential template never reached the request")
			}
			if len(report.Events) != 2 || !report.Events[0].Success || !report.Events[1].Success {
				t.Fatalf("premise failed: replay did not execute both actions: %+v", report.Events)
			}
			if _, extracted := report.Events[0].Extractions["echoed"]; !extracted {
				t.Fatalf("premise failed: the echoed credential was not captured as evidence: %+v",
					report.Events[0].Extractions)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("marshal replay report: %v", err)
			}
			assertScriptSourcedRunEvidence(t, "replay report", string(encoded), handle, mode.redact)
			// The executable script is untouched in both modes.
			if got := script.Actions[1].HeaderList[0].Value; got != "Bearer {{var."+variableName+"}}" {
				t.Fatalf("replay rewrote the executable header: %q", got)
			}
		})
	}
}

// A source action assigning a literal from the script is script-sourced material
// even when an import also references that variable from a credential-governed
// header. Whether it is scrubbed out of the run's own output is the operator's
// call; the literal is never removed from the script.
func TestSourceVariableAssignmentFollowsRunEvidenceSetting(t *testing.T) {
	const (
		variableName = "opaque_handle"
		literal      = "source-assigned-4471"
	)
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			script := setRunEvidenceRedaction(
				importedCredentialVariableScript(variableName),
				mode.redact,
			)
			vu := runShapedVUser(t, script)
			// Guard the premise: if the import stopped referencing this variable from
			// a credential-governed field the test would pass vacuously.
			requireImportedCredentialVariableReference(t, vu.Script, variableName)

			vu.setSourceVariable(variableName, literal)

			if _, registered := allRuntimeSecretValues(vu.RedactionConfig)[literal]; registered != mode.redact {
				t.Fatalf("source-action literal %q registered = %v, want %v", literal, registered, mode.redact)
			}
			assertScriptSourcedRunEvidence(
				t,
				"source variable",
				scrubSensitiveTextWithConfig("evidence "+literal, vu.RedactionConfig),
				literal,
				mode.redact,
			)
			if got := vu.Variables[variableName]; got != literal {
				t.Fatalf("executable variable = %q, want the unmodified literal %q", got, literal)
			}
		})
	}
}

// Registering a script-sourced value must not turn binary evidence capture off.
// HasRuntimeSecretMaterial is a PRESENCE test, not a text scrub: protocols read
// it to suppress screenshots and browser HAR/trace capture, which cannot be
// proven clean by exact-value replacement. A script the operator can already read
// is not a reason to lose that evidence, so only platform-managed membership
// counts.
func TestScriptSourcedRedactionKeepsScreenshotAndActivityEvidence(t *testing.T) {
	const (
		variableName   = "opaque_handle"
		literal        = "screenshot-visible-6142"
		platformSecret = "vault-issued-screenshot-3318"
		screenshot     = "iVBORw0KGgoAAAANSUhEUg=="
	)
	script := setRunEvidenceRedaction(importedCredentialVariableScript(variableName), true)
	vu := runShapedVUser(t, script)
	vu.setSourceVariable(variableName, literal)

	if _, registered := allRuntimeSecretValues(vu.RedactionConfig)[literal]; !registered {
		t.Fatalf("premise failed: script-sourced literal %q was not registered with redaction on", literal)
	}
	if vu.RedactionConfig.HasRuntimeSecretMaterial() {
		t.Fatal("script-sourced material alone reported platform-managed runtime material")
	}
	result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
	scrubSensitiveResult(result, vu.RedactionConfig)
	if result.ScreenshotBase64 != screenshot {
		t.Fatalf("screenshot was suppressed by script-sourced redaction: %q", result.ScreenshotBase64)
	}

	// The platform-managed half is unchanged: one project secret in the same
	// matcher still fails screenshot retention closed.
	platform, err := WithSecretValueRedactionChecked(vu.RedactionConfig, []string{platformSecret})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	if !platform.HasRuntimeSecretMaterial() {
		t.Fatal("platform-managed secret did not report runtime material")
	}
	platformResult := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
	scrubSensitiveResult(platformResult, platform)
	if platformResult.ScreenshotBase64 != "" {
		t.Fatal("platform-managed secret no longer suppresses screenshot retention")
	}
	// Both values are still scrubbed out of TEXT evidence; only the presence test
	// distinguishes them.
	if got := scrubSensitiveTextWithConfig(literal+" "+platformSecret, platform); strings.Contains(got, literal) ||
		strings.Contains(got, platformSecret) {
		t.Fatalf("text scrubbing lost a registered value: %q", got)
	}
}

// runtimeSecretActivityProbe is the browser adapter's half of the same presence
// test: SetRuntimeSecretActivity gates HAR and trace capture, which are binary
// artifacts that cannot be text-scrubbed.
type runtimeSecretActivityProbe struct {
	activity func() bool
}

func (*runtimeSecretActivityProbe) Name() string { return "activity-probe" }
func (*runtimeSecretActivityProbe) Init(context.Context, map[string]any) error {
	return nil
}
func (*runtimeSecretActivityProbe) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*runtimeSecretActivityProbe) Teardown(context.Context) error    { return nil }
func (*runtimeSecretActivityProbe) HealthCheck(context.Context) error { return nil }
func (probe *runtimeSecretActivityProbe) SetRuntimeSecretActivity(activity func() bool) {
	probe.activity = activity
}

// The browser HAR/trace suppression gate reads the same presence test as
// screenshot retention, through the protocol capability rather than the result
// scrubber. Enabling script-sourced run-evidence redaction must not switch HAR
// capture off for a script that captures it today.
func TestScriptSourcedRedactionKeepsBrowserActivityCaptureEnabled(t *testing.T) {
	const (
		variableName   = "opaque_handle"
		literal        = "har-visible-2264"
		platformSecret = "vault-issued-har-9931"
	)
	probe := &runtimeSecretActivityProbe{}
	vu := runShapedVUser(t, setRunEvidenceRedaction(importedCredentialVariableScript(variableName), true))
	vu.Proto = probe
	vu.setSourceVariable(variableName, literal)
	vu.bindRuntimeTextRedactor()
	if probe.activity == nil {
		t.Fatal("runtime secret activity gate was never installed")
	}
	if _, registered := allRuntimeSecretValues(vu.RedactionConfig)[literal]; !registered {
		t.Fatalf("premise failed: script-sourced literal %q was not registered with redaction on", literal)
	}
	if probe.activity() {
		t.Fatal("script-sourced redaction suppressed browser HAR/trace capture")
	}

	// A project secret in the same matcher still suppresses it.
	promoted, err := WithSecretValueRedactionChecked(vu.RedactionConfig, []string{platformSecret})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	vu.RedactionConfig = promoted
	vu.bindRuntimeTextRedactor()
	if !probe.activity() {
		t.Fatal("platform-managed secret no longer suppresses browser HAR/trace capture")
	}
}

// A project secret that happens to equal a retained import literal must be
// treated as platform-managed everywhere, including the presence test.
func TestPlatformManagedRegistrationOutranksScriptSourcedMembership(t *testing.T) {
	const shared = "shared-literal-5527"
	scriptSourced, err := WithScriptSourcedSecretValueRedaction(nil, []string{shared})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	if scriptSourced.HasRuntimeSecretMaterial() {
		t.Fatal("script-sourced registration reported platform-managed material")
	}
	promoted, err := WithSecretValueRedactionChecked(scriptSourced, []string{shared})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	if !promoted.HasRuntimeSecretMaterial() {
		t.Fatal("platform-managed registration of an already script-sourced literal did not promote it")
	}
	if got := scrubSensitiveTextWithConfig("evidence "+shared, promoted); strings.Contains(got, shared) {
		t.Fatalf("promoted value survived text scrubbing: %q", got)
	}
}

// The debugger explains a failed predicate with the value it actually compared.
// Import provenance alone no longer downgrades the operand to its template text;
// a project-secret reference still does.
func TestConditionEvidenceOperandKeepsImportedValueAndHidesProjectSecret(t *testing.T) {
	const (
		variableName = "opaque_handle"
		resolved     = "condition-operand-6620"
		secretValue  = "platform-condition-secret-8133"
	)
	vu := &VUser{
		Script:          importedCredentialVariableScript(variableName),
		Variables:       map[string]string{},
		RedactionConfig: WithSecretValueRedaction(nil, []string{secretValue}),
	}
	requireImportedCredentialVariableReference(t, vu.Script, variableName)

	// A deliberately neutral name: a generically sensitive name such as
	// "session_token" would be downgraded by scrub.SensitiveName regardless of
	// provenance and would hide what this test measures.
	raw := "{{var." + variableName + "}}"
	if got := vu.conditionEvidenceOperand(raw, resolved); got != resolved {
		t.Fatalf("imported-variable operand = %q, want evaluated value %q", got, resolved)
	}

	// The platform-managed half is unchanged: a {{secret.*}} operand never
	// reports its resolved value.
	got := vu.conditionEvidenceOperand("{{secret.CONDITION_SECRET}}", secretValue)
	if strings.Contains(got, secretValue) {
		t.Fatalf("project-secret operand leaked its resolved value: %q", got)
	}
	if got != "{{secret.CONDITION_SECRET}}" {
		t.Fatalf("project-secret operand = %q, want author-facing template text", got)
	}
}

// The template plan records EVERY namespace, "secret.NAME" included, so any
// registration built on it has to re-establish origin itself. If
// scriptSourcedTemplateExpressions ever stopped filtering, a platform-managed
// value would be swept into a script-sourced scope where the operator setting
// could switch its protection off.
func TestScriptSourcedTemplateExpressionsExcludeManagedNamespaces(t *testing.T) {
	plan := &importedCredentialTemplatePlan{expressions: map[string]struct{}{
		"var.handle":       {},
		"param.row":        {},
		"data.users.token": {},
		"env.API_KEY":      {},
		"secret.PLATFORM":  {},
		"file.payload":     {},
		"fn.uuid":          {},
	}}
	// Guard the premise this whole filter exists for.
	if _, exists := plan.expressions["secret.PLATFORM"]; !exists {
		t.Fatal("fixture premise lost: the plan no longer carries a secret namespace")
	}
	got := scriptSourcedTemplateExpressions(plan)
	want := []string{"data.users.token", "env.API_KEY", "param.row", "var.handle"}
	if len(got) != len(want) {
		t.Fatalf("script-sourced expressions = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("script-sourced expressions = %#v, want %#v", got, want)
		}
	}
}

// The import-template producer resolves the script-sourced namespaces an import
// used from a credential-governed field. It is rebuilt on every data-row refresh,
// so a VUser reused across iterations cannot keep a stale registration — and a
// {{secret.*}} reference in the same plan is never swept in with them.
func TestImportedCredentialTemplateScopeRegistersScriptSourcedNamespaces(t *testing.T) {
	const (
		variableName = "opaque_handle"
		firstRow     = "template-row-one-5518"
		secondRow    = "template-row-two-2094"
	)
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			script := setRunEvidenceRedaction(importedCredentialVariableScript(variableName), mode.redact)
			requireImportedCredentialVariableReference(t, script, variableName)
			vu := runShapedVUser(t, script)
			vu.Variables[variableName] = firstRow

			if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
				t.Fatalf("refreshImportedCredentialRuntimeValues: %v", err)
			}
			if _, registered := allRuntimeSecretValues(vu.RedactionConfig)[firstRow]; registered != mode.redact {
				t.Fatalf("template value %q registered = %v, want %v", firstRow, registered, mode.redact)
			}
			assertScriptSourcedRunEvidence(
				t,
				"import template",
				scrubSensitiveTextWithConfig("evidence "+firstRow, vu.RedactionConfig),
				firstRow,
				mode.redact,
			)

			// The next iteration's data row replaces the scope wholesale.
			vu.Variables[variableName] = secondRow
			if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
				t.Fatalf("refreshImportedCredentialRuntimeValues: %v", err)
			}
			if _, stale := allRuntimeSecretValues(vu.RedactionConfig)[firstRow]; stale {
				t.Fatalf("stale template value %q survived an iteration refresh", firstRow)
			}
			if _, registered := allRuntimeSecretValues(vu.RedactionConfig)[secondRow]; registered != mode.redact {
				t.Fatalf("refreshed template value %q registered = %v, want %v", secondRow, registered, mode.redact)
			}
		})
	}
}

// The import-template producer reads {{var.NAME}} out of the SAME variable map
// that response extractions land in, so it has to tell the two fillers apart.
//
// An import referencing a variable from a credential-governed field is import
// PROVENANCE, and provenance is deliberately not a reason to hide what the system
// under test returned from the operator who owns the import — the plan's
// `variables` index was deleted for exactly that reason. A response value is
// governed by its own NAME instead, origin-independently. Both halves are pinned
// here because the producer resolves them through one lookup: covering the
// script-sourced filler by resolving the whole variable map would silently
// reinstate the rejected behaviour.
func TestImportTemplateScopeSkipsResponseExtractionsButKeepsScriptFills(t *testing.T) {
	const (
		variableName = "opaque_handle"
		scriptFill   = "data-row-fill-7413"
		harvested    = "response-harvest-2288"
		namedToken   = "named-harvest-9910"
	)
	for _, canary := range []string{scriptFill, harvested, namedToken} {
		if scrubSensitiveTextWithConfig(canary, nil) != canary {
			t.Fatalf("canary %q is removed by an origin-independent scrubber", canary)
		}
	}
	script := setRunEvidenceRedaction(importedCredentialVariableScript(variableName), true)
	requireImportedCredentialVariableReference(t, script, variableName)

	// A script-sourced filler — a data row, a parameter, an operator-supplied
	// replay variable — is what the producer exists for.
	filled := runShapedVUser(t, script)
	filled.Variables[variableName] = scriptFill
	if err := filled.refreshImportedCredentialRuntimeValues(); err != nil {
		t.Fatalf("refreshImportedCredentialRuntimeValues: %v", err)
	}
	if _, registered := allRuntimeSecretValues(filled.RedactionConfig)[scriptFill]; !registered {
		t.Fatalf("script-sourced fill %q of a credential-governed variable was not registered", scriptFill)
	}

	// The same variable filled from a RESPONSE is the rejected case.
	harvestedVU := runShapedVUser(t, script)
	if err := harvestedVU.mergeExtractions(&protocol.Result{Extractions: map[string]string{
		variableName:    harvested,
		"session_token": namedToken,
	}}); err != nil {
		t.Fatalf("mergeExtractions: %v", err)
	}
	if err := harvestedVU.refreshImportedCredentialRuntimeValues(); err != nil {
		t.Fatalf("refreshImportedCredentialRuntimeValues: %v", err)
	}
	if got := harvestedVU.Variables[variableName]; got != harvested {
		t.Fatalf("premise failed: extracted variable = %q, want %q", got, harvested)
	}
	registered := allRuntimeSecretValues(harvestedVU.RedactionConfig)
	if _, hidden := registered[harvested]; hidden {
		t.Fatalf("a response value %q was hidden from the operator on import provenance alone", harvested)
	}
	// The origin-independent NAME rule is untouched, and stays platform-managed.
	if _, hidden := registered[namedToken]; !hidden {
		t.Fatalf("a sensitively named extraction %q lost its registration", namedToken)
	}
	if !harvestedVU.RedactionConfig.HasRuntimeSecretMaterial() {
		t.Fatal("a sensitively named extraction stopped reporting platform-managed runtime material")
	}
}

// Groups in one run may disagree about the setting. The multi-group matcher cache
// keys on the value sets, so origin has to be part of that key or the second
// group would silently inherit the first group's policy.
func TestMultiGroupSharedMatcherKeepsPerGroupRunEvidenceSetting(t *testing.T) {
	// Deliberately short canaries: a 24+ character opaque run trips the generic
	// bare-token rule, which is an origin-independent layer and would satisfy
	// every assertion here without the exact-value matcher doing anything.
	const (
		retained       = "mg-pin-4720"
		platformSecret = "mg-plat-8135"
	)
	for _, canary := range []string{retained, platformSecret} {
		if scrubSensitiveTextWithConfig(canary, nil) != canary {
			t.Fatalf("canary %q is removed by an origin-independent scrubber", canary)
		}
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "PLATFORM_TOKEN" {
			return platformSecret, true
		}
		return "", false
	})
	newScript := func(redact bool) *dsl.Script {
		return setRunEvidenceRedaction(credentialPreservingRuntimeScript(dsl.Action{
			Name:   "verify",
			Type:   "POST",
			Method: "POST",
			Target: "https://api.example.test/verify",
			HeaderList: dsl.HeaderList{
				{Name: "Content-Type", Value: "application/json"},
				{Name: "X-Platform", Value: "{{secret.PLATFORM_TOKEN}}"},
			},
			Body: `{"pin":"` + retained + `"}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "body", "pointer": "/pin"},
				},
			},
		}), redact)
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{{
			Name: "redacting", Script: newScript(true), VUsers: 1, SecretResolver: resolver,
		}, {
			Name: "disclosing", Script: newScript(false), VUsers: 1, SecretResolver: resolver,
		}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	for _, group := range mge.groups {
		redact := group.Name == "redacting"
		// A neutral carrier: naming the fields "pin"/"token" would let the generic
		// payload regexes redact them regardless of matcher membership.
		evidence := scrubSensitiveTextWithConfig(
			"one "+retained+" two "+platformSecret,
			group.RedactionConfig,
		)
		if strings.Contains(evidence, platformSecret) {
			t.Fatalf("group %q leaked the platform-managed secret: %q", group.Name, evidence)
		}
		assertScriptSourcedRunEvidence(t, "group "+group.Name, evidence, retained, redact)
	}
}

// B3: the setting must never fail a run. A script-sourced set that overruns the
// runtime matcher drops the script-sourced contribution and warns; every
// platform-managed value keeps its protection and its fail-closed behaviour.
func TestOversizedScriptSourcedSetDropsInsteadOfFailingTheVUser(t *testing.T) {
	const (
		platformSecret = "platform-overflow-secret-7742"
		scriptLiteral  = "script-overflow-literal-1183"
	)
	oversized := make([]string, maxRuntimeSecretRedactionValues+1)
	for index := range oversized {
		oversized[index] = "script-overflow-" + strconv.Itoa(index)
	}
	oversized[0] = scriptLiteral

	vu := &VUser{
		Script:          setRunEvidenceRedaction(credentialPreservingRuntimeScript(dsl.Action{Name: "noop"}), true),
		RedactionConfig: WithSecretValueRedaction(nil, []string{platformSecret}),
	}
	if err := vu.replaceRuntimeCredentialScopes(map[string][]string{
		runtimeCredentialTemplateScope: oversized,
		runtimeCredentialActionScope:   {"platform-overflow-composite-5561"},
	}); err != nil {
		t.Fatalf("oversized script-sourced set failed the run: %v", err)
	}
	if err := vu.runtimeCredentialRedactionError(); err != nil {
		t.Fatalf("oversized script-sourced set poisoned the VUser: %v", err)
	}
	if !vu.scriptSourcedRedactionDropped {
		t.Fatal("oversized script-sourced set was accepted without surfacing a warning")
	}
	registered := allRuntimeSecretValues(vu.RedactionConfig)
	if _, exists := registered[scriptLiteral]; exists {
		t.Fatal("script-sourced value survived registration after the drop")
	}
	for _, value := range []string{platformSecret, "platform-overflow-composite-5561"} {
		if _, exists := registered[value]; !exists {
			t.Fatalf("platform-managed value %q lost its registration when the script-sourced half was dropped", value)
		}
		if got := scrubSensitiveTextWithConfig("evidence "+value, vu.RedactionConfig); strings.Contains(got, value) {
			t.Fatalf("platform-managed value %q survived evidence after the drop: %q", value, got)
		}
	}

	// The platform-managed half keeps today's fail-closed behaviour exactly.
	failing := &VUser{Script: vu.Script}
	if err := failing.replaceRuntimeCredentialScope(runtimeCredentialActionScope, oversized); err == nil {
		t.Fatal("an oversized PLATFORM-managed set was accepted; it must still fail closed")
	}
	if failing.scriptSourcedRedactionDropped {
		t.Fatal("a platform-managed overflow was reported as a script-sourced drop")
	}
}

// The multi-group aggregate matcher budget has the same rule: platform-managed
// values still fail NewMultiGroup closed, script-sourced values are dropped.
func TestAggregateMatcherBudgetDropsScriptSourcedBeforeFailing(t *testing.T) {
	const (
		platformSecret = "aggregate-platform-secret-3391"
		scriptLiteral  = "aggregate-script-literal-8827"
	)
	platformOnly, err := WithSecretValueRedactionChecked(nil, []string{platformSecret})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	// A budget that fits the platform-managed matcher and nothing more.
	budget := platformOnly.runtimePatternBytes

	config, dropped, err := buildBoundedRuntimeSecretRedaction(
		[]string{platformSecret},
		[]string{scriptLiteral},
		budget,
	)
	if err != nil {
		t.Fatalf("aggregate budget failed the run for a script-sourced value: %v", err)
	}
	if !dropped {
		t.Fatal("script-sourced values fit a budget sized for the platform-managed matcher alone")
	}
	if got := scrubSensitiveTextWithConfig(platformSecret, config); strings.Contains(got, platformSecret) {
		t.Fatalf("platform-managed value lost protection when the script-sourced half was dropped: %q", got)
	}
	if got := scrubSensitiveTextWithConfig(scriptLiteral, config); !strings.Contains(got, scriptLiteral) {
		t.Fatalf("dropped script-sourced value was still redacted: %q", got)
	}

	// One byte less than the platform-managed matcher needs still fails closed.
	if _, _, err := buildBoundedRuntimeSecretRedaction([]string{platformSecret}, nil, budget-1); err == nil {
		t.Fatal("an over-budget PLATFORM-managed matcher was accepted; it must still fail closed")
	}
}

// The static chokepoint reads the setting; the fail-closed locator walk inside it
// does not.
func TestRuntimeSecretValuesForScriptReadsTheOperatorSetting(t *testing.T) {
	const retained = "static-chokepoint-credential-6604"
	newScript := func(redact bool) *dsl.Script {
		return setRunEvidenceRedaction(credentialPreservingRuntimeScript(dsl.Action{
			Name:   "verify",
			Type:   "POST",
			Method: "POST",
			Target: "https://api.example.test/verify",
			HeaderList: dsl.HeaderList{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: `{"pin":"` + retained + `"}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "body", "pointer": "/pin"},
				},
			},
		}), redact)
	}
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			platform, scriptSourced, _, err := runtimeSecretValuesForScript(newScript(mode.redact), nil, nil)
			if err != nil {
				t.Fatalf("runtimeSecretValuesForScript: %v", err)
			}
			if containsExactString(platform, retained) {
				t.Fatalf("script-sourced credential leaked into the platform-managed set: %#v", platform)
			}
			if containsExactString(scriptSourced, retained) != mode.redact {
				t.Fatalf("script-sourced set = %#v, want retained credential present = %v", scriptSourced, mode.redact)
			}
		})
	}

	// The locator walk is a corruption gate, not a redaction feature: a stale
	// locator fails closed in BOTH modes.
	for _, mode := range runEvidenceRedactionModes {
		t.Run("stale_locator_"+mode.name, func(t *testing.T) {
			script := newScript(mode.redact)
			script.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = []any{
				map[string]any{"field": "body", "pointer": "/removed"},
			}
			if _, _, _, err := runtimeSecretValuesForScript(script, nil, nil); err == nil {
				t.Fatal("a stale credential locator was accepted")
			}
		})
	}
}

// retainedBodyCredentialScript returns an import-preserving script whose body
// carries one retained literal behind a credential locator. It is the shape every
// origin test below needs: script-sourced material, no {{secret.*}} reference.
func retainedBodyCredentialScript(retained string) *dsl.Script {
	return credentialPreservingRuntimeScript(dsl.Action{
		Name:   "verify",
		Type:   "POST",
		Method: "POST",
		Target: "https://api.example.test/verify",
		HeaderList: dsl.HeaderList{
			{Name: "Content-Type", Value: "application/json"},
		},
		Body: `{"pin":"` + retained + `"}`,
		Metadata: map[string]any{
			dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
				map[string]any{"field": "body", "pointer": "/pin"},
			},
		},
	})
}

// emptySecretResolver keeps NewMultiGroup off the project default resolver so
// these tests measure origin accounting and nothing else.
var emptySecretResolver = SecretResolverFunc(func(string) (string, bool) { return "", false })

// ORIGIN IS A PROPERTY OF THE VALUE'S SOURCE and has to survive every
// re-registration, including the process hop onto a distributed generator.
//
// prepareAssignmentStage (pkg/distributed/generatorrpc/execution_staging.go)
// registers the script-sourced half itself and hands the result to NewMultiGroup
// as GroupConfig.RedactionConfig. NewMultiGroup used to fold every inbound value
// into the PLATFORM-managed half, which rebuilt the script-sourced values as
// platform-managed and turned HasRuntimeSecretMaterial() on — so the same script
// kept its screenshot and HAR evidence locally and lost it on the fleet, the exact
// coupling B4 forbids. The inbound config is the only place that origin still
// exists, so it has to be read, not flattened.
func TestInboundGroupRedactionConfigKeepsScriptSourcedOrigin(t *testing.T) {
	const (
		retained   = "staged-pin-4913"
		bundle     = "bundle-issued-7250"
		screenshot = "iVBORw0KGgoAAAANSUhEUg=="
	)
	for _, canary := range []string{retained, bundle} {
		if scrubSensitiveTextWithConfig(canary, nil) != canary {
			t.Fatalf("canary %q is removed by an origin-independent scrubber", canary)
		}
	}
	// Reproduce the staging sequence exactly: no proto redaction rules, an empty
	// secret bundle, then the script-sourced registration at
	// execution_staging.go:908-920.
	stagedRedaction := func(t *testing.T, script *dsl.Script, bundleValues []string) *RedactionConfig {
		t.Helper()
		config, err := WithSecretValueRedactionChecked(nil, bundleValues)
		if err != nil {
			t.Fatalf("WithSecretValueRedactionChecked: %v", err)
		}
		values, err := ImportedCredentialValues(script)
		if err != nil {
			t.Fatalf("ImportedCredentialValues: %v", err)
		}
		if len(values) == 0 {
			t.Fatal("fixture premise lost: the script retains no credential values")
		}
		config, err = WithScriptSourcedSecretValueRedaction(config, values)
		if err != nil {
			t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
		}
		return config
	}
	groupRedaction := func(t *testing.T, inbound *RedactionConfig, script *dsl.Script) *RedactionConfig {
		t.Helper()
		mge, err := NewMultiGroup(MultiGroupConfig{
			Duration: time.Second,
			Groups: []GroupConfig{{
				Name:            "generator",
				Script:          script,
				VUsers:          1,
				SecretResolver:  emptySecretResolver,
				RedactionConfig: inbound,
			}},
		})
		if err != nil {
			t.Fatalf("NewMultiGroup: %v", err)
		}
		return mge.groups[0].RedactionConfig
	}

	t.Run("script_sourced_only", func(t *testing.T) {
		script := setRunEvidenceRedaction(retainedBodyCredentialScript(retained), true)
		staged := stagedRedaction(t, script, nil)
		// Premise: the generator got the origin right before the hop.
		if staged.HasRuntimeSecretMaterial() {
			t.Fatal("premise failed: staging reported platform-managed material for a script-sourced value")
		}
		config := groupRedaction(t, staged, script)
		if config.HasRuntimeSecretMaterial() {
			t.Fatal("a distributed generator re-registered script-sourced values as platform-managed")
		}
		result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
		scrubSensitiveResult(result, config)
		if result.ScreenshotBase64 != screenshot {
			t.Fatalf("distributed run lost its screenshot to script-sourced redaction: %q", result.ScreenshotBase64)
		}
		// The local path must agree, value for value.
		local := groupRedaction(t, nil, script)
		if local.HasRuntimeSecretMaterial() {
			t.Fatal("the local path reported platform-managed material for a script-sourced value")
		}
		// Losing the promotion must not cost the value its text scrubbing.
		if got := scrubSensitiveTextWithConfig("evidence "+retained, config); strings.Contains(got, retained) {
			t.Fatalf("script-sourced value survived distributed text evidence: %q", got)
		}
	})

	t.Run("platform_managed_bundle_still_suppresses_evidence", func(t *testing.T) {
		script := setRunEvidenceRedaction(retainedBodyCredentialScript(retained), true)
		config := groupRedaction(t, stagedRedaction(t, script, []string{bundle}), script)
		if !config.HasRuntimeSecretMaterial() {
			t.Fatal("a platform-managed secret bundle value stopped suppressing binary evidence")
		}
		result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
		scrubSensitiveResult(result, config)
		if result.ScreenshotBase64 != "" {
			t.Fatal("a platform-managed secret bundle value no longer suppresses screenshot retention")
		}
		if got := scrubSensitiveTextWithConfig(retained+" "+bundle, config); strings.Contains(got, retained) ||
			strings.Contains(got, bundle) {
			t.Fatalf("text scrubbing lost a registered value: %q", got)
		}
	})

	// A group that carries its own policy takes attachRuntimeSecretRedaction's
	// detached branch, which has to copy the origin set alongside the values.
	t.Run("custom_policy_rules_keep_the_origin_set", func(t *testing.T) {
		const canary = "custom-rule-canary-2841"
		policy, err := NewRedactionConfig([]RedactionRule{{
			Name: "canary", Pattern: canary, Replacement: "[CUSTOM]",
		}})
		if err != nil {
			t.Fatalf("NewRedactionConfig: %v", err)
		}
		script := setRunEvidenceRedaction(retainedBodyCredentialScript(retained), true)
		config := groupRedaction(t, policy, script)
		if config.HasRuntimeSecretMaterial() {
			t.Fatal("a group with custom redaction rules lost the script-sourced origin set")
		}
		result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
		scrubSensitiveResult(result, config)
		if result.ScreenshotBase64 != screenshot {
			t.Fatalf("a group with custom redaction rules lost its screenshot: %q", result.ScreenshotBase64)
		}
		if got := scrubSensitiveTextWithConfig(canary+" "+retained, config); strings.Contains(got, canary) ||
			strings.Contains(got, retained) {
			t.Fatalf("attached policy lost a rule or a runtime value: %q", got)
		}
	})
}

// A platform-managed literal is never demoted, whichever order the two origins
// are registered in. Production always registers platform-managed FIRST
// (buildRuntimeSecretRedaction, buildBoundedRuntimeSecretRedaction, and the
// generator staging boundary), so that ordering is the one that matters — and the
// single-value case short-circuits before the marking loop, so the script-sourced
// half has to contribute at least one NEW value to reach it.
func TestScriptSourcedRegistrationNeverDemotesAPlatformManagedLiteral(t *testing.T) {
	const (
		shared     = "shared-literal-3074"
		other      = "script-only-literal-8816"
		screenshot = "iVBORw0KGgoAAAANSUhEUg=="
	)
	platform, err := WithSecretValueRedactionChecked(nil, []string{shared})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	if !platform.HasRuntimeSecretMaterial() {
		t.Fatal("premise failed: a platform-managed value did not report runtime material")
	}
	extended, err := WithScriptSourcedSecretValueRedaction(platform, []string{shared, other})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	if !extended.HasRuntimeSecretMaterial() {
		t.Fatal("a script-sourced registration demoted an already platform-managed literal")
	}
	result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
	scrubSensitiveResult(result, extended)
	if result.ScreenshotBase64 != "" {
		t.Fatal("a demoted platform-managed literal stopped suppressing screenshot retention")
	}

	// The same ordering through the shared static chokepoint: a partially migrated
	// script whose {{secret.NAME}} value equals one of its retained literals.
	config, dropped, err := buildRuntimeSecretRedaction(nil, []string{shared}, []string{shared, other})
	if err != nil {
		t.Fatalf("buildRuntimeSecretRedaction: %v", err)
	}
	if dropped {
		t.Fatal("the script-sourced half was dropped for a two-value set")
	}
	if !config.HasRuntimeSecretMaterial() {
		t.Fatal("the static chokepoint demoted a platform-managed literal to script-sourced")
	}
	if got := scrubSensitiveTextWithConfig(shared+" "+other, config); strings.Contains(got, shared) ||
		strings.Contains(got, other) {
		t.Fatalf("text scrubbing lost a registered value: %q", got)
	}
}

// The per-VUser funnel drops values the shared base matcher already covers. That
// filter has to read ORIGIN: a value the base carries only as script-sourced is
// not covered at platform strength, so a platform-managed producer's registration
// of the same literal must still reach the overlay and promote it.
//
// "extraction:<name>" is such a producer (vuser_resolve.go registers it for any
// extraction whose NAME is sensitive, independently of where the value came from).
func TestPlatformManagedScopePromotesAValueTheBaseCarriesScriptSourced(t *testing.T) {
	const (
		shared     = "extraction-collision-5140"
		screenshot = "iVBORw0KGgoAAAANSUhEUg=="
	)
	base, err := WithScriptSourcedSecretValueRedaction(nil, []string{shared})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	if base.HasRuntimeSecretMaterial() {
		t.Fatal("premise failed: a script-sourced base reported platform-managed material")
	}
	vu := runShapedVUser(t, setRunEvidenceRedaction(retainedBodyCredentialScript(shared), true))
	vu.RedactionConfig = base
	if err := vu.replaceRuntimeCredentialScope("extraction:session_token", []string{shared}); err != nil {
		t.Fatalf("replaceRuntimeCredentialScope: %v", err)
	}
	if !vu.RedactionConfig.HasRuntimeSecretMaterial() {
		t.Fatal("a platform-managed runtime scope was dropped because the base already carried the value script-sourced")
	}
	result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
	scrubSensitiveResult(result, vu.RedactionConfig)
	if result.ScreenshotBase64 != "" {
		t.Fatal("a sensitively named extraction no longer suppresses screenshot retention")
	}
	if got := scrubSensitiveTextWithConfig("evidence "+shared, vu.RedactionConfig); strings.Contains(got, shared) {
		t.Fatalf("promoted value survived text scrubbing: %q", got)
	}
}

// PRODUCER-SIDE ORIGIN. publishRuntimeCredentialRedaction reads origin off the
// SCOPE NAME alone (isScriptSourcedRuntimeCredentialScope tests one prefix), so a
// producer's choice of name IS the security decision — and it is a decision no
// test sees unless the operator setting is OFF, because with it on both origins
// scrub identically.
//
// These two tests drive the three platform-managed producers that had no such
// test through their real entry points, with script.Config.Log.RedactRunEvidence
// explicitly false. Each asserts BOTH platform-managed invariants: the value is
// still scrubbed (the setting never reaches platform-managed material) and
// HasRuntimeSecretMaterial() is true (so screenshot/HAR suppression still fires).
// Filing any of these under the "script:" prefix by mistake would hand a value
// harvested from the system under test to a cosmetic run-output toggle.
//
// "extraction:<name>" — vuser_resolve.go registers it for any extraction whose
// NAME is sensitive, whatever the value's provenance.
func TestSensitiveExtractionStaysPlatformManagedWithRunEvidenceRedactionOff(t *testing.T) {
	const (
		harvested  = "harvested-session-2703"
		screenshot = "iVBORw0KGgoAAAANSUhEUg=="
	)
	if scrubSensitiveTextWithConfig(harvested, nil) != harvested {
		t.Fatalf("canary %q is removed by an origin-independent scrubber", harvested)
	}
	script := setRunEvidenceRedaction(retainedBodyCredentialScript("body-literal-8840"), false)
	vu := runShapedVUser(t, script)
	// Premise: the operator really did turn script-sourced run-evidence redaction
	// off, so anything still redacted below is redacted because it is
	// platform-managed.
	if scriptRedactsRunEvidence(vu.Script) {
		t.Fatal("premise failed: the fixture still redacts script-sourced run evidence")
	}
	if err := vu.mergeExtractions(&protocol.Result{
		Extractions: map[string]string{"session_token": harvested},
	}); err != nil {
		t.Fatalf("mergeExtractions: %v", err)
	}
	if got := scrubSensitiveTextWithConfig("evidence "+harvested, vu.RedactionConfig); strings.Contains(got, harvested) {
		t.Fatalf("a harvested sensitive extraction was disclosed when the operator turned redact_run_evidence off: %q", got)
	}
	if !vu.RedactionConfig.HasRuntimeSecretMaterial() {
		t.Fatal("a harvested sensitive extraction stopped reporting platform-managed runtime material")
	}
	result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
	scrubSensitiveResult(result, vu.RedactionConfig)
	if result.ScreenshotBase64 != "" {
		t.Fatal("a harvested sensitive extraction no longer suppresses screenshot retention")
	}
}

// runtimeCredentialOAuthURLScope and "oauth:<save_as>" — the token_url userinfo
// composite and the access token an http_oauth2_token action fetches.
// handleHTTPOAuth2TokenAction builds no protocol.Action, so the resolved-action
// path never sees either one.
func TestOAuth2ProducersStayPlatformManagedWithRunEvidenceRedactionOff(t *testing.T) {
	const (
		userinfoUser     = "usr"
		userinfoPassword = "pw41"
		accessToken      = "fetched-handle-3162"
		screenshot       = "iVBORw0KGgoAAAANSUhEUg=="
	)
	// net/http synthesises this from token_url's userinfo; the exact-value matcher
	// cannot derive it from the password alone, which is why the composite is
	// registered separately. The halves are short enough that the composite stays
	// under the origin-independent bare-token rule, and long enough not to be
	// one character: a one-character secret is registered as an exact value and
	// would blank single letters out of every other canary, hiding what these
	// assertions measure.
	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(userinfoUser + ":" + userinfoPassword))
	for _, canary := range []string{encodedCredentials, accessToken} {
		if scrubSensitiveTextWithConfig(canary, nil) != canary {
			t.Fatalf("canary %q is removed by an origin-independent scrubber", canary)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()

	script := setRunEvidenceRedaction(retainedBodyCredentialScript("body-literal-5529"), false)
	script.Protocol = "http"
	vu := runShapedVUser(t, script)
	vu.SecretResolver = SecretResolverFunc(func(name string) (string, bool) {
		return userinfoPassword, name == "BASIC_PASSWORD"
	})
	if scriptRedactsRunEvidence(vu.Script) {
		t.Fatal("premise failed: the fixture still redacts script-sourced run evidence")
	}
	tokenURL := strings.Replace(
		server.URL, "http://", "http://"+userinfoUser+":{{secret.BASIC_PASSWORD}}@", 1,
	) + "/token"
	if err := vu.handleHTTPOAuth2TokenAction(context.Background(), dsl.Action{
		Name: "token", Type: "http_oauth2_token",
		Params: map[string]any{
			"token_url":     tokenURL,
			"grant_type":    "client_credentials",
			"client_id":     "id",
			"client_secret": "shh",
			"save_as":       "handle",
		},
	}); err != nil {
		t.Fatalf("handleHTTPOAuth2TokenAction: %v", err)
	}
	// Premise: the token really was fetched and stored, so the assertions below
	// measure a live value.
	if got := vu.Variables["handle"]; got != accessToken {
		t.Fatalf("premise failed: fetched access token = %q, want %q", got, accessToken)
	}
	for label, value := range map[string]string{
		"token_url userinfo composite": encodedCredentials,
		"fetched access token":         accessToken,
	} {
		if got := scrubSensitiveTextWithConfig("evidence "+value, vu.RedactionConfig); strings.Contains(got, value) {
			t.Fatalf("%s was disclosed when the operator turned redact_run_evidence off: %q", label, got)
		}
	}
	if !vu.RedactionConfig.HasRuntimeSecretMaterial() {
		t.Fatal("the OAuth producers stopped reporting platform-managed runtime material")
	}
	result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
	scrubSensitiveResult(result, vu.RedactionConfig)
	if result.ScreenshotBase64 != "" {
		t.Fatal("the OAuth producers no longer suppress screenshot retention")
	}
}

// The per-VUser overlay's LAYERED branch is the one production takes for any
// imported script: engine.New installs a non-nil base, so withRuntimeSecretValueOverlay
// never reaches its base==nil shortcut. The script-sourced subset has to be carried
// onto that layered config, or every screenshot and browser HAR/trace on an
// imported script silently stops being captured.
func TestLayeredPerVUserOverlayKeepsScriptSourcedOrigin(t *testing.T) {
	const (
		variableName = "opaque_handle"
		baseLiteral  = "base-retained-9927"
		overlayValue = "overlay-assigned-3365"
		screenshot   = "iVBORw0KGgoAAAANSUhEUg=="
	)
	base, err := WithScriptSourcedSecretValueRedaction(nil, []string{baseLiteral})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	script := setRunEvidenceRedaction(importedCredentialVariableScript(variableName), true)
	requireImportedCredentialVariableReference(t, script, variableName)
	vu := runShapedVUser(t, script)
	vu.RedactionConfig = base
	vu.setSourceVariable(variableName, overlayValue)

	// Premise: this is the layered branch, not the base==nil shortcut.
	if vu.RedactionConfig == base || vu.RedactionConfig.runtimeParent != base {
		t.Fatalf("premise failed: the overlay did not layer onto the shared base (parent=%p base=%p)",
			vu.RedactionConfig.runtimeParent, base)
	}
	if _, registered := allRuntimeSecretValues(vu.RedactionConfig)[overlayValue]; !registered {
		t.Fatalf("premise failed: overlay value %q was not registered", overlayValue)
	}
	if vu.RedactionConfig.HasRuntimeSecretMaterial() {
		t.Fatal("a layered script-sourced overlay reported platform-managed runtime material")
	}
	result := &protocol.Result{ActionName: "shot", ScreenshotBase64: screenshot}
	scrubSensitiveResult(result, vu.RedactionConfig)
	if result.ScreenshotBase64 != screenshot {
		t.Fatalf("a layered script-sourced overlay suppressed the screenshot: %q", result.ScreenshotBase64)
	}
	if got := scrubSensitiveTextWithConfig(baseLiteral+" "+overlayValue, vu.RedactionConfig); strings.Contains(got, baseLiteral) ||
		strings.Contains(got, overlayValue) {
		t.Fatalf("the layered overlay lost a registered value: %q", got)
	}
}

// Flattening the layer chain must never downgrade a project secret: a value marked
// script-sourced on an overlay layer but platform-managed on the parent is
// platform-managed.
func TestScriptSourcedFlattenYieldsToPlatformManagedParentMembership(t *testing.T) {
	const (
		shared     = "layer-collision-7719"
		scriptOnly = "layer-script-only-4408"
	)
	base, err := WithSecretValueRedactionChecked(nil, []string{shared})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	layered, err := withRuntimeSecretValueOverlay(base, nil, []string{shared, scriptOnly})
	if err != nil {
		t.Fatalf("withRuntimeSecretValueOverlay: %v", err)
	}
	if layered.runtimeParent != base {
		t.Fatal("premise failed: the overlay did not layer onto the base")
	}
	if _, marked := layered.runtimeScriptSourcedValues[shared]; !marked {
		t.Fatalf("premise failed: the overlay layer does not mark %q script-sourced", shared)
	}
	flattened := allRuntimeScriptSourcedValues(layered)
	if _, marked := flattened[shared]; marked {
		t.Fatalf("flattening downgraded a platform-managed parent value to script-sourced: %#v", flattened)
	}
	if _, marked := flattened[scriptOnly]; !marked {
		t.Fatalf("flattening lost a genuinely script-sourced value: %#v", flattened)
	}
	if !layered.HasRuntimeSecretMaterial() {
		t.Fatal("a platform-managed parent value stopped reporting runtime material through the overlay")
	}
}

// requireImportedCredentialVariableReference asserts the fixture premise that an
// import placed {{var.<name>}} in a credential-governed executable field.
//
// It replaces vu.importedCredentialVariableName(name), which was deleted with
// the plan's `variables` index. The value-free expression plan still records the
// reference, which is what makes these fixtures non-vacuous — and what
// setSourceVariable re-reads, after re-establishing origin, to decide whether an
// assignment is script-sourced credential material.
func requireImportedCredentialVariableReference(t *testing.T, script *dsl.Script, name string) {
	t.Helper()
	_, plan, err := inspectImportedCredentials(script)
	if err != nil {
		t.Fatalf("inspectImportedCredentials: %v", err)
	}
	if plan == nil {
		t.Fatalf("fixture no longer records any credential template reference for %q", name)
	}
	if _, exists := plan.expressions["var."+name]; !exists {
		t.Fatalf("fixture no longer references {{var.%s}} from a credential-governed field: %#v",
			name, plan.expressions)
	}
}
