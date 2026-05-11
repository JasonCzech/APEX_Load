import { useState, useEffect, useCallback, useMemo, memo } from 'react'
import { downloadBlob } from '../utils/fileUtils'
import { ResponsiveContainer, LineChart, Line, CartesianGrid, XAxis, YAxis, Tooltip, Legend } from 'recharts'
import { useApi } from '../hooks/useApi'
import { Folder, IntervalSummaryRecord, Project, RunArchiveReport, TestSummary } from '../types'
import FolderTree from './FolderTree'
import StatusBadge from './StatusBadge'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  activeProject?: Project | null
}

function ResultsPage({ testStatuses, onSelectTest, activeProject }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [sortField, setSortField] = useState<string>('start_time')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [comparisonReports, setComparisonReports] = useState<Record<string, RunArchiveReport>>({})
  const [comparisonSummaries, setComparisonSummaries] = useState<Record<string, IntervalSummaryRecord[]>>({})
  const [comparisonLoading, setComparisonLoading] = useState(false)
  const [comparisonError, setComparisonError] = useState('')
  const [folders, setFolders] = useState<Folder[]>([])
  const [selectedFolderId, setSelectedFolderId] = useState<string | null | undefined>(undefined)

  const refresh = useCallback(async () => {
    try {
      const [testRes, folderRes] = await Promise.all([
        api.listTests(activeProject?.id),
        activeProject?.id ? api.listFolders(activeProject.id, 'results') : Promise.resolve({ folders: [] }),
      ])
      setTests(testRes.tests || [])
      setFolders(folderRes.folders || [])
    } catch {
      // API not available
    }
  }, [api, activeProject?.id])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 10000)
    return () => clearInterval(interval)
  }, [refresh])

  useEffect(() => {
    setSelectedFolderId(undefined)
    setSelectedIds(new Set())
  }, [activeProject?.id])

  const completedTests = tests.filter((t) => {
    const s = testStatuses[t.id] || t.status
    const statusMatches = s === 'COMPLETE' || s === 'FAILED' || s === 'ABORTED'
    if (!statusMatches) return false
    if (selectedFolderId === undefined) return true
    if (selectedFolderId === null) return !t.folder_id
    return t.folder_id === selectedFolderId
  })

  const allCompletedTests = tests.filter((t) => {
    const s = testStatuses[t.id] || t.status
    return s === 'COMPLETE' || s === 'FAILED' || s === 'ABORTED'
  })

  const selectedTests = useMemo(
    () => completedTests.filter((test) => selectedIds.has(test.id)),
    [completedTests, selectedIds],
  )

  const comparableTests = useMemo(
    () => selectedTests.filter((test) => test.archive).slice(0, 2),
    [selectedTests],
  )

  useEffect(() => {
    if (comparableTests.length !== 2) {
      setComparisonReports({})
      setComparisonSummaries({})
      setComparisonError('')
      setComparisonLoading(false)
      return
    }

    let cancelled = false
    const loadReports = async () => {
      setComparisonLoading(true)
      setComparisonError('')
      try {
        const reports = await Promise.all(comparableTests.map(async (test) => {
          const report = await api.getTestArchiveReport(test.id)
          return [test.id, report as RunArchiveReport] as const
        }))
        const summaries = await Promise.all(comparableTests.map(async (test) => {
          const response = await api.getTestArchiveSummaries(test.id, { limit: 120, offset: 0 })
          return [test.id, response.summaries || []] as const
        }))
        if (!cancelled) {
          setComparisonReports(Object.fromEntries(reports))
          setComparisonSummaries(Object.fromEntries(summaries))
        }
      } catch (error: any) {
        if (!cancelled) {
          setComparisonReports({})
          setComparisonSummaries({})
          setComparisonError(error.message || 'Unable to load comparison reports')
        }
      } finally {
        if (!cancelled) setComparisonLoading(false)
      }
    }

    loadReports()
    return () => {
      cancelled = true
    }
  }, [api, comparableTests])

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

  const handleDownloadArchive = async (test: TestSummary, e: React.MouseEvent) => {
    e.stopPropagation()
    if (!test.archive) return
    try {
      const blob = await api.downloadTestArchive(test.id)
      const filename = `${test.name.replace(/\\s+/g, '-').toLowerCase()}-${test.id}.zip`
      downloadBlob(blob, filename)
    } catch {
      // ignore for now
    }
  }

  const createFolder = async (parentId: string | null) => {
    if (!activeProject?.id) return
    const name = window.prompt('Folder name')
    if (!name?.trim()) return
    await api.createFolder(activeProject.id, { name: name.trim(), category: 'results', parent_id: parentId })
    refresh()
  }

  const renameFolder = async (id: string, name: string) => {
    await api.updateFolder(id, { name })
    refresh()
  }

  const deleteFolder = async (id: string) => {
    await api.deleteFolder(id)
    if (selectedFolderId === id) setSelectedFolderId(undefined)
    refresh()
  }

  const moveResult = async (id: string, folderId: string | null) => {
    await api.moveTest(id, folderId)
    refresh()
  }

  const comparisonCards = useMemo(() => {
    if (comparableTests.length !== 2) return []
    return comparableTests.map((test) => ({
      test,
      report: comparisonReports[test.id],
    }))
  }, [comparableTests, comparisonReports])

  const comparisonActionRows = useMemo(() => {
    if (comparisonCards.length !== 2) return []
    const [left, right] = comparisonCards
    const actionNames = new Set<string>([
      ...Object.keys(left.report?.by_action || {}),
      ...Object.keys(right.report?.by_action || {}),
    ])
    return Array.from(actionNames)
      .map((name) => ({
        name,
        left: left.report?.by_action?.[name],
        right: right.report?.by_action?.[name],
        deltaMs: (right.report?.by_action?.[name]?.avg_response_ms || 0) - (left.report?.by_action?.[name]?.avg_response_ms || 0),
      }))
      .sort((a, b) => Math.abs(b.deltaMs) - Math.abs(a.deltaMs))
      .slice(0, 8)
  }, [comparisonCards])

  const comparisonChartData = useMemo(() => {
    if (comparisonCards.length !== 2) return []
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

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Results</h2>
          <p className="page-subtitle">Historical scenario results and performance reports</p>
        </div>
        <div className="results-actions">
          {selectedIds.size === 2 && comparableTests.length === 2 && (
            <span className="results-compare-hint">Comparing 2 archived runs</span>
          )}
          {selectedIds.size > 0 && (
            <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} onClick={handleDeleteSelected}>
              Delete Selected ({selectedIds.size})
            </button>
          )}
        </div>
      </div>

      {(comparisonCards.length === 2 || comparisonLoading || comparisonError) && (
        <div className="page-section">
          <div className="results-compare-card">
            <div className="results-compare-header">
              <div>
                <h3 className="section-title">Run Comparison</h3>
                <p className="page-subtitle">Side-by-side view of two archived runs using saved report data.</p>
              </div>
            </div>

            {comparisonLoading && <p className="results-compare-status">Loading comparison…</p>}
            {comparisonError && <p className="results-compare-status error">{comparisonError}</p>}

            {comparisonCards.length === 2 && comparisonCards.every((item) => item.report) && (
              <>
                <div className="results-compare-grid">
                  {comparisonCards.map(({ test, report }) => (
                    <div key={test.id} className="results-compare-panel">
                      <div className="results-compare-panel-header">
                        <div>
                          <h4>{test.name}</h4>
                          <p>{new Date(test.start_time || test.created_at).toLocaleString()}</p>
                        </div>
                        <StatusBadge status={testStatuses[test.id] || test.status} />
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
                            labelFormatter={(value) => `Sample ${value}`}
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
                      {comparisonActionRows.map((row) => (
                        <tr key={row.name}>
                          <td className="cell-name">{row.name}</td>
                          <td>{row.left?.avg_response_ms?.toFixed(1) || '0.0'} ms</td>
                          <td>{row.right?.avg_response_ms?.toFixed(1) || '0.0'} ms</td>
                          <td className={row.deltaMs > 0 ? 'text-danger' : row.deltaMs < 0 ? 'text-success' : 'cell-muted'}>
                            {row.deltaMs >= 0 ? '+' : ''}{row.deltaMs.toFixed(1)} ms
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

      <div className="folder-layout">
        <FolderTree
          title="Results"
          category="results"
          folders={folders}
          items={allCompletedTests}
          selectedFolderId={selectedFolderId}
          onSelectFolder={setSelectedFolderId}
          onSelectItem={onSelectTest}
          onCreateFolder={createFolder}
          onRenameFolder={renameFolder}
          onDeleteFolder={deleteFolder}
          onMoveItem={moveResult}
          renderItemExtra={(item) => <StatusBadge status={testStatuses[item.id] || item.status} />}
        />

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
              <p>No completed scenarios here.</p>
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
                  <th>Archive</th>
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
                    <td className="cell-muted">{test.archive ? `${test.archive.transaction_count} tx` : 'Pending'}</td>
                    <td className="cell-muted">{new Date(test.start_time || test.created_at).toLocaleDateString()}</td>
                    <td className="cell-muted">{test.duration}</td>
                    <td style={{ textAlign: 'right' }}>
                      {test.archive && (
                        <button className="btn btn-sm btn-secondary" onClick={(e) => handleDownloadArchive(test, e)}>
                          Archive
                        </button>
                      )}
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
    </div>
  )
}
export default memo(ResultsPage)
