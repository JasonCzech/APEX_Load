package perfevidence

import (
	"strings"
	"testing"
	"time"
)

func TestReportSchemaRoundTripAndValidation(t *testing.T) {
	report := validTestReport(t)
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport: %v", err)
	}
	decoded, err := DecodeReport(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatalf("DecodeReport: %v", err)
	}
	if decoded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", decoded.SchemaVersion, SchemaVersion)
	}
	if len(decoded.Measurements) != len(requiredMeasurementIDs) {
		t.Fatalf("measurement count = %d, want %d", len(decoded.Measurements), len(requiredMeasurementIDs))
	}
	if !decoded.Validation.ReleaseEvidenceReady {
		t.Fatalf("release evidence ready = false, warnings=%v", decoded.Validation.Warnings)
	}
}

func TestValidateReportRejectsSchemaDriftAndTargetClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Report)
		want   string
	}{
		{
			name: "unknown schema",
			mutate: func(report *Report) {
				report.SchemaVersion = "apexload.performance-evidence/v2"
			},
			want: "schema_version",
		},
		{
			name: "top-level claim",
			mutate: func(report *Report) {
				report.ClaimPolicy.TargetAttainmentClaimed = true
			},
			want: "must not claim target attainment",
		},
		{
			name: "measurement claim",
			mutate: func(report *Report) {
				report.Measurements[0].TargetComparison.AttainmentStatus = "met"
			},
			want: "attainment_status must be not_evaluated",
		},
		{
			name: "missing target probe",
			mutate: func(report *Report) {
				report.Measurements = report.Measurements[:3]
			},
			want: "required measurement",
		},
		{
			name: "dropped hot-path samples",
			mutate: func(report *Report) {
				report.Measurements[0].SupportingMetrics[0].Value = 1
			},
			want: "requires a zero-drop sample",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validTestReport(t)
			test.mutate(&report)
			if err := ValidateReport(report); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateReport error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeReportRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	valid := validTestReport(t)
	encoded, err := MarshalReport(valid)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(encoded), `"schema_version":`, `"unknown_field": true, "schema_version":`, 1)
	if _, err := DecodeReport(strings.NewReader(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
	if _, err := DecodeReport(strings.NewReader(string(encoded) + `{}`)); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("trailing-value error = %v", err)
	}
}

func TestNewStatisticsUsesNearestRankAndPreservesRawSamples(t *testing.T) {
	statistics, err := NewStatistics("milliseconds", []float64{50, 10, 40, 20, 30})
	if err != nil {
		t.Fatal(err)
	}
	if statistics.Min != 10 || statistics.Median != 30 || statistics.P95 != 50 || statistics.Max != 50 {
		t.Fatalf("statistics = %#v", statistics)
	}
	if statistics.Values[0] != 50 {
		t.Fatalf("raw values were reordered: %#v", statistics.Values)
	}
}

func TestRenderMarkdownIsDeterministicAndPreservesCaveats(t *testing.T) {
	report := validTestReport(t)
	first, err := RenderMarkdown(report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderMarkdown(report)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("Markdown output is not deterministic")
	}
	for _, required := range []string{
		"Production target attainment is not evaluated or claimed",
		"`not_evaluated`",
		"Harness overhead per transaction",
		"HTTP VUsers per pod",
		"Ramp-up rate",
		"Metrics latency",
		"The probe measures MetricsCollector.Record only",
	} {
		if !strings.Contains(first, required) {
			t.Fatalf("Markdown is missing %q", required)
		}
	}
}

func TestSmokeProfileIsValidButNotReleaseEvidenceReady(t *testing.T) {
	report := validTestReport(t)
	report.Configuration.Profile = "smoke"
	report.Finalize()
	if err := ValidateReport(report); err != nil {
		t.Fatalf("ValidateReport: %v", err)
	}
	if report.Validation.ReleaseEvidenceReady {
		t.Fatal("smoke report unexpectedly marked release-evidence ready")
	}
	if len(report.Validation.Warnings) == 0 {
		t.Fatal("smoke report has no readiness warning")
	}
}

func TestReleaseProfileBelowMinimumIsNotReleaseEvidenceReady(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		weaken    func(*RunConfiguration)
	}{
		{name: "harness iterations", fieldName: "harness_iterations", weaken: func(config *RunConfiguration) { config.HarnessIterations-- }},
		{name: "harness samples", fieldName: "harness_samples", weaken: func(config *RunConfiguration) { config.HarnessSamples-- }},
		{name: "HTTP VUsers", fieldName: "http_vusers", weaken: func(config *RunConfiguration) { config.HTTPVUsers-- }},
		{name: "HTTP timeout", fieldName: "http_density_timeout_millis", weaken: func(config *RunConfiguration) { config.HTTPDensityTimeoutMillis-- }},
		{name: "HTTP hold", fieldName: "http_density_hold_millis", weaken: func(config *RunConfiguration) { config.HTTPDensityHoldMillis-- }},
		{name: "HTTP release grace", fieldName: "http_density_release_grace_millis", weaken: func(config *RunConfiguration) { config.HTTPDensityReleaseGraceMillis-- }},
		{name: "ramp VUsers", fieldName: "ramp_vusers", weaken: func(config *RunConfiguration) { config.RampVUsers-- }},
		{name: "ramp samples", fieldName: "ramp_samples", weaken: func(config *RunConfiguration) { config.RampSamples-- }},
		{name: "metrics samples", fieldName: "metrics_latency_samples", weaken: func(config *RunConfiguration) { config.MetricsLatencySamples-- }},
		{name: "metrics interval", fieldName: "metrics_interval_millis", weaken: func(config *RunConfiguration) { config.MetricsIntervalMillis++ }},
		{name: "metrics timeout", fieldName: "metrics_probe_timeout_millis", weaken: func(config *RunConfiguration) { config.MetricsProbeTimeoutMillis-- }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validTestReport(t)
			test.weaken(&report.Configuration)
			report.Finalize()
			if err := ValidateReport(report); err != nil {
				t.Fatalf("ValidateReport: %v", err)
			}
			if report.Validation.Status != "valid" {
				t.Fatalf("validation status = %q, want valid", report.Validation.Status)
			}
			if report.Validation.ReleaseEvidenceReady {
				t.Fatal("weakened release configuration unexpectedly marked release-evidence ready")
			}
			warnings := strings.Join(report.Validation.Warnings, "\n")
			if !strings.Contains(warnings, test.fieldName) || !strings.Contains(warnings, "release-evidence bounds") {
				t.Fatalf("warnings = %q, want weakened field %q and release-evidence reason", warnings, test.fieldName)
			}
		})
	}
}

