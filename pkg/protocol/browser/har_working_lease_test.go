package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

const ownedHARFixture = `{"log":{"version":"1.2","creator":{"name":"synthetic","version":"1"},"entries":[]}}`

func fixtureHAROwner(t *testing.T, dir string) *harCaptureOwner {
	t.Helper()
	o, err := newHARCaptureOwner(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Fixtures have no native writer. Explicit fixture cleanup is not a
		// production authorization to release a failed or unknown owner.
		_ = o.closeDescriptors()
		o.releaseReservation()
	})
	return o
}

func writeOwnedHARFixture(t *testing.T, o *harCaptureOwner) {
	t.Helper()
	if err := os.WriteFile(o.path, []byte(ownedHARFixture), 0o600); err != nil {
		t.Fatal(err)
	}
}

func finishFixtureHAR(t *testing.T, o *harCaptureOwner, keep bool) (string, error) {
	t.Helper()
	o.startFinish(keep, nil)
	return o.waitFinish(context.Background())
}

func TestHAROwnershipPrivateGenerationAndDurablePublication(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	if !validHARGeneration(o.generation) || !strings.Contains(o.path, runtimeHARWorkingDirectory) || strings.HasSuffix(o.path, ".har.tmp") {
		t.Fatal("raw capture escaped private generation")
	}
	info, err := os.Stat(filepath.Dir(o.path))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("generation is not private", err)
	}
	artifact, err := finishFixtureHAR(t, o, true)
	if err != nil {
		t.Fatal(err)
	}
	actualDir, _ := os.Stat(filepath.Dir(artifact))
	expectedDir, _ := os.Stat(dir)
	if !isRetainedRuntimeHARArtifact(filepath.Base(artifact)) || !os.SameFile(actualDir, expectedDir) {
		t.Fatal("completed artifact is not discoverable")
	}
	if _, err := os.Stat(o.path); !os.IsNotExist(err) {
		t.Fatal("joined raw survived retirement", err)
	}
	data, err := os.ReadFile(artifact)
	if err != nil || !json.Valid(data) {
		t.Fatal("invalid published HAR", err)
	}
	if !o.retiredSuccessfully() {
		t.Fatal("physical owner was not retired")
	}
	if entries, err := os.ReadDir(filepath.Join(dir, runtimeHARWorkingDirectory)); err != nil || len(entries) != 2 || entries[0].Name() != "namespace.lock" || entries[1].Name() != harRetirementLedgerName {
		t.Fatal("private generation leftovers", err)
	}
}

func TestHAROwnershipCollisionNeverReplacesCompletedArtifact(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	existing := filepath.Join(dir, "apexload_browser_"+o.generation+"_scrubbed.har")
	if err := os.WriteFile(existing, []byte("existing artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := finishFixtureHAR(t, o, true)
	if err == nil || artifact != "" {
		t.Fatal("collision reported success", err)
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "existing artifact" {
		t.Fatal("collision overwrote artifact")
	}
	if !o.retiredSuccessfully() {
		t.Fatal("proven joined raw was not safely retired after collision")
	}
	if _, err := os.Stat(o.path); !os.IsNotExist(err) {
		t.Fatal("collision retained raw", err)
	}
}

func TestHAROwnershipUnknownLegacyAndUnjoinedCrashRemainPrivate(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "apexload_browser_legacy.har.tmp")
	if err := os.WriteFile(legacy, []byte("unjoined legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	if err := o.closeDescriptors(); err != nil {
		t.Fatal(err)
	}
	o.releaseReservation() // Simulate a dead Go owner; this is not writer proof.
	purgeInactiveRuntimeHARWorkingFiles(dir)
	if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
		t.Fatal("abandoned lease invented writer join")
	}
	for _, p := range []string{legacy, o.path} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("unknown raw deleted", err)
		}
	}
}

func TestHAROwnershipFailedAndBlockedCloseRetainsExactLease(t *testing.T) {
	for _, kind := range []string{"error", "panic", "blocked"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			o := fixtureHAROwner(t, dir)
			writeOwnedHARFixture(t, o)
			gate := make(chan struct{})
			var calls atomic.Int32
			o.beginWriter()
			o.bindWriter(func() error {
				calls.Add(1)
				if kind == "blocked" {
					<-gate
					return nil
				}
				if kind == "panic" {
					panic("synthetic close panic")
				}
				return errors.New("synthetic close failure")
			})
			o.startFinish(false, nil)
			o.startClose()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if _, err := o.waitFinish(ctx); err == nil {
				t.Fatal("unjoined close reported success")
			}
			if o.retiredSuccessfully() {
				t.Fatal("failed close retired owner")
			}
			if _, err := os.Stat(o.path); err != nil {
				t.Fatal("raw removed before join", err)
			}
			if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
				t.Fatal("unjoined close published writer proof")
			}
			runtimeHAROwners.Lock()
			charged := runtimeHAROwners.owners[o]
			runtimeHAROwners.Unlock()
			if !charged {
				t.Fatal("unjoined physical work released admission charge")
			}
			if kind == "blocked" {
				close(gate)
				if _, err := o.waitFinish(context.Background()); err != nil {
					t.Fatal("late successful join did not retire exact owner", err)
				}
				if _, err := os.Stat(o.path); !os.IsNotExist(err) {
					t.Fatal("late joined raw survived", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("context closer entered more than once")
			}
		})
	}
}

