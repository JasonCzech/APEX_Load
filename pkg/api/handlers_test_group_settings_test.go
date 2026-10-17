package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func parameterizedHTTPScript(id, name string) *dsl.Script {
	script := httpScript(id, name)
	script.DataSources = []dsl.DataSource{{
		Name:           "accounts",
		Type:           "csv",
		InlineData:     "user,region\nu1,east\nu2,west",
		Strategy:       "sequential",
		FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{
		{Name: "AccountUser", Type: "file", DataSource: "accounts", Column: "user", Update: "each_iteration", Selection: "sequential", OnExhaustion: "wrap"},
		{Name: "Region", Type: "list", Values: []string{"east", "west"}, Update: "once", Selection: "random"},
	}
	return script
}

func mustGroupSettingsRevision(t *testing.T, runner *TestRunner, testID, group string) string {
	t.Helper()
	view, err := runner.GetTestGroupRuntimeSettingsView(testID, group)
	if err != nil {
		t.Fatalf("GetTestGroupRuntimeSettingsView revision: %v", err)
	}
	if strings.TrimSpace(view.Revision) == "" {
		t.Fatal("group settings revision is empty")
	}
	return view.Revision
}

func TestInlineDataRowCountUsesEngineCSVSemantics(t *testing.T) {
	tests := []struct {
		name   string
		source dsl.DataSource
		want   int
	}{
		{
			name: "quoted multiline field is one record",
			source: dsl.DataSource{
				Type:           "csv",
				InlineData:     "id,notes\n1,\"line one\nline two\"\n2,plain\n",
				FirstRowHeader: true,
			},
			want: 2,
		},
		{
			name: "auto detected tab delimiter",
			source: dsl.DataSource{
				Type:           "dat",
				Delimiter:      "auto",
				InlineData:     "id\tnotes\n1\tone\n2\ttwo",
				FirstRowHeader: true,
			},
			want: 2,
		},
		{
			name: "blank physical lines are ignored by csv reader",
			source: dsl.DataSource{
				Type:       "csv",
				InlineData: "one\n\n two\n",
				Columns:    []string{"value"},
			},
			want: 2,
		},
		{
			name: "base64 assets are not interpreted as rows",
			source: dsl.DataSource{
				Type:       "file",
				InlineData: "YSxiCjEsMgo=",
				Encoding:   "base64",
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inlineDataRowCount(tt.source); got != tt.want {
				t.Fatalf("inlineDataRowCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBaseParametersAfterEditPreservesTemplatesOnSiblingEdit(t *testing.T) {
	base := []dsl.Parameter{{
		Name:      "SessionValue",
		Type:      "list",
		Values:    []string{"{{env.SESSION_VALUE}}"},
		Update:    "once",
		Selection: "sequential",
	}}
	compiled := cloneScriptParameters(base)
	compiled[0].Values = []string{"resolved-runtime-credential"}
	next := cloneScriptParameters(compiled)
	next[0].Selection = "random"

	got := baseParametersAfterEdit(base, compiled, next)
	if len(got) != 1 {
		t.Fatalf("parameters = %#v, want one", got)
	}
	if !reflect.DeepEqual(got[0].Values, base[0].Values) {
		t.Fatalf("base template was replaced by compiled value: %#v", got[0].Values)
	}
	if got[0].Selection != "random" {
		t.Fatalf("edited sibling field = %q, want random", got[0].Selection)
	}
}

func TestParameterFieldChangesDoesNotTraverseActionsOrInlineData(t *testing.T) {
	before := &dsl.Script{
		Parameters: []dsl.Parameter{{
			Name: "Region", Type: "list", Values: []string{"east", "west"}, Selection: "sequential",
		}},
		Actions: []dsl.Action{{
			Name: "projection poison", Params: map[string]any{"not_json": make(chan struct{})},
		}},
		DataSources: []dsl.DataSource{{
			Name: "large", Type: "csv", InlineData: strings.Repeat("value\n", 1<<18),
		}},
	}
	after := &dsl.Script{
		Parameters:  cloneScriptParameters(before.Parameters),
		Actions:     before.Actions,
		DataSources: before.DataSources,
	}
	after.Parameters[0].Selection = "random"

	changes, err := parameterFieldChanges(before, after)
	if err != nil {
		t.Fatalf("parameterFieldChanges traversed unrelated script payload: %v", err)
	}
	if _, ok := changes["Region"]; !ok {
		t.Fatalf("parameter changes = %#v, want Region", changes)
	}
}

func TestGroupEditableSettingsRevisionIsFocusedDeterministicAndDisclosureSafe(t *testing.T) {
	script := &dsl.Script{
		Parameters: []dsl.Parameter{{
			Name: "SessionValue", Type: "list", Values: []string{"Bearer hidden-revision-token-123456789"},
			Update: "once", Selection: "sequential",
		}},
		Actions: []dsl.Action{{
			Name: "projection poison", Params: map[string]any{"not_json": make(chan struct{})},
		}},
		DataSources: []dsl.DataSource{{
			Name: "large", Type: "csv", InlineData: strings.Repeat("value\n", 1<<18),
		}},
	}
	runtimeSettings := &RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 500}}
	first, err := testGroupEditableSettingsRevision(runtimeSettings, script)
	if err != nil {
		t.Fatalf("revision traversed unrelated script payload: %v", err)
	}
	second, err := testGroupEditableSettingsRevision(runtimeSettings, script)
	if err != nil || second != first {
		t.Fatalf("revision is not deterministic: first=%q second=%q err=%v", first, second, err)
	}
	script.Actions[0].Name = "unrelated change"
	script.DataSources[0].InlineData += "more\n"
	unrelated, err := testGroupEditableSettingsRevision(runtimeSettings, script)
	if err != nil || unrelated != first {
		t.Fatalf("unrelated actions/data changed focused revision: got=%q want=%q err=%v", unrelated, first, err)
	}
	script.Parameters[0].Selection = "random"
	changed, err := testGroupEditableSettingsRevision(runtimeSettings, script)
	if err != nil || changed == first {
		t.Fatalf("parameter change did not advance revision: got=%q first=%q err=%v", changed, first, err)
	}
}

func createQueuedGroupSettingsTest(t *testing.T, server *Server) *ManagedTest {
	t.Helper()
	server.runner.StoreScript(parameterizedHTTPScript("script-a", "Checkout"))
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Command Center Run",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "Buyers",
			ScriptID: "script-a",
			VUsers:   5,
			RuntimeSettings: &RuntimeSettingsOverride{
				LogLevel: "debug",
				Pacing:   &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 500},
			},
		}},
		RuntimeSettings: &RuntimeSettingsOverride{ThinkTime: &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 250}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if test.Status != "QUEUED" {
		t.Fatalf("created test status = %q, want QUEUED", test.Status)
	}
	return test
}

// TestCommandCenterRuntimeFieldCoverage is a maintenance guard: adding a new
// top-level section to RuntimeSettingsOverride (or a new key to
// runtimeOverrideFieldKeys) requires matching manual cases in
// replaceRuntimeFields and runtimeSettingsFieldValue, plus the dialog's
// EDITABLE_FIELD_KEYS (enforced by a tsc-level check on the frontend). This
// test fails with the missing site named instead of letting edits to the new
// section silently stop being applied or audited.
func TestCommandCenterRuntimeFieldCoverage(t *testing.T) {
	maxRedirects := 4
	fullyPopulated := &RuntimeSettingsOverride{
		Results:       &dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 1}},
		WebSocket:     &dsl.WebSocketConfig{CallbackIntervalMs: 100},
		Pacing:        &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
		ThinkTime:     &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 100},
		ErrorHandling: &dsl.ErrorHandlingConfig{MaxRetries: 2},
		Log:           &dsl.LogConfig{Level: "debug"},
		RunLogic:      &dsl.RunLogicConfig{Iterations: 3, AutoTransactions: "actions"},
		HTTP:          &dsl.HTTPConfig{MaxRedirects: &maxRedirects},
		WebRuntime:    &dsl.WebRuntimeConfig{},
		JavaVuser:     &RuntimeJavaVuserOverride{JDKSelection: "internal"},
		BrowserConfig: &dsl.BrowserConfig{DeviceProfile: "desktop"},
		BrowserEmulation: &dsl.BrowserEmulationConfig{
			BrowserType: "chrome",
		},
		BaseURL: "https://coverage.example",
		Env:     map[string]string{"PLAIN": "value"},
	}
	overrideType := reflect.TypeOf(*fullyPopulated)
	jsonFieldNames := map[string]bool{}
	for i := 0; i < overrideType.NumField(); i++ {
		tag := strings.Split(overrideType.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		jsonFieldNames[tag] = true
		if tag == "env" || tag == "redaction_rules" {
			continue
		}
		if !commandCenterRuntimeFieldKeys[tag] {
			t.Errorf("RuntimeSettingsOverride field %q is not command-center editable; add it to runtimeOverrideFieldKeys (or commandCenterRuntimeFieldKeys) with replaceRuntimeFields + runtimeSettingsFieldValue cases and the dialog's EDITABLE_FIELD_KEYS", tag)
		}
	}
	for key := range commandCenterRuntimeFieldKeys {
		if !jsonFieldNames[key] {
			t.Errorf("editable field %q has no RuntimeSettingsOverride JSON field", key)
		}
		if value := runtimeSettingsFieldValue(fullyPopulated, key); value == nil {
			t.Errorf("runtimeSettingsFieldValue has no case for %q; changes to it would vanish from the audit delta", key)
		}
		replaced := replaceRuntimeFields(nil, fullyPopulated, []string{key})
		if runtimeSettingsOverrideEmpty(replaced) {
			t.Errorf("replaceRuntimeFields has no effective case for %q; edits to it would be dropped", key)
		}
	}
}

