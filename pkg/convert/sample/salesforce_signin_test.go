package sample

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	signInHost        = "https://acme.my.salesforce.com"
	signInIdentityURL = "https://login.salesforce.com/id/00D5g000004ExAmPLE/0055g00000ABCDeAAH"
	signInSource      = "SRCa1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0"
)

// signInVariant changes the recorded sign-in: the token exchange's grant
// and redirect, a CSRF token the login page hands its forms, and the body of
// the successful login.
type signInVariant struct {
	grant, redirect string
	csrf            bool
	successBody     string
}

const signInCSRF = "CSRFa1b2c3d4e5f6g7h8i9j0"

// salesforceSignInHAR is a Mobile SDK sign-in shaped like a real capture: the
// identity-first page, a failed attempt, a password reset, SMS verification,
// the approval page and the token exchange, then two API calls.
func salesforceSignInHAR(t *testing.T, variant signInVariant) string {
	t.Helper()
	if variant.grant == "" {
		variant.grant = "hybrid_auth_code"
	}
	if variant.redirect == "" {
		variant.redirect = "testsfdc%3A%2F%2F%2Fmobilesdk%2Fdetect%2Foauth%2Fdone"
	}
	if variant.successBody == "" {
		variant.successBody = "un=perf.user%40example.com&pw=Secr3t%26&lt=standard&username=perf.user%40example.com"
	}
	loginPage, field := "<html></html>", ""
	if variant.csrf {
		loginPage = `<html><form><input type="hidden" name="csrf_token" value="` + signInCSRF + `"></form></html>`
		field = "&csrf_token=" + signInCSRF
	}
	stamp := func(second int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", second) }
	page := func(second int, method, path, body string, status int, extra map[string]any) map[string]any {
		entry := recorderEntry(stamp(second), method, signInHost+path, status, "text/html", "<html></html>", extra)
		if body != "" {
			entry = formRequest(entry, body)
		}
		return entry
	}
	approvalPage := `<html><body><script>window.location.replace('` + mobileSDKCallback + `?display=touch&code=` + mobileSDKCodeWire + `');</script></body></html>`
	bearer := map[string]any{"name": "Authorization", "value": "Bearer " + mobileSDKAccessToken}
	tokenResponse := `{"access_token":"` + mobileSDKAccessToken + `","instance_url":"` + signInHost + `","id":"` + signInIdentityURL + `","token_type":"Bearer"}`
	return recorderHAR(t,
		recorderEntry(stamp(1), "GET", signInHost+"/.well-known/auth-configuration", 200, "application/json", `{"LoginPageEnabled":true}`, nil),
		withResponseHeaders(recorderEntry(stamp(2), "GET", signInHost+"/services/oauth2/authorize?response_type=code&client_id=3MVGexample&code_challenge=challengeExampleValue123", 302, "text/html", "", nil),
			map[string]any{"name": "Location", "value": signInHost + "/setup/secur/RemoteAccessAuthorizationPage.apexp?source=" + signInSource + "&display=touch"}),
		recorderEntry(stamp(3), "GET", signInHost+"/setup/secur/RemoteAccessAuthorizationPage.apexp?source="+signInSource+"&display=touch", 200, "text/html", loginPage, nil),
		page(4, "POST", "/", "un=perf.user%40example.com&lt=standard&username=perf.user%40example.com"+field, 200, nil),
		page(5, "POST", "/", "un=perf.usr%40example.com&pw=wrong&lt=standard&username=perf.usr%40example.com"+field, 200, nil),
		page(6, "POST", "/secur/forgotpassword.jsp", "un=perf.user%40example.com", 200, nil),
		withResponseHeaders(page(7, "POST", "/", variant.successBody+field, 302, nil),
			map[string]any{"name": "Location", "value": signInHost + "/secur/frontdoor.jsp?sid=00D5g000004ExAmPLE%21SessionValue123"}),
		page(8, "GET", "/_ui/identity/verification/method/SmsVerificationFinishUi/e", "", 200, nil),
		formRequest(recorderEntry(stamp(9), "POST", signInHost+"/_ui/identity/oauth/ui/AuthorizationPage", 200, "text/html", approvalPage, nil), "oauth_response=approve&display=touch"),
		formRequest(recorderEntry(stamp(10), "POST", signInHost+"/services/oauth2/token", 200, "application/json", tokenResponse, nil),
			"format=json&redirect_uri="+variant.redirect+"&client_id=3MVGexample&code_verifier=verifierExampleValue1234567890&grant_type="+variant.grant+"&code="+mobileSDKCodeWire),
		withRequestHeaders(recorderEntry(stamp(11), "GET", signInIdentityURL, 200, "application/json", `{"user_id":"0055g00000ABCDeAAH"}`, nil), bearer),
		withRequestHeaders(recorderEntry(stamp(20), "GET", signInHost+"/services/data/v66.0/query?q=SELECT+Id+FROM+Account", 200, "application/json", `{"records":[]}`, nil), bearer),
		withRequestHeaders(recorderEntry(stamp(21), "GET", signInHost+"/services/data/v66.0/limits", 200, "application/json", `{}`, nil), bearer),
	)
}

// convertSignIn converts a sign-in variant as the recorder does.
func convertSignIn(t *testing.T, variant signInVariant, transactions ...TransactionWindow) *ConvertResult {
	t.Helper()
	result, err := Convert(ImportRequest{Format: "har", Content: salesforceSignInHAR(t, variant), Options: ConvertOptions{
		HighConfidenceOnly: true, OmitSourceReview: true, Transactions: transactions,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func signInAt(second float64) time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(second * float64(time.Second)))
}

func usersDataSource(script *dsl.Script) *dsl.DataSource {
	for index := range script.DataSources {
		if script.DataSources[index].Name == salesforceUsersDataSource {
			return &script.DataSources[index]
		}
	}
	return nil
}

func TestSalesforceSignInRunsInInitWithPerUserCredentials(t *testing.T) {
	result := convertSignIn(t, signInVariant{},
		TransactionWindow{Name: "Login", Start: signInAt(1.5), End: signInAt(11.5), StartOrder: 0, EndOrder: 1},
		TransactionWindow{Name: "Search", Start: signInAt(19.5), End: signInAt(21.5), StartOrder: 2, EndOrder: 3},
	)
	script := result.Script
	label := func(action dsl.Action) string {
		if action.Type == "source_transaction" {
			return action.Name
		}
		return action.Method + " " + strings.SplitN(action.Target, "?", 2)[0]
	}
	var initShape, actionShape []string
	for _, action := range script.Init {
		initShape = append(initShape, label(action))
	}
	for _, action := range script.Actions {
		actionShape = append(actionShape, label(action))
	}
	if len(script.Init) != 13 || initShape[0] != "GET /.well-known/auth-configuration" || initShape[1] != "Start Login transaction" ||
		initShape[11] != "GET {{var.corr_salesforce_identity_url}}" || initShape[12] != "End Login transaction" {
		t.Fatalf("init:\n%s", strings.Join(initShape, "\n"))
	}
	if strings.Join(actionShape, "|") != "Start Search transaction|GET /services/data/v66.0/query|GET /services/data/v66.0/limits|End Search transaction" {
		t.Fatalf("actions:\n%s", strings.Join(actionShape, "\n"))
	}

	// Credentials come from the users file, one row per virtual user.
	users := usersDataSource(script)
	if users == nil || users.Strategy != "unique" || !users.FirstRowHeader || users.InlineData != "username,password\nperf.user@example.com,Secr3t&" {
		t.Fatalf("users data source = %+v", users)
	}
	blockers := func(action dsl.Action) string {
		var codes []string
		switch values := action.Metadata["execution_blockers"].(type) {
		case []string:
			codes = values
		case []any:
			for _, value := range values {
				codes = append(codes, fmt.Sprint(value))
			}
		}
		return strings.Join(codes, ",")
	}
	// Both password posts read the users file; the failed attempt is gated,
	// the identity-first post (the user's name alone) runs.
	var logins, failed, identityFirst int
	for _, action := range script.Init {
		switch {
		case strings.Contains(action.Body, "pw={{data"):
			logins++
			// The form's username field is a copy of un.
			if action.Body != "un={{data.salesforce_users.username}}&pw={{data.salesforce_users.password}}&lt=standard&username={{data.salesforce_users.username}}" ||
				action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] != dsl.ImportURLFormTemplateValueEncodingQueryComponent {
				t.Fatalf("login = %q %v", action.Body, action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey])
			}
			if strings.Contains(blockers(action), salesforceFailedSignInReasonCode) {
				failed++
			}
		case strings.HasPrefix(action.Body, "un={{data"):
			identityFirst++
			if action.Body != "un={{data.salesforce_users.username}}&lt=standard&username={{data.salesforce_users.username}}" || blockers(action) != "" {
				t.Fatalf("identity-first post = %q, gates %q", action.Body, blockers(action))
			}
		}
	}
	if logins != 2 || failed != 1 || identityFirst != 1 {
		t.Fatalf("login posts = %d, failed attempts gated = %d, identity-first posts = %d; want 2, 1 and 1", logins, failed, identityFirst)
	}

	// Traffic that cannot replay at load is gated with the reason.
	gated := map[string]string{}
	for _, action := range script.Init {
		if codes := blockers(action); codes != "" {
			gated[label(action)] = codes
		}
	}
	for step, reason := range map[string]string{
		"POST /secur/forgotpassword.jsp":                                  salesforcePasswordResetReasonCode,
		"GET /_ui/identity/verification/method/SmsVerificationFinishUi/e": salesforceIdentityVerificationReasonCode,
	} {
		if !strings.Contains(gated[step], reason) {
			t.Fatalf("%s gates = %q, want %s (all gates: %v)", step, gated[step], reason, gated)
		}
	}

	// The authorize state and the user's identity URL are correlated, so a
	// second user gets their own.
	if step := script.Init[3]; !strings.Contains(step.Target, "source={{var.corr_salesforce_authorize_source}}") {
		t.Fatalf("approval page target = %q", step.Target)
	}
	warnings := strings.Join(result.Warnings, "\n")
	for _, want := range []string{"runs in init (13 steps), so each virtual user signs in once", `data source "salesforce_users"`, "pre-authorize the perf users", "removing that step alone breaks the sign-in"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("warnings miss %q:\n%s", want, warnings)
		}
	}
	// The recording baseline names only the iteration's steps, by their index.
	if len(result.Baseline) != 2 || result.Baseline[0].ID != 1 || result.Baseline[1].ID != 2 {
		t.Fatalf("baseline = %+v", result.Baseline)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("the converted script must validate: %v", issues)
	}
}

