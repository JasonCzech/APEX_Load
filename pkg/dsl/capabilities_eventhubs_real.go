//go:build eventhubs_real

package dsl

// EventHubsRealAdapterAvailable reports whether the real Azure Event Hubs
// (azeventhubs/AMQP) adapter is compiled into this build. It is true only under
// -tags eventhubs_real, mirroring KafkaRealAdapterAvailable.
func EventHubsRealAdapterAvailable() bool {
	return true
}
