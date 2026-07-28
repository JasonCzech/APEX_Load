package enterprisearchive

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// RuntimeMigrationIssue records a source setting that could not be mapped
// exactly. It is converted into the existing partial parity/readiness model.
type RuntimeMigrationIssue struct {
	ID           string
	Area         string
	Status       RuntimeSettingStatus
	Notes        string
	RuntimeFiles []string
	// sourceKey is the canonical field within ID for parser-generated findings.
	// It lets a later synonymous/case-fold conflict supersede only the stale
	// finding for that field instead of deleting independent findings that share
	// a broad parity ID such as http_transport.
	sourceKey string
	// profileSelection distinguishes cfg/usp identity conflicts from stale cfg
	// pacing values that a selected USP authoritatively replaces.
	profileSelection bool
}

func resolveRuntimeConfig(files map[string]string, project *ProjectConfig) (string, *RuntimeConfig) {
	cfgName, cfgContent, cfgIssue := findRuntimeConfig(files, project)
	// Parse even an empty selected cfg (and use the same parser for USP-only
	// archives) so HTTP version, keep-alive, redirects, and other safe defaults
	// do not depend on whether the cfg happened to contain a byte.
	rc := ParseRuntimeCfg(cfgContent)
	setRuntimeIssueFileRange(rc, 0, cfgName)
	if cfgName != "" {
		appendRuntimeFile(rc, cfgName)
	} else if cfgIssue != "" {
		// Retain the attempted pointer when no cfg was selected. It will not
		// contribute source sections, but it makes the partial finding's
		// provenance explicit.
		appendRuntimeFile(rc, configuredRuntimeCfgPointer(project))
	}
	if cfgIssue != "" {
		issueFiles := configuredRuntimeCfgProfileFiles(project)
		if attemptedPointer := configuredRuntimeCfgPointer(project); attemptedPointer != "" {
			_, generalConflict := projectRuntimePointerConflict(project, "General", "DefaultCfg", "RuntimeSettings", "RuntimeFile")
			_, profileConflict := projectRuntimePointerConflict(project, "CfgFiles")
			if generalConflict || profileConflict {
				issueFiles = mergeRuntimeFileLists(issueFiles, []string{attemptedPointer})
			} else {
				issueFiles = []string{attemptedPointer}
			}
		}
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "runtime_profile", Area: "Runtime Settings", Notes: cfgIssue,
			RuntimeFiles: issueFiles,
		})
		// The cfg and its run-logic file form one selected runtime profile.
		// Once cfg resolution fails, choosing default.usp or the sole USP would
		// splice pacing from an unrelated profile into otherwise-safe defaults.
		return "", rc
	}

	uspName, issue := resolveRunLogicFile(files, project, cfgName, cfgContent)
	if issue != "" {
		issueFiles := runtimeRunLogicSelectionEvidence(
			project,
			cfgName,
			uspName,
		)
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing", Notes: issue,
			RuntimeFiles:     issueFiles,
			profileSelection: true,
		})
	}
	if uspName == "" {
		if issue != "" {
			resetRuntimePacing(rc)
			rc.Iterations = 1
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "run_logic_iterations", Area: "Run Logic",
				Notes: "No unambiguous run-logic profile was selected; using 1 iteration.",
			})
		}
		if cfgName == "" && len(rc.RuntimeIssues) == 0 {
			return "", nil
		}
		return cfgName, rc
	}
	resolvedUSPName, uspContent, ambiguous := findRuntimeArchiveFile(files, uspName)
	if len(ambiguous) > 0 {
		appendRuntimeFile(rc, uspName)
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("Selected run-logic file %q matched multiple archive files (%s); using APEX pacing defaults.", uspName, strings.Join(ambiguous, ", ")),
		})
		resetRuntimePacing(rc)
		rc.Iterations = 1
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_iterations", Area: "Run Logic",
			Notes: fmt.Sprintf("Selected run-logic file %q was ambiguous; using 1 iteration.", uspName),
		})
		return cfgName, rc
	}
	if resolvedUSPName == "" {
		appendRuntimeFile(rc, uspName)
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("Selected run-logic file %q was not found; using APEX pacing defaults.", uspName),
		})
		resetRuntimePacing(rc)
		rc.Iterations = 1
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_iterations", Area: "Run Logic",
			Notes: fmt.Sprintf("Selected run-logic file %q was not found; using 1 iteration.", uspName),
		})
		return cfgName, rc
	}
	appendRuntimeFile(rc, resolvedUSPName)
	discardCfgRunLogicIssuesOverriddenByUSP(rc, cfgName)
	issueStart := len(rc.RuntimeIssues)
	applyRuntimeUSP(rc, resolvedUSPName, uspContent)
	setRuntimeIssueFileRange(rc, issueStart, resolvedUSPName)
	return cfgName, rc
}

