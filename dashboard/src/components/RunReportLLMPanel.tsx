import type { LLMBandSummary, LLMGoldenPromptVarianceRow, LLMPercentileSet, LLMSummary, RunArchiveLLMLatencyQuantiles, RunArchiveLLMLengthBandStats, RunArchiveLLMReport } from '../types'
import { formatFixed } from '../utils/numberFormat'
import CapacitySweepPanel from './CapacitySweepPanel'
import LLMComparisonPanel from './LLMComparisonPanel'
import LLMLatencyBreakdown from './LLMLatencyBreakdown'

function toPercentileSet(p: LLMPercentileSet | undefined, q?: RunArchiveLLMLatencyQuantiles): LLMPercentileSet | undefined {
  if (p) return p
  if (!q || (q.samples !== undefined && q.samples <= 0)) return undefined
  return {
    p50: q.p50_ms ?? 0,
    p90: q.p90_ms ?? 0,
    p95: q.p95_ms ?? 0,
    p99: q.p99_ms ?? 0,
    max: q.max_ms ?? 0,
  }
}

function formatPercentiles(p: LLMPercentileSet | undefined, unit: string, q?: RunArchiveLLMLatencyQuantiles): string {
  const resolved = toPercentileSet(p, q)
  if (!resolved) return 'Unavailable'
  return `${formatFixed(resolved.p50, 1)}${unit} / ${formatFixed(resolved.p95, 1)}${unit} / ${formatFixed(resolved.p99, 1)}${unit}`
}

function normalizeBands(raw: RunArchiveLLMReport['by_input_band'], fallback?: RunArchiveLLMLengthBandStats[]): RunArchiveLLMLengthBandStats[] {
  const source = raw ?? fallback
  if (!source) return []
  if (Array.isArray(source)) return source
  return Object.values(source)
}

function resolveReportSummary(report: RunArchiveLLMReport): LLMSummary | undefined {
  if (report.summary && report.summary.requests > 0) return report.summary
  const requests = report.total_requests ?? 0
  if (requests <= 0) return undefined
  const tpotP50 = report.tpot?.p50_ms ?? 0
  return {
    requests,
    streamed_requests: report.streamed_requests ?? 0,
    ttft_ms: toPercentileSet(undefined, report.ttft),
    tpot_ms: toPercentileSet(undefined, report.tpot),
    e2e_ms: toPercentileSet(undefined, report.e2e),
    output_tps: tpotP50 > 0 ? { p50: 1000 / tpotP50, p90: 0, p95: 0, p99: 0, max: 0 } : undefined,
    system_output_tps: report.output_tokens_per_sec ?? 0,
    prompt_tokens: report.prompt_tokens ?? 0,
    completion_tokens: report.completion_tokens ?? 0,
    reasoning_tokens: report.reasoning_tokens ?? 0,
    cached_tokens: report.cached_tokens ?? 0,
    cache_hit_ratio: report.cache_hit_rate_pct !== undefined ? report.cache_hit_rate_pct / 100 : (report.prompt_tokens ? (report.cached_tokens ?? 0) / report.prompt_tokens : 0),
    truncated: report.truncated_requests ?? 0,
    refused: report.refused_requests ?? 0,
    stalls: report.itl?.stall_count ?? 0,
    throttled_429: report.throttled_requests ?? 0,
    tokens_estimated: Boolean(report.tokens_estimated || (report.tokens_estimated_requests ?? 0) > 0),
  }
}

