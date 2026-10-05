package dsl

import (
	eventhubsprotocol "github.com/jasonczech/apexload/pkg/protocol/eventhubs"
	kafkaprotocol "github.com/jasonczech/apexload/pkg/protocol/kafka"
	"strings"
)

// RequiredMessagingRuntimeFeatures rejects older or stub peers before execution.
func RequiredMessagingRuntimeFeatures(script *Script) []string {
	if script == nil {
		return nil
	}
	var features []string
	if strings.EqualFold(script.Protocol, "kafka") {
		cfg := script.Config.Kafka
		if cfg.ConsumerTransactions {
			features = append(features, kafkaprotocol.FeatureConsumerTransactionsV1)
		}
		if !cfg.Mock {
			seek := false
			visitScriptActions(script, func(_ string, a Action) {
				if strings.EqualFold(a.Type, "kafka_seek") {
					seek = true
				}
			})
			if seek {
				features = append(features, kafkaprotocol.FeatureRealSeekV1)
			}
		}
	}
	if strings.EqualFold(script.Protocol, "eventhubs") && script.Config.EventHubs.ConsumerMode == "processor" {
		features = append(features, eventhubsprotocol.FeatureProcessorCheckpointsV1, eventhubsprotocol.FeatureProcessorReservationsV1)
		if script.Config.EventHubs.ObservePartitionTail {
			features = append(features, eventhubsprotocol.FeatureProcessorProgressV1, eventhubsprotocol.FeaturePartitionTailV1, "messaging_progress_v1")
		}
	}
	if strings.EqualFold(script.Protocol, "eventhubs") && script.Config.EventHubs.ConsumerMode == "exclusive" {
		features = append(features, eventhubsprotocol.FeatureExclusiveConsumerV1)
	}
	return features
}
