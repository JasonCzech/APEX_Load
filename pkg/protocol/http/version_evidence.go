package http

import "github.com/jasonczech/apexload/pkg/protocol"

// Native HTTP producers bound their own typed Aura reports and scalar resource
// and warning observations. Preserve those types and bounds while adding the
// fixed-enum version receipts and the Alt-Svc HTTP/3 observation. A generic
// evidence projection would drop typed Aura reports or truncate raw payloads
// before fresh-credential classification.
func buildHTTPProtocolEvidence(_ *protocol.Action, result *protocol.Result) map[string]any {
	if result == nil {
		return nil
	}
	evidence := make(map[string]any, len(result.ProtocolEvidence))
	for key, value := range result.ProtocolEvidence {
		evidence[key] = value
	}
	if result.HTTPVersionPolicy != "" {
		evidence["http_version_policy"] = result.HTTPVersionPolicy
	}
	if result.HTTPVersionNegotiated != "" {
		evidence["http_version_negotiated"] = result.HTTPVersionNegotiated
	}
	if result.HTTPVersionSource != "" {
		evidence["http_version_source"] = result.HTTPVersionSource
	}
	if result.HTTPAltSvcH3Advertised {
		evidence["alt_svc_h3_advertised"] = true
	}
	if result.HTTP3FailureCertainty != "" {
		evidence["http3_failure_certainty"] = result.HTTP3FailureCertainty
	}
	for key, value := range result.HTTPReceipt {
		evidence[key] = value
	}
	if timings := buildHTTPProtocolTimings(evidence["timings"], result); len(timings) > 0 {
		evidence["timings"] = timings
	}
	if len(evidence) == 0 {
		return nil
	}
	return evidence
}

// buildHTTPProtocolTimings refreshes the shared timings contract over the
// timings an earlier evidence pass published for the same result. One
// httptrace spans a whole redirect chain: TTFB is the first hop's first byte
// while DNS, connect and TLS come from the last hop that dialed. Redirected
// actions therefore also carry redirect_count and redirect_ms so readers do not
// split the chain's total into per-phase spans as if it were one exchange.
func buildHTTPProtocolTimings(existing any, result *protocol.Result) map[string]any {
	if result == nil {
		return nil
	}
	prev, _ := existing.(map[string]any)
	measured := protocol.EvidenceTimings(result)
	timings := make(map[string]any, len(measured))
	for key, value := range prev {
		timings[key] = value
	}
	for key, value := range measured {
		timings[key] = value
	}
	delete(timings, "redirect_count")
	delete(timings, "redirect_ms")
	if telemetry := result.ServerTelemetry; telemetry != nil && telemetry.RedirectCount > 0 {
		timings["redirect_count"] = telemetry.RedirectCount
		if telemetry.RedirectMs > 0 {
			timings["redirect_ms"] = telemetry.RedirectMs
		}
	}
	return timings
}