func setRuntimeIssueFileRange(rc *RuntimeConfig, start int, fileName string) {
	if rc == nil || strings.TrimSpace(fileName) == "" {
		return
	}
	if start < 0 {
		start = 0
	}
	for index := start; index < len(rc.RuntimeIssues); index++ {
		if len(rc.RuntimeIssues[index].RuntimeFiles) == 0 {
			rc.RuntimeIssues[index].RuntimeFiles = []string{normalizeRuntimeArchivePath(fileName)}
		}
	}
}

func appendRuntimeFile(rc *RuntimeConfig, name string) {
	if rc == nil {
		return
	}
	name = normalizeRuntimeArchivePath(name)
	if name == "" {
		return
	}
	for _, existing := range rc.RuntimeFiles {
		if strings.EqualFold(normalizeRuntimeArchivePath(existing), name) {
			return
		}
	}
	rc.RuntimeFiles = append(rc.RuntimeFiles, name)
}

func discardCfgRunLogicIssuesOverriddenByUSP(rc *RuntimeConfig, cfgName string) {
	if rc == nil || strings.TrimSpace(cfgName) == "" || len(rc.RuntimeIssues) == 0 {
		return
	}
	kept := rc.RuntimeIssues[:0]
	for _, issue := range rc.RuntimeIssues {
		cfgRunLogicIssue := issue.ID == "pacing" || issue.ID == "run_logic_iterations"
		fromSelectedCfg := len(issue.RuntimeFiles) == 1 && sameRuntimeFile(issue.RuntimeFiles[0], cfgName)
		if cfgRunLogicIssue && fromSelectedCfg && !issue.profileSelection {
			continue
		}
		kept = append(kept, issue)
	}
	rc.RuntimeIssues = kept
}

