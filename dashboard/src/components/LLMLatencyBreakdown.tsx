import type { LLMLatencyDecomposition } from '../types'
import { formatFixed } from '../utils/numberFormat'

export default function LLMLatencyBreakdown({ decomposition }: { decomposition?: LLMLatencyDecomposition }) {
  const sampleCount = decomposition ? (decomposition.samples ?? decomposition.streamed_requests ?? decomposition.total_requests ?? 0) : 0
  if (!decomposition || sampleCount <= 0) return null
  const est = decomposition.concurrency_estimate
  const queueMs = decomposition.server_queue_avg_ms ?? decomposition.server_avg_queue_ms ?? 0
  const prefillMs = decomposition.prefill_avg_ms ?? decomposition.server_avg_prefill_ms ?? 0
  const decodeMs = decomposition.decode_avg_ms ?? (decomposition.server_avg_decode_ms && decomposition.server_avg_decode_ms > 0 ? decomposition.server_avg_decode_ms : decomposition.client_avg_decode_ms) ?? 0
  const e2eMs = decomposition.total_e2e_avg_ms ?? decomposition.client_avg_e2e_ms ?? 0

  return (
    <section aria-label="LLM latency decomposition">
      <h4>End-to-end latency decomposition</h4>
      <p className="run-report-muted">
        Decomposition source: {decomposition.source} across {sampleCount} streamed request(s).
        {est
          ? ` Little's Law in-flight concurrency: ${formatFixed(est.estimated_in_flight, 2)} total (${formatFixed(est.prefill_in_flight, 2)} prefill, ${formatFixed(est.decode_in_flight, 2)} decode).`
          : ''}
      </p>
      <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="LLM latency breakdown table">
        <table className="run-report-table">
          <thead>
            <tr>
              {['Network and gateway (ms)', 'Server queue (ms)', 'Prefill (ms)', 'Decode (ms)', 'Total E2E (ms)'].map(label => (
                <th key={label} scope="col">
                  {label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr>
              <td>{formatFixed(decomposition.network_gateway_avg_ms, 1)}</td>
              <td>{formatFixed(queueMs, 1)}</td>
              <td>{formatFixed(prefillMs, 1)}</td>
              <td>{formatFixed(decodeMs, 1)}</td>
              <td>{formatFixed(e2eMs, 1)}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  )
}
