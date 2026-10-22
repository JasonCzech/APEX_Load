import './RunPolicySelector.css'
import { useEffect, useId, useState } from 'react'
import { analysisPoliciesApi, type AnalysisPolicyRecord } from '../../hooks/api/analysisPoliciesApi'
import LoadingState from '../ux/LoadingState'
import EmptyState from '../ux/EmptyState'
import { runPolicySelectionProblem, type RunAnalysisPolicySelections } from './runPolicySelection'

const kinds = [{ key: 'browser', kind: 'browser_policy', label: 'Browser policy' }, { key: 'journey', kind: 'journey_policy', label: 'Journey policy' }] as const

export default function RunPolicySelector({ projectId, value, onChange, disabled = false }: {
  projectId: string
  value: RunAnalysisPolicySelections | undefined
  onChange: (value: RunAnalysisPolicySelections) => void
  disabled?: boolean
}) {
  const id = useId()
  const [open, setOpen] = useState(false)
  const [retry, setRetry] = useState(0)
  const [state, setState] = useState<{ project: string; records?: AnalysisPolicyRecord[]; error?: string }>({ project: '' })
  useEffect(() => {
    if (!open || !projectId) return
    const abort = new AbortController()
    setState({ project: projectId })
    Promise.all(kinds.map(item => analysisPoliciesApi.list(projectId, item.kind, abort.signal))).then(pages => {
      if (!abort.signal.aborted) setState({ project: projectId, records: pages.flat().filter(record => !record.deleted && record.project_id === projectId) })
    }).catch(() => {
      if (!abort.signal.aborted) setState({ project: projectId, error: 'Policies could not be loaded. Retry before choosing a revision.' })
    })
    return () => abort.abort()
  }, [open, projectId, retry])
  const current = state.project === projectId ? state : undefined
  const records = current?.records
  return <details className="run-policy-selector" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>Browser and journey policies</summary>
    <div className="run-policy-selector-body">
    <p>Use project defaults at start, or choose an exact revision for this run. Later policy edits do not change a selected revision. A deleted or unavailable selection prevents the run from starting.</p>
    {!projectId ? <p>Choose a project to select policies.</p> : open && !current?.error && !records ? <LoadingState what="analysis policies" inline /> : null}
    {current?.error && <p role="alert">{current.error} <button type="button" onClick={() => setRetry(count => count + 1)}>Retry</button></p>}
    {records?.length === 0 && <EmptyState title="No analysis policies yet." hint="Create a browser or journey policy in Settings to select a revision. This run can use the project defaults." compact />}
    {runPolicySelectionProblem(value) && <p role="alert">{runPolicySelectionProblem(value)}</p>}
    {kinds.map(item => {
      const selection = value?.[item.key]
      const policies = records?.filter(record => record.kind === item.kind) ?? []
      const chosen = policies.find(record => record.id === selection?.id)
      const unavailable = selection && records && !chosen
      return <fieldset key={item.key} disabled={disabled || !records || Boolean(current?.error)}>
        <label htmlFor={`${id}-${item.key}`}>{item.label}</label>
        <select className="settings-input" id={`${id}-${item.key}`} value={selection?.id ?? ''} onChange={event => {
          const next = { ...value }
          const selected = policies.find(record => record.id === event.target.value)
          if (selected) next[item.key] = { id: selected.id, revision: selected.revision }
          else delete next[item.key]
          onChange(next)
        }}>
          <option value="">Project default at start</option>
          {selection && !chosen && <option value={selection.id}>Selected policy (unavailable)</option>}
          {policies.map(record => <option key={record.id} value={record.id}>{record.name}</option>)}
        </select>
        {selection && <label>{item.label} revision
          <input className="settings-input" type="number" min={1} max={chosen?.revision} step={1} value={Number.isFinite(selection.revision) ? selection.revision : ''} onChange={event => onChange({ ...value, [item.key]: { ...selection, revision: event.currentTarget.valueAsNumber } })} />
        </label>}
        {chosen && selection && <p>Selected revision {selection.revision}. Latest revision: {chosen.revision}.</p>}
        {unavailable && <p role="alert">The selected {item.label.toLowerCase()} is unavailable. Choose another policy or explicitly use the project default.</p>}
      </fieldset>
    })}
    </div>
  </details>
}
