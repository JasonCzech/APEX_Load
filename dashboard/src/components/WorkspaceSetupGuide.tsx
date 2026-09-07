import { useEffect, useRef, useState } from 'react'
import { getErrorMessage } from '../utils/errorUtils'

interface Choice { id: string; name: string }
export interface WorkspaceSetupStep {
  label: string
  description: string
  choices: Choice[]
  selectedId?: string
  canCreate: boolean
  create: (name: string) => Promise<Choice>
  select: (id: string) => void
}

export default function WorkspaceSetupGuide({ steps, storageKey }: { steps: WorkspaceSetupStep[]; storageKey?: string }) {
  const [index, setIndex] = useState(() => {
    try { return storageKey ? Math.min(steps.length, Math.max(0, Number(sessionStorage.getItem(storageKey)) || 0)) : 0 } catch { return 0 }
  })
  const rememberStep = (next: number) => { try { if (storageKey) sessionStorage.setItem(storageKey, String(next)) } catch { /* Continue in memory when storage is unavailable. */ } }
  const moveToStep = (next: number) => { rememberStep(next); setIndex(next) }
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const step = steps[index]
  const headingRef = useRef<HTMLHeadingElement>(null)
  useEffect(() => {
    if (index > 0) { headingRef.current?.focus(); headingRef.current?.scrollIntoView?.({block:'start'}) }
  }, [index])
  return <details className="run-outcome-summary workspace-setup-guide" open={index > 0}><summary>Guided workspace setup</summary>
    <p>Domain → Project → Application → Release. Choose existing items or create each item in order. Existing “Legacy” items are compatibility containers and remain available.</p>
    {!step ? <p role="status">Workspace ready. Open Scripts to create or import your first script.</p> : <>
      <h3 ref={headingRef} tabIndex={-1}>{index + 1}. {step.label}</h3><p>{step.description}</p>
      <label>Existing {step.label.toLowerCase()}<select value={step.selectedId || ''} disabled={busy} onChange={event => step.select(event.target.value)}>
        <option value="">Select {step.label.toLowerCase()}</option>
        {step.choices.map(choice => <option key={choice.id} value={choice.id}>{choice.name}</option>)}
      </select></label>
      <button className="btn btn-primary" disabled={busy || !step.selectedId} onClick={() => { moveToStep(index + 1); setName(''); setError('') }}>Use selected {step.label.toLowerCase()}</button>
      {step.canCreate ? <div>
        <label>New {step.label.toLowerCase()} name<input value={name} disabled={busy} onChange={event => setName(event.target.value)} /></label>
        <button className="btn btn-secondary" disabled={busy || !name.trim()} onClick={async () => {
          setBusy(true); setError(''); rememberStep(index + 1)
          try { await step.create(name.trim()); setName(''); moveToStep(index + 1) }
          catch (error) { rememberStep(index); setError(getErrorMessage(error, 'Unable to create workspace item. Your inputs are retained.')) }
          finally { setBusy(false) }
        }}>{busy ? 'Creating…' : `Create ${step.label.toLowerCase()} and continue`}</button>
      </div> : <p>Choose an existing item or ask an administrator to create one in this scope.</p>}
      {error && <p role="alert">{error}</p>}
    </>}
    {index === steps.length && <button className="btn btn-secondary" onClick={() => moveToStep(0)}>Set up another workspace</button>}
    {index > 0 && <button className="btn btn-secondary" disabled={busy} onClick={() => {moveToStep(index - 1); setError(''); setName('')}}>Back</button>}
  </details>
}
