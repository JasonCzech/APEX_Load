import { useState } from 'react'
import type { SourceReview, SourceReviewMapping } from '../types/scripts'
import type { AuraActionState, AuraOutcomeBatch, AuraOutcomeReport } from '../types/scriptImports'
import { ImportEvidenceFacts } from './ImportConversionDetails'
import './AuraOutcomeReview.css'

const STATES: AuraActionState[] = ['SUCCESS', 'ERROR', 'INCOMPLETE', 'ABORTED', 'UNKNOWN']
const PAGE_SIZE = 20
const OUTCOME_LIMIT = 50
const ISSUE_LABELS: Record<string, string> = {
  invalid_json: 'Envelope JSON could not be inspected',
  duplicate_member: 'Duplicate envelope fields',
  byte_limit: 'Envelope size limit reached',
  depth_limit: 'Envelope nesting limit reached',
  work_limit: 'Inspection work limit reached',
  action_limit: 'Action inspection limit reached',
  invalid_envelope: 'Invalid action envelope',
  empty_actions: 'No actions in the envelope',
  invalid_action: 'Invalid action structure',
  invalid_id: 'Invalid action identity',
  duplicate_id: 'Duplicate action identities',
  missing_descriptor: 'Request action descriptor missing',
  missing_state: 'Response action state missing',
  unknown_state: 'Response action state not recognized',
  non_success_state: 'Captured action did not succeed',
  missing_response: 'Request action has no matched response',
  unexpected_response: 'Response action has no matched request',
  invalid_form: 'Request form could not be inspected',
  missing_message: 'Request message missing',
  duplicate_message: 'Multiple request messages',
}

// Treat wire values as untrusted even though the server emits a value-free report.
function count(value: number | undefined): number {
  return Number.isSafeInteger(value) && value! >= 0 ? value! : 0
}
function ordinal(value: number | undefined): string {
  return count(value) > 0 ? String(value) : 'unavailable'
}
function stateLabel(value: AuraActionState): AuraActionState {
  return STATES.includes(value) ? value : 'UNKNOWN'
}
function issueLabel(code: string): string {
  return Object.prototype.hasOwnProperty.call(ISSUE_LABELS, code) ? ISSUE_LABELS[code] : 'Unrecognized inspection diagnostic'
}
function sourceMapping(batch: AuraOutcomeBatch, sourceReview?: SourceReview): SourceReviewMapping | undefined {
  if (!batch.action_path) return undefined
  const matches = sourceReview?.mappings?.filter(mapping => mapping.target_path === batch.action_path && mapping.target_kind === 'action') || []
  return matches.length === 1 ? matches[0] : undefined
}
function batchStatus(batch: AuraOutcomeBatch): string {
  if (batch.analysis.has_response !== true) return 'No response captured'
  if (batch.request_valid !== true || batch.analysis.complete !== true) return 'Incomplete evidence'
  return batch.analysis.success === true ? 'Captured success' : 'Captured failure'
}
function requiresReview(batch: AuraOutcomeBatch): boolean {
  return batch.request_valid !== true || batch.analysis.has_response !== true || batch.analysis.complete !== true || batch.analysis.success !== true
}

