import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import StatusBadge from './StatusBadge'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

export default function ResultsPage({ testStatuses, onSelectTest }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])

  const refresh = useCallback(async () => {
    try {
      const res = await api.listTests()
      setTests(res.tests || [])
    } catch {
      // API not available
    }
  }, [api])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 10000)
    return () => clearInterval(interval)
  }, [refresh])

  const completedTests = tests.filter((t) => {
    const s = testStatuses[t.id] || t.status
    return s === 'COMPLETE' || s === 'FAILED' || s === 'ABORTED'
  })

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Results</h2>
          <p className="page-subtitle">Historical test results and performance reports</p>
        </div>
      </div>

      <div className="page-section">
        <h3 className="section-title">Completed Tests ({completedTests.length})</h3>
        {completedTests.length === 0 ? (
          <div className="empty-card">
            <div className="empty-icon">
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <line x1="18" y1="20" x2="18" y2="10" />
                <line x1="12" y1="20" x2="12" y2="4" />
                <line x1="6" y1="20" x2="6" y2="14" />
              </svg>
            </div>
            <p>No completed tests yet.</p>
            <p className="empty-hint">Run a test to see results and performance data here.</p>
          </div>
        ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th>Test Name</th>
                  <th>Status</th>
                  <th>VUsers</th>
                  <th>Groups</th>
                  <th>Duration</th>
                </tr>
              </thead>
              <tbody>
                {completedTests.map((test) => (
                  <tr key={test.id} className="clickable-row" onClick={() => onSelectTest(test.id)}>
                    <td className="cell-name">{test.name}</td>
                    <td><StatusBadge status={testStatuses[test.id] || test.status} /></td>
                    <td>{test.total_vusers}</td>
                    <td>{test.groups?.length || 1}</td>
                    <td className="cell-muted">{test.duration}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
