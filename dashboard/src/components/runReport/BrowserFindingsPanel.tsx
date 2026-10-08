import { useCallback, useEffect, useState } from 'react'

import { getErrorMessage } from '../../utils/errorUtils'
import {
  browserCohortLabel, browserFindingReasonText, browserFindingsApi, browserScoreUnscoredText, formatBrowserMetric,
  type BrowserFinding, type RunBrowserFindings,
} from './browserFindingsModel'

const stateOrder: Record<string, number> = { active: 0, unscored: 1, suppressed: 2 }

function findingTitle(finding: BrowserFinding): string {
  if (finding.metric) return `${finding.metric.toUpperCase()} · ${browserCohortLabel(finding.cohort)}`
  return finding.rule_id === 'collection.absent' ? 'Document evidence absent' : 'Document evidence incomplete'
}

// BrowserFindingsPanel shows a run's server-side browser findings and
// qualified score, and lets an editor evaluate the run on demand.
export default function BrowserFindingsPanel({ testId, projectId, canEvaluate }: { testId: string; projectId: string; canEvaluate: boolean }) {
  const [state, setState] = useState<RunBrowserFindings | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [queueing, setQueueing] = useState(false)
  const [reload, setReload] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    browserFindingsApi.get(testId, controller.signal)
      .then(next => { if (!controller.signal.aborted) setState(next) })
      .catch(err => { if (!controller.signal.aborted) { setState(null); setError(getErrorMessage(err, 'Unable to load browser findings')) } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [testId, reload])

  const evaluate = useCallback(async () => {
    setQueueing(true)
    setError('')
    try {
      await browserFindingsApi.evaluate(projectId, testId)
      setReload(value => value + 1)
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to queue a browser evaluation'))
    } finally {
      setQueueing(false)
    }
  }, [projectId, testId])

  const result = state?.result
  const findings = [...(result?.findings || [])].sort((a, b) => (stateOrder[a.state] ?? 3) - (stateOrder[b.state] ?? 3) || a.summary.localeCompare(b.summary))
  return <section className="run-report-panel browser-findings" aria-label="Browser findings">
    <h2>Browser findings</h2>
    {loading && <p role="status">Loading browser findings...</p>}
    {!loading && error && <p role="alert" className="run-report-error">{error}</p>}
    {!loading && state && !result && <p>{state.status === 'pending' ? 'A browser evaluation is queued or running.' : 'This run has no browser evaluation. Runs that pin a browser policy are evaluated when their archive is finalized.'}</p>}
    {canEvaluate && !loading && <button type="button" className="btn btn-secondary" onClick={() => void evaluate()} disabled={queueing || state?.status === 'pending'}>Evaluate with the pinned policy</button>}
    {result && <>
      <p>
        <strong>{result.score.score !== undefined ? `Score ${result.score.score.toFixed(1)}/100 (${result.score.severity.replace(/_/g, ' ')})` : browserScoreUnscoredText(result.score.unscored_reason)}</strong>{' '}
        {result.score.missing?.length ? `Missing: ${result.score.missing.map(metric => metric.toUpperCase()).join(', ')}. ` : ''}
        {result.score.coverage.observed}/{result.score.coverage.required} required metrics observed · {result.score.version} · {result.policy.name || result.policy.id} revision {result.policy.revision} ({result.policy.source === 'pinned' ? "the run's pinned policy" : 'requested'}).
      </p>
      <div className="run-archive-coverage-table"><table className="tx-table" aria-label="Score components">
        <thead><tr><th scope="col">Metric</th><th scope="col">Weight</th><th scope="col">p75</th><th scope="col">Good / poor</th><th scope="col">Component</th><th scope="col">Contribution</th><th scope="col">Samples</th></tr></thead>
        <tbody>{result.score.components.map(component => <tr key={component.metric}>
          <td>{component.metric.toUpperCase()}{component.required ? '' : ' (not weighted)'}</td>
          <td>{component.weight}</td>
          <td>{formatBrowserMetric(component.metric, component.observed)}</td>
          <td>{formatBrowserMetric(component.metric, component.good)} / {formatBrowserMetric(component.metric, component.poor)}</td>
          <td>{component.score !== undefined ? component.score.toFixed(1) : 'Unscored'}</td>
          <td>{component.contribution !== undefined ? component.contribution.toFixed(1) : '—'}</td>
          <td>{component.samples}</td>
        </tr>)}</tbody>
      </table></div>
      <p>{result.counts.active || 0} active · {result.counts.unscored || 0} unscored · {result.counts.suppressed || 0} suppressed</p>
      {findings.length > 0 && <ul className="browser-findings-list">{findings.map(finding => <li key={finding.id} data-state={finding.state} data-category={finding.category}>
        <strong>{findingTitle(finding)}</strong> — {finding.state === 'active' ? (finding.severity || 'active').replace(/_/g, ' ') : `${finding.state}: ${browserFindingReasonText(finding.reason)}`}
        {finding.category === 'collection_health' ? ' (collection health)' : ''}. {finding.summary}
        {finding.evidence.documents?.length ? <> Worst documents: {finding.evidence.documents.map(document => `${formatBrowserMetric(finding.metric, document.value)} (document …${document.document_id.slice(-8)})`).join(', ')}.</> : null}
      </li>)}</ul>}
      <ul className="browser-findings-notes">{result.notes.map(note => <li key={note}>{note}</li>)}</ul>
    </>}
  </section>
}
