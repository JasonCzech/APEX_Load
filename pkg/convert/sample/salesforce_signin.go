package sample

import (
	"encoding/csv"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// A recorded Salesforce Mobile SDK sign-in (an OAuth authorization code the
// app exchanges at /services/oauth2/token for its hybrid or native session)
// replays as a LoadRunner script signs in: once per virtual user, in init,
// each user with its own row of credentials, and the Bearer token reused by
// every iteration. The recording's other sign-in traffic is reviewed: failed
// attempts, a password reset and identity verification (SMS) cannot run at
// load, and the approval page disappears once perf users are pre-authorized.

// salesforceUsersDataSource holds the sign-in credentials, one row per
// virtual user.
const salesforceUsersDataSource = "salesforce_users"

// Review reason codes of recorded sign-in traffic that cannot replay.
const (
	salesforceFailedSignInReasonCode         = "salesforce_failed_sign_in_attempt"
	salesforcePasswordResetReasonCode        = "salesforce_password_reset_request"
	salesforceIdentityVerificationReasonCode = "salesforce_identity_verification"
	salesforceRecordedSignInReasonCode       = "salesforce_recorded_sign_in_again"
)

type salesforceSignIn struct {
	// token is the exchange that traded the authorization code for tokens (or,
	// when desktop is true, the last exchange of the browser sign-in bootstrap
	// chain through /one/one.app or /home/home.jsp).
	token int
	// desktop marks a browser form -> frontdoor.jsp sign-in chain rather than
	// a Mobile SDK OAuth token exchange.
	desktop bool
	// logoutStart is the first exchange of a trailing /secur/logout.jsp chain
	// that moves to teardown when sign-in moves to init, or -1. logoutStays
	// says why a recorded sign-out cannot move (Salesforce traffic follows
	// it, or no step comes before it), or is "".
	logoutStart int
	logoutStays string
	// logins are the login form posts (un and pw) before it; signedIn the one
	// that signed in (a redirect), or -1. usernames are the identity-first
	// posts (un alone) to the same login page.
	logins    []int
	usernames []int
	signedIn  int
	// resignIns are the login form posts (un and pw) after it, such as a
	// sign-in again after a sign-out that stays in the iteration, and
	// resignInUsernames the identity-first posts to a login page after it.
	// They read the users file too, so no later sign-in replays the recorded
	// user's literal credentials.
	resignIns         []int
	resignInUsernames []int
	// otherUser counts the later sign-ins that signed in a different recorded
	// user, now replayed as the users-file user.
	otherUser int
	// approved marks a recorded OAuth approval page post.
	approved bool
	// username and password are the signed-in user's recorded credentials;
	// credentials reports whether the login form was parameterized.
	username, password string
	credentials        bool
	// recordedBodies keeps the recorded bodies the users file replaced, for
	// the recording baseline.
	recordedBodies map[int]string
	// spanning names the transactions marked across the sign-in that moved to
	// init whole, and carried counts the steps after the sign-in they took.
	spanning []string
	carried  int
}

// findSalesforceSignIn finds a Mobile SDK OAuth sign-in (a token exchange with
// grant_type hybrid_auth_code, or authorization_code for an app-scheme
// redirect_uri) or a desktop Salesforce browser sign-in chain (login form POST
// with un+pw redirecting to /secur/frontdoor.jsp, followed by session handoff
// through /one/one.app or /home/home.jsp).
func findSalesforceSignIn(exchanges []exchange) *salesforceSignIn {
	// An authorization-code exchange this detector cannot place (an https
	// redirect_uri, an unreadable form) is a sign-in of its own, so the
	// browser chain is not looked for beside it. Grants that sign nobody in
	// (refresh_token, a JWT bearer, client_credentials), such as an embedded
	// app renewing its token, do not hide a desktop sign-in.
	hasCodeExchange := false
	for index, ex := range exchanges {
		if !strings.EqualFold(ex.Method, "POST") || !strings.HasSuffix(urlPath(ex.URL), "/services/oauth2/token") || ex.BodyEncoding != "" {
			continue
		}
		form, err := url.ParseQuery(ex.Body)
		if err != nil {
			hasCodeExchange = true
			continue
		}
		switch form.Get("grant_type") {
		case "hybrid_auth_code":
		case "authorization_code":
			hasCodeExchange = true
			redirect, err := url.Parse(form.Get("redirect_uri"))
			if err != nil || redirect.Scheme == "" || redirect.Scheme == "http" || redirect.Scheme == "https" {
				continue
			}
		case "":
			hasCodeExchange = true
			continue
		default:
			continue
		}
		signIn := &salesforceSignIn{token: index, signedIn: -1, logoutStart: -1}
		populateSalesforceSignInForms(signIn, exchanges, index)
		return signIn
	}
	if hasCodeExchange {
		return nil
	}
	return findDesktopSalesforceSignIn(exchanges)
}

func populateSalesforceSignInForms(signIn *salesforceSignIn, exchanges []exchange, limit int) {
	// One pass reads the form posts before limit: login posts carry un and pw,
	// identity-first posts un alone to the same login page.
	type usernamePost struct {
		index int
		path  string
	}
	var usernamePosts []usernamePost
	loginPaths := map[string]bool{}
	for login := range exchanges {
		ex := exchanges[login]
		if !strings.EqualFold(ex.Method, "POST") || ex.BodyEncoding != "" || !strings.Contains(ex.Body, "un=") {
			continue
		}
		form, err := url.ParseQuery(ex.Body)
		if err != nil || !form.Has("un") {
			continue
		}
		path := urlPath(ex.URL)
		if !form.Has("pw") {
			usernamePosts = append(usernamePosts, usernamePost{index: login, path: path})
			continue
		}
		loginPaths[path] = true
		if login >= limit {
			// A later sign-in (after a sign-out the recording keeps) is not
			// the one that signed in, nor a failed attempt before it.
			signIn.resignIns = append(signIn.resignIns, login)
			continue
		}
		signIn.logins = append(signIn.logins, login)
		if response := ex.Response; response != nil && response.Status >= 300 && response.Status < 400 {
			signIn.signedIn = login
		}
	}
	for _, post := range usernamePosts {
		if !loginPaths[post.path] {
			continue
		}
		if post.index >= limit {
			signIn.resignInUsernames = append(signIn.resignInUsernames, post.index)
			continue
		}
		signIn.usernames = append(signIn.usernames, post.index)
	}
}

func findDesktopSalesforceSignIn(exchanges []exchange) *salesforceSignIn {
	signedIn := -1
	frontdoorIdx := -1
	for index, ex := range exchanges {
		if !strings.EqualFold(ex.Method, "POST") || ex.BodyEncoding != "" || !strings.Contains(ex.Body, "un=") || !strings.Contains(ex.Body, "pw=") {
			continue
		}
		if ex.Response == nil || ex.Response.Status < 300 || ex.Response.Status >= 400 {
			continue
		}
		form, err := url.ParseQuery(ex.Body)
		if err != nil || !form.Has("un") || !form.Has("pw") {
			continue
		}
		loc := headerLookup(ex.Response.Headers, "Location")
		if !strings.HasSuffix(strings.ToLower(urlPath(loc)), "/secur/frontdoor.jsp") {
			continue
		}
		// Require an actual GET /secur/frontdoor.jsp follow-up in the capture so
		// external web-app OAuth redirects are never misclassified as desktop sessions.
		fd := -1
		for next := index + 1; next < len(exchanges) && next <= index+8; next++ {
			if strings.HasSuffix(strings.ToLower(urlPath(exchanges[next].URL)), "/secur/frontdoor.jsp") {
				fd = next
				break
			}
		}
		if fd < 0 {
			continue
		}
		signedIn = index
		frontdoorIdx = fd
		break
	}
	if signedIn < 0 || frontdoorIdx < 0 {
		return nil
	}
	end := frontdoorIdx
	reachedOneApp200 := false
	// landed is set once the sign-in reaches the org's home page (home.jsp or
	// one.app). Before that, a Visualforce or Flow page the chain passes
	// through is taken as a login flow (whatever the admin named it), but it
	// joins the sign-in only when the chain lands on the home page within
	// salesforceLoginFlowReach exchanges of the hop before it. While such a
	// page is pending (flowPage), a session handoff does not move end past
	// it, and a second, different page ends the chain: a login flow shows
	// one page, while a startURL business page leads on to others. A login
	// flow hands the user on when it finishes (a session handoff, or a
	// redirect from its page or to a sign-in destination, finishedFlow),
	// while a landing Visualforce tab the user leaves by clicking Home does
	// not: reaching the home page with no such hop after the pending page
	// ends the chain before it. A post back to the page that answers 200 is
	// the tab's own form, and a servlet redirect it loads (a file download)
	// hands nobody on.
	landed := false
	lastHop := frontdoorIdx
	flowPage := ""
	finishedFlow := false
chain:
	for next := frontdoorIdx + 1; next < len(exchanges); next++ {
		ex := exchanges[next]
		p := strings.ToLower(strings.TrimRight(urlPath(ex.URL), "/"))
		if p == "/aura" || strings.HasSuffix(p, "/aura") || strings.HasPrefix(p, "/services/") || strings.HasSuffix(p, "/secur/logout.jsp") {
			break
		}
		if page := salesforceLoginFlowPageName(p); !landed && page != "" && flowPage != "" && page != flowPage {
			break
		}
		if reachedOneApp200 {
			if p == "/one/one.app" {
				end = next
				continue
			}
			break
		}
		switch {
		case strings.HasSuffix(p, "/secur/frontdoor.jsp"),
			strings.HasSuffix(p, "/secur/contentdoor"),
			strings.HasSuffix(p, "/visualforce/session"),
			strings.HasSuffix(p, "/visualforce/recsession"),
			strings.Contains(p, "/apex/") && strings.Contains(p, "loginflow"):
			if flowPage != "" {
				// A handoff after a pending page joins only if the chain
				// lands.
				finishedFlow = true
				continue
			}
			end, lastHop = next, next
		case (strings.HasSuffix(p, "/home/home.jsp") || p == "/one/one.app") && flowPage != "" && !finishedFlow:
			// The pending page never handed the user on: it is a landing
			// business page, and the home page a step after it.
			break chain
		case strings.HasSuffix(p, "/home/home.jsp"):
			end, lastHop = next, next
			landed = true
		case p == "/one/one.app":
			end, lastHop = next, next
			landed = true
			if ex.Response != nil && ex.Response.Status >= 200 && ex.Response.Status < 300 {
				reachedOneApp200 = true
			}
		case ex.Response != nil && ex.Response.Status >= 300 && ex.Response.Status < 400:
			// Intermediate redirect hops inside the login/session handoff
			// chain; after a pending page, the hop it finishes with.
			if flowPage != "" && salesforceLoginFlowRedirectFinishes(ex, p, flowPage) {
				finishedFlow = true
			}
			continue
		case isSalesforceSignInStaticAsset(ex):
			// A script or image a sign-in page loads (kept when static assets
			// are included) is not a business step.
			continue
		case !landed && next-lastHop <= salesforceLoginFlowReach && isSalesforceLoginFlowPage(p):
			// A custom login-flow page (or its remoting call) before the
			// home page: end moves past it only if the chain lands. A post
			// back that answers 200 rendered the page again (a login flow
			// finishes with a redirect), so it finishes nothing.
			if page := salesforceLoginFlowPageName(p); page != "" {
				flowPage = page
			}
			continue
		default:
			// Any other page is the first business step (a Classic or
			// Visualforce-only capture never reaches /aura): the chain ends
			// here, so a later home.jsp or session handoff cannot pull the
			// business flow into init.
			break chain
		}
	}
	logoutStart, logoutStays := findDesktopSalesforceLogoutStart(exchanges, end)
	signIn := &salesforceSignIn{
		token:       end,
		desktop:     true,
		signedIn:    signedIn,
		logoutStart: logoutStart,
		logoutStays: logoutStays,
	}
	populateSalesforceSignInForms(signIn, exchanges, signedIn+1)
	return signIn
}

// salesforceLoginFlowRedirectFinishes reports whether a redirect after the
// pending login-flow page (flowPage) is the hop the flow finishes with: one
// from the page itself (its form's finish action), or one to the home page or
// a session handoff. A static asset or a servlet redirect the page loads
// (a file or image download) hands nobody on.
func salesforceLoginFlowRedirectFinishes(ex exchange, p, flowPage string) bool {
	if isSalesforceSignInStaticAsset(ex) {
		return false
	}
	if salesforceLoginFlowPageName(p) == flowPage {
		return true
	}
	location := ""
	if ex.Response != nil {
		location = strings.ToLower(strings.TrimRight(urlPath(headerLookup(ex.Response.Headers, "Location")), "/"))
	}
	for _, suffix := range []string{"/home/home.jsp", "/one/one.app", "/secur/frontdoor.jsp", "/secur/contentdoor", "/visualforce/session", "/visualforce/recsession"} {
		if strings.HasSuffix(location, suffix) {
			return true
		}
	}
	return false
}

// salesforceLoginFlowReach bounds how many exchanges a login-flow page may
// stand from the sign-in hop before it, so a business Visualforce page far
// from the sign-in never joins it.
const salesforceLoginFlowReach = 8

// isSalesforceLoginFlowPage reports whether a lower-cased path is a page a
// Salesforce login flow can show: a Visualforce page (any name), its
// remoting endpoint, or a Flow interview.
func isSalesforceLoginFlowPage(p string) bool {
	return strings.Contains(p, "/apex/") || strings.HasSuffix(p, "/apexremote") || strings.Contains(p, "/flow/")
}

// salesforceLoginFlowPageName names the Visualforce page or Flow a
// lower-cased path shows ("" for a remoting call, a page named as a login
// flow, or any other path).
func salesforceLoginFlowPageName(p string) string {
	if strings.Contains(p, "loginflow") {
		return ""
	}
	for _, marker := range []string{"/apex/", "/flow/"} {
		if _, rest, ok := strings.Cut(p, marker); ok {
			name, _, _ := strings.Cut(rest, "/")
			return marker + name
		}
	}
	return ""
}

func isSalesforceSignInStaticAsset(ex exchange) bool {
	if method := strings.ToUpper(strings.TrimSpace(ex.Method)); method != "GET" && method != "HEAD" {
		return false
	}
	if ex.Response != nil && isStaticMIMEType(headerLookup(ex.Response.Headers, "Content-Type")) {
		return true
	}
	return isStaticURL(ex.URL)
}

// isSalesforceSessionHopExtractor reports whether an extractor carries a
// desktop sign-in hop: a redirect Location or a session handoff value (sid,
// cshc, lm) that the next hop of the chain sends.
func isSalesforceSessionHopExtractor(extractor dsl.Extractor) bool {
	if strings.EqualFold(extractor.Source, "header") && strings.EqualFold(strings.TrimSpace(extractor.Expression), "Location") {
		return true
	}
	for _, token := range strings.Split(strings.ToLower(extractor.Name), "_") {
		switch token {
		case "location", "sid", "cshc", "lm":
			return true
		}
	}
	return false
}

// findDesktopSalesforceLogoutStart finds the first exchange of a trailing
// /secur/logout.jsp chain: one only the sign-out and the login page follow.
// A sign-out the recording signs in again after (a mid-iteration sign-out and
// re-sign-in, which reads the users file too) leaves the iteration with a
// session, so the next sign-out after that sign-in decides. When the deciding
// sign-out is not trailing (other Salesforce traffic follows it, or no step
// comes before it), nothing moves and the reason says why, so the tester is
// warned that later iterations replay without a session. Client telemetry
// beacons a page fires as it unloads are not Salesforce traffic. Without a
// deciding sign-out it returns -1 and "".
func findDesktopSalesforceLogoutStart(exchanges []exchange, signInEnd int) (int, string) {
	first := true
	for index := signInEnd + 1; index < len(exchanges); index++ {
		p := strings.ToLower(strings.TrimRight(urlPath(exchanges[index].URL), "/"))
		if !strings.HasSuffix(p, "/secur/logout.jsp") {
			continue
		}
		signsInAgain := -1
	tail:
		for tail := index + 1; tail < len(exchanges); tail++ {
			tp := strings.ToLower(strings.TrimRight(urlPath(exchanges[tail].URL), "/"))
			switch {
			case startsSalesforceSession(tp, exchanges[tail]):
				signsInAgain = tail
				break tail
			case isClientTelemetryExchange(exchanges[tail]):
				continue
			case tp == "/aura" || strings.HasSuffix(tp, "/aura") ||
				strings.HasPrefix(tp, "/services/") ||
				tp == "/one/one.app" ||
				strings.HasPrefix(tp, "/lightning") ||
				strings.Contains(tp, "/visualforce/") ||
				strings.Contains(tp, "/apex/"):
				return -1, "Salesforce traffic follows it in the recording"
			}
		}
		if signsInAgain >= 0 {
			// The sign-in after it restores the session: a later sign-out
			// decides.
			index, first = signsInAgain, false
			continue
		}
		// Must leave at least one application step between sign-in and sign-out.
		if first && index <= signInEnd+1 {
			return -1, "the iteration holds no step before it"
		}
		return index, ""
	}
	return -1, ""
}

// startsSalesforceSession reports whether an exchange (with lower-cased path
// p) belongs to a desktop sign-in: a login form post with un and pw, the
// frontdoor.jsp session handoff, or the Classic home page.
func startsSalesforceSession(p string, ex exchange) bool {
	if strings.HasSuffix(p, "/secur/frontdoor.jsp") || strings.HasSuffix(p, "/home/home.jsp") {
		return true
	}
	if !strings.EqualFold(ex.Method, "POST") || ex.BodyEncoding != "" || !strings.Contains(ex.Body, "pw=") {
		return false
	}
	form, err := url.ParseQuery(ex.Body)
	return err == nil && form.Has("un") && form.Has("pw")
}

func urlPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Path
}

