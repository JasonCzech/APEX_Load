package aurafixture

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

func newTarget(t *testing.T, opts Options) (*Fixture, *httptest.Server) {
	t.Helper()
	if opts.ReactJS == nil {
		opts.ReactJS = []byte("synthetic React test asset")
	}
	if opts.ReactDOMJS == nil {
		opts.ReactDOMJS = []byte("synthetic ReactDOM test asset")
	}
	fixture, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	return fixture, server
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 3 * time.Second}
}

func getBootstrap(t *testing.T, client *http.Client, target string) Bootstrap {
	t.Helper()
	response, err := client.Get(target + "/bootstrap")
	if err != nil {
		t.Fatal("synthetic bootstrap request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("synthetic bootstrap was rejected")
	}
	var bootstrap Bootstrap
	if err := json.NewDecoder(response.Body).Decode(&bootstrap); err != nil {
		t.Fatal("synthetic bootstrap could not be decoded")
	}
	if len(bootstrap.ActionIDs) != 2 || bootstrap.Token == "" || bootstrap.Nonce == "" || bootstrap.Context.FWUID == "" {
		t.Fatal("synthetic bootstrap is incomplete")
	}
	return bootstrap
}

func batchForm(t *testing.T, bootstrap Bootstrap, ids []string) string {
	t.Helper()
	actions := make([]requestAction, len(ids))
	for index, id := range ids {
		actions[index] = requestAction{ID: id, Descriptor: "synthetic://Account/ACTION$save"}
	}
	message, err := json.Marshal(map[string]any{"actions": actions})
	if err != nil {
		t.Fatal(err)
	}
	contextData, err := json.Marshal(bootstrap.Context)
	if err != nil {
		t.Fatal(err)
	}
	return url.Values{"message": {string(message)}, "aura.token": {bootstrap.Token}, "aura.context": {string(contextData)}}.Encode()
}

func sendBatch(t *testing.T, client *http.Client, target, body string) (int, []byte) {
	t.Helper()
	response, err := client.Post(target+"/aura", "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		t.Fatal("synthetic Aura request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("synthetic Aura response could not be read")
	}
	return response.StatusCode, data
}

func TestFixtureBootstrapScopesColdWarmAndIndependentContexts(t *testing.T) {
	fixture, server := newTarget(t, Options{})
	firstClient, secondClient := newClient(t), newClient(t)
	cold := getBootstrap(t, firstClient, server.URL)
	warm := getBootstrap(t, firstClient, server.URL)
	other := getBootstrap(t, secondClient, server.URL)
	if cold.ClientState != "cold" || warm.ClientState != "warm" || other.ClientState != "cold" || cold.Token != warm.Token || cold.Nonce != warm.Nonce || cold.Context != warm.Context || cold.ActionIDs[0] == warm.ActionIDs[0] {
		t.Fatal("warm bootstrap did not retain credentials while allocating fresh IDs")
	}
	if cold.Token == other.Token || cold.Nonce == other.Nonce || cold.Context.FWUID == other.Context.FWUID {
		t.Fatal("independent context inherited synthetic session values")
	}
	response, err := firstClient.Get(server.URL + "/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("synthetic session cookie attributes were lost")
	}
	stats := fixture.Stats()
	if stats.BootstrapRequests != 4 || stats.ColdBootstraps != 2 || stats.WarmBootstraps != 2 || stats.DistinctSessions != 2 || stats.DistinctTokens != 2 {
		t.Fatalf("unexpected safe bootstrap counts: %+v", stats)
	}
	assertStatsValueFree(t, stats, cold.Token, cold.Nonce, cold.Context.FWUID, cold.ActionIDs[0], other.Token)
}

func TestFixtureScenariosEchoActualIDsReorderedAndReportOnlyCounts(t *testing.T) {
	for _, tc := range []struct {
		scenario          Scenario
		complete, success bool
		responses         int
		state             aura.State
	}{
		{Success, true, true, 2, aura.Success},
		{Error, true, false, 2, aura.Error},
		{Incomplete, true, false, 2, aura.Incomplete},
		{Missing, false, false, 1, aura.Success},
		{Duplicate, false, false, 3, aura.Success},
	} {
		t.Run(string(tc.scenario), func(t *testing.T) {
			fixture, server := newTarget(t, Options{Scenario: tc.scenario})
			client := newClient(t)
			bootstrap := getBootstrap(t, client, server.URL)
			body := batchForm(t, bootstrap, bootstrap.ActionIDs)
			status, response := sendBatch(t, client, server.URL, body)
			if status != http.StatusOK {
				t.Fatal("application scenario changed HTTP200")
			}
			var decoded struct {
				Actions []responseAction `json:"actions"`
			}
			if json.Unmarshal(response, &decoded) != nil || len(decoded.Actions) != tc.responses {
				t.Fatal("synthetic response envelope differs from selected scenario")
			}
			if tc.scenario != Missing && decoded.Actions[0].ID != bootstrap.ActionIDs[1] {
				t.Fatal("response did not reverse actual outgoing action IDs")
			}
			analysis := aura.Validate([]byte(body), response)
			if analysis.Complete != tc.complete || analysis.Success != tc.success {
				t.Fatalf("scenario result mismatch: %+v", analysis)
			}
			if tc.scenario == Error || tc.scenario == Incomplete {
				if len(analysis.Outcomes) != 2 || analysis.Outcomes[1].State != tc.state {
					t.Fatal("failure state was not matched in request order")
				}
			}
			stats := fixture.Stats()
			if stats.AcceptedBatches != 1 || stats.RejectedBatches != 0 || stats.RequestActions != 2 || stats.ResponseActions != tc.responses || stats.DistinctActionIDs != 2 || stats.ColdBatches != 1 {
				t.Fatalf("unexpected safe batch counts: %+v", stats)
			}
			if tc.scenario == Error && stats.ErrorActions != 1 || tc.scenario == Incomplete && stats.IncompleteActions != 1 || tc.scenario == Missing && stats.MissingActions != 1 || tc.scenario == Duplicate && stats.DuplicateActions != 1 {
				t.Fatal("selected structural outcome missing from safe stats")
			}
			assertStatsValueFree(t, stats, bootstrap.Token, bootstrap.Nonce, bootstrap.Context.FWUID, bootstrap.ActionIDs[0])
		})
	}
}

func TestFixtureRejectsCrossSessionScopeAndReusedIDsAtomically(t *testing.T) {
	fixture, server := newTarget(t, Options{})
	client, otherClient := newClient(t), newClient(t)
	bootstrap := getBootstrap(t, client, server.URL)
	other := getBootstrap(t, otherClient, server.URL)
	wrongToken := bootstrap
	wrongToken.Token = other.Token
	wrongContext := bootstrap
	wrongContext.Context = other.Context
	for _, tc := range []struct {
		name      string
		client    *http.Client
		bootstrap Bootstrap
		ids       []string
	}{
		{"missing cookie", newClient(t), bootstrap, bootstrap.ActionIDs},
		{"wrong token", client, wrongToken, bootstrap.ActionIDs},
		{"wrong context", client, wrongContext, bootstrap.ActionIDs},
		{"wrong namespace", client, bootstrap, other.ActionIDs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, response := sendBatch(t, tc.client, server.URL, batchForm(t, tc.bootstrap, tc.ids))
			if status != http.StatusUnauthorized {
				t.Fatal("foreign session values were accepted")
			}
			for _, private := range []string{bootstrap.Token, bootstrap.Nonce, other.Token, other.Nonce} {
				if strings.Contains(string(response), private) {
					t.Fatal("scope rejection disclosed a session value")
				}
			}
		})
	}
	body := batchForm(t, bootstrap, bootstrap.ActionIDs)
	if status, _ := sendBatch(t, client, server.URL, body); status != http.StatusOK {
		t.Fatal("rejected requests consumed valid IDs")
	}
	warm := getBootstrap(t, client, server.URL)
	mixed := []string{bootstrap.ActionIDs[0], warm.ActionIDs[0]}
	if status, _ := sendBatch(t, client, server.URL, batchForm(t, warm, mixed)); status != http.StatusConflict {
		t.Fatal("reused ID was accepted")
	}
	if status, _ := sendBatch(t, client, server.URL, batchForm(t, warm, warm.ActionIDs)); status != http.StatusOK {
		t.Fatal("mixed rejection consumed the fresh sibling ID")
	}
	stats := fixture.Stats()
	if stats.AcceptedBatches != 2 || stats.RejectedBatches != 5 || stats.ScopeRejections != 4 || stats.ReusedIDRejections != 1 || stats.DistinctActionIDs != 4 || stats.RequestActions != 4 || stats.ColdBatches != 1 || stats.WarmBatches != 1 {
		t.Fatalf("scope/reuse safe counts incorrect: %+v", stats)
	}
}

