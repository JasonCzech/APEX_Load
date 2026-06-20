//go:build kafka_real

package dsl

// KafkaRealAdapterAvailable reports whether the real Kafka network adapter
// (franz-go) is compiled into this build. It is true only under -tags kafka_real.
func KafkaRealAdapterAvailable() bool {
	return true
}
