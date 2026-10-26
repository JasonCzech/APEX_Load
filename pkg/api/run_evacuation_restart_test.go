package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// readyCapacityServer is an in-process generator whose heartbeat can report
// its pod capacity configured, as a deployed generator's does, so the
// controller plans the restart's reservation as it would on a cluster. Its
// capacity calls are then stubbed (below): an in-process generator has no
// runtime limits. available, when set, overrides the free VUser capacity
// each heartbeat probe reports, numbered from 1.
type readyCapacityServer struct {
	*generatorrpc.Service
	ready     bool
	available func(probe int) int32
	probes    atomic.Int32
	// withoutFeatures are left out of the heartbeat, as a generator that
	// lacks those capabilities reports.
	withoutFeatures []string
}

type readyCapacityHeartbeat struct {
	pb.GeneratorControl_HeartbeatServer
	server *readyCapacityServer
	probe  int
}

func (s readyCapacityHeartbeat) Send(pong *pb.HeartbeatPong) error {
	if s.server.ready {
		if pong.PodCapacity != nil && pong.PodCapacity.State == "unconfigured" {
			pong.PodCapacity.State = "ready"
			pong.PodCapacity.Schedulable = generatorrpc.CapacityVectorToProto(rollbackBudget)
		}
		if !slices.Contains(pong.CompatibleFeatures, generatorrpc.FeaturePodCapacityV1) {
			pong.CompatibleFeatures = append(pong.CompatibleFeatures, generatorrpc.FeaturePodCapacityV1)
		}
	}
	if s.server.available != nil {
		pong.AvailableCapacity = s.server.available(s.probe)
	}
	if len(s.server.withoutFeatures) > 0 {
		pong.CompatibleFeatures = slices.DeleteFunc(pong.CompatibleFeatures, func(feature string) bool {
			return slices.Contains(s.server.withoutFeatures, feature)
		})
	}
	return s.GeneratorControl_HeartbeatServer.Send(pong)
}

func (s *readyCapacityServer) Heartbeat(stream pb.GeneratorControl_HeartbeatServer) error {
	return s.Service.Heartbeat(readyCapacityHeartbeat{GeneratorControl_HeartbeatServer: stream, server: s, probe: int(s.probes.Add(1))})
}

// ReservePodCapacity confirms every claim for the incarnation the heartbeat
// reported: an in-process generator has no runtime limits to size a ledger by.
func (s *readyCapacityServer) ReservePodCapacity(ctx context.Context, claim *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
	if !s.ready {
		return s.Service.ReservePodCapacity(ctx, claim)
	}
	return &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: claim.GetIncarnation()}}, nil
}

// restartRecorder records what a restart's new replacement was asked to run.
type restartRecorder struct {
	mu          sync.Mutex
	assignments []generatorrpc.Assignment
	commits     []uint64
	// accepted lists the generation of each CommitWork the generator accepted.
	accepted     []uint64
	caps         []generatorrpc.ThroughputCap
	beforeAssign func()
	// assigned, supervised and committed record each AssignWork,
	// ClaimRunSupervision and CommitWork with its outcome.
	assigned   []restartCall
	supervised []restartCall
	committed  []restartCall
	// refuseSupervision makes the generator refuse the restart's supervision.
	refuseSupervision bool
	// beforeSupervision runs when a restart claims the staged replacement's
	// supervision; beforeCommit and afterCommit around its CommitWork.
	beforeSupervision func()
	beforeCommit      func()
	afterCommit       func()
}

// restartCall is one generation-fenced call and its outcome: an error, or an
// answer with the generator's refusal message.
type restartCall struct {
	generation uint64
	accepted   bool
	err        error
	message    string
}

// refusedByGenerator reports a call the generator itself turned down: a gRPC
// status from the generator, or an answer that declines with a message. A
// transport failure or a cancelled call is not a refusal.
func (c restartCall) refusedByGenerator() bool {
	if c.accepted {
		return false
	}
	if c.err == nil {
		return strings.TrimSpace(c.message) != ""
	}
	code := status.Code(c.err)
	return code != codes.Unknown && code != codes.Unavailable && code != codes.DeadlineExceeded && code != codes.Canceled
}

func (r *restartRecorder) calls() (assigned, supervised, committed []restartCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.assigned), slices.Clone(r.supervised), slices.Clone(r.committed)
}

func (r *restartRecorder) hook(pick func(*restartRecorder) func()) {
	r.mu.Lock()
	hook := pick(r)
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (r *restartRecorder) throughputCaps() []generatorrpc.ThroughputCap {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.caps)
}

func (c *restartRecordingClient) SetThroughputCap(ctx context.Context, request generatorrpc.ThroughputCap) error {
	recorded := request
	recorded.RunSupervisionToken = nil
	c.recorder.mu.Lock()
	c.recorder.caps = append(c.recorder.caps, recorded)
	c.recorder.mu.Unlock()
	return c.Client.SetThroughputCap(ctx, request)
}

func (r *restartRecorder) acceptedCommits() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.accepted)
}

func (r *restartRecorder) recordCommit(generation uint64, result generatorrpc.WorkCommitResult, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	accepted := err == nil && result.Accepted
	r.committed = append(r.committed, restartCall{generation: generation, accepted: accepted, err: err, message: result.ErrorMessage})
	if accepted {
		r.accepted = append(r.accepted, generation)
	}
}

func (r *restartRecorder) snapshot() ([]generatorrpc.Assignment, []uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.assignments), slices.Clone(r.commits)
}

// restartRecordingClient is one dial of a recorded in-process generator.
type restartRecordingClient struct {
	*generatorrpc.Client
	recorder *restartRecorder
}

func (c *restartRecordingClient) AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	c.recorder.hook(func(r *restartRecorder) func() { return r.beforeAssign })
	recorded := assignment
	// The caller clears its token once the evacuation returns.
	recorded.RunSupervisionToken = slices.Clone(assignment.RunSupervisionToken)
	c.recorder.mu.Lock()
	c.recorder.assignments = append(c.recorder.assignments, recorded)
	c.recorder.mu.Unlock()
	result, err := c.Client.AssignWork(ctx, assignment)
	c.recorder.mu.Lock()
	c.recorder.assigned = append(c.recorder.assigned, restartCall{generation: assignment.ExecutionGeneration, accepted: err == nil && result.Accepted, err: err, message: result.ErrorMessage})
	c.recorder.mu.Unlock()
	return result, err
}

func (c *restartRecordingClient) ClaimRunSupervision(ctx context.Context, claim generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
	c.recorder.hook(func(r *restartRecorder) func() { return r.beforeSupervision })
	c.recorder.mu.Lock()
	refuse := c.recorder.refuseSupervision
	c.recorder.mu.Unlock()
	result, err := generatorrpc.RunSupervisionResult{}, errors.New("supervision refused")
	if !refuse {
		result, err = c.Client.ClaimRunSupervision(ctx, claim)
	}
	c.recorder.mu.Lock()
	c.recorder.supervised = append(c.recorder.supervised, restartCall{generation: claim.ExecutionGeneration, accepted: err == nil && result.Accepted, err: err, message: result.ErrorMessage})
	c.recorder.mu.Unlock()
	return result, err
}

func (c *restartRecordingClient) CommitWorkAt(ctx context.Context, testID string, generation uint64, startAt time.Time) (generatorrpc.WorkCommitResult, error) {
	c.recorder.mu.Lock()
	c.recorder.commits = append(c.recorder.commits, generation)
	c.recorder.mu.Unlock()
	result, err := c.Client.CommitWorkAt(ctx, testID, generation, startAt)
	c.recorder.recordCommit(generation, result, err)
	return result, err
}

func (c *restartRecordingClient) CommitWorkSupervisedAt(ctx context.Context, testID string, generation uint64, startAt time.Time, token []byte) (generatorrpc.WorkCommitResult, error) {
	c.recorder.hook(func(r *restartRecorder) func() { return r.beforeCommit })
	c.recorder.mu.Lock()
	c.recorder.commits = append(c.recorder.commits, generation)
	c.recorder.mu.Unlock()
	result, err := c.Client.CommitWorkSupervisedAt(ctx, testID, generation, startAt, token)
	c.recorder.recordCommit(generation, result, err)
	c.recorder.hook(func(r *restartRecorder) func() { return r.afterCommit })
	return result, err
}

// restartGenerator is an in-process generator that can take a restarted
// share. Every dial returns its own retained handle.
type restartGenerator struct {
	client   *generatorrpc.Client
	service  *generatorrpc.Service
	recorder *restartRecorder
}

func (g *restartGenerator) dial() (generatorClient, error) {
	handle, err := g.client.Retain()
	if err != nil {
		return nil, err
	}
	return &restartRecordingClient{Client: handle, recorder: g.recorder}, nil
}

func newRestartGenerator(t *testing.T, id string, maxVUsers int, readyCapacity bool, configure ...func(*readyCapacityServer)) *restartGenerator {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	service := generatorrpc.NewService(id, maxVUsers, generatorrpc.WithExecution(), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	wrapper := &readyCapacityServer{Service: service, ready: readyCapacity}
	for _, apply := range configure {
		apply(wrapper)
	}
	pb.RegisterGeneratorControlServer(server, wrapper)
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(ctx, id, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		server.Stop()
		t.Fatalf("dial %s: %v", id, err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = service.Shutdown(shutdownCtx)
		server.Stop()
		_ = listener.Close()
	})
	return &restartGenerator{client: client, service: service, recorder: &restartRecorder{}}
}

type restartOptions struct {
	rollForwardOptions
	// restartVUsers is the new replacement's VUser capacity (default 10).
	restartVUsers int
	// noRestartTarget leaves the fleet without a spare generator.
	noRestartTarget bool
	// settingOff leaves the restart setting off.
	settingOff bool
	// pausedFor is the run's durable cumulative pause before the failover.
	pausedFor time.Duration
	// configureRestartTarget adjusts gen-d's server.
	configureRestartTarget func(*readyCapacityServer)
}

// newEvacuationRestartFixture journals an evacuation of gen-a to gen-b that
// left the survivor gen-c unchanged, enables the restart and offers gen-d, an
// in-process generator, as the new replacement. The moved group runs a real
// HTTP script against a local target.
func newEvacuationRestartFixture(t *testing.T, opts restartOptions, beforeHandOver ...func(*evacuationRollbackFixture)) (*evacuationRollbackFixture, *restartGenerator) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	if opts.phase == "" {
		opts.phase = runEvacuationPhaseSourceFenced
	}
	if opts.restartVUsers == 0 {
		opts.restartVUsers = 10
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	var spare *restartGenerator
	if !opts.noRestartTarget {
		var configure []func(*readyCapacityServer)
		if opts.configureRestartTarget != nil {
			configure = append(configure, opts.configureRestartTarget)
		}
		spare = newRestartGenerator(t, "gen-d", opts.restartVUsers, !opts.noCapacity, configure...)
	}
	hooks := append([]func(*evacuationRollbackFixture){func(f *evacuationRollbackFixture) {
		f.tr.evacuationReplacementRestart = !opts.settingOff
		script := &dsl.Script{ID: "restart-script", Name: "Restart", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: target.URL}, Actions: []dsl.Action{{Name: "home", Type: "GET", Target: "/"}}}
		for index := range f.test.Groups {
			if f.test.Groups[index].Name != "gc" {
				f.test.Groups[index].ScriptID, f.test.Groups[index].ScriptName, f.test.Groups[index].script = script.ID, script.Name, script
			}
		}
		// The run's clock and pause state are durable, as a successor reads them.
		if opts.pausedFor > 0 {
			started := f.test.StartedAt.Add(-opts.pausedFor)
			f.test.StartedAt, f.test.CumulativePause = &started, opts.pausedFor
		}
		record, err := f.store.GetTest(f.test.ID)
		if err != nil {
			t.Fatal(err)
		}
		record.Status, record.StartedAt, record.Duration = f.test.Status, f.test.StartedAt, int64(f.test.Duration)
		record.PausedAt, record.CumulativePause = f.test.PausedAt, int64(f.test.CumulativePause)
		if err := f.store.SaveTest(record); err != nil {
			t.Fatal(err)
		}
		if !opts.noCapacity {
			settings := f.tr.GetSystemSettings()
			settings.CapacityPlanning.PodReservationsEnabled = true
			f.tr.settings = settings
		}
		f.extra = map[string]func() (generatorClient, error){}
		if spare != nil {
			f.extra["gen-d"] = spare.dial
		}
		// In-process generators need more than the fakes' 1 s under load.
		f.backendTimeout = 10 * time.Second
	}}, beforeHandOver...)
	f := newEvacuationRollForwardFixture(t, opts.rollForwardOptions, hooks...)
	f.superviseRuns()
	// Whatever runner leads at the end, its runtimes stop before the store
	// closes (cleanups run last-registered first).
	t.Cleanup(func() { stopRunnerRuntimes(t, f.tr) })
	return f, spare
}

