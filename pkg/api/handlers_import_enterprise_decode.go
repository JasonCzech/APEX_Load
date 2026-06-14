package api

// handlers_import_enterprise_decode — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
)

func fidelityReportValue(value any) *enterprisearchive.ImportFidelityReport {
	if value == nil {
		return nil
	}
	if report, ok := value.(*enterprisearchive.ImportFidelityReport); ok {
		return report
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var report enterprisearchive.ImportFidelityReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return nil
	}
	return &report
}

func salesforceReviewValue(value any) *salesforce.Review {
	if value == nil {
		return nil
	}
	if review, ok := value.(*salesforce.Review); ok {
		return review
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var review salesforce.Review
	if err := json.Unmarshal(data, &review); err != nil {
		return nil
	}
	return &review
}

func runtimeSettingsParityValue(value any) []enterprisearchive.RuntimeSettingParityEntry {
	switch typed := value.(type) {
	case []enterprisearchive.RuntimeSettingParityEntry:
		return append([]enterprisearchive.RuntimeSettingParityEntry(nil), typed...)
	case []any:
		entries := make([]enterprisearchive.RuntimeSettingParityEntry, 0, len(typed))
		for _, item := range typed {
			mapping, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entry := enterprisearchive.RuntimeSettingParityEntry{
				ID:           stringValue(mapping["id"]),
				Area:         stringValue(mapping["area"]),
				Concept:      stringValue(mapping["concept"]),
				Fields:       stringSliceValue(mapping["fields"]),
				Status:       enterprisearchive.RuntimeSettingStatus(stringValue(mapping["status"])),
				Notes:        stringValue(mapping["notes"]),
				RuntimeFiles: stringSliceValue(mapping["runtime_files"]),
			}
			if entry.ID == "" {
				continue
			}
			entries = append(entries, entry)
		}
		return entries
	default:
		return nil
	}
}

func enterpriseArchiveMigrationReadinessValue(value any) *enterprisearchive.RuntimeSettingMigrationReadiness {
	switch typed := value.(type) {
	case *enterprisearchive.RuntimeSettingMigrationReadiness:
		return typed
	case enterprisearchive.RuntimeSettingMigrationReadiness:
		clone := typed
		return &clone
	case map[string]any:
		readiness := &enterprisearchive.RuntimeSettingMigrationReadiness{
			ScorePct:         intValue(typed["score_pct"]),
			Blocked:          boolValue(typed["blocked"]),
			Summary:          stringValue(typed["summary"]),
			BlockingStatuses: runtimeSettingStatusSliceValue(typed["blocking_statuses"]),
			BlockingSettings: runtimeSettingsParityValue(typed["blocking_settings"]),
			Policy: enterprisearchive.RuntimeSettingMigrationPolicy{
				BlockStatuses: runtimeSettingPolicyStatuses(typed["policy"]),
			},
		}
		if readiness.ScorePct == 0 && !readiness.Blocked && readiness.Summary == "" && len(readiness.BlockingStatuses) == 0 && len(readiness.BlockingSettings) == 0 && len(readiness.Policy.BlockStatuses) == 0 {
			return nil
		}
		return readiness
	default:
		return nil
	}
}

func trueClientArchiveInspectionValue(value any) *trueclient.ArchiveInspection {
	switch typed := value.(type) {
	case *trueclient.ArchiveInspection:
		return typed
	case trueclient.ArchiveInspection:
		return &typed
	default:
		return nil
	}
}

func sourceReviewValue(value any) *enterprisearchive.SourceReview {
	switch typed := value.(type) {
	case *enterprisearchive.SourceReview:
		return typed
	case enterprisearchive.SourceReview:
		clone := typed
		return &clone
	case map[string]any:
		review := &enterprisearchive.SourceReview{
			SourceFiles: sourceReviewFilesValue(typed["source_files"]),
			Mappings:    sourceReviewMappingsValue(typed["mappings"]),
		}
		if len(review.SourceFiles) == 0 && len(review.Mappings) == 0 {
			return nil
		}
		return review
	default:
		return nil
	}
}

func sourceReviewFilesValue(value any) []enterprisearchive.SourceReviewFile {
	switch typed := value.(type) {
	case []enterprisearchive.SourceReviewFile:
		return append([]enterprisearchive.SourceReviewFile(nil), typed...)
	case []any:
		files := make([]enterprisearchive.SourceReviewFile, 0, len(typed))
		for _, item := range typed {
			mapping, ok := item.(map[string]any)
			if !ok {
				continue
			}
			file := enterprisearchive.SourceReviewFile{
				Path:          stringValue(mapping["path"]),
				Language:      stringValue(mapping["language"]),
				Content:       stringValue(mapping["content"]),
				OriginalBytes: intValue(mapping["original_bytes"]),
				Truncated:     boolValue(mapping["truncated"]),
				Redacted:      boolValue(mapping["redacted"]),
			}
			if file.Path == "" {
				continue
			}
			files = append(files, file)
		}
		return files
	default:
		return nil
	}
}

func sourceReviewMappingsValue(value any) []enterprisearchive.SourceReviewMapping {
	switch typed := value.(type) {
	case []enterprisearchive.SourceReviewMapping:
		return append([]enterprisearchive.SourceReviewMapping(nil), typed...)
	case []any:
		mappings := make([]enterprisearchive.SourceReviewMapping, 0, len(typed))
		for _, item := range typed {
			raw, ok := item.(map[string]any)
			if !ok {
				continue
			}
			mapping := enterprisearchive.SourceReviewMapping{
				ID:          stringValue(raw["id"]),
				SourceFile:  stringValue(raw["source_file"]),
				StartLine:   intValue(raw["start_line"]),
				EndLine:     intValue(raw["end_line"]),
				Function:    stringValue(raw["function"]),
				TargetPath:  stringValue(raw["target_path"]),
				TargetName:  stringValue(raw["target_name"]),
				TargetKind:  stringValue(raw["target_kind"]),
				Status:      stringValue(raw["status"]),
				SupportMode: stringValue(raw["support_mode"]),
				Notes:       stringValue(raw["notes"]),
			}
			if mapping.ID == "" && mapping.SourceFile == "" && mapping.TargetPath == "" {
				continue
			}
			mappings = append(mappings, mapping)
		}
		return mappings
	default:
		return nil
	}
}

func intMapValue(value any) map[string]int {
	switch typed := value.(type) {
	case map[string]int:
		if len(typed) == 0 {
			return nil
		}
		clone := make(map[string]int, len(typed))
		for key, count := range typed {
			clone[key] = count
		}
		return clone
	case map[string]any:
		if len(typed) == 0 {
			return nil
		}
		clone := make(map[string]int, len(typed))
		for key, raw := range typed {
			switch count := raw.(type) {
			case int:
				clone[key] = count
			case float64:
				clone[key] = int(count)
			}
		}
		if len(clone) == 0 {
			return nil
		}
		return clone
	default:
		return nil
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return parseBoolDefault(typed, false)
	default:
		return false
	}
}

func runtimeSettingStatusSliceValue(value any) []enterprisearchive.RuntimeSettingStatus {
	switch typed := value.(type) {
	case []enterprisearchive.RuntimeSettingStatus:
		return append([]enterprisearchive.RuntimeSettingStatus(nil), typed...)
	case []any:
		statuses := make([]enterprisearchive.RuntimeSettingStatus, 0, len(typed))
		for _, item := range typed {
			status := enterprisearchive.RuntimeSettingStatus(stringValue(item))
			if status == "" {
				continue
			}
			statuses = append(statuses, status)
		}
		return statuses
	default:
		return nil
	}
}

func runtimeSettingPolicyStatuses(value any) []enterprisearchive.RuntimeSettingStatus {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return runtimeSettingStatusSliceValue(mapping["block_statuses"])
}

func runtimeSettingsParitySeverity(entries []enterprisearchive.RuntimeSettingParityEntry) string {
	severity := "info"
	for _, entry := range entries {
		switch entry.Status {
		case enterprisearchive.RuntimeSettingStatusBlocked:
			return "blocker"
		case enterprisearchive.RuntimeSettingStatusPartial, enterprisearchive.RuntimeSettingStatusCompatibilityOnly:
			severity = "warning"
		}
	}
	return severity
}

func runtimeSettingsParitySummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "Imported runtime settings were classified against APEX Load's code-owned runtime compatibility matrix."
	}
	parts := make([]string, 0, 5)
	for _, status := range []string{
		string(enterprisearchive.RuntimeSettingStatusExecuted),
		string(enterprisearchive.RuntimeSettingStatusPartial),
		string(enterprisearchive.RuntimeSettingStatusCompatibilityOnly),
		string(enterprisearchive.RuntimeSettingStatusBlocked),
		string(enterprisearchive.RuntimeSettingStatusNonApplicable),
	} {
		if counts[status] == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[status], strings.ReplaceAll(status, "_", "-")))
	}
	if len(parts) == 0 {
		return "Imported runtime settings were classified against APEX Load's code-owned runtime compatibility matrix."
	}
	return "Imported runtime settings were classified against APEX Load's code-owned runtime compatibility matrix: " + strings.Join(parts, ", ") + "."
}

