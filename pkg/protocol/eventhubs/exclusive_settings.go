package eventhubs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const FeatureExclusiveConsumerV1 = "eventhubs_exclusive_consumer_v1"
const MaxExclusiveOwnerLevel int64 = 1<<53 - 1

// ValidateExclusiveSettings requires an explicit partition and epoch, before
// defaults can silently broaden the operator's intended ownership scope.
func ValidateExclusiveSettings(mode, hub, group, partition string, level *int64, mock bool) error {
	if mode != "exclusive" {
		if level != nil {
			return fmt.Errorf("eventhubs owner_level requires exclusive consumer mode")
		}
		return nil
	}
	if mock || strings.TrimSpace(hub) == "" || strings.TrimSpace(group) == "" || strings.TrimSpace(partition) == "" || level == nil {
		return fmt.Errorf("Event Hubs exclusive consumer requires non-mock execution, explicit event_hub, consumer_group, partition_id and owner_level")
	}
	if *level < 0 || *level > MaxExclusiveOwnerLevel {
		return fmt.Errorf("eventhubs owner_level must be between 0 and %d", MaxExclusiveOwnerLevel)
	}
	return nil
}

func parseOwnerLevel(raw any) (*int64, error) {
	if raw == nil {
		return nil, nil
	}
	var level int64
	switch v := raw.(type) {
	case int:
		level = int64(v)
	case int64:
		level = v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v < 0 || v > float64(MaxExclusiveOwnerLevel) {
			return nil, fmt.Errorf("eventhubs owner_level must be an exact nonnegative JSON-safe integer")
		}
		level = int64(v)
	case string:
		var err error
		level, err = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("eventhubs owner_level must be an exact nonnegative JSON-safe integer")
		}
	default:
		return nil, fmt.Errorf("eventhubs owner_level must be an integer")
	}
	if level < 0 || level > MaxExclusiveOwnerLevel {
		return nil, fmt.Errorf("eventhubs owner_level must be between 0 and %d", MaxExclusiveOwnerLevel)
	}
	return &level, nil
}
