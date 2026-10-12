package api

import "html/template"

// runArchiveReportShellTemplate is the single, profile-agnostic page shell. It
// renders the shared chrome (masthead, cover, meta strip, footer) and then
// places each profile-selected section body, in profile order, wrapped with a
// data-report-section marker. The bespoke per-profile templates it replaced are
// gone; profile metadata now drives the entire document.
var runArchiveReportShellTemplate = template.Must(template.New("run-archive-report-shell").Funcs(profileHTMLFuncMap).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>APEX Load Report — {{.Data.Report.Manifest.TestName}}</title>
  <style>
    :root {
      color-scheme: light;
      --navy:   #1f2933;
      --blue:   #0f766e;
      --blue2:  #0c645d;
      --sky:    #e8f5f2;
      --silver: #f7f4ee;
      --line:   #d8cfc2;
      --ink:    #1f2933;
      --slate:  #5b6875;
      --good:    #1a6e3c;
      --good-bg: #e6f4ed;
      --warn:    #b45309;
      --warn-bg: #fdf3da;
      --bad:     #b42318;
      --bad-bg:  #fbe9e7;
      --info:    var(--blue2);
      --info-bg: var(--sky);
    }
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
    body {
      font-family: "Iowan Old Style", "Palatino Linotype", Georgia, serif;
      font-size: 14.5px;
      line-height: 1.58;
      color: var(--ink);
      background: #e8e3d9;
    }
    .mono { overflow-wrap: anywhere; word-break: break-word; font-family: "IBM Plex Mono", "SF Mono", Menlo, monospace; }
    .page { max-width: 960px; margin: 28px auto; background: #fffdf8; box-shadow: 0 1px 32px rgba(31,41,51,.14); }
    .skip-link { position: absolute; left: -10000px; top: 8px; z-index: 10; background: #fff; color: var(--navy); padding: 8px 12px; }
    .skip-link:focus { left: 8px; }
    .masthead { background: #fffdf8; padding: 22px 52px 20px; display: flex; align-items: center; justify-content: space-between; gap: 24px; border-bottom: 1px solid var(--line); }
    .masthead-brand { display: flex; align-items: center; gap: 14px; }
    .brand-logo { height: 34px; width: auto; }
    .brand-unit { font-size: 12.5px; font-weight: 600; color: var(--blue); letter-spacing: .06em; text-transform: uppercase; line-height: 1.3; }
    .brand-sub { font-size: 11px; color: var(--slate); letter-spacing: .03em; margin-top: 2px; }
    .doc-badge { text-align: right; font-size: 11px; line-height: 1.7; color: var(--slate); }
    .doc-badge .mono { color: var(--ink); }
    .header-rule { height: 4px; background: linear-gradient(90deg, var(--blue) 0%, var(--blue2) 100%); }
    .content { overflow-wrap: anywhere; padding: 40px 52px 60px; }
    .eyebrow { font-size: 10.5px; font-weight: 700; letter-spacing: .18em; text-transform: uppercase; color: var(--blue2); margin-bottom: 8px; }
    h1 { font-family: "Avenir Next", "Segoe UI", sans-serif; font-size: 30px; font-weight: 700; letter-spacing: -.02em; line-height: 1.18; color: var(--navy); margin-bottom: 10px; }
    .subtitle { font-size: 14.5px; color: var(--slate); max-width: 72ch; line-height: 1.55; margin-bottom: 28px; }
    .meta-strip { display: grid; grid-template-columns: repeat(4, 1fr); border: 1px solid var(--line); overflow: hidden; margin-bottom: 24px; }
    .meta-cell { background: var(--silver); border-right: 1px solid var(--line); padding: 11px 14px; }
    .meta-cell:last-child { border-right: none; }
    .meta-cell b { display: block; font-size: 9.5px; font-weight: 700; letter-spacing: .14em; text-transform: uppercase; color: var(--slate); margin-bottom: 4px; }
    .meta-cell span { font-size: 13px; font-weight: 600; }
    .report-block { display: block; }
    .review-summary { border: 1px solid var(--line); border-left: 5px solid var(--blue2); padding: 18px 20px; margin: 0 0 24px; background: #fff; }
    .review-summary h2 { margin: 0 0 12px; border: 0; padding: 0; }
    .review-summary-status { font-size: 17px; font-weight: 700; color: var(--navy); margin-bottom: 6px; }
    .review-summary-status.fail { color: var(--bad); }
    .review-summary-status.conditional { color: var(--warn); }
    .review-summary p { margin: 0 0 12px; }
    .review-summary-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px 18px; }
    .review-summary-grid div { min-width: 0; }
    .review-summary-grid b { display: block; font-size: 10px; color: var(--slate); text-transform: uppercase; letter-spacing: .08em; }
    .review-summary-grid span { font-weight: 600; }
    .evidence-gaps { margin-top: 15px; padding-top: 12px; border-top: 1px solid var(--line); }
    .evidence-gaps h3 { margin: 0 0 5px; }
    .evidence-gaps ul { padding-left: 20px; }
    .summary-note { margin: 12px 0 0; padding: 10px 12px; background: var(--silver); border-left: 3px solid var(--blue2); }
    .summary-note strong { color: var(--navy); }
    .report-contents { border: 1px solid var(--line); background: var(--silver); padding: 16px 20px; margin-bottom: 26px; }
    .report-contents h2 { margin: 0 0 10px; border: 0; padding: 0; }
    .report-contents ol { columns: 2; column-gap: 32px; padding-left: 20px; }
    .report-contents li { break-inside: avoid; margin: 0 0 5px; }
    .not-captured { margin: 10px 0 0; font-size: 12.5px; color: var(--slate); }
    .report-contents a, .back-link { color: var(--blue2); text-decoration: underline; text-underline-offset: 2px; }
    .report-contents a:focus-visible, .back-link:focus-visible { outline: 3px solid var(--blue); outline-offset: 2px; }
    .report-block { scroll-margin-top: 20px; }
    .back-link { display: inline-block; font-size: 11px; margin-top: 4px; }
    .integrity-banner { display: flex; align-items: baseline; gap: 10px; padding: 12px 16px; border-radius: 8px; margin: 6px 0 14px; font-size: 13px; border: 1px solid; }
    .integrity-banner.pass { background: var(--good-bg); color: var(--good); border-color: #b7e0c6; }
    .integrity-banner.caution { background: var(--warn-bg); color: var(--warn); border-color: #f0deb0; }
    .integrity-banner.fail { background: var(--bad-bg); color: var(--bad); border-color: #f3c2bb; }
    .integrity-banner .integrity-label { font-weight: 800; text-transform: uppercase; letter-spacing: .04em; white-space: nowrap; }
    .verdict { display: flex; align-items: stretch; margin: 18px 0 32px; border: 1px solid var(--line); }
    .verdict-badge { flex: 0 0 auto; padding: 16px 22px; font-weight: 700; font-size: 12px; letter-spacing: .08em; text-transform: uppercase; color: #fff; background: var(--good); text-align: center; min-width: 130px; display: flex; align-items: center; justify-content: center; }
    .verdict-badge.conditional { background: var(--warn); }
    .verdict-badge.fail { background: var(--bad); }
    .verdict-body { padding: 14px 20px; font-size: 13.5px; background: var(--good-bg); flex: 1; border-left: 3px solid var(--good); line-height: 1.5; }
    .verdict-body.conditional { background: var(--warn-bg); border-left-color: var(--warn); }
    .verdict-body.fail { background: var(--bad-bg); border-left-color: var(--bad); }
    .outcome-pill { display: inline-block; padding: 4px 10px; border-radius: 999px; background: var(--good-bg); color: var(--good); font-weight: 700; }
    .outcome-pill.fail { background: var(--bad-bg); color: var(--bad); }
    .outcome-pill.conditional { background: var(--warn-bg); color: var(--warn); }
    h2 { font-family: "Avenir Next", "Segoe UI", sans-serif; font-size: 19px; font-weight: 700; color: var(--navy); margin: 30px 0 10px; padding-bottom: 8px; border-bottom: 2px solid var(--line); }
    h3 { font-family: "Avenir Next", "Segoe UI", sans-serif; font-size: 13.5px; font-weight: 700; color: var(--navy); margin: 22px 0 8px; }
    .kpis { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 18px; }
    .kpi { border: 1px solid var(--line); border-top: 3px solid var(--blue); padding: 12px 14px; background: var(--silver); }
    .kpi.warn { border-top-color: var(--warn); }
    .kpi b { display: block; font-size: 9.5px; font-weight: 700; letter-spacing: .12em; text-transform: uppercase; color: var(--slate); margin-bottom: 6px; }
    .kpi .v { font-size: 26px; font-weight: 700; color: var(--navy); line-height: 1.05; }
    .kpi .v small { font-size: 13px; font-weight: 600; color: var(--slate); }
    .kpi .d { font-size: 11px; color: var(--slate); margin-top: 4px; }
    .kpi-basis { font-size: 11px; color: var(--slate); margin: 0 0 8px; }
    table { width: 100%; table-layout: auto; border-collapse: collapse; border: 1px solid var(--line); margin: 6px 0 14px; font-size: 13px; }
    caption { caption-side: top; text-align: left; font-size: 11px; color: var(--slate); padding: 0 0 6px; font-style: italic; }
    th, td { overflow-wrap: anywhere; min-width: 0; padding: 9px 12px; border-bottom: 1px solid var(--line); text-align: left; vertical-align: top; }
    th { overflow-wrap: break-word; background: var(--silver); font-size: 10.5px; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; color: var(--slate); }
    td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
    td.nowrap, th.nowrap { overflow-wrap: normal; white-space: nowrap; }
    tr:last-child td { border-bottom: 0; }
    .pill { display: inline-block; padding: 2px 9px; border-radius: 999px; font-size: 11px; font-weight: 600; white-space: nowrap; }
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
    /* Names and identifiers in a metric tile read as text, not as a figure. */
    .metric-text { display: block; margin-top: 6px; font-size: 13px; font-weight: 600; color: var(--navy); overflow-wrap: anywhere; }
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
      .skip-link, .report-contents, .back-link { display: none; }
      .export-pdf .report-contents { display: block; break-after: page; }
      .export-pdf .masthead { border-bottom: 0; }
      .export-pdf h2, .export-pdf h3 { break-after: avoid; }
      .export-pdf .report-block { break-before: auto; }
      .export-pdf .page-footer { display: none; }
      .export-pdf table { break-inside: auto; page-break-inside: auto; }
      .review-summary { break-inside: avoid; }
    }
    @media (max-width: 640px) {
      .content { padding: 22px 18px 32px; }
      .masthead { padding: 18px 18px; }
      h1 { font-size: 24px; }
      .meta-strip, .kpis, .signoff { grid-template-columns: 1fr 1fr; }
      table { display: block; overflow-x: auto; }
      .review-summary-grid { grid-template-columns: 1fr 1fr; }
      .report-contents ol { columns: 1; }
    }
  </style>
</head>
<body>
<a class="skip-link" href="#report-main">Skip to report</a>
<div class="page">

<div class="masthead">
  <div class="masthead-brand">
    {{if .Data.LogoDataURI}}<img class="brand-logo" src="{{.Data.LogoDataURI}}" alt="APEX Load">{{end}}
    <div class="brand-lockup">
      <div class="brand-unit">APEX Load · Performance Engineering</div>
      <div class="brand-sub">Performance Test Results Analysis</div>
    </div>
  </div>
  <div class="doc-badge">
    Run <span>{{reportRunName .Data.Report}}</span><br>
    Execution <span class="mono">{{executionLabel .Data.Report.Manifest.TerminationReason}}</span><br>
    Generated <span class="mono">{{formatTime .Data.GeneratedAt}}</span>
  </div>
</div>
<div class="header-rule"></div>

<main class="content" id="report-main">

<div class="eyebrow">{{.Data.Profile.Label}} report</div>
<h1>{{if .Data.Annotations.Title}}{{.Data.Annotations.Title}}{{else}}{{.Data.Report.Manifest.TestName}}{{end}}</h1>
<p class="subtitle">Performance results for <strong>{{reportRunName .Data.Report}}</strong>.{{if .Data.Annotations.PreparedBy}} Prepared by {{.Data.Annotations.PreparedBy}}.{{end}}</p>

<div class="meta-strip">
  <div class="meta-cell"><b>Environment</b><span>{{reportEnvironment .Data.Report}}</span></div>
  <div class="meta-cell"><b>Ended</b><span>{{formatTime .Data.Report.Manifest.CompletedAt}}</span></div>
  <div class="meta-cell"><b>Peak load</b><span>{{.Data.Report.Overview.PeakActiveVUsers}} VUsers · {{formatFloat (headline .Data.Report).PeakTPS}} attempts/s</span></div>
  <div class="meta-cell"><b>Profile</b><span>{{.Data.Profile.Label}}</span></div>
</div>

<section class="review-summary" aria-labelledby="review-summary-heading">
  <h2 id="review-summary-heading">Review summary</h2>
  <div class="review-summary-status {{if .Data.Verdict.Failed}}fail{{else if .Data.Verdict.Conditional}}conditional{{end}}">{{.Data.Verdict.Status}}</div>
  <p>{{.Data.Verdict.Explanation}}</p>
  <div class="review-summary-grid">
    <div><b>Run</b><span>{{reportRunName .Data.Report}}</span></div>
    <div><b>Environment</b><span>{{reportEnvironment .Data.Report}}</span></div>
    <div><b>Evidence coverage</b><span>{{f0 .Data.Report.Completeness.ScorePct}}% · {{tokenLabel .Data.Report.Completeness.Status}}</span></div>
    {{if eq .Data.Profile.ID "audit"}}<div><b>Artifact integrity</b><span>{{if .Data.Report.Integrity}}{{.Data.Report.Integrity.Status}}{{else}}Not verified{{end}}</span></div>{{end}}
    {{if ne .Data.Profile.ID "audit"}}
    {{with headline .Data.Report}}<div><b>Error rate</b><span>{{formatPercent .ErrorPct}} · {{hint .Errors}} of {{hint .Transactions}} ({{.Label}})</span></div>{{end}}
    <div><b>Peak load</b><span>{{.Data.Report.Overview.PeakActiveVUsers}} VUsers</span></div>
    {{end}}
  </div>
  {{if eq .Data.Profile.ID "executive"}}
    {{with index .Data.Annotations.SectionComments "decision"}}{{if ne . "Not provided"}}<p class="summary-note"><strong>Decision context:</strong> {{.}}</p>{{end}}{{end}}
    {{with index .Data.Annotations.SectionComments "recommendations"}}{{if ne . "Not provided"}}<p class="summary-note"><strong>Recommendation:</strong> {{.}}</p>{{end}}{{end}}
  {{else if eq .Data.Profile.ID "engineer"}}
    {{if .Data.Findings}}<p class="summary-note"><strong>Priority finding:</strong> {{(index .Data.Findings 0).Title}} · <a href="#report-root_cause">Review findings and evidence</a></p>{{end}}
  {{end}}
  {{if .Data.EvidenceGaps}}<div class="evidence-gaps"><h3>Evidence gaps</h3><ul>{{range .Data.EvidenceGaps}}<li>{{.}}</li>{{end}}</ul></div>{{end}}
</section>

<nav class="report-contents" id="report-contents" aria-label="Report contents">
  <h2>Contents</h2>
  <ol>{{range .Sections}}<li><a href="#report-{{.ID}}">{{.Label}}</a></li>{{end}}</ol>
  {{if .NotCaptured}}<p class="not-captured">Not captured for this run: {{join .NotCaptured}}.</p>{{end}}
</nav>

{{range .Sections}}<section class="report-block" id="report-{{.ID}}" data-report-section="{{.ID}}">{{.Body}}<a class="back-link" href="#report-contents">Back to contents</a></section>
{{end}}

</main><!-- .content -->

<footer class="page-footer">
  <span>APEX Load · Performance Test Results Analysis</span>
  <span class="mono">{{.Data.Report.SchemaVersion}} · generated {{formatTime .Data.GeneratedAt}}{{if .Data.Annotations.UpdatedBy}} · commentary by {{.Data.Annotations.UpdatedBy}}{{end}}</span>
</footer>
</div>
</body>
</html>`))

// reportSectionTemplates holds the per-section HTML fragment templates, one
// {{define}} per registered section id. Each fragment is executed with the
// shared *runArchiveHTMLReportData as its dot and always emits at least a
// heading, so no profile-selected section ever renders blank.
var reportSectionTemplates = template.Must(template.New("run-archive-report-sections").Funcs(profileHTMLFuncMap).Parse(`
{{define "verdict"}}
<h2>{{sectionTitle "verdict"}}</h2>
{{if .Report.MetricPopulations}}<table><thead><tr><th>Population</th><th class="num">Count</th><th class="num">Errors</th><th class="num">P95 (ms)</th><th class="num">Average attempts/s (elapsed run)</th></tr></thead><tbody>
{{range $kind, $metrics := .Report.MetricPopulations}}<tr><td>{{if eq $kind "action"}}Requests / actions{{else if eq $kind "transaction"}}Named transactions{{else}}Unclassified legacy records{{end}}</td><td class="num">{{hint $metrics.Transactions}}</td><td class="num">{{hint $metrics.Errors}}</td><td class="num">{{if gt $metrics.Transactions 0}}{{f0 $metrics.P95Ms}}{{else}}No samples{{end}}</td><td class="num">{{populationRate $metrics.AverageRate}}</td></tr>{{end}}
</tbody></table>{{end}}
<p>Execution: {{executionLabel .Report.Manifest.TerminationReason}}. Planned duration: {{.Report.Manifest.Provenance.Duration}}. Elapsed: {{elapsedDuration .Report.Manifest}}. Planned load: {{.Report.Manifest.Provenance.TotalVUsers}} VUsers; observed peak: {{.Report.Overview.PeakActiveVUsers}}. Archive coverage applies to the captured period, not completion of the planned workload.</p>
<div class="verdict">
  <div class="verdict-badge {{if .Verdict.Failed}}fail{{else if .Verdict.Conditional}}conditional{{end}}">{{.Verdict.Status}}</div>
  <div class="verdict-body {{if .Verdict.Failed}}fail{{else if .Verdict.Conditional}}conditional{{end}}">{{.Verdict.Explanation}}</div>
</div>
{{if .AnalystAssessmentInVerdict}}<p class="analyst-assessment">Analyst assessment: {{.Annotations.VerdictOverride}}{{with .Annotations.VerdictExplanation}} — {{.}}{{end}}. Assessment author: {{with .Annotations.VerdictAuthor}}{{.}}{{else}}Not provided{{end}}.</p>{{end}}
{{end}}

{{define "official_outcome"}}
<h2>{{sectionTitle "official_outcome"}}</h2>
<p><span class="outcome-pill {{if .Verdict.Failed}}fail{{else if .Verdict.Conditional}}conditional{{end}}">{{.Verdict.Status}}</span> {{.Verdict.Explanation}}</p>
{{end}}

{{define "executive_overview"}}
<h2>{{sectionTitle "executive_overview"}}</h2>
{{with headline .Report}}<p class="kpi-basis">Headline metrics: {{.Label}}</p>
<div class="kpis">
  <div class="kpi"><b>Peak TPS (attempts/s)</b><div class="v">{{formatFloat .PeakTPS}}</div></div>
  <div class="kpi"><b>P95 latency</b><div class="v">{{fms .P95Ms}}</div></div>
  <div class="kpi {{if gt .ErrorPct 0.0}}warn{{end}}"><b>Error rate</b><div class="v">{{formatPercent .ErrorPct}}</div></div>
  <div class="kpi"><b>Peak VUsers</b><div class="v">{{$.Report.Overview.PeakActiveVUsers}}</div></div>
</div>{{end}}
<div class="analyst">
  {{if .Annotations.ExecutiveNarrative}}<p>{{.Annotations.ExecutiveNarrative}}</p>{{end}}
  {{with index .Annotations.SectionComments "decision"}}<p>{{.}}</p>{{end}}
  {{with index .Annotations.SectionComments "business_impact"}}<p>{{.}}</p>{{end}}
</div>
{{end}}

{{define "technical_overview"}}
<h2>{{sectionTitle "technical_overview"}}</h2>
<div class="analyst">
  {{if .Annotations.ExecutiveNarrative}}<p>{{.Annotations.ExecutiveNarrative}}</p>{{end}}
  {{with index .Annotations.SectionComments "overview"}}<p>{{.}}</p>{{end}}
</div>
<h3>Run at a glance</h3>
{{with headline .Report}}<p class="kpi-basis">Headline metrics: {{.Label}}</p>
<div class="kpis">
  <div class="kpi"><b>Peak TPS (attempts/s)</b><div class="v">{{formatFloat .PeakTPS}}</div><div class="d">{{if gt $.Report.Overview.PeakTargetTPS 0.0}}target {{formatFloat $.Report.Overview.PeakTargetTPS}} TPS{{else}}observed peak{{end}}</div></div>
  <div class="kpi"><b>P95 latency</b><div class="v">{{fms .P95Ms}}</div><div class="d">avg {{fms .AvgResponseMs}}</div></div>
  <div class="kpi {{if gt .ErrorPct 0.0}}warn{{end}}"><b>Error rate</b><div class="v">{{formatPercent .ErrorPct}}</div><div class="d">{{hint .Errors}} of {{hint .Transactions}}</div></div>
  <div class="kpi"><b>Archive records</b><div class="v">{{hint $.Report.Overview.Transactions}}</div><div class="d">peak {{$.Report.Overview.PeakActiveVUsers}} VUsers</div></div>
</div>
<table>
<caption>Table — Run summary ({{.Label}})</caption>
<thead><tr><th>Metric</th><th class="num">Value</th><th>Metric</th><th class="num">Value</th></tr></thead>
<tbody>
<tr><td>P50 / P90 / P99</td><td class="num">{{f0 .P50Ms}} / {{f0 .P90Ms}} / {{f0 .P99Ms}} ms</td><td>Peak concurrent VUsers</td><td class="num">{{$.Report.Overview.PeakActiveVUsers}}</td></tr>
<tr><td>Peak TPS (attempts/s)</td><td class="num">{{formatFloat .PeakTPS}}</td><td>Load generators</td><td class="num">{{if gt $.GeneratorCount 0}}{{$.GeneratorCount}}{{else}}Not recorded{{end}}</td></tr>
<tr><td>Response range</td><td class="num">{{f0 .MinResponseMs}}–{{f0 .MaxResponseMs}} ms</td><td>Evidence coverage</td><td class="num">{{f0 $.Report.Completeness.ScorePct}}%</td></tr>
<tr><td>Interval summaries</td><td class="num">{{hint $.Report.Overview.IntervalSummaries}}</td><td>Resource samples</td><td class="num">{{hint $.Report.Overview.ResourceSamples}}</td></tr>
</tbody></table>{{end}}
{{end}}

{{define "sla_rollup"}}
<h2>{{sectionTitle "sla_rollup"}}</h2>
{{if .TransactionResponseTimeSLAResults}}
<table>
  <caption>Transaction response-time SLA (95% of successful samples required)</caption>
  <thead><tr><th>Group</th><th>Transaction</th><th class="num">Threshold</th><th class="num">Eligible</th><th class="num">Excluded errors</th><th class="num">Missed</th><th class="num">Missed %</th><th>Result</th></tr></thead>
  <tbody>{{range .TransactionResponseTimeSLAResults}}<tr><td>{{.Group}}</td><td>{{if .Transaction}}{{.Transaction}}{{else}}No named transaction observed{{end}}</td><td class="num">{{f0 .ThresholdMs}} ms</td><td class="num">{{hint .EligibleSuccessCount}}</td><td class="num">{{hint .ExcludedErrorCount}}</td><td class="num">{{hint .MissedCount}}</td><td class="num">{{formatPercent .MissedPct}}</td><td><span class="pill {{.Status}}">{{.Status}}</span></td></tr>{{end}}</tbody>
</table>
{{end}}
<table>
  <thead><tr><th>Transaction</th><th class="num">Volume</th><th class="num">P95 (ms)</th><th class="num">SLA (ms)</th><th>Result</th></tr></thead>
  <tbody>
  {{range .SLARows}}
  <tr><td>{{.Name}}</td><td class="num">{{hint .Volume}}</td><td class="num">{{f0 .P95Ms}}</td><td class="num">{{if .HasSLA}}{{f0 .SLAMs}}{{else}}—{{end}}</td><td><span class="pill {{.ResultKind}}">{{.Result}}</span></td></tr>
  {{else}}<tr><td colspan="5" class="muted">No per-transaction SLA evidence was archived.</td></tr>{{end}}
  </tbody>
</table>
{{if .TransactionRuleResults}}
<table>
  <caption>Exact transaction terminal rules</caption>
  <thead><tr><th>Transaction</th><th>Metric</th><th>Expectation</th><th class="num">Observed</th><th>Severity</th><th>Result</th></tr></thead>
  <tbody>{{range .TransactionRuleResults}}<tr><td>{{.Rule.Transaction}}{{with .Rule.Population}} · {{.}}{{end}}{{with .Rule.Protocol}} · {{.}}{{end}}{{with .Rule.Group}} · {{.}}{{end}}</td><td>{{.Rule.Metric}}</td><td>{{.Rule.Comparator}} {{formatFloat .Rule.Threshold}}</td><td class="num">{{optFloat .Observed}}</td><td>{{.Rule.Severity}}</td><td>{{.Status}}</td></tr>{{end}}</tbody>
</table>
{{end}}
{{with index .Annotations.SectionComments "sla"}}<div class="analyst"><p>{{.}}</p></div>{{end}}
{{end}}

{{define "sla_detail"}}
<h2>{{sectionTitle "sla_detail"}}</h2>
{{if .TransactionResponseTimeSLAResults}}
<table>
<caption>Transaction response-time SLA by group and named business transaction</caption>
<thead><tr><th>Group</th><th>Transaction</th><th class="num">Threshold (ms)</th><th class="num">Total</th><th class="num">Eligible successes</th><th class="num">Excluded errors</th><th class="num">Missed</th><th class="num">Missed %</th><th class="num">Met %</th><th class="num">Required met %</th><th>Result</th></tr></thead>
<tbody>{{range .TransactionResponseTimeSLAResults}}<tr><td>{{.Group}}</td><td>{{if .Transaction}}{{.Transaction}}{{else}}No named transaction observed{{end}}</td><td class="num">{{f0 .ThresholdMs}}</td><td class="num">{{hint .TotalCount}}</td><td class="num">{{hint .EligibleSuccessCount}}</td><td class="num">{{hint .ExcludedErrorCount}}</td><td class="num">{{hint .MissedCount}}</td><td class="num">{{formatPercent .MissedPct}}</td><td class="num">{{formatPercent .MetPct}}</td><td class="num">{{formatPercent .RequiredMetPct}}</td><td><span class="pill {{.Status}}">{{.Status}}</span></td></tr>{{end}}</tbody>
</table>
{{end}}
<table>
<caption>SLA evaluation by transaction (P95 vs configured latency SLA; full results in run archive)</caption>
<thead><tr><th>Transaction</th><th class="num">Volume</th><th class="num">P95 (ms)</th><th class="num">SLA (ms)</th><th class="num">Error rate</th><th>Result</th></tr></thead>
<tbody>
{{range .SLARows}}
<tr><td>{{.Name}}</td><td class="num">{{hint .Volume}}</td><td class="num">{{f0 .P95Ms}}</td><td class="num">{{if .HasSLA}}{{f0 .SLAMs}}{{else}}—{{end}}</td><td class="num">{{formatPercent .ErrorPct}}</td><td><span class="pill {{.ResultKind}}">{{.Result}}</span></td></tr>
{{else}}
<tr><td colspan="6" class="muted">No per-transaction archive data was recorded.</td></tr>
{{end}}
</tbody></table>
{{if .TransactionRuleResults}}
<table>
<caption>Exact transaction terminal rules</caption>
<thead><tr><th>Transaction</th><th>Metric</th><th>Expectation</th><th class="num">Observed</th><th>Severity</th><th>Result</th></tr></thead>
<tbody>{{range .TransactionRuleResults}}<tr><td>{{.Rule.Transaction}}{{with .Rule.Population}} · {{.}}{{end}}{{with .Rule.Protocol}} · {{.}}{{end}}{{with .Rule.Group}} · {{.}}{{end}}</td><td>{{.Rule.Metric}}</td><td>{{.Rule.Comparator}} {{formatFloat .Rule.Threshold}}</td><td class="num">{{optFloat .Observed}}</td><td>{{.Rule.Severity}}</td><td>{{.Status}}</td></tr>{{end}}</tbody>
</table>
{{end}}
{{with index .Annotations.SectionComments "sla"}}<div class="analyst"><p>{{.}}</p></div>{{end}}
{{end}}

{{define "graphs"}}
<h2>{{sectionTitle "graphs"}}</h2>
{{if or .Charts.HasTimeline .Charts.HasActions}}
<div class="charts">
  {{if .Charts.HasTimeline}}{{.Charts.Throughput}}{{.Charts.ResponseTime}}{{.Charts.ErrorRate}}{{.Charts.VUsers}}{{end}}
  {{if .Charts.HasActions}}{{.Charts.ActionLatency}}{{end}}
</div>
{{else}}
<p class="muted">No interval timeline was captured for this run, so time-series graphs are unavailable.</p>
{{end}}
{{with index .Annotations.SectionComments "performance"}}<div class="analyst"><p>{{.}}</p></div>{{end}}
{{end}}

{{define "risk_summary"}}
<h2>{{sectionTitle "risk_summary"}}</h2>
{{range .Findings}}
  <div class="finding"><div class="finding-head {{if eq .Severity "major"}}major{{end}}"><span class="fid">{{.ID}}</span><span class="ft">{{.Title}}</span><span class="pill {{sevPill .Severity}}">{{sevLabel .Severity}}</span></div>
  <div class="finding-body">{{if .Impact}}<p>{{.Impact}}</p>{{else if .Observed}}<p>{{.Observed}}</p>{{end}}</div></div>
{{else}}<p class="muted">No material release risks were derived from the archive.</p>{{end}}
{{with index .Annotations.SectionComments "risks"}}<div class="analyst"><p>{{.}}</p></div>{{end}}
{{end}}

{{define "rca"}}
<h2>{{sectionTitle "rca"}}</h2>
{{with .RootCause}}
<p class="muted">Generated from archived evidence{{if ne $.Profile.ID "executive"}} · {{rootCauseSource .}}{{end}}. Analyst findings are reported separately.</p>
<p><strong>{{rootCauseOutcome .Outcome}}.</strong> {{.Summary}}</p>
{{if .Validity}}<p>Run validity: {{rootCauseOutcome .Validity}}{{range .ValidityReasons}} · {{.}}{{end}}</p>{{end}}
{{range .Notes}}<p class="muted">{{.}}</p>{{end}}
{{else}}<p class="muted">No generated root-cause analysis is available for this run.</p>{{end}}
{{range .RootCauseHypotheses}}
<div class="finding">
  <div class="finding-head"><span class="fid">#{{.Rank}}</span><span class="ft">{{.Title}}</span>{{if .Confidence}}<span class="pill">{{.Confidence}} confidence</span>{{else if .Severity}}<span class="pill">{{rootCauseOutcome .Severity}}</span>{{end}}</div>
  <div class="finding-body"><dl>
    {{if .Explains}}<dt>Explains</dt><dd>{{join .Explains}}</dd>{{end}}
    {{if .Detail}}<dt>Detail</dt><dd>{{.Detail}}</dd>{{end}}
    {{range .Chain}}<dt>Evidence</dt><dd>{{.}}</dd>{{end}}
    {{if .Reasons}}<dt>Confidence</dt><dd><ul>{{range .Reasons}}<li>{{.}}</li>{{end}}</ul></dd>{{end}}
    {{if $.RootCause}}{{if eq $.RootCause.Source "v2"}}<dt>Contradicting evidence</dt><dd>{{if .Contradicting}}{{join .Contradicting}}{{else}}None found{{end}}</dd>
    <dt>Would disprove it</dt><dd>{{if .Falsification}}{{.Falsification}}{{else}}Not provided{{end}}</dd>{{end}}{{end}}
    {{if .Competing}}<dt>Also supported</dt><dd>{{join .Competing}}</dd>{{end}}
    {{range .Limitations}}<dt>Limitation</dt><dd>{{.}}</dd>{{end}}
  </dl></div>
</div>
{{end}}
{{if .RootCause}}{{if gt (len .RootCause.Hypotheses) (len .RootCauseHypotheses)}}<p class="muted">{{sub (len .RootCause.Hypotheses) (len .RootCauseHypotheses)}} more ranked in the Engineer report.</p>{{end}}
{{with .RootCause.BrowserBaseline}}<div class="browser-baseline"><p>{{.Summary}}</p>{{if .Findings}}<ul>{{range .Findings}}<li>{{.}}</li>{{end}}</ul>{{end}}{{if .Qualification}}<p class="muted">{{range $i, $q := .Qualification}}{{if $i}}; {{end}}{{$q}}{{end}}.</p>{{end}}{{range .Limitations}}<p class="muted">Limitation: {{.}}</p>{{end}}</div>{{end}}
{{if .RootCause.Limitations}}<h3>Limitations</h3><ul>{{range .RootCause.Limitations}}<li>{{.}}</li>{{end}}</ul>{{end}}{{end}}
{{end}}

{{define "root_cause"}}
<h2>{{sectionTitle "root_cause"}}</h2>
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
{{with index .Annotations.SectionComments "bottlenecks"}}<div class="analyst"><p>{{.}}</p></div>{{end}}
{{with index .Annotations.SectionComments "errors"}}<div class="analyst"><p>{{.}}</p></div>{{end}}
{{end}}

{{define "recommendations"}}
<h2>{{sectionTitle "recommendations"}}</h2>
<div class="analyst">
  {{with index .Annotations.SectionComments "recommendations"}}<p>{{.}}</p>{{else}}<p class="muted">Document recommended follow-up actions, owners, target dates, retest scope, or accepted risks here.</p>{{end}}
</div>
{{end}}

{{define "signoff"}}
<h2>{{sectionTitle "signoff"}}</h2>
{{if .Annotations.VerdictOverride}}<p>Analyst assessment: {{.Annotations.VerdictOverride}} — {{.Annotations.VerdictExplanation}}</p><p>Assessment author: {{with .Annotations.VerdictAuthor}}{{.}}{{else}}Not yet recorded{{end}}</p>{{else}}<p class="muted">No analyst assessment has been recorded.</p>{{end}}
<div class="signoff">
  <div class="sign-cell"><b>Prepared by</b><div class="sign-line"></div><div class="sign-meta">{{if .Annotations.PreparedBy}}{{.Annotations.PreparedBy}}{{else}}Name and date{{end}}</div></div>
  <div class="sign-cell"><b>Approver</b><div class="sign-line"></div><div class="sign-meta">Name and date</div></div>
</div>
{{end}}

{{define "release_trend"}}
<h2>{{sectionTitle "release_trend"}}</h2>
{{with .Report.ReleaseTrend}}
<p class="muted">Release: {{if .ReleaseName}}{{.ReleaseName}}{{else}}{{.ReleaseID}}{{end}}.</p>
<p class="muted">Metric population: {{if eq .Current.MetricPopulation "transaction"}}named transaction attempts{{else}}Unknown (legacy archive records){{end}}.</p>
  {{if .HasBaseline}}
  <p class="muted">This run vs the prior run in release <strong>{{if .ReleaseName}}{{.ReleaseName}}{{else}}{{.ReleaseID}}{{end}}</strong> (baseline {{if .Baseline.RunName}}{{.Baseline.RunName}}{{else}}{{.Baseline.RunID}}{{end}}{{if not .Baseline.CompletedAt.IsZero}}, completed {{formatTime .Baseline.CompletedAt}}{{end}}; {{.RunCount}} archived {{if eq .RunCount 1}}run{{else}}runs{{end}}).</p>
  <table>
    <thead><tr><th>Metric</th><th class="num">Prior run</th><th class="num">This run</th><th class="num">Change</th></tr></thead>
    <tbody>
      <tr><td>Error rate</td><td class="num">{{formatPercent .Baseline.ErrorPct}}</td><td class="num">{{formatPercent .Current.ErrorPct}}</td><td class="num">{{signed .Deltas.ErrorPctDelta}} pts</td></tr>
      <tr><td>Avg latency</td><td class="num">{{f0 .Baseline.AvgResponseMs}} ms</td><td class="num">{{f0 .Current.AvgResponseMs}} ms</td><td class="num">{{signed .Deltas.AvgResponseMsDelta}} ms ({{signedPct .Deltas.AvgResponsePctDelta}})</td></tr>
      <tr><td>Peak TPS</td><td class="num">{{formatFloat .Baseline.PeakTPS}}</td><td class="num">{{formatFloat .Current.PeakTPS}}</td><td class="num">{{signed .Deltas.PeakTPSDelta}}</td></tr>
      <tr><td>Evidence coverage</td><td class="num">{{f0 .Baseline.CompletenessPct}}%</td><td class="num">{{f0 .Current.CompletenessPct}}%</td><td class="num">{{signed .Deltas.CompletenessDelta}} pts</td></tr>
    </tbody>
  </table>
  {{if .Regressed}}<div class="finding"><div class="finding-body"><strong>Regressions vs prior run</strong><p>{{join .Regressions}}</p></div></div>{{else}}<p class="muted">No regressions detected versus the prior run.</p>{{end}}
  {{else}}
  <p class="muted">{{if .Note}}{{.Note}}{{else}}First archived run for this release; no prior run to compare against.{{end}}</p>
  {{end}}
{{else}}
<p class="muted">This run is not associated with a release, so release-over-release trend analysis is unavailable.</p>
{{end}}
{{end}}

{{define "dashboard_panels"}}
<h2>{{sectionTitle "dashboard_panels"}}</h2>
{{with .Report.DashboardPanels}}
  {{range .Dashboards}}
  <h3>{{if .Title}}{{.Title}}{{else}}Dashboard{{end}}</h3>
  <table>
    <thead><tr><th>Panel</th><th>Unit</th><th class="num">Min</th><th class="num">Avg</th><th class="num">Max</th><th class="num">Last / Value</th></tr></thead>
    <tbody>
      {{range .Panels}}
      {{if .Error}}
      <tr><td>{{.Title}}</td><td colspan="5" class="muted">error: {{.Error}}</td></tr>
      {{else if eq .Kind "single_value"}}
      <tr><td>{{.Title}}</td><td>{{.Unit}}</td><td class="num">—</td><td class="num">—</td><td class="num">—</td><td class="num">{{optFloat .Value}}</td></tr>
      {{else}}
      <tr><td>{{.Title}}</td><td>{{.Unit}}</td><td class="num">{{optFloat .Min}}</td><td class="num">{{optFloat .Avg}}</td><td class="num">{{optFloat .Max}}</td><td class="num">{{optFloat .Last}}</td></tr>
      {{end}}
      {{end}}
    </tbody>
  </table>
  {{end}}
{{else}}
<p class="muted">No imported Grafana dashboards were captured for this run.</p>
{{end}}
{{end}}

{{define "run_context"}}
<h2>{{sectionTitle "run_context"}}</h2>
<div class="kpis">
  <div class="kpi"><b>Evidence coverage</b><div class="v">{{f0 .Report.Completeness.ScorePct}}%</div></div>
  <div class="kpi"><b>Environment</b><div class="v">{{reportEnvironment .Report}}</div></div>
  <div class="kpi"><b>Runtime overrides</b><div class="v">{{if .Report.ChangeAttribution.RuntimeOverrides}}Yes{{else}}No{{end}}</div></div>
</div>
{{end}}

{{define "controller_failover"}}
<h2>{{sectionTitle "controller_failover"}}</h2>
{{with .Report.Manifest.Continuity}}{{if .Failovers}}
<p class="section-copy">This run survived {{len .Failovers}} HA controller {{if eq (len .Failovers) 1}}failover{{else}}failovers{{end}}. A standby controller adopted the run. Generator archives provide retained measurements; completeness and missing sources are evaluated separately. During each gap the controller observed nothing live: controller-derived overlays (target TPS, telemetry health) are absent for those windows, and controller-driven shapes (stage schedules, traffic profiles, goal control) held their last value until adoption.</p>
<table>
  <thead><tr><th>Adopted at</th><th>Generation</th><th class="num">Unobserved gap (s)</th><th class="num">Generators adopted</th><th class="num">Finished during gap</th></tr></thead>
  <tbody>
    {{range .Failovers}}
    <tr><td>{{formatTime .AdoptedAt}}</td><td>{{.PriorGeneration}} → {{.Generation}}</td><td class="num">{{if .GapSeconds}}{{formatFloat .GapSeconds}}{{else}}n/a{{end}}</td><td class="num">{{.AdoptedGenerators}}</td><td class="num">{{.CompletedGenerators}}</td></tr>
    {{end}}
  </tbody>
</table>
{{else}}<p class="section-copy">No controller failover occurred during this run.</p>{{end}}
{{else}}<p class="section-copy">No controller failover occurred during this run.</p>{{end}}
{{end}}

{{define "telemetry_acceptance"}}
<h2>{{sectionTitle "telemetry_acceptance"}}</h2>
{{with .TelemetryAcceptanceView}}
<p class="section-copy">{{.Summary}}</p>
{{if .Pins}}<table><thead><tr><th>Policy</th><th>Name</th><th class="num">Revision</th><th>SHA-256</th></tr></thead><tbody>
{{range .Pins}}<tr><td>{{telemetryLabel .Kind}}</td><td>{{.Name}}</td><td class="num">{{.Revision}}</td><td>{{.SHA256}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .Criteria}}<table><thead><tr><th>Criterion</th><th>Result</th><th>Observed</th><th>Bound</th><th>Threshold</th><th>Basis</th><th>Reason</th></tr></thead><tbody>
{{range .Criteria}}<tr><td>{{.CriterionLabel}}</td><td>{{.OutcomeLabel}}</td><td>{{.Observed}}</td><td>{{.BoundLabel}}</td><td>{{.Threshold}}</td><td>{{.BasisLabel}}</td><td>{{.Reason}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .Loss}}<table><thead><tr><th>Loss counter</th><th class="num">Total</th><th>State</th><th>Reason</th></tr></thead><tbody>
{{range .Loss}}<tr><td>{{.Label}}</td><td>{{.Value}}</td><td>{{.StateLabel}}</td><td>{{.ReasonLabel}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="section-copy">Run-level loss totals were not recorded for this run.</p>{{end}}
<p class="section-copy">Coverage: {{.Coverage}}</p>
{{if .Sources}}<table><thead><tr><th>Source</th><th>Observed</th></tr></thead><tbody>
{{range .Sources}}<tr><td>{{.Source}}</td><td>{{.Observed}}</td></tr>{{end}}
</tbody></table>{{end}}
<p class="section-copy">Classified time: {{.Breach}}</p>
<p class="section-copy">Clock quality: {{.Clock}}</p>
{{if .Stages}}<table><thead><tr><th>Stage</th><th>Availability</th><th class="num">Samples</th><th class="num">P50 (ms)</th><th class="num">P95 (ms)</th><th class="num">P99 (ms)</th><th class="num">Max (ms)</th><th>Excluded</th></tr></thead><tbody>
{{range .Stages}}<tr><td>{{.Label}}</td><td>{{.AvailabilityLabel}}</td><td class="num">{{.SamplesLabel}}</td><td class="num">{{.P50}}</td><td class="num">{{.P95}}</td><td class="num">{{.P99}}</td><td class="num">{{.Max}}</td><td>{{.ExcludedLabel}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .Exclusions}}<ul>{{range .Exclusions}}<li>Excluded — {{telemetryExclusion .}}</li>{{end}}</ul>{{end}}
{{end}}
{{end}}

{{define "browser_findings"}}
<h2>{{sectionTitle "browser_findings"}}</h2>
{{with .BrowserFindingsView}}
<p class="section-copy">{{.Summary}}</p>
{{if .Findings}}<table><thead><tr><th>Finding</th><th>State</th><th>Severity</th><th>Observed</th><th>Summary</th></tr></thead><tbody>
{{range .Findings}}<tr><td>{{.Title}}</td><td>{{.State}}</td><td>{{.Severity}}</td><td>{{.Observed}}</td><td>{{.Summary}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .More}}<p class="section-copy">{{.More}} more findings are in the run report.</p>{{end}}
{{if .Notes}}<ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{end}}
{{end}}

{{define "interaction_map"}}
<h2>{{sectionTitle "interaction_map"}}</h2>
{{with .InteractionMapView}}
<p class="section-copy">{{.Summary}}</p>
{{if .Patterns}}<table><thead><tr><th>Pattern</th><th>Provenance</th><th>Route</th><th>Action</th><th class="num">Gestures</th><th>Detail</th></tr></thead><tbody>
{{range .Patterns}}<tr><td>{{.Kind}}</td><td>{{.Provenance}}</td><td>{{.Route}}</td><td>{{.Action}}</td><td class="num">{{.Count}}</td><td>{{.Detail}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .More}}<p class="section-copy">{{.More}} more patterns are in the run report.</p>{{end}}
{{if .Heatmaps}}<table><thead><tr><th>Route</th><th>Layout</th><th>Viewport</th><th>Engine</th><th>Profile</th><th class="num">Gestures</th><th>Occupied cells</th></tr></thead><tbody>
{{range .Heatmaps}}<tr><td>{{.Route}}</td><td>{{.Layout}}</td><td>{{.Viewport}}</td><td>{{.Engine}}</td><td>{{.Profile}}</td><td class="num">{{.Gestures}}</td><td>{{.Cells}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .MoreMaps}}<p class="section-copy">{{.MoreMaps}} more heatmaps are in the run report.</p>{{end}}
{{if .Notes}}<ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{end}}
{{end}}

