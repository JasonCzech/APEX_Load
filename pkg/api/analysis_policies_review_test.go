package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

const acceptanceDefault = `{"version":1,"apply_by_default":true,"mode":"fail_ci","max_loss_ratio":0}`

func TestAnalysisPolicyValidationMatchesTheCatalogAndSpecBounds(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	cases := []struct {
		kind, definition string
		accepted         bool
	}{
		// Derived operations must be a derivation the metric declares, with
		// only that operation's parameters in the engine's range.
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"delta","metric":"response_time","population":"action"}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"avg","metric":"response_time","population":"action"}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"moving_mean","metric":"response_time","population":"action","parameters":{"window_buckets":1e9}}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"moving_mean","metric":"response_time","population":"action","parameters":{"window_buckets":2.5}}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"moving_mean","metric":"response_time","population":"action","parameters":{"window_buckets":5,"alpha":1}}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"moving_mean","metric":"response_time","population":"action"}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"moving_stddev","metric":"response_time","population":"action","parameters":{"window_buckets":5,"minimum_coverage":0}}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"baseline_delta","metric":"response_time","population":"action","parameters":{"window_buckets":5}}`, false},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"moving_stddev","metric":"error_rate","population":"transaction","parameters":{"window_buckets":1000,"minimum_coverage":0.5,"bucket_seconds":60}}`, true},
		{storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"baseline_percent_delta","metric":"response_time","population":"action"}`, true},
		// The acceptance policy defaults to advisory.
		{storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"max_loss_ratio":0.01}`, true},
		// Effect floors can only raise the spec minimums.
		{storage.AnalysisDefinitionKindBrowserPolicy, `{"version":1,"metrics":[{"metric":"lcp","good":1,"poor":2,"weight":1}],"effect_floors":{"timing_ms":10},"baseline":{"mode":"none"}}`, false},
		{storage.AnalysisDefinitionKindBrowserPolicy, `{"version":1,"metrics":[{"metric":"lcp","good":1,"poor":2,"weight":1}],"effect_floors":{"timing_relative":0.05},"baseline":{"mode":"none"}}`, false},
		{storage.AnalysisDefinitionKindBrowserPolicy, `{"version":1,"metrics":[{"metric":"cls","good":0.1,"poor":0.25,"weight":1}],"effect_floors":{"cls":0},"baseline":{"mode":"none"}}`, false},
		{storage.AnalysisDefinitionKindBrowserPolicy, `{"version":1,"metrics":[{"metric":"cls","good":0.1,"poor":0.25,"weight":1}],"effect_floors":{"cls":11},"baseline":{"mode":"none"}}`, false},
		{storage.AnalysisDefinitionKindBrowserPolicy, `{"version":1,"metrics":[{"metric":"cls","good":0.1,"poor":0.25,"weight":1}],"effect_floors":{"timing_ms":50,"timing_relative":0.1,"cls":0.02},"baseline":{"mode":"none"}}`, true},
		// The documented description bound is the decoder's 256 bytes.
		{storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":1,"description":"` + strings.Repeat("d", 256) + `"}`, true},
		{storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":1,"description":"` + strings.Repeat("d", 257) + `"}`, false},
	}
	for _, tc := range cases {
		response := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(tc.kind, "Case", 0, tc.definition), "")
		if accepted := response.Code == http.StatusCreated; accepted != tc.accepted || (!accepted && response.Code != http.StatusBadRequest) {
			t.Fatalf("%s %s: %d %s", tc.kind, tc.definition, response.Code, response.Body.String())
		}
	}
}

// The second-default rule is enforced inside the save transaction: of two
// concurrent default saves exactly one succeeds.
func TestConcurrentDefaultPolicySavesHaveOneWinner(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	codes := make(chan int, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, "Racing default", 0, acceptanceDefault), "")
			codes <- response.Code
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for code := range codes {
		got[code]++
	}
	if got[http.StatusCreated] != 1 || got[http.StatusConflict] != 5 {
		t.Fatalf("concurrent default saves: %v", got)
	}
}