type harCloseFixtureContext struct {
	playwright.BrowserContext
	close func() error
}

func (c *harCloseFixtureContext) Close(_ ...playwright.BrowserContextCloseOptions) error {
	return c.close()
}

func TestHAROwnershipInitFailureJoinsBeforeRemovingRaw(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	var calls atomic.Int32
	c := &harCloseFixtureContext{close: func() error {
		calls.Add(1)
		if _, err := os.Stat(o.path); err != nil {
			t.Error("raw was removed before native close", err)
		}
		return nil
	}}
	o.beginWriter()
	o.bindWriter(func() error { return c.Close() })
	b := &Browser{harOwner: o, harWorkingPath: o.path}
	cause := errors.New("synthetic init failure")
	if err := b.failBrowserContextInitialization(c, cause); !errors.Is(err, cause) {
		t.Fatal("init cause lost", err)
	}
	if calls.Load() != 1 || !o.retiredSuccessfully() {
		t.Fatal("init failure did not join exact closer")
	}
	if _, err := os.Stat(o.path); !os.IsNotExist(err) {
		t.Fatal("init failure raw survived successful join", err)
	}
}

func TestHAROwnershipLateOldLimiterCannotCloseNewGeneration(t *testing.T) {
	dir := t.TempDir()
	old := fixtureHAROwner(t, dir)
	current := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, old)
	writeOwnedHARFixture(t, current)
	var oldCalls, newCalls atomic.Int32
	old.beginWriter()
	old.bindWriter(func() error { oldCalls.Add(1); return nil })
	current.beginWriter()
	current.bindWriter(func() error { newCalls.Add(1); return nil })
	b := &Browser{harOwner: current, harWorkingPath: current.path}
	b.evidence.generation = "new"
	b.closeOwnedOversizedHARContext(old, "new")
	if b.harErrorSeen || newCalls.Load() != 0 || oldCalls.Load() != 0 {
		t.Fatal("old limiter mutated current generation")
	}
	if _, err := finishFixtureHAR(t, old, false); err != nil {
		t.Fatal(err)
	}
	if b.harOwner != current || b.harWorkingPath != current.path {
		t.Fatal("old retirement replaced current owner")
	}
	if _, err := os.Stat(current.path); err != nil {
		t.Fatal("old retirement removed current raw", err)
	}
	if _, err := finishFixtureHAR(t, current, false); err != nil {
		t.Fatal(err)
	}
}

