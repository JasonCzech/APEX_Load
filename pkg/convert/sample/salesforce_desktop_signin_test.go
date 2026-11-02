package sample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

func TestDesktopSalesforceSignInHoistsThroughOneAppAndMovesLogoutToTeardown(t *testing.T) {
	const (
		myDomainHost  = "https://example-perf.sandbox.my.salesforce.com"
		fileHost      = "https://example-perf.sandbox.file.force.com"
		vfHost        = "https://example-perf--c.sandbox.vf.force.com"
		lightningHost = "https://example-perf.sandbox.lightning.force.com"
		frontdoorSID  = "00D000000000001!SyntheticFrontdoorSessionToken123456"
		contentSID    = "00D000000000001!SyntheticContentSessionToken789012"
		vfSID         = "00D000000000001!SyntheticVisualforceSessionToken345"
		cshcVal       = "SyntheticCshcTokenValue987654"
		lmVal         = "SyntheticLmTokenValue12345678901234567890"
	)

	entries := []any{
		recorderEntry("2026-01-01T00:00:00.000Z", "GET", myDomainHost+"/", 200, "text/html", "<html><title>Login</title></html>", nil),
		formRequest(recorderEntry("2026-01-01T00:00:01.000Z", "POST", myDomainHost+"/", 200, "text/html", "<html></html>", nil),
			"un=perf.tester%40example.test&username=perf.tester%40example.test&lt=standard"),
		withResponseHeaders(
			formRequest(recorderEntry("2026-01-01T00:00:02.000Z", "POST", myDomainHost+"/", 302, "text/html", "", nil),
				"un=perf.tester%40example.test&pw=SynthPass%2142&username=perf.tester%40example.test&lt=standard"),
			map[string]any{"name": "Location", "value": myDomainHost + "/secur/frontdoor.jsp?sid=" + strings.ReplaceAll(frontdoorSID, "!", "%21") + "&cshc=" + cshcVal},
		),
		map[string]any{
			"startedDateTime": "2026-01-01T00:00:02.100Z",
			"time":            40,
			"request": map[string]any{
				"method":  "GET",
				"url":     myDomainHost + "/secur/frontdoor.jsp?sid=" + strings.ReplaceAll(frontdoorSID, "!", "%21") + "&cshc=" + cshcVal,
				"headers": []any{},
				"queryString": []any{
					map[string]any{"name": "sid", "value": frontdoorSID},
					map[string]any{"name": "cshc", "value": cshcVal},
				},
			},
			"response": map[string]any{
				"status":  200,
				"headers": []any{map[string]any{"name": "Content-Type", "value": "text/html"}},
				"content": map[string]any{
					"mimeType": "text/html",
					"text":     `<html><form action="` + fileHost + `/secur/contentDoor" method="POST"><input type="hidden" name="lm" value="` + lmVal + `"/><input type="hidden" name="sid" value="` + contentSID + `"/></form></html>`,
				},
			},
		},
		formRequest(recorderEntry("2026-01-01T00:00:02.200Z", "POST", fileHost+"/secur/contentDoor?startURL=%2Fapex%2FVFLoginFlow", 200, "text/html", "<html></html>", nil),
			"lm="+lmVal+"&sid="+strings.ReplaceAll(contentSID, "!", "%21")+"&skipRedirect=1"),
		withResponseHeaders(recorderEntry("2026-01-01T00:00:02.300Z", "GET", myDomainHost+"/apex/VFLoginFlow?retURL=%2Fhome%2Fhome.jsp", 302, "text/html", "", nil),
			map[string]any{"name": "Location", "value": lightningHost + "/visualforce/session?url=%2Fapex%2FVFLoginFlow"}),
		recorderEntry("2026-01-01T00:00:02.400Z", "GET", lightningHost+"/visualforce/session?url=%2Fapex%2FVFLoginFlow", 200, "text/html",
			`<html><form action="`+vfHost+`/visualforce/recsession" method="POST"><input type="hidden" name="sid" value="`+vfSID+`"/></form></html>`, nil),
		formRequest(recorderEntry("2026-01-01T00:00:02.500Z", "POST", vfHost+"/visualforce/recsession", 200, "text/html", "<html></html>", nil),
			"sid="+strings.ReplaceAll(vfSID, "!", "%21")+"&cshc="+cshcVal+"&inst= synthetic"),
		formRequest(recorderEntry("2026-01-01T00:00:02.600Z", "POST", vfHost+"/apex/VFLoginFlow?retURL=%2Fhome%2Fhome.jsp", 200, "text/html", "<html></html>", nil),
			"sid="+strings.ReplaceAll(vfSID, "!", "%21")+"&cshc="+cshcVal),
		recorderEntry("2026-01-01T00:00:02.700Z", "GET", myDomainHost+"/home/home.jsp", 200, "text/html", "<html></html>", nil),
		withResponseHeaders(recorderEntry("2026-01-01T00:00:02.800Z", "GET", myDomainHost+"/one/one.app", 302, "text/html", "", nil),
			map[string]any{"name": "Location", "value": lightningHost + "/one/one.app"}),
		recorderEntry("2026-01-01T00:00:02.900Z", "GET", lightningHost+"/one/one.app", 200, "text/html", `<html><script>var auraConfig={"eikoocnekot":"__Host-ERIC_PROD-999"};</script></html>`, nil),
		// Pure client-telemetry beacon (with body) — should be filtered when FilterClientTelemetry is true.
		formRequest(recorderEntry("2026-01-01T00:00:05.000Z", "POST", lightningHost+"/aura?r=1&ui-instrumentation-components-beacon.InstrumentationBeacon.sendData=1", 200, "application/json", `{"actions":[]}`, nil),
			"message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22serviceComponent%3A%2F%2Fui.instrumentation.components.beacon.InstrumentationBeaconController%2FACTION%24sendData%22%2C%22params%22%3A%7B%7D%7D%5D%7D&aura.pageURI=%2Flightning%2Fn%2FCustomer_Search&aura.token=tok1"),
		// Excluded logger Apex call via profile.ExcludedAuraDescriptors.
		formRequest(recorderEntry("2026-01-01T00:00:05.100Z", "POST", lightningHost+"/aura?r=2&aura.ApexAction.execute=1", 200, "application/json", `{"actions":[]}`, nil),
			"message=%7B%22actions%22%3A%5B%7B%22id%22%3A%222%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FApexActionController%2FACTION%24execute%22%2C%22params%22%3A%7B%22classname%22%3A%22ComponentLogger%22%2C%22method%22%3A%22saveComponentLogEntries%22%7D%7D%5D%7D&aura.pageURI=%2Flightning%2Fn%2FCustomer_Search&aura.token=tok1"),
		// Business Aura POST on Interaction__c view.
		formRequest(recorderEntry("2026-01-01T00:00:06.000Z", "POST", lightningHost+"/aura?r=3&aura.RecordUi.getRecordWithFields=1", 200, "application/json", `{"actions":[{"id":"3;a","state":"SUCCESS"}]}`, nil),
			"message=%7B%22actions%22%3A%5B%7B%22id%22%3A%223%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FRecordUiController%2FACTION%24getRecordWithFields%22%2C%22params%22%3A%7B%7D%7D%5D%7D&aura.pageURI=%2Flightning%2Fr%2FInteraction__c%2Fa3C000000012345EAA%2Fview&aura.token=tok1"),
		// Trailing logout chain -> moves to teardown.
		withResponseHeaders(recorderEntry("2026-01-01T00:00:10.000Z", "GET", lightningHost+"/secur/logout.jsp", 302, "text/html", "", nil),
			map[string]any{"name": "Location", "value": myDomainHost + "/secur/logout.jsp"}),
		recorderEntry("2026-01-01T00:00:10.100Z", "GET", myDomainHost+"/secur/logout.jsp", 200, "text/html", "<html></html>", nil),
	}

	rawBytes, err := json.MarshalIndent(map[string]any{"log": map[string]any{"entries": entries}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	prettyHAR := string(rawBytes)
	origNewlines := strings.Count(prettyHAR, "\n")

	tr := true
	fl := false
	profile := &recordingprofile.Profile{
		ID:                      "sf-clean",
		Name:                    "SF Clean",
		ExcludedAuraDescriptors: []string{"ComponentLogger.saveComponentLogEntries"},
	}
	res, err := Convert(ImportRequest{
		Name:    "Desktop SF Flow",
		Format:  "har",
		Content: prettyHAR,
		Options: ConvertOptions{
			EntryPoint:            EntryPointImportHAR,
			SourceFileName:        "/tmp/uploads/perf-login-baseline.har",
			RedactSourceReview:    &tr,
			SanitizeSensitive:     &fl,
			FilterClientTelemetry: &tr,
			StampActionProvenance: true,
			SalesforceNative:      &fl,
			Profile:               profile,
		},
	})
	if err != nil {
		t.Fatalf("Convert failed: %v", err)
	}

	if len(res.Script.Init) == 0 {
		t.Fatalf("expected desktop sign-in chain in script.Init, got 0 actions")
	}
	if len(res.Script.Teardown) == 0 {
		t.Fatalf("expected trailing /secur/logout.jsp chain in script.Teardown, got 0 actions")
	}
	users := usersDataSource(res.Script)
	if users == nil || users.Strategy != "unique" || !strings.Contains(users.InlineData, "perf.tester@example.test,SynthPass!42") {
		t.Fatalf("unexpected salesforce_users data source: %+v", users)
	}

	// Verify the Location and sid hop extractors use OnMiss: "fail" with nil
	// DefaultValue, and frontdoor.jsp har.request.query_string has correlated
	// templates instead of raw sid.
	foundOneAppInInit := false
	foundFrontdoorInInit := false
	hopExtractors := 0
	for _, act := range res.Script.Init {
		for _, ext := range act.Extractors {
			if !isSalesforceSessionHopExtractor(ext) {
				continue
			}
			hopExtractors++
			if ext.OnMiss != "fail" || ext.DefaultValue != nil {
				t.Fatalf("init hop extractor %q has on_miss=%q default=%v, want fail/nil", ext.Name, ext.OnMiss, ext.DefaultValue)
			}
		}
		if strings.Contains(act.Target, "/one/one.app") {
			foundOneAppInInit = true
		}
		if strings.Contains(act.Target, "/secur/frontdoor.jsp") {
			foundFrontdoorInInit = true
			qsRaw, _ := json.Marshal(act.Metadata["har.request.query_string"])
			if strings.Contains(string(qsRaw), frontdoorSID) {
				t.Fatalf("har.request.query_string retained raw correlated frontdoor sid: %s", string(qsRaw))
			}
		}
	}
	if hopExtractors == 0 {
		t.Fatalf("expected Location/sid hop extractors in Init")
	}
	if !foundOneAppInInit || !foundFrontdoorInInit {
		t.Fatalf("expected frontdoor.jsp and /one/one.app in Init (frontdoor=%v, oneApp=%v)", foundFrontdoorInInit, foundOneAppInInit)
	}

	// Verify Actions contains only the business Aura step (+ its step transaction markers),
	// with telemetry beacon and ComponentLogger omitted, and generic HAR /*ERROR*/ guard attached.
	businessReqs := 0
	foundInteractionTx := false
	for _, act := range res.Script.Actions {
		if act.Type == "source_transaction" && strings.Contains(act.Name, "Interaction__c_view") {
			foundInteractionTx = true
		}
		if act.Type != "request" {
			continue
		}
		businessReqs++
		if strings.Contains(act.Target, "InstrumentationBeacon") || strings.Contains(act.Body, "ComponentLogger") {
			t.Fatalf("telemetry/logger action was not filtered: %s %s", act.Target, act.Body)
		}
		uid, _ := act.Metadata[dsl.ImportActionUIDMetadataKey].(string)
		if !dsl.IsValidImportActionUID(uid) {
			t.Fatalf("missing valid import_action_uid on converted HAR action: %#v", act.Metadata)
		}
		hasErrorGuard := false
		for _, assertion := range act.Assertions {
			if assertion.Type == "body_not_contains" && assertion.Value == "/*ERROR*/" {
				hasErrorGuard = true
			}
		}
		if !hasErrorGuard {
			t.Fatalf("expected generic HAR /aura /*ERROR*/ assertion on %s, got %#v", act.Target, act.Assertions)
		}
	}
	if businessReqs != 1 {
		t.Fatalf("expected exactly 1 business request in Actions, got %d", businessReqs)
	}
	if !foundInteractionTx {
		t.Fatalf("expected default Salesforce step transaction named Interaction__c_view in Actions: %#v", res.Script.Actions)
	}

	// Verify SourceReview uses real basename, is marked Redacted, scrubs pw & sid,
	// and preserves line breaks so line mappings remain valid.
	if res.SourceReview == nil || len(res.SourceReview.SourceFiles) != 1 {
		t.Fatalf("unexpected SourceReview: %#v", res.SourceReview)
	}
	sf := res.SourceReview.SourceFiles[0]
	if sf.Path != "perf-login-baseline.har" || !sf.Redacted {
		t.Fatalf("SourceFile path=%q redacted=%v, want perf-login-baseline.har / true", sf.Path, sf.Redacted)
	}
	if strings.Contains(sf.Content, "SynthPass") || strings.Contains(sf.Content, frontdoorSID) || strings.Contains(sf.Content, contentSID) {
		t.Fatalf("SourceReview leaked raw credentials or session IDs")
	}
	if gotNewlines := strings.Count(sf.Content, "\n"); gotNewlines != origNewlines {
		t.Fatalf("SourceReview collapsed line breaks: got %d newlines, want %d", gotNewlines, origNewlines)
	}

	// Verify source mappings point into init[...], actions[...], and teardown[...].
	var hasInitMap, hasActionMap, hasTeardownMap bool
	for _, m := range res.SourceReview.Mappings {
		hasInitMap = hasInitMap || strings.HasPrefix(m.TargetPath, "init[")
		hasActionMap = hasActionMap || strings.HasPrefix(m.TargetPath, "actions[")
		hasTeardownMap = hasTeardownMap || strings.HasPrefix(m.TargetPath, "teardown[")
	}
	if !hasInitMap || !hasActionMap || !hasTeardownMap {
		t.Fatalf("expected remapped SourceReview paths across init/actions/teardown (init=%v action=%v teardown=%v)", hasInitMap, hasActionMap, hasTeardownMap)
	}
}

// A Classic (or Visualforce-only) capture never reaches /aura. The sign-in
// chain ends at the first business page, so a later Home tab visit or
// session handoff does not pull the business flow into init.
func TestDesktopSalesforceSignInChainStopsAtTheFirstBusinessPage(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	exchanges := []exchange{
		ok("GET", "/"),
		{
			Method: "POST", URL: host + "/", Body: "un=a%40example.test&pw=secret",
			Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}},
		},
		ok("GET", "/secur/frontdoor.jsp?sid=x"),
		ok("GET", "/home/home.jsp"),
		ok("GET", "/001/o"),
		ok("GET", "/001000000000001"),
		ok("GET", "/home/home.jsp"),
		ok("GET", "/001/o"),
		ok("GET", "/secur/logout.jsp"),
	}
	signIn := findDesktopSalesforceSignIn(exchanges)
	if signIn == nil {
		t.Fatal("desktop sign-in not found")
	}
	if signIn.token != 3 {
		t.Fatalf("sign-in ends at exchange %d, want 3 (the first home.jsp)", signIn.token)
	}
	if signIn.logoutStart != 8 {
		t.Fatalf("logoutStart = %d, want 8", signIn.logoutStart)
	}

	// The scripts and images a login-flow page loads (kept when static assets
	// are included) do not end the chain before the flow's own hops.
	flow := []exchange{
		exchanges[0], exchanges[1], exchanges[2],
		ok("GET", "/apex/CustomLoginFlow"),
		ok("GET", "/resource/1700000000000/flowjs/app.js"),
		{Method: "GET", URL: host + "/img/logo", Response: &response{Status: 200, Headers: map[string]string{"Content-Type": "image/png"}}},
		{Method: "POST", URL: host + "/apex/CustomLoginFlow", Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/home/home.jsp"}}},
		ok("GET", "/home/home.jsp"),
		ok("GET", "/001/o"),
	}
	signIn = findDesktopSalesforceSignIn(flow)
	if signIn == nil || signIn.token != 7 {
		t.Fatalf("login-flow sign-in = %+v, want it to end at exchange 7 (home.jsp)", signIn)
	}
}

// Only the Location and session-ID hops of a desktop sign-in are forced to
// fail on a miss. A less certain correlation on served content keeps its
// recorded fallback, and so does a step an open transaction carried into init.
func TestDesktopSalesforceSignInForcesFailOnlyOnSessionHops(t *testing.T) {
	recorded := "recorded"
	soft := func(name, source, expression string) dsl.Extractor {
		value := recorded
		return dsl.Extractor{Name: name, Source: source, Type: "regex", Expression: expression, OnMiss: "default", DefaultValue: &value}
	}
	marker := func(function string) dsl.Action {
		return dsl.Action{Type: "source_transaction", Params: map[string]any{"function": function, "args": []any{"Login"}}}
	}
	script := &dsl.Script{Actions: []dsl.Action{
		{Type: "http", Method: "GET", Target: "/promos.html", Extractors: []dsl.Extractor{
			soft("corr_banner_id", "body", `id="(\w+)"`),
			{Name: "corr_location", Source: "header", Type: "header", Expression: "Location", OnMiss: "default", DefaultValue: &recorded},
		}},
		marker("lr_start_transaction"),
		{Type: "http", Method: "GET", Target: "/secur/frontdoor.jsp", Extractors: []dsl.Extractor{soft("corr_sid", "body", `name="sid" value="([^"]+)"`)}},
		{Type: "http", Method: "GET", Target: "/001/o", Extractors: []dsl.Extractor{soft("corr_sid_2", "body", `sid=([^&]+)`)}},
		marker("lr_end_transaction"),
		{Type: "http", Method: "GET", Target: "/002/o"},
	}}
	exchanges := []exchange{{Method: "GET"}, {Method: "GET"}, {Method: "GET"}, {Method: "GET"}}
	signIn := &salesforceSignIn{token: 1, desktop: true}
	_, _, boundary, why := signIn.moveToInit(script, exchanges, []int{0, 2, 3, 5}, nil)
	if why != "" || boundary != 5 {
		t.Fatalf("boundary = %d, why = %q; want 5 and no reason", boundary, why)
	}
	got := map[string]string{}
	for _, action := range script.Init {
		for _, extractor := range action.Extractors {
			got[extractor.Name] = extractor.OnMiss
			if extractor.OnMiss == "fail" && extractor.DefaultValue != nil {
				t.Fatalf("%s fails on a miss but keeps a default", extractor.Name)
			}
		}
	}
	want := map[string]string{"corr_banner_id": "default", "corr_location": "fail", "corr_sid": "fail", "corr_sid_2": "default"}
	for name, policy := range want {
		if got[name] != policy {
			t.Fatalf("%s on_miss = %q, want %q (all: %v)", name, got[name], policy, got)
		}
	}
}
