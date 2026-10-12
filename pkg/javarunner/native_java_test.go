package javarunner

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeJavaClasses(t *testing.T, actions string) (string, string) {
	t.Helper()
	javac, e := exec.LookPath("javac")
	if e != nil {
		t.Skip("javac unavailable")
	}
	java, e := exec.LookPath("java")
	if e != nil {
		t.Skip("java unavailable")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if e = os.Mkdir(classes, 0700); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(dir, "Actions.java")
	if e = os.WriteFile(source, []byte(actions), 0600); e != nil {
		t.Fatal(e)
	}
	args := []string{"--release", "8", "-d", classes, "../../runners/java_vuser/lrapi/lr.java", "../../runners/java_vuser/Harness.java", source}
	for _, pattern := range []string{"../../runners/java_vuser/apexload/data/*.java", "../../runners/java_vuser/apexload/bridge/*.java", "../../runners/java_vuser/apexload/host/*.java"} {
		files, e := filepath.Glob(pattern)
		if e != nil {
			t.Fatal(e)
		}
		args = append(args, files...)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if out, e := exec.CommandContext(ctx, javac, args...).CombinedOutput(); e != nil {
		t.Fatalf("javac: %v %s", e, out)
	}
	return java, classes
}

const nativeSDKActions = `import apexload.data.Data;
import java.util.*;
public class Actions {
 public int init(){Data.Result r=Data.table("work").push(Collections.singletonMap("id","{{secret.literal}}雪"));if(!"ok".equals(r.status()))throw new RuntimeException("push failed");return 0;}
 public int action() throws Exception {
   final boolean[] rejected={false};Thread child=new Thread(()->{try{Data.table("work").pop();}catch(Data.Failure e){rejected[0]=true;}});child.start();child.join();if(!rejected[0])throw new RuntimeException("background authority");
   Data.Result r=Data.table("work").lease(1,30000);if(!"leased".equals(r.leaseRef()))throw new RuntimeException("lease reference");if(!"雪".equals(r.row().values().get("id"))||!r.row().values().containsKey("empty")||r.row().values().containsKey("absent"))throw new RuntimeException("cell semantics");Data.table("work").ack(r.leaseRef());return 0;
 }
 public int end(){return 0;}
}`

// Actual Java 8-compatible SDK/Harness framing. Test process hooks make no
// descendant-containment claim; production containment is separately gated.
func TestNativeJavaDedicatedSDKLiteralLeaseAndBackgroundRejection(t *testing.T) {
	java, classes := nativeJavaClasses(t, nativeSDKActions)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, java, "-cp", classes, "Harness", "--session", "--class", "Actions", "--method", "init")
	input, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { input.Close(); cancel(); cmd.Wait() }()
	scan := bufio.NewScanner(output)
	scan.Buffer(make([]byte, 4096), NativeDataFrameBytes+128)
	secret := make([]byte, 32)
	calls := 0
	for index, method := range []string{"init", "action", "end"} {
		id := "invoke-" + method
		command := map[string]any{"protocol_version": "apexload.java_vuser.ipc.v2", "event_contract": "apexload.java_vuser.lrapi_events.v2", "request_id": id, "class_name": "Actions", "method": method, "native_services": true}
		if index == 0 {
			command["session_secret"] = hex.EncodeToString(secret)
		}
		if e = json.NewEncoder(input).Encode(command); e != nil {
			t.Fatal(e)
		}
		for {
			if !scan.Scan() {
				t.Fatalf("harness exited %v", scan.Err())
			}
			line := scan.Text()
			if strings.HasPrefix(line, NativeDataPrefix) {
				f, e := DecodeNativeDataFrame(strings.TrimPrefix(line, NativeDataPrefix), secret, false)
				if e != nil || f.Scope != "dedicated" || f.Invocation != id {
					t.Fatalf("frame=%+v err=%v", f, e)
				}
				calls++
				var request map[string]any
				if json.Unmarshal(f.Payload, &request) != nil {
					t.Fatal("invalid SDK request")
				}
				switch request["operation"] {
				case "push":
					if request["values"].(map[string]any)["id"] != "{{secret.literal}}雪" {
						t.Fatal("literal changed")
					}
					f.Payload = []byte(`{"result":{"status":"ok","revision":"1","count":"1"}}`)
				case "lease":
					if f.Sequence != 1 {
						t.Fatal("invocation sequence leaked")
					}
					f.Payload = []byte(`{"result":{"status":"ok","revision":"2","count":"1","lease_ref":"leased","row":{"id":"row","version":"18446744073709551615","values":{"id":"雪","empty":""}}}}`)
				case "ack":
					if request["lease_ref"] != "leased" || f.Sequence != 2 {
						t.Fatal("lease authority changed")
					}
					f.Payload = []byte(`{"result":{"status":"ok","revision":"3","count":"0"}}`)
				default:
					t.Fatal("unexpected callback", request["operation"])
				}
				encoded, e := EncodeNativeDataFrame(f, secret, true)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = io.WriteString(input, NativeDataPrefix+encoded+"\n"); e != nil {
					t.Fatal(e)
				}
				continue
			}
			if strings.HasPrefix(line, "APEXLOAD_JAVA_VUSER_V2 ") {
				var result struct {
					Success bool   `json:"success"`
					Error   string `json:"error"`
				}
				if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "APEXLOAD_JAVA_VUSER_V2 ")), &result); e != nil || !result.Success {
					t.Fatalf("method=%s result=%+v err=%v", method, result, e)
				}
				break
			}
		}
	}
	if calls != 3 {
		t.Fatalf("callbacks=%d", calls)
	}
}
