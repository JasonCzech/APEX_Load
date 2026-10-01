package protocol

import "fmt"

// Diagnostic findings are advisory observations, never execution-stop policy.
// Counts describe retained diagnostic samples and preserve their denominators.
type DiagnosticFinding struct {
	Rule        string `json:"rule"`
	Protocol    string `json:"protocol"`
	Measurement string `json:"measurement"`
	Message     string `json:"message"`
	Observed    int64  `json:"observed"`
	Samples     int64  `json:"samples"`
	Advisory    bool   `json:"advisory"`
}

func ProtocolDiagnosticFindings(grpc *GRPCSummary, ws *WebSocketSummary) []DiagnosticFinding {
	var findings []DiagnosticFinding
	add := func(rule, p, metric, message string, observed, total int64) {
		if observed > 0 && total >= observed {
			findings = append(findings, DiagnosticFinding{Rule: "protocol-v1/" + rule, Protocol: p, Measurement: metric, Message: message, Observed: observed, Samples: total, Advisory: true})
		}
	}
	if grpc != nil {
		add("deadline-budget", "grpc", "deadline_consumption", fmt.Sprintf("%d of %d measured RPCs consumed at least 80%% of their deadline budget. Review the call latency and configured deadline.", grpc.NearDeadline, grpc.DeadlineObserved), grpc.NearDeadline, grpc.DeadlineObserved)
		add("deadline-exceeded", "grpc", "status", fmt.Sprintf("%d RPCs ended with DeadlineExceeded. This is the client-observed status; it does not establish server processing time.", grpc.StatusCounts[4]), grpc.StatusCounts[4], grpc.Samples-grpc.UnavailableStatus)
		add("unavailable", "grpc", "status", fmt.Sprintf("%d RPCs ended with Unavailable. Inspect endpoint readiness and transport evidence.", grpc.StatusCounts[14]), grpc.StatusCounts[14], grpc.Samples-grpc.UnavailableStatus)
		add("receive-cap", "grpc", "coverage", fmt.Sprintf("Observation stopped at the receive limit for %d RPCs; remote completion is unknown.", grpc.ReceiveLimitReached), grpc.ReceiveLimitReached, grpc.Samples)
	}
	if ws != nil {
		add("disconnect", "websocket", "disconnect", fmt.Sprintf("%d abnormal close or transport-error observations were retained. Inspect close codes and reconnect evidence.", ws.AbnormalCloses), ws.AbnormalCloses, ws.Samples)
		add("timeout", "websocket", "receive_wait", fmt.Sprintf("%d operations timed out. A receive timeout alone does not prove a dead connection.", ws.Timeouts), ws.Timeouts, ws.Samples)
		if ws.WriteWait.Count >= 5 && ws.WriteWait.Sum/float64(ws.WriteWait.Count) >= 50 {
			add("write-pressure", "websocket", "write_wait_ms", fmt.Sprintf("Application writes waited %.1f ms on average across %d measurements; inspect send throughput and pending writes.", ws.WriteWait.Sum/float64(ws.WriteWait.Count), ws.WriteWait.Count), ws.WriteWait.Count, ws.WriteWait.Count)
		}
	}
	return findings
}
