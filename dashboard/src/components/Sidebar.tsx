import TooltipButton from './ux/TooltipButton'
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { useAuthApi } from '../hooks/api/useAuthApi'
import { resolveApiResourceUrl } from '../hooks/apiClient'
import { useDevView } from '../hooks/useDevView'
import { useNavigationGuardRunner } from '../hooks/NavigationGuardProvider'
import { Application, ApplicationRepository, DashboardAuthStatus, Domain, Project, Release } from '../types'
import BuildBadge from './BuildBadge'
import Logo from './Logo'
import ProjectModal from './ProjectModal'
import { OPEN_PROJECT_PICKER_EVENT } from '../workspacePickerEvents'
import './Sidebar.css'

export type NavPage = 'dashboard' | 'command-center' | 'repository' | 'tests' | 'new-test' | 'scripts' | 'shared-assets' | 'results' | 'activity' | 'monitoring' | 'fleet' | 'schedules' | 'docs' | 'access' | 'settings'

interface Props {
  active: NavPage
  onNavigate: (page: NavPage) => void
  connectionState: WebSocketConnectionState
  authStatus?: DashboardAuthStatus
  projects?: Project[]
  activeProject?: Project | null
  onProjectChange?: (project: Project) => void
  onCreateProject?: (name: string) => Promise<Project>
  domains?: Domain[]
  activeDomain?: Domain | null
  applicationRepository?: ApplicationRepository | null
  onDomainChange?: (domain: Domain) => void
  applications?: Application[]
  releases?: Release[]
  activeApplication?: Application | null
  activeRelease?: Release | null
  onApplicationChange?: (application: Application) => void
  onReleaseChange?: (release: Release) => void
  onCreateApplication?: (name: string) => Promise<Application>
  onCreateRelease?: (name: string) => Promise<Release | null>
  canCreateProjects?: boolean
  canCreateReleases?: boolean
  showAccess?: boolean
  onDevPreviewLogout?: () => void
}

type NavItem = { id: NavPage; label: string; icon: ReactNode; requiresAccess?: boolean }

