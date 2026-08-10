package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// resolvedCredentialProbeProtocol inspects the runtime matcher at the exact
// moment an adapter would see a resolved action — the last point before
// transport.
//
// scriptSourced values are read out of the imported script (retained literals,
// Basic-auth material, values interpolated into a credential-governed field).
// The operator running the test can already read every one of them in the
// script, so whether they are registered — and therefore scrubbed out of the
// operator's own retained evidence — follows the operator setting
// script.Config.Log.RedactRunEvidence. scriptSourcedRedacted states which side of
// that setting the fixture is running under.
//
// platformManaged values are resolved from the project secret store through a
// {{secret.NAME}} reference. The operator does not hold those, so they must be
// registered and scrubbed on BOTH sides. Asserting both in one probe keeps the
// two halves of the rule from drifting apart.
type resolvedCredentialProbeProtocol struct {
	vu                    *VUser
	scriptSourced         []string
	scriptSourcedRedacted bool
	platformManaged       []string
	inspect               func(*protocol.Action) error
	probeErr              error
}

func (*resolvedCredentialProbeProtocol) Name() string { return "credential-probe" }
func (*resolvedCredentialProbeProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (probe *resolvedCredentialProbeProtocol) Execute(
	_ context.Context,
	action *protocol.Action,
) (*protocol.Result, error) {
	for _, value := range probe.scriptSourced {
		if probe.vu == nil {
			probe.probeErr = errorsJoin(probe.probeErr, fmt.Errorf("probe VUser is not configured"))
			continue
		}
		_, registered := allRuntimeSecretValues(probe.vu.RedactionConfig)[value]
		if registered != probe.scriptSourcedRedacted {
			probe.probeErr = errorsJoin(
				probe.probeErr,
				fmt.Errorf("script-sourced value %q registered = %v, want %v",
					value, registered, probe.scriptSourcedRedacted),
			)
		}
		scrubbed := scrubSensitiveTextWithConfig("evidence:"+value, probe.vu.RedactionConfig)
		if strings.Contains(scrubbed, value) == probe.scriptSourcedRedacted {
			probe.probeErr = errorsJoin(
				probe.probeErr,
				fmt.Errorf("script-sourced value %q in evidence %q contradicts redaction=%v",
					value, scrubbed, probe.scriptSourcedRedacted),
			)
		}
		// Registering a script-sourced value must never suppress binary evidence.
		if probe.vu.RedactionConfig.HasRuntimeSecretMaterial() && len(probe.platformManaged) == 0 {
			probe.probeErr = errorsJoin(
				probe.probeErr,
				fmt.Errorf("script-sourced value %q reported platform-managed runtime material", value),
			)
		}
	}
	for _, value := range probe.platformManaged {
		if probe.vu == nil || probe.vu.RedactionConfig == nil {
			probe.probeErr = errorsJoin(probe.probeErr, fmt.Errorf("runtime redaction is not configured"))
			continue
		}
		if _, exists := allRuntimeSecretValues(probe.vu.RedactionConfig)[value]; !exists {
			probe.probeErr = errorsJoin(
				probe.probeErr,
				fmt.Errorf("platform-managed secret %q was not registered before transport", value),
			)
		}
		if scrubbed := scrubSensitiveTextWithConfig("evidence:"+value, probe.vu.RedactionConfig); strings.Contains(scrubbed, value) {
			probe.probeErr = errorsJoin(
				probe.probeErr,
				fmt.Errorf("platform-managed secret %q remained in retained evidence", value),
			)
		}
	}
	if probe.inspect != nil {
		probe.probeErr = errorsJoin(probe.probeErr, probe.inspect(action))
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*resolvedCredentialProbeProtocol) Teardown(context.Context) error    { return nil }
func (*resolvedCredentialProbeProtocol) HealthCheck(context.Context) error { return nil }

// Values interpolated into a credential-governed field of an imported script
// come from {{env.*}}, {{data.*}} and {{var.*}} — none of which is resolved
// through the secret resolver. They are script-sourced, so whether they are
// scrubbed out of run output follows the operator setting. A {{secret.*}} value
// installed in the base matcher by engine construction keeps its protection
// across the same action boundary in BOTH modes.
//
// The wire values and the durable script are identical in both modes: the
// setting never touches what executes or what is stored.
func TestResolvedImportedDynamicHeaderAndBodyValuesFollowRunEvidenceSetting(t *testing.T) {
	const (
		envKey         = "APEXLOAD_IMPORTED_RUNTIME_HEADER"
		envCredential  = "env-runtime-credential-7841"
		dataValue      = "data-runtime-credential-9253"
		varValue       = "variable{{literal}}credential-4186"
		platformSecret = "platform-store-secret-3092"
	)
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv(EnvScriptEnvAllowlist, envKey)
			t.Setenv(envKey, envCredential)

			action := dsl.Action{
				Name:   "dynamic credentials",
				Type:   "POST",
				Method: "POST",
				Target: "https://api.example.test/login",
				HeaderList: dsl.HeaderList{
					{Name: "Content-Type", Value: "application/json"},
					{Name: "X-Imported-Value", Value: "env={{env." + envKey + "}}"},
					{Name: "X-Second-Imported-Value", Value: "{{var.runtime_header}}"},
				},
				Body: `{"credential":"data={{data.users.credential}}"}`,
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
						map[string]any{"field": "headers", "index": 1},
						map[string]any{"field": "headers", "index": 2},
						map[string]any{"field": "body", "pointer": "/credential"},
					},
				},
			}
			script := setRunEvidenceRedaction(credentialPreservingRuntimeScript(action), mode.redact)
			probe := &resolvedCredentialProbeProtocol{
				scriptSourced: []string{
					"env=" + envCredential,
					varValue,
					"data=" + dataValue,
				},
				scriptSourcedRedacted: mode.redact,
				platformManaged:       []string{platformSecret},
			}
			vu := &VUser{
				Script:    script,
				Proto:     probe,
				Variables: map[string]string{"runtime_header": varValue, "users.credential": dataValue},
				// Stands in for the base matcher engine.New installs from
				// snapshotReferencedSecrets.
				RedactionConfig: WithSecretValueRedaction(nil, []string{platformSecret}),
			}
			probe.vu = vu
			probe.inspect = func(resolved *protocol.Action) error {
				if got, want := resolved.HeaderList[1].Value, "env="+envCredential; got != want {
					return fmt.Errorf("wire header = %q, want %q", got, want)
				}
				if got := resolved.HeaderList[2].Value; got != varValue {
					return fmt.Errorf("second wire header = %q, want %q", got, varValue)
				}
				if got, want := string(resolved.Body), `{"credential":"data=`+dataValue+`"}`; got != want {
					return fmt.Errorf("wire body = %q, want %q", got, want)
				}
				return nil
			}

			resolved := vu.resolveAction(action)
			if _, err := vu.executeWithRetry(context.Background(), resolved, dsl.ErrorHandlingConfig{}); err != nil {
				t.Fatalf("executeWithRetry: %v", err)
			}
			if probe.probeErr != nil {
				t.Fatal(probe.probeErr)
			}
			if got := script.Actions[0].HeaderList[1].Value; got != "env={{env."+envKey+"}}" {
				t.Fatalf("durable header was changed to %q", got)
			}
			if got := script.Actions[0].Body; got != action.Body {
				t.Fatalf("durable body was changed to %q", got)
			}
		})
	}
}

