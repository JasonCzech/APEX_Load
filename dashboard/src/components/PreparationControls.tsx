import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { AnalysisCapabilities, Preparation, PreparationCapacityPolicy } from '../types/analysis'

export default function PreparationControls({ testId, projectId, status, canEdit = false }: { testId: string; projectId?: string; status: string; canEdit?: boolean }) {
  const [capacity, setCapacity] = useState<PreparationCapacityPolicy>({ selection: 'existing', spot: 'off' })
  const [enabled, setEnabled] = useState(false)
  const [preparation, setPreparation] = useState<Preparation>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const controller = useRef<AbortController>()
  const path = `/tests/${encodeURIComponent(testId)}/preparation`
  const preparationState = preparation?.state
  useEffect(() => {
    const abort = new AbortController()
    setEnabled(false); setPreparation(undefined); setError(''); setBusy(false); setCapacity({ selection: 'existing', spot: 'off' })
    if (projectId) void request<AnalysisCapabilities>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: abort.signal })
      .then(async caps => { if (!abort.signal.aborted) { setEnabled(caps.preparation_enabled); if (caps.preparation_enabled) { const p = await request<Preparation>(path, { signal: abort.signal }); if (!abort.signal.aborted) setPreparation(p) } } })
      .catch(() => { /* Optional project capability; ordinary Start remains available. */ })
    return () => { abort.abort(); controller.current?.abort(); controller.current = undefined }
  }, [projectId, path])
  useEffect(() => {
    if (!enabled || !preparationState || !['preparing', 'ready'].includes(preparationState)) return
    const abort = new AbortController()
    const timer = window.setInterval(() => void request<Preparation>(path, { signal: abort.signal }).then(p => { if (!abort.signal.aborted && !controller.current) setPreparation(p) }).catch(e => { if (!abort.signal.aborted) setError(String(e)) }), 1000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [enabled, path, preparationState])
  if (!enabled || !['QUEUED', 'PENDING', 'INITIALIZING'].includes(status)) return null
  const act = async (kind: 'prepare' | 'cancel' | 'start' | 'renew') => {
    const abort = new AbortController(); controller.current = abort; setBusy(true); setError('')
    try {
      if (kind === 'start' && preparation) {
        await request(`/tests/${encodeURIComponent(testId)}/start`, { method: 'POST', signal: abort.signal, body: JSON.stringify({ preparation_id: preparation.id, expected_preparation_revision: preparation.execution_revision }) })
        if (!abort.signal.aborted) setPreparation({ ...preparation, state: 'consumed' })
      } else if (kind === 'renew' && preparation) {
        const next = await request<Preparation>(path, { method: 'POST', signal: abort.signal, body: JSON.stringify({ preparation_id: preparation.id, revision: preparation.revision, ttl_seconds: 1800 }) }); if (!abort.signal.aborted) setPreparation(next)
      } else { const next = await request<Preparation>(path, { method: kind === 'cancel' ? 'DELETE' : 'POST', signal: abort.signal, ...(kind === 'prepare' ? { headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ ttl_seconds: 1800, capacity }) } : {}) }); if (!abort.signal.aborted) setPreparation(next) }
    } catch (e) { if (!abort.signal.aborted) setError(String(e)) }
    finally { if (controller.current === abort) controller.current = undefined; if (!abort.signal.aborted) setBusy(false) }
  }
  return <section className="tx-section" aria-label="Run preparation">
    <h3>Prepare run</h3>
    <p>Stage assets and reserve capacity before sending target traffic. Readiness expires after 30 minutes.</p>
    {preparation && preparation.state !== 'none' && <p role="status">{preparation.state}{preparation.expires_at ? ` · Expires ${new Date(preparation.expires_at).toLocaleString()}` : ''}</p>}
    {canEdit && ['QUEUED', 'PENDING'].includes(status) && !['preparing', 'ready'].includes(preparation?.state ?? '') && <fieldset disabled={busy}>
      <legend>Preparation capacity</legend>
      <label>Capacity<select aria-label="Capacity" value={capacity.selection} onChange={e => setCapacity({ selection: e.target.value as 'existing' | 'ephemeral', spot: 'off' })}><option value="existing">Reserve existing generators</option><option value="ephemeral">Provision ephemeral generators</option></select></label>
      {capacity.selection === 'ephemeral' && <>
        <label>Spot policy<select aria-label="Spot policy" value={capacity.spot} onChange={e => setCapacity({ ...capacity, spot: e.target.value as 'off' | 'preferred' | 'required', spot_ceiling_percent: e.target.value === 'off' ? 0 : 50 })}><option value="off">Off</option><option value="preferred">Preferred when supported</option><option value="required">Required</option></select></label>
        <label>On-demand generator floor<input type="number" min={0} max={10000} value={capacity.on_demand_floor ?? 0} onChange={e => setCapacity({ ...capacity, on_demand_floor: Number(e.target.value) })} /></label>
        {capacity.spot !== 'off' && <label>Maximum spot percentage<input type="number" min={0} max={100} value={capacity.spot_ceiling_percent ?? 0} onChange={e => setCapacity({ ...capacity, spot_ceiling_percent: Number(e.target.value) })} /></label>}
        <p>Requires a supported capacity pool. Required policies fail when unsupported; no region or cloud fallback.</p>
      </>}
    </fieldset>}
    {error && <p role="alert">{error}</p>}
    {canEdit && ['QUEUED', 'PENDING'].includes(status) && !['preparing', 'ready', 'consumed'].includes(preparation?.state ?? '') && <button type="button" disabled={busy} onClick={() => void act('prepare')}>Prepare</button>}
    {canEdit && preparation?.state === 'ready' && <button type="button" disabled={busy} onClick={() => void act('start')}>Start prepared run</button>}
    {canEdit && preparation?.state === 'ready' && <button type="button" disabled={busy} onClick={() => void act('renew')}>Renew readiness</button>}
    {canEdit && preparation && ['preparing', 'ready'].includes(preparation.state) && <button type="button" disabled={busy} onClick={() => void act('cancel')}>Cancel preparation</button>}
  </section>
}
