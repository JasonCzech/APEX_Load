import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { downloadBlob } from '../utils/fileUtils'
import { Application, IntervalSummaryRecord, Release, RunArchiveReport, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import StatusBadge from './StatusBadge'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  activeApplication?: Application | null
  activeRelease?: Release | null
}

type SortField = 'name' | 'status' | 'total_vusers' | 'groups' | 'started' | 'duration' | 'archive'
type StatusFilter = 'all' | 'complete' | 'failed' | 'aborted'
type ArchiveFilter = 'all' | 'ready' | 'pending'

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Never'
}

function formatShortDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function resolveStatus(test: TestSummary, testStatuses: Record<string, string>) {
  return testStatuses[test.id] || test.status
}

function isCompletedStatus(status?: string) {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

function SortIcon({ active, direction }: { active: boolean; direction: 'asc' | 'desc' }) {
  if (!active) return <span className="sort-icon">↕</span>
  return <span className="sort-icon active">{direction === 'asc' ? '↑' : '↓'}</span>
}

function ResultsPage({ testStatuses, onSelectTest, activeApplication, activeRelease }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [comparisonReports, setComparisonReports] = useState<Record<string, RunArchiveReport>>({})
  const [comparisonSummaries, setComparisonSummaries] = useState<Record<string, IntervalSummaryRecord[]>>({})
  const [comparisonLoading, setComparisonLoading] = useState(false)
  const [comparisonError, setComparisonError] = useState('')
  const [error, setError] = useState('')
  const [sortField, setSortField] = useState<SortField>('started')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [archiveFilter, setArchiveFilter] = useState<ArchiveFilter>('all')

  const refresh = useCallback(async () => {
    if (!activeApplication?.id || !activeRelease?.id) {
      setTests([])
      setError('')
      return
    }
    try {
      const res = await api.listTests({ application_id: activeApplication.id, release_id: activeRelease.id })
      setTests(res.tests || [])
      setError('')
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Unable to load results'))
    }
  }, [api, activeApplication?.id, activeRelease?.id])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 10000)
    return () => clearInterval(interval)
  }, [refresh])

  useEffect(() => {
    setSelectedIds(new Set())
  }, [activeRelease?.id])

  const completed = useMemo(
    () =>
      tests.filter(test => {
        const status = resolveStatus(test, testStatuses)
        return isCompletedStatus(status)
      }),
    [tests, testStatuses],
  )

  const summary = useMemo(() => {
    const complete = completed.filter(test => resolveStatus(test, testStatuses) === 'COMPLETE').length
    const failed = completed.filter(test => resolveStatus(test, testStatuses) === 'FAILED').length
    const aborted = completed.filter(test => resolveStatus(test, testStatuses) === 'ABORTED').length
    const archivesReady = completed.filter(test => Boolean(test.archive)).length
    const latestRun = [...completed]
      .sort((a, b) => new Date(b.start_time || b.created_at || 0).getTime() - new Date(a.start_time || a.created_at || 0).getTime())[0]
    return { complete, failed, aborted, archivesReady, latestRun }
  }, [completed, testStatuses])

  const filteredCompleted = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase()
    return completed.filter(test => {
      const status = resolveStatus(test, testStatuses)
      if (statusFilter !== 'all' && status.toLowerCase() !== statusFilter) return false
      if (archiveFilter === 'ready' && !test.archive) return false
      if (archiveFilter === 'pending' && test.archive) return false
      if (!normalizedSearch) return true
      const haystack = [
        test.name,
        test.duration,
        status,
        String(test.total_vusers),
        ...(test.groups || []).map(group => group.name || group.script_name || group.script_id),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return haystack.includes(normalizedSearch)
    })
  }, [archiveFilter, completed, search, statusFilter, testStatuses])

  const sortedCompleted = useMemo(() => {
    return [...filteredCompleted].sort((a, b) => {
      let aValue: string | number = ''
      let bValue: string | number = ''
      switch (sortField) {
        case 'status':
          aValue = resolveStatus(a, testStatuses).toLowerCase()
          bValue = resolveStatus(b, testStatuses).toLowerCase()
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
  }, [filteredCompleted, sortDirection, sortField, testStatuses])

  const selectedTests = useMemo(
    () => sortedCompleted.filter(test => selectedIds.has(test.id)),
    [selectedIds, sortedCompleted],
  )

  const selectionState = useMemo(() => {
    const archivedSelected = selectedTests.filter(test => Boolean(test.archive)).length
    if (archivedSelected >= 2) return `Compare ready: ${archivedSelected} archived runs selected`
    if (archivedSelected === 1) return 'Select one more archived run to compare'
    if (selectedTests.length > 0) return 'Archive comparison requires archived runs'
    return ''
  }, [selectedTests])

  const comparableTests = useMemo(
    () => selectedTests.filter(test => test.archive).slice(0, 2),
    [selectedTests],
  )

  useEffect(() => {
    if (comparableTests.length !== 2) {
      setComparisonReports({})
      setComparisonSummaries({})
      setComparisonLoading(false)
      setComparisonError('')
      return
    }

    let cancelled = false
    const loadComparison = async () => {
      setComparisonLoading(true)
      setComparisonError('')
      try {
        const reports = await Promise.all(
          comparableTests.map(async test => [test.id, (await api.getTestArchiveReport(test.id)) as RunArchiveReport] as const),
        )
        const summaries = await Promise.all(
          comparableTests.map(async test => [test.id, ((await api.getTestArchiveSummaries(test.id, { limit: 120, offset: 0 })).summaries || []) as IntervalSummaryRecord[]] as const),
        )
        if (!cancelled) {
          setComparisonReports(Object.fromEntries(reports))
          setComparisonSummaries(Object.fromEntries(summaries))
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setComparisonReports({})
          setComparisonSummaries({})
          setComparisonError(getErrorMessage(error, 'Unable to load comparison reports'))
        }
      } finally {
        if (!cancelled) setComparisonLoading(false)
      }
    }

    loadComparison()
    return () => {
      cancelled = true
    }
  }, [api, comparableTests])

  const comparisonCards = useMemo(() => {
    if (comparableTests.length !== 2) return []
    return comparableTests.map(test => ({ test, report: comparisonReports[test.id] }))
  }, [comparableTests, comparisonReports])

  const comparisonActionRows = useMemo(() => {
    if (comparisonCards.length !== 2 || !comparisonCards[0].report || !comparisonCards[1].report) return []
    const [left, right] = comparisonCards
    const actionNames = new Set<string>([
      ...Object.keys(left.report.by_action || {}),
      ...Object.keys(right.report.by_action || {}),
    ])
    return Array.from(actionNames)
      .map(name => ({
        name,
        left: left.report?.by_action?.[name],
        right: right.report?.by_action?.[name],
        deltaMs: (right.report?.by_action?.[name]?.avg_response_ms || 0) - (left.report?.by_action?.[name]?.avg_response_ms || 0),
      }))
      .sort((a, b) => Math.abs(b.deltaMs) - Math.abs(a.deltaMs))
      .slice(0, 8)
  }, [comparisonCards])

  const comparisonChartData = useMemo(() => {
    if (comparisonCards.length !== 2 || !comparisonCards[0].report || !comparisonCards[1].report) return []
    const [left, right] = comparisonCards
    const leftSeries = comparisonSummaries[left.test.id] || []
    const rightSeries = comparisonSummaries[right.test.id] || []
    const maxLength = Math.max(leftSeries.length, rightSeries.length)
    return Array.from({ length: maxLength }, (_, index) => ({
      sample: index + 1,
      leftTps: leftSeries[index]?.tps,
      rightTps: rightSeries[index]?.tps,
      leftP95: leftSeries[index]?.p95_ms,
      rightP95: rightSeries[index]?.p95_ms,
    }))
  }, [comparisonCards, comparisonSummaries])

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDirection(prev => (prev === 'asc' ? 'desc' : 'asc'))
      return
    }
    setSortField(field)
    setSortDirection(field === 'started' || field === 'archive' ? 'desc' : 'asc')
  }

  const toggleSelectAll = () => {
    if (selectedIds.size === sortedCompleted.length) {
      setSelectedIds(new Set())
      return
    }
    setSelectedIds(new Set(sortedCompleted.map(test => test.id)))
  }

  const toggleSelect = (id: string, event: React.MouseEvent | React.ChangeEvent) => {
    event.stopPropagation()
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const handleDelete = async (id: string, event: React.MouseEvent) => {
    event.stopPropagation()
    if (!confirm('Delete this scenario result?')) return
    await api.deleteTest(id)
    setSelectedIds(prev => {
      const next = new Set(prev)
      next.delete(id)
      return next
    })
    await refresh()
  }

  const deleteSelected = async () => {
    if (!confirm(`Delete ${selectedIds.size} selected results?`)) return
    await Promise.all(Array.from(selectedIds).map(id => api.deleteTest(id)))
    setSelectedIds(new Set())
    await refresh()
  }

  const downloadArchive = async (test: TestSummary, event: React.MouseEvent) => {
    event.stopPropagation()
    if (!test.archive) return
    const blob = await api.downloadTestArchive(test.id)
    const filename = `${(test.name || 'run').replace(/\s+/g, '-').toLowerCase()}-${test.id}.zip`
    downloadBlob(blob, filename)
  }

  if (!activeApplication?.id || !activeRelease?.id) {
    return (
      <div className="page">
        <div className="page-header">
          <div>
            <h2>Results</h2>
            <p className="page-subtitle">Select an application and release to browse archived runs.</p>
          </div>
        </div>
        <div className="empty-card">
          <p>No release selected.</p>
          <p className="empty-hint">Results are scoped to one application and one release so the history stays clear.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Results</h2>
          <p className="page-subtitle">
            {activeApplication.name} / {activeRelease.name}
          </p>
        </div>
        <div className="results-actions">
          {selectedIds.size === 2 && comparableTests.length === 2 && (
            <span className="results-compare-hint">Comparing 2 archived runs</span>
          )}
          {selectedIds.size > 0 && (
            <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} onClick={deleteSelected}>
              Delete Selected ({selectedIds.size})
            </button>
          )}
        </div>
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      <div className="results-overview">
        <div>
          <span>Completed</span>
          <strong>{completed.length}</strong>
        </div>
        <div>
          <span>Successful</span>
          <strong>{summary.complete}</strong>
        </div>
        <div>
          <span>Failed</span>
          <strong>{summary.failed}</strong>
        </div>
        <div>
          <span>Aborted</span>
          <strong>{summary.aborted}</strong>
        </div>
        <div>
          <span>Archives Ready</span>
          <strong>{summary.archivesReady}</strong>
        </div>
        <div>
          <span>Latest Run</span>
          <strong>{summary.latestRun ? formatShortDate(summary.latestRun.start_time || summary.latestRun.created_at) : 'None yet'}</strong>
        </div>
      </div>

      <div className="results-filter-bar">
        <input
          className="settings-input"
          value={search}
          onChange={event => setSearch(event.target.value)}
          placeholder="Search scenario, group, status, or VUsers"
        />
        <select className="settings-input" value={statusFilter} onChange={event => setStatusFilter(event.target.value as StatusFilter)}>
          <option value="all">All statuses</option>
          <option value="complete">Complete</option>
          <option value="failed">Failed</option>
          <option value="aborted">Aborted</option>
        </select>
        <select className="settings-input" value={archiveFilter} onChange={event => setArchiveFilter(event.target.value as ArchiveFilter)}>
          <option value="all">All archive states</option>
          <option value="ready">Archive ready</option>
          <option value="pending">Archive pending</option>
        </select>
      </div>

      {(selectedIds.size > 0 || filteredCompleted.length !== completed.length) && (
        <div className="results-selection-bar">
          <div>
            <span>Visible Runs</span>
            <strong>{filteredCompleted.length} of {completed.length}</strong>
          </div>
          {selectedIds.size > 0 && (
            <div>
              <span>Selected</span>
              <strong>{selectedIds.size}</strong>
            </div>
          )}
          {selectionState && (
            <div className="wide">
              <span>Compare Status</span>
              <strong>{selectionState}</strong>
            </div>
          )}
          {selectedIds.size > 0 && (
            <button className="btn btn-secondary" onClick={() => setSelectedIds(new Set())}>
              Clear Selection
            </button>
          )}
        </div>
      )}

      {(comparisonCards.length === 2 || comparisonLoading || comparisonError) && (
        <div className="page-section">
          <div className="results-compare-card">
            <div className="results-compare-header">
              <div>
                <h3 className="section-title">Run Comparison</h3>
                <p className="page-subtitle">Side-by-side view of two archived runs from this release.</p>
              </div>
            </div>

            {comparisonLoading && <p className="results-compare-status">Loading comparison...</p>}
            {comparisonError && <p className="results-compare-status error">{comparisonError}</p>}

            {comparisonCards.length === 2 && comparisonCards.every(item => item.report) && (
              <>
                <div className="results-compare-grid">
                  {comparisonCards.map(({ test, report }) => (
                    <div key={test.id} className="results-compare-panel">
                      <div className="results-compare-panel-header">
                        <div>
                          <h4>{test.name}</h4>
                          <p>{formatDate(test.start_time || test.created_at)}</p>
                        </div>
                        <StatusBadge status={resolveStatus(test, testStatuses)} />
                      </div>
                      <div className="results-compare-metrics">
                        <div className="results-compare-metric">
                          <span className="results-compare-label">Transactions</span>
                          <span className="results-compare-value">{report!.overview.transactions}</span>
                        </div>
                        <div className="results-compare-metric">
                          <span className="results-compare-label">Avg Latency</span>
                          <span className="results-compare-value">{report!.overview.avg_response_ms.toFixed(1)} ms</span>
                        </div>
                        <div className="results-compare-metric">
                          <span className="results-compare-label">Error Rate</span>
                          <span className="results-compare-value">{report!.overview.error_pct.toFixed(2)}%</span>
                        </div>
                        <div className="results-compare-metric">
                          <span className="results-compare-label">Peak TPS</span>
                          <span className="results-compare-value">{report!.overview.peak_tps.toFixed(1)}</span>
                        </div>
                        <div className="results-compare-metric">
                          <span className="results-compare-label">Peak VUsers</span>
                          <span className="results-compare-value">{report!.overview.peak_active_vusers}</span>
                        </div>
                        <div className="results-compare-metric">
                          <span className="results-compare-label">CPU Peak</span>
                          <span className="results-compare-value">{report!.resource_peaks.cpu_process_pct.toFixed(1)}%</span>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>

                <div className="results-compare-table-wrap">
                  {comparisonChartData.length > 0 && (
                    <div className="results-compare-chart-card">
                      <h4 className="results-compare-chart-title">Archived Summary Curves</h4>
                      <ResponsiveContainer width="100%" height={260}>
                        <LineChart data={comparisonChartData}>
                          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                          <XAxis dataKey="sample" stroke="var(--text-muted)" fontSize={11} />
                          <YAxis yAxisId="tps" stroke="var(--text-muted)" fontSize={11} />
                          <YAxis yAxisId="p95" orientation="right" stroke="var(--text-muted)" fontSize={11} />
                          <Tooltip
                            contentStyle={{ background: 'rgba(33, 36, 47, 0.94)', border: '1px solid var(--border)', borderRadius: 10 }}
                            labelFormatter={value => `Sample ${value}`}
                            isAnimationActive={false}
                          />
                          <Legend />
                          <Line yAxisId="tps" type="monotone" dataKey="leftTps" name={`${comparisonCards[0].test.name} TPS`} stroke="var(--chart-1)" dot={false} isAnimationActive={false} />
                          <Line yAxisId="tps" type="monotone" dataKey="rightTps" name={`${comparisonCards[1].test.name} TPS`} stroke="var(--chart-2)" dot={false} isAnimationActive={false} />
                          <Line yAxisId="p95" type="monotone" dataKey="leftP95" name={`${comparisonCards[0].test.name} P95`} stroke="var(--chart-4)" dot={false} strokeDasharray="4 2" isAnimationActive={false} />
                          <Line yAxisId="p95" type="monotone" dataKey="rightP95" name={`${comparisonCards[1].test.name} P95`} stroke="var(--chart-5)" dot={false} strokeDasharray="4 2" isAnimationActive={false} />
                        </LineChart>
                      </ResponsiveContainer>
                    </div>
                  )}
                  <table className="results-compare-table">
                    <thead>
                      <tr>
                        <th>Action</th>
                        <th>{comparisonCards[0].test.name} Avg</th>
                        <th>{comparisonCards[1].test.name} Avg</th>
                        <th>Latency Delta</th>
                        <th>{comparisonCards[0].test.name} Errors</th>
                        <th>{comparisonCards[1].test.name} Errors</th>
                      </tr>
                    </thead>
                    <tbody>
                      {comparisonActionRows.map(row => (
                        <tr key={row.name}>
                          <td className="cell-name">{row.name}</td>
                          <td>{row.left?.avg_response_ms?.toFixed(1) || '0.0'} ms</td>
                          <td>{row.right?.avg_response_ms?.toFixed(1) || '0.0'} ms</td>
                          <td className={row.deltaMs > 0 ? 'text-danger' : row.deltaMs < 0 ? 'text-success' : 'cell-muted'}>
                            {row.deltaMs >= 0 ? '+' : ''}
                            {row.deltaMs.toFixed(1)} ms
                          </td>
                          <td>{row.left?.errors || 0}</td>
                          <td>{row.right?.errors || 0}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </div>
        </div>
      )}

      <div className="page-section">
        <h3 className="section-title">Release Results ({filteredCompleted.length})</h3>
        {completed.length === 0 ? (
          <div className="empty-card">
            <p>No completed scenarios yet.</p>
            <p className="empty-hint">Run a scenario in this release to see archives, summaries, and comparisons here.</p>
          </div>
        ) : filteredCompleted.length === 0 ? (
          <div className="empty-card">
            <p>No results match the current filters.</p>
            <p className="empty-hint">Clear the search or filter values to bring the release history back into view.</p>
          </div>
        ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th style={{ width: 40 }}>
                    <input
                      type="checkbox"
                      checked={sortedCompleted.length > 0 && selectedIds.size === sortedCompleted.length}
                      onChange={toggleSelectAll}
                    />
                  </th>
                  <th onClick={() => handleSort('name')} className="sortable">Scenario <SortIcon active={sortField === 'name'} direction={sortDirection} /></th>
                  <th onClick={() => handleSort('status')} className="sortable">Status <SortIcon active={sortField === 'status'} direction={sortDirection} /></th>
                  <th onClick={() => handleSort('total_vusers')} className="sortable">VUsers <SortIcon active={sortField === 'total_vusers'} direction={sortDirection} /></th>
                  <th onClick={() => handleSort('groups')} className="sortable">Groups <SortIcon active={sortField === 'groups'} direction={sortDirection} /></th>
                  <th onClick={() => handleSort('started')} className="sortable">Started <SortIcon active={sortField === 'started'} direction={sortDirection} /></th>
                  <th onClick={() => handleSort('duration')} className="sortable">Duration <SortIcon active={sortField === 'duration'} direction={sortDirection} /></th>
                  <th onClick={() => handleSort('archive')} className="sortable">Archive <SortIcon active={sortField === 'archive'} direction={sortDirection} /></th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {sortedCompleted.map(test => (
                  <tr key={test.id} className={`clickable-row ${selectedIds.has(test.id) ? 'selected' : ''}`} onClick={() => onSelectTest(test.id)}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selectedIds.has(test.id)}
                        onClick={event => event.stopPropagation()}
                        onChange={event => toggleSelect(test.id, event)}
                      />
                    </td>
                    <td className="cell-name">{test.name}</td>
                    <td><StatusBadge status={resolveStatus(test, testStatuses)} /></td>
                    <td>{test.total_vusers}</td>
                    <td>{test.groups?.length || 1}</td>
                    <td className="cell-muted">{formatDate(test.start_time || test.created_at)}</td>
                    <td className="cell-muted">{test.duration}</td>
                    <td className="cell-muted">{test.archive ? `${test.archive.transaction_count} tx` : 'Pending'}</td>
                    <td style={{ textAlign: 'right' }}>
                      {test.archive && (
                        <button className="btn btn-sm btn-secondary" onClick={event => downloadArchive(test, event)}>
                          Archive
                        </button>
                      )}
                      <button className="btn btn-sm btn-ghost" onClick={event => { event.stopPropagation(); onSelectTest(test.id) }}>
                        Open
                      </button>
                      <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={event => handleDelete(test.id, event)}>
                        Delete
                      </button>
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
