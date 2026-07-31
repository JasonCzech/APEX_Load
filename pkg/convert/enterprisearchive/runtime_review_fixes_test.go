package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
)

// Conflicting case-insensitive RunLogicRunMode declarations must fail closed
// to the engine default instead of silently letting the last spelling win.
func TestApplyRuntimeUSPRunModeConflictFailsClosed(t *testing.T) {
	rc := ParseRuntimeCfg("")
	applyRuntimeUSP(rc, "default.usp", strings.Join([]string{
		"[RunLogicRunRoot]",
		`RunLogicNumOfIterations="2"`,
		`RunLogicRunMode="Sequential"`,
		`runlogicrunmode="Random"`,
		`RunLogicPaceType="ASAP"`,
	}, "\n"))
	if rc.BlockScheduling != "" {
		t.Fatalf("block scheduling = %q, want cleared on conflicting declarations", rc.BlockScheduling)
	}
	found := false
	for _, issue := range rc.RuntimeIssues {
		if issue.ID == "run_logic_block_scheduling" && strings.Contains(issue.Notes, "conflicting case-insensitive RunLogicRunMode") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing fail-closed run-mode conflict issue: %#v", rc.RuntimeIssues)
	}
}

func TestApplyRuntimeUSPActionSequenceConflictFailsClosed(t *testing.T) {
	rc := ParseRuntimeCfg("")
	applyRuntimeUSP(rc, "default.usp", strings.Join([]string{
		"[Profile Actions]",
		"Profile Actions name=vuser_init,Action,vuser_end",
		"profile actions NAME=vuser_init,Other,vuser_end",
		"[RunLogicRunRoot]",
		"RunLogicActionOrder=Action",
		"RunLogicNumOfIterations=1",
		"RunLogicPaceType=ASAP",
	}, "\n"))
	if len(rc.ActionSequence) != 0 {
		t.Fatalf("action sequence = %#v, want cleared on conflicting declarations", rc.ActionSequence)
	}
	if !runtimeConfigHasIssue(rc, "run_logic_action_sequence") {
		t.Fatalf("missing fail-closed action-sequence conflict issue: %#v", rc.RuntimeIssues)
	}
}

// Spill-row base extraction slices the lowercased key; a key built from runes
// whose lowercase form changes byte length must classify without panicking.
func TestClassifyRuntimeSpillRowHandlesMultiByteLowercase(t *testing.T) {
	// U+023A lowercases to U+2C65 (2 bytes -> 3 bytes).
	sections := map[string]map[string]map[string]string{
		"default.cfg": {
			"WEB": {
				"ȺExotic_extra_arg_2": "value",
			},
		},
	}
	dispositions := classifyRuntimeSelectedSections(sections, "http")
	disposition, ok := dispositions["default.cfg"]["WEB"]["ȺExotic_extra_arg_2"]
	if !ok {
		t.Fatalf("exotic spill key was not classified: %#v", dispositions)
	}
	switch disposition.Status {
	case RuntimeSettingDispositionMapped, RuntimeSettingDispositionCompatibilityOnly, RuntimeSettingDispositionNonApplicable:
	default:
		t.Fatalf("exotic spill key disposition = %#v, want a classified status", disposition)
	}
}

func TestParseUSRFileTransactionOrderPreservesQuotedSeparatorsAndDeduplicatesCase(t *testing.T) {
	project, err := ParseUSRFile(`[TransactionsOrder]
Order="Checkout, retail","Pipe|name","Semi;name","Path\Name",Customer's order
[Transactions]
CHECKOUT, RETAIL=
Additional=
`)
	if err != nil {
		t.Fatalf("ParseUSRFile: %v", err)
	}
	want := []string{"Checkout, retail", "Pipe|name", "Semi;name", `Path\Name`, "Customer's order", "Additional"}
	if strings.Join(project.Transactions, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("transactions = %#v, want %#v", project.Transactions, want)
	}

	if _, err := ParseUSRFile("[TransactionsOrder]\nOrder=\"unterminated\n"); err == nil ||
		!strings.Contains(err.Error(), "unterminated quoted transaction name") {
		t.Fatalf("unterminated transaction order error = %v", err)
	}
}

func TestDeclaredTransactionConservationCountsStartedAndPublishedTransactions(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"Transactions.usr": `[General]
Type=Multi
ScriptLanguage=C
[Actions]
Action=Action.c
[Transactions]
Started=
Published=
`,
		"Action.c": `Action() {
lr_start_transaction("Started");
web_url("Request", "URL=https://example.test", LAST);
lr_end_transaction("Started", LR_AUTO);
lr_set_transaction("Published", 1.25, LR_PASS);
return 0;
}`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "no converted action starts or publishes") {
			t.Fatalf("false declared-transaction finding: %q", warning)
		}
	}
}

