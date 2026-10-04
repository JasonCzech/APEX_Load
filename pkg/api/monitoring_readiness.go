package api

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const (
	oracleDBMetricPrefix = "oracle.db."
)

type MonitoringMetricSpec struct {
	CanonicalName    string   `json:"canonical_name"`
	PrometheusMetric string   `json:"prometheus_metric"`
	Unit             string   `json:"unit,omitempty"`
	Required         bool     `json:"required"`
	Description      string   `json:"description,omitempty"`
	ExpectedLabels   []string `json:"expected_labels,omitempty"`
}

type MonitoringExternalMetricExample struct {
	Name       string            `json:"name"`
	URL        string            `json:"url"`
	Format     string            `json:"format"`
	MetricName string            `json:"metric_name"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type MonitoringReadinessProfile struct {
	ID                     string                            `json:"id"`
	Name                   string                            `json:"name"`
	Provider               string                            `json:"provider,omitempty"`
	Status                 string                            `json:"status"`
	ResourceType           string                            `json:"resource_type"`
	Summary                string                            `json:"summary,omitempty"`
	RequiredMetrics        []string                          `json:"required_metrics,omitempty"`
	MissingMetrics         []string                          `json:"missing_metrics,omitempty"`
	MetricContract         []MonitoringMetricSpec            `json:"metric_contract,omitempty"`
	ExpectedLabels         []string                          `json:"expected_labels,omitempty"`
	NamingConventions      []string                          `json:"naming_conventions,omitempty"`
	SuggestedDashboards    []string                          `json:"suggested_dashboards,omitempty"`
	ExampleExternalMetrics []MonitoringExternalMetricExample `json:"example_external_metrics,omitempty"`
}

type RunArchiveOracleDBEvidence struct {
	Status              string                                   `json:"status"`
	Samples             int64                                    `json:"samples"`
	RequiredMetrics     []string                                 `json:"required_metrics,omitempty"`
	MissingMetrics      []string                                 `json:"missing_metrics,omitempty"`
	MetricStats         map[string]RunArchiveExternalMetricStats `json:"metric_stats,omitempty"`
	Correlations        []RunArchiveExternalMetricCorrelation    `json:"correlations,omitempty"`
	SuggestedDashboards []string                                 `json:"suggested_dashboards,omitempty"`
}

type RunArchiveExternalMetricStats struct {
	Samples   int64      `json:"samples"`
	Peak      float64    `json:"peak"`
	Average   float64    `json:"average"`
	FirstSeen *time.Time `json:"first_seen,omitempty"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

type RunArchiveExternalMetricCorrelation struct {
	Metric      string  `json:"metric"`
	With        string  `json:"with"`
	Coefficient float64 `json:"coefficient"`
	Samples     int64   `json:"samples"`
	Strength    string  `json:"strength"`
}

type oracleMetricAccumulator struct {
	samples   int64
	sum       float64
	peak      float64
	firstSeen *time.Time
	lastSeen  *time.Time
}

func oracleDBMetricContract() []MonitoringMetricSpec {
	labels := oracleDBExpectedLabels()
	return []MonitoringMetricSpec{
		{
			CanonicalName:    "oracle.db.cpu_pct",
			PrometheusMetric: "oracle_db_cpu_pct",
			Unit:             "percent",
			Required:         true,
			Description:      "Oracle database CPU utilization percentage for the target instance or service.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "oracle.db.session_active",
			PrometheusMetric: "oracle_db_session_active",
			Unit:             "sessions",
			Required:         true,
			Description:      "Active foreground sessions observed for the target database service.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "oracle.db.wait_time_ms",
			PrometheusMetric: "oracle_db_wait_time_ms",
			Unit:             "milliseconds",
			Required:         true,
			Description:      "Total or representative database wait time for the target service.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "oracle.db.lock_waits",
			PrometheusMetric: "oracle_db_lock_waits",
			Unit:             "waits",
			Required:         true,
			Description:      "Lock wait count or active lock-wait sessions for the target service.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "oracle.db.parse_rate",
			PrometheusMetric: "oracle_db_parse_rate",
			Unit:             "parses_per_second",
			Required:         true,
			Description:      "SQL parse rate used to correlate hard-parse pressure with response-time changes.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "oracle.db.physical_reads",
			PrometheusMetric: "oracle_db_physical_reads",
			Unit:             "reads_per_second",
			Required:         true,
			Description:      "Physical read activity for storage pressure correlation.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "oracle.db.redo_mb_s",
			PrometheusMetric: "oracle_db_redo_mb_s",
			Unit:             "megabytes_per_second",
			Required:         true,
			Description:      "Redo generation throughput for write-path saturation correlation.",
			ExpectedLabels:   labels,
		},
	}
}

func oracleDBMonitoringProfile() MonitoringReadinessProfile {
	required := oracleDBCanonicalMetricNames(true)
	return MonitoringReadinessProfile{
		ID:              "oracle-database",
		Name:            "Oracle Database",
		Provider:        "oracle",
		Status:          "awaiting_metric_source",
		ResourceType:    "database",
		Summary:         "Canonical oracle.db.* metric contract for offline archive/report readiness before live Oracle, Prometheus, or Dynatrace connections are configured.",
		RequiredMetrics: required,
		MissingMetrics:  append([]string(nil), required...),
		MetricContract:  oracleDBMetricContract(),
		ExpectedLabels:  oracleDBExpectedLabels(),
		NamingConventions: []string{
			"Use oracle.db.* canonical names inside APEX Load archives and reports.",
			"Keep Prometheus labels bounded to database identity and service dimensions.",
			"Do not place SQL text, user names, session IDs, or trace IDs in Prometheus labels.",
		},
		SuggestedDashboards:    oracleDBSuggestedDashboards(),
		ExampleExternalMetrics: oracleDBExternalMetricExamples("http://127.0.0.1:9100/metrics"),
	}
}

func oracleDBExpectedLabels() []string {
	return []string{"db_name", "service_name", "instance", "tablespace"}
}

func oracleDBSuggestedDashboards() []string {
	return []string{
		"Oracle DB Waits vs APEX P95 Latency",
		"Oracle Sessions, Locks, and Error Rate",
		"Oracle IO, Redo, and Throughput Saturation",
	}
}

func oracleDBExternalMetricExamples(url string) []MonitoringExternalMetricExample {
	labels := map[string]string{"service_name": "checkout"}
	examples := make([]MonitoringExternalMetricExample, 0, len(oracleDBMetricContract()))
	for _, metric := range oracleDBMetricContract() {
		examples = append(examples, MonitoringExternalMetricExample{
			Name:       metric.CanonicalName,
			URL:        url,
			Format:     "prometheus",
			MetricName: metric.PrometheusMetric,
			Labels:     cloneStringMap(labels),
		})
	}
	return examples
}

func oracleDBCanonicalMetricNames(requiredOnly bool) []string {
	metrics := oracleDBMetricContract()
	names := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		if requiredOnly && !metric.Required {
			continue
		}
		names = append(names, metric.CanonicalName)
	}
	sort.Strings(names)
	return names
}

