import { Fragment, memo, useCallback, useEffect, useMemo, useState } from 'react'
import { StoredScenario, StoredScript, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import './TestHub.css'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function releaseList(scenario: StoredScenario) {
  const releases = scenario.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return releases.map(release => release.name).join(', ')
}

function SortIndicator({ active, direction }: { active: boolean, direction: 'asc' | 'desc' }) {
  return (
    <span className={`sort-icon ${active ? 'active' : ''}`}>
      {active ? (direction === 'asc' ? '↑' : '↓') : '↕'}
    </span>
  )
}

function TestHub({ onSelectTest }: Props) {
  const api = useApi()
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<any>({ name: '', duration: '1m', script_id: '', vusers: 10 })
  const [error, setError] = useState('')
  const [sortField, setSortField] = useState<'name' | 'scripts' | 'created_at' | 'associated_releases' | 'last_run_at' | 'result_count'>('last_run_at')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [expandedScenarioId, setExpandedScenarioId] = useState<string | null>(null)
  const [scenarioRuns, setScenarioRuns] = useState<Record<string, TestSummary[]>>({})
  const [loadingRunsFor, setLoadingRunsFor] = useState<string | null>(null)
  const [innerSortField, setInnerSortField] = useState<'name' | 'status' | 'release_id' | 'start_time' | 'duration'>('start_time')
  const [innerSortDirection, setInnerSortDirection] = useState<'asc' | 'desc'>('desc')

  const refresh = useCallback(async () => {
    try {
      const [scenarioRes, scriptRes] = await Promise.all([api.listScenarios(), api.listScripts()])
      const nextScenarios = scenarioRes.scenarios || []
      const nextScripts = scriptRes.scripts || []
      setScenarios(nextScenarios)
      setScripts(nextScripts)
      setForm((prev: any) => ({ ...prev, script_id: prev.script_id || nextScripts[0]?.id || '' }))
    } catch (err: any) {
      setError(err.message || 'Unable to load scenarios')
    }
  }, [api])

  useEffect(() => { refresh() }, [refresh])

  const sorted = useMemo(() => {
    return [...scenarios].sort((a, b) => {
      let aValue: any
      let bValue: any
      switch (sortField) {
        case 'scripts':
          aValue = (a.scripts || []).length
          bValue = (b.scripts || []).length
          break
        case 'created_at':
        case 'last_run_at':
          aValue = new Date((a as any)[sortField] || 0).getTime()
          bValue = new Date((b as any)[sortField] || 0).getTime()
          break
        case 'associated_releases':
          aValue = (a.associated_releases || []).length
          bValue = (b.associated_releases || []).length
          break
        case 'result_count':
          aValue = a.result_count || 0
          bValue = b.result_count || 0
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
  }, [scenarios, sortField, sortDirection])

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else {
      setSortField(field)
      setSortDirection(field === 'last_run_at' || field === 'result_count' ? 'desc' : 'asc')
    }
  }

  const handleInnerSort = (field: typeof innerSortField) => {
    if (innerSortField === field) setInnerSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else { setInnerSortField(field); setInnerSortDirection('asc') }
  }

  const getSortedInnerRuns = (runs: TestSummary[]) => {
    return [...runs].sort((a: any, b: any) => {
      let aValue: any
      let bValue: any
      if (innerSortField === 'start_time') {
        aValue = new Date(a.start_time || a.created_at || 0).getTime()
        bValue = new Date(b.start_time || b.created_at || 0).getTime()
      } else {
        aValue = String(a[innerSortField] || '').toLowerCase()
        bValue = String(b[innerSortField] || '').toLowerCase()
      }
      if (aValue < bValue) return innerSortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return innerSortDirection === 'asc' ? 1 : -1
      return 0
    })
  }

  const toggleScenarioRuns = async (scenario: StoredScenario) => {
    if (expandedScenarioId === scenario.id) {
      setExpandedScenarioId(null)
      return
    }
    setExpandedScenarioId(scenario.id)
    if (scenarioRuns[scenario.id]) return
    setLoadingRunsFor(scenario.id)
    try {
      const res = await api.listScenarioResults(scenario.id)
      const runs = (res.tests || []).sort((a: TestSummary, b: TestSummary) => {
        const aTime = new Date(a.start_time || a.created_at || 0).getTime()
        const bTime = new Date(b.start_time || b.created_at || 0).getTime()
        return bTime - aTime
      })
      setScenarioRuns(prev => ({ ...prev, [scenario.id]: runs }))
    } catch {
      setScenarioRuns(prev => ({ ...prev, [scenario.id]: [] }))
    } finally {
      setLoadingRunsFor(null)
    }
  }

  const openCreate = () => {
    setEditingId(null)
    setForm({ name: '', duration: '1m', script_id: scripts[0]?.id || '', vusers: 10 })
    setShowForm(true)
  }

  const openEdit = (scenario: StoredScenario) => {
    const first = scenario.scripts[0]
    setEditingId(scenario.id)
    setForm({
      name: scenario.name,
      duration: scenario.duration,
      script_id: first?.script_id || scripts[0]?.id || '',
      vusers: first?.vusers || 10,
    })
    setShowForm(true)
  }

  const saveScenario = async () => {
    if (!form.name.trim() || !form.script_id) {
      setError('Scenario name and script are required.')
      return
    }
    const payload = {
      name: form.name.trim(),
      duration: form.duration || '1m',
      scenario_mode: 'vuser_driven',
      groups: [{
        script_id: form.script_id,
        vusers: Number(form.vusers) || 10,
        initialization: 'at_runtime',
      }],
    }
    try {
      if (editingId) await api.updateScenario(editingId, payload)
      else await api.createScenario(payload)
      setShowForm(false)
      setEditingId(null)
      refresh()
    } catch (err: any) {
      setError(err.message || 'Unable to save scenario')
    }
  }

  const deleteScenario = async (id: string) => {
    if (!confirm('Delete this scenario? Scenarios with results cannot be deleted.')) return
    try {
      await api.deleteScenario(id)
      refresh()
    } catch (err: any) {
      setError(err.message || 'Unable to delete scenario')
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scenarios</h2>
          <p className="page-subtitle">Reusable scenario definitions linked to scripts, releases, and results.</p>
        </div>
        <button className="btn btn-primary" onClick={openCreate}>+ New Scenario</button>
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      {showForm && (
        <div className="page-section">
          <h3 className="section-title">{editingId ? 'Edit Scenario' : 'New Scenario'}</h3>
          <div className="settings-grid">
            <input className="settings-input" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} placeholder="Scenario name" />
            <input className="settings-input" value={form.duration} onChange={event => setForm({ ...form, duration: event.target.value })} placeholder="Duration, e.g. 5m" />
            <select className="settings-input" value={form.script_id} onChange={event => setForm({ ...form, script_id: event.target.value })}>
              {scripts.map(script => <option key={script.id} value={script.id}>{script.name}</option>)}
            </select>
            <input className="settings-input" type="number" min={1} value={form.vusers} onChange={event => setForm({ ...form, vusers: Number(event.target.value) })} />
          </div>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 12 }}>
            <button className="btn btn-secondary" onClick={() => setShowForm(false)}>Cancel</button>
            <button className="btn btn-primary" onClick={saveScenario}>Save Scenario</button>
          </div>
        </div>
      )}

      <div className="page-section">
        <h3 className="section-title">Scenario Library ({scenarios.length})</h3>
        <div className="data-table">
          <table>
            <thead>
              <tr>
                <th onClick={() => handleSort('name')} className="sortable">Name <SortIndicator active={sortField === 'name'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('scripts')} className="sortable">Scripts <SortIndicator active={sortField === 'scripts'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('created_at')} className="sortable">Created <SortIndicator active={sortField === 'created_at'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('associated_releases')} className="sortable">Associated Releases <SortIndicator active={sortField === 'associated_releases'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('last_run_at')} className="sortable">Last Run <SortIndicator active={sortField === 'last_run_at'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('result_count')} className="sortable">Results <SortIndicator active={sortField === 'result_count'} direction={sortDirection} /></th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map(scenario => (
                <Fragment key={scenario.id}>
                  <tr className="clickable-row" onClick={() => toggleScenarioRuns(scenario)}>
                    <td className="cell-name">{scenario.name}</td>
                    <td>{scenario.scripts.map(script => script.script_name || script.name || script.script_id).join(', ')}</td>
                    <td className="cell-muted">{formatDate(scenario.created_at)}</td>
                    <td className="cell-muted">{releaseList(scenario)}</td>
                    <td className="cell-muted">{formatDate(scenario.last_run_at)}</td>
                    <td>{scenario.result_count || 0}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button className="btn btn-sm btn-secondary" onClick={(event) => {
                        event.stopPropagation()
                        const first = (scenarioRuns[scenario.id] || [])[0]
                        if (first) onSelectTest(first.id)
                      }}>Results</button>
                      <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); openEdit(scenario) }}>Edit</button>
                      <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(event) => { event.stopPropagation(); deleteScenario(scenario.id) }}>Delete</button>
                    </td>
                  </tr>
                  {expandedScenarioId === scenario.id && (
                    <tr className="no-hover">
                      <td colSpan={7} style={{ background: 'var(--bg-primary)' }}>
                        <div style={{ padding: '10px 4px' }}>
                          <div className="cell-muted" style={{ marginBottom: 8 }}>
                            Linked Scenario Runs {loadingRunsFor === scenario.id ? '(loading...)' : `(${(scenarioRuns[scenario.id] || []).length})`}
                          </div>
                          {(scenarioRuns[scenario.id] || []).length === 0 ? (
                            <div className="cell-muted">No runs for this scenario yet.</div>
                          ) : (
                            <div className="data-table">
                              <table>
                                <thead>
                                  <tr>
                                    <th onClick={() => handleInnerSort('name')} className="sortable">
                                      Run Name <SortIndicator active={innerSortField === 'name'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('status')} className="sortable">
                                      Status <SortIndicator active={innerSortField === 'status'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('release_id')} className="sortable">
                                      Release ID <SortIndicator active={innerSortField === 'release_id'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('start_time')} className="sortable">
                                      Started <SortIndicator active={innerSortField === 'start_time'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('duration')} className="sortable">
                                      Duration <SortIndicator active={innerSortField === 'duration'} direction={innerSortDirection} />
                                    </th>
                                    <th style={{ textAlign: 'right' }}>Actions</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {getSortedInnerRuns(scenarioRuns[scenario.id] || []).map(run => (
                                    <tr key={run.id} className="clickable-row" onClick={(e) => { e.stopPropagation(); onSelectTest(run.id) }}>
                                      <td className="cell-name">{run.name}</td>
                                      <td>{run.status}</td>
                                      <td className="cell-mono">{run.release_id || '—'}</td>
                                      <td className="cell-muted">{formatDateTime(run.start_time || run.created_at)}</td>
                                      <td className="cell-muted">{run.duration}</td>
                                      <td style={{ textAlign: 'right' }}>
                                        <button className="btn btn-sm btn-secondary" onClick={(e) => { e.stopPropagation(); onSelectTest(run.id) }}>Open</button>
                                      </td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
              {sorted.length === 0 && (
                <tr><td colSpan={7} className="cell-muted">No scenarios yet. Create a scenario from an existing script.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export default memo(TestHub)
