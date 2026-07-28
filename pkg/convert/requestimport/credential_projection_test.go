package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

func TestFinalizeActionProjectsBase64BodyAgainstWireBytes(t *testing.T) {
	const canary = "base64-source-password-91ae"
	wireBody := "{\n  \"password\": \"" + canary + "\",\n  \"note\": \"unchanged\"\n}"
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "encoded-login",
		Name:   "Encoded login",
		Method: "POST",
		Target: "/login",
		Body: Body{
			Kind:      BodyJSON,
			MediaType: "application/json",
			Payload:   []byte(wireBody),
			Encoding:  BodyEncodingBase64,
		},
		Source: SourceRef{Format: "contract", Path: "encoded.json"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.SecretReference},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if action.BodyEncoding != "base64" {
		t.Fatalf("body encoding = %q, want base64", action.BodyEncoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(action.Body)
	if err != nil {
		t.Fatalf("decode finalized body: %v", err)
	}
	if len(report.Requirements) != 1 || report.Requirements[0].Value != canary {
		t.Fatalf("requirements = %#v, want protected base64 body credential", report.Requirements)
	}
	replacement := report.Requirements[0].Replacement
	wantWire := strings.Replace(wireBody, canary, replacement, 1)
	if string(decoded) != wantWire {
		t.Fatalf("decoded projected body = %q, want byte-preserved %q", decoded, wantWire)
	}
	if strings.Contains(action.Body, canary) || strings.Contains(string(decoded), canary) {
		t.Fatal("finalized base64 body disclosed source credential")
	}

	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if len(result.Requirements) != 1 || result.Requirements[0].Replacement != replacement {
		t.Fatalf("final requirements = %#v", result.Requirements)
	}
}

func TestFinalizeActionPreserveRetainsInvalidUTF8JSONAsBlockedBase64(t *testing.T) {
	t.Parallel()

	wireBody := []byte("{\"password\":\"invalid-utf8-canary-73d1")
	wireBody = append(wireBody, 0xff, '"', '}')
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "invalid-utf8-json",
		Name:   "Invalid UTF-8 JSON",
		Method: "POST",
		Target: "/login",
		Body: Body{
			Kind:      BodyJSON,
			MediaType: "application/json",
			Payload:   wireBody,
			Encoding:  BodyEncodingText,
		},
		Source: SourceRef{Format: "fixture", Pointer: "/request/body"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.Preserve},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if action.BodyEncoding != "base64" {
		t.Fatalf("body encoding = %q, want exact-byte base64 storage", action.BodyEncoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(action.Body)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !reflect.DeepEqual(decoded, wireBody) {
		t.Fatalf("decoded body = %q, want exact imported bytes %q", decoded, wireBody)
	}
	if report.Disposition != BlockedDraft || len(report.Requirements) != 0 ||
		action.Metadata["execution_blocker"] == nil {
		t.Fatalf("action/report = %#v / %#v, want blocked retained draft", action, report)
	}
	targets := preservedCredentialTargetsFromMetadata(action.Metadata)
	if len(targets) == 0 ||
		!strings.HasSuffix(targets[len(targets)-1].Field, ".opaque") {
		t.Fatalf("credential locators = %#v, want opaque body locator", targets)
	}

	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if len(result.ValidationIssues) != 0 || result.Disposition != BlockedDraft ||
		result.Readiness.ReplayReady {
		t.Fatalf("final result = %#v, want valid blocked draft", result)
	}
}

func TestFinalizeActionPreserveFormLimitCarriesOpaqueRuntimeLocator(t *testing.T) {
	t.Parallel()

	const sourceBody = "ordinary=exact-uninspected-form-value"
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "bounded-form",
		Name:   "Bounded form",
		Method: "POST",
		Target: "/submit",
		Body: Body{
			Kind:      BodyFormURLEncoded,
			MediaType: "application/x-www-form-urlencoded",
			Payload:   []byte(sourceBody),
		},
		Source: SourceRef{Format: "fixture", Pointer: "/request/body"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{
			Mode:   credentialpolicy.Preserve,
			Limits: credentialpolicy.Limits{MaxBytes: 8},
		},
		ActionPath: "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if action.Body != sourceBody || report.Disposition != BlockedDraft ||
		len(report.Requirements) != 0 {
		t.Fatalf("form action/report = %#v / %#v", action, report)
	}
	targets := preservedCredentialTargetsFromMetadata(action.Metadata)
	if len(targets) != 1 || targets[0].Field != "body.form.opaque" {
		t.Fatalf("form fallback locators = %#v", targets)
	}
	if action.Metadata["execution_blocker"] == nil {
		t.Fatalf("form fallback is not execution-gated: %#v", action.Metadata)
	}
}

func TestFinalizeActionPreserveRetainsInvalidMultipartFieldsInInertEnvelope(t *testing.T) {
	t.Parallel()

	const (
		invalidName        = "X-Bad\rName"
		invalidValue       = "bad\r\nInjected: one"
		invalidDisposition = "form-data; name=\"unterminated"
		invalidContentType = "text/plain\r\nInjected: two"
		bodyCanary         = "multipart-preserve-body-canary-73a2"
	)
	requestSource := SourceRef{Format: "fixture", Path: "multipart.json", Pointer: "/request"}
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "invalid-multipart-fields",
		Name:   "Invalid multipart fields",
		Method: "POST",
		Target: "/upload",
		Body: Body{
			Kind:      BodyMultipart,
			MediaType: "multipart/form-data",
			Multipart: &MultipartBody{
				Boundary: "fixture-boundary",
				Parts: []MultipartPart{{
					Name:        "ordinary",
					ContentType: invalidContentType,
					Headers: HeaderList{
						{Name: invalidName, Value: "one", Source: SourceRef{Pointer: "/parts/0/headers/0"}},
						{Name: "X-Bad", Value: invalidValue, Source: SourceRef{Pointer: "/parts/0/headers/1"}},
						{Name: "Content-Disposition", Value: invalidDisposition, Source: SourceRef{Pointer: "/parts/0/headers/2"}},
					},
					Data:   []byte(bodyCanary),
					Source: SourceRef{Pointer: "/parts/0"},
				}},
			},
			Source: requestSource,
		},
		Source: requestSource,
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.Preserve},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if report.Disposition != BlockedDraft || len(report.Requirements) != 0 ||
		action.Metadata["execution_blocker"] == nil {
		t.Fatalf("multipart action/report = %#v / %#v", action, report)
	}
	for _, invalid := range []string{invalidName, invalidValue, invalidDisposition, invalidContentType} {
		if strings.Contains(action.Body, invalid) {
			t.Fatalf("invalid multipart field remained executable: %q", invalid)
		}
	}

	marker, ok := action.Metadata[ImportMultipartFidelityMetadataKey].(map[string]any)
	if !ok {
		t.Fatalf("missing multipart fidelity envelope: %#v", action.Metadata)
	}
	fields := metadataObjectValues(marker[multipartFidelityFieldName])
	if len(fields) != 4 {
		t.Fatalf("multipart fidelity fields = %#v, want four ordered omissions", fields)
	}
	expected := []struct {
		field string
		name  string
		value string
	}{
		{field: "header", name: invalidName, value: "one"},
		{field: "header", name: "X-Bad", value: invalidValue},
		{field: "content_disposition", name: "Content-Disposition", value: invalidDisposition},
		{field: "content_type", value: invalidContentType},
	}
	for index, want := range expected {
		if got, _ := fields[index]["field"].(string); got != want.field {
			t.Fatalf("multipart fidelity field %d = %q, want %q", index, got, want.field)
		}
		decode := func(key string) string {
			raw, _ := fields[index][key].(string)
			decoded, decodeErr := base64.StdEncoding.DecodeString(raw)
			if decodeErr != nil {
				t.Fatalf("decode multipart field %d %s: %v", index, key, decodeErr)
			}
			return string(decoded)
		}
		if want.name != "" && decode(dsl.ImportHeaderFidelityPreservedNameBase64Key) != want.name {
			t.Fatalf("multipart fidelity field %d lost exact name", index)
		}
		if decode(dsl.ImportHeaderFidelityPreservedValueBase64Key) != want.value {
			t.Fatalf("multipart fidelity field %d lost exact value", index)
		}
	}

	script := validBaseScript()
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "fixture"}}
	script.Actions = []dsl.Action{action}
	finalized, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if len(finalized.ValidationIssues) != 0 || finalized.Readiness.ReplayReady {
		t.Fatalf("multipart draft validation/readiness = %#v", finalized)
	}
	disclosed, err := scriptdisclosure.Project(finalized.Script)
	if err != nil {
		t.Fatalf("project disclosure: %v", err)
	}
	disclosedJSON, err := json.Marshal(disclosed)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{
		base64.StdEncoding.EncodeToString([]byte(invalidName)),
		base64.StdEncoding.EncodeToString([]byte(invalidValue)),
		base64.StdEncoding.EncodeToString([]byte(invalidDisposition)),
		base64.StdEncoding.EncodeToString([]byte(invalidContentType)),
		bodyCanary,
	} {
		if strings.Contains(string(disclosedJSON), exact) {
			t.Fatalf("disclosure projection retained exact multipart source material: %s", disclosedJSON)
		}
	}
}

func TestFinalizeActionPreserveRetainsMalformedAuthWithoutReentry(t *testing.T) {
	t.Parallel()

	const token = "retained-auth-canary-61c2\r\nInjected: yes"
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "invalid-auth",
		Name:   "Invalid auth",
		Method: "GET",
		Target: "/orders",
		Auth: Auth{
			Mode:   AuthBearer,
			Token:  token,
			Source: SourceRef{Format: "fixture", Pointer: "/request/auth"},
		},
		Source: SourceRef{Format: "fixture", Pointer: "/request"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.Preserve},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if action.Auth == nil || action.Auth.Type != httpprotocol.AuthTypeBearer ||
		action.Auth.Token != token {
		t.Fatalf("auth = %#v, want exact retained token", action.Auth)
	}
	if report.Disposition != BlockedDraft || len(report.Requirements) != 0 ||
		action.Metadata["execution_blocker"] == nil {
		t.Fatalf("action/report = %#v / %#v, want blocked retained auth", action, report)
	}
	targets := preservedCredentialTargetsFromMetadata(action.Metadata)
	found := false
	for _, target := range targets {
		found = found || strings.HasSuffix(target.Field, "auth.token")
	}
	if !found {
		t.Fatalf("credential locators = %#v, want auth token locator", targets)
	}
	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if len(result.ValidationIssues) != 0 || result.Disposition != BlockedDraft ||
		result.Script.Actions[0].Auth == nil ||
		result.Script.Actions[0].Auth.Token != token {
		t.Fatalf("durable auth result = %#v, want exact blocked credential", result)
	}
}

func TestFinalizeActionPreserveCarriesDynamicBodyCredentialRuntimeLocator(t *testing.T) {
	t.Parallel()

	const body = `{"password":"{{env.IMPORTED_PASSWORD}}"}`
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "dynamic-password",
		Name:   "Dynamic password",
		Method: "POST",
		Target: "/login",
		Body: Body{
			Kind:      BodyJSON,
			MediaType: "application/json",
			Payload:   []byte(body),
		},
		Source: SourceRef{Format: "fixture", Pointer: "/request"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.Preserve},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if action.Body != body || len(report.Requirements) != 0 {
		t.Fatalf("dynamic body/action report = %#v / %#v", action, report)
	}
	targets := preservedCredentialTargetsFromMetadata(action.Metadata)
	if len(targets) != 1 ||
		targets[0].Field != "body" ||
		targets[0].Pointer != "/password" {
		t.Fatalf("runtime credential locators = %#v", targets)
	}
}

func TestReviewCredentialProjectionDoesNotSerializeProtectedValue(t *testing.T) {
	requirement, _, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{
		Key:      "password",
		Label:    "Password",
		Context:  "body",
		NameHint: "fixture_password",
		Source:   SourceRef{Format: "fixture", Path: "request.json", Pointer: "/password"},
		Targets:  []credentialpolicy.TargetRef{{RequestKey: "fixture", Field: "json", Pointer: "/password"}},
		Value:    "credential-canary-review-75e8",
		Mode:     credentialpolicy.SecretReference,
	})
	if err != nil {
		t.Fatal(err)
	}
	review := ReviewCredentialProjection(credentialpolicy.Result{
		Requirements: []credentialpolicy.Requirement{requirement},
		Findings: []credentialpolicy.Finding{{
			Code: "credential_json_duplicate_key", Source: requirement.Source,
			Disposition: credentialpolicy.BlockedDraft,
		}},
		Disposition: credentialpolicy.BlockedDraft,
	}, "JSON body")
	encoded, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), requirement.Value) {
		t.Fatalf("protected value leaked through review bridge: %s", encoded)
	}
	if review.Disposition != BlockedDraft || review.Rejected ||
		len(review.Findings) != 1 || review.Findings[0].Element != "JSON body" {
		t.Fatalf("review = %#v", review)
	}
}

