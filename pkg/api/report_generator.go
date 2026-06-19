package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"sync"
	"time"
)

type runArchiveHTMLActionRow struct {
	Name  string
	Stats RunArchiveActionStats
}

type runArchiveHTMLCountRow struct {
	Name  string
	Count int64
}

type runArchiveHTMLSOAPFaultBucketRow struct {
	Timestamp  time.Time
	Total      int64
	Codes      string
	Categories string
}

type runArchiveHTMLSOAPPayloadBucketRow struct {
	Timestamp             time.Time
	Samples               int64
	AvgRequestPayloadKB   float64
	AvgResponsePayloadKB  float64
	AvgEnvelopeOverheadKB float64
	MaxEnvelopeOverheadKB float64
}

type runArchiveHTMLReportData struct {
	GeneratedAt           time.Time
	Report                *RunArchiveReport
	Profile               RunArchiveReportProfile
	Annotations           *ReportAnnotations
	CommentarySections    []reportCommentarySection
	Charts                reportCharts
	LogoDataURI           template.URL
	Manifest              *TestRunManifest
	Verdict               reportVerdict
	Findings              []ReportFinding
	SLARows               []slaComplianceRow
	GeneratorCount        int
	InventoryRows         []GeneratorInventoryEntry
	InventoryOverflow     int
	ActionRows            []runArchiveHTMLActionRow
	SOAPOperationRows     []runArchiveHTMLActionRow
	SOAPFaultCodeRows     []runArchiveHTMLCountRow
	SOAPFaultCategoryRows []runArchiveHTMLCountRow
	SOAPFaultBuckets      []runArchiveHTMLSOAPFaultBucketRow
	SOAPPayloadBuckets    []runArchiveHTMLSOAPPayloadBucketRow
	BrowserEngineRows     []*BrowserEngineLiveMetrics
	BrowserPlatformRows   []*BrowserPlatformLiveMetrics
	BrowserDeviceRows     []*BrowserDeviceLiveMetrics
}

