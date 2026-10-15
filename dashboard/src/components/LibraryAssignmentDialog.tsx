import { assignmentMatches, assignmentSummary } from './libraryAssignmentModel'
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useModalDialog } from '../hooks/useModalDialog'
import type { Application, Release } from '../types'
import type { LibraryAssignment } from '../types/storedContent'
import { boundedAllSettled } from '../utils/boundedAllSettled'
import { getErrorMessage } from '../utils/errorUtils'
import DialogLayer from './ux/DialogLayer'
import './MoveScriptsDialog.css'
import './LibraryAssignmentDialog.css'

type Item = { id: string; name: string; library_assignment?: LibraryAssignment }
interface Props {
  projectId: string
  kind: 'scripts' | 'scenarios'
  items: Item[]
  onClose: () => void
  onBusy: (ids: string[]) => void
  onResult: (values: Map<string, LibraryAssignment>, notice: string) => void
}
export default function LibraryAssignmentDialog({ projectId, kind, items, onClose, onBusy, onResult }: Props) {
  const api = useApi()
  const [remaining, setRemaining] = useState(items)
  const [applications, setApplications] = useState<Application[]>([])
  const [releases, setReleases] = useState<Release[]>([])
  const [application, setApplication] = useState(() => new Set(items.map(item => item.library_assignment?.application?.id || '')).size === 1 ? items[0]?.library_assignment?.application?.id || '' : '__choose__')
  const [selected, setSelected] = useState<string[]>(() => { const first = items[0]?.library_assignment; const ids = first?.releases.map(release => release.id) || []; return items.every(item => assignmentMatches(item.library_assignment, first?.application?.id || '', ids)) ? ids : [] })
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [reload, setReload] = useState(0)
  const [busy, setBusy] = useState(false)
  const [failures, setFailures] = useState<Array<{id: string; error: string; conflict: boolean}>>([])
  const [notice, setNotice] = useState('')
  const active = useRef(false)
  const submitting = useRef(false)
  const dialogRef = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const close = () => { if (!submitting.current) onClose() }
  useModalDialog({ dialogRef, onClose: close })
  useLayoutEffect(() => { active.current = true; return () => { active.current = false } }, [])
  useEffect(() => {
    let cancelled = false
    setLoading(true); setLoadError('')
    void Promise.all([api.listApplications(projectId), application && application !== '__choose__' ? api.listReleases(application, true) : Promise.resolve({ releases: [] })]).then(([apps, response]) => {
      if (cancelled) return
      setApplications(apps.applications.filter(app => app.project_id === projectId))
      setReleases(response.releases.filter(release => release.application_id === application))
    }).catch(error => { if (!cancelled) setLoadError(getErrorMessage(error, 'Unable to load assignments.')) }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [api, projectId, application, reload])
  const changed = remaining.filter(item => !assignmentMatches(item.library_assignment, application, selected))
  const conflicts = failures.some(failure => failure.conflict)
  const valid = application === '' || applications.some(app => app.id === application)
  const reloadConflicts = async () => {
    if (submitting.current) return
    submitting.current = true; setBusy(true)
    try {
      const values = await boundedAllSettled<Item, Item>(remaining, item => kind === 'scripts' ? api.getScript(item.id) : api.getScenario(item.id))
      if (!active.current) return
      setRemaining(current => current.map((item, index) => values[index].status === 'fulfilled' ? { ...item, library_assignment: (values[index] as PromiseFulfilledResult<Item>).value.library_assignment } : item))
      setFailures(values.flatMap((value, index) => value.status === 'rejected' ? [{ id: remaining[index].id, error: getErrorMessage(value.reason, 'Reload failed'), conflict: true }] : []))
      setNotice('Assignments reloaded. Review the replacement and apply again.')
    } finally { if (active.current) { submitting.current = false; setBusy(false) } }
  }
  const submit = async () => {
    if (submitting.current || loading || loadError || !valid || conflicts || !changed.length) return
    submitting.current = true; setBusy(true); onBusy(changed.map(item => item.id))
    const unchanged = remaining.length - changed.length
    const outcomes = await boundedAllSettled(changed, item => {
      if (!active.current) throw new Error('Project changed')
      return api.replaceLibraryAssignment(kind, item.id, application || null, selected, item.library_assignment?.revision || 0)
    })
    if (!active.current) return
    const successful = new Map<string, LibraryAssignment>()
    const errors: typeof failures = []
    outcomes.forEach((result, index) => {
      if (result.status === 'fulfilled') successful.set(changed[index].id, result.value)
      else errors.push({ id: changed[index].id, error: getErrorMessage(result.reason, 'Assignment failed'), conflict: result.reason?.status === 409 || result.reason?.status === 412 })
    })
    const message = `Updated ${successful.size}; unchanged ${unchanged}; failed ${errors.length}.`
    setFailures(errors); setNotice(message); setRemaining(changed.filter(item => !successful.has(item.id)))
    submitting.current = false; setBusy(false); onBusy([]); onResult(successful, message)
    if (!errors.length) onClose()
  }
  return <DialogLayer><div className="modal-overlay"><div className="modal move-scripts-dialog library-assignment-dialog" ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby={titleId} aria-busy={busy}>
    <div className="modal-header"><h3 id={titleId}>Change application/releases</h3></div>
    <div className="modal-form">
      <p>{items.length === 1 ? items[0].name : `${items.length} selected ${kind}`}</p>
      <details><summary>Current assignments</summary><ul>{remaining.map(item => <li key={item.id}>{item.name}: {assignmentSummary(item.library_assignment)}</li>)}</ul></details>
      <label>Application<select aria-label="Application" className="settings-input" value={application} disabled={busy || loading} onChange={event => { setApplication(event.target.value); setSelected([]) }}><option value="__choose__" disabled>Choose an application</option><option value="">Unassigned (clear assignment)</option>{applications.map(app => <option key={app.id} value={app.id}>{app.name}</option>)}</select></label>
      {application && application !== '__choose__' && <fieldset disabled={busy || loading}><legend>Assigned releases</legend><label>Search releases<input className="settings-input" type="search" autoComplete="off" value={query} onChange={event => setQuery(event.target.value)} /></label>{releases.filter(release => release.name.toLowerCase().includes(query.toLowerCase())).map(release => {
        const retained = remaining.every(item => item.library_assignment?.releases.some(current => current.id === release.id))
        return <label key={release.id} style={{ display: 'block' }}><input type="checkbox" checked={selected.includes(release.id)} disabled={Boolean(release.archived_at) && !retained} onChange={event => setSelected(current => event.target.checked ? [...current, release.id] : current.filter(id => id !== release.id))} />{release.name}{release.archived_at ? ' (archived)' : ''}</label>
      })}</fieldset>}
      {valid && <p>Replacement: {application ? `${applications.find(app => app.id === application)?.name} · ${selected.map(id => releases.find(release => release.id === id)?.name || id).join(', ') || 'No releases'}` : 'Unassigned'}. {changed.length} will change.</p>}
      {loading && <p role="status">Loading applications and releases…</p>}
      {loadError && <p role="alert">{loadError} <button onClick={() => setReload(value => value + 1)}>Retry loading</button></p>}
      {notice && <p role="status">{notice}</p>}
      {!!failures.length && <ul role="alert">{failures.map(failure => <li key={failure.id}>{remaining.find(item => item.id === failure.id)?.name}: {failure.error}</li>)}</ul>}
      {conflicts && <button disabled={busy} onClick={() => { void reloadConflicts() }}>Reload conflicting assignments</button>}
    </div><div className="modal-actions"><button className="btn btn-secondary" disabled={busy} onClick={close}>Cancel</button><button className="btn btn-primary" disabled={busy || loading || !!loadError || !valid || conflicts || !changed.length} onClick={() => { void submit() }}>{busy ? 'Saving…' : failures.length ? 'Retry failed assignments' : 'Apply assignment'}</button></div>
  </div></div></DialogLayer>
}
