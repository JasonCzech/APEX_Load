// Package ipc defines the Java harness wire data shared by local and sidecar
// execution. It does not own processes, sessions, admission, or error reporting.
package ipc

import (
	"encoding/json"
	"strings"
)

const (
	ContractV2      = "apexload.java_vuser.ipc.v2"
	EventContractV2 = "apexload.java_vuser.lrapi_events.v2"
)

type Summary struct {
	Contract          string `json:"contract,omitempty"`
	Kind              string `json:"kind,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	Success           *bool  `json:"success,omitempty"`
	Error             string `json:"error,omitempty"`
	ClassName         string `json:"class_name,omitempty"`
	Method            string `json:"method,omitempty"`
	DurationMS        int64  `json:"duration_ms,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used,omitempty"`
	EventsTruncated   bool   `json:"events_truncated,omitempty"`
}

type PersistentEnvelope struct {
	Contract          string `json:"contract"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id"`
	FrameToken        string `json:"frame_token,omitempty"`
	Success           bool   `json:"success"`
	Error             string `json:"error,omitempty"`
	ClassName         string `json:"class_name,omitempty"`
	Method            string `json:"method,omitempty"`
	DurationMS        int64  `json:"duration_ms,omitempty"`
	Stdout            string `json:"stdout,omitempty"`
	Stderr            string `json:"stderr,omitempty"`
	StdoutTruncated   bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated   bool   `json:"stderr_truncated,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used,omitempty"`
	EventsTruncated   bool   `json:"events_truncated,omitempty"`
	Fatal             bool   `json:"fatal,omitempty"`
}

type HealthEnvelope struct {
	Contract      string   `json:"contract"`
	Kind          string   `json:"kind"`
	EventContract string   `json:"event_contract"`
	Features      []string `json:"features"`
}

// SidecarSummary deliberately excludes local execution metadata. Sidecar
// admission has historically ignored class_name, method, and duration_ms even
// when a custom harness emits values the local execution adapter cannot decode.
// Keep that parsing policy explicit instead of widening the accepted DTO.
type SidecarSummary struct {
	Contract          string `json:"contract"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id"`
	Success           *bool  `json:"success"`
	Error             string `json:"error,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used"`
	EventsTruncated   bool   `json:"events_truncated"`
}

// SidecarPersistentEnvelope is the sidecar's view of a persistent frame. It
// omits local metadata for the same compatibility reason as SidecarSummary.
type SidecarPersistentEnvelope struct {
	Contract          string `json:"contract"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id"`
	FrameToken        string `json:"frame_token,omitempty"`
	Success           bool   `json:"success"`
	Error             string `json:"error,omitempty"`
	Stdout            string `json:"stdout,omitempty"`
	Stderr            string `json:"stderr,omitempty"`
	StdoutTruncated   bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated   bool   `json:"stderr_truncated,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used,omitempty"`
	EventsTruncated   bool   `json:"events_truncated,omitempty"`
	Fatal             bool   `json:"fatal,omitempty"`
}

// ParseBoundSummary selects the last complete v2 result for this invocation.
// Noise, incomplete JSON, other requests, and frames without an explicit success
// value cannot replace the authoritative result. Legacy parsing is host policy.
func ParseBoundSummary(stdout, requestID string) (Summary, bool) {
	return parseBoundResult[Summary](stdout, requestID)
}

func ParseBoundSidecarSummary(stdout, requestID string) (SidecarSummary, bool) {
	return parseBoundResult[SidecarSummary](stdout, requestID)
}

func parseBoundResult[T interface{ boundTo(string) bool }](stdout, requestID string) (T, bool) {
	lines := strings.Split(stdout, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		var summary T
		if json.Unmarshal([]byte(line), &summary) == nil && summary.boundTo(requestID) {
			return summary, true
		}
	}
	var zero T
	return zero, false
}

func resultBoundTo(contract, kind, actualID, requestID string, success *bool) bool {
	return contract == ContractV2 && kind == "result" && actualID == requestID && success != nil
}

func (summary Summary) boundTo(requestID string) bool {
	return resultBoundTo(summary.Contract, summary.Kind, summary.RequestID, requestID, summary.Success)
}

func (summary SidecarSummary) boundTo(requestID string) bool {
	return resultBoundTo(summary.Contract, summary.Kind, summary.RequestID, requestID, summary.Success)
}

// EventCaptureTruncated distinguishes incomplete diagnostics from incomplete
// events: stderr truncation affects events only when the harness fell back to
// stderr. Independent event-file or harness truncation always remains sticky.
func EventCaptureTruncated(captured, reported, fallbackUsed, stderrTruncated bool) bool {
	return captured || reported || (fallbackUsed && stderrTruncated)
}

func (summary Summary) EventCaptureTruncated(captured, stderrTruncated bool) bool {
	return EventCaptureTruncated(captured, summary.EventsTruncated, summary.EventFallbackUsed, stderrTruncated)
}

func (envelope PersistentEnvelope) EventCaptureTruncated(captured bool) bool {
	return EventCaptureTruncated(captured, envelope.EventsTruncated, envelope.EventFallbackUsed, envelope.StderrTruncated)
}

func (summary SidecarSummary) EventCaptureTruncated(captured, stderrTruncated bool) bool {
	return EventCaptureTruncated(captured, summary.EventsTruncated, summary.EventFallbackUsed, stderrTruncated)
}

func (envelope SidecarPersistentEnvelope) EventCaptureTruncated(captured bool) bool {
	return EventCaptureTruncated(captured, envelope.EventsTruncated, envelope.EventFallbackUsed, envelope.StderrTruncated)
}
