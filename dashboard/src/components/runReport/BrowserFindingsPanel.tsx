import { useCallback, useEffect, useState } from 'react'

import { getErrorMessage } from '../../utils/errorUtils'
import {
  BROWSER_FINDINGS_POLL_MS, browserCohortLabel, browserFindingReasonText, browserFindingsApi, browserScoreUnscoredText, formatBrowserMetric,
  type BrowserFinding, type RunBrowserFindings,
} from './browserFindingsModel'

const stateOrder: Record<string, number> = { active: 0, unscored: 1, suppressed: 2 }

function findingTitle(finding: BrowserFinding): string {
  if (finding.metric) return `${finding.metric.toUpperCase()} · ${browserCohortLabel(finding.cohort)}`
  return finding.rule_id === 'collection.absent' ? 'Document evidence absent' : 'Document evidence incomplete'
}

// BrowserFindingsPanel shows a run's server-side browser findings and
// qualified score, and lets an editor evaluate the run on demand. With
// onInspectDocument, a finding's worst documents open that document's
// retained resources (finding → document → resources → HAR entry).
export default function BrowserFindingsPanel({ testId, projectId, canEvaluate, browserPolicyPinned, onInspectDocument }: { testId: string; projectId: string; canEvaluate: boolean; browserPolicyPinned?: boolean; onInspectDocument?: (documentId: string) => void }) {
  // An evaluation needs the run's pinned browser policy; unknown is allowed.
  const evaluable = canEvaluate && browserPolicyPinned !== false
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

  // A queued or running evaluation is checked again until it settles.
  const pending = state?.status === 'pending'
  useEffect(() => {
    if (!pending) return
    const timer = window.setTimeout(() => setReload(value => value + 1), BROWSER_FINDINGS_POLL_MS)
    return () => window.clearTimeout(timer)
  }, [pending, state])

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
    {loading && !state && <p role="status">Loading browser findings…</p>}
    {!loading && error && <p role="alert" className="run-report-error">{error}</p>}
    {state && !result && <p>{state.status === 'pending'
      ? 'A browser evaluation is queued or running.'
      : state.status === 'failed'
        ? `The browser evaluation failed: ${state.failure || 'no reason recorded'}.`
        : browserPolicyPinned === false
          ? 'This run has no browser evaluation. It pinned no browser policy, so the run report cannot evaluate it. An evaluation under a named policy revision (API or apexctl) is read from that evaluation and does not appear here.'
          : `This run has no browser evaluation. A run that pins a browser policy and captures page timings and Web Vitals (Runtime Settings › Browser) is queued for evaluation when its archive is finalized${evaluable ? '; evaluate it here if none was queued' : ''}.`}</p>}
    {evaluable && !loading && <button type="button" className="btn btn-secondary" onClick={() => void evaluate()} disabled={queueing || state?.status === 'pending'}>Evaluate with the pinned policy</button>}
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
        {finding.evidence.documents?.length ? <> Worst documents: {onInspectDocument
          ? finding.evidence.documents.map((document, index) => <span key={document.document_id}>{index > 0 ? ', ' : ''}{formatBrowserMetric(finding.metric, document.value)}{' '}
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => onInspectDocument(document.document_id)} aria-label={`Inspect the resources of document …${document.document_id.slice(-8)}`}>(document …{document.document_id.slice(-8)})</button></span>)
          : finding.evidence.documents.map(document => `${formatBrowserMetric(finding.metric, document.value)} (document …${document.document_id.slice(-8)})`).join(', ')}.</> : null}
      </li>)}</ul>}
      <ul className="browser-findings-notes">{result.notes.map(note => <li key={note}>{note}</li>)}</ul>
    </>}
  </section>
}
