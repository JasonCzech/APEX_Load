import { useEffect, useState } from 'react'
import type { SharedDataPool } from '../types/dataPools'
import type { BrunoNativeScriptCandidate, SharedValues } from '../types/scriptImports'
import { brunoInputBindingComplete, brunoVariableCandidate, type BrunoInputBinding } from './brunoReviewState'
import { aliasForPool, dataPoolColumnNames } from './scenarioDataBindingModel'

interface PreviousInput {
  mode: string
  value?: string
  alias?: string
  column?: string
  generator?: string
  digits?: number
}

interface Props {
  candidates: BrunoNativeScriptCandidate[]
  shared?: SharedValues
  values: Record<string, string>
  onChange: (values: Record<string, string>) => void
  /** Inputs read from a data pool or generated, by shared-value candidate ID. */
  bindings?: Record<string, BrunoInputBinding>
  onBindingsChange?: (bindings: Record<string, BrunoInputBinding>) => void
  /** How the scripts a re-import updates supplied each input. */
  previous?: Record<string, PreviousInput>
  /** The project's data pools, offered to fill an alias and column. */
  loadDataPools?: () => Promise<SharedDataPool[]>
  onInspect: (id: string) => void
  onCredentials: () => void
  disabled: boolean
}

type SupplyMode = 'value' | 'data' | 'generated'

const DEFAULT_DIGITS = 10