func isOracleDBMetricName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), oracleDBMetricPrefix)
}

func countOracleDBMetricSamples(resources []ResourceSample) int64 {
	var count int64
	for _, sample := range resources {
		if sampleHasOracleDBMetric(sample) {
			count++
		}
	}
	return count
}

func sampleHasOracleDBMetric(sample ResourceSample) bool {
	for name := range sample.External {
		if isOracleDBMetricName(name) {
			return true
		}
	}
	return false
}

func buildRunArchiveOracleDBEvidence(resources []ResourceSample, summaries []IntervalSummaryRecord) *RunArchiveOracleDBEvidence {
	accumulators := make(map[string]*oracleMetricAccumulator)
	var samples int64
	for _, sample := range resources {
		sampleHasMetric := false
		for name, value := range sample.External {
			if !isOracleDBMetricName(name) {
				continue
			}
			sampleHasMetric = true
			acc := accumulators[name]
			if acc == nil {
				acc = &oracleMetricAccumulator{peak: value}
				accumulators[name] = acc
			}
			acc.samples++
			acc.sum += value
			if acc.samples == 1 || value > acc.peak {
				acc.peak = value
			}
			timestamp := sample.Timestamp
			if acc.firstSeen == nil || timestamp.Before(*acc.firstSeen) {
				acc.firstSeen = &timestamp
			}
			if acc.lastSeen == nil || timestamp.After(*acc.lastSeen) {
				acc.lastSeen = &timestamp
			}
		}
		if sampleHasMetric {
			samples++
		}
	}
	if len(accumulators) == 0 {
		return nil
	}
	stats := make(map[string]RunArchiveExternalMetricStats, len(accumulators))
	present := make(map[string]struct{}, len(accumulators))
	for name, acc := range accumulators {
		present[name] = struct{}{}
		avg := 0.0
		if acc.samples > 0 {
			avg = acc.sum / float64(acc.samples)
		}
		stats[name] = RunArchiveExternalMetricStats{
			Samples:   acc.samples,
			Peak:      acc.peak,
			Average:   avg,
			FirstSeen: acc.firstSeen,
			LastSeen:  acc.lastSeen,
		}
	}
	required := oracleDBCanonicalMetricNames(true)
	missing := make([]string, 0)
	for _, name := range required {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	status := "ready"
	if len(missing) > 0 {
		status = "partial"
	}
	return &RunArchiveOracleDBEvidence{
		Status:              status,
		Samples:             samples,
		RequiredMetrics:     required,
		MissingMetrics:      missing,
		MetricStats:         stats,
		Correlations:        buildOracleDBMetricCorrelations(resources, summaries, sortedOracleMetricNames(accumulators)),
		SuggestedDashboards: oracleDBSuggestedDashboards(),
	}
}

func sortedOracleMetricNames(values map[string]*oracleMetricAccumulator) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Oracle metric correlation pairs each sample only with the interval whose
// window contains it, and scores a metric only with enough paired intervals
// covering enough of the metric's observed span. Association is not causation.
const (
	oracleCorrelationMinPairs    = 10
	oracleCorrelationMinCoverage = 0.5
)

type oracleP95Window struct {
	start time.Time // exclusive
	end   time.Time // inclusive; the interval summary timestamp
	p95   float64
}

func buildOracleDBMetricCorrelations(resources []ResourceSample, summaries []IntervalSummaryRecord, metricNames []string) []RunArchiveExternalMetricCorrelation {
	windows := oracleP95Windows(summaries)
	if len(resources) == 0 || len(windows) < oracleCorrelationMinPairs || len(metricNames) == 0 {
		return nil
	}
	correlations := make([]RunArchiveExternalMetricCorrelation, 0)
	for _, metricName := range metricNames {
		pairs, expected := oracleMetricWindowPairs(resources, windows, metricName)
		if len(pairs) < oracleCorrelationMinPairs || expected == 0 || float64(len(pairs))/float64(expected) < oracleCorrelationMinCoverage {
			continue
		}
		coefficient, ok := analysis.Pearson(pairs)
		if !ok || math.Abs(coefficient) < 0.5 {
			continue
		}
		correlations = append(correlations, RunArchiveExternalMetricCorrelation{
			Metric:      metricName,
			With:        "summary_timeline.p95_ms",
			Coefficient: roundFloat(coefficient, 4),
			Samples:     int64(len(pairs)),
			Strength:    correlationStrength(coefficient),
		})
	}
	sort.SliceStable(correlations, func(left, right int) bool {
		return math.Abs(correlations[left].Coefficient) > math.Abs(correlations[right].Coefficient)
	})
	return correlations
}

// oracleP95Windows turns percentile-bearing interval summaries into disjoint
// (start, end] windows. A summary without a recorded sample window spans back
// to the previous summary; the first such summary borrows the next spacing.
func oracleP95Windows(summaries []IntervalSummaryRecord) []oracleP95Window {
	ordered := make([]IntervalSummaryRecord, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Timestamp.IsZero() || !archivePercentileAvailable(summary.PercentileQuality) || math.IsNaN(summary.P95Ms) || math.IsInf(summary.P95Ms, 0) {
			continue
		}
		ordered = append(ordered, summary)
	}
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].Timestamp.Before(ordered[right].Timestamp) })
	windows := make([]oracleP95Window, 0, len(ordered))
	for i, summary := range ordered {
		end := summary.Timestamp.UTC()
		if len(windows) > 0 && !end.After(windows[len(windows)-1].end) {
			continue // duplicate timestamp: ambiguous, keep the first
		}
		span := time.Duration(summary.SampleWindowSeconds * float64(time.Second))
		if span <= 0 {
			switch {
			case len(windows) > 0:
				span = end.Sub(windows[len(windows)-1].end)
			case i+1 < len(ordered):
				span = ordered[i+1].Timestamp.UTC().Sub(end)
			}
		}
		if span <= 0 {
			continue
		}
		start := end.Add(-span)
		if len(windows) > 0 && start.Before(windows[len(windows)-1].end) {
			start = windows[len(windows)-1].end
		}
		windows = append(windows, oracleP95Window{start: start, end: end, p95: summary.P95Ms})
	}
	return windows
}

