package appdynamics

import (
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// AnalyticsQueryWindow distinguishes an aggregate over the entire bounded
// query from observations projected into independent timestamped buckets.
func AnalyticsQueryWindow(q AnalyticsQuery) bool {
	return !analyticsProjectionField(q.Query, "bucket") && !analyticsProjectionField(q.Query, "eventTimestamp") && !analyticsProjectionField(q.Query, "timestamp")
}

// AnalyticsAggregationEligible derives eligibility from the administrator-owned
// definition as well as returned weights. Collectors cannot make percentile
// buckets or averages without declared sum/count projections aggregatable.
func AnalyticsAggregationEligible(q AnalyticsQuery, quality string, count, sum *float64) bool {
	if quality != analysis.QualityValid {
		return false
	}
	projection := analyticsProjection(q.Query)
	if projection == "" || analyticsProjectionFunction(projection, "percentile") || analyticsProjectionFunction(projection, "stdev") {
		return false
	}
	statistic := q.Statistic
	if statistic == "" {
		statistic = "value"
	}
	if analyticsProjectionFunction(projection, "avg") && statistic != "average" {
		return false
	}
	if statistic == "average" {
		return finite(count) && *count > 0 && finite(sum) && analyticsProjectionField(q.Query, "count") && analyticsProjectionField(q.Query, "sum")
	}
	return statistic == "value" || statistic == "sum"
}

func analyticsProjectionFunction(projection, name string) bool {
	pattern := `(?i)(?:\b` + regexp.QuoteMeta(name) + `|` + "`" + regexp.QuoteMeta(name) + "`" + `)\s*\(`
	return regexp.MustCompile(pattern).MatchString(projection)
}

func analyticsProjectionField(query, field string) bool {
	projection := analyticsProjection(query)
	if projection == "" {
		return false
	}
	identifier := "`?" + regexp.QuoteMeta(field) + "`?"
	pattern := `(?i)(?:\bAS\s+` + identifier + `\s*(?:,|$)|(?:^|,)\s*(?:[A-Za-z_][A-Za-z0-9_]*\.)*` + identifier + `\s*(?:,|$))`
	return regexp.MustCompile(pattern).MatchString(projection)
}

func analyticsProjection(query string) string {
	query = strings.TrimSpace(query)
	if !validAnalyticsQuery(query) {
		return ""
	}
	words, err := analyticsTopLevelWords(query)
	if err != nil {
		return ""
	}
	for _, word := range words {
		if word.value == "FROM" {
			if word.at <= len("SELECT") {
				return ""
			}
			projection := strings.TrimSpace(query[len("SELECT"):word.at])
			// String constants do not declare fields or aggregate functions.
			var quote byte
			var out strings.Builder
			for i := 0; i < len(projection); i++ {
				ch := projection[i]
				if quote != 0 {
					if ch == '\\' {
						i++
					} else if ch == quote {
						if i+1 < len(projection) && projection[i+1] == quote {
							i++
						} else {
							quote = 0
						}
					}
					out.WriteByte(' ')
				} else if ch == '\'' || ch == '"' {
					quote = ch
					out.WriteByte(' ')
				} else {
					out.WriteByte(ch)
				}
			}
			return out.String()
		}
	}
	return ""
}
