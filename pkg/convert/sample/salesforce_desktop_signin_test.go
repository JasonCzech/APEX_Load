package sample

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
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

// A login flow's Visualforce page may have any name, and it may call Visualforce
// remoting before the session handoff. Before the home page they are part of
// the sign-in; after it a Visualforce page is a business step.
func TestDesktopSalesforceSignInFollowsACustomLoginFlowPage(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	signedIn := []exchange{
		ok("GET", "/"),
		{
			Method: "POST", URL: host + "/", Body: "un=a%40example.test&pw=secret",
			Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}},
		},
		ok("GET", "/secur/frontdoor.jsp?sid=x"),
		ok("POST", "/secur/contentDoor"),
	}
	flow := append(append([]exchange(nil), signedIn...),
		ok("GET", "/apex/TermsAcceptance"),
		ok("POST", "/apexremote"),
		ok("GET", "/visualforce/session"),
		ok("GET", "/home/home.jsp"),
		ok("GET", "/one/one.app"),
		ok("POST", "/aura"),
	)
	signIn := findDesktopSalesforceSignIn(flow)
	if signIn == nil || signIn.token != 8 {
		t.Fatalf("custom login-flow sign-in = %+v, want it to end at exchange 8 (one.app)", signIn)
	}

	// Once the home page loads, a Visualforce page and a later handoff are the
	// business flow.
	business := append(append([]exchange(nil), signedIn...),
		ok("GET", "/home/home.jsp"),
		ok("GET", "/apex/AccountView"),
		ok("GET", "/visualforce/session"),
		ok("GET", "/home/home.jsp"),
	)
	if signIn = findDesktopSalesforceSignIn(business); signIn == nil || signIn.token != 4 {
		t.Fatalf("business Visualforce sign-in = %+v, want it to end at exchange 4 (home.jsp)", signIn)
	}

	// A login-flow-shaped page with no sign-in hop after it ends the chain,
	// and so does one too far from the last hop.
	unfinished := append(append([]exchange(nil), signedIn...),
		ok("GET", "/apex/LandingPage"),
		ok("GET", "/001/o"),
		ok("GET", "/home/home.jsp"),
	)
	if signIn = findDesktopSalesforceSignIn(unfinished); signIn == nil || signIn.token != 3 {
		t.Fatalf("unfinished login-flow sign-in = %+v, want it to end at exchange 3 (contentDoor)", signIn)
	}
	far := append([]exchange(nil), signedIn...)
	for range salesforceLoginFlowReach + 1 {
		far = append(far, ok("POST", "/apexremote"))
	}
	far = append(far, ok("GET", "/home/home.jsp"))
	if signIn = findDesktopSalesforceSignIn(far); signIn == nil || signIn.token != 3 {
		t.Fatalf("distant login-flow sign-in = %+v, want it to end at exchange 3 (contentDoor)", signIn)
	}
}

// A token grant that signs nobody in (an embedded app renewing its token)
// does not hide the browser sign-in; an authorization-code exchange the
// Mobile SDK detector cannot place still does.
func TestDesktopSalesforceSignInIgnoresATokenRefresh(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	withToken := func(body string) []exchange {
		return []exchange{
			ok("GET", "/"),
			{
				Method: "POST", URL: host + "/", Body: "un=a%40example.test&pw=secret",
				Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}},
			},
			ok("GET", "/secur/frontdoor.jsp?sid=x"),
			ok("GET", "/one/one.app"),
			{Method: "POST", URL: host + "/services/oauth2/token", Body: body, Response: &response{Status: 200}},
			ok("POST", "/aura"),
		}
	}
	for _, grant := range []string{
		"grant_type=refresh_token&refresh_token=r&client_id=c",
		"grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Ajwt-bearer&assertion=a",
		"grant_type=client_credentials",
	} {
		signIn := findSalesforceSignIn(withToken(grant))
		if signIn == nil || !signIn.desktop || signIn.token != 3 {
			t.Fatalf("%s: sign-in = %+v, want the desktop chain ending at one.app", grant, signIn)
		}
	}
	for _, code := range []string{
		"grant_type=authorization_code&code=c&redirect_uri=https%3A%2F%2Fapp.example.test%2Fcallback",
		"code=c&redirect_uri=x",
	} {
		if signIn := findSalesforceSignIn(withToken(code)); signIn != nil {
			t.Fatalf("%s: sign-in = %+v, want none beside an unplaced code exchange", code, signIn)
		}
	}
}

