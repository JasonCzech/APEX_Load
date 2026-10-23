package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// rollbackGeneratorClient records the epoch claims and capacity requests one
// generator receives during a successor's evacuation rollback.
type rollbackGeneratorClient struct {
	*capacityAdoptionClient
	mu        sync.Mutex
	claims    [][]generatorrpc.RunAdoption
	capacity  []*pb.PodCapacityClaim
	stops     []uint64
	rejectCap bool
	panicCap  bool
	panicStop bool
}

func (c *rollbackGeneratorClient) StopTestAtGenerationWithToken(_ context.Context, _ string, graceful bool, generation uint64, _ []byte) (generatorrpc.StopResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if graceful {
		return generatorrpc.StopResult{}, errors.New("rollback failure must force-stop")
	}
	if c.panicStop {
		panic("stop client failure")
	}
	c.stops = append(c.stops, generation)
	return generatorrpc.StopResult{Acknowledged: true}, nil
}

func (c *rollbackGeneratorClient) stopGenerations() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint64(nil), c.stops...)
}

func newRollbackGeneratorClient(outcome pb.RunAdoptionOutcome, incarnation string) *rollbackGeneratorClient {
	client := &rollbackGeneratorClient{}
	client.capacityAdoptionClient = &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{outcome: outcome}}
	client.confirm = func(claim *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
		client.mu.Lock()
		defer client.mu.Unlock()
		client.capacity = append(client.capacity, claim)
		if client.panicCap {
			panic("capacity client failure")
		}
		if client.rejectCap {
			return &pb.PodCapacityAck{ErrorMessage: "refused", Snapshot: &pb.PodCapacitySnapshot{Incarnation: incarnation}}, nil
		}
		return &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: incarnation}}, nil
	}
	return client
}

func (c *rollbackGeneratorClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, owner string, ttl time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	c.mu.Lock()
	c.claims = append(c.claims, append([]generatorrpc.RunAdoption(nil), adoptions...))
	c.mu.Unlock()
	return c.adoptionFakeClient.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, adoptions)
}

func (c *rollbackGeneratorClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, owner string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	return c.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, nil)
}

func (c *rollbackGeneratorClient) snapshot() ([][]generatorrpc.RunAdoption, []*pb.PodCapacityClaim) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]generatorrpc.RunAdoption(nil), c.claims...), append([]*pb.PodCapacityClaim(nil), c.capacity...)
}

type evacuationRollbackFixture struct {
	tr        *TestRunner
	store     *storage.SQLiteStore
	test      *ManagedTest
	guard     string
	oldOwner  string
	oldGen    uint64
	source    storage.PodCapacityReservation
	claim     storage.PodCapacityReservation
	standby   *storage.PodCapacityReservation
	sourceGen *rollbackGeneratorClient
	spareGen  *rollbackGeneratorClient
	spareDown bool
}

var rollbackBudget = protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 10}
var rollbackDemand = protocol.CapacityVector{CPUCores: .5, MemoryMB: 50, NetworkMbps: 50, VUsers: 5}

// newEvacuationRollbackFixture journals an evacuation of gen-a to gen-b that
// reserved replacement capacity (activating a standby when standby is set),
// then hands the execution lease to a successor that has not yet reconciled.
func newEvacuationRollbackFixture(t *testing.T, phase string, standby bool, beforeHandOver ...func(*evacuationRollbackFixture)) *evacuationRollbackFixture {
	t.Helper()
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.evacuationRollbackRecovery = true
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	f := &evacuationRollbackFixture{tr: tr, store: store, test: test, oldOwner: owner, oldGen: generation}
	f.source = storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen-a", TestID: test.ID, Incarnation: "pod-a", Generation: generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackDemand, GroupPeaks: map[string]int32{"ga": 5}, GroupDemands: map[string]protocol.CapacityVector{"ga": rollbackDemand}}
	reserveCommitted(t, store, f.source, owner, generation)
	var journal runEvacuationCapacity
	if standby {
		idle := storage.PodCapacityReservation{ReservationID: "spare", Revision: 3, Target: "gen-b", TestID: test.ID, Incarnation: "pod-b", Generation: generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackBudget, Dedicated: true}
		reserveCommitted(t, store, idle, owner, generation)
		rows, err := store.ListPodCapacityReservations()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Target == idle.Target {
				idle = row
			}
		}
		activated := idle
		activated.State = "pending"
		activated.Revision++
		activated.GroupPeaks = map[string]int32{"ga": 5}
		activated.GroupDemands = map[string]protocol.CapacityVector{"ga": rollbackDemand}
		if err := store.ReservePodCapacityIfUnchanged([]storage.PodCapacityReservation{activated}, []storage.PodCapacityReservation{idle}, executionLeaseName, owner, generation); err != nil {
			t.Fatal(err)
		}
		f.claim, f.standby = activated, &idle
		journal = runEvacuationCapacity{claims: []storage.PodCapacityReservation{activated}, preImages: []storage.PodCapacityReservation{idle}}
	} else {
		f.claim = storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen-b", TestID: test.ID, Incarnation: "pod-b", Generation: generation, State: "pending", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackDemand, GroupPeaks: map[string]int32{"ga": 5}, GroupDemands: map[string]protocol.CapacityVector{"ga": rollbackDemand}}
		if err := store.ReservePodCapacity([]storage.PodCapacityReservation{f.claim}, executionLeaseName, owner, generation); err != nil {
			t.Fatal(err)
		}
		journal = runEvacuationCapacity{claims: []storage.PodCapacityReservation{f.claim}}
	}
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, journal)
	if err != nil {
		t.Fatal(err)
	}
	if phase == runEvacuationPhaseStaged || phase == runEvacuationPhaseSourceFenced {
		if guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"}); err != nil {
			t.Fatal(err)
		}
	}
	if phase == runEvacuationPhaseSourceFenced {
		if guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner); err != nil {
			t.Fatal(err)
		}
	}
	f.guard = guard
	f.sourceGen = newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-a")
	f.spareGen = newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-b")
	for _, prepare := range beforeHandOver {
		prepare(f)
	}
	f.handOver(t, "rollback-successor")
	return f
}

