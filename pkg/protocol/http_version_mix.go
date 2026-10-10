package protocol

// HTTPVersionFacts are one HTTP action's version facts, read from its result
// or from its archived protocol evidence: the effective policy, the version
// the wire response used, how the request was routed, and what HTTP/3 tried.
type HTTPVersionFacts struct {
	Policy        string // auto, 1.1, 2, auto+h3 or 3
	Negotiated    string // 1.1, 2 or 3, from wire evidence only
	Source        string // wire or cache
	Selection     string // http_version_selection_reason
	Fallback      string // http_version_fallback; "" or none when no fallback
	Attempts      int    // HTTP attempts in the logical action
	QUICAttempted bool
	QUICFailed    bool
	QUICError     string // quic_error_class of the failure, when one was recorded
}

// HTTPVersionBucket names the action's bucket in a version mix: the
// negotiated version, "cache" for a cache hit, or "unknown" when no wire
// response established one (a failure before any response, for example).
func (f HTTPVersionFacts) HTTPVersionBucket() string {
	if f.Source == "cache" {
		return "cache"
	}
	switch f.Negotiated {
	case "1.1", "2", "3":
		return f.Negotiated
	}
	return "unknown"
}

// FellBack reports whether an attempted QUIC path ended on TCP.
func (f HTTPVersionFacts) FellBack() bool { return f.Fallback != "" && f.Fallback != "none" }

// HTTPVersionFactsFromResult reads a native HTTP result's typed version fields
// and receipt. ok is false for results that carry no HTTP version facts.
func HTTPVersionFactsFromResult(r *Result) (HTTPVersionFacts, bool) {
	if r == nil {
		return HTTPVersionFacts{}, false
	}
	facts := HTTPVersionFacts{Policy: r.HTTPVersionPolicy, Negotiated: r.HTTPVersionNegotiated, Source: r.HTTPVersionSource}
	applyHTTPVersionReceipt(&facts, r.HTTPReceipt)
	return facts, facts.Policy != "" || facts.Negotiated != "" || facts.Source != ""
}

// HTTPVersionFactsFromEvidence reads the same facts from archived protocol
// evidence (the receipt fields are merged into it).
func HTTPVersionFactsFromEvidence(evidence map[string]any) (HTTPVersionFacts, bool) {
	if len(evidence) == 0 {
		return HTTPVersionFacts{}, false
	}
	str := func(key string) string { value, _ := evidence[key].(string); return value }
	facts := HTTPVersionFacts{Policy: str("http_version_policy"), Negotiated: str("http_version_negotiated"), Source: str("http_version_source")}
	applyHTTPVersionReceipt(&facts, evidence)
	return facts, facts.Policy != "" || facts.Negotiated != "" || facts.Source != ""
}

func applyHTTPVersionReceipt(facts *HTTPVersionFacts, receipt map[string]any) {
	if len(receipt) == 0 {
		return
	}
	facts.Selection, _ = receipt["http_version_selection_reason"].(string)
	facts.Fallback, _ = receipt["http_version_fallback"].(string)
	switch attempts := receipt["attempt_count"].(type) {
	case int:
		facts.Attempts = attempts
	case int64:
		facts.Attempts = int(attempts)
	case float64:
		facts.Attempts = int(attempts)
	}
	facts.QUICAttempted, _ = receipt["quic_attempted"].(bool)
	facts.QUICError, _ = receipt["quic_error_class"].(string)
	facts.QUICFailed = facts.QUICAttempted && (facts.QUICError != "" || facts.FellBack())
}

// HTTPVersionMix counts HTTP actions by negotiated version and routing. The
// ratios a report publishes are built from these counts with their
// denominators: fell back ÷ upgrade-eligible auto+h3 actions, and failed ÷
// attempted QUIC actions.
type HTTPVersionMix struct {
	Actions       int64            `json:"actions"`
	ByVersion     map[string]int64 `json:"by_version,omitempty"`
	BySelection   map[string]int64 `json:"by_selection,omitempty"`
	ByFallback    map[string]int64 `json:"by_fallback,omitempty"`
	ByQUICError   map[string]int64 `json:"by_quic_error,omitempty"`
	HTTP3Policies int64            `json:"http3_policies,omitempty"` // actions with an auto+h3 or 3 policy
	// UpgradeEligible counts auto+h3 actions routed to the alternative;
	// FellBack counts those that ended on TCP.
	UpgradeEligible int64 `json:"upgrade_eligible,omitempty"`
	FellBack        int64 `json:"fell_back,omitempty"`
	// QUICAttempted counts actions that tried QUIC; QUICFailed counts those
	// whose QUIC attempt failed (with or without a TCP fallback).
	QUICAttempted int64 `json:"quic_attempted,omitempty"`
	QUICFailed    int64 `json:"quic_failed,omitempty"`
	// Attempts sums the HTTP attempts of actions that recorded them (HTTP/3
	// policies do; TCP policies record none), so it can exceed Actions.
	Attempts int64 `json:"attempts,omitempty"`
}

