package generatorrpc

import (
	"context"
	"crypto/sha256"
	"fmt"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourcePlanCommitsOneOwnerAndCompleteMembership(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	service := NewService("source", 3, WithExecution())
	defer service.StopTest(context.Background(), &pb.StopRequest{TestId: "source-run"})
	script := []byte(fmt.Sprintf(`{"project_id":"p","name":"source","version":"1","protocol":"http","config":{"base_url":%q},"actions":[{"type":"GET","name":"read","target":"/","think_time":{"type":"fixed","mean_ms":10}}]}`, target.URL))
	hash := fmt.Sprintf("%x", sha256.Sum256(script))
	m := &pb.SourcePlanManifest{Version: 1, ProjectId: "p", SourcePlanId: "source", RequiredFeatures: []string{FeatureSourcePlanV1}}
	for _, name := range []string{"a", "b", "c"} {
		m.Groups = append(m.Groups, &pb.SourcePlanGroup{GroupName: name, SourceGroupId: name, ScriptHash: hash, Vusers: 1})
	}
	commit := &pb.WorkCommitRequest{TestId: "source-run", SourcePlanDigest: SourcePlanDigest(m)}
	for i, g := range m.Groups {
		a := &pb.WorkAssignment{TestId: "source-run", GroupName: g.GroupName, ScriptId: g.GroupName, ScriptContent: script, ExpectedScriptHash: hash, VuserCount: 1, TargetDuration: durationpb.New(200 * time.Millisecond), SourcePlan: m}
		ack, err := service.AssignWork(context.Background(), a)
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("stage %s: %v %v", g.GroupName, ack, err)
		}
		if i == 0 && ack.AvailableCapacity != 0 {
			t.Fatalf("complete plan capacity was not reserved: %d", ack.AvailableCapacity)
		}

		if hits.Load() != 0 {
			t.Fatal("staged plan sent traffic")
		}
	}
	ack, err := service.CommitWork(context.Background(), commit)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("commit: %v %v", ack, err)
	}
	// A duplicate commit must retain the existing owner and send no second run.
	if retry, err := service.CommitWork(context.Background(), commit); err != nil || !retry.GetAccepted() {
		t.Fatalf("commit retry: %v %v", retry, err)
	}
	service.mu.Lock()
	execs := service.executions["source-run"]
	owner := execs["a"].owner
	for _, e := range execs {
		if e.owner != owner || e.engine != owner.engine {
			t.Fatal("groups have independent owners")
		}
	}
	done := owner.done
	service.mu.Unlock()
	if names := owner.engine.GroupNames(); len(names) != 3 {
		t.Fatalf("engine groups: %v", names)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("source plan did not finish")
	}
	if hits.Load() == 0 {
		t.Fatal("no traffic after commit")
	}
}
func TestSourcePlanManifestValidationAndFingerprint(t *testing.T) {
	m := &pb.SourcePlanManifest{Version: 1, ProjectId: "p", SourcePlanId: "source", RequiredFeatures: []string{FeatureSourcePlanV1}, Groups: []*pb.SourcePlanGroup{{GroupName: "a", SourceGroupId: "node", ScriptHash: fmt.Sprintf("%064d", 0), Vusers: 2}}}
	original := &pb.WorkAssignment{GroupName: "a", ExpectedScriptHash: m.Groups[0].ScriptHash, VuserCount: 2, SourcePlan: m}
	if err := validateSourcePlanAssignment(original); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*pb.WorkAssignment){func(a *pb.WorkAssignment) { a.SourcePlan.Version++ }, func(a *pb.WorkAssignment) { a.SourcePlan.Groups = append(a.SourcePlan.Groups, a.SourcePlan.Groups[0]) }, func(a *pb.WorkAssignment) { a.SourcePlan.Groups[0].ScriptHash = "bad" }, func(a *pb.WorkAssignment) { a.ShardCount = 2 }, func(a *pb.WorkAssignment) { a.SourcePlan.ExecutionGeneration = 1 }, func(a *pb.WorkAssignment) { a.GroupName = "other" }} {
		a := proto.Clone(original).(*pb.WorkAssignment)
		change(a)
		if validateSourcePlanAssignment(a) == nil {
			t.Fatal("invalid manifest accepted")
		}
		if workAssignmentFingerprint(a) == workAssignmentFingerprint(original) {
			t.Fatal("manifest omitted from fingerprint")
		}
	}
}

func TestSourcePlanIncompleteCommitReleasesAllReservedCapacity(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	service := NewService("source", 3)
	script := []byte(`{"project_id":"p","name":"source","version":"1","protocol":"http","config":{"base_url":"https://example.invalid"},"actions":[{"type":"GET","name":"get","target":"/"}]}`)
	hash := fmt.Sprintf("%x", sha256.Sum256(script))
	m := &pb.SourcePlanManifest{Version: 1, ProjectId: "p", SourcePlanId: "source", RequiredFeatures: []string{FeatureSourcePlanV1}, Groups: []*pb.SourcePlanGroup{{GroupName: "a", SourceGroupId: "a", ScriptHash: hash, Vusers: 1}, {GroupName: "b", SourceGroupId: "b", ScriptHash: hash, Vusers: 2}}}
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "incomplete", GroupName: "a", ScriptId: "a", ScriptContent: script, ExpectedScriptHash: hash, VuserCount: 1, SourcePlan: m})
	if err != nil || !ack.GetAccepted() || ack.AvailableCapacity != 0 {
		t.Fatalf("stage: %v %v", ack, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "incomplete", SourcePlanDigest: SourcePlanDigest(m)}); err == nil && ack.GetAccepted() {
		t.Fatal("partial manifest committed")
	}
	service.mu.RLock()
	available := service.availableCapacityLocked()
	_, exists := service.assignments["incomplete"]
	service.mu.RUnlock()
	if exists || available != 3 {
		t.Fatalf("failed manifest retained reservations: exists=%v available=%d", exists, available)
	}
}