// A sign-in whose transaction never ends stays in the iteration, and says so;
// its baseline holds the login bodies as recorded.
func TestSalesforceSignInStaysInlineWhenItCannotMove(t *testing.T) {
	result := convertSignIn(t, signInVariant{}, TransactionWindow{Name: "Everything", Start: signInAt(0), StartOrder: 0})
	if len(result.Script.Init) != 0 || !strings.Contains(strings.Join(result.Warnings, "\n"), "The Salesforce sign-in stays in the iteration") {
		t.Fatalf("init = %d steps, warnings = %q", len(result.Script.Init), result.Warnings)
	}
	recorded := false
	for _, observation := range result.Baseline {
		if strings.Contains(observation.RequestBody, "{{data.") {
			t.Fatalf("baseline observation %d holds a template: %q", observation.ID, observation.RequestBody)
		}
		recorded = recorded || observation.RequestBody == "un=perf.user%40example.com&pw=Secr3t%26&lt=standard&username=perf.user%40example.com"
	}
	if !recorded {
		t.Fatalf("the baseline misses the recorded login body: %+v", result.Baseline)
	}
}

// A transaction marked across the sign-in moves to init whole, and the
// conversion names the steps it carried along.
func TestSalesforceSignInCarriesASpanningTransactionAndSaysSo(t *testing.T) {
	result := convertSignIn(t, signInVariant{},
		TransactionWindow{Name: "Launch", Start: signInAt(0.5), End: signInAt(20.5), StartOrder: 0, EndOrder: 3},
		TransactionWindow{Name: "Login", Start: signInAt(1.5), End: signInAt(11.5), StartOrder: 1, EndOrder: 2},
	)
	if len(result.Script.Actions) != 1 || !strings.Contains(result.Script.Actions[0].Target, "/limits") {
		t.Fatalf("actions = %+v, want only the request after the transaction", result.Script.Actions)
	}
	// Login ends with the sign-in, so only Launch carried a step.
	if warnings := strings.Join(result.Warnings, "\n"); !strings.Contains(warnings, `Transaction "Launch", marked across the sign-in, moved to init whole with the 1 step(s) after the sign-in it times`) {
		t.Fatalf("warnings:\n%s", warnings)
	}
}

