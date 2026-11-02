package salesforce

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

func TestAuraTokenEikoocnekotConfirmationAndRedirectScope(t *testing.T) {
	for _, cookieName := range []string{
		"__Host-ERIC_PROD-2876452450001777317",
		"__Host-ERIC_PROD1049330172993496433",
	} {
		t.Run(cookieName, func(t *testing.T) {
			script := auraTokenScript(auraTokenFixture)
			rawHAR := auraTokenHAR(t, auraTokenFixture, "acme.lightning.force.com", "")
			var parsed map[string]any
			if err := json.Unmarshal(rawHAR, &parsed); err != nil {
				t.Fatal(err)
			}
			entries := parsed["log"].(map[string]any)["entries"].([]any)
			entries[0].(map[string]any)["response"].(map[string]any)["content"].(map[string]any)["text"] =
				`<html><script>var auraConfig = {"eikoocnekot":"` + cookieName + `"};</script></html>`
			var err error
			rawHAR, err = json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}

			review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: rawHAR})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if review.NativePreview == nil || review.NativePreview.Script == nil {
				t.Fatal("expected NativePreview.Script")
			}
			var foundReason bool
			for _, corr := range review.NativePreview.Correlations {
				if corr.RuleID == correlate.RuleSalesforceAuraTokenUncapturedCookie {
					for _, reason := range corr.Reasons {
						if strings.Contains(reason, cookieName) {
							foundReason = true
						}
					}
				}
			}
			if !foundReason {
				t.Fatalf("expected eikoocnekot confirmation for %s in correlation reasons: %#v", cookieName, review.NativePreview.Correlations)
			}
			var hasScope bool
			expression := ""
			requestURL := ""
			for _, ext := range review.NativePreview.Script.Actions[0].Extractors {
				if ext.Name == "corr_salesforce_aura_token" && ext.Scope != nil {
					hasScope = true
					expression = ext.Expression
					requestURL = ext.Scope.RequestURL
					// The relative bootstrap's Lightning glob cannot be anchored
					// to the host, so redirect hops are not searched.
					if ext.Scope.Redirects != "" {
						t.Fatalf("relative bootstrap scope searches redirect hops: %#v", ext.Scope)
					}
				}
			}
			if !hasScope {
				t.Fatalf("expected a Scope on corr_salesforce_aura_token: %#v", review.NativePreview.Script.Actions[0].Extractors)
			}
			// The bootstrap target is relative to config.base_url, so pointing
			// the script at another org changes its host: a recorded host in
			// the scope would make the on_miss=fail extractor miss every replay.
			// The Lightning base_url still keeps the scope to Lightning hosts,
			// so a my.salesforce.com hop's same-family cookie cannot win.
			for hopURL, want := range map[string]bool{
				"https://acme.lightning.force.com/one/one.app":       true,
				"https://otherorg.lightning.force.com/one/one.app":   true,
				"https://acme.my.salesforce.com/one/one.app":         false,
				"https://acme.my.salesforce.com/visualforce/session": false,
			} {
				if got := httpcontract.MatchScopeGlob(requestURL, hopURL); got != want {
					t.Fatalf("relative bootstrap scope request_url %q match %q = %v, want %v", requestURL, hopURL, got, want)
				}
			}
			// With an absolute bootstrap target the suffix stays open, so a
			// same-family __Host- cookie that a hop on another host sets must
			// be out of scope: a __Host- cookie is host-only and never reaches
			// the Lightning host's Aura requests.
			absolute := auraTokenScript(auraTokenFixture)
			absolute.Config.BaseURL = ""
			for index := range absolute.Actions {
				absolute.Actions[index].Target = "https://acme.lightning.force.com" + absolute.Actions[index].Target
			}
			absoluteReview, err := Analyze(absolute, AnalyzeOptions{BuildNative: true, HARData: rawHAR})
			if err != nil {
				t.Fatalf("Analyze absolute: %v", err)
			}
			if absoluteReview.NativePreview == nil || absoluteReview.NativePreview.Script == nil {
				t.Fatal("expected NativePreview.Script for the absolute bootstrap target")
			}
			absoluteRequestURL := ""
			for _, ext := range absoluteReview.NativePreview.Script.Actions[0].Extractors {
				if ext.Name == "corr_salesforce_aura_token" && ext.Scope != nil {
					absoluteRequestURL = ext.Scope.RequestURL
				}
			}
			if !httpcontract.MatchScopeGlob(absoluteRequestURL, "https://acme.lightning.force.com/one/one.app?x=1") {
				t.Fatalf("scope request_url %q excludes the bootstrap host's own response", absoluteRequestURL)
			}
			for _, hop := range []string{
				"https://acme.my.salesforce.com/secur/frontdoor.jsp?retURL=https://acme.lightning.force.com/one/one.app",
				"https://acme.lightning.force.com.evil.test/one/one.app",
			} {
				if httpcontract.MatchScopeGlob(absoluteRequestURL, hop) {
					t.Fatalf("scope request_url %q keeps the other host's hop %s", absoluteRequestURL, hop)
				}
			}
			// Redirect hops are searched first and the first match wins, so the
			// confirmed name must pin the extractor to that cookie family.
			headers := "Set-Cookie: ERIC_PROD-1111111111111111111=hop-token; Path=/; Secure\r\n" +
				"Set-Cookie: __Host-ERIC_DEV-2222222222222222222=dev-token; Path=/; Secure\r\n" +
				"Set-Cookie: " + cookieName + "=confirmed-token; Path=/; Secure\r\n"
			match := regexp.MustCompile(expression).FindStringSubmatch(headers)
			if len(match) < 2 || match[1] != "confirmed-token" {
				t.Fatalf("extractor %q matched %q, want only the confirmed %s cookie", expression, match, cookieName)
			}
			// The digit suffix is server-generated: another virtual user or
			// session signing in may be issued a different one, and the
			// on_miss=fail sign-in step must still read its token.
			// The suffix is a signed 64-bit number, so the other session's
			// sign may differ from the recorded one in either direction.
			prefix := strings.TrimSuffix(strings.TrimRight(cookieName, "0123456789"), "-")
			for _, suffix := range []string{"9081726354453627180", "-9081726354453627180"} {
				otherUser := "Set-Cookie: " + prefix + suffix + "=other-user-token; Path=/; Secure\r\n"
				if match := regexp.MustCompile(expression).FindStringSubmatch(otherUser); len(match) < 2 || match[1] != "other-user-token" {
					t.Fatalf("extractor %q matched %q, want the token of a %s%s cookie", expression, match, prefix, suffix)
				}
			}
			if strings.Contains(expression, strings.TrimLeft(strings.TrimPrefix(cookieName, prefix), "-")) {
				t.Fatalf("extractor %q carries the recorded cookie-name suffix", expression)
			}
		})
	}
}

