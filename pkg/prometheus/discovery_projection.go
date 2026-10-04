package prometheus

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/scrub"
)

// SafeDiscoveryEntityFields revalidates bounded discovery fields at the API
// trust boundary. It shares the provider's label-name and disclosure rules;
// collector-supplied URLs, secret templates and arbitrary metadata are omitted.
func SafeDiscoveryEntityFields(kind string, values map[string]string) (map[string]string, bool) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := map[string]string{}
	omitted := false
	for _, key := range keys {
		value := values[key]
		allowed := false
		limit := 128
		switch kind {
		case "series", "target":
			allowed = key != "__name__" && labelRE.MatchString(key) && !sensitiveLabel(key)
		case "metric_metadata":
			allowed = key == "unit" || key == "type"
			limit = 64
		}
		if !allowed || len(key) > 128 || len(value) > limit || !utf8.ValidString(value) || strings.Contains(value, "://") || strings.Contains(value, "{{") || strings.Contains(value, "}}") || strings.IndexFunc(value, unicode.IsControl) >= 0 || scrub.ValueLabel(value, scrub.Policy{}) != value {
			omitted = true
			continue
		}
		if len(out) >= 12 {
			omitted = true
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil, omitted
	}
	return out, omitted
}