func TestFixtureRequestBoundariesAndAmbiguousFormsRejectExplicitly(t *testing.T) {
	fixture, server := newTarget(t, Options{})
	client := newClient(t)
	bootstrap := getBootstrap(t, client, server.URL)
	valid := batchForm(t, bootstrap, bootstrap.ActionIDs)
	values, _ := url.ParseQuery(valid)
	badContext := url.Values{}
	for key, value := range values {
		badContext[key] = append([]string(nil), value...)
	}
	badContext.Set("aura.context", `{"fwuid":"discarded","fwuid":`+strconvQuote(bootstrap.Context.FWUID)+`,"mode":"PROD","app":"synthetic:reactAura"}`)
	tooMany := make([]string, maxActions+1)
	for index := range tooMany {
		tooMany[index] = actionID(bootstrap.Nonce, index+1)
	}
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"oversized", strings.Repeat("x", maxRequestBytes+1), "application/x-www-form-urlencoded", http.StatusRequestEntityTooLarge},
		{"invalid json", "message=%7B&" + valid, "application/x-www-form-urlencoded", http.StatusBadRequest},
		{"duplicate message", valid + "&message=" + url.QueryEscape(values.Get("message")), "application/x-www-form-urlencoded", http.StatusBadRequest},
		{"duplicate token", valid + "&aura.token=synthetic-other", "application/x-www-form-urlencoded", http.StatusBadRequest},
		{"duplicate context member", badContext.Encode(), "application/x-www-form-urlencoded", http.StatusBadRequest},
		{"too many actions", batchForm(t, bootstrap, tooMany), "application/x-www-form-urlencoded", http.StatusBadRequest},
		{"wrong content type", valid, "application/json", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := client.Post(server.URL+"/aura", tc.contentType, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("rejection status = %d, want %d", response.StatusCode, tc.status)
			}
		})
	}
	if stats := fixture.Stats(); stats.AcceptedBatches != 0 || stats.RejectedBatches != 7 {
		t.Fatalf("malformed request silently reached an outcome: %+v", stats)
	}
	if status, _ := sendBatch(t, client, server.URL, valid); status != http.StatusOK {
		t.Fatal("malformed requests consumed valid action IDs")
	}
}

