import { type ReactNode } from 'react'
import './ux.css'

/** Selection actions appear only when they can operate on a selection. */
export default function BulkActions({ count, children, className = '' }: { count: number; children: ReactNode; className?: string }) {
  if (count === 0) return null
  return <section className={`ux-bulk-actions ${className}`} aria-label="Selected library items"><span role="status">{count} selected</span><div className="library-bulk-selection-actions">{children}</div></section>
}