func runtimeSettingsParityItems(entries []enterprisearchive.RuntimeSettingParityEntry) []string {
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		item := fmt.Sprintf("%s: %s / %s", runtimeSettingStatusLabel(entry.Status), entry.Area, entry.Concept)
		if len(entry.RuntimeFiles) > 0 {
			item += " [" + strings.Join(entry.RuntimeFiles, ", ") + "]"
		}
		items = append(items, item)
	}
	return items
}

func migrationReadinessSeverity(readiness *enterprisearchive.RuntimeSettingMigrationReadiness) string {
	if readiness == nil {
		return "info"
	}
	if readiness.Blocked {
		return "blocker"
	}
	if readiness.ScorePct < 85 {
		return "warning"
	}
	return "info"
}

func migrationReadinessItems(readiness *enterprisearchive.RuntimeSettingMigrationReadiness) []string {
	if readiness == nil {
		return nil
	}
	items := []string{
		fmt.Sprintf("Score: %d%%", readiness.ScorePct),
		"Block policy: " + runtimeSettingStatusLabels(readiness.Policy.BlockStatuses),
	}
	if readiness.Blocked {
		items = append(items, "Matched blocking statuses: "+runtimeSettingStatusLabels(readiness.BlockingStatuses))
		for _, entry := range readiness.BlockingSettings {
			items = append(items, fmt.Sprintf("Blocking setting: %s / %s", entry.Area, entry.Concept))
		}
	} else {
		items = append(items, "No imported runtime settings matched the selected block statuses.")
	}
	return items
}

