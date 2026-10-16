//go:build linux

package javarunner

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// A failed identity capture owns no descriptor: closing it must not close
// whatever holds fd 0, such as another identity's pidfd.
func TestFailedLinuxIdentityCaptureOwnsNoDescriptor(t *testing.T) {
	const missingPID = 1 << 30 // above the kernel's PID limit
	if identity, err := captureLinuxProcessIdentity(missingPID); err == nil || identity.pidfd != -1 {
		t.Fatalf("capture of a missing process = %+v, %v; want an error and no descriptor", identity, err)
	}
	if identity, err := readLinuxProcessIdentity(missingPID); err == nil || identity.pidfd != -1 {
		t.Fatalf("read of a missing process = %+v, %v; want an error and no descriptor", identity, err)
	}

	// Put a known file at fd 0, then close a failed capture the way the
	// descendant scans do.
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	saved, err := unix.Dup(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = unix.Dup2(saved, 0)
		_ = unix.Close(saved)
	}()
	if err := unix.Dup2(int(file.Fd()), 0); err != nil {
		t.Fatal(err)
	}
	identity, _ := captureLinuxProcessIdentity(missingPID)
	closeLinuxProcessIdentity(identity)
	if _, err := unix.FcntlInt(0, unix.F_GETFD, 0); err != nil {
		t.Fatalf("closing a failed capture closed fd 0: %v", err)
	}

	tree := &managedLinuxProcessTree{known: map[int]linuxProcessIdentity{}, root: noLinuxProcessIdentity()}
	tree.closeKnownIdentities()
	if tree.root.pidfd != -1 {
		t.Fatalf("reset root claims descriptor %d", tree.root.pidfd)
	}
}
