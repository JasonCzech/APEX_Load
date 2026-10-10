import { useState } from 'react'
import type { ProtocolEvidence } from '../types/replay'
import { httpReplayVersionEvidence } from './httpActionVersionModel'
import { isHTTP3Policy } from './httpVersionModel'

export type HTTP3UpgradeScope = 'request' | 'script'

/** Offered when the replayed draft is still the current one, so the edit lands on this request. */
export type HTTP3UpgradeOffer = {
  scopes: HTTP3UpgradeScope[]
  onSwitch: (scope: HTTP3UpgradeScope) => void
}

const SCOPE_LABELS: Record<HTTP3UpgradeScope, { button: string; done: string }> = {
  request: { button: 'Switch this request to auto+h3', done: 'this request' },
  script: { button: 'Switch the script default to auto+h3', done: 'the script default' },
}

export function HTTPVersionReplayEvidence({ evidence, unavailable = false, upgrade }: { evidence?: ProtocolEvidence; unavailable?: boolean; upgrade?: HTTP3UpgradeOffer }) {
  const [switched, setSwitched] = useState<HTTP3UpgradeScope | null>(null)
  const receipt = unavailable ? null : httpReplayVersionEvidence(evidence)
  if (!receipt) return null
  const used = receipt.actual.startsWith('HTTP/') ? receipt.actual : 'another HTTP version'
  // auto+h3 already acts on the advertisement, and exact 3 needs no upgrade.
  const offer = receipt.advertisesHTTP3 && !isHTTP3Policy(evidence?.http_version_policy) && upgrade && upgrade.scopes.length > 0 ? upgrade : undefined
  return <section className="sub-section" aria-label="HTTP version evidence">
    <dl><dt>Replay policy</dt><dd>{receipt.policy}</dd>
      <dt>Actual negotiated HTTP version</dt><dd>{receipt.actual}</dd>
      <dt>Version evidence source</dt><dd>{receipt.source}</dd></dl>
    {receipt.http3.length > 0 && <dl aria-label="HTTP/3 receipt">{receipt.http3.map(row => <div key={row.label}><dt>{row.label}</dt><dd>{row.value}</dd></div>)}</dl>}
    {receipt.advertisesHTTP3 && (receipt.upgradesLater
      ? <p>The target advertises HTTP/3 (Alt-Svc). This request used {used}. auto+h3 uses a same-host HTTP/3 alternative for later requests to this origin.</p>
      : offer && !switched
        ? <div className="http3-upgrade-offer" role="group" aria-label="Switch to auto+h3">
          <p>The origin advertises HTTP/3; this request used {used}. Switch to auto+h3?</p>
          <p className="form-hint">auto+h3 starts on TCP and uses HTTP/3 once the origin advertises it, falling back to TCP only when the request was not sent.</p>
          {offer.scopes.map(scope => <button key={scope} type="button" className="btn btn-sm btn-secondary" onClick={() => { setSwitched(scope); offer.onSwitch(scope) }}>{SCOPE_LABELS[scope].button}</button>)}
        </div>
        : <p>The target advertises HTTP/3 (Alt-Svc). This request used {used}, so HTTP/3 was not exercised.</p>)}
    {switched && <p role="status">Switched {SCOPE_LABELS[switched].done} to auto+h3. The script is being validated again; replay it to exercise HTTP/3.</p>}
    <small>Negotiated versions describe the observed response. Cached responses and missing wire evidence do not establish an HTTP version.</small>
  </section>
}
