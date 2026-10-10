package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/hostnet"
)

// RunArchiveHostNetworkEvidence is one execution process's network limits
// and how much its namespace's UDP drop counters grew during the run. The
// warning is set only for growth in a scope the deployment declared as the
// process's own. The fields repeat hostnet.RunEvidence's rather than embed
// it, so the API contract lists them.
type RunArchiveHostNetworkEvidence struct {
	Source       string                   `json:"source,omitempty"`
	OpenFiles    hostnet.OpenFileLimit    `json:"open_files"`
	UDPBuffers   hostnet.UDPBufferLimits  `json:"udp_buffers"`
	DropsAtStart *hostnet.UDPDropCounters `json:"udp_drops_at_start,omitempty"`
	DropsAtEnd   *hostnet.UDPDropCounters `json:"udp_drops_at_end,omitempty"`
	Delta        *hostnet.UDPDropDelta    `json:"udp_drops_delta,omitempty"`
	Warning      string                   `json:"warning,omitempty"`
}

func buildRunArchiveHostNetwork(evidence []hostnet.RunEvidence) []RunArchiveHostNetworkEvidence {
	if len(evidence) == 0 {
		return nil
	}
	out := make([]RunArchiveHostNetworkEvidence, 0, len(evidence))
	for _, item := range evidence {
		entry := RunArchiveHostNetworkEvidence{Source: item.Source, OpenFiles: item.OpenFiles, UDPBuffers: item.UDPBuffers,
			DropsAtStart: item.DropsAtStart, DropsAtEnd: item.DropsAtEnd, Warning: item.DropWarning()}
		if delta, ok := item.DropDelta(); ok {
			entry.Delta = &delta
		}
		out = append(out, entry)
	}
	return out
}

// hostNetworkWarnings are the report's attributable UDP drop warnings.
func hostNetworkWarnings(evidence []RunArchiveHostNetworkEvidence) []string {
	var warnings []string
	for _, item := range evidence {
		if item.Warning != "" {
			warnings = append(warnings, item.Warning)
		}
	}
	return warnings
}

// reportHostNetworkRow is one process in the report's load generator UDP
// table.
type reportHostNetworkRow struct {
	Source, OpenFiles, Buffers, Drops, Note string
}

func reportHostNetworkRows(evidence []RunArchiveHostNetworkEvidence) []reportHostNetworkRow {
	rows := make([]reportHostNetworkRow, 0, len(evidence))
	for _, item := range evidence {
		row := reportHostNetworkRow{Source: item.Source, OpenFiles: "Unavailable", Buffers: "Unavailable", Drops: "Unavailable"}
		if row.Source == "" {
			row.Source = "Load generator"
		}
		if item.OpenFiles.Available {
			row.OpenFiles = fmt.Sprintf("%d", item.OpenFiles.Soft)
		}
		if item.UDPBuffers.Available {
			row.Buffers = fmt.Sprintf("%d / %d bytes", item.UDPBuffers.ReceiveMaxBytes, item.UDPBuffers.SendMaxBytes)
			if item.UDPBuffers.BelowRecommended() {
				row.Buffers += " (below the 7 MB QUIC requests)"
			}
		}
		if item.Delta != nil {
			row.Drops = fmt.Sprintf("input errors %d (receive buffer %d), send buffer %d", item.Delta.InputErrors(), item.Delta.ReceiveBufferErrors, item.Delta.SendBufferErrors)
			if item.DropsAtEnd != nil && item.DropsAtEnd.Scope != hostnet.ScopePod {
				row.Note = "Counters cover a shared network namespace, so they are not attributed to this run."
			}
		}
		if item.Warning != "" {
			row.Note = "Drops grew in the generator's own pod during the run."
		}
		rows = append(rows, row)
	}
	return rows
}

func reportHostNetworkLines(evidence []RunArchiveHostNetworkEvidence) []string {
	if len(evidence) == 0 {
		return nil
	}
	lines := []string{"Load generator UDP (open-file limit; buffer maxima receive / send; drops during the run):"}
	for _, row := range reportHostNetworkRows(evidence) {
		line := fmt.Sprintf("- %s: open files %s; buffers %s; drops %s", row.Source, row.OpenFiles, row.Buffers, row.Drops)
		if row.Note != "" {
			line += ". " + row.Note
		}
		lines = append(lines, line)
	}
	return lines
}

func writeHostNetworkCSVRows(write reportCSVWriteFunc, evidence []RunArchiveHostNetworkEvidence) error {
	for _, row := range reportHostNetworkRows(evidence) {
		key := "host_network:" + row.Source
		for _, field := range [][2]string{{"open_files", row.OpenFiles}, {"udp_buffer_max", row.Buffers}, {"udp_drops_during_run", row.Drops}, {"note", row.Note}} {
			if field[1] == "" {
				continue
			}
			if err := write("network_phases", key, field[0], field[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// hostNetworkFinding is the derived report finding for attributable drops.
func hostNetworkFinding(evidence []RunArchiveHostNetworkEvidence) *ReportFinding {
	warnings := hostNetworkWarnings(evidence)
	if len(warnings) == 0 {
		return nil
	}
	observed := strings.Join(warnings[:min(len(warnings), 2)], " ")
	if len(warnings) > 2 {
		observed += fmt.Sprintf(" %d more load generators reported the same.", len(warnings)-2)
	}
	return &ReportFinding{Title: "UDP drops on load generators", Severity: "minor", Observed: observed,
		Evidence: "UDP error counters of each generator's pod network namespace at execution start and end."}
}
