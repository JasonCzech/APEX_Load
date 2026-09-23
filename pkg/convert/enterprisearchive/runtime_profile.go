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
	noteRuntimeUSPErrorHandler(rc, resolvedUSPName, uspContent, project, files)
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
	rc.selectedRunLogicFile = normalizeRuntimeArchivePath(fileName)
	resetRuntimePacing(rc)
	uspSections := ParseConfigSections(content)
	section := findSection(uspSections, "RunLogicRunRoot")
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

	// RunLogicRunMode is the run-group block scheduling (Sequential/Random).
	// Preserve recognized source vocabulary in the draft, but keep its support
	// classification compatibility-only unless the complete flat profile is
	// separately verified against the native lifecycle. The engine does not consume
	// BlockScheduling. The selected run-logic profile is authoritative, so a
	// recognized value still replaces any cfg-derived migration evidence.
	runMode := strings.TrimSpace(valueOf("RunLogicRunMode"))
	switch strings.ToLower(runMode) {
	case "":
		// absent — keep whatever the cfg configured
	case "sequential":
		rc.BlockScheduling = "sequential"
	case "random":
		rc.BlockScheduling = "random"
	default:
		// The selected USP is authoritative. Do not retain a stale cfg value
		// when its run mode cannot be represented safely.
		rc.BlockScheduling = ""
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_block_scheduling", Area: "Run Logic",
			Notes: fmt.Sprintf("%s RunLogicRunMode=%q is unsupported; leaving block_scheduling unset while the engine retains its fixed action order, and requiring review.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewEvidence(runMode)),
		})
	}

	applyRuntimeUSPActionSequence(rc, fileName, uspSections, valueOf("RunLogicActionOrder"))

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

