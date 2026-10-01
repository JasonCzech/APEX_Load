package collectoragent

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
)

// Parse the bounded text 0.0.4 label grammar. Label values never become metric
// names. Reject ambiguous/secret-bearing labels rather than collapsing series.
func parsePrometheusLabels(line string) (string, map[string]string, error) {
	bad := errors.New("invalid, sensitive, or excessive Prometheus dimensions")
	i := strings.IndexByte(line, '{')
	if i < 1 {
		return "", nil, bad
	}
	name, rest := line[:i], line[i+1:]
	labels := map[string]string{}
	for {
		rest = strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(rest, "}") {
			if len(rest) < 2 || rest[1] != ' ' && rest[1] != '\t' {
				return "", nil, bad
			}
			return name + " " + strings.TrimSpace(rest[1:]), labels, nil
		}
		eq := strings.IndexByte(rest, '=')
		if eq < 1 {
			return "", nil, bad
		}
		key := strings.TrimSpace(rest[:eq])
		if len(labels) >= 16 || len(key) > 128 || !validPrometheusLabelName(key) || credential.SensitiveName(key) {
			return "", nil, bad
		}
		if _, exists := labels[key]; exists {
			return "", nil, bad
		}
		rest = strings.TrimLeft(rest[eq+1:], " \t")
		if !strings.HasPrefix(rest, "\"") {
			return "", nil, bad
		}
		end := 1
		for ; end < len(rest) && rest[end] != '"'; end++ {
			if end > 768 {
				return "", nil, bad
			}
			if rest[end] == '\\' {
				end++
				if end >= len(rest) || !strings.ContainsRune("\\\"n", rune(rest[end])) {
					return "", nil, bad
				}
			}
		}
		if end >= len(rest) {
			return "", nil, bad
		}
		value, err := strconv.Unquote(rest[:end+1])
		if err != nil || len(value) > 128 || !utf8.ValidString(value) {
			return "", nil, bad
		}
		labels[key] = value
		rest = strings.TrimLeft(rest[end+1:], " \t")
		if strings.HasPrefix(rest, ",") {
			rest = rest[1:]
		} else if !strings.HasPrefix(rest, "}") {
			return "", nil, bad
		}
	}
}

func validPrometheusLabelName(value string) bool {
	if value == "" {
		return false
	}
	for i, c := range value {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func prometheusUnit(name string) string {
	name = strings.TrimSuffix(name, "_total")
	for suffix, unit := range map[string]string{"_seconds": "s", "_milliseconds": "ms", "_bytes": "bytes", "_ratio": "ratio", "_percent": "percent"} {
		if strings.HasSuffix(name, suffix) {
			return unit
		}
	}
	return ""
}

func localMetricUnit(name string) string {
	if strings.HasSuffix(name, "_mb") {
		return "MiB"
	}
	switch name {
	case "goroutines":
		return "count"
	case "num_cpu", "load_avg_1m":
		return "cores"
	}
	return ""
}
