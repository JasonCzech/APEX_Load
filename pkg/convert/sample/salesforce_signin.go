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
)

type salesforceSignIn struct {
	// token is the exchange that traded the authorization code for tokens.
	token int
	// logins are the login form posts (un and pw) before it; signedIn the one
	// that signed in (a redirect), or -1. usernames are the identity-first
	// posts (un alone) to the same login page.
	logins    []int
	usernames []int
	signedIn  int
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

// findSalesforceSignIn finds a Mobile SDK OAuth sign-in: a token exchange
// with grant_type hybrid_auth_code, or authorization_code for an app-scheme
// redirect_uri.
func findSalesforceSignIn(exchanges []exchange) *salesforceSignIn {
	for index, ex := range exchanges {
		if !strings.EqualFold(ex.Method, "POST") || !strings.HasSuffix(urlPath(ex.URL), "/services/oauth2/token") || ex.BodyEncoding != "" {
			continue
		}
		form, err := url.ParseQuery(ex.Body)
		if err != nil {
			continue
		}
		switch form.Get("grant_type") {
		case "hybrid_auth_code":
		case "authorization_code":
			redirect, err := url.Parse(form.Get("redirect_uri"))
			if err != nil || redirect.Scheme == "" || redirect.Scheme == "http" || redirect.Scheme == "https" {
				continue
			}
		default:
			continue
		}
		signIn := &salesforceSignIn{token: index, signedIn: -1}
		// One pass reads the form posts before the exchange: login posts carry
		// un and pw, identity-first posts un alone to the same login page.
		type usernamePost struct {
			index int
			path  string
		}
		var usernamePosts []usernamePost
		loginPaths := map[string]bool{}
		for login := 0; login < index; login++ {
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
			signIn.logins = append(signIn.logins, login)
			loginPaths[path] = true
			if response := ex.Response; response != nil && response.Status >= 300 && response.Status < 400 {
				signIn.signedIn = login
			}
		}
		for _, post := range usernamePosts {
			if loginPaths[post.path] {
				signIn.usernames = append(signIn.usernames, post.index)
			}
		}
		return signIn
	}
	return nil
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
	rewrite := func(index int, withPassword bool) {
		if body, _, _, ok := salesforceLoginFormWithUser(exchanges[index].Body, withPassword); ok {
			if s.recordedBodies == nil {
				s.recordedBodies = map[int]string{}
			}
			s.recordedBodies[index] = exchanges[index].Body
			exchanges[index].Body = body
		}
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
	for index := range exchanges {
		ex := &exchanges[index]
		path := strings.ToLower(urlPath(ex.URL))
		switch {
		case strings.EqualFold(ex.Method, "POST") && strings.HasSuffix(path, "/secur/forgotpassword.jsp"):
			ex.ReviewFindings = append(ex.ReviewFindings, salesforceSignInFinding(salesforcePasswordResetReasonCode, "Salesforce password reset",
				"A password reset was requested while recording. Replayed at load it would send every perf user a reset email; remove the step."))
		case strings.Contains(path, "/_ui/identity/verification/"):
			ex.ReviewFindings = append(ex.ReviewFindings, salesforceSignInFinding(salesforceIdentityVerificationReasonCode, "Salesforce identity verification",
				"Salesforce asked for identity verification (such as an SMS code) while recording, which cannot replay. Exempt the perf users from it (for example a trusted IP range or a waived MFA requirement for API-only perf users) and remove these steps."))
		case strings.EqualFold(ex.Method, "POST") && (strings.Contains(path, "/_ui/identity/oauth/ui/authorizationpage") || strings.HasSuffix(path, "/remoteaccessauthorizationpage.apexp")):
			s.approved = true
		}
	}
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
	forms := append(append([]int(nil), s.logins...), s.usernames...)
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
		return newIndex, mappings, 0, "the token exchange was folded into a background conversation"
	}
	// The app reads the signed-in user's identity right after the exchange.
	for next := s.token + 1; next < len(exchanges) && strings.EqualFold(exchanges[next].Method, "GET") && strings.Contains(urlPath(exchanges[next].URL), "/id/"); next++ {
		if p := position(next); p > end {
			end = p
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
	started := map[string]bool{}
	for _, action := range script.Actions[:boundary] {
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
	if len(started) > 0 {
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

// warnings explains the sign-in shaping to the tester.
func (s *salesforceSignIn) warnings(moved int, notMoved string) []string {
	var out []string
	switch {
	case moved > 0:
		out = append(out, fmt.Sprintf("The Salesforce sign-in runs in init (%d steps), so each virtual user signs in once and every iteration reuses its Bearer token.", moved))
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
	}
	if s.approved {
		out = append(out, "The recording approves the app's OAuth access on the approval page, so every sign-in posts the approval. To sign in without it, pre-authorize the perf users (in the connected app, Admin approved users are pre-authorized) and record the sign-in again: the authorization code is read from the approval's response, so removing that step alone breaks the sign-in.")
	}
	return out
}
