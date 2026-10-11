import type { TopbarAction } from '../hooks/TopbarContributionProvider'
import TooltipButton from './ux/TooltipButton'

function topbarActionClass(action: TopbarAction): string {
  const tone = action.tone || 'secondary'
  const btnClass = tone === 'primary' ? 'btn-primary' : tone === 'success' ? 'btn-success' : tone === 'danger' ? 'btn-danger' : tone === 'ghost' ? 'btn-ghost' : 'btn-secondary'
  return ['btn', 'btn-sm', 'topbar-action-button', btnClass, action.className, action.compactLabel ? 'has-compact-label' : '', action.pressed ? 'topbar-action-pressed' : '']
    .filter(Boolean)
    .join(' ')
}

/**
 * A page action as the top bar draws it. The script debugger's session bar
 * uses the same button, so its step controls look and read the same.
 */
export default function TopbarActionButton({ action }: { action: TopbarAction }) {
  if (action.hidden) return null
  return (
    <TooltipButton
      type="button"
      className={topbarActionClass(action)}
      aria-label={action.ariaLabel}
      aria-pressed={action.pressed}
      title={action.title}
      disabled={action.disabled}
      onClick={action.onClick}
    >
      {action.icon && <span className="topbar-action-icon" aria-hidden="true">{action.icon}</span>}
      <span className="topbar-action-label">{action.label}</span>
      {action.compactLabel && <span className="topbar-action-compact-label">{action.compactLabel}</span>}
    </TooltipButton>
  )
}
