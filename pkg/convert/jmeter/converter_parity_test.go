package jmeter

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// parityPlan wraps samplers in a plan whose thread group carries props.
func parityPlan(groupProps, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true">` + groupProps + `</ThreadGroup>
      <hashTree>
        ` + body + `
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
}

func paritySampler(protocol, port, extra string) string {
	return `<HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
          <stringProp name="HTTPSampler.protocol">` + protocol + `</stringProp>
          <stringProp name="HTTPSampler.port">` + port + `</stringProp>
          <stringProp name="HTTPSampler.path">/orders</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
          ` + extra + `
        </HTTPSamplerProxy>
        <hashTree/>`
}

func convertParity(t *testing.T, jmx string, opts ConvertOptions) (*dsl.Script, *ConvertResult) {
	t.Helper()
	if opts.FileName == "" {
		opts.FileName = "parity.jmx"
	}
	result, err := ConvertJMX([]byte(jmx), opts)
	if err != nil {
		t.Fatalf("ConvertJMX: %v", err)
	}
	if len(result.Scripts) == 0 {
		t.Fatal("no scripts")
	}
	return result.Scripts[0], result
}

func parityDiagnostic(result *ConvertResult, severity diag.Severity, text string) bool {
	for _, item := range append(append(diag.Diagnostics(nil), result.Diagnostics...), result.Unsupported...) {
		if item.Severity == severity && strings.Contains(item.Detail, text) {
			return true
		}
	}
	return false
}

// JMeter sends a raw body byte for byte, so the CRLF that ends the source
// body is part of the request.
func TestRawBodyKeepsSurroundingWhitespace(t *testing.T) {
	script, _ := convertParity(t, splitPayText(t), ConvertOptions{})
	body := script.Actions[0].Body
	if !strings.HasSuffix(body, "}\r\n") {
		t.Fatalf("body ends %q, want the source's trailing CRLF", body[max(len(body)-8, 0):])
	}
	raw := `<boolProp name="HTTPSampler.postBodyRaw">true</boolProp>
          <elementProp name="HTTPsampler.Arguments" elementType="Arguments"><collectionProp name="Arguments.arguments">
            <elementProp name="" elementType="HTTPArgument"><stringProp name="Argument.value">  {"a":1}&#xd;
</stringProp></elementProp>
          </collectionProp></elementProp>`
	script, _ = convertParity(t, parityPlan("", paritySampler("https", "", raw)), ConvertOptions{})
	if got := script.Actions[0].Body; got != "  {\"a\":1}\r\n" {
		t.Fatalf("body = %q, want leading spaces and trailing CRLF kept", got)
	}
}

// Form parameter values are encoded as authored; a trailing space is data.
func TestSamplerParameterValuesKeepWhitespace(t *testing.T) {
	args := `<elementProp name="HTTPsampler.Arguments" elementType="Arguments"><collectionProp name="Arguments.arguments">
            <elementProp name="q" elementType="HTTPArgument"><stringProp name="Argument.name">q</stringProp><stringProp name="Argument.value">two words </stringProp></elementProp>
          </collectionProp></elementProp>`
	script, _ := convertParity(t, parityPlan("", paritySampler("https", "", args)), ConvertOptions{})
	if got := script.Actions[0].Params["q"]; got != "two words " {
		t.Fatalf("param q = %#v, want the trailing space kept", got)
	}
}

// JMeter leaves the scheme's default port out of the URL and the Host header.
func TestBaseURLDropsTheSchemeDefaultPort(t *testing.T) {
	for _, tc := range []struct{ protocol, port, want string }{
		{"https", "443", "https://api.example.test"},
		{"http", "80", "http://api.example.test"},
		{"HTTPS", "443", "HTTPS://api.example.test"},
		{"https", "8443", "https://api.example.test:8443"},
		{"http", "443", "http://api.example.test:443"},
	} {
		script, _ := convertParity(t, parityPlan("", paritySampler(tc.protocol, tc.port, "")), ConvertOptions{})
		if script.Config.BaseURL != tc.want {
			t.Errorf("%s port %s: base_url = %q, want %q", tc.protocol, tc.port, script.Config.BaseURL, tc.want)
		}
	}
}