func TestReleaseProfileWithShorterMetricsIntervalIsReleaseEvidenceReady(t *testing.T) {
	report := validTestReport(t)
	report.Configuration.MetricsIntervalMillis--
	report.Finalize()
	if err := ValidateReport(report); err != nil {
		t.Fatalf("ValidateReport: %v", err)
	}
	if !report.Validation.ReleaseEvidenceReady {
		t.Fatalf("shorter metrics interval unexpectedly not ready: %v", report.Validation.Warnings)
	}
}

func TestReleaseProfileAtMinimumIsReleaseEvidenceReady(t *testing.T) {
	report := validTestReport(t)
	report.Finalize()
	if err := ValidateReport(report); err != nil {
		t.Fatalf("ValidateReport: %v", err)
	}
	if !report.Validation.ReleaseEvidenceReady {
		t.Fatalf("release minimum unexpectedly not ready: %v", report.Validation.Warnings)
	}
}

func validTestReport(t *testing.T) Report {
	t.Helper()
	measurements, err := buildMeasurements(
		harnessProbe{NanosecondsPerTransaction: []float64{100, 110, 105}},
		httpDensityProbe{
			ConfiguredVUsers:         10_000,
			PeakConcurrentRequests:   10_000,
			RequestsReceived:         10_000,
			SuccessfulTransactions:   10_000,
			TimeToPeak:               200 * time.Millisecond,
			RuntimeGoroutineDelta:    30_000,
			StackInUseDeltaBytes:     20_000_000,
			HeapAllocDeltaBytes:      1_000_000,
			TotalAllocDeltaBytes:     2_000_000,
			StackBytesPerPeakRequest: 2_000,
		},
		rampProbe{VUsersPerSecond: []float64{900, 950, 1_000}},
		metricsLatencyProbe{Milliseconds: []float64{990, 1_005, 1_010}},
	)
	if err != nil {
		t.Fatalf("buildMeasurements: %v", err)
	}
	report := Report{
		SchemaVersion: SchemaVersion,
		EvidenceKind:  EvidenceKind,
		GeneratedAt:   time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
		Build: BuildMetadata{
			Version:            "1.2.3",
			DisplayVersion:     "v1.2.3",
			Commit:             "abcdef0",
			Revision:           "abcdef0123456789",
			BuildTime:          "2026-07-19T11:00:00Z",
			GoVersion:          "go1.26.5",
			VCSStatusAvailable: true,
		},
		Environment: EnvironmentMetadata{
			GOOS:                "linux",
			GOARCH:              "amd64",
			KernelVersion:       "6.8.0",
			CPUModel:            "Controlled CPU",
			LogicalCPUs:         4,
			PhysicalCores:       4,
			GOMAXPROCS:          4,
			PhysicalMemoryBytes: 8 << 30,
			HostClass:           "generator-4vcpu-8gb",
			RunnerImage:         "apexload-generator@sha256:example",
		},
		Configuration: RunConfiguration{
			Profile:                       "release",
			HarnessIterations:             250_000,
			HarnessSamples:                7,
			HTTPVUsers:                    10_000,
			HTTPDensityTimeoutMillis:      30_000,
			HTTPDensityHoldMillis:         250,
			HTTPDensityReleaseGraceMillis: 1_250,
			RampVUsers:                    1_000,
			RampSamples:                   5,
			MetricsLatencySamples:         7,
			MetricsIntervalMillis:         1_000,
			MetricsProbeTimeoutMillis:     4_000,
		},
		ClaimPolicy: ClaimPolicy{
			Reason: "Controlled local components do not cover production target scope.",
		},
		Measurements: measurements,
	}
	report.Finalize()
	return report
}
