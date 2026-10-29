package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The property-level half of the lifecycle parity matrix (AUTH-C). Every
// AUTH-2 row proves each of the nine AUTH-2 properties with named tests, or
// names the row whose canonical owner proves it, or records the gap; and
// every route that can add a library script is inventoried by walking the
// package's call graph up from the calls that insert one.

// lifecycleProperty is one property every AUTH-2 row must prove.
type lifecycleProperty string

const (
	lifecycleOwnership  lifecycleProperty = "Ownership and destination"
	lifecycleProvenance lifecycleProperty = "Warnings and provenance"
	lifecycleSecrets    lifecycleProperty = "Secret references"
	lifecycleGates      lifecycleProperty = "Capability and policy gates"
	lifecyclePending    lifecycleProperty = "Pending edits"
	lifecycleNavigation lifecycleProperty = "Dirty navigation"
	lifecycleDelayed    lifecycleProperty = "Delayed replies"
	lifecyclePayload    lifecycleProperty = "Exact resolved payload"
	lifecycleReopen     lifecycleProperty = "Durable reopen"
)

var lifecycleProperties = []lifecycleProperty{
	lifecycleOwnership, lifecycleProvenance, lifecycleSecrets, lifecycleGates, lifecyclePending,
	lifecycleNavigation, lifecycleDelayed, lifecyclePayload, lifecycleReopen,
}

// lifecycleProof is one test that proves a property of a row, the row whose
// canonical owner proves it, or a recorded gap.
type lifecycleProof struct {
	// Kind is "go" (a pkg/api test), "vitest" (a test under dashboard/src),
	// "e2e" (a Playwright test under dashboard/e2e), "shared" or "open".
	Kind string
	File string
	// Title is the Go test function, the Vitest/Playwright test title, the
	// row a shared cell defers to, or an open cell's reason.
	Title string
	// Why says why a shared cell's row owns the property.
	Why string
}

func goProof(name string) lifecycleProof { return lifecycleProof{Kind: "go", Title: name} }
func vitestProof(file, title string) lifecycleProof {
	return lifecycleProof{Kind: "vitest", File: file, Title: title}
}
func e2eProof(file, title string) lifecycleProof {
	return lifecycleProof{Kind: "e2e", File: file, Title: title}
}
func sharedWith(row, why string) lifecycleProof {
	return lifecycleProof{Kind: "shared", Title: row, Why: why}
}
func openGap(reason string) lifecycleProof { return lifecycleProof{Kind: "open", Title: reason} }

const (
	lifecycleEditorRow   = "Direct JSON and structured editor"
	lifecycleEditorTests = "components/ScriptEditor.test.tsx"
)