// A login form that also carries a correlated value keeps the users file in
// wire spelling, so nothing is escaped twice.
func TestSalesforceSignInUsersFileStaysURLEncodedBesideCorrelations(t *testing.T) {
	result := convertSignIn(t, signInVariant{csrf: true})
	users := usersDataSource(result.Script)
	if users == nil || users.InlineData != "username,password\nperf.user%40example.com,Secr3t%26" {
		t.Fatalf("users data source = %+v", users)
	}
	logins := 0
	for _, action := range result.Script.Init {
		if !strings.Contains(action.Body, "{{data."+salesforceUsersDataSource) {
			continue
		}
		logins++
		if !strings.Contains(action.Body, "{{var.") || action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] != nil {
			t.Fatalf("login = %q, encoding %v; want the correlated token and no escaping", action.Body, action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey])
		}
	}
	if logins != 3 || !strings.Contains(strings.Join(result.Warnings, "\n"), "keeps its values URL-encoded") {
		t.Fatalf("login forms = %d, warnings = %q", logins, result.Warnings)
	}
}

// A native Mobile SDK app exchanges an authorization_code for its app-scheme
// redirect; a web app's https redirect is not a Mobile SDK sign-in.
func TestSalesforceSignInDetectsNativeAuthorizationCodeGrant(t *testing.T) {
	native := convertSignIn(t, signInVariant{grant: "authorization_code"})
	if len(native.Script.Init) == 0 || usersDataSource(native.Script) == nil {
		t.Fatalf("native sign-in: init = %d, users = %v", len(native.Script.Init), usersDataSource(native.Script))
	}
	web := convertSignIn(t, signInVariant{grant: "authorization_code", redirect: "https%3A%2F%2Fapp.example.test%2Fcallback"})
	if len(web.Script.Init) != 0 || usersDataSource(web.Script) != nil {
		t.Fatalf("web sign-in: init = %d, users = %v", len(web.Script.Init), usersDataSource(web.Script))
	}
}

