package engine

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// This is an actual SDK/Harness -> authenticated callback -> engine broker ->
// real table round trip. The fixture invokes only the synthetic class below;
// process-containment availability is covered by runtime admission tests.
func TestNativeJavaSDKRealTableLiteralLeaseAndAck(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.Mkdir(classes, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "Actions.java")
	const actions = `import apexload.data.Data;
import java.util.*;
public class Actions {
 public int init() {
   Map<String,String> cells = new LinkedHashMap<>();
   cells.put("id", "{{var.replacement}} {{secret.literal}} 雪");
   cells.put("empty", "");
   if (!"ok".equals(Data.table("work").push(cells).status())) throw new RuntimeException("push failed");
   return 0;
 }
 public int action() {
   Data.Result result = Data.table("work").lease(1000, 30000);
   if (!"ok".equals(result.status()) || result.row() == null) throw new RuntimeException("lease failed");
   Map<String,String> cells = result.row().values();
   if (!"{{var.replacement}} {{secret.literal}} 雪".equals(cells.get("id"))) throw new RuntimeException("literal changed");
   if (!cells.containsKey("empty") || !"".equals(cells.get("empty")) || cells.containsKey("counter")) throw new RuntimeException("cell presence changed");
   if (result.row().id().isEmpty() || result.row().version().isEmpty() || !result.leaseRef().startsWith("native_")) throw new RuntimeException("row or opaque lease metadata missing");
   if (!"ok".equals(Data.table("work").ack(result.leaseRef()).status())) throw new RuntimeException("ack failed");
   return 0;
 }
 public int end() {
   Data.Result result = Data.table("work").pop();
   if (!"empty".equals(result.status()) || result.row() != null) throw new RuntimeException("ack did not consume row");
   return 0;
 }
}`
	if err := os.WriteFile(source, []byte(actions), 0600); err != nil {
		t.Fatal(err)
	}
	compileArgs := []string{"--release", "8", "-d", classes, "../../runners/java_vuser/lrapi/lr.java", "../../runners/java_vuser/Harness.java", source}
	for _, pattern := range []string{"../../runners/java_vuser/apexload/data/*.java", "../../runners/java_vuser/apexload/bridge/*.java"} {
		files, err := filepath.Glob(pattern)
		if err != nil || len(files) == 0 {
			t.Fatalf("SDK sources: %s %v", pattern, err)
		}
		compileArgs = append(compileArgs, files...)
	}
	compileCtx, compileCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer compileCancel()
	if output, err := exec.CommandContext(compileCtx, javac, compileArgs...).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v %s", err, output)
	}

	vu, manager := nativeTestVU(t, livetable.ProfileNative, "push", "lease", "ack", "pop")
	vu.Variables["replacement"] = "must-not-interpolate"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, java, "-cp", classes, "Harness", "--session", "--class", "Actions", "--method", "init")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cancel(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), javarunner.NativeDataFrameBytes+128)
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	calls := 0
	for index, method := range []string{"init", "action", "end"} {
		id := "native-real-table-" + method
		invocation, err := vu.nativeServices.BeginInvocation(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer invocation.Close()
		callbackCtx := javarunner.WithNativeDataCallback(ctx, func(callCtx context.Context, seq uint64, payload []byte) ([]byte, error) {
			request, err := protocol.DecodeNativeDataRequest(payload)
			if err != nil {
				return nil, err
			}
			result, err := invocation.Call(callCtx, seq, request)
			if err != nil {
				return nil, err
			}
			return json.Marshal(struct {
				Result protocol.NativeDataResult `json:"result"`
			}{Result: result})
		})
		command := map[string]any{"protocol_version": "apexload.java_vuser.ipc.v2", "event_contract": "apexload.java_vuser.lrapi_events.v2", "request_id": id, "class_name": "Actions", "method": method, "native_services": true}
		if index == 0 {
			command["session_secret"] = hex.EncodeToString(secret)
		}
		if err := json.NewEncoder(input).Encode(command); err != nil {
			t.Fatal(err)
		}
		for {
			if !scanner.Scan() {
				t.Fatalf("Harness exited during %s: %v", method, scanner.Err())
			}
			line := scanner.Text()
			if strings.HasPrefix(line, javarunner.NativeDataPrefix) {
				frame, err := javarunner.DecodeNativeDataFrame(strings.TrimPrefix(line, javarunner.NativeDataPrefix), secret, false)
				if err != nil || frame.Scope != "dedicated" || frame.Invocation != id {
					t.Fatalf("invalid callback identity: %+v %v", frame, err)
				}
				calls++
				frame.Payload, err = javarunner.NativeDataCallbackFrom(callbackCtx)(callbackCtx, frame.Sequence, frame.Payload)
				if err != nil {
					t.Fatalf("real table operation %d: %v", calls, err)
				}
				encoded, err := javarunner.EncodeNativeDataFrame(frame, secret, true)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(input, javarunner.NativeDataPrefix+encoded+"\n"); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if strings.HasPrefix(line, "APEXLOAD_JAVA_VUSER_V2 ") {
				var response struct {
					Success bool   `json:"success"`
					Error   string `json:"error"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "APEXLOAD_JAVA_VUSER_V2 ")), &response); err != nil || !response.Success {
					t.Fatalf("method=%s response=%+v err=%v", method, response, err)
				}
				break
			}
		}
		invocation.Close()
	}
	if calls != 4 {
		t.Fatalf("callbacks=%d, want push/lease/ack/empty pop", calls)
	}
	result, err := manager.Execute(ctx, "table", livetable.Request{Operation: livetable.OpRead, Position: 1})
	if err != nil || result.Status != livetable.StatusEmpty || result.Row != nil {
		t.Fatalf("ack did not remove real row: %+v %v", result, err)
	}
	vu.liveTables.mu.Lock()
	remainingLeases := len(vu.liveTables.leases)
	vu.liveTables.mu.Unlock()
	if remainingLeases != 0 {
		t.Fatal("ack retained engine lease authority")
	}
}
