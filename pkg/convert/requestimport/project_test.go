package requestimport

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestProjectPreservesWireShapeAndFinalizesSharedSidecars(t *testing.T) {
	t.Parallel()
	base := validBaseScript()
	inputFinding := importreview.Finding{
		Code:       "wire_review",
		Element:    "wire semantics",
		Detail:     "confirm source-specific behavior",
		ActionPath: "caller-owned",
		Gate:       importreview.GatePromotion,
	}
	requirement := credentialpolicy.Requirement{
		ID:            "requirement-1",
		Label:         "API token",
		SuggestedName: "API_TOKEN",
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "create-order",
			Field:      "header_list",
			Index:      1,
		}},
		Value: "migration-only",
	}
	inputs := []RequestInput{{
		Request: Request{
			Key:         "create-order",
			Name:        "Create order",
			Method:      " post ",
			Target:      " /orders ",
			HTTPVersion: "HTTP/1.1",
			Headers: HeaderList{
				{Name: "X-Trace", Value: "first"},
				{Name: "x-trace", Value: "second"},
				{Name: "Accept", Value: "application/json"},
			},
			Body: Body{
				Kind:      BodyJSON,
				MediaType: "application/json",
				Payload:   []byte(`{"sku":"A-1"}`),
				Assets: []Asset{{
					Name:        "order_fixture",
					FileName:    "order.json",
					ContentType: "application/json",
					Data:        []byte(`{"fixture":true}`),
				}},
			},
			Auth:      Auth{Mode: AuthBasic, Username: "user", Password: "{{secret.PASSWORD}}"},
			Timeout:   Timeout{Mode: TimeoutBounded, Milliseconds: 2500},
			Redirects: Redirects{Mode: RedirectLimited, Max: 4},
			Metadata:  map[string]any{"source": "fixture", "precedence": "request"},
			Source:    SourceRef{Format: "fixture", Path: "collection.json", Pointer: "/requests/0"},
		},
		Overlay: ActionOverlay{
			Params:   map[string]any{"source_option": "kept"},
			Metadata: map[string]any{"precedence": "overlay", "overlay": true},
		},
		Findings:     []importreview.Finding{inputFinding},
		Requirements: []credentialpolicy.Requirement{requirement},
	}}

	draft, err := Project(base, inputs, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Actions) != 0 || len(base.DataSources) != 0 {
		t.Fatalf("Project mutated its base script: %#v", base)
	}
	if !reflect.DeepEqual(inputs[0].Findings[0], inputFinding) {
		t.Fatalf("Project mutated caller-owned findings: %#v", inputs[0].Findings)
	}
	if len(draft.Script.Actions) != 1 {
		t.Fatalf("actions = %#v", draft.Script.Actions)
	}
	action := draft.Script.Actions[0]
	if action.Name != "Create order" || action.Method != "POST" || action.Target != "/orders" || action.Type != "request" {
		t.Fatalf("projected action identity = %#v", action)
	}
	wantHeaders := dsl.HeaderList{
		{Name: "X-Trace", Value: "first"},
		{Name: "x-trace", Value: "second"},
		{Name: "Accept", Value: "application/json"},
		{Name: "Content-Type", Value: "application/json"},
	}
	if !reflect.DeepEqual(action.HeaderList, wantHeaders) {
		t.Fatalf("header_list = %#v, want %#v", action.HeaderList, wantHeaders)
	}
	if !reflect.DeepEqual(action.Headers, map[string]string{
		"x-trace":      "second",
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}) {
		t.Fatalf("compatibility headers = %#v", action.Headers)
	}
	if action.Body != `{"sku":"A-1"}` || action.BodyEncoding != "" {
		t.Fatalf("body projection = %q (%q)", action.Body, action.BodyEncoding)
	}
	if action.Auth == nil || action.Auth.Type != "basic" ||
		action.Auth.Username != "user" || action.Auth.Password != "{{secret.PASSWORD}}" {
		t.Fatalf("auth projection = %#v", action.Auth)
	}
	if action.TimeoutMs != 2500 || action.MaxRedirects == nil || *action.MaxRedirects != 4 {
		t.Fatalf("runtime bounds = timeout %d redirects %#v", action.TimeoutMs, action.MaxRedirects)
	}
	if action.Params["source_option"] != "kept" ||
		action.Metadata["source"] != "fixture" ||
		action.Metadata["precedence"] != "overlay" ||
		action.Metadata["overlay"] != true ||
		action.Metadata["http_version"] != "HTTP/1.1" {
		t.Fatalf("source overlays = params %#v metadata %#v", action.Params, action.Metadata)
	}
	if action.Metadata["review_gate"] != "wire_review" ||
		action.Metadata["review_required"] != true ||
		action.Metadata["promotion_blocker"] != true {
		t.Fatalf("durable review metadata = %#v", action.Metadata)
	}
	if len(draft.Script.DataSources) != 1 {
		t.Fatalf("data sources = %#v", draft.Script.DataSources)
	}
	asset := draft.Script.DataSources[0]
	if asset.Name != "order_fixture" || asset.Type != "file" ||
		asset.FileName != "order.json" || asset.Encoding != "text" ||
		asset.InlineData != `{"fixture":true}` {
		t.Fatalf("projected body asset = %#v", asset)
	}
	if len(draft.Requirements) != 1 ||
		draft.Requirements[0].Targets[0].RequestKey != "create-order" ||
		draft.Requirements[0].Value != "migration-only" {
		t.Fatalf("requirements = %#v", draft.Requirements)
	}
	inputs[0].Requirements[0].Targets[0].RequestKey = "mutated"
	if draft.Requirements[0].Targets[0].RequestKey != "create-order" {
		t.Fatal("Project retained caller-owned requirement target storage")
	}
	if len(draft.Findings) != 1 ||
		draft.Findings[0].ActionPath != "actions[0]" ||
		draft.Findings[0].Location != "/requests/0" {
		t.Fatalf("findings = %#v", draft.Findings)
	}

	result, err := draft.Finalize(FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ValidationIssues) != 0 {
		t.Fatalf("validation issues = %#v", result.ValidationIssues)
	}
	if result.Readiness.State != importreview.ReviewRequired ||
		!result.Readiness.DraftValid ||
		!result.Readiness.ReplayReady ||
		result.Readiness.PromotionReady {
		t.Fatalf("readiness = %#v", result.Readiness)
	}
	if len(result.Diagnostics) != 1 ||
		result.Diagnostics[0].Severity != diag.Warning ||
		result.Diagnostics[0].Location != "/requests/0" {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestFinalizeActionAndScriptFacade(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "bounded",
		Name:   "Bounded request",
		Method: "GET",
		Target: "/bounded",
		Headers: HeaderList{
			{Name: "X-Dupe", Value: "first"},
			{Name: "X-Dupe", Value: "second"},
		},
		Timeout: Timeout{
			Mode:         TimeoutBounded,
			Milliseconds: httpprotocol.Capabilities().MaxTimeoutMS + 1,
		},
	}, FinalizeOptions{ActionPath: "actions[0]"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Disposition != ExecutableBounded || report.ActionPath != "actions[0]" {
		t.Fatalf("report = %#v", report)
	}
	if action.TimeoutMs != httpprotocol.Capabilities().MaxTimeoutMS ||
		len(action.HeaderList) != 2 ||
		action.Headers["X-Dupe"] != "second" {
		t.Fatalf("action = %#v", action)
	}
	base := validBaseScript()
	base.Actions = []dsl.Action{action}
	result, err := FinalizeScript(base, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != ExecutableBounded ||
		result.Readiness.State != importreview.ReviewRequired ||
		!result.Readiness.ReplayReady {
		t.Fatalf("result = %#v", result)
	}
}

func TestFinalizeScriptRejectsInvalidDraftInvariant(t *testing.T) {
	t.Parallel()
	invalid := validBaseScript()
	invalid.Actions = []dsl.Action{{Name: "bad", Type: "request", Method: "GET"}}
	result, err := FinalizeScript(invalid, nil, FinalizeOptions{})
	if err == nil || !strings.Contains(err.Error(), "internal request import invariant") {
		t.Fatalf("FinalizeScript() = %#v, %v", result, err)
	}
}

func TestFinalizersBackfillCredentialPreservationMetadataFromGeneratedSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		finalize func(*dsl.Script) (*Result, error)
	}{
		{
			name: "FinalizeScript",
			finalize: func(script *dsl.Script) (*Result, error) {
				return FinalizeScript(script, nil, FinalizeOptions{})
			},
		},
		{
			name: "Draft.Finalize",
			finalize: func(script *dsl.Script) (*Result, error) {
				return (&Draft{Script: script}).Finalize(FinalizeOptions{})
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := validBaseScript()
			script.GeneratedFrom = "fixture-import"
			script.Actions = []dsl.Action{{
				Name: "request", Type: "request", Method: "GET", Target: "/",
			}}

			result, err := test.finalize(script)
			if err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
				t.Fatalf("import metadata was not created: %#v", result.Script.Metadata)
			}
			if got := result.Script.Metadata.Import.Source; got != "fixture-import" {
				t.Fatalf("import source = %q, want fixture-import", got)
			}
			if got := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey]; got !=
				dsl.ImportPreservedCredentialPolicyPreserve {
				t.Fatalf("credential policy = %#v, want preserve", got)
			}
		})
	}
}

