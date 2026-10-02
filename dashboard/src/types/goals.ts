export type ScenarioMode = 'vuser_driven' | 'goal_oriented'

export interface TransactionSLARule {
  population?: 'transaction' | 'action'
  protocol?: string
  group?: string
  transaction: string
  metric: 'average_duration_ms' | 'p90_duration_ms' | 'p95_duration_ms' | 'p99_duration_ms' | 'max_acknowledgement_ms' | 'min_duration_ms' | 'max_duration_ms' | 'error_count' | 'error_rate_pct' | 'minimum_count' | 'bytes_sent' | 'bytes_received' | 'tps' | 'max_consumer_lag_ms' | 'max_consumer_offset_lag'
  comparator: 'lt' | 'lte' | 'gt' | 'gte' | 'eq' | 'neq'
  threshold: number
  severity?: 'fail' | 'warning'
}

export interface TransactionSLARuleResult {
  rule: TransactionSLARule
  status: 'pass' | 'fail' | 'warning' | 'not_evaluated'
  observed?: number
  matched_count: number
  message: string
}

export interface TransactionResponseTimeSLAResult {
	group: string
	transaction: string
	threshold_ms: number
	total_count: number
	eligible_success_count: number
	excluded_error_count: number
	missed_count: number
	missed_pct: number
	met_pct: number
	required_met_pct: number
	status: 'pass' | 'fail' | 'not_evaluated'
}

/** Goal-oriented load targets and their live controller status. */
export interface GoalConfig {
	/** Scenario-wide default SLA for each named business transaction. */
	transaction_response_time_sla_ms?: number
  target_average_tps?: number
  p95_latency_ms?: number
  max_error_pct?: number
  max_avg_server_processing_ms?: number
  max_avg_transfer_ms?: number
  max_error_count?: number
  min_vusers?: number
  max_vusers?: number
  max_lcp_ms?: number
  max_inp_ms?: number
  max_cls?: number
  max_throttled_requests?: number
  max_end_to_end_latency_ms?: number
  max_partition_skew_pct?: number
  max_redelivery_rate_pct?: number
  max_dead_letter_rate_pct?: number
  stop_on_sla_breach?: boolean
  stop_on_max_errors?: boolean
  sla_breach_window?: string
  transaction_rules?: TransactionSLARule[]
  early_stop?: {
    enabled: boolean
    error_rate_pct: number
    breach_window: string
  }
  workload_shape_config?: {
    stepped_ramp?: SteppedRampConfig
    closed_vuser?: ClosedVUserConfig
    goal_driven?: GoalDrivenConfig
  }
}

export interface GoalDrivenARTConfig {
  basis: 'manual' | 'baseline'
  art_ms: number
  baseline_test_id?: string
  baseline_group?: string
}

export interface GoalDrivenGroupDefinition {
  target_tps: number
  art?: GoalDrivenARTConfig
}

export interface GoalDrivenConfig {
  groups: Record<string, GoalDrivenGroupDefinition>
}

export type SteppedRampIncrementMode = 'step_count' | 'vusers_per_step'

export interface SteppedRampDefinition {
  start_vusers: number
  end_vusers: number
  increment_by: SteppedRampIncrementMode
  step_count?: number
  vusers_per_step?: number
}

export interface SteppedRampConfig {
  scope: 'scenario' | 'group'
  scenario?: SteppedRampDefinition
  groups?: Record<string, SteppedRampDefinition>
}

export interface ClosedVUserDefinition {
  peak_vusers: number
  ramp_up: string
  sustain: string
  ramp_down_enabled: boolean
  ramp_down: string
}

export interface ClosedVUserConfig {
  scope: 'scenario' | 'group'
  scenario?: ClosedVUserDefinition
  groups?: Record<string, ClosedVUserDefinition>
}

export interface GoalStatus {
  mode: 'goal_oriented'
  current_tps: number
  current_total_vusers: number
  target_average_tps?: number
  max_error_count?: number
  min_vusers?: number
  max_vusers?: number
  suggested_total_vusers?: number
  last_scale_direction?: 'up' | 'down'
  last_scale_reason?: string
  last_scale_at?: string
  latency_breached?: boolean
  error_breached?: boolean
  server_processing_breached?: boolean
  transfer_breached?: boolean
  error_count_breached?: boolean
  lcp_breached?: boolean
  inp_breached?: boolean
  cls_breached?: boolean
  throttled_requests_breached?: boolean
  end_to_end_latency_breached?: boolean
  partition_skew_breached?: boolean
  redelivery_rate_breached?: boolean
  dead_letter_rate_breached?: boolean
  breach_active?: boolean
  breach_duration_sec?: number
  stop_triggered?: boolean
  stop_reason?: string
}

export interface GoalTargetStatus {
  group: string
  target_tps: number
  updated_at?: string
}
