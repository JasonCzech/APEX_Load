package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// handlers_import_enterprise_decode — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/agentforce"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/mulesoft"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
)

var fidelityReportValue = importreview.FidelityReportValue

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

func agentforceReviewValue(value any) *agentforce.Review {
	if value == nil {
		return nil
	}
	if review, ok := value.(*agentforce.Review); ok {
		return review
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var review agentforce.Review
	if err := json.Unmarshal(data, &review); err != nil {
		return nil
	}
	return &review
}

func mulesoftReviewValue(value any) *mulesoft.Review {
	if value == nil {
		return nil
	}
	if review, ok := value.(*mulesoft.Review); ok {
		return review
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var review mulesoft.Review
	if err := json.Unmarshal(data, &review); err != nil {
		return nil
	}
	return &review
}

var runtimeSettingsParityValue = importreview.RuntimeSettingsParityValue

var enterpriseArchiveMigrationReadinessValue = importreview.EnterpriseArchiveMigrationReadinessValue

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

var sourceReviewValue = importreview.SourceReviewValue

var sourceReviewFilesValue = importreview.SourceReviewFilesValue

var sourceReviewMappingsValue = importreview.SourceReviewMappingsValue

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

var intValue = importreview.IntValue

var boolValue = importreview.BoolValue

var runtimeSettingStatusSliceValue = importreview.RuntimeSettingStatusSliceValue

var runtimeSettingPolicyStatuses = importreview.RuntimeSettingPolicyStatuses

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
