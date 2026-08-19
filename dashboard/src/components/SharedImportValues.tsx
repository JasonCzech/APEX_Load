import { useEffect, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import {
  type SharedValueCandidate,
  type SharedValueDiff,
  type SharedValues,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { reachLabel, sharedValueNameError } from './sharedImportValueUtils'
import './SharedImportValues.css'

interface Props {
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
  scripts = [],
  scriptCount,
  collectOnly = false,
  onValuesChange,
  reviewGeneration = 0,
  disabled = false,
  onApplyingChange,
  onApplied,
}: Props) {
  const api = useApi()
  const candidates = useMemo(() => sharedValues.candidates ?? [], [sharedValues.candidates])
  const [values, setValues] = useState<Record<string, string>>(() => initialValues(candidates))
  const [selected, setSelected] = useState<Record<string, boolean>>(initialSelection)
  const [applying, setApplying] = useState(false)
  const [appliedCount, setAppliedCount] = useState(0)
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
    setApplying(false)
    onApplyingChangeRef.current?.(false)
    setAppliedCount(0)
    setError('')
  }, [candidates, reviewGeneration])

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
  const selectedIds = editable
    .filter(candidate => selected[candidate.id] && (values[candidate.id] ?? '').trim() !== '')
    .map(candidate => candidate.id)
  const nameErrors = new Map(
    editable
      .filter(candidate => selected[candidate.id])
      .map(candidate => [candidate.id, sharedValueNameError(candidate.name)] as const)
      .filter(([, message]) => message !== ''),
  )
  const canApply = selectedIds.length > 0 && nameErrors.size === 0 && !applying && !disabled
  const affectedScriptCount = scriptCount ?? scripts.length

  const toggle = (candidate: SharedValueCandidate) => {
    if (candidate.credential_shaped || applying || disabled) return
    setSelected(previous => ({ ...previous, [candidate.id]: !previous[candidate.id] }))
    setAppliedCount(0)
  }

  const setValue = (candidate: SharedValueCandidate, value: string) => {
    setValues(previous => ({ ...previous, [candidate.id]: value }))
    // Typing a value is intent to use it; requiring a separate checkbox click
    // after filling the field would be a pointless second step.
    setSelected(previous => ({ ...previous, [candidate.id]: value.trim() !== '' }))
    setAppliedCount(0)
  }

  const handleApply = async () => {
    if (!canApply) return
    setApplying(true)
    onApplyingChangeRef.current?.(true)
    setError('')
    try {
      const payload: Record<string, string> = {}
      for (const id of selectedIds) payload[id] = values[id]
      const response = await api.applyImportSharedValues({ scripts, values: payload })
      if (!mountedRef.current) return
      setAppliedCount(response.applied_count ?? selectedIds.length)
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
    <section className="shared-values" aria-labelledby="shared-values-heading">
      <div className="shared-values-header">
        <div>
          <h4 id="shared-values-heading">Collection Values</h4>
          <p>
            Values every request in this collection shares. Enter one once and it is written
            into all {affectedScriptCount} generated {affectedScriptCount === 1 ? 'script' : 'scripts'}.
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

      <ul className="shared-values-list">
        {candidates.map(candidate => {
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
              {candidate.rationale && <p className="shared-values-rationale">{candidate.rationale}</p>}
              {candidate.credential_shaped ? (
                <p className="shared-values-credential">
                  This looks like a credential. Set it once in Secret Bindings below — a script
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
      </ul>

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
              onClick={handleApply}
            >
              {applying ? 'Applying...' : `Apply to all scripts${selectedIds.length > 0 ? ` (${selectedIds.length})` : ''}`}
            </button>
            <small>Nothing changes until you apply.</small>
          </>
        )}
      </div>
    </section>
  )
}
