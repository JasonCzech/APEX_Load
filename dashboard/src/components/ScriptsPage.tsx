import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../hooks/useApi'
import { StoredScript } from '../types'
import ScriptEditor from './ScriptEditor'
import LoadRunnerImport from './LoadRunnerImport'

type SubView = 'list' | 'create' | 'edit' | 'import'

interface Props {
  initialMode?: SubView
  initialEditId?: string
}

export default function ScriptsPage({ initialMode = 'list', initialEditId }: Props) {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [subView, setSubView] = useState<SubView>(initialMode)
  const [editingId, setEditingId] = useState<string | null>(initialEditId || null)
  const [editingScript, setEditingScript] = useState<any>(null)
  const [sortField, setSortField] = useState<string>('name')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts()
      setScripts(res.scripts || [])
    } catch { /* API not available */ }
  }, [api])

  useEffect(() => { refresh() }, [refresh])

  useEffect(() => {
    if (initialMode === 'edit' && initialEditId) {
      handleEdit(initialEditId)
    } else if (initialMode && initialMode !== subView) {
      setSubView(initialMode)
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

  const handleSaved = () => { refresh(); setSubView('list'); setEditingId(null); setEditingScript(null) }
  const handleImported = () => { refresh(); setSubView('list') }

  const handleSort = (field: string) => {
    if (sortField === field) {
      setSortDirection(sortDirection === 'asc' ? 'desc' : 'asc')
    } else {
      setSortField(field)
      setSortDirection('asc')
    }
  }

  const sortedScripts = [...scripts].sort((a: any, b: any) => {
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
    if (selectedIds.size === scripts.length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(scripts.map((s) => s.id)))
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
        <ScriptEditor onSave={handleSaved} onClose={() => setSubView('list')} />
      </div>
    )
  }

  if (subView === 'edit') {
    return (
      <div className="page">
        <ScriptEditor scriptId={editingId} initialScript={editingScript} onSave={handleSaved} onClose={() => setSubView('list')} />
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

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scripts</h2>
          <p className="page-subtitle">Manage your test scripts and upload new ones</p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          {selectedIds.size > 0 && (
            <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} onClick={handleDeleteSelected}>
              Delete Selected ({selectedIds.size})
            </button>
          )}
          <button className="btn btn-ghost" onClick={() => setSubView('import')}>
            <span style={{ marginRight: 6 }}>⬆</span>Import LoadRunner
          </button>
          <button className="btn btn-primary" onClick={() => setSubView('create')}>+ Create Script</button>
        </div>
      </div>

      <div className="page-section">
        <h3 className="section-title">Script Library ({scripts.length})</h3>
        {scripts.length === 0 ? (
          <div className="empty-card">
            <div className="empty-icon">
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="16 18 22 12 16 6" />
                <polyline points="8 6 2 12 8 18" />
              </svg>
            </div>
            <p>No scripts uploaded yet.</p>
            <p className="empty-hint">Click "Create Script" to build one with the visual editor, or "Import LoadRunner" to convert a VUser script.</p>
          </div>
        ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th style={{ width: 40 }}>
                    <input
                      type="checkbox"
                      checked={scripts.length > 0 && selectedIds.size === scripts.length}
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
  )
}
