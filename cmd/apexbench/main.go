// Command apexbench emits controlled local performance evidence for APEX Load.
// It never writes a file unless the caller supplies an explicit output path.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/perfevidence"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "apexbench:", err)
		os.Exit(1)
	}
}

func run() error {
	profile := flag.String("profile", "smoke", "benchmark profile: smoke or release")
	output := flag.String("output", "", "explicit JSON output path (default: stdout)")
	markdownOutput := flag.String("markdown-output", "", "explicit Markdown companion output path")
	overwrite := flag.Bool("overwrite", false, "allow replacement of explicitly named output files")
	validatePath := flag.String("validate", "", "validate an existing JSON report instead of running probes")
	format := flag.String("format", "json", "stdout format when no output path is supplied: json or markdown")
	hostClass := flag.String("host-class", os.Getenv("APEXLOAD_BENCHMARK_HOST_CLASS"), "operator-defined hardware/runner class label")
	runnerImage := flag.String("runner-image", os.Getenv("APEXLOAD_BENCHMARK_RUNNER_IMAGE"), "generator or CI image identifier, when applicable")
	vcsStatus := flag.String("vcs-status", os.Getenv("APEXLOAD_BENCHMARK_VCS_STATUS"), "source status override: clean, modified, or unknown")
	harnessIterations := flag.Int("harness-iterations", 0, "override metrics hot-path iterations per sample")
	harnessSamples := flag.Int("harness-samples", 0, "override metrics hot-path sample count")
	httpVUsers := flag.Int("http-vusers", 0, "override real HTTP VUser density count")
	httpDensityTimeout := flag.Duration("http-density-timeout", 0, "override local HTTP density probe timeout")
	httpDensityHold := flag.Duration("http-density-hold", -1, "override time to hold concurrent loopback requests")
	rampVUsers := flag.Int("ramp-vusers", 0, "override synthetic engine VUser count")
	rampSamples := flag.Int("ramp-samples", 0, "override synthetic engine ramp sample count")
	metricsSamples := flag.Int("metrics-samples", 0, "override metrics publication-latency sample count")
	metricsInterval := flag.Duration("metrics-interval", 0, "override collector interval")
	metricsTimeout := flag.Duration("metrics-timeout", 0, "override per-sample collector timeout")
	flag.Parse()

	if *format != "json" && *format != "markdown" {
		return fmt.Errorf("format must be json or markdown")
	}
	if strings.TrimSpace(*validatePath) != "" {
		return validateExisting(*validatePath, *format, *markdownOutput, *overwrite)
	}

	config, err := perfevidence.DefaultConfig(*profile)
	if err != nil {
		return err
	}
	config.HostClass = *hostClass
	config.RunnerImage = *runnerImage
	config.VCSStatus = strings.ToLower(strings.TrimSpace(*vcsStatus))
	applyPositiveIntOverride(&config.HarnessIterations, *harnessIterations)
	applyPositiveIntOverride(&config.HarnessSamples, *harnessSamples)
	applyPositiveIntOverride(&config.HTTPVUsers, *httpVUsers)
	applyPositiveDurationOverride(&config.HTTPDensityTimeout, *httpDensityTimeout)
	if *httpDensityHold >= 0 {
		config.HTTPDensityHold = *httpDensityHold
	}
	applyPositiveIntOverride(&config.RampVUsers, *rampVUsers)
	applyPositiveIntOverride(&config.RampSamples, *rampSamples)
	applyPositiveIntOverride(&config.MetricsLatencySamples, *metricsSamples)
	applyPositiveDurationOverride(&config.MetricsInterval, *metricsInterval)
	applyPositiveDurationOverride(&config.MetricsProbeTimeout, *metricsTimeout)
	if err := config.Validate(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintf(os.Stderr, "running %s performance evidence profile\n", config.Profile)
	report, err := perfevidence.Run(ctx, config)
	if err != nil {
		return err
	}
	jsonData, err := perfevidence.MarshalReport(report)
	if err != nil {
		return err
	}
	markdown, err := perfevidence.RenderMarkdown(report)
	if err != nil {
		return err
	}

	if strings.TrimSpace(*output) == "" {
		if *format == "markdown" {
			fmt.Print(markdown)
		} else {
			_, _ = os.Stdout.Write(jsonData)
		}
	} else if err := writeOutput(*output, jsonData, *overwrite); err != nil {
		return err
	}
	if strings.TrimSpace(*markdownOutput) != "" {
		if err := writeOutput(*markdownOutput, []byte(markdown), *overwrite); err != nil {
			return err
		}
	}
	if strings.TrimSpace(*output) != "" || strings.TrimSpace(*markdownOutput) != "" {
		fmt.Fprintf(os.Stderr, "report valid; release_evidence_ready=%t\n", report.Validation.ReleaseEvidenceReady)
	}
	return nil
}

func validateExisting(path, format, markdownOutput string, overwrite bool) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open report: %w", err)
	}
	report, decodeErr := perfevidence.DecodeReport(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return decodeErr
	}
	if closeErr != nil {
		return fmt.Errorf("close report: %w", closeErr)
	}
	markdown, err := perfevidence.RenderMarkdown(report)
	if err != nil {
		return err
	}
	if strings.TrimSpace(markdownOutput) != "" {
		if err := writeOutput(markdownOutput, []byte(markdown), overwrite); err != nil {
			return err
		}
	}
	if format == "markdown" {
		fmt.Print(markdown)
	} else {
		fmt.Printf("valid %s release_evidence_ready=%t\n", report.SchemaVersion, report.Validation.ReleaseEvidenceReady)
	}
	return nil
}

func writeOutput(path string, data []byte, overwrite bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("output path is empty")
	}
	flags := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return fmt.Errorf("create output %q: %w", path, err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write output %q: %w", path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close output %q: %w", path, closeErr)
	}
	return nil
}

func applyPositiveIntOverride(target *int, value int) {
	if value > 0 {
		*target = value
	}
}

func applyPositiveDurationOverride(target *time.Duration, value time.Duration) {
	if value > 0 {
		*target = value
	}
}
