import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useDashboardTheme } from './hooks/useDashboardTheme'
import { useDashboardZoomShortcuts } from './hooks/useDashboardZoomShortcuts'
import { useWebSocket } from './hooks/useWebSocket'
import { ApplicationProvider } from './hooks/ApplicationProvider'
import { useApplication } from './hooks/useApplication'
import { MetricsHistoryPoint, TelemetryDiagnostic, TelemetryDiagnosticEntry, WSMessage } from './types'
import Sidebar, { type NavPage } from './components/Sidebar'
import DashboardPage from './components/DashboardPage'
import TablePaginationManager from './components/TablePaginationManager'
import { type AppView, parseAppViewFromHash, viewToLocationHash } from './appRouting'
import { appendTelemetryDiagnostic } from './components/monitoringTelemetryDiagnostics'
import AuthGate from './components/AuthGate'
import { useAuthStatus } from './hooks/useAuthStatus'
import { ProjectProvider } from './hooks/ProjectProvider'
import { useProject } from './hooks/useProject'
import { DevViewProvider, useDevView } from './hooks/DevViewProvider'
import { useSystemApi } from './hooks/api/useSystemApi'
import { useRepositoryApi } from './hooks/api/useRepositoryApi'
import { useLibraryApi } from './hooks/api/useLibraryApi'
import { isEffectiveOperationAllowed } from './utils/authAccessUtils'
import { DesktopStatusResponse } from './types/library'
import ThemeToggle from './components/ThemeToggle'
import './App.css'
import './components/Pages.css'