var runArchiveReportTemplate = template.Must(template.New("run-archive-report").Funcs(template.FuncMap{
	"formatTime": func(value time.Time) string {
		if value.IsZero() {
			return "n/a"
		}
		return value.UTC().Format(time.RFC3339)
	},
	"formatFloat": func(value float64) string {
		return fmt.Sprintf("%.1f", value)
	},
	"formatPercent": func(value float64) string {
		return fmt.Sprintf("%.2f%%", value)
	},
	"formatScore": func(value float64) string {
		return fmt.Sprintf("%.3f", value)
	},
	"f0": func(value float64) string {
		return fmt.Sprintf("%.0f", value)
	},
	"hint": func(value int64) string {
		return formatPDFInt(value)
	},
	"sevPill": func(severity string) string {
		if strings.EqualFold(severity, "major") {
			return "warn"
		}
		return "info"
	},
	"sevLabel": func(severity string) string {
		switch strings.ToLower(strings.TrimSpace(severity)) {
		case "major":
			return "Major"
		case "minor":
			return "Minor"
		default:
			return "Info"
		}
	},
	"join": func(values []string) string {
		return strings.Join(values, ", ")
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>APEX Load Report — {{.Report.Manifest.TestName}}</title>
  <style>
    :root {
      color-scheme: light;
      --navy:   #0b2a4a;
      --blue:   #1f6feb;
      --blue2:  #0b5fc7;
      --sky:    #e7f1fd;
      --silver: #f3f6f9;
      --line:   #d4dbe3;
      --ink:    #1a2330;
      --slate:  #4a5568;
      --good:    #1a6e3c;
      --good-bg: #e6f4ed;
      --warn:    #8a6100;
      --warn-bg: #fdf3da;
      --bad:     #b42318;
      --bad-bg:  #fbe9e7;
      --info:    var(--blue2);
      --info-bg: var(--sky);
    }
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
    body {
      font-family: "Avenir Next", "Segoe UI", -apple-system, sans-serif;
      font-size: 14.5px;
      line-height: 1.58;
      color: var(--ink);
      background: #dde3e9;
    }
    .mono { font-family: "IBM Plex Mono", "SF Mono", Menlo, monospace; }
    .page { max-width: 960px; margin: 28px auto; background: #fff; box-shadow: 0 1px 32px rgba(11,42,74,.14); }
    .masthead { background: var(--navy); padding: 22px 52px 20px; display: flex; align-items: center; justify-content: space-between; gap: 24px; }
    .masthead-brand { display: flex; align-items: center; gap: 14px; }
    .brand-logo { height: 34px; width: auto; filter: brightness(0) invert(1); }
    .brand-unit { font-size: 12.5px; font-weight: 600; color: #8fc1ff; letter-spacing: .06em; text-transform: uppercase; line-height: 1.3; }
    .brand-sub { font-size: 11px; color: rgba(255,255,255,.6); letter-spacing: .03em; margin-top: 2px; }
    .doc-badge { text-align: right; font-size: 11px; line-height: 1.7; color: rgba(255,255,255,.55); }
    .doc-badge .mono { color: rgba(255,255,255,.88); }
    .header-rule { height: 4px; background: linear-gradient(90deg, var(--blue) 0%, var(--blue2) 100%); }
    .content { padding: 40px 52px 60px; }
    .eyebrow { font-size: 10.5px; font-weight: 700; letter-spacing: .18em; text-transform: uppercase; color: var(--blue2); margin-bottom: 8px; }
    h1 { font-size: 30px; font-weight: 700; letter-spacing: -.02em; line-height: 1.18; color: var(--navy); margin-bottom: 10px; }
    .subtitle { font-size: 14.5px; color: var(--slate); max-width: 72ch; line-height: 1.55; margin-bottom: 28px; }
    .meta-strip { display: grid; grid-template-columns: repeat(4, 1fr); border: 1px solid var(--line); overflow: hidden; margin-bottom: 24px; }
    .meta-cell { background: var(--silver); border-right: 1px solid var(--line); padding: 11px 14px; }
    .meta-cell:last-child { border-right: none; }
    .meta-cell b { display: block; font-size: 9.5px; font-weight: 700; letter-spacing: .14em; text-transform: uppercase; color: var(--slate); margin-bottom: 4px; }
    .meta-cell span { font-size: 13px; font-weight: 600; }
    .verdict { display: flex; align-items: stretch; margin-bottom: 32px; border: 1px solid var(--line); }
    .verdict-badge { flex: 0 0 auto; padding: 16px 22px; font-weight: 700; font-size: 12px; letter-spacing: .08em; text-transform: uppercase; color: #fff; background: var(--good); text-align: center; min-width: 130px; display: flex; align-items: center; justify-content: center; }
    .verdict-badge.conditional { background: var(--warn); }
    .verdict-badge.fail { background: var(--bad); }
    .verdict-body { padding: 14px 20px; font-size: 13.5px; background: var(--good-bg); flex: 1; border-left: 3px solid var(--good); line-height: 1.5; }
    .verdict-body.conditional { background: var(--warn-bg); border-left-color: var(--warn); }
    .verdict-body.fail { background: var(--bad-bg); border-left-color: var(--bad); }
    h2 { font-size: 16.5px; font-weight: 700; color: var(--navy); margin: 44px 0 10px; padding-bottom: 8px; border-bottom: 2px solid var(--navy); display: flex; align-items: baseline; gap: 10px; }
    h2 .sec-no { font-family: "IBM Plex Mono", monospace; font-size: 11px; font-weight: 600; color: var(--blue); background: var(--sky); padding: 2px 7px; border-radius: 3px; }
    h2.appendix-h { border-bottom-color: var(--blue2); }
    h3 { font-size: 13.5px; font-weight: 700; color: var(--navy); margin: 22px 0 8px; }
    .toc { display: grid; grid-template-columns: repeat(2, 1fr); gap: 6px 24px; margin-bottom: 8px; }
    .toc a { color: var(--blue2); text-decoration: none; font-size: 13px; padding: 3px 0; border-bottom: 1px dotted var(--line); display: flex; gap: 10px; }
    .toc a .mono { color: var(--slate); }
    .kpis { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 18px; }
    .kpi { border: 1px solid var(--line); border-top: 3px solid var(--blue); padding: 12px 14px; background: var(--silver); }
    .kpi.good { border-top-color: var(--good); }
    .kpi.warn { border-top-color: var(--warn); }
    .kpi b { display: block; font-size: 9.5px; font-weight: 700; letter-spacing: .12em; text-transform: uppercase; color: var(--slate); margin-bottom: 6px; }
    .kpi .v { font-size: 26px; font-weight: 700; color: var(--navy); line-height: 1.05; }
    .kpi .v small { font-size: 13px; font-weight: 600; color: var(--slate); }
    .kpi .d { font-size: 11px; color: var(--slate); margin-top: 4px; }
    table { width: 100%; border-collapse: collapse; border: 1px solid var(--line); margin: 6px 0 14px; font-size: 13px; }
    caption { caption-side: top; text-align: left; font-size: 11px; color: var(--slate); padding: 0 0 6px; font-style: italic; }
    th, td { padding: 9px 12px; border-bottom: 1px solid var(--line); text-align: left; vertical-align: top; }
    th { background: var(--silver); font-size: 10.5px; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; color: var(--slate); }
    td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
    tr:last-child td { border-bottom: 0; }
    .pill { display: inline-block; padding: 2px 9px; border-radius: 999px; font-size: 11px; font-weight: 600; }
    .pill.pass, .pill.good { background: var(--good-bg); color: var(--good); }
    .pill.warn { background: var(--warn-bg); color: var(--warn); }
    .pill.fail, .pill.bad { background: var(--bad-bg); color: var(--bad); }
    .pill.info, .pill.na { background: var(--info-bg); color: var(--info); }
    .analyst { margin: 6px 0 14px; }
    .analyst p { margin: 0 0 8px; white-space: pre-wrap; }
    .muted, .section-copy { color: var(--slate); }
    .section-copy { margin: 0 0 12px; font-size: 13px; }
    .finding { border: 1px solid var(--line); margin-bottom: 12px; }
    .finding-head { display: flex; align-items: center; gap: 10px; padding: 10px 14px; background: var(--silver); border-bottom: 1px solid var(--line); }
    .finding-head.major { background: var(--warn-bg); }
    .finding-head .fid { font-family: "IBM Plex Mono", monospace; font-weight: 700; color: var(--blue2); }
    .finding-head .ft { flex: 1; font-weight: 600; color: var(--navy); }
    .finding-body { padding: 4px 14px 12px; }
    .finding-body dl { display: grid; grid-template-columns: 110px 1fr; gap: 4px 14px; margin-top: 8px; }
    .finding-body dt { font-size: 10.5px; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; color: var(--slate); padding-top: 2px; }
    .finding-body dd { font-size: 13px; }
    .signoff { display: grid; grid-template-columns: repeat(2, 1fr); gap: 18px 28px; margin-top: 14px; }
    .sign-cell b { display: block; font-size: 10.5px; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; color: var(--slate); margin-bottom: 26px; }
    .sign-line { border-bottom: 1px solid var(--ink); }
    .sign-meta { font-size: 11px; color: var(--slate); margin-top: 4px; }
    .overview { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 12px; margin: 8px 0 14px; }
    .metric { border: 1px solid var(--line); padding: 12px 14px; background: var(--silver); }
    .metric-label { display: block; color: var(--slate); font-size: 10.5px; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
    .metric-value { display: block; margin-top: 6px; font-size: 20px; font-weight: 700; color: var(--navy); }
    .status { display: inline-block; padding: 2px 9px; border-radius: 999px; background: var(--good-bg); color: var(--good); font-size: 12px; font-weight: 600; }
    .status.danger { background: var(--bad-bg); color: var(--bad); }
    .charts { display: grid; grid-template-columns: 1fr; gap: 16px; margin: 8px 0 14px; }
    .page-footer { display: flex; justify-content: space-between; gap: 16px; padding: 16px 52px; border-top: 1px solid var(--line); color: var(--slate); font-size: 11px; }
    @page { size: auto; margin: 14mm; }
    @media print {
      body { background: #fff; }
      .page { max-width: none; margin: 0; box-shadow: none; }
      .masthead { padding: 16px 0; }
      .content { padding: 18px 0 24px; }
      .page-footer { padding: 12px 0; }
      .verdict, .finding, table, tr, .kpi, .metric { break-inside: avoid; page-break-inside: avoid; }
      thead { display: table-header-group; }
      tfoot { display: table-footer-group; }
    }
    @media (max-width: 640px) {
      .content { padding: 22px 18px 32px; }
      .masthead { padding: 18px 18px; }
      h1 { font-size: 24px; }
      .meta-strip, .kpis, .toc, .signoff { grid-template-columns: 1fr 1fr; }
      table { display: block; overflow-x: auto; }
    }
  </style>
</head>
<body>
<div class="page">

<div class="masthead">
  <div class="masthead-brand">
    {{if .LogoDataURI}}<img class="brand-logo" src="{{.LogoDataURI}}" alt="APEX Load">{{end}}
    <div class="brand-lockup">
      <div class="brand-unit">APEX Load · Performance Engineering</div>
      <div class="brand-sub">Performance Test Results Analysis</div>
    </div>
  </div>
  <div class="doc-badge">
    Test <span class="mono">{{.Report.Manifest.TestID}}</span><br>
    Status <span class="mono">{{.Report.Manifest.Status}}</span><br>
    Generated <span class="mono">{{formatTime .GeneratedAt}}</span>
  </div>
</div>
<div class="header-rule"></div>

<div class="content">

<div class="eyebrow">Results Analysis Report</div>
<h1>{{if .Annotations.Title}}{{.Annotations.Title}}{{else}}{{.Report.Manifest.TestName}}{{end}}</h1>
<p class="subtitle">{{.Profile.Label}} profile generated from the archived run bundle for test
<span class="mono">{{.Report.Manifest.TestID}}</span>.{{if .Annotations.PreparedBy}} Prepared by {{.Annotations.PreparedBy}}.{{end}} {{.Profile.Description}}</p>

<div class="meta-strip">
  <div class="meta-cell"><b>Environment</b><span>{{if .Report.ChangeAttribution.Environment}}{{.Report.ChangeAttribution.Environment}}{{else}}—{{end}}</span></div>
  <div class="meta-cell"><b>Completed</b><span>{{formatTime .Report.Manifest.CompletedAt}}</span></div>
  <div class="meta-cell"><b>Peak load</b><span>{{.Report.Overview.PeakActiveVUsers}} VUsers · {{formatFloat .Report.Overview.PeakTPS}} TPS</span></div>
  <div class="meta-cell"><b>Profile</b><span>{{.Profile.Label}}</span></div>
</div>

<div class="verdict">
  <div class="verdict-badge {{if .Verdict.Failed}}fail{{else if .Verdict.Conditional}}conditional{{end}}">{{.Verdict.Status}}</div>
  <div class="verdict-body {{if .Verdict.Failed}}fail{{else if .Verdict.Conditional}}conditional{{end}}">{{.Verdict.Explanation}}</div>
</div>

<h2><span class="sec-no">§</span>Contents</h2>
<nav class="toc">
  <a href="#s1"><span class="mono">1</span>Executive summary</a>
  <a href="#s2"><span class="mono">2</span>Run at a glance</a>
  <a href="#s3"><span class="mono">3</span>SLA compliance</a>
  <a href="#s4"><span class="mono">4</span>Performance analysis</a>
  <a href="#s5"><span class="mono">5</span>Findings</a>
  <a href="#s6"><span class="mono">6</span>Recommendations</a>
  <a href="#a1"><span class="mono">A</span>Appendix A — Test setup</a>
  <a href="#a2"><span class="mono">B</span>Appendix B — Data sources &amp; glossary</a>
  <a href="#a3"><span class="mono">C</span>Appendix C — Generator assignment manifest</a>
  <a href="#a4"><span class="mono">D</span>Appendix D — Engineering diagnostics</a>
</nav>

<h2 id="s1"><span class="sec-no">1</span>Executive summary</h2>
<div class="analyst">
  {{if .Annotations.ExecutiveNarrative}}<p>{{.Annotations.ExecutiveNarrative}}</p>{{end}}
  {{with index .Annotations.SectionComments "overview"}}<p>{{.}}</p>{{end}}
</div>

<h2 id="s2"><span class="sec-no">2</span>Run at a glance</h2>
<div class="kpis">
  <div class="kpi"><b>Peak throughput</b><div class="v">{{formatFloat .Report.Overview.PeakTPS}} <small>TPS</small></div><div class="d">{{if gt .Report.Overview.PeakTargetTPS 0.0}}target {{formatFloat .Report.Overview.PeakTargetTPS}}{{else}}observed peak{{end}}</div></div>
  <div class="kpi"><b>Overall p95</b><div class="v">{{f0 .Report.Overview.P95Ms}} <small>ms</small></div><div class="d">avg {{f0 .Report.Overview.AvgResponseMs}} ms</div></div>
  <div class="kpi {{if gt .Report.Overview.ErrorPct 0.0}}warn{{end}}"><b>Error rate</b><div class="v">{{formatPercent .Report.Overview.ErrorPct}}</div><div class="d">{{hint .Report.Overview.Errors}} of {{hint .Report.Overview.Transactions}}</div></div>
  <div class="kpi"><b>Transactions</b><div class="v">{{hint .Report.Overview.Transactions}}</div><div class="d">peak {{.Report.Overview.PeakActiveVUsers}} VUsers</div></div>
</div>
<table>
<caption>Table 2.1 — Run summary</caption>
<thead><tr><th>Metric</th><th class="num">Value</th><th>Metric</th><th class="num">Value</th></tr></thead>
<tbody>
<tr><td>p50 / p90 / p99 (overall)</td><td class="num">{{f0 .Report.Overview.P50Ms}} / {{f0 .Report.Overview.P90Ms}} / {{f0 .Report.Overview.P99Ms}} ms</td><td>Peak concurrent VUsers</td><td class="num">{{.Report.Overview.PeakActiveVUsers}}</td></tr>
<tr><td>Peak TPS observed</td><td class="num">{{formatFloat .Report.Overview.PeakTPS}}</td><td>Throughput target / gap</td><td class="num">{{if gt .Report.Overview.PeakTargetTPS 0.0}}{{formatFloat .Report.Overview.PeakTargetTPS}} / {{formatPercent .Report.Overview.MaxThroughputGapPct}}{{else}}n/a{{end}}</td></tr>
<tr><td>Load generators</td><td class="num">{{if gt .GeneratorCount 0}}{{.GeneratorCount}}{{else}}n/a{{end}}</td><td>Response range</td><td class="num">{{f0 .Report.Overview.MinResponseMs}}–{{f0 .Report.Overview.MaxResponseMs}} ms</td></tr>
<tr><td>Telemetry health</td><td class="num">{{if .Report.Overview.Telemetry}}{{.Report.Overview.Telemetry.Status}}{{else}}n/a{{end}}</td><td>Data completeness</td><td class="num">{{formatFloat .Report.Completeness.ScorePct}}%</td></tr>
<tr><td>Interval summaries</td><td class="num">{{.Report.Overview.IntervalSummaries}}</td><td>Resource samples</td><td class="num">{{.Report.Overview.ResourceSamples}}</td></tr>
</tbody></table>

<h2 id="s3"><span class="sec-no">3</span>SLA compliance</h2>
<table>
<caption>Table 3.1 — SLA evaluation by transaction (p95 vs configured latency SLA; full results in run archive)</caption>
<thead><tr><th>Transaction</th><th class="num">Volume</th><th class="num">p95 (ms)</th><th class="num">SLA (ms)</th><th class="num">Error %</th><th>Result</th></tr></thead>
<tbody>
{{range .SLARows}}
<tr><td>{{.Name}}</td><td class="num">{{hint .Volume}}</td><td class="num">{{f0 .P95Ms}}</td><td class="num">{{if .HasSLA}}{{f0 .SLAMs}}{{else}}—{{end}}</td><td class="num">{{formatPercent .ErrorPct}}</td><td><span class="pill {{.ResultKind}}">{{.Result}}</span></td></tr>
{{else}}
<tr><td colspan="6" class="muted">No per-transaction archive data was recorded.</td></tr>
{{end}}
</tbody></table>
{{with index .Annotations.SectionComments "sla"}}<div class="analyst"><p>{{.}}</p></div>{{end}}

<h2 id="s4"><span class="sec-no">4</span>Performance analysis</h2>
{{if or .Charts.HasTimeline .Charts.HasActions}}
<div class="charts">
  {{if .Charts.HasTimeline}}{{.Charts.Throughput}}{{.Charts.ResponseTime}}{{.Charts.ErrorRate}}{{.Charts.VUsers}}{{end}}
  {{if .Charts.HasActions}}{{.Charts.ActionLatency}}{{end}}
</div>
{{else}}
<p class="muted">No interval timeline was captured for this run, so time-series graphs are unavailable.</p>
{{end}}
{{with index .Annotations.SectionComments "performance"}}<div class="analyst"><p>{{.}}</p></div>{{end}}

<h2 id="s5"><span class="sec-no">5</span>Findings</h2>
{{range .Findings}}
<div class="finding">
  <div class="finding-head {{if eq .Severity "major"}}major{{end}}"><span class="fid">{{.ID}}</span><span class="ft">{{.Title}}</span><span class="pill {{sevPill .Severity}}">{{sevLabel .Severity}}</span></div>
  <div class="finding-body"><dl>
    {{if .Observed}}<dt>Observed</dt><dd>{{.Observed}}</dd>{{end}}
    {{if .Evidence}}<dt>Evidence</dt><dd>{{.Evidence}}</dd>{{end}}
    {{if .RootCause}}<dt>Root cause</dt><dd>{{.RootCause}}</dd>{{end}}
    {{if .Impact}}<dt>Impact</dt><dd>{{.Impact}}</dd>{{end}}
  </dl></div>
</div>
{{else}}
<p class="muted">No automated findings were derived; the run completed without notable issues.</p>
{{end}}
{{with index .Annotations.SectionComments "errors"}}<div class="analyst"><p>{{.}}</p></div>{{end}}

<h2 id="s6"><span class="sec-no">6</span>Recommendations</h2>
<div class="analyst">
  {{with index .Annotations.SectionComments "recommendations"}}<p>{{.}}</p>{{else}}<p class="muted">Document recommended follow-up actions, owners, target dates, retest scope, or accepted risks here.</p>{{end}}
</div>

<h2 id="a1" class="appendix-h"><span class="sec-no">A</span>Appendix A — Test setup</h2>
<h3>A.1 Environment &amp; topology</h3>
<table>
<thead><tr><th>Component</th><th>Detail</th></tr></thead>
<tbody>
<tr><td>Environment</td><td>{{if .Report.ChangeAttribution.Environment}}{{.Report.ChangeAttribution.Environment}}{{else}}n/a{{end}}</td></tr>
<tr><td>Scenario revision</td><td>{{if .Report.ChangeAttribution.ScenarioRevision}}{{.Report.ChangeAttribution.ScenarioRevision}}{{else}}n/a{{end}}</td></tr>
<tr><td>Load generation</td><td>APEX Load{{if gt .GeneratorCount 0}} · {{.GeneratorCount}} generator(s){{end}}{{if .Report.ChangeAttribution.GeneratorVersions}} · {{join .Report.ChangeAttribution.GeneratorVersions}}{{end}}</td></tr>
<tr><td>Scripts</td><td>{{if .Report.ChangeAttribution.ScriptRevisions}}{{join .Report.ChangeAttribution.ScriptRevisions}}{{else}}n/a{{end}}</td></tr>
<tr><td>Data revision</td><td>{{if .Report.ChangeAttribution.DataRevision}}{{.Report.ChangeAttribution.DataRevision}}{{else}}n/a{{end}}</td></tr>
</tbody></table>
<h3>A.2 Scenario &amp; runtime settings</h3>
<table>
<thead><tr><th>Setting</th><th>Value</th><th>Setting</th><th>Value</th></tr></thead>
<tbody>
{{with .Manifest}}
<tr><td>Total VUsers</td><td>{{.CurrentConfiguration.TotalVUsers}}</td><td>Duration</td><td>{{if .CurrentConfiguration.Duration}}{{.CurrentConfiguration.Duration}}{{else}}n/a{{end}}</td></tr>
<tr><td>Scenario mode</td><td>{{if .CurrentConfiguration.ScenarioMode}}{{.CurrentConfiguration.ScenarioMode}}{{else}}n/a{{end}}</td><td>Start delay</td><td>{{if .CurrentConfiguration.StartDelay}}{{.CurrentConfiguration.StartDelay}}{{else}}none{{end}}</td></tr>
<tr><td>Script shakeout</td><td>{{if .CurrentConfiguration.ScriptShakeout}}enabled{{else}}disabled{{end}}</td><td>Resource baseline</td><td>{{if .CurrentConfiguration.ResourceBaseline}}enabled{{else}}disabled{{end}}</td></tr>
{{else}}
<tr><td colspan="4" class="muted">Run configuration was not available.</td></tr>
{{end}}
</tbody></table>
<h3>A.3 Change context</h3>
<p class="section-copy">Runtime overrides {{if .Report.ChangeAttribution.RuntimeOverrides}}were applied{{if .Report.ChangeAttribution.RuntimeOverrideFields}} to: {{join .Report.ChangeAttribution.RuntimeOverrideFields}}{{end}}{{else}}were not applied{{end}}.</p>

<h2 id="a2" class="appendix-h"><span class="sec-no">B</span>Appendix B — Data sources &amp; glossary</h2>
<table>
<thead><tr><th>Artifact</th><th>Detail</th></tr></thead>
<tbody>
<tr><td>Run archive (manifest, transactions, summaries)</td><td><span class="mono">{{.Report.Manifest.TestID}}</span></td></tr>
<tr><td>Transactions captured</td><td>{{hint .Report.Overview.Transactions}}</td></tr>
<tr><td>Interval summaries</td><td>{{.Report.Overview.IntervalSummaries}}</td></tr>
<tr><td>Resource samples</td><td>{{.Report.Overview.ResourceSamples}}</td></tr>
<tr><td>Report schema</td><td><span class="mono">{{.Report.SchemaVersion}}</span></td></tr>
</tbody></table>
<p class="section-copy"><strong>Glossary.</strong> <strong>TPS</strong> — completed transactions per second.
<strong>p95</strong> — 95th-percentile response time over the run. <strong>VU</strong> — virtual user.
<strong>Conditional pass</strong> — SLAs met overall, with remediation or review recommended before
certification is final.</p>

<h2 id="a3" class="appendix-h"><span class="sec-no">C</span>Appendix C — Generator assignment manifest</h2>
{{with .Manifest}}
<h3>C.1 Assignment strategy</h3>
<table>
<thead><tr><th>Setting</th><th>Value</th></tr></thead>
<tbody>
{{with .LoadGenerators.Assignment}}
<tr><td>Mode</td><td>{{if .Mode}}{{.Mode}}{{else}}auto{{end}}</td></tr>
<tr><td>Shard strategy</td><td>{{if .ShardStrategy}}{{.ShardStrategy}}{{else}}capacity_weighted{{end}}</td></tr>
{{if .SelectedTargets}}<tr><td>Selected targets</td><td class="mono">{{join .SelectedTargets}}</td></tr>{{end}}
{{else}}
<tr><td>Mode</td><td>auto (capacity-weighted)</td></tr>
{{end}}
</tbody></table>
<h3>C.2 Generator shards</h3>
<table>
<caption>VUser distribution by test group and generator target.</caption>
<thead><tr><th>Group</th><th>Target</th><th class="num">VUsers</th><th>Strategy</th></tr></thead>
<tbody>
{{range .LoadGenerators.GeneratorShards}}
<tr><td>{{.Group}}</td><td class="mono">{{.Target}}</td><td class="num">{{.VUsers}}</td><td>{{if .ShardStrategy}}{{.ShardStrategy}}{{else}}—{{end}}</td></tr>
{{else}}
<tr><td colspan="4" class="muted">No generator shard assignment was recorded.</td></tr>
{{end}}
</tbody></table>
<h3>C.3 Generator inventory</h3>
<table>
<caption>Per-generator capacity and health captured at run start.</caption>
<thead><tr><th>Target</th><th>Region</th><th>Status</th><th class="num">Health</th><th class="num">Active</th><th class="num">Available</th><th class="num">Max</th><th>Capacity</th></tr></thead>
<tbody>
{{range $.InventoryRows}}
<tr><td class="mono">{{.Target}}</td><td>{{if .Region}}{{.Region}}{{else}}—{{end}}</td><td>{{.Status}}</td><td class="num">{{.HealthScore}}</td><td class="num">{{.ActiveVUsers}}</td><td class="num">{{.AvailableCapacity}}</td><td class="num">{{.MaxVUsers}}</td><td>{{if .CapacityState}}{{.CapacityState}}{{else}}—{{end}}</td></tr>
{{else}}
<tr><td colspan="8" class="muted">No generator inventory snapshot was recorded.</td></tr>
{{end}}
</tbody></table>
{{if gt $.InventoryOverflow 0}}<p class="section-copy">… and {{$.InventoryOverflow}} more generator(s) not shown.</p>{{end}}
<h3>C.4 Scripts &amp; groups</h3>
<table>
<thead><tr><th>Group</th><th>Script</th><th>Protocol</th><th class="num">VUsers</th><th>Ramp up</th><th>Initialization</th></tr></thead>
<tbody>
{{range .Scripts}}
<tr><td>{{.GroupName}}</td><td>{{if .ScriptName}}{{.ScriptName}}{{else}}{{.ScriptID}}{{end}}</td><td>{{if .Protocol}}{{.Protocol}}{{else}}—{{end}}</td><td class="num">{{.VUsers}}</td><td>{{if .RampUp}}{{.RampUp}}{{else}}—{{end}}</td><td>{{if .Initialization}}{{.Initialization}}{{else}}—{{end}}</td></tr>
{{else}}
<tr><td colspan="6" class="muted">No script/group assignment was recorded.</td></tr>
{{end}}
</tbody></table>
<h3>C.5 Execution settings</h3>
<table>
<thead><tr><th>Setting</th><th>Value</th></tr></thead>
<tbody>
<tr><td>Metrics interval (s)</td><td>{{.LoadGenerators.ExecutionSettings.General.MetricsIntervalSeconds}}</td></tr>
<tr><td>Max concurrent tests</td><td>{{.LoadGenerators.ExecutionSettings.General.MaxConcurrentTests}}</td></tr>
{{range $key, $value := .LoadGenerators.ControllerEnv}}
<tr><td class="mono">{{$key}}</td><td class="mono">{{$value}}</td></tr>
{{end}}
</tbody></table>
{{else}}
<p class="muted">The generator assignment manifest was not available for this run.</p>
{{end}}

<h2 id="a4" class="appendix-h"><span class="sec-no">D</span>Appendix D — Engineering diagnostics</h2>

<h3>Completeness Score</h3>
<p class="section-copy">{{formatFloat .Report.Completeness.ScorePct}}% — {{.Report.Completeness.Status}}</p>
{{if .Report.Completeness.Findings}}
<table>
  <thead><tr><th>Dimension</th><th>Severity</th><th class="num">Penalty</th><th>Finding</th></tr></thead>
  <tbody>
    {{range .Report.Completeness.Findings}}
    <tr><td>{{.Dimension}}</td><td>{{.Severity}}</td><td class="num">-{{formatFloat .Penalty}}</td><td>{{.Message}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}

<h3>Schema Compatibility</h3>
<p class="section-copy">Report schema {{.Report.SchemaVersion}} is {{.Report.Compatibility.Status}} with archive manifest {{.Report.Compatibility.ManifestSchemaVersion}}.</p>
<table>
  <thead><tr><th>Output</th><th>Artifact</th><th>Status</th><th>Schema</th></tr></thead>
  <tbody>
    {{range .Report.Compatibility.Outputs}}
    <tr><td>{{.Format}}</td><td>{{if .Artifact}}{{.Artifact}}{{else}}n/a{{end}}</td><td>{{.Status}}</td><td>{{if .SchemaVersion}}{{.SchemaVersion}}{{else}}n/a{{end}}</td></tr>
    {{end}}
  </tbody>
</table>

<h3>Change Attribution</h3>
<div class="overview">
  <div class="metric"><span class="metric-label">Scripts</span><span class="metric-value">{{if .Report.ChangeAttribution.ScriptRevisions}}{{join .Report.ChangeAttribution.ScriptRevisions}}{{else}}n/a{{end}}</span></div>
  <div class="metric"><span class="metric-label">Scenario</span><span class="metric-value">{{if .Report.ChangeAttribution.ScenarioRevision}}{{.Report.ChangeAttribution.ScenarioRevision}}{{else}}n/a{{end}}</span></div>
  <div class="metric"><span class="metric-label">Environment</span><span class="metric-value">{{if .Report.ChangeAttribution.Environment}}{{.Report.ChangeAttribution.Environment}}{{else}}n/a{{end}}</span></div>
  <div class="metric"><span class="metric-label">Generators</span><span class="metric-value">{{if .Report.ChangeAttribution.GeneratorVersions}}{{join .Report.ChangeAttribution.GeneratorVersions}}{{else}}n/a{{end}}</span></div>
</div>

{{if .Report.SummaryTimeline}}
<h3>Summary Timeline</h3>
<table>
  <thead><tr><th>Timestamp</th><th class="num">Active VUsers</th><th class="num">TPS</th><th class="num">Target TPS</th><th class="num">Gap</th><th class="num">P95 (ms)</th><th class="num">Error %</th><th class="num">Total Tx</th><th class="num">Total Errors</th></tr></thead>
  <tbody>
    {{range .Report.SummaryTimeline}}
    <tr><td>{{formatTime .Timestamp}}</td><td class="num">{{.ActiveVUsers}}</td><td class="num">{{formatFloat .TPS}}</td><td class="num">{{formatFloat .TargetTPS}}</td><td class="num"><span class="pill {{if .ThroughputTargetMissed}}fail{{else}}pass{{end}}">{{formatPercent .ThroughputGapPct}}</span></td><td class="num">{{formatFloat .P95Ms}}</td><td class="num">{{formatPercent .ErrorPct}}</td><td class="num">{{.TotalTx}}</td><td class="num">{{.TotalErrors}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}

{{if .Report.ErrorPatterns}}
<h3>Error Patterns</h3>
<table>
  <thead><tr><th>Pattern</th><th class="num">Occurrences</th><th>First Seen</th><th>Latest Seen</th><th>Actions</th><th>Snapshot</th></tr></thead>
  <tbody>
    {{range .Report.ErrorPatterns}}
    <tr><td>{{.Sample}}</td><td class="num">{{.Count}}</td><td>{{formatTime .FirstSeen}}</td><td>{{formatTime .LatestSeen}}</td><td>{{if .Actions}}{{join .Actions}}{{else}}n/a{{end}}</td><td>{{if .Snapshot}}retained{{else}}n/a{{end}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}

<h3>Resource Peaks</h3>
<div class="overview">
  <div class="metric"><span class="metric-label">CPU Peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.CPUProcessPct}}%</span></div>
  <div class="metric"><span class="metric-label">Memory Peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.MemProcessMB}} MB</span></div>
  <div class="metric"><span class="metric-label">Net Sent Peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.NetSentKbps}} KB/s</span></div>
  <div class="metric"><span class="metric-label">Net Recv Peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.NetRecvKbps}} KB/s</span></div>
</div>

<h3>Action Summary</h3>
<table>
  <thead><tr><th>Action</th><th class="num">Transactions</th><th class="num">Errors</th><th class="num">Error %</th><th class="num">Avg (ms)</th><th class="num">Max (ms)</th></tr></thead>
  <tbody>
    {{range .ActionRows}}
    <tr><td>{{.Name}}</td><td class="num">{{.Stats.Transactions}}</td><td class="num">{{.Stats.Errors}}</td><td class="num">{{formatPercent .Stats.ErrorPct}}</td><td class="num">{{formatFloat .Stats.AvgResponseMs}}</td><td class="num">{{formatFloat .Stats.MaxResponseMs}}</td></tr>
    {{else}}
    <tr><td colspan="6" class="muted">No action-level archive data was recorded.</td></tr>
    {{end}}
  </tbody>
</table>

{{if .Report.Overview.Browser}}
<h3>Browser Metrics</h3>
<div class="overview">
  <div class="metric"><span class="metric-label">Samples</span><span class="metric-value">{{.Report.Overview.Browser.Samples}}</span></div>
  <div class="metric"><span class="metric-label">Avg FCP</span><span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgFCPMs}} ms</span></div>
  <div class="metric"><span class="metric-label">Avg LCP</span><span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgLCPMs}} ms</span></div>
  <div class="metric"><span class="metric-label">Avg CLS</span><span class="metric-value">{{formatScore .Report.Overview.Browser.AvgCLSScore}}</span></div>
  <div class="metric"><span class="metric-label">Avg INP</span><span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgINPMs}} ms</span></div>
  <div class="metric"><span class="metric-label">Avg JS Heap</span><span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgJSHeapUsedMB}} MB</span></div>
