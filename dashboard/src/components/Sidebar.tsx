import { useState } from 'react'
import { Application, Release } from '../types'
import ProjectModal from './ProjectModal'
import './Sidebar.css'

export type NavPage = 'dashboard' | 'tests' | 'new-test' | 'scripts' | 'results' | 'monitoring' | 'settings'

interface Props {
  active: NavPage
  onNavigate: (page: NavPage) => void
  connected: boolean
  applications?: Application[]
  releases?: Release[]
  activeApplication?: Application | null
  activeRelease?: Release | null
  onApplicationChange?: (application: Application) => void
  onReleaseChange?: (release: Release) => void
  onCreateApplication?: (name: string) => Promise<Application>
}

const NAV_ITEMS: { id: NavPage; label: string; icon: JSX.Element }[] = [
  {
    id: 'dashboard',
    label: 'Dashboard',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3" y="3" width="7" height="7" rx="1" />
        <rect x="14" y="3" width="7" height="7" rx="1" />
        <rect x="3" y="14" width="7" height="7" rx="1" />
        <rect x="14" y="14" width="7" height="7" rx="1" />
      </svg>
    ),
  },
  {
    id: 'tests',
    label: 'Scenarios',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
      </svg>
    ),
  },
  {
    id: 'new-test',
    label: 'New Test',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="10" />
        <polygon points="10 8 16 12 10 16 10 8" />
      </svg>
    ),
  },
  {
    id: 'scripts',
    label: 'Scripts',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
        <polyline points="14 2 14 8 20 8" />
        <line x1="16" y1="13" x2="8" y2="13" />
        <line x1="16" y1="17" x2="8" y2="17" />
        <polyline points="10 9 9 9 8 9" />
      </svg>
    )
  },
  {
    id: 'results',
    label: 'Results',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <line x1="18" y1="20" x2="18" y2="10" />
        <line x1="12" y1="20" x2="12" y2="4" />
        <line x1="6" y1="20" x2="6" y2="14" />
      </svg>
    ),
  },
  {
    id: 'monitoring',
    label: 'Monitoring',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
      </svg>
    ),
  },
  {
    id: 'settings',
    label: 'Settings',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="3" />
        <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
      </svg>
    ),
  },
]


export default function Sidebar({
  active,
  onNavigate,
  connected,
  applications = [],
  releases = [],
  activeApplication,
  activeRelease,
  onApplicationChange,
  onReleaseChange,
  onCreateApplication,
}: Props) {
  const [collapsed, setCollapsed] = useState(false)
  const [projectOpen, setProjectOpen] = useState(false)
  const [showProjectModal, setShowProjectModal] = useState(false)

  const handleNewProject = async (name: string) => {
    if (!onCreateApplication) return
    await onCreateApplication(name)
    setProjectOpen(false)
  }

  return (
    <aside className={`sidebar ${collapsed ? 'collapsed' : ''}`}>
      <div className="sidebar-brand" onClick={() => onNavigate('dashboard')}>
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" className="brand-icon">
          <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" fill="var(--accent)" />
        </svg>
        {!collapsed && <span className="brand-name">Stormrunner</span>}
      </div>

      <div className="sidebar-project">
        <button
          className="project-selector"
          onClick={() => setProjectOpen(!projectOpen)}
          title={collapsed ? activeApplication?.name || 'Application' : undefined}
        >
          <span className="project-icon">▣</span>
          {!collapsed && (
            <>
              <span className="project-name">{activeApplication?.name || 'Application'}{activeRelease ? ` / ${activeRelease.name}` : ''}</span>
              <span className="project-chevron">⌄</span>
            </>
          )}
        </button>
        {projectOpen && !collapsed && (
          <div className="project-menu">
            <div className="project-menu-list">
              {applications.map(application => (
                <button
                  key={application.id}
                  className={`project-menu-item ${activeApplication?.id === application.id ? 'active' : ''}`}
                  onClick={() => {
                    onApplicationChange?.(application)
                    setProjectOpen(false)
                  }}
                >
                  {application.name}
                </button>
              ))}
            </div>
            {releases.length > 0 && (
              <div className="project-menu-list release-list">
                {releases.map(release => (
                  <button
                    key={release.id}
                    className={`project-menu-item ${activeRelease?.id === release.id ? 'active' : ''}`}
                    onClick={() => {
                      onReleaseChange?.(release)
                      setProjectOpen(false)
                    }}
                  >
                    ↳ {release.name}
                  </button>
                ))}
              </div>
            )}
            <button className="project-menu-action" onClick={() => setShowProjectModal(true)}>+ New Application</button>
            <button
              className="project-menu-action muted"
              onClick={() => {
                if (typeof window !== 'undefined') {
                  window.localStorage.setItem('stormrunner.settings.focusSection', 'applications')
                }
                setProjectOpen(false)
                onNavigate('settings')
              }}
            >
              Manage Applications
            </button>
          </div>
        )}
      </div>

      <nav className="sidebar-nav">
        {NAV_ITEMS.map((item) => (
          <button
            key={item.id}
            className={`nav-item ${active === item.id ? 'active' : ''}`}
            onClick={() => onNavigate(item.id)}
            title={collapsed ? item.label : undefined}
          >
            <span className="nav-icon">{item.icon}</span>
            {!collapsed && <span className="nav-label">{item.label}</span>}
          </button>
        ))}
      </nav>

      <div className="sidebar-footer">
        {!collapsed && (
          <div className={`sidebar-status ${connected ? 'connected' : 'disconnected'}`}>
            <span className="status-dot" />
            <span className="status-text">{connected ? 'Live' : 'Disconnected'}</span>
          </div>
        )}
        <button
          className="collapse-btn"
          onClick={() => setCollapsed(!collapsed)}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            {collapsed ? (
              <polyline points="9 18 15 12 9 6" />
            ) : (
              <polyline points="15 18 9 12 15 6" />
            )}
          </svg>
        </button>
      </div>
      {showProjectModal && (
        <ProjectModal
          title="New Application"
          onClose={() => setShowProjectModal(false)}
          onSubmit={handleNewProject}
        />
      )}
    </aside>
  )
}
