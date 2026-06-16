import { useCallback, useEffect, useMemo, useState } from 'react'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import { ApiError } from '../hooks/apiClient'
import { ExecutionPlan, ExecutionPlanEntry, ExecutionPlanRunResponse } from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import './parityPanels.css'

interface Props {
  projectId?: string
  canManage?: boolean
  scenarioOptions?: { id: string; name: string }[]
  onOpenTest?: (testId: string) => void
  // When set, opens a new-plan draft pre-seeded with this scenario as the first entry.
  seedScenarioId?: string
  onSeedConsumed?: () => void
}

type EntryDraft = {
  scenario_id: string
  scenario_name: string
  enabled: boolean
  failure_policy: string
}

type PlanDraft = {
  id?: string
  name: string
  run_mode: string
  entries: EntryDraft[]
}

const emptyEntry: EntryDraft = { scenario_id: '', scenario_name: '', enabled: true, failure_policy: 'halt' }

function emptyDraft(): PlanDraft {
  return { name: '', run_mode: 'parallel', entries: [{ ...emptyEntry }] }
}

function runResultPill(status: string): string {
  switch (status) {
    case 'created':
      return 'pp-pill pp-pill-success'
    case 'failed':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

export default function ExecutionPlansPanel({ projectId, canManage = false, scenarioOptions, onOpenTest, seedScenarioId, onSeedConsumed }: Props) {
  const api = useExecutionPlansApi()
  const [plans, setPlans] = useState<ExecutionPlan[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<PlanDraft | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ExecutionPlan | null>(null)
  const [runResult, setRunResult] = useState<{ plan: string; res: ExecutionPlanRunResponse } | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    if (!projectId) {
      setPlans([])
      return
    }
    setLoading(true)
    setError(null)
    try {
      const res = await api.listExecutionPlans({ project_id: projectId })
      setPlans(res.execution_plans || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load execution plans.')
      setPlans([])
    } finally {
      setLoading(false)
    }
  }, [api, projectId])

  useEffect(() => {
    void load()
  }, [load])

  // When a scenario is handed in from the library, open a fresh plan draft seeded with it.
  useEffect(() => {
    if (!seedScenarioId || !canManage) return
    const opt = scenarioOptions?.find(s => s.id === seedScenarioId)
    setDraft({
      name: opt?.name ? `${opt.name} plan` : '',
      run_mode: 'parallel',
      entries: [{ scenario_id: seedScenarioId, scenario_name: opt?.name || '', enabled: true, failure_policy: 'halt' }],
    })
    onSeedConsumed?.()
  }, [seedScenarioId, canManage, scenarioOptions, onSeedConsumed])

  const draftToEntries = (entries: EntryDraft[]): ExecutionPlanEntry[] =>
    entries
      .filter(e => e.scenario_id.trim())
      .map((e, index) => ({
        order: index,
        scenario_id: e.scenario_id.trim(),
        scenario_name: e.scenario_name.trim() || undefined,
        enabled: e.enabled,
        failure_policy: e.failure_policy,
      }))

  const submitDraft = useCallback(async () => {
    if (!draft) return
    const entries = draftToEntries(draft.entries)
    setBusy(true)
    setError(null)
    try {
      if (draft.id) {
        await api.updateExecutionPlan(draft.id, { name: draft.name.trim(), run_mode: draft.run_mode, entries })
      } else {
        await api.createExecutionPlan({ project_id: projectId, name: draft.name.trim(), run_mode: draft.run_mode, entries })
      }
      setDraft(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save execution plan.')
    } finally {
      setBusy(false)
    }
  }, [api, draft, load, projectId])

  const runPlan = useCallback(
    async (plan: ExecutionPlan) => {
      setBusy(true)
      setError(null)
      try {
        const res = await api.runExecutionPlan(plan.id)
        setRunResult({ plan: plan.name, res })
        await load()
      } catch (err) {
        setError(err instanceof ApiError ? err.message : 'Failed to run execution plan.')
      } finally {
        setBusy(false)
      }
    },
    [api, load],
  )

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteExecutionPlan(deleteTarget.id)
      setDeleteTarget(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete execution plan.')
    } finally {
      setBusy(false)
    }
  }, [api, deleteTarget, load])

  const startEdit = (plan: ExecutionPlan) => {
    setDraft({
      id: plan.id,
      name: plan.name,
      run_mode: plan.run_mode,
      entries:
        plan.entries.length > 0
          ? plan.entries.map(e => ({
              scenario_id: e.scenario_id,
              scenario_name: e.scenario_name || '',
              enabled: e.enabled !== false,
              failure_policy: e.failure_policy || 'halt',
            }))
          : [{ ...emptyEntry }],
    })
  }

  const updateEntry = (index: number, patch: Partial<EntryDraft>) =>
    setDraft(d => (d ? { ...d, entries: d.entries.map((e, i) => (i === index ? { ...e, ...patch } : e)) } : d))

  const moveEntry = (index: number, dir: -1 | 1) =>
    setDraft(d => {
      if (!d) return d
      const next = [...d.entries]
      const target = index + dir
      if (target < 0 || target >= next.length) return d
      ;[next[index], next[target]] = [next[target], next[index]]
      return { ...d, entries: next }
    })

  const draftValid = useMemo(
    () => Boolean(draft && draft.name.trim() && draft.entries.some(e => e.scenario_id.trim())),
    [draft],
  )

  return (
    <div className="pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {plans.length} execution plan{plans.length === 1 ? '' : 's'} — ordered, cross-run scenario orchestration.
        </span>
        {canManage && (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            disabled={!projectId}
            title={projectId ? undefined : 'Select a project to create an execution plan'}
            onClick={() => setDraft(emptyDraft())}
          >
            New Execution Plan
          </button>
        )}
      </div>

      {error && <div className="pp-error">{error}</div>}

      {!projectId ? (
        <div className="pp-empty">Select a project to view its execution plans.</div>
      ) : loading ? (
        <div className="pp-empty">Loading execution plans…</div>
      ) : plans.length === 0 ? (
        <div className="pp-empty">
          No execution plans yet. An execution plan runs several scenarios in order — in parallel or
          sequentially — with per-entry failure handling.
        </div>
      ) : (
        <div className="pp-list">
          {plans.map(plan => (
            <div className="pp-row" key={plan.id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {plan.name}
                  <span className="pp-pill pp-pill-info">{plan.run_mode}</span>
                  {plan.status ? <span className="pp-pill pp-pill-muted">{plan.status}</span> : null}
                </div>
                <div className="pp-row-meta">
                  <span>{plan.entries.length} entr{plan.entries.length === 1 ? 'y' : 'ies'}</span>
                  <span>{plan.entries.filter(e => e.enabled !== false).length} enabled</span>
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button type="button" className="btn btn-primary btn-sm" disabled={busy} onClick={() => void runPlan(plan)}>
                    Run
                  </button>
                  <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => startEdit(plan)}>
                    Edit
                  </button>
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(plan)}>
                    Delete
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Create / edit modal */}
      {draft && (
        <div className="pp-modal-overlay" onClick={() => setDraft(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>{draft.id ? 'Edit Execution Plan' : 'New Execution Plan'}</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setDraft(null)}>
                Close
              </button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Name</span>
                  <input value={draft.name} onChange={e => setDraft(d => (d ? { ...d, name: e.target.value } : d))} autoFocus placeholder="e.g. nightly regression" />
                </label>
                <label className="pp-field">
                  <span>Run mode</span>
                  <select value={draft.run_mode} onChange={e => setDraft(d => (d ? { ...d, run_mode: e.target.value } : d))}>
                    <option value="parallel">parallel</option>
                    <option value="sequential">sequential</option>
                  </select>
                </label>
              </div>

              <div className="pp-field">
                <span>Entries (run order)</span>
                <div className="pp-entry-list">
                  {draft.entries.map((entry, index) => (
                    <div className="pp-entry" key={index}>
                      <div className="pp-entry-index">{index + 1}</div>
                      {scenarioOptions && scenarioOptions.length > 0 ? (
                        <select
                          value={entry.scenario_id}
                          onChange={e => {
                            const opt = scenarioOptions.find(s => s.id === e.target.value)
                            updateEntry(index, { scenario_id: e.target.value, scenario_name: opt?.name || '' })
                          }}
                        >
                          <option value="">— select scenario —</option>
                          {scenarioOptions.map(s => (
                            <option key={s.id} value={s.id}>
                              {s.name}
                            </option>
                          ))}
                        </select>
                      ) : (
                        <input value={entry.scenario_id} placeholder="scenario id" onChange={e => updateEntry(index, { scenario_id: e.target.value })} />
                      )}
                      <select value={entry.failure_policy} onChange={e => updateEntry(index, { failure_policy: e.target.value })} title="On failure">
                        <option value="halt">halt</option>
                        <option value="skip">skip</option>
                      </select>
                      <label className="pp-entry-enabled" title="Enabled">
                        <input type="checkbox" checked={entry.enabled} onChange={e => updateEntry(index, { enabled: e.target.checked })} />
                        on
                      </label>
                      <button type="button" className="btn btn-ghost btn-sm" disabled={index === 0} onClick={() => moveEntry(index, -1)} title="Move up">
                        ↑
                      </button>
                      <button type="button" className="btn btn-ghost btn-sm" disabled={index === draft.entries.length - 1} onClick={() => moveEntry(index, 1)} title="Move down">
                        ↓
                      </button>
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        onClick={() => setDraft(d => (d ? { ...d, entries: d.entries.filter((_, i) => i !== index) } : d))}
                        title="Remove entry"
                      >
                        ✕
                      </button>
                    </div>
                  ))}
                </div>
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => setDraft(d => (d ? { ...d, entries: [...d.entries, { ...emptyEntry }] } : d))}>
                  + Add entry
                </button>
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setDraft(null)}>
                Cancel
              </button>
              <button type="button" className="btn btn-primary" disabled={!draftValid || busy} onClick={() => void submitDraft()}>
                {busy ? 'Saving…' : draft.id ? 'Save' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Run result modal */}
      {runResult && (
        <div className="pp-modal-overlay" onClick={() => setRunResult(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>Run “{runResult.plan}”</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setRunResult(null)}>
                Close
              </button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-row-meta">
                <span>mode: {runResult.res.run_mode}</span>
                <span>created: {runResult.res.created_count}</span>
                <span>failed: {runResult.res.failed_count}</span>
              </div>
              <div className="pp-list">
                {runResult.res.results.map(result => (
                  <div className="pp-row" key={result.order}>
                    <div className="pp-row-main">
                      <div className="pp-row-title">
                        #{result.order + 1} {result.scenario_id}
                        <span className={runResultPill(result.status)}>{result.status}</span>
                      </div>
                      {result.detail ? <div className="pp-row-meta"><span>{result.detail}</span></div> : null}
                    </div>
                    {result.test_id && onOpenTest ? (
                      <div className="pp-row-actions">
                        <button type="button" className="btn btn-ghost btn-sm" onClick={() => onOpenTest(result.test_id!)}>
                          Open test
                        </button>
                      </div>
                    ) : null}
                  </div>
                ))}
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-primary" onClick={() => setRunResult(null)}>
                Done
              </button>
            </div>
          </div>
        </div>
      )}

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete execution plan"
          description={`This permanently deletes the execution plan “${deleteTarget.name}”.`}
          target={deleteTarget.name}
          targetLabel="Execution plan"
          confirmLabel="Delete plan"
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void confirmDelete()}
        />
      )}
    </div>
  )
}
