//go:build eventhubs_real

package eventhubs

import (
	"errors"
	"fmt"
	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
)

func exclusivePartitionOptions(cfg config, start eh.StartPosition) (*eh.PartitionClientOptions, error) {
	if err := ValidateExclusiveSettings(cfg.ConsumerMode, cfg.EventHub, cfg.ConsumerGroup, cfg.PartitionID, cfg.OwnerLevel, cfg.Mock); err != nil {
		return nil, err
	}
	options := &eh.PartitionClientOptions{StartPosition: start}
	if cfg.ConsumerMode == "exclusive" && cfg.OwnerLevel != nil {
		level := *cfg.OwnerLevel
		options.OwnerLevel = &level
		options.Prefetch = -1
	}
	return options, nil
}

func (c *realClient) exclusiveReceiveError(hub, group string) error {
	if c.cfg.ConsumerMode != "exclusive" {
		return nil
	}
	if hub != c.cfg.EventHub || group != c.cfg.ConsumerGroup {
		return fmt.Errorf("exclusive consumer can only receive from its configured event hub and consumer group")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ownershipLost
}

// A fenced exclusive client never reacquires automatically with its old epoch.
// The operator must explicitly create a new execution to try new ownership.
func (c *realClient) recordExclusiveOwnershipLoss(err error) error {
	var serviceErr *eh.Error
	if c.cfg.ConsumerMode != "exclusive" || !errors.As(err, &serviceErr) || serviceErr.Code != eh.ErrorCodeOwnershipLost {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ownershipLost == nil {
		c.ownershipLost = fmt.Errorf("exclusive Event Hubs ownership lost; recreate the execution with an explicitly selected owner_level: %w", err)
	}
	return c.ownershipLost
}
