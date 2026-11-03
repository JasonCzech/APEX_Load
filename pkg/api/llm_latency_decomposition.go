package api

import (
	"fmt"
	"math"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/engine"
)

// LLMLatencyDecomposition breaks down average end-to-end LLM request latency
// into Queue, Prefill, Network/Gateway overhead, and Decode phases.
// Arithmetic means are used because quantiles are not additive across phases.
type LLMLatencyDecomposition struct {
	TestID                 string   `json:"test_id"`
	Basis                  string   `json:"basis"` // always "mean"
	Source                 string   `json:"source"`
	TotalRequests          int64    `json:"total_requests"`
	StreamedRequests       int64    `json:"streamed_requests"`
	ServerTelemetrySamples int64    `json:"server_telemetry_samples"`
	ServerCoveragePct      float64  `json:"server_coverage_pct"`
	ClientAvgE2EMs         float64  `json:"client_avg_e2e_ms"`
	ClientAvgTTFTMs        float64  `json:"client_avg_ttft_ms"`
	ClientAvgDecodeMs      float64  `json:"client_avg_decode_ms"`
	ServerAvgQueueMs       float64  `json:"server_avg_queue_ms"`
	ServerAvgPrefillMs     float64  `json:"server_avg_prefill_ms"`
	ServerAvgDecodeMs      float64  `json:"server_avg_decode_ms"`
	NetworkGatewayAvgMs    float64  `json:"network_gateway_avg_ms"`
	QueueSharePct          float64  `json:"queue_share_pct"`
	PrefillSharePct        float64  `json:"prefill_share_pct"`
	NetworkGatewaySharePct float64  `json:"network_gateway_share_pct"`
	DecodeSharePct         float64  `json:"decode_share_pct"`
	SharedServerWarning    bool     `json:"shared_server_warning,omitempty"`
	Warnings               []string `json:"warnings,omitempty"`
}

