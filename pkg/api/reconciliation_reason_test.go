package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestReconciliationReportsInterruptedOwnershipWithoutClaimingProcessRestart(t *testing.T) {
	for _, status := range []string{"INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT"} {
		t.Run(status, func(t *testing.T) {
			runner, store, _ := newAdoptionTestRunner(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			row := &storage.TestRecord{ID: "interrupted", Name: "Interrupted", Status: status, StartTime: now}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			test := managedTestFromRecord(row)
			// The same running controller reconciles the durable row. Reconciliation
			// cannot infer a process restart from interruption of execution ownership.
			if err := runner.reconcileStoredActiveTest(test); err != nil {
				t.Fatal(err)
			}
			durable, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, detail := range []string{test.Error, durable.Error} {
				if strings.Contains(detail, "restarted") || !strings.Contains(detail, "execution ownership was interrupted") {
					t.Fatalf("misleading interruption detail: %q", detail)
				}
				if status == "SHAKEOUT" && !strings.Contains(detail, "not replayed") {
					t.Fatalf("shakeout ambiguity missing: %q", detail)
				}
			}
			if test.Status != "FAILED" || durable.Status != "FAILED" || durable.EndTime == nil {
				t.Fatal("interrupted execution was not failed durably")
			}
		})
	}
}
