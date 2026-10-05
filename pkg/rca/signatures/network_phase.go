package signatures

// NetworkPhase: name resolution, connection set-up or the TLS handshake to
// the affected dependencies slowed, as the archived request timing shows.
var NetworkPhase = Signature{
	Name:          "network_phase",
	Required:      []string{KindDNS, KindConnect, KindTLS},
	Indicators:    []Indicator{{KindDNS, Increase}, {KindConnect, Increase}, {KindTLS, Increase}},
	Symptoms:      []string{KindLatency, KindErrors},
	Competing:     []string{"retries", "resource_pressure"},
	Falsification: "DNS, connect and TLS times to the affected dependencies stay within their baselines",
}
