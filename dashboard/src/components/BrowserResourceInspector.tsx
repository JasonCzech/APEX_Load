import { useState, type FormEvent } from 'react'
import type { BrowserResourcePageQuery } from '../types/browserResourcePage'
import { DEFAULT_BROWSER_RESOURCE_QUERY } from '../types/browserResourcePage'
import { readArchiveBrowserResourcePage } from '../hooks/api/useBrowserResourceApi'
import { useBrowserResourcePage, type BrowserResourcePageLoader } from './useBrowserResourcePage'
import { browserResourceRowKey } from './browserResourceEvidenceModel'
import BrowserNativeRequestEvidence from './BrowserNativeRequestEvidence'
import './BrowserResourceInspector.css'

interface Props { testID: string; projectID: string; sourceIdentity: string; loadPage?: BrowserResourcePageLoader }
const textFilters = [
  ['action_name', 'Capturing action', 256], ['group', 'Group', 256], ['region', 'Region', 256], ['resource_type', 'Resource type', 64],
  ['source_generation', 'Source generation', 32], ['request_id', 'Native request identity', 128], ['redirect_chain_id', 'Redirect chain identity', 128],
  ['document_id', 'Document identity', 128], ['initiating_action_id', 'Initiating action identity', 128], ['observation_action_id', 'Observation action identity', 128],
  ['origin', 'Exact origin', 512], ['browser_engine', 'Browser engine', 32], ['device_profile', 'Device profile', 128],
] as const
const number = (value: number | undefined, suffix: string) => value === undefined ? 'Unavailable' : `${value.toLocaleString('en-US', { maximumFractionDigits: 6 })}${suffix}`

/** A scope change remounts draft controls as well as retiring asynchronous
 * reads; project/run filters can never carry into another selected archive. */
