package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func collectionIntentFixture(t *testing.T) (*TestRunner, *storage.SQLiteStore, *ManagedTest) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "collection.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	tr := NewTestRunner(1, nil, store)
	row := &storage.TestRecord{ID: "collection-identity", Name: "collection", ProjectID: storage.DefaultProjectID, StartTime: time.Now().UTC(), Status: "RUNNING", Groups: []any{}}
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	run := managedTestFromRecord(row)
	tr.tests[run.ID] = run
	return tr, store, run
}

func TestArchiveCollectionIntentSurvivesControllerAndTokenRotation(t *testing.T) {
	tr, store, run := collectionIntentFixture(t)
	token := bytes.Repeat([]byte{1}, generatorrpc.RunSupervisionTokenSize)
	if err := tr.prepareRunArchiveSources(run, []string{"a:9091"}, 0, token); err != nil {
		t.Fatal(err)
	}
	original, err := store.GetRunArchiveCollection(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Inspect durable metadata before execution reconciliation. The separate
	// real-generator adoption scenarios exercise full runtime startup.
	restarted := NewTestRunner(1, nil, nil)
	restarted.testStore, restarted.archiveStore = store, store
	restored := managedTestFromRecord(row)
	rotated := bytes.Repeat([]byte{2}, generatorrpc.RunSupervisionTokenSize)
	if err := restarted.prepareRunArchiveSources(restored, []string{"a:9091", "b:9091"}, 0, rotated); err != nil {
		t.Fatal(err)
	}
	got, err := restarted.runArchiveCollectionIntent(restored)
	if err != nil {
		t.Fatal(err)
	}
	if got.IntentToken != original.IntentToken || got.Sources["a:9091"] != generatorrpc.ArchiveSourceIdentity(run.ID, token, "a:9091") || got.Sources["b:9091"] != generatorrpc.ArchiveSourceIdentity(run.ID, rotated, "b:9091") {
		t.Fatal("source history lost on restart/rotation")
	}
	restored.CreatedAt = restored.CreatedAt.Add(time.Second)
	if _, err := restarted.runArchiveCollectionIntent(restored); err == nil {
		t.Fatal("reused test id inherited source identity")
	}
}

type sourceIdentityArchiveClient struct {
	*archiveStreamingFakeClient
	expected string
	requests []generatorrpc.ArchiveArtifactRequest
	t        *testing.T
}

func (c *sourceIdentityArchiveClient) StreamArchiveArtifact(ctx context.Context, r generatorrpc.ArchiveArtifactRequest, w io.Writer, max int64) (int64, error) {
	c.t.Helper()
	if r.SourceIdentity != c.expected {
		c.t.Errorf("artifact %s lost expected source identity", r.Name)
	}
	c.requests = append(c.requests, r)
	return c.archiveStreamingFakeClient.StreamArchiveArtifact(ctx, r, w, max)
}
func setSourceIdentityManifest(t *testing.T, c *archiveStreamingFakeClient, identity string) {
	t.Helper()
	path := c.artifacts["manifest.json"]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["source_identity"] = identity
	data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestArchiveCollectionSourceIdentityCheckedBeforeArtifacts(t *testing.T) {
	for _, mode := range []string{"matching", "different", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			id := "archive-bound-source"
			identity := generatorrpc.ArchiveSourceIdentity(id, bytes.Repeat([]byte{1}, 32), "a")
			fake := coverageArchiveClient(t, id)
			proof := identity
			if mode == "different" {
				proof = generatorrpc.ArchiveSourceIdentity(id, bytes.Repeat([]byte{2}, 32), "a")
			}
			if mode == "legacy" {
				proof = ""
			}
			setSourceIdentityManifest(t, fake, proof)
			client := &sourceIdentityArchiveClient{archiveStreamingFakeClient: fake, expected: identity, t: t}
			b := newDistributedExecutionBackend([]string{"a"}, time.Second, nil)
			bundle, _, err := b.fetchRemoteArchiveBundleTargetForSource(context.Background(), client, id, "a", identity, 0, t.TempDir(), true, &remoteArchiveTransferBudget{remaining: 1 << 20})
			defer cleanupRemoteArchiveBundles([]remoteArchiveBundle{bundle})
			if mode == "matching" {
				if err != nil || len(client.requests) != 4 {
					t.Fatalf("collection=%v requests=%d", err, len(client.requests))
				}
			} else if err == nil || len(client.requests) != 1 {
				t.Fatal("unverified source artifact downloaded")
			}
		})
	}
}
func TestArchiveCollectionKeepsDurableMissingSources(t *testing.T) {
	tr, _, run := collectionIntentFixture(t)
	token := bytes.Repeat([]byte{1}, 32)
	if err := tr.prepareRunArchiveSources(run, []string{"a", "lost"}, 0, token); err != nil {
		t.Fatal(err)
	}
	client := coverageArchiveClient(t, run.ID)
	identity := generatorrpc.ArchiveSourceIdentity(run.ID, token, "a")
	setSourceIdentityManifest(t, client, identity)
	run.remoteClientsByTarget = map[string]generatorClient{"a": client}
	b := newDistributedExecutionBackend([]string{"a"}, time.Second, nil)
	bundles, coverage, err := b.fetchRemoteArchiveBundlesWithCoverage(context.Background(), tr, run, false)
	defer cleanupRemoteArchiveBundles(bundles)
	if err != nil || len(bundles) != 1 || coverage == nil || len(coverage.Sources) != 2 {
		t.Fatalf("coverage=%+v err=%v", coverage, err)
	}
	for _, source := range coverage.Sources {
		if source.Target == "lost" && source.Collected {
			t.Fatal("missing durable source counted as collected")
		}
	}

	// A restart may have no transport or shard snapshot; durable sources still
	// form explicit missing coverage rather than disappearing from the run.
	run.remoteClientsByTarget = nil
	bundles, coverage, err = b.fetchRemoteArchiveBundlesWithCoverage(context.Background(), tr, run, false)
	cleanupRemoteArchiveBundles(bundles)
	if err != nil || len(bundles) != 0 || coverage == nil || len(coverage.Sources) != 2 {
		t.Fatalf("lost transports coverage=%+v err=%v", coverage, err)
	}
	for _, source := range coverage.Sources {
		if source.Collected {
			t.Fatal("lost transport counted as collected")
		}
	}
	run.remoteClientsByTarget = map[string]generatorClient{"a": client}
	run.remoteClientsByTarget["unbound"] = client
	if _, _, err := b.fetchRemoteArchiveBundlesWithCoverage(context.Background(), tr, run, false); err == nil {
		t.Fatal("unbound source entered merge")
	}
}