// JMeter's HTTP samplers speak HTTP/1.1 and trust any certificate; the
// converted script matches both and says so about TLS.
func TestHTTPScriptsMatchJMeterProtocolAndCertificateTrust(t *testing.T) {
	script, result := convertParity(t, parityPlan("", paritySampler("https", "443", "")), ConvertOptions{})
	if script.Config.HTTP.Version != "1.1" {
		t.Fatalf("http version = %q, want 1.1", script.Config.HTTP.Version)
	}
	if !script.Config.HTTP.TLS.InsecureSkipVerify {
		t.Fatal("certificate verification is still on; JMeter accepts any certificate")
	}
	if !parityDiagnostic(result, diag.Warning, "accept any server certificate") || !parityDiagnostic(result, diag.Warning, "ca_cert_file") {
		t.Fatalf("diagnostics lack the TLS warning: %+v", result.Unsupported)
	}
	// The retained-credential guard pins TLS on scripts that keep an imported
	// credential, so the warning must name the edit path that works for them
	// and say that carried credentials go to unverified servers.
	for _, text := range []string{"any credential they carry, go to servers whose certificates are not checked", "bind the credential to a runtime secret first", "Access > Import credential policy"} {
		if !parityDiagnostic(result, diag.Warning, text) {
			t.Fatalf("TLS warning lacks %q: %+v", text, result.Unsupported)
		}
	}
	found := false
	for _, warning := range result.Warnings {
		found = found || strings.Contains(warning, "insecure_skip_verify")
	}
	if !found {
		t.Fatalf("warnings = %v, want the TLS warning visible", result.Warnings)
	}
	if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) != 0 {
		t.Fatalf("invalid script: %v", issues)
	}
}

// Without a Cookie Manager JMeter neither keeps nor sends cookies.
func TestCookiesFollowTheCookieManager(t *testing.T) {
	script, result := convertParity(t, parityPlan("", paritySampler("https", "", "")), ConvertOptions{})
	if script.Config.HTTP.CookiesEnabled == nil || *script.Config.HTTP.CookiesEnabled {
		t.Fatalf("cookies_enabled = %v, want false without a Cookie Manager", script.Config.HTTP.CookiesEnabled)
	}
	if !parityDiagnostic(result, diag.Info, "no HTTP Cookie Manager") {
		t.Fatalf("diagnostics lack the cookie note: %+v", result.Unsupported)
	}

	manager := func(props string) string {
		return `<CookieManager testname="Cookies" enabled="true">` + props + `</CookieManager><hashTree/>`
	}
	for name, tc := range map[string]struct {
		group, manager string
		clear          bool
	}{
		"kept across iterations":      {"", manager(`<boolProp name="CookieManager.clearEachIteration">false</boolProp>`), false},
		"cleared each iteration":      {"", manager(`<boolProp name="CookieManager.clearEachIteration">true</boolProp>`), true},
		"new user, follows the group": {`<boolProp name="ThreadGroup.same_user_on_next_iteration">false</boolProp>`, manager(`<boolProp name="CookieManager.controlledByThreadGroup">true</boolProp>`), true},
		"new user, own setting":       {`<boolProp name="ThreadGroup.same_user_on_next_iteration">false</boolProp>`, manager(`<boolProp name="CookieManager.clearEachIteration">false</boolProp>`), false},
		"nested in a controller": {"", `<GenericController testname="group" enabled="true"/><hashTree>` +
			manager("") + `</hashTree>`, false},
	} {
		script, _ := convertParity(t, parityPlan(tc.group, tc.manager+paritySampler("https", "", "")), ConvertOptions{})
		if script.Config.HTTP.CookiesEnabled != nil && !*script.Config.HTTP.CookiesEnabled {
			t.Errorf("%s: cookies turned off despite a Cookie Manager", name)
		}
		cleared := script.Config.HTTP.ClearCookiesPerIter != nil && *script.Config.HTTP.ClearCookiesPerIter
		if cleared != tc.clear {
			t.Errorf("%s: clear_cookies_per_iteration = %v, want %v", name, cleared, tc.clear)
		}
	}
}

// A SOAP-promoted group keeps its cookies across iterations at runtime, so the
// Cookie Manager's clear-each-iteration mapping is disclosed as not applied.
func TestSOAPPromotionDisclosesUnappliedCookieClear(t *testing.T) {
	soapHeader := `<HeaderManager testclass="HeaderManager" testname="SOAP headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp name="" elementType="Header"><stringProp name="Header.name">SOAPAction</stringProp><stringProp name="Header.value">urn:GetQuote</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>`
	manager := `<CookieManager testname="Cookies" enabled="true"><boolProp name="CookieManager.clearEachIteration">true</boolProp></CookieManager><hashTree/>`
	const disclosure = "SOAP scripts keep their cookies from one iteration to the next"
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"soap, cleared each iteration": {manager + soapHeader + paritySampler("https", "", ""), true},
		"soap, cookies kept":           {soapHeader + `<CookieManager testname="Cookies" enabled="true"/><hashTree/>` + paritySampler("https", "", ""), false},
		"http, cleared each iteration": {manager + paritySampler("https", "", ""), false},
	} {
		script, result := convertParity(t, parityPlan("", tc.body), ConvertOptions{})
		disclosed := false
		for _, warning := range result.Warnings {
			if strings.Contains(warning, disclosure) {
				disclosed = true
			}
		}
		if disclosed != tc.want {
			t.Errorf("%s: protocol=%s disclosed=%v, want %v (warnings %v)", name, script.Protocol, disclosed, tc.want, result.Warnings)
		}
		if tc.want && script.Protocol != "soap" {
			t.Errorf("%s: protocol = %q, want soap", name, script.Protocol)
		}
	}
}