{{define "journey_risk"}}
<h2>{{sectionTitle "journey_risk"}}</h2>
{{with .JourneyRiskView}}
<p class="section-copy">{{.Summary}}</p>
{{if .Journeys}}<table><thead><tr><th>Journey</th><th class="num">Eligible</th><th class="num">Completed</th><th class="num">Failed</th><th class="num">Aborted</th><th class="num">Skipped</th><th class="num">Censored</th><th class="num">Failure rate</th></tr></thead><tbody>
{{range .Journeys}}<tr><td>{{.Name}}{{if ne .Name .ID}} <span class="muted">({{.ID}})</span>{{end}}</td><td class="num">{{.Eligible}}</td><td class="num">{{.Completed}}</td><td class="num">{{.Failed}}</td><td class="num">{{.Aborted}}</td><td class="num">{{.Skipped}}</td><td class="num">{{.Censored}}</td><td class="num">{{.FailureRate}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .Notes}}<ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{end}}
{{end}}

{{define "completeness"}}
<h2>{{sectionTitle "completeness"}}</h2>
<p class="section-copy">{{f0 .Report.Completeness.ScorePct}}% of expected archive evidence was captured — {{tokenLabel .Report.Completeness.Status}}.</p>
{{with .Report.Manifest}}{{with .SourceCoverage}}
<p class="section-copy">Generator sources known at collection time. This inventory does not prove complete shard history or that all measurements survived.</p>
<table><thead><tr><th>Generator source</th><th>Collection</th></tr></thead><tbody>
{{range .Sources}}<tr><td>{{.Target}}</td><td>{{if .Collected}}Collected{{else}}Not collected{{end}}</td></tr>{{end}}
</tbody></table>
{{end}}{{end}}
{{if .Report.Completeness.Findings}}
<table>
  <thead><tr><th>Dimension</th><th>Severity</th><th class="num">Penalty</th><th>Finding</th></tr></thead>
  <tbody>
    {{range .Report.Completeness.Findings}}
    <tr><td>{{tokenLabel .Dimension}}</td><td>{{tokenLabel .Severity}}</td><td class="num">-{{formatFloat .Penalty}}</td><td>{{.Message}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{end}}

