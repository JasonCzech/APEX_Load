import { useMemo } from 'react'
import type { BrowserDocumentSummaryMode, BrowserRuntimeCohort } from '../types/browserDocumentEvidence'
import { buildBrowserDocumentPanelModel, browserDocumentDistributionBins, formatBrowserDocumentValue } from './browserDocumentEvidenceModel'
import './BrowserDocumentEvidencePanel.css'

interface Props {
  summary?: unknown
  observations?: unknown
  summaryMode: BrowserDocumentSummaryMode
  observationsTruncated?: boolean
  /** The API's soft-navigation state; soft navigations are never measured. */
  softNavigation?: string
}
const observedBoolean = (value: boolean | undefined) => value === true ? 'Yes' : value === false ? 'No' : 'Unavailable'
const label = (value: string | undefined) => value || 'Not recorded'
const frame = (value: string) => value === 'main_frame' ? 'Main frame' : 'Child frame (separate document)'
const lifecycle = (value: string) => value === 'navigation' ? 'Navigation' : 'BFCache restore'
function RuntimeCohort({ cohort }: { cohort: BrowserRuntimeCohort | null | undefined }) {
  if (!cohort) return <p>Actual runtime cohort unavailable. This cohort cannot qualify a final distribution.</p>
  return <dl className="browser-document-details">
    <dt>Actual browser version / platform</dt><dd>{label(cohort.browser_version)} / {label(cohort.platform)}</dd>
    <dt>Actual viewport</dt><dd>{cohort.viewport_width} × {cohort.viewport_height}</dd>
    <dt>Headless / mobile / touch</dt><dd>{observedBoolean(cohort.headless)} / {observedBoolean(cohort.mobile)} / {observedBoolean(cohort.touch)}</dd>
    <dt>Device scale / CPU throttle</dt><dd>{cohort.device_scale_factor} / {cohort.cpu_throttle_rate}</dd>
    <dt>Network latency / bandwidth</dt><dd>{cohort.network_latency_ms} ms / {cohort.network_bandwidth_kbps} Kbps</dd>
    <dt>Shaping applied</dt><dd>{observedBoolean(cohort.shaping_applied)}</dd>
    <dt>Cache enabled / cache content URLs / check newer versions</dt><dd>{observedBoolean(cohort.cache_enabled)} / {observedBoolean(cohort.cache_content_urls)} / {observedBoolean(cohort.check_newer_versions)}</dd>
    <dt>New-user policy / applied</dt><dd>{label(cohort.new_user_policy)} / {observedBoolean(cohort.new_user_policy_applied)}</dd>
    {cohort.unavailable_reason && <><dt>Cohort availability</dt><dd>{cohort.unavailable_reason}</dd></>}
  </dl>
}

