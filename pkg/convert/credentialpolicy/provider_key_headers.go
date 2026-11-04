package credentialpolicy

import "strings"

// ProviderKeyHeaderReference returns the value an imported request carries
// for a model-provider credential header (Authorization, api-key, x-api-key,
// ocp-apim-subscription-key): the captured credential is replaced by ref, a
// governed secret reference, so no captured key survives the import. A value
// that is already a template is kept. changed is false for any other header
// and for a kept value.
func ProviderKeyHeaderReference(name, value, ref string) (replacement string, changed bool) {
	if strings.Contains(strings.TrimSpace(value), "{{") {
		return value, false
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization":
		return "Bearer " + ref, true
	case "api-key", "x-api-key", "ocp-apim-subscription-key":
		return ref, true
	default:
		return value, false
	}
}
