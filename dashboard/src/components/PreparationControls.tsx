import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { AnalysisCapabilities, Preparation } from '../types/analysis'

export default function PreparationControls({ testId, projectId, status, canEdit = false }: { testId: string; projectId?: string; status: string; canEdit?: boolean }) {
  const [enabled, setEnabled] = useState(false)
  const [preparation, setPreparation] = useState<Preparation>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const controller = useRef<AbortController>()
  const path = `/tests/${encodeURIComponent(testId)}/preparation`
  const preparationState = preparation?.state
  useEffect(() => {
    const abort = new AbortController()
    setEnabled(false); setPreparation(undefined); setError(''); setBusy(false)
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
      } else { const next = await request<Preparation>(path, { method: kind === 'cancel' ? 'DELETE' : 'POST', signal: abort.signal, ...(kind === 'prepare' ? { headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ ttl_seconds: 1800 }) } : {}) }); if (!abort.signal.aborted) setPreparation(next) }
    } catch (e) { if (!abort.signal.aborted) setError(String(e)) }
    finally { if (controller.current === abort) controller.current = undefined; if (!abort.signal.aborted) setBusy(false) }
  }
  return <section className="tx-section" aria-label="Run preparation">
    <h3>Prepare run</h3>
    <p>Stage assets and reserve capacity before sending target traffic. Readiness expires after 30 minutes.</p>
    {preparation && preparation.state !== 'none' && <p role="status">{preparation.state}{preparation.expires_at ? ` · Expires ${new Date(preparation.expires_at).toLocaleString()}` : ''}</p>}
    {error && <p role="alert">{error}</p>}
    {canEdit && ['QUEUED', 'PENDING'].includes(status) && !['preparing', 'ready', 'consumed'].includes(preparation?.state ?? '') && <button type="button" disabled={busy} onClick={() => void act('prepare')}>Prepare</button>}
    {canEdit && preparation?.state === 'ready' && <button type="button" disabled={busy} onClick={() => void act('start')}>Start prepared run</button>}
    {canEdit && preparation?.state === 'ready' && <button type="button" disabled={busy} onClick={() => void act('renew')}>Renew readiness</button>}
    {canEdit && preparation && ['preparing', 'ready'].includes(preparation.state) && <button type="button" disabled={busy} onClick={() => void act('cancel')}>Cancel preparation</button>}
  </section>
}
