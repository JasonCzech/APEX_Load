package importreview

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// JMeter copies an inherited header manager into config.http.default_headers
// and into each sampler's own headers. A review gate on the sampler hides its
// copies from discovery, so the one binding offered is the default header. The
// action copy replaces the default on the wire, so binding the default alone
// would leave the recorded value as the one sent.
func TestPreservedDefaultHeaderBindingRebindsShadowingActionCopies(t *testing.T) {
	const recorded = "synthetic-client-secret-7f3a"
	script := &dsl.Script{
		Name: "Split", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test", HTTP: dsl.HTTPConfig{
			DefaultHeaders: map[string]string{"client_secret": recorded, "client_id": "public-client"},
		}},
		Actions: []dsl.Action{{
			Name: "BALANCE", Type: "request", Method: "POST", Target: "/balance",
			Headers: map[string]string{"client_secret": recorded, "Content-Type": "application/json"},
			HeaderList: dsl.HeaderList{
				{Name: "Content-Type", Value: "application/json"},
				{Name: "Client_Secret", Value: recorded},
			},
			Body:     `{"amount":1}`,
			Metadata: map[string]any{"execution_blocker": "import_review_required", "import_review": map[string]any{"reason_codes": []any{"jmeter_timestamp_timezone_required"}}},
		}, {
			Name: "OTHER", Type: "request", Method: "GET", Target: "/other",
			Headers: map[string]string{"client_secret": "a-different-value"},
		}},
	}
	EnsureCredentialPreservingImportMetadata(script, "jmeter")
	requirements := DiscoverSecretBindingRequirements(MaskScriptSecrets(script), 0)
	var defaultHeader *SecretBindingRequirement
	for index := range requirements {
		if requirements[index].Path == "/config/http/default_headers/client_secret" {
			defaultHeader = &requirements[index]
		}
	}
	if defaultHeader == nil {
		t.Fatalf("no default-header requirement in %#v", requirements)
	}
	bound, _, err := ApplyCredentialPreservingSecretBindings(script, 0, []SecretBindingSelection{{RequirementID: defaultHeader.ID, SecretName: "HTTP_CLIENT_SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	const ref = "{{secret.HTTP_CLIENT_SECRET}}"
	action := bound.Actions[0]
	if bound.Config.HTTP.DefaultHeaders["client_secret"] != ref || action.Headers["client_secret"] != ref || action.HeaderList[1].Value != ref {
		t.Fatalf("binding left a shadowing copy: default=%q headers=%q list=%q",
			bound.Config.HTTP.DefaultHeaders["client_secret"], action.Headers["client_secret"], action.HeaderList[1].Value)
	}
	effective, _ := httpcontract.EffectiveAuthoredHeaderList(bound.Config.HTTP.DefaultHeaders, protocolHeaders(bound.Config.HTTP.DefaultHeaderList), action.Headers, protocolHeaders(action.HeaderList))
	for _, header := range effective {
		if strings.EqualFold(header.Name, "client_secret") && header.Value != ref {
			t.Fatalf("effective header sent %q, want the bound reference", header.Value)
		}
	}
	if bound.Actions[1].Headers["client_secret"] != "a-different-value" {
		t.Fatal("a header the action sets to its own value was rebound")
	}
	if action.Headers["Content-Type"] != "application/json" || bound.Config.HTTP.DefaultHeaders["client_id"] != "public-client" {
		t.Fatal("binding changed unrelated headers")
	}
	if script.Actions[0].Headers["client_secret"] != recorded {
		t.Fatal("binding changed the caller's script")
	}
}

func protocolHeaders(list dsl.HeaderList) protocol.HeaderList {
	out := make(protocol.HeaderList, 0, len(list))
	for _, entry := range list {
		out = append(out, protocol.HeaderEntry{Name: entry.Name, Value: entry.Value})
	}
	return out
}

func TestHeaderListAndHeadersParityAndShortenedActionPrefix(t *testing.T) {
	const longActionName = "Start_iPadTest_LrgeBANs_ViewServProm_T08_ClkBackToAccInfo||aura_823_0_s_2_1754691916807_1_ACTION$getRecord_transaction"
	const recordedAuth = "Bearer secret-bearer-token-123456"
	script := &dsl.Script{
		Name: "AuraScript", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.lightning.force.com",
			HTTP: dsl.HTTPConfig{
				DefaultHeaders: map[string]string{"X-Api-Key": "default-api-key-value-123456"},
				DefaultHeaderList: dsl.HeaderList{
					{Name: "X-Api-Key", Value: "default-api-key-value-123456"},
				},
			},
		},
		Actions: []dsl.Action{{
			Name:    longActionName,
			Type:    "request",
			Method:  "POST",
			Target:  "/aura",
			Headers: map[string]string{"Authorization": recordedAuth},
			HeaderList: dsl.HeaderList{
				{Name: "Authorization", Value: recordedAuth},
			},
			Metadata: map[string]any{"execution_blocker": "import_credentials_unmanaged"},
		}},
	}
	EnsureCredentialPreservingImportMetadata(script, "enterprise_script_archive")
	projected := MaskScriptSecrets(script)
	requirements := DiscoverSecretBindingRequirements(projected, 0)

	byPath := make(map[string]SecretBindingRequirement, len(requirements))
	for _, req := range requirements {
		byPath[req.Path] = req
		if len(req.SuggestedName) >= 40 {
			t.Fatalf("path %s suggested_name %q is not < 40 chars (len=%d)", req.Path, req.SuggestedName, len(req.SuggestedName))
		}
		if strings.HasSuffix(req.SuggestedName, "_VALUE") {
			t.Fatalf("path %s suggested_name %q used generic VALUE suffix instead of header name", req.Path, req.SuggestedName)
		}
	}

	listReq, ok1 := byPath["/actions/0/header_list/0/value"]
	mapReq, ok2 := byPath["/actions/0/headers/Authorization"]
	if !ok1 || !ok2 {
		t.Fatalf("missing action header requirements in %#v", byPath)
	}
	if listReq.SuggestedName != "START_IPADTEST_LRGEBANS_TOKEN" || mapReq.SuggestedName != "START_IPADTEST_LRGEBANS_TOKEN" {
		t.Fatalf("action header suggested names = (%q, %q), want both START_IPADTEST_LRGEBANS_TOKEN", listReq.SuggestedName, mapReq.SuggestedName)
	}

	defListReq, ok3 := byPath["/config/http/default_header_list/0/value"]
	defMapReq, ok4 := byPath["/config/http/default_headers/X-Api-Key"]
	if !ok3 || !ok4 {
		t.Fatalf("missing default header requirements in %#v", byPath)
	}
	if defListReq.SuggestedName != "HTTP_X_API_KEY" || defMapReq.SuggestedName != "HTTP_X_API_KEY" {
		t.Fatalf("default header suggested names = (%q, %q), want both HTTP_X_API_KEY", defListReq.SuggestedName, defMapReq.SuggestedName)
	}

	// Binding only one side of each pair in ApplyCredentialPreservingSecretBindings
	// must rewrite the exact-value twin on the same scope so EffectiveAuthoredHeaderList
	// never sends an unbound mirror copy.
	bound, remaining, err := ApplyCredentialPreservingSecretBindings(script, 0, []SecretBindingSelection{
		{RequirementID: mapReq.ID, SecretName: listReq.SuggestedName},
		{RequirementID: defListReq.ID, SecretName: defListReq.SuggestedName},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining requirements after binding one side of each pair, got %#v", remaining)
	}
	const wantAuthRef = "Bearer {{secret.START_IPADTEST_LRGEBANS_TOKEN}}"
	const wantKeyRef = "{{secret.HTTP_X_API_KEY}}"
	if bound.Actions[0].Headers["Authorization"] != wantAuthRef || bound.Actions[0].HeaderList[0].Value != wantAuthRef {
		t.Fatalf("action header pair not synchronized: headers=%q header_list=%q", bound.Actions[0].Headers["Authorization"], bound.Actions[0].HeaderList[0].Value)
	}
	if bound.Config.HTTP.DefaultHeaders["X-Api-Key"] != wantKeyRef || bound.Config.HTTP.DefaultHeaderList[0].Value != wantKeyRef {
		t.Fatalf("default header pair not synchronized: default_headers=%q default_header_list=%q", bound.Config.HTTP.DefaultHeaders["X-Api-Key"], bound.Config.HTTP.DefaultHeaderList[0].Value)
	}
	if blocker := bound.Actions[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("expected import_credentials_unmanaged blocker cleared, got %v", blocker)
	}
	effective, _ := httpcontract.EffectiveAuthoredHeaderList(
		bound.Config.HTTP.DefaultHeaders,
		protocolHeaders(bound.Config.HTTP.DefaultHeaderList),
		bound.Actions[0].Headers,
		protocolHeaders(bound.Actions[0].HeaderList),
	)
	for _, header := range effective {
		if strings.EqualFold(header.Name, "Authorization") && header.Value != wantAuthRef {
			t.Fatalf("effective Authorization header = %q, want %q", header.Value, wantAuthRef)
		}
		if strings.EqualFold(header.Name, "X-Api-Key") && header.Value != wantKeyRef {
			t.Fatalf("effective X-Api-Key header = %q, want %q", header.Value, wantKeyRef)
		}
	}

	// Verify ApplyRedactedSecretBindings also synchronizes a Cookie header_list + headers pair.
	cookieScript := &dsl.Script{
		Name: "AuraCookieScript", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.lightning.force.com"},
		Actions: []dsl.Action{{
			Name:    longActionName,
			Type:    "request",
			Method:  "POST",
			Target:  "/aura",
			Headers: map[string]string{"Cookie": MaskedScriptSecret},
			HeaderList: dsl.HeaderList{
				{Name: "Cookie", Value: MaskedScriptSecret},
			},
		}},
	}
	cookieReqs := DiscoverSecretBindingRequirements(cookieScript, 0)
	if len(cookieReqs) != 2 || cookieReqs[0].SuggestedName != "START_IPADTEST_LRGEBANS_COOKIE" || cookieReqs[1].SuggestedName != "START_IPADTEST_LRGEBANS_COOKIE" {
		t.Fatalf("cookie requirements = %#v, want both START_IPADTEST_LRGEBANS_COOKIE", cookieReqs)
	}
	redactedBound, redactedRemaining, err := ApplyRedactedSecretBindings(cookieScript, 0, []SecretBindingSelection{
		{RequirementID: cookieReqs[0].ID, SecretName: cookieReqs[0].SuggestedName},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(redactedRemaining) != 0 {
		t.Fatalf("expected 0 remaining requirements from ApplyRedactedSecretBindings, got %#v", redactedRemaining)
	}
	const wantCookieRef = "{{secret.START_IPADTEST_LRGEBANS_COOKIE}}"
	if redactedBound.Actions[0].Headers["Cookie"] != wantCookieRef || redactedBound.Actions[0].HeaderList[0].Value != wantCookieRef {
		t.Fatalf("redacted Cookie header pair not synchronized: headers=%q header_list=%q", redactedBound.Actions[0].Headers["Cookie"], redactedBound.Actions[0].HeaderList[0].Value)
	}

	// Verify split multi-occurrence bindings across header_list (occurrence 0) and
	// headers (occurrence 1) merge occurrences across both twins.
	multiCookieScript := &dsl.Script{
		Name: "MultiCookieScript", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.lightning.force.com"},
		Actions: []dsl.Action{{
			Name:    "CookieRequest",
			Type:    "request",
			Method:  "POST",
			Target:  "/aura",
			Headers: map[string]string{"Cookie": "sid=" + MaskedScriptSecret + "; csrf=" + MaskedScriptSecret},
			HeaderList: dsl.HeaderList{
				{Name: "Cookie", Value: "sid=" + MaskedScriptSecret + "; csrf=" + MaskedScriptSecret},
			},
		}},
	}
	multiReqs := DiscoverSecretBindingRequirements(multiCookieScript, 0)
	var listOcc0ID, mapOcc1ID string
	for _, req := range multiReqs {
		if req.Path == "/actions/0/header_list/0/value" && req.Occurrence == 0 {
			listOcc0ID = req.ID
		}
		if req.Path == "/actions/0/headers/Cookie" && req.Occurrence == 1 {
			mapOcc1ID = req.ID
		}
	}
	if listOcc0ID == "" || mapOcc1ID == "" {
		t.Fatalf("missing multi-occurrence cookie requirements in %#v", multiReqs)
	}
	multiBound, multiRemaining, err := ApplyRedactedSecretBindings(multiCookieScript, 0, []SecretBindingSelection{
		{RequirementID: listOcc0ID, SecretName: "COOKIE_SID"},
		{RequirementID: mapOcc1ID, SecretName: "COOKIE_CSRF"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(multiRemaining) != 0 {
		t.Fatalf("expected 0 remaining requirements for split multi-occurrence bindings, got %#v", multiRemaining)
	}
	const wantMultiCookie = "sid={{secret.COOKIE_SID}}; csrf={{secret.COOKIE_CSRF}}"
	if multiBound.Actions[0].Headers["Cookie"] != wantMultiCookie || multiBound.Actions[0].HeaderList[0].Value != wantMultiCookie {
		t.Fatalf("split multi-occurrence Cookie pair not synchronized: headers=%q header_list=%q", multiBound.Actions[0].Headers["Cookie"], multiBound.Actions[0].HeaderList[0].Value)
	}

	preservedMultiHeader := &dsl.Script{
		Name: "PreservedMultiHeaderScript", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.lightning.force.com"},
		Actions: []dsl.Action{{
			Name:    "ContextRequest",
			Type:    "request",
			Method:  "POST",
			Target:  "/aura",
			Headers: map[string]string{"X-Custom-Context": "Bearer sk_live_51H8abc123def456ghi789jkl012, Bearer sk_live_99Z8abc123def456ghi789jkl999"},
			HeaderList: dsl.HeaderList{
				{Name: "X-Custom-Context", Value: "Bearer sk_live_51H8abc123def456ghi789jkl012, Bearer sk_live_99Z8abc123def456ghi789jkl999"},
			},
		}},
	}
	EnsureCredentialPreservingImportMetadata(preservedMultiHeader, "enterprise_script_archive")
	preservedMultiReqs := DiscoverSecretBindingRequirements(MaskScriptSecrets(preservedMultiHeader), 0)
	var pListOcc0ID, pMapOcc1ID string
	for _, req := range preservedMultiReqs {
		if req.Path == "/actions/0/header_list/0/value" && req.Occurrence == 0 {
			pListOcc0ID = req.ID
		}
		if req.Path == "/actions/0/headers/X-Custom-Context" && req.Occurrence == 1 {
			pMapOcc1ID = req.ID
		}
	}
	if pListOcc0ID == "" || pMapOcc1ID == "" {
		t.Fatalf("missing preserved multi-occurrence header requirements in %#v", preservedMultiReqs)
	}
	pBound, pRemaining, err := ApplyCredentialPreservingSecretBindings(preservedMultiHeader, 0, []SecretBindingSelection{
		{RequirementID: pListOcc0ID, SecretName: "COOKIE_SID"},
		{RequirementID: pMapOcc1ID, SecretName: "COOKIE_CSRF"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pRemaining) != 0 {
		t.Fatalf("expected 0 remaining requirements for preserved split multi-occurrence bindings, got %#v", pRemaining)
	}
	const wantPreservedMulti = "Bearer {{secret.COOKIE_SID}}, Bearer {{secret.COOKIE_CSRF}}"
	if pBound.Actions[0].Headers["X-Custom-Context"] != wantPreservedMulti || pBound.Actions[0].HeaderList[0].Value != wantPreservedMulti {
		t.Fatalf("preserved split multi-occurrence header pair not synchronized: headers=%q header_list=%q", pBound.Actions[0].Headers["X-Custom-Context"], pBound.Actions[0].HeaderList[0].Value)
	}
}
