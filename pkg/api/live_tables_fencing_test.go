package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLiveTableWaitingReadRejectsExpiredOwner(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "memory", "discard")
	id := liveTableID(test.ID, "work")
	client := runner.liveClient(test.ID, "local")
	for seq := uint64(1); seq <= 2; seq++ {
		if _, err := client.Execute(context.Background(), id, livetable.Request{ClientID: "drain", Sequence: seq, Operation: livetable.OpPop}); err != nil {
			t.Fatal(err)
		}
	}
	table := runner.liveRun(test.ID).tables[id].table
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_, err := client.Execute(ctx, id, livetable.Request{Operation: livetable.OpRead, Wait: time.Second})
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for table.Stats().Waiters == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if table.Stats().Waiters != 1 {
		t.Fatal("read did not enter waiter queue")
	}
	runner.leadership.mu.Lock()
	originalDeadline := runner.leadership.localLeaseDeadline
	runner.leadership.localLeaseDeadline = time.Now().Add(-time.Second)
	runner.leadership.mu.Unlock()
	defer func() {
		runner.leadership.mu.Lock()
		runner.leadership.localLeaseDeadline = originalDeadline
		runner.leadership.mu.Unlock()
	}()
	// A pending core read may finish even after the controller's authority expired.
	// Exercise the response fence without invoking another authorized controller op.
	if _, err := table.Execute(ctx, livetable.Request{ClientID: "producer", Sequence: 1, Operation: livetable.OpPush, Values: map[string]string{"value": "late"}}); err != nil {
		t.Fatal(err)
	}
	err := <-result
	var typed *livetable.Error
	if !errors.As(err, &typed) || typed.Code != livetable.CodeUnavailable || typed.OutcomeUnknown {
		t.Fatalf("stale read response was not fenced: %v", err)
	}
}

func TestLiveTableReadHandlerRechecksAuthorizedProject(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "memory", "discard")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("id", test.ID)
	req = req.WithContext(context.WithValue(req.Context(), authorizedProjectContextKey{}, "unrelated-project"))
	response := httptest.NewRecorder()
	(&Server{runner: runner}).handleListLiveTables(response, req)
	if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "private-customer") {
		t.Fatalf("cross-project response %d %s", response.Code, response.Body.String())
	}
}

