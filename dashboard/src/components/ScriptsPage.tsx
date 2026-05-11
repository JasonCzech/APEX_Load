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
        renderItemExtra={(item) => {
          const s = scripts.find((x) => x.id === item.id)
          return (
            <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginLeft: 'auto', flexShrink: 0 }}>
              {s && <span className="protocol-badge">{s.protocol}</span>}
              <button className="btn btn-sm btn-ghost" onClick={(e) => { e.stopPropagation(); handleEdit(item.id) }}>Edit</button>
              <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(e) => { e.stopPropagation(); handleDelete(item.id) }}>Delete</button>
            </div>
          )
        }}
      />
    </div>
  )
}