// A bearer token composed from an {{env.*}} component inside an imported script
// is script-sourced: the operator can read both the template and the environment
// it runs under. Whether the component and the composed header are scrubbed out
// of run output follows the operator setting; the wire header never changes.
func TestResolvedImportedComposedBearerComponentFollowsRunEvidenceSetting(t *testing.T) {
	const (
		envKey = "APEXLOAD_IMPORTED_RUNTIME_BEARER"
		// Deliberately short: a longer opaque token is removed by the generic
		// high-entropy scrubber, which is an origin-independent layer and would
		// mask what this test is actually measuring.
		token = "bearer-tok-6418"
	)
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv(EnvScriptEnvAllowlist, envKey)
			t.Setenv(envKey, token)

			action := dsl.Action{
				Name:   "bearer",
				Type:   "GET",
				Method: "GET",
				Target: "https://api.example.test/orders",
				HeaderList: dsl.HeaderList{{
					Name:  "Authorization",
					Value: "Bearer prefix-{{env." + envKey + "}}-suffix",
				}},
			}
			script := setRunEvidenceRedaction(credentialPreservingRuntimeScript(action), mode.redact)
			bearerComponent := "prefix-" + token + "-suffix"
			probe := &resolvedCredentialProbeProtocol{
				scriptSourced: []string{
					bearerComponent,
					"Bearer " + bearerComponent,
				},
				scriptSourcedRedacted: mode.redact,
			}
			vu := &VUser{Script: script, Proto: probe, Variables: map[string]string{}}
			probe.vu = vu
			probe.inspect = func(resolved *protocol.Action) error {
				if got, want := resolved.HeaderList[0].Value, "Bearer "+bearerComponent; got != want {
					return fmt.Errorf("wire Authorization = %q, want %q", got, want)
				}
				return nil
			}

			if _, err := vu.executeWithRetry(
				context.Background(),
				vu.resolveAction(action),
				dsl.ErrorHandlingConfig{},
			); err != nil {
				t.Fatalf("executeWithRetry: %v", err)
			}
			if probe.probeErr != nil {
				t.Fatal(probe.probeErr)
			}
			if got := script.Actions[0].HeaderList[0].Value; got != action.HeaderList[0].Value {
				t.Fatalf("durable Authorization was changed to %q", got)
			}
		})
	}
}