// Deleted policies are filtered before paging, so a page is short only at the
// end of the list.
func TestAnalysisPolicyListPagesOnlyLivePolicies(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	kind := storage.AnalysisDefinitionKindTelemetryPolicy
	for _, id := range []string{"a", "b", "c", "d"} {
		if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: id, ProjectID: storage.DefaultProjectID, Kind: kind, Name: id, Definition: json.RawMessage(`{"version":1,"sample_age_ms":1}`)}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "a", ProjectID: storage.DefaultProjectID, Kind: kind, Name: "a", Deleted: true, Definition: json.RawMessage(`{"version":1,"sample_age_ms":1}`)}, 1); err != nil {
		t.Fatal(err)
	}
	page := func(query string) []string {
		response := authoringAPIRequest(t, server, http.MethodGet, policiesPath+"&kind="+kind+query, nil, "")
		var items []storage.AnalysisDefinition
		if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		return ids
	}
	if first, second := page("&limit=2"), page("&limit=2&offset=2"); !reflect.DeepEqual(first, []string{"b", "c"}) || !reflect.DeepEqual(second, []string{"d"}) {
		t.Fatalf("pages %v %v", first, second)
	}
}

func seedPolicy(t *testing.T, store *storage.SQLiteStore, project, id, kind, definition string) {
	t.Helper()
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: id, ProjectID: project, Kind: kind, Name: id, Definition: json.RawMessage(definition)}, 0); err != nil {
		t.Fatal(err)
	}
}

