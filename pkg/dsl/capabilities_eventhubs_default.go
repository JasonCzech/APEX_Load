//go:build !eventhubs_real

package dsl

// EventHubsRealAdapterAvailable reports whether the real Azure Event Hubs
// (azeventhubs/AMQP) adapter is compiled into this build. Default builds ship
// only the in-memory mock plus a fail-closed stub, so it reports false and
// non-mock Event Hubs scripts fail closed during validation.
func EventHubsRealAdapterAvailable() bool {
	return false
}
