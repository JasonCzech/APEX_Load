import type { ProtocolEvidence } from '../types/replay'
import { httpReplayVersionEvidence } from './httpActionVersionModel'

export function HTTPVersionReplayEvidence({ evidence, unavailable = false }: { evidence?: ProtocolEvidence; unavailable?: boolean }) {
  const receipt = unavailable ? null : httpReplayVersionEvidence(evidence)
  if (!receipt) return null
  return <section className="sub-section" aria-label="HTTP version evidence">
    <dl><dt>Replay policy</dt><dd>{receipt.policy}</dd>
      <dt>Actual negotiated HTTP version</dt><dd>{receipt.actual}</dd>
      <dt>Version evidence source</dt><dd>{receipt.source}</dd></dl>
    <small>Negotiated versions describe the observed response. Cached responses and missing wire evidence do not establish an HTTP version.</small>
  </section>
}