// lifecycleAuth2Properties maps each AUTH-2 row to the proof of every
// property.
func lifecycleAuth2Properties() map[string]map[lifecycleProperty][]lifecycleProof {
	const (
		scripts    = "components/ScriptsPage.test.tsx"
		assist     = "components/scriptAuthoringAssist.test.ts"
		sample     = "components/SampleScriptImport.test.tsx"
		protocol   = "components/ProtocolAssetImport.test.tsx"
		review     = "components/ScriptImportReview.test.tsx"
		archive    = "components/EnterpriseArchiveImport.test.tsx"
		recorder   = "components/BrowserRecorderControlPanel.test.tsx"
		hub        = "components/TestHub.test.tsx"
		newTest    = "components/NewTestPage.test.tsx"
		modal      = "components/CreateTestModal.test.tsx"
		queued     = "components/CommandCenterQueuedRevision.test.tsx"
		template   = "workflow-lifecycle-template.spec.ts"
		templateT  = "a starter template opens an independent draft that saves, reopens and leaves the template unchanged"
		handoff    = "workflow-archive-approval-handoff.spec.ts"
		handoffT   = "Legacy archive editor returns a durable approval receipt"
		newTestE2E = "workflow-new-test.spec.ts"
		editorE2E  = "workflow-script-editor.spec.ts"
		scenario   = "workflow-scenario-composition.spec.ts"
	)
	editor := lifecycleEditorTests
	inEditor := "its drafts open in the shared script editor, which owns this property"
	return map[string]map[lifecycleProperty][]lifecycleProof{
		lifecycleEditorRow: {
			lifecycleOwnership: {
				vitestProof(editor, "carries the editor folder onto the authoring session"),
				vitestProof(editor, "does not let an older save response overwrite a newly opened project document"),
				goProof("TestScriptAuthoringAPIConditionedSaveAndStalePlacement"),
			},
			lifecycleProvenance: {
				vitestProof(editor, "keeps created-name provenance within the actual editor resource"),
				vitestProof(editor, "refreshes imported runtime parity metadata from validation responses"),
			},
			lifecycleSecrets: {vitestProof(editor, "loads stored secret status, opens script bindings, and refreshes it after save")},
			lifecycleGates: {
				vitestProof(editor, "queues a new runner-backed Java Vuser script for approval instead of creating it"),
				vitestProof(editor, "clears a readiness result whose controller context changed and says why (AUTH-B)"),
				vitestProof(editor, "treats a newer stored revision as making the result stale (AUTH-B)"),
			},
			lifecyclePending:    {vitestProof(editor, "retains a focused cell typed during save through canonical parent echo and the next save")},
			lifecycleNavigation: {vitestProof(editor, "guards dirty editor close and clears the guard after a successful save")},
			lifecycleDelayed:    {vitestProof(editor, "ignores a validation response when the editable draft changes in flight")},
			lifecyclePayload:    {vitestProof(editor, "validates the resolved draft with unblurred data cells and binds the result to it")},
			lifecycleReopen: {
				e2eProof(editorE2E, "authored script goals survive an unrelated editor save and canonical reopen"),
				e2eProof(editorE2E, "two editors preserve stale drafts and require explicit revision review before reapply"),
			},
		},
		"Template/component/clone": {
			lifecycleOwnership: {
				vitestProof(scripts, "models a new draft from an existing saved script without editing the original"),
				e2eProof(template, templateT),
				goProof("TestLibrarySyncLinkCreatesIndependentAssetsAcrossDestinationProjects"),
			},
			lifecycleProvenance: {
				goProof("TestCloneDraftKeepsSourceImportProvenance"),
				vitestProof(assist, "keeps import provenance on the edited script when a component is inserted"),
				goProof("TestLibraryImportKeepsScriptImportProvenance"),
			},
			lifecycleSecrets: {
				vitestProof(scripts, "optionally migrates recoverable source credentials while always applying safe references"),
				goProof("TestCloneDraftSourceMigrationIsPolicyGatedAndSingleUse"),
				goProof("TestScriptSecretReferenceLifecycleKeepsNamesAuthoritative"),
			},
			lifecycleGates: {
				goProof("TestScriptBuilderRoutesRetainStaticDomainFeatures"),
				vitestProof(scripts, "keeps policy retry visible when initial authoring capabilities fail"),
			},
			lifecyclePending:    {sharedWith(lifecycleEditorRow, inEditor)},
			lifecycleNavigation: {sharedWith(lifecycleEditorRow, inEditor)},
			lifecycleDelayed:    {vitestProof(scripts, "ignores a late Model Existing response after authoring navigation")},
			lifecyclePayload: {
				e2eProof(template, templateT),
				vitestProof(assist, "inserts reusable authoring components into the editable script"),
			},
			lifecycleReopen: {e2eProof(template, templateT)},
		},
		"Every import launcher": {
			lifecycleOwnership: {
				vitestProof(sample, "passes the active project id into sample conversion previews"),
				vitestProof(protocol, "requires project scope before HAR inspection and explains the disabled action"),
				e2eProof("workflow-import-source.spec.ts", "JMeter review uses the shared six tabs and retains project scope during upload"),
			},
			lifecycleProvenance: {
				vitestProof(protocol, "renders import diagnostics returned by protocol asset converters"),
				vitestProof(review, "keeps conversion notes informational and blockers distinct from unsupported features"),
				e2eProof("workflow-jmeter-fidelity.spec.ts", "JMeter fidelity review retains blockers and regenerates explicit timestamp and name repairs"),
			},
			lifecycleSecrets: {
				vitestProof(sample, "stores sample authentication options as governed secret references"),
				vitestProof(review, "never places imported credential values in the generated preview DOM"),
			},
			lifecycleGates: {goProof("TestDisabledImportFeatureBlocksGlobalAdminPathAfterRBAC")},
			lifecyclePending: {
				vitestProof(review, "preserves edited credential names and write-only values after applying parameterization"),
				vitestProof(protocol, "invalidates the protected OpenAPI candidate after an arbitrary runtime edit"),
			},
			lifecycleNavigation: {vitestProof(protocol, "asks before leaving an unsaved import review and lets a saved one go")},
			lifecycleDelayed:    {vitestProof(sample, "keeps a newer conversion when an older credential application resolves late")},
			lifecyclePayload:    {vitestProof(protocol, "keeps the OpenAPI protected candidate while forwarding credential and parameterization overrides")},
			lifecycleReopen:     {goProof("TestProtectedImportReplaceRetainsRevisionAndDeleteCleansHTTPSource")},
		},
		"Multi-script imports": {
			lifecycleOwnership: {
				vitestProof(archive, "does not attach a stale upload result after switching projects"),
				goProof("TestHandleSaveEnterpriseArchiveImportCommitsBatchToSelectedFolder"),
			},
			lifecycleProvenance: {vitestProof(archive, "saves uploaded JAR provenance in the authoring draft and changes save identity after removal")},
			lifecycleSecrets: {
				vitestProof(archive, "prepares credentialed generated lanes before one atomic enterprise batch save"),
				goProof("TestHandleSaveEnterpriseArchiveImportRestartRecoversCommittedCredentialedBatch"),
			},
			lifecycleGates: {
				vitestProof(archive, "keeps a fidelity-blocked combined Java Vuser draft in review instead of offering approval"),
				e2eProof(handoff, handoffT),
			},
			lifecyclePending: {
				vitestProof(archive, "saves an inline credential edit from validation without reconverting the source archive"),
				e2eProof(handoff, "Legacy archive editor retains later edits while queueing approval"),
			},
			lifecycleNavigation: {e2eProof(handoff, handoffT)},
			lifecycleDelayed:    {vitestProof(archive, "does not publish a late credential result after the selected generated script changes")},
			lifecyclePayload: {
				vitestProof(archive, "saves the credential-bound combined Java draft instead of the stale redacted review"),
				goProof("TestHandleSaveEnterpriseArchiveImportReplaysExactCommittedBatch"),
			},
			lifecycleReopen: {
				goProof("TestHandleSaveEnterpriseArchiveImportRestartRecoversCommittedCredentialedBatch"),
				e2eProof(handoff, handoffT),
			},
		},
		"Recording": {
			lifecycleOwnership: {
				goProof("TestHandleConvertBrowserRecordingPersistsEveryScriptInSelectedFolder"),
				goProof("TestBridgedBrowserImportUsesResolverProjectWhenWrapperOmitsProject"),
			},
			lifecycleProvenance: {
				goProof("TestHandleConvertBrowserRecordingStampsProvenance"),
				vitestProof(recorder, "shows live agent-reported environment and retains browser and HTTP receipts in scoped output reviews"),
			},
			lifecycleSecrets: {
				vitestProof(recorder, "prompts for optional credentials and saves a configure-later reference"),
				goProof("TestHandleConvertBrowserRecordingAtomicallyAppliesExplicitSecretBinding"),
			},
			lifecycleGates: {
				goProof("TestBrowserRecorderAttestationUsesRecordingWorkflowFeature"),
				goProof("TestBridgedBrowserImportRejectsNonBrowserProtocol"),
				vitestProof(recorder, "live-disables credential and parameterization controls when a preserved browser draft expires"),
			},
			lifecyclePending: {vitestProof(recorder, "keeps an operator rename through a late refresh that changes the recording environment, and saves it")},
			lifecycleNavigation: {
				vitestProof(recorder, "asks before leaving an unsaved recording review and lets a saved one go"),
				vitestProof(recorder, "asks before dismissing an unsaved recording review and dismisses a saved one directly"),
			},
			lifecycleDelayed: {vitestProof(recorder, "ignores a late response from the previously selected recording session")},
			lifecyclePayload: {
				vitestProof(recorder, "applies preview parameterization to the browser script that is saved"),
				goProof("TestHandleConvertBrowserRecordingAppliesReviewedParameterizationAtomically"),
			},
			lifecycleReopen: {goProof("TestHandleConvertBrowserRecordingReplaysCompletedReceiptAfterRestart")},
		},
		"Classic replay/debug/browser replay": {
			lifecycleOwnership: {
				vitestProof(editor, "does not let an older replay start response attach to a newly opened project document"),
				vitestProof(editor, "keeps tracking an acknowledged stop until the final WebSocket event arrives"),
			},
			lifecycleProvenance: {
				vitestProof(editor, "renders replay validation warnings returned on failed browser replay start"),
				goProof("TestLifecycleParityStaticFindingsMatchAcrossEntryPoints"),
			},
			lifecycleSecrets: {
				vitestProof(editor, "keeps Save available but fails replay closed while required-secret status is unknown or unavailable"),
				goProof("TestTransientReplayRefusesAMaskedRecordedResourceURL"),
			},
			lifecycleGates:      {goProof("TestBrowserCompatibilityMatrixParityAcrossValidationAndReplayEndpoints")},
			lifecyclePending:    {vitestProof(editor, "requires a new replay after actions change even when the edited draft revalidates")},
			lifecycleNavigation: {sharedWith(lifecycleEditorRow, "replay and debug run inside the shared script editor, whose guard covers them")},
			lifecycleDelayed: {
				vitestProof(editor, "reconciles terminal evidence by trusted GET after an early unbound WebSocket and stale running start response"),
				e2eProof("workflow-script-session.spec.ts", "a validation that returns after an edit is ignored and the next one binds to the edited draft"),
			},
			lifecyclePayload: {vitestProof(editor, "debugs the resolved draft with an unblurred data cell rather than an earlier copy")},
			lifecycleReopen:  {e2eProof("workflow-debugger-experience.spec.ts", "debugger captures successful evidence, diagnoses runtime failure, repairs a draft and retains protocol-correct history")},
		},
		"Scenario library wizard and compact modal": {
			lifecycleOwnership:  {vitestProof(hub, "resets a pristine scenario edit before authoring in a different project")},
			lifecycleProvenance: {vitestProof(hub, "applies workload templates and renders load-model validation findings")},
			lifecycleSecrets: {
				goProof("TestScenarioSecretReferencesSurviveSaveAndReload"),
				goProof("TestMaskRuntimeSettingsForEditorShowsOnlyProjectSecretReferences"),
				vitestProof("components/scenarioAssembly.test.ts", "passes a reopened scenario's secret references, spaced or not, through the checkpoint's credential check into its save payload"),
			},
			lifecycleGates: {
				vitestProof(hub, "converts a newly disabled selected shape to Custom Workload"),
				goProof("TestLibraryScenarioPreservesAndEnforcesWorkloadShapeIdentity"),
			},
			lifecyclePending: {
				vitestProof(hub, "retains a scenario script review with later edits and returns after a finished save"),
				e2eProof(scenario, "scenario prerequisite saves retain archive siblings and later editor changes"),
			},
			lifecycleNavigation: {vitestProof(hub, "keeps a saved project draft when later unsaved edits are discarded")},
			lifecycleDelayed:    {vitestProof(hub, "does not attach a late data-pool result to a replacement draft")},
			lifecyclePayload:    {vitestProof(hub, "creates a scenario from the typed form payload")},
			lifecycleReopen: {
				e2eProof(scenario, "scenario rename and reopen retain Results, WebSocket, named shape and group timing intent"),
				vitestProof(hub, "autosaves builder progress so a reload offers to resume it"),
				vitestProof(hub, "continues from the scenario as it is now when a save replays an earlier create (AUTH-1)"),
			},
		},
		"New run": {
			lifecycleOwnership:  {vitestProof(newTest, "waits for a project scope and ignores scenarios returned from the previous project")},
			lifecycleProvenance: {vitestProof(newTest, "says beside Start now why a run larger than the project cap can never start, without offering a queue")},
			lifecycleSecrets:    {goProof("TestStartTestRequiresConfiguredStaticSecretsWithoutInvalidatingSavedScript")},
			lifecycleGates:      {vitestProof(newTest, "surfaces governed schedule policies and approval actions")},
			lifecyclePending:    {vitestProof(newTest, "saves a local draft with pending group edits without creating or starting a test")},
			lifecycleNavigation: {vitestProof(newTest, "guards scenario row changes that would discard dirty launch overrides")},
			lifecycleDelayed:    {vitestProof(newTest, "does not replace a release choice when assignment suggestions arrive late")},
			lifecyclePayload:    {vitestProof(newTest, "carries the untouched error handling half into the group override payload")},
			lifecycleReopen: {
				e2eProof(newTestE2E, "New Test saves drafts without execution, retains both tabs and validates recurring triggers"),
				vitestProof(newTest, "restores a saved run draft after unmount without executing or modifying a schedule"),
			},
		},
		"Queued revision and execution overview": {
			lifecycleOwnership: {
				goProof("TestQueuedRunRevisionRejectsNonQueuedAndCrossProjectSources"),
				vitestProof(modal, "never recalls one queued run's revise key for another revised with the same settings (AUTH-1)"),
				vitestProof(modal, "replays rather than queues another when a replayed draft is created again (AUTH-1)"),
			},
			lifecycleProvenance: {
				vitestProof(modal, "preserves hierarchy provenance and the data-pool snapshot on a queued-run revision"),
				e2eProof(newTestE2E, "queued-copy preserves source capture IDs and durably clears the last authored goal"),
			},
			lifecycleSecrets: {goProof("TestRevisionSourceGroupIndexPreventsCredentialSwap")},
			lifecycleGates: {
				vitestProof(queued, "does not mount a late lazy editor after permission retirement"),
				vitestProof(modal, "blocks aggregate staged demand above the backend one-million VUser limit"),
			},
			lifecyclePending:    {vitestProof(modal, "retains an editable late draft and guard when the submitted snapshot succeeds")},
			lifecycleNavigation: {vitestProof(modal, "prompts before closing a dirty queued-run draft")},
			lifecycleDelayed: {
				vitestProof(queued, "retires planned settings across project ABA and fences late callbacks from a former modal"),
				vitestProof(modal, "clears capture IDs only on a real project change and ignores old project list completions"),
			},
			lifecyclePayload: {vitestProof(modal, "submits the complete queued-copy snapshot after an actual unrelated name edit")},
			lifecycleReopen:  {e2eProof(newTestE2E, "actual held-run serialization and an API-seeded queued copy preserve complete authored intent")},
		},
	}
}

