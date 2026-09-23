package api

// Command Center per-group run settings.
//
// A created-but-not-yet-started run (queued, reserved, scheduled, or pending)
// exposes each group's effective runtime settings and script parameters for
// inspection, and accepts targeted edits that are applied to the run in place:
// the group is recompiled from its immutable base script through the same
// revision machinery the queued-run editor uses, so every creation-time gate
// (secret protection, runtime-settings review, Java Vuser approval, launch
// conflicts) re-applies. Once a run has started the views stay readable but the
// mutation endpoints refuse.
//
// Env and redaction rules are deliberately not editable here — secrets and PII
// policy stay in the scenario editor, mirroring apply-to-groups. Every applied
// edit records a test-scoped audit event that surfaces in the run manifest's
// runtime_changes timeline (the post-test results delta).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxTestGroupParameters             = 256
	maxTestGroupParameterNameBytes     = 256
	testGroupSettingsAuditValueCap     = 8
	testGroupSettingsAuditValueBytes   = 256
	testGroupImportedRuntimeFileCap    = 8
	testGroupImportedRuntimeSectionCap = 12
	testGroupImportedRuntimeRowCap     = 30
	auditParameterValueOverflowNote    = "… (+%d more)"
)

var (
	errTestGroupSettingsTestNotFound     = errors.New("test not found")
	errTestGroupSettingsGroupNotFound    = errors.New("group not found")
	errTestGroupSettingsPersist          = errors.New("failed to persist updated run settings")
	errTestGroupSettingsRevisionRequired = errors.New("expected_revision is required")
)

// testGroupSettingsNotEditableError reports why a run's group settings are
// read-only. It maps to HTTP 409: the resource exists but its lifecycle state
// no longer accepts pre-start edits.
type testGroupSettingsNotEditableError struct {
	status string
}

func (e *testGroupSettingsNotEditableError) Error() string {
	return fmt.Sprintf("test is %s; group settings are editable only before the run starts", e.status)
}

// testGroupSettingsRevisionConflictError reports an optimistic-concurrency
// miss. Both focused editors share one revision so a parameter edit also
// invalidates a stale runtime-settings form (and vice versa).
type testGroupSettingsRevisionConflictError struct{}

func (*testGroupSettingsRevisionConflictError) Error() string {
	return "group settings changed since they were loaded; reload and re-apply your edits"
}

// testGroupSettingsEditableStatus reports whether a run is still in a
// pre-start lifecycle state. Shakeout runs already execute scripts, so they
// are deliberately not editable.
func testGroupSettingsEditableStatus(status string) bool {
	switch status {
	case "DRAFT", "CREATED", "QUEUED", "RESERVED", "SCHEDULED", "PENDING":
		return true
	}
	return false
}

func testGroupSettingsEditableReason(status string) string {
	if testGroupSettingsEditableStatus(status) {
		return ""
	}
	return fmt.Sprintf("run is %s; settings were locked when the run left the pre-start queue", status)
}

// commandCenterRuntimeFieldKeys is the field set the Command Center group
// dialog may replace. It is the apply-to-groups vocabulary plus java_vuser
// (safe here because the recompile re-runs the Java Env validation and the
// authoring-approval executable-surface gate). Env and redaction rules remain
// excluded: credentials and PII policy are never editable from the run view.
var commandCenterRuntimeFieldKeys = func() map[string]bool {
	keys := map[string]bool{"java_vuser": true}
	for key := range runtimeOverrideFieldKeys {
		keys[key] = true
	}
	return keys
}()