func TestDeclaredTransactionConservationCountsReachableJavaTransactions(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"JavaTransactions.usr": `[General]
Type=Java Vuser
ScriptLanguage=JAVA
[Actions]
Action=Actions.java
[Transactions]
BusinessCall=
`,
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    lr.start_transaction("BusinessCall");
    lr.end_transaction("BusinessCall", lr.AUTO);
    return 0;
  }
}`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "no converted action starts or publishes") {
			t.Fatalf("false Java declared-transaction finding: %q", warning)
		}
	}
}

func TestDeclaredTransactionConservationSkipsUntrackedImportBranchesAndBoundsEvidence(t *testing.T) {
	untracked := &ConvertResult{}
	reviewDeclaredTransactionConservation(untracked, "", &ProjectConfig{Transactions: []string{"Declared"}}, nil)
	if len(untracked.Warnings) != 0 {
		t.Fatalf("untracked branch received transaction warning: %#v", untracked.Warnings)
	}

	transactions := make([]string, 45)
	for index := range transactions {
		transactions[index] = fmt.Sprintf("T%02d", index)
	}
	tracked := &ConvertResult{sourceTransactionNames: make(map[string]struct{})}
	reviewDeclaredTransactionConservation(tracked, "", &ProjectConfig{Transactions: transactions}, nil)
	if len(tracked.Warnings) != 1 ||
		!strings.Contains(tracked.Warnings[0], "declares 45 transactions") ||
		!strings.Contains(tracked.Warnings[0], "+5 more") ||
		strings.Contains(tracked.Warnings[0], "T44") {
		t.Fatalf("bounded transaction evidence = %#v", tracked.Warnings)
	}

	runtimeCfg := &RuntimeConfig{RuntimeFiles: []string{"default.cfg", "default.usp"}}
	withRuntime := &ConvertResult{sourceTransactionNames: make(map[string]struct{})}
	reviewDeclaredTransactionConservation(
		withRuntime,
		"Project.usr",
		&ProjectConfig{Transactions: []string{"Missing"}},
		runtimeCfg,
	)
	entry := runtimeParityEntryByID(t, applyRuntimeMigrationIssues(nil, runtimeCfg), "transactions")
	if strings.Join(entry.RuntimeFiles, ",") != "Project.usr" {
		t.Fatalf("transaction provenance = %#v, want Project.usr", entry.RuntimeFiles)
	}

	sensitive := &ConvertResult{sourceTransactionNames: make(map[string]struct{})}
	reviewDeclaredTransactionConservation(
		sensitive,
		"",
		&ProjectConfig{Transactions: []string{"token=transaction-secret-canary"}},
		nil,
	)
	if len(sensitive.Warnings) != 1 || strings.Contains(sensitive.Warnings[0], "transaction-secret-canary") {
		t.Fatalf("transaction evidence disclosed source credential: %#v", sensitive.Warnings)
	}
}

func TestApplyRuntimeUSPInvalidOverridesClearStaleCfgRunLogic(t *testing.T) {
	rc := &RuntimeConfig{
		BlockScheduling: "random",
		ActionSequence:  []string{"vuser_end", "action"},
	}
	applyRuntimeUSP(rc, "selected.usp", `[Profile Actions]
Profile Actions name=vuser_init,Action,vuser_end
[RunLogicRunRoot]
RunLogicRunMode=Unsupported
RunLogicActionOrder=DifferentAction
RunLogicNumOfIterations=1
RunLogicPaceType=ASAP
`)
	if rc.BlockScheduling != "" {
		t.Fatalf("block scheduling = %q, want stale cfg value cleared", rc.BlockScheduling)
	}
	if len(rc.ActionSequence) != 0 {
		t.Fatalf("action sequence = %#v, want stale cfg value cleared", rc.ActionSequence)
	}
	for _, id := range []string{"run_logic_block_scheduling", "run_logic_action_sequence"} {
		if !runtimeConfigHasIssue(rc, id) {
			t.Fatalf("missing %s review issue: %#v", id, rc.RuntimeIssues)
		}
	}
}

func TestApplyRuntimeUSPRejectsNonStandardLifecycleOrder(t *testing.T) {
	rc := ParseRuntimeCfg("")
	applyRuntimeUSP(rc, "selected.usp", `[Profile Actions]
