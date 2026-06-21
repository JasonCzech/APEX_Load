//go:build !eventhubs_real

package eventhubs

// newRealClient returns the non-mock client for the default build. The real
// azeventhubs-backed adapter is only compiled in with the `eventhubs_real` build
// tag (EH.3 part 2); without it, the non-mock path fails closed with actionable
// guidance (mirroring the Kafka/JMS stubs).
func newRealClient(cfg config) eventHubsClient {
	ns := cfg.Namespace
	if ns == "" {
		ns = cfg.ConnectionString
	}
	return unavailableClient{namespace: ns}
}
