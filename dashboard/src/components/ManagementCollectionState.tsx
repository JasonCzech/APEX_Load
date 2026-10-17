import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { JsonSuccessResponseBody } from '../types/generated/openapi'

type CollectionState = JsonSuccessResponseBody<'handleManagementCollectionState', '200'>

export default function ManagementCollectionState({ projectId, sourceId, canManage }: { projectId: string; sourceId: string; canManage: boolean }) {
  const [state, setState] = useState<CollectionState>()
  const [confirmed, setConfirmed] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const operation = useRef<AbortController | undefined>(undefined)
  const path = `/projects/${encodeURIComponent(projectId)}/monitor-sources/${encodeURIComponent(sourceId)}/management-state`
  useEffect(() => {
    const abort = new AbortController()
    setState(undefined); setConfirmed(false); setError(''); setBusy(false)
    void request<CollectionState>(path, { signal: abort.signal }).then(result => { if (!abort.signal.aborted) setState(result) }).catch(cause => { if (!abort.signal.aborted) setError(String(cause)) })
    return () => { abort.abort(); operation.current?.abort() }
  }, [path, refresh])
  const release = async () => {
    if (!confirmed || !state?.poll_id) return
    const abort = new AbortController(); operation.current?.abort(); operation.current = abort
    setBusy(true); setError('')
    try {
      await request<CollectionState>(path, { method: 'POST', signal: abort.signal, body: JSON.stringify({ poll_id: state.poll_id, collector_id: state.collector_id, collector_instance_id: state.collector_instance_id, generation: state.generation, process_stopped: true }) })
      if (!abort.signal.aborted) setRefresh(value => value + 1)
    } catch (cause) { if (!abort.signal.aborted) setError(String(cause)) }
    finally { if (!abort.signal.aborted) setBusy(false) }
  }
  return <section aria-label="Management collection ownership">
    <h4>Management collection ownership</h4>
    {state && <p>{state.state}{state.generation ? ` · generation ${state.generation}` : ''}{state.collector_instance_id ? ` · collector instance ${state.collector_instance_id}` : ''}{state.deadline ? ` · poll deadline ${state.deadline}` : ''}</p>}
    {error && <p role="alert">{error}</p>}
    <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setRefresh(value => value + 1)}>Refresh collection ownership</button>
    {canManage && state?.poll_id && ['collecting', 'draining', 'draining_unverified'].includes(state.state) && <fieldset disabled={busy}>
      <legend>Release stopped collector ownership</legend>
      <p>Poll expiry does not prove that provider requests stopped. Release this ownership only after verifying that the named collector process has stopped; the attestation is audited.</p>
      <label><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />The named collector process is stopped</label>
      <button type="button" className="btn btn-danger btn-sm" disabled={!confirmed} onClick={() => void release()}>Attest process stop and release ownership</button>
    </fieldset>}
  </section>
}
