//go:build windows

package duckdb

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// This is a required executable Windows platform oracle, not a source-derived
// qualification. It models production's exact inherited duplicate + NewFile
// kindPipe and synchronous read; Close must join CancelIoEx/Read/CloseHandle
// while the parent's writer still exists. No EOF or child kill can supply proof.
func TestWorkerWindowsInheritedCommandCloseJoinsSynchronousReadWithoutParentEOF(t *testing.T) {
	original, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stopWriter := sync.OnceFunc(func() { _ = writer.Close() })
	defer stopWriter()
	var inherited windows.Handle
	err = windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(original.Fd()), windows.CurrentProcess(), &inherited, 0, true, windows.DUPLICATE_SAME_ACCESS)
	originalClose := original.Close()
	if err != nil || originalClose != nil {
		if inherited != 0 {
			_ = windows.CloseHandle(inherited)
		}
		t.Fatal("exact inherited handle setup failed", err, originalClose)
	}
	input := os.NewFile(uintptr(inherited), "native-inherited-commands")
	if input == nil {
		_ = windows.CloseHandle(inherited)
		t.Fatal("inherited handle wrapper refused")
	}
	kind, err := windows.GetFileType(inherited)
	if err != nil || kind != windows.FILE_TYPE_PIPE {
		_ = input.Close()
		t.Fatal("inherited command is not a pipe", kind, err)
	}
	entered, readJoined, closeJoined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	readOutcome, closeOutcome := make(chan error, 1), make(chan error, 1)
	startClose := sync.OnceFunc(func() { go func() { defer close(closeJoined); closeOutcome <- input.Close() }() })
	t.Cleanup(func() {
		stopWriter()
		startClose()
		for _, joined := range []<-chan struct{}{readJoined, closeJoined} {
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Error("exact Windows command fixture owner did not join")
			}
		}
	})
	go func() {
		defer close(readJoined)
		close(entered)
		var b [1]byte
		_, err := input.Read(b[:])
		readOutcome <- err
	}()
	<-entered
	select {
	case err := <-readOutcome:
		t.Fatal("idle inherited read ended before Close", err)
	case <-time.After(20 * time.Millisecond):
	}
	startClose()
	select {
	case err := <-readOutcome:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatal("actual synchronous command read did not observe Close", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelIoEx did not join inherited synchronous command read")
	}
	select {
	case err := <-closeOutcome:
		if err != nil {
			t.Fatal("physical Windows command Close failed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("command Close did not join exact handle destruction")
	}
	// Do not touch/reclose the inherited numeric handle: it may now be reused.
}
