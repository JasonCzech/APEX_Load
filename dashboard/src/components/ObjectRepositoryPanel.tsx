import { lazy, Suspense, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { useObjectRepositoryApi } from '../hooks/api/useObjectRepositoryApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { ObjectRepository, RepositoryObject } from '../types'
import './parityPanels.css'
import ScriptPicker, { type ScriptOption } from './ScriptPicker'
import { useDisclosedFormFocus } from './useDisclosedFormFocus'
import { formatDateTime } from '../utils/dateTime'

const ConfirmDestructiveModal = lazy(() => import('./ConfirmDestructiveModal'))

interface Props {
  projectId?: string
  canManage?: boolean
  /** The project's scripts, when the page already holds them. */
  scripts?: ScriptOption[]
}

const CATALOG_PAGE_SIZE = 100
const MAX_CATALOG_PAGES = 1_000

// A new repository starts from a script's recorded elements, or empty.
interface NewRepositoryForm { source: 'script' | 'empty'; scriptId: string; name: string; description: string; bindScript: boolean }
const blankNewRepository: NewRepositoryForm = { source: 'script', scriptId: '', name: '', description: '', bindScript: false }

function snapshotCaughtUp(listed: ObjectRepository, committed: ObjectRepository): boolean {
  if (JSON.stringify(listed) === JSON.stringify(committed)) return true
  const listedTimestamp = Date.parse(listed.updated_at || '')
  const committedTimestamp = Date.parse(committed.updated_at || '')
  return Number.isFinite(listedTimestamp)
    && Number.isFinite(committedTimestamp)
    && listedTimestamp > committedTimestamp
}

// Shared formatter: dates read the same, zone included, as the rest of the app.
function formatTimestamp(value?: string): string {
  return formatDateTime(value, { seconds: true, fallback: '—' })
}

// Project-scoped catalog of reusable named UI element descriptors (the
// LoadRunner/TruClient object-repository analog). Seed one from a script's
// captured selectors, or create it empty.
export default function ObjectRepositoryPanel({ projectId, canManage = false, scripts }: Props) {
  const api = useObjectRepositoryApi()
  const titleId = useId()
  const sourceName = useId()
  const mountedRef = useRef(false)
  const [repositories, setRepositories] = useState<ObjectRepository[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const [newRepository, setNewRepository] = useState<NewRepositoryForm>(blankNewRepository)
  const { openerRef, formRef } = useDisclosedFormFocus(showCreate)
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
    setNewRepository(blankNewRepository)
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

  const newRepositoryReady = newRepository.source === 'script' ? Boolean(newRepository.scriptId) : Boolean(newRepository.name.trim())

  const handleCreate = (): Promise<boolean> => {
    const requestedProject = projectId
    const form = newRepository
    if (!requestedProject || !newRepositoryReady) return Promise.resolve(false)
    return runAction(
      async () => {
        if (form.source === 'empty') {
          return api.createObjectRepository({ project_id: requestedProject, name: form.name.trim(), description: form.description.trim() || undefined, objects: [] })
        }
        const committed = await api.createObjectRepositoryFromScript({
          project_id: requestedProject,
          script_id: form.scriptId,
          name: form.name.trim() || undefined,
          bind_script: form.bindScript || undefined,
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
        setShowCreate(false)
        setNewRepository(blankNewRepository)
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

  // Only the fields of the chosen source count: they are the ones on screen.
  const draftDirty = showCreate && (newRepository.source === 'script'
    ? Boolean(newRepository.scriptId || newRepository.name.trim() || newRepository.bindScript)
    : Boolean(newRepository.name.trim() || newRepository.description.trim()))
  const draftDirtyRef = useRef(draftDirty)
  draftDirtyRef.current = draftDirty
  const saveDraftRef = useRef<() => Promise<boolean>>(async () => false)
  saveDraftRef.current = async () => {
    if (!newRepositoryReady) {
      setError(newRepository.source === 'script' ? 'Choose the script to seed the repository from, or cancel it.' : 'Name the repository, or cancel it.')
      return false
    }
    return handleCreate()
  }
  const discardDraftRef = useRef<() => void>(() => undefined)
  discardDraftRef.current = () => {
    setShowCreate(false)
    setNewRepository(blankNewRepository)
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
    <section className="pp-panel" aria-labelledby={titleId}>
      <div className="pp-toolbar">
        <div className="pp-toolbar-copy">
          <h3 id={titleId} className="pp-panel-title">Object repositories</h3>
          <p className="pp-muted">Named page elements that browser scripts share. When a page changes, fix the element here once instead of in every script.</p>
        </div>
        {/* The form closes through its own Cancel, so a draft is never hidden. The
            opener stays enabled so focus can return to it when a create completes. */}
        {canManage && projectId && !showCreate && (
          <button ref={openerRef} type="button" className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
            New repository
          </button>
        )}
      </div>

      {error && <div className="pp-error" role="alert">{error}</div>}
      {!projectId && <div className="pp-empty">Select a project to view its object repositories.</div>}

      {canManage && projectId && showCreate && (
        <form ref={formRef} className="pp-create" aria-label="New object repository" onSubmit={event => { event.preventDefault(); void handleCreate() }}>
          <fieldset className="pp-choice">
            <legend>Start from</legend>
            <label><input type="radio" name={sourceName} checked={newRepository.source === 'script'} onChange={() => setNewRepository(form => ({ ...form, source: 'script' }))} /> A script&apos;s recorded elements</label>
            <label><input type="radio" name={sourceName} checked={newRepository.source === 'empty'} onChange={() => setNewRepository(form => ({ ...form, source: 'empty' }))} /> An empty repository</label>
          </fieldset>
          {newRepository.source === 'script' ? (
            <>
              <ScriptPicker
                projectId={projectId}
                label="Script"
                hint="Use a browser script: each of its actions' element selectors becomes an object."
                value={newRepository.scriptId}
                scripts={scripts}
                disabled={busy}
                onChange={scriptId => setNewRepository(form => ({ ...form, scriptId }))}
              />
              <label className="pp-field">
                <span>Name (optional)</span>
                <input placeholder="The script's name, then “objects”" value={newRepository.name} onChange={event => setNewRepository(form => ({ ...form, name: event.target.value }))} />
              </label>
              <label className="pp-check pp-check-block">
                <input type="checkbox" checked={newRepository.bindScript} onChange={event => setNewRepository(form => ({ ...form, bindScript: event.target.checked }))} />
                <span>Point the script&apos;s actions at this repository <span className="pp-muted">(each keeps its own selector as a fallback)</span></span>
              </label>
            </>
          ) : (
            <>
              <label className="pp-field">
                <span>Name</span>
                <input value={newRepository.name} onChange={event => setNewRepository(form => ({ ...form, name: event.target.value }))} />
              </label>
              <label className="pp-field">
                <span>Description (optional)</span>
                <input value={newRepository.description} onChange={event => setNewRepository(form => ({ ...form, description: event.target.value }))} />
              </label>
            </>
          )}
          <div className="pp-create-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !newRepositoryReady}>Create repository</button>
            <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => discardDraftRef.current()}>Cancel</button>
          </div>
        </form>
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
    </section>
  )
}
