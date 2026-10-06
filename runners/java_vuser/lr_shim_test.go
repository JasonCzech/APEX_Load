package java_vuser_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHarnessSessionPersistsActionsAndLRParameters(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatalf("mkdir classes: %v", err)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	actionsSource := `import lrapi.lr;

public class Actions {
  private int count;
  public int initialize() {
    count = 10;
    lr.save_string("persisted", "Token");
    System.out.println("init:" + count);
    return lr.PASS;
  }
  public int Action() {
    count++;
    System.out.println("action:" + count + ":" + lr.eval_string("{Token}"));
    lr.start_transaction("Checkout");
    lr.end_transaction("Checkout", lr.PASS);
    return lr.PASS;
  }
  public int teardown() {
    System.out.println("end:" + count + ":" + lr.eval_string("{Token}"));
    return lr.PASS;
  }
}`
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatalf("write Actions.java: %v", err)
	}
	compile := exec.Command(javac, "-d", classes, "lrapi/lr.java", "Harness.java", actionsPath)
	compile.Dir = "."
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}
	commands := strings.Join([]string{
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"r1","session_secret":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","class_name":"Actions","method":"initialize"}`,
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"r2","class_name":"Actions","method":"Action"}`,
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"r3","class_name":"Actions","method":"Action","params":{"lr_parameters":{"Token":"overlaid"}}}`,
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"r4","class_name":"Actions","method":"teardown"}`,
	}, "\n") + "\n"
	run := exec.Command(javaBin, "-cp", classes, "Harness", "--class", "Actions", "--method", "init", "--session")
	run.Stdin = strings.NewReader(commands)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("persistent Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("session frames = %d, want 4\n%s", len(lines), stdout.String())
	}
	type envelope struct {
		Contract          string `json:"contract"`
		RequestID         string `json:"request_id"`
		FrameToken        string `json:"frame_token"`
		Success           bool   `json:"success"`
		Stdout            string `json:"stdout"`
		Stderr            string `json:"stderr"`
		EventFallbackUsed bool   `json:"event_fallback_used"`
	}
	wantOutput := []string{"init:10", "action:11:persisted", "action:12:overlaid", "end:12:overlaid"}
	sessionSecret, err := hex.DecodeString(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for index, line := range lines {
		const prefix = "APEXLOAD_JAVA_VUSER_V2 "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("frame %d = %q, want v2 prefix", index, line)
		}
		if count := strings.Count(line, `"duration_ms":`); count != 1 {
			t.Fatalf("frame %d contains %d duration_ms keys, want exactly one: %s", index, count, line)
		}
		var got envelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &got); err != nil {
			t.Fatalf("decode frame %d: %v\n%s", index, err, line)
		}
		if got.Contract != "apexload.java_vuser.ipc.v2" || got.RequestID != "r"+strconv.Itoa(index+1) || !got.Success {
			t.Fatalf("frame %d = %#v", index, got)
		}
		mac := hmac.New(sha256.New, sessionSecret)
		_, _ = mac.Write([]byte("apexload.java_vuser.ipc.v2"))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(got.RequestID))
		if got.FrameToken != hex.EncodeToString(mac.Sum(nil)) {
			t.Fatalf("frame %d token = %q, want request-bound HMAC", index, got.FrameToken)
		}
		if !strings.Contains(got.Stdout, wantOutput[index]) {
			t.Fatalf("frame %d stdout = %q, want %q", index, got.Stdout, wantOutput[index])
		}
		if index == 1 && (!strings.Contains(got.Stderr, `"contract":"apexload.java_vuser.lrapi_events.v2"`) ||
			!strings.Contains(got.Stderr, `"request_id":"r2"`) ||
			!strings.Contains(got.Stderr, `"timestamp_ms":`) ||
			!strings.Contains(got.Stderr, `"transaction_id":"r2-tx-`)) {
			t.Fatalf("action event stream = %q, want v2 request/timestamp/transaction identifiers", got.Stderr)
		}
		if index == 1 && !got.EventFallbackUsed {
			t.Fatal("session action did not report stderr event fallback without an event file")
		}
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("unframed process stderr = %q, want user diagnostics inside envelopes", stderr.String())
	}
}

