package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestTruClientRuntimeTimeoutBounds(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int
		valid bool
	}{{"1.5", 1500, true}, {"3600", 3600000, true}, {"0", 0, false}, {"-1", 0, false}, {"3601", 0, false}, {"NaN", 0, false}, {"+Inf", 0, false}, {"0.0001", 0, false}, {"bad", 0, false}} {
		got, ok := truClientTimeoutMS(test.value)
		if got != test.want || ok != test.valid {
			t.Fatalf("timeout(%q)=%d/%v want%d/%v", test.value, got, ok, test.want, test.valid)
		}
	}
}

func TestTruClientMultipleXMLSelectionAndSourceMappingsAlwaysAgree(t *testing.T) {
	files := map[string]string{
		"Review/Review.usr": "[General]\nType=TruClientWeb\n",
		"z/default.xml":     `<truScript><step action="Navigate" stepId="wrong" arguments='{&quot;Location&quot;:{&quot;value&quot;:&quot;https://wrong.invalid/&quot;}}'/></truScript>`,
		"a/default.xml":     "<truScript>\n\n<step action=\"Navigate\" stepId=\"selected\" arguments='{&quot;Location&quot;:{&quot;value&quot;:&quot;https://selected.invalid/&quot;}}'/>\n</truScript>",
	}
	for iteration := 0; iteration < 30; iteration++ {
		result, err := ConvertZip(zipFilesForTest(t, files), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		action := result.Script.Actions[0]
		if action.Metadata["source_step_id"] != "selected" || action.Metadata["source_file"] != "a/default.xml" {
			t.Fatal("conversion selected a different XML document")
		}
		found := false
		for _, mapping := range result.SourceReview.Mappings {
			if mapping.TargetKind == "action" && mapping.TargetPath == "actions[0]" {
				found = true
				if mapping.SourceFile != "a/default.xml" || mapping.StartLine != 3 {
					t.Fatalf("mapping selected a different XML document: %+v", mapping)
				}
			}
		}
		if !found {
			t.Fatal("selected XML action mapping missing")
		}
		gate := false
		for _, init := range result.Script.Init {
			if init.Metadata["source_file"] == "a/default.xml" && init.Metadata["execution_blocker"] != nil {
				gate = true
			}
		}
		if !gate {
			t.Fatal("ambiguous source selection lost its durable execution gate")
		}
	}
}

func truClientReviewZIP(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := map[string]string{
		"Review/Review.usr":   "[General]\nType=TruClientWeb\nParameterFile=Review.prm\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n",
		"Review/default.cfg":  "[General]\nDefaultRunLogic=default.usp\n[TruClient]\nStepTimeout=180\nObjectTimeout=20\nSimulateNewUser=1\nApplyWinSizeByUser=0\nWindowWidth=1280\nWindowHeight=1024\nUseRecordedDuration=0\nNetworkEndThreshold=150\n[TruClientEx]\nUnknownBrowserPolicy=1\n",
		"Review/default.usp":  "[RunLogicRunRoot]\nRunLogicNumOfIterations=1\nRunLogicPaceType=ASAP\n",
		"Review/Review.prm":   "[parameter:AccountID]\nParamType=Table\nTable=accounts.dat\nColumnName=AccountID\nSelectNextRow=Unique\nStartRow=1\nUpdateMethod=EachIteration\nOutOfRangePolicy=Cyclic\n",
		"Review/accounts.dat": "AccountID\n100001\n100002\n",
		"Review/default.xml":  `<truScript><step action="action" actionName="Action"><step action="Type" stepId="type" objectTimeout="30" arguments='{&quot;Selector&quot;:{&quot;value&quot;:&quot;#account&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;AccountID&quot;,&quot;param&quot;:true}}'/><step action="Select" stepId="select" arguments='{&quot;Selector&quot;:{&quot;value&quot;:&quot;#choice&quot;},&quot;Ordinal&quot;:{&quot;value&quot;:3}}'/><step action="Click" stepId="disabled" disabled="true"/><step action="Wait" stepId="wait" arguments='{&quot;Interval&quot;:{&quot;value&quot;:2}}'/></step><transactions><transaction name="Selection"><vertex type="start" progressType="Step started" step="type"/><vertex type="end" progressType="After step ended" step="select"/></transaction></transactions></truScript>`,
	}
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestPlannedTruClientBrowserDraftFinalizesSettingsParametersAndProvenance(t *testing.T) {
	result, err := ConvertZip(truClientReviewZIP(t), ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || result.Script.Protocol != "browser" || result.ReviewArtifact == nil || result.DetectedProtocol.ImportMode != ImportModeReviewOnly {
		t.Fatalf("planned browser draft missing: mode=%v", result.DetectedProtocol)
	}
	script := result.Script
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("validation=%v", issues)
	}
	if len(script.Init) == 0 || script.Init[0].Metadata["execution_blocker"] == nil {
		t.Fatal("planned profile execution gate missing")
	}
	if len(script.Parameters) != 1 || len(script.DataSources) != 1 || script.Config.Browser.NewUserPerIter == nil || !*script.Config.Browser.NewUserPerIter {
		t.Fatal("data or browser settings missing")
	}
	var typing, selecting *dsl.Action
	var waiting *dsl.Action
	for i := range script.Actions {
		action := &script.Actions[i]
		if action.Metadata["source_step_id"] == "disabled" {
			t.Fatal("disabled step became executable")
		}
		if action.Type == "browser_fill" {
			typing = action
		}
		if action.Type == "browser_select" {
			selecting = action
		}
		if action.Type == "browser_wait" {
			waiting = action
		}
	}
	if typing == nil || typing.Value != "{{param.accountid}}" || typing.TimeoutMs != 180000 || typing.Params["timeout_ms"] != 30000 {
		t.Fatalf("typed source contract=%+v", typing)
	}
	if selecting == nil || selecting.Params["option_index"] != 2 || selecting.Params["timeout_ms"] != 20000 {
		t.Fatalf("select contract=%+v", selecting)
	}
	if waiting == nil || waiting.WaitFor.TimeoutMs != 180000 || waiting.Params["timeout_ms"] != 180000 {
		t.Fatalf("duration wait inherited object timeout: %+v", waiting)
	}
	if result.Stats.Transactions != 1 || result.Stats.Skipped != 1 {
		t.Fatalf("stats=%+v", result.Stats)
	}
	preserved := script.Metadata.Import.Preserved
	rows := preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)
	count := 0
	for _, sections := range rows {
		for _, keys := range sections {
			count += len(keys)
			for _, row := range keys {
				if row.Status == "" {
					t.Fatal("unclassified source setting")
				}
			}
		}
	}
	if count != 12 {
		t.Fatalf("runtime keys=%d want12", count)
	}
	if rows["default.cfg"]["TruClient"]["StepTimeout"].Status != RuntimeSettingDispositionMapped || rows["default.cfg"]["TruClient"]["WindowWidth"].Status != RuntimeSettingDispositionNonApplicable || rows["default.cfg"]["TruClientEx"]["UnknownBrowserPolicy"].Status != RuntimeSettingDispositionCompatibilityOnly {
		t.Fatal("runtime support claims incorrect")
	}
	markerMappings := 0
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "Transaction definition" {
			markerMappings++
		}
	}
	if markerMappings != 2 {
		t.Fatalf("transaction source mappings=%d", markerMappings)
	}
	encoded, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	var decoded dsl.Script
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Parameters) != 1 || len(decoded.DataSources) != 1 || len(decoded.Init) == 0 || decoded.Init[0].Metadata["execution_blocker"] == nil {
		t.Fatal("serialized authoring draft lost data or gates")
	}
	if _, err := engine.New(engine.Config{Script: &decoded, VUsers: 1, Duration: time.Second}); err == nil || !strings.Contains(err.Error(), "truclient_planned_profile_requires_review") {
		t.Fatalf("engine preflight did not reject serialized planned profile: %v", err)
	}
	if _, err := engine.ReplayScript(context.Background(), &decoded, engine.ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "truclient_planned_profile_requires_review") {
		t.Fatalf("replay preflight did not reject serialized planned profile: %v", err)
	}
}

func TestTruClientRuntimeDoesNotClaimHTTPNetworkShaping(t *testing.T) {
	for _, value := range []string{"0", "1"} {
		result := &ConvertResult{Script: &dsl.Script{Name: "Network review", Protocol: "browser"}}
		applyTruClientBrowserRuntimeSettings(result, "default.cfg", nil, map[string]string{"default.cfg": "[ModemSpeed]\nEnableModemSpeed=" + value + "\n"})
		rows := map[string]map[string]map[string]RuntimeSettingDisposition{"default.cfg": {"ModemSpeed": {"EnableModemSpeed": {Status: RuntimeSettingDispositionMapped}}}}
		refineTruClientBrowserRuntimeDispositions(rows, result.Script)
		want := RuntimeSettingDispositionCompatibilityOnly
		if value == "0" {
			want = RuntimeSettingDispositionNonApplicable
		}
		if rows["default.cfg"]["ModemSpeed"]["EnableModemSpeed"].Status != want {
			t.Fatalf("source selector %s=%+v", value, rows)
		}
	}
}
