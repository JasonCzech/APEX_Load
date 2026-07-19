//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeRenameNoReplaceDoesNotClobberExistingTarget(t *testing.T) {
	dirPath := t.TempDir()
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)
	if err := os.WriteFile(filepath.Join(dirPath, "staging"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "result.json"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = nativeRenameNoReplace(dirFD, "staging", dirFD, "result.json")
	if !errors.Is(err, unix.EEXIST) {
		t.Fatalf("exclusive rename error = %v, want EEXIST", err)
	}
	got, err := os.ReadFile(filepath.Join(dirPath, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("exclusive rename clobbered destination: %q", got)
	}
	staging, err := os.ReadFile(filepath.Join(dirPath, "staging"))
	if err != nil || string(staging) != "new" {
		t.Fatalf("exclusive rename damaged staging file: content=%q err=%v", staging, err)
	}
}
