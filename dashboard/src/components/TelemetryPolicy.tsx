import { useEffect, useState } from 'react'
import { telemetryPolicyChosen, telemetryPolicyView, telemetryStageLabels, type TelemetryPolicyView } from './telemetryPolicyModel'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'

export default function TelemetryPolicy({ evidence }: { evidence?: unknown }) {
  const [verified, setVerified] = useState<{ evidence: unknown; view: TelemetryPolicyView }>()
  useEffect(() => {
    let active = true
    void telemetryPolicyView(evidence).then(view => { if (active) setVerified({ evidence, view }) })
    return () => { active = false }
  }, [evidence])
  // A changed response cannot display the previous receipt while its digest is
  // pending. Cleanup fences delayed completions after replacement or unmount.
  const { policy, reason } = verified && verified.evidence === evidence ? verified.view : { reason: 'Verifying the retained policy digest…' }
  return <section className="goal-status-card telemetry-policy-card" aria-label="Telemetry budget policy">
    <h3>Telemetry budget policy</h3>
    {policy ? <>
      <p>Immutable receipt captured before runtime staging. Later system settings or policy revisions do not rewrite these budgets.</p>
      <dl className="manifest-definition-grid">
        <div><dt>Policy</dt><dd>{telemetryPolicyChosen(policy)
          ? <>Project policy {policy.policy_id} · revision {policy.revision} (chosen by the run)</>
          : <>System threshold settings ({policy.policy_id} · revision {policy.revision})</>}</dd></div>
        {policy.definition_sha256 ? <div><dt>Definition digest</dt><dd style={{ overflowWrap: 'anywhere' }}><code>{policy.definition_sha256}</code></dd></div> : null}
        <div><dt>Policy hash</dt><dd style={{ overflowWrap: 'anywhere' }}><code>{policy.hash}</code></dd></div>
        <div><dt>Captured at</dt><dd>{formatDisplayDateTime(policy.captured_at, { seconds: true })}</dd></div>
        <div><dt>Controller publication interval</dt><dd>{policy.expected_controller_publication_interval_ms.toLocaleString()} ms · {policy.cadence_scope === 'local-controller-ticker' ? 'local controller ticker' : 'distributed controller coalescing gate'}</dd></div>
        <div><dt>Sustained warning window</dt><dd>{policy.sustained_breach_window_ms.toLocaleString()} ms</dd></div>
        <div><dt>Classification algorithm</dt><dd>{policy.classification_algorithm}</dd></div>
      </dl>
      <dl className="manifest-definition-grid" aria-label="Retained telemetry stage budgets">
        {policy.budgets.map(budget => <div key={budget.stage}><dt>{telemetryStageLabels[budget.stage]}</dt><dd>{budget.enabled ? `${budget.limit_ms.toString()} ms` : 'Disabled (explicit zero)'}</dd></div>)}
      </dl>
    </> : <p>{reason}</p>}
    <p>Policy availability describes retained settings. It does not establish synchronized clocks, wall-time coverage, exact loss totals, or a telemetry CI verdict. Missing intervals remain unknown.</p>
  </section>
}
