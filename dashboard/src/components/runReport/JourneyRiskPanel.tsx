import { useEffect, useRef, useState, type FormEvent } from 'react'

import { useApi } from '../../hooks/useApi'
import { getErrorMessage } from '../../utils/errorUtils'
import Notice from '../ux/Notice'
import { formatJourneyBoundary, formatJourneyFraction, journeyIncompleteText, journeyUnavailableText, journeyUnscoredText, type JourneyRiskReport } from './journeyRiskModel'

interface JourneySelection {
  policy?: string
  revision?: number
}

interface JourneyLoadFailure {
  kind: 'archive_missing' | 'not_finalized' | 'policy' | 'error'
  detail: string
  // message is the plain-language refusal of a policy request.
  message?: string
}

function policyLabel(selection: JourneySelection): string {
  return `"${selection.policy}"${selection.revision ? ` revision ${selection.revision}` : ''}`
}

// A missing or not-yet-finalized archive is a state of the run, said plainly
// with a Check again (an HA controller answers 404 until the archive is
// published). A refused policy request is the viewer's to correct, said
// plainly without a retry that would fail the same way. Anything else is a
// load failure with the server's text kept behind the notice's details.
function journeyLoadFailure(err: unknown, selection: JourneySelection): JourneyLoadFailure {
  const detail = getErrorMessage(err, 'Unable to load journey accounting')
  const status = (err as { status?: unknown } | null)?.status
  if (status === 404 && /archive not found/i.test(detail)) return { kind: 'archive_missing', detail }
  if (status === 409 && /not finalized/i.test(detail)) return { kind: 'not_finalized', detail }
  if (status === 404 && /journey policy not found/i.test(detail)) {
    return { kind: 'policy', detail, message: selection.policy ? `No journey policy ${policyLabel(selection)} exists in this project.` : "The run's pinned journey policy no longer exists in this project. Name another policy to account its journeys." }
  }
  if (status === 409 && /no longer matches its pin/i.test(detail)) {
    return { kind: 'policy', detail, message: "The run's pinned journey policy revision changed after the run pinned it, so it cannot be accounted. Name a policy to account its journeys." }
  }
  if (status === 409 && /revision is invalid/i.test(detail)) {
    return { kind: 'policy', detail, message: selection.policy ? `Journey policy ${policyLabel(selection)} is not a valid journey policy.` : "The run's pinned journey policy revision is not a valid journey policy." }
  }
  if (status === 400) return { kind: 'policy', detail, message: 'The journey policy request was refused. Check the policy ID and revision.' }
  return { kind: 'error', detail }
}