func TestFinalizeActionProjectsCredentialsAcrossRequestSurfaces(t *testing.T) {
	canaryHeader := "canary-header-31a4"
	canaryQuery := "canary-query-42b5"
	canaryPassword := "canary-password-53c6"
	canaryBody := "canary-body-64d7"
	request := EffectiveRequest{
		Key:    "orders",
		Name:   "Orders",
		Method: "POST",
		Target: "https://example.test/orders?api_key=" + canaryQuery + "&tag=exact",
		Headers: HeaderList{
			{Name: "X-Trace", Value: "exact"},
			{Name: "Authorization", Value: "Bearer " + canaryHeader},
		},
		Auth: Auth{Mode: AuthBasic, Username: "user", Password: canaryPassword},
		Body: Body{
			Kind:      BodyJSON,
			MediaType: "application/json",
			Payload:   []byte(`{"password":"` + canaryBody + `","count":7,"note":"exact"}`),
		},
		Source: SourceRef{Format: "contract", Path: "collection.json", Pointer: "/requests/0"},
	}

	action, report, err := FinalizeAction(request, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.SecretReference},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	encoded, err := json.Marshal(struct {
		Action any `json:"action"`
		Report any `json:"report"`
	}{Action: action, Report: report})
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{canaryHeader, canaryQuery, canaryPassword, canaryBody} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("credential canary leaked: %q in %s", canary, encoded)
		}
	}
	if len(report.Requirements) != 4 {
		t.Fatalf("requirements = %d, want 4: %#v", len(report.Requirements), report.Requirements)
	}
	if action.Headers["X-Trace"] != "exact" || !strings.Contains(action.Body, `"count":7`) ||
		!strings.Contains(action.Body, `"note":"exact"`) {
		t.Fatalf("nonsensitive request data changed: %#v", action)
	}
	if action.Auth == nil || !strings.HasPrefix(action.Auth.Password, "{{secret.") {
		t.Fatalf("auth password = %#v", action.Auth)
	}
	protectedValues := make(map[string]struct{}, len(report.Requirements))
	replacements := make(map[string]string, len(report.Requirements))
	for _, requirement := range report.Requirements {
		if requirement.Value == "" {
			t.Fatalf("protected value was discarded from requirement %#v", requirement)
		}
		if priorID, duplicate := replacements[requirement.Replacement]; duplicate && priorID != requirement.ID {
			t.Fatalf("different requirements shared non-bindable replacement %q: %s and %s",
				requirement.Replacement, priorID, requirement.ID)
		}
		replacements[requirement.Replacement] = requirement.ID
		protectedValues[requirement.Value] = struct{}{}
	}
	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != len(report.Requirements) || len(result.SourceMappings) != 1 {
		t.Fatalf("finalize sidecars = requirements %#v mappings %#v", result.Requirements, result.SourceMappings)
	}
	for _, requirement := range result.Requirements {
		if _, exists := protectedValues[requirement.Value]; !exists {
			t.Fatalf("FinalizeScript discarded protected requirement value: %#v", requirement)
		}
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for protected := range protectedValues {
		if strings.Contains(string(serialized), protected) {
			t.Fatalf("protected requirement value leaked through FinalizeResult JSON: %s", serialized)
		}
	}
}

