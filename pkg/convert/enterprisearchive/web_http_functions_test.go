package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// publishedWebHTTPFunctionExpectations pins the disposition of every function in
// the published OpenText/Micro Focus VuGen 26.1 Web (HTTP/HTML) library. It is the
// classification guard for the conversion engine: any change to a function's
// family or support mode must be a deliberate edit here AND in the embedded
// registry, so coverage can never silently regress to a custom_call review stub.
var publishedWebHTTPFunctionExpectations = map[string]struct {
	family string
	mode   SourceFunctionSupportMode
}{
	"web_add_auto_filter":            {"web", SourceFunctionSupportNativeNoop},
	"web_add_auto_header":            {"web", SourceFunctionSupportExecutable},
	"web_add_cookie":                 {"web", SourceFunctionSupportExecutable},
	"web_add_cookie_ex":              {"web", SourceFunctionSupportExecutable},
	"web_add_filter":                 {"web", SourceFunctionSupportNativeNoop},
	"web_add_header":                 {"web", SourceFunctionSupportExecutable},
	"web_cache_cleanup":              {"web", SourceFunctionSupportNativeNoop},
	"web_cleanup_auto_headers":       {"web", SourceFunctionSupportExecutable},
	"web_cleanup_cookies":            {"web", SourceFunctionSupportExecutable},
	"web_concurrent_end":             {"web", SourceFunctionSupportNativeNoop},
	"web_concurrent_start":           {"web", SourceFunctionSupportNativeNoop},
	"web_convert_date_param":         {"parameter", SourceFunctionSupportExecutable},
	"web_convert_from_formatted":     {"parameter", SourceFunctionSupportExecutable},
	"web_convert_param":              {"parameter", SourceFunctionSupportExecutable},
	"web_convert_to_formatted":       {"parameter", SourceFunctionSupportExecutable},
	"web_custom_request":             {"web", SourceFunctionSupportExecutable},
	"web_disable_keep_alive":         {"web", SourceFunctionSupportExecutable},
	"web_dump_cache":                 {"web", SourceFunctionSupportNativeNoop},
	"web_enable_keep_alive":          {"web", SourceFunctionSupportExecutable},
	"web_find":                       {"web", SourceFunctionSupportExecutable},
	"web_get_int_property":           {"parameter", SourceFunctionSupportExecutable},
	"web_global_verification":        {"web", SourceFunctionSupportExecutable},
	"web_global_verification_pause":  {"web", SourceFunctionSupportReviewOnly},
	"web_global_verification_resume": {"web", SourceFunctionSupportReviewOnly},
	"web_image":                      {"web", SourceFunctionSupportExecutable},
	"web_image_check":                {"web", SourceFunctionSupportExecutable},
	"web_js_reset":                   {"web", SourceFunctionSupportReviewOnly},
	"web_js_run":                     {"web", SourceFunctionSupportReviewOnly},
	"web_link":                       {"web", SourceFunctionSupportExecutable},
	"web_load_cache":                 {"web", SourceFunctionSupportNativeNoop},
	"web_reg_add_cookie":             {"web", SourceFunctionSupportExecutable},
	"web_reg_async_attributes":       {"web", SourceFunctionSupportReviewOnly},
	"web_reg_find":                   {"web", SourceFunctionSupportExecutable},
	"web_reg_save_param":             {"web", SourceFunctionSupportExecutable},
	"web_reg_save_param_attrib":      {"web", SourceFunctionSupportExecutable},
	"web_reg_save_param_ex":          {"web", SourceFunctionSupportExecutable},
	"web_reg_save_param_json":        {"web", SourceFunctionSupportExecutable},
	"web_reg_save_param_regexp":      {"web", SourceFunctionSupportExecutable},
	"web_reg_save_param_xpath":       {"web", SourceFunctionSupportExecutable},
	"web_remove_auto_filter":         {"web", SourceFunctionSupportNativeNoop},
	"web_remove_auto_header":         {"web", SourceFunctionSupportExecutable},
	"web_remove_cookie":              {"web", SourceFunctionSupportExecutable},
	"web_report_data_point":          {"metric", SourceFunctionSupportExecutable},
	"web_rest":                       {"web", SourceFunctionSupportExecutable},
	"web_revert_auto_header":         {"web", SourceFunctionSupportExecutable},
	"web_save_header":                {"web", SourceFunctionSupportReviewOnly},
	"web_save_param_length":          {"parameter", SourceFunctionSupportExecutable},
	"web_save_timestamp_param":       {"parameter", SourceFunctionSupportExecutable},
	"web_set_certificate":            {"web", SourceFunctionSupportReviewOnly},
	"web_set_certificate_ex":         {"web", SourceFunctionSupportExecutable},
	"web_set_max_html_param_len":     {"web", SourceFunctionSupportNativeNoop},
	"web_set_max_retries":            {"web", SourceFunctionSupportExecutable},
	"web_set_option":                 {"web", SourceFunctionSupportExecutable},
	"web_set_pac":                    {"web", SourceFunctionSupportReviewOnly},
	"web_set_proxy":                  {"web", SourceFunctionSupportExecutable},
	"web_set_proxy_bypass":           {"web", SourceFunctionSupportExecutable},
	"web_set_proxy_bypass_local":     {"web", SourceFunctionSupportExecutable},
	"web_set_secure_proxy":           {"web", SourceFunctionSupportExecutable},
	"web_set_sockets_option":         {"web", SourceFunctionSupportExecutable},
	"web_set_timeout":                {"web", SourceFunctionSupportExecutable},
	"web_set_user":                   {"web", SourceFunctionSupportExecutable},
	"web_sjis_to_euc_param":          {"parameter", SourceFunctionSupportExecutable},
	"web_stop_async":                 {"web", SourceFunctionSupportReviewOnly},
	"web_stream_close":               {"web", SourceFunctionSupportReviewOnly},
	"web_stream_get_param_double":    {"web", SourceFunctionSupportReviewOnly},
	"web_stream_get_param_int":       {"web", SourceFunctionSupportReviewOnly},
	"web_stream_get_param_string":    {"web", SourceFunctionSupportReviewOnly},
	"web_stream_open":                {"web", SourceFunctionSupportReviewOnly},
	"web_stream_pause":               {"web", SourceFunctionSupportReviewOnly},
	"web_stream_play":                {"web", SourceFunctionSupportReviewOnly},
	"web_stream_seek":                {"web", SourceFunctionSupportReviewOnly},
	"web_stream_set_param_double":    {"web", SourceFunctionSupportReviewOnly},
	"web_stream_set_param_int":       {"web", SourceFunctionSupportReviewOnly},
	"web_stream_stop":                {"web", SourceFunctionSupportReviewOnly},
	"web_stream_wait":                {"web", SourceFunctionSupportReviewOnly},
	"web_submit_data":                {"web", SourceFunctionSupportExecutable},
	"web_submit_form":                {"web", SourceFunctionSupportExecutable},
	"web_sync":                       {"web", SourceFunctionSupportReviewOnly},
	"web_url":                        {"web", SourceFunctionSupportExecutable},
	"web_util_set_request_body":      {"parameter", SourceFunctionSupportExecutable},
	"web_util_set_request_url":       {"parameter", SourceFunctionSupportExecutable},
}

