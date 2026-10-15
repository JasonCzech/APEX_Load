import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import type { SharedDataPool } from '../types/dataPools'
import {
  type SharedValueCandidate,
  type SharedValueDiff,
  type SharedValues,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { brunoInputBindingComplete, type BrunoInputBinding } from './brunoReviewState'
import ImportInputSupply, { type SupplyMode } from './ImportInputSupply'
import { reachLabel, sharedValueNameError } from './sharedImportValueUtils'
import './SharedImportValues.css'

interface Props {
  guided?: boolean
  groupRequirements?: boolean
  sharedValues: SharedValues
  // The whole import, not just the script currently on screen. A collection-wide
  // value spans every generated script, so the panel must hand all of them to
  // the apply endpoint and hand all of them back.
  //
  // Empty in collect mode, where the scripts never leave the server.
  scripts?: ScriptDefinition[]
  // Number of generated scripts the values will reach. Defaults to scripts.length;
  // collect-mode callers pass it explicitly because they hold no script bodies.
  scriptCount?: number
  // Collect mode is for importers whose artifacts never leave the server (Bruno).
  // There is nothing to send to the apply endpoint: the values ride along with
  // that importer's own commit request, which applies them server-side.
  collectOnly?: boolean
  onValuesChange?: (values: Record<string, string>) => void
  reviewGeneration?: string | number
  disabled?: boolean
  onApplyingChange?: (applying: boolean) => void
  /**
   * The project's data pools, offered when an input the source left unset is
   * read per virtual user. Without it the alias and column are typed.
   */
  loadDataPools?: () => Promise<SharedDataPool[]>
  onApplied?: (
    scripts: ScriptDefinition[],
    diffs: SharedValueDiff[],
    refreshed?: SharedValues,
    importDraftExpiresAt?: (string | null)[],
  ) => void
}

const kindLabels: Record<string, string> = {
  existing_parameter: 'From source',
  source_variable: 'Needs a value',
  repeated_literal: 'Repeated',
}

function initialValues(candidates: SharedValueCandidate[]): Record<string, string> {
  const values: Record<string, string> = {}
  for (const candidate of candidates) {
    if (candidate.credential_shaped) continue
    values[candidate.id] = candidate.current_value ?? ''
  }
  return values
}

// Rows start unchecked. Applying rewrites every generated script, so it is an
// explicit operator decision rather than something that happens by default.
function initialSelection(): Record<string, boolean> {
  return {}
}

export default function SharedImportValues({
  sharedValues,
  guided = false,
  groupRequirements = false,
  scripts = [],
  scriptCount,
  collectOnly = false,
  onValuesChange,
  reviewGeneration = 0,
  disabled = false,
  onApplyingChange,
  loadDataPools,
  onApplied,
}: Props) {
  const headingId = useId()
  const api = useApi()
  const candidates = useMemo(() => (sharedValues.candidates ?? []).filter(candidate => !guided || candidate.kind !== 'source_variable'), [sharedValues.candidates, guided])
  const [values, setValues] = useState<Record<string, string>>(() => initialValues(candidates))
  const [selected, setSelected] = useState<Record<string, boolean>>(initialSelection)
  // Inputs the source left unset that each virtual user reads from a data pool
  // or generates, by candidate ID. Applied with the values, server-side.
  const [bindings, setBindings] = useState<Record<string, BrunoInputBinding>>({})
  const [pools, setPools] = useState<SharedDataPool[]>([])
  const [applying, setApplying] = useState(false)
  const [appliedCount, setAppliedCount] = useState(0)
  const [search, setSearch] = useState('')
  const [reviewing, setReviewing] = useState(false)
  const [error, setError] = useState('')

  const onApplyingChangeRef = useRef(onApplyingChange)
  onApplyingChangeRef.current = onApplyingChange
  const onValuesChangeRef = useRef(onValuesChange)
  onValuesChangeRef.current = onValuesChange
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // A new import or review generation resets the panel; a stale value carried
  // across would be applied to a tree it was never derived from.
  useEffect(() => {
    setValues(initialValues(candidates))
    setSelected(initialSelection())
    setBindings({})
    setApplying(false)
    onApplyingChangeRef.current?.(false)
    setAppliedCount(0); setReviewing(false)
    setError('')
  }, [candidates, reviewGeneration])

  // Per-user supply is offered for unset inputs on the apply path; the pool
  // list is read only when such an input is on screen.
  const bindable = !guided && !collectOnly
  const needsInputs = bindable && candidates.some(candidate => candidate.kind === 'source_variable' && !candidate.credential_shaped)
  // Without a loader the panel reads the pools of its scripts' project.
  const projectId = scripts.find(script => script.project_id)?.project_id
  const listDataPools = typeof api.listDataPools === 'function' ? api.listDataPools : undefined
  const loadPools = useCallback(async () => {
    if (loadDataPools) return loadDataPools()
    if (!projectId || !listDataPools) return []
    return (await listDataPools(projectId)).data_pools || []
  }, [loadDataPools, listDataPools, projectId])
  useEffect(() => {
    if (!needsInputs) return
    let current = true
    loadPools().then(loaded => {
      if (current) setPools(loaded.filter(pool => !pool.archived_at))
    }).catch(() => undefined)
    return () => { current = false }
  }, [loadPools, needsInputs])

  // Collect mode has no apply step, so the caller needs the current selection
  // continuously — it is carried on that importer's own commit request.
  useEffect(() => {
    if (!collectOnly) return
    const collected: Record<string, string> = {}
    for (const candidate of candidates) {
      if (candidate.credential_shaped || !selected[candidate.id]) continue
      const value = (values[candidate.id] ?? '').trim()
      if (value !== '') collected[candidate.id] = value
    }
    onValuesChangeRef.current?.(collected)
  }, [candidates, collectOnly, selected, values])

  if (candidates.length === 0) return null

  const editable = candidates.filter(candidate => !candidate.credential_shaped)
  const hasRequiredInputs = candidates.some(candidate => candidate.kind === 'source_variable')
  const selectedIds = editable
    .filter(candidate => !bindings[candidate.id] && selected[candidate.id] && (values[candidate.id] ?? '').trim() !== '')
    .map(candidate => candidate.id)
  const boundIds = editable.filter(candidate => brunoInputBindingComplete(bindings[candidate.id])).map(candidate => candidate.id)
  const nameErrors = new Map(
    editable
      .filter(candidate => selected[candidate.id])
      .map(candidate => [candidate.id, sharedValueNameError(candidate.name)] as const)
      .filter(([, message]) => message !== ''),
  )
  const canApply = selectedIds.length + boundIds.length > 0 && nameErrors.size === 0 && !applying && !disabled
  const affectedScriptCount = scriptCount ?? scripts.length

  const toggle = (candidate: SharedValueCandidate) => {
    if (candidate.credential_shaped || applying || disabled) return
    setSelected(previous => ({ ...previous, [candidate.id]: !previous[candidate.id] }))
    setAppliedCount(0); setReviewing(false)
  }

  const setValue = (candidate: SharedValueCandidate, value: string) => {
    setValues(previous => ({ ...previous, [candidate.id]: value }))
    // Typing a value is intent to use it; requiring a separate checkbox click
    // after filling the field would be a pointless second step.
    setSelected(previous => ({ ...previous, [candidate.id]: value.trim() !== '' }))
    setAppliedCount(0); setReviewing(false)
  }

  const setMode = (candidate: SharedValueCandidate, mode: SupplyMode) => {
    setBindings(previous => {
      const next = { ...previous }
      if (mode === 'value') delete next[candidate.id]
      else next[candidate.id] = mode === 'data' ? { mode: 'data', alias: '', column: '' } : { mode: 'generated', generator: 'uuid' }
      return next
    })
    // A binding replaces the value for every user.
    if (mode !== 'value') {
      setValues(previous => ({ ...previous, [candidate.id]: '' }))
      setSelected(previous => ({ ...previous, [candidate.id]: false }))
    }
    setAppliedCount(0); setReviewing(false)
  }

  const setBinding = (candidate: SharedValueCandidate, binding: BrunoInputBinding) => {
    setBindings(previous => ({ ...previous, [candidate.id]: binding }))
    setAppliedCount(0); setReviewing(false)
  }

  const handleApply = async () => {
    if (!canApply) return
    setApplying(true)
    onApplyingChangeRef.current?.(true)
    setError('')
    try {
      const payload: Record<string, string> = {}
      for (const id of selectedIds) payload[id] = values[id]
      const boundPayload = Object.fromEntries(boundIds.map(id => [id, bindings[id]]))
      const response = await api.applyImportSharedValues({
        scripts,
        values: payload,
        ...(boundIds.length > 0 ? { bindings: boundPayload } : {}),
      })
      if (!mountedRef.current) return
      setAppliedCount(response.applied_count ?? selectedIds.length + boundIds.length)
      onApplied?.(
        response.scripts ?? [],
        response.diffs ?? [],
        response.shared_values,
        response.import_draft_expires_at,
      )
    } catch (caught) {
      if (!mountedRef.current) return
      setError(getErrorMessage(caught, 'Applying collection values failed.'))
    } finally {
      if (mountedRef.current) {
        setApplying(false)
        onApplyingChangeRef.current?.(false)
      }
    }
  }

  return (
    <section className="shared-values" aria-labelledby={headingId}>
      <div className="shared-values-header">
        <div>
          <h4 id={headingId}>{guided ? 'Optional substitutions' : groupRequirements ? 'Required inputs & optional substitutions' : 'Collection Values'}</h4>
          <p>
            Choose values to replace at their detected locations across {affectedScriptCount} generated {affectedScriptCount === 1 ? 'script' : 'scripts'}. Usage counts describe each value’s scope. Source variables need a value or a supported producer before replay. Optional substitutions change only detected occurrences; review unresolved correlations in the Review tab.
          </p>
        </div>
        {appliedCount > 0 && (
          <span className="shared-values-applied-badge" role="status">
            {appliedCount} applied
          </span>
        )}
      </div>

      {error && <p className="shared-values-error" role="alert">{error}</p>}
      {(sharedValues.warnings ?? []).map(warning => (
        <p className="shared-values-warning" key={warning} role="status">{warning}</p>
      ))}

      {(guided || groupRequirements) && <div className="import-review-toolbar"><label>{guided ? 'Find optional value' : 'Find value'}<input type="search" value={search} onChange={e => setSearch(e.target.value)} /></label></div>}
      {(groupRequirements ? ['Required runtime inputs', 'Optional substitutions'] : guided ? ['Environment overrides', 'Advanced repeated values'] : ['Values']).map(group => <details key={group} open={groupRequirements ? (group === 'Required runtime inputs' ? hasRequiredInputs : !hasRequiredInputs) || !!search : !guided || group === 'Environment overrides' || !!search}><summary>{group}</summary><ul className="shared-values-list">
        {candidates.filter(candidate => (!groupRequirements || (group === 'Required runtime inputs') === (candidate.kind === 'source_variable')) && (!guided || (group === 'Environment overrides') === (candidate.targets_base_url || candidate.name === 'base_url' || candidate.name.toLowerCase() === 'origin')) && (!search || candidate.name.toLowerCase().includes(search.toLowerCase()))).map(candidate => {
          const inputId = `shared-value-${candidate.id}`
          const nameError = nameErrors.get(candidate.id)
          return (
            <li className="shared-values-item" key={candidate.id}>
              <div className="shared-values-item-head">
                <span className="shared-values-name">{candidate.name}</span>
                <span className={`shared-values-kind shared-values-kind-${candidate.kind}`}>
                  {kindLabels[candidate.kind] ?? candidate.kind}
                </span>
                <span className="shared-values-reach">{reachLabel(candidate)}</span>
              </div>
              {candidate.rationale && <p className="shared-values-rationale">{guided ? 'Only detected occurrences are eligible for this substitution.' : candidate.rationale}</p>}{guided && <details><summary>Affected scripts</summary><ul>{(candidate.script_names || []).map(name => <li key={name}>{name}</li>)}</ul><p>Exact request changes are returned after applying. The import does not expose individual request paths for these suggestions.</p></details>}
              {bindable && candidate.kind === 'source_variable' && !candidate.credential_shaped ? (
                <>
                  <ImportInputSupply
                    valueInputId={inputId}
                    variable={candidate.name}
                    value={values[candidate.id] ?? ''}
                    binding={bindings[candidate.id]}
                    pools={pools}
                    canBind
                    disabled={applying || disabled}
                    onValue={value => setValue(candidate, value)}
                    onMode={mode => setMode(candidate, mode)}
                    onBinding={binding => setBinding(candidate, binding)}
                  />
                  {nameError && <p className="shared-values-name-error">{nameError}</p>}
                </>
              ) : candidate.credential_shaped ? (
                <p className="shared-values-credential">
                  This looks like a credential. Set it in the Credentials tab — a script
                  parameter of this name is hidden from every later read, so the value would be
                  lost the next time the script is saved.
                </p>
              ) : (
                <>
                  <div className="shared-values-input">
                    <input
                      type="checkbox"
                      checked={Boolean(selected[candidate.id])}
                      disabled={applying || disabled}
                      onChange={() => toggle(candidate)}
                      aria-label={`Apply ${candidate.name} to every script`}
                    />
                    {/* The name is already the row heading, so the input is
                        labelled via aria-label rather than repeating it on
                        screen. */}
                    <input
                      id={inputId}
                      type="text"
                      value={values[candidate.id] ?? ''}
                      disabled={applying || disabled}
                      placeholder="Enter a value"
                      aria-label={`Value for ${candidate.name}`}
                      onChange={event => setValue(candidate, event.target.value)}
                    />
                  </div>
                  {nameError && <p className="shared-values-name-error">{nameError}</p>}
                </>
              )}
            </li>
          )
        })}
      </ul></details>)}
      {guided && reviewing && <section aria-label="Substitution preview"><h4>Review selected substitutions</h4><p>Only the detected occurrences will change. Credential-shaped candidates cannot be submitted here.</p><ul>{editable.filter(c => selectedIds.includes(c.id)).map(c => <li key={c.id}><strong>{c.name}</strong> · {reachLabel(c)}<p>Before: <code>{c.current_value ? '[existing value]' : '[empty]'}</code> → After: <code>[replacement supplied]</code></p></li>)}</ul></section>}
      <div className="shared-values-actions">
        {collectOnly ? (
          <small role="status">
            {selectedIds.length > 0
              ? `${selectedIds.length} value${selectedIds.length === 1 ? '' : 's'} will be written into every script when you import.`
              : 'Enter a value to apply it to every script when you import.'}
          </small>
        ) : (
          <>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              disabled={!canApply}
              onClick={() => { if (guided && !reviewing) setReviewing(true); else void handleApply() }}
            >
              {applying ? 'Applying…' : guided ? reviewing ? 'Apply reviewed substitutions' : 'Preview substitutions' : `Apply to all scripts${selectedIds.length + boundIds.length > 0 ? ` (${selectedIds.length + boundIds.length})` : ''}`}
            </button>
            <small>Nothing changes until you apply.</small>
          </>
        )}
      </div>
    </section>
  )
}