{{define "change_attribution"}}
<h2>{{sectionTitle "change_attribution"}}</h2>
<div class="overview">
  <div class="metric"><span class="metric-label">Scripts</span><span class="metric-text">{{reportScripts .Report}}</span></div>
  <div class="metric"><span class="metric-label">Scenario</span><span class="metric-text mono">{{if .Report.ChangeAttribution.ScenarioRevision}}{{.Report.ChangeAttribution.ScenarioRevision}}{{else}}Not recorded{{end}}</span></div>
  <div class="metric"><span class="metric-label">Environment</span><span class="metric-text">{{reportEnvironment .Report}}</span></div>
  <div class="metric"><span class="metric-label">Generators</span><span class="metric-text">{{if .Report.ChangeAttribution.GeneratorVersions}}{{join .Report.ChangeAttribution.GeneratorVersions}}{{else}}Not recorded{{end}}</span></div>
</div>
<p class="section-copy">Runtime overrides {{if .Report.ChangeAttribution.RuntimeOverrides}}were applied{{if .Report.ChangeAttribution.RuntimeOverrideFields}} to: {{join .Report.ChangeAttribution.RuntimeOverrideFields}}{{end}}{{else}}were not applied{{end}}.</p>
{{end}}

{{define "schema_compatibility"}}
<h2>{{sectionTitle "schema_compatibility"}}</h2>
<p class="section-copy">Report schema {{.Report.SchemaVersion}} is {{.Report.Compatibility.Status}} with archive manifest {{.Report.Compatibility.ManifestSchemaVersion}}.</p>
<table>
  <thead><tr><th>Output</th><th>Artifact</th><th>Status</th><th>Schema</th></tr></thead>
  <tbody>
    {{range .Report.Compatibility.Outputs}}
    <tr><td>{{.Format}}</td><td>{{if .Artifact}}{{.Artifact}}{{else}}n/a{{end}}</td><td>{{.Status}}</td><td>{{if .SchemaVersion}}{{.SchemaVersion}}{{else}}n/a{{end}}</td></tr>
    {{else}}<tr><td colspan="4" class="muted">No output compatibility was recorded.</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}

