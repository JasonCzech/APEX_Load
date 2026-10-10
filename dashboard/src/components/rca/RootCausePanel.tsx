import { useEffect, useMemo, useState } from 'react'

import type { RootCauseRouteState } from '../../appRouting'
import type { MetricsHistoryPoint, RunArchiveReport } from '../../types'
import { useApi } from '../../hooks/useApi'
import type { RcaAssessment, RcaCoverage, RcaCritique, RcaHypothesis, ReportRootCauseSummary } from '../../types/rca'
import { formatNumber } from '../runReport/runReportModel'
import RootCauseInvestigation from './RootCauseInvestigation'
import RcaBrowserBaselineSection from './RcaBrowserBaseline'
import RcaTimeline from './RcaTimeline'
import {
  formatRcaDuration,
  formatRcaEffect,
  formatRcaTime,
  formatRcaWindow,
  parseRcaWindow,
  rcaAnomalyWindow,
  rcaConfidenceLabel,
  rcaHypothesisWindow,
  rcaLargestChanges,
  rcaLimitationLabel,
  rcaOriginLabel,
  rcaOutcomeLabel,
  rcaSeriesLabel,
  rcaSignatureLabel,
  rcaStatusLabel,
  rcaTone,
  rcaValidityConcerns,
  rcaValidityLabel,
  sameRcaWindow,
  type RcaWindow,
} from './rcaModel'
import { useRcaPhases } from './useRcaPhases'
import type { RootCauseAnalysisModel } from './useRootCauseAnalysis'
import './RootCause.css'

interface Props {
  testId: string
  model: RootCauseAnalysisModel
  report: RunArchiveReport | null
  history: MetricsHistoryPoint[]
  historyLoading?: boolean
  canAnalyze: boolean
  // baselineTestId and baselineName name the report's comparison baseline,
  // which a re-analysis is judged against.
  baselineTestId?: string
  baselineName?: string
  route: RootCauseRouteState
  onRouteChange: (next: RootCauseRouteState) => void
  // liveSummary is the run's root-cause summary, kept current while its
  // newest analysis is queued or running; it says when an automatic
  // analysis is on its way before any has completed.
  liveSummary?: ReportRootCauseSummary
  // onCopyLink copies the report's link, which names the analysis,
  // hypothesis and window shown.
  onCopyLink?: () => void
  // settingsHref opens the project's analysis settings, for those who may
  // change them.
  settingsHref?: string
  // monitorEvidenceHref opens the report's archived monitor evidence;
  // monitorSourcesHref the project's monitor sources, for those who manage
  // them.
  monitorEvidenceHref?: string
  monitorSourcesHref?: string
  // hypothesisActions hand a hypothesis to the report's other tools; only
  // editors get them.
  hypothesisActions?: RcaHypothesisActions
}

// RcaHypothesisActions hand a hypothesis to the report's other tools. Each
// resolves to a short message for the person who asked, or rejects with an
// error to show.
export interface RcaHypothesisActions {
  recordFinding: (hypothesis: RcaHypothesis, assessment: RcaAssessment) => Promise<string>
  annotate: (hypothesis: RcaHypothesis, assessment: RcaAssessment) => Promise<string>
  addNote: (hypothesis: RcaHypothesis, assessment: RcaAssessment) => void
}

const RELATION_LABELS: Record<string, string> = {
  precedes: 'began before',
  unordered: 'began at about the same time as',
  load_coincident: 'began with a load step, as did',
}

function Badge({ tone, children }: { tone: string; children: React.ReactNode }) {
  return <span className={`rca-badge rca-badge-${tone}`}>{children}</span>
}

