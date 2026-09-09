import { type ReactNode } from 'react'
import './ux.css'

/** All routes contribute context and actions to the same application header. */
export default function PageHeader({ title, contextual, children }: { title: string; contextual?: boolean; children: ReactNode }) {
  return <div className={`main-topbar${contextual ? ' contextual' : ''}`} aria-label="Dashboard controls">
    <div className="main-topbar-page"><h1 id="app-page-title" className="topbar-page-title">{title}</h1></div>
    {children}
  </div>
}
