package scripts_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Check the actual transitive dependency graph of all shipped command entrypoints
// and the desktop shell. Merely hiding a control or disabling a runtime flag must
// not allow a retired implementation back into a release binary.
func TestShippedBuildsExcludeRetiredRuntimes(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "./cmd/...", "./desktop")
	command.Dir = ".."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect shipped dependency graph: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, retired := range []string{"/pkg/appdynamics", "/pkg/protocol/bruno", "/_retired/"} {
			if strings.Contains(dependency, retired) {
				t.Errorf("shipped build imports retired runtime: %s", dependency)
			}
		}
	}
}

func TestRetiredArchivesPreserveSourceBytes(t *testing.T) {
	command := exec.Command("python3", "test_retired_archives.py")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("verify retained archive checksums: %v\n%s", err, output)
	}
}
