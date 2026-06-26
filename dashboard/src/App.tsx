import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useDashboardTheme } from './hooks/useDashboardTheme'
import { useDashboardZoomShortcuts } from './hooks/useDashboardZoomShortcuts'
import { KeyboardShortcutProvider, useKeyboardShortcuts } from './hooks/KeyboardShortcutProvider'
import { useWebSocket } from './hooks/useWebSocket'
import { ApplicationProvider } from './hooks/ApplicationProvider'
import { useApplication } from './hooks/useApplication'
import { MetricsHistoryPoint, TelemetryDiagnostic, TelemetryDiagnosticEntry, WSMessage } from './types'
import Sidebar, { type NavPage } from './components/Sidebar'
import DashboardPage, { type DashboardSnapshotAudience } from './components/DashboardPage'
import TablePaginationManager from './components/TablePaginationManager'
import { type AppView, parseAppViewFromHash, type TestWorkspaceTabID, viewToLocationHash } from './appRouting'
import { appendTelemetryDiagnostic } from './components/monitoringTelemetryDiagnostics'
import { mapExecutionMetricToHistoryPoint } from './components/testExecutionMetricsModel'
import AuthGate from './components/AuthGate'
import { useAuthStatus } from './hooks/useAuthStatus'
import { ProjectProvider } from './hooks/ProjectProvider'
import { useProject } from './hooks/useProject'
import { DevViewProvider } from './hooks/DevViewProvider'
import { useDevView } from './hooks/useDevView'
import { useSystemApi } from './hooks/api/useSystemApi'
import { useRepositoryApi } from './hooks/api/useRepositoryApi'
import { useLibraryApi } from './hooks/api/useLibraryApi'
import { useTestApi } from './hooks/api/useTestApi'
import {
  TopbarContributionProvider,
  useActiveTopbarContribution,
  type TopbarAction,
  type TopbarMetaChip,
} from './hooks/TopbarContributionProvider'
import { NavigationGuardProvider, useNavigationGuardRunner } from './hooks/NavigationGuardProvider'
import { isEffectiveOperationAllowed } from './utils/authAccessUtils'
import { DesktopStatusResponse } from './types/library'
import ThemeToggle from './components/ThemeToggle'
import DeploymentBanner from './components/DeploymentBanner'
import ClientDownloadsMenu from './components/ClientDownloadsMenu'
import {
  pageTitleForScriptSubView,
  pageTitleForView,
  selectTopbarActiveRuns,
  shouldShowTopbarClientDownloads,
  topbarRunStatusLabel,
  topbarRunTone,
  updateTopbarActiveRuns,
  type TopbarRunSummary,
} from './appTopbar'
import { buildNavigationShortcuts, focusCurrentPageSearch } from './appKeyboardShortcuts'
import type { ScriptsAuthoringSubView } from './components/useScriptsAuthoringState'
import './App.css'
import './components/Pages.css'

const ScriptsPage = lazy(() => import('./components/ScriptsPage'))
const CommandCenterPrototype = lazy(() => import('./components/CommandCenterPrototype'))
const TestHub = lazy(() => import('./components/TestHub'))
const TestExecution = lazy(() => import('./components/TestExecution'))
const ResultsPage = lazy(() => import('./components/ResultsPage'))
const NewTestPage = lazy(() => import('./components/NewTestPage'))
const MonitoringPage = lazy(() => import('./components/MonitoringPage'))
const ApiGuidePage = lazy(() => import('./components/ApiGuidePage'))
const SwaggerDocsPage = lazy(() => import('./components/SwaggerDocsPage'))
const RepositoryGuidePage = lazy(() => import('./components/RepositoryGuidePage'))
const ApplicationRepositoryPage = lazy(() => import('./components/ApplicationRepositoryPage'))
const AccessPage = lazy(() => import('./components/AccessPage'))
const SettingsPage = lazy(() => import('./components/SettingsPage'))
const BrowserRecorderControlPanel = lazy(() => import('./components/BrowserRecorderControlPanel'))

type WailsDesktopBridge = {
  OpenInBrowser?: () => Promise<void>
  OpenLogsFolder?: () => Promise<void>
  OpenAppDataFolder?: () => Promise<void>
}

function getWailsDesktopBridge(): WailsDesktopBridge | undefined {
  if (typeof window === 'undefined') return undefined
  return (window as unknown as { go?: { main?: { App?: WailsDesktopBridge } } }).go?.main?.App
}

function formatDesktopRemoteHealth(status?: DesktopStatusResponse) {
  const count = status?.remote_libraries?.count || 0
  if (count === 0) return '0 hosts'
  const statuses = status?.remote_libraries?.statuses || {}
  const healthy = statuses.healthy || 0
  return `${healthy}/${count} hosts`
}

