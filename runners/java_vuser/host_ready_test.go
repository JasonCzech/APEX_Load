package java_vuser_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDedicatedHostReadinessDoesNotInitializeUserCode(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	classes := t.TempDir()
	source := filepath.Join(classes, "Actions.java")
	code := `public class Actions {
 static { System.out.println("STATIC_INITIALIZED"); }
 public Actions() { System.out.println("CONSTRUCTED"); }
 public int init() throws Exception {
   if (System.in.read() != -1) throw new Exception("user can read control input");
   System.out.println("INITIALIZED"); return 0;
 }
 public int action() { return 0; }
 public int end() { return 0; }
}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(javac, "--release", "8", "-d", classes, "lrapi/lr.java", "Harness.java", source).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, out)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	nonce := strings.Repeat("b", 64)
	cmd := exec.CommandContext(ctx, java, "-cp", classes, "Harness", "--session", "--class", "Actions", "--host-ready-nonce", nonce)
	cmd.Env = append(os.Environ(), "APEXLOAD_JAVA_VUSER_EVENT_FILE="+filepath.Join(classes, "events"))
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scan := bufio.NewScanner(output)
	if !scan.Scan() || scan.Text() != "APEXLOAD_JAVA_HOST_READY_V1 "+nonce {
		t.Fatalf("ready: %q %v", scan.Text(), scan.Err())
	}
	// Initialization output is kept in the next authenticated result. If either
	// initializer ran before readiness, the per-invocation capture reset loses it.
	q := map[string]any{"protocol_version": "apexload.java_vuser.ipc.v2", "event_contract": "apexload.java_vuser.lrapi_events.v2", "request_id": "host-call-1", "session_secret": strings.Repeat("a", 64), "class_name": "Actions", "method": "init", "params": map[string]any{}}
	if err := json.NewEncoder(input).Encode(q); err != nil {
		t.Fatal(err)
	}
	if !scan.Scan() {
		t.Fatal("no init response", scan.Err())
	}
	var result struct {
		Success bool   `json:"success"`
		Stdout  string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(scan.Text(), "APEXLOAD_JAVA_VUSER_V2 ")), &result); err != nil || !result.Success {
		t.Fatalf("init: %v %s", err, scan.Text())
	}
	for _, marker := range []string{"STATIC_INITIALIZED", "CONSTRUCTED", "INITIALIZED"} {
		if !strings.Contains(result.Stdout, marker) {
			t.Fatalf("missing %s: %s", marker, result.Stdout)
		}
	}
}