// Empty timeouts keep APEX's bounded defaults, with a note that JMeter had
// none; HTTP Request Defaults supply a timeout the sampler leaves empty.
func TestTimeoutsDiscloseDefaultsAndInheritRequestDefaults(t *testing.T) {
	script, result := convertParity(t, parityPlan("", paritySampler("https", "", "")), ConvertOptions{})
	if script.Actions[0].TimeoutMs != 0 {
		t.Fatalf("timeout = %d, want the runtime default", script.Actions[0].TimeoutMs)
	}
	if !parityDiagnostic(result, diag.Info, "1 HTTP sampler(s) set no connect or response timeout") || !parityDiagnostic(result, diag.Info, "30s to connect and 1m0s per request") {
		t.Fatalf("diagnostics lack the timeout disclosure: %+v", result.Unsupported)
	}
	defaults := `<ConfigTestElement guiclass="HttpDefaultsGui" testname="Defaults" enabled="true">
          <stringProp name="HTTPSampler.response_timeout">5000</stringProp>
        </ConfigTestElement><hashTree/>`
	script, result = convertParity(t, parityPlan("", defaults+paritySampler("https", "", "")), ConvertOptions{})
	if script.Actions[0].TimeoutMs != 5000 {
		t.Fatalf("timeout = %d, want 5000 from HTTP Request Defaults", script.Actions[0].TimeoutMs)
	}
	if parityDiagnostic(result, diag.Info, "set no connect or response timeout") {
		t.Fatalf("timeout disclosure despite an inherited timeout: %+v", result.Unsupported)
	}
}

// A JMeter connect timeout bounds only the connect phase, so neither an
// inherited nor a sampler-level connect-only timeout may become the action's
// whole-request timeout; it is disclosed instead.
func TestConnectOnlyTimeoutIsNotARequestDeadline(t *testing.T) {
	defaults := `<ConfigTestElement guiclass="HttpDefaultsGui" testname="Defaults" enabled="true">
          <stringProp name="HTTPSampler.connect_timeout">3000</stringProp>
        </ConfigTestElement><hashTree/>`
	for name, body := range map[string]string{
		"inherited":     defaults + paritySampler("https", "", ""),
		"sampler_level": paritySampler("https", "", `<stringProp name="HTTPSampler.connect_timeout">3000</stringProp>`),
	} {
		t.Run(name, func(t *testing.T) {
			script, result := convertParity(t, parityPlan("", body), ConvertOptions{})
			if script.Actions[0].TimeoutMs != 0 {
				t.Fatalf("timeout = %d, want the runtime default: a connect timeout is not a request deadline", script.Actions[0].TimeoutMs)
			}
			if !parityDiagnostic(result, diag.Info, "1 HTTP sampler(s) set only a connect timeout") {
				t.Fatalf("diagnostics lack the connect-only disclosure: %+v", result.Unsupported)
			}
		})
	}
	// An inherited connect timeout next to a response timeout keeps the
	// response timeout as the action timeout.
	script, result := convertParity(t, parityPlan("", defaults+paritySampler("https", "", `<stringProp name="HTTPSampler.response_timeout">7000</stringProp>`)), ConvertOptions{})
	if script.Actions[0].TimeoutMs != 7000 || parityDiagnostic(result, diag.Info, "only a connect timeout") {
		t.Fatalf("timeout = %d, want 7000 with no connect-only disclosure", script.Actions[0].TimeoutMs)
	}
}

// A timeout written as a variable or function does set a timeout in JMeter;
// the disclosure must not claim the sampler waits without limit.
func TestVariableTimeoutIsDisclosedAsUnresolved(t *testing.T) {
	script, result := convertParity(t, parityPlan("", paritySampler("https", "", `<stringProp name="HTTPSampler.response_timeout">${__P(rt,30000)}</stringProp>`)), ConvertOptions{})
	if script.Actions[0].TimeoutMs != 0 {
		t.Fatalf("timeout = %d, want the runtime default", script.Actions[0].TimeoutMs)
	}
	if parityDiagnostic(result, diag.Info, "set no connect or response timeout") {
		t.Fatalf("a variable timeout was reported as no timeout: %+v", result.Unsupported)
	}
	if !parityDiagnostic(result, diag.Info, "1 HTTP sampler(s) take a timeout from a variable or function") {
		t.Fatalf("diagnostics lack the variable-timeout disclosure: %+v", result.Unsupported)
	}
}

