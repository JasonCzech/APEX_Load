package grafana

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func mustConvert(t *testing.T, content string) *ConvertResult {
	t.Helper()
	res, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if res == nil || res.Dashboard == nil {
		t.Fatalf("Convert returned nil dashboard")
	}
	return res
}

func hasBlockerContaining(res *ConvertResult, substr string) bool {
	for _, d := range res.Unsupported {
		if d.Severity == diag.Blocker && strings.Contains(d.Detail, substr) {
			return true
		}
	}
	return false
}

func hasDiagContaining(res *ConvertResult, sev diag.Severity, substr string) bool {
	for _, d := range res.Unsupported {
		if d.Severity == sev && strings.Contains(d.Detail, substr) {
			return true
		}
	}
	return false
}

func TestConvertRealFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "apexload.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res := mustConvert(t, string(data))

	if got, want := len(res.Dashboard.Panels), 5; got != want {
		t.Fatalf("panels = %d, want %d", got, want)
	}
	if res.HasBlockers() {
		t.Fatalf("real fixture should not produce blockers: %v", res.Unsupported.Strings())
	}
	if res.Stats.LineCharts != 4 || res.Stats.SingleValueTiles != 1 {
		t.Fatalf("stats line=%d tile=%d, want 4/1", res.Stats.LineCharts, res.Stats.SingleValueTiles)
	}
	// legendFormat "generator {{test_id}}" -> LabelFields includes test_id, and the
	// reserved auto-binding assumption is recorded.
	var sawTestIDLabel bool
	for _, p := range res.Dashboard.Panels {
		for _, tg := range p.Targets {
			for _, f := range tg.LabelFields {
				if f == "test_id" {
					sawTestIDLabel = true
				}
			}
		}
	}
	if !sawTestIDLabel {
		t.Fatalf("expected a target with a test_id legend label field")
	}
}

func TestConvertUnwrapsExportEnvelope(t *testing.T) {
	content := `{"meta":{"type":"db"},"dashboard":{"title":"Wrapped","schemaVersion":39,"panels":[
		{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"up","refId":"A"}]}
	]}}`
	res := mustConvert(t, content)
	if res.Dashboard.SourceTitle != "Wrapped" {
		t.Fatalf("title = %q, want Wrapped", res.Dashboard.SourceTitle)
	}
	if len(res.Dashboard.Panels) != 1 {
		t.Fatalf("panels = %d, want 1", len(res.Dashboard.Panels))
	}
}

func TestConvertLiftsCollapsedRowChildren(t *testing.T) {
	content := `{"title":"Rows","schemaVersion":39,"panels":[
		{"id":1,"type":"row","title":"Infra","panels":[
			{"id":2,"type":"timeseries","title":"CPU","targets":[{"expr":"cpu","refId":"A"}]},
			{"id":3,"type":"stat","title":"Up","targets":[{"expr":"up","refId":"A"}]}
		]}
	]}`
	res := mustConvert(t, content)
	if len(res.Dashboard.Panels) != 2 {
		t.Fatalf("panels = %d, want 2", len(res.Dashboard.Panels))
	}
	if res.Stats.RowsExpanded != 1 {
		t.Fatalf("rows expanded = %d, want 1", res.Stats.RowsExpanded)
	}
	for _, p := range res.Dashboard.Panels {
		if p.RowTitle != "Infra" {
			t.Fatalf("panel %q rowTitle = %q, want Infra", p.Title, p.RowTitle)
		}
	}
}

