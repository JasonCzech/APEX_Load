import { useEffect, useState } from 'react'

import type { RootCauseRouteState } from '../../appRouting'
import type { MetricsHistoryPoint, RunArchiveReport } from '../../types'
import { useApi } from '../../hooks/useApi'
import type { RcaAssessment, RcaCritique, RcaHypothesis } from '../../types/rca'
import { formatNumber } from '../runReport/runReportModel'
import RootCauseInvestigation from './RootCauseInvestigation'
import RcaBrowserBaselineSection from './RcaBrowserBaseline'
import {
  formatRcaDuration,
  formatRcaEffect,
  formatRcaTime,
  formatRcaWindow,
  parseRcaWindow,
  rcaConfidenceLabel,
  rcaHypothesisWindow,
  rcaLimitationLabel,
  rcaOutcomeLabel,
  rcaSeriesLabel,
  rcaSignatureLabel,
  rcaStatusLabel,
  rcaTone,
  rcaValidityLabel,
} from './rcaModel'
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
}

const RELATION_LABELS: Record<string, string> = {
  precedes: 'began before',
  unordered: 'began at about the same time as',
  load_coincident: 'began with a load step, as did',
}

function Badge({ tone, children }: { tone: string; children: React.ReactNode }) {
  return <span className={`rca-badge rca-badge-${tone}`}>{children}</span>
}

