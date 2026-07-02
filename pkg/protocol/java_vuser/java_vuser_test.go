package java_vuser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

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
	script := "#!/bin/sh\ncat >" + stdinCapture + "\nprintf '{\"type\":\"transaction_start\",\"name\":\"Checkout\",\"message\":\"\"}\\n' >&2\nprintf '{\"type\":\"log\",\"name\":\"\",\"message\":\"checkpoint\"}\\n' >&2\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":42}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	// The runner command and enablement come from the operator environment,
	// never from the uploaded script config.
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		// Generous ceiling, not a behavior under test: the runner uses this as the
		// spawned-process context deadline, and a tight 2s budget flaked under
		// parallel CPU load (process spawn + stdin/stdout round-trip racing the
		// deadline). The assertions below check invocation + success, not timeout.
		"java_vuser_action_timeout_ms":  float64(60000),
		"java_vuser_startup_timeout_ms": float64(3000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
		Params: map[string]any{
			"method":             "full_lifecycle",
			"source_archive_ref": "artifact-1",
			"lr_parameters": map[string]any{
				"OrderId": "A-100",
				"Count":   3,
			},
		},
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
	if result.ProtocolEvidence["status"] != "passed" {
		t.Fatalf("protocol evidence = %#v, want passed status", result.ProtocolEvidence)
	}
	if result.Extractions["java_vuser_duration_ms"] != "42" || result.Extractions["java_vuser_event_count"] != "2" {
		t.Fatalf("extractions = %#v, want duration and event count", result.Extractions)
	}
	events, ok := result.ProtocolEvidence["runner_events"].([]runnerEvent)
	if !ok || len(events) != 2 || events[0].Type != "transaction_start" || events[0].Name != "Checkout" {
		t.Fatalf("runner events = %#v, want parsed lrapi.lr event JSON lines", result.ProtocolEvidence["runner_events"])
	}
	captured, err := os.ReadFile(stdinCapture)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if !strings.Contains(string(captured), `"lr_parameters"`) || !strings.Contains(string(captured), `"OrderId":"A-100"`) || !strings.Contains(string(captured), `"Count":3`) {
		t.Fatalf("runner stdin = %s, want lr_parameters forwarded to sidecar command", captured)
	}
	if !strings.Contains(string(captured), `"startup_timeout_ms":3000`) {
		t.Fatalf("runner stdin = %s, want startup_timeout_ms forwarded to sidecar command", captured)
	}
}

func TestJavaVuserRunnerFailsClosedOnReportedFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '{\"success\":false,\"error\":\"lr.abort: business failure\"}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(60000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{
		Name:   "Run Actions",
		Type:   "java_vuser_run",
		Target: "Actions",
		Params: map[string]any{"method": "full_lifecycle"},
	})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success {
		t.Fatalf("result.Success = true, want runner-reported failure to fail closed")
	}
	if result.StatusCode != 500 || !strings.Contains(result.ErrorMessage, "lr.abort") {
		t.Fatalf("result = %#v, want status 500 with reported runner error", result)
	}
	if result.ProtocolEvidence["status"] != "failed" {
		t.Fatalf("protocol evidence = %#v, want failed status", result.ProtocolEvidence)
	}
}

func TestJavaVuserRunnerFailsClosedOnMissingSummary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'diagnostic without summary\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(60000),
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
	if result.Success || !strings.Contains(result.ErrorMessage, "no result summary") {
		t.Fatalf("result = %#v, want missing summary failure", result)
	}
}

func TestJavaVuserRunnerFailsOnFailedTransactionEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '{\"type\":\"transaction_end\",\"name\":\"Checkout\",\"message\":\"1\"}\\n' >&2\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":42}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_action_timeout_ms": float64(60000),
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
	if result.Success || !strings.Contains(result.ErrorMessage, "Checkout=1") {
		t.Fatalf("result = %#v, want failed transaction failure", result)
	}
	if result.Extractions["java_vuser_failed_transaction_count"] != "1" {
		t.Fatalf("extractions = %#v, want failed transaction count", result.Extractions)
	}
}

func TestJavaVuserRunnerReadsEventsFromSideChannelFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'diagnostic stderr\\n' >&2\nprintf '{\"type\":\"transaction_end\",\"name\":\"Checkout\",\"message\":\"FAIL\"}\\n' >> \"$APEXLOAD_JAVA_VUSER_EVENT_FILE\"\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":42}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{"java_vuser_action_timeout_ms": float64(60000)}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run Actions", Type: "java_vuser_run", Target: "Actions"})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "Checkout=1") {
		t.Fatalf("result = %#v, want failed transaction from side-channel event", result)
	}
	if result.ProtocolEvidence["runner_stderr"] != "diagnostic stderr\n" {
		t.Fatalf("runner_stderr evidence = %#v, want diagnostics only", result.ProtocolEvidence["runner_stderr"])
	}
	if result.ProtocolEvidence["runner_event_stream_bytes"] == nil {
		t.Fatalf("protocol evidence = %#v, want side-channel event byte count", result.ProtocolEvidence)
	}
}

func TestJavaVuserRunnerWaitDelayBoundsInheritedPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	previousDelay := runnerWaitDelay
	runnerWaitDelay = 50 * time.Millisecond
	t.Cleanup(func() { runnerWaitDelay = previousDelay })

	dir := t.TempDir()
	runnerPath := filepath.Join(dir, "java-vuser-runner")
	script := "#!/bin/sh\ncat >/dev/null\n(sleep 5) &\nprintf '{\"success\":true,\"class_name\":\"Actions\",\"method\":\"full_lifecycle\",\"duration_ms\":1}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerCommand, runnerPath)

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{"java_vuser_action_timeout_ms": float64(60000)}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	start := time.Now()
	result, err := runner.Execute(context.Background(), &protocol.Action{Name: "Run Actions", Type: "java_vuser_run", Target: "Actions"})
	if err != nil {
		t.Fatalf("Execute returned protocol error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Execute took %s; WaitDelay did not bound inherited pipe wait", elapsed)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "WaitDelay") {
		t.Fatalf("result = %#v, want WaitDelay failure", result)
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
