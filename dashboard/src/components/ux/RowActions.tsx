import { type ReactNode } from 'react'
import './ux.css'

export default function RowActions({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <details className={`ux-row-actions ${className}`} onClick={event => event.stopPropagation()}><summary>More</summary>{children}</details>
}
