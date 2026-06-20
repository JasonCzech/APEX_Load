package api

// import_promotion_gating.go — refuses to launch a script whose import fidelity
// report classified it as not promotable to executable (PromotionStatus ==
// "blocked"): e.g. lr_unmask secret-gated request bodies, request bodies that
// were detected but not converted, or an adapter-required protocol. Mirrors the
// external-runner / java-vuser launch gates (external_runner_gating.go), gating
// both PreflightLaunch and the non-DRAFT test-creation path.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// allowBlockedImportEnv overrides the gate for the rare case where launching a
// fidelity-blocked import is intentional.
const allowBlockedImportEnv = "APEXLOAD_ALLOW_BLOCKED_IMPORT"

func allowBlockedImportPromotion() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(allowBlockedImportEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// scriptImportPromotionBlockers returns the fidelity-report blockers for a script
// whose import was classified as not promotable. An empty result means runnable.
func scriptImportPromotionBlockers(script *dsl.Script) []string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	report := fidelityReportValue(script.Metadata.Import.Preserved["fidelity_report"])
	if report == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(report.PromotionStatus), enterprisearchive.ImportPromotionBlocked) {
		return nil
	}
	if len(report.Blockers) == 0 {
		return []string{"Imported script is marked not promotable to executable."}
	}
	return append([]string(nil), report.Blockers...)
}

// importPromotionLaunchConflicts returns a blocking conflict per script whose
// import fidelity report is not promotable. Operators may override the gate via
// APEXLOAD_ALLOW_BLOCKED_IMPORT.
func importPromotionLaunchConflicts(groups []TestGroup) []LaunchConflict {
	if allowBlockedImportPromotion() {
		return nil
	}
	conflicts := make([]LaunchConflict, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		if group.script == nil {
			continue
		}
		blockers := scriptImportPromotionBlockers(group.script)
		if len(blockers) == 0 {
			continue
		}
		key := strings.TrimSpace(group.script.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		conflicts = append(conflicts, LaunchConflict{
			Code:       "import_promotion_blocked",
			Severity:   "blocking",
			Source:     "import_fidelity",
			ResourceID: key,
			Message:    importPromotionBlockedMessage(blockers),
		})
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].ResourceID < conflicts[j].ResourceID })
	return conflicts
}

func importPromotionLaunchConflict(groups []TestGroup) *LaunchConflict {
	conflicts := importPromotionLaunchConflicts(groups)
	if len(conflicts) == 0 {
		return nil
	}
	return &conflicts[0]
}

func importPromotionBlockedMessage(blockers []string) string {
	return fmt.Sprintf("Imported script is not promotable to executable: %s Set %s=true to override.",
		strings.Join(blockers, " "), allowBlockedImportEnv)
}
