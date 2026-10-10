// Package hostnet reads the host network limits HTTP/3 depends on: the
// process's open-file limit, the kernel's UDP socket buffer maxima, and the
// UDP drop counters of the process's network namespace. QUIC runs over one
// UDP socket per virtual user and in the generator process, so these limits
// bound its density and explain dropped packets.
//
// Every value says whether it was available; nothing is estimated. Drop
// counters carry their scope, because a network namespace may be shared with
// other processes: only a scope the deployment declares as the generator's
// own (a pod) may be attributed to a run.
package hostnet

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RecommendedUDPBufferBytes is the socket buffer size quic-go requests (7
// MB); it warns, and throughput may suffer, when the kernel caps it lower.
const RecommendedUDPBufferBytes = 7 << 20

// EnvUDPCounterScope declares the scope of the network namespace's UDP
// counters. "pod" says the namespace belongs to this workload alone, which
// lets a run report attribute drops to the run; anything else is reported
// as a shared network namespace.
const EnvUDPCounterScope = "APEXLOAD_UDP_COUNTER_SCOPE"

// Counter scopes.
const (
	ScopePod              = "pod"
	ScopeNetworkNamespace = "network_namespace"
)

// Facts are one process's network limits.
type Facts struct {
	OpenFiles  OpenFileLimit    `json:"open_files"`
	UDPBuffers UDPBufferLimits  `json:"udp_buffers"`
	UDPDrops   *UDPDropCounters `json:"udp_drops,omitempty"` // nil when unavailable
}

// OpenFileLimit is RLIMIT_NOFILE. Go raises the soft limit to the hard limit
// at startup on Unix, so Soft is the effective limit.
type OpenFileLimit struct {
	Available bool   `json:"available"`
	Soft      uint64 `json:"soft,omitempty"`
	Hard      uint64 `json:"hard,omitempty"`
}

// UDPBufferLimits are net.core.rmem_max and wmem_max, the largest socket
// buffers an unprivileged process can request.
type UDPBufferLimits struct {
	Available       bool  `json:"available"`
	ReceiveMaxBytes int64 `json:"receive_max_bytes,omitempty"`
	SendMaxBytes    int64 `json:"send_max_bytes,omitempty"`
}

// BelowRecommended reports whether the kernel caps either buffer below what
// QUIC requests.
func (b UDPBufferLimits) BelowRecommended() bool {
	return b.Available && (b.ReceiveMaxBytes < RecommendedUDPBufferBytes || b.SendMaxBytes < RecommendedUDPBufferBytes)
}

// UDPDropCounters are the namespace's cumulative UDP error counters, IPv4
// and IPv6 summed.
type UDPDropCounters struct {
	Scope string `json:"scope"`
	// ScopeID identifies the network namespace, so two samples are compared
	// only when they describe the same one.
	ScopeID             string    `json:"scope_id,omitempty"`
	ReceiveBufferErrors uint64    `json:"receive_buffer_errors"`
	SendBufferErrors    uint64    `json:"send_buffer_errors"`
	InErrors            uint64    `json:"in_errors"`
	SampledAt           time.Time `json:"sampled_at"`
}

// Collect reads the facts from root ("" for the real filesystem) and the
// process's resource limits.
func Collect(root string, getenv func(string) string) Facts {
	if getenv == nil {
		getenv = os.Getenv
	}
	facts := Facts{OpenFiles: openFileLimit()}
	if rmem, ok := readInt(filepath.Join(root, "/proc/sys/net/core/rmem_max")); ok {
		if wmem, ok := readInt(filepath.Join(root, "/proc/sys/net/core/wmem_max")); ok {
			facts.UDPBuffers = UDPBufferLimits{Available: true, ReceiveMaxBytes: rmem, SendMaxBytes: wmem}
		}
	}
	facts.UDPDrops = SampleUDPDrops(root, getenv)
	return facts
}

// SampleUDPDrops reads the namespace's UDP counters, or nil where the
// platform does not expose them.
func SampleUDPDrops(root string, getenv func(string) string) *UDPDropCounters {
	if getenv == nil {
		getenv = os.Getenv
	}
	v4, ok := readSNMPUDP(filepath.Join(root, "/proc/net/snmp"))
	if !ok {
		return nil
	}
	counters := &UDPDropCounters{Scope: ScopeNetworkNamespace, ReceiveBufferErrors: v4["RcvbufErrors"], SendBufferErrors: v4["SndbufErrors"], InErrors: v4["InErrors"], SampledAt: time.Now().UTC()}
	if v6, ok := readSNMP6UDP(filepath.Join(root, "/proc/net/snmp6")); ok {
		counters.ReceiveBufferErrors += v6["Udp6RcvbufErrors"]
		counters.SendBufferErrors += v6["Udp6SndbufErrors"]
		counters.InErrors += v6["Udp6InErrors"]
	}
	if link, err := os.Readlink(filepath.Join(root, "/proc/self/ns/net")); err == nil {
		counters.ScopeID = strings.TrimSpace(link)
	}
	if strings.EqualFold(strings.TrimSpace(getenv(EnvUDPCounterScope)), ScopePod) {
		counters.Scope = ScopePod
	}
	return counters
}

// SocketsPerVUserTCP is the floor of TCP connections the plan assumes per
// virtual user; HTTP/3 adds one UDP socket per virtual user (decision D3).
const SocketsPerVUserTCP = 1

// socketReserve covers the process's own files, listeners and control
// connections.
const socketReserve = 256

// PlannedSockets is the descriptor count a generator needs for maxVUsers
// under the socket-ownership model: one TCP connection each at least, plus
// one UDP socket each when HTTP/3 is enabled, plus a fixed reserve.
func PlannedSockets(maxVUsers int, http3 bool) uint64 {
	if maxVUsers < 0 {
		maxVUsers = 0
	}
	perVUser := uint64(SocketsPerVUserTCP)
	if http3 {
		perVUser++
	}
	return socketReserve + uint64(maxVUsers)*perVUser
}

// OpenFileShortfall reports whether the limit is known and below planned.
func OpenFileShortfall(limit OpenFileLimit, planned uint64) bool {
	return limit.Available && limit.Soft < planned
}

func readInt(path string) (int64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return value, err == nil && value >= 0
}

// readSNMPUDP parses the "Udp:" header and value lines of /proc/net/snmp.
func readSNMPUDP(path string) (map[string]uint64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	var header []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "Udp:" {
			continue
		}
		if header == nil {
			header = fields[1:]
			continue
		}
		values := fields[1:]
		if len(values) != len(header) {
			return nil, false
		}
		out := make(map[string]uint64, len(header))
		for index, name := range header {
			value, err := strconv.ParseUint(values[index], 10, 64)
			if err != nil {
				return nil, false
			}
			out[name] = value
		}
		return out, true
	}
	return nil, false
}

// readSNMP6UDP parses the "Udp6*" name/value lines of /proc/net/snmp6.
func readSNMP6UDP(path string) (map[string]uint64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	out := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !strings.HasPrefix(fields[0], "Udp6") {
			continue
		}
		if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
			out[fields[0]] = value
		}
	}
	return out, len(out) > 0
}
