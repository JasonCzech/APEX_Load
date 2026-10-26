package scrub

import "regexp"

var bodyDoubleAssignment = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|token|access_token|refresh_token|api_key|apikey|client_secret)\b["']?\s*[:=]\s*")((?:\\.|[^"\\])*)(")`)
var bodySingleAssignment = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|token|access_token|refresh_token|api_key|apikey|client_secret)\b["']?\s*[:=]\s*')((?:\\.|[^'\\])*)(')`)

func redactBodyAssignments(value string) string {
	value = bodyDoubleAssignment.ReplaceAllString(value, `${1}[REDACTED]${3}`)
	return bodySingleAssignment.ReplaceAllString(value, `${1}[REDACTED]${3}`)
}

// BodyText applies credential detection to retained text, including quoted
// credentials in inline JavaScript. Exact runtime values remain the caller's
// responsibility, as with ValueLabel.
func BodyText(value string) string { return ValueLabel(redactBodyAssignments(value), Policy{}) }
