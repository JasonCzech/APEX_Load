import { useState } from 'react'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { useAuthApi } from '../hooks/api/useAuthApi'
import { Application, DashboardAuthStatus, Project, Release } from '../types'
import ProjectModal from './ProjectModal'
import './Sidebar.css'

export type NavPage = 'dashboard' | 'tests' | 'new-test' | 'scripts' | 'results' | 'monitoring' | 'settings'

interface Props {
  active: NavPage
  onNavigate: (page: NavPage) => void
  connectionState: WebSocketConnectionState
  authStatus?: DashboardAuthStatus
  projects?: Project[]
  activeProject?: Project | null
  onProjectChange?: (project: Project) => void
  onCreateProject?: (name: string) => Promise<Project>
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
  connectionState,
  authStatus,
  projects = [],
  activeProject,
  onProjectChange,
  onCreateProject,
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
  const [applicationOpen, setApplicationOpen] = useState(false)
  const [showProjectModal, setShowProjectModal] = useState(false)
  const [showApplicationModal, setShowApplicationModal] = useState(false)
  const [logoutPending, setLogoutPending] = useState(false)
  const [logoutError, setLogoutError] = useState('')
  const { logout } = useAuthApi()
  const connectionClass = connectionState === 'connected' ? 'connected' : connectionState === 'connecting' ? 'connecting' : 'disconnected'
  const connectionLabel = connectionState === 'connected' ? 'Live' : connectionState === 'connecting' ? 'Connecting' : 'Disconnected'
  const identityName = formatIdentityName(authStatus)
  const identityInitials = formatIdentityInitials(identityName)
  const identityMeta = formatIdentityMeta(authStatus)
  const identityTitle = formatIdentityTitle(authStatus, identityMeta)
  const logoutURL = authStatus?.authenticated ? authStatus.logout_url?.trim() || '' : ''
  const canPostLogout = authStatus?.authenticated && authStatus.mode === 'oidc' && Boolean(authStatus.logout_csrf_token?.trim())
  const sessionExpiryNotice = formatSessionExpiryNotice(authStatus)

  const handleNewProject = async (name: string) => {
    if (!onCreateProject) return
    await onCreateProject(name)
    setProjectOpen(false)
  }

  const handleNewApplication = async (name: string) => {
    if (!onCreateApplication) return
    await onCreateApplication(name)
    setApplicationOpen(false)
  }

