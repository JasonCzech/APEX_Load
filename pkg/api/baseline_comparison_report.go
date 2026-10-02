package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
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
	"join": strings.Join,
	"cell": func(row BaselineComparisonRow, id string) BaselineComparisonCell { return row.Baselines[id] },
}).Parse(`<h2>Baseline comparison</h2>
{{if .Report.BaselineComparison}}{{$c := .Report.BaselineComparison}}<p>Advisory comparison {{$c.ID}} · rule {{$c.RuleVersion}} · {{$c.CreatedAt}}</p>
{{with $c.Verdict}}<p><strong>Regression verdict: {{.Status}}</strong> · {{.Evaluated}} evaluated · {{.Unavailable}} unavailable</p>{{range .Evidence}}<p>{{.Metric}}: {{.Status}} · row {{.RowID}} · baseline {{.BaselineID}} · current {{observed .Current}} / baseline {{observed .Baseline}} · {{.Reason}}</p>{{end}}{{end}}
<p>Current run: {{$c.Current.Name}} · {{$c.Current.ReleaseName}}</p>
{{range $c.Warnings}}<p>{{.}}</p>{{end}}
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
<h4>Resources — time-weighted means</h4><table><thead><tr><th>Resource</th><th>Current</th><th>Baseline</th><th>Evidence</th></tr></thead><tbody>{{range $c.Resources}}{{$b := cell . $id}}<tr><td>{{.Name}} ({{.Source}}), {{.Unit}}</td><td>{{measurement .Current}}</td><td>{{measurement $b.Measurement}} · {{delta $b}}</td><td>{{join $b.Qualification "; "}}</td></tr>{{end}}</tbody></table>
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
			for _, f := range c.Findings {
				*lines = append(*lines, f.Status+": "+f.Message)
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
			for _, diff := range c.Setup {
				if err := write("baseline_comparison", diff.BaselineID, diff.Field, diff.Status+": "+diff.Current+" / "+diff.Baseline); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
