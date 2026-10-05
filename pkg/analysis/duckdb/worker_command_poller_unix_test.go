//go:build linux || darwin

package duckdb

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestInheritedCommandPipePollerCloseJoinsReadWithoutParentEOF(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stopWriter := sync.OnceFunc(func() { _ = write.Close() })
	defer stopWriter()
	// Reproduce StartProcess's inherited FD state without another live wrapper
	// owning the same descriptor: Fd switches to blocking, Dup inherits it.
	descriptor, err := syscall.Dup(int(read.Fd()))
	closeErr := read.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	input, err := workerPollableInheritedCommandFile(uintptr(descriptor))
	if err != nil {
		t.Fatal(err)
	}
	// SetReadDeadline itself is the runtime registration oracle, not a flag or
	// Close call claiming that a still blocked read has physically returned.
	if err = input.SetReadDeadline(time.Time{}); err != nil {
		input.Close()
		t.Fatal("inherited command pipe is not pollable", err)
	}
	raw, err := input.SyscallConn()
	if err != nil {
		input.Close()
		t.Fatal(err)
	}
	var flags int
	var flagErr error
	if err = raw.Control(func(fd uintptr) { flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil || flagErr != nil || flags&syscall.O_NONBLOCK == 0 {
		input.Close()
		t.Fatal("inherited pipe became blocking", err, flagErr)
	}
	done := make(chan error, 1)
	readJoined := make(chan struct{})
	closeDone := make(chan error, 1)
	closeJoined := make(chan struct{})
	startClose := sync.OnceFunc(func() { go func() { defer close(closeJoined); closeDone <- input.Close() }() })
	t.Cleanup(func() {
		stopWriter()
		startClose()
		for _, joined := range []<-chan struct{}{readJoined, closeJoined} {
			select {
			case <-joined:
			case <-time.After(time.Second):
				t.Error("exact poller fixture operation did not join during cleanup")
			}
		}
	})
	entered := make(chan struct{})
	go func() {
		defer close(readJoined)
		close(entered)
		var b [1]byte
		_, err := input.Read(b[:])
		done <- err
	}()
	<-entered
	select {
	case err := <-done:
		t.Fatal("idle inherited read ended before stop", err)
	case <-time.After(20 * time.Millisecond):
	}
	startClose()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatal("reader did not observe actual poller close", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close trapped command read while parent writer remained open")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal("physical command closure failed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close failed to join the command read")
	}
	// The write endpoint remains open for the entire oracle. No parent EOF or
	// child kill supplied its success; normal result/cancel IPC tests cover frames.
}
func TestInheritedCommandPollerRejectsForeignRegularFileWithoutChangingOrClosingIt(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-command-pipe")
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := syscall.Dup(int(file.Fd()))
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	defer func() {
		if err := syscall.Close(descriptor); err != nil {
			t.Error("exact fixture descriptor close", err)
		}
	}()
	flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := workerPollableInheritedCommandFile(uintptr(descriptor)); result != nil || !errors.Is(err, errWorkerProtocol) {
		t.Fatal("regular file admitted as command pipe", result, err)
	}
	after, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	if err != nil || after != flags {
		t.Fatal("foreign rejected descriptor was closed or flags changed", after, flags, err)
	}
}