func TestGetTestGroupRuntimeSettingsViewReturnsLayers(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	view, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("GetTestGroupRuntimeSettingsView: %v", err)
	}
	if !view.Editable || view.EditableReason != "" {
		t.Fatalf("queued run should be editable, got editable=%v reason=%q", view.Editable, view.EditableReason)
	}
	if view.Group != "Buyers" || view.Protocol != "http" {
		t.Fatalf("unexpected identity: %#v", view)
	}
	if view.RuntimeSettings == nil || view.RuntimeSettings.LogLevel != "debug" {
		t.Fatalf("group override layer = %#v, want log_level=debug", view.RuntimeSettings)
	}
	if view.TestRuntimeSettings == nil || view.TestRuntimeSettings.ThinkTime == nil || view.TestRuntimeSettings.ThinkTime.MeanMs != 250 {
		t.Fatalf("test override layer = %#v, want think_time 250ms", view.TestRuntimeSettings)
	}
	if view.EffectiveSettings == nil || view.EffectiveSettings.Pacing == nil || view.EffectiveSettings.Pacing.DelayMs != 500 {
		t.Fatalf("effective layer = %#v, want pacing delay 500", view.EffectiveSettings)
	}
	if view.EffectiveSettings.Log == nil || view.EffectiveSettings.Log.Level != "debug" {
		t.Fatalf("effective log = %#v, want debug", view.EffectiveSettings.Log)
	}
	fieldSet := map[string]bool{}
	for _, field := range view.EditableFields {
		fieldSet[field] = true
	}
	if !fieldSet["pacing"] || !fieldSet["java_vuser"] || fieldSet["env"] {
		t.Fatalf("editable fields = %v, want pacing+java_vuser and no env", view.EditableFields)
	}
}

func TestGetTestGroupRuntimeSettingsViewUsesImmutableBoundedImportProvenance(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := parameterizedHTTPScript("script-imported-runtime", "Imported Runtime")
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source: "loadrunner",
		Preserved: map[string]any{
			"runtime_files": map[string]map[string]map[string]string{
				"default.cfg": {"HTTP": {"ProxyPassword": "source-password", "ConnectTimeout": "45"}},
			},
			"runtime_setting_dispositions": map[string]map[string]map[string]any{
				"default.cfg": {"HTTP": {
					"ConnectTimeout": map[string]any{"status": "mapped", "fields": []string{"config.http.connect_timeout_ms"}},
				}},
			},
		},
	}}
	server.runner.StoreScript(script)
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Immutable import", Duration: "5m",
		Groups: []CreateGroupEntry{{Name: "Buyers", ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}

	mutated := parameterizedHTTPScript(script.ID, "Mutable Library Copy")
	mutated.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
		"runtime_files": map[string]map[string]map[string]string{
			"default.cfg": {"HTTP": {"ConnectTimeout": "999"}},
		},
	}}}
	server.runner.StoreScript(mutated)

	view, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil {
		t.Fatal(err)
	}
	source := view.ImportedRuntimeSource
	if source == nil || source.Files["default.cfg"]["HTTP"]["ConnectTimeout"] != "45" {
		t.Fatalf("runtime provenance followed mutable library script: %#v", source)
	}
	payload, _ := json.Marshal(source)
	if strings.Contains(string(payload), "source-password") || !strings.Contains(string(payload), scrub.Redacted) {
		t.Fatalf("immutable provenance was not disclosure-safe: %s", payload)
	}
}

func TestGroupSettingsViewsRefreshMissingAndStaleHAState(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	leadership := newExecutionLeadership(store.(executionLeaseStore), "group-settings-follower")
	follower := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership))

	test := createQueuedGroupSettingsTest(t, server)
	runtimeView, err := follower.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("runtime view did not refresh missing HA test: %v", err)
	}
	parameterView, err := follower.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil || len(parameterView.Parameters) != 2 {
		t.Fatalf("parameter view did not observe refreshed HA test: view=%#v err=%v", parameterView, err)
	}

	_, err = server.runner.UpdateTestGroupRuntimeSettings(
		test.ID, "Buyers", runtimeView.Revision,
		&RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 2222}},
		[]string{"pacing"},
	)
	if err != nil {
		t.Fatalf("leader runtime update: %v", err)
	}
	latest, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatal(err)
	}
	parameters := cloneScriptParameters(latest.Parameters)
	parameters[1].Selection = "sequential"
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", latest.Revision, parameters); err != nil {
		t.Fatalf("leader parameter update: %v", err)
	}

	follower.haTestRefresh.expire()
	refreshedParameters, err := follower.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil || refreshedParameters.Parameters[1].Selection != "sequential" {
		t.Fatalf("parameter view retained stale HA state: view=%#v err=%v", refreshedParameters, err)
	}
	refreshedRuntime, err := follower.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil || refreshedRuntime.RuntimeSettings.Pacing.DelayMs != 2222 {
		t.Fatalf("runtime view retained stale HA state: view=%#v err=%v", refreshedRuntime, err)
	}
}

