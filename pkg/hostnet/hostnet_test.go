package hostnet

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeProc(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectReadsBuffersAndDropCountersWithScope(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/sys/net/core/rmem_max", "212992\n")
	writeProc(t, root, "proc/sys/net/core/wmem_max", "7340032\n")
	writeProc(t, root, "proc/net/snmp", "Ip: Forwarding DefaultTTL\nIp: 1 64\nUdp: InDatagrams NoPorts InErrors OutDatagrams RcvbufErrors SndbufErrors InCsumErrors IgnoredMulti MemErrors\nUdp: 900 1 7 800 5 2 0 0 0\nUdpLite: InDatagrams NoPorts\nUdpLite: 0 0\n")
	writeProc(t, root, "proc/net/snmp6", "Ip6InReceives 10\nUdp6InDatagrams 40\nUdp6InErrors 3\nUdp6RcvbufErrors 1\nUdp6SndbufErrors 0\n")
	if err := os.MkdirAll(filepath.Join(root, "proc/self/ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("net:[4026532001]", filepath.Join(root, "proc/self/ns/net")); err != nil {
		t.Fatal(err)
	}

	shared := Collect(root, func(string) string { return "" })
	if !shared.UDPBuffers.Available || shared.UDPBuffers.ReceiveMaxBytes != 212992 || shared.UDPBuffers.SendMaxBytes != 7340032 || !shared.UDPBuffers.BelowRecommended() {
		t.Fatalf("buffers = %+v", shared.UDPBuffers)
	}
	drops := shared.UDPDrops
	if drops == nil || drops.ReceiveBufferErrors != 6 || drops.SendBufferErrors != 2 || drops.InErrors != 10 || drops.Scope != ScopeNetworkNamespace || drops.ScopeID != "net:[4026532001]" || drops.SampledAt.IsZero() {
		t.Fatalf("drops = %+v", drops)
	}
	pod := SampleUDPDrops(root, func(name string) string {
		if name == EnvUDPCounterScope {
			return "pod"
		}
		return ""
	})
	if pod.Scope != ScopePod {
		t.Fatalf("declared scope = %q", pod.Scope)
	}
	if runtime.GOOS != "windows" && !shared.OpenFiles.Available {
		t.Fatal("RLIMIT_NOFILE should be readable on Unix")
	}
}

func TestCollectReportsUnavailableValuesInsteadOfZeros(t *testing.T) {
	root := t.TempDir()
	facts := Collect(root, nil)
	if facts.UDPBuffers.Available || facts.UDPDrops != nil {
		t.Fatalf("missing /proc = %+v", facts)
	}
	writeProc(t, root, "proc/net/snmp", "Udp: InDatagrams RcvbufErrors\nUdp: 1\n")
	if SampleUDPDrops(root, nil) != nil {
		t.Fatal("a malformed counter line must not read as zero drops")
	}
	if (UDPBufferLimits{}).BelowRecommended() {
		t.Fatal("unavailable buffers are not below anything")
	}
}

func TestPlannedSocketsFollowTheOwnershipModel(t *testing.T) {
	if got := PlannedSockets(1000, false); got != 1256 {
		t.Fatalf("TCP only = %d", got)
	}
	if got := PlannedSockets(1000, true); got != 2256 {
		t.Fatalf("with HTTP/3 = %d", got)
	}
	if !OpenFileShortfall(OpenFileLimit{Available: true, Soft: 1024}, PlannedSockets(1000, true)) {
		t.Fatal("1024 descriptors cannot hold 1000 HTTP/3 VUsers")
	}
	if OpenFileShortfall(OpenFileLimit{}, 10) {
		t.Fatal("an unknown limit is not a shortfall")
	}
}

func TestRunEvidenceAttributesOnlyOwnScopeGrowth(t *testing.T) {
	sample := func(scope, id string, rcv, snd, in uint64) *UDPDropCounters {
		return &UDPDropCounters{Scope: scope, ScopeID: id, ReceiveBufferErrors: rcv, SendBufferErrors: snd, InErrors: in}
	}
	pod := RunEvidence{Source: "gen-0", DropsAtStart: sample(ScopePod, "net:[1]", 10, 0, 20), DropsAtEnd: sample(ScopePod, "net:[1]", 25, 1, 30)}
	delta, ok := pod.AttributableDropGrowth()
	if !ok || delta != (UDPDropDelta{ReceiveBufferErrors: 15, SendBufferErrors: 1, InErrors: 10}) || delta.Total() != 16 || delta.InputErrors() != 15 {
		t.Fatalf("pod delta = %+v %v", delta, ok)
	}
	if warning := pod.DropWarning(); !strings.Contains(warning, "gen-0") || !strings.Contains(warning, "input errors 15, receive buffer 15 of them; send buffer 1") {
		t.Fatalf("warning = %q", warning)
	}
	shared := RunEvidence{DropsAtStart: sample(ScopeNetworkNamespace, "net:[1]", 10, 0, 0), DropsAtEnd: sample(ScopeNetworkNamespace, "net:[1]", 99, 0, 0)}
	if _, ok := shared.DropDelta(); !ok {
		t.Fatal("a shared namespace's delta is still reported")
	}
	if _, ok := shared.AttributableDropGrowth(); ok || shared.DropWarning() != "" {
		t.Fatal("a shared namespace's growth must not be attributed to the run")
	}
	for name, evidence := range map[string]RunEvidence{
		"missing end":      {DropsAtStart: sample(ScopePod, "net:[1]", 1, 0, 0)},
		"other namespace":  {DropsAtStart: sample(ScopePod, "net:[1]", 1, 0, 0), DropsAtEnd: sample(ScopePod, "net:[2]", 5, 0, 0)},
		"counter reset":    {DropsAtStart: sample(ScopePod, "net:[1]", 9, 0, 0), DropsAtEnd: sample(ScopePod, "net:[1]", 2, 0, 0)},
		"no growth in pod": {DropsAtStart: sample(ScopePod, "net:[1]", 3, 0, 0), DropsAtEnd: sample(ScopePod, "net:[1]", 3, 0, 0)},
	} {
		if evidence.DropWarning() != "" {
			t.Fatalf("%s warned", name)
		}
	}
	var none *RunEvidence
	none.Finish("", nil)
}