{{define "summary_timeline"}}
<h2>{{sectionTitle "summary_timeline"}}</h2>
{{if .Report.SummaryTimeline}}
<table>
  <thead><tr><th class="nowrap">Time (UTC)</th><th class="num">Active VUsers</th><th class="num">TPS</th><th class="num">Target TPS</th><th class="num">Gap</th><th class="num">P95 (ms)</th><th class="num">Error rate</th><th class="num">Transactions</th><th class="num">Errors</th></tr></thead>
  <tbody>
    {{range .Report.SummaryTimeline}}
    <tr><td class="nowrap">{{clockTime .Timestamp}}</td><td class="num">{{.ActiveVUsers}}</td><td class="num">{{formatFloat .TPS}}</td><td class="num">{{formatFloat .TargetTPS}}</td><td class="num"><span class="pill {{if .ThroughputTargetMissed}}fail{{else}}pass{{end}}">{{formatPercent .ThroughputGapPct}}</span></td><td class="num">{{if .HasP95}}{{f0 .P95Ms}}{{else}}Not recorded{{end}}</td><td class="num">{{formatPercent .ErrorPct}}</td><td class="num">{{hint .TotalTx}}</td><td class="num">{{hint .TotalErrors}}</td></tr>
    {{end}}
  </tbody>