func reserveCommitted(t *testing.T, store *storage.SQLiteStore, claim storage.PodCapacityReservation, owner string, generation uint64) {
	t.Helper()
	if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
}

// handOver releases the current lease and installs a successor leader with a
// newer generation and fresh generator transports.
func (f *evacuationRollbackFixture) handOver(t *testing.T, name string) (string, uint64) {
	t.Helper()
	owner, _ := f.tr.leadership.executionLeaseTerm()
	if err := f.store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(f.store, name)
	successor.tick()
	f.tr.leadership = successor
	f.tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, func(_ context.Context, target string) (generatorClient, error) {
		switch target {
		case "gen-a":
			return f.sourceGen, nil
		case "gen-b":
			if f.spareDown {
				return nil, errors.New("replacement unreachable")
			}
			return f.spareGen, nil
		}
		return nil, errors.New("unknown generator")
	})
	return successor.executionLeaseTerm()
}

func (f *evacuationRollbackFixture) ledger(t *testing.T) map[string]storage.PodCapacityReservation {
	t.Helper()
	rows, err := f.store.ListPodCapacityReservations()
	if err != nil {
		t.Fatal(err)
	}
	byTarget := make(map[string]storage.PodCapacityReservation)
	for _, row := range rows {
		if row.TestID == f.test.ID {
			byTarget[row.Target] = row
		}
	}
	return byTarget
}

func (f *evacuationRollbackFixture) durableGuard(t *testing.T) (runEvacuationCheckpoint, string) {
	t.Helper()
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	var guard runEvacuationCheckpoint
	_ = json.Unmarshal([]byte(record.ContinuityCheckpoint), &guard)
	return guard, record.ContinuityCheckpoint
}

func TestEvacuationJournalVersionFollowsRollbackRecovery(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
		tr.evacuationRollbackRecovery = enabled
		claim := storage.PodCapacityReservation{ReservationID: "attempt", Target: "gen-b", TestID: test.ID, Generation: generation, State: "pending", GroupPeaks: map[string]int32{"ga": 5}}
		raw, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{claims: []storage.PodCapacityReservation{claim}})
		if err != nil {
			t.Fatal(err)
		}
		var guard runEvacuationCheckpoint
		if err := json.Unmarshal([]byte(raw), &guard); err != nil {
			t.Fatal(err)
		}
		if enabled && (guard.Version != runEvacuationRollbackCheckpointVersion || len(guard.CapacityClaims) != 1 || guard.CapacityClaims[0].Target != "gen-b") {
			t.Fatalf("enabled journal = version %d claims %+v", guard.Version, guard.CapacityClaims)
		}
		if !enabled && (guard.Version != runEvacuationCheckpointVersion || guard.CapacityClaims != nil || strings.Contains(raw, "capacity_claims")) {
			t.Fatalf("disabled journal = version %d claims %+v", guard.Version, guard.CapacityClaims)
		}
		if _, _, err := decodeRunContinuityCheckpoint(raw); err == nil {
			t.Fatal("an older adoption reader accepted the evacuation journal")
		}
		// Progress keeps the version it was written with.
		staged, err := tr.markRunEvacuationStaged(context.Background(), test, raw, generation, owner, []string{"gen-b"})
		if err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal([]byte(staged), &guard)
		if guard.Version != map[bool]int{false: runEvacuationCheckpointVersion, true: runEvacuationRollbackCheckpointVersion}[enabled] {
			t.Fatalf("staged journal changed version to %d", guard.Version)
		}
	}
}

func TestEvacuationRollbackReturnsRunToPriorTopology(t *testing.T) {
	for _, phase := range []string{runEvacuationPhasePrepared, runEvacuationPhaseStaged} {
		for _, standby := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "/replacement", true: "/standby"}[standby], func(t *testing.T) {
				f := newEvacuationRollbackFixture(t, phase, standby)
				owner, generation := f.tr.leadership.executionLeaseTerm()
				if err := f.tr.reconcileStoredActiveTest(f.test); err != nil || f.test.Status != "RUNNING" {
					t.Fatalf("rollback candidate reconciled to %s: %v", f.test.Status, err)
				}
				if err := f.tr.adoptOrphanedRuns(); err != nil {
					t.Fatal(err)
				}
				resume := f.test.adoptedResume
				if resume == nil || resume.finishStatus != "" || len(resume.adopted) != 1 || resume.adopted["gen-a"] == nil {
					t.Fatalf("prior topology not adopted: %+v", resume)
				}
				if f.test.remoteClientsByTarget["gen-b"] != nil {
					t.Fatal("the fenced replacement became a run participant")
				}
				claims, capacity := f.spareGen.snapshot()
				if len(claims) != 1 || len(claims[0]) != 0 {
					t.Fatalf("replacement epoch claims = %+v, want one claim without this run's adoption", claims)
				}
				sourceClaims, _ := f.sourceGen.snapshot()
				if len(sourceClaims) != 1 || len(sourceClaims[0]) != 1 || sourceClaims[0][0].PriorGeneration != f.oldGen {
					t.Fatalf("source adoption = %+v", sourceClaims)
				}
				record, err := f.store.GetTest(f.test.ID)
				if err != nil {
					t.Fatal(err)
				}
				checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
				if err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-a" || checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner {
					t.Fatalf("rolled-back checkpoint = %+v, %v", checkpoint, err)
				}
				ledger := f.ledger(t)
				if ledger["gen-a"].Generation != generation || ledger["gen-a"].State != "committed" {
					t.Fatalf("source capacity not adopted: %+v", ledger["gen-a"])
				}
				if !standby {
					if _, kept := ledger["gen-b"]; kept || len(capacity) != 1 || !capacity[0].Release || capacity[0].ExecutionGeneration != generation {
						t.Fatalf("replacement capacity not released: ledger=%+v requests=%+v", ledger["gen-b"], capacity)
					}
					return
				}
				want := *f.standby
				want.Generation = generation
				if got := ledger["gen-b"]; !restoredRunEvacuationStandby(got, want) || got.Generation != generation || got.State != "committed" || got.Revision != want.Revision {
					t.Fatalf("standby not restored idle: %+v", got)
				}
				if len(capacity) != 2 || !capacity[0].Release || capacity[1].Release || len(capacity[1].GroupPeaks) != 0 || capacity[1].ExecutionGeneration != generation {
					t.Fatalf("standby restore requests = %+v", capacity)
				}
			})
		}
	}
}