func TestFinalizeActionBlocksInvalidSourceURLAuthAndMultipartHeadersBeforeProjection(t *testing.T) {
	t.Parallel()
	const (
		urlCanary       = "unsafe-url-canary-a11"
		authCanary      = "unsafe-auth-canary-b22"
		multipartCanary = "unsafe-multipart-canary-c33"
	)
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "unsafe-wire-credentials",
		Name:   "Unsafe wire credentials",
		Method: "POST",
		Target: "https://example.test/orders?api_key=" + urlCanary + "\r\nInjected: yes",
		Auth: Auth{
			Mode:  AuthBearer,
			Token: "{{var.auth_token}}",
		},
		Body: Body{
			Kind: BodyMultipart,
			Multipart: &MultipartBody{Parts: []MultipartPart{{
				Name: "payload",
				Headers: HeaderList{{
					Name:  "Authorization",
					Value: "Bearer {{var.part_token}}",
				}},
				Data: []byte("safe body"),
			}}},
		},
		Source: SourceRef{Format: "contract", Path: "unsafe.json", Pointer: "/request"},
	}, FinalizeOptions{
		ActionPath: "actions[0]",
		Credentials: &CredentialProjectionOptions{
			Mode: credentialpolicy.SecretReference,
			SourceVariables: map[string]credentialpolicy.SourceVariable{
				"auth_token": {Value: authCanary + "\r\nInjected: yes"},
				"part_token": {Value: multipartCanary + "\r\nInjected: yes"},
			},
		},
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if report.Disposition != BlockedDraft || len(report.Requirements) != 0 {
		t.Fatalf("report = %#v, want blocked draft without stale bindings", report)
	}
	if action.Target != "/" || action.Auth == nil || action.Auth.Type != "none" {
		t.Fatalf("inert URL/auth projection = target %q auth %#v", action.Target, action.Auth)
	}
	if strings.Contains(action.Body, "Authorization") || !strings.Contains(action.Body, "safe body") {
		t.Fatalf("multipart header was not safely omitted: %q", action.Body)
	}
	reasons := make(map[string]bool)
	for _, finding := range report.Findings {
		reasons[finding.ReasonCode] = true
	}
	for _, reason := range []string{
		"invalid_url_wire_value",
		"invalid_auth_wire_value",
		"invalid_multipart_header_value",
	} {
		if !reasons[reason] {
			t.Fatalf("finding reasons = %#v, missing %q", reasons, reason)
		}
	}
	encoded, err := json.Marshal(struct {
		Action dsl.Action `json:"action"`
		Report Report     `json:"report"`
	}{Action: action, Report: report})
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{urlCanary, authCanary, multipartCanary} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("disclosure-safe draft retained %q: %s", canary, encoded)
		}
	}

	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if result.Disposition != BlockedDraft || len(result.ValidationIssues) != 0 ||
		len(result.Requirements) != 0 {
		t.Fatalf("final result = %#v", result)
	}
}