func TestFinalizeScriptRecognizesLegacyImporterFilePlaceholder(t *testing.T) {
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name: "upload", Type: "request", Method: "POST", Target: "/upload",
		Body: "{{file.payload}}",
	}}
	script.DataSources = []dsl.DataSource{{
		Name:       "payload",
		Type:       "file",
		InlineData: "[APEXLOAD_OPENAPI_FILE_PLACEHOLDER]",
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.DataSources[0].InlineData; got != "[APEXLOAD_OPENAPI_FILE_PLACEHOLDER]" {
		t.Fatalf("legacy placeholder changed: %q", got)
	}
	if result.Script.Actions[0].Metadata["execution_blocker"] != "import_missing_file_bindings" {
		t.Fatalf("metadata = %#v", result.Script.Actions[0].Metadata)
	}
	if result.Disposition != BlockedDraft || result.Readiness.ReplayReady {
		t.Fatalf("result = %#v", result)
	}
}

func TestProjectPlacesRequestsInEveryLifecyclePhase(t *testing.T) {
	t.Parallel()
	inputs := []RequestInput{
		{Phase: PhaseTeardown, Request: Request{Name: "finish", Target: "/finish"}},
		{Phase: PhaseInit, Request: Request{Name: "start", Target: "/start"}},
		{Request: Request{Name: "main", Target: "/main"}},
	}
	draft, err := Project(validBaseScript(), inputs, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Script.Init) != 1 || draft.Script.Init[0].Name != "start" ||
		len(draft.Script.Actions) != 1 || draft.Script.Actions[0].Name != "main" ||
		len(draft.Script.Teardown) != 1 || draft.Script.Teardown[0].Name != "finish" {
		t.Fatalf("lifecycle projection = init %#v actions %#v teardown %#v",
			draft.Script.Init, draft.Script.Actions, draft.Script.Teardown)
	}
	if draft.Script.Init[0].Method != "GET" ||
		draft.Script.Actions[0].Method != "GET" ||
		draft.Script.Teardown[0].Method != "GET" {
		t.Fatal("empty source methods did not receive the shared GET default")
	}
}

func TestProjectAuthModesPreserveInheritanceAndExplicitNoAuth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		auth     Auth
		wantNil  bool
		wantType string
	}{
		{name: "unspecified inherits", auth: Auth{}, wantNil: true},
		{name: "explicit inherit", auth: Auth{Mode: AuthInherit}, wantNil: true},
		{name: "explicit no auth", auth: Auth{Mode: AuthNone}, wantType: "none"},
		{name: "basic", auth: Auth{Mode: AuthBasic}, wantType: "basic"},
		{name: "bearer", auth: Auth{Mode: AuthBearer}, wantType: "bearer"},
		{name: "digest", auth: Auth{Mode: AuthDigest}, wantType: "digest"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			draft, err := Project(validBaseScript(), []RequestInput{{
				Request: Request{Target: "/auth", Auth: test.auth},
			}}, ProjectOptions{})
			if err != nil {
				t.Fatal(err)
			}
			auth := draft.Script.Actions[0].Auth
			if test.wantNil {
				if auth != nil {
					t.Fatalf("auth = %#v, want nil inheritance", auth)
				}
				return
			}
			if auth == nil || auth.Type != test.wantType {
				t.Fatalf("auth = %#v, want type %q", auth, test.wantType)
			}
		})
	}
}