func TestConvertReportsUnsupportedPanelTypes(t *testing.T) {
	content := `{"title":"Mixed","schemaVersion":39,"panels":[
		{"id":1,"type":"timeseries","title":"OK","targets":[{"expr":"up","refId":"A"}]},
		{"id":2,"type":"table","title":"Grid","targets":[{"expr":"up","refId":"A"}]},
		{"id":3,"type":"heatmap","title":"Heat","targets":[{"expr":"up","refId":"A"}]}
	]}`
	res := mustConvert(t, content)
	if len(res.Dashboard.Panels) != 1 {
		t.Fatalf("panels = %d, want 1", len(res.Dashboard.Panels))
	}
	if !hasDiagContaining(res, diag.Warning, `panel type "table" is not supported`) {
		t.Fatalf("expected table unsupported warning; got %v", res.Unsupported.Strings())
	}
	if len(res.Stats.UnsupportedPanelTypes) != 2 {
		t.Fatalf("unsupported types = %v, want table+heatmap", res.Stats.UnsupportedPanelTypes)
	}
}

func TestConvertVariablePolicy(t *testing.T) {
	content := `{"title":"Vars","schemaVersion":39,
		"templating":{"list":[
			{"name":"env","type":"custom","query":"prod,stage","options":[{"value":"prod"},{"value":"stage"}]},
			{"name":"dc","type":"constant","current":{"value":"us-east"}},
			{"name":"win","type":"interval"},
			{"name":"ds","type":"datasource"},
			{"name":"all","type":"query","multi":true,"includeAll":true},
			{"name":"filters","type":"adhoc"}
		]},
		"panels":[
			{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"rate(http_requests_total{env=\"$env\",dc=\"$dc\"}[$win])","refId":"A"}]}
		]}`
	res := mustConvert(t, content)

	kinds := map[string]string{}
	for _, b := range res.Bindings {
		kinds[b.Name] = b.Kind
	}
	if kinds["env"] != BindingRequired {
		t.Fatalf("env kind = %q, want required", kinds["env"])
	}
	if kinds["dc"] != BindingAuto || kinds["win"] != BindingAuto || kinds["ds"] != BindingAuto {
		t.Fatalf("auto bindings wrong: %v", kinds)
	}
	if kinds["all"] != BindingRejected || kinds["filters"] != BindingRejected {
		t.Fatalf("rejected bindings wrong: %v", kinds)
	}
	// env is used -> required binding name surfaces; the panel imported fine.
	req := res.RequiredBindingNames()
	if len(req) != 1 || req[0] != "env" {
		t.Fatalf("required binding names = %v, want [env]", req)
	}
	if len(res.Dashboard.Panels) != 1 {
		t.Fatalf("expected the panel to import; got %d panels / %v", len(res.Dashboard.Panels), res.Unsupported.Strings())
	}
}

func TestConvertBlocksRejectedVariableWhenUsed(t *testing.T) {
	content := `{"title":"V","schemaVersion":39,
		"templating":{"list":[{"name":"pods","type":"query","multi":true}]},
		"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"up{pod=~\"$pods\"}","refId":"A"}]}]}`
	res := mustConvert(t, content)
	if !res.HasBlockers() {
		t.Fatalf("expected a blocker for a used multi variable")
	}
	if !hasBlockerContaining(res, "$pods") {
		t.Fatalf("expected blocker naming $pods; got %v", res.Unsupported.Strings())
	}
}

func TestConvertBlocksFormatModifier(t *testing.T) {
	content := `{"title":"V","schemaVersion":39,
		"templating":{"list":[{"name":"env","type":"custom"}]},
		"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"up{env=~\"${env:regex}\"}","refId":"A"}]}]}`
	res := mustConvert(t, content)
	if !hasBlockerContaining(res, "format modifier") {
		t.Fatalf("expected a format-modifier blocker; got %v", res.Unsupported.Strings())
	}
}