func TestHarnessSessionExitIterationContinuesWithSameActionsState(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatal(err)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	actionsSource := `import lrapi.lr;

public class Actions {
  private int iterations;
  public int init() {
    System.out.println("init");
    return lr.PASS;
  }
  public int action() {
    iterations++;
    System.out.println("action-before-exit:" + iterations);
    lr.start_transaction("Interrupted");
    lr.exit(lr.EXIT_ITERATION_AND_CONTINUE, lr.FAIL);
    System.out.println("action-after-exit");
    return lr.PASS;
  }
  public int end() {
    System.out.println("end:" + iterations);
    return lr.PASS;
  }
}`
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := exec.Command(javac, "--release", "17", "-d", classes, "lrapi/lr.java", "Harness.java", actionsPath)
	compile.Dir = "."
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}
	commands := strings.Join([]string{
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"exit-1","session_secret":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","class_name":"Actions","method":"init"}`,
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"exit-2","class_name":"Actions","method":"action"}`,
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"exit-3","class_name":"Actions","method":"action"}`,
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"exit-4","class_name":"Actions","method":"end"}`,
	}, "\n") + "\n"
	run := exec.Command(javaBin, "-cp", classes, "Harness", "--session")
	run.Stdin = strings.NewReader(commands)
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("session run failed: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 4 {
		t.Fatalf("frames = %d, want 4\n%s", len(lines), output)
	}
	type envelope struct {
		Success bool   `json:"success"`
		Stdout  string `json:"stdout"`
		Stderr  string `json:"stderr"`
	}
	frames := make([]envelope, 0, len(lines))
	for index, line := range lines {
		const prefix = "APEXLOAD_JAVA_VUSER_V2 "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("frame %d = %q", index, line)
		}
		var frame envelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &frame); err != nil {
			t.Fatalf("decode frame %d: %v", index, err)
		}
		if !frame.Success {
			t.Fatalf("frame %d = %#v, controlled continuation must not poison the VUser", index, frame)
		}
		frames = append(frames, frame)
	}
	if !strings.Contains(frames[0].Stdout, "init") ||
		!strings.Contains(frames[1].Stdout, "action-before-exit:1") ||
		!strings.Contains(frames[2].Stdout, "action-before-exit:2") ||
		!strings.Contains(frames[3].Stdout, "end:2") {
		t.Fatalf("frames = %#v, want init once, two persistent iterations, and end once", frames)
	}
	for _, index := range []int{1, 2} {
		if strings.Contains(frames[index].Stdout, "action-after-exit") {
			t.Fatalf("frame %d continued Java code after lr.exit: %#v", index, frames[index])
		}
		if !strings.Contains(frames[index].Stderr, `"type":"runtime"`) ||
			!strings.Contains(frames[index].Stderr, `"name":"exit"`) ||
			!strings.Contains(frames[index].Stderr, `"message":"2 1"`) ||
			!strings.Contains(frames[index].Stderr, `"type":"transaction_ignored"`) {
			t.Fatalf("frame %d stderr = %q, want bounded failed-exit and ignored-transaction evidence", index, frames[index].Stderr)
		}
		if strings.Contains(frames[index].Stderr, `"type":"transaction_unmatched"`) {
			t.Fatalf("frame %d stderr = %q, interrupted transaction must be ignored", index, frames[index].Stderr)
		}
	}
}

func TestHarnessLegacyFullLifecycleDoesNotApplyPersistentExitContinuation(t *testing.T) {
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int init() { System.out.println("legacy-init"); return lr.PASS; }
  public int action() {
    System.out.println("legacy-action");
    lr.exit(lr.EXIT_ITERATION_AND_CONTINUE, lr.FAIL);
    return lr.PASS;
  }
  public int end() { System.out.println("legacy-end"); return lr.PASS; }
}`)
	if err == nil {
		t.Fatalf("legacy full_lifecycle unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "legacy-init") || !strings.Contains(stdout, "legacy-action") ||
		strings.Contains(stdout, "legacy-end") || !strings.Contains(stdout, `"success":false`) {
		t.Fatalf("stdout = %s, want existing one-shot unwind before end", stdout)
	}
}

func TestHarnessLegacyFullLifecycleResolvesLifecycleAliases(t *testing.T) {
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int vuser_init() { System.out.println("alias-init"); return lr.PASS; }
  public int run() { System.out.println("alias-action"); return lr.PASS; }
  public int vuser_end() { System.out.println("alias-end"); return lr.PASS; }
}`)
	if err != nil {
		t.Fatalf("legacy alias lifecycle failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, marker := range []string{"alias-init", "alias-action", "alias-end", `"success":true`} {
		if !strings.Contains(stdout, marker) {
			t.Fatalf("stdout = %s, want %q", stdout, marker)
		}
	}
}

func TestHarnessLifecycleResolutionSkipsInvalidPreferredAliases(t *testing.T) {
	source := `import lrapi.lr;

public class Actions {
  public static int init() { throw new AssertionError("invalid init invoked"); }
  public int vuser_init() { System.out.println("valid-init"); return lr.PASS; }
  public String action() { throw new AssertionError("invalid action invoked"); }
  public int run() { System.out.println("valid-action"); return lr.PASS; }
  public int end(String ignored) { throw new AssertionError("invalid end invoked"); }
  public int vuser_end() { System.out.println("valid-end"); return lr.PASS; }
}`
	validationStdout, validationStderr, validationErr := runHarnessFixtureWithInput(
		t,
		source,
		`{"class_name":"Actions","method":"full_lifecycle","validation_only":true}`+"\n",
	)
	if validationErr != nil || !strings.Contains(validationStdout, `"success":true`) {
		t.Fatalf(
			"validation did not select valid aliases: %v\nstdout:\n%s\nstderr:\n%s",
			validationErr,
			validationStdout,
			validationStderr,
		)
	}

	stdout, stderr, err := runHarnessFixture(t, source)
	if err != nil {
		t.Fatalf("alias lifecycle failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, marker := range []string{"valid-init", "valid-action", "valid-end", `"success":true`} {
		if !strings.Contains(stdout, marker) {
			t.Fatalf("stdout = %s, want %q", stdout, marker)
		}
	}
}

func TestLRShimCompilesAndEmitsEventsForCommonJavaVuserAPI(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}

	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatalf("mkdir classes: %v", err)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	if err := os.WriteFile(actionsPath, []byte(javaShimActionsSource), 0o644); err != nil {
		t.Fatalf("write Actions.java: %v", err)
	}

	compile := exec.Command(javac, "-d", classes, "lrapi/lr.java", "Harness.java", actionsPath)
	compile.Dir = "."
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}

	run := exec.Command(javaBin, "-cp", classes, "Harness", "--class", "Actions", "--method", "full_lifecycle")
	run.Stdin = strings.NewReader(`{"class_name":"Actions","method":"full_lifecycle","params":{"lr_parameters":{"OrderId":"SEEDED-42","Count":7}}}` + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"success":true`) {
		t.Fatalf("stdout = %s, want success summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"event_fallback_used":true`) {
		t.Fatalf("stdout = %s, want explicit one-shot event fallback signal", stdout.String())
	}
	if !strings.Contains(stderr.String(), `"type":"transaction_start"`) || !strings.Contains(stderr.String(), `"type":"transaction_end"`) {
		t.Fatalf("stderr = %s, want transaction event JSON lines", stderr.String())
	}
	if !strings.Contains(stderr.String(), `log SEEDED-42`) {
		t.Fatalf("stderr = %s, want seeded lr parameter to be visible through lr.eval_string", stderr.String())
	}
}

