package api

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"
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
	"join": func(values []string) string {
		return strings.Join(values, ", ")
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Stormrunner Report - {{.Report.Manifest.TestName}}</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f4f1ea;
      --surface: #fffdf8;
      --ink: #1f2933;
      --muted: #5b6875;
      --line: #d8cfc2;
      --accent: #0f766e;
      --danger: #b42318;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: linear-gradient(180deg, #efe7da 0%, var(--bg) 100%);
      color: var(--ink);
      font: 15px/1.5 "Iowan Old Style", "Palatino Linotype", "Book Antiqua", Georgia, serif;
    }
    main {
      max-width: 1100px;
      margin: 0 auto;
      padding: 32px 20px 48px;
    }
    .hero {
      background: var(--surface);
      border: 1px solid var(--line);
      border-radius: 20px;
      padding: 24px;
      box-shadow: 0 18px 40px rgba(31, 41, 51, 0.08);
    }
    .eyebrow {
      margin: 0 0 8px;
      color: var(--accent);
      font: 600 12px/1.2 "Avenir Next", "Segoe UI", sans-serif;
      letter-spacing: 0.12em;
      text-transform: uppercase;
    }
    h1, h2 {
      margin: 0;
      font-family: "Avenir Next", "Segoe UI", sans-serif;
    }
    h1 { font-size: 34px; line-height: 1.1; }
    h2 { font-size: 20px; margin-bottom: 14px; }
    .subhead {
      margin: 12px 0 0;
      color: var(--muted);
      max-width: 70ch;
    }
    .meta, .overview {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
      gap: 12px;
      margin-top: 20px;
    }
    .card, .metric {
      background: var(--surface);
      border: 1px solid var(--line);
      border-radius: 18px;
      padding: 16px;
    }
    .metric-label {
      display: block;
      color: var(--muted);
      font: 600 11px/1.2 "Avenir Next", "Segoe UI", sans-serif;
      letter-spacing: 0.08em;
      text-transform: uppercase;
    }
    .metric-value {
      display: block;
      margin-top: 8px;
      font: 600 24px/1.1 "Avenir Next", "Segoe UI", sans-serif;
    }
    section { margin-top: 24px; }
    table {
      width: 100%;
      border-collapse: collapse;
      background: var(--surface);
      border: 1px solid var(--line);
      border-radius: 18px;
      overflow: hidden;
    }
    th, td {
      padding: 12px 14px;
      border-bottom: 1px solid var(--line);
      text-align: left;
      vertical-align: top;
    }
    th {
      font: 600 12px/1.2 "Avenir Next", "Segoe UI", sans-serif;
      color: var(--muted);
      letter-spacing: 0.08em;
      text-transform: uppercase;
    }
    tr:last-child td { border-bottom: 0; }
    .status {
      display: inline-block;
      padding: 4px 10px;
      border-radius: 999px;
      background: rgba(15, 118, 110, 0.12);
      color: var(--accent);
      font: 600 12px/1.2 "Avenir Next", "Segoe UI", sans-serif;
    }
    .status.danger {
      background: rgba(180, 35, 24, 0.12);
      color: var(--danger);
    }
    .muted { color: var(--muted); }
    .list {
      margin: 10px 0 0;
      padding-left: 18px;
    }
    .section-copy {
      margin: 0 0 14px;
      color: var(--muted);
    }
    .footer {
      margin-top: 28px;
      color: var(--muted);
      font-size: 13px;
    }
    @page {
      size: auto;
      margin: 12mm;
    }
    @media print {
      :root {
        --bg: #ffffff;
        --surface: #ffffff;
        --line: #c9c9c9;
      }
      body {
        background: #ffffff;
        color: #111111;
        font-size: 11pt;
      }
      main {
        max-width: none;
        margin: 0;
        padding: 0;
      }
      .hero,
      .card,
      .metric,
      table {
        box-shadow: none;
        border-radius: 0;
      }
      .hero,
      section,
      table,
      tr {
        break-inside: avoid;
        page-break-inside: avoid;
      }
      thead {
        display: table-header-group;
      }
      tfoot {
        display: table-footer-group;
      }
      .eyebrow,
      .metric-label,
      th {
        color: #333333;
      }
      .footer {
        margin-top: 18px;
      }
    }
    @media (max-width: 640px) {
      main { padding: 20px 14px 32px; }
      h1 { font-size: 28px; }
      table { display: block; overflow-x: auto; }
    }
  </style>
