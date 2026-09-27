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
