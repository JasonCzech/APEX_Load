package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultHAFailoverSoakIterations = 25
	maxHAFailoverSoakIterations     = 250
	haFailoverSoakStepTimeout       = 10 * time.Second
)

type haFailoverSoakStart struct {
	testID string
	owner  string
	err    error
}

type haFailoverSoakStage struct {
	testID string
	record *storage.RunArchiveRecord
	err    error
}

// haFailoverSoakBackend is deliberately deterministic: it makes the durable
// INITIALIZING -> RUNNING transition, then remains active until the owning
// leadership epoch is cancelled. On cancellation it commits a local terminal
// manifest but does not publish the archive catalog row. That models the real
// crash window closed by recoverOrphanedLocalRunArchives without requiring a
// generator pod or object store in the unit-test process.
type haFailoverSoakBackend struct {
	mu             sync.Mutex
	ownerByRunner  map[*TestRunner]string
	startsByTestID map[string][]string
	started        chan haFailoverSoakStart
	staged         chan haFailoverSoakStage
	active         int
	idle           chan struct{}
}

func newHAFailoverSoakBackend(capacity int, owners map[*TestRunner]string) *haFailoverSoakBackend {
	if capacity < 1 {
		capacity = 1
	}
	idle := make(chan struct{})
	close(idle)
	return &haFailoverSoakBackend{
		ownerByRunner:  owners,
		startsByTestID: make(map[string][]string),
		started:        make(chan haFailoverSoakStart, capacity),
		staged:         make(chan haFailoverSoakStage, capacity),
		idle:           idle,
	}
}

func (b *haFailoverSoakBackend) Run(tr *TestRunner, test *ManagedTest) {
	if b == nil || tr == nil || test == nil {
		return
	}
	b.mu.Lock()
	if b.active == 0 {
		b.idle = make(chan struct{})
	}
	b.active++
	owner := b.ownerByRunner[tr]
	b.startsByTestID[test.ID] = append(b.startsByTestID[test.ID], owner)
	duplicate := len(b.startsByTestID[test.ID]) > 1
	b.mu.Unlock()
	defer b.finishRun()
	if duplicate {
		err := fmt.Errorf("duplicate execution start for %s", test.ID)
		b.started <- haFailoverSoakStart{testID: test.ID, owner: owner, err: err}
		b.staged <- haFailoverSoakStage{testID: test.ID, err: err}
		return
	}

	now := time.Now().UTC()
	tr.mu.Lock()
	ctx := test.executionCtx
	if ctx == nil || test.cancel == nil {
		tr.mu.Unlock()
		err := fmt.Errorf("test %s has no execution context", test.ID)
		b.started <- haFailoverSoakStart{testID: test.ID, owner: owner, err: err}
		b.staged <- haFailoverSoakStage{testID: test.ID, err: err}
		return
	}
	if test.Status != "INITIALIZING" || !tr.isExecutionLeader() {
		status := test.Status
		tr.mu.Unlock()
		err := fmt.Errorf("test %s entered backend as %s without leadership", test.ID, status)
		b.started <- haFailoverSoakStart{testID: test.ID, owner: owner, err: err}
		b.staged <- haFailoverSoakStage{testID: test.ID, err: err}
		return
	}
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	persistErr := tr.saveManagedTestRecord(test)
	tr.mu.Unlock()
	if persistErr != nil {
		err := fmt.Errorf("persist RUNNING state: %w", persistErr)
		b.started <- haFailoverSoakStart{testID: test.ID, owner: owner, err: err}
		b.staged <- haFailoverSoakStage{testID: test.ID, err: err}
		return
	}

	writer, err := newRunArchiveWriter(test, buildRunArchiveProvenance(tr, test))
	if err != nil {
		err = fmt.Errorf("initialize local archive staging: %w", err)
		b.started <- haFailoverSoakStart{testID: test.ID, owner: owner, err: err}
		b.staged <- haFailoverSoakStage{testID: test.ID, err: err}
		return
	}
	b.started <- haFailoverSoakStart{testID: test.ID, owner: owner}

	<-ctx.Done()
	completedAt := time.Now().UTC()
	record, finalizeErr := writer.Finalize(
		"FAILED",
		completedAt,
		"execution owner relinquished leadership during HA failover soak",
		false,
		false,
		nil,
	)
	b.staged <- haFailoverSoakStage{testID: test.ID, record: record, err: finalizeErr}
}