func TestUpdateTestGroupRuntimeSettingsAppliesPreStart(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	result, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		Pacing:    &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 2000},
		ThinkTime: &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 900},
	}, []string{"pacing", "think_time"})
	if err != nil {
		t.Fatalf("UpdateTestGroupRuntimeSettings: %v", err)
	}
	if result.NoChanges {
		t.Fatalf("expected changes, got no_changes")
	}
	if len(result.Fields) != 2 || result.Fields[0] != "pacing" || result.Fields[1] != "think_time" {
		t.Fatalf("changed fields = %v, want [pacing think_time]", result.Fields)
	}
	if _, ok := result.Changes["pacing"]; !ok {
		t.Fatalf("changes missing pacing entry: %#v", result.Changes)
	}

	// The in-memory run must execute the new profile.
	managed, ok := server.runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("test disappeared")
	}
	group := managed.Groups[0]
	if group.RuntimeSettings == nil || group.RuntimeSettings.Pacing == nil || group.RuntimeSettings.Pacing.DelayMs != 2000 {
		t.Fatalf("group override = %#v, want pacing 2000", group.RuntimeSettings)
	}
	if group.script == nil || group.script.Config.Pacing.DelayMs != 2000 {
		t.Fatalf("compiled script pacing = %#v, want 2000", group.script.Config.Pacing)
	}
	if group.script.Config.ThinkTime.MeanMs != 900 {
		t.Fatalf("compiled script think time = %#v, want 900", group.script.Config.ThinkTime)
	}
	// The engine spec the launch will clone must reference the recompiled script.
	server.runner.mu.RLock()
	spec := server.runner.tests[test.ID].groupSpecs[0]
	server.runner.mu.RUnlock()
	if spec.Script == nil || spec.Script.Config.Pacing.DelayMs != 2000 {
		t.Fatalf("engine spec pacing = %#v, want 2000", spec.Script.Config.Pacing)
	}

	// The durable record must survive a restart with the edit applied.
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatalf("GetTest record: %v", err)
	}
	storedGroups := decodeStoredGroups(record.Groups)
	if len(storedGroups) != 1 {
		t.Fatalf("stored groups = %d, want 1", len(storedGroups))
	}
	if storedGroups[0].RuntimeSettings == nil || storedGroups[0].RuntimeSettings.Pacing == nil || storedGroups[0].RuntimeSettings.Pacing.DelayMs != 2000 {
		t.Fatalf("stored group override = %#v, want pacing 2000", storedGroups[0].RuntimeSettings)
	}
	if storedGroups[0].script == nil || storedGroups[0].script.Config.Pacing.DelayMs != 2000 {
		t.Fatalf("stored runtime script pacing not persisted")
	}
}

func TestExecutableGroupEditsInvalidateShakeoutPassAndOverride(t *testing.T) {
	seedShakeoutEvidence := func(runner *TestRunner, testID string) {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		test := runner.tests[testID]
		test.StartAuthorization = &QueuedStartAuthorization{
			CapacityShortfall:  &CapacityShortfallStartAuthorization{ApprovedAt: time.Now().UTC(), TotalVUsers: test.TotalVUsers},
			ScriptShakeout:     &ScriptShakeoutStartAuthorization{ApprovedAt: time.Now().UTC(), ReviewEvidenceSHA256: "prior-review"},
			ScriptShakeoutPass: &ScriptShakeoutPassEvidence{PassedAt: time.Now().UTC(), EvidenceSHA256: "prior-executable-snapshot"},
		}
		test.validatedScriptShakeoutPass = "prior-executable-snapshot"
	}
	assertInvalidated := func(t *testing.T, runner *TestRunner, testID string) {
		t.Helper()
		runner.mu.RLock()
		defer runner.mu.RUnlock()
		test := runner.tests[testID]
		if test.StartAuthorization == nil || test.StartAuthorization.CapacityShortfall == nil {
			t.Fatalf("unrelated capacity authorization was removed: %#v", test.StartAuthorization)
		}
		if test.StartAuthorization.ScriptShakeout != nil || test.StartAuthorization.ScriptShakeoutPass != nil || test.validatedScriptShakeoutPass != "" {
			t.Fatalf("executable edit retained stale shakeout authority: authorization=%#v validated=%q", test.StartAuthorization, test.validatedScriptShakeoutPass)
		}
	}

	t.Run("runtime settings", func(t *testing.T) {
		server, _ := newRuntimeOverrideTestServer(t)
		test := createQueuedGroupSettingsTest(t, server)
		seedShakeoutEvidence(server.runner, test.ID)
		_, err := server.runner.UpdateTestGroupRuntimeSettings(
			test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"),
			&RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 1777}}, []string{"pacing"},
		)
		if err != nil {
			t.Fatalf("UpdateTestGroupRuntimeSettings: %v", err)
		}
		assertInvalidated(t, server.runner, test.ID)
	})

	t.Run("parameters", func(t *testing.T) {
		server, _ := newRuntimeOverrideTestServer(t)
		test := createQueuedGroupSettingsTest(t, server)
		seedShakeoutEvidence(server.runner, test.ID)
		view, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
		if err != nil {
			t.Fatalf("GetTestGroupParametersView: %v", err)
		}
		parameters := cloneScriptParameters(view.Parameters)
		parameters[1].Selection = "sequential"
		if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, parameters); err != nil {
			t.Fatalf("UpdateTestGroupParameters: %v", err)
		}
		assertInvalidated(t, server.runner, test.ID)
	})
}

func TestUpdateTestGroupRuntimeSettingsReplacesCreateTimeIterations(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(parameterizedHTTPScript("script-iterations", "Iterations"))
	initialIterations := 10
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Iteration edit", Duration: "5m",
		Groups: []CreateGroupEntry{{Name: "Buyers", ScriptID: "script-iterations", VUsers: 1, Iterations: &initialIterations}},
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedIterations := 20
	result, err := server.runner.UpdateTestGroupRuntimeSettings(
		test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"),
		&RuntimeSettingsOverride{Iterations: &updatedIterations}, []string{"iterations"},
	)
	if err != nil {
		t.Fatalf("UpdateTestGroupRuntimeSettings iterations: %v", err)
	}
	if result.NoChanges {
		t.Fatal("iteration update reported no changes")
	}
	managed, ok := server.runner.GetTest(test.ID)
	if !ok || len(managed.Groups) != 1 {
		t.Fatalf("updated test = %#v", managed)
	}
	group := managed.Groups[0]
	if group.IterationOverride == nil || *group.IterationOverride != updatedIterations || group.script == nil || group.script.Config.RunLogic.Iterations != updatedIterations {
		t.Fatalf("iteration projection = override %#v compiled %#v, want %d", group.IterationOverride, group.script, updatedIterations)
	}
}

func TestUpdateTestGroupRuntimeSettingsRejectsStartedRun(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)
	server.runner.mu.Lock()
	server.runner.tests[test.ID].Status = "RUNNING"
	server.runner.mu.Unlock()

	_, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 2000},
	}, []string{"pacing"})
	var notEditable *testGroupSettingsNotEditableError
	if err == nil || !errors.As(err, &notEditable) {
		t.Fatalf("expected not-editable error, got %v", err)
	}
	if notEditable.status != "RUNNING" {
		t.Fatalf("not-editable status = %q, want RUNNING", notEditable.status)
	}

	// The read view must still work and report why editing is refused.
	view, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("GetTestGroupRuntimeSettingsView: %v", err)
	}
	if view.Editable || view.EditableReason == "" {
		t.Fatalf("running view should be read-only with a reason, got %#v", view)
	}
}

func TestUpdateTestGroupRuntimeSettingsRejectsEnvEdits(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	if _, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), nil, []string{"env"}); err == nil ||
		!strings.Contains(err.Error(), "cannot be edited from the command center") {
		t.Fatalf("env field should be rejected, got %v", err)
	}
	if _, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		Env:    map[string]string{"TOKEN": "abc"},
		Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
	}, []string{"pacing"}); err == nil || !strings.Contains(err.Error(), "env and redaction rules") {
		t.Fatalf("env payload should be rejected, got %v", err)
	}
}