// superviseRuns requires run supervision, as a controller with run adoption
// does, so the new replacement commits under the rotated token.
func (f *evacuationRollbackFixture) superviseRuns() {
	f.tr.execution.(*distributedExecutionBackend).enableRunSupervision(time.Minute, 10*time.Second)
}

// restartPhaseB runs the restart part of Phase B for the adopted run, as
// resumeAdoptedRun does before it opens the metrics streams.
func (f *evacuationRollbackFixture) restartPhaseB(t *testing.T) (bool, *adoptedRunResume) {
	t.Helper()
	f.tr.mu.Lock()
	resume := f.test.adoptedResume
	f.test.adoptedResume = nil
	executionCtx := f.test.executionCtx
	token := f.test.remoteRunSupervisionToken
	f.tr.mu.Unlock()
	if resume == nil || resume.restart == nil {
		t.Fatalf("Phase A planned no restart: %+v", resume)
	}
	b := f.tr.execution.(*distributedExecutionBackend)
	return f.tr.restartEvacuationReplacement(b, f.test, resume, executionCtx, token), resume
}

// assertRestartIntent checks the run was installed on the survivors under a
// replacement_restarting intent of generation, with the old replacement's
// share still missing.
func (f *evacuationRollbackFixture) assertRestartIntent(t *testing.T, generation uint64) {
	t.Helper()
	guard, _ := f.durableGuard(t)
	if guard.Version != runEvacuationRestartCheckpointVersion || guard.Phase != runEvacuationPhaseReplacementRestarting || guard.RestartGeneration != generation ||
		guard.RollForwardGeneration != 0 || guard.RestartTargetDuration <= 0 {
		t.Fatalf("restart intent = version %d phase %q restart generation %d", guard.Version, guard.Phase, guard.RestartGeneration)
	}
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.restart == nil || resume.restart.missing != "gen-b" || resume.adopted["gen-c"] == nil || resume.adopted["gen-b"] != nil {
		t.Fatalf("survivors not installed for a restart: %+v", resume)
	}
	if f.test.remoteClientsByTarget["gen-b"] != nil || f.test.remoteClientsByTarget["gen-a"] != nil {
		t.Fatal("the old replacement or the fenced source became a participant")
	}
	if claims, _ := f.sourceGen.snapshot(); len(claims) != 0 {
		t.Fatalf("the fenced source was claimed: %+v", claims)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status == "STOPPING" || strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) {
		t.Fatalf("durable run = %+v, %v", record, err)
	}
}

// assertRestarted checks that the missing share runs on gen-d under the
// successor's term, never past the journaled duration, and that the journal
// was replaced by the plain checkpoint of the new topology.
func (f *evacuationRollbackFixture) assertRestarted(t *testing.T, spare *restartGenerator, resume *adoptedRunResume, owner string, generation uint64, paused bool, maxDuration time.Duration) {
	t.Helper()
	if resume.adopted["gen-d"] == nil || !slices.Equal(resume.restarted, []string{"gen-d"}) {
		t.Fatalf("restarted replacement not adopted for Phase B: %+v", resume)
	}
	assignments, commits := spare.recorder.snapshot()
	if len(assignments) != 1 || len(commits) != 1 || commits[0] != generation {
		t.Fatalf("new replacement assignments %d, commits %v", len(assignments), commits)
	}
	assignment := assignments[0]
	token := f.test.remoteRunSupervisionToken
	if assignment.ExecutionGeneration != generation || assignment.GroupName != "ga" || assignment.VUserCount != 5 || assignment.StartPaused != paused ||
		string(assignment.RunSupervisionToken) != string(token[:]) || assignment.DurationOffset+assignment.TargetDuration > maxDuration {
		t.Fatalf("restart assignment = %+v (max logical duration %s)", assignment, maxDuration)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status == "STOPPING" || f.test.Status == "STOPPING" {
		t.Fatalf("durable run = %+v, %v", record, err)
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	if err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-d" || checkpoint.Shards["gc"][0].Target != "gen-c" ||
		checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner {
		t.Fatalf("restarted checkpoint = %+v, %v", checkpoint, err)
	}
	if count := len(checkpoint.Failovers); count == 0 || checkpoint.Failovers[count-1].RestartedVUsers != 5 || !slices.Equal(checkpoint.Failovers[count-1].RestartedGroups, []string{"ga"}) {
		t.Fatalf("failover record does not say the share was restarted: %+v", checkpoint.Failovers)
	}
	if note := controllerFailoverComparisonWarning(&RunContinuityStatus{Failovers: checkpoint.Failovers}); !strings.Contains(note, "5 VUsers of ga lost their evacuation replacement and were restarted fresh") {
		t.Fatalf("comparison warning = %q", note)
	}
	if f.test.remoteClientsByTarget["gen-d"] == nil || f.test.remoteClientsByTarget["gen-b"] != nil {
		t.Fatal("runtime topology does not run the share on the new replacement")
	}
	// The retired source is never offered the share again.
	if claims, _ := f.sourceGen.snapshot(); len(claims) != 0 {
		t.Fatalf("the fenced source was claimed while placing the share: %+v", claims)
	}
}

func (f *evacuationRollbackFixture) stopRestarted(t *testing.T, generation uint64) {
	t.Helper()
	client := f.test.remoteClientsByTarget["gen-d"]
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token := f.test.remoteRunSupervisionToken
	if _, err := stopGeneratorTestWithToken(ctx, client, f.test.ID, false, generation, token[:]); err != nil {
		t.Logf("stop after test: %v", err)
	}
	f.tr.mu.RLock()
	cancelRun := f.test.cancel
	f.tr.mu.RUnlock()
	if cancelRun != nil {
		cancelRun()
	}
}

func TestEvacuationRestartFromSourceFencedJournal(t *testing.T) {
	for _, c := range []struct {
		name string
		opts restartOptions
	}{
		{name: "running"},
		{name: "paused", opts: restartOptions{rollForwardOptions: rollForwardOptions{paused: true}}},
		{name: "activated standby", opts: restartOptions{rollForwardOptions: rollForwardOptions{standby: true}}},
		{name: "no capacity ledger", opts: restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, spare := newEvacuationRestartFixture(t, c.opts)
			// The staged replacement was drained by the successor's claim.
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
			owner, generation := f.tr.leadership.executionLeaseTerm()
			status := f.test.Status
			if err := f.tr.reconcileStoredActiveTest(f.test); err != nil || f.test.Status != status {
				t.Fatalf("restart candidate reconciled to %s: %v", f.test.Status, err)
			}
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRestartIntent(t, generation)
			claims, capacity := f.spareGen.snapshot()
			if len(claims) != 1 || len(claims[0]) != 1 || claims[0][0].PriorGeneration != f.oldGen {
				t.Fatalf("old replacement claims = %+v", claims)
			}
			if stops := f.spareGen.stopGenerations(); len(stops) != 0 {
				t.Fatalf("old replacement that runs nothing was stopped: %v", stops)
			}
			if !c.opts.noCapacity {
				// The moved share's reservation is confirmed at the new epoch,
				// committed and transferred.
				for _, request := range capacity {
					if request.Release || request.ExecutionGeneration != generation || request.ReservationId != f.claim.ReservationID {
						t.Fatalf("old replacement capacity request = %+v", request)
					}
				}
				if row := f.ledger(t)["gen-b"]; row.State != "committed" || row.Generation != generation {
					t.Fatalf("moved share reservation = %+v", row)
				}
			}
			ok, resume := f.restartPhaseB(t)
			if !ok {
				record, _ := f.store.GetTest(f.test.ID)
				t.Fatalf("restart failed: %+v", record)
			}
			defer f.stopRestarted(t, generation)
			f.assertRestarted(t, spare, resume, owner, generation, c.opts.paused, rollForwardStagedElapsed+rollForwardStagedRemaining)
			checkpoint, _, _ := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint)
			if slices.Contains(checkpoint.ArchiveTargets, "gen-b") {
				t.Fatalf("an old replacement that never committed kept an archive: %v", checkpoint.ArchiveTargets)
			}
			if c.opts.noCapacity {
				return
			}
			ledger := f.ledger(t)
			for target, want := range map[string]struct {
				state      string
				generation uint64
			}{"gen-a": {"releasing", f.oldGen}, "gen-b": {"releasing", generation}, "gen-c": {"committed", generation}, "gen-d": {"committed", generation}} {
				if row := ledger[target]; row.State != want.state || row.Generation != want.generation {
					t.Fatalf("%s reservation = %s at %d, want %s at %d", target, row.State, row.Generation, want.state, want.generation)
				}
			}
			if len(ledger["gen-d"].GroupPeaks) == 0 || ledger["gen-d"].ReservationID != f.claim.ReservationID {
				t.Fatalf("new replacement reservation = %+v", ledger["gen-d"])
			}
		})
	}
}

func encodedJournal(t *testing.T, raw string) runEvacuationCheckpoint {
	t.Helper()
	var guard runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(raw), &guard); err != nil {
		t.Fatal(err)
	}
	return guard
}

// stillStoppingClient answers an epoch claim as a generator whose run is still
// stopping: ALREADY_STOPPED without a terminal state.
type stillStoppingClient struct {
	*rollbackGeneratorClient
}

func (c *stillStoppingClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, owner string, ttl time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	result, err := c.rollbackGeneratorClient.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, adoptions)
	for i := range result.Adoptions {
		result.Adoptions[i].Outcome, result.Adoptions[i].TerminalState = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, ""
	}
	return result, err
}

func TestEvacuationRestartAfterAFailedRollForward(t *testing.T) {
	for _, c := range []struct {
		name      string
		outcome   pb.RunAdoptionOutcome
		committed bool
		standby   bool
	}{
		// The replacement's generator restarted and lost the run.
		{name: "unknown test", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST},
		{name: "unknown test with committed capacity", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST, committed: true},
		// The replacement never committed and lost its staged work to the claim.
		{name: "staged work drained", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, standby: true},
		// The lost leader's uncertain path stopped the replacement.
		{name: "stopped by a controller", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting, committedCapacity: c.committed, standby: c.standby}})
			f.spareGen.outcome = c.outcome
			f.spareGen.cause = pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP
			owner, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRestartIntent(t, generation)
			guard, _ := f.durableGuard(t)
			if guard.CommittingAt.IsZero() || guard.RollForwardCheckpoint != nil || guard.RestartTargetDuration != rollForwardCommitTarget {
				t.Fatalf("restart intent after the roll-forward = %+v", guard)
			}
			if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
				t.Fatalf("a replacement that runs nothing was shortened: %+v", adjustments)
			}
			if claims, _ := f.spareGen.snapshot(); len(claims) != 1 || len(claims[0]) != 1 {
				t.Fatalf("old replacement claims = %+v", claims)
			}
			ok, resume := f.restartPhaseB(t)
			if !ok {
				record, _ := f.store.GetTest(f.test.ID)
				t.Fatalf("restart failed: %+v", record)
			}
			defer f.stopRestarted(t, generation)
			f.assertRestarted(t, spare, resume, owner, generation, false, rollForwardCommitTarget)
			// CommitWork may have reached the old replacement, so it keeps an
			// archive, as the fenced source does.
			checkpoint, _, _ := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint)
			if !slices.Contains(checkpoint.ArchiveTargets, "gen-b") || !slices.Contains(checkpoint.ArchiveTargets, "gen-a") || f.test.remoteArchiveClientsByTarget["gen-b"] == nil {
				t.Fatalf("archive targets = %v", checkpoint.ArchiveTargets)
			}
			ledger := f.ledger(t)
			if row := ledger["gen-b"]; row.State != "releasing" || row.Generation != generation {
				t.Fatalf("old replacement reservation = %+v", row)
			}
			if row := ledger["gen-d"]; row.State != "committed" || row.Generation != generation {
				t.Fatalf("new replacement reservation = %+v", row)
			}
		})
	}
}