function HypothesisCard({ assessment, hypothesis, selected, onInvestigate, onClose, actions }: {
  assessment: RcaAssessment
  hypothesis: RcaHypothesis
  selected: boolean
  onInvestigate: () => void
  onClose: () => void
  actions?: RcaHypothesisActions
}) {
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<{ text: string; error: boolean } | null>(null)
  const act = async (run: () => Promise<string>) => {
    setBusy(true)
    setMessage(null)
    try {
      setMessage({ text: await run(), error: false })
    } catch (err) {
      setMessage({ text: err instanceof Error ? err.message : 'The action failed', error: true })
    } finally {
      setBusy(false)
    }
  }
  const anomaly = (id: string) => assessment.anomalies.find(a => a.id === id)
  const label = (id: string) => { const a = anomaly(id); return a ? rcaSeriesLabel(a.series) : id }
  const title = rcaSignatureLabel(hypothesis.signature)
  return (
    <li className={`rca-hypothesis${selected ? ' rca-hypothesis-selected' : ''}`} aria-label={`Hypothesis ${hypothesis.rank}: ${title}`}>
      <div className="rca-hypothesis-heading">
        <h4><span className="rca-rank">#{hypothesis.rank}</span> {title}</h4>
        <span title={hypothesis.confidence_reasons[0] || undefined}><Badge tone={rcaTone(hypothesis.confidence)}>{rcaConfidenceLabel(hypothesis.confidence)}</Badge></span>
        {selected
          ? <button type="button" className="btn btn-sm btn-ghost" onClick={onClose}>Close investigation</button>
          : <button type="button" className="btn btn-sm btn-secondary" onClick={onInvestigate}>Investigate</button>}
      </div>
      <p><strong>Explains:</strong> {hypothesis.symptoms.map(label).join('; ') || 'No symptom recorded'}</p>
      {hypothesis.chain.length > 0 && <ul className="rca-chain" aria-label="Evidence chain">{hypothesis.chain.map(step => {
        const cause = anomaly(step.from)
        return <li key={`${step.from}>${step.to}`}>{label(step.from)}{cause ? ` (${formatRcaEffect(cause)})` : ''} {RELATION_LABELS[step.relation] || step.relation.replace(/_/g, ' ')} {label(step.to)}</li>
      })}</ul>}
      <details>
        <summary>Why {rcaConfidenceLabel(hypothesis.confidence).toLowerCase()}</summary>
        <ul>{hypothesis.confidence_reasons.map((reason, i) => <li key={i}>{reason}</li>)}</ul>
      </details>
      <p><strong>Contradicting evidence:</strong> {hypothesis.contradicting.length ? hypothesis.contradicting.map((ref, i) => <code key={i} className="rca-locator">{ref.locator}</code>) : 'None found.'}</p>
      {hypothesis.competing.length > 0 && <p><strong>Also supported:</strong> {hypothesis.competing.map(rcaSignatureLabel).join(', ')}</p>}
      <p className="rca-falsification"><strong>Would disprove it:</strong> {hypothesis.falsification || 'No falsification check is defined for this signature.'}</p>
      {hypothesis.limitations.length > 0 && <ul className="rca-limitations" aria-label="Hypothesis limitations">{hypothesis.limitations.map((item, i) => <li key={i}>{item}</li>)}</ul>}
      {actions && <div className="rca-hypothesis-actions" role="group" aria-label={`Use hypothesis ${hypothesis.rank}`}>
        <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => void act(() => actions.recordFinding(hypothesis, assessment))}>Record as finding</button>
        <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => void act(() => actions.annotate(hypothesis, assessment))}>Annotate timeline</button>
        <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => actions.addNote(hypothesis, assessment)}>Add test note</button>
        {message && <span className={message.error ? 'run-report-error' : 'run-report-muted'} role={message.error ? 'alert' : 'status'}>{message.text}</span>}
      </div>}
    </li>
  )
}

// RcaCritiques lists the reviews stored for an analysis. Each passed the
// narration check against the analysis's bundle; none changes it.
function RcaCritiques({ testId, analysisId }: { testId: string; analysisId: string }) {
  const api = useApi()
  const [critiques, setCritiques] = useState<RcaCritique[]>([])
  useEffect(() => {
    const controller = new AbortController()
    setCritiques([])
    api.getRootCauseCritiques(testId, analysisId, { signal: controller.signal })
      .then(r => { if (!controller.signal.aborted) setCritiques(Array.isArray(r?.critiques) ? r.critiques : []) })
      .catch(() => { if (!controller.signal.aborted) setCritiques([]) })
    return () => controller.abort()
  }, [api, analysisId, testId])
  if (critiques.length === 0) return null
  return (
    <details className="rca-critiques">
      <summary>Critiques ({critiques.length})</summary>
      <p className="run-report-muted">Reviews of this analysis by agents or analysts. Each was checked against the analysis's evidence; none changes it.</p>
      <ul>{critiques.map(c => (
        <li key={c.id}>
          <strong>{c.author_kind === 'agent' ? `Agent${c.model ? ` (${c.model})` : ''}` : 'Analyst'}</strong>{c.created_by ? ` · ${c.created_by}` : ''} · {formatRcaTime(c.created_at)}
          <p>{c.narrative}</p>
        </li>
      ))}</ul>
    </details>
  )
}