func normalizeCommandCenterRuntimeFieldKeys(fields []string) ([]string, error) {
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		key := strings.TrimSpace(strings.ToLower(field))
		if key == "" || seen[key] {
			continue
		}
		if key == "env" || key == "redaction_rules" {
			return nil, fmt.Errorf("field %q cannot be edited from the command center; use the scenario editor", key)
		}
		if !commandCenterRuntimeFieldKeys[key] {
			return nil, fmt.Errorf("unknown runtime settings field %q", key)
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

// TestGroupRuntimeSettingsView is the read model for the Command Center
// group runtime-settings dialog. Every override layer is masked.
type TestGroupRuntimeSettingsView struct {
	TestID     string `json:"test_id"`
	RefKey     string `json:"ref_key,omitempty"`
	Group      string `json:"group"`
	GroupIndex int    `json:"group_index"`
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Status     string `json:"status"`
	Editable   bool   `json:"editable"`
	Revision   string `json:"revision"`
	// EditableReason explains a false Editable (run already started/finished).
	EditableReason string   `json:"editable_reason,omitempty"`
	EditableFields []string `json:"editable_fields"`
	// RuntimeSettings is the group's own override layer.
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	// TestRuntimeSettings is the run-level (root) override layer the group inherits.
	TestRuntimeSettings *RuntimeSettingsOverride `json:"test_runtime_settings,omitempty"`
	// ScriptSettings is the base script's own configuration before any overrides.
	ScriptSettings *RuntimeSettingsOverride `json:"script_settings,omitempty"`
	// EffectiveSettings is the compiled configuration this run will execute.
	EffectiveSettings     *RuntimeSettingsOverride        `json:"effective_settings,omitempty"`
	ImportedRuntimeSource *TestGroupImportedRuntimeSource `json:"imported_runtime_source,omitempty"`
	ProjectID             string                          `json:"-"`
	TestName              string                          `json:"-"`
}

type TestGroupImportedRuntimeDisposition struct {
	Reason   string   `json:"reason,omitempty"`
	Status   string   `json:"status,omitempty"`
	ParityID string   `json:"parity_id,omitempty"`
	Fields   []string `json:"fields,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// TestGroupImportedRuntimeSource is a bounded projection of the immutable
// run snapshot's importer provenance. It never follows the mutable library
// script after run creation.
type TestGroupImportedRuntimeSource struct {
	Files        map[string]map[string]map[string]string                              `json:"files,omitempty"`
	Dispositions map[string]map[string]map[string]TestGroupImportedRuntimeDisposition `json:"dispositions,omitempty"`
	Truncated    bool                                                                 `json:"truncated,omitempty"`
}

type TestGroupRuntimeSettingsUpdateResult struct {
	TestID    string   `json:"test_id"`
	Group     string   `json:"group"`
	Fields    []string `json:"fields"`
	NoChanges bool     `json:"no_changes,omitempty"`
	// Changes maps each actually-changed field to its masked {from,to} pair.
	Changes         map[string]any           `json:"changes,omitempty"`
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	ReviewStatus    string                   `json:"review_status,omitempty"`
	Revision        string                   `json:"revision"`
	ProjectID       string                   `json:"-"`
	TestName        string                   `json:"-"`
}

// TestGroupParameterDataSource summarizes a data source a parameter may bind
// to. Rows are intentionally not exposed here; the dialog only needs shape.
type TestGroupParameterDataSource struct {
	Name     string   `json:"name"`
	Type     string   `json:"type,omitempty"`
	Columns  []string `json:"columns,omitempty"`
	RowCount int      `json:"row_count,omitempty"`
}

type TestGroupParametersView struct {
	TestID         string                         `json:"test_id"`
	RefKey         string                         `json:"ref_key,omitempty"`
	Group          string                         `json:"group"`
	GroupIndex     int                            `json:"group_index"`
	ScriptID       string                         `json:"script_id,omitempty"`
	ScriptName     string                         `json:"script_name,omitempty"`
	Protocol       string                         `json:"protocol,omitempty"`
	Status         string                         `json:"status"`
	Editable       bool                           `json:"editable"`
	Revision       string                         `json:"revision"`
	EditableReason string                         `json:"editable_reason,omitempty"`
	Parameters     []dsl.Parameter                `json:"parameters"`
	DataSources    []TestGroupParameterDataSource `json:"data_sources,omitempty"`
	ProjectID      string                         `json:"-"`
	TestName       string                         `json:"-"`
}

type TestGroupParametersUpdateResult struct {
	TestID    string   `json:"test_id"`
	Group     string   `json:"group"`
	Changed   []string `json:"changed"`
	NoChanges bool     `json:"no_changes,omitempty"`
	// Changes maps each changed parameter name to a compacted {from,to} pair.
	Changes    map[string]any  `json:"changes,omitempty"`
	Parameters []dsl.Parameter `json:"parameters"`
	Revision   string          `json:"revision"`
	ProjectID  string          `json:"-"`
	TestName   string          `json:"-"`
}

func (tr *TestRunner) testGroupIndexLocked(test *ManagedTest, groupName string) (int, error) {
	wanted := normalizeEffectiveGroupName(groupName)
	if wanted == "" {
		return -1, fmt.Errorf("group name is required")
	}
	for index := range test.Groups {
		if normalizeEffectiveGroupName(test.Groups[index].Name) == wanted {
			return index, nil
		}
	}
	return -1, errTestGroupSettingsGroupNotFound
}

func commandCenterEditableFieldList() []string {
	fields := make([]string, 0, len(commandCenterRuntimeFieldKeys))
	for key := range commandCenterRuntimeFieldKeys {
		fields = append(fields, key)
	}
	sortStrings(fields)
	return fields
}

func projectTestGroupImportedRuntimeSource(script *dsl.Script) *TestGroupImportedRuntimeSource {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return nil
	}
	preserved := script.Metadata.Import.Preserved
	runtimeFiles, hasFiles := preserved["runtime_files"]
	runtimeDispositions, hasDispositions := preserved["runtime_setting_dispositions"]
	if !hasFiles && !hasDispositions {
		return nil
	}
	snapshot := &dsl.Script{Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Preserved: map[string]any{},
	}}}
	if hasFiles {
		snapshot.Metadata.Import.Preserved["runtime_files"] = runtimeFiles
	}
	if hasDispositions {
		snapshot.Metadata.Import.Preserved["runtime_setting_dispositions"] = runtimeDispositions
	}
	masked := maskScriptSecrets(snapshot)
	if masked == nil || masked.Metadata == nil || masked.Metadata.Import == nil {
		return nil
	}
	payload, err := json.Marshal(masked.Metadata.Import.Preserved)
	if err != nil {
		return nil
	}
	var decoded struct {
		Files        map[string]map[string]map[string]string                              `json:"runtime_files"`
		Dispositions map[string]map[string]map[string]TestGroupImportedRuntimeDisposition `json:"runtime_setting_dispositions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil || len(decoded.Files) == 0 {
		return nil
	}

	result := &TestGroupImportedRuntimeSource{
		Files:        map[string]map[string]map[string]string{},
		Dispositions: map[string]map[string]map[string]TestGroupImportedRuntimeDisposition{},
	}
	fileNames := make([]string, 0, len(decoded.Files))
	for fileName := range decoded.Files {
		fileNames = append(fileNames, fileName)
	}
	sortStrings(fileNames)
	if len(fileNames) > testGroupImportedRuntimeFileCap {
		fileNames = fileNames[:testGroupImportedRuntimeFileCap]
		result.Truncated = true
	}
	sectionsCopied := 0
	for _, fileName := range fileNames {
		if len(fileName) > testGroupSettingsAuditValueBytes {
			result.Truncated = true
			continue
		}
		sectionNames := make([]string, 0, len(decoded.Files[fileName]))
		for sectionName := range decoded.Files[fileName] {
			sectionNames = append(sectionNames, sectionName)
		}
		sortStrings(sectionNames)
		fileSections := map[string]map[string]string{}
		fileDispositions := map[string]map[string]TestGroupImportedRuntimeDisposition{}
		for _, sectionName := range sectionNames {
			if len(sectionName) > testGroupSettingsAuditValueBytes {
				result.Truncated = true
				continue
			}
			if sectionsCopied == testGroupImportedRuntimeSectionCap {
				result.Truncated = true
				break
			}
			sectionsCopied++
			keys := make([]string, 0, len(decoded.Files[fileName][sectionName]))
			for key := range decoded.Files[fileName][sectionName] {
				keys = append(keys, key)
			}
			sortStrings(keys)
			if len(keys) > testGroupImportedRuntimeRowCap {
				keys = keys[:testGroupImportedRuntimeRowCap]
				result.Truncated = true
			}
			rows := map[string]string{}
			dispositionRows := map[string]TestGroupImportedRuntimeDisposition{}
			for _, key := range keys {
				if len(key) > testGroupSettingsAuditValueBytes {
					result.Truncated = true
					continue
				}
				value := decoded.Files[fileName][sectionName][key]
				bounded := boundedTestGroupAuditValue(value)
				if bounded != value {
					result.Truncated = true
				}
				rows[key] = bounded
				if disposition, ok := decoded.Dispositions[fileName][sectionName][key]; ok {
					if len(disposition.Fields) > testGroupSettingsAuditValueCap {
						disposition.Fields = append([]string(nil), disposition.Fields[:testGroupSettingsAuditValueCap]...)
						result.Truncated = true
					}
					for index := range disposition.Fields {
						disposition.Fields[index] = boundedTestGroupAuditValue(disposition.Fields[index])
					}
					disposition.Reason = boundedTestGroupAuditValue(disposition.Reason)
					disposition.Status = boundedTestGroupAuditValue(disposition.Status)
					disposition.ParityID = boundedTestGroupAuditValue(disposition.ParityID)
					disposition.Note = boundedTestGroupAuditValue(disposition.Note)
					dispositionRows[key] = disposition
				}
			}
			fileSections[sectionName] = rows
			if len(dispositionRows) > 0 {
				fileDispositions[sectionName] = dispositionRows
			}
		}
		if len(fileSections) > 0 {
			result.Files[fileName] = fileSections
		}
		if len(fileDispositions) > 0 {
			result.Dispositions[fileName] = fileDispositions
		}
	}
	if len(result.Files) == 0 {
		return nil
	}
	return result
}

// testGroupEditableSettingsRevision hashes only the focused editors' small,
// disclosure-safe state: the group's editable runtime override sections plus
// the parameter/provenance projection. It deliberately excludes actions and
// data-source contents, which can be large and cannot be edited here.
func testGroupEditableSettingsRevision(runtimeSettings *RuntimeSettingsOverride, script *dsl.Script) (string, error) {
	maskedRuntime := maskRuntimeSettings(cloneRuntimeSettings(runtimeSettings))
	editableRuntime := replaceRuntimeFields(nil, maskedRuntime, commandCenterEditableFieldList())
	parameterSnapshot := testGroupParameterProjectionSnapshot(script)
	parameters, err := projectTestGroupParameters(parameterSnapshot)
	if err != nil {
		return "", fmt.Errorf("project group parameters for revision: %w", err)
	}
	credentialParameterNames := []string{}
	if parameterSnapshot != nil && parameterSnapshot.Metadata != nil && parameterSnapshot.Metadata.Import != nil {
		credentialParameterNames = importPreservedStringSlice(
			parameterSnapshot.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey],
		)
		sortStrings(credentialParameterNames)
	}
	payload, err := json.Marshal(struct {
		RuntimeSettings          *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
		Parameters               []dsl.Parameter          `json:"parameters"`
		CredentialParameterNames []string                 `json:"credential_parameter_names,omitempty"`
	}{
		RuntimeSettings:          editableRuntime,
		Parameters:               parameters,
		CredentialParameterNames: credentialParameterNames,
	})
	if err != nil {
		return "", fmt.Errorf("encode group settings revision: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}

func requireTestGroupSettingsRevision(expected string, runtimeSettings *RuntimeSettingsOverride, script *dsl.Script) (string, error) {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return "", errTestGroupSettingsRevisionRequired
	}
	current, err := testGroupEditableSettingsRevision(runtimeSettings, script)
	if err != nil {
		return "", err
	}
	if expected != current {
		return current, &testGroupSettingsRevisionConflictError{}
	}
	return current, nil
}

func (tr *TestRunner) durableTestGroupSettingsSnapshotForHARead(id string) (*ManagedTest, bool, error) {
	if tr == nil || tr.leadership == nil || tr.holdsExecutionLeadership() || tr.testStore == nil {
		return nil, false, nil
	}
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	record, err := tr.testStore.GetTest(resolvedID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, true, errTestGroupSettingsTestNotFound
	}
	if err != nil {
		return nil, true, fmt.Errorf("refresh group settings test: %w", err)
	}
	if record == nil {
		return nil, true, errTestGroupSettingsTestNotFound
	}
	managed := managedTestFromRecord(record)
	if err := tr.restoreManagedTestRuntimeAndRepair(managed); err != nil {
		return nil, true, fmt.Errorf("restore group settings test: %w", err)
	}
	return managed, true, nil
}

func (tr *TestRunner) GetTestGroupRuntimeSettingsView(id string, groupName string) (*TestGroupRuntimeSettingsView, error) {
	tr.refreshTestsForHARead()
	test, durable, err := tr.durableTestGroupSettingsSnapshotForHARead(id)
	if err != nil {
		return nil, err
	}
	locked := false
	if !durable {
		tr.mu.RLock()
		locked = true
		id = tr.resolveRefKeyLocked(id)
		var ok bool
		test, ok = tr.tests[id]
		if !ok {
			tr.mu.RUnlock()
			return nil, errTestGroupSettingsTestNotFound
		}
	}
	index, err := tr.testGroupIndexLocked(test, groupName)
	if err != nil {
		if locked {
			tr.mu.RUnlock()
		}
		return nil, err
	}
	group := test.Groups[index]
	testID := test.ID
	refKey := test.RefKey
	status := test.Status
	projectID := test.ProjectID
	testName := test.Name
	groupRuntimeSettings := cloneRuntimeSettings(group.RuntimeSettings)
	testRuntimeSettings := cloneRuntimeSettings(test.RuntimeSettings)
	baseScript := group.baseScript
	compiledScript := group.script
	if locked {
		tr.mu.RUnlock()
	}

	revision, err := testGroupEditableSettingsRevision(groupRuntimeSettings, compiledScript)
	if err != nil {
		return nil, err
	}

	view := &TestGroupRuntimeSettingsView{
		TestID:              testID,
		RefKey:              refKey,
		Group:               group.Name,
		GroupIndex:          index,
		ScriptID:            group.ScriptID,
		ScriptName:          group.ScriptName,
		Protocol:            group.Protocol,
		Status:              status,
		Editable:            testGroupSettingsEditableStatus(status),
		Revision:            revision,
		EditableReason:      testGroupSettingsEditableReason(status),
		EditableFields:      commandCenterEditableFieldList(),
		RuntimeSettings:     maskRuntimeSettings(groupRuntimeSettings),
		TestRuntimeSettings: maskRuntimeSettings(testRuntimeSettings),
		ProjectID:           projectID,
		TestName:            testName,
	}
	if baseScript != nil {
		view.ScriptSettings = maskRuntimeSettings(runtimeSettingsFromScriptConfig(baseScript.Config))
	}
	if compiledScript != nil {
		effective := runtimeSettingsFromScriptConfig(compiledScript.Config)
		if mergedEnv := mergeRuntimeSettings(testRuntimeSettings, groupRuntimeSettings); mergedEnv != nil && len(mergedEnv.Env) > 0 {
			if effective == nil {
				effective = &RuntimeSettingsOverride{}
			}
			effective.Env = mergedEnv.Env
		}
		view.EffectiveSettings = maskRuntimeSettings(effective)
	}
	provenanceScript := baseScript
	if provenanceScript == nil {
		provenanceScript = compiledScript
	}
	view.ImportedRuntimeSource = projectTestGroupImportedRuntimeSource(provenanceScript)
	return view, nil
}

func (tr *TestRunner) UpdateTestGroupRuntimeSettings(id string, groupName string, expectedRevision string, incoming *RuntimeSettingsOverride, fields []string) (*TestGroupRuntimeSettingsUpdateResult, error) {
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return nil, errTestGroupSettingsRevisionRequired
	}
	normalizedFields, err := normalizeCommandCenterRuntimeFieldKeys(fields)
	if err != nil {
		return nil, err
	}
	if len(normalizedFields) == 0 {
		return nil, fmt.Errorf("at least one runtime settings field is required")
	}
	if incoming != nil && (len(incoming.Env) > 0 || len(incoming.RedactionRules) > 0) {
		return nil, fmt.Errorf("env and redaction rules cannot be edited from the command center; use the scenario editor")
	}
	// Validate the raw selected layer before masked sentinels are reconciled.
	// Controller-owned runtime secret references may only be restored from the
	// prior value at that same protected path; clients cannot author or retarget
	// them into another executable field.
	selectedIncoming := replaceRuntimeFields(nil, incoming, normalizedFields)
	if err := rejectReservedRuntimeOverrideSecretReferences(selectedIncoming); err != nil {
		return nil, err
	}

	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return nil, err
	}
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok {
		return nil, errTestGroupSettingsTestNotFound
	}
	if !testGroupSettingsEditableStatus(test.Status) {
		return nil, &testGroupSettingsNotEditableError{status: test.Status}
	}
	index, err := tr.testGroupIndexLocked(test, groupName)
	if err != nil {
		return nil, err
	}
	oldGroup := test.Groups[index]
	currentRevision, err := requireTestGroupSettingsRevision(expectedRevision, oldGroup.RuntimeSettings, oldGroup.script)
	if err != nil {
		return nil, err
	}
	// A test rehydrated after a restart/failover has no claimed execution
	// epoch yet; claim it only after the client's revision wins so a stale
	// request cannot mutate even execution-fence bookkeeping.
	claimedFence, err := tr.claimPassiveExecutionFenceLocked(test, nil)
	if err != nil {
		return nil, err
	}
	if claimedFence {
		defer releasePassiveExecutionFenceLocked(test)
	}
	if !oldGroup.IterationOverrideKnown {
		return nil, fmt.Errorf("group %q lacks runtime-override provenance; revise the queued run from the scenario editor instead", oldGroup.Name)
	}

	storedSettings := cloneRuntimeSettings(oldGroup.RuntimeSettings)
	merged := replaceRuntimeFields(storedSettings, incoming, normalizedFields)
	// Restore any masked credential the client echoed back inside an edited
	// section so an untouched secret survives a sibling-field edit.
	merged = reconcileGroupRuntimeSecrets(merged, storedSettings)
	changes := runtimeSettingsFieldChanges(storedSettings, merged, normalizedFields)
	if len(changes) == 0 {
		return &TestGroupRuntimeSettingsUpdateResult{
			TestID:          test.ID,
			Group:           oldGroup.Name,
			Fields:          []string{},
			NoChanges:       true,
			RuntimeSettings: maskRuntimeSettings(cloneRuntimeSettings(oldGroup.RuntimeSettings)),
			Revision:        currentRevision,
			ProjectID:       test.ProjectID,
			TestName:        test.Name,
		}, nil
	}

	iterations := cloneIntPtr(oldGroup.IterationOverride)
	if slices.Contains(normalizedFields, "iterations") {
		if value, present := runtimeSettingsIterationValue(merged); present {
			iterations = &value
		} else {
			iterations = nil
		}
	}
	entry := CreateGroupEntry{
		Name:                         oldGroup.Name,
		ScriptID:                     oldGroup.ScriptID,
		Role:                         oldGroup.Role,
		VUsers:                       oldGroup.VUsers,
		RampUp:                       oldGroup.RampUp,
		RampDown:                     oldGroup.RampDown,
		Stages:                       cloneLoadStages(oldGroup.Stages),
		Iterations:                   iterations,
		RuntimeSettings:              merged,
		Assignment:                   cloneGeneratorAssignmentSettings(oldGroup.Assignment),
		InitStrategy:                 oldGroup.Initialization,
		TransactionResponseTimeSLAMs: cloneFloat64Ptr(oldGroup.TransactionResponseTimeSLAMs),
		runtimeSecretScope:           test.ID,
	}
	newGroup, newSpec, err := tr.compileManagedTestGroupForModeWithRevisionLocked(
		index,
		len(test.Groups),
		entry,
		test.RuntimeSettings,
		oldGroup.sharedDataSources,
		test.ScenarioMode,
		&test.Groups[index],
		false,
	)
	if err != nil {
		return nil, err
	}
	// Newly protected plaintext from this request would exist only in leader
	// memory: it bypasses the governed secret-provisioning pipeline (durable
	// store, per-secret audit, service-account gating) and evaporates on
	// restart or HA failover, stranding the run at its start gate. Reject it
	// here; credential values are entered as {{secret.NAME}} references or
	// through the scenario editor's create/revise path.
	if len(newGroup.runtimeSecretValues) > 0 {
		return nil, fmt.Errorf("this edit adds new credential material, which the command center cannot store durably; reference a configured project secret with {{secret.NAME}} or revise the run from the scenario editor")
	}
	// Untouched sections may still reference secrets protected at creation;
	// their plaintext lives only in the prior group's in-memory overlay. Carry
	// it forward so those references stay resolvable, and extend the engine
	// value-redaction to cover the carried values — the same invariant the
	// restart-restore path maintains.
	newGroup.runtimeSecretValues = cloneRuntimeSecretValues(oldGroup.runtimeSecretValues)
	if newSpec.Script != nil {
		newSpec.SecretResolver = overlayRuntimeSecretResolver(newGroup.runtimeSecretValues, tr.secretResolverForProject(newSpec.Script.ProjectID))
	}
	if len(newGroup.runtimeSecretValues) > 0 {
		augmented, redactErr := engine.WithSecretValueRedactionChecked(newSpec.RedactionConfig, runtimeSecretValueList(newGroup.runtimeSecretValues))
		if redactErr != nil {
			return nil, fmt.Errorf("prepare runtime credential redaction: %w", redactErr)
		}
		newSpec.RedactionConfig = augmented
	}

	candidateGroups := append([]TestGroup(nil), test.Groups...)
	candidateGroups[index] = newGroup
	candidateSpecs := append([]engine.GroupConfig(nil), test.groupSpecs...)
	candidateSpecs[index] = newSpec
	applyTransactionResponseTimeSLAThresholds(test.GoalConfig, candidateGroups, candidateSpecs)
	review := buildRuntimeSettingsScenarioReview(candidateGroups, candidateSpecs)
	if review != nil && review.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", review.Summary)
	}
	candidate := *test
	candidate.Groups = candidateGroups
	candidate.groupSpecs = candidateSpecs
	if conflict := externalRunnerLaunchConflict(candidateGroups); conflict != nil {
		return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
	}
	if conflicts := tr.javaVuserRunnerLaunchConflictsForManagedTestLocked(&candidate); len(conflicts) > 0 {
		return nil, fmt.Errorf("%s: %s", conflicts[0].Code, conflicts[0].Message)
	}
	if conflict := importPromotionLaunchConflict(candidateGroups); conflict != nil {
		return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
	}

	previousGroups := test.Groups
	previousSpecs := test.groupSpecs
	previousReview := test.RuntimeSettingsReview
	previousAdvisory := test.CapacityAdvisory
	previousStartAuthorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	previousValidatedShakeoutPass := test.validatedScriptShakeoutPass
	previousValidatedShakeoutTerm := test.validatedScriptShakeoutTerm
	test.Groups = candidateGroups
	test.groupSpecs = candidateSpecs
	test.RuntimeSettingsReview = cloneRuntimeSettingsReview(review)
	clearScriptShakeoutAuthorizations(test)
	tr.refreshCapacityAdvisoryLocked(test)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Groups = previousGroups
		test.groupSpecs = previousSpecs
		test.RuntimeSettingsReview = previousReview
		test.CapacityAdvisory = previousAdvisory
		test.StartAuthorization = previousStartAuthorization
		test.validatedScriptShakeoutPass = previousValidatedShakeoutPass
		test.validatedScriptShakeoutTerm = previousValidatedShakeoutTerm
		return nil, fmt.Errorf("%w: %v", errTestGroupSettingsPersist, err)
	}

	changedFields := make([]string, 0, len(changes))
	for field := range changes {
		changedFields = append(changedFields, field)
	}
	sortStrings(changedFields)
	reviewStatus := ""
	if review != nil {
		reviewStatus = review.Status
	}
	newRevision, err := testGroupEditableSettingsRevision(newGroup.RuntimeSettings, newGroup.script)
	if err != nil {
		return nil, err
	}
	return &TestGroupRuntimeSettingsUpdateResult{
		TestID:          test.ID,
		Group:           newGroup.Name,
		Fields:          changedFields,
		Changes:         changes,
		RuntimeSettings: maskRuntimeSettings(cloneRuntimeSettings(newGroup.RuntimeSettings)),
		ReviewStatus:    reviewStatus,
		Revision:        newRevision,
		ProjectID:       test.ProjectID,
		TestName:        test.Name,
	}, nil
}

