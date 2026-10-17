package enterprisearchive

import (
	"fmt"
	"math"
	filepath "path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/protocol"
)

var (
	javaEnvPutRe               = regexp.MustCompile(`(?i)\.put\s*\((.+)\)\s*;?\s*$`)
	javaSetMessagePropertyRe   = regexp.MustCompile(`(?i)^([A-Za-z_][A-Za-z0-9_]*)\.set(?:String|Boolean|Int|Long)Property\s*\((.+)\)\s*;?\s*$`)
	javaSendOnProducerRe       = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\.send\s*\((.*)\)\s*;?\s*$`)
	javaSendCallRe             = regexp.MustCompile(`(?i)\.send\s*\((.*)\)\s*;?\s*$`)
	javaReceiveCallRe          = regexp.MustCompile(`(?i)\.receive(?:NoWait)?\s*\((.*)\)\s*;?\s*$`)
	javaAckCallRe              = regexp.MustCompile(`(?i)\.acknowledge\s*\(\s*\)\s*;?\s*$`)
	javaCommitCallRe           = regexp.MustCompile(`(?i)\.commit\s*\(\s*\)\s*;?\s*$`)
	javaRollbackCallRe         = regexp.MustCompile(`(?i)\.rollback\s*\(\s*\)\s*;?\s*$`)
	javaDeclarationOnlyRe      = regexp.MustCompile(`^\s*(?:final\s+)?[A-Za-z_][A-Za-z0-9_<>\[\].]*\s+[A-Za-z_][A-Za-z0-9_]*\s*;\s*$`)
	javaBlockCommentRe         = regexp.MustCompile(`(?s)/\*.*?\*/`)
	javaUnqualifiedCallRe      = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	javaQualifiedCallRe        = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$.]*)\s*\.\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	javaNewObjectCallRe        = regexp.MustCompile(`\bnew\s+([A-Za-z_$][A-Za-z0-9_$.]*)\s*\([^;{}]*\)\s*\.\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	javaMemberAccessRe         = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$.]*)\s*\.\s*([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	javaCallReceiverFieldRe    = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\([^;{}()]*\)\s*\.\s*([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	javaCastReceiverFieldRe    = regexp.MustCompile(`\(\s*\(\s*([A-Za-z_$][A-Za-z0-9_$.]*(?:\s*\[\])*)\s*\)\s*[^()]+\)\s*\.\s*([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	javaArrayReceiverFieldRe   = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\[[^\]]+\]\s*\.\s*([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	javaMethodReferenceNameRe  = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$.]*)\s*::\s*([A-Za-z_$][A-Za-z0-9_$]*|new)\b`)
	javaLRCallStartRe          = regexp.MustCompile(`(?i)\blr\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	javaFullyQualifiedLRCallRe = regexp.MustCompile(`(?i)\blrapi\s*\.\s*lr\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	javaVariableDeclRe         = regexp.MustCompile(`(?m)\b(?:(?:public|private|protected|static|final|volatile|transient)\s+)*([A-Za-z_$][A-Za-z0-9_$.]*(?:\s*<[^;={}()]+>)?(?:\s*\[\])*)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:=|;|,|\))`)
	javaVarObjectDeclRe        = regexp.MustCompile(`(?i)\bvar\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*new\s+([A-Za-z_$][A-Za-z0-9_$.]*(?:\s*<[^;={}()]+>)?(?:\s*\[\])*)`)
	javaLRValueCallRe          = regexp.MustCompile(`(?i)^(?:lrapi\s*\.\s*)?lr\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	javaStaticFieldModifierRe  = regexp.MustCompile(`\bstatic\b`)
	javaFinalFieldModifierRe   = regexp.MustCompile(`\bfinal\b`)
	javaConstantNumberRe       = regexp.MustCompile(`(?i)^[+-]?(?:(?:0x[0-9a-f_]+|0b[01_]+|0[0-7_]+|[0-9][0-9_]*)[l]?|(?:(?:(?:[0-9][0-9_]*)?\.[0-9][0-9_]*|[0-9][0-9_]*\.)(?:e[+-]?[0-9][0-9_]*)?[fd]?|[0-9][0-9_]*e[+-]?[0-9][0-9_]*[fd]?|[0-9][0-9_]*[fd])|0x(?:(?:[0-9a-f_]+\.[0-9a-f_]*)|(?:[0-9a-f_]*\.[0-9a-f_]+)|[0-9a-f_]+)p[+-]?[0-9][0-9_]*[fd]?)$`)
)

var javaKnownLRReturnTypes = map[string]string{
	"aws_get_secret":              "string",
	"cyberark_get_vault":          "string",
	"decrypt":                     "string",
	"decrypt_ex":                  "string",
	"eval_int":                    "int",
	"eval_string":                 "string",
	"get_attrib_double":           "double",
	"get_attrib_long":             "long",
	"get_attrib_string":           "string",
	"get_debug_message":           "int",
	"get_group_name":              "string",
	"get_host_name":               "string",
	"get_master_host_name":        "string",
	"get_scenario_id":             "string",
	"get_transaction_duration":    "double",
	"get_transaction_status":      "int",
	"get_transaction_think_time":  "double",
	"get_transaction_wasted_time": "double",
	"get_vuser_id":                "int",
}

const (
	javaVuserRunnerRequirement = "java_vuser_jvm_runner"
	javaVuserRunnerBundle      = "Governed Java Vuser JVM Runner"
	javaVuserReviewReason      = "java_vuser_governed_runner_required"
	javaVuserSidecarTransport  = "loopback_http_json_v1"
	javaVuserEventContract     = "apexload.java_vuser.lrapi_events.v2"
	javaVuserShimClass         = "lrapi.lr"
	javaExternalLifecycleKey   = "external_lifecycle_methods"
	javaLifecycleBlockersKey   = "external_lifecycle_blockers"
	javaSourceIntegrityKey     = "source_integrity_blockers"
	javaRunnerSemanticsKey     = "runner_semantics_blockers"
)

// javaVuserNoParseableSourceBlocker blocks promotion of a combined draft whose
// archive contained no parseable Java lifecycle method: the draft would invoke
// a class the importer never verified, guaranteeing runtime failure once a
// governed runner pool is enabled. Recorded on the review artifact so the
// fidelity report (and the import-promotion launch gate) fail closed.
const javaVuserNoParseableSourceBlocker = "Java archive contains no parseable Java lifecycle method; the combined runner draft references unverified source and must stay review-only until the original .java lifecycle files are re-imported."
const javaVuserMissingActionBlocker = "Selected Java runner class does not declare a parseable action lifecycle method (action, Action, or run); the combined draft must stay review-only until that method is restored and the archive is re-imported."

const (
	// This is a conversion-scoped work budget, not a Java source-size limit.
	// It bounds the candidate comparisons projected across the importer
	// classifier and runtime-reachability passes before either pass can enter an
	// overloads x call-sites amplification loop.
	javaOverloadCandidateComparisonLimit int64 = 2_000_000
	javaOverloadResolutionPassMultiplier int64 = 8
	// Mappable-entry construction retains the variable types visible at each
	// source statement. Bound the aggregate number of copied bindings so a
	// method with tens of thousands of sequential declarations cannot turn the
	// statement quota into quadratic map-copy work before overload preflight.
	javaVariableScopeSnapshotBindingLimit int64 = 1_000_000
)

var javaVuserShimEventTypes = []string{
	"debug",
	"error",
	"external_secret",
	"log",
	"metric",
	"parameter",
	"rendezvous",
	"review",
	"runtime",
	"status",
	"timer_end",
	"timer_start",
	"transaction_end",
	"transaction_set",
	"transaction_start",
	"transaction_status",
	"transaction_wasted_time",
}

type javaImportState struct {
	env                javaStringEnv
	unrecognizedAction int
	jmsActions         int
	configReview       int
	statementCount     int
	profile            JavaProfile
	skipHTTP           bool
	skipDatabase       bool

	// recognizedLRActions counts lr.* methods that resolved to a documented
	// disposition (so a fully-classified non-JMS lr script can be reported as
	// "classified, no executable protocol target" rather than "unmapped").
	recognizedLRActions int
	// Transaction state mirrors the C converter loop so Java transactions feed
	// Stats and downstream actions are grouped. Literal lr.think_time calls are
	// retained directly on their source_runtime actions, preserving call order.
	currentTransaction      string
	currentSubTransaction   string
	emitsTransactions       bool
	emitsActionTransactions bool
	transactionMarkers      int
	transactions            int
}

func ConvertJava(files map[string]string, opts ConvertOptions, detected DetectedProtocol, projectConfig *ProjectConfig) (*ConvertResult, error) {
	program, parseWarnings := parseJavaProgram(files)
	program = javaProgramWithProjectVuserClass(program, projectConfig)
	// One evidence index serves every pass of this import. Its type, field and
	// hierarchy maps are a pure function of the parsed program, so building it
	// once removes the repeated per-class source rescans a large archive would
	// otherwise pay for on every pass.
	evidenceIndex := newJavaEvidenceIndex(program)
	mappable := collectJavaCombinedMappableEntries(program)
	overloadWork := analyzeJavaOverloadResolutionWorkForEntries(program, mappable, evidenceIndex)
	var runtimeReachability javaVuserRuntimeReachability
	if overloadWork.blocker == "" {
		runtimeReachability = buildJavaVuserRuntimeReachabilityWithEvidenceIndex(program, evidenceIndex)
	} else {
		runtimeReachability = javaVuserRuntimeReachability{
			byOccurrence:      map[string]int{},
			byStatement:       map[string][]int{},
			scopeReachability: map[string]javaRuntimeReachableStatement{},
			evidenceIndex:     evidenceIndex,
		}
		// Fail closed before the expensive passes. The source and mappings are
		// still preserved in the singleton Java draft, while the explicit
		// unresolved item prevents authoring promotion.
		runtimeReachability.unresolved = []string{overloadWork.blocker}
		for index := range mappable.entries {
			// Later fidelity counting and source mapping normally bind every LR
			// call again. Mark all entries incomplete when preflight stops so a
			// guarded import cannot accidentally repeat the work that exceeded
			// the limit.
			mappable.entries[index].typesComplete = false
			mappable.entries[index].preflightIncomplete = true
		}
	}
	// Archive classification only fed the retired executable statement lanes, which
	// read JavaProfile to gate the JMS idiom mapper. Those lanes have no production
	// caller any more — their orchestration is quarantined in
	// java_native_lanes_test.go — and the combined runner draft below never consults
	// the profile, so the live path skips classification and its executable-evidence
	// and review-family scans entirely.
	state := &javaImportState{env: newJavaStringEnv(), profile: JavaProfile{Surfaces: map[javaSurface]bool{}}}
	externalLifecycle := javaVuserExternalLifecycleMethods(program, projectConfig)
	// Count exactly the unique statements the mapping pass will disposition so
	// TotalFunctions reconciles against the recorded support counters instead of
	// double-counting inlined helper statements or empty leaves.
	state.statementCount = javaCombinedMappableFunctionCountForEntries(program, mappable.entries, evidenceIndex) + len(externalLifecycle)
	state.emitsTransactions, state.emitsActionTransactions, state.transactionMarkers, state.transactions = javaProgramTransactionStats(runtimeReachability)

	result := convertJavaVuserRunnerDraft(files, opts, detected, program, append([]string(nil), parseWarnings...), state, projectConfig)
	recordJavaProgramTransactionNames(result, runtimeReachability)
	applyJavaVuserSourceSelectionGate(result, program)
	appendJavaCombinedSourceMappingsForEntries(result, program, mappable.entries, externalLifecycle, runtimeReachability)
	attachJavaKafkaSettingsReview(result, program)
	attachJavaDependencyInventory(result, files)
	if blockers := javaVuserRunnerSemanticBlockers(runtimeReachability); len(blockers) > 0 {
		if result.ReviewArtifact != nil {
			if result.ReviewArtifact.Metadata == nil {
				result.ReviewArtifact.Metadata = map[string]any{}
			}
			result.ReviewArtifact.Metadata[javaRunnerSemanticsKey] = blockers
			result.ReviewArtifact.NextActions = appendUnique(
				result.ReviewArtifact.NextActions,
				"Resolve the reported review-only LoadRunner Java API calls or add verified governed-runner support, then re-import before approval.",
			)
		}
		result.Warnings = append(result.Warnings, blockers...)
	}
	if blockers := javaVuserSourceIntegrityBlockers(program, projectConfig, evidenceIndex); len(blockers) > 0 {
		if result.ReviewArtifact != nil {
			if result.ReviewArtifact.Metadata == nil {
				result.ReviewArtifact.Metadata = map[string]any{}
			}
			existing := javaReviewArtifactBlockers(result, javaSourceIntegrityKey)
			result.ReviewArtifact.Metadata[javaSourceIntegrityKey] = appendUnique(existing, blockers...)
			result.ReviewArtifact.NextActions = appendUnique(result.ReviewArtifact.NextActions, "Re-import the archive with parseable Java lifecycle source (for example an Actions.java declaring public int action()).")
		}
		result.Warnings = append(result.Warnings, blockers...)
		result.Warnings = append(result.Warnings, "The combined review draft retains an action placeholder, but promotion remains blocked until the selected runner class has a verified action lifecycle method.")
	}
	if len(program.Unparsed) > 0 {
		result.Warnings = append(result.Warnings, "Java source contained unparsed regions; the original source remains preserved in the combined runner draft for review.")
	}
	result.Warnings = uniqueSortedStrings(result.Warnings)
	result.Scripts = []*dsl.Script{result.Script}
	return result, nil
}

const javaVuserSourceSelectionExecutionBlocker = "java_source_selection_unresolved"

func applyJavaVuserSourceSelectionGate(result *ConvertResult, program javaProgram) {
	if result == nil || result.Script == nil || program.vuserSourceSelectionBlocker == "" {
		return
	}
	_ = dslwalk.WalkActions(result.Script, func(ref dslwalk.ActionRef) error {
		importreview.ApplyToAction(ref.Action, []importreview.Finding{{
			Code:       javaVuserSourceSelectionExecutionBlocker,
			Severity:   diag.Blocker,
			Element:    "Java lifecycle source",
			Detail:     "the declared Java action lifecycle source could not be resolved unambiguously; no alternate same-basename source was selected",
			Location:   ref.Path,
			ActionPath: ref.Path,
			Source:     "enterprise_script_archive",
			ReasonCode: javaVuserSourceSelectionExecutionBlocker,
			SourceRef:  program.vuserSourceSelectionRef,
			Resolution: "replace_behavior_and_remove_gate",
			Gate:       importreview.GateExecution,
		}})
		return nil
	})
}

// javaSharedEvidenceIndexForPass hands one import-scoped evidence index to a
// pass instead of rebuilding it. Only the structural maps newJavaEvidenceIndex
// derives from the parsed program are shared: they are a pure function of that
// program, so reuse cannot change a disposition, and building them once removes
// the per-class source rescans that dominate a large archive.
//
// Everything a freshly built index starts empty is rebased here. The governed
// lookup budgets are per-pass limits, and the lazily filled caches decide how
// much of each budget a pass actually charges: a warm staticMethodCache returns
// before javaEvidenceStaticImportedMethods charges staticImportLookups, a warm
// fieldLookupCache collapses a whole hierarchy walk into a single member lookup,
// and the functional-binding caches skip builds that charge both. Carrying any
// of them across passes would quietly widen the allowance and could drop the
// fail-closed blockers those limits exist to raise.
func javaSharedEvidenceIndexForPass(index *javaEvidenceIndex, program javaProgram) *javaEvidenceIndex {
	if index == nil {
		return newJavaEvidenceIndex(program)
	}
	index.totalMemberLookups = 0
	index.staticImportLookups = 0
	index.staticImportLimitHit = false
	index.functionalBindingBuilds = 0
	index.hierarchyFieldTypes = make(map[string]map[string]string, len(program.Classes))
	index.fieldLookupCache = make(map[string]javaEvidenceFieldLookup)
	index.bindingCache = make(map[string][]string)
	index.importScopes = make(map[string]*javaEvidenceImportScope)
	index.staticMethodCache = make(map[string][]javaResolvedHelperMethod)
	index.subtypeCache = make(map[string][]int)
	index.functionalBindings = make(map[string]javaStoredFunctionalBindingTimeline)
	index.persistentFunctional = make(map[string]javaStoredFunctionalBindingSet)
	return index
}

func javaVuserSourceIntegrityBlockers(program javaProgram, projectConfig *ProjectConfig, evidenceIndex *javaEvidenceIndex) []string {
	blockers := javaVuserInstantiationBlockers(program)
	blockers = append(blockers, javaVuserInheritanceBlockers(program, evidenceIndex)...)
	blockers = append(blockers, javaVuserUnparsedSourceBlockers(program, projectConfig, evidenceIndex)...)
	if program.vuserSourceSelectionBlocker != "" {
		blockers = append(blockers, program.vuserSourceSelectionBlocker)
	}
	if len(program.Methods) == 0 {
		blockers = append(blockers, javaVuserNoParseableSourceBlocker)
		return uniqueSortedStrings(blockers)
	}
	blockers = append(blockers, javaVuserLifecycleSignatureBlockers(program)...)
	if !javaProgramHasLifecycleMethod(program, "action") {
		blockers = append(blockers, javaVuserMissingActionBlocker)
	}
	return uniqueSortedStrings(blockers)
}

func javaVuserUnparsedSourceBlockers(program javaProgram, projectConfig *ProjectConfig, evidenceIndex *javaEvidenceIndex) []string {
	regions := javaVuserRelevantUnparsedRegionsWithEvidenceIndex(program, projectConfig, evidenceIndex)
	blockers := make([]string, 0, len(regions))
	for _, region := range regions {
		filename := strings.TrimSpace(region.File)
		if filename == "" {
			filename = "an uploaded Java source"
		}
		line := region.StartLine
		if line <= 0 {
			line = 1
		}
		reason := strings.ToLower(strings.TrimSpace(region.Text + " " + region.Header))
		if strings.Contains(reason, "analysis") && strings.Contains(reason, "limit") {
			blockers = append(blockers, fmt.Sprintf(
				"Java source %s reached a governed analysis limit at line %d; parsed types and methods from that source are incomplete, so the combined runner cannot be promoted.",
				filename,
				line,
			))
			continue
		}
		blockers = append(blockers, fmt.Sprintf(
			"Java source %s contains an unparsed region at line %d; exact runtime reachability cannot be verified, so the combined runner cannot be promoted.",
			filename,
			line,
		))
	}
	return uniqueSortedStrings(blockers)
}

// javaVuserUnparsedRegionIsGlobalTruncation reports whether an unparsed region
// means the parser stopped in a way that can hide code ANYWHERE in the archive.
// The aggregate budgets in parseJavaProgram set analysisLimitReached, which skips
// every remaining .java file, so those files never enter program.Classes at all
// and no later evidence pass can notice they are missing. Such a region can never
// be scoped away by relevance, because relevance itself is unknowable once the
// type model is truncated.
func javaVuserUnparsedRegionIsGlobalTruncation(region javaStatement) bool {
	reason := strings.ToLower(strings.TrimSpace(region.Text + " " + region.Header))
	// "Java sources exceed aggregate <budget> limit" (java_model.go) and
	// "Java sources exceed aggregate analysis limit ... at <file>" (java_scanner.go).
	if strings.Contains(reason, "aggregate") {
		return true
	}
	// Per-file scanner budgets, e.g. "Java source exceeds analysis method limit".
	return strings.Contains(reason, "analysis") && strings.Contains(reason, "limit")
}

// javaVuserRunnerSourceFiles returns the normalized source paths the combined
// runner actually executes: the selected class and its superclass chain, plus the
// declared (or conventionally named) lifecycle sources. External lifecycle files
// such as vuser_init.java are NOT in the superclass chain, yet ConvertJava binds
// their init/action/teardown bodies into the draft, so they must count as runner
// sources for blocker scoping.
func javaVuserRunnerSourceFiles(
	program javaProgram,
	selected javaClass,
	projectConfig *ProjectConfig,
	evidenceIndex *javaEvidenceIndex,
) map[string]bool {
	sources := map[string]bool{}
	add := func(file string) {
		if normalized := javaNormalizedSourcePath(file); normalized != "" {
			sources[normalized] = true
		}
	}

	index := javaSharedEvidenceIndexForPass(evidenceIndex, program)

	// Seed with the selected runner plus every file that supplies a lifecycle
	// phase: those are executed even though they are unrelated by inheritance.
	worklist := []javaClass{selected}
	for _, class := range program.Classes {
		for _, lifecycle := range []string{"init", "action", "teardown"} {
			if javaExternalLifecycleFileMatches(lifecycle, class.File, projectConfig) {
				worklist = append(worklist, class)
				break
			}
		}
	}

	// Close over supertypes. classHierarchy walks SUPERCLASSES only, but an
	// implemented interface can contribute executable default-method bodies, so
	// its source is part of the runner's surface too.
	visited := map[string]bool{}
	for len(worklist) > 0 {
		current := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		key := javaEvidenceOwnerKey(current)
		if visited[key] {
			continue
		}
		visited[key] = true
		add(current.File)

		for _, superType := range append([]string{current.SuperClass}, current.Interfaces...) {
			name := strings.TrimSpace(superType)
			if name == "" || strings.EqualFold(name, "Object") || strings.EqualFold(name, "java.lang.Object") {
				continue
			}
			if resolved, ok := index.classForType(current, name); ok {
				worklist = append(worklist, resolved)
			}
		}
	}
	return sources
}

// javaClassReferenceText returns the text of a class that can name another type.
// class.Source holds only the class BODY (java_scanner.go sets it to the span
// inside the braces), so the declaration header — extends/implements clauses —
// and the file's imports must be searched explicitly or a header-only reference
// looks like "unused" and its unparsed source gets scoped away.
func javaClassReferenceText(class javaClass) string {
	var builder strings.Builder
	builder.WriteString(class.Source)
	builder.WriteByte('\n')
	builder.WriteString(class.SuperClass)
	builder.WriteByte('\n')
	for _, name := range class.Interfaces {
		builder.WriteString(name)
		builder.WriteByte('\n')
	}
	for _, imported := range class.Imports {
		builder.WriteString(imported)
		builder.WriteByte('\n')
	}
	if strings.TrimSpace(class.Source) == "" {
		for _, method := range class.Methods {
			builder.WriteString(method.Body)
			builder.WriteByte('\n')
		}
		for _, constructor := range class.Constructors {
			builder.WriteString(constructor.Body)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

// javaVuserSourceIsReferencedByRunner reports whether any type declared by the
// given source file is named anywhere in the runner's own sources. A file that
// fails to parse yields a class with zero methods, so the usual evidence passes
// cannot see it; matching the declared type name against the runner text is what
// lets a genuinely unused helper be scoped away without hiding a used one.
func javaVuserSourceIsReferencedByRunner(program javaProgram, source string, runnerSources map[string]bool) bool {
	declared := make([]string, 0, 2)
	for _, class := range program.Classes {
		if javaNormalizedSourcePath(class.File) != source {
			continue
		}
		if name := strings.TrimSpace(class.Name); name != "" {
			declared = append(declared, name)
		}
	}
	if len(declared) == 0 {
		// Unknown type surface: fail closed.
		return true
	}

	for _, class := range program.Classes {
		if !runnerSources[javaNormalizedSourcePath(class.File)] {
			continue
		}
		text := javaClassReferenceText(class)
		for _, name := range declared {
			if javaSourceMentionsType(text, name) {
				return true
			}
		}
	}
	return false
}

// javaSourceMentionsType reports whether name appears in text as a whole Java
// identifier rather than as a substring of a longer one.
func javaSourceMentionsType(text, name string) bool {
	if name == "" || text == "" {
		return false
	}
	isIdentRune := func(r byte) bool {
		return r == '_' || r == '$' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	for offset := 0; ; {
		index := strings.Index(text[offset:], name)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(name)
		beforeOK := start == 0 || !isIdentRune(text[start-1])
		afterOK := end == len(text) || !isIdentRune(text[end])
		if beforeOK && afterOK {
			return true
		}
		offset = start + 1
	}
}

// javaVuserRelevantUnparsedRegions narrows unparsed-source blockers to the
// sources that can actually affect the selected runner. A support class the
// runner never names cannot change its construction or lifecycle semantics, so an
// unparsed region there is not a promotion blocker. Everything else fails closed:
// global (aggregate) truncation always blocks, as does any file the runner names
// or any file whose type surface cannot be determined.
func javaVuserRelevantUnparsedRegions(program javaProgram, projectConfig *ProjectConfig) []javaStatement {
	return javaVuserRelevantUnparsedRegionsWithEvidenceIndex(program, projectConfig, nil)
}

func javaVuserRelevantUnparsedRegionsWithEvidenceIndex(
	program javaProgram,
	projectConfig *ProjectConfig,
	evidenceIndex *javaEvidenceIndex,
) []javaStatement {
	if len(program.Unparsed) == 0 {
		return nil
	}
	selected, ok := javaVuserClass(program)
	if !ok {
		// Fail closed when no runner class can be selected.
		return append([]javaStatement(nil), program.Unparsed...)
	}

	runnerSources := javaVuserRunnerSourceFiles(program, selected, projectConfig, evidenceIndex)
	if len(runnerSources) == 0 {
		// Preserve fail-closed behavior if source ownership cannot be resolved.
		return append([]javaStatement(nil), program.Unparsed...)
	}

	regions := make([]javaStatement, 0, len(program.Unparsed))
	for _, region := range program.Unparsed {
		if javaVuserUnparsedRegionIsGlobalTruncation(region) {
			regions = append(regions, region)
			continue
		}
		source := javaNormalizedSourcePath(region.File)
		if source == "" || runnerSources[source] {
			regions = append(regions, region)
			continue
		}
		if javaVuserSourceIsReferencedByRunner(program, source, runnerSources) {
			regions = append(regions, region)
		}
	}
	return regions
}

func javaVuserInheritanceBlockers(program javaProgram, evidenceIndex *javaEvidenceIndex) []string {
	selected, ok := javaVuserClass(program)
	if !ok {
		return nil
	}
	index := javaSharedEvidenceIndexForPass(evidenceIndex, program)
	visited := map[string]bool{}
	var blockers []string
	var inspect func(javaClass)
	inspect = func(class javaClass) {
		superName := strings.TrimSpace(class.SuperClass)
		if superName == "" || strings.EqualFold(superName, "Object") ||
			strings.EqualFold(superName, "java.lang.Object") {
			return
		}
		key := javaEvidenceOwnerKey(class)
		if visited[key] {
			blockers = append(blockers, fmt.Sprintf(
				"Selected Java runner inheritance contains a cycle at %s; superclass construction cannot be verified for governed replay.",
				javaQualifiedClassName(class),
			))
			return
		}
		visited[key] = true
		defer delete(visited, key)

		superClass, resolved := index.classForType(class, superName)
		if !resolved {
			blockers = append(blockers, fmt.Sprintf(
				"Java runner class %s extends unresolved superclass %s; upload the exact superclass source so construction and lifecycle semantics can be verified before promotion.",
				javaQualifiedClassName(class), superName,
			))
			return
		}
		inspect(superClass)
	}
	inspect(selected)
	return uniqueSortedStrings(blockers)
}

func javaVuserInstantiationBlockers(program javaProgram) []string {
	class, ok := javaVuserClass(program)
	if !ok {
		return nil
	}
	className := javaQualifiedClassName(class)
	blockers := make([]string, 0, 3)
	if !strings.EqualFold(strings.TrimSpace(class.Kind), "class") {
		kind := convertutil.FirstNonEmpty(strings.TrimSpace(class.Kind), "non-class type")
		article := "a"
		if strings.HasPrefix(strings.ToLower(kind), "a") || strings.HasPrefix(strings.ToLower(kind), "e") ||
			strings.HasPrefix(strings.ToLower(kind), "i") || strings.HasPrefix(strings.ToLower(kind), "o") ||
			strings.HasPrefix(strings.ToLower(kind), "u") {
			article = "an"
		}
		blockers = append(blockers, fmt.Sprintf(
			"Selected Java runner type %s is %s %s, not a concrete class; the governed harness cannot create one Actions instance per VUser.",
			className, article, kind,
		))
	}
	if class.Abstract {
		blockers = append(blockers, fmt.Sprintf(
			"Selected Java runner class %s is abstract; the governed harness cannot create one Actions instance per VUser.",
			className,
		))
	}
	if !class.Public {
		blockers = append(blockers, fmt.Sprintf(
			"Selected Java runner class %s is not public; the isolated governed harness cannot instantiate it reflectively.",
			className,
		))
	}
	if len(class.Constructors) > 0 {
		hasPublicZeroArg := false
		for _, constructor := range class.Constructors {
			if javaMethodIsPublicZeroArg(constructor) {
				hasPublicZeroArg = true
				break
			}
		}
		if !hasPublicZeroArg {
			blockers = append(blockers, fmt.Sprintf(
				"Selected Java runner class %s declares constructors but no public zero-argument constructor; the governed harness invokes getDeclaredConstructor().newInstance() once per VUser.",
				className,
			))
		}
	}
	return blockers
}

// javaLeafStatementList flattens one statement into the leaves the converter
// actually visits: transparent blocks recurse into their children, opaque blocks
// stay a single leaf.
func javaLeafStatementList(statement javaStatement) []javaStatement {
	if statement.Kind == "block" && javaTransparentBlockHeader(statement.Header) {
		out := make([]javaStatement, 0)
		for _, child := range statement.Children {
			out = append(out, javaLeafStatementList(child)...)
		}
		return out
	}
	return []javaStatement{statement}
}

func convertJavaVuserRunnerDraft(files map[string]string, opts ConvertOptions, detected DetectedProtocol, program javaProgram, warnings []string, state *javaImportState, projectConfig *ProjectConfig) *ConvertResult {
	detected.CanonicalSourceProtocol = "java_vuser"
	detected.APEXLoadProtocol = "java_vuser"
	detected.ImportMode = ImportModeExternalRunnerRequired
	// Protocol-family classifiers remain useful as review signals, but they no
	// longer choose an executable native lane. Normalize every Java archive to
	// the same combined-runner identity so a JMS/JDBC/HTTP hint cannot retain a
	// native bundle name or provider-specific credential requirement.
	detected.DisplayName = "Java Vuser"
	detected.Bundle = javaVuserRunnerBundle
	detected.RunnerRequirement = javaVuserRunnerRequirement
	detected.Warnings = filterJavaNativeLaneWarnings(detected.Warnings)
	detected.RequiredEvidence = appendUnique(nil,
		"Authoring review and approval of preserved Java source",
		"generator-java image with isolated classpath and no ambient secret/environment leakage",
		"java_vuser_jvm_runner entitlement",
		"runner capability "+protocol.FeatureJavaVuserPersistentLifecycleV1,
		"runner capability "+protocol.FeatureJavaVuserSessionFrameTokenV1,
	)
	detected.Warnings = append(detected.Warnings,
		"Java Vuser replay executes source code and remains gated until a governed runner pool is configured.",
		"Only a governed local or loopback-sidecar runner with qualified process containment and both "+protocol.FeatureJavaVuserPersistentLifecycleV1+" and "+protocol.FeatureJavaVuserSessionFrameTokenV1+" keeps one JVM and one Actions instance alive per VUser so Java object state and saved LoadRunner parameters persist across init, action iterations, and end; stock non-privileged sidecars remain legacy full_lifecycle only.",
	)

	className := javaVuserClassName(program)
	externalLifecycle := javaVuserExternalLifecycleMethods(program, projectConfig)
	if len(externalLifecycle) > 0 {
		detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Resolution evidence that one selected Actions class owns every declared Java Vuser lifecycle phase")
	}
	initActions := make([]dsl.Action, 0, 1)
	actions := make([]dsl.Action, 0, 1)
	teardownActions := make([]dsl.Action, 0, 1)
	if method, ok := javaVuserLifecycleMethod(program, "init"); ok {
		initActions = append(initActions, javaVuserLifecycleAction(className, method.Name, false))
	}
	actionMethod := "action"
	if method, ok := javaVuserLifecycleMethod(program, "action"); ok {
		actionMethod = method.Name
	}
	actions = append(actions, javaVuserLifecycleAction(className, actionMethod, state.emitsActionTransactions))
	if method, ok := javaVuserLifecycleMethod(program, "teardown"); ok {
		teardownActions = append(teardownActions, javaVuserLifecycleAction(className, method.Name, false))
	}
	convertedActions := len(initActions) + len(actions) + len(teardownActions)
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append(append(append([]string(nil), warnings...), detected.Warnings...), "A combined Java Vuser runner draft was generated; launch remains blocked until authoring approval and a governed java_vuser runner are available."),
		Script: &dsl.Script{
			Name:          sanitizeName(convertutil.FirstNonEmpty(opts.ScriptName, "Java_Vuser_Combined_Draft")),
			Version:       "1.0.0",
			Protocol:      "java_vuser",
			GeneratedBy:   "apexload-enterprise-script-archive-java-importer",
			GeneratedFrom: "enterprise_script_archive",
			Config: dsl.ScriptConfig{
				JavaVuser: dsl.JavaVuserConfig{
					Enabled:         false,
					HarnessJar:      "runners/java_vuser/apexload-java-vuser-runner.jar",
					MaxProcesses:    25,
					ActionTimeoutMs: 30000,
				},
			},
			Init:     initActions,
			Actions:  actions,
			Teardown: teardownActions,
		},
		Stats: ConvertStats{
			TotalFunctions:     state.statementCount,
			ConvertedActions:   convertedActions,
			TransactionMarkers: state.transactionMarkers,
			Transactions:       state.transactions,
			// Per-statement support counters (ExternalRequired/ReviewOnly) are
			// recorded once by appendJavaCombinedSourceMappings so unparsed
			// regions are not double-counted against TotalFunctions.
			ExternalRequiredFunctions: 0,
			ReviewOnlyFunctions:       0,
			CustomCallFunctions:       0,
			Skipped:                   0,
			UnsupportedFuncs:          nil,
		},
	}
	result.ReviewArtifact = NewReviewArtifact(detected, files, nil, map[string]any{
		"reason":                   javaVuserReviewReason,
		"runner_requirement":       javaVuserRunnerRequirement,
		"required_runtime_feature": protocol.FeatureJavaVuserPersistentLifecycleV1,
		"required_runtime_features": []string{
			protocol.FeatureJavaVuserPersistentLifecycleV1,
			protocol.FeatureJavaVuserSessionFrameTokenV1,
		},
		"sidecar_transport":           javaVuserSidecarTransport,
		"event_contract":              javaVuserEventContract,
		"shim_class":                  javaVuserShimClass,
		"shim_event_types":            append([]string(nil), javaVuserShimEventTypes...),
		"lifecycle_mode":              "persistent_per_vuser",
		"lifecycle_phases":            javaVuserLifecyclePhases(program),
		"emits_business_transactions": state.emitsTransactions,
		// The combined draft runs no per-statement classification, so the old
		// unrecognized/configuration counters (which were hardwired to zero on
		// this path) are replaced by the counter the draft actually measures.
		"unparsed_source_regions": len(program.Unparsed),
	})
	if manifest := BuildJavaVuserSourceManifest(files); manifest != nil {
		result.ReviewArtifact.Metadata[JavaVuserSourceManifestMetadataKey] = manifest
	}
	if len(externalLifecycle) > 0 {
		metadata := javaExternalLifecycleMetadata(externalLifecycle)
		blockers := javaExternalLifecycleBlockers(className, externalLifecycle)
		result.ReviewArtifact.Metadata[javaExternalLifecycleKey] = metadata
		result.ReviewArtifact.Metadata[javaLifecycleBlockersKey] = blockers
		result.ReviewArtifact.PreservedFunctions = appendUnique(result.ReviewArtifact.PreservedFunctions, javaExternalLifecycleFunctionNames(externalLifecycle)...)
		result.ReviewArtifact.NextActions = appendUnique(result.ReviewArtifact.NextActions, "Merge the reported lifecycle methods into one selected Actions class, then re-import the archive before promotion.")
		result.Warnings = append(result.Warnings, blockers...)
	}
	return result
}

func filterJavaNativeLaneWarnings(warnings []string) []string {
	filtered := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		lower := strings.ToLower(warning)
		if strings.Contains(lower, "generated as jms dsl") || strings.Contains(lower, "native java lane") {
			continue
		}
		filtered = append(filtered, warning)
	}
	return filtered
}

func javaVuserLifecycleAction(className string, method string, emitsTransactions bool) dsl.Action {
	action := dsl.Action{
		Name:   "Run Java Vuser " + method,
		Type:   "java_vuser_run",
		Target: className,
		Params: map[string]any{
			"method":             method,
			"source_archive_ref": "enterprise_script_archive",
			"runner_requirement": javaVuserRunnerRequirement,
			"event_contract":     javaVuserEventContract,
			"shim_event_types":   append([]string(nil), javaVuserShimEventTypes...),
		},
		Metadata: map[string]any{
			"review_required":          true,
			"review_reason":            javaVuserReviewReason,
			"runner_requirement":       javaVuserRunnerRequirement,
			"required_runtime_feature": protocol.FeatureJavaVuserPersistentLifecycleV1,
			"required_runtime_features": []string{
				protocol.FeatureJavaVuserPersistentLifecycleV1,
				protocol.FeatureJavaVuserSessionFrameTokenV1,
			},
			"source_protocol":   "java_vuser",
			"sidecar_transport": javaVuserSidecarTransport,
			"event_contract":    javaVuserEventContract,
			"shim_class":        javaVuserShimClass,
			"shim_callbacks":    append([]string(nil), javaVuserShimEventTypes...),
			"shim_event_types":  append([]string(nil), javaVuserShimEventTypes...),
			"source_class":      className,
			"lifecycle_method":  method,
			"lifecycle_mode":    "persistent_per_vuser",
		},
	}
	if emitsTransactions {
		action.Metadata["emits_business_transactions"] = true
	}
	return action
}

func javaProgramHasLifecycleMethod(program javaProgram, lifecycle string) bool {
	_, ok := javaVuserLifecycleMethod(program, lifecycle)
	return ok
}

func javaVuserLifecycleMethod(program javaProgram, lifecycle string) (javaMethod, bool) {
	class, ok := javaVuserClass(program)
	if !ok {
		return javaMethod{}, false
	}
	return javaExecutableLifecycleMethodInClass(class, lifecycle)
}

func javaExecutableLifecycleMethodInClass(class javaClass, lifecycle string) (javaMethod, bool) {
	for _, name := range javaLifecycleMethodNames(lifecycle) {
		for _, method := range javaMethodsNamed(class, name) {
			if javaLifecycleMethodIsSupported(method) {
				return method, true
			}
		}
	}
	return javaMethod{}, false
}

func javaMethodsNamed(class javaClass, name string) []javaMethod {
	if methods := class.MethodOverloads[name]; len(methods) > 0 {
		return methods
	}
	if method, ok := class.Methods[name]; ok {
		return []javaMethod{method}
	}
	return nil
}

func javaMethodIsPublicZeroArg(method javaMethod) bool {
	return strings.EqualFold(strings.TrimSpace(method.Visibility), "public") &&
		method.ParameterCount == 0
}

func javaLifecycleMethodIsSupported(method javaMethod) bool {
	return javaMethodIsPublicZeroArg(method) &&
		!method.Static &&
		!method.Abstract &&
		strings.EqualFold(strings.TrimSpace(method.ReturnType), "int")
}

func javaVuserLifecycleSignatureBlockers(program javaProgram) []string {
	class, ok := javaVuserClass(program)
	if !ok {
		return nil
	}
	className := javaQualifiedClassName(class)
	blockers := make([]string, 0)
	for _, lifecycle := range []string{"init", "action", "teardown"} {
		// One valid alias is sufficient for the selected lifecycle phase. Other
		// overloads or private helpers with conventional lifecycle names are not
		// invoked by the generated draft and must not block an otherwise exact
		// runner contract.
		if _, ok := javaExecutableLifecycleMethodInClass(class, lifecycle); ok {
			continue
		}
		for _, name := range javaLifecycleMethodNames(lifecycle) {
			for _, method := range javaMethodsNamed(class, name) {
				if method.ParameterCount != 0 || javaLifecycleMethodIsSupported(method) {
					continue
				}
				blockers = append(blockers, fmt.Sprintf(
					"Selected Java runner lifecycle method %s.%s must be a public non-static zero-argument method returning int; the governed harness rejects its current signature.",
					className,
					method.Name,
				))
			}
		}
	}
	return uniqueSortedStrings(blockers)
}

type javaExternalLifecycleMethod struct {
	Phase      string
	ClassName  string
	MethodName string
	SourceFile string
	StartLine  int
	EndLine    int
}

// javaVuserExternalLifecycleMethods finds lifecycle phases that the selected
// Actions class does not own but another parsed Java class does. Those methods
// cannot be invoked without creating a second object/session, so the importer
// preserves them as review evidence and blocks promotion instead of silently
// treating the single-class draft as complete.
func javaVuserExternalLifecycleMethods(program javaProgram, projectConfig *ProjectConfig) []javaExternalLifecycleMethod {
	selected, ok := javaVuserClass(program)
	if !ok {
		return nil
	}
	issues := make([]javaExternalLifecycleMethod, 0)
	for _, lifecycle := range []string{"init", "action", "teardown"} {
		if _, exists := javaVuserLifecycleMethod(program, lifecycle); exists {
			continue
		}
		for _, class := range program.Classes {
			if strings.EqualFold(strings.TrimSpace(class.File), strings.TrimSpace(selected.File)) {
				continue
			}
			if !javaExternalLifecycleFileMatches(lifecycle, class.File, projectConfig) {
				continue
			}
			for _, methodName := range javaLifecycleMethodNames(lifecycle) {
				for _, method := range javaMethodsNamed(class, methodName) {
					if !javaLifecycleMethodIsSupported(method) {
						continue
					}
					issues = append(issues, javaExternalLifecycleMethod{
						Phase:      lifecycle,
						ClassName:  javaQualifiedClassName(class),
						MethodName: method.Name,
						SourceFile: method.File,
						StartLine:  method.StartLine,
						EndLine:    method.EndLine,
					})
				}
			}
		}
	}
	return issues
}

func javaExternalLifecycleFileMatches(lifecycle, sourceFile string, projectConfig *ProjectConfig) bool {
	declaredFiles := javaDeclaredLifecycleSourceFiles(lifecycle, projectConfig)
	if len(declaredFiles) > 0 {
		for _, declaredFile := range declaredFiles {
			actual := javaNormalizedSourcePath(sourceFile)
			configured := javaNormalizedSourcePath(declaredFile)
			if actual == configured ||
				(!strings.Contains(configured, "/") && filepath.Base(actual) == filepath.Base(configured)) {
				return true
			}
		}
		return false
	}

	base := strings.ToLower(strings.TrimSpace(filepath.Base(sourceFile)))
	switch lifecycle {
	case "init":
		return base == "vuser_init.java"
	case "teardown":
		return base == "vuser_end.java"
	default:
		return base == "action.java" || base == "actions.java"
	}
}

func javaDeclaredLifecycleSourceFiles(lifecycle string, projectConfig *ProjectConfig) []string {
	if projectConfig == nil {
		return nil
	}
	phase := normalizeSourcePhase(lifecycle)
	files := make([]string, 0)
	for _, sourceFile := range projectConfig.SourceActionFiles {
		if !strings.EqualFold(strings.TrimSpace(sourceFile.Language), "java") &&
			!strings.HasSuffix(strings.ToLower(strings.TrimSpace(sourceFile.Name)), ".java") {
			continue
		}
		sourcePhase := strings.TrimSpace(sourceFile.Phase)
		if sourcePhase == "" {
			sourcePhase = sourceActionPhase(sourceFile.Name)
		}
		if normalizeSourcePhase(sourcePhase) != phase {
			continue
		}
		files = append(files, sourceFile.Name)
	}
	return uniqueSortedStrings(files)
}

func javaExternalLifecycleMetadata(issues []javaExternalLifecycleMethod) []map[string]any {
	metadata := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		metadata = append(metadata, map[string]any{
			"phase":       issue.Phase,
			"class_name":  issue.ClassName,
			"method_name": issue.MethodName,
			"source_file": issue.SourceFile,
			"start_line":  issue.StartLine,
			"end_line":    issue.EndLine,
		})
	}
	return metadata
}

func javaExternalLifecycleFunctionNames(issues []javaExternalLifecycleMethod) []string {
	names := make([]string, 0, len(issues))
	for _, issue := range issues {
		names = append(names, issue.ClassName+"."+issue.MethodName)
	}
	return names
}

func javaExternalLifecycleBlockers(selectedClass string, issues []javaExternalLifecycleMethod) []string {
	blockers := make([]string, 0, len(issues))
	for _, issue := range issues {
		blockers = append(blockers, fmt.Sprintf(
			"Java %s lifecycle method %s.%s exists only outside selected runner class %s; merge all lifecycle methods into one Actions class and re-import before promotion.",
			issue.Phase, issue.ClassName, issue.MethodName, selectedClass,
		))
	}
	return uniqueSortedStrings(blockers)
}

func javaVuserLifecycleStatements(program javaProgram, lifecycle string) []javaStatement {
	method, ok := javaVuserLifecycleMethod(program, lifecycle)
	if !ok {
		return nil
	}
	class, _ := javaVuserClass(program)
	visited := map[string]bool{javaHelperVisitKey(class, method): true}
	return inlineJavaVuserHelperStatements(method.Statements, class, method, program, visited, 0)
}

type javaResolvedHelperMethod struct {
	class  javaClass
	method javaMethod
}

// inlineJavaVuserHelperStatements follows only calls that can be resolved from
// the selected lifecycle: same-class calls, static Class.method calls, and
// qualified instance calls whose declared type is another archived class. It
// intentionally does not scan every method globally, which would make an
// unrelated bundled helper containing lr.start_transaction suppress automatic
// iteration transactions.
func inlineJavaVuserHelperStatements(statements []javaStatement, owner javaClass, current javaMethod, program javaProgram, visited map[string]bool, depth int) []javaStatement {
	if depth >= 12 {
		return append([]javaStatement(nil), statements...)
	}
	return inlineJavaVuserHelperStatementsInScope(
		statements,
		owner,
		current,
		program,
		visited,
		javaDeclaredVariableTypes(owner, current),
		depth,
	)
}

func inlineJavaVuserHelperStatementsInScope(statements []javaStatement, owner javaClass, current javaMethod, program javaProgram, visited map[string]bool, inheritedTypes map[string]string, depth int) []javaStatement {
	if depth >= 12 {
		return append([]javaStatement(nil), statements...)
	}
	variableTypes := cloneJavaVariableTypes(inheritedTypes)
	out := make([]javaStatement, 0, len(statements))
	for _, statement := range statements {
		copy := statement
		callSource := statement.Text
		callVariableTypes := variableTypes
		if statement.Kind == "block" {
			callSource = statement.Header
			childTypes := cloneJavaVariableTypes(variableTypes)
			// Loop/catch/resource declarations in a block header are visible to
			// that block's children, but not to sibling statements afterward.
			collectJavaDeclaredVariableTypes(childTypes, callSource)
			callVariableTypes = childTypes
			copy.Children = inlineJavaVuserHelperStatementsInScope(statement.Children, owner, current, program, visited, childTypes, depth+1)
		}
		out = append(out, copy)
		for _, helper := range javaResolvedHelperCalls(callSource, owner, program, callVariableTypes) {
			key := javaHelperVisitKey(helper.class, helper.method)
			if visited[key] {
				continue
			}
			visited[key] = true
			out = append(out, inlineJavaVuserHelperStatements(helper.method.Statements, helper.class, helper.method, program, visited, depth+1)...)
			delete(visited, key)
		}
		if statement.Kind != "block" {
			// A local declaration becomes visible only after its declarator, so
			// it cannot retroactively redirect a helper call earlier in the same
			// scope. Nested-block declarations stay in the cloned child scope.
			collectJavaDeclaredVariableTypes(variableTypes, statement.Text)
		}
	}
	return out
}

func javaResolvedHelperCalls(source string, owner javaClass, program javaProgram, variableTypes map[string]string) []javaResolvedHelperMethod {
	code := javaCodeOnly(source)
	resolved := make([]javaResolvedHelperMethod, 0)
	seen := map[string]bool{}
	add := func(class javaClass, methodName string, args []string) {
		for _, method := range javaHelperOverloadsForArguments(javaMethodsNamed(class, methodName), args, variableTypes) {
			key := javaHelperVisitKey(class, method)
			if seen[key] {
				continue
			}
			seen[key] = true
			resolved = append(resolved, javaResolvedHelperMethod{class: class, method: method})
		}
	}

	// Resolve unqualified same-class helpers by call arity. The scanner retains
	// every overload, so choosing the legacy name-only map entry here would make
	// source order decide whether a transaction-producing overload was reached.
	for _, match := range javaUnqualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		methodName := code[match[2]:match[3]]
		if !javaUnqualifiedInvocationCandidate(code, match[0], methodName) {
			continue
		}
		args, ok := javaCallArguments(source, code, match[3])
		if !ok {
			continue
		}
		add(owner, methodName, args)
	}

	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		qualifier := strings.TrimSpace(code[match[2]:match[3]])
		methodName := strings.TrimSpace(code[match[4]:match[5]])
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		switch qualifier {
		case "this":
			add(owner, methodName, args)
			continue
		case "super":
			continue
		}
		if class, ok := javaClassForType(program, qualifier); ok {
			add(class, methodName, args)
			continue
		}
		// For this.helper.run(), the regular expression sees helper.run().
		if dot := strings.LastIndexByte(qualifier, '.'); dot >= 0 {
			qualifier = qualifier[dot+1:]
		}
		if typeName := variableTypes[qualifier]; typeName != "" {
			if class, ok := javaClassForType(program, typeName); ok {
				add(class, methodName, args)
			}
		}
	}
	for _, match := range javaNewObjectCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		args, argsOK := javaCallArguments(source, code, match[5])
		if !argsOK {
			continue
		}
		if class, ok := javaClassForType(program, code[match[2]:match[3]]); ok {
			add(class, code[match[4]:match[5]], args)
		}
	}
	return resolved
}

func javaCallArguments(source, code string, methodNameEnd int) ([]string, bool) {
	open := methodNameEnd
	for open < len(code) && (code[open] == ' ' || code[open] == '\t' || code[open] == '\r' || code[open] == '\n') {
		open++
	}
	if open >= len(code) || code[open] != '(' {
		return nil, false
	}
	close := matchingJavaParen(code, open)
	if close < 0 || close > len(source) {
		return nil, false
	}
	args := strings.TrimSpace(source[open+1 : close])
	if args == "" {
		return nil, true
	}
	return splitJavaArgs(args), true
}

type javaOverloadWorkBucketKey struct {
	owner   string
	name    string
	arity   int
	varargs bool
}

type javaOverloadWorkOwnerName struct {
	owner string
	name  string
}

type javaOverloadWorkAnalysis struct {
	blocker                string
	candidateComparisons   int64
	resolutionMemoEntries  int
	indexedOverloadBuckets int
	indexedFixedBuckets    int
	indexedVariableBuckets int
	scopeSnapshotBindings  int64
}

type javaOverloadWorkGuard struct {
	evidenceIndex        *javaEvidenceIndex
	buckets              map[javaOverloadWorkBucketKey]int
	varargArities        map[javaOverloadWorkOwnerName][]int
	varargPrefixCounts   map[javaOverloadWorkOwnerName][]int
	globalMethodsByName  map[string]int
	globalConstructors   int
	programClassCount    int
	staticMethodImports  map[string]javaStaticMethodImportIndex
	staticImportWorkMemo map[string]bool
	resolutionMemo       map[string]int64
	candidateComparisons int64
	exceeded             bool
}

type javaStaticMethodImportIndex struct {
	wildcardOwners int
	methodOwners   map[string]int
}

func analyzeJavaOverloadResolutionWork(program javaProgram) javaOverloadWorkAnalysis {
	return analyzeJavaOverloadResolutionWorkForEntries(
		program,
		collectJavaCombinedMappableEntries(program),
		nil,
	)
}

func analyzeJavaOverloadResolutionWorkForEntries(
	program javaProgram,
	mappable javaCombinedMappableCollection,
	evidenceIndex *javaEvidenceIndex,
) javaOverloadWorkAnalysis {
	if mappable.blocker != "" {
		return javaOverloadWorkAnalysis{
			blocker:               mappable.blocker,
			scopeSnapshotBindings: mappable.scopeSnapshotBindings,
		}
	}
	guard := newJavaOverloadWorkGuardWithEvidenceIndex(program, evidenceIndex)
	for _, entry := range mappable.entries {
		if guard.exceeded {
			break
		}
		guard.inspect(entry)
	}
	if !guard.exceeded {
		guard.inspectEnumConstructors(program)
	}
	analysis := javaOverloadWorkAnalysis{
		candidateComparisons:   guard.candidateComparisons,
		resolutionMemoEntries:  len(guard.resolutionMemo),
		indexedOverloadBuckets: len(guard.buckets),
		scopeSnapshotBindings:  mappable.scopeSnapshotBindings,
	}
	for key := range guard.buckets {
		if key.varargs {
			analysis.indexedVariableBuckets++
		} else {
			analysis.indexedFixedBuckets++
		}
	}
	if guard.exceeded {
		analysis.blocker = fmt.Sprintf(
			"Combined Java overload and runtime-reachability analysis exceeded the deterministic candidate-comparison limit of %d; simplify generated overloads/call sites or split unused support source before re-import. Promotion is blocked because exact reachable-method analysis was not completed.",
			javaOverloadCandidateComparisonLimit,
		)
	}
	return analysis
}

func newJavaOverloadWorkGuard(program javaProgram) *javaOverloadWorkGuard {
	return newJavaOverloadWorkGuardWithEvidenceIndex(program, nil)
}

func newJavaOverloadWorkGuardWithEvidenceIndex(
	program javaProgram,
	evidenceIndex *javaEvidenceIndex,
) *javaOverloadWorkGuard {
	guard := &javaOverloadWorkGuard{
		evidenceIndex:        javaSharedEvidenceIndexForPass(evidenceIndex, program),
		buckets:              make(map[javaOverloadWorkBucketKey]int),
		varargArities:        make(map[javaOverloadWorkOwnerName][]int),
		varargPrefixCounts:   make(map[javaOverloadWorkOwnerName][]int),
		globalMethodsByName:  make(map[string]int),
		programClassCount:    len(program.Classes),
		staticMethodImports:  make(map[string]javaStaticMethodImportIndex),
		staticImportWorkMemo: make(map[string]bool),
		resolutionMemo:       make(map[string]int64),
	}
	add := func(owner javaClass, method javaMethod, name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return
		}
		arity := method.ParameterCount
		if method.Varargs {
			if method.ParameterCount <= 0 {
				return
			}
			arity--
		}
		ownerKey := javaEvidenceOwnerKey(owner)
		for _, indexedOwner := range []string{ownerKey, ""} {
			key := javaOverloadWorkBucketKey{
				owner:   indexedOwner,
				name:    name,
				arity:   arity,
				varargs: method.Varargs,
			}
			guard.buckets[key]++
		}
		if name != "<init>" {
			guard.globalMethodsByName[name]++
		} else {
			guard.globalConstructors++
		}
	}
	for _, class := range program.Classes {
		for _, constructor := range class.Constructors {
			add(class, constructor, "<init>")
		}
		for _, method := range sortedJavaMethods(class) {
			add(class, method, method.Name)
		}
	}
	for key := range guard.buckets {
		if !key.varargs {
			continue
		}
		ownerName := javaOverloadWorkOwnerName{owner: key.owner, name: key.name}
		guard.varargArities[ownerName] = append(guard.varargArities[ownerName], key.arity)
	}
	for ownerName, arities := range guard.varargArities {
		sort.Ints(arities)
		guard.varargArities[ownerName] = arities
		prefix := make([]int, len(arities))
		total := 0
		for index, minimum := range arities {
			total += guard.buckets[javaOverloadWorkBucketKey{
				owner:   ownerName.owner,
				name:    ownerName.name,
				arity:   minimum,
				varargs: true,
			}]
			prefix[index] = total
		}
		guard.varargPrefixCounts[ownerName] = prefix
	}
	return guard
}

func (guard *javaOverloadWorkGuard) inspect(entry javaCombinedMappableEntry) {
	if guard == nil || guard.exceeded || entry.statement.Kind == "unparsed" {
		return
	}
	source := entry.statement.Text
	code := javaCodeOnly(source)
	if strings.TrimSpace(code) == "" {
		return
	}
	for _, match := range javaUnqualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		methodName := code[match[2]:match[3]]
		if !javaUnqualifiedInvocationCandidate(code, match[0], methodName) {
			continue
		}
		args, ok := javaCallArguments(source, code, match[3])
		if !ok {
			continue
		}
		owner := entry.owner
		globalFallback := guard.hasStaticMethodImport(owner, methodName)
		if globalFallback {
			guard.chargeStaticImportWork(owner, methodName)
			if guard.exceeded {
				return
			}
		}
		guard.recordCall(&owner, methodName, args, entry.variableTypes, true, globalFallback)
		if guard.exceeded {
			return
		}
	}
	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		qualifier := strings.TrimSpace(code[match[2]:match[3]])
		methodName := strings.TrimSpace(code[match[4]:match[5]])
		if qualifier == "super" {
			if class, ok := guard.evidenceIndex.superclass(entry.owner); ok {
				guard.recordCall(&class, methodName, args, entry.variableTypes, false, false)
			}
			continue
		}
		if qualifier == "this" {
			owner := entry.owner
			guard.recordCall(&owner, methodName, args, entry.variableTypes, true, false)
			continue
		}
		if strings.Contains(qualifier, ".") {
			valueRoot, rootLimitExceeded := guard.evidenceIndex.memberReceiverHasValueRoot(
				entry.owner,
				qualifier,
				entry.variableTypes,
			)
			if rootLimitExceeded {
				guard.recordCall(nil, methodName, args, entry.variableTypes, true, true)
				continue
			}
			if valueRoot {
				if class, _, exact, _ := guard.evidenceIndex.resolveMemberReceiver(
					entry.owner,
					qualifier,
					entry.variableTypes,
				); exact {
					guard.recordCall(&class, methodName, args, entry.variableTypes, true, false)
				}
				continue
			}
			if class, resolved := guard.evidenceIndex.classForType(entry.owner, qualifier); resolved {
				guard.recordCall(&class, methodName, args, entry.variableTypes, false, false)
				continue
			}
			if class, archived, exact, _ := guard.evidenceIndex.resolveMemberReceiver(
				entry.owner,
				qualifier,
				entry.variableTypes,
			); exact {
				guard.recordCall(&class, methodName, args, entry.variableTypes, true, false)
				continue
			} else if archived {
				continue
			}
		}
		valueName := qualifier
		if dot := strings.LastIndexByte(valueName, '.'); dot >= 0 {
			valueName = valueName[dot+1:]
		}
		if typeName := entry.variableTypes[valueName]; typeName != "" {
			if class, resolved := guard.evidenceIndex.classForType(entry.owner, typeName); resolved {
				guard.recordCall(&class, methodName, args, entry.variableTypes, true, false)
			}
			continue
		}
		if class, resolved := guard.evidenceIndex.classForType(entry.owner, qualifier); resolved {
			guard.recordCall(&class, methodName, args, entry.variableTypes, false, false)
		}
		if guard.exceeded {
			return
		}
	}
	for _, match := range javaObjectConstructionRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		args, ok := javaCallArguments(source, code, match[3])
		if !ok {
			continue
		}
		if class, resolved := guard.evidenceIndex.classForType(entry.owner, code[match[2]:match[3]]); resolved {
			guard.recordCall(&class, "<init>", args, entry.variableTypes, false, false)
		}
		if guard.exceeded {
			return
		}
	}
	if entry.current.Name == entry.owner.Name {
		for _, match := range javaThisConstructorCallRe.FindAllStringIndex(code, -1) {
			args, ok := javaCallArguments(source, code, match[1]-1)
			if ok {
				owner := entry.owner
				guard.recordCall(&owner, "<init>", args, entry.variableTypes, false, false)
			}
		}
		for _, match := range javaSuperConstructorCallRe.FindAllStringIndex(code, -1) {
			args, ok := javaCallArguments(source, code, match[1]-1)
			if !ok {
				continue
			}
			if class, resolved := guard.evidenceIndex.superclass(entry.owner); resolved {
				guard.recordCall(&class, "<init>", args, entry.variableTypes, false, false)
			}
		}
	}
	for _, match := range javaNewObjectCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		class, resolved := guard.evidenceIndex.classForType(entry.owner, code[match[2]:match[3]])
		if !resolved {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if ok {
			guard.recordCall(&class, code[match[4]:match[5]], args, entry.variableTypes, true, false)
		}
		if guard.exceeded {
			return
		}
	}
	for _, match := range javaParenthesizedReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		valueName := strings.TrimSpace(code[match[2]:match[3]])
		class, resolved := guard.evidenceIndex.classForType(entry.owner, entry.variableTypes[valueName])
		if !resolved {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if ok {
			guard.recordCall(&class, code[match[4]:match[5]], args, entry.variableTypes, true, false)
		}
	}
	for _, match := range javaCastReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		if class, resolved := guard.evidenceIndex.classForType(entry.owner, code[match[2]:match[3]]); resolved {
			guard.recordCall(&class, code[match[4]:match[5]], args, entry.variableTypes, true, false)
		}
	}
	for _, match := range javaArrayReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		name := strings.TrimSpace(code[match[2]:match[3]])
		class, resolved := guard.evidenceIndex.classForType(entry.owner, entry.variableTypes[name])
		if !resolved {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if ok {
			guard.recordCall(&class, code[match[4]:match[5]], args, entry.variableTypes, true, false)
		}
	}
	for _, call := range javaReturnedReceiverCalls(source) {
		// Determining the returned receiver requires overload resolution of the
		// producer. The producer itself was recorded above; use the global
		// owner/name/arity bucket for the consumer so a generated returned-
		// receiver chain cannot bypass the preflight budget.
		for _, link := range call.links() {
			if !link.isMethod() {
				continue
			}
			guard.recordCall(nil, link.methodName, link.args, entry.variableTypes, true, true)
			if guard.exceeded {
				return
			}
		}
	}
	for _, match := range javaMethodReferenceNameRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		guard.recordUnknownArityCall(code[match[4]:match[5]])
		if guard.exceeded {
			return
		}
	}
}

func (guard *javaOverloadWorkGuard) inspectEnumConstructors(program javaProgram) {
	if guard == nil || guard.exceeded {
		return
	}
	for _, class := range program.Classes {
		if !strings.EqualFold(strings.TrimSpace(class.Kind), "enum") {
			continue
		}
		fieldTypes := javaDeclaredVariableTypes(class, javaMethod{})
		for _, field := range class.Fields {
			for _, call := range javaEnumConstructorCalls(class, field) {
				if !call.argsOK {
					continue
				}
				owner := class
				guard.recordCall(
					&owner,
					"<init>",
					call.args,
					fieldTypes,
					false,
					false,
				)
				if guard.exceeded {
					return
				}
			}
		}
	}
}

func (guard *javaOverloadWorkGuard) hasStaticMethodImport(
	owner javaClass,
	methodName string,
) bool {
	if guard == nil {
		return false
	}
	cacheKey := strings.ToLower(strings.TrimSpace(owner.File))
	imports, cached := guard.staticMethodImports[cacheKey]
	if cached {
		return imports.wildcardOwners > 0 || imports.methodOwners[methodName] > 0
	}
	imports.methodOwners = make(map[string]int)
	for _, rawImport := range owner.Imports {
		imported := strings.TrimSpace(rawImport)
		if !strings.HasPrefix(imported, "static ") {
			continue
		}
		imported = strings.TrimSpace(strings.TrimPrefix(imported, "static "))
		if strings.HasSuffix(imported, ".*") {
			imports.wildcardOwners++
			continue
		}
		if dot := strings.LastIndexByte(imported, '.'); dot >= 0 && dot < len(imported)-1 {
			imports.methodOwners[imported[dot+1:]]++
		}
	}
	guard.staticMethodImports[cacheKey] = imports
	return imports.wildcardOwners > 0 || imports.methodOwners[methodName] > 0
}

func (guard *javaOverloadWorkGuard) chargeStaticImportWork(
	owner javaClass,
	methodName string,
) {
	if guard == nil || guard.exceeded {
		return
	}
	ownerKey := strings.ToLower(strings.TrimSpace(owner.File))
	memoKey := ownerKey + "\x00" + methodName
	if guard.staticImportWorkMemo[memoKey] {
		return
	}
	guard.staticImportWorkMemo[memoKey] = true
	// javaEvidenceStaticImportedMethods caches by source scope and method, but
	// its first lookup must inspect every wildcard owner plus every exact owner
	// for this name. Charge that real fan-out once per cache key so thousands of
	// wildcard imports and distinct calls cannot bypass overload preflight.
	imports := guard.staticMethodImports[ownerKey]
	guard.charge(int64(imports.wildcardOwners + imports.methodOwners[methodName]))
}

func (guard *javaOverloadWorkGuard) recordCall(
	owner *javaClass,
	methodName string,
	args []string,
	variableTypes map[string]string,
	virtual bool,
	globalFallback bool,
) {
	if guard == nil || guard.exceeded {
		return
	}
	name := strings.ToLower(strings.TrimSpace(methodName))
	ownerKey := ""
	if owner != nil {
		ownerKey = javaEvidenceOwnerKey(*owner)
	}
	argTypes := make([]string, len(args))
	for index, arg := range args {
		argTypes[index] = javaStaticArgumentType(arg, variableTypes)
		if argTypes[index] == "" {
			argTypes[index] = "?"
		}
	}
	memoKey := strings.Join([]string{
		ownerKey,
		name,
		strconv.Itoa(len(args)),
		strings.Join(argTypes, ","),
		strconv.FormatBool(virtual),
		strconv.FormatBool(globalFallback),
	}, "\x00")
	projected, memoized := guard.resolutionMemo[memoKey]
	if !memoized {
		candidates, lookupWork := guard.candidateCount(owner, name, len(args), globalFallback)
		projected = int64(candidates + lookupWork)
		if virtual && candidates > 0 && name != "<init>" {
			globalMethods := int64(guard.globalMethodsByName[name])
			if globalMethods < 1 {
				globalMethods = 1
			}
			if owner == nil ||
				strings.EqualFold(strings.TrimSpace(owner.Kind), "interface") {
				// Interface dispatch visits every archived subtype, including
				// implementors that inherit the method and therefore do not
				// appear in methodClasses. Unknown returned receivers must use
				// the same conservative bound.
				projected += int64(guard.programClassCount) * globalMethods
			} else {
				projected += int64(candidates) * globalMethods
			}
		}
		argumentFactor := int64(len(args) + 4)
		if projected > javaOverloadCandidateComparisonLimit/
			javaOverloadResolutionPassMultiplier/argumentFactor {
			projected = javaOverloadCandidateComparisonLimit + 1
		} else {
			projected *= javaOverloadResolutionPassMultiplier * argumentFactor
		}
		guard.resolutionMemo[memoKey] = projected
	}
	guard.charge(projected)
}

func (guard *javaOverloadWorkGuard) recordUnknownArityCall(methodName string) {
	if guard == nil || guard.exceeded {
		return
	}
	name := strings.ToLower(strings.TrimSpace(methodName))
	if name == "" {
		return
	}
	memoKey := "method_reference\x00" + name
	projected, memoized := guard.resolutionMemo[memoKey]
	if !memoized {
		candidates := int64(guard.globalMethodsByName[name])
		if name == "new" {
			candidates = int64(guard.globalConstructors)
		}
		if candidates > javaOverloadCandidateComparisonLimit/
			javaOverloadResolutionPassMultiplier/4 {
			projected = javaOverloadCandidateComparisonLimit + 1
		} else {
			projected = candidates * javaOverloadResolutionPassMultiplier * 4
		}
		guard.resolutionMemo[memoKey] = projected
	}
	guard.charge(projected)
}

func (guard *javaOverloadWorkGuard) candidateCount(
	owner *javaClass,
	name string,
	argumentCount int,
	globalFallback bool,
) (int, int) {
	if owner == nil {
		return guard.bucketCount("", name, argumentCount), 1
	}
	if name == "<init>" {
		return guard.bucketCount(javaEvidenceOwnerKey(*owner), name, argumentCount), 1
	}
	count := 0
	lookupWork := 0
	current := *owner
	seen := make(map[string]bool)
	for {
		key := javaEvidenceOwnerKey(current)
		if seen[key] {
			break
		}
		seen[key] = true
		lookupWork++
		count += guard.bucketCount(key, name, argumentCount)
		superClass, ok := guard.evidenceIndex.superclass(current)
		if !ok {
			break
		}
		current = superClass
		if lookupWork > int(javaOverloadCandidateComparisonLimit) {
			break
		}
	}
	if count == 0 && globalFallback {
		count = guard.bucketCount("", name, argumentCount)
		lookupWork++
	}
	return count, lookupWork
}

func (guard *javaOverloadWorkGuard) bucketCount(owner, name string, argumentCount int) int {
	count := guard.buckets[javaOverloadWorkBucketKey{
		owner: owner,
		name:  name,
		arity: argumentCount,
	}]
	ownerName := javaOverloadWorkOwnerName{owner: owner, name: name}
	arities := guard.varargArities[ownerName]
	eligible := sort.Search(len(arities), func(index int) bool {
		return arities[index] > argumentCount
	})
	if eligible > 0 {
		count += guard.varargPrefixCounts[ownerName][eligible-1]
	}
	return count
}

func (guard *javaOverloadWorkGuard) charge(work int64) {
	if guard == nil || guard.exceeded || work <= 0 {
		return
	}
	if work > javaOverloadCandidateComparisonLimit-guard.candidateComparisons {
		guard.candidateComparisons = javaOverloadCandidateComparisonLimit + 1
		guard.exceeded = true
		return
	}
	guard.candidateComparisons += work
}

func javaHelperOverloadsForArguments(methods []javaMethod, args []string, variableTypes map[string]string) []javaMethod {
	candidates := make([]javaMethod, 0, len(methods))
	for _, method := range methods {
		if javaMethodAcceptsArgumentCount(method, len(args)) {
			candidates = append(candidates, method)
		}
	}
	if len(candidates) <= 1 {
		return candidates
	}

	argTypes := make([]string, len(args))
	for index, arg := range args {
		argTypes[index] = javaStaticArgumentType(arg, variableTypes)
		if argTypes[index] == "" {
			// Source analysis cannot reproduce Java overload resolution without
			// a static argument type. Preserve every same-arity target as
			// conservatively reachable: runtime method analysis can inspect all
			// candidates, while constructor analysis requires exactly one and
			// therefore records a promotion blocker.
			return candidates
		}
	}
	// Java resolves applicable fixed-arity methods before it considers an
	// expanded variable-arity invocation. Preserve that ordering when the
	// compact type model can prove compatibility. If it cannot, retain all
	// arity-compatible candidates so runtime review fails closed.
	fixedCandidates := make([]javaMethod, 0, len(candidates))
	varargsCandidates := make([]javaMethod, 0, len(candidates))
	for _, method := range candidates {
		if method.Varargs {
			varargsCandidates = append(varargsCandidates, method)
			// A varargs declaration is also considered during Java's
			// fixed-arity phase when the invocation supplies exactly one array
			// argument for its final declared parameter.
			if len(args) != method.ParameterCount {
				continue
			}
		}
		fixedCandidates = append(fixedCandidates, method)
	}
	if best, uncertain := javaBestTypedOverloads(fixedCandidates, argTypes, false); len(best) > 0 && !uncertain {
		return best
	}
	if len(fixedCandidates) > 0 {
		return candidates
	}
	if best, uncertain := javaBestTypedOverloads(varargsCandidates, argTypes, true); len(best) > 0 && !uncertain {
		return best
	}
	// The compact type scorer intentionally does not attempt Java's full
	// reference-conversion lattice. Falling back to every arity-compatible
	// candidate is safer than silently dropping a valid archived target.
	return candidates
}

func javaMethodAcceptsArgumentCount(method javaMethod, argumentCount int) bool {
	if !method.Varargs {
		return method.ParameterCount == argumentCount
	}
	// A legal Java varargs declaration always has a final array parameter.
	// Malformed scanner metadata must not make every arity appear applicable.
	return method.ParameterCount > 0 && argumentCount >= method.ParameterCount-1
}

func javaBestTypedOverloads(methods []javaMethod, argTypes []string, expandVarargs bool) ([]javaMethod, bool) {
	bestScore := -1
	bestPhase := 3
	var best []javaMethod
	uncertain := false
	for _, method := range methods {
		score := 0
		phase := 0
		compatible := true
		candidateUncertain := false
		for index, argumentType := range argTypes {
			parameterIndex := index
			if expandVarargs && method.Varargs && parameterIndex >= method.ParameterCount-1 {
				parameterIndex = method.ParameterCount - 1
			}
			if parameterIndex < 0 || parameterIndex >= len(method.Parameters) {
				compatible = false
				break
			}
			parameterType := javaOverloadParameterType(method, parameterIndex, expandVarargs)
			matchScore := javaTypeMatchScore(argumentType, parameterType)
			if matchScore < 0 {
				if javaTypeMismatchDefinitelyIncompatible(
					argumentType,
					parameterType,
				) {
					compatible = false
				} else {
					candidateUncertain = true
				}
				continue
			}
			score += matchScore
			if matchPhase := javaTypeMatchPhase(argumentType, parameterType); matchPhase > phase {
				phase = matchPhase
			}
		}
		if !compatible {
			continue
		}
		if candidateUncertain {
			uncertain = true
			continue
		}
		switch {
		case phase < bestPhase:
			bestPhase = phase
			bestScore = score
			best = []javaMethod{method}
		case phase > bestPhase:
			continue
		case score > bestScore:
			bestScore = score
			best = []javaMethod{method}
		case score == bestScore:
			best = append(best, method)
		}
	}
	return best, uncertain
}

func javaTypeMatchPhase(argumentType, parameterType string) int {
	argumentType = javaComparableOverloadType(argumentType)
	parameterType = javaComparableOverloadType(parameterType)
	if argumentType == parameterType {
		return 0
	}
	if strings.Contains(argumentType, "[]") || strings.Contains(parameterType, "[]") {
		// The only non-identity array match the compact scorer proves is array
		// widening to Object, which belongs to strict invocation.
		return 0
	}
	argumentPrimitive := javaPrimitiveComparableType(argumentType)
	parameterPrimitive := javaPrimitiveComparableType(parameterType)
	switch {
	case argumentPrimitive && parameterPrimitive:
		return 0 // primitive widening
	case !argumentPrimitive && !parameterPrimitive:
		return 0 // reference widening (including Object)
	default:
		return 1 // boxing or unboxing belongs to loose invocation
	}
}

func javaOverloadParameterType(method javaMethod, parameterIndex int, expandVarargs bool) string {
	if parameterIndex < 0 || parameterIndex >= len(method.Parameters) {
		return ""
	}
	parameter := method.Parameters[parameterIndex]
	typeName := parameter.Type
	if parameter.Array {
		typeName += "[]"
	}
	if method.Varargs && !expandVarargs && parameterIndex == method.ParameterCount-1 {
		typeName += "[]"
	}
	return javaComparableOverloadType(typeName)
}

func javaTypeMismatchDefinitelyIncompatible(argumentType, parameterType string) bool {
	argumentType = javaComparableOverloadType(argumentType)
	parameterType = javaComparableOverloadType(parameterType)
	argumentArrays := strings.Count(argumentType, "[]")
	parameterArrays := strings.Count(parameterType, "[]")
	if parameterArrays > 0 && argumentArrays == 0 {
		return true
	}
	if argumentArrays > 0 || parameterArrays > 0 {
		if argumentArrays == parameterArrays {
			argumentBase := javaBoxedPrimitive(strings.TrimSuffix(argumentType, strings.Repeat("[]", argumentArrays)))
			parameterBase := javaBoxedPrimitive(strings.TrimSuffix(parameterType, strings.Repeat("[]", parameterArrays)))
			return javaPrimitiveComparableType(argumentBase) &&
				javaPrimitiveComparableType(parameterBase)
		}
		return false
	}
	argumentWasPrimitive := javaPrimitiveComparableType(argumentType)
	if argumentWasPrimitive && !javaPrimitiveComparableType(parameterType) {
		return !javaPrimitiveBoxingReferenceMayConvert(argumentType, parameterType)
	}
	argumentType = javaBoxedPrimitive(argumentType)
	parameterType = javaBoxedPrimitive(parameterType)
	return javaPrimitiveComparableType(argumentType) &&
		javaPrimitiveComparableType(parameterType)
}

func javaPrimitiveBoxingReferenceMayConvert(primitiveType, referenceType string) bool {
	referenceType = javaComparableOverloadType(referenceType)
	switch referenceType {
	case "object", "serializable", "comparable", "constable", "constantdesc":
		return true
	case "number":
		return primitiveType != "boolean" && primitiveType != "char"
	}
	return javaBoxedPrimitive(referenceType) == primitiveType
}

func javaPrimitiveComparableType(typeName string) bool {
	switch typeName {
	case "boolean", "byte", "short", "char", "int", "long", "float", "double":
		return true
	default:
		return false
	}
}

func javaStaticArgumentType(argument string, variableTypes map[string]string) string {
	argument = strings.TrimSpace(argument)
	if argument == "" || argument == "null" {
		return ""
	}
	if match := javaLRValueCallRe.FindStringSubmatch(argument); len(match) == 2 {
		if returnType := javaKnownLRReturnTypes[strings.ToLower(strings.TrimSpace(match[1]))]; returnType != "" {
			return returnType
		}
	}
	if value, ok := parseJavaStringLiteral(argument); ok {
		_ = value
		return "string"
	}
	if len(argument) >= 3 && argument[0] == '\'' && argument[len(argument)-1] == '\'' {
		return "char"
	}
	switch strings.ToLower(argument) {
	case "true", "false":
		return "boolean"
	}
	if match := regexp.MustCompile(`(?i)^new\s+([A-Za-z_$][A-Za-z0-9_$.]*)`).FindStringSubmatch(argument); len(match) == 2 {
		typeName := match[1]
		suffix := strings.TrimSpace(argument[len(match[0]):])
		for strings.HasPrefix(suffix, "[") {
			if close := strings.IndexByte(suffix, ']'); close >= 0 {
				typeName += "[]"
				suffix = strings.TrimSpace(suffix[close+1:])
				continue
			}
			break
		}
		return javaComparableOverloadType(typeName)
	}
	if match := regexp.MustCompile(`^\(\s*([A-Za-z_$][A-Za-z0-9_$.]*(?:\s*\[\])*)\s*\)`).FindStringSubmatch(argument); len(match) == 2 {
		return javaComparableOverloadType(match[1])
	}
	if regexp.MustCompile(`^[+-]?[0-9][0-9_]*[lL]$`).MatchString(argument) {
		return "long"
	}
	if regexp.MustCompile(`^[+-]?(?:[0-9][0-9_]*\.[0-9_]*|[0-9][0-9_]*[eE][+-]?[0-9_]+)[fF]$`).MatchString(argument) {
		return "float"
	}
	if regexp.MustCompile(`^[+-]?(?:[0-9][0-9_]*\.[0-9_]*|[0-9][0-9_]*[eE][+-]?[0-9_]+)(?:[dD])?$`).MatchString(argument) {
		return "double"
	}
	if regexp.MustCompile(`^[+-]?[0-9][0-9_]*$`).MatchString(argument) {
		return "int"
	}
	if typeName := variableTypes[argument]; typeName != "" {
		return javaComparableOverloadType(typeName)
	}
	return ""
}

func javaTypeMatchScore(argumentType, parameterType string) int {
	argumentType = javaComparableOverloadType(argumentType)
	parameterType = javaComparableOverloadType(parameterType)
	if argumentType == "" || parameterType == "" {
		return -1
	}
	if argumentType == parameterType {
		return 4
	}
	if strings.Contains(argumentType, "[]") || strings.Contains(parameterType, "[]") {
		if parameterType == "object" {
			return 1
		}
		return -1
	}
	if javaBoxedPrimitive(argumentType) == javaBoxedPrimitive(parameterType) {
		return 3
	}
	if parameterType == "object" {
		return 1
	}
	widening := map[string]string{
		"byte":  "short,int,long,float,double",
		"short": "int,long,float,double",
		"char":  "int,long,float,double",
		"int":   "long,float,double",
		"long":  "float,double",
		"float": "double",
	}
	if strings.Contains(","+widening[argumentType]+",", ","+parameterType+",") {
		return 2
	}
	return -1
}

func javaComparableOverloadType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	arrayDepth := 0
	for {
		trimmed := strings.TrimSpace(value)
		if !strings.HasSuffix(trimmed, "[]") {
			value = trimmed
			break
		}
		arrayDepth++
		value = strings.TrimSpace(strings.TrimSuffix(trimmed, "[]"))
	}
	value = javaComparableType(value)
	return value + strings.Repeat("[]", arrayDepth)
}

func javaComparableType(value string) string {
	value = strings.ToLower(strings.TrimSpace(javaSimpleTypeName(value)))
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		value = value[dot+1:]
	}
	return value
}

func javaBoxedPrimitive(value string) string {
	switch value {
	case "boolean", "bool":
		return "boolean"
	case "byte":
		return "byte"
	case "short":
		return "short"
	case "char", "character":
		return "char"
	case "int", "integer":
		return "int"
	case "long":
		return "long"
	case "float":
		return "float"
	case "double":
		return "double"
	default:
		return value
	}
}

func javaDeclaredVariableTypes(owner javaClass, current javaMethod) map[string]string {
	types := map[string]string{}
	// Seed only direct fields owned by this class. Scanning owner.Source would
	// also ingest locals from unrelated methods, allowing a later same-name
	// declaration to redirect reachability for the lifecycle currently being
	// analyzed.
	for _, field := range owner.Fields {
		collectJavaDeclaredVariableTypes(types, field.Text)
	}
	// Java parameters shadow fields for the entire current method. Locals are
	// added sequentially by inlineJavaVuserHelperStatementsInScope so their
	// declaration order and block scope are respected.
	for _, parameter := range current.Parameters {
		if parameter.Name != "" && parameter.Type != "" {
			typeName := parameter.Type
			if parameter.Array || parameter.Varargs {
				typeName += "[]"
			}
			types[parameter.Name] = typeName
		}
	}
	return types
}

func collectJavaDeclaredVariableTypes(types map[string]string, source string) {
	code := javaCodeOnly(source)
	for _, match := range javaVarObjectDeclRe.FindAllStringSubmatch(code, -1) {
		if len(match) == 3 {
			types[strings.TrimSpace(match[1])] = strings.TrimSpace(match[2])
		}
	}
	for _, match := range javaVariableDeclRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(match[1]), "var") {
			if _, inferred := types[strings.TrimSpace(match[2])]; inferred {
				continue
			}
		}
		// Preserve array shape for conservative varargs overload resolution.
		// Receiver/type lookup normalizes it through javaSimpleTypeName later.
		types[strings.TrimSpace(match[2])] = strings.TrimSpace(match[1])
	}
}

func cloneJavaVariableTypes(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for name, typeName := range source {
		cloned[name] = typeName
	}
	return cloned
}

func javaSimpleTypeName(value string) string {
	value = strings.TrimSpace(value)
	if generic := strings.IndexByte(value, '<'); generic >= 0 {
		value = value[:generic]
	}
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "[]"))
	return value
}

func isJavaIdentifierByte(value byte) bool {
	return value == '_' || value == '$' ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func javaClassForType(program javaProgram, typeName string) (javaClass, bool) {
	typeName = javaSimpleTypeName(typeName)
	for _, class := range program.Classes {
		if strings.EqualFold(typeName, class.Name) ||
			strings.EqualFold(typeName, javaQualifiedClassName(class)) ||
			strings.EqualFold(typeName, strings.TrimSuffix(filepath.Base(class.File), filepath.Ext(class.File))) {
			return class, true
		}
	}
	return javaClass{}, false
}

func javaHelperVisitKey(class javaClass, method javaMethod) string {
	return javaEvidenceOwnerKey(class) +
		"::" + strings.ToLower(strings.TrimSpace(method.Name)) +
		"(" + strings.ToLower(strings.TrimSpace(method.ParameterSignature)) + ")" +
		"@" + strconv.Itoa(method.StartLine)
}

func javaVuserLeafStatements(program javaProgram) []javaStatement {
	var statements []javaStatement
	for _, lifecycle := range []string{"init", "action", "teardown"} {
		for _, statement := range javaVuserLifecycleStatements(program, lifecycle) {
			statements = append(statements, javaLeafStatementList(statement)...)
		}
	}
	return statements
}

func javaVuserLifecyclePhases(program javaProgram) []string {
	phases := make([]string, 0, 3)
	if javaProgramHasLifecycleMethod(program, "init") {
		phases = append(phases, "init")
	}
	phases = append(phases, "action")
	if javaProgramHasLifecycleMethod(program, "teardown") {
		phases = append(phases, "end")
	}
	return phases
}

type javaRuntimeReachableStatement struct {
	statement      javaStatement
	actionRepeated bool
	phases         map[string]bool
	owner          javaClass
	current        javaMethod
	variableTypes  map[string]string
}

type javaVuserRuntimeReachability struct {
	statements        []javaRuntimeReachableStatement
	unresolved        []string
	byOccurrence      map[string]int
	byStatement       map[string][]int
	scopeReachability map[string]javaRuntimeReachableStatement
	evidenceIndex     *javaEvidenceIndex
}

func buildJavaVuserRuntimeReachability(program javaProgram) javaVuserRuntimeReachability {
	return buildJavaVuserRuntimeReachabilityWithEvidenceIndex(program, nil)
}

// buildJavaVuserRuntimeReachabilityWithEvidenceIndex lets a live import reuse the
// evidence index the earlier passes already built instead of rescanning every
// class source again.
func buildJavaVuserRuntimeReachabilityWithEvidenceIndex(
	program javaProgram,
	sharedIndex *javaEvidenceIndex,
) javaVuserRuntimeReachability {
	selected, ok := javaVuserClass(program)
	if !ok {
		return javaVuserRuntimeReachability{}
	}
	evidenceIndex := javaSharedEvidenceIndexForPass(sharedIndex, program)
	builder := &javaRuntimeReachabilityBuilder{
		index:          evidenceIndex,
		constantFields: newJavaConstantFieldResolver(evidenceIndex),
		simpleTypes:    javaRuntimeSimpleTypeIndex(program),
		reachability: javaVuserRuntimeReachability{
			byOccurrence:      map[string]int{},
			byStatement:       map[string][]int{},
			scopeReachability: map[string]javaRuntimeReachableStatement{},
			evidenceIndex:     evidenceIndex,
		},
		staticInitialized:   map[string]bool{},
		methodStack:         map[string]bool{},
		methodComplete:      map[string]bool{},
		constructorStack:    map[string]bool{},
		constructorComplete: map[string]bool{},
		classFieldTypes:     map[string]map[string]string{},
	}
	builder.constructSelectedActions(selected)
	for _, lifecycle := range []struct {
		name     string
		phase    string
		repeated bool
	}{
		{name: "init", phase: "init"},
		{name: "action", phase: "action", repeated: true},
		{name: "teardown", phase: "teardown"},
	} {
		if method, exists := javaVuserLifecycleMethod(program, lifecycle.name); exists {
			builder.walkMethod(
				selected,
				method,
				javaRuntimeOccurrence{phase: lifecycle.phase, repeated: lifecycle.repeated},
			)
		}
	}
	builder.reachability.unresolved = uniqueSortedStrings(builder.reachability.unresolved)
	return builder.reachability
}

type javaRuntimeOccurrence struct {
	phase    string
	repeated bool
}

type javaRuntimeReachabilityBuilder struct {
	index               *javaEvidenceIndex
	constantFields      *javaConstantFieldResolver
	simpleTypes         map[string]bool
	reachability        javaVuserRuntimeReachability
	staticInitialized   map[string]bool
	methodStack         map[string]bool
	methodComplete      map[string]bool
	constructorStack    map[string]bool
	constructorComplete map[string]bool
	classFieldTypes     map[string]map[string]string
}

func (builder *javaRuntimeReachabilityBuilder) fieldTypes(
	class javaClass,
) map[string]string {
	key := javaEvidenceOwnerKey(class)
	if cached, ok := builder.classFieldTypes[key]; ok {
		return cached
	}
	types := builder.index.canonicalHierarchyFieldTypes(class)
	builder.classFieldTypes[key] = types
	return types
}

func (builder *javaRuntimeReachabilityBuilder) constructSelectedActions(class javaClass) {
	occurrence := javaRuntimeOccurrence{phase: "construction"}
	builder.initializeStaticClass(class, occurrence)
	for _, constructor := range class.Constructors {
		if javaMethodIsPublicZeroArg(constructor) {
			builder.walkConstructor(class, &constructor, occurrence)
			return
		}
	}
	// No explicit constructor means Java supplies the public zero-argument
	// constructor used by Harness.getDeclaredConstructor().newInstance().
	if len(class.Constructors) == 0 {
		builder.walkConstructor(class, nil, occurrence)
	}
}

func (builder *javaRuntimeReachabilityBuilder) addStatement(
	statement javaStatement,
	occurrence javaRuntimeOccurrence,
	scopeKey string,
	statementPath string,
	owner javaClass,
	current javaMethod,
	variableTypes map[string]string,
) {
	if statement.Kind == "block" {
		for index, child := range statement.Children {
			builder.addStatement(
				child,
				occurrence,
				scopeKey,
				javaRuntimeChildPath(statementPath, index),
				owner,
				current,
				variableTypes,
			)
		}
		return
	}
	text := javaNormalizeStatementText(statement.Text)
	if text == "" {
		return
	}
	occurrenceKey := javaRuntimeOccurrenceKey(scopeKey, statementPath)
	if index, exists := builder.reachability.byOccurrence[occurrenceKey]; exists {
		reachable := &builder.reachability.statements[index]
		mergeJavaRuntimeOccurrence(reachable, occurrence)
		builder.mergeScopeReachability(scopeKey, occurrence)
		return
	}
	index := len(builder.reachability.statements)
	builder.reachability.byOccurrence[occurrenceKey] = index
	statementKey := javaRuntimeStatementKey(statement)
	builder.reachability.byStatement[statementKey] = append(
		builder.reachability.byStatement[statementKey],
		index,
	)
	builder.reachability.statements = append(builder.reachability.statements, javaRuntimeReachableStatement{
		statement:      statement,
		actionRepeated: occurrence.phase == "action" && occurrence.repeated,
		phases:         map[string]bool{occurrence.phase: true},
		owner:          owner,
		current:        current,
		variableTypes:  cloneJavaVariableTypes(variableTypes),
	})
	builder.mergeScopeReachability(scopeKey, occurrence)
}

func javaRuntimeStatementKey(statement javaStatement) string {
	return strings.Join([]string{
		javaReviewPathKey(statement.File),
		strconv.Itoa(statement.StartLine),
		strconv.Itoa(statement.EndLine),
		javaNormalizeStatementText(statement.Text),
	}, "\x00")
}

func javaRuntimeOccurrenceKey(scopeKey, statementPath string) string {
	return scopeKey + "\x00" + statementPath
}

func javaRuntimeChildPath(parent string, index int) string {
	child := strconv.Itoa(index)
	if parent == "" {
		return child
	}
	return parent + "/" + child
}

func javaRuntimeMethodScopeKey(class javaClass, method javaMethod) string {
	return "method\x00" + javaHelperVisitKey(class, method)
}

func javaRuntimeFieldScopeKey(class javaClass, index int) string {
	return "field\x00" + javaEvidenceOwnerKey(class) + "\x00" + strconv.Itoa(index)
}

func javaRuntimeInitializerScopeKey(class javaClass, index int) string {
	return "initializer\x00" + javaEvidenceOwnerKey(class) + "\x00" + strconv.Itoa(index)
}

func mergeJavaRuntimeOccurrence(
	reachable *javaRuntimeReachableStatement,
	occurrence javaRuntimeOccurrence,
) {
	if reachable.phases == nil {
		reachable.phases = map[string]bool{}
	}
	reachable.phases[occurrence.phase] = true
	reachable.actionRepeated = reachable.actionRepeated ||
		(occurrence.phase == "action" && occurrence.repeated)
}

func (builder *javaRuntimeReachabilityBuilder) mergeScopeReachability(
	scopeKey string,
	occurrence javaRuntimeOccurrence,
) {
	if strings.TrimSpace(scopeKey) == "" {
		return
	}
	reachable := builder.reachability.scopeReachability[scopeKey]
	mergeJavaRuntimeOccurrence(&reachable, occurrence)
	builder.reachability.scopeReachability[scopeKey] = reachable
}

func (builder *javaRuntimeReachabilityBuilder) initializeStaticClass(class javaClass, occurrence javaRuntimeOccurrence) {
	key := javaEvidenceOwnerKey(class)
	if builder.staticInitialized[key] {
		return
	}
	builder.staticInitialized[key] = true
	staticOccurrence := javaRuntimeOccurrence{phase: occurrence.phase}
	if superClass, ok := builder.resolvedSuperclass(class); ok {
		builder.initializeStaticClass(superClass, staticOccurrence)
	}
	if !strings.EqualFold(strings.TrimSpace(class.Kind), "interface") {
		builder.initializeDefaultMethodInterfaces(class, staticOccurrence)
	}
	fieldTypes := builder.fieldTypes(class)
	for fieldIndex, field := range class.Fields {
		if !javaFieldIsStaticInClass(class, field) {
			continue
		}
		builder.walkStatementsWithClone(
			javaExecutedFieldStatements([]javaStatement{field}),
			class,
			javaMethod{},
			fieldTypes,
			staticOccurrence,
			false,
			javaRuntimeFieldScopeKey(class, fieldIndex),
			"",
			false,
		)
		builder.walkEnumConstantConstructors(
			class,
			field,
			fieldTypes,
			staticOccurrence,
		)
	}
	for initializerIndex, initializer := range javaClassInitializerStatements(class) {
		if !strings.EqualFold(strings.TrimSpace(javaCodeOnly(initializer.Header)), "static") {
			continue
		}
		builder.walkStatements(
			initializer.Children,
			class,
			javaMethod{},
			fieldTypes,
			staticOccurrence,
			false,
			javaRuntimeInitializerScopeKey(class, initializerIndex),
			"",
		)
	}
}

func (builder *javaRuntimeReachabilityBuilder) initializeDefaultMethodInterfaces(
	class javaClass,
	occurrence javaRuntimeOccurrence,
) {
	visited := map[string]bool{}
	var inspect func(javaClass)
	inspect = func(current javaClass) {
		key := javaEvidenceOwnerKey(current)
		if visited[key] {
			return
		}
		visited[key] = true
		for _, parentName := range current.Interfaces {
			if parent, ok := builder.index.classForType(current, parentName); ok {
				inspect(parent)
			}
		}
		for _, method := range sortedJavaMethods(current) {
			if method.Default && !method.Abstract {
				builder.initializeStaticClass(current, occurrence)
				return
			}
		}
	}
	for _, interfaceName := range class.Interfaces {
		if implemented, ok := builder.index.classForType(class, interfaceName); ok {
			inspect(implemented)
		}
	}
}

func (builder *javaRuntimeReachabilityBuilder) walkEnumConstantConstructors(
	class javaClass,
	field javaStatement,
	fieldTypes map[string]string,
	occurrence javaRuntimeOccurrence,
) {
	if !strings.EqualFold(strings.TrimSpace(class.Kind), "enum") {
		return
	}
	for _, call := range javaEnumConstructorCalls(class, field) {
		name := call.name
		args := call.args
		if !call.argsOK {
			builder.addUnresolved(fmt.Sprintf(
				"Enum constant arguments for %s.%s at %s:%d could not be parsed exactly.",
				javaQualifiedClassName(class), name, field.File, field.StartLine,
			))
			continue
		}
		if len(class.Constructors) == 0 {
			if len(args) == 0 {
				builder.walkConstructor(class, nil, occurrence)
			} else {
				builder.addUnresolved(fmt.Sprintf(
					"Enum constant %s.%s supplies arguments, but no matching archived enum constructor was parsed.",
					javaQualifiedClassName(class), name,
				))
			}
			continue
		}
		constructors := javaHelperOverloadsForArguments(
			class.Constructors,
			args,
			fieldTypes,
		)
		if len(constructors) != 1 {
			builder.addUnresolved(fmt.Sprintf(
				"Enum constant %s.%s resolved to %d archived constructors instead of exactly one.",
				javaQualifiedClassName(class), name, len(constructors),
			))
		}
		for index := range constructors {
			constructor := constructors[index]
			builder.walkConstructor(class, &constructor, occurrence)
		}
	}
}

type javaEnumConstructorCall struct {
	name   string
	args   []string
	argsOK bool
}

func javaEnumConstructorCalls(
	class javaClass,
	field javaStatement,
) []javaEnumConstructorCall {
	if !strings.EqualFold(strings.TrimSpace(class.Kind), "enum") {
		return nil
	}
	constantNames := class.EnumConstantSet
	if !class.EnumConstantsReady {
		constantNames = make(map[string]bool)
		for _, name := range javaEnumConstantNames(class) {
			constantNames[name] = true
		}
	}
	calls := make([]javaEnumConstructorCall, 0)
	for _, part := range splitJavaFieldDeclarators(field.Text) {
		name := javaEnumConstantName(part)
		if !constantNames[name] {
			continue
		}
		code := javaCodeOnly(part)
		nameOffset := strings.Index(code, name)
		if nameOffset < 0 {
			continue
		}
		open := nameOffset + len(name)
		for open < len(code) && strings.ContainsRune(" \t\r\n", rune(code[open])) {
			open++
		}
		call := javaEnumConstructorCall{name: name, argsOK: true}
		if open < len(code) && code[open] == '(' {
			close := matchingJavaParen(code, open)
			if close < 0 || close > len(part) {
				call.argsOK = false
				calls = append(calls, call)
				continue
			}
			if sourceArgs := strings.TrimSpace(part[open+1 : close]); sourceArgs != "" {
				call.args = splitJavaArgs(sourceArgs)
			}
		}
		calls = append(calls, call)
	}
	return calls
}

func (builder *javaRuntimeReachabilityBuilder) walkInstanceInitializers(class javaClass, occurrence javaRuntimeOccurrence) {
	fieldTypes := builder.fieldTypes(class)
	for fieldIndex, field := range class.Fields {
		if javaFieldIsStaticInClass(class, field) {
			continue
		}
		builder.walkStatementsWithClone(
			javaExecutedFieldStatements([]javaStatement{field}),
			class,
			javaMethod{},
			fieldTypes,
			occurrence,
			false,
			javaRuntimeFieldScopeKey(class, fieldIndex),
			"",
			false,
		)
	}
	for initializerIndex, initializer := range javaClassInitializerStatements(class) {
		if strings.EqualFold(strings.TrimSpace(javaCodeOnly(initializer.Header)), "static") {
			continue
		}
		builder.walkStatements(
			initializer.Children,
			class,
			javaMethod{},
			fieldTypes,
			occurrence,
			false,
			javaRuntimeInitializerScopeKey(class, initializerIndex),
			"",
		)
	}
}

func javaFieldIsStatic(field javaStatement) bool {
	code := javaCodeOnly(field.Text)
	if assignment := strings.IndexByte(code, '='); assignment >= 0 {
		code = code[:assignment]
	}
	return javaStaticFieldModifierRe.MatchString(code)
}

func javaFieldIsStaticInClass(class javaClass, field javaStatement) bool {
	if javaFieldIsStatic(field) || strings.EqualFold(strings.TrimSpace(class.Kind), "interface") {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(class.Kind), "enum") {
		return false
	}
	name := javaEnumConstantName(field.Text)
	if name == "" {
		return false
	}
	if class.EnumConstantsReady {
		return class.EnumConstantSet[name]
	}
	for _, constantName := range javaEnumConstantNames(class) {
		if name == constantName {
			return true
		}
	}
	return false
}

func (builder *javaRuntimeReachabilityBuilder) staticFieldOwners(
	source string,
	owner javaClass,
	current javaMethod,
	variableTypes map[string]string,
) []javaClass {
	code := javaCodeOnly(source)
	owners := make([]javaClass, 0, 1)
	seen := map[string]bool{}
	add := func(class javaClass, fieldName string) {
		declaringClasses := javaStaticFieldDeclaringClassesWithResolver(
			builder.index,
			builder.constantFields,
			class,
			fieldName,
		)
		for _, declaringClass := range declaringClasses {
			key := javaEvidenceOwnerKey(declaringClass)
			if seen[key] {
				continue
			}
			seen[key] = true
			owners = append(owners, declaringClass)
		}
	}
	effectiveTypes := javaEffectiveEvidenceVariableTypes(builder.index, owner, variableTypes)
	for _, match := range javaMemberAccessRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		qualifier := strings.TrimSpace(match[1])
		fieldName := strings.TrimSpace(match[2])
		switch qualifier {
		case "this":
			add(owner, fieldName)
			continue
		case "super":
			if superClass, ok := builder.index.superclass(owner); ok {
				add(superClass, fieldName)
			}
			continue
		}
		if class, ok := builder.index.classForType(owner, qualifier); ok {
			add(class, fieldName)
			continue
		}
		// Java permits a static member to be selected through an expression
		// (including a null variable). Resolve the last receiver segment through
		// the evidence scope so that helper.VALUE and this.helper.VALUE both
		// trigger initialization of the field's declaring class.
		receiver := qualifier
		if dot := strings.LastIndexByte(receiver, '.'); dot >= 0 {
			receiver = receiver[dot+1:]
		}
		if typeName := effectiveTypes[receiver]; typeName != "" {
			if class, ok := builder.index.classForType(owner, typeName); ok {
				add(class, fieldName)
			}
		}
	}
	for _, match := range javaCastReceiverFieldRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		if class, ok := builder.index.classForType(owner, match[1]); ok {
			add(class, match[2])
		}
	}
	for _, match := range javaArrayReceiverFieldRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		typeName := variableTypes[match[1]]
		if typeName == "" {
			typeName = effectiveTypes[match[1]]
		}
		if elementType, ok := javaArrayElementType(typeName); ok {
			if class, resolved := builder.index.classForType(owner, elementType); resolved {
				add(class, match[2])
			}
		}
	}
	callReceiverMatches := javaCallReceiverFieldRe.FindAllStringSubmatch(code, -1)
	if len(callReceiverMatches) > 0 {
		resolvedHelpers := javaResolvedEvidenceHelperCalls(
			source,
			owner,
			current,
			builder.index,
			variableTypes,
		)
		for _, match := range callReceiverMatches {
			if len(match) != 3 {
				continue
			}
			for _, helper := range resolvedHelpers {
				if helper.method.Name != match[1] || strings.TrimSpace(helper.method.ReturnType) == "" {
					continue
				}
				if class, ok := builder.index.classForType(helper.class, helper.method.ReturnType); ok {
					add(class, match[2])
				}
			}
		}
	}

	// An exact or wildcard static import removes the declaring type from the
	// expression entirely. Resolve archived imports explicitly; otherwise a
	// first access such as `int value = VALUE` would omit the imported class's
	// static initializer from runtime evidence.
	staticFields := builder.constantFields.staticImportedFields(owner)
	for _, fieldName := range javaUnqualifiedIdentifiers(code) {
		if effectiveTypes[fieldName] != "" ||
			javaSourceDeclaresVariableNamed(code, fieldName) {
			continue
		}
		for _, class := range staticFields[fieldName] {
			add(class, fieldName)
		}
	}
	return owners
}

func javaArrayElementType(typeName string) (string, bool) {
	typeName = strings.TrimSpace(typeName)
	if !strings.HasSuffix(typeName, "[]") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimSuffix(typeName, "[]")), true
}

type javaConstantFieldDeclaration struct {
	typeName   string
	expression string
	static     bool
	final      bool
}

// javaConstantFieldResolver indexes archived fields once and memoizes recursive
// constant-expression proofs. A failed or cyclic proof is deliberately treated
// as a runtime initialization trigger.
type javaConstantFieldResolver struct {
	index         *javaEvidenceIndex
	declarations  map[string]map[string]javaConstantFieldDeclaration
	staticImports map[string]map[string][]javaClass
	staticNames   map[string][]string
	cache         map[string]bool
	settled       map[string]bool
	visiting      map[string]bool
}

func newJavaConstantFieldResolver(index *javaEvidenceIndex) *javaConstantFieldResolver {
	resolver := &javaConstantFieldResolver{
		index:         index,
		declarations:  map[string]map[string]javaConstantFieldDeclaration{},
		staticImports: map[string]map[string][]javaClass{},
		staticNames:   map[string][]string{},
		cache:         map[string]bool{},
		settled:       map[string]bool{},
		visiting:      map[string]bool{},
	}
	if index == nil {
		return resolver
	}
	for _, class := range index.program.Classes {
		ownerKey := javaEvidenceOwnerKey(class)
		for _, field := range class.Fields {
			for fieldName, declaration := range javaConstantFieldDeclarations(class, field) {
				if resolver.declarations[ownerKey] == nil {
					resolver.declarations[ownerKey] = map[string]javaConstantFieldDeclaration{}
				}
				resolver.declarations[ownerKey][fieldName] = declaration
			}
		}
		for _, fieldName := range javaEnumConstantNames(class) {
			if resolver.declarations[ownerKey] == nil {
				resolver.declarations[ownerKey] = map[string]javaConstantFieldDeclaration{}
			}
			resolver.declarations[ownerKey][fieldName] = javaConstantFieldDeclaration{
				typeName: javaQualifiedClassName(class),
				static:   true,
				final:    true,
			}
		}
	}
	return resolver
}

func (resolver *javaConstantFieldResolver) staticFieldNames(
	class javaClass,
) []string {
	if resolver == nil || resolver.index == nil {
		return nil
	}
	cacheKey := javaEvidenceOwnerKey(class)
	if cached, ok := resolver.staticNames[cacheKey]; ok {
		return cached
	}
	names := javaStaticFieldNamesWithResolver(resolver.index, resolver, class)
	resolver.staticNames[cacheKey] = names
	return names
}

func (resolver *javaConstantFieldResolver) staticImportedFields(
	owner javaClass,
) map[string][]javaClass {
	if resolver == nil || resolver.index == nil {
		return nil
	}
	cacheKey := javaEvidenceImportScopeKey(owner)
	if cached, ok := resolver.staticImports[cacheKey]; ok {
		return cached
	}
	fields := make(map[string][]javaClass)
	seen := make(map[string]map[string]bool)
	appendField := func(fieldName string, class javaClass) {
		if fieldName == "" {
			return
		}
		if seen[fieldName] == nil {
			seen[fieldName] = make(map[string]bool)
		}
		classKey := javaEvidenceOwnerKey(class)
		if seen[fieldName][classKey] {
			return
		}
		seen[fieldName][classKey] = true
		fields[fieldName] = append(fields[fieldName], class)
	}
	scope := resolver.index.importScope(owner)
	for fieldName, typeNames := range scope.staticExact {
		for _, typeName := range typeNames {
			class, ok := resolver.index.classForType(owner, typeName)
			if !ok {
				continue
			}
			declaringClass, declaration, found := resolver.declarationInHierarchy(
				class,
				fieldName,
			)
			if found && declaration.static {
				appendField(fieldName, declaringClass)
			}
		}
	}
	for _, typeName := range scope.staticWildcard {
		class, ok := resolver.index.classForType(owner, typeName)
		if !ok {
			continue
		}
		for _, fieldName := range resolver.staticFieldNames(class) {
			declaringClass, declaration, found := resolver.declarationInHierarchy(
				class,
				fieldName,
			)
			if found && declaration.static {
				appendField(fieldName, declaringClass)
			}
		}
	}
	resolver.staticImports[cacheKey] = fields
	return fields
}

func javaEnumConstantNames(class javaClass) []string {
	if !strings.EqualFold(strings.TrimSpace(class.Kind), "enum") {
		return nil
	}
	if class.EnumConstantsReady {
		return append([]string(nil), class.EnumConstantNames...)
	}
	return javaEnumConstantNamesFromSource(class.Source)
}

func javaEnumConstantNamesFromSource(source string) []string {
	code := javaCodeOnly(source)
	parts := make([]string, 0, 4)
	start := 0
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	terminated := false
	for offset := 0; offset < len(code); offset++ {
		switch code[offset] {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ',':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				parts = append(parts, source[start:offset])
				start = offset + 1
			}
		case ';':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				parts = append(parts, source[start:offset])
				terminated = true
				offset = len(code)
			}
		}
	}
	if !terminated && start < len(source) {
		parts = append(parts, source[start:])
	}
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := javaEnumConstantName(part); name != "" {
			names = append(names, name)
		}
	}
	return uniqueSortedStrings(names)
}

func javaEnumConstantName(source string) string {
	code := strings.TrimSpace(javaCodeOnly(source))
	code = stripLeadingJavaParameterDecorators(code)
	if code == "" || !isJavaIdentifierByte(code[0]) ||
		(code[0] >= '0' && code[0] <= '9') {
		return ""
	}
	end := 1
	for end < len(code) && isJavaIdentifierByte(code[end]) {
		end++
	}
	return code[:end]
}

func javaStaticFieldDeclaringClassWithResolver(
	index *javaEvidenceIndex,
	resolver *javaConstantFieldResolver,
	class javaClass,
	fieldName string,
) (javaClass, bool) {
	classes := javaStaticFieldDeclaringClassesWithResolver(index, resolver, class, fieldName)
	if len(classes) != 1 {
		return javaClass{}, false
	}
	return classes[0], true
}

func javaStaticFieldDeclaringClassesWithResolver(
	index *javaEvidenceIndex,
	resolver *javaConstantFieldResolver,
	class javaClass,
	fieldName string,
) []javaClass {
	if resolver == nil {
		resolver = newJavaConstantFieldResolver(index)
	}
	visited := map[string]bool{}
	classChain := make([]javaClass, 0, 4)
	for {
		key := javaEvidenceOwnerKey(class)
		if visited[key] {
			return nil
		}
		visited[key] = true
		classChain = append(classChain, class)
		if declaration, exists := resolver.directDeclaration(class, fieldName); exists {
			if declaration.static && !resolver.isCompileTimeConstant(class, fieldName) {
				return []javaClass{class}
			}
			// A declaration on the current type hides any inherited field.
			return nil
		}
		superClass, ok := index.superclass(class)
		if !ok {
			break
		}
		class = superClass
	}

	declaring := make([]javaClass, 0, 1)
	seenDeclaring := map[string]bool{}
	visitedInterfaces := map[string]bool{}
	var inspectInterface func(javaClass)
	inspectInterface = func(current javaClass) {
		key := javaEvidenceOwnerKey(current)
		if visitedInterfaces[key] {
			return
		}
		visitedInterfaces[key] = true
		if declaration, exists := resolver.directDeclaration(current, fieldName); exists {
			if declaration.static && !resolver.isCompileTimeConstant(current, fieldName) &&
				!seenDeclaring[key] {
				seenDeclaring[key] = true
				declaring = append(declaring, current)
			}
			// An interface declaration hides declarations inherited through its
			// own parent interfaces, including an inlined constant variable.
			return
		}
		for _, interfaceName := range current.Interfaces {
			if parent, ok := index.classForType(current, interfaceName); ok {
				inspectInterface(parent)
			}
		}
	}
	for _, owner := range classChain {
		for _, interfaceName := range owner.Interfaces {
			if implemented, ok := index.classForType(owner, interfaceName); ok {
				inspectInterface(implemented)
			}
		}
	}
	return declaring
}

func javaConstantFieldDeclarations(
	owner javaClass,
	field javaStatement,
) map[string]javaConstantFieldDeclaration {
	declarations := map[string]javaConstantFieldDeclaration{}
	code := javaCodeOnly(field.Text)
	match := javaVariableDeclRe.FindStringSubmatchIndex(code)
	if len(match) != 6 || match[0] < 0 || match[1] > len(code) ||
		match[2] < 0 || match[3] > len(code) || match[4] < 0 || match[4] > len(field.Text) {
		return declarations
	}
	declarationPrefix := code[match[0]:match[1]]
	typeName := strings.TrimSpace(code[match[2]:match[3]])
	static := javaFieldIsStaticInClass(owner, field)
	final := javaFinalFieldModifierRe.MatchString(declarationPrefix) ||
		strings.EqualFold(strings.TrimSpace(owner.Kind), "interface")
	for _, declarator := range splitJavaFieldDeclarators(field.Text[match[4]:]) {
		name, expression, arrayDeclarator, ok := javaFieldDeclarator(declarator)
		if !ok {
			continue
		}
		declaratorType := typeName
		if arrayDeclarator {
			declaratorType += "[]"
		}
		declarations[name] = javaConstantFieldDeclaration{
			typeName:   declaratorType,
			expression: expression,
			static:     static,
			final:      final,
		}
	}
	return declarations
}

func splitJavaFieldDeclarators(source string) []string {
	code := javaCodeOnly(source)
	parts := make([]string, 0, 2)
	start := 0
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	for offset := 0; offset < len(code); offset++ {
		switch code[offset] {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ',':
			// Generic constructor/method expressions are never Java constant
			// expressions. Splitting their type-argument comma conservatively
			// may truncate that one nonconstant initializer, but it guarantees
			// later field declarators are still indexed. Treating every '<...>'
			// as a generic span can instead swallow a real declarator comma
			// across ordinary comparison operators.
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				parts = append(parts, strings.TrimSpace(source[start:offset]))
				start = offset + 1
			}
		}
	}
	if part := strings.TrimSpace(source[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func javaFieldDeclarator(source string) (name, expression string, arrayDeclarator, ok bool) {
	source = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(source), ";"))
	code := javaCodeOnly(source)
	offset := 0
	for offset < len(code) && (code[offset] == ' ' || code[offset] == '\t' ||
		code[offset] == '\r' || code[offset] == '\n') {
		offset++
	}
	start := offset
	if offset >= len(code) || !isJavaIdentifierByte(code[offset]) ||
		(code[offset] >= '0' && code[offset] <= '9') {
		return "", "", false, false
	}
	for offset < len(code) && isJavaIdentifierByte(code[offset]) {
		offset++
	}
	name = strings.TrimSpace(code[start:offset])
	for offset < len(code) {
		switch {
		case code[offset] == ' ' || code[offset] == '\t' ||
			code[offset] == '\r' || code[offset] == '\n':
			offset++
		case code[offset] == '[':
			close := offset + 1
			for close < len(code) && (code[close] == ' ' || code[close] == '\t') {
				close++
			}
			if close >= len(code) || code[close] != ']' {
				return "", "", false, false
			}
			arrayDeclarator = true
			offset = close + 1
		case code[offset] == '=':
			expression = strings.TrimSpace(source[offset+1:])
			return name, expression, arrayDeclarator, true
		default:
			return "", "", false, false
		}
	}
	return name, "", arrayDeclarator, true
}

func (resolver *javaConstantFieldResolver) directDeclaration(
	class javaClass,
	fieldName string,
) (javaConstantFieldDeclaration, bool) {
	if resolver == nil {
		return javaConstantFieldDeclaration{}, false
	}
	declaration, ok := resolver.declarations[javaEvidenceOwnerKey(class)][fieldName]
	return declaration, ok
}

func (resolver *javaConstantFieldResolver) declarationInHierarchy(
	class javaClass,
	fieldName string,
) (javaClass, javaConstantFieldDeclaration, bool) {
	visited := map[string]bool{}
	classChain := make([]javaClass, 0, 4)
	for {
		key := javaEvidenceOwnerKey(class)
		if visited[key] {
			return javaClass{}, javaConstantFieldDeclaration{}, false
		}
		visited[key] = true
		classChain = append(classChain, class)
		if declaration, ok := resolver.directDeclaration(class, fieldName); ok {
			return class, declaration, true
		}
		superClass, ok := resolver.index.superclass(class)
		if !ok {
			break
		}
		class = superClass
	}

	type match struct {
		class       javaClass
		declaration javaConstantFieldDeclaration
	}
	matches := make([]match, 0, 1)
	seenMatches := map[string]bool{}
	visitedInterfaces := map[string]bool{}
	var inspectInterface func(javaClass)
	inspectInterface = func(current javaClass) {
		key := javaEvidenceOwnerKey(current)
		if visitedInterfaces[key] {
			return
		}
		visitedInterfaces[key] = true
		if declaration, ok := resolver.directDeclaration(current, fieldName); ok {
			if !seenMatches[key] {
				seenMatches[key] = true
				matches = append(matches, match{class: current, declaration: declaration})
			}
			return
		}
		for _, parentName := range current.Interfaces {
			if parent, ok := resolver.index.classForType(current, parentName); ok {
				inspectInterface(parent)
			}
		}
	}
	for _, owner := range classChain {
		for _, interfaceName := range owner.Interfaces {
			if implemented, ok := resolver.index.classForType(owner, interfaceName); ok {
				inspectInterface(implemented)
			}
		}
	}
	if len(matches) != 1 {
		return javaClass{}, javaConstantFieldDeclaration{}, false
	}
	return matches[0].class, matches[0].declaration, true
}

func (resolver *javaConstantFieldResolver) isCompileTimeConstant(class javaClass, fieldName string) bool {
	key := javaEvidenceOwnerKey(class) + "\x00" + fieldName
	if resolver.settled[key] {
		return resolver.cache[key]
	}
	if resolver.visiting[key] {
		return false
	}
	resolver.visiting[key] = true
	defer delete(resolver.visiting, key)

	declaration, exists := resolver.directDeclaration(class, fieldName)
	constant := exists && declaration.static && declaration.final &&
		javaConstantVariableType(declaration.typeName) &&
		declaration.expression != "" &&
		javaConstantExpressionIsSafe(declaration.expression, class, resolver)
	resolver.cache[key] = constant
	resolver.settled[key] = true
	return constant
}

func javaConstantVariableType(typeName string) bool {
	if strings.Contains(strings.ReplaceAll(typeName, " ", ""), "[]") {
		return false
	}
	switch javaComparableType(typeName) {
	case "byte", "short", "int", "long", "float", "double", "char", "boolean", "string":
		return true
	default:
		return false
	}
}

func (resolver *javaConstantFieldResolver) resolvesConstantReference(owner javaClass, reference string) bool {
	reference = strings.ReplaceAll(strings.TrimSpace(reference), " ", "")
	if reference == "" {
		return false
	}
	if dot := strings.LastIndexByte(reference, '.'); dot >= 0 {
		typeName := reference[:dot]
		fieldName := reference[dot+1:]
		if typeName == "" || fieldName == "" || typeName == "this" {
			return false
		}
		var class javaClass
		var ok bool
		if typeName == "super" {
			class, ok = resolver.index.superclass(owner)
		} else {
			class, ok = resolver.index.classForType(owner, typeName)
		}
		if !ok {
			return false
		}
		declaringClass, _, found := resolver.declarationInHierarchy(class, fieldName)
		return found && resolver.isCompileTimeConstant(declaringClass, fieldName)
	}

	if declaringClass, _, found := resolver.declarationInHierarchy(owner, reference); found {
		return resolver.isCompileTimeConstant(declaringClass, reference)
	}

	matches := resolver.staticImportedFields(owner)[reference]
	if len(matches) != 1 {
		return false
	}
	return resolver.isCompileTimeConstant(matches[0], reference)
}

func javaConstantExpressionIsSafe(
	expression string,
	owner javaClass,
	resolver *javaConstantFieldResolver,
) bool {
	operandSeen := false
	expectOperand := true
	parenDepth := 0
	pendingQuestions := 0
	for offset := 0; offset < len(expression); {
		next, ok := javaSkipConstantTrivia(expression, offset)
		if !ok {
			return false
		}
		offset = next
		if offset >= len(expression) {
			break
		}

		switch expression[offset] {
		case '"', '\'':
			if !expectOperand {
				return false
			}
			textBlock := expression[offset] == '"' && strings.HasPrefix(expression[offset:], `"""`)
			end, literalOK := javaConstantLiteralEnd(expression, offset, expression[offset])
			if !literalOK {
				return false
			}
			if expression[offset] == '"' && !textBlock {
				if _, parsed := parseJavaStringLiteral(expression[offset:end]); !parsed {
					return false
				}
			}
			operandSeen = true
			expectOperand = false
			offset = end
			continue
		case '(':
			if !expectOperand {
				// A parenthesis after an operand is a method invocation, not a
				// Java constant expression. This remains true when a field and
				// method intentionally share the same identifier.
				return false
			}
			if castEnd, cast := javaConstantCastEnd(expression, offset); cast {
				offset = castEnd
				continue
			}
			parenDepth++
			offset++
			continue
		case ')':
			if expectOperand {
				return false
			}
			parenDepth--
			if parenDepth < 0 {
				return false
			}
			expectOperand = false
			offset++
			continue
		case '?':
			if expectOperand {
				return false
			}
			pendingQuestions++
			expectOperand = true
			offset++
			continue
		case ':':
			if expectOperand || pendingQuestions == 0 {
				return false
			}
			pendingQuestions--
			expectOperand = true
			offset++
			continue
		}

		if (expression[offset] >= '0' && expression[offset] <= '9') ||
			(expression[offset] == '.' && offset+1 < len(expression) &&
				expression[offset+1] >= '0' && expression[offset+1] <= '9') {
			if !expectOperand {
				return false
			}
			end := javaConstantNumberEnd(expression, offset)
			if end == offset || !javaConstantNumberRe.MatchString(expression[offset:end]) {
				return false
			}
			operandSeen = true
			expectOperand = false
			offset = end
			continue
		}

		if isJavaIdentifierByte(expression[offset]) &&
			!(expression[offset] >= '0' && expression[offset] <= '9') {
			if !expectOperand {
				return false
			}
			end := javaConstantReferenceEnd(expression, offset)
			reference := javaNormalizeConstantReference(expression[offset:end])
			switch reference {
			case "true", "false":
				operandSeen = true
			default:
				if resolver == nil || !resolver.resolvesConstantReference(owner, reference) {
					return false
				}
				operandSeen = true
			}
			expectOperand = false
			offset = end
			continue
		}

		operatorLength := javaConstantOperatorLength(expression[offset:])
		if operatorLength == 0 {
			return false
		}
		operator := expression[offset : offset+operatorLength]
		if expectOperand {
			switch operator {
			case "+", "-", "!", "~":
			default:
				return false
			}
		} else {
			switch operator {
			case "!", "~":
				return false
			default:
				expectOperand = true
			}
		}
		offset += operatorLength
	}
	return operandSeen && !expectOperand && parenDepth == 0 && pendingQuestions == 0
}

func javaSkipConstantTrivia(source string, offset int) (int, bool) {
	for offset < len(source) {
		switch {
		case source[offset] == ' ' || source[offset] == '\t' ||
			source[offset] == '\r' || source[offset] == '\n' || source[offset] == '\f':
			offset++
		case strings.HasPrefix(source[offset:], "//"):
			if newline := strings.IndexByte(source[offset+2:], '\n'); newline >= 0 {
				offset += newline + 3
			} else {
				return len(source), true
			}
		case strings.HasPrefix(source[offset:], "/*"):
			end := strings.Index(source[offset+2:], "*/")
			if end < 0 {
				return offset, false
			}
			offset += end + 4
		default:
			return offset, true
		}
	}
	return offset, true
}

func javaConstantLiteralEnd(source string, start int, quote byte) (int, bool) {
	// Java text blocks are String literals and therefore participate in
	// constant expressions. Treat a syntactically closed block as one operand.
	if quote == '"' && strings.HasPrefix(source[start:], `"""`) {
		if relative := strings.Index(source[start+3:], `"""`); relative >= 0 {
			return start + 3 + relative + 3, true
		}
		return start, false
	}
	escaped := false
	for offset := start + 1; offset < len(source); offset++ {
		switch {
		case escaped:
			escaped = false
		case source[offset] == '\\':
			escaped = true
		case source[offset] == quote:
			// The empty String literal is a valid constant expression; an empty
			// character literal is not valid Java.
			return offset + 1, quote == '"' || offset > start+1
		case source[offset] == '\n' || source[offset] == '\r':
			return start, false
		}
	}
	return start, false
}

func javaConstantNumberEnd(source string, start int) int {
	offset := start
	for offset < len(source) {
		ch := source[offset]
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '.' {
			offset++
			continue
		}
		if (ch == '+' || ch == '-') && offset > start {
			previous := source[offset-1]
			if previous == 'e' || previous == 'E' || previous == 'p' || previous == 'P' {
				offset++
				continue
			}
		}
		break
	}
	return offset
}

func javaConstantReferenceEnd(source string, start int) int {
	offset := start
	for offset < len(source) && isJavaIdentifierByte(source[offset]) {
		offset++
	}
	for {
		dot := offset
		for dot < len(source) && (source[dot] == ' ' || source[dot] == '\t' ||
			source[dot] == '\r' || source[dot] == '\n') {
			dot++
		}
		if dot >= len(source) || source[dot] != '.' {
			return offset
		}
		next := dot + 1
		for next < len(source) && (source[next] == ' ' || source[next] == '\t' ||
			source[next] == '\r' || source[next] == '\n') {
			next++
		}
		if next >= len(source) || !isJavaIdentifierByte(source[next]) ||
			(source[next] >= '0' && source[next] <= '9') {
			return offset
		}
		offset = next + 1
		for offset < len(source) && isJavaIdentifierByte(source[offset]) {
			offset++
		}
	}
}

func javaNormalizeConstantReference(reference string) string {
	parts := strings.Split(reference, ".")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return strings.Join(parts, ".")
}

func javaConstantCastEnd(source string, start int) (int, bool) {
	if start < 0 || start >= len(source) || source[start] != '(' {
		return start, false
	}
	close := strings.IndexByte(source[start+1:], ')')
	if close < 0 {
		return start, false
	}
	close += start + 1
	typeName := strings.Join(strings.Fields(javaCodeOnly(source[start+1:close])), "")
	switch typeName {
	case "byte", "short", "int", "long", "float", "double", "char", "boolean",
		"String", "java.lang.String":
		return close + 1, true
	default:
		return start, false
	}
}

func javaConstantOperatorLength(source string) int {
	if strings.HasPrefix(source, "->") || strings.HasPrefix(source, "::") {
		return 0
	}
	for _, operator := range []string{
		">>>", "<<", ">>", "<=", ">=", "==", "!=", "&&", "||",
		"+", "-", "*", "/", "%", "<", ">", "&", "^", "|", "!", "~",
	} {
		if strings.HasPrefix(source, operator) {
			if len(source) > len(operator) && source[len(operator)] == '=' {
				return 0
			}
			if (operator == "+" || operator == "-") && len(source) > 1 &&
				source[1] == operator[0] {
				return 0
			}
			return len(operator)
		}
	}
	return 0
}

func javaStaticFieldDeclaringClass(
	index *javaEvidenceIndex,
	class javaClass,
	fieldName string,
) (javaClass, bool) {
	return javaStaticFieldDeclaringClassWithResolver(
		index,
		newJavaConstantFieldResolver(index),
		class,
		fieldName,
	)
}

func javaStaticFieldNames(index *javaEvidenceIndex, class javaClass) []string {
	return javaStaticFieldNamesWithResolver(index, newJavaConstantFieldResolver(index), class)
}

func javaStaticFieldNamesWithResolver(
	index *javaEvidenceIndex,
	resolver *javaConstantFieldResolver,
	class javaClass,
) []string {
	names := make([]string, 0)
	visited := map[string]bool{}
	var addOwner func(javaClass)
	addOwner = func(owner javaClass) {
		key := javaEvidenceOwnerKey(owner)
		if visited[key] {
			return
		}
		visited[key] = true
		for name, declaration := range resolver.declarations[key] {
			if declaration.static {
				names = append(names, name)
			}
		}
		for _, interfaceName := range owner.Interfaces {
			if inherited, ok := index.classForType(owner, interfaceName); ok {
				addOwner(inherited)
			}
		}
	}
	for _, owner := range index.classHierarchy(class) {
		addOwner(owner)
	}
	return uniqueSortedStrings(names)
}

func javaUnqualifiedIdentifierUsed(code, name string) bool {
	for offset := 0; offset < len(code); {
		relative := strings.Index(code[offset:], name)
		if relative < 0 {
			return false
		}
		start := offset + relative
		end := start + len(name)
		beforeIdentifier := start > 0 && isJavaIdentifierByte(code[start-1])
		afterIdentifier := end < len(code) && isJavaIdentifierByte(code[end])
		previous := start - 1
		for previous >= 0 && (code[previous] == ' ' || code[previous] == '\t' ||
			code[previous] == '\r' || code[previous] == '\n') {
			previous--
		}
		if !beforeIdentifier && !afterIdentifier && (previous < 0 || code[previous] != '.') {
			return true
		}
		offset = end
	}
	return false
}

func javaUnqualifiedIdentifiers(code string) []string {
	identifiers := make([]string, 0, 4)
	seen := make(map[string]bool)
	for offset := 0; offset < len(code); {
		if !isJavaIdentifierByte(code[offset]) ||
			(code[offset] >= '0' && code[offset] <= '9') {
			offset++
			continue
		}
		start := offset
		for offset < len(code) && isJavaIdentifierByte(code[offset]) {
			offset++
		}
		previous := start - 1
		for previous >= 0 && strings.ContainsRune(" \t\r\n", rune(code[previous])) {
			previous--
		}
		if previous >= 0 && code[previous] == '.' {
			continue
		}
		name := code[start:offset]
		if !seen[name] {
			seen[name] = true
			identifiers = append(identifiers, name)
		}
	}
	return identifiers
}

func javaSourceDeclaresVariableNamed(code, name string) bool {
	for _, match := range javaVariableDeclRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 || !strings.EqualFold(strings.TrimSpace(match[2]), name) {
			continue
		}
		// javaVariableDeclRe intentionally accepts broad source-like evidence,
		// so a statement such as `return VALUE;` can look like a declaration
		// with synthetic type "return". Exclude control-flow keywords before
		// using it as a static-import shadowing check.
		switch strings.ToLower(javaSimpleTypeName(match[1])) {
		case "return", "throw", "break", "continue", "case", "default", "new", "yield":
			continue
		default:
			return true
		}
	}
	return false
}

func (builder *javaRuntimeReachabilityBuilder) walkConstructor(
	class javaClass,
	constructor *javaMethod,
	occurrence javaRuntimeOccurrence,
) {
	builder.initializeStaticClass(class, occurrence)
	constructorKey := javaEvidenceOwnerKey(class) + "\x00<implicit>"
	if constructor != nil {
		constructorKey = javaHelperVisitKey(class, *constructor)
	}
	constructorKey += "\x00" + occurrence.phase + "\x00" + strconv.FormatBool(occurrence.repeated)
	if builder.constructorComplete[constructorKey] {
		return
	}
	if builder.constructorStack[constructorKey] {
		builder.addUnresolved(fmt.Sprintf(
			"Java construction recursion at %s prevents exact governed-runner reachability verification.",
			javaQualifiedClassName(class),
		))
		return
	}
	builder.constructorStack[constructorKey] = true
	defer func() {
		delete(builder.constructorStack, constructorKey)
		builder.constructorComplete[constructorKey] = true
	}()

	if constructor == nil {
		builder.walkDefaultSuperclassConstructor(class, occurrence)
		builder.walkInstanceInitializers(class, occurrence)
		return
	}

	thisTargets, superTargets := builder.constructorDelegationTargets(class, *constructor)
	switch {
	case len(thisTargets) > 0:
		for index := range thisTargets {
			builder.walkConstructor(class, &thisTargets[index], occurrence)
		}
	case len(superTargets) > 0:
		for index := range superTargets {
			builder.walkConstructor(superTargets[index].class, &superTargets[index].method, occurrence)
		}
	default:
		builder.walkDefaultSuperclassConstructor(class, occurrence)
	}
	if len(thisTargets) == 0 {
		builder.walkInstanceInitializers(class, occurrence)
	}
	builder.walkStatements(
		constructor.Statements,
		class,
		*constructor,
		javaDeclaredVariableTypes(class, *constructor),
		occurrence,
		true,
		javaRuntimeMethodScopeKey(class, *constructor),
		"",
	)
}

type javaRuntimeConstructorTarget struct {
	class  javaClass
	method javaMethod
}

func (builder *javaRuntimeReachabilityBuilder) constructorDelegationTargets(
	class javaClass,
	constructor javaMethod,
) ([]javaMethod, []javaRuntimeConstructorTarget) {
	types := javaDeclaredVariableTypes(class, constructor)
	resolved := javaResolvedEvidenceHelperCalls(constructor.Body, class, constructor, builder.index, types)
	var thisTargets []javaMethod
	var superTargets []javaRuntimeConstructorTarget
	for _, helper := range resolved {
		if helper.method.Name != helper.class.Name {
			continue
		}
		method := helper.method
		if javaEvidenceOwnerKey(helper.class) == javaEvidenceOwnerKey(class) {
			thisTargets = append(thisTargets, method)
			continue
		}
		superTargets = append(superTargets, javaRuntimeConstructorTarget{class: helper.class, method: method})
	}
	code := javaCodeOnly(constructor.Body)
	if javaThisConstructorCallRe.MatchString(code) && len(thisTargets) != 1 {
		builder.addUnresolved(fmt.Sprintf(
			"Constructor delegation in %s at %s:%d resolved to %d archived candidates instead of exactly one; governed construction reachability must be repaired before promotion.",
			javaQualifiedClassName(class), constructor.File, constructor.StartLine, len(thisTargets),
		))
	}
	if javaSuperConstructorCallRe.MatchString(code) && len(superTargets) != 1 {
		builder.addUnresolved(fmt.Sprintf(
			"Superclass constructor delegation in %s at %s:%d resolved to %d archived candidates instead of exactly one; governed construction reachability must be repaired before promotion.",
			javaQualifiedClassName(class), constructor.File, constructor.StartLine, len(superTargets),
		))
	}
	return thisTargets, superTargets
}

func (builder *javaRuntimeReachabilityBuilder) walkDefaultSuperclassConstructor(
	class javaClass,
	occurrence javaRuntimeOccurrence,
) {
	superClass, ok := builder.resolvedSuperclass(class)
	if !ok {
		return
	}
	if len(superClass.Constructors) == 0 {
		builder.walkConstructor(superClass, nil, occurrence)
		return
	}
	for _, constructor := range superClass.Constructors {
		if constructor.ParameterCount == 0 {
			copy := constructor
			builder.walkConstructor(superClass, &copy, occurrence)
			return
		}
	}
	builder.addUnresolved(fmt.Sprintf(
		"Archived superclass %s has no resolvable zero-argument constructor for %s.",
		javaQualifiedClassName(superClass), javaQualifiedClassName(class),
	))
}

func (builder *javaRuntimeReachabilityBuilder) resolvedSuperclass(class javaClass) (javaClass, bool) {
	superName := strings.TrimSpace(class.SuperClass)
	if superName == "" || strings.EqualFold(superName, "Object") ||
		strings.EqualFold(superName, "java.lang.Object") {
		return javaClass{}, false
	}
	superClass, ok := builder.index.classForType(class, superName)
	if !ok {
		builder.addUnresolved(fmt.Sprintf(
			"Java runner class %s extends unresolved superclass %s; exact construction reachability is unavailable.",
			javaQualifiedClassName(class), superName,
		))
	}
	return superClass, ok
}

func (builder *javaRuntimeReachabilityBuilder) walkMethod(
	class javaClass,
	method javaMethod,
	occurrence javaRuntimeOccurrence,
) {
	builder.initializeStaticClass(class, occurrence)
	key := javaHelperVisitKey(class, method) + "\x00" + occurrence.phase +
		"\x00" + strconv.FormatBool(occurrence.repeated)
	if builder.methodComplete[key] {
		return
	}
	if builder.methodStack[key] {
		return
	}
	builder.methodStack[key] = true
	defer delete(builder.methodStack, key)
	builder.walkStatements(
		method.Statements,
		class,
		method,
		javaDeclaredVariableTypes(class, method),
		occurrence,
		false,
		javaRuntimeMethodScopeKey(class, method),
		"",
	)
	builder.methodComplete[key] = true
}

func (builder *javaRuntimeReachabilityBuilder) walkStatements(
	statements []javaStatement,
	owner javaClass,
	current javaMethod,
	inheritedTypes map[string]string,
	occurrence javaRuntimeOccurrence,
	skipConstructorDelegation bool,
	scopeKey string,
	pathPrefix string,
) {
	builder.walkStatementsWithClone(
		statements,
		owner,
		current,
		inheritedTypes,
		occurrence,
		skipConstructorDelegation,
		scopeKey,
		pathPrefix,
		true,
	)
}

func (builder *javaRuntimeReachabilityBuilder) walkStatementsWithClone(
	statements []javaStatement,
	owner javaClass,
	current javaMethod,
	inheritedTypes map[string]string,
	occurrence javaRuntimeOccurrence,
	skipConstructorDelegation bool,
	scopeKey string,
	pathPrefix string,
	cloneInherited bool,
) {
	statements = javaImmediatelyExecutedStatements(statements)
	variableTypes := inheritedTypes
	if cloneInherited {
		variableTypes = cloneJavaVariableTypes(inheritedTypes)
	}
	for statementIndex, statement := range statements {
		statementPath := javaRuntimeChildPath(pathPrefix, statementIndex)
		callSource := statement.Text
		callTypes := variableTypes
		if statement.Kind == "block" {
			callSource = statement.Header
			childTypes := cloneJavaVariableTypes(variableTypes)
			collectJavaDeclaredVariableTypes(childTypes, callSource)
			callTypes = childTypes
		} else {
			builder.addStatement(
				statement,
				occurrence,
				scopeKey,
				statementPath,
				owner,
				current,
				callTypes,
			)
		}

		for _, staticOwner := range builder.staticFieldOwners(callSource, owner, current, callTypes) {
			builder.initializeStaticClass(staticOwner, occurrence)
		}

		for _, construction := range builder.resolveConstructions(statement, callSource, owner, callTypes) {
			if construction.constructor == nil {
				builder.walkConstructor(construction.class, nil, occurrence)
			} else {
				constructor := *construction.constructor
				builder.walkConstructor(construction.class, &constructor, occurrence)
			}
		}
		for _, methodName := range javaLRAmbiguousStaticCalls(
			callSource,
			owner,
			current,
			builder.index,
			callTypes,
		) {
			builder.addUnresolved(fmt.Sprintf(
				"Unqualified Java call %s at %s:%d has an ambiguous lrapi.lr static-import binding; qualify the intended owner before promotion so transaction and runner semantics remain exact.",
				methodName,
				statement.File,
				statement.StartLine,
			))
		}
		for _, helper := range javaResolvedEvidenceHelperCalls(callSource, owner, current, builder.index, callTypes) {
			if helper.method.Name == helper.class.Name {
				if skipConstructorDelegation {
					continue
				}
				constructor := helper.method
				builder.walkConstructor(helper.class, &constructor, occurrence)
				continue
			}
			builder.walkMethod(helper.class, helper.method, occurrence)
		}
		if builder.index.staticImportLimitHit {
			builder.addUnresolved(fmt.Sprintf(
				"Static-import helper analysis exceeded the deterministic owner-lookup limit of %d while evaluating %s:%d; simplify wildcard static imports before promotion.",
				maxJavaStaticImportOwnerLookups,
				statement.File,
				statement.StartLine,
			))
		}
		for _, receiver := range builder.unresolvedArchivedReceiverCalls(
			callSource,
			owner,
			callTypes,
		) {
			builder.addUnresolved(fmt.Sprintf(
				"Invocation through archived Java receiver %s at %s:%d could not be bound to a parsed helper type; governed runtime reachability must be repaired before promotion.",
				receiver,
				statement.File,
				statement.StartLine,
			))
		}
		for _, valueName := range javaUnresolvedInvokedFunctionalValues(
			current,
			callSource,
			owner,
			builder.index,
			callTypes,
		) {
			builder.addUnresolved(fmt.Sprintf(
				"Invocation of stored Java functional value %s at %s:%d could not be resolved to its archived lambda, method reference, or anonymous implementation; governed runtime reachability must be repaired before promotion.",
				valueName,
				statement.File,
				statement.StartLine,
			))
		}
		if statement.Kind == "block" {
			builder.walkStatements(
				statement.Children,
				owner,
				current,
				callTypes,
				occurrence,
				skipConstructorDelegation,
				scopeKey,
				statementPath,
			)
		} else if cloneInherited {
			collectJavaDeclaredVariableTypes(variableTypes, statement.Text)
		}
	}
}

func (builder *javaRuntimeReachabilityBuilder) unresolvedArchivedReceiverCalls(
	source string,
	owner javaClass,
	variableTypes map[string]string,
) []string {
	if builder == nil || builder.index == nil {
		return nil
	}
	code := javaCodeOnly(source)
	unresolved := make(map[string]bool)
	checkType := func(label, typeName, methodName string) {
		typeName = strings.TrimSpace(typeName)
		if typeName == "" ||
			(!builder.simpleTypes[javaComparableType(typeName)] &&
				!builder.index.archivedNestedType(owner, typeName)) {
			return
		}
		class, ok := builder.index.classForType(owner, typeName)
		if !ok {
			unresolved[label] = true
			return
		}
		// An analysis-limit placeholder intentionally retains the archived type
		// name but has no trustworthy member model. Do not let that placeholder
		// turn an invocation into apparently resolved reachability.
		if len(class.Unparsed) > 0 && len(javaMethodsNamed(class, methodName)) == 0 {
			unresolved[label] = true
		}
	}
	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		qualifier := strings.TrimSpace(code[match[2]:match[3]])
		methodName := strings.TrimSpace(code[match[4]:match[5]])
		if strings.Contains(qualifier, ".") {
			if class, archived, exact, limitExceeded := builder.index.resolveMemberReceiver(
				owner,
				qualifier,
				variableTypes,
			); archived {
				if limitExceeded || !exact || len(javaEvidenceMethodsInHierarchy(
					builder.index,
					class,
					owner,
					methodName,
					javaSameEvidenceClass(class, owner),
				)) == 0 {
					unresolved[qualifier] = true
				}
				continue
			}
		}
		valueName := qualifier
		if dot := strings.LastIndexByte(valueName, '.'); dot >= 0 {
			valueName = valueName[dot+1:]
		}
		if typeName := variableTypes[valueName]; typeName != "" {
			checkType(valueName, typeName, methodName)
			continue
		}
		checkType(qualifier, qualifier, methodName)
	}
	for _, match := range javaCastReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) >= 6 {
			checkType(
				strings.TrimSpace(code[match[2]:match[3]]),
				code[match[2]:match[3]],
				code[match[4]:match[5]],
			)
		}
	}
	for _, match := range javaArrayReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) >= 6 {
			name := strings.TrimSpace(code[match[2]:match[3]])
			checkType(name, variableTypes[name], code[match[4]:match[5]])
		}
	}
	for _, receiver := range javaUnresolvedReturnedReceiverCalls(
		source,
		owner,
		builder.index,
		variableTypes,
	) {
		unresolved[receiver] = true
	}
	values := make([]string, 0, len(unresolved))
	for receiver := range unresolved {
		values = append(values, receiver)
	}
	sort.Strings(values)
	return values
}

type javaRuntimeConstruction struct {
	class       javaClass
	constructor *javaMethod
}

func (builder *javaRuntimeReachabilityBuilder) resolveConstructions(
	statement javaStatement,
	source string,
	owner javaClass,
	variableTypes map[string]string,
) []javaRuntimeConstruction {
	code := javaCodeOnly(source)
	var resolved []javaRuntimeConstruction
	seen := map[string]bool{}
	for _, match := range javaObjectConstructionRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		typeName := strings.TrimSpace(code[match[2]:match[3]])
		class, ok := builder.index.classForType(owner, typeName)
		if !ok {
			if builder.simpleTypes[javaComparableType(typeName)] ||
				builder.index.archivedNestedType(owner, typeName) {
				builder.addUnresolved(fmt.Sprintf(
					"Construction of archived Java type %s at %s:%d is ambiguous or import-unresolved; exact runtime reachability is unavailable.",
					typeName, statement.File, statement.StartLine,
				))
			}
			continue
		}
		if len(class.Unparsed) > 0 {
			builder.addUnresolved(fmt.Sprintf(
				"Construction of archived Java type %s at %s:%d depends on an incomplete parsed type; exact runtime reachability is unavailable.",
				javaQualifiedClassName(class), statement.File, statement.StartLine,
			))
			continue
		}
		args, argsOK := javaCallArguments(source, code, match[3])
		if !argsOK {
			builder.addUnresolved(fmt.Sprintf(
				"Constructor arguments for %s at %s:%d could not be parsed exactly.",
				javaQualifiedClassName(class), statement.File, statement.StartLine,
			))
			continue
		}
		if len(class.Constructors) == 0 {
			if len(args) != 0 {
				builder.addUnresolved(fmt.Sprintf(
					"Construction of %s at %s:%d supplies arguments, but no matching archived constructor was parsed.",
					javaQualifiedClassName(class), statement.File, statement.StartLine,
				))
				continue
			}
			key := javaEvidenceOwnerKey(class) + "\x00<implicit>"
			if !seen[key] {
				seen[key] = true
				resolved = append(resolved, javaRuntimeConstruction{class: class})
			}
			continue
		}
		constructors := javaHelperOverloadsForArguments(class.Constructors, args, variableTypes)
		if len(constructors) != 1 {
			builder.addUnresolved(fmt.Sprintf(
				"Construction of %s at %s:%d did not resolve to exactly one archived constructor.",
				javaQualifiedClassName(class), statement.File, statement.StartLine,
			))
			continue
		}
		constructor := constructors[0]
		key := javaHelperVisitKey(class, constructor)
		if seen[key] {
			continue
		}
		seen[key] = true
		resolved = append(resolved, javaRuntimeConstruction{class: class, constructor: &constructor})
	}
	return resolved
}

func javaProgramContainsSimpleType(program javaProgram, typeName string) bool {
	return javaRuntimeSimpleTypeIndex(program)[javaComparableType(typeName)]
}

func javaRuntimeSimpleTypeIndex(program javaProgram) map[string]bool {
	index := make(map[string]bool, len(program.Classes)*2)
	for _, class := range program.Classes {
		index[javaComparableType(class.Name)] = true
		index[javaComparableType(strings.TrimSuffix(filepath.Base(class.File), filepath.Ext(class.File)))] = true
		for _, match := range javaTypeDeclarationRe.FindAllStringSubmatch(javaCodeOnly(class.Source), -1) {
			if len(match) == 3 {
				index[javaComparableType(match[2])] = true
			}
		}
	}
	return index
}

func (builder *javaRuntimeReachabilityBuilder) addUnresolved(message string) {
	if strings.TrimSpace(message) != "" {
		builder.reachability.unresolved = append(builder.reachability.unresolved, message)
	}
}

func javaProgramTransactionStats(reachability javaVuserRuntimeReachability) (emits bool, actionEmits bool, markers int, transactions int) {
	for _, reachable := range reachability.statements {
		for _, fn := range javaLRToParsedFunctionsBound(
			reachable.statement,
			"action",
			reachable.owner,
			reachable.current,
			reachability.evidenceIndex,
			reachable.variableTypes,
		) {
			switch fn.Name {
			case "lr_start_transaction":
				emits = true
				if reachable.actionRepeated {
					actionEmits = true
				}
				markers++
				transactions++
			case "lr_set_transaction":
				// set_transaction(name, duration, status) publishes one
				// already-completed business transaction. The shim emits a
				// timestamped start/end event pair for it, so it suppresses
				// the automatic iteration transaction just like an explicit
				// start/end pair in the action lifecycle.
				emits = true
				if reachable.actionRepeated {
					actionEmits = true
				}
				markers += 2
				transactions++
			case "lr_end_transaction", "lr_start_sub_transaction", "lr_end_sub_transaction", "lr_end_sub_trans":
				emits = true
				if reachable.actionRepeated {
					actionEmits = true
				}
				markers++
			}
		}
	}
	return emits, actionEmits, markers, transactions
}

// recordJavaProgramTransactionNames captures every reachable Java transaction
// publisher for the project-level declared-transaction conservation check.
// Initializing the map even when no calls are present distinguishes an
// inspected Java source from importer branches that do not support this check.
func recordJavaProgramTransactionNames(result *ConvertResult, reachability javaVuserRuntimeReachability) {
	if result == nil {
		return
	}
	if result.sourceTransactionNames == nil {
		result.sourceTransactionNames = make(map[string]struct{})
	}
	for _, reachable := range reachability.statements {
		for _, fn := range javaLRToParsedFunctionsBound(
			reachable.statement,
			"action",
			reachable.owner,
			reachable.current,
			reachability.evidenceIndex,
			reachable.variableTypes,
		) {
			switch fn.Name {
			case "lr_start_transaction", "lr_set_transaction":
				recordSourceTransactionName(result, convertutil.FirstNonEmpty(sourceControlArgAt(fn, 0), fn.StepName))
			}
		}
	}
}

type javaCombinedMappableEntry struct {
	statement     javaStatement
	scopeKey      string
	statementPath string
	owner         javaClass
	current       javaMethod
	variableTypes map[string]string
	typesComplete bool
	// preflightIncomplete distinguishes a global governed work-limit stop from
	// an individual variable-scope snapshot that could not be retained.
	preflightIncomplete bool
}

type javaCombinedMappableCollection struct {
	entries               []javaCombinedMappableEntry
	blocker               string
	scopeSnapshotBindings int64
	scopeSnapshotsLimited bool
}

// javaCombinedMappableEntries enumerates every non-empty source member once
// while retaining its declaring member and structural statement path. Line
// numbers alone are not a stable identity: minified Java can contain identical
// statements from different lifecycle methods on the same physical line.
func javaCombinedMappableEntries(program javaProgram) []javaCombinedMappableEntry {
	return collectJavaCombinedMappableEntries(program).entries
}

func collectJavaCombinedMappableEntries(program javaProgram) javaCombinedMappableCollection {
	collection := javaCombinedMappableCollection{
		entries: make([]javaCombinedMappableEntry, 0),
	}
	cloneScope := func(source map[string]string, retained bool) map[string]string {
		if collection.scopeSnapshotsLimited {
			if retained {
				return nil
			}
			return make(map[string]string)
		}
		bindings := int64(len(source))
		if bindings > javaVariableScopeSnapshotBindingLimit-collection.scopeSnapshotBindings {
			collection.scopeSnapshotsLimited = true
			collection.blocker = fmt.Sprintf(
				"Combined Java variable-scope analysis exceeded the deterministic snapshot limit of %d retained bindings; simplify generated local declarations or split unused support source before re-import. Promotion is blocked because exact statement-level type binding was not completed.",
				javaVariableScopeSnapshotBindingLimit,
			)
			if retained {
				return nil
			}
			return make(map[string]string)
		}
		collection.scopeSnapshotBindings += bindings
		return cloneJavaVariableTypes(source)
	}
	add := func(
		statement javaStatement,
		scopeKey string,
		statementPath string,
		owner javaClass,
		current javaMethod,
		variableTypes map[string]string,
	) {
		if javaNormalizeStatementText(statement.Text) == "" {
			return
		}
		snapshot := cloneScope(variableTypes, true)
		collection.entries = append(collection.entries, javaCombinedMappableEntry{
			statement:     statement,
			scopeKey:      scopeKey,
			statementPath: statementPath,
			owner:         owner,
			current:       current,
			variableTypes: snapshot,
			typesComplete: !collection.scopeSnapshotsLimited,
		})
	}
	var addLeaf func(javaStatement, string, string, javaClass, javaMethod, map[string]string)
	addLeaf = func(
		statement javaStatement,
		scopeKey string,
		statementPath string,
		owner javaClass,
		current javaMethod,
		variableTypes map[string]string,
	) {
		if statement.Kind == "block" && javaTransparentBlockHeader(statement.Header) {
			childTypes := cloneScope(variableTypes, false)
			collectJavaDeclaredVariableTypes(childTypes, statement.Header)
			for index, child := range statement.Children {
				addLeaf(
					child,
					scopeKey,
					javaRuntimeChildPath(statementPath, index),
					owner,
					current,
					childTypes,
				)
				if child.Kind != "block" {
					collectJavaDeclaredVariableTypes(childTypes, child.Text)
				}
			}
			return
		}
		add(statement, scopeKey, statementPath, owner, current, variableTypes)
	}
	for _, class := range program.Classes {
		classTypes := javaDeclaredVariableTypes(class, javaMethod{})
		for fieldIndex, field := range class.Fields {
			add(field, javaRuntimeFieldScopeKey(class, fieldIndex), "", class, javaMethod{}, classTypes)
		}
		for initializerIndex, initializer := range javaClassInitializerStatements(class) {
			scopeKey := javaRuntimeInitializerScopeKey(class, initializerIndex)
			types := cloneScope(classTypes, false)
			for statementIndex, statement := range initializer.Children {
				addLeaf(statement, scopeKey, strconv.Itoa(statementIndex), class, javaMethod{}, types)
				if statement.Kind != "block" {
					collectJavaDeclaredVariableTypes(types, statement.Text)
				}
			}
		}
		for _, constructor := range class.Constructors {
			scopeKey := javaRuntimeMethodScopeKey(class, constructor)
			types := cloneScope(classTypes, false)
			for _, parameter := range constructor.Parameters {
				if parameter.Name != "" && parameter.Type != "" {
					types[parameter.Name] = parameter.Type
				}
			}
			for statementIndex, statement := range constructor.Statements {
				addLeaf(statement, scopeKey, strconv.Itoa(statementIndex), class, constructor, types)
				if statement.Kind != "block" {
					collectJavaDeclaredVariableTypes(types, statement.Text)
				}
			}
		}
		for _, method := range sortedJavaMethods(class) {
			scopeKey := javaRuntimeMethodScopeKey(class, method)
			types := cloneScope(classTypes, false)
			for _, parameter := range method.Parameters {
				if parameter.Name != "" && parameter.Type != "" {
					types[parameter.Name] = parameter.Type
				}
			}
			for statementIndex, statement := range method.Statements {
				addLeaf(statement, scopeKey, strconv.Itoa(statementIndex), class, method, types)
				if statement.Kind != "block" {
					collectJavaDeclaredVariableTypes(types, statement.Text)
				}
			}
		}
	}
	for index, statement := range program.Unparsed {
		add(statement, "unparsed\x00"+strconv.Itoa(index), "", javaClass{}, javaMethod{}, nil)
	}
	return collection
}

func javaCombinedMappableStatements(program javaProgram) []javaStatement {
	entries := javaCombinedMappableEntries(program)
	statements := make([]javaStatement, 0, len(entries))
	for _, entry := range entries {
		statements = append(statements, entry.statement)
	}
	return statements
}

func javaCombinedMappableFunctionCount(program javaProgram) int {
	return javaCombinedMappableFunctionCountForEntries(
		program,
		collectJavaCombinedMappableEntries(program).entries,
		nil,
	)
}

func javaCombinedMappableFunctionCountForEntries(
	program javaProgram,
	entries []javaCombinedMappableEntry,
	evidenceIndex *javaEvidenceIndex,
) int {
	count := 0
	index := javaSharedEvidenceIndexForPass(evidenceIndex, program)
	for _, entry := range entries {
		if entry.statement.Kind == "unparsed" || !entry.typesComplete {
			count++
			continue
		}
		if calls := len(javaLRCallPartsBound(
			entry.statement.Text,
			entry.owner,
			entry.current,
			index,
			entry.variableTypes,
		)); calls > 0 {
			count += calls
		} else {
			count++
		}
	}
	return count
}

func sortedJavaMethods(class javaClass) []javaMethod {
	methods := make([]javaMethod, 0)
	if len(class.MethodOverloads) > 0 {
		for _, overloads := range class.MethodOverloads {
			methods = append(methods, overloads...)
		}
	} else {
		for _, method := range class.Methods {
			methods = append(methods, method)
		}
	}
	sort.SliceStable(methods, func(i, j int) bool {
		if methods[i].File != methods[j].File {
			return methods[i].File < methods[j].File
		}
		if methods[i].StartLine != methods[j].StartLine {
			return methods[i].StartLine < methods[j].StartLine
		}
		if methods[i].Name != methods[j].Name {
			return methods[i].Name < methods[j].Name
		}
		return methods[i].ParameterSignature < methods[j].ParameterSignature
	})
	return methods
}

func sortedJavaMethodNames(methods map[string]javaMethod) []string {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func appendJavaCombinedSourceMappings(result *ConvertResult, program javaProgram, externalLifecycle []javaExternalLifecycleMethod, reachability javaVuserRuntimeReachability) {
	appendJavaCombinedSourceMappingsForEntries(
		result,
		program,
		collectJavaCombinedMappableEntries(program).entries,
		externalLifecycle,
		reachability,
	)
}

func appendJavaCombinedSourceMappingsForEntries(
	result *ConvertResult,
	program javaProgram,
	entries []javaCombinedMappableEntry,
	externalLifecycle []javaExternalLifecycleMethod,
	reachability javaVuserRuntimeReachability,
) {
	if result == nil {
		return
	}
	// The reachability pass already indexed this program; reuse its evidence
	// index for mapping instead of rescanning every class source twice more.
	evidenceIndex := javaSharedEvidenceIndexForPass(reachability.evidenceIndex, program)
	mappingIndex := newJavaCombinedMappingIndexWithEvidenceIndex(program, evidenceIndex)
	targetName := javaVuserClassName(program)
	for _, entry := range entries {
		statement := entry.statement
		targetPath := javaCombinedTargetPathWithIndex(entry, reachability, mappingIndex)
		if statement.Kind == "unparsed" || !entry.typesComplete {
			// Unparsed regions were never analyzed, so they are review evidence,
			// not converted work: counting them external_required would let the
			// fidelity report claim full conversion for source the scanner could
			// not read.
			appendJavaCombinedMapping(
				result,
				targetName,
				statement,
				targetPath,
				"java_statement",
				"review_only",
				SourceFunctionSupportReviewOnly,
				javaCombinedReviewOnlyMappingNote(entry),
			)
			continue
		}

		functions := javaLRToParsedFunctionsBound(
			statement,
			javaCombinedPhaseForTargetPath(targetPath),
			entry.owner,
			entry.current,
			evidenceIndex,
			entry.variableTypes,
		)
		if len(functions) == 0 {
			appendJavaCombinedMapping(
				result,
				targetName,
				statement,
				targetPath,
				"java_statement",
				"converted",
				SourceFunctionSupportExternalRequired,
				"Preserved inside the combined Java Vuser source-order draft for governed phase replay on the same persistent per-VUser Actions instance; Java fields, object state, and saved LoadRunner parameters survive init, action, and end requests.",
			)
			continue
		}
		for _, fn := range functions {
			mode := SourceFunctionSupportCustomReview
			status := statusForSupportMode(mode)
			notes := "The combined Java runner has no verified lrapi.lr contract for this call; preserve it for authoring and fidelity review."
			if def, runnerNotes, known := javaLRRunnerCallDisposition(fn); known {
				mode = def.SupportMode
				status = statusForSupportMode(mode)
				notes = runnerNotes
				if mode == SourceFunctionSupportNativeNoop {
					notes = "Preserved and invoked by the combined Java runner as an intentional no-op. " + notes
				}
			}
			appendJavaCombinedMapping(
				result,
				targetName,
				statement,
				targetPath,
				fn.Name,
				status,
				mode,
				notes,
			)
		}
	}
	for _, issue := range externalLifecycle {
		notes := fmt.Sprintf(
			"Preserved for remediation because Java %s lifecycle method %s.%s is outside selected runner class %s and cannot execute through the selected single-class runner target.",
			issue.Phase, issue.ClassName, issue.MethodName, javaVuserClassName(program),
		)
		appendSourceReviewMapping(result, SourceReviewMapping{
			SourceFile:  issue.SourceFile,
			StartLine:   issue.StartLine,
			EndLine:     issue.EndLine,
			Function:    issue.MethodName,
			TargetPath:  "metadata.import.preserved." + javaExternalLifecycleKey,
			TargetName:  issue.ClassName + "." + issue.MethodName,
			TargetKind:  "review",
			Status:      "review_only",
			SupportMode: string(SourceFunctionSupportReviewOnly),
			Notes:       notes,
		})
		recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
	}
}

func javaCombinedReviewOnlyMappingNote(entry javaCombinedMappableEntry) string {
	if entry.statement.Kind == "unparsed" {
		return "Preserved verbatim for review because the Java scanner could not parse this source region."
	}
	if entry.preflightIncomplete {
		return "Preserved verbatim for review because governed Java overload/runtime-reachability preflight reached its deterministic work limit; see the runner-semantics blocker for remediation."
	}
	return "Preserved verbatim for review because the governed variable-scope snapshot limit was reached before exact statement-level type binding could be retained."
}

func appendJavaCombinedMapping(
	result *ConvertResult,
	targetName string,
	statement javaStatement,
	targetPath string,
	function string,
	status string,
	mode SourceFunctionSupportMode,
	notes string,
) {
	appendSourceReviewMapping(result, SourceReviewMapping{
		SourceFile:  statement.File,
		StartLine:   statement.StartLine,
		EndLine:     statement.EndLine,
		Function:    function,
		TargetPath:  targetPath,
		TargetName:  targetName,
		TargetKind:  "action",
		Status:      status,
		SupportMode: string(mode),
		Notes:       notes,
	})
	recordSourceFunctionSupport(result, mode)
}

func javaCombinedPhaseForTargetPath(targetPath string) string {
	switch {
	case strings.HasPrefix(targetPath, "init["):
		return "init"
	case strings.HasPrefix(targetPath, "teardown["):
		return "teardown"
	default:
		return "action"
	}
}

// javaVuserRunnerSemanticBlockers considers only source that can run while the
// selected Actions instance is constructed or one of its lifecycle methods is
// invoked. Review-only calls in unused helper methods remain visible in the
// source mapping, but do not falsely block an otherwise complete draft.
func javaVuserRunnerSemanticBlockers(reachability javaVuserRuntimeReachability) []string {
	seen := map[string]bool{}
	blockers := make([]string, 0)
	for _, reachable := range reachability.statements {
		statement := reachable.statement
		for _, fn := range javaLRToParsedFunctionsBound(
			statement,
			"action",
			reachable.owner,
			reachable.current,
			reachability.evidenceIndex,
			reachable.variableTypes,
		) {
			if seen[fn.Name] {
				continue
			}
			def, notes, known := javaLRRunnerCallDisposition(fn)
			if known && def.SupportMode != SourceFunctionSupportReviewOnly && def.SupportMode != SourceFunctionSupportCustomReview {
				continue
			}
			seen[fn.Name] = true
			mode := SourceFunctionSupportCustomReview
			if known {
				mode = def.SupportMode
			}
			location := strings.TrimSpace(statement.File)
			if statement.StartLine > 0 {
				location = fmt.Sprintf("%s:%d", location, statement.StartLine)
			}
			if location == "" {
				location = "uploaded Java source"
			}
			if strings.TrimSpace(notes) == "" {
				notes = "No verified combined-runner behavior is available."
			}
			blockers = append(blockers, fmt.Sprintf(
				"Combined Java Vuser source reaches %s at %s, but the governed runner classifies it as %s: %s",
				fn.Name, location, mode, notes,
			))
		}
	}
	blockers = append(blockers, reachability.unresolved...)
	return uniqueSortedStrings(blockers)
}

func javaVuserReachableStatements(program javaProgram) []javaStatement {
	class, ok := javaVuserClass(program)
	if !ok {
		return nil
	}
	statements := make([]javaStatement, 0)
	seen := map[string]bool{}
	add := func(statement javaStatement) {
		for _, leaf := range javaLeafStatementList(statement) {
			text := javaNormalizeStatementText(leaf.Text)
			if text == "" {
				continue
			}
			key := strings.Join([]string{
				javaReviewPathKey(leaf.File),
				strconv.Itoa(leaf.StartLine),
				strconv.Itoa(leaf.EndLine),
				text,
			}, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			statements = append(statements, leaf)
		}
	}
	for _, field := range class.Fields {
		add(field)
	}
	for _, constructor := range class.Constructors {
		if !javaMethodIsPublicZeroArg(constructor) {
			continue
		}
		visited := map[string]bool{javaHelperVisitKey(class, constructor): true}
		for _, statement := range inlineJavaVuserHelperStatements(constructor.Statements, class, constructor, program, visited, 0) {
			add(statement)
		}
		break
	}
	for _, phase := range []string{"init", "action", "teardown"} {
		for _, statement := range javaVuserLifecycleStatements(program, phase) {
			add(statement)
		}
	}
	return statements
}

type javaCombinedMappingIndex struct {
	lifecycleScopes    map[string]string
	constructionScopes map[string]bool
	hasInit            bool
}

func newJavaCombinedMappingIndex(program javaProgram) *javaCombinedMappingIndex {
	return newJavaCombinedMappingIndexWithEvidenceIndex(program, nil)
}

func newJavaCombinedMappingIndexWithEvidenceIndex(
	program javaProgram,
	sharedIndex *javaEvidenceIndex,
) *javaCombinedMappingIndex {
	index := &javaCombinedMappingIndex{
		lifecycleScopes:    map[string]string{},
		constructionScopes: map[string]bool{},
		hasInit:            javaProgramHasLifecycleMethod(program, "init"),
	}
	for _, lifecycle := range []struct {
		phase string
		path  string
	}{{"init", "init[0]"}, {"action", "actions[0]"}, {"teardown", "teardown[0]"}} {
		if class, ok := javaVuserClass(program); ok {
			if method, exists := javaVuserLifecycleMethod(program, lifecycle.phase); exists {
				index.lifecycleScopes[javaRuntimeMethodScopeKey(class, method)] = lifecycle.path
			}
		}
	}

	selected, ok := javaVuserClass(program)
	if !ok {
		return index
	}
	evidenceIndex := javaSharedEvidenceIndexForPass(sharedIndex, program)
	for _, class := range evidenceIndex.classHierarchy(selected) {
		for fieldIndex := range class.Fields {
			index.constructionScopes[javaRuntimeFieldScopeKey(class, fieldIndex)] = true
		}
		for initializerIndex := range javaClassInitializerStatements(class) {
			index.constructionScopes[javaRuntimeInitializerScopeKey(class, initializerIndex)] = true
		}
		for _, constructor := range class.Constructors {
			index.constructionScopes[javaRuntimeMethodScopeKey(class, constructor)] = true
		}
	}
	return index
}

func javaCombinedTargetPath(
	entry javaCombinedMappableEntry,
	program javaProgram,
	reachability javaVuserRuntimeReachability,
) string {
	return javaCombinedTargetPathWithIndex(entry, reachability, newJavaCombinedMappingIndex(program))
}

func javaCombinedTargetPathWithIndex(
	entry javaCombinedMappableEntry,
	reachability javaVuserRuntimeReachability,
	index *javaCombinedMappingIndex,
) string {
	if reachable, ok := reachability.statementReachability(entry); ok {
		switch {
		case reachable.actionRepeated:
			return "actions[0]"
		case reachable.phases["init"]:
			return "init[0]"
		case reachable.phases["action"]:
			return "actions[0]"
		case reachable.phases["teardown"]:
			return "teardown[0]"
		case reachable.phases["construction"] && index.hasInit:
			return "init[0]"
		case reachable.phases["construction"]:
			return "actions[0]"
		}
	}
	if path := index.lifecycleScopes[entry.scopeKey]; path != "" {
		return path
	}
	if index.constructionScopes[entry.scopeKey] {
		if index.hasInit {
			return "init[0]"
		}
		return "actions[0]"
	}
	return "actions[0]"
}

func (reachability javaVuserRuntimeReachability) statementReachability(entry javaCombinedMappableEntry) (javaRuntimeReachableStatement, bool) {
	if index, exists := reachability.byOccurrence[javaRuntimeOccurrenceKey(entry.scopeKey, entry.statementPath)]; exists &&
		index >= 0 && index < len(reachability.statements) {
		return reachability.statements[index], true
	}
	if reachable, exists := reachability.scopeReachability[entry.scopeKey]; exists {
		return reachable, true
	}
	// Retain a narrow compatibility fallback for callers that do not have a
	// structural occurrence match. Never merge multiple same-line candidates:
	// doing so recreates the init/action aliasing this identity model prevents.
	indices := reachability.byStatement[javaRuntimeStatementKey(entry.statement)]
	if len(indices) == 1 && indices[0] >= 0 && indices[0] < len(reachability.statements) {
		return reachability.statements[indices[0]], true
	}
	return javaRuntimeReachableStatement{}, false
}

func javaVuserClassName(program javaProgram) string {
	if class, ok := javaVuserClass(program); ok {
		return javaQualifiedClassName(class)
	}
	return "Actions"
}

func javaVuserClass(program javaProgram) (javaClass, bool) {
	if program.vuserSourceSelectionBlocker != "" {
		return javaClass{}, false
	}
	if program.selectedVuserClassFile != "" {
		for _, class := range program.Classes {
			if javaSourcePathEqual(class.File, program.selectedVuserClassFile) &&
				(program.selectedVuserClassName == "" || strings.EqualFold(javaQualifiedClassName(class), program.selectedVuserClassName)) {
				return class, true
			}
		}
	}
	for _, class := range program.Classes {
		if strings.EqualFold(strings.TrimSpace(class.Name), "Actions") {
			return class, true
		}
	}
	// Prefer the class that owns an action-phase lifecycle method over the
	// alphabetically-first file: selecting a bundled helper/SDK class as the
	// runner target would declare the real lifecycle owner "external" and block
	// promotion with impossible merge-into-Actions remediation advice.
	for _, class := range program.Classes {
		if _, ok := javaExecutableLifecycleMethodInClass(class, "action"); ok {
			return class, true
		}
	}
	if len(program.Classes) > 0 {
		return program.Classes[0], true
	}
	return javaClass{}, false
}

// javaProgramWithProjectVuserClass makes the .usr action-source declaration
// authoritative when an archive contains more than one class named Actions.
// LoadRunner archives may include backup or packaged Java sources; alphabetical
// zip order must not redirect lifecycle execution to one of those classes.
func javaProgramWithProjectVuserClass(program javaProgram, projectConfig *ProjectConfig) javaProgram {
	class, declared, blocker := javaVuserClassForProject(program, projectConfig)
	if blocker != "" {
		program.vuserSourceSelectionBlocker = blocker
		program.vuserSourceSelectionRef = javaProjectActionLifecycleSource(projectConfig)
		return program
	}
	if !declared {
		return program
	}
	program.selectedVuserClassFile = class.File
	program.selectedVuserClassName = javaQualifiedClassName(class)
	return program
}

func javaProjectActionLifecycleSource(projectConfig *ProjectConfig) string {
	if projectConfig == nil {
		return ""
	}
	for _, sourceFile := range projectConfig.SourceActionFiles {
		if !strings.EqualFold(strings.TrimSpace(sourceFile.Language), "java") &&
			!strings.HasSuffix(strings.ToLower(strings.TrimSpace(sourceFile.Name)), ".java") {
			continue
		}
		phase := normalizeSourcePhase(sourceFile.Phase)
		if phase == "" {
			phase = sourceActionPhase(sourceFile.Name)
		}
		if phase == "action" {
			return normalizeRuntimeArchivePath(sourceFile.Name)
		}
	}
	return ""
}

func javaVuserClassForProject(
	program javaProgram,
	projectConfig *ProjectConfig,
) (class javaClass, declared bool, blocker string) {
	if projectConfig == nil {
		return javaClass{}, false, ""
	}
	for _, sourceFile := range projectConfig.SourceActionFiles {
		if !strings.EqualFold(strings.TrimSpace(sourceFile.Language), "java") &&
			!strings.HasSuffix(strings.ToLower(strings.TrimSpace(sourceFile.Name)), ".java") {
			continue
		}
		phase := strings.ToLower(strings.TrimSpace(sourceFile.Phase))
		if phase == "" {
			phase = sourceActionPhase(sourceFile.Name)
		}
		if phase != "action" {
			continue
		}
		class, candidates, ok := javaVuserClassForSourceFile(program, sourceFile.Name)
		if ok {
			return class, true, ""
		}
		pointer := normalizeRuntimeArchivePath(sourceFile.Name)
		if len(candidates) > 1 {
			return javaClass{}, true, fmt.Sprintf(
				"Declared Java action lifecycle source %q matched multiple archive files (%s); runner-class selection is ambiguous and must be corrected before promotion.",
				pointer,
				strings.Join(candidates, ", "),
			)
		}
		// Preserve the established Java-only recovery for a missing, generic
		// unqualified Actions.java declaration: when exactly one parsed class
		// owns action(), javaVuserClass can still identify it structurally.
		// Qualified pointers and duplicate-basename matches never take this
		// fallback.
		if len(candidates) == 0 && !strings.Contains(pointer, "/") {
			return javaClass{}, false, ""
		}
		return javaClass{}, true, fmt.Sprintf(
			"Declared Java action lifecycle source %q could not be resolved exactly; no same-basename source was substituted and the declaration must be corrected before promotion.",
			pointer,
		)
	}
	return javaClass{}, false, ""
}

func javaVuserClassForSourceFile(
	program javaProgram,
	sourceFile string,
) (javaClass, []string, bool) {
	configured := javaNormalizedSourcePath(sourceFile)
	if configured == "" {
		return javaClass{}, nil, false
	}
	files := make(map[string]string)
	for _, class := range program.Classes {
		normalized := javaNormalizedSourcePath(class.File)
		if normalized != "" {
			files[normalized] = class.File
		}
	}
	matchedFiles := make([]string, 0)
	if actual, ok := files[configured]; ok {
		matchedFiles = append(matchedFiles, actual)
	} else if !strings.Contains(configured, "/") {
		for normalized, actual := range files {
			if filepath.Base(normalized) == filepath.Base(configured) {
				matchedFiles = append(matchedFiles, actual)
			}
		}
	}
	matchedFiles = uniqueSortedStrings(matchedFiles)
	if len(matchedFiles) != 1 {
		return javaClass{}, matchedFiles, false
	}
	resolvedFile := javaNormalizedSourcePath(matchedFiles[0])
	matches := make([]javaClass, 0)
	for _, class := range program.Classes {
		if javaNormalizedSourcePath(class.File) == resolvedFile {
			matches = append(matches, class)
		}
	}

	baseType := strings.TrimSuffix(filepath.Base(resolvedFile), filepath.Ext(resolvedFile))
	for _, requireAction := range []bool{true, false} {
		for _, class := range matches {
			if requireAction != javaClassHasLifecycleMethod(class, "action") {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(class.Name), baseType) {
				return class, matchedFiles, true
			}
		}
		for _, class := range matches {
			if requireAction != javaClassHasLifecycleMethod(class, "action") {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(class.Name), "Actions") {
				return class, matchedFiles, true
			}
		}
		for _, class := range matches {
			if requireAction == javaClassHasLifecycleMethod(class, "action") {
				return class, matchedFiles, true
			}
		}
	}
	return javaClass{}, matchedFiles, false
}

func javaClassHasLifecycleMethod(class javaClass, lifecycle string) bool {
	_, ok := javaExecutableLifecycleMethodInClass(class, lifecycle)
	return ok
}

func javaSourcePathMatchRank(actual, configured string) int {
	actualPath := javaNormalizedSourcePath(actual)
	configuredPath := javaNormalizedSourcePath(configured)
	if actualPath == "" || configuredPath == "" {
		return 3
	}
	if actualPath == configuredPath {
		return 0
	}
	if strings.HasSuffix(actualPath, "/"+configuredPath) {
		return 1
	}
	if filepath.Base(actualPath) == filepath.Base(configuredPath) {
		return 2
	}
	return 3
}

func javaSourcePathEqual(left, right string) bool {
	return javaNormalizedSourcePath(left) == javaNormalizedSourcePath(right)
}

func javaNormalizedSourcePath(value string) string {
	value = strings.TrimSpace(cleanConfigValue(value))
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = filepath.Clean(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	return strings.ToLower(strings.TrimSpace(value))
}

func javaQualifiedClassName(class javaClass) string {
	name := strings.TrimSpace(class.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(class.File), filepath.Ext(class.File))
	}
	if pkg := strings.TrimSpace(class.Package); pkg != "" {
		return pkg + "." + name
	}
	return name
}

func javaVuserMappingStatement(program javaProgram) javaStatement {
	for _, lifecycle := range []string{"action", "init", "teardown"} {
		if method, ok := javaVuserLifecycleMethod(program, lifecycle); ok {
			if len(method.Statements) > 0 {
				return method.Statements[0]
			}
			return javaStatement{File: method.File, StartLine: method.StartLine, EndLine: method.EndLine, Text: method.Name}
		}
	}
	for _, class := range program.Classes {
		for _, method := range class.Methods {
			if len(method.Statements) > 0 {
				return method.Statements[0]
			}
			return javaStatement{File: method.File, StartLine: method.StartLine, EndLine: method.EndLine, Text: method.Name}
		}
	}
	return javaStatement{File: "Actions.java", StartLine: 1, EndLine: 1, Text: "Java Vuser lifecycle"}
}

// attachJavaTruststoreArtifact is a retired executable-lane body. It is still
// production resident and still compiled in, but it has no production caller:
// its only caller is the quarantined lane orchestration in
// java_native_lanes_test.go. Changing it changes nothing for any import.
func attachJavaTruststoreArtifact(result *ConvertResult, files map[string]string) {
	if result == nil || result.Script == nil {
		return
	}
	truststore := findTruststoreArtifact(files)
	if truststore == "" {
		return
	}
	if result.Script.Config.JMS.TLS == nil {
		result.Script.Config.JMS.TLS = &dsl.JMSTLSConfig{}
	}
	if result.Script.Config.JMS.TLS.TrustStoreRef == "" {
		result.Script.Config.JMS.TLS.TrustStoreRef = filepath.Base(truststore)
		result.Warnings = append(result.Warnings, fmt.Sprintf("Java archive truststore %q was preserved as config.jms.tls.trust_store_ref; provision it as governed runtime material before replay.", filepath.Base(truststore)))
	}
}

// convertJavaStatement is a retired executable-lane body, and so is the whole
// javaStatementMapperRegistry subgraph it drives (java_idiom_registry.go,
// including the JMS idiom mapper). It is still production resident and still
// compiled in, but apart from its own recursion below it has no production
// caller: the lane orchestration that drove it is quarantined in
// java_native_lanes_test.go. Fixing a mis-mapped statement here changes nothing
// for any import — ConvertJava only ever emits the combined runner draft.
func convertJavaStatement(result *ConvertResult, state *javaImportState, files map[string]string, phase string, statement javaStatement) error {
	text := javaNormalizeStatementText(statement.Text)
	if text == "" {
		return nil
	}
	matchStatement := statement
	matchStatement.Text = text
	if statement.Kind == "block" {
		if javaTransparentBlockHeader(statement.Header) {
			for _, child := range statement.Children {
				if err := convertJavaStatement(result, state, files, phase, child); err != nil {
					return err
				}
			}
			return nil
		}
		appendJavaReview(result, state, statement, phase, "unmapped_java_block", phase == "action")
		return nil
	}
	ctx := &javaMapperContext{
		result:    result,
		state:     state,
		files:     files,
		phase:     phase,
		statement: matchStatement,
		text:      text,
	}
	for _, mapper := range javaStatementMapperRegistry {
		handled, err := mapper.Map(ctx)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	return nil
}

func javaStatementLooksLikeDatabase(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "preparedstatement") ||
		strings.Contains(lower, "drivermanager") ||
		strings.Contains(lower, "executequery(") ||
		strings.Contains(lower, "executeupdate(") ||
		strings.Contains(lower, "preparestatement(") ||
		javaStatementIsSQLTransactionControl(text) ||
		strings.Contains(lower, ".setautocommit(false")
}

func javaStatementIsSQLTransactionControl(text string) bool {
	_, ok := javaSQLTransactionControlFunction(text)
	return ok
}

func javaNamedReviewReason(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "javax.ejb") || strings.Contains(lower, "jakarta.ejb"):
		return "ejb_review"
	case strings.Contains(lower, "org.omg.corba") || strings.Contains(lower, "corba.orb"):
		return "corba_review"
	case strings.Contains(lower, "naming.lookup") || strings.Contains(lower, "initialcontext.lookup") || strings.Contains(lower, "javax.rmi"):
		return "rmi_java_review"
	case strings.Contains(lower, "sapgui") || strings.Contains(lower, "com.sap"):
		return "sapgui_java_review"
	case strings.Contains(lower, "jacada"):
		return "jacada_java_review"
	case strings.Contains(lower, "oracle.apps") || strings.Contains(lower, "oracle.forms") || strings.Contains(lower, "oracle.nca"):
		return "oracle_nca_java_review"
	case strings.Contains(lower, "tuxedo") || strings.Contains(lower, "weblogic.wtc"):
		return "tuxedo_java_review"
	case strings.Contains(lower, "javax.xml.ws") || strings.Contains(lower, "soapmessage") || strings.Contains(lower, "soapconnection"):
		return "soap_java_review"
	default:
		return ""
	}
}

func javaTransparentBlockHeader(header string) bool {
	normalized := javaNormalizeBlockHeader(header)
	return strings.HasPrefix(normalized, "try") ||
		strings.HasPrefix(normalized, "catch") ||
		strings.HasPrefix(normalized, "finally") ||
		strings.HasPrefix(normalized, "if") ||
		strings.HasPrefix(normalized, "else") ||
		strings.HasPrefix(normalized, "for") ||
		strings.HasPrefix(normalized, "while") ||
		strings.HasPrefix(normalized, "switch") ||
		strings.HasPrefix(normalized, "synchronized")
}

func javaNormalizeBlockHeader(header string) string {
	header = javaBlockCommentRe.ReplaceAllString(header, " ")
	lines := strings.Split(header, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.ToLower(strings.TrimSpace(strings.Join(kept, " ")))
}

func isJavaInertStatement(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	if javaDeclarationOnlyRe.MatchString(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "return ") || lower == "return;" || lower == "return 0;" ||
		strings.HasPrefix(lower, "throw ") ||
		strings.HasPrefix(lower, "import ") || strings.HasPrefix(lower, "package ") ||
		strings.Contains(lower, "new hashtable") || strings.Contains(lower, "new properties") ||
		strings.Contains(lower, "initialcontext(") || strings.Contains(lower, "connectionfactory") ||
		strings.Contains(lower, ".createconnection(") || strings.Contains(lower, ".createsession(") ||
		strings.Contains(lower, ".createproducer(") || strings.Contains(lower, ".createconsumer(") ||
		strings.Contains(lower, ".start()") || strings.Contains(lower, ".close()")
}

func convertJavaEnvPut(result *ConvertResult, state *javaImportState, files map[string]string, statement javaStatement) bool {
	matches := javaEnvPutRe.FindStringSubmatch(statement.Text)
	if len(matches) != 2 {
		return false
	}
	args := splitJavaArgs(matches[1])
	if len(args) < 2 {
		return false
	}
	key := normalizeJavaJNDIKey(args[0])
	value, ok := state.env.eval(args[1], files)
	if !ok {
		appendJavaReview(result, state, statement, "init", "unresolved_jndi_value", false)
		state.configReview++
		return true
	}
	switch key {
	case "provider_url":
		result.Script.Config.JMS.BrokerURL = value
	case "security_principal", "username":
		if user, vpn, ok := strings.Cut(value, "@"); ok {
			if result.Script.Config.JMS.Properties == nil {
				result.Script.Config.JMS.Properties = map[string]string{}
			}
			result.Script.Config.JMS.Properties["recorded_username"] = user
			result.Script.Config.JMS.Properties["recorded_security_principal"] = value
			result.Script.Config.JMS.Username = user
			if result.Script.Config.JMS.MessageVPN == "" {
				result.Script.Config.JMS.MessageVPN = vpn
			}
		} else if value != "" {
			if result.Script.Config.JMS.Properties == nil {
				result.Script.Config.JMS.Properties = map[string]string{}
			}
			result.Script.Config.JMS.Properties["recorded_username"] = value
			result.Script.Config.JMS.Username = value
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s line %d: JMS username was retained in the durable destination for review.", statement.File, statement.StartLine))
	case "security_credentials", "password":
		result.Script.Config.JMS.Password = value
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s line %d: JMS password was retained in the durable destination and is masked on ordinary reads.", statement.File, statement.StartLine))
	case "solace_jms_vpn", "message_vpn":
		result.Script.Config.JMS.MessageVPN = value
	case "trust_store", "truststore", "ssl_trust_store", "ssl_truststore":
		if result.Script.Config.JMS.TLS == nil {
			result.Script.Config.JMS.TLS = &dsl.JMSTLSConfig{}
		}
		result.Script.Config.JMS.TLS.TrustStoreRef = value
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s line %d: JMS truststore %q was preserved as config.jms.tls.trust_store_ref; provision it as a governed secret/file before replay.", statement.File, statement.StartLine, filepath.Base(value)))
	default:
		if result.Script.Config.JMS.Properties == nil {
			result.Script.Config.JMS.Properties = map[string]string{}
		}
		result.Script.Config.JMS.Properties[key] = value
	}
	appendJavaMapping(result, statement, "config.jms", key, "config", "converted", "Mapped Java JNDI environment property into JMS config.", SourceFunctionSupportExecutable)
	return true
}

func normalizeJavaJNDIKey(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"`)
	replacer := strings.NewReplacer("InitialContext.", "", "Context.", "", "SupportedProperty.", "", "javax.naming.", "", "java.naming.", "", "com.solacesystems.jms.", "", "SOLACE_JMS_", "")
	raw = replacer.Replace(raw)
	raw = strings.ToLower(raw)
	raw = strings.TrimPrefix(raw, "solace_jms_")
	raw = strings.NewReplacer(".", "_", "-", "_").Replace(raw)
	switch raw {
	case "provider_url", "initial_context_factory_provider_url":
		return "provider_url"
	case "security_principal":
		return "security_principal"
	case "security_credentials":
		return "security_credentials"
	case "vpn", "message_vpn":
		return "message_vpn"
	default:
		return raw
	}
}

func convertJavaMessageProperty(result *ConvertResult, state *javaImportState, files map[string]string, statement javaStatement) bool {
	matches := javaSetMessagePropertyRe.FindStringSubmatch(statement.Text)
	if len(matches) != 3 {
		return false
	}
	msg := state.env.Messages[matches[1]]
	if msg.Properties == nil {
		msg.Properties = map[string]string{}
	}
	args := splitJavaArgs(matches[2])
	if len(args) >= 2 {
		key, ok1 := state.env.eval(args[0], files)
		value, ok2 := state.env.eval(args[1], files)
		if !ok2 {
			trimmed := strings.TrimSpace(args[1])
			if strings.EqualFold(trimmed, "true") || strings.EqualFold(trimmed, "false") {
				value = strings.ToLower(trimmed)
				ok2 = true
			}
		}
		if ok1 && ok2 {
			msg.Properties[key] = value
			state.env.Messages[matches[1]] = msg
			appendJavaMapping(result, statement, "", key, "headers", "converted", "Mapped TextMessage string property to JMS headers.", SourceFunctionSupportExecutable)
			return true
		}
	}
	appendJavaReview(result, state, statement, "action", "unresolved_message_property", false)
	return true
}

// javaLRToParsedFunction turns a matched Java `lr.<method>(args)` statement into a
// synthetic ParsedFunction so the Java path can reuse the hardened C lr_*
// registry + family router (lookupSourceFunction / convertSourceFunctionAction)
// instead of the old coarse name-substring mapping. The LR Java API is snake_case
// and 1:1 with the C functions, so lr.start_transaction -> lr_start_transaction.
func javaLRToParsedFunction(statement javaStatement, phase string) (ParsedFunction, bool) {
	functions := javaLRToParsedFunctions(statement, phase)
	if len(functions) == 0 {
		return ParsedFunction{}, false
	}
	return functions[0], true
}

func javaLRToParsedFunctions(statement javaStatement, phase string) []ParsedFunction {
	parts := javaLRCallPartsAll(statement.Text)
	return javaLRParsedFunctionsFromParts(statement, phase, parts)
}

func javaLRToParsedFunctionsBound(
	statement javaStatement,
	phase string,
	owner javaClass,
	current javaMethod,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []ParsedFunction {
	parts := javaLRCallPartsBound(
		statement.Text,
		owner,
		current,
		index,
		variableTypes,
	)
	return javaLRParsedFunctionsFromParts(statement, phase, parts)
}

func javaLRParsedFunctionsFromParts(
	statement javaStatement,
	phase string,
	parts []javaLRCallPart,
) []ParsedFunction {
	functions := make([]ParsedFunction, 0, len(parts))
	for _, part := range parts {
		functions = append(functions, javaLRParsedFunction(
			statement,
			phase,
			part.methodName,
			part.arguments,
		))
	}
	return functions
}

func javaLRParsedFunction(
	statement javaStatement,
	phase string,
	methodName string,
	rawArgumentText string,
) ParsedFunction {
	fn := ParsedFunction{
		Name:       "lr_" + javaLRFunctionName(methodName),
		Phase:      phase,
		Filename:   statement.File,
		LineNumber: statement.StartLine,
		EndLine:    statement.EndLine,
		Args:       map[string]string{},
		Headers:    map[string]string{},
	}
	rawArgs := splitJavaArgs(rawArgumentText)
	for i, raw := range rawArgs {
		fn.RawArgs = append(fn.RawArgs, strings.TrimSpace(raw))
		fn.Args[fmt.Sprintf("arg%d", i+1)] = javaArgToSourceArg(raw)
	}
	fn.StepName = javaLRStepName(rawArgs)
	// lr.think_time(n): expose the seconds value under the key the shared
	// ExtractThinkTimeSeconds() helper reads (the C parser sets it directly).
	if fn.Name == "lr_think_time" && len(rawArgs) > 0 {
		fn.Args["seconds"] = strings.TrimSpace(cleanConfigValue(rawArgs[0]))
	}
	return fn
}

type javaLRCallPart struct {
	methodName string
	arguments  string
}

type javaLocatedLRCallPart struct {
	javaLRCallPart
	start int
	open  int
}

func javaLRCallPartsBound(
	raw string,
	owner javaClass,
	current javaMethod,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []javaLRCallPart {
	// Callers without source ownership are the retained native-analysis path.
	// Preserve its historical syntactic behavior; production combined imports
	// always supply an owning class and use binding-aware recognition below.
	if strings.TrimSpace(owner.Name) == "" {
		return javaLRCallPartsAll(raw)
	}

	code := javaCodeOnly(raw)
	located := make([]javaLocatedLRCallPart, 0)
	seenOpen := map[int]bool{}
	appendMatch := func(match []int) {
		if len(match) != 4 || match[0] < 0 || match[1] <= 0 ||
			match[2] < 0 || match[3] > len(raw) || match[1] > len(code) {
			return
		}
		open := match[1] - 1
		if code[open] != '(' || seenOpen[open] {
			return
		}
		close := matchingJavaParen(code, open)
		if close < 0 || close > len(raw) {
			return
		}
		seenOpen[open] = true
		located = append(located, javaLocatedLRCallPart{
			javaLRCallPart: javaLRCallPart{
				methodName: raw[match[2]:match[3]],
				arguments:  raw[open+1 : close],
			},
			start: match[0],
			open:  open,
		})
	}

	// A fully-qualified lrapi.lr call is unambiguous even when a local variable
	// or field is also named lr.
	for _, match := range javaFullyQualifiedLRCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) != 4 || match[0] < 0 ||
			javaPreviousNonspaceByte(code, match[0]) == '.' ||
			javaLRPackageNameShadowed(raw, owner, index, variableTypes) {
			continue
		}
		appendMatch(match)
	}

	if javaSimpleLRTypeBinding(owner, index) &&
		!javaLRReceiverShadowed(raw, owner, index, variableTypes) {
		for _, match := range javaLRCallStartRe.FindAllStringSubmatchIndex(code, -1) {
			if len(match) != 4 || match[0] < 0 {
				continue
			}
			previous := javaPreviousNonspaceByte(code, match[0])
			// `lrapi.lr.method` was already recorded above. Any other
			// expression-qualified `.lr.method` is a value receiver, not the
			// imported LoadRunner class.
			if previous == '.' {
				continue
			}
			appendMatch(match)
		}
	}

	staticImports := javaLRStaticImports(owner, index)
	if staticImports.wildcard || len(staticImports.methods) > 0 {
		for _, match := range javaUnqualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
			if len(match) != 4 || match[0] < 0 || match[2] < 0 || match[3] > len(raw) {
				continue
			}
			methodName := raw[match[2]:match[3]]
			if !javaUnqualifiedInvocationCandidate(code, match[0], methodName) {
				continue
			}
			if !staticImports.wildcard && !staticImports.methods[methodName] {
				continue
			}
			args, argsOK := javaCallArguments(raw, code, match[3])
			if !argsOK ||
				javaLRStaticCallBinding(
					owner,
					current,
					index,
					methodName,
					args,
					variableTypes,
				) != javaLRStaticCallLoadRunner {
				continue
			}
			appendMatch(match)
		}
	}

	sort.SliceStable(located, func(i, j int) bool {
		if located[i].start != located[j].start {
			return located[i].start < located[j].start
		}
		return located[i].open < located[j].open
	})
	parts := make([]javaLRCallPart, 0, len(located))
	for _, part := range located {
		parts = append(parts, part.javaLRCallPart)
	}
	return parts
}

func javaSimpleLRTypeBinding(owner javaClass, index *javaEvidenceIndex) bool {
	if index != nil {
		scope := index.importScope(owner)
		if scope.lrSimpleReady {
			return scope.lrSimpleBinding
		}
		scope.lrSimpleReady = true
		explicitLR := ""
		for _, imported := range scope.explicitTypes["lr"] {
			if explicitLR != "" && explicitLR != imported {
				return false
			}
			explicitLR = imported
		}
		wildcard := false
		for _, packageName := range scope.wildcardPackages {
			if strings.EqualFold(strings.TrimSpace(packageName), "lrapi") {
				wildcard = true
				break
			}
		}
		if class, ok := index.classForType(owner, "lr"); ok &&
			javaQualifiedClassName(class) != "lrapi.lr" {
			return false
		}
		switch {
		case explicitLR != "":
			scope.lrSimpleBinding = explicitLR == "lrapi.lr"
		case strings.EqualFold(strings.TrimSpace(owner.Package), "lrapi"):
			scope.lrSimpleBinding = true
		default:
			scope.lrSimpleBinding = wildcard
		}
		return scope.lrSimpleBinding
	}
	wildcard := false
	explicitLR := ""
	for _, imported := range owner.Imports {
		imported = strings.TrimSpace(imported)
		if strings.HasPrefix(imported, "static ") {
			continue
		}
		if strings.HasSuffix(imported, ".lr") {
			if explicitLR != "" && explicitLR != imported {
				return false
			}
			explicitLR = imported
		}
		if imported == "lrapi.*" {
			wildcard = true
		}
	}
	if explicitLR != "" {
		return explicitLR == "lrapi.lr"
	}
	if strings.EqualFold(strings.TrimSpace(owner.Package), "lrapi") {
		return true
	}
	if !wildcard {
		return false
	}
	// A same-package or otherwise archived simple type named `lr` wins over a
	// wildcard import. Do not classify its static calls as LoadRunner calls.
	return true
}

type javaLRStaticImportSet struct {
	wildcard bool
	methods  map[string]bool
}

type javaLRStaticCallBindingKind uint8

const (
	javaLRStaticCallNone javaLRStaticCallBindingKind = iota
	javaLRStaticCallLoadRunner
	javaLRStaticCallSourceMethod
	javaLRStaticCallAmbiguous
)

func javaLRStaticImports(
	owner javaClass,
	index *javaEvidenceIndex,
) javaLRStaticImportSet {
	if index != nil {
		scope := index.importScope(owner)
		if scope.lrStaticReady {
			return scope.lrStaticImports
		}
		scope.lrStaticReady = true
		scope.lrStaticImports.methods = make(map[string]bool)
		for methodName, typeNames := range scope.staticExact {
			for _, typeName := range typeNames {
				if typeName == "lrapi.lr" {
					scope.lrStaticImports.methods[methodName] = true
					break
				}
			}
		}
		for _, typeName := range scope.staticWildcard {
			if typeName == "lrapi.lr" {
				scope.lrStaticImports.wildcard = true
				break
			}
		}
		return scope.lrStaticImports
	}
	imports := javaLRStaticImportSet{methods: map[string]bool{}}
	for _, imported := range owner.Imports {
		imported = strings.TrimSpace(imported)
		if !strings.HasPrefix(imported, "static ") {
			continue
		}
		imported = strings.TrimSpace(strings.TrimPrefix(imported, "static "))
		switch {
		case imported == "lrapi.lr.*":
			imports.wildcard = true
		case strings.HasPrefix(imported, "lrapi.lr."):
			imports.methods[strings.TrimPrefix(imported, "lrapi.lr.")] = true
		}
	}
	return imports
}

// javaLRStaticCallBinding follows Java's unqualified-method lookup tiers far
// enough to decide whether a statically imported lrapi.lr method is the actual
// target. Member methods are considered before static imports; exact static
// imports are considered before on-demand imports. When archived source and the
// compact type model cannot prove a unique tier, callers fail closed rather
// than inventing a LoadRunner transaction and suppressing iteration metrics.
func javaLRStaticCallBinding(
	owner javaClass,
	_ javaMethod,
	index *javaEvidenceIndex,
	methodName string,
	args []string,
	variableTypes map[string]string,
) javaLRStaticCallBindingKind {
	imports := javaLRStaticImports(owner, index)
	if (!imports.wildcard && !imports.methods[methodName]) ||
		javaReturnedReceiverKeyword(methodName) {
		return javaLRStaticCallNone
	}
	if index == nil {
		if javaLRMethodShadowed(owner, methodName, nil) {
			return javaLRStaticCallSourceMethod
		}
		return javaLRStaticCallLoadRunner
	}

	variableTypes = javaEffectiveEvidenceVariableTypes(index, owner, variableTypes)
	memberCandidates := javaEvidenceMethodsInHierarchy(
		index,
		owner,
		owner,
		methodName,
		true,
	)
	memberMethods := make([]javaMethod, 0, len(memberCandidates))
	for _, candidate := range memberCandidates {
		memberMethods = append(memberMethods, candidate.method)
	}
	if applicable, uncertain := javaMethodSetPotentiallyApplicable(
		memberMethods,
		args,
		variableTypes,
	); applicable {
		return javaLRStaticCallSourceMethod
	} else if uncertain {
		return javaLRStaticCallAmbiguous
	}

	scope := index.importScope(owner)
	exactOwners := scope.staticExact[methodName]
	lrExact := javaStringSliceContains(exactOwners, "lrapi.lr")
	exactApplicable, exactUncertain := javaImportedStaticCompetitorApplicability(
		index,
		owner,
		methodName,
		exactOwners,
		args,
		variableTypes,
	)
	if lrExact {
		if exactApplicable || exactUncertain {
			return javaLRStaticCallAmbiguous
		}
		return javaLRStaticCallLoadRunner
	}
	if exactApplicable {
		// An applicable single-static import is selected before on-demand
		// imports, including an lrapi.lr wildcard.
		return javaLRStaticCallSourceMethod
	}
	if exactUncertain {
		return javaLRStaticCallAmbiguous
	}

	lrWildcard := javaStringSliceContains(scope.staticWildcard, "lrapi.lr")
	if !lrWildcard {
		return javaLRStaticCallNone
	}
	wildcardApplicable, wildcardUncertain := javaImportedStaticCompetitorApplicability(
		index,
		owner,
		methodName,
		scope.staticWildcard,
		args,
		variableTypes,
	)
	if wildcardApplicable || wildcardUncertain {
		return javaLRStaticCallAmbiguous
	}
	return javaLRStaticCallLoadRunner
}

func javaImportedStaticCompetitorApplicability(
	index *javaEvidenceIndex,
	owner javaClass,
	methodName string,
	typeNames []string,
	args []string,
	variableTypes map[string]string,
) (applicable bool, uncertain bool) {
	for _, typeName := range typeNames {
		if typeName == "lrapi.lr" {
			continue
		}
		class, ok := index.classForType(owner, typeName)
		if !ok {
			// Stage 1 permits operator-provisioned dependencies that are not in
			// the archive. Their static members cannot be disambiguated from an
			// lrapi.lr import without bytecode evidence.
			uncertain = true
			continue
		}
		if len(class.Unparsed) > 0 {
			uncertain = true
			continue
		}
		candidates := javaEvidenceMethodsInHierarchy(
			index,
			class,
			owner,
			methodName,
			false,
		)
		methods := make([]javaMethod, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.method.Static {
				methods = append(methods, candidate.method)
			}
		}
		candidateApplicable, candidateUncertain := javaMethodSetPotentiallyApplicable(
			methods,
			args,
			variableTypes,
		)
		applicable = applicable || candidateApplicable
		uncertain = uncertain || candidateUncertain
	}
	return applicable, uncertain
}

func javaMethodSetPotentiallyApplicable(
	methods []javaMethod,
	args []string,
	variableTypes map[string]string,
) (applicable bool, uncertain bool) {
	candidates := make([]javaMethod, 0, len(methods))
	for _, method := range methods {
		if javaMethodAcceptsArgumentCount(method, len(args)) {
			candidates = append(candidates, method)
		}
	}
	if len(candidates) == 0 {
		return false, false
	}
	argTypes := make([]string, len(args))
	for index, arg := range args {
		argTypes[index] = javaStaticArgumentType(arg, variableTypes)
		if argTypes[index] == "" {
			return false, true
		}
	}
	fixed := make([]javaMethod, 0, len(candidates))
	varargs := make([]javaMethod, 0, len(candidates))
	for _, method := range candidates {
		if method.Varargs {
			varargs = append(varargs, method)
			if len(args) != method.ParameterCount {
				continue
			}
		}
		fixed = append(fixed, method)
	}
	if best, unresolved := javaBestTypedOverloads(fixed, argTypes, false); unresolved {
		return false, true
	} else if len(best) > 0 {
		return true, false
	}
	if best, unresolved := javaBestTypedOverloads(varargs, argTypes, true); unresolved {
		return false, true
	} else if len(best) > 0 {
		return true, false
	}
	return false, false
}

func javaStringSliceContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func javaLRAmbiguousStaticCalls(
	source string,
	owner javaClass,
	current javaMethod,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []string {
	imports := javaLRStaticImports(owner, index)
	if !imports.wildcard && len(imports.methods) == 0 {
		return nil
	}
	code := javaCodeOnly(source)
	ambiguous := make([]string, 0)
	seen := make(map[string]bool)
	for _, match := range javaUnqualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		methodName := strings.TrimSpace(code[match[2]:match[3]])
		if !javaUnqualifiedInvocationCandidate(code, match[0], methodName) ||
			(!imports.wildcard && !imports.methods[methodName]) {
			continue
		}
		args, ok := javaCallArguments(source, code, match[3])
		if !ok ||
			javaLRStaticCallBinding(
				owner,
				current,
				index,
				methodName,
				args,
				variableTypes,
			) != javaLRStaticCallAmbiguous ||
			seen[methodName] {
			continue
		}
		seen[methodName] = true
		ambiguous = append(ambiguous, methodName)
	}
	sort.Strings(ambiguous)
	return ambiguous
}

func javaLRReceiverShadowed(
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) bool {
	if javaSourceDeclaresVariableNamed(javaCodeOnly(source), "lr") {
		return true
	}
	if index == nil {
		return variableTypes["lr"] != ""
	}
	return javaEffectiveEvidenceVariableTypes(index, owner, variableTypes)["lr"] != ""
}

func javaLRPackageNameShadowed(
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) bool {
	if javaSourceDeclaresVariableNamed(javaCodeOnly(source), "lrapi") {
		return true
	}
	if index == nil {
		return variableTypes["lrapi"] != ""
	}
	// A legal (if unconventional) archived type named `lrapi` wins the
	// syntactic-name lookup before a package qualifier. Its static `lr` member
	// must not be treated as the governed lrapi.lr shim.
	if _, resolved := index.classForType(owner, "lrapi"); resolved {
		return true
	}
	return javaEffectiveEvidenceVariableTypes(index, owner, variableTypes)["lrapi"] != ""
}

func javaLRMethodShadowed(owner javaClass, methodName string, index *javaEvidenceIndex) bool {
	classes := []javaClass{owner}
	if index != nil {
		classes = index.classHierarchy(owner)
	}
	for _, class := range classes {
		if len(javaMethodsNamed(class, methodName)) > 0 {
			return true
		}
	}
	return false
}

func javaPreviousNonspaceByte(code string, offset int) byte {
	for offset--; offset >= 0; offset-- {
		switch code[offset] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return code[offset]
		}
	}
	return 0
}

func javaPreviousJavaWord(code string, offset int) string {
	for offset > 0 {
		switch code[offset-1] {
		case ' ', '\t', '\r', '\n':
			offset--
		default:
			goto word
		}
	}
word:
	end := offset
	for offset > 0 && isJavaIdentifierByte(code[offset-1]) {
		offset--
	}
	return code[offset:end]
}

// javaUnqualifiedInvocationCandidate excludes Java syntax that shares the
// lexical shape "identifier(" but cannot be an unqualified method invocation.
// In particular, wildcard static imports must not turn control statements,
// constructor chaining, object construction, or annotations into synthetic
// lrapi.lr calls.
func javaUnqualifiedInvocationCandidate(code string, start int, name string) bool {
	if start < 0 || start > len(code) || javaReturnedReceiverKeyword(name) {
		return false
	}
	switch javaPreviousNonspaceByte(code, start) {
	case '.', '@':
		return false
	}
	return javaPreviousJavaWord(code, start) != "new"
}

func javaLRCallPartsAll(raw string) []javaLRCallPart {
	code := javaCodeOnly(raw)
	matches := javaLRCallStartRe.FindAllStringSubmatchIndex(code, -1)
	parts := make([]javaLRCallPart, 0, len(matches))
	for _, match := range matches {
		if len(match) != 4 || match[2] < 0 || match[3] > len(raw) ||
			match[1] <= 0 || match[1] > len(code) {
			continue
		}
		open := match[1] - 1
		if code[open] != '(' {
			continue
		}
		close := matchingJavaParen(code, open)
		if close < 0 || close > len(raw) {
			continue
		}
		parts = append(parts, javaLRCallPart{
			methodName: raw[match[2]:match[3]],
			arguments:  raw[open+1 : close],
		})
	}
	return parts
}

// javaLRCallParts matches against the offset-preserving lexical projection and
// slices arguments from the original source. This keeps real string arguments
// available while preventing comments or diagnostic strings containing text
// such as "lr.start_transaction(...)" from becoming executable transaction
// evidence.
func javaLRCallParts(raw string) (methodName string, arguments string, ok bool) {
	parts := javaLRCallPartsAll(raw)
	if len(parts) == 0 {
		return "", "", false
	}
	return parts[0].methodName, parts[0].arguments, true
}

// routeJavaLRFunction classifies one lr.* call through the shared registry and
// applies stateful transaction/think-time parity. Unknown lr methods are
// preserved for review (never custom_call).
func routeJavaLRFunction(result *ConvertResult, state *javaImportState, phase string, statement javaStatement, fn ParsedFunction) {
	if _, ok := lookupSourceFunction(fn.Name); !ok {
		appendJavaLRReview(result, state, statement, phase, fn)
		return
	}
	state.recognizedLRActions++

	// Stateful parity with the C converter loop (converter.go): feed the
	// transaction counters so Java transactions behave like C transactions
	// rather than being inert markers.
	switch fn.Name {
	case "lr_start_transaction":
		recordSourceTransactionName(result, convertutil.FirstNonEmpty(sourceControlArgAt(fn, 0), fn.StepName))
		state.currentTransaction = fn.StepName
		state.currentSubTransaction = ""
		result.Stats.Transactions++
		result.Stats.TransactionMarkers++
	case "lr_end_transaction":
		result.Stats.TransactionMarkers++
		state.currentTransaction = ""
		state.currentSubTransaction = ""
	case "lr_start_sub_transaction":
		state.currentSubTransaction = fn.StepName
		result.Stats.TransactionMarkers++
	case "lr_end_sub_transaction", "lr_end_sub_trans":
		result.Stats.TransactionMarkers++
		state.currentSubTransaction = ""
	}

	action, mode, _ := convertSourceFunctionAction(fn)
	notes := sourceSupportNotes(fn, mode)
	if nativeSourceTransactionOwnershipUnsupported(fn.Name) {
		mode = SourceFunctionSupportReviewOnly
		notes = "Transaction instance/cross-VUser ownership cannot be reproduced by the native source runtime; preserved for review and not executed."
		gateUnsupportedSourceTransactionOwnership(&action, fn, notes)
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%s:%d %s requires review: %s",
			statement.File,
			statement.StartLine,
			fn.Name,
			notes,
		))
	}
	if fn.Name == "lr_think_time" {
		seconds, parsed := ExtractThinkTimeSeconds(fn)
		waitMs := seconds * 1000
		if parsed && seconds >= 0 && !math.IsNaN(waitMs) && !math.IsInf(waitMs, 0) &&
			waitMs <= float64(dsl.MaxConfiguredProtocolTimeoutMS) {
			// Keep the wait at the exact lr.think_time call position. Leaving
			// multiplier/limit/ignore unset intentionally inherits the global
			// LoadRunner runtime policy during load execution and replay.
			action.ThinkTimeAfter = true
			action.ThinkTime = dsl.ThinkTimeConfig{
				Type:   "fixed",
				MeanMs: waitMs,
			}
			result.Stats.ThinkTimes++
			notes = "Preserved as an exact action-local fixed wait at the original Java source position."
		} else {
			// Dynamic, malformed, negative, non-finite, and oversized values
			// cannot be represented safely in the static DSL. Keep the source
			// marker at zero delay and make the loss of fidelity review-blocking.
			mode = SourceFunctionSupportReviewOnly
			action.Params["support_mode"] = string(mode)
			action.Metadata["support_mode"] = string(mode)
			state.configReview++
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%s:%d lr.think_time could not be represented as a bounded literal wait; using zero delay and requiring review",
				statement.File,
				statement.StartLine,
			))
			notes = "Dynamic, malformed, negative, non-finite, or oversized Java think time; zero-delay fallback requires review."
		}
	}
	applyJavaTransactionMetadata(&action, state)
	appendJavaLRAction(result, statement, phase, fn, action, mode, notes)
}

// applyJavaTransactionMetadata stamps the open source transaction onto an action
// so the dashboard can group Java actions like the C importer groups requests.
func applyJavaTransactionMetadata(action *dsl.Action, state *javaImportState) {
	if state.currentTransaction == "" {
		return
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata["source_transaction"] = state.currentTransaction
	if state.currentSubTransaction != "" {
		action.Metadata["source_subtransaction"] = state.currentSubTransaction
	}
}

// appendJavaLRAction records the action plus a SourceReview mapping keyed by the
// real lr_ function name (not the generic "java_statement") so per-method
// coverage is visible and the completeness test can assert each method's mode.
func appendJavaLRAction(result *ConvertResult, statement javaStatement, phase string, fn ParsedFunction, action dsl.Action, mode SourceFunctionSupportMode, notes string) {
	index := actionCountForPhase(result.Script, phase)
	targetPath := sourceReviewActionPath(phase, index)
	targetName := action.Name
	targetKind := "action"
	if fn.Name == "lr_think_time" && action.ThinkTimeAfter {
		targetPath += ".think_time"
		targetName = "Think Time"
		targetKind = "config"
	}
	appendSourceReviewMapping(result, SourceReviewMapping{
		SourceFile:  statement.File,
		StartLine:   statement.StartLine,
		EndLine:     statement.EndLine,
		Function:    fn.Name,
		TargetPath:  targetPath,
		TargetName:  targetName,
		TargetKind:  targetKind,
		Status:      statusForSupportMode(mode),
		SupportMode: string(mode),
		Notes:       notes,
	})
	appendActionForPhase(result.Script, phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, mode)
}

// appendJavaLRReview preserves an unrecognized lr.* method for manual review
// (keyed by its name) rather than emitting a custom_call stub.
func appendJavaLRReview(result *ConvertResult, state *javaImportState, statement javaStatement, phase string, fn ParsedFunction) {
	action := javaSourceReviewAction(statement, phase, "unrecognized_lr_method")
	index := actionCountForPhase(result.Script, phase)
	appendSourceReviewMapping(result, SourceReviewMapping{
		SourceFile:  statement.File,
		StartLine:   statement.StartLine,
		EndLine:     statement.EndLine,
		Function:    fn.Name,
		TargetPath:  sourceReviewActionPath(phase, index),
		TargetName:  action.Name,
		TargetKind:  "action",
		Status:      "review_only",
		SupportMode: string(SourceFunctionSupportReviewOnly),
		Notes:       "Unrecognized lr.* method preserved for manual review.",
	})
	appendActionForPhase(result.Script, phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
	if phase == "action" {
		state.unrecognizedAction++
	}
}

func javaLRFunctionName(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func javaLRFunctionAllowedInExecutableLane(name string) bool {
	def, ok := lookupSourceFunction(name)
	if !ok {
		return false
	}
	if nativeSourceTransactionOwnershipUnsupported(name) {
		return false
	}
	switch def.Family {
	case "web", "database":
		return false
	default:
		return true
	}
}

// javaArgToSourceArg renders one Java lr argument as a source-arg string: string
// literals are unquoted + parameter-converted, a nested lr.eval_string("{x}") is
// unwrapped to {{var.x}}, and everything else (variables, enum constants like
// lr.AUTO) passes through trimmed.
func javaArgToSourceArg(raw string) string {
	raw = strings.TrimSpace(raw)
	if value, ok := parseJavaStringLiteral(raw); ok {
		return convertParamReferences(value)
	}
	if inner, ok := unwrapJavaEvalString(raw); ok {
		return inner
	}
	return raw
}

func unwrapJavaEvalString(raw string) (string, bool) {
	methodName, rawArgumentText, ok := javaLRCallParts(strings.TrimSpace(raw))
	if !ok || javaLRFunctionName(methodName) != "eval_string" {
		return "", false
	}
	inner := splitJavaArgs(rawArgumentText)
	if len(inner) == 0 {
		return "", false
	}
	if value, ok := parseJavaStringLiteral(inner[0]); ok {
		return convertParamReferences(value), true
	}
	return "", false
}

// javaLRStepName returns the first string-literal argument (the transaction/step
// name), skipping enum constants like lr.AUTO so they never become a step name.
func javaLRStepName(rawArgs []string) string {
	for _, raw := range rawArgs {
		if value, ok := parseJavaStringLiteral(raw); ok {
			return value
		}
	}
	return ""
}

func convertJavaJMSAction(state *javaImportState, files map[string]string, statement javaStatement) (dsl.Action, bool) {
	text := strings.TrimSpace(statement.Text)
	if javaCommitCallRe.MatchString(text) {
		return javaJMSControlAction(statement, "jms_commit"), true
	}
	if javaRollbackCallRe.MatchString(text) {
		return javaJMSControlAction(statement, "jms_rollback"), true
	}
	if javaAckCallRe.MatchString(text) {
		return javaJMSControlAction(statement, "jms_ack"), true
	}
	producerName := ""
	if matches := javaSendOnProducerRe.FindStringSubmatch(text); len(matches) == 3 {
		producerName = strings.TrimSpace(matches[1])
	}
	if matches := javaSendCallRe.FindStringSubmatch(text); len(matches) == 2 {
		args := splitJavaArgs(matches[1])
		target := ""
		msgName := ""
		if len(args) == 1 {
			msgName = strings.TrimSpace(args[0])
			target = state.env.Producers[producerName]
		} else if len(args) >= 2 {
			target = state.resolveDestination(args[0], files)
			msgName = strings.TrimSpace(args[len(args)-1])
		}
		msg := state.env.Messages[msgName]
		action := javaBaseJMSAction(statement, "jms_publish")
		action.Target = convertutil.FirstNonEmpty(target, "{{env.JMS_DESTINATION}}")
		action.Body = msg.Body
		action.Headers = cloneStringMap(msg.Properties)
		if len(action.Headers) == 0 {
			action.Headers = nil
		}
		if msg.BodyUnresolved {
			// The source supplied a message body that could not be resolved; mark
			// it so the mapper downgrades to review rather than shipping an empty
			// broker message as executable (consistent with the unresolved JNDI /
			// message-property handling).
			action.Metadata["body_unresolved"] = true
		}
		return action, true
	}
	if matches := javaReceiveCallRe.FindStringSubmatch(text); len(matches) == 2 {
		action := javaBaseJMSAction(statement, "jms_consume")
		action.Target = "{{env.JMS_DESTINATION}}"
		if timeout, ok := javaReceiveTimeout(matches[1]); ok {
			action.Params = map[string]any{"timeout_ms": timeout}
		}
		return action, true
	}
	return dsl.Action{}, false
}

func (state *javaImportState) resolveDestination(expr string, files map[string]string) string {
	expr = strings.TrimSpace(expr)
	if value, ok := state.env.Destinations[expr]; ok {
		return value
	}
	if value, ok := state.env.eval(expr, files); ok {
		return value
	}
	return ""
}

func javaReceiveTimeout(args string) (int, bool) {
	args = strings.TrimSpace(args)
	if args == "" {
		return 0, false
	}
	if value, err := strconv.Atoi(args); err == nil {
		return value, true
	}
	return 0, false
}

func javaJMSControlAction(statement javaStatement, actionType string) dsl.Action {
	return javaBaseJMSAction(statement, actionType)
}

func javaBaseJMSAction(statement javaStatement, actionType string) dsl.Action {
	return dsl.Action{
		Name: sanitizeName(convertutil.FirstNonEmpty(statementName(statement), actionType)),
		Type: actionType,
		Metadata: map[string]any{
			"source":      "java_jms_idiom",
			"source_file": statement.File,
			"source_line": statement.StartLine,
		},
	}
}

func statementName(statement javaStatement) string {
	text := strings.TrimSpace(statement.Text)
	if len(text) > 60 {
		text = text[:60]
	}
	return text
}

func appendJavaAction(result *ConvertResult, statement javaStatement, phase string, action dsl.Action, mode SourceFunctionSupportMode, notes string) {
	index := actionCountForPhase(result.Script, phase)
	appendJavaMapping(result, statement, sourceReviewActionPath(phase, index), action.Name, "action", statusForSupportMode(mode), notes, mode)
	appendActionForPhase(result.Script, phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, mode)
}

func appendJavaReview(result *ConvertResult, state *javaImportState, statement javaStatement, phase string, reason string, actionPhase bool) {
	action := javaSourceReviewAction(statement, phase, reason)
	appendJavaAction(result, statement, phase, action, SourceFunctionSupportReviewOnly, "Java statement was preserved for manual review.")
	if actionPhase {
		state.unrecognizedAction++
	}
}

func javaSourceReviewAction(statement javaStatement, phase string, reason string) dsl.Action {
	return dsl.Action{
		Name: sanitizeName(fmt.Sprintf("Java_review_%d", statement.StartLine)),
		Type: "source_review",
		Params: map[string]any{
			"function": "java_statement",
			"args":     []any{statement.Text},
		},
		Metadata: map[string]any{
			"source":        "java_vuser",
			"review_reason": reason,
			"source_file":   statement.File,
			"source_line":   statement.StartLine,
			"phase":         phase,
		},
	}
}

func appendJavaMapping(result *ConvertResult, statement javaStatement, targetPath string, targetName string, targetKind string, status string, notes string, mode SourceFunctionSupportMode) {
	appendSourceReviewMapping(result, SourceReviewMapping{
		SourceFile:  statement.File,
		StartLine:   statement.StartLine,
		EndLine:     statement.EndLine,
		Function:    "java_statement",
		TargetPath:  targetPath,
		TargetName:  targetName,
		TargetKind:  targetKind,
		Status:      status,
		SupportMode: string(mode),
		Notes:       notes,
	})
}
