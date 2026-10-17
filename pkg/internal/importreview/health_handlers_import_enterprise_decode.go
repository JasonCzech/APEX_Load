package importreview

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
)

func FidelityReportValue(value any) *enterprisearchive.ImportFidelityReport {
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

func RuntimeSettingsParityValue(value any) []enterprisearchive.RuntimeSettingParityEntry {
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
				ID:           StringValue(mapping["id"]),
				Area:         StringValue(mapping["area"]),
				Concept:      StringValue(mapping["concept"]),
				Fields:       StringSliceValue(mapping["fields"]),
				Status:       enterprisearchive.RuntimeSettingStatus(StringValue(mapping["status"])),
				Notes:        StringValue(mapping["notes"]),
				RuntimeFiles: StringSliceValue(mapping["runtime_files"]),
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

func EnterpriseArchiveMigrationReadinessValue(value any) *enterprisearchive.RuntimeSettingMigrationReadiness {
	switch typed := value.(type) {
	case *enterprisearchive.RuntimeSettingMigrationReadiness:
		return typed
	case enterprisearchive.RuntimeSettingMigrationReadiness:
		clone := typed
		return &clone
	case map[string]any:
		readiness := &enterprisearchive.RuntimeSettingMigrationReadiness{
			ScorePct:         IntValue(typed["score_pct"]),
			Blocked:          BoolValue(typed["blocked"]),
			Summary:          StringValue(typed["summary"]),
			BlockingStatuses: RuntimeSettingStatusSliceValue(typed["blocking_statuses"]),
			BlockingSettings: RuntimeSettingsParityValue(typed["blocking_settings"]),
			Policy: enterprisearchive.RuntimeSettingMigrationPolicy{
				BlockStatuses: RuntimeSettingPolicyStatuses(typed["policy"]),
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

func RuntimeSettingStatusSliceValue(value any) []enterprisearchive.RuntimeSettingStatus {
	switch typed := value.(type) {
	case []enterprisearchive.RuntimeSettingStatus:
		return append([]enterprisearchive.RuntimeSettingStatus(nil), typed...)
	case []any:
		statuses := make([]enterprisearchive.RuntimeSettingStatus, 0, len(typed))
		for _, item := range typed {
			status := enterprisearchive.RuntimeSettingStatus(StringValue(item))
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

func RuntimeSettingPolicyStatuses(value any) []enterprisearchive.RuntimeSettingStatus {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return RuntimeSettingStatusSliceValue(mapping["block_statuses"])
}