func TestPrecompiledActionsLinksAgainstEveryClaimedLRDescriptor(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	stubSourceDir := filepath.Join(dir, "stub-source", "lrapi")
	stubClasses := filepath.Join(dir, "stub-classes")
	userClasses := filepath.Join(dir, "user-classes")
	harnessClasses := filepath.Join(dir, "harness-classes")
	for _, path := range []string{stubSourceDir, stubClasses, userClasses, harnessClasses} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stubSource := `package lrapi;
public final class lr {
  public static final int PASS = 0;
  public static final int FAIL = 1;
  public static final int AUTO = 2;
  public static final int EXIT_ITERATION_AND_CONTINUE = 2;
  public static int debug_message(int level, String message) { return 0; }
  public static int end_sub_trans(String name, int status) { return 0; }
  public static int end_sub_transaction(String name, int status) { return 0; }
  public static int end_transaction(String name, int status) { return 0; }
  public static int error_message(String message) { return 0; }
  public static void exit(int continuation, int status) {}
  public static String eval_string(String value) { return value; }
  public static double get_transaction_duration(String name) { return 0; }
  public static int get_transaction_status(String name) { return 0; }
  public static double get_transaction_think_time(String name) { return 0; }
  public static double get_transaction_wasted_time(String name) { return 0; }
  public static int log_message(String message) { return 0; }
  public static int message(String message) { return 0; }
  public static int output_message(String message) { return 0; }
  public static int save_int(int value, String name) { return 0; }
  public static int save_string(String value, String name) { return 0; }
  public static int set_transaction(String name, double duration, int status) { return 0; }
  public static int set_transaction_status(int status) { return 0; }
  public static int start_sub_transaction(String name, String parent) { return 0; }
  public static int start_transaction(String name) { return 0; }
  public static void think_time(double seconds) {}
  public static int vuser_status_message(String message) { return 0; }
  public static int wasted_time(int millis) { return 0; }
}`
	stubPath := filepath.Join(stubSourceDir, "lr.java")
	if err := os.WriteFile(stubPath, []byte(stubSource), 0o644); err != nil {
		t.Fatal(err)
	}
	compileStub := exec.Command(javac, "--release", "17", "-d", stubClasses, stubPath)
	if output, err := compileStub.CombinedOutput(); err != nil {
		t.Fatalf("compile independent lrapi stub: %v\n%s", err, output)
	}
	actionsSource := `import lrapi.lr;
public class Actions {
  public int action() {
    if (lr.save_string("saved", "Token") != 0) return 90;
    if (lr.save_int(7, "Count") != 0) return 91;
    if (!"saved".equals(lr.eval_string("{Token}"))) return 92;
    if (lr.start_transaction("Outer") != 0) return 93;
    lr.get_transaction_duration("Outer");
    lr.get_transaction_status("Outer");
    lr.get_transaction_think_time("Outer");
    lr.get_transaction_wasted_time("Outer");
    if (lr.start_sub_transaction("Sub", "Outer") != 0) return 94;
    if (lr.set_transaction_status(lr.PASS) != 0) return 95;
    if (lr.end_sub_transaction("Sub", lr.AUTO) != 0) return 96;
    if (lr.start_sub_transaction("Alias", "Outer") != 0) return 97;
    if (lr.end_sub_trans("Alias", lr.PASS) != 0) return 98;
    lr.think_time(0.0);
    if (lr.wasted_time(0) != 0) return 99;
    if (lr.end_transaction("Outer", lr.PASS) != 0) return 100;
    if (lr.set_transaction("Manual", 0.001, lr.FAIL) != 0) return 101;
    if (lr.debug_message(16, "debug") != 0) return 102;
    if (lr.log_message("log") != 0) return 103;
    if (lr.message("message") != 0) return 104;
    if (lr.output_message("output") != 0) return 105;
    if (lr.vuser_status_message("status") != 0) return 106;
    if (lr.error_message("error") < 0) return 107;
    return lr.PASS;
  }
}`
	actionsPath := filepath.Join(dir, "Actions.java")
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	compileActions := exec.Command(javac, "--release", "17", "-cp", stubClasses, "-d", userClasses, actionsPath)
	if output, err := compileActions.CombinedOutput(); err != nil {
		t.Fatalf("compile Actions against independent OpenText-shaped stub: %v\n%s", err, output)
	}
	compileHarness := exec.Command(javac, "--release", "17", "-d", harnessClasses, "lrapi/lr.java", "Harness.java")
	compileHarness.Dir = "."
	if output, err := compileHarness.CombinedOutput(); err != nil {
		t.Fatalf("compile governed harness: %v\n%s", err, output)
	}
	run := exec.Command(
		javaBin,
		"-cp",
		strings.Join([]string{harnessClasses, userClasses}, string(filepath.ListSeparator)),
		"Harness",
		"--class",
		"Actions",
		"--method",
		"full_lifecycle",
	)
	run.Stdin = strings.NewReader(`{"class_name":"Actions","method":"full_lifecycle"}` + "\n")
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("precompiled Actions failed to link/run against governed shim: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"success":true`) ||
		!strings.Contains(string(output), `"name":"Manual"`) ||
		!strings.Contains(string(output), `"duration_ms":1`) {
		t.Fatalf("output = %s, want successful execution of every claimed descriptor", output)
	}

	exitActionsPath := filepath.Join(dir, "ExitActions.java")
	exitActionsSource := `import lrapi.lr;
public class ExitActions {
  public int action() {
    lr.exit(lr.EXIT_ITERATION_AND_CONTINUE, lr.FAIL);
    return lr.PASS;
  }
}`
	if err := os.WriteFile(exitActionsPath, []byte(exitActionsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	compileExitActions := exec.Command(
		javac,
		"--release",
		"17",
		"-cp",
		stubClasses,
		"-d",
		userClasses,
		exitActionsPath,
	)
	if output, err := compileExitActions.CombinedOutput(); err != nil {
		t.Fatalf("compile exit Actions against independent stub: %v\n%s", err, output)
	}
	exitRequest := `{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"abi-exit","session_secret":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","class_name":"ExitActions","method":"action"}` + "\n"
	runExit := exec.Command(
		javaBin,
		"-cp",
		strings.Join([]string{harnessClasses, userClasses}, string(filepath.ListSeparator)),
		"Harness",
		"--session",
	)
	runExit.Stdin = strings.NewReader(exitRequest)
	exitOutput, err := runExit.CombinedOutput()
	if err != nil {
		t.Fatalf("precompiled exit Actions failed to link/run: %v\n%s", err, exitOutput)
	}
	if !strings.Contains(string(exitOutput), `"success":true`) ||
		!strings.Contains(string(exitOutput), `\"name\":\"exit\"`) {
		t.Fatalf("exit output = %s, want controlled two-argument exit descriptor execution", exitOutput)
	}
}

func TestHarnessFailsWhenActionMethodIsMissing(t *testing.T) {
	stdout, stderr, err := runHarnessFixture(t, `public class Actions {
  public int init() { return lrapi.lr.PASS; }
  public int end() { return lrapi.lr.PASS; }
}`)
	if err == nil {
		t.Fatalf("Harness succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, `"success":false`) || !strings.Contains(stdout, "action") {
		t.Fatalf("stdout = %s, want failure summary naming missing action", stdout)
	}
}

func TestHarnessFailsOnLifecycleFailReturnCode(t *testing.T) {
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() { return lr.FAIL; }
}`)
	if err == nil {
		t.Fatalf("Harness succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, `"success":false`) || !strings.Contains(stdout, "non-PASS") {
		t.Fatalf("stdout = %s, want non-PASS failure summary", stdout)
	}
}

func TestHarnessStartupTimeoutBoundsClassLoading(t *testing.T) {
	stdout, stderr, err := runHarnessFixtureWithInput(t, `import lrapi.lr;

public class Actions {
  static {
    try {
      Thread.sleep(500);
    } catch (InterruptedException ex) {
      Thread.currentThread().interrupt();
    }
  }
  public int action() { return lr.PASS; }
}`, `{"class_name":"Actions","method":"full_lifecycle","startup_timeout_ms":50}`+"\n")
	if err == nil {
		t.Fatalf("Harness succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, `"success":false`) || !strings.Contains(stdout, "startup timeout") {
		t.Fatalf("stdout = %s, want startup timeout failure", stdout)
	}
}

func TestHarnessJarHasMainClassEntrypoint(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	jarBin, err := exec.LookPath("jar")
	if err != nil {
		t.Skip("jar is not available")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatalf("mkdir classes: %v", err)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	if err := os.WriteFile(actionsPath, []byte(`public class Actions { public int action() { return lrapi.lr.PASS; } }`), 0o644); err != nil {
		t.Fatalf("write Actions.java: %v", err)
	}
	compile := exec.Command(javac, "-d", classes, "lrapi/lr.java", "Harness.java", actionsPath)
	compile.Dir = "."
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}
	jarPath := filepath.Join(dir, "apexload-java-vuser-runner.jar")
	createJar := exec.Command(jarBin, "--create", "--file", jarPath, "--main-class", "Harness", "-C", classes, ".")
	if out, err := createJar.CombinedOutput(); err != nil {
		t.Fatalf("jar failed: %v\n%s", err, out)
	}
	health := exec.Command(javaBin, "-jar", jarPath, "--health")
	healthOutput, err := health.CombinedOutput()
	if err != nil {
		t.Fatalf("java -jar Harness --health failed: %v\n%s", err, healthOutput)
	}
	for _, want := range []string{
		"APEXLOAD_JAVA_VUSER_V2 ",
		`"contract":"apexload.java_vuser.ipc.v2"`,
		`"kind":"capabilities"`,
		`"event_contract":"apexload.java_vuser.lrapi_events.v2"`,
		`"java_vuser_phase_lifecycle_v1"`,
		`"java_vuser_persistent_lifecycle_v1"`,
		`"java_vuser_session_frame_token_v1"`,
	} {
		if !strings.Contains(string(healthOutput), want) {
			t.Fatalf("health output = %s, want %q", healthOutput, want)
		}
	}
	run := exec.Command(javaBin, "-jar", jarPath, "--class", "Actions", "--method", "full_lifecycle")
	run.Stdin = strings.NewReader(`{"class_name":"Actions","method":"full_lifecycle"}` + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("java -jar Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"success":true`) {
		t.Fatalf("stdout = %s, want success summary", stdout.String())
	}
}

func TestLRThinkTimeSleepsAndEmitsEvent(t *testing.T) {
	// The whole Vuser class runs inside one java_vuser_run protocol action, so
	// the engine cannot re-insert pacing between lr.think_time calls: the pause
	// must happen in the JVM or pacing is silently dropped. Measure only the JVM
	// run (runDur), not javac compilation + process setup.
	stdout, stderr, runDur, err := runHarnessFixtureWithInputTimed(t, `import lrapi.lr;

public class Actions {
  public int action() {
    lr.think_time(2.0);
    return lr.PASS;
  }
}`, `{"class_name":"Actions","method":"full_lifecycle"}`+"\n")
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	// think_time(2.0) must actually pace ~2s. Allow generous JVM-startup slack on
	// the upper bound; the lower bound (1.5s) proves the sleep happened and is
	// well clear of a no-op event-only run (~0.5s of pure startup).
	if runDur < 1500*time.Millisecond {
		t.Fatalf("lr.think_time only took %s; want it to sleep for the requested pacing", runDur)
	}
	if !strings.Contains(stderr, `"type":"think_time"`) || !strings.Contains(stderr, `"message":"2000"`) {
		t.Fatalf("stderr = %s, want think_time event with milliseconds", stderr)
	}
}

func TestLRThinkTimeHonorsIgnoreEnv(t *testing.T) {
	// APEXLOAD_IGNORE_THINK_TIME lets the controller disable pacing for a stress
	// run without re-importing the script; the event is still emitted.
	t.Setenv("APEXLOAD_IGNORE_THINK_TIME", "1")
	stdout, stderr, runDur, err := runHarnessFixtureWithInputTimed(t, `import lrapi.lr;

public class Actions {
  public int action() {
    lr.think_time(2.0);
    return lr.PASS;
  }
}`, `{"class_name":"Actions","method":"full_lifecycle"}`+"\n")
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if runDur > 1500*time.Millisecond {
		t.Fatalf("lr.think_time slept for %s with ignore-think-time set; want no pacing", runDur)
	}
	if !strings.Contains(stderr, `"type":"think_time"`) || !strings.Contains(stderr, `"message":"0"`) {
		t.Fatalf("stderr = %s, want think_time event reporting 0ms", stderr)
	}
}

func TestLRAutoTransactionFailsWhenErrorRaisedDuringTransaction(t *testing.T) {
	t.Setenv("APEXLOAD_FAIL_OPEN_TRANSACTIONS_ON_ERROR_MESSAGE", "true")
	// LoadRunner optionally fails all open AUTO transactions when the matching
	// runtime setting is enabled.
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    lr.start_transaction("Login");
    lr.error_message("login failed");
    lr.end_transaction("Login", lr.AUTO);
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, `"type":"transaction_end"`) ||
		!strings.Contains(stderr, `"name":"Login"`) ||
		!strings.Contains(stderr, `"message":"1"`) {
		t.Fatalf("stderr = %s, want transaction_end Login with FAIL status 1", stderr)
	}
}

func TestLRErrorMessageDefaultReturnsLengthWithoutFailingOpenTransaction(t *testing.T) {
	t.Setenv("APEXLOAD_FAIL_OPEN_TRANSACTIONS_ON_ERROR_MESSAGE", "false")
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    lr.start_transaction("Login");
    System.out.println("message-length:" + lr.error_message("login failed"));
    lr.end_transaction("Login", lr.AUTO);
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "message-length:12") {
		t.Fatalf("stdout = %s, want published message-length return", stdout)
	}
	if !strings.Contains(stderr, `"type":"transaction_end"`) ||
		!strings.Contains(stderr, `"name":"Login"`) ||
		!strings.Contains(stderr, `"message":"0"`) {
		t.Fatalf("stderr = %s, want default AUTO transaction PASS", stderr)
	}
}