// lifecycleGoTest is a test function declared in pkg/api.
type lifecycleGoTest struct {
	File, Body string
	// Tagged reports a file build constraint, so a default run may skip it.
	Tagged bool
	// Skips names what in the body can skip the test: t.Skip, or a test
	// helper or method that skips, directly or through another.
	Skips []string
}

var lifecycleGoSkip = regexp.MustCompile(`\.Skip(?:f|Now)?\(`)

var lifecycleGoBuildConstraint = regexp.MustCompile(`(?m)^//go:build `)

// lifecycleGoTests lists the test functions declared in pkg/api.
func lifecycleGoTests(t *testing.T) map[string]lifecycleGoTest {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]lifecycleGoTest{}
	// Test helpers and methods (by name) that can skip, and every one's calls.
	skippingHelpers := map[string]bool{}
	calls := map[string][]string{}
	fset := token.NewFileSet()
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, file, source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		tagged := lifecycleGoBuildConstraint.Match(source)
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			body := string(source[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset])
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					switch fun := call.Fun.(type) {
					case *ast.Ident:
						calls[fn.Name.Name] = append(calls[fn.Name.Name], fun.Name)
					case *ast.SelectorExpr:
						calls[fn.Name.Name] = append(calls[fn.Name.Name], fun.Sel.Name)
					}
				}
				return true
			})
			if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				if lifecycleGoSkip.MatchString(body) {
					skippingHelpers[fn.Name.Name] = true
				}
				continue
			}
			test := lifecycleGoTest{File: file, Body: body, Tagged: tagged}
			if lifecycleGoSkip.MatchString(body) {
				test.Skips = append(test.Skips, "t.Skip")
			}
			tests[fn.Name.Name] = test
		}
	}
	// A helper that calls a skipping helper can skip too.
	for changed := true; changed; {
		changed = false
		for name, callees := range calls {
			if skippingHelpers[name] {
				continue
			}
			for _, callee := range callees {
				if skippingHelpers[callee] {
					skippingHelpers[name], changed = true, true
					break
				}
			}
		}
	}
	for name, test := range tests {
		for _, callee := range calls[name] {
			if skippingHelpers[callee] {
				test.Skips = append(test.Skips, callee)
			}
		}
		tests[name] = test
	}
	return tests
}