</table>
{{else}}<p class="muted">No interval timeline was captured for this run.</p>{{end}}
{{end}}

{{define "actions"}}
<h2>{{sectionTitle "actions"}}</h2>
<table>
  <thead><tr><th>Action</th><th class="num">Records</th><th class="num">Errors</th><th class="num">Error rate</th><th class="num">Avg (ms)</th><th class="num">Max (ms)</th></tr></thead>
  <tbody>
    {{range .ActionRows}}
    <tr><td>{{.Name}}</td><td class="num">{{hint .Stats.Transactions}}</td><td class="num">{{hint .Stats.Errors}}</td><td class="num">{{formatPercent .Stats.ErrorPct}}</td><td class="num">{{msValue .Stats.AvgResponseMs}}</td><td class="num">{{msValue .Stats.MaxResponseMs}}</td></tr>
    {{else}}
    <tr><td colspan="6" class="muted">No action-level archive data was recorded.</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}

{{define "network_phases"}}
<h2>{{sectionTitle "network_phases"}}</h2>
{{if gt .Report.NetworkPhases.Samples 0}}
<p class="section-copy">Averages across {{hint .Report.NetworkPhases.Samples}} measured requests and transactions; a new connection is not opened for every request, so averages sit well below a single connection's time. Root-cause analysis compares each interval with its own baseline instead.</p>
<table>
  <thead><tr><th>Phase</th><th class="num">Avg (ms)</th><th class="num">Max (ms)</th></tr></thead>
  <tbody>
    <tr><td>DNS</td><td class="num">{{msValue .Report.NetworkPhases.Overall.AvgDNSMs}}</td><td class="num">{{msValue .Report.NetworkPhases.Overall.MaxDNSMs}}</td></tr>
    <tr><td>Connect (TCP)</td><td class="num">{{msValue .Report.NetworkPhases.Overall.AvgConnectMs}}</td><td class="num">{{msValue .Report.NetworkPhases.Overall.MaxConnectMs}}</td></tr>
    <tr><td>TLS handshake</td><td class="num">{{msValue .Report.NetworkPhases.Overall.AvgTLSMs}}</td><td class="num">{{msValue .Report.NetworkPhases.Overall.MaxTLSMs}}</td></tr>
    <tr><td>TTFB (server + transfer)</td><td class="num">{{msValue .Report.NetworkPhases.Overall.AvgTTFBMs}}</td><td class="num">{{msValue .Report.NetworkPhases.Overall.MaxTTFBMs}}</td></tr>
  </tbody>
