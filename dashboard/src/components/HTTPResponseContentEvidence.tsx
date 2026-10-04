import type { ProtocolEvidence } from '../types/replay'
import { httpResponseContentEvidence } from './httpResponseContentEvidenceModel'

export function HTTPResponseContentEvidence({ evidence, bodyState, unavailable = false }: { evidence?: ProtocolEvidence; bodyState?: string; unavailable?: boolean }) {
  const receipt = unavailable ? null : httpResponseContentEvidence(evidence, bodyState)
  if (!receipt) return null
  return <section className="sub-section" aria-label="HTTP response content evidence">
    <dl><dt>Response content status</dt><dd>{receipt.state}</dd>
      <dt>Body representation</dt><dd>{receipt.basis}</dd></dl>
    <small>{receipt.qualification}</small>
  </section>
}