func strconvQuote(value string) string { data, _ := json.Marshal(value); return string(data) }

func TestFixtureLimitsRetainedSessionsAndAcceptedActionIDs(t *testing.T) {
	fixture, server := newTarget(t, Options{})
	client := newClient(t)
	bootstrap := getBootstrap(t, client, server.URL)
	for batch := 0; batch < maxRetainedIDs/maxActions; batch++ {
		ids := make([]string, maxActions)
		for index := range ids {
			ids[index] = actionID(bootstrap.Nonce, batch*maxActions+index+1)
		}
		if status, _ := sendBatch(t, client, server.URL, batchForm(t, bootstrap, ids)); status != http.StatusOK {
			t.Fatal("bounded action ID capacity was rejected early")
		}
	}
	if status, _ := sendBatch(t, client, server.URL, batchForm(t, bootstrap, []string{actionID(bootstrap.Nonce, maxRetainedIDs+1)})); status != http.StatusTooManyRequests {
		t.Fatal("retained ID limit did not reject explicitly")
	}
	stats := fixture.Stats()
	if stats.DistinctActionIDs != maxRetainedIDs || stats.RequestActions != maxRetainedIDs || stats.LimitRejections != 1 {
		t.Fatalf("retained ID bound miscounted: %+v", stats)
	}
	for index := 1; index < maxSessions; index++ {
		getBootstrap(t, newClient(t), server.URL)
	}
	response, err := newClient(t).Get(server.URL + "/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	stats = fixture.Stats()
	if response.StatusCode != http.StatusTooManyRequests || stats.DistinctSessions != maxSessions || stats.DistinctTokens != maxSessions || stats.LimitRejections != 2 {
		t.Fatalf("session capacity did not stay bounded: %+v", stats)
	}
}

func TestFixtureRoutesAreLoopbackOnlyAndAssetsAreCopied(t *testing.T) {
	asset := []byte("synthetic React test asset")
	fixture, _ := newTarget(t, Options{ReactJS: asset})
	asset[0] = '!'
	for _, tc := range []struct {
		method, path, remote, host string
		status                     int
	}{
		{"GET", "/", "127.0.0.1:1234", "127.0.0.1:1234", 200},
		{"GET", "/react.js", "[::1]:1234", "localhost:1234", 200},
		{"GET", "/react-dom.js", "127.0.0.1:1234", "127.0.0.1:1234", 200},
		{"GET", "/unknown", "127.0.0.1:1234", "127.0.0.1:1234", 404},
		{"GET", "/aura", "127.0.0.1:1234", "127.0.0.1:1234", 405},
		{"POST", "/", "127.0.0.1:1234", "127.0.0.1:1234", 405},
		{"GET", "/", "198.51.100.1:1234", "127.0.0.1:1234", 403},
		{"GET", "/", "127.0.0.1:1234", "external.example:1234", 403},
		{"GET", "/", "invalid", "127.0.0.1:1234", 403},
	} {
		request := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		request.RemoteAddr = tc.remote
		response := httptest.NewRecorder()
		fixture.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, response.Code, tc.status)
		}
		if tc.path == "/react.js" && response.Code == 200 && response.Body.String()[0] == '!' {
			t.Fatal("caller mutated retained dependency bytes")
		}
	}
}