func TestLRTransactionKindsNamesStatusAndTimingEvidence(t *testing.T) {
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    System.out.println("start:" + lr.start_transaction("Outer"));
    System.out.println("duplicate:" + lr.start_transaction("Outer"));
    System.out.println("empty:" + lr.start_transaction(""));
    System.out.println("control:" + lr.start_transaction("bad\nname"));
    System.out.println("long:" + lr.start_transaction("x".repeat(257)));
    System.out.println("invalid-status:" + lr.end_transaction("Outer", 99));
    System.out.println("wrong-kind-outer:" + lr.end_sub_transaction("Outer", lr.PASS));
    System.out.println("sub:" + lr.start_sub_transaction("Nested", "Outer"));
    System.out.println("wrong-kind-sub:" + lr.end_transaction("Nested", lr.PASS));
    lr.think_time(0.005);
    lr.wasted_time(3);
    System.out.println("set-status:" + lr.set_transaction_status(lr.FAIL));
    System.out.println("statuses:" + lr.get_transaction_status("Outer") + ":" + lr.get_transaction_status("Nested"));
    System.out.println("timing:" + lr.get_transaction_think_time("Nested") + ":" + lr.get_transaction_wasted_time("Nested"));
    System.out.println("end-sub:" + lr.end_sub_transaction("Nested", lr.AUTO));
    System.out.println("end-outer:" + lr.end_transaction("Outer", lr.PASS));
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, want := range []string{
		"start:0",
		"duplicate:-1",
		"empty:-1",
		"control:-1",
		"long:-1",
		"invalid-status:-1",
		"wrong-kind-outer:-1",
		"sub:0",
		"wrong-kind-sub:-1",
		"set-status:0",
		"statuses:1:1",
		"timing:0.005:0.003",
		"end-sub:0",
		"end-outer:0",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %s, want %q", stdout, want)
		}
	}
	if !strings.Contains(stderr, `"parent_transaction_id":"legacy-tx-1"`) {
		t.Fatalf("stderr = %s, want nested transaction parent identifier", stderr)
	}
	if !strings.Contains(stderr, `"raw_duration_ms":`) ||
		!strings.Contains(stderr, `"think_time_ms":5`) ||
		!strings.Contains(stderr, `"wasted_time_ms":3`) {
		t.Fatalf("stderr = %s, want non-lossy raw/think/wasted duration evidence", stderr)
	}
	type transactionTimingEvent struct {
		Type          string `json:"type"`
		Name          string `json:"name"`
		DurationMS    *int64 `json:"duration_ms"`
		RawDurationMS *int64 `json:"raw_duration_ms"`
		WastedTimeMS  *int64 `json:"wasted_time_ms"`
	}
	checked := 0
	for _, line := range strings.Split(stderr, "\n") {
		var event transactionTimingEvent
		if json.Unmarshal([]byte(line), &event) != nil ||
			event.Type != "transaction_end" ||
			(event.Name != "Nested" && event.Name != "Outer") {
			continue
		}
		if event.DurationMS == nil || event.RawDurationMS == nil || event.WastedTimeMS == nil {
			t.Fatalf("transaction timing event = %s, want reported/raw/wasted durations", line)
		}
		wantDuration := *event.RawDurationMS - *event.WastedTimeMS
		if wantDuration < 0 {
			wantDuration = 0
		}
		if *event.DurationMS != wantDuration {
			t.Fatalf(
				"%s duration_ms = %d, want max(0, raw %d - wasted %d) = %d",
				event.Name,
				*event.DurationMS,
				*event.RawDurationMS,
				*event.WastedTimeMS,
				wantDuration,
			)
		}
		checked++
	}
	if checked != 2 {
		t.Fatalf("checked %d completed transaction timing events, want Nested and Outer\nstderr:\n%s", checked, stderr)
	}
}

