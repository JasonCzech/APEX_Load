import { useState } from 'react'
import { request } from '../hooks/apiClient'
import { getErrorMessage } from '../utils/errorUtils'
import { showUndoNotice } from './ux/undoNotice'

export default function ObjectArchiveControls({ resourceType, resourceId, resourceName, archivedAt, updatedAt, onChanged, canManage }: { resourceType: 'script' | 'scenario'; resourceId: string; resourceName?: string; archivedAt?: string; updatedAt?: string; onChanged: () => void | Promise<void>; canManage: boolean }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const action = archivedAt ? 'restore' : 'archive'
  const path = (next: 'archive' | 'restore') => `/${resourceType}s/${encodeURIComponent(resourceId)}/${next}`
  const change = async () => {
    setBusy(true); setError('')
    try {
      const changed = await request<{ updated_at?: string }>(path(action), { method: 'POST', body: JSON.stringify({ updated_at: updatedAt }) })
      await onChanged()
      // An archived row leaves the active list, so the undo lives outside it.
      if (action === 'archive') {
        showUndoNotice({
          message: `Archived ${resourceType} “${resourceName || resourceId}”.`,
          onUndo: async () => {
            await request(path('restore'), { method: 'POST', body: JSON.stringify({ updated_at: changed?.updated_at }) })
            await onChanged()
          },
        })
      }
    } catch (error) { setError(getErrorMessage(error, 'Unable to change archive state')) } finally { setBusy(false) }
  }
  return <span className="object-archive-controls">{archivedAt && <span className="status-badge">Archived</span>}{canManage && <button type="button" className="btn btn-sm btn-ghost" disabled={busy || !updatedAt} aria-label={`${archivedAt ? 'Restore' : 'Archive'} ${resourceName || resourceType}`} onClick={event => { event.stopPropagation(); void change() }}>{busy ? 'Saving…' : archivedAt ? 'Restore' : 'Archive'}</button>}{error && <span role="alert">{error}</span>}</span>
}