</div>
{{if .BrowserEngineRows}}
<h3>Engine Rollup</h3>
<table>
  <thead><tr><th>Engine</th><th class="num">Samples</th><th class="num">Avg LCP</th><th class="num">Avg CLS</th><th class="num">Avg INP</th></tr></thead>
  <tbody>
    {{range .BrowserEngineRows}}
    <tr><td>{{.BrowserEngine}}</td><td class="num">{{.Samples}}</td><td class="num">{{formatFloat .AvgLCPMs}} ms</td><td class="num">{{formatScore .AvgCLSScore}}</td><td class="num">{{formatFloat .AvgINPMs}} ms</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{if .BrowserPlatformRows}}
<h3>Platform Rollup</h3>
<table>
  <thead><tr><th>Platform</th><th>Engine</th><th class="num">Samples</th><th class="num">Avg LCP</th><th class="num">Avg CLS</th><th class="num">Avg INP</th></tr></thead>
  <tbody>
    {{range .BrowserPlatformRows}}
    <tr><td>{{.DevicePlatform}}</td><td>{{.BrowserEngine}}</td><td class="num">{{.Samples}}</td><td class="num">{{formatFloat .AvgLCPMs}} ms</td><td class="num">{{formatScore .AvgCLSScore}}</td><td class="num">{{formatFloat .AvgINPMs}} ms</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{if .BrowserDeviceRows}}