func TestLRSetTransactionUsesSuppliedDurationAndBusinessStatus(t *testing.T) {
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    System.out.println("set-result:" + lr.set_transaction("Manual", 1.234, lr.FAIL));
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "set-result:0") {
		t.Fatalf("stdout = %s, want technical PASS from set_transaction", stdout)
	}
	for _, want := range []string{
		`"type":"transaction_start"`,
		`"type":"transaction_end"`,
		`"name":"Manual"`,
		`"message":"1"`,
		`"duration_ms":1234`,
		`"raw_duration_ms":1234`,
		`"think_time_ms":0`,
		`"wasted_time_ms":0`,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %s, want %q", stderr, want)
		}
	}
}

func TestLRAutoTransactionPassesWhenErrorPrecedesTransaction(t *testing.T) {
	// An error raised before the transaction opened must not fail it: LR_AUTO
	// only fails on errors during the transaction's own window.
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    lr.error_message("earlier failure");
    lr.start_transaction("Checkout");
    lr.end_transaction("Checkout", lr.AUTO);
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, `"type":"transaction_end"`) ||
		!strings.Contains(stderr, `"name":"Checkout"`) ||
		!strings.Contains(stderr, `"message":"0"`) {
		t.Fatalf("stderr = %s, want transaction_end Checkout with PASS status 0", stderr)
	}
}

