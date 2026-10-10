package scripts_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runE2EAgainstHeldPort runs scripts/run-e2e.sh with its port held by this
// test and returns its exit error and combined output.
func runE2EAgainstHeldPort(t *testing.T, path string) (error, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "run-e2e.sh", "nothing.spec.ts")
	cmd.Env = append(os.Environ(), "APEXLOAD_E2E_PORT="+strconv.Itoa(port), "APEXLOAD_E2E_PROFILE=dev-admin", "PATH="+path)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("run-e2e.sh did not stop at the held port:\n%s", output)
	}
	text := string(output)
	if !strings.Contains(text, "port "+strconv.Itoa(port)+" is already in use") {
		t.Fatalf("run-e2e.sh output does not refuse port %d:\n%s", port, text)
	}
	return err, text
}

// TestRunE2ERefusesAnOccupiedPort: a run must only ever test the controller
// it starts. With its port already held, the health probe would answer from
// the other server, so the script refuses the port before building anything.
func TestRunE2ERefusesAnOccupiedPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run-e2e.sh is a bash script for macOS and Linux")
	}
	err, output := runE2EAgainstHeldPort(t, os.Getenv("PATH"))
	if err == nil {
		t.Fatalf("run-e2e.sh succeeded against a held port:\n%s", output)
	}
	if strings.Contains(output, "Building controller") {
		t.Fatalf("run-e2e.sh built the controller before refusing the port:\n%s", output)
	}

	// Where lsof cannot see the holder (not installed, or another user's
	// process), a connection that succeeds still marks the port as held.
	tools := t.TempDir()
	writeExecutable(t, filepath.Join(tools, "lsof"), "#!/bin/sh\nexit 1\n")
	err, output = runE2EAgainstHeldPort(t, tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err == nil || strings.Contains(output, "Building controller") {
		t.Fatalf("run-e2e.sh without a working lsof did not refuse the held port (err %v):\n%s", err, output)
	}
}

// TestRunE2EConfirmsItsOwnListener keeps the second half of the guarantee:
// after the controller answers, the script checks the process it started is
// the one listening, before the auth check and the specs run.
func TestRunE2EConfirmsItsOwnListener(t *testing.T) {
	content, err := os.ReadFile("run-e2e.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, want := range []string{
		`lsof -nP -a -p "$pid" -iTCP:"$port" -sTCP:LISTEN -t`,
		"require_own_listener\n  echo \"Controller healthy.\"",
		"start_controller() {\n  require_free_port",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("run-e2e.sh lacks %q", want)
		}
	}
	if strings.Index(script, "# Refuse an occupied port before spending time on the build.\nrequire_free_port") > strings.Index(script, `go build -trimpath -o "$controller_bin" ./cmd/controller`) {
		t.Error("run-e2e.sh checks the port only after building the controller")
	}
}