func TestUnsupportedAuthRetainsCredentialsBehindDurableGate(t *testing.T) {
	t.Parallel()
	draft, err := Project(validBaseScript(), []RequestInput{{
		Request: Request{
			Name:   "Windows auth",
			Target: "/protected",
			Auth: Auth{
				Mode:       AuthUnsupported,
				SourceMode: "NTLM",
				Username:   "user",
				Password:   "{{secret.NTLM_PASSWORD}}",
				Domain:     "example",
			},
		},
	}}, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	action := draft.Script.Actions[0]
	if action.Auth == nil || action.Auth.Type != "ntlm" ||
		action.Auth.Username != "user" ||
		action.Auth.Password != "{{secret.NTLM_PASSWORD}}" ||
		action.Auth.Domain != "example" {
		t.Fatalf("unsupported source auth did not retain its exact review fields: %#v", action.Auth)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("execution gate = %#v", action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(review["reason_codes"], []string{"unsupported_http_auth_ntlm"}) {
		t.Fatalf("durable review = %#v", review)
	}
	result, err := draft.Finalize(FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Readiness.State != importreview.Blocked ||
		result.Readiness.ReplayReady ||
		result.Readiness.PromotionReady ||
		!reflect.DeepEqual(result.Readiness.BlockingCodes, []string{"import_review_required"}) {
		t.Fatalf("readiness = %#v", result.Readiness)
	}
}

func TestUnsupportedMethodBecomesValidGenericReviewDraft(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:           "custom-method",
		CandidateID:   "candidate-custom-method",
		SourceProfile: "fixture",
		Method:        "PROPFIND",
		Target:        "/collection",
		Source:        SourceRef{Format: "fixture", Path: "collection.json", Pointer: "/requests/0"},
	}, FinalizeOptions{ActionPath: "actions[0]"})
	if err != nil {
		t.Fatal(err)
	}
	if action.Method != "GET" || action.Metadata["source_http_method"] != "PROPFIND" {
		t.Fatalf("placeholder action = %#v", action)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("metadata = %#v", action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if review["source"] != "fixture" ||
		!reflect.DeepEqual(review["reason_codes"], []string{"unsupported_http_method"}) ||
		!reflect.DeepEqual(review["source_refs"], []string{"/requests/0"}) {
		t.Fatalf("import review = %#v", review)
	}
	if report.Disposition != BlockedDraft {
		t.Fatalf("report = %#v", report)
	}
	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ValidationIssues) != 0 || result.Disposition != BlockedDraft || result.Readiness.ReplayReady {
		t.Fatalf("result = %#v", result)
	}
}

func TestInvalidAndExcessAssertionsBecomeValidBlockedDraft(t *testing.T) {
	t.Parallel()
	assertions := make([]dsl.Assertion, httpprotocol.Capabilities().MaxAssertions+1)
	for index := range assertions {
		assertions[index] = dsl.Assertion{Type: "status_code", Expected: 200}
	}
	assertions[1] = dsl.Assertion{Type: "body_regex_capture_equals", Value: "(", Expected: "value"}
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:        "assertions",
		Method:     "GET",
		Target:     "/assertions",
		Assertions: assertions,
		Source:     SourceRef{Format: "fixture", Pointer: "/requests/1"},
	}, FinalizeOptions{ActionPath: "actions[0]"})
	if err != nil {
		t.Fatal(err)
	}
	if len(action.Assertions) != httpprotocol.Capabilities().MaxAssertions-1 {
		t.Fatalf("projected assertions = %d", len(action.Assertions))
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(review["reason_codes"], []string{
		"http_assertion_limit_exceeded",
		"unsupported_http_assertion",
	}) {
		t.Fatalf("import review = %#v", review)
	}
	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ValidationIssues) != 0 || result.Disposition != BlockedDraft {
		t.Fatalf("result = %#v", result)
	}
}

func TestEffectiveRequestNativeFieldsProjectWithoutOverlay(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:           "native-fields",
		CandidateID:   "candidate-native",
		SourceProfile: "fixture",
		Protocol:      ProtocolSOAP,
		Method:        "POST",
		Target:        "/soap",
		BaseURL:       "https://soap.example.test",
		URLPolicy:     URLPolicyPreserveRaw,
		AuthConfig:    &dsl.AuthConfig{Type: "digest", Username: "user", Password: "{{secret.PASSWORD}}"},
		ThinkTime:     dsl.ThinkTimeConfig{Type: "recorded", MeanMs: 250},
		Assertions:    []dsl.Assertion{{Type: "status_code", Expected: 200}},
		Extractors:    []dsl.Extractor{{Name: "id", Source: "body", Type: "jsonpath", Expression: "$.id"}},
		SOAP:          &dsl.SOAPActionConfig{Action: "urn:lookup", Operation: "Lookup", BodyMode: "raw_xml"},
		SourceRefs:    []SourceRef{{Format: "fixture", Pointer: "/requests/2"}},
		SourceMappings: []SourceMapping{{
			Source: SourceRef{Format: "fixture", Pointer: "/requests/2/body"},
			Target: "actions[0].body",
		}},
	}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Disposition != ExecutableExact ||
		action.Auth == nil || action.Auth.Type != "digest" ||
		len(action.Assertions) != 1 || len(action.Extractors) != 1 ||
		action.ThinkTime.MeanMs != 250 || action.SOAP == nil ||
		action.Metadata["request_protocol"] != "soap" ||
		action.Metadata["url_encoding_policy"] != "preserve_raw" {
		t.Fatalf("action = %#v; report = %#v", action, report)
	}
}

