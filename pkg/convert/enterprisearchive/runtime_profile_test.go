package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestApplyRuntimeUSPMapsPacingModes(t *testing.T) {
	tests := []struct {
		mode string
		body string
		want RuntimeConfig
	}{
		{mode: "ASAP", want: RuntimeConfig{PacingType: "none"}},
		{mode: "Const", body: `RunLogicPaceConstTime="5"`, want: RuntimeConfig{PacingType: "fixed_interval", IntervalMs: 5000}},
		{mode: "Random", body: "RunLogicRandomPaceMin=\"1.25\"\nRunLogicRandomPaceMax=\"2.5\"", want: RuntimeConfig{PacingType: "random_interval", MinIntervalMs: 1250, MaxIntervalMs: 2500}},
		{mode: "ConstAfter", body: `RunLogicPaceConstAfterTime="3.5"`, want: RuntimeConfig{PacingType: "fixed_delay", DelayMs: 3500}},
		{mode: "After", body: "RunLogicAfterPaceMin=\"2\"\nRunLogicAfterPaceMax=\"4\"", want: RuntimeConfig{PacingType: "random_delay", MinDelayMs: 2000, MaxDelayMs: 4000}},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			rc := &RuntimeConfig{}
			content := "[RunLogicRunRoot]\nRunLogicNumOfIterations=\"1\"\nRunLogicPaceType=\"" + test.mode + "\"\n" + test.body
			applyRuntimeUSP(rc, "default.usp", content)
			if rc.Iterations != 1 || rc.PacingType != test.want.PacingType ||
				rc.DelayMs != test.want.DelayMs || rc.MinDelayMs != test.want.MinDelayMs ||
				rc.MaxDelayMs != test.want.MaxDelayMs || rc.IntervalMs != test.want.IntervalMs ||
				rc.MinIntervalMs != test.want.MinIntervalMs || rc.MaxIntervalMs != test.want.MaxIntervalMs {
				t.Fatalf("applyRuntimeUSP() = %#v, want pacing %#v and one iteration", rc, test.want)
			}
			if len(rc.RuntimeIssues) != 0 {
				t.Fatalf("unexpected issues: %#v", rc.RuntimeIssues)
			}
		})
	}
}

func TestResolveRuntimeConfigUsesActiveCfgAndAuthoritativeUSP(t *testing.T) {
	files := map[string]string{
		"script.usr": `[General]
DefaultCfg=active.cfg
[CfgFiles]
Default Profile=active.cfg
[RunLogicFiles]
Default Profile=selected.usp`,
		"active.cfg": `[General]
DefaultRunLogic=selected.usp
[Iterations]
NumOfIterations=9
[ThinkTime]
Options=RECORDED
Factor=1
Limit=12
LimitFlag=0`,
		"stale.cfg": `[Pacing]
Mode=fixed_delay
DelayMs=9999`,
		"selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations="1"
RunLogicPaceType="Const"
RunLogicPaceConstTime="5"`,
	}
	project, err := ParseUSRFile(files["script.usr"])
	if err != nil {
		t.Fatal(err)
	}
	name, rc := resolveRuntimeConfig(files, project)
	if name != "active.cfg" {
		t.Fatalf("runtime cfg = %q, want active.cfg", name)
	}
	if rc.Iterations != 1 || rc.PacingType != "fixed_interval" || rc.IntervalMs != 5000 {
		t.Fatalf("runtime mapping = %#v", rc)
	}
	if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 || rc.ThinkTimeLimitMs != 0 {
		t.Fatalf("think-time mapping = %#v", rc)
	}
	if strings.Join(rc.RuntimeFiles, ",") != "active.cfg,selected.usp" {
		t.Fatalf("runtime provenance = %#v", rc.RuntimeFiles)
	}
}

func TestResolveRuntimeConfigProfileConflictUsesDefaultsWithoutReadingEitherUSP(t *testing.T) {
	files := map[string]string{
		"active.cfg": `[General]
DefaultRunLogic=direct.usp`,
		"direct.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations="bad"
RunLogicPaceType="After"
RunLogicAfterPaceMin="10"
RunLogicAfterPaceMax="2"`,
		"mapped.usp": `[RunLogicRunRoot]
RunLogicPaceType="ASAP"`,
	}
	project := &ProjectConfig{
		RuntimeFile:      "active.cfg",
		CfgProfiles:      map[string]string{"Profile": "active.cfg"},
		RunLogicProfiles: map[string]string{"Profile": "mapped.usp"},
	}
	cfgName, rc := resolveRuntimeConfig(files, project)
	if rc.Iterations != 1 || rc.PacingType != "none" ||
		rc.MinDelayMs != 0 || rc.MaxDelayMs != 0 {
		t.Fatalf("conflicting profile did not use APEX defaults: %#v", rc)
	}
	if len(rc.RuntimeIssues) != 2 {
		t.Fatalf("issues = %#v, want conflict and default-iteration findings only", rc.RuntimeIssues)
	}
	sections := selectedRuntimeSections(collectRuntimeSections(files), cfgName, rc)
	parity := applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(sections, "http"), rc)
	pacing := runtimeParityEntryByID(t, parity, "pacing")
	if strings.Join(pacing.RuntimeFiles, ",") != "active.cfg" {
		t.Fatalf("pacing provenance = %#v, want the cfg containing the conflicting direct pointer", pacing.RuntimeFiles)
	}
	for _, evidence := range []string{"profile conflict", "direct.usp", "mapped.usp", "using APEX pacing defaults"} {
		if !strings.Contains(strings.ToLower(pacing.Notes), strings.ToLower(evidence)) {
			t.Fatalf("pacing notes = %q, missing %q", pacing.Notes, evidence)
		}
	}
	if strings.Contains(strings.ToLower(pacing.Notes), "invalid random delay bounds") {
		t.Fatalf("rejected direct USP was parsed after selection conflict: %q", pacing.Notes)
	}
}

func TestConvertZipRuntimeProfileConflictsProduceDurableExecutionGate(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "cfg profile conflict",
			files: map[string]string{
				"ProfileConflict.usr": `[General]
Type=Web
DefaultCfg=first.cfg
defaultcfg=second.cfg
[Actions]
Action=Action.c
`,
				"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
				"first.cfg":  "[ThinkTime]\nOptions=RECORDED",
				"second.cfg": "[ThinkTime]\nOptions=NOTHINK",
			},
		},
		{
			name: "run logic profile conflict",
			files: map[string]string{
				"ProfileConflict.usr": `[General]
Type=Web
DefaultCfg=active.cfg
[Actions]
Action=Action.c
`,
				"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
				"active.cfg": `[General]
DefaultRunLogic=first.usp
defaultrunlogic=second.usp
`,
				"first.usp":  "[RunLogicRunRoot]\nRunLogicNumOfIterations=2\nRunLogicPaceType=ASAP",
				"second.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=3\nRunLogicPaceType=ASAP",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ConvertZip(buildSourceZip(t, test.files), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			if result.Script == nil || len(result.Script.Actions) != 1 {
				t.Fatalf("script/actions = %#v, want one blocked request", result.Script)
			}
			action := result.Script.Actions[0]
			if action.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatalf("action metadata = %#v, want durable runtime-profile gate", action.Metadata)
			}
			review, _ := action.Metadata["import_review"].(map[string]any)
			reasons, _ := review["reason_codes"].([]string)
			if !containsString(reasons, runtimeProfileSelectionReason) {
				t.Fatalf("import review = %#v, want reason %q", review, runtimeProfileSelectionReason)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("DSL validation issues = %#v", issues)
			}
		})
	}
}

func TestResolveRuntimeConfigAmbiguousRunLogicDropsLegacyCfgPacingAndIterations(t *testing.T) {
	files := map[string]string{
		"active.cfg": `[Iterations]
NumOfIterations=9
[Pacing]
Mode=fixed_delay
DelayMs=9000`,
		"one.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations="2"
RunLogicPaceType="ASAP"`,
		"two.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations="3"
RunLogicPaceType="ASAP"`,
	}
	_, rc := resolveRuntimeConfig(files, &ProjectConfig{RuntimeFile: "active.cfg"})
	if rc.Iterations != 1 || rc.PacingType != "none" || rc.DelayMs != 0 {
		t.Fatalf("ambiguous authoritative run logic retained stale cfg values: %#v", rc)
	}
	if len(rc.RuntimeIssues) < 2 {
		t.Fatalf("issues = %#v, want pacing and iteration review findings", rc.RuntimeIssues)
	}
}

func TestResolveRuntimeConfigMissingSelectedUSPRecordsAttemptAndDefaultsWholeProfile(t *testing.T) {
	files := map[string]string{
		"active.cfg": `[General]
DefaultRunLogic=missing.usp
[Iterations]
NumOfIterations=9`,
	}
	_, rc := resolveRuntimeConfig(files, &ProjectConfig{RuntimeFile: "active.cfg"})
	if rc.Iterations != 1 || rc.PacingType != "none" {
		t.Fatalf("missing selected USP retained legacy values: %#v", rc)
	}
	if strings.Join(rc.RuntimeFiles, ",") != "active.cfg,missing.usp" {
		t.Fatalf("runtime provenance = %#v, want attempted missing file", rc.RuntimeFiles)
	}
}

func TestParseRuntimeCfgMapsLoadRunnerThinkTimeModes(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		multiplier float64
		limit      float64
		ignore     bool
		partial    bool
	}{
		{name: "recorded", body: "Options=RECORDED\nFactor=7\nLimitFlag=0\nLimit=9", multiplier: 1},
		{name: "multiply and limit", body: "Options=MULTIPLY\nFactor=2.5\nLimitFlag=1\nLimit=3", multiplier: 2.5, limit: 3000},
		{name: "no think", body: "Options=NOTHINK", multiplier: 1, ignore: true},
		{name: "random invalid review", body: "Options=RANDOM\nThinkTimeRandomLow=150\nThinkTimeRandomHigh=50", multiplier: 1, partial: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg("[ThinkTime]\n" + test.body)
			if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != test.multiplier ||
				rc.ThinkTimeLimitMs != test.limit || rc.ThinkTimeIgnore != test.ignore {
				t.Fatalf("ParseRuntimeCfg() = %#v", rc)
			}
			if (len(rc.RuntimeIssues) > 0) != test.partial {
				t.Fatalf("issues = %#v, partial want %v", rc.RuntimeIssues, test.partial)
			}
		})
	}
}

func TestParseRuntimeCfgEnabledZeroThinkTimeLimitRequiresReview(t *testing.T) {
	for _, rawLimit := range []string{"0", "0.0", "-0"} {
		t.Run(strings.ReplaceAll(rawLimit, "-", "negative_"), func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"profiles/zero-limit.cfg": "[ThinkTime]\nOptions=RECORDED\nLimitFlag=1\nLimit=" + rawLimit,
			}, nil)
			if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
				rc.ThinkTimeLimitMs != 0 || rc.ThinkTimeIgnore {
				t.Fatalf("zero-limit fallback = %#v, want recorded mode with APEX no-cap default", rc)
			}
			issue, ok := runtimeConfigIssueByID(rc, "think_time")
			if !ok || !strings.Contains(issue.Notes, "LimitFlag=1") ||
				!strings.Contains(issue.Notes, `Limit="`+rawLimit+`"`) ||
				!strings.Contains(issue.Notes, "APEX limit_ms=0 means no cap") {
				t.Fatalf("think-time issue = %#v, want unrepresentable zero-cap evidence", issue)
			}
			if len(issue.RuntimeFiles) != 1 || issue.RuntimeFiles[0] != "profiles/zero-limit.cfg" {
				t.Fatalf("think-time issue provenance = %#v", issue.RuntimeFiles)
			}
		})
	}
}