func TestEvacuationRollbackKeepsFailClosedPaths(t *testing.T) {
	cases := map[string]func(*evacuationRollbackFixture){
		"disabled":      func(f *evacuationRollbackFixture) { f.tr.evacuationRollbackRecovery = false },
		"source fenced": func(*evacuationRollbackFixture) {},
		"stopping":      func(f *evacuationRollbackFixture) { f.test.Status = "STOPPING" },
		"fail policy": func(f *evacuationRollbackFixture) {
			f.test.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: "fail"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			phase := runEvacuationPhaseStaged
			if name == "source fenced" {
				phase = runEvacuationPhaseSourceFenced
			}
			f := newEvacuationRollbackFixture(t, phase, false)
			mutate(f)
			if _, _, rollback := f.tr.evacuationRollbackGuard(f.test, f.test.continuityCheckpoint); rollback {
				t.Fatal("journal was accepted for rollback")
			}
			if f.tr.isRunAdoptionCandidate(f.test) {
				t.Fatal("journal became a rollback candidate")
			}
			if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
				t.Fatal(err)
			}
			record, err := f.store.GetTest(f.test.ID)
			if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.HasPrefix(record.Error, "generator evacuation state is uncertain") || record.ContinuityCheckpoint != f.guard {
				t.Fatalf("journal reconciled to %+v, %v", record, err)
			}
		})
	}
}

func TestEvacuationRollbackPlanRejectsUnsafeJournals(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, true)
	if _, replacements, err := runEvacuationRollbackPlan(f.guard, f.test.ID); err != nil || len(replacements) != 1 || replacements[0] != "gen-b" {
		t.Fatalf("valid journal rejected: %v %v", replacements, err)
	}
	type mutation struct {
		mutate func(*runEvacuationCheckpoint)
		want   string
	}
	mutations := map[string]mutation{
		"version 3":        {func(g *runEvacuationCheckpoint) { g.Version = runEvacuationCheckpointVersion }, "predates successor rollback"},
		"source completed": {func(g *runEvacuationCheckpoint) { g.Prior.CompletedTargets = []string{"gen-a"} }, "source already completed"},
		"survivor changed": {func(g *runEvacuationCheckpoint) {
			g.BeforeShards["gb"] = []GeneratorShardSummary{{Group: "gb", Target: "gen-c", VUsers: 2}}
			g.Prior.Shards = cloneGroupShardMap(g.BeforeShards)
			g.NextShards["gb"] = []GeneratorShardSummary{{Group: "gb", Target: "gen-c", VUsers: 3}}
		}, "changed surviving participant"},
		"claim off replacement": {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].Target = "gen-z" }, "invalid replacement claim"},
		"claim duplicated": {func(g *runEvacuationCheckpoint) {
			g.CapacityClaims = append(g.CapacityClaims, g.CapacityClaims[0])
		}, "invalid replacement claim"},
		"claim other run":       {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].TestID = "other" }, "invalid replacement claim"},
		"claim committed":       {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].State = "committed" }, "invalid replacement claim"},
		"claim other term":      {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].Generation++ }, "invalid replacement claim"},
		"claim without peaks":   {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].GroupPeaks = nil }, "invalid replacement claim"},
		"claim without attempt": {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].ReservationID = "" }, "invalid replacement claim"},
		"pre-image unclaimed":   {func(g *runEvacuationCheckpoint) { g.CapacityClaims = nil }, "invalid standby pre-image"},
		"pre-image duplicated": {func(g *runEvacuationCheckpoint) {
			g.StandbyPreImages = append(g.StandbyPreImages, g.StandbyPreImages[0])
		}, "invalid standby pre-image"},
		"pre-image not idle":    {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].GroupPeaks = map[string]int32{"ga": 1} }, "invalid standby pre-image"},
		"pre-image pending":     {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].State = "pending" }, "invalid standby pre-image"},
		"pre-image other run":   {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].TestID = "other" }, "invalid standby pre-image"},
		"pre-image other term":  {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].Generation++ }, "invalid standby pre-image"},
		"pre-image attempt":     {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].ReservationID = "other" }, "invalid standby pre-image"},
		"pre-image incarnation": {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].Incarnation = "other" }, "invalid standby pre-image"},
		"pre-image headroom":    {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].HeadroomPct = 7 }, "invalid standby pre-image"},
		"pre-image budget":      {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].Budget.VUsers++ }, "invalid standby pre-image"},
		"pre-image demand":      {func(g *runEvacuationCheckpoint) { g.StandbyPreImages[0].Demand = rollbackDemand }, "invalid standby pre-image"},
		"pre-image budget vs claim": {func(g *runEvacuationCheckpoint) {
			g.StandbyPreImages[0].Budget.VUsers++
			g.StandbyPreImages[0].Demand.VUsers++
		}, "invalid standby pre-image"},
		"claim without incarnation": {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].Incarnation = "" }, "invalid replacement claim"},
	}
	for name, c := range mutations {
		t.Run(name, func(t *testing.T) {
			var guard runEvacuationCheckpoint
			_ = json.Unmarshal([]byte(f.guard), &guard)
			c.mutate(&guard)
			raw, _ := json.Marshal(guard)
			if _, _, err := runEvacuationRollbackPlan(string(raw), f.test.ID); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("plan error = %v, want %q", err, c.want)
			}
		})
	}
}

