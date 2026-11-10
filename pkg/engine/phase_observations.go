package engine

import "time"

// PhaseObservationVersion is the wire version of PhaseObservations. A
// generator that predates it sends no version, and a merge that includes such
// a part reports the observations as unknown rather than as zero.
const PhaseObservationVersion uint32 = 1

// PhaseObservations counts the samples in which a connection-setup phase
// actually happened (took measurable time). A reused keep-alive connection,
// cached DNS or cleartext request records 0 for that phase. The phase
// averages divide by every sample, so these counts tell "fast" apart from
// "skipped": avg_tls_ms * count / tls is the average handshake, and
// 1 - connect / count is the connection reuse rate.
//
// It also keeps the samples with no TTFB split (TTFB 0). That is either a
// request that never got a first response byte (a timeout or connection
// failure) or a sample whose protocol records no TTFB at all (gRPC, JDBC,
// WebSocket, ...); an authored transaction holding such a child reports no
// TTFB either (sourceTransactionPhaseRollup.boundedTo). A sample does not say
// which, so the bucket is "no TTFB (unsplit)", not a failure signal. Their
// setup still counts in the DNS/connect/TLS averages, but the rest of their
// time is neither server nor download time, so ResponseSplit reports it as
// that unsplit "no response" remainder and keeps the stacked phases equal to
// the average duration.
//
// Server = TTFB - setup needs setup and TTFB from the same request leg. The
// HTTP protocol measures both on the first leg of a redirect chain (TTFB is
// the first response byte; setup that happens after it, on a later leg's new
// connection, is not counted), so a later leg's time is in the time after
// TTFB (download) rather than subtracted from the first leg's server time.
type PhaseObservations struct {
	DNS     int64 `json:"dns"`
	Connect int64 `json:"connect"`
	TLS     int64 `json:"tls"`
	// NoTTFB counts samples without a first byte; NoTTFBSetupMs sums their
	// DNS+connect+TLS and NoTTFBRemainderMs their duration minus that setup.
	NoTTFB            int64   `json:"no_ttfb,omitempty"`
	NoTTFBSetupMs     float64 `json:"no_ttfb_setup_ms,omitempty"`
	NoTTFBRemainderMs float64 `json:"no_ttfb_remainder_ms,omitempty"`
}

// Observe counts the phases one sample spent time in, and keeps the setup and
// remainder of a sample that got no first byte.
func (p *PhaseObservations) Observe(dns, connect, tls, ttfb, duration time.Duration) {
	p.ObserveMs(durationToMilliseconds(dns), durationToMilliseconds(connect), durationToMilliseconds(tls), durationToMilliseconds(ttfb), durationToMilliseconds(duration))
}

// ObserveMs is Observe for millisecond values.
func (p *PhaseObservations) ObserveMs(dnsMs, connectMs, tlsMs, ttfbMs, durationMs float64) {
	if p == nil {
		return
	}
	if dnsMs > 0 {
		p.DNS++
	}
	if connectMs > 0 {
		p.Connect++
	}
	if tlsMs > 0 {
		p.TLS++
	}
	if ttfbMs > 0 {
		return
	}
	setup := max(0, dnsMs) + max(0, connectMs) + max(0, tlsMs)
	p.NoTTFB++
	p.NoTTFBSetupMs += setup
	p.NoTTFBRemainderMs += max(0, durationMs-setup)
}

// Add sums another set of observations into p.
func (p *PhaseObservations) Add(other PhaseObservations) {
	if p == nil {
		return
	}
	p.DNS += other.DNS
	p.Connect += other.Connect
	p.TLS += other.TLS
	p.NoTTFB += other.NoTTFB
	p.NoTTFBSetupMs += other.NoTTFBSetupMs
	p.NoTTFBRemainderMs += other.NoTTFBRemainderMs
}

// ResponseSplit is where the average sample's time after connection setup
// went, given the averages over count samples (every average divides by all
// of them). Samples with a first byte split into server (TTFB - setup) and
// download (duration - TTFB); samples without one contribute their remainder
// as noResponse. So avgDNS + avgConnect + avgTLS + server + download +
// noResponse == avgMs whenever each sample's setup <= TTFB <= duration. ok is
// false when the observations are unknown (nil, from an older generator) or
// there are no samples; callers then fall back to TTFB-only arithmetic.
func (p *PhaseObservations) ResponseSplit(count int64, avgMs, avgDNSMs, avgConnectMs, avgTLSMs, avgTTFBMs float64) (serverMs, downloadMs, noResponseMs float64, ok bool) {
	if p == nil || count <= 0 {
		return 0, 0, 0, false
	}
	n := float64(count)
	setup := avgDNSMs + avgConnectMs + avgTLSMs
	noTTFBSetup := p.NoTTFBSetupMs / n
	noResponseMs = max(0, p.NoTTFBRemainderMs/n)
	// Samples without a first byte add 0 to the TTFB sum but their setup to
	// the setup sum, so only the first-byte samples' setup comes off TTFB.
	serverMs = max(0, avgTTFBMs-(setup-noTTFBSetup))
	downloadMs = max(0, avgMs-avgTTFBMs-noTTFBSetup-noResponseMs)
	return serverMs, downloadMs, noResponseMs, true
}

// Clone returns a copy, or nil for nil.
func (p *PhaseObservations) Clone() *PhaseObservations {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

// PhaseObservationMerge merges observations from several parts. Any part
// with samples but without observations (an older generator) makes the
// merged result unknown.
type PhaseObservationMerge struct {
	sum     PhaseObservations
	unknown bool
	seen    bool
}

// Add folds one part with count samples into the merge.
func (m *PhaseObservationMerge) Add(observations *PhaseObservations, count int64) {
	if m == nil || count <= 0 {
		return
	}
	m.seen = true
	if observations == nil {
		m.unknown = true
		return
	}
	m.sum.Add(*observations)
}

// Result returns the merged observations, or nil when any part was unknown or
// no part had samples.
func (m *PhaseObservationMerge) Result() *PhaseObservations {
	if m == nil || !m.seen || m.unknown {
		return nil
	}
	out := m.sum
	return &out
}