export default function BrunoDataReview({ candidates, shared, values, onChange, bindings = {}, onBindingsChange, previous = {}, loadDataPools, onInspect, onCredentials, disabled }: Props) {
  const [search, setSearch] = useState('')
  const [unresolvedOnly, setUnresolvedOnly] = useState(false)
  const [pools, setPools] = useState<SharedDataPool[]>([])
  const variables = [...new Set(candidates.flatMap(candidate => candidate.missing_variables || []))].sort()
  const needsInputs = variables.length > 0
  // The pool list is optional: without it, an alias and column are typed.
  useEffect(() => {
    if (!loadDataPools || !needsInputs || !onBindingsChange) return
    let current = true
    loadDataPools().then(loaded => {
      if (current) setPools(loaded.filter(pool => !pool.archived_at))
    }).catch(() => undefined)
    return () => { current = false }
  }, [loadDataPools, needsInputs, onBindingsChange])
  const names = new Set(candidates.map(candidate => candidate.script.name))
  const optional = (shared?.candidates || []).filter(value => value.kind !== 'source_variable' && !value.credential_shaped
    && (!value.script_names?.length || value.script_names.some(name => names.has(name))))
  const update = (id: string, value: string) => {
    const next = { ...values }
    if (value.trim()) next[id] = value
    else delete next[id]
    onChange(next)
  }
  const setBinding = (id: string, binding: BrunoInputBinding | undefined) => {
    const next = { ...bindings }
    if (binding) next[id] = binding
    else delete next[id]
    onBindingsChange?.(next)
  }
  const setMode = (id: string, mode: SupplyMode) => {
    if (mode === 'value') {
      setBinding(id, undefined)
      return
    }
    // A binding replaces the value for every user.
    if (values[id]) update(id, '')
    setBinding(id, mode === 'data' ? { mode: 'data', alias: '', column: '' } : { mode: 'generated', generator: 'uuid' })
  }
  const supplied = (id: string) => Boolean(values[id]?.trim()) || brunoInputBindingComplete(bindings[id])
  return <section className="bruno-card bruno-data-review">
    <h3>Required runtime inputs</h3>
    <p>Supply each input the collection leaves unset: one value for every virtual user, a column of a data pool so each virtual user reads its own row, or a value generated each iteration. Inputs are applied when drafts are saved. Conversion review gates remain separate.</p>
    <div className="bruno-review-toolbar">
      <label>Find input<input type="search" value={search} onChange={event => setSearch(event.target.value)} /></label>
      <label><input type="checkbox" checked={unresolvedOnly} onChange={event => setUnresolvedOnly(event.target.checked)} /> Unresolved only</label>
    </div>
    {variables.length === 0 && <p role="status">No runtime inputs are required for the selected APIs.</p>}
    <ul className="bruno-input-list">
      {variables.filter(variable => variable.toLowerCase().includes(search.toLowerCase())).filter(variable => {
        const item = brunoVariableCandidate(shared, variable)
        return !unresolvedOnly || !item || !supplied(item.id)
      }).map(variable => {
        const item = brunoVariableCandidate(shared, variable)
        const affected = candidates.filter(candidate => candidate.missing_variables?.includes(variable))
        return <li key={variable}>
          <strong>{variable}</strong><span>{affected.length} selected API{affected.length === 1 ? '' : 's'}</span>
          {item ? <BrunoInputSupply variable={variable} value={values[item.id] || ''} binding={bindings[item.id]} previous={previous[item.id]}
            pools={pools} canBind={Boolean(onBindingsChange)} disabled={disabled}
            onValue={value => update(item.id, value)} onMode={mode => setMode(item.id, mode)} onBinding={binding => setBinding(item.id, binding)} />
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

function BrunoInputSupply({ variable, value, binding, previous, pools, canBind, disabled, onValue, onMode, onBinding }: {
  variable: string
  value: string
  binding?: BrunoInputBinding
  previous?: PreviousInput
  pools: SharedDataPool[]
  canBind: boolean
  disabled: boolean
  onValue: (value: string) => void
  onMode: (mode: SupplyMode) => void
  onBinding: (binding: BrunoInputBinding) => void
}) {
  const mode: SupplyMode = binding?.mode ?? 'value'
  const pool = binding?.mode === 'data' ? pools.find(item => aliasForPool(item.name, []) === binding.alias) : undefined
  const fromLastImport = previous && (previous.mode === 'value'
    ? previous.value === value && mode === 'value'
    : previous.mode === mode && previous.alias === binding?.alias && previous.column === binding?.column && previous.generator === binding?.generator
      && (previous.generator !== 'digits' || previous.digits === binding?.digits))
  return <div className="bruno-input-supply">
    {canBind && <label>Supply {variable}
      <select disabled={disabled} value={mode} onChange={event => onMode(event.target.value as SupplyMode)}>
        <option value="value">One value for every user</option>
        <option value="data">A different value per user, from a data pool</option>
        <option value="generated">A value generated each iteration</option>
      </select>
    </label>}
    {mode === 'value' && <label>Value for {variable}<input disabled={disabled} value={value} onChange={event => onValue(event.target.value)} /></label>}
    {mode === 'data' && binding && <>
      <label>Data pool for {variable}
        <select disabled={disabled} value={pool?.id || ''} onChange={event => {
          const chosen = pools.find(item => item.id === event.target.value)
          onBinding(chosen
            ? { mode: 'data', alias: aliasForPool(chosen.name, []), column: dataPoolColumnNames(chosen)[0] || '' }
            : { mode: 'data', alias: '', column: '' })
        }}>
          <option value="">{pools.length ? 'Name the alias and column myself' : 'No data pools in this project'}</option>
          {pools.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
        </select>
      </label>
      {pool
        ? <label>Column for {variable}
          <select disabled={disabled} value={binding.column || ''} onChange={event => onBinding({ ...binding, column: event.target.value })}>
            {dataPoolColumnNames(pool).map(column => <option key={column} value={column}>{column}</option>)}
          </select>
        </label>
        : <>
          <label>Alias for {variable}<input disabled={disabled} value={binding.alias || ''} onChange={event => onBinding({ ...binding, alias: event.target.value.trim() })} /></label>
          <label>Column for {variable}<input disabled={disabled} value={binding.column || ''} onChange={event => onBinding({ ...binding, column: event.target.value.trim() })} /></label>
        </>}
      <small>Reads <code>{`{{data.${binding.alias || 'alias'}.${binding.column || 'column'}}}`}</code>. Bind the alias to a pool in the scenario&apos;s Data step.</small>
    </>}
    {mode === 'generated' && binding && <>
      <label>Generate for {variable}
        <select disabled={disabled} value={binding.generator || 'uuid'} onChange={event => {
          const generator = event.target.value as NonNullable<BrunoInputBinding['generator']>
          onBinding({ mode: 'generated', generator, ...(generator === 'digits' ? { digits: binding.digits || DEFAULT_DIGITS } : {}) })
        }}>
          <option value="uuid">A new UUID</option>
          <option value="timestamp">The current time (ISO 8601)</option>
          <option value="digits">Random digits</option>
        </select>
      </label>
      {binding.generator === 'digits' && <label>Digits in {variable}<input type="number" min={1} max={64} disabled={disabled} value={binding.digits ?? DEFAULT_DIGITS}
        onChange={event => onBinding({ ...binding, digits: Number(event.target.value) })} /></label>}
    </>}
    {fromLastImport && <small className="bruno-input-previous">As the last import of these requests supplied it.</small>}
  </div>
}
