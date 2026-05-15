import { RunComparisonBaseline, TxMetrics } from '../types'

interface MetricsSummary {
  p50_ms?: number
  p90_ms?: number
  p95_ms?: number
  p99_ms?: number
}

interface Props {
  transactions: TxMetrics[]
  metrics: MetricsSummary | null | undefined
  baseline?: RunComparisonBaseline
}

export default function TestExecutionSummary({ transactions, metrics, baseline }: Props) {
  const previousTransactions = baseline?.transactions || {}
  const topServerTelemetry = transactions
    .filter((tx) => tx.server_telemetry)
    .sort((left, right) => (right.server_telemetry?.avg_server_processing_ms || 0) - (left.server_telemetry?.avg_server_processing_ms || 0))
    .slice(0, 4)

  return (
    <>
      {topServerTelemetry.length > 0 && (
        <div className="tx-section server-telemetry-section">
          <div className="server-telemetry-header">
            <div>
              <h3 className="chart-title">Live Server Telemetry</h3>
              <p className="server-telemetry-subtitle">
                Server-processing and transfer hotspots from the current run.
                {baseline?.source_test_name ? ` Baseline: ${baseline.source_test_name}` : ' No previous run baseline available.'}
              </p>
            </div>
            {baseline?.completed_at && (
              <div className="server-telemetry-baseline-badge">
                Prev run {new Date(baseline.completed_at).toLocaleString()}
              </div>
            )}
          </div>
          <div className="server-telemetry-grid">
            {topServerTelemetry.map((tx) => {
              const previous = previousTransactions[tx.name]
              return (
                <div key={tx.name} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{tx.name}</span>
                    <span className="server-telemetry-target">{tx.dependency_target || tx.dependency_type || 'transaction'}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Server</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.avg_server_processing_ms, ' ms')}{formatTelemetryDelta(tx.server_telemetry?.avg_server_processing_ms, previous?.server_telemetry?.avg_server_processing_ms, ' ms')}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Transfer</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.avg_transfer_ms, ' ms')}{formatTelemetryDelta(tx.server_telemetry?.avg_transfer_ms, previous?.server_telemetry?.avg_transfer_ms, ' ms')}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Payload</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.avg_request_payload_kb, ' KB', 2)} / {formatTelemetryValue(tx.server_telemetry?.avg_response_payload_kb, ' KB', 2)}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Reuse</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.connection_reused_pct, '%', 0)}{formatTelemetryDelta(tx.server_telemetry?.connection_reused_pct, previous?.server_telemetry?.connection_reused_pct, '%', 0)}</span>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}

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