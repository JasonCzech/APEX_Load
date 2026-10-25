package importreview

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"path/filepath"
	"regexp"
	"strings"
)

const JavaVuserCombinedImportReviewKey = "java_vuser_combined_import_review_v1"

func ScriptImportPromotionBlockers(script *dsl.Script) []string {
	importedJava := IsGovernedJavaVuserDraft(script) &&
		(IsCombinedJavaVuserPhaseDraft(script) || JavaVuserHasCombinedImportReviewMarker(script)) &&
		JavaVuserHasEnterpriseArchiveProvenance(script)
	var preserved map[string]any
	if script != nil && script.Metadata != nil && script.Metadata.Import != nil {
		preserved = script.Metadata.Import.Preserved
	}
	report := FidelityReportValue(preserved["fidelity_report"])
	blockers := ImportedJavaVuserReviewEvidenceBlockers(report, preserved, importedJava)
	if report == nil {
		return UniqueSortedStrings(blockers)
	}
	reportBlocked := strings.EqualFold(strings.TrimSpace(report.PromotionStatus), enterprisearchive.ImportPromotionBlocked)
	// A server-produced report never carries concrete blockers while claiming
	// promotable status. For an imported Java draft, retain those blockers even
	// if a client edits only the status field before authoring approval.
	if !reportBlocked && !(importedJava && len(report.Blockers) > 0) {
		return UniqueSortedStrings(blockers)
	}
	reportBlockers := append([]string(nil), report.Blockers...)
	if IsGovernedJavaVuserDraft(script) {
		reportBlockers = FilterJavaVuserRunnerClassificationBlockers(reportBlockers)
	}
	if len(reportBlockers) == 0 && len(report.Blockers) > 0 {
		return UniqueSortedStrings(blockers)
	}
	if len(reportBlockers) == 0 {
		reportBlockers = []string{unnamedPromotionBlocker(report.Warnings)}
	}
	return UniqueSortedStrings(append(blockers, reportBlockers...))
}

// unnamedPromotionBlocker states why a blocked report that names no blocker
// holds promotion. A guided repair keeps a report blocked while review codes
// remain, and lists them as its warnings, so a launch refusal names them
// rather than only saying the script is held. A converter report's warnings
// are free text that can quote the source, and a credential-preserving import
// stores them unmasked, so only review-code entries are ever quoted.
func unnamedPromotionBlocker(warnings []string) string {
	named := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning = strings.TrimSpace(warning); promotionReviewCode.MatchString(warning) {
			named = append(named, warning)
		}
	}
	if named = UniqueSortedStrings(named); len(named) > 0 {
		return "Imported script needs import review before promotion: " + strings.Join(named, ", ") + "."
	}
	return "Imported script is marked not promotable to executable."
}

// promotionReviewCode matches an import review finding code, such as
// import_review_required or missing_producer:<name>. Converter sentences, and
// the quoted source text a warning may hold, never match.
var promotionReviewCode = regexp.MustCompile(`^[a-z][a-z0-9_]*(:\S+)?$`)

func JavaVuserHasCombinedImportReviewMarker(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	return BoolValue(script.Metadata.Import.Preserved[JavaVuserCombinedImportReviewKey])
}

func IsCombinedJavaVuserPhaseDraft(script *dsl.Script) bool {
	return script != nil &&
		strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") &&
		(JavaVuserActionsRequirePhaseLifecycle(script.Init) ||
			JavaVuserActionsRequirePhaseLifecycle(script.Actions) ||
			JavaVuserActionsRequirePhaseLifecycle(script.Teardown))
}

func JavaVuserHasEnterpriseArchiveProvenance(script *dsl.Script) bool {
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
		if JavaVuserActionsHaveEnterpriseArchiveRef(actions) {
			return true
		}
	}
	return false
}

func JavaVuserActionsHaveEnterpriseArchiveRef(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") &&
			strings.EqualFold(strings.TrimSpace(fmt.Sprint(action.Params["source_archive_ref"])), "enterprise_script_archive") {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if JavaVuserActionsHaveEnterpriseArchiveRef(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}

func ImportedJavaVuserReviewEvidenceBlockers(
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

	review := SourceReviewValue(preserved["source_review"])
	if review == nil {
		return append(blockers, "Imported combined Java Vuser draft is missing its preserved Java source review; re-import the archive before authoring approval.")
	}
	accountedSourceFiles := len(review.SourceFiles) + len(review.OmittedSourceFiles)
	if review.TotalSourceFiles <= 0 || review.TotalSourceFiles != accountedSourceFiles {
		blockers = append(blockers, "Imported combined Java Vuser source-review file counts are incomplete; re-import the archive before authoring approval.")
	}
	manifest, manifestErr := ImportedJavaVuserSourceManifest(preserved)
	if manifestErr != nil {
		blockers = append(blockers, "Imported combined Java Vuser draft is missing a valid immutable full-source manifest; re-import the archive before authoring approval.")
		return blockers
	}
	if issue := MatchJavaVuserPreviewToSourceManifest(review, manifest); issue != "" {
		blockers = append(blockers, issue)
	}
	return blockers
}

func ImportedJavaVuserSourceManifest(preserved map[string]any) (*enterprisearchive.JavaVuserSourceManifest, error) {
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

func MatchJavaVuserPreviewToSourceManifest(
	review *enterprisearchive.SourceReview,
	manifest *enterprisearchive.JavaVuserSourceManifest,
) string {
	if review == nil || manifest == nil {
		return "Imported combined Java Vuser source-review evidence cannot be matched to its immutable full-source manifest; re-import the archive before authoring approval."
	}
	manifestFiles := make(map[string]enterprisearchive.JavaVuserSourceManifestFile, len(manifest.Files))
	for _, file := range manifest.Files {
		path, ok := JavaVuserReviewPath(file.Path)
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
		path, ok := JavaVuserReviewPath(sourceFile.Path)
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
		path, ok := JavaVuserReviewPath(omittedPath)
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

func JavaVuserReviewPath(value string) (string, bool) {
	canonical, ok := enterprisearchive.CanonicalJavaVuserSourcePath(strings.TrimSpace(value))
	if !ok {
		return "", false
	}
	return strings.ToLower(canonical), true
}

func IsGovernedJavaVuserDraft(script *dsl.Script) bool {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
		return false
	}
	return JavaVuserRunActionPresent(script.Init) ||
		JavaVuserRunActionPresent(script.Actions) ||
		JavaVuserRunActionPresent(script.Teardown)
}

func JavaVuserRunActionPresent(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if JavaVuserRunActionPresent(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}

func FilterJavaVuserRunnerClassificationBlockers(blockers []string) []string {
	filtered := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		if IsJavaVuserRunnerClassificationBlocker(blocker) {
			continue
		}
		filtered = append(filtered, blocker)
	}
	return filtered
}

func IsJavaVuserRunnerClassificationBlocker(blocker string) bool {
	normalized := strings.ToLower(strings.TrimSpace(blocker))
	return strings.Contains(normalized, "source protocol") &&
		strings.Contains(normalized, string(enterprisearchive.ImportModeExternalRunnerRequired)) &&
		strings.Contains(normalized, "cannot be promoted")
}