func TestResolveRuntimeConfigDoesNotUseUSPAsCfgFallback(t *testing.T) {
	files := map[string]string{
		"custom.cfg": `[ThinkTime]
Options=NOTHINK`,
		"default.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations="2"
RunLogicPaceType="ASAP"`,
	}
	name, rc := resolveRuntimeConfig(files, nil)
	if name != "custom.cfg" {
		t.Fatalf("runtime cfg = %q, want sole cfg", name)
	}
	if rc.ThinkTimeType != "recorded" || !rc.ThinkTimeIgnore || rc.Iterations != 2 {
		t.Fatalf("resolved runtime = %#v", rc)
	}
}

func TestApplyRuntimeUSPDefaultsMissingOrInvalidActiveValues(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing iterations", body: `RunLogicPaceType="Const"`},
		{name: "iterations out of bounds", body: "RunLogicNumOfIterations=\"1000000001\"\nRunLogicPaceType=\"Const\""},
		{name: "malformed active pacing", body: "RunLogicNumOfIterations=\"1\"\nRunLogicPaceType=\"Const\"\nRunLogicPaceConstTime=\"bad\""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := &RuntimeConfig{
				Iterations:    9,
				PacingType:    "random_delay",
				MinDelayMs:    100,
				MaxDelayMs:    200,
				IntervalMs:    300,
				MaxIntervalMs: 400,
			}
			applyRuntimeUSP(rc, "selected.usp", "[RunLogicRunRoot]\n"+test.body)
			if rc.Iterations != 1 || rc.PacingType != "none" ||
				rc.MinDelayMs != 0 || rc.MaxDelayMs != 0 ||
				rc.IntervalMs != 0 || rc.MaxIntervalMs != 0 {
				t.Fatalf("invalid authoritative USP retained stale cfg values: %#v", rc)
			}
			if len(rc.RuntimeIssues) == 0 {
				t.Fatal("invalid authoritative USP was not marked partial")
			}
		})
	}
}

func TestParseRuntimeCfgDefaultsMalformedThinkTimeValues(t *testing.T) {
	tests := []string{
		"Options=MULTIPLY\nFactor=not-a-number",
		"Options=MULTIPLY\nFactor=-2",
		"Options=UNKNOWN",
		"Options=RECORDED\nLimitFlag=maybe\nLimit=4",
		"Options=RECORDED\nLimitFlag=1\nLimit=invalid",
	}
	for _, body := range tests {
		t.Run(strings.ReplaceAll(body, "\n", "_"), func(t *testing.T) {
			rc := ParseRuntimeCfg("[ThinkTime]\n" + body)
			if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
				rc.ThinkTimeLimitMs != 0 || rc.ThinkTimeIgnore {
				t.Fatalf("malformed think-time did not use recorded defaults: %#v", rc)
			}
			if len(rc.RuntimeIssues) == 0 {
				t.Fatal("malformed think-time was not marked partial")
			}
		})
	}
}

func TestParseRuntimeCfgFormalThinkTimeOptionsWinLegacyIgnoreConflicts(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantIgnore bool
		wantIssue  bool
	}{
		{name: "recorded overrides ignore", body: "Options=RECORDED\nIgnore=1", wantIssue: true},
		{name: "multiply overrides ignore", body: "Options=MULTIPLY\nFactor=2\nIgnoreThinkTime=true", wantIssue: true},
		{name: "no think overrides replay", body: "Options=NOTHINK\nIgnore=0", wantIgnore: true, wantIssue: true},
		{name: "matching recorded alias", body: "Options=RECORDED\nIgnore=0"},
		{name: "matching no think alias", body: "Options=NOTHINK\nIgnore=1", wantIgnore: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg("[ThinkTime]\n" + test.body)
			if rc.ThinkTimeType != "recorded" || rc.ThinkTimeIgnore != test.wantIgnore {
				t.Fatalf("ParseRuntimeCfg() think time = %#v", rc)
			}
			if (len(rc.RuntimeIssues) > 0) != test.wantIssue {
				t.Fatalf("issues = %#v, want issue %v", rc.RuntimeIssues, test.wantIssue)
			}
		})
	}
}

func TestResolveRuntimeConfigUSPOnlyUsesSafeCfgDefaults(t *testing.T) {
	files := map[string]string{
		"default.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations="2"
RunLogicPaceType="Const"
RunLogicPaceConstTime="1"`,
	}
	name, rc := resolveRuntimeConfig(files, nil)
	if name != "" {
		t.Fatalf("runtime cfg = %q, want no synthesized cfg selection", name)
	}
	if rc == nil || rc.HTTPVersion != "1.1" || !rc.KeepAlive || rc.MaxRedirects != 10 {
		t.Fatalf("USP-only safe cfg defaults = %#v", rc)
	}
	if rc.Iterations != 2 || rc.PacingType != "fixed_interval" || rc.IntervalMs != 1000 {
		t.Fatalf("USP-only run logic = %#v", rc)
	}
	if strings.Join(rc.RuntimeFiles, ",") != "default.usp" {
		t.Fatalf("runtime provenance = %#v, want only default.usp", rc.RuntimeFiles)
	}
}

func TestResolveRuntimeConfigPreservesSelectedEmptyCfg(t *testing.T) {
	files := map[string]string{"profiles/active.cfg": ""}
	name, rc := resolveRuntimeConfig(files, &ProjectConfig{DefaultCfg: `profiles\active.cfg`})
	if name != "profiles/active.cfg" {
		t.Fatalf("runtime cfg = %q, want normalized selected path", name)
	}
	if rc == nil || rc.HTTPVersion != "1.1" || !rc.KeepAlive || rc.MaxRedirects != 10 {
		t.Fatalf("empty selected cfg did not retain safe defaults: %#v", rc)
	}
	if strings.Join(rc.RuntimeFiles, ",") != "profiles/active.cfg" {
		t.Fatalf("runtime provenance = %#v, want selected empty cfg", rc.RuntimeFiles)
	}
}

func TestApplyRuntimeUSPMissingRootMarksPacingAndIterationsPartial(t *testing.T) {
	rc := &RuntimeConfig{Iterations: 9, PacingType: "fixed_delay", DelayMs: 9000}
	applyRuntimeUSP(rc, "profiles/selected.usp", "[Profile Actions]\nProfile Actions name=Action")
	if rc.Iterations != 1 || rc.PacingType != "none" || rc.DelayMs != 0 {
		t.Fatalf("missing root did not use full run-logic defaults: %#v", rc)
	}
	for _, id := range []string{"pacing", "run_logic_iterations"} {
		if !runtimeConfigHasIssue(rc, id) {
			t.Fatalf("missing %s issue in %#v", id, rc.RuntimeIssues)
		}
	}
}

func TestRuntimeSelectionRejectsAmbiguousDefaultBasenames(t *testing.T) {
	files := map[string]string{
		"profiles/a/default.cfg": "[ThinkTime]\nOptions=RECORDED",
		"profiles/b/default.cfg": "[ThinkTime]\nOptions=NOTHINK",
		"profiles/a/default.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=1\nRunLogicPaceType=ASAP",
		"profiles/b/default.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=2\nRunLogicPaceType=ASAP",
	}
	if name, _, issue := findRuntimeConfig(files, nil); name != "" || !strings.Contains(issue, "Multiple default runtime cfg files") {
		t.Fatalf("cfg selection = %q issue=%q, want ambiguous default rejection", name, issue)
	}
	if name, issue := resolveRunLogicFile(files, nil, "", ""); name != "" || !strings.Contains(issue, "Multiple default run-logic files") {
		t.Fatalf("USP selection = %q issue=%q, want ambiguous default rejection", name, issue)
	}
}

func TestRuntimeSelectionUsesExactNormalizedPathAcrossDuplicateBasenames(t *testing.T) {
	files := map[string]string{
		"profiles/a/default.cfg": "[ThinkTime]\nOptions=RECORDED",
		"profiles/b/default.cfg": "[ThinkTime]\nOptions=NOTHINK",
		"profiles/a/default.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=2\nRunLogicPaceType=ASAP",
		"profiles/b/default.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=3\nRunLogicPaceType=ASAP",
	}
	project := &ProjectConfig{
		DefaultCfg:       `profiles\a\default.cfg`,
		CfgProfiles:      map[string]string{"Active": `profiles\a\default.cfg`},
		RunLogicProfiles: map[string]string{"Active": `profiles\a\default.usp`},
	}
	name, rc := resolveRuntimeConfig(files, project)
	if name != "profiles/a/default.cfg" {
		t.Fatalf("runtime cfg = %q, want exact normalized path", name)
	}
	if rc == nil || rc.ThinkTimeIgnore || rc.Iterations != 2 {
		t.Fatalf("resolved runtime = %#v, want profiles/a settings", rc)
	}
	if runtimeConfigHasIssue(rc, "runtime_profile") || runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("exact path selection was marked ambiguous: %#v", rc.RuntimeIssues)
	}
	if strings.Join(rc.RuntimeFiles, ",") != "profiles/a/default.cfg,profiles/a/default.usp" {
		t.Fatalf("runtime provenance = %#v", rc.RuntimeFiles)
	}
}

func TestSameRuntimeFileUsesDirectionalQualifiedPointerSemantics(t *testing.T) {
	tests := []struct {
		name          string
		pointer, file string
		want          bool
	}{
		{name: "unqualified pointer may match selected qualified file", pointer: "active.cfg", file: "profiles/a/active.cfg", want: true},
		{name: "qualified pointer cannot match selected root file", pointer: "profiles/a/active.cfg", file: "active.cfg"},
		{name: "qualified pointer cannot match another directory", pointer: "profiles/a/active.cfg", file: "profiles/b/active.cfg"},
		{name: "qualified exact path matches", pointer: `profiles\a\active.cfg`, file: "profiles/a/active.cfg", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sameRuntimeFile(test.pointer, test.file); got != test.want {
				t.Fatalf("sameRuntimeFile(%q, %q) = %v, want %v", test.pointer, test.file, got, test.want)
			}
		})
	}
}