// RcaOverview is the run's timeline when no hypothesis is open: its
// largest changes, its suspect and invalid spans and its load steps, with a
// focus control for each change. It reads the same whether or not a cause
// is supported.
function RcaOverview({ testId, assessment, report, history, historyLoading, window, onWindowChange }: {
  testId: string
  assessment: RcaAssessment
  report: RunArchiveReport | null
  history: MetricsHistoryPoint[]
  historyLoading?: boolean
  window: RcaWindow | null
  onWindowChange: (window: RcaWindow | null) => void
}) {
  const changes = useMemo(() => rcaLargestChanges(assessment, 5), [assessment])
  const involved = useMemo(() => changes.map(anomaly => ({ anomaly, role: 'anomaly' as const })), [changes])
  const timeline = useRcaPhases(testId, report, assessment.detection)
  const concerns = useMemo(() => rcaValidityConcerns(assessment.validity.windows), [assessment.validity.windows])
  return (
    <section className="rca-overview" aria-label="Largest changes" data-report-focus="rca-changes">
      <div className="rca-investigation-heading">
        <h3>Largest changes</h3>
        {changes.length > 0 && <div className="rca-window-controls" role="group" aria-label="Time window">
          <span className="run-report-muted">Window: {window ? `${formatRcaTime(window.start)} to ${formatRcaTime(window.end)}` : 'Whole run'}</span>
          {changes.map(anomaly => {
            const w = rcaAnomalyWindow(anomaly)
            return <button key={anomaly.id} type="button" className="btn btn-sm btn-ghost" aria-pressed={sameRcaWindow(window, w)} onClick={() => onWindowChange(w)}>Focus {rcaSeriesLabel(anomaly.series)}</button>
          })}
          <button type="button" className="btn btn-sm btn-ghost" aria-pressed={!window} onClick={() => onWindowChange(null)}>Whole run</button>
        </div>}
      </div>
      {changes.length === 0
        ? <p className="run-report-muted">No series changed beyond its own spread. Coverage below lists what could not be judged.</p>
        : <p className="run-report-muted">The {changes.length === 1 ? 'change' : `${changes.length} changes`} that moved furthest from their own spread, whether or not a hypothesis explains them.</p>}
      <RcaTimeline history={history} anomalies={involved} phases={timeline.phases} phasesInferred={timeline.inferred} validity={concerns} window={window} loading={historyLoading} />
    </section>
  )
}

// RcaCoverageList lists evidence coverage, naming the monitor sources that
// left a gap with links to their archived evidence and, for those who manage
// them, the project's monitor sources.
function RcaCoverageList({ coverage, monitorEvidenceHref, monitorSourcesHref }: { coverage: RcaCoverage[]; monitorEvidenceHref?: string; monitorSourcesHref?: string }) {
  return (
    <ul className="rca-coverage">{coverage.map((c, i) => {
      const gaps = (c.sources || []).filter(source => source.reason)
      return (
        <li key={i}><Badge tone={rcaTone(c.status)}>{rcaStatusLabel(c.status)}</Badge> {rcaStatusLabel(c.evidence)}{c.detail ? `: ${c.detail}` : ''}
          {gaps.length > 0 && <ul className="rca-coverage-sources" aria-label="Monitor sources with gaps">{gaps.map(source => (
            <li key={source.id}>{source.pack || 'Monitor'} source <code>{source.id}</code>{source.required ? ' (required)' : ''}: {rcaStatusLabel(source.reason || '')}
              {monitorEvidenceHref && <> · <a href={monitorEvidenceHref}>Archived monitor evidence</a></>}
              {monitorSourcesHref && <> · <a href={monitorSourcesHref}>Monitor sources</a></>}
            </li>
          ))}</ul>}
        </li>
      )
    })}</ul>
  )
}