// disable_salesforce asks for a generic script: the recorded desktop sign-in
// and sign-out stay in the iteration, and no users file is added.
func TestDesktopSalesforceSignInHonorsDisableSalesforce(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	entries := []any{
		recorderEntry("2026-01-01T00:00:00.000Z", "GET", host+"/", 200, "text/html", "<html></html>", nil),
		withResponseHeaders(
			formRequest(recorderEntry("2026-01-01T00:00:01.000Z", "POST", host+"/", 302, "text/html", "", nil), "un=a%40example.test&pw=Synth1"),
			map[string]any{"name": "Location", "value": host + "/secur/frontdoor.jsp?sid=x"},
		),
		recorderEntry("2026-01-01T00:00:01.100Z", "GET", host+"/secur/frontdoor.jsp?sid=x", 200, "text/html", "<html></html>", nil),
		recorderEntry("2026-01-01T00:00:01.200Z", "GET", host+"/one/one.app", 200, "text/html", "<html></html>", nil),
		recorderEntry("2026-01-01T00:00:05.000Z", "GET", host+"/lightning/o/Account/list", 200, "text/html", "<html></html>", nil),
		formRequest(recorderEntry("2026-01-01T00:00:06.000Z", "POST", host+"/secur/forgotpassword.jsp", 200, "text/html", "<html></html>", nil), "un=a%40example.test"),
		recorderEntry("2026-01-01T00:00:07.000Z", "GET", host+"/_ui/identity/verification/method/SmsVerificationFinishUi/e", 200, "text/html", "<html></html>", nil),
		recorderEntry("2026-01-01T00:00:09.000Z", "GET", host+"/secur/logout.jsp", 200, "text/html", "<html></html>", nil),
	}
	raw, err := json.Marshal(map[string]any{"log": map[string]any{"entries": entries}})
	if err != nil {
		t.Fatal(err)
	}
	convert := func(disable *bool) *ConvertResult {
		t.Helper()
		res, err := Convert(ImportRequest{
			Name: "Desktop", Format: "har", Content: string(raw),
			Options: ConvertOptions{EntryPoint: EntryPointImportHAR, DisableSalesforce: disable},
		})
		if err != nil {
			t.Fatalf("Convert failed: %v", err)
		}
		return res
	}
	if res := convert(nil); len(res.Script.Init) == 0 || usersDataSource(res.Script) == nil {
		t.Fatalf("default conversion did not hoist the desktop sign-in: init=%d", len(res.Script.Init))
	}
	disabled := true
	res := convert(&disabled)
	if len(res.Script.Init) != 0 || len(res.Script.Teardown) != 0 || usersDataSource(res.Script) != nil {
		t.Fatalf("disable_salesforce still hoisted the sign-in: init=%d teardown=%d users=%+v", len(res.Script.Init), len(res.Script.Teardown), usersDataSource(res.Script))
	}
	// The steps that act on the org at load are still gated.
	gated := map[string]string{}
	for _, action := range res.Script.Actions {
		if codes, ok := action.Metadata["execution_blockers"]; ok {
			gated[action.Target] = fmt.Sprint(codes)
		}
	}
	for target, reason := range map[string]string{
		"/secur/forgotpassword.jsp":                                   salesforcePasswordResetReasonCode,
		"/_ui/identity/verification/method/SmsVerificationFinishUi/e": salesforceIdentityVerificationReasonCode,
	} {
		found := false
		for gatedTarget, codes := range gated {
			found = found || (strings.HasSuffix(gatedTarget, target) && strings.Contains(codes, reason))
		}
		if !found {
			t.Fatalf("disable_salesforce: %s not gated with %s (gates: %v)", target, reason, gated)
		}
	}
}

