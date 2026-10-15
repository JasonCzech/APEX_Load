package dsl

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/protocol"
	"regexp"
	"strings"
)

var propertyCategory = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func ValidatePropertyAnalytics(items []PropertyAnalytics) []string {
	var issues []string
	if len(items) > 4 {
		return []string{"property_analytics allows at most four promoted extractions"}
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !propertyCategory.MatchString(item.Name) || seen[item.Name] || credential.SensitiveName(item.Name) || credential.SensitiveName(item.Extraction) || !propertyCategory.MatchString(item.Extraction) {
			issues = append(issues, "property_analytics requires unique non-sensitive names and extraction keys")
		}
		seen[item.Name] = true
		if item.Name == protocol.BrowserIterationDimension {
			issues = append(issues, fmt.Sprintf("property_analytics name %q is reserved: browser iterations are tagged cold or warm under it", item.Name))
		}
		lower := strings.ToLower(item.Extraction)
		for _, sensitive := range []string{"email", "phone", "address", "user", "customer", "account", "session"} {
			if strings.Contains(lower, sensitive) {
				issues = append(issues, "property_analytics cannot promote personal identifiers")
			}
		}
		if len(item.Values) < 1 || len(item.Values) > 16 {
			issues = append(issues, "property_analytics requires 1-16 approved categories")
		}
		values := map[string]bool{}
		for _, value := range item.Values {
			if !propertyCategory.MatchString(value) || value == "other" || value == "missing" || values[value] {
				issues = append(issues, fmt.Sprintf("property_analytics %s has invalid or duplicate categories", item.Name))
			}
			values[value] = true
		}
	}
	return issues
}
