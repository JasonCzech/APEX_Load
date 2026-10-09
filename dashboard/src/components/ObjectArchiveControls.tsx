import { useState } from 'react'
import { request } from '../hooks/apiClient'
import { getErrorMessage } from '../utils/errorUtils'
import { showErrorNotice, showUndoNotice } from './ux/undoNotice'

/**
 * Archive / Restore for a library object. inMenu renders only the button, for a
 * row's More menu: the menu closes when it is chosen, so the row shows the
 * archived state itself and a failure is reported in a notice.
 */
export default function ObjectArchiveControls({ resourceType, resourceId, resourceName, archivedAt, updatedAt, onChanged, canManage, inMenu = false }: { resourceType: 'script' | 'scenario'; resourceId: string; resourceName?: string; archivedAt?: string; updatedAt?: string; onChanged: () => void | Promise<void>; canManage: boolean; inMenu?: boolean }) {
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
    } catch (error) {
      const message = getErrorMessage(error, 'Unable to change archive state')
      if (inMenu) showErrorNotice(`Could not ${action} ${resourceType} “${resourceName || resourceId}”: ${message}`)
      else setError(message)
    } finally { setBusy(false) }
  }
  const button = canManage && <button type="button" className="btn btn-sm btn-ghost" disabled={busy || !updatedAt} aria-label={`${archivedAt ? 'Restore' : 'Archive'} ${resourceName || resourceType}`} onClick={event => { event.stopPropagation(); void change() }}>{busy ? 'Saving…' : archivedAt ? 'Restore' : 'Archive'}</button>
  if (inMenu) return button || null
  return <span className="object-archive-controls">{archivedAt && <span className="status-badge">Archived</span>}{button}{error && <span role="alert">{error}</span>}</span>
}