export default function AuraOutcomeReview({ report, sourceReview, onViewSource }: { report?: AuraOutcomeReport; sourceReview?: SourceReview; onViewSource?: (mapping: SourceReviewMapping) => void }) {
  const [onlyReview, setOnlyReview] = useState(true)
  const [page, setPage] = useState(0)
  if (!report) return null
  const batches = report.batches || []
  const reviewBatches = batches.filter(requiresReview)
  const filtered = onlyReview && reviewBatches.length ? reviewBatches : batches
  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const currentPage = Math.min(page, pageCount - 1)
  const visible = filtered.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE)
  const complete = report.complete === true && count(report.omitted_batches) === 0 && count(report.incomplete_batches) === 0 && count(report.missing_responses) === 0 && count(report.invalid_requests) === 0
  const failedActions = count(report.state_counts?.ERROR) + count(report.state_counts?.INCOMPLETE) + count(report.state_counts?.ABORTED)
  const omittedDetails = count(report.outcomes_omitted) + count(report.issues_omitted) + count(report.request_issues_omitted)
  const issues = Object.entries(report.issue_counts || {}).filter(([, amount]) => count(amount) > 0)

  return <section className="aura-outcome-review" aria-label="Aura outcomes">
    <h4>Aura outcomes</h4>
    <p className="aura-outcome-status" role="status">
      {complete ? 'Capture inspection complete.' : 'Capture inspection incomplete.'}
      {failedActions > 0 && ` ${failedActions} captured action${failedActions === 1 ? '' : 's'} did not succeed.`}
    </p>
    <p>These are captured baseline results, not replay results. HTTP 200 does not establish Aura action success. Successful capture inspection does not verify login, correlation, or replay readiness.</p>
    <ImportEvidenceFacts facts={[
      { label: 'Batches inspected', value: `${count(report.inspected_batches)} / ${count(report.total_batches)}` },
      { label: 'Complete batches', value: count(report.complete_batches) },
      { label: 'Request actions', value: count(report.request_actions) },
      { label: 'Response actions', value: count(report.response_actions) },
      { label: 'Matched actions', value: count(report.matched_actions) },
      ...STATES.map(state => ({ label: state, value: count(report.state_counts?.[state]) })),
    ]} />
    {(!complete || count(report.omitted_batches) > 0 || count(report.missing_responses) > 0 || count(report.invalid_requests) > 0) && <div className="aura-outcome-limits">
      <strong>Evidence limits</strong>
      <p>{count(report.incomplete_batches)} incomplete batches · {count(report.missing_responses)} missing responses · {count(report.invalid_requests)} invalid requests · {count(report.omitted_batches)} batches omitted by inspection limits.</p>
      <p>Missing or ambiguous evidence remains unresolved; it is not counted as a successful action.</p>
    </div>}
    {count(report.batch_limit) > 0 && <p>Inspection limit: {count(report.batch_limit)} batches · {Math.floor(count(report.analysis_byte_limit) / (1024 * 1024))} MiB of envelope analysis. Action counts cover inspected batches only.</p>}
    {(report.details_complete === false || omittedDetails > 0) && <div className="aura-outcome-limits">
      <strong>Detailed rows limited</strong>
      <p>{count(report.outcomes_omitted)} action outcomes · {count(report.issues_omitted)} captured findings · {count(report.request_issues_omitted)} imported request findings omitted from retained details. Aggregate counts and inspection results include these inspected rows.</p>
      {count(report.outcome_detail_limit) > 0 && <p>Detail limit: {count(report.outcome_detail_limit)} action outcomes · {count(report.issue_detail_limit)} findings across all batches. Failed and unknown action outcomes are retained before successful outcomes.</p>}
    </div>}
    {issues.length > 0 && <details className="aura-outcome-diagnostics"><summary>Inspection findings ({issues.length})</summary>
      <ul>{issues.map(([code, amount], index) => <li key={index}>{issueLabel(code)} ({count(amount)})</li>)}</ul>
    </details>}
    {batches.length > 0 && <details className="aura-outcome-batches">
      <summary>Review captured batches ({batches.length})</summary>
      {reviewBatches.length > 0 && <label className="aura-outcome-filter"><input type="checkbox" checked={onlyReview} onChange={event => { setOnlyReview(event.target.checked); setPage(0) }} /> Only batches requiring review ({reviewBatches.length})</label>}
      <p className="aura-outcome-source-hint">HAR entry numbers identify the original capture order. Imported action positions may differ after filtering or conversion. {sourceReview?.mappings?.length ? 'Source Mapping contains available request mappings.' : 'Source mappings are unavailable for this review.'}</p>
      {visible.map((batch, index) => {
        const mapping = sourceMapping(batch, sourceReview)
        return <div className="aura-outcome-batch" key={currentPage * PAGE_SIZE + index}>
        <div className="aura-outcome-batch-heading"><strong>Request {ordinal(batch.action_sequence)}</strong><span>{batchStatus(batch)}</span></div>
        {mapping && onViewSource && <button type="button" className="btn btn-secondary btn-sm" onClick={() => onViewSource(mapping)}>View request {ordinal(batch.action_sequence)} source mapping</button>}
        <p>{batch.har_entry_id ? `HAR entry ${ordinal(batch.har_entry_id)} · ` : ''}{batch.request_valid === true ? 'Imported request structure valid' : 'Imported request structure needs review'} · {count(batch.analysis.matched_actions)} matched / {count(batch.analysis.request_actions)} requested / {count(batch.analysis.response_actions)} returned actions</p>
        {(count(batch.analysis.outcomes_omitted) > 0 || count(batch.analysis.issues_omitted) > 0) && <p>{count(batch.analysis.outcomes_omitted)} action outcomes · {count(batch.analysis.issues_omitted)} captured findings omitted from this batch’s retained details.</p>}
        {count(batch.request_issues_omitted) > 0 && <p>{count(batch.request_issues_omitted)} imported request findings omitted from this batch’s retained details.</p>}
        {(batch.analysis.outcomes?.length || 0) > 0 && <ul className="aura-outcome-actions">{batch.analysis.outcomes!.slice(0, OUTCOME_LIMIT).map((outcome, outcomeIndex) => <li key={outcomeIndex}>Action {ordinal(outcome.ordinal)}: <strong>{stateLabel(outcome.state)}</strong></li>)}</ul>}
        {(batch.analysis.outcomes?.length || 0) > OUTCOME_LIMIT && <details><summary>Remaining action outcomes ({batch.analysis.outcomes!.length - OUTCOME_LIMIT})</summary><ul>{batch.analysis.outcomes!.slice(OUTCOME_LIMIT).map((outcome, outcomeIndex) => <li key={outcomeIndex}>Action {ordinal(outcome.ordinal)}: <strong>{stateLabel(outcome.state)}</strong></li>)}</ul></details>}
        {(batch.request_issues?.length || 0) > 0 && <div><strong>Imported request findings</strong><ul>{batch.request_issues!.slice(0, OUTCOME_LIMIT).map((issue, issueIndex) => <li key={issueIndex}>{issueLabel(issue.code)}{count(issue.ordinal) > 0 ? ` at action ${ordinal(issue.ordinal)}` : ''}</li>)}</ul>{batch.request_issues!.length > OUTCOME_LIMIT && <details><summary>Remaining imported request findings ({batch.request_issues!.length - OUTCOME_LIMIT})</summary><ul>{batch.request_issues!.slice(OUTCOME_LIMIT).map((issue, issueIndex) => <li key={issueIndex}>{issueLabel(issue.code)}{count(issue.ordinal) > 0 ? ` at action ${ordinal(issue.ordinal)}` : ''}</li>)}</ul></details>}</div>}
        {(batch.analysis.issues?.length || 0) > 0 && <strong>Captured envelope findings</strong>}
        {(batch.analysis.issues?.length || 0) > 0 && <ul>{batch.analysis.issues!.slice(0, OUTCOME_LIMIT).map((issue, issueIndex) => <li key={issueIndex}>{issueLabel(issue.code)}{count(issue.ordinal) > 0 ? ` at action ${ordinal(issue.ordinal)}` : ''}</li>)}</ul>}
        {(batch.analysis.issues?.length || 0) > OUTCOME_LIMIT && <details><summary>Remaining captured findings ({batch.analysis.issues!.length - OUTCOME_LIMIT})</summary><ul>{batch.analysis.issues!.slice(OUTCOME_LIMIT).map((issue, issueIndex) => <li key={issueIndex}>{issueLabel(issue.code)}{count(issue.ordinal) > 0 ? ` at action ${ordinal(issue.ordinal)}` : ''}</li>)}</ul></details>}
      </div>})}
      <div className="aura-outcome-pagination"><span>{filtered.length ? currentPage * PAGE_SIZE + 1 : 0}–{Math.min((currentPage + 1) * PAGE_SIZE, filtered.length)} of {filtered.length} batches</span><button type="button" className="btn btn-secondary btn-sm" disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>Previous batches</button><button type="button" className="btn btn-secondary btn-sm" disabled={currentPage + 1 >= pageCount} onClick={() => setPage(currentPage + 1)}>Next batches</button></div>
    </details>}
  </section>
}
