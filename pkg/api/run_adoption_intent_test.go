package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

type adoptionIntentClient struct {
	*adoptionFakeClient
	claim func(uint64, []generatorrpc.RunAdoption)
}

func (c *adoptionIntentClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, g uint64, owner string, ttl time.Duration, a []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	c.claim(g, a)
	return c.adoptionFakeClient.ClaimExecutionEpochWithAdoptions(ctx, g, owner, ttl, a)
}

func TestAdoptionIntentSurvivesInterruptedController(t *testing.T) {
	tr, store, leader := newAdoptionTestRunner(t)
	run := storedContinueRun(t, tr, store, "intent-restart", "continue")
	prior, _, _ := decodeRunContinuityCheckpoint(run.continuityCheckpoint)
	owner, generation := leader.executionLeaseTerm()
	planned, first, err := tr.planRunAdoptionIntent(context.Background(), run.ID, prior, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.persistRunAdoptionIntent(run, run.continuityCheckpoint, planned, owner); err != nil {
		t.Fatal(err)
	}
	repeated, retry, err := tr.planRunAdoptionIntent(context.Background(), run.ID, planned, generation, owner)
	if err != nil || len(repeated.PendingAdoptions) != 1 || !bytes.Equal(first.NewToken, retry.NewToken) {
		t.Fatal("retry lost its durable rotation")
	}
	// Discard in-memory state at the RPC/ack boundary; only the database survives.
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	next := newExecutionLeadership(store, "intent-successor")
	next.tick()
	tr.leadership = next
	record, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{ID: run.ID, Name: run.Name, Status: "RUNNING", Assignment: run.Assignment}
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	tr.tests[run.ID] = restored
	calls := 0
	client := &adoptionIntentClient{adoptionFakeClient: &adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}}
	client.claim = func(g uint64, a []generatorrpc.RunAdoption) {
		calls++
		if len(a) != 1 || len(a[0].PriorProofs) != 1 || a[0].PriorProofs[0].Generation != generation || !bytes.Equal(a[0].PriorProofs[0].Token, first.NewToken) {
			t.Fatal("successor lost interrupted proof")
		}
		saved, err := store.GetTest(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		cp, _, err := decodeRunContinuityCheckpoint(saved.ContinuityCheckpoint)
		if err != nil || len(cp.PendingAdoptions) != 2 || cp.PendingAdoptions[1].Generation != g {
			t.Fatal("RPC preceded durable intent")
		}
		opened, err := openRunSupervisionToken(context.Background(), tr.continuityKeyWrapper, run.ID, runContinuityCheckpoint{ExecutionGeneration: g, RunSupervisionToken: cp.PendingAdoptions[1].Token})
		if err != nil || !bytes.Equal(opened, a[0].NewToken) {
			t.Fatal("persisted token differs from RPC")
		}
	}
	tr.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	if err := tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || restored.adoptedResume == nil || restored.adoptedResume.finishStatus != "" {
		t.Fatal("successor did not adopt")
	}
	saved, _ := store.GetTest(run.ID)
	cp, _, _ := decodeRunContinuityCheckpoint(saved.ContinuityCheckpoint)
	if len(cp.PendingAdoptions) != 0 || cp.ExecutionGeneration != generation+1 {
		t.Fatal("confirmed adoption did not compact intent history")
	}
	public, _ := json.Marshal(cloneManagedTestFieldsLocked(restored))
	if strings.Contains(string(public), "pending_adoptions") || strings.Contains(string(public), "run_supervision_token") {
		t.Fatal("private intent exposed")
	}
}

func TestAdoptionIntentCASFailurePreventsRPC(t *testing.T) {
	tr, store, leader := newAdoptionTestRunner(t)
	run := storedContinueRun(t, tr, store, "intent-cas", "continue")
	tr.tests[run.ID] = run
	owner, _ := leader.executionLeaseTerm()
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(run.ID, "changed", executionLeaseName, owner); err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatal(err)
	}
	calls := 0
	client := &adoptionIntentClient{adoptionFakeClient: &adoptionFakeClient{}, claim: func(uint64, []generatorrpc.RunAdoption) { calls++ }}
	tr.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	if err := tr.adoptOrphanedRuns(); err == nil || calls != 0 {
		t.Fatalf("CAS failure sent RPC: %v calls=%d", err, calls)
	}
	saved, _ := store.GetTest(run.ID)
	if saved.ContinuityCheckpoint != "changed" {
		t.Fatal("stale intent overwrote checkpoint")
	}
}

func TestAdoptionIntentRejectsInvalidHistory(t *testing.T) {
	for _, mode := range []string{"owner", "order", "oversized", "ciphertext", "future", "wrong run"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, leader := newAdoptionTestRunner(t)
			run := storedContinueRun(t, tr, store, "intent-invalid", "continue")
			cp, _, _ := decodeRunContinuityCheckpoint(run.continuityCheckpoint)
			owner, g := leader.executionLeaseTerm()
			cp, _, err := tr.planRunAdoptionIntent(context.Background(), run.ID, cp, g, owner)
			if err != nil {
				t.Fatal(err)
			}
			id := run.ID
			switch mode {
			case "owner":
				cp.PendingAdoptions[0].OwnerID = "other"
			case "order":
				cp.PendingAdoptions = append(cp.PendingAdoptions, cp.PendingAdoptions[0])
			case "oversized":
				for len(cp.PendingAdoptions) <= generatorrpc.MaxRunAdoptionPriorProofs {
					cp.PendingAdoptions = append(cp.PendingAdoptions, cp.PendingAdoptions[0])
				}
			case "ciphertext":
				cp.PendingAdoptions[0].Token = keywrap.WrappedKey{}
			case "future":
				cp.PendingAdoptions[0].Generation = g + 1
			case "wrong run":
				id = "another"
			}
			if _, _, err := tr.planRunAdoptionIntent(context.Background(), id, cp, g, owner); err == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
}

func TestAdoptionIntentBoundsUnconfirmedTakeovers(t *testing.T) {
	tr, store, _ := newAdoptionTestRunner(t)
	run := storedContinueRun(t, tr, store, "intent-bound", "continue")
	cp, _, _ := decodeRunContinuityCheckpoint(run.continuityCheckpoint)
	for i := 1; i <= generatorrpc.MaxRunAdoptionPriorProofs; i++ {
		var err error
		cp, _, err = tr.planRunAdoptionIntent(context.Background(), run.ID, cp, uint64(i), fmt.Sprintf("owner-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := tr.planRunAdoptionIntent(context.Background(), run.ID, cp, uint64(generatorrpc.MaxRunAdoptionPriorProofs+1), "too-many"); err == nil {
		t.Fatal("unbounded adoption history")
	}
}