Profile Actions name=Action,vuser_init,vuser_end
[RunLogicRunRoot]
RunLogicRunMode=Sequential
RunLogicActionOrder=Action
RunLogicNumOfIterations=1
RunLogicPaceType=ASAP
`)
	if len(rc.ActionSequence) != 0 || !runtimeConfigHasIssue(rc, "run_logic_action_sequence") {
		t.Fatalf("non-standard lifecycle = %#v / %#v, want review-only unset sequence", rc.ActionSequence, rc.RuntimeIssues)
	}
}

func TestApplyRuntimeUSPRejectsExplicitEmptyActionList(t *testing.T) {
	rc := &RuntimeConfig{ActionSequence: []string{"action", "vuser_end"}}
	applyRuntimeUSP(rc, "selected.usp", `[Profile Actions]
Profile Actions name=
[RunLogicRunRoot]
RunLogicNumOfIterations=1
RunLogicPaceType=ASAP
`)
	if len(rc.ActionSequence) != 0 || !runtimeConfigHasIssue(rc, "run_logic_action_sequence") {
		t.Fatalf("empty selected lifecycle = %#v / %#v, want stale sequence cleared with review", rc.ActionSequence, rc.RuntimeIssues)
	}
}

func TestRuntimeUSPErrorHandlerMatchesDeclaredFilenameStemAndFunction(t *testing.T) {
	content := `[RunLogicErrorHandlerRoot]
MercIniTreeSons=vuser_errorhandler
RunLogicActionOrder=vuser_errorhandler
`
	tests := []ProjectConfig{
		{ActionFiles: []string{"actions/vuser_errorhandler.c"}},
		{SourceActionFiles: []SourceActionFile{{Name: "handlers/error.c", Function: "vuser_errorhandler"}}},
	}
	for index := range tests {
		rc := ParseRuntimeCfg("")
		noteRuntimeUSPErrorHandler(rc, "default.usp", content, &tests[index], nil)
		issue, ok := runtimeConfigIssueByID(rc, "run_logic_error_handler")
		if !ok || issue.Status != RuntimeSettingStatusCompatibilityOnly {
			t.Fatalf("project %d error-handler issue = %#v, want compatibility_only", index, rc.RuntimeIssues)
		}
	}
}

func TestRuntimeUSPErrorHandlerUsesArchiveInventoryWithoutProject(t *testing.T) {
	rc := ParseRuntimeCfg("")
	noteRuntimeUSPErrorHandler(rc, "default.usp", `[RunLogicErrorHandlerRoot]