func TestProjectCredentialsAcceptsBoundedURLTemplates(t *testing.T) {
	t.Parallel()
	const target = "https://{{ param.host }}:{{param.port}}/orders/{{var.id}}?tag={{env.TAG}}"
	projected, report, err := ProjectCredentials(EffectiveRequest{
		Key: "templated-url", Method: "GET", Target: target,
		Source: SourceRef{Format: "contract", Path: "request.json"},
	}, CredentialProjectionOptions{Mode: credentialpolicy.SecretReference})
	if err != nil {
		t.Fatalf("ProjectCredentials: %v", err)
	}
	if projected.Target != target || report.Disposition != ExecutableExact ||
		len(report.Findings) != 0 || len(report.Requirements) != 0 {
		t.Fatalf("templated URL projection = %q / %#v", projected.Target, report)
	}
}

func TestFinalizeActionBlocksOpaqueAndUnsupportedAbsoluteURLs(t *testing.T) {
	t.Parallel()
	const canary = "opaque-userinfo-canary-e55"
	tests := []struct {
		name   string
		target string
	}{
		{name: "opaque HTTP userinfo", target: "https:alice:" + canary + "@example.test"},
		{name: "unsupported literal scheme", target: "ftp://example.test/archive"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			action, report, err := FinalizeAction(EffectiveRequest{
				Key: "invalid-absolute-url", Method: "GET", Target: test.target,
				Source: SourceRef{Format: "contract", Path: "request.json"},
			}, FinalizeOptions{
				ActionPath: "actions[0]",
				Credentials: &CredentialProjectionOptions{
					Mode: credentialpolicy.SecretReference,
				},
			})
			if err != nil {
				t.Fatalf("FinalizeAction: %v", err)
			}
			if action.Target != "/" || report.Disposition != BlockedDraft ||
				len(report.Requirements) != 0 {
				t.Fatalf("blocked URL projection = target %q report %#v", action.Target, report)
			}
			if len(report.Findings) != 1 ||
				report.Findings[0].ReasonCode != "invalid_url_wire_value" {
				t.Fatalf("findings = %#v", report.Findings)
			}
			encoded, err := json.Marshal(struct {
				Action dsl.Action `json:"action"`
				Report Report     `json:"report"`
			}{Action: action, Report: report})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), canary) {
				t.Fatalf("blocked URL disclosed source credential: %s", encoded)
			}
		})
	}

	const dynamicScheme = "{{param.scheme}}://example.test/orders"
	projected, report, err := ProjectCredentials(EffectiveRequest{
		Key: "dynamic-scheme", Method: "GET", Target: dynamicScheme,
		Source: SourceRef{Format: "contract", Path: "request.json"},
	}, CredentialProjectionOptions{Mode: credentialpolicy.SecretReference})
	if err != nil {
		t.Fatalf("ProjectCredentials dynamic scheme: %v", err)
	}
	if projected.Target != dynamicScheme || report.Disposition != ExecutableExact ||
		len(report.Findings) != 0 {
		t.Fatalf("dynamic scheme projection = %q / %#v", projected.Target, report)
	}
}

