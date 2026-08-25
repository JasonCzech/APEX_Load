package api

// import_promotion_gating.go — refuses to launch a script whose import fidelity
// report classified it as not promotable to executable (PromotionStatus ==
// "blocked"): e.g. lr_unmask secret-gated request bodies, request bodies that
// were detected but not converted, or an adapter-required protocol. Mirrors the
// external-runner / java-vuser launch gates (external_runner_gating.go), gating
// both PreflightLaunch and the non-DRAFT test-creation path.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// allowBlockedImportEnv overrides the gate for the rare case where launching a
// fidelity-blocked import is intentional.
const allowBlockedImportEnv = "APEXLOAD_ALLOW_BLOCKED_IMPORT"
const javaVuserCombinedImportReviewKey = "java_vuser_combined_import_review_v1"

func allowBlockedImportPromotion() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(allowBlockedImportEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// scriptImportPromotionBlockers returns fidelity and preserved-review blockers
// for an imported script. An empty result means runnable.
func scriptImportPromotionBlockers(script *dsl.Script) []string {
	importedJava := isGovernedJavaVuserDraft(script) &&
		(isCombinedJavaVuserPhaseDraft(script) || javaVuserHasCombinedImportReviewMarker(script)) &&
		javaVuserHasEnterpriseArchiveProvenance(script)
	var preserved map[string]any
	if script != nil && script.Metadata != nil && script.Metadata.Import != nil {
		preserved = script.Metadata.Import.Preserved
	}
	report := fidelityReportValue(preserved["fidelity_report"])
	blockers := importedJavaVuserReviewEvidenceBlockers(report, preserved, importedJava)
	if report == nil {
		return uniqueSortedStrings(blockers)
	}
	reportBlocked := strings.EqualFold(strings.TrimSpace(report.PromotionStatus), enterprisearchive.ImportPromotionBlocked)
	// A server-produced report never carries concrete blockers while claiming
	// promotable status. For an imported Java draft, retain those blockers even
	// if a client edits only the status field before authoring approval.
	if !reportBlocked && !(importedJava && len(report.Blockers) > 0) {
		return uniqueSortedStrings(blockers)
	}
	reportBlockers := append([]string(nil), report.Blockers...)
	if isGovernedJavaVuserDraft(script) {
		reportBlockers = filterJavaVuserRunnerClassificationBlockers(reportBlockers)
	}
	if len(reportBlockers) == 0 && len(report.Blockers) > 0 {
		return uniqueSortedStrings(blockers)
	}
	if len(reportBlockers) == 0 {
		reportBlockers = []string{"Imported script is marked not promotable to executable."}
	}
	return uniqueSortedStrings(append(blockers, reportBlockers...))
}

func javaVuserHasCombinedImportReviewMarker(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	return boolValue(script.Metadata.Import.Preserved[javaVuserCombinedImportReviewKey])
}

func isCombinedJavaVuserPhaseDraft(script *dsl.Script) bool {
	return script != nil &&
		strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") &&
		(javaVuserActionsRequirePhaseLifecycle(script.Init) ||
			javaVuserActionsRequirePhaseLifecycle(script.Actions) ||
			javaVuserActionsRequirePhaseLifecycle(script.Teardown))
}

func javaVuserHasEnterpriseArchiveProvenance(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(script.GeneratedFrom), "enterprise_script_archive") ||
		strings.EqualFold(strings.TrimSpace(script.GeneratedBy), "apexload-enterprise-script-archive-java-importer") {
		return true
	}
	if script.Metadata != nil && script.Metadata.Import != nil &&
		strings.EqualFold(strings.TrimSpace(script.Metadata.Import.Source), "enterprise_script_archive") {
		return true
	}
	for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		if javaVuserActionsHaveEnterpriseArchiveRef(actions) {
			return true
		}
	}
	return false
}

