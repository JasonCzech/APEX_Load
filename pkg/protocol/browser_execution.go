package protocol

// BrowserActionExecution distinguishes engine retry attempts from a new logical
// invocation of the same authored step. Invocation is scoped by source generation.
// Values are engine-owned, never imported from action JSON.
type BrowserActionExecution struct {
	Invocation uint64 `json:"invocation"`
	Attempt    uint32 `json:"attempt"`
}

func (e *BrowserActionExecution) Valid() bool {
	return e != nil && e.Invocation > 0 && e.Invocation <= 1<<53-1 && e.Attempt > 0 && e.Attempt <= 11
}
func (e *BrowserActionExecution) Clone() *BrowserActionExecution {
	if !e.Valid() {
		return nil
	}
	out := *e
	return &out
}
