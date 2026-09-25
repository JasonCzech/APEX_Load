import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useConfluenceApi, type ConfluenceConnection, type ConfluenceConnectionInput } from '../hooks/api/useConfluenceApi'
import { useNavigationGuard } from '../hooks/NavigationGuardProvider'
import { getErrorMessage } from '../utils/errorUtils'
import './CaptureConnections.css'

const blank = (): ConfluenceConnectionInput => ({ name:'', enabled:false, site_url:'', email:'', space_id:'', parent_page_id:'', token_secret_name:'', auto_publish:false })
const fromSaved = (connection: ConfluenceConnection): ConfluenceConnectionInput => ({ name:connection.name, enabled:connection.enabled, site_url:connection.site_url, email:connection.email, space_id:connection.space_id, parent_page_id:connection.parent_page_id, token_secret_name:'', auto_publish:connection.auto_publish, updated_at:connection.updated_at })

interface Props { projectId?: string; canManage?: boolean }

export default function ConfluenceConnectionsCard(props: Props) {
  return <Editor key={props.projectId || 'none'} {...props} />
}

function Editor({projectId, canManage = false}: Props) {
  const api = useConfluenceApi()
  const [connections,setConnections] = useState<ConfluenceConnection[]>([])
  const [publicURL,setPublicURL] = useState(false)
  const [editing,setEditing] = useState<ConfluenceConnection | null>(null)
  const [draft,setDraft] = useState<ConfluenceConnectionInput>(blank)
  const [open,setOpen] = useState(false)
  const [busy,setBusy] = useState(false)
  const [loading,setLoading] = useState(true)
  const [error,setError] = useState('')
  const [notice,setNotice] = useState('')
  const [deleting,setDeleting] = useState<string | null>(null)
  const automaticDestinationTaken = connections.some(connection => connection.auto_publish && connection.id !== editing?.id)
  const load = useCallback(async () => {
    if (!projectId) {setLoading(false);return}
    setLoading(true)
    try {const result=await api.listConfluenceConnections(projectId);setConnections(result.connections || []);setPublicURL(result.public_url_configured);setError('')}
    catch (cause) {setError(getErrorMessage(cause,'Failed to load Confluence connections'))}
    finally {setLoading(false)}
  },[api,projectId])
  useEffect(() => {void load()},[load])
  const reset = () => {setEditing(null);setDraft(blank());setOpen(false)}
  const save = async () => {
    if (!projectId || busy) return false
    setBusy(true);setError('');setNotice('')
    try {
      const payload={...draft, token_secret_name:draft.token_secret_name?.trim() || undefined}
      if (editing) await api.updateConfluenceConnection(projectId,editing.id,payload)
      else await api.createConfluenceConnection(projectId,payload)
      reset();await load();setNotice('Connection saved. Test the saved destination before publishing.');return true
    } catch (cause) {setError(getErrorMessage(cause,'Failed to save Confluence connection'));return false}
    finally {setBusy(false)}
  }
  const dirty=open && JSON.stringify(draft)!==JSON.stringify(editing?fromSaved(editing):blank())
  const dirtyRef=useRef(dirty);dirtyRef.current=dirty
  const saveRef=useRef(save);saveRef.current=save
  const resetRef=useRef(reset);resetRef.current=reset
  const guard=useMemo(() => ({id:'confluence-connection-draft',label:'Confluence connection draft',isDirty:()=>dirtyRef.current,save:()=>saveRef.current(),discard:()=>resetRef.current()}),[])
  useNavigationGuard(guard,dirty)
  if (!projectId) return <p>Select a project to configure Confluence publishing.</p>
  return <div className="capture-connections" aria-label="Confluence connections">
    <p>Publish a summary page for an archived run under a selected Confluence parent page. Automatic publishing is optional and applies to every archived run in this project. One connection can be the automatic destination.</p>
    {notice && <p role="status">{notice}</p>}
    {loading && <p role="status">Loading connections…</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {error && <button type="button" className="btn btn-secondary" onClick={() => void load()}>Retry loading connections</button>}
    {!loading && !error && connections.length===0 && <p>No Confluence connections are configured for this project.</p>}
    {connections.map(connection => <div className="capture-connection-row" key={connection.id}>
      <div><strong>{connection.name}</strong><p>{connection.site_url ? `${connection.site_url} · ` : ''}{connection.status}{connection.auto_publish ? ' · Automatic publishing' : ''}</p>
        {connection.last_check_status && <p role="status">Last test: {connection.last_check_status}{connection.last_check_message ? ` — ${connection.last_check_message}` : ''}</p>}
      </div>
      {canManage && <div className="capture-actions">
        <button type="button" className="btn btn-secondary btn-sm" disabled={busy || open} onClick={() => {setEditing(connection);setDraft(fromSaved(connection));setOpen(true)}}>Edit</button>
        <button type="button" className="btn btn-secondary btn-sm" disabled={busy || open || !connection.has_token} onClick={async () => {setBusy(true);try {await api.testConfluenceConnection(projectId,connection.id);await load();setNotice('Confluence destination verified.')} catch(cause) {await load();setError(getErrorMessage(cause,'Connection test failed'))} finally {setBusy(false)}}}>Test saved connection</button>
        <button type="button" className="btn btn-danger btn-sm" disabled={busy || open} onClick={() => setDeleting(connection.id)}>Delete</button>
        {deleting===connection.id && <div role="group" aria-label="Confirm connection deletion"><p>Delete {connection.name}? Existing Confluence pages will remain.</p><button type="button" className="btn btn-danger" disabled={busy} onClick={async () => {setBusy(true);try {await api.deleteConfluenceConnection(projectId,connection.id);setDeleting(null);await load()} catch(cause) {setError(getErrorMessage(cause,'Delete failed'))} finally {setBusy(false)}}}>Confirm delete</button><button type="button" className="btn btn-secondary" onClick={() => setDeleting(null)}>Keep connection</button></div>}
      </div>}
    </div>)}
    {canManage && !open && <button type="button" className="btn btn-primary" onClick={() => {setEditing(null);setDraft(blank());setOpen(true);setNotice('')}}>Add Confluence connection</button>}
    {canManage && open && <section aria-label="Confluence connection editor"><fieldset className="capture-editor" disabled={busy}><h3>{editing ? `Edit ${editing.name}` : 'Add Confluence connection'}</h3><div className="capture-form-grid">
      <label><span>Name</span><input value={draft.name} onChange={e => setDraft({...draft,name:e.target.value})} /></label>
      <label><span>Confluence Cloud site URL</span><input placeholder="https://example.atlassian.net" value={draft.site_url} onChange={e => setDraft({...draft,site_url:e.target.value})} /></label>
      <label><span>Atlassian account email</span><input type="email" value={draft.email} onChange={e => setDraft({...draft,email:e.target.value})} /></label>
      <label><span>Space ID</span><input value={draft.space_id} onChange={e => setDraft({...draft,space_id:e.target.value})} /></label>
      <label><span>Parent page ID</span><input value={draft.parent_page_id} onChange={e => setDraft({...draft,parent_page_id:e.target.value})} /></label>
      <label><span>API token project-secret reference {editing?.has_token && '(leave blank to keep saved reference)'}</span><input type="password" autoComplete="off" value={draft.token_secret_name || ''} onChange={e => setDraft({...draft,token_secret_name:e.target.value})} /></label>
      <label className="capture-checkbox"><span>Enable connection</span><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({...draft,enabled:e.target.checked,auto_publish:e.target.checked?draft.auto_publish:false})} /></label>
      <label className="capture-checkbox"><span>Automatically publish archived runs</span><input type="checkbox" checked={draft.auto_publish} disabled={!draft.enabled || (!publicURL && !draft.auto_publish) || automaticDestinationTaken} onChange={e => setDraft({...draft,auto_publish:e.target.checked})} /></label>
      {!publicURL && <p>Set APEXLOAD_PUBLIC_URL on the controller to enable automatic publishing and link back to full reports.</p>}
      {automaticDestinationTaken && <p>Another Confluence connection is already the automatic destination for this project.</p>}
    </div><div className="capture-actions"><button type="button" className="btn btn-primary btn-sm" disabled={!draft.name.trim() || (draft.enabled && (!draft.site_url.trim() || !draft.email.trim() || !draft.space_id.trim() || !draft.parent_page_id.trim() || (!draft.token_secret_name?.trim() && !editing?.has_token)))} onClick={() => void save()}>{busy?'Saving…':'Save connection'}</button><button type="button" className="btn btn-secondary btn-sm" onClick={reset}>Discard draft</button></div></fieldset></section>}
  </div>
}
