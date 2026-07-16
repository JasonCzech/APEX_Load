package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type ciCheckStubState struct {
	mu          sync.Mutex
	tokenCalls  int
	createCalls int
	updateCalls int
	creates     []map[string]any
	updates     []map[string]any
	externalID  string
}

func TestValidateAcceptedCIGitHubCheckRejectsStaleOrMalformedRecovery(t *testing.T) {
	const (
		testID = "ci-run-1"
		sha    = "0123456789abcdef0123456789abcdef01234567"
	)
	base := githubCheckRun{
		ID: 77, Status: "in_progress", HTMLURL: "https://github.example.test/checks/77",
		ExternalID: testID, HeadSHA: sha,
	}
	testRun := &ManagedTest{ID: testID, Status: "QUEUED"}
	metadata := ciRunMetadata{Source: ciRunSource{SHA: sha}}
	tests := []struct {
		name   string
		mutate func(*githubCheckRun)
		ok     bool
	}{
		{name: "accepted in progress", ok: true},
		{name: "accepted queued", mutate: func(check *githubCheckRun) { check.Status = "queued" }, ok: true},
		{name: "completed stale check", mutate: func(check *githubCheckRun) {
			check.Status, check.Conclusion = "completed", "success"
		}},
		{name: "terminal conclusion", mutate: func(check *githubCheckRun) { check.Conclusion = "failure" }},
		{name: "missing id", mutate: func(check *githubCheckRun) { check.ID = 0 }},
		{name: "missing url", mutate: func(check *githubCheckRun) { check.HTMLURL = "" }},
		{name: "missing external id", mutate: func(check *githubCheckRun) { check.ExternalID = "" }},
		{name: "wrong external id", mutate: func(check *githubCheckRun) { check.ExternalID = "old-ci-run" }},
		{name: "missing commit", mutate: func(check *githubCheckRun) { check.HeadSHA = "" }},
		{name: "wrong commit", mutate: func(check *githubCheckRun) {
			check.HeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check := base
			if test.mutate != nil {
				test.mutate(&check)
			}
			err := validateAcceptedCIGitHubCheck(&check, testRun, metadata)
			if test.ok && err != nil {
				t.Fatalf("valid Check rejected: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("stale or malformed Check was accepted")
			}
		})
	}
}

func TestStoredCIGitHubCheckSelectsOnlyCanonicalControllerLink(t *testing.T) {
	server, store := newCIContractTestServer(t, ciTestActionsPolicy())
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-selection-script", ProjectID: "project-ci", Name: "Selection", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Check selection", ProjectID: "project-ci", ScriptID: script.ID, VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGitHubCheckLink(&storage.GitHubCheckLink{
		ID: "manual-check", Kind: "manual", TestID: testRun.ID, ScriptID: script.ID,
		Owner: "octo", Repo: "apex", CheckRunID: 41, Status: "in_progress",
	}); err != nil {
		t.Fatal(err)
	}
	if link, err := storedCIGitHubCheck(store, testRun.ID); err != nil || link != nil {
		t.Fatalf("manual Check selected as controller CI Check: %#v, %v", link, err)
	}
	canonicalID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+testRun.ID)).String()
	if err := store.SaveGitHubCheckLink(&storage.GitHubCheckLink{
		ID: canonicalID, Kind: "ci", TestID: testRun.ID,
		Owner: "octo", Repo: "apex", CheckRunID: 42, Status: "in_progress",
	}); err != nil {
		t.Fatal(err)
	}
	link, err := storedCIGitHubCheck(store, testRun.ID)
	if err != nil || link == nil || link.ID != canonicalID || link.CheckRunID != 42 {
		t.Fatalf("canonical controller Check = %#v, %v", link, err)
	}
}

func TestStoredCIGitHubCheckForRunRejectsStaleSourceBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*storage.GitHubCheckLink)
		ok     bool
	}{
		{name: "matching binding", ok: true},
		{name: "wrong owner", mutate: func(link *storage.GitHubCheckLink) { link.Owner = "other" }},
		{name: "wrong repository", mutate: func(link *storage.GitHubCheckLink) { link.Repo = "other" }},
		{name: "wrong commit", mutate: func(link *storage.GitHubCheckLink) { link.CommitSHA = strings.Repeat("a", 40) }},
		{name: "missing URL", mutate: func(link *storage.GitHubCheckLink) { link.CheckRunURL = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, store := newCIContractTestServer(t, ciTestActionsPolicy())
			script, err := server.runner.StoreScriptDurable(&dsl.Script{
				ID: "ci-check-binding-script", ProjectID: "project-ci", Name: "Binding", Version: "1.0.0", Protocol: "http",
				Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			testRun, err := server.runner.CreateTest(CreateTestRequest{
				Name: "Check binding", ProjectID: "project-ci", ScriptID: script.ID, VUsers: 1, Duration: "1m",
			})
			if err != nil {
				t.Fatal(err)
			}
			metadata := ciRunMetadata{Source: ciRunSource{
				Repository: "octo/apex",
				SHA:        "0123456789abcdef0123456789abcdef01234567",
			}}
			link := storage.GitHubCheckLink{
				ID:   uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+testRun.ID)).String(),
				Kind: "ci", TestID: testRun.ID, Owner: "octo", Repo: "apex",
				CommitSHA: metadata.Source.SHA, CheckRunID: 42,
				CheckRunURL: "https://github.example.test/octo/apex/runs/42", Status: "in_progress",
			}
			if test.mutate != nil {
				test.mutate(&link)
			}
			if err := store.SaveGitHubCheckLink(&link); err != nil {
				t.Fatal(err)
			}
			_, err = storedCIGitHubCheckForRun(store, testRun, metadata)
			if test.ok && err != nil {
				t.Fatalf("matching stored Check rejected: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("stale stored Check binding was accepted")
			}
		})
	}
}