// assertFailedRollbackHeld checks that a failed rollback kept the journal,
// force-stopped the adopted source in this term and left the run STOPPING with
// no runtime, so attestation and a newer term's fences can resolve it.
func (f *evacuationRollbackFixture) assertFailedRollbackHeld(t *testing.T, generation uint64, problem string, sourceAdopted bool) {
	t.Helper()
	if f.test.adoptedResume != nil || f.test.executionCtx != nil || f.test.cancel != nil {
		t.Fatalf("failed rollback installed a runtime: %+v", f.test.adoptedResume)
	}
	guard, _ := f.durableGuard(t)
	if guard.Phase != runEvacuationPhaseRollingBack || guard.RollbackGeneration != generation || guard.Version != runEvacuationRollbackCheckpointVersion {
		t.Fatalf("journal = phase %q rollback generation %d", guard.Phase, guard.RollbackGeneration)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) || !strings.Contains(record.Error, problem) {
		t.Fatalf("durable run = %+v, %v", record, err)
	}
	f.tr.mu.RLock()
	eligible := interruptedEvacuationAwaitingProofLocked(f.test)
	f.tr.mu.RUnlock()
	if !eligible {
		t.Fatal("failed rollback is not eligible for administrator attestation")
	}
	stops := f.sourceGen.stopGenerations()
	if sourceAdopted && (len(stops) != 1 || stops[0] != generation) {
		t.Fatalf("adopted source stops = %v, want one forced stop at %d", stops, generation)
	}
	if !sourceAdopted && len(stops) != 0 {
		t.Fatalf("unadopted source was stopped: %v", stops)
	}
	// The same term may have adopted the prior topology; it cannot fence it.
	if err := f.tr.recoverInterruptedRunEvacuation(f.test); err == nil || !strings.Contains(err.Error(), "rollback generation") {
		t.Fatalf("same-term fail-closed recovery = %v", err)
	}
}

func TestEvacuationRollbackUnfencedReplacementKeepsJournal(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	f.spareDown = true
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertFailedRollbackHeld(t, generation, "gen-b could not be fenced", true)
	if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
		t.Fatalf("replacement capacity changed without a fence: %+v", row)
	}
	guard, _ := f.durableGuard(t)
	if _, targets, err := interruptedEvacuationTargets(encodeEvacuationGuard(t, guard), f.test.ID); err != nil || len(targets) != 2 {
		t.Fatalf("rolling-back participants = %v, %v", targets, err)
	}
	// A newer term fences every participant, including the replacement, and
	// then records the failure.
	f.spareDown = false
	f.handOver(t, "rollback-fencing-successor")
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: record.Status, Error: record.Error, Assignment: f.test.Assignment}
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	f.tr.mu.Lock()
	f.tr.tests[f.test.ID] = restored
	f.tr.mu.Unlock()
	f.test = restored
	markEvacuationRecoveryRunStarted(t, f.store, restored)
	if err := f.tr.recoverInterruptedRunEvacuation(f.test); err != nil {
		t.Fatal(err)
	}
	record, err = f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "FAILED" || !strings.HasPrefix(record.Error, interruptedEvacuationFenceProof) {
		t.Fatalf("newer term did not fence the failed rollback: %+v %v", record, err)
	}
	if claims, _ := f.spareGen.snapshot(); len(claims) != 1 || len(claims[0]) != 0 {
		t.Fatalf("replacement fences = %+v", claims)
	}
}

func encodeEvacuationGuard(t *testing.T, g runEvacuationCheckpoint) string {
	t.Helper()
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEvacuationRollbackResumesAfterAnotherLeaderChange(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, true)
	// The first successor records its rollback intent and is lost before its
	// epoch claims, leaving the durable row RUNNING under a rolling_back journal.
	firstOwner, firstGeneration := f.tr.leadership.executionLeaseTerm()
	guard, _, err := runEvacuationRollbackPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	planned, _, err := f.tr.planRunAdoptionIntent(context.Background(), f.test.ID, guard.Prior, firstGeneration, firstOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.tr.persistRunEvacuationRollbackIntent(f.test, f.guard, guard, planned, firstGeneration, firstOwner); err != nil {
		t.Fatal(err)
	}
	_, raw := f.durableGuard(t)
	_, secondGeneration := f.handOver(t, "rollback-second-successor")
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "RUNNING" {
		t.Fatalf("a lost rollback intent left the run %+v, %v", record, err)
	}
	restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: record.Status, Assignment: f.test.Assignment}
	setContinuityCheckpointLocked(restored, raw)
	f.tr.mu.Lock()
	f.tr.tests[f.test.ID] = restored
	f.tr.mu.Unlock()
	f.test = restored
	if !f.tr.isRunAdoptionCandidate(restored) {
		t.Fatal("rolling_back journal of a running run is no rollback candidate")
	}
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if restored.adoptedResume == nil || restored.adoptedResume.finishStatus != "" || restored.adoptedResume.adopted["gen-a"] == nil {
		t.Fatalf("second successor did not adopt the prior topology: %+v", restored.adoptedResume)
	}
	sourceClaims, _ := f.sourceGen.snapshot()
	if len(sourceClaims) != 1 || len(sourceClaims[0]) != 1 || len(sourceClaims[0][0].PriorProofs) != 1 || sourceClaims[0][0].PriorProofs[0].Generation != firstGeneration {
		t.Fatalf("second adoption lacked the first successor's token proof: %+v", sourceClaims)
	}
	if got := f.ledger(t)["gen-b"]; got.Generation != secondGeneration || got.State != "committed" || !standbyCapacityReservation(got) {
		t.Fatalf("standby after second successor = %+v", got)
	}
	if claims, _ := f.spareGen.snapshot(); len(claims) != 1 || len(claims[0]) != 0 {
		t.Fatalf("replacement epoch claims = %+v", claims)
	}
	if _, final := f.durableGuard(t); final == raw {
		t.Fatal("the rolling_back journal was not replaced")
	} else if _, present, err := decodeRunContinuityCheckpoint(final); err != nil || !present {
		t.Fatalf("final checkpoint is not plain: %v", err)
	}
}

func TestEvacuationRollbackFailsWhenSourceAlreadyCompleted(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	f.sourceGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertFailedRollbackHeld(t, generation, "no longer runs its prior assignment", false)
	if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
		t.Fatalf("replacement capacity changed after a failed rollback: %+v", row)
	}
	// A later successor never sees a RUNNING row with a plain checkpoint.
	f.handOver(t, "rollback-later-successor")
	record, _ := f.store.GetTest(f.test.ID)
	restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: record.Status, Error: record.Error, Assignment: f.test.Assignment}
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	if f.tr.isRunAdoptionCandidate(restored) {
		t.Fatal("failed rollback became an ordinary adoption candidate")
	}
}

func TestEvacuationRollbackCapacityFailureKeepsJournal(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhasePrepared, false)
	f.spareGen.rejectCap = true
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertFailedRollbackHeld(t, generation, "evacuation capacity rollback", true)
	if row := f.ledger(t)["gen-b"]; row.State != "releasing" {
		t.Fatalf("unconfirmed release left ledger %+v", row)
	}
}