// Credential edits: new plaintext material is rejected (it would bypass the
// governed secret-provisioning pipeline and evaporate on restart), while a
// rotation to a governed {{secret.NAME}} reference must register as a change
// — even though the stored protected reference and the new reference mask to
// the same redaction sentinel — with a masked audit payload.
func TestUpdateTestGroupRuntimeSettingsCredentialEdits(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(parameterizedHTTPScript("script-cred", "Cred Checkout"))
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Credential Run",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "Buyers",
			ScriptID: "script-cred",
			VUsers:   2,
			RuntimeSettings: &RuntimeSettingsOverride{
				HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "bearer", Token: "secret-a"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	// Plaintext rotation is refused with guidance; the run is unchanged.
	if _, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "bearer", Token: "secret-b"}},
	}, []string{"http"}); err == nil || !strings.Contains(err.Error(), "new credential material") {
		t.Fatalf("plaintext credential edit should be rejected, got %v", err)
	}
	managed, _ := server.runner.GetTest(test.ID)
	if config := managed.Groups[0].script.Config.HTTP.Auth.Token; !strings.Contains(config, "{{secret.") {
		t.Fatalf("stored token should remain the protected creation-time reference, got %q", config)
	}

	// Rotation to a governed secret reference is accepted and registers a
	// change even though both sides mask to the same sentinel.
	result, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "bearer", Token: "{{secret.ROTATED_TOKEN}}"}},
	}, []string{"http"})
	if err != nil {
		t.Fatalf("UpdateTestGroupRuntimeSettings with secret reference: %v", err)
	}
	if result.NoChanges {
		t.Fatalf("credential-reference rotation was swallowed as a no-op")
	}
	if len(result.Fields) != 1 || result.Fields[0] != "http" {
		t.Fatalf("changed fields = %v, want [http]", result.Fields)
	}
	payload, err := json.Marshal(result.Changes)
	if err != nil {
		t.Fatalf("marshal changes: %v", err)
	}
	if strings.Contains(string(payload), "secret-a") || strings.Contains(string(payload), "ROTATED_TOKEN") {
		t.Fatalf("audit change payload leaked credential material: %s", payload)
	}
	if masked := string(payload); !strings.Contains(masked, "REDACTED") {
		t.Fatalf("audit change payload should carry masked credential markers: %s", masked)
	}
}

func TestUpdateTestGroupRuntimeSettingsMaskedCredentialUsernamesSurviveSiblingEdits(t *testing.T) {
	t.Run("HTTP auth", func(t *testing.T) {
		server, _ := newRuntimeOverrideTestServer(t)
		server.runner.StoreScript(parameterizedHTTPScript("script-http-user", "HTTP Username"))
		test, err := server.runner.CreateTest(CreateTestRequest{
			Name:     "HTTP Username Run",
			Duration: "5m",
			Groups: []CreateGroupEntry{{
				Name:     "Buyers",
				ScriptID: "script-http-user",
				VUsers:   2,
				RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{
					Auth: dsl.AuthConfig{Type: "basic", Username: "runtime-user", Password: "runtime-password"},
				}},
			}},
		})
		if err != nil {
			t.Fatalf("CreateTest: %v", err)
		}
		managed, ok := server.runner.GetTest(test.ID)
		if !ok {
			t.Fatal("created test is unavailable")
		}
		storedUsername := managed.Groups[0].RuntimeSettings.HTTP.Auth.Username
		if !isReservedRuntimeOverrideSecretReference(storedUsername) {
			t.Fatalf("stored HTTP username = %q, want protected internal reference", storedUsername)
		}

		view, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
		if err != nil {
			t.Fatalf("GetTestGroupRuntimeSettingsView: %v", err)
		}
		if got := view.RuntimeSettings.HTTP.Auth.Username; got != scrub.Redacted {
			t.Fatalf("GET HTTP username = %q, want redacted", got)
		}
		if payload, marshalErr := json.Marshal(view); marshalErr != nil {
			t.Fatalf("marshal view: %v", marshalErr)
		} else if strings.Contains(string(payload), runtimeOverrideSecretPrefix) {
			t.Fatalf("GET view exposed controller-owned secret reference: %s", payload)
		}

		view.RuntimeSettings.HTTP.RequestTimeoutMs = 4321
		if _, err := server.runner.UpdateTestGroupRuntimeSettings(
			test.ID,
			"Buyers",
			view.Revision,
			view.RuntimeSettings,
			[]string{"http"},
		); err != nil {
			t.Fatalf("masked HTTP sibling edit: %v", err)
		}
		managed, ok = server.runner.GetTest(test.ID)
		if !ok {
			t.Fatal("test disappeared after sibling edit")
		}
		http := managed.Groups[0].RuntimeSettings.HTTP
		if http.Auth.Username != storedUsername || http.RequestTimeoutMs != 4321 {
			t.Fatalf("HTTP sibling edit did not preserve the protected username: %#v", http)
		}
	})

	t.Run("browser HTTP credentials", func(t *testing.T) {
		server, _ := newRuntimeOverrideTestServer(t)
		script := server.runner.StoreScript(&dsl.Script{
			ID:        "script-browser-user",
			Name:      "Browser Username",
			Version:   "1.0.0",
			Protocol:  "browser",
			ProjectID: storage.DefaultProjectID,
			Actions: []dsl.Action{{
				Name: "Open", Type: "browser_navigate", Target: "https://example.test",
			}},
		})
		test, err := server.runner.CreateTest(CreateTestRequest{
			Name:     "Browser Username Run",
			Duration: "5m",
			Groups: []CreateGroupEntry{{
				Name:     "Browsers",
				ScriptID: script.ID,
				VUsers:   1,
				RuntimeSettings: &RuntimeSettingsOverride{BrowserConfig: &dsl.BrowserConfig{
					Engine:          "chromium",
					DeviceProfile:   "desktop_1080p",
					HTTPCredentials: dsl.BrowserHTTPCredentials{Username: "browser-user", Password: "browser-password"},
				}},
			}},
		})
		if err != nil {
			t.Fatalf("CreateTest: %v", err)
		}
		managed, ok := server.runner.GetTest(test.ID)
		if !ok {
			t.Fatal("created test is unavailable")
		}
		storedUsername := managed.Groups[0].RuntimeSettings.BrowserConfig.HTTPCredentials.Username
		if !isReservedRuntimeOverrideSecretReference(storedUsername) {
			t.Fatalf("stored browser username = %q, want protected internal reference", storedUsername)
		}

		view, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Browsers")
		if err != nil {
			t.Fatalf("GetTestGroupRuntimeSettingsView: %v", err)
		}
		if got := view.RuntimeSettings.BrowserConfig.HTTPCredentials.Username; got != scrub.Redacted {
			t.Fatalf("GET browser username = %q, want redacted", got)
		}

		view.RuntimeSettings.BrowserConfig.Headless = boolPtr(true)
		if _, err := server.runner.UpdateTestGroupRuntimeSettings(
			test.ID,
			"Browsers",
			view.Revision,
			view.RuntimeSettings,
			[]string{"browser_config"},
		); err != nil {
			t.Fatalf("masked browser sibling edit: %v", err)
		}
		managed, ok = server.runner.GetTest(test.ID)
		if !ok {
			t.Fatal("test disappeared after sibling edit")
		}
		browser := managed.Groups[0].RuntimeSettings.BrowserConfig
		if browser.HTTPCredentials.Username != storedUsername || browser.Headless == nil || !*browser.Headless {
			t.Fatalf("browser sibling edit did not preserve the protected username: %#v", browser)
		}
	})
}