// A user:password pair interpolated into an imported URL is script-sourced. The
// password and the composite follow the setting; the bare username was never a
// secret to begin with and is registered in NEITHER mode.
func TestResolvedImportedURLPasswordAndCompositeFollowRunEvidenceSetting(t *testing.T) {
	const (
		username = "runtime-user-3571"
		password = "runtime-password-8642"
	)
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			action := dsl.Action{
				Name:   "userinfo",
				Type:   "GET",
				Method: "GET",
				Target: "https://{{var.runtime_username}}:{{var.runtime_password}}@api.example.test/orders",
			}
			script := setRunEvidenceRedaction(credentialPreservingRuntimeScript(action), mode.redact)
			probe := &resolvedCredentialProbeProtocol{
				scriptSourced: []string{
					password,
					username + ":" + password,
				},
				scriptSourcedRedacted: mode.redact,
			}
			vu := &VUser{
				Script: script,
				Proto:  probe,
				Variables: map[string]string{
					"runtime_username": username,
					"runtime_password": password,
				},
			}
			probe.vu = vu
			probe.inspect = func(resolved *protocol.Action) error {
				want := "https://" + username + ":" + password + "@api.example.test/orders"
				if resolved.Target != want {
					return fmt.Errorf("wire target = %q, want %q", resolved.Target, want)
				}
				return nil
			}

			if _, err := vu.executeWithRetry(
				context.Background(),
				vu.resolveAction(action),
				dsl.ErrorHandlingConfig{},
			); err != nil {
				t.Fatalf("executeWithRetry: %v", err)
			}
			if probe.probeErr != nil {
				t.Fatal(probe.probeErr)
			}
			if got, want := vu.RedactionConfig.ScrubText("account "+username), "account "+username; got != want {
				t.Fatalf("bare username was treated as a runtime secret: got %q, want %q", got, want)
			}
			if got := script.Actions[0].Target; got != action.Target {
				t.Fatalf("durable target was changed to %q", got)
			}
		})
	}
}

func TestImportedCredentialValuesExcludeBareURLUsernameBeforeProtocolInit(t *testing.T) {
	const (
		username = "static-user-5831"
		password = "static-password-7164"
	)
	script := credentialPreservingRuntimeScript(dsl.Action{
		Name:   "userinfo",
		Type:   "GET",
		Method: "GET",
		Target: "https://static%2Duser%2D5831:static-password-7164@api.example.test/orders",
	})

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if containsExactString(values, username) {
		t.Fatalf("runtime credential values = %#v, included bare URL username %q", values, username)
	}
	for _, expected := range []string{password, username + ":" + password} {
		if !containsExactString(values, expected) {
			t.Fatalf("runtime credential values = %#v, want protected URL credential %q", values, expected)
		}
	}
}

