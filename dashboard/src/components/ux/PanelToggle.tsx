import { type ButtonHTMLAttributes } from 'react'
import './ux.css'

interface Props extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'onChange'> {
  expanded: boolean
  panelName: string
  collapsedLabel?: string
  controls: string
  onToggle: () => void
  /** Which edge the panel sits on; the chevron points the way the panel will move. */
  side?: 'start' | 'end'
  /**
   * A small chevron with no text; the accessible name still says what it does.
   * An open panel's control is icon-only by default, so it takes little room;
   * a collapsed rail keeps its label so the panel can be found again.
   */
  iconOnly?: boolean
}
export default function PanelToggle({ expanded, panelName, collapsedLabel = panelName, controls, onToggle, side = 'start', iconOnly = expanded, className = '', ...props }: Props) {
  // Collapsing a start-side panel moves it toward the start, and expanding moves it back out.
  const pointsStart = side === 'start' ? expanded : !expanded
  return <button {...props} type="button" className={`ux-panel-toggle${iconOnly ? ' ux-panel-toggle-icon' : ''} ${className}`} aria-label={`${expanded ? 'Collapse' : 'Expand'} ${panelName}`} aria-expanded={expanded} aria-controls={controls} title={`${expanded ? 'Collapse' : 'Expand'} ${panelName}`} onClick={() => {
    onToggle()
    // The expanded/collapsed affordance may be rendered in a different branch.
    requestAnimationFrame(() => Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-controls]')).find(button => button.getAttribute('aria-controls') === controls && !button.closest('[hidden], [inert]') && button.getClientRects().length > 0)?.focus())
  }}><svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ transform: pointsStart ? undefined : 'rotate(180deg)' }}><path d="m10 4-4 4 4 4" /></svg>{!iconOnly && <span>{expanded ? 'Collapse' : collapsedLabel}</span>}</button>
}
