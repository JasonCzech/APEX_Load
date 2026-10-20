import type { JavaHTTPActions } from '../types/javaHttp'
import { javaHTTPSummaryLines } from '../utils/javaHttpDiagnostics'

export default function JavaHTTPDiagnostics({ evidence }: { evidence?: JavaHTTPActions | null }) {
  if (!evidence) return null
  const entries = Object.entries(evidence.actions ?? {})
  const unavailable = evidence.version !== 1 || !!evidence.unavailable_reason || entries.length > 128
  return <section className="tx-section server-telemetry-section" aria-label="Java HTTP representation diagnostics">
    <h3 className="chart-title">Java HTTP representation diagnostics</h3>
    <p>Byte counts describe opaque request and retained response representations. They exclude network framing, retries and delivery. JVM object footprint is unavailable in this execution mode.</p>
    {unavailable ? <p role="status">Diagnostics unavailable: this evidence could not be retained under the supported contract.</p> : <>
      {evidence.incomplete && <p>Source coverage incomplete. Values describe the retained observations.</p>}
      {entries.sort(([a], [b]) => a.localeCompare(b)).map(([name, action]) => <details className="server-telemetry-card" key={name}>
        <summary>{name}</summary>
        {javaHTTPSummaryLines(action.diagnostics, action.population).map(line => <p key={line}>{line}</p>)}
      </details>)}
    </>}
  </section>
}
