import { useEffect, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import {
  type ApplyParameterizationRequest,
  type ApplyParameterizationResponse,
  type Parameterization,
  type ParameterizationDiff,
  type ParameterizationSuggestion,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import AuraParameterLocationDetails from './AuraParameterLocationDetails'
import AuraParameterizationCoverage from './AuraParameterizationCoverage'
import { isAuraParameterLocation } from './auraParameterLocationModel'
import './ParameterizationReview.css'

interface Props {
  guided?: boolean
  parameterization: Parameterization
  script: ScriptDefinition
  // Restricts the panel to suggestions scoped to one script of a multi-script
  // import (JMeter); omit for single-script imports.
  scriptName?: string
  reviewGeneration?: string | number
  disabled?: boolean
  onApplyingChange?: (applying: boolean) => void
  /**
   * Applies the selection to the import draft. Defaults to this controller;
   * a draft another server converted (the paired Recording Agent) must be
   * changed there.
   */
  applyParameterization?: (payload: ApplyParameterizationRequest) => Promise<ApplyParameterizationResponse>
  onApplied: (
    script: ScriptDefinition,
    diffs: ParameterizationDiff[],
    selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => void
}

const kindLabels: Record<string, string> = {
  correlation: 'Correlations',
  data_param: 'Data Parameters',
  secret: 'Secrets',
}

const kindOrder = ['correlation', 'data_param', 'secret']
const SUGGESTION_PAGE_SIZE = 50
const DIFF_PAGE_SIZE = 100
const LOCATION_PREVIEW_LIMIT = 20

function confidenceBand(confidence: number): 'high' | 'medium' | 'low' {
  if (confidence >= 0.8) return 'high'
  if (confidence >= 0.5) return 'medium'
  return 'low'
}

function initialSelection(suggestions: ParameterizationSuggestion[]): Record<string, boolean> {
  const initial: Record<string, boolean> = {}
  for (const suggestion of suggestions) {
    if (suggestion.auto_apply && !suggestion.pre_applied) initial[suggestion.id] = true
  }
  return initial
}

function isAuraSuggestion(suggestion: ParameterizationSuggestion): boolean {
  return suggestion.locations?.some(isAuraParameterLocation) ?? false
}

function suggestionDisplayValue(suggestion: ParameterizationSuggestion): string {
  if (isAuraSuggestion(suggestion)) return 'Captured value hidden'
  if (suggestion.kind === 'secret' || suggestion.high_risk) return '••••••'
  return suggestion.sample_value || suggestion.literal_value || 'Detected value'
}

function diffDisplayValue(
  diff: ParameterizationDiff,
  suggestionsById: Map<string, ParameterizationSuggestion>,
  side: 'before' | 'after',
): string | undefined {
  const value = side === 'before' ? diff.before : diff.after
  if (!value) return value
  const suggestion = diff.suggestion_id ? suggestionsById.get(diff.suggestion_id) : undefined
  if ((suggestion && isAuraSuggestion(suggestion)) || (!suggestion && [...suggestionsById.values()].some(isAuraSuggestion))) {
    if (side === 'before') return 'Captured value hidden'
    if (diff.kind === 'data_source_added') return 'Data source created'
    if (diff.kind === 'parameter_added') return 'Parameter binding created'
    if (diff.kind === 'extractor_added') return 'Correlation extractor added'
    return suggestion && isAuraSuggestion(suggestion) && diff.kind === 'value_replaced' ? 'Decoded field updated' : 'Change applied'
  }
  if (!suggestion || (suggestion.kind !== 'secret' && !suggestion.high_risk)) return value
  if (side === 'after' && value.includes('{{')) return value
  return '••••••'
}

export default function ParameterizationReview({ parameterization, script, scriptName, reviewGeneration = 0, guided = false, disabled = false, onApplyingChange, applyParameterization, onApplied }: Props) {
  const api = useApi()
  const suggestions = useMemo(
    () => (parameterization.suggestions ?? []).filter(s => !scriptName || !s.script_name || s.script_name === scriptName),
    [parameterization.suggestions, scriptName],
  )
  const [selected, setSelected] = useState<Record<string, boolean>>(() => initialSelection(suggestions))
  const [applying, setApplying] = useState(false)
  const [reviewing, setReviewing] = useState(false)
  useEffect(() => setReviewing(false), [script])
  const [applied, setApplied] = useState(false)
  const [diffs, setDiffs] = useState<ParameterizationDiff[]>([])
  const [suggestionPage, setSuggestionPage] = useState(0)
  const [diffPage, setDiffPage] = useState(0)
  const [error, setError] = useState('')
  const [blockedReason, setBlockedReason] = useState('')
  const reviewIdentity = `${String(reviewGeneration)}\u0000${scriptName || ''}`
  const activeReviewIdentityRef = useRef(reviewIdentity)
  const previousReviewIdentityRef = useRef(reviewIdentity)
  const previousParameterizationRef = useRef(parameterization)
  const selfAppliedSourceIdentityRef = useRef<string | null>(null)
  const preserveAppliedIdentityRef = useRef<string | null>(null)
  const previousParameterization = previousParameterizationRef.current
  if (previousReviewIdentityRef.current !== reviewIdentity) {
    if (
      selfAppliedSourceIdentityRef.current === previousReviewIdentityRef.current
      && previousParameterization === parameterization
    ) {
      preserveAppliedIdentityRef.current = reviewIdentity
      selfAppliedSourceIdentityRef.current = null
    }
    previousReviewIdentityRef.current = reviewIdentity
  }
  if (previousParameterization !== parameterization) {
    previousParameterizationRef.current = parameterization
    selfAppliedSourceIdentityRef.current = null
  }
  activeReviewIdentityRef.current = reviewIdentity
  const onApplyingChangeRef = useRef(onApplyingChange)
  onApplyingChangeRef.current = onApplyingChange
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // A genuinely new parameterization or review generation resets this panel.
  // Successful application is the one exception: callers advance their review
  // generation to invalidate concurrent save/credential work, so preserve the
  // applied badge and diffs across that exact same-parameterization transition.
  useEffect(() => {
    if (preserveAppliedIdentityRef.current === reviewIdentity) {
      preserveAppliedIdentityRef.current = null
      setApplying(false)
      onApplyingChangeRef.current?.(false)
      return
    }
    setReviewing(false)
    setSelected(initialSelection(suggestions))
    setApplying(false)
    onApplyingChangeRef.current?.(false)
    setApplied(false)
    setDiffs([])
    setSuggestionPage(0)
    setDiffPage(0)
    setError('')
    setBlockedReason('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [parameterization, reviewIdentity, scriptName])

  if (suggestions.length === 0 && !parameterization.aura_payload) return null

  const suggestionPageCount = Math.max(1, Math.ceil(suggestions.length / SUGGESTION_PAGE_SIZE))
  const safeSuggestionPage = Math.min(suggestionPage, suggestionPageCount - 1)
  const suggestionPageStart = safeSuggestionPage * SUGGESTION_PAGE_SIZE
  const visibleSuggestions = suggestions.slice(suggestionPageStart, suggestionPageStart + SUGGESTION_PAGE_SIZE)
  const grouped = kindOrder
    .map(kind => ({ kind, items: visibleSuggestions.filter(s => s.kind === kind) }))
    .filter(group => group.items.length > 0)

  const selectedIds = suggestions.filter(s => selected[s.id] && !s.pre_applied).map(s => s.id)
  const selectedHighRisk = suggestions.some(s => selected[s.id] && s.high_risk && !s.pre_applied)
  const suggestionsById = new Map(suggestions.map(s => [s.id, s]))

  const toggle = (suggestion: ParameterizationSuggestion) => {
    if (suggestion.pre_applied || applying || disabled) return
    setReviewing(false)
    setSelected(prev => ({ ...prev, [suggestion.id]: !prev[suggestion.id] }))
    if (applied) {
      setApplied(false)
      setDiffs([])
    }
  }

  const handleApply = async () => {
    if (selectedIds.length === 0 || applying || disabled || (guided && !reviewing)) return
    const sourceIdentity = activeReviewIdentityRef.current
    const requestIsCurrent = () => mountedRef.current && activeReviewIdentityRef.current === sourceIdentity
    setApplying(true)
    onApplyingChangeRef.current?.(true)
    setError('')
    setBlockedReason('')
    try {
      const response = await (applyParameterization ?? api.applyImportParameterization)({
        script,
        suggestions,
        selected_ids: selectedIds,
        // Checking a high-risk (secret) row is the user's explicit opt-in; the
        // rows start unchecked, so confirmation rides along with the selection.
        confirm_high_risk: selectedHighRisk,
      })
      if (!requestIsCurrent()) return
      if (response.blocked) {
        setBlockedReason(response.blocked_reason || 'Selection blocked: high-risk suggestions need explicit confirmation.')
        return
      }
      setDiffs(response.diffs ?? [])
      setDiffPage(0)
      setApplied(true)
      setReviewing(false)
      selfAppliedSourceIdentityRef.current = sourceIdentity
      window.setTimeout(() => {
        if (
          selfAppliedSourceIdentityRef.current === sourceIdentity
          && activeReviewIdentityRef.current === sourceIdentity
        ) {
          selfAppliedSourceIdentityRef.current = null
        }
      }, 0)
      onApplied(response.script, response.diffs ?? [], selectedIds, response.import_draft_expires_at)
    } catch (err: unknown) {
      if (requestIsCurrent()) setError(getErrorMessage(err, 'Failed to apply parameterization'))
    } finally {
      if (requestIsCurrent()) {
        setApplying(false)
        onApplyingChangeRef.current?.(false)
      }
    }
  }

  return (
    <div className="paramize-review">
      <div className="paramize-review-header">
        <div>
          <h4>{guided ? 'Parameter suggestions' : 'Parameterization review'}{scriptName ? ` — ${scriptName}` : ''} ({suggestions.length})</h4>
          <p>{suggestions.length > 0
            ? 'Suggestions to review before running at load. Confirm whether each value is test data, application configuration, or session data. High-confidence suggestions are pre-selected; review each row, then apply the selection.'
            : 'No selectable parameter suggestions were produced. Inspection coverage is shown below.'}</p>
        </div>
        {!guided && suggestions.length > 0 && <button className="btn btn-primary" onClick={handleApply} disabled={disabled || applying || selectedIds.length === 0}>
          {applying ? 'Applying…' : `${applied ? 'Reapply' : 'Apply'} Selected (${selectedIds.length})`}
        </button>}
        {applied && <span className="paramize-applied-badge">Applied {diffs.length} change(s)</span>}
      </div>

      {parameterization.aura_payload && <AuraParameterizationCoverage coverage={parameterization.aura_payload} />}
      {error && <div className="paramize-review-error">{error}</div>}
      {blockedReason && <div className="paramize-review-blocked">{blockedReason}</div>}

      {suggestions.length > SUGGESTION_PAGE_SIZE && (
        <nav className="paramize-pagination" aria-label="Parameterization suggestion pages">
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            disabled={disabled || applying || safeSuggestionPage === 0}
            onClick={() => setSuggestionPage(page => Math.max(0, page - 1))}
          >
            Previous suggestions
          </button>
          <span role="status" aria-live="polite">
            Showing {suggestionPageStart + 1}–{Math.min(suggestionPageStart + SUGGESTION_PAGE_SIZE, suggestions.length)} of {suggestions.length}
          </span>
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            disabled={disabled || applying || safeSuggestionPage >= suggestionPageCount - 1}
            onClick={() => setSuggestionPage(page => Math.min(suggestionPageCount - 1, page + 1))}
          >
            Next suggestions
          </button>
        </nav>
      )}

      {grouped.map(group => (
        <details key={group.kind} className={`paramize-group paramize-group-${group.kind}`} open>
          <summary>
            <span>{kindLabels[group.kind] ?? group.kind}</span>
            <small>{group.items.length}</small>
          </summary>
          <ul className="paramize-list">
            {group.items.map(suggestion => (
              <li key={suggestion.id} className={`paramize-item${suggestion.pre_applied ? ' paramize-item-preapplied' : ''}`}>
                <label className="paramize-item-main">
                  {suggestion.pre_applied
                    ? <span className="paramize-preapplied-mark" title="Applied during conversion">✓</span>
                    : <input
                        type="checkbox"
                        checked={!!selected[suggestion.id]}
                        disabled={disabled || applying}
                        onChange={() => toggle(suggestion)}
                      />}
                  <span className="paramize-item-name">{suggestion.var_name}</span>
                  <span className="paramize-item-change">
                    <code className="paramize-before">{suggestionDisplayValue(suggestion)}</code>
                    <span className="paramize-arrow" aria-hidden>→</span>
                    <code className="paramize-after">{suggestion.replacement}</code>
                  </span>
                  <span className={`paramize-confidence paramize-confidence-${confidenceBand(suggestion.confidence)}`}>
                    {suggestion.pre_applied ? 'applied' : `${confidenceBand(suggestion.confidence)} ${Math.round(suggestion.confidence * 100)}%`}
                  </span>
                  {suggestion.high_risk && <span className="paramize-high-risk">high risk</span>}
                  {suggestion.script_name && <span className="paramize-script-tag">{suggestion.script_name}</span>}
                </label>
                <p className="paramize-rationale">{isAuraSuggestion(suggestion) ? 'Replace the selected decoded field and preserve its encoding.' : suggestion.rationale}</p>
                {isAuraSuggestion(suggestion) && <div className="paramize-aura-locations">
                  <p>{suggestion.locations!.length} occurrence(s). Captured payload values are hidden.</p>
                  <ul>{suggestion.locations!.slice(0, LOCATION_PREVIEW_LIMIT).map((location, index) => <li key={index}>
                    <AuraParameterLocationDetails location={location} />
                  </li>)}</ul>
                  {suggestion.locations!.length > LOCATION_PREVIEW_LIMIT && <p>{suggestion.locations!.length - LOCATION_PREVIEW_LIMIT} more occurrences. All are included when this suggestion is applied.</p>}
                </div>}
                {!isAuraSuggestion(suggestion) && suggestion.locations && suggestion.locations.length > 0 && (
                  <p className="paramize-locations">
                    {suggestion.locations.length} occurrence(s): {suggestion.locations.slice(0, LOCATION_PREVIEW_LIMIT).map(l => `${l.action_name || `${l.section}[${l.action_index}]`} (${l.field})`).join(', ')}
                    {suggestion.locations.length > LOCATION_PREVIEW_LIMIT ? `, and ${suggestion.locations.length - LOCATION_PREVIEW_LIMIT} more` : ''}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </details>
      ))}

      {guided && reviewing && <section className="import-review-suggestion-preview" aria-label="Parameter suggestion preview">
        <h4>Review selected parameter changes</h4>
        <p>{selectedIds.length} selected {selectedIds.length === 1 ? 'suggestion' : 'suggestions'}. These are proposed replacements; the server validates the changes when applied.</p>
        <ul>{suggestions.filter(item => selectedIds.includes(item.id)).slice(0, SUGGESTION_PAGE_SIZE).map(item => <li key={item.id}><strong>{item.var_name}</strong>: <code>{suggestionDisplayValue(item)}</code> → <code>{item.replacement}</code>{item.high_risk && ' · High risk: explicitly selected'}{isAuraSuggestion(item) && item.locations?.slice(0, LOCATION_PREVIEW_LIMIT).map((location, index) => <AuraParameterLocationDetails key={index} location={location} />)}</li>)}</ul>
        {selectedIds.length > SUGGESTION_PAGE_SIZE && <p>Showing the first {SUGGESTION_PAGE_SIZE}. Use the suggestion pages above to inspect all {selectedIds.length} selected changes.</p>}
      </section>}
      {guided && suggestions.length > 0 && <div className="import-review-suggestion-actions">
        <button type="button" className="btn btn-secondary btn-sm" disabled={disabled || applying || selectedIds.length === 0} onClick={() => { if (reviewing) void handleApply(); else setReviewing(true) }}>{applying ? 'Applying…' : reviewing ? 'Apply reviewed parameter changes' : 'Preview parameter changes'}</button>
        {reviewing && <button type="button" className="btn btn-secondary btn-sm" disabled={applying} onClick={() => setReviewing(false)}>Back to selection</button>}
        <small>Nothing changes until you apply.</small>
      </div>}

      {applied && diffs.length > 0 && (
        <details className="paramize-diffs" open>
          <summary><span>Applied Changes</span><small>{diffs.length}</small></summary>
          {diffs.length > DIFF_PAGE_SIZE && (
            <nav className="paramize-pagination" aria-label="Applied parameterization change pages">
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={diffPage === 0}
                onClick={() => setDiffPage(page => Math.max(0, page - 1))}
              >
                Previous changes
              </button>
              <span role="status" aria-live="polite">
                Showing {diffPage * DIFF_PAGE_SIZE + 1}–{Math.min((diffPage + 1) * DIFF_PAGE_SIZE, diffs.length)} of {diffs.length}
              </span>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={(diffPage + 1) * DIFF_PAGE_SIZE >= diffs.length}
                onClick={() => setDiffPage(page => Math.min(Math.ceil(diffs.length / DIFF_PAGE_SIZE) - 1, page + 1))}
              >
                Next changes
              </button>
            </nav>
          )}
          <ul>
            {diffs.slice(diffPage * DIFF_PAGE_SIZE, (diffPage + 1) * DIFF_PAGE_SIZE).map((diff, index) => (
              <li key={`${diff.path}-${diffPage * DIFF_PAGE_SIZE + index}`}>
                <code>{diff.path}</code> — {diff.kind.replace(/_/g, ' ')}
                {diffDisplayValue(diff, suggestionsById, 'before') && <> : <code className="paramize-before">{diffDisplayValue(diff, suggestionsById, 'before')}</code></>}
                {diffDisplayValue(diff, suggestionsById, 'after') && <> <span className="paramize-arrow" aria-hidden>→</span> <code className="paramize-after">{diffDisplayValue(diff, suggestionsById, 'after')}</code></>}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}