func TestUpdateTestGroupRuntimeSettingsRejectsNeutralHeaderCredentialMaterial(t *testing.T) {
	const credential = "Bearer command-center-route-token-0123456789"
	server, store := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	if _, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			DefaultHeaders: map[string]string{"X-Route": credential},
		},
	}, []string{"http"}); err == nil || !strings.Contains(err.Error(), "new credential material") {
		t.Fatalf("neutral-name plaintext credential edit should be rejected, got %v", err)
	}

	managed, ok := server.runner.GetTest(test.ID)
	if !ok {
		t.Fatal("test disappeared after rejected edit")
	}
	if managed.Groups[0].RuntimeSettings != nil && managed.Groups[0].RuntimeSettings.HTTP != nil {
		if got := managed.Groups[0].RuntimeSettings.HTTP.DefaultHeaders["X-Route"]; got != "" {
			t.Fatalf("rejected edit mutated in-memory runtime settings: %q", got)
		}
	}
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatalf("GetTest record: %v", err)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal durable test: %v", err)
	}
	if strings.Contains(string(payload), credential) || strings.Contains(string(payload), "command-center-route-token") {
		t.Fatalf("rejected edit persisted credential material: %s", payload)
	}
}

func TestUpdateTestGroupRuntimeSettingsRejectsReservedInternalSecretReferences(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(parameterizedHTTPScript("script-reserved-ref", "Reserved Ref Checkout"))
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Reserved Ref Run",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "Buyers",
			ScriptID: "script-reserved-ref",
			VUsers:   2,
			RuntimeSettings: &RuntimeSettingsOverride{
				HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "bearer", Token: "creation-time-secret"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	internalName := runtimeOverrideSecretName(test.ID, 0, "http.auth.token")
	internalRef := "{{secret." + internalName + "}}"
	managed, ok := server.runner.GetTest(test.ID)
	if !ok || managed.Groups[0].RuntimeSettings == nil || managed.Groups[0].RuntimeSettings.HTTP == nil {
		t.Fatalf("created runtime settings unavailable: %#v", managed)
	}
	if got := managed.Groups[0].RuntimeSettings.HTTP.Auth.Token; got != internalRef {
		t.Fatalf("protected token reference = %q, want %q", got, internalRef)
	}

	tests := []struct {
		name     string
		incoming *RuntimeSettingsOverride
	}{
		{
			name: "client authors reserved reference at original path",
			incoming: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "bearer", Token: internalRef},
			}},
		},
		{
			name: "client retargets reserved reference into non-sensitive header",
			incoming: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{
				Auth:           dsl.AuthConfig{Type: "bearer", Token: scrub.Redacted},
				DefaultHeaders: map[string]string{"X-Exfiltrate": "Bearer {{secret. " + internalName + " }}"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := server.runner.UpdateTestGroupRuntimeSettings(
				test.ID,
				"Buyers",
				mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"),
				tt.incoming,
				[]string{"http"},
			); err == nil || !strings.Contains(err.Error(), "reserved internal secret references") {
				t.Fatalf("reserved internal reference should be rejected, got %v", err)
			}
			current, ok := server.runner.GetTest(test.ID)
			if !ok {
				t.Fatal("test disappeared after rejected edit")
			}
			http := current.Groups[0].RuntimeSettings.HTTP
			if http.Auth.Token != internalRef || len(http.DefaultHeaders) != 0 {
				t.Fatalf("rejected edit mutated runtime HTTP settings: %#v", http)
			}
		})
	}

	// The masked response contract remains the only client path that preserves
	// an existing internal credential: the server restores it at the same field
	// while accepting a sibling setting change.
	if _, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			RequestTimeoutMs: 4321,
			Auth:             dsl.AuthConfig{Type: "bearer", Token: scrub.Redacted},
		},
	}, []string{"http"}); err != nil {
		t.Fatalf("same-path masked reconciliation: %v", err)
	}
	current, ok := server.runner.GetTest(test.ID)
	if !ok {
		t.Fatal("test disappeared after accepted edit")
	}
	if http := current.Groups[0].RuntimeSettings.HTTP; http.Auth.Token != internalRef || http.RequestTimeoutMs != 4321 {
		t.Fatalf("same-path reconciliation did not preserve only the original token: %#v", http)
	}
}

func TestUpdateTestGroupRuntimeSettingsNoChangesIsNoOp(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	result, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), &RuntimeSettingsOverride{
		Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 500},
	}, []string{"pacing"})
	if err != nil {
		t.Fatalf("UpdateTestGroupRuntimeSettings: %v", err)
	}
	if !result.NoChanges {
		t.Fatalf("identical pacing should be a no-op, got %#v", result)
	}
}

func TestUpdateTestGroupRuntimeSettingsRejectsStaleSameSectionEdit(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)
	firstReader, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil {
		t.Fatal(err)
	}
	secondReader, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil {
		t.Fatal(err)
	}
	firstResult, err := server.runner.UpdateTestGroupRuntimeSettings(
		test.ID, "Buyers", firstReader.Revision,
		&RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{RequestTimeoutMs: 4321}},
		[]string{"http"},
	)
	if err != nil {
		t.Fatalf("first reader update: %v", err)
	}
	if firstResult.Revision == firstReader.Revision {
		t.Fatal("successful runtime edit did not advance the shared revision")
	}
	_, err = server.runner.UpdateTestGroupRuntimeSettings(
		test.ID, "Buyers", secondReader.Revision,
		&RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{ConnectTimeoutMs: 1234}},
		[]string{"http"},
	)
	var conflict *testGroupSettingsRevisionConflictError
	if err == nil || !errors.As(err, &conflict) {
		t.Fatalf("stale same-section edit = %v, want revision conflict", err)
	}
	current, _ := server.runner.GetTest(test.ID)
	http := current.Groups[0].RuntimeSettings.HTTP
	if http.RequestTimeoutMs != 4321 || http.ConnectTimeoutMs != 0 {
		t.Fatalf("stale reader overwrote HTTP section: %#v", http)
	}
}

func TestUpdateTestGroupParametersAppliesPreStart(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	view, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("GetTestGroupParametersView: %v", err)
	}
	if len(view.Parameters) != 2 {
		t.Fatalf("parameters = %d, want 2", len(view.Parameters))
	}
	if len(view.DataSources) != 1 || view.DataSources[0].Name != "accounts" || view.DataSources[0].RowCount != 2 {
		t.Fatalf("data sources = %#v, want accounts summary with 2 data rows", view.DataSources)
	}

	next := cloneScriptParameters(view.Parameters)
	next[1].Values = []string{"east", "west", "central"}
	next[1].Selection = "sequential"
	next = append(next, dsl.Parameter{Name: "OrderPrefix", Type: "list", Values: []string{"A", "B"}, Update: "each_iteration", Selection: "sequential"})

	result, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, next)
	if err != nil {
		t.Fatalf("UpdateTestGroupParameters: %v", err)
	}
	if result.NoChanges {
		t.Fatalf("expected parameter changes")
	}
	if len(result.Changed) != 2 || result.Changed[0] != "OrderPrefix" || result.Changed[1] != "Region" {
		t.Fatalf("changed = %v, want [OrderPrefix Region]", result.Changed)
	}

	managed, _ := server.runner.GetTest(test.ID)
	group := managed.Groups[0]
	if len(group.script.Parameters) != 3 {
		t.Fatalf("script parameters = %d, want 3", len(group.script.Parameters))
	}
	if group.script.Parameters[1].Values[2] != "central" {
		t.Fatalf("edited values not applied: %#v", group.script.Parameters[1].Values)
	}
	if group.baseScript == nil || len(group.baseScript.Parameters) != 3 {
		t.Fatalf("base script parameters = %#v, want 3 entries", group.baseScript)
	}
	server.runner.mu.RLock()
	spec := server.runner.tests[test.ID].groupSpecs[0]
	server.runner.mu.RUnlock()
	if len(spec.Script.Parameters) != 3 {
		t.Fatalf("engine spec parameters = %d, want 3", len(spec.Script.Parameters))
	}

	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatalf("GetTest record: %v", err)
	}
	storedGroups := decodeStoredGroups(record.Groups)
	if len(storedGroups) != 1 || len(storedGroups[0].script.Parameters) != 3 {
		t.Fatalf("stored parameters not persisted")
	}
}