// A connect timeout written as a variable bounds only the connect phase, so
// the disclosure must not suggest using it as the action's whole-request
// timeout.
func TestVariableConnectTimeoutIsNotSuggestedAsActionTimeout(t *testing.T) {
	defaults := `<ConfigTestElement guiclass="HttpDefaultsGui" testname="Defaults" enabled="true">
          <stringProp name="HTTPSampler.connect_timeout">${__P(ct,5000)}</stringProp>
        </ConfigTestElement><hashTree/>`
	script, result := convertParity(t, parityPlan("", defaults+paritySampler("https", "", "")), ConvertOptions{})
	if script.Actions[0].TimeoutMs != 0 {
		t.Fatalf("timeout = %d, want the runtime default", script.Actions[0].TimeoutMs)
	}
	if !parityDiagnostic(result, diag.Info, "1 HTTP sampler(s) take a timeout from a variable or function") {
		t.Fatalf("diagnostics lack the variable-timeout disclosure: %+v", result.Unsupported)
	}
	if parityDiagnostic(result, diag.Info, "Set the action's timeout_ms, or config.http.connect_timeout_ms") ||
		!parityDiagnostic(result, diag.Info, "a variable connect timeout only as config.http.connect_timeout_ms") {
		t.Fatalf("the disclosure must keep a variable connect timeout out of the action timeout: %+v", result.Unsupported)
	}
}

// The thread group's sample-error action is recorded on the script; actions
// that stop a thread or the test have no equivalent and gate the script.
func TestThreadGroupSampleErrorAction(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   bool
		gated  bool
	}{
		{"", true, false},
		{"continue", true, false},
		{"startnextloop", false, false},
		{"stopthread", false, true},
		{"stoptest", false, true},
		{"stoptestnow", false, true},
	} {
		props := ""
		if tc.action != "" {
			props = `<stringProp name="ThreadGroup.on_sample_error">` + tc.action + `</stringProp>`
		}
		script, result := convertParity(t, parityPlan(props, paritySampler("https", "", "")), ConvertOptions{})
		continueOnError := script.Config.ErrorHandling.ContinueOnError
		if continueOnError == nil || *continueOnError != tc.want {
			t.Errorf("%q: continue_on_error = %v, want %v", tc.action, continueOnError, tc.want)
		}
		if gated := hasJMeterReviewReason(script.Actions, jmeterSampleErrorReason); gated != tc.gated {
			t.Errorf("%q: gated = %v, want %v", tc.action, gated, tc.gated)
		}
		if tc.gated && !parityDiagnostic(result, diag.Blocker, "APEX can only continue or end the iteration") {
			t.Errorf("%q: diagnostics lack the blocker: %+v", tc.action, result.Unsupported)
		}
	}
}

