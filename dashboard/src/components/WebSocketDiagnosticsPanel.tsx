import type { TxMetrics } from '../types'
import { websocketSummaryLines } from '../utils/websocketDiagnostics'

export default function WebSocketDiagnosticsPanel({ transactions }: { transactions: TxMetrics[] }) {
  const measured = transactions.filter(tx => tx.server_telemetry?.websocket).slice(0, 8)
  if (!measured.length) return null
  return <section className="tx-section server-telemetry-section" aria-label="WebSocket diagnostics">
    <h3 className="chart-title">WebSocket diagnostics</h3>
    <p className="server-telemetry-subtitle">Receive wait measures time waiting for a message. Round-trip timing is available only for matched authored ping/pong messages.</p>
    <div className="server-telemetry-grid">
      {measured.map(tx => <div className="server-telemetry-card" key={tx.name}>
        <h4>{tx.name}</h4>
        {websocketSummaryLines(tx.server_telemetry!.websocket!).map(line => <p key={line}>{line}</p>)}
        {tx.server_telemetry?.diagnostic_findings?.filter(f => f.protocol === 'websocket').map(f => <p key={f.rule}><strong>Advisory: </strong>{f.message}</p>)}
      </div>)}
    </div>
  </section>
}
