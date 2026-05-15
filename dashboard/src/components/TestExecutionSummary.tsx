import { LiveMetrics, TxMetrics } from '../types'

interface MetricsSummary {
  p50_ms?: number
  p90_ms?: number
  p95_ms?: number
  p99_ms?: number
}

interface Props {
  transactions: TxMetrics[]
  metrics: MetricsSummary | null | undefined
  history?: LiveMetrics[]
}

export default function TestExecutionSummary({ transactions, metrics, history }: Props) {
  const previousTransactions = previousTransactionMap(history)

  return (
    <>
      {transactions.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Transaction Breakdown</h3>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Transaction</th>
                <th>Count</th>
                <th>Errors</th>
                <th>Avg (ms)</th>
                <th>P50 (ms)</th>
                <th>P90 (ms)</th>
                <th>P99 (ms)</th>
                <th>Server Telemetry</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map((tx) => (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{tx.count}</td>
                  <td className={tx.errors > 0 ? 'error-cell' : ''}>{tx.errors}</td>
                  <td>{tx.avg_ms?.toFixed(1)}</td>
                  <td>{tx.p50_ms?.toFixed(1)}</td>
                  <td>{tx.p90_ms?.toFixed(1)}</td>
                  <td>{tx.p99_ms?.toFixed(1)}</td>
                  <td>{renderServerTelemetry(tx, previousTransactions[tx.name])}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {metrics && (
        <div className="percentile-bar">
          <div className="pct-item">
            <span className="pct-label">P50</span>
            <span className="pct-value">{metrics.p50_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P90</span>
            <span className="pct-value">{metrics.p90_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P95</span>
            <span className="pct-value">{metrics.p95_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P99</span>
            <span className="pct-value">{metrics.p99_ms?.toFixed(1)} ms</span>
          </div>
        </div>
      )}
    </>
  )
}

function renderServerTelemetry(current: TxMetrics, previous?: TxMetrics) {
  if (!current.server_telemetry) {
    return 'n/a'
  }
  const telemetry = current.server_telemetry
  const previousTelemetry = previous?.server_telemetry
  const parts = [
    `Server ${formatTelemetryValue(telemetry.avg_server_processing_ms, ' ms')}${formatTelemetryDelta(telemetry.avg_server_processing_ms, previousTelemetry?.avg_server_processing_ms, ' ms')}`,
    `Transfer ${formatTelemetryValue(telemetry.avg_transfer_ms, ' ms')}${formatTelemetryDelta(telemetry.avg_transfer_ms, previousTelemetry?.avg_transfer_ms, ' ms')}`,
    `Payload ${formatTelemetryValue(telemetry.avg_request_payload_kb, ' KB', 2)} / ${formatTelemetryValue(telemetry.avg_response_payload_kb, ' KB', 2)}`,
    `Reuse ${formatTelemetryValue(telemetry.connection_reused_pct, '%', 0)}${formatTelemetryDelta(telemetry.connection_reused_pct, previousTelemetry?.connection_reused_pct, '%', 0)}`,
  ]
  return parts.join(' | ')
}

function previousTransactionMap(history?: LiveMetrics[]): Record<string, TxMetrics> {
  if (!history || history.length < 2) {
    return {}
  }
  for (let index = history.length - 2; index >= 0; index -= 1) {
    const snapshot = history[index]
    if (snapshot?.transactions) {
      return snapshot.transactions
    }
  }
  return {}
}

function formatTelemetryValue(value?: number, suffix = ' ms', digits = 1): string {
  if (value === undefined || Number.isNaN(value)) {
    return 'n/a'
  }
  return `${value.toFixed(digits)}${suffix}`
}

function formatTelemetryDelta(current?: number, previous?: number, suffix = ' ms', digits = 1): string {
  if (current === undefined || previous === undefined || Number.isNaN(current) || Number.isNaN(previous)) {
    return ''
  }
  const delta = current - previous
  if (Math.abs(delta) < Math.pow(10, -digits)) {
    return ' (steady)'
  }
  return ` (${delta > 0 ? '+' : ''}${delta.toFixed(digits)}${suffix} vs prev)`
}