// TestWebHTTPFunctionRegistryMatchesPublishedLibrary pins the embedded registry to
// the published library: the set of names, families, and support modes must match
// the expectations table exactly (no missing, extra, or reclassified function).
func TestWebHTTPFunctionRegistryMatchesPublishedLibrary(t *testing.T) {
	defs := allWebHTTPFunctionDefinitions()
	if len(defs) != len(publishedWebHTTPFunctionExpectations) {
		t.Fatalf("web registry has %d functions, want %d", len(defs), len(publishedWebHTTPFunctionExpectations))
	}
	seen := make(map[string]bool, len(defs))
	for _, def := range defs {
		seen[def.Name] = true
		want, ok := publishedWebHTTPFunctionExpectations[def.Name]
		if !ok {
			t.Errorf("registry function %q is not in the published expectations table", def.Name)
			continue
		}
		if def.Family != want.family || def.SupportMode != want.mode {
			t.Errorf("function %q classified as (%s,%s), want (%s,%s)", def.Name, def.Family, def.SupportMode, want.family, want.mode)
		}
	}
	for name := range publishedWebHTTPFunctionExpectations {
		if !seen[name] {
			t.Errorf("expected published function %q missing from the embedded registry", name)
		}
	}
}

// sampleWebFunctionCall renders a syntactically valid VuGen call for a published
// Web (HTTP/HTML) function, with enough arguments to exercise the disposition the
// registry declares for it.
func sampleWebFunctionCall(name string) string {
	switch name {
	case "web_url":
		return `web_url("Home", "URL=https://example.test/home", LAST);`
	case "web_link":
		return `web_link("Catalog", "Text=Catalog", "URL=https://example.test/catalog", LAST);`
	case "web_image":
		return `web_image("Logo", "Alt=Logo", "Src=https://example.test/logo.png", LAST);`
	case "web_image_check":
		return `web_image_check("LogoCheck", "Src=https://example.test/logo.png", LAST);`
	case "web_submit_data":
		return `web_submit_data("Login", "Action=https://example.test/login", "Method=POST", ITEMDATA, "Name=user", "Value=ada", ENDITEM, LAST);`
	case "web_submit_form":
		return `web_submit_form("https://example.test/login", "Snapshot=t.inf", ITEMDATA, "Name=user", "Value=ada", ENDITEM, LAST);`
	case "web_custom_request":
		return `web_custom_request("Api", "URL=https://example.test/api", "Method=PUT", "Body=hello", LAST);`
	case "web_rest":
		return `web_rest("Rest", "URL=https://example.test/api", "Method=GET", LAST);`
	case "web_find":
		return `web_find("Verify", "What=Welcome", LAST);`
	case "web_reg_find":
		return `web_reg_find("Text=Welcome", LAST);`
	case "web_global_verification":
		return `web_global_verification("Text=ServerError", "Fail=Found", LAST);`
	case "web_reg_save_param":
		return `web_reg_save_param("Token", "LB=token=", "RB=;", LAST);`
	case "web_reg_save_param_ex":
		return `web_reg_save_param_ex("ParamName=Token", "LB=token=", "RB=;", LAST);`
	case "web_reg_save_param_regexp":
		return `web_reg_save_param_regexp("ParamName=Token", "RegExp=token=(.+?);", LAST);`
	case "web_reg_save_param_json":
		return `web_reg_save_param_json("ParamName=Token", "QueryString=$.token", LAST);`
	case "web_reg_save_param_xpath":
		return `web_reg_save_param_xpath("ParamName=Token", "QueryString=//token", LAST);`
	case "web_reg_save_param_attrib":
		return `web_reg_save_param_attrib("ParamName=Href", "Tag=a", "Attribute=href", LAST);`
	case "web_add_header":
		return `web_add_header("X-Trace", "abc123");`
	case "web_add_auto_header":
		return `web_add_auto_header("X-Env", "test");`
	case "web_revert_auto_header":
		return `web_revert_auto_header("X-Env");`
	case "web_remove_auto_header":
		return `web_remove_auto_header("X-Env");`
	case "web_cleanup_auto_headers":
		return `web_cleanup_auto_headers();`
	case "web_save_header":
		return `web_save_header(RESPONSE, "SavedHeaders");`
	case "web_add_cookie":
		return `web_add_cookie("SessionID=abc; domain=example.test");`
	case "web_add_cookie_ex":
		return `web_add_cookie_ex("SessionID=abc; domain=example.test", "URL=https://example.test/", LAST);`
	case "web_reg_add_cookie":
		return `web_reg_add_cookie("SessionID=abc; domain=example.test", "URL=https://example.test/", LAST);`
	case "web_remove_cookie":
		return `web_remove_cookie("SessionID");`
	case "web_set_user":
		return `web_set_user("user", "pass", "example.test:443");`
	case "web_set_timeout":
		return `web_set_timeout("STEP", "120");`
	case "web_set_sockets_option":
		return `web_set_sockets_option("SSL_VERSION", "TLS1.2");`
	case "web_set_option":
		return `web_set_option("MaxTotalConnections", "8", LAST);`
	case "web_set_max_retries":
		return `web_set_max_retries("3");`
	case "web_disable_keep_alive":
		return `web_disable_keep_alive();`
	case "web_enable_keep_alive":
		return `web_enable_keep_alive();`
	case "web_set_certificate":
		return `web_set_certificate("2");`
	case "web_set_certificate_ex":
		return `web_set_certificate_ex("CertFilePath=client.pem", "KeyFilePath=client.key", LAST);`
	case "web_set_proxy":
		return `web_set_proxy("proxy.example.test:8080");`
	case "web_set_secure_proxy":
		return `web_set_secure_proxy("proxy.example.test:8443");`
	case "web_set_proxy_bypass":
		return `web_set_proxy_bypass("internal.example.test");`
	case "web_set_proxy_bypass_local":
		return `web_set_proxy_bypass_local();`
	case "web_set_pac":
		return `web_set_pac("https://example.test/proxy.pac");`
	case "web_set_max_html_param_len":
		return `web_set_max_html_param_len("4096");`
	case "web_cache_cleanup":
		return `web_cache_cleanup();`
	case "web_cleanup_cookies":
		return `web_cleanup_cookies();`
	case "web_load_cache":
		return `web_load_cache("Cache", "FileName=cache.bin", LAST);`
	case "web_dump_cache":
		return `web_dump_cache("Cache", "FileName=cache.bin", LAST);`
	case "web_add_filter":
		return `web_add_filter("Action=Exclude", "ContentType=image/*", LAST);`
	case "web_add_auto_filter":
		return `web_add_auto_filter("Action=Include", "URL=https://example.test/", LAST);`
	case "web_remove_auto_filter":
		return `web_remove_auto_filter(LAST);`
	case "web_concurrent_start":
		return `web_concurrent_start(NULL);`
	case "web_concurrent_end":
		return `web_concurrent_end(NULL);`
	case "web_convert_param":
		return `web_convert_param("Param", "SourceEncoding=HTML", "TargetEncoding=URL", LAST);`
	case "web_convert_date_param":
		return `web_convert_date_param("Out", "SourceString={In}", LAST);`
	case "web_convert_from_formatted":
		return `web_convert_from_formatted("Out", "Format=%d", "Source={In}", LAST);`
	case "web_convert_to_formatted":
		return `web_convert_to_formatted("Out", "Format=%d", "Source={In}", LAST);`
	case "web_sjis_to_euc_param":
		return `web_sjis_to_euc_param("Out", "In");`
	case "web_save_param_length":
		return `web_save_param_length("Token", "Length", LAST);`
	case "web_save_timestamp_param":
		return `web_save_timestamp_param("Now", LAST);`
	case "web_get_int_property":
		return `web_get_int_property(HTTP_INFO_RETURN_CODE);`
	case "web_util_set_request_body":
		return `web_util_set_request_body("body bytes");`
	case "web_util_set_request_url":
		return `web_util_set_request_url("https://example.test/next");`
	case "web_report_data_point":
		return `web_report_data_point("dp", 1.5);`
	case "web_reg_async_attributes":
		return `web_reg_async_attributes("ID=poll", "Pattern=Poll", LAST);`
	case "web_stop_async":
		return `web_stop_async("poll");`
	case "web_js_run":
		return `web_js_run("Code=return 1;", "ResultParam=R", LAST);`
	case "web_js_reset":
		return `web_js_reset(LAST);`
	case "web_sync":
		return `web_sync("WaitForResponse=https://example.test/ready", LAST);`
	}
	if strings.HasPrefix(name, "web_stream_") {
		return name + `("StreamName");`
	}
	return name + `("Arg1", "Arg2");`
}

