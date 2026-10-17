package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Inspect the production graph reported by Go itself, including the in-process
// recording command; checking only direct CLI imports misses server/assets edges.
func TestCLIProductionDependencyBoundary(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".", "../apexload-record-agent")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve production dependency graph: %v\n%s", err, output)
	}
	sawBridge := false
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == "github.com/jasonczech/apexload/pkg/recordbridge" {
			sawBridge = true
		}
		if dependency == "github.com/jasonczech/apexload/pkg/api" || strings.HasPrefix(dependency, "github.com/jasonczech/apexload/pkg/api/") {
			t.Fatalf("CLI depends on controller or embedded dashboard: %s", dependency)
		}
	}
	if !sawBridge {
		t.Fatal("dependency graph did not include the standalone recording bridge")
	}
}
