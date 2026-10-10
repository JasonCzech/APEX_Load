import { useEffect, useMemo, useState } from 'react'

import { useApi } from '../../hooks/useApi'
import type { MetricsHistoryPoint, RunArchiveReport } from '../../types'
import type { ExemplarPage, RcaAssessment, RcaHypothesis, RcaWindowLink } from '../../types/rca'
import { getErrorMessage } from '../../utils/errorUtils'
import { formatMs, formatNumber, formatPct } from '../runReport/runReportModel'
import RcaTimeline from './RcaTimeline'
import { useRcaPhases } from './useRcaPhases'
import {
  formatRcaDuration,
  formatRcaEffect,
  formatRcaClock,
  formatRcaTime,
  rcaAnomalyWindow,
  rcaErrorPatternAnchor,
  rcaErrorPatterns,
  rcaRevealElement,
  rcaGraphNodeLabel,
  rcaHypothesisAnomalies,
  rcaHypothesisWindow,
  rcaPopulationStats,
  rcaProviderLabel,
  rcaSeriesLabel,
  rcaSeriesPopulation,
  rcaSignatureLabel,
  rcaValidityConcerns,
  sameRcaWindow,
  type RcaWindow,
} from './rcaModel'

interface Props {
  testId: string
  assessment: RcaAssessment
  hypothesis: RcaHypothesis
  window: RcaWindow | null
  report: RunArchiveReport | null
  history: MetricsHistoryPoint[]
  historyLoading?: boolean
  onWindowChange: (window: RcaWindow | null) => void
}

// The exemplar endpoint's page limit; it filters by name, population and
// window, so one page covers any window short of the whole run.
const EXEMPLAR_PAGE = 1000
const EXEMPLAR_ROWS = 25