  const handleLogout = async () => {
    if (!authStatus?.logout_csrf_token || !logoutURL || logoutPending) {
      return
    }
    setLogoutPending(true)
    setLogoutError('')
    try {
      const res = await logout(authStatus.logout_csrf_token)
      if (typeof window !== 'undefined') {
        window.location.assign(res.redirect_url || logoutURL)
      }
    } catch (error) {
      setLogoutError(error instanceof Error ? error.message : 'Unable to sign out.')
      setLogoutPending(false)
    }
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
          title={collapsed ? activeProject?.name || 'Project' : undefined}
        >
          <span className="project-icon">◫</span>
          {!collapsed && (
            <>
              <span className="project-name">{activeProject?.name || 'Project'}</span>
              <span className="project-chevron">⌄</span>
            </>
          )}
        </button>
        {projectOpen && !collapsed && (
          <div className="project-menu">
            <div className="project-menu-list">
              {projects.map(project => (
                <button
                  key={project.id}
                  className={`project-menu-item ${activeProject?.id === project.id ? 'active' : ''}`}
                  onClick={() => {
                    onProjectChange?.(project)
                    setProjectOpen(false)
                  }}
                >
                  {project.name}
                </button>
              ))}
            </div>
            <button className="project-menu-action" onClick={() => setShowProjectModal(true)}>+ New Project</button>
            <button
              className="project-menu-action muted"
              onClick={() => {
                if (typeof window !== 'undefined') {
                  window.localStorage.setItem('stormrunner.settings.focusSection', 'projects')
                }
                setProjectOpen(false)
                onNavigate('settings')
              }}
            >
              Manage Projects
            </button>
          </div>
        )}
      </div>

      <div className="sidebar-project">
        <button
          className="project-selector"
          onClick={() => setApplicationOpen(!applicationOpen)}
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
        {applicationOpen && !collapsed && (
          <div className="project-menu">
            <div className="project-menu-list">
              {applications.map(application => (
                <button
                  key={application.id}
                  className={`project-menu-item ${activeApplication?.id === application.id ? 'active' : ''}`}
                  onClick={() => {
                    onApplicationChange?.(application)
                    setApplicationOpen(false)
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
                      setApplicationOpen(false)
                    }}
                  >
                    ↳ {release.name}
                  </button>
                ))}
              </div>
            )}
            <button className="project-menu-action" onClick={() => setShowApplicationModal(true)}>+ New Application</button>
            <button
              className="project-menu-action muted"
              onClick={() => {
                if (typeof window !== 'undefined') {
                  window.localStorage.setItem('stormrunner.settings.focusSection', 'applications')
                }
                setApplicationOpen(false)
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
          <div className="sidebar-footer-meta">
            {authStatus?.authenticated && authStatus.user && identityName && (
              <div className="sidebar-identity" title={identityTitle}>
                <div className="sidebar-identity-avatar" aria-hidden="true">{identityInitials}</div>
                <div className="sidebar-identity-copy">
                  <span className="sidebar-identity-name">{identityName}</span>
                  {identityMeta && <span className="sidebar-identity-meta">{identityMeta}</span>}
                  {sessionExpiryNotice && <span className="sidebar-identity-session">{sessionExpiryNotice}</span>}
                  {logoutURL && (canPostLogout ? (
                    <button
                      type="button"
                      className="sidebar-identity-action sidebar-identity-action-button"
                      onClick={handleLogout}
                      disabled={logoutPending}
                    >
                      {logoutPending ? 'Signing out…' : 'Sign out'}
                    </button>
                  ) : (
                    <a className="sidebar-identity-action" href={logoutURL}>Sign out</a>
                  ))}
                  {logoutError && <span className="sidebar-identity-meta">{logoutError}</span>}
                </div>
              </div>
            )}
            <div className={`sidebar-status ${connectionClass}`}>
              <span className="status-dot" />
              <span className="status-text">{connectionLabel}</span>
            </div>
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
          title="New Project"
          onClose={() => setShowProjectModal(false)}
          onSubmit={handleNewProject}
        />
      )}
      {showApplicationModal && (
        <ProjectModal
          title="New Application"
          onClose={() => setShowApplicationModal(false)}
          onSubmit={handleNewApplication}
        />
      )}
    </aside>
  )
}

const ROLE_LABELS: Record<string, string> = {
  system_admin: 'System Admin',
  project_admin: 'Project Admin',
  editor: 'Editor',
  viewer: 'Viewer',
}

function formatIdentityName(authStatus?: DashboardAuthStatus): string {
  const user = authStatus?.user
  return user?.display_name || user?.subject || 'User'
}

function formatIdentityInitials(identityName: string): string {
  return identityName
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() || '')
    .join('') || 'SR'
}

function formatIdentityMeta(authStatus?: DashboardAuthStatus): string {
  const user = authStatus?.user
  if (!user) {
    return ''
  }

  if (user.global_roles && user.global_roles.length > 0) {
    return user.global_roles.slice(0, 2).map((role) => ROLE_LABELS[role] || role).join(' · ')
  }

  if (user.project_roles) {
    const projectRoleCount = Object.values(user.project_roles).reduce((count, roles) => count + roles.length, 0)
    if (projectRoleCount > 0) {
      return `${projectRoleCount} project role${projectRoleCount === 1 ? '' : 's'}`
    }
  }

  return user.email || user.subject
}

function formatIdentityTitle(authStatus?: DashboardAuthStatus, identityMeta?: string): string {
  const user = authStatus?.user
  if (!user) {
    return ''
  }

  const parts = [user.display_name || user.subject]
  if (user.email) {
    parts.push(user.email)
  }
  if (identityMeta && identityMeta !== user.email && identityMeta !== user.subject) {
    parts.push(identityMeta)
  }
  return parts.join(' • ')
}

function formatSessionExpiryNotice(authStatus?: DashboardAuthStatus): string {
  if (!authStatus?.authenticated || authStatus.mode !== 'oidc') {
    return ''
  }
  const ttl = authStatus.session_ttl_seconds
  if (typeof ttl !== 'number' || Number.isNaN(ttl) || ttl <= 0 || ttl > 15 * 60) {
    return ''
  }
  const minutes = Math.max(1, Math.ceil(ttl / 60))
  return `Session expires in ${minutes} min`
}
