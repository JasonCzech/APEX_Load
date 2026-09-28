package javasidecar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func podAsset(t *testing.T, path string) PodAsset {
	t.Helper()
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(data)
	return PodAsset{path, hex.EncodeToString(sum[:])}
}
func TestPodRuntimePublicConstructorRequiresPID1(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("ordinary process only")
	}
	if _, e := NewDedicatedPodRuntime(DedicatedPodProfile{}, t.TempDir()); !errors.Is(e, javarunner.ErrPID1Isolation) {
		t.Fatal(e)
	}
}
func TestPodAssetsAreBoundedAndHashBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.jar")
	if e := os.WriteFile(path, []byte("approved"), 0600); e != nil {
		t.Fatal(e)
	}
	asset := podAsset(t, path)
	if e := verifyPodAsset(t.Context(), asset); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("modified"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := verifyPodAsset(t.Context(), asset); e == nil {
		t.Fatal("mutation accepted")
	}
	if e := verifyPodAsset(t.Context(), PodAsset{Path: t.TempDir()}); e == nil {
		t.Fatal("directory accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e := verifyPodAsset(ctx, asset); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

// Tests the real reused IPC2 adapter, not PID namespace containment. Production
// construction remains PID1-only; this private helper is confined to the package.
func TestDedicatedPodRuntimeColdPreparationAndPersistentLifecycle(t *testing.T) {
	java, e := exec.LookPath("java")
	if e != nil {
		t.Skip("java unavailable")
	}
	javac, e := exec.LookPath("javac")
	if e != nil {
		t.Skip("javac unavailable")
	}
	jar, e := exec.LookPath("jar")
	if e != nil {
		t.Skip("jar unavailable")
	}
	jdk, e := javarunner.ResolveJDK(t.Context(), javarunner.ResolveOptions{ExplicitJavaCommand: java})
	if e != nil {
		t.Skipf("JDK: %v", e)
	}
	dir := t.TempDir()
	harnessClasses := filepath.Join(dir, "harness")
	actionsClasses := filepath.Join(dir, "actions")
	for _, path := range []string{harnessClasses, actionsClasses} {
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	source := filepath.Join(dir, "Actions.java")
	code := `public class Actions {
 static { System.out.println("STATIC"); }
 private final Thread owner=Thread.currentThread(); private int calls=0;
 public Actions(){System.out.println("CONSTRUCTOR:"+lrapi.lr.get_vuser_id()+":"+lrapi.lr.get_group_name()+":"+lrapi.lr.get_scenario_id());}
 public int init() throws Exception {if(System.in.read()!=-1)throw new Exception("stdin");System.out.println(lrapi.lr.eval_string("{Token}"));return 0;}
 public int action() throws Exception {if(Thread.currentThread()!=owner)throw new Exception("thread");if(++calls==1)throw new Exception("expected lifecycle failure");System.out.println("calls="+calls);return 0;}
 public int end(){System.out.println("END");return 0;}
}`
	if e := os.WriteFile(source, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	commands := [][]string{{javac, "--release", "8", "-d", harnessClasses, "../../runners/java_vuser/lrapi/lr.java", "../../runners/java_vuser/Harness.java"}, {javac, "--release", "8", "-cp", harnessClasses, "-d", actionsClasses, source}, {jar, "--create", "--file", filepath.Join(dir, "harness.jar"), "--main-class", "Harness", "-C", harnessClasses, "."}, {jar, "--create", "--file", filepath.Join(dir, "actions.jar"), "-C", actionsClasses, "."}}
	for _, args := range commands {
		if out, e := exec.Command(args[0], args[1:]...).CombinedOutput(); e != nil {
			t.Fatalf("build: %v %s", e, out)
		}
	}
	p := DedicatedPodProfile{JDK: jdk, JavaSHA256: podAsset(t, jdk.JavaBin).SHA256, Harness: podAsset(t, filepath.Join(dir, "harness.jar")), Classpath: []PodAsset{podAsset(t, filepath.Join(dir, "actions.jar"))}, Command: ActionCommand{ClassName: "Actions"}}
	p, e = BindDedicatedPodContext(p, javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: "vuser-5", VUserID: 5, GroupName: "fixture-group", ScenarioID: "fixture-scenario"})
	if e != nil {
		t.Fatal(e)
	}
	r, e := newDedicatedPodRuntime(p, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// Cleanup a fixture JVM explicitly. Real PID1 exits its namespace; never
	// interpret this fixture's process kill as production termination evidence.
	defer func() {
		r.Close()
		r.mu.Lock()
		s := r.session
		r.mu.Unlock()
		if s != nil {
			_ = s.cmd.Process.Kill()
			select {
			case <-s.waitDone:
			case <-time.After(5 * time.Second):
				t.Error("fixture reap timeout")
			}
			s.cleanup()
		}
	}()
	if e = r.Ready(ctx); e != nil {
		t.Fatal(e)
	}
	if r.RootPID() < 1 {
		t.Fatal("no root")
	}
	if e = r.Ready(ctx); e == nil {
		t.Fatal("preparation replayed")
	}
	for i, method := range []string{"init", "action", "action", "end"} {
		payload, e := r.Invoke(ctx, "host-call-"+string(rune('1'+i)), method, map[string]string{"Token": "host parameter"})
		if e != nil {
			t.Fatal(e)
		}
		var response ExecutionResponse
		if e = json.Unmarshal(payload, &response); e != nil {
			t.Fatal(e)
		}
		if response.Success != (i != 1) {
			t.Fatalf("lifecycle %s: %+v", method, response)
		}
		if i == 0 {
			for _, want := range []string{"STATIC", "CONSTRUCTOR:5:fixture-group:fixture-scenario", "host parameter"} {
				if !strings.Contains(response.Stdout, want) {
					t.Fatalf("init before commit or parameter isolation failed: %+v", response)
				}
			}
		}
		if i == 2 && !strings.Contains(response.Stdout, "calls=2") {
			t.Fatal("session was restarted", response)
		}
	}
	select {
	case <-r.Done():
		t.Fatal("ordinary lifecycle failure retired runtime")
	default:
	}
	r.Close()
	if _, e = r.Invoke(ctx, "host-call-5", "action", nil); e == nil {
		t.Fatal("closed runtime invoked")
	}
}

func TestPodRuntimeRejectsScratchAssetThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix image symlinks")
	}
	root := t.TempDir()
	work := filepath.Join(root, "scratch")
	assets := filepath.Join(root, "image")
	for _, dir := range []string{work, assets} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	write := func(path string) PodAsset {
		t.Helper()
		if e := os.WriteFile(path, []byte("fixture"), 0600); e != nil {
			t.Fatal(e)
		}
		return podAsset(t, path)
	}
	java := write(filepath.Join(assets, "java"))
	harness := write(filepath.Join(assets, "harness.jar"))
	script := write(filepath.Join(work, "script.jar"))
	alias := filepath.Join(assets, "script.jar")
	if e := os.Symlink(script.Path, alias); e != nil {
		t.Fatal(e)
	}
	script.Path = alias
	p := DedicatedPodProfile{JDK: javarunner.ResolvedJDK{Home: assets, JavaBin: java.Path, Version: "test", Major: 17, VMFamily: javarunner.JavaVMHotSpot}, JavaSHA256: java.SHA256, Harness: harness, Classpath: []PodAsset{script}, Command: ActionCommand{ClassName: "Actions"}}
	if _, e := newDedicatedPodRuntime(p, work); !errors.Is(e, ErrPodRuntime) {
		t.Fatal("writable scratch artifact hidden behind image symlink", e)
	}
}
