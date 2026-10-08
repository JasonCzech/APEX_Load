import { useEffect, useState, type FormEvent } from 'react'

import { useApi } from '../../hooks/useApi'
import { getErrorMessage } from '../../utils/errorUtils'
import { formatJourneyBoundary, formatJourneyFraction, journeyUnavailableText, journeyUnscoredText, type JourneyRiskReport } from './journeyRiskModel'

interface JourneySelection {
  policy?: string
  revision?: number
}

// JourneyRiskPanel shows a finalized run's journey accounting under its
// pinned journey policy, or another one the viewer names.
export default function JourneyRiskPanel({ testId }: { testId: string }) {
  const { getTestArchiveJourneys } = useApi()
  const [selection, setSelection] = useState<JourneySelection>({})
  const [policyDraft, setPolicyDraft] = useState('')
  const [revisionDraft, setRevisionDraft] = useState('')
  const [report, setReport] = useState<JourneyRiskReport | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    getTestArchiveJourneys(testId, selection, { signal: controller.signal })
      .then(next => { if (!controller.signal.aborted) setReport(next) })
      .catch(err => {
        if (controller.signal.aborted) return
        setReport(null)
        setError(getErrorMessage(err, 'Unable to load journey accounting'))
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [getTestArchiveJourneys, testId, selection])

  const apply = (event: FormEvent) => {
    event.preventDefault()
    const policy = policyDraft.trim()
    const revision = Number.parseInt(revisionDraft, 10)
    setSelection(policy ? { policy, revision: Number.isFinite(revision) && revision > 0 ? revision : undefined } : {})
  }

  return <section className="run-report-panel journey-risk" aria-label="Journey risk">
    <h2>Journey risk</h2>
    <form className="run-report-toolbar-actions" onSubmit={apply} aria-label="Journey policy">
      <label>Journey policy <input value={policyDraft} onChange={event => setPolicyDraft(event.target.value)} placeholder="Run's pinned policy" maxLength={128} /></label>
      <label>Revision <input value={revisionDraft} onChange={event => setRevisionDraft(event.target.value)} placeholder="Latest" inputMode="numeric" size={6} /></label>
      <button type="submit" className="btn btn-secondary" disabled={loading}>Apply</button>
    </form>
    {loading && <p role="status">Accounting journeys...</p>}
    {!loading && error && <p role="alert" className="run-report-error">{error}</p>}
    {!loading && report && report.availability !== 'available' && <p>{journeyUnavailableText(report.reason)}</p>}
    {!loading && report && report.availability === 'available' && <>
      <p>
        {report.policy.name || report.policy.id} revision {report.policy.revision} ({report.policy.source === 'pinned' ? "the run's pinned policy" : 'requested'}).{' '}
        <strong>Weighted failure index:</strong> {report.weighted_failure_index !== undefined ? formatJourneyFraction(report.weighted_failure_index) : journeyUnscoredText(report.index_unscored)}
        {report.step_risk_enabled && <> <strong>Step risk index:</strong> {report.step_risk_index !== undefined ? formatJourneyFraction(report.step_risk_index) : journeyUnscoredText(report.step_risk_unscored)}</>}
      </p>
      {report.incomplete && <p role="note">Some rows were past the accounting bounds, so the counts are incomplete.</p>}
      <div className="run-archive-coverage-table"><table className="tx-table">
        <thead><tr>
          <th scope="col">Journey</th><th scope="col">Weight</th><th scope="col">Completed</th><th scope="col">Failed</th><th scope="col">Aborted</th>
          <th scope="col">Skipped</th><th scope="col">Censored</th><th scope="col">Failure rate</th><th scope="col">Step failures</th>
        </tr></thead>
        <tbody>{report.journeys.map(journey => <tr key={journey.id}>
          <td><span title={`From ${formatJourneyBoundary(journey.start)} to ${formatJourneyBoundary(journey.success)}`}>{journey.name}</span></td>
          <td>{journey.weight}</td><td>{journey.completed}</td><td>{journey.failed}</td><td>{journey.aborted}</td>
          <td>{journey.skipped}</td><td>{journey.censored}</td><td>{formatJourneyFraction(journey.failure_rate)}</td>
          <td>{journey.step_failures} of {journey.steps}</td>
        </tr>)}</tbody>
      </table></div>
      {report.journeys.some(journey => journey.examples?.length) && <details>
        <summary>Example attempts</summary>
        <ul>{report.journeys.flatMap(journey => (journey.examples || []).map((example, index) => <li key={`${journey.id}-${index}`}>
          {journey.name}: {example.outcome} — group {example.group}, VUser {example.vuser_id}, iteration {example.iteration}{example.failed_step ? `, first failed step ${example.failed_step}` : ''}
        </li>))}</ul>
      </details>}
      <ul className="journey-risk-notes">{report.notes.map(note => <li key={note}>{note}</li>)}</ul>
    </>}
  </section>
}