// When the signed-in user's login cannot be read, no form reads a users file
// the script does not have.
func TestSalesforceSignInLeavesLoginsWhenCredentialsDoNotParse(t *testing.T) {
	result := convertSignIn(t, signInVariant{successBody: "un=perf.user%40example.com&pw=Secr3t&pw=again&lt=standard&username=perf.user%40example.com"})
	if usersDataSource(result.Script) != nil {
		t.Fatal("a users data source was added without credentials")
	}
	for _, action := range append(append([]dsl.Action(nil), result.Script.Init...), result.Script.Actions...) {
		if strings.Contains(action.Body, "{{data."+salesforceUsersDataSource) {
			t.Fatalf("%s %s reads the missing users file: %q", action.Method, action.Target, action.Body)
		}
	}
}

func TestMoveActionPath(t *testing.T) {
	for path, want := range map[string]string{
		"actions[2].body":          "init[2].body",
		"actions[5]":               "actions[0]",
		"actions[7].extractors[1]": "actions[2].extractors[1]",
		"config.base_url":          "config.base_url",
	} {
		if got := moveActionPath(path, 5); got != want {
			t.Fatalf("moveActionPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestMoveToInitRefusesAConversationThatOutlivesSignIn(t *testing.T) {
	exchanges := []exchange{
		{Method: "GET", URL: "https://acme.my.salesforce.com/services/oauth2/authorize"},
		{Method: "POST", URL: "https://acme.my.salesforce.com/services/oauth2/token"},
		{Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v66.0/limits"},
	}
	start := func(id, pattern string) dsl.Action {
		return dsl.Action{Type: "http_async_start", Params: map[string]any{"id": id, "pattern": pattern}}
	}
	request := dsl.Action{Type: "http", Method: "GET", Target: "/x"}
	for _, tc := range []struct {
		name    string
		actions []dsl.Action
		want    string
	}{
		{name: "poll runs on", actions: []dsl.Action{start("poll_1", "poll"), request, request, request}, want: "runs on after it"},
		{name: "nothing after", actions: []dsl.Action{request, request}, want: "no steps after sign-in"},
		{name: "open transaction", actions: []dsl.Action{
			{Type: "source_transaction", Name: "Start Login transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{"Login"}}},
			request, request, request,
		}, want: "never ends"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{Actions: tc.actions}
			newIndex := []int{len(tc.actions) - 3, len(tc.actions) - 2, len(tc.actions) - 1}
			if tc.name == "nothing after" {
				newIndex = []int{0, 1, -1}
			}
			signIn := &salesforceSignIn{token: 1}
			_, _, moved, why := signIn.moveToInit(script, exchanges, newIndex, nil)
			if moved != 0 || !strings.Contains(why, tc.want) || len(script.Init) != 0 {
				t.Fatalf("moved = %d, why = %q, init = %d; want %q", moved, why, len(script.Init), tc.want)
			}
		})
	}

	// A one-shot background request may start during sign-in; it never runs on.
	script := &dsl.Script{Actions: []dsl.Action{start("log_1", "once"), request, request, request}}
	signIn := &salesforceSignIn{token: 1}
	moved, mappings, boundary, why := signIn.moveToInit(script, exchanges, []int{1, 2, 3}, []sourceview.Mapping{{TargetPath: "actions[3].target"}, {TargetPath: "actions[1]"}})
	if boundary != 3 || why != "" || len(script.Init) != 3 || len(script.Actions) != 1 {
		t.Fatalf("boundary = %d, why = %q, init = %d, actions = %d", boundary, why, len(script.Init), len(script.Actions))
	}
	if !slices.Equal(moved, []int{-1, -1, 0}) || mappings[0].TargetPath != "actions[0].target" || mappings[1].TargetPath != "init[1]" {
		t.Fatalf("moved = %v, mappings = %+v", moved, mappings)
	}
}