func TestLREndTransactionWithoutStartEmitsFailedSampleAndContinues(t *testing.T) {
	// LoadRunner logs an error for lr_end_transaction on a never-started
	// transaction and the Vuser continues. The adapter still needs an explicit
	// failed child sample when the script ignores the FAIL return.
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    int transactionResult = lr.end_transaction("Login", lr.PASS);
    int subTransactionResult = lr.end_sub_transaction("Nested", lr.AUTO);
    System.out.println("unmatched-results:" + transactionResult + ":" + subTransactionResult);
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, `"success":true`) {
		t.Fatalf("stdout = %s, want success summary", stdout)
	}
	if !strings.Contains(stdout, "unmatched-results:-1:-1") {
		t.Fatalf("stdout = %s, want LoadRunner-compatible -1 error returns", stdout)
	}
	if got := strings.Count(stderr, `"type":"transaction_unmatched"`); got != 2 {
		t.Fatalf("stderr = %s, want two transaction_unmatched events", stderr)
	}
	if !strings.Contains(stderr, `"type":"log"`) {
		t.Fatalf("stderr = %s, want log-level warning events", stderr)
	}
	for _, want := range []string{`"name":"Login"`, `"name":"Nested"`, "never started"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %s, want %q in warning events", stderr, want)
		}
	}
}

func TestLRStartSubTransactionRequiresOpenParent(t *testing.T) {
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    int result = lr.start_sub_transaction("Child", "MissingParent");
    System.out.println("start-sub-result:" + result);
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "start-sub-result:-1") {
		t.Fatalf("stdout = %s, want missing-parent return -1", stdout)
	}
	if strings.Contains(stderr, `"type":"transaction_start"`) {
		t.Fatalf("stderr = %s, missing parent must not create a top-level transaction", stderr)
	}
	for _, want := range []string{
		`"type":"transaction_unmatched"`,
		`"name":"Child"`,
		`parent transaction \"MissingParent\" was never started`,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %s, want %q", stderr, want)
		}
	}
}

func TestHarnessSessionShutdownHookCannotBlockExit(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatalf("mkdir classes: %v", err)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	actionsSource := `import lrapi.lr;

public class Actions {
  static {
    Runtime.getRuntime().addShutdownHook(new Thread(() -> {
      for (;;) {
        try {
          Thread.sleep(1000L);
        } catch (InterruptedException ignored) {
        }
      }
    }, "blocking-user-shutdown-hook"));
  }
  public int action() { return lr.PASS; }
}`
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatalf("write Actions.java: %v", err)
	}
	compile := exec.Command(javac, "-d", classes, "lrapi/lr.java", "Harness.java", actionsPath)
	compile.Dir = "."
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	run := exec.CommandContext(ctx, javaBin, "-cp", classes, "Harness", "--class", "Actions", "--method", "action", "--session")
	run.Stdin = strings.NewReader(
		`{"protocol_version":"apexload.java_vuser.ipc.v2","event_contract":"apexload.java_vuser.lrapi_events.v2","request_id":"shutdown-r1","session_secret":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","class_name":"Actions","method":"action"}` + "\n",
	)
	var output bytes.Buffer
	run.Stdout = &output
	start := time.Now()
	err = run.Run()
	duration := time.Since(start)
	if ctx.Err() != nil {
		t.Fatalf("session teardown was retained by a user shutdown hook after %s", duration)
	}
	if err != nil {
		t.Fatalf("session exit failed: %v\nstdout:\n%s", err, output.String())
	}
	if duration > 5*time.Second {
		t.Fatalf("session exit took %s, want bounded shutdown-hook grace", duration)
	}
	if !strings.Contains(output.String(), `"request_id":"shutdown-r1"`) ||
		!strings.Contains(output.String(), `"success":true`) {
		t.Fatalf("stdout = %s, want successful terminal frame before forced halt", output.String())
	}
}

func TestLRStartWithoutEndStillEmitsUnmatched(t *testing.T) {
	// The log-and-continue leniency is only for end-without-start; a transaction
	// left open at the invocation boundary is still an incomplete action and
	// must keep emitting transaction_unmatched so it cannot be reported as a
	// successful business transaction.
	stdout, stderr, err := runHarnessFixture(t, `import lrapi.lr;

public class Actions {
  public int action() {
    lr.start_transaction("Orphan");
    return lr.PASS;
  }
}`)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, `"type":"transaction_unmatched"`) || !strings.Contains(stderr, `"name":"Orphan"`) {
		t.Fatalf("stderr = %s, want transaction_unmatched for the still-open Orphan transaction", stderr)
	}
}