// Add counts one action.
func (m *HTTPVersionMix) Add(facts HTTPVersionFacts) {
	if m == nil {
		return
	}
	m.Actions++
	increment(&m.ByVersion, facts.HTTPVersionBucket(), 1)
	if facts.Selection != "" {
		increment(&m.BySelection, facts.Selection, 1)
	}
	if facts.FellBack() {
		increment(&m.ByFallback, facts.Fallback, 1)
	}
	if facts.Policy == "auto+h3" || facts.Policy == "3" {
		m.HTTP3Policies++
	}
	if facts.Policy == "auto+h3" && facts.Selection == "upgraded" {
		m.UpgradeEligible++
		if facts.FellBack() {
			m.FellBack++
		}
	}
	if facts.QUICAttempted {
		m.QUICAttempted++
		if facts.QUICFailed {
			m.QUICFailed++
		}
	}
	if facts.QUICError != "" {
		increment(&m.ByQUICError, facts.QUICError, 1)
	}
	if facts.Attempts > 0 {
		m.Attempts += int64(facts.Attempts)
	}
}

// Merge adds another mix's counts.
func (m *HTTPVersionMix) Merge(other *HTTPVersionMix) {
	if m == nil || other == nil {
		return
	}
	m.Actions += other.Actions
	for key, count := range other.ByVersion {
		increment(&m.ByVersion, key, count)
	}
	for key, count := range other.BySelection {
		increment(&m.BySelection, key, count)
	}
	for key, count := range other.ByFallback {
		increment(&m.ByFallback, key, count)
	}
	for key, count := range other.ByQUICError {
		increment(&m.ByQUICError, key, count)
	}
	m.HTTP3Policies += other.HTTP3Policies
	m.UpgradeEligible += other.UpgradeEligible
	m.FellBack += other.FellBack
	m.QUICAttempted += other.QUICAttempted
	m.QUICFailed += other.QUICFailed
	m.Attempts += other.Attempts
}

// Clone returns an independent copy (nil for nil).
func (m *HTTPVersionMix) Clone() *HTTPVersionMix {
	if m == nil {
		return nil
	}
	out := &HTTPVersionMix{}
	out.Merge(m)
	return out
}

// UsedHTTP3 reports whether any action had an HTTP/3 policy or response.
func (m *HTTPVersionMix) UsedHTTP3() bool {
	return m != nil && (m.HTTP3Policies > 0 || m.ByVersion["3"] > 0 || m.QUICAttempted > 0)
}

// Ratio is a published ratio with its denominator; Value is 0 when the
// denominator is 0.
type Ratio struct {
	Numerator   int64   `json:"numerator"`
	Denominator int64   `json:"denominator"`
	Value       float64 `json:"value"`
}

func NewRatio(numerator, denominator int64) Ratio {
	ratio := Ratio{Numerator: numerator, Denominator: denominator}
	if denominator > 0 {
		ratio.Value = float64(numerator) / float64(denominator)
	}
	return ratio
}

// FallbackRatio is fell back ÷ upgrade-eligible auto+h3 actions.
func (m *HTTPVersionMix) FallbackRatio() Ratio {
	if m == nil {
		return Ratio{}
	}
	return NewRatio(m.FellBack, m.UpgradeEligible)
}

// QUICFailureRatio is failed ÷ attempted QUIC actions.
func (m *HTTPVersionMix) QUICFailureRatio() Ratio {
	if m == nil {
		return Ratio{}
	}
	return NewRatio(m.QUICFailed, m.QUICAttempted)
}

func increment(counts *map[string]int64, key string, by int64) {
	if *counts == nil {
		*counts = map[string]int64{}
	}
	(*counts)[key] += by
}

// ArchiveHTTPVersionFields are the typed archive event fields for a result:
// the negotiated wire version (empty for a cache hit), the fallback kind when
// a QUIC path ended on TCP, and the action's HTTP attempts.
func ArchiveHTTPVersionFields(r *Result) (version, fallback string, attempts int) {
	facts, ok := HTTPVersionFactsFromResult(r)
	if !ok {
		return "", "", 0
	}
	if facts.Source == "wire" {
		version = facts.Negotiated
	}
	if facts.FellBack() {
		fallback = facts.Fallback
	}
	return version, fallback, facts.Attempts
}