</head>
<body>
  <main>
    <section class="hero">
      <p class="eyebrow">Stormrunner standalone report</p>
      <h1>{{.Report.Manifest.TestName}}</h1>
      <p class="subhead">Generated from the archived run bundle for test {{.Report.Manifest.TestID}}. This report is self-contained and suitable for CI artifacts or human review.</p>
      <div class="meta">
        <div class="card">
          <span class="metric-label">Status</span>
          <span class="metric-value"><span class="status {{if .Report.Manifest.SLABreached}}danger{{end}}">{{.Report.Manifest.Status}}</span></span>
        </div>
        <div class="card">
          <span class="metric-label">Completed</span>
          <span class="metric-value">{{formatTime .Report.Manifest.CompletedAt}}</span>
        </div>
        <div class="card">
          <span class="metric-label">Transactions</span>
          <span class="metric-value">{{.Report.Overview.Transactions}}</span>
        </div>
        <div class="card">
          <span class="metric-label">SLA</span>
          <span class="metric-value">{{if .Report.Manifest.SLABreached}}Breached{{else}}Passed{{end}}</span>
          {{if .Report.Manifest.SLADetails}}
          <p class="muted">{{join .Report.Manifest.SLADetails}}</p>
          {{end}}
        </div>
      </div>
    </section>

    <section>
      <h2>Overview</h2>
      <div class="overview">
        <div class="metric">
          <span class="metric-label">Avg Response</span>
          <span class="metric-value">{{formatFloat .Report.Overview.AvgResponseMs}} ms</span>
        </div>
        <div class="metric">
          <span class="metric-label">Error Rate</span>
          <span class="metric-value">{{formatPercent .Report.Overview.ErrorPct}}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Peak TPS</span>
          <span class="metric-value">{{formatFloat .Report.Overview.PeakTPS}}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Peak Active VUsers</span>
          <span class="metric-value">{{.Report.Overview.PeakActiveVUsers}}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Response Range</span>
          <span class="metric-value">{{formatFloat .Report.Overview.MinResponseMs}}-{{formatFloat .Report.Overview.MaxResponseMs}} ms</span>
        </div>
        <div class="metric">
          <span class="metric-label">Resource Samples</span>
          <span class="metric-value">{{.Report.Overview.ResourceSamples}}</span>
        </div>
      </div>
    </section>

    {{if .Report.SummaryTimeline}}
    <section>
      <h2>Summary Timeline</h2>
      <p class="section-copy">Interval snapshots captured during the run for throughput, latency, and error tracking.</p>
      <table>
        <thead>
          <tr>
            <th>Timestamp</th>
            <th>Active VUsers</th>
            <th>TPS</th>
            <th>P95 (ms)</th>
            <th>Error %</th>
            <th>Total Tx</th>
            <th>Total Errors</th>
          </tr>
        </thead>
        <tbody>
          {{range .Report.SummaryTimeline}}
          <tr>
            <td>{{formatTime .Timestamp}}</td>
            <td>{{.ActiveVUsers}}</td>
            <td>{{formatFloat .TPS}}</td>
            <td>{{formatFloat .P95Ms}}</td>
            <td>{{formatPercent .ErrorPct}}</td>
            <td>{{.TotalTx}}</td>
            <td>{{.TotalErrors}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
    </section>
    {{end}}

    {{if .Report.ErrorPatterns}}
    <section>
      <h2>Error Patterns</h2>
      <p class="section-copy">Grouped failure signatures derived from archived transaction errors. The first retained snapshot stays attached to each cluster.</p>
      <table>
        <thead>
          <tr>
            <th>Pattern</th>
            <th>Occurrences</th>
            <th>First Seen</th>
            <th>Latest Seen</th>
            <th>Actions</th>
            <th>Snapshot</th>
          </tr>
        </thead>
        <tbody>
          {{range .Report.ErrorPatterns}}
          <tr>
            <td>{{.Sample}}</td>
            <td>{{.Count}}</td>
            <td>{{formatTime .FirstSeen}}</td>
            <td>{{formatTime .LatestSeen}}</td>
            <td>{{if .Actions}}{{join .Actions}}{{else}}n/a{{end}}</td>
            <td>{{if .Snapshot}}retained{{else}}n/a{{end}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
    </section>
    {{end}}

    <section>
      <h2>Resource Peaks</h2>
      <div class="overview">
        <div class="metric">
          <span class="metric-label">CPU Peak</span>
          <span class="metric-value">{{formatFloat .Report.ResourcePeaks.CPUProcessPct}}%</span>
        </div>
        <div class="metric">
          <span class="metric-label">Memory Peak</span>
          <span class="metric-value">{{formatFloat .Report.ResourcePeaks.MemProcessMB}} MB</span>
        </div>
        <div class="metric">
          <span class="metric-label">Net Sent Peak</span>
          <span class="metric-value">{{formatFloat .Report.ResourcePeaks.NetSentKbps}} KB/s</span>
        </div>
        <div class="metric">
          <span class="metric-label">Net Recv Peak</span>
          <span class="metric-value">{{formatFloat .Report.ResourcePeaks.NetRecvKbps}} KB/s</span>
        </div>
      </div>
    </section>

    <section>
      <h2>Action Summary</h2>
      <p class="section-copy">Top transaction groups captured from the archived JSONL bundle.</p>
      <table>
        <thead>
          <tr>
            <th>Action</th>
            <th>Transactions</th>
            <th>Errors</th>
            <th>Error %</th>
            <th>Avg (ms)</th>
            <th>Max (ms)</th>
          </tr>
        </thead>
        <tbody>
          {{range .ActionRows}}
          <tr>
            <td>{{.Name}}</td>
            <td>{{.Stats.Transactions}}</td>
            <td>{{.Stats.Errors}}</td>
            <td>{{formatPercent .Stats.ErrorPct}}</td>
            <td>{{formatFloat .Stats.AvgResponseMs}}</td>
            <td>{{formatFloat .Stats.MaxResponseMs}}</td>
          </tr>
          {{else}}
          <tr>
            <td colspan="6" class="muted">No action-level archive data was recorded.</td>
          </tr>
          {{end}}
        </tbody>
      </table>
    </section>

    {{if .Report.Overview.Browser}}
    <section>
      <h2>Browser Metrics</h2>
      <p class="section-copy">Client-side rendering and interaction measurements aggregated from browser-enabled transactions.</p>
      <div class="overview">
        <div class="metric">
          <span class="metric-label">Samples</span>
          <span class="metric-value">{{.Report.Overview.Browser.Samples}}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Avg FCP</span>
          <span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgFCPMs}} ms</span>
        </div>
        <div class="metric">
          <span class="metric-label">Avg LCP</span>
          <span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgLCPMs}} ms</span>
        </div>
        <div class="metric">
          <span class="metric-label">Avg CLS</span>
          <span class="metric-value">{{formatScore .Report.Overview.Browser.AvgCLSScore}}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Avg INP</span>
          <span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgINPMs}} ms</span>
        </div>
        <div class="metric">
          <span class="metric-label">Avg JS Heap</span>
          <span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgJSHeapUsedMB}} MB</span>
        </div>
        <div class="metric">
          <span class="metric-label">Avg Browser CPU</span>
          <span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgBrowserProcessCPU}}%</span>
        </div>
        <div class="metric">
          <span class="metric-label">Avg Browser Memory</span>
          <span class="metric-value">{{formatFloat .Report.Overview.Browser.AvgBrowserProcessMem}} MB</span>
        </div>
      </div>

      {{if .BrowserEngineRows}}
      <h2 style="margin-top:20px;">Engine Rollup</h2>
      <table>
        <thead>
          <tr>
            <th>Engine</th>
            <th>Samples</th>
            <th>Avg LCP</th>
            <th>Avg CLS</th>
            <th>Avg INP</th>
          </tr>
        </thead>
        <tbody>
          {{range .BrowserEngineRows}}
          <tr>
            <td>{{.BrowserEngine}}</td>
            <td>{{.Samples}}</td>
            <td>{{formatFloat .AvgLCPMs}} ms</td>
            <td>{{formatScore .AvgCLSScore}}</td>
            <td>{{formatFloat .AvgINPMs}} ms</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}

      {{if .BrowserPlatformRows}}
      <h2 style="margin-top:20px;">Platform Rollup</h2>
      <table>
        <thead>
          <tr>
            <th>Platform</th>
            <th>Engine</th>
            <th>Samples</th>
            <th>Avg LCP</th>
            <th>Avg CLS</th>
            <th>Avg INP</th>
          </tr>
        </thead>
        <tbody>
          {{range .BrowserPlatformRows}}
          <tr>
            <td>{{.DevicePlatform}}</td>
            <td>{{.BrowserEngine}}</td>
            <td>{{.Samples}}</td>
            <td>{{formatFloat .AvgLCPMs}} ms</td>
            <td>{{formatScore .AvgCLSScore}}</td>
            <td>{{formatFloat .AvgINPMs}} ms</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}

      {{if .BrowserDeviceRows}}
      <h2 style="margin-top:20px;">Device Rollup</h2>
      <table>
        <thead>
          <tr>
            <th>Device</th>
            <th>Platform</th>
            <th>Engine</th>
            <th>Tier</th>
            <th>Avg LCP</th>
            <th>Avg INP</th>
          </tr>
        </thead>
        <tbody>
          {{range .BrowserDeviceRows}}
          <tr>
            <td>{{.DeviceProfile}}</td>
            <td>{{.DevicePlatform}}</td>
            <td>{{.BrowserEngine}}</td>
            <td>{{.DeviceTier}}</td>
            <td>{{formatFloat .AvgLCPMs}} ms</td>
            <td>{{formatFloat .AvgINPMs}} ms</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}
    </section>
    {{end}}

    {{if or .SOAPOperationRows .SOAPFaultCodeRows .SOAPFaultBuckets .SOAPPayloadBuckets}}
    <section>
      <h2>SOAP Diagnostics</h2>
      <p class="section-copy">Archived SOAP operation, fault, and payload summaries extracted from transaction metadata.</p>

      {{if .SOAPOperationRows}}
      <h2 style="margin-top:20px;">SOAP Operation Summary</h2>
      <table>
        <thead>
          <tr>
            <th>Operation</th>
            <th>Transactions</th>
            <th>Errors</th>
            <th>Error %</th>
            <th>Avg (ms)</th>
          </tr>
        </thead>
        <tbody>
          {{range .SOAPOperationRows}}
          <tr>
            <td>{{.Name}}</td>
            <td>{{.Stats.Transactions}}</td>
            <td>{{.Stats.Errors}}</td>
            <td>{{formatPercent .Stats.ErrorPct}}</td>
            <td>{{formatFloat .Stats.AvgResponseMs}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}

      {{if .SOAPFaultCodeRows}}
      <h2 style="margin-top:20px;">SOAP Fault Codes</h2>
      <table>
        <thead>
          <tr>
            <th>Code</th>
            <th>Count</th>
          </tr>
        </thead>
        <tbody>
          {{range .SOAPFaultCodeRows}}
          <tr>
            <td>{{.Name}}</td>
            <td>{{.Count}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}

      {{if .SOAPFaultCategoryRows}}
      <h2 style="margin-top:20px;">SOAP Fault Categories</h2>
      <table>
        <thead>
          <tr>
            <th>Category</th>
            <th>Count</th>
          </tr>
        </thead>
        <tbody>
          {{range .SOAPFaultCategoryRows}}
          <tr>
            <td>{{.Name}}</td>
            <td>{{.Count}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}

      {{if .SOAPFaultBuckets}}
      <h2 style="margin-top:20px;">SOAP Fault Timeline</h2>
      <table>
        <thead>
          <tr>
            <th>Timestamp</th>
            <th>Total</th>
            <th>Codes</th>
            <th>Categories</th>
          </tr>
        </thead>
        <tbody>
          {{range .SOAPFaultBuckets}}
          <tr>
            <td>{{formatTime .Timestamp}}</td>
            <td>{{.Total}}</td>
            <td>{{.Codes}}</td>
            <td>{{.Categories}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}

      {{if .SOAPPayloadBuckets}}
      <h2 style="margin-top:20px;">SOAP Payload Timeline</h2>
      <table>
        <thead>
          <tr>
            <th>Timestamp</th>
            <th>Samples</th>
            <th>Avg Request KB</th>
            <th>Avg Response KB</th>
            <th>Avg Envelope Overhead KB</th>
            <th>Max Envelope Overhead KB</th>
          </tr>
        </thead>
        <tbody>
          {{range .SOAPPayloadBuckets}}
          <tr>
            <td>{{formatTime .Timestamp}}</td>
            <td>{{.Samples}}</td>
            <td>{{formatFloat .AvgRequestPayloadKB}}</td>
            <td>{{formatFloat .AvgResponsePayloadKB}}</td>
            <td>{{formatFloat .AvgEnvelopeOverheadKB}}</td>
            <td>{{formatFloat .MaxEnvelopeOverheadKB}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{end}}
    </section>
    {{end}}

    <p class="footer">Report generated at {{formatTime .GeneratedAt}} from archived artifacts. Keep this file with the run archive for audit history.</p>
  </main>
</body>
</html>`))

func renderRunArchiveHTMLReport(report *RunArchiveReport) ([]byte, error) {
	if report == nil || report.Manifest == nil {
		return nil, fmt.Errorf("archive report is incomplete")
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

	data := runArchiveHTMLReportData{
		GeneratedAt:           time.Now().UTC(),
		Report:                report,
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