func resolveRunLogicFile(files map[string]string, project *ProjectConfig, cfgName, cfgContent string) (string, string) {
	if conflict, ok := projectRuntimePointerConflict(project, "RunLogicFiles"); ok {
		return "", fmt.Sprintf(
			"Conflicting case-insensitive .usr run-logic profile declarations (%s); using APEX pacing defaults and requiring review.",
			runtimeConfigConflictEvidence(conflict),
		)
	}
	for _, conflict := range runtimeConfigCaseFoldConflicts(cfgContent) {
		if runtimeConfigConflictMatches(
			conflict,
			[]string{"General"},
			[]string{"DefaultRunLogic"},
		) {
			return "", fmt.Sprintf(
				"Run-logic profile conflict in the active cfg (%s); using APEX pacing defaults and requiring review.",
				runtimeConfigConflictEvidence(conflict),
			)
		}
	}
	sections := ParseConfigSections(cfgContent)
	direct, _ := sectionValueCaseInsensitive(findSection(sections, "General"), "DefaultRunLogic")
	direct = strings.TrimSpace(direct)
	var profiles []string
	if project != nil {
		for name, candidate := range project.CfgProfiles {
			if sameRuntimeFile(candidate, cfgName) {
				profiles = append(profiles, name)
			}
		}
	}
	sort.Strings(profiles)
	mappedByFile := make(map[string]string)
	for _, profile := range profiles {
		if candidate := strings.TrimSpace(runtimeProfileValue(project.RunLogicProfiles, profile)); candidate != "" {
			normalized := normalizeRuntimeArchivePath(candidate)
			mappedByFile[strings.ToLower(normalized)] = normalized
		}
	}
	var mappedFiles []string
	for _, candidate := range mappedByFile {
		mappedFiles = append(mappedFiles, candidate)
	}
	sort.Slice(mappedFiles, func(i, j int) bool { return strings.ToLower(mappedFiles[i]) < strings.ToLower(mappedFiles[j]) })
	mapped := ""
	if len(mappedFiles) == 1 {
		mapped = mappedFiles[0]
	}
	if direct != "" {
		if len(mappedFiles) > 1 ||
			(mapped != "" && !runtimePointersIdentifySameArchiveFile(files, direct, mapped)) {
			return "", fmt.Sprintf("Run-logic profile conflict: matching .usr profiles point to %q while the active cfg points to %q; using APEX pacing defaults and requiring review.", strings.Join(mappedFiles, ", "), direct)
		}
		return direct, ""
	}
	if len(mappedFiles) > 1 {
		return "", fmt.Sprintf("Multiple matching run-logic profiles point to different files (%s); using APEX pacing defaults and requiring review.", strings.Join(mappedFiles, ", "))
	}
	if mapped != "" {
		return mapped, ""
	}

	var uspNames []string
	for name := range files {
		normalized := normalizeRuntimeArchivePath(name)
		if strings.HasSuffix(strings.ToLower(normalized), ".usp") {
			uspNames = append(uspNames, normalized)
		}
	}
	sort.Slice(uspNames, func(i, j int) bool { return strings.ToLower(uspNames[i]) < strings.ToLower(uspNames[j]) })
	var defaultNames []string
	for _, name := range uspNames {
		if strings.EqualFold(runtimeArchiveBase(name), "default.usp") {
			defaultNames = append(defaultNames, name)
		}
	}
	if len(defaultNames) == 1 {
		return defaultNames[0], ""
	}
	if len(defaultNames) > 1 {
		return "", fmt.Sprintf("Multiple default run-logic files (%s) are present without an active profile; using APEX pacing defaults and requiring review.", strings.Join(defaultNames, ", "))
	}
	if len(uspNames) == 1 {
		return uspNames[0], ""
	}
	if len(uspNames) > 1 {
		return "", fmt.Sprintf("Multiple run-logic files (%s) are present without an active profile; using APEX pacing defaults and requiring review.", strings.Join(uspNames, ", "))
	}
	return "", ""
}

// runtimeRunLogicSelectionEvidence names the files that actually contributed to
// the resolved run-logic settings: the selected cfg and, when one survived the
// conflict, the selected USP. Rejected pointers are named in the issue Notes
// instead — listing them here would claim provenance for values that were never
// read, which is what the migration report renders as the source of a setting.
func runtimeRunLogicSelectionEvidence(
	project *ProjectConfig,
	cfgName string,
	uspName string,
) []string {
	files := mergeRuntimeFileLists(nil, []string{cfgName})
	files = mergeRuntimeFileLists(files, []string{uspName})
	return mergeRuntimeFileLists(files, configuredRunLogicConflictFiles(project))
}

func runtimeProfileValue(profiles map[string]string, name string) string {
	if value, ok := profiles[name]; ok {
		return value
	}
	for candidate, value := range profiles {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			return value
		}
	}
	return ""
}

func runtimePointersIdentifySameArchiveFile(files map[string]string, left, right string) bool {
	leftNormalized := normalizeRuntimeArchivePath(left)
	rightNormalized := normalizeRuntimeArchivePath(right)
	if leftNormalized == "" || rightNormalized == "" {
		return false
	}
	if strings.EqualFold(leftNormalized, rightNormalized) {
		return true
	}
	leftName, _, leftAmbiguous := findRuntimeArchiveFile(files, left)
	rightName, _, rightAmbiguous := findRuntimeArchiveFile(files, right)
	return len(leftAmbiguous) == 0 &&
		len(rightAmbiguous) == 0 &&
		leftName != "" &&
		rightName != "" &&
		strings.EqualFold(leftName, rightName)
}