func TestHasOpenBackgroundConversation(t *testing.T) {
	start := func(id, pattern string) dsl.Action {
		return dsl.Action{Type: "http_async_start", Params: map[string]any{"id": id, "pattern": pattern}}
	}
	stop := func(id string) dsl.Action {
		return dsl.Action{Type: "http_async_stop", Params: map[string]any{"id": id}}
	}
	for name, test := range map[string]struct {
		actions []dsl.Action
		want    bool
	}{
		"none":          {nil, false},
		"once":          {[]dsl.Action{start("a", "once")}, false},
		"stopped":       {[]dsl.Action{start("a", "repeat"), stop("a")}, false},
		"left running":  {[]dsl.Action{start("a", "repeat"), start("b", "long_poll"), stop("a")}, true},
		"stop the poll": {[]dsl.Action{start("a", "long_poll"), stop("a")}, false},
	} {
		if got := hasOpenBackgroundConversation(test.actions); got != test.want {
			t.Fatalf("%s: hasOpenBackgroundConversation = %v, want %v", name, got, test.want)
		}
	}
}

// A Visualforce page reached from frontdoor (a startURL) is a business page
// when the chain leads on to a second page or never lands: neither it nor
// its remoting calls join the sign-in.
func TestDesktopSalesforceSignInKeepsAStartURLPageInTheIteration(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	signedIn := []exchange{
		ok("GET", "/"),
		{
			Method: "POST", URL: host + "/", Body: "un=a%40example.test&pw=secret",
			Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}},
		},
		ok("GET", "/secur/frontdoor.jsp?sid=x"),
	}
	secondPage := append(append([]exchange(nil), signedIn...),
		ok("GET", "/apex/OrderEntry"),
		ok("POST", "/apexremote"),
		ok("POST", "/apexremote"),
		exchange{Method: "GET", URL: host + "/apex/pkg__Catalog", Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/visualforce/session"}}},
		ok("GET", "/visualforce/session"),
		ok("GET", "/apex/pkg__Catalog"),
		ok("GET", "/home/home.jsp"),
	)
	if signIn := findDesktopSalesforceSignIn(secondPage); signIn == nil || signIn.token != 2 {
		t.Fatalf("startURL page then a second page: sign-in = %+v, want it to end at exchange 2 (frontdoor)", signIn)
	}
	handoffOnly := append(append([]exchange(nil), signedIn...),
		ok("GET", "/apex/OrderEntry"),
		ok("POST", "/apexremote"),
		ok("GET", "/visualforce/session"),
		ok("GET", "/001/o"),
	)
	if signIn := findDesktopSalesforceSignIn(handoffOnly); signIn == nil || signIn.token != 2 {
		t.Fatalf("startURL page then a session handoff: sign-in = %+v, want it to end at exchange 2 (frontdoor)", signIn)
	}
}