<h3>Device Rollup</h3>
<table>
  <thead><tr><th>Device</th><th>Platform</th><th>Engine</th><th>Tier</th><th class="num">Avg LCP</th><th class="num">Avg INP</th></tr></thead>
  <tbody>
    {{range .BrowserDeviceRows}}
    <tr><td>{{.DeviceProfile}}</td><td>{{.DevicePlatform}}</td><td>{{.BrowserEngine}}</td><td>{{.DeviceTier}}</td><td class="num">{{formatFloat .AvgLCPMs}} ms</td><td class="num">{{formatFloat .AvgINPMs}} ms</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{end}}

{{if or .SOAPOperationRows .SOAPFaultCodeRows .SOAPFaultBuckets .SOAPPayloadBuckets}}
<h3>SOAP Diagnostics</h3>
{{if .SOAPOperationRows}}
<h3>SOAP Operation Summary</h3>
<table>
  <thead><tr><th>Operation</th><th class="num">Transactions</th><th class="num">Errors</th><th class="num">Error %</th><th class="num">Avg (ms)</th></tr></thead>
  <tbody>
    {{range .SOAPOperationRows}}
    <tr><td>{{.Name}}</td><td class="num">{{.Stats.Transactions}}</td><td class="num">{{.Stats.Errors}}</td><td class="num">{{formatPercent .Stats.ErrorPct}}</td><td class="num">{{formatFloat .Stats.AvgResponseMs}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{if .SOAPFaultCodeRows}}
