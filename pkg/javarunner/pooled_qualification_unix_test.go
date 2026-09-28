//go:build linux || darwin

package javarunner

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPooledQualificationRejectsFIFOWithoutOpening(t *testing.T) {
	for _, runtimeFile := range []bool{false, true} {
		request := qualificationFixture(t)
		dir := request.Launch.WorkDir
		if runtimeFile {
			dir = request.Launch.JDK.Home
		}
		path := filepath.Join(dir, "blocked")
		if err := unix.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		if !runtimeFile {
			request.SourceArchive = path
		}
		done := make(chan error, 1)
		go func() { _, err := InspectPooledQualification(context.Background(), request); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrPooledQualification) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("qualification opened a blocking special file")
		}
	}
}

func TestNativeInspectionRejectsFIFOWithoutOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.java")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := InspectApplicationNative(context.Background(), []string{path}); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("inspection opened blocking special file")
	}
}
