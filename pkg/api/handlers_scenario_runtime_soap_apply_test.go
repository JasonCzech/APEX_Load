package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func soapScriptForSOAPApply(id, password string) *dsl.Script {
	script := httpScript(id, id)
	script.Protocol = "soap"
	script.Config.SOAP = dsl.SOAPConfig{Version: "1.1"}
	if password != "" {
		script.Config.SOAP.WSSecurity = &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: id + "-user", Password: password, PasswordType: "text"}}
	}
	return script
}

func soapTokenForSOAPApply(o *RuntimeSettingsOverride) *dsl.SOAPUsernameTokenConfig {
	if o == nil || o.SOAP == nil || o.SOAP.WSSecurity == nil {
		return nil
	}
	return o.SOAP.WSSecurity.UsernameToken
}

// Apply-to-groups copies the SOAP section but never the source group's
// WS-Security credentials: the target keeps its own (sealed) credential and never
// receives the source group's controller-owned reference or script literal.
func TestApplyScenarioRuntimeToGroupsKeepsTargetSOAPCredentials(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(soapScriptForSOAPApply("soap-source", "source-script-literal"))
	server.runner.StoreScript(soapScriptForSOAPApply("soap-target", ""))

	soapOverride := func(version, username, password string) *RuntimeSettingsOverride {
		return &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{
			Version: version,
			WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{
				Username: username, Password: password, PasswordType: "text", Nonce: true,
			}},
		}}
	}
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "SOAP apply credentials",
		Duration: "1m",
		Groups: []CreateGroupEntry{
			{Name: "Source", ScriptID: "soap-source", VUsers: 1, RuntimeSettings: soapOverride("1.2", "src-user", "source-typed-secret")},
			{Name: "Target", ScriptID: "soap-target", VUsers: 1, RuntimeSettings: soapOverride("1.1", "tgt-user", "target-typed-secret")},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	stored := server.runner.scenarios[scenario.ID]
	sourceSealed := soapTokenForSOAPApply(stored.Scripts[0].RuntimeSettings)
	targetSealed := soapTokenForSOAPApply(stored.Scripts[1].RuntimeSettings)
	if sourceSealed == nil || targetSealed == nil || !isReservedRuntimeOverrideSecretReference(sourceSealed.Password) || !isReservedRuntimeOverrideSecretReference(targetSealed.Password) {
		t.Fatalf("precondition: typed passwords must be sealed, source=%#v target=%#v", sourceSealed, targetSealed)
	}
	sourcePasswordRef, sourceUsername, targetPasswordRef, targetUsername := sourceSealed.Password, sourceSealed.Username, targetSealed.Password, targetSealed.Username

	_, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{
		SourceGroup:  0,
		TargetGroups: []int{1},
		Fields:       []string{"soap"},
	})
	if err != nil || !ok {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v", ok, err)
	}
	after := server.runner.scenarios[scenario.ID]
	for _, index := range []int{1} {
		runtime := after.Scripts[index].RuntimeSettings
		if runtime == nil || runtime.SOAP == nil || runtime.SOAP.Version != "1.2" {
			t.Fatalf("group %d SOAP = %#v, want copied version 1.2", index, runtime)
		}
		token := soapTokenForSOAPApply(runtime)
		if token == nil || !token.Nonce || token.PasswordType != "text" {
			t.Fatalf("group %d token settings = %#v, want copied nonce/password_type", index, token)
		}
		if token.Password == sourcePasswordRef || strings.Contains(token.Password, "source-script-literal") || token.Username == sourceUsername || token.Username == "src-user" {
			t.Fatalf("group %d received the source group's credentials: %#v", index, token)
		}
	}
	if token := soapTokenForSOAPApply(after.Scripts[1].RuntimeSettings); token.Password != targetPasswordRef || token.Username != targetUsername {
		t.Fatalf("target credentials = %#v, want its own %q/%q kept", token, targetUsername, targetPasswordRef)
	}
}

