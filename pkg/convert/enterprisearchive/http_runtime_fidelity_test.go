package enterprisearchive

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertZipHTTP10ProducesValidDurablyBlockedDraft(t *testing.T) {
	t.Parallel()
	archive := buildSourceZip(t, map[string]string{
		"HTTP10.usr": `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("HTTP 1.0 request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[HTTP]
HTTPVersion=HTTP/1.0
`,
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 {
		t.Fatalf("script/actions = %#v", result.Script)
	}
	if result.Script.Config.HTTP.Version != "" {
		t.Fatalf("config.http.version = %q, want inert auto projection", result.Script.Config.HTTP.Version)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("action metadata = %#v", action.Metadata)
	}
	versionMarker, _ := action.Metadata["import_http_version_fidelity"].(map[string]any)
	if versionMarker["reason_code"] != "unsupported_http_version" ||
		!reflect.DeepEqual(versionMarker["source_refs"], []string{"default.cfg#HTTPVersion"}) {
		t.Fatalf("version marker = %#v", versionMarker)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(review["reason_codes"], []string{"unsupported_http_version"}) {
		t.Fatalf("import review = %#v", review)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}

	parity := enterpriseRuntimeParity(t, result.Script)
	entry := runtimeParityEntryByID(t, parity, "http_transport")
	if entry.Status != RuntimeSettingStatusBlocked ||
		!reflect.DeepEqual(entry.RuntimeFiles, []string{"default.cfg"}) {
		t.Fatalf("http transport parity = %#v", entry)
	}
	if result.SourceReview == nil {
		t.Fatal("missing source review")
	}
	foundMapping := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.SourceFile == "default.cfg" &&
			strings.Contains(mapping.TargetPath, ".metadata.import_http_version_fidelity") &&
			mapping.Status == "review_only" {
			foundMapping = true
			break
		}
	}
	if !foundMapping {
		t.Fatalf("source mappings = %#v", result.SourceReview.Mappings)
	}
}

func TestDirectConvertCannotEmitUnsupportedHTTPVersion(t *testing.T) {
	t.Parallel()
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		HTTPConfig: &dsl.HTTPConfig{Version: "HTTP/1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script.Config.HTTP.Version != "" ||
		len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("script = %#v", result.Script)
	}
	marker, _ := result.Script.Actions[0].Metadata["import_http_version_fidelity"].(map[string]any)
	if marker["reason_code"] != "unsupported_http_version" {
		t.Fatalf("version marker = %#v", marker)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestWebRESTHeaderOccurrencesSurviveTransactionCompactionAndGateSingletonDuplicates(t *testing.T) {
	t.Parallel()
	parsed, err := ParseActionFile("Action.c", `Action()
{
	lr_start_transaction("ordered headers");
	web_rest("first",
		"URL=https://example.test/one",
		"Method=GET",
		HEADERS,
		"Name=X-Trace", "Value=first", ENDHEADER,
		"Name=x-trace", "Value=second", ENDHEADER,
		"Name=Host", "Value=one.example.test", ENDHEADER,
		"Name=Host", "Value=two.example.test", ENDHEADER,
		LAST);
	web_rest("second",
		"URL=https://example.test/two",
		"Method=GET",
		LAST);
	lr_end_transaction("ordered headers", LR_AUTO);
	return 0;
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var parsedREST *ParsedFunction
	for index := range parsed.Functions {
		if parsed.Functions[index].Name == "web_rest" {
			parsedREST = &parsed.Functions[index]
			break
		}
	}
	if parsedREST == nil {
		t.Fatal("parsed web_rest function missing")
	}
	wantParsed := dsl.HeaderList{
		{Name: "X-Trace", Value: "first"},
		{Name: "x-trace", Value: "second"},
		{Name: "Host", Value: "one.example.test"},
		{Name: "Host", Value: "two.example.test"},
	}
	if !reflect.DeepEqual(parsedREST.HeaderList, wantParsed) {
		t.Fatalf("parsed header_list = %#v, want %#v", parsedREST.HeaderList, wantParsed)
	}
	if !reflect.DeepEqual(parsedREST.Headers, map[string]string{
		"x-trace": "second",
		"Host":    "two.example.test",
	}) {
		t.Fatalf("parsed compatibility headers = %#v", parsedREST.Headers)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sequence *dsl.Action
	for index := range result.Script.Actions {
		if result.Script.Actions[index].Type == "http_sequence" {
			sequence = &result.Script.Actions[index]
			break
		}
	}
	if sequence == nil {
		t.Fatalf("actions = %#v, want compacted HTTP sequence", result.Script.Actions)
	}
	requests, ok := sequence.Params["requests"].([]map[string]any)
	if !ok || len(requests) != 2 {
		t.Fatalf("sequence requests = %#v", sequence.Params["requests"])
	}
	first := requests[0]
	list := enterpriseSequenceHeaderListForTest(t, first["header_list"])
	if len(list) != 3 ||
		list[0] != (dsl.HeaderEntry{Name: "X-Trace", Value: "first"}) ||
		list[1] != (dsl.HeaderEntry{Name: "x-trace", Value: "second"}) ||
		list[2] != (dsl.HeaderEntry{Name: "Host", Value: "one.example.test"}) {
		t.Fatalf("reviewed sequence header_list = %#v", list)
	}
	headers, _ := first["headers"].(map[string]string)
	if !reflect.DeepEqual(headers, map[string]string{
		"x-trace": "second",
		"Host":    "one.example.test",
	}) {
		t.Fatalf("reviewed compatibility headers = %#v", first["headers"])
	}
	metadata, _ := first["metadata"].(map[string]any)
	if metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("duplicate singleton header escaped review gate: %#v", metadata)
	}
	review, _ := metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(review["reason_codes"], []string{"http_header_duplicate_singleton_header"}) {
		t.Fatalf("duplicate singleton review = %#v", review)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestConvertZipSupportedSourceHTTPVersionConflictIsDurablyBlocked(t *testing.T) {
	t.Parallel()
	archive := buildSourceZip(t, map[string]string{
		"VersionConflict.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[HTTP]
HTTPVersion=HTTP/2
`,
	})
	result, err := ConvertZip(archive, ConvertOptions{
		HTTPConfig: &dsl.HTTPConfig{Version: "1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script.Config.HTTP.Version != "1.1" {
		t.Fatalf("config.http.version = %q, want caller-selected 1.1 retained", result.Script.Config.HTTP.Version)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("action metadata = %#v", action.Metadata)
	}
	marker, _ := action.Metadata["import_http_version_fidelity"].(map[string]any)
	if marker["reason_code"] != "http_version_not_preserved" ||
		!reflect.DeepEqual(marker["source_refs"], []string{"default.cfg#HTTPVersion"}) {
		t.Fatalf("version marker = %#v", marker)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(review["reason_codes"], []string{"http_version_not_preserved"}) {
		t.Fatalf("import review = %#v", review)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestConvertZipRuntimeCredentialsAreExecutableAndProvenanceIsExact(t *testing.T) {
	t.Parallel()
	const (
		proxyUser = "proxy-user-canary"
		proxyPass = "proxy-pass-canary"
		authPass  = "auth-pass-canary"
		apiKey    = "attribute-key-canary"
	)
	archive := buildSourceZip(t, map[string]string{
		"RuntimeCredentials.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[Proxy]
Mode=custom
ProxyServer=http://proxy.example.test:8080
ProxyUserName=` + proxyUser + `
ProxyPassword=` + proxyPass + `
[Authentication]
UserName=auth-user
Password=` + authPass + `
[Additional Attributes]
api_key=` + apiKey + `
`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	config := result.Script.Config
	for field, values := range map[string][2]string{
		"proxy username": {config.HTTP.Proxy.Username, proxyUser},
		"proxy password": {config.HTTP.Proxy.Password, proxyPass},
		"auth password":  {config.HTTP.Auth.Password, authPass},
		"api key":        {config.WebRuntime.AdditionalAttributes["api_key"], apiKey},
	} {
		if values[0] != values[1] {
			t.Errorf("%s = %q, want imported executable bytes %q", field, values[0], values[1])
		}
	}
	runtimeFiles := result.Script.Metadata.Import.Preserved["runtime_files"]
	runtimeJSON, err := json.Marshal(runtimeFiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{proxyUser, proxyPass, authPass, apiKey} {
		if !strings.Contains(string(runtimeJSON), canary) {
			t.Fatalf("preserved runtime metadata lost %q: %s", canary, runtimeJSON)
		}
	}
	assertEnterpriseCredentialProvenancePreserved(
		t,
		result,
		[]string{proxyUser, proxyPass, authPass, apiKey},
	)

	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, want no re-entry requirements for retained runtime credentials", requirements)
	}
	if result.ProtectedRequirements.Take() != nil {
		t.Fatal("protected requirements handoff was not one-shot")
	}
}

func TestConvertZipEmbeddedProxyUserinfoIsExecutableAndPreservedInProvenance(t *testing.T) {
	t.Parallel()
	const (
		proxyUser = "embedded-proxy-user-canary"
		proxyPass = "embedded-proxy-pass-canary"
	)
	archive := buildSourceZip(t, map[string]string{
		"EmbeddedProxyCredentials.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[Proxy]
Mode=custom
ProxyServer=http://` + proxyUser + `:` + proxyPass + `@proxy.example.test:8080
`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script.Config.HTTP.Proxy.URL != "http://proxy.example.test:8080" {
		t.Fatalf("proxy URL = %q, want URL without userinfo", result.Script.Config.HTTP.Proxy.URL)
	}
	if result.Script.Config.HTTP.Proxy.Username != proxyUser ||
		result.Script.Config.HTTP.Proxy.Password != proxyPass {
		t.Fatalf("proxy config = %#v, want imported executable username/password", result.Script.Config.HTTP.Proxy)
	}
	runtimeJSON, err := json.Marshal(result.Script.Metadata.Import.Preserved["runtime_files"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runtimeJSON), proxyUser) ||
		!strings.Contains(string(runtimeJSON), proxyPass) {
		t.Fatalf("preserved runtime proxy metadata lost userinfo: %s", runtimeJSON)
	}
	assertEnterpriseCredentialProvenancePreserved(t, result, []string{proxyUser, proxyPass})

	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, want no re-entry requirements for retained proxy credentials", requirements)
	}
}

func TestConvertZipProxyURLQueryCredentialIsExecutableAndRemovedFromProvenance(t *testing.T) {
	t.Parallel()
	const proxyToken = "proxy-query-token-canary-3f71"
	archive := buildSourceZip(t, map[string]string{
		"ProxyQueryCredential.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[Proxy]
Mode=custom
ProxyServer=http://proxy.example.test:8080?region=east&token=` + proxyToken + `
`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Config.HTTP.Proxy.URL; got !=
		"http://proxy.example.test:8080?region=east&token="+proxyToken {
		t.Fatalf("proxy URL = %q, want imported ordered query bytes", got)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved[dsl.ImportPreservedCredentialPolicyKey] !=
		dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("enterprise credential preservation policy = %#v", preserved)
	}
	targetsJSON, err := json.Marshal(
		preserved[dsl.ImportPreservedCredentialTargetsMetadataKey],
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(targetsJSON), "config.http.proxy.url.query") {
		t.Fatalf("proxy query credential locator was discarded: %s", targetsJSON)
	}
	assertEnterpriseCredentialProvenancePreserved(t, result, []string{proxyToken})
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, want no re-entry requirement for retained proxy query credential", requirements)
	}
}

func TestRuntimeConfigToHTTPConfigNeverReturnsEmbeddedProxyUserinfo(t *testing.T) {
	t.Parallel()
	const (
		proxyUser = "direct-proxy-user-canary"
		proxyPass = "direct-proxy-pass-canary"
	)
	config := (&RuntimeConfig{
		ProxyMode: "custom",
		ProxyURL:  "http://" + proxyUser + ":" + proxyPass + "@proxy.example.test:8080",
	}).ToHTTPConfig()
	if config.Proxy.URL != "http://proxy.example.test:8080" ||
		config.Proxy.Username != "{{secret.ENTERPRISE_PROXY_USERNAME}}" ||
		config.Proxy.Password != "{{secret.ENTERPRISE_PROXY_PASSWORD}}" {
		t.Fatalf("direct HTTP config retained unsafe proxy fields: %#v", config.Proxy)
	}
	serialized, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), proxyUser) || strings.Contains(string(serialized), proxyPass) {
		t.Fatalf("direct HTTP config disclosed proxy userinfo: %s", serialized)
	}
}

func enterpriseSequenceHeaderListForTest(t *testing.T, value any) dsl.HeaderList {
	t.Helper()
	switch typed := value.(type) {
	case dsl.HeaderList:
		return append(dsl.HeaderList(nil), typed...)
	case []map[string]any:
		out := make(dsl.HeaderList, 0, len(typed))
		for _, entry := range typed {
			name, _ := entry["name"].(string)
			value, _ := entry["value"].(string)
			out = append(out, dsl.HeaderEntry{
				Name:  name,
				Value: value,
			})
		}
		return out
	default:
		t.Fatalf("header_list = %#v (%T)", value, value)
		return nil
	}
}

func enterpriseRuntimeParity(t *testing.T, script *dsl.Script) []RuntimeSettingParityEntry {
	t.Helper()
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatalf("missing import metadata: %#v", script)
	}
	raw := script.Metadata.Import.Preserved["runtime_settings_parity"]
	switch parity := raw.(type) {
	case []RuntimeSettingParityEntry:
		return parity
	case []any:
		out := make([]RuntimeSettingParityEntry, 0, len(parity))
		for _, value := range parity {
			entry, ok := value.(RuntimeSettingParityEntry)
			if ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		t.Fatalf("runtime parity = %#v", raw)
		return nil
	}
}
