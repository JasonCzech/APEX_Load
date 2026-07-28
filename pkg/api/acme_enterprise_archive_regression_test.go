package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
)

func TestSanitizedACMEArchivePreservesSafeBodyAndRuntimeProfile(t *testing.T) {
	archive := buildSanitizedACMEArchive(t)

	result, err := enterprisearchive.ConvertZip(archive, enterprisearchive.ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.Script == nil {
		t.Fatal("ConvertZip returned no script")
	}
	protectedRequirements := result.ProtectedRequirements.Take()
	if len(protectedRequirements) != 0 {
		t.Fatalf("protected requirements = %#v, want no custom-body binding", protectedRequirements)
	}
	repeated, err := enterprisearchive.ConvertZip(archive, enterprisearchive.ConvertOptions{})
	if err != nil {
		t.Fatalf("repeat ConvertZip: %v", err)
	}
	repeatedRequirements := repeated.ProtectedRequirements.Take()
	if len(repeatedRequirements) != 0 {
		t.Fatalf("repeat protected requirements = %#v, want none", repeatedRequirements)
	}
	publicResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal public converter result: %v", err)
	}
	if !strings.Contains(string(publicResult), "synthetic-value") {
		t.Fatalf("executable converter result lost imported password: %s", publicResult)
	}
	if strings.Contains(string(publicResult), "{{secret.") {
		t.Fatalf("converter result replaced imported password with a binding: %s", publicResult)
	}

	projected := maskScriptSecrets(result.Script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned no projected script")
	}
	requirements := discoverSecretBindingRequirements(projected, 0)
	if len(requirements) != 0 {
		t.Fatalf("pre-governed secret reference produced legacy redaction requirements: %#v", requirements)
	}
	body := acmeImportedRequestBodyContaining(t, projected, "<soap:Envelope")
	for _, want := range []string{
		"http://safe.example.test:18000/Services/ACME_ISBA_WEB.jws",
		"http://schemas.xmlsoap.org/soap/envelope/",
		"http://www.w3.org/2001/XMLSchema-instance",
		"{{param.ctn}}",
		"{{param.username}}",
		"{{param.password}}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("imported body lost %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "{{secret.") {
		t.Fatalf("parameterized request body gained a false secret reference:\n%s", body)
	}

	parameters := make(map[string]dsl.Parameter, len(projected.Parameters))
	for _, parameter := range projected.Parameters {
		parameters[parameter.Name] = parameter
	}
	ctn, ok := parameters["ctn"]
	if !ok {
		t.Fatalf("CTN parameter missing from %#v", projected.Parameters)
	}
	if ctn.Selection != "random" || ctn.Update != "each_iteration" || ctn.BlockSize != 0 {
		t.Fatalf("CTN parameter = %#v, want random/each_iteration over the full table with no block_size", ctn)
	}
	password, ok := parameters["password"]
	if !ok {
		t.Fatalf("password runtime parameter missing from %#v", projected.Parameters)
	}
	if password.Type != "file" || password.DataSource == "" || password.Column == "" {
		t.Fatalf("password parameter = %#v, want file-backed governed runtime data", password)
	}
	var ctnSource *dsl.DataSource
	for index := range projected.DataSources {
		if projected.DataSources[index].Name == ctn.DataSource {
			ctnSource = &projected.DataSources[index]
			break
		}
	}
	if ctnSource == nil {
		t.Fatalf("CTN data source %q missing from %#v", ctn.DataSource, projected.DataSources)
	}
	for _, row := range []string{"2025550101", "2025550102", "2025550103"} {
		if !strings.Contains(ctnSource.InlineData, row) {
			t.Errorf("CTN data source lost row %q: %q", row, ctnSource.InlineData)
		}
	}
	var passwordSource *dsl.DataSource
	for index := range projected.DataSources {
		if projected.DataSources[index].Name == password.DataSource {
			passwordSource = &projected.DataSources[index]
			break
		}
	}
	if passwordSource == nil {
		t.Fatalf("password data source %q missing from %#v", password.DataSource, projected.DataSources)
	}
	if strings.Contains(passwordSource.InlineData, "synthetic-value") ||
		!strings.Contains(passwordSource.InlineData, maskedScriptSecret) {
		t.Fatalf("password data source was not redacted while preserving its runtime shape: %#v", passwordSource)
	}
	requestAction := acmeImportedActionContaining(t, projected, "<soap:Envelope")
	if blocker := requestAction.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("masked read projection unexpectedly changed stored execution policy: %#v", requestAction.Metadata)
	}
	if issues := dsl.ValidateScriptIssues(projected); len(issues) != 0 {
		t.Fatalf("projected script validation issues = %#v", issues)
	}

	config := projected.Config
	if config.ThinkTime.Type != "recorded" || config.ThinkTime.Multiplier != 1 || config.ThinkTime.Ignore {
		t.Fatalf("think time = %#v, want replay-as-recorded with multiplier 1", config.ThinkTime)
	}
	if config.Pacing.Type != "fixed_interval" || config.Pacing.IntervalMs != 5000 {
		t.Fatalf("pacing = %#v, want fixed_interval every 5000ms", config.Pacing)
	}
	if config.RunLogic.Iterations != 1 {
		t.Fatalf("iterations = %d, want authoritative default.usp value 1", config.RunLogic.Iterations)
	}

	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatalf("import metadata missing from converted script: %#v", result.Script.Metadata)
	}
	// Use the converter result for typed metadata assertions. The response
	// projection intentionally JSON-round-trips extensible `any` metadata.
	preserved := result.Script.Metadata.Import.Preserved
	mappedFiles, ok := preserved["mapped_runtime_files"].([]string)
	if !ok {
		t.Fatalf("mapped_runtime_files = %#v, want []string", preserved["mapped_runtime_files"])
	}
	for _, want := range []string{"default.cfg", "default.usp"} {
		if !acmeContainsFold(mappedFiles, want) {
			t.Errorf("mapped_runtime_files = %#v, missing %q", mappedFiles, want)
		}
	}

	parity, ok := preserved["runtime_settings_parity"].([]enterprisearchive.RuntimeSettingParityEntry)
	if !ok {
		t.Fatalf("runtime_settings_parity = %#v, want typed parity entries", preserved["runtime_settings_parity"])
	}
	pacing := acmeRuntimeParityEntryByID(t, parity, "pacing")
	if pacing.Status != enterprisearchive.RuntimeSettingStatusExecuted || !acmeContainsFold(pacing.RuntimeFiles, "default.usp") {
		t.Fatalf("pacing parity = %#v, want executed with default.usp provenance", pacing)
	}
	unmapped := acmeRuntimeParityEntryByID(t, parity, "runtime_unmapped")
	if unmapped.Status != enterprisearchive.RuntimeSettingStatusPartial ||
		!strings.Contains(unmapped.Notes, `EnableSnapshotsInReplay="1"`) ||
		len(unmapped.RuntimeFiles) != 1 ||
		!strings.EqualFold(unmapped.RuntimeFiles[0], "default.cfg") {
		t.Fatalf("unmapped runtime parity = %#v, want review-required default.cfg source evidence", unmapped)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), "default.usp") {
			t.Fatalf("selected supported default.usp produced warning %q", warning)
		}
	}

	server, records, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source-secret migration: %v", err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5d}, 32),
		KeyID: "acme-parameter-protected-source-session",
	})
	if err != nil {
		t.Fatalf("create protected source-session wrapper: %v", err)
	}
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{
		Records: records,
		Wrapper: wrapper,
	}

	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)
	filePart, err := multipartWriter.CreateFormFile("file", "ACME_ISBA_Azure_2025.zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := filePart.Write(archive); err != nil {
		t.Fatalf("write multipart archive: %v", err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart request: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID,
		&requestBody,
	)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("enterprise archive import status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode enterprise archive import response: %v", err)
	}
	var responseRequirements []SecretBindingRequirement
	if payload, ok := envelope["secret_binding_requirements"]; !ok {
		t.Fatal("enterprise archive import response omitted secret_binding_requirements")
	} else if err := json.Unmarshal(payload, &responseRequirements); err != nil {
		t.Fatalf("decode secret_binding_requirements: %v", err)
	}
	if len(responseRequirements) != 0 {
		t.Fatalf("enterprise archive HTTP import requirements = %#v, want none", responseRequirements)
	}
	var editableScript dsl.Script
	if err := json.Unmarshal(envelope["script"], &editableScript); err != nil {
		t.Fatalf("decode editable enterprise script: %v", err)
	}
	var editablePasswordSource *dsl.DataSource
	for index := range editableScript.DataSources {
		if editableScript.DataSources[index].Name == password.DataSource {
			editablePasswordSource = &editableScript.DataSources[index]
			break
		}
	}
	if editablePasswordSource == nil ||
		!strings.Contains(editablePasswordSource.InlineData, "synthetic-value") {
		t.Fatalf("editable enterprise destination lost imported password bytes: %#v", editableScript.DataSources)
	}
	var reviewResult struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(envelope["result"], &reviewResult); err != nil {
		t.Fatalf("decode enterprise review result: %v", err)
	}
	var reviewPasswordSource *dsl.DataSource
	for index := range reviewResult.Script.DataSources {
		if reviewResult.Script.DataSources[index].Name == password.DataSource {
			reviewPasswordSource = &reviewResult.Script.DataSources[index]
			break
		}
	}
	if reviewPasswordSource == nil ||
		strings.Contains(reviewPasswordSource.InlineData, "synthetic-value") ||
		!strings.Contains(reviewPasswordSource.InlineData, maskedScriptSecret) {
		t.Fatalf("enterprise review copy was not disclosure-safe: %#v", reviewResult.Script.DataSources)
	}
	for _, field := range []string{
		"source_secret_session_id",
		"source_secret_session_expires_at",
		"source_secret_migration_allowed",
	} {
		if _, exists := envelope[field]; exists {
			t.Errorf("enterprise archive HTTP import unexpectedly emitted %q: %s", field, response.Body.String())
		}
	}

	savablePayload, err := json.Marshal(&editableScript)
	if err != nil {
		t.Fatalf("marshal projected ACME script for save: %v", err)
	}
	var savable dsl.Script
	if err := json.Unmarshal(savablePayload, &savable); err != nil {
		t.Fatalf("clone projected ACME script for save: %v", err)
	}
	savable.ID = ""
	savable.ProjectID = projectID
	createResponse := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{
		"project_id": projectID,
		"script":     &savable,
	})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf(
			"save imported ACME script status = %d, want %d; body=%s",
			createResponse.Code,
			http.StatusCreated,
			createResponse.Body.String(),
		)
	}
	scripts := server.runner.ListScripts()
	if len(scripts) != 1 || scripts[0] == nil || scripts[0].Script == nil {
		t.Fatalf("saved ACME scripts = %#v, want one durable script", scripts)
	}
	saved := scripts[0].Script
	if saved.Config.ThinkTime.Type != "recorded" ||
		saved.Config.Pacing.Type != "fixed_interval" ||
		saved.Config.Pacing.IntervalMs != 5000 ||
		saved.Config.RunLogic.Iterations != 1 {
		t.Fatalf("saved ACME runtime config = %#v", saved.Config)
	}
	savedAction := acmeImportedActionContaining(t, saved, "<soap:Envelope")
	if blocker := savedAction.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("saved ACME script unexpectedly blocks preserved credentials: %#v", savedAction.Metadata)
	}
	var savedPasswordSource *dsl.DataSource
	for index := range saved.DataSources {
		if saved.DataSources[index].Name == password.DataSource {
			savedPasswordSource = &saved.DataSources[index]
			break
		}
	}
	if savedPasswordSource == nil ||
		!strings.Contains(savedPasswordSource.InlineData, "synthetic-value") {
		t.Fatalf("saved ACME script lost executable password source: %#v", saved.DataSources)
	}
}