func (tr *TestRunner) GetTestGroupParametersView(id string, groupName string) (*TestGroupParametersView, error) {
	return tr.getTestGroupParametersView(id, groupName, summarizeParameterDataSources)
}

type testGroupDataSourceSummarizer func([]dsl.DataSource) []TestGroupParameterDataSource

func (tr *TestRunner) getTestGroupParametersView(id string, groupName string, summarize testGroupDataSourceSummarizer) (*TestGroupParametersView, error) {
	tr.refreshTestsForHARead()
	test, durable, err := tr.durableTestGroupSettingsSnapshotForHARead(id)
	if err != nil {
		return nil, err
	}
	locked := false
	if !durable {
		tr.mu.RLock()
		locked = true
		id = tr.resolveRefKeyLocked(id)
		var ok bool
		test, ok = tr.tests[id]
		if !ok {
			tr.mu.RUnlock()
			return nil, errTestGroupSettingsTestNotFound
		}
	}
	index, err := tr.testGroupIndexLocked(test, groupName)
	if err != nil {
		if locked {
			tr.mu.RUnlock()
		}
		return nil, err
	}
	group := test.Groups[index]
	view := &TestGroupParametersView{
		TestID:         test.ID,
		RefKey:         test.RefKey,
		Group:          group.Name,
		GroupIndex:     index,
		ScriptID:       group.ScriptID,
		ScriptName:     group.ScriptName,
		Protocol:       group.Protocol,
		Status:         test.Status,
		Editable:       testGroupSettingsEditableStatus(test.Status),
		EditableReason: testGroupSettingsEditableReason(test.Status),
		Parameters:     []dsl.Parameter{},
		ProjectID:      test.ProjectID,
		TestName:       test.Name,
	}
	var parameterScript *dsl.Script
	var runtimeSettings *RuntimeSettingsOverride
	var dataSources []dsl.DataSource
	if group.script != nil {
		// Detach the small projection inputs while holding the lock, then do
		// disclosure projection and CSV row counting after releasing it. Inline
		// sources can approach the engine's 50 MiB limit; scanning one while
		// holding the runner-wide read lock would stall metrics/lifecycle writers
		// for every test.
		parameterScript = testGroupParameterProjectionSnapshot(group.script)
		dataSources = cloneParameterSummaryDataSources(group.script.DataSources)
	}
	runtimeSettings = cloneRuntimeSettings(group.RuntimeSettings)
	if locked {
		tr.mu.RUnlock()
	}

	revision, err := testGroupEditableSettingsRevision(runtimeSettings, parameterScript)
	if err != nil {
		return nil, err
	}
	view.Revision = revision
	if parameterScript != nil {
		projected, err := projectTestGroupParameters(parameterScript)
		if err != nil {
			return nil, fmt.Errorf("project group parameters: %w", err)
		}
		view.Parameters = projected
		if summarize != nil {
			view.DataSources = summarize(dataSources)
		}
	}
	return view, nil
}

