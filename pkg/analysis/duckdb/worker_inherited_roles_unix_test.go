//go:build linux || darwin

package duckdb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type workerRawDescriptorSnapshot struct {
	info              unix.Stat_t
	flags             int
	statErr, flagsErr error
}

func workerRawSnapshot(descriptor int) workerRawDescriptorSnapshot {
	var result workerRawDescriptorSnapshot
	result.statErr = unix.Fstat(descriptor, &result.info)
	result.flags, result.flagsErr = unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	return result
}

func workerRawSnapshotMatches(before, after workerRawDescriptorSnapshot) bool {
	return errors.Is(before.statErr, after.statErr) && errors.Is(after.statErr, before.statErr) && errors.Is(before.flagsErr, after.flagsErr) && errors.Is(after.flagsErr, before.flagsErr) && before.flags == after.flags && (before.statErr != nil || before.info.Dev == after.info.Dev && before.info.Ino == after.info.Ino && before.info.Mode == after.info.Mode)
}

func TestWorkerRawInheritedAdmissionHelper(t *testing.T) {
	mode := os.Getenv("APEXLOAD_TEST_RAW_WORKER_ROLES")
	if mode == "" {
		return
	}
	if mode == "missing" {
		// Initialize the real runtime poller, then release only this exact pair.
		// Its kqueue/epoll and wake descriptors remain runtime-owned.
		workerRawRolePollerProbe(t)
	}
	var before [3]workerRawDescriptorSnapshot
	for i := range before {
		before[i] = workerRawSnapshot(3 + i)
	}
	boot, root, commands, err := workerInheritedChannels()
	if !errors.Is(err, errWorkerProtocol) || boot != nil || root != nil || commands != nil {
		t.Fatal("wrong raw roles were adopted", err)
	}
	for i, snapshot := range before {
		if !workerRawSnapshotMatches(snapshot, workerRawSnapshot(3+i)) {
			t.Fatalf("foreign raw descriptor %d was changed/closed", 3+i)
		}
	}
	workerRawRolePollerProbe(t)
	if _, err := fmt.Fprintln(os.Stdout, "RAW_ROLE_REFUSAL_OK"); err != nil {
		t.Fatal(err)
	}
}

func workerRawRolePollerProbe(t *testing.T) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := read.Close(); err != nil {
			t.Error(err)
		}
		if err := write.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := read.SetReadDeadline(time.Now().Add(2 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if _, err := read.Read(data[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("real runtime poller failed after raw role refusal", err)
	}
}

func TestWorkerRawInheritedRefusalPreservesForeignDescriptorsAndRuntimePoller(t *testing.T) {
	for _, mode := range []string{"missing", "bootstrap-write", "root-regular", "root-public", "commands-write", "repeated-pipe"} {
		t.Run(mode, func(t *testing.T) {
			// Register root cleanup before the physical owner cleanup so LIFO
			// cannot remove the namespace before the exact child/FD joins.
			rootPath := t.TempDir()
			var owned []*os.File
			track := func(file *os.File, err error) *os.File {
				if file != nil {
					owned = append(owned, file)
				}
				if err != nil {
					t.Fatal(err)
				}
				return file
			}
			var cmd *exec.Cmd
			var waitDone chan struct{}
			var waitErr error
			t.Cleanup(func() {
				if waitDone != nil {
					select {
					case <-waitDone:
					default:
						if cmd.Process != nil {
							if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
								t.Error(err)
							}
						}
						select {
						case <-waitDone:
						case <-time.After(5 * time.Second):
							t.Error("exact child Wait did not join; retaining its files/root until actual join")
							// Logical teardown expiry does not release the physical bundle.
							// Keep this same waiter/stack before any file or TempDir cleanup.
							<-waitDone
						}
					}
				}
				for i := len(owned) - 1; i >= 0; i-- {
					if err := owned[i].Close(); err != nil {
						t.Error("exact test descriptor Close", err)
					}
				}
			})
			if err := os.Chmod(rootPath, 0700); err != nil {
				t.Fatal(err)
			}
			stdoutPath := rootPath + "/child.log"
			stdout := track(os.OpenFile(stdoutPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600))
			stdin := track(os.Open(os.DevNull))
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd = exec.Command(executable, "-test.run=^TestWorkerRawInheritedAdmissionHelper$", "-test.timeout=10s")
			cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "APEXLOAD_TEST_RAW_WORKER_ROLES=" + mode}
			cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stdout
			if mode != "missing" {
				bootRead, bootWrite, err := os.Pipe()
				track(bootRead, err)
				track(bootWrite, nil)
				commandRead, commandWrite, err := os.Pipe()
				track(commandRead, err)
				track(commandWrite, nil)
				root := track(os.Open(rootPath))
				cmd.ExtraFiles = []*os.File{bootRead, root, commandRead}
				switch mode {
				case "bootstrap-write":
					cmd.ExtraFiles[0] = bootWrite
				case "root-regular":
					cmd.ExtraFiles[1] = stdin
				case "root-public":
					if err := os.Chmod(rootPath, 0750); err != nil {
						t.Fatal(err)
					}
				case "commands-write":
					cmd.ExtraFiles[2] = commandWrite
				case "repeated-pipe":
					cmd.ExtraFiles[2] = bootRead
				}
			}
			// Admit the sole waiter before Start. A non-returned launch remains
			// owned behind this exact gate; cleanup cannot infer no child from it.
			waitDone = make(chan struct{})
			launchDone := make(chan struct{})
			var launchErr error
			publishLaunch := sync.OnceFunc(func() { close(launchDone) })
			go func() {
				defer close(waitDone)
				<-launchDone
				if launchErr == nil {
					waitErr = cmd.Wait()
				} else {
					waitErr = launchErr
				}
			}()
			launchErr = cmd.Start()
			publishLaunch()
			if launchErr != nil {
				t.Fatal(launchErr)
			}
			select {
			case <-waitDone:
			case <-time.After(5 * time.Second):
				t.Fatal("raw role child did not return")
			}
			if waitErr != nil {
				t.Fatal("raw role child failed", waitErr)
			}
			result, err := os.ReadFile(stdoutPath)
			if err != nil || !strings.Contains(string(result), "RAW_ROLE_REFUSAL_OK") || strings.Contains(string(result), "fatal error:") || strings.Contains(string(result), "runtime: netpoll failed") {
				t.Fatal("invalid-role refusal harmed runtime poller", string(result), err)
			}
		})
	}
}

func TestWorkerRawRoleProbeLeavesWritePipeAndFlagsUnchanged(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	closePair := sync.OnceFunc(func() {
		if err := read.Close(); err != nil {
			t.Error(err)
		}
		if err := write.Close(); err != nil {
			t.Error(err)
		}
	})
	defer closePair()
	fd := write.Fd()
	before := workerRawSnapshot(int(fd))
	if _, err := workerInspectRawInheritedDescriptor(fd, workerRawReadPipe, false); !errors.Is(err, errWorkerProtocol) {
		t.Fatal("write end became read authority", err)
	}
	if file, err := workerPollableInheritedCommandFile(fd); file != nil || !errors.Is(err, errWorkerProtocol) {
		t.Fatal("write end became command reader", err)
	}
	if !workerRawSnapshotMatches(before, workerRawSnapshot(int(fd))) {
		t.Fatal("foreign write pipe was changed or closed")
	}
}