// lifecycleGoProofProblem says why a named Go test does not prove anything
// in a default run, or "".
func lifecycleGoProofProblem(tests map[string]lifecycleGoTest, name string) string {
	test, ok := tests[name]
	switch {
	case !ok:
		return "does not exist in pkg/api"
	case test.Tagged:
		return "is in a build-tagged file (" + test.File + "), which a default run skips"
	case len(test.Skips) > 0:
		return "can skip itself (" + strings.Join(test.Skips, ", ") + ")"
	}
	return ""
}

var (
	lifecycleVitestTest     = `\b(?:it|test)\(\s*['"` + "`" + `]%s['"` + "`" + `]`
	lifecycleVitestSkipping = regexp.MustCompile(`\b(?:describe|suite|it|test|bench)(?:\.\w+)*\.(?:skip|todo|only|fails|skipIf|runIf)\b|\b\w+\.skip\(`)
)

// lifecycleVitestProblem says why a Vitest file's test with this title does
// not run, or "". A cited file has no skipped, conditional, focused, todo or
// expected-to-fail test at all, so no title can hide inside one.
func lifecycleVitestProblem(source, title string) string {
	if !regexp.MustCompile(fmt.Sprintf(lifecycleVitestTest, regexp.QuoteMeta(title))).MatchString(source) {
		return fmt.Sprintf("has no test %q", title)
	}
	if match := lifecycleVitestSkipping.FindString(source); match != "" {
		return fmt.Sprintf("skips, focuses or conditions tests (%s), so %q may not run", match, title)
	}
	return ""
}

// lifecycleE2EProofProblems says why a Playwright test (or, with no title,
// a whole spec) does not run in both e2e profiles.
func lifecycleE2EProofProblems(source, file, title string, localAuth map[string]bool, defaultIgnore []string) []string {
	var problems []string
	if title != "" && !strings.Contains(source, "test('"+title+"'") {
		problems = append(problems, fmt.Sprintf("%s has no test %q", file, title))
	}
	if !localAuth[file] {
		problems = append(problems, file+" does not run in the local-auth profile")
	}
	for _, pattern := range defaultIgnore {
		if matched, _ := filepath.Match(pattern, file); matched {
			problems = append(problems, fmt.Sprintf("%s is ignored by the default profile (%s)", file, pattern))
		}
	}
	if lifecycleProfileSkip(source, title) {
		if title == "" {
			problems = append(problems, file+" skips by profile")
		} else {
			problems = append(problems, fmt.Sprintf("%s test %q skips by profile, so it does not run in both", file, title))
		}
	}
	return problems
}

