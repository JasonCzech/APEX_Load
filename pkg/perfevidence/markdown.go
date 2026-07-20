package perfevidence

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderMarkdown renders the validated JSON artifact into a deterministic,
// human-readable companion. JSON remains the canonical machine-readable data.
func RenderMarkdown(report Report) (string, error) {
	if err := ValidateReport(report); err != nil {
		return "", err
	}

	var output strings.Builder
	output.WriteString("# APEX Load Performance Evidence\n\n")
	output.WriteString("This report contains controlled local component measurements. **Production target attainment is not evaluated or claimed.**\n\n")
	output.WriteString("## Run metadata\n\n")
	output.WriteString("| Field | Value |\n| --- | --- |\n")
	writeMarkdownRow(&output, "Generated at", report.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))
	writeMarkdownRow(&output, "Profile", report.Configuration.Profile)
	writeMarkdownRow(&output, "Build", report.Build.DisplayVersion)
	writeMarkdownRow(&output, "Revision", fallback(report.Build.Revision, "unavailable"))
	vcsStatus := "unknown"
	if report.Build.VCSStatusAvailable {
		vcsStatus = "clean"
		if report.Build.VCSModified {
			vcsStatus = "modified"
		}
	}
	writeMarkdownRow(&output, "VCS status", vcsStatus)
	writeMarkdownRow(&output, "Runtime", report.Build.GoVersion+" / "+report.Environment.GOOS+"/"+report.Environment.GOARCH)
	writeMarkdownRow(&output, "CPU", fmt.Sprintf("%s; logical=%d; physical=%d; GOMAXPROCS=%d", fallback(report.Environment.CPUModel, "unavailable"), report.Environment.LogicalCPUs, report.Environment.PhysicalCores, report.Environment.GOMAXPROCS))
	writeMarkdownRow(&output, "Physical memory bytes", strconv.FormatUint(report.Environment.PhysicalMemoryBytes, 10))
	if report.Environment.HostClass != "" {
		writeMarkdownRow(&output, "Host class", report.Environment.HostClass)
	}
	if report.Environment.RunnerImage != "" {
		writeMarkdownRow(&output, "Runner image", report.Environment.RunnerImage)
	}

	output.WriteString("\n## Measured versus target\n\n")
	output.WriteString("The numerical relation is descriptive only. Every row remains `not_evaluated` because the local probe scope differs from the production target scope.\n\n")
	output.WriteString("| Target area | Observed | Design target | Numerical relation | Attainment |\n| --- | ---: | ---: | --- | --- |\n")
	for _, measurement := range report.Measurements {
		comparison := measurement.TargetComparison
		observed := comparison.ComparedStatistic + " " + formatNumber(comparison.ObservedValue) + " " + measurement.Observed.Unit
		target := comparison.Target.Operator + " " + formatNumber(comparison.Target.Value) + " " + comparison.Target.Unit
		output.WriteString("| " + escapeMarkdownCell(comparison.Target.Name) + " | " + escapeMarkdownCell(observed) + " | " + escapeMarkdownCell(target) + " | " + escapeMarkdownCell(comparison.NumericRelation) + " | `" + comparison.AttainmentStatus + "` |\n")
	}

	output.WriteString("\n## Probe scope and limitations\n")
	for _, measurement := range report.Measurements {
		output.WriteString("\n### `" + measurement.ID + "`\n\n")
		output.WriteString(measurement.Description + "\n\n")
		output.WriteString("Target comparison is not evaluated: " + measurement.TargetComparison.Reason + "\n\n")
		for _, limitation := range measurement.Limitations {
			output.WriteString("- " + limitation + "\n")
		}
		if len(measurement.SupportingMetrics) > 0 {
			output.WriteString("\nSupporting measurements:\n\n")
			for _, metric := range measurement.SupportingMetrics {
				output.WriteString("- `" + metric.Name + "`: " + formatNumber(metric.Value) + " " + metric.Unit + "\n")
			}
		}
	}

	output.WriteString("\n## Artifact validation\n\n")
	output.WriteString("- Schema: `" + report.SchemaVersion + "`\n")
	output.WriteString("- Validation status: `" + report.Validation.Status + "`\n")
	output.WriteString("- Release-evidence ready: `" + strconv.FormatBool(report.Validation.ReleaseEvidenceReady) + "`\n")
	for _, warning := range report.Validation.Warnings {
		output.WriteString("- Warning: " + warning + "\n")
	}

	return output.String(), nil
}

func writeMarkdownRow(output *strings.Builder, field, value string) {
	output.WriteString("| " + escapeMarkdownCell(field) + " | " + escapeMarkdownCell(value) + " |\n")
}

func escapeMarkdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}