// applyRuntimeUSPActionSequence maps the source lifecycle declaration
// ([Profile Actions] plus the run-root RunLogicActionOrder) onto
// run_logic.action_sequence as migration evidence. The current engine does not
// consume this field and instead runs its fixed init/action/teardown lifecycle,
// so only a separately verified flat profile is reported as executed. Only the standard source
// lifecycle — optional vuser_init, at most one run action, optional vuser_end —
// is safe to preserve in this coarse vocabulary; custom multi-action orders
// fail to review instead of collapsing distinct actions into indistinguishable
// "action" entries.
func applyRuntimeUSPActionSequence(rc *RuntimeConfig, fileName string, uspSections map[string]map[string]string, runActionOrder string) {
	profileSection := findSection(uspSections, "Profile Actions")
	_, profileNamesRaw, profileNamesPresent := sectionEntryCaseInsensitive(profileSection, "Profile Actions name")
	if !profileNamesPresent {
		return
	}
	// A selected profile supersedes any cfg-authored fallback. Unsupported
	// shapes must leave the destination unset instead of retaining stale order.
	rc.ActionSequence = nil
	addReviewIssue := func(notes string) {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_action_sequence", Area: "Run Logic",
			Status: RuntimeSettingStatusCompatibilityOnly,
			Notes:  notes,
		})
	}
	profileNames := splitSourceActionNames(profileNamesRaw)
	if len(profileNames) == 0 {
		addReviewIssue(fmt.Sprintf("%s declares an empty [Profile Actions] action list; leaving action_sequence unset and requiring review.", boundedRuntimeReviewEvidence(fileName)))
		return
	}
	runActions := splitSourceActionNames(runActionOrder)
	if len(runActions) > 1 {
		addReviewIssue(fmt.Sprintf("%s declares a multi-action run order (%s) that config.run_logic.action_sequence cannot express per-action; the converted script keeps its imported action order and the source order requires review.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewList(runActions)))
		return
	}
	sequence := make([]string, 0, len(profileNames))
	runActionNames := make([]string, 0, 1)
	initCount := 0
	endCount := 0
	for index, name := range profileNames {
		switch strings.ToLower(name) {
		case "vuser_init":
			initCount++
			if index != 0 {
				addReviewIssue(fmt.Sprintf("%s declares vuser_init outside the first lifecycle position (%s); the source order requires review.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewList(profileNames)))
				return
			}
			sequence = append(sequence, "vuser_init")
		case "vuser_end":
			endCount++
			if index != len(profileNames)-1 {
				addReviewIssue(fmt.Sprintf("%s declares vuser_end outside the final lifecycle position (%s); the source order requires review.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewList(profileNames)))
				return
			}
			sequence = append(sequence, "vuser_end")
		default:
			runActionNames = append(runActionNames, name)
			sequence = append(sequence, "action")
		}
	}
	if initCount > 1 || endCount > 1 || len(runActionNames) != 1 {
		addReviewIssue(fmt.Sprintf("%s declares a non-standard lifecycle (%s) that config.run_logic.action_sequence cannot express exactly; the converted script keeps its imported action order and the source order requires review.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewList(profileNames)))
		return
	}
	if len(runActions) == 1 && !strings.EqualFold(runActions[0], runActionNames[0]) {
		addReviewIssue(fmt.Sprintf("%s RunLogicActionOrder names %q, but [Profile Actions] names run action %q; leaving action_sequence unset and requiring review.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewEvidence(runActions[0]), boundedRuntimeReviewEvidence(runActionNames[0])))
		return
	}
	rc.ActionSequence = sequence
}

// splitSourceActionNames splits a comma-separated .usp action list, keeping
// the source action names intact for evidence (no enum normalization).
func splitSourceActionNames(value string) []string {
	parts := strings.Split(value, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.Trim(strings.TrimSpace(part), `"`))
		if trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// noteRuntimeUSPErrorHandler accounts for the source error-handler run-logic
// block. APEX has no error-handler action lane, so a real handler action is
// compatibility-only; the stock scaffold block VuGen writes without a matching
// action file is recorded as non-applicable rather than a behavior gap.
func noteRuntimeUSPErrorHandler(rc *RuntimeConfig, fileName, content string, project *ProjectConfig, files map[string]string) {
	if rc == nil {
		return
	}
	for _, conflict := range runtimeConfigCaseFoldConflicts(content) {
		if !runtimeConfigConflictMatches(
			conflict,
			[]string{"RunLogicErrorHandlerRoot"},
			[]string{"RunLogicActionOrder", "MercIniTreeSons"},
		) {
			continue
		}
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_error_handler", Area: "Run Logic",
			Status: RuntimeSettingStatusCompatibilityOnly,
			Notes: fmt.Sprintf(
				"%s has conflicting case-insensitive error-handler declarations (%s); the handler cannot be proven inert and requires review.",
				boundedRuntimeReviewEvidence(fileName),
				boundedRuntimeReviewEvidence(runtimeConfigConflictEvidence(conflict)),
			),
		})
		return
	}
	sections := ParseConfigSections(content)
	root := findSection(sections, "RunLogicErrorHandlerRoot")
	if len(root) == 0 {
		return
	}
	handlerNames := splitSourceActionNames(runtimeSectionValue(root, "RunLogicActionOrder"))
	if len(handlerNames) == 0 {
		handlerNames = splitSourceActionNames(runtimeSectionValue(root, "MercIniTreeSons"))
	}
	if len(handlerNames) == 0 {
		return
	}
	actionInventory := sourceActionInventory(project, files)
	present := make([]string, 0, len(handlerNames))
	for _, name := range handlerNames {
		if _, exists := actionInventory[strings.ToLower(strings.TrimSpace(name))]; exists {
			present = append(present, name)
		}
	}
	if len(present) > 0 {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_error_handler", Area: "Run Logic",
			Status: RuntimeSettingStatusCompatibilityOnly,
			Notes:  fmt.Sprintf("%s declares an error-handler run-logic block (%s). APEX has no error-handler action lane, so the handler does not execute and requires review.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewList(present)),
		})
		return
	}
	if project == nil && files == nil {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_error_handler", Area: "Run Logic",
			Status: RuntimeSettingStatusCompatibilityOnly,
			Notes:  fmt.Sprintf("%s declares an error-handler run-logic block (%s), but no project/archive inventory was available to prove that it is inert; review is required.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewList(handlerNames)),
		})
		return
	}
	rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
		ID: "run_logic_error_handler", Area: "Run Logic",
		Status: RuntimeSettingStatusNonApplicable,
		Notes:  fmt.Sprintf("%s declares an error-handler run-logic block (%s), but the project [Actions] list carries no matching action file; the block is inert VuGen scaffolding.", boundedRuntimeReviewEvidence(fileName), boundedRuntimeReviewList(handlerNames)),
	})
}

func sourceActionInventory(project *ProjectConfig, files map[string]string) map[string]struct{} {
	inventory := make(map[string]struct{})
	add := func(candidate string) {
		for _, identity := range sourceActionDeclarationIdentities(candidate) {
			inventory[strings.ToLower(identity)] = struct{}{}
		}
	}
	if project != nil {
		for _, action := range project.SourceActionFiles {
			add(action.Function)
			add(action.Name)
		}
		for _, action := range project.ActionFiles {
			add(action)
		}
	}
	for fileName := range files {
		if isSupportedLifecycleSourceFile(fileName) {
			add(fileName)
		}
	}
	return inventory
}

func sourceActionDeclarationIdentities(candidate string) []string {
	candidate = normalizeRuntimeArchivePath(candidate)
	if candidate == "" {
		return nil
	}
	base := candidate
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	stem := strings.TrimSuffix(base, filepathExt(base))
	return []string{candidate, base, stem}
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
			[]string{"RunLogicActionOrder"},
		) || runtimeConfigConflictMatches(
			conflict,
			[]string{"Profile Actions"},
			[]string{"Profile Actions name"},
		) {
			rc.ActionSequence = nil
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "run_logic_action_sequence", Area: "Run Logic",
				Status: RuntimeSettingStatusCompatibilityOnly,
				Notes: fmt.Sprintf(
					"%s has conflicting case-insensitive action-sequence declarations (%s); leaving action_sequence unset and requiring review.",
					boundedRuntimeReviewEvidence(fileName),
					boundedRuntimeReviewEvidence(runtimeConfigConflictEvidence(conflict)),
				),
			})
			continue
		}
		if runtimeConfigConflictMatches(
			conflict,
			[]string{"RunLogicRunRoot"},
			[]string{"RunLogicRunMode"},
		) {
			// Same fail-closed stance as iterations/pacing: contradictory
			// case-insensitive run-mode declarations must not silently last-win.
			rc.BlockScheduling = ""
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "run_logic_block_scheduling", Area: "Run Logic",
				Notes: fmt.Sprintf(
					"%s has conflicting case-insensitive RunLogicRunMode declarations (%s); leaving block_scheduling unset while the engine retains its fixed action order, and requiring review.",
					boundedRuntimeReviewEvidence(fileName),
					boundedRuntimeReviewEvidence(runtimeConfigConflictEvidence(conflict)),
				),
			})
			continue
		}
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
		case "http_idle_timeout":
			if !rc.KeepAlive {
				parity[index].Status = RuntimeSettingStatusNonApplicable
				parity[index].Notes = "The saved idle timeout is inactive because KeepAlive is disabled."
			}
		case "browser_selection":
			if customUserAgentSelected {
				parity[index].Status = RuntimeSettingStatusNonApplicable
				parity[index].Notes = "Specific browser type/version/platform values are dormant because the selected custom User-Agent is authoritative."
			}
		case "browser_cache_content_urls", "browser_check_newer_versions", "http_cache_eviction":
			if cacheDisabled {
				parity[index].Status = RuntimeSettingStatusNonApplicable
				parity[index].Notes = "The source child setting is dormant because browser-cache simulation is disabled."
			}
		}
	}
	return parity
}