func TestResolveRuntimeConfigDoesNotAssociateQualifiedProfileWithRootCfg(t *testing.T) {
	files := map[string]string{
		"active.cfg": "[ThinkTime]\nOptions=RECORDED",
		"root.usp":   "[RunLogicRunRoot]\nRunLogicNumOfIterations=2\nRunLogicPaceType=ASAP",
		"other.usp":  "[RunLogicRunRoot]\nRunLogicNumOfIterations=9\nRunLogicPaceType=Const\nRunLogicPaceConstTime=9",
	}
	project := &ProjectConfig{
		DefaultCfg:  "active.cfg",
		CfgProfiles: map[string]string{"Root": "active.cfg", "Other": "profiles/a/active.cfg"},
		RunLogicProfiles: map[string]string{
			"Root":  "root.usp",
			"Other": "other.usp",
		},
	}
	_, rc := resolveRuntimeConfig(files, project)
	if rc.Iterations != 2 || rc.PacingType != "none" || runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("qualified profile was incorrectly associated with root cfg: %#v", rc)
	}
}

func TestRuntimePointersPreserveQuotedWindowsBackslashes(t *testing.T) {
	project, err := ParseUSRFile(`[General]
DefaultCfg="profiles\a\active.cfg"
[CfgFiles]
Active="profiles\a\active.cfg"
`)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"profiles/a/active.cfg": `[General]
DefaultRunLogic="profiles\a\default.usp"
[ThinkTime]
Options=NOTHINK`,
		"profiles/a/default.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=3
RunLogicPaceType=ASAP`,
	}
	cfgName, rc := resolveRuntimeConfig(files, project)
	if cfgName != "profiles/a/active.cfg" || rc.Iterations != 3 || !rc.ThinkTimeIgnore {
		t.Fatalf("quoted Windows runtime pointers were C-unescaped or misresolved: cfg=%q rc=%#v project=%#v", cfgName, rc, project)
	}
	if strings.ContainsRune(project.DefaultCfg, '\a') {
		t.Fatalf("DefaultCfg contains a C-decoded bell character: %q", project.DefaultCfg)
	}
}

func TestRuntimeProfileFieldsAreCaseInsensitive(t *testing.T) {
	tests := []struct {
		name    string
		cfgBody string
		uspBody string
	}{
		{
			name: "lowercase",
			cfgBody: `[general]
defaultrunlogic=selected.usp
[thinktime]
options=multiply
factor=2
limitflag=1
limit=3`,
			uspBody: `[runlogicrunroot]
runlogicnumofiterations=2
runlogicpacetype=const
runlogicpaceconsttime=5`,
		},
		{
			name: "mixed case",
			cfgBody: `[GeNeRaL]
dEfAuLtRuNlOgIc=selected.usp
[ThInK TiMe]
oPtIoNs=MuLtIpLy
fAcToR=2
lImItFlAg=1
lImIt=3`,
			uspBody: `[RuNlOgIcRuNrOoT]
rUnLoGiCnUmOfItErAtIoNs=2
rUnLoGiCpAcEtYpE=CoNsT
rUnLoGiCpAcEcOnStTiMe=5`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"active.cfg":   test.cfgBody,
				"selected.usp": test.uspBody,
			}, &ProjectConfig{DefaultCfg: "active.cfg"})
			if rc.Iterations != 2 ||
				rc.PacingType != "fixed_interval" ||
				rc.IntervalMs != 5000 ||
				rc.ThinkTimeType != "recorded" ||
				rc.ThinkTimeMultiplier != 2 ||
				rc.ThinkTimeLimitMs != 3000 ||
				runtimeConfigHasIssue(rc, "pacing") ||
				runtimeConfigHasIssue(rc, "think_time") {
				t.Fatalf("case-insensitive runtime mapping = %#v", rc)
			}
		})
	}
}

func TestRuntimeCfgCaseFoldConflictsDefaultAffectedSettings(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		issueID    string
		assertions func(*testing.T, *RuntimeConfig)
		evidence   []string
	}{
		{
			name: "think-time key casing conflict",
			content: `[ThinkTime]
Options=MULTIPLY
options=NOTHINK
Factor=2`,
			issueID: "think_time",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
					rc.ThinkTimeIgnore || rc.ThinkTimeLimitMs != 0 {
					t.Fatalf("think-time conflict retained selected source value: %#v", rc)
				}
			},
			evidence: []string{`[ThinkTime] Options="MULTIPLY"`, `[ThinkTime] options="NOTHINK"`},
		},
		{
			name: "think-time section casing conflict",
			content: `[ThinkTime]
Options=MULTIPLY
Factor=2
[thinktime]
options=NOTHINK`,
			issueID: "think_time",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
					rc.ThinkTimeIgnore || rc.ThinkTimeLimitMs != 0 {
					t.Fatalf("section conflict retained selected source value: %#v", rc)
				}
			},
			evidence: []string{`[ThinkTime] Options="MULTIPLY"`, `[thinktime] options="NOTHINK"`},
		},
		{
			name: "think-time semantic alias section conflict",
			content: `[Think Time]
Options=MULTIPLY
Factor=2
[ThinkTime]
Options=NOTHINK`,
			issueID: "think_time",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
					rc.ThinkTimeIgnore || rc.ThinkTimeLimitMs != 0 {
					t.Fatalf("semantic section alias conflict retained selected source value: %#v", rc)
				}
			},
			evidence: []string{`[Think Time] Options="MULTIPLY"`, `[ThinkTime] Options="NOTHINK"`},
		},
		{
			name: "pacing key casing conflict",
			content: `[Pacing]
Mode=fixed_interval
mode=fixed_delay
IntervalMs=5000
DelayMs=9000`,
			issueID: "pacing",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.PacingType != "none" || rc.IntervalMs != 0 || rc.DelayMs != 0 {
					t.Fatalf("pacing conflict retained selected source value: %#v", rc)
				}
			},
			evidence: []string{`[Pacing] Mode="fixed_interval"`, `[Pacing] mode="fixed_delay"`},
		},
		{
			name: "iteration key casing conflict",
			content: `[Iterations]
NumOfIterations=2
numofiterations=3`,
			issueID: "run_logic_iterations",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.Iterations != 1 {
					t.Fatalf("iteration conflict retained selected source value: %#v", rc)
				}
			},
			evidence: []string{`[Iterations] NumOfIterations="2"`, `[Iterations] numofiterations="3"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg(test.content)
			test.assertions(t, rc)
			issue, ok := runtimeConfigIssueByID(rc, test.issueID)
			if !ok || !strings.Contains(issue.Notes, "requiring review") {
				t.Fatalf("%s issue = %#v, want review-required conflict", test.issueID, issue)
			}
			for _, want := range test.evidence {
				if !strings.Contains(issue.Notes, want) {
					t.Fatalf("%s issue = %#v, missing source evidence %q", test.issueID, issue, want)
				}
			}
		})
	}
}

func TestRuntimeCfgIdenticalCaseFoldDuplicatesAreNotConflicts(t *testing.T) {
	rc := ParseRuntimeCfg(`[ThinkTime]
Options=RECORDED
options=recorded
[Iterations]
NumOfIterations=2
numofiterations=2
[Pacing]
Mode=fixed_interval
mode=FIXED_INTERVAL
IntervalMs=5000
intervalms=5000`)
	if runtimeConfigHasIssue(rc, "think_time") ||
		runtimeConfigHasIssue(rc, "run_logic_iterations") ||
		runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("identical case-fold duplicates were marked partial: %#v", rc.RuntimeIssues)
	}
	if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
		rc.Iterations != 2 || rc.PacingType != "fixed_interval" || rc.IntervalMs != 5000 {
		t.Fatalf("identical case-fold duplicates did not resolve deterministically: %#v", rc)
	}
}

func TestRuntimeCfgMergesComplementaryCaseEquivalentSections(t *testing.T) {
	for _, content := range []string{
		`[ThinkTime]
Options=RECORDED
[thinktime]
LimitFlag=1
Limit=5`,
		`[Think Time]
Options=RECORDED
[ThinkTime]
LimitFlag=1
Limit=5`,
	} {
		rc := ParseRuntimeCfg(content)
		if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
			rc.ThinkTimeLimitMs != 5000 {
			t.Fatalf("complementary think-time sections were not merged: %#v", rc)
		}
		if runtimeConfigHasIssue(rc, "think_time") {
			t.Fatalf("complementary think-time sections were marked partial: %#v", rc.RuntimeIssues)
		}
	}
}

func TestRuntimeCfgSupportedSettingConflictsUseDefaultsWithEvidence(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		issueID    string
		assertions func(*testing.T, *RuntimeConfig)
		evidence   []string
	}{
		{
			name: "HTTP version synonymous keys",
			content: `[HTTP]
HTTPVersion=2
HTTPVer=1.1
ConnectTimeout=12`,
			issueID: "http_transport",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.HTTPVersion != "1.1" || rc.ConnectTimeoutSec != 12 {
					t.Fatalf("HTTP conflict defaulted the wrong settings: %#v", rc)
				}
			},
			evidence: []string{`HTTPVersion="2"`, `HTTPVer="1.1"`},
		},
		{
			name: "pacing synonymous keys",
			content: `[Pacing]
Mode=none
PacingMode=fixed_interval
IntervalMs=5000`,
			issueID: "pacing",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.PacingType != "none" || rc.IntervalMs != 0 {
					t.Fatalf("pacing alias conflict retained executable values: %#v", rc)
				}
			},
			evidence: []string{`Mode="none"`, `PacingMode="fixed_interval"`},
		},
		{
			name: "authentication synonymous keys",
			content: `[Authentication]
UserName=alice
Username=bob
Password=short-secret`,
			issueID: "authentication",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.AuthUser != "" || rc.AuthPass != "" {
					t.Fatalf("authentication conflict retained credentials: %#v", rc)
				}
			},
			evidence: []string{`UserName="alice"`, `Username="bob"`},
		},
		{
			name: "browser key casing",
			content: `[Browser]
Engine=chromium
engine=firefox
ViewportWidth=1200`,
			issueID: "browser_device_emulation",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.BrowserEngine != "" || rc.BrowserViewportWidth != 1200 {
					t.Fatalf("browser conflict defaulted the wrong settings: %#v", rc)
				}
			},
			evidence: []string{`Engine="chromium"`, `engine="firefox"`},
		},
		{
			name: "network alias sections and keys",
			content: `[Network Speed Simulation]
Mode=custom
[NetworkSimulation]
SimulationMode=none
Bandwidth=1000`,
			issueID: "network_simulation",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.NetworkSimulationMode != "" || rc.NetworkBandwidthKbps != 1000 {
					t.Fatalf("network conflict defaulted the wrong settings: %#v", rc)
				}
			},
			evidence: []string{`Mode="custom"`, `SimulationMode="none"`},
		},
		{
			name: "logging alias sections and keys",
			content: `[Log]
Level=extended
[Logging]
LogLevel=disabled
OnErrorOnly=1`,
			issueID: "logging",
			assertions: func(t *testing.T, rc *RuntimeConfig) {
				t.Helper()
				if rc.LogLevel != "" || !rc.OnErrorOnly {
					t.Fatalf("logging conflict defaulted the wrong settings: %#v", rc)
				}
			},
			evidence: []string{`Level="extended"`, `LogLevel="disabled"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg(test.content)
			test.assertions(t, rc)
			issue, ok := runtimeConfigIssueByID(rc, test.issueID)
			if !ok || !strings.Contains(issue.Notes, "requiring review") {
				t.Fatalf("%s issue = %#v, want review-required conflict", test.issueID, issue)
			}
			for _, want := range test.evidence {
				if !strings.Contains(issue.Notes, want) {
					t.Fatalf("%s issue = %#v, missing %q", test.issueID, issue, want)
				}
			}
		})
	}
}

func TestRuntimeCfgProxyConflictEvidencePreservesURLCredentials(t *testing.T) {
	for _, content := range []string{
		`[Proxy]
Mode=custom
ProxyURL=http://user-one:pass-one@proxy-one.example/?token=secret-one
proxyurl=http://user-two:pass-two@proxy-two.example/?token=secret-two`,
		`[WEB]
ProxyURL=user-one:pass-one@proxy-one.example/?token=secret-one
proxyurl=user-two:pass-two@proxy-two.example/?token=secret-two`,
	} {
		rc := ParseRuntimeCfg(content)
		if rc.ProxyMode != "none" || rc.ProxyURL != "" || rc.ProxyUser != "" || rc.ProxyPass != "" {
			t.Fatalf("proxy conflict retained executable proxy settings: %#v", rc)
		}
		foundConflict := false
		var proxyIssues []RuntimeMigrationIssue
		for _, issue := range rc.RuntimeIssues {
			if issue.ID != "proxy" {
				continue
			}
			proxyIssues = append(proxyIssues, issue)
			if strings.Contains(issue.Notes, "Conflicting") {
				foundConflict = true
			}
		}
		if !foundConflict {
			t.Fatalf("proxy issues = %#v, conflicts=%#v content=%q, want conflict evidence", proxyIssues, runtimeConfigCaseFoldConflicts(content), content)
		}
		combined := fmt.Sprintf("%#v", proxyIssues)
		for _, secret := range []string{
			"user-one", "pass-one", "secret-one",
			"user-two", "pass-two", "secret-two",
		} {
			if !strings.Contains(combined, secret) {
				t.Fatalf("proxy issues lost %q: %s", secret, combined)
			}
		}
	}
}