func TestUpdateTestGroupParametersRejectsTwoReaderLostUpdate(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)
	firstReader, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatal(err)
	}
	secondReader, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatal(err)
	}
	firstEdit := cloneScriptParameters(firstReader.Parameters)
	firstEdit[1].Selection = "sequential"
	firstResult, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", firstReader.Revision, firstEdit)
	if err != nil {
		t.Fatalf("first reader update: %v", err)
	}
	if firstResult.Revision == firstReader.Revision {
		t.Fatal("successful parameter edit did not advance the shared revision")
	}
	secondEdit := cloneScriptParameters(secondReader.Parameters)
	secondEdit[0].Selection = "random"
	_, err = server.runner.UpdateTestGroupParameters(test.ID, "Buyers", secondReader.Revision, secondEdit)
	var conflict *testGroupSettingsRevisionConflictError
	if err == nil || !errors.As(err, &conflict) {
		t.Fatalf("stale disjoint parameter edit = %v, want revision conflict", err)
	}
	current, _ := server.runner.GetTest(test.ID)
	if current.Groups[0].script.Parameters[1].Selection != "sequential" ||
		current.Groups[0].script.Parameters[0].Selection != "sequential" {
		t.Fatalf("stale reader overwrote first edit: %#v", current.Groups[0].script.Parameters)
	}
	runtimeView, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil || runtimeView.Revision != firstResult.Revision {
		t.Fatalf("runtime and parameter editors do not share revision: runtime=%#v result=%#v err=%v", runtimeView, firstResult, err)
	}
}

// Parameter edits: credential-NAMED parameters are refused (library parity —
// the durable store requires secret bindings for them), credential-SHAPED
// values on benign names are allowed but scrubbed from the audit payload, and
// edits made against the compiled view must not bake substituted values into
// untouched base-script parameters (the base keeps its authoring templates).
func TestUpdateTestGroupParametersCredentialGuardsAndBaseTemplates(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := parameterizedHTTPScript("script-tpl", "Template Checkout")
	script.Parameters = append(script.Parameters,
		dsl.Parameter{Name: "SuffixedRegion", Type: "list", Values: []string{"east-{{env.SUFFIX}}"}, Update: "once", Selection: "sequential"},
	)
	server.runner.StoreScript(script)
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Template Run",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "Buyers",
			ScriptID: "script-tpl",
			VUsers:   2,
			RuntimeSettings: &RuntimeSettingsOverride{
				Env: map[string]string{"SUFFIX": "west"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	view, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("GetTestGroupParametersView: %v", err)
	}
	var suffixed dsl.Parameter
	for _, param := range view.Parameters {
		if param.Name == "SuffixedRegion" {
			suffixed = param
		}
	}
	if len(suffixed.Values) != 1 || suffixed.Values[0] != "east-west" {
		t.Fatalf("compiled view should show substituted values, got %#v", suffixed.Values)
	}

	// Adding a credential-named parameter is refused outright.
	withCredentialName := append(cloneScriptParameters(view.Parameters), dsl.Parameter{
		Name: "api_password", Type: "list", Values: []string{"hunter2"}, Update: "once", Selection: "sequential",
	})
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, withCredentialName); err == nil ||
		!strings.Contains(err.Error(), "credential-shaped") {
		t.Fatalf("credential-named parameter edit should be rejected, got %v", err)
	}

	// A benign-named parameter cannot bypass the credential boundary by
	// carrying a credential-shaped value.
	next := append(cloneScriptParameters(view.Parameters), dsl.Parameter{
		Name: "SessionNotes", Type: "list", Values: []string{"Bearer abc123-static-token"}, Update: "once", Selection: "sequential",
	})
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, next); err == nil ||
		!strings.Contains(err.Error(), "credential-shaped value") {
		t.Fatalf("credential-valued parameter edit should be rejected, got %v", err)
	}

	// A safe sibling edit still succeeds, and the untouched env-templated
	// parameter must not be baked into the base script.
	next = cloneScriptParameters(view.Parameters)
	for index := range next {
		if next[index].Name == "Region" {
			next[index].Selection = "sequential"
		}
	}
	result, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, next)
	if err != nil {
		t.Fatalf("UpdateTestGroupParameters safe edit: %v", err)
	}
	if len(result.Changed) != 1 || result.Changed[0] != "Region" {
		t.Fatalf("changed = %v, want [Region]", result.Changed)
	}
	managed, _ := server.runner.GetTest(test.ID)
	group := managed.Groups[0]
	scriptValues := map[string][]string{}
	for _, param := range group.script.Parameters {
		scriptValues[param.Name] = param.Values
	}
	baseValues := map[string][]string{}
	for _, param := range group.baseScript.Parameters {
		baseValues[param.Name] = param.Values
	}
	if _, ok := scriptValues["SessionNotes"]; ok {
		t.Fatal("rejected credential-valued parameter reached the run script")
	}
	if _, ok := baseValues["SessionNotes"]; ok {
		t.Fatal("rejected credential-valued parameter reached the base script")
	}
	// The untouched parameter keeps its authoring template in the base while
	// the compiled run script keeps the substituted value.
	if got := baseValues["SuffixedRegion"]; len(got) != 1 || got[0] != "east-{{env.SUFFIX}}" {
		t.Fatalf("untouched base parameter lost its env template: %#v", got)
	}
	if got := scriptValues["SuffixedRegion"]; len(got) != 1 || got[0] != "east-west" {
		t.Fatalf("untouched run parameter should keep the substituted value: %#v", got)
	}
}

