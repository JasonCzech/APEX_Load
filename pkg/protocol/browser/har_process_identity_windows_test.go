//go:build windows

package browser

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The host is this machine's MachineGuid paired with its physical DNS host
// name, the kernel's boot sequence number and the "windows" namespace.
func TestWindowsHARHostIdentity(t *testing.T) {
	host, err := readHARHost()
	if err != nil {
		t.Fatalf("readHARHost: %v", err)
	}
	if host.namespace != "windows" {
		t.Fatalf("namespace %q", host.namespace)
	}
	if _, err := strconv.ParseUint(host.boot, 10, 64); err != nil {
		t.Fatalf("boot %q: %v", host.boot, err)
	}
	guid, _, found := strings.Cut(host.machine, "@")
	if !found || guid == "" || strings.TrimPrefix(host.machine, guid+"@") == "" {
		t.Fatalf("machine %q is not MachineGuid@host", host.machine)
	}
	if containerized, err := harInWindowsContainer(); err != nil || containerized {
		t.Fatalf("the runner reads as a container: %v %v", containerized, err)
	}
}

// A live process has a creation time; one that exited is not found, whether
// or not its handle is still held elsewhere; an unused PID is not found.
func TestWindowsHARProcessStart(t *testing.T) {
	start, found, err := harProcessStart(os.Getpid())
	if err != nil || !found {
		t.Fatalf("this process: %q %v %v", start, found, err)
	}
	if _, err := strconv.ParseUint(start, 10, 64); err != nil {
		t.Fatalf("creation time %q: %v", start, err)
	}
	again, _, _ := harProcessStart(os.Getpid())
	if again != start {
		t.Fatalf("the creation time changed: %s -> %s", start, again)
	}
	child := exec.Command("cmd", "/c", "exit", "0")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	pid := child.Process.Pid
	// Until Wait the child's handle is held, so the exited process still
	// answers OpenProcess: WAIT_OBJECT_0 must count as gone.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, found, err := harProcessStart(pid); err == nil && !found {
			break
		}
	}
	if _, found, err := harProcessStart(pid); err != nil || found {
		t.Fatalf("an exited child with a held handle: found=%v err=%v", found, err)
	}
	_ = child.Wait()
	if _, found, err := harProcessStart(pid); err != nil || found {
		t.Fatalf("a reaped child: found=%v err=%v", found, err)
	}
	if _, found, err := harProcessStart(0x7ffffff0); err != nil || found {
		t.Fatalf("an unused PID: found=%v err=%v", found, err)
	}
	if _, _, err := harProcessStart(0); err == nil {
		t.Fatal("PID 0 was accepted")
	}
}
