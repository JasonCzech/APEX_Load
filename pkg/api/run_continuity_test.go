package api

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

func testContinuityWrapper(t *testing.T) keywrap.KeyWrapper {
	t.Helper()
	wrapper, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: bytes.Repeat([]byte("k"), 32), KeyID: "test"})
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}
	return wrapper
}

func TestFailoverPolicyNormalization(t *testing.T) {
	for input, want := range map[string]string{"": "", "fail": "fail", " Continue ": "continue"} {
		out, err := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{FailoverPolicy: input})
		if err != nil || out.FailoverPolicy != want {
			t.Fatalf("normalize(%q) = (%q, %v), want %q", input, out.FailoverPolicy, err, want)
		}
	}
	if _, err := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{FailoverPolicy: "resume"}); err == nil {
		t.Fatal("unknown failover policy was accepted")
	}
	if _, err := normalizeGroupGeneratorAssignmentSettings(&GeneratorAssignmentSettings{FailoverPolicy: "continue"}); err == nil || !strings.Contains(err.Error(), "test-scoped") {
		t.Fatalf("group-level failover policy error = %v, want test-scoped rejection", err)
	}
	if got := effectiveFailoverPolicy(nil); got != failoverPolicyFail {
		t.Fatalf("default policy = %q, want fail", got)
	}
	cloned := cloneGeneratorAssignmentSettings(&GeneratorAssignmentSettings{FailoverPolicy: "continue"})
	if cloned.FailoverPolicy != "continue" {
		t.Fatal("clone dropped failover policy")
	}
}

func TestRunSupervisionTokenSealIsBoundToRunAndGeneration(t *testing.T) {
	wrapper := testContinuityWrapper(t)
	ctx := context.Background()
	token := bytes.Repeat([]byte{7}, generatorrpc.RunSupervisionTokenSize)
	sealed, err := sealRunSupervisionToken(ctx, wrapper, "run-a", 3, token)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(sealed.Ciphertext, string(token)) {
		t.Fatal("sealed token contains the plaintext")
	}
	checkpoint := runContinuityCheckpoint{Version: runContinuityCheckpointVersion, ExecutionGeneration: 3, RunSupervisionToken: sealed}
	opened, err := openRunSupervisionToken(ctx, wrapper, "run-a", checkpoint)
	if err != nil || !bytes.Equal(opened, token) {
		t.Fatalf("open = (%x, %v), want original token", opened, err)
	}
	if _, err := openRunSupervisionToken(ctx, wrapper, "run-b", checkpoint); err == nil {
		t.Fatal("token opened for a different run")
	}
	checkpoint.ExecutionGeneration = 4
	if _, err := openRunSupervisionToken(ctx, wrapper, "run-a", checkpoint); err == nil {
		t.Fatal("token opened under a different execution generation")
	}
	if _, err := sealRunSupervisionToken(ctx, nil, "run-a", 3, token); err == nil {
		t.Fatal("sealing without a wrapper succeeded")
	}
}

func TestRunContinuityLaunchWarning(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	failRun := &ManagedTest{ID: "fail-run", Assignment: &GeneratorAssignmentSettings{}}
	if warning := runner.runContinuityLaunchWarning(failRun); warning != nil {
		t.Fatalf("fail-policy run produced a continuity warning: %+v", warning)
	}
	continueRun := &ManagedTest{ID: "continue-run", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}
	warning := runner.runContinuityLaunchWarning(continueRun)
	if warning == nil || warning.Severity != "warning" || warning.Code != "failover_continuity_unavailable" {
		t.Fatalf("continue-policy warning = %+v", warning)
	}
	for _, want := range []string{"HA mode", "local backend", "key wrapper"} {
		if !strings.Contains(warning.Message, want) {
			t.Errorf("warning %q does not explain %q", warning.Message, want)
		}
	}
}