func TestEvacuationRollbackAcceptsPredecessorRestoredStandby(t *testing.T) {
	for _, committed := range []bool{true, false} {
		t.Run(map[bool]string{true: "committed", false: "pending"}[committed], func(t *testing.T) {
			f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, true, func(f *evacuationRollbackFixture) {
				predecessorRestoreStandby(t, f, committed)
			})
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if f.test.adoptedResume == nil || f.test.adoptedResume.finishStatus != "" {
				t.Fatalf("restored standby blocked the rollback: %+v", f.test.adoptedResume)
			}
			got := f.ledger(t)["gen-b"]
			if !standbyCapacityReservation(got) || got.Generation != generation || got.State != "committed" || got.Revision != f.claim.Revision+1 {
				t.Fatalf("restored standby = %+v", got)
			}
			_, capacity := f.spareGen.snapshot()
			for _, request := range capacity {
				if request.Release || len(request.GroupPeaks) != 0 || request.ExecutionGeneration != generation {
					t.Fatalf("restored standby received %+v", request)
				}
			}
			if want := map[bool]int{true: 1, false: 2}[committed]; len(capacity) != want {
				t.Fatalf("restored standby requests = %d, want %d", len(capacity), want)
			}
		})
	}
}

func TestRestoredRunEvacuationStandbyMatchesFrozenIdentity(t *testing.T) {
	standby := storage.PodCapacityReservation{ReservationID: "spare", Revision: 3, Target: "gen-b", TestID: "run", Incarnation: "pod-b", Generation: 4, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackBudget, Dedicated: true}
	predecessorRestored := standby
	predecessorRestored.Revision = 5
	if !restoredRunEvacuationStandby(predecessorRestored, standby) {
		t.Fatal("a predecessor's idle revision was not recognized")
	}
	for name, mutate := range map[string]func(*storage.PodCapacityReservation){
		"peaks":       func(r *storage.PodCapacityReservation) { r.GroupPeaks = map[string]int32{"ga": 1} },
		"incarnation": func(r *storage.PodCapacityReservation) { r.Incarnation = "pod-c" },
		"budget":      func(r *storage.PodCapacityReservation) { r.Budget.VUsers = 9; r.Demand.VUsers = 9 },
		"demand":      func(r *storage.PodCapacityReservation) { r.Demand = rollbackDemand },
		"shared":      func(r *storage.PodCapacityReservation) { r.Dedicated = false },
		"attempt":     func(r *storage.PodCapacityReservation) { r.ReservationID = "other" },
		"model":       func(r *storage.PodCapacityReservation) { r.ModelVersion = "other" },
		"headroom":    func(r *storage.PodCapacityReservation) { r.HeadroomPct = 7 },
		"placement": func(r *storage.PodCapacityReservation) {
			r.RecoveryPlacement = &storage.PodCapacityRecoveryPlacement{NodeName: "other"}
		},
		"run":    func(r *storage.PodCapacityReservation) { r.TestID = "other" },
		"target": func(r *storage.PodCapacityReservation) { r.Target = "gen-z" },
	} {
		row := standby
		mutate(&row)
		if restoredRunEvacuationStandby(row, standby) {
			t.Fatalf("%s change was accepted as the restored standby", name)
		}
	}
}

func TestEvacuationRollbackPanicKeepsJournal(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	f.spareGen.panicCap = true
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertFailedRollbackHeld(t, generation, "internal error while rolling back the evacuation", true)
}

func TestCleanSameLeaderRollbackRestoresPriorCheckpoint(t *testing.T) {
	for _, armed := range []bool{true, false} {
		t.Run(map[bool]string{true: "armed", false: "unarmed"}[armed], func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			tr.evacuationRollbackRecovery = true
			if armed {
				tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			}
			before := test.continuityCheckpoint
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
			if err != nil {
				t.Fatal(err)
			}
			guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
			if err != nil {
				t.Fatal(err)
			}
			tr.abandonRunEvacuationCheckpoint(test, guard, generation, owner)
			record, err := store.GetTest(test.ID)
			if err != nil || record.ContinuityCheckpoint != test.continuityCheckpoint {
				t.Fatalf("restored checkpoint not durable: %v", err)
			}
			if !armed {
				if record.ContinuityCheckpoint != "" {
					t.Fatalf("unarmed run gained a checkpoint: %s", record.ContinuityCheckpoint)
				}
				return
			}
			restored, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
			original, _, _ := decodeRunContinuityCheckpoint(before)
			if err != nil || !present || restored.Shards["ga"][0].Target != "gen-a" || !restored.ArmedAt.Equal(original.ArmedAt) {
				t.Fatalf("restored checkpoint = %+v, %v", restored, err)
			}
			// Later topology changes can be re-armed again.
			scaled := cloneGroupShardMap(previous)
			scaled["ga"][0].VUsers = 8
			tr.armRunContinuity(context.Background(), test, generation, owner, token, scaled)
			record, _ = store.GetTest(test.ID)
			if rearmed, _, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint); err != nil || rearmed.Shards["ga"][0].VUsers != 8 {
				t.Fatalf("re-arm after restore = %+v, %v", rearmed, err)
			}
		})
	}
}

func TestCleanRollbackLeavesOtherJournals(t *testing.T) {
	for _, mode := range []string{"version 3", "source fenced", "other term", "other owner"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			tr.evacuationRollbackRecovery = mode != "version 3"
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
			if err != nil {
				t.Fatal(err)
			}
			if guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"}); err != nil {
				t.Fatal(err)
			}
			if mode == "source fenced" {
				if guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner); err != nil {
					t.Fatal(err)
				}
			}
			abandonGeneration, abandonOwner := generation, owner
			switch mode {
			case "other term":
				abandonGeneration++
			case "other owner":
				abandonOwner = "another-leader"
			}
			tr.abandonRunEvacuationCheckpoint(test, guard, abandonGeneration, abandonOwner)
			record, err := store.GetTest(test.ID)
			if err != nil || record.ContinuityCheckpoint != guard {
				t.Fatalf("%s journal was replaced: %v", mode, err)
			}
		})
	}
}

