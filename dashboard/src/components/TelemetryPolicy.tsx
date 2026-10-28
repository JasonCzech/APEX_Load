import { useEffect, useState } from 'react'
import { TELEMETRY_ADMISSION_BOUNDARY, TELEMETRY_ANY_BOUNDARY, telemetryPolicyChosen, telemetryPolicyView, telemetryStageLabels, type TelemetryPolicyView } from './telemetryPolicyModel'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'

interface Props {
  evidence?: unknown
  /** Live evidence may be an in-run policy segment's receipt (TEL-B); an archive's admission evidence never is. */
  acceptSegments?: boolean
  /** The segment the receipt is, when known (from the run's segments). */
  segmentSequence?: number
}

export default function TelemetryPolicy({ evidence, acceptSegments = false, segmentSequence }: Props) {
  const [verified, setVerified] = useState<{ evidence: unknown; view: TelemetryPolicyView }>()
  useEffect(() => {
    let active = true
    void telemetryPolicyView(evidence, acceptSegments ? TELEMETRY_ANY_BOUNDARY : TELEMETRY_ADMISSION_BOUNDARY).then(view => { if (active) setVerified({ evidence, view }) })
    return () => { active = false }
  }, [evidence, acceptSegments])
  // A changed response cannot display the previous receipt while its digest is
  // pending. Cleanup fences delayed completions after replacement or unmount.
  const { policy, reason } = verified && verified.evidence === evidence ? verified.view : { reason: 'Verifying the retained policy digest…' }
  return <section className="goal-status-card telemetry-policy-card run-report-panel" aria-label="Telemetry budget policy">
    <h3>Telemetry budget policy</h3>
    {policy ? <>
      {policy.capture_boundary === 'in-run-policy-segment'
        ? <p className="telemetry-policy-lead">{segmentSequence ? `Segment ${segmentSequence}, changed while the run ran` : "Captured when the run's policy changed while it ran"}: immutable receipt classifying the live telemetry from that change on. Later system settings or policy revisions do not rewrite these budgets.</p>
        : <p className="telemetry-policy-lead">Immutable receipt captured before runtime staging. Later system settings or policy revisions do not rewrite these budgets.</p>}
      <div className="telemetry-policy-group">
        <h4 className="telemetry-policy-group-title">Admission &amp; provenance receipt</h4>
        <dl className="manifest-definition-grid telemetry-policy-meta-grid">
          <div className="manifest-definition"><dt>Policy</dt><dd>{telemetryPolicyChosen(policy)
            ? <>Project policy {policy.policy_id} · revision {policy.revision} ({policy.capture_boundary === 'in-run-policy-segment' ? 'chosen while the run ran' : 'chosen by the run'})</>
            : <>{policy.capture_boundary === 'in-run-policy-segment' ? 'Threshold settings when the policy changed' : 'System threshold settings'} ({policy.policy_id} · revision {policy.revision})</>}</dd></div>
          <div className="manifest-definition"><dt>Captured at</dt><dd>{formatDisplayDateTime(policy.captured_at, { seconds: true })}</dd></div>
          <div className="manifest-definition"><dt>Controller publication interval</dt><dd>{policy.expected_controller_publication_interval_ms.toLocaleString()} ms · {policy.cadence_scope === 'local-controller-ticker' ? 'local controller ticker' : 'distributed controller coalescing gate'}</dd></div>
          <div className="manifest-definition"><dt>Sustained warning window</dt><dd>{policy.sustained_breach_window_ms.toLocaleString()} ms</dd></div>
          <div className="manifest-definition"><dt>Classification algorithm</dt><dd>{policy.classification_algorithm}</dd></div>
          {policy.definition_sha256 ? <div className="manifest-definition telemetry-policy-digest-cell"><dt>Definition digest</dt><dd className="telemetry-policy-digest"><code>{policy.definition_sha256}</code></dd></div> : null}
          <div className="manifest-definition telemetry-policy-digest-cell"><dt>Policy hash</dt><dd className="telemetry-policy-digest"><code>{policy.hash}</code></dd></div>
        </dl>
      </div>
      <div className="telemetry-policy-group">
        <h4 className="telemetry-policy-group-title">Stage latency budgets</h4>
        <dl className="manifest-definition-grid telemetry-policy-budgets-grid" aria-label="Retained telemetry stage budgets">
          {policy.budgets.map(budget => <div key={budget.stage} className={`manifest-definition telemetry-budget-cell ${budget.enabled ? 'telemetry-budget-enabled' : 'telemetry-budget-disabled'}`}><dt>{telemetryStageLabels[budget.stage]}</dt><dd>{budget.enabled ? `${budget.limit_ms.toString()} ms` : 'Disabled (explicit zero)'}</dd></div>)}
        </dl>
      </div>
    </> : <p>{reason}</p>}
    <p className="telemetry-policy-disclaimer">Policy availability describes retained settings. It does not establish synchronized clocks, wall-time coverage, exact loss totals, or a telemetry CI verdict. Missing intervals remain unknown.</p>
  </section>
}
