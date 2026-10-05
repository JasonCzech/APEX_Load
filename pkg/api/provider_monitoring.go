package api

// nativeProviderPack identifies providers using durable canonical monitoring.
// Older point-only collector packs retain their existing ingestion contract.
func nativeProviderPack(pack string) bool {
	return pack == "appdynamics" || pack == "dynatrace" || pack == "prometheus-query"
}
