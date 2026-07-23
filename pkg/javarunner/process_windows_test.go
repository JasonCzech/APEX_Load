//go:build windows

package javarunner

import (
	"os/exec"
	"testing"
)

func TestStartProcessTreeOwnsWindowsProcessWithJobObject(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	ConfigureProcessTree(cmd)
	if err := StartStrongProcessTree(cmd); err != nil {
		t.Fatalf("StartStrongProcessTree() = %v", err)
	}
	if _, ok := managedProcessJobs.Load(cmd); !ok {
		t.Fatal("managed process was not assigned to a Job Object")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait() = %v", err)
	}
	if err := KillProcessTree(cmd); err != nil {
		t.Fatalf("KillProcessTree() after exit = %v", err)
	}
	if _, ok := managedProcessJobs.Load(cmd); ok {
		t.Fatal("Job Object handle remained registered after cleanup")
	}
}
