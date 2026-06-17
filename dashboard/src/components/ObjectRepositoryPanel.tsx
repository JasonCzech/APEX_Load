import { useCallback, useEffect, useState } from 'react'

import { useObjectRepositoryApi } from '../hooks/api/useObjectRepositoryApi'
import { ApiError } from '../hooks/apiClient'
import { ObjectRepository } from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import './parityPanels.css'

interface Props {
  projectId?: string
  canManage?: boolean
}

function formatTimestamp(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

// Project-scoped catalog of reusable named UI element descriptors (the
// LoadRunner/TruClient object-repository analog). Create empty, or seed from a
// script's captured selectors.
export default function ObjectRepositoryPanel({ projectId, canManage = false }: Props) {
  const api = useObjectRepositoryApi()
  const [repositories, setRepositories] = useState<ObjectRepository[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const [createForm, setCreateForm] = useState({ name: '', description: '' })
  const [importForm, setImportForm] = useState({ scriptId: '', name: '' })
  const [expanded, setExpanded] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ObjectRepository | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    if (!projectId) {
      setRepositories([])
      return
    }
    setLoading(true)
    setError(null)
    try {
      const res = await api.listObjectRepositories({ project_id: projectId })
      setRepositories(res.object_repositories || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load object repositories.')
      setRepositories([])
    } finally {
      setLoading(false)
    }
  }, [api, projectId])

  useEffect(() => { void load() }, [load])

  const runAction = async (action: () => Promise<unknown>, onDone?: () => void) => {
    setBusy(true)
    setError(null)
    try {
      await action()
      onDone?.()
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Action failed.')
    } finally {
      setBusy(false)
    }
  }

  const handleCreate = () => {
    if (!projectId || !createForm.name.trim()) return
    void runAction(
      () => api.createObjectRepository({ project_id: projectId, name: createForm.name.trim(), description: createForm.description.trim() || undefined, objects: [] }),
      () => { setShowCreate(false); setCreateForm({ name: '', description: '' }) },
    )
  }

  const handleImport = () => {
    if (!importForm.scriptId.trim()) return
    void runAction(
      () => api.createObjectRepositoryFromScript({ script_id: importForm.scriptId.trim(), name: importForm.name.trim() || undefined }),
      () => setImportForm({ scriptId: '', name: '' }),
    )
  }

  return (
    <div className="pp-panel" aria-label="Object repositories">
      <div className="pp-toolbar">
        <h3 className="pp-toolbar-title">Object Repositories</h3>
        {canManage && projectId && (
          <button type="button" className="btn btn-primary btn-sm" onClick={() => setShowCreate(value => !value)} disabled={busy}>
            New repository
          </button>
        )}
      </div>

      {error && <div className="pp-error">{error}</div>}
      {!projectId && <div className="pp-empty">Select a project to view its object repositories.</div>}

      {canManage && projectId && showCreate && (
        <div className="pp-field-row">
          <input className="pp-field" placeholder="Repository name" value={createForm.name} onChange={e => setCreateForm(f => ({ ...f, name: e.target.value }))} />
          <input className="pp-field" placeholder="Description (optional)" value={createForm.description} onChange={e => setCreateForm(f => ({ ...f, description: e.target.value }))} />
          <button type="button" className="btn btn-primary btn-sm" onClick={handleCreate} disabled={busy || !createForm.name.trim()}>Create</button>
        </div>
      )}

      {canManage && projectId && (
        <div className="pp-field-row">
          <input className="pp-field" placeholder="Seed from script id…" value={importForm.scriptId} onChange={e => setImportForm(f => ({ ...f, scriptId: e.target.value }))} />
          <input className="pp-field" placeholder="Repository name (optional)" value={importForm.name} onChange={e => setImportForm(f => ({ ...f, name: e.target.value }))} />
          <button type="button" className="btn btn-secondary btn-sm" onClick={handleImport} disabled={busy || !importForm.scriptId.trim()}>Import from script</button>
        </div>
      )}

      {loading && <p className="pp-muted">Loading…</p>}
      {!loading && projectId && repositories.length === 0 && <div className="pp-empty">No object repositories yet.</div>}

      <div className="pp-list">
        {repositories.map(repo => (
          <div key={repo.id} className="pp-row">
            <div className="pp-row-main">
              <button type="button" className="pp-row-title pp-row-title-button" onClick={() => setExpanded(id => id === repo.id ? null : repo.id)}>
                {repo.name}
                <span className="pp-pill pp-pill-muted">{repo.objects?.length || 0} object{(repo.objects?.length || 0) === 1 ? '' : 's'}</span>
              </button>
              <div className="pp-row-meta">
                {repo.description ? `${repo.description} · ` : ''}{repo.source ? `${repo.source} · ` : ''}updated {formatTimestamp(repo.updated_at)}
              </div>
              {expanded === repo.id && (repo.objects?.length || 0) > 0 && (
                <ul className="pp-sublist">
                  {repo.objects.slice(0, 25).map((object, index) => (
                    <li key={`${repo.id}-${index}`} className="pp-subrow">
                      <strong>{object.logical_name}</strong>
                      <code>{object.css || object.xpath || object.role || object.text || '—'}</code>
                      {typeof object.best_confidence === 'number' && object.best_confidence > 0 && (
                        <span className="pp-pill pp-pill-muted">{object.best_confidence}%</span>
                      )}
                    </li>
                  ))}
                  {repo.objects.length > 25 && <li className="pp-muted">+{repo.objects.length - 25} more</li>}
                </ul>
              )}
            </div>
            {canManage && (
              <div className="pp-row-actions">
                <button type="button" className="btn btn-danger btn-sm" onClick={() => setDeleteTarget(repo)} disabled={busy}>Delete</button>
              </div>
            )}
          </div>
        ))}
      </div>

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete object repository"
          description={`This permanently deletes “${deleteTarget.name}” and its ${deleteTarget.objects?.length || 0} objects. Scripts referencing it will lose the shared descriptors.`}
          target={deleteTarget.name}
          targetLabel="Repository"
          confirmLabel="Delete repository"
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => {
            const target = deleteTarget
            setDeleteTarget(null)
            void runAction(() => api.deleteObjectRepository(target.id))
          }}
        />
      )}
    </div>
  )
}
