import type { BrowserReplayEvent } from '../types/replay'
import SnapshotBodyPreview from './SnapshotBodyPreview'

export function ReplayExtractions({ event }: { event: BrowserReplayEvent }) {
  if (event.action_type === 'live_table') return null
  const omitted = event.protocol_evidence?.concurrent_group_extractions_omitted === true
  const count = event.protocol_evidence?.concurrent_group_extractions_omitted_count
  const hasCount = typeof count === 'number' && Number.isSafeInteger(count) && count > 0
  const hasValues = event.extractions && Object.keys(event.extractions).length > 0
  const unappliedCount = event.unapplied_extractions_omitted_count
  const hasUnappliedCount = typeof unappliedCount === 'number' && Number.isSafeInteger(unappliedCount) && unappliedCount > 0
  const hasUnappliedValues = event.unapplied_extractions && Object.keys(event.unapplied_extractions).length > 0
  const children = event.streamed_children || []
  const omittedChildren = event.streamed_children_omitted_count || 0
  if (!omitted && !hasValues && !hasUnappliedValues && !hasUnappliedCount && !children.length && !omittedChildren && !event.credential_classification_error) return null
  return <div className="replay-code-block">
    {event.credential_classification_error && <p role="status">Credential classification: {event.credential_classification_error}</p>}
    {(omitted || hasValues) && <>
      <span className="replay-detail-label">Extractions</span>
      {omitted && <p role="status">{hasCount ? `${count} ${count === 1 ? 'extraction was' : 'extractions were'} omitted` : 'Some extractions were omitted'} from the retained debugger evidence. The displayed values may be incomplete.</p>}
      {hasValues && <SnapshotBodyPreview body={JSON.stringify(event.extractions, null, 2)} headers={{ 'Content-Type': 'application/json' }} />}
    </>}
    {(hasUnappliedValues || hasUnappliedCount) && <>
      <span className="replay-detail-label">Unapplied extractions</span>
      <p>These values were not applied to the session variables.</p>
      {hasUnappliedCount && <p role="status">{unappliedCount} {unappliedCount === 1 ? 'extraction was' : 'extractions were'} omitted from the retained debugger evidence.</p>}
      {hasUnappliedValues && <SnapshotBodyPreview body={JSON.stringify(event.unapplied_extractions, null, 2)} headers={{ 'Content-Type': 'application/json' }} />}
    </>}
    {children.map((child, index) => <details key={index}>
      <summary>{child.action_name || `Streamed child ${index + 1}`} · {child.success ? 'Passed' : 'Failed'}</summary>
      {child.error_message && <p>{child.error_message}</p>}
      {child.credential_classification_error && <p role="status">Credential classification: {child.credential_classification_error}</p>}
      {child.extractions && Object.keys(child.extractions).length > 0 && <><span className="replay-detail-label">Extractions</span><SnapshotBodyPreview body={JSON.stringify(child.extractions, null, 2)} headers={{ 'Content-Type': 'application/json' }} /></>}
      {Boolean(child.extractions_omitted_count) && <p role="status">{child.extractions_omitted_count} applied {child.extractions_omitted_count === 1 ? 'extraction was' : 'extractions were'} omitted from retained evidence.</p>}
      {child.unapplied_extractions && Object.keys(child.unapplied_extractions).length > 0 && <><span className="replay-detail-label">Unapplied extractions</span><p>These values were not applied to the session variables.</p><SnapshotBodyPreview body={JSON.stringify(child.unapplied_extractions, null, 2)} headers={{ 'Content-Type': 'application/json' }} /></>}
      {Boolean(child.unapplied_extractions_omitted_count) && <p role="status">{child.unapplied_extractions_omitted_count} unapplied {child.unapplied_extractions_omitted_count === 1 ? 'extraction was' : 'extractions were'} omitted from retained evidence.</p>}
    </details>)}
    {omittedChildren > 0 && <p role="status">{omittedChildren} streamed child {omittedChildren === 1 ? 'event was' : 'events were'} omitted from retained evidence.</p>}
  </div>
}
