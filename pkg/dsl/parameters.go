package dsl

import "strings"

// CanonicalParameterType maps supported aliases and LoadRunner parameter spellings
// onto the APEX DSL parameter type names used by validators and runtimes.
func CanonicalParameterType(paramType string) string {
	trimmed := strings.TrimSpace(paramType)
	normalized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return -1
	}, trimmed)

	switch normalized {
	case "file":
		return "file"
	case "list":
		return "list"
	case "randomnumber":
		return "random_number"
	case "datetime":
		return "date_time"
	case "timestamp":
		return "timestamp"
	case "counter":
		return "counter"
	case "currentiteration", "currentiterationnumber":
		return "current_iteration"
	case "userid", "vuserid", "vuser", "vusernum", "vusernumber":
		return "vuser_id"
	case "host", "hostname", "loadgenerator", "loadgeneratorname":
		return "host"
	case "time":
		return "time"
	case "uuid":
		return "uuid"
	case "randomstring":
		return "random_string"
	default:
		return trimmed
	}
}