RunLogicActionOrder=vuser_errorhandler
`, nil, map[string]string{"handlers/vuser_errorhandler.c": "vuser_errorhandler() { return 0; }"})
	issue, ok := runtimeConfigIssueByID(rc, "run_logic_error_handler")
	if !ok || issue.Status != RuntimeSettingStatusCompatibilityOnly {
		t.Fatalf("archive error-handler issue = %#v, want compatibility_only", rc.RuntimeIssues)
	}
}

func TestNestedRunLogicBehaviorIsNotClassifiedAsBookkeeping(t *testing.T) {
	dispositions := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{
		"default.usp": {
			"RunLogicRunRoot:Block1": {
				"MercIniTreeFather":   "RunLogicRunRoot",
				"RunLogicRunMode":     "Random",
				"RunLogicActionOrder": "Action1,Action2",
			},
		},
	}, "http")
	rows := dispositions["default.usp"]["RunLogicRunRoot:Block1"]
	if rows["MercIniTreeFather"].Status != RuntimeSettingDispositionNonApplicable {
		t.Fatalf("tree parent disposition = %#v, want bookkeeping", rows["MercIniTreeFather"])
	}
	for _, key := range []string{"RunLogicRunMode", "RunLogicActionOrder"} {
		if rows[key].Status != RuntimeSettingDispositionCompatibilityOnly {
			t.Fatalf("%s disposition = %#v, want compatibility_only behavior", key, rows[key])
		}
	}
}

func TestRuntimeDispositionRollupsUseStatusSpecificFileProvenance(t *testing.T) {
	dispositions := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{
		"active.cfg": {"WEB": {"UnknownActiveSetting": "1"}},
		"tree.usp":   {"RunLogicRunRoot:Action": {"Name": "Action"}},
	}, "http")
	parity := appendRuntimeDispositionParityEntries(nil, dispositions)
	unmapped := runtimeParityEntryByID(t, parity, "runtime_unmapped")
	if strings.Join(unmapped.RuntimeFiles, ",") != "active.cfg" {
		t.Fatalf("unmapped files = %#v, want only active.cfg", unmapped.RuntimeFiles)
	}
	nonApplicable := runtimeParityEntryByID(t, parity, "runtime_non_applicable")
	if strings.Join(nonApplicable.RuntimeFiles, ",") != "tree.usp" {
		t.Fatalf("non-applicable files = %#v, want only tree.usp", nonApplicable.RuntimeFiles)
	}
}

func TestRuntimeDispositionDoesNotClaimShadowedSourceAliasesWereMapped(t *testing.T) {
	dispositions := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{
		"default.cfg": {
			"Network Speed Simulation": {"Mode": "custom", "BandwidthKbps": "1000"},
			"ModemSpeed":               {"EnableModemSpeed": "1", "ModemSpeed": "128000"},
			"Download Filters":         {"Mode": "allow_list", "Hosts": "native.example.test"},
			"FILTERS":                  {"IncludeFiltersInList": "source.example.test"},
		},
	}, "http")
	rows := dispositions["default.cfg"]
	for section, key := range map[string]string{
		"ModemSpeed": "ModemSpeed",
		"FILTERS":    "IncludeFiltersInList",
	} {
		disposition := rows[section][key]
		if disposition.Status != RuntimeSettingDispositionNonApplicable ||
			!strings.Contains(disposition.Note, "Superseded") {
			t.Fatalf("[%s] %s disposition = %#v, want superseded non-applicable", section, key, disposition)
		}
	}
}

func TestRuntimeDispositionAccountsForDormantChildSettings(t *testing.T) {
	dispositions := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{
		"default.cfg": {
			"General": {
				"AutomaticTransactions":         "0",
				"automatic_nested_transactions": "1",
			},
			"WEB": {
				"SimulateNewUser":              "0",
				"ClearCacheForSimulateNewUser": "1",
			},
			"ModemSpeed": {
				"EnableModemSpeed":       "0",
				"EnableCustomModemSpeed": "0",
				"ModemSpeed":             "128000",
				"CustomModemSpeed":       "1000",
			},
			"FILTERS": {
				"IncludeFiltersInList": "",
				"ExcludeFiltersInList": "",
			},
		},
	}, "http")
	rows := dispositions["default.cfg"]
	for section, keys := range map[string][]string{
		"General":    {"automatic_nested_transactions"},
		"WEB":        {"ClearCacheForSimulateNewUser"},
		"ModemSpeed": {"ModemSpeed", "CustomModemSpeed"},
		"FILTERS":    {"IncludeFiltersInList", "ExcludeFiltersInList"},
	} {
		for _, key := range keys {
			if got := rows[section][key].Status; got != RuntimeSettingDispositionNonApplicable {
				t.Fatalf("[%s] %s disposition = %q, want dormant non_applicable", section, key, got)
			}
		}
	}
}

func TestRuntimeSpillRowsRequirePositiveIndexes(t *testing.T) {
	for _, key := range []string{"Setting_extra_arg_0", "Setting_extra_arg_-1", "Setting_extra_arg_bad"} {
		if base, ok := runtimeSpillRowBaseKey(key); ok {
			t.Fatalf("runtimeSpillRowBaseKey(%q) = %q, true; want malformed row rejection", key, base)
		}
	}
}

func TestUnknownNegativeRuntimeValueRequiresReview(t *testing.T) {
	dispositions := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{
		"default.cfg": {"WEB": {"UnknownLimit": "-1"}},
	}, "http")
	if got := dispositions["default.cfg"]["WEB"]["UnknownLimit"].Status; got != RuntimeSettingDispositionCompatibilityOnly {
		t.Fatalf("negative unknown disposition = %q, want compatibility_only", got)
	}
}

func TestParseRuntimeCfgRejectsInvalidAutoLogSelector(t *testing.T) {
	rc := ParseRuntimeCfg("[Log]\nAutoLog=occasionally\n")
	if rc.OnErrorOnly || !runtimeConfigHasIssue(rc, "logging") {
		t.Fatalf("invalid AutoLog = %#v, want disabled with review issue", rc)
	}
}

func TestSourceDownloadFiltersPreservePortsAndRejectPathPatterns(t *testing.T) {
	rc := ParseRuntimeCfg(`[FILTERS]