// TestWebHTTPFunctionRegistryResolvesEveryPublishedFunction is the closure
// backbone: every published Web (HTTP/HTML) function must convert to a concrete
// disposition — never a custom_call review stub, never Skipped/unsupported — and
// must emit a source-review mapping whose support mode matches the registry.
func TestWebHTTPFunctionRegistryResolvesEveryPublishedFunction(t *testing.T) {
	for _, def := range allWebHTTPFunctionDefinitions() {
		def := def
		t.Run(def.Name, func(t *testing.T) {
			source := "Action()\n{\n\t" + sampleWebFunctionCall(def.Name) +
				"\n\tweb_url(\"Anchor\", \"URL=https://example.test/anchor\", LAST);\n\treturn 0;\n}\n"
			parsed, err := ParseActionFile("Action.c", source)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
			if err != nil {
				t.Fatalf("convert error: %v", err)
			}
			if result.Stats.Skipped != 0 || len(result.Stats.UnsupportedFuncs) != 0 {
				t.Fatalf("%s: skipped=%d unsupported=%v, want none", def.Name, result.Stats.Skipped, result.Stats.UnsupportedFuncs)
			}
			if result.Stats.CustomCallFunctions != 0 {
				t.Fatalf("%s: produced %d custom_call stubs, want 0", def.Name, result.Stats.CustomCallFunctions)
			}
			for _, action := range result.Script.Actions {
				if action.Type == "custom_call" && action.Params["function"] == def.Name {
					t.Fatalf("%s: emitted a custom_call action instead of a real translation", def.Name)
				}
			}
			if result.SourceReview == nil {
				t.Fatalf("%s: missing source review", def.Name)
			}
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, def.Name, def.SupportMode)
		})
	}
}

