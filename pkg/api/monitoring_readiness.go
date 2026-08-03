package api

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
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

func buildOracleDBMetricCorrelations(resources []ResourceSample, summaries []IntervalSummaryRecord, metricNames []string) []RunArchiveExternalMetricCorrelation {
	if len(resources) == 0 || len(summaries) < 3 || len(metricNames) == 0 {
		return nil
	}
	correlations := make([]RunArchiveExternalMetricCorrelation, 0)
	for _, metricName := range metricNames {
		xs := make([]float64, 0)
		ys := make([]float64, 0)
		for _, sample := range resources {
			value, ok := sample.External[metricName]
			if !ok {
				continue
			}
			p95, ok := nearestSummaryP95(sample.Timestamp, summaries)
			if !ok {
				continue
			}
			xs = append(xs, value)
			ys = append(ys, p95)
		}
		if len(xs) < 3 {
			continue
		}
		coefficient := pearsonCorrelation(xs, ys)
		if math.IsNaN(coefficient) || math.Abs(coefficient) < 0.5 {
			continue
		}
		correlations = append(correlations, RunArchiveExternalMetricCorrelation{
			Metric:      metricName,
			With:        "summary_timeline.p95_ms",
			Coefficient: roundFloat(coefficient, 4),
			Samples:     int64(len(xs)),
			Strength:    correlationStrength(coefficient),
		})
	}
	sort.SliceStable(correlations, func(left, right int) bool {
		return math.Abs(correlations[left].Coefficient) > math.Abs(correlations[right].Coefficient)
	})
	return correlations
}

func nearestSummaryP95(timestamp time.Time, summaries []IntervalSummaryRecord) (float64, bool) {
	if len(summaries) == 0 {
		return 0, false
	}
	bestIndex := -1
	var bestDistance time.Duration
	for index, summary := range summaries {
		if !archivePercentileAvailable(summary.PercentileQuality) {
			continue
		}
		distance := timestamp.Sub(summary.Timestamp)
		if distance < 0 {
			distance = -distance
		}
		if bestIndex == -1 || distance < bestDistance {
			bestIndex = index
			bestDistance = distance
		}
	}
	if bestIndex < 0 {
		return 0, false
	}
	return summaries[bestIndex].P95Ms, true
}

func pearsonCorrelation(xs []float64, ys []float64) float64 {
	if len(xs) != len(ys) || len(xs) < 2 {
		return math.NaN()
	}
	var sumX, sumY float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX := sumX / float64(len(xs))
	meanY := sumY / float64(len(ys))
	var numerator, denomX, denomY float64
	for i := range xs {
		dx := xs[i] - meanX
		dy := ys[i] - meanY
		numerator += dx * dy
		denomX += dx * dx
		denomY += dy * dy
	}
	denominator := math.Sqrt(denomX * denomY)
	if denominator == 0 {
		return math.NaN()
	}
	return numerator / denominator
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
