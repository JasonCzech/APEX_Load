package kafka

import (
	"github.com/jasonczech/apexload/pkg/protocol/kafkacontract"
)

// ValidateConsumerGroupOverride enforces the real client's single-group scope.
// Empty identities select the configured group. Callers must resolve templates
// before runtime admission; static validation may defer unresolved identities.
func ValidateConsumerGroupOverride(group, configuredGroup string) error {
	return kafkacontract.ValidateConsumerGroupOverride(group, configuredGroup)
}
