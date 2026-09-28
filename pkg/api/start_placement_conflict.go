package api

import "fmt"

// StartPlacementConflictError preserves the preflight conflict details when
// placement is revalidated at the start boundary. Capacity overrides cannot
// make an ineligible generator compatible with a run.
type StartPlacementConflictError struct {
	Conflicts []LaunchConflict
}

func (e *StartPlacementConflictError) Error() string {
	if e == nil || len(e.Conflicts) == 0 {
		return "generator placement conflict"
	}
	return fmt.Sprintf("%s: %s", e.Conflicts[0].Code, e.Conflicts[0].Message)
}

// shouldCheckStartPlacement preserves reservation-mode queuing. Legacy placement
// can reject known shortages at admission; a cold inventory is revalidated by
// the execution backend's fresh discovery before work is assigned.
func (tr *TestRunner) shouldCheckStartPlacement(test *ManagedTest) bool {
	if groupsUseJavaVuserProtocol(test.Groups) {
		return true
	}
	if tr.settings.CapacityPlanning.PodReservationsEnabled {
		return false
	}
	inventory, _ := tr.cachedGeneratorInventorySnapshot()
	return len(inventory) > 0
}
