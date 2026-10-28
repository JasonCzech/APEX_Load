//go:build darwin || linux || windows

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/procidentity"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHARProcessGoneDecidesOnlyProvableExits(t *testing.T) {
	host := harHost{machine: "m1", boot: "b1", namespace: "pid:[1]"}
	id := harProcessIdentity{Role: harProcessGenerator, Machine: "m1", Boot: "b1", Namespace: "pid:[1]", PID: 42, Start: "100"}
	lookup := func(start string, found bool, err error) func(int) (string, bool, error) {
		return func(int) (string, bool, error) { return start, found, err }
	}
	alive := lookup("100", true, nil)
	for name, c := range map[string]struct {
		id    func(harProcessIdentity) harProcessIdentity
		start func(int) (string, bool, error)
		gone  bool
	}{
		"same process alive":          {func(i harProcessIdentity) harProcessIdentity { return i }, alive, false},
		"no such PID":                 {func(i harProcessIdentity) harProcessIdentity { return i }, lookup("", false, nil), true},
		"PID reused":                  {func(i harProcessIdentity) harProcessIdentity { return i }, lookup("200", true, nil), true},
		"lookup failed":               {func(i harProcessIdentity) harProcessIdentity { return i }, lookup("", false, os.ErrPermission), false},
		"earlier boot, same machine":  {func(i harProcessIdentity) harProcessIdentity { i.Boot = "b0"; return i }, alive, true},
		"earlier boot, other machine": {func(i harProcessIdentity) harProcessIdentity { i.Boot, i.Machine = "b0", "m2"; return i }, alive, false},
		"boot of unknown machine":     {func(i harProcessIdentity) harProcessIdentity { i.Boot, i.Machine = "b0", ""; return i }, alive, false},
		"other PID namespace":         {func(i harProcessIdentity) harProcessIdentity { i.Namespace = "pid:[2]"; return i }, lookup("", false, nil), false},
		"no start time":               {func(i harProcessIdentity) harProcessIdentity { i.Start = ""; return i }, lookup("", false, nil), false},
		"no PID":                      {func(i harProcessIdentity) harProcessIdentity { i.PID = 0; return i }, lookup("", false, nil), false},
	} {
		if got := harProcessGone(host, c.id(id), c.start); got != c.gone {
			t.Errorf("%s: gone=%v, want %v", name, got, c.gone)
		}
	}
	unknownMachine := harHost{boot: "b1", namespace: "pid:[1]"}
	if harProcessGone(unknownMachine, harProcessIdentity{Boot: "b0", Namespace: "pid:[1]", PID: 42, Start: "100"}, alive) {
		t.Error("an unidentified host judged an earlier boot")
	}
}