func TestHarnessDedicatedClassLoaderAndValidationOnly(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	harnessClasses := filepath.Join(dir, "harness")
	userClasses := filepath.Join(dir, "user")
	if err := os.MkdirAll(harnessClasses, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userClasses, 0o755); err != nil {
		t.Fatal(err)
	}
	compileHarness := exec.Command(javac, "--release", "17", "-d", harnessClasses, "lrapi/lr.java", "Harness.java")
	compileHarness.Dir = "."
	if out, err := compileHarness.CombinedOutput(); err != nil {
		t.Fatalf("compile harness: %v\n%s", err, out)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	actionsSource := `import java.net.URLClassLoader;
import java.nio.file.Files;
import java.nio.file.Paths;
import lrapi.lr;

public class Actions {
  static {
    String marker = System.getProperty("apexload.validation.marker", "");
    if (!marker.isEmpty()) {
      try { Files.write(Paths.get(marker), new byte[] {1}); }
      catch (Exception ex) { throw new RuntimeException(ex); }
    }
  }
  public int action() {
    ClassLoader own = getClass().getClassLoader();
    if (!(own instanceof URLClassLoader)) throw new IllegalStateException("not URLClassLoader: " + own);
    if (Thread.currentThread().getContextClassLoader() != own) throw new IllegalStateException("TCCL mismatch");
    if (lr.class.getClassLoader() == own) throw new IllegalStateException("lrapi.lr was duplicated in user loader");
    lr.save_string("shared", "LoaderToken");
    if (!"shared".equals(lr.eval_string("{LoaderToken}"))) throw new IllegalStateException("lr state not shared");
    System.out.println("dedicated-loader-ok");
    return lr.PASS;
  }
}`
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	compileActions := exec.Command(javac, "--release", "17", "-cp", harnessClasses, "-d", userClasses, actionsPath)
	if out, err := compileActions.CombinedOutput(); err != nil {
		t.Fatalf("compile Actions: %v\n%s", err, out)
	}

	payload, err := json.Marshal(map[string]any{
		"class_name":             "Actions",
		"method":                 "full_lifecycle",
		"dedicated_class_loader": true,
		"user_classpath":         []string{userClasses},
		"params": map[string]any{
			"dedicated_class_loader": false,
			"validation_only":        true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := exec.Command(javaBin, "-cp", harnessClasses, "Harness", "--class", "Actions", "--method", "full_lifecycle")
	run.Stdin = bytes.NewReader(append(payload, '\n'))
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("dedicated harness failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "dedicated-loader-ok") || !strings.Contains(string(output), `"success":true`) {
		t.Fatalf("output = %s, want dedicated loader evidence and success", output)
	}

	marker := filepath.Join(dir, "initialized")
	validationPayload, err := json.Marshal(map[string]any{
		"class_name":             "Actions",
		"method":                 "full_lifecycle",
		"dedicated_class_loader": true,
		"user_classpath":         []string{userClasses},
		"validation_only":        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	validate := exec.Command(javaBin, "-Dapexload.validation.marker="+marker, "-cp", harnessClasses, "Harness", "--validate")
	validate.Stdin = bytes.NewReader(append(validationPayload, '\n'))
	validationOutput, err := validate.CombinedOutput()
	if err != nil {
		t.Fatalf("validation-only harness failed: %v\n%s", err, validationOutput)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("validation initialized Actions (marker stat = %v), want load without initialization", err)
	}
	if !strings.Contains(string(validationOutput), `"success":true`) {
		t.Fatalf("validation output = %s, want success summary", validationOutput)
	}
}

func TestHarnessValidationRejectsIncompatibleProvisionedActionsSignatures(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	tests := []struct {
		name       string
		source     string
		wantDetail string
	}{
		{
			name:       "non-public class",
			source:     `class Actions { public int action() { return 0; } }`,
			wantDetail: "public, concrete class",
		},
		{
			name:       "abstract class",
			source:     `public abstract class Actions { public int action() { return 0; } }`,
			wantDetail: "public, concrete class",
		},
		{
			name:       "private constructor",
			source:     `public class Actions { private Actions() {} public int action() { return 0; } }`,
			wantDetail: "NoSuchMethodException",
		},
		{
			name:       "static action",
			source:     `public class Actions { public static int action() { return 0; } }`,
			wantDetail: "public non-static zero-argument method returning int",
		},
		{
			name:       "wrong return type",
			source:     `public class Actions { public String action() { return "ok"; } }`,
			wantDetail: "public non-static zero-argument method returning int",
		},
		{
			name:       "invalid optional init without valid alias",
			source:     `public class Actions { public static int init() { return 0; } public int action() { return 0; } }`,
			wantDetail: "public non-static zero-argument method returning int",
		},
		{
			name: "inherited lifecycle",
			source: `class BaseActions { public int action() { return 0; } }
public class Actions extends BaseActions { public Actions() {} }`,
			wantDetail: "required zero-argument lifecycle method was not found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			classes := filepath.Join(dir, "classes")
			if err := os.MkdirAll(classes, 0o755); err != nil {
				t.Fatal(err)
			}
			actionsPath := filepath.Join(dir, "Actions.java")
			if err := os.WriteFile(actionsPath, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			compile := exec.Command(javac, "--release", "17", "-d", classes, "lrapi/lr.java", "Harness.java", actionsPath)
			compile.Dir = "."
			if output, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("javac failed: %v\n%s", err, output)
			}
			validate := exec.Command(javaBin, "-cp", classes, "Harness", "--validate")
			validate.Stdin = strings.NewReader(`{"class_name":"Actions","method":"full_lifecycle","validation_only":true}` + "\n")
			output, err := validate.CombinedOutput()
			if err == nil {
				t.Fatalf("validation unexpectedly succeeded\n%s", output)
			}
			if !strings.Contains(string(output), `"success":false`) ||
				!strings.Contains(string(output), tc.wantDetail) {
				t.Fatalf("validation output = %s, want failure detail %q", output, tc.wantDetail)
			}
		})
	}
}

func runHarnessFixture(t *testing.T, actionsSource string) (string, string, error) {
	return runHarnessFixtureWithInput(t, actionsSource, `{"class_name":"Actions","method":"full_lifecycle"}`+"\n")
}

func runHarnessFixtureWithInput(t *testing.T, actionsSource string, stdin string) (string, string, error) {
	stdout, stderr, _, err := runHarnessFixtureWithInputTimed(t, actionsSource, stdin)
	return stdout, stderr, err
}

// runHarnessFixtureWithInputTimed compiles and runs the shim like
// runHarnessFixtureWithInput, but also returns the wall-clock duration of the
// JVM run alone (excluding javac compilation and command setup) so timing
// assertions measure the shim's behavior, not the toolchain.
func runHarnessFixtureWithInputTimed(t *testing.T, actionsSource string, stdin string) (string, string, time.Duration, error) {
	t.Helper()
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac is not available")
	}
	javaBin, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not available")
	}
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatalf("mkdir classes: %v", err)
	}
	actionsPath := filepath.Join(dir, "Actions.java")
	if err := os.WriteFile(actionsPath, []byte(actionsSource), 0o644); err != nil {
		t.Fatalf("write Actions.java: %v", err)
	}
	compile := exec.Command(javac, "-d", classes, "lrapi/lr.java", "Harness.java", actionsPath)
	compile.Dir = "."
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, out)
	}
	run := exec.Command(javaBin, "-cp", classes, "Harness", "--class", "Actions", "--method", "full_lifecycle")
	run.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	start := time.Now()
	err = run.Run()
	runDur := time.Since(start)
	return stdout.String(), stderr.String(), runDur, err
}

