import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { AnalysisMapping } from '../types/analysis'
import { analysisMappingDisclosureError } from '../utils/analysisIdentity'

export interface SavedAnalysisMapping { id: string; name: string; revision: number; definition: AnalysisMapping; deleted: boolean }

export default function AnalysisSavedMappings({ projectId, mapping, canEdit, onLoad }: { projectId: string; mapping: AnalysisMapping; canEdit: boolean; onLoad: (item: SavedAnalysisMapping) => void }) {
  const [items, setItems] = useState<SavedAnalysisMapping[]>([])
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const saving = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => saving.current?.abort(), [projectId])
  const path = `/analysis/mappings?project_id=${encodeURIComponent(projectId)}`
  useEffect(() => {
    const controller = new AbortController()
    void request<SavedAnalysisMapping[]>(path, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setItems(result.filter(x => !x.deleted)) }).catch(e => { if (!controller.signal.aborted) setError(String(e)) })
    return () => controller.abort()
  }, [path, revision])
  return <fieldset disabled={busy}>
    <legend>Saved mappings</legend>
    {error && <p role="alert">{error}</p>}
    <label>Apply mapping<select defaultValue="" onChange={e => { const item = items.find(x => x.id === e.target.value); if (item) onLoad(item) }}><option value="">Select a versioned mapping…</option>{items.map(item => <option key={item.id} value={item.id}>{item.name} · revision {item.revision}</option>)}</select></label>
    {canEdit && <><label>Mapping name<input value={name} onChange={e => setName(e.target.value)} maxLength={256} /></label>
      <button type="button" className="btn btn-secondary btn-sm" disabled={!name.trim() || !mapping.measures.length || !!analysisMappingDisclosureError(mapping)} onClick={() => {
        const identityError = analysisMappingDisclosureError(mapping)
        if (identityError) { setError(identityError); return }
        saving.current?.abort(); const controller = new AbortController(); saving.current = controller
        setBusy(true); setError('')
        void request<SavedAnalysisMapping>(path, { method: 'POST', signal: controller.signal, body: JSON.stringify({ name, definition: mapping }) }).then(item => { if (!controller.signal.aborted) { const returnedIdentityError = analysisMappingDisclosureError(item.definition); if (returnedIdentityError) throw new Error(returnedIdentityError); onLoad(item); setRevision(x => x + 1) } }).catch(e => { if (!controller.signal.aborted) setError(String(e)) }).finally(() => { if (!controller.signal.aborted) setBusy(false) })
      }}>Save mapping</button></>}
  </fieldset>
}
