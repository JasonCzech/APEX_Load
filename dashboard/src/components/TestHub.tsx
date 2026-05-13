import { Fragment, memo, useCallback, useEffect, useMemo, useState } from 'react'
import { ScenarioDataPoolBinding, SharedDataPool, StoredScenario, StoredScript, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import './TestHub.css'
import { buildScenarioUpsertPayload } from './scenarioPayloadUtils'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

interface ScenarioFormState {
  name: string
  duration: string
  script_id: string
  vusers: number
  data_pool_id?: string
  data_pool_alias?: string
  data_pool_access_mode?: ScenarioDataPoolBinding['access_mode']
  data_pool_selection_strategy?: ScenarioDataPoolBinding['selection_strategy']
}

function emptyScenarioForm(scriptId = ''): ScenarioFormState {
  return {
    name: '',
    duration: '1m',
    script_id: scriptId,
    vusers: 10,
    data_pool_id: '',
    data_pool_alias: '',
    data_pool_access_mode: 'read_only',
    data_pool_selection_strategy: 'sequential',
  }
}

function getScenarioSortValue(scenario: StoredScenario, field: 'name' | 'scripts' | 'data_pools' | 'created_at' | 'associated_releases' | 'last_run_at' | 'result_count'): string | number {
  switch (field) {
    case 'scripts':
      return (scenario.scripts || []).length
    case 'data_pools':
      return (scenario.data_pools || []).length
    case 'created_at':
      return new Date(scenario.created_at || 0).getTime()
    case 'last_run_at':
      return new Date(scenario.last_run_at || 0).getTime()
    case 'associated_releases':
      return (scenario.associated_releases || []).length
    case 'result_count':
      return scenario.result_count || 0
    case 'name':
    default:
      return (scenario.name || '').toLowerCase()
  }
}

function getRunSortValue(run: TestSummary, field: 'name' | 'status' | 'release_id' | 'start_time' | 'duration'): string | number {
  if (field === 'start_time') {
    return new Date(run.start_time || run.created_at || 0).getTime()
  }
  return String(run[field] || '').toLowerCase()
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
  const [dataPools, setDataPools] = useState<SharedDataPool[]>([])
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<ScenarioFormState>(emptyScenarioForm())
  const [error, setError] = useState('')
  const [sortField, setSortField] = useState<'name' | 'scripts' | 'data_pools' | 'created_at' | 'associated_releases' | 'last_run_at' | 'result_count'>('last_run_at')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [expandedScenarioId, setExpandedScenarioId] = useState<string | null>(null)
  const [scenarioRuns, setScenarioRuns] = useState<Record<string, TestSummary[]>>({})
  const [loadingRunsFor, setLoadingRunsFor] = useState<string | null>(null)
  const [innerSortField, setInnerSortField] = useState<'name' | 'status' | 'release_id' | 'start_time' | 'duration'>('start_time')
  const [innerSortDirection, setInnerSortDirection] = useState<'asc' | 'desc'>('desc')

  const refresh = useCallback(async () => {
    try {
      const [scenarioRes, scriptRes, dataPoolRes] = await Promise.all([api.listScenarios(), api.listScripts(), api.listDataPools()])
      const nextScenarios = scenarioRes.scenarios || []
      const nextScripts = scriptRes.scripts || []
      setScenarios(nextScenarios)
      setScripts(nextScripts)
      setDataPools(dataPoolRes.data_pools || [])
      setForm(prev => ({ ...prev, script_id: prev.script_id || nextScripts[0]?.id || '' }))
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load scenarios'))
    }
  }, [api])

  useEffect(() => { refresh() }, [refresh])

  const sorted = useMemo(() => {
    return [...scenarios].sort((a, b) => {
      const aValue = getScenarioSortValue(a, sortField)
      const bValue = getScenarioSortValue(b, sortField)
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
    return [...runs].sort((a, b) => {
      const aValue = getRunSortValue(a, innerSortField)
      const bValue = getRunSortValue(b, innerSortField)
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
    setForm(emptyScenarioForm(scripts[0]?.id || ''))
    setShowForm(true)
  }

  const openEdit = (scenario: StoredScenario) => {
    const first = scenario.scripts[0]
    const firstPool = scenario.data_pools?.[0]
    setEditingId(scenario.id)
    setForm({
      name: scenario.name,
      duration: scenario.duration,
      script_id: first?.script_id || scripts[0]?.id || '',
      vusers: first?.vusers || 10,
      data_pool_id: firstPool?.pool_id || '',
      data_pool_alias: firstPool?.alias || '',
      data_pool_access_mode: firstPool?.access_mode || 'read_only',
      data_pool_selection_strategy: firstPool?.selection_strategy || 'sequential',
    })
    setShowForm(true)
  }

  const saveScenario = async () => {
    if (!form.name.trim() || !form.script_id) {
      setError('Scenario name and script are required.')
      return
    }
    const dataPoolsPayload = form.data_pool_id ? [{
      pool_id: form.data_pool_id,
      alias: (form.data_pool_alias || dataPools.find(pool => pool.id === form.data_pool_id)?.name || 'data').trim().toLowerCase().replace(/\s+/g, '_'),
      access_mode: form.data_pool_access_mode || 'read_only',
      selection_strategy: form.data_pool_selection_strategy || 'sequential',
    }] : []
    const payload = buildScenarioUpsertPayload({
      name: form.name,
      duration: form.duration || '1m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{
        name: '',
        scriptId: form.script_id,
        vusers: Number(form.vusers) || 10,
        rampUp: '',
        rampDown: '',
        initialization: 'at_runtime',
      }],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        log_level: '',
        auto_transactions: '',
        snapshot_on_error: false,
      },
      dataPools: dataPoolsPayload,
    })
    try {
      if (editingId) await api.updateScenario(editingId, payload)
      else await api.createScenario(payload)
      setShowForm(false)
      setEditingId(null)
      refresh()
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to save scenario'))
    }
  }

  const deleteScenario = async (id: string) => {
    if (!confirm('Delete this scenario? Scenarios with results cannot be deleted.')) return
    try {
      await api.deleteScenario(id)
      refresh()
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to delete scenario'))
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
            <select className="settings-input" value={form.data_pool_id || ''} onChange={event => setForm({ ...form, data_pool_id: event.target.value })}>
              <option value="">No shared data pool</option>
              {dataPools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}
            </select>
            <input
              className="settings-input"
              value={form.data_pool_alias || ''}
              onChange={event => setForm({ ...form, data_pool_alias: event.target.value })}
              placeholder="Pool alias, e.g. customers"
              disabled={!form.data_pool_id}
            />
            <select
              className="settings-input"
              value={form.data_pool_access_mode || 'read_only'}
              onChange={event => setForm({ ...form, data_pool_access_mode: event.target.value as 'read_only' | 'read_write' | 'consume' })}
              disabled={!form.data_pool_id}
            >
              <option value="read_only">Read only</option>
              <option value="read_write">Read/write</option>
              <option value="consume">Consume rows</option>
            </select>
            <select
              className="settings-input"
              value={form.data_pool_selection_strategy || 'sequential'}
              onChange={event => setForm({ ...form, data_pool_selection_strategy: event.target.value as 'sequential' | 'random' | 'unique' })}
              disabled={!form.data_pool_id}
            >
              <option value="sequential">Sequential rows</option>
              <option value="random">Random rows</option>
              <option value="unique">Unique rows</option>
            </select>
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
                <th onClick={() => handleSort('data_pools')} className="sortable">Data Pools <SortIndicator active={sortField === 'data_pools'} direction={sortDirection} /></th>
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
                    <td className="cell-muted">{(scenario.data_pools || []).map(pool => `${pool.alias} (${pool.pool_name || pool.pool_id})`).join(', ') || 'None'}</td>
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
                      <td colSpan={8} style={{ background: 'var(--bg-primary)' }}>
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
                <tr><td colSpan={8} className="cell-muted">No scenarios yet. Create a scenario from an existing script.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export default memo(TestHub)