func sameRuntimeFile(left, right string) bool {
	left = normalizeRuntimeArchivePath(left)
	right = normalizeRuntimeArchivePath(right)
	if left == "" || right == "" {
		return false
	}
	if strings.EqualFold(left, right) {
		return true
	}
	// left is a source pointer and right is the resolved/selected file. An
	// unqualified pointer may match a unique archive basename, but a qualified
	// pointer is exact: profiles/a/active.cfg must never match root active.cfg.
	if strings.Contains(left, "/") {
		return false
	}
	return strings.EqualFold(runtimeArchiveBase(left), runtimeArchiveBase(right))
}

func applyRuntimeUSP(rc *RuntimeConfig, fileName, content string) {
	resetRuntimePacing(rc)
	section := findSection(ParseConfigSections(content), "RunLogicRunRoot")
	if len(section) == 0 {
		rc.Iterations = 1
		rc.PacingType = "none"
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("%s has no [RunLogicRunRoot] section; using APEX pacing defaults.", fileName),
		})
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_iterations", Area: "Run Logic",
			Notes: fmt.Sprintf("%s has no [RunLogicRunRoot] section; using 1 iteration.", fileName),
		})
		return
	}
	valueOf := func(key string) string {
		value, _ := sectionValueCaseInsensitive(section, key)
		return value
	}
	rawIterations := strings.TrimSpace(valueOf("RunLogicNumOfIterations"))
	value, err := strconv.Atoi(rawIterations)
	if rawIterations == "" || err != nil || value < 1 || value > dsl.MaxRunIterations {
		rc.Iterations = 1
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_iterations", Area: "Run Logic",
			Notes: fmt.Sprintf("%s RunLogicNumOfIterations=%q is missing, invalid, or out of range; using 1 iteration.", fileName, rawIterations),
		})
	} else {
		rc.Iterations = value
	}

	mode := strings.TrimSpace(valueOf("RunLogicPaceType"))
	rc.PacingType = "none"
	switch strings.ToUpper(mode) {
	case "ASAP":
		rc.PacingType = "none"
	case "CONST":
		rc.PacingType = "fixed_interval"
		var ok bool
		rc.IntervalMs, ok = runtimeSecondsToMS(rc, fileName, "RunLogicPaceConstTime", valueOf("RunLogicPaceConstTime"))
		if !ok {
			resetRuntimePacing(rc)
		}
	case "RANDOM":
		rc.PacingType = "random_interval"
		var lowOK, highOK bool
		rc.MinIntervalMs, lowOK = runtimeSecondsToMS(rc, fileName, "RunLogicRandomPaceMin", valueOf("RunLogicRandomPaceMin"))
		rc.MaxIntervalMs, highOK = runtimeSecondsToMS(rc, fileName, "RunLogicRandomPaceMax", valueOf("RunLogicRandomPaceMax"))
		if !lowOK || !highOK || !validateRuntimeBounds(rc, fileName, "random interval", &rc.MinIntervalMs, &rc.MaxIntervalMs) {
			resetRuntimePacing(rc)
		}
	case "CONSTAFTER":
		rc.PacingType = "fixed_delay"
		var ok bool
		rc.DelayMs, ok = runtimeSecondsToMS(rc, fileName, "RunLogicPaceConstAfterTime", valueOf("RunLogicPaceConstAfterTime"))
		if !ok {
			resetRuntimePacing(rc)
		}
	case "AFTER":
		rc.PacingType = "random_delay"
		var lowOK, highOK bool
		rc.MinDelayMs, lowOK = runtimeSecondsToMS(rc, fileName, "RunLogicAfterPaceMin", valueOf("RunLogicAfterPaceMin"))
		rc.MaxDelayMs, highOK = runtimeSecondsToMS(rc, fileName, "RunLogicAfterPaceMax", valueOf("RunLogicAfterPaceMax"))
		if !lowOK || !highOK || !validateRuntimeBounds(rc, fileName, "random delay", &rc.MinDelayMs, &rc.MaxDelayMs) {
			resetRuntimePacing(rc)
		}
	default:
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("%s RunLogicPaceType=%q is unsupported; using no pacing.", fileName, mode),
		})
	}
	applyRuntimeUSPCaseFoldConflicts(rc, fileName, content)
}

