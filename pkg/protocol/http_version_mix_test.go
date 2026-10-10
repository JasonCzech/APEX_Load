package protocol

import "testing"

func TestHTTPVersionFactsReadResultsAndArchivedEvidence(t *testing.T) {
	result := &Result{HTTPVersionPolicy: "auto+h3", HTTPVersionNegotiated: "2", HTTPVersionSource: "wire", HTTPReceipt: map[string]any{
		"http_version_selection_reason": "upgraded", "http_version_fallback": "quic_connect_failed", "attempt_count": 2, "quic_attempted": true,
	}}
	facts, ok := HTTPVersionFactsFromResult(result)
	if !ok || facts.HTTPVersionBucket() != "2" || !facts.FellBack() || !facts.QUICFailed || facts.Attempts != 2 {
		t.Fatalf("result facts = %+v", facts)
	}
	// Archived JSON numbers decode as float64.
	evidence, ok := HTTPVersionFactsFromEvidence(map[string]any{"http_version_policy": "3", "attempt_count": 1.0, "quic_attempted": true, "quic_error_class": "timeout"})
	if !ok || evidence.HTTPVersionBucket() != "unknown" || evidence.Attempts != 1 || !evidence.QUICFailed || evidence.FellBack() {
		t.Fatalf("evidence facts = %+v", evidence)
	}
	if _, ok := HTTPVersionFactsFromEvidence(map[string]any{"protocol": "kafka"}); ok {
		t.Fatal("non-HTTP evidence has no version facts")
	}
	if _, ok := HTTPVersionFactsFromResult(&Result{}); ok {
		t.Fatal("a result without version fields has no facts")
	}

	// A cache hit is never a wire version in the archive fields.
	version, fallback, attempts := ArchiveHTTPVersionFields(&Result{HTTPVersionPolicy: "auto", HTTPVersionNegotiated: "2", HTTPVersionSource: "cache"})
	if version != "" || fallback != "" || attempts != 0 {
		t.Fatalf("cache hit fields = %q %q %d", version, fallback, attempts)
	}
	version, fallback, attempts = ArchiveHTTPVersionFields(result)
	if version != "2" || fallback != "quic_connect_failed" || attempts != 2 {
		t.Fatalf("fallback fields = %q %q %d", version, fallback, attempts)
	}
}

func TestHTTPVersionMixCountsMergesAndPublishesRatios(t *testing.T) {
	var mix HTTPVersionMix
	mix.Add(HTTPVersionFacts{Policy: "auto+h3", Negotiated: "3", Source: "wire", Selection: "upgraded", Fallback: "none", Attempts: 1, QUICAttempted: true})
	mix.Add(HTTPVersionFacts{Policy: "auto+h3", Negotiated: "2", Source: "wire", Selection: "upgraded", Fallback: "quic_connect_failed", Attempts: 2, QUICAttempted: true, QUICFailed: true})
	mix.Add(HTTPVersionFacts{Policy: "auto", Negotiated: "2", Source: "cache"})
	if mix.Actions != 3 || mix.ByVersion["cache"] != 1 || mix.ByFallback["none"] != 0 || mix.UpgradeEligible != 2 || mix.FellBack != 1 || mix.Attempts != 3 || !mix.UsedHTTP3() {
		t.Fatalf("mix = %+v", mix)
	}
	if ratio := mix.FallbackRatio(); ratio != (Ratio{Numerator: 1, Denominator: 2, Value: 0.5}) {
		t.Fatalf("fallback ratio = %+v", ratio)
	}
	clone := mix.Clone()
	clone.Merge(&mix)
	if clone.Actions != 6 || clone.ByVersion["2"] != 2 || mix.ByVersion["2"] != 1 || clone.Attempts != 6 {
		t.Fatalf("clone/merge = %+v (original %+v)", clone, mix)
	}
	var nilMix *HTTPVersionMix
	nilMix.Add(HTTPVersionFacts{Negotiated: "3"})
	nilMix.Merge(&mix)
	if nilMix.Clone() != nil || nilMix.UsedHTTP3() || nilMix.FallbackRatio() != (Ratio{}) {
		t.Fatal("nil mix must be inert")
	}
	if NewRatio(1, 0).Value != 0 {
		t.Fatal("a ratio without a denominator is 0")
	}
}