func TestRuntimeCfgIdenticalSynonymousDeclarationsAreExact(t *testing.T) {
	rc := ParseRuntimeCfg(`[HTTP]
HTTPVersion=2
HTTPVer=2
[Authentication]
UserName=alice
Username=alice
[Pacing]
Mode=none
PacingMode=NONE`)
	for _, id := range []string{"http_transport", "authentication", "pacing"} {
		if runtimeConfigHasIssue(rc, id) {
			t.Fatalf("identical synonymous %s declarations were marked partial: %#v", id, rc.RuntimeIssues)
		}
	}
	if rc.HTTPVersion != "2" || rc.AuthUser != "alice" || rc.PacingType != "none" {
		t.Fatalf("identical synonymous declarations did not map exactly: %#v", rc)
	}
}

func TestRuntimeCfgIgnoresConflictsInInactiveConditionalFields(t *testing.T) {
	rc := ParseRuntimeCfg(`[ThinkTime]
Options=RECORDED
Factor=2
factor=9
LimitFlag=0
Limit=3
limit=8
[Pacing]
Mode=none
IntervalMs=5000
intervalms=9000`)
	if runtimeConfigHasIssue(rc, "think_time") || runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("inactive cfg conflicts were marked partial: %#v", rc.RuntimeIssues)
	}
	if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 ||
		rc.ThinkTimeLimitMs != 0 || rc.PacingType != "none" {
		t.Fatalf("inactive cfg conflicts changed active semantics: %#v", rc)
	}
}

func TestRuntimeUSPCaseFoldConflictsDefaultAffectedSettings(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantPacing string
		wantMS     int
		wantIters  int
		issueID    string
		evidence   []string
	}{
		{
			name: "pacing mode key conflict",
			content: `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=Const
runlogicpacetype=After
RunLogicPaceConstTime=5
RunLogicAfterPaceMin=1
RunLogicAfterPaceMax=2`,
			wantPacing: "none",
			wantIters:  2,
			issueID:    "pacing",
			evidence:   []string{`RunLogicPaceType="Const"`, `runlogicpacetype="After"`},
		},
		{
			name: "active pacing value conflict",
			content: `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=Const
RunLogicPaceConstTime=5
runlogicpaceconsttime=9`,
			wantPacing: "none",
			wantIters:  2,
			issueID:    "pacing",
			evidence:   []string{`RunLogicPaceConstTime="5"`, `runlogicpaceconsttime="9"`},
		},
		{
			name: "iteration key conflict",
			content: `[RunLogicRunRoot]
RunLogicNumOfIterations=2
runlogicnumofiterations=3
RunLogicPaceType=Const
RunLogicPaceConstTime=5`,
			wantPacing: "fixed_interval",
			wantMS:     5000,
			wantIters:  1,
			issueID:    "run_logic_iterations",
			evidence:   []string{`RunLogicNumOfIterations="2"`, `runlogicnumofiterations="3"`},
		},
		{
			name: "section casing conflict",
			content: `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=Const
RunLogicPaceConstTime=5
[runlogicrunroot]
runlogicnumofiterations=2
runlogicpacetype=After`,
			wantPacing: "none",
			wantIters:  2,
			issueID:    "pacing",
			evidence:   []string{`[RunLogicRunRoot] RunLogicPaceType="Const"`, `[runlogicrunroot] runlogicpacetype="After"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := &RuntimeConfig{}
			applyRuntimeUSP(rc, "profiles/selected.usp", test.content)
			if rc.Iterations != test.wantIters || rc.PacingType != test.wantPacing ||
				rc.IntervalMs != test.wantMS {
				t.Fatalf("conflicting USP mapping = %#v", rc)
			}
			issue, ok := runtimeConfigIssueByID(rc, test.issueID)
			if !ok || !strings.Contains(issue.Notes, "profiles/selected.usp") ||
				!strings.Contains(issue.Notes, "requiring review") {
				t.Fatalf("%s issue = %#v, want file-scoped review evidence", test.issueID, issue)
			}
			for _, want := range test.evidence {
				if !strings.Contains(issue.Notes, want) {
					t.Fatalf("%s issue = %#v, missing %q", test.issueID, issue, want)
				}
			}
		})
	}
}

func TestRuntimeUSPIgnoresConflictsInInactivePacingFields(t *testing.T) {
	rc := &RuntimeConfig{}
	applyRuntimeUSP(rc, "selected.usp", `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=ASAP
RunLogicPaceConstTime=5
runlogicpaceconsttime=9`)
	if rc.Iterations != 2 || rc.PacingType != "none" || runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("inactive pacing conflict affected authoritative ASAP mode: %#v", rc)
	}
}

func TestRuntimeUSPIdenticalCaseFoldDuplicatesAreNotConflicts(t *testing.T) {
	rc := &RuntimeConfig{}
	applyRuntimeUSP(rc, "selected.usp", `[RunLogicRunRoot]
RunLogicNumOfIterations=2
runlogicnumofiterations=2
RunLogicPaceType=Const
runlogicpacetype=CONST
RunLogicPaceConstTime=5
runlogicpaceconsttime=5`)
	if rc.Iterations != 2 || rc.PacingType != "fixed_interval" || rc.IntervalMs != 5000 ||
		runtimeConfigHasIssue(rc, "pacing") || runtimeConfigHasIssue(rc, "run_logic_iterations") {
		t.Fatalf("identical USP duplicates were not resolved exactly: %#v", rc)
	}
}

func TestRuntimeUSPMergesComplementaryCaseEquivalentSections(t *testing.T) {
	rc := &RuntimeConfig{}
	applyRuntimeUSP(rc, "selected.usp", `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=Const
[runlogicrunroot]
RunLogicPaceConstTime=5`)
	if rc.Iterations != 2 || rc.PacingType != "fixed_interval" || rc.IntervalMs != 5000 {
		t.Fatalf("complementary USP sections were not merged: %#v", rc)
	}
	if runtimeConfigHasIssue(rc, "pacing") || runtimeConfigHasIssue(rc, "run_logic_iterations") {
		t.Fatalf("complementary USP sections were marked partial: %#v", rc.RuntimeIssues)
	}
}

func TestResolveRuntimeConfigDefaultsConflictingCaseFoldDefaultRunLogic(t *testing.T) {
	_, rc := resolveRuntimeConfig(map[string]string{
		"active.cfg": `[General]
DefaultRunLogic=root.usp
defaultrunlogic=profiles/a/selected.usp`,
		"root.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=Const
RunLogicPaceConstTime=5`,
		"profiles/a/selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=3
RunLogicPaceType=After
RunLogicAfterPaceMin=1
RunLogicAfterPaceMax=2`,
	}, &ProjectConfig{DefaultCfg: "active.cfg"})
	if rc.Iterations != 1 || rc.PacingType != "none" || rc.IntervalMs != 0 ||
		rc.MinDelayMs != 0 || rc.MaxDelayMs != 0 {
		t.Fatalf("conflicting DefaultRunLogic pointer selected executable run logic: %#v", rc)
	}
	issue, ok := runtimeConfigIssueByID(rc, "pacing")
	if !ok ||
		!strings.Contains(issue.Notes, `DefaultRunLogic="root.usp"`) ||
		!strings.Contains(issue.Notes, `defaultrunlogic="profiles/a/selected.usp"`) {
		t.Fatalf("pacing issue = %#v, want both conflicting pointer declarations", issue)
	}
	if len(issue.RuntimeFiles) != 1 || issue.RuntimeFiles[0] != "active.cfg" {
		t.Fatalf("pacing issue provenance = %#v, want active.cfg", issue.RuntimeFiles)
	}
}

func TestParseUSRFileCaseInsensitiveProfilesIgnoreArbitraryCfgValues(t *testing.T) {
	project, err := ParseUSRFile(`[gEnErAl]
tYpE=Web
[eXtRaFiLeS]
helper=helper.cfg
[cFgFiLeS]
ACTIVE=profiles/active.cfg
[rUnLoGiCfIlEs]
active=profiles/selected.usp
[aCtIoNs]
action=Action.c`)
	if err != nil {
		t.Fatal(err)
	}
	if project.ScriptType != "Web" ||
		project.RuntimeFile != "profiles/active.cfg" ||
		project.CfgProfiles["ACTIVE"] != "profiles/active.cfg" ||
		project.RunLogicProfiles["active"] != "profiles/selected.usp" {
		t.Fatalf("case-insensitive USR profile parse = %#v", project)
	}

	_, rc := resolveRuntimeConfig(map[string]string{
		"profiles/active.cfg": `[general]
defaultrunlogic=profiles/selected.usp`,
		"profiles/selected.usp": `[runlogicrunroot]
runlogicnumofiterations=2
runlogicpacetype=asap`,
	}, project)
	if rc.Iterations != 2 || runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("case-insensitive profile-name association failed: %#v", rc)
	}
}

func TestParseUSRFileCaseInsensitiveDuplicateProfilesRetainsConflictEvidence(t *testing.T) {
	project, err := ParseUSRFile(`[CfgFiles]
ACTIVE=profiles/stale.cfg
active=profiles/selected.cfg
[RunLogicFiles]
active=profiles/stale.usp
ACTIVE=profiles/selected.usp`)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.CfgProfiles) != 1 ||
		len(project.RunLogicProfiles) != 1 ||
		project.RuntimeFile != "profiles/selected.cfg" ||
		runtimeProfileValue(project.RunLogicProfiles, "active") != "profiles/selected.usp" {
		t.Fatalf("case-insensitive duplicate profiles were not deterministic last-wins: %#v", project)
	}
	if len(project.runtimePointerConflicts) != 2 {
		t.Fatalf("runtime pointer conflicts = %#v, want cfg and run-logic evidence", project.runtimePointerConflicts)
	}
}

func TestResolveRuntimeConfigRejectsConflictingCaseFoldUSRProfiles(t *testing.T) {
	tests := []struct {
		name            string
		usr             string
		files           map[string]string
		issueID         string
		wantIssueFiles  []string
		evidence        []string
		wantSelectedCfg string
	}{
		{
			name: "DefaultCfg pointer",
			usr: `[General]
DefaultCfg=profiles/a.cfg
defaultcfg=profiles/b.cfg`,
			files: map[string]string{
				"profiles/a.cfg": "[ThinkTime]\nOptions=NOTHINK",
				"profiles/b.cfg": "[ThinkTime]\nOptions=MULTIPLY\nFactor=3",
				"default.usp":    "[RunLogicRunRoot]\nRunLogicNumOfIterations=4\nRunLogicPaceType=Const\nRunLogicPaceConstTime=4",
			},
			issueID:        "runtime_profile",
			wantIssueFiles: []string{"profiles/a.cfg", "profiles/b.cfg"},
			evidence:       []string{`DefaultCfg="profiles/a.cfg"`, `defaultcfg="profiles/b.cfg"`},
		},
		{
			name: "CfgFiles profile",
			usr: `[General]
DefaultCfg=ACTIVE
[CfgFiles]
ACTIVE=profiles/a.cfg
active=profiles/b.cfg`,
			files: map[string]string{
				"profiles/a.cfg": "[ThinkTime]\nOptions=NOTHINK",
				"profiles/b.cfg": "[ThinkTime]\nOptions=MULTIPLY\nFactor=3",
				"default.usp":    "[RunLogicRunRoot]\nRunLogicNumOfIterations=4\nRunLogicPaceType=Const\nRunLogicPaceConstTime=4",
			},
			issueID:        "runtime_profile",
			wantIssueFiles: []string{"profiles/a.cfg", "profiles/b.cfg"},
			evidence:       []string{`ACTIVE="profiles/a.cfg"`, `active="profiles/b.cfg"`},
		},
		{
			name: "RunLogicFiles profile",
			usr: `[General]
DefaultCfg=active.cfg
[CfgFiles]
ACTIVE=active.cfg
[RunLogicFiles]
ACTIVE=profiles/a.usp
active=profiles/b.usp`,
			files: map[string]string{
				"active.cfg":     "[ThinkTime]\nOptions=RECORDED",
				"profiles/a.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=2\nRunLogicPaceType=Const\nRunLogicPaceConstTime=2",
				"profiles/b.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=3\nRunLogicPaceType=After\nRunLogicAfterPaceMin=1\nRunLogicAfterPaceMax=2",
			},
			issueID:         "pacing",
			wantSelectedCfg: "active.cfg",
			wantIssueFiles:  []string{"active.cfg", "profiles/a.usp", "profiles/b.usp"},
			evidence:        []string{`ACTIVE="profiles/a.usp"`, `active="profiles/b.usp"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project, err := ParseUSRFile(test.usr)
			if err != nil {
				t.Fatal(err)
			}
			cfgName, rc := resolveRuntimeConfig(test.files, project)
			if cfgName != test.wantSelectedCfg {
				t.Fatalf("selected cfg = %q, want %q", cfgName, test.wantSelectedCfg)
			}
			if rc == nil || rc.ThinkTimeIgnore || rc.Iterations > 1 || rc.PacingType != "none" && rc.PacingType != "" {
				t.Fatalf("conflicting .usr pointer selected executable runtime values: %#v", rc)
			}
			issue, ok := runtimeConfigIssueByID(rc, test.issueID)
			if !ok || !strings.Contains(issue.Notes, "requiring review") {
				t.Fatalf("%s issue = %#v, want review-required .usr conflict", test.issueID, issue)
			}
			for _, want := range test.evidence {
				if !strings.Contains(issue.Notes, want) {
					t.Fatalf("%s issue = %#v, missing %q", test.issueID, issue, want)
				}
			}
			for _, want := range test.wantIssueFiles {
				if !containsFold(issue.RuntimeFiles, want) {
					t.Fatalf("%s issue files = %#v, missing %q", test.issueID, issue.RuntimeFiles, want)
				}
			}
		})
	}
}