func TestFinalizeActionUsesAuthoritativeMultipartDispositionNameForCredentials(t *testing.T) {
	t.Parallel()
	const canary = "multipart-disposition-canary-d44"
	action, report, err := FinalizeAction(EffectiveRequest{
		Key: "multipart-disposition", Method: "POST", Target: "/upload",
		Body: Body{
			Kind: BodyMultipart,
			Multipart: &MultipartBody{Parts: []MultipartPart{{
				Name: "note",
				Headers: HeaderList{{
					Name:  "Content-Disposition",
					Value: `form-data; name="password"`,
				}},
				Data: []byte(canary),
			}}},
		},
		Source: SourceRef{Format: "contract", Path: "multipart.json"},
	}, FinalizeOptions{
		ActionPath: "actions[0]",
		Credentials: &CredentialProjectionOptions{
			Mode: credentialpolicy.SecretReference,
		},
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if report.Disposition != BlockedDraft || len(report.Requirements) != 1 ||
		report.Requirements[0].Value != canary {
		t.Fatalf("multipart disposition report = %#v", report)
	}
	if strings.Contains(action.Body, canary) ||
		!strings.Contains(action.Body, report.Requirements[0].Replacement) {
		t.Fatalf("multipart body was not governed safely: %q", action.Body)
	}
	foundConflict := false
	for _, finding := range report.Findings {
		foundConflict = foundConflict || finding.ReasonCode == "multipart_name_conflict"
	}
	if !foundConflict {
		t.Fatalf("multipart conflict finding missing: %#v", report.Findings)
	}
}

func TestFinalizeActionGovernsMultipartBodyAfterMalformedDisposition(t *testing.T) {
	t.Parallel()
	const canary = "multipart-malformed-disposition-canary-f66"
	action, report, err := FinalizeAction(EffectiveRequest{
		Key: "multipart-malformed-disposition", Method: "POST", Target: "/upload",
		Body: Body{
			Kind: BodyMultipart,
			Multipart: &MultipartBody{Parts: []MultipartPart{{
				Name: "note",
				Headers: HeaderList{
					{Name: "Content-Disposition", Value: `form-data; name="note"`},
					{Name: "Content-Disposition", Value: "form-data;\r\nInjected: yes"},
				},
				Data: []byte(canary),
			}}},
		},
		Source: SourceRef{Format: "contract", Path: "multipart.json"},
	}, FinalizeOptions{
		ActionPath: "actions[0]",
		Credentials: &CredentialProjectionOptions{
			Mode: credentialpolicy.SecretReference,
		},
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if report.Disposition != BlockedDraft || len(report.Requirements) != 1 ||
		report.Requirements[0].Value != canary {
		t.Fatalf("multipart malformed disposition report = %#v", report)
	}
	if strings.Contains(action.Body, canary) ||
		!strings.Contains(action.Body, report.Requirements[0].Replacement) {
		t.Fatalf("multipart body was not conservatively governed: %q", action.Body)
	}
	reasons := make(map[string]bool)
	for _, finding := range report.Findings {
		reasons[finding.ReasonCode] = true
	}
	if !reasons["invalid_multipart_header_value"] {
		t.Fatalf("multipart malformed disposition finding missing: %#v", report.Findings)
	}
}

func TestFinalizeActionBlocksSensitiveMultipartFileWithoutOrphanRequirement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		part           MultipartPart
		sensitiveNames map[string]string
		reasons        []string
	}{
		{
			name: "sensitive structured name",
			part: MultipartPart{Name: "password", FileAssetRef: "credential_file"},
			reasons: []string{
				"credential_multipart_sensitive_file_unsupported",
			},
		},
		{
			name: "XML-sensitive structured name",
			part: MultipartPart{Name: "databasePassword", FileAssetRef: "credential_file"},
			reasons: []string{
				"credential_multipart_sensitive_file_unsupported",
			},
		},
		{
			name:           "adapter-governed structured name",
			part:           MultipartPart{Name: "pin", FileAssetRef: "credential_file"},
			sensitiveNames: map[string]string{"pin": "PIN"},
			reasons: []string{
				"credential_multipart_sensitive_file_unsupported",
			},
		},
		{
			name: "forced disposition conflict",
			part: MultipartPart{
				Name:         "note",
				FileAssetRef: "credential_file",
				Headers: HeaderList{{
					Name:  "Content-Disposition",
					Value: `form-data; name="password"`,
				}},
			},
			reasons: []string{
				"credential_multipart_sensitive_file_unsupported",
				"multipart_name_conflict",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			action, report, err := FinalizeAction(EffectiveRequest{
				Key: "multipart-sensitive-file", Method: "POST", Target: "/upload",
				Body: Body{
					Kind:      BodyMultipart,
					Multipart: &MultipartBody{Parts: []MultipartPart{test.part}},
				},
				Source: SourceRef{Format: "contract", Path: "multipart.json"},
			}, FinalizeOptions{
				ActionPath: "actions[0]",
				Credentials: &CredentialProjectionOptions{
					Mode:           credentialpolicy.SecretReference,
					SensitiveNames: test.sensitiveNames,
				},
			})
			if err != nil {
				t.Fatalf("FinalizeAction: %v", err)
			}
			if report.Disposition != BlockedDraft || len(report.Requirements) != 0 {
				t.Fatalf("sensitive file report = %#v", report)
			}
			if !strings.Contains(action.Body, "{{file.credential_file}}") {
				t.Fatalf("blocked draft lost file binding: %q", action.Body)
			}
			reasonSet := make(map[string]bool)
			for _, finding := range report.Findings {
				reasonSet[finding.ReasonCode] = true
			}
			for _, reason := range test.reasons {
				if !reasonSet[reason] {
					t.Fatalf("findings = %#v, missing %q", report.Findings, reason)
				}
			}
		})
	}
}

func TestFinalizeActionBlocksDynamicMultipartHeaderUntilPostBindingValidationExists(t *testing.T) {
	t.Parallel()
	const canary = "multipart-header-canary-g77"
	tests := []struct {
		name string
		part MultipartPart
	}{
		{
			name: "auth header projected to secret",
			part: MultipartPart{
				Name: "note",
				Headers: HeaderList{{
					Name: "Authorization", Value: "Bearer " + canary,
				}},
				Data: []byte("safe"),
			},
		},
		{
			name: "dynamic synthesized disposition name",
			part: MultipartPart{
				Name: "{{var.part_name}}",
				Data: []byte("safe"),
			},
		},
		{
			name: "dynamic authored disposition parameter",
			part: MultipartPart{
				Name: "note",
				Headers: HeaderList{{
					Name: "Content-Disposition", Value: `form-data; name="{{secret.PART_NAME}}"`,
				}},
				Data: []byte("safe"),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			action, report, err := FinalizeAction(EffectiveRequest{
				Key: "multipart-dynamic-header", Method: "POST", Target: "/upload",
				Body: Body{
					Kind:      BodyMultipart,
					Multipart: &MultipartBody{Parts: []MultipartPart{test.part}},
				},
				Source: SourceRef{Format: "contract", Path: "multipart.json"},
			}, FinalizeOptions{
				ActionPath: "actions[0]",
				Credentials: &CredentialProjectionOptions{
					Mode: credentialpolicy.SecretReference,
				},
			})
			if err != nil {
				t.Fatalf("FinalizeAction: %v", err)
			}
			if report.Disposition != BlockedDraft {
				t.Fatalf("dynamic MIME header disposition = %q, report %#v", report.Disposition, report)
			}
			found := false
			for _, finding := range report.Findings {
				found = found || finding.ReasonCode == "multipart_dynamic_header_unvalidated"
			}
			if !found {
				t.Fatalf("dynamic MIME header finding missing: %#v", report.Findings)
			}
			if test.name == "auth header projected to secret" {
				if len(report.Requirements) != 1 ||
					strings.Contains(action.Body, canary) ||
					!strings.Contains(action.Body, report.Requirements[0].Replacement) {
					t.Fatalf("projected multipart header = body %q report %#v", action.Body, report)
				}
			}
		})
	}
}

func TestFinalizeActionBlocksDynamicHeadersInRawMultipartWireBody(t *testing.T) {
	t.Parallel()
	const (
		boundary = "apex-raw-boundary"
		canary   = "raw-multipart-header-canary-h88"
	)
	tests := []struct {
		name             string
		partHeader       string
		wantRequirements int
	}{
		{
			name:             "credential projection introduces template",
			partHeader:       "Authorization: Bearer " + canary,
			wantRequirements: 1,
		},
		{
			name:       "authored runtime template",
			partHeader: "X-Part-Metadata: {{var.part_metadata}}",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			payload := "--" + boundary + "\r\n" +
				"Content-Disposition: form-data; name=\"note\"\r\n" +
				test.partHeader + "\r\n\r\nsafe\r\n" +
				"--" + boundary + "--\r\n"
			action, report, err := FinalizeAction(EffectiveRequest{
				Key: "raw-multipart-dynamic-header", Method: "POST", Target: "/upload",
				Body: Body{
					Kind:      BodyMultipart,
					MediaType: "multipart/form-data; boundary=" + boundary,
					Payload:   []byte(payload),
				},
				Source: SourceRef{Format: "contract", Path: "multipart.raw"},
			}, FinalizeOptions{
				ActionPath: "actions[0]",
				Credentials: &CredentialProjectionOptions{
					Mode: credentialpolicy.SecretReference,
				},
			})
			if err != nil {
				t.Fatalf("FinalizeAction: %v", err)
			}
			if report.Disposition != BlockedDraft ||
				len(report.Requirements) != test.wantRequirements {
				t.Fatalf("raw multipart report = %#v", report)
			}
			found := false
			for _, finding := range report.Findings {
				found = found || finding.ReasonCode == "multipart_dynamic_header_unvalidated"
			}
			if !found {
				t.Fatalf("raw multipart dynamic header finding missing: %#v", report.Findings)
			}
			if test.wantRequirements == 1 {
				if strings.Contains(action.Body, canary) ||
					!strings.Contains(action.Body, report.Requirements[0].Replacement) {
					t.Fatalf("raw multipart credential header = body %q report %#v", action.Body, report)
				}
			}
		})
	}
}

func TestRuntimeHeaderRequirementMapReconciliationIsDeterministic(t *testing.T) {
	t.Parallel()
	requirement := credentialpolicy.Requirement{
		ID: "auth", Label: "Authorization", SuggestedName: "AUTH",
		Replacement: "{{secret.AUTH}}", Value: "protected-auth",
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "request", Field: "headers", Pointer: "/Authorization",
		}},
	}
	before := map[string]runtimeHeaderSurfaceSnapshot{
		"actions[0]": {
			headers: map[string]string{
				"Authorization": "{{secret.AUTH}}",
				"authorization": "ambiguous-other",
			},
		},
	}
	after := map[string]runtimeHeaderSurfaceSnapshot{
		"actions[0]": {
			headers: map[string]string{"Authorization": "{{secret.AUTH}}"},
		},
	}
	for iteration := 0; iteration < 25; iteration++ {
		got := reconcileRuntimeHeaderRequirements(pendingRuntimeRequirements{
			actionPath:  "actions[0]",
			requestKey:  "request",
			requirement: []credentialpolicy.Requirement{requirement},
		}, before, after)
		if len(got) != 1 || got[0].ID != requirement.ID {
			t.Fatalf("iteration %d reconciliation = %#v", iteration, got)
		}
	}
}

