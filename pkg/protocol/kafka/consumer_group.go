package kafka

import "fmt"

// ValidateConsumerGroupOverride enforces the real client's single-group scope.
// Empty identities select the configured group. Callers must resolve templates
// before runtime admission; static validation may defer unresolved identities.
func ValidateConsumerGroupOverride(group, configuredGroup string) error {
	if group != "" && group != configuredGroup {
		return fmt.Errorf("per-action consumer_group differs from the configured config.kafka.consumer_group; the real Kafka adapter binds one group per client")
	}
	return nil
}