// assertRestartHeld checks a refused restart: the run is held STOPPING under
// its journal with the reason, eligible for attestation, the survivor (and an
// adopted old replacement) force-stopped at generation with the rotated token,
// and no same-term recovery can fence it.
func (f *evacuationRollbackFixture) assertRestartHeld(t *testing.T, generation uint64, problem, phase string, survivorAdopted, replacementAdopted bool) {
	t.Helper()
	guard, _ := f.durableGuard(t)
	if guard.Version != runEvacuationRestartCheckpointVersion || guard.Phase != phase {
		t.Fatalf("journal = version %d phase %q, want %q", guard.Version, guard.Phase, phase)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) || !strings.Contains(record.Error, problem) {
		t.Fatalf("durable run = %q %q, %v; want reason %q", record.Status, record.Error, err, problem)
	}
	f.tr.mu.RLock()
	eligible := interruptedEvacuationAwaitingProofLocked(f.test)
	resumed := f.test.adoptedResume != nil
	f.tr.mu.RUnlock()
	if !eligible || resumed {
		t.Fatalf("held restart eligible for attestation = %v, resume pending = %v", eligible, resumed)
	}
	for name, c := range map[string]struct {
		gen     *rollbackGeneratorClient
		adopted bool
	}{"survivor": {f.survivorGen, survivorAdopted}, "old replacement": {f.spareGen, replacementAdopted}} {
		stops := 0
		for _, stopped := range c.gen.stopGenerations() {
			if stopped == generation {
				stops++
			}
		}
		if c.adopted && (stops != 1 || !c.gen.stoppedWithClaimedToken()) {
			t.Fatalf("%s stops = %v, want one forced stop at %d with the rotated token", name, c.gen.stopGenerations(), generation)
		}
		if !c.adopted && stops != 0 {
			t.Fatalf("%s was stopped at %d: %v", name, generation, c.gen.stopGenerations())
		}
	}
	if stops := f.sourceGen.stopGenerations(); len(stops) != 0 {
		t.Fatalf("fenced source was stopped again: %v", stops)
	}
	if err := f.tr.recoverInterruptedRunEvacuation(f.test); err == nil {
		t.Fatal("the term that adopted the survivors fenced them itself")
	}
}

func TestEvacuationRestartRefusalsHoldTheRun(t *testing.T) {
	tamperDuration := func(t *testing.T, f *evacuationRollbackFixture) {
		guard := encodedJournal(t, f.guard)
		guard.StagedSteadyElapsed, guard.StagedRemaining = 0, 0
		raw := encodeEvacuationGuard(t, guard)
		if err := f.tr.swapRunContinuityCheckpoint(f.test, f.guard, raw, f.oldOwner); err != nil {
			t.Fatal(err)
		}
		f.guard = raw
	}
	t.Run("before any claim", func(t *testing.T) {
		cases := map[string]struct {
			opts   restartOptions
			before func(*testing.T, *evacuationRollbackFixture)
			mutate func(*testing.T, *evacuationRollbackFixture)
			want   string
		}{
			"setting off":   {opts: restartOptions{settingOff: true}, want: "the missing-replacement restart is not enabled"},
			"iterations":    {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.TerminationMode = "iterations" }, want: "iteration work cannot be moved"},
			"stopping":      {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.Status = "STOPPING" }, want: "the run is STOPPING"},
			"no run start":  {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.StartedAt = nil }, want: "the remaining active duration cannot be proven"},
			"no share time": {before: tamperDuration, want: "the journal records no duration for the moved share"},
			"rendezvous": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) {
				f.test.Groups[0].script = &dsl.Script{Name: "meet", Protocol: "http", Actions: []dsl.Action{{Name: "meet", Type: "rendezvous", Params: map[string]any{
					"scope": "distributed_group", "key": "meet", "timeout_ms": 1000, "on_timeout": "fail",
				}}}}
			}, want: "distributed rendezvous cannot be moved"},
			"stateful data": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) {
				f.test.Groups[0].script = &dsl.Script{Name: "accounts", Protocol: "http", DataSources: []dsl.DataSource{{Name: "accounts", Type: "csv", InlineData: "id\n1\n", Strategy: "sequential"}}}
			}, want: "cursor-stateful sequential selection"},
			"stateful protocol": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.Groups[0].Protocol = "browser" }, want: "are stateful"},
			"fail policy": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) {
				f.test.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: "fail"}
			}, want: "the run does not continue on controller failover"},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				var hooks []func(*evacuationRollbackFixture)
				if c.before != nil {
					hooks = append(hooks, func(f *evacuationRollbackFixture) { c.before(t, f) })
				}
				f, _ := newEvacuationRestartFixture(t, c.opts, hooks...)
				if c.mutate != nil {
					c.mutate(t, f)
				}
				if f.tr.isRunAdoptionCandidate(f.test) {
					t.Fatal("refused journal became an adoption candidate")
				}
				if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
					t.Fatal(err)
				}
				record, err := f.store.GetTest(f.test.ID)
				if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) || record.ContinuityCheckpoint != f.guard {
					t.Fatalf("journal reconciled to %+v, %v", record, err)
				}
				if !strings.Contains(record.Error, "the replacement never committed") || !strings.Contains(record.Error, "the moved share was not restarted: ") || !strings.Contains(record.Error, c.want) {
					t.Fatalf("held reason = %q, want %q", record.Error, c.want)
				}
				if err := f.tr.adoptOrphanedRuns(); err != nil {
					t.Fatal(err)
				}
				for name, gen := range map[string]*rollbackGeneratorClient{"source": f.sourceGen, "old replacement": f.spareGen, "survivor": f.survivorGen} {
					if claims, _ := gen.snapshot(); len(claims) != 0 {
						t.Fatalf("a refused restart reached the %s: %+v", name, claims)
					}
				}
			})
		}
	})
	t.Run("at the epoch claim", func(t *testing.T) {
		cases := map[string]struct {
			prepare            func(*evacuationRollbackFixture)
			want               string
			replacementAdopted bool
		}{
			"old replacement unreachable": {prepare: func(f *evacuationRollbackFixture) { f.spareDown = true }, want: "old replacement gen-b is unreachable, which does not prove it stopped"},
			"old replacement still runs": {prepare: func(f *evacuationRollbackFixture) {
				f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED
			}, want: "old replacement gen-b still runs its assignment", replacementAdopted: true},
			"old replacement still stopping": {prepare: func(f *evacuationRollbackFixture) {
				f.replacement = &stillStoppingClient{f.spareGen}
			}, want: "old replacement gen-b does not prove it stopped: its run is still stopping"},
			"survivor stopped by a controller": {prepare: func(f *evacuationRollbackFixture) {
				f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
				f.survivorGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
				f.survivorGen.cause = pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP
			}, want: "participant gen-c reports a completed run the journal never recorded"},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				f, spare := newEvacuationRestartFixture(t, restartOptions{})
				f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
				c.prepare(f)
				_, generation := f.tr.leadership.executionLeaseTerm()
				if err := f.tr.adoptOrphanedRuns(); err != nil {
					t.Fatal(err)
				}
				f.assertRestartHeld(t, generation, c.want, runEvacuationPhaseReplacementRestarting, f.survivorGen.outcome == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, c.replacementAdopted)
				if f.test.executionCtx != nil || f.test.cancel != nil {
					t.Fatal("a restart refused at the claim installed a runtime")
				}
				if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
					t.Fatalf("a refused restart assigned the share: %+v", assignments)
				}
				// The moved share keeps its journaled reservation for the fence.
				if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
					t.Fatalf("old replacement reservation changed: %+v", row)
				}
			})
		}
	})
	t.Run("when the restart runs", func(t *testing.T) {
		cases := map[string]struct {
			opts   restartOptions
			mutate func(*evacuationRollbackFixture, *restartGenerator)
			want   string
			phase  string
		}{
			"no spare capacity":  {opts: restartOptions{restartVUsers: 2}, want: "no healthy generator with sufficient capacity", phase: runEvacuationPhaseReplacementRestarting},
			"no spare generator": {opts: restartOptions{noRestartTarget: true}, want: "no healthy generator with sufficient capacity", phase: runEvacuationPhaseReplacementRestarting},
			"share time ran out": {mutate: func(f *evacuationRollbackFixture, _ *restartGenerator) {
				started := time.Now().Add(-2 * time.Minute)
				f.test.StartedAt = &started
			}, want: "steady-state duration is exhausted", phase: runEvacuationPhaseReplacementRestarting},
			// The staged replacement is stopped again and its capacity returned;
			// the restart's own journal stays for the hold.
			"new replacement refuses supervision": {mutate: func(_ *evacuationRollbackFixture, spare *restartGenerator) {
				spare.recorder.refuseSupervision = true
			}, want: "claim replacement run supervision", phase: runEvacuationPhaseStaged},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				f, spare := newEvacuationRestartFixture(t, c.opts)
				f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
				_, generation := f.tr.leadership.executionLeaseTerm()
				if err := f.tr.adoptOrphanedRuns(); err != nil {
					t.Fatal(err)
				}
				f.assertRestartIntent(t, generation)
				if c.mutate != nil {
					c.mutate(f, spare)
				}
				// The hold is durable before any survivor is stopped.
				var statusAtStop string
				f.survivorGen.beforeStop = func() {
					if record, err := f.store.GetTest(f.test.ID); err == nil && statusAtStop == "" {
						statusAtStop = record.Status
					}
				}
				if ok, _ := f.restartPhaseB(t); ok {
					t.Fatal("a refused restart reported success")
				}
				f.assertRestartHeld(t, generation, c.want, c.phase, true, false)
				if statusAtStop != "STOPPING" {
					t.Fatalf("durable status at the first survivor stop = %q", statusAtStop)
				}
				// Only a newer term fences what this term adopted and staged.
				f.tr.resumeInterruptedRunEvacuations()
				f.tr.terminalRetryMu.Lock()
				_, queued := f.tr.terminalRetrying["evacuation-guard:"+f.test.ID]
				f.tr.terminalRetryMu.Unlock()
				if queued {
					t.Fatal("same-term recovery was queued for a failed restart")
				}
				if f.test.executionCtx == nil || f.test.executionCtx.Err() == nil {
					t.Fatal("the held run's runtime still renews its generators")
				}
				ledger := f.ledger(t)
				if row := ledger["gen-b"]; row.State != "committed" || row.Generation != generation {
					t.Fatalf("moved share reservation = %+v", row)
				}
				if row, present := ledger["gen-d"]; present && row.State != "released" {
					t.Fatalf("refused restart kept the new replacement's reservation: %+v", row)
				}
				if c.phase == runEvacuationPhaseStaged {
					// The staging was rolled back: the staged assignment was
					// accepted, and a commit of it is now refused.
					if assigned, _, _ := spare.recorder.calls(); len(assigned) != 1 || !assigned[0].accepted || assigned[0].generation != generation {
						t.Fatalf("restart assignments = %+v", assigned)
					}
					token := f.test.remoteRunSupervisionToken
					owner, _ := f.tr.leadership.executionLeaseTerm()
					commitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					// A still-staged assignment would take this supervision and commit.
					claim, claimErr := spare.client.ClaimRunSupervision(commitCtx, generatorrpc.RunSupervisionClaim{TestID: f.test.ID, ExecutionGeneration: generation, Token: token[:], RequestedTTL: time.Minute, ExecutionLeaseOwnerID: owner})
					commit, commitErr := spare.client.CommitWorkSupervisedAt(commitCtx, f.test.ID, generation, time.Now().Add(500*time.Millisecond), token[:])
					cancel()
					for name, call := range map[string]restartCall{
						"supervision claim": {accepted: claimErr == nil && claim.Accepted, err: claimErr, message: claim.ErrorMessage},
						"commit":            {accepted: commitErr == nil && commit.Accepted, err: commitErr, message: commit.ErrorMessage},
					} {
						if !call.refusedByGenerator() {
							t.Fatalf("the rolled-back staged replacement's answer to a %s = %+v, want a refusal", name, call)
						}
					}
					guard, _ := f.durableGuard(t)
					if guard.Source != "gen-b" || guard.SourceAbsentAt.IsZero() || guard.Generation != generation || guard.NextShards["ga"][0].Target != "gen-d" {
						t.Fatalf("restart journal = %+v", guard)
					}
				}
			})
		}
	})
}