func TestNativePreviewDeduplicatesBodyNotExportedGateAndEnrichesAuraActionNames(t *testing.T) {
	validMsg := `{"actions":[{"id":"1;a","descriptor":"aura://ApexActionController/ACTION$execute","callingDescriptor":"UNKNOWN","params":{"namespace":"","classname":"CustomerSearchController","method":"findAccount","params":{}}},{"id":"2;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields","callingDescriptor":"UNKNOWN","params":{}}]}`
	validBody := "message=" + url.QueryEscape(validMsg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=null"

	script := &dsl.Script{
		Name:     "Aura Gate Dedup",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.sandbox.lightning.force.com"},
		Actions: []dsl.Action{
			{
				Name:   "POST /aura",
				Type:   "http_request",
				Method: "POST",
				Target: "/aura?r=1",
				Body:   validBody,
				Metadata: map[string]any{
					"har.entry_index": 1,
				},
			},
			{
				Name:   "POST /aura",
				Type:   "http_request",
				Method: "POST",
				Target: "/aura?r=2",
				Body:   "",
				Metadata: map[string]any{
					"har.entry_index":                     2,
					"har.request.recorded_content_length": int64(96719),
					"execution_blockers":                  []string{sample.HARRequestBodyNotExportedCode},
				},
			},
		},
	}

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	native := review.NativePreview.Script
	if native.Actions[0].Name != "POST /aura — CustomerSearchController.findAccount (+1)" {
		t.Fatalf("enriched Aura action name = %q, want Apex class+method (+1)", native.Actions[0].Name)
	}
	findings, err := importreview.FindingsFromScript(&dsl.Script{Actions: []dsl.Action{native.Actions[1]}})
	if err != nil {
		t.Fatalf("FindingsFromScript: %v", err)
	}
	for _, f := range findings {
		if f.Code == "salesforce_aura_request_requires_review" {
			t.Fatalf("unexported body action received duplicate salesforce_aura_request_requires_review finding: %#v", findings)
		}
	}
}

func TestAnalyzeMatchesTemplatedHAREntriesRewritesQueryMetadataAndBoostsDynamicDiffIDs(t *testing.T) {
	const (
		rawSID       = "00Dxx0000001gEREAY!AQEAQSyntheticSession987654"
		escapedSID   = "00Dxx0000001gEREAY%21AQEAQSyntheticSession987654"
		dynamicRecID = "a3Cir000000bcUXEAY"
		staticAppID  = "06mir0000008XYZAA2"
	)

	createResp := `{"actions":[{"id":"1;a","state":"SUCCESS","returnValue":{"id":"` + dynamicRecID + `","appId":"` + staticAppID + `"}}]}`
	viewMsg := `{"actions":[{"id":"2;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields","callingDescriptor":"UNKNOWN","params":{"recordId":"` + dynamicRecID + `","appId":"` + staticAppID + `"}}]}`
	viewBody := "message=" + url.QueryEscape(viewMsg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=null"

	harPayload := map[string]any{
		"log": map[string]any{
			"entries": []any{
				map[string]any{
					"request": map[string]any{"method": "POST", "url": "https://acme.sandbox.my.salesforce.com/", "headers": []any{}},
					"response": map[string]any{
						"status":  302,
						"headers": []map[string]string{{"name": "Location", "value": "https://acme.sandbox.my.salesforce.com/secur/frontdoor.jsp?sid=" + escapedSID}},
						"content": map[string]any{"text": ""},
					},
				},
				map[string]any{
					"request":  map[string]any{"method": "GET", "url": "https://acme.sandbox.my.salesforce.com/secur/frontdoor.jsp?sid=" + escapedSID, "queryString": []map[string]string{{"name": "sid", "value": rawSID}}, "headers": []any{}},
					"response": map[string]any{"status": 200, "headers": []map[string]string{{"name": "Content-Type", "value": "application/json"}}, "content": map[string]any{"mimeType": "application/json", "text": createResp}},
				},
				map[string]any{
					"request": map[string]any{
						"method":   "POST",
						"url":      "https://acme.sandbox.lightning.force.com/aura?r=1",
						"headers":  []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
						"postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": viewBody},
					},
					"response": map[string]any{"status": 200, "headers": []map[string]string{{"name": "Content-Type", "value": "application/json"}}, "content": map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"2;a","state":"SUCCESS"}]}`}},
				},
			},
		},
	}
	rawHAR, err := json.Marshal(harPayload)
	if err != nil {
		t.Fatal(err)
	}

	converted, err := sample.Convert(sample.ImportRequest{
		Name:    "Paired Diff Native",
		Format:  "har",
		Content: string(rawHAR),
		Options: sample.ConvertOptions{HighConfidenceOnly: true},
	})
	if err != nil {
		t.Fatalf("sample.Convert: %v", err)
	}
	script := converted.Script

	review, err := Analyze(script, AnalyzeOptions{
		BuildNative: true,
		HARData:     rawHAR,
		DiffEvidence: []DiffCorrelationEvidence{
			{
				Source:         "body",
				Expression:     "$.actions[0].returnValue.id",
				BaselineValue:  dynamicRecID,
				BaselineEntry:  2,
				Classification: "dynamic",
			},
			{
				Source:         "body",
				Expression:     "$.actions[0].returnValue.appId",
				BaselineValue:  staticAppID,
				BaselineEntry:  2,
				Classification: "static",
			},
		},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if review.HARMatchCoverage.MatchedRequests != 3 || review.HARMatchCoverage.CoveragePct != 100 {
		t.Fatalf("HARMatchCoverage = %+v, want 3/3 (100%%)", review.HARMatchCoverage)
	}
	totalDomainCount := 0
	for _, d := range review.Domains {
		totalDomainCount += d.Count
	}
	if totalDomainCount != 3 {
		t.Fatalf("domainObservations double-counted matched entries: total=%d, domains=%+v", totalDomainCount, review.Domains)
	}

	native := review.NativePreview.Script
	// Producer 0 must reuse corr_salesforce_frontdoor_sid without creating a _2 or secondary duplicate extractor.
	var extNames []string
	for _, ext := range native.Actions[0].Extractors {
		extNames = append(extNames, ext.Name)
	}
	if len(extNames) != 1 || extNames[0] != "corr_salesforce_frontdoor_sid" {
		t.Fatalf("unexpected duplicate extractors on action 0: %v", extNames)
	}
	// Query metadata must carry templated {{var.corr_salesforce_frontdoor_sid}} and no raw sid.
	qRaw, _ := json.Marshal(native.Actions[1].Metadata["har.request.query_string"])
	if !strings.Contains(string(qRaw), `"value":"{{var.corr_salesforce_frontdoor_sid}}"`) || strings.Contains(string(qRaw), rawSID) || strings.Contains(string(qRaw), escapedSID) {
		t.Fatalf("har.request.query_string = %s, want templated sid", string(qRaw))
	}
	// Also verify applyCorrelationVarsToActionQueryMetadata directly rewrites decoded ! values against percent-encoded vars.
	directAction := &dsl.Action{
		Target: "/secur/frontdoor.jsp?sid={{var.corr_salesforce_frontdoor_sid}}",
		Metadata: map[string]any{
			"har.request.query_string": []any{map[string]any{"name": "sid", "value": rawSID}},
		},
	}
	applyCorrelationVarsToActionQueryMetadata(directAction, map[string]string{escapedSID: "{{var.corr_salesforce_frontdoor_sid}}"})
	qDirect, _ := json.Marshal(directAction.Metadata["har.request.query_string"])
	if !strings.Contains(string(qDirect), `"value":"{{var.corr_salesforce_frontdoor_sid}}"`) {
		t.Fatalf("direct applyCorrelationVarsToActionQueryMetadata = %s, want templated sid", string(qDirect))
	}
	// Dynamic 18-char record ID must be auto-applied, while static 18-char appId stays literal!
	if strings.Contains(native.Actions[2].Body, dynamicRecID) {
		t.Fatalf("dynamic record ID %s remained literal in body: %s", dynamicRecID, native.Actions[2].Body)
	}
	if !strings.Contains(native.Actions[2].Body, staticAppID) {
		t.Fatalf("static appId %s was unexpectedly correlated in body: %s", staticAppID, native.Actions[2].Body)
	}
}

func TestAuraTokenCookieExtractorScopeLimitsOnlyHostOnlyNamesToTheBootstrapOrigin(t *testing.T) {
	const producer = "https://Acme.Lightning.force.com/one/one.app"
	const lightningBase = "https://acme.lightning.force.com"
	hostOnly := namedAuraTokenCookieExtractor("__Host-ERIC_PROD-2876452450001777317")
	if scope := auraTokenCookieExtractorScope(hostOnly, producer, lightningBase); scope.Redirects != "include" || scope.RequestURL != "https://acme.lightning.force.com/*" {
		t.Fatalf("__Host- scope = %#v, want redirects with the bootstrap origin", scope)
	}
	// A relative bootstrap under a Lightning base_url keeps any Lightning
	// host, so a my.salesforce.com hop's same-family __Host- cookie cannot
	// win while a retargeted base_url (another org, or a sandbox) still does.
	for _, base := range []string{lightningBase, "https://acme--uat.sandbox.lightning.force.com/"} {
		scope := auraTokenCookieExtractorScope(hostOnly, "/one/one.app", base)
		// The glob's "*" spans "/" and "?", so it cannot keep a hop to its
		// host: an off-host hop carrying an unencoded Lightning URL in its
		// query passes it. Redirect hops are therefore not searched.
		if scope.Redirects != "" || scope.RequestURL != "https://*.lightning.force.com/*" {
			t.Fatalf("relative producer under %s scope = %#v, want the final Lightning-host response only", base, scope)
		}
		if !httpcontract.MatchScopeGlob(scope.RequestURL, "https://acme.my.salesforce.com/secur/frontdoor.jsp?retURL=https://acme.lightning.force.com/one/one.app") {
			t.Fatal("the unanchored glob no longer passes an off-host hop with an embedded Lightning URL; revisit whether redirect hops can be searched again")
		}
		for hopURL, want := range map[string]bool{
			"https://acme.lightning.force.com/one/one.app":               true,
			"https://other--uat.sandbox.lightning.force.com/one/one.app": true,
			"https://acme.my.salesforce.com/one/one.app":                 false,
			"https://acme.my.salesforce.com/visualforce/session":         false,
		} {
			if got := httpcontract.MatchScopeGlob(scope.RequestURL, hopURL); got != want {
				t.Fatalf("relative scope %q match %q = %v, want %v", scope.RequestURL, hopURL, got, want)
			}
		}
	}
	for name, tc := range map[string]struct{ expression, producer, base string }{
		// A non-__Host- ERIC cookie may carry a Domain attribute, so another
		// host can legitimately set it.
		"domain cookie":      {namedAuraTokenCookieExtractor("ERIC_PROD-2876452450001777317"), producer, lightningBase},
		"generic pattern":    {auraTokenCookieExtractor, producer, lightningBase},
		"templated producer": {hostOnly, "https://{{var.host}}/one/one.app", lightningBase},
		"relative producer under a non-Lightning base_url": {hostOnly, "/one/one.app", "https://acme.my.salesforce.com"},
		"relative producer under a templated base_url":     {hostOnly, "/one/one.app", "https://{{var.host}}"},
		"relative producer without a base_url":             {hostOnly, "/one/one.app", ""},
		"relative domain cookie":                           {namedAuraTokenCookieExtractor("ERIC_PROD-2876452450001777317"), "/one/one.app", lightningBase},
	} {
		if scope := auraTokenCookieExtractorScope(tc.expression, tc.producer, tc.base); scope.Redirects != "include" || scope.RequestURL != "" {
			t.Fatalf("%s scope = %#v, want redirects without a request_url filter", name, scope)
		}
	}
}
