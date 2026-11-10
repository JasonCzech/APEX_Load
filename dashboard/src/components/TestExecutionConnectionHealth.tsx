import type { LiveMetrics } from '../types'
import { formatWaterfallMs } from './waterfall/phases'
import { PHASE_PERCENTILES_NOT_REPORTED_TEXT, SETUP_UNMEASURED_HINT, SETUP_UNMEASURED_TEXT, phasePercentileMs, reuseBasisText, reuseValueText } from './livePhaseModel'
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
const RESTORED_PERCENTILES_HINT = 'Restored from stored history, which keeps no per-phase percentiles; the average is in the tile beside'

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
  const percentiles = health.phasePercentiles
  // Per-phase P95 over the samples with the phase; unknown (an older load
  // generator) falls back to the average tile beside it, never to 0.
  const p95Tile = (phase: 'tls' | 'dns', what: string) => {
    if (!observed) return { value: unobservedValue, hint: unobservedHint }
    if (!percentiles) return { value: undefined, hint: health.fromHistory ? RESTORED_PERCENTILES_HINT : `${PHASE_PERCENTILES_NOT_REPORTED_TEXT}; the average is in the tile beside` }
    const row = percentiles[phase]
    const ms = phasePercentileMs(percentiles, phase, 'p95')
    if (ms === undefined || !row) return { value: 'None', hint: `No transaction sample had ${what} this window` }
    return { value: formatWaterfallMs(ms), hint: `95% of the ${row.samples.toLocaleString()} transaction samples with ${what} spent at most this long in it; one with several counts their total` }
  }
  const tlsP95 = p95Tile('tls', 'a handshake')
  const dnsP95 = p95Tile('dns', 'a lookup')
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
        <Tile label="TLS P95 per transaction with a handshake" value={tlsP95.value} hint={tlsP95.hint} />
        <Tile
          label="DNS time per transaction with a lookup"
          value={observed ? (stats.avgDnsLookupMs === undefined ? 'None' : formatWaterfallMs(stats.avgDnsLookupMs)) : unobservedValue}
          hint={observed ? `Average over the ${(stats.dnsSamples ?? 0).toLocaleString()} transaction samples with any lookup this window; one with several lookups counts their total` : unobservedHint}
        />
        <Tile label="DNS P95 per transaction with a lookup" value={dnsP95.value} hint={dnsP95.hint} />
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
      <p className="connection-health-note">{runFinished ? 'The run has ended: these are averages over the transaction samples of its last window.' : 'Averages over the latest window\u2019s transaction samples.'} The P95 tiles are percentiles over the samples that spent time in that phase{percentiles ? '' : '; this window has none, so only the averages are shown'}.</p>
    </section>
  )
}