const NAV_ITEMS: NavItem[] = [
  { id: 'activity', label: 'Activity', icon: <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M9 5H5v16h14V5h-4M9 3h6v4H9zM8 12h8M8 16h6" /></svg> },
  {
    id: 'dashboard',
    label: 'Dashboard',
    icon: (
      <svg data-nav-icon="dashboard-grid" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
        <rect x="3" y="3" width="7" height="7" rx="1" />
        <rect x="14" y="3" width="7" height="7" rx="1" />
        <rect x="3" y="14" width="7" height="7" rx="1" />
        <rect x="14" y="14" width="7" height="7" rx="1" />
      </svg>
    ),
  },
  {
    id: 'new-test',
    label: 'New run',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="10" />
        <polygon points="10 8 16 12 10 16 10 8" />
      </svg>
    ),
  },
  {
    id: 'command-center',
    label: 'Command Center',
    icon: (
      <svg data-nav-icon="command-center-signal" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
        <path d="M4.5 6.5a10 10 0 0 1 15 0" />
        <path d="M7.5 9.5a6 6 0 0 1 9 0" />
        <circle cx="12" cy="12" r="2" />
        <path d="M12 14v6" />
        <path d="M8 20h8" />
      </svg>
    ),
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
    id: 'tests',
    label: 'Scenarios',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
      </svg>
    ),
  },
  {
    id: 'shared-assets',
    label: 'Shared assets',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <polygon points="12 2 2 7 12 12 22 7 12 2" />
        <polyline points="2 17 12 22 22 17" />
        <polyline points="2 12 12 17 22 12" />
      </svg>
    ),
  },
  {
    id: 'repository',
    label: 'Repository',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M3 7h7l2 2h9v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
        <path d="M3 7V5a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v2" />
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
    id: 'docs',
    label: 'Help & documentation',
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
        <path d="M4 4.5A2.5 2.5 0 0 1 6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5z" />
        <path d="M8 7h8" />
        <path d="M8 11h8" />
        <path d="M8 15h5" />
      </svg>
    ),
  },
  {
    id: 'access',
    label: 'Access',
    requiresAccess: true,
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.78 7.78 5.5 5.5 0 0 1 7.78-7.78z" />
        <path d="M14.5 9.5L21 3" />
        <path d="M17 6l2 2" />
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

const NAV_GROUPS: { label: string; itemIds: NavPage[] }[] = [
  { label: 'Operate', itemIds: ['dashboard', 'command-center', 'results', 'monitoring', 'activity'] },
  { label: 'Build', itemIds: ['scripts', 'tests', 'shared-assets'] },
  { label: 'Administer', itemIds: ['repository', 'access', 'settings'] },
  { label: 'Support', itemIds: ['docs'] },
]

// Anything a click on the collapsed rail should leave to its own handler
// rather than treating as "empty space".
const RAIL_CONTROL_SELECTOR =
  'a, button, input, select, textarea, label, summary, [role="button"], [role="link"], [role="menuitem"], [role="option"], [role="dialog"], [tabindex]:not([tabindex="-1"])'

export default function Sidebar({
  active,
  onNavigate,
  connectionState,
  authStatus,
  projects = [],
  activeProject,
  onProjectChange,
  onCreateProject,
  domains = [],
  activeDomain,
  applicationRepository,
  onDomainChange,
  applications = [],
  releases = [],
  activeApplication,
  activeRelease,
  onApplicationChange,
  onReleaseChange,
  onCreateApplication,
  onCreateRelease,
  canCreateProjects = false,
  canCreateReleases = false,
  showAccess = false,
  onDevPreviewLogout,
}: Props) {
  // Collapsed to the icon rail by default; expanding opens the full sidebar as
  // an overlay above the page instead of pushing the content right.
  const [collapsed, setCollapsed] = useState(true)
  const asideRef = useRef<HTMLElement | null>(null)
  const [workspaceOpen, setWorkspaceOpen] = useState(false)
  const [domainOpen, setDomainOpen] = useState(false)
  const [projectOpen, setProjectOpen] = useState(false)
  const [applicationOpen, setApplicationOpen] = useState(false)
  const [releaseOpen, setReleaseOpen] = useState(false)
  const workspaceRef = useRef<HTMLDivElement | null>(null)
  const domainSelectorRef = useRef<HTMLDivElement | null>(null)
  const projectSelectorRef = useRef<HTMLDivElement | null>(null)
  const applicationSelectorRef = useRef<HTMLDivElement | null>(null)
  const releaseSelectorRef = useRef<HTMLDivElement | null>(null)
  const [showProjectModal, setShowProjectModal] = useState(false)
  const [showApplicationModal, setShowApplicationModal] = useState(false)
  const [showReleaseModal, setShowReleaseModal] = useState(false)
  const [devViewOpen, setDevViewOpen] = useState(false)
  const [logoutPending, setLogoutPending] = useState(false)
  const [logoutError, setLogoutError] = useState('')
  const { logout } = useAuthApi()
  const { runGuarded } = useNavigationGuardRunner()
  const { mode: devViewMode, setMode: setDevViewMode } = useDevView()
  const isDevDashboard = authStatus?.mode === 'dev'
  const isDevUserPreview = isDevDashboard && devViewMode === 'user'
  const visibleNavItems = NAV_ITEMS.filter(item => {
    if (item.requiresAccess && !showAccess) {
      return false
    }
    return true
  })
  const visibleNavItemsById = useMemo(
    () => new Map(visibleNavItems.map(item => [item.id, item])),
    [visibleNavItems],
  )
  const workspaceLabel = [
    activeDomain?.name || 'Domain',
    activeProject?.name || 'Project',
    activeApplication?.name || 'Application',
    activeRelease?.name || 'Release',
  ].join(' / ')
  const connectionClass = connectionState === 'connected' ? 'connected' : connectionState === 'connecting' ? 'connecting' : 'disconnected'
  const connectionLabel = connectionState === 'connected' ? 'Live' : connectionState === 'connecting' ? 'Connecting' : 'Disconnected'
  const identityName = formatIdentityName(authStatus)
  const identityInitials = formatIdentityInitials(identityName)
  const identityMeta = formatIdentityMeta(authStatus, {
    domainId: activeDomain?.id,
    applicationRepositoryId: applicationRepository?.id,
    projectId: activeProject?.id,
  })
  const identityTitle = formatIdentityTitle(authStatus, identityMeta)
  const rawLogoutURL = authStatus?.authenticated ? authStatus.logout_url?.trim() || '' : ''
  const logoutURL = rawLogoutURL ? resolveApiResourceUrl(rawLogoutURL) : ''
  const canPostLogout = authStatus?.authenticated && (authStatus.mode === 'oidc' || authStatus.mode === 'local') && Boolean(authStatus.logout_csrf_token?.trim())
  const sessionDeadline = useMemo(
    () => resolveSessionDeadline(authStatus),
    [authStatus],
  )
  const [sessionClock, setSessionClock] = useState(() => Date.now())
  useEffect(() => {
    if (sessionDeadline === undefined) return undefined

    let activationTimer: number | undefined
    let countdownTimer: number | undefined
    const updateClock = () => setSessionClock(Date.now())
    const startCountdown = () => {
      updateClock()
      countdownTimer = window.setInterval(updateClock, 30_000)
    }
    const untilNoticeWindow = sessionDeadline - Date.now() - 15 * 60 * 1000
    if (untilNoticeWindow > 0) {
      activationTimer = window.setTimeout(startCountdown, untilNoticeWindow)
    } else {
      startCountdown()
    }

    return () => {
      if (activationTimer !== undefined) window.clearTimeout(activationTimer)
      if (countdownTimer !== undefined) window.clearInterval(countdownTimer)
    }
  }, [sessionDeadline])
  const sessionExpiryNotice = formatSessionExpiryNotice(authStatus, sessionDeadline, sessionClock)
  const closeWorkspaceMenus = useCallback(() => {
    setWorkspaceOpen(false)
    setDomainOpen(false)
    setProjectOpen(false)
    setApplicationOpen(false)
    setReleaseOpen(false)
  }, [])

  useEffect(() => {
    if (!workspaceOpen && !domainOpen && !projectOpen && !applicationOpen && !releaseOpen) {
      return undefined
    }

    const isInsideOpenMenu = (target: Node) => (
      (domainOpen && Boolean(domainSelectorRef.current?.contains(target))) ||
      (projectOpen && Boolean(projectSelectorRef.current?.contains(target))) ||
      (applicationOpen && Boolean(applicationSelectorRef.current?.contains(target))) ||
      (releaseOpen && Boolean(releaseSelectorRef.current?.contains(target))) ||
      (workspaceOpen && Boolean(workspaceRef.current?.contains(target)))
    )

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (!(target instanceof Node) || isInsideOpenMenu(target)) {
        return
      }
      closeWorkspaceMenus()
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closeWorkspaceMenus()
      }
    }

    document.addEventListener('pointerdown', handlePointerDown, true)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown, true)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [applicationOpen, closeWorkspaceMenus, domainOpen, projectOpen, releaseOpen, workspaceOpen])

  const anyModalOpen = showProjectModal || showApplicationModal || showReleaseModal
  const anyMenuOpen = workspaceOpen || domainOpen || projectOpen || applicationOpen || releaseOpen

  // The expanded overlay closes on an outside click or Escape. Escape closes an
  // open workspace menu first, and a modal opened from the sidebar keeps it open.
  useEffect(() => {
    if (collapsed || anyModalOpen) {
      return undefined
    }

    const collapse = () => {
      closeWorkspaceMenus()
      setDevViewOpen(false)
      setCollapsed(true)
    }

    const swallowClick = (event: MouseEvent) => {
      if (event.target instanceof Node && asideRef.current?.contains(event.target)) return
      event.preventDefault()
      event.stopPropagation()
    }
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && asideRef.current?.contains(target)) {
        return
      }
      // The outside press only dismisses the overlay: the click that follows
      // must not also activate whatever was underneath it.
      // The listener outlives this effect (collapsing re-runs it), so its own
      // timer removes it if no click follows (a drag or scroll).
      document.addEventListener('click', swallowClick, { capture: true, once: true })
      window.setTimeout(() => document.removeEventListener('click', swallowClick, true), 700)
      collapse()
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !anyMenuOpen) {
        collapse()
      }
    }

    document.addEventListener('pointerdown', handlePointerDown, true)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown, true)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [anyMenuOpen, anyModalOpen, closeWorkspaceMenus, collapsed])

  // While collapsed, a click on any empty part of the rail (such as the space
  // below the icons) opens the sidebar. This is a pointer convenience only:
  // keyboard and screen-reader users have the Expand sidebar button.
  useEffect(() => {
    const aside = asideRef.current
    if (!collapsed || !aside) {
      return undefined
    }
    const expandFromEmptySpace = (event: MouseEvent) => {
      if (event.button !== 0 || !(event.target instanceof Element) || event.target.closest(RAIL_CONTROL_SELECTOR)) {
        return
      }
      setCollapsed(false)
    }
    aside.addEventListener('click', expandFromEmptySpace)
    return () => aside.removeEventListener('click', expandFromEmptySpace)
  }, [collapsed])

  const navigateTo = (page: NavPage) => {
    onNavigate(page)
    closeWorkspaceMenus()
    setCollapsed(true)
  }

  const toggleWorkspace = () => {
    setWorkspaceOpen(open => (collapsed ? true : !open))
    setDomainOpen(false)
    setProjectOpen(false)
    setApplicationOpen(false)
    setReleaseOpen(false)
    setCollapsed(false)
  }

  // The menus only render expanded, so a click while collapsed has to open the
  // rail as well or it silently does nothing.
  const toggleDomainMenu = () => {
    setWorkspaceOpen(true)
    setDomainOpen(open => (collapsed ? true : !open))
    setProjectOpen(false)
    setApplicationOpen(false)
    setReleaseOpen(false)
    setCollapsed(false)
  }

  const toggleProjectMenu = () => {
    setWorkspaceOpen(true)
    setDomainOpen(false)
    setProjectOpen(open => (collapsed ? true : !open))
    setApplicationOpen(false)
    setReleaseOpen(false)
    setCollapsed(false)
  }

  // Another surface (a link to a project this session cannot open) asks for
  // the picker: expand the rail, open the project menu and focus its button.
  useEffect(() => {
    const openProjectPicker = () => {
      setWorkspaceOpen(true)
      setDomainOpen(false)
      setProjectOpen(true)
      setApplicationOpen(false)
      setReleaseOpen(false)
      setCollapsed(false)
      window.setTimeout(() => projectSelectorRef.current?.querySelector<HTMLElement>('.project-selector')?.focus(), 0)
    }
    window.addEventListener(OPEN_PROJECT_PICKER_EVENT, openProjectPicker)
    return () => window.removeEventListener(OPEN_PROJECT_PICKER_EVENT, openProjectPicker)
  }, [])

  // The 'user' preview emulates an editor, so it is named for that role.
  const devViewModeLabel = devViewMode === 'user' ? 'Editor' : 'Admin'

  const toggleApplicationMenu = () => {
    setWorkspaceOpen(true)
    setDomainOpen(false)
    setProjectOpen(false)
    setApplicationOpen(open => (collapsed ? true : !open))
    setReleaseOpen(false)
    setCollapsed(false)
  }

  const toggleReleaseMenu = () => {
    setWorkspaceOpen(true)
    setDomainOpen(false)
    setProjectOpen(false)
    setApplicationOpen(false)
    setReleaseOpen(open => (collapsed ? true : !open))
    setCollapsed(false)
  }

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

  const handleNewRelease = async (name: string) => {
    if (!onCreateRelease) return
    await onCreateRelease(name)
    setReleaseOpen(false)
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
        if (authStatus.mode === 'local') {
          window.location.reload()
        } else {
          window.location.assign(res.redirect_url ? resolveApiResourceUrl(res.redirect_url) : logoutURL)
        }
      }
    } catch (error) {
      setLogoutError(error instanceof Error ? error.message : 'Unable to sign out.')
      setLogoutPending(false)
    }
  }

  const handleDevPreviewLogout = () => {
    if (onDevPreviewLogout) {
      onDevPreviewLogout()
      return
    }
    setDevViewMode('admin')
  }

  return (
    <div className={`sidebar-shell ${collapsed ? '' : 'overlay-open'}`}>
    <aside ref={asideRef} className={`sidebar ${collapsed ? 'collapsed' : ''}`}>
      <button type="button" className="sidebar-brand" onClick={() => navigateTo('dashboard')} aria-label="APEX Load dashboard">
        <Logo size={28} className="brand-icon" title="APEX Load" />
        {!collapsed && <span className="brand-name">APEX Load</span>}
      </button>

      <div className={`sidebar-workspace ${workspaceOpen ? 'open' : ''}`} ref={workspaceRef}>
        <TooltipButton
          type="button"
          className="workspace-summary"
          onClick={toggleWorkspace}
          aria-label={`Workspace context. ${workspaceLabel}`}
          title={collapsed ? workspaceLabel : undefined}
          tooltipPlacement="right"
          aria-expanded={workspaceOpen && !collapsed}
          aria-controls="sidebar-workspace-context"
        >
          <span className="workspace-summary-icon" aria-hidden="true">◈</span>
          {!collapsed && (
            <span className="workspace-summary-copy">
              <span className="workspace-summary-kicker">Workspace</span>
              <span className="workspace-summary-label">{workspaceLabel}</span>
            </span>
          )}
          {!collapsed && <span className="project-chevron" aria-hidden="true">⌄</span>}
        </TooltipButton>
        <div
          className="workspace-context"
          id="sidebar-workspace-context"
          aria-label="Workspace context selectors"
          aria-hidden={!workspaceOpen || collapsed}
        >
            <p className="workspace-context-heading">Change workspace</p>
      <div className="sidebar-project" ref={domainSelectorRef}>
        <TooltipButton
          className="project-selector"
          onClick={toggleDomainMenu}
          title={collapsed ? activeDomain?.name || 'Domain' : undefined}
          tooltipPlacement="right"
          aria-label={`Select domain. Current domain: ${activeDomain?.name || 'none'}`}
          aria-expanded={domainOpen && !collapsed}
          aria-controls="sidebar-domain-options"
        >
          <span className="project-icon">◇</span>
          {!collapsed && (
            <>
              <span className="project-name">{activeDomain?.name || 'Domain'}</span>
              <span className="project-chevron">⌄</span>
            </>
          )}
        </TooltipButton>
        {domainOpen && !collapsed && (
          <div className="project-menu" id="sidebar-domain-options" aria-label="Domain options">
            <div className="project-menu-list">
              {domains.map(domain => (
                <button
                  key={domain.id}
                  className={`project-menu-item ${activeDomain?.id === domain.id ? 'active' : ''}`}
                  aria-current={activeDomain?.id === domain.id ? 'true' : undefined}
                  onClick={() => {
                    onDomainChange?.(domain)
                    setDomainOpen(false)
                  }}
                >
                  {domain.name}
                </button>
              ))}
            </div>
          </div>
        )}
      </div>

      <div className="sidebar-project" ref={projectSelectorRef}>
        <TooltipButton
          className="project-selector"
          onClick={toggleProjectMenu}
          title={collapsed ? activeProject?.name || 'Project' : undefined}
          tooltipPlacement="right"
          aria-label={`Select project. Current project: ${activeProject?.name || 'none'}`}
          aria-expanded={projectOpen && !collapsed}
          aria-controls="sidebar-project-options"
        >
          <span className="project-icon">◫</span>
          {!collapsed && (
            <>
              <span className="project-name">{activeProject?.name || 'Project'}</span>
              <span className="project-chevron">⌄</span>
            </>
          )}
        </TooltipButton>
        {projectOpen && !collapsed && (
          <div className="project-menu" id="sidebar-project-options" aria-label="Project options">
            <div className="project-menu-list">
              {projects.length === 0 ? (
                <p className="project-menu-empty">
                  {`No projects in ${activeDomain?.name || 'this domain'}`}
                </p>
              ) : projects.map(project => (
                <button
                  key={project.id}
                  className={`project-menu-item ${activeProject?.id === project.id ? 'active' : ''}`}
                  aria-current={activeProject?.id === project.id ? 'true' : undefined}
                  onClick={() => {
                    onProjectChange?.(project)
                    setProjectOpen(false)
                  }}
                >
                  {project.name}
                </button>
              ))}
            </div>
            {canCreateProjects && !isDevUserPreview && onCreateProject && (
              <button
                className="project-menu-action"
                onClick={() => {
                  setProjectOpen(false)
                  // Creating a project switches the workspace scope on save, so
                  // resolve unsaved-changes guards before opening the form.
                  runGuarded(() => setShowProjectModal(true))
                }}
              >
                New Project
              </button>
            )}
          </div>
        )}
      </div>

      <div className="sidebar-project" ref={applicationSelectorRef}>
        <TooltipButton
          className="project-selector"
          onClick={toggleApplicationMenu}
          title={collapsed ? activeApplication?.name || 'Application' : undefined}
          tooltipPlacement="right"
          aria-label={`Select application. Current application: ${activeApplication?.name || 'none'}`}
          aria-expanded={applicationOpen && !collapsed}
          aria-controls="sidebar-application-options"
        >
          <span className="project-icon">▣</span>
          {!collapsed && (
            <>
              <span className="project-name">{activeApplication?.name || 'Application'}</span>
              <span className="project-chevron">⌄</span>
            </>
          )}
        </TooltipButton>
        {applicationOpen && !collapsed && (
          <div className="project-menu" id="sidebar-application-options" aria-label="Application options">
            <div className="project-menu-list">
              {applications.length === 0 ? (
                <p className="project-menu-empty">
                  {activeProject
                    ? `No applications in ${activeProject.name}`
                    : 'Select a project first'}
                </p>
              ) : applications.map(application => (
                <button
                  key={application.id}
                  className={`project-menu-item ${activeApplication?.id === application.id ? 'active' : ''}`}
                  aria-current={activeApplication?.id === application.id ? 'true' : undefined}
                  onClick={() => {
                    onApplicationChange?.(application)
                    setApplicationOpen(false)
                  }}
                >
                  {application.name}
                </button>
              ))}
            </div>
            {/* Not gated on canCreateProjects: application creation is a wider
                permission than project management on the server (global
                editors qualify), and the Settings create row matches. A
                denied create surfaces its error in the dialog. */}
            {activeProject && !isDevUserPreview && onCreateApplication && (
              <button
                className="project-menu-action"
                onClick={() => {
                  setApplicationOpen(false)
                  runGuarded(() => setShowApplicationModal(true))
                }}
              >
                New Application
              </button>
            )}
          </div>
        )}
      </div>

      <div className="sidebar-project" ref={releaseSelectorRef}>
        <TooltipButton
          className="project-selector"
          onClick={toggleReleaseMenu}
          title={collapsed ? activeRelease?.name || 'Release' : undefined}
          tooltipPlacement="right"
          aria-label={`Select release. Current release: ${activeRelease?.name || 'none'}`}
          aria-expanded={releaseOpen && !collapsed}
          aria-controls="sidebar-release-options"
        >
          <span className="project-icon">◆</span>
          {!collapsed && (
            <>
              <span className="project-name">{activeRelease?.name || 'Release'}</span>
              <span className="project-chevron">⌄</span>
            </>
          )}
        </TooltipButton>
        {releaseOpen && !collapsed && (
          <div className="project-menu" id="sidebar-release-options" aria-label="Release options">
            <div className="project-menu-list">
              {!activeApplication ? (
                <p className="project-menu-empty">Select an application first</p>
              ) : releases.length === 0 ? (
                <p className="project-menu-empty">No releases in {activeApplication.name}</p>
              ) : releases.map(release => (
                <button
                  key={release.id}
                  className={`project-menu-item ${activeRelease?.id === release.id ? 'active' : ''}`}
                  aria-current={activeRelease?.id === release.id ? 'true' : undefined}
                  onClick={() => {
                    onReleaseChange?.(release)
                    setReleaseOpen(false)
                  }}
                >
                  {release.name}
                </button>
              ))}
            </div>
            {activeProject && activeApplication && onCreateRelease && canCreateReleases && (
              <button
                className="project-menu-action"
                onClick={() => {
                  setReleaseOpen(false)
                  runGuarded(() => setShowReleaseModal(true))
                }}
              >
                New Release
              </button>
            )}
          </div>
        )}
      </div>
          </div>
      </div>

      <nav className="sidebar-nav" aria-label="Primary navigation">
        {visibleNavItemsById.get('new-test') && (
          <TooltipButton
            className={`nav-item nav-item-primary ${active === 'new-test' ? 'active' : ''}`}
            onClick={() => navigateTo('new-test')}
            aria-label="New run"
            title={collapsed ? 'New run' : undefined}
            tooltipPlacement="right"
            aria-current={active === 'new-test' ? 'page' : undefined}
            data-nav-label="New run"
          >
            <span className="nav-icon">{visibleNavItemsById.get('new-test')?.icon}</span>
            {!collapsed && <span className="nav-label">New run</span>}
          </TooltipButton>
        )}
        {NAV_GROUPS.map(group => {
          const items = group.itemIds
            .map(itemId => visibleNavItemsById.get(itemId))
            .filter((item): item is NavItem => Boolean(item))
          if (items.length === 0) return null
          return (
            <div className="sidebar-nav-group" key={group.label}>
              {!collapsed && <p className="sidebar-nav-group-label">{group.label}</p>}
              {items.map(item => (
                <TooltipButton
                  key={item.id}
                  className={`nav-item ${active === item.id ? 'active' : ''}`}
                  onClick={() => navigateTo(item.id)}
                  aria-label={item.label}
                  title={collapsed ? item.label : undefined}
                  tooltipPlacement="right"
                  aria-current={active === item.id ? 'page' : undefined}
                  data-nav-label={item.label}
                >
                  <span className="nav-icon">{item.icon}</span>
                  {!collapsed && <span className="nav-label">{item.label}</span>}
                </TooltipButton>
              ))}
            </div>
          )
        })}
      </nav>

      <div className="sidebar-footer">
        <div className="sidebar-footer-meta">
          {collapsed && authStatus?.authenticated && authStatus.user && identityName && (
            // The collapsed rail hides the identity card, so keep the signed-in
            // user and any session-expiry warning visible as an avatar. Clicking
            // it opens the sidebar on the full card with Sign out.
            <TooltipButton
              type="button"
              className={`sidebar-rail-avatar ${sessionExpiryNotice ? (sessionExpiryNotice === 'Session expired' ? 'session-expired' : 'session-warning') : ''}`}
              onClick={() => setCollapsed(false)}
              aria-label={[identityName, identityMeta, sessionExpiryNotice, 'Open account details'].filter(Boolean).join('. ')}
              title={[identityName, identityMeta, sessionExpiryNotice].filter(Boolean).join(' · ')}
              tooltipPlacement="right"
            >
              <span aria-hidden="true">{identityInitials}</span>
              {sessionExpiryNotice && <span className="sidebar-rail-avatar-alert" aria-hidden="true">!</span>}
            </TooltipButton>
          )}
          {!collapsed && authStatus?.authenticated && authStatus.user && identityName && (
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
                {logoutError && <span className="sidebar-identity-meta" role="alert">{logoutError}</span>}
              </div>
            </div>
          )}
          {isDevDashboard && (
            <div className="dev-view-control">
              <TooltipButton
                type="button"
                className="dev-view-toggle"
                aria-label={`Preview permissions. Current mode: ${devViewModeLabel}`}
                title={collapsed ? `Preview permissions (${devViewModeLabel})` : undefined}
                tooltipPlacement="right"
                aria-expanded={devViewOpen && !collapsed}
                aria-controls="sidebar-dev-view-panel"
                onClick={() => {
                  setDevViewOpen(open => (collapsed ? true : !open))
                  setCollapsed(false)
                }}
              >
                <span aria-hidden="true">DEV</span>
                {!collapsed && <span>Preview permissions</span>}
              </TooltipButton>
              {devViewOpen && !collapsed && (
                <div className="dev-view-panel" id="sidebar-dev-view-panel" aria-label="Development preview mode">
              <div className="dev-view-label-row">
                <span>Dev View</span>
                {isDevUserPreview && <span className="dev-view-badge">Editor preview</span>}
              </div>
              <div className="dev-view-segmented" role="group" aria-label="Development dashboard view">
                <button
                  type="button"
                  className={devViewMode === 'admin' ? 'active' : ''}
                  aria-pressed={devViewMode === 'admin'}
                  onClick={() => setDevViewMode('admin')}
                >
                  Admin
                </button>
                <button
                  type="button"
                  className={devViewMode === 'user' ? 'active' : ''}
                  aria-pressed={devViewMode === 'user'}
                  onClick={() => setDevViewMode('user')}
                >
                  Editor
                </button>
              </div>
              <button
                type="button"
                className="sidebar-identity-action sidebar-identity-action-button"
                onClick={handleDevPreviewLogout}
              >
                Sign out
              </button>
                </div>
              )}
            </div>
          )}
          <div className="sidebar-footer-row">
            <div className={`sidebar-status ${connectionClass}`} title={collapsed ? `Live connection: ${connectionLabel}` : undefined} role="status" aria-live="polite">
              <span className="status-dot" aria-hidden="true" />
              {/* Visually hidden in the collapsed rail, but still announced. */}
              <span className="status-text"><span className="sr-only">Live connection: </span>{connectionLabel}</span>
            </div>
            <BuildBadge placement="up" />
          </div>
        </div>
        <TooltipButton
          className="collapse-btn"
          onClick={() => setCollapsed(!collapsed)}
          title={collapsed ? 'Expand sidebar' : undefined}
          tooltipPlacement="right"
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-expanded={!collapsed}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            {collapsed ? (
              <polyline points="9 18 15 12 9 6" />
            ) : (
              <polyline points="15 18 9 12 15 6" />
            )}
          </svg>
        </TooltipButton>
      </div>
      {showProjectModal && (
        <ProjectModal
          title="New Project"
          returnFocusSelector='[aria-controls="sidebar-project-options"]'
          onClose={() => setShowProjectModal(false)}
          onSubmit={handleNewProject}
        />
      )}
      {showApplicationModal && (
        <ProjectModal
          title="New Application"
          returnFocusSelector='[aria-controls="sidebar-application-options"]'
          onClose={() => setShowApplicationModal(false)}
          onSubmit={handleNewApplication}
        />
      )}
      {showReleaseModal && (
        <ProjectModal
          title="New Release"
          returnFocusSelector='[aria-controls="sidebar-release-options"]'
          namePlaceholder="Release name"
          onClose={() => setShowReleaseModal(false)}
          onSubmit={handleNewRelease}
        />
      )}
    </aside>
    </div>
  )
}