func TestConvertExcludesNonPrometheusDatasource(t *testing.T) {
	content := `{"title":"DS","schemaVersion":39,"panels":[
		{"id":1,"type":"timeseries","title":"Prom","datasource":{"type":"prometheus","uid":"abc"},"targets":[{"expr":"up","refId":"A"}]},
		{"id":2,"type":"timeseries","title":"Loki","datasource":{"type":"loki","uid":"xyz"},"targets":[{"expr":"{app=\"x\"}","refId":"A"}]}
	]}`
	res := mustConvert(t, content)
	if len(res.Dashboard.Panels) != 1 || res.Dashboard.Panels[0].Title != "Prom" {
		t.Fatalf("expected only the Prometheus panel; got %d", len(res.Dashboard.Panels))
	}
	if !hasDiagContaining(res, diag.Warning, `datasource type "loki" is not Prometheus`) {
		t.Fatalf("expected loki exclusion warning; got %v", res.Unsupported.Strings())
	}
	// The Prometheus panel's datasource uid is stored for the future proxy backend.
	if res.Dashboard.Panels[0].DatasourceUID != "abc" {
		t.Fatalf("datasource uid = %q, want abc", res.Dashboard.Panels[0].DatasourceUID)
	}
}

func TestConvertBlocksOldSchemaVersion(t *testing.T) {
	res := mustConvert(t, `{"title":"Old","schemaVersion":12,"panels":[]}`)
	if !hasBlockerContaining(res, "schema version 12") {
		t.Fatalf("expected schema-version blocker; got %v", res.Unsupported.Strings())
	}
}

func TestConvertBlocksOversizedExpr(t *testing.T) {
	big := strings.Repeat("a", MaxExprBytes+1)
	content := `{"title":"Big","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"` + big + `","refId":"A"}]}]}`
	res := mustConvert(t, content)
	if !hasBlockerContaining(res, "exceeds the limit") {
		t.Fatalf("expected oversized-expr blocker; got %v", res.Unsupported.Strings())
	}
}

func TestConvertBlocksTooManyPanels(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"title":"Many","schemaVersion":39,"panels":[`)
	for i := 0; i < MaxPanels+1; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":`)
		b.WriteString(itoa(i))
		b.WriteString(`,"type":"timeseries","title":"P","targets":[{"expr":"up","refId":"A"}]}`)
	}
	b.WriteString(`]}`)
	res := mustConvert(t, b.String())
	if !hasBlockerContaining(res, "exceeding the import limit") {
		t.Fatalf("expected panel-count blocker; got %v", res.Unsupported.Strings())
	}
}

func TestConvertBlocksMatchAllSelector(t *testing.T) {
	content := `{"title":"M","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"{__name__=~\".+\"}","refId":"A"}]}]}`
	res := mustConvert(t, content)
	if !hasBlockerContaining(res, "match-all selector") {
		t.Fatalf("expected match-all blocker; got %v", res.Unsupported.Strings())
	}
}

func TestConvertEmptyContentErrors(t *testing.T) {
	if _, err := Convert(ImportRequest{Content: "   "}); err == nil {
		t.Fatalf("expected error for empty content")
	}
}

func TestConvertProjectImportRequiresExactRunScope(t *testing.T) {
	unscoped, err := Convert(ImportRequest{ProjectID: "project-a", Content: `{"title":"Unsafe","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","targets":[{"expr":"up"}]}]}`})
	if err != nil {
		t.Fatal(err)
	}
	if !hasBlockerContaining(unscoped, "not run-scoped") {
		t.Fatalf("expected unscoped query blocker, got %v", unscoped.Unsupported.Strings())
	}
	scoped, err := Convert(ImportRequest{ProjectID: "project-a", Content: `{"title":"Safe","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","targets":[{"expr":"up{project_id=\"$project_id\"}"}]}]}`})
	if err != nil || scoped.HasBlockers() {
		t.Fatalf("scoped query should import: err=%v diagnostics=%v", err, scoped.Unsupported.Strings())
	}
}

