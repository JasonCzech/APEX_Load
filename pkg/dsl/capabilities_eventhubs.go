package dsl

// EventHubsRealAdapterAvailable reports whether the real Azure Event Hubs
// (azeventhubs/AMQP) adapter is compiled into this build.
//
// EH.3 part 1 ships the native eventhubs protocol with only the in-memory mock
// and a fail-closed stub, so this is unconditionally false and every non-mock
// Event Hubs script fails closed during validation. EH.3 part 2 replaces this
// file with a build-tag pair (capabilities_eventhubs_real.go / _default.go)
// gated on -tags eventhubs_real, mirroring KafkaRealAdapterAvailable.
func EventHubsRealAdapterAvailable() bool {
	return false
}