func TestValidateCompletedCIGitHubCheckRejectsMismatchedResponse(t *testing.T) {
	const (
		testID = "ci-run-terminal"
		sha    = "0123456789abcdef0123456789abcdef01234567"
	)
	link := &storage.GitHubCheckLink{CheckRunID: 77}
	testRun := &ManagedTest{ID: testID}
	metadata := ciRunMetadata{Source: ciRunSource{SHA: sha}}
	base := githubCheckRun{
		ID: 77, Status: "completed", Conclusion: "success",
		ExternalID: testID, HeadSHA: sha, HTMLURL: "https://github.example.test/checks/77",
	}
	for _, test := range []struct {
		name   string
		mutate func(*githubCheckRun)
		ok     bool
	}{
		{name: "matching response", ok: true},
		{name: "wrong id", mutate: func(check *githubCheckRun) { check.ID = 88 }},
		{name: "nonterminal status", mutate: func(check *githubCheckRun) { check.Status = "in_progress" }},
		{name: "wrong conclusion", mutate: func(check *githubCheckRun) { check.Conclusion = "failure" }},
		{name: "missing external id", mutate: func(check *githubCheckRun) { check.ExternalID = "" }},
		{name: "wrong commit", mutate: func(check *githubCheckRun) { check.HeadSHA = strings.Repeat("a", 40) }},
		{name: "missing URL", mutate: func(check *githubCheckRun) { check.HTMLURL = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			check := base
			if test.mutate != nil {
				test.mutate(&check)
			}
			err := validateCompletedCIGitHubCheck(&check, link, testRun, metadata, "success")
			if test.ok && err != nil {
				t.Fatalf("matching terminal Check rejected: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("mismatched terminal Check response was accepted")
			}
		})
	}
}

func TestEnsureCIGitHubCheckDoesNotSerializeUnrelatedRuns(t *testing.T) {
	server, _ := newCIContractTestServer(t, ciTestActionsPolicy())
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-concurrency-script", ProjectID: "project-ci", Name: "Concurrent Checks", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const (
		firstSHA  = "1111111111111111111111111111111111111111"
		secondSHA = "2222222222222222222222222222222222222222"
	)
	type runCase struct {
		test     *ManagedTest
		metadata ciRunMetadata
	}
	makeRun := func(name, sha string) runCase {
		testRun, createErr := server.runner.CreateTest(CreateTestRequest{
			Name: name, ProjectID: "project-ci", ScriptID: script.ID, VUsers: 1, Duration: "1m",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		metadata := ciRunMetadata{
			SchemaVersion: ciRunSchemaVersion,
			Source:        ciRunSource{Repository: "octo/apex", SHA: sha},
			PublishCheck:  true, CheckInstallationID: 101,
		}
		if saveErr := server.saveCIGitHubCheckIntent(testRun.ID, testRun.ProjectID, metadata); saveErr != nil {
			t.Fatal(saveErr)
		}
		return runCase{test: testRun, metadata: metadata}
	}
	first := makeRun("First Check", firstSHA)
	second := makeRun("Second Check", secondSHA)

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var firstOnce, secondOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, firstSHA):
			firstOnce.Do(func() { close(firstEntered) })
			<-releaseFirst
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, secondSHA):
			secondOnce.Do(func() { close(secondEntered) })
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			id := int64(72)
			if body["head_sha"] == firstSHA {
				id = 71
			}
			writeJSON(w, http.StatusCreated, map[string]any{
				"id": id, "status": "in_progress", "html_url": fmt.Sprintf("%s/runs/%d", stub.URL, id),
				"external_id": body["external_id"], "head_sha": body["head_sha"], "app": map[string]any{"id": 42},
			})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer stub.Close()
	base, err := url.Parse(stub.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &githubClient{
		http: stub.Client(), apiBase: stub.URL, appID: 42,
		allowed: map[string]struct{}{base.Hostname(): {}},
	}
	errs := make(chan error, 2)
	go func() {
		_, ensureErr := server.ensureCIGitHubCheck(context.Background(), client, first.test, first.metadata)
		errs <- ensureErr
	}()
	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("first Check creation did not reach GitHub")
	}
	go func() {
		_, ensureErr := server.ensureCIGitHubCheck(context.Background(), client, second.test, second.metadata)
		errs <- ensureErr
	}()
	concurrent := false
	select {
	case <-secondEntered:
		concurrent = true
	case <-time.After(500 * time.Millisecond):
	}
	release()
	for range 2 {
		if ensureErr := <-errs; ensureErr != nil {
			t.Fatalf("ensure GitHub Check: %v", ensureErr)
		}
	}
	if !concurrent {
		t.Fatal("a blocked GitHub request serialized Check creation for an unrelated CI run")
	}
}

func (state *ciCheckStubState) snapshot() (int, int, int, []map[string]any, []map[string]any) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.tokenCalls, state.createCalls, state.updateCalls, append([]map[string]any(nil), state.creates...), append([]map[string]any(nil), state.updates...)
}

