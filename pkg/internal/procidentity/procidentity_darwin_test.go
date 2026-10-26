//go:build darwin

package procidentity

import (
	"testing"

	"golang.org/x/sys/unix"
)

// macOS: the boot session UUID scopes a lookup and no machine identity is
// recorded (kern.uuid names the kernel build, shared by every Mac on it), so
// an earlier boot never proves an exit here.
func TestDarwinHostIdentity(t *testing.T) {
	host, err := readHost()
	if err != nil {
		t.Fatalf("readHost: %v", err)
	}
	if host.Namespace != "darwin" || host.Machine != "" || len(host.Boot) < 32 {
		t.Fatalf("darwin host identity %+v", host)
	}
	// The boot is this boot's session, not the kernel build's UUID, which
	// every Mac on one macOS build shares.
	if build, err := unix.Sysctl("kern.uuid"); err == nil && host.Boot == build {
		t.Fatalf("the boot identity is the kernel build UUID %q", build)
	}
	if session, err := unix.Sysctl("kern.bootsessionuuid"); err != nil || host.Boot != session {
		t.Fatalf("the boot identity %q is not the boot session %q (%v)", host.Boot, session, err)
	}
	start, found, err := processStart(1)
	if err != nil || !found || start == "" {
		t.Fatalf("launchd's start time: %q %v %v", start, found, err)
	}
}