func TestResolveRuntimeConfigAllowsIdenticalCaseFoldUSRProfiles(t *testing.T) {
	project, err := ParseUSRFile(`[General]
DefaultCfg=ACTIVE
defaultcfg=active
[CfgFiles]
ACTIVE=profiles\active.cfg
active=profiles/active.cfg
[RunLogicFiles]
ACTIVE=profiles\selected.usp
active=profiles/selected.usp`)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.runtimePointerConflicts) != 0 {
		t.Fatalf("identical .usr pointers were classified as conflicts: %#v", project.runtimePointerConflicts)
	}
	cfgName, rc := resolveRuntimeConfig(map[string]string{
		"profiles/active.cfg": "[ThinkTime]\nOptions=RECORDED",
		"profiles/selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=Const
RunLogicPaceConstTime=5`,
	}, project)
	if cfgName != "profiles/active.cfg" || rc.Iterations != 2 ||
		rc.PacingType != "fixed_interval" || rc.IntervalMs != 5000 ||
		runtimeConfigHasIssue(rc, "runtime_profile") || runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("identical .usr profiles did not resolve deterministically: cfg=%q rc=%#v", cfgName, rc)
	}
}

func TestResolveRunLogicComparesResolvedArchiveIdentities(t *testing.T) {
	tests := []struct {
		name           string
		files          map[string]string
		mapped         string
		wantIssue      bool
		wantIterations int
		wantPacing     string
		wantInterval   int
	}{
		{
			name: "qualified direct and unique unqualified mapped pointer",
			files: map[string]string{
				"active.cfg": `[General]
DefaultRunLogic=profiles/a/selected.usp`,
				"profiles/a/selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=ASAP`,
			},
			mapped:         "selected.usp",
			wantIterations: 2,
			wantPacing:     "none",
		},
		{
			name: "ambiguous unqualified mapped pointer remains conflict",
			files: map[string]string{
				"active.cfg": `[General]
DefaultRunLogic=profiles/a/selected.usp`,
				"profiles/a/selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=ASAP`,
				"profiles/b/selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=3
RunLogicPaceType=Const
RunLogicPaceConstTime=9`,
			},
			mapped:         "selected.usp",
			wantIssue:      true,
			wantIterations: 1,
			wantPacing:     "none",
		},
		{
			name: "different resolved files remain conflict",
			files: map[string]string{
				"active.cfg": `[General]
DefaultRunLogic=profiles/a/selected.usp`,
				"profiles/a/selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=ASAP`,
				"profiles/b/other.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=3
RunLogicPaceType=Const
RunLogicPaceConstTime=9`,
			},
			mapped:         "profiles/b/other.usp",
			wantIssue:      true,
			wantIterations: 1,
			wantPacing:     "none",
		},
		{
			name: "unqualified direct and qualified mapped pointer resolve to different duplicate basenames",
			files: map[string]string{
				"active.cfg": `[General]
DefaultRunLogic=selected.usp`,
				"selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=ASAP`,
				"profiles/a/selected.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=3
RunLogicPaceType=Const
RunLogicPaceConstTime=9`,
			},
			mapped:         "profiles/a/selected.usp",
			wantIssue:      true,
			wantIterations: 1,
			wantPacing:     "none",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := &ProjectConfig{
				DefaultCfg:       "active.cfg",
				CfgProfiles:      map[string]string{"ACTIVE": "active.cfg"},
				RunLogicProfiles: map[string]string{"active": test.mapped},
			}
			_, rc := resolveRuntimeConfig(test.files, project)
			if rc.Iterations != test.wantIterations ||
				rc.PacingType != test.wantPacing ||
				rc.IntervalMs != test.wantInterval ||
				runtimeConfigHasIssue(rc, "pacing") != test.wantIssue {
				t.Fatalf("resolved run-logic identity mapping = %#v, want issue=%v", rc, test.wantIssue)
			}
		})
	}
}

func TestConvertZipMixedRootPreservesQualifiedRuntimePath(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"script.usr": `[General]
Type=Web
DefaultCfg="profiles\a\default.cfg"
[Actions]
Action=Action.c`,
		"Action.c": `Action()
{
	web_url("home", "URL=https://example.test/", LAST);
	return 0;
}`,
		"profiles/a/default.cfg": `[ThinkTime]
Options=NOTHINK`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || !result.Script.Config.ThinkTime.Ignore {
		t.Fatalf("qualified runtime cfg was not applied after mixed-root extraction: %#v", result.Script)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if got, _ := preserved["mapped_runtime_file"].(string); got != "profiles/a/default.cfg" {
		t.Fatalf("mapped_runtime_file = %q, want profiles/a/default.cfg", got)
	}
}

func TestRuntimeSelectionDoesNotSubstituteForUnresolvedExplicitCfgPointer(t *testing.T) {
	tests := []struct {
		name      string
		pointer   string
		files     map[string]string
		wantTrace string
	}{
		{
			name:      "qualified pointer does not alias another profile basename",
			pointer:   `profiles\a\default.cfg`,
			wantTrace: "profiles/a/default.cfg",
			files: map[string]string{
				"profiles/b/default.cfg": "[ThinkTime]\nOptions=NOTHINK",
				"default.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=7
RunLogicPaceType=Const
RunLogicPaceConstTime=7`,
			},
		},
		{
			name:      "missing explicit pointer does not fall back to default cfg",
			pointer:   "missing.cfg",
			wantTrace: "missing.cfg",
			files: map[string]string{
				"default.cfg": "[ThinkTime]\nOptions=NOTHINK",
				"default.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=8
RunLogicPaceType=Const
RunLogicPaceConstTime=8`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfgName, rc := resolveRuntimeConfig(test.files, &ProjectConfig{DefaultCfg: test.pointer})
			if cfgName != "" {
				t.Fatalf("selected cfg = %q, want no cfg after explicit pointer failure", cfgName)
			}
			if rc == nil || rc.ThinkTimeIgnore || rc.ProxyMode != "" || rc.Iterations != 0 || rc.PacingType != "" {
				t.Fatalf("unrelated cfg settings were applied: %#v", rc)
			}
			if strings.Join(rc.RuntimeFiles, ",") != test.wantTrace {
				t.Fatalf("runtime provenance = %#v, want attempted pointer %q", rc.RuntimeFiles, test.wantTrace)
			}
			issue, ok := runtimeConfigIssueByID(rc, "runtime_profile")
			if !ok || strings.Join(issue.RuntimeFiles, ",") != test.wantTrace ||
				!strings.Contains(issue.Notes, "using APEX defaults") {
				t.Fatalf("runtime-profile issue = %#v, want attempted pointer and default rationale", issue)
			}

			sections := selectedRuntimeSections(collectRuntimeSections(test.files), cfgName, rc)
			if len(sections) != 0 {
				t.Fatalf("unresolved pointer selected unrelated parity sections: %#v", sections)
			}
			parity := applyRuntimeMigrationIssues(nil, rc)
			entry := runtimeParityEntryByID(t, parity, "runtime_profile")
			if strings.Join(entry.RuntimeFiles, ",") != test.wantTrace {
				t.Fatalf("runtime-profile parity provenance = %#v, want %q", entry.RuntimeFiles, test.wantTrace)
			}
		})
	}
}

func TestRuntimeSelectionDoesNotSubstituteForQualifiedRunLogicPointer(t *testing.T) {
	files := map[string]string{
		"active.cfg": `[General]
DefaultRunLogic=profiles/a/default.usp
[Iterations]
NumOfIterations=9
IterationPace=IterationFixed
StartEvery=9`,
		"profiles/b/default.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=4
RunLogicPaceType=Const
RunLogicPaceConstTime=4`,
	}
	cfgName, rc := resolveRuntimeConfig(files, &ProjectConfig{DefaultCfg: "active.cfg"})
	if cfgName != "active.cfg" {
		t.Fatalf("selected cfg = %q, want active.cfg", cfgName)
	}
	if rc.Iterations != 1 || rc.PacingType != "none" || rc.IntervalMs != 0 {
		t.Fatalf("same-basename run logic was applied: %#v", rc)
	}
	if strings.Join(rc.RuntimeFiles, ",") != "active.cfg,profiles/a/default.usp" {
		t.Fatalf("runtime provenance = %#v, want selected cfg and attempted USP", rc.RuntimeFiles)
	}
	for _, id := range []string{"pacing", "run_logic_iterations"} {
		issue, ok := runtimeConfigIssueByID(rc, id)
		if !ok || !strings.Contains(issue.Notes, "was not found") {
			t.Fatalf("%s issue = %#v, want missing qualified USP evidence", id, issue)
		}
	}
}

