package api

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Regression is an explicit acceptance assessment, separate from advisory findings.
// Every requested cell must be comparable before a passed verdict is possible.
type BaselineRegressionVerdict struct {
	Status      string                       `json:"status"`
	Metrics     []string                     `json:"metrics"`
	Evaluated   int                          `json:"evaluated"`
	Unavailable int                          `json:"unavailable"`
	Evidence    []BaselineRegressionEvidence `json:"evidence"`
}
type BaselineRegressionEvidence struct {
	RowID      string   `json:"row_id"`
	BaselineID string   `json:"baseline_id"`
	Metric     string   `json:"metric"`
	Status     string   `json:"status"`
	Current    *float64 `json:"current,omitempty"`
	Baseline   *float64 `json:"baseline,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

var baselineRegressionMetrics = []string{"successful_mean", "successful_p95", "successful_p99", "successful_throughput", "error_rate_pct", "consumer_lag_ms", "consumer_offset_lag", "acknowledgement_ms", "grpc_call_p95", "grpc_first_message_p95", "websocket_handshake_p95", "websocket_write_wait_p95", "websocket_ping_rtt_p95", "grpc_encode_p95", "grpc_decode_p95", "grpc_message_throughput", "websocket_message_throughput", "websocket_reconnect_p95", "websocket_send_p95", "websocket_receive_p95", "websocket_text_receive_p95", "websocket_binary_receive_p95"}

func validateBaselineRegressionRequest(r BaselineComparisonRequest) error {
	for i, list := range [][]string{r.RegressionMetrics, r.RegressionProtocols, r.RegressionGroups, r.RegressionIdentities} {
		if len(list) > 32 {
			return fmt.Errorf("regression selection exceeds 32 entries")
		}
		limit := 256
		if i == 3 {
			limit = 1024
		}
		seen := map[string]bool{}
		for _, v := range list {
			if v == "" || len(v) > limit || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\r\n\x00") || seen[v] {
				return fmt.Errorf("invalid or duplicate regression selection")
			}
			seen[v] = true
		}
	}
	for _, metric := range r.RegressionMetrics {
		if !slices.Contains(baselineRegressionMetrics, metric) {
			return fmt.Errorf("unsupported regression metric %q", metric)
		}
	}
	return nil
}
func baselineRegressionValue(m *BaselineMeasurement, metric string) (float64, bool) {
	if m == nil || m.Samples < 100 {
		return 0, false
	}
	switch metric {
	case "successful_mean":
		return m.Mean, m.Successes >= 100 && finiteComparison(m.Mean)
	case "successful_p95":
		return m.P95, m.Successes >= 100 && finiteComparison(m.P95)
	case "successful_p99":
		return m.P99, m.Successes >= 1000 && finiteComparison(m.P99)
	case "successful_throughput":
		return m.SuccessfulThroughput, finiteComparison(m.SuccessfulThroughput)
	case "error_rate_pct":
		return m.ErrorPct, finiteComparison(m.ErrorPct)
	}
	if strings.HasPrefix(metric, "grpc_") && m.GRPC != nil && !m.GRPC.MixedMethods && m.GRPC.Samples == m.Samples {
		if metric == "grpc_message_throughput" {
			return float64(m.GRPC.MessagesReceived) * m.Throughput / float64(m.Samples), m.GRPC.MessagesReceived >= 0
		}
		d := m.GRPC.Call
		if metric == "grpc_encode_p95" {
			d = m.GRPC.Encode
		}
		if metric == "grpc_decode_p95" {
			d = m.GRPC.Decode
		}
		if metric == "grpc_first_message_p95" {
			d = m.GRPC.FirstMessage
		}
		x, ok := d.Quantile(.95)
		return x, ok && d.Count == m.Samples
	}
	if strings.HasPrefix(metric, "websocket_") && m.WebSocket != nil && m.WebSocket.Samples == m.Samples {
		if metric == "websocket_message_throughput" {
			return float64(m.WebSocket.MessagesReceived) * m.Throughput / float64(m.Samples), m.WebSocket.MessagesReceived >= 0
		}
		d := m.WebSocket.Handshake
		if metric == "websocket_reconnect_p95" {
			d = m.WebSocket.Reconnect
		}
		if metric == "websocket_send_p95" {
			d = m.WebSocket.Send
		}
		if metric == "websocket_receive_p95" {
			d = m.WebSocket.Receive
		}
		if metric == "websocket_text_receive_p95" {
			d = m.WebSocket.TextReceive
		}
		if metric == "websocket_binary_receive_p95" {
			d = m.WebSocket.BinaryReceive
		}
		if metric == "websocket_write_wait_p95" {
			d = m.WebSocket.WriteWait
		}
		if metric == "websocket_ping_rtt_p95" {
			d = m.WebSocket.PingRTT
		}
		x, ok := d.Quantile(.95)
		return x, ok && d.Count == m.Samples
	}
	if m.Messaging == nil || m.Messaging.Incomplete {
		return 0, false
	}
	var count int64
	var sum float64
	for _, d := range m.Messaging.Dimensions {
		switch metric {
		case "consumer_lag_ms":
			count += d.Age.Count
			sum += d.Age.Sum
		case "consumer_offset_lag":
			count += d.Offset.Count
			sum += d.Offset.Sum
		case "acknowledgement_ms":
			count += d.Ack.Count
			sum += d.Ack.Sum
		}
	}
	if count != m.Samples || count == 0 || !finiteComparison(sum) {
		return 0, false
	}
	return sum / float64(count), true
}
func evaluateBaselineRegression(s *BaselineComparisonSnapshot) *BaselineRegressionVerdict {
	metrics := slices.Clone(s.Request.RegressionMetrics)
	if len(metrics) == 0 {
		metrics = []string{"successful_p95", "successful_throughput", "error_rate_pct"}
	}
	v := &BaselineRegressionVerdict{Status: "passed", Metrics: metrics, Evidence: []BaselineRegressionEvidence{}}
	for _, row := range s.Transactions {
		if len(s.Request.RegressionIdentities) > 0 && !slices.Contains(s.Request.RegressionIdentities, row.Name) {
			continue
		}
		if len(s.Request.RegressionProtocols) > 0 && !slices.Contains(s.Request.RegressionProtocols, row.Protocol) {
			continue
		}
		if len(s.Request.RegressionGroups) > 0 && !slices.Contains(s.Request.RegressionGroups, row.Group) {
			continue
		}
		for _, b := range s.Baselines {
			cell := row.Baselines[b.ID]
			for _, metric := range metrics {
				e := BaselineRegressionEvidence{RowID: row.ID, BaselineID: b.ID, Metric: metric, Status: "not_evaluated"}
				a, okA := baselineRegressionValue(row.Current, metric)
				base, okB := baselineRegressionValue(cell.Measurement, metric)
				qualified := s.Current.Status != "COMPLETE" || b.Status != "COMPLETE"
				for _, q := range cell.Qualification {
					if q != "Fewer than 100 successful transaction samples" {
						qualified = true
					}
				}
				for _, d := range s.Setup {
					if d.BaselineID == b.ID && d.Status != "match" {
						qualified = true
					}
				}
				if !okA || !okB || qualified || row.Window == nil || cell.Window == nil {
					e.Reason = "Unavailable measurements or incompatible workload/version/window"
					v.Unavailable++
					v.Evidence = append(v.Evidence, e)
					continue
				}
				pct, absolute := s.Policy.LatencyPct, s.Policy.LatencyMs
				if metric == "error_rate_pct" {
					pct, absolute = 0, s.Policy.ErrorPoints
				}
				if metric == "successful_throughput" || strings.HasSuffix(metric, "message_throughput") {
					pct, absolute = s.Policy.ThroughputPct, 0
				}
				if base == 0 && absolute == 0 {
					e.Reason = "Baseline zero requires an absolute threshold"
					v.Unavailable++
					v.Evidence = append(v.Evidence, e)
					continue
				}
				if metric == "consumer_offset_lag" {
					pct, absolute = s.Policy.LatencyPct, 0
				}
				delta := a - base
				if metric == "successful_throughput" || strings.HasSuffix(metric, "message_throughput") {
					delta = -delta
				}
				regression := delta > 0 && delta >= absolute && (pct == 0 || base == 0 || 100*delta/math.Abs(base) >= pct)
				e.Current, e.Baseline = &a, &base
				e.Status = "passed"
				v.Evaluated++
				if regression {
					e.Status = "regressed"
					v.Status = "regressed"
				}
				v.Evidence = append(v.Evidence, e)
			}
		}
	}
	if len(v.Evidence) == 0 {
		v.Unavailable++
		v.Evidence = append(v.Evidence, BaselineRegressionEvidence{Status: "not_evaluated", Reason: "No selected comparable transaction identities"})
	}
	if v.Status != "regressed" && v.Unavailable > 0 {
		v.Status = "not_evaluated"
	}
	return v
}
