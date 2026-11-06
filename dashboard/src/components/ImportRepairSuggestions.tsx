import type { ImportRepairSuggestion } from '../types/scriptImports'
import ImportIssueSeverityTag from './ImportIssueSeverityTag'

export interface ImportRepairSuggestionAction {
  label: string
  run: () => void
}

function severityOf(value: string): 'blocker' | 'warning' | 'info' {
  return value === 'blocker' || value === 'warning' ? value : 'info'
}

// An action that is an identifier (an importer's operation kind) reads as
// words; a sentence, such as a JMeter gate's resolution, is shown as written.
function isIdentifier(action: string): boolean {
  return /^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$/i.test(action.trim())
}

function actionText(action: string): string {
  const value = action.trim()
  if (!isIdentifier(value)) return value
  const words = value.replace(/_/g, ' ')
  return `${words.charAt(0).toUpperCase()}${words.slice(1)}`
}

/**
 * The importer's repair suggestions. A suggestion whose fix the hosting
 * importer can open gets a button. One marked clearable: false says it cannot
 * be cleared yet and offers none. Any other kind is shown as guidance, so a
 * suggestion this page does not know is still visible.
 */
export default function ImportRepairSuggestions({ suggestions, actionFor, scopeFor, disabled = false }: {
  suggestions?: ImportRepairSuggestion[] | null
  actionFor?: (suggestion: ImportRepairSuggestion) => ImportRepairSuggestionAction | undefined
  /** Where the suggestion applies (a script name), when the host can tell. */
  scopeFor?: (suggestion: ImportRepairSuggestion) => string | undefined
  disabled?: boolean
}) {
  const items = (suggestions || []).filter(suggestion => suggestion.title || suggestion.description)
  if (items.length === 0) return null
  return <section className="lr-warnings" aria-label="Suggested repairs">
    <h4>Suggested repairs ({items.length})</h4>
    <ul>
      {items.map((suggestion, index) => {
        const notClearable = suggestion.clearable === false
        const action = notClearable ? undefined : actionFor?.(suggestion)
        const text = suggestion.action ? actionText(suggestion.action) : ''
        const scope = scopeFor?.(suggestion)
        return <li key={suggestion.id || index}>
          <ImportIssueSeverityTag severity={severityOf(suggestion.severity)} />{' '}
          <strong>{suggestion.title || text}</strong>
          {scope && <> <small>{scope}{suggestion.target_action ? ` · first held step: ${suggestion.target_action}` : ''}</small></>}
          {suggestion.description && <p>{suggestion.description}</p>}
          {notClearable && <p><strong>Cannot be cleared yet.</strong>{text ? ` ${text}` : ''}</p>}
          {!notClearable && text && text !== suggestion.title && <p>{suggestion.action && isIdentifier(suggestion.action) ? 'Suggested action' : 'To clear it'}: {text}</p>}
          {action && <button type="button" className="btn btn-secondary btn-sm" disabled={disabled} onClick={action.run}>{action.label}</button>}
        </li>
      })}
    </ul>
  </section>
}
