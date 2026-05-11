import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../hooks/useApi'
import { Folder, Project, StoredScript } from '../types'
import ScriptEditor from './ScriptEditor'
import LoadRunnerImport from './LoadRunnerImport'
import RecordingStudio from './RecordingStudio'
import SampleScriptImport from './SampleScriptImport'
import FolderTree from './FolderTree'

type SubView = 'list' | 'create-options' | 'create' | 'edit' | 'import' | 'recorder' | 'sample'

interface Props {
  initialMode?: SubView
  initialEditId?: string
  activeProject?: Project | null
}

export default function ScriptsPage({ initialMode = 'list', initialEditId, activeProject }: Props) {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [subView, setSubView] = useState<SubView>(initialMode === 'create' ? 'create-options' : initialMode)
  const [editingId, setEditingId] = useState<string | null>(initialEditId || null)
  const [editingScript, setEditingScript] = useState<any>(null)
  const [draftScript, setDraftScript] = useState<any>(null)
  const [sortField, setSortField] = useState<string>('name')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [folders, setFolders] = useState<Folder[]>([])
  const [selectedFolderId, setSelectedFolderId] = useState<string | null | undefined>(undefined)

  const refresh = useCallback(async () => {
    try {
      const [scriptRes, folderRes] = await Promise.all([
        api.listScripts(activeProject?.id),
        activeProject?.id ? api.listFolders(activeProject.id, 'scripts') : Promise.resolve({ folders: [] }),
      ])
      setScripts(scriptRes.scripts || [])
      setFolders(folderRes.folders || [])
    } catch { /* API not available */ }
  }, [api, activeProject?.id])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => {
    setSelectedFolderId(undefined)
    setSelectedIds(new Set())
  }, [activeProject?.id])

  useEffect(() => {
    if (initialMode === 'edit' && initialEditId) {
      handleEdit(initialEditId)
    } else if (initialMode && initialMode !== subView) {
      setSubView(initialMode === 'create' ? 'create-options' : initialMode)
    }
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

  const handleSaved = () => { refresh(); setSubView('list'); setEditingId(null); setEditingScript(null); setDraftScript(null) }
  const handleImported = () => { refresh(); setSubView('list') }
  const handleSampleEdit = (script: any) => { setDraftScript(script); setEditingId(null); setEditingScript(null); setSubView('create') }

  const createFolder = async (parentId: string | null) => {
    if (!activeProject?.id) return
    const name = window.prompt('Folder name')
    if (!name?.trim()) return
    await api.createFolder(activeProject.id, { name: name.trim(), category: 'scripts', parent_id: parentId })
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

  const moveScript = async (id: string, folderId: string | null) => {
    await api.moveScript(id, folderId)
    refresh()
  }

  const handleSort = (field: string) => {
    if (sortField === field) {
      setSortDirection(sortDirection === 'asc' ? 'desc' : 'asc')
    } else {
      setSortField(field)
      setSortDirection('asc')
    }
  }

  const visibleScripts = scripts.filter((script) => {
    if (selectedFolderId === undefined) return true
    if (selectedFolderId === null) return !script.folder_id
    return script.folder_id === selectedFolderId
  })

  const sortedScripts = [...visibleScripts].sort((a: any, b: any) => {
    let vA = a[sortField]
    let vB = b[sortField]
    if (sortField === 'created_at') {
      vA = new Date(vA).getTime()
      vB = new Date(vB).getTime()
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
    if (selectedIds.size === visibleScripts.length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(visibleScripts.map((s) => s.id)))
    }
  }

  const toggleSelect = (id: string) => {
    const next = new Set(selectedIds)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setSelectedIds(next)
  }

  const handleDeleteSelected = async () => {
    if (!confirm(`Delete ${selectedIds.size} selected scripts?`)) return
    try {
      await Promise.all(Array.from(selectedIds).map((id) => api.deleteScript(id)))
      refresh()
      setSelectedIds(new Set())
    } catch { /* ignore */ }
  }

  if (subView === 'create') {
    return (
      <div className="page">
        <ScriptEditor initialScript={draftScript} projectId={activeProject?.id} folderId={selectedFolderId || null} onSave={handleSaved} onClose={() => setSubView('list')} />
      </div>
    )
  }

  if (subView === 'create-options') {
    return (
      <div className="page">
        <div className="page-header">
          <div>
            <h2>Create Script</h2>
            <p className="page-subtitle">Choose how you want to create a new script</p>
          </div>
          <button className="btn btn-secondary" onClick={() => setSubView('list')}>
            Back to Library
          </button>
        </div>

        <div className="script-create-grid">
          <button className="script-create-option" onClick={() => setSubView('create')}>
            <h3>Visual Editor</h3>
            <p>Build a script from scratch with actions, assertions, and extractors.</p>
          </button>

          <button className="script-create-option" onClick={() => setSubView('recorder')}>
            <h3>Script Recorder</h3>
            <p>Record browser traffic and convert captured requests into a script.</p>
          </button>

          <button className="script-create-option" onClick={() => setSubView('import')}>
            <h3>Import LoadRunner</h3>
            <p>Upload a LoadRunner ZIP and convert it into Stormrunner DSL.</p>
          </button>

          <button className="script-create-option" onClick={() => setSubView('sample')}>
            <h3>Build From Sample</h3>
            <p>Paste a request/response, curl command, REST JSON, or HAR and generate a starter script with inferred details.</p>
          </button>
        </div>
      </div>
    )
  }

  if (subView === 'edit') {
    return (
      <div className="page">
        <ScriptEditor scriptId={editingId} initialScript={editingScript} projectId={activeProject?.id} folderId={selectedFolderId || null} onSave={handleSaved} onClose={() => setSubView('list')} />
      </div>
    )
  }

  if (subView === 'import') {
    return (
      <div className="page">
        <LoadRunnerImport onImported={handleImported} onClose={() => setSubView('list')} />
      </div>
    )
  }

  if (subView === 'recorder') {
    return (
      <div className="page">
        <button className="btn btn-secondary" onClick={() => setSubView('create-options')} style={{ alignSelf: 'flex-start' }}>
          Back to Create Script
        </button>
        <RecordingStudio activeProject={activeProject} />
      </div>
    )
  }

  if (subView === 'sample') {
    return (
      <div className="page">
        <button className="btn btn-secondary" onClick={() => setSubView('create-options')} style={{ alignSelf: 'flex-start' }}>
          Back to Create Script
        </button>
        <SampleScriptImport onImported={handleImported} onEdit={handleSampleEdit} onClose={() => setSubView('create-options')} />
      </div>
    )
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scripts</h2>
          <p className="page-subtitle">Manage scripts for {activeProject?.name || 'the selected project'}</p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          {selectedIds.size > 0 && (
            <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} onClick={handleDeleteSelected}>
              Delete Selected ({selectedIds.size})
            </button>
          )}
        </div>
      </div>

      <div className="folder-layout">
        <FolderTree
          title="Scripts"
          rootLabel={activeProject?.name || 'Root'}
          headerActions={<button className="btn btn-sm btn-primary" onClick={() => setSubView('create-options')}>+ Create Script</button>}
          category="scripts"
          folders={folders}
          items={scripts}
          selectedFolderId={selectedFolderId}
          onSelectFolder={setSelectedFolderId}
          onSelectItem={handleEdit}
          onCreateFolder={createFolder}
          onRenameFolder={renameFolder}
          onDeleteFolder={deleteFolder}
          onMoveItem={moveScript}
        />

        <div className="page-section">
          <h3 className="section-title">Script Library ({visibleScripts.length})</h3>
          {visibleScripts.length === 0 ? (
            <div className="empty-card">
              <div className="empty-icon">
                <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="16 18 22 12 16 6" />
                  <polyline points="8 6 2 12 8 18" />
                </svg>
              </div>
              <p>No scripts here yet.</p>
              <p className="empty-hint">Click "Create Script" to choose Visual Editor, Script Recorder, sample-based import, or LoadRunner import.</p>
            </div>
          ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th style={{ width: 40 }}>
                    <input
                      type="checkbox"
                      checked={visibleScripts.length > 0 && selectedIds.size === visibleScripts.length}
                      onChange={toggleSelectAll}
                    />
                  </th>
                  <th onClick={() => handleSort('name')} className="sortable">Name <SortIcon field="name" /></th>
                  <th onClick={() => handleSort('protocol')} className="sortable">Protocol <SortIcon field="protocol" /></th>
                  <th onClick={() => handleSort('version')} className="sortable">Version <SortIcon field="version" /></th>
                  <th onClick={() => handleSort('created_at')} className="sortable">Date Added <SortIcon field="created_at" /></th>
                  <th>ID</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {sortedScripts.map((s) => (
                  <tr key={s.id} className={selectedIds.has(s.id) ? 'selected' : ''}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selectedIds.has(s.id)}
                        onChange={() => toggleSelect(s.id)}
                      />
                    </td>
                    <td className="cell-name">{s.name}</td>
                    <td><span className="protocol-badge">{s.protocol}</span></td>
                    <td>{s.version || '—'}</td>
                    <td className="cell-muted">{new Date(s.created_at).toLocaleDateString()}</td>
                    <td className="cell-mono">{s.id.slice(0, 8)}</td>
                    <td style={{ textAlign: 'right' }}>
                      <div style={{ display: 'flex', gap: 4, justifyContent: 'flex-end' }}>
                        <button className="btn btn-sm btn-ghost" onClick={() => handleEdit(s.id)}>Edit</button>
                        <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => handleDelete(s.id)}>Delete</button>
                      </div>
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