func TestProjectUsesEffectiveProtocolAndBaseURLWhenScriptOmitsThem(t *testing.T) {
	t.Parallel()
	base := &dsl.Script{Name: "SOAP import", Version: "1.0"}
	draft, err := Project(base, []RequestInput{{Request: EffectiveRequest{
		Key:      "soap-operation",
		Protocol: ProtocolSOAP,
		BaseURL:  "https://soap.example.test/service",
		Method:   "POST",
		Target:   "/service",
	}}}, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Script.Protocol != "soap" ||
		draft.Script.Config.BaseURL != "https://soap.example.test/service" ||
		base.Protocol != "" || base.Config.BaseURL != "" {
		t.Fatalf("projected script = %#v; base = %#v", draft.Script, base)
	}
	result, err := draft.Finalize(FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ValidationIssues) != 0 || len(result.SourceMappings) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestProjectBoundsPolicies(t *testing.T) {
	t.Parallel()
	capabilities := httpprotocol.Capabilities()
	input := RequestInput{Request: Request{
		Target:    "/bounded",
		Timeout:   Timeout{Mode: TimeoutBounded, Milliseconds: capabilities.MaxTimeoutMS + 1},
		Redirects: Redirects{Mode: RedirectUnlimited},
	}}
	tests := []struct {
		name          string
		options       ProjectOptions
		wantErr       bool
		wantState     importreview.State
		wantReplay    bool
		wantPromotion bool
	}{
		{
			name:          "default clamp requires promotion review",
			options:       ProjectOptions{},
			wantState:     importreview.ReviewRequired,
			wantReplay:    true,
			wantPromotion: false,
		},
		{
			name:          "warning clamp remains ready",
			options:       ProjectOptions{Bounds: BoundsClampWithWarning},
			wantState:     importreview.Ready,
			wantReplay:    true,
			wantPromotion: true,
		},
		{
			name:    "reject",
			options: ProjectOptions{Bounds: BoundsReject},
			wantErr: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			draft, err := Project(validBaseScript(), []RequestInput{input}, test.options)
			if test.wantErr {
				if err == nil {
					t.Fatal("Project unexpectedly accepted an out-of-range source setting")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			action := draft.Script.Actions[0]
			if action.TimeoutMs != capabilities.MaxTimeoutMS ||
				action.MaxRedirects == nil ||
				*action.MaxRedirects != capabilities.MaxRedirects {
				t.Fatalf("clamped action = %#v", action)
			}
			if len(draft.Findings) != 2 {
				t.Fatalf("clamp findings = %#v", draft.Findings)
			}
			result, err := draft.Finalize(FinalizeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Readiness.State != test.wantState ||
				result.Readiness.ReplayReady != test.wantReplay ||
				result.Readiness.PromotionReady != test.wantPromotion {
				t.Fatalf("readiness = %#v", result.Readiness)
			}
		})
	}
}

func TestProjectMissingReferencesAreSortedAndBlockExecution(t *testing.T) {
	t.Parallel()
	draft, err := Project(validBaseScript(), []RequestInput{{
		Request: Request{
			Name:   "templated",
			Target: "/items/{{var.id}}",
			MissingReferences: []MissingReference{
				{Namespace: "var", Name: "z"},
				{Namespace: "env", Name: "a"},
				{Namespace: "var", Name: "z"},
			},
		},
	}}, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := draft.Script.Actions[0].Metadata["missing_variables"]; !reflect.DeepEqual(got, []string{"env.a", "var.z"}) {
		t.Fatalf("missing_variables = %#v", got)
	}
	result, err := draft.Finalize(FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Readiness.State != importreview.Blocked ||
		!reflect.DeepEqual(result.Readiness.BlockingCodes, []string{"import_missing_variables"}) {
		t.Fatalf("readiness = %#v", result.Readiness)
	}
}

func TestProjectBodyEncodings(t *testing.T) {
	t.Parallel()
	binary := []byte{0xff, 0x00, 0x81}
	tests := []struct {
		name         string
		body         Body
		wantBody     string
		wantEncoding string
		wantErr      string
	}{
		{name: "empty", body: Body{}},
		{name: "auto text", body: Body{Kind: BodyRaw, Payload: []byte("hello")}, wantBody: "hello"},
		{
			name:         "auto binary",
			body:         Body{Kind: BodyRaw, Payload: binary},
			wantBody:     base64.StdEncoding.EncodeToString(binary),
			wantEncoding: "base64",
		},
		{
			name:         "forced base64",
			body:         Body{Kind: BodyRaw, Encoding: BodyEncodingBase64, Payload: []byte("hello")},
			wantBody:     base64.StdEncoding.EncodeToString([]byte("hello")),
			wantEncoding: "base64",
		},
		{
			name:    "invalid text",
			body:    Body{Kind: BodyRaw, Encoding: BodyEncodingText, Payload: binary},
			wantErr: "invalid UTF-8",
		},
		{
			name:    "unknown encoding",
			body:    Body{Kind: BodyRaw, Encoding: "source-native", Payload: []byte("x")},
			wantErr: "unsupported body encoding",
		},
		{
			name:    "unknown encoding on empty body",
			body:    Body{Encoding: "source-native"},
			wantErr: "unsupported body encoding",
		},
		{
			name:    "content requires a body kind",
			body:    Body{Payload: []byte("x")},
			wantErr: "body kind is required",
		},
		{
			name:    "unknown kind",
			body:    Body{Kind: "future", Payload: []byte("x")},
			wantErr: "unsupported body kind",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body, encoding, err := projectBody(test.body)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("projectBody() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if body != test.wantBody || encoding != test.wantEncoding {
				t.Fatalf("projectBody() = %q, %q; want %q, %q", body, encoding, test.wantBody, test.wantEncoding)
			}
		})
	}
}

func TestProjectAssetsDeduplicateAndPreserveBinary(t *testing.T) {
	t.Parallel()
	data := []byte{0xff, 0x01, 0x02}
	asset := Asset{Name: "binary_part", FileName: "part.bin", Data: data}
	draft, err := Project(validBaseScript(), []RequestInput{{
		Request: Request{
			Target: "/upload",
			Body:   Body{Kind: BodyMultipart, Assets: []Asset{asset, asset}},
		},
	}}, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Script.DataSources) != 1 {
		t.Fatalf("data sources = %#v", draft.Script.DataSources)
	}
	source := draft.Script.DataSources[0]
	if source.Encoding != "base64" || source.InlineData != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("binary asset = %#v", source)
	}
}

func TestProjectRejectsConflictingAssetIdentity(t *testing.T) {
	t.Parallel()
	base := validBaseScript()
	base.DataSources = []dsl.DataSource{{
		Name:       "payload",
		Type:       "file",
		InlineData: "same bytes",
		Encoding:   "text",
		Strategy:   "sequential",
	}}
	_, err := Project(base, []RequestInput{{
		Request: Request{
			Target: "/upload",
			Body: Body{Kind: BodyMultipart, Assets: []Asset{{
				Name: "payload",
				Data: []byte("same bytes"),
			}}},
		},
	}}, ProjectOptions{})
	if err == nil || !strings.Contains(err.Error(), "conflicts with an existing data source") {
		t.Fatalf("Project() error = %v", err)
	}
}

func TestFinalizeScriptTreatsExistingFileBindingAsIdempotentForUnboundReportAsset(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Actions = []dsl.Action{{Name: "upload", Type: "request", Method: "POST", Target: "/upload", Body: "{{file.payload}}"}}
	script.DataSources = []dsl.DataSource{{
		Name:        "payload",
		Type:        "file",
		InlineData:  MissingFilePlaceholder,
		Encoding:    "text",
		FileName:    "payload.bin",
		ContentType: "application/octet-stream",
	}}
	result, err := FinalizeScript(script, []Report{{
		RequestKey:  "upload",
		Disposition: ExecutableExact,
		Assets: []Asset{{
			Name: "payload", FileName: "payload.bin", ContentType: "application/octet-stream",
		}},
	}}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.DataSources) != 1 || result.Disposition != BlockedDraft {
		t.Fatalf("result = %#v", result)
	}
}

func TestProjectRejectsMalformedIR(t *testing.T) {
	t.Parallel()
	valid := Request{Target: "/ok"}
	tests := []struct {
		name    string
		base    *dsl.Script
		input   RequestInput
		options ProjectOptions
		want    string
	}{
		{name: "nil base", input: RequestInput{Request: valid}, want: "base script is required"},
		{
			name:    "unknown bounds policy",
			base:    validBaseScript(),
			input:   RequestInput{Request: valid},
			options: ProjectOptions{Bounds: "future"},
			want:    "unsupported bounds policy",
		},
		{name: "unknown phase", base: validBaseScript(), input: RequestInput{Phase: "middle", Request: valid}, want: "unsupported request phase"},
		{name: "missing target", base: validBaseScript(), input: RequestInput{Request: Request{}}, want: "request target is required"},
		{name: "invalid method", base: validBaseScript(), input: RequestInput{Request: Request{Target: "/", Method: "GET\nInjected"}}, want: "invalid HTTP method"},
		{
			name: "zero bounded timeout",
			base: validBaseScript(),
			input: RequestInput{Request: Request{
				Target:  "/",
				Timeout: Timeout{Mode: TimeoutBounded},
			}},
			want: "must be positive",
		},
		{
			name: "negative redirect limit",
			base: validBaseScript(),
			input: RequestInput{Request: Request{
				Target:    "/",
				Redirects: Redirects{Mode: RedirectLimited, Max: -1},
			}},
			want: "must be non-negative",
		},
		{
			name: "missing reference name",
			base: validBaseScript(),
			input: RequestInput{Request: Request{
				Target:            "/",
				MissingReferences: []MissingReference{{Namespace: "var"}},
			}},
			want: "has no name",
		},
		{
			name: "empty asset name",
			base: validBaseScript(),
			input: RequestInput{Request: Request{
				Target: "/",
				Body:   Body{Kind: BodyMultipart, Assets: []Asset{{Data: []byte("x")}}},
			}},
			want: "body asset name is required",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Project(test.base, []RequestInput{test.input}, test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Project() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestProjectSanitizesMalformedHeadersIntoValidBlockedDrafts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		request    Request
		wantReason string
	}{
		{
			name:       "invalid header name",
			request:    Request{Target: "/", Headers: HeaderList{{Name: "Bad Header", Value: "x"}}},
			wantReason: "http_header_invalid_header_name",
		},
		{
			name:       "header name whitespace is not wire-valid",
			request:    Request{Target: "/", Headers: HeaderList{{Name: " X-Test ", Value: "x"}}},
			wantReason: "http_header_invalid_header_name",
		},
		{
			name:       "invalid header value",
			request:    Request{Target: "/", Headers: HeaderList{{Name: "X-Test", Value: "x\r\nInjected: yes"}}},
			wantReason: "http_header_invalid_header_value",
		},
		{
			name:       "empty user agent would be silently omitted",
			request:    Request{Target: "/", Headers: HeaderList{{Name: "User-Agent", Value: ""}}},
			wantReason: "http_header_invalid_header_value",
		},
		{
			name: "invalid synthesized content type",
			request: Request{
				Target: "/",
				Body:   Body{Kind: BodyRaw, MediaType: "text/plain\r\nInjected: yes"},
			},
			wantReason: "http_header_invalid_header_value",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			draft, err := Project(validBaseScript(), []RequestInput{{Request: test.request}}, ProjectOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(draft.Script.Actions) != 1 || len(draft.Script.Actions[0].HeaderList) != 0 {
				t.Fatalf("sanitized actions = %#v", draft.Script.Actions)
			}
			action := draft.Script.Actions[0]
			if action.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatalf("metadata = %#v", action.Metadata)
			}
			marker, _ := action.Metadata[ImportHeaderFidelityMetadataKey].(map[string]any)
			if !reflect.DeepEqual(marker["reason_codes"], []string{test.wantReason}) {
				t.Fatalf("fidelity marker = %#v", marker)
			}
			result, err := draft.Finalize(FinalizeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ValidationIssues) != 0 ||
				result.Readiness.State != importreview.Blocked ||
				result.Readiness.ReplayReady {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestFinalizeReportsValidationAndBindingReadiness(t *testing.T) {
	t.Parallel()
	base := validBaseScript()
	base.Name = ""
	draft, err := Project(base, []RequestInput{{
		Request: Request{Target: "/"},
	}}, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := draft.Finalize(FinalizeOptions{UnresolvedBindings: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Readiness.State != importreview.Blocked ||
		result.Readiness.DraftValid ||
		result.Readiness.UnresolvedBindings != 2 ||
		!reflect.DeepEqual(result.Readiness.BlockingCodes, []string{"credential_bindings_required"}) {
		t.Fatalf("readiness = %#v", result.Readiness)
	}
	if len(result.ValidationIssues) == 0 || !strings.Contains(strings.Join(result.ValidationIssues, "\n"), "script name is required") {
		t.Fatalf("validation issues = %#v", result.ValidationIssues)
	}

	if _, err := draft.Finalize(FinalizeOptions{UnresolvedBindings: -1}); err == nil {
		t.Fatal("negative unresolved binding count unexpectedly accepted")
	}
	var nilDraft *Draft
	if _, err := nilDraft.Finalize(FinalizeOptions{}); err == nil {
		t.Fatal("nil draft unexpectedly accepted")
	}
}

func TestFinalizeScriptDisablesImplicitTraceparentAndPreservesExplicitChoice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		config   *bool
		expected bool
	}{
		{name: "unset becomes disabled", config: nil, expected: false},
		{name: "explicit enabled is preserved", config: boolPointer(true), expected: true},
		{name: "explicit disabled is preserved", config: boolPointer(false), expected: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := validBaseScript()
			script.Config.HTTP.Traceparent = test.config
			script.Actions = []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}
			result, err := FinalizeScript(script, nil, FinalizeOptions{})
			if err != nil {
				t.Fatalf("FinalizeScript: %v", err)
			}
			if result.Script.Config.HTTP.Traceparent == nil ||
				*result.Script.Config.HTTP.Traceparent != test.expected {
				t.Fatalf("traceparent setting was not normalized to %t", test.expected)
			}
		})
	}
}

func TestMergePreservedCredentialTargetsMetadataKeepsOccurrences(t *testing.T) {
	t.Parallel()
	first := credentialpolicy.TargetRef{
		RequestKey: "request-1",
		Field:      "body.json",
		Pointer:    "/password",
		Occurrence: 1,
	}
	second := credentialpolicy.TargetRef{
		RequestKey: "request-1",
		Field:      "body.xml.mixed_content",
		Pointer:    "/secret",
		Occurrence: 2,
	}
	metadata := map[string]any{
		dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
			map[string]any{
				"request_key": first.RequestKey,
				"field":       first.Field,
				"pointer":     first.Pointer,
				"occurrence":  float64(first.Occurrence),
			},
		},
	}
	merged := MergePreservedCredentialTargetsMetadata(
		metadata,
		[]credentialpolicy.TargetRef{first, second},
	)
	raw, ok := merged[dsl.ImportPreservedCredentialTargetsMetadataKey].([]credentialpolicy.TargetRef)
	if !ok {
		t.Fatalf("merged targets type = %T", merged[dsl.ImportPreservedCredentialTargetsMetadataKey])
	}
	if !reflect.DeepEqual(raw, []credentialpolicy.TargetRef{first, second}) {
		t.Fatalf("merged targets = %#v", raw)
	}
}

func TestAttachScriptPreservedCredentialTargetsMergesExistingTargets(t *testing.T) {
	t.Parallel()
	existing := credentialpolicy.TargetRef{
		RequestKey: "script-config",
		Field:      "config.http.proxy.url.query",
		Pointer:    "token",
		Occurrence: 1,
	}
	added := credentialpolicy.TargetRef{
		RequestKey: "script-base-url",
		Field:      "config.base_url.userinfo",
		Index:      0,
	}
	script := validBaseScript()
	script.Metadata = &dsl.ScriptMetadata{
		Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{existing},
			},
		},
	}

	attachScriptPreservedCredentialTargets(script, []credentialpolicy.TargetRef{existing, added})

	targets := preservedCredentialTargetsFromMetadata(script.Metadata.Import.Preserved)
	if !reflect.DeepEqual(targets, []credentialpolicy.TargetRef{existing, added}) {
		t.Fatalf("script targets = %#v", targets)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func validBaseScript() *dsl.Script {
	return &dsl.Script{
		Name:     "Imported requests",
		Version:  "1.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
		},
	}
}
