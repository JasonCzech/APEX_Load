import { useState, useCallback } from 'react'
import { useDashboardTheme } from './hooks/useDashboardTheme'
import { useWebSocket } from './hooks/useWebSocket'
import { ApplicationProvider, useApplication } from './hooks/useApplication'
import { MetricsHistoryPoint, TelemetryDiagnostic, TelemetryDiagnosticEntry, WSMessage } from './types'
import Sidebar, { NavPage } from './components/Sidebar'
import TestHub from './components/TestHub'
import TestExecution from './components/TestExecution'
import DashboardPage from './components/DashboardPage'
import ScriptsPage from './components/ScriptsPage'
import ResultsPage from './components/ResultsPage'
import NewTestPage from './components/NewTestPage'
import MonitoringPage from './components/MonitoringPage'
import SettingsPage from './components/SettingsPage'
import { appendTelemetryDiagnostic } from './components/monitoringTelemetryDiagnostics'
import './App.css'
import './components/Pages.css'

type View =
  | { page: NavPage }
  | { page: 'scripts'; mode?: 'list' | 'create' | 'edit' | 'import'; scriptId?: string }
  | { page: 'test'; testId: string }

function AppShell() {
  const [view, setView] = useState<View>({ page: 'dashboard' })
  const [metricsHistory, setMetricsHistory] = useState<Record<string, MetricsHistoryPoint[]>>({})
  const [testStatuses, setTestStatuses] = useState<Record<string, string>>({})
  const [telemetryDiagnostics, setTelemetryDiagnostics] = useState<TelemetryDiagnosticEntry[]>([])
  const { theme, setTheme } = useDashboardTheme()
  const scriptMode = view.page === 'scripts' && 'mode' in view ? view.mode : undefined
  const scriptEditId = view.page === 'scripts' && 'scriptId' in view ? view.scriptId : undefined

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

  const { connected, connectionState } = useWebSocket({ onMessage: handleWSMessage })
  const { applications, releases, activeApplication, activeRelease, setActiveApplication, setActiveRelease, createAndSelectApplication, createAndSelectRelease } = useApplication()

  const navigateToTest = (id: string) => setView({ page: 'test', testId: id })
  const activePage = view.page === 'test' ? 'tests' : view.page

  const navigateToScriptEdit = (scriptId: string) => setView({ page: 'scripts', mode: 'edit', scriptId })

  const handleNavigate = (page: NavPage) => {
    setView({ page })
  }

  return (
    <div className="app-layout">
      <Sidebar
        active={activePage}
        onNavigate={handleNavigate}
        connectionState={connectionState}
        applications={applications}
        releases={releases}
        activeApplication={activeApplication}
        activeRelease={activeRelease}
        onApplicationChange={setActiveApplication}
        onReleaseChange={setActiveRelease}
        onCreateApplication={createAndSelectApplication}
      />
      <main className="main-content">
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
            testStatuses={testStatuses}
            onSelectTest={navigateToTest}
          />
        )}
        {view.page === 'new-test' && (
          <NewTestPage
            activeApplication={activeApplication}
            activeRelease={activeRelease}
            releases={releases}
            onSelectTest={navigateToTest}
            onCreateRelease={createAndSelectRelease}
          />
        )}
        {view.page === 'test' && (
          <TestExecution
            testId={view.testId}
            history={metricsHistory[view.testId] || []}
            onBack={() => setView({ page: 'tests' })}
            onEditScript={navigateToScriptEdit}
            onSelectTest={navigateToTest}
          />
        )}
        {view.page === 'scripts' && <ScriptsPage initialMode={scriptMode} initialEditId={scriptEditId} onSelectScenario={navigateToTest} />}
        {view.page === 'results' && (
          <ResultsPage
            testStatuses={testStatuses}
            onSelectTest={navigateToTest}
            activeApplication={activeApplication}
            activeRelease={activeRelease}
          />
        )}
        {view.page === 'monitoring' && <MonitoringPage connectionState={connectionState} telemetryDiagnostics={telemetryDiagnostics} />}
        {view.page === 'settings' && <SettingsPage theme={theme} onThemeChange={setTheme} />}
      </main>
    </div>
  )
}

export default function App() {
  return (
    <ApplicationProvider>
      <AppShell />
    </ApplicationProvider>
  )
}