// oracleMetricWindowPairs averages one metric's samples inside each window and
// pairs that mean with the window's P95. expected counts the windows inside the
// metric's observed span, so sparse coverage cannot pass as a full series.
func oracleMetricWindowPairs(resources []ResourceSample, windows []oracleP95Window, metricName string) ([]analysis.AlignedPair, int) {
	sums := make([]float64, len(windows))
	counts := make([]int, len(windows))
	var first, last time.Time
	for _, sample := range resources {
		value, ok := sample.External[metricName]
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		at := sample.Timestamp.UTC()
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if last.IsZero() || at.After(last) {
			last = at
		}
		index := sort.Search(len(windows), func(i int) bool { return !windows[i].end.Before(at) })
		if index == len(windows) || !at.After(windows[index].start) {
			continue
		}
		sums[index] += value
		counts[index]++
	}
	pairs := make([]analysis.AlignedPair, 0)
	expected := 0
	for i, window := range windows {
		if first.IsZero() || window.end.Before(first) || !window.start.Before(last) {
			continue
		}
		expected++
		if counts[i] == 0 {
			continue
		}
		mean := sums[i] / float64(counts[i])
		p95 := window.p95
		point := func(name string, value *float64) analysis.MetricPoint {
			return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "run-archive", Source: "archive", MetricName: name, TimestampUTC: window.end, Value: value, Kind: analysis.KindGauge, Quality: analysis.QualityValid}
		}
		pairs = append(pairs, analysis.AlignedPair{Left: point(metricName, &mean), Right: point("summary_timeline.p95_ms", &p95)})
	}
	if len(pairs) > analysis.MaxSeriesPoints {
		// Deterministic stride keeps very long runs within the analysis bound.
		stride := (len(pairs) + analysis.MaxSeriesPoints - 1) / analysis.MaxSeriesPoints
		kept := pairs[:0]
		for i := 0; i < len(pairs); i += stride {
			kept = append(kept, pairs[i])
		}
		pairs = kept
	}
	return pairs, expected
}