func convertSingleWebSource(t *testing.T, body string) *ConvertResult {
	t.Helper()
	source := "Action()\n{\n" + body + "\n\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	return result
}

func TestWebLinkImageConvertToGetRequests(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_link("Catalog", "Text=Catalog", "URL=https://example.test/catalog", LAST);
	web_image("Logo", "Alt=Logo", "Src=https://example.test/logo.png", LAST);`)
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2 GET requests", len(result.Script.Actions))
	}
	for _, action := range result.Script.Actions {
		if action.Method != "GET" || action.Type != "GET" {
			t.Errorf("action %q method/type = %s/%s, want GET", action.Name, action.Method, action.Type)
		}
	}
	if got := result.Script.Actions[0].Target; !strings.Contains(got, "/catalog") {
		t.Errorf("web_link target = %q, want catalog URL", got)
	}
	if got := result.Script.Actions[1].Target; !strings.Contains(got, "/logo.png") {
		t.Errorf("web_image target = %q, want image Src URL", got)
	}
}

func TestWebLinkWithoutURLIsReviewedNotFabricated(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_url("Home", "URL=https://example.test/home", LAST);
	web_link("Continue", "Text=Continue", LAST);`)
	// Only the anchor request is emitted; the context-resolved link is not a request.
	for _, action := range result.Script.Actions {
		if action.Type == "GET" && strings.EqualFold(action.Name, "Continue") {
			t.Fatalf("web_link without a URL must not fabricate a GET action: %#v", action)
		}
	}
	if result.Stats.ReviewOnlyFunctions == 0 {
		t.Fatalf("web_link without URL should be classified review_only, stats=%#v", result.Stats)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_link", SourceFunctionSupportReviewOnly)
	if !hasWarningContaining(result.Warnings, "no URL") {
		t.Errorf("expected a 'no URL' warning, got %v", result.Warnings)
	}
}

