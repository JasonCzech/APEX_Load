import { TxMetrics } from '../types'
import { diagnosticMean, grpcSummaryLines } from '../utils/grpcDiagnostics'

export default function GRPCDiagnosticsPanel({ transactions }: { transactions: TxMetrics[] }) {
  const measured = transactions.filter(tx => tx.server_telemetry?.grpc)
    .sort((a, b) => (diagnosticMean(b.server_telemetry!.grpc!.call_ms) ?? 0) - (diagnosticMean(a.server_telemetry!.grpc!.call_ms) ?? 0))
    .slice(0, 4)
  if (measured.length === 0) return null
  return (
    <section className="tx-section server-telemetry-section" aria-label="gRPC diagnostics">
      <h3 className="chart-title">gRPC diagnostics</h3>
      <p className="server-telemetry-subtitle">Client-observed RPC timings from retained diagnostic samples. A receive limit leaves the final status unavailable.</p>
      <div className="server-telemetry-grid">
        {measured.map(tx => (
          <div className="server-telemetry-card" key={tx.name}>
            <div className="server-telemetry-card-header"><span className="server-telemetry-name">{tx.name}</span></div>
            {grpcSummaryLines(tx.server_telemetry!.grpc!).map(line => <p key={line}>{line}</p>)}
          </div>
        ))}
      </div>
    </section>
  )
}
