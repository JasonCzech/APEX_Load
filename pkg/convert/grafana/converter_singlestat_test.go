package grafana

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

// Legacy singlestat panels are ubiquitous in dashboards at the low end of the
// accepted schema range (schemaVersion >= MinSchemaVersion), which predates
// fieldConfig entirely.
func TestConvertMapsLegacySingleStatPanels(t *testing.T) {
	content := `{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"P95","format":"ms","valueName":"current","targets":[{"expr":"p95","refId":"A"}]},
		{"id":2,"type":"grafana-singlestat-panel","title":"Errors","format":"percent","valueName":"current","targets":[{"expr":"errs","refId":"A"}]}
	]}`
	res := mustConvert(t, content)
	if len(res.Dashboard.Panels) != 2 {
		t.Fatalf("panels = %d, want 2 (diagnostics: %v)", len(res.Dashboard.Panels), res.Unsupported.Strings())
	}
	for i, p := range res.Dashboard.Panels {
		if p.Kind != PanelKindSingleValue {
			t.Fatalf("panel %d kind = %q, want %q", i, p.Kind, PanelKindSingleValue)
		}
	}
	if res.Stats.SingleValueTiles != 2 {
		t.Fatalf("single-value tiles = %d, want 2 (stats %+v)", res.Stats.SingleValueTiles, res.Stats)
	}
	if len(res.Stats.UnsupportedPanelTypes) != 0 {
		t.Fatalf("unsupported panel types = %v, want none", res.Stats.UnsupportedPanelTypes)
	}
	// The legacy root-level "format" is the panel unit; without it the tile would
	// render unitless with no diagnostic.
	if res.Dashboard.Panels[0].Unit != "ms" {
		t.Fatalf("panel 1 unit = %q, want %q", res.Dashboard.Panels[0].Unit, "ms")
	}
	if res.Dashboard.Panels[1].Unit != "%" {
		t.Fatalf("panel 2 unit = %q, want %q", res.Dashboard.Panels[1].Unit, "%")
	}
	// The mapping itself is reported, not applied silently.
	if !hasDiagContaining(res, diag.Info, "legacy singlestat panel imported") {
		t.Fatalf("missing legacy-singlestat provenance diagnostic: %v", res.Unsupported.Strings())
	}
	// "current" is what APEX renders, so it must not raise a reduction warning.
	if hasDiagContaining(res, diag.Warning, "valueName") {
		t.Fatalf("unexpected valueName warning: %v", res.Unsupported.Strings())
	}
	if res.HasBlockers() {
		t.Fatalf("legacy singlestat import should not block: %v", res.Unsupported.Strings())
	}
}

