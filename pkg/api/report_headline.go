package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// reportHeadlineMetrics is the metric population that every human-readable
// report headline (HTML/PDF KPIs, summary strips, generated narratives) uses.
//
// It mirrors the dashboard's defaultReportPopulation/reportPopulationOverview
// (dashboard/src/components/runReport/reportPresentation.ts), so a number in an
// exported headline equals the on-screen KPI scorecard. The archive Overview
// mixes actions with their enclosing named transactions and takes its peak
// from successful transactions only, so headlines must not read it directly.
type reportHeadlineMetrics struct {
	Population string
	// Label names the population and window beside each headline number,
	// e.g. "Requests / actions · whole run".
	Label string
	RunArchiveActionStats
}

func reportHeadline(report *RunArchiveReport) reportHeadlineMetrics {
	if report == nil {
		return reportHeadlineMetrics{Label: "No archive records"}
	}
	if report.MetricPopulations != nil {
		if sel := strings.TrimSpace(report.SelectedPopulation); sel == "action" || sel == "transaction" || sel == "unknown" {
			if metrics, ok := report.MetricPopulations[sel]; ok && metrics.Transactions > 0 {
				return reportHeadlineMetrics{
					Population:            sel,
					Label:                 reportPopulationLabel(sel) + " · whole run",
					RunArchiveActionStats: metrics.RunArchiveActionStats,
				}
			}
		}
		for _, key := range []string{"action", "transaction", "unknown"} {
			if metrics, ok := report.MetricPopulations[key]; ok && metrics.Transactions > 0 {
				return reportHeadlineMetrics{
					Population:            key,
					Label:                 reportPopulationLabel(key) + " · whole run",
					RunArchiveActionStats: metrics.RunArchiveActionStats,
				}
			}
		}
	}
	// Archives written before populations existed only have the mixed overview.
	ov := report.Overview
	return reportHeadlineMetrics{
		Label: "Archive records (unclassified) · whole run",
		RunArchiveActionStats: RunArchiveActionStats{
			Transactions:  ov.Transactions,
			Errors:        ov.Errors,
			ErrorPct:      ov.ErrorPct,
			AvgResponseMs: ov.AvgResponseMs,
			MinResponseMs: ov.MinResponseMs,
			MaxResponseMs: ov.MaxResponseMs,
			PeakTPS:       ov.PeakTPS,
			P50Ms:         ov.P50Ms,
			P90Ms:         ov.P90Ms,
			P95Ms:         ov.P95Ms,
			P99Ms:         ov.P99Ms,
		},
	}
}

func reportPopulationActions(report *RunArchiveReport) (map[string]RunArchiveActionStats, float64) {
	if report == nil {
		return nil, 0
	}
	if report.MetricPopulations == nil {
		return report.ByAction, report.Overview.AvgResponseMs
	}
	pop := strings.TrimSpace(report.SelectedPopulation)
	if pop == "" {
		pop = reportHeadline(report).Population
	}
	switch pop {
	case "transaction":
		if len(report.ByTransaction) > 0 || report.MetricPopulations["transaction"].Transactions > 0 {
			return report.ByTransaction, report.MetricPopulations["transaction"].AvgResponseMs
		}
	case "unknown":
		if len(report.ByUnknown) > 0 || report.MetricPopulations["unknown"].Transactions > 0 {
			return report.ByUnknown, report.MetricPopulations["unknown"].AvgResponseMs
		}
	}
	return report.ByRequest, report.MetricPopulations["action"].AvgResponseMs
}

func parseOptionalReportPopulation(w http.ResponseWriter, r *http.Request) (string, bool) {
	pop := strings.TrimSpace(r.URL.Query().Get("population"))
	if pop == "" {
		return "", true
	}
	switch pop {
	case "action", "transaction", "unknown":
		return pop, true
	default:
		writeError(w, http.StatusBadRequest, "invalid population")
		return "", false
	}
}

// formatReportMs renders a latency the way the dashboard's formatMs does:
// one decimal below 100 ms, whole milliseconds with thousands separators above.
func formatReportMs(value float64) string {
	if value < 100 {
		return strconv.FormatFloat(value, 'f', 1, 64) + " ms"
	}
	return groupThousands(fmt.Sprintf("%.0f", value)) + " ms"
}

func groupThousands(digits string) string {
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if len(digits) <= 3 {
		return sign + digits
	}
	var out strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		out.WriteString(digits[:lead])
	}
	for index := lead; index < len(digits); index += 3 {
		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.WriteString(digits[index : index+3])
	}
	return sign + out.String()
}
