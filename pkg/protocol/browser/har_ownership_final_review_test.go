package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHAROwnershipTerminalPlanRetiresUnchangedBatchAndRejectsExternalReplacement(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged_batch", true: "external_replacement"}[replace], func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < 2; i++ {
				o, n, _ := fixtureHARJoinedRetirement(t, dir)
				if err := n.close(); err != nil {
					t.Fatal(err)
				}
				if err := o.closeDescriptors(); err != nil {
					t.Fatal(err)
				}
				o.releaseReservation()
			}
			plans, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 2)
			if err != nil || len(plans) != 2 {
				t.Fatal("expected two recorded terminal payloads", err, len(plans))
			}
			if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
				t.Fatal("first authorized retirement failed", err)
			}
			if replace {
				path := filepath.Join(dir, runtimeHARWorkingDirectory, harRetirementLedgerName)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, path+".external"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err = PruneRuntimeHARWorkingCandidate(context.Background(), plans[1])
			if replace {
				if err == nil {
					t.Fatal("external replacement was adopted as an authorized batch mutation")
				}
				if _, err := os.Stat(plans[1].Path); err != nil {
					t.Fatal("externally replaced ledger permitted payload deletion", err)
				}
			} else {
				if err != nil {
					t.Fatal("batch's own durable ledger update invalidated untouched authorized payload", err)
				}
				if _, err := os.Stat(plans[1].Path); !os.IsNotExist(err) {
					t.Fatal("second terminal payload survived", err)
				}
			}
		})
	}
}