func TestResolvedBasicCredentialExcludesBareUsername(t *testing.T) {
	const (
		username = "admin"
		password = "basic-password-9731"
	)
	collector := importedCredentialCollector{resolved: true, seen: make(map[string]struct{})}
	collector.collectHeaderCredential(
		"Authorization",
		"Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
	)

	if containsExactString(collector.values, username) {
		t.Fatalf("resolved credential values = %#v, included bare Basic username", collector.values)
	}
	for _, expected := range []string{password, username + ":" + password} {
		if !containsExactString(collector.values, expected) {
			t.Fatalf("resolved credential values = %#v, want %q", collector.values, expected)
		}
	}
}

func TestResolvedDigestCredentialExcludesBareUsername(t *testing.T) {
	const username = "admin"
	collector := importedCredentialCollector{resolved: true, seen: make(map[string]struct{})}
	collector.collectHeaderCredential(
		"Authorization",
		`Digest username="admin", realm="example", response="digest-response-4182"`,
	)

	if containsExactString(collector.values, username) {
		t.Fatalf("resolved credential values = %#v, included bare Digest username", collector.values)
	}
	if !containsExactString(collector.values, "digest-response-4182") {
		t.Fatalf("resolved credential values = %#v, omitted Digest response", collector.values)
	}
}

func TestResolvedOpenAPIPathCredentialAllowsOtherParameterLengthsToChange(t *testing.T) {
	const password = "path-password-7319"
	sourceSegment := "prefix-" + password + "-{{data.openapi.tenant}}-suffix"
	passwordStart := len("prefix-")
	passwordEnd := passwordStart + len(password)
	shape := []byte(sourceSegment)
	for index := passwordStart; index < passwordEnd; index++ {
		shape[index] = 0
	}
	shapeHash := sha256.Sum256(shape)
	pointer := fmt.Sprintf(
		"openapi-path-v1:cGFzc3dvcmQ:%d:%x:%d-%d:%d-%d",
		len(sourceSegment),
		shapeHash,
		passwordStart,
		passwordEnd,
		passwordStart,
		passwordEnd,
	)
	targets := []any{map[string]any{
		"request_key": "GET /accounts/{password}/{tenant}",
		"field":       "url.path",
		"index":       2,
		"pointer":     pointer,
	}}
	action := dsl.Action{
		Name:   "path credential",
		Type:   "GET",
		Method: "GET",
		Target: "/accounts/" + sourceSegment,
		Metadata: map[string]any{
			"openapi": map[string]any{
				"path": "/accounts/prefix-{password}-{tenant}-suffix",
			},
			dsl.ImportPreservedCredentialTargetsMetadataKey: targets,
		},
	}
	script := credentialPreservingRuntimeScript(action)
	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if !containsExactString(values, password) {
		t.Fatalf("source credential values = %#v, want authored password", values)
	}

	resolved := &protocol.Action{
		Name:     action.Name,
		Type:     action.Type,
		Method:   action.Method,
		Target:   "/accounts/prefix-" + password + "-a-much-longer-tenant-suffix",
		Metadata: action.Metadata,
	}
	if _, err := resolvedActionCredentialValues(script, resolved); err != nil {
		t.Fatalf("resolvedActionCredentialValues rejected valid interpolation: %v", err)
	}
}

