import { lazy, Suspense, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useCorrelationRulesetApi } from '../hooks/api/useCorrelationRulesetApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { CorrelationRuleset, CorrelationRulesetApplyResponse } from '../types'
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

// Shared formatter: dates read the same, zone included, as the rest of the app.
function formatTimestamp(value?: string): string {
  return formatDateTime(value, { seconds: true, fallback: '—' })
}

function snapshotCaughtUp(
  listed: { updated_at?: string },
  committed: { updated_at?: string },
): boolean {
  if (JSON.stringify(listed) === JSON.stringify(committed)) return true
  const listedTimestamp = Date.parse(listed.updated_at || '')
  const committedTimestamp = Date.parse(committed.updated_at || '')
  return Number.isFinite(listedTimestamp)
    && Number.isFinite(committedTimestamp)
    && listedTimestamp > committedTimestamp
}

// Project-scoped catalog of learned, reusable correlation rules. Discover rules
// from a script's recorded baseline, then apply them to other scripts.
export default function CorrelationRulesetPanel({ projectId, canManage = false, scripts }: Props) {
  const api = useCorrelationRulesetApi()
  const titleId = useId()
  const mountedRef = useRef(false)
  const currentProjectRef = useRef(projectId)
  const loadRequestRef = useRef(0)
  const actionRequestRef = useRef(0)
  const actionInFlightRef = useRef(false)
  const applyRequestRef = useRef(0)
  const committedRulesetsRef = useRef(new Map<string, CorrelationRuleset>())
  const deletedRulesetIdsRef = useRef(new Set<string>())
  currentProjectRef.current = projectId
  const [rulesets, setRulesets] = useState<CorrelationRuleset[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loadError, setLoadError] = useState(false)
  const [showDiscover, setShowDiscover] = useState(false)
  const [discoverForm, setDiscoverForm] = useState({ scriptId: '', name: '' })
  const { openerRef, formRef } = useDisclosedFormFocus(showDiscover)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [applyForm, setApplyForm] = useState<{ rulesetId: string; scriptId: string }>({ rulesetId: '', scriptId: '' })
  const [applyResult, setApplyResult] = useState<CorrelationRulesetApplyResponse | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<CorrelationRuleset | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    // Project changes are an authorization-boundary change. Invalidate every
    // request issued for the prior project and clear state that references its
    // rulesets before loading the new project.
    loadRequestRef.current += 1
    actionRequestRef.current += 1
    applyRequestRef.current += 1
    actionInFlightRef.current = false
    committedRulesetsRef.current.clear()
    deletedRulesetIdsRef.current.clear()
    setRulesets([])
    setLoading(false)
    setError(null)
    setLoadError(false)
    setShowDiscover(false)
    setDiscoverForm({ scriptId: '', name: '' })
    setExpanded(null)
    setApplyForm({ rulesetId: '', scriptId: '' })
    setApplyResult(null)
    setDeleteTarget(null)
    setBusy(false)
  }, [projectId])

  const load = useCallback(async () => {
    const requestedProject = projectId
    const requestID = ++loadRequestRef.current
    const isCurrentRequest = () => (
      mountedRef.current
      && currentProjectRef.current === requestedProject
      && loadRequestRef.current === requestID
    )
    if (!requestedProject) {
      if (isCurrentRequest()) setRulesets([])
      return
    }
    if (!isCurrentRequest()) return
    setLoading(true)
    setError(null)
    setLoadError(false)
    try {
      const listedByID = new Map<string, CorrelationRuleset>()
      const pageSignatures = new Set<string>()
      let offset = 0
      for (let pageNumber = 0; pageNumber < MAX_CATALOG_PAGES; pageNumber += 1) {
        const res = await api.listCorrelationRulesets({
          project_id: requestedProject,
          limit: CATALOG_PAGE_SIZE,
          offset,
        })
        if (!isCurrentRequest()) return
        const page = res.correlation_rulesets || []
        page.forEach(ruleset => listedByID.set(ruleset.id, ruleset))
        if (page.length < CATALOG_PAGE_SIZE) break
        const signature = page.map(ruleset => ruleset.id).join('\u0000')
        if (pageSignatures.has(signature)) {
          throw new ApiError('Correlation ruleset pagination did not advance.', 502)
        }
        pageSignatures.add(signature)
        const responseOffset = Number.isFinite(res.offset) ? Number(res.offset) : offset
        const nextOffset = responseOffset + page.length
        if (nextOffset <= offset) {
          throw new ApiError('Correlation ruleset pagination did not advance.', 502)
        }
        offset = nextOffset
        if (pageNumber === MAX_CATALOG_PAGES - 1) {
          throw new ApiError('Correlation ruleset catalog exceeded the supported page limit.', 502)
        }
      }
      if (!isCurrentRequest()) return
      const listed = [...listedByID.values()]
      const listedIds = new Set(listed.map(ruleset => ruleset.id))
      deletedRulesetIdsRef.current.forEach(id => {
        if (!listedIds.has(id)) deletedRulesetIdsRef.current.delete(id)
      })
      const next = listed.filter(ruleset => !deletedRulesetIdsRef.current.has(ruleset.id))
      committedRulesetsRef.current.forEach((ruleset, id) => {
        if (deletedRulesetIdsRef.current.has(id)) return
        const listedRuleset = listed.find(candidate => candidate.id === id)
        if (listedRuleset && snapshotCaughtUp(listedRuleset, ruleset)) {
          committedRulesetsRef.current.delete(id)
          return
        }
        const index = next.findIndex(candidate => candidate.id === id)
        if (index >= 0) next[index] = ruleset
        else next.push(ruleset)
      })
      setRulesets(next.sort((left, right) => left.name.localeCompare(right.name)))
    } catch (err) {
      if (!isCurrentRequest()) return
      setError(err instanceof ApiError ? err.message : 'Failed to load correlation rulesets.')
      setLoadError(true)
    } finally {
      if (isCurrentRequest()) setLoading(false)
    }
  }, [api, projectId])

  useEffect(() => { void load() }, [load])

  const runAction = async <T,>(action: () => Promise<T>, onDone?: (result: T) => void): Promise<boolean> => {
    if (!mountedRef.current || actionInFlightRef.current) return false
    const requestedProject = projectId
    const requestID = ++actionRequestRef.current
    const isCurrentAction = () => (
      mountedRef.current
      && currentProjectRef.current === requestedProject
      && actionRequestRef.current === requestID
    )
    actionInFlightRef.current = true
    setBusy(true)
    setError(null)
    setLoadError(false)
    try {
      const result = await action()
      if (!isCurrentAction()) return false
      onDone?.(result)
      await load()
      return true
    } catch (err) {
      if (!isCurrentAction()) return false
      setError(err instanceof ApiError ? err.message : 'Action failed.')
      return false
    } finally {
      if (isCurrentAction()) {
        actionInFlightRef.current = false
        setBusy(false)
      }
    }
  }

  const handleDiscover = (): Promise<boolean> => {
    const requestedProject = projectId
    const form = discoverForm
    if (!requestedProject || !form.scriptId) return Promise.resolve(false)
    return runAction(
      async () => {
        const committed = await api.discoverCorrelationRulesetFromScript({ script_id: form.scriptId, name: form.name.trim() || undefined })
        if ((committed.project_id || '').trim() !== requestedProject) {
          throw new ApiError('The selected script belongs to a different project. Select a script from the active project.', 409)
        }
        return committed
      },
      committed => {
        committedRulesetsRef.current.set(committed.id, committed)
        deletedRulesetIdsRef.current.delete(committed.id)
        setRulesets(current => [
          ...current.filter(ruleset => ruleset.id !== committed.id),
          committed,
        ].sort((left, right) => left.name.localeCompare(right.name)))
        setShowDiscover(false)
        setDiscoverForm({ scriptId: '', name: '' })
      },
    )
  }

  const handleApply = async (rulesetId: string, commit: boolean) => {
    const requestedScriptID = applyForm.scriptId
    if (!requestedScriptID || applyForm.rulesetId !== rulesetId || actionInFlightRef.current) return
    // An applied result's token describes the script before the apply, so it cannot commit again.
    if (commit && (
      !applyResult?.preview_token
      || applyResult.applied
      || applyResult.ruleset_id !== rulesetId
      || applyResult.script_id !== requestedScriptID
    )) return
    const requestedProject = projectId
    const actionRequestID = ++actionRequestRef.current
    const applyRequestID = ++applyRequestRef.current
    const isCurrentAction = () => (
      mountedRef.current
      && currentProjectRef.current === requestedProject
      && actionRequestRef.current === actionRequestID
    )
    const isCurrentApply = () => isCurrentAction() && applyRequestRef.current === applyRequestID
    actionInFlightRef.current = true
    setBusy(true)
    setError(null)
    try {
      const res = await api.applyCorrelationRulesetToScript(rulesetId, {
        script_id: requestedScriptID,
        apply: commit,
        preview_token: commit ? applyResult?.preview_token : undefined,
      })
      if (!isCurrentApply()) return
      if (res.ruleset_id !== rulesetId || res.script_id !== requestedScriptID) {
        setApplyResult(null)
        setError('The apply response did not match the selected ruleset and script. Preview again.')
        return
      }
      setApplyResult(res)
      if (commit) await load()
    } catch (err) {
      if (!isCurrentApply()) return
      setApplyResult(null)
      setError(err instanceof ApiError ? err.message : 'Apply failed.')
    } finally {
      if (isCurrentAction()) {
        actionInFlightRef.current = false
        setBusy(false)
      }
    }
  }

  const cancelDiscovery = () => {
    setShowDiscover(false)
    setDiscoverForm({ scriptId: '', name: '' })
    setError(null)
  }

  // Choosing a script for one ruleset replaces any choice and preview made for another.
  const updateApplyScriptID = (rulesetId: string, scriptId: string) => {
    applyRequestRef.current += 1
    setApplyForm({ rulesetId, scriptId })
    setApplyResult(null)
  }

  // A chosen script is unfinished work until its Apply has gone through.
  const applied = Boolean(applyResult?.applied && applyResult.ruleset_id === applyForm.rulesetId && applyResult.script_id === applyForm.scriptId)
  const draftDirty = Boolean(
    (showDiscover && (discoverForm.scriptId || discoverForm.name.trim()))
    || (applyForm.scriptId && !applied),
  )
  const draftDirtyRef = useRef(draftDirty)
  draftDirtyRef.current = draftDirty
  const saveDraftRef = useRef<() => Promise<boolean>>(async () => false)
  saveDraftRef.current = async () => {
    if (applyForm.scriptId && !applied) {
      setError('Finish or clear the correlation preview before leaving this page.')
      return false
    }
    if (!discoverForm.scriptId) {
      setError('Choose the script to discover rules from, or cancel the discovery.')
      return false
    }
    return handleDiscover()
  }
  const discardDraftRef = useRef<() => void>(() => undefined)
  discardDraftRef.current = () => {
    applyRequestRef.current += 1
    cancelDiscovery()
    setApplyForm({ rulesetId: '', scriptId: '' })
    setApplyResult(null)
  }
  const navigationGuard = useMemo<NavigationGuard>(() => ({
    id: 'correlation-ruleset-draft',
    label: 'Correlation ruleset draft',
    isDirty: () => draftDirtyRef.current,
    save: () => saveDraftRef.current(),
    discard: () => discardDraftRef.current(),
  }), [])
  useNavigationGuard(navigationGuard, draftDirty)

  return (
    <section className="pp-panel" aria-labelledby={titleId}>
      <div className="pp-toolbar">
        <div className="pp-toolbar-copy">
          <h3 id={titleId} className="pp-panel-title">Correlation rulesets</h3>
          <p className="pp-muted">Rules for dynamic values, such as session ids and tokens, learned from one script&apos;s recording and reusable on other scripts.</p>
        </div>
        {/* The form closes through its own Cancel, so a draft is never hidden. The
            opener stays enabled so focus can return to it when a discovery completes. */}
        {canManage && projectId && !showDiscover && (
          <button ref={openerRef} type="button" className="btn btn-primary btn-sm" onClick={() => setShowDiscover(true)}>
            Discover from a script
          </button>
        )}
      </div>

      {error && (
        <div>
          <div className="pp-error" role="alert">{error}</div>
          {loadError && (
            <button type="button" className="btn btn-secondary btn-sm" disabled={loading || busy} onClick={() => { void load() }}>
              Retry rulesets
            </button>
          )}
        </div>
      )}
      {!projectId && <div className="pp-empty">Select a project to view its correlation rulesets.</div>}

      {canManage && projectId && showDiscover && (
        <form ref={formRef} className="pp-create" aria-label="Discover a correlation ruleset" onSubmit={event => { event.preventDefault(); void handleDiscover() }}>
          <ScriptPicker
            projectId={projectId}
            label="Script"
            hint="Needs a recording made in APEX Load, which keeps the recorded baseline the rules are learned from."
            value={discoverForm.scriptId}
            scripts={scripts}
            disabled={busy}
            onChange={scriptId => setDiscoverForm(form => ({ ...form, scriptId }))}
          />
          <label className="pp-field">
            <span>Ruleset name (optional)</span>
            <input value={discoverForm.name} onChange={event => setDiscoverForm(form => ({ ...form, name: event.target.value }))} />
          </label>
          <div className="pp-create-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !discoverForm.scriptId}>Discover rules</button>
            <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={cancelDiscovery}>Cancel</button>
          </div>
        </form>
      )}

      {loading && <p className="pp-muted">Loading…</p>}
      {!loading && projectId && rulesets.length === 0 && <div className="pp-empty">No correlation rulesets yet — discover one from a script with a recorded baseline.</div>}

      <div className="pp-list">
        {rulesets.map(ruleset => (
          <div key={ruleset.id} className="pp-row">
            <div className="pp-row-main">
              <button type="button" className="pp-row-title pp-row-title-button" onClick={() => setExpanded(id => id === ruleset.id ? null : ruleset.id)}>
                {ruleset.name}
                <span className="pp-pill pp-pill-muted">{ruleset.rules?.length || 0} rule{(ruleset.rules?.length || 0) === 1 ? '' : 's'}</span>
              </button>
              <div className="pp-row-meta">
                {ruleset.source ? `${ruleset.source} · ` : ''}updated {formatTimestamp(ruleset.updated_at)}
              </div>
              {expanded === ruleset.id && (
                <>
                  {(ruleset.rules?.length || 0) > 0 && (
                    <ul className="pp-sublist">
                      {ruleset.rules.slice(0, 25).map((rule, index) => (
                        <li key={`${ruleset.id}-${index}`} className="pp-subrow">
                          <strong>{rule.name}</strong>
                          <span className="pp-pill pp-pill-kind">{rule.extract_type}</span>
                          <code>{rule.expression}</code>
                          {typeof rule.confidence === 'number' && rule.confidence > 0 && (
                            <span className="pp-pill pp-pill-muted">{Math.round(rule.confidence * 100)}%</span>
                          )}
                        </li>
                      ))}
                      {ruleset.rules.length > 25 && <li className="pp-muted">+{ruleset.rules.length - 25} more</li>}
                    </ul>
                  )}
                  {canManage && projectId && (
                    <div className="pp-apply">
                      <ScriptPicker
                        projectId={projectId}
                        label="Apply to script"
                        hint="Preview the changes first; Apply saves them as a new script version."
                        value={applyForm.rulesetId === ruleset.id ? applyForm.scriptId : ''}
                        scripts={scripts}
                        // Changing the script mid-request would drop the answer, even a completed Apply.
                        disabled={busy}
                        onChange={scriptId => updateApplyScriptID(ruleset.id, scriptId)}
                      />
                      <div className="pp-create-actions">
                        <button type="button" className="btn btn-secondary btn-sm" disabled={busy || applyForm.rulesetId !== ruleset.id || !applyForm.scriptId} onClick={() => handleApply(ruleset.id, false)}>Preview</button>
                        <button type="button" className="btn btn-primary btn-sm" disabled={busy || applyForm.rulesetId !== ruleset.id || !applyForm.scriptId || !applyResult?.preview_token || applyResult.applied || applyResult.matched_rules === 0 || applyResult.ruleset_id !== ruleset.id || applyResult.script_id !== applyForm.scriptId} onClick={() => handleApply(ruleset.id, true)}>Apply</button>
                      </div>
                    </div>
                  )}
                  {applyResult && applyForm.rulesetId === ruleset.id && applyResult.ruleset_id === ruleset.id && applyResult.script_id === applyForm.scriptId && (
                    <div className="pp-apply-result">
                      {applyResult.applied
                        ? <span className="pp-pill pp-pill-heal">applied {applyResult.matched_rules} rule{applyResult.matched_rules === 1 ? '' : 's'}</span>
                        : <span className="pp-pill pp-pill-muted">{applyResult.matched_rules} rule{applyResult.matched_rules === 1 ? '' : 's'} matched · {(applyResult.diffs?.length || 0)} change{(applyResult.diffs?.length || 0) === 1 ? '' : 's'}</span>}
                      {(applyResult.diffs || []).slice(0, 12).map((diff, i) => (
                        <div key={i} className="pp-candidate">
                          <span className="pp-pill pp-pill-kind">{diff.kind}</span>
                          <code className="pp-candidate-value">{diff.path || ''}</code>
                        </div>
                      ))}
                    </div>
                  )}
                </>
              )}
            </div>
            {canManage && (
              <div className="pp-row-actions">
                <button type="button" className="btn btn-danger btn-sm" onClick={() => setDeleteTarget(ruleset)} disabled={busy}>Delete</button>
              </div>
            )}
          </div>
        ))}
      </div>

      {deleteTarget && (
        <Suspense fallback={null}>
          <ConfirmDestructiveModal
            title="Delete correlation ruleset"
            description={`This permanently deletes “${deleteTarget.name}” and its ${deleteTarget.rules?.length || 0} rules.`}
            target={deleteTarget.name}
            targetLabel="Ruleset"
            confirmLabel="Delete ruleset"
            loading={busy}
            error={error}
            onCancel={() => setDeleteTarget(null)}
            onConfirm={() => {
              const target = deleteTarget
              void runAction(
                () => api.deleteCorrelationRuleset(target.id),
                () => {
                  deletedRulesetIdsRef.current.add(target.id)
                  committedRulesetsRef.current.delete(target.id)
                  setRulesets(current => current.filter(ruleset => ruleset.id !== target.id))
                  setDeleteTarget(null)
                  // A script chosen to apply the deleted ruleset is no longer work to finish.
                  if (applyForm.rulesetId === target.id) {
                    applyRequestRef.current += 1
                    setApplyForm({ rulesetId: '', scriptId: '' })
                    setApplyResult(null)
                  }
                },
              )
            }}
          />
        </Suspense>
      )}
    </section>
  )
}
