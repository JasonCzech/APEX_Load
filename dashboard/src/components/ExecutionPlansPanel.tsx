import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import { ApiError } from '../hooks/apiClient'
import { useModalDialog } from '../hooks/useModalDialog'
import {
  ExecutionPlan,
  ExecutionPlanEntry,
  ExecutionPlanRun,
  ExecutionPlanRunResponse,
  ScenarioDataPoolBinding,
} from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import ExecutionPlanApprovalsModal from './ExecutionPlanApprovalsModal'
import ExecutionPlanSchedulesModal from './ExecutionPlanSchedulesModal'
import { dialogBackdropProps } from './dialogBackdropProps'
import './parityPanels.css'

interface Props {
  projectId?: string
  canManage?: boolean
  canAdmin?: boolean
  scenarioOptions?: { id: string; name: string }[]
  onOpenTest?: (testId: string) => void
  // When set, opens a new-plan draft pre-seeded with this scenario as the first entry.
  seedScenarioId?: string
  onSeedConsumed?: () => void
}

type DataPoolDraft = { pool_id: string; alias: string }

type EntryDraft = {
  scenario_id: string
  scenario_name: string
  enabled: boolean
  failure_policy: string
  timeslot_id: string
  data_pools: DataPoolDraft[]
}

type PlanDraft = {
  id?: string
  name: string
  run_mode: string
  entries: EntryDraft[]
}

const emptyEntry: EntryDraft = {
  scenario_id: '',
  scenario_name: '',
  enabled: true,
  failure_policy: 'halt',
  timeslot_id: '',
  data_pools: [],
}

function emptyDraft(): PlanDraft {
  return { name: '', run_mode: 'parallel', entries: [{ ...emptyEntry }] }
}

const RUN_TERMINAL = ['completed', 'halted', 'failed']

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

// Per-entry status within a sequential plan run.
function entryStatusPill(status: string): string {
  switch (status) {
    case 'complete':
      return 'pp-pill pp-pill-success'
    case 'running':
      return 'pp-pill pp-pill-info'
    case 'failed':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-muted' // pending | skipped
  }
}