func TestFinalizeScriptPrunesAndRemapsRequirementsRemovedByFinalRuntimeReview(t *testing.T) {
	t.Parallel()
	const (
		removedValue  = "removed-connection-canary"
		retainedValue = "retained-auth-canary"
	)
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "request",
		Method: "GET",
		Target: "/",
		HeaderList: dsl.HeaderList{
			{Name: "X-Safe", Value: "yes"},
			{Name: "Connection", Value: "{{secret.CONNECTION_TOKEN}}"},
			{Name: "Authorization", Value: "{{secret.AUTH_TOKEN}}"},
		},
		Headers: map[string]string{
			"X-Safe":        "yes",
			"Connection":    "{{secret.CONNECTION_TOKEN}}",
			"Authorization": "{{secret.AUTH_TOKEN}}",
		},
	}}
	report := Report{
		RequestKey:  "request",
		ActionPath:  "actions[0]",
		Disposition: ExecutableExact,
		Requirements: []credentialpolicy.Requirement{
			{
				ID: "removed", Label: "Connection token", SuggestedName: "CONNECTION_TOKEN",
				Replacement: "{{secret.CONNECTION_TOKEN}}", Value: removedValue,
				Targets: []credentialpolicy.TargetRef{{
					RequestKey: "request", Field: "header_list", Index: 1, Occurrence: 1,
				}},
			},
			{
				ID: "retained", Label: "Authorization token", SuggestedName: "AUTH_TOKEN",
				Replacement: "{{secret.AUTH_TOKEN}}", Value: retainedValue,
				Targets: []credentialpolicy.TargetRef{{
					RequestKey: "request", Field: "header_list", Index: 2, Occurrence: 2,
				}},
			},
		},
	}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if result.Disposition != BlockedDraft || len(result.Requirements) != 1 {
		t.Fatalf("result disposition/requirements = %q/%#v", result.Disposition, result.Requirements)
	}
	requirement := result.Requirements[0]
	if requirement.ID != "retained" || requirement.Value != retainedValue ||
		len(requirement.Targets) != 1 || requirement.Targets[0].Index != 1 ||
		requirement.Targets[0].Occurrence != 1 {
		t.Fatalf("retained requirement was not remapped exactly: %#v", requirement)
	}
	if len(result.Script.Actions[0].HeaderList) != 2 ||
		result.Script.Actions[0].HeaderList[1].Value != requirement.Replacement {
		t.Fatalf("sanitized action headers = %#v", result.Script.Actions[0].HeaderList)
	}
}

func TestFinalizeActionAuditModePreservesValueAndAddsUnmanagedGate(t *testing.T) {
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:     "audit",
		Name:    "Audit",
		Method:  "GET",
		Target:  "/",
		Headers: HeaderList{{Name: "Authorization", Value: "literal"}},
		Source:  SourceRef{Format: "contract", Path: "request.txt"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.Audit},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if action.HeaderList[0].Value != "literal" {
		t.Fatalf("audit header = %q", action.HeaderList[0].Value)
	}
	if action.Metadata["execution_blocker"] != "import_credentials_unmanaged" {
		t.Fatalf("execution blocker = %#v", action.Metadata["execution_blocker"])
	}
	if report.Disposition != BlockedDraft {
		t.Fatalf("disposition = %q", report.Disposition)
	}
}

