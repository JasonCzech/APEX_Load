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
	evidence := make(map[string]any, len(result.ProtocolEvidence)+3)
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
	if len(evidence) == 0 {
		return nil
	}
	return evidence
}