// A target with no WS-Security credentials of its own is rejected atomically
// rather than handed the source group's credential.
func TestApplyScenarioRuntimeToGroupsRejectsSOAPTargetWithoutCredentials(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(soapScriptForSOAPApply("soap-src2", ""))
	server.runner.StoreScript(soapScriptForSOAPApply("soap-bare2", ""))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "SOAP apply bare target",
		Duration: "1m",
		Groups: []CreateGroupEntry{
			{Name: "Source", ScriptID: "soap-src2", VUsers: 1, RuntimeSettings: &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{
				Version:    "1.2",
				WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "src-user", Password: "source-typed-secret", PasswordType: "text"}},
			}}},
			{Name: "Bare", ScriptID: "soap-bare2", VUsers: 1},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	_, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"soap"}})
	if !ok || err == nil || !strings.Contains(err.Error(), "not copied between groups") {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v, want credentials-not-copied rejection", ok, err)
	}
	if runtime := server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings; soapTokenForSOAPApply(runtime) != nil {
		t.Fatalf("rejected apply persisted target SOAP credentials: %#v", runtime.SOAP)
	}
}

// effectiveTargetSOAPForSOAPApply resolves a group's SOAP config the way a run does:
// scenario root merged with the group override, applied onto the group script.
func effectiveTargetSOAPForSOAPApply(t *testing.T, server *Server, scenarioID string, group int) dsl.SOAPConfig {
	t.Helper()
	stored := server.runner.scenarios[scenarioID]
	script := cloneViaJSON(server.runner.scripts[stored.Scripts[group].ScriptID].Script)
	if err := applyRuntimeOverrides(script, mergeRuntimeSettings(stored.RuntimeSettings, stored.Scripts[group].RuntimeSettings)); err != nil {
		t.Fatalf("applyRuntimeOverrides: %v", err)
	}
	return script.Config.SOAP
}

func assertNoSOAPCredentialInRowForSOAPApply(t *testing.T, runtime *RuntimeSettingsOverride, forbidden ...string) {
	t.Helper()
	data, err := json.Marshal(runtime)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	row := string(data)
	if strings.Contains(row, "APEX_RUNTIME_OVERRIDE") || strings.Contains(row, "{{secret.") {
		t.Fatalf("target row holds a copied secret reference: %s", row)
	}
	for _, value := range forbidden {
		if strings.Contains(row, value) {
			t.Fatalf("target row holds credential %q: %s", value, row)
		}
	}
	if token := soapTokenForSOAPApply(runtime); token == nil || token.Username != "" || token.Password != "" {
		t.Fatalf("target row token = %#v, want copied settings with no credentials", token)
	}
	if slices.Contains(runtime.OverrideFields, "soap") ||
		overrideFieldsCoverPath(runtime.OverrideFields, soapCredentialFieldPaths[0]) ||
		overrideFieldsCoverPath(runtime.OverrideFields, soapCredentialFieldPaths[1]) {
		t.Fatalf("target override_fields %v own the credential leaves; they must stay inherited", runtime.OverrideFields)
	}
}

// A target whose WS-Security credentials come from its own script keeps
// inheriting them: the script literal is neither written unsealed into the
// target row nor pinned there, and the source credentials never land there.
func TestApplyScenarioRuntimeToGroupsTargetInheritsScriptSOAPCredentials(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(soapScriptForSOAPApply("soap-src3", ""))
	server.runner.StoreScript(soapScriptForSOAPApply("soap-tgt3", "target-script-literal"))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "SOAP apply script-inherited target",
		Duration: "1m",
		Groups: []CreateGroupEntry{
			{Name: "Source", ScriptID: "soap-src3", VUsers: 1, RuntimeSettings: &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{
				Version: "1.2",
				WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: "src-user", Password: "source-typed-secret", PasswordType: "digest", Nonce: true, Created: true,
				}},
			}}},
			{Name: "Target", ScriptID: "soap-tgt3", VUsers: 1},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	if _, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"soap"}}); err != nil || !ok {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v", ok, err)
	}
	assertNoSOAPCredentialInRowForSOAPApply(t, server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings, "target-script-literal", "src-user", "source-typed-secret")

	effective := effectiveTargetSOAPForSOAPApply(t, server, scenario.ID, 1)
	if effective.Version != "1.2" || effective.WSSecurity == nil || effective.WSSecurity.UsernameToken == nil {
		t.Fatalf("effective target SOAP = %#v, want copied version 1.2 with a UsernameToken", effective)
	}
	token := effective.WSSecurity.UsernameToken
	if token.Username != "soap-tgt3-user" || token.Password != "target-script-literal" {
		t.Fatalf("effective target credentials = %q/%q, want inherited script credentials", token.Username, token.Password)
	}
	if token.PasswordType != "digest" || !token.Nonce || !token.Created {
		t.Fatalf("effective target token settings = %#v, want copied digest/nonce/created", token)
	}

	// The credential is inherited, not pinned: a later script change reaches the group.
	server.runner.StoreScript(soapScriptForSOAPApply("soap-tgt3", "rotated-script-literal"))
	if got := effectiveTargetSOAPForSOAPApply(t, server, scenario.ID, 1).WSSecurity.UsernameToken.Password; got != "rotated-script-literal" {
		t.Fatalf("effective target password after script change = %q, want rotated-script-literal", got)
	}
}