// A Classic sign-out followed by a second sign-in is not a trailing sign-out:
// the second session's steps stay in the iteration, which ends with the
// session the second sign-in made, so nothing moves and nothing warns of a
// dead session.
func TestDesktopSalesforceLogoutBeforeASecondSignInStays(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	login := func(user string) exchange {
		return exchange{
			Method: "POST", URL: host + "/", Body: "un=" + user + "%40example.test&pw=secret",
			Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}},
		}
	}
	exchanges := []exchange{
		ok("GET", "/"), login("a"), ok("GET", "/secur/frontdoor.jsp?sid=x"), ok("GET", "/home/home.jsp"),
		ok("GET", "/001/o"),
		ok("GET", "/secur/logout.jsp"),
		ok("GET", "/"), login("b"), ok("GET", "/secur/frontdoor.jsp?sid=x"), ok("GET", "/home/home.jsp"),
		ok("GET", "/001/o"), ok("GET", "/001000000000001"),
	}
	signIn := findDesktopSalesforceSignIn(exchanges)
	if signIn == nil || signIn.token != 3 {
		t.Fatalf("sign-in = %+v, want it to end at exchange 3", signIn)
	}
	if signIn.logoutStart != -1 || signIn.logoutStays != "" {
		t.Fatalf("logoutStart = %d, logoutStays = %q; want -1 and no reason", signIn.logoutStart, signIn.logoutStays)
	}
	script := &dsl.Script{Init: []dsl.Action{{Type: "http"}}, Actions: []dsl.Action{{Type: "http"}, {Type: "http"}, {Type: "http"}}}
	_, _, moved, _, reason := signIn.moveLogoutToTeardown(script, exchanges, []int{-1, -1, -1, -1, 0, 1, 2, -1, -1, -1, -1, -1}, nil)
	if moved != 0 || len(script.Teardown) != 0 || reason != "" {
		t.Fatalf("moved = %d, teardown = %d, reason = %q; want nothing moved and no reason", moved, len(script.Teardown), reason)
	}

	// The second sign-in, replayed every iteration, reads the users file as
	// the first does: no recorded name or password stays in its body, and it
	// is not taken for a failed attempt.
	signIn.prepareExchanges(exchanges)
	for _, index := range []int{1, 7} {
		body := exchanges[index].Body
		if strings.Contains(body, "secret") || strings.Contains(body, "example.test") || !strings.Contains(body, "{{data."+salesforceUsersDataSource+".password}}") {
			t.Fatalf("login %d body = %q, want it to read the users file", index, body)
		}
		if len(exchanges[index].ReviewFindings) != 0 {
			t.Fatalf("login %d findings = %+v, want none", index, exchanges[index].ReviewFindings)
		}
	}
	signIn.restoreRecordedBodies(exchanges)
	if !strings.Contains(exchanges[7].Body, "pw=secret") {
		t.Fatalf("recorded body = %q, want it restored for the baseline", exchanges[7].Body)
	}

	// A second sign-in whose form cannot read the users file is gated for
	// removal rather than replaying the recorded credentials.
	unreadable := append([]exchange(nil), exchanges...)
	unreadable[7].Body = "un=b%40example.test&pw=secret&pw=again"
	signIn = findDesktopSalesforceSignIn(unreadable)
	signIn.prepareExchanges(unreadable)
	if findings := unreadable[7].ReviewFindings; len(findings) != 1 || findings[0].ReasonCode != salesforceRecordedSignInReasonCode {
		t.Fatalf("findings = %+v, want the recorded sign-in gated", findings)
	}
}

// A Lightning recording that signs out, signs in again and signs out at the
// end moves only the final sign-out to teardown: iteration 2 starts with the
// session the second sign-in made. A telemetry beacon the page fires as it
// unloads is not traffic that keeps a sign-out in the iteration.
func TestDesktopSalesforceLogoutAfterASecondSignInMovesTheFinalSignOut(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	login := exchange{
		Method: "POST", URL: host + "/", Body: "un=a%40example.test&pw=secret",
		Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}},
	}
	beacon := ok("POST", "/aura?r=9&ui-instrumentation-components-beacon.InstrumentationBeacon.sendData=1")
	exchanges := []exchange{
		ok("GET", "/"), login, ok("GET", "/secur/frontdoor.jsp?sid=x"), ok("GET", "/one/one.app"),
		ok("POST", "/aura?r=1&ui-force-components-controllers-recordGlobalValueProvider.RecordGvp.getRecord=1"),
		ok("GET", "/secur/logout.jsp"), beacon, ok("GET", "/"),
		login, ok("GET", "/secur/frontdoor.jsp?sid=y"), ok("GET", "/one/one.app"),
		ok("POST", "/aura?r=2&ui-force-components-controllers-recordGlobalValueProvider.RecordGvp.getRecord=1"),
		ok("GET", "/secur/logout.jsp"), beacon, ok("GET", "/"),
	}
	signIn := findDesktopSalesforceSignIn(exchanges)
	if signIn == nil || signIn.token != 3 {
		t.Fatalf("sign-in = %+v, want it to end at exchange 3", signIn)
	}
	if signIn.logoutStart != 12 || signIn.logoutStays != "" {
		t.Fatalf("logoutStart = %d, logoutStays = %q; want the final sign-out (12) and no reason", signIn.logoutStart, signIn.logoutStays)
	}

	// Beacons after a single trailing sign-out do not keep it either.
	single := append(append([]exchange(nil), exchanges[:5]...), exchanges[12:]...)
	if signIn = findDesktopSalesforceSignIn(single); signIn == nil || signIn.logoutStart != 5 || signIn.logoutStays != "" {
		t.Fatalf("single sign-out: sign-in = %+v, want the sign-out at exchange 5 to move", signIn)
	}
	// Business Aura traffic after the final sign-out still keeps it, with a
	// reason that does not blame the session init made.
	business := append(append([]exchange(nil), exchanges...), exchanges[11])
	signIn = findDesktopSalesforceSignIn(business)
	if signIn == nil || signIn.logoutStart != -1 || !strings.Contains(signIn.logoutStays, "traffic follows") {
		t.Fatalf("business after sign-out: sign-in = %+v, want no move and a reason", signIn)
	}
	if warnings := logoutWarnings(0, nil, signIn.logoutStays); len(warnings) != 1 || strings.Contains(warnings[0], "init signed in") {
		t.Fatalf("warnings = %q", warnings)
	}
}

