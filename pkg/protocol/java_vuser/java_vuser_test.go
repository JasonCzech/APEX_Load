package java_vuser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

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
	script := "#!/bin/sh\ncat >/tmp/apexload-java-vuser-stdin\nprintf '{\"success\":true,\"class\":\"ok\"}\\n'\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	var runner JavaVuser
	if err := runner.Init(context.Background(), map[string]any{
		"java_vuser_enabled":           true,
		"java_vuser_runner_command":    runnerPath,
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
