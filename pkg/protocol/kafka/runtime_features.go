package kafka

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/kafkacontract"
)

const FeatureRealSeekV1 = kafkacontract.FeatureRealSeekV1
const FeatureConsumerTransactionsV1 = kafkacontract.FeatureConsumerTransactionsV1

// Capability discovery performs no network I/O and preserves mock availability.
func (*Kafka) RuntimeCapabilities() protocol.RuntimeCapabilities {
	out := protocol.RuntimeCapabilities{Available: true}
	if realKafkaAvailable {
		out.Features = []string{FeatureRealSeekV1, FeatureConsumerTransactionsV1}
	}
	return out
}
