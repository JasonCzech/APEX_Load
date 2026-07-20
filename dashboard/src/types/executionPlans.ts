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

export interface ExecutionPlan {
  id: string
  project_id?: string
  name: string
  description?: string
  entries: ExecutionPlanEntry[]
  run_mode: string
  timeslot_id?: string
  status?: string
  created_at: string
  updated_at: string
}

export interface ExecutionPlanListResponse {
  execution_plans: ExecutionPlan[]
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
  runs: ExecutionPlanRun[]
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
  run_name_template?: string
  enabled?: boolean
  missed_run_policy?: string
  disabled_from?: string
  disabled_until?: string
  expected_revision?: number
}

export interface ExecutionPlanScheduleListResponse {
  schedules: ExecutionPlanSchedule[]
  count: number
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