func TestTestGroupParametersProjectAndReconcileImportedCredentialValues(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	const credential = "tiny-imported-credential"
	test := createQueuedGroupSettingsTest(t, server)

	// Imported execution snapshots can retain credential source values even
	// though durable library projections refuse to disclose them. Model that
	// post-creation execution state directly; the endpoint must use the same
	// projection/reconciliation contract as the script library.
	server.runner.mu.Lock()
	executionScript := cloneScriptForStoredGroup(server.runner.tests[test.ID].Groups[0].script)
	executionScript.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source: "postman",
		Preserved: map[string]any{
			dsl.ImportPreservedCredentialParameterNamesKey: []string{"SessionValue"},
		},
	}}
	executionScript.Parameters = []dsl.Parameter{{
		Name:      "SessionValue",
		Type:      "list",
		Values:    []string{credential},
		Update:    "once",
		Selection: "sequential",
	}}
	baseScript := cloneScriptForStoredGroup(executionScript)
	server.runner.tests[test.ID].Groups[0].script = executionScript
	server.runner.tests[test.ID].Groups[0].baseScript = baseScript
	server.runner.tests[test.ID].groupSpecs[0].Script = executionScript
	server.runner.mu.Unlock()

	view, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("GetTestGroupParametersView: %v", err)
	}
	viewPayload, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(viewPayload), credential) {
		t.Fatalf("parameter view leaked imported credential: %s", viewPayload)
	}
	if len(view.Parameters) != 1 || len(view.Parameters[0].Values) != 1 ||
		view.Parameters[0].Values[0] != maskedScriptSecret {
		t.Fatalf("parameter view values = %#v, want disclosure sentinel", view.Parameters)
	}

	// A normal read/modify/write changes a sibling field while echoing the
	// projection sentinel. The server must restore the stored credential, never
	// persist the sentinel, and keep both the response and audit delta masked.
	next := cloneScriptParameters(view.Parameters)
	next[0].Selection = "random"
	result, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, next)
	if err != nil {
		t.Fatalf("UpdateTestGroupParameters: %v", err)
	}
	resultPayload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resultPayload), credential) {
		t.Fatalf("parameter update response or audit delta leaked imported credential: %s", resultPayload)
	}
	if len(result.Parameters) != 1 || result.Parameters[0].Values[0] != maskedScriptSecret {
		t.Fatalf("parameter update response values = %#v, want disclosure sentinel", result.Parameters)
	}
	managed, ok := server.runner.GetTest(test.ID)
	if !ok {
		t.Fatal("updated test disappeared")
	}
	if got := managed.Groups[0].script.Parameters[0].Values; len(got) != 1 || got[0] != credential {
		t.Fatalf("execution parameter value = %#v, want original credential restored", got)
	}
	if managed.Groups[0].script.Parameters[0].Selection != "random" {
		t.Fatalf("sibling parameter edit was not applied: %#v", managed.Groups[0].script.Parameters[0])
	}

	// A protected field itself cannot be replaced through the focused run
	// editor; credential rotation belongs in the governed script workflow.
	changedCredential := cloneScriptParameters(result.Parameters)
	changedCredential[0].Values[0] = "replacement-plaintext"
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", result.Revision, changedCredential); err == nil ||
		!strings.Contains(err.Error(), "protected field Values changed") {
		t.Fatalf("credential-backed value edit should be rejected, got %v", err)
	}

	// Echoing the updated projection again is a true no-op.
	noChange, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", result.Revision, result.Parameters)
	if err != nil {
		t.Fatalf("UpdateTestGroupParameters no-op: %v", err)
	}
	if !noChange.NoChanges {
		t.Fatalf("projected no-op registered changes: %#v", noChange)
	}
}

func TestTestGroupParametersProjectAndReconcileLegacyValueShapedCredential(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	const credential = "Bearer legacy-token-value-3819"
	test := createQueuedGroupSettingsTest(t, server)

	server.runner.mu.Lock()
	executionScript := cloneScriptForStoredGroup(server.runner.tests[test.ID].Groups[0].script)
	executionScript.Metadata = nil
	executionScript.Parameters = []dsl.Parameter{{
		Name:      "SessionNotes",
		Type:      "list",
		Values:    []string{credential},
		Update:    "once",
		Selection: "sequential",
	}}
	baseScript := cloneScriptForStoredGroup(executionScript)
	server.runner.tests[test.ID].Groups[0].script = executionScript
	server.runner.tests[test.ID].Groups[0].baseScript = baseScript
	server.runner.tests[test.ID].groupSpecs[0].Script = executionScript
	server.runner.mu.Unlock()

	view, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("GetTestGroupParametersView: %v", err)
	}
	assertNoCredential := func(label string, value any) {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), credential) {
			t.Fatalf("%s leaked legacy credential: %s", label, payload)
		}
		if !strings.Contains(string(payload), maskedScriptSecret) {
			t.Fatalf("%s omitted the disclosure sentinel: %s", label, payload)
		}
	}
	assertNoCredential("initial view", view)

	next := cloneScriptParameters(view.Parameters)
	next[0].Selection = "random"
	result, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, next)
	if err != nil {
		t.Fatalf("UpdateTestGroupParameters sibling edit: %v", err)
	}
	assertNoCredential("update response", result)

	subsequent, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("subsequent GetTestGroupParametersView: %v", err)
	}
	assertNoCredential("subsequent view", subsequent)

	managed, ok := server.runner.GetTest(test.ID)
	if !ok {
		t.Fatal("updated test disappeared")
	}
	got := managed.Groups[0].script.Parameters[0]
	if len(got.Values) != 1 || got.Values[0] != credential {
		t.Fatalf("stored credential = %#v, want original value preserved", got.Values)
	}
	if got.Selection != "random" {
		t.Fatalf("safe sibling edit was not persisted: %#v", got)
	}
}

func TestGetTestGroupParametersViewReleasesRunnerLockBeforeSummarizing(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	entered := make(chan struct{})
	release := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		_, err := server.runner.getTestGroupParametersView(test.ID, "Buyers", func(sources []dsl.DataSource) []TestGroupParameterDataSource {
			close(entered)
			<-release
			return summarizeParameterDataSources(sources)
		})
		readDone <- err
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("parameter summarizer was not entered")
	}

	writeAcquired := make(chan struct{})
	go func() {
		server.runner.mu.Lock()
		server.runner.mu.Unlock() //nolint:staticcheck // acquiring the lock is the signal the test waits for
		close(writeAcquired)
	}()
	select {
	case <-writeAcquired:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("runner writer was blocked while parameter data sources were summarized")
	}
	close(release)
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("GetTestGroupParametersView: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("parameter view did not complete after summarization resumed")
	}
}

func TestUpdateTestGroupParametersRejectsRemovalAndInvalid(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	// Removing an existing parameter is refused.
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), []dsl.Parameter{
		{Name: "AccountUser", Type: "file", DataSource: "accounts", Column: "user"},
	}); err == nil || !strings.Contains(err.Error(), "cannot be removed or renamed") {
		t.Fatalf("removal should be rejected, got %v", err)
	}

	// Invalid definitions are refused by DSL validation.
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), []dsl.Parameter{
		{Name: "AccountUser", Type: "file", DataSource: "missing-source", Column: "user"},
		{Name: "Region", Type: "list", Values: []string{"east"}},
	}); err == nil || !strings.Contains(err.Error(), "parameters are invalid") {
		t.Fatalf("invalid data source should be rejected, got %v", err)
	}

	oversizedName := strings.Repeat("n", maxTestGroupParameterNameBytes+1)
	withOversizedName := append(cloneScriptParameters(test.Groups[0].script.Parameters), dsl.Parameter{
		Name: oversizedName, Type: "list", Values: []string{"value"},
	})
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), withOversizedName); err == nil ||
		!strings.Contains(err.Error(), "name must not exceed") {
		t.Fatalf("oversized parameter name should be rejected, got %v", err)
	}

	// Started runs are read-only.
	server.runner.mu.Lock()
	server.runner.tests[test.ID].Status = "RUNNING"
	server.runner.mu.Unlock()
	var notEditable *testGroupSettingsNotEditableError
	if _, err := server.runner.UpdateTestGroupParameters(test.ID, "Buyers", mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"), nil); err == nil || !errors.As(err, &notEditable) {
		t.Fatalf("expected not-editable error, got %v", err)
	}
}

func TestCompactParameterForAuditBoundsIndividualValues(t *testing.T) {
	large := strings.Repeat("界", testGroupSettingsAuditValueBytes)
	compacted := compactParameterForAudit(dsl.Parameter{
		Name:       "ordinary",
		Type:       "list",
		DataSource: large,
		Column:     large,
		Values:     []string{large},
	})
	for _, field := range []string{"data_source", "column"} {
		value, ok := compacted[field].(string)
		if !ok || len(value) > testGroupSettingsAuditValueBytes || !utf8.ValidString(value) {
			t.Fatalf("compacted %s = %#v, want valid UTF-8 bounded to %d bytes", field, compacted[field], testGroupSettingsAuditValueBytes)
		}
	}
	values, ok := compacted["values"].([]string)
	if !ok || len(values) != 1 || len(values[0]) > testGroupSettingsAuditValueBytes || !utf8.ValidString(values[0]) {
		t.Fatalf("compacted values = %#v, want one valid UTF-8 bounded value", compacted["values"])
	}
}

