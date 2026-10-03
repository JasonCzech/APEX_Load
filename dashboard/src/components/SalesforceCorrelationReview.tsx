import { useMemo } from 'react'
import type { SourceReview, SourceReviewMapping } from '../types/scripts'
import type { SalesforceNativePreview, SalesforceNativeRequestReference } from '../types/scriptImports'
import './SalesforceCorrelationReview.css'
import { admitNativeCorrelationEvidence } from './correlationEvidenceAdmission'
import { useEvidencePage } from './useEvidencePage'

const PAGE_SIZE = 20
const DETAIL_LIMIT = 2048
const ACTION_PATH = /^(?:init|actions|teardown)\[\d+\](?:\.[A-Za-z_]\w*(?:\[\d+\])?)*$/
const count = (value: number | undefined) => Number.isSafeInteger(value) && value! >= 0 ? value! : 0
const path = (value: string | undefined) => typeof value === 'string' && value.length <= 4096 && ACTION_PATH.test(value) ? value : ''

function exactSourceMapping(reference: SalesforceNativeRequestReference, review?: SourceReview): SourceReviewMapping | undefined {
  if (!reference.source_mapping_id || !reference.source_file || !path(reference.original_action_path)) return undefined
  const matches = Array.isArray(review?.mappings) ? review.mappings.filter(mapping => mapping && typeof mapping === 'object' && mapping.id === reference.source_mapping_id) : []
  const mapping = matches.length === 1 ? matches[0] : undefined
  return mapping?.source_file === reference.source_file && mapping.target_path === reference.original_action_path ? mapping : undefined
}

function Reference({ reference, label, review, nativeFinal, onViewSource }: { reference?: SalesforceNativeRequestReference; label: string; review?: SourceReview; nativeFinal: boolean; onViewSource?: (mapping: SourceReviewMapping) => void }) {
  if (!reference) return <p>{label}: captured request reference unavailable.</p>
  const mapping = exactSourceMapping(reference, review)
  const originalPath = path(reference.original_action_path)
  const nativePath = nativeFinal ? path(reference.native_action_path) : ''
  return <div className="salesforce-correlation-reference">
    <strong>{label}</strong>
    <span>HAR entry: {count(reference.har_entry_id) || 'Unavailable'}</span>
    <span>Original import path: <code>{originalPath || 'Unavailable'}</code></span>
    {nativePath && <span>Native path at import: <code>{nativePath}</code></span>}
    {mapping && onViewSource ? <button type="button" className="btn btn-secondary btn-sm" onClick={() => onViewSource(mapping)}>View {label.toLowerCase()} source mapping</button> : <span>Exact source link unavailable.</span>}
  </div>
}

function Consumers({ references, omitted, review, nativeFinal, onViewSource }: { references: SalesforceNativeRequestReference[]; omitted?: number; review?: SourceReview; nativeFinal: boolean; onViewSource?: (mapping: SourceReviewMapping) => void }) {
  const [page, setPage] = useEvidencePage(references)
  const retained = references.slice(0, DETAIL_LIMIT)
  const pageCount = Math.max(1, Math.ceil(retained.length / PAGE_SIZE))
  const current = Math.min(page, pageCount - 1)
  return <>
    {retained.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE).map((reference, index) => <Reference key={current * PAGE_SIZE + index} reference={reference} label={`Consumer ${current * PAGE_SIZE + index + 1}`} review={review} nativeFinal={nativeFinal} onViewSource={onViewSource} />)}
    {retained.length === 0 && <p>No consumer request references retained.</p>}
    {(count(omitted) > 0 || references.length > DETAIL_LIMIT) && <p>{count(omitted) + Math.max(0, references.length - DETAIL_LIMIT)} consumer references not shown.</p>}
    {pageCount > 1 && <div className="salesforce-correlation-pagination"><span>Consumer references {current * PAGE_SIZE + 1}–{Math.min((current + 1) * PAGE_SIZE, retained.length)} of {retained.length}</span><button type="button" className="btn btn-secondary btn-sm" disabled={current === 0} onClick={() => setPage(current - 1)}>Previous consumer references</button><button type="button" className="btn btn-secondary btn-sm" disabled={current + 1 >= pageCount} onClick={() => setPage(current + 1)}>Next consumer references</button></div>}
  </>
}

