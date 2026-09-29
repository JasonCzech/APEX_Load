import type { RunArchiveFailure, RunArchiveRecovery } from '../../types'
import { executionDate } from './executionPresentation'

function failureDescription(reason: string, historical = false): string {
  if (reason === 'archive_collection_unconfirmed' && historical) return 'Collection had not been confirmed when the run ended.'
  if (reason === 'archive_collection_unconfirmed') return 'The run ended before archive collection was confirmed. Collection may still be in progress or may have been interrupted.'
  if (reason === 'local_archive_failed') return 'The controller could not finalize the local archive. Captured files may still be available for recovery.'
  if (reason === 'remote_collection_unavailable') return 'No generator archive was available when collection finished.'
  return 'The controller could not collect the generator archive.'
}

function recoveryDescription(state: string): { heading: string; detail: string } | null {
  switch (state) {
    case 'collection_pending': return { heading: 'Archive recovery queued', detail: 'Automatic collection retries are queued. Recovery depends on surviving source data.' }
    case 'cache_recovery_pending': return { heading: 'Partial evidence recovery queued', detail: 'Collection retries have reached their attempt or time limit. Partial evidence can still be prepared from surviving verified copies.' }
    case 'publication_pending': return { heading: 'Archive publication pending', detail: 'Publication has not been confirmed. Completion may require a surviving staging copy or an already uploaded archive.' }
    case 'exhausted': return { heading: 'Automatic archive recovery ended', detail: 'No archive could be prepared within the recovery limit. Surviving source data may require operator recovery.' }
    case 'unavailable': return { heading: 'Saved archive unavailable', detail: 'No saved archive or pending publication is recorded. The archive may have been removed by retention.' }
    default: return null
  }
}

export default function RunArchiveAvailabilitySummary({ failure, recovery }: { failure?: RunArchiveFailure; recovery?: RunArchiveRecovery }) {
  const status = recovery ? recoveryDescription(recovery.state) : null
  if (status) return <>
    <h2>{status.heading}</h2><p>{status.detail}</p>
    {recovery?.expected_sources !== undefined && recovery.verified_sources !== undefined && <p>Sources verified during collection: {recovery.verified_sources} of {recovery.expected_sources}. These receipts do not confirm that the files are still available.</p>}
    {recovery?.recovery_attempts !== undefined && <p>Recovery attempts recorded: {recovery.recovery_attempts}.</p>}
    {failure && <p>Earlier collection issue: {failureDescription(failure.reason, true)} Recorded: {executionDate(failure.recorded_at)}</p>}
    <p>Retained controller records are available below and may be incomplete. Checking again refreshes archive availability; it does not restart collection.</p>
  </>
  if (failure) return <>
    <h2>{failure.reason === 'archive_collection_unconfirmed' ? 'Archive collection unconfirmed' : failure.reason === 'local_archive_failed' ? 'Local archive finalization failed' : 'Archive collection failed'}</h2>
    <p>{failureDescription(failure.reason)}</p><p>Recorded: {executionDate(failure.recorded_at)}</p>
    <p>Retained controller records are available below and may be incomplete. Checking again refreshes archive availability; it does not restart collection.</p>
  </>
  return <><h2>Saved archive unavailable</h2><p>Retained controller records are available below. Archive publication progress is unknown; these records do not establish complete workload or performance evidence.</p></>
}