func TestArmRunContinuityPersistsSealedCheckpoint(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	wrapper := testContinuityWrapper(t)
	runner := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(wrapper))
	leadership := newExecutionLeadership(store, "continuity-arm")
	leadership.tick()
	runner.leadership = leadership
	runner.durableExecutionFence = true
	owner, generation := leadership.executionLeaseTerm()
	if owner == "" || generation == 0 {
		t.Fatal("test leadership did not acquire the lease")
	}

	token := bytes.Repeat([]byte{9}, generatorrpc.RunSupervisionTokenSize)
	shards := map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-1:9091", VUsers: 5, ShardCount: 2}, {Group: "ga", Target: "gen-2:9091", VUsers: 5, ShardOrdinal: 1, ShardCount: 2, VUserIDOffset: 5}}}
	for _, tc := range []struct {
		id     string
		policy string
		armed  bool
	}{{"arm-continue", "continue", true}, {"arm-fail", "", false}} {
		if err := store.SaveTest(&storage.TestRecord{ID: tc.id, Name: tc.id, Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
			t.Fatalf("save %s: %v", tc.id, err)
		}
		test := &ManagedTest{ID: tc.id, Assignment: &GeneratorAssignmentSettings{FailoverPolicy: tc.policy},
			remoteTargetStates:           map[string]string{"gen-2:9091": "complete"},
			remoteArchiveClientsByTarget: map[string]generatorClient{"retired:9091": &adoptionFakeClient{}},
			pinnedSecrets:                &runPinnedSecretState{},
		}
		test.pinnedSecrets.setIfAbsent(map[int]map[string]string{0: {"LOGIN_PASSWORD": "secret-before-rotation"}})
		runner.armRunContinuity(context.Background(), test, generation, owner, token, shards)
		record, err := store.GetTest(tc.id)
		if err != nil {
			t.Fatalf("get %s: %v", tc.id, err)
		}
		checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
		if err != nil || present != tc.armed {
			t.Fatalf("%s checkpoint present=%v err=%v, want present=%v", tc.id, present, err, tc.armed)
		}
		if !tc.armed {
			continue
		}
		if strings.Contains(record.ContinuityCheckpoint, string(token)) || strings.Contains(record.ContinuityCheckpoint, "secret-before-rotation") {
			t.Fatal("checkpoint stores secret material in plaintext")
		}
		pinned, err := openRunPinnedSecrets(context.Background(), wrapper, tc.id, checkpoint.PinnedSecretCiphertext, checkpoint.PinnedSecretKey)
		if err != nil || pinned[0]["LOGIN_PASSWORD"] != "secret-before-rotation" {
			t.Fatalf("recovered pinned secret = %q, %v", pinned[0]["LOGIN_PASSWORD"], err)
		}
		if checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner {
			t.Fatalf("checkpoint term = (%d, %q), want (%d, %q)", checkpoint.ExecutionGeneration, checkpoint.ExecutionLeaseOwnerID, generation, owner)
		}
		if got := checkpoint.Shards["ga"]; len(got) != 2 || got[1].VUserIDOffset != 5 || got[1].Target != "gen-2:9091" {
			t.Fatalf("checkpoint shards = %+v", checkpoint.Shards)
		}
		if len(checkpoint.CompletedTargets) != 1 || checkpoint.CompletedTargets[0] != "gen-2:9091" || len(checkpoint.ArchiveTargets) != 1 || checkpoint.ArchiveTargets[0] != "retired:9091" {
			t.Fatalf("checkpoint lost finished participants: %+v", checkpoint)
		}
		opened, err := openRunSupervisionToken(context.Background(), wrapper, tc.id, checkpoint)
		if err != nil || !bytes.Equal(opened, token) {
			t.Fatalf("recovered token = (%x, %v), want original", opened, err)
		}
	}
}

func TestArmRunContinuityWithoutWrapperLeavesRunUnarmed(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	runner := NewTestRunner(10, NewHub(), store)
	leadership := newExecutionLeadership(store, "continuity-no-wrapper")
	leadership.tick()
	runner.leadership = leadership
	runner.durableExecutionFence = true
	owner, generation := leadership.executionLeaseTerm()
	if err := store.SaveTest(&storage.TestRecord{ID: "unwrapped", Name: "unwrapped", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	test := &ManagedTest{ID: "unwrapped", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}
	runner.armRunContinuity(context.Background(), test, generation, owner, bytes.Repeat([]byte{1}, generatorrpc.RunSupervisionTokenSize), nil)
	record, err := store.GetTest("unwrapped")
	if err != nil || record.ContinuityCheckpoint != "" {
		t.Fatalf("run without a key wrapper was armed: %q (%v)", record.ContinuityCheckpoint, err)
	}
}
