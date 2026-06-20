//go:build !kafka_real

package dsl

// KafkaRealAdapterAvailable reports whether the real Kafka network adapter
// (franz-go) is compiled into this build. Default builds ship only the
// in-memory mock, so it reports false and non-mock Kafka scripts fail closed
// during validation.
func KafkaRealAdapterAvailable() bool {
	return false
}