func (tr *TestRunner) UpdateTestGroupParameters(id string, groupName string, expectedRevision string, parameters []dsl.Parameter) (*TestGroupParametersUpdateResult, error) {
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return nil, errTestGroupSettingsRevisionRequired
	}
	if len(parameters) > maxTestGroupParameters {
		return nil, fmt.Errorf("a group supports at most %d parameters (got %d)", maxTestGroupParameters, len(parameters))
	}

	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return nil, err
	}
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok {
		return nil, errTestGroupSettingsTestNotFound
	}
	if !testGroupSettingsEditableStatus(test.Status) {
		return nil, &testGroupSettingsNotEditableError{status: test.Status}
	}
	index, err := tr.testGroupIndexLocked(test, groupName)
	if err != nil {
		return nil, err
	}
	group := &test.Groups[index]
	if group.script == nil {
		return nil, fmt.Errorf("group %q has no compiled script; revise the queued run instead", group.Name)
	}
	currentRevision, err := requireTestGroupSettingsRevision(expectedRevision, group.RuntimeSettings, group.script)
	if err != nil {
		return nil, err
	}
	// Claim the durable execution fence only after optimistic concurrency has
	// admitted the request.
	claimedFence, err := tr.claimPassiveExecutionFenceLocked(test, nil)
	if err != nil {
		return nil, err
	}
	if claimedFence {
		defer releasePassiveExecutionFenceLocked(test)
	}
	// The Java Vuser authoring-approval gate pins the approved script's content
	// hash, which includes parameters. Rewriting them here would silently
	// invalidate the approval and block the run at its start gate; refuse with
	// the recoverable path instead.
	if scriptRequiresJavaVuserAuthoringApproval(group.script) {
		return nil, fmt.Errorf("group %q runs an approval-gated Java Vuser script; parameter changes alter the approved content and must go through the script editor and authoring re-approval", group.Name)
	}

	next := cloneScriptParameters(parameters)
	if err := validateTestGroupParameterIdentity(group.script.Parameters, next); err != nil {
		return nil, err
	}
	newScript := cloneScriptForStoredGroup(group.script)
	if newScript == nil {
		return nil, fmt.Errorf("group %q script snapshot could not be cloned", group.Name)
	}
	// The read endpoint returns disclosure-safe parameters, so protected fields
	// arrive as [REDACTED] (or another sentinel-free projection). Reconcile each
	// field against the prior projection: a protected field must be echoed
	// unchanged and is restored from the execution copy, while an unrelated
	// sibling field may still be edited. The generic whole-script reconciler is
	// intentionally stricter and rejects any change to an enclosing parameter
	// object, which would make the focused run editor unusable for safe sibling
	// changes such as selection strategy.
	reconciledParameters, err := reconcileProjectedTestGroupParameters(next, group.script)
	if err != nil {
		return nil, fmt.Errorf("reconcile projected parameters: %w", err)
	}
	newScript.Parameters = reconciledParameters
	next = cloneScriptParameters(reconciledParameters)
	if err := rejectCredentialShapedParameterEdits(group.script.Parameters, next); err != nil {
		return nil, err
	}
	if issues := validateStoredExecutionScript(newScript); len(issues) > 0 {
		return nil, fmt.Errorf("parameters are invalid: %s", strings.Join(issues, "; "))
	}
	var newBase *dsl.Script
	if group.baseScript != nil {
		newBase = cloneScriptForStoredGroup(group.baseScript)
		if newBase != nil {
			// The base script is the pre-override authoring input: parameters the
			// user did not touch must keep the base's own definitions (which may
			// still carry {{env.*}} templates the compiled copy has substituted),
			// so only genuinely edited or added parameters take the literal values
			// the user saw in the compiled view.
			newBase.Parameters = baseParametersAfterEdit(newBase.Parameters, group.script.Parameters, next)
		}
	}

	changes, err := parameterFieldChanges(group.script, newScript)
	if err != nil {
		return nil, fmt.Errorf("project parameter audit changes: %w", err)
	}
	if len(changes) == 0 {
		projected, err := projectTestGroupParameters(group.script)
		if err != nil {
			return nil, fmt.Errorf("project group parameters: %w", err)
		}
		return &TestGroupParametersUpdateResult{
			TestID:     test.ID,
			Group:      group.Name,
			Changed:    []string{},
			NoChanges:  true,
			Parameters: projected,
			Revision:   currentRevision,
			ProjectID:  test.ProjectID,
			TestName:   test.Name,
		}, nil
	}

	previousScript := group.script
	previousBase := group.baseScript
	previousStartAuthorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	previousValidatedShakeoutPass := test.validatedScriptShakeoutPass
	previousValidatedShakeoutTerm := test.validatedScriptShakeoutTerm
	var previousSpecScript *dsl.Script
	group.script = newScript
	if newBase != nil {
		group.baseScript = newBase
	}
	if index < len(test.groupSpecs) {
		previousSpecScript = test.groupSpecs[index].Script
		test.groupSpecs[index].Script = newScript
	}
	clearScriptShakeoutAuthorizations(test)
	if err := tr.saveManagedTestRecord(test); err != nil {
		group.script = previousScript
		group.baseScript = previousBase
		if index < len(test.groupSpecs) {
			test.groupSpecs[index].Script = previousSpecScript
		}
		test.StartAuthorization = previousStartAuthorization
		test.validatedScriptShakeoutPass = previousValidatedShakeoutPass
		test.validatedScriptShakeoutTerm = previousValidatedShakeoutTerm
		return nil, fmt.Errorf("%w: %v", errTestGroupSettingsPersist, err)
	}

	changed := make([]string, 0, len(changes))
	for name := range changes {
		changed = append(changed, name)
	}
	sortStrings(changed)
	projected, err := projectTestGroupParameters(newScript)
	if err != nil {
		// Projection is expected to be infallible for a script that was just
		// validated and persisted, but fail closed rather than returning the raw
		// execution parameters if an unexpected value defeats JSON projection.
		return nil, fmt.Errorf("project updated group parameters: %w", err)
	}
	newRevision, err := testGroupEditableSettingsRevision(group.RuntimeSettings, newScript)
	if err != nil {
		return nil, err
	}
	return &TestGroupParametersUpdateResult{
		TestID:     test.ID,
		Group:      group.Name,
		Changed:    changed,
		Changes:    changes,
		Parameters: projected,
		Revision:   newRevision,
		ProjectID:  test.ProjectID,
		TestName:   test.Name,
	}, nil
}