type rejectedCollectionIntentStore struct {
	*storage.SQLiteStore
	called atomic.Bool
}

func (s *rejectedCollectionIntentStore) PrepareRunArchiveCollection(string, string, time.Time, map[string]string, string, string) (*storage.RunArchiveCollectionIntent, bool, error) {
	s.called.Store(true)
	return nil, false, errors.New("injected collection persistence failure")
}

type collectionAssignmentClient struct {
	*generatorrpc.Client
	calls atomic.Int32
}

func (c *collectionAssignmentClient) AssignWork(ctx context.Context, a generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	c.calls.Add(1)
	return c.Client.AssignWork(ctx, a)
}
func TestArchiveCollectionPersistenceFailurePreventsAssignment(t *testing.T) {
	tr, store, run := collectionIntentFixture(t)
	rejected := &rejectedCollectionIntentStore{SQLiteStore: store}
	tr.testStore = rejected
	rpc, _ := newTestGeneratorRPCClient(t, "collection-generator", 10)
	client := &collectionAssignmentClient{Client: rpc}
	backend := newDistributedExecutionBackend([]string{"collection-generator"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	backend.enableRunSupervision(time.Minute, 10*time.Second)
	script := &dsl.Script{ID: "collection-script", Name: "collection", Protocol: "http"}
	run.Status = "INITIALIZING"
	run.Duration = time.Minute
	run.Groups = []TestGroup{{Name: "group", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 1, script: script}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run.executionCtx = ctx
	run.cancel = cancel
	backend.Run(tr, run)
	if !rejected.called.Load() || client.calls.Load() != 0 || run.Status != "FAILED" || !strings.Contains(run.Error, "collection persistence failure") {
		t.Fatalf("called=%v assignments=%d status=%s error=%s", rejected.called.Load(), client.calls.Load(), run.Status, run.Error)
	}
}

func TestArchiveCollectionPersistenceFailureLeavesUnassignedEvacuationCandidateUntouched(t *testing.T) {
	tr, store, run := collectionIntentFixture(t)
	if err := tr.prepareRunArchiveSources(run, []string{"lost"}, 0, bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}

	rejected := &rejectedCollectionIntentStore{SQLiteStore: store}
	tr.testStore = rejected
	rpc, _ := newTestGeneratorRPCClient(t, "collection-evacuation", 10)
	client := &collectionEvacuationClient{collectionAssignmentClient: &collectionAssignmentClient{Client: rpc}}
	b := newDistributedExecutionBackend([]string{"new"}, time.Second, nil)
	script := &dsl.Script{ID: "evac-script", Name: "evacuation", Protocol: "http"}
	group := TestGroup{Name: "group", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 1, script: script}
	_, _, touched, _, err := b.applyEvacuationShardAdjustments(context.Background(), context.Background(), tr, run, run.ID, 0, false, time.Minute, 0, false,
		map[string]TestGroup{"group": group}, map[string]generatorClient{"new": client}, nil,
		map[string][]GeneratorShardSummary{"group": {{Group: "group", Target: "lost", VUsers: 1}}},
		map[string][]GeneratorShardSummary{"group": {{Group: "group", Target: "new", VUsers: 1}}}, "lost", bytes.Repeat([]byte{1}, 32))
	if err == nil || !rejected.called.Load() || client.calls.Load() != 0 || client.stops.Load() != 0 || len(touched) != 0 {
		t.Fatalf("err=%v persistence=%v assignments=%d stops=%d touched=%v", err, rejected.called.Load(), client.calls.Load(), client.stops.Load(), touched)
	}
}

type collectionEvacuationClient struct {
	*collectionAssignmentClient
	stops atomic.Int32
}

func (c *collectionEvacuationClient) StopTestAtGenerationWithToken(ctx context.Context, id string, graceful bool, generation uint64, token []byte) (generatorrpc.StopResult, error) {
	c.stops.Add(1)
	return c.Client.StopTestAtGenerationWithToken(ctx, id, graceful, generation, token)
}

func TestArchiveCollectionLegacyEvacuationDoesNotCreatePartialIdentityMap(t *testing.T) {
	tr, store, run := collectionIntentFixture(t)
	run.remoteGroupShards = map[string][]GeneratorShardSummary{"buyers": {{Group: "buyers", Target: "original", VUsers: 1}}}
	if err := tr.extendRunArchiveSources(run, []string{"replacement"}, 0, bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRunArchiveCollection(run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy run acquired incomplete identity map: %v", err)
	}
	if err := tr.prepareRunArchiveSources(run, []string{"original"}, 0, bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	if err := tr.extendRunArchiveSources(run, []string{"replacement"}, 0, bytes.Repeat([]byte{2}, 32)); err != nil {
		t.Fatal(err)
	}
	if err := tr.extendRunArchiveSources(run, []string{"unbound"}, 0, nil); err == nil {
		t.Fatal("bound evacuation accepted missing supervision token")
	}
	got, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || len(got.Sources) != 2 {
		t.Fatalf("bound evacuation failed to extend source map: %+v %v", got, err)
	}
}

func TestArchiveCollectionDispatchBindsDistinctTargetIdentities(t *testing.T) {
	tr, store, run := collectionIntentFixture(t)
	token := bytes.Repeat([]byte{1}, 32)
	if err := tr.prepareRunArchiveSources(run, []string{"a", "b"}, 0, token); err != nil {
		t.Fatal(err)
	}
	first, second := &startupAssignmentFakeClient{}, &startupAssignmentFakeClient{}
	plans := []distributedTargetAssignmentPlan{
		{target: "a", client: first, shards: []GeneratorShardSummary{{Target: "a", Group: "group", VUsers: 1, ShardCount: 2, ShardOrdinal: 0}}},
		{target: "b", client: second, shards: []GeneratorShardSummary{{Target: "b", Group: "group", VUsers: 1, ShardCount: 2, ShardOrdinal: 1}}},
	}
	if err := tr.bindRunArchiveSourcePlans(run, plans); err != nil {
		t.Fatal(err)
	}
	b := newDistributedExecutionBackend([]string{"a", "b"}, time.Second, nil)
	outcomes := b.assignDistributedGroupTargets(context.Background(), plans, generatorrpc.Script{}, generatorrpc.Assignment{TestID: run.ID, GroupName: "group", RunSupervisionToken: token}, "script", nil, nil)
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Sources["a"] == intent.Sources["b"] {
		t.Fatal("targets share archive identity")
	}
	for target, client := range map[string]*startupAssignmentFakeClient{"a": first, "b": second} {
		if len(client.assignments) != 1 {
			t.Fatal("assignment missing")
		}
		a := client.assignments[0]
		if a.ArchiveSourceTarget != target || generatorrpc.ArchiveSourceIdentity(run.ID, a.RunSupervisionToken, a.ArchiveSourceTarget) != intent.Sources[target] {
			t.Fatal("assignment differs from persisted source identity")
		}
	}
}
