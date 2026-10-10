package main

import (
	"fmt"
	"log/slog"

	"github.com/jasonczech/apexload/pkg/hostnet"
)

// hostNetworkWarnings checks the limits that bound this generator's density
// at startup: the open-file limit against the sockets planned for maxVUsers
// (one TCP connection each at least, plus one UDP socket each when HTTP/3
// runs), and, with HTTP/3, the kernel's UDP buffer maxima.
func hostNetworkWarnings(maxVUsers int, http3 bool, facts hostnet.Facts) []string {
	var warnings []string
	planned := hostnet.PlannedSockets(maxVUsers, http3)
	if hostnet.OpenFileShortfall(facts.OpenFiles, planned) {
		model := "one TCP connection per VUser"
		if http3 {
			model = "one TCP connection and one HTTP/3 UDP socket per VUser"
		}
		warnings = append(warnings, fmt.Sprintf("open-file limit %d is below the %d descriptors planned for %d VUsers (%s, plus a reserve); raise RLIMIT_NOFILE (ulimit -n) or lower APEXLOAD_GENERATOR_MAX_VUSERS", facts.OpenFiles.Soft, planned, maxVUsers, model))
	}
	if http3 && facts.UDPBuffers.BelowRecommended() {
		warnings = append(warnings, fmt.Sprintf("UDP socket buffer maxima (net.core.rmem_max %d, wmem_max %d bytes) are below the %d bytes QUIC requests; HTTP/3 throughput may suffer until the node raises them", facts.UDPBuffers.ReceiveMaxBytes, facts.UDPBuffers.SendMaxBytes, hostnet.RecommendedUDPBufferBytes))
	}
	return warnings
}

func logHostNetworkLimits(maxVUsers int, http3 bool, facts hostnet.Facts) {
	for _, warning := range hostNetworkWarnings(maxVUsers, http3, facts) {
		slog.Warn("generator host limit", "detail", warning)
	}
	attrs := []any{"max_vusers", maxVUsers, "http3", http3, "planned_sockets", hostnet.PlannedSockets(maxVUsers, http3)}
	if facts.OpenFiles.Available {
		attrs = append(attrs, "open_files_soft", facts.OpenFiles.Soft, "open_files_hard", facts.OpenFiles.Hard)
	}
	if facts.UDPBuffers.Available {
		attrs = append(attrs, "udp_rmem_max", facts.UDPBuffers.ReceiveMaxBytes, "udp_wmem_max", facts.UDPBuffers.SendMaxBytes)
	}
	slog.Info("generator host network limits", attrs...)
}