// prepareExchanges parameterizes the login form with the users data source
// and gates the recorded traffic that cannot replay at load. It runs before
// correlation, so the recorded credentials are never correlation candidates.
func (s *salesforceSignIn) prepareExchanges(exchanges []exchange) {
	credentialsFrom := s.signedIn
	if credentialsFrom < 0 && len(s.logins) > 0 {
		credentialsFrom = s.logins[len(s.logins)-1]
	}
	// The users file holds the signed-in user's credentials; every login form
	// reads it only when they parse.
	if credentialsFrom >= 0 {
		if _, username, password, ok := salesforceLoginFormWithUser(exchanges[credentialsFrom].Body, true); ok {
			s.username, s.password, s.credentials = username, password, true
		}
	}
	rewrite := func(index int, withPassword bool) bool {
		body, _, _, ok := salesforceLoginFormWithUser(exchanges[index].Body, withPassword)
		if !ok {
			return false
		}
		if s.recordedBodies == nil {
			s.recordedBodies = map[int]string{}
		}
		s.recordedBodies[index] = exchanges[index].Body
		exchanges[index].Body = body
		return true
	}
	for _, login := range s.logins {
		if login != credentialsFrom && s.signedIn >= 0 {
			exchanges[login].ReviewFindings = append(exchanges[login].ReviewFindings, salesforceSignInFinding(salesforceFailedSignInReasonCode, "Salesforce sign-in attempt",
				"This sign-in attempt failed while recording (the login page came back). Replayed with the users file it would sign in, and the recorded flow would no longer match; remove the step."))
		}
		if s.credentials {
			rewrite(login, true)
		}
	}
	if s.credentials {
		// The identity-first page posts the same user's name before the
		// password.
		for _, login := range s.usernames {
			rewrite(login, false)
		}
	}
	// A later sign-in signs the virtual user in again from the users file. One
	// whose form cannot read it would replay the recorded user's credentials,
	// so it is gated for removal instead.
	for position, login := range s.resignIns {
		failed := salesforceSignInAttemptFailedBefore(exchanges, login, s.resignIns[position+1:])
		if failed {
			exchanges[login].ReviewFindings = append(exchanges[login].ReviewFindings, salesforceSignInFinding(salesforceFailedSignInReasonCode, "Salesforce sign-in attempt",
				"This sign-in attempt failed while recording (the login page came back). Replayed with the users file it would sign in, and the recorded flow would no longer match; remove the step."))
		}
		// Only a post that redirected signed in; a last attempt that came
		// back to the login page is no second user. Salesforce usernames
		// ignore case.
		if !failed && s.credentials && salesforceSignInRedirected(exchanges[login]) {
			if _, username, _, ok := salesforceLoginFormWithUser(exchanges[login].Body, true); ok && !strings.EqualFold(username, s.username) {
				s.otherUser++
			}
		}
		if !s.credentials || !rewrite(login, true) {
			exchanges[login].ReviewFindings = append(exchanges[login].ReviewFindings, salesforceSignInFinding(salesforceRecordedSignInReasonCode, "Salesforce sign-in again",
				"The recording signs in again with the recorded user's name and password, which cannot be read from the users file here. Replayed at load every virtual user would sign in as the recorded user; remove the step."))
		}
	}
	for _, login := range s.resignInUsernames {
		if s.credentials {
			rewrite(login, false)
		}
	}
	s.approved = gateSalesforceSignInSideEffects(exchanges)
}

