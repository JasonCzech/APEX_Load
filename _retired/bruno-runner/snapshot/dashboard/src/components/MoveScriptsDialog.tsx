import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { type Folder, type StoredScript } from '../types'
import { boundedAllSettled } from '../utils/boundedAllSettled'
import { getErrorMessage } from '../utils/errorUtils'
import { brunoDestinationFolderOptions } from './brunoFolderOptions'
import { dialogBackdropProps } from './dialogBackdropProps'
import DialogLayer from './ux/DialogLayer'
import './MoveScriptsDialog.css'

interface Props {
  projectId: string
  scripts: StoredScript[]
  onClose: () => void
  onFoldersLoaded: (folders: Folder[]) => void
  onBusy: (ids: string[]) => void
  onResult: (movedIds: string[], unchangedIds: string[], folderId: string | null, notice: string) => void
}

export default function MoveScriptsDialog({ projectId, scripts, onClose, onBusy, onFoldersLoaded, onResult }: Props) {
  const api = useApi()
  const [remaining, setRemaining] = useState(scripts)
  const [destination, setDestination] = useState<string | undefined>(() => {
    const folders = new Set(scripts.map(script => script.folder_id || ''))
    return folders.size === 1 ? [...folders][0] : undefined
  })
  const [folders, setFolders] = useState<Folder[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [reload, setReload] = useState(0)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [failures, setFailures] = useState<Array<{ id: string; name: string; error: string }>>([])
  const active = useRef(false)
  const submitting = useRef(false)
  const dialogRef = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const close = () => { if (!submitting.current) onClose() }
  useModalDialog({ dialogRef, onClose: close, returnFocusSelector: '.library-folder-filter' })
  useLayoutEffect(() => {
    active.current = true
    return () => { active.current = false }
  }, [])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setLoadError('')
    void api.listFolders(projectId, 'scripts').then(response => {
      if (!cancelled) {
        const available = (response.folders || []).filter(folder => folder.project_id === projectId && folder.category === 'scripts')
        setFolders(available)
        onFoldersLoaded(available)
      }
    }).catch(error => {
      if (!cancelled) setLoadError(getErrorMessage(error, 'Unable to load destination folders.'))
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [api, projectId, reload, onFoldersLoaded])

  const validDestination = destination !== undefined && (destination === '' || folders.some(folder => folder.id === destination))
  const changed = remaining.filter(script => (script.folder_id || '') !== destination)
  const move = async () => {
    if (submitting.current || loading || loadError || !validDestination || changed.length === 0) return
    submitting.current = true
    setBusy(true)
    setFailures([])
    const target = destination || null
    const unchanged = remaining.filter(script => (script.folder_id || '') === destination).map(script => script.id)
    onBusy(changed.map(script => script.id))
    const outcomes = await boundedAllSettled(changed, script => {
      if (!active.current) throw new Error('Project changed before move started.')
      return api.moveScript(script.id, target)
    })
    if (!active.current) return
    const moved = changed.filter((_, index) => outcomes[index].status === 'fulfilled').map(script => script.id)
    const failed = changed.filter((_, index) => outcomes[index].status === 'rejected')
    const errors = outcomes.flatMap((outcome, index) => outcome.status === 'rejected'
      ? [{ id: changed[index].id, name: changed[index].name, error: getErrorMessage(outcome.reason, 'Unable to move script.') }]
      : [])
    const message = `Moved ${moved.length}; unchanged ${unchanged.length}; failed ${failed.length}.`
    setNotice(message)
    setFailures(errors)
    setRemaining(failed)
    submitting.current = false
    setBusy(false)
    onBusy([])
    onResult(moved, unchanged, target, message)
    if (failed.length === 0) onClose()
  }

  return <DialogLayer>
    <div className="modal-overlay" {...dialogBackdropProps(close, 'Close move scripts')}>
      <div className="modal move-scripts-dialog" ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby={titleId} aria-busy={busy}>
        <div className="modal-header"><h3 id={titleId}>Move to folder</h3></div>
        <div className="modal-form">
          <p>{scripts.length === 1 ? scripts[0].name : `${scripts.length} selected scripts`}</p>
          <label>Destination folder
            <select className="settings-input" value={destination ?? '__choose__'} disabled={busy || loading || Boolean(loadError)} onChange={event => setDestination(event.target.value)}>
              <option value="__choose__" disabled>Choose a folder</option>
              <option value="">Unfiled (project root)</option>
              {brunoDestinationFolderOptions(folders).map(({ folder, label }) => <option key={folder.id} value={folder.id}>{label}</option>)}
            </select>
          </label>
          {loading && <p role="status">Loading script folders…</p>}
          {loadError && <div role="alert">{loadError} <button className="btn btn-secondary" onClick={() => setReload(value => value + 1)}>Retry folders</button></div>}
          {!loading && !loadError && destination !== undefined && !validDestination && <p role="alert">The selected folder is unavailable. Choose another destination.</p>}
          {validDestination && changed.length === 0 && <p>These scripts are already in this folder.</p>}
          {notice && <p role="status">{notice}</p>}
          {failures.length > 0 && <ul role="alert">{failures.map(failure => <li key={failure.id}>{failure.name}: {failure.error}</li>)}</ul>}
        </div>
        <div className="modal-actions">
          <button className="btn btn-secondary" disabled={busy} onClick={close}>Cancel</button>
          <button className="btn btn-primary" disabled={busy || loading || Boolean(loadError) || !validDestination || changed.length === 0} onClick={() => { void move() }}>{busy ? 'Moving…' : failures.length > 0 ? 'Retry failed moves' : 'Move'}</button>
        </div>
      </div>
    </div>
  </DialogLayer>
}
