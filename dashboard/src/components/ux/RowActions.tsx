import { type ReactNode } from 'react'
import './ux.css'

// label names the row, so each "More" menu announces which row it belongs to.
export default function RowActions({ children, className = '', label }: { children: ReactNode; className?: string; label?: string }) {
  return <details className={`ux-row-actions ${className}`} onClick={event => event.stopPropagation()}><summary aria-label={label ? `More actions for ${label}` : undefined}>More</summary>{children}</details>
}
