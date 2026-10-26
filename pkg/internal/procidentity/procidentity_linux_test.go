//go:build linux

package procidentity

import (
	"errors"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// fakeKernel replaces the Linux kernel interfaces for one test. A link target
// of "!EACCES" answers that path with a permission error.
func fakeKernel(t *testing.T, files map[string]string, links map[string]string, kill error) {
	t.Helper()
	oldRead, oldLink, oldKill, oldHost := readFile, readLink, killZero, hostName
	t.Cleanup(func() { readFile, readLink, killZero, hostName = oldRead, oldLink, oldKill, oldHost })
	hostName = func() (string, error) { return "host-a", nil }
	readFile = func(path string) ([]byte, error) {
		if body, ok := files[path]; ok {
			return []byte(body), nil
		}
		return nil, os.ErrNotExist
	}
	readLink = func(path string) (string, error) {
		if target, ok := links[path]; ok {
			if target == "!EACCES" {
				return "", unix.EACCES
			}
			return target, nil
		}
		return "", os.ErrNotExist
	}
	killZero = func(int) error { return kill }
}

// Under hidepid or ProtectProc a live process has no /proc entry for other
// users: only the kernel's ESRCH proves an absent entry means no process.
func TestProcessStartNeedsESRCHForAMissingEntry(t *testing.T) {
	for name, c := range map[string]struct {
		kill    error
		found   bool
		decided bool
	}{
		"hidden live process (EPERM)": {unix.EPERM, false, false},
		"visible to kill only (nil)":  {nil, false, false},
		"no such process (ESRCH)":     {unix.ESRCH, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			fakeKernel(t, nil, nil, c.kill)
			start, found, err := processStart(42)
			if found != c.found || (err == nil) != c.decided || start != "" {
				t.Fatalf("processStart: %q %v %v", start, found, err)
			}
		})
	}
	fakeKernel(t, map[string]string{"/proc/42/stat": "42 (a b) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 777 19"}, nil, unix.ESRCH)
	if start, found, err := processStart(42); err != nil || !found || start != "777" {
		t.Fatalf("a visible entry: %q %v %v", start, found, err)
	}
	// A short or malformed stat line is an error, never an answer.
	for name, stat := range map[string]string{
		"too few fields":     "42 (a b) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18",
		"no command bracket": "42 a S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 777 19",
		"non-numeric start":  "42 (a) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 x 19",
	} {
		fakeKernel(t, map[string]string{"/proc/42/stat": stat}, nil, unix.ESRCH)
		if start, found, err := processStart(42); err == nil || found || start != "" {
			t.Errorf("%s: %q %v %v", name, start, found, err)
		}
	}
}

func TestReadHostRefusesAForeignProcAndScopesTimeNamespaces(t *testing.T) {
	files := map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1\n", "/etc/machine-id": "machine-1\n"}
	self := strconv.Itoa(os.Getpid())
	fakeKernel(t, files, map[string]string{"/proc/self/ns/pid": "pid:[1]", "/proc/self": "999999999"}, unix.ESRCH)
	if _, err := readHost(); err == nil {
		t.Fatal("a /proc of another PID namespace was accepted")
	}
	fakeKernel(t, files, map[string]string{"/proc/self/ns/pid": "pid:[1]", "/proc/self": self}, unix.ESRCH)
	host, err := readHost()
	if err != nil || host.Namespace != "pid:[1]" || host.Boot != "boot-1" {
		t.Fatalf("host without a time namespace: %+v %v", host, err)
	}
	fakeKernel(t, files, map[string]string{"/proc/self/ns/pid": "pid:[1]", "/proc/self/ns/time": "time:[7]", "/proc/self": self}, unix.ESRCH)
	host, err = readHost()
	if err != nil || host.Namespace != "pid:[1] time:[7]" {
		t.Fatalf("host with a time namespace: %+v %v", host, err)
	}
	// A time namespace that cannot be read is not the same as none.
	fakeKernel(t, files, map[string]string{"/proc/self/ns/pid": "pid:[1]", "/proc/self/ns/time": "!EACCES", "/proc/self": self}, unix.ESRCH)
	if host, err = readHost(); err == nil {
		t.Fatalf("an unreadable time namespace was ignored: %+v", host)
	}
	fakeKernel(t, map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1"}, map[string]string{"/proc/self/ns/pid": "pid:[1]", "/proc/self": self}, unix.ESRCH)
	if host, err = readHost(); err != nil || host.Machine != "" {
		t.Fatalf("host without a machine-id: %+v %v", host, err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatal("unexpected unavailable host")
	}
}

// With the real kernel, run where /proc hides another user's PID 1.
func TestProcessStartRefusesHiddenProcesses(t *testing.T) {
	if _, err := os.Stat("/proc/1/stat"); os.Getuid() == 0 || !errors.Is(err, os.ErrNotExist) {
		t.Skip("needs /proc mounted with hidepid and a non-root test process")
	}
	start, found, err := processStart(1)
	if err == nil || found || start != "" {
		t.Fatalf("a hidden live process was judged: %q %v %v", start, found, err)
	}
	host, err := CurrentHost()
	if err != nil {
		t.Fatal(err)
	}
	if Exited(Identity{Machine: host.Machine, Boot: host.Boot, Namespace: host.Namespace, PID: 1, Start: "1"}) {
		t.Fatal("a hidden live process was judged exited")
	}
}

// A machine-id alone is shared by every host built from one image, so the
// machine identity pairs it with the host name, and an earlier boot is proved
// only on that exact pair. Without either half there is no machine identity.
func TestReadHostPairsMachineIDWithHostName(t *testing.T) {
	self := strconv.Itoa(os.Getpid())
	links := map[string]string{"/proc/self/ns/pid": "pid:[1]", "/proc/self": self}
	for name, c := range map[string]struct {
		files    map[string]string
		hostname string
		machine  string
	}{
		"machine-id and host name": {map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1", "/etc/machine-id": "machine-1\n"}, " host-a\n", "machine-1@host-a"},
		"no host name":             {map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1", "/etc/machine-id": "machine-1"}, "  ", ""},
		"dbus fallback":            {map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1", "/var/lib/dbus/machine-id": "dbus-1"}, "host-a", "dbus-1@host-a"},
		"empty machine-id, dbus":   {map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1", "/etc/machine-id": " \n", "/var/lib/dbus/machine-id": "dbus-1"}, "host-a", "dbus-1@host-a"},
		"etc wins over dbus":       {map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1", "/etc/machine-id": "machine-1", "/var/lib/dbus/machine-id": "dbus-1"}, "host-a", "machine-1@host-a"},
		"no machine-id":            {map[string]string{"/proc/sys/kernel/random/boot_id": "boot-1"}, "host-a", ""},
	} {
		t.Run(name, func(t *testing.T) {
			fakeKernel(t, c.files, links, unix.ESRCH)
			hostname := c.hostname
			hostName = func() (string, error) { return hostname, nil }
			host, err := readHost()
			if err != nil || host.Machine != c.machine || host.Boot != "boot-1" {
				t.Fatalf("host %+v %v, want machine %q", host, err, c.machine)
			}
		})
	}
	fakeKernel(t, map[string]string{"/proc/sys/kernel/random/boot_id": " \n", "/etc/machine-id": "machine-1"}, links, unix.ESRCH)
	if host, err := readHost(); err == nil {
		t.Fatalf("an empty boot_id was accepted: %+v", host)
	}
}