// gateSalesforceSignInSideEffects gates the recorded sign-in steps that act
// on the org when replayed at load (a password reset request, an identity
// verification) and reports whether the recording approved a connected app.
// It holds for a generic script too (disable_salesforce), which replays the
// recorded sign-in as it was recorded.
func gateSalesforceSignInSideEffects(exchanges []exchange) bool {
	approved := false
	for index := range exchanges {
		ex := &exchanges[index]
		path := strings.ToLower(urlPath(ex.URL))
		switch {
		case strings.EqualFold(ex.Method, "POST") && strings.HasSuffix(path, "/secur/forgotpassword.jsp"):
			ex.ReviewFindings = append(ex.ReviewFindings, salesforceSignInFinding(salesforcePasswordResetReasonCode, "Salesforce password reset",
				"A password reset was requested while recording. Replayed at load every virtual user would request a reset for the account it signs in as; remove the step."))
		case strings.Contains(path, "/_ui/identity/verification/"):
			ex.ReviewFindings = append(ex.ReviewFindings, salesforceSignInFinding(salesforceIdentityVerificationReasonCode, "Salesforce identity verification",
				"Salesforce asked for identity verification (such as an SMS code) while recording, which cannot replay. Exempt the accounts the script signs in as from it (for example a trusted IP range or a waived MFA requirement for API-only users) and remove these steps."))
		case strings.EqualFold(ex.Method, "POST") && (strings.Contains(path, "/_ui/identity/oauth/ui/authorizationpage") || strings.HasSuffix(path, "/remoteaccessauthorizationpage.apexp")):
			approved = true
		}
	}
	return approved
}