</table>
{{with versionPhaseRows .Report.NetworkPhases}}
<h3>Phases by negotiated HTTP version</h3>
<table>
  <thead><tr><th>Version</th><th class="num">Samples</th><th class="num">DNS avg (ms)</th><th class="num">Connect avg (ms)</th><th class="num">TLS avg (ms)</th><th class="num">TTFB avg (ms)</th><th class="num">TTFB max (ms)</th></tr></thead>
  <tbody>
    {{range .}}<tr><td>{{.Label}}</td><td class="num">{{.Phases.Count}}</td><td class="num">{{f0 .Phases.AvgDNSMs}}</td><td class="num">{{f0 .Phases.AvgConnectMs}}</td><td class="num">{{f0 .Phases.AvgTLSMs}}</td><td class="num">{{f0 .Phases.AvgTTFBMs}}</td><td class="num">{{f0 .Phases.MaxTTFBMs}}</td></tr>{{end}}
  </tbody>
</table>
{{if (index $.Report.NetworkPhases.ByHTTPVersion "3").Count}}<p class="section-copy">HTTP/3 connect time includes the QUIC handshake, which carries TLS, so HTTP/3 has no separate TLS time.</p>{{end}}
{{end}}
{{else}}<p class="muted">No network-phase timing was captured for this run.</p>{{end}}
{{with .Report.NetworkPhases.VersionMix}}
<h3>HTTP version mix</h3>
<p class="section-copy">Every HTTP action, by the version its response used. Cache hits and requests that got no response are counted separately.</p>
<table>
  <thead><tr><th>Version</th><th class="num">Actions</th><th class="num">Share</th></tr></thead>
  <tbody>
    {{range versionMixRows .}}<tr><td>{{.Label}}</td><td class="num">{{.Count}}</td><td class="num">{{formatPercent .SharePct}}</td></tr>{{end}}
  </tbody>
</table>
{{if .UsedHTTP3}}
<table>
  <thead><tr><th>Ratio</th><th>Observed</th></tr></thead>
  <tbody>
    <tr><td>Fell back to TCP</td><td>{{ratioText .FallbackRatio "upgrade-eligible auto+h3 actions"}}</td></tr>
    <tr><td>QUIC failures</td><td>{{ratioText .QUICFailureRatio "QUIC attempts"}}</td></tr>
  </tbody>
</table>
{{end}}
{{with countText .BySelection}}<p class="section-copy">Routing: {{.}}</p>{{end}}
{{if .Attempts}}<p class="section-copy">HTTP attempts recorded by HTTP/3 actions: {{.Attempts}}.</p>{{end}}
{{with .Finding}}<div class="finding"><div class="finding-head"><span class="pill warn">Minor</span><span class="ft">Protocol Mix</span></div><div class="finding-body"><p>{{.Summary}}</p></div></div>{{end}}
{{end}}
{{with .Report.HostNetwork}}
<h3>Load generator UDP</h3>
<p class="section-copy">Each execution process's open-file limit and UDP buffer maxima, and how much its network namespace's UDP error counters grew between execution start and end.</p>
<table>
  <thead><tr><th>Process</th><th class="num">Open files</th><th>UDP buffer max (receive / send)</th><th>UDP drops during the run</th><th>Note</th></tr></thead>
  <tbody>
    {{range hostNetworkRows .}}<tr><td>{{.Source}}</td><td class="num">{{.OpenFiles}}</td><td>{{.Buffers}}</td><td>{{.Drops}}</td><td>{{.Note}}</td></tr>{{end}}
  </tbody>
</table>
{{end}}
{{with .Report.HTTP3Preflight}}
<h3>HTTP/3 preflight</h3>
<p class="section-copy">{{http3PreflightSummary .}}</p>
<table>
  <thead><tr><th>Load source</th><th>Group</th><th>Origin</th><th class="nowrap">Policy</th><th>Result</th><th>Detail</th></tr></thead>
  <tbody>
    {{range http3PreflightRows .}}<tr><td>{{.Source}}</td><td>{{.Group}}</td><td>{{.Origin}}</td><td class="nowrap">{{.Policy}}</td><td>{{.Result}}</td><td>{{.Detail}}</td></tr>{{end}}
  </tbody>
</table>
{{end}}
{{end}}

{{define "errors"}}
<h2>{{sectionTitle "errors"}}</h2>
{{if .Report.ErrorPatterns}}
<table>
  <thead><tr><th>Pattern</th><th class="num">Occurrences</th><th>First seen</th><th>Latest seen</th><th>Actions</th><th>Snapshot</th></tr></thead>
  <tbody>
    {{range .Report.ErrorPatterns}}
    <tr><td>{{.Sample}}</td><td class="num">{{hint .Count}}</td><td>{{formatTime .FirstSeen}}</td><td>{{formatTime .LatestSeen}}</td><td>{{if .Actions}}{{join .Actions}}{{else}}n/a{{end}}</td><td>{{if .Snapshot}}retained{{else}}n/a{{end}}</td></tr>
    {{end}}
  </tbody>
</table>
{{else}}<p class="muted">No clustered error patterns were recorded.</p>{{end}}
{{end}}

