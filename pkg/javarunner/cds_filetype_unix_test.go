//go:build linux || darwin

package javarunner

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCDSRejectsFIFOWithoutOpening(t *testing.T) {
	for _, manifest := range []bool{true, false} {
		s, _, _ := cdsFixture(t)
		fifo := filepath.Join(t.TempDir(), "blocked.jar")
		if err := unix.Mkfifo(fifo, 0600); err != nil {
			t.Fatal(err)
		}
		if manifest {
			s.OperatorCDSManifest = fifo
		} else {
			s.ScriptClasspath = []string{fifo}
		}
		done := make(chan error, 1)
		go func() { _, err := BuildCommand(s); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("accepted FIFO")
			}
		case <-time.After(time.Second):
			t.Fatal("CDS validation blocked on FIFO")
		}
	}
}