// salesforceSignInAttemptFailedBefore reports whether the login post at login
// came back without the sign-in redirect while a later post (one of later)
// did redirect: an attempt that failed before the sign-in that worked.
func salesforceSignInAttemptFailedBefore(exchanges []exchange, login int, later []int) bool {
	redirected := func(index int) bool { return salesforceSignInRedirected(exchanges[index]) }
	return !redirected(login) && slices.ContainsFunc(later, redirected)
}

// salesforceSignInRedirected reports whether a login post answered with the
// sign-in redirect.
func salesforceSignInRedirected(ex exchange) bool {
	return ex.Response != nil && ex.Response.Status >= 300 && ex.Response.Status < 400
}

// restoreRecordedBodies puts back the recorded login bodies once the script
// is built, so the recording baseline holds what was sent.
func (s *salesforceSignIn) restoreRecordedBodies(exchanges []exchange) {
	for index, body := range s.recordedBodies {
		if index < len(exchanges) {
			exchanges[index].Body = body
		}
	}
}

func salesforceSignInFinding(code, element, detail string) importreview.Finding {
	return importreview.Finding{
		Code: code, ReasonCode: code, Severity: diag.Blocker, Gate: importreview.GateExecution,
		Element: element, Detail: detail, Resolution: "remove_step_and_remove_gate",
	}
}