func buildSanitizedACMEArchive(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		"ACME_ISBA_Azure_2025.usr": `[General]
Type=Multi
DefaultCfg=default.cfg
ParameterFile=ACME_ISBA_Azure_2025.prm
ActiveTypes=QTWeb
ScriptLanguage=C

[Actions]
Action=Action.c

[CfgFiles]
Default Profile=default.cfg

[RunLogicFiles]
Default Profile=default.usp
`,
		"default.cfg": `[General]
DefaultRunLogic=default.usp
ContinueOnError=0

[Iterations]
NumOfIterations=9
IterationPace=IterationASAP

[ThinkTime]
Options=RECORDED
Factor=1
Limit=30
LimitFlag=0

[Log]
AutoLog=0
IncludeEnvInfo=1
LogDetail=1
LogOptions=LogExtended
MsgClassData=1
MsgClassFull=1
MsgClassParameters=1

[WEB]
Browser_Type=Microsoft Internet Explorer
BrowserAcceptEncoding=gzip, deflate, br
BrowserAcceptLanguage=None
BrowserPlatform=Windows
BrowserVer=10.0
CustomUserAgent=APEX-Sanitized-Importer-Test
UseCustomAgent=1
EnableHTTP2=1
HTTPVer=1.1
KeepAlive=1
SimulateCache=1
CacheHtmlPages=1
CacheAlwaysCheckForNewerPages=0
SearchForImages=1
SimulatePrefetchPrerender=0
EnableSnapshotsInReplay=1
`,
		"default.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations="1"
RunLogicPaceType="Const"
RunLogicPaceConstTime="5"
`,
		"ACME_ISBA_Azure_2025.prm": `[parameter:CTN]
ColumnName="CTN"
Delimiter=","
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueWithLast"
ParamName="CTN"
SelectNextRow="Random"
StartRow="1"
Table="CTN.dat"
Type="Table"
auto_allocate_block_size="1"
value_for_each_vuser=""

[parameter:UserName]
ColumnName="UserName"
Delimiter=","
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueWithLast"
ParamName="UserName"
SelectNextRow="Sequential"
StartRow="1"
Table="UserName.dat"
Type="Table"

[parameter:Password]
ColumnName="Password"
Delimiter=","
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueWithLast"
ParamName="Password"
SelectNextRow="Sequential"
StartRow="1"
Table="Password.dat"
Type="Table"
`,
		"CTN.dat":      "CTN\n2025550101\n2025550102\n2025550103\n",
		"UserName.dat": "UserName\nsynthetic-user\n",
		"Password.dat": "Password\nsynthetic-value\n",
		"Action.c": `Action()
{
	web_custom_request("ACME_ISBA_WEB_CUSTOM_REQUEST",
		"URL=https://safe.example.test/Services/ACME_ISBA_WEB.jws",
		"Method=POST",
		"EncType=text/xml",
		"Body=<!-- endpoint http://safe.example.test:18000/Services/ACME_ISBA_WEB.jws --><soap:Envelope xmlns:soap=\"http://schemas.xmlsoap.org/soap/envelope/\" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"><soap:Body><request><ctn>{CTN}</ctn><user>{UserName}</user><password>{Password}</password></request></soap:Body></soap:Envelope>",
		LAST);
	return 0;
}
`,
	}

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("zip.Write(%q): %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

func acmeImportedRequestBodyContaining(t *testing.T, script *dsl.Script, fragment string) string {
	return acmeImportedActionContaining(t, script, fragment).Body
}

func acmeImportedActionContaining(t *testing.T, script *dsl.Script, fragment string) dsl.Action {
	t.Helper()
	actions := make([]dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	actions = append(actions, script.Init...)
	actions = append(actions, script.Actions...)
	actions = append(actions, script.Teardown...)
	for _, action := range flattenACMEImportedActions(actions) {
		if strings.Contains(action.Body, fragment) {
			return action
		}
	}
	t.Fatalf("no imported action body contains %q; actions=%#v", fragment, actions)
	return dsl.Action{}
}

func acmeActionHasExecutionBlocker(action dsl.Action, want string) bool {
	if action.Metadata == nil {
		return false
	}
	switch blockers := action.Metadata["execution_blockers"].(type) {
	case []string:
		return acmeContainsFold(blockers, want)
	case []any:
		for _, blocker := range blockers {
			if value, ok := blocker.(string); ok && strings.EqualFold(value, want) {
				return true
			}
		}
	case string:
		return strings.EqualFold(blockers, want)
	}
	if blocker, _ := action.Metadata["execution_blocker"].(string); strings.EqualFold(blocker, want) {
		return true
	}
	return false
}

func flattenACMEImportedActions(actions []dsl.Action) []dsl.Action {
	var flattened []dsl.Action
	for _, action := range actions {
		flattened = append(flattened, action)
		if action.Control != nil {
			flattened = append(flattened, flattenACMEImportedActions(action.Control.Steps)...)
			flattened = append(flattened, flattenACMEImportedActions(action.Control.Else)...)
		}
		if action.Type != "http_sequence" || action.Params == nil {
			continue
		}
		var entries []any
		switch requests := action.Params["requests"].(type) {
		case []any:
			entries = requests
		case []map[string]any:
			entries = make([]any, 0, len(requests))
			for _, request := range requests {
				entries = append(entries, request)
			}
		}
		for _, entry := range entries {
			payload, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			var request dsl.Action
			if err := json.Unmarshal(payload, &request); err == nil {
				flattened = append(flattened, flattenACMEImportedActions([]dsl.Action{request})...)
			}
		}
	}
	return flattened
}

func acmeRuntimeParityEntryByID(t *testing.T, entries []enterprisearchive.RuntimeSettingParityEntry, id string) enterprisearchive.RuntimeSettingParityEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("runtime parity entry %q missing from %#v", id, entries)
	return enterprisearchive.RuntimeSettingParityEntry{}
}

func acmeContainsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}