func TestHAROwnershipTeardownUsesOneOverallBudget(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	writeOwnedHARFixture(t, o)
	frame := &browserFinalizationHeldFrame{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
	native := &harFinalizationCloseGate{frame: frame, gate: make(chan struct{})}
	var gateOnce sync.Once
	release := func() { gateOnce.Do(func() { close(native.gate) }) }
	o.beginWriter()
	o.bindWriter(func() error { return native.Close() })
	b := &Browser{harOwner: o, harWorkingPath: o.path, harCapturePolicy: HARCaptureAlways, pwContext: native, evidence: browserEvidenceState{
		finalizer: "synthetic", lifecycleContext: context.Background(), documents: map[string]*browserDocumentState{"held": {frame: frame, page: &browserGenerationUntouchedPage{}, observations: map[string]protocol.BrowserDocumentObservation{}}}}}
	done := make(chan struct{})
	result := make(chan error, 1)
	start := time.Now()
	go func() { defer close(done); result <- b.Teardown(context.Background()) }()
	defer func() { release(); <-done; _, _ = o.waitFinish(context.Background()) }()
	<-frame.entered
	select {
	case err := <-result:
		if !errors.Is(err, errHAROwnershipUnproven) {
			t.Error("overall deadline became successful teardown", err)
		}
		if elapsed := time.Since(start); elapsed > runtimeHARCloseWait+time.Second {
			t.Error("teardown spent fresh per-phase budgets", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Error("owned overall teardown never returned")
	}
	if o.retiredSuccessfully() {
		t.Error("deadline released unfinished document/native work")
	}
}

func TestHAROwnershipNamespaceRejectsWorkDirectoryReplacementAfterLockWait(t *testing.T) {
	dir := t.TempDir()
	first, err := lockHARNamespace(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	path, parent, err := openHARPublishedRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	work, err := openHARPrivateChild(parent, runtimeHARWorkingDirectory, false)
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	file, err := openHARRegular(work, "namespace.lock", false)
	if err != nil {
		_ = work.Close()
		_ = parent.Close()
		t.Fatal(err)
	}
	waiter := &harNamespace{path: path, root: parent, work: work, file: file}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	result := make(chan error, 1)
	done := make(chan struct{})
	entered := make(chan struct{})
	go func() { defer close(done); close(entered); result <- waiter.acquire(ctx) }()
	defer func() { cancel(); <-done; _ = waiter.close() }()
	<-entered
	old := runtimeHARWorkingDirectory + "-preserved"
	if err := first.root.Rename(runtimeHARWorkingDirectory, old); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		// Windows refuses to rename a directory while a handle to one of
		// its children is open (both namespace.lock handles are), so the
		// replacement cannot happen: the waiter takes the unchanged
		// namespace once the holder closes.
		t.Logf("replacing the held namespace directory was refused: %v", err)
		if err := first.close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("waiter refused the unchanged namespace: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("namespace waiter never acquired the unchanged namespace")
		}
		return
	}
	if err := first.root.Mkdir(runtimeHARWorkingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	replacement, err := first.root.OpenRoot(runtimeHARWorkingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeHARPrivateDurable(replacement, "namespace.lock", nil); err != nil {
		_ = replacement.Close()
		t.Fatal(err)
	}
	_ = replacement.Close()
	if err := first.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, errHAROwnershipUnproven) {
			t.Error("waiter accepted a replaced namespace directory", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("namespace waiter never completed its error receipt")
	}
	for _, name := range []string{old, runtimeHARWorkingDirectory} {
		if _, err := os.Stat(filepath.Join(dir, name, "namespace.lock")); err != nil {
			t.Fatal("replaced/old namespace was deleted", name, err)
		}
	}
}

func TestHAROwnershipMixedPlanRetiresOwnMutationsAndRejectsExternalReplacement(t *testing.T) {
	for _, unrecordedFirst := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			name := map[bool]string{false: "recorded_first", true: "unrecorded_first"}[unrecordedFirst] + "/" + map[bool]string{false: "unchanged", true: "external_replacement"}[replace]
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				recorded, namespace, _ := fixtureHARJoinedRetirement(t, dir)
				if err := namespace.close(); err != nil {
					t.Fatal(err)
				}
				if err := recorded.closeDescriptors(); err != nil {
					t.Fatal(err)
				}
				recorded.releaseReservation()
				unrecorded := fixtureHAROwner(t, dir)
				writeOwnedHARFixture(t, unrecorded)
				unrecorded.startClose()
				<-unrecorded.closeDone
				if unrecorded.closeErr != nil {
					t.Fatal(unrecorded.closeErr)
				}
				if err := unrecorded.closeDescriptors(); err != nil {
					t.Fatal(err)
				}
				unrecorded.releaseReservation()
				plans, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 2)
				if err != nil || len(plans) != 2 {
					t.Fatal("expected recorded and unrecorded joined candidates", err, len(plans))
				}
				if (plans[0].terminal == nil) != unrecordedFirst {
					plans[0], plans[1] = plans[1], plans[0]
				}
				if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
					t.Fatal("first proven own retirement failed", err)
				}
				if replace {
					path := filepath.Join(dir, runtimeHARWorkingDirectory, harRetirementLedgerName)
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(path, path+".external"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				err = PruneRuntimeHARWorkingCandidate(context.Background(), plans[1])
				if replace {
					if err == nil {
						t.Error("mixed plan adopted an external replacement")
					}
					if _, err := os.Stat(plans[1].Path); err != nil {
						t.Error("external ledger replacement authorized payload deletion", err)
					}
				} else {
					if err != nil {
						t.Error("mixed plan invalidated its own verified ledger mutation", err)
					}
					if _, err := os.Stat(plans[1].Path); !os.IsNotExist(err) {
						t.Error("mixed authorized batch left joined payload", err)
					}
				}
			})
		}
	}
}

func TestHAROwnershipUnrecordedPlanStartsAbsentAndRejectsExternalReplacement(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "own_mutations", true: "external_replacement"}[replace], func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < 2; i++ {
				owner := fixtureHAROwner(t, dir)
				writeOwnedHARFixture(t, owner)
				owner.startClose()
				<-owner.closeDone
				if owner.closeErr != nil {
					t.Fatal(owner.closeErr)
				}
				if err := owner.closeDescriptors(); err != nil {
					t.Fatal(err)
				}
				owner.releaseReservation()
			}
			ledgerPath := filepath.Join(dir, runtimeHARWorkingDirectory, harRetirementLedgerName)
			if _, err := os.Stat(ledgerPath); !os.IsNotExist(err) {
				t.Fatal("fixture must begin without a ledger", err)
			}
			plans, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 2)
			if err != nil || len(plans) != 2 {
				t.Fatal("expected two unrecorded joined generations", err, len(plans))
			}
			secondInfo, err := os.Stat(plans[1].Path)
			if err != nil {
				t.Fatal(err)
			}
			if !plans[1].MatchesFileIdentity(secondInfo) || plans[1].MatchesFileIdentity(nil) {
				t.Fatal("candidate identity comparison lost its exact payload")
			}
			otherInfo, err := os.Stat(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if plans[1].MatchesFileIdentity(otherInfo) {
				t.Fatal("candidate identity comparison adopted a different payload")
			}
			if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
				t.Fatal("absent-ledger plan refused its own first retirement", err)
			}
			if replace {
				data, err := os.ReadFile(ledgerPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(ledgerPath, ledgerPath+".external"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(ledgerPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err = PruneRuntimeHARWorkingCandidate(context.Background(), plans[1])
			if replace {
				if err == nil {
					t.Error("unrecorded plan adopted external ledger replacement")
				}
				if _, err := os.Stat(plans[1].Path); err != nil {
					t.Error("external replacement deleted an unrecorded payload", err)
				}
			} else {
				if err != nil {
					t.Error("unrecorded plan refused its own empty-ledger mutation", err)
				}
				if _, err := os.Stat(plans[1].Path); !os.IsNotExist(err) {
					t.Error("second unrecorded joined payload survived", err)
				}
			}
		})
	}
}