// salesforceLoginFormWithUser replaces the login form's un (and pw, when
// withPassword) values with the users data source and returns their recorded,
// decoded values. The form's username field, a copy of un, reads the same
// column. Every other byte of the form stays as recorded.
func salesforceLoginFormWithUser(body string, withPassword bool) (string, string, string, bool) {
	fields := strings.Split(body, "&")
	values := make([]string, len(fields))
	var username, password string
	var haveUser, havePassword bool
	for index, field := range fields {
		key, value, _ := strings.Cut(field, "=")
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			return "", "", "", false
		}
		values[index] = decoded
		switch key {
		case "un":
			if haveUser {
				return "", "", "", false
			}
			username, haveUser = decoded, true
		case "pw":
			if havePassword {
				return "", "", "", false
			}
			password, havePassword = decoded, true
		}
	}
	if !haveUser || havePassword != withPassword {
		return "", "", "", false
	}
	for index, field := range fields {
		key, _, _ := strings.Cut(field, "=")
		switch {
		case key == "un", key == "username" && values[index] == username:
			fields[index] = key + "={{data." + salesforceUsersDataSource + ".username}}"
		case key == "pw":
			fields[index] = "pw={{data." + salesforceUsersDataSource + ".password}}"
		}
	}
	return strings.Join(fields, "&"), username, password, true
}

var salesforceUsersTemplate = regexp.MustCompile(`\{\{data\.` + salesforceUsersDataSource + `\.(username|password)\}\}`)

// addUsersDataSource gives the script its users file. A form body whose only
// templates are the users' reads them as plain values and escapes them at
// replay; any other template in a login form (a correlation spelled for the
// wire) keeps the file in wire spelling instead, so nothing is escaped twice.
func (s *salesforceSignIn) addUsersDataSource(script *dsl.Script) string {
	if !s.credentials || script == nil {
		return ""
	}
	forms := slices.Concat(s.logins, s.usernames, s.resignIns, s.resignInUsernames)
	wire := false
	for _, login := range forms {
		if login < len(script.Actions) && strings.Contains(salesforceUsersTemplate.ReplaceAllString(script.Actions[login].Body, ""), "{{") {
			wire = true
		}
	}
	username, password := s.username, s.password
	if wire {
		username, password = url.QueryEscape(username), url.QueryEscape(password)
	} else {
		for _, login := range forms {
			if login >= len(script.Actions) || !salesforceUsersTemplate.MatchString(script.Actions[login].Body) {
				continue
			}
			action := &script.Actions[login]
			if action.Metadata == nil {
				action.Metadata = map[string]any{}
			}
			action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] = dsl.ImportURLFormTemplateValueEncodingQueryComponent
		}
	}
	var rows strings.Builder
	writer := csv.NewWriter(&rows)
	_ = writer.WriteAll([][]string{{"username", "password"}, {username, password}})
	script.DataSources = append(script.DataSources, dsl.DataSource{
		Name:           salesforceUsersDataSource,
		Type:           "csv",
		Strategy:       "unique",
		FirstRowHeader: true,
		InlineData:     strings.TrimSuffix(rows.String(), "\n"),
	})
	if wire {
		return fmt.Sprintf("The login form also carries correlated values, so data source %q keeps its values URL-encoded; add each user's username and password that way.", salesforceUsersDataSource)
	}
	return ""
}

