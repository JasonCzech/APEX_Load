import { useMemo } from 'react'
import type { CorrelationRelationshipCoverage as Coverage, CorrelationRelationshipState } from '../types/scripts'
import { admitRelationshipEvidence } from './correlationEvidenceAdmission'
import { useEvidencePage } from './useEvidencePage'

const PAGE_SIZE = 20
const LABELS: Record<CorrelationRelationshipState, string> = {
  producer_missing: 'Replay producer missing',
  extractor_unavailable: 'Replay extracted value unavailable',
  consumer_missing: 'Replay consumer missing',
  producer_unchanged: 'Producer value unchanged',
  stale_literal: 'Recorded literal remains in replay consumer',
  no_stale_literal: 'Recorded literal not observed in replay consumer',
  manual_encoding: 'Encoded consumer needs manual review',
  manual_surface: 'Consumer surface needs manual review',
}
const count = (value: number | undefined) => Number.isSafeInteger(value) && value! >= 0 ? value! : 0
const step = (value: number) => Number.isSafeInteger(value) && value >= 0 ? String(value) : 'Unavailable'
const stateLabel = (value: CorrelationRelationshipState) => Object.prototype.hasOwnProperty.call(LABELS, value) ? LABELS[value] : 'Unrecognized relationship state'

export default function CorrelationRelationshipCoverage({ coverage: suppliedCoverage }: { coverage?: Coverage }) {
  const evidence = useMemo(() => admitRelationshipEvidence(suppliedCoverage), [suppliedCoverage])
  const [page, setPage] = useEvidencePage(suppliedCoverage)
  if (!evidence.present) return null
  const coverage = evidence.coverage
  const rows = evidence.rows
  const pageCount = Math.max(1, Math.ceil(rows.length / PAGE_SIZE))
  const current = Math.min(page, pageCount - 1)
  return <section className="correlation-relationship-coverage" aria-label="Correlation relationship evidence">
    <h5>Correlation relationship evidence</h5>
    <p role="status">{coverage?.complete === true && !evidence.malformed ? 'Relationship inspection complete.' : 'Relationship inspection incomplete.'}</p>
    {evidence.malformed && <p role="note">Relationship details are malformed or unavailable. Inspection is incomplete.</p>}
    <p>{count(coverage?.producer_count)} detected producer {count(coverage?.producer_count) === 1 ? 'binding' : 'bindings'} · {count(coverage?.consumer_count)} recorded consumer {count(coverage?.consumer_count) === 1 ? 'link' : 'links'}. These counts describe observed evidence, not successful extraction, correlation, or replay.</p>
    <p>{count(coverage?.missing_producers)} missing replay {count(coverage?.missing_producers) === 1 ? 'producer' : 'producers'} · {count(coverage?.unavailable_extractors)} unavailable replay extracted {count(coverage?.unavailable_extractors) === 1 ? 'value' : 'values'} · {count(coverage?.missing_consumers)} missing replay {count(coverage?.missing_consumers) === 1 ? 'consumer' : 'consumers'}.</p>
    {count(coverage?.producer_count) === 0 && <p>No producer bindings detected. This does not prove login or session bootstrap coverage.</p>}
    <ul>{(Object.entries(LABELS) as [CorrelationRelationshipState, string][]).filter(([state]) => count(coverage?.state_counts?.[state]) > 0).map(([state, label]) => <li key={state}>{label}: {count(coverage?.state_counts?.[state])}</li>)}</ul>
    {(coverage?.details_complete !== true || evidence.malformed || count(coverage?.details_omitted) > 0 || evidence.omittedRows > 0) && <p role="note">Detailed relationship evidence incomplete: {count(coverage?.details_omitted) + evidence.omittedRows} relationship rows not shown. Aggregate counts cover the inspected relationships.</p>}
    {rows.length > 0 && <details><summary>Review observed relationships ({rows.length})</summary>
      <ul>{rows.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE).map((row, index) => <li key={current * PAGE_SIZE + index}>Producer step ID {step(row.producer_step_id)} → consumer step ID {step(row.consumer_step_id)}: {stateLabel(row.state)}. {row.producer_observed === true ? 'Producer observed' : 'Producer absent'} · {row.extractor_available === true ? 'Extracted value available' : 'Extracted value unavailable'} · {row.consumer_observed === true ? 'Consumer observed' : 'Consumer absent'}.</li>)}</ul>
      {pageCount > 1 && <div className="replay-panel-actions"><span>{current * PAGE_SIZE + 1}–{Math.min((current + 1) * PAGE_SIZE, rows.length)} of {rows.length} relationships</span><button type="button" className="btn btn-secondary btn-sm" disabled={current === 0} onClick={() => setPage(current - 1)}>Previous relationships</button><button type="button" className="btn btn-secondary btn-sm" disabled={current + 1 >= pageCount} onClick={() => setPage(current + 1)}>Next relationships</button></div>}
    </details>}
  </section>
}
