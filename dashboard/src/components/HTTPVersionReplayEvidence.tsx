import type { ProtocolEvidence } from '../types/replay'
import { httpReplayVersionEvidence } from './httpActionVersionModel'

export function HTTPVersionReplayEvidence({ evidence, unavailable = false }: { evidence?: ProtocolEvidence; unavailable?: boolean }) {
  const receipt = unavailable ? null : httpReplayVersionEvidence(evidence)
  if (!receipt) return null
  return <section className="sub-section" aria-label="HTTP version evidence">
    <dl><dt>Replay policy</dt><dd>{receipt.policy}</dd>
      <dt>Actual negotiated HTTP version</dt><dd>{receipt.actual}</dd>
      <dt>Version evidence source</dt><dd>{receipt.source}</dd></dl>
    {receipt.http3.length > 0 && <dl aria-label="HTTP/3 receipt">{receipt.http3.map(row => <div key={row.label}><dt>{row.label}</dt><dd>{row.value}</dd></div>)}</dl>}
    {receipt.advertisesHTTP3 && (receipt.upgradesLater
      ? <p>The target advertises HTTP/3 (Alt-Svc). This request used {receipt.actual.startsWith('HTTP/') ? receipt.actual : 'another HTTP version'}; later requests to this origin can use HTTP/3.</p>
      : <p>The target advertises HTTP/3 (Alt-Svc). This request used {receipt.actual.startsWith('HTTP/') ? receipt.actual : 'another HTTP version'}, so HTTP/3 was not exercised.</p>)}
    <small>Negotiated versions describe the observed response. Cached responses and missing wire evidence do not establish an HTTP version.</small>
  </section>
}