<h3>SOAP Fault Codes</h3>
<table>
  <thead><tr><th>Code</th><th class="num">Count</th></tr></thead>
  <tbody>
    {{range .SOAPFaultCodeRows}}
    <tr><td>{{.Name}}</td><td class="num">{{.Count}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{if .SOAPFaultCategoryRows}}
<h3>SOAP Fault Categories</h3>
<table>
  <thead><tr><th>Category</th><th class="num">Count</th></tr></thead>
  <tbody>
    {{range .SOAPFaultCategoryRows}}
    <tr><td>{{.Name}}</td><td class="num">{{.Count}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{if .SOAPFaultBuckets}}
<h3>SOAP Fault Timeline</h3>
<table>
  <thead><tr><th>Timestamp</th><th class="num">Total</th><th>Codes</th><th>Categories</th></tr></thead>
  <tbody>
    {{range .SOAPFaultBuckets}}
    <tr><td>{{formatTime .Timestamp}}</td><td class="num">{{.Total}}</td><td>{{.Codes}}</td><td>{{.Categories}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{if .SOAPPayloadBuckets}}
<h3>SOAP Payload Timeline</h3>
<table>
  <thead><tr><th>Timestamp</th><th class="num">Samples</th><th class="num">Avg Request KB</th><th class="num">Avg Response KB</th><th class="num">Avg Envelope Overhead KB</th><th class="num">Max Envelope Overhead KB</th></tr></thead>
  <tbody>
    {{range .SOAPPayloadBuckets}}
    <tr><td>{{formatTime .Timestamp}}</td><td class="num">{{.Samples}}</td><td class="num">{{formatFloat .AvgRequestPayloadKB}}</td><td class="num">{{formatFloat .AvgResponsePayloadKB}}</td><td class="num">{{formatFloat .AvgEnvelopeOverheadKB}}</td><td class="num">{{formatFloat .MaxEnvelopeOverheadKB}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{end}}

