export type ScenarioMode = 'vuser_driven' | 'goal_oriented'

export interface TransactionSLARule {
  transaction: string
  metric: 'average_duration_ms' | 'p90_duration_ms' | 'p95_duration_ms' | 'min_duration_ms' | 'max_duration_ms' | 'error_count' | 'error_rate_pct' | 'minimum_count' | 'bytes_sent' | 'bytes_received' | 'tps'
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

/** Goal-oriented load targets and their live controller status. */
export interface GoalConfig {
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
