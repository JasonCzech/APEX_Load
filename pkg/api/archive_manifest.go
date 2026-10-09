package api

// manifest — split from archive.go (same package, behavior-preserving).

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

func loadRunArchiveManifest(manifestPath string) (*RunArchiveManifest, error) {
	data, err := readLocalFileLimitedContext(context.Background(), manifestPath, maxArchiveManifestDownloadBytes)
	if err != nil {
		return nil, err
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = archiveSchemaVersion
	}
	if err := validateRunArchiveManifestArtifacts(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func archiveManifestArtifactNames(manifest *RunArchiveManifest) (map[string]struct{}, error) {
	if err := validateRunArchiveManifestArtifacts(manifest); err != nil {
		return nil, err
	}
	artifacts := map[string]struct{}{}
	for _, name := range []string{
		manifest.TransactionsFile,
		manifest.TransactionsParquetFile,
		manifest.ResourceSamplesFile,
		manifest.ResourceSamplesParquetFile,
		manifest.IntervalSummariesFile,
		manifest.IntervalSummariesParquetFile,
		manifest.TestManifestFile,
		manifest.TestNotesFile,
		manifest.ELKEvidenceFile,
		manifest.DynatraceEvidenceFile,
		manifest.MonitorEvidenceFile,
		manifest.MonitorManagementFile,
		manifest.MonitorDiagnosticsFile,
		manifest.IntervalDistributionsFile,
		manifest.ProviderLookupsFile,
		manifest.ExemplarsFile,
		manifest.ClockObservationsFile,
		manifest.TelemetrySourcesFile,
		manifest.PhaseEventsFile,
		manifest.JointIntervalsFile,
		manifest.JointIntervalsParquetFile,
		"manifest.json",
	} {
		if strings.TrimSpace(name) == "" {
			continue
		}
		artifacts[name] = struct{}{}
	}
	for _, export := range manifest.CSVExports {
		if export.Status == "complete" {
			if _, err := normalizeArchiveArtifactName(export.FileName); err != nil {
				return nil, err
			}
			artifacts[export.FileName] = struct{}{}
		}
	}
	return artifacts, nil
}

func validateRunArchiveManifestArtifacts(manifest *RunArchiveManifest) error {
	if manifest == nil {
		return fmt.Errorf("archive manifest is missing")
	}
	var err error
	if manifest.TransactionsFile, err = optionalArchiveArtifactName("transactions_file", manifest.TransactionsFile); err != nil {
		return err
	}
	if manifest.ResourceSamplesFile, err = optionalArchiveArtifactName("resource_samples_file", manifest.ResourceSamplesFile); err != nil {
		return err
	}
	if manifest.IntervalSummariesFile, err = optionalArchiveArtifactName("interval_summaries_file", manifest.IntervalSummariesFile); err != nil {
		return err
	}
	if manifest.TransactionsParquetFile, err = optionalArchiveArtifactName("transactions_parquet_file", manifest.TransactionsParquetFile); err != nil {
		return err
	}
	if manifest.ResourceSamplesParquetFile, err = optionalArchiveArtifactName("resource_samples_parquet_file", manifest.ResourceSamplesParquetFile); err != nil {
		return err
	}
	if manifest.IntervalSummariesParquetFile, err = optionalArchiveArtifactName("interval_summaries_parquet_file", manifest.IntervalSummariesParquetFile); err != nil {
		return err
	}
	if manifest.TestManifestFile, err = optionalArchiveArtifactName("test_manifest_file", manifest.TestManifestFile); err != nil {
		return err
	}
	if manifest.TestNotesFile, err = optionalArchiveArtifactName("test_notes_file", manifest.TestNotesFile); err != nil {
		return err
	}
	if manifest.ELKEvidenceFile, err = optionalArchiveArtifactName("elk_evidence_file", manifest.ELKEvidenceFile); err != nil {
		return err
	}
	if manifest.DynatraceEvidenceFile, err = optionalArchiveArtifactName("dynatrace_evidence_file", manifest.DynatraceEvidenceFile); err != nil {
		return err
	}
	if manifest.MonitorEvidenceFile, err = optionalArchiveArtifactName("monitor_evidence_file", manifest.MonitorEvidenceFile); err != nil {
		return err
	}
	if manifest.MonitorManagementFile, err = optionalArchiveArtifactName("monitor_management_file", manifest.MonitorManagementFile); err != nil {
		return err
	}
	if manifest.MonitorManagementSnapshotCount < 0 || manifest.MonitorManagementRowCount < 0 {
		return fmt.Errorf("invalid monitor management counts")
	}
	if manifest.MonitorManagementFile == "" {
		if manifest.MonitorManagementVersion != 0 || manifest.MonitorManagementSnapshotCount != 0 || manifest.MonitorManagementRowCount != 0 {
			return fmt.Errorf("monitor management metadata requires an artifact")
		}
	} else if manifest.MonitorManagementVersion != 1 {
		return fmt.Errorf("unsupported monitor management version")
	}
	if manifest.MonitorDiagnosticsFile, err = optionalArchiveArtifactName("monitor_diagnostics_file", manifest.MonitorDiagnosticsFile); err != nil {
		return err
	}
	if manifest.IntervalDistributionsFile, err = optionalArchiveArtifactName("interval_distributions_file", manifest.IntervalDistributionsFile); err != nil {
		return err
	}
	if manifest.ProviderLookupsFile, err = optionalArchiveArtifactName("provider_lookups_file", manifest.ProviderLookupsFile); err != nil {
		return err
	}
	if manifest.ExemplarsFile, err = optionalArchiveArtifactName("exemplars_file", manifest.ExemplarsFile); err != nil {
		return err
	}
	if manifest.ClockObservationsFile, err = optionalArchiveArtifactName("clock_observations_file", manifest.ClockObservationsFile); err != nil {
		return err
	}
	if manifest.PhaseEventsFile, err = optionalArchiveArtifactName("phase_events_file", manifest.PhaseEventsFile); err != nil {
		return err
	}
	if manifest.JointIntervalsFile, err = optionalArchiveArtifactName("joint_intervals_file", manifest.JointIntervalsFile); err != nil {
		return err
	}
	if manifest.JointIntervalsParquetFile, err = optionalArchiveArtifactName("joint_intervals_parquet_file", manifest.JointIntervalsParquetFile); err != nil {
		return err
	}
	if manifest.TelemetrySourcesFile, err = optionalArchiveArtifactName("telemetry_sources_file", manifest.TelemetrySourcesFile); err != nil {
		return err
	}
	return nil
}

func requiredArchiveArtifactName(field, name string) (string, error) {
	normalized, err := normalizeArchiveArtifactName(name)
	if err != nil {
		return "", fmt.Errorf("invalid archive manifest artifact %s: %w", field, err)
	}
	return normalized, nil
}

func optionalArchiveArtifactName(field, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	return requiredArchiveArtifactName(field, name)
}

func normalizeArchiveArtifactName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("name is empty")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", fmt.Errorf("name contains NUL byte")
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("name contains backslash")
	}
	if hasWindowsVolumeName(trimmed) {
		return "", fmt.Errorf("name uses a Windows volume path")
	}
	if path.IsAbs(trimmed) || filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("name is absolute")
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", fmt.Errorf("name contains parent directory traversal")
		}
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("name is not a file path")
	}
	return cleaned, nil
}

func hasWindowsVolumeName(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	ch := name[0]
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func archiveArtifactPath(root, name string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("archive root is empty")
	}
	normalized, err := normalizeArchiveArtifactName(name)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullPath, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(normalized)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, fullPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive artifact escapes archive root")
	}
	return fullPath, nil
}