func (b *haFailoverSoakBackend) starts(testID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.startsByTestID[testID]...)
}

func (b *haFailoverSoakBackend) finishRun() {
	b.mu.Lock()
	b.active--
	if b.active == 0 {
		close(b.idle)
	}
	b.mu.Unlock()
}

func (b *haFailoverSoakBackend) waitForIdle(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	idle := b.idle
	b.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestHAFailoverSoakIterationCount(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "default", want: defaultHAFailoverSoakIterations},
		{name: "one", raw: "1", want: 1},
		{name: "bounded maximum", raw: strconv.Itoa(maxHAFailoverSoakIterations), want: maxHAFailoverSoakIterations},
		{name: "zero", raw: "0", wantErr: true},
		{name: "negative", raw: "-1", wantErr: true},
		{name: "over maximum", raw: strconv.Itoa(maxHAFailoverSoakIterations + 1), wantErr: true},
		{name: "invalid", raw: "many", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHAFailoverSoakIterations(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseHAFailoverSoakIterations(%q) error = %v, wantErr %t", tc.raw, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("parseHAFailoverSoakIterations(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func parseHAFailoverSoakIterations(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultHAFailoverSoakIterations, nil
	}
	iterations, err := strconv.Atoi(raw)
	if err != nil || iterations < 1 || iterations > maxHAFailoverSoakIterations {
		return 0, fmt.Errorf("APEXLOAD_HA_SOAK_ITERATIONS must be an integer from 1 through %d", maxHAFailoverSoakIterations)
	}
	return iterations, nil
}

// TestHAControllerFailoverSoak exercises two controller/API Server instances
// against two independent connections to the same durable store. It is opt-in
// because the default 25 handoffs intentionally do substantially more I/O than
// a unit test. SQLite is the dependency-free transactional simulation only;
// APEXLOAD_HA_SOAK_DATABASE_URL selects a disposable PostgreSQL database, which
// is the supported shared metadata backend for an HA deployment.
func TestHAControllerFailoverSoak(t *testing.T) {
	if !envBool("APEXLOAD_RUN_HA_FAILOVER_SOAK") {
		t.Skip("set APEXLOAD_RUN_HA_FAILOVER_SOAK=1 to run the HA failover soak")
	}
	iterations, err := parseHAFailoverSoakIterations(os.Getenv("APEXLOAD_HA_SOAK_ITERATIONS"))
	if err != nil {
		t.Fatal(err)
	}

	archiveRoot := filepath.Join(t.TempDir(), "ha-soak-archives")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_CACHE_BACKEND", cacheBackendMemory)
	t.Setenv("APEXLOAD_REDIS_URL", "")

	firstStore, secondStore, storageMode := openHAFailoverSoakStores(t)
	t.Cleanup(func() {
		_ = secondStore.Close()
		_ = firstStore.Close()
	})

	t.Setenv("APEXLOAD_CONTROLLER_ID", "ha-soak-controller-a")
	first := NewServer(ServerConfig{Store: firstStore, HAExecutionLeadership: true, MaxConcurrent: 1})
	t.Setenv("APEXLOAD_CONTROLLER_ID", "ha-soak-controller-b")
	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true, MaxConcurrent: 1})
	if first.leadership == nil || second.leadership == nil {
		t.Fatal("HA leadership was not configured for both server instances")
	}
	backend := newHAFailoverSoakBackend(iterations*2+4, map[*TestRunner]string{
		first.runner:  first.controllerID,
		second.runner: second.controllerID,
	})
	first.runner.setExecutionBackend(backend)
	second.runner.setExecutionBackend(backend)

	var cleanupOnce sync.Once
	var cleanupErr error
	cleanupServers := func() error {
		cleanupOnce.Do(func() {
			// This also drives the production loss callback, cancelling and
			// detaching any runtime left by a failed soak assertion.
			first.leadership.relinquish()
			second.leadership.relinquish()
			ctx, cancel := context.WithTimeout(context.Background(), haFailoverSoakStepTimeout)
			defer cancel()
			cleanupErr = errors.Join(backend.waitForIdle(ctx), first.Shutdown(ctx), second.Shutdown(ctx))
		})
		return cleanupErr
	}
	t.Cleanup(func() { _ = cleanupServers() })

	active := first
	standby := second
	active.leadership.tick()
	waitForHAFailoverSoak(t, "initial leader admission", func() bool { return active.leadership.IsAdmitted() })

	for iteration := 1; iteration <= iterations; iteration++ {
		if !active.leadership.IsAdmitted() {
			active.leadership.tick()
			waitForHAFailoverSoak(t, "leader admission", func() bool { return active.leadership.IsAdmitted() })
		}
		standby.leadership.tick()
		if standby.leadership.IsLeader() {
			t.Fatalf("iteration %d: standby acquired an already-held execution lease", iteration)
		}

		script, err := active.runner.StoreScriptDurable(&dsl.Script{
			Name:      fmt.Sprintf("HA failover soak %d", iteration),
			Protocol:  "http",
			Version:   "1.0.0",
			ProjectID: storage.DefaultProjectID,
			Config: dsl.ScriptConfig{
				BaseURL: "https://example.invalid",
				RunLogic: dsl.RunLogicConfig{
					Iterations: 1,
				},
			},
			Actions: []dsl.Action{{Name: "controlled", Type: "GET", Target: "/"}},
		})
		if err != nil {
			t.Fatalf("iteration %d: store script: %v", iteration, err)
		}
		run, err := active.runner.CreateTest(CreateTestRequest{
			Name:     fmt.Sprintf("HA failover run %d", iteration),
			Duration: "5m",
			Groups: []CreateGroupEntry{{
				Name:     "load",
				ScriptID: script.ID,
				VUsers:   1,
			}},
		})
		if err != nil {
			t.Fatalf("iteration %d: create run: %v", iteration, err)
		}

		followerResponse := callHAFailoverStartHandler(standby, run.ID)
		if followerResponse.Code != http.StatusServiceUnavailable {
			t.Fatalf("iteration %d: follower start status = %d, want 503", iteration, followerResponse.Code)
		}
		leaderResponse := callHAFailoverStartHandler(active, run.ID)
		if leaderResponse.Code != http.StatusOK && leaderResponse.Code != http.StatusAccepted {
			t.Fatalf("iteration %d: leader start status = %d body=%s", iteration, leaderResponse.Code, leaderResponse.Body.String())
		}
		started := receiveHAFailoverStart(t, backend.started, run.ID)
		if started.err != nil {
			t.Fatalf("iteration %d: backend start: %v", iteration, started.err)
		}
		if started.owner != active.controllerID {
			t.Fatalf("iteration %d: execution owner = %q, want %q", iteration, started.owner, active.controllerID)
		}
		oldOwner, oldGeneration := active.leadership.executionLeaseTerm()
		if oldOwner == "" || oldGeneration == 0 {
			t.Fatalf("iteration %d: active controller has no durable execution term", iteration)
		}

		// Relinquish after the run is durably RUNNING. The production loss hook
		// cancels and detaches the active runtime before releasing the exact lease
		// token. The backend leaves a terminal manifest in the recoverable local
		// crash window, just as a killed controller or retained generator can.
		active.leadership.relinquish()
		staged := receiveHAFailoverStage(t, backend.staged, run.ID)
		if staged.err != nil {
			t.Fatalf("iteration %d: stage terminal archive: %v", iteration, staged.err)
		}
		if staged.record == nil || staged.record.TestID != run.ID {
			t.Fatalf("iteration %d: staged archive = %#v", iteration, staged.record)
		}

		standby.leadership.tick()
		waitForHAFailoverSoak(t, "successor admission", func() bool { return standby.leadership.IsAdmitted() })
		newOwner, newGeneration := standby.leadership.executionLeaseTerm()
		if newOwner == "" || newOwner == oldOwner || newGeneration <= oldGeneration {
			t.Fatalf("iteration %d: successor term = %q/%d, prior = %q/%d", iteration, newOwner, newGeneration, oldOwner, oldGeneration)
		}

		durable, err := standby.store.GetTest(run.ID)
		if err != nil {
			t.Fatalf("iteration %d: load durable run: %v", iteration, err)
		}
		if durable.Status != "FAILED" || durable.EndTime == nil {
			t.Fatalf("iteration %d: successor reconciliation = status:%s end:%v", iteration, durable.Status, durable.EndTime)
		}
		// Reconciliation does not take over execution, so the row still names
		// the term whose archive is the run's outcome.
		if durable.ExecutionGeneration != oldGeneration {
			t.Fatalf("iteration %d: durable execution generation = %d, want the executing term %d", iteration, durable.ExecutionGeneration, oldGeneration)
		}
		starts := backend.starts(run.ID)
		if len(starts) != 1 || starts[0] != active.controllerID {
			t.Fatalf("iteration %d: execution starts = %v, want exactly one by %q", iteration, starts, active.controllerID)
		}

		ctx, cancel := context.WithTimeout(context.Background(), haFailoverSoakStepTimeout)
		err = standby.runner.recoverOrphanedLocalRunArchives(ctx)
		if err == nil {
			err = standby.retryArchiveFinalizationJobs(ctx)
		}
		cancel()
		if err != nil {
			t.Fatalf("iteration %d: successor archive recovery: %v", iteration, err)
		}
		archiveStore := standby.store
		archive, err := archiveStore.GetRunArchive(run.ID)
		if err != nil {
			t.Fatalf("iteration %d: load recovered archive: %v", iteration, err)
		}
		manifest, err := loadRunArchiveManifestFromRecord(archive)
		if err != nil {
			t.Fatalf("iteration %d: load recovered manifest: %v", iteration, err)
		}
		if manifest.TestID != run.ID || manifest.Status != "FAILED" || manifest.CompletedAt.IsZero() {
			t.Fatalf("iteration %d: recovered terminal manifest = test:%q status:%q completed:%v", iteration, manifest.TestID, manifest.Status, manifest.CompletedAt)
		}
		// The published archive carries the durable end; the old leader's own
		// finalization time is kept beside it.
		if !archiveRunTimestampMatches(manifest.CompletedAt, *durable.EndTime, archiveStore) || !archiveRunTimestampMatches(archive.CompletedAt, *durable.EndTime, archiveStore) ||
			manifest.ExecutorCompletedAt == nil || !manifest.ExecutorCompletedAt.Equal(staged.record.CompletedAt) || manifest.Provenance.ExecutionGeneration != oldGeneration {
			t.Fatalf("iteration %d: recovered completion = manifest:%v catalog:%v executor:%v generation:%d, durable end:%v executor finalized:%v term:%d",
				iteration, manifest.CompletedAt, archive.CompletedAt, manifest.ExecutorCompletedAt, manifest.Provenance.ExecutionGeneration, *durable.EndTime, staged.record.CompletedAt, oldGeneration)
		}
		assertSingleHAFailoverArchiveDirectory(t, archiveRoot, run.ID)

		// Repeat both recovery stages. The create-if-orphan transition and archive
		// catalog key must make this a no-op, not a duplicate publication.
		ctx, cancel = context.WithTimeout(context.Background(), haFailoverSoakStepTimeout)
		err = standby.runner.recoverOrphanedLocalRunArchives(ctx)
		if err == nil {
			err = standby.retryArchiveFinalizationJobs(ctx)
		}
		cancel()
		if err != nil {
			t.Fatalf("iteration %d: idempotent archive recovery: %v", iteration, err)
		}
		recoveredAgain, err := archiveStore.GetRunArchive(run.ID)
		if err != nil {
			t.Fatalf("iteration %d: reload recovered archive: %v", iteration, err)
		}
		if recoveredAgain.ManifestSHA256 != archive.ManifestSHA256 || recoveredAgain.ManifestPath != archive.ManifestPath {
			t.Fatalf("iteration %d: repeated recovery replaced archive: before=%#v after=%#v", iteration, archive, recoveredAgain)
		}
		assertNoHAFailoverArchiveIntent(t, archiveStore, run.ID)

		active, standby = standby, active
	}

	active.leadership.relinquish()
	// A follower deliberately retains the durable RUNNING projection after loss
	// while the successor owns reconciliation. Refresh both local caches before
	// invoking ordinary process shutdown so shutdown does not mistake that stale
	// projection for locally owned work.
	if err := errors.Join(first.runner.reloadStoredTests(), second.runner.reloadStoredTests()); err != nil {
		t.Fatalf("refresh follower caches before shutdown: %v", err)
	}
	if err := cleanupServers(); err != nil {
		t.Fatalf("shutdown HA soak servers: %v", err)
	}
	t.Logf("completed %d deterministic controller handoffs using %s; this is not production AKS validation", iterations, storageMode)
}

func openHAFailoverSoakStores(t *testing.T) (storage.Store, storage.Store, string) {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_HA_SOAK_DATABASE_URL"))
	if databaseURL == "" {
		path := filepath.Join(t.TempDir(), "ha-failover-soak.db")
		first, err := storage.NewSQLiteStore(path)
		if err != nil {
			t.Fatalf("open first SQLite HA simulation store: %v", err)
		}
		second, err := storage.NewSQLiteStore(path)
		if err != nil {
			first.Close()
			t.Fatalf("open second SQLite HA simulation store: %v", err)
		}
		return first, second, "local SQLite transactional simulation"
	}
	options := storage.StoreConnectionOptions{MaxOpenConns: 8, MaxIdleConns: 2, ConnMaxLifetime: time.Minute}
	first, err := storage.NewPostgresStore(databaseURL, options, false)
	if err != nil {
		t.Fatalf("open first PostgreSQL HA soak store: %v", err)
	}
	records, err := first.ListTests("", nil)
	if err != nil {
		first.Close()
		t.Fatalf("inspect PostgreSQL HA soak database: %v", err)
	}
	if len(records) != 0 {
		first.Close()
		t.Fatalf("APEXLOAD_HA_SOAK_DATABASE_URL must name an isolated empty database; found %d test rows", len(records))
	}
	second, err := storage.NewPostgresStore(databaseURL, options, false)
	if err != nil {
		first.Close()
		t.Fatalf("open second PostgreSQL HA soak store: %v", err)
	}
	return first, second, "disposable PostgreSQL metadata store"
}

func callHAFailoverStartHandler(server *Server, testID string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/start", nil)
	request.SetPathValue("id", testID)
	server.handleStartTest(recorder, request)
	return recorder
}

func receiveHAFailoverStart(t *testing.T, events <-chan haFailoverSoakStart, testID string) haFailoverSoakStart {
	t.Helper()
	select {
	case event := <-events:
		if event.testID != testID {
			t.Fatalf("received start event for %q, want %q", event.testID, testID)
		}
		return event
	case <-time.After(haFailoverSoakStepTimeout):
		t.Fatalf("timed out waiting for test %s to start", testID)
		return haFailoverSoakStart{}
	}
}

func receiveHAFailoverStage(t *testing.T, events <-chan haFailoverSoakStage, testID string) haFailoverSoakStage {
	t.Helper()
	select {
	case event := <-events:
		if event.testID != testID {
			t.Fatalf("received archive event for %q, want %q", event.testID, testID)
		}
		return event
	case <-time.After(haFailoverSoakStepTimeout):
		t.Fatalf("timed out waiting for test %s archive staging", testID)
		return haFailoverSoakStage{}
	}
}

func waitForHAFailoverSoak(t *testing.T, step string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(haFailoverSoakStepTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", step)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertSingleHAFailoverArchiveDirectory(t *testing.T, root, testID string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read HA soak archive root: %v", err)
	}
	matches := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), testID) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("archive directories for %s = %d, want exactly one", testID, matches)
	}
}

func assertNoHAFailoverArchiveIntent(t *testing.T, store storage.Store, testID string) {
	t.Helper()
	outbox, ok := store.(interface {
		GetArchiveFinalizationJob(string) (*storage.ArchiveFinalizationJob, error)
	})
	if !ok {
		t.Fatal("HA soak store does not expose archive finalization recovery")
	}
	if job, err := outbox.GetArchiveFinalizationJob(testID); err == nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archive finalization intent after recovery = %#v, err=%v", job, err)
	}
}
