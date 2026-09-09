import { type ButtonHTMLAttributes } from 'react'
import './ux.css'

interface Props extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'onChange'> {
  expanded: boolean
  panelName: string
  collapsedLabel?: string
  controls: string
  onToggle: () => void
}
export default function PanelToggle({ expanded, panelName, collapsedLabel = panelName, controls, onToggle, className = '', ...props }: Props) {
  return <button {...props} type="button" className={`ux-panel-toggle ${className}`} aria-label={`${expanded ? 'Collapse' : 'Expand'} ${panelName}`} aria-expanded={expanded} aria-controls={controls} title={`${expanded ? 'Collapse' : 'Expand'} ${panelName}`} onClick={() => {
    onToggle()
    // The expanded/collapsed affordance may be rendered in a different branch.
    requestAnimationFrame(() => Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-controls]')).find(button => button.getAttribute('aria-controls') === controls && !button.closest('[hidden], [inert]') && button.getClientRects().length > 0)?.focus())
  }}><svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ transform: expanded ? undefined : "rotate(180deg)" }}><path d="m10 4-4 4 4 4" /></svg><span>{expanded ? 'Collapse' : collapsedLabel}</span></button>
}
