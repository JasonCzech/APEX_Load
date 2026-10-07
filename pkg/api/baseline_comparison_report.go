package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

var baselineComparisonTemplate = template.Must(template.New("baseline-comparison").Funcs(template.FuncMap{
	"observed": func(v *float64) string {
		if v == nil {
			return "Unavailable"
		}
		return fmt.Sprintf("%.3g", *v)
	},
	"measurement": func(m *BaselineMeasurement) string {
		if m == nil {
			return "Unavailable"
		}
		if m.MeanStatistic == "evaluation_grid_mean" {
			return fmt.Sprintf("evaluation-grid mean %.3g · range %.3g–%.3g · n=%d · grid coverage %.1f%% · %d instances", m.Mean, m.Min, m.Max, m.Samples, m.CoveragePct, m.Instances)
		}
		return fmt.Sprintf("mean %.3g · P95 %.3g · range %.3g–%.3g · n=%d · coverage %.1f%% · %d instances", m.Mean, m.P95, m.Min, m.Max, m.Samples, m.CoveragePct, m.Instances)
	},
	"delta": func(c BaselineComparisonCell) string {
		if c.Delta == nil {
			return "Change unavailable"
		}
		if c.DeltaPct == nil {
			return fmt.Sprintf("Change %+.3g; percentage unavailable", *c.Delta)
		}
		return fmt.Sprintf("Change %+.3g (%+.1f%%)", *c.Delta, *c.DeltaPct)
	},
	"transaction": func(m *BaselineMeasurement) string {
		if m == nil {
			return "Unavailable"
		}
		if m.Successes == 0 {
			return fmt.Sprintf("n=%d; no successful latency samples; errors %.2f%%; %.3g/s", m.Samples, m.ErrorPct, m.Throughput)
		}
		return fmt.Sprintf("mean %.3g · P50 %.3g · P90 %.3g · P95 %.3g · P99 %.3g · min/max %.3g/%.3g · n=%d (%d successful) · %.3g/s (%.3g successful/s) · errors %.2f%%", m.Mean, m.P50, m.P90, m.P95, m.P99, m.Min, m.Max, m.Samples, m.Successes, m.Throughput, m.SuccessfulThroughput, m.ErrorPct)
	},
	"join":      strings.Join,
	"cell":      func(row BaselineComparisonRow, id string) BaselineComparisonCell { return row.Baselines[id] },
	"documents": baselineBrowserDocumentRows,
	"tuple":     baselineBrowserDocumentTuple,
	"documentChange": func(row protocol.BrowserDocumentComparison) string {
		if !row.Comparable || row.DeltaP75 == nil {
			return "Not compared: " + row.Reason
		}
		return fmt.Sprintf("p75 change %+.3g %s", *row.DeltaP75, row.Unit)
	},
}).Parse(`<h2>Baseline comparison</h2>
{{if .Report.BaselineComparison}}{{$c := .Report.BaselineComparison}}<p>Advisory comparison {{$c.ID}} · rule {{$c.RuleVersion}} · {{$c.CreatedAt}}</p>
{{with $c.Verdict}}<p><strong>Regression verdict: {{.Status}}</strong> · {{.Evaluated}} evaluated · {{.Unavailable}} unavailable</p>{{range .Evidence}}<p>{{.Metric}}: {{.Status}} · row {{.RowID}} · baseline {{.BaselineID}} · current {{observed .Current}} / baseline {{observed .Baseline}} · {{.Reason}}</p>{{end}}{{end}}
<p>Current run: {{$c.Current.Name}} · {{$c.Current.ReleaseName}}</p>
{{range $c.Warnings}}<p>{{.}}</p>{{end}}
<h3>Whole-run telemetry health</h3>{{range $c.Telemetry}}<p>Run {{.RunID}} · retained intervals {{.RetainedIntervals}} · classified {{.HealthIntervals}} · coverage {{observed .HealthCoveragePct}}% · breached classified intervals {{observed .BreachedIntervalPct}}% · peak observed drop counter {{with .DroppedResults}}{{.}}{{else}}Unavailable{{end}} · maximum sample age {{observed .MaxSampleAgeMs}} ms</p>{{range .Warnings}}<p>{{.}}</p>{{end}}{{end}}
{{range $c.Findings}}<p><strong>{{.Status}} · {{.Category}}</strong> · baseline {{.BaselineID}}: {{.Message}}</p>{{else}}<p>No configured change threshold exceeded.</p>{{end}}
{{if ne .Profile.ID "executive"}}
<h3>Effective comparison policy</h3>
<p>Policy revision {{$c.Policy.Revision}} · load tolerance ±{{$c.Policy.LoadTolerancePct}}%.</p>
<p>Latency: {{$c.Policy.LatencyPct}}% and {{$c.Policy.LatencyMs}} ms. Throughput: {{$c.Policy.ThroughputPct}}%. Error rate: {{$c.Policy.ErrorPoints}} percentage points. Resources: {{$c.Policy.ResourcePct}}% in either direction.</p>
<p>Minimum evidence: 100 successful samples for mean/P95; 1,000 for P99. Resources: 10 samples and 80% window coverage. Baseline zero: percentage unavailable. Stable windows exclude pauses and the first 30 seconds, with at least 60 seconds remaining.</p>
{{range $key, $value := $c.Policy.GroupMappings}}<p>Group mapping: {{$key}} → {{$value}}</p>{{end}}
{{range $c.Policy.ResourceMappings}}<p>Resource mapping: {{.Identity}} · run {{.RunID}} · {{.Kind}} / {{.Unit}} · {{.Aggregation}} over {{len .Keys}} sources · absolute threshold {{.AbsoluteThreshold}}.</p>{{end}}
{{range $c.Baselines}}{{$id := .ID}}<div style="break-before:page"><h3 class="baseline-reference">Reference: {{.Name}} · {{.ReleaseName}}</h3><p>{{.ID}} · {{.Status}}</p>
<h4>Transactions — successful latency in milliseconds</h4><table><thead><tr><th>Transaction / group / load window</th><th>Current</th><th>Baseline</th><th>Evidence</th></tr></thead><tbody>
{{range $c.Transactions}}{{$b := cell . $id}}<tr><td>{{.Name}} / {{.Group}}{{with .Window}} / {{printf "%.0f" .Load}} VUsers, {{printf "%.0f–%.0f s" .StartSeconds .EndSeconds}}{{end}}</td><td>{{transaction .Current}}</td><td>{{transaction $b.Measurement}} · {{delta $b}}</td><td>{{join $b.Qualification "; "}}</td></tr>{{end}}</tbody></table>
<h4>Resources — recorded means</h4><p>Prometheus means describe settled evaluations on the saved grid. Missing cells reduce coverage; evaluation levels do not reconstruct rates, totals or request percentiles.</p><table><thead><tr><th>Resource</th><th>Current</th><th>Baseline</th><th>Evidence</th></tr></thead><tbody>{{range $c.Resources}}{{$b := cell . $id}}<tr><td>{{.Name}} ({{.Source}}), {{.Unit}}{{with .Provider}} · query {{.QueryID}} · {{.BackendProfile}} · {{.Statistic}} · {{.EvaluationStepSeconds}}s grid · {{.Dimensions}}{{end}}</td><td>{{measurement .Current}}</td><td>{{measurement $b.Measurement}} · {{delta $b}}</td><td>{{join $b.Qualification "; "}}</td></tr>{{end}}</tbody></table>
{{with documents $c $id}}<h4>Browser documents — p75 of qualified final document measurements</h4><p>Compared only when algorithm, unit, lifecycle phase, frame scope, engine, device profile and runtime cohort all match; otherwise the first difference is named. Soft navigations: {{$c.BrowserSoftNavigation}} (not measured as navigations). Legacy action summaries are not compared here.</p><table><thead><tr><th>Metric tuple</th><th>Current p75</th><th>Baseline p75</th><th>Comparison</th></tr></thead><tbody>{{range .}}<tr><td>{{tuple .}}</td><td>{{observed .CurrentP75}} · n={{.CurrentFinal}}</td><td>{{observed .BaselineP75}} · n={{.BaselineFinal}}</td><td>{{documentChange .}}</td></tr>{{end}}</tbody></table>{{end}}
<h4>Setup differences</h4>{{range $c.Setup}}{{if eq .BaselineID $id}}<p><strong>{{.Field}}: {{.Status}}</strong> · current {{.Current}} · baseline {{.Baseline}}</p>{{end}}{{end}}</div>{{end}}
{{end}}{{else}}<p>No saved baseline comparison selected.</p>{{end}}`))