// validateTestGroupParameterIdentity enforces the run-edit contract: existing
// parameters may be edited and new ones appended, but a definition the script
// may reference cannot be removed or renamed from the run view.
func validateTestGroupParameterIdentity(current, next []dsl.Parameter) error {
	nextNames := map[string]bool{}
	for i, param := range next {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			return fmt.Errorf("parameters[%d]: name is required", i)
		}
		if len(name) > maxTestGroupParameterNameBytes {
			return fmt.Errorf("parameters[%d]: name must not exceed %d bytes", i, maxTestGroupParameterNameBytes)
		}
		normalized := strings.ToLower(name)
		if nextNames[normalized] {
			return fmt.Errorf("parameters[%d] %q: duplicate name", i, name)
		}
		nextNames[normalized] = true
	}
	for _, param := range current {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		if !nextNames[strings.ToLower(name)] {
			return fmt.Errorf("parameter %q cannot be removed or renamed from the command center; edit the script instead", name)
		}
	}
	return nil
}

// rejectCredentialShapedParameterEdits refuses edits to credential-named
// parameters and additions/replacements whose values themselves look like
// credentials. The durable library already refuses to store these without
// logical secret bindings; letting the run view accept a neutral name with a
// bearer token would reintroduce plaintext credential flow the create path
// blocks. Unchanged value lists pass through so a safe sibling-field edit can
// preserve credential-bearing imported execution snapshots.
func rejectCredentialShapedParameterEdits(current, next []dsl.Parameter) error {
	currentByName := map[string]dsl.Parameter{}
	for _, param := range current {
		key := strings.ToLower(strings.TrimSpace(param.Name))
		if key == "" {
			continue
		}
		if _, exists := currentByName[key]; !exists {
			currentByName[key] = param
		}
	}
	for _, param := range next {
		prior, existed := currentByName[strings.ToLower(strings.TrimSpace(param.Name))]
		if existed && reflect.DeepEqual(prior, param) {
			continue
		}
		if sensitiveEnvKey(param.Name) {
			return fmt.Errorf("parameter %q is credential-shaped; manage credential values through secret bindings in the script editor, not the command center", param.Name)
		}
		if !existed || !reflect.DeepEqual(prior.Values, param.Values) {
			for _, value := range param.Values {
				if sensitiveEnvValue(value) {
					return fmt.Errorf("parameter %q contains a credential-shaped value; manage credential values through secret bindings in the script editor, not the command center", param.Name)
				}
			}
		}
	}
	return nil
}

