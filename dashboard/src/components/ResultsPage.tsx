import { useState, useEffect, useCallback, memo } from 'react'
import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import StatusBadge from './StatusBadge'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

function ResultsPage({ testStatuses, onSelectTest }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [sortField, setSortField] = useState<string>('start_time')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())

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

  const handleSort = (field: string) => {
    if (sortField === field) {
      setSortDirection(sortDirection === 'asc' ? 'desc' : 'asc')
    } else {
      setSortField(field)
      setSortDirection('asc')
    }
  }

  const sortedResults = [...completedTests].sort((a: any, b: any) => {
    let vA = a[sortField]
    let vB = b[sortField]
    if (sortField === 'start_time') {
      vA = new Date(vA).getTime()
      vB = new Date(vB).getTime()
    }
    if (sortField === 'status') {
      vA = testStatuses[a.id] || a.status
      vB = testStatuses[b.id] || b.status
    }
    if (vA < vB) return sortDirection === 'asc' ? -1 : 1
    if (vA > vB) return sortDirection === 'asc' ? 1 : -1
    return 0
  })

  const SortIcon = ({ field }: { field: string }) => {
    if (sortField !== field) return <span className="sort-icon">↕</span>
    return <span className="sort-icon active">{sortDirection === 'asc' ? '↑' : '↓'}</span>
  }

  const toggleSelectAll = () => {
    if (selectedIds.size === completedTests.length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(completedTests.map((t) => t.id)))
    }
  }

  const toggleSelect = (id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    const next = new Set(selectedIds)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setSelectedIds(next)
  }

  const handleDelete = async (id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    if (!confirm('Delete this scenario result?')) return
    try {
      await api.deleteTest(id)
      refresh()
      const next = new Set(selectedIds)
      next.delete(id)
      setSelectedIds(next)
    } catch { /* ignore */ }
  }

  const handleDeleteSelected = async () => {
    if (!confirm(`Delete ${selectedIds.size} selected results?`)) return
    try {
      await Promise.all(Array.from(selectedIds).map((id) => api.deleteTest(id)))
      refresh()
      setSelectedIds(new Set())
    } catch { /* ignore */ }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Results</h2>
          <p className="page-subtitle">Historical scenario results and performance reports</p>
        </div>
        {selectedIds.size > 0 && (
          <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} onClick={handleDeleteSelected}>
            Delete Selected ({selectedIds.size})
          </button>
        )}
      </div>

      <div className="page-section">
        <h3 className="section-title">Completed Scenarios ({completedTests.length})</h3>
        {completedTests.length === 0 ? (
          <div className="empty-card">
            <div className="empty-icon">
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <line x1="18" y1="20" x2="18" y2="10" />
                <line x1="12" y1="20" x2="12" y2="4" />
                <line x1="6" y1="20" x2="6" y2="14" />
              </svg>
            </div>
            <p>No completed scenarios yet.</p>
            <p className="empty-hint">Run a scenario to see results and performance data here.</p>
          </div>
        ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th style={{ width: 40 }}>
                    <input
                      type="checkbox"
                      checked={completedTests.length > 0 && selectedIds.size === completedTests.length}
                      onChange={toggleSelectAll}
                    />
                  </th>
                  <th onClick={() => handleSort('name')} className="sortable">Scenario Name <SortIcon field="name" /></th>
                  <th onClick={() => handleSort('status')} className="sortable">Status <SortIcon field="status" /></th>
                  <th onClick={() => handleSort('total_vusers')} className="sortable">VUsers <SortIcon field="total_vusers" /></th>
                  <th>Groups</th>
                  <th onClick={() => handleSort('start_time')} className="sortable">Date <SortIcon field="start_time" /></th>
                  <th>Duration</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {sortedResults.map((test) => (
                  <tr
                    key={test.id}
                    className={`clickable-row ${selectedIds.has(test.id) ? 'selected' : ''}`}
                    onClick={() => onSelectTest(test.id)}
                  >
                    <td>
                      <input
                        type="checkbox"
                        checked={selectedIds.has(test.id)}
                        onClick={(e) => e.stopPropagation()}
                        onChange={(e) => toggleSelect(test.id, e as any)}
                      />
                    </td>
                    <td className="cell-name">{test.name}</td>
                    <td><StatusBadge status={testStatuses[test.id] || test.status} /></td>
                    <td>{test.total_vusers}</td>
                    <td>{test.groups?.length || 1}</td>
                    <td className="cell-muted">{new Date(test.start_time || test.created_at).toLocaleDateString()}</td>
                    <td className="cell-muted">{test.duration}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(e) => handleDelete(test.id, e)}>Delete</button>
                    </td>
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
export default memo(ResultsPage)
