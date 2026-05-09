import { useState, useCallback } from 'react'
import { useWebSocket } from './hooks/useWebSocket'
import { WSMessage, MetricsHistoryPoint } from './types'
import Header from './components/Header'
import TestHub from './components/TestHub'
import TestExecution from './components/TestExecution'
import './App.css'

type View = { page: 'hub' } | { page: 'test'; testId: string }

export default function App() {
  const [view, setView] = useState<View>({ page: 'hub' })
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
      // Inject per-group metrics as dynamic keys for chart rendering
      if (msg.data.groups) {
        for (const [name, gm] of Object.entries(msg.data.groups)) {
          point[`${name}_tps`] = Math.round(gm.tps * 10) / 10
          point[`${name}_avgMs`] = Math.round(gm.avg_response_ms * 10) / 10
          point[`${name}_vusers`] = gm.vusers
        }
      }
      setMetricsHistory((prev) => {
        const existing = prev[msg.test_id] || []
        const updated = [...existing, point].slice(-300) // keep 5 min at 1s intervals
        return { ...prev, [msg.test_id]: updated }
      })
    }

    if (msg.status) {
      setTestStatuses((prev) => ({ ...prev, [msg.test_id]: msg.status! }))
    }
  }, [])

  const { connected } = useWebSocket({ onMessage: handleWSMessage })

  return (
    <div className="app">
      <Header
        connected={connected}
        onNavigate={() => setView({ page: 'hub' })}
      />
      <main className="main">
        {view.page === 'hub' && (
          <TestHub
            testStatuses={testStatuses}
            onSelectTest={(id) => setView({ page: 'test', testId: id })}
          />
        )}
        {view.page === 'test' && (
          <TestExecution
            testId={view.testId}
            history={metricsHistory[view.testId] || []}
            onBack={() => setView({ page: 'hub' })}
          />
        )}
      </main>
    </div>
  )
}
