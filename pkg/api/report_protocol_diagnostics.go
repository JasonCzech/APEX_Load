package api

import (
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func protocolReportLines(report *RunArchiveReport) []string {
	lines := []string{"Client-observed measurements; unavailable phases are not inferred. Histogram percentiles are approximate. Findings are advisory."}
	for _, finding := range report.StatisticalFindings {
		lines = append(lines, fmt.Sprintf("%s at %s: %.2f standard deviations from %d comparable buckets (mean %.3f, standard deviation %.3f). Advisory association.", finding.MetricName, finding.Timestamp.Format("15:04:05Z07:00"), finding.ZScore, finding.BaselineBuckets, finding.BaselineMean, finding.BaselineStdDev))
	}
	for _, key := range sortedArchiveReportStatKeys(report.ByProperty) {
		stats := report.ByProperty[key]
		lines = append(lines, fmt.Sprintf("Property %s: %d observations, %.2f%% errors, mean %.3f ms, P95 %.3f ms", key, stats.Transactions, stats.ErrorPct, stats.AvgResponseMs, stats.P95Ms))
	}
	keys := sortedArchiveReportStatKeys(report.ByAction)
	// Rank method hotspots by measured client call P95, retaining stable names
	// for ties. This never substitutes call time for server processing time.
	sort.SliceStable(keys, func(i, j int) bool {
		var a, b float64
		if g := report.ByAction[keys[i]].GRPC; g != nil {
			a, _ = g.Call.Quantile(.95)
		}
		if g := report.ByAction[keys[j]].GRPC; g != nil {
			b, _ = g.Call.Quantile(.95)
		}
		return a > b
	})
	add := func(name, metric string, d protocol.DiagnosticDistribution) {
		if d.Count == 0 {
			return
		}
		p95, _ := d.Quantile(.95)
		lines = append(lines, fmt.Sprintf("%s · %s: %d samples; mean %.3f ms; approximate P95 %.3f ms", name, metric, d.Count, d.Sum/float64(d.Count), p95))
	}
	for _, name := range keys {
		a := report.ByAction[name]
		if a.Messaging != nil {
			if a.Messaging.Incomplete {
				lines = append(lines, name+" · Messaging coverage incomplete; partition skew is unscored.")
			}
			for _, d := range a.Messaging.Dimensions {
				lines = append(lines, fmt.Sprintf("%s · protocol %s / execution group %s / source %s / partition %s / subscription %s: %d client operations, %d sent, %d received, %d throttled, %d redelivered", name, firstNonEmpty(d.Protocol, "unavailable"), firstNonEmpty(d.Group, "unavailable"), firstNonEmpty(d.Source, "unattributed"), firstNonEmpty(d.Partition, "unavailable"), firstNonEmpty(d.Subscription, "unavailable"), d.Operations, d.Sent, d.Received, d.Throttled, d.Redelivered))
				for _, measurement := range []struct {
					name string
					m    protocol.MessagingMoments
				}{{"message age (ms)", d.Age}, {"offset backlog", d.Offset}, {"acknowledged client operation (ms)", d.Ack}} {
					if mean, ok := measurement.m.Mean(); ok {
						lines = append(lines, fmt.Sprintf("%s: %d observed samples, mean %.3f, maximum %.3f", measurement.name, measurement.m.Count, mean, measurement.m.Max))
					} else {
						lines = append(lines, measurement.name+": unavailable")
					}
				}
			}
		}
		if g := a.GRPC; g != nil {
			identity := name
			if g.Method != "" {
				identity += " " + g.Method + " (" + g.Mode + ")"
			}
			if g.MixedMethods {
				identity += " (multiple methods; hotspot identity unavailable)"
			}
			add(identity, "gRPC client call", g.Call)
			add(identity, "first response", g.FirstMessage)
			add(identity, "protobuf encoding per call", g.Encode)
			add(identity, "protobuf decoding per call", g.Decode)
			analysis := g.ClientAnalysis()
			metric := func(v *float64, unit string) string {
				if v == nil {
					return "unavailable"
				}
				return fmt.Sprintf("%.2f %s", *v, unit)
			}
			lines = append(lines,
				identity+" · Client-call message rate: "+metric(analysis.SentMessagesPerCallSecond, "sent/s")+" / "+metric(analysis.ReceivedMessagesPerCallSecond, "received/s"),
				identity+" · Client-call protobuf byte rate: "+metric(analysis.SentBytesPerCallSecond, "sent bytes/s")+" / "+metric(analysis.ReceivedBytesPerCallSecond, "received bytes/s"),
				identity+" · Mean protobuf message payload: "+metric(analysis.MeanSentPayloadBytes, "sent bytes")+" / "+metric(analysis.MeanReceivedPayloadBytes, "received bytes"),
				identity+" · Codec elapsed work / call wall time: "+metric(analysis.CodecWorkToCallWallPct, "%")+" (codec timings can overlap)",
			)

		}
		if w := a.WebSocket; w != nil {
			add(name, "WebSocket handshake", w.Handshake)
			add(name, "WebSocket reconnect", w.Reconnect)
			add(name, "WebSocket application write wait", w.WriteWait)
			add(name, "WebSocket matched authored ping/pong RTT", w.PingRTT)
			add(name, "WebSocket text receive wait", w.TextReceive)
			add(name, "WebSocket binary receive wait", w.BinaryReceive)
		}
		for _, finding := range a.DiagnosticFindings {
			lines = append(lines, fmt.Sprintf("%s · %s: %s", name, finding.Rule, finding.Message))
		}
		if len(lines) >= 200 {
			lines = append(lines, "Additional protocol diagnostics remain available in the structured report.")
			break
		}
	}
	if len(lines) == 1 {
		lines = append(lines, "No typed messaging, gRPC or WebSocket diagnostic samples were captured.")
	}
	if len(lines) > 200 {
		lines = append(lines[:200], "Additional protocol diagnostics remain available in the structured report.")
	}
	return lines
}

func protocolDiagnosticReportSection() reportSectionRenderer {
	return reportSectionRenderer{ID: "protocol_diagnostics", Label: "Protocol Diagnostics",
		HTML: func(d *runArchiveHTMLReportData) (template.HTML, error) {
			var b strings.Builder
			b.WriteString("<h2>Protocol Diagnostics</h2><ul>")
			for _, line := range protocolReportLines(d.Report) {
				b.WriteString("<li>" + template.HTMLEscapeString(line) + "</li>")
			}
			b.WriteString("</ul>")
			return template.HTML(b.String()), nil
		},
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Protocol Diagnostics")
			*lines = append(*lines, protocolReportLines(d.Report)...)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			for i, line := range protocolReportLines(d.Report) {
				if err := write("protocol_diagnostics", fmt.Sprint(i), "evidence", line); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
