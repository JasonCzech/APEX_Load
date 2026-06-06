//go:build !kafka_real

package kafka

// newRealClient returns the non-mock client for the default build. The real
// franz-go-backed adapter is only compiled in with the `kafka_real` build tag;
// without it, the non-mock path fails loudly with actionable guidance (mirroring
// the JMS Solace/IBM MQ stubs).
func newRealClient(cfg config) kafkaClient {
	return unavailableClient{brokers: cfg.Brokers}
}
