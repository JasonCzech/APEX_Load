package httpcontract

func NetworkShapingActive(mode, preset string, latencyMS, bandwidthKbps, jitterMS int, packetLossPct float64) bool {
	latency, bandwidth := ResolveNetworkProfile(mode, preset, latencyMS, bandwidthKbps)
	return latency > 0 || bandwidth > 0 || jitterMS > 0 || packetLossPct > 0
}
