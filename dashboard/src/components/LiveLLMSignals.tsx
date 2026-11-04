import type { LiveLLMMetrics, LiveMetrics, MetricsHistoryPoint } from '../types'

// The live LLM signals render only for a run that sends LLM requests, so the
// live route loads them on use (TestExecutionStatusPanels, TestExecutionCharts).
export function LiveLLMSignalsCard({ llm, cpuPct, droppedResults }: { llm: LiveLLMMetrics; cpuPct: number; droppedResults: number }) {
  const summary = llm.summary!
  const cacheHitPct = (summary.prompt_tokens ?? 0) > 0
    ? ((summary.cached_tokens ?? 0) / (summary.prompt_tokens ?? 1)) * 100
    : 0
  const finishEntries = Object.entries(summary.finish_reasons ?? {}).sort((a, b) => b[1] - a[1])
  const bandEntries = Object.entries(summary.by_input_band ?? {}).sort((a, b) => a[0].localeCompare(b[0]))
  const badgeTone = (llm.throttle_rate_pct ?? 0) > 5 ? 'danger' : (llm.goodput_pct ?? 100) < 90 ? 'warning' : 'healthy'
  const cpuSaturated = cpuPct >= 85
  const incompletePeer = Boolean(llm.incomplete || summary.incomplete)
  return (
    <div className="goal-status-card llm-metrics-card" data-testid="live-llm-signals">
      <div className="goal-status-header">
        <div>
          <h3 className="goal-status-title">LLM inference signals</h3>
          <p className="goal-status-subtitle">
            Rolling {Math.round(llm.rolling_window_seconds || 30)}s window across {summary.requests.toLocaleString()} requests ({(summary.streamed_requests ?? 0).toLocaleString()} streamed).
            {(summary.tokens_estimated_requests ?? 0) > 0 ? ' Token counts include client-side ~4 B/tok estimates.' : ''}
          </p>
        </div>
        <div className={`goal-status-badge ${badgeTone}`}>
          {(llm.output_tokens_per_sec ?? 0).toFixed(1)} tok/s
        </div>
      </div>

      {(cpuSaturated || droppedResults > 0 || incompletePeer) && (
        <div className="goal-status-warning" role="status" data-testid="llm-generator-self-check-banner">
          {cpuSaturated || droppedResults > 0
            ? `Generator self-check: ${cpuSaturated ? `generator CPU saturated (${cpuPct.toFixed(1)}%)` : ''}${cpuSaturated && droppedResults > 0 ? ' and ' : ''}${droppedResults > 0 ? `${droppedResults.toLocaleString()} results dropped` : ''} — inter-token latency (ITL) is marked unreliable.`
            : ''}
          {incompletePeer
            ? `${cpuSaturated || droppedResults > 0 ? ' ' : ''}A generator in this run predates LLM summaries — the LLM summary is incomplete.`
            : ''}
        </div>
      )}

      <div className="goal-status-grid">
        <div className="goal-status-metric">
          <span className="goal-status-label">TTFT P50 / P95</span>
          <span className="goal-status-value">{(summary.p50_ttft_ms ?? 0).toFixed(0)} / {(summary.p95_ttft_ms ?? 0).toFixed(0)} ms</span>
        </div>
        <div className="goal-status-metric">
          <span className="goal-status-label">TPOT P50 / P95</span>
          <span className="goal-status-value">{(summary.p50_tpot_ms ?? 0).toFixed(1)} / {(summary.p95_tpot_ms ?? 0).toFixed(1)} ms/tok</span>
        </div>
        <div className="goal-status-metric">
          <span className="goal-status-label">Output tok/s</span>
          <span className="goal-status-value">{(llm.output_tokens_per_sec ?? 0).toFixed(1)} tok/s</span>
        </div>
        <div className="goal-status-metric">
          <span className="goal-status-label">Goodput</span>
          <span className="goal-status-value">{(llm.goodput_pct ?? 0).toFixed(1)}%</span>
        </div>
        <div className="goal-status-metric">
          <span className="goal-status-label">Throttle rate</span>
          <span className="goal-status-value">{(llm.throttle_rate_pct ?? 0).toFixed(1)}%</span>
        </div>
        <div className="goal-status-metric">
          <span className="goal-status-label">Cache hit rate</span>
          <span className="goal-status-value">{cacheHitPct.toFixed(1)}%</span>
        </div>
      </div>

      {finishEntries.length > 0 && (
        <div className="capacity-live-breakdown">
          <div>
            <span className="capacity-live-heading">Finish reasons</span>
            {finishEntries.map(([reason, count]) => (
              <span key={reason}>{reason}: {count.toLocaleString()}</span>
            ))}
          </div>
          {bandEntries.length > 0 && (
            <div>
              <span className="capacity-live-heading">By input length band</span>
              {bandEntries.map(([band, bandStats]) => (
                <span key={band}>{band}: {bandStats.requests.toLocaleString()} req · {(bandStats.p95_ttft_ms ?? 0).toFixed(0)} ms P95 TTFT</span>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// LiveLLMChartSignals is the graph wall's generator-CPU warning and, on the
// LLM tab, its KPI tiles; values fall back to the latest history point.
export function LiveLLMChartSignals({ latestMetrics, latest, showTiles }: { latestMetrics?: LiveMetrics; latest: Partial<MetricsHistoryPoint>; showTiles: boolean }) {
  const llm = latestMetrics?.llm
  const cpu = latestMetrics?.resources?.cpu_process_pct_max ?? latestMetrics?.resources?.cpu_process_pct ?? latest.cpuProcessMax ?? latest.cpuProcess ?? 0
  return (<>
    {cpu > 85 && (
      <p className="scenario-script-status error" role="alert">
        Generator CPU exceeded 85% while streaming SSE tokens — client-side TTFT and TPOT measurements may include generator scheduling jitter.
      </p>
    )}
    {showTiles && (
      <div className="charts-toolbar-badges llm-kpi-tiles" role="region" aria-label="LLM live KPIs">
        <span className="archive-chip">
          Output tok/s: {(llm?.interval_output_tokens_per_sec ?? llm?.output_tokens_per_sec ?? latest.llmOutputTokensPerSec ?? 0).toFixed(1)}
        </span>
        <span className="archive-chip">
          TTFT p95: {(llm?.interval_summary?.p95_ttft_ms ?? llm?.summary?.p95_ttft_ms ?? latest.llmTtftP95Ms ?? 0).toFixed(1)} ms
        </span>
        <span className="archive-chip">
          TPOT p95: {(llm?.interval_summary?.p95_tpot_ms ?? llm?.summary?.p95_tpot_ms ?? latest.llmTpotP95Ms ?? 0).toFixed(1)} ms
        </span>
        <span className="archive-chip">
          Goodput %: {(llm?.goodput_pct ?? latest.llmGoodputPct ?? 0).toFixed(1)}%
        </span>
        <span className="archive-chip">
          Throttle 429 %: {(llm?.throttle_rate_pct ?? latest.llmThrottleRatePct ?? 0).toFixed(1)}%
        </span>
      </div>
    )}
  </>)
}