// RootCauseInvestigation is one hypothesis's drill-down: its timeline, the
// symptom's transaction population, exemplars with trace links, the cause
// series and the run's matching error patterns, all for one time window.
export default function RootCauseInvestigation({ testId, assessment, hypothesis, window, report, history, historyLoading, onWindowChange }: Props) {
  const api = useApi()
  const involved = useMemo(() => rcaHypothesisAnomalies(hypothesis, assessment.anomalies), [assessment.anomalies, hypothesis])
  const span = useMemo(() => rcaHypothesisWindow(hypothesis, assessment.anomalies), [assessment.anomalies, hypothesis])
  // The run's archived phase events mark its load steps; a run without level
  // events falls back to steps inferred from the detection levels.
  const timeline = useRcaPhases(testId, report, assessment.detection)
  const populations = useMemo(() => {
    const seen = new Set<string>()
    return involved.flatMap(({ anomaly, role }) => {
      const ref = role === 'symptom' ? rcaSeriesPopulation(anomaly.series) : null
      if (!ref || seen.has(`${ref.population}/${ref.name}`)) return []
      seen.add(`${ref.population}/${ref.name}`)
      return [ref]
    })
  }, [involved])
  const causes = involved.filter(item => item.role === 'cause')
  const names = populations.map(p => p.name)
  const errors = rcaErrorPatterns(report, names, window)
  const exemplarName = populations[0]?.name || ''
  const exemplarPopulation = populations[0]?.population || ''
  const windowStart = window ? new Date(window.start).toISOString() : ''
  const windowEnd = window ? new Date(window.end).toISOString() : ''
  // The window in focus, or the hypothesis's span, opens in the run's trace
  // providers that have a window link template.
  const linkWindow = window || span
  const linkFrom = linkWindow ? new Date(linkWindow.start).toISOString() : ''
  const linkTo = linkWindow ? new Date(linkWindow.end).toISOString() : ''
  const [windowLinks, setWindowLinks] = useState<RcaWindowLink[]>([])
  useEffect(() => {
    setWindowLinks([])
    if (!linkFrom || !linkTo) return
    const controller = new AbortController()
    // Links are secondary: any failure leaves the timeline without them.
    Promise.resolve()
      .then(() => api.getRootCauseWindowLinks(testId, linkFrom, linkTo, { signal: controller.signal }))
      .then(result => { if (!controller.signal.aborted) setWindowLinks(Array.isArray(result?.links) ? result.links : []) })
      .catch(() => { if (!controller.signal.aborted) setWindowLinks([]) })
    return () => controller.abort()
  }, [api, linkFrom, linkTo, testId])

  const [exemplars, setExemplars] = useState<ExemplarPage | null>(null)
  const [exemplarError, setExemplarError] = useState('')
  const [exemplarsLoading, setExemplarsLoading] = useState(false)
  useEffect(() => {
    setExemplars(null)
    setExemplarError('')
    if (!exemplarName) return
    const controller = new AbortController()
    setExemplarsLoading(true)
    api.getTestArchiveExemplars(testId, { name: exemplarName, population: exemplarPopulation, from: windowStart || undefined, to: windowEnd || undefined, limit: EXEMPLAR_PAGE }, { signal: controller.signal })
      .then(page => { if (!controller.signal.aborted) setExemplars(page) })
      .catch(err => { if (!controller.signal.aborted) setExemplarError(getErrorMessage(err, 'Unable to load exemplars')) })
      .finally(() => { if (!controller.signal.aborted) setExemplarsLoading(false) })
    return () => controller.abort()
  }, [api, exemplarName, exemplarPopulation, testId, windowEnd, windowStart])
  const windowExemplars = exemplars?.exemplars || []
  const validityConcerns = useMemo(() => rcaValidityConcerns(assessment.validity.windows), [assessment.validity.windows])

  return (
    <section className="rca-investigation" aria-label={`Investigation: ${rcaSignatureLabel(hypothesis.signature)}`}>
      <div className="rca-investigation-heading">
        <h3>Investigating {rcaSignatureLabel(hypothesis.signature)}</h3>
        <div className="rca-window-controls" role="group" aria-label="Time window">
          <span className="run-report-muted">Window: {window ? `${formatRcaTime(window.start)} to ${formatRcaTime(window.end)}` : 'Whole run'}</span>
          {span && <button type="button" className="btn btn-sm btn-ghost" aria-pressed={sameRcaWindow(window, span)} onClick={() => onWindowChange(span)}>Hypothesis span</button>}
          {involved.map(({ anomaly, role }) => {
            const w = rcaAnomalyWindow(anomaly)
            return <button key={anomaly.id} type="button" className="btn btn-sm btn-ghost" aria-pressed={sameRcaWindow(window, w)} onClick={() => onWindowChange(w)}>Focus {role}: {rcaSeriesLabel(anomaly.series)} at {formatRcaClock(anomaly.onset)}</button>
          })}
          <button type="button" className="btn btn-sm btn-ghost" aria-pressed={!window} onClick={() => onWindowChange(null)}>Whole run</button>
        </div>
      </div>

      <RcaTimeline history={history} anomalies={involved} phases={timeline.phases} phasesInferred={timeline.inferred} validity={validityConcerns} window={window} loading={historyLoading} />
      {report?.manifest?.phase_events_truncated && <p className="run-report-muted">The run recorded more load-phase events than its archive keeps; later ones are missing.</p>}

      <div className="rca-investigation-grid">
        <section aria-label="Transaction populations">
          <h4>Transaction populations</h4>
          {populations.length === 0 ? <p className="run-report-muted">The symptom is not a transaction or action series.</p> : (
            <table className="rca-table">
              <thead><tr><th scope="col">Name</th><th scope="col">Population</th><th scope="col">Samples</th><th scope="col">Errors</th><th scope="col">p95</th></tr></thead>
              <tbody>{populations.map(({ population, name }) => {
                const stats = rcaPopulationStats(report, population, name)
                return <tr key={`${population}/${name}`}><th scope="row">{name}</th><td>{population === 'transaction' ? 'Business transaction' : 'Action'}</td>
                  <td>{stats ? formatNumber(stats.transactions, 0) : 'Not recorded'}</td><td>{stats ? formatPct(stats.error_pct, 2) : 'Not recorded'}</td><td>{stats?.p95_ms !== undefined ? formatMs(stats.p95_ms) : 'Not recorded'}</td></tr>
              })}</tbody>
            </table>
          )}
          <p className="run-report-muted">Whole-run statistics; the timeline and exemplars follow the window.</p>
        </section>

        <section aria-label="Cause evidence">
          <h4>Cause evidence</h4>
          {causes.length === 0 ? <p className="run-report-muted">No cause anomaly is cited; the hypothesis rests on the absence of competing evidence.</p> : (
            <ul className="rca-evidence-list">{causes.map(({ anomaly }) => (
              <li key={anomaly.id}>
                <strong>{rcaSeriesLabel(anomaly.series)}</strong> {formatRcaEffect(anomaly)} (peak z {formatNumber(Math.abs(anomaly.peak_z), 1)}), onset {formatRcaTime(anomaly.onset)}{anomaly.onset_uncertainty_ns > 0 && <> ± {formatRcaDuration(anomaly.onset_uncertainty_ns)}</>}, {anomaly.scope === 'load' ? `against the baseline at ${anomaly.level || 'its load level'}` : 'over time'}{anomaly.recovered ? ', recovered' : ', not recovered'}.
                {anomaly.evidence.map((ref, i) => <code key={i} className="rca-locator">{ref.locator}</code>)}
              </li>
            ))}</ul>
          )}
          {hypothesis.nodes.length > 0 && <p>Components: {hypothesis.nodes.map(id => rcaGraphNodeLabel(assessment, id)).join(', ')}</p>}
        </section>
      </div>

      {windowLinks.length > 0 && <p className="rca-window-links">Open {window ? 'this window' : 'the hypothesis span'} in {windowLinks.map(link => <a key={`${link.connection_id}/${link.provider}`} className="rca-trace-link" href={link.url} target="_blank" rel="noopener noreferrer">{rcaProviderLabel(link.provider)}</a>)}</p>}

      <section aria-label="Exemplars">
        <h4>Exemplars{exemplarName ? ` for ${exemplarName}` : ''}</h4>
        {exemplarsLoading && <p role="status">Loading exemplars…</p>}
        {exemplarError && <p className="run-report-error" role="alert">{exemplarError}</p>}
        {!exemplarName && <p className="run-report-muted">Exemplars are captured for transactions; this hypothesis has no transaction symptom.</p>}
        {exemplars && windowExemplars.length === 0 && <p className="run-report-muted">No exemplars were captured {window ? 'in this window' : 'for this transaction'}.</p>}
        {windowExemplars.length > 0 && (
          <table className="rca-table">
            <thead><tr><th scope="col">Time</th><th scope="col">Duration</th><th scope="col">Result</th><th scope="col">Why picked</th><th scope="col">Trace</th></tr></thead>
            <tbody>{windowExemplars.slice(0, EXEMPLAR_ROWS).map(e => (
              <tr key={`${e.trace_id}/${e.span_id || ''}/${e.timestamp}`}>
                <td>{formatRcaTime(e.timestamp)}</td><td>{formatMs(e.duration_ms)}</td>
                <td>{e.success ? 'Success' : `Failed${e.status_code ? ` (${e.status_code})` : ''}${e.error_class ? ` · ${e.error_class}` : ''}`}</td>
                <td>{e.reason.replace(/_/g, ' ')}</td>
                <td><code>{e.trace_id}</code>{e.links.map(link => <a key={`${link.connection_id}/${link.provider}`} className="rca-trace-link" href={link.url} target="_blank" rel="noopener noreferrer">Open in {rcaProviderLabel(link.provider)}</a>)}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
        {windowExemplars.length > EXEMPLAR_ROWS && <p className="run-report-muted">Showing {EXEMPLAR_ROWS} of {windowExemplars.length} exemplars in this window.</p>}
        {exemplars && exemplars.total > exemplars.exemplars.length && <p className="run-report-muted">Loaded the first {exemplars.exemplars.length} of {exemplars.total} matching exemplars; narrow the window to see the rest.</p>}
        {exemplars?.capture_truncated && <p className="run-report-muted">Exemplar capture reached its limit during the run; some intervals have none.</p>}
        {exemplars?.links_unavailable && <p className="run-report-muted">Trace links are unavailable: the run's trace connections could not be read.</p>}
      </section>

      <section aria-label="Errors">
        <h4>Errors</h4>
        {errors.length === 0 ? <p className="run-report-muted">No captured error pattern {window ? 'overlaps this window' : 'matches'}{names.length ? ` for ${names.join(', ')}` : ''}.</p> : (
          <table className="rca-table">
            <thead><tr><th scope="col">Pattern</th><th scope="col">Actions</th><th scope="col">Count</th><th scope="col">First seen</th><th scope="col">Last seen</th></tr></thead>
            <tbody>{errors.map(p => <tr key={p.signature}><td><code>{p.sample || p.signature}</code> <button type="button" className="btn btn-sm btn-ghost" onClick={() => rcaRevealElement(rcaErrorPatternAnchor(p.signature))}>Show in error patterns</button></td><td>{p.actions?.length ? p.actions.join(', ') : 'Not attributed'}</td><td>{formatNumber(p.count, 0)}</td><td>{formatRcaTime(p.first_seen)}</td><td>{formatRcaTime(p.latest_seen)}</td></tr>)}</tbody>
          </table>
        )}
        {errors.length > 0 && <p className="run-report-muted">Counts cover the whole run. A pattern not attributed to an action may come from any transaction.</p>}
      </section>
    </section>
  )
}