// Only the selected default is strictly validated at Start; a non-default row
// that a stricter validator would now reject never blocks a run.
func TestStartIgnoresInvalidNonDefaultPoliciesButRefusesAnInvalidDefault(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-lenient")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	project := normalizeProjectID(test.ProjectID)
	seedPolicy(t, store, project, "stale-rule", storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"operation":"delta","metric":"response_time","population":"action"}`)
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("a non-default policy blocked Start: %v", err)
	}
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	finishTelemetryBackend(t, backend)

	other, otherStore, otherTest := newPassiveTransitionTestRunner(t, "policy-invalid-default")
	other.setExecutionBackend(newTelemetryPolicyBarrierBackend(t))
	seedPolicy(t, otherStore, normalizeProjectID(otherTest.ProjectID), "bad-default", storage.AnalysisDefinitionKindDerivedOperation, `{"version":1,"apply_by_default":true,"operation":"delta","metric":"response_time","population":"action"}`)
	err := other.StartTest(otherTest.ID)
	if !errors.Is(err, errRunPolicyConfiguration) || errors.Is(err, errStartAdmissionEvidenceStale) {
		t.Fatalf("invalid default policy: %v", err)
	}
}

type failingPolicyListStore struct {
	SQLiteStore *storage.SQLiteStore
}

func (s *failingPolicyListStore) ListLatestAnalysisDefinitions(string, string) ([]storage.AnalysisDefinition, error) {
	return nil, errors.New("database is locked")
}

// A store failure while resolving pins is retryable, never a permanent 400.
func TestPolicyStoreFailureAtStartIsRetryable(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-store-failure")
	runner.setExecutionBackend(newTelemetryPolicyBarrierBackend(t))
	runner.testStore = sqliteOverlay(&failingPolicyListStore{SQLiteStore: store})
	err := runner.StartTest(test.ID)
	if !errors.Is(err, errStartAdmissionEvidenceStale) || errors.Is(err, errRunPolicyConfiguration) {
		t.Fatalf("store failure at Start: %v", err)
	}
	stored, getErr := store.GetTest(test.ID)
	if getErr != nil || decodeStoredInitialProvenance(stored.InitialProvenance) != nil {
		t.Fatalf("refused Start persisted admission: %v", getErr)
	}
}

// A queued run whose project policies refuse admission fails visibly; it is
// not left at the head of the queue.
func TestPendingPromotionFailsARunRefusedByItsPolicies(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-pending")
	runner.setExecutionBackend(newTelemetryPolicyBarrierBackend(t))
	project := normalizeProjectID(test.ProjectID)
	for _, id := range []string{"first", "second"} {
		seedPolicy(t, store, project, id, storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, acceptanceDefault)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	test.Status = "PENDING"
	test.PendingSince = &pendingSince
	test.PendingReason = "waiting for capacity"
	record := buildManagedTestRecord(test)
	runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	// A second queued run behind it is still considered in the same pass:
	// the refusal does not leave the queue stuck at its head.
	behind, err := runner.CreateTest(CreateTestRequest{Name: "behind", ScriptID: "policy-pending-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	laterPending := pendingSince.Add(time.Second)
	runner.mu.Lock()
	second := runner.tests[behind.ID]
	second.Status, second.PendingSince, second.PendingReason = "PENDING", &laterPending, "waiting for capacity"
	secondRecord := buildManagedTestRecord(second)
	runner.mu.Unlock()
	if err := store.SaveTest(secondRecord); err != nil {
		t.Fatal(err)
	}
	runner.promotePendingTests()
	got, ok := runner.GetTest(test.ID)
	if !ok || got.Status != "FAILED" || !strings.Contains(got.Error, "more than one") {
		t.Fatalf("pending run with ambiguous policies: %#v", got)
	}
	if next, ok := runner.GetTest(behind.ID); !ok || next.Status == "PENDING" {
		t.Fatalf("the run behind the refused one was not considered: %#v", next)
	}
}

// Policy save failures map to fixed statuses and messages; storage detail
// never reaches the response.
func TestAnalysisPolicySaveErrorsAreMappedWithoutStorageDetail(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{storage.ErrAnalysisRevision, http.StatusConflict},
		{storage.ErrAnalysisDefinitionLimit, http.StatusConflict},
		{storage.ErrAnalysisDefinitionInvalid, http.StatusBadRequest},
		{fmt.Errorf("lock project: %w", storage.ErrOwnedProjectNotFound), http.StatusNotFound},
		{errors.New("pq: connection reset by secret-host"), http.StatusInternalServerError},
	} {
		response := httptest.NewRecorder()
		if !writeAnalysisPolicySaveError(response, tc.err) || response.Code != tc.status || strings.Contains(response.Body.String(), "secret-host") {
			t.Fatalf("%v: %d %s", tc.err, response.Code, response.Body.String())
		}
	}
}

type failingPolicyBody struct{}

func (failingPolicyBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// Only an oversized body is 413; any other read failure is a 400.
func TestAnalysisPolicyBodyErrors(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	oversized := authoringAPIRequest(t, server, http.MethodPost, policiesPath, map[string]string{"pad": strings.Repeat("x", 2<<20)}, "")
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized policy: %d %s", oversized.Code, oversized.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, policiesPath, failingPolicyBody{})
	response := httptest.NewRecorder()
	server.handleCreateAnalysisPolicy(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("failed body read: %d %s", response.Code, response.Body.String())
	}
}

// Pins are part of the write-once admission receipt, so a restarted
// controller reloads exactly the pins the run started with.
func TestRunPolicyPinsSurviveControllerRestart(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-restart")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	seedPolicy(t, store, normalizeProjectID(test.ProjectID), "gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, acceptanceDefault)
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatal(err)
	}
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	runner.mu.RLock()
	want := cloneRunPolicyPins(test.initialArchiveProvenance.PolicyPins)
	runner.mu.RUnlock()
	if len(want) != 1 || want[0].Applied {
		t.Fatalf("started pins %#v", want)
	}
	// Editing the default after Start must not change what the run pinned.
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "gate", ProjectID: normalizeProjectID(test.ProjectID), Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, Name: "gate", Definition: json.RawMessage(`{"version":1,"apply_by_default":true,"mode":"advisory","max_loss_ratio":0.5}`)}, want[0].Revision); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	merged := &ManagedTest{ID: test.ID}
	applyStoredManagedTestSnapshot(merged, managedTestFromRecord(record))
	if merged.initialArchiveProvenance == nil || !reflect.DeepEqual(merged.initialArchiveProvenance.PolicyPins, want) {
		t.Fatalf("HA refresh re-resolved pins: %#v", merged.initialArchiveProvenance)
	}
	restarted := NewTestRunner(2, NewHub(), store)
	restarted.mu.RLock()
	reloaded := restarted.tests[test.ID]
	var pins []RunPolicyPin
	if reloaded != nil && reloaded.initialArchiveProvenance != nil {
		pins = cloneRunPolicyPins(reloaded.initialArchiveProvenance.PolicyPins)
	}
	restarted.mu.RUnlock()
	if !reflect.DeepEqual(pins, want) {
		t.Fatalf("restarted controller pins %#v, want %#v", pins, want)
	}
	finishTelemetryBackend(t, backend)
}

// The archive manifest carries the pins, and an empty list stays distinct
// from a legacy run that predates policies.
func TestRunPolicyPinsReachTheManifestAndKeepEmptyDistinct(t *testing.T) {
	pins := []RunPolicyPin{{Kind: storage.AnalysisDefinitionKindTelemetryPolicy, ID: "p", Name: "P", Revision: 2, SHA256: "abc", Source: "project_default"}}
	test := &ManagedTest{ID: "manifest-pins", initialArchiveProvenance: &RunArchiveProvenance{PolicyPins: pins}}
	if got := maskRunArchiveProvenance(buildRunArchiveProvenanceFromSnapshot(test)).PolicyPins; !reflect.DeepEqual(got, pins) {
		t.Fatalf("manifest pins %#v", got)
	}
	empty := decodeStoredInitialProvenance(initialProvenanceForStorage(&RunArchiveProvenance{PolicyPins: []RunPolicyPin{}}))
	if empty == nil || empty.PolicyPins == nil || len(empty.PolicyPins) != 0 {
		t.Fatalf("an empty pin list did not survive storage: %#v", empty)
	}
	legacy := decodeStoredInitialProvenance(initialProvenanceForStorage(&RunArchiveProvenance{}))
	if legacy == nil || legacy.PolicyPins != nil {
		t.Fatalf("a legacy receipt gained pins: %#v", legacy)
	}
	encoded, err := json.Marshal(RunPolicyPin{})
	if err != nil || !strings.Contains(string(encoded), `"applied":false`) {
		t.Fatalf("pins must say they were not applied: %s %v", encoded, err)
	}
}

// FuzzDecodeAnalysisPolicy checks the strict decoder never panics, and that
// whatever it accepts is stable: the envelope flag read at Start agrees with
// the strict decode.
func FuzzDecodeAnalysisPolicy(f *testing.F) {
	seeds := []string{
		`{"version":1,"sample_age_ms":2000}`,
		acceptanceDefault,
		`{"version":1,"metrics":[{"metric":"lcp","good":1,"poor":2,"weight":1}],"baseline":{"mode":"none"}}`,
		`{"version":1,"journeys":[{"id":"a","name":"A","start":{"action":"x"},"success":{"action":"y"},"weight":1}]}`,
		`{"version":1,"operation":"moving_mean","metric":"response_time","population":"action","parameters":{"window_buckets":5}}`,
		`{"version":1,"apply_by_default":true,"apply_by_default":false,"sample_age_ms":1}`,
		`[]`, `null`, `{"version":1e400}`,
	}
	for index, seed := range seeds {
		f.Add(uint8(index), []byte(seed))
	}
	f.Fuzz(func(t *testing.T, kindIndex uint8, data []byte) {
		kind := storage.AnalysisPolicyKinds[int(kindIndex)%len(storage.AnalysisPolicyKinds)]
		envelope, err := decodeAnalysisPolicy(kind, data)
		if err != nil {
			return
		}
		if !json.Valid(data) {
			t.Fatalf("accepted invalid JSON %q", data)
		}
		isDefault, err := policyAppliesByDefault(data)
		if err != nil || isDefault != envelope.ApplyByDefault {
			t.Fatalf("envelope flag %v/%v disagrees with the strict decode %v for %q", isDefault, err, envelope.ApplyByDefault, data)
		}
		if _, err := decodeAnalysisPolicy(kind, data); err != nil {
			t.Fatalf("decode is not stable: %v", err)
		}
	})
}

// Over HTTP a policy store failure at Start is a retryable 503, not a 400.
func TestPolicyStoreFailureAtStartIsAServiceUnavailableResponse(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	db, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	server := NewServer(ServerConfig{Store: sqliteOverlay(&failingPolicyListStore{SQLiteStore: db}), MaxConcurrent: 10, Addr: ":8080"})
	storePreflightScript(server.runner, "policy-store-http", "")
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "Policy outage", ScriptID: "policy-store-http", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/start", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" || !strings.Contains(response.Body.String(), "start_admission_temporarily_unavailable") || !strings.Contains(response.Body.String(), "analysis policy store unavailable") || strings.Contains(response.Body.String(), "database is locked") {
		t.Fatalf("policy store failure at Start: %d %s", response.Code, response.Body.String())
	}
}

// pagedPolicyStore pages like a store without a single-statement read, and a
// concurrent save shifts the second page so one policy is listed twice.
type pagedPolicyStore struct {
	pages [][]storage.AnalysisDefinition
}

func (s *pagedPolicyStore) SaveAnalysisDefinition(storage.AnalysisDefinition, int64) (*storage.AnalysisDefinition, error) {
	return nil, errors.New("read only")
}

func (s *pagedPolicyStore) GetAnalysisDefinition(string, string, int64) (*storage.AnalysisDefinition, error) {
	return nil, errors.New("read only")
}

func (s *pagedPolicyStore) ListAnalysisDefinitions(_, _ string, _, offset int) ([]storage.AnalysisDefinition, error) {
	if index := offset / 100; index < len(s.pages) {
		return s.pages[index], nil
	}
	return nil, nil
}

func TestPagedPolicyReadDeduplicatesAPolicyShiftedAcrossPages(t *testing.T) {
	full := make([]storage.AnalysisDefinition, 100)
	for i := range full {
		full[i] = storage.AnalysisDefinition{ID: fmt.Sprintf("policy-%03d", i)}
	}
	// The last policy of page one reappears first on page two, and a deleted
	// policy is dropped.
	second := []storage.AnalysisDefinition{full[99], {ID: "policy-100"}, {ID: "policy-101", Deleted: true}}
	live, err := liveAnalysisPolicies(&pagedPolicyStore{pages: [][]storage.AnalysisDefinition{full, second}}, storage.DefaultProjectID, storage.AnalysisDefinitionKindTelemetryPolicy)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, item := range live {
		seen[item.ID]++
	}
	if len(live) != 101 || seen["policy-099"] != 1 || seen["policy-101"] != 0 {
		t.Fatalf("paged read listed %d policies: %v", len(live), seen)
	}
}

// flakyPolicyListStore fails the first policy reads, then recovers.
type flakyPolicyListStore struct {
	SQLiteStore *storage.SQLiteStore
	mu          sync.Mutex
	remaining   int
}

func (s *flakyPolicyListStore) ListLatestAnalysisDefinitions(project, kind string) ([]storage.AnalysisDefinition, error) {
	s.mu.Lock()
	failing := s.remaining > 0
	if failing {
		s.remaining--
	}
	s.mu.Unlock()
	if failing {
		return nil, errors.New("database is locked")
	}
	return s.SQLiteStore.ListLatestAnalysisDefinitions(project, kind)
}

// A queued run whose policies cannot be read for a moment is retried, not
// failed: only a configuration refusal fails a pending run.
func TestPendingPromotionRetriesAPolicyStoreFailure(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-retry")
	runner.testStore = sqliteOverlay(&flakyPolicyListStore{SQLiteStore: store, remaining: 1})
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	test.Status = "PENDING"
	test.PendingSince = &pendingSince
	test.PendingReason = "waiting for capacity"
	record := buildManagedTestRecord(test)
	runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.promotePendingTests()
	select {
	case started := <-backend.called:
		if started != test.ID {
			t.Fatalf("backend started %q, want %q", started, test.ID)
		}
	case <-time.After(5 * time.Second):
		if got, _ := runner.GetTest(test.ID); got != nil {
			t.Fatalf("the run was not retried after the policy store recovered: %s %s", got.Status, got.Error)
		}
		t.Fatal("the run was not retried after the policy store recovered")
	}
}