// TestLifecycleParityPropertiesAreProven holds every AUTH-2 row to a proof
// of each property: a named Go, Vitest or Playwright test that exists and
// runs (Playwright in both e2e profiles), a row whose direct proof covers it,
// or a recorded gap.
func TestLifecycleParityPropertiesAreProven(t *testing.T) {
	properties := lifecycleAuth2Properties()
	rows := map[string]bool{}
	for _, row := range lifecycleAuth2Rows() {
		rows[row.Entry] = true
		if properties[row.Entry] == nil {
			t.Errorf("AUTH-2 row %q has no property proofs", row.Entry)
		}
	}
	goTests := lifecycleGoTests(t)
	localAuth, defaultIgnore := lifecycleE2EProfiles(t)
	sources := map[string]string{}
	read := func(path string) (string, error) {
		if source, ok := sources[path]; ok {
			return source, nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		sources[path] = string(raw)
		return sources[path], nil
	}
	for entry, cells := range properties {
		if !rows[entry] {
			t.Errorf("property proofs name %q, which is not an AUTH-2 row", entry)
		}
		for property := range cells {
			if !lifecyclePropertyKnown(property) {
				t.Errorf("%s names unknown property %q", entry, property)
			}
		}
		for _, property := range lifecycleProperties {
			proofs := cells[property]
			if len(proofs) == 0 {
				t.Errorf("%s / %s has no proof", entry, property)
				continue
			}
			for _, proof := range proofs {
				where := fmt.Sprintf("%s / %s", entry, property)
				switch proof.Kind {
				case "go":
					if problem := lifecycleGoProofProblem(goTests, proof.Title); problem != "" {
						t.Errorf("%s: Go test %s %s", where, proof.Title, problem)
					}
				case "vitest":
					source, err := read(filepath.Join("../../dashboard/src", proof.File))
					if err != nil {
						t.Errorf("%s names %s: %v", where, proof.File, err)
						continue
					}
					if problem := lifecycleVitestProblem(source, proof.Title); problem != "" {
						t.Errorf("%s: %s %s", where, proof.File, problem)
					}
				case "e2e":
					source, err := read(filepath.Join("../../dashboard/e2e", proof.File))
					if err != nil {
						t.Errorf("%s names %s: %v", where, proof.File, err)
						continue
					}
					for _, problem := range lifecycleE2EProofProblems(source, proof.File, proof.Title, localAuth, defaultIgnore) {
						t.Errorf("%s: %s", where, problem)
					}
				case "shared":
					if len(proofs) != 1 || proof.Why == "" {
						t.Errorf("%s defers to %q: a shared cell is alone and says why", where, proof.Title)
					}
					for _, owner := range properties[proof.Title][property] {
						if owner.Kind == "shared" || owner.Kind == "open" {
							t.Errorf("%s defers to %q, which does not prove it directly", where, proof.Title)
						}
					}
					if len(properties[proof.Title][property]) == 0 {
						t.Errorf("%s defers to %q, which has no proof", where, proof.Title)
					}
				case "open":
					if len(proofs) != 1 || strings.TrimSpace(proof.Title) == "" {
						t.Errorf("%s: an open cell is alone and says what is missing", where)
					}
				default:
					t.Errorf("%s: unknown proof kind %q", where, proof.Kind)
				}
			}
		}
	}
}

func lifecyclePropertyKnown(property lifecycleProperty) bool {
	for _, known := range lifecycleProperties {
		if known == property {
			return true
		}
	}
	return false
}

// lifecycleRunnerCreateCores are the runner's create paths. With the store
// entry points (lifecycleStoreCreateEntries other than SaveScript, which is
// checked apart), they are where the walk starts.
var lifecycleRunnerCreateCores = []string{"createScriptDurableCompletingReceipt", "storeScriptDurable", "saveStoredScript"}

// lifecycleScriptCreatePrimitives are the calls that add a library script.
func lifecycleScriptCreatePrimitives() []string {
	primitives := append([]string(nil), lifecycleRunnerCreateCores...)
	for name := range lifecycleStoreCreateEntries {
		if name != "SaveScript" {
			primitives = append(primitives, name)
		}
	}
	sort.Strings(primitives)
	return primitives
}

// lifecycleScriptInsertSites classifies every pkg/storage function that
// inserts into the scripts table: a "create" site, walked up to the exported
// store methods that reach it, or a "migration" that only re-keys rows that
// already exist.
var lifecycleScriptInsertSites = map[string]string{
	"createScriptAuthoringIfAbsent": "create",
	"upsertBrunoImportScriptTx":     "create",
	"CommitGitHubScriptImport":      "create",
	"SaveScript":                    "create",
	"backfillOneBlankScriptID":      "migration",
}

// lifecycleStoreCreateEntries are the only exported store methods that may
// reach a create site: each is where the walk of pkg/api starts, except the
// SaveScript upsert, which pkg/api calls only through
// lifecycleSaveScriptCallers.
var lifecycleStoreCreateEntries = map[string]bool{
	"CreateScriptIfAbsent": true, "CreateScriptAuthoringIfAbsent": true, "CreateScriptAuthoringCompletingReceipt": true,
	"CommitBrunoImport": true, "CommitBrunoNativeImport": true, "CommitImportBatch": true, "CommitGitHubScriptImport": true,
	"SaveScript": true,
}

// lifecycleSaveScriptCallers are the runner functions that call
// saveStoredScriptWithImportTrust, the only pkg/api caller of the SaveScript
// upsert: the create cores (primitives) and updates of a durable script.
var lifecycleSaveScriptCallers = map[string]string{
	"TestRunner.createScriptDurableCompletingReceipt":   "primitive",
	"TestRunner.storeScriptDurable":                     "primitive",
	"TestRunner.saveStoredScript":                       "primitive",
	"TestRunner.updateScriptDurableInProjectRefreshing": "updates a durable script",
	"TestRunner.UpdateScriptDurableIfUnchanged":         "updates a durable script",
	"TestRunner.backfillScriptRefKeys":                  "rewrites loaded scripts' ref keys",
}

// lifecycleResolution lists the callers of a reached method whose name is
// declared more than once in pkg/api, so its callers cannot be found by name.
type lifecycleResolution struct {
	Callers []string
	// Holders are the identifiers a function names to hold a value of the
	// receiver type: the type and every struct field and function result of
	// it, which the graph finds and requires here. Every function that names
	// one and calls the method must be among Callers.
	Holders []string
}

var lifecycleProducerResolutions = map[string]lifecycleResolution{
	"githubSyncService.Run": {Callers: []string{"Server.handleCreateGitHubSyncJob"}, Holders: []string{"githubSyncService", "newGitHubSyncService"}},
}

// lifecycleProducerNonRoutes are reached functions no route handler calls.
var lifecycleProducerNonRoutes = map[string]string{
	"TestRunner.StoreScript":                  "seeding helper with no production caller",
	"TestRunner.storeScriptDurableForRestore": "hydrates pre-governance records; no production caller",
	"TestRunner.saveStoredScript":             "unused upsert wrapper; no production caller",
	"createImportTransaction.createScript":    "unused transaction method; no production caller",
}

// lifecycleOutsideCallers are the calls outside pkg/api and pkg/storage to a
// method named like a script create; each is classified.
var lifecycleOutsideCallers = map[string]string{
	"cmd/apexctl/bruno_import.go CommitBrunoImport": "the CLI's HTTP client posts to /api/v1/scripts/import/bruno/commit",
}

// lifecycleScriptProducer is a route outside /api/v1/scripts/import/ that
// can add a library script.
type lifecycleScriptProducer struct {
	Row string
	// Proof names pkg/api Go tests whose bodies request the route.
	Proof []string
	// Note qualifies a route that reaches a create path without adding a
	// script.
	Note string
}

// lifecycleScriptProducers inventories every route outside the import
// routes that can add a library script; the call-graph walk fails the build
// on one without a row.
var lifecycleScriptProducers = map[string]lifecycleScriptProducer{
	"POST /api/v1/scripts":                                 {Row: "Direct JSON and structured editor", Proof: []string{"TestCloneDraftKeepsSourceImportProvenance", "TestIdempotentScriptCreateAdoptsAnUnconfirmedEarlierCreate"}},
	"POST /api/v1/scripts/authoring/sessions/{id}/approve": {Row: "Multi-script imports", Proof: []string{"TestApproveAuthoringSessionPromotesDraftToScriptLibrary"}},
	"POST /api/v1/recorder/{id}/convert":                   {Row: "Recording", Proof: []string{"TestHandleConvertRecordingPersistsBaseline"}},
	"POST /api/v1/browser-recorder/sessions/{id}/convert":  {Row: "Recording", Proof: []string{"TestHandleConvertBrowserRecordingStoresScript"}},
	"POST /api/v1/library/import":                          {Row: "Template/component/clone", Proof: []string{"TestLibraryExportImportScenarioIncludesScriptDependency"}},
	"POST /api/v1/library/import/archive":                  {Row: "Template/component/clone", Proof: []string{"TestLibraryArchiveImportRefusesScriptItems"}, Note: "reaches the library import but refuses every item other than a result"},
	"POST /api/v1/library/remotes/{id}/pull":               {Row: "Template/component/clone", Proof: []string{"TestLibraryRemotePullAndPushScriptBetweenControllers"}},
	"POST /api/v1/integrations/github/sync-jobs":           {Row: "Every import launcher", Proof: []string{"TestGitHubImportSyncJobRouteCreatesTheScript"}},
}

// lifecycleCallGraph is a package's production functions (receiver-qualified)
// and package-level variables ("var." and the name), with, by name, the
// declarations that call or reference each name. A route registration's
// references to handlers are bindings, not calls, so they are left out; its
// calls are kept.
type lifecycleCallGraph struct {
	declared    map[string]bool
	declsByName map[string][]string
	callers     map[string]map[string]bool
	calls       map[string]map[string]bool
	mentions    map[string]map[string]bool
	// holders maps a type name to the struct fields and functions that hold
	// or return a value of it.
	holders map[string]map[string]bool
}

// lifecycleTypeName is the named type an expression's type holds, or "".
func lifecycleTypeName(expression ast.Expr) string {
	for {
		switch typed := expression.(type) {
		case *ast.StarExpr:
			expression = typed.X
		case *ast.ArrayType:
			expression = typed.Elt
		case *ast.MapType:
			expression = typed.Value
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.Ident:
			return typed.Name
		default:
			return ""
		}
	}
}

func lifecycleBareName(key string) string {
	if _, name, ok := strings.Cut(key, "."); ok {
		return name
	}
	return key
}

// lifecycleRegisters reports whether a declaration registers routes.
func lifecycleRegisters(node ast.Node) bool {
	registers := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !registers
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			registers = registers || fun.Name == "newAuthenticatedAPIRoute" || fun.Name == "newPublicAPIRoute"
		case *ast.SelectorExpr:
			if (fun.Sel.Name == "Handle" || fun.Sel.Name == "HandleFunc") && len(call.Args) > 0 {
				_, literal := call.Args[0].(*ast.BasicLit)
				registers = registers || literal
			}
		}
		return !registers
	})
	return registers
}