func TestGroupSettingsEndpointsOverHTTP(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)

	get := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+test.ID+"/groups/Buyers/runtime-settings", nil)
	getRec := httptest.NewRecorder()
	server.mux.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET runtime-settings status = %d body=%s", getRec.Code, getRec.Body.String())
	}
	var view TestGroupRuntimeSettingsView
	if err := json.Unmarshal(getRec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if !view.Editable {
		t.Fatalf("queued view should be editable")
	}

	patchBody, _ := json.Marshal(map[string]any{
		"runtime_settings":  map[string]any{"pacing": map[string]any{"type": "fixed_delay", "delay_ms": 1500}},
		"fields":            []string{" pacing ", "PACING", "pacing"},
		"expected_revision": view.Revision,
	})
	patch := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+test.ID+"/groups/Buyers/runtime-settings", bytes.NewReader(patchBody))
	patch.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	server.mux.ServeHTTP(patchRec, patch)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH runtime-settings status = %d body=%s", patchRec.Code, patchRec.Body.String())
	}

	// The audit event lands with the manifest-visible action and details.
	events, err := store.ListAuditEvents(storage.AuditEventFilter{ResourceType: "test", ResourceID: test.ID, Action: "test.runtime_settings.update", Status: "success", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	allEvents, err := store.ListAuditEvents(storage.AuditEventFilter{ResourceType: "test", ResourceID: test.ID, Action: "test.runtime_settings.update", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents(all): %v", err)
	}
	for _, event := range allEvents {
		if event.Status != "pending" {
			continue
		}
		fields, ok := event.Details["fields"].([]any)
		if !ok || len(fields) != 1 || fields[0] != "pacing" {
			t.Fatalf("pending audit retained raw duplicate fields: %#v", event.Details["fields"])
		}
	}
	if events[0].Details["group"] != "Buyers" {
		t.Fatalf("audit details group = %#v", events[0].Details)
	}

	// The run manifest surfaces the change in the runtime_changes delta.
	manifest, err := server.runner.GetTestManifest(test.ID)
	if err != nil {
		t.Fatalf("GetTestManifest: %v", err)
	}
	found := false
	for _, change := range manifest.RuntimeChanges {
		if change.Action != "test.runtime_settings.update" {
			continue
		}
		found = true
		if change.Label != "Runtime settings changed" {
			t.Fatalf("change label = %q", change.Label)
		}
		if change.Details["group"] != "Buyers" {
			t.Fatalf("manifest change details missing group: %#v", change.Details)
		}
		if _, ok := change.Details["changes"]; !ok {
			t.Fatalf("manifest change details missing changes: %#v", change.Details)
		}
	}
	if !found {
		t.Fatalf("manifest runtime_changes missing runtime settings update: %#v", manifest.RuntimeChanges)
	}

	// Started runs refuse edits over HTTP with 409.
	server.runner.mu.Lock()
	server.runner.tests[test.ID].Status = "RUNNING"
	server.runner.mu.Unlock()
	conflict := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+test.ID+"/groups/Buyers/runtime-settings", bytes.NewReader(patchBody))
	conflict.Header.Set("Content-Type", "application/json")
	conflictRec := httptest.NewRecorder()
	server.mux.ServeHTTP(conflictRec, conflict)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("PATCH on running test status = %d, want 409", conflictRec.Code)
	}

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+test.ID+"/groups/Nope/runtime-settings", nil)
	missingRec := httptest.NewRecorder()
	server.mux.ServeHTTP(missingRec, missing)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown group status = %d, want 404", missingRec.Code)
	}
}

type groupSettingsAuditFailureStore struct {
	Store  storage.Store
	action string
	status string
}

func (s *groupSettingsAuditFailureStore) SaveAuditEvent(event *storage.AuditEvent) error {
	if event != nil && event.Action == s.action && event.Status == s.status {
		return errors.New("forced group-settings audit failure")
	}
	return s.Store.SaveAuditEvent(event)
}

func TestGroupSettingsHandlersRequireDurableAudit(t *testing.T) {
	t.Run("runtime intent failure prevents mutation", func(t *testing.T) {
		server, store := newRuntimeOverrideTestServer(t)
		test := createQueuedGroupSettingsTest(t, server)
		view, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
		if err != nil {
			t.Fatal(err)
		}
		server.store = storeOverlay(&groupSettingsAuditFailureStore{
			Store: store, action: "test.runtime_settings.update", status: "pending",
		})
		body, _ := json.Marshal(map[string]any{
			"expected_revision": view.Revision,
			"fields":            []string{"pacing"},
			"runtime_settings":  map[string]any{"pacing": map[string]any{"type": "fixed_delay", "delay_ms": 9876}},
		})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+test.ID+"/groups/Buyers/runtime-settings", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit-intent failure status = %d, want 503 body=%s", rec.Code, rec.Body.String())
		}
		current, _ := server.runner.GetTest(test.ID)
		if current.Groups[0].RuntimeSettings.Pacing.DelayMs != 500 {
			t.Fatalf("mutation ran without durable audit intent: %#v", current.Groups[0].RuntimeSettings.Pacing)
		}
	})

	t.Run("parameter success outcome failure withholds 200", func(t *testing.T) {
		server, store := newRuntimeOverrideTestServer(t)
		test := createQueuedGroupSettingsTest(t, server)
		view, err := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
		if err != nil {
			t.Fatal(err)
		}
		parameters := cloneScriptParameters(view.Parameters)
		parameters[1].Selection = "sequential"
		server.store = storeOverlay(&groupSettingsAuditFailureStore{
			Store: store, action: "test.parameters.update", status: "success",
		})
		body, _ := json.Marshal(map[string]any{
			"expected_revision": view.Revision,
			"parameters":        parameters,
		})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tests/"+test.ID+"/groups/Buyers/parameters", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit-outcome failure status = %d, want 503 body=%s", rec.Code, rec.Body.String())
		}
		current, _ := server.runner.GetTest(test.ID)
		if current.Groups[0].script.Parameters[1].Selection != "sequential" {
			t.Fatal("test setup did not reach the post-mutation audit failure")
		}
	})
}

func TestUpdateTestGroupParametersStaleHTTPClientGetsConflict(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	test := createQueuedGroupSettingsTest(t, server)
	first, _ := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	second, _ := server.runner.GetTestGroupParametersView(test.ID, "Buyers")
	firstParameters := cloneScriptParameters(first.Parameters)
	firstParameters[1].Selection = "sequential"
	firstBody, _ := json.Marshal(map[string]any{"expected_revision": first.Revision, "parameters": firstParameters})
	firstReq := httptest.NewRequest(http.MethodPut, "/api/v1/tests/"+test.ID+"/groups/Buyers/parameters", bytes.NewReader(firstBody))
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	server.mux.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first update = %d body=%s", firstRec.Code, firstRec.Body.String())
	}

	secondParameters := cloneScriptParameters(second.Parameters)
	secondParameters[0].Selection = "random"
	secondBody, _ := json.Marshal(map[string]any{"expected_revision": second.Revision, "parameters": secondParameters})
	secondReq := httptest.NewRequest(http.MethodPut, "/api/v1/tests/"+test.ID+"/groups/Buyers/parameters", bytes.NewReader(secondBody))
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	server.mux.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409 body=%s", secondRec.Code, secondRec.Body.String())
	}
}
