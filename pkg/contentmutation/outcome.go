package contentmutation

// WriteState classifies a conditional write independently of scheduling policy.
// Unknown means the caller cannot yet prove whether its transition committed.
type WriteState uint8

const (
	WriteApplied WriteState = iota
	WriteConflict
	WriteOwnershipLost
	WriteUnknown
)

type WriteResolution[T any] struct {
	State     WriteState
	Current   T
	ReadError error
}

// ResolveWrite reconciles a failed or ambiguous reply against the exact intended
// transition. A successful reply needs no extra read. The caller retains its
// domain-specific CAS, transition identity, revision restoration, and error text.
// Ownership is checked around the read so a delayed read cannot publish across
// a lost execution term. The match callback must compare the transition token as
// well as the domain identity and revision; content similarity is insufficient.
func ResolveWrite[T any](changed bool, writeErr error, owns func() bool, read func() (T, error), matches func(T) bool) WriteResolution[T] {
	if changed && writeErr == nil {
		return WriteResolution[T]{State: WriteApplied}
	}
	if !owns() {
		return WriteResolution[T]{State: WriteOwnershipLost}
	}
	current, readErr := read()
	result := WriteResolution[T]{Current: current, ReadError: readErr}
	switch {
	case !owns():
		result.State = WriteOwnershipLost
	case readErr == nil && matches(current):
		result.State = WriteApplied
	case writeErr != nil || readErr != nil:
		result.State = WriteUnknown
	default:
		result.State = WriteConflict
	}
	return result
}