func TestHARProcessIdentityOfLiveAndExitedProcesses(t *testing.T) {
	self, err := harProcessIdentityOf(harProcessGenerator, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	host, err := currentHARHost()
	if err != nil {
		t.Fatal(err)
	}
	if harProcessGone(host, self, harProcessStart) {
		t.Fatal("this process judged gone")
	}
	child := harTestSleeper(30)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	identity, err := harProcessIdentityOf(harProcessWriter, child.Process.Pid)
	if err != nil {
		_ = child.Process.Kill()
		t.Fatal(err)
	}
	if harProcessGone(host, identity, harProcessStart) {
		t.Fatal("a live child judged gone")
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if !harProcessGone(host, identity, harProcessStart) {
		t.Fatal("a reaped child judged alive")
	}
	generator := identity
	generator.Role = harProcessGenerator
	if !harProcessesGone([]harProcessIdentity{generator, identity}) || !harProcessesGone([]harProcessIdentity{generator}) {
		t.Fatal("exited generator and writer not judged gone")
	}
	for name, processes := range map[string][]harProcessIdentity{
		"writer only":     {identity},
		"live generator":  {identity, self},
		"duplicate roles": {generator, generator},
		"no processes":    nil,
	} {
		if harProcessesGone(processes) {
			t.Errorf("%s judged gone", name)
		}
	}
}

// The crash child creates a generation whose native writer is the given
// process, reaches the requested stage and waits there to be killed.
func harCrashChild(t *testing.T, dir, stage string, writerPID int) {
	harNativeWriterPID = func() int { return writerPID }
	o, err := newHARCaptureOwner(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ready := func(stage string) {
		writeReadyToken(dir, o.generation+" "+stage)
		select {} // Wait here for SIGKILL.
	}
	if err := o.beginWriter(); err != nil {
		t.Fatal(err)
	}
	if stage == "write" {
		if err := os.WriteFile(o.path, []byte(ownedHARFixture[:20]), 0o600); err != nil {
			t.Fatal(err)
		}
		ready(stage)
	}
	if err := os.WriteFile(o.path, []byte(ownedHARFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	harFinalizeCheckpoint = func(reached string) {
		if reached == stage {
			ready(stage)
		}
	}
	o.bindWriter(func() error { return nil })
	o.startFinish(true, nil)
	_, err = o.waitFinish(context.Background())
	t.Fatalf("finish passed its %s checkpoint: %v", stage, err)
}

func TestHAROwnershipReclaimsKilledGenerations(t *testing.T) {
	if dir := os.Getenv("APEX_HAR_CRASH_DIR"); dir != "" {
		pid, _ := strconv.Atoi(os.Getenv("APEX_HAR_CRASH_WRITER"))
		harCrashChild(t, dir, os.Getenv("APEX_HAR_CRASH_STAGE"), pid)
		return
	}
	for _, stage := range []string{"write", "scrub", "publish"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			writer := harTestSleeper(60)
			if err := writer.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Process.Kill(); _ = writer.Wait() }()
			child := exec.Command(os.Args[0], "-test.run=^TestHAROwnershipReclaimsKilledGenerations$")
			child.Env = append(os.Environ(), "APEX_HAR_CRASH_DIR="+dir, "APEX_HAR_CRASH_STAGE="+stage, "APEX_HAR_CRASH_WRITER="+strconv.Itoa(writer.Process.Pid))
			log, err := os.Create(filepath.Join(t.TempDir(), "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			child.Stdout, child.Stderr = log, log
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
			var token []byte
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				if token, err = os.ReadFile(filepath.Join(dir, "ready")); err == nil {
					break
				}
			}
			var generation, reached string
			if _, err := fmt.Sscan(string(token), &generation, &reached); err != nil || reached != stage || !validHARGeneration(generation) {
				data, _ := os.ReadFile(log.Name())
				t.Fatalf("child did not reach %s: %q %s", stage, token, data)
			}
			generationDir := filepath.Join(dir, runtimeHARWorkingDirectory, generation)
			published := filepath.Join(dir, "apexload_browser_"+generation+"_scrubbed.har")
			if stage == "write" && len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
				t.Fatal("a live owner's generation was planned")
			}
			// kill -9 (TerminateProcess on Windows), then reap it so no zombie
			// keeps its identity.
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			if stage == "write" {
				// The native writer may still be flushing: nothing is reclaimable.
				if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
					t.Fatal("a generation was planned while its native writer lives")
				}
				if _, err := os.Stat(generationDir); err != nil {
					t.Fatal("a live writer's generation was removed", err)
				}
				_ = writer.Process.Kill()
				_ = writer.Wait()
			}
			plans := PlanRuntimeHARWorkingCleanup(dir, 128)
			if len(plans) != 1 {
				t.Fatalf("killed generation planned %d times", len(plans))
			}
			if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(generationDir); !os.IsNotExist(err) {
				t.Fatal("killed generation survived", err)
			}
			data, err := os.ReadFile(published)
			if stage == "publish" {
				var har map[string]any
				if err != nil || json.Unmarshal(data, &har) != nil {
					t.Fatalf("published artifact lost or partial: %v %q", err, data)
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("an unpublished generation left an artifact", err)
			}
			if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
				t.Fatal("reclaim left a plan behind")
			}
		})
	}
}

func TestHAROwnershipPreservesForeignAndUnprovenGenerations(t *testing.T) {
	dir := t.TempDir()
	host, err := currentHARHost()
	if err != nil {
		t.Fatal(err)
	}
	ended := func(role string) harProcessIdentity {
		child := harTestExiter()
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		pid := child.Process.Pid
		_ = child.Wait()
		return harProcessIdentity{Role: role, Machine: host.machine, Boot: host.boot, Namespace: host.namespace, PID: pid, Start: "1"}
	}
	rewrite := func(root *os.Root, name string, m harOwnershipManifest) {
		t.Helper()
		data, _ := json.Marshal(m)
		if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := writeHARPrivateDurable(root, name, data); err != nil {
			t.Fatal(name, err)
		}
	}
	// generation records a generator and, unless writer is nil, an entered
	// writer (an empty identity list records an unknown writer).
	generation := func(generator *harProcessIdentity, writer []harProcessIdentity, hold bool) *harCaptureOwner {
		o := fixtureHAROwner(t, dir)
		sealed := harOwnershipManifest{Version: 1, Generation: o.generation, State: "sealed"}
		if generator != nil {
			sealed.Version, sealed.Processes = 2, []harProcessIdentity{*generator}
		}
		rewrite(o.root, "manifest.json", sealed)
		if writer != nil {
			entered := harOwnershipManifest{Version: 1, Generation: o.generation, State: "writer_entered"}
			if len(writer) > 0 {
				entered.Version, entered.Processes = 2, writer
			}
			rewrite(o.root, "writer-entered.json", entered)
		}
		if !hold {
			if err := o.closeDescriptors(); err != nil {
				t.Fatal(err)
			}
		}
		return o
	}
	foreign := func(mutate func(*harProcessIdentity)) (*harProcessIdentity, []harProcessIdentity) {
		generator, writer := ended(harProcessGenerator), ended(harProcessWriter)
		mutate(&generator)
		mutate(&writer)
		return &generator, []harProcessIdentity{writer}
	}
	preserved := map[string]*harCaptureOwner{}
	g, w := foreign(func(p *harProcessIdentity) { p.Boot, p.Machine = "other-boot", "other-machine" })
	preserved["another machine's boot"] = generation(g, w, false)
	g, w = foreign(func(p *harProcessIdentity) { p.Namespace = "pid:[foreign]" })
	preserved["another PID namespace"] = generation(g, w, false)
	g, _ = foreign(func(*harProcessIdentity) {})
	preserved["writer entered without identity"] = generation(g, []harProcessIdentity{}, false)
	preserved["legacy without processes"] = generation(nil, []harProcessIdentity{}, false)
	g, w = foreign(func(*harProcessIdentity) {})
	preserved["lease still held"] = generation(g, w, true)
	// Two controls whose recorded processes provably ended: one whose writer
	// entered, one no native writer ever entered.
	g, w = foreign(func(*harProcessIdentity) {})
	control := generation(g, w, false)
	g, _ = foreign(func(*harProcessIdentity) {})
	unentered := generation(g, nil, false)
	// A writer record a crash left half-written was never completed, so no
	// writer received the path: like no record at all.
	g, _ = foreign(func(*harProcessIdentity) {})
	torn := generation(g, nil, true)
	if err := writeHARPrivateDurable(torn.root, "writer-entered.json", []byte(`{"version":1,"generat`)); err != nil {
		t.Fatal(err)
	}
	if err := torn.closeDescriptors(); err != nil {
		t.Fatal(err)
	}
	plans := PlanRuntimeHARWorkingCleanup(dir, 128)
	planned := map[string]RuntimeHARWorkingPruneCandidate{}
	for _, plan := range plans {
		if !plan.Abandoned {
			t.Fatalf("plan without abandonment: %+v", plan)
		}
		planned[filepath.Base(plan.Path)] = plan
	}
	if len(plans) != 3 || planned[control.generation].Path == "" || planned[unentered.generation].Path == "" || planned[torn.generation].Path == "" {
		t.Fatalf("planned %d generations, want the three controls", len(plans))
	}
	for name, o := range preserved {
		if _, ok := planned[o.generation]; ok {
			t.Errorf("%s planned", name)
		}
		if _, err := os.Stat(filepath.Dir(o.path)); err != nil {
			t.Fatal(name, "removed", err)
		}
	}
	// Pruning re-checks the processes under the lease: a plan cannot retire
	// a generation whose recorded writer is now this live process.
	self, err := harProcessIdentityOf(harProcessWriter, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	controlRoot, err := os.OpenRoot(filepath.Dir(control.path))
	if err != nil {
		t.Fatal(err)
	}
	defer controlRoot.Close()
	rewrite(controlRoot, "writer-entered.json", harOwnershipManifest{Version: 2, Generation: control.generation, State: "writer_entered", Processes: []harProcessIdentity{self}})
	if err := PruneRuntimeHARWorkingCandidate(context.Background(), planned[control.generation]); err == nil {
		t.Fatal("a generation with a live writer was retired")
	}
	if _, err := os.Stat(filepath.Dir(control.path)); err != nil {
		t.Fatal("live writer's generation removed", err)
	}
	// A fresh sweep retires only what is now proven.
	if reclaimed, err := ReclaimRuntimeHARWorking(context.Background(), dir, 128); err != nil || reclaimed != 2 {
		t.Fatalf("sweep reclaimed %d: %v", reclaimed, err)
	}
	if _, err := os.Stat(filepath.Dir(unentered.path)); !os.IsNotExist(err) {
		t.Fatal("a generation no writer entered survived", err)
	}
	for name, o := range map[string]*harCaptureOwner{"control with a live writer": control, "another PID namespace": preserved["another PID namespace"]} {
		if _, err := os.Stat(filepath.Dir(o.path)); err != nil {
			t.Fatal(name, "removed by the sweep", err)
		}
	}
}

// A generator killed while its real Playwright driver records a native HAR:
// the generation stays until the orphaned driver has exited too, then a
// restarted generator reclaims it.
func TestHAROwnershipReclaimsKilledNativeRecording(t *testing.T) {
	if dir := os.Getenv("APEX_HAR_NATIVE_CRASH_DIR"); dir != "" {
		server := os.Getenv("APEX_HAR_NATIVE_CRASH_URL")
		b := &Browser{}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := b.Init(ctx, map[string]any{"base_url": server, "browser_engine": os.Getenv("APEX_HAR_NATIVE_CRASH_ENGINE"), "browser_headless": true, "browser_har_capture": "always", "browser_har_dir": dir}); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Execute(ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"}); err != nil {
			t.Fatal(err)
		}
		b.mu.Lock()
		owner := b.harOwner
		b.mu.Unlock()
		if owner == nil {
			t.Fatal("no HAR owner")
		}
		writeReadyToken(dir, fmt.Sprintf("%s %d", owner.generation, playwrightDriverPID()))
		select {} // Wait here for SIGKILL.
	}
	// BE-4: controlled pages in Chromium and WebKit.
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) { testHAROwnershipReclaimsKilledNativeRecording(t, engine) })
	}
}