function runStatusPill(status: string): string {
  switch (status) {
    case 'completed':
      return 'pp-pill pp-pill-success'
    case 'running':
      return 'pp-pill pp-pill-info'
    case 'halted':
    case 'failed':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

export default function ExecutionPlansPanel({ projectId, canManage = false, canAdmin = false, scenarioOptions, onOpenTest, seedScenarioId, onSeedConsumed }: Props) {
  const api = useExecutionPlansApi()
  const [plans, setPlans] = useState<ExecutionPlan[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<PlanDraft | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ExecutionPlan | null>(null)
  const [runResult, setRunResult] = useState<{ plan: string; res: ExecutionPlanRunResponse } | null>(null)
  const [planRun, setPlanRun] = useState<{ planId: string; planName: string; run: ExecutionPlanRun } | null>(null)
  const [schedulesTarget, setSchedulesTarget] = useState<ExecutionPlan | null>(null)
  const [approvalsTarget, setApprovalsTarget] = useState<ExecutionPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const draftDialogRef = useRef<HTMLDivElement>(null)
  const resultDialogRef = useRef<HTMLDivElement>(null)
  const progressDialogRef = useRef<HTMLDivElement>(null)
  const closeDraft = useCallback(() => setDraft(null), [])
  const closeRunResult = useCallback(() => setRunResult(null), [])
  const closePlanRun = useCallback(() => setPlanRun(null), [])

  useModalDialog({ dialogRef: draftDialogRef, enabled: Boolean(draft), onClose: closeDraft })
  useModalDialog({ dialogRef: resultDialogRef, enabled: Boolean(runResult), onClose: closeRunResult })
  useModalDialog({ dialogRef: progressDialogRef, enabled: Boolean(planRun), onClose: closePlanRun })

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
      entries: [{ ...emptyEntry, scenario_id: seedScenarioId, scenario_name: opt?.name || '' }],
    })
    onSeedConsumed?.()
  }, [seedScenarioId, canManage, scenarioOptions, onSeedConsumed])

  // Poll a running sequential plan-run so the gated progression is reflected live.
  useEffect(() => {
    if (!planRun || RUN_TERMINAL.includes(planRun.run.status)) return
    const timer = setInterval(async () => {
      try {
        const run = await api.getExecutionPlanRun(planRun.planId, planRun.run.id)
        setPlanRun(prev => (prev && prev.run.id === run.id ? { ...prev, run } : prev))
      } catch {
        /* transient errors are fine — keep polling */
      }
    }, 1500)
    return () => clearInterval(timer)
  }, [planRun, api])

  const draftToEntries = (entries: EntryDraft[]): ExecutionPlanEntry[] =>
    entries
      .filter(e => e.scenario_id.trim())
      .map((e, index) => {
        const pools: ScenarioDataPoolBinding[] = e.data_pools
          .filter(dp => dp.pool_id.trim() && dp.alias.trim())
          .map(dp => ({
            pool_id: dp.pool_id.trim(),
            alias: dp.alias.trim(),
            access_mode: 'read_only',
            selection_strategy: 'sequential',
          }))
        return {
          order: index,
          scenario_id: e.scenario_id.trim(),
          scenario_name: e.scenario_name.trim() || undefined,
          enabled: e.enabled,
          failure_policy: e.failure_policy,
          timeslot_id: e.timeslot_id.trim() || undefined,
          data_pools: pools.length > 0 ? pools : undefined,
        }
      })

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
        if (res.run_id) {
          // Sequential: dependency-gated run — show live per-entry progress.
          const seeded: ExecutionPlanRun = {
            id: res.run_id,
            plan_id: plan.id,
            project_id: plan.project_id,
            run_mode: res.run_mode,
            status: res.status || 'running',
            entries: res.results.map(r => ({
              order: r.order,
              scenario_id: r.scenario_id,
              test_id: r.test_id,
              status: r.status,
              detail: r.detail,
            })),
            created_at: res.materialized_at,
            updated_at: res.materialized_at,
          }
          setPlanRun({ planId: plan.id, planName: plan.name, run: seeded })
          try {
            const run = await api.getExecutionPlanRun(plan.id, res.run_id)
            setPlanRun({ planId: plan.id, planName: plan.name, run })
          } catch {
            /* keep the seeded snapshot if the run can't be re-read yet */
          }
        } else {
          setRunResult({ plan: plan.name, res })
        }
        await load()
      } catch (err) {
        // A run that exceeds the project VUser quota is blocked (HTTP 409) until
        // a project admin approves it. Open the approvals surface instead of
        // showing a bare error so the gate is actionable.
        if (err instanceof ApiError && err.status === 409 && (err.payload as { status?: string } | undefined)?.status === 'blocked') {
          setError('This run exceeds the project VUser quota and needs project-admin approval. Review the pending request below.')
          setApprovalsTarget(plan)
        } else {
          setError(err instanceof ApiError ? err.message : 'Failed to run execution plan.')
        }
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
              timeslot_id: e.timeslot_id || '',
              data_pools: (e.data_pools || []).map(dp => ({ pool_id: dp.pool_id, alias: dp.alias })),
            }))
          : [{ ...emptyEntry }],
    })
  }

  const updateEntry = (index: number, patch: Partial<EntryDraft>) =>
    setDraft(d => (d ? { ...d, entries: d.entries.map((e, i) => (i === index ? { ...e, ...patch } : e)) } : d))

  const updatePool = (entryIdx: number, poolIdx: number, patch: Partial<DataPoolDraft>) =>
    setDraft(d =>
      d
        ? {
            ...d,
            entries: d.entries.map((e, i) =>
              i === entryIdx ? { ...e, data_pools: e.data_pools.map((p, j) => (j === poolIdx ? { ...p, ...patch } : p)) } : e,
            ),
          }
        : d,
    )

  const addPool = (entryIdx: number) =>
    setDraft(d =>
      d
        ? { ...d, entries: d.entries.map((e, i) => (i === entryIdx ? { ...e, data_pools: [...e.data_pools, { pool_id: '', alias: '' }] } : e)) }
        : d,
    )

  const removePool = (entryIdx: number, poolIdx: number) =>
    setDraft(d =>
      d
        ? { ...d, entries: d.entries.map((e, i) => (i === entryIdx ? { ...e, data_pools: e.data_pools.filter((_, j) => j !== poolIdx) } : e)) }
        : d,
    )

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

      {error && <div className="pp-error" role="alert">{error}</div>}

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
                  <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => setSchedulesTarget(plan)}>
                    Schedules
                  </button>
                  <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => setApprovalsTarget(plan)}>
                    Approvals
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
        <div className="pp-modal-overlay" {...dialogBackdropProps(closeDraft, 'Close execution plan editor')}>
          <div ref={draftDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="execution-plan-editor-title">
            <div className="pp-modal-header">
              <h3 id="execution-plan-editor-title">{draft.id ? 'Edit Execution Plan' : 'New Execution Plan'}</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeDraft}>
                Close
              </button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Name</span>
                  <input value={draft.name} onChange={e => setDraft(d => (d ? { ...d, name: e.target.value } : d))} placeholder="e.g. nightly regression" />
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
                    <div className="pp-entry-block" key={index}>
                      <div className="pp-entry">
                      <div className="pp-entry-index">{index + 1}</div>
                      {scenarioOptions && scenarioOptions.length > 0 ? (
                        <select
                          aria-label={`Entry ${index + 1} scenario`}
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
                        <input aria-label={`Entry ${index + 1} scenario ID`} value={entry.scenario_id} placeholder="scenario id" onChange={e => updateEntry(index, { scenario_id: e.target.value })} />
                      )}
                      <select aria-label={`Entry ${index + 1} failure policy`} value={entry.failure_policy} onChange={e => updateEntry(index, { failure_policy: e.target.value })} title="On failure">
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
                      <div className="pp-entry-advanced">
                        <label className="pp-entry-advanced-field">
                          <span>Timeslot</span>
                          <input
                            aria-label={`Entry ${index + 1} timeslot ID`}
                            value={entry.timeslot_id}
                            placeholder="optional timeslot id"
                            onChange={e => updateEntry(index, { timeslot_id: e.target.value })}
                          />
                        </label>
                        <div className="pp-entry-pools">
                          <span className="pp-entry-advanced-label">Data Pools</span>
                          {entry.data_pools.map((dp, di) => (
                            <div className="pp-entry-pool" key={di}>
                              <input aria-label={`Entry ${index + 1} data pool ${di + 1} ID`} value={dp.pool_id} placeholder="pool id" onChange={e => updatePool(index, di, { pool_id: e.target.value })} />
                              <input aria-label={`Entry ${index + 1} data pool ${di + 1} alias`} value={dp.alias} placeholder="alias" onChange={e => updatePool(index, di, { alias: e.target.value })} />
                              <button type="button" className="btn btn-ghost btn-sm" onClick={() => removePool(index, di)} title="Remove data pool">
                                ✕
                              </button>
                            </div>
                          ))}
                          <button type="button" className="btn btn-ghost btn-sm" onClick={() => addPool(index)}>
                            + Add data pool
                          </button>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => setDraft(d => (d ? { ...d, entries: [...d.entries, { ...emptyEntry }] } : d))}>
                  + Add entry
                </button>
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={closeDraft}>
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
        <div className="pp-modal-overlay" {...dialogBackdropProps(closeRunResult, 'Close execution plan result')}>
          <div ref={resultDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="execution-plan-result-title">
            <div className="pp-modal-header">
              <h3 id="execution-plan-result-title">Run “{runResult.plan}”</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeRunResult}>
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
              <button type="button" className="btn btn-primary" onClick={closeRunResult}>
                Done
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Sequential plan-run progress modal (dependency-gated, polled live) */}
      {planRun && (
        <div className="pp-modal-overlay" {...dialogBackdropProps(closePlanRun, 'Close execution plan progress')}>
          <div ref={progressDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="execution-plan-progress-title">
            <div className="pp-modal-header">
              <h3 id="execution-plan-progress-title">
                Run “{planRun.planName}”
                <span className={runStatusPill(planRun.run.status)} style={{ marginLeft: 8 }} role="status" aria-live="polite">
                  {planRun.run.status}
                </span>
              </h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closePlanRun}>
                Close
              </button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-row-meta">
                <span>sequential — each entry starts only after the previous run finishes</span>
                {!RUN_TERMINAL.includes(planRun.run.status) ? <span>updating…</span> : null}
              </div>
              <div className="pp-list">
                {planRun.run.entries.map(entry => (
                  <div className="pp-row" key={entry.order}>
                    <div className="pp-row-main">
                      <div className="pp-row-title">
                        #{entry.order + 1} {entry.scenario_name || entry.scenario_id}
                        <span className={entryStatusPill(entry.status)}>{entry.status}</span>
                      </div>
                      {entry.detail ? <div className="pp-row-meta"><span>{entry.detail}</span></div> : null}
                    </div>
                    {entry.test_id && onOpenTest ? (
                      <div className="pp-row-actions">
                        <button type="button" className="btn btn-ghost btn-sm" onClick={() => onOpenTest(entry.test_id!)}>
                          Open test
                        </button>
                      </div>
                    ) : null}
                  </div>
                ))}
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-primary" onClick={closePlanRun}>
                Done
              </button>
            </div>
          </div>
        </div>
      )}

      {schedulesTarget && (
        <ExecutionPlanSchedulesModal
          plan={schedulesTarget}
          canManage={canManage}
          canAdmin={canAdmin}
          onClose={() => setSchedulesTarget(null)}
        />
      )}

      {approvalsTarget && (
        <ExecutionPlanApprovalsModal
          plan={approvalsTarget}
          canAdmin={canAdmin}
          onClose={() => setApprovalsTarget(null)}
          onResolved={() => void load()}
        />
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