func TestHAROwnershipRejectsUnsafeRootsAndLockAliases(t *testing.T) {
	for _, kind := range []string{"root_symlink", "work_symlink", "work_permissions", "lock_hardlink", "lock_symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			other := t.TempDir()
			if kind == "root_symlink" {
				p := filepath.Join(dir, "alias")
				if err := os.Symlink(other, p); err != nil {
					t.Fatal(err)
				}
				dir = p
			} else if kind == "work_symlink" {
				if err := os.Symlink(other, filepath.Join(dir, runtimeHARWorkingDirectory)); err != nil {
					t.Fatal(err)
				}
			} else {
				work := filepath.Join(dir, runtimeHARWorkingDirectory)
				mode := os.FileMode(0o700)
				if kind == "work_permissions" {
					mode = 0o755
				}
				if err := os.Mkdir(work, mode); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(kind, "lock_") {
					target := filepath.Join(other, "target")
					if err := os.WriteFile(target, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					if kind == "lock_hardlink" {
						if err := os.Link(target, filepath.Join(work, "namespace.lock")); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := os.Symlink(target, filepath.Join(work, "namespace.lock")); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if o, err := newHARCaptureOwner(context.Background(), dir); err == nil {
				_, _ = finishFixtureHAR(t, o, false)
				t.Fatal("unsafe staging admitted")
			}
		})
	}
}

func TestHAROwnershipManifestAndGenerationFences(t *testing.T) {
	for _, kind := range []string{"unknown_version", "wrong_generation", "trailing_json", "oversized", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			o := fixtureHAROwner(t, dir)
			writeOwnedHARFixture(t, o)
			o.startClose()
			<-o.closeDone
			if o.closeErr != nil {
				t.Fatal(o.closeErr)
			}
			plans := PlanRuntimeHARWorkingCleanup(dir, 1)
			if len(plans) != 1 {
				t.Fatal("joined generation not planned")
			}
			if err := o.closeDescriptors(); err != nil {
				t.Fatal(err)
			}
			o.releaseReservation()
			marker := filepath.Join(filepath.Dir(o.path), "writer-joined.json")
			switch kind {
			case "unknown_version":
				if err := os.WriteFile(marker, []byte(`{"version":2,"generation":"`+o.generation+`","state":"writer_joined"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "wrong_generation":
				if err := os.WriteFile(marker, []byte(`{"version":1,"generation":"ffffffffffffffffffffffffffffffff","state":"writer_joined"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "trailing_json":
				f, err := os.OpenFile(marker, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.WriteString(` {}`)
				_ = f.Close()
			case "oversized":
				if err := os.WriteFile(marker, []byte(strings.Repeat("x", maxRuntimeHARManifestBytes+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				old := filepath.Dir(o.path)
				if err := os.Rename(old, old+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(old, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err == nil {
				t.Fatal("stale or invalid cleanup plan accepted")
			}
			if kind != "replacement" {
				if _, err := os.Stat(o.path); err != nil {
					t.Fatal("invalid cleanup removed raw", err)
				}
			}
		})
	}
}

func TestHAROwnershipCrossProcessCleaner(t *testing.T) {
	if dir := os.Getenv("APEX_NATIVE_HAR_LEASE_CHILD_DIR"); dir != "" {
		o, err := newHARCaptureOwner(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(o.path, os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(ownedHARFixture); err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		o.beginWriter()
		o.bindWriter(file.Close)
		if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(o.generation), 0o600); err != nil {
			t.Fatal(err)
		}
		var command [1]byte
		if _, err := os.Stdin.Read(command[:]); err != nil {
			t.Fatal(err)
		}
		if command[0] == 'j' {
			o.startClose()
			<-o.closeDone
			if o.closeErr != nil {
				t.Fatal(o.closeErr)
			}
			if err := os.WriteFile(filepath.Join(dir, "joined"), []byte("joined"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stdin.Read(command[:]); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(0) // Deliberate process exit leaves the generation for another process.
	}
	for _, joined := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown_crash", true: "joined_crash"}[joined], func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHAROwnershipCrossProcessCleaner$")
			cmd.Env = append(os.Environ(), "APEX_NATIVE_HAR_LEASE_CHILD_DIR="+dir)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			log, err := os.Create(filepath.Join(dir, "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd.Stdout = log
			cmd.Stderr = log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			var token []byte
			for time.Now().Before(deadline) {
				token, err = os.ReadFile(filepath.Join(dir, "ready"))
				if err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !validHARGeneration(string(token)) {
				t.Fatal("child did not establish generation")
			}
			path := filepath.Join(dir, runtimeHARWorkingDirectory, string(token), "working.har")
			purgeInactiveRuntimeHARWorkingFiles(dir)
			if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
				t.Fatal("live writer eligible for cleanup")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("other process live writer deleted", err)
			}
			command := byte('u')
			if joined {
				command = 'j'
			}
			if _, err := input.Write([]byte{command}); err != nil {
				t.Fatal(err)
			}
			if joined {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(filepath.Join(dir, "joined")); err == nil {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				plans := PlanRuntimeHARWorkingCleanup(dir, 1)
				if len(plans) != 1 {
					t.Fatal("joined active lease was not planned")
				}
				bound, stop := context.WithTimeout(context.Background(), 25*time.Millisecond)
				err := PruneRuntimeHARWorkingCandidate(bound, plans[0])
				stop()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("cleaner bypassed another process's held lease", err)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("held joined writer raw was deleted", err)
				}
				if _, err := input.Write([]byte{'e'}); err != nil {
					t.Fatal(err)
				}
			}
			_ = input.Close()
			if err := cmd.Wait(); err != nil {
				t.Fatal("child exit", err)
			}
			plans := PlanRuntimeHARWorkingCleanup(dir, 128)
			if joined {
				if len(plans) != 1 {
					t.Fatal("joined crash not planned")
				}
				if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("joined abandoned raw survived", err)
				}
			} else {
				if len(plans) != 0 {
					t.Fatal("crash with dropped Go lease invented native writer proof")
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("unknown crashed raw deleted", err)
				}
			}
		})
	}
}