export default function RunReportLLMPanel({ report }: { report?: RunArchiveLLMReport }) {
  if (!report) return null
  const summary = resolveReportSummary(report)
  if (!summary || summary.requests <= 0) return null
  const { by_model, by_action, by_input_band, by_length_band, golden_prompts, golden_prompt_variance, golden_prompt_drift } = report
  const modelEntries = Object.entries(by_model || {})
  const actionEntries = Object.entries(by_action || {})
  const bandEntries: (LLMBandSummary & Partial<RunArchiveLLMLengthBandStats>)[] = normalizeBands(by_input_band, by_length_band)
  const goldenEntries: LLMGoldenPromptVarianceRow[] = golden_prompts || golden_prompt_variance || golden_prompt_drift?.items || []

  return (
    <details className="run-report-panel llm-diagnostics-panel" open>
      <summary>LLM inference diagnostics</summary>
      <p className="run-report-muted">
        Prefill time to first token (TTFT), decode time per output token (TPOT), token throughput, and input-length bands observed across inference requests in this run.
        {summary.tokens_estimated ? ' Token counts include client-side ~4-byte estimates where server usage was omitted.' : ''}
      </p>
      <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="LLM inference summary">
        <table className="run-report-table" aria-label="LLM inference summary">
          <thead>
            <tr>
              {[
                'Requests',
                'Streaming',
                'TTFT (P50 / P95 / P99)',
                'TPOT (P50 / P95 / P99)',
                'E2E (P50 / P95 / P99)',
                'System output tok/s',
                'Per-request tok/s P50',
                'Prompt tokens',
                'Output tokens',
                'Reasoning tokens',
                'Cached tokens',
                'Cache hit ratio',
                'Truncated',
                'Refused',
                'Stalled',
                'Throttled (429)',
              ].map(label => (
                <th key={label} scope="col">
                  {label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr>
              <td>{summary.requests}</td>
              <td>{summary.streaming_requests ?? summary.streamed_requests ?? 0}</td>
              <td>{formatPercentiles(summary.ttft_ms, ' ms', report.ttft)}</td>
              <td>{formatPercentiles(summary.tpot_ms, ' ms', report.tpot)}</td>
              <td>{formatPercentiles(summary.e2e_ms, ' ms', report.e2e)}</td>
              <td>{formatFixed(summary.system_output_tps ?? report.output_tokens_per_sec ?? 0, 2)}</td>
              <td>{formatFixed(summary.output_tps?.p50 ?? 0, 2)}</td>
              <td>{summary.prompt_tokens ?? 0}</td>
              <td>{summary.output_tokens ?? summary.completion_tokens ?? 0}</td>
              <td>{summary.reasoning_tokens ?? 0}</td>
              <td>{summary.cached_tokens ?? 0}</td>
              <td>{formatFixed((summary.cache_hit_ratio ?? 0) * 100, 1)}%</td>
              <td>{summary.truncated ?? summary.truncated_requests ?? 0}</td>
              <td>{summary.refused ?? 0}</td>
              <td>{summary.stalls ?? 0}</td>
              <td>{summary.throttled_429 ?? summary.throttled_requests ?? 0}</td>
            </tr>
          </tbody>
        </table>
      </div>

      {bandEntries.length > 0 && (
        <section aria-label="LLM input length bands">
          <h4>Latency by prompt length band</h4>
          <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="Latency by prompt length band">
            <table className="run-report-table">
              <thead>
                <tr>
                  {['Input band', 'Requests', 'Prompt tokens', 'Output tokens', 'TTFT (P50 / P95 / P99)', 'TPOT (P50 / P95 / P99)', 'E2E (P50 / P95 / P99)'].map(label => (
                    <th key={label} scope="col">
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {bandEntries.map(band => {
                  const bs = band.summary
                  return (
                    <tr key={band.band}>
                      <th scope="row">{band.band}</th>
                      <td>{bs?.requests ?? band.requests ?? 0}</td>
                      <td>{bs?.prompt_tokens ?? band.prompt_tokens ?? 0}</td>
                      <td>{bs?.output_tokens ?? band.completion_tokens ?? 0}</td>
                      <td>{formatPercentiles(bs?.ttft_ms, ' ms', band.ttft)}</td>
                      <td>{formatPercentiles(bs?.tpot_ms, ' ms', band.tpot)}</td>
                      <td>{formatPercentiles(bs?.e2e_ms, ' ms', band.e2e)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {modelEntries.length > 0 && (
        <section aria-label="LLM breakdown by model">
          <h4>Breakdown by model</h4>
          <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="Breakdown by model">
            <table className="run-report-table">
              <thead>
                <tr>
                  {['Model', 'Requests', 'TTFT (P50 / P95 / P99)', 'TPOT (P50 / P95 / P99)', 'Prompt tokens', 'Output tokens', 'Truncated', 'Throttled (429)'].map(label => (
                    <th key={label} scope="col">
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {modelEntries.map(([model, m]) => (
                  <tr key={model}>
                    <th scope="row">{model}</th>
                    <td>{m.requests}</td>
                    <td>{formatPercentiles(m.ttft_ms, ' ms', m.ttft)}</td>
                    <td>{formatPercentiles(m.tpot_ms, ' ms', m.tpot)}</td>
                    <td>{m.prompt_tokens ?? 0}</td>
                    <td>{m.output_tokens ?? m.completion_tokens ?? 0}</td>
                    <td>{m.truncated ?? m.truncated_requests ?? 0}</td>
                    <td>{m.throttled_429 ?? m.throttled_requests ?? 0}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {actionEntries.length > 0 && (
        <section aria-label="LLM breakdown by action">
          <h4>Breakdown by action</h4>
          <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="Breakdown by action">
            <table className="run-report-table">
              <thead>
                <tr>
                  {['Action', 'Requests', 'TTFT (P50 / P95 / P99)', 'TPOT (P50 / P95 / P99)', 'Prompt tokens', 'Output tokens'].map(label => (
                    <th key={label} scope="col">
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {actionEntries.map(([action, a]) => (
                  <tr key={action}>
                    <th scope="row">{action}</th>
                    <td>{a.requests}</td>
                    <td>{formatPercentiles(a.ttft_ms, ' ms', a.ttft)}</td>
                    <td>{formatPercentiles(a.tpot_ms, ' ms', a.tpot)}</td>
                    <td>{a.prompt_tokens ?? 0}</td>
                    <td>{a.output_tokens ?? a.completion_tokens ?? 0}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {goldenEntries.length > 0 && (
        <section aria-label="Golden prompt variance">
          <h4>Golden prompt output variance</h4>
          <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="Golden prompt output variance">
            <table className="run-report-table">
              <thead>
                <tr>
                  {['Prompt ID', 'Samples', 'Mean output tokens', 'Min / max output tokens', 'Stddev', 'Coefficient of variation'].map(label => (
                    <th key={label} scope="col">
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {goldenEntries.map(gp => {
                  const id = gp.prompt_id ?? gp.name ?? 'golden_prompt'
                  const minTok = gp.min_output_tokens ?? gp.min_completion_tokens ?? 0
                  const maxTok = gp.max_output_tokens ?? gp.max_completion_tokens ?? 0
                  const meanTok = gp.mean_output_tokens ?? gp.avg_completion_tokens ?? ((minTok + maxTok) / 2)
                  return (
                    <tr key={id}>
                      <th scope="row">{id}</th>
                      <td>{gp.samples}</td>
                      <td>{formatFixed(meanTok, 1)}</td>
                      <td>
                        {minTok} / {maxTok}
                      </td>
                      <td>{formatFixed(gp.stddev_output_tokens ?? 0, 2)}</td>
                      <td>{formatFixed(((gp.cv ?? gp.length_drift_ratio) ?? 0) * 100, 1)}%</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}

      <CapacitySweepPanel sweep={report.capacity_sweep} />
      <LLMLatencyBreakdown decomposition={report.latency_decomposition} />
      <LLMComparisonPanel comparison={report.comparison} />
    </details>
  )
}
