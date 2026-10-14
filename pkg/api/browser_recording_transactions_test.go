package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A browser recording whose operator marked transactions saves an HTTP output
// that times them with the browser script's start and end markers, and the
// markers do not unpair the saved correlation baseline from the requests it
// was recorded for: the saved script replays against a live target that
// rotates its CSRF token, and the replay's correlation diagnosis compares
// every recorded request with its own replayed one. The user's pause after the
// cart request follows its transaction's end, as VuGen records lr_think_time.
func TestSavedBrowserRecordingWithTransactionsCorrelatesAndDebugs(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "browser-transactions-baseline-key")
	const recordedToken = "rec-7f3a9c21d4e5b6a8"
	var (
		mu     sync.Mutex
		issued string
		serial int
	)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session":
			mu.Lock()
			serial++
			issued = fmt.Sprintf("live-%04d-9c3e7a1f5b", serial)
			token := issued
			mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"csrf":%q}`, token)
		case "/api/cart":
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				CSRF string `json:"csrf"`
			}
			_ = json.Unmarshal(body, &payload)
			mu.Lock()
			valid := payload.CSRF != "" && payload.CSRF == issued
			mu.Unlock()
			if !valid {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"stale csrf"}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":1}`))
		case "/api/done":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer target.Close()

	recorded := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	stamp := func(ms int) time.Time { return recorded.Add(time.Duration(ms) * time.Millisecond) }
	entry := func(ms int, method, path, requestBody, responseBody string) string {
		post := ""
		if requestBody != "" {
			post = fmt.Sprintf(`,"postData":{"mimeType":"application/json","text":%q}`, requestBody)
		}
		return `{"startedDateTime":"` + stamp(ms).Format(time.RFC3339Nano) + `","time":10,"request":{"method":"` + method + `","url":"` + target.URL + path + `","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0` + post + `},` +
			`"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"cookies":[],"content":{"size":` + fmt.Sprint(len(responseBody)) + `,"mimeType":"application/json","text":` + fmt.Sprintf("%q", responseBody) + `},"redirectURL":"","headersSize":-1,"bodySize":` + fmt.Sprint(len(responseBody)) + `},"cache":{},"timings":{"send":1,"wait":8,"receive":1}}`
	}
	artifact := recordedBrowserArtifact()
	artifact.StartURL = target.URL + "/"
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[` + strings.Join([]string{
		entry(0, "GET", "/api/session", "", `{"csrf":"`+recordedToken+`"}`),
		entry(100, "POST", "/api/cart", `{"csrf":"`+recordedToken+`","sku":"synthetic-1"}`, `{"items":1}`),
		entry(1200, "GET", "/api/done", "", `{"ok":true}`),
	}, ",") + `]}}`)
	artifact.Markers = []recbrowser.Marker{
		{Kind: recbrowser.MarkerTransactionStart, Name: "Checkout", Timestamp: stamp(50)},
		{Kind: recbrowser.MarkerTransactionStart, Name: "Cart", Timestamp: stamp(80)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Cart", Timestamp: stamp(150)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Checkout", Timestamp: stamp(1250)},
	}

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-transactions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{ID: "transactions-folder", ProjectID: "proj-1", Name: "Recordings", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(artifact), store)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)
	save := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout","also_create_http_script":true,"folder_id":"transactions-folder","expected_script_count":2}`))
	save.Header.Set("Content-Type", "application/json")
	saved := httptest.NewRecorder()
	server.mux.ServeHTTP(saved, save)
	if saved.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%s", saved.Code, saved.Body.String())
	}
	var httpStored *StoredScript
	for _, item := range server.runner.ListScripts() {
		if item.Script.Protocol == "http" {
			httpStored = item
		}
	}
	if httpStored == nil {
		t.Fatal("the HTTP output was not saved")
	}

	// The transactions are timed by markers around their requests.
	var layout []string
	for _, action := range httpStored.Script.Actions {
		if action.Type == "source_transaction" {
			function, _ := action.Params["function"].(string)
			layout = append(layout, strings.TrimPrefix(function, "lr_")+":"+dsl.TransactionMarkerName(action))
			continue
		}
		layout = append(layout, action.Method+" "+strings.TrimPrefix(action.Target, target.URL))
	}
	want := "GET /api/session|start_transaction:Checkout|start_transaction:Cart|POST /api/cart|end_transaction:Cart|GET /api/done|end_transaction:Checkout"
	if got := strings.Join(layout, "|"); got != want {
		t.Fatalf("HTTP output = %s\nwant %s", got, want)
	}

	for _, action := range httpStored.Script.Actions {
		pausesAfter := action.ThinkTimeAfter && action.ThinkTime.MeanMs > 0
		if want := action.Name == "End Cart transaction"; pausesAfter != want {
			t.Fatalf("%q pauses after = %t, want the recorded pause only after the Cart end", action.Name, pausesAfter)
		}
	}

	// The saved baseline still names each request by its action index.
	record, err := store.GetScriptRecording(httpStored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRecordingActionGraph(record, httpStored.Script); err != nil {
		t.Fatalf("saved baseline does not match the saved script: %v", err)
	}
	baseline, err := baselineObservations(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline) != 3 {
		t.Fatalf("baseline = %d observations, want one per request", len(baseline))
	}
	for _, observation := range baseline {
		action := httpStored.Script.Actions[observation.ID]
		if action.Type == "source_transaction" || !strings.HasSuffix(observation.RequestURL, strings.TrimPrefix(action.Target, target.URL)) {
			t.Fatalf("baseline observation for %s is keyed to action %d (%s %q)", observation.RequestURL, observation.ID, action.Type, action.Name)
		}
	}

	// It debugs: the correlated token is refreshed, and the markers time
	// their transactions.
	const replaySessionID = "6f0c6a0e-3b55-4b63-9a2c-7c1d8e9f0a11"
	session, err := server.scriptDebug.StartChecked(httpStored.ID, httpStored.Script, ScriptDebugStartRequest{
		ProjectID:       httpStored.Script.ProjectID,
		ReplaySessionID: replaySessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForScriptDebugStatus(t, server.scriptDebug, session.ID, ScriptDebugCompleted)
	if finished.FinalReport == nil || !finished.FinalReport.Success {
		t.Fatalf("debug replay = %#v, want success", finished.FinalReport)
	}
	var markers []string
	for _, event := range finished.FinalReport.Events {
		if !event.Success {
			t.Fatalf("replay event %q (%s) failed: %s", event.ActionName, event.ActionType, event.ErrorMessage)
		}
		if event.ActionType == "source_transaction" {
			markers = append(markers, event.ActionName)
		}
	}
	if got := strings.Join(markers, "|"); got != "Start Checkout transaction|Start Cart transaction|End Cart transaction|End Checkout transaction" {
		t.Fatalf("replayed markers = %s, want both transactions started and ended around their requests", got)
	}

	// It correlates: the diagnosis pairs all three recorded requests with
	// their replayed ones.
	diagnose := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+httpStored.ID+"/diagnose-replay",
		strings.NewReader(`{"replay_session_id":"`+replaySessionID+`"}`))
	diagnose.SetPathValue("id", httpStored.ID)
	diagnosed := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(diagnosed, diagnose)
	if diagnosed.Code != http.StatusOK {
		t.Fatalf("diagnose status = %d body=%s", diagnosed.Code, diagnosed.Body.String())
	}
	var diagnosis correlationDiagnoseResponse
	if err := json.Unmarshal(diagnosed.Body.Bytes(), &diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis.ComparedRecordedSteps != 3 || diagnosis.TotalRecordedSteps != 3 || diagnosis.Truncated || diagnosis.Blocking {
		t.Fatalf("diagnosis = %#v, want all three recorded requests compared and nothing blocking", diagnosis)
	}
}

// A Salesforce recording with marked transactions keeps its markers through
// the native Aura rewrite, which skips them as engine actions, and its
// baseline still names each request after the rewrite.
func TestSalesforceBrowserRecordingKeepsTransactionMarkersThroughNativeRewrite(t *testing.T) {
	recorded := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	stamp := func(ms int) time.Time { return recorded.Add(time.Duration(ms) * time.Millisecond) }
	har := `{"log":{"entries":[
	  {"startedDateTime":"` + stamp(0).Format(time.RFC3339Nano) + `","time":10,
	   "request":{"method":"GET","url":"https://synthetic--sandbox.sandbox.my.salesforce.com/login","headers":[]},
	   "response":{"status":200,"headers":[],"content":{"text":"{\"access_token\":\"tok+/==\"}"}}},
	  {"startedDateTime":"` + stamp(200).Format(time.RFC3339Nano) + `","time":10,
	   "request":{"method":"POST","url":"https://synthetic--sandbox.sandbox.lightning.force.com/aura","headers":[{"name":"content-type","value":"application/x-www-form-urlencoded"}],
	    "postData":{"text":"aura.token=tok%2B%2F%3D%3D&message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22apex%3A%2F%2FAccountController%2FACTION%24get%22%7D%5D%7D"}},
	   "response":{"status":200,"headers":[],"content":{"text":"{\"actions\":[{\"id\":\"1;a\",\"state\":\"SUCCESS\"}]}"}}}
	]}}`
	session := &recbrowser.Session{HAR: []byte(har), Markers: []recbrowser.Marker{
		{Kind: recbrowser.MarkerTransactionStart, Name: "Open account", Timestamp: stamp(100)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Open account", Timestamp: stamp(300)},
	}}
	server := &Server{}
	generated, err := server.createHTTPScriptFromHARAtLevel(session, &BrowserRecordingSession{Name: "Aura recording", ProjectID: "project-aura"}, "Aura recording", recbrowser.HTTPScriptLevelURL, browserHTTPScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	script := generated.Script
	if script.GeneratedBy != "apexload-salesforce-builder" {
		t.Fatalf("native draft not selected: generatedBy=%s warnings=%v", script.GeneratedBy, generated.Warnings)
	}
	var layout []string
	for _, action := range script.Actions {
		if action.Type == "source_transaction" {
			layout = append(layout, action.Name)
			continue
		}
		layout = append(layout, action.Method+" "+action.Target[strings.LastIndex(action.Target, "/"):])
		if strings.HasSuffix(action.Target, "/aura") && !strings.Contains(action.Body, "{{var.") {
			t.Fatalf("Aura token not correlated in the native draft: %s", action.Body)
		}
	}
	if got := strings.Join(layout, "|"); got != "GET /login|Start Open account transaction|POST /aura|End Open account transaction" {
		t.Fatalf("native draft = %s", got)
	}
	if len(generated.Baseline) != 2 {
		t.Fatalf("baseline = %d observations, want one per request", len(generated.Baseline))
	}
	for _, observation := range generated.Baseline {
		action := script.Actions[observation.ID]
		if action.Type == "source_transaction" || !strings.HasSuffix(observation.RequestURL, action.Target[strings.LastIndex(action.Target, "/"):]) {
			t.Fatalf("baseline observation for %s is keyed to action %d (%s %q)", observation.RequestURL, observation.ID, action.Type, action.Name)
		}
	}
}
