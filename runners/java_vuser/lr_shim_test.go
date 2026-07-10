package java_vuser_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if !strings.Contains(stderr.String(), `"type":"transaction_start"`) || !strings.Contains(stderr.String(), `"type":"transaction_end"`) {
		t.Fatalf("stderr = %s, want transaction event JSON lines", stderr.String())
	}
	if !strings.Contains(stderr.String(), `log SEEDED-42`) {
		t.Fatalf("stderr = %s, want seeded lr parameter to be visible through lr.eval_string", stderr.String())
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
    return lr.think_time(2.0);
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
    return lr.think_time(2.0);
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
	// LoadRunner LR_AUTO fails a transaction if an error was raised while it was
	// open. Regression guard for the "Java false-success" P0: end_transaction
	// with LR_AUTO after lr.error_message must emit a FAIL (status 1) so the Go
	// adapter marks the run failed instead of reporting success.
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