func TestRuntimeSelectionRequiresDefaultCfgWhenUSRDeclaresMultipleProfiles(t *testing.T) {
	project, err := ParseUSRFile(`[General]
Type=Web
[CfgFiles]
Profile A=profiles/a.cfg
Profile B=profiles/b.cfg
`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "does not choose default cfg",
			files: map[string]string{
				"default.cfg": "[ThinkTime]\nOptions=NOTHINK",
				"default.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=7\nRunLogicPaceType=Const\nRunLogicPaceConstTime=7",
			},
		},
		{
			name: "does not choose sole cfg",
			files: map[string]string{
				"profiles/a.cfg": "[ThinkTime]\nOptions=NOTHINK",
				"default.usp":    "[RunLogicRunRoot]\nRunLogicNumOfIterations=8\nRunLogicPaceType=Const\nRunLogicPaceConstTime=8",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfgName, rc := resolveRuntimeConfig(test.files, project)
			if cfgName != "" || rc == nil {
				t.Fatalf("runtime selection = %q %#v, want APEX defaults with a review finding", cfgName, rc)
			}
			if rc.ThinkTimeIgnore || rc.Iterations != 0 || rc.PacingType != "" {
				t.Fatalf("unselected cfg/USP settings leaked into defaults: %#v", rc)
			}
			issue, ok := runtimeConfigIssueByID(rc, "runtime_profile")
			if !ok || !strings.Contains(issue.Notes, "DefaultCfg is missing") {
				t.Fatalf("runtime-profile issue = %#v, want missing active-profile evidence", issue)
			}
			if strings.Join(issue.RuntimeFiles, ",") != "profiles/a.cfg,profiles/b.cfg" {
				t.Fatalf("runtime-profile provenance = %#v, want both declared profiles", issue.RuntimeFiles)
			}
		})
	}
}

func TestRuntimeSectionsPreserveDistinctRelativePaths(t *testing.T) {
	sections := collectRuntimeSections(map[string]string{
		"profiles/a/default.cfg": "[ThinkTime]\nOptions=RECORDED",
		"profiles/b/default.cfg": "[ThinkTime]\nOptions=NOTHINK",
		"profiles/a/default.usp": "[RunLogicRunRoot]\nRunLogicPaceType=ASAP",
		"profiles/b/default.usp": "[RunLogicRunRoot]\nRunLogicPaceType=Const",
	})
	for _, name := range []string{
		"profiles/a/default.cfg", "profiles/b/default.cfg",
		"profiles/a/default.usp", "profiles/b/default.usp",
	} {
		if _, ok := sections[name]; !ok {
			t.Fatalf("runtime sections lost %q: %#v", name, sections)
		}
	}
}

func TestRuntimeParityPreservesDistinctArchiveRelativePaths(t *testing.T) {
	sections := collectRuntimeSections(map[string]string{
		`profiles\a\default.cfg`: "[ThinkTime]\nOptions=RECORDED",
		"profiles/b/default.cfg": "[ThinkTime]\nOptions=NOTHINK",
	})
	parity := BuildRTSParityReport(sections)
	entry := runtimeParityEntryByID(t, parity, "think_time")
	got := strings.Join(entry.RuntimeFiles, ",")
	if got != "profiles/a/default.cfg,profiles/b/default.cfg" {
		t.Fatalf("think-time provenance = %q, want both normalized archive-relative paths", got)
	}
}

func TestRuntimeCfgProxyEnableFlagsOverrideDormantProxyFields(t *testing.T) {
	cfgName, rc := resolveRuntimeConfig(map[string]string{
		"profiles/disabled.cfg": `[WEB]
ProxyUseProxy=0
ProxyUseProxyServer=false
ProxyHTTPHost=stale-proxy.example.test
ProxyHTTPPort=3128
ProxyUserName=stale-user
ProxyPassword=stale-password
ProxyPasswordIsEncrypted=true
`,
	}, nil)
	if cfgName != "profiles/disabled.cfg" {
		t.Fatalf("selected cfg = %q", cfgName)
	}
	httpConfig := rc.ToHTTPConfig()
	if httpConfig.ProxyMode != "none" || httpConfig.Proxy.URL != "" ||
		httpConfig.Proxy.Username != "" || httpConfig.Proxy.Password != "" {
		t.Fatalf("disabled proxy imported dormant fields: %#v", httpConfig)
	}
	if runtimeConfigHasIssue(rc, "proxy") {
		t.Fatalf("consistent disabled proxy flags should map exactly: %#v", rc.RuntimeIssues)
	}
}