function topbarActionClass(action: TopbarAction): string {
  const tone = action.tone || 'secondary'
  const btnClass = tone === 'primary'
    ? 'btn-primary'
    : tone === 'success'
      ? 'btn-success'
      : tone === 'danger'
        ? 'btn-danger'
        : tone === 'ghost'
          ? 'btn-ghost'
          : 'btn-secondary'
  return ['btn', 'btn-sm', 'topbar-action-button', btnClass, action.className, action.compactLabel ? 'has-compact-label' : '', action.pressed ? 'topbar-action-pressed' : '']
    .filter(Boolean)
    .join(' ')
}

function renderTopbarAction(action: TopbarAction) {
  if (action.hidden) return null
  return (
    <button
      key={action.id}
      type="button"
      className={topbarActionClass(action)}
      aria-label={action.ariaLabel}
      aria-pressed={action.pressed}
      title={action.title}
      disabled={action.disabled}
      onClick={action.onClick}
    >
      {action.icon && <span className="topbar-action-icon" aria-hidden="true">{action.icon}</span>}
      <span className="topbar-action-label">{action.label}</span>
      {action.compactLabel && <span className="topbar-action-compact-label">{action.compactLabel}</span>}
    </button>
  )
}

function renderTopbarMetaChip(chip: TopbarMetaChip) {
  if (chip.hidden) return null
  return (
    <span key={chip.id} className={`topbar-meta-chip ${chip.tone || 'default'}`} title={chip.title}>
      {chip.label}
    </span>
  )
}

function dashboardSnapshotAudience(
  authStatus: import('./types').DashboardAuthStatus | undefined,
  devViewMode: string,
  activeProjectId?: string,
): DashboardSnapshotAudience {
  if (authStatus?.mode === 'dev') {
    return devViewMode === 'user' ? 'user' : 'admin'
  }
  if (!authStatus || !authStatus.enabled) {
    return 'admin'
  }

  const globalRoles = authStatus.user?.global_roles || []
  const projectRoles = activeProjectId ? authStatus.user?.project_roles?.[activeProjectId] || [] : []
  return [...globalRoles, ...projectRoles].some(role => role === 'system_admin' || role === 'project_admin')
    ? 'admin'
    : 'user'
}

