import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../hooks/useApi'
import { TestSummary, StoredScript } from '../types'
import CreateTestModal from './CreateTestModal'
import StatusBadge from './StatusBadge'
import './TestHub.css'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

export default function TestHub({ testStatuses, onSelectTest }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [showCreate, setShowCreate] = useState(false)
  const [health, setHealth] = useState<any>(null)

  const refresh = useCallback(async () => {
    try {
      const [testRes, scriptRes, healthRes] = await Promise.all([
        api.listTests(),
        api.listScripts(),
        api.getHealth(),
      ])
      setTests(testRes.tests || [])
      setScripts(scriptRes.scripts || [])
      setHealth(healthRes)
    } catch {
      // API not available yet
    }
  }, [api])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 5000)
    return () => clearInterval(interval)
  }, [refresh])

  const activeTests = tests.filter((t) => {
    const status = testStatuses[t.id] || t.status
    return status === 'RUNNING' || status === 'INITIALIZING'
  })
  const queuedTests = tests.filter((t) => (testStatuses[t.id] || t.status) === 'QUEUED')
  const recentTests = tests
    .filter((t) => {
      const s = testStatuses[t.id] || t.status
      return s === 'COMPLETE' || s === 'FAILED' || s === 'ABORTED'
    })
    .slice(0, 20)

  return (
    <div className="test-hub">
      <div className="hub-header">
        <div>
          <h2>Test Hub</h2>
          <p className="hub-subtitle">
            {health ? `v${health.version} · ${health.ws_clients} connected` : 'Connecting...'}
          </p>
        </div>
        <button className="btn btn-primary" onClick={() => setShowCreate(true)}>
          New Test
        </button>
      </div>

      {activeTests.length > 0 && (
        <section className="hub-section">
          <h3 className="section-title">Active Tests</h3>
          <div className="test-grid">
            {activeTests.map((test) => (
              <div key={test.id} className="test-card active" onClick={() => onSelectTest(test.id)}>
                <div className="card-header">
                  <span className="card-name">{test.name}</span>
                  <StatusBadge status={testStatuses[test.id] || test.status} />
                </div>
                <div className="card-stats">
                  <div className="stat">
                    <span className="stat-value">{test.live_metrics?.active_vusers ?? test.total_vusers}</span>
                    <span className="stat-label">VUsers</span>
                  </div>
                  <div className="stat">
                    <span className="stat-value">{test.live_metrics?.tps?.toFixed(1) ?? '—'}</span>
                    <span className="stat-label">TPS</span>
                  </div>
                  <div className="stat">
                    <span className="stat-value">
                      {test.live_metrics?.avg_response_ms?.toFixed(0) ?? '—'}
                    </span>
                    <span className="stat-label">Avg ms</span>
                  </div>
                  <div className="stat">
                    <span className="stat-value">{test.live_metrics?.error_pct?.toFixed(2) ?? '0'}%</span>
                    <span className="stat-label">Errors</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {queuedTests.length > 0 && (
        <section className="hub-section">
          <h3 className="section-title">Queued</h3>
          <div className="test-list">
            {queuedTests.map((test) => (
              <div key={test.id} className="test-row" onClick={() => onSelectTest(test.id)}>
                <span className="row-name">{test.name}</span>
                <span className="row-meta">{test.total_vusers} VUsers ({test.groups?.length || 1} group{(test.groups?.length || 1) > 1 ? 's' : ''}) · {test.duration}</span>
                <StatusBadge status="QUEUED" />
              </div>
            ))}
          </div>
        </section>
      )}

      <section className="hub-section">
        <h3 className="section-title">Recent Results</h3>
        {recentTests.length === 0 ? (
          <p className="empty-state">No completed tests yet. Create and run a test to see results here.</p>
        ) : (
          <div className="test-list">
            {recentTests.map((test) => (
              <div key={test.id} className="test-row" onClick={() => onSelectTest(test.id)}>
                <span className="row-name">{test.name}</span>
                <span className="row-meta">
                  {test.total_vusers} VUsers ({test.groups?.length || 1} group{(test.groups?.length || 1) > 1 ? 's' : ''}) · {test.duration}
                </span>
                <StatusBadge status={testStatuses[test.id] || test.status} />
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="hub-section">
        <h3 className="section-title">Scripts ({scripts.length})</h3>
        {scripts.length === 0 ? (
          <p className="empty-state">No scripts uploaded. Use the API to upload a script first.</p>
        ) : (
          <div className="test-list">
            {scripts.map((s) => (
              <div key={s.id} className="test-row">
                <span className="row-name">{s.name}</span>
                <span className="row-meta">{s.protocol} · v{s.version}</span>
                <span className="script-id">{s.id.slice(0, 8)}</span>
              </div>
            ))}
          </div>
        )}
      </section>

      {showCreate && (
        <CreateTestModal
          scripts={scripts}
          onClose={() => setShowCreate(false)}
          onCreated={(test) => {
            setShowCreate(false)
            refresh()
            onSelectTest(test.id)
          }}
        />
      )}
    </div>
  )
}