func TestProjectCredentialsPreservesDuplicateJSONKeysAndTypes(t *testing.T) {
	request := EffectiveRequest{
		Key: "json", Method: "POST", Target: "/",
		Body:   Body{Kind: BodyJSON, Payload: []byte(`{"password":"first","password":"second","count":7,"enabled":true}`)},
		Source: SourceRef{Format: "contract", Path: "request.json"},
	}
	projected, report, err := ProjectCredentials(request, CredentialProjectionOptions{Mode: credentialpolicy.SecretReference})
	if err != nil {
		t.Fatalf("ProjectCredentials: %v", err)
	}
	if report.Disposition != BlockedDraft {
		t.Fatalf("disposition = %q, want blocked duplicate-key draft", report.Disposition)
	}
	body := string(projected.Body.Payload)
	if strings.Count(body, `"password"`) != 2 || !strings.Contains(body, `"count":7`) ||
		!strings.Contains(body, `"enabled":true`) {
		t.Fatalf("typed/duplicate JSON shape changed: %s", body)
	}
	if len(report.Requirements) != 2 {
		t.Fatalf("requirements = %#v", report.Requirements)
	}
	ids := []string{report.Requirements[0].ID, report.Requirements[1].ID}
	if reflect.DeepEqual(ids[0], ids[1]) {
		t.Fatalf("duplicate occurrences shared an ID: %#v", ids)
	}
}

func TestFinalizeActionValidatesSourceHeadersBeforeCredentialProjection(t *testing.T) {
	t.Parallel()
	const (
		authCanary   = "source-auth-canary-a17c"
		cookieCanary = "source-cookie-canary-b28d"
		proxyCanary  = "source-proxy-canary-c39e"
	)
	request := EffectiveRequest{
		Key:    "unsafe-source-headers",
		Name:   "Unsafe source headers",
		Method: "GET",
		Target: "/",
		Headers: HeaderList{
			{Name: "X-Before", Value: "safe", Source: SourceRef{Pointer: "/headers/0"}},
			{Name: "Authorization", Value: "Bearer {{var.auth}}", Source: SourceRef{Pointer: "/headers/1"}},
			{Name: "Cookie", Value: "session={{var.cookie}}", Source: SourceRef{Pointer: "/headers/2"}},
			{Name: "Proxy-Authorization", Value: "Basic " + proxyCanary, Source: SourceRef{Pointer: "/headers/3"}},
			{Name: "X-After", Value: "safe", Source: SourceRef{Pointer: "/headers/4"}},
		},
		Source: SourceRef{Format: "contract", Path: "request.json", Pointer: "/request"},
	}
	action, report, err := FinalizeAction(request, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{
			Mode: credentialpolicy.SecretReference,
			SourceVariables: map[string]credentialpolicy.SourceVariable{
				"auth":   {Value: authCanary + "\r\nInjected: yes"},
				"cookie": {Value: cookieCanary + "; malformed"},
			},
		},
		ActionPath: "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if report.Disposition != BlockedDraft || len(report.Requirements) != 0 {
		t.Fatalf("disposition/requirement count = %q/%d, want blocked with no omitted-header requirements", report.Disposition, len(report.Requirements))
	}
	if !reflect.DeepEqual(action.HeaderList, dsl.HeaderList{
		{Name: "X-Before", Value: "safe"},
		{Name: "X-After", Value: "safe"},
	}) {
		t.Fatalf("sanitized header list = %#v", action.HeaderList)
	}
	marker := headerFidelityMarker(t, action)
	reasons, _ := marker["reason_codes"].([]string)
	for _, reason := range []string{
		"http_header_invalid_header_value",
		"http_header_transport_owned_header",
	} {
		if !sliceContains(reasons, reason) {
			t.Fatalf("header fidelity reasons = %#v", reasons)
		}
	}
	if occurrences := metadataObjectValues(marker["omitted_occurrences"]); len(occurrences) != 3 {
		t.Fatalf("omitted occurrence count = %d, want 3", len(occurrences))
	}

	encoded, err := json.Marshal(struct {
		Action dsl.Action `json:"action"`
		Report Report     `json:"report"`
	}{Action: action, Report: report})
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{authCanary, cookieCanary, proxyCanary} {
		if strings.Contains(string(encoded), canary) {
			t.Fatal("source credential appeared in the disclosure-safe action/report")
		}
	}

	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if len(result.ValidationIssues) != 0 || result.Disposition != BlockedDraft ||
		result.Script.Config.HTTP.Traceparent == nil || *result.Script.Config.HTTP.Traceparent {
		t.Fatalf("finalized blocked draft invariant failed: disposition=%q issues=%d traceparent=%v",
			result.Disposition, len(result.ValidationIssues), result.Script.Config.HTTP.Traceparent)
	}
}

func TestFinalizeActionValidatesMaterializedTypedAuthWireValues(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "unsafe-source-auth",
		Name:   "Unsafe source auth",
		Method: "GET",
		Target: "/",
		Auth: Auth{
			Mode:   AuthBearer,
			Token:  "{{var.auth_token}}",
			Source: SourceRef{Format: "contract", Path: "request.json", Pointer: "/auth"},
		},
		Source: SourceRef{Format: "contract", Path: "request.json", Pointer: "/request"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{
			Mode: credentialpolicy.SecretReference,
			SourceVariables: map[string]credentialpolicy.SourceVariable{
				"auth_token": {Value: "source-token\r\nInjected: yes"},
			},
		},
		ActionPath: "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if report.Disposition != BlockedDraft ||
		len(report.Requirements) != 0 ||
		action.Auth == nil ||
		action.Auth.Type != httpprotocol.AuthTypeNone {
		t.Fatalf("invalid typed auth was not replaced by blocked no-auth: action=%#v report=%#v", action.Auth, report)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.ReasonCode == "invalid_auth_wire_value" {
			found = true
		}
	}
	if !found {
		t.Fatalf("invalid auth wire finding missing: %#v", report.Findings)
	}
}

func TestFinalizeActionBlocksDigestCredentialsRejectedByRuntime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		username string
		password string
	}{
		{name: "whitespace username", username: " \t ", password: "secret"},
		{name: "empty password", username: "alice"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			action, report, err := FinalizeAction(EffectiveRequest{
				Key: "invalid-digest-auth", Method: "GET", Target: "/",
				Auth: Auth{
					Mode:     AuthDigest,
					Username: test.username,
					Password: test.password,
				},
				Source: SourceRef{Format: "contract", Path: "request.json"},
			}, FinalizeOptions{
				ActionPath: "actions[0]",
				Credentials: &CredentialProjectionOptions{
					Mode: credentialpolicy.SecretReference,
				},
			})
			if err != nil {
				t.Fatalf("FinalizeAction: %v", err)
			}
			if report.Disposition != BlockedDraft ||
				len(report.Requirements) != 0 ||
				action.Auth == nil ||
				action.Auth.Type != httpprotocol.AuthTypeNone {
				t.Fatalf("invalid digest auth projection = action %#v report %#v", action.Auth, report)
			}
			found := false
			for _, finding := range report.Findings {
				found = found || finding.ReasonCode == "invalid_auth_wire_value"
			}
			if !found {
				t.Fatalf("invalid digest auth finding missing: %#v", report.Findings)
			}
		})
	}
}

