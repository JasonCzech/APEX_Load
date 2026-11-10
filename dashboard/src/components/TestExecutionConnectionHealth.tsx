import type { LiveMetrics } from '../types'
import { formatWaterfallMs } from './waterfall/phases'
import { SETUP_UNMEASURED_HINT, SETUP_UNMEASURED_TEXT, reuseBasisText, reuseValueText } from './livePhaseModel'
import { EPHEMERAL_PORT_WARNING_PCT, connectionHealth, type PortPressure } from './connectionHealthModel'
import './LiveWaterfallPanel.css'

interface Props {
  metrics?: LiveMetrics
  /** The run has ended: the window is the last one, and no further window will arrive. */
  runFinished?: boolean
}

const NOT_REPORTED = 'Not reported'

function Tile({ label, value, hint, warning }: { label: string; value?: string; hint: string; warning?: boolean }) {
  return (
    <div className={`connection-health-tile${warning ? ' warning' : ''}`}>
      <dt>{label}</dt>
      <dd className={value === undefined ? 'not-reported' : undefined}>{value ?? NOT_REPORTED}</dd>
      <dd><small>{hint}</small></dd>
    </div>
  )
}

const where = (p: PortPressure | undefined) => (p?.inferredZero ? 'Measured 0 (zero counts are omitted on the wire)' : p?.generatorId ? `Highest generator: ${p.generatorId}` : 'Fleet resources')
const NOT_REPORTED_SOCKETS = 'Generators sent no TCP socket diagnostics (not collected on this OS, or no connections open)'
// History keeps no resources: the generators may well have reported them live.
const NOT_KEPT_SOCKETS = 'Not kept in run history: TCP socket diagnostics are live-only, and this window was restored from stored history'
const RESTORED_PHASES_HINT = 'Restored from stored history (after a controller restart or failover); phases were not kept for this window'
// The socket table was read, but no port share came with it: never shown as a measured 0.
const NOT_REPORTED_PORT_SHARE = 'No generator sent a port share: its local port range could not be read, or no ephemeral port was in use'

/** L4: latest-window connection reuse, setup cost and port pressure. */
export default function TestExecutionConnectionHealth({ metrics, runFinished = false }: Props) {
  const health = connectionHealth(metrics, { runFinished })
  if (!health) {
    return <div className="empty-card"><p>Waiting for live metrics.</p><p className="empty-hint">Connection health appears with the first live window.</p></div>
  }
  const { stats, samples } = health
  // No setup or first-byte timing at all (gRPC, JDBC, messaging, browser): not 100% reuse.
  const unmeasured = health.observed && stats.setupUnmeasured === true
  const observed = health.observed && !unmeasured
  // A restored row lacks phases because history did not keep them, not because a generator is old.
  const unobservedHint = unmeasured ? SETUP_UNMEASURED_HINT : samples > 0 ? (health.fromHistory ? RESTORED_PHASES_HINT : 'A generator predates phase observations') : 'No transaction samples this window'
  const noSockets = health.fromHistory ? NOT_KEPT_SOCKETS : NOT_REPORTED_SOCKETS
  const unobservedValue = unmeasured ? SETUP_UNMEASURED_TEXT : undefined
  const portPct = health.ephemeralPortPct
  const portWarning = portPct !== undefined && portPct.value >= EPHEMERAL_PORT_WARNING_PCT
  return (
    <section className="connection-health" aria-label="Connection health">
      <dl className="connection-health-tiles">
        <Tile
          label="Transactions with no new connection"
          value={observed ? reuseValueText(stats) : unobservedValue}
          hint={observed ? reuseBasisText(stats) : unobservedHint}
        />
        <Tile
          label="Transactions opening connections / s"
          value={observed && health.newConnectionsPerSec !== undefined ? health.newConnectionsPerSec.toFixed(1) : unobservedValue}
          hint={observed ? `Transaction samples that opened one or more new connections; one that opened several counts once, so actual connections can be higher${health.windowSeconds === undefined ? ' (1 s window assumed)' : ''}` : unobservedHint}
        />
        <Tile
          label="TLS time per transaction with a handshake"
          value={observed ? (stats.avgHandshakeMs === undefined ? 'None' : formatWaterfallMs(stats.avgHandshakeMs)) : unobservedValue}
          hint={observed ? `Average over the ${(stats.tlsSamples ?? 0).toLocaleString()} transaction samples with any handshake this window; one with several handshakes counts their total, so this is not the cost of one handshake` : unobservedHint}
        />
        <Tile
          label="DNS time per transaction with a lookup"
          value={observed ? (stats.avgDnsLookupMs === undefined ? 'None' : formatWaterfallMs(stats.avgDnsLookupMs)) : unobservedValue}
          hint={observed ? `Average over the ${(stats.dnsSamples ?? 0).toLocaleString()} transaction samples with any lookup this window; one with several lookups counts their total` : unobservedHint}
        />
        <Tile
          label="TCP TIME_WAIT"
          value={health.timeWait ? Math.round(health.timeWait.value).toLocaleString() : undefined}
          hint={health.timeWait ? where(health.timeWait) : noSockets}
        />
        <Tile
          label="Ephemeral ports used"
          value={portPct ? `${portPct.value.toFixed(1)}%` : undefined}
          hint={portPct ? `${where(portPct)}${portWarning ? ' · port exhaustion risk (SNAT or local ports)' : ''}` : health.timeWait ? NOT_REPORTED_PORT_SHARE : noSockets}
          warning={portWarning}
        />
      </dl>
      {health.fromHistory && (
        <p className="connection-health-note" role="note">This window was restored from stored history (after a controller restart or failover), which keeps rate, counts and averages but no socket diagnostics{health.observed ? '' : ' and, for this window, no phases'}.{runFinished ? '' : ' The next live window replaces it.'}</p>
      )}
      <p className="connection-health-note">{runFinished ? 'The run has ended: these are averages over the transaction samples of its last window.' : 'Averages over the latest window\u2019s transaction samples.'} DNS and TLS P95 need per-phase percentiles on the live wire, which it does not carry yet.</p>
    </section>
  )
}