func TestSourcePlanClientCloseDropsManifestState(t *testing.T) {
	c := &Client{}
	c.sourcePlans.Store(sourcePlanClientKey{"run", 1}, "digest")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.sourcePlans.Load(sourcePlanClientKey{"run", 1}); ok {
		t.Fatal("closed client retained source plan")
	}
}

func TestSourcePlanGroupSnapshotDoesNotMultiplySharedEvidence(t *testing.T) {
	owner := &sourcePlanExecution{telemetryGroup: "a"}
	full := engine.MetricsSnapshot{Transactions: 90, ActiveVUsers: 200, Telemetry: &engine.TelemetryMetrics{DroppedResults: 7}, ByGroup: map[string]*engine.GroupMetrics{"a": {ActiveVUsers: 100}, "b": {ActiveVUsers: 50}, "c": {ActiveVUsers: 50}}}
	var users, dropped int64
	for _, name := range []string{"a", "b", "c"} {
		snapshot := owner.groupSnapshot(full, name)
		users += snapshot.ActiveVUsers
		if snapshot.Transactions != 0 {
			t.Fatal("aggregate transactions duplicated into group")
		}
		if snapshot.Telemetry != nil {
			dropped += snapshot.Telemetry.DroppedResults
		}
	}
	if users != 200 || dropped != 7 {
		t.Fatalf("shared evidence multiplied/lost: users=%d dropped=%d", users, dropped)
	}
}

func TestSourcePlanOwnerFailureReleasesEveryWaiter(t *testing.T) {
	owner := &sourcePlanExecution{done: make(chan struct{})}
	// A missing engine injects an owner-level failure, outside sampler handling.
	for range 3 {
		if err := owner.run(context.Background(), &groupExecution{}); err == nil {
			t.Fatal("owner failure was hidden")
		}
	}
	select {
	case <-owner.done:
	default:
		t.Fatal("owner waiters remain blocked")
	}
	if _, err := assembleSourcePlan(nil); err == nil {
		t.Fatal("empty source plan assembled")
	}
}

func TestSourcePlanScriptIdentityCannotBeWeakened(t *testing.T) {
	a := &pb.WorkAssignment{GroupName: "g", SourcePlan: &pb.SourcePlanManifest{ProjectId: "p", SourcePlanId: "source", CompatibilityProfile: dsl.JMeterMessagingProfile, Groups: []*pb.SourcePlanGroup{{GroupName: "g", SourceGroupId: "node"}}}}
	script := &dsl.Script{ProjectID: "p", Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile, SourcePlanID: "source", SourceGroupID: "node"}}}
	if err := validateSourcePlanScript(a, script); err != nil {
		t.Fatal(err)
	}
	script.ProjectID = "other"
	if validateSourcePlanScript(a, script) == nil {
		t.Fatal("cross-project group accepted")
	}
	script.ProjectID = "p"
	script.Config.JMeter = nil
	if validateSourcePlanScript(a, script) == nil {
		t.Fatal("ordinary script accepted inside compatible source plan")
	}
	a.SourcePlan.CompatibilityProfile = ""
	if err := validateSourcePlanScript(a, script); err != nil {
		t.Fatal(err)
	}
	a.SourcePlan = nil
	if err := validateSourcePlanScript(a, script); err != nil {
		t.Fatal(err)
	}
}

func TestSourcePlanRejectedManifestOmissionReleasesReservation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	service := NewService("source", 3)
	script := []byte(`{"project_id":"p","name":"source","version":"1","protocol":"http","config":{"base_url":"https://example.invalid"},"actions":[{"type":"GET","name":"get","target":"/"}]}`)
	hash := fmt.Sprintf("%x", sha256.Sum256(script))
	manifest := &pb.SourcePlanManifest{Version: 1, ProjectId: "p", SourcePlanId: "source", RequiredFeatures: []string{FeatureSourcePlanV1}, Groups: []*pb.SourcePlanGroup{{GroupName: "a", SourceGroupId: "a", ScriptHash: hash, Vusers: 1}, {GroupName: "b", SourceGroupId: "b", ScriptHash: hash, Vusers: 2}}}
	assignment := &pb.WorkAssignment{TestId: "omitted", GroupName: "a", ScriptId: "a", ScriptContent: script, ExpectedScriptHash: hash, VuserCount: 1, SourcePlan: manifest}
	if ack, err := service.AssignWork(context.Background(), assignment); err != nil || !ack.GetAccepted() {
		t.Fatalf("stage: %v %v", ack, err)
	}
	assignment.SourcePlan = nil
	assignment.GroupName = "b"
	assignment.VuserCount = 2
	if ack, err := service.AssignWork(context.Background(), assignment); err == nil && ack.GetAccepted() {
		t.Fatal("manifest omission accepted")
	}
	service.mu.RLock()
	available := service.availableCapacityLocked()
	service.mu.RUnlock()
	if available != 3 {
		t.Fatalf("failed stage retained reservation: %d", available)
	}
}