export default function BrowserDocumentEvidencePanel({ summary, observations, summaryMode, observationsTruncated, softNavigation }: Props) {
  const model = useMemo(() => buildBrowserDocumentPanelModel(summary, observations, summaryMode, observationsTruncated),
    [summary, observations, summaryMode, observationsTruncated])
  const provisionalLabel = model.mode === 'live-history' ? 'First seen provisional' : model.mode === 'archive-latest' ? 'Retained pending' : 'Provisional semantics unavailable'
  return <section className="browser-document-evidence tx-section archive-summary-section" aria-label="Browser document evidence">
    <h3 className="chart-title">Browser document lifecycle evidence</h3>
    <p>These distributions use qualified final document measurements. Action counts, legacy action summaries and browser process CPU/RSS do not supply their denominator. No score is calculated.</p>
    <p>Soft (same-document) navigations: {softNavigation || 'unsupported'}. They are not measured as navigations.</p>
    {model.mode === 'live-history' && <p>Live intervals retain first-seen provisional history. A measurement can later reach a terminal state; these provisional counts overlap terminal history and are never added to a final population.</p>}
    {model.mode === 'archive-latest' && <p>Archived summaries retain the latest state per source and measurement identity. Pending or censored measurements do not enter qualified final distributions.</p>}
    {model.state === 'absent' && <p>Document summary unavailable for this peer or archive. Legacy action measurements do not substitute for it.</p>}
    {model.issue && <p role="status">{model.issue}</p>}
    {model.incomplete === true && <p role="status">Document evidence is incomplete. {model.droppedObservations?.toLocaleString('en-US')} observations were dropped.</p>}
    {model.incomplete === false && model.droppedObservations !== null && <p>{model.droppedObservations.toLocaleString('en-US')} dropped observations reported.</p>}
    {!!model.rows.length && <div className="browser-document-table-scroll" role="region" tabIndex={0} aria-label="Document metric populations">
      <table className="tx-table browser-document-populations"><thead><tr><th>Metric</th><th>Captured cohort</th><th>Frame</th><th>Lifecycle</th><th>Qualified final</th><th>Unavailable final</th><th>Unqualified final</th><th>Censored</th><th>{provisionalLabel}</th><th>Final mean</th><th>Final min</th><th>Final max</th><th>Evidence</th></tr></thead>
        <tbody>{model.rows.map((row, i) => {
          const m = row.summary
          if (!m) return <tr key={row.key}><td colSpan={13}>Tuple {i + 1} unavailable: {row.issue}</td></tr>
          const d = m.distribution
          return <tr key={row.key}>
            <td>{m.metric.toUpperCase()}</td><td>{m.engine} · {label(m.device_profile)}<br />Group: {label(m.group)} · Region: {label(m.region)}</td>
            <td>{frame(m.frame_scope)}</td><td>{lifecycle(m.lifecycle_phase)}</td>
            <td>{m.eligible_final.toLocaleString('en-US')}</td><td>{m.unavailable_final.toLocaleString('en-US')}</td><td>{m.unqualified_final.toLocaleString('en-US')}</td><td>{m.censored.toLocaleString('en-US')}</td><td>{m.provisional.toLocaleString('en-US')}</td>
            <td>{formatBrowserDocumentValue(d.count ? d.sum / d.count : null, m.unit)}</td><td>{formatBrowserDocumentValue(d.count ? d.min : null, m.unit)}</td><td>{formatBrowserDocumentValue(d.count ? d.max : null, m.unit)}</td>
            <td><details><summary>Inspect {m.metric.toUpperCase()} tuple {i + 1}</summary>
              <dl className="browser-document-details"><dt>Algorithm</dt><dd>{m.algorithm}</dd><dt>Algorithm SHA-256</dt><dd>{m.algorithm_sha256}</dd><dt>Measurement unit</dt><dd>{m.unit}</dd><dt>Population</dt><dd>{d.count.toLocaleString('en-US')} eligible final document measurements</dd></dl>
              <RuntimeCohort cohort={m.cohort} />
              <p>Concrete document identities and source windows are available only in retained observations below; this summary does not infer them.</p>
              <h4>Known exclusion reasons</h4>
              {Object.keys(m.excluded_reasons ?? {}).length ? <ul>{Object.entries(m.excluded_reasons!).map(([reason, n]) => <li key={reason}>{reason}: {n.toLocaleString('en-US')}</li>)}</ul> : <p>Reason tallies not supplied.</p>}
              <p>Reason tallies can cover only part of the excluded population.</p>
              {d.count > 0 ? <table className="tx-table" aria-label={`${m.metric.toUpperCase()} qualified final histogram for tuple ${i + 1}`}><thead><tr><th>Bucket in {m.unit}</th><th>Count</th></tr></thead><tbody>{browserDocumentDistributionBins(d).map(bin => <tr key={bin.index}><td>{bin.index === 0 ? '[' : '('}{bin.lower.toLocaleString('en-US', { maximumSignificantDigits: 6 })}, {bin.upper === null ? 'overflow' : bin.upper.toLocaleString('en-US', { maximumSignificantDigits: 6 })}]</td><td>{bin.count.toLocaleString('en-US')}</td></tr>)}</tbody></table> : <p>No qualified final values; distribution statistics remain unavailable.</p>}
            </details></td>
          </tr>
        })}</tbody>
      </table>
    </div>}
    {model.state === 'ready' && !model.rows.length && <p>No document metric tuples were recorded.</p>}
    <h4>Retained document observations</h4>
    <p>Bounded display of at most 256 observations. Source UTC windows are shown as supplied; cross-host clock synchronization is not verified. Child frames and BFCache lifecycles remain separate.</p>
    {model.observationsTruncated === true && <p role="status">The retained display reached its cap. Additional source observations are excluded from this view.</p>}
    {model.observationsTruncated === null && <p>Retained display completeness was not supplied.</p>}
    {model.observationIssue && <p role="status">{model.observationIssue}</p>}
    {!!model.observations.length && <div className="browser-document-table-scroll" role="region" tabIndex={0} aria-label="Retained document measurements"><table className="tx-table browser-document-observations"><thead><tr><th>Metric</th><th>State</th><th>Observed value</th><th>Qualification</th><th>Source window</th><th>Identity and runtime</th></tr></thead><tbody>{model.observations.map((row, i) => {
      const o = row.observation
      if (!o) return <tr key={row.key}><td colSpan={6}>Observation {i + 1} unavailable: {row.issue}</td></tr>
      return <tr key={`${row.key}/${i}`}><td>{o.metric.toUpperCase()}</td><td>{o.state} · {o.boundary}</td>
        <td>{formatBrowserDocumentValue(o.available ? o.value : null, o.unit)}{!o.available && o.unavailable_reason ? ` · ${o.unavailable_reason}` : ''}</td>
        <td>{o.qualified ? 'Eligible qualified final' : 'Excluded from qualified final distribution'}</td>
        <td>{o.window_start}<br />through {o.window_end}</td>
        <td><details><summary>Inspect document observation {i + 1}</summary><dl className="browser-document-details">
          <dt>Source generation</dt><dd>{o.source_generation}</dd><dt>Document / lifecycle</dt><dd>{o.document_id} / {o.lifecycle_id}</dd><dt>Frame / navigation</dt><dd>{o.frame_id} / {o.navigation_id}</dd>
          <dt>Measurement / revision</dt><dd>{o.measurement_id} / {o.revision}</dd><dt>Frame scope / lifecycle</dt><dd>{frame(o.frame_scope)} / {lifecycle(o.lifecycle_phase)}</dd>
          <dt>Captured at (source UTC)</dt><dd>{o.captured_at}</dd><dt>Algorithm / SHA-256</dt><dd>{o.algorithm} / {o.algorithm_sha256}</dd><dt>Observed interactions</dt><dd>{o.interaction_count === undefined ? 'Unavailable' : o.interaction_count.toLocaleString('en-US')}</dd>
        </dl><RuntimeCohort cohort={o.cohort} /></details></td></tr>
    })}</tbody></table></div>}
    {!model.observations.length && !model.observationIssue && <p>No concrete retained document observations were supplied. Summary counts do not identify individual documents or windows.</p>}
  </section>
}
