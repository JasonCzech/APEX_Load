package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestJavaFixtureCommandWritesValidatedDSL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.json")
	args := []string{"fixture", "-workload", "middleware-heavy", "-classpath", "/approved/fixtures.jar", "-classpath", "/approved/dependencies.jar", "-output", path}
	if err := runJava(args); err != nil {
		t.Fatal(err)
	}
	script, err := dsl.ParseFile(path)
	if err != nil || script.Protocol != "java_vuser" || script.Actions[0].Target != "HeavyActions" {
		t.Fatalf("script=%+v err=%v", script, err)
	}
	if err := runJava(args); err == nil {
		t.Fatal("overwrote evidence without opt-in")
	}
}

func TestJavaCommandsRejectInvalidInput(t *testing.T) {
	badReport := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badReport, []byte(`{"schema":"unrecognized"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		nil, {"unknown"}, {"fixture"}, {"fixture", "-classpath", "a.jar", "extra"},
		{"fixture", "-classpath", "a.jar", "-endpoint", "http://secret:credential@127.0.0.1"},
		{"target", "-addr", "0.0.0.0:18099"}, {"target", "-output", "unwanted.json"},
		{"baseline", "unexpected"}, {"pilot"}, {"pilot", "unexpected"}, {"baseline", "-pilot-manifest", "/operator/pilot.json"}, {"pilot", "-preparation-timeout", "2h", "-pilot-manifest", "/operator/pilot.json"}, {"compare", "-baseline", badReport, "-candidate", badReport},
	} {
		if err := runJava(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestJavaPilotCommandRequiresManifestBeforeExecution(t *testing.T) {
	err := runJava([]string{"pilot"})
	if err == nil || !strings.Contains(err.Error(), "pilot configuration") {
		t.Fatal("pilot bypassed explicit configuration", err)
	}
}

func TestJavaInspectCommandWritesBoundedEvidence(t *testing.T) {
	dir := t.TempDir()
	source, output := filepath.Join(dir, "Actions.java"), filepath.Join(dir, "inspection.json")
	if err := os.WriteFile(source, []byte("class Actions {public native int call();}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runJava([]string{"inspect", "--artifact", source, "--output", output}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"assessment": "native_indicators_found"`) || strings.Contains(string(data), "public native") {
		t.Fatal(string(data))
	}
	if err := runJava([]string{"inspect"}); err == nil {
		t.Fatal("empty inspection accepted")
	}
}