// reconcileProjectedTestGroupParameters performs a field-level three-way merge:
// raw prior + disclosure projection + incoming edit. If projection changed a
// field, the client must return that field exactly as projected; only then is
// the raw value restored. This permits a safe sibling-field edit without
// allowing a redacted value list to be replaced, reordered, or partially
// reconstructed.
func reconcileProjectedTestGroupParameters(incoming []dsl.Parameter, priorScript *dsl.Script) ([]dsl.Parameter, error) {
	if priorScript == nil {
		return nil, fmt.Errorf("prior script is required")
	}
	projectedParameters, err := projectTestGroupParameters(priorScript)
	if err != nil {
		return nil, err
	}
	indexByName := func(parameters []dsl.Parameter) map[string]dsl.Parameter {
		out := make(map[string]dsl.Parameter, len(parameters))
		for _, param := range parameters {
			key := strings.ToLower(strings.TrimSpace(param.Name))
			if key == "" {
				continue
			}
			if _, exists := out[key]; !exists {
				out[key] = param
			}
		}
		return out
	}
	rawByName := indexByName(priorScript.Parameters)
	projectedByName := indexByName(projectedParameters)
	out := cloneScriptParameters(incoming)
	for index := range out {
		key := strings.ToLower(strings.TrimSpace(out[index].Name))
		raw, hadRaw := rawByName[key]
		projected, hadProjection := projectedByName[key]
		if !hadRaw || !hadProjection {
			if parameterContainsMaskedSentinel(out[index]) {
				return nil, fmt.Errorf("parameter %q contains an orphan %s sentinel", out[index].Name, maskedScriptSecret)
			}
			continue
		}
		if err := reconcileProjectedParameterFields(&out[index], raw, projected); err != nil {
			return nil, fmt.Errorf("parameter %q: %w", out[index].Name, err)
		}
	}
	return cloneScriptParameters(out), nil
}

func reconcileProjectedParameterFields(incoming *dsl.Parameter, raw, projected dsl.Parameter) error {
	if incoming == nil {
		return fmt.Errorf("incoming parameter is required")
	}
	incomingValue := reflect.ValueOf(incoming).Elem()
	rawValue := reflect.ValueOf(raw)
	projectedValue := reflect.ValueOf(projected)
	parameterType := incomingValue.Type()
	for field := 0; field < incomingValue.NumField(); field++ {
		structField := parameterType.Field(field)
		if structField.PkgPath != "" {
			continue
		}
		rawField := rawValue.Field(field).Interface()
		projectedField := projectedValue.Field(field).Interface()
		if reflect.DeepEqual(rawField, projectedField) {
			incomingField := incomingValue.Field(field).Interface()
			if !reflect.DeepEqual(incomingField, rawField) && valueContainsMaskedSentinel(incomingField) {
				return fmt.Errorf("field %s contains an orphan %s sentinel", structField.Name, maskedScriptSecret)
			}
			continue
		}
		if !reflect.DeepEqual(incomingValue.Field(field).Interface(), projectedField) {
			return fmt.Errorf("protected field %s changed; revise credential-backed values in the script editor", structField.Name)
		}
		incomingValue.Field(field).Set(rawValue.Field(field))
	}
	return nil
}

func parameterContainsMaskedSentinel(param dsl.Parameter) bool {
	return valueContainsMaskedSentinel(param)
}

func valueContainsMaskedSentinel(value any) bool {
	payload, err := json.Marshal(value)
	return err != nil || strings.Contains(string(payload), maskedScriptSecret)
}

// baseParametersAfterEdit projects a parameters edit made against the compiled
// view back onto the base script with a field-wise three-way merge. Fields that
// still equal the compiled view retain the base value (including unresolved
// {{env.*}} templates); only fields the operator actually changed take the
// submitted value. Copying the whole submitted parameter after a sibling-field
// edit would otherwise replace a base template with its compiled plaintext.
func baseParametersAfterEdit(base, compiled, next []dsl.Parameter) []dsl.Parameter {
	indexByName := func(parameters []dsl.Parameter) map[string]dsl.Parameter {
		byName := map[string]dsl.Parameter{}
		for _, param := range parameters {
			key := strings.ToLower(strings.TrimSpace(param.Name))
			if key == "" {
				continue
			}
			if _, exists := byName[key]; !exists {
				byName[key] = param
			}
		}
		return byName
	}
	baseByName := indexByName(base)
	compiledByName := indexByName(compiled)
	out := make([]dsl.Parameter, 0, len(next))
	for _, param := range next {
		key := strings.ToLower(strings.TrimSpace(param.Name))
		compiledPrior, inCompiled := compiledByName[key]
		basePrior, inBase := baseByName[key]
		if inCompiled && inBase {
			out = append(out, mergeParameterFieldsOntoBase(basePrior, compiledPrior, param))
			continue
		}
		out = append(out, param)
	}
	return cloneScriptParameters(out)
}

func mergeParameterFieldsOntoBase(base, compiled, next dsl.Parameter) dsl.Parameter {
	merged := base
	mergedValue := reflect.ValueOf(&merged).Elem()
	compiledValue := reflect.ValueOf(compiled)
	nextValue := reflect.ValueOf(next)
	for field := 0; field < nextValue.NumField(); field++ {
		if nextValue.Type().Field(field).PkgPath != "" {
			continue
		}
		if reflect.DeepEqual(nextValue.Field(field).Interface(), compiledValue.Field(field).Interface()) {
			continue
		}
		mergedValue.Field(field).Set(nextValue.Field(field))
	}
	return merged
}

func cloneScriptParameters(parameters []dsl.Parameter) []dsl.Parameter {
	if parameters == nil {
		return nil
	}
	out := make([]dsl.Parameter, len(parameters))
	for i, param := range parameters {
		cloned := param
		cloned.Values = append([]string(nil), param.Values...)
		if param.Seed != nil {
			seed := *param.Seed
			cloned.Seed = &seed
		}
		out[i] = cloned
	}
	return out
}

// testGroupParameterProjectionSnapshot captures only the script fields that
// influence parameter disclosure. It avoids cloning actions and potentially
// huge inline data sources while preserving importer credential provenance.
func testGroupParameterProjectionSnapshot(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	snapshot := &dsl.Script{Parameters: cloneScriptParameters(script.Parameters)}
	if script.Metadata == nil || script.Metadata.Import == nil {
		return snapshot
	}
	names := importPreservedStringSlice(
		script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey],
	)
	if len(names) == 0 {
		return snapshot
	}
	snapshot.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Preserved: map[string]any{
			dsl.ImportPreservedCredentialParameterNamesKey: names,
		},
	}}
	return snapshot
}

func cloneParameterSummaryDataSources(sources []dsl.DataSource) []dsl.DataSource {
	if sources == nil {
		return nil
	}
	out := make([]dsl.DataSource, len(sources))
	for index, source := range sources {
		out[index] = dsl.DataSource{
			Name:           source.Name,
			Type:           source.Type,
			InlineData:     source.InlineData,
			Encoding:       source.Encoding,
			Delimiter:      source.Delimiter,
			FirstRowHeader: source.FirstRowHeader,
			Columns:        append([]string(nil), source.Columns...),
		}
	}
	return out
}