func applyRuntimeUSPCaseFoldConflicts(rc *RuntimeConfig, fileName, content string) {
	if rc == nil {
		return
	}
	selectedPacingType := rc.PacingType
	for _, conflict := range runtimeConfigCaseFoldConflicts(content) {
		if runtimeConfigConflictMatches(
			conflict,
			[]string{"RunLogicRunRoot"},
			[]string{"RunLogicNumOfIterations"},
		) {
			rc.Iterations = 1
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "run_logic_iterations", Area: "Run Logic",
				Notes: fmt.Sprintf(
					"%s has conflicting case-insensitive iteration declarations (%s); using 1 iteration and requiring review.",
					fileName,
					runtimeConfigConflictEvidence(conflict),
				),
			})
			continue
		}

		modeConflict := runtimeConfigConflictMatches(
			conflict,
			[]string{"RunLogicRunRoot"},
			[]string{"RunLogicPaceType"},
		)
		activeValueConflict := false
		switch selectedPacingType {
		case "fixed_interval":
			activeValueConflict = runtimeConfigConflictMatches(
				conflict,
				[]string{"RunLogicRunRoot"},
				[]string{"RunLogicPaceConstTime"},
			)
		case "random_interval":
			activeValueConflict = runtimeConfigConflictMatches(
				conflict,
				[]string{"RunLogicRunRoot"},
				[]string{"RunLogicRandomPaceMin", "RunLogicRandomPaceMax"},
			)
		case "fixed_delay":
			activeValueConflict = runtimeConfigConflictMatches(
				conflict,
				[]string{"RunLogicRunRoot"},
				[]string{"RunLogicPaceConstAfterTime"},
			)
		case "random_delay":
			activeValueConflict = runtimeConfigConflictMatches(
				conflict,
				[]string{"RunLogicRunRoot"},
				[]string{"RunLogicAfterPaceMin", "RunLogicAfterPaceMax"},
			)
		}
		if !modeConflict && !activeValueConflict {
			continue
		}
		resetRuntimePacing(rc)
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf(
				"%s has conflicting case-insensitive pacing declarations (%s); using no pacing and requiring review.",
				fileName,
				runtimeConfigConflictEvidence(conflict),
			),
		})
	}
}

func runtimeSecondsToMS(rc *RuntimeConfig, fileName, field, raw string) (int, bool) {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds*1000 > dsl.MaxConfiguredProtocolTimeoutMS {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("%s %s=%q is invalid; using the APEX default value.", fileName, field, raw),
		})
		return 0, false
	}
	return int(math.Round(seconds * 1000)), true
}

func validateRuntimeBounds(rc *RuntimeConfig, fileName, label string, low, high *int) bool {
	if *high < *low {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("%s has invalid %s bounds (%dms..%dms); using APEX defaults.", fileName, label, *low, *high),
		})
		*low, *high = 0, 0
		return false
	}
	return true
}

func resetRuntimePacing(rc *RuntimeConfig) {
	rc.PacingType = "none"
	rc.DelayMs = 0
	rc.MinDelayMs = 0
	rc.MaxDelayMs = 0
	rc.IntervalMs = 0
	rc.MinIntervalMs = 0
	rc.MaxIntervalMs = 0
}