<h2><span class="sec-no">✓</span>Review &amp; sign-off</h2>
<div class="signoff">
  <div class="sign-cell"><b>Prepared by</b><div class="sign-line"></div><div class="sign-meta">{{if .Annotations.PreparedBy}}{{.Annotations.PreparedBy}}{{else}}Performance Engineer{{end}} · Date</div></div>
  <div class="sign-cell"><b>Reviewed by</b><div class="sign-line"></div><div class="sign-meta">Performance Engineering Lead · Date</div></div>
  <div class="sign-cell"><b>Application owner</b><div class="sign-line"></div><div class="sign-meta">Delivery Lead · Date</div></div>
  <div class="sign-cell"><b>Release approval</b><div class="sign-line"></div><div class="sign-meta">Release Manager · Date</div></div>
</div>

</div><!-- .content -->

<footer class="page-footer">
  <span>APEX Load · Performance Test Results Analysis</span>
  <span class="mono">{{.Report.SchemaVersion}} · generated {{formatTime .GeneratedAt}}{{if .Annotations.UpdatedBy}} · commentary by {{.Annotations.UpdatedBy}}{{end}}</span>
</footer>
</div>
</body>
</html>`))

func renderRunArchiveHTMLReport(report *RunArchiveReport) ([]byte, error) {
	return renderRunArchiveHTMLReportWithProfile(report, "engineer")
}

func renderRunArchiveHTMLReportWithProfile(report *RunArchiveReport, profileID string) ([]byte, error) {
	return renderRunArchiveHTMLReportWithAnnotations(report, profileID, nil, nil)
}

// renderRunArchiveHTMLReportWithAnnotations renders the report with analyst
// commentary, an auto-derived verdict and findings, supporting graphs, and a
// generator-assignment manifest appendix. When annotations is nil, generated
// defaults are derived from the report data so the document still reads well
// unedited. When manifest is nil, the manifest appendix renders an
// "unavailable" note rather than failing.
func renderRunArchiveHTMLReportWithAnnotations(report *RunArchiveReport, profileID string, annotations *ReportAnnotations, manifest *TestRunManifest) ([]byte, error) {
	if report == nil || report.Manifest == nil {
		return nil, fmt.Errorf("archive report is incomplete")
	}
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return nil, fmt.Errorf("unknown report profile %q", profileID)
	}
	if annotations == nil {
		annotations = defaultReportAnnotations(report, report.Manifest.TestID, profile.ID)
	}

	actionRows := make([]runArchiveHTMLActionRow, 0, len(report.ByAction))
	for name, stats := range report.ByAction {
		actionRows = append(actionRows, runArchiveHTMLActionRow{Name: name, Stats: stats})
	}
	sort.Slice(actionRows, func(i, j int) bool {
		if actionRows[i].Stats.Transactions == actionRows[j].Stats.Transactions {
			return actionRows[i].Name < actionRows[j].Name
		}
		return actionRows[i].Stats.Transactions > actionRows[j].Stats.Transactions
	})

	soapOperationRows := make([]runArchiveHTMLActionRow, 0, len(report.BySOAPOperation))
	for name, stats := range report.BySOAPOperation {
		soapOperationRows = append(soapOperationRows, runArchiveHTMLActionRow{Name: name, Stats: stats})
	}
	sort.Slice(soapOperationRows, func(i, j int) bool {
		if soapOperationRows[i].Stats.Transactions == soapOperationRows[j].Stats.Transactions {
			return soapOperationRows[i].Name < soapOperationRows[j].Name
		}
		return soapOperationRows[i].Stats.Transactions > soapOperationRows[j].Stats.Transactions
	})

	soapFaultCodeRows := buildHTMLCountRows(report.BySOAPFaultCode)
	soapFaultCategoryRows := buildHTMLCountRows(report.BySOAPFaultCategory)
	soapFaultBuckets := buildHTMLSOAPFaultBuckets(report.SOAPFaultTimeline)
	soapPayloadBuckets := buildHTMLSOAPPayloadBuckets(report.SOAPPayloadTimeline)
	browserEngineRows := buildHTMLBrowserEngineRows(report.BrowserByEngine)
	browserPlatformRows := buildHTMLBrowserPlatformRows(report.BrowserByPlatform)
	browserDeviceRows := buildHTMLBrowserDeviceRows(report.BrowserByDevice)

	verdict := deriveReportVerdict(report)
	if strings.TrimSpace(annotations.VerdictOverride) != "" {
		verdict = applyVerdictOverride(annotations.VerdictOverride, annotations.VerdictExplanation, verdict)
	}
	findings := annotations.Findings
	if len(findings) == 0 {
		findings = deriveReportFindings(report)
	} else {
		findings = normalizeReportFindings(findings)
	}
	inventoryRows, inventoryOverflow := manifestInventoryRows(manifest, 50)

	data := runArchiveHTMLReportData{
		GeneratedAt:           time.Now().UTC(),
		Report:                report,
		Profile:               profile,
		Annotations:           annotations,
		CommentarySections:    reportCommentarySectionOrder(),
		Charts:                buildReportCharts(report),
		LogoDataURI:           reportLogoDataURI(),
		Manifest:              manifest,
		Verdict:               verdict,
		Findings:              findings,
		SLARows:               buildSLAComplianceRows(report),
		GeneratorCount:        manifestGeneratorCount(manifest),
		InventoryRows:         inventoryRows,
		InventoryOverflow:     inventoryOverflow,
		ActionRows:            actionRows,
		SOAPOperationRows:     soapOperationRows,
		SOAPFaultCodeRows:     soapFaultCodeRows,
		SOAPFaultCategoryRows: soapFaultCategoryRows,
		SOAPFaultBuckets:      soapFaultBuckets,
		SOAPPayloadBuckets:    soapPayloadBuckets,
		BrowserEngineRows:     browserEngineRows,
		BrowserPlatformRows:   browserPlatformRows,
		BrowserDeviceRows:     browserDeviceRows,
	}

	var buf bytes.Buffer
	if err := runArchiveReportTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applyVerdictOverride replaces the auto-derived verdict with an analyst-set
// status (and optional explanation), recomputing the conditional/failed styling
// flags from the override label.
func applyVerdictOverride(status, explanation string, base reportVerdict) reportVerdict {
	override := reportVerdict{Status: strings.TrimSpace(status)}
	switch strings.ToLower(override.Status) {
	case "fail", "failed":
		override.Status = reportVerdictFail
		override.Failed = true
	case "conditional pass", "conditional":
		override.Status = reportVerdictConditional
		override.Conditional = true
	case "pass", "passed":
		override.Status = reportVerdictPass
	default:
		// An out-of-vocabulary override (e.g. a direct API client bypassing the
		// constrained dashboard dropdown) must NOT fall through to clean green
		// pass styling on a sign-off document. Surface it as a Conditional Pass so
		// it gets the warn badge while keeping the submitted label visible.
		override.Conditional = true
	}
	if strings.TrimSpace(explanation) != "" {
		override.Explanation = explanation
	} else {
		override.Explanation = base.Explanation
	}
	return override
}

// manifestGeneratorCount returns the number of distinct generator targets in
// the run manifest (preferring the inventory snapshot, falling back to shards).
func manifestGeneratorCount(manifest *TestRunManifest) int {
	if manifest == nil {
		return 0
	}
	seen := make(map[string]struct{})
	for _, entry := range manifest.LoadGenerators.GeneratorInventory {
		if entry.Target != "" {
			seen[entry.Target] = struct{}{}
		}
	}
	if len(seen) == 0 {
		for _, shard := range manifest.LoadGenerators.GeneratorShards {
			if shard.Target != "" {
				seen[shard.Target] = struct{}{}
			}
		}
	}
	return len(seen)
}

// manifestInventoryRows returns the generator-inventory rows for the appendix,
// capped at limit, plus the count of any rows beyond the cap.
func manifestInventoryRows(manifest *TestRunManifest, limit int) ([]GeneratorInventoryEntry, int) {
	if manifest == nil {
		return nil, 0
	}
	inventory := manifest.LoadGenerators.GeneratorInventory
	if limit > 0 && len(inventory) > limit {
		return inventory[:limit], len(inventory) - limit
	}
	return inventory, 0
}

var (
	reportLogoOnce sync.Once
	reportLogoURI  template.URL
)

// reportLogoDataURI returns the APEX Load logo as a base64 data URI so reports
// are self-contained (no external asset fetch). Returns "" if the embedded
// asset cannot be read. Wrapped as template.URL so html/template emits the
// data: URI without rewriting it to a safe placeholder.
func reportLogoDataURI() template.URL {
	reportLogoOnce.Do(func() {
		data, err := StaticFS.ReadFile("static/apex-load-logo.png")
		if err != nil {
			return
		}
		reportLogoURI = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(data))
	})
	return reportLogoURI
}

func buildHTMLCountRows(source map[string]int64) []runArchiveHTMLCountRow {
	rows := make([]runArchiveHTMLCountRow, 0, len(source))
	for name, count := range source {
		rows = append(rows, runArchiveHTMLCountRow{Name: name, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Count > rows[j].Count
	})
	return rows
}

func buildHTMLSOAPFaultBuckets(source []RunArchiveSOAPFaultTimelineBucket) []runArchiveHTMLSOAPFaultBucketRow {
	rows := make([]runArchiveHTMLSOAPFaultBucketRow, 0, len(source))
	for _, bucket := range source {
		rows = append(rows, runArchiveHTMLSOAPFaultBucketRow{
			Timestamp:  bucket.Timestamp,
			Total:      bucket.Total,
			Codes:      joinCountRows(buildHTMLCountRows(bucket.ByCode)),
			Categories: joinCountRows(buildHTMLCountRows(bucket.ByCategory)),
		})
	}
	return rows
}

func buildHTMLSOAPPayloadBuckets(source []RunArchiveSOAPPayloadTimelineBucket) []runArchiveHTMLSOAPPayloadBucketRow {
	rows := make([]runArchiveHTMLSOAPPayloadBucketRow, 0, len(source))
	for _, bucket := range source {
		rows = append(rows, runArchiveHTMLSOAPPayloadBucketRow{
			Timestamp:             bucket.Timestamp,
			Samples:               bucket.Samples,
			AvgRequestPayloadKB:   bucket.AvgRequestPayloadKB,
			AvgResponsePayloadKB:  bucket.AvgResponsePayloadKB,
			AvgEnvelopeOverheadKB: bucket.AvgEnvelopeOverheadKB,
			MaxEnvelopeOverheadKB: bucket.MaxEnvelopeOverheadKB,
		})
	}
	return rows
}

func buildHTMLBrowserEngineRows(source map[string]*BrowserEngineLiveMetrics) []*BrowserEngineLiveMetrics {
	rows := make([]*BrowserEngineLiveMetrics, 0, len(source))
	for _, row := range source {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].BrowserEngine < rows[j].BrowserEngine
	})
	return rows
}

func buildHTMLBrowserPlatformRows(source map[string]*BrowserPlatformLiveMetrics) []*BrowserPlatformLiveMetrics {
	rows := make([]*BrowserPlatformLiveMetrics, 0, len(source))
	for _, row := range source {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DevicePlatform == rows[j].DevicePlatform {
			return rows[i].BrowserEngine < rows[j].BrowserEngine
		}
		return rows[i].DevicePlatform < rows[j].DevicePlatform
	})
	return rows
}

func buildHTMLBrowserDeviceRows(source map[string]*BrowserDeviceLiveMetrics) []*BrowserDeviceLiveMetrics {
	rows := make([]*BrowserDeviceLiveMetrics, 0, len(source))
	for _, row := range source {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].DeviceProfile < rows[j].DeviceProfile
	})
	return rows
}

func joinCountRows(rows []runArchiveHTMLCountRow) string {
	if len(rows) == 0 {
		return "n/a"
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("%s (%d)", row.Name, row.Count))
	}
	return strings.Join(parts, ", ")
}
