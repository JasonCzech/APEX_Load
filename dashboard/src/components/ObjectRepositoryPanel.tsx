import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { useObjectRepositoryApi } from '../hooks/api/useObjectRepositoryApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { ObjectRepository, RepositoryObject } from '../types'
import './parityPanels.css'

const ConfirmDestructiveModal = lazy(() => import('./ConfirmDestructiveModal'))

interface Props {
  projectId?: string
  canManage?: boolean
}

const CATALOG_PAGE_SIZE = 100
const MAX_CATALOG_PAGES = 1_000

function snapshotCaughtUp(listed: ObjectRepository, committed: ObjectRepository): boolean {
  if (JSON.stringify(listed) === JSON.stringify(committed)) return true
  const listedTimestamp = Date.parse(listed.updated_at || '')
  const committedTimestamp = Date.parse(committed.updated_at || '')
  return Number.isFinite(listedTimestamp)
    && Number.isFinite(committedTimestamp)
    && listedTimestamp > committedTimestamp
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
  const mountedRef = useRef(false)
  const [repositories, setRepositories] = useState<ObjectRepository[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const [createForm, setCreateForm] = useState({ name: '', description: '' })
  const [importForm, setImportForm] = useState({ scriptId: '', name: '', bindScript: false })
  const [expanded, setExpanded] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ObjectRepository | null>(null)
  const [busy, setBusy] = useState(false)
  const actionSequenceRef = useRef(0)
  const activeActionRef = useRef<{ id: number; projectId?: string; generation: number } | null>(null)
  const committedRepositoriesRef = useRef(new Map<string, ObjectRepository>())
  const deletedRepositoryIDsRef = useRef(new Set<string>())
  const scopeRef = useRef({ projectId, generation: 0 })
  const loadGenerationRef = useRef(0)
  if (scopeRef.current.projectId !== projectId) {
    scopeRef.current = { projectId, generation: scopeRef.current.generation + 1 }
  }
  const isCurrentScope = useCallback((scope: { projectId?: string; generation: number }) => (
    scopeRef.current.projectId === scope.projectId && scopeRef.current.generation === scope.generation
  ), [])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useLayoutEffect(() => {
    loadGenerationRef.current += 1
    setRepositories([])
    setLoading(false)
    setError(null)
    setShowCreate(false)
    setCreateForm({ name: '', description: '' })
    setImportForm({ scriptId: '', name: '', bindScript: false })
    setExpanded(null)
    setDeleteTarget(null)
    setBusy(false)
    activeActionRef.current = null
    committedRepositoriesRef.current.clear()
    deletedRepositoryIDsRef.current.clear()
  }, [projectId])

  const load = useCallback(async () => {
    const scope = { ...scopeRef.current }
    const requestGeneration = ++loadGenerationRef.current
    if (!projectId) {
      if (mountedRef.current && isCurrentScope(scope)) setRepositories([])
      return
    }
    if (!mountedRef.current) return
    setLoading(true)
    setError(null)
    try {
      const listedByID = new Map<string, ObjectRepository>()
      const pageSignatures = new Set<string>()
      let offset = 0
      for (let pageNumber = 0; pageNumber < MAX_CATALOG_PAGES; pageNumber += 1) {
        const res = await api.listObjectRepositories({ project_id: projectId, limit: CATALOG_PAGE_SIZE, offset })
        if (!mountedRef.current || !isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
        const page = res.object_repositories || []
        page.forEach(repository => listedByID.set(repository.id, repository))
        if (page.length < CATALOG_PAGE_SIZE) break
        const signature = page.map(repository => repository.id).join('\u0000')
        if (pageSignatures.has(signature)) throw new ApiError('Object repository pagination did not advance.', 502)
        pageSignatures.add(signature)
        const responseOffset = typeof res.offset === 'number' ? res.offset : offset
        const nextOffset = responseOffset + page.length
        if (nextOffset <= offset) throw new ApiError('Object repository pagination did not advance.', 502)
        offset = nextOffset
        if (pageNumber === MAX_CATALOG_PAGES - 1) throw new ApiError('Object repository catalog exceeded the supported page limit.', 502)
      }
      if (!mountedRef.current || !isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
      const listed = [...listedByID.values()]
      const listedIDs = new Set(listed.map(repository => repository.id))
      deletedRepositoryIDsRef.current.forEach(id => {
        if (!listedIDs.has(id)) deletedRepositoryIDsRef.current.delete(id)
      })
      const next = listed.filter(repository => !deletedRepositoryIDsRef.current.has(repository.id))
      committedRepositoriesRef.current.forEach((repository, id) => {
        if (deletedRepositoryIDsRef.current.has(id)) return
        const listedRepository = listed.find(candidate => candidate.id === id)
        if (listedRepository && snapshotCaughtUp(listedRepository, repository)) {
          committedRepositoriesRef.current.delete(id)
          return
        }
        const index = next.findIndex(candidate => candidate.id === id)
        if (index >= 0) next[index] = repository
        else next.push(repository)
      })
      setRepositories(next.sort((left, right) => left.name.localeCompare(right.name)))
    } catch (err) {
      if (!mountedRef.current || !isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
      setError(err instanceof ApiError ? err.message : 'Failed to load object repositories.')
    } finally {
      if (mountedRef.current && isCurrentScope(scope) && requestGeneration === loadGenerationRef.current) setLoading(false)
    }
  }, [api, isCurrentScope, projectId])

  useEffect(() => { void load() }, [load])

  const runAction = async <T,>(action: () => Promise<T>, onDone?: (result: T) => void): Promise<boolean> => {
    if (!mountedRef.current || activeActionRef.current) return false
    const scope = { ...scopeRef.current }
    const operation = { id: ++actionSequenceRef.current, ...scope }
    activeActionRef.current = operation
    setBusy(true)
    setError(null)
    try {
      const result = await action()
      if (!mountedRef.current || !isCurrentScope(scope) || activeActionRef.current !== operation) return false
      onDone?.(result)
      await load()
      return true
    } catch (err) {
      if (!mountedRef.current || !isCurrentScope(scope) || activeActionRef.current !== operation) return false
      setError(err instanceof ApiError ? err.message : 'Action failed.')
      return false
    } finally {
      if (activeActionRef.current === operation) {
        activeActionRef.current = null
        if (mountedRef.current && isCurrentScope(scope)) setBusy(false)
      }
    }
  }

  const handleCreate = (): Promise<boolean> => {
    if (!projectId || !createForm.name.trim()) return Promise.resolve(false)
    return runAction(
      () => api.createObjectRepository({ project_id: projectId, name: createForm.name.trim(), description: createForm.description.trim() || undefined, objects: [] }),
      committed => {
        committedRepositoriesRef.current.set(committed.id, committed)
        deletedRepositoryIDsRef.current.delete(committed.id)
        setRepositories(current => [...current.filter(repository => repository.id !== committed.id), committed].sort((left, right) => left.name.localeCompare(right.name)))
        setShowCreate(false)
        setCreateForm({ name: '', description: '' })
      },
    )
  }

  const handleImport = (): Promise<boolean> => {
    const requestedProject = projectId
    if (!requestedProject || !importForm.scriptId.trim()) return Promise.resolve(false)
    return runAction(
      async () => {
        const committed = await api.createObjectRepositoryFromScript({
          project_id: requestedProject,
          script_id: importForm.scriptId.trim(),
          name: importForm.name.trim() || undefined,
          bind_script: importForm.bindScript || undefined,
        })
        if ((committed.project_id || '').trim() !== requestedProject) {
          throw new ApiError('The selected script belongs to a different project. Select a script from the active project.', 409)
        }
        return committed
      },
      committed => {
        committedRepositoriesRef.current.set(committed.id, committed)
        deletedRepositoryIDsRef.current.delete(committed.id)
        setRepositories(current => [...current.filter(repository => repository.id !== committed.id), committed].sort((left, right) => left.name.localeCompare(right.name)))
        setImportForm({ scriptId: '', name: '', bindScript: false })
      },
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
    // Send the current auto_promote_on_heal so a candidate promotion never
    // implicitly resets it (the server also preserves omitted fields, but being
    // explicit keeps the client intent unambiguous).
    void runAction(
      () => api.updateObjectRepository(repo.id, { name: repo.name, description: repo.description, objects, auto_promote_on_heal: repo.auto_promote_on_heal }),
      committed => {
        committedRepositoriesRef.current.set(committed.id, committed)
        setRepositories(current => current.map(repository => repository.id === committed.id ? committed : repository))
      },
    )
  }

  const toggleAutoPromote = (repo: ObjectRepository) => {
    void runAction(
      () => api.updateObjectRepository(repo.id, {
        name: repo.name,
        description: repo.description,
        objects: repo.objects,
        auto_promote_on_heal: !repo.auto_promote_on_heal,
      }),
      committed => {
        committedRepositoriesRef.current.set(committed.id, committed)
        setRepositories(current => current.map(repository => repository.id === committed.id ? committed : repository))
      },
    )
  }

  const draftDirty = Boolean(
    (showCreate && (createForm.name.trim() || createForm.description.trim()))
    || importForm.scriptId.trim()
    || importForm.name.trim()
    || importForm.bindScript,
  )
  const draftDirtyRef = useRef(draftDirty)
  draftDirtyRef.current = draftDirty
  const saveDraftRef = useRef<() => Promise<boolean>>(async () => false)
  saveDraftRef.current = async () => {
    if (showCreate && (createForm.name.trim() || createForm.description.trim()) && !(await handleCreate())) return false
    if ((importForm.scriptId.trim() || importForm.name.trim() || importForm.bindScript) && !(await handleImport())) return false
    return true
  }
  const discardDraftRef = useRef<() => void>(() => undefined)
  discardDraftRef.current = () => {
    setShowCreate(false)
    setCreateForm({ name: '', description: '' })
    setImportForm({ scriptId: '', name: '', bindScript: false })
    setError(null)
  }
  const navigationGuard = useMemo<NavigationGuard>(() => ({
    id: 'object-repository-draft',
    label: 'Object repository draft',
    isDirty: () => draftDirtyRef.current,
    save: () => saveDraftRef.current(),
    discard: () => discardDraftRef.current(),
  }), [])
  useNavigationGuard(navigationGuard, draftDirty)

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
          <button type="button" className="btn btn-primary btn-sm" onClick={() => { void handleCreate() }} disabled={busy || !createForm.name.trim()}>Create</button>
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
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => { void handleImport() }} disabled={busy || !importForm.scriptId.trim()}>Import from script</button>
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
                <label className="pp-check" title="When a bound action self-heals during replay, promote the working locator to this object's primary automatically.">
                  <input type="checkbox" checked={!!repo.auto_promote_on_heal} disabled={busy} onChange={() => toggleAutoPromote(repo)} />
                  Auto-promote heals
                </label>
                <button type="button" className="btn btn-danger btn-sm" onClick={() => setDeleteTarget(repo)} disabled={busy}>Delete</button>
              </div>
            )}
          </div>
        ))}
      </div>

      {deleteTarget && (
        <Suspense fallback={null}>
          <ConfirmDestructiveModal
            title="Delete object repository"
            description={`This permanently deletes “${deleteTarget.name}” and its ${deleteTarget.objects?.length || 0} objects. Scripts referencing it will lose the shared descriptors.`}
            target={deleteTarget.name}
            targetLabel="Repository"
            confirmLabel="Delete repository"
            loading={busy}
            error={error}
            onCancel={() => setDeleteTarget(null)}
            onConfirm={() => {
              const target = deleteTarget
              void runAction(
                () => api.deleteObjectRepository(target.id),
                () => {
                  deletedRepositoryIDsRef.current.add(target.id)
                  committedRepositoriesRef.current.delete(target.id)
                  setRepositories(current => current.filter(repository => repository.id !== target.id))
                  setDeleteTarget(null)
                },
              )
            }}
          />
        </Suspense>
      )}
    </div>
  )
}