// A sign-in again that failed while recording (the login page came back
// before the attempt that redirected) is gated as a failed attempt, as one
// before the first sign-in is; a later sign-in as a different recorded user
// is replayed as the users-file user and says so.
func TestDesktopSalesforceFailedOrOtherUserSignInAgainSaysSo(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	login := func(user string, status int) exchange {
		ex := exchange{Method: "POST", URL: host + "/", Body: "un=" + user + "%40example.test&pw=secret", Response: &response{Status: status}}
		if status == 302 {
			ex.Response.Headers = map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}
		}
		return ex
	}
	exchanges := []exchange{
		ok("GET", "/"), login("a", 302), ok("GET", "/secur/frontdoor.jsp?sid=x"), ok("GET", "/home/home.jsp"),
		ok("GET", "/001/o"),
		ok("GET", "/secur/logout.jsp"),
		ok("GET", "/"), login("a", 200), login("a", 302), ok("GET", "/secur/frontdoor.jsp?sid=x"), ok("GET", "/home/home.jsp"),
		ok("GET", "/001/o"),
	}
	signIn := findDesktopSalesforceSignIn(exchanges)
	signIn.prepareExchanges(exchanges)
	if findings := exchanges[7].ReviewFindings; len(findings) != 1 || findings[0].ReasonCode != salesforceFailedSignInReasonCode {
		t.Fatalf("failed attempt findings = %+v, want it gated as a failed attempt", findings)
	}
	if findings := exchanges[8].ReviewFindings; len(findings) != 0 {
		t.Fatalf("sign-in again findings = %+v, want none", findings)
	}
	for _, warning := range signIn.warnings(1, "") {
		if strings.Contains(warning, "different user") {
			t.Fatalf("same-user sign-in again warned: %q", warning)
		}
	}

	other := append([]exchange(nil), exchanges...)
	other[7], other[8] = ok("GET", "/"), login("b", 302)
	signIn = findDesktopSalesforceSignIn(other)
	signIn.prepareExchanges(other)
	if body := other[8].Body; strings.Contains(body, "example.test") {
		t.Fatalf("other user's body = %q, want it to read the users file", body)
	}
	warned := false
	for _, warning := range signIn.warnings(1, "") {
		warned = warned || (strings.Contains(warning, "different user") && !strings.Contains(warning, "example.test"))
	}
	if !warned {
		t.Fatalf("warnings = %q, want the other-user sign-in named without the recorded user", signIn.warnings(1, ""))
	}

	// The same user typed in another case, or a last attempt that came back
	// to the login page, is no second user.
	for name, last := range map[string]exchange{"case": login("A", 302), "failed last attempt": login("b", 200)} {
		same := []exchange{
			ok("GET", "/"), login("a", 302), ok("GET", "/secur/frontdoor.jsp?sid=x"), ok("GET", "/home/home.jsp"),
			ok("GET", "/001/o"),
			ok("GET", "/secur/logout.jsp"),
			ok("GET", "/"), ok("GET", "/"), last, ok("GET", "/secur/frontdoor.jsp?sid=x"), ok("GET", "/home/home.jsp"),
			ok("GET", "/001/o"),
		}
		signIn = findDesktopSalesforceSignIn(same)
		signIn.prepareExchanges(same)
		if signIn.otherUser != 0 {
			t.Fatalf("%s: otherUser = %d, want 0 (warnings %q)", name, signIn.otherUser, signIn.warnings(1, ""))
		}
	}
}

