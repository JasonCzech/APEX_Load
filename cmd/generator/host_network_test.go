package main

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/hostnet"
)

func TestHostNetworkWarningsCheckTheOpenFileLimitAgainstPlannedSockets(t *testing.T) {
	low := hostnet.Facts{OpenFiles: hostnet.OpenFileLimit{Available: true, Soft: 4096, Hard: 4096}, UDPBuffers: hostnet.UDPBufferLimits{Available: true, ReceiveMaxBytes: 212992, SendMaxBytes: 212992}}
	warnings := strings.Join(hostNetworkWarnings(10000, true, low), "\n")
	for _, want := range []string{"open-file limit 4096 is below the 20256 descriptors planned for 10000 VUsers", "one HTTP/3 UDP socket per VUser", "net.core.rmem_max 212992"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("warnings missing %q:\n%s", want, warnings)
		}
	}
	// Without HTTP/3 the plan is TCP only and UDP buffers are not checked.
	tcp := hostNetworkWarnings(3000, false, low)
	if len(tcp) != 0 {
		t.Fatalf("TCP-only warnings = %v", tcp)
	}
	if len(hostNetworkWarnings(10000, true, hostnet.Facts{})) != 0 {
		t.Fatal("unavailable limits must not warn")
	}
}
