package scripts_test

import (
	"os/exec"
	"testing"
)

func TestNodeFaultHarnessSafety(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required")
	}
	cmd := exec.Command("python3", "-m", "unittest", "discover", "-s", "fault_harness", "-p", "test_*.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fault harness: %v\n%s", err, output)
	}
}