func TestOpenAPIPathCredentialLocatorIsSelfContainedWithoutSourceMetadata(t *testing.T) {
	const password = "self-contained-path-password-7319"
	sourceSegment := "prefix-" + password + "-{{data.openapi.tenant}}-suffix"
	passwordStart := len("prefix-")
	passwordEnd := passwordStart + len(password)
	shape := []byte(sourceSegment)
	for index := passwordStart; index < passwordEnd; index++ {
		shape[index] = 0
	}
	shapeHash := sha256.Sum256(shape)
	pointer := fmt.Sprintf(
		"openapi-path-v1:cGFzc3dvcmQ:%d:%x:%d-%d:%d-%d",
		len(sourceSegment),
		shapeHash,
		passwordStart,
		passwordEnd,
		passwordStart,
		passwordEnd,
	)
	action := dsl.Action{
		Name:   "path credential",
		Type:   "GET",
		Method: "GET",
		Target: "/accounts/" + sourceSegment,
		Metadata: map[string]any{
			dsl.ImportPreservedCredentialTargetsMetadataKey: []any{map[string]any{
				"request_key": "GET /accounts/{password}/{tenant}",
				"field":       "url.path",
				"index":       2,
				"pointer":     pointer,
			}},
		},
	}
	script := credentialPreservingRuntimeScript(action)
	originalTarget := script.Actions[0].Target

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues without optional OpenAPI metadata: %v", err)
	}
	if !containsExactString(values, password) {
		t.Fatalf("source credential values = %#v, want exact authored password", values)
	}
	if script.Actions[0].Target != originalTarget {
		t.Fatalf("credential inspection changed executable target to %q", script.Actions[0].Target)
	}
	config, err := WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		t.Fatalf("construct exact runtime matcher: %v", err)
	}
	if got := config.ScrubText("echo=" + password); strings.Contains(got, password) {
		t.Fatalf("component-only echo was not redacted: %q", got)
	}

	resolved := &protocol.Action{
		Name:     action.Name,
		Type:     action.Type,
		Method:   action.Method,
		Target:   "/accounts/prefix-" + password + "-a-much-longer-tenant-suffix",
		Metadata: action.Metadata,
	}
	if _, err := resolvedActionCredentialValues(script, resolved); err != nil {
		t.Fatalf("resolved path rejected self-contained locator: %v", err)
	}
	if resolved.Target != "/accounts/prefix-"+password+"-a-much-longer-tenant-suffix" {
		t.Fatalf("resolved target bytes changed to %q", resolved.Target)
	}
}

func TestLegacyBodyCredentialFallbackTracksParameterAndResolvedDataValues(t *testing.T) {
	const (
		parameterPassword = "parameter-password-7319"
		resolvedPassword  = "resolved-data-password-6248"
	)
	action := dsl.Action{
		Name:   "legacy login",
		Type:   "POST",
		Method: "POST",
		Target: "/login",
		HeaderList: dsl.HeaderList{{
			Name: "Content-Type", Value: "application/json",
		}},
		Body: `{"password":"{{param.legacy_password}}",` +
			`"token":"{{data.users.session_token}}","ordinary":"visible"}`,
	}
	script := credentialPreservingRuntimeScript(action)
	script.Parameters = []dsl.Parameter{{
		Name: "legacy_password", Type: "list", Values: []string{parameterPassword},
	}}

	values, plan, err := inspectImportedCredentials(script)
	if err != nil {
		t.Fatalf("inspectImportedCredentials: %v", err)
	}
	if !containsExactString(values, parameterPassword) {
		t.Fatalf("authored credential values = %#v, want parameter value", values)
	}
	if plan == nil {
		t.Fatal("authored credential templates were not recorded")
	}
	for _, expression := range []string{
		"param.legacy_password",
		"data.users.session_token",
	} {
		if _, exists := plan.expressions[expression]; !exists {
			t.Fatalf("credential template plan omitted %q: %#v", expression, plan.expressions)
		}
	}
	if script.Actions[0].Body != action.Body {
		t.Fatal("authored executable request body changed during inspection")
	}

	resolvedBody := []byte(
		`{"password":"` + parameterPassword + `","token":"` +
			resolvedPassword + `","ordinary":"visible"}`,
	)
	resolved := &protocol.Action{
		Name:   action.Name,
		Type:   action.Type,
		Method: action.Method,
		Target: action.Target,
		HeaderList: protocol.HeaderList{{
			Name: "Content-Type", Value: "application/json",
		}},
		Body: append([]byte(nil), resolvedBody...),
	}
	resolvedValues, err := resolvedActionCredentialValues(script, resolved)
	if err != nil {
		t.Fatalf("resolvedActionCredentialValues: %v", err)
	}
	for _, want := range []string{parameterPassword, resolvedPassword} {
		if !containsExactString(resolvedValues, want) {
			t.Fatalf("resolved credential values = %#v, want %q", resolvedValues, want)
		}
	}
	if !bytes.Equal(resolved.Body, resolvedBody) {
		t.Fatalf("resolved executable request body changed to %q", resolved.Body)
	}
}

