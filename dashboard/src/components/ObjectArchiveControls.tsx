import { useState } from 'react'
import { request } from '../hooks/apiClient'
import { getErrorMessage } from '../utils/errorUtils'

export default function ObjectArchiveControls({ resourceType, resourceId, archivedAt, updatedAt, onChanged, canManage }: { resourceType: 'script' | 'scenario'; resourceId: string; archivedAt?: string; updatedAt?: string; onChanged: () => void | Promise<void>; canManage: boolean }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const action = archivedAt ? 'restore' : 'archive'
  return <span className="object-archive-controls">{archivedAt && <span className="status-badge">Archived</span>}{canManage && <button type="button" className="btn btn-sm btn-ghost" disabled={busy || !updatedAt} onClick={async event => { event.stopPropagation(); setBusy(true); setError(''); try { await request(`/${resourceType}s/${encodeURIComponent(resourceId)}/${action}`, { method: 'POST', body: JSON.stringify({ updated_at: updatedAt }) }); await onChanged() } catch (error) { setError(getErrorMessage(error, 'Unable to change archive state')) } finally { setBusy(false) } }}>{busy ? 'Saving…' : archivedAt ? 'Restore' : 'Archive'}</button>}{error && <span role="alert">{error}</span>}</span>
}
