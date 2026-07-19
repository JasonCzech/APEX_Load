//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadGrantRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected.har")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := newReadPathGrant(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO grant error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read grant blocked while opening a FIFO")
	}
}

func TestReadAndRevealRejectFIFOReplacementWithoutBlocking(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	path := filepath.Join(dir, "selected.har")
	if err := os.WriteFile(path, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.rememberReadPath(path)
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := app.ReadFileBase64(path)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("FIFO replacement was readable")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native read blocked on a FIFO replacement")
	}

	revealDone := make(chan bool, 1)
	go func() {
		_, ok := app.authorizedRevealPath(path)
		revealDone <- ok
	}()
	select {
	case ok := <-revealDone:
		if ok {
			t.Fatal("FIFO replacement was reveal-authorized")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native reveal authorization blocked on a FIFO replacement")
	}
}