func webFindAssertionPresent(assertions []dsl.Assertion, value string) bool {
	for _, a := range assertions {
		if a.Type == "body_contains" && a.Value == value {
			return true
		}
	}
	return false
}

func TestWebFindConvertsToAssertion(t *testing.T) {
	// web_find checks the PREVIOUS response, so its assertion must land on the
	// request that came before it (Home) — not the following request (Next).
	result := convertSingleWebSource(t, `
	web_url("Home", "URL=https://example.test/home", LAST);
	web_find("Verify", "What=Welcome back", LAST);
	web_url("Next", "URL=https://example.test/next", LAST);`)
	if len(result.Script.Actions) != 2 {
		t.Fatalf("expected 2 request actions, got %d", len(result.Script.Actions))
	}
	if !webFindAssertionPresent(result.Script.Actions[0].Assertions, "Welcome back") {
		t.Fatalf("web_find assertion did not attach to the preceding request (Home): %#v", result.Script.Actions[0].Assertions)
	}
	if webFindAssertionPresent(result.Script.Actions[1].Assertions, "Welcome back") {
		t.Fatalf("web_find assertion wrongly attached to the FOLLOWING request (Next): %#v", result.Script.Actions[1].Assertions)
	}
}

func TestWebFindBeforeAnyRequestAttachesForwardWithWarning(t *testing.T) {
	// No preceding request: the check is preserved by attaching forward, and a
	// warning flags the relocation rather than dropping it silently.
	result := convertSingleWebSource(t, `
	web_find("Verify", "What=Welcome", LAST);
	web_url("Home", "URL=https://example.test/home", LAST);`)
	if !webFindAssertionPresent(result.Script.Actions[0].Assertions, "Welcome") {
		t.Fatalf("fallback: web_find assertion should attach forward to Home: %#v", result.Script.Actions[0].Assertions)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "web_find") && strings.Contains(w, "no preceding request") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning about web_find with no preceding request, got: %v", result.Warnings)
	}
}