func TestRuntimeCfgProxyMalformedOrConflictingFlagsUseNoProxyWithProvenance(t *testing.T) {
	tests := []struct {
		name  string
		flags string
	}{
		{name: "malformed", flags: "ProxyUseProxy=maybe"},
		{name: "conflicting", flags: "ProxyUseProxy=0\nProxyUseProxyServer=1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"profiles/proxy.cfg": "[WEB]\n" + test.flags + `
ProxyHTTPHost=proxy.example.test
ProxyHTTPPort=3128
`,
			}, nil)
			httpConfig := rc.ToHTTPConfig()
			if httpConfig.ProxyMode != "none" || httpConfig.Proxy.URL != "" {
				t.Fatalf("invalid proxy flags did not fail closed: %#v", httpConfig)
			}
			issue, ok := runtimeConfigIssueByID(rc, "proxy")
			if !ok || len(issue.RuntimeFiles) != 1 || issue.RuntimeFiles[0] != "profiles/proxy.cfg" ||
				!strings.Contains(issue.Notes, "ProxyUseProxy") {
				t.Fatalf("proxy issue = %#v, want raw flag evidence and cfg provenance", issue)
			}
		})
	}
}

func TestRuntimeCfgProxyUnsupportedModeFailsClosedUnlessDormant(t *testing.T) {
	tests := []struct {
		name      string
		enableRow string
		wantIssue bool
	}{
		{name: "mode without enable flag", wantIssue: true},
		{name: "explicitly enabled", enableRow: "ProxyUseProxy=1\n", wantIssue: true},
		{name: "dormant invalid mode", enableRow: "ProxyUseProxy=0\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"profiles/proxy.cfg": `[Proxy]
Mode=automatic
ProxyServer=http://proxy.example.test:3128
ProxyUserName=stale-user
ProxyPassword=stale-password
[WEB]
` + test.enableRow,
			}, nil)
			httpConfig := rc.ToHTTPConfig()
			if httpConfig.ProxyMode != "none" || httpConfig.Proxy.URL != "" {
				t.Fatalf("unsupported proxy mode did not fail closed: %#v", httpConfig)
			}
			issue, ok := runtimeConfigIssueByID(rc, "proxy")
			if ok != test.wantIssue {
				t.Fatalf("proxy issue = %#v, want issue %v", issue, test.wantIssue)
			}
			if test.wantIssue {
				if httpConfig.Proxy.Username != "{{secret.ENTERPRISE_PROXY_USERNAME}}" ||
					httpConfig.Proxy.Password != "{{secret.ENTERPRISE_PROXY_PASSWORD}}" {
					t.Fatalf("unsupported proxy lost inert credential record: %#v", httpConfig.Proxy)
				}
			} else if httpConfig.Proxy.Username != "" || httpConfig.Proxy.Password != "" {
				t.Fatalf("explicitly dormant proxy retained credentials: %#v", httpConfig.Proxy)
			}
			if test.wantIssue && (len(issue.RuntimeFiles) != 1 ||
				issue.RuntimeFiles[0] != "profiles/proxy.cfg" ||
				!strings.Contains(issue.Notes, `automatic`)) {
				t.Fatalf("proxy issue = %#v, want exact mode evidence and cfg provenance", issue)
			}
		})
	}
}

func TestRuntimeCfgProxyWithoutEnableFlagsFinalizesSelectedMode(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		serverRow string
		wantMode  string
		wantIssue bool
	}{
		{name: "none clears dormant endpoint", mode: "none", serverRow: "ProxyServer=http://stale.example.test:3128\n", wantMode: "none"},
		{name: "system clears dormant endpoint", mode: "system", serverRow: "ProxyServer=http://stale.example.test:3128\n", wantMode: "system"},
		{name: "custom requires endpoint", mode: "custom", wantMode: "none", wantIssue: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"profiles/proxy.cfg": "[Proxy]\nMode=" + test.mode + "\n" + test.serverRow +
					"ProxyUserName=stale-user\nProxyPassword=stale-password\n",
			}, nil)
			httpConfig := rc.ToHTTPConfig()
			if httpConfig.ProxyMode != test.wantMode || httpConfig.Proxy.URL != "" {
				t.Fatalf("finalized proxy = %#v, want mode %q without an executable endpoint", httpConfig, test.wantMode)
			}
			issue, ok := runtimeConfigIssueByID(rc, "proxy")
			if ok != test.wantIssue {
				t.Fatalf("proxy issue = %#v, want issue %v", issue, test.wantIssue)
			}
			if test.wantIssue {
				if httpConfig.Proxy.Username != "{{secret.ENTERPRISE_PROXY_USERNAME}}" ||
					httpConfig.Proxy.Password != "{{secret.ENTERPRISE_PROXY_PASSWORD}}" {
					t.Fatalf("review-only proxy lost inert credential record: %#v", httpConfig.Proxy)
				}
			} else if httpConfig.Proxy.Username != "" || httpConfig.Proxy.Password != "" {
				t.Fatalf("inactive proxy retained dormant credentials: %#v", httpConfig.Proxy)
			}
			if test.wantIssue && (len(issue.RuntimeFiles) != 1 ||
				issue.RuntimeFiles[0] != "profiles/proxy.cfg" ||
				!strings.Contains(issue.Notes, "no proxy server URL")) {
				t.Fatalf("proxy issue = %#v, want missing URL and cfg provenance", issue)
			}
		})
	}
}

func TestRuntimeCfgCustomProxyURLValidation(t *testing.T) {
	tests := []struct {
		name      string
		rawURL    string
		enableRow string
		wantValid bool
	}{
		{name: "http", rawURL: "http://proxy.example.test:8080", wantValid: true},
		{name: "https", rawURL: "https://proxy.example.test:8443", wantValid: true},
		{name: "socks5", rawURL: "socks5://proxy.example.test:1080", wantValid: true},
		{name: "socks5h", rawURL: "socks5h://proxy.example.test:1080", wantValid: true},
		{name: "relative", rawURL: "proxy.example.test:8080"},
		{name: "unsupported scheme", rawURL: "ftp://proxy.example.test:21"},
		{name: "missing host", rawURL: "http:///proxy", enableRow: "ProxyUseProxy=1\n"},
		{name: "malformed", rawURL: "http://%zz", enableRow: "ProxyUseProxy=1\n"},
		{name: "port out of range", rawURL: "http://proxy.example.test:99999"},
		{name: "zero port", rawURL: "socks5://proxy.example.test:0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"profiles/proxy.cfg": "[Proxy]\nMode=custom\nProxyServer=" + test.rawURL +
					"\n[WEB]\n" + test.enableRow,
			}, nil)
			httpConfig := rc.ToHTTPConfig()
			issue, hasIssue := runtimeConfigIssueByID(rc, "proxy")
			if test.wantValid {
				if httpConfig.ProxyMode != "custom" || httpConfig.Proxy.URL == "" || hasIssue {
					t.Fatalf("valid proxy was rejected: config=%#v issue=%#v", httpConfig, issue)
				}
				return
			}
			if httpConfig.ProxyMode != "none" || httpConfig.Proxy.URL != "" || !hasIssue {
				t.Fatalf("invalid proxy did not fail closed: config=%#v issue=%#v", httpConfig, issue)
			}
			if len(issue.RuntimeFiles) != 1 || issue.RuntimeFiles[0] != "profiles/proxy.cfg" ||
				!strings.Contains(issue.Notes, "absolute http, https, socks5, or socks5h") {
				t.Fatalf("proxy issue = %#v, want validation rationale and cfg provenance", issue)
			}
		})
	}
}

func TestRuntimeCfgProxyUseSameAndSeparateEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		useSame     string
		httpsPort   string
		wantMode    string
		wantURL     string
		wantPartial bool
	}{
		{name: "same flag chooses HTTP endpoint", useSame: "1", httpsPort: "8443", wantMode: "custom", wantURL: "http://proxy.example.test:3128"},
		{name: "separate equivalent endpoints are representable", useSame: "0", httpsPort: "3128", wantMode: "custom", wantURL: "http://proxy.example.test:3128"},
		{name: "separate distinct endpoints fail closed", useSame: "0", httpsPort: "8443", wantMode: "none", wantPartial: true},
		{name: "malformed same flag fails closed", useSame: "maybe", httpsPort: "3128", wantMode: "none", wantPartial: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"profiles/proxy.cfg": `[WEB]
ProxyUseProxy=1
ProxyHTTPHost=proxy.example.test
ProxyHTTPPort=3128
ProxyHTTPSHost=proxy.example.test
ProxyHTTPSPort=` + test.httpsPort + `
ProxyUseSame=` + test.useSame,
			}, nil)
			httpConfig := rc.ToHTTPConfig()
			if httpConfig.ProxyMode != test.wantMode || httpConfig.Proxy.URL != test.wantURL {
				t.Fatalf("proxy mapping = %#v, want mode=%q URL=%q", httpConfig, test.wantMode, test.wantURL)
			}
			if runtimeConfigHasIssue(rc, "proxy") != test.wantPartial {
				t.Fatalf("proxy issues = %#v, want partial %v", rc.RuntimeIssues, test.wantPartial)
			}
		})
	}
}

func TestRuntimeCfgProxyBypassAndNoLocalMigration(t *testing.T) {
	t.Run("exact bypass list", func(t *testing.T) {
		cfgName, rc := resolveRuntimeConfig(map[string]string{
			"profiles/proxy.cfg": `[Proxy]
Mode=custom
ProxyServer=http://proxy.example.test:3128
ProxyBypass=localhost;*.internal.example,10.0.0.0/8
ProxyNoLocal=0`,
		}, nil)
		httpConfig := rc.ToHTTPConfig()
		want := "localhost,*.internal.example,10.0.0.0/8"
		if httpConfig.ProxyMode != "custom" || strings.Join(httpConfig.Proxy.NoProxy, ",") != want ||
			runtimeConfigHasIssue(rc, "proxy") {
			t.Fatalf("proxy bypass mapping = %#v issues=%#v", httpConfig, rc.RuntimeIssues)
		}
		sections := selectedRuntimeSections(collectRuntimeSections(map[string]string{
			"profiles/proxy.cfg": `[Proxy]
Mode=custom
ProxyServer=http://proxy.example.test:3128
ProxyBypass=localhost;*.internal.example,10.0.0.0/8
ProxyNoLocal=0`,
		}), cfgName, rc)
		entry := runtimeParityEntryByID(t, buildRTSParityReportForProtocol(sections, "http"), "proxy")
		if entry.Status != RuntimeSettingStatusExecuted || strings.Join(entry.RuntimeFiles, ",") != "profiles/proxy.cfg" {
			t.Fatalf("proxy parity = %#v", entry)
		}
	})

	for _, raw := range []string{"1", "maybe"} {
		t.Run("no_local_"+raw, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{
				"profiles/proxy.cfg": `[Proxy]
Mode=custom
ProxyServer=http://proxy.example.test:3128
ProxyNoLocal=` + raw,
			}, nil)
			httpConfig := rc.ToHTTPConfig()
			issue, ok := runtimeConfigIssueByID(rc, "proxy")
			if httpConfig.ProxyMode != "none" || !ok || !strings.Contains(issue.Notes, "ProxyNoLocal") {
				t.Fatalf("ProxyNoLocal=%q did not fail closed with review evidence: config=%#v issue=%#v", raw, httpConfig, issue)
			}
		})
	}
}

func TestRuntimeCfgProxyRejectsInvalidSplitPort(t *testing.T) {
	_, rc := resolveRuntimeConfig(map[string]string{
		"profiles/proxy.cfg": `[WEB]
ProxyUseProxy=1
ProxyUseSame=1
ProxyHTTPHost=proxy.example.test
ProxyHTTPPort=99999`,
	}, nil)
	httpConfig := rc.ToHTTPConfig()
	issue, ok := runtimeConfigIssueByID(rc, "proxy")
	if httpConfig.ProxyMode != "none" || !ok || !strings.Contains(issue.Notes, "1..65535") {
		t.Fatalf("out-of-range split proxy port did not fail closed: config=%#v issue=%#v", httpConfig, issue)
	}
}

func TestRuntimeCfgOnlyIterationsAndPacingValidateActiveValues(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantIterations int
		wantPacing     string
		wantInterval   int
		wantIssues     []string
	}{
		{
			name: "valid fixed interval",
			body: `[Iterations]
NumOfIterations=3
[Pacing]
Mode=fixed_interval
IntervalMs=5000`,
			wantIterations: 3,
			wantPacing:     "fixed_interval",
			wantInterval:   5000,
		},
		{
			name: "missing values",
			body: `[Iterations]
[Pacing]`,
			wantIterations: 1,
			wantPacing:     "none",
			wantIssues:     []string{"run_logic_iterations", "pacing"},
		},
		{
			name: "malformed and unsupported",
			body: `[Iterations]
NumOfIterations=bad
[Pacing]
Mode=unsupported
IntervalMs=5000`,
			wantIterations: 1,
			wantPacing:     "none",
			wantIssues:     []string{"run_logic_iterations", "pacing"},
		},
		{
			name: "out of range",
			body: `[Iterations]
NumOfIterations=1000000001
[Pacing]
Mode=fixed_interval
IntervalMs=3600001`,
			wantIterations: 1,
			wantPacing:     "none",
			wantIssues:     []string{"run_logic_iterations", "pacing"},
		},
		{
			name: "invalid random bounds",
			body: `[Iterations]
NumOfIterations=1
[Pacing]
Mode=random_delay
MinDelayMs=5000
MaxDelayMs=1000`,
			wantIterations: 1,
			wantPacing:     "none",
			wantIssues:     []string{"pacing"},
		},
		{
			name: "inactive stale values ignored",
			body: `[Iterations]
NumOfIterations=1
[Pacing]
Mode=none
IntervalMs=not-a-number`,
			wantIterations: 1,
			wantPacing:     "none",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rc := resolveRuntimeConfig(map[string]string{"cfg-only.cfg": test.body}, nil)
			if rc.Iterations != test.wantIterations || rc.PacingType != test.wantPacing ||
				rc.IntervalMs != test.wantInterval {
				t.Fatalf("cfg runtime = %#v", rc)
			}
			for _, id := range test.wantIssues {
				if !runtimeConfigHasIssue(rc, id) {
					t.Fatalf("missing %s issue in %#v", id, rc.RuntimeIssues)
				}
			}
			if len(test.wantIssues) == 0 && (runtimeConfigHasIssue(rc, "run_logic_iterations") || runtimeConfigHasIssue(rc, "pacing")) {
				t.Fatalf("exact cfg runtime was marked partial: %#v", rc.RuntimeIssues)
			}
		})
	}
}

func TestRuntimeCfgLoadRunnerIterationsPacingFallback(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		wantType        string
		wantDelay       int
		wantMinDelay    int
		wantMaxDelay    int
		wantInterval    int
		wantMinInterval int
		wantMaxInterval int
		wantPartial     bool
		wantEvidence    string
	}{
		{
			name: "as soon as possible ignores dormant values",
			body: `IterationPace=IterationASAP
StartEvery=not-a-number
RandomMin=also-invalid
RandomMax=also-invalid`,
			wantType: "none",
		},
		{
			name:         "fixed interval with decimal seconds",
			body:         "IterationPace=IterationFixed\nStartEvery=5.25",
			wantType:     "fixed_interval",
			wantInterval: 5250,
		},
		{
			name:            "random interval",
			body:            "IterationPace=IterationRandom\nRandomMin=1.25\nRandomMax=2.5",
			wantType:        "random_interval",
			wantMinInterval: 1250,
			wantMaxInterval: 2500,
		},
		{
			name:      "fixed delay",
			body:      "IterationPace=IterationFixedAfter\nStartEvery=3.5",
			wantType:  "fixed_delay",
			wantDelay: 3500,
		},
		{
			name:         "random delay",
			body:         "IterationPace=IterationAfter\nRandomMin=2\nRandomMax=4",
			wantType:     "random_delay",
			wantMinDelay: 2000,
			wantMaxDelay: 4000,
		},
		{
			name:         "unsupported mode defaults for review",
			body:         "IterationPace=IterationMystery\nStartEvery=5",
			wantType:     "none",
			wantPartial:  true,
			wantEvidence: "IterationMystery",
		},
		{
			name:         "malformed active value defaults for review",
			body:         "IterationPace=IterationFixed\nStartEvery=not-a-number",
			wantType:     "none",
			wantPartial:  true,
			wantEvidence: "StartEvery",
		},
		{
			name:         "reversed active bounds default for review",
			body:         "IterationPace=IterationRandom\nRandomMin=9\nRandomMax=2",
			wantType:     "none",
			wantPartial:  true,
			wantEvidence: "bounds",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfgName, rc := resolveRuntimeConfig(map[string]string{
				"profiles/default.cfg": "[Iterations]\nNumOfIterations=3\n" + test.body,
			}, nil)
			if cfgName != "profiles/default.cfg" || rc.Iterations != 3 ||
				rc.PacingType != test.wantType ||
				rc.DelayMs != test.wantDelay ||
				rc.MinDelayMs != test.wantMinDelay ||
				rc.MaxDelayMs != test.wantMaxDelay ||
				rc.IntervalMs != test.wantInterval ||
				rc.MinIntervalMs != test.wantMinInterval ||
				rc.MaxIntervalMs != test.wantMaxInterval {
				t.Fatalf("cfg fallback = %#v", rc)
			}
			issue, partial := runtimeConfigIssueByID(rc, "pacing")
			if partial != test.wantPartial {
				t.Fatalf("pacing issue = %#v, want partial %v", issue, test.wantPartial)
			}
			if partial && (len(issue.RuntimeFiles) != 1 ||
				issue.RuntimeFiles[0] != "profiles/default.cfg" ||
				!strings.Contains(issue.Notes, test.wantEvidence)) {
				t.Fatalf("pacing issue = %#v, want %q and cfg provenance", issue, test.wantEvidence)
			}

			sections := selectedRuntimeSections(
				collectRuntimeSections(map[string]string{
					"profiles/default.cfg": "[Iterations]\nNumOfIterations=3\n" + test.body,
				}),
				cfgName,
				rc,
			)
			parity := applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(sections, "http"), rc)
			entry := runtimeParityEntryByID(t, parity, "pacing")
			wantStatus := RuntimeSettingStatusExecuted
			if test.wantPartial {
				wantStatus = RuntimeSettingStatusPartial
			}
			if entry.Status != wantStatus || strings.Join(entry.RuntimeFiles, ",") != "profiles/default.cfg" {
				t.Fatalf("pacing parity = %#v, want %s with cfg provenance", entry, wantStatus)
			}
		})
	}
}

func TestAuthoritativeUSPDiscardsStaleCfgRunLogicIssues(t *testing.T) {
	files := map[string]string{
		"active.cfg": `[General]
DefaultRunLogic=active.usp
[Iterations]
NumOfIterations=bad
[Pacing]
Mode=unsupported`,
		"active.usp": `[RunLogicRunRoot]
RunLogicNumOfIterations=2
RunLogicPaceType=Const
RunLogicPaceConstTime=5`,
	}
	_, rc := resolveRuntimeConfig(files, nil)
	if rc.Iterations != 2 || rc.PacingType != "fixed_interval" || rc.IntervalMs != 5000 {
		t.Fatalf("authoritative USP was not applied: %#v", rc)
	}
	if runtimeConfigHasIssue(rc, "run_logic_iterations") || runtimeConfigHasIssue(rc, "pacing") {
		t.Fatalf("stale cfg run settings remained review blockers: %#v", rc.RuntimeIssues)
	}
}

func TestLegacyRuntimeThinkTimeNumericValuesValidateBounds(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing multiplier", body: "Multiplier="},
		{name: "nan multiplier", body: "Multiplier=NaN"},
		{name: "negative multiplier", body: "Multiplier=-1"},
		{name: "oversized multiplier", body: "Multiplier=101"},
		{name: "missing limit", body: "LimitMs="},
		{name: "nan limit", body: "LimitMs=NaN"},
		{name: "negative limit", body: "LimitMs=-1"},
		{name: "oversized limit", body: "LimitMs=3600001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg("[ThinkTime]\n" + test.body)
			if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 1 || rc.ThinkTimeLimitMs != 0 {
				t.Fatalf("legacy think-time fallback = %#v", rc)
			}
			if !runtimeConfigHasIssue(rc, "think_time") {
				t.Fatalf("invalid legacy value was not marked partial: %#v", rc.RuntimeIssues)
			}
		})
	}

	rc := ParseRuntimeCfg("[ThinkTime]\nMultiplier=0.5\nLimitMs=1250")
	if rc.ThinkTimeType != "recorded" || rc.ThinkTimeMultiplier != 0.5 || rc.ThinkTimeLimitMs != 1250 ||
		runtimeConfigHasIssue(rc, "think_time") {
		t.Fatalf("valid legacy think time = %#v", rc)
	}
}

func TestAttachSourceMetadataEmitsRuntimeIssuesWithoutRuntimeSections(t *testing.T) {
	rc := ParseRuntimeCfg("")
	rc.RuntimeFiles = []string{"missing.cfg"}
	rc.RuntimeIssues = []RuntimeMigrationIssue{{
		ID: "runtime_profile", Area: "Runtime Settings",
		Notes: "Active runtime cfg \"missing.cfg\" was not found.",
	}}
	script := &dsl.Script{Name: "missing runtime", Protocol: "http"}
	attachSourceMetadata(script, nil, "", nil, nil, map[string]string{}, "", rc, nil, nil, nil)
	if script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatal("runtime issue did not create import metadata")
	}
	parity, ok := script.Metadata.Import.Preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
	if !ok || len(parity) != 1 || parity[0].ID != "runtime_profile" || parity[0].Status != RuntimeSettingStatusPartial {
		t.Fatalf("runtime parity = %#v, want one runtime_profile partial", script.Metadata.Import.Preserved["runtime_settings_parity"])
	}
	counts, _ := script.Metadata.Import.Preserved["runtime_settings_status_counts"].(map[string]int)
	if counts[string(RuntimeSettingStatusPartial)] != 1 {
		t.Fatalf("runtime status counts = %#v, want partial=1", counts)
	}
}

func TestApplyRuntimeMigrationIssuesUnionsRepeatedIssueProvenance(t *testing.T) {
	rc := &RuntimeConfig{
		RuntimeFiles: []string{"profiles/active.cfg", "profiles/selected.usp"},
		RuntimeIssues: []RuntimeMigrationIssue{
			{
				ID: "pacing", Area: "Pacing", Notes: "cfg pacing value is malformed.",
				RuntimeFiles: []string{`profiles\active.cfg`},
			},
			{
				ID: "pacing", Area: "Pacing", Notes: "USP pacing bounds are invalid.",
				RuntimeFiles: []string{"PROFILES/ACTIVE.CFG", "profiles/selected.usp"},
			},
		},
	}
	parity := applyRuntimeMigrationIssues([]RuntimeSettingParityEntry{{
		ID: "pacing", Area: "Pacing", Concept: "Pacing",
		Status: RuntimeSettingStatusExecuted,
	}}, rc)
	entry := runtimeParityEntryByID(t, parity, "pacing")
	if entry.Status != RuntimeSettingStatusPartial {
		t.Fatalf("pacing status = %q, want partial", entry.Status)
	}
	if strings.Join(entry.RuntimeFiles, ",") != "profiles/active.cfg,profiles/selected.usp" {
		t.Fatalf("pacing provenance = %#v, want normalized union without duplicates", entry.RuntimeFiles)
	}
	for _, evidence := range []string{"cfg pacing value is malformed.", "USP pacing bounds are invalid."} {
		if !strings.Contains(entry.Notes, evidence) {
			t.Fatalf("pacing notes = %q, missing %q", entry.Notes, evidence)
		}
	}
}

func TestApplyRuntimeMigrationIssuesPreservesExistingParityProvenance(t *testing.T) {
	rc := &RuntimeConfig{RuntimeIssues: []RuntimeMigrationIssue{{
		ID: "pacing", Area: "Pacing", Notes: "selected USP is malformed.",
		RuntimeFiles: []string{"profiles/selected.usp"},
	}}}
	parity := applyRuntimeMigrationIssues([]RuntimeSettingParityEntry{{
		ID: "pacing", Area: "Pacing", Status: RuntimeSettingStatusExecuted,
		RuntimeFiles: []string{"profiles/active.cfg"},
	}}, rc)
	entry := runtimeParityEntryByID(t, parity, "pacing")
	if strings.Join(entry.RuntimeFiles, ",") != "profiles/active.cfg,profiles/selected.usp" {
		t.Fatalf("pacing provenance = %#v, want existing parity files unioned with issue files", entry.RuntimeFiles)
	}
}

func TestParseConfigSectionsRetainsSettingsAfterLongLine(t *testing.T) {
	content := "[Metadata]\nOpaque=" + strings.Repeat("x", 70<<10) + "\n[Proxy]\nMode=custom\nProxyServer=http://proxy.example.test:3128\n"
	sections := ParseConfigSections(content)
	if got := sections["Proxy"]["Mode"]; got != "custom" {
		t.Fatalf("Mode after >64 KiB line = %q, want custom", got)
	}
	rc := ParseRuntimeCfg(content)
	if httpConfig := rc.ToHTTPConfig(); httpConfig.ProxyMode != "custom" ||
		httpConfig.Proxy.URL != "http://proxy.example.test:3128" ||
		runtimeConfigHasIssue(rc, "proxy") {
		t.Fatalf("runtime config after long line was truncated: config=%#v issues=%#v", httpConfig, rc.RuntimeIssues)
	}
}

func runtimeConfigHasIssue(rc *RuntimeConfig, id string) bool {
	if rc == nil {
		return false
	}
	for _, issue := range rc.RuntimeIssues {
		if issue.ID == id {
			return true
		}
	}
	return false
}

func runtimeConfigIssueByID(rc *RuntimeConfig, id string) (RuntimeMigrationIssue, bool) {
	if rc == nil {
		return RuntimeMigrationIssue{}, false
	}
	for _, issue := range rc.RuntimeIssues {
		if issue.ID == id {
			return issue, true
		}
	}
	return RuntimeMigrationIssue{}, false
}
