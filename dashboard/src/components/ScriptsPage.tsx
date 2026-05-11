import { useCallback, useEffect, useMemo, useState } from 'react'
import { StoredScript } from '../types'
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

function releaseList(script: StoredScript) {
  const releases = script.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return releases.map(release => release.name).join(', ')
}

export default function ScriptsPage({ initialMode = 'list', initialEditId }: Props) {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [subView, setSubView] = useState<SubView>(initialMode === 'create' ? 'create-options' : initialMode)
  const [editingId, setEditingId] = useState<string | null>(initialEditId || null)
  const [editingScript, setEditingScript] = useState<any>(null)
  const [draftScript, setDraftScript] = useState<any>(null)
  const [sortField, setSortField] = useState<'name' | 'created_at' | 'last_run_at'>('name')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc')

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
      const aValue = sortField.includes('_at') ? new Date(a[sortField] || 0).getTime() : a[sortField]
      const bValue = sortField.includes('_at') ? new Date(b[sortField] || 0).getTime() : b[sortField]
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [scripts, sortField, sortDirection])

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else { setSortField(field); setSortDirection('asc') }
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
                <th>Protocol</th>
                <th>Version</th>
                <th onClick={() => handleSort('created_at')} className="sortable">Created</th>
                <th>Linked Scenarios</th>
                <th>Associated Releases</th>
                <th onClick={() => handleSort('last_run_at')} className="sortable">Last Run</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedScripts.map(script => (
                <tr key={script.id}>
                  <td className="cell-name">{script.name}</td>
                  <td><span className="protocol-badge">{script.protocol}</span></td>
                  <td>{script.version || '—'}</td>
                  <td className="cell-muted">{formatDate(script.created_at)}</td>
                  <td>{script.scenario_count || 0}</td>
                  <td className="cell-muted">{releaseList(script)}</td>
                  <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                  <td style={{ textAlign: 'right' }}>
                    <button className="btn btn-sm btn-ghost" onClick={() => handleEdit(script.id)}>Edit</button>
                    <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => handleDelete(script.id)}>Delete</button>
                  </td>
                </tr>
              ))}
              {sortedScripts.length === 0 && <tr><td colSpan={8} className="cell-muted">No scripts yet.</td></tr>}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
