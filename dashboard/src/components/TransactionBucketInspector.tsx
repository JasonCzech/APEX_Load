import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { DashboardAnalyticsQuery } from '../hooks/api/useDashboardDefinitionsApi'
import type { TransactionEvent } from '../types'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { formatSnapshotRequestLineAndHeaders, formatSnapshotResponseStatusAndHeaders } from '../utils/snapshotFormatUtils'

import { bucketInspectorParams, transactionWaterfall, type InspectorSelection } from './bucketInspectorModel'
import { formatMs, formatNumber } from './runReport/runReportModel'
import { MISSING_VALUE } from './runReport/reportPresentation'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'
export default function TransactionBucketInspector({ query, selection, onClose }: { query: DashboardAnalyticsQuery; selection: InspectorSelection; onClose: () => void }) {
  const [page, setPage] = useState(0)
  const [details, setDetails] = useState<{ transactions: TransactionEvent[]; total: number } | null>(null)
  const [error, setError] = useState('')
  const encoded = bucketInspectorParams(query, selection, page).toString()
  useEffect(() => {
    const abort = new AbortController(); setDetails(null); setError('')
    request<{ transactions: TransactionEvent[]; total: number }>(`/tests/${encodeURIComponent(selection.run)}/archive/transactions?${encoded}`, { signal: abort.signal })
      .then(value => { if (!abort.signal.aborted) setDetails(value) })
      .catch(reason => { if (!abort.signal.aborted) setError(reason instanceof Error ? reason.message : 'Inspector failed') })
    return () => abort.abort()
  }, [selection.run, encoded])
  return <section aria-label="Filtered transaction inspector"><h4>Transactions from {formatDisplayDateTime(selection.from, { seconds: true })} to {formatDisplayDateTime(selection.to, { seconds: true })}</h4><button onClick={onClose}>Close inspector</button>{error && <p role="alert">{error}</p>}{!details && !error && <p>Loading transactions…</p>}{details && <><p>{formatNumber(details.total)} matching observations</p>{details.transactions.map((event, i) => <details key={event.event_id || i}><summary>{event.action_name} · {formatMs(event.duration_ms)} · {event.success ? 'Success' : 'Failure'}</summary><p>{formatDisplayDateTime(event.timestamp, { milliseconds: true })}{event.error_message ? ` · ${event.error_message}` : ''}</p><section aria-label="Transaction timing waterfall"><h5>Retained timing waterfall</h5><p>Phase durations and elapsed milestones have separate labels. Missing phases remain unavailable; total duration does not imply server processing.</p>{transactionWaterfall(event).map(([label, value]) => <div key={label}><span>{label}: {value === undefined || !Number.isFinite(value) || value < 0 ? MISSING_VALUE : formatMs(value)}</span>{value !== undefined && Number.isFinite(value) && value >= 0 && event.duration_ms > 0 && <meter aria-label={label} min={0} max={Math.max(event.duration_ms, value)} value={value} />}</div>)}</section><h5>Request</h5><SnapshotTextPreview text={formatSnapshotRequestLineAndHeaders(event.snapshot)} /><SnapshotBodyPreview body={event.snapshot?.request_body} headers={event.snapshot?.request_headers} emptyMessage="No retained request body" /><h5>Response</h5><SnapshotTextPreview text={formatSnapshotResponseStatusAndHeaders(event.snapshot, event.status_code)} /><SnapshotBodyPreview body={event.snapshot?.response_body} headers={event.snapshot?.response_headers} emptyMessage="No retained response body" /></details>)}<button disabled={page === 0} onClick={() => setPage(page - 1)}>Previous observations</button><button disabled={(page + 1) * 25 >= details.total} onClick={() => setPage(page + 1)}>Next observations</button></>}</section>
}
