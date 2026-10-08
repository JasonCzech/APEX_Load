package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// runReportVerdict is the analyst's verdict for a run. It is one decision about
// the run, so it is stored once per run rather than per report audience: a Pass
// set while viewing the executive report must also appear in the engineer and
// audit reports and in every export.
type runReportVerdict struct {
	VerdictOverride    string    `json:"verdict_override,omitempty"`
	VerdictExplanation string    `json:"verdict_explanation,omitempty"`
	Profile            string    `json:"profile,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
	UpdatedBy          string    `json:"updated_by,omitempty"`
}

func reportVerdictSettingKey(testID string) string {
	return fmt.Sprintf("report:verdict:%s", strings.TrimSpace(testID))
}

// loadRunReportVerdict returns the run's analyst verdict, or nil when none was
// ever set. Runs annotated before the verdict moved to the run fall back to the
// most recently updated profile record that carries a verdict.
func loadRunReportVerdict(store storage.Store, testID string) (*runReportVerdict, error) {
	verdict, _, err := loadRunReportVerdictRecord(store, testID)
	return verdict, err
}

// loadRunReportVerdictRecord also returns the stored run-level record exactly
// as persisted (empty when only legacy profile verdicts exist); it is the
// compare-and-swap witness for the next verdict write.
func loadRunReportVerdictRecord(store storage.Store, testID string) (*runReportVerdict, string, error) {
	if store == nil {
		return nil, "", nil
	}
	raw, err := store.GetSetting(reportVerdictSettingKey(testID))
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(raw) != "" {
		var verdict runReportVerdict
		if err := json.Unmarshal([]byte(raw), &verdict); err != nil {
			return nil, "", fmt.Errorf("decode report verdict: %w", err)
		}
		return &verdict, raw, nil
	}
	var latest *runReportVerdict
	for _, profile := range buildRunArchiveReportProfiles() {
		ann, err := loadReportAnnotations(store, testID, profile.ID)
		if err != nil {
			return nil, "", err
		}
		if ann == nil || strings.TrimSpace(ann.VerdictOverride) == "" {
			continue
		}
		if latest == nil || ann.UpdatedAt.After(latest.UpdatedAt) {
			latest = &runReportVerdict{
				VerdictOverride:    ann.VerdictOverride,
				VerdictExplanation: ann.VerdictExplanation,
				Profile:            profile.ID,
				UpdatedAt:          ann.UpdatedAt,
				UpdatedBy:          ann.UpdatedBy,
			}
		}
	}
	return latest, "", nil
}

// saveRunReportVerdictIfUnchanged writes the run's verdict only when the stored
// record still equals currentRaw, so two audiences changing the verdict at the
// same time cannot silently overwrite each other.
func saveRunReportVerdictIfUnchanged(store storage.Store, testID string, verdict runReportVerdict, currentRaw string) (bool, error) {
	if store == nil {
		return false, fmt.Errorf("storage backend unavailable")
	}
	cas, ok := store.(settingsCompareAndSwapper)
	if !ok {
		return false, fmt.Errorf("storage backend does not support report verdict compare-and-swap")
	}
	data, err := json.Marshal(verdict)
	if err != nil {
		return false, fmt.Errorf("encode report verdict: %w", err)
	}
	return cas.CompareAndSwapSetting(reportVerdictSettingKey(testID), currentRaw, string(data))
}

// applyRunReportVerdict overlays the run's verdict onto profile annotations.
func applyRunReportVerdict(ann *ReportAnnotations, verdict *runReportVerdict) {
	if ann == nil {
		return
	}
	if verdict == nil {
		ann.VerdictOverride, ann.VerdictExplanation = "", ""
		ann.VerdictProfile, ann.VerdictUpdatedAt, ann.VerdictUpdatedBy = "", nil, ""
		return
	}
	updatedAt := verdict.UpdatedAt
	ann.VerdictOverride = verdict.VerdictOverride
	ann.VerdictExplanation = verdict.VerdictExplanation
	ann.VerdictProfile = verdict.Profile
	ann.VerdictUpdatedAt = &updatedAt
	ann.VerdictUpdatedBy = verdict.UpdatedBy
}

// AnalystAssessmentInVerdict reports whether the verdict section must carry the
// run's analyst assessment: profiles without a sign-off section (engineer)
// would otherwise export the run without the verdict other audiences show.
func (d *runArchiveHTMLReportData) AnalystAssessmentInVerdict() bool {
	if d == nil || d.Annotations == nil || strings.TrimSpace(d.Annotations.VerdictOverride) == "" {
		return false
	}
	for _, section := range d.Profile.IncludedSections {
		if section == "signoff" {
			return false
		}
	}
	return true
}