// takeOver hands the run to a new controller process: a separate runner on the
// same store and key wrapper, with a newer lease term and fresh transports.
// The previous runner is left as it was, so it can return late.
func (f *evacuationRollbackFixture) takeOver(t *testing.T, name string) (*TestRunner, string, uint64) {
	t.Helper()
	previous := f.tr
	// A replica that does not lead yet loads the run without reconciling it.
	tr := NewTestRunner(10, NewHub(), f.store, WithRunContinuityKeyWrapper(previous.continuityKeyWrapper), WithExecutionLeadership(newExecutionLeadership(f.store, name+"-boot")))
	t.Cleanup(tr.beginShutdown)
	tr.evacuationRollbackRecovery, tr.evacuationRollForwardRecovery, tr.evacuationReplacementRestart = previous.evacuationRollbackRecovery, previous.evacuationRollForwardRecovery, previous.evacuationReplacementRestart
	settings := previous.GetSystemSettings()
	_ = tr.GetSystemSettings() // the first durable refresh, before the override
	tr.mu.Lock()
	tr.settings = settings
	tr.mu.Unlock()
	tr.leadership = previous.leadership
	t.Cleanup(func() { stopRunnerRuntimes(t, previous) })
	f.tr = tr
	owner, generation := f.handOver(t, name)
	f.superviseRuns()
	// The new controller knows the run only from the store: its status, clock
	// and pause state. Groups keep their compiled scripts, which a reload
	// restores from the stored scripts.
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := managedTestFromRecord(record)
	previous.mu.RLock()
	restored.Assignment, restored.Groups = f.test.Assignment, append([]TestGroup(nil), f.test.Groups...)
	previous.mu.RUnlock()
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	tr.tests[f.test.ID] = restored
	f.test = restored
	return previous, owner, generation
}

// restartBlock parks a controller inside one step of its restart, as if it
// had been lost there, until the test lets it return late.
type restartBlock struct {
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newRestartBlock() *restartBlock {
	return &restartBlock{reached: make(chan struct{}), release: make(chan struct{})}
}

func (b *restartBlock) park() {
	first := false
	b.once.Do(func() { first = true; close(b.reached) })
	if first {
		<-b.release
	}
}

func (b *restartBlock) awaitReached(t *testing.T) {
	t.Helper()
	select {
	case <-b.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the first controller never reached the interrupted step")
	}
}

func TestEvacuationRestartAcrossAnotherLeaderChange(t *testing.T) {
	for _, boundary := range []string{
		"after the restart intent",
		"after the survivors were adopted",
		"after the restart journal began",
		"after the new replacement was staged",
		"after the restart fenced its source",
		"before the new replacement committed",
		"after the new replacement committed",
	} {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			paused := boundary == "after the restart fenced its source"
			var pausedFor time.Duration
			if paused {
				pausedFor = 30 * time.Second
			}
			// At one boundary the survivor is an in-process generator, so the
			// first successor's late stop meets a real generation fence.
			var survivor *restartGenerator
			var hooks []func(*evacuationRollbackFixture)
			if boundary == "before the new replacement committed" {
				t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
				survivor = newRestartGenerator(t, "gen-c", 10, true)
				hooks = append(hooks, func(f *evacuationRollbackFixture) { f.runSurvivorShareOn(t, survivor) })
			}
			f, first := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{paused: paused}, pausedFor: pausedFor}, hooks...)
			second := newRestartGenerator(t, "gen-e", 10, true)
			f.extra["gen-e"] = second.dial
			f.handOver(t, "restart-first-successor")
			f.superviseRuns()
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
			firstOwner, firstGeneration := f.tr.leadership.executionLeaseTerm()
			block := newRestartBlock()
			var late chan struct{}
			switch boundary {
			case "after the restart intent":
				plan, err := f.tr.evacuationRestartGuard(f.test, f.guard)
				if err != nil {
					t.Fatal(err)
				}
				planned, _, err := f.tr.planRunAdoptionIntent(ctx, f.test.ID, runEvacuationRestartCheckpoint(plan.guard, plan.missing), firstGeneration, firstOwner)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := f.tr.persistRunEvacuationRestartIntent(f.test, f.guard, plan.guard, planned, firstGeneration, firstOwner, plan.target); err != nil {
					t.Fatal(err)
				}
			default:
				if err := f.tr.adoptOrphanedRuns(); err != nil {
					t.Fatal(err)
				}
				f.assertRestartIntent(t, firstGeneration)
				if boundary == "after the survivors were adopted" {
					break
				}
				switch boundary {
				case "after the restart journal began":
					first.recorder.beforeAssign = block.park
				case "after the new replacement was staged", "after the restart fenced its source":
					first.recorder.beforeSupervision = block.park
				case "before the new replacement committed":
					first.recorder.beforeCommit = block.park
				case "after the new replacement committed":
					first.recorder.afterCommit = block.park
				}
				late = make(chan struct{})
				firstTest := f.test
				go func() {
					defer close(late)
					f.tr.mu.Lock()
					resume := firstTest.adoptedResume
					firstTest.adoptedResume = nil
					executionCtx, token := firstTest.executionCtx, firstTest.remoteRunSupervisionToken
					f.tr.mu.Unlock()
					f.tr.restartEvacuationReplacement(f.tr.execution.(*distributedExecutionBackend), firstTest, resume, executionCtx, token)
				}()
				block.awaitReached(t)
				if boundary == "after the restart fenced its source" {
					guard, raw := f.durableGuard(t)
					if guard.Phase != runEvacuationPhaseStaged {
						t.Fatalf("restart journal phase = %q", guard.Phase)
					}
					if _, err := f.tr.markRunEvacuationSourceFenced(ctx, f.test, raw, firstGeneration, firstOwner); err != nil {
						t.Fatal(err)
					}
				}
			}
			journal, _ := f.durableGuard(t)
			claimsBefore, _ := f.spareGen.snapshot()
			// The first successor's rotated token, which its late calls carry.
			f.tr.mu.RLock()
			firstToken := f.test.remoteRunSupervisionToken
			f.tr.mu.RUnlock()
			_, owner, generation := f.takeOver(t, "restart-second-successor")
			released := false
			// returnLate lets the first successor go on from where it was lost,
			// with the lease and the generators already taken by the second.
			returnLate := func() {
				if !released {
					released = true
					close(block.release)
					if late != nil {
						<-late
					}
				}
			}
			defer returnLate()
			if !f.tr.isRunAdoptionCandidate(f.test) {
				_, restartErr := f.tr.evacuationRestartGuard(f.test, f.test.continuityCheckpoint)
				_, _, rollForward := f.tr.evacuationRollForwardGuard(f.test, f.test.continuityCheckpoint)
				t.Fatalf("a %s journal at phase %q is no adoption candidate: restart %v, roll-forward %v", boundary, journal.Phase, restartErr, rollForward)
			}
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			claims, _ := f.spareGen.snapshot()
			switch last := claims[len(claims)-1]; {
			case journal.SourceAbsentAt.IsZero():
				// The second successor proves the first one's rotation.
				if len(last) != 1 || len(last[0].PriorProofs) != 1 || last[0].PriorProofs[0].Generation != firstGeneration {
					t.Fatalf("second claim of the old replacement = %+v", last)
				}
			case journal.SourceFencedAt.IsZero():
				// A restart's absent source is fenced again, without the run.
				if len(last) != 0 || len(claims) != len(claimsBefore)+1 {
					t.Fatalf("second claim of the restart's absent source = %+v", last)
				}
			default:
				// A restart's source that its journal fenced is not claimed again.
				if len(claims) != len(claimsBefore) {
					t.Fatalf("the restart's fenced source was claimed again: %+v", claims[len(claimsBefore):])
				}
			}
			if boundary == "after the new replacement committed" {
				// The new replacement crossed its start barrier: the second
				// successor adopts it and rolls forward; nothing restarts again.
				resume := f.test.adoptedResume
				if resume == nil || resume.finishStatus != "" || resume.restart != nil || resume.adopted["gen-d"] == nil {
					t.Fatalf("committed restart not rolled forward: %+v", resume)
				}
				defer f.stopRestartedOn(t, first, generation)
				waitForActiveVUsers(t, first.service, 5)
				_, before := f.durableGuard(t)
				ledgerBefore := f.ledger(t)
				returnLate()
				if _, after := f.durableGuard(t); after != before {
					t.Fatal("the first successor's late return changed the checkpoint")
				}
				if row := f.ledger(t)["gen-d"]; row.State != ledgerBefore["gen-d"].State || row.Generation != generation {
					t.Fatalf("the late return changed the adopted replacement's reservation: %+v", row)
				}
				if assignments, _ := second.recorder.snapshot(); len(assignments) != 0 {
					t.Fatalf("a committed restart was started again: %+v", assignments)
				}
				checkpoint, _, _ := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint)
				if count := len(checkpoint.Failovers); count < 2 || checkpoint.Failovers[count-2].RestartedVUsers != 5 {
					t.Fatalf("rolled-forward restart lost its failover record: %+v", checkpoint.Failovers)
				}
				// Its stop at the older generation did not reach the adopted run.
				if got := first.service.ActiveVUsers(); got != 5 {
					t.Fatalf("adopted replacement active VUsers after the late return = %d, want 5", got)
				}
				return
			}
			ok, resume := f.restartPhaseB(t)
			if !ok {
				record, _ := f.store.GetTest(f.test.ID)
				t.Fatalf("second successor's restart failed: %+v", record)
			}
			defer f.stopRestarted(t, generation)
			replacement, replaced, want := second, first, "gen-e"
			if journal.Phase == runEvacuationPhaseReplacementRestarting {
				// Nothing was staged: the share restarts on the first spare.
				replacement, replaced, want = first, second, "gen-d"
			}
			if resume.adopted[want] == nil || !slices.Equal(resume.restarted, []string{want}) {
				t.Fatalf("restarted on %v, want %s", resume.restarted, want)
			}
			assignments, commits := replacement.recorder.snapshot()
			restarted := assignments[len(assignments)-1]
			if len(commits) != 1 || commits[0] != generation || restarted.ExecutionGeneration != generation || restarted.StartPaused != paused ||
				restarted.DurationOffset+restarted.TargetDuration > rollForwardStagedElapsed+rollForwardStagedRemaining {
				t.Fatalf("second successor's restart assignments %+v commits %v", assignments, commits)
			}
			// The durable pause is not active time: the share resumes at the
			// steady offset the run reached, not at its wall-clock age.
			if paused && (restarted.DurationOffset < rollForwardStagedElapsed-5*time.Second || restarted.DurationOffset > rollForwardStagedElapsed+5*time.Second) {
				t.Fatalf("restart offset %s after a %s pause, want about %s", restarted.DurationOffset, pausedFor, rollForwardStagedElapsed)
			}
			// Never a duplicate start: what the first successor staged was
			// drained by the second's claim before the share moved on, so its
			// supervision and a commit of it under the first successor's term
			// are refused by the generator. After the restart journal began the
			// replacement never held the assignment, so a commit there would
			// prove nothing; the late AssignWork below covers that boundary.
			if journal.Phase != runEvacuationPhaseReplacementRestarting && journal.Phase != runEvacuationPhasePrepared {
				commitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				claim, claimErr := replaced.client.ClaimRunSupervision(commitCtx, generatorrpc.RunSupervisionClaim{TestID: f.test.ID, ExecutionGeneration: firstGeneration, Token: firstToken[:], RequestedTTL: time.Minute, ExecutionLeaseOwnerID: firstOwner})
				commit, commitErr := replaced.client.CommitWorkSupervisedAt(commitCtx, f.test.ID, firstGeneration, time.Now().Add(500*time.Millisecond), firstToken[:])
				cancel()
				for name, call := range map[string]restartCall{
					"supervision claim": {accepted: claimErr == nil && claim.Accepted, err: claimErr, message: claim.ErrorMessage},
					"commit":            {accepted: commitErr == nil && commit.Accepted, err: commitErr, message: commit.ErrorMessage},
				} {
					if !call.refusedByGenerator() {
						t.Fatalf("the drained replacement's answer to the first successor's %s = %+v, want a refusal", name, call)
					}
				}
			}
			checkpoint, present, err := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint)
			if err != nil || !present || checkpoint.Shards["ga"][0].Target != want || checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner {
				t.Fatalf("checkpoint after the second restart = %+v, %v", checkpoint, err)
			}
			if row := f.ledger(t)[want]; row.State != "committed" || row.Generation != generation {
				t.Fatalf("restarted share reservation = %+v", row)
			}
			// The first successor returns late: its journal writes, capacity
			// changes and generator commands of the older term are refused.
			_, before := f.durableGuard(t)
			ledgerBefore := f.ledger(t)
			assignedBefore, supervisedBefore, committedBefore := replaced.recorder.calls()
			survivorStopsBefore := len(f.survivorGen.stopGenerations())
			returnLate()
			// Every stop the first successor sent its survivor on the way out
			// carried its own, older generation, which the survivor's adoption
			// by the second successor fenced.
			for _, stopped := range f.survivorGen.stopGenerations()[survivorStopsBefore:] {
				if stopped != firstGeneration {
					t.Fatalf("a late stop reached the survivor at generation %d, want only %d", stopped, firstGeneration)
				}
			}
			if survivor != nil {
				// The real survivor refused that stop and still runs its share.
				if got := survivor.service.ActiveVUsers(); got != 3 {
					t.Fatalf("survivor active VUsers after the late return = %d, want 3", got)
				}
			} else if boundary != "after the restart intent" && boundary != "after the survivors were adopted" && len(f.survivorGen.stopGenerations()) == survivorStopsBefore {
				t.Fatal("the first successor sent its survivor no late stop")
			}
			if _, after := f.durableGuard(t); after != before {
				t.Fatal("the first successor's late return changed the checkpoint")
			}
			if ledgerAfter := f.ledger(t); ledgerAfter[want].State != ledgerBefore[want].State || ledgerAfter[want].Generation != ledgerBefore[want].Generation {
				t.Fatalf("the late return changed the share's reservation: %+v", ledgerAfter[want])
			}
			if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status == "STOPPING" || f.test.Status == "STOPPING" {
				t.Fatalf("the late return stopped the run: %+v, %v", record, err)
			}
			// A commit the first successor sends when it returns is refused: the
			// second's claim fenced its generation on that generator.
			_, replacedCommits := replaced.recorder.snapshot()
			if boundary == "before the new replacement committed" && !slices.Contains(replacedCommits, firstGeneration) {
				t.Fatalf("the first successor did not try its commit when it returned: %v", replacedCommits)
			}
			if accepted := replaced.recorder.acceptedCommits(); len(accepted) != 0 {
				t.Fatalf("the abandoned replacement accepted a commit at generations %v", accepted)
			}
			// The call each first successor was parked in is sent when it
			// returns, and the generator refuses every call it makes at its
			// older generation.
			assigned, supervised, committed := replaced.recorder.calls()
			var lateCalls []restartCall
			switch boundary {
			case "after the restart journal began":
				lateCalls = assigned[len(assignedBefore):]
			case "after the new replacement was staged", "after the restart fenced its source":
				lateCalls = supervised[len(supervisedBefore):]
			case "before the new replacement committed":
				lateCalls = committed[len(committedBefore):]
			}
			if journal.Phase != runEvacuationPhaseReplacementRestarting {
				refused := 0
				for _, call := range lateCalls {
					if call.generation != firstGeneration {
						continue
					}
					if !call.refusedByGenerator() || !lateCallRefusalNamesGeneration(call) {
						t.Fatalf("the generator's answer to the first successor's late call = %+v, want a refusal of its generation", call)
					}
					refused++
				}
				if refused == 0 {
					t.Fatalf("the first successor sent no late call at %d: %+v", firstGeneration, lateCalls)
				}
			}
			waitForActiveVUsers(t, replacement.service, 5)
		})
	}
}

