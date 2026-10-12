package api

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	// These controller-side limits are intentionally above the platform's current
	// published scale target while still bounding parsing, scheduling, goroutine
	// creation, and the int32 controller-to-generator transport.
	maxManagedTestGroups         = engine.MaxEngineGroups
	maxManagedLoadStagesPerGroup = 256
	maxManagedTestVUsers         = engine.MaxEngineVUsers
)

func validateManagedTestWorkloadBounds(test *ManagedTest) error {
	if test == nil {
		return fmt.Errorf("test is required")
	}
	if err := validateRecoverySpareWorkload(test); err != nil {
		return err
	}
	if len(test.Groups) == 0 {
		return fmt.Errorf("test must contain at least one group")
	}
	if len(test.Groups) > maxManagedTestGroups {
		return fmt.Errorf("test contains %d groups; limit is %d", len(test.Groups), maxManagedTestGroups)
	}
	total := 0
	for index, group := range test.Groups {
		if _, err := distributedRendezvousContracts(test, group); err != nil {
			return fmt.Errorf("group[%d]: %w", index, err)
		}
		if group.VUsers < 0 || group.VUsers > maxManagedTestVUsers {
			return fmt.Errorf("group[%d] vusers must be between 0 and %d", index, maxManagedTestVUsers)
		}
		if len(group.Stages) > maxManagedLoadStagesPerGroup {
			return fmt.Errorf("group[%d] contains %d stages; limit is %d", index, len(group.Stages), maxManagedLoadStagesPerGroup)
		}
		for stageIndex, stage := range group.Stages {
			if stage.TargetVUsers < 0 || stage.TargetVUsers > maxManagedTestVUsers {
				return fmt.Errorf("group[%d].stages[%d].target_vusers must be between 0 and %d", index, stageIndex, maxManagedTestVUsers)
			}
		}
		reserved := group.VUsers
		if group.stageDriven && group.currentVUsers > reserved {
			reserved = group.currentVUsers
		}
		if reserved < 0 || reserved > maxManagedTestVUsers || nonNegativeSumExceedsLimit(total, reserved, maxManagedTestVUsers) {
			return fmt.Errorf("test exceeds the hard workload limit of %d VUsers", maxManagedTestVUsers)
		}
		total += reserved
	}
	if test.TotalVUsers < 0 || test.TotalVUsers > maxManagedTestVUsers {
		return fmt.Errorf("test total_vusers must be between 0 and %d", maxManagedTestVUsers)
	}
	if test.GoalConfig != nil {
		if test.GoalConfig.MinVUsers != nil && *test.GoalConfig.MinVUsers > maxManagedTestVUsers {
			return fmt.Errorf("goal min_vusers cannot exceed %d", maxManagedTestVUsers)
		}
		if test.GoalConfig.MaxVUsers != nil && *test.GoalConfig.MaxVUsers > maxManagedTestVUsers {
			return fmt.Errorf("goal max_vusers cannot exceed %d", maxManagedTestVUsers)
		}
	}
	return nil
}

func validateEngineGroupBounds(groups []engine.GroupConfig) error {
	if len(groups) == 0 {
		return fmt.Errorf("test must contain at least one engine group")
	}
	if len(groups) > maxManagedTestGroups {
		return fmt.Errorf("test contains %d engine groups; limit is %d", len(groups), maxManagedTestGroups)
	}
	total := 0
	for index, group := range groups {
		if group.VUsers < 0 || group.VUsers > maxManagedTestVUsers || nonNegativeSumExceedsLimit(total, group.VUsers, maxManagedTestVUsers) {
			return fmt.Errorf("engine group[%d] exceeds the hard workload limit of %d VUsers", index, maxManagedTestVUsers)
		}
		total += group.VUsers
	}
	return nil
}
