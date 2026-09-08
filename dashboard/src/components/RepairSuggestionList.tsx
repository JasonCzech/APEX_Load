import type { ReactNode } from 'react'

import { type ReplayRepairSuggestion } from '../types'

interface RepairSuggestionListProps {
  suggestions: ReplayRepairSuggestion[]
  className?: string
  itemClassName?: string
  label?: string
  labelClassName?: string
  confidenceClassName?: string
  descriptionClassName?: string
  actionsClassName?: string
  actionButtonClassName?: string
  rejected?: Record<string, boolean>
  applying?: Record<string, boolean>
  onApplySuggestion?: (suggestion: ReplayRepairSuggestion) => void
  onRejectSuggestion?: (suggestion: ReplayRepairSuggestion) => void
  renderExtraAction?: (suggestion: ReplayRepairSuggestion) => ReactNode
}

export default function RepairSuggestionList({
  suggestions,
  className,
  itemClassName,
  label,
  labelClassName,
  confidenceClassName,
  descriptionClassName,
  actionsClassName,
  actionButtonClassName = 'btn btn-sm btn-secondary',
  rejected = {},
  applying = {},
  onApplySuggestion,
  onRejectSuggestion,
  renderExtraAction,
}: RepairSuggestionListProps) {
  const visible = suggestions.filter(suggestion => !rejected[suggestion.id])
  if (visible.length === 0) return null

  return (
    <div className={className}>
      {label && <div className={labelClassName}>{label}</div>}
      {visible.map(suggestion => {
        const extraAction = renderExtraAction?.(suggestion)
        const showActions = Boolean(onApplySuggestion || onRejectSuggestion || extraAction)
        return (
          <div key={suggestion.id} className={itemClassName}>
            <strong>
              {suggestion.title}
              {typeof suggestion.confidence === 'number' && suggestion.confidence > 0 && (
                <span className={confidenceClassName}> Evidence score: {Math.round(suggestion.confidence * 100)}/100</span>
              )}
            </strong>
            {suggestion.description && <span className={descriptionClassName}>{suggestion.description}</span>}
            {showActions && (
              <div className={actionsClassName}>
                {onApplySuggestion && (
                  <button
                    type="button"
                    className={actionButtonClassName}
                    disabled={!!applying[suggestion.id]}
                    onClick={() => onApplySuggestion(suggestion)}
                  >
                    {applying[suggestion.id] ? 'Applying...' : 'Accept'}
                  </button>
                )}
                {extraAction}
                {onRejectSuggestion && (
                  <button
                    type="button"
                    className={actionButtonClassName}
                    onClick={() => onRejectSuggestion(suggestion)}
                  >
                    Reject
                  </button>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