{{define "resources"}}
<h2>{{sectionTitle "resources"}}</h2>
<div class="overview">
  <div class="metric"><span class="metric-label">CPU peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.CPUProcessPct}}%</span></div>
  <div class="metric"><span class="metric-label">Memory peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.MemProcessMB}} MB</span></div>
  <div class="metric"><span class="metric-label">Network sent peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.NetSentKbps}} KB/s</span></div>
  <div class="metric"><span class="metric-label">Network received peak</span><span class="metric-value">{{formatFloat .Report.ResourcePeaks.NetRecvKbps}} KB/s</span></div>
</div>
{{end}}

{{define "browser"}}
<h2>{{sectionTitle "browser"}}</h2>
{{if .Report.Overview.Browser}}
<p>{{legacyBrowserSummaryNote}}</p>
<div class="overview">
  <div class="metric"><span class="metric-label">Samples</span><span class="metric-value">{{.Report.Overview.Browser.Samples}}</span></div>
  <div class="metric"><span class="metric-label">Avg FCP</span><span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgFCPMs}} ms</span></div>
  <div class="metric"><span class="metric-label">Avg LCP</span><span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgLCPMs}} ms</span></div>
  <div class="metric"><span class="metric-label">Avg CLS</span><span class="metric-value">{{formatScore .Report.Overview.Browser.AvgCLSScore}}</span></div>
  <div class="metric"><span class="metric-label">Avg INP</span><span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgINPMs}} ms</span></div>
</div>
{{if .BrowserEngineRows}}
<h3>Engine rollup</h3>
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
<h3>Platform rollup</h3>
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
<h3>Device rollup</h3>
<table>
  <thead><tr><th>Device</th><th>Platform</th><th>Engine</th><th>Tier</th><th class="num">Avg LCP</th><th class="num">Avg INP</th></tr></thead>
  <tbody>
    {{range .BrowserDeviceRows}}
    <tr><td>{{.DeviceProfile}}</td><td>{{.DevicePlatform}}</td><td>{{.BrowserEngine}}</td><td>{{.DeviceTier}}</td><td class="num">{{formatFloat .AvgLCPMs}} ms</td><td class="num">{{formatFloat .AvgINPMs}} ms</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{else}}<p class="muted">No browser metrics were captured for this run.</p>{{end}}
{{end}}

{{define "soap"}}
<h2>{{sectionTitle "soap"}}</h2>
{{if or .SOAPOperationRows .SOAPFaultCodeRows .SOAPFaultBuckets .SOAPPayloadBuckets}}
{{if .SOAPOperationRows}}
<h3>SOAP operation summary</h3>
<table>
  <thead><tr><th>Operation</th><th class="num">Request / action records</th><th class="num">Errors</th><th class="num">Error %</th><th class="num">Avg (ms)</th></tr></thead>
  <tbody>
    {{range .SOAPOperationRows}}
    <tr><td>{{.Name}}</td><td class="num">{{.Stats.Transactions}}</td><td class="num">{{.Stats.Errors}}</td><td class="num">{{formatPercent .Stats.ErrorPct}}</td><td class="num">{{formatFloat .Stats.AvgResponseMs}}</td></tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{if .SOAPFaultCodeRows}}
<h3>SOAP fault codes</h3>
<table>
  <thead><tr><th>Code</th><th class="num">Count</th></tr></thead>
  <tbody>{{range .SOAPFaultCodeRows}}<tr><td>{{.Name}}</td><td class="num">{{.Count}}</td></tr>{{end}}</tbody>
</table>
{{end}}
{{if .SOAPFaultCategoryRows}}
<h3>SOAP fault categories</h3>
<table>
  <thead><tr><th>Category</th><th class="num">Count</th></tr></thead>
  <tbody>{{range .SOAPFaultCategoryRows}}<tr><td>{{.Name}}</td><td class="num">{{.Count}}</td></tr>{{end}}</tbody>
</table>
{{end}}
{{if .SOAPFaultBuckets}}
<h3>SOAP fault timeline</h3>
<table>
  <thead><tr><th>Timestamp</th><th class="num">Total</th><th>Codes</th><th>Categories</th></tr></thead>
  <tbody>{{range .SOAPFaultBuckets}}<tr><td>{{formatTime .Timestamp}}</td><td class="num">{{.Total}}</td><td>{{.Codes}}</td><td>{{.Categories}}</td></tr>{{end}}</tbody>
</table>
{{end}}
{{if .SOAPPayloadBuckets}}
<h3>SOAP payload timeline</h3>
<table>
  <thead><tr><th>Timestamp</th><th class="num">Samples</th><th class="num">Avg request (KB)</th><th class="num">Avg response (KB)</th><th class="num">Avg envelope overhead (KB)</th><th class="num">Max envelope overhead (KB)</th></tr></thead>
  <tbody>{{range .SOAPPayloadBuckets}}<tr><td>{{formatTime .Timestamp}}</td><td class="num">{{.Samples}}</td><td class="num">{{formatFloat .AvgRequestPayloadKB}}</td><td class="num">{{formatFloat .AvgResponsePayloadKB}}</td><td class="num">{{formatFloat .AvgEnvelopeOverheadKB}}</td><td class="num">{{formatFloat .MaxEnvelopeOverheadKB}}</td></tr>{{end}}</tbody>
</table>
{{end}}
{{else}}<p class="muted">No SOAP diagnostics were captured for this run.</p>{{end}}
{{end}}

{{define "web2_migration"}}
<h2>{{sectionTitle "web2_migration"}}</h2>
{{if .Report.Web2Migration}}
<div class="overview">
  <div class="metric"><span class="metric-label">Status</span><span class="metric-value">{{.Report.Web2Migration.Status}}</span></div>
  <div class="metric"><span class="metric-label">Imported steps</span><span class="metric-value">{{.Report.Web2Migration.ImportedSteps}}</span></div>
  <div class="metric"><span class="metric-label">Replayed steps</span><span class="metric-value">{{.Report.Web2Migration.ReplayedSteps}}</span></div>
  <div class="metric"><span class="metric-label">Failed steps</span><span class="metric-value">{{.Report.Web2Migration.FailedSteps}}</span></div>
</div>
{{else}}<p class="muted">No TruClient/Web 2.0 migration evidence is present.</p>{{end}}
{{end}}

{{define "test_setup"}}
<h2>{{sectionTitle "test_setup"}}</h2>
<table>
<thead><tr><th>Component</th><th>Detail</th></tr></thead>
<tbody>
<tr><td>Environment</td><td>{{reportEnvironment .Report}}</td></tr>
<tr><td>Scenario revision</td><td>{{if .Report.ChangeAttribution.ScenarioRevision}}{{.Report.ChangeAttribution.ScenarioRevision}}{{else}}n/a{{end}}</td></tr>
<tr><td>Load generation</td><td>APEX Load{{if gt .GeneratorCount 0}} · {{.GeneratorCount}} load {{if eq .GeneratorCount 1}}generator{{else}}generators{{end}}{{end}}{{if .Report.ChangeAttribution.GeneratorVersions}} · {{join .Report.ChangeAttribution.GeneratorVersions}}{{end}}</td></tr>
<tr><td>Scripts</td><td>{{reportScripts .Report}}</td></tr>
</tbody></table>
{{template "live_table_summary" .}}
{{end}}

{{define "raw_exports"}}
<h2>{{sectionTitle "raw_exports"}}</h2>
<table>
<thead><tr><th>Artifact</th><th>Detail</th></tr></thead>
<tbody>
<tr><td>Run archive (manifest, transactions, summaries)</td><td><span class="mono">{{.Report.Manifest.TestID}}</span></td></tr>
<tr><td>Archive records captured</td><td>{{hint .Report.Overview.Transactions}}</td></tr>
<tr><td>Interval summaries</td><td>{{.Report.Overview.IntervalSummaries}}</td></tr>
<tr><td>Resource samples</td><td>{{.Report.Overview.ResourceSamples}}</td></tr>
<tr><td>Report schema</td><td><span class="mono">{{.Report.SchemaVersion}}</span></td></tr>
</tbody></table>
<p class="section-copy"><strong>Glossary.</strong> <strong>TPS</strong> — attempts per second, successful and failed (shown as attempts/s).
<strong>P95</strong> — 95th-percentile response time over the run. <strong>VUsers</strong> — virtual users. <strong>Evidence coverage</strong> — the share of expected archive evidence that was captured.</p>
{{end}}

{{define "generator_manifest"}}
<h2>{{sectionTitle "generator_manifest"}}</h2>
{{with .Manifest}}
<h3>Assignment strategy</h3>
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
<h3>Generator shards</h3>
<table>
<thead><tr><th>Group</th><th>Target</th><th class="num">VUsers</th><th>Strategy</th></tr></thead>
<tbody>
{{range .LoadGenerators.GeneratorShards}}
<tr><td>{{.Group}}</td><td class="mono">{{.Target}}</td><td class="num">{{.VUsers}}</td><td>{{if .ShardStrategy}}{{.ShardStrategy}}{{else}}—{{end}}</td></tr>
{{else}}
<tr><td colspan="4" class="muted">No generator shard assignment was recorded.</td></tr>
{{end}}
</tbody></table>
<h3>Generator inventory</h3>
<table>
<thead><tr><th>Target</th><th>Region</th><th>Status</th><th class="num">Health</th><th class="num">Active</th><th class="num">Available</th><th class="num">Max</th><th>Capacity</th></tr></thead>
<tbody>
{{range $.InventoryRows}}
<tr><td class="mono">{{.Target}}</td><td>{{if .Region}}{{.Region}}{{else}}—{{end}}</td><td>{{.Status}}</td><td class="num">{{.HealthScore}}</td><td class="num">{{.ActiveVUsers}}</td><td class="num">{{.AvailableCapacity}}</td><td class="num">{{.MaxVUsers}}</td><td>{{if .CapacityState}}{{.CapacityState}}{{else}}—{{end}}</td></tr>
{{else}}
<tr><td colspan="8" class="muted">No generator inventory snapshot was recorded.</td></tr>
{{end}}
</tbody></table>
{{if gt $.InventoryOverflow 0}}<p class="section-copy">… and {{$.InventoryOverflow}} more load {{if eq $.InventoryOverflow 1}}generator{{else}}generators{{end}} not shown.</p>{{end}}
<h3>Scripts and groups</h3>
<table>
<thead><tr><th>Group</th><th>Script</th><th>Protocol</th><th class="num">VUsers</th><th>Ramp up</th></tr></thead>
<tbody>
{{range .Scripts}}
<tr><td>{{.GroupName}}</td><td>{{if .ScriptName}}{{.ScriptName}}{{else}}{{.ScriptID}}{{end}}</td><td>{{if .Protocol}}{{.Protocol}}{{else}}—{{end}}</td><td class="num">{{.VUsers}}</td><td>{{if .RampUp}}{{.RampUp}}{{else}}—{{end}}</td></tr>
{{else}}
<tr><td colspan="5" class="muted">No script/group assignment was recorded.</td></tr>
{{end}}
</tbody></table>
{{else}}
<p class="muted">The generator assignment manifest was not available for this run.</p>
{{end}}
{{end}}

