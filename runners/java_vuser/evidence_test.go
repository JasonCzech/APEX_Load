package java_vuser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/perfevidence"
)

// This verifies fixture behavior through IPC v2, not containment or memory.
// Dependency resolution is an explicit offline-capable build step, never an
// implicit network dependency of the ordinary Go suite.
func TestJavaEvidenceFixturesThroughDedicatedHarness(t *testing.T) {
	root := os.Getenv("APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR")
	if root == "" {
		t.Skip("build fixtures, then set APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR")
	}
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Fatal(err)
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Fatal(err)
	}
	classes := t.TempDir()
	if out, err := exec.Command(javac, "--release", "8", "-d", classes, "lrapi/lr.java", "Harness.java").CombinedOutput(); err != nil {
		t.Fatalf("compile harness: %v\n%s", err, out)
	}
	deps, err := filepath.Glob(filepath.Join(root, "lib", "*.jar"))
	if err != nil || len(deps) == 0 {
		t.Fatalf("dependencies: %v", err)
	}
	classpath := append([]string{filepath.Join(root, "fixtures.jar")}, deps...)
	server := httptest.NewServer(perfevidence.JavaFixtureTarget())
	defer server.Close()
	for _, target := range []string{"LightActions", "HeavyActions"} {
		t.Run(target, func(t *testing.T) {
			var input bytes.Buffer
			for i, method := range []string{"init", "action", "action", "end"} {
				command := map[string]any{"protocol_version": "apexload.java_vuser.ipc.v2", "event_contract": "apexload.java_vuser.lrapi_events.v2", "request_id": "fixture-" + strconv.Itoa(i), "class_name": target, "method": method, "dedicated_class_loader": true, "user_classpath": classpath, "params": map[string]any{"lr_parameters": map[string]any{"Endpoint": server.URL}}}
				if i == 0 {
					command["session_secret"] = strings.Repeat("a", 64)
				}
				if err := json.NewEncoder(&input).Encode(command); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, java, "-cp", classes, "Harness", "--session")
			cmd.Stdin = &input
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			cmd.Env = append(os.Environ(), "APEXLOAD_JAVA_VUSER_EVENT_FILE="+filepath.Join(t.TempDir(), "events"))
			if err := cmd.Run(); err != nil {
				t.Fatalf("harness: %v\n%s\n%s", err, stdout.String(), stderr.String())
			}
			lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			if len(lines) != 4 {
				t.Fatalf("frames=%d\n%s", len(lines), stdout.String())
			}
			for i, line := range lines {
				var frame struct {
					Success   bool   `json:"success"`
					RequestID string `json:"request_id"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "APEXLOAD_JAVA_VUSER_V2 ")), &frame); err != nil || !frame.Success || frame.RequestID != "fixture-"+strconv.Itoa(i) {
					t.Fatalf("frame %d: %s (%v)", i, line, err)
				}
			}
		})
	}
}

// Representative library-loading/lifecycle evidence for the checked-in fixtures.
// This does not approve arbitrary workloads or establish OS containment or PSS.
func TestJavaEvidenceFixturesThroughPooledHarness(t *testing.T) {
	root := os.Getenv("APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR")
	if root == "" {
		t.Skip("build fixtures, then set APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR")
	}
	javac, e := exec.LookPath("javac")
	if e != nil {
		t.Fatal(e)
	}
	java, e := exec.LookPath("java")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	api, shim := filepath.Join(dir, "api"), filepath.Join(dir, "shim")
	for _, p := range []string{api, shim} {
		if e = os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	run := func(bin string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		if out, e := exec.CommandContext(ctx, bin, args...).CombinedOutput(); e != nil {
			t.Fatalf("fixture: %v\n%s", e, out)
		}
	}
	run(javac, "--release", "8", "-d", api, "apexload/bridge/SlotRuntime.java", "apexload/host/SlotLoader.java", "apexload/host/SlotWorker.java", "apexload/host/PooledHost.java")
	run(javac, "--release", "8", "-cp", api, "-d", shim, "lrapi/lr.java", "lrapi/SlotBridge.java")
	probe := filepath.Join(dir, "EvidenceProbe.java")
	if e = os.WriteFile(probe, []byte(pooledEvidenceProbe), 0600); e != nil {
		t.Fatal(e)
	}
	run(javac, "--release", "8", "-cp", api, "-d", api, probe)
	deps, e := filepath.Glob(filepath.Join(root, "lib", "*.jar"))
	if e != nil || len(deps) == 0 {
		t.Fatal("fixture dependencies unavailable", e)
	}
	server := httptest.NewServer(perfevidence.JavaFixtureTarget())
	defer server.Close()
	for _, target := range []string{"LightActions", "HeavyActions"} {
		t.Run(target, func(t *testing.T) {
			events := t.TempDir()
			args := []string{"-cp", api, "EvidenceProbe", shim, filepath.Join(root, "fixtures.jar"), target, server.URL, events}
			args = append(args, deps...)
			run(java, args...)
			for _, slot := range []string{"a", "b"} {
				data, e := os.ReadFile(filepath.Join(events, slot+".events"))
				if e != nil || !bytes.Contains(data, []byte("order-flow")) {
					t.Fatal("missing fixture transactions", slot, e)
				}
			}
		})
	}
}

const pooledEvidenceProbe = `
import apexload.host.PooledHost;
import apexload.host.SlotWorker;
import java.io.File;
import java.net.URL;
import java.util.*;
import java.util.concurrent.*;
public final class EvidenceProbe {
 public static void main(String[] a) throws Exception {
  URL[] deps=new URL[a.length-5];for(int i=5;i<a.length;i++)deps[i-5]=new File(a[i]).toURI().toURL();
  PooledHost h=new PooledHost("fixture",2,new File(a[0]).toURI().toURL(),new URL[]{new File(a[1]).toURI().toURL()},deps,a[2]);
  String[] slots={"a","b"};
  for(int i=0;i<slots.length;i++){Map<String,String> c=new HashMap<String,String>();c.put("APEXLOAD_VUSER_ID",Integer.toString(i));c.put("APEXLOAD_JAVA_VUSER_EVENT_FILE",a[4]+"/"+slots[i]+".events");h.prepareSlot("fixture",slots[i],c);}
  h.commit("fixture");
  long seq=0;
  for(String method:new String[]{"init","action","action","end"}){
   seq++;List<CompletableFuture<SlotWorker.Result>> results=new ArrayList<CompletableFuture<SlotWorker.Result>>();
   for(String slot:slots)results.add(h.invoke("fixture",slot,seq,slot+seq,method,Collections.singletonMap("Endpoint",a[3])));
   for(CompletableFuture<SlotWorker.Result> f:results){SlotWorker.Result r=f.get(10,TimeUnit.SECONDS);if(r.returnCode!=0||r.exit!=null||r.eventTruncated||r.eventFallback)throw new AssertionError("fixture lifecycle/capture failure");}
  }
  for(String slot:slots)h.closeSlot("fixture",slot,true).get(5,TimeUnit.SECONDS);
  h.awaitClosed(5000);if(h.requiresReap()||!h.isSealed())throw new AssertionError("fixture cleanup failed");
 }
}`
