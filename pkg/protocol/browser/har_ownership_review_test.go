package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

type harDetachFixture struct {
	playwright.CDPSession
	detach func() error
}

func (d *harDetachFixture) Detach() error { return d.detach() }

func TestHAROwnershipBlockedDetachRetainsExactJoinAndReturnsDeadline(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	entered, gate := make(chan struct{}), make(chan struct{})
	b := &Browser{harOwner: o, harWorkingPath: o.path, harCapturePolicy: HARCaptureAlways, pwCDP: &harDetachFixture{detach: func() error { close(entered); <-gate; return nil }}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- b.Teardown(ctx) }()
	<-entered
	select {
	case err := <-result:
		if !errors.Is(err, errHAROwnershipUnproven) {
			t.Error("blocked detach did not report incomplete ownership", err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Error("blocked SDK detach prevented Teardown deadline")
	}
	if o.retiredSuccessfully() {
		t.Error("blocked detach retired physical owner")
	}
	if _, err := os.Stat(o.path); err != nil {
		t.Error("raw removed before detach joined", err)
	}
	close(gate)
	if _, err := o.waitFinish(context.Background()); err != nil {
		t.Fatal("late detach join did not retire owner", err)
	}
}

func TestHAROwnershipGoexitNeverInventsCompletion(t *testing.T) {
	for _, kind := range []string{"closer", "redactor", "runtime_join"} {
		t.Run(kind, func(t *testing.T) {
			o := fixtureHAROwner(t, t.TempDir())
			writeOwnedHARFixture(t, o)
			switch kind {
			case "closer":
				o.beginWriter()
				o.bindWriter(func() error { runtime.Goexit(); return nil })
				o.startFinish(false, nil)
			case "redactor":
				o.startFinish(true, func(string) string { runtime.Goexit(); return "" })
			case "runtime_join":
				o.startRuntimeJoin(func() { runtime.Goexit() })
				o.startFinish(false, nil)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if _, err := o.waitFinish(ctx); !errors.Is(err, errHAROwnershipUnproven) {
				t.Error("Goexit became successful finalization", err)
			}
			select {
			case <-o.finishDone:
			case <-time.After(150 * time.Millisecond):
				t.Error("Goexit did not finish its error receipt")
			}
			select {
			case <-o.closeDone:
			case <-time.After(150 * time.Millisecond):
				t.Error("Goexit stranded exact closer receipt")
			}
			if o.retiredSuccessfully() {
				t.Error("Goexit released physical ownership")
			}
			if _, err := os.Stat(o.path); err != nil {
				t.Error("Goexit permitted raw cleanup without join proof", err)
			}
		})
	}
}

func TestHAROwnershipInterruptedRetirementResumesOnlyBoundGeneration(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "replacement"}[replace], func(t *testing.T) {
			dir := t.TempDir()
			o := fixtureHAROwner(t, dir)
			writeOwnedHARFixture(t, o)
			o.startClose()
			<-o.closeDone
			if o.closeErr != nil {
				t.Fatal(o.closeErr)
			}
			if err := o.closeDescriptors(); err != nil {
				t.Fatal(err)
			}
			o.releaseReservation()
			retired := filepath.Join(dir, runtimeHARWorkingDirectory, ".retired-"+o.generation)
			if err := os.Rename(filepath.Dir(o.path), retired); err != nil {
				t.Fatal(err)
			}
			plans := PlanRuntimeHARWorkingCleanup(dir, 1)
			if len(plans) != 1 {
				t.Fatal("proven joined interrupted retirement was not recognized")
			}
			if replace {
				old := retired + "-old"
				if err := os.Rename(retired, old); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(retired, 0o700); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"manifest.json", "writer-joined.json", "owner.lock", "working.har"} {
					data, err := os.ReadFile(filepath.Join(old, name))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(retired, name), data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err == nil {
					t.Fatal("replaced interrupted generation accepted")
				}
				if _, err := os.Stat(filepath.Join(retired, "working.har")); err != nil {
					t.Fatal("replacement deleted", err)
				}
			} else {
				if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(retired); !os.IsNotExist(err) {
					t.Fatal("proven joined retirement not resumed", err)
				}
			}
		})
	}
}

func TestHAROwnershipCompletedRetentionHasBoundedScan(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 1025; i++ {
		name := filepath.Join(dir, fmt.Sprintf("unowned-%04d", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := enforceRuntimeHARRetentionLimit(dir, 2, 100); err == nil {
		t.Fatal("unbounded completed namespace scan reported success")
	}
}

func TestHAROwnershipJoinedCandidateCarriesBoundedActualSize(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	o.startClose()
	<-o.closeDone
	if o.closeErr != nil {
		t.Fatal(o.closeErr)
	}
	plans := PlanRuntimeHARWorkingCleanup(dir, 1)
	if len(plans) != 1 || plans[0].SizeBytes < int64(len(ownedHARFixture)) || plans[0].ModifiedAt.IsZero() {
		t.Fatal("joined candidate silently reported zero source bytes", plans)
	}
}

func TestHAROwnershipRawFileIsPrivateBeforeNativeWriterEnters(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	info, err := os.Lstat(o.path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 0 {
		t.Fatal("raw path was not privately reserved before native writer admission", err)
	}
	// The pinned local SDK uses ordinary writeFile/truncate, which preserves
	// the pre-created inode and mode instead of creating a default-mode file.
	if err := os.WriteFile(o.path, []byte(ownedHARFixture), 0o666); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(o.path)
	if err != nil || !os.SameFile(info, after) || after.Mode().Perm() != 0o600 {
		t.Fatal("native-style truncate replaced private file identity or mode", err)
	}
	if _, err := finishFixtureHAR(t, o, true); err != nil {
		t.Fatal(err)
	}
}

func TestHAROwnershipRetentionRejectsOversizedLimitsAndCancelledWork(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := enforceRuntimeHARRetentionAtContext(context.Background(), root, 1, maxRetainedRuntimeHARBytes+1); err == nil {
		t.Error("retention accepted an unbounded byte ceiling")
	}
	name := "apexload_browser_123_scrubbed.har"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("preserve cancelled work"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := enforceRuntimeHARRetentionAtContext(ctx, root, 1, 1); !errors.Is(err, context.Canceled) {
		t.Error("cancelled retention reported success", err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal("cancelled work removed a completed artifact", err)
	}
}

func TestHAROwnershipUnjoinedRetiredGenerationIsPreserved(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	if err := o.closeDescriptors(); err != nil {
		t.Fatal(err)
	}
	o.releaseReservation()
	retired := filepath.Join(dir, runtimeHARWorkingDirectory, ".retired-"+o.generation)
	if err := os.Rename(filepath.Dir(o.path), retired); err != nil {
		t.Fatal(err)
	}
	if plans := PlanRuntimeHARWorkingCleanup(dir, 128); len(plans) != 0 {
		t.Fatal("unjoined tombstone inferred writer death", plans)
	}
	purgeInactiveRuntimeHARWorkingFiles(dir)
	if _, err := os.Stat(filepath.Join(retired, "working.har")); err != nil {
		t.Fatal("unjoined tombstone was removed", err)
	}
}