func applyRuntimeMigrationIssues(parity []RuntimeSettingParityEntry, rc *RuntimeConfig) []RuntimeSettingParityEntry {
	parity = applyRuntimeSemanticStatuses(parity, rc)
	if rc == nil || len(rc.RuntimeIssues) == 0 {
		return parity
	}
	issueFilesByID := make(map[string][]string)
	for _, issue := range rc.RuntimeIssues {
		issueStatus := issue.Status
		if issueStatus == "" {
			issueStatus = RuntimeSettingStatusPartial
		}
		issueFiles := issue.RuntimeFiles
		if len(issueFiles) == 0 {
			issueFiles = rc.RuntimeFiles
		}
		issueFilesByID[issue.ID] = mergeRuntimeFileLists(issueFilesByID[issue.ID], issueFiles)
		mergedIssueFiles := issueFilesByID[issue.ID]
		found := false
		for index := range parity {
			if parity[index].ID != issue.ID {
				continue
			}
			if parity[index].Status != RuntimeSettingStatusBlocked ||
				issueStatus == RuntimeSettingStatusBlocked {
				parity[index].Status = issueStatus
			}
			if parity[index].Notes == "" {
				parity[index].Notes = issue.Notes
			} else if !strings.Contains(parity[index].Notes, issue.Notes) {
				parity[index].Notes += " " + issue.Notes
			}
			parity[index].RuntimeFiles = orderRuntimeFilesByProfile(
				mergeRuntimeFileLists(parity[index].RuntimeFiles, mergedIssueFiles),
				rc.RuntimeFiles,
			)
			found = true
		}
		if !found {
			parity = append(parity, RuntimeSettingParityEntry{
				ID: issue.ID, Area: issue.Area, Concept: issue.Area,
				Status: issueStatus, Notes: issue.Notes,
				RuntimeFiles: orderRuntimeFilesByProfile(
					append([]string(nil), mergedIssueFiles...),
					rc.RuntimeFiles,
				),
			})
		}
	}
	return parity
}

func mergeRuntimeFileLists(existing, additional []string) []string {
	merged := make([]string, 0, len(existing)+len(additional))
	appendFile := func(name string) {
		name = normalizeRuntimeArchivePath(name)
		if name == "" {
			return
		}
		for _, current := range merged {
			if strings.EqualFold(current, name) {
				return
			}
		}
		merged = append(merged, name)
	}
	for _, name := range existing {
		appendFile(name)
	}
	for _, name := range additional {
		appendFile(name)
	}
	return merged
}

// orderRuntimeFilesByProfile keeps selected profile provenance stable without
// widening the union. In particular, the active cfg should precede its USP even
// when the parity matcher initially found only the USP.
func orderRuntimeFilesByProfile(files, profileOrder []string) []string {
	if len(files) < 2 || len(profileOrder) == 0 {
		return files
	}
	ordered := make([]string, 0, len(files))
	used := make([]bool, len(files))
	for _, preferred := range profileOrder {
		preferred = normalizeRuntimeArchivePath(preferred)
		for index, file := range files {
			if used[index] || !strings.EqualFold(normalizeRuntimeArchivePath(file), preferred) {
				continue
			}
			ordered = append(ordered, file)
			used[index] = true
			break
		}
	}
	for index, file := range files {
		if !used[index] {
			ordered = append(ordered, file)
		}
	}
	return ordered
}

func applyRuntimeSemanticStatuses(parity []RuntimeSettingParityEntry, rc *RuntimeConfig) []RuntimeSettingParityEntry {
	if rc == nil {
		return parity
	}
	customUserAgentSelected := false
	if emulation := rc.ToBrowserEmulationConfig(); emulation != nil {
		customUserAgentSelected = strings.EqualFold(emulation.Mode, "custom")
	}
	cacheDisabled := rc.BESimulateCache != nil && !*rc.BESimulateCache
	for index := range parity {
		switch parity[index].ID {
		case "browser_selection":
			if customUserAgentSelected {
				parity[index].Status = RuntimeSettingStatusNonApplicable
				parity[index].Notes = "Specific browser type/version/platform values are dormant because the selected custom User-Agent is authoritative."
			}
		case "browser_cache_content_urls", "browser_check_newer_versions":
			if cacheDisabled {
				parity[index].Status = RuntimeSettingStatusNonApplicable
				parity[index].Notes = "The source child setting is dormant because browser-cache simulation is disabled."
			}
		}
	}
	return parity
}