func loadLifecycleCallGraph(t *testing.T, dir string) *lifecycleCallGraph {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	graph := &lifecycleCallGraph{declared: map[string]bool{}, declsByName: map[string][]string{}, callers: map[string]map[string]bool{}, calls: map[string]map[string]bool{}, mentions: map[string]map[string]bool{}, holders: map[string]map[string]bool{}}
	add := func(set map[string]map[string]bool, outer, inner string) {
		if set[outer] == nil {
			set[outer] = map[string]bool{}
		}
		set[outer][inner] = true
	}
	declare := func(key, name string, node ast.Node) {
		graph.declared[key] = true
		graph.declsByName[name] = append(graph.declsByName[name], key)
		registers := lifecycleRegisters(node)
		edge := func(name string, call bool) {
			add(graph.mentions, key, name)
			if call {
				add(graph.calls, key, name)
			}
			if call || !registers {
				add(graph.callers, name, key)
			}
		}
		var visit func(ast.Node)
		visit = func(root ast.Node) {
			ast.Inspect(root, func(n ast.Node) bool {
				switch expression := n.(type) {
				case *ast.CallExpr:
					switch fun := expression.Fun.(type) {
					case *ast.SelectorExpr:
						edge(fun.Sel.Name, true)
						visit(fun.X)
					case *ast.Ident:
						edge(fun.Name, true)
					default:
						visit(fun)
					}
					for _, arg := range expression.Args {
						visit(arg)
					}
					return false
				case *ast.SelectorExpr:
					edge(expression.Sel.Name, false)
					visit(expression.X)
					return false
				case *ast.Ident:
					edge(expression.Name, false)
				}
				return true
			})
		}
		visit(node)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Body == nil {
					continue
				}
				if decl.Type.Results != nil {
					for _, result := range decl.Type.Results.List {
						if name := lifecycleTypeName(result.Type); name != "" {
							add(graph.holders, name, decl.Name.Name)
						}
					}
				}
				key := decl.Name.Name
				if decl.Recv != nil && len(decl.Recv.List) > 0 {
					receiver := decl.Recv.List[0].Type
					if star, ok := receiver.(*ast.StarExpr); ok {
						receiver = star.X
					}
					if index, ok := receiver.(*ast.IndexExpr); ok {
						receiver = index.X
					}
					if ident, ok := receiver.(*ast.Ident); ok {
						key = ident.Name + "." + key
					}
				}
				declare(key, decl.Name.Name, decl.Body)
			case *ast.GenDecl:
				if decl.Tok == token.TYPE {
					for _, spec := range decl.Specs {
						structure, ok := spec.(*ast.TypeSpec).Type.(*ast.StructType)
						if !ok {
							continue
						}
						for _, field := range structure.Fields.List {
							for _, name := range field.Names {
								if typeName := lifecycleTypeName(field.Type); typeName != "" {
									add(graph.holders, typeName, name.Name)
								}
							}
						}
					}
					continue
				}
				if decl.Tok != token.VAR {
					continue
				}
				// Package-level initializers (dispatch tables, route specs)
				// call and reference functions too.
				for _, spec := range decl.Specs {
					value := spec.(*ast.ValueSpec)
					for index, name := range value.Names {
						if index < len(value.Values) {
							declare("var."+name.Name, name.Name, value.Values[index])
						} else if len(value.Values) == 1 {
							declare("var."+name.Name, name.Name, value.Values[0])
						}
					}
				}
			}
		}
	}
	return graph
}

// lifecycleScriptProducerWalk walks up from the create primitives and
// returns every reached declaration, every reached route handler (bare names)
// and the reached declarations nothing calls.
func lifecycleScriptProducerWalk(t *testing.T, graph *lifecycleCallGraph, handlers map[string]bool) (map[string]bool, map[string]bool, []string) {
	t.Helper()
	var queue []string
	for _, primitive := range lifecycleScriptCreatePrimitives() {
		if keys := graph.declsByName[primitive]; len(keys) > 0 {
			queue = append(queue, keys...)
		} else {
			queue = append(queue, primitive)
		}
	}
	reached := map[string]bool{}
	reachedHandlers := map[string]bool{}
	var roots []string
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if reached[key] {
			continue
		}
		reached[key] = true
		name := lifecycleBareName(key)
		handler := key == "Server."+name && handlers[name]
		if handler {
			reachedHandlers[name] = true
		}
		var callers []string
		if resolution, ok := lifecycleProducerResolutions[key]; ok {
			callers = lifecycleResolvedCallers(t, graph, key, resolution)
		} else {
			if graph.declared[key] && len(graph.declsByName[name]) > 1 {
				t.Errorf("%s adds a library script, but %d declarations are named %s: list its callers in lifecycleProducerResolutions", key, len(graph.declsByName[name]), name)
				continue
			}
			for caller := range graph.callers[name] {
				if caller != key {
					callers = append(callers, caller)
				}
			}
		}
		if len(callers) == 0 && graph.declared[key] && !handler {
			roots = append(roots, key)
		}
		queue = append(queue, callers...)
	}
	sort.Strings(roots)
	return reached, reachedHandlers, roots
}

// lifecycleResolvedCallers checks a resolution against the graph: each
// listed caller calls the method, and every function that holds the
// receiver type and calls the method is listed.
func lifecycleResolvedCallers(t *testing.T, graph *lifecycleCallGraph, key string, resolution lifecycleResolution) []string {
	t.Helper()
	receiver, method, _ := strings.Cut(key, ".")
	holders := map[string]bool{}
	for _, holder := range resolution.Holders {
		holders[holder] = true
	}
	if !holders[receiver] {
		t.Errorf("lifecycleProducerResolutions: %s must list its type %s among its holders", key, receiver)
	}
	for holder := range graph.holders[receiver] {
		if !holders[holder] {
			t.Errorf("lifecycleProducerResolutions: %s holds a %s; list it among the holders of %s", holder, receiver, key)
		}
	}
	listed := map[string]bool{}
	for _, caller := range resolution.Callers {
		listed[caller] = true
		if !graph.calls[caller][method] {
			t.Errorf("lifecycleProducerResolutions: %s does not call %s", caller, method)
		}
	}
	for declaration, names := range graph.mentions {
		if strings.HasPrefix(declaration, receiver+".") || !graph.calls[declaration][method] {
			continue
		}
		for _, holder := range resolution.Holders {
			if names[holder] && !listed[declaration] {
				t.Errorf("%s names %s and calls %s: list it among the callers of %s in lifecycleProducerResolutions", declaration, holder, method, key)
			}
		}
	}
	return resolution.Callers
}

