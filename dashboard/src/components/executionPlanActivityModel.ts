import type {
  ExecutionPlanActivity,
  ExecutionPlanOverlapPolicy,
  ExecutionPlanRunEntry,
  ExecutionPlanRunEntryView,
  ExecutionPlanRunView,
  ExecutionPlanWaitingOccurrence,
} from '../types/executionPlans'
import type { TestExecutionPlanLink } from '../types'
import { formatDateTime } from '../utils/dateTime'

/** What each overlap policy does, in the editor's words. */
export const OVERLAP_POLICY_OPTIONS: { value: ExecutionPlanOverlapPolicy; label: string; description: string }[] = [
  { value: 'skip', label: 'Skip the new run', description: 'A run started while the previous one is still running does not start.' },
  { value: 'queue', label: 'Wait for the running one to finish', description: 'A scheduled run waits and starts when the previous run finishes. Run is unavailable while a run is in progress.' },
  { value: 'allow', label: 'Start alongside it', description: 'Both runs send load at once; quota approval counts them together.' },
]

/** The policy a plan runs under; plans saved before the policy existed allow overlap. */
export function effectiveOverlapPolicy(policy?: string): ExecutionPlanOverlapPolicy {
  return policy === 'skip' || policy === 'queue' ? policy : 'allow'
}

export function overlapPolicyLabel(policy?: string): string {
  return OVERLAP_POLICY_OPTIONS.find(option => option.value === effectiveOverlapPolicy(policy))?.label || 'Start alongside it'
}

/** "45 s", "12 min", "1 h 5 min". */
export function formatPlanDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return ''
  if (seconds < 60) return `${Math.max(1, Math.round(seconds))} s`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest ? `${hours} h ${rest} min` : `${hours} h`
}

function parseTime(iso?: string): number | null {
  if (!iso) return null
  const value = Date.parse(iso)
  return Number.isNaN(value) ? null : value
}

export interface PlanRunTiming {
  elapsedSeconds: number | null
  remainingSeconds: number | null
  endsAt?: string
  tookSeconds: number | null
}

/** Elapsed and remaining time for an active run; the total for a finished one. */
export function planRunTiming(run: ExecutionPlanRunView, now = Date.now()): PlanRunTiming {
  const started = parseTime(run.started_at) ?? parseTime(run.created_at)
  if (!run.active) {
    const ended = parseTime(run.ended_at) ?? parseTime(run.completed_at)
    return {
      elapsedSeconds: null,
      remainingSeconds: null,
      tookSeconds: started !== null && ended !== null && ended >= started ? (ended - started) / 1000 : null,
    }
  }
  const estimatedEnd = parseTime(run.estimated_end_at)
  return {
    elapsedSeconds: started !== null ? Math.max(0, (now - started) / 1000) : null,
    remainingSeconds: estimatedEnd !== null ? Math.max(0, (estimatedEnd - now) / 1000) : null,
    endsAt: estimatedEnd !== null ? run.estimated_end_at : undefined,
    tookSeconds: null,
  }
}

/** One line: "Started 6:27 AM · 12 min elapsed · about 8 min left". */
export function planRunTimingSentence(run: ExecutionPlanRunView, now = Date.now()): string {
  const timing = planRunTiming(run, now)
  const startedAt = run.started_at || run.created_at
  if (run.active) {
    return [
      startedAt ? `Started ${formatDateTime(startedAt)}` : '',
      timing.elapsedSeconds !== null ? `${formatPlanDuration(timing.elapsedSeconds)} elapsed` : '',
      timing.remainingSeconds !== null
        ? `about ${formatPlanDuration(timing.remainingSeconds)} left`
        : 'end time unknown (a run has no fixed duration)',
    ].filter(Boolean).join(' · ')
  }
  const endedAt = run.ended_at || run.completed_at
  return [
    endedAt ? `Finished ${formatDateTime(endedAt)}` : '',
    timing.tookSeconds !== null ? `took ${formatPlanDuration(timing.tookSeconds)}` : '',
  ].filter(Boolean).join(' · ')
}

/** A run Stop plan was asked for, while its running entry stops. */
export function planRunStopping(run: Pick<ExecutionPlanRunView, 'outcome' | 'entries'>): boolean {
  return run.outcome === 'running' && run.entries.some(entry => entry.stopped)
}

export function planRunOutcomeLabel(run: Pick<ExecutionPlanRunView, 'outcome'> & Partial<Pick<ExecutionPlanRunView, 'entries'>>): string {
  switch (run.outcome) {
    case 'running': return run.entries && planRunStopping({ outcome: run.outcome, entries: run.entries }) ? 'Stopping' : 'Running'
    case 'completed': return 'Completed'
    case 'stopped': return 'Stopped'
    default: return 'Failed'
  }
}

