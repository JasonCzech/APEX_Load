package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Execute the actual command in a child process so its exit policy is checked
// along with the rendered verdict, including failing runs that call os.Exit.
func TestStatusCommandProcess(t *testing.T) {
	raw := os.Getenv("APEXLOAD_TEST_STATUS_COMMAND_ARGS")
	if raw == "" {
		return
	}
	if err := json.Unmarshal([]byte(raw), &os.Args); err != nil {
		t.Fatal(err)
	}
	statusCmd()
	os.Exit(0)
}

func TestStatusCommandPreservesSLAEvaluation(t *testing.T) {
	for _, tc := range []struct {
		name, status, verdict string
		evaluated, breached   bool
		wantExit              int
	}{
		{name: "unevaluated complete", status: "COMPLETE", verdict: "not evaluated"},
		{name: "unevaluated running", status: "RUNNING", verdict: "not evaluated"},
		{name: "evaluated pass", status: "COMPLETE", evaluated: true, verdict: "passing"},
		{name: "evaluated breach", status: "COMPLETE", evaluated: true, breached: true, verdict: "breached", wantExit: exitCodeTestFailure},
		{name: "unevaluated failed run", status: "FAILED", verdict: "not evaluated", wantExit: exitCodeTestFailure},
		{name: "evaluated failed run", status: "FAILED", evaluated: true, verdict: "passing", wantExit: exitCodeTestFailure},
		{name: "partial evaluation breach", status: "COMPLETE", breached: true, verdict: "breached", wantExit: exitCodeTestFailure},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/v1/tests/test-1/sla-status" {
						t.Errorf("unexpected path %s", r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"test_id":"test-1","status":%q,"sla_evaluated":%t,"sla_breached":%t}`, tc.status, tc.evaluated, tc.breached)
				}))
				defer server.Close()
				args, err := json.Marshal([]string{"apexctl", "status", "--api", server.URL, "--test", "test-1", "--format", format})
				if err != nil {
					t.Fatal(err)
				}
				command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStatusCommandProcess$")
				command.Env = append(os.Environ(), "APEXLOAD_TEST_STATUS_COMMAND_ARGS="+string(args))
				output, err := command.CombinedOutput()
				exitCode := 0
				if err != nil {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) {
						t.Fatalf("status process: %v", err)
					}
					exitCode = exitErr.ExitCode()
				}
				if exitCode != tc.wantExit {
					t.Fatalf("exit = %d, want %d; output = %s", exitCode, tc.wantExit, output)
				}
				if format == "text" {
					if !strings.Contains(string(output), "SLA:    "+tc.verdict+"\n") {
						t.Errorf("output = %s, want SLA %s", output, tc.verdict)
					}
					return
				}
				var got map[string]any
				if err := json.Unmarshal(output, &got); err != nil {
					t.Fatalf("status JSON: %v: %s", err, output)
				}
				if got["test_id"] != "test-1" || got["status"] != tc.status || got["sla_evaluated"] != tc.evaluated || got["sla_breached"] != tc.breached || got["failed"] != (tc.wantExit != 0) {
					t.Errorf("status JSON lost evaluation or verdict: %s", output)
				}
			})
		}
	}
}