IncludeFiltersInList=https://CDN.Example.test:8443/;https://cdn.example.test/private;////
ExcludeFiltersInList=
`)
	if rc.DownloadFilterMode != "allow_list" ||
		len(rc.DownloadFilterHosts) != 1 ||
		rc.DownloadFilterHosts[0] != "cdn.example.test:8443" {
		t.Fatalf("download filters = %q/%#v, want exact host and port", rc.DownloadFilterMode, rc.DownloadFilterHosts)
	}
	if !runtimeConfigHasIssue(rc, "download_filters") {
		t.Fatalf("path-specific filter was silently accepted: %#v", rc.RuntimeIssues)
	}
}

func TestSourceRuntimeAliasesDoNotOverrideConfiguredInvalidNativeSections(t *testing.T) {
	rc := ParseRuntimeCfg(`[Network Speed Simulation]
BandwidthKbps=invalid
[ModemSpeed]
EnableModemSpeed=1
ModemSpeed=128000
[Download Filters]
Hosts=https://native.example.test/private
[FILTERS]
IncludeFiltersInList=source.example.test
`)
	if rc.NetworkSimulationMode != "" || rc.NetworkBandwidthKbps != 0 {
		t.Fatalf("invalid native network setting fell back to [ModemSpeed]: %#v", rc)
	}
	if rc.DownloadFilterMode != "" || len(rc.DownloadFilterHosts) != 0 {
		t.Fatalf("invalid native filter fell back to [FILTERS]: %q/%#v", rc.DownloadFilterMode, rc.DownloadFilterHosts)
	}
	if !runtimeConfigHasIssue(rc, "download_filters") {
		t.Fatalf("invalid native filter lacks review issue: %#v", rc.RuntimeIssues)
	}

	dispositions := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{
		"default.cfg": ParseConfigSections(`[Network Speed Simulation]
BandwidthKbps=invalid
[ModemSpeed]
EnableModemSpeed=1
ModemSpeed=128000
[Download Filters]
Hosts=https://native.example.test/private
[FILTERS]
IncludeFiltersInList=source.example.test
`),
	}, "http")
	for section, key := range map[string]string{
		"ModemSpeed": "ModemSpeed",
		"FILTERS":    "IncludeFiltersInList",
	} {
		if got := dispositions["default.cfg"][section][key]; got.Status != RuntimeSettingDispositionNonApplicable ||
			!strings.Contains(got.Note, "Superseded") {
			t.Fatalf("[%s] %s disposition = %#v, want superseded", section, key, got)
		}
	}
}

func TestSourceDownloadFiltersFailClosedWhenBothRawListsArePopulated(t *testing.T) {
	rc := ParseRuntimeCfg(`[FILTERS]
IncludeFiltersInList=https://include.example.test/path
ExcludeFiltersInList=exclude.example.test
`)
	if rc.DownloadFilterMode != "" || len(rc.DownloadFilterHosts) != 0 {
		t.Fatalf("ambiguous raw lists applied a filter: %q/%#v", rc.DownloadFilterMode, rc.DownloadFilterHosts)
	}
	if !runtimeConfigHasIssue(rc, "download_filters") {
		t.Fatalf("ambiguous raw lists lack review issue: %#v", rc.RuntimeIssues)
	}
}

func TestRuntimeReviewEvidenceListsAreBounded(t *testing.T) {
	var headerRows strings.Builder
	headerRows.WriteString("[WEB]\nBrowserAcceptLanguage=None\n")
	var filters strings.Builder
	filters.WriteString("[FILTERS]\nIncludeFiltersInList=")
	for index := 1; index <= 45; index++ {
		fmt.Fprintf(&headerRows, "BrowserAcceptLanguage_extra_arg_%d=language-%d\n", index, index)
		if index > 1 {
			filters.WriteByte(';')
		}
		fmt.Fprintf(&filters, "*.unsupported-%d.example.test", index)
	}

	rc := ParseRuntimeCfg(headerRows.String() + filters.String())
	headerIssue, ok := runtimeConfigIssueByID(rc, "request_headers")
	if !ok || !strings.Contains(headerIssue.Notes, "+25 more") || strings.Contains(headerIssue.Notes, "language-45") {
		t.Fatalf("header spill evidence is not bounded: %#v", rc.RuntimeIssues)
	}
	filterIssue, ok := runtimeConfigIssueByID(rc, "download_filters")
	if !ok || !strings.Contains(filterIssue.Notes, "+25 more") || strings.Contains(filterIssue.Notes, "unsupported-45") {
		t.Fatalf("download-filter evidence is not bounded: %#v", rc.RuntimeIssues)
	}
}
