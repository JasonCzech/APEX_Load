import { scriptHasSourceOnlyReview } from './sourceReviewDomain'
import { useState, useRef, useEffect } from 'react'
import type { ScriptDefinition, SourceReviewMapping } from '../types/scripts'
import { useAuthApi } from '../hooks/api/useAuthApi'
import { decisionMatches, rebindMapping, type MappingReviewState, type ResolvedMapping } from './sourceMappingReviewState'
import { formatDateTime } from '../utils/dateTime'

export function MappingReviewDetails({ mapping, script, state, resolution, onChange, dirty }: {
  mapping: SourceReviewMapping; script?: ScriptDefinition | null; state?: MappingReviewState; resolution?: ResolvedMapping
  onChange?: (state: MappingReviewState) => void; dirty?: boolean
}) {
  const latest = useRef({ script, state, mapping })
  latest.current = { script, state, mapping }
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const { getAuthStatus } = useAuthApi()
  const decision = state?.decisions[mapping.id]
  const [note, setNote] = useState(decision?.note || '')
  const [outcome, setOutcome] = useState<'acknowledged' | 'needs_changes'>(decision?.outcome || 'acknowledged')
  const [path, setPath] = useState(resolution?.path || mapping.target_path || '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const current = state && decisionMatches(decision, state, resolution)
  const record = async (rebind: boolean) => {
    if (!script || !state || !onChange || scriptHasSourceOnlyReview(script) || state.sourceOnly) return
    setBusy(true); setError('')
    try {
      const auth = await getAuthStatus()
      if (auth.enabled && !auth.authenticated) throw new Error('Sign in before recording a review decision.')
      const reviewer = auth.user?.subject || (auth.enabled ? '' : 'Development administrator')
      if (!reviewer) throw new Error('The current reviewer identity is unavailable. Try again after signing in.')
      if (!mounted.current || latest.current.script !== script || latest.current.state !== state || latest.current.mapping !== mapping) return
      if (rebind) {
        const next = await rebindMapping(script, mapping, state, path.trim())
        if (!mounted.current || latest.current.script !== script || latest.current.state !== state) return
        onChange({ ...next, bindings: { ...next.bindings, [mapping.id]: { ...next.bindings[mapping.id], linkedBy: reviewer, linkedAt: new Date().toISOString() } } })
      } else {
        if (!resolution?.targetDigest) throw new Error('Resolve the destination before recording a review decision.')
        onChange({ ...state, decisions: { ...state.decisions, [mapping.id]: { outcome, reviewer, reviewedAt: new Date().toISOString(), importRevision: state.importRevision, targetDigest: resolution.targetDigest, note: note.trim() } } })
      }
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Unable to record the review decision.') }
    finally { setBusy(false) }
  }
  if (scriptHasSourceOnlyReview(script) || state?.sourceOnly) return null
  return <details className="mapping-review-details">
    <summary>Review &amp; mapping details · {decision ? current ? decision.outcome === 'acknowledged' ? 'Acknowledged' : 'Needs changes' : 'Decision needs renewed review' : 'Not reviewed'}</summary>
    <p>Review annotations are saved with the script. They do not change conversion status, runtime support, or execution approval.</p>
    {decision && <p>{decision.reviewer} · {formatDateTime(decision.reviewedAt, { seconds: true })} · {dirty ? 'Draft has unsaved changes' : 'Saved with script'}{!current && ' · Destination or import revision changed'}</p>}
    {decision?.note && <blockquote>{decision.note}</blockquote>}
    {state?.bindings[mapping.id]?.linkedBy && <p>Manually linked by {state.bindings[mapping.id].linkedBy} · {state.bindings[mapping.id].linkedAt}</p>}
    {onChange ? <fieldset disabled={busy || !state}>
      <label>Review decision<select value={outcome} onChange={event => setOutcome(event.target.value as typeof outcome)}><option value="acknowledged">Acknowledged</option><option value="needs_changes">Needs changes</option></select></label>
      <label>Review note<textarea value={note} maxLength={4000} onChange={event => setNote(event.target.value)} /></label>
      <button type="button" className="btn btn-sm btn-primary" disabled={!resolution?.targetDigest} onClick={() => void record(false)}>Record review decision</button>
      <label>Destination path<input value={path} onChange={event => setPath(event.target.value)} placeholder="actions[0].target" /></label>
      <p>Explicitly re-link this source mapping after inspecting the current destination. Re-linking clears its previous decision.</p>
      <button type="button" className="btn btn-sm btn-secondary" disabled={!path.trim()} onClick={() => void record(true)}>Re-link to this destination</button>
    </fieldset> : <p>Save the imported script and open it in Author to record review decisions.</p>}
    {error && <p role="alert">{error}</p>}
  </details>
}