func TestWebRemoveCookieStopsAttachingDownstream(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_add_cookie("SessionID=abc; domain=example.test");
	web_remove_cookie("SessionID");
	web_url("Home", "URL=https://example.test/home", LAST);`)
	action := result.Script.Actions[0]
	if cookie, ok := action.Headers["Cookie"]; ok && strings.Contains(cookie, "SessionID") {
		t.Fatalf("web_remove_cookie did not stop the cookie attaching: Cookie=%q", cookie)
	}
}

func TestWebDisableKeepAliveMapsToConfig(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_disable_keep_alive();
	web_url("Home", "URL=https://example.test/home", LAST);`)
	if result.Script.Config.HTTP.KeepAlive == nil || *result.Script.Config.HTTP.KeepAlive {
		t.Fatalf("web_disable_keep_alive should set config.http.keep_alive=false, got %v", result.Script.Config.HTTP.KeepAlive)
	}
}

func TestWebSetProxyMapsToConfig(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_set_proxy("proxy.example.test:8080");
	web_set_proxy_bypass("internal.example.test");
	web_url("Home", "URL=https://example.test/home", LAST);`)
	if result.Script.Config.HTTP.Proxy.URL != "http://proxy.example.test:8080" {
		t.Fatalf("proxy url = %q, want http://proxy.example.test:8080", result.Script.Config.HTTP.Proxy.URL)
	}
	if len(result.Script.Config.HTTP.Proxy.NoProxy) == 0 || result.Script.Config.HTTP.Proxy.NoProxy[0] != "internal.example.test" {
		t.Fatalf("no_proxy = %v, want [internal.example.test]", result.Script.Config.HTTP.Proxy.NoProxy)
	}
}

func TestWebSetCertificateExMapsToTLSConfig(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_set_certificate_ex("CertFilePath=client.pem", "KeyFilePath=client.key", LAST);
	web_url("Home", "URL=https://example.test/home", LAST);`)
	if result.Script.Config.HTTP.TLS.ClientCertFile != "client.pem" || result.Script.Config.HTTP.TLS.ClientKeyFile != "client.key" {
		t.Fatalf("tls cert/key = %q/%q, want client.pem/client.key", result.Script.Config.HTTP.TLS.ClientCertFile, result.Script.Config.HTTP.TLS.ClientKeyFile)
	}
}

func TestWebRegSaveParamAttribConvertsToXPathExtractor(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_reg_save_param_attrib("ParamName=Href", "Tag=a", "Attribute=href", LAST);
	web_url("Home", "URL=https://example.test/home", LAST);`)
	action := result.Script.Actions[0]
	if len(action.Extractors) != 1 {
		t.Fatalf("extractors = %#v, want 1 attrib extractor", action.Extractors)
	}
	ext := action.Extractors[0]
	if ext.Type != "xpath" || ext.Expression != "//a/@href" {
		t.Fatalf("attrib extractor = %#v, want xpath //a/@href", ext)
	}
}

func TestWebReportDataPointMapsToMetric(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_report_data_point("latency_dp", 12.5);
	web_url("Home", "URL=https://example.test/home", LAST);`)
	if !hasActionOfType(result.Script.Actions, "source_metric") {
		t.Fatalf("web_report_data_point should emit a source_metric action: %#v", actionTypes(result.Script.Actions))
	}
}

func TestWebStreamFunctionsAreReviewOnly(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_stream_open("Stream", "URL=rtmp://example.test/live", LAST);
	web_stream_play("Stream");
	web_url("Home", "URL=https://example.test/home", LAST);`)
	if result.Stats.CustomCallFunctions != 0 {
		t.Fatalf("web_stream_* must not be custom_call stubs, stats=%#v", result.Stats)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_stream_open", SourceFunctionSupportReviewOnly)
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_stream_play", SourceFunctionSupportReviewOnly)
}

func TestWebConcurrentGroupWarnsAndSerializes(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_concurrent_start(NULL);
	web_url("A", "URL=https://example.test/a", LAST);
	web_url("B", "URL=https://example.test/b", LAST);
	web_concurrent_end(NULL);`)
	if !hasWarningContaining(result.Warnings, "replayed sequentially") {
		t.Errorf("expected a serialization warning for web_concurrent_start, got %v", result.Warnings)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_concurrent_start", SourceFunctionSupportNativeNoop)
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func hasActionOfType(actions []dsl.Action, actionType string) bool {
	for _, action := range actions {
		if action.Type == actionType {
			return true
		}
	}
	return false
}

func actionTypes(actions []dsl.Action) []string {
	types := make([]string, 0, len(actions))
	for _, action := range actions {
		types = append(types, action.Type)
	}
	return types
}
