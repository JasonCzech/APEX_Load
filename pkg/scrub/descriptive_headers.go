package scrub

import (
	"regexp"
	"strings"
)

var userAgentProductToken = regexp.MustCompile(`^[A-Za-z][A-Za-z-]{0,127}/[0-9]+$`)
var rumCorrelationValue = regexp.MustCompile(`^[0-9]{1,10}\$[0-9]{1,20}_[A-Za-z0-9]{1,128}(?:-[0-9]{1,10}e[0-9]{1,10})?$`)

// ValueDescriptiveHeader preserves recognized product/version and correlation
// syntax without exempting an arbitrary value merely because of its header
// name. Callers must still apply sensitive-header policy and known credential
// matching: even structurally ordinary identifiers may equal a known secret.
func ValueDescriptiveHeader(name, value string, policy Policy) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "user-agent":
		value = redactJWTs(ValueLabel(value, policy))
		return highEntropyToken.ReplaceAllStringFunc(value, func(candidate string) string {
			if userAgentProductToken.MatchString(candidate) ||
				(strings.HasPrefix(candidate, "uid_") && canonicalUUID.MatchString(strings.TrimPrefix(candidate, "uid_"))) {
				return candidate
			}
			return redactBareTokens(candidate)
		})
	case "x-dtpc":
		if rumCorrelationValue.MatchString(value) {
			return ValueLabel(value, policy)
		}
	}
	return Value(value, policy)
}