func projectTestGroupParameters(script *dsl.Script) ([]dsl.Parameter, error) {
	if script == nil {
		return []dsl.Parameter{}, nil
	}
	projected := maskScriptSecrets(testGroupParameterProjectionSnapshot(script))
	if projected == nil {
		return nil, fmt.Errorf("script secret projection failed")
	}
	// The general script projector uses semantic names/import provenance. The
	// focused run editor also rejects value-shaped credentials, so its read
	// surface must fail closed for legacy execution snapshots that predate that
	// write rule and carry (for example) a bearer token under a neutral name.
	for parameterIndex := range projected.Parameters {
		if parameterIndex >= len(script.Parameters) {
			break
		}
		raw := script.Parameters[parameterIndex]
		for valueIndex, value := range raw.Values {
			if valueIndex >= len(projected.Parameters[parameterIndex].Values) ||
				value == "" || isPortableTemplateBinding(value) ||
				strings.Contains(value, maskedScriptSecret) ||
				!sensitiveEnvValue(value) {
				continue
			}
			projected.Parameters[parameterIndex].Values[valueIndex] = projectSensitiveScriptValue(value)
		}
	}
	return cloneScriptParameters(projected.Parameters), nil
}

func summarizeParameterDataSources(sources []dsl.DataSource) []TestGroupParameterDataSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]TestGroupParameterDataSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, TestGroupParameterDataSource{
			Name:     source.Name,
			Type:     source.Type,
			Columns:  append([]string(nil), source.Columns...),
			RowCount: inlineDataRowCount(source),
		})
	}
	return out
}

// inlineDataRowCount counts data rows in an inline CSV/DAT source (excluding a
// declared header row). Path- or asset-backed sources report zero: the dialog
// only needs shape, not a storage read.
func inlineDataRowCount(source dsl.DataSource) int {
	if strings.TrimSpace(source.InlineData) == "" || strings.EqualFold(strings.TrimSpace(source.Encoding), "base64") {
		return 0
	}

	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	rows := 0
	for {
		_, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// The DSL validator reports malformed tabular data. Avoid presenting
			// a plausible-but-wrong count for an imported legacy script that has
			// not yet passed validation.
			return 0
		}
		rows++
	}
	if source.FirstRowHeader && rows > 0 {
		rows--
	}
	return rows
}

// runtimeSettingsFieldChanges returns the masked {from,to} pair for every
// requested field whose effective value actually changed. Change detection
// compares the UNMASKED values: masking first would collapse a credential-only
// edit (stored protected reference vs new plaintext) into the same redaction
// sentinel on both sides and silently swallow the update as a no-op. Only the
// emitted from/to payload is masked.
func runtimeSettingsFieldChanges(before, after *RuntimeSettingsOverride, fields []string) map[string]any {
	maskedBefore := maskRuntimeSettings(cloneRuntimeSettings(before))
	maskedAfter := maskRuntimeSettings(cloneRuntimeSettings(after))
	changes := map[string]any{}
	for _, field := range fields {
		if reflect.DeepEqual(runtimeSettingsFieldValue(before, field), runtimeSettingsFieldValue(after, field)) {
			continue
		}
		change := map[string]any{}
		if fromValue := runtimeSettingsFieldValue(maskedBefore, field); fromValue != nil {
			change["from"] = fromValue
		}
		if toValue := runtimeSettingsFieldValue(maskedAfter, field); toValue != nil {
			change["to"] = toValue
		}
		changes[field] = change
	}
	return changes
}

// runtimeSettingsFieldValue extracts the effective value of one override field,
// resolving legacy aliases the same way execution does (section wins).
func runtimeSettingsFieldValue(settings *RuntimeSettingsOverride, field string) any {
	if settings == nil {
		return nil
	}
	switch field {
	case "iterations":
		if value, present := runtimeSettingsIterationValue(settings); present {
			return value
		}
		return nil
	case "pacing":
		return nilIfNilPointer(settings.Pacing)
	case "think_time":
		return nilIfNilPointer(settings.ThinkTime)
	case "error_handling":
		return nilIfNilPointer(settings.ErrorHandling)
	case "browser":
		if settings.BrowserConfig != nil {
			return settings.BrowserConfig
		}
		return nilIfNilPointer(settings.Browser)
	case "log_level":
		if settings.Log != nil {
			return settings.Log.Level
		}
		if settings.LogLevel == "" {
			return nil
		}
		return settings.LogLevel
	case "auto_transactions":
		if settings.RunLogic != nil {
			return settings.RunLogic.AutoTransactions
		}
		if settings.AutoTransactions == "" {
			return nil
		}
		return settings.AutoTransactions
	case "base_url":
		if settings.BaseURL == "" {
			return nil
		}
		return settings.BaseURL
	case "results":
		return nilIfNilPointer(settings.Results)
	case "log":
		return nilIfNilPointer(settings.Log)
	case "run_logic":
		return nilIfNilPointer(settings.RunLogic)
	case "http":
		return nilIfNilPointer(settings.HTTP)
	case "websocket":
		return nilIfNilPointer(settings.WebSocket)
	case "web_runtime":
		return nilIfNilPointer(settings.WebRuntime)
	case "browser_config":
		return nilIfNilPointer(settings.BrowserConfig)
	case "browser_emulation":
		return nilIfNilPointer(settings.BrowserEmulation)
	case "java_vuser":
		return nilIfNilPointer(settings.JavaVuser)
	}
	return nil
}

// parameterFieldChanges maps each edited or added parameter name to a
// compacted {from,to} pair suitable for audit details. Change detection uses
// the raw execution copies so a credential rotation cannot collapse into a
// no-op after projection; emitted values come from the disclosure-safe
// projections so importer-provenance credentials with neutral names cannot
// leak into audit events or the run manifest.
func parameterFieldChanges(beforeScript, afterScript *dsl.Script) (map[string]any, error) {
	if beforeScript == nil || afterScript == nil {
		return nil, fmt.Errorf("before and after scripts are required")
	}
	projectedBefore, err := projectTestGroupParameters(beforeScript)
	if err != nil {
		return nil, fmt.Errorf("project prior parameters: %w", err)
	}
	projectedAfter, err := projectTestGroupParameters(afterScript)
	if err != nil {
		return nil, fmt.Errorf("project updated parameters: %w", err)
	}
	before := beforeScript.Parameters
	after := afterScript.Parameters
	byName := map[string]dsl.Parameter{}
	for _, param := range before {
		name := strings.ToLower(strings.TrimSpace(param.Name))
		if name == "" {
			continue
		}
		if _, exists := byName[name]; !exists {
			byName[name] = param
		}
	}
	projectedByName := func(parameters []dsl.Parameter) map[string]dsl.Parameter {
		out := make(map[string]dsl.Parameter, len(parameters))
		for _, param := range parameters {
			name := strings.ToLower(strings.TrimSpace(param.Name))
			if name == "" {
				continue
			}
			if _, exists := out[name]; !exists {
				out[name] = param
			}
		}
		return out
	}
	projectedBeforeByName := projectedByName(projectedBefore)
	projectedAfterByName := projectedByName(projectedAfter)
	changes := map[string]any{}
	for _, param := range after {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		prior, existed := byName[strings.ToLower(name)]
		if existed && reflect.DeepEqual(prior, param) {
			continue
		}
		projectedParam := param
		if safe, ok := projectedAfterByName[strings.ToLower(name)]; ok {
			projectedParam = safe
		}
		change := map[string]any{"to": compactParameterForAudit(projectedParam)}
		if existed {
			projectedPrior := prior
			if safe, ok := projectedBeforeByName[strings.ToLower(name)]; ok {
				projectedPrior = safe
			}
			change["from"] = compactParameterForAudit(projectedPrior)
		}
		changes[name] = change
	}
	return changes, nil
}

