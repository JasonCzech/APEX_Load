import type { GeneratorShardSummary, TestGroup } from './testConfiguration'
import type {
  SystemInventoryController,
  SystemInventoryGenerator,
} from './system'

/** Contracts owned by the scenario scheduling and run-calendar API domain. */
export interface ScenarioSchedule {
  id: string
  scenario_id: string
  project_id?: string
  name: string
  cron_expr: string
  timezone?: string
  release_id?: string
  timeslot_id?: string
  run_name_template?: string
  enabled: boolean
  revision?: number
  approval_policy?: 'auto' | 'manual_review' | 'high_cost' | 'off_hours' | 'production' | 'quota_burst' | string
  missed_run_policy?: 'skip' | 'catch_up_once' | 'catch_up_all' | 'require_operator_decision' | string
  conflict_summary?: string
  last_occurrence?: ScenarioScheduleOccurrence
  next_required_action?: string
  pending_approval_id?: string
  owner_subject?: string
  owner_display_name?: string
  governance?: ScenarioScheduleGovernance
  blackout_windows?: ScenarioScheduleWindow[]
  maintenance_windows?: ScenarioScheduleWindow[]
  availability_windows?: ScenarioScheduleWindow[]
  // Optional absolute date-range pause; either bound may be omitted for an
  // open-ended pause. Stored/served as RFC3339 UTC instants.
  disabled_from?: string
  disabled_until?: string
  last_run_test_id?: string
  last_run_at?: string
  next_run_at?: string
  last_error?: string
  created_at: string
  updated_at: string
}

export interface ScenarioScheduleGovernance {
  rbac_review?: {
    decision?: string
    reviewed_at?: string
    reviewed_by?: string
    required_roles?: string[]
    review_source?: string
  }
  quota_review?: {
    decision?: string
    project_max_concurrent_tests?: number
    project_max_vusers?: number
    estimated_peak_vusers?: number
    findings?: string[]
  }
  policy_version?: string
}

export interface ScenarioScheduleWindow {
  name?: string
  days_of_week?: number[]
  start_time: string
  end_time: string
  timezone?: string
}

export interface ScenarioScheduleOccurrence {
  id: string
  schedule_id: string
  scenario_id: string
  project_id?: string
  schedule_revision: number
  revision?: number
  planned_at: string
  materialized_at?: string
  status: 'planned' | 'materialized' | 'queued' | 'blocked' | 'skipped' | 'failed' | 'approved' | 'completed' | string
  reason_code?: string
  test_id?: string
  approval_id?: string
  conflict_summary?: string
  created_at: string
  updated_at: string
}

export interface ScenarioScheduleApproval {
  id: string
  schedule_id: string
  scenario_id: string
  project_id?: string
  schedule_revision: number
  policy: string
  status: 'pending' | 'approved' | 'rejected' | string
  requested_by?: string
  requested_at?: string
  resolved_by?: string
  resolved_at?: string
  decision_reason?: string
  required_role?: string
  created_at: string
  updated_at: string
}

export interface ScenarioScheduleHealth {
  total: number
  enabled: number
  due: number
  blocked: number
  failed: number
  pending_approval: number
  queued?: number
  materialized?: number
  skipped?: number
  completed?: number
  status_counts?: Record<string, number>
  is_leader?: boolean
  storage_error?: string
  sweeps?: Record<string, { last_sweep_at?: string; last_successful_at?: string; last_error?: string }>
  execution_plan_total?: number
  execution_plan_enabled?: number
  execution_plan_due?: number
  execution_plan_blocked?: number
  one_time_scheduled?: number
  one_time_due?: number
}

export interface RunCalendarRange {
  from: string
  to: string
  requested_from: string
  requested_to: string
  history_floor?: string
  future_ceiling: string
  history_clamped?: boolean
  future_clamped?: boolean
  retention_enforced?: boolean
}

export interface RunCalendarUser {
  display_name?: string
  subject?: string
  attribution?: 'started_by' | 'created_by' | 'schedule_owner' | string
  owner_subject?: string
  owner_display_name?: string
  created_by_subject?: string
  created_by_display_name?: string
  started_by_subject?: string
  started_by_display_name?: string
}

export interface RunCalendarWorkload {
  total_vusers?: number
  duration?: string
  groups?: TestGroup[]
}

export interface RunCalendarTopology {
  source?: 'live' | 'archive' | 'planned' | 'legacy' | string
  controller?: SystemInventoryController
  controller_env?: Record<string, string>
  generator_shards?: GeneratorShardSummary[]
  generator_inventory?: SystemInventoryGenerator[]
  summary?: string
}

export interface RunCalendarEvent {
  id: string
  source: 'cron' | 'ad_hoc' | 'execution_plan' | string
  run_type: 'scheduled' | 'ad_hoc' | 'execution_plan' | string
  title: string
  status: string
  start_at: string
  end_at?: string
  projected?: boolean
  occurrence_status?: string
  reason_code?: string
  conflict_summary?: string
  test_id?: string
  test_ref_key?: string
  schedule_id?: string
  schedule_name?: string
  cron_expr?: string
  timezone?: string
  scenario_id?: string
  scenario_name?: string
  plan_id?: string
  application_id?: string
  release_id?: string
  project_id?: string
  workload: RunCalendarWorkload
  user: RunCalendarUser
  topology: RunCalendarTopology
}

export interface RunCalendarCapacityDay {
  date: string
  configured_generators: number
  available_generators: number
  peak_reserved_generators: number
  reservation_count: number
  pressure_pct: number
  pressure: 'clear' | 'tight' | 'over' | string
}

export interface RunCalendarCapacity {
  project_id?: string
  days: RunCalendarCapacityDay[]
}

export interface RunCalendarResponse {
  generated_at: string
  range: RunCalendarRange
  events: RunCalendarEvent[]
  count: number
  status_counts?: Record<string, number>
  source_counts?: Record<string, number>
  truncated?: boolean
  capacity?: RunCalendarCapacity
}