func TestFinalizeActionBlocksAmbiguousBasicUsername(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(EffectiveRequest{
		Key: "invalid-basic-auth", Method: "GET", Target: "/",
		Auth: Auth{
			Mode:     AuthBasic,
			Username: "tenant:operator",
			Password: "password",
		},
		Source: SourceRef{Format: "contract", Path: "request.json"},
	}, FinalizeOptions{
		ActionPath: "actions[0]",
		Credentials: &CredentialProjectionOptions{
			Mode: credentialpolicy.SecretReference,
		},
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if report.Disposition != BlockedDraft ||
		len(report.Requirements) != 0 ||
		action.Auth == nil ||
		action.Auth.Type != httpprotocol.AuthTypeNone {
		t.Fatalf("ambiguous basic auth projection = action %#v report %#v", action.Auth, report)
	}
	found := false
	for _, finding := range report.Findings {
		found = found || finding.ReasonCode == "invalid_auth_wire_value"
	}
	if !found {
		t.Fatalf("invalid auth wire finding missing: %#v", report.Findings)
	}
}

func TestFinalizeActionPrunesAndRemapsRequirementsForOmittedHeaders(t *testing.T) {
	t.Parallel()
	const droppedCanary = "omitted-proxy-secret-d41f"
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "requirement-remap",
		Method: "GET",
		Target: "/",
		Headers: HeaderList{
			{Name: "Proxy-Authorization", Value: "Basic " + droppedCanary},
			{Name: "Authorization", Value: "{{secret.KEPT_AUTH}}"},
		},
	}, FinalizeOptions{
		ActionPath: "actions[0]",
		Requirements: []credentialpolicy.Requirement{
			{
				ID: "drop", Label: "Proxy auth", SuggestedName: "PROXY_AUTH",
				Replacement: "{{secret.PROXY_AUTH}}", Value: droppedCanary,
				Targets: []credentialpolicy.TargetRef{{
					RequestKey: "requirement-remap", Field: "header_list", Index: 0,
				}},
			},
			{
				ID: "keep", Label: "Origin auth", SuggestedName: "KEPT_AUTH",
				Replacement: "{{secret.KEPT_AUTH}}", Value: "protected-origin-auth",
				Targets: []credentialpolicy.TargetRef{{
					RequestKey: "requirement-remap", Field: "header_list", Index: 1, Occurrence: 1,
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if len(action.HeaderList) != 1 || action.HeaderList[0].Name != "Authorization" {
		t.Fatalf("sanitized headers = %#v", action.HeaderList)
	}
	if len(report.Requirements) != 1 || report.Requirements[0].ID != "keep" ||
		len(report.Requirements[0].Targets) != 1 ||
		report.Requirements[0].Targets[0].Index != 0 ||
		report.Requirements[0].Targets[0].Occurrence != 0 {
		t.Fatal("omitted-header requirements were not pruned and remapped")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), droppedCanary) {
		t.Fatal("omitted header credential survived in the public report")
	}
}

func TestFinalizeActionRemapsPreGovernedHeaderOccurrencesAfterOmission(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(EffectiveRequest{
		Key: "pre-governed-remap", Method: "GET", Target: "/",
		Headers: HeaderList{
			{Name: "Proxy-Authorization", Value: "Basic source-value"},
			{Name: "Cookie", Value: "session={{secret.EXISTING_SESSION}}"},
		},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{
			Mode:                         credentialpolicy.SecretReference,
			PreGovernedHeaderOccurrences: map[int]struct{}{1: {}},
		},
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if len(report.Requirements) != 0 || len(action.HeaderList) != 1 ||
		action.HeaderList[0].Name != "Cookie" ||
		action.HeaderList[0].Value != "session={{secret.EXISTING_SESSION}}" {
		t.Fatal("pre-governed occurrence was not remapped after the earlier omission")
	}
}

func TestProjectCredentialsKeepsBaseAndTargetURLBindingsDistinct(t *testing.T) {
	t.Parallel()

	const (
		baseCanary   = "base-url-secret-canary-61a2"
		targetCanary = "target-url-secret-canary-94bd"
	)
	request := EffectiveRequest{
		Key:           "distinct-url-surfaces",
		Method:        "GET",
		BaseURL:       "https://example.test/root?token=" + baseCanary,
		BaseURLSource: SourceRef{Format: "fixture", Pointer: "/request"},
		Target:        "/orders?token=" + targetCanary,
		Source:        SourceRef{Format: "fixture", Pointer: "/request"},
	}
	projected, report, err := ProjectCredentials(request, CredentialProjectionOptions{
		Mode: credentialpolicy.SecretReference,
	})
	if err != nil {
		t.Fatalf("ProjectCredentials: %v", err)
	}
	if strings.Contains(projected.BaseURL, baseCanary) ||
		strings.Contains(projected.Target, targetCanary) {
		t.Fatalf("projected request retained a URL credential: %#v", projected)
	}
	if len(report.Requirements) != 2 {
		t.Fatalf("requirements = %#v, want distinct base and target requirements", report.Requirements)
	}
	ids := map[string]bool{}
	values := map[string]string{}
	for _, requirement := range report.Requirements {
		ids[requirement.ID] = true
		if len(requirement.Targets) != 1 {
			t.Fatalf("requirement targets = %#v", requirement.Targets)
		}
		values[requirement.Targets[0].Field] = requirement.Value
	}
	if len(ids) != 2 ||
		values["url.query"] != targetCanary ||
		values["base_url.query"] != baseCanary {
		t.Fatalf("URL requirements were conflated: %#v", report.Requirements)
	}
}