func baselineComparisonReportRenderer() reportSectionRenderer {
	return reportSectionRenderer{
		ID: "baseline_comparison", Label: "Baseline Comparison",
		HTML: func(d *runArchiveHTMLReportData) (template.HTML, error) {
			var b bytes.Buffer
			err := baselineComparisonTemplate.Execute(&b, d)
			return template.HTML(b.String()), err
		},
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			*lines = append(*lines, "Baseline comparison (advisory)")
			if d.Report.BaselineComparison == nil {
				*lines = append(*lines, "No saved baseline comparison selected.")
				return
			}
			c := d.Report.BaselineComparison
			*lines = append(*lines, "Snapshot "+c.ID+" / "+c.RuleVersion)
			if c.Verdict != nil {
				*lines = append(*lines, "Regression verdict: "+c.Verdict.Status)
			}
			for _, telemetry := range c.Telemetry {
				*lines = append(*lines, baselineTelemetryLines(telemetry)...)
			}
			for _, f := range c.Findings {
				*lines = append(*lines, f.Status+": "+f.Message)
			}
			if len(c.BrowserDocuments) > 0 {
				*lines = append(*lines, "Browser documents (p75 of qualified final document measurements; soft navigations "+c.BrowserSoftNavigation+")")
				for _, row := range c.BrowserDocuments {
					change := "not compared: " + row.Reason
					if row.Comparable && row.DeltaP75 != nil {
						change = fmt.Sprintf("p75 change %+.3g %s", *row.DeltaP75, row.Unit)
					}
					*lines = append(*lines, "Baseline "+row.BaselineID+" · "+baselineBrowserDocumentTuple(row)+" · "+change)
				}
			}
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			c := d.Report.BaselineComparison
			if c == nil {
				return write("baseline_comparison", "", "status", "No saved baseline comparison selected")
			}
			if c.Verdict != nil {
				verdict, _ := json.Marshal(c.Verdict)
				if err := write("baseline_comparison", c.ID, "regression_verdict", string(verdict)); err != nil {
					return err
				}
			}
			for _, telemetry := range c.Telemetry {
				payload, err := json.Marshal(telemetry)
				if err != nil {
					return err
				}
				if err := write("baseline_telemetry", telemetry.RunID, "whole_run_evidence", string(payload)); err != nil {
					return err
				}
			}
			payload, _ := json.Marshal(c.Policy)
			if err := write("baseline_comparison", c.ID, "effective_policy", string(payload)); err != nil {
				return err
			}
			for _, f := range c.Findings {
				if err := write("baseline_comparison", f.BaselineID+"/"+f.RowID, f.Metric, f.Status+": "+f.Message); err != nil {
					return err
				}
			}
			for _, rows := range [][]BaselineComparisonRow{c.Transactions, c.Resources} {
				for _, row := range rows {
					payload, _ := json.Marshal(row)
					if err := write("baseline_comparison", row.ID, "evidence", string(payload)); err != nil {
						return err
					}
				}
			}
			for _, row := range c.BrowserDocuments {
				payload, _ := json.Marshal(row)
				if err := write("baseline_browser_document", row.BaselineID+"/"+baselineBrowserDocumentTuple(row), "comparison", string(payload)); err != nil {
					return err
				}
			}
			for _, diff := range c.Setup {
				if err := write("baseline_comparison", diff.BaselineID, diff.Field, diff.Status+": "+diff.Current+" / "+diff.Baseline); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// baselineBrowserDocumentRows are the document comparisons with one baseline.
func baselineBrowserDocumentRows(c *BaselineComparisonSnapshot, baselineID string) []protocol.BrowserDocumentComparison {
	var rows []protocol.BrowserDocumentComparison
	for _, row := range c.BrowserDocuments {
		if row.BaselineID == baselineID {
			rows = append(rows, row)
		}
	}
	return rows
}

// baselineBrowserDocumentTuple names a compared document metric tuple.
func baselineBrowserDocumentTuple(row protocol.BrowserDocumentComparison) string {
	parts := []string{strings.ToUpper(row.Metric) + " (" + row.Unit + ")", row.Engine, "profile " + row.DeviceProfile, row.FrameScope, row.LifecyclePhase}
	if row.Group != "" {
		parts = append(parts, "group "+row.Group)
	}
	if row.Region != "" {
		parts = append(parts, "region "+row.Region)
	}
	return strings.Join(parts, " · ")
}