func TestLiveTableDiscardCannotBeReportedAsSaveAsNew(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "memory", "discard")
	test.Status = "COMPLETE"
	ended := time.Now()
	if err := store.UpdateTestStatus(test.ID, "COMPLETE", &ended, ""); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(context.Background(), test); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"output_pool_name":"must not claim saved"}`))
	req.SetPathValue("id", test.ID)
	req.SetPathValue("tableID", liveTableID(test.ID, "work"))
	response := httptest.NewRecorder()
	(&Server{runner: runner, store: store}).handleFinalizeLiveTable(response, req)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "already finalized") {
		t.Fatalf("discard reported as successful save: %d %s", response.Code, response.Body.String())
	}
}

func TestLiveTableRecoveryKeyOutageIsRetryable(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "durable", "discard")
	existing := runner.liveRun(test.ID)
	wrapper := &flakyAdoptionKeyWrapper{KeyWrapper: runner.continuityKeyWrapper}
	wrapper.failUnwrap.Store(true)
	runner.continuityKeyWrapper = wrapper
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	err = runner.restoreLiveTables(context.Background(), test, term)
	if !errors.Is(err, errRunAdoptionKeyAccess) {
		t.Fatalf("key outage became permanent failure: %v", err)
	}
	if runner.liveRun(test.ID) != existing || test.Status != "INITIALIZING" {
		t.Fatal("failed restore published partial state or changed run status")
	}
	wrapper.failUnwrap.Store(false)
	if err := runner.restoreLiveTables(context.Background(), test, term); err != nil {
		t.Fatal(err)
	}
}

func TestLiveTableProjectAdmissionRejectsBeforeSeedAllocation(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("budget-run-%d", i)
		test := liveIntegrationNewRun(t, runner, store, id, []storage.ScenarioDataPoolBinding{{Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"value"}}}})
		err := runner.prepareLiveTables(context.Background(), test)
		if i < 4 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			if err == nil || !strings.Contains(err.Error(), "project memory budget") {
				t.Fatalf("project admission not enforced: %v", err)
			}
			if runner.liveRun(test.ID) != nil {
				t.Fatal("failed admission installed runtime")
			}
			tables, err := store.ListLiveTablesForRun(context.Background(), test.ID, test.ProjectID)
			if err != nil || len(tables) != 0 {
				t.Fatalf("failed admission persisted seed: %+v %v", tables, err)
			}
		}
	}
}

type liveTableSlowResponse struct {
	*httptest.ResponseRecorder
	started chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (w *liveTableSlowResponse) Write(body []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.proceed
	return w.ResponseRecorder.Write(body)
}

func TestLiveTableSlowBrowserDoesNotBlockFinalization(t *testing.T) {
	for _, responseKind := range []string{"rows", "export", "metadata"} {
		t.Run(responseKind, func(t *testing.T) {
			runner, store, test := newLiveRuntimeFixture(t, "memory", "discard")
			server := &Server{runner: runner, store: store}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.SetPathValue("id", test.ID)
			req.SetPathValue("tableID", liveTableID(test.ID, "work"))
			response := &liveTableSlowResponse{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), proceed: make(chan struct{})}
			defer close(response.proceed)
			go func() {
				if responseKind == "export" {
					server.handleExportLiveTable(response, req)
				} else if responseKind == "metadata" {
					server.handleListLiveTables(response, req)
				} else {
					server.handleLiveTableRows(response, req)
				}
			}()
			select {
			case <-response.started:
			case <-time.After(2 * time.Second):
				t.Fatal("response never reached client write")
			}
			if !runner.liveTablesInspectMu.TryLock() {
				t.Fatal("slow response retained the inspection reservation")
			}
			runner.liveTablesInspectMu.Unlock()
			if responseKind != "metadata" {
				other := httptest.NewRecorder()
				server.handleExportLiveTable(other, req)
				if other.Code != http.StatusTooManyRequests {
					t.Fatalf("slow raw response did not bound later export allocation: %d", other.Code)
				}
			}
			done := make(chan error, 1)
			go func() { done <- runner.finalizeLiveTables(context.Background(), test, "COMPLETE") }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("slow browser blocked table finalization")
			}
		})
	}
}

type blockingLiveTableKeyWrapper struct {
	keywrap.KeyWrapper
	entered chan struct{}
}

func (w *blockingLiveTableKeyWrapper) WrapKey(ctx context.Context, _, _ []byte) (keywrap.WrappedKey, error) {
	close(w.entered)
	<-ctx.Done()
	return keywrap.WrappedKey{}, ctx.Err()
}

func TestLiveTablePreparationDoesNotBlockOtherRuns(t *testing.T) {
	runner, store, active := newLiveRuntimeFixture(t, "memory", "discard")
	wrapper := &blockingLiveTableKeyWrapper{KeyWrapper: runner.continuityKeyWrapper, entered: make(chan struct{})}
	runner.continuityKeyWrapper = wrapper
	next := liveIntegrationNewRun(t, runner, store, "blocked-preparation", active.DataPools)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared := make(chan error, 1)
	go func() { prepared <- runner.prepareLiveTables(ctx, next) }()
	select {
	case <-wrapper.entered:
	case <-time.After(time.Second):
		t.Fatal("preparation did not enter wrapping service")
	}
	read := make(chan error, 1)
	go func() {
		_, err := runner.liveClient(active.ID, "local").Execute(context.Background(), liveTableID(active.ID, "work"), livetable.Request{Operation: livetable.OpRead})
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Error("slow preparation blocked an unrelated live run")
	}
	cancel()
	if err := <-prepared; err == nil {
		t.Fatal("cancelled preparation succeeded")
	}
	if err := runner.finalizeLiveTables(context.Background(), next, "STOPPED"); err != nil {
		t.Fatal(err)
	}
}

func TestLiveTableRuntimeReleaseIsBoundedAndValueFree(t *testing.T) {
	runner := &TestRunner{liveTables: map[string]*runLiveTables{}}
	for i := 0; i < liveTableRetainedRuns+3; i++ {
		id := fmt.Sprintf("run-%d", i)
		run := &runLiveTables{testID: id, closed: true, budget: 10, tables: map[string]*runLiveTable{
			"table": {record: storage.LiveTableRecord{SeedMetadata: "encrypted-secret"}, seed: storage.LiveDataPoolSeedIdentity{Schema: map[string]string{"private": "string"}}},
		}}
		runner.liveTables[id] = run
		runner.releaseLiveTableRuntime(run)
		if run.budget != 0 || run.tables["table"].record.SeedMetadata != "" || run.tables["table"].seed.Schema != nil {
			t.Fatal("released runtime retained seed metadata or its budget")
		}
	}
	if len(runner.liveTables) != liveTableRetainedRuns || runner.liveRun("run-0") != nil {
		t.Fatal("terminal metadata cache was not bounded")
	}
}

func TestOriginalLiveSeedDoesNotWaitForAnotherRunLock(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "memory", "discard")
	source := runner.liveRun(test.ID)
	source.mu.Lock()
	defer source.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := runner.originalLiveSeed(context.Background(), test.ID, test.ProjectID, "work")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "seed is busy") {
			t.Fatalf("expected retryable busy error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("seed lookup waited on a different run's initialization lock")
	}
}

type blockingLiveFinalizationStore struct {
	*storage.SQLiteStore
	entered chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (s *blockingLiveFinalizationStore) GetLiveTableFinalization(ctx context.Context, id, projectID string) (*storage.LiveTableFinalizationResult, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.proceed:
		return s.SQLiteStore.GetLiveTableFinalization(ctx, id, projectID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestLiveTableSlowFinalizationBoundsConcurrentRawCopies(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "memory", "write_back")
	if err := runner.finalizeLiveTables(context.Background(), test, "FAILED"); err != nil {
		t.Fatal(err)
	}
	ended := time.Now()
	if err := store.UpdateTestStatus(test.ID, "FAILED", &ended, "failed"); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.Status = "FAILED"
	runner.mu.Unlock()
	blocked := &blockingLiveFinalizationStore{SQLiteStore: store, entered: make(chan struct{}), proceed: make(chan struct{})}
	runner.dataPoolStore = blocked
	server := &Server{runner: runner, store: store}
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"output_pool_name":"Recovered output"}`))
		r.SetPathValue("id", test.ID)
		r.SetPathValue("tableID", liveTableID(test.ID, "work"))
		return r
	}
	first := httptest.NewRecorder()
	var requests sync.WaitGroup
	requests.Add(1)
	go func() { defer requests.Done(); server.handleFinalizeLiveTable(first, request()) }()
	defer func() {
		close(blocked.proceed)
		requests.Wait()
		if first.Code != http.StatusOK {
			t.Errorf("first finalization failed: %d %s", first.Code, first.Body.String())
		}
	}()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("finalization did not reach storage after copying rows")
	}
	if !runner.liveTablesInspectMu.TryLock() {
		t.Fatal("slow finalization retained inspection lock")
	}
	runner.liveTablesInspectMu.Unlock()
	second := make(chan int, 1)
	requests.Add(1)
	go func() {
		defer requests.Done()
		response := httptest.NewRecorder()
		server.handleFinalizeLiveTable(response, request())
		second <- response.Code
	}()
	select {
	case code := <-second:
		if code != http.StatusTooManyRequests {
			t.Fatalf("concurrent finalization status %d", code)
		}
	case <-time.After(time.Second):
		t.Error("concurrent finalization allocated another output copy")
	}
}

func TestLiveTablePendingOutputSurvivesRuntimeEviction(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "memory", "write_back")
	if err := runner.finalizeLiveTables(context.Background(), test, "FAILED"); err != nil {
		t.Fatal(err)
	}
	ended := time.Now()
	if err := store.UpdateTestStatus(test.ID, "FAILED", &ended, "failed"); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.Status = "FAILED"
	runner.mu.Unlock()
	runner.liveTablesMu.Lock()
	delete(runner.liveTables, test.ID)
	runner.liveTablesMu.Unlock()
	server := &Server{runner: runner, store: store}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("id", test.ID)
	response := httptest.NewRecorder()
	server.handleListLiveTables(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reason":"reconciliation_required"`) || strings.Contains(response.Body.String(), "private-customer") {
		t.Fatalf("retained pending output not visible safely: %d %s", response.Code, response.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"output_pool_name":"Recovered after restart"}`))
	req.SetPathValue("id", test.ID)
	req.SetPathValue("tableID", liveTableID(test.ID, "work"))
	response = httptest.NewRecorder()
	server.handleFinalizeLiveTable(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"complete"`) {
		t.Fatalf("retained checkpoint could not be reconciled: %d %s", response.Code, response.Body.String())
	}
}