export function planRunOutcomeTone(run: Pick<ExecutionPlanRunView, 'outcome'>): 'info' | 'success' | 'muted' | 'danger' {
  switch (run.outcome) {
    case 'running': return 'info'
    case 'completed': return 'success'
    case 'stopped': return 'muted'
    default: return 'danger'
  }
}

/** An entry's state in plain words, preferring the live state of its run. */
export function planEntryStatusLabel(entry: ExecutionPlanRunEntryView | ExecutionPlanRunEntry): { label: string; tone: 'info' | 'success' | 'muted' | 'danger' | 'warning' } {
  switch ((('test_status' in entry && entry.test_status) || '').toUpperCase()) {
    case 'CREATED':
    case 'QUEUED':
    case 'PENDING':
    case 'RESERVED':
    case 'SCHEDULED':
      return { label: 'Waiting to start', tone: 'muted' }
    case 'INITIALIZING':
    case 'SHAKEOUT':
    case 'SHAKEOUT_REVIEW':
      return { label: 'Starting', tone: 'info' }
    case 'RUNNING':
      return { label: 'Running', tone: 'info' }
    case 'PAUSED':
      return { label: 'Paused', tone: 'warning' }
    case 'STOPPING':
      return { label: 'Stopping', tone: 'info' }
    case 'COMPLETE':
      return { label: 'Completed', tone: 'success' }
    case 'FAILED':
      return { label: 'Failed', tone: 'danger' }
    case 'ABORTED':
      return { label: 'Aborted', tone: 'danger' }
  }
  switch (entry.status) {
    case 'pending': return { label: 'Not started', tone: 'muted' }
    case 'running':
    case 'created': return { label: 'Starting', tone: 'info' }
    case 'complete': return { label: 'Completed', tone: 'success' }
    case 'failed': return { label: 'Failed', tone: 'danger' }
    case 'skipped': return { label: 'Skipped', tone: 'muted' }
    default: return { label: entry.status || 'Unknown', tone: 'muted' }
  }
}

function activeEntryCount(run: ExecutionPlanRunView): number {
  return run.entries.filter(entry => entry.test_status && !['COMPLETE', 'FAILED', 'ABORTED'].includes(entry.test_status.toUpperCase())).length
}

export type PlanCardState =
  | { kind: 'running'; run: ExecutionPlanRunView; summary: string; extraRuns: number; stopping: boolean }
  | { kind: 'waiting_approval'; summary: string; approvalId?: string; occurrence?: ExecutionPlanWaitingOccurrence }
  | { kind: 'queued'; summary: string; occurrence: ExecutionPlanWaitingOccurrence }
  | { kind: 'decision'; summary: string; occurrence: ExecutionPlanWaitingOccurrence }
  | { kind: 'idle' }

function scheduledRunPhrase(occurrence: ExecutionPlanWaitingOccurrence): string {
  const schedule = occurrence.schedule_name ? `“${occurrence.schedule_name}” ` : ''
  return `The ${schedule}run planned for ${formatDateTime(occurrence.planned_at)}`
}

/** What the plan card leads with: running, held, or nothing in progress. */
export function planCardState(activity: ExecutionPlanActivity | undefined, now = Date.now()): PlanCardState {
  if (!activity) return { kind: 'idle' }
  const active = activity.active_run
  if (active) {
    const entries = active.entries.filter(entry => entry.status !== 'skipped' || entry.detail !== 'entry disabled')
    const running = activeEntryCount(active)
    // A sequential run is on its running entry, else the next one to start.
    const current = entries.findIndex(entry => entry.status === 'running')
    const next = current >= 0 ? current : entries.findIndex(entry => entry.status === 'pending')
    const progress = active.run_mode === 'sequential'
      ? `entry ${next >= 0 ? next + 1 : entries.length} of ${entries.length}`
      : `${running} of ${entries.length} run${entries.length === 1 ? '' : 's'} active`
    return { kind: 'running', run: active, summary: `${progress} · ${planRunTimingSentence(active, now)}`, extraRuns: Math.max(0, (activity.active_runs || 1) - 1), stopping: planRunStopping(active) }
  }
  const waiting = activity.waiting || []
  const approvalHold = waiting.find(occurrence => occurrence.reason_code === 'approval_required')
  if (approvalHold) {
    return {
      kind: 'waiting_approval',
      approvalId: approvalHold.approval_id || activity.pending_approval?.id,
      occurrence: approvalHold,
      summary: `${scheduledRunPhrase(approvalHold)} is held until a project admin approves it. Later runs of this schedule wait for that decision.`,
    }
  }
  if (activity.pending_approval) {
    return {
      kind: 'waiting_approval',
      approvalId: activity.pending_approval.id,
      summary: `A run is held until a project admin approves it${activity.pending_approval.decision_reason ? `: ${activity.pending_approval.decision_reason}` : '.'}`,
    }
  }
  const queued = waiting.find(occurrence => occurrence.reason_code === 'waiting_for_previous_run')
  if (queued) {
    return { kind: 'queued', occurrence: queued, summary: `${scheduledRunPhrase(queued)} starts when the plan's previous run finishes.` }
  }
  const decision = waiting[0]
  if (decision) {
    return { kind: 'decision', occurrence: decision, summary: `${scheduledRunPhrase(decision)} was missed and waits for a project admin to retry, catch up or skip it in Schedules.` }
  }
  return { kind: 'idle' }
}

