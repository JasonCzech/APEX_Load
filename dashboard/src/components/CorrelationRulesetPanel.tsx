import { useCallback, useEffect, useRef, useState } from 'react'

import { useCorrelationRulesetApi } from '../hooks/api/useCorrelationRulesetApi'
import { ApiError } from '../hooks/apiClient'
import { CorrelationRuleset, CorrelationRulesetApplyResponse } from '../types'
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

// Project-scoped catalog of learned, reusable correlation rules. Discover rules
// from a script's recorded baseline, then apply them to other scripts.
export default function CorrelationRulesetPanel({ projectId, canManage = false }: Props) {
  const api = useCorrelationRulesetApi()
  const mountedRef = useRef(false)
  const currentProjectRef = useRef(projectId)
  const loadRequestRef = useRef(0)
  const actionRequestRef = useRef(0)
  const actionInFlightRef = useRef(false)
  const applyRequestRef = useRef(0)
  currentProjectRef.current = projectId
  const [rulesets, setRulesets] = useState<CorrelationRuleset[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [discoverForm, setDiscoverForm] = useState({ scriptId: '', name: '' })
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
    setRulesets([])
    setLoading(false)
    setError(null)
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
    try {
      const res = await api.listCorrelationRulesets({ project_id: requestedProject })
      if (!isCurrentRequest()) return
      setRulesets(res.correlation_rulesets || [])
    } catch (err) {
      if (!isCurrentRequest()) return
      setError(err instanceof ApiError ? err.message : 'Failed to load correlation rulesets.')
      setRulesets([])
    } finally {
      if (isCurrentRequest()) setLoading(false)
    }
  }, [api, projectId])

  useEffect(() => { void load() }, [load])

  const runAction = async (action: () => Promise<unknown>, onDone?: () => void) => {
    if (!mountedRef.current || actionInFlightRef.current) return
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
    try {
      await action()
      if (!isCurrentAction()) return
      onDone?.()
      await load()
    } catch (err) {
      if (!isCurrentAction()) return
      setError(err instanceof ApiError ? err.message : 'Action failed.')
    } finally {
      if (isCurrentAction()) {
        actionInFlightRef.current = false
        setBusy(false)
      }
    }
  }

  const handleDiscover = () => {
    if (!discoverForm.scriptId.trim()) return
    void runAction(
      () => api.discoverCorrelationRulesetFromScript({ script_id: discoverForm.scriptId.trim(), name: discoverForm.name.trim() || undefined }),
      () => setDiscoverForm({ scriptId: '', name: '' }),
    )
  }

  const handleApply = async (rulesetId: string, commit: boolean) => {
    const requestedScriptID = applyForm.scriptId.trim()
    if (!requestedScriptID || applyForm.rulesetId !== rulesetId || actionInFlightRef.current) return
    if (commit && (
      !applyResult?.preview_token
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
      if (commit) setApplyResult(null)
      setError(err instanceof ApiError ? err.message : 'Apply failed.')
    } finally {
      if (isCurrentAction()) {
        actionInFlightRef.current = false
        setBusy(false)
      }
    }
  }

  const openApply = (rulesetId: string) => {
    // Only (re)initialize when switching to a different ruleset's input. onFocus
    // fires every time the field gains focus, so resetting unconditionally would
    // wipe the typed script id and the computed preview when the user clicks back
    // into the same field to verify or edit it.
    if (applyForm.rulesetId === rulesetId) return
    applyRequestRef.current += 1
    setApplyForm({ rulesetId, scriptId: '' })
    setApplyResult(null)
  }

  const updateApplyScriptID = (rulesetId: string, scriptId: string) => {
    applyRequestRef.current += 1
    setApplyForm({ rulesetId, scriptId })
    setApplyResult(null)
  }

  return (
    <div className="pp-panel" aria-label="Correlation rulesets">
      <div className="pp-toolbar">
        <h3 className="pp-toolbar-title">Correlation Rulesets</h3>
      </div>

      {error && <div className="pp-error">{error}</div>}
      {!projectId && <div className="pp-empty">Select a project to view its correlation rulesets.</div>}

      {canManage && projectId && (
        <div className="pp-field-row">
          <input className="pp-field" placeholder="Discover from script id…" value={discoverForm.scriptId} onChange={e => setDiscoverForm(f => ({ ...f, scriptId: e.target.value }))} />
          <input className="pp-field" placeholder="Ruleset name (optional)" value={discoverForm.name} onChange={e => setDiscoverForm(f => ({ ...f, name: e.target.value }))} />
          <button type="button" className="btn btn-primary btn-sm" onClick={handleDiscover} disabled={busy || !discoverForm.scriptId.trim()}>Discover from script</button>
        </div>
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
                  {canManage && (
                    <div className="pp-field-row">
                      <input className="pp-field" placeholder="Apply to script id…" value={applyForm.rulesetId === ruleset.id ? applyForm.scriptId : ''} onFocus={() => openApply(ruleset.id)} onChange={e => updateApplyScriptID(ruleset.id, e.target.value)} />
                      <button type="button" className="btn btn-secondary btn-sm" disabled={busy || applyForm.rulesetId !== ruleset.id || !applyForm.scriptId.trim()} onClick={() => handleApply(ruleset.id, false)}>Preview</button>
                      <button type="button" className="btn btn-primary btn-sm" disabled={busy || applyForm.rulesetId !== ruleset.id || !applyForm.scriptId.trim() || !applyResult?.preview_token || applyResult.matched_rules === 0 || applyResult.ruleset_id !== ruleset.id || applyResult.script_id !== applyForm.scriptId.trim()} onClick={() => handleApply(ruleset.id, true)}>Apply</button>
                    </div>
                  )}
                  {applyResult && applyForm.rulesetId === ruleset.id && applyResult.ruleset_id === ruleset.id && applyResult.script_id === applyForm.scriptId.trim() && (
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
        <ConfirmDestructiveModal
          title="Delete correlation ruleset"
          description={`This permanently deletes “${deleteTarget.name}” and its ${deleteTarget.rules?.length || 0} rules.`}
          target={deleteTarget.name}
          targetLabel="Ruleset"
          confirmLabel="Delete ruleset"
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => {
            const target = deleteTarget
            setDeleteTarget(null)
            void runAction(() => api.deleteCorrelationRuleset(target.id))
          }}
        />
      )}
    </div>
  )
}
