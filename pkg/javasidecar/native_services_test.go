package javasidecar

import (
	"bytes"
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nativeRemoteJavaFixture(t *testing.T) (javarunner.ResolvedJDK, string, string) {
	t.Helper()
	java, e := exec.LookPath("java")
	if e != nil {
		t.Skip("java unavailable")
	}
	jdk, e := javarunner.ResolveJDK(t.Context(), javarunner.ResolveOptions{ExplicitJavaCommand: java})
	if e != nil {
		t.Skipf("JDK unavailable %v", e)
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "harness")
	actions := filepath.Join(dir, "actions")
	for _, p := range []string{classes, actions} {
		if e = os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	source := filepath.Join(dir, "Actions.java")
	code := `import apexload.data.Data;import java.util.Collections;
 public class Actions {public int init(){if(!"ok".equals(Data.table("work").push(Collections.singletonMap("id","{{secret.literal}}雪")).status()))throw new RuntimeException("push");return 0;}
 public int action(){Data.Result r=Data.table("work").pop();if(!"empty".equals(r.status())||r.row()!=null)throw new RuntimeException("empty");return 0;}
 public int end(){return 0;}}`
	if e = os.WriteFile(source, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	run := func(name string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		if out, e := exec.CommandContext(ctx, name, args...).CombinedOutput(); e != nil {
			t.Fatalf("%s: %v %s", name, e, out)
		}
	}
	args := []string{"--release", "8", "-d", classes, "../../runners/java_vuser/lrapi/lr.java", "../../runners/java_vuser/Harness.java"}
	for _, p := range []string{"../../runners/java_vuser/apexload/data/*.java", "../../runners/java_vuser/apexload/bridge/Native*.java"} {
		paths, _ := filepath.Glob(p)
		args = append(args, paths...)
	}
	run("javac", args...)
	run("javac", "--release", "8", "-cp", classes, "-d", actions, source)
	harnessJar := filepath.Join(dir, "harness.jar")
	actionsJar := filepath.Join(dir, "actions.jar")
	run("jar", "--create", "--file", harnessJar, "--main-class", "Harness", "-C", classes, ".")
	run("jar", "--create", "--file", actionsJar, "-C", actions, ".")
	return jdk, harnessJar, actionsJar
}
func nativeRemoteCallback(t *testing.T, count *atomic.Int32) javarunner.NativeDataCallback {
	return func(_ context.Context, seq uint64, payload []byte) ([]byte, error) {
		count.Add(1)
		if seq != 1 {
			t.Error("callback sequence crossed invocation")
		}
		if bytes.Contains(payload, []byte(`"operation":"push"`)) {
			if !bytes.Contains(payload, []byte("{{secret.literal}}雪")) {
				t.Error("literal changed")
			}
			return []byte(`{"result":{"status":"ok","revision":"1","count":"1"}}`), nil
		}
		if !bytes.Contains(payload, []byte(`"operation":"pop"`)) {
			return nil, errors.New("unexpected operation")
		}
		return []byte(`{"result":{"status":"empty","revision":"1","count":"0"}}`), nil
	}
}
func nativePodFixture(t *testing.T, jdk javarunner.ResolvedJDK, harness, actions string) *DedicatedPodRuntime {
	t.Helper()
	p := DedicatedPodProfile{JDK: jdk, JavaSHA256: podAsset(t, jdk.JavaBin).SHA256, Harness: podAsset(t, harness), Classpath: []PodAsset{podAsset(t, actions)}, Command: ActionCommand{ClassName: "Actions"}}
	p, e := BindDedicatedPodContext(p, javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: "session", VUserID: 1, GroupName: "group", ScenarioID: "run"})
	if e != nil {
		t.Fatal(e)
	}
	guest, e := newDedicatedPodRuntime(p, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		guest.Close()
		guest.mu.Lock()
		s := guest.session
		guest.mu.Unlock()
		if s != nil {
			_ = s.cmd.Process.Kill()
			select {
			case <-s.waitDone:
			case <-time.After(5 * time.Second):
				t.Error("fixture JVM reap timeout")
			}
			s.cleanup()
		}
	})
	return guest
}

// Real JVM and real authenticated HTTP paths. Private process hooks are used
// solely for fixtures; these tests do not attest pod descendant termination.
func TestNativeJavaRemotePaths(t *testing.T) {
	jdk, harness, actions := nativeRemoteJavaFixture(t)
	t.Run("sidecar", func(t *testing.T) {
		implementation := NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{Enabled: true, AuthToken: testSidecarAuthToken, DefaultJDKHome: jdk.Home, HarnessJar: harness, MaxProcesses: 1}, CredentialCheck: func() error { return nil }, supportsStrongProcessContainment: func() bool { return true }, startStrongProcessTree: javarunner.StartProcessTree})
		server := httptest.NewServer(implementation)
		defer server.Close()
		client := newClaimedTestClient(t, server.URL, server.Client(), "native-sidecar")
		stop := keepTestOwnerLease(client, "native-sidecar")
		defer stop()
		defer client.CloseSession(context.Background(), "session")
		caps, e := client.Capabilities(t.Context())
		if e != nil || !protocol.RuntimeFeaturesSatisfy(caps.Features, protocol.FeatureNativeDataSidecarV1) {
			t.Fatal("capability", caps, e)
		}
		var count atomic.Int32
		for _, method := range []string{"init", "action", "end"} {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			ctx = javarunner.WithNativeDataCallback(ctx, nativeRemoteCallback(t, &count))
			out, e := client.Execute(ctx, ExecutionRequest{ExecutionID: "invoke-" + method, SessionID: "session", Runtime: RuntimeSettings{Classpath: []string{actions}, DedicatedClassLoader: true}, Command: ActionCommand{ActionType: "java_vuser_run", ClassName: "Actions", Method: method}, TimeoutMS: 10000})
			cancel()
			if e != nil || !out.Success {
				t.Fatalf("%s: %+v %v", method, out, e)
			}
		}
		if count.Load() != 2 {
			t.Fatal(count.Load())
		}
	})
	t.Run("broker_facade", func(t *testing.T) {
		guest := nativePodFixture(t, jdk, harness, actions)
		remote, _, _, _ := facadeBrokerFixtureWrapped(t, false, nil, guest)
		token := strings.Repeat("x", 32)
		facade, e := NewBrokerFacade(t.Context(), remote, token)
		if e != nil {
			t.Fatal(e)
		}
		defer facade.Close()
		server := httptest.NewServer(facade)
		defer server.Close()
		client, e := NewBrokerFacadeClient(server.URL, token, 10*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		defer client.Close()
		if e = client.Claim(t.Context()); e != nil {
			t.Fatal(e)
		}
		run := facadePrepared(t, client, time.Minute)
		if !run.NativeDataAvailable() {
			t.Fatal("prepared SDK proof was lost across broker/facade")
		}
		if e = run.Commit(t.Context()); e != nil {
			t.Fatal(e)
		}
		session, e := run.Activate(t.Context(), "session")
		if e != nil {
			t.Fatal(e)
		}
		if !guest.NativeDataAvailable() {
			t.Fatal("actual SDK readiness not negotiated")
		}
		var count atomic.Int32
		for _, method := range []string{"init", "action", "end"} {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			ctx = javarunner.WithNativeDataCallback(ctx, nativeRemoteCallback(t, &count))
			out, e := session.Execute(ctx, method, nil)
			cancel()
			if e != nil || !out.Success {
				t.Fatalf("%s: %+v %v", method, out, e)
			}
		}
		if count.Load() != 2 {
			t.Fatal("callbacks lost across authenticated hops", count.Load())
		}
	})
}