func TestFixtureOptionsAndKnownLocalDependencyBounds(t *testing.T) {
	for _, opts := range []Options{
		{},
		{ReactJS: []byte("react")},
		{ReactJS: make([]byte, maxAssetBytes+1), ReactDOMJS: []byte("dom")},
		{ReactJS: []byte("react"), ReactDOMJS: []byte("dom"), ResponseDelay: -1},
		{ReactJS: []byte("react"), ReactDOMJS: []byte("dom"), ResponseDelay: maxResponseDelay + 1},
		{ReactJS: []byte("react"), ReactDOMJS: []byte("dom"), Scenario: "PRIVATE_SCENARIO"},
	} {
		if _, err := New(opts); err == nil || strings.Contains(err.Error(), "PRIVATE_SCENARIO") {
			t.Fatal("invalid options were accepted or disclosed")
		}
	}
	fixture, _ := newTarget(t, Options{})
	if err := fixture.SetScenario("PRIVATE_SCENARIO"); err == nil || strings.Contains(err.Error(), "PRIVATE_SCENARIO") {
		t.Fatal("invalid scenario was accepted or disclosed")
	}
	if err := fixture.SetScenario(Error); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, _, err := LoadReactDependencies(dir); err == nil {
		t.Fatal("missing local dependencies were accepted")
	}
	for relative := range map[string]bool{"react/umd/react.production.min.js": true, "react-dom/umd/react-dom.production.min.js": true} {
		path := filepath.Join(dir, "node_modules", relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("local synthetic asset"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if react, dom, err := LoadReactDependencies(dir); err != nil || len(react) == 0 || len(dom) == 0 {
		t.Fatal("known bounded local dependencies unavailable")
	}
	path := filepath.Join(dir, "node_modules", "react", "umd", "react.production.min.js")
	if err := os.WriteFile(path, make([]byte, maxAssetBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReactDependencies(dir); err == nil {
		t.Fatal("oversized local dependency was admitted")
	}
}

func TestFixtureDelayCancellationKeepsAcceptedAndTransportCountsSeparate(t *testing.T) {
	fixture, server := newTarget(t, Options{ResponseDelay: time.Second})
	client := newClient(t)
	bootstrap := getBootstrap(t, client, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/aura", strings.NewReader(batchForm(t, bootstrap, bootstrap.ActionIDs)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for fixture.Stats().AcceptedBatches == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled delayed request unexpectedly completed")
	}
	deadline = time.Now().Add(time.Second)
	for fixture.Stats().TransportFailures == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stats := fixture.Stats()
	if stats.AcceptedBatches != 1 || stats.TransportFailures != 1 || stats.ResponseActions != 0 {
		t.Fatalf("transport and accepted request counts conflated: %+v", stats)
	}
}

func assertStatsValueFree(t *testing.T, stats Stats, private ...string) {
	t.Helper()
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range private {
		if strings.Contains(string(data), value) {
			t.Fatal("safe fixture stats disclosed an opaque source value")
		}
	}
	typeOf := reflect.TypeOf(stats)
	for index := 0; index < typeOf.NumField(); index++ {
		if typeOf.Field(index).Type.Kind() != reflect.Int {
			t.Fatal("fixture stats contain a non-count field")
		}
	}
}
