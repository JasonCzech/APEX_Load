package java_vuser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// resetProcessSlots clears the lazily-initialized, generator-wide process
// limiter so a test can observe its sizing from a fresh operator environment.
func resetProcessSlots() {
	processSlotsOnce = sync.Once{}
	processSlots = nil
}

func TestJavaVuserRunnerIsDisabledByDefault(t *testing.T) {
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run", Type: "java_vuser_run", Target: "Actions"})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success {
		t.Fatal("result.Success = true, want disabled runner failure")
	}
	if result.StatusCode != 500 || result.ErrorMessage == "" {
		t.Fatalf("result = %#v, want status 500 with error message", result)
	}
}

func TestJavaVuserRunnerInvokesConfiguredCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	stdinCapture := filepath.Join(dir, "stdin-capture")
	script := "#!/bin/sh\ncat >" + stdinCapture + "\nprintf '{\"success\":true,\"class\":\"ok\"}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	// The runner command and enablement come from the operator environment,
	// never from the uploaded script config.
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(2000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
		Params: map[string]any{"method": "full_lifecycle", "source_archive_ref": "artifact-1"},
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if !result.Success {
		t.Fatalf("result.Success = false: %s", result.ErrorMessage)
	}
	if result.ResponseBody == "" || result.ProtocolEvidence["protocol"] != javaVuserProtocolName {
		t.Fatalf("result evidence/body = %#v / %q", result.ProtocolEvidence, result.ResponseBody)
	}
}

// TestJavaVuserIgnoresScriptSuppliedRunnerCommand is the core P0 regression
// guard: a script that tries to enable the runner and point it at an
// attacker-controlled command must NOT execute that command. With no operator
// enablement, the runner stays disabled and the script-supplied command is
// never run.
func TestJavaVuserIgnoresScriptSuppliedRunnerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	malicious := filepath.Join(dir, "malicious-runner")
	script := "#!/bin/sh\ntouch " + marker + "\nprintf '{\"success\":true}\\n'\n"
	if err := os.WriteFile(malicious, []byte(script), 0o755); err != nil {
		t.Fatalf("write malicious runner: %v", err)
	}

	// Operator has NOT enabled the runner. The script attempts to enable it and
	// steer the command — these fields must be inert.
	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_enabled":        true,
		"java_vuser_runner_command": malicious,
		"java_vuser_work_dir":       dir,
		"java_vuser_classpath":      []any{"/evil"},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success {
		t.Fatal("result.Success = true, want disabled runner (script must not enable execution)")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("script-supplied command executed — RCE gate bypassed")
	}
}

// TestJavaVuserProcessSlotsSizedFromOperatorEnv verifies the generator-wide
// concurrency cap is taken from the operator env (the script-supplied
// max_processes is ignored), fixing the previously parsed-but-ignored limit.
func TestJavaVuserProcessSlotsSizedFromOperatorEnv(t *testing.T) {
	t.Setenv(config.EnvJavaVuserMaxProcesses, "3")
	resetProcessSlots()
	t.Cleanup(resetProcessSlots)

	if got := cap(processSlotChan()); got != 3 {
		t.Fatalf("process slot capacity = %d, want 3 (from operator env)", got)
	}
}