func TestConvertRunScopeCannotBeSpoofedInsideAStringOrOtherLabel(t *testing.T) {
	for _, expr := range []string{
		`label_replace(up, "dst", "{project_id=\"$project_id\"}", "src", ".*")`,
		`up{note="project_id=\"$project_id\""}`,
		`up{project_id=~"$project_id"}`,
		`up{project_id="$test_id"}`,
		`up{project_id="$project_id"} + secret_metric`,
		`up{project_id="$project_id"} + other{tenant="elsewhere"}`,
		`up{project_id="$project_id"} + $extra`,
	} {
		res, err := Convert(ImportRequest{ProjectID: "project-a", Content: `{"title":"Spoof","schemaVersion":39,"templating":{"list":[{"name":"extra","type":"custom"}]},"panels":[{"id":1,"type":"timeseries","targets":[{"expr":` + fmt.Sprintf("%q", expr) + `}]}]}`})
		if err != nil {
			t.Fatalf("Convert(%q): %v", expr, err)
		}
		if !hasBlockerContaining(res, "not run-scoped") {
			t.Fatalf("spoofed scope %q was accepted: %v", expr, res.Unsupported.Strings())
		}
	}
	for _, expr := range []string{
		`sum by (instance) (rate(http_total{project_id="$project_id"}[$__interval]))`,
		`up{project_id="$project_id"} + other{test_id="$test_id"}`,
	} {
		res, err := Convert(ImportRequest{ProjectID: "project-a", Content: `{"title":"Scoped","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","targets":[{"expr":` + fmt.Sprintf("%q", expr) + `}]}]}`})
		if err != nil || res.HasBlockers() {
			t.Fatalf("fully scoped expression %q rejected: err=%v diagnostics=%v", expr, err, res.Unsupported.Strings())
		}
	}
}

func TestConvertBlocksMissingAndDuplicatePanelIDs(t *testing.T) {
	res := mustConvert(t, `{"title":"IDs","schemaVersion":39,"panels":[
		{"type":"timeseries","targets":[{"expr":"up"}]},
		{"id":2,"type":"timeseries","targets":[{"expr":"up"}]},
		{"id":2,"type":"stat","targets":[{"expr":"up"}]}
	]}`)
	if !hasBlockerContaining(res, "missing a positive Grafana id") || !hasBlockerContaining(res, "duplicates") {
		t.Fatalf("expected stable identity blockers, got %v", res.Unsupported.Strings())
	}
}

func TestConvertRejectsNullAndEmptyDashboard(t *testing.T) {
	if _, err := Convert(ImportRequest{Content: `null`}); err == nil {
		t.Fatal("expected null dashboard to fail parsing")
	}
	res := mustConvert(t, `{}`)
	if !res.HasBlockers() {
		t.Fatalf("empty dashboard must block commit: %v", res.Unsupported.Strings())
	}
	encoded, err := json.Marshal(res.Dashboard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"panels":[]`) {
		t.Fatalf("blocked dashboard panels must encode as [], got %s", encoded)
	}
}

func TestCanonicalizeImportSourceDropsUnknownExportFields(t *testing.T) {
	canonical, err := CanonicalizeImportSource(`{"meta":{"token":"secret"},"dashboard":{"title":"Safe","schemaVersion":39,"pluginSecret":"drop-me","panels":[{"id":1,"type":"timeseries","unknown":"drop-me","targets":[{"expr":"up","refId":"A"}]}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(canonical, "secret") || strings.Contains(canonical, "drop-me") {
		t.Fatalf("canonical source retained unknown fields: %s", canonical)
	}
	res := mustConvert(t, canonical)
	if res.Dashboard.SourceTitle != "Safe" || len(res.Dashboard.Panels) != 1 {
		t.Fatalf("canonical source changed import semantics: %#v", res.Dashboard)
	}
}

func TestConvertBlocksOversizedDurableMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "dashboard title", content: fmt.Sprintf(`{"title":%q,"schemaVersion":39,"panels":[{"id":1,"type":"timeseries","targets":[{"expr":"up"}]}]}`, strings.Repeat("d", MaxTitleBytes+1)), want: "dashboard title exceeds"},
		{name: "panel title", content: fmt.Sprintf(`{"title":"D","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","title":%q,"targets":[{"expr":"up"}]}]}`, strings.Repeat("p", MaxTitleBytes+1)), want: "panel title exceeds"},
		{name: "legend", content: fmt.Sprintf(`{"title":"D","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","targets":[{"expr":"up","legendFormat":%q}]}]}`, strings.Repeat("l", MaxLegendBytes+1)), want: "legend format exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := mustConvert(t, tc.content)
			if !hasBlockerContaining(result, tc.want) {
				t.Fatalf("diagnostics = %#v, want blocker containing %q", result.Unsupported, tc.want)
			}
		})
	}
}