function HypothesisCard({ assessment, hypothesis, selected, onInvestigate, onClose }: {
  assessment: RcaAssessment
  hypothesis: RcaHypothesis
  selected: boolean
  onInvestigate: () => void
  onClose: () => void
}) {
  const anomaly = (id: string) => assessment.anomalies.find(a => a.id === id)
  const label = (id: string) => { const a = anomaly(id); return a ? rcaSeriesLabel(a.series) : id }
  const title = rcaSignatureLabel(hypothesis.signature)
  return (
    <li className={`rca-hypothesis${selected ? ' rca-hypothesis-selected' : ''}`} aria-label={`Hypothesis ${hypothesis.rank}: ${title}`}>
      <div className="rca-hypothesis-heading">
        <h4><span className="rca-rank">#{hypothesis.rank}</span> {title}</h4>
        <Badge tone={rcaTone(hypothesis.confidence)}>{rcaConfidenceLabel(hypothesis.confidence)}</Badge>
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

// RootCausePanel presents a run's generated root-cause assessment in
// Diagnostics: acceptance and validity, the ranked hypotheses with what
// would disprove each, the anomalies, coverage and limitations, and the
// drill-down for the hypothesis the route names. Analyst findings stay in
// the report annotations, apart from this.
export default function RootCausePanel({ testId, model, report, history, historyLoading, canAnalyze, baselineTestId, baselineName, route, onRouteChange }: Props) {
  const [enrich, setEnrich] = useState(false)
  const { response, loading, error } = model
  // Only a project that presents v2 shows the panel. Until a response says
  // so, it shows only for a link that names an analysis.
  if (response && !response.presented) {
    // An analyst learns the capability exists; others see nothing.
    return canAnalyze ? <p className="run-report-muted" role="note">Root-cause analysis is not enabled for this project. A system administrator enables it per project.</p> : null
  }
  if (!response && !(route.analysis || route.hypothesis)) return null
  const assessment = response?.assessment
  const pinned = response?.analysis_id ? { analysis: response.analysis_id, rev: response.revision ? String(response.revision) : undefined } : {}
  const selected = assessment?.hypotheses.find(h => h.id === route.hypothesis)
  const window = parseRcaWindow(route.window)
  const pending = response?.state === 'queued' || response?.state === 'running'

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

  return (
    <section className="run-report-panel rca-panel" aria-label="Root cause">
      <div className="run-report-section-heading rca-panel-heading">
        <div>
          <h2>Root cause</h2>
          <p className="run-report-muted">Generated from the run's archived evidence{response?.revision ? ` · analysis revision ${response.revision}` : ''}{response?.completed_at ? ` · ${formatRcaTime(response.completed_at)}` : ''}. Analyst findings are recorded separately in the run annotations.</p>
        </div>
        <div className="rca-panel-actions">
          {assessment && <Badge tone={assessment.outcome === 'supported' ? 'success' : 'warning'}>{rcaOutcomeLabel(assessment.outcome)}</Badge>}
          {route.analysis && <button type="button" className="btn btn-sm btn-ghost" onClick={() => onRouteChange({ analysis: undefined, rev: undefined, hypothesis: undefined, window: undefined })}>Show latest analysis</button>}
          {canAnalyze && <>
            <label className="rca-enrich"><input type="checkbox" checked={enrich} onChange={e => setEnrich(e.target.checked)} /> Re-read provider evidence</label>
            {baselineTestId && <span className="rca-enrich">Against baseline {baselineName || baselineTestId}</span>}
            <button type="button" className="btn btn-sm btn-secondary" disabled={model.requesting || pending} onClick={() => void reanalyze()}>{model.requesting ? 'Starting…' : response?.state === 'none' ? 'Analyze' : 'Re-analyze'}</button>
          </>}
        </div>
      </div>
      {model.requestError && <p className="run-report-error" role="alert">{model.requestError}</p>}
      {loading && !response && <p role="status">Loading the root-cause analysis…</p>}
      {error && <p className="run-report-error" role="alert">{error} <button type="button" className="btn btn-sm btn-ghost" onClick={model.reload}>Retry</button></p>}
      {response?.state === 'none' && <p>No root-cause analysis has completed for this run.{canAnalyze ? ' Analyze it to rank the likely causes.' : ''}</p>}
      {pending && <p role="status">Analysis revision {response?.revision} is {response?.state}…</p>}
      {response?.state === 'failed' && <p className="run-report-error" role="alert">The analysis failed: {response.error || 'no reason was recorded'}.</p>}
      {response?.state === 'cancelled' && <p>This analysis was cancelled before it completed.</p>}
      {route.rev && response?.revision && String(response.revision) !== route.rev && <p className="run-report-muted" role="note">The link named revision {route.rev}; showing revision {response.revision}.</p>}
      {response?.archive_changed && <p className="run-report-muted" role="note">This analysis judged an earlier revision of the run's archive; evidence archived since is not in it.{canAnalyze ? ' Re-analyze to include it.' : ''}</p>}
      {assessment && <>
        <dl className="rca-facts">
          <div><dt>Acceptance</dt><dd><Badge tone={rcaTone(assessment.acceptance.status)}>{rcaStatusLabel(assessment.acceptance.status)}</Badge></dd></div>
          <div><dt>Run validity</dt><dd><Badge tone={rcaTone(assessment.validity.verdict)}>{rcaValidityLabel(assessment.validity.verdict)}</Badge></dd></div>
          <div><dt>Onset detection</dt><dd>{assessment.detection ? `${rcaStatusLabel(assessment.detection.status)} · ${formatNumber(assessment.detection.series_evaluated, 0)} series judged` : 'Not run'}</dd></div>
          <div><dt>Anomalies</dt><dd>{formatNumber(assessment.anomalies.length, 0)}</dd></div>
        </dl>
        {assessment.validity.reasons.length > 0 && <ul className="rca-validity-reasons" aria-label="Validity reasons">{assessment.validity.reasons.map((reason, i) => <li key={i}>{reason}</li>)}</ul>}
        {assessment.acceptance.details.length > 0 && <details><summary>Acceptance details ({assessment.acceptance.details.length})</summary><ul>{assessment.acceptance.details.map((detail, i) => <li key={i}>{detail}</li>)}</ul></details>}

        <h3>Hypotheses</h3>
        {assessment.hypotheses.length === 0
          ? <p>{assessment.outcome === 'insufficient_evidence' ? 'Too little evidence was measured to judge any cause. See coverage below for what was missing.' : 'No cause the evidence supports was found.'}</p>
          : <ol className="rca-hypotheses">{assessment.hypotheses.map(h => <HypothesisCard key={h.id} assessment={assessment} hypothesis={h} selected={h.id === selected?.id}
              onInvestigate={() => investigate(h)} onClose={() => onRouteChange({ ...pinned, hypothesis: undefined, window: undefined })} />)}</ol>}
        {route.hypothesis && !selected && <p className="run-report-muted" role="note">The linked hypothesis is not in this analysis.</p>}
        {selected && <RootCauseInvestigation key={selected.id} testId={testId} assessment={assessment} hypothesis={selected} window={window} report={report} history={history} historyLoading={historyLoading}
          onWindowChange={w => onRouteChange({ ...pinned, hypothesis: selected.id, window: w ? formatRcaWindow(w) : undefined })} />}

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
        <details>
          <summary>Evidence coverage ({assessment.coverage.filter(c => c.status !== 'complete').length} gaps)</summary>
          <ul className="rca-coverage">{assessment.coverage.map((c, i) => <li key={i}><Badge tone={rcaTone(c.status)}>{rcaStatusLabel(c.status)}</Badge> {rcaStatusLabel(c.evidence)}{c.detail ? `: ${c.detail}` : ''}</li>)}</ul>
        </details>
        {(assessment.limitations.length > 0 || assessment.validity.limitations.length > 0) && <details>
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