func testHAROwnershipReclaimsKilledNativeRecording(t *testing.T, engine string) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>recorded</title><body>native HAR recording</body>`)
	}))
	defer server.Close()
	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestHAROwnershipReclaimsKilledNativeRecording$")
	child.Env = append(os.Environ(), "APEX_HAR_NATIVE_CRASH_DIR="+dir, "APEX_HAR_NATIVE_CRASH_URL="+server.URL, "APEX_HAR_NATIVE_CRASH_ENGINE="+engine)
	log, err := os.Create(filepath.Join(t.TempDir(), "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	child.Stdout, child.Stderr = log, log
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	var generation string
	var driver int
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if token, err := os.ReadFile(filepath.Join(dir, "ready")); err == nil {
			_, _ = fmt.Sscan(string(token), &generation, &driver)
			break
		}
	}
	if !validHARGeneration(generation) || driver <= 0 {
		data, _ := os.ReadFile(log.Name())
		requireBrowserRuntime(t, engine, fmt.Errorf("child did not start a native recording: %s", data))
	}
	generationDir := filepath.Join(dir, runtimeHARWorkingDirectory, generation)
	host, err := currentHARHost()
	if err != nil {
		t.Fatal(err)
	}
	driverStart, found, err := harProcessStart(driver)
	if err != nil || !found {
		t.Fatal("driver identity unavailable", err)
	}
	driverIdentity := harProcessIdentity{Role: harProcessWriter, Machine: host.machine, Boot: host.boot, Namespace: host.namespace, PID: driver, Start: driverStart}
	if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
		t.Fatal("a live recording was planned")
	}
	killedAt := time.Now()
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if harLeaseInheritableAcrossExec {
		if local, localErr := harWorkingDirectoryIsLocal(filepath.Join(dir, runtimeHARWorkingDirectory)); localErr == nil && local {
			for _, name := range []string{"manifest.json", "writer-entered.json"} {
				raw, err := os.ReadFile(filepath.Join(generationDir, name))
				if err != nil {
					t.Fatalf("read %s: %v", name, err)
				}
				var m harOwnershipManifest
				if err := json.Unmarshal(raw, &m); err != nil || m.Version != harOwnershipManifestVersion {
					t.Fatalf("%s version = %d (err=%v), want %d (%s)", name, m.Version, err, harOwnershipManifestVersion, raw)
				}
			}
		}
	}
	// The orphaned driver notices its closed pipe and exits; until it has,
	// the generation is not reclaimable (neither in this PID namespace nor
	// from a replacement container in a new PID namespace, because the driver
	// holds the inherited owner.lock descriptor). On stdin close the driver
	// closes its browsers gracefully and only force-exits after 30 seconds
	// (gracefullyProcessExitDoNotHang), which a loaded runner closing WebKit
	// can reach.
	origHost := currentHARHost
	t.Cleanup(func() { currentHARHost = origHost })
	for deadline := time.Now().Add(45 * time.Second); !harProcessGone(host, driverIdentity, harProcessStart); time.Sleep(50 * time.Millisecond) {
		if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
			t.Fatal("planned while the native writer lives")
		}
		currentHARHost = func() (harHost, error) {
			return harHost{machine: host.machine, boot: host.boot, namespace: host.namespace + "-replacement"}, nil
		}
		replacementPlanned := len(PlanRuntimeHARWorkingCleanup(dir, 128))
		currentHARHost = origHost
		if replacementPlanned != 0 {
			t.Fatal("replacement container planned while the native writer still holds the inherited lease")
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphaned driver %d did not exit", driver)
		}
	}
	driverExitElapsed := time.Since(killedAt)
	t.Logf("engine=%s pid=%d driver=%d exited after %s", engine, os.Getpid(), driver, driverExitElapsed.Round(time.Millisecond))
	var plans []RuntimeHARWorkingPruneCandidate
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		plans = PlanRuntimeHARWorkingCleanup(dir, 128)
		if len(plans) > 0 {
			break
		}
	}
	if len(plans) != 1 || !plans[0].Abandoned {
		manifest, _ := os.ReadFile(filepath.Join(generationDir, "manifest.json"))
		entries, _ := os.ReadDir(generationDir)
		t.Fatalf("killed recording planned %d times; manifest %s; entries %v", len(plans), manifest, entries)
	}
	if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generationDir); !os.IsNotExist(err) {
		t.Fatal("killed recording survived", err)
	}
}

// A writer record that cannot be written leaves no writer entered and no
// record behind, so the owner can still close and its generation can be
// reclaimed once the generator has gone.
func TestHARBeginWriterFailureEntersNoWriter(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	generationDir := filepath.Dir(o.path)
	makeHARReadOnlyDir(t, generationDir)
	if err := o.beginWriter(); err == nil {
		t.Fatal("writer record written into a read-only generation")
	}
	o.mu.Lock()
	entered := o.writerEntered
	o.mu.Unlock()
	if entered {
		t.Fatal("a writer counted as entered without its record")
	}
	if _, err := os.Stat(filepath.Join(generationDir, "writer-entered.json")); !os.IsNotExist(err) {
		t.Fatalf("writer record left behind: %v", err)
	}
}

// A writer record written but not made durable is removed again, leaving the
// owner as one no writer entered.
func TestHARBeginWriterRemovesARecordItCouldNotSync(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	generationDir := filepath.Dir(o.path)
	original := syncHARWriterRecord
	t.Cleanup(func() { syncHARWriterRecord = original })
	syncHARWriterRecord = func(*os.Root) error { return errors.New("injected sync failure") }
	if err := o.beginWriter(); err == nil {
		t.Fatal("an unsynced writer record counted")
	}
	syncHARWriterRecord = original
	if _, err := os.Stat(filepath.Join(generationDir, "writer-entered.json")); !os.IsNotExist(err) {
		t.Fatalf("the record this call created was left behind: %v", err)
	}
	o.mu.Lock()
	entered := o.writerEntered
	o.mu.Unlock()
	if entered {
		t.Fatal("a writer counted as entered without a durable record")
	}
}

// macOS records no machine identity (kern.uuid names the kernel build), so
// an earlier boot never proves a process gone there; Linux and Windows pair
// a machine ID with the host name.
func TestHARHostMachineIdentityIsHostScoped(t *testing.T) {
	host, err := currentHARHost()
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "darwin":
		if host.machine != "" {
			t.Fatalf("darwin machine identity %q", host.machine)
		}
	case "linux":
		if host.machine != "" && !strings.Contains(host.machine, "@") {
			t.Fatalf("linux machine identity %q is not paired with the host name", host.machine)
		}
	case "windows":
		// MachineGuid paired with the physical DNS host name; the boot is
		// the kernel's boot sequence number.
		if host.namespace != "windows" || host.machine != "" && !strings.Contains(host.machine, "@") {
			t.Fatalf("windows host identity %+v", host)
		}
		if _, err := strconv.ParseUint(host.boot, 10, 64); err != nil {
			t.Fatalf("windows boot identity %q: %v", host.boot, err)
		}
	}
}

// writeReadyToken publishes a child's handshake atomically (write, then
// rename), so the parent never reads a created-but-empty token.
func writeReadyToken(dir, token string) {
	staged := filepath.Join(dir, "ready.tmp")
	if os.WriteFile(staged, []byte(token), 0o600) == nil {
		_ = os.Rename(staged, filepath.Join(dir, "ready"))
	}
}

func TestParseLinuxProcStatStartTreatsZombieAndDeadStatesAsExited(t *testing.T) {
	statLine := func(comm, state, startTime string) []byte {
		// Fields 3..22 after ')': state(0) ppid(1) pgrp(2) session(3) tty_nr(4)
		// tpgid(5) flags(6) minflt(7) cminflt(8) majflt(9) cmajflt(10) utime(11)
		// stime(12) cutime(13) cstime(14) priority(15) nice(16) num_threads(17)
		// itrealvalue(18) starttime(19).
		return []byte(fmt.Sprintf("42 (%s) %s 1 42 42 0 -1 4194304 10 0 0 0 5 2 0 0 20 0 1 0 %s 123456 100", comm, state, startTime))
	}

	host := harHost{machine: "m1@node-a", boot: "boot-1", namespace: "pid:[4026531836]"}
	id := harProcessIdentity{
		Role:      harProcessWriter,
		Machine:   host.machine,
		Boot:      host.boot,
		Namespace: host.namespace,
		PID:       42,
		Start:     "987654",
	}

	for _, state := range []string{"R", "S", "D", "T"} {
		got, found, err := procidentity.ParseLinuxStatStart(statLine("node (playwright)", state, "987654"))
		if err != nil || !found || got != "987654" {
			t.Fatalf("live state %q: got=(%q, %v, %v), want (987654, true, nil)", state, got, found, err)
		}
		if harProcessGone(host, id, func(int) (string, bool, error) {
			return procidentity.ParseLinuxStatStart(statLine("node (playwright)", state, "987654"))
		}) {
			t.Fatalf("live state %q reported harProcessGone = true", state)
		}
	}

	// Container PID-1 scenario: when generator is PID 1 without --init, an
	// orphaned Playwright Node grandchild reparented to PID 1 becomes a 'Z'
	// zombie when it exits on stdin EOF. parseLinuxProcStatStart must treat
	// 'Z', 'X', and 'x' as exited (found = false, err = nil).
	for _, exitedState := range []string{"Z", "X", "x"} {
		got, found, err := procidentity.ParseLinuxStatStart(statLine("node (playwright)", exitedState, "987654"))
		if err != nil || found || got != "" {
			t.Fatalf("exited state %q: got=(%q, %v, %v), want (\"\", false, nil)", exitedState, got, found, err)
		}
		if !harProcessGone(host, id, func(int) (string, bool, error) {
			return procidentity.ParseLinuxStatStart(statLine("node (playwright)", exitedState, "987654"))
		}) {
			t.Fatalf("exited state %q reported harProcessGone = false", exitedState)
		}
	}

	// Malformed stat lines must fail closed (err != nil -> harProcessGone = false),
	// even if field 3 is 'Z'.
	for _, malformed := range [][]byte{
		[]byte("42 unclosed_comm Z 1 2 3"),
		[]byte("42 (short) Z 1 2 3"),
		statLine("node", "Z", "not-a-uint"),
		statLine("node", "S", "-1"),
	} {
		_, found, err := procidentity.ParseLinuxStatStart(malformed)
		if err == nil || found {
			t.Fatalf("malformed stat %q: found=%v err=%v, want false and non-nil error", string(malformed), found, err)
		}
		if harProcessGone(host, id, func(int) (string, bool, error) {
			return procidentity.ParseLinuxStatStart(malformed)
		}) {
			t.Fatalf("malformed stat %q reported harProcessGone = true", string(malformed))
		}
	}
}

func harReplacementContainerCrashChild(t *testing.T, dir string) {
	o, err := newHARCaptureOwner(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	writer := harTestSleeper(60)
	if err := o.startProcessWithInheritedLease(func() (int, error) {
		if err := writer.Start(); err != nil {
			return 0, err
		}
		return writer.Process.Pid, nil
	}); err != nil {
		t.Fatal(err)
	}
	harNativeWriterPID = func() int { return writer.Process.Pid }
	if err := o.beginWriter(); err != nil {
		_ = writer.Process.Kill()
		t.Fatal(err)
	}
	if err := os.WriteFile(o.path, []byte(ownedHARFixture[:20]), 0o600); err != nil {
		_ = writer.Process.Kill()
		t.Fatal(err)
	}
	writeReadyToken(dir, fmt.Sprintf("%s %d", o.generation, writer.Process.Pid))
	time.Sleep(time.Hour) // Wait here for SIGKILL without triggering Go's all-goroutines-asleep detector under PID 1.
}

// A replacement container in a new PID namespace on the same kernel boot and
// machine identity reclaims a version-3 generation on a verified local Unix
// filesystem only after BOTH the killed generator and its inherited-lease
// writer process have exited, while preserving same-namespace procidentity.Gone
// checks, non-local (NFS/FUSE) mounts, and foreign machine/boot identities.
func TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits(t *testing.T) {
	if !harLeaseInheritableAcrossExec {
		t.Skip("inherited flock lease across exec requires Unix")
	}
	if dir := os.Getenv("APEX_HAR_REPLACEMENT_CRASH_DIR"); dir != "" {
		harReplacementContainerCrashChild(t, dir)
		return
	}
	if dir := os.Getenv("APEX_HAR_REPLACEMENT_RECLAIM_DIR"); dir != "" {
		host, err := currentHARHost()
		if err != nil {
			t.Fatal(err)
		}
		reclaimed, err := ReclaimRuntimeHARWorking(context.Background(), dir, 128)
		t.Logf("replacement container host=%+v reclaimed=%d err=%v", host, reclaimed, err)
		if err != nil || reclaimed != 1 {
			t.Fatalf("ReclaimRuntimeHARWorking(%q) = (%d, %v), want (1, nil)", dir, reclaimed, err)
		}
		return
	}

	dir := t.TempDir()
	local, localErr := harWorkingDirectoryIsLocal(dir)
	if localErr != nil || !local {
		t.Skipf("tempdir %q is not an allowlisted local filesystem: local=%v err=%v", dir, local, localErr)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits$")
	child.Env = append(os.Environ(), "APEX_HAR_REPLACEMENT_CRASH_DIR="+dir)
	logFile, err := os.Create(filepath.Join(t.TempDir(), "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	child.Stdout, child.Stderr = logFile, logFile
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var writerProc *os.Process
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
		if writerProc != nil {
			_ = writerProc.Kill()
			_, _ = writerProc.Wait()
		}
	}()

	var generation string
	var writerPID int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if token, err := os.ReadFile(filepath.Join(dir, "ready")); err == nil {
			_, _ = fmt.Sscan(string(token), &generation, &writerPID)
			break
		}
	}
	if !validHARGeneration(generation) || writerPID <= 0 {
		data, _ := os.ReadFile(logFile.Name())
		t.Fatalf("replacement crash child did not become ready: gen=%q writer=%d log=%s", generation, writerPID, data)
	}
	writerProc, _ = os.FindProcess(writerPID)
	generationDir := filepath.Join(dir, runtimeHARWorkingDirectory, generation)

	for _, name := range []string{"manifest.json", "writer-entered.json"} {
		raw, err := os.ReadFile(filepath.Join(generationDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var m harOwnershipManifest
		if err := json.Unmarshal(raw, &m); err != nil || m.Version != harOwnershipManifestVersion {
			t.Fatalf("%s version = %d (err=%v), want %d (%s)", name, m.Version, err, harOwnershipManifestVersion, raw)
		}
	}

	host, err := currentHARHost()
	if err != nil {
		t.Fatal(err)
	}
	writerStart, found, err := harProcessStart(writerPID)
	if err != nil || !found {
		t.Fatalf("writer %d start unavailable: found=%v err=%v", writerPID, found, err)
	}
	writerIdentity := harProcessIdentity{Role: harProcessWriter, Machine: host.machine, Boot: host.boot, Namespace: host.namespace, PID: writerPID, Start: writerStart}

	// Kill the generator child (SIGKILL). The writer process remains alive and
	// holds the inherited open file description for owner.lock.
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()

	origHost := currentHARHost
	origLocal := harWorkingDirectoryIsLocal
	t.Cleanup(func() {
		currentHARHost = origHost
		harWorkingDirectoryIsLocal = origLocal
	})

	// Simulate a replacement container in a new PID namespace on the same boot.
	currentHARHost = func() (harHost, error) {
		return harHost{machine: host.machine, boot: host.boot, namespace: host.namespace + "-replacement-container"}, nil
	}

	// 1. While the orphaned writer is still alive, it holds the inherited
	// owner.lock descriptor, so the replacement container cannot plan or prune it.
	if plans := PlanRuntimeHARWorkingCleanup(dir, 128); len(plans) != 0 {
		t.Fatalf("replacement container planned generation while inherited writer %d is still alive: %+v", writerPID, plans)
	}

	// 2. Even if leaseCoversWriterTree is true, within the SAME PID namespace
	// procidentity.Gone remains mandatory and refuses a live writer.
	if harProcessGoneWithLease(host, writerIdentity, harProcessStart, true) {
		t.Fatal("same-namespace live writer judged gone by leaseCoversWriterTree")
	}

	// Kill the orphaned writer and wait for the kernel to close its inherited
	// owner.lock descriptor.
	if writerProc != nil {
		_ = writerProc.Kill()
		_, _ = writerProc.Wait()
	}
	for deadline := time.Now().Add(5 * time.Second); !harProcessGone(host, writerIdentity, harProcessStart); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("writer %d did not exit", writerPID)
		}
	}

	// 3. On a non-local filesystem (e.g. NFS/FUSE), cross-namespace reclamation
	// must be refused even after the lease is free.
	harWorkingDirectoryIsLocal = func(string) (bool, error) { return false, nil }
	if plans := PlanRuntimeHARWorkingCleanup(dir, 128); len(plans) != 0 {
		t.Fatalf("replacement container planned generation on non-local filesystem: %+v", plans)
	}
	harWorkingDirectoryIsLocal = origLocal

	// 4. On a foreign boot or foreign machine, cross-namespace reclamation must
	// be refused.
	currentHARHost = func() (harHost, error) {
		return harHost{machine: "foreign-machine@other", boot: host.boot, namespace: host.namespace + "-replacement-container"}, nil
	}
	if plans := PlanRuntimeHARWorkingCleanup(dir, 128); len(plans) != 0 {
		t.Fatalf("foreign machine planned generation: %+v", plans)
	}

	// 5. On the same machine and boot with a verified local filesystem and
	// free lease, the replacement container in the new PID namespace reclaims
	// the predecessor container's v3 generation.
	currentHARHost = func() (harHost, error) {
		return harHost{machine: host.machine, boot: host.boot, namespace: host.namespace + "-replacement-container"}, nil
	}
	plans := PlanRuntimeHARWorkingCleanup(dir, 128)
	if len(plans) != 1 || !plans[0].Abandoned {
		t.Fatalf("replacement container planned %+v, want 1 abandoned candidate", plans)
	}
	if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
		t.Fatalf("replacement container PruneRuntimeHARWorkingCandidate failed: %v", err)
	}
	if _, err := os.Stat(generationDir); !os.IsNotExist(err) {
		t.Fatalf("reclaimed generation directory still exists: %v", err)
	}
}
