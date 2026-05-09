import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../hooks/useApi'
import { StoredScript } from '../types'
import ScriptEditor from './ScriptEditor'
import LoadRunnerImport from './LoadRunnerImport'

type SubView = 'list' | 'create' | 'edit' | 'import'

export default function ScriptsPage() {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [subView, setSubView] = useState<SubView>('list')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editingScript, setEditingScript] = useState<any>(null)

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts()
      setScripts(res.scripts || [])
    } catch { /* API not available */ }
  }, [api])

  useEffect(() => { refresh() }, [refresh])

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
                  <th>Name</th>
                  <th>Protocol</th>
                  <th>Version</th>
                  <th>Created</th>
                  <th>ID</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {scripts.map((s) => (
                  <tr key={s.id}>
                    <td className="cell-name">{s.name}</td>
                    <td><span className="protocol-badge">{s.protocol}</span></td>
                    <td>{s.version || '—'}</td>
                    <td className="cell-muted">{new Date(s.created_at).toLocaleDateString()}</td>
                    <td className="cell-mono">{s.id.slice(0, 8)}</td>
                    <td>
                      <div style={{ display: 'flex', gap: 4 }}>
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
