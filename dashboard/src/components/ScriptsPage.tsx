import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { StoredScript, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import ScriptEditor from './ScriptEditor'
import LoadRunnerImport from './LoadRunnerImport'
import RecordingStudio from './RecordingStudio'
import SampleScriptImport from './SampleScriptImport'

type SubView = 'list' | 'create-options' | 'create' | 'edit' | 'import' | 'recorder' | 'sample'

interface Props {
  initialMode?: SubView
  initialEditId?: string
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function releaseList(script: StoredScript) {
  const releases = script.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return releases.map(release => release.name).join(', ')
}

function scenarioRunReleaseList(script: StoredScript) {
  const releases = script.scenario_run_releases || []
  if (releases.length === 0) return 'No scenario-run releases yet'
  return releases.map(release => release.name).join(', ')
}

export default function ScriptsPage({ initialMode = 'list', initialEditId }: Props) {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [subView, setSubView] = useState<SubView>(initialMode === 'create' ? 'create-options' : initialMode)
  const [editingId, setEditingId] = useState<string | null>(initialEditId || null)
  const [editingScript, setEditingScript] = useState<any>(null)
  const [draftScript, setDraftScript] = useState<any>(null)
  const [sortField, setSortField] = useState<'name' | 'protocol' | 'version' | 'created_at' | 'scenario_count' | 'associated_releases' | 'scenario_run_releases' | 'last_run_at'>('name')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc')
  const [expandedScriptId, setExpandedScriptId] = useState<string | null>(null)
  const [scriptRuns, setScriptRuns] = useState<Record<string, TestSummary[]>>({})
  const [loadingRunsFor, setLoadingRunsFor] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts()
      setScripts(res.scripts || [])
    } catch { /* API not available */ }
  }, [api])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => {
    if (initialMode === 'edit' && initialEditId) handleEdit(initialEditId)
    else if (initialMode && initialMode !== subView) setSubView(initialMode === 'create' ? 'create-options' : initialMode)
  }, [initialEditId, initialMode])

  const handleEdit = async (id: string) => {
    try {
      const res = await api.getScript(id)
      setEditingId(id)
      setEditingScript(res.script)
      setSubView('edit')
    } catch { /* ignore */ }
  }

  const handleDelete = async (id: string) => {
    if (!confirm('Delete this script?')) return
    try { await api.deleteScript(id); refresh() } catch { /* ignore */ }
  }

  const sortedScripts = useMemo(() => {
    return [...scripts].sort((a: any, b: any) => {
      let aValue: any
      let bValue: any
      if (sortField === 'created_at' || sortField === 'last_run_at') {
        aValue = new Date(a[sortField] || 0).getTime()
        bValue = new Date(b[sortField] || 0).getTime()
      } else if (sortField === 'scenario_count') {
        aValue = a.scenario_count || 0
        bValue = b.scenario_count || 0
      } else if (sortField === 'associated_releases') {
        aValue = (a.associated_releases || []).length
        bValue = (b.associated_releases || []).length
      } else if (sortField === 'scenario_run_releases') {
        aValue = (a.scenario_run_releases || []).length
        bValue = (b.scenario_run_releases || []).length
      } else {
        aValue = String(a[sortField] || '').toLowerCase()
        bValue = String(b[sortField] || '').toLowerCase()
      }
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [scripts, sortField, sortDirection])

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else { setSortField(field); setSortDirection('asc') }
  }

  const toggleScriptRuns = async (script: StoredScript) => {
    if (expandedScriptId === script.id) {
      setExpandedScriptId(null)
      return
    }
    setExpandedScriptId(script.id)
    if (scriptRuns[script.id]) return
    setLoadingRunsFor(script.id)
    try {
      const res = await api.listTests()
      const linkedRuns = (res.tests || [])
        .filter((test: TestSummary) =>
          !!test.scenario_id && (test.groups || []).some(group => group.script_id === script.id),
        )
        .sort((a: TestSummary, b: TestSummary) => {
          const aTime = new Date(a.start_time || a.created_at || 0).getTime()
          const bTime = new Date(b.start_time || b.created_at || 0).getTime()
          return bTime - aTime
        })
      setScriptRuns(prev => ({ ...prev, [script.id]: linkedRuns }))
    } catch {
      setScriptRuns(prev => ({ ...prev, [script.id]: [] }))
    } finally {
      setLoadingRunsFor(null)
    }
  }

  const handleSaved = () => { refresh(); setSubView('list'); setEditingId(null); setEditingScript(null); setDraftScript(null) }
  const handleImported = () => { refresh(); setSubView('list') }
  const handleSampleEdit = (script: any) => { setDraftScript(script); setEditingId(null); setEditingScript(null); setSubView('create') }

  if (subView === 'create') {
    return <div className="page"><ScriptEditor initialScript={draftScript} onSave={handleSaved} onClose={() => setSubView('list')} /></div>
  }

  if (subView === 'edit') {
    return <div className="page"><ScriptEditor scriptId={editingId} initialScript={editingScript} onSave={handleSaved} onClose={() => setSubView('list')} /></div>
  }

  if (subView === 'import') {
    return <div className="page"><LoadRunnerImport onImported={handleImported} onClose={() => setSubView('list')} /></div>
  }

  if (subView === 'recorder') {
    return (
      <div className="page">
        <button className="btn btn-secondary" onClick={() => setSubView('create-options')} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
        <RecordingStudio />
      </div>
    )
  }

  if (subView === 'sample') {
    return (
      <div className="page">
        <button className="btn btn-secondary" onClick={() => setSubView('create-options')} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
        <SampleScriptImport onImported={handleImported} onEdit={handleSampleEdit} onClose={() => setSubView('create-options')} />
      </div>
    )
  }

  if (subView === 'create-options') {
    return (
      <div className="page">
        <div className="page-header">
          <div>
            <h2>Create Script</h2>
            <p className="page-subtitle">Choose how you want to create a reusable script.</p>
          </div>
          <button className="btn btn-secondary" onClick={() => setSubView('list')}>Back to Library</button>
        </div>
        <div className="script-create-grid">
          <button className="script-create-option" onClick={() => setSubView('create')}><h3>Visual Editor</h3><p>Build a script from scratch.</p></button>
          <button className="script-create-option" onClick={() => setSubView('recorder')}><h3>Script Recorder</h3><p>Record browser traffic into a reusable script.</p></button>
          <button className="script-create-option" onClick={() => setSubView('import')}><h3>Import LoadRunner</h3><p>Convert a LoadRunner ZIP.</p></button>
          <button className="script-create-option" onClick={() => setSubView('sample')}><h3>Build From Sample</h3><p>Generate from sample traffic.</p></button>
        </div>
      </div>
    )
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scripts</h2>
          <p className="page-subtitle">Reusable scripts. Release associations are inferred from actual test usage.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setSubView('create-options')}>+ Create Script</button>
      </div>

      <div className="page-section">
        <h3 className="section-title">Script Library ({scripts.length})</h3>
        <div className="data-table">
          <table>
            <thead>
              <tr>
                <th onClick={() => handleSort('name')} className="sortable">Name</th>
                <th onClick={() => handleSort('protocol')} className="sortable">Protocol</th>
                <th onClick={() => handleSort('version')} className="sortable">Version</th>
                <th onClick={() => handleSort('created_at')} className="sortable">Created</th>
                <th onClick={() => handleSort('scenario_count')} className="sortable">Linked Scenarios</th>
                <th onClick={() => handleSort('associated_releases')} className="sortable">Associated Releases</th>
                <th onClick={() => handleSort('scenario_run_releases')} className="sortable">Scenario Run Releases</th>
                <th onClick={() => handleSort('last_run_at')} className="sortable">Last Run</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedScripts.map(script => (
                <Fragment key={script.id}>
                  <tr key={script.id} className="clickable-row" onClick={() => toggleScriptRuns(script)}>
                    <td className="cell-name">{script.name}</td>
                    <td><span className="protocol-badge">{script.protocol}</span></td>
                    <td>{script.version || '—'}</td>
                    <td className="cell-muted">{formatDate(script.created_at)}</td>
                    <td>{script.scenario_count || 0}</td>
                    <td className="cell-muted">{releaseList(script)}</td>
                    <td className="cell-muted">{scenarioRunReleaseList(script)}</td>
                    <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); handleEdit(script.id) }}>Edit</button>
                      <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(event) => { event.stopPropagation(); handleDelete(script.id) }}>Delete</button>
                    </td>
                  </tr>
                  {expandedScriptId === script.id && (
                    <tr>
                      <td colSpan={9} style={{ background: 'var(--bg-primary)' }}>
                        <div style={{ padding: '10px 4px' }}>
                          <div className="cell-muted" style={{ marginBottom: 8 }}>
                            Linked Scenario Runs {loadingRunsFor === script.id ? '(loading...)' : `(${(scriptRuns[script.id] || []).length})`}
                          </div>
                          {(scriptRuns[script.id] || []).length === 0 ? (
                            <div className="cell-muted">No linked scenario runs for this script yet.</div>
                          ) : (
                            <div className="data-table">
                              <table>
                                <thead>
                                  <tr>
                                    <th>Run Name</th>
                                    <th>Status</th>
                                    <th>Scenario ID</th>
                                    <th>Release ID</th>
                                    <th>Started</th>
                                    <th>Duration</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {(scriptRuns[script.id] || []).map(run => (
                                    <tr key={run.id}>
                                      <td className="cell-name">{run.name}</td>
                                      <td>{run.status}</td>
                                      <td className="cell-mono">{run.scenario_id || '—'}</td>
                                      <td className="cell-mono">{run.release_id || '—'}</td>
                                      <td className="cell-muted">{formatDateTime(run.start_time || run.created_at)}</td>
                                      <td className="cell-muted">{run.duration}</td>
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
              {sortedScripts.length === 0 && <tr><td colSpan={9} className="cell-muted">No scripts yet.</td></tr>}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
