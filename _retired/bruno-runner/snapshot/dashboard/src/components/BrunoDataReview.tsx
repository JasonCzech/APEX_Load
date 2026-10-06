import { useState } from 'react'
import type { BrunoNativeScriptCandidate, SharedValues } from '../types/scriptImports'
import { brunoVariableCandidate } from './brunoReviewState'

interface Props {
  candidates: BrunoNativeScriptCandidate[]
  shared?: SharedValues
  values: Record<string, string>
  onChange: (values: Record<string, string>) => void
  onInspect: (id: string) => void
  onCredentials: () => void
  disabled: boolean
}

export default function BrunoDataReview({ candidates, shared, values, onChange, onInspect, onCredentials, disabled }: Props) {
  const [search, setSearch] = useState('')
  const [unresolvedOnly, setUnresolvedOnly] = useState(false)
  const variables = [...new Set(candidates.flatMap(candidate => candidate.missing_variables || []))].sort()
  const names = new Set(candidates.map(candidate => candidate.script.name))
  const optional = (shared?.candidates || []).filter(value => value.kind !== 'source_variable' && !value.credential_shaped
    && (!value.script_names?.length || value.script_names.some(name => names.has(name))))
  const update = (id: string, value: string) => {
    const next = { ...values }
    if (value.trim()) next[id] = value
    else delete next[id]
    onChange(next)
  }
  return <section className="bruno-card bruno-data-review">
    <h3>Required runtime inputs</h3>
    <p>Supply literal values at their detected locations in the selected APIs. Values are applied when drafts are saved. Conversion review gates remain separate.</p>
    <div className="bruno-review-toolbar">
      <label>Find input<input type="search" value={search} onChange={event => setSearch(event.target.value)} /></label>
      <label><input type="checkbox" checked={unresolvedOnly} onChange={event => setUnresolvedOnly(event.target.checked)} /> Unresolved only</label>
    </div>
    {variables.length === 0 && <p role="status">No runtime inputs are required for the selected APIs.</p>}
    <ul className="bruno-input-list">
      {variables.filter(variable => variable.toLowerCase().includes(search.toLowerCase())).filter(variable => {
        const item = brunoVariableCandidate(shared, variable)
        return !unresolvedOnly || !item || !values[item.id]?.trim()
      }).map(variable => {
        const item = brunoVariableCandidate(shared, variable)
        const affected = candidates.filter(candidate => candidate.missing_variables?.includes(variable))
        return <li key={variable}>
          <strong>{variable}</strong><span>{affected.length} selected API{affected.length === 1 ? '' : 's'}</span>
          {item ? <label>Literal value for {variable}<input disabled={disabled} value={values[item.id] || ''} onChange={event => update(item.id, event.target.value)} /></label>
            : <p role="note">This source expression cannot be supplied through the literal-value editor. Inspect the affected source, then replace it with a verified supported APEX parameter or action in the saved draft. Replay remains blocked until the requirement is resolved. Dynamic expressions must not be replaced with an invented static value.</p>}
          <details><summary>View affected APIs</summary>{affected.map(candidate => <button className="btn btn-secondary" type="button" key={candidate.candidate_id} onClick={() => onInspect(candidate.candidate_id)}>{candidate.script.name}</button>)}</details>
        </li>
      })}
    </ul>
    <button type="button" className="btn btn-secondary" onClick={onCredentials}>Configure credential inputs</button>
    <details className="bruno-optional-values"><summary>Optional substitutions ({optional.length})</summary>
      <p>Replace repeated values only at detected locations. Leave an input blank to preserve its imported value.</p>
      {optional.filter(item => item.name.toLowerCase().includes(search.toLowerCase())).map(item => <label key={item.id}>{item.name}<input disabled={disabled} value={values[item.id] || ''} placeholder="Preserve imported value" onChange={event => update(item.id, event.target.value)} /><small>{item.script_names?.filter(name => names.has(name)).length || candidates.length} selected scripts; detected locations only</small></label>)}
    </details>
  </section>
}