// A Classic org can land on a Visualforce tab after frontdoor. When the user
// then clicks Home, the page handed nobody on (no redirect, handoff or post
// back), so it is a business page and stays in the iteration.
func TestDesktopSalesforceSignInKeepsAVisualforceLandingTabInTheIteration(t *testing.T) {
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
		ok("GET", "/apex/AccountDashboard"),
		ok("POST", "/apexremote"),
		ok("GET", "/apex/AccountDashboard"),
		ok("GET", "/home/home.jsp"),
	}
	if signIn := findDesktopSalesforceSignIn(exchanges); signIn == nil || signIn.token != 2 {
		t.Fatalf("Visualforce landing tab: sign-in = %+v, want it to end at exchange 2 (frontdoor)", signIn)
	}
	// A login flow page whose form posts back and redirects before the home
	// page joins.
	finish := exchange{Method: "POST", URL: host + "/apex/AccountDashboard", Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/home/home.jsp"}}}
	posted := append(append([]exchange(nil), exchanges[:4]...), finish, ok("GET", "/home/home.jsp"))
	if signIn := findDesktopSalesforceSignIn(posted); signIn == nil || signIn.token != 5 {
		t.Fatalf("posted login flow: sign-in = %+v, want it to end at exchange 5 (home.jsp)", signIn)
	}
	// The tab's own form posting back (a search answering 200) or a servlet
	// redirect it loads hands nobody on: the tab stays in the iteration.
	download := exchange{Method: "GET", URL: host + "/servlet/servlet.FileDownload?file=015x", Response: &response{Status: 302, Headers: map[string]string{"Content-Type": "text/html", "Location": host + "/sfc/servlet.shepherd/version/download/068x"}}}
	for name, step := range map[string]exchange{"postback": ok("POST", "/apex/AccountDashboard"), "servlet redirect": download} {
		tab := append(append([]exchange(nil), exchanges[:4]...), step, ok("GET", "/home/home.jsp"))
		if signIn := findDesktopSalesforceSignIn(tab); signIn == nil || signIn.token != 2 {
			t.Fatalf("Visualforce tab %s: sign-in = %+v, want it to end at exchange 2 (frontdoor)", name, signIn)
		}
	}
}