func runtimeSettingStatusLabels(statuses []enterprisearchive.RuntimeSettingStatus) string {
	if len(statuses) == 0 {
		return "None"
	}
	labels := make([]string, 0, len(statuses))
	for _, status := range statuses {
		labels = append(labels, runtimeSettingStatusLabel(status))
	}
	return strings.Join(labels, ", ")
}

func runtimeSettingStatusLabel(status enterprisearchive.RuntimeSettingStatus) string {
	switch status {
	case enterprisearchive.RuntimeSettingStatusExecuted:
		return "Executed"
	case enterprisearchive.RuntimeSettingStatusPartial:
		return "Partial"
	case enterprisearchive.RuntimeSettingStatusCompatibilityOnly:
		return "Compatibility-only"
	case enterprisearchive.RuntimeSettingStatusBlocked:
		return "Blocked"
	case enterprisearchive.RuntimeSettingStatusNonApplicable:
		return "Non-applicable"
	default:
		label := strings.ReplaceAll(string(status), "_", " ")
		if label == "" {
			return "Unknown"
		}
		return strings.ToUpper(label[:1]) + label[1:]
	}
}

func sortedRecordKeys(value any) []string {
	v := reflect.ValueOf(value)
	if !v.IsValid() || v.Kind() != reflect.Map || v.Type().Key().Kind() != reflect.String {
		return nil
	}

	keys := make([]string, 0, v.Len())
	for _, key := range v.MapKeys() {
		keys = append(keys, key.String())
	}
	sort.Strings(keys)
	return keys
}