const ROLE_LABELS: Record<string, string> = {
  system_admin: 'System Admin',
  project_admin: 'Project Admin',
  editor: 'Editor',
  viewer: 'Viewer',
}

const ROLE_PRIORITY: Record<string, number> = {
  system_admin: 4,
  project_admin: 3,
  editor: 2,
  viewer: 1,
}

interface IdentityScope {
  domainId?: string
  applicationRepositoryId?: string
  projectId?: string
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

function formatIdentityMeta(authStatus?: DashboardAuthStatus, scope: IdentityScope = {}): string {
  const user = authStatus?.user
  if (!user) {
    return ''
  }

  const relevantRoles = [
    ...(user.global_roles || []),
    ...(scope.domainId ? user.domain_roles?.[scope.domainId] || [] : []),
    ...(scope.applicationRepositoryId
      ? user.application_repository_roles?.[scope.applicationRepositoryId] || []
      : []),
    ...(scope.projectId ? user.project_roles?.[scope.projectId] || [] : []),
  ]
  const highestRole = relevantRoles.reduce<string | undefined>((highest, role) => {
    if (!highest || (ROLE_PRIORITY[role] || 0) > (ROLE_PRIORITY[highest] || 0)) {
      return role
    }
    return highest
  }, undefined)
  if (highestRole) {
    return ROLE_LABELS[highestRole] || highestRole
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

function resolveSessionDeadline(authStatus?: DashboardAuthStatus): number | undefined {
  if (!authStatus?.authenticated || (authStatus.mode !== 'oidc' && authStatus.mode !== 'local')) {
    return undefined
  }
  const expiresAt = Date.parse(authStatus.session_expires_at || '')
  if (Number.isFinite(expiresAt)) {
    return expiresAt
  }
  const ttl = authStatus.session_ttl_seconds
  if (typeof ttl !== 'number' || !Number.isFinite(ttl) || ttl <= 0) {
    return undefined
  }
  return Date.now() + ttl * 1000
}

function formatSessionExpiryNotice(
  authStatus: DashboardAuthStatus | undefined,
  sessionDeadline: number | undefined,
  now: number,
): string {
  if (
    !authStatus?.authenticated
    || (authStatus.mode !== 'oidc' && authStatus.mode !== 'local')
    || sessionDeadline === undefined
  ) {
    return ''
  }
  const remainingSeconds = Math.ceil((sessionDeadline - now) / 1000)
  if (remainingSeconds <= 0) return 'Session expired'
  if (remainingSeconds > 15 * 60) return ''
  const minutes = Math.max(1, Math.ceil(remainingSeconds / 60))
  return `Session expires in ${minutes} min`
}
