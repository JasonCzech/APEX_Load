package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestStartInitialProvenanceWinner(t *testing.T) {
	store, _ := newDeploymentLeaseTestStores(t)
	exerciseStartInitialProvenanceWinner(t, store)
}

func exerciseStartInitialProvenanceWinner(t *testing.T, store *SQLiteStore) {
	for _, prior := range []bool{false, true} {
		t.Run(fmt.Sprintf("prior-%t", prior), func(t *testing.T) {
			initial := map[string]any{"total_vusers": float64(7), "legacy": true}
			candidate := map[string]any{"total_vusers": float64(9), "policy": map[string]any{"hash": "candidate"}}
			record := &TestRecord{ID: fmt.Sprintf("start-winner-%t", prior), Name: "winner", Status: "QUEUED", ProjectID: DefaultProjectID, StartTime: time.Now().UTC()}
			if prior {
				record.InitialProvenance = initial
			}
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			record.Status = "INITIALIZING"
			record.InitialProvenance = candidate
			if err := store.SaveTestStartIfNoDeploymentLease(record, DefaultDeploymentLeaseName, "", ""); err != nil {
				t.Fatal(err)
			}
			want := candidate
			if prior {
				want = initial
			}
			if !reflect.DeepEqual(record.InitialProvenance, want) {
				t.Fatalf("winning receipt = %#v, want %#v", record.InitialProvenance, want)
			}
			// The returned decoded receipt is owned, with no reference to caller input
			// or durable metadata. Mutating it cannot change the database's winner.
			record.InitialProvenance.(map[string]any)["total_vusers"] = float64(99)
			durable, err := store.GetTest(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(durable.InitialProvenance, want) {
				t.Fatal("returned receipt aliases input or durable winner")
			}
		})
	}
}

func TestStartInitialProvenanceFailurePreservesCandidateAndQueuedRow(t *testing.T) {
	store, _ := newDeploymentLeaseTestStores(t)
	exerciseStartInitialProvenanceFailure(t, store)
}

func exerciseStartInitialProvenanceFailure(t *testing.T, store *SQLiteStore) {
	record := &TestRecord{ID: "start-corrupt-winner", Name: "corrupt winner", Status: "QUEUED", ProjectID: DefaultProjectID, StartTime: time.Now().UTC()}
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	// Corruption is injected below the normal encoder to qualify transactional
	// read/decode failure. The execution-capable write must roll back completely.
	if _, err := store.db.Exec(`UPDATE tests SET initial_provenance_json = ? WHERE id = ?`, `{invalid`, record.ID); err != nil {
		t.Fatal(err)
	}
	candidate := map[string]any{"total_vusers": float64(9)}
	record.InitialProvenance = candidate
	record.Status = "INITIALIZING"
	if err := store.SaveTestStartIfNoDeploymentLease(record, DefaultDeploymentLeaseName, "", ""); err == nil {
		t.Fatal("corrupt prior receipt admitted")
	}
	if !reflect.DeepEqual(record.InitialProvenance, candidate) {
		t.Fatal("failed transaction changed caller receipt")
	}
	var status, raw string
	if err := store.db.QueryRow(`SELECT status, initial_provenance_json FROM tests WHERE id = ?`, record.ID).Scan(&status, &raw); err != nil {
		t.Fatal(err)
	}
	if status != "QUEUED" || raw != "{invalid" {
		t.Fatalf("failed receipt read committed Start: %s %s", status, raw)
	}
	if _, err := store.db.Exec(`UPDATE tests SET initial_provenance_json = NULL WHERE id = ?`, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AcquireDeploymentLease(DefaultDeploymentLeaseName, "winner-owner", "winner-token", time.Minute, DeploymentLeaseContext{}, false); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(candidate)
	if err := store.SaveTestStartIfNoDeploymentLease(record, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrDeploymentLeaseActive) {
		t.Fatalf("lease failure = %v", err)
	}
	after, _ := json.Marshal(record.InitialProvenance)
	if string(before) != string(after) {
		t.Fatal("failed lease write changed receipt")
	}
}

// Exercise both outcomes of the public HA Start result contract against real
// storage. Lease refusal must neither adopt a prior receipt into the caller's
// candidate nor change the queued durable row.
func TestStartInitialProvenanceExecutionLeaseResults(t *testing.T) {
	store, _ := newDeploymentLeaseTestStores(t)
	exerciseStartInitialProvenanceExecutionLease(t, store)
}

func exerciseStartInitialProvenanceExecutionLease(t *testing.T, store *SQLiteStore) {
	t.Helper()
	for _, leaseState := range []string{"owned", "wrong-owner", "expired"} {
		t.Run(leaseState, func(t *testing.T) {
			prior := map[string]any{"total_vusers": float64(7), "legacy": true}
			candidate := map[string]any{"total_vusers": float64(9), "policy": map[string]any{"hash": "candidate"}}
			record := &TestRecord{
				ID: "start-execution-receipt-" + leaseState, Name: "execution receipt", Status: "QUEUED",
				ProjectID: DefaultProjectID, StartTime: time.Now().UTC(), InitialProvenance: prior,
			}
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			before, err := store.GetTest(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			leaseName := "start-execution-receipt-lease-" + leaseState
			const leaseOwner = "receipt-owner/current-term"
			if acquired, err := store.TryAcquireSchedulerLease(leaseName, leaseOwner, time.Now().UTC(), time.Minute); err != nil || !acquired {
				t.Fatalf("acquire execution lease = %t, %v", acquired, err)
			}
			startOwner := leaseOwner
			if leaseState == "wrong-owner" {
				startOwner = "receipt-owner/stale-term"
			}
			if leaseState == "expired" {
				// Release expires the real stored row, preserving its owner and
				// generation. This avoids a timing-sensitive wall-clock sleep.
				if err := store.ReleaseSchedulerLease(leaseName, leaseOwner); err != nil {
					t.Fatal(err)
				}
				lease, err := store.GetSchedulerLease(leaseName)
				if err != nil || lease.OwnerID != leaseOwner || !lease.ExpiresAt.Equal(lease.UpdatedAt) {
					t.Fatalf("execution lease was not durably expired: %#v %v", lease, err)
				}
			}
			record.Status = "INITIALIZING"
			record.InitialProvenance = candidate
			saved, err := store.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(
				record, leaseName, startOwner, DefaultDeploymentLeaseName, "", "",
			)
			wantSaved := leaseState == "owned"
			if err != nil || saved != wantSaved {
				t.Fatalf("execution Start = %t, %v; want %t, nil", saved, err, wantSaved)
			}
			durable, err := store.GetTest(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !wantSaved {
				if !reflect.DeepEqual(record.InitialProvenance, candidate) {
					t.Fatal("refused execution lease changed the caller candidate")
				}
				if durable.Status != "QUEUED" || !reflect.DeepEqual(durable, before) {
					t.Fatalf("refused execution lease changed the queued durable row: %#v", durable)
				}
				return
			}
			if durable.Status != "INITIALIZING" || !reflect.DeepEqual(record.InitialProvenance, prior) || !reflect.DeepEqual(durable.InitialProvenance, prior) {
				t.Fatalf("owned execution Start did not adopt the durable winner: caller=%#v durable=%#v", record.InitialProvenance, durable)
			}
			// Successful adoption returns a private decoded value, never an alias
			// of the candidate, prior caller map, or immutable durable receipt.
			record.InitialProvenance.(map[string]any)["total_vusers"] = float64(99)
			if !reflect.DeepEqual(prior, before.InitialProvenance) || candidate["total_vusers"] != float64(9) {
				t.Fatal("execution Start result aliases an input receipt")
			}
			durable, err = store.GetTest(record.ID)
			if err != nil || !reflect.DeepEqual(durable.InitialProvenance, prior) {
				t.Fatalf("execution Start result aliases durable metadata: %#v %v", durable, err)
			}
		})
	}
}