type redactedProjectionEchoProtocol struct {
	canaries []string
	probeErr error
}

func (*redactedProjectionEchoProtocol) Name() string { return "redacted-projection-echo" }
func (*redactedProjectionEchoProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (probe *redactedProjectionEchoProtocol) Execute(
	_ context.Context,
	action *protocol.Action,
) (*protocol.Result, error) {
	rawBody, rawErr := base64.StdEncoding.DecodeString(string(action.Body))
	redactedBody, redactedErr := base64.StdEncoding.DecodeString(string(action.RedactedBody))
	if rawErr != nil || redactedErr != nil {
		probe.probeErr = fmt.Errorf("decode action bodies: raw=%v redacted=%v", rawErr, redactedErr)
	}
	rawProjection := action.Target + "\n" + string(rawBody)
	redactedProjection := action.RedactedTarget + "\n" + string(redactedBody)
	snapshotHeaders := make(map[string]string, len(action.RedactedHeaderList))
	for _, header := range action.HeaderList {
		rawProjection += "\n" + header.Name + ": " + header.Value
	}
	for _, header := range action.RedactedHeaderList {
		redactedProjection += "\n" + header.Name + ": " + header.Value
		snapshotHeaders[header.Name] = header.Value
	}
	for _, canary := range probe.canaries {
		if !strings.Contains(rawProjection, canary) {
			probe.probeErr = errorsJoin(
				probe.probeErr,
				fmt.Errorf("wire projection did not retain canary %q", canary),
			)
		}
		// The adapter-facing "redacted" projection must now agree with the wire
		// request for script-sourced values. Blanking them here used to make the
		// projection lie about the request the operator's own script sent.
		if !strings.Contains(redactedProjection, canary) {
			probe.probeErr = errorsJoin(
				probe.probeErr,
				fmt.Errorf("redacted projection dropped script-sourced canary %q", canary),
			)
		}
	}
	return &protocol.Result{
		ActionName:   action.Name,
		Success:      false,
		ErrorMessage: redactedProjection,
		Snapshot: &protocol.ErrorSnapshot{
			RequestURL:     action.RedactedTarget,
			RequestHeaders: snapshotHeaders,
			RequestBody:    string(redactedBody),
		},
	}, nil
}
func (*redactedProjectionEchoProtocol) Teardown(context.Context) error    { return nil }
func (*redactedProjectionEchoProtocol) HealthCheck(context.Context) error { return nil }

// The adapter-facing redacted projection carries script-sourced credentials
// through unchanged, exactly like the wire request. The durable script keeps its
// templates: nothing here rewrites what is stored.
func TestResolvedImportedCredentialProjectionRetainsScriptSourcedValues(t *testing.T) {
	const (
		username       = "projection-user-7316"
		password       = "projection-password-2849"
		bearerToken    = "projection-bearer-9537"
		bodyCredential = "projection-body-4268"
	)
	body := base64.StdEncoding.EncodeToString([]byte(
		`{"payload":"body={{var.body_credential}}"}`,
	))
	action := dsl.Action{
		Name:         "projection",
		Type:         "POST",
		Method:       "POST",
		Target:       "https://{{var.username}}:{{var.password}}@api.example.test/orders",
		Body:         body,
		BodyEncoding: "base64",
		Headers: map[string]string{
			"Authorization": "Bearer prefix-{{var.bearer}}-suffix",
			"Content-Type":  "application/json",
		},
		HeaderList: dsl.HeaderList{
			{Name: "Authorization", Value: "Bearer prefix-{{var.bearer}}-suffix"},
			{Name: "Content-Type", Value: "application/json"},
		},
		Metadata: map[string]any{
			dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
				map[string]any{"field": "body", "pointer": "/payload"},
			},
		},
	}
	script := credentialPreservingRuntimeScript(action)
	canaries := []string{username, password, bearerToken, bodyCredential}
	probe := &redactedProjectionEchoProtocol{canaries: canaries}
	vu := &VUser{
		Script: script,
		Proto:  probe,
		Variables: map[string]string{
			"username":        username,
			"password":        password,
			"bearer":          bearerToken,
			"body_credential": bodyCredential,
		},
	}

	result, err := vu.executeWithRetry(
		context.Background(),
		vu.resolveAction(action),
		dsl.ErrorHandlingConfig{},
	)
	if err != nil {
		t.Fatalf("executeWithRetry: %v", err)
	}
	if probe.probeErr != nil {
		t.Fatal(probe.probeErr)
	}
	if result == nil || result.Snapshot == nil {
		t.Fatal("adapter did not return echoed error evidence")
	}
	evidence := result.ErrorMessage + "\n" + result.Snapshot.RequestURL +
		"\n" + result.Snapshot.RequestBody
	for name, value := range result.Snapshot.RequestHeaders {
		evidence += "\n" + name + ": " + value
	}
	for _, canary := range canaries {
		if !strings.Contains(evidence, canary) {
			t.Fatalf("adapter evidence dropped script-sourced canary %q: %q", canary, evidence)
		}
	}
	for _, template := range []string{
		"{{var.username}}",
		"{{var.password}}",
		"{{var.bearer}}",
		"{{var.body_credential}}",
	} {
		durable, _ := base64.StdEncoding.DecodeString(script.Actions[0].Body)
		if !strings.Contains(script.Actions[0].Target+script.Actions[0].HeaderList[0].Value+string(durable), template) {
			t.Fatalf("durable action no longer contains %q", template)
		}
	}
}