func TestConvertBlocksMultiTargetSingleValuePanel(t *testing.T) {
	result := mustConvert(t, `{"title":"D","schemaVersion":39,"panels":[{"id":1,"type":"stat","targets":[{"refId":"A","expr":"up"},{"refId":"B","expr":"process_start_time_seconds"}]}]}`)
	if !hasBlockerContaining(result, "exactly one visible Prometheus target") {
		t.Fatalf("diagnostics = %#v, want deterministic single-value blocker", result.Unsupported)
	}
}

func TestConvertBoundsNestedCollectionDiagnostics(t *testing.T) {
	var panels strings.Builder
	for i := 1; i <= MaxPanels*20; i++ {
		if i > 1 {
			panels.WriteByte(',')
		}
		fmt.Fprintf(&panels, `{"id":%d,"type":"row","title":"row"}`, i)
	}
	result := mustConvert(t, `{"title":"Large","schemaVersion":39,"panels":[`+panels.String()+`]}`)
	if !hasBlockerContaining(result, "panel/row entries") {
		t.Fatalf("diagnostics = %#v, want panel processing-limit blocker", result.Unsupported)
	}
	if len(result.Unsupported) > MaxPanels+3 {
		t.Fatalf("diagnostics amplified to %d entries, want a bounded review", len(result.Unsupported))
	}

	var targets strings.Builder
	for i := 0; i < MaxTargetsPerPanel*100; i++ {
		if i > 0 {
			targets.WriteByte(',')
		}
		targets.WriteString(`{"hide":true,"expr":"up"}`)
	}
	result = mustConvert(t, `{"title":"Targets","schemaVersion":39,"panels":[{"id":1,"type":"timeseries","targets":[`+targets.String()+`]}]}`)
	if !hasBlockerContaining(result, "target entries") {
		t.Fatalf("diagnostics = %#v, want target processing-limit blocker", result.Unsupported)
	}
	if len(result.Unsupported) > MaxTargetsPerPanel+3 {
		t.Fatalf("target diagnostics amplified to %d entries", len(result.Unsupported))
	}
}

func TestConvertBoundsVariableValues(t *testing.T) {
	content := fmt.Sprintf(`{"title":"Variables","schemaVersion":39,"templating":{"list":[{"name":"env","type":"constant","current":{"value":%q}}]},"panels":[{"id":1,"type":"timeseries","targets":[{"expr":"up{env=\"$env\"}"}]}]}`, strings.Repeat("x", MaxVariableValueBytes+1))
	result := mustConvert(t, content)
	if !hasBlockerContaining(result, "cannot be imported") {
		t.Fatalf("diagnostics = %#v, want oversized variable-value blocker", result.Unsupported)
	}
	if len(result.Bindings) != 1 || result.Bindings[0].Default != "" || result.Bindings[0].Kind != BindingRejected {
		t.Fatalf("oversized variable value survived into review: %#v", result.Bindings)
	}

	unused := mustConvert(t, fmt.Sprintf(`{"title":"Variables","schemaVersion":39,"templating":{"list":[{"name":"env","type":"constant","current":{"value":%q}}]},"panels":[{"id":1,"type":"timeseries","targets":[{"expr":"up"}]}]}`, strings.Repeat("x", MaxVariableValueBytes+1)))
	if unused.HasBlockers() {
		t.Fatalf("unused oversized variable should not block an otherwise safe dashboard: %#v", unused.Unsupported)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