/** A scheduled or requested run held before it started, whether or not another run is active. */
export function planHeldState(activity: ExecutionPlanActivity | undefined, now = Date.now()): PlanCardState {
  if (!activity) return { kind: 'idle' }
  return planCardState({ ...activity, active_run: undefined, active_runs: 0 }, now)
}

/** "Last run: Completed · Oct 12, 6:45 AM · took 18 min". */
export function planLastRunSentence(activity: ExecutionPlanActivity | undefined): string {
  const last = activity?.last_run
  if (!last) return ''
  return [`Last run: ${planRunOutcomeLabel(last)}`, planRunTimingSentence(last)].filter(Boolean).join(' · ')
}

type PlanReleaseNames = { applications: Record<string, string>; releases: Record<string, string> }

/** The plan's release as "Application › Release", or '' when it has none. */
export function planReleaseName(plan: { application_id?: string; release_id?: string }, names: PlanReleaseNames): string {
  if (!plan.release_id) return ''
  const application = plan.application_id ? names.applications[plan.application_id] : ''
  const release = names.releases[plan.release_id] || plan.release_id
  return `${application ? `${application} › ` : ''}${release}`
}

/** Where a plan files its runs, by name when the release is known. */
export function planReleaseSentence(plan: { application_id?: string; release_id?: string }, names: PlanReleaseNames): string {
  const release = planReleaseName(plan, names)
  return release ? `Runs are filed under ${release}` : 'Runs are filed under Imported Legacy (no release chosen)'
}

/**
 * A held scheduled run's state in plain words, for schedule rows. Only these
 * two actions always hold the run at next_run_at; a schedule also asks for a
 * decision after a run failed, once it has moved on, so that row keeps "next"
 * and shows its last error.
 */
export function scheduleHoldSentence(nextRequiredAction?: string, nextRunAt?: string): string {
  if (!nextRunAt) return ''
  switch (nextRequiredAction) {
    case 'approval_required':
      return `Waiting for approval: the ${formatDateTime(nextRunAt)} run is held; later runs wait for the decision.`
    case 'waiting_for_previous_run':
      return `Queued: the ${formatDateTime(nextRunAt)} run starts when the plan's previous run finishes.`
    default:
      return ''
  }
}

/** Plain words for a schedule occurrence's status and reason. */
export function occurrenceStatusLabel(status: string, reasonCode?: string): string {
  if (status === 'blocked') {
    switch (reasonCode) {
      case 'approval_required': return 'Waiting for approval'
      case 'waiting_for_previous_run': return 'Queued behind the previous run'
      case 'missed_run_requires_decision': return 'Missed — needs a decision'
      case 'plan_not_found': return 'Blocked — plan deleted'
      default: return 'Blocked'
    }
  }
  if (status === 'skipped') {
    switch (reasonCode) {
      case 'previous_run_active': return 'Skipped — previous run still running'
      case 'schedule_changed': return 'Skipped — schedule changed while queued'
      case 'approval_rejected': return 'Skipped — approval rejected'
      case 'missed_run_skipped': return 'Skipped — missed'
      case 'object_archived': return 'Skipped — plan archived'
      default: return 'Skipped'
    }
  }
  switch (status) {
    case 'materialized': return 'Started'
    case 'materializing': return 'Starting'
    case 'failed': return 'Failed to start'
    default: return status
  }
}

/** A run's plan and place in it: "Nightly regression, entry 2 of 3". */
export function executionPlanRunLabel(link: TestExecutionPlanLink): string {
  const name = link.plan_name?.trim() || 'Execution plan'
  const position = link.entry_number && link.entry_count ? `, entry ${link.entry_number} of ${link.entry_count}` : ''
  return `${name}${position}`
}