// A response value extracted into a neutrally named variable used to be
// registered as a runtime secret purely because an IMPORT referenced that
// variable from a credential-governed header. Import provenance is not a reason
// to hide a value from the operator who owns the import, so the extraction now
// survives the replay report. The origin-independent layer is unchanged: the
// same response's sensitively NAMED extraction is still redacted everywhere.
func TestImportedCredentialNeutralExtractionSurvivesWhileNamedSecretIsRedacted(t *testing.T) {
	const (
		canary     = "neutral-runtime-credential-early-4197"
		namedToken = "named-runtime-credential-early-8825"
	)
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Name != "issue credential" {
			return nil, fmt.Errorf("skipped credential consumer unexpectedly executed: %s", action.Name)
		}
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
			Extractions: map[string]string{
				"opaque_handle": canary,
				"session_token": namedToken,
			},
			ProtocolEvidence: map[string]any{
				"ordinary_echo": canary,
			},
		}, nil
	})

	skippedConsumer := dsl.Action{
		Name:   "skipped consumer",
		Type:   "POST",
		Method: "POST",
		Target: "https://api.example.test/login",
		HeaderList: dsl.HeaderList{{
			Name:  "X-Imported-Credential",
			Value: "prefix={{var.opaque_handle}}",
		}},
		Metadata: map[string]any{
			dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
				map[string]any{"field": "headers", "index": 0},
			},
		},
	}
	script := credentialPreservingRuntimeScript(
		dsl.Action{Name: "issue credential", Type: "GET", Method: "GET", Target: "/issue"},
	)
	script.Protocol = protocolName
	script.Actions = append(script.Actions, dsl.Action{
		Name: "skip credential branch",
		Type: "control_if",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{
				Left:  "{{var.opaque_handle}}",
				Op:    "eq",
				Right: "different",
			},
			Steps: []dsl.Action{skippedConsumer},
		},
	})

	var progress []ReplayProgressEvent
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		IgnoreThinkTime: true,
		OnProgress: func(event ReplayProgressEvent) {
			progress = append(progress, event)
		},
	})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("replay events = %d, want only the issuing request", len(report.Events))
	}
	// Previously asserted these two were redactedPayloadValue. That was the
	// rejected decision: the only thing making this neutral value a "credential"
	// was the import the operator supplied.
	if got := report.Events[0].Extractions["opaque_handle"]; got != canary {
		t.Fatalf("early extraction = %q, want the value the system under test returned", got)
	}
	if got := report.FinalVariables["opaque_handle"]; got != canary {
		t.Fatalf("final neutral extraction = %q, want the value the system under test returned", got)
	}
	if got := report.Events[0].Extractions["session_token"]; got != redactedPayloadValue {
		t.Fatalf("sensitively named extraction = %q, want redacted", got)
	}
	if got := report.FinalVariables["session_token"]; got != redactedPayloadValue {
		t.Fatalf("final sensitively named extraction = %q, want redacted", got)
	}
	encodedReport, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	encodedProgress, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedReport), canary) {
		t.Fatalf("replay report dropped the neutral extraction: %s", encodedReport)
	}
	if strings.Contains(string(encodedReport), namedToken) {
		t.Fatalf("replay report retained a sensitively named extraction: %s", encodedReport)
	}
	if strings.Contains(string(encodedProgress), namedToken) {
		t.Fatalf("replay progress retained a sensitively named extraction: %s", encodedProgress)
	}
	if !strings.Contains(string(encodedProgress), canary) {
		t.Fatalf("replay progress dropped the neutral extraction: %s", encodedProgress)
	}
	if got := script.Actions[1].Control.Steps[0].HeaderList[0].Value; got != skippedConsumer.HeaderList[0].Value {
		t.Fatalf("durable skipped consumer was changed to %q", got)
	}
}