func TestVersionFourJournalCarriesFailoverHistory(t *testing.T) {
	history := []RunFailover{{PriorGeneration: 1, Generation: 2, AdoptedAt: time.Now().UTC(), AdoptedGenerators: 1}}
	guard := runEvacuationCheckpoint{Version: runEvacuationRollbackCheckpointVersion, Source: "gen-a", Prior: runContinuityCheckpoint{Failovers: history}}
	raw, _ := json.Marshal(guard)
	test := &ManagedTest{ID: "history"}
	setContinuityCheckpointLocked(test, string(raw))
	if len(test.continuityFailovers) != 1 || test.continuityFailovers[0].Generation != 2 {
		t.Fatalf("failover history = %+v", test.continuityFailovers)
	}
}

// predecessorRestoreStandby applies the predecessor's own same-leader standby
// rollback (rollbackEvacuationPodClaims) under the old term.
func predecessorRestoreStandby(t *testing.T, f *evacuationRollbackFixture, committed bool) {
	t.Helper()
	idle := f.claim
	idle.Revision++
	idle.GroupPeaks, idle.GroupDemands = nil, nil
	idle.Demand = idle.Budget
	if err := f.store.ReservePodCapacityIfUnchanged([]storage.PodCapacityReservation{idle}, []storage.PodCapacityReservation{f.claim}, executionLeaseName, f.oldOwner, f.oldGen); err != nil {
		t.Fatal(err)
	}
	if committed {
		if err := f.store.TransitionPodCapacity(idle.Target, idle.TestID, idle.Incarnation, idle.ReservationID, "committed", executionLeaseName, f.oldOwner, f.oldGen); err != nil {
			t.Fatal(err)
		}
	}
}

// rollbackLedgerStore rewrites the capacity rows the successor reads and can
// refuse the final journal swap, so the rollback's own checks are exercised.
type rollbackLedgerStore struct {
	*storage.SQLiteStore
	rewrite       func(*storage.PodCapacityReservation)
	failPlainSwap bool
}

func (s *rollbackLedgerStore) ListPodCapacityReservations() ([]storage.PodCapacityReservation, error) {
	rows, err := s.SQLiteStore.ListPodCapacityReservations()
	if err == nil && s.rewrite != nil {
		for i := range rows {
			s.rewrite(&rows[i])
		}
	}
	return rows, err
}

func (s *rollbackLedgerStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner string) (storage.TestStatusTransitionResult, error) {
	if _, present, err := decodeRunContinuityCheckpoint(next); s.failPlainSwap && err == nil && present && interruptedRunEvacuation(expected) {
		return "", errors.New("injected journal swap failure")
	}
	return s.SQLiteStore.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner)
}

func TestEvacuationRollbackRefusesChangedLedger(t *testing.T) {
	cases := map[string]struct {
		standby  bool
		prepare  func(*testing.T, *evacuationRollbackFixture)
		rewrite  func(*storage.PodCapacityReservation)
		swapFail bool
		want     string
	}{
		"replacement changed": {rewrite: func(r *storage.PodCapacityReservation) {
			if r.Target == "gen-b" {
				r.Demand.VUsers++
			}
		}, want: "replacement reservation changed since the evacuation journal"},
		"replacement committed": {rewrite: func(r *storage.PodCapacityReservation) {
			if r.Target == "gen-b" {
				r.State = "committed"
			}
		}, want: "replacement reservation changed since the evacuation journal"},
		"standby newer term": {standby: true, prepare: func(t *testing.T, f *evacuationRollbackFixture) { predecessorRestoreStandby(t, f, true) }, rewrite: func(r *storage.PodCapacityReservation) {
			if r.Target == "gen-b" {
				r.Generation += 100
			}
		}, want: "standby reservation changed since the evacuation journal"},
		"replacement idle": {rewrite: func(r *storage.PodCapacityReservation) {
			if r.Target == "gen-b" {
				r.GroupPeaks = nil
			}
		}, want: "neither the replacement nor its standby"},
		"standby changed": {standby: true, prepare: func(t *testing.T, f *evacuationRollbackFixture) { predecessorRestoreStandby(t, f, true) }, rewrite: func(r *storage.PodCapacityReservation) {
			if r.Target == "gen-b" {
				r.Incarnation = "pod-c"
			}
		}, want: "standby reservation changed since the evacuation journal"},
		"standby releasing": {standby: true, prepare: func(t *testing.T, f *evacuationRollbackFixture) {
			predecessorRestoreStandby(t, f, true)
			if err := f.store.TransitionPodCapacity("gen-b", f.test.ID, f.claim.Incarnation, f.claim.ReservationID, "releasing", executionLeaseName, f.oldOwner, f.oldGen); err != nil {
				t.Fatal(err)
			}
		}, want: "standby reservation is releasing"},
		"journal swap fails": {swapFail: true, want: "record evacuation rollback"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var hooks []func(*evacuationRollbackFixture)
			if c.prepare != nil {
				hooks = append(hooks, func(f *evacuationRollbackFixture) { c.prepare(t, f) })
			}
			f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, c.standby, hooks...)
			f.tr.testStore = &rollbackLedgerStore{SQLiteStore: f.store, rewrite: c.rewrite, failPlainSwap: c.swapFail}
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertFailedRollbackHeld(t, generation, c.want, true)
			if _, capacity := f.spareGen.snapshot(); !c.swapFail && len(capacity) != 0 {
				t.Fatalf("changed ledger still reached the generator: %+v", capacity)
			}
		})
	}
}

func TestEvacuationRollbackResumesPredecessorRelease(t *testing.T) {
	for _, released := range []bool{false, true} {
		t.Run(map[bool]string{false: "releasing", true: "released"}[released], func(t *testing.T) {
			f := newEvacuationRollbackFixture(t, runEvacuationPhasePrepared, false, func(f *evacuationRollbackFixture) {
				// The predecessor's own rollback began or finished the release.
				if err := f.store.TransitionPodCapacity("gen-b", f.test.ID, "pod-b", f.claim.ReservationID, "releasing", executionLeaseName, f.oldOwner, f.oldGen); err != nil {
					t.Fatal(err)
				}
				if released {
					if err := f.store.TransitionPodCapacity("gen-b", f.test.ID, "pod-b", f.claim.ReservationID, "released", executionLeaseName, f.oldOwner, f.oldGen); err != nil {
						t.Fatal(err)
					}
				}
			})
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if f.test.adoptedResume == nil || f.test.adoptedResume.finishStatus != "" {
				t.Fatalf("predecessor release blocked the rollback: %+v", f.test.adoptedResume)
			}
			if _, kept := f.ledger(t)["gen-b"]; kept {
				t.Fatal("replacement reservation was not released")
			}
			_, capacity := f.spareGen.snapshot()
			if want := map[bool]int{false: 1, true: 0}[released]; len(capacity) != want {
				t.Fatalf("replacement capacity requests = %d, want %d", len(capacity), want)
			}
		})
	}
}