export default function SalesforceCorrelationReview({ preview, sourceReview, onViewSource }: { preview?: SalesforceNativePreview; sourceReview?: SourceReview; onViewSource?: (mapping: SourceReviewMapping) => void }) {
  const evidence = useMemo(() => admitNativeCorrelationEvidence(preview), [preview])
  const [page, setPage] = useEvidencePage(preview)
  if (!evidence.present || !evidence.coverage && !evidence.rows.length && !evidence.malformed) return null
  const coverage = evidence.coverage
  const rows = evidence.rows
  const pageCount = Math.max(1, Math.ceil(rows.length / PAGE_SIZE))
  const current = Math.min(page, pageCount - 1)
  const nativeFinal = evidence.nativeFinal
  return <section className="salesforce-correlation-review" aria-label="Captured correlation evidence">
    <h4>Captured correlation evidence</h4>
    <p role="status">{coverage?.complete === true && evidence.coverageValid && !evidence.malformed ? 'Reference inspection complete.' : 'Reference inspection incomplete.'}</p>
    <p>These are captured request relationships. Consumer counts describe request references, not field occurrences or successful runtime replacement. Native paths identify the draft at import.</p>
    {evidence.malformed && <p role="note">Correlation details are malformed or unavailable. Reference inspection is incomplete.</p>}
    {coverage ? <>
      <p>{count(coverage.discovered_correlations)} correlations discovered · {count(coverage.observed_consumer_references)} consumer request references.</p>
      <div className="salesforce-correlation-table-wrap"><table aria-label="Correlation reference coverage"><thead><tr><th>Evidence</th><th>Producers</th><th>Consumer references</th></tr></thead><tbody>
        <tr><th>Original import matches</th><td>{count(coverage.import_matched_producer_correlations)} matched · {count(coverage.import_unmatched_producer_correlations)} unmatched</td><td>{count(coverage.import_matched_consumer_references)} matched · {count(coverage.import_unmatched_consumer_references)} unmatched</td></tr>
        <tr><th>Exact source links</th><td>{count(coverage.source_linked_producer_correlations)} linked · {count(coverage.source_unlinked_producer_correlations)} unavailable</td><td>{count(coverage.source_linked_consumer_references)} linked · {count(coverage.source_unlinked_consumer_references)} unavailable</td></tr>
        {nativeFinal && <tr><th>Native paths at import</th><td>{count(coverage.native_mapped_producer_correlations)} mapped · {count(coverage.native_unmapped_producer_correlations)} unavailable</td><td>{count(coverage.native_mapped_consumer_references)} mapped · {count(coverage.native_unmapped_consumer_references)} unavailable</td></tr>}
      </tbody></table></div>
      {!coverage.source_review_available && <p>Source mappings are unavailable for this review.</p>}
      {!nativeFinal && <p>Native path coverage is unavailable after final transformations.</p>}
      <p>{count(coverage.pre_applied_correlations)} draft replacements · {count(coverage.manual_correlations)} correlations requiring manual review. Draft replacement does not validate login, session freshness, or replay.</p>
      {count(coverage.discovered_correlations) === 0 && <p>No correlations detected. This does not prove login or session bootstrap coverage.</p>}
      {(coverage.details_complete !== true || evidence.malformed || count(coverage.omitted_correlations) > 0 || count(coverage.omitted_consumer_references) > 0 || evidence.omittedRows > 0 || evidence.omittedReferences > 0) && <div role="note"><strong>Detailed evidence incomplete</strong><p>{count(coverage.retained_correlations)} correlation rows retained · {count(coverage.omitted_correlations) + evidence.omittedRows} correlation rows not shown · {count(coverage.retained_consumer_references)} consumer references retained · {count(coverage.omitted_consumer_references) + evidence.omittedReferences} consumer references not shown.</p></div>}
      {coverage.catalog_limited && <p role="note">Reference inspection limits were reached. Missing links remain unresolved.</p>}
    </> : <p>Reference coverage was not retained for this import. Sequence numbers alone cannot establish a source link.</p>}
    {rows.length > 0 && <details><summary>Review captured correlations ({rows.length})</summary>
      {rows.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE).map(correlation => <article key={correlation.ordinal} aria-label={`Captured correlation ${correlation.ordinal}`} className="salesforce-correlation-row">
        <h5>Correlation {correlation.ordinal}</h5>
        <p>{correlation.preApplied ? 'Draft replacement applied.' : 'Manual review required.'}</p>
        <Reference reference={correlation.producer} label="Producer" review={sourceReview} nativeFinal={nativeFinal} onViewSource={onViewSource} />
        <Consumers references={correlation.consumers} omitted={correlation.consumersOmitted} review={sourceReview} nativeFinal={nativeFinal} onViewSource={onViewSource} />
      </article>)}
      {pageCount > 1 && <div className="salesforce-correlation-pagination"><span>{current * PAGE_SIZE + 1}–{Math.min((current + 1) * PAGE_SIZE, rows.length)} of {rows.length} correlations</span><button type="button" className="btn btn-secondary btn-sm" disabled={current === 0} onClick={() => setPage(current - 1)}>Previous correlations</button><button type="button" className="btn btn-secondary btn-sm" disabled={current + 1 >= pageCount} onClick={() => setPage(current + 1)}>Next correlations</button></div>}
    </details>}
  </section>
}
