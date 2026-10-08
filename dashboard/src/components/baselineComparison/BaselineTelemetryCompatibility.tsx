import { useEffect, useRef, useState } from 'react'

import type { TelemetryComparison, TelemetryRunProfile } from './model'
import { telemetryReevaluationApi, telemetryReevaluationTerminal, type EvaluationJob, type TelemetryClassificationCounts, type TelemetryPolicySummary } from './telemetryReevaluationModel'

const REASON_LABELS: Record<string, string> = {
  policy_unavailable: 'a run retained no telemetry policy receipt',
  budgets_differ: 'different lag budgets',
  sustained_window_differs: 'different sustained-breach window',
  cadence_differs: 'different publication cadence',
  source_capability_differs: 'different telemetry source capabilities',
  workload_unqualified: 'different offered workload',
  workload_version_differs: 'differently recorded offered workloads',
}

const POLL_MS = 2000
// A poll that fails is retried with backoff this many times before the job
// is dropped (so a new re-evaluation can be queued).
export const POLL_RETRIES = 4

const shortHash = (hash?: string) => (hash ? hash.slice(0, 12) : 'Unavailable')
const signed = (value: number | undefined, unit: string) => (value === undefined || !Number.isFinite(value) ? 'Unavailable' : `${value > 0 ? '+' : ''}${value.toFixed(2)}${unit}`)

function profileCells(profile: TelemetryRunProfile) {
  const budgets = profile.budgets?.filter(budget => budget.stage !== 'browser_render').map(budget => `${budget.stage} ${budget.enabled ? `${budget.limit_ms} ms` : 'off'}`).join(', ')
  return [
    profile.policy_availability === 'available' ? shortHash(profile.policy_hash) : 'No receipt',
    budgets || 'Unavailable',
    profile.sustained_breach_window_ms !== undefined ? `${profile.sustained_breach_window_ms} ms` : 'Unavailable',
    profile.cadence_ms !== undefined ? `${profile.cadence_ms} ms (${profile.cadence_scope})` : 'Unavailable',
    profile.source_capabilities.length ? profile.source_capabilities.join(', ') : 'none',
  ]
}

function counts(value?: TelemetryClassificationCounts) {
  if (!value) return '—'
  const pct = value.breached_classified_pct !== undefined ? ` · ${value.breached_classified_pct.toFixed(1)}% breached` : ''
  return `${value.healthy} healthy · ${value.degraded} degraded · ${value.breached} breached · ${value.unscored} unscored · ${value.unknown} unknown${pct}`
}

// unscoredBreakdown names why an unanswerable run's intervals could not be
// judged, most frequent first.
function unscoredBreakdown(unscored?: Record<string, number>): string {
  const entries = Object.entries(unscored || {}).filter(([, count]) => count > 0).sort(([a, x], [b, y]) => y - x || a.localeCompare(b)).slice(0, 3)
  return entries.length ? ` (${entries.map(([reason, count]) => `${reason.replace(/_/g, ' ')} ×${count}`).join(', ')})` : ''
}

