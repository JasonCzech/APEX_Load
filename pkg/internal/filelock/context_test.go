package filelock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockContextBoundsWaitAndPreservesCallerDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	release, err := LockContext(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := LockContext(ctx, second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait: %v", err)
	}
	if _, err := second.Stat(); err != nil {
		t.Fatalf("wait closed caller file: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := LockContext(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	defer again()
	// Repeated release cannot unlock a later holder on the first descriptor.
	if err := release(); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel2()
	if _, err := LockContext(ctx2, first); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second owner not fenced: %v", err)
	}
}

func TestLockContextRejectsInvalidAndCancelledInputs(t *testing.T) {
	if _, err := LockContext(context.Background(), nil); err == nil {
		t.Fatal("nil accepted")
	}
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if _, err := LockContext(context.Background(), directory); err == nil {
		t.Fatal("directory accepted")
	}
	file, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LockContext(ctx, file); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	release, err := LockContext(nil, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := LockContext(context.Background(), file); err == nil {
		t.Fatal("closed file accepted")
	}
}

func TestLockContextAcrossProcesses(t *testing.T) {
	if path := os.Getenv("APEX_TEST_FILELOCK_PATH"); path != "" {
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		unlock, err := LockContext(ctx, file)
		if os.Getenv("APEX_TEST_FILELOCK_EXPECT_BLOCKED") == "1" {
			if !errors.Is(err, context.DeadlineExceeded) {
				if unlock != nil {
					_ = unlock()
				}
				t.Fatalf("child bypassed parent lock: %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for _, blocked := range []bool{true, false} {
		if !blocked {
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockContextAcrossProcesses$")
		flag := "0"
		if blocked {
			flag = "1"
		}
		cmd.Env = append(os.Environ(), "APEX_TEST_FILELOCK_PATH="+path, "APEX_TEST_FILELOCK_EXPECT_BLOCKED="+flag)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("child blocked=%v: %v %s", blocked, err, output)
		}
	}
}