func (f *evacuationRollbackFixture) stopRestartedOn(t *testing.T, gen *restartGenerator, generation uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token := f.test.remoteRunSupervisionToken
	if _, err := stopGeneratorTestWithToken(ctx, gen.client, f.test.ID, false, generation, token[:]); err != nil {
		t.Logf("stop after test: %v", err)
	}
}

// TestEvacuationRestartAgainstRealGenerator drives the restart against real
// in-process generators for both the old and the new replacement: the old one
// is drained by the successor's claim, or answers UNKNOWN_TEST after its
// process restarted, and the share then runs on the new one under the
// successor's term, while the predecessor's generation and token are refused
// by both.
func TestEvacuationRestartAgainstRealGenerator(t *testing.T) {
	t.Run("staged replacement is drained", func(t *testing.T) { runRealGeneratorRestart(t, "staged") })
	t.Run("restarted replacement process lost the run", func(t *testing.T) { runRealGeneratorRestart(t, "process restarted") })
}

func runRealGeneratorRestart(t *testing.T, mode string) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	ctx := context.Background()
	phase := runEvacuationPhaseSourceFenced
	if mode == "process restarted" {
		phase = runEvacuationPhaseReplacementCommitting
	}
	oldClient, oldService := newTestGeneratorRPCClient(t, "gen-b", 10, generatorrpc.WithExecution(), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	var script []byte
	var scriptHash string
	f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: phase, noCapacity: true}}, func(f *evacuationRollbackFixture) {
		// The lost leader staged the replacement, and in replacement_committing
		// committed it, with a start barrier of 500 ms.
		if _, err := oldClient.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("lost leader epoch claim: %v", err)
		}
		var err error
		script, scriptHash, _, err = generatorrpc.PrepareDistributedScript(f.test.Groups[0].script)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := oldClient.DistributeScript(ctx, generatorrpc.Script{ID: "restart-script", ExecutionGeneration: f.oldGen, Content: script, ContentHash: scriptHash}); err != nil {
			t.Fatalf("distribute script: %v", err)
		}
		assign, err := oldClient.AssignWork(ctx, generatorrpc.Assignment{
			TestID: f.test.ID, GroupName: "ga", ScriptID: "restart-script", ExpectedScriptHash: scriptHash,
			VUserCount: 5, ExecutionGeneration: f.oldGen, TargetDuration: rollForwardStagedRemaining, DurationOffset: rollForwardStagedElapsed,
			RunSupervisionToken: f.token, AdoptionGrace: 30 * time.Second,
		})
		if err != nil || !assign.Accepted {
			t.Fatalf("assign work = (%+v, %v)", assign, err)
		}
		f.replacement = oldClient
		if mode != "process restarted" {
			return
		}
		if claim, err := oldClient.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{TestID: f.test.ID, ExecutionGeneration: f.oldGen, Token: f.token, RequestedTTL: time.Minute, ExecutionLeaseOwnerID: f.oldOwner}); err != nil || !claim.Accepted {
			t.Fatalf("supervision claim = (%+v, %v)", claim, err)
		}
		if commit, err := oldClient.CommitWorkSupervisedAt(ctx, f.test.ID, f.oldGen, time.Now().Add(500*time.Millisecond), f.token); err != nil || !commit.Accepted {
			t.Fatalf("commit = (%+v, %v)", commit, err)
		}
		waitForActiveVUsers(t, oldService, 5)
		// The replacement's process restarted: its VUsers stopped with it, and
		// the new process knows nothing of the run.
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = oldService.Shutdown(shutdownCtx)
		waitForActiveVUsers(t, oldService, 0)
		fresh, _ := newTestGeneratorRPCClient(t, "gen-b", 10, generatorrpc.WithExecution(), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
		f.replacement = fresh
	})
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertRestartIntent(t, generation)
	if mode == "staged" {
		// The claim that refused the adoption drained the staged assignment.
		waitForActiveVUsers(t, oldService, 0)
		if commit, err := oldClient.CommitWorkSupervisedAt(ctx, f.test.ID, f.oldGen, time.Now().Add(500*time.Millisecond), f.token); err == nil && commit.Accepted {
			t.Fatal("the drained staged assignment still accepted the predecessor's commit")
		}
	}
	ok, resume := f.restartPhaseB(t)
	if !ok {
		record, _ := f.store.GetTest(f.test.ID)
		t.Fatalf("restart failed: %+v", record)
	}
	defer f.stopRestarted(t, generation)
	maxDuration := rollForwardStagedElapsed + rollForwardStagedRemaining
	if mode == "process restarted" {
		maxDuration = rollForwardCommitTarget
	}
	f.assertRestarted(t, spare, resume, owner, generation, false, maxDuration)
	waitForActiveVUsers(t, spare.service, 5)
	// The predecessor's generation and token control neither replacement.
	if result, err := spare.client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: f.oldGen, TargetDuration: time.Hour, RunSupervisionToken: f.token}); err == nil && result.Accepted {
		t.Fatal("the new replacement accepted a duration change from the predecessor")
	}
	if stop, err := stopGeneratorTestWithToken(ctx, spare.client, f.test.ID, false, f.oldGen, f.token); err == nil && stop.Acknowledged {
		t.Fatal("the new replacement accepted the predecessor's stop")
	}
	if got := spare.service.ActiveVUsers(); got != 5 {
		t.Fatalf("restarted share active VUsers after the predecessor's commands = %d, want 5", got)
	}
	if mode == "staged" {
		if got := oldService.ActiveVUsers(); got != 0 {
			t.Fatalf("old replacement active VUsers = %d, want 0", got)
		}
	}
}