// lifecycleStorageCreateEntries walks pkg/storage's calls and references
// (a method value passed to a transaction helper included) up from each
// create insert site and returns the exported functions it reaches.
func lifecycleStorageCreateEntries(t *testing.T) map[string]bool {
	t.Helper()
	dir := "../storage"
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	inserts := map[string]bool{}
	fset := token.NewFileSet()
	insert := regexp.MustCompile(`(?i)INSERT\s+INTO\s+scripts\b`)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, file, source, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				if insert.Match(source[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset]) {
					inserts[fn.Name.Name] = true
				}
			}
		}
	}
	for name := range inserts {
		if _, ok := lifecycleScriptInsertSites[name]; !ok {
			t.Errorf("pkg/storage %s inserts into scripts: classify it in lifecycleScriptInsertSites", name)
		}
	}
	for name := range lifecycleScriptInsertSites {
		if !inserts[name] {
			t.Errorf("lifecycleScriptInsertSites names %s, which no longer inserts into scripts", name)
		}
	}
	graph := loadLifecycleCallGraph(t, dir)
	reached := map[string]bool{}
	entries := map[string]bool{}
	var queue []string
	for name, kind := range lifecycleScriptInsertSites {
		if kind == "create" {
			queue = append(queue, graph.declsByName[name]...)
		}
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if reached[key] {
			continue
		}
		reached[key] = true
		name := lifecycleBareName(key)
		if ast.IsExported(name) {
			entries[name] = true
		}
		if len(graph.declsByName[name]) > 1 {
			t.Errorf("pkg/storage %s reaches a script insert, but %d declarations are named %s", key, len(graph.declsByName[name]), name)
			continue
		}
		for caller := range graph.callers[name] {
			if caller != key {
				queue = append(queue, caller)
			}
		}
	}
	return entries
}

// TestLifecycleParityScriptProducersAreInventoried walks the controller's
// call graph up from every call that adds a library script; each route it
// reaches is an import route (inventoried by lifecycleImportRoutes) or has a
// lifecycleScriptProducers row, and nothing else adds one unaccounted.
func TestLifecycleParityScriptProducersAreInventoried(t *testing.T) {
	entries := lifecycleStorageCreateEntries(t)
	for name := range entries {
		if !lifecycleStoreCreateEntries[name] {
			t.Errorf("pkg/storage %s reaches a script insert: add it to lifecycleStoreCreateEntries", name)
		}
	}
	for name := range lifecycleStoreCreateEntries {
		if !entries[name] {
			t.Errorf("lifecycleStoreCreateEntries names %s, which reaches no script insert", name)
		}
	}

	graph := loadLifecycleCallGraph(t, ".")
	for caller := range graph.callers["SaveScript"] {
		if caller != "TestRunner.saveStoredScriptWithImportTrust" {
			t.Errorf("%s calls SaveScript directly: route it through a create primitive or saveStoredScriptWithImportTrust", caller)
		}
	}
	callers := graph.callers["saveStoredScriptWithImportTrust"]
	for caller := range callers {
		if _, ok := lifecycleSaveScriptCallers[caller]; !ok {
			t.Errorf("%s calls saveStoredScriptWithImportTrust: classify it in lifecycleSaveScriptCallers", caller)
		}
	}
	for caller := range lifecycleSaveScriptCallers {
		if !callers[caller] {
			t.Errorf("lifecycleSaveScriptCallers names %s, which does not call saveStoredScriptWithImportTrust", caller)
		}
	}

	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatal(err)
	}
	routesByHandler := map[string][]string{}
	for _, route := range discovered {
		if route.Handler != "" {
			routesByHandler[route.Handler] = append(routesByHandler[route.Handler], route.Method+" "+route.Path)
		}
	}
	handlers := map[string]bool{}
	for handler := range routesByHandler {
		handlers[handler] = true
	}
	reached, reachedHandlers, roots := lifecycleScriptProducerWalk(t, graph, handlers)
	for _, root := range roots {
		if _, ok := lifecycleProducerNonRoutes[root]; !ok {
			t.Errorf("%s adds a library script and no route handler calls it: register it, list its callers in lifecycleProducerResolutions, or classify it in lifecycleProducerNonRoutes", root)
		}
	}
	rootSet := map[string]bool{}
	for _, root := range roots {
		rootSet[root] = true
	}
	for root := range lifecycleProducerNonRoutes {
		if !rootSet[root] {
			t.Errorf("lifecycleProducerNonRoutes names %s, which is not a root of the walk", root)
		}
	}
	producerRoutes := map[string]bool{}
	for handler := range reachedHandlers {
		for _, route := range routesByHandler[handler] {
			producerRoutes[route] = true
			method, path, _ := strings.Cut(route, " ")
			if strings.HasPrefix(path, "/api/v1/scripts/import/") {
				if _, ok := lifecycleImportRoutes[path]; method != "POST" || !ok {
					t.Errorf("%s (%s) adds a library script but has no lifecycleImportRoutes row", route, handler)
				}
				continue
			}
			if _, ok := lifecycleScriptProducers[route]; !ok {
				t.Errorf("%s (%s) adds a library script: add it to lifecycleScriptProducers with its AUTH-2 row and proof", route, handler)
			}
		}
	}
	rows := map[string]bool{}
	for _, row := range lifecycleAuth2Rows() {
		rows[row.Entry] = true
	}
	goTests := lifecycleGoTests(t)
	for route, producer := range lifecycleScriptProducers {
		if !producerRoutes[route] {
			t.Errorf("lifecycleScriptProducers names %s, which the walk does not reach", route)
		}
		if !rows[producer.Row] {
			t.Errorf("%s names AUTH-2 row %q, which is not in the spec table", route, producer.Row)
		}
		if len(producer.Proof) == 0 {
			t.Errorf("%s names no proof", route)
		}
		method, path, _ := strings.Cut(route, " ")
		for _, proof := range producer.Proof {
			if problem := lifecycleGoProofProblem(goTests, proof); problem != "" {
				t.Errorf("%s names proof %s, which %s", route, proof, problem)
				continue
			}
			if !lifecycleRouteRequested(goTests[proof].Body, method, path) {
				t.Errorf("%s names proof %s, which never requests it", route, proof)
			}
		}
	}

	// Outside these two packages, nothing calls a script create unclassified:
	// a store entry point, or an exported function the walk reached that
	// another package can call.
	outside := map[string]bool{}
	for name := range lifecycleStoreCreateEntries {
		outside[name] = true
	}
	for key := range reached {
		receiver, name, method := strings.Cut(key, ".")
		if !method {
			name = receiver
		}
		if graph.declared[key] && receiver != "var" && ast.IsExported(name) && (!method || ast.IsExported(receiver)) {
			outside[name] = true
		}
	}
	found := map[string]bool{}
	for _, call := range lifecycleOutsideCreateCalls(t, outside) {
		found[call] = true
		if _, ok := lifecycleOutsideCallers[call]; !ok {
			t.Errorf("%s: a script create outside pkg/api and pkg/storage; classify it in lifecycleOutsideCallers", call)
		}
	}
	for call := range lifecycleOutsideCallers {
		if !found[call] {
			t.Errorf("lifecycleOutsideCallers names %s, which no longer calls a script create", call)
		}
	}
}

