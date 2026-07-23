//go:build aix || dragonfly || freebsd || netbsd || openbsd || solaris

package javarunner

import (
	"errors"
	"os/exec"
	"testing"
)

func TestStrongProcessTreeFailsClosedOnUnsupportedUnix(t *testing.T) {
	cmd := exec.Command("java", "-version")
	err := StartStrongProcessTree(cmd)
	if !errors.Is(err, ErrStrongProcessContainmentUnavailable) {
		t.Fatalf("StartStrongProcessTree() = %v, want containment unavailable", err)
	}
	if cmd.Process != nil {
		t.Fatal("unsupported Unix strong start launched the target")
	}
	if SupportsStrongProcessContainment() {
		t.Fatal("unsupported Unix advertised strong containment")
	}
}