const javaShimActionsSource = `import lrapi.lr;

public class Actions {
  public int init() {
    return lr.PASS;
  }

  public int action() throws Throwable {
    lr.start_transaction("Checkout");
    String order = lr.eval_string("{OrderId}");
    int count = lr.eval_int("3");
    byte[] payload = lr.eval_data("payload");
    lr.save_data(payload, "Payload");
    lr.save_int(count, "Count");
    lr.log("general log");
    lr.log_message("log " + order);
    lr.message("message");
    lr.output_message("output");
    lr.debug_message(1, "debug");
    lr.error_message("soft error");
    lr.rendezvous("Sync");
    lr.start_timer("Timer");
    double elapsed = lr.end_timer("Timer");
    lr.start_sub_transaction("Sub", "Checkout");
    lr.end_sub_transaction("Sub", lr.AUTO);
    lr.end_sub_trans("Alias", lr.AUTO);
    lr.set_transaction("Manual", elapsed, lr.PASS);
    lr.set_transaction_status(lr.PASS);
    int status = lr.get_transaction_status("Checkout");
    double duration = lr.get_transaction_duration("Checkout");
    double think = lr.get_transaction_think_time("Checkout");
    double wasted = lr.get_transaction_wasted_time("Checkout");
    lr.user_data_point("duration", duration + think + wasted + status);
    lr.vuser_status_message("ok");
    lr.wasted_time(1);
    lr.advance_param("OrderId");
    lr.next_row("Orders");
    lr.get_attrib_string("name", "fallback");
    lr.get_attrib_double("ratio", 1.5);
    lr.get_attrib_long("count", 7);
    lr.get_debug_message();
    lr.get_group_name();
    lr.get_host_name();
    lr.get_master_host_name();
    lr.get_scenario_id();
    lr.get_vuser_id();
    lr.peek_events();
    lr.redirect("stdout");
    lr.enable_redirection("stderr");
    lr.end_transaction("Checkout", lr.AUTO);
    return lr.PASS;
  }

  public int end() {
    lr.output_message("end");
    return lr.PASS;
  }

  public void compileOnly() {
    lr.aws_get_secret("secret");
    lr.cyberark_get_vault("vault");
    lr.decrypt("cipher");
    lr.decrypt_ex("cipher");
    lr.encrypt_ex("plain");
    lr.deserialize(new byte[0]);
    lr.get_object("obj");
    lr.read_file("missing.txt");
    lr.read_xml("missing.xml");
    lr.save_string("value", "name");
    lr.sleep(1);
    lr.think_time(0.001);
    lr.unmask("masked");
    lr.value_check("actual", "expected");
    lr.exit(lr.EXIT_ACTION_AND_CONTINUE);
    lr.abort();
  }
}
`

// Imported .prm parameters arrive under canonical lowercase names while the
// Java source keeps the recorded spelling, and a script may use custom
// parameter delimiters (.usr ParamLeftBrace/ParamRightBrace).
func TestLREvalStringResolvesCanonicalNamesAndCustomBraces(t *testing.T) {
	source := `import lrapi.lr;

public class Actions {
  public int action() {
    System.out.println("url:" + lr.eval_string("<dbUrl>"));
    System.out.println("mixed:" + lr.eval_string("{connectionFactoryJNDI}|<Missing>|<dbUrl"));
    System.out.println("xml:" + lr.eval_string("<root><ban></ban></root>"));
    return lr.PASS;
  }
}`
	braced := `{"class_name":"Actions","method":"full_lifecycle","params":{"lr_parameters":{"dburl":"jdbc:test","ban":"<dburl>","connectionfactoryjndi":"cf","__apexload_param_left_brace":"<","__apexload_param_right_brace":">"}}}` + "\n"
	stdout, stderr, _, err := runHarnessFixtureWithInputTimed(t, source, braced)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, want := range []string{
		"url:jdbc:test",
		// With <> delimiters, {name} is literal text and an undefined <Missing> stays as written.
		"mixed:{connectionFactoryJNDI}|<Missing>|<dbUrl",
		// A substituted value is not rescanned, and a closing tag names no parameter.
		"xml:<root><dburl></ban></root>",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %s, want %q", stdout, want)
		}
	}
	defaults := `{"class_name":"Actions","method":"full_lifecycle","params":{"lr_parameters":{"connectionfactoryjndi":"cf"}}}` + "\n"
	stdout, stderr, _, err = runHarnessFixtureWithInputTimed(t, `import lrapi.lr;

public class Actions {
  public int action() {
    System.out.println("jndi:" + lr.eval_string("{connectionFactoryJNDI}/{CONNECTIONFACTORYJNDI}/{connection-factory.jndi}"));
    return lr.PASS;
  }
}`, defaults)
	if err != nil {
		t.Fatalf("Harness failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "jndi:cf/cf/{connection-factory.jndi}") {
		t.Fatalf("stdout = %s, want canonical-name resolution with default braces", stdout)
	}
	if strings.Contains(stdout+stderr, "__apexload_param") {
		t.Fatalf("reserved brace keys leaked as parameters: %s %s", stdout, stderr)
	}
}