func TestConvertReportsUnreproducedSingleStatReduction(t *testing.T) {
	res := mustConvert(t, `{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"Avg","format":"s","valueName":"avg","targets":[{"expr":"lat","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Kind != PanelKindSingleValue {
		t.Fatalf("panels = %#v", res.Dashboard.Panels)
	}
	if !hasDiagContaining(res, diag.Warning, `valueName "avg" is not reproduced`) {
		t.Fatalf("missing valueName reduction warning: %v", res.Unsupported.Strings())
	}
	if res.HasBlockers() {
		t.Fatalf("valueName should warn, not block: %v", res.Unsupported.Strings())
	}
}

// "last_time" is the timestamp of the last non-null point, not a reduction of the
// series values, so the tile shows a different quantity and must say so.
func TestConvertReportsSingleStatLastTimeValueName(t *testing.T) {
	res := mustConvert(t, `{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"Last seen","format":"dateTimeAsIso","valueName":"last_time","targets":[{"expr":"up","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Kind != PanelKindSingleValue {
		t.Fatalf("panels = %#v", res.Dashboard.Panels)
	}
	if !hasDiagContaining(res, diag.Warning, `valueName "last_time" renders the timestamp`) {
		t.Fatalf("missing last_time warning: %v", res.Unsupported.Strings())
	}
	if res.HasBlockers() {
		t.Fatalf("last_time should warn, not block: %v", res.Unsupported.Strings())
	}
}

// Grafana's singlestat defaults valueName to "avg", so an omitted valueName is an
// average and the tile's newest-sample value is a different number.
func TestConvertReportsOmittedSingleStatValueName(t *testing.T) {
	res := mustConvert(t, `{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"Latency","format":"ms","targets":[{"expr":"lat","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 {
		t.Fatalf("panels = %#v", res.Dashboard.Panels)
	}
	if !hasDiagContaining(res, diag.Warning, `omits valueName, which Grafana defaults to "avg"`) {
		t.Fatalf("missing omitted-valueName warning: %v", res.Unsupported.Strings())
	}
	if res.HasBlockers() {
		t.Fatalf("omitted valueName should warn, not block: %v", res.Unsupported.Strings())
	}
}

// Legacy singlestat panels used to be skipped as an unsupported type. Mapping them
// onto the single-value tile must not turn a malformed one into a dashboard-wide
// rejection that also discards the dashboard's unrelated panels.
func TestConvertExcludesMultiTargetLegacySingleStatWithoutBlocking(t *testing.T) {
	res := mustConvert(t, `{"title":"Mixed","schemaVersion":18,"panels":[
		{"id":1,"type":"timeseries","title":"OK","targets":[{"expr":"up","refId":"A"}]},
		{"id":2,"type":"grafana-singlestat-panel","title":"Two","format":"ms","valueName":"current","targets":[{"expr":"a","refId":"A"},{"expr":"b","refId":"B"}]}
	]}`)
	if res.HasBlockers() {
		t.Fatalf("multi-target legacy singlestat must not block the dashboard: %v", res.Unsupported.Strings())
	}
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Title != "OK" {
		t.Fatalf("panels = %#v, want only the timeseries panel", res.Dashboard.Panels)
	}
	if !hasDiagContaining(res, diag.Warning, "legacy singlestat panel has 2 visible Prometheus targets") {
		t.Fatalf("missing exclusion warning: %v", res.Unsupported.Strings())
	}
	// A modern stat panel keeps the pre-existing blocker.
	modern := mustConvert(t, `{"title":"Modern","schemaVersion":39,"panels":[
		{"id":1,"type":"stat","title":"Two","targets":[{"expr":"a","refId":"A"},{"expr":"b","refId":"B"}]}
	]}`)
	if !hasBlockerContaining(modern, "must contain exactly one visible Prometheus target") {
		t.Fatalf("stat panel lost its multi-target blocker: %v", modern.Unsupported.Strings())
	}
}

// The provenance diagnostic asserts a completed import, so it must not be reported
// for a legacy singlestat panel that is then excluded.
func TestConvertOmitsSingleStatProvenanceForExcludedPanels(t *testing.T) {
	cases := map[string]string{
		"multiple visible targets": `{"id":1,"type":"singlestat","title":"Two","valueName":"avg","targets":[{"expr":"a","refId":"A"},{"expr":"b","refId":"B"}]}`,
		"only a hidden target":     `{"id":1,"type":"singlestat","title":"Hidden","valueName":"avg","targets":[{"expr":"a","refId":"A","hide":true}]}`,
		"only a rawSql target":     `{"id":1,"type":"singlestat","title":"SQL","valueName":"avg","targets":[{"rawSql":"select 1","refId":"A"}]}`,
	}
	for name, panel := range cases {
		t.Run(name, func(t *testing.T) {
			res := mustConvert(t, `{"title":"Legacy","schemaVersion":18,"panels":[`+panel+`]}`)
			if len(res.Dashboard.Panels) != 0 {
				t.Fatalf("panels = %#v, want the panel excluded", res.Dashboard.Panels)
			}
			if hasDiagContaining(res, diag.Info, "legacy singlestat panel imported") {
				t.Fatalf("excluded panel was reported as imported: %v", res.Unsupported.Strings())
			}
			if hasDiagContaining(res, diag.Warning, "valueName") {
				t.Fatalf("excluded panel reported a tile rendering: %v", res.Unsupported.Strings())
			}
		})
	}
}

// fieldConfig wins when both are present, so a converted-forward dashboard that
// kept a stale root "format" is not misread.
func TestConvertPrefersFieldConfigUnitOverLegacyFormat(t *testing.T) {
	res := mustConvert(t, `{"title":"Legacy","schemaVersion":30,"panels":[
		{"id":1,"type":"singlestat","title":"P95","format":"ms","fieldConfig":{"defaults":{"unit":"s"}},"targets":[{"expr":"p95","refId":"A"}]}
	]}`)
	if got := res.Dashboard.Panels[0].Unit; got != "s" {
		t.Fatalf("unit = %q, want %q", got, "s")
	}
}

// Modern panel types must not pick up a root-level "format" field.
func TestConvertIgnoresRootFormatOnModernPanelTypes(t *testing.T) {
	res := mustConvert(t, `{"title":"Modern","schemaVersion":39,"panels":[
		{"id":1,"type":"stat","title":"P95","format":"ms","targets":[{"expr":"p95","refId":"A"}]}
	]}`)
	if got := res.Dashboard.Panels[0].Unit; got != "" {
		t.Fatalf("unit = %q, want empty", got)
	}
	if hasDiagContaining(res, diag.Info, "legacy singlestat panel imported") {
		t.Fatalf("modern stat panel should not be reported as legacy singlestat: %v", res.Unsupported.Strings())
	}
}

// Panel types that remain unmapped must still be reported, never dropped silently.
func TestConvertReportsStillUnsupportedPanelTypes(t *testing.T) {
	res := mustConvert(t, `{"title":"Mixed","schemaVersion":39,"panels":[
		{"id":1,"type":"timeseries","title":"OK","targets":[{"expr":"up","refId":"A"}]},
		{"id":2,"type":"piechart","title":"Pie","targets":[{"expr":"up","refId":"A"}]},
		{"id":3,"type":"nodeGraph","title":"Nodes","targets":[{"expr":"up","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 {
		t.Fatalf("panels = %d, want 1", len(res.Dashboard.Panels))
	}
	if got := res.Stats.UnsupportedPanelTypes; len(got) != 2 {
		t.Fatalf("unsupported panel types = %v, want 2 entries", got)
	}
	for _, panelType := range []string{"piechart", "nodeGraph"} {
		if !hasDiagContaining(res, diag.Warning, panelType) {
			t.Fatalf("panel type %q was dropped without a diagnostic: %v", panelType, res.Unsupported.Strings())
		}
	}
}

// The legacy fields must survive canonicalization, otherwise the unit is stripped
// before the converter ever sees it.
func TestCanonicalizeImportSourceRetainsLegacySingleStatFields(t *testing.T) {
	canonical, err := CanonicalizeImportSource(`{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"P95","format":"ms","valueName":"avg","targets":[{"expr":"p95","refId":"A"}]}
	]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(canonical, `"format":"ms"`) || !strings.Contains(canonical, `"valueName":"avg"`) {
		t.Fatalf("canonical source dropped legacy singlestat fields: %s", canonical)
	}
	res := mustConvert(t, canonical)
	if got := res.Dashboard.Panels[0].Unit; got != "ms" {
		t.Fatalf("unit after canonicalization = %q, want %q", got, "ms")
	}
}

// findBinding returns the variable binding with the given name, or nil.
func findBinding(res *ConvertResult, name string) *VariableBinding {
	for i := range res.Bindings {
		if res.Bindings[i].Name == name {
			return &res.Bindings[i]
		}
	}
	return nil
}

// A legacy singlestat keeps its thresholds in the root-level "thresholds"/"colors"
// fields rather than fieldConfig. They drive the tile severity and the run verdict, so
// they must be translated rather than dropped.
func TestConvertTranslatesLegacySingleStatThresholds(t *testing.T) {
	res := mustConvert(t, `{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"P95","format":"ms","valueName":"current",
		 "thresholds":"70,90",
		 "colors":["rgba(50,172,45,0.97)","rgba(237,129,40,0.89)","rgba(245,54,54,0.9)"],
		 "mappingType":1,"valueMaps":[{"value":"0","op":"=","text":"DOWN"}],
		 "targets":[{"expr":"p95","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 {
		t.Fatalf("panels = %#v", res.Dashboard.Panels)
	}
	steps := res.Dashboard.Panels[0].Thresholds
	if len(steps) != 3 {
		t.Fatalf("threshold steps = %#v, want 3 translated from the legacy config", steps)
	}
	if steps[0].Value != nil || steps[0].Severity != "normal" {
		t.Fatalf("base step = %#v, want the base color with normal severity", steps[0])
	}
	if steps[1].Value == nil || *steps[1].Value != 70 || steps[1].Severity != "warning" {
		t.Fatalf("warning step = %#v (value %v), want 70/warning", steps[1], steps[1].Value)
	}
	if steps[2].Value == nil || *steps[2].Value != 90 || steps[2].Severity != "critical" {
		t.Fatalf("critical step = %#v (value %v), want 90/critical", steps[2], steps[2].Value)
	}
	// The translation is reported, not applied invisibly.
	if !hasDiagContaining(res, diag.Info, "legacy singlestat thresholds and colors were translated") {
		t.Fatalf("missing legacy threshold translation diagnostic: %v", res.Unsupported.Strings())
	}
	// Legacy value maps get the same report as their fieldConfig equivalent.
	if !hasDiagContaining(res, diag.Info, "value mappings are not reproduced") {
		t.Fatalf("legacy valueMaps dropped with no diagnostic: %v", res.Unsupported.Strings())
	}
	if res.HasBlockers() {
		t.Fatalf("legacy thresholds should not block: %v", res.Unsupported.Strings())
	}
}

// An unreadable legacy threshold configuration must be reported; before the fix the
// whole construct vanished with the panel reported as a clean import.
func TestConvertReportsUnreadableLegacySingleStatThresholds(t *testing.T) {
	res := mustConvert(t, `{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"P95","valueName":"current","thresholds":"70,abc",
		 "colors":["green","orange","red"],"targets":[{"expr":"p95","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Thresholds != nil {
		t.Fatalf("panels = %#v, want one panel with no thresholds", res.Dashboard.Panels)
	}
	if !hasDiagContaining(res, diag.Warning, "legacy singlestat thresholds could not be read") {
		t.Fatalf("unreadable legacy thresholds dropped silently: %v", res.Unsupported.Strings())
	}
	if res.HasBlockers() {
		t.Fatalf("unreadable legacy thresholds should warn, not block: %v", res.Unsupported.Strings())
	}
}

// Grafana writes one more color than there are levels. A mismatched color array means
// the severities cannot be derived, but the levels themselves are still imported.
func TestConvertReportsLegacySingleStatThresholdColorMismatch(t *testing.T) {
	res := mustConvert(t, `{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"P95","valueName":"current","thresholds":"70,90",
		 "colors":["red"],"targets":[{"expr":"p95","refId":"A"}]}
	]}`)
	steps := res.Dashboard.Panels[0].Thresholds
	if len(steps) != 2 {
		t.Fatalf("threshold steps = %#v, want the 2 declared levels", steps)
	}
	for i, step := range steps {
		if step.Severity != "unknown" {
			t.Fatalf("step %d severity = %q, want unknown when colors cannot be paired", i, step.Severity)
		}
	}
	if !hasDiagContaining(res, diag.Warning, "their severities could not be derived") {
		t.Fatalf("missing color-mismatch diagnostic: %v", res.Unsupported.Strings())
	}
}

// Canonicalization re-marshals the typed dashboard, so the legacy threshold and value
// mapping keys must be decoded or they are erased before a re-conversion sees them.
func TestCanonicalizeImportSourceRetainsLegacySingleStatThresholds(t *testing.T) {
	canonical, err := CanonicalizeImportSource(`{"title":"Legacy","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"P95","valueName":"current","thresholds":"70,90",
		 "colors":["green","orange","red"],"valueMaps":[{"value":"0","op":"=","text":"DOWN"}],
		 "targets":[{"expr":"p95","refId":"A"}]}
	]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"thresholds":"70,90"`, `"colors":`, `"valueMaps":`} {
		if !strings.Contains(canonical, want) {
			t.Fatalf("canonical source dropped %s: %s", want, canonical)
		}
	}
	res := mustConvert(t, canonical)
	steps := res.Dashboard.Panels[0].Thresholds
	if len(steps) != 3 || steps[2].Severity != "critical" {
		t.Fatalf("thresholds after canonicalization = %#v", steps)
	}
}

// A panel that is excluded contributes no query to the import, so charging its targets
// to the dashboard-wide budget would reject an import over queries it never runs.
func TestConvertExcludedPanelTargetsDoNotConsumeQueryBudget(t *testing.T) {
	// 6 imported panels x 10 targets = exactly MaxQueriesTotal, plus one excluded
	// multi-target legacy singlestat.
	panels := []string{}
	for id := 1; id <= 6; id++ {
		targets := []string{}
		for ti := 0; ti < MaxTargetsPerPanel; ti++ {
			targets = append(targets, fmt.Sprintf(`{"expr":"metric_%d_%d","refId":"R%d"}`, id, ti, ti))
		}
		panels = append(panels, fmt.Sprintf(`{"id":%d,"type":"timeseries","title":"P%d","targets":[%s]}`,
			id, id, strings.Join(targets, ",")))
	}
	excluded := `{"id":99,"type":"singlestat","title":"Two","valueName":"current","targets":[{"expr":"a","refId":"A"},{"expr":"b","refId":"B"}]}`
	res := mustConvert(t, `{"title":"Budget","schemaVersion":18,"panels":[`+strings.Join(append(panels, excluded), ",")+`]}`)

	if res.Stats.Targets != MaxQueriesTotal {
		t.Fatalf("imported targets = %d, want %d", res.Stats.Targets, MaxQueriesTotal)
	}
	if res.HasBlockers() {
		t.Fatalf("excluded panel targets were charged to the query budget: %v", res.Unsupported.Strings())
	}

	// The limit still bites when the imported queries genuinely exceed it.
	over := mustConvert(t, `{"title":"Budget","schemaVersion":18,"panels":[`+
		strings.Join(append(panels, strings.Replace(panels[0], `"id":1,`, `"id":7,`, 1)), ",")+`]}`)
	if !hasBlockerContaining(over, "queries across its panels, exceeding the import limit") {
		t.Fatalf("query budget stopped being enforced: %v", over.Unsupported.Strings())
	}
}

// Variable provenance is part of the import contract: a binding must not be listed as
// used by — or made mandatory at commit by — a query that was excluded.
func TestConvertExcludedPanelDoesNotMakeVariableRequired(t *testing.T) {
	res := mustConvert(t, `{"title":"Provenance","schemaVersion":18,
		"templating":{"list":[{"name":"env","type":"custom","query":"prod,stage","current":{"value":"prod","text":"prod"}}]},
		"panels":[
			{"id":1,"type":"timeseries","title":"OK","targets":[{"expr":"up","refId":"A"}]},
			{"id":2,"type":"singlestat","title":"Two","valueName":"current","targets":[
				{"expr":"a{env=\"$env\"}","refId":"A"},{"expr":"b{env=\"$env\"}","refId":"B"}]}
		]}`)
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Title != "OK" {
		t.Fatalf("panels = %#v, want only the timeseries panel", res.Dashboard.Panels)
	}
	binding := findBinding(res, "env")
	if binding == nil {
		t.Fatalf("bindings = %#v, want a binding for env", res.Bindings)
	}
	if binding.Kind != BindingRequired {
		t.Fatalf("env binding kind = %q, want %q", binding.Kind, BindingRequired)
	}
	if len(binding.UsedBy) != 0 {
		t.Fatalf("env used_by = %v, want empty: only excluded panel queries reference it", binding.UsedBy)
	}
	if names := res.RequiredBindingNames(); len(names) != 0 {
		t.Fatalf("required bindings = %v, want none: no imported query references env", names)
	}
}

// warnDroppedPanelConstructs asserts a completed import ("one panel is imported",
// "... are rendered"), so it must not be reported for a panel that is then excluded.
func TestConvertOmitsDroppedConstructWarningsForExcludedPanels(t *testing.T) {
	assertedImport := []string{
		"one panel is imported without repetition",
		"value mappings are not reproduced",
		"transformations are not reproduced",
	}
	res := mustConvert(t, `{"title":"Mixed","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"Excluded","valueName":"current","repeat":"host",
		 "valueMaps":[{"value":"0","op":"=","text":"DOWN"}],"transformations":[{"id":"reduce"}],
		 "targets":[{"rawSql":"select 1","refId":"A"}]},
		{"id":2,"type":"timeseries","title":"OK","targets":[{"expr":"up","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Title != "OK" {
		t.Fatalf("panels = %#v, want only the timeseries panel", res.Dashboard.Panels)
	}
	if !hasDiagContaining(res, diag.Warning, "no importable Prometheus query and was excluded") {
		t.Fatalf("missing exclusion warning: %v", res.Unsupported.Strings())
	}
	for _, phrase := range assertedImport {
		if hasDiagContaining(res, diag.Warning, phrase) || hasDiagContaining(res, diag.Info, phrase) {
			t.Fatalf("excluded panel was reported as rendering %q: %v", phrase, res.Unsupported.Strings())
		}
	}

	// The same constructs on a panel that IS imported must still be reported.
	imported := mustConvert(t, `{"title":"Mixed","schemaVersion":18,"panels":[
		{"id":1,"type":"timeseries","title":"OK","repeat":"host",
		 "fieldConfig":{"defaults":{"mappings":[{"type":"value"}]}},"transformations":[{"id":"reduce"}],
		 "targets":[{"expr":"up","refId":"A"}]}
	]}`)
	for _, phrase := range assertedImport {
		if !hasDiagContaining(imported, diag.Warning, phrase) && !hasDiagContaining(imported, diag.Info, phrase) {
			t.Fatalf("imported panel lost the %q diagnostic: %v", phrase, imported.Unsupported.Strings())
		}
	}
}

// A legacy singlestat that is excluded must not report threshold diagnostics either:
// they describe what the rendered tile does with its threshold lines.
func TestConvertOmitsLegacyThresholdDiagnosticsForExcludedPanels(t *testing.T) {
	res := mustConvert(t, `{"title":"Mixed","schemaVersion":18,"panels":[
		{"id":1,"type":"singlestat","title":"Excluded","valueName":"current","thresholds":"70,abc",
		 "colors":["green","orange","red"],"targets":[{"expr":"a","refId":"A"},{"expr":"b","refId":"B"}]},
		{"id":2,"type":"timeseries","title":"OK","targets":[{"expr":"up","refId":"A"}]}
	]}`)
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Title != "OK" {
		t.Fatalf("panels = %#v, want only the timeseries panel", res.Dashboard.Panels)
	}
	if hasDiagContaining(res, diag.Warning, "legacy singlestat thresholds could not be read") {
		t.Fatalf("excluded panel reported tile threshold rendering: %v", res.Unsupported.Strings())
	}
}
