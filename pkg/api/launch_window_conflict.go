package api

// LaunchWindowConflictError retains the authoritative conflicts found when a
// run's launch window is revalidated. Unavailable evidence uses the separate
// retryable admission error, rather than this rejection.
type LaunchWindowConflictError struct {
	Conflicts []LaunchConflict
}

func (e *LaunchWindowConflictError) Error() string {
	if e == nil || len(e.Conflicts) == 0 {
		return "launch window conflict"
	}
	return "launch window conflict: " + e.Conflicts[0].Message
}
