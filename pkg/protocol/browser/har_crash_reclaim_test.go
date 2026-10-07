//go:build darwin || linux

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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
	child := exec.Command("sleep", "30")
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
		_ = os.WriteFile(filepath.Join(dir, "ready"), []byte(o.generation+" "+stage), 0o600)
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
			writer := exec.Command("sleep", "60")
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
			// kill -9, then reap it so no zombie keeps its identity.
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
		child := exec.Command("true")
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
	plans := PlanRuntimeHARWorkingCleanup(dir, 128)
	planned := map[string]RuntimeHARWorkingPruneCandidate{}
	for _, plan := range plans {
		if !plan.Abandoned {
			t.Fatalf("plan without abandonment: %+v", plan)
		}
		planned[filepath.Base(plan.Path)] = plan
	}
	if len(plans) != 2 || planned[control.generation].Path == "" || planned[unentered.generation].Path == "" {
		t.Fatalf("planned %d generations, want the two controls", len(plans))
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
	if reclaimed, err := ReclaimRuntimeHARWorking(context.Background(), dir, 128); err != nil || reclaimed != 1 {
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
		if err := b.Init(ctx, map[string]any{"base_url": server, "browser_engine": "chromium", "browser_headless": true, "browser_har_capture": "always", "browser_har_dir": dir}); err != nil {
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
		_ = os.WriteFile(filepath.Join(dir, "ready"), []byte(fmt.Sprintf("%s %d", owner.generation, playwrightDriverPID())), 0o600)
		select {} // Wait here for SIGKILL.
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>recorded</title><body>native HAR recording</body>`)
	}))
	defer server.Close()
	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestHAROwnershipReclaimsKilledNativeRecording$")
	child.Env = append(os.Environ(), "APEX_HAR_NATIVE_CRASH_DIR="+dir, "APEX_HAR_NATIVE_CRASH_URL="+server.URL)
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
		requireBrowserRuntime(t, "chromium", fmt.Errorf("child did not start a native recording: %s", data))
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
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	// The orphaned driver notices its closed pipe and exits; until it has,
	// the generation is not reclaimable.
	for deadline := time.Now().Add(20 * time.Second); !harProcessGone(host, driverIdentity, harProcessStart); time.Sleep(50 * time.Millisecond) {
		if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
			t.Fatal("planned while the native writer lives")
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphaned driver %d did not exit", driver)
		}
	}
	plans := PlanRuntimeHARWorkingCleanup(dir, 128)
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