// writeRestartIntent records the current term's restart intent for the
// fixture's journal without claiming any generator.
func (f *evacuationRollbackFixture) writeRestartIntent(t *testing.T) string {
	t.Helper()
	owner, generation := f.tr.leadership.executionLeaseTerm()
	plan, err := f.tr.evacuationRestartGuard(f.test, f.guard)
	if err != nil {
		t.Fatal(err)
	}
	planned, _, err := f.tr.planRunAdoptionIntent(context.Background(), f.test.ID, runEvacuationRestartCheckpoint(plan.guard, plan.missing), generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := f.tr.persistRunEvacuationRestartIntent(f.test, f.guard, plan.guard, planned, generation, owner, plan.target)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRestartJournalRejectsContradictions(t *testing.T) {
	f, _ := newEvacuationRestartFixture(t, restartOptions{})
	_, generation := f.tr.leadership.executionLeaseTerm()
	sourceFenced := encodedJournal(t, f.guard)
	for name, mutate := range map[string]func(*runEvacuationCheckpoint){
		"restart term on version 5":   func(g *runEvacuationCheckpoint) { g.RestartGeneration = generation },
		"absent source on version 5":  func(g *runEvacuationCheckpoint) { g.SourceAbsentAt = time.Now().UTC() },
		"share duration on version 5": func(g *runEvacuationCheckpoint) { g.RestartTargetDuration = time.Minute },
		"exclusions on version 5":     func(g *runEvacuationCheckpoint) { g.RestartExcluded = []string{"gen-a"} },
		"restart term at source_fenced": func(g *runEvacuationCheckpoint) {
			g.Version, g.RestartGeneration = runEvacuationRestartCheckpointVersion, generation
		},
	} {
		t.Run("source_fenced/"+name, func(t *testing.T) {
			g := sourceFenced
			mutate(&g)
			encoded := encodeEvacuationGuard(t, g)
			if _, _, err := interruptedEvacuationTargets(encoded, f.test.ID); err == nil || !strings.Contains(err.Error(), "contradictory restart progress") {
				t.Fatalf("fail-closed recovery accepted it: %v", err)
			}
			if _, _, _, err := runEvacuationRestartPlan(encoded, f.test.ID); err == nil {
				t.Fatal("restart accepted it")
			}
		})
	}
	for name, c := range map[string]struct {
		mutate func(*runEvacuationCheckpoint)
		want   string
	}{
		"abandoned":             {func(g *runEvacuationCheckpoint) { g.AbandonedAt = time.Now().UTC() }, "abandoned"},
		"unarmed lineage":       {func(g *runEvacuationCheckpoint) { g.Prior.ArmedAt = time.Time{} }, "never armed"},
		"completed source":      {func(g *runEvacuationCheckpoint) { g.Prior.CompletedTargets = []string{"gen-a"} }, "already completed"},
		"completed replacement": {func(g *runEvacuationCheckpoint) { g.Prior.CompletedTargets = []string{"gen-b"} }, "replacement is recorded completed"},
		"changed survivor":      {func(g *runEvacuationCheckpoint) { g.NextShards["gc"][0].VUsers++ }, "changed surviving participant"},
		"invalid claim":         {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].State = "committed" }, "invalid replacement claim"},
		"no share duration":     {func(g *runEvacuationCheckpoint) { g.StagedRemaining = 0 }, "records no duration for the moved share"},
		"negative offset":       {func(g *runEvacuationCheckpoint) { g.StagedSteadyElapsed, g.StagedRemaining = -time.Second, 0 }, "records no duration"},
		"two replacements": {func(g *runEvacuationCheckpoint) {
			g.BeforeShards["gd"] = []GeneratorShardSummary{{Group: "gd", Target: "gen-a", VUsers: 1, ShardCount: 1}}
			g.Prior.Shards = g.BeforeShards
			g.NextShards["gd"] = []GeneratorShardSummary{{Group: "gd", Target: "gen-e", VUsers: 1, ShardCount: 1}}
			g.StagedTargets = []string{"gen-b", "gen-e"}
		}, "one replacement's share"},
	} {
		t.Run("plan/"+name, func(t *testing.T) {
			g := encodedJournal(t, f.guard)
			c.mutate(&g)
			if _, _, _, err := runEvacuationRestartPlan(encodeEvacuationGuard(t, g), f.test.ID); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("restart plan = %v, want %q", err, c.want)
			}
		})
	}
	raw := f.writeRestartIntent(t)
	if _, _, err := decodeRunContinuityCheckpoint(raw); err == nil {
		t.Fatal("a version 6 journal decoded as an adoption checkpoint")
	}
	if _, targets, err := interruptedEvacuationTargets(raw, f.test.ID); err != nil || !slices.Equal(targets, []string{"gen-b", "gen-c"}) {
		t.Fatalf("restart intent participants = %v, %v", targets, err)
	}
	if _, missing, target, err := runEvacuationRestartPlan(raw, f.test.ID); err != nil || missing != "gen-b" || target != rollForwardStagedElapsed+rollForwardStagedRemaining {
		t.Fatalf("restart intent plan = %s %s %v", missing, target, err)
	}
	if _, _, err := runEvacuationRollbackPlan(raw, f.test.ID); err == nil {
		t.Fatal("a restart intent was accepted for rollback")
	}
	if _, _, err := runEvacuationRollForwardPlan(raw, f.test.ID); err == nil {
		t.Fatal("a restart intent was accepted for roll-forward")
	}
	for name, mutate := range map[string]func(*runEvacuationCheckpoint){
		"version 5":           func(g *runEvacuationCheckpoint) { g.Version = runEvacuationRollForwardCheckpointVersion },
		"restart term":        func(g *runEvacuationCheckpoint) { g.RestartGeneration = g.Generation },
		"restart owner":       func(g *runEvacuationCheckpoint) { g.RestartOwnerID = "" },
		"restart start":       func(g *runEvacuationCheckpoint) { g.RestartStartedAt = time.Time{} },
		"share duration":      func(g *runEvacuationCheckpoint) { g.RestartTargetDuration = 0 },
		"roll-forward term":   func(g *runEvacuationCheckpoint) { g.RollForwardGeneration = generation },
		"rollback term":       func(g *runEvacuationCheckpoint) { g.RollbackGeneration = generation },
		"source unfenced":     func(g *runEvacuationCheckpoint) { g.SourceFencedAt = time.Time{} },
		"replacement commit":  func(g *runEvacuationCheckpoint) { g.ReplacementCommittedAt = time.Now().UTC() },
		"staged targets":      func(g *runEvacuationCheckpoint) { g.StagedTargets = []string{"gen-z"} },
		"no checkpoint":       func(g *runEvacuationCheckpoint) { g.RestartCheckpoint = nil },
		"prior topology":      func(g *runEvacuationCheckpoint) { g.RestartCheckpoint.Shards = cloneGroupShardMap(g.BeforeShards) },
		"source not archived": func(g *runEvacuationCheckpoint) { g.RestartCheckpoint.ArchiveTargets = nil },
		"other lineage owner": func(g *runEvacuationCheckpoint) { g.RestartCheckpoint.ExecutionLeaseOwnerID = "other" },
		"other lineage term":  func(g *runEvacuationCheckpoint) { g.RestartCheckpoint.ExecutionGeneration++ },
		"other token":         func(g *runEvacuationCheckpoint) { g.RestartCheckpoint.RunSupervisionToken.Ciphertext = "other" },
		"no intent":           func(g *runEvacuationCheckpoint) { g.RestartCheckpoint.PendingAdoptions = nil },
		"intent of another owner": func(g *runEvacuationCheckpoint) {
			g.RestartCheckpoint.PendingAdoptions[len(g.RestartCheckpoint.PendingAdoptions)-1].OwnerID = "other"
		},
		"completed old replacement": func(g *runEvacuationCheckpoint) { g.RestartCheckpoint.CompletedTargets = []string{"gen-b"} },
	} {
		t.Run("replacement_restarting/"+name, func(t *testing.T) {
			g := encodedJournal(t, raw)
			mutate(&g)
			encoded := encodeEvacuationGuard(t, g)
			if _, _, err := interruptedEvacuationTargets(encoded, f.test.ID); err == nil || !strings.Contains(err.Error(), "contradictory") {
				t.Fatalf("fail-closed recovery accepted it: %v", err)
			}
			if _, _, _, err := runEvacuationRestartPlan(encoded, f.test.ID); err == nil {
				t.Fatal("restart accepted it")
			}
		})
	}
}

// A restart intent reaching a term with the setting off, as on an upgrade
// rolled back, is no candidate: it is held, and a newer term fences the
// survivor and the old replacement, never the fenced source.
func TestRestartJournalWithTheSettingOffIsHeld(t *testing.T) {
	f, _ := newEvacuationRestartFixture(t, restartOptions{})
	raw := f.writeRestartIntent(t)
	_, _, _ = f.takeOver(t, "restart-disabled-successor")
	f.tr.evacuationReplacementRestart = false
	if f.tr.isRunAdoptionCandidate(f.test) {
		t.Fatal("a restart intent was adopted with the setting off")
	}
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.ContinuityCheckpoint != raw || !strings.Contains(record.Error, "the missing-replacement restart is not enabled") {
		t.Fatalf("restart intent with the setting off = %q %q, %v", record.Status, record.Error, err)
	}
	// This term is newer than the intent's, so its fences cover whatever
	// that term may have adopted.
	markEvacuationRecoveryRunStarted(t, f.store, f.test)
	if err := f.tr.recoverInterruptedRunEvacuation(f.test); err != nil {
		t.Fatal(err)
	}
	if record, err = f.store.GetTest(f.test.ID); err != nil || record.Status != "FAILED" || !strings.HasPrefix(record.Error, interruptedEvacuationFenceProof) {
		t.Fatalf("newer term did not fence the held restart: %+v %v", record, err)
	}
	for name, gen := range map[string]*rollbackGeneratorClient{"old replacement": f.spareGen, "survivor": f.survivorGen} {
		if claims, _ := gen.snapshot(); len(claims) != 1 || len(claims[0]) != 0 {
			t.Fatalf("%s fences = %+v", name, claims)
		}
	}
	if claims, _ := f.sourceGen.snapshot(); len(claims) != 0 {
		t.Fatalf("the durably fenced source was claimed: %+v", claims)
	}
}

// A stop that wins the race with the restart owns the run: the restart
// starts nothing and Phase B goes on so that the stop can finish.
func TestEvacuationRestartLeavesARacingStopToTheStop(t *testing.T) {
	f, spare := newEvacuationRestartFixture(t, restartOptions{})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.tr.mu.Lock()
	f.test.Status = "STOPPING"
	f.tr.mu.Unlock()
	if ok, _ := f.restartPhaseB(t); !ok {
		t.Fatal("the restart claimed a run a stop owns")
	}
	if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
		t.Fatalf("a stopping run's share was restarted: %+v", assignments)
	}
	if guard, _ := f.durableGuard(t); guard.Phase != runEvacuationPhaseReplacementRestarting {
		t.Fatalf("journal = %q", guard.Phase)
	}
}

// A version 6 replacement_committed journal is promoted as a version 5 one
// is; its absent source gains no archive it never had.
func TestVersionSixCommittedRestartIsPromoted(t *testing.T) {
	ctx := context.Background()
	f, _ := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true}})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	_, raw := f.durableGuard(t)
	next := cloneGroupShardMap(f.test.remoteGroupShards)
	next["ga"][0].Target = "gen-d"
	token := f.test.remoteRunSupervisionToken
	journal, err := f.tr.beginRunEvacuationRestartCheckpoint(ctx, f.test, raw, generation, owner, "gen-b", token[:], f.test.remoteGroupShards, next, runEvacuationCapacity{}, runEvacuationRestartMode{target: rollForwardCommitTarget})
	if err != nil {
		t.Fatal(err)
	}
	steps := []func(string) (string, error){
		func(raw string) (string, error) {
			return f.tr.markRunEvacuationStaged(ctx, f.test, raw, generation, owner, []string{"gen-d"}, runEvacuationStagedTiming{steadyElapsed: rollForwardStagedElapsed, remaining: 30 * time.Second})
		},
		func(raw string) (string, error) {
			return f.tr.markRunEvacuationSourceFenced(ctx, f.test, raw, generation, owner)
		},
		func(raw string) (string, error) {
			return f.tr.markRunEvacuationReplacementCommitting(ctx, f.test, raw, generation, owner, rollForwardStagedElapsed+30*time.Second)
		},
		func(raw string) (string, error) {
			return f.tr.markRunEvacuationReplacementCommitted(ctx, f.test, raw, generation, owner)
		},
	}
	for _, step := range steps {
		if journal, err = step(journal); err != nil {
			t.Fatal(err)
		}
	}
	guard, committed := committedRunEvacuationCheckpoint(journal, f.test.ID)
	if !committed || guard.Version != runEvacuationRestartCheckpointVersion || guard.SourceAbsentAt.IsZero() {
		t.Fatalf("version 6 committed journal lacks promotion proof: %+v", guard)
	}
	adoptedSpare := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-d")
	f.extra["gen-d"] = func() (generatorClient, error) { return adoptedSpare, nil }
	_, newOwner, newGeneration := f.takeOver(t, "restart-promoting-successor")
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.restart != nil || resume.adopted["gen-d"] == nil || resume.adopted["gen-c"] == nil {
		t.Fatalf("committed restart not promoted: %+v", resume)
	}
	if claims, _ := f.spareGen.snapshot(); len(claims) != 1 {
		t.Fatalf("the restart's absent source was claimed again: %+v", claims)
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint)
	if err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-d" || checkpoint.ExecutionGeneration != newGeneration || checkpoint.ExecutionLeaseOwnerID != newOwner ||
		slices.Contains(checkpoint.ArchiveTargets, "gen-b") || !slices.Contains(checkpoint.ArchiveTargets, "gen-a") {
		t.Fatalf("promoted checkpoint = %+v, %v", checkpoint, err)
	}
	// The restart journal's prior was sealed before the share moved; the
	// promoted lineage records the restart with the failover that made it.
	if count := len(checkpoint.Failovers); count < 2 || checkpoint.Failovers[count-2].RestartedVUsers != 5 || !slices.Equal(checkpoint.Failovers[count-2].RestartedGroups, []string{"ga"}) {
		t.Fatalf("promoted failovers = %+v", checkpoint.Failovers)
	}
}

// A roll-forward whose replacement was not adopted, and whose answer does
// not prove it runs nothing, still fails as before, and its hold says why the
// share was not restarted.
func TestEvacuationRestartFallbackRefusalsHoldTheRollForward(t *testing.T) {
	for name, c := range map[string]struct {
		prepare func(*evacuationRollbackFixture)
		want    string
	}{
		"replacement unreachable": {func(f *evacuationRollbackFixture) { f.spareDown = true }, "it is unreachable, which does not prove it stopped"},
		"replacement still stopping": {func(f *evacuationRollbackFixture) {
			f.replacement = &stillStoppingClient{f.spareGen}
		}, "its run is still stopping"},
		"setting off": {func(f *evacuationRollbackFixture) {
			f.tr.evacuationReplacementRestart = false
			f.tr.evacuationRollForwardRecovery = true
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
		}, "gen-b: UNKNOWN_TEST"},
		"no run start": {func(f *evacuationRollbackFixture) {
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
			f.test.StartedAt = nil
		}, "the remaining active duration cannot be proven"},
	} {
		t.Run(name, func(t *testing.T) {
			f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting}})
			c.prepare(f)
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertFailedRollForwardHeld(t, generation, c.want, true, false)
			if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
				t.Fatalf("a refused restart assigned the share: %+v", assignments)
			}
		})
	}
}