// A value generated by the script itself ({{fn.random_string}}) inside a
// credential-governed field of an imported script used to be registered as a
// runtime secret purely because the field came from an import. It is not a
// secret-store value, so it is script-sourced and stays visible in the replay
// report the operator reads.
func TestResolvedImportedCookieGeneratedTemplateComponentIsNotRedacted(t *testing.T) {
	const expression = "{{fn.random_string(20)}}"
	var (
		wireCookie string
		component  string
	)
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if len(action.HeaderList) != 1 {
			return nil, fmt.Errorf("wire headers = %#v, want one Cookie", action.HeaderList)
		}
		wireCookie = action.HeaderList[0].Value
		const prefix = "sid=prefix-"
		const suffix = "-suffix"
		if !strings.HasPrefix(wireCookie, prefix) || !strings.HasSuffix(wireCookie, suffix) {
			return nil, fmt.Errorf("wire Cookie = %q, want composed credential", wireCookie)
		}
		component = strings.TrimSuffix(strings.TrimPrefix(wireCookie, prefix), suffix)
		if len(component) != 20 {
			return nil, fmt.Errorf("generated credential component length = %d, want 20", len(component))
		}
		return &protocol.Result{
			ActionName:   action.Name,
			Success:      false,
			ErrorMessage: "ordinary component echo: " + component,
			ResponseBody: `{"ordinary_echo":"` + component + `"}`,
			Extractions: map[string]string{
				"ordinary_value": component,
			},
			ProtocolEvidence: map[string]any{
				"ordinary_value": component,
			},
			Snapshot: &protocol.ErrorSnapshot{
				ResponseBody: "ordinary snapshot echo: " + component,
			},
		}, nil
	})

	action := dsl.Action{
		Name:   "generated cookie",
		Type:   "GET",
		Method: "GET",
		Target: "https://api.example.test/orders",
		HeaderList: dsl.HeaderList{{
			Name:  "Cookie",
			Value: "sid=prefix-" + expression + "-suffix",
		}},
	}
	script := credentialPreservingRuntimeScript(action)
	script.Protocol = protocolName
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		IgnoreThinkTime: true,
	})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if component == "" || !strings.Contains(wireCookie, component) {
		t.Fatalf("transport did not receive the generated raw component: cookie=%q component=%q", wireCookie, component)
	}
	if got := script.Actions[0].HeaderList[0].Value; got != action.HeaderList[0].Value {
		t.Fatalf("durable Cookie was changed to %q", got)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), component) {
		t.Fatalf("script-generated component was scrubbed from replay disclosure surfaces: %s", encoded)
	}
	if got := report.FinalVariables["ordinary_value"]; got != component {
		t.Fatalf("component extraction final value = %q, want %q", got, component)
	}
}

func credentialPreservingRuntimeScript(action dsl.Action) *dsl.Script {
	return &dsl.Script{
		Name:     "runtime credential fixture",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{action},
	}
}

func errorsJoin(existing, next error) error {
	if next == nil {
		return existing
	}
	if existing == nil {
		return next
	}
	return fmt.Errorf("%v; %w", existing, next)
}