const ScriptsPage = lazy(() => import('./components/ScriptsPage'))
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
  const [effectiveAccessResources, setEffectiveAccessResources] = useState<import('./types').DashboardAuthEffectiveAccessResource[]>([])
  const [domains, setDomains] = useState<import('./types').Domain[]>([])
  const [activeDomain, setActiveDomainState] = useState<import('./types').Domain | null>(null)
  const [applicationRepository, setApplicationRepository] = useState<import('./types').ApplicationRepository | null>(null)
  const [repositoryCategories, setRepositoryCategories] = useState<import('./types').RepositoryCategory[]>([])
  const [desktopStatus, setDesktopStatus] = useState<DesktopStatusResponse | null>(null)

  const handleWSMessage = useCallback((msg: WSMessage) => {
    if (msg.type === 'metrics_update' && msg.data && msg.test_id && msg.timestamp) {
      const testId = msg.test_id
      const point: MetricsHistoryPoint = {
        time: new Date(msg.timestamp).toLocaleTimeString(),
        timestampMs: Date.parse(msg.timestamp),
        vusers: msg.data.active_vusers,
        tps: Math.round(msg.data.tps * 10) / 10,
        avgMs: Math.round(msg.data.avg_response_ms * 10) / 10,
        errorPct: Math.round(msg.data.error_pct * 100) / 100,
        p50: Math.round(msg.data.p50_ms * 10) / 10,
        p90: Math.round(msg.data.p90_ms * 10) / 10,
        p99: Math.round(msg.data.p99_ms * 10) / 10,
      }
      if (msg.data.groups) {
        for (const [name, gm] of Object.entries(msg.data.groups)) {
          point[`${name}_tps`] = Math.round(gm.tps * 10) / 10
          point[`${name}_avgMs`] = Math.round(gm.avg_response_ms * 10) / 10
          point[`${name}_vusers`] = gm.vusers
        }
      }
      if (msg.data.resources) {
        const r = msg.data.resources
        point.cpuProcess = Math.round(r.cpu_process_pct * 10) / 10
        point.cpuFree = Math.round(r.cpu_free_pct * 10) / 10
        point.memProcess = Math.round(r.mem_process_mb * 10) / 10
        point.memFree = Math.round(r.mem_free_mb * 10) / 10
        point.netSent = Math.round(r.net_sent_kbps * 10) / 10
        point.netRecv = Math.round(r.net_recv_kbps * 10) / 10
        point.diskRead = Math.round(r.disk_read_kbps * 10) / 10
        point.diskWrite = Math.round(r.disk_write_kbps * 10) / 10
      }
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
  const canManageUsers = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_users', false)
  const canViewUserActivity = isEffectiveOperationAllowed(effectiveAccessResources, 'view_user_activity', false)
  const canUseAccessWorkspace = canManageServiceAccounts || canManageUsers || canViewUserActivity
  const canManageRepositoryProjects = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_repository_projects', false)
  const canManageRepositoryReleases = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_repository_releases', false)
  const canManageRepository = isEffectiveOperationAllowed(effectiveAccessResources, 'manage_application_repository', false)

  const navigateToTest = (id: string) => setView({ page: 'test', testId: id })
  const activePage: NavPage = view.page === 'test' ? 'tests' : view.page === 'swagger' || view.page === 'repository-guide' ? 'docs' : view.page

  const navigateToScriptEdit = (scriptId: string) => setView({ page: 'scripts', mode: 'edit', scriptId })
  const navigateToScenarioBuilder = () => setView({ page: 'tests', scenarioView: 'builder' })
  const navigateToNewTestForScenario = (scenarioId: string) => setView({ page: 'new-test', scenarioId })

  const handleNavigate = (page: NavPage) => {
    if (page === 'scripts') {
      setScriptsNavigationVersion(version => version + 1)
    }
    setView({ page })
  }

  const appHistoryDepth = useRef(0)
  useEffect(() => {
    appHistoryDepth.current += 1
  }, [view])

  const navigateBack = useCallback((fallbackPage: NavPage = 'dashboard') => {
    if (appHistoryDepth.current > 1) {
      window.history.back()
    } else {
      setView({ page: fallbackPage })
    }
  }, [])

  useEffect(() => {
    const handleGlobalKeyDown = (event: KeyboardEvent) => {
      if (
        event.target instanceof HTMLInputElement ||
        event.target instanceof HTMLTextAreaElement ||
        (event.target as HTMLElement).isContentEditable ||
        event.defaultPrevented
      ) {
        return
      }
      if (event.key === 'Backspace') {
        event.preventDefault()
        navigateBack('dashboard')
      }
    }
    window.addEventListener('keydown', handleGlobalKeyDown)
    return () => window.removeEventListener('keydown', handleGlobalKeyDown)
  }, [navigateBack])

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
    let cancelled = false
    const loadDomains = async () => {
      try {
        const response = await repositoryApi.listDomains()
        if (cancelled) return
        const next = response.domains || []
        setDomains(next)
        const storedId = typeof window !== 'undefined' ? window.localStorage.getItem('apexload.activeDomainId') : ''
        setActiveDomainState(prev => next.find(domain => domain.id === storedId) || next.find(domain => domain.id === prev?.id) || next[0] || null)
      } catch {
        if (!cancelled) {
          const fallback: import('./types').Domain = {
            id: 'default',
            name: 'Default Domain',
            created_at: new Date(0).toISOString(),
            updated_at: new Date(0).toISOString(),
          }
          setDomains([fallback])
          setActiveDomainState(fallback)
        }
      }
    }
    loadDomains()
    return () => {
      cancelled = true
    }
  }, [repositoryApi])

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
      <main className="main-content">
        <div className="main-topbar" aria-label="Dashboard controls">
          <div className="main-topbar-context">
            <span className="topbar-domain">{activeDomain?.name || 'APEX Load'}</span>
            <span className="topbar-project">{activeProject?.name || 'No project selected'}</span>
          </div>
          <div className="main-topbar-actions">
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
            <ThemeToggle theme={theme} onThemeChange={setTheme} />
          </div>
        </div>
        <Suspense fallback={<div className="page">Loading page...</div>}>
          {view.page === 'dashboard' && (
            <DashboardPage
              testStatuses={testStatuses}
              metricsHistory={metricsHistory}
              connectionState={connectionState}
              onSelectTest={navigateToTest}
              onNavigate={handleNavigate}
            />
          )}
          {view.page === 'tests' && (
            <TestHub
              initialView={view.scenarioView}
              testStatuses={testStatuses}
              onSelectTest={navigateToTest}
              onCreateTestFromScenario={navigateToNewTestForScenario}
              onNavigate={handleNavigate}
            />
          )}
          {view.page === 'new-test' && (
            <NewTestPage
              activeApplication={activeApplication}
              activeRelease={activeRelease}
              releases={releases}
              initialScenarioId={'scenarioId' in view ? view.scenarioId : undefined}
              onSelectTest={navigateToTest}
              onCreateRelease={createAndSelectRelease}
              onOpenScenarioBuilder={navigateToScenarioBuilder}
            />
          )}
          {view.page === 'test' && (
            <TestExecution
              testId={view.testId}
              history={metricsHistory[view.testId] || []}
              onBack={() => navigateBack('tests')}
              onEditScript={navigateToScriptEdit}
              onSelectTest={navigateToTest}
            />
          )}
          {view.page === 'scripts' && (
            <ScriptsPage
              key={`scripts-${scriptsNavigationVersion}-${scriptMode || 'list'}-${scriptEditId || ''}`}
              initialMode={scriptMode}
              initialEditId={scriptEditId}
              onSelectScenario={navigateToTest}
              onNavigate={handleNavigate}
            />
          )}
          {view.page === 'results' && (
            <ResultsPage
              testStatuses={testStatuses}
              onSelectTest={navigateToTest}
              onNavigate={handleNavigate}
              activeApplication={activeApplication}
              activeRelease={activeRelease}
            />
          )}
          {view.page === 'monitoring' && <MonitoringPage connectionState={connectionState} telemetryDiagnostics={telemetryDiagnostics} onSelectTest={navigateToTest} />}
          {view.page === 'docs' && <ApiGuidePage onNavigate={handleNavigate} effectiveAccessResources={effectiveAccessResources} />}
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
              onCreateTestFromScenario={navigateToNewTestForScenario}
            />
          )}
          {view.page === 'access' && (
            <AccessPage
              projects={projects}
              domains={domains}
              activeDomain={activeDomain}
              activeProject={activeProject}
              canManageUsers={canManageUsers}
              canViewUserActivity={canViewUserActivity}
              canManageServiceAccounts={canManageServiceAccounts}
            />
          )}
          {view.page === 'settings' && <SettingsPage theme={theme} onThemeChange={setTheme} authStatus={authStatus} onNavigate={handleNavigate} />}
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
          <TablePaginationManager />
          <AppShell authStatus={authState.status === 'ready' ? authState.auth : undefined} />
        </ApplicationProvider>
      </ProjectProvider>
    </AuthGate>
  )
}