// moveToInit moves the sign-in, from the first step through the token
// exchange and the identity request after it, into init, so each virtual user
// signs in once. newIndex maps exchanges to actions (nil: one to one); the
// returned map leaves the moved exchanges out (-1), and source mappings move
// with their actions. It reports why when the sign-in cannot move.
func (s *salesforceSignIn) moveToInit(script *dsl.Script, exchanges []exchange, newIndex []int, mappings []sourceview.Mapping) ([]int, []sourceview.Mapping, int, string) {
	if script == nil || len(script.Init) > 0 {
		return newIndex, mappings, 0, ""
	}
	position := func(exchangeIndex int) int {
		if newIndex == nil {
			return exchangeIndex
		}
		if exchangeIndex < len(newIndex) {
			return newIndex[exchangeIndex]
		}
		return -1
	}
	end := position(s.token)
	if end < 0 {
		if s.desktop {
			return newIndex, mappings, 0, "the sign-in bootstrap step was folded into a background conversation"
		}
		return newIndex, mappings, 0, "the token exchange was folded into a background conversation"
	}
	if !s.desktop {
		// The app reads the signed-in user's identity right after the exchange.
		for next := s.token + 1; next < len(exchanges) && strings.EqualFold(exchanges[next].Method, "GET") && strings.Contains(urlPath(exchanges[next].URL), "/id/"); next++ {
			if p := position(next); p > end {
				end = p
			}
		}
	}
	boundary, ok := extendPastOpenTransactions(script.Actions, end+1)
	if !ok {
		return newIndex, mappings, 0, "a transaction marked during sign-in never ends"
	}
	if boundary >= len(script.Actions) {
		return newIndex, mappings, 0, "the recording holds no steps after sign-in"
	}
	// A background conversation started during sign-in must also end there.
	if hasOpenBackgroundConversation(script.Actions[:boundary]) {
		return newIndex, mappings, 0, "a background conversation started during sign-in runs on after it"
	}

	// A transaction marked across the sign-in moves whole, with the steps
	// after the sign-in it times: those still open at the first such step.
	for index := end + 1; index < boundary; index++ {
		if script.Actions[index].Type == "source_transaction" {
			continue
		}
		if s.carried == 0 {
			s.spanning = openTransactionNames(script.Actions[:index])
		}
		s.carried++
	}
	script.Init = append([]dsl.Action(nil), script.Actions[:boundary]...)
	script.Actions = append([]dsl.Action(nil), script.Actions[boundary:]...)
	if s.desktop {
		// The Location and session-ID hops of a desktop sign-in chain must
		// fail fast when a sign-in step fails instead of replaying a recorded
		// user's stale redirect or sid. Other extractors, and the steps an
		// open transaction carried into init, keep their own miss policy: a
		// less certain correlation on served content still falls back to its
		// recorded value.
		for i := 0; i <= end && i < len(script.Init); i++ {
			for j := range script.Init[i].Extractors {
				if !isSalesforceSessionHopExtractor(script.Init[i].Extractors[j]) {
					continue
				}
				script.Init[i].Extractors[j].OnMiss = "fail"
				script.Init[i].Extractors[j].DefaultValue = nil
			}
		}
	}
	moved := make([]int, len(exchanges))
	for index := range exchanges {
		p := position(index)
		switch {
		case p < 0 || p < boundary:
			moved[index] = -1
		default:
			moved[index] = p - boundary
		}
	}
	remapped := make([]sourceview.Mapping, 0, len(mappings))
	for _, mapping := range mappings {
		mapping.TargetPath = moveActionPath(mapping.TargetPath, boundary)
		remapped = append(remapped, mapping)
	}
	return moved, remapped, boundary, ""
}

// extendPastOpenTransactions moves a boundary past the end marker of every
// transaction that starts before it, so a transaction marked across the
// sign-in moves whole. It reports false when such a transaction never ends.
func extendPastOpenTransactions(actions []dsl.Action, boundary int) (int, bool) {
	for {
		extended := boundary
		for _, name := range openTransactionNames(actions[:boundary]) {
			end := -1
			for index := boundary; index < len(actions); index++ {
				if actions[index].Type == "source_transaction" && transactionMarkerEnds(actions[index]) && dsl.TransactionMarkerName(actions[index]) == name {
					end = index
					break
				}
			}
			if end < 0 {
				return boundary, false
			}
			extended = max(extended, end+1)
		}
		if extended == boundary {
			return boundary, true
		}
		boundary = extended
	}
}

// hasOpenBackgroundConversation reports whether actions start a repeating
// background conversation (http_async_start with a pattern other than
// "once") that they do not stop, so a cut after them would split it.
func hasOpenBackgroundConversation(actions []dsl.Action) bool {
	started := map[string]bool{}
	for _, action := range actions {
		switch action.Type {
		case "http_async_start":
			if pattern, _ := action.Params["pattern"].(string); pattern != "once" {
				id, _ := action.Params["id"].(string)
				started[id] = true
			}
		case "http_async_stop":
			id, _ := action.Params["id"].(string)
			delete(started, id)
		}
	}
	return len(started) > 0
}

