import { useState, useCallback } from 'react'
import { useWebSocket } from './hooks/useWebSocket'
import { WSMessage, MetricsHistoryPoint } from './types'
import Sidebar, { NavPage } from './components/Sidebar'
import TestHub from './components/TestHub'
import TestExecution from './components/TestExecution'
import ScriptsPage from './components/ScriptsPage'
import ResultsPage from './components/ResultsPage'
import MonitoringPage from './components/MonitoringPage'
import SettingsPage from './components/SettingsPage'
import './App.css'
import './components/Pages.css'

type View =
  | { page: NavPage }
  | { page: 'test'; testId: string }

export default function App() {
  const [view, setView] = useState<View>({ page: 'dashboard' })
  const [metricsHistory, setMetricsHistory] = useState<Record<string, MetricsHistoryPoint[]>>({})
  const [testStatuses, setTestStatuses] = useState<Record<string, string>>({})

  const handleWSMessage = useCallback((msg: WSMessage) => {
    if (msg.type === 'metrics_update' && msg.data) {
      const point: MetricsHistoryPoint = {
        time: new Date(msg.timestamp).toLocaleTimeString(),
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
        const existing = prev[msg.test_id] || []
        const updated = [...existing, point].slice(-300)
        return { ...prev, [msg.test_id]: updated }
      })
    }

    if (msg.status) {
      setTestStatuses((prev) => ({ ...prev, [msg.test_id]: msg.status! }))
    }
  }, [])

  const { connected } = useWebSocket({ onMessage: handleWSMessage })

  const navigateToTest = (id: string) => setView({ page: 'test', testId: id })
  const activePage = view.page === 'test' ? 'tests' : view.page

  const handleNavigate = (page: NavPage) => {
    setView({ page })
  }

  return (
    <div className="app-layout">
      <Sidebar
        active={activePage}
        onNavigate={handleNavigate}
        connected={connected}
      />
      <main className="main-content">
        {view.page === 'dashboard' && (
          <TestHub
            testStatuses={testStatuses}
            onSelectTest={navigateToTest}
          />
        )}
        {view.page === 'tests' && (
          <TestHub
            testStatuses={testStatuses}
            onSelectTest={navigateToTest}
          />
        )}
        {view.page === 'test' && (
          <TestExecution
            testId={view.testId}
            history={metricsHistory[view.testId] || []}
            onBack={() => setView({ page: 'tests' })}
          />
        )}
        {view.page === 'scripts' && <ScriptsPage />}
        {view.page === 'results' && (
          <ResultsPage
            testStatuses={testStatuses}
            onSelectTest={navigateToTest}
          />
        )}
        {view.page === 'monitoring' && <MonitoringPage connected={connected} />}
        {view.page === 'settings' && <SettingsPage />}
      </main>
    </div>
  )
}
