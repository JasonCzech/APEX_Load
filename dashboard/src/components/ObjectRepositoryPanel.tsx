import { useCallback, useEffect, useState } from 'react'

import { useObjectRepositoryApi } from '../hooks/api/useObjectRepositoryApi'
import { ApiError } from '../hooks/apiClient'
import { ObjectRepository, RepositoryObject } from '../types'
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
  const [importForm, setImportForm] = useState({ scriptId: '', name: '', bindScript: false })
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
      () => api.createObjectRepositoryFromScript({ script_id: importForm.scriptId.trim(), name: importForm.name.trim() || undefined, bind_script: importForm.bindScript || undefined }),
      () => setImportForm({ scriptId: '', name: '', bindScript: false }),
    )
  }

  // Promote a candidate to be the object's primary locator: raise its confidence
  // above the others (so script-prep resolution picks it) and mirror it into the
  // matching descriptor field, then persist the whole repository.
  const promoteCandidate = (repo: ObjectRepository, objectIndex: number, candidateIndex: number) => {
    const objects = repo.objects.map((object, oi): RepositoryObject => {
      if (oi !== objectIndex) return object
      const candidates = object.candidates || []
      const target = candidates[candidateIndex]
      if (!target) return object
      const maxConfidence = candidates.reduce((max, c) => Math.max(max, c.confidence || 0), 0)
      const promoted = Math.min(100, Math.max(maxConfidence + 1, target.confidence || 0))
      const next: RepositoryObject = {
        ...object,
        best_confidence: promoted,
        candidates: candidates.map((c, ci) => (ci === candidateIndex ? { ...c, confidence: promoted } : c)),
      }
      if (target.kind === 'css') next.css = target.value
      else if (target.kind === 'xpath') next.xpath = target.value
      else if (target.kind === 'role') next.role = target.value
      else if (target.kind === 'text') next.text = target.value
      return next
    })
    void runAction(() => api.updateObjectRepository(repo.id, { name: repo.name, description: repo.description, objects }))
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
          <label className="pp-check" title="Rewrite the script's actions to reference the new repository by object_ref (keeps inline selectors as fallback).">
            <input type="checkbox" checked={importForm.bindScript} onChange={e => setImportForm(f => ({ ...f, bindScript: e.target.checked }))} />
            Bind script
          </label>
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
                  {repo.objects.slice(0, 25).map((object, objectIndex) => {
                    const ranked = (object.candidates || [])
                      .map((candidate, ci) => ({ candidate, ci }))
                      .sort((a, b) => (b.candidate.confidence || 0) - (a.candidate.confidence || 0))
                    return (
                      <li key={`${repo.id}-${objectIndex}`} className="pp-subrow pp-subrow-object">
                        <div className="pp-subrow-head">
                          <strong>{object.logical_name}</strong>
                          <code>{object.css || object.xpath || object.role || object.text || '—'}</code>
                          {typeof object.best_confidence === 'number' && object.best_confidence > 0 && (
                            <span className="pp-pill pp-pill-muted">{object.best_confidence}%</span>
                          )}
                        </div>
                        {ranked.length > 0 && (
                          <ul className="pp-candidates">
                            {ranked.map(({ candidate, ci }, rank) => {
                              const healed = (candidate.reason || '').toLowerCase().includes('self-heal')
                              return (
                                <li key={ci} className="pp-candidate">
                                  <span className="pp-pill pp-pill-kind">{candidate.kind}</span>
                                  <code className="pp-candidate-value">{candidate.value}</code>
                                  <span className="pp-pill pp-pill-muted">{candidate.confidence}%</span>
                                  {healed && <span className="pp-pill pp-pill-heal" title={candidate.reason}>self-healed</span>}
                                  {canManage && rank !== 0 && (
                                    <button type="button" className="btn btn-secondary btn-sm pp-promote" disabled={busy} onClick={() => promoteCandidate(repo, objectIndex, ci)}>Promote</button>
                                  )}
                                </li>
                              )
                            })}
                          </ul>
                        )}
                      </li>
                    )
                  })}
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