// A target whose WS-Security credentials come from the scenario root keeps
// inheriting the root's sealed reference instead of receiving a copy of it (or
// the source group's reference) in its own group row.
func TestApplyScenarioRuntimeToGroupsTargetInheritsRootSOAPCredentials(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(soapScriptForSOAPApply("soap-src4", ""))
	server.runner.StoreScript(soapScriptForSOAPApply("soap-tgt4", ""))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "SOAP apply root-inherited target",
		Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{
			Version:    "1.1",
			WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "root-user", Password: "root-typed-secret", PasswordType: "text"}},
		}},
		Groups: []CreateGroupEntry{
			{Name: "Source", ScriptID: "soap-src4", VUsers: 1, RuntimeSettings: &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{
				Version:    "1.2",
				WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "src-user", Password: "source-typed-secret", PasswordType: "text", Nonce: true}},
			}}},
			{Name: "Target", ScriptID: "soap-tgt4", VUsers: 1},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	rootToken := soapTokenForSOAPApply(server.runner.scenarios[scenario.ID].RuntimeSettings)
	if rootToken == nil || !isReservedRuntimeOverrideSecretReference(rootToken.Password) {
		t.Fatalf("precondition: root password must be sealed, got %#v", rootToken)
	}
	if _, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"soap"}}); err != nil || !ok {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v", ok, err)
	}
	assertNoSOAPCredentialInRowForSOAPApply(t, server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings, "root-user", "src-user", "root-typed-secret", "source-typed-secret")

	effective := effectiveTargetSOAPForSOAPApply(t, server, scenario.ID, 1)
	if effective.Version != "1.2" || effective.WSSecurity == nil || effective.WSSecurity.UsernameToken == nil {
		t.Fatalf("effective target SOAP = %#v, want copied version 1.2 with a UsernameToken", effective)
	}
	if token := effective.WSSecurity.UsernameToken; token.Username != rootToken.Username || token.Password != rootToken.Password || !token.Nonce {
		t.Fatalf("effective target token = %#v, want root credentials %q/%q with copied nonce", token, rootToken.Username, rootToken.Password)
	}
}

// keepTargetSOAPCredentials turns a v1 target row into a partial row when the
// target inherits its WS-Security credentials. The converted row must apply
// every non-SOAP field exactly as the v1 row did, including v1's rule that a
// section wins over its compact alias (log over log_level, run_logic over
// iterations/auto_transactions, browser_config over the compact browser).
func TestKeepTargetSOAPCredentialsPreservesV1AliasPrecedence(t *testing.T) {
	seven := 7
	headless := true
	prior := &RuntimeSettingsOverride{
		Log:              &dsl.LogConfig{OnErrorOnly: true},
		LogLevel:         "debug",
		RunLogic:         &dsl.RunLogicConfig{WorkloadModel: "closed"},
		Iterations:       &seven,
		AutoTransactions: "per_action",
		BrowserConfig:    &dsl.BrowserConfig{DeviceProfile: "desktop"},
		Browser:          &RuntimeBrowserOverride{Headless: &headless, DeviceProfile: "iphone", Orientation: "landscape"},
	}
	source := &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{Version: "1.2", WSSecurity: &dsl.SOAPWSSecurityConfig{
		UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "src-user", Password: "src-pass", PasswordType: "text"},
	}}}
	next := keepTargetSOAPCredentials(replaceRuntimeFields(prior, source, []string{"soap"}), prior)
	if len(next.OverrideFields) == 0 {
		t.Fatalf("precondition: inherited credentials must convert the row to override_fields")
	}
	base := func() *dsl.Script {
		s := httpScript("alias", "alias")
		s.Config.Log.Level = "info"
		s.Config.RunLogic.Iterations = 3
		s.Config.RunLogic.AutoTransactions = "none"
		s.Config.Browser.Orientation = "portrait"
		return s
	}
	legacy, converted := base(), base()
	if err := applyRuntimeOverrides(legacy, prior); err != nil {
		t.Fatalf("apply v1 row: %v", err)
	}
	if err := applyRuntimeOverrides(converted, next); err != nil {
		t.Fatalf("apply converted row: %v", err)
	}
	legacy.Config.SOAP, converted.Config.SOAP = dsl.SOAPConfig{}, dsl.SOAPConfig{}
	want, _ := json.Marshal(legacy.Config)
	got, _ := json.Marshal(converted.Config)
	if string(want) != string(got) {
		t.Fatalf("converted row changed non-SOAP settings:\n v1:        %s\n converted: %s", want, got)
	}
}

