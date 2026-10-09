import { type ReactNode } from 'react'
import './ux.css'

export type NoticeTone = 'info' | 'success' | 'warning' | 'error'

/**
 * The shared feedback block. Errors are announced (role="alert"), everything
 * else politely (role="status"). Raw server or parser text goes in `details`,
 * behind a disclosure, so the visible message stays in plain language.
 */
export default function Notice({ tone = 'info', title, children, details, action, className = '' }: {
  tone?: NoticeTone
  title?: ReactNode
  children?: ReactNode
  details?: string
  action?: ReactNode
  className?: string
}) {
  return <div className={`ux-notice ux-notice-${tone} ${className}`.trim()} role={tone === 'error' ? 'alert' : 'status'}>
    <div className="ux-notice-body">
      {title && <strong className="ux-notice-title">{title}</strong>}
      {children && <div className="ux-notice-message">{children}</div>}
      {details && <details className="ux-notice-details"><summary>Details</summary><pre>{details}</pre></details>}
    </div>
    {action && <div className="ux-notice-action">{action}</div>}
  </div>
}
