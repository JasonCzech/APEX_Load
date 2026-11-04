import type { CapacitySweepReport } from '../types'
import { formatFixed } from '../utils/numberFormat'

export default function CapacitySweepPanel({ sweep }: { sweep?: CapacitySweepReport }) {
  if (!sweep || !sweep.levels || sweep.levels.length === 0) return null

  const levels = sweep.levels
  const maxLoad = Math.max(...levels.map(lv => lv.offered_load || 0), sweep.knee?.load ?? 0, 1)
  const sloCeiling = sweep.slo_ttft_ms ?? sweep.slo_e2e_ms
  const maxLatency = Math.max(
    ...levels.map(lv => Math.max(lv.ttft_p95_ms || 0, lv.p95_ms || 0)),
    sloCeiling ?? 0,
    10,
  )
  const chartWidth = 520
  const chartHeight = 140
  const padLeft = 42
  const padRight = 18
  const padTop = 14
  const padBottom = 26
  const plotW = chartWidth - padLeft - padRight
  const plotH = chartHeight - padTop - padBottom
  const xFor = (load: number) => padLeft + Math.min(1, Math.max(0, load / maxLoad)) * plotW
  const yFor = (ms: number) => padTop + plotH - Math.min(1, Math.max(0, ms / maxLatency)) * plotH
  const polylinePoints = levels
    .map(lv => `${xFor(lv.offered_load).toFixed(1)},${yFor(lv.ttft_p95_ms > 0 ? lv.ttft_p95_ms : lv.p95_ms || 0).toFixed(1)}`)
    .join(' ')

  return (
    <section aria-label="Capacity sweep analysis">
      <h4>Capacity sweep analysis</h4>
      {sweep.knee ? (
        <p className="run-report-muted">
          Detected latency knee at load {formatFixed(sweep.knee.load, 1)} ({sweep.mode}){sweep.knee.ttft_p95_ms !== undefined ? ` with TTFT P95 ${formatFixed(sweep.knee.ttft_p95_ms, 1)} ms` : ''} (slope {formatFixed(sweep.knee.slope_before, 2)} → {formatFixed(sweep.knee.slope_after, 2)}{sweep.knee.r_squared !== undefined ? `, R²=${formatFixed(sweep.knee.r_squared, 2)}` : ''}).
          {sweep.max_sustainable_rate
            ? ` Max sustainable rate: ${formatFixed(sweep.max_sustainable_rate.offered_load, 1)} (${formatFixed(sweep.max_sustainable_rate.good_output_tokens_per_s ?? sweep.max_sustainable_rate.system_output_tokens_per_sec ?? 0, 1)} good output tok/s).`
            : ''}
        </p>
      ) : (
        sweep.knee_note && <p className="run-report-muted">{sweep.knee_note}</p>
      )}
      <div className="capacity-sweep-chart-wrap" style={{ marginBottom: 12 }}>
        <svg
          viewBox={`0 0 ${chartWidth} ${chartHeight}`}
          width="100%"
          height={chartHeight}
          role="img"
          aria-label="Capacity sweep latency vs throughput chart"
        >
          <line x1={padLeft} y1={padTop} x2={padLeft} y2={padTop + plotH} stroke="currentColor" strokeOpacity={0.25} />
          <line x1={padLeft} y1={padTop + plotH} x2={padLeft + plotW} y2={padTop + plotH} stroke="currentColor" strokeOpacity={0.25} />
          {sloCeiling !== undefined && sloCeiling > 0 && (
            <g data-testid="capacity-sweep-slo-line">
              <line
                x1={padLeft}
                y1={yFor(sloCeiling)}
                x2={padLeft + plotW}
                y2={yFor(sloCeiling)}
                stroke="#f59e0b"
                strokeDasharray="4 3"
                strokeWidth={1.5}
              />
              <text x={padLeft + plotW - 4} y={Math.max(padTop + 10, yFor(sloCeiling) - 4)} textAnchor="end" fontSize="10" fill="#f59e0b">
                SLO {formatFixed(sloCeiling, 0)} ms
              </text>
            </g>
          )}
          {sweep.knee && sweep.knee.load > 0 && (
            <g data-testid="capacity-sweep-knee-line">
              <line
                x1={xFor(sweep.knee.load)}
                y1={padTop}
                x2={xFor(sweep.knee.load)}
                y2={padTop + plotH}
                stroke="#ef4444"
                strokeDasharray="4 3"
                strokeWidth={1.5}
              />
              <text x={Math.min(padLeft + plotW - 4, xFor(sweep.knee.load) + 4)} y={padTop + 10} fontSize="10" fill="#ef4444">
                Knee ({formatFixed(sweep.knee.load, 0)})
              </text>
            </g>
          )}
          {levels.length > 1 && (
            <polyline fill="none" stroke="#38bdf8" strokeWidth={2} points={polylinePoints} />
          )}
          {levels.map(lv => {
            const lat = lv.ttft_p95_ms > 0 ? lv.ttft_p95_ms : lv.p95_ms || 0
            const ok = lv.slo_compliant ?? lv.meets_slo ?? true
            return (
              <circle
                key={`pt-${lv.level_index ?? lv.stage_index ?? 0}-${lv.name}`}
                cx={xFor(lv.offered_load)}
                cy={yFor(lat)}
                r={4}
                fill={ok ? '#22c55e' : '#ef4444'}
              >
                <title>{`${lv.name}: load ${formatFixed(lv.offered_load, 1)}, P95 ${formatFixed(lat, 1)} ms`}</title>
              </circle>
            )
          })}
        </svg>
      </div>
      <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="Capacity sweep load levels">
        <table className="run-report-table">
          <thead>
            <tr>
              {[
                'Load level',
                'Offered load',
                'Achieved RPS',
                'Goodput %',
                'Output tok/s',
                'TTFT P50 / P95 (ms)',
                'TPOT P50 / P95 (ms)',
                'Throttled %',
                'KV cache peak %',
                'Cost / 1M output tok',
                'SLO status',
              ].map(label => (
                <th key={label} scope="col">
                  {label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sweep.levels.map(lv => (
              <tr key={`${lv.level_index ?? lv.stage_index ?? 0}-${lv.name}`}>
                <th scope="row">{lv.name}</th>
                <td>{formatFixed(lv.offered_load, 1)}</td>
                <td>{formatFixed(lv.achieved_tps, 2)}</td>
                <td>{formatFixed(lv.goodput_pct, 1)}%</td>
                <td>{formatFixed(lv.output_tokens_per_s ?? lv.system_output_tokens_per_sec ?? 0, 1)}</td>
                <td>
                  {formatFixed(lv.ttft_p50_ms, 1)} / {formatFixed(lv.ttft_p95_ms, 1)}
                </td>
                <td>
                  {formatFixed(lv.tpot_p50_ms, 1)} / {formatFixed(lv.tpot_p95_ms, 1)}
                </td>
                <td>{formatFixed(lv.throttle_rate_pct, 1)}%</td>
                <td>{lv.server_kv_cache_peak_pct !== undefined ? `${formatFixed(lv.server_kv_cache_peak_pct, 1)}%` : '—'}</td>
                <td>{lv.cost_per_million_output_tokens_usd !== undefined ? `$${formatFixed(lv.cost_per_million_output_tokens_usd, 2)}` : '—'}</td>
                <td>{(lv.slo_compliant ?? lv.meets_slo) ? 'Within SLO' : 'Breached'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