function TelemetryReevaluationPanel({ projectId, runIds, label }: { projectId: string; runIds: string[]; label: (id: string) => string }) {
  const [policies, setPolicies] = useState<TelemetryPolicySummary[] | null>(null)
  const [selected, setSelected] = useState('')
  const [job, setJob] = useState<EvaluationJob | null>(null)
  const [error, setError] = useState('')
  const timer = useRef<number | undefined>(undefined)
  const abort = useRef<AbortController | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    telemetryReevaluationApi.policies(projectId, controller.signal)
      .then(list => {
        const live = (list || []).filter(policy => !policy.deleted)
        setPolicies(live)
        setSelected(current => current || live[0]?.id || '')
      })
      .catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load telemetry policies') })
    return () => controller.abort()
  }, [projectId])

  useEffect(() => () => {
    window.clearTimeout(timer.current)
    abort.current?.abort()
  }, [])

  const poll = (id: string, failures = 0) => {
    abort.current?.abort()
    const controller = new AbortController()
    abort.current = controller
    telemetryReevaluationApi.get(projectId, id, controller.signal)
      .then(next => {
        setError('')
        setJob(next)
        if (!telemetryReevaluationTerminal(next.state)) timer.current = window.setTimeout(() => poll(id), POLL_MS)
      })
      .catch(cause => {
        if (controller.signal.aborted) return
        const message = cause instanceof Error ? cause.message : 'Could not read the re-evaluation'
        if (failures < POLL_RETRIES) {
          setError(`${message}; retrying`)
          timer.current = window.setTimeout(() => poll(id, failures + 1), POLL_MS * 2 ** (failures + 1))
          return
        }
        setError(message)
        setJob(null)
      })
  }

  const start = async () => {
    const policy = policies?.find(item => item.id === selected)
    if (!policy) return
    setError('')
    window.clearTimeout(timer.current)
    try {
      const created = await telemetryReevaluationApi.create(projectId, runIds, { id: policy.id, revision: policy.revision })
      setJob(created)
      if (!telemetryReevaluationTerminal(created.state)) timer.current = window.setTimeout(() => poll(created.id), POLL_MS)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not queue the re-evaluation')
    }
  }

  const busy = job !== null && !telemetryReevaluationTerminal(job.state)
  const result = job?.state === 'ready' ? job.result : undefined
  return (
    <div className="bc-telemetry-reevaluation" aria-label="Telemetry re-evaluation">
      <h3>Re-evaluate under a common policy</h3>
      <p>Classifies every run's retained lag evidence under one project telemetry policy revision. The result is derived; each run's own classification is unchanged.</p>
      {policies === null && !error && <p>Loading telemetry policies…</p>}
      {policies !== null && policies.length === 0 && <p>This project has no telemetry policy yet. Define one to re-evaluate these runs alike.</p>}
      {policies !== null && policies.length > 0 && (
        <div className="bc-telemetry-reevaluation-controls">
          <label>
            Telemetry policy{' '}
            <select value={selected} onChange={event => setSelected(event.target.value)} aria-label="Telemetry policy">
              {policies.map(policy => <option key={policy.id} value={policy.id}>{policy.name} (revision {policy.revision})</option>)}
            </select>
          </label>{' '}
          <button type="button" className="btn btn-secondary" disabled={busy || !selected} onClick={() => void start()}>Re-evaluate {runIds.length} runs</button>
        </div>
      )}
      {error && <p role="alert">{error}</p>}
      {job && !result && <p role="status">Re-evaluation {job.state}{job.failure ? `: ${job.failure}` : ''}</p>}
      {result && (
        <div role="region" aria-label="Telemetry re-evaluation result">
          <p>
            {result.policy.name} revision {result.policy.revision} · policy {shortHash(result.policy.sha256)} · inputs {shortHash(result.input_fingerprint)} · result {shortHash(job?.result_sha256)}
            {result.all_answerable ? '' : ' · some runs cannot be answered under this policy'}
          </p>
          <div className="bc-table-scroll">
            <table>
              <thead><tr><th>Run</th><th>Original classification</th><th>Re-evaluated</th><th>Answerable</th></tr></thead>
              <tbody>
                {result.runs.map(run => (
                  <tr key={run.run_id} data-run={run.run_id}>
                    <th scope="row" title={run.run_id}>{label(run.run_id)}</th>
                    <td>{counts(run.original)}</td>
                    <td>{counts(run.reevaluated)}</td>
                    <td>{run.answerable ? 'Yes' : `No: ${(run.unanswerable || []).join(', ')}${unscoredBreakdown(run.stage_unscored)}`}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <ul>{result.notes.map((note, index) => <li key={index}>{note}</li>)}</ul>
        </div>
      )}
    </div>
  )
}

// Per baseline: whether its whole-run telemetry health compares with the
// current run's. A comparable pair shows the health change; an incomparable
// one shows both runs' classification profiles side by side and why.
export default function BaselineTelemetryCompatibility({ comparisons, projectId, runNames }: { comparisons?: TelemetryComparison[]; projectId?: string; runNames?: Map<string, string> }) {
  if (!comparisons?.length) return null
  const label = (id: string) => runNames?.get(id) || id
  const runIds = [comparisons[0].current.run_id, ...comparisons.map(comparison => comparison.baseline_run_id)]
  return (
    <section className="run-report-panel" aria-label="Telemetry compatibility">
      <h2>Telemetry compatibility</h2>
      <p>Whole-run telemetry health is compared only when both runs were classified alike: the same budgets, sustained window, publication cadence, source capabilities and a qualified workload.</p>
      {comparisons.map(comparison => (
        <div key={comparison.baseline_run_id} className="bc-telemetry-compatibility" data-baseline={comparison.baseline_run_id}>
          <h3>{label(comparison.current.run_id)} vs {label(comparison.baseline_run_id)}: {comparison.comparable ? 'comparable' : 'not comparable'}</h3>
          {comparison.comparable ? (
            <p>Breached classified intervals {signed(comparison.breached_interval_pct_delta, ' pts')} · health coverage {signed(comparison.health_coverage_pct_delta, ' pts')} · breached known-time fraction {signed(comparison.breached_known_fraction_delta, '')}</p>
          ) : (
            <p>Not compared: {comparison.reasons.map(reason => REASON_LABELS[reason] || reason).join('; ')}.</p>
          )}
          <div className="bc-table-scroll">
            <table aria-label={`Telemetry profiles of ${comparison.current.run_id} and ${comparison.baseline_run_id}`}>
              <thead><tr><th>Run</th><th>Policy receipt</th><th>Budgets</th><th>Sustained window</th><th>Cadence</th><th>Source capabilities</th></tr></thead>
              <tbody>
                {[comparison.current, comparison.baseline].map(profile => (
                  <tr key={profile.run_id}><th scope="row" title={profile.run_id}>{label(profile.run_id)}</th>{profileCells(profile).map((cell, index) => <td key={index}>{cell}</td>)}</tr>
                ))}
              </tbody>
            </table>
          </div>
          {comparison.notes?.map((note, index) => <p key={index}>{note}</p>)}
        </div>
      ))}
      {/* Keyed on the run set: another comparison starts a fresh panel and
          stops polling the previous one's job. */}
      {projectId && <TelemetryReevaluationPanel key={runIds.join(',')} projectId={projectId} runIds={runIds} label={label} />}
    </section>
  )
}