func TestRollbackRunEvacuationCapacityRefusesUnprovableTargets(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	owner, generation := f.tr.leadership.executionLeaseTerm()
	guard, replacements, err := runEvacuationRollbackPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.tr.rollbackRunEvacuationCapacity(context.Background(), guard, replacements, map[string]generatorClient{"gen-b": &adoptionFakeClient{}}, generation, owner); err == nil || !strings.Contains(err.Error(), "cannot confirm capacity changes") {
		t.Fatalf("client without capacity RPC = %v", err)
	}
	guard.CapacityClaims = nil
	if _, err := f.tr.rollbackRunEvacuationCapacity(context.Background(), guard, replacements, map[string]generatorClient{"gen-b": f.spareGen}, generation, owner); err == nil || !strings.Contains(err.Error(), "did not record") {
		t.Fatalf("unjournaled reservation = %v", err)
	}
	if _, capacity := f.spareGen.snapshot(); len(capacity) != 0 {
		t.Fatalf("unprovable targets reached the generator: %+v", capacity)
	}
}

func TestEvacuationRollbackAdoptsPausedRun(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	f.test.Status = "PAUSED"
	if !f.tr.isRunAdoptionCandidate(f.test) {
		t.Fatal("paused run is no rollback candidate")
	}
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if f.test.Status != "PAUSED" || f.test.adoptedResume == nil || f.test.adoptedResume.finishStatus != "" || f.test.adoptedResume.adopted["gen-a"] == nil {
		t.Fatalf("paused run after rollback = %s %+v", f.test.Status, f.test.adoptedResume)
	}
}

func TestFailedEvacuationRollbackIsNotRetriedInItsTerm(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	f.spareDown = true
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.tr.resumeInterruptedRunEvacuations()
	f.tr.terminalRetryMu.Lock()
	_, queued := f.tr.terminalRetrying["evacuation-guard:"+f.test.ID]
	f.tr.terminalRetryMu.Unlock()
	if queued {
		t.Fatal("same-term recovery was queued for a failed rollback")
	}
}

func TestVersionFourCommittedEvacuationIsPromoted(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.evacuationRollbackRecovery = true
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func(string) (string, error){
		func(raw string) (string, error) {
			return tr.markRunEvacuationStaged(context.Background(), test, raw, generation, owner, []string{"gen-b"})
		},
		func(raw string) (string, error) {
			return tr.markRunEvacuationSourceFenced(context.Background(), test, raw, generation, owner)
		},
		func(raw string) (string, error) {
			return tr.markRunEvacuationReplacementCommitted(context.Background(), test, raw, generation, owner)
		},
	} {
		if guard, err = step(guard); err != nil {
			t.Fatal(err)
		}
	}
	if _, committed := committedRunEvacuationCheckpoint(guard, test.ID); !committed {
		t.Fatal("version 4 committed journal lacks promotion proof")
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "v4-committed-successor")
	successor.tick()
	tr.leadership = successor
	tr.execution = newDistributedExecutionBackend([]string{"gen-b"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return &adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}, nil
	})
	if err := tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if test.adoptedResume == nil || test.adoptedResume.adopted["gen-b"] == nil {
		t.Fatalf("version 4 committed replacement not adopted: %+v", test.adoptedResume)
	}
}

func TestEvacuationRollbackUnusablePriorCheckpointHoldsRun(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false, func(f *evacuationRollbackFixture) {
		// A prior adoption intent out of order makes the planned adoption fail
		// for a reason other than key access.
		var guard runEvacuationCheckpoint
		if err := json.Unmarshal([]byte(f.guard), &guard); err != nil {
			t.Fatal(err)
		}
		guard.Prior.PendingAdoptions = []runAdoptionIntent{{Generation: guard.Generation, OwnerID: "earlier", PreparedAt: time.Now().UTC()}}
		raw := encodeEvacuationGuard(t, guard)
		if err := f.tr.swapRunContinuityCheckpoint(f.test, f.guard, raw, f.oldOwner); err != nil {
			t.Fatal(err)
		}
		f.guard = raw
	})
	if !f.tr.isRunAdoptionCandidate(f.test) {
		t.Fatal("journal is no rollback candidate")
	}
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.Contains(record.Error, "could not use the evacuation journal's prior checkpoint") || record.ContinuityCheckpoint != f.guard {
		t.Fatalf("unusable prior checkpoint = %+v, %v", record, err)
	}
	if f.test.adoptedResume != nil || f.test.executionCtx != nil {
		t.Fatal("an unusable rollback bound an adoption outcome")
	}
	if claims, _ := f.sourceGen.snapshot(); len(claims) != 0 {
		t.Fatalf("source was claimed without a usable checkpoint: %+v", claims)
	}
}

func TestEvacuationRollbackFailurePanicHoldsRun(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	f.spareDown = true
	f.sourceGen.panicStop = true
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.Contains(record.Error, "internal error while rolling back the evacuation") {
		t.Fatalf("panicking rollback failure = %+v, %v", record, err)
	}
	guard, _ := f.durableGuard(t)
	if guard.Phase != runEvacuationPhaseRollingBack || f.test.adoptedResume != nil || f.test.executionCtx != nil {
		t.Fatalf("panicking rollback failure lost its hold: phase %q resume %+v", guard.Phase, f.test.adoptedResume)
	}
}