// A transaction the recording opened before the sign-out (the avatar menu
// clicks that lead to it, or a whole business flow) keeps the steps it times
// in the iteration and ends before the sign-out, so no business step runs
// once per virtual user. When the sign-out cannot move, the reason is
// returned so the tester is warned.
func TestDesktopSalesforceLogoutMovesItsOpenTransactionOrSaysWhy(t *testing.T) {
	marker := func(function, name string) dsl.Action {
		return dsl.Action{Type: "source_transaction", Params: map[string]any{"function": function, "args": []any{name}}}
	}
	http := func(target string) dsl.Action { return dsl.Action{Type: "http", Method: "GET", Target: target} }
	targets := func(actions []dsl.Action) []string {
		out := make([]string, len(actions))
		for index, action := range actions {
			if action.Type == "source_transaction" {
				function, _ := action.Params["function"].(string)
				out[index] = function + ":" + dsl.TransactionMarkerName(action)
				continue
			}
			out[index] = action.Target
		}
		return out
	}
	exchanges := make([]exchange, 4)
	signIn := &salesforceSignIn{desktop: true, logoutStart: 2}

	script := &dsl.Script{Init: []dsl.Action{http("/login")}, Actions: []dsl.Action{
		marker("lr_start_transaction", "Search"), http("/001/o"), marker("lr_end_transaction", "Search"),
		marker("lr_start_transaction", "Logout"), http("/aura"), http("/secur/logout.jsp"), http("/"), marker("lr_end_transaction", "Logout"),
	}}
	mappings := []sourceview.Mapping{{TargetPath: "actions[4]"}, {TargetPath: "actions[1]"}, {TargetPath: "actions[5]"}, {TargetPath: "actions[7]"}}
	newIndex, mappings, moved, carried, reason := signIn.moveLogoutToTeardown(script, exchanges, []int{1, 4, 5, 6}, mappings)
	if reason != "" || moved != 2 || len(carried) != 1 || carried[0] != "Logout" {
		t.Fatalf("moved = %d, carried = %v, reason = %q; want the sign-out alone in teardown and Logout named", moved, carried, reason)
	}
	if got, want := strings.Join(targets(script.Actions), " "), "lr_start_transaction:Search /001/o lr_end_transaction:Search lr_start_transaction:Logout /aura lr_end_transaction:Logout"; got != want {
		t.Fatalf("actions = %s, want %s", got, want)
	}
	if got, want := strings.Join(targets(script.Teardown), " "), "/secur/logout.jsp /"; got != want {
		t.Fatalf("teardown = %s, want %s", got, want)
	}
	if newIndex[0] != 1 || newIndex[1] != 4 || newIndex[2] != -1 ||
		mappings[0].TargetPath != "actions[4]" || mappings[1].TargetPath != "actions[1]" ||
		mappings[2].TargetPath != "teardown[0]" || mappings[3].TargetPath != "actions[5]" {
		t.Fatalf("newIndex = %v, mappings = %+v", newIndex, mappings)
	}
	if warnings := logoutWarnings(moved, carried, reason); len(warnings) != 1 || !strings.Contains(warnings[0], `"Logout"`) || !strings.Contains(warnings[0], "stay in every iteration") {
		t.Fatalf("warnings = %q", warnings)
	}

	// A transaction around the whole business flow (nested ones included)
	// keeps that flow in the iteration: it is not dragged into teardown.
	main := &dsl.Script{Init: []dsl.Action{http("/login")}, Actions: []dsl.Action{
		http("/home"),
		marker("lr_start_transaction", "Main"), http("/001/o"),
		marker("lr_start_transaction", "Close case and log out"), http("/500/x"), http("/secur/logout.jsp"), http("/"),
		marker("lr_end_transaction", "Close case and log out"), marker("lr_end_transaction", "Main"),
	}}
	mainMappings := []sourceview.Mapping{{TargetPath: "actions[7]"}, {TargetPath: "actions[8]"}}
	_, mainMappings, moved, carried, reason = signIn.moveLogoutToTeardown(main, exchanges, []int{2, 4, 5, 6}, mainMappings)
	if reason != "" || moved != 2 || len(carried) != 2 {
		t.Fatalf("moved = %d, carried = %v, reason = %q; want the sign-out alone in teardown", moved, carried, reason)
	}
	// Each removed end marker's mapping follows its own transaction's
	// inserted end marker, the inner one first.
	if mainMappings[0].TargetPath != "actions[5]" || mainMappings[1].TargetPath != "actions[6]" {
		t.Fatalf("mappings = %+v, want the inner end at actions[5] and Main's at actions[6]", mainMappings)
	}
	if got, want := strings.Join(targets(main.Actions), " "), "/home lr_start_transaction:Main /001/o lr_start_transaction:Close case and log out /500/x lr_end_transaction:Close case and log out lr_end_transaction:Main"; got != want {
		t.Fatalf("actions = %s, want %s", got, want)
	}
	if got, want := strings.Join(targets(main.Teardown), " "), "/secur/logout.jsp /"; got != want {
		t.Fatalf("teardown = %s, want %s", got, want)
	}

	// A transaction around the whole iteration ends before the sign-out too.
	whole := &dsl.Script{Init: []dsl.Action{http("/login")}, Actions: []dsl.Action{
		marker("lr_start_transaction", "Flow"), http("/001/o"), http("/secur/logout.jsp"), marker("lr_end_transaction", "Flow"),
	}}
	_, _, moved, carried, reason = signIn.moveLogoutToTeardown(whole, exchanges, []int{-1, 1, 2, -1}, nil)
	if reason != "" || moved != 1 || len(carried) != 1 || carried[0] != "Flow" {
		t.Fatalf("moved = %d, carried = %v, reason = %q; want the sign-out in teardown", moved, carried, reason)
	}
	if got, want := strings.Join(targets(whole.Actions), " "), "lr_start_transaction:Flow /001/o lr_end_transaction:Flow"; got != want {
		t.Fatalf("actions = %s, want %s", got, want)
	}

	// A sign-out with nothing but markers before it cannot move.
	bare := &dsl.Script{Init: []dsl.Action{http("/login")}, Actions: []dsl.Action{
		marker("lr_start_transaction", "Flow"), http("/secur/logout.jsp"), marker("lr_end_transaction", "Flow"),
	}}
	_, _, moved, _, reason = signIn.moveLogoutToTeardown(bare, exchanges, []int{-1, -1, 1, -1}, nil)
	if moved != 0 || len(bare.Teardown) != 0 || reason == "" {
		t.Fatalf("moved = %d, reason = %q; want nothing moved and a reason", moved, reason)
	}
	if warnings := logoutWarnings(moved, nil, reason); len(warnings) != 1 || !strings.Contains(warnings[0], "stays in the iteration") {
		t.Fatalf("warnings = %q", warnings)
	}

	// A background conversation still running at the sign-out keeps it in place.
	background := &dsl.Script{Init: []dsl.Action{http("/login")}, Actions: []dsl.Action{
		{Type: "http_async_start", Params: map[string]any{"id": "poll", "pattern": "poll"}}, http("/001/o"), http("/secur/logout.jsp"),
	}}
	_, _, moved, _, reason = signIn.moveLogoutToTeardown(background, exchanges, []int{-1, 1, 2, -1}, nil)
	if moved != 0 || reason == "" {
		t.Fatalf("moved = %d, reason = %q; want nothing moved and a reason", moved, reason)
	}
}