// A restart's own committing journal reaching a term with the setting off is
// not rolled forward: only a term that may restart again finishes it.
func TestRestartCommittingJournalWithTheSettingOffIsHeld(t *testing.T) {
	ctx := context.Background()
	f, _ := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true}})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	_, raw := f.durableGuard(t)
	next := cloneGroupShardMap(f.test.remoteGroupShards)
	next["ga"][0].Target = "gen-d"
	token := f.test.remoteRunSupervisionToken
	journal, err := f.tr.beginRunEvacuationRestartCheckpoint(ctx, f.test, raw, generation, owner, "gen-b", token[:], f.test.remoteGroupShards, next, runEvacuationCapacity{}, runEvacuationRestartMode{target: rollForwardCommitTarget})
	if err != nil {
		t.Fatal(err)
	}
	if journal, err = f.tr.markRunEvacuationStaged(ctx, f.test, journal, generation, owner, []string{"gen-d"}, runEvacuationStagedTiming{steadyElapsed: rollForwardStagedElapsed, remaining: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if journal, err = f.tr.markRunEvacuationSourceFenced(ctx, f.test, journal, generation, owner); err != nil {
		t.Fatal(err)
	}
	if journal, err = f.tr.markRunEvacuationReplacementCommitting(ctx, f.test, journal, generation, owner, rollForwardStagedElapsed+30*time.Second); err != nil {
		t.Fatal(err)
	}
	f.takeOver(t, "restart-disabled-successor")
	if _, _, rollForward := f.tr.evacuationRollForwardGuard(f.test, journal); !rollForward {
		t.Fatal("a restart's committing journal is not rolled forward with the setting on")
	}
	f.tr.evacuationReplacementRestart = false
	if _, _, rollForward := f.tr.evacuationRollForwardGuard(f.test, journal); rollForward || f.tr.isRunAdoptionCandidate(f.test) {
		t.Fatal("a restart's committing journal was rolled forward with the setting off")
	}
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "STOPPING" || record.ContinuityCheckpoint != journal {
		t.Fatalf("restart's committing journal with the setting off = %+v, %v", record, err)
	}
}

// TestEvacuationRestartResumesThroughPhaseB runs the whole successor against
// real in-process generators for the survivor and the new replacement: Phase A
// adopts the survivor under the restart intent, and Phase B restarts the share
// and then streams both generators' metrics.
func TestEvacuationRestartResumesThroughPhaseB(t *testing.T) {
	ctx := context.Background()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	survivor := newRestartGenerator(t, "gen-c", 10, false)
	f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true}}, func(f *evacuationRollbackFixture) {
		f.runSurvivorShareOn(t, survivor)
	})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if resume := f.test.adoptedResume; resume == nil || resume.restart == nil || resume.adopted["gen-c"] == nil || !resume.secured {
		t.Fatalf("Phase A did not secure the survivor for a restart: %+v", resume)
	}
	f.tr.resumeAdoptedRuns()
	t.Cleanup(func() { f.endResumedRun(t, generation, survivor.client, spare.client) })
	waitForActiveVUsers(t, spare.service, 5)
	waitForActiveVUsers(t, survivor.service, 3)
	f.tr.mu.RLock()
	streams := len(f.test.remoteMetricsStreamsByTarget)
	status := f.test.Status
	f.tr.mu.RUnlock()
	if streams != 2 || status != "RUNNING" {
		t.Fatalf("resumed run: %d metrics streams, status %s", streams, status)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "RUNNING" {
		t.Fatalf("durable run = %+v, %v", record, err)
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	if err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-d" || checkpoint.Shards["gc"][0].Target != "gen-c" || checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner {
		t.Fatalf("resumed checkpoint = %+v, %v", checkpoint, err)
	}
	// The survivor answers to the successor's term only.
	if result, err := survivor.client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: f.oldGen, TargetDuration: time.Hour, RunSupervisionToken: f.token}); err == nil && result.Accepted {
		t.Fatal("the survivor accepted the predecessor's duration change")
	}
}

// TestEvacuationRestartWithoutASurvivor restarts the share of a run that the
// source alone ran, through the whole successor and with the new replacement
// in process: nothing is adopted in Phase A, and Phase B secures the new
// replacement once it runs. A loss-tolerant run also moves its per-run lease.
func TestEvacuationRestartWithoutASurvivor(t *testing.T) {
	for _, mode := range []string{"ordinary", "loss tolerant", "goal"} {
		t.Run(mode, func(t *testing.T) {
			lossTolerant := mode == "loss tolerant"
			f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true, noSurvivor: true}}, func(f *evacuationRollbackFixture) {
				f.test.Assignment.TolerateGeneratorLoss = lossTolerant
				if mode == "goal" {
					target := 10.0
					f.test.ScenarioMode, f.test.GoalConfig = "goal_oriented", &GoalConfig{TargetAverageTPS: &target}
				}
			})
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
			owner, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			resume := f.test.adoptedResume
			if resume == nil || resume.restart == nil || len(resume.adopted) != 0 || resume.secured {
				t.Fatalf("Phase A of a run without a survivor = %+v", resume)
			}
			f.tr.resumeAdoptedRuns()
			t.Cleanup(func() { f.endResumedRun(t, generation, spare.client) })
			waitForActiveVUsers(t, spare.service, 5)
			f.tr.mu.RLock()
			streams, status := len(f.test.remoteMetricsStreamsByTarget), f.test.Status
			f.tr.mu.RUnlock()
			if streams != 1 || status != "RUNNING" {
				t.Fatalf("resumed run: %d metrics streams, status %s", streams, status)
			}
			record, err := f.store.GetTest(f.test.ID)
			if err != nil || record.Status != "RUNNING" {
				t.Fatalf("durable run = %+v, %v", record, err)
			}
			checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
			if err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-d" || len(checkpoint.Shards) != 1 || checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner {
				t.Fatalf("resumed checkpoint = %+v, %v", checkpoint, err)
			}
			assignments, _ := spare.recorder.snapshot()
			if len(assignments) != 1 || assignments[0].RequireTestExecutionLease != lossTolerant {
				t.Fatalf("restart assignments = %+v", assignments)
			}
			// The restarted share starts uncapped; Phase B sends a goal run's
			// cap to it, as the end of an evacuation does.
			caps := spare.recorder.throughputCaps()
			if mode == "goal" && (len(caps) == 0 || caps[0].GroupName != "ga" || caps[0].ExecutionGeneration != generation || !caps[0].Enabled || caps[0].TPS != 10) {
				t.Fatalf("goal caps sent to the restarted share = %+v", caps)
			}
			if mode != "goal" && len(caps) != 0 {
				t.Fatalf("a run without a goal was capped: %+v", caps)
			}
		})
	}
}

// A restart's own journal whose absent source the next term cannot reach is
// held: that term's claim must fence the source again before the share moves.
func TestEvacuationRestartHoldsAnUnreachableAbsentSource(t *testing.T) {
	ctx := context.Background()
	f, _ := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true}})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	_, raw := f.durableGuard(t)
	next := cloneGroupShardMap(f.test.remoteGroupShards)
	next["ga"][0].Target = "gen-d"
	token := f.test.remoteRunSupervisionToken
	// The restart moves the share of the intent's missing replacement only.
	if _, err := f.tr.beginRunEvacuationRestartCheckpoint(ctx, f.test, raw, generation, owner, "gen-c", token[:], f.test.remoteGroupShards, next, runEvacuationCapacity{}, runEvacuationRestartMode{target: rollForwardCommitTarget}); err == nil {
		t.Fatal("a restart began from a generator its intent does not name")
	}
	// The restart may never run the share past the duration its intent carries.
	if _, err := f.tr.beginRunEvacuationRestartCheckpoint(ctx, f.test, raw, generation, owner, "gen-b", token[:], f.test.remoteGroupShards, next, runEvacuationCapacity{}, runEvacuationRestartMode{target: rollForwardStagedElapsed + rollForwardStagedRemaining + time.Second}); err == nil {
		t.Fatal("a restart began past its journaled duration")
	}
	if _, err := f.tr.beginRunEvacuationRestartCheckpoint(ctx, f.test, raw, generation, owner, "gen-b", token[:], f.test.remoteGroupShards, next, runEvacuationCapacity{}, runEvacuationRestartMode{target: rollForwardCommitTarget}); err != nil {
		t.Fatal(err)
	}
	_, newGeneration := func() (string, uint64) { _, o, g := f.takeOver(t, "restart-unreachable-source-successor"); return o, g }()
	f.spareDown = true
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "restart source gen-b could not be fenced") {
		t.Fatalf("held run = %q %q, %v", record.Status, record.Error, err)
	}
	guard, _ := f.durableGuard(t)
	if guard.Phase != runEvacuationPhaseReplacementRestarting || guard.RestartGeneration != newGeneration || guard.Source != "gen-b" || guard.SourceAbsentAt.IsZero() {
		t.Fatalf("journal after the refused restart = %+v", guard)
	}
}

// endResumedRun ends a run Phase B resumed, then stops its generators.
func (f *evacuationRollbackFixture) endResumedRun(t *testing.T, generation uint64, clients ...*generatorrpc.Client) {
	t.Helper()
	f.tr.mu.RLock()
	token := f.test.remoteRunSupervisionToken
	f.tr.mu.RUnlock()
	stopRunnerRuntimes(t, f.tr)
	for _, client := range clients {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = stopGeneratorTestWithToken(stopCtx, client, f.test.ID, false, generation, token[:])
		cancel()
	}
}

// stopRunnerRuntimes ends every runtime a runner installed before the test's
// store closes. It joins the supervision keeper. Metrics consumers have no done
// signal: cancelling their lifecycle ends their streams, and a consumer that
// sees a receive fail after the cancellation returns without writing, but one
// already past that check can still process one batch it had received. Leases,
// schedules and clocks stop with the run's context.
func stopRunnerRuntimes(t *testing.T, tr *TestRunner) {
	t.Helper()
	tr.mu.Lock()
	var cancels []context.CancelFunc
	var keepers []runSupervisionKeeperHandle
	for _, test := range tr.tests {
		if test.remoteLifecycleCancel != nil {
			cancels = append(cancels, test.remoteLifecycleCancel)
		}
		keepers = append(keepers, detachRunSupervisionKeeperLocked(test))
		if test.cancel != nil {
			cancels = append(cancels, test.cancel)
		}
	}
	tr.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, keeper := range keepers {
		if err := stopAndWaitRunSupervisionKeeper(ctx, keeper); err != nil {
			t.Errorf("run supervision keeper did not stop: %v", err)
		}
	}
	tr.beginShutdown()
}

// TestEvacuationRestartMovesEveryGroupToOneReplacement restarts a source that
// held two groups: both go to one generator, and when no generator can take
// both the restart is refused before anything is reserved or assigned.
func TestEvacuationRestartMovesEveryGroupToOneReplacement(t *testing.T) {
	for _, mode := range []string{"one generator takes both", "no generator takes both", "only another generator takes the second"} {
		t.Run(mode, func(t *testing.T) {
			split := mode != "one generator takes both"
			capacity, secondCapacity := 10, 10
			opts := restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true, secondMovedGroup: true}}
			switch mode {
			case "no generator takes both":
				capacity, secondCapacity = 6, 6
			case "only another generator takes the second":
				// gen-d wins the first group (it ties gen-e and comes first),
				// then reports no room: only gen-e could take the second.
				opts.configureRestartTarget = func(server *readyCapacityServer) {
					server.available = func(probe int) int32 {
						if probe <= 2 {
							return 10
						}
						return 0
					}
				}
			}
			opts.restartVUsers = capacity
			f, first := newEvacuationRestartFixture(t, opts)
			second := newRestartGenerator(t, "gen-e", secondCapacity, false)
			f.extra["gen-e"] = second.dial
			f.handOver(t, "restart-two-group-successor")
			f.superviseRuns()
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRestartIntent(t, generation)
			ok, resume := f.restartPhaseB(t)
			firstAssigned, _ := first.recorder.snapshot()
			secondAssigned, _ := second.recorder.snapshot()
			if split {
				if ok {
					t.Fatal("the moved groups were split across two generators")
				}
				want := "a restart moves every moved group to one generator"
				if mode == "only another generator takes the second" {
					want = "the restart's replacement gen-d cannot also take this group"
				}
				f.assertRestartHeld(t, generation, want, runEvacuationPhaseReplacementRestarting, true, false)
				if len(firstAssigned)+len(secondAssigned) != 0 {
					t.Fatalf("a refused restart assigned work: %d on gen-d, %d on gen-e", len(firstAssigned), len(secondAssigned))
				}
				return
			}
			if !ok {
				record, _ := f.store.GetTest(f.test.ID)
				t.Fatalf("restart failed: %+v", record)
			}
			defer f.stopRestarted(t, generation)
			if len(firstAssigned) != 2 || len(secondAssigned) != 0 || !slices.Equal(resume.restarted, []string{"gen-d"}) {
				t.Fatalf("assignments: %d on gen-d, %d on gen-e; restarted on %v", len(firstAssigned), len(secondAssigned), resume.restarted)
			}
			checkpoint, _, _ := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint)
			if checkpoint.Shards["ga"][0].Target != "gen-d" || checkpoint.Shards["gb"][0].Target != "gen-d" {
				t.Fatalf("restarted topology = %+v", checkpoint.Shards)
			}
			if count := len(checkpoint.Failovers); count == 0 || checkpoint.Failovers[count-1].RestartedVUsers != 8 || !slices.Equal(checkpoint.Failovers[count-1].RestartedGroups, []string{"ga", "gb"}) {
				t.Fatalf("failover record = %+v", checkpoint.Failovers)
			}
		})
	}
}