{{define "audit_identity"}}
<h2>{{sectionTitle "audit_identity"}}</h2>
<table><tbody>
  <tr><th>Run name</th><td>{{reportRunName .Report}}</td><th>Status</th><td>{{.Report.Manifest.Status}}</td></tr>
  <tr><th>Completed</th><td>{{formatTime .Report.Manifest.CompletedAt}}</td><th>Scenario</th><td>{{identityName .Report.IdentityNames.Scenario .Report.Manifest.Provenance.ScenarioID}}</td></tr>
  <tr><th>Project</th><td>{{identityName .Report.IdentityNames.Project .Report.Manifest.Provenance.ProjectID}}</td><th>Release</th><td>{{identityName .Report.IdentityNames.Release .Report.Manifest.Provenance.ReleaseID}}</td></tr>
</tbody></table>
{{end}}

{{define "authorization"}}
<h2>{{sectionTitle "authorization"}}</h2>
<table><tbody>
  <tr><th>Created by</th><td>{{.Report.Manifest.Provenance.CreatedByDisplayName}} <span class="mono">{{.Report.Manifest.Provenance.CreatedBySubject}}</span></td><th>Started by</th><td>{{.Report.Manifest.Provenance.StartedByDisplayName}} <span class="mono">{{.Report.Manifest.Provenance.StartedBySubject}}</span></td></tr>
  <tr><th>Stopped by</th><td>{{.Report.Manifest.Provenance.StoppedByDisplayName}} <span class="mono">{{.Report.Manifest.Provenance.StoppedBySubject}}</span></td><th>Scheduled start</th><td>{{if .Report.Manifest.Provenance.ScheduledStartAt}}{{formatTime .Report.Manifest.Provenance.ScheduledStartAt}}{{else}}n/a{{end}}</td></tr>
</tbody></table>
{{end}}

{{define "governance"}}
<h2>{{sectionTitle "governance"}}</h2>
{{with .Report.Manifest.Provenance.Governance}}
<table><tbody>
  <tr><th>Capacity reservation</th><td class="mono">{{if .CapacityReservationID}}{{.CapacityReservationID}}{{else}}n/a{{end}}</td><th>Launch status</th><td>{{if .LaunchPreflight}}{{.LaunchPreflight.Status}}{{else}}n/a{{end}}</td></tr>
  {{with .ExecutionPlanApproval}}<tr><th>Execution-plan approval</th><td><span class="mono">{{if .ID}}{{.ID}}{{else}}n/a{{end}}</span> · {{if .Status}}{{.Status}}{{else}}n/a{{end}}</td><th>Approved by</th><td>{{if .ResolvedBy}}{{.ResolvedBy}}{{else}}n/a{{end}} · {{if .DecisionReason}}{{.DecisionReason}}{{else}}no reason captured{{end}}</td></tr>{{end}}
</tbody></table>
{{else}}<p class="muted">No governance provenance was captured for this run.</p>{{end}}
{{end}}

{{define "provenance"}}
<h2>{{sectionTitle "provenance"}}</h2>
<table><tbody>
  <tr><th>Scenario mode</th><td>{{if .Report.Manifest.Provenance.ScenarioMode}}{{.Report.Manifest.Provenance.ScenarioMode}}{{else}}n/a{{end}}</td><th>Total VUsers</th><td>{{.Report.Manifest.Provenance.TotalVUsers}}</td></tr>
  <tr><th>Duration</th><td>{{if .Report.Manifest.Provenance.Duration}}{{.Report.Manifest.Provenance.Duration}}{{else}}n/a{{end}}</td><th>Start delay</th><td>{{if .Report.Manifest.Provenance.StartDelay}}{{.Report.Manifest.Provenance.StartDelay}}{{else}}none{{end}}</td></tr>
  <tr><th>Report schema</th><td class="mono">{{.Report.SchemaVersion}}</td><th>Manifest schema</th><td class="mono">{{.Report.Manifest.SchemaVersion}}</td></tr>
</tbody></table>
{{template "live_table_summary" .}}
{{end}}

{{define "live_table_summary"}}
{{with .Report.Manifest}}{{if .Provenance.LiveTables}}
<h3>Live data tables</h3>
<p class="section-copy">Live table operations are separate from target metrics and reset after controller adoption.</p>
<p class="section-copy">Operation latency includes controller waits and commits; excludes transport.</p>
{{range .Provenance.LiveTables}}
<h4>{{.Alias}}</h4>
<p>{{.Profile}} · {{.Durability}} · {{.EndMode}} · {{.State}} · revision {{.Revision}} · {{.DataBytes}} data bytes</p>
<table><thead><tr><th>Seeded</th><th>Pushed</th><th>Available</th><th>Leased</th><th>Acknowledged</th><th>Popped</th><th>Quarantined</th><th>Discarded</th><th>Delivery attempts</th></tr></thead>
<tbody><tr><td>{{.Counts.Seeded}}</td><td>{{.Counts.Pushed}}</td><td>{{.Counts.Available}}</td><td>{{.Counts.Leased}}</td><td>{{.Counts.Acknowledged}}</td><td>{{.Counts.Popped}}</td><td>{{.Counts.Quarantined}}</td><td>{{.Counts.Discarded}}</td><td>{{.Counts.DeliveryAttempts}}</td></tr></tbody></table>
<table><thead><tr><th>Operations</th><th>Errors</th><th>Unknown outcomes</th><th>Total operation latency (ns)</th><th>Max operation latency (ns)</th></tr></thead>
<tbody><tr><td>{{.OpCount}}</td><td>{{.OpErrors}}</td><td>{{.OpUnknown}}</td><td>{{.OpLatencyTotalNS}}</td><td>{{.OpLatencyMaxNS}}</td></tr></tbody></table>
{{with .Finalization}}<p>Output: {{.Mode}} · {{.State}}{{if .OutputPoolID}} · pool <span class="mono">{{.OutputPoolID}}</span>{{end}}{{if .Reason}} · {{.Reason}}{{end}}</p>{{end}}
{{end}}{{end}}{{end}}
{{end}}

{{define "evidence_tiers"}}
<h2>{{sectionTitle "evidence_tiers"}}</h2>
<p class="section-copy">Evidence coverage: <strong>{{f0 .Report.Completeness.ScorePct}}%</strong> ({{tokenLabel .Report.Completeness.Status}}).</p>
<table><thead><tr><th>Tier</th><th>Status</th><th class="num">Count</th><th>Notes</th></tr></thead><tbody>
  {{range .Report.EvidenceTiers}}<tr><td>{{.Label}}</td><td>{{.Status}}</td><td class="num">{{.Count}}</td><td>{{.Reason}}</td></tr>
  {{else}}<tr><td colspan="4" class="muted">No evidence tiers were declared for this run.</td></tr>{{end}}
</tbody></table>
{{end}}

{{define "artifact_integrity"}}
<h2>{{sectionTitle "artifact_integrity"}}</h2>
{{with .Report.Integrity}}<div class="integrity-banner {{if eq .Status "verified"}}pass{{else if eq .Status "verified_partial"}}caution{{else}}fail{{end}}"><span class="integrity-label">{{if eq .Status "verified"}}&#10003; Integrity verified{{else if eq .Status "verified_partial"}}&#9888; Integrity partially verified{{else}}&#10007; Integrity {{.Status}}{{end}}</span><span>{{.Summary}}</span></div>{{end}}
<table><thead><tr><th>Artifact</th><th>Type</th><th class="num">Records</th><th class="num">Bytes</th><th>SHA-256</th></tr></thead><tbody>
  {{range .Report.ArtifactInventory}}<tr><td class="mono">{{.Name}}</td><td>{{.RecordType}} {{.Format}}</td><td class="num">{{.Count}}</td><td class="num">{{.SizeBytes}}</td><td class="mono">{{if .SHA256}}{{.SHA256}}{{else}}not captured{{end}}</td></tr>
  {{else}}<tr><td colspan="5" class="muted">No artifact inventory was available.</td></tr>{{end}}
</tbody></table>
{{end}}

{{define "report_authorship"}}
<h2>{{sectionTitle "report_authorship"}}</h2>
<table><tbody>
  <tr><th>Prepared by</th><td>{{if .Annotations.PreparedBy}}{{.Annotations.PreparedBy}}{{else}}n/a{{end}}</td><th>Updated by</th><td>{{if .Annotations.UpdatedBy}}{{.Annotations.UpdatedBy}}{{else}}n/a{{end}}</td></tr>
  <tr><th>Updated at</th><td>{{formatTime .Annotations.UpdatedAt}}</td><th>Verdict override</th><td>{{if .Annotations.VerdictOverride}}{{.Annotations.VerdictOverride}} — {{.Annotations.VerdictExplanation}}{{else}}none{{end}}</td></tr>
</tbody></table>
<div class="analyst">
  {{range $section := .CommentarySections}}{{with $comment := index $.Annotations.SectionComments $section.ID}}<p><strong>{{$section.Label}}</strong>: {{$comment}}</p>{{end}}{{end}}
</div>
{{end}}
`))