// lifecycleOutsideCreateCalls lists ("file name") the production calls,
// outside the top-level pkg/api and pkg/storage packages, to a function or
// method with one of these names.
func lifecycleOutsideCreateCalls(t *testing.T, names map[string]bool) []string {
	t.Helper()
	return lifecycleOutsideCreateCallsAt(t, "../..", names)
}

func lifecycleOutsideCreateCallsAt(t *testing.T, root string, names map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	var calls []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "_retired" || name == "node_modules" || name == "vendor" || name == "testdata" || relative == "dashboard") {
				return filepath.SkipDir
			}
			// A nested checkout is a separate repository, not a producer in this
			// checkout. Its .git may be a directory or a worktree pointer file.
			if path != root {
				if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr == nil {
					return filepath.SkipDir
				} else if !os.IsNotExist(statErr) {
					return statErr
				}
			}
			return nil
		}
		// The two walked packages are covered above; their subpackages are not.
		if directory := filepath.ToSlash(filepath.Dir(relative)); directory == "pkg/api" || directory == "pkg/storage" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", relative, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					if names[fun.Sel.Name] {
						calls = append(calls, relative+" "+fun.Sel.Name)
					}
				case *ast.Ident:
					if names[fun.Name] {
						calls = append(calls, relative+" "+fun.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func TestLifecycleOutsideCreateCallsSkipsNestedCheckouts(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"cmd", "nested-worktree", "nested-repo"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "producer.go"), []byte("package producer\nfunc run() { CreateScript() }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nested-worktree", ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested-repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := lifecycleOutsideCreateCallsAt(t, root, map[string]bool{"CreateScript": true})
	if len(calls) != 1 || calls[0] != "cmd/producer.go CreateScript" {
		t.Fatalf("calls = %v, want only the root checkout producer", calls)
	}
}

// lifecycleRouteMentioned reports whether a source mentions a route: the
// path or, for a path with parameters (built at run time), each literal piece
// between them, the last one ending the path.
func lifecycleRouteMentioned(source []byte, path string) bool {
	parts := lifecycleRouteParameter.Split(path, -1)
	for index, part := range parts {
		if part == "" {
			continue
		}
		pattern := regexp.QuoteMeta(part)
		if index == len(parts)-1 {
			pattern += `(?:[^\w/-]|$)`
		}
		if !regexp.MustCompile(pattern).Match(source) {
			return false
		}
	}
	return true
}

var lifecycleRouteParameter = regexp.MustCompile(`\{[^}]+\}`)

// lifecycleRouteRequested reports whether a test body requests a route: a
// line with the path's first literal piece also names the method (its
// net/http constant, its string, or a helper named for it, such as
// postGitHubJSON), the other pieces
// appear, and the last ends a string or starts a query (a path without
// parameters ends on that line).
func lifecycleRouteRequested(body, method, path string) bool {
	parts := lifecycleRouteParameter.Split(path, -1)
	title := strings.ToUpper(method[:1]) + strings.ToLower(method[1:])
	methodToken := regexp.MustCompile(`\bMethod` + title + `\b|"` + method + `"|\b(?:` + strings.ToLower(method) + `|` + title + `)[A-Z]\w*\(`)
	pattern := func(index int) *regexp.Regexp {
		quoted := regexp.QuoteMeta(parts[index])
		if index == len(parts)-1 {
			quoted += `["?]`
		}
		return regexp.MustCompile(quoted)
	}
	first := pattern(0)
	requested := false
	for _, line := range strings.Split(body, "\n") {
		requested = requested || (first.MatchString(line) && methodToken.MatchString(line))
	}
	for index := 1; requested && index < len(parts); index++ {
		requested = parts[index] == "" || pattern(index).MatchString(body)
	}
	return requested
}

// renderLifecycleParityProperties is the generated property and producer
// sections.
func renderLifecycleParityProperties(out *strings.Builder) {
	properties := lifecycleAuth2Properties()
	out.WriteString("\n## AUTH-2 properties\n\nEvery row proves each property with a named test (`TestLifecycleParityPropertiesAreProven`): a Go test in `pkg/api` outside a build-tagged file that cannot skip itself, a Vitest test under `dashboard/src` in a file that skips, focuses or conditions no test, or a Playwright test that runs in both e2e profiles. A shared cell defers to the row whose canonical owner proves the property directly; an open cell records the gap.\n\n")
	out.WriteString("| AUTH-2 row |")
	for _, property := range lifecycleProperties {
		out.WriteString(" " + string(property) + " |")
	}
	out.WriteString("\n| --- |")
	for range lifecycleProperties {
		out.WriteString(" --- |")
	}
	out.WriteString("\n")
	for _, row := range lifecycleAuth2Rows() {
		out.WriteString("| " + row.Entry + " |")
		for _, property := range lifecycleProperties {
			proofs := properties[row.Entry][property]
			switch {
			case len(proofs) == 1 && proofs[0].Kind == "open":
				out.WriteString(" open |")
			case len(proofs) == 1 && proofs[0].Kind == "shared":
				out.WriteString(" shared |")
			default:
				out.WriteString(fmt.Sprintf(" %d |", len(proofs)))
			}
		}
		out.WriteString("\n")
	}
	for _, row := range lifecycleAuth2Rows() {
		out.WriteString("\n### " + row.Entry + "\n\n")
		for _, property := range lifecycleProperties {
			var cells []string
			for _, proof := range properties[row.Entry][property] {
				switch proof.Kind {
				case "go":
					cells = append(cells, "`"+proof.Title+"`")
				case "vitest", "e2e":
					cells = append(cells, "`"+proof.File+"`: "+proof.Title)
				case "shared":
					cells = append(cells, "shared with "+proof.Title+": "+proof.Why)
				case "open":
					cells = append(cells, "**open**: "+proof.Title)
				}
			}
			out.WriteString("- " + string(property) + ": " + strings.Join(cells, "; ") + "\n")
		}
	}
	out.WriteString("\n## Script producers\n\n`TestLifecycleParityScriptProducersAreInventoried` walks `pkg/api` up from every call that adds a library script (the store methods that reach a script insert and the runner's create cores) through every call and reference, including package-level initializers, to the route handlers that reach it. A route registration's references to handlers are bindings, not calls; a handler another handler calls is followed too. Each such route is an import route above or has a row here, and each proof is a `pkg/api` test that requests the route with its method. The walk also classifies every `pkg/storage` script insert and the store methods that reach one (through calls and references), the callers of the `SaveScript` upsert, and every production call outside the two packages (their subpackages included) to a store entry point or to an exported function the walk reached.\n\n| Route | AUTH-2 row | Proof | Note |\n| --- | --- | --- | --- |\n")
	routes := make([]string, 0, len(lifecycleScriptProducers))
	for route := range lifecycleScriptProducers {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	for _, route := range routes {
		producer := lifecycleScriptProducers[route]
		note := producer.Note
		if note == "" {
			note = "—"
		}
		out.WriteString(fmt.Sprintf("| `%s` | %s | `%s` | %s |\n", route, producer.Row, strings.Join(producer.Proof, "`, `"), note))
	}
}