// compactParameterForAudit truncates long value lists so audit events and the
// run manifest delta stay readable and bounded, and scrubs values that look
// like credential material — the audit stream and the viewer-facing manifest
// must never carry secrets even when a parameter is (mis)used to hold one.
func compactParameterForAudit(param dsl.Parameter) map[string]any {
	out := map[string]any{"type": boundedTestGroupAuditValue(param.Type)}
	if param.Update != "" {
		out["update"] = boundedTestGroupAuditValue(param.Update)
	}
	if param.Selection != "" {
		out["selection"] = boundedTestGroupAuditValue(param.Selection)
	}
	if param.OnExhaustion != "" {
		out["on_exhaustion"] = boundedTestGroupAuditValue(param.OnExhaustion)
	}
	if param.DataSource != "" {
		out["data_source"] = boundedTestGroupAuditValue(param.DataSource)
	}
	if param.Column != "" {
		out["column"] = boundedTestGroupAuditValue(param.Column)
	}
	if len(param.Values) > 0 {
		sensitiveName := sensitiveEnvKey(param.Name)
		values := make([]string, 0, testGroupSettingsAuditValueCap+1)
		for index, value := range param.Values {
			if index == testGroupSettingsAuditValueCap {
				values = append(values, fmt.Sprintf(auditParameterValueOverflowNote, len(param.Values)-testGroupSettingsAuditValueCap))
				break
			}
			if sensitiveName || sensitiveEnvValue(value) {
				values = append(values, scrub.Redacted)
				continue
			}
			values = append(values, boundedTestGroupAuditValue(value))
		}
		out["values"] = values
	}
	return out
}

func boundedTestGroupAuditValue(value string) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= testGroupSettingsAuditValueBytes {
		return value
	}
	const suffix = "…"
	maxPrefixBytes := testGroupSettingsAuditValueBytes - len(suffix)
	prefix := value[:maxPrefixBytes]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix + suffix
}

func nilIfNilPointer[T any](value *T) any {
	if value == nil {
		return nil
	}
	return value
}

func sortStrings(values []string) {
	sort.Strings(values)
}

func writeTestGroupSettingsError(w http.ResponseWriter, err error) {
	var notEditable *testGroupSettingsNotEditableError
	var revisionConflict *testGroupSettingsRevisionConflictError
	switch {
	case errors.Is(err, errTestGroupSettingsTestNotFound):
		writeError(w, http.StatusNotFound, "test not found")
	case errors.Is(err, errTestGroupSettingsGroupNotFound):
		writeError(w, http.StatusNotFound, "group not found")
	case errors.As(err, &notEditable):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &revisionConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errNotExecutionLeader):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, errTestGroupSettingsPersist):
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// OpenAPI-Responses: 404, 500
func (s *Server) handleGetTestGroupRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	view, err := s.runner.GetTestGroupRuntimeSettingsView(r.PathValue("id"), r.PathValue("group"))
	if err != nil {
		writeTestGroupSettingsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// OpenAPI-Responses: 404, 409, 500, 503
func (s *Server) handleUpdateTestGroupRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req struct {
		RuntimeSettings  *RuntimeSettingsOverride `json:"runtime_settings"`
		Fields           []string                 `json:"fields"`
		ExpectedRevision string                   `json:"expected_revision"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ExpectedRevision) == "" {
		writeError(w, http.StatusBadRequest, errTestGroupSettingsRevisionRequired.Error())
		return
	}
	normalizedFields, err := normalizeCommandCenterRuntimeFieldKeys(req.Fields)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(normalizedFields) == 0 {
		writeError(w, http.StatusBadRequest, "at least one runtime settings field is required")
		return
	}
	req.Fields = normalizedFields
	auditView, err := s.runner.GetTestGroupRuntimeSettingsView(r.PathValue("id"), r.PathValue("group"))
	if err != nil {
		writeTestGroupSettingsError(w, err)
		return
	}
	auditDetails := map[string]any{
		"name":   auditView.TestName,
		"group":  auditView.Group,
		"fields": append([]string(nil), req.Fields...),
	}
	operationID, err := s.beginRequiredAuditOperation(
		r,
		"test.runtime_settings.update",
		"test",
		auditView.TestID,
		auditView.ProjectID,
		auditDetails,
	)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "runtime settings audit is unavailable; the run was not changed")
		return
	}
	result, err := s.runner.UpdateTestGroupRuntimeSettings(r.PathValue("id"), r.PathValue("group"), req.ExpectedRevision, req.RuntimeSettings, req.Fields)
	if err != nil {
		if auditErr := s.completeRequiredAuditOperation(
			r,
			operationID,
			"test.runtime_settings.update",
			"failed",
			"group_settings_update_failed",
			"test",
			auditView.TestID,
			auditView.ProjectID,
			auditDetails,
		); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "runtime settings audit outcome could not be persisted")
			return
		}
		writeTestGroupSettingsError(w, err)
		return
	}
	outcome := "updated"
	if result.NoChanges {
		outcome = "no_changes"
	}
	auditDetails = map[string]any{
		"name":    result.TestName,
		"group":   result.Group,
		"fields":  result.Fields,
		"changes": result.Changes,
	}
	if auditErr := s.completeRequiredAuditOperation(
		r,
		operationID,
		"test.runtime_settings.update",
		"success",
		outcome,
		"test",
		result.TestID,
		result.ProjectID,
		auditDetails,
	); auditErr != nil {
		writeError(w, http.StatusServiceUnavailable, "runtime settings were updated but the audit outcome could not be persisted; reload the run before retrying")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// OpenAPI-Responses: 404, 500
func (s *Server) handleGetTestGroupParameters(w http.ResponseWriter, r *http.Request) {
	view, err := s.runner.GetTestGroupParametersView(r.PathValue("id"), r.PathValue("group"))
	if err != nil {
		writeTestGroupSettingsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// OpenAPI-Responses: 404, 409, 500, 503
func (s *Server) handleUpdateTestGroupParameters(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req struct {
		Parameters       []dsl.Parameter `json:"parameters"`
		ExpectedRevision string          `json:"expected_revision"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ExpectedRevision) == "" {
		writeError(w, http.StatusBadRequest, errTestGroupSettingsRevisionRequired.Error())
		return
	}
	auditView, err := s.runner.GetTestGroupParametersView(r.PathValue("id"), r.PathValue("group"))
	if err != nil {
		writeTestGroupSettingsError(w, err)
		return
	}
	auditDetails := map[string]any{
		"name":  auditView.TestName,
		"group": auditView.Group,
	}
	operationID, err := s.beginRequiredAuditOperation(
		r,
		"test.parameters.update",
		"test",
		auditView.TestID,
		auditView.ProjectID,
		auditDetails,
	)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "parameter audit is unavailable; the run was not changed")
		return
	}
	result, err := s.runner.UpdateTestGroupParameters(r.PathValue("id"), r.PathValue("group"), req.ExpectedRevision, req.Parameters)
	if err != nil {
		if auditErr := s.completeRequiredAuditOperation(
			r,
			operationID,
			"test.parameters.update",
			"failed",
			"group_settings_update_failed",
			"test",
			auditView.TestID,
			auditView.ProjectID,
			auditDetails,
		); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "parameter audit outcome could not be persisted")
			return
		}
		writeTestGroupSettingsError(w, err)
		return
	}
	outcome := "updated"
	if result.NoChanges {
		outcome = "no_changes"
	}
	auditDetails = map[string]any{
		"name":       result.TestName,
		"group":      result.Group,
		"parameters": result.Changed,
		"changes":    result.Changes,
	}
	if auditErr := s.completeRequiredAuditOperation(
		r,
		operationID,
		"test.parameters.update",
		"success",
		outcome,
		"test",
		result.TestID,
		result.ProjectID,
		auditDetails,
	); auditErr != nil {
		writeError(w, http.StatusServiceUnavailable, "parameters were updated but the audit outcome could not be persisted; reload the run before retrying")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