func correlationStrength(coefficient float64) string {
	abs := math.Abs(coefficient)
	switch {
	case abs >= 0.8:
		return "strong"
	case abs >= 0.65:
		return "moderate"
	default:
		return "weak"
	}
}

func oracleDBRootCauseFinding(evidence *RunArchiveOracleDBEvidence) (severity string, title string, detail string, score float64, ok bool) {
	if evidence == nil || evidence.Samples == 0 {
		return "", "", "", 0, false
	}
	metricPeak := func(name string) float64 {
		if evidence.MetricStats == nil {
			return 0
		}
		return evidence.MetricStats[name].Peak
	}
	severity = "warning"
	title = "Oracle database telemetry needs review"
	score = 55
	details := make([]string, 0)
	if wait := metricPeak("oracle.db.wait_time_ms"); wait >= 100 {
		details = append(details, "wait time peaked at "+formatOneDecimal(wait)+" ms")
		score = math.Max(score, math.Min(95, 55+wait/20))
		if wait >= 500 {
			severity = "critical"
		}
	}
	if locks := metricPeak("oracle.db.lock_waits"); locks > 0 {
		details = append(details, "lock waits peaked at "+formatOneDecimal(locks))
		score = math.Max(score, math.Min(90, 60+locks*2))
		if locks >= 10 {
			severity = "critical"
		}
	}
	if cpu := metricPeak("oracle.db.cpu_pct"); cpu >= 80 {
		details = append(details, "database CPU peaked at "+formatOneDecimal(cpu)+"%")
		score = math.Max(score, cpu)
		if cpu >= 90 {
			severity = "critical"
		}
	}
	for _, correlation := range evidence.Correlations {
		if correlation.Strength == "strong" {
			details = append(details, correlation.Metric+" strongly correlated with P95 latency")
			score = math.Max(score, 75)
			break
		}
	}
	if len(details) == 0 {
		return "", "", "", 0, false
	}
	return severity, title, strings.Join(details, "; ") + ".", roundFloat(score, 1), true
}

func oracleDBSignalSeverity(evidence *RunArchiveOracleDBEvidence) string {
	if evidence == nil || evidence.Samples == 0 {
		return "healthy"
	}
	severity, _, _, _, ok := oracleDBRootCauseFinding(evidence)
	if ok {
		return severity
	}
	if len(evidence.MissingMetrics) > 0 {
		return "warning"
	}
	return "healthy"
}

func roundFloat(value float64, places int) float64 {
	if places < 0 {
		return value
	}
	factor := math.Pow(10, float64(places))
	return math.Round(value*factor) / factor
}

func formatOneDecimal(value float64) string {
	return fmt.Sprintf("%.1f", value)
}
