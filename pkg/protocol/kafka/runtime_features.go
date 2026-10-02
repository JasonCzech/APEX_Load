package kafka

import "github.com/jasonczech/apexload/pkg/protocol"

const FeatureRealSeekV1 = "kafka_real_seek_v1"
const FeatureConsumerTransactionsV1 = "kafka_consumer_transactions_v1"

// Capability discovery performs no network I/O and preserves mock availability.
func (*Kafka) RuntimeCapabilities() protocol.RuntimeCapabilities {
	out := protocol.RuntimeCapabilities{Available: true}
	if realKafkaAvailable {
		out.Features = []string{FeatureRealSeekV1, FeatureConsumerTransactionsV1}
	}
	return out
}