func TestRunEvacuationCapacityReturnedReadsTheLedger(t *testing.T) {
	ordinary := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	claims := []storage.PodCapacityReservation{ordinary.claim}
	if ordinary.tr.runEvacuationCapacityReturned(ordinary.test.ID, claims, nil) {
		t.Fatal("a pending replacement reservation counted as returned")
	}
	owner, generation := ordinary.tr.leadership.executionLeaseTerm()
	for _, state := range []string{"releasing", "released"} {
		if err := ordinary.store.TransitionPodCapacity("gen-b", ordinary.test.ID, "pod-b", ordinary.claim.ReservationID, state, executionLeaseName, owner, generation); err != nil {
			t.Fatal(err)
		}
		if returned := ordinary.tr.runEvacuationCapacityReturned(ordinary.test.ID, claims, nil); returned != (state == "released") {
			t.Fatalf("%s replacement reservation returned = %v", state, returned)
		}
	}

	standby := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, true, func(f *evacuationRollbackFixture) {
		predecessorRestoreStandby(t, f, false)
	})
	preImages := map[string]storage.PodCapacityReservation{"gen-b": *standby.standby}
	if standby.tr.runEvacuationCapacityReturned(standby.test.ID, []storage.PodCapacityReservation{standby.claim}, preImages) {
		t.Fatal("a pending idle standby counted as returned")
	}
	owner, generation = standby.tr.leadership.executionLeaseTerm()
	if err := standby.store.TransitionPodCapacity("gen-b", standby.test.ID, "pod-b", standby.claim.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if !standby.tr.runEvacuationCapacityReturned(standby.test.ID, []storage.PodCapacityReservation{standby.claim}, preImages) {
		t.Fatal("a committed idle standby did not count as returned")
	}
}

func TestAbandonedEvacuationJournalIsNeverRolledBack(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.evacuationRollbackRecovery = true
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	before := test.continuityCheckpoint
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
	if err != nil {
		t.Fatal(err)
	}
	marked := tr.markRunEvacuationCheckpointAbandoned(test, guard, generation, owner)
	var decoded runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(marked), &decoded); err != nil || decoded.AbandonedAt.IsZero() || decoded.Phase != runEvacuationPhasePrepared {
		t.Fatalf("abandoned journal = %+v, %v", decoded, err)
	}
	if record, _ := store.GetTest(test.ID); record.ContinuityCheckpoint != marked {
		t.Fatal("abandoned marker is not durable")
	}
	if _, _, err := runEvacuationRollbackPlan(marked, test.ID); err == nil || !strings.Contains(err.Error(), "abandoned by its leader") {
		t.Fatalf("abandoned journal plan = %v", err)
	}
	if _, _, err := interruptedEvacuationTargets(marked, test.ID); err != nil {
		t.Fatalf("abandoned journal lost its fail-closed participants: %v", err)
	}
	if again := tr.markRunEvacuationCheckpointAbandoned(test, marked, generation, owner); again != marked {
		t.Fatal("an abandoned journal was marked twice")
	}
	// Once the capacity is back, the leader still restores the prior checkpoint.
	tr.abandonRunEvacuationCheckpoint(test, marked, generation, owner)
	record, _ := store.GetTest(test.ID)
	restored, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	original, _, _ := decodeRunContinuityCheckpoint(before)
	if err != nil || !present || restored.Shards["ga"][0].Target != "gen-a" || !restored.ArmedAt.Equal(original.ArmedAt) {
		t.Fatalf("abandoned journal was not restored: %+v, %v", restored, err)
	}
}

func TestFailedEvacuationRollbackWaitsWhenTermIsUnknown(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	f.spareDown = true
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	// A replica that cannot read its own term must not fence either.
	f.tr.leadership = newExecutionLeadership(f.store, "unknown-term")
	f.tr.resumeInterruptedRunEvacuations()
	f.tr.terminalRetryMu.Lock()
	_, queued := f.tr.terminalRetrying["evacuation-guard:"+f.test.ID]
	f.tr.terminalRetryMu.Unlock()
	if queued {
		t.Fatal("recovery was queued without a readable term")
	}
}

func TestRollingBackJournalRejectsContradictions(t *testing.T) {
	f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
	owner, generation := f.tr.leadership.executionLeaseTerm()
	guard, _, err := runEvacuationRollbackPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	planned, _, err := f.tr.planRunAdoptionIntent(context.Background(), f.test.ID, guard.Prior, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	rolling, raw, err := f.tr.persistRunEvacuationRollbackIntent(f.test, f.guard, guard, planned, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := interruptedEvacuationTargets(raw, f.test.ID); err != nil {
		t.Fatalf("valid rolling_back journal rejected: %v", err)
	}
	if _, _, err := runEvacuationRollbackPlan(raw, f.test.ID); err != nil {
		t.Fatalf("valid rolling_back journal rejected for rollback: %v", err)
	}
	for name, mutate := range map[string]func(*runEvacuationCheckpoint){
		"version 3":          func(g *runEvacuationCheckpoint) { g.Version = runEvacuationCheckpointVersion },
		"rollback term":      func(g *runEvacuationCheckpoint) { g.RollbackGeneration = g.Generation },
		"rollback owner":     func(g *runEvacuationCheckpoint) { g.RollbackOwnerID = "" },
		"rollback start":     func(g *runEvacuationCheckpoint) { g.RollbackStartedAt = time.Time{} },
		"staged targets":     func(g *runEvacuationCheckpoint) { g.StagedTargets = []string{"gen-z"} },
		"unstaged targets":   func(g *runEvacuationCheckpoint) { g.StagedAt = time.Time{} },
		"source fenced":      func(g *runEvacuationCheckpoint) { g.SourceFencedAt = time.Now().UTC() },
		"replacement commit": func(g *runEvacuationCheckpoint) { g.ReplacementCommittedAt = time.Now().UTC() },
	} {
		t.Run(name, func(t *testing.T) {
			g := rolling
			g.StagedTargets = append([]string(nil), rolling.StagedTargets...)
			mutate(&g)
			encoded := encodeEvacuationGuard(t, g)
			if _, _, err := interruptedEvacuationTargets(encoded, f.test.ID); err == nil || !strings.Contains(err.Error(), "rolling-back evacuation has contradictory progress") {
				t.Fatalf("fail-closed recovery accepted it: %v", err)
			}
			if _, _, err := runEvacuationRollbackPlan(encoded, f.test.ID); err == nil {
				t.Fatal("rollback accepted it")
			}
		})
	}
}
