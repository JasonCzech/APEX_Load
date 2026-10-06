package analysis

import (
	"regexp"

	"github.com/jasonczech/apexload/pkg/scrub"
)

var providerAuthorization = regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*(?:bearer|basic|api\s*key|apikey)\s+)[^\s,;"']+`)
var providerEmail = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+`)

// Monitoring projections include prose rather than only structured headers.
// Apply the same protections at preview, spool and disclosure boundaries.
func ScrubProviderText(value string) string {
	return scrubProviderProse(scrub.Value(scrubProviderProse(value), scrub.Policy{}))
}

// Stable trace/document identifiers retain their spelling while structured
// credentials and recognizable personal addresses are still removed.
func ScrubProviderLabel(value string) string {
	return scrubProviderProse(scrub.ValueLabel(scrubProviderProse(value), scrub.Policy{}))
}

func scrubProviderProse(value string) string {
	value = providerAuthorization.ReplaceAllString(value, `${1}`+scrub.Redacted)
	return providerEmail.ReplaceAllString(value, scrub.Redacted)
}