// rcaExplanation is the sentence under the outcome that says how to read it.
function rcaExplanation(assessment: RcaAssessment): string {
  const changes = assessment.anomalies.length
  switch (assessment.outcome) {
    case 'supported':
      return `${assessment.hypotheses.length === 1 ? 'One hypothesis is' : `${assessment.hypotheses.length} hypotheses are`} ranked; the first explains the most symptoms. Each says what would disprove it.`
    case 'no_supported_hypothesis':
      return changes > 0
        ? `${changes === 1 ? 'One series' : `${changes.toLocaleString()} series`} changed, but no signature explains the symptoms. The largest changes are below; coverage lists what could not be judged.`
        : 'Nothing changed beyond its own spread, and no signature explains a symptom.'
    default:
      return 'Too few series were measured to judge a cause. Coverage below lists what is missing.'
  }
}

// RootCausePanel presents a run's generated root-cause assessment in
// Diagnostics: the analysis and the run's validity, the ranked hypotheses
// with what would disprove each, the run's largest changes on its timeline,
// coverage and limitations, and the drill-down for the hypothesis the route
// names. Analyst findings stay in the report annotations, apart from this.
export default function RootCausePanel({ testId, model, report, history, historyLoading, canAnalyze, baselineTestId, baselineName, route, onRouteChange, liveSummary, onCopyLink, settingsHref, monitorEvidenceHref, monitorSourcesHref, hypothesisActions }: Props) {
  const [enrich, setEnrich] = useState(false)
  const { response, loading, error } = model
  // Only a project that presents v2 shows the panel. Until a response says
  // so, it shows only for a link that names an analysis.
  if (response && !response.presented) {
    // An analyst learns the capability exists and who turns it on; others
    // see nothing.
    if (!canAnalyze) return null
    return <p className="run-report-muted" role="note">Root-cause analysis is not enabled for this project. {settingsHref
      ? <>Turn it on in <a href={settingsHref}>Settings › Project › Analysis</a>.</>
      : 'A project administrator turns it on in Settings › Project › Analysis.'}</p>
  }
  if (!response && !(route.analysis || route.hypothesis)) return null
  const assessment = response?.assessment
  const pinned = response?.analysis_id ? { analysis: response.analysis_id, rev: response.revision ? String(response.revision) : undefined } : {}
  const selected = assessment?.hypotheses.find(h => h.id === route.hypothesis)
  const window = parseRcaWindow(route.window)
  const pending = response?.state === 'queued' || response?.state === 'running'
  // The newest analysis may be queued or running (an automatic one, say)
  // while the panel shows the newest completed one, or none.
  const latest = liveSummary?.latest
  const newerPending = Boolean(latest && (latest.state === 'queued' || latest.state === 'running') && latest.analysis_id !== response?.analysis_id)
  // A project can present saved analyses without computing new ones; the
  // controls then say so instead of offering a request that is refused.
  const analysisEnabled = response?.analysis_enabled !== false

  const investigate = (h: RcaHypothesis) => {
    const span = assessment ? rcaHypothesisWindow(h, assessment.anomalies) : null
    onRouteChange({ ...pinned, hypothesis: h.id, window: span ? formatRcaWindow(span) : undefined })
  }
  const reanalyze = async () => {
    const next = await model.request({ enrich, ...(baselineTestId ? { baseline_test_id: baselineTestId } : {}) })
    if (!next?.analysis_id) return
    if (next.analysis_id === route.analysis) model.reload()
    else onRouteChange({ analysis: next.analysis_id, rev: next.revision ? String(next.revision) : undefined, hypothesis: undefined, window: undefined })
  }
  const detection = assessment?.detection
  const judged = detection ? detection.series_evaluated + detection.series_excluded : 0

  return (
    <section className="run-report-panel rca-panel" aria-label="Root cause" data-report-focus="root-cause">
      <div className="run-report-section-heading rca-panel-heading">
        <div>
          <h2>Root cause</h2>
          <p className="run-report-muted">Generated from the run's archived evidence{response?.revision ? ` · ${rcaOriginLabel(response.origin).toLowerCase()} analysis, revision ${response.revision}` : ''}{response?.completed_at ? ` · ${formatRcaTime(response.completed_at)}` : ''}.</p>
        </div>
        <div className="rca-panel-actions">
          {assessment && <Badge tone={assessment.outcome === 'supported' ? 'success' : 'warning'}>{rcaOutcomeLabel(assessment.outcome)}</Badge>}
          {route.analysis && <button type="button" className="btn btn-sm btn-ghost" onClick={() => onRouteChange({ analysis: undefined, rev: undefined, hypothesis: undefined, window: undefined })}>Show latest analysis</button>}
          {onCopyLink && response?.analysis_id && <button type="button" className="btn btn-sm btn-ghost" onClick={onCopyLink}>Copy link</button>}
          {canAnalyze && !analysisEnabled && <span className="run-report-muted" role="note">New analyses are not enabled for this project; saved ones stay readable.</span>}
          {canAnalyze && analysisEnabled && <>
            <label className="rca-enrich" title="Freeze the archive, monitor sources and datasets as they are now, then analyze that copy. Use it after late monitoring evidence arrived."><input type="checkbox" checked={enrich} onChange={e => setEnrich(e.target.checked)} /> Capture evidence afresh</label>
            {baselineTestId && <span className="rca-enrich">Against baseline {baselineName || baselineTestId}</span>}
            <button type="button" className="btn btn-sm btn-secondary" disabled={model.requesting || pending || newerPending} onClick={() => void reanalyze()}>{model.requesting ? 'Starting…' : response?.state === 'none' ? 'Analyze' : 'Re-analyze'}</button>
          </>}
        </div>
      </div>
      {model.requestError && <p className="run-report-error" role="alert">{model.requestError}</p>}
      {loading && !response && <p role="status">Loading the root-cause analysis…</p>}
      {error && <p className="run-report-error" role="alert">{error} <button type="button" className="btn btn-sm btn-ghost" onClick={model.reload}>Retry</button></p>}
      {newerPending && latest && <p role="status">{rcaOriginLabel(latest.origin)} analysis revision {latest.revision} is {latest.state}; it replaces this view when it completes.</p>}
      {response?.state === 'none' && !newerPending && (latest?.state === 'failed'
        ? <p className="run-report-error" role="alert">The run's {rcaOriginLabel(latest.origin).toLowerCase()} analysis failed.{canAnalyze && analysisEnabled ? ' Analyze to try again.' : ''}</p>
        : <p>No root-cause analysis has completed for this run.{canAnalyze && analysisEnabled ? ' Analyze it to rank the likely causes.' : ''}</p>)}
      {pending && <p role="status">Analysis revision {response?.revision} is {response?.state}…</p>}
      {response?.state === 'failed' && <p className="run-report-error" role="alert">The analysis failed: {response.error || 'no reason was recorded'}.</p>}
      {response?.state === 'cancelled' && <p>This analysis was cancelled before it completed.</p>}
      {route.rev && response?.revision && String(response.revision) !== route.rev && <p className="run-report-muted" role="note">The link named revision {route.rev}; showing revision {response.revision}.</p>}
      {response?.archive_changed && <p className="run-report-muted" role="note">This analysis judged an earlier revision of the run's archive; evidence archived since is not in it.{canAnalyze && analysisEnabled ? ' Re-analyze to include it.' : ''}</p>}
      {assessment && <>
        <dl className="rca-facts">
          <div><dt>Analysis</dt><dd>{rcaOriginLabel(response?.origin)} · revision {response?.revision ?? assessment.revision}</dd></div>
          <div><dt>Run validity</dt><dd><Badge tone={rcaTone(assessment.validity.verdict)}>{rcaValidityLabel(assessment.validity.verdict)}</Badge></dd></div>
          <div><dt>Series judged</dt><dd>{detection ? `${formatNumber(detection.series_evaluated, 0)} of ${formatNumber(judged, 0)} · ${rcaStatusLabel(detection.status)}` : 'Not run'}</dd></div>
          <div><dt>Anomalies</dt><dd>{formatNumber(assessment.anomalies.length, 0)}</dd></div>
        </dl>
        <p className="rca-explanation">{rcaExplanation(assessment)}</p>
        {assessment.validity.reasons.length > 0 && <ul className="rca-validity-reasons" aria-label="Validity reasons">{assessment.validity.reasons.map((reason, i) => <li key={i}>{reason}</li>)}</ul>}
        {assessment.acceptance.details.length > 0 && <details><summary>Acceptance: {rcaStatusLabel(assessment.acceptance.status)} ({assessment.acceptance.details.length} details)</summary><ul>{assessment.acceptance.details.map((detail, i) => <li key={i}>{detail}</li>)}</ul></details>}

        <h3>Hypotheses</h3>
        {assessment.hypotheses.length === 0
          ? <p>{assessment.outcome === 'insufficient_evidence' ? 'Too little evidence was measured to judge any cause. See coverage below for what was missing.' : 'No cause the evidence supports was found.'}</p>
          : <ol className="rca-hypotheses">{assessment.hypotheses.map(h => <HypothesisCard key={h.id} assessment={assessment} hypothesis={h} selected={h.id === selected?.id} actions={hypothesisActions}
              onInvestigate={() => investigate(h)} onClose={() => onRouteChange({ ...pinned, hypothesis: undefined, window: undefined })} />)}</ol>}
        {route.hypothesis && !selected && <p className="run-report-muted" role="note">The linked hypothesis is not in this analysis.</p>}
        {selected
          ? <RootCauseInvestigation key={selected.id} testId={testId} assessment={assessment} hypothesis={selected} window={window} report={report} history={history} historyLoading={historyLoading}
              onWindowChange={w => onRouteChange({ ...pinned, hypothesis: selected.id, window: w ? formatRcaWindow(w) : undefined })} />
          : <RcaOverview testId={testId} assessment={assessment} report={report} history={history} historyLoading={historyLoading} window={window}
              onWindowChange={w => onRouteChange({ ...pinned, hypothesis: undefined, window: w ? formatRcaWindow(w) : undefined })} />}

        <details>
          <summary>Anomalies ({assessment.anomalies.length})</summary>
          {assessment.anomalies.length === 0 ? <p className="run-report-muted">No anomaly was detected.</p> : (
            <table className="rca-table">
              <thead><tr><th scope="col">Series</th><th scope="col">Change</th><th scope="col">Onset</th><th scope="col">Scope</th><th scope="col">Recovered</th></tr></thead>
              <tbody>{assessment.anomalies.map(a => <tr key={a.id}><th scope="row">{rcaSeriesLabel(a.series)}</th><td>{formatRcaEffect(a)}</td>
                <td>{formatRcaTime(a.onset)}{a.onset_uncertainty_ns > 0 ? ` ± ${formatRcaDuration(a.onset_uncertainty_ns)}` : ''}</td>
                <td>{a.scope === 'load' ? `Against baseline${a.level ? ` (${a.level})` : ''}` : 'Over time'}</td><td>{a.recovered ? 'Yes' : 'No'}</td></tr>)}</tbody>
            </table>
          )}
        </details>
        <RcaBrowserBaselineSection baseline={assessment.browser_baseline} />
        <details data-report-focus="rca-coverage">
          <summary>Evidence coverage ({assessment.coverage.filter(c => c.status !== 'complete').length} gaps)</summary>
          <RcaCoverageList coverage={assessment.coverage} monitorEvidenceHref={monitorEvidenceHref} monitorSourcesHref={monitorSourcesHref} />
        </details>
        {(assessment.limitations.length > 0 || assessment.validity.limitations.length > 0) && <details data-report-focus="rca-limitations">
          <summary>Limitations ({assessment.limitations.length + assessment.validity.limitations.length})</summary>
          <ul className="rca-limitations">
            {assessment.limitations.map((l, i) => <li key={`${l.code}-${i}`}><strong>{rcaLimitationLabel(l.code)}</strong>{l.detail ? `: ${l.detail}` : ''}</li>)}
            {assessment.validity.limitations.map((item, i) => <li key={`validity-${i}`}>{item}</li>)}
          </ul>
        </details>}
        {(assessment.capacity?.length ?? 0) > 0 && <details>
          <summary>Capacity</summary>
          <ul>{assessment.capacity!.map((c, i) => <li key={i}>{rcaSeriesLabel(c.series)}: {c.knee !== undefined ? `latency bends at about ${formatNumber(c.knee, 1)} ${c.load_unit}` : c.reason || 'no knee found'}</li>)}</ul>
        </details>}
        {response?.analysis_id && <RcaCritiques testId={testId} analysisId={response.analysis_id} />}
        <p className="run-report-muted rca-provenance">Engine {assessment.inputs.engine} · evidence revision {assessment.inputs.evidence_revision}{assessment.inputs.baseline ? ' · with baseline' : ''}</p>
      </>}
    </section>
  )
}
