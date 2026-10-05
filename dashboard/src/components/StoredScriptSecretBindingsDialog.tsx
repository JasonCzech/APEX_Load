import { useCallback, useEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import type { StoredScript, ScriptAuthoringBaseline } from '../types/storedContent'
import type { ScriptDefinition } from '../types/scripts'
import { baselineFromSavedScript, reapplyScriptDraft, scriptSaveNeedsReconciliation } from '../utils/scriptAuthoringRevision'
import { getErrorMessage } from '../utils/errorUtils'
import ScriptSecretBindingsDialog from './ScriptSecretBindingsDialog'
import ScriptRevisionConflict from './ScriptRevisionConflict'

interface Props {
  stored: StoredScript
  projectId: string
  onClose: () => void
  onConfigured: () => void | Promise<void>
  onSaved: (saved: StoredScript, changedLocations: string[]) => void | Promise<void>
}

export default function StoredScriptSecretBindingsDialog({ stored, projectId, onClose, onConfigured, onSaved }: Props) {
  const api = useApi()
  const [baseline, setBaseline] = useState<ScriptAuthoringBaseline | null>(null)
  const [draft, setDraft] = useState<ScriptDefinition | null>(null)
  const [pending, setPending] = useState<{ locations: string[]; createdName?: string } | null>(null)
  const [reason, setReason] = useState('')
  const [reviewed, setReviewed] = useState<ScriptAuthoringBaseline | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const requestRef = useRef(0)
  const scopeRef = useRef({ id: stored.id, projectId })
  if (scopeRef.current.id !== stored.id || scopeRef.current.projectId !== projectId) scopeRef.current = { id: stored.id, projectId }
  const mountedRef = useRef(true)
  const invalidateRequests = useCallback(() => { requestRef.current += 1 }, [])
  useEffect(() => { mountedRef.current = true; return () => { mountedRef.current = false; invalidateRequests() } }, [invalidateRequests])

  const load = useCallback(async (initial: boolean) => {
    const request = ++requestRef.current
    const scope = scopeRef.current
    setLoading(true)
    setReviewed(null)
    try {
      const current = await api.getScriptAuthoring(stored.id, projectId)
      if (!mountedRef.current || scopeRef.current !== scope || requestRef.current !== request) return
      if (initial) { setBaseline(current); setDraft(current.projection.script); setReason('') }
      else setReviewed(current)
    } catch (cause) {
      if (mountedRef.current && scopeRef.current === scope && requestRef.current === request) setReason(getErrorMessage(cause, 'Unable to load a protected authoring baseline.'))
    } finally {
      if (mountedRef.current && scopeRef.current === scope && requestRef.current === request) setLoading(false)
    }
  }, [api, projectId, stored.id])
  useEffect(() => {
    setBaseline(null); setDraft(null); setPending(null); setReason('')
    void load(true)
    return invalidateRequests
  }, [load, invalidateRequests])

  const save = async (next: ScriptDefinition, locations: string[], createdName?: string) => {
    if (!baseline || reason || saving) throw new Error('Review a protected current baseline before saving this rebind.')
    const request = ++requestRef.current
    const scope = scopeRef.current
    const current = () => mountedRef.current && scopeRef.current === scope && requestRef.current === request
    setDraft(next); setPending({ locations, createdName }); setSaving(true)
    try {
      const saved = await api.updateScript(stored.id, next, baseline)
      if (!current()) return
      setBaseline(baselineFromSavedScript(saved, projectId))
      setPending(null)
      await onSaved(saved, locations)
      if (current()) onClose()
    } catch (cause) {
      if (!current()) return
      if (scriptSaveNeedsReconciliation(cause)) {
        setReviewed(null)
        setReason(cause instanceof ApiError && cause.status === 412
          ? 'This script or its placement changed. The rebound draft is retained. Load the current baseline and review it before reapplying.'
          : 'This save outcome could not be verified. The rebound draft is retained; load and review the current baseline before another save.')
      }
      throw cause
    } finally { if (current()) setSaving(false) }
  }
  const reapply = () => {
    if (!draft || !reviewed || !baseline) return
    if (baseline.projection.library_placement.application_id !== reviewed.projection.library_placement.application_id) {
      setReason('Application placement changed. Reload the current script and configure credentials in its current vault; the rebound draft cannot be carried across vault ownership.')
      return
    }
    setDraft(reapplyScriptDraft(draft, reviewed, baseline.projection.script))
    setBaseline(reviewed); setReviewed(null); setReason('')
  }
  if (!draft || !baseline) return <section role="status" aria-label="Loading script secret authoring"><p>{loading ? 'Loading protected script secret bindings…' : reason}</p><button type="button" className="btn btn-secondary" disabled={loading} onClick={() => { void load(true) }}>Reload baseline</button><button type="button" className="btn btn-ghost" onClick={onClose}>Close</button></section>
  return <ScriptSecretBindingsDialog
    scriptId={stored.id} script={draft} projectId={projectId} authoringBaseline={baseline}
    applicationId={baseline.projection.library_placement.application_id || undefined}
    scriptName={draft.name} onClose={onClose} onConfigured={onConfigured}
    rebindDisabled={Boolean(reason) || loading || saving || Boolean(pending)}
    onRebound={(next, locations, outcome) => save(next, locations, outcome?.createdSecretName)}
    reconciliation={<>
      {reason && <ScriptRevisionConflict reason={reason} current={reviewed} draft={draft} loading={loading || saving}
        onLoad={() => { void load(false) }} onReapply={reapply}
        onReload={() => { if (reviewed) { setBaseline(reviewed); setDraft(reviewed.projection.script); setPending(null); setReason(''); setReviewed(null) } }} />}
      {pending?.createdName && <p role="status">{pending.createdName} was created before this save completed. Its value remains in the original vault; it has not been recreated.</p>}
      {pending && !reason && <button type="button" className="btn btn-primary" disabled={loading || saving} onClick={() => { void save(draft, pending.locations, pending.createdName).then(() => onClose()).catch(() => undefined) }}>Save reviewed rebound draft</button>}
    </>}
  />
}
