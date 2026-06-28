package java_vuser_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