func javaVuserActionsHaveEnterpriseArchiveRef(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") &&
			strings.EqualFold(strings.TrimSpace(fmt.Sprint(action.Params["source_archive_ref"])), "enterprise_script_archive") {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if javaVuserActionsHaveEnterpriseArchiveRef(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}

func importedJavaVuserReviewEvidenceBlockers(
	report *enterprisearchive.ImportFidelityReport,
	preserved map[string]any,
	importedJava bool,
) []string {
	if !importedJava {
		return nil
	}
	blockers := make([]string, 0, 4)
	if report == nil {
		blockers = append(blockers, "Imported combined Java Vuser draft is missing its fidelity report; re-import the archive and approve the unchanged review artifact.")
	} else {
		status := strings.ToLower(strings.TrimSpace(report.PromotionStatus))
		if status != enterprisearchive.ImportPromotionPromotable && status != enterprisearchive.ImportPromotionBlocked {
			blockers = append(blockers, "Imported combined Java Vuser draft has an invalid fidelity-report status; re-import the archive before authoring approval.")
		}
	}

	review := sourceReviewValue(preserved["source_review"])
	if review == nil {
		return append(blockers, "Imported combined Java Vuser draft is missing its preserved Java source review; re-import the archive before authoring approval.")
	}
	accountedSourceFiles := len(review.SourceFiles) + len(review.OmittedSourceFiles)
	if review.TotalSourceFiles <= 0 || review.TotalSourceFiles != accountedSourceFiles {
		blockers = append(blockers, "Imported combined Java Vuser source-review file counts are incomplete; re-import the archive before authoring approval.")
	}
	manifest, manifestErr := importedJavaVuserSourceManifest(preserved)
	if manifestErr != nil {
		blockers = append(blockers, "Imported combined Java Vuser draft is missing a valid immutable full-source manifest; re-import the archive before authoring approval.")
		return blockers
	}
	if issue := matchJavaVuserPreviewToSourceManifest(review, manifest); issue != "" {
		blockers = append(blockers, issue)
	}
	return blockers
}

func importedJavaVuserSourceManifest(preserved map[string]any) (*enterprisearchive.JavaVuserSourceManifest, error) {
	if len(preserved) == 0 {
		return nil, fmt.Errorf("preserved import evidence is missing")
	}
	artifactValue, ok := preserved["review_artifact"]
	if !ok || artifactValue == nil {
		return nil, fmt.Errorf("review artifact is missing")
	}
	// Review artifacts cross JSON/storage boundaries before approval. Decode
	// through JSON here so typed converter values and ordinary map values follow
	// the same validation path.
	data, err := json.Marshal(artifactValue)
	if err != nil {
		return nil, fmt.Errorf("encode review artifact: %w", err)
	}
	var artifact struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(data, &artifact); err != nil {
		return nil, fmt.Errorf("decode review artifact: %w", err)
	}
	if len(artifact.Metadata) == 0 {
		return nil, fmt.Errorf("review artifact metadata is missing")
	}
	return enterprisearchive.DecodeJavaVuserSourceManifest(
		artifact.Metadata[enterprisearchive.JavaVuserSourceManifestMetadataKey],
	)
}

func matchJavaVuserPreviewToSourceManifest(
	review *enterprisearchive.SourceReview,
	manifest *enterprisearchive.JavaVuserSourceManifest,
) string {
	if review == nil || manifest == nil {
		return "Imported combined Java Vuser source-review evidence cannot be matched to its immutable full-source manifest; re-import the archive before authoring approval."
	}
	manifestFiles := make(map[string]enterprisearchive.JavaVuserSourceManifestFile, len(manifest.Files))
	for _, file := range manifest.Files {
		path, ok := javaVuserReviewPath(file.Path)
		if !ok {
			return "Imported combined Java Vuser full-source manifest contains a non-canonical Java path; re-import the archive before authoring approval."
		}
		manifestFiles[path] = file
	}
	reviewFiles := make(map[string]struct{}, len(review.SourceFiles)+len(review.OmittedSourceFiles))
	for _, sourceFile := range review.SourceFiles {
		if !strings.EqualFold(filepath.Ext(strings.TrimSpace(sourceFile.Path)), ".java") {
			continue
		}
		path, ok := javaVuserReviewPath(sourceFile.Path)
		if !ok {
			return "Imported combined Java Vuser source-review evidence contains a non-canonical Java path; re-import the archive before authoring approval."
		}
		if _, duplicate := reviewFiles[path]; duplicate {
			return "Imported combined Java Vuser source-review evidence contains duplicate Java paths; re-import the archive before authoring approval."
		}
		reviewFiles[path] = struct{}{}
		if manifestFile, exists := manifestFiles[path]; exists && int64(sourceFile.OriginalBytes) != manifestFile.Bytes {
			return "Imported combined Java Vuser source-review byte counts do not match its immutable full-source manifest; re-import the archive before authoring approval."
		}
	}
	for _, omittedPath := range review.OmittedSourceFiles {
		if !strings.EqualFold(filepath.Ext(strings.TrimSpace(omittedPath)), ".java") {
			continue
		}
		path, ok := javaVuserReviewPath(omittedPath)
		if !ok {
			return "Imported combined Java Vuser source-review evidence contains a non-canonical Java path; re-import the archive before authoring approval."
		}
		if _, duplicate := reviewFiles[path]; duplicate {
			return "Imported combined Java Vuser source-review evidence contains duplicate Java paths; re-import the archive before authoring approval."
		}
		reviewFiles[path] = struct{}{}
	}
	if len(reviewFiles) == 0 || len(reviewFiles) != len(manifestFiles) {
		return "Imported combined Java Vuser source-review paths do not match its immutable full-source manifest; re-import the archive before authoring approval."
	}
	for path := range manifestFiles {
		if _, exists := reviewFiles[path]; !exists {
			return "Imported combined Java Vuser source-review paths do not match its immutable full-source manifest; re-import the archive before authoring approval."
		}
	}
	return ""
}

func javaVuserReviewPath(value string) (string, bool) {
	canonical, ok := enterprisearchive.CanonicalJavaVuserSourcePath(strings.TrimSpace(value))
	if !ok {
		return "", false
	}
	return strings.ToLower(canonical), true
}

// isGovernedJavaVuserDraft identifies a Java script whose executable work is
// deliberately deferred to the Java Vuser runner. Runner availability is a
// launch concern; it is not evidence that source behavior was dropped during
// import.
func isGovernedJavaVuserDraft(script *dsl.Script) bool {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
		return false
	}
	return javaVuserRunActionPresent(script.Init) ||
		javaVuserRunActionPresent(script.Actions) ||
		javaVuserRunActionPresent(script.Teardown)
}

func javaVuserRunActionPresent(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if javaVuserRunActionPresent(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}

func filterJavaVuserRunnerClassificationBlockers(blockers []string) []string {
	filtered := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		if isJavaVuserRunnerClassificationBlocker(blocker) {
			continue
		}
		filtered = append(filtered, blocker)
	}
	return filtered
}

func isJavaVuserRunnerClassificationBlocker(blocker string) bool {
	normalized := strings.ToLower(strings.TrimSpace(blocker))
	return strings.Contains(normalized, "source protocol") &&
		strings.Contains(normalized, string(enterprisearchive.ImportModeExternalRunnerRequired)) &&
		strings.Contains(normalized, "cannot be promoted")
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
