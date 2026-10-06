import type { ReportRootCause } from '../../types/rca'
import { rcaStatusLabel, rootCauseSourceLabel } from './rcaModel'

// ReportRootCauseView previews the report's generated root cause as the
// exported report's rca section renders it, showing at most limit
// hypotheses (the profile's root_cause_hypothesis_limit). Analyst findings
// are edited and shown apart from it.
export default function ReportRootCauseView({ view, limit }: { view: ReportRootCause; limit?: number }) {
  const shown = limit && limit > 0 ? view.hypotheses.slice(0, limit) : view.hypotheses
  return (
    <div className="report-root-cause" data-source={view.source}>
      <p className="report-hint">{rootCauseSourceLabel(view)}. Generated from archived evidence; analyst findings are reported separately.</p>
      <p><strong>{rcaStatusLabel(view.outcome)}.</strong> {view.summary}</p>
      {view.validity && <p>Run validity: {rcaStatusLabel(view.validity)}{view.validity_reasons?.map((reason, i) => <span key={i}> · {reason}</span>)}</p>}
      {view.notes?.map((note, i) => <p key={i} className="report-hint">{note}</p>)}
      {shown.length > 0 && <ol className="report-list">
        {shown.map(h => (
          <li key={h.rank}>
            <strong>{h.title}</strong> ({h.confidence ? `${h.confidence} confidence` : rcaStatusLabel(h.severity || 'unrated')})
            {h.explains?.length ? <div>Explains: {h.explains.join(', ')}</div> : null}
            {h.detail && <div>{h.detail}</div>}
            {h.chain?.map((step, i) => <div key={i}>Evidence: {step}</div>)}
            {view.source === 'v2' && <div>Contradicting evidence: {h.contradicting?.length ? h.contradicting.join(', ') : 'None found'}</div>}
            {view.source === 'v2' && <div>Would disprove it: {h.falsification || 'Not provided'}</div>}
          </li>
        ))}
      </ol>}
      {view.hypotheses.length > shown.length && <p className="report-hint">{view.hypotheses.length - shown.length} more ranked in the Detailed Technical report.</p>}
    </div>
  )
}