func TestControllerOwnedGitHubCheckLifecycle(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_APP_KEY", privateKey)

	state := &ciCheckStubState{}
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			state.mu.Lock()
			state.tokenCalls++
			state.mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/actions/runs/9001":
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 9001, "event": "push", "head_sha": "0123456789abcdef0123456789abcdef01234567",
				"repository":      map[string]any{"id": 12345, "full_name": "octo/apex"},
				"head_repository": map[string]any{"id": 12345, "full_name": "octo/apex"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/commits/0123456789abcdef0123456789abcdef01234567/check-runs":
			if r.URL.Query().Get("check_name") != ciGitHubCheckName || r.URL.Query().Get("filter") != "all" {
				t.Errorf("unexpected Check recovery query: %s", r.URL.RawQuery)
			}
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create Check body: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.createCalls++
			state.creates = append(state.creates, body)
			state.mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{
				"id": 77, "status": "in_progress", "html_url": stub.URL + "/octo/apex/runs/77",
				"external_id": body["external_id"], "head_sha": body["head_sha"],
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/octo/apex/check-runs/77":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode update Check body: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.updateCalls++
			state.updates = append(state.updates, body)
			state.mu.Unlock()
			state.mu.Lock()
			externalID := state.externalID
			state.mu.Unlock()
			response := map[string]any{
				"id": 77, "status": body["status"], "html_url": stub.URL + "/octo/apex/runs/77",
				"external_id": externalID, "head_sha": "0123456789abcdef0123456789abcdef01234567",
			}
			if conclusion, ok := body["conclusion"]; ok {
				response["conclusion"] = conclusion
			}
			writeJSON(w, http.StatusOK, response)
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer stub.Close()

	actionsPolicy := ciTestActionsPolicy()
	actionsPolicy.ServerURL = stub.URL
	server, store := newCIContractTestServer(t, actionsPolicy)
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
		InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected",
		PermissionsJSON: `{"checks":"write"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveGitHubAppInstallation: %v", err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_APP_KEY",
		APIBaseURL: stub.URL, WebBaseURL: stub.URL,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-script", ProjectID: "project-ci", Name: "Check lifecycle", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	source := ciTestRunSource("1")
	source.ServerURL, source.APIURL = stub.URL, stub.URL
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + script.ID,
		RequireSLA: true, RequireResults: true, PublishCheck: true, CheckInstallationID: 101,
		PolicyRevision: actionsPolicy.Revision, IdempotencySHA: sha256String("ci-check-lifecycle-idempotency-key"),
	}
	materializationKey, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "CI Check lifecycle", ProjectID: "project-ci", ScriptID: script.ID, VUsers: 1, Duration: "1m",
		MaterializationKey: materializationKey,
		GoalConfig: &GoalConfig{
			P95LatencyMs: ptr(250.0), MaxErrorPct: ptr(5.0),
			MaxAvgServerProcessingMs: ptr(100.0), MaxAvgTransferMs: ptr(50.0), MaxErrorCount: ptr(10),
		},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	state.mu.Lock()
	state.externalID = testRun.ID
	state.mu.Unlock()
	projectPolicy := storage.GitHubProjectPolicy{AppInstallationID: 101, Actions: actionsPolicy}
	client, err := server.validateCICheckPublishing(context.Background(), projectPolicy, source)
	if err != nil {
		t.Fatalf("validateCICheckPublishing: %v", err)
	}
	if err := server.saveCIGitHubCheckIntent(testRun.ID, testRun.ProjectID, metadata); err != nil {
		t.Fatalf("saveCIGitHubCheckIntent: %v", err)
	}
	link, err := server.createCIGitHubCheck(context.Background(), client, projectPolicy, testRun, metadata)
	if err != nil {
		t.Fatalf("createCIGitHubCheck: %v", err)
	}
	if link.CheckRunID != 77 || link.Status != "in_progress" || link.Conclusion != "" {
		t.Fatalf("created Check link = %#v", link)
	}
	manualPublishedAt := link.PublishedAt.Add(time.Minute)
	if err := store.SaveGitHubCheckLink(&storage.GitHubCheckLink{
		ID: "newer-manual-check", Kind: "manual", TestID: testRun.ID, ScriptID: script.ID,
		Owner: "octo", Repo: "apex", CommitSHA: source.SHA, CheckRunID: 999,
		CheckRunURL: stub.URL + "/octo/apex/runs/999", Status: "in_progress",
		PublishedAt: manualPublishedAt, UpdatedAt: manualPublishedAt,
	}); err != nil {
		t.Fatalf("SaveGitHubCheckLink(manual): %v", err)
	}
	_, createCalls, updateCalls, creates, _ := state.snapshot()
	if createCalls != 1 || updateCalls != 0 || len(creates) != 1 {
		t.Fatalf("Check API calls after create = create:%d update:%d", createCalls, updateCalls)
	}
	if creates[0]["status"] != "in_progress" || creates[0]["head_sha"] != source.SHA || creates[0]["external_id"] != testRun.ID {
		t.Fatalf("create Check body = %#v", creates[0])
	}
	if _, exists := creates[0]["conclusion"]; exists {
		t.Fatalf("in-progress Check carried a final conclusion: %#v", creates[0])
	}

	complete := *testRun
	complete.Status = "COMPLETE"
	complete.LiveMetrics = &LiveMetrics{
		P95Ms: 180, ErrorPct: 1.67, TotalErrors: 2,
		Transactions: map[string]*TxMetrics{
			"health": {ServerTelemetry: &ServerTelemetrySummary{Samples: 1, AvgServerProcessingMs: 80, AvgTransferMs: 20}},
		},
	}
	persistValidCIRunArchive(t, store, &complete, 120, true, false, nil)
	passed := ciRunResponse{
		Status: "COMPLETE", Outcome: "passed", Transactions: 120, Errors: 2, TPS: 40, DurationMs: 3000,
		SLAEvaluated: true, ResultsPresent: true, ReportURL: "https://apex.example.test/tests/" + testRun.ID,
	}
	checkURL, err := server.syncCIGitHubCheck(context.Background(), &complete, metadata, passed)
	if err != nil {
		t.Fatalf("sync passed Check: %v", err)
	}
	if checkURL != stub.URL+"/octo/apex/runs/77" {
		t.Fatalf("check URL = %q", checkURL)
	}
	_, _, updateCalls, _, updates := state.snapshot()
	if updateCalls != 1 || updates[0]["status"] != "completed" || updates[0]["conclusion"] != "success" {
		t.Fatalf("passed Check updates = %#v", updates)
	}
	output, _ := updates[0]["output"].(map[string]any)
	summary, _ := output["summary"].(string)
	for _, required := range []string{
		"120", "octo/apex@" + source.SHA, source.WorkflowRef,
		"| P95 latency | ≤ 250.00 ms | 180.00 ms | `PASS` |",
		"| Error rate | ≤ 5.00% | 1.67% | `PASS` |",
		"| Average server processing | ≤ 100.00 ms | 80.00 ms | `PASS` |",
		"| Average transfer | ≤ 50.00 ms | 20.00 ms | `PASS` |",
		"| Error count | ≤ 10 | 2 | `PASS` |",
	} {
		if !strings.Contains(summary, required) {
			t.Fatalf("Check summary missing %q: %s", required, summary)
		}
	}

	if _, err := server.syncCIGitHubCheck(context.Background(), &complete, metadata, passed); err != nil {
		t.Fatalf("idempotent sync: %v", err)
	}
	_, _, updateCalls, _, _ = state.snapshot()
	if updateCalls != 1 {
		t.Fatalf("idempotent sync issued %d updates, want 1", updateCalls)
	}

	// Once the durable Check is completed, conflicting terminal snapshots and a
	// stale nonterminal snapshot are strict no-ops.
	breached := passed
	breached.Outcome, breached.SLABreached, breached.SLADetails = "sla_breached", true, []string{"P95 latency breached threshold"}
	if _, err := server.syncCIGitHubCheck(context.Background(), &complete, metadata, breached); err != nil {
		t.Fatalf("sync breached Check: %v", err)
	}
	aborted := complete
	aborted.Status = "ABORTED"
	cancelled := passed
	cancelled.Status, cancelled.Outcome = "ABORTED", "cancelled"
	if _, err := server.syncCIGitHubCheck(context.Background(), &aborted, metadata, cancelled); err != nil {
		t.Fatalf("sync cancelled Check: %v", err)
	}
	running := complete
	running.Status = "RUNNING"
	inProgress := passed
	inProgress.Status, inProgress.Outcome = "RUNNING", "running"
	if _, err := server.syncCIGitHubCheck(context.Background(), &running, metadata, inProgress); err != nil {
		t.Fatalf("sync nonterminal Check: %v", err)
	}

	tokenCalls, _, updateCalls, _, updates := state.snapshot()
	if tokenCalls != 1 {
		t.Fatalf("installation token calls = %d, want cached single call", tokenCalls)
	}
	if updateCalls != 1 {
		t.Fatalf("total Check updates = %d, want immutable single terminal update; updates=%#v", updateCalls, updates)
	}

	links, err := store.ListGitHubCheckLinks(testRun.ID, "", 10)
	if err != nil || len(links) != 2 {
		t.Fatalf("ListGitHubCheckLinks = %#v, %v", links, err)
	}
	canonical, err := storedCIGitHubCheck(store, testRun.ID)
	if err != nil || canonical == nil {
		t.Fatalf("storedCIGitHubCheck = %#v, %v", canonical, err)
	}
	if canonical.ScriptID != "" {
		t.Fatalf("CI Check link script id = %q, want test-only lifecycle anchor", canonical.ScriptID)
	}
	if canonical.Status != "completed" || canonical.Conclusion != "success" || canonical.PendingConclusion != "" {
		t.Fatalf("persisted Check lifecycle = %#v", canonical)
	}
	manual, err := store.GetGitHubCheckLink("newer-manual-check")
	if err != nil || manual == nil || manual.Status != "in_progress" || manual.Conclusion != "" || manual.PendingConclusion != "" {
		t.Fatalf("manual Check was modified by CI synchronization: %#v, %v", manual, err)
	}
	intents, err := store.ListPendingGitHubCheckIntents(10)
	if err != nil || len(intents) != 0 {
		t.Fatalf("completed Check left pending intents = %#v, %v", intents, err)
	}
	if deleted, deleteErr := server.runner.DeleteScriptDurableInProject(script.ID, "project-ci"); deleteErr != nil || !deleted {
		t.Fatalf("DeleteScriptDurableInProject = %t, %v", deleted, deleteErr)
	}
	links, err = store.ListGitHubCheckLinks(testRun.ID, "", 10)
	if err != nil || len(links) != 2 {
		t.Fatalf("script cleanup removed test-anchored CI Check link: %#v, %v", links, err)
	}
	canonical, err = storedCIGitHubCheck(store, testRun.ID)
	if err != nil || canonical == nil || canonical.Status != "completed" || canonical.Conclusion != "success" {
		t.Fatalf("script cleanup changed the canonical CI Check: %#v, %v", canonical, err)
	}
}

func TestCIGitHubCheckReconciliationRecoversIntentAfterRestart(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_RECOVERY_KEY", privateKey)

	const (
		testID = "7aa02adb-8398-4fc4-9748-218d07a0712a"
		sha    = "0123456789abcdef0123456789abcdef01234567"
	)
	state := &ciCheckStubState{}
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			state.mu.Lock()
			state.tokenCalls++
			state.mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/commits/"+sha+"/check-runs":
			writeJSON(w, http.StatusOK, map[string]any{
				"total_count": 1,
				"check_runs": []map[string]any{{
					"id": 88, "name": ciGitHubCheckName, "head_sha": sha, "external_id": testID,
					"status": "in_progress", "html_url": stub.URL + "/octo/apex/runs/88", "app": map[string]any{"id": 12345},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			state.mu.Lock()
			state.createCalls++
			state.mu.Unlock()
			http.Error(w, "duplicate Check creation", http.StatusConflict)
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer stub.Close()

	policy := ciTestActionsPolicy()
	policy.ServerURL = stub.URL
	server, store := newCIContractTestServer(t, policy)
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
		InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected",
		PermissionsJSON: `{"checks":"write"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_RECOVERY_KEY",
		APIBaseURL: stub.URL, WebBaseURL: stub.URL,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-recovery-script", ProjectID: "project-ci", Name: "Recovery", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := ciTestRunSource("1")
	source.SHA = sha
	source.ServerURL = stub.URL
	source.APIURL = stub.URL
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + script.ID,
		PublishCheck: true, CheckInstallationID: 101, PolicyRevision: policy.Revision, IdempotencySHA: sha256String("restart-recovery"),
	}
	materializationKey, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: testID, Name: "CI Check recovery", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m", MaterializationKey: materializationKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.saveCIGitHubCheckIntent(testRun.ID, testRun.ProjectID, metadata); err != nil {
		t.Fatal(err)
	}
	if links, err := store.ListGitHubCheckLinks(testID, "", 10); err != nil || len(links) != 0 {
		t.Fatalf("pre-restart links = %#v, %v", links, err)
	}

	// Simulate the process stopping after GitHub accepted the Check but before
	// the local link was saved. The durable intent and test must survive reopen.
	dbPath := store.BackendInfo().DSN
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := server.Shutdown(shutdownCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewServer(ServerConfig{Store: reopened, MaxConcurrent: 4})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = restarted.Shutdown(ctx)
		_ = reopened.Close()
	})
	restarted.reconcileIncompleteCIGitHubChecks(context.Background())

	links, err := reopened.ListGitHubCheckLinks(testID, "", 10)
	if err != nil || len(links) != 1 || links[0].CheckRunID != 88 {
		t.Fatalf("recovered Check link = %#v, %v", links, err)
	}
	intent, err := reopened.GetGitHubCheckIntent(testID)
	if err != nil || intent != nil {
		t.Fatalf("recovered Check left intent = %#v, %v", intent, err)
	}
	_, createCalls, _, _, _ := state.snapshot()
	if createCalls != 0 {
		t.Fatalf("recovery created %d duplicate Checks", createCalls)
	}
}