// JourneyRiskPanel shows a finalized run's journey accounting under its
// pinned journey policy, or another one the viewer names.
export default function JourneyRiskPanel({ testId }: { testId: string }) {
  const { getTestArchiveJourneys } = useApi()
  const [selection, setSelection] = useState<JourneySelection>({})
  const [policyDraft, setPolicyDraft] = useState('')
  const [revisionDraft, setRevisionDraft] = useState('')
  const [report, setReport] = useState<JourneyRiskReport | null>(null)
  const [failure, setFailure] = useState<JourneyLoadFailure | null>(null)
  const [loading, setLoading] = useState(true)
  const [attempt, setAttempt] = useState(0)
  const loadedRunIdRef = useRef<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(current => loadedRunIdRef.current === testId ? current : true)
    setFailure(null)
    getTestArchiveJourneys(testId, selection, { signal: controller.signal })
      .then(next => {
        if (!controller.signal.aborted) {
          loadedRunIdRef.current = next.run_id
          setReport(next)
        }
      })
      .catch(err => {
        if (controller.signal.aborted) return
        loadedRunIdRef.current = null
        setReport(null)
        setFailure(journeyLoadFailure(err, selection))
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [getTestArchiveJourneys, testId, selection, attempt])

  const isProvisional = Boolean(report?.provisional)
  useEffect(() => {
    if (!isProvisional) return
    const timer = window.setTimeout(() => setAttempt(value => value + 1), 3000)
    return () => window.clearTimeout(timer)
  }, [isProvisional, report])

  const apply = (event: FormEvent) => {
    event.preventDefault()
    const policy = policyDraft.trim()
    const revision = Number.parseInt(revisionDraft, 10)
    loadedRunIdRef.current = null
    setReport(null)
    setSelection(policy ? { policy, revision: Number.isFinite(revision) && revision > 0 ? revision : undefined } : {})
  }

  return <section className="run-report-panel journey-risk" aria-label="Journey risk" data-provisional={isProvisional ? 'true' : undefined}>
    <h2>Journey risk</h2>
    <form className="run-report-toolbar-actions" onSubmit={apply} aria-label="Journey policy">
      <label>Journey policy <input value={policyDraft} onChange={event => setPolicyDraft(event.target.value)} placeholder="Run's pinned policy" maxLength={128} /></label>
      <label>Revision <input value={revisionDraft} onChange={event => setRevisionDraft(event.target.value)} placeholder="Latest" inputMode="numeric" size={6} /></label>
      <button type="submit" className="btn btn-secondary" disabled={loading}>Apply</button>
    </form>
    {loading && <p role="status">Accounting journeys…</p>}
    {!loading && isProvisional && <p role="note" className="run-report-provisional-notice">Provisional live journey accounting (non-verdict) — updated from active and completed VUser iterations and replaced when the archive is finalized.{report?.live_truncated ? ' Live journey event buffer reached its in-memory bound (live_truncated).' : ''}</p>}
    {!loading && failure?.kind === 'archive_missing' && <p role="note">This run's saved archive is not available yet (it may still be publishing), so its journeys cannot be accounted. <button type="button" className="btn btn-secondary" onClick={() => setAttempt(value => value + 1)}>Check again</button></p>}
    {!loading && failure?.kind === 'not_finalized' && <p role="note">Journeys are accounted once the run's archive is finalized. <button type="button" className="btn btn-secondary" onClick={() => setAttempt(value => value + 1)}>Check again</button></p>}
    {!loading && failure?.kind === 'policy' && <Notice tone="error" title={failure.message} details={failure.detail} />}
    {!loading && failure?.kind === 'error' && <Notice tone="error" title="Journey accounting could not be loaded" details={failure.detail} action={<button type="button" className="btn btn-secondary" onClick={() => setAttempt(value => value + 1)}>Retry journeys</button>} />}
    {!loading && report && report.availability !== 'available' && <p>{journeyUnavailableText(report.reason)}</p>}
    {!loading && report && report.availability === 'available' && <>
      <p>
        {report.policy.name || report.policy.id} revision {report.policy.revision} ({report.policy.source === 'pinned' ? "the run's pinned policy" : 'requested'}).{' '}
        <strong>Weighted failure index:</strong> {report.weighted_failure_index !== undefined ? formatJourneyFraction(report.weighted_failure_index) : journeyUnscoredText(report.index_unscored)}
        {report.step_risk_enabled && <> <strong>Step risk index:</strong> {report.step_risk_index !== undefined ? formatJourneyFraction(report.step_risk_index) : journeyUnscoredText(report.step_risk_unscored)}</>}
      </p>
      {report.incomplete && <p role="note">{journeyIncompleteText(report.incomplete_reasons)}</p>}
      <div className="run-archive-coverage-table"><table className="tx-table">
        <thead><tr>
          <th scope="col">Journey</th><th scope="col">Weight</th><th scope="col">Completed</th><th scope="col">Failed</th><th scope="col">Aborted</th>
          <th scope="col">Skipped</th><th scope="col">Censored</th><th scope="col">Failure rate</th><th scope="col">Step failures</th>
        </tr></thead>
        <tbody>{report.journeys.map(journey => <tr key={journey.id}>
          <td><span title={`From ${formatJourneyBoundary(journey.start)} to ${formatJourneyBoundary(journey.success)}${journey.match_mode ? ` (matched by ${journey.match_mode === 'step_path' ? 'step path' : journey.match_mode})` : ''}`}>{journey.name}</span></td>
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
