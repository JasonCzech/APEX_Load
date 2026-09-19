package protocol

// VerificationController keeps source-registered checks on the VUser's protocol
// instance. It is deliberately separate from static, script-wide content checks.
type VerificationController interface{ ConfigureVerification(Verification) error }
type Verification struct {
	Operation       string `json:"operation"`
	ID              string `json:"id"`
	Text            string `json:"text,omitempty"`
	Scope           string `json:"scope,omitempty"`
	FailIfFound     bool   `json:"fail_if_found"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

// VerificationSnapshotter transfers the parent's registrations and pause state to a parallel
// branch protocol without sharing mutable registration state across branches.
type VerificationSnapshotter interface{ VerificationSnapshot() []Verification }