func TestCIGitHubCheckCreationClaimRenewsAcrossSlowGitHubRequests(t *testing.T) {
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	const (
		testID = "f24429c7-334f-4a48-a57e-e5c80cd2de62"
		sha    = "0123456789abcdef0123456789abcdef01234567"
	)
	var (
		mu          sync.Mutex
		findCalls   int
		createCalls int
		findStarted = make(chan struct{})
		findOnce    sync.Once
	)
	slowResponse := func(r *http.Request) bool {
		select {
		case <-time.After(2 * time.Second):
			return true
		case <-r.Context().Done():
			return false
		}
	}
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/commits/"+sha+"/check-runs":
			mu.Lock()
			findCalls++
			mu.Unlock()
			findOnce.Do(func() { close(findStarted) })
			if !slowResponse(r) {
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			if !slowResponse(r) {
				return
			}
			mu.Lock()
			createCalls++
			checkID := int64(80 + createCalls)
			mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{
				"id": checkID, "name": ciGitHubCheckName, "head_sha": sha, "external_id": testID,
				"status": "in_progress", "html_url": fmt.Sprintf("%s/octo/apex/runs/%d", stub.URL, checkID),
			})
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer stub.Close()

	server, store := newCIContractTestServer(t, ciTestActionsPolicy())
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-slow-claim-script", ProjectID: "project-ci", Name: "Slow claim", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: testID, Name: "Slow Check creation", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := ciTestRunSource("1")
	source.SHA = sha
	metadata := ciRunMetadata{SchemaVersion: ciRunSchemaVersion, Source: source, PublishCheck: true}
	if err := store.SaveGitHubCheckIntent(&storage.GitHubCheckIntent{
		TestID: testID, ProjectID: testRun.ProjectID, InstallationID: 101,
		Owner: "octo", Repo: "apex", CommitSHA: sha,
	}); err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(stub.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &githubClient{
		http: stub.Client(), apiBase: stub.URL, webBase: stub.URL,
		allowed: map[string]struct{}{base.Hostname(): {}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results := make(chan struct {
		link *storage.GitHubCheckLink
		err  error
	}, 2)
	runEnsure := func() {
		link, ensureErr := server.ensureCIGitHubCheckWithClaimTiming(ctx, client, testRun, metadata, time.Second, 100*time.Millisecond)
		results <- struct {
			link *storage.GitHubCheckLink
			err  error
		}{link: link, err: ensureErr}
	}
	go runEnsure()
	select {
	case <-findStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Start a competing replica after the original non-renewed lease would have
	// expired, while the first replica is still inside the slow GitHub lookup.
	time.Sleep(1400 * time.Millisecond)
	go runEnsure()

	var firstID int64
	for range 2 {
		result := <-results
		if result.err != nil || result.link == nil {
			t.Fatalf("slow Check creation result = %#v, %v", result.link, result.err)
		}
		if firstID == 0 {
			firstID = result.link.CheckRunID
		} else if result.link.CheckRunID != firstID {
			t.Fatalf("replicas observed different Check ids: %d and %d", firstID, result.link.CheckRunID)
		}
	}
	mu.Lock()
	gotFindCalls, gotCreateCalls := findCalls, createCalls
	mu.Unlock()
	if gotFindCalls != 1 || gotCreateCalls != 1 {
		t.Fatalf("slow HA creation issued find=%d create=%d, want one of each", gotFindCalls, gotCreateCalls)
	}
	if intent, err := store.GetGitHubCheckIntent(testID); err != nil || intent != nil {
		t.Fatalf("successful claimed creation left intent = %#v, %v", intent, err)
	}
}

func TestCIGitHubCheckReconciliationCapsAndRotatesPoisonedIntents(t *testing.T) {
	server, store := newCIContractTestServer(t, ciTestActionsPolicy())
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-poisoned-intent-script", ProjectID: "project-ci", Name: "Poisoned intent", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const extraIntents = 5
	intentIDs := make([]string, 0, ciGitHubCheckIntentBatch+extraIntents)
	initial := make(map[string]time.Time, ciGitHubCheckIntentBatch+extraIntents)
	for i := 0; i < ciGitHubCheckIntentBatch+extraIntents; i++ {
		testRun, createErr := server.runner.CreateTest(CreateTestRequest{
			Name: fmt.Sprintf("Poisoned CI intent %d", i), ProjectID: "project-ci", ScriptID: script.ID,
			VUsers: 1, Duration: "1m", MaterializationKey: fmt.Sprintf("invalid-ci-metadata-%02d", i),
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		intent := &storage.GitHubCheckIntent{
			TestID: testRun.ID, ProjectID: "project-ci", InstallationID: 101,
			Owner: "octo", Repo: "apex", CommitSHA: ciTestRunSource("1").SHA,
		}
		if err := store.SaveGitHubCheckIntent(intent); err != nil {
			t.Fatal(err)
		}
		stored, err := store.GetGitHubCheckIntent(testRun.ID)
		if err != nil || stored == nil {
			t.Fatalf("stored intent %q = %#v, %v", testRun.ID, stored, err)
		}
		intentIDs = append(intentIDs, testRun.ID)
		initial[testRun.ID] = stored.UpdatedAt
	}

	time.Sleep(5 * time.Millisecond)
	server.reconcileIncompleteCIGitHubChecks(context.Background())
	changed := 0
	untouched := make([]string, 0, extraIntents)
	firstPass := make(map[string]time.Time, len(intentIDs))
	for _, id := range intentIDs {
		intent, err := store.GetGitHubCheckIntent(id)
		if err != nil || intent == nil {
			t.Fatalf("intent after first pass %q = %#v, %v", id, intent, err)
		}
		firstPass[id] = intent.UpdatedAt
		if intent.UpdatedAt.After(initial[id]) {
			changed++
		} else {
			untouched = append(untouched, id)
		}
	}
	if changed != ciGitHubCheckIntentBatch || len(untouched) != extraIntents {
		t.Fatalf("first pass touched %d intents and left %d untouched; want %d and %d", changed, len(untouched), ciGitHubCheckIntentBatch, extraIntents)
	}

	time.Sleep(5 * time.Millisecond)
	server.reconcileIncompleteCIGitHubChecks(context.Background())
	for _, id := range untouched {
		intent, err := store.GetGitHubCheckIntent(id)
		if err != nil || intent == nil || !intent.UpdatedAt.After(firstPass[id]) {
			t.Fatalf("oldest untouched intent %q was not rotated on the next pass: %#v, %v", id, intent, err)
		}
	}
}

func TestCIGitHubCheckReconciliationCancelsWithLeadershipEpoch(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_LEADERSHIP_KEY", privateKey)
	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var startedOnce sync.Once
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/101/access_tokens" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		startedOnce.Do(func() { close(requestStarted) })
		select {
		case <-r.Context().Done():
		case <-releaseHandler:
		}
	}))
	defer stub.Close()
	defer close(releaseHandler)

	policy := ciTestActionsPolicy()
	policy.ServerURL = stub.URL
	server, store := newCIContractTestServer(t, policy)
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
		InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected",
		PermissionsJSON: `{"checks":"write"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_LEADERSHIP_KEY",
		APIBaseURL: stub.URL, WebBaseURL: stub.URL,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-leadership-script", ProjectID: "project-ci", Name: "Leadership", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := ciTestRunSource("1")
	source.ServerURL = stub.URL
	source.APIURL = stub.URL
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + script.ID,
		PublishCheck: true, CheckInstallationID: 101, PolicyRevision: policy.Revision, IdempotencySHA: sha256String("leadership-cancellation"),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Leadership cancellation", ProjectID: "project-ci", ScriptID: script.ID, VUsers: 1, Duration: "1m", MaterializationKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.saveCIGitHubCheckIntent(testRun.ID, testRun.ProjectID, metadata); err != nil {
		t.Fatal(err)
	}

	leadership := newExecutionLeadership(store, "ci-check-reconciler")
	leadership.ttl = time.Hour
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("test leadership was not admitted")
	}
	server.runner.leadership = leadership
	done := make(chan struct{})
	go func() {
		server.reconcileIncompleteCIGitHubChecks(context.Background())
		close(done)
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("GitHub Check reconciliation did not start its remote request")
	}
	leadership.relinquish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("GitHub Check reconciliation did not stop after leadership loss")
	}
}

func TestCreateCIRunRetryRecoversAmbiguousRemoteCheckCreation(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_RETRY_KEY", privateKey)

	state := &ciCheckStubState{}
	remoteAccepted := false
	observedDurableIntent := false
	var server *Server
	var store *storage.SQLiteStore
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/actions/runs/9001":
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 9001, "event": "push", "head_sha": ciTestRunSource("1").SHA,
				"repository": map[string]any{"id": 12345, "full_name": "octo/apex"},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/octo/apex/commits/") && strings.HasSuffix(r.URL.Path, "/check-runs"):
			// This request occurs only after test materialization. Verify the
			// pre-create outbox record is already durable at that boundary.
			intents, intentErr := store.ListPendingGitHubCheckIntents(10)
			externalID := ""
			if intentErr != nil || len(intents) != 1 {
				t.Errorf("Check lookup observed intents %#v, %v", intents, intentErr)
			} else if _, exists := server.runner.GetTest(intents[0].TestID); !exists {
				t.Errorf("Check intent test %q was not materialized", intents[0].TestID)
			} else {
				externalID = intents[0].TestID
				state.mu.Lock()
				observedDurableIntent = true
				state.externalID = externalID
				state.mu.Unlock()
			}
			state.mu.Lock()
			accepted := remoteAccepted
			state.mu.Unlock()
			if accepted {
				if externalID == "" {
					http.Error(w, "missing durable intent", http.StatusInternalServerError)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"total_count": 2,
					"check_runs": []map[string]any{{
						"id": 77, "name": ciGitHubCheckName, "head_sha": ciTestRunSource("1").SHA,
						"external_id": externalID, "status": "completed", "conclusion": "success",
						"html_url": stub.URL + "/octo/apex/runs/77", "app": map[string]any{"id": 12345},
					}, {
						"id": 88, "name": ciGitHubCheckName, "head_sha": ciTestRunSource("1").SHA,
						"external_id": externalID, "status": "in_progress",
						"html_url": stub.URL + "/octo/apex/runs/88", "app": map[string]any{"id": 12345},
					}},
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			state.mu.Lock()
			state.createCalls++
			remoteAccepted = true
			state.mu.Unlock()
			// Simulate GitHub committing the Check while the response is lost.
			http.Error(w, "upstream response lost", http.StatusBadGateway)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/octo/apex/check-runs/88":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			state.mu.Lock()
			externalID := state.externalID
			state.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 88, "status": "completed", "conclusion": body["conclusion"],
				"html_url": stub.URL + "/octo/apex/runs/88", "external_id": externalID,
				"head_sha": ciTestRunSource("1").SHA,
			})
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer stub.Close()

	policy := ciTestActionsPolicy()
	policy.ServerURL = stub.URL
	server, store = newCIContractTestServer(t, policy)
	project, err := store.GetProject("project-ci")
	if err != nil || project == nil {
		t.Fatalf("GetProject = %#v, %v", project, err)
	}
	project.Settings.GitHub.AppInstallationID = 101
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
		InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected",
		PermissionsJSON: `{"checks":"write"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_RETRY_KEY",
		APIBaseURL: stub.URL, WebBaseURL: stub.URL,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}

	publishCheck := true
	scriptRaw, _ := json.Marshal(ciTestScript("https://api.example.test"))
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "1m"}, Goals: ciRunGoalsRequest{},
		Source: ciTestRunSource("1"), PublishCheck: &publishCheck,
	}
	request.Source.ServerURL, request.Source.APIURL = stub.URL, stub.URL
	invoke := func() *httptest.ResponseRecorder {
		t.Helper()
		body, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", "ambiguous-check-create-retry-9001-1")
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, req)
		return recorder
	}
	first := invoke()
	state.mu.Lock()
	observedIntent := observedDurableIntent
	state.mu.Unlock()
	if first.Code != http.StatusBadGateway || !observedIntent {
		t.Fatalf("first create = %d, body=%s, observed_intent=%t", first.Code, first.Body.String(), observedIntent)
	}
	pendingIntents, err := store.ListPendingGitHubCheckIntents(10)
	if err != nil || len(pendingIntents) != 1 {
		t.Fatalf("ambiguous Check create intents = %#v, %v", pendingIntents, err)
	}
	queued, ok := server.runner.GetTest(pendingIntents[0].TestID)
	if !ok || queued.Status != "QUEUED" || queued.StartedAt != nil {
		t.Fatalf("ambiguous Check create did not preserve a recoverable queued run: %#v", queued)
	}
	second := invoke()
	if second.Code != http.StatusAccepted {
		t.Fatalf("retry create = %d, body=%s", second.Code, second.Body.String())
	}
	_, createCalls, _, _, _ := state.snapshot()
	if createCalls != 1 {
		t.Fatalf("ambiguous retry issued %d Check creates, want 1", createCalls)
	}
	var response ciRunResponse
	if err := json.Unmarshal(second.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status == "ABORTED" || response.Outcome == "cancelled" {
		t.Fatalf("ambiguous Check retry remained cancelled instead of recovering: %#v", response)
	}
	links, err := store.ListGitHubCheckLinks(response.TestID, "", 10)
	if err != nil || len(links) != 1 || links[0].CheckRunID != 88 {
		t.Fatalf("recovered retry link = %#v, %v", links, err)
	}
	intent, err := store.GetGitHubCheckIntent(response.TestID)
	if err != nil || intent != nil {
		t.Fatalf("successful retry left intent = %#v, %v", intent, err)
	}
}

func TestCIGitHubCheckConclusionMapping(t *testing.T) {
	for _, test := range []struct{ outcome, want string }{
		{outcome: "passed", want: "success"},
		{outcome: "sla_breached", want: "failure"},
		{outcome: "execution_error", want: "failure"},
		{outcome: "configuration_error", want: "failure"},
		{outcome: "timed_out", want: "failure"},
		{outcome: "cancelled", want: "cancelled"},
	} {
		if got := ciGitHubCheckConclusion(ciRunResponse{Outcome: test.outcome}); got != test.want {
			t.Fatalf("outcome %q conclusion = %q, want %q", test.outcome, got, test.want)
		}
	}
}

func TestCIGitHubCheckSummaryRendersConfiguredSLAResultsDeterministically(t *testing.T) {
	testRun := &ManagedTest{
		Status: "COMPLETE",
		GoalConfig: &GoalConfig{
			P95LatencyMs: ptr(200.0), MaxErrorPct: ptr(1.0),
			MaxAvgServerProcessingMs: ptr(75.0), MaxAvgTransferMs: ptr(40.0), MaxErrorCount: ptr(3),
		},
		LiveMetrics: &LiveMetrics{
			P95Ms: 250, ErrorPct: 0.5, TotalErrors: 4,
			Transactions: map[string]*TxMetrics{
				"first":  {ServerTelemetry: &ServerTelemetrySummary{Samples: 1, AvgServerProcessingMs: 50, AvgTransferMs: 45}},
				"second": {ServerTelemetry: &ServerTelemetrySummary{Samples: 1, AvgServerProcessingMs: 90, AvgTransferMs: 20}},
			},
		},
	}
	metadata := ciRunMetadata{Source: ciRunSource{Repository: "octo/apex", SHA: "abc", WorkflowRef: "octo/apex/.github/workflows/ci.yml@refs/heads/main", RunID: "1", RunAttempt: "2"}}
	summary := ciGitHubCheckSummary(testRun, ciRunResponse{Status: "COMPLETE", Outcome: "sla_breached", SLAEvaluated: true, SLABreached: true}, metadata)
	wantRows := []string{
		"| P95 latency | ≤ 200.00 ms | 250.00 ms | `FAIL` |",
		"| Error rate | ≤ 1.00% | 0.50% | `PASS` |",
		"| Average server processing | ≤ 75.00 ms | 90.00 ms | `FAIL` |",
		"| Average transfer | ≤ 40.00 ms | 45.00 ms | `FAIL` |",
		"| Error count | ≤ 3 | 4 | `FAIL` |",
	}
	previous := -1
	for _, row := range wantRows {
		index := strings.Index(summary, row)
		if index < 0 {
			t.Fatalf("Check summary missing SLA row %q:\n%s", row, summary)
		}
		if index <= previous {
			t.Fatalf("Check SLA rows are not deterministic:\n%s", summary)
		}
		previous = index
	}

	pending := ciGitHubCheckSummary(&ManagedTest{Status: "QUEUED", GoalConfig: &GoalConfig{P95LatencyMs: ptr(200.0)}}, ciRunResponse{Status: "QUEUED", Outcome: "queued"}, metadata)
	if !strings.Contains(pending, "| P95 latency | ≤ 200.00 ms | Not available | `PENDING` |") {
		t.Fatalf("queued Check did not expose a pending configured SLA:\n%s", pending)
	}
	terminal := ciGitHubCheckSummary(&ManagedTest{Status: "FAILED", GoalConfig: &GoalConfig{P95LatencyMs: ptr(200.0)}}, ciRunResponse{Status: "FAILED", Outcome: "execution_error"}, metadata)
	if !strings.Contains(terminal, "| P95 latency | ≤ 200.00 ms | Not available | `NOT EVALUATED` |") {
		t.Fatalf("failed Check did not expose an unevaluated configured SLA:\n%s", terminal)
	}
}

func TestCIGitHubCheckSummaryBoundsUntrustedDetailsWithoutLosingCoreVerdict(t *testing.T) {
	testRun := &ManagedTest{
		Status: "FAILED",
		Error:  strings.Repeat("秘密", ciGitHubCheckSummaryMaxBytes),
	}
	slaDetails := make([]string, ciExternalSLADetailLimit)
	for index := range slaDetails {
		slaDetails[index] = strings.Repeat("遅延", ciGitHubCheckSummaryMaxBytes)
	}
	response := ciRunResponse{
		Status:       "FAILED",
		Outcome:      "execution_error",
		Transactions: 123,
		Errors:       7,
		SLADetails:   slaDetails,
	}
	metadata := ciRunMetadata{Source: ciRunSource{
		Repository: "octo/apex", SHA: "abc",
		WorkflowRef: "octo/apex/.github/workflows/ci.yml@refs/heads/main",
		RunID:       "1", RunAttempt: "2",
	}}

	summary := ciGitHubCheckSummary(testRun, response, metadata)
	if len(summary) > ciGitHubCheckSummaryMaxBytes {
		t.Fatalf("Check summary bytes = %d, want <= %d", len(summary), ciGitHubCheckSummaryMaxBytes)
	}
	if !utf8.ValidString(summary) {
		t.Fatal("Check summary truncation produced invalid UTF-8")
	}
	for _, core := range []string{
		"| Status | `FAILED` |",
		"| Outcome | `execution_error` |",
		"| Transactions | 123 |",
		"| Errors | 7 |",
	} {
		if !strings.Contains(summary, core) {
			t.Fatalf("bounded Check summary lost core verdict %q", core)
		}
	}
	if !strings.HasSuffix(summary, ciGitHubCheckTruncatedSuffix) {
		t.Fatalf("bounded Check summary omitted truncation notice: %q", summary[len(summary)-min(256, len(summary)):])
	}
}

func TestTruncateCIUTF8WithSuffixRepairsInvalidInput(t *testing.T) {
	result := truncateCIUTF8WithSuffix("prefix\xff秘密suffix", 16, "...")
	if len(result) > 16 || !utf8.ValidString(result) {
		t.Fatalf("bounded string = %q (%d bytes), want valid UTF-8 within 16 bytes", result, len(result))
	}
}

func TestValidateCICheckPublishingRequiresChecksWrite(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_PERMISSIONS_KEY", privateKey)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer stub.Close()
	policy := ciTestActionsPolicy()
	policy.ServerURL = stub.URL
	server, store := newCIContractTestServer(t, policy)
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{Enabled: true, ChecksEnabled: true, AppID: 1, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_PERMISSIONS_KEY", APIBaseURL: stub.URL, WebBaseURL: stub.URL}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected", PermissionsJSON: `{"contents":"read"}`, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	source := ciTestRunSource("1")
	source.ServerURL = stub.URL
	projectPolicy := storage.GitHubProjectPolicy{AppInstallationID: 101, Actions: policy}
	if _, err := server.validateCICheckPublishing(context.Background(), projectPolicy, source); err == nil || !strings.Contains(err.Error(), "Checks: write") {
		t.Fatalf("validateCICheckPublishing error = %v, want Checks: write", err)
	}
}

func TestCIGitHubCheckReconciliationRejectsGlobalAppServerDrift(t *testing.T) {
	server, _ := newCIContractTestServer(t, ciTestActionsPolicy())
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled:             true,
		AppID:               1,
		AppPrivateKeyEnvVar: "APEXLOAD_TEST_CHECK_APP_KEY",
		APIBaseURL:          "https://github.enterprise.example/api/v3",
		WebBaseURL:          "https://github.enterprise.example",
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	metadata := ciRunMetadata{
		Source:              ciTestRunSource("1"),
		CheckInstallationID: 101,
	}
	intent := &storage.GitHubCheckIntent{
		TestID: "test-1", ProjectID: "project-ci", InstallationID: 101,
		Owner: "octo", Repo: "apex", CommitSHA: metadata.Source.SHA,
	}
	if _, err := server.githubClientForCICheckIntent(context.Background(), intent, metadata); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("reconciliation accepted a GitHub App from another server: %v", err)
	}
	if _, err := server.githubSettingsForCICheckSource(metadata.Source); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("terminal Check update accepted a GitHub App from another server: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.settings.GitHub.WebBaseURL = metadata.Source.ServerURL
	server.runner.settings.GitHub.APIBaseURL = "https://api.github.com.evil.example"
	server.runner.mu.Unlock()
	if _, err := server.githubSettingsForCICheckSource(metadata.Source); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("terminal Check update accepted a GitHub App API from another server: %v", err)
	}
}