// The first sign-out after the sign-in ends the session init signed in, so a
// sign-out that cannot move (other Salesforce traffic follows it, or nothing
// comes before it) returns a reason even when a later sign-out trails.
func TestDesktopSalesforceLogoutThatCannotMoveSaysWhy(t *testing.T) {
	const host = "https://acme.my.salesforce.com"
	ok := func(method, path string) exchange {
		return exchange{Method: method, URL: host + path, Response: &response{Status: 200}}
	}
	signedIn := []exchange{
		ok("GET", "/"),
		{
			Method: "POST", URL: host + "/", Body: "un=a%40example.test&pw=secret",
			Response: &response{Status: 302, Headers: map[string]string{"Location": host + "/secur/frontdoor.jsp?sid=x"}},
		},
		ok("GET", "/secur/frontdoor.jsp?sid=x"),
		ok("GET", "/home/home.jsp"),
	}
	followed := append(append([]exchange(nil), signedIn...),
		ok("GET", "/001/o"),
		ok("GET", "/secur/logout.jsp"),
		ok("POST", "/aura"),
		ok("GET", "/secur/logout.jsp"),
		ok("GET", "/"),
	)
	signIn := findDesktopSalesforceSignIn(followed)
	if signIn == nil || signIn.token != 3 {
		t.Fatalf("sign-in = %+v, want it to end at exchange 3", signIn)
	}
	if signIn.logoutStart != -1 || !strings.Contains(signIn.logoutStays, "traffic follows") {
		t.Fatalf("logoutStart = %d, logoutStays = %q; want -1 and a reason", signIn.logoutStart, signIn.logoutStays)
	}
	immediate := append(append([]exchange(nil), signedIn...),
		ok("GET", "/secur/logout.jsp"),
		ok("GET", "/001/o"),
		ok("GET", "/secur/logout.jsp"),
		ok("GET", "/"),
	)
	if signIn = findDesktopSalesforceSignIn(immediate); signIn == nil || signIn.logoutStart != -1 || signIn.logoutStays == "" {
		t.Fatalf("sign-in = %+v, want no move and a reason", signIn)
	}
	trailing := append(append([]exchange(nil), signedIn...),
		ok("GET", "/001/o"),
		ok("GET", "/secur/logout.jsp"),
		ok("GET", "/"),
	)
	if signIn = findDesktopSalesforceSignIn(trailing); signIn == nil || signIn.logoutStart != 5 || signIn.logoutStays != "" {
		t.Fatalf("sign-in = %+v, want the trailing sign-out at exchange 5", signIn)
	}
}
