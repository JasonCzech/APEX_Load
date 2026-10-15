package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
)

func TestRunnerPilotRemainsExplicit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		op := config.JavaVuserOperatorConfig{Enabled: enabled, AuthToken: "token", MaxProcesses: 25}
		options, err := runnerServerOptions(context.Background(), op, "")
		if err != nil || options.HostAdmission != nil || options.PooledControlProfiles != nil || options.Operator.Enabled != op.Enabled || options.Operator.MaxProcesses != 25 {
			t.Fatal("default runner changed", err)
		}
	}
	for _, op := range []config.JavaVuserOperatorConfig{{}, {Enabled: true}, {Enabled: true, AuthToken: "token", MaxProcesses: 25}} {
		if _, err := runnerServerOptions(context.Background(), op, "/missing/operator-pilot.json"); err == nil {
			t.Fatal("invalid explicit pilot silently ignored")
		}
	}
}

func TestRunnerSharedJVMPolicyIsExplicitAndExclusive(t *testing.T) {
	dir := t.TempDir()
	jar := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("jar"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	op := config.JavaVuserOperatorConfig{Enabled: true, AuthToken: "token", MaxProcesses: 25,
		SharedJVM: config.JavaVuserSharedJVMConfig{Enabled: true, VUsersPerJVM: 250, HeapMB: 1024, DedicatedJVMMemoryMB: 256, SlotAPIJar: jar("api.jar"), SlotShimJar: jar("shim.jar")}}
	options, err := runnerServerOptions(context.Background(), op, "")
	if runtime.GOOS != "linux" {
		if err == nil || options.SharedJVM != nil {
			t.Fatal("shared JVMs enabled without delegated Linux containment")
		}
		return
	}
	if err != nil || options.SharedJVM == nil || options.HostAdmission == nil || options.PooledControlProfiles != nil {
		t.Fatalf("shared policy not wired: %v", err)
	}
	if _, err := runnerServerOptions(context.Background(), op, "/operator/pilot.json"); err == nil {
		t.Fatal("pilot manifest and shared policy both accepted")
	}
	op.SharedJVM.SlotShimJar = filepath.Join(dir, "absent.jar")
	if _, err := runnerServerOptions(context.Background(), op, ""); err == nil {
		t.Fatal("invalid shared policy silently ignored")
	}
}