// JMeter can run thread groups one after another; the draft starts them all
// at once, which the import must say.
func TestSerializedThreadGroupsAreDisclosed(t *testing.T) {
	group := func(name string) string {
		return `<ThreadGroup testname="` + name + `" enabled="true"/><hashTree>` + paritySampler("https", "", "") + `</hashTree>`
	}
	plan := func(serialize string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><jmeterTestPlan version="1.2"><hashTree>
    <TestPlan testname="Plan" enabled="true"><boolProp name="TestPlan.serialize_threadgroups">` + serialize + `</boolProp></TestPlan>
    <hashTree>` + group("First") + group("Second") + `</hashTree></hashTree></jmeterTestPlan>`
	}
	_, result := convertParity(t, plan("true"), ConvertOptions{})
	if !parityDiagnostic(result, diag.Warning, "one after another") {
		t.Fatalf("diagnostics lack the serial-groups warning: %+v", result.Unsupported)
	}
	_, result = convertParity(t, plan("false"), ConvertOptions{})
	if parityDiagnostic(result, diag.Warning, "one after another") {
		t.Fatalf("serial-groups warning for concurrent groups: %+v", result.Unsupported)
	}
}

// A script saved without the scenario draft keeps the thread group's users,
// ramp-up and loops in its import metadata.
func TestScriptOnlyImportKeepsThreadGroupPopulation(t *testing.T) {
	script, result := convertParity(t, splitPayText(t), ConvertOptions{ScenarioDraft: false})
	if result.ScenarioDraft != nil {
		t.Fatal("scenario draft returned without being requested")
	}
	population, _ := script.Metadata.Import.Preserved["source_population"].(map[string]any)
	if population["num_threads"] != 50 || population["ramp_time_seconds"] != 1 || population["loops"] != 200 {
		t.Fatalf("source_population = %#v, want 50 users, 1 s ramp, 200 loops", population)
	}
	if _, scheduled := population["scheduler"]; scheduled {
		t.Fatalf("source_population = %#v, want no schedule for an unscheduled group", population)
	}
	props := `<stringProp name="ThreadGroup.num_threads">${__P(users,10)}</stringProp>
        <boolProp name="ThreadGroup.scheduler">true</boolProp><stringProp name="ThreadGroup.duration">600</stringProp>`
	script, _ = convertParity(t, parityPlan(props, paritySampler("https", "", "")), ConvertOptions{})
	population, _ = script.Metadata.Import.Preserved["source_population"].(map[string]any)
	if population["num_threads_source"] != "${__P(users,10)}" || population["scheduler"] != true || population["duration_seconds"] != 600 {
		t.Fatalf("source_population = %#v, want the source thread expression and the 600 s schedule", population)
	}
}

// Source review links the plan, its variables, headers and thread group to
// the script they shaped.
func TestSourceReviewMapsConfigurationElements(t *testing.T) {
	_, result := convertParity(t, splitPayText(t), ConvertOptions{ScenarioDraft: true})
	if result.SourceReview == nil {
		t.Fatal("no source review")
	}
	want := map[string]string{"TestPlan": "", "Arguments": "", "HeaderManager": "", "ThreadGroup": "config.run_logic"}
	for function, target := range want {
		if !containsJMeterMapping(result.SourceReview.Mappings, function, target, "converted") {
			t.Errorf("no converted %s mapping to %q in %+v", function, target, mappingFunctions(result.SourceReview.Mappings))
		}
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetKind == "config" && mapping.StartLine <= 1 {
			t.Errorf("config mapping %s has no source line", mapping.Function)
		}
	}
	without := append([]sourceview.Mapping(nil), result.SourceReview.Mappings...)
	kept := without[:0]
	for _, mapping := range without {
		if mapping.TargetKind != "config" {
			kept = append(kept, mapping)
		}
	}
	mapped, review, adapter := tallyJMeterCategories(kept)
	if mapped != result.Stats.Mapped || review != result.Stats.ReviewRequired || adapter != result.Stats.AdapterRequired {
		t.Fatalf("configuration mappings changed the behaviour tally: %d/%d/%d vs %+v", mapped, review, adapter, result.Stats)
	}
}

// A shared counter over a range narrower than the plan's thread count keeps
// its range and wrap instead of issuing values past the source maximum.
func TestNarrowSharedCounterKeepsItsRange(t *testing.T) {
	for name, tc := range map[string]struct {
		threads, start, end string
	}{
		"fewer values than threads": {"50", "0", "5"},
	} {
		plan := strings.Replace(counterPlan(counterProps(tc.start, tc.end, "1", "", "false"), false, "p-${p_TraceID}"),
			`<ThreadGroup testname="HTTP" enabled="true"/>`,
			`<ThreadGroup testname="HTTP" enabled="true"><stringProp name="ThreadGroup.num_threads">`+tc.threads+`</stringProp></ThreadGroup>`, 1)
		result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "counter.jmx"})
		if err != nil {
			t.Fatal(err)
		}
		script := result.Scripts[0]
		parameter, missing, globalGate := counterScriptState(script)
		if parameter == nil || len(missing) != 0 || globalGate {
			t.Fatalf("%s: parameter=%+v missing=%v globalGate=%v, want an executable counter", name, parameter, missing, globalGate)
		}
		if parameter.Selection != "" || parameter.BlockSize != 0 || parameter.End != parseInt(tc.end) || parameter.Start != parseInt(tc.start) || parameter.OnExhaustion != "wrap" {
			t.Fatalf("%s: parameter = %+v, want a per-VUser counter wrapping %s..%s", name, parameter, tc.start, tc.end)
		}
		if !parityDiagnostic(result, diag.Warning, "too narrow to give each VUser its own block") {
			t.Fatalf("%s: diagnostics lack the range note: %+v", name, result.Unsupported)
		}
		if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) != 0 {
			t.Fatalf("%s: invalid script: %v", name, issues)
		}
	}
}

// With a thread count that is not a literal the VUser count is unknown, so a
// shared counter keeps unique-number blocks: values stay distinct across
// VUsers whatever the range, instead of every VUser wrapping through it.
func TestSharedCounterWithUnknownThreadCountKeepsUniqueBlocks(t *testing.T) {
	for name, tc := range map[string]struct {
		threads, start, end string
		block               int
	}{
		"wide range":   {"${__P(threads,10)}", "1", "100000", 100000},
		"under 2M":     {"${__P(threads,10)}", "1", "999999", 999999},
		"narrow range": {"${__P(users)}", "1", "50", 50},
	} {
		plan := strings.Replace(counterPlan(counterProps(tc.start, tc.end, "1", "", "false"), false, "p-${p_TraceID}"),
			`<ThreadGroup testname="HTTP" enabled="true"/>`,
			`<ThreadGroup testname="HTTP" enabled="true"><stringProp name="ThreadGroup.num_threads">`+tc.threads+`</stringProp></ThreadGroup>`, 1)
		result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "counter.jmx"})
		if err != nil {
			t.Fatal(err)
		}
		parameter, missing, globalGate := counterScriptState(result.Scripts[0])
		if parameter == nil || len(missing) != 0 || globalGate {
			t.Fatalf("%s: parameter=%+v missing=%v globalGate=%v, want an executable counter", name, parameter, missing, globalGate)
		}
		if parameter.Selection != "unique" || parameter.BlockSize != tc.block {
			t.Fatalf("%s: parameter = %+v, want unique-number blocks of %d", name, parameter, tc.block)
		}
		if parityDiagnostic(result, diag.Warning, "too narrow to give each VUser its own block") {
			t.Fatalf("%s: an unknown thread count must not be reported as too narrow: %+v", name, result.Unsupported)
		}
	}
}

// A Throughput Shaping Timer starting at 0 requests/s is a warm-up, not an
// invalid profile; one that cannot become pacing gates the script instead of
// letting it run unpaced behind a warning.
func TestShapingTimerZeroRateAndUnconvertedGate(t *testing.T) {
	segments, invalid := jmeterShapingProfile(mustShapingElement(t, [3]string{"0", "18", "60"}, [3]string{"18", "18", "3600"}))
	if invalid != "" || len(segments) != 2 || segments[0].StartRPS != 0 {
		t.Fatalf("segments=%+v invalid=%q, want the 0 rps warm-up accepted", segments, invalid)
	}
	for rows, reason := range map[[3]string]string{
		{"-1", "5", "60"}:  "row 1 of its load profile has a negative or non-finite rate",
		{"5", "5", "0"}:    "row 1 of its load profile lasts 0 s; each row must last at least 1 s",
		{"${r}", "5", "1"}: "row 1 of its load profile has a rate that is not a literal number",
	} {
		if _, got := jmeterShapingProfile(mustShapingElement(t, rows)); got != reason {
			t.Errorf("%v: reason = %q, want %q", rows, got, reason)
		}
	}

	enabled := strings.Replace(splitPayText(t), `testname="jp@gc - Throughput Shaping Timer" enabled="false"`, `testname="jp@gc - Throughput Shaping Timer" enabled="true"`, 1)
	script, result := convertParity(t, enabled, ConvertOptions{})
	if script.Config.Pacing.Type != "" {
		t.Fatalf("pacing = %+v, want none for a ramping profile", script.Config.Pacing)
	}
	if !hasJMeterReviewReason(script.Actions, jmeterShapingReviewReason) {
		t.Fatal("unconverted shaping timer left the script runnable and unpaced")
	}
	if !parityDiagnostic(result, diag.Blocker, "changes rate over time") || parityDiagnostic(result, diag.Blocker, "empty or invalid") {
		t.Fatalf("diagnostics lack the real reason: %+v", result.Unsupported)
	}
	zero := parityPlan("", paritySampler("https", "", "")+``)
	zero = strings.Replace(zero, `<hashTree/>`, `<hashTree>`+shapingTimer("pause", [3]string{"0", "0", "60"})+`</hashTree>`, 1)
	script, result = convertParity(t, zero, ConvertOptions{})
	if !hasJMeterReviewReason(script.Actions, jmeterShapingReviewReason) || !parityDiagnostic(result, diag.Blocker, "holds the rate at 0 requests/s") {
		t.Fatalf("a 0 rps profile was not gated with its reason: %+v", result.Unsupported)
	}
	// At 0 requests/s the plugin still admits the first request of each
	// second, so the reason must not say the requests pause.
	if parityDiagnostic(result, diag.Blocker, "pauses the requests") || !parityDiagnostic(result, diag.Blocker, "holds every other thread until another request is admitted") {
		t.Fatalf("the 0 rps reason misstates the plugin's behaviour: %+v", result.Unsupported)
	}
}

// In an iteration-terminated group the profile end is an upper bound, and a
// scenario duration is not allowed, so the note estimates the run instead of
// asking for a duration.
func TestShapingNoteFitsIterationTermination(t *testing.T) {
	constant := strings.Replace(splitPayText(t), `testname="jp@gc - Throughput Shaping Timer" enabled="false"`, `testname="jp@gc - Throughput Shaping Timer" enabled="true"`, 1)
	constant = strings.Replace(constant, `<stringProp name="48">0</stringProp>`, `<stringProp name="48">18</stringProp>`, 1)
	script, result := convertParity(t, constant, ConvertOptions{})
	if script.Config.Pacing.Type != "tps" || script.Config.Pacing.TPS != 18 {
		t.Fatalf("pacing = %+v, want 18 tps", script.Config.Pacing)
	}
	if !parityDiagnostic(result, diag.Warning, "10000 iterations take about 556 s at 18 iterations/s, within the profile") {
		t.Fatalf("diagnostics lack the iteration estimate: %+v", result.Unsupported)
	}
	if parityDiagnostic(result, diag.Warning, "set the scenario duration") || parityDiagnostic(result, diag.Warning, "keep the scenario duration") {
		t.Fatalf("note still asks for a duration the iteration scenario rejects: %+v", result.Unsupported)
	}
	longer := strings.Replace(constant, `<stringProp name="LoopController.loops">200</stringProp>`, `<stringProp name="LoopController.loops">2000</stringProp>`, 1)
	_, result = convertParity(t, longer, ConvertOptions{})
	if !parityDiagnostic(result, diag.Warning, "longer than the profile, so APEX sends more requests than JMeter; lower the iterations to about 1317 per VUser") {
		t.Fatalf("diagnostics lack the over-run advice: %+v", result.Unsupported)
	}
}

func splitPayText(t *testing.T) string {
	t.Helper()
	return string(splitPayFixture(t, "original"))
}

func mustShapingElement(t *testing.T, rows ...[3]string) *xmlNode {
	t.Helper()
	root, err := parseXML([]byte(shapingTimer("timer", rows...)[:strings.Index(shapingTimer("timer", rows...), "\n")]))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mappingFunctions(mappings []sourceview.Mapping) []string {
	var functions []string
	for _, mapping := range mappings {
		functions = append(functions, mapping.Function+"->"+mapping.TargetPath+"("+mapping.Status+")")
	}
	return functions
}

// reviewDiagnostics returns the review-evidence diagnostics (what the
// dashboard shows) whose detail holds text; result.Unsupported is the raw list.
func reviewDiagnostics(result *ConvertResult, text string) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, item := range result.Diagnostics {
		if strings.Contains(item.Detail, text) {
			out = append(out, item)
		}
	}
	return out
}

// planOfGroups builds a plan from complete thread group snippets.
func planOfGroups(groups ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><jmeterTestPlan version="1.2"><hashTree>
    <TestPlan testname="Plan" enabled="true"><boolProp name="TestPlan.serialize_threadgroups">true</boolProp></TestPlan>
    <hashTree>` + strings.Join(groups, "") + `</hashTree></hashTree></jmeterTestPlan>`
}

// Every timeout disclosure reaches review evidence: kinds within one group,
// and groups that share JMeter's default name.
func TestTimeoutDisclosuresSurviveReviewEvidence(t *testing.T) {
	body := paritySampler("https", "", "") +
		paritySampler("https", "", `<stringProp name="HTTPSampler.connect_timeout">5000</stringProp>`) +
		paritySampler("https", "", `<stringProp name="HTTPSampler.response_timeout">${T}</stringProp>`)
	_, result := convertParity(t, parityPlan("", body), ConvertOptions{})
	for _, text := range []string{"set no connect or response timeout", "set only a connect timeout", "take a timeout from a variable or function"} {
		if len(reviewDiagnostics(result, text)) != 1 {
			t.Errorf("review evidence lacks %q: %+v", text, result.Diagnostics)
		}
	}

	group := func(extra string) string {
		return `<ThreadGroup testname="Thread Group" enabled="true"/><hashTree>` + paritySampler("https", "", extra) + `</hashTree>`
	}
	_, result = convertParity(t, planOfGroups(group(""), group(`<stringProp name="HTTPSampler.response_timeout">${T}</stringProp>`)), ConvertOptions{})
	untimed, variable := reviewDiagnostics(result, "set no connect or response timeout"), reviewDiagnostics(result, "take a timeout from a variable")
	if len(untimed) != 1 || len(variable) != 1 || untimed[0].SourceRef == variable[0].SourceRef {
		t.Fatalf("same-named groups must keep their own disclosures: untimed=%+v variable=%+v", untimed, variable)
	}
	if tls := reviewDiagnostics(result, "certificate verification is turned off"); len(tls) != 2 || tls[0].SourceRef == tls[1].SourceRef {
		t.Fatalf("each same-named group keeps its TLS warning: %+v", tls)
	}
}

// A stop-thread/stop-test group keeps its HTTP disclosures in review evidence
// and counts its gate once, whether or not its name survives sanitizing.
func TestSampleErrorGateKeepsHTTPDisclosures(t *testing.T) {
	for _, name := range []string{"Users", "Billing Users"} {
		convert := func(action string) *ConvertResult {
			plan := strings.Replace(parityPlan(`<stringProp name="ThreadGroup.on_sample_error">`+action+`</stringProp>`, paritySampler("https", "", "")), `testname="Users"`, `testname="`+name+`"`, 1)
			_, result := convertParity(t, plan, ConvertOptions{})
			return result
		}
		continued, stopped := convert("continue"), convert("stoptest")
		for _, element := range []string{"JMeter HTTP TLS", "JMeter HTTP cookies", "JMeter HTTP timeouts"} {
			found := false
			for _, item := range stopped.Diagnostics {
				found = found || (item.Element == element && item.Code == "jmeter_diagnostic:"+element && item.Location == name)
			}
			if !found {
				t.Errorf("%s: review evidence lacks %s located at the group: %+v", name, element, stopped.Diagnostics)
			}
		}
		if stopped.ReviewSummary.BlockingBehaviors != continued.ReviewSummary.BlockingBehaviors+1 {
			t.Errorf("%s: blocking behaviours %d, want %d (one sample-error gate)", name, stopped.ReviewSummary.BlockingBehaviors, continued.ReviewSummary.BlockingBehaviors+1)
		}
	}
}

// A diagnostic about a plan or thread group keeps its own location: a
// configuration contributor's target path names where settings went.
func TestContributorMappingsKeepDiagnosticLocations(t *testing.T) {
	group := func(name string) string {
		return `<ThreadGroup testname="` + name + `" enabled="true"/><hashTree>` + paritySampler("https", "", "") + `</hashTree>`
	}
	_, result := convertParity(t, planOfGroups(group("First"), group("Second")), ConvertOptions{})
	if serial := reviewDiagnostics(result, "one after another"); len(serial) != 1 || serial[0].Location != "Plan" {
		t.Fatalf("serial-groups warning location: %+v", serial)
	}
	for _, tls := range reviewDiagnostics(result, "certificate verification is turned off") {
		if tls.Location != "First" && tls.Location != "Second" {
			t.Fatalf("TLS warning located at %q, want its thread group", tls.Location)
		}
	}
}

// A thread group that converts to no script leaves no mapping, diagnostic
// or per-script warning on the scripts that do exist.
// Plan-level configuration and the Test Plan are mapped once per plan, not once
// per thread group, and a dropped group leaves none of its config mappings,
// even when it comes first.
func TestPlanContributorsMappedOncePerPlan(t *testing.T) {
	headers := func(name string) string {
		return `<HeaderManager testclass="HeaderManager" testname="` + name + `" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp name="" elementType="Header"><stringProp name="Header.name">X-Client</stringProp><stringProp name="Header.value">load</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>`
	}
	group := func(name string) string {
		return `<ThreadGroup testname="` + name + `" enabled="true"/><hashTree>` + paritySampler("https", "", "") + `</hashTree>`
	}
	empty := `<ThreadGroup testname="Empty" enabled="true"/><hashTree>` + headers("InnerHdr") + `</hashTree>`
	cookies := `<CookieManager testclass="CookieManager" testname="Cookies" enabled="true"><boolProp name="CookieManager.clearEachIteration">false</boolProp></CookieManager><hashTree/>`
	_, result := convertParity(t, planOfGroups(cookies, headers("PlanHdr"), empty, group("A"), group("B"), group("C")), ConvertOptions{})
	if len(result.Scripts) != 3 {
		t.Fatalf("scripts = %d, want 3", len(result.Scripts))
	}
	counts := map[string]int{}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetKind == "config" && mapping.TargetPath == "" {
			counts[mapping.Function+"/"+mapping.TargetName]++
		}
	}
	for key, want := range map[string]int{"CookieManager/Cookies": 1, "HeaderManager/PlanHdr": 1, "TestPlan/Plan": 1, "HeaderManager/InnerHdr": 0} {
		if counts[key] != want {
			t.Errorf("%s mappings = %d, want %d (all: %v)", key, counts[key], want, counts)
		}
	}
}

func TestDroppedThreadGroupLeavesNoTraces(t *testing.T) {
	group := func(name string) string {
		return `<ThreadGroup testname="` + name + `" enabled="true"/><hashTree>` + paritySampler("https", "", "") + `</hashTree>`
	}
	setup := `<ThreadGroup testname="Setup" enabled="true"/><hashTree><DebugSampler testname="Debug" enabled="true"/><hashTree/></hashTree>`
	_, result := convertParity(t, planOfGroups(group("A"), group("B"), setup), ConvertOptions{})
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetName == "Setup" && mapping.TargetKind == "config" {
			t.Fatalf("dropped group kept a contributor mapping: %+v", mapping)
		}
	}
	for _, item := range result.Unsupported {
		if item.Location == "Setup" && strings.HasPrefix(item.Element, "JMeter HTTP") {
			t.Fatalf("dropped group kept an HTTP diagnostic: %+v", item)
		}
	}
	for script, warnings := range result.ScriptWarnings {
		for _, warning := range warnings {
			if strings.HasPrefix(warning, "Setup:") {
				t.Fatalf("%s carries the dropped group's warning %q", script.Name, warning)
			}
		}
	}
}