export default function BrowserResourceInspector(props: Props) {
  return <ResourceInspectorBody key={JSON.stringify([props.testID, props.projectID, props.sourceIdentity])} {...props} />
}
function ResourceInspectorBody({ testID, projectID, sourceIdentity, loadPage = readArchiveBrowserResourcePage }: Props) {
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [sort, setSort] = useState<BrowserResourcePageQuery['sort_by']>('capture_order')
  const [direction, setDirection] = useState<BrowserResourcePageQuery['sort_dir']>('asc')
  const [limit, setLimit] = useState<BrowserResourcePageQuery['limit']>(50)
  const [query, setQuery] = useState<BrowserResourcePageQuery>({ ...DEFAULT_BROWSER_RESOURCE_QUERY })
  const [refreshRevision, setRefreshRevision] = useState(0)
  const [pageNumber, setPageNumber] = useState(1)
  const { page, loading, error } = useBrowserResourcePage({ testID, projectID, sourceIdentity, query, loadPage, refreshRevision })
  const ready = Boolean(testID && projectID && sourceIdentity)
  const apply = (event: FormEvent) => {
    event.preventDefault()
    const filters = Object.fromEntries(Object.entries(draft).filter(([, value]) => value !== ''))
    setQuery({ ...filters, limit, sort_by: sort, sort_dir: direction })
    setPageNumber(1); setRefreshRevision(value => value + 1)
  }
  const first = () => { const firstQuery = { ...query }; delete firstQuery.cursor; setQuery(firstQuery); setPageNumber(1); setRefreshRevision(value => value + 1) }
  return <section className="tx-section archive-summary-section browser-resource-inspector" aria-label="Retained archive resource inspector">
    <div className="archive-section-header archive-section-header-stack"><div><h3 className="chart-title">Retained resource observations</h3>
      <p className="archive-section-copy">Filter and inspect retained network-entry observations in bounded archive pages.</p></div></div>
    <p>Legacy captures can include HTTP document resources. Native browser request details appear only when explicitly retained. Retained observations are not a complete network census. Action capture timestamps and native request clocks remain distinct; clock alignment and capture coverage are unavailable.</p>
    {!ready ? <p role="status">Run and project context is unavailable.</p> : <>
      <form onSubmit={apply} className="browser-resource-filter-form" aria-label="Archive resource filters">
        <div className="browser-resource-filter-row">
          <label>Capturing action<input value={draft.action_name || ''} maxLength={256} onChange={event => setDraft(value => ({ ...value, action_name: event.target.value }))} /></label>
          <label>Failure classification<select aria-label="Failure classification" value={draft.failure_kind || ''} onChange={event => setDraft(value => ({ ...value, failure_kind: event.target.value }))}><option value="">All retained observations</option>{['http', 'transport', 'timeout', 'canceled', 'body_limit'].map(value => <option key={value} value={value}>{value}</option>)}</select></label>
          <label>Sort retained observations<select aria-label="Sort retained observations" value={sort} onChange={event => setSort(event.target.value as typeof sort)}><option value="capture_order">Capture order</option><option value="duration_ms">Legacy duration</option><option value="native_start_ms">Native request epoch start</option><option value="response_encoded_body">Native encoded body bytes</option></select></label>
          <label>Sort direction<select aria-label="Sort direction" value={direction} onChange={event => setDirection(event.target.value as typeof direction)}><option value="asc">Ascending</option><option value="desc">Descending</option></select></label>
          <label>Resources per page<select aria-label="Resources per page" value={limit} onChange={event => setLimit(Number(event.target.value) as typeof limit)}>{[25, 50, 100].map(value => <option key={value}>{value}</option>)}</select></label>
        </div>
        <details><summary>Exact identity and cohort filters</summary><div className="browser-resource-filter-row">
          {textFilters.slice(1).map(([key, label, maximum]) => <label key={key}>{label}<input value={draft[key] || ''} maxLength={maximum} onChange={event => setDraft(value => ({ ...value, [key]: event.target.value }))} /></label>)}
          <label>Native request state<select aria-label="Native request state" value={draft.state || ''} onChange={event => setDraft(value => ({ ...value, state: event.target.value }))}><option value="">All retained states</option>{['finished', 'failed', 'censored'].map(value => <option key={value}>{value}</option>)}</select></label>
        </div><p>Identity filters match the source's opaque identities exactly. Capturing action names are separate from initiating and observation action identities.</p></details>
        <button className="btn btn-secondary btn-sm" type="submit">Apply resource filters</button>
      </form>
      <div className="browser-resource-pagination" aria-label="Archive resource pages">
        <button className="btn btn-secondary btn-sm" type="button" onClick={first}>First page / retry</button>
        <button className="btn btn-secondary btn-sm" type="button" disabled={loading || !page?.next_cursor} onClick={() => { if (page?.next_cursor) { setQuery(value => ({ ...value, cursor: page.next_cursor })); setPageNumber(value => value + 1) } }}>Next resource page</button>
      </div>
      {loading && <p role="status">Loading retained resource observations…</p>}
      {error && <p role="alert">{error}</p>}
      {page && <>
        <p role="status">{page.population.availability === 'retained_observations'
          ? `Page ${pageNumber}: ${page.items.length} retained observations. ${page.population.matched_retained_observations!.toLocaleString()} match these filters out of ${page.population.retained_entry_observations!.toLocaleString()} retained entry observations.`
          : 'The archive resource projection is unavailable.'}</p>
        <p>Source: {page.source_format} · {page.source_timestamp_precision} · {page.source_integrity === 'verified_manifest_and_artifact' ? 'Manifest and artifact integrity verified' : 'Legacy scan fingerprint; producer integrity is unverified'}.</p>
        <details><summary>Archive source identity</summary><p className="browser-resource-source-revision">{page.source_revision}</p></details>
        {page.items.length ? <div role="region" aria-label="Retained archive resources table" tabIndex={0} className="browser-resource-table-scroll"><table className="tx-table"><thead><tr>
          <th scope="col">Resource</th><th scope="col">Type / status</th><th scope="col">Legacy duration / transfer</th><th scope="col">Capturing action / cohort</th><th scope="col">Inspection</th>
        </tr></thead><tbody>{page.items.map((row, index) => <tr key={browserResourceRowKey(JSON.stringify(row.locator), row.browser_evidence)}>
          <td className="browser-resource-url">{row.url || 'Unavailable'}</td><td>{row.resource_type || 'Unavailable'} / {row.status_code ?? 'Unavailable'}{row.failure_kind ? ` · ${row.failure_kind}` : ''}</td>
          <td>{number(row.duration_ms, ' ms')} / {number(row.bytes_received, ' B')}</td><td>{row.action_name || 'Unavailable'}<br />{[row.group, row.region, row.device_profile, row.device_platform, row.browser_engine].filter(Boolean).join(' / ') || 'Unavailable'}</td>
          <td><details><summary>Inspect retained resource {index + 1}</summary>
            <dl className="browser-resource-details"><dt>Capture locator</dt><dd>Transaction {row.locator.transaction_ordinal}, resource {row.locator.resource_ordinal}, {row.locator.branch}</dd>
              <dt>Captured by action at (UTC)</dt><dd>{row.timestamp || 'Unavailable'} · {row.timestamp_precision}</dd><dt>Page URL</dt><dd>{row.page_url || 'Unavailable'}</dd><dt>Method</dt><dd>{row.method || 'Unavailable'}</dd></dl>
            {row.browser_evidence ? <BrowserNativeRequestEvidence evidence={row.browser_evidence} /> : <p>Native request evidence: {row.evidence_state}. Request identity and phases remain unavailable.</p>}
          </details></td>
        </tr>)}</tbody></table></div> : <p>{page.population.availability === 'unavailable' ? 'No resource projection was retained.' : 'No retained observations match these filters.'}</p>}
      </>}
    </>}
  </section>
}
