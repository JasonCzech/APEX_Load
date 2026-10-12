import { type ReactNode } from 'react'
import './ux.css'

/**
 * All routes contribute context and actions to the same application header.
 * A page that edits its own title (the script editor's name) passes
 * `titleEditor`; the heading stays for assistive technology.
 */
export default function PageHeader({ title, titleEditor, contextual, children }: { title: string; titleEditor?: ReactNode; contextual?: boolean; children: ReactNode }) {
  return <div className={`main-topbar${contextual ? ' contextual' : ''}`} aria-label="Dashboard controls">
    <div className="main-topbar-page"><h1 id="app-page-title" className={`topbar-page-title${titleEditor ? ' sr-only' : ''}`}>{title}</h1>{titleEditor}</div>
    {children}
  </div>
}
