import { useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import {
  type Parameterization,
  type ParameterizationDiff,
  type ParameterizationSuggestion,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import './ParameterizationReview.css'

interface Props {
  parameterization: Parameterization
  script: ScriptDefinition
  // Restricts the panel to suggestions scoped to one script of a multi-script
  // import (JMeter); omit for single-script imports.
  scriptName?: string
  onApplied: (script: ScriptDefinition, diffs: ParameterizationDiff[]) => void
}

const kindLabels: Record<string, string> = {
  correlation: 'Correlations',
  data_param: 'Data Parameters',
  secret: 'Secrets',
}

const kindOrder = ['correlation', 'data_param', 'secret']

function confidenceBand(confidence: number): 'high' | 'medium' | 'low' {
  if (confidence >= 0.8) return 'high'
  if (confidence >= 0.5) return 'medium'
  return 'low'
}

export default function ParameterizationReview({ parameterization, script, scriptName, onApplied }: Props) {
  const api = useApi()
  const suggestions = useMemo(
    () => (parameterization.suggestions ?? []).filter(s => !scriptName || !s.script_name || s.script_name === scriptName),
    [parameterization.suggestions, scriptName],
  )
  const [selected, setSelected] = useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {}
    for (const suggestion of suggestions) {
      if (suggestion.auto_apply && !suggestion.pre_applied) initial[suggestion.id] = true
    }
    return initial
  })
  const [applying, setApplying] = useState(false)
  const [applied, setApplied] = useState(false)
  const [diffs, setDiffs] = useState<ParameterizationDiff[]>([])
  const [error, setError] = useState('')
  const [blockedReason, setBlockedReason] = useState('')

  if (suggestions.length === 0) return null

  const grouped = kindOrder
    .map(kind => ({ kind, items: suggestions.filter(s => s.kind === kind) }))
    .filter(group => group.items.length > 0)

  const selectedIds = suggestions.filter(s => selected[s.id] && !s.pre_applied).map(s => s.id)
  const selectedHighRisk = suggestions.some(s => selected[s.id] && s.high_risk && !s.pre_applied)

  const toggle = (suggestion: ParameterizationSuggestion) => {
    if (suggestion.pre_applied || applied) return
    setSelected(prev => ({ ...prev, [suggestion.id]: !prev[suggestion.id] }))
  }

  const handleApply = async () => {
    if (selectedIds.length === 0 || applying) return
    setApplying(true)
    setError('')
    setBlockedReason('')
    try {
      const response = await api.applyImportParameterization({
        script,
        suggestions,
        selected_ids: selectedIds,
        // Checking a high-risk (secret) row is the user's explicit opt-in; the
        // rows start unchecked, so confirmation rides along with the selection.
        confirm_high_risk: selectedHighRisk,
      })
      if (response.blocked) {
        setBlockedReason(response.blocked_reason || 'Selection blocked: high-risk suggestions need explicit confirmation.')
        return
      }
      setDiffs(response.diffs ?? [])
      setApplied(true)
      onApplied(response.script, response.diffs ?? [])
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Failed to apply parameterization'))
    } finally {
      setApplying(false)
    }
  }

  return (
    <div className="paramize-review">
      <div className="paramize-review-header">
        <div>
          <h4>Parameterization Review ({suggestions.length})</h4>
          <p>
            Detected values that should be parameterized before this script runs at load.
            High-confidence suggestions are pre-selected; review each row, then apply the selection.
          </p>
        </div>
        {!applied && (
          <button className="btn btn-primary" onClick={handleApply} disabled={applying || selectedIds.length === 0}>
            {applying ? 'Applying...' : `Apply Selected (${selectedIds.length})`}
          </button>
        )}
        {applied && <span className="paramize-applied-badge">Applied {diffs.length} change(s)</span>}
      </div>

      {error && <div className="paramize-review-error">{error}</div>}
      {blockedReason && <div className="paramize-review-blocked">{blockedReason}</div>}

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
                        disabled={applied}
                        onChange={() => toggle(suggestion)}
                      />}
                  <span className="paramize-item-name">{suggestion.var_name}</span>
                  <span className="paramize-item-change">
                    <code className="paramize-before">{suggestion.sample_value || suggestion.literal_value}</code>
                    <span className="paramize-arrow" aria-hidden>→</span>
                    <code className="paramize-after">{suggestion.replacement}</code>
                  </span>
                  <span className={`paramize-confidence paramize-confidence-${confidenceBand(suggestion.confidence)}`}>
                    {suggestion.pre_applied ? 'applied' : `${confidenceBand(suggestion.confidence)} ${Math.round(suggestion.confidence * 100)}%`}
                  </span>
                  {suggestion.high_risk && <span className="paramize-high-risk">high risk</span>}
                  {suggestion.script_name && <span className="paramize-script-tag">{suggestion.script_name}</span>}
                </label>
                <p className="paramize-rationale">{suggestion.rationale}</p>
                {suggestion.locations && suggestion.locations.length > 0 && (
                  <p className="paramize-locations">
                    {suggestion.locations.length} occurrence(s): {suggestion.locations.map(l => `${l.action_name || `${l.section}[${l.action_index}]`} (${l.field})`).join(', ')}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </details>
      ))}

      {applied && diffs.length > 0 && (
        <details className="paramize-diffs" open>
          <summary><span>Applied Changes</span><small>{diffs.length}</small></summary>
          <ul>
            {diffs.map((diff, index) => (
              <li key={`${diff.path}-${index}`}>
                <code>{diff.path}</code> — {diff.kind.replace(/_/g, ' ')}
                {diff.before && <> : <code className="paramize-before">{diff.before}</code></>}
                {diff.after && <> <span className="paramize-arrow" aria-hidden>→</span> <code className="paramize-after">{diff.after}</code></>}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}