// A roll-forward whose replacement's duration ran out finishes as a
// roll-forward with the replacement completed; nothing is restarted.
func TestEvacuationRestartLeavesANaturallyCompletedReplacement(t *testing.T) {
	f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting}})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
	f.spareGen.cause, f.spareGen.completed = pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, rollForwardCommitTarget
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.restart != nil || resume.completed["gen-b"] == nil || resume.adopted["gen-c"] == nil {
		t.Fatalf("naturally completed replacement not rolled forward: %+v", resume)
	}
	if _, present, err := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint); err != nil || !present {
		t.Fatalf("journal not replaced: %v", err)
	}
	if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
		t.Fatalf("a share whose duration ran out was restarted: %+v", assignments)
	}
}

// An earlier successor recorded a rolling_forward intent and was lost. The
// next term finds the replacement gone and restarts its share, proving the
// earlier term's rotation.
func TestEvacuationRestartFromAStoredRollingForwardIntent(t *testing.T) {
	ctx := context.Background()
	f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting}})
	firstOwner, firstGeneration := f.tr.leadership.executionLeaseTerm()
	guard, _, err := runEvacuationRollForwardPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	planned, _, err := f.tr.planRunAdoptionIntent(ctx, f.test.ID, runEvacuationRollForwardCheckpoint(guard), firstGeneration, firstOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.tr.persistRunEvacuationRollForwardIntent(f.test, f.guard, guard, planned, firstGeneration, firstOwner); err != nil {
		t.Fatal(err)
	}
	_, owner, generation := f.takeOver(t, "restart-after-rolling-forward")
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertRestartIntent(t, generation)
	intent, _ := f.durableGuard(t)
	if intent.RestartOwnerID != owner || intent.RestartCheckpoint.PendingAdoptions[len(intent.RestartCheckpoint.PendingAdoptions)-1].Generation != generation {
		t.Fatalf("restart intent = %+v", intent)
	}
	claims, _ := f.spareGen.snapshot()
	if last := claims[len(claims)-1]; len(last) != 1 || len(last[0].PriorProofs) != 1 || last[0].PriorProofs[0].Generation != firstGeneration {
		t.Fatalf("claim of the old replacement = %+v", last)
	}
	ok, resume := f.restartPhaseB(t)
	if !ok {
		record, _ := f.store.GetTest(f.test.ID)
		t.Fatalf("restart failed: %+v", record)
	}
	defer f.stopRestarted(t, generation)
	f.assertRestarted(t, spare, resume, owner, generation, false, rollForwardCommitTarget)
}

// The journal carries the targets a restart must never use. The fenced source
// was unreachable when the first successor installed the run, so its archive
// client and archive target dropped out; the second successor, restarting
// from the first one's restart journal, still never offers it the share.
func TestEvacuationRestartNeverReturnsToAnUnreachableFencedSource(t *testing.T) {
	f, first := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true}})
	second := newRestartGenerator(t, "gen-e", 10, false)
	f.extra["gen-e"] = second.dial
	f.extra["gen-a"] = func() (generatorClient, error) { return nil, errors.New("source node is down") }
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	_, firstGeneration := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if intent, _ := f.durableGuard(t); !slices.Contains(intent.RestartExcluded, "gen-a") {
		t.Fatalf("restart intent excludes %v, want the fenced source", intent.RestartExcluded)
	}
	block := newRestartBlock()
	first.recorder.beforeAssign = block.park
	late := make(chan struct{})
	firstTest := f.test
	go func() {
		defer close(late)
		f.tr.mu.Lock()
		resume := firstTest.adoptedResume
		firstTest.adoptedResume = nil
		executionCtx, token := firstTest.executionCtx, firstTest.remoteRunSupervisionToken
		f.tr.mu.Unlock()
		f.tr.restartEvacuationReplacement(f.tr.execution.(*distributedExecutionBackend), firstTest, resume, executionCtx, token)
	}()
	block.awaitReached(t)
	defer func() {
		close(block.release)
		<-late
	}()
	journal, _ := f.durableGuard(t)
	if journal.Phase != runEvacuationPhasePrepared || journal.Generation != firstGeneration || slices.Contains(journal.Prior.ArchiveTargets, "gen-a") || !slices.Contains(journal.RestartExcluded, "gen-a") {
		t.Fatalf("first restart journal = phase %q archive %v excluded %v", journal.Phase, journal.Prior.ArchiveTargets, journal.RestartExcluded)
	}
	// The source is back, healthy, idle and first in the fleet's order: only
	// the durable exclusion keeps the share off it.
	source := newRestartGenerator(t, "gen-a", 10, false)
	f.extra["gen-a"] = source.dial
	_, _, generation := f.takeOver(t, "restart-excluding-successor")
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	ok, resume := f.restartPhaseB(t)
	if !ok {
		record, _ := f.store.GetTest(f.test.ID)
		t.Fatalf("second restart failed: %+v", record)
	}
	defer f.stopRestarted(t, generation)
	if !slices.Equal(resume.restarted, []string{"gen-e"}) {
		t.Fatalf("second restart took %v, want gen-e", resume.restarted)
	}
	if assignments, _ := source.recorder.snapshot(); len(assignments) != 0 {
		t.Fatalf("the fenced source was given the share: %+v", assignments)
	}
}

// The share may run to the shortest journaled end.
func TestRunEvacuationRestartTargetTakesTheShortestEnd(t *testing.T) {
	for _, c := range []struct {
		guard runEvacuationCheckpoint
		want  time.Duration
	}{
		{runEvacuationCheckpoint{StagedSteadyElapsed: 20 * time.Second, StagedRemaining: 40 * time.Second}, time.Minute},
		{runEvacuationCheckpoint{StagedSteadyElapsed: 20 * time.Second, StagedRemaining: 40 * time.Second, CommitTargetDuration: 55 * time.Second}, 55 * time.Second},
		{runEvacuationCheckpoint{RestartTargetDuration: 50 * time.Second, CommitTargetDuration: 55 * time.Second}, 50 * time.Second},
		{runEvacuationCheckpoint{RestartTargetDuration: time.Minute, CommitTargetDuration: 55 * time.Second}, 55 * time.Second},
		{runEvacuationCheckpoint{RestartTargetDuration: time.Minute}, time.Minute},
		{runEvacuationCheckpoint{StagedSteadyElapsed: -time.Second, StagedRemaining: time.Minute}, 0},
	} {
		if got := runEvacuationRestartTarget(c.guard); got != c.want {
			t.Errorf("runEvacuationRestartTarget(%+v) = %s, want %s", c.guard, got, c.want)
		}
	}
}

// stopOnRestartCheckpointStore marks the run STOPPING, as an operator stop
// does, as soon as the restart's plain checkpoint is written: the stop lands
// after the restart succeeded and before Phase B looks at the run again.
type stopOnRestartCheckpointStore struct {
	*storage.SQLiteStore
	stop func()
}

func (s *stopOnRestartCheckpointStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner string) (storage.TestStatusTransitionResult, error) {
	result, err := s.SQLiteStore.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner)
	if checkpoint, present, decodeErr := decodeRunContinuityCheckpoint(next); err == nil && result == storage.TestStatusTransitionUpdated && decodeErr == nil && present && checkpoint.Shards["ga"][0].Target == "gen-d" {
		s.stop()
	}
	return result, err
}

// A stop that lands right after a successful restart still reaches the new
// replacement: it is adopted before the run's status is looked at, so Phase B
// streams its metrics for the stop's finalizer.
func TestEvacuationRestartHandsTheNewReplacementToARacingStop(t *testing.T) {
	f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{noCapacity: true}})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	f.tr.testStore = &stopOnRestartCheckpointStore{SQLiteStore: f.store, stop: func() {
		once.Do(func() {
			f.tr.mu.Lock()
			f.test.Status = "STOPPING"
			f.tr.mu.Unlock()
		})
	}}
	ok, resume := f.restartPhaseB(t)
	defer f.stopRestarted(t, generation)
	if !ok || resume.adopted["gen-d"] == nil || resume.adopted["gen-c"] == nil || !slices.Equal(resume.restarted, []string{"gen-d"}) {
		t.Fatalf("restart before a racing stop: resume Phase B = %v, adopted %v, restarted %v", ok, resume.adopted, resume.restarted)
	}
	if assignments, _ := spare.recorder.snapshot(); len(assignments) != 1 {
		t.Fatalf("restart assignments = %+v", assignments)
	}
}

// lateCallRefusalNamesGeneration reports a refusal that rests on the call's
// older execution generation or the generator's fenced state for the run.
func lateCallRefusalNamesGeneration(call restartCall) bool {
	text := strings.ToLower(call.message)
	if call.err != nil {
		text = strings.ToLower(status.Convert(call.err).Message())
	}
	for _, reason := range []string{"generation", "fenced"} {
		if strings.Contains(text, reason) {
			return true
		}
	}
	return false
}

// runSurvivorShareOn has the lost leader run the survivor's share (gc, 3
// VUsers) on an in-process generator with a 500 ms start barrier, and serves
// gen-c from it.
func (f *evacuationRollbackFixture) runSurvivorShareOn(t *testing.T, survivor *restartGenerator) {
	t.Helper()
	ctx := context.Background()
	client := survivor.client
	if _, err := client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
		t.Fatalf("lost leader epoch claim: %v", err)
	}
	f.test.Groups[1].ScriptID, f.test.Groups[1].script = f.test.Groups[0].ScriptID, f.test.Groups[0].script
	content, hash, _, err := generatorrpc.PrepareDistributedScript(f.test.Groups[1].script)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DistributeScript(ctx, generatorrpc.Script{ID: "restart-script", ExecutionGeneration: f.oldGen, Content: content, ContentHash: hash}); err != nil {
		t.Fatalf("distribute script: %v", err)
	}
	if assign, err := client.AssignWork(ctx, generatorrpc.Assignment{
		TestID: f.test.ID, GroupName: "gc", ScriptID: "restart-script", ExpectedScriptHash: hash, VUserCount: 3, ExecutionGeneration: f.oldGen,
		TargetDuration: rollForwardStagedElapsed + rollForwardStagedRemaining, RunSupervisionToken: f.token, AdoptionGrace: 30 * time.Second,
	}); err != nil || !assign.Accepted {
		t.Fatalf("assign survivor = (%+v, %v)", assign, err)
	}
	if claim, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{TestID: f.test.ID, ExecutionGeneration: f.oldGen, Token: f.token, RequestedTTL: time.Minute, ExecutionLeaseOwnerID: f.oldOwner}); err != nil || !claim.Accepted {
		t.Fatalf("survivor supervision = (%+v, %v)", claim, err)
	}
	if commit, err := client.CommitWorkSupervisedAt(ctx, f.test.ID, f.oldGen, time.Now().Add(500*time.Millisecond), f.token); err != nil || !commit.Accepted {
		t.Fatalf("survivor commit = (%+v, %v)", commit, err)
	}
	waitForActiveVUsers(t, survivor.service, 3)
	f.extra["gen-c"] = survivor.dial
}