// openTransactionNames names the transactions started and not yet ended by
// the end of actions, in name order.
func openTransactionNames(actions []dsl.Action) []string {
	open := map[string]int{}
	for _, action := range actions {
		if action.Type != "source_transaction" {
			continue
		}
		if transactionMarkerEnds(action) {
			open[dsl.TransactionMarkerName(action)]--
		} else {
			open[dsl.TransactionMarkerName(action)]++
		}
	}
	var names []string
	for name, count := range open {
		if count > 0 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

var actionPathIndex = regexp.MustCompile(`^actions\[(\d+)\]`)

// moveActionPath rewrites an actions[i] path after the first boundary actions
// moved to init.
func moveActionPath(path string, boundary int) string {
	match := actionPathIndex.FindStringSubmatch(path)
	if match == nil {
		return path
	}
	index, err := strconv.Atoi(match[1])
	if err != nil {
		return path
	}
	rest := path[len(match[0]):]
	if index < boundary {
		return fmt.Sprintf("init[%d]%s", index, rest)
	}
	return fmt.Sprintf("actions[%d]%s", index-boundary, rest)
}

// moveTeardownPath rewrites an actions[i] path after actions[startPos:] moved
// to teardown, where kept[i-startPos] is its teardown position. A path to an
// end marker that left teardown (kept -1) points at closedAt[i-startPos],
// the end marker the iteration now closes the transaction with, instead.
func moveTeardownPath(path string, startPos int, kept []int, closedAt map[int]int) string {
	match := actionPathIndex.FindStringSubmatch(path)
	if match == nil {
		return path
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || index < startPos {
		return path
	}
	rest := path[len(match[0]):]
	position := index - startPos
	if position < len(kept) {
		if kept[position] < 0 {
			return fmt.Sprintf("actions[%d]%s", closedAt[position], rest)
		}
		position = kept[position]
	}
	return fmt.Sprintf("teardown[%d]%s", position, rest)
}

// moveLogoutToTeardown moves a trailing desktop /secur/logout.jsp chain into
// teardown after sign-in moved to init, so multi-iteration runs do not sign
// out on iteration 1. A transaction open where the sign-out starts (one the
// recording marked around the steps that lead to it) keeps those steps in
// the iteration and ends before the sign-out; carried names it. When the
// sign-out cannot move, reason says why
// (the sign-out then ends every iteration and the next one replays a dead
// session); reason is "" when there is no sign-out to move.
func (s *salesforceSignIn) moveLogoutToTeardown(script *dsl.Script, exchanges []exchange, newIndex []int, mappings []sourceview.Mapping) (updatedIndex []int, remapped []sourceview.Mapping, moved int, carried []string, reason string) {
	if s == nil || script == nil || len(script.Init) == 0 {
		return newIndex, mappings, 0, nil, ""
	}
	if s.logoutStays != "" {
		return newIndex, mappings, 0, nil, s.logoutStays
	}
	if s.logoutStart < 0 || s.logoutStart >= len(exchanges) || newIndex == nil {
		return newIndex, mappings, 0, nil, ""
	}
	// The first exchange of the chain may have no action (a filtered
	// redirect hop): the chain starts at its first exchange that has one.
	startPos := -1
	for index := s.logoutStart; index < len(newIndex) && index < len(exchanges); index++ {
		if newIndex[index] >= 0 {
			startPos = newIndex[index]
			break
		}
	}
	if startPos < 0 || startPos >= len(script.Actions) {
		return newIndex, mappings, 0, nil, ""
	}
	if len(script.Teardown) > 0 {
		return newIndex, mappings, 0, nil, "the script already has teardown steps"
	}
	// Include any transaction start markers immediately preceding startPos so a
	// step transaction wrapping logout moves whole into teardown.
	for startPos > 0 && script.Actions[startPos-1].Type == "source_transaction" && !transactionMarkerEnds(script.Actions[startPos-1]) {
		startPos--
	}
	if !slices.ContainsFunc(script.Actions[:startPos], func(action dsl.Action) bool { return action.Type != "source_transaction" }) {
		return newIndex, mappings, 0, nil, "the iteration holds no step before it"
	}
	// Do not split an async background conversation across actions and teardown.
	if hasOpenBackgroundConversation(script.Actions[:startPos]) {
		return newIndex, mappings, 0, nil, "a background conversation is still running when it starts"
	}
	// A transaction still open where the sign-out starts times steps of the
	// iteration: they stay there, and the transaction ends before the
	// sign-out instead of after it (its end marker leaves teardown), so no
	// business step runs once per virtual user instead of once per iteration.
	closing := openTransactionStack(script.Actions[:startPos])
	for index := len(closing) - 1; index >= 0; index-- {
		carried = append(carried, closing[index])
	}
	teardown, kept := withoutUnmatchedEnds(script.Actions[startPos:], closing)
	movedCount := len(teardown)
	// Each end marker that left teardown is replaced by the inserted end
	// marker of the same transaction (carried is in insertion order here).
	closedAt := map[int]int{}
	for position, at := range kept {
		if at < 0 {
			closedAt[position] = startPos + max(0, slices.Index(carried, dsl.TransactionMarkerName(script.Actions[startPos+position])))
		}
	}
	actions := append([]dsl.Action(nil), script.Actions[:startPos]...)
	for _, name := range carried {
		actions = append(actions, dsl.TransactionMarker(name, false))
	}
	script.Teardown = teardown
	script.Actions = actions
	updated := make([]int, len(newIndex))
	for i, pos := range newIndex {
		if pos >= startPos {
			updated[i] = -1
		} else {
			updated[i] = pos
		}
	}
	remapped = make([]sourceview.Mapping, 0, len(mappings))
	for _, mapping := range mappings {
		mapping.TargetPath = moveTeardownPath(mapping.TargetPath, startPos, kept, closedAt)
		remapped = append(remapped, mapping)
	}
	slices.Sort(carried)
	carried = slices.Compact(carried)
	return updated, remapped, movedCount, carried, ""
}

// openTransactionStack names the transactions actions start and do not end,
// outermost first, so closing them in reverse nests the end markers.
func openTransactionStack(actions []dsl.Action) []string {
	var open []string
	for _, action := range actions {
		if action.Type != "source_transaction" {
			continue
		}
		name := dsl.TransactionMarkerName(action)
		if !transactionMarkerEnds(action) {
			open = append(open, name)
			continue
		}
		for last := len(open) - 1; last >= 0; last-- {
			if open[last] == name {
				open = slices.Delete(open, last, last+1)
				break
			}
		}
	}
	return open
}

// withoutUnmatchedEnds drops from actions the first end marker of each
// transaction in closing that actions end without starting, and returns the
// rest with kept[i] the new position of actions[i] (-1 when dropped).
func withoutUnmatchedEnds(actions []dsl.Action, closing []string) ([]dsl.Action, []int) {
	pending := map[string]int{}
	for _, name := range closing {
		pending[name]++
	}
	started := map[string]int{}
	out := make([]dsl.Action, 0, len(actions))
	kept := make([]int, len(actions))
	for index, action := range actions {
		if action.Type == "source_transaction" {
			name := dsl.TransactionMarkerName(action)
			switch {
			case !transactionMarkerEnds(action):
				started[name]++
			case started[name] > 0:
				started[name]--
			case pending[name] > 0:
				pending[name]--
				kept[index] = -1
				continue
			}
		}
		kept[index] = len(out)
		out = append(out, action)
	}
	return out, kept
}

// logoutWarnings explains where the recorded sign-out runs once the sign-in
// moved to init.
func logoutWarnings(moved int, carried []string, reason string) []string {
	switch {
	case moved > 0 && len(carried) > 0:
		return []string{fmt.Sprintf("The Salesforce sign-out runs in teardown (%d steps), so each virtual user signs out once after its final iteration. Transaction %s, open where the sign-out starts, now ends before it: the steps it times stay in every iteration, and the sign-out runs untimed.", moved, strings.Join(quoteNames(carried), ", "))}
	case moved > 0:
		return []string{fmt.Sprintf("The Salesforce sign-out runs in teardown (%d steps), so each virtual user signs out once after its final iteration.", moved)}
	case reason != "":
		return []string{"The Salesforce sign-out stays in the iteration (" + reason + "): the first iteration signs out, so every later iteration replays without a session. Delete the sign-out steps, or move them to teardown, before running more than one iteration."}
	}
	return nil
}

func quoteNames(names []string) []string {
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = strconv.Quote(name)
	}
	return quoted
}

// warnings explains the sign-in shaping to the tester.
func (s *salesforceSignIn) warnings(moved int, notMoved string) []string {
	var out []string
	switch {
	case moved > 0:
		if s.desktop {
			out = append(out, fmt.Sprintf("The Salesforce sign-in runs in init (%d steps), so each virtual user signs in once and every iteration reuses its session.", moved))
		} else {
			out = append(out, fmt.Sprintf("The Salesforce sign-in runs in init (%d steps), so each virtual user signs in once and every iteration reuses its Bearer token.", moved))
		}
		if s.carried > 0 {
			quoted := make([]string, len(s.spanning))
			for index, name := range s.spanning {
				quoted[index] = strconv.Quote(name)
			}
			noun, pronoun := "Transaction", "it times"
			if len(quoted) > 1 {
				noun, pronoun = "Transactions", "they time"
			}
			out = append(out, fmt.Sprintf("%s %s, marked across the sign-in, moved to init whole with the %d step(s) after the sign-in %s, which now run once per virtual user. To keep them in every iteration, mark the sign-in as its own transaction and record again.", noun, strings.Join(quoted, ", "), s.carried, pronoun))
		}
	case notMoved != "":
		out = append(out, "The Salesforce sign-in stays in the iteration: "+notMoved+".")
	}
	if s.credentials {
		out = append(out, fmt.Sprintf("The login form reads data source %q, one row per virtual user (unique): it holds the recorded user; add a row for every virtual user, each a perf user exempt from identity verification.", salesforceUsersDataSource))
		if s.otherUser > 0 {
			out = append(out, fmt.Sprintf("The recording signs in again as a different user (%d sign-in(s)). Replayed, every sign-in reads data source %q, so the virtual user signs in again as its own user; to replay a second user, record that flow as its own script.", s.otherUser, salesforceUsersDataSource))
		}
	}
	if s.approved {
		out = append(out, "The recording approves the app's OAuth access on the approval page, so every sign-in posts the approval. To sign in without it, pre-authorize the perf users (in the connected app, Admin approved users are pre-authorized) and record the sign-in again: the authorization code is read from the approval's response, so removing that step alone breaks the sign-in.")
	}
	return out
}

// stepBoundary is where a step transaction must start so that none spans the
// sign-in (or trailing sign-out): the first exchange after the sign-in chain,
// and the first exchange of a trailing /secur/logout.jsp chain. Nil without a
// sign-in.
func (s *salesforceSignIn) stepBoundary(exchanges []exchange) map[int]bool {
	if s == nil || s.token < 0 || s.token >= len(exchanges) {
		return nil
	}
	next := s.token + 1
	if !s.desktop {
		for next < len(exchanges) && strings.EqualFold(exchanges[next].Method, "GET") && strings.Contains(urlPath(exchanges[next].URL), "/id/") {
			next++
		}
	}
	if next >= len(exchanges) {
		return nil
	}
	boundaries := map[int]bool{next: true}
	if s.logoutStart > next && s.logoutStart < len(exchanges) {
		boundaries[s.logoutStart] = true
	}
	return boundaries
}
