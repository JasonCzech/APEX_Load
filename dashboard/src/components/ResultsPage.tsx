import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { Application, Release, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import StatusBadge from './StatusBadge'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  activeApplication?: Application | null
  activeRelease?: Release | null
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function ResultsPage({ testStatuses, onSelectTest, activeApplication, activeRelease }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [error, setError] = useState('')
  const [sortField, setSortField] = useState<'name' | 'status' | 'total_vusers' | 'groups' | 'started' | 'duration' | 'archive'>('started')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')

  const refresh = useCallback(async () => {
    if (!activeApplication?.id || !activeRelease?.id) {
      setTests([])
      return
    }
    try {
      const res = await api.listTests({ application_id: activeApplication.id, release_id: activeRelease.id })
      setTests(res.tests || [])
    } catch (err: any) {
      setError(err.message || 'Unable to load results')
    }
  }, [api, activeApplication?.id, activeRelease?.id])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 10000)
    return () => clearInterval(interval)
  }, [refresh])

  useEffect(() => setSelectedIds(new Set()), [activeRelease?.id])

  const completed = useMemo(() => tests.filter(test => {
    const status = testStatuses[test.id] || test.status
    return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
  }), [tests, testStatuses])

  const sortedCompleted = useMemo(() => {
    return [...completed].sort((a, b) => {
      let aValue: any
      let bValue: any
      switch (sortField) {
        case 'status':
          aValue = (testStatuses[a.id] || a.status || '').toLowerCase()
          bValue = (testStatuses[b.id] || b.status || '').toLowerCase()
          break
        case 'total_vusers':
          aValue = a.total_vusers || 0
          bValue = b.total_vusers || 0
          break
        case 'groups':
          aValue = a.groups?.length || 1
          bValue = b.groups?.length || 1
          break
        case 'started':
          aValue = new Date(a.start_time || a.created_at || 0).getTime()
          bValue = new Date(b.start_time || b.created_at || 0).getTime()
          break
        case 'duration':
          aValue = a.duration || ''
          bValue = b.duration || ''
          break
        case 'archive':
          aValue = a.archive?.transaction_count || 0
          bValue = b.archive?.transaction_count || 0
          break
        case 'name':
        default:
          aValue = (a.name || '').toLowerCase()
          bValue = (b.name || '').toLowerCase()
      }
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [completed, sortField, sortDirection, testStatuses])

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else {
      setSortField(field)
      setSortDirection(field === 'started' || field === 'archive' ? 'desc' : 'asc')
    }
  }

  const toggleSelect = (id: string, event: React.MouseEvent) => {
    event.stopPropagation()
    const next = new Set(selectedIds)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setSelectedIds(next)
  }

  const deleteSelected = async () => {
    if (!confirm(`Delete ${selectedIds.size} selected results?`)) return
    await Promise.all(Array.from(selectedIds).map(id => api.deleteTest(id)))
    setSelectedIds(new Set())
    refresh()
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Results</h2>
          <p className="page-subtitle">
            {activeApplication?.name || 'Application'} / {activeRelease?.name || 'Select a release'}
          </p>
        </div>
        {selectedIds.size > 0 && (
          <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} onClick={deleteSelected}>
            Delete Selected ({selectedIds.size})
          </button>
        )}
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      <div className="page-section">
        <h3 className="section-title">Release Results ({completed.length})</h3>
        <div className="data-table">
          <table>
            <thead>
              <tr>
                <th style={{ width: 40 }}></th>
                <th onClick={() => handleSort('name')} className="sortable">Scenario</th>
                <th onClick={() => handleSort('status')} className="sortable">Status</th>
                <th onClick={() => handleSort('total_vusers')} className="sortable">VUsers</th>
                <th onClick={() => handleSort('groups')} className="sortable">Groups</th>
                <th onClick={() => handleSort('started')} className="sortable">Started</th>
                <th onClick={() => handleSort('duration')} className="sortable">Duration</th>
                <th onClick={() => handleSort('archive')} className="sortable">Archive</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedCompleted.map(test => (
                <tr key={test.id} className="clickable-row" onClick={() => onSelectTest(test.id)}>
                  <td><input type="checkbox" checked={selectedIds.has(test.id)} onClick={(event) => event.stopPropagation()} onChange={(event) => toggleSelect(test.id, event as any)} /></td>
                  <td className="cell-name">{test.name}</td>
                  <td><StatusBadge status={(testStatuses[test.id] || test.status) as any} /></td>
                  <td>{test.total_vusers}</td>
                  <td>{test.groups?.length || 1}</td>
                  <td className="cell-muted">{formatDate(test.start_time || test.created_at)}</td>
                  <td className="cell-muted">{test.duration}</td>
                  <td className="cell-muted">{test.archive ? `${test.archive.transaction_count} tx` : 'Pending'}</td>
                  <td style={{ textAlign: 'right' }}><button className="btn btn-sm btn-secondary" onClick={(event) => { event.stopPropagation(); onSelectTest(test.id) }}>Open</button></td>
                </tr>
              ))}
              {sortedCompleted.length === 0 && (
                <tr><td colSpan={9} className="cell-muted">No completed results for this release yet.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export default memo(ResultsPage)
