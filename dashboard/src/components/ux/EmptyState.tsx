import type { ReactNode } from 'react'

/**
 * The one empty state: what is missing ("No <things> yet."), then what to do
 * about it. Loading uses LoadingState instead, so a slow load never reads as
 * an empty list.
 */
export default function EmptyState({ title, hint, action, compact = false }: { title: string; hint?: ReactNode; action?: ReactNode; compact?: boolean }) {
  return <div className={`pp-empty ux-empty-state${compact ? ' pp-empty-compact' : ''}`}>
    <p className="ux-empty-title">{title}</p>
    {hint && <p>{hint}</p>}
    {action}
  </div>
}