// BuildLLMLatencyDecomposition computes the client-vs-server mean latency breakdown
// from an aggregated LLMSummary and optional server monitor points.
func BuildLLMLatencyDecomposition(testID string, summary *engine.LLMSummary, achievedTPS float64, monitorPoints []analysis.MetricPoint) *LLMLatencyDecomposition {
	out := &LLMLatencyDecomposition{
		TestID: testID,
		Basis:  "mean",
		Source: "client_only",
	}
	if summary == nil || summary.Requests == 0 {
		out.Warnings = append(out.Warnings, "No LLM requests were recorded in this run.")
		return out
	}
	engine.FinalizeLLMSummary(summary)
	out.TotalRequests = summary.Requests
	out.StreamedRequests = summary.StreamedRequests
	out.ClientAvgE2EMs = summary.AvgE2EMs
	out.ClientAvgTTFTMs = summary.AvgTTFTMs
	out.ClientAvgDecodeMs = math.Max(0, summary.AvgE2EMs-summary.AvgTTFTMs)

	samples := summary.ServerQueueSamples
	if summary.ServerPrefillSamples > samples {
		samples = summary.ServerPrefillSamples
	}
	out.ServerTelemetrySamples = samples
	if summary.StreamedRequests > 0 {
		out.ServerCoveragePct = math.Min(100.0, float64(samples)*100.0/float64(summary.StreamedRequests))
	} else if summary.Requests > 0 {
		out.ServerCoveragePct = math.Min(100.0, float64(samples)*100.0/float64(summary.Requests))
	}

	if samples > 0 {
		out.Source = "response_headers"
		out.ServerAvgQueueMs = summary.AvgServerQueueMs
		out.ServerAvgPrefillMs = summary.AvgServerPrefillMs
		out.ServerAvgDecodeMs = summary.AvgServerDecodeMs
		if out.ServerCoveragePct < 80.0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("Server timing headers covered %.1f%% of streamed requests; breakdown may not represent uninstrumented hops.", out.ServerCoveragePct))
		}
	} else if len(monitorPoints) > 0 {
		var queueMsSum, prefillMsSum, decodeMsSum float64
		var qN, pN, dN int
		var serverReqRate float64
		for _, pt := range monitorPoints {
			if pt.Value == nil {
				continue
			}
			name := strings.ToLower(pt.MetricName)
			val := *pt.Value
			if strings.Contains(name, "seconds") || pt.Unit == "s" || pt.Unit == "sec" || pt.Unit == "seconds" {
				val *= 1000.0
			}
			switch {
			case strings.Contains(name, "request_queue_time") || strings.Contains(name, "queue_duration"):
				queueMsSum += val
				qN++
			case strings.Contains(name, "request_prefill_time") || strings.Contains(name, "prefill_duration"):
				prefillMsSum += val
				pN++
			case strings.Contains(name, "request_decode_time") || strings.Contains(name, "decode_duration"):
				decodeMsSum += val
				dN++
			case strings.Contains(name, "request_success_total") || strings.Contains(name, "requests_per_sec"):
				if *pt.Value > serverReqRate {
					serverReqRate = *pt.Value
				}
			}
		}
		if qN > 0 || pN > 0 || dN > 0 {
			out.Source = "prometheus_monitors"
			out.ServerCoveragePct = 100.0
			if qN > 0 {
				out.ServerAvgQueueMs = queueMsSum / float64(qN)
			}
			if pN > 0 {
				out.ServerAvgPrefillMs = prefillMsSum / float64(pN)
			}
			if dN > 0 {
				out.ServerAvgDecodeMs = decodeMsSum / float64(dN)
			}
			if achievedTPS > 0 && serverReqRate > achievedTPS*1.25 {
				out.SharedServerWarning = true
				out.Warnings = append(out.Warnings, fmt.Sprintf("Server request rate (%.1f req/s) exceeds test throughput (%.1f req/s) by >25%%; server queue and prefill metrics likely include shared-tenant traffic.", serverReqRate, achievedTPS))
			}
		}
	}

	if out.Source == "client_only" {
		out.Warnings = append(out.Warnings, "No server-side queue or prefill telemetry was captured; showing client TTFT vs. decode split only.")
		out.PrefillSharePct = 0
		out.QueueSharePct = 0
		if out.ClientAvgE2EMs > 0 {
			out.NetworkGatewayAvgMs = out.ClientAvgTTFTMs
			out.NetworkGatewaySharePct = (out.ClientAvgTTFTMs / out.ClientAvgE2EMs) * 100.0
			out.DecodeSharePct = (out.ClientAvgDecodeMs / out.ClientAvgE2EMs) * 100.0
		}
		return out
	}

	// Compute Network/Gateway overhead as the residual between client TTFT and server (queue + prefill).
	serverFirstTokenMs := out.ServerAvgQueueMs + out.ServerAvgPrefillMs
	if out.ClientAvgTTFTMs >= serverFirstTokenMs {
		out.NetworkGatewayAvgMs = out.ClientAvgTTFTMs - serverFirstTokenMs
	} else {
		out.NetworkGatewayAvgMs = 0
		out.SharedServerWarning = true
		out.Warnings = append(out.Warnings, "Server queue + prefill mean exceeds client TTFT mean; server metrics may reflect background traffic or clock skew.")
	}

	decodeMs := out.ClientAvgDecodeMs
	if out.ServerAvgDecodeMs > 0 && out.ServerAvgDecodeMs < decodeMs {
		// Any decode transport overhead is added to Network/Gateway.
		out.NetworkGatewayAvgMs += decodeMs - out.ServerAvgDecodeMs
		decodeMs = out.ServerAvgDecodeMs
	}

	totalParts := out.ServerAvgQueueMs + out.ServerAvgPrefillMs + out.NetworkGatewayAvgMs + decodeMs
	if totalParts > 0 {
		out.QueueSharePct = (out.ServerAvgQueueMs / totalParts) * 100.0
		out.PrefillSharePct = (out.ServerAvgPrefillMs / totalParts) * 100.0
		out.NetworkGatewaySharePct = (out.NetworkGatewayAvgMs / totalParts) * 100.0
		out.DecodeSharePct = (decodeMs / totalParts) * 100.0
	}
	return out
}