// A v1 scenario-root row merged beside a partial group row must apply exactly
// what it applied beside the v1 group row: its sections keep winning over
// their compact aliases. The SOAP apply-to-groups conversion is one way a
// group row becomes partial; any partial group row reaches the same merge.
func TestPartialGroupMergeKeepsV1RootAliasPrecedence(t *testing.T) {
	seven := 7
	headless := true
	root := func() *RuntimeSettingsOverride {
		return &RuntimeSettingsOverride{
			Log:              &dsl.LogConfig{OnErrorOnly: true},
			LogLevel:         "debug",
			RunLogic:         &dsl.RunLogicConfig{WorkloadModel: "closed"},
			Iterations:       &seven,
			AutoTransactions: "per_action",
			BrowserConfig:    &dsl.BrowserConfig{DeviceProfile: "desktop"},
			Browser:          &RuntimeBrowserOverride{Headless: &headless, Orientation: "landscape"},
		}
	}
	source := &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{Version: "1.2", WSSecurity: &dsl.SOAPWSSecurityConfig{
		UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "u", Password: "p", PasswordType: "text"},
	}}}
	v1Group := replaceRuntimeFields(nil, source, []string{"soap"})
	converted := keepTargetSOAPCredentials(replaceRuntimeFields(nil, source, []string{"soap"}), nil)
	if len(v1Group.OverrideFields) != 0 || len(converted.OverrideFields) == 0 {
		t.Fatalf("precondition: v1 group fields=%v, converted group fields=%v", v1Group.OverrideFields, converted.OverrideFields)
	}
	base := func() *dsl.Script {
		s := httpScript("alias", "alias")
		s.Config.Log.Level = "info"
		s.Config.RunLogic.Iterations = 3
		s.Config.RunLogic.AutoTransactions = "none"
		s.Config.Browser.Orientation = "portrait"
		return s
	}
	for name, group := range map[string]*RuntimeSettingsOverride{
		"soap conversion":     converted,
		"plain partial group": {OverrideFields: []string{"soap.version"}, SOAP: &dsl.SOAPConfig{Version: "1.2"}},
	} {
		t.Run(name, func(t *testing.T) {
			legacy, partial := base(), base()
			if err := applyRuntimeOverrides(legacy, mergeRuntimeSettings(root(), v1Group)); err != nil {
				t.Fatalf("apply v1 merge: %v", err)
			}
			rootRow := root()
			if err := applyRuntimeOverrides(partial, mergeRuntimeSettings(rootRow, group)); err != nil {
				t.Fatalf("apply partial merge: %v", err)
			}
			if rootRow.LogLevel != "debug" || rootRow.Iterations == nil || rootRow.Browser == nil {
				t.Fatal("merge modified the stored v1 root row")
			}
			legacy.Config.SOAP, partial.Config.SOAP = dsl.SOAPConfig{}, dsl.SOAPConfig{}
			want, _ := json.Marshal(legacy.Config)
			got, _ := json.Marshal(partial.Config)
			if string(want) != string(got) {
				t.Fatalf("v1 root applies differently beside a partial group row:\n v1 group:      %s\n partial group: %s", want, got)
			}
		})
	}
}
