package engine

import "strings"

// Error taxonomy classes. Kept in one place so the engine (single-pod) and the
// controller (distributed) classify identically and the dashboard can rely on a
// stable key set.
const (
	ErrorClassTimeout    = "timeout"
	ErrorClassConnection = "connection"
	ErrorClassHTTP4xx    = "http_4xx"
	ErrorClassHTTP5xx    = "http_5xx"
	ErrorClassAssertion  = "assertion"
	// ErrorClassResourceLimit is a session the generator refused or closed
	// to protect itself (browser memory or evidence budgets), not a target
	// failure.
	ErrorClassResourceLimit = "resource_limit"
	// ErrorClassSessionLost is a browser observer sent to sign in, or whose
	// Aura session was rejected, during an iteration.
	ErrorClassSessionLost = "session_lost"
	ErrorClassOther       = "other"
)

// ClassifyError buckets a failed result into a taxonomy class for the error
// breakdown graph. A successful result returns "" (not an error). Apart from a
// lost observer session, which the engine names itself, HTTP status codes are
// authoritative when present; otherwise the error message is inspected
// for transport-level (timeout/connection) and validation (assertion) signatures.
func ClassifyError(statusCode int, errorMessage string, success bool) string {
	if success {
		return ""
	}
	msg := strings.ToLower(errorMessage)
	// The engine names a lost observer session itself, whatever status the
	// sign-in page's document carried.
	if strings.HasPrefix(msg, sessionLostPrefix) {
		return ErrorClassSessionLost
	}
	switch {
	case statusCode >= 500 && statusCode <= 599:
		return ErrorClassHTTP5xx
	case statusCode >= 400 && statusCode <= 499:
		return ErrorClassHTTP4xx
	}
	switch {
	case strings.HasPrefix(msg, "browser resource limit"):
		return ErrorClassResourceLimit
	case containsAny(msg, "timeout", "timed out", "deadline exceeded"):
		return ErrorClassTimeout
	case containsAny(msg, "connection refused", "connection reset", "no such host", "broken pipe", "connection closed", "dial ", "eof"):
		return ErrorClassConnection
	case containsAny(msg, "assert", "validation", "expected "):
		return ErrorClassAssertion
	default:
		return ErrorClassOther
	}
}
