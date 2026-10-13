import type { ScenarioDataPoolBinding } from './dataPools'

/** Contracts for execution plans, persisted plan runs, and plan scheduling. */
export interface ExecutionPlanEntry {
  order: number
  scenario_id: string
  scenario_name?: string
  enabled?: boolean
  runtime_overrides?: unknown
  failure_policy?: string
  data_pools?: ScenarioDataPoolBinding[]
  timeslot_id?: string
}

/** What a plan does when started while an earlier run of it is still running. */
export type ExecutionPlanOverlapPolicy = 'skip' | 'queue' | 'allow'

export interface ExecutionPlan {
  id: string
  project_id?: string
  revision: number
  name: string
  description?: string
  entries: ExecutionPlanEntry[]
  run_mode: string
  timeslot_id?: string
  /** Where the plan's runs are filed. Empty files them under the project's legacy release. */
  application_id?: string
  release_id?: string
  /** Empty on plans saved before the policy existed; they behave as allow. */
  overlap_policy?: ExecutionPlanOverlapPolicy | ''
  status?: string
  created_at: string
  updated_at: string
}

/** A plan as listed with include_activity=true. */
export interface ExecutionPlanListItem extends ExecutionPlan {
  activity?: ExecutionPlanActivity
}

/** A plan run's entry with the live state of the run it started. */
export interface ExecutionPlanRunEntryView extends ExecutionPlanRunEntry {
  test_status?: string
  test_ref_key?: string
  started_at?: string
  ended_at?: string
  planned_seconds?: number
  vusers?: number
}

export type ExecutionPlanRunOutcome = 'running' | 'completed' | 'failed' | 'stopped'

/** A plan run with its entries' live state, outcome and timing. */
export interface ExecutionPlanRunView extends Omit<ExecutionPlanRun, 'entries'> {
  entries: ExecutionPlanRunEntryView[]
  active: boolean
  outcome: ExecutionPlanRunOutcome
  started_at?: string
  ended_at?: string
  /** Absent when a running or remaining entry has no fixed duration. */
  estimated_end_at?: string
  active_vusers?: number
}

/** A scheduled run of the plan held before it started. */
export interface ExecutionPlanWaitingOccurrence {
  schedule_id: string
  schedule_name?: string
  occurrence_id?: string
  planned_at: string
  reason_code: 'approval_required' | 'waiting_for_previous_run' | 'missed_run_requires_decision' | string
  approval_id?: string
  summary?: string
}

export interface ExecutionPlanActivity {
  active_run?: ExecutionPlanRunView
  active_runs?: number
  last_run?: ExecutionPlanRunView
  pending_approval?: ExecutionPlanApproval
  waiting?: ExecutionPlanWaitingOccurrence[]
}

/** The run already in progress when the overlap policy kept another from starting. */
export interface ExecutionPlanOverlap {
  overlap_policy: ExecutionPlanOverlapPolicy
  active_run_id: string
  started_at: string
  active_vusers?: number
  message: string
}

export interface ExecutionPlanListResponse {
  execution_plans: ExecutionPlanListItem[]
  count: number
  limit?: number
  offset?: number
}

export interface ExecutionPlanRunResult {
  order: number
  scenario_id: string
  test_id?: string
  status: string
  detail?: string
}

export interface ExecutionPlanRunResponse {
  execution_plan_id: string
  run_mode: string
  run_id?: string
  status?: string
  created_count?: number
  failed_count?: number
  results: ExecutionPlanRunResult[]
  materialized_at: string
}

export interface ExecutionPlanRunEntry {
  order: number
  scenario_id: string
  scenario_name?: string
  test_id?: string
  status: string
  detail?: string
  plan_name?: string
  application_id?: string
  release_id?: string
  /** Stop plan ended or skipped this entry. */
  stopped?: boolean
}

export interface ExecutionPlanRun {
  id: string
  plan_id: string
  project_id?: string
  run_mode: string
  status: string
  entries: ExecutionPlanRunEntry[]
  created_at: string
  updated_at: string
  completed_at?: string
}

export interface ExecutionPlanRunListResponse {
  runs: ExecutionPlanRunView[]
  count: number
  limit?: number
  offset?: number
}

export interface ExecutionPlanSchedule {
  id: string
  plan_id: string
  project_id?: string
  revision?: number
  name: string
  cron_expr: string
  timezone?: string
  /** Empty files the schedule's runs under the plan's release. */
  release_id?: string
  run_name_template?: string
  enabled: boolean
  missed_run_policy?: string
  conflict_summary?: string
  next_required_action?: string
  last_run_run_id?: string
  last_run_at?: string
  next_run_at?: string
  disabled_from?: string
  disabled_until?: string
  last_error?: string
  created_at: string
  updated_at: string
}

export interface ExecutionPlanScheduleRequest {
  name: string
  cron_expr: string
  timezone?: string
  /** Omitted keeps the stored release; empty uses the plan's. */
  release_id?: string
  run_name_template?: string
  enabled?: boolean
  missed_run_policy?: string
  disabled_from?: string
  disabled_until?: string
  expected_revision?: number
}

/**
 * A daylight-saving change in a schedule's timezone that its wall-clock times
 * fall into: when the clocks go forward the matched times do not exist and
 * are skipped; when they go back the matched times happen twice and run once,
 * at the first.
 */
export interface ScheduleClockChange {
  /** Local calendar date of the affected times (YYYY-MM-DD). */
  date: string
  kind: 'skipped' | 'repeated'
  /** The first few matched wall-clock times (HH:MM). */
  local_times: string[]
  /** Every matched wall-clock time of the change. */
  matches: number
  /** Repeated times: the one instant the first of them runs. */
  runs_at?: string
  /** The instant the clocks change. */
  at: string
}

/** A candidate schedule's next runs, from the server's own evaluator. */
export interface SchedulePreviewResponse {
  cron_expr: string
  timezone: string
  occurrences: string[]
  count: number
  conflict_summary?: string
  clock_changes?: ScheduleClockChange[]
}

export interface ExecutionPlanScheduleListResponse {
  schedules: ExecutionPlanSchedule[]
  count: number
  /** Each schedule's clock changes, by schedule ID; schedules without any are left out. */
  clock_changes?: Record<string, ScheduleClockChange[]>
}

export interface ExecutionPlanScheduleOccurrence {
  id: string
  schedule_id: string
  plan_id: string
  project_id?: string
  schedule_revision?: number
  revision?: number
  planned_at: string
  materialized_at?: string
  status: string
  reason_code?: string
  run_id?: string
  conflict_summary?: string
  created_at: string
  updated_at: string
}

export interface ScheduleOccurrenceResolutionRequest {
  action: 'retry' | 'catch_up' | 'skip'
  reason?: string
  expected_revision?: number
}

export interface ExecutionPlanScheduleOccurrenceListResponse {
  occurrences: ExecutionPlanScheduleOccurrence[]
  count: number
}

export interface ExecutionPlanApproval {
  id: string
  plan_id: string
  project_id?: string
  policy: string
  status: string
  requested_by?: string
  requested_at: string
  resolved_by?: string
  resolved_at?: string
  decision_reason?: string
  required_role?: string
  run_id?: string
  created_at: string
  updated_at: string
}

export interface ExecutionPlanApprovalListResponse {
  approvals: ExecutionPlanApproval[]
  count: number
}
