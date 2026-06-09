package api

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	archiveComparisonBreakdownOverall  = "overall"
	archiveComparisonBreakdownPlatform = "platform"
	archiveComparisonBreakdownEngine   = "engine"
	archiveComparisonBreakdownDevice   = "device"
)

type archiveComparisonRun struct {
	ID              string
	Name            string
	Report          *RunArchiveReport
	Action          string
	ActionStats     *RunArchiveActionStats
	ActionSummaries []IntervalSummaryRecord
}

func writeArchiveComparisonCSV(writer io.Writer, breakdown string, action string, runs ...archiveComparisonRun) error {
	breakdown = normalizeArchiveComparisonBreakdown(breakdown)
	if breakdown == "" {
		return fmt.Errorf("unsupported comparison breakdown")
	}
	action = strings.TrimSpace(action)
	csvWriter := csv.NewWriter(writer)
	headers := []string{
		"run_id",
		"run_name",
	}
	if action != "" {
		headers = append(headers, "action_name")
	}
	headers = append(headers,
		"breakdown",
		"segment_key",
		"device_platform",
		"browser_engine",
		"device_tier",
		"samples",
		"avg_lcp_ms",
		"avg_cls_score",
		"avg_inp_ms",
		"transactions",
		"error_pct",
	)
	if action != "" {
		headers = append(headers, "avg_response_ms")
	}
	headers = append(headers,
		"peak_tps",
		"peak_target_tps",
		"max_throughput_gap_pct",
		"throughput_missed_intervals",
		"throughput_target_missed",
		"peak_active_vusers",
	)
	if err := csvWriter.Write(headers); err != nil {
		return err
	}
	for _, run := range runs {
		if run.Report == nil {
			continue
		}
		if err := writeArchiveComparisonRows(csvWriter, breakdown, action, run); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func normalizeArchiveComparisonBreakdown(raw string) string {
	switch raw {
	case "", archiveComparisonBreakdownDevice:
		return archiveComparisonBreakdownDevice
	case archiveComparisonBreakdownOverall, archiveComparisonBreakdownPlatform, archiveComparisonBreakdownEngine:
		return raw
	default:
		return ""
	}
}

func writeArchiveComparisonRows(writer *csv.Writer, breakdown string, action string, run archiveComparisonRun) error {
	if strings.TrimSpace(action) != "" {
		return writer.Write(archiveComparisonActionCSVRow(run, action))
	}
	switch breakdown {
	case archiveComparisonBreakdownOverall:
		return writer.Write(archiveComparisonCSVRow(run, breakdown, "overall", "", "", "", run.Report.Overview.Browser))
	case archiveComparisonBreakdownPlatform:
		for _, key := range sortedBrowserPlatformKeys(run.Report.BrowserByPlatform) {
			metrics := run.Report.BrowserByPlatform[key]
			if err := writer.Write(archiveComparisonCSVRow(run, breakdown, firstNonEmpty(metrics.DevicePlatform, key), metrics.DevicePlatform, metrics.BrowserEngine, "", browserLiveMetricsFromPlatform(metrics))); err != nil {
				return err
			}
		}
	case archiveComparisonBreakdownEngine:
		for _, key := range sortedBrowserEngineKeys(run.Report.BrowserByEngine) {
			metrics := run.Report.BrowserByEngine[key]
			if err := writer.Write(archiveComparisonCSVRow(run, breakdown, firstNonEmpty(metrics.BrowserEngine, key), "", metrics.BrowserEngine, "", browserLiveMetricsFromEngine(metrics))); err != nil {
				return err
			}
		}
	case archiveComparisonBreakdownDevice:
		for _, key := range sortedBrowserDeviceKeys(run.Report.BrowserByDevice) {
			metrics := run.Report.BrowserByDevice[key]
			if err := writer.Write(archiveComparisonCSVRow(run, breakdown, key, metrics.DevicePlatform, metrics.BrowserEngine, metrics.DeviceTier, browserLiveMetricsFromDevice(metrics))); err != nil {
				return err
			}
		}
	}
	return nil
}

func archiveComparisonActionCSVRow(run archiveComparisonRun, action string) []string {
	stats := run.ActionStats
	if stats == nil {
		emptyStats := RunArchiveActionStats{}
		stats = &emptyStats
	}
	return []string{
		run.ID,
		run.Name,
		action,
		"action",
		action,
		"",
		"",
		"",
		"0",
		"",
		"",
		"",
		formatCSVInt(stats.Transactions),
		formatCSVFloat(stats.ErrorPct),
		formatCSVFloat(stats.AvgResponseMs),
		formatCSVFloat(archiveComparisonActionPeakTPS(run.ActionSummaries)),
		"",
		"",
		"",
		"",
		"",
	}
}

func archiveComparisonCSVRow(run archiveComparisonRun, breakdown string, segmentKey string, platform string, engine string, tier string, browser *BrowserLiveMetrics) []string {
	samples := int64(0)
	lcp := ""
	cls := ""
	inp := ""
	if browser != nil {
		samples = browser.Samples
		lcp = formatCSVFloat(browser.AvgLCPMs)
		cls = formatCSVFloat(browser.AvgCLSScore)
		inp = formatCSVFloat(browser.AvgINPMs)
	}
	return []string{
		run.ID,
		run.Name,
		breakdown,
		segmentKey,
		platform,
		engine,
		tier,
		formatCSVInt(samples),
		lcp,
		cls,
		inp,
		formatCSVInt(run.Report.Overview.Transactions),
		formatCSVFloat(run.Report.Overview.ErrorPct),
		formatCSVFloat(run.Report.Overview.PeakTPS),
		formatCSVFloat(run.Report.Overview.PeakTargetTPS),
		formatCSVFloat(run.Report.Overview.MaxThroughputGapPct),
		formatCSVInt(run.Report.Overview.ThroughputMissedIntervals),
		fmt.Sprintf("%t", run.Report.Overview.ThroughputMissedIntervals > 0),
		formatCSVInt(run.Report.Overview.PeakActiveVUsers),
	}
}

func archiveComparisonActionPeakTPS(summaries []IntervalSummaryRecord) float64 {
	var peak float64
	for _, summary := range summaries {
		if summary.TPS > peak {
			peak = summary.TPS
		}
	}
	return peak
}

func browserLiveMetricsFromDevice(metrics *BrowserDeviceLiveMetrics) *BrowserLiveMetrics {
	if metrics == nil {
		return nil
	}
	return &BrowserLiveMetrics{
		Samples:              metrics.Samples,
		AvgFCPMs:             metrics.AvgFCPMs,
		AvgLCPMs:             metrics.AvgLCPMs,
		AvgCLSScore:          metrics.AvgCLSScore,
		AvgINPMs:             metrics.AvgINPMs,
		AvgBlockingResources: metrics.AvgBlockingResources,
		AvgLongTaskCount:     metrics.AvgLongTaskCount,
		AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
	}
}

func browserLiveMetricsFromPlatform(metrics *BrowserPlatformLiveMetrics) *BrowserLiveMetrics {
	if metrics == nil {
		return nil
	}
	return &BrowserLiveMetrics{
		Samples:              metrics.Samples,
		AvgFCPMs:             metrics.AvgFCPMs,
		AvgLCPMs:             metrics.AvgLCPMs,
		AvgCLSScore:          metrics.AvgCLSScore,
		AvgINPMs:             metrics.AvgINPMs,
		AvgBlockingResources: metrics.AvgBlockingResources,
		AvgLongTaskCount:     metrics.AvgLongTaskCount,
		AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
	}
}

func browserLiveMetricsFromEngine(metrics *BrowserEngineLiveMetrics) *BrowserLiveMetrics {
	if metrics == nil {
		return nil
	}
	return &BrowserLiveMetrics{
		Samples:              metrics.Samples,
		AvgFCPMs:             metrics.AvgFCPMs,
		AvgLCPMs:             metrics.AvgLCPMs,
		AvgCLSScore:          metrics.AvgCLSScore,
		AvgINPMs:             metrics.AvgINPMs,
		AvgBlockingResources: metrics.AvgBlockingResources,
		AvgLongTaskCount:     metrics.AvgLongTaskCount,
		AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
	}
}

func sortedBrowserDeviceKeys(values map[string]*BrowserDeviceLiveMetrics) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBrowserPlatformKeys(values map[string]*BrowserPlatformLiveMetrics) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBrowserEngineKeys(values map[string]*BrowserEngineLiveMetrics) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