function AppShell({ authStatus }: { authStatus?: import('./types').DashboardAuthStatus }) {
  useDashboardZoomShortcuts()

  const [view, setView] = useState<AppView>(() => (
    typeof window === 'undefined' ? { page: 'dashboard' } : parseAppViewFromHash(window.location.hash)
  ))
  const routeSyncInitialized = useRef(false)
  const [metricsHistory, setMetricsHistory] = useState<Record<string, MetricsHistoryPoint[]>>({})
  const [testStatuses, setTestStatuses] = useState<Record<string, string>>({})
  const [telemetryDiagnostics, setTelemetryDiagnostics] = useState<TelemetryDiagnosticEntry[]>([])
  const { theme, setTheme } = useDashboardTheme()
  const { mode: devViewMode } = useDevView()
  const { getAuthEffectiveAccess } = useSystemApi()
  const libraryApi = useLibraryApi()
  const repositoryApi = useRepositoryApi()
  const isDevUserPreview = authStatus?.mode === 'dev' && devViewMode === 'user'
  const scriptMode = view.page === 'scripts' && 'mode' in view ? view.mode : undefined
  const scriptEditId = view.page === 'scripts' && 'scriptId' in view ? view.scriptId : undefined
  const [scriptsNavigationVersion, setScriptsNavigationVersion] = useState(0)
  const [scriptsTopbarState, setScriptsTopbarState] = useState<{ subView: ScriptsAuthoringSubView; editingId: string | null } | null>(null)
  const [effectiveAccessResources, setEffectiveAccessResources] = useState<import('./types').DashboardAuthEffectiveAccessResource[]>([])
  const [domains, setDomains] = useState<import('./types').Domain[]>([])
  const [activeDomain, setActiveDomainState] = useState<import('./types').Domain | null>(null)
  const [applicationRepository, setApplicationRepository] = useState<import('./types').ApplicationRepository | null>(null)
  const [repositoryCategories, setRepositoryCategories] = useState<import('./types').RepositoryCategory[]>([])
  const [desktopStatus, setDesktopStatus] = useState<DesktopStatusResponse | null>(null)
  const [activeTopbarRuns, setActiveTopbarRuns] = useState<TopbarRunSummary[]>([])
  const testApi = useTestApi()
  const activeTopbarContribution = useActiveTopbarContribution()
  const { runGuarded } = useNavigationGuardRunner()

  const handleWSMessage = useCallback((msg: WSMessage) => {
    if (msg.type === 'metrics_update' && msg.data && msg.test_id && msg.timestamp) {
      const testId = msg.test_id
      const point = mapExecutionMetricToHistoryPoint({ ...msg.data, timestamp: String(msg.timestamp) })
      setMetricsHistory((prev) => {
        const existing = prev[testId] || []
        const updated = [...existing, point].slice(-300)
        return { ...prev, [testId]: updated }
      })
    }

    if (msg.type === 'telemetry_diagnostic' && msg.data && msg.test_id && msg.timestamp) {
      const diagnostic = msg.data as unknown as TelemetryDiagnostic
      if (diagnostic.summary && diagnostic.severity) {
        const testId = msg.test_id
        const timestamp = msg.timestamp
        setTelemetryDiagnostics((prev) => appendTelemetryDiagnostic(prev, {
          test_id: testId,
          test_name: msg.test_name,
          timestamp,
          diagnostic,
        }))
      }
    }

    if (msg.status && msg.test_id) {
      const testId = msg.test_id
      setTestStatuses((prev) => ({ ...prev, [testId]: msg.status! }))
      setActiveTopbarRuns((prev) => updateTopbarActiveRuns(prev, {
        id: testId,
        name: msg.test_name,
        status: msg.status,
        timestamp: msg.timestamp,
      }))
    }
  }, [])

  const { projects, activeProject, setActiveProject, createAndSelectProject, refreshProjects } = useProject()
  const domainProjects = useMemo(() => {
    const domainID = activeDomain?.id || 'default'
    return projects.filter(project => (project.domain_id || 'default') === domainID)
  }, [activeDomain?.id, projects])
  const { connectionState } = useWebSocket({ onMessage: handleWSMessage, projectId: activeProject?.id })
  const { applications, releases, activeApplication, activeRelease, setActiveApplication, setActiveRelease, createAndSelectApplication, createAndSelectRelease } = useApplication()
  const canManageServiceAccounts = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_service_accounts', false)
  const canManageUserLifecycle = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_users', false)
  const canManageUserBindings = canManageUserLifecycle || isEffectiveOperationAllowed(effectiveAccessResources, 'manage_user_bindings', false)
  const canViewUserActivity = isEffectiveOperationAllowed(effectiveAccessResources, 'view_user_activity', false)
  const canUseAccessWorkspace = canManageServiceAccounts || canManageUserBindings || canViewUserActivity
  const canManageRepositoryProjects = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_repository_projects', false)
  const canManageRepositoryReleases = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_repository_releases', false)
  const canManageRepository = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_application_repository', false)
  const canManageSystem = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_system', false)
  const snapshotAudience = dashboardSnapshotAudience(authStatus, devViewMode, activeProject?.id)

  const setGuardedView = useCallback((next: AppView | ((current: AppView) => AppView)) => {
    runGuarded(() => {
      setView(current => (typeof next === 'function' ? next(current) : next))
    })
  }, [runGuarded])

  const navigateToTest = useCallback((id: string, tab?: TestWorkspaceTabID) => {
    setGuardedView(tab ? { page: 'test', testId: id, tab } : { page: 'test', testId: id })
  }, [setGuardedView])
  const navigateToTestArchive = useCallback((id: string) => navigateToTest(id, 'archive'), [navigateToTest])
  const navigateToCommandCenter = useCallback((id?: string) => {
    setGuardedView(id ? { page: 'command-center', testId: id } : { page: 'command-center' })
  }, [setGuardedView])
  const activePage: NavPage = view.page === 'test' ? 'tests' : view.page === 'swagger' || view.page === 'repository-guide' ? 'docs' : view.page === 'recorder-panel' ? 'scripts' : view.page

  const navigateToScriptEdit = (scriptId: string) => setGuardedView({ page: 'scripts', mode: 'edit', scriptId })
  const navigateToScenarioBuilder = () => setGuardedView({ page: 'tests', scenarioView: 'builder' })
  const navigateToNewTestForScenario = (scenarioId: string) => {
    setView({ page: 'new-test', scenarioId })
  }

  const navigateToScheduleForDate = (prefillStartLocal: string) => setGuardedView({ page: 'new-test', prefillStartLocal })

  const performNavigate = useCallback((page: NavPage) => {
    if (page === 'scripts') {
      setScriptsNavigationVersion(version => version + 1)
      setScriptsTopbarState(null)
    }
    setView({ page })
  }, [])

  const handleNavigate = useCallback((page: NavPage) => {
    runGuarded(() => performNavigate(page))
  }, [performNavigate, runGuarded])

  const handleScriptsAuthoringStateChange = useCallback((state: { subView: ScriptsAuthoringSubView; editingId: string | null }) => {
    setScriptsTopbarState(state)
  }, [])

  const handleScenarioViewChange = useCallback((scenarioView: 'library' | 'builder' | 'plans') => {
    setView(current => {
      if (current.page !== 'tests') return current
      const next: AppView = scenarioView === 'builder'
        ? { page: 'tests', scenarioView: 'builder' }
        : scenarioView === 'plans'
          ? { page: 'tests', scenarioView: 'plans' }
          : { page: 'tests' }
      return viewToLocationHash(current) === viewToLocationHash(next) ? current : next
    })
  }, [])

  useEffect(() => {
    if (view.page !== 'scripts') {
      setScriptsTopbarState(null)
    }
  }, [view.page])

  const appHistoryDepth = useRef(0)
  useEffect(() => {
    appHistoryDepth.current += 1
  }, [view])

  const navigateBack = useCallback((fallbackPage: NavPage = 'dashboard') => {
    if (appHistoryDepth.current > 1) {
      runGuarded(() => window.history.back())
    } else {
      setGuardedView({ page: fallbackPage })
    }
  }, [runGuarded, setGuardedView])
  const navigateBackToTests = useCallback(() => navigateBack('tests'), [navigateBack])

  const appShellShortcuts = useMemo(() => [
    {
      id: 'global.back',
      label: 'Go back',
      keys: 'Backspace',
      group: 'Global',
      priority: 600,
      onTrigger: () => navigateBack('dashboard'),
    },
    {
      id: 'current-page.search',
      label: 'Focus page search or filter',
      keys: '/',
      group: 'Current Page',
      priority: 200,
      onTrigger: () => {
        focusCurrentPageSearch()
      },
    },
    {
      id: 'global.zoom-in',
      label: 'Zoom dashboard in',
      keys: 'Cmd/Ctrl+=',
      group: 'Global',
      preventDefault: false,
      priority: 20,
      onTrigger: () => undefined,
    },
    {
      id: 'global.zoom-out',
      label: 'Zoom dashboard out',
      keys: 'Cmd/Ctrl+-',
      group: 'Global',
      preventDefault: false,
      priority: 20,
      onTrigger: () => undefined,
    },
    {
      id: 'global.zoom-reset',
      label: 'Reset dashboard zoom',
      keys: 'Cmd/Ctrl+0',
      group: 'Global',
      preventDefault: false,
      priority: 20,
      onTrigger: () => undefined,
    },
    ...buildNavigationShortcuts({
      canUseAccessWorkspace,
      isDevUserPreview,
      onNavigate: handleNavigate,
    }),
  ], [canUseAccessWorkspace, handleNavigate, isDevUserPreview, navigateBack])
  useKeyboardShortcuts(appShellShortcuts)

  useEffect(() => {
    if (typeof window === 'undefined') return undefined

    const handleRouteChange = () => {
      const nextView = parseAppViewFromHash(window.location.hash)
      const nextHash = viewToLocationHash(nextView)
      setView(current => (viewToLocationHash(current) === nextHash ? current : nextView))
    }

    window.addEventListener('popstate', handleRouteChange)
    window.addEventListener('hashchange', handleRouteChange)
    return () => {
      window.removeEventListener('popstate', handleRouteChange)
      window.removeEventListener('hashchange', handleRouteChange)
    }
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined') return

    const nextHash = viewToLocationHash(view)
    if (!routeSyncInitialized.current) {
      routeSyncInitialized.current = true
      if (!window.location.hash && view.page === 'dashboard') return
      if (window.location.hash !== nextHash) {
        window.history.replaceState(null, '', nextHash)
      }
      return
    }

    if (window.location.hash !== nextHash) {
      window.history.pushState(null, '', nextHash)
    }
  }, [view])

  const setActiveDomain = useCallback((domain: import('./types').Domain) => {
    setActiveDomainState(domain)
    if (typeof window !== 'undefined') {
      window.localStorage.setItem('apexload.activeDomainId', domain.id)
    }
  }, [])

  const refreshDomains = useCallback(async (preferredId?: string) => {
    try {
      const response = await repositoryApi.listDomains()
      const next = response.domains || []
      setDomains(next)
      setActiveDomainState(prev => {
        const storedId = typeof window !== 'undefined' ? window.localStorage.getItem('apexload.activeDomainId') : ''
        const selected = next.find(domain => domain.id === preferredId)
          || next.find(domain => domain.id === storedId)
          || next.find(domain => domain.id === prev?.id)
          || next[0]
          || null
        if (typeof window !== 'undefined') {
          if (selected) {
            window.localStorage.setItem('apexload.activeDomainId', selected.id)
          } else {
            window.localStorage.removeItem('apexload.activeDomainId')
          }
        }
        return selected
      })
      return next
    } catch {
      const fallback: import('./types').Domain = {
        id: 'default',
        name: 'Default Domain',
        created_at: new Date(0).toISOString(),
        updated_at: new Date(0).toISOString(),
      }
      setDomains([fallback])
      setActiveDomainState(fallback)
      if (typeof window !== 'undefined') {
        window.localStorage.setItem('apexload.activeDomainId', fallback.id)
      }
      return [fallback]
    }
  }, [repositoryApi])

  const refreshRepository = useCallback(async () => {
    if (!activeDomain?.id) {
      setApplicationRepository(null)
      setRepositoryCategories([])
      return
    }
    const [repo, categoryResponse] = await Promise.all([
      repositoryApi.getApplicationRepository(activeDomain.id),
      repositoryApi.listRepositoryCategories(activeDomain.id),
    ])
    setApplicationRepository(repo)
    setRepositoryCategories(categoryResponse.categories || [])
  }, [activeDomain?.id, repositoryApi])

  useEffect(() => {
    void refreshDomains()
  }, [refreshDomains])

  useEffect(() => {
    refreshRepository().catch(() => {
      setApplicationRepository(null)
      setRepositoryCategories([])
    })
  }, [refreshRepository])

  useEffect(() => {
    if (!activeDomain || domainProjects.length === 0) {
      return
    }
    if (!activeProject || (activeProject.domain_id || 'default') !== activeDomain.id) {
      setActiveProject(domainProjects[0])
    }
  }, [activeDomain, activeProject, domainProjects, setActiveProject])

  useEffect(() => {
    let cancelled = false
    const loadEffectiveAccess = async () => {
      try {
        const response = await getAuthEffectiveAccess(activeProject?.id, {
          domain_id: activeDomain?.id,
          application_repository_id: applicationRepository?.id,
        })
        if (!cancelled) {
          setEffectiveAccessResources(response.resources || [])
        }
      } catch {
        if (!cancelled) {
          setEffectiveAccessResources([])
        }
      }
    }

    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, activeDomain?.id, applicationRepository?.id, authStatus?.mode, devViewMode, getAuthEffectiveAccess])

  useEffect(() => {
    if (!isDevUserPreview) {
      return
    }
    setView((current) => {
      if (current.page === 'settings') {
        return { page: 'dashboard' }
      }
      return current
    })
  }, [isDevUserPreview])

  useEffect(() => {
    if (view.page === 'access' && !canUseAccessWorkspace) {
      setView({ page: 'dashboard' })
    }
  }, [canUseAccessWorkspace, view.page])

  useEffect(() => {
    let cancelled = false
    const loadDesktopStatus = async () => {
      try {
        const status = await libraryApi.getDesktopStatus()
        if (!cancelled) {
          setDesktopStatus(status)
        }
      } catch {
        if (!cancelled) {
          setDesktopStatus(null)
        }
      }
    }

    void loadDesktopStatus()
    const interval = window.setInterval(() => {
      void loadDesktopStatus()
    }, 30000)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [libraryApi])

  const desktopBridge = getWailsDesktopBridge()
  const showDesktopStatus = Boolean(desktopStatus?.desktop_mode)
  const currentRouteTestId = view.page === 'test' || view.page === 'command-center' ? view.testId : undefined
  const currentTestRun = currentRouteTestId ? activeTopbarRuns.find(run => run.id === currentRouteTestId) : undefined
  const topbarPageTitle = view.page === 'scripts' && scriptsTopbarState
    ? pageTitleForScriptSubView(scriptsTopbarState.subView)
    : pageTitleForView(view, {
      repositoryName: applicationRepository?.name,
      testName: currentTestRun?.name,
    })
  const orderedTopbarRuns = view.page === 'test' && currentTestRun
    ? [currentTestRun, ...activeTopbarRuns.filter(run => run.id !== currentTestRun.id)]
    : activeTopbarRuns
  const visibleTopbarRuns = orderedTopbarRuns.slice(0, 2)
  const hiddenTopbarRunCount = Math.max(0, orderedTopbarRuns.length - visibleTopbarRuns.length)
  const topbarPrimaryActions = useMemo(() => (
    (activeTopbarContribution?.primaryActions || []).filter(action => !action.hidden)
  ), [activeTopbarContribution?.primaryActions])
  const topbarSecondaryActions = useMemo(() => (
    (activeTopbarContribution?.secondaryActions || []).filter(action => !action.hidden)
  ), [activeTopbarContribution?.secondaryActions])
  const topbarMeta = useMemo(() => (
    (activeTopbarContribution?.meta || []).filter(chip => !chip.hidden)
  ), [activeTopbarContribution?.meta])
  const hasContextualTopbarActions = topbarPrimaryActions.length > 0 || topbarSecondaryActions.length > 0 || Boolean(activeTopbarContribution?.controls)
  const showTopbarRunChips = visibleTopbarRuns.length > 0 && !hasContextualTopbarActions
  const effectiveTopbarTitle = activeTopbarContribution?.title || topbarPageTitle
  const showTopbarClientDownloads = shouldShowTopbarClientDownloads(view)
  const mainContentClassName = `main-content topbar-title-mode page-${view.page}`

  const topbarActionShortcuts = useMemo(() => (
    [...topbarSecondaryActions, ...topbarPrimaryActions]
      .filter(action => action.shortcut)
      .map(action => ({
        id: `topbar.${activeTopbarContribution?.id || 'active'}.${action.id}`,
        label: action.label,
        keys: action.shortcut!,
        group: action.shortcutGroup || 'Current Page',
        disabled: action.disabled,
        hidden: action.hidden,
        allowInEditable: action.allowShortcutInEditable,
        priority: 700,
        onTrigger: action.onClick,
      }))
  ), [activeTopbarContribution?.id, topbarPrimaryActions, topbarSecondaryActions])
  useKeyboardShortcuts(topbarActionShortcuts)

  useEffect(() => {
    let cancelled = false

    const loadTopbarRuns = async () => {
      if (!activeProject?.id) {
        setActiveTopbarRuns([])
        return
      }

      try {
        const response = await testApi.listTests({
          project_id: activeProject.id,
          application_id: activeApplication?.id,
        })
        if (!cancelled) {
          setActiveTopbarRuns(selectTopbarActiveRuns(response.tests || [], testStatuses))
        }
      } catch {
        // WebSocket updates will keep the rail useful if the snapshot fetch fails.
      }
    }

    void loadTopbarRuns()
    const interval = window.setInterval(() => {
      void loadTopbarRuns()
    }, 10000)

    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [activeApplication?.id, activeProject?.id, testApi, testStatuses])

  // The recorder control panel renders chrome-less so it works as a compact
  // companion popup window next to the recording browser.
  if (view.page === 'recorder-panel' && view.sessionId) {
    return (
      <Suspense fallback={<div className="page-loading">Loading…</div>}>
        <BrowserRecorderControlPanel sessionId={view.sessionId} standalone />
      </Suspense>
    )
  }

  return (
    <div className="app-layout">
      <Sidebar
        active={activePage}
        onNavigate={handleNavigate}
        connectionState={connectionState}
        authStatus={authStatus}
        projects={domainProjects}
        activeProject={activeProject}
        onProjectChange={setActiveProject}
        onCreateProject={createAndSelectProject}
        domains={domains}
        activeDomain={activeDomain}
        onDomainChange={setActiveDomain}
        applications={applications}
        releases={releases}
        activeApplication={activeApplication}
        activeRelease={activeRelease}
        onApplicationChange={setActiveApplication}
        onReleaseChange={setActiveRelease}
        onCreateApplication={createAndSelectApplication}
        showAccess={canUseAccessWorkspace}
      />
      <main className={mainContentClassName}>
        <div className={`main-topbar${activeTopbarContribution ? ' contextual' : ''}`} aria-label="Dashboard controls">
          <div className="main-topbar-page">
            {activeTopbarContribution?.titleNode || <strong className="topbar-page-title">{effectiveTopbarTitle}</strong>}
          </div>
          {topbarMeta.length > 0 && (
            <div className="topbar-meta-strip" aria-label="Current context">
              {topbarMeta.map(renderTopbarMetaChip)}
            </div>
          )}
          {activeTopbarContribution?.controls && (
            <div className="topbar-contribution-controls">
              {activeTopbarContribution.controls}
            </div>
          )}
          {showTopbarRunChips && (
            <nav className="topbar-active-runs" aria-label="Active test runs">
              {visibleTopbarRuns.map((run) => {
                const statusLabel = topbarRunStatusLabel(run.status)
                const tone = topbarRunTone(run.status)
                return (
                  <a
                    key={run.id}
                    className={`topbar-run-chip ${tone}`}
                    href={viewToLocationHash({ page: 'command-center', testId: run.id })}
                    title={`Open ${run.name} (${statusLabel})`}
                    onClick={(event) => {
                      event.preventDefault()
                      navigateToCommandCenter(run.id)
                    }}
                  >
                    <span className="topbar-run-dot" aria-hidden="true" />
                    <span className="topbar-run-name">{run.name}</span>
                  </a>
                )
              })}
              {hiddenTopbarRunCount > 0 && (
                <a
                  className="topbar-run-chip topbar-run-more"
                  href={viewToLocationHash({ page: 'tests' })}
                  onClick={(event) => {
                    event.preventDefault()
                    setGuardedView({ page: 'tests' })
                  }}
                >
                  +{hiddenTopbarRunCount} run{hiddenTopbarRunCount === 1 ? '' : 's'}
                </a>
              )}
            </nav>
          )}
          <div className="main-topbar-actions">
            {topbarSecondaryActions.length > 0 && (
              <div className="topbar-action-group secondary" aria-label="Contextual secondary actions">
                {topbarSecondaryActions.map(renderTopbarAction)}
              </div>
            )}
            {topbarPrimaryActions.length > 0 && (
              <div className="topbar-action-group primary" aria-label="Contextual primary actions">
                {topbarPrimaryActions.map(renderTopbarAction)}
              </div>
            )}
            {showDesktopStatus && (
              <div className="desktop-status-strip" aria-label="Desktop controller status">
                <span className="script-health-token healthy">Desktop</span>
                <span className="desktop-status-address">{desktopStatus?.browser_url || desktopStatus?.api_address}</span>
                <span className="script-health-token">{formatDesktopRemoteHealth(desktopStatus || undefined)}</span>
                {desktopBridge?.OpenInBrowser && (
                  <button className="btn btn-sm btn-ghost" type="button" onClick={() => { void desktopBridge.OpenInBrowser?.() }}>
                    Open Browser
                  </button>
                )}
                {desktopBridge?.OpenLogsFolder && (
                  <button className="btn btn-sm btn-ghost" type="button" onClick={() => { void desktopBridge.OpenLogsFolder?.() }}>
                    Logs
                  </button>
                )}
                {desktopBridge?.OpenAppDataFolder && (
                  <button className="btn btn-sm btn-ghost" type="button" onClick={() => { void desktopBridge.OpenAppDataFolder?.() }}>
                    App Data
                  </button>
                )}
              </div>
            )}
            {showTopbarClientDownloads && <ClientDownloadsMenu />}
            <DeploymentBanner />
            <ThemeToggle theme={theme} onThemeChange={setTheme} />
          </div>
        </div>
        {activeTopbarContribution?.feedback && (
          <div className="main-topbar-feedback">
            {activeTopbarContribution.feedback}
          </div>
        )}
        <Suspense fallback={<div className="page">Loading page...</div>}>
          {view.page === 'dashboard' && (
            <DashboardPage
              testStatuses={testStatuses}
              metricsHistory={metricsHistory}
              connectionState={connectionState}
              onSelectTest={navigateToTest}
              onOpenTestArchive={navigateToTestArchive}
              onOpenCommandCenter={navigateToCommandCenter}
              onNavigate={handleNavigate}
              snapshotAudience={snapshotAudience}
            />
          )}
          {view.page === 'command-center' && (
            <CommandCenterPrototype
              testId={view.testId}
              history={view.testId ? metricsHistory[view.testId] || [] : []}
              statusOverride={view.testId ? testStatuses[view.testId] : undefined}
              applicationId={activeApplication?.id}
              projectId={activeProject?.id}
              onOpenExecution={navigateToTest}
              onOpenArchive={navigateToTestArchive}
              onOpenResults={() => handleNavigate('results')}
            />
          )}
          {view.page === 'tests' && (
            <TestHub
              initialView={view.scenarioView}
              testStatuses={testStatuses}
              onSelectTest={navigateToTest}
              onOpenCommandCenter={navigateToCommandCenter}
              onCreateTestFromScenario={navigateToNewTestForScenario}
              onNavigate={handleNavigate}
              onScenarioViewChange={handleScenarioViewChange}
            />
          )}
          {view.page === 'new-test' && (
            <NewTestPage
              activeApplication={activeApplication}
              activeRelease={activeRelease}
              releases={releases}
              initialScenarioId={'scenarioId' in view ? view.scenarioId : undefined}
              initialPrefillStartLocal={'prefillStartLocal' in view ? view.prefillStartLocal : undefined}
              onSelectTest={navigateToTest}
              onOpenCommandCenter={navigateToCommandCenter}
              onCreateRelease={createAndSelectRelease}
              onOpenScenarioBuilder={navigateToScenarioBuilder}
            />
          )}
          {view.page === 'test' && (
            <TestExecution
              testId={view.testId}
              history={metricsHistory[view.testId] || []}
              initialWorkspaceTabId={view.tab}
              onBack={navigateBackToTests}
              onEditScript={navigateToScriptEdit}
              onSelectTest={navigateToTest}
              onOpenCommandCenter={navigateToCommandCenter}
            />
          )}
          {view.page === 'scripts' && (
            <ScriptsPage
              key={`scripts-${scriptsNavigationVersion}-${scriptMode || 'list'}-${scriptEditId || ''}`}
              initialMode={scriptMode}
              initialEditId={scriptEditId}
              onSelectScenario={navigateToTest}
              onNavigate={handleNavigate}
              onAuthoringStateChange={handleScriptsAuthoringStateChange}
            />
          )}
          {view.page === 'results' && (
            <ResultsPage
              testStatuses={testStatuses}
              onSelectTest={navigateToTest}
              onOpenCommandCenter={navigateToCommandCenter}
              onOpenTestArchive={navigateToTestArchive}
              onNavigate={handleNavigate}
              activeApplication={activeApplication}
              activeRelease={activeRelease}
            />
          )}
          {view.page === 'monitoring' && <MonitoringPage connectionState={connectionState} telemetryDiagnostics={telemetryDiagnostics} onSelectTest={navigateToTest} canManageSystem={canManageSystem} onScheduleForDate={navigateToScheduleForDate} />}
          {view.page === 'schedules' && <MonitoringPage connectionState={connectionState} telemetryDiagnostics={telemetryDiagnostics} onSelectTest={navigateToTest} canManageSystem={canManageSystem} onScheduleForDate={navigateToScheduleForDate} initialCategoryId="schedules" />}
          {view.page === 'docs' && <ApiGuidePage activePage={view.guidePage} onNavigate={handleNavigate} effectiveAccessResources={effectiveAccessResources} />}
          {view.page === 'swagger' && <SwaggerDocsPage />}
          {view.page === 'repository-guide' && <RepositoryGuidePage />}
          {view.page === 'repository' && (
            <ApplicationRepositoryPage
              domains={domains}
              activeDomain={activeDomain}
              activeApplication={activeApplication}
              applicationRepository={applicationRepository}
              categories={repositoryCategories}
              projects={projects}
              canManageRepository={canManageRepository}
              canManageProjects={canManageRepositoryProjects}
              canManageReleases={canManageRepositoryReleases}
              onDomainChange={setActiveDomain}
              onRepositoryRefresh={refreshRepository}
              onProjectsRefresh={refreshProjects}
              onProjectCreated={createAndSelectProject}
              onOpenScript={navigateToScriptEdit}
              onOpenTest={navigateToTest}
              onOpenCommandCenter={navigateToCommandCenter}
              onCreateTestFromScenario={navigateToNewTestForScenario}
            />
          )}
          {view.page === 'access' && (
            <AccessPage
              projects={projects}
              domains={domains}
              applicationRepository={applicationRepository}
              activeDomain={activeDomain}
              activeProject={activeProject}
              canManageUserLifecycle={canManageUserLifecycle}
              canManageUserBindings={canManageUserBindings}
              canViewUserActivity={canViewUserActivity}
              canManageServiceAccounts={canManageServiceAccounts}
            />
          )}
          {view.page === 'settings' && (
            <SettingsPage
              theme={theme}
              onThemeChange={setTheme}
              authStatus={authStatus}
              onNavigate={handleNavigate}
              domains={domains}
              activeDomain={activeDomain}
              onDomainChange={setActiveDomain}
              onDomainsRefresh={refreshDomains}
            />
          )}
        </Suspense>
      </main>
    </div>
  )
}

export default function App() {
  useEffect(() => {
    if (typeof window === 'undefined') return
    const isWails = window.location.protocol === 'wails:' || window.location.hostname === 'wails.localhost'
    const isMac = navigator.userAgent.toLowerCase().includes('mac')
    if (isWails) {
      document.body.classList.add('is-desktop')
      if (isMac) {
        document.body.classList.add('is-mac')
      }
    }
  }, [])

  return (
    <DevViewProvider>
      <AppContent />
    </DevViewProvider>
  )
}

function AppContent() {
  const authState = useAuthStatus()

  return (
    <AuthGate state={authState}>
      <ProjectProvider>
        <ApplicationProvider>
          <KeyboardShortcutProvider>
            <TopbarContributionProvider>
              <NavigationGuardProvider>
                <TablePaginationManager />
                <AppShell authStatus={authState.status === 'ready' ? authState.auth : undefined} />
              </NavigationGuardProvider>
            </TopbarContributionProvider>
          </KeyboardShortcutProvider>
        </ApplicationProvider>
      </ProjectProvider>
    </AuthGate>
  )
}
