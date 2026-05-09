import { useState, useEffect, useCallback, useMemo, memo } from 'react'
import { useApi } from '../hooks/useApi'
import { TestSummary, TestStatus, StoredScript } from '../types'
import CreateTestModal from './CreateTestModal'
import StatusBadge from './StatusBadge'
import './TestHub.css'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

interface Scenario {
  name: string
  runs: TestSummary[]
  latestRun: TestSummary
  hasActive: boolean
  totalRuns: number
}

function resolveStatus(test: TestSummary, overrides: Record<string, string>): TestStatus {
  return (overrides[test.id] || test.status) as TestStatus
}

function formatDate(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function TestHub({ testStatuses, onSelectTest }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [showCreate, setShowCreate] = useState(false)
  const [expandedScenario, setExpandedScenario] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const [testRes, scriptRes] = await Promise.all([
        api.listTests(),
        api.listScripts(),
      ])
      setTests(testRes.tests || [])
      setScripts(scriptRes.scripts || [])
    } catch { /* API not available yet */ }
  }, [api])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 5000)
    return () => clearInterval(interval)
  }, [refresh])

  const scenarios = useMemo(() => {
    const map = new Map<string, TestSummary[]>()
    for (const test of tests) {
      const name = test.name
      if (!map.has(name)) map.set(name, [])
      map.get(name)!.push(test)
    }

    const result: Scenario[] = []
    for (const [name, runs] of map) {
      const sorted = [...runs].sort((a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
      )
      const hasActive = sorted.some(r => {
        const s = resolveStatus(r, testStatuses)
        return s === 'RUNNING' || s === 'INITIALIZING' || s === 'STOPPING'
      })
      result.push({
        name,
        runs: sorted,
        latestRun: sorted[0],
        hasActive,
        totalRuns: sorted.length,
      })
    }

    result.sort((a, b) => {
      if (a.hasActive && !b.hasActive) return -1
      if (!a.hasActive && b.hasActive) return 1
      return new Date(b.latestRun.created_at).getTime() - new Date(a.latestRun.created_at).getTime()
    })

    return result
  }, [tests, testStatuses])

  const toggleExpand = (name: string) => {
    setExpandedScenario(prev => prev === name ? null : name)
  }

  return (
    <div className="test-hub">
      <div className="hub-header">
        <div>
          <h2>Scenarios</h2>
          <p className="hub-subtitle">{scenarios.length} scenario{scenarios.length !== 1 ? 's' : ''} · {tests.length} total run{tests.length !== 1 ? 's' : ''}</p>
        </div>
        <button className="btn btn-primary" onClick={() => setShowCreate(true)}>
          New Scenario
        </button>
      </div>

      {scenarios.length === 0 ? (
        <div className="empty-state">
          No scenarios yet. Create one to get started.
        </div>
      ) : (
        <div className="scenario-list">
          {scenarios.map(scenario => {
            const isExpanded = expandedScenario === scenario.name
            const latest = scenario.latestRun
            const latestStatus = resolveStatus(latest, testStatuses)

            return (
              <div key={scenario.name} className={`scenario-item ${isExpanded ? 'expanded' : ''} ${scenario.hasActive ? 'has-active' : ''}`}>
                {/* Scenario row */}
                <div className="scenario-row" onClick={() => toggleExpand(scenario.name)}>
                  <button className={`expand-toggle ${isExpanded ? 'open' : ''}`} aria-label="Toggle runs">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="9 18 15 12 9 6" />
                    </svg>
                  </button>

                  <div className="scenario-info">
                    <span className="scenario-name">{scenario.name}</span>
                    <span className="scenario-meta">
                      {scenario.totalRuns} run{scenario.totalRuns !== 1 ? 's' : ''}
                      {' · '}{latest.total_vusers} VUsers
                      {' · '}{(latest.groups?.length || 1)} group{(latest.groups?.length || 1) > 1 ? 's' : ''}
                    </span>
                  </div>

                  {scenario.hasActive && (
                    <span className="scenario-live-badge">LIVE</span>
                  )}

                  <div className="scenario-latest">
                    <span className="scenario-latest-label">Latest</span>
                    <StatusBadge status={latestStatus} />
                  </div>

                  <span className="scenario-date">{formatDate(latest.created_at)}</span>

                  <button
                    className="btn btn-sm btn-secondary scenario-open-btn"
                    onClick={(e) => { e.stopPropagation(); onSelectTest(latest.id) }}
                  >
                    Open
                  </button>
                </div>

                {/* Expanded runs */}
                {isExpanded && (
                  <div className="runs-panel">
                    <div className="runs-header">
                      <span className="runs-col">Status</span>
                      <span className="runs-col">VUsers</span>
                      <span className="runs-col">Duration</span>
                      <span className="runs-col">Groups</span>
                      <span className="runs-col">Created</span>
                      <span className="runs-col"></span>
                    </div>
                    {scenario.runs.map(run => {
                      const runStatus = resolveStatus(run, testStatuses)
                      const isActive = runStatus === 'RUNNING' || runStatus === 'INITIALIZING'
                      return (
                        <div
                          key={run.id}
                          className={`run-row ${isActive ? 'active' : ''}`}
                          onClick={() => onSelectTest(run.id)}
                        >
                          <span className="runs-col">
                            <StatusBadge status={runStatus} />
                          </span>
                          <span className="runs-col run-value">{run.total_vusers}</span>
                          <span className="runs-col run-value">{run.duration}</span>
                          <span className="runs-col run-value">{run.groups?.length || 1}</span>
                          <span className="runs-col run-date">{formatDate(run.created_at)}</span>
                          <span className="runs-col run-actions">
                            {isActive && run.live_metrics && (
                              <span className="run-live-stats">
                                {run.live_metrics.tps?.toFixed(1)} TPS · {run.live_metrics.avg_response_ms?.toFixed(0)}ms
                              </span>
                            )}
                            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="run-arrow">
                              <polyline points="9 18 15 12 9 6" />
                            </svg>
                          </span>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

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
export default memo(TestHub)
