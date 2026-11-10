// Report-time waterfalls (apexload.time_anatomy.v1), as the controller returns
// them in the archive report's `time_anatomy` field, and the drill-down
// (apexload.transaction_waterfall.v1) from
// /tests/{id}/archive/transactions/{event_id}/waterfall.
//
// These mirror pkg/api/archive_time_anatomy_types.go key for key. Optional
// keys are exactly the Go omitempty/pointer fields. `vuser_lifetime` is a
// 63-bit nonce, so it crosses JSON as a string.

import type { ExemplarLink } from './rca'

export type TimeAnatomyTruncationReason =
  | 'transaction_cap'
  | 'report_transaction_cap'
  | 'request_row_cap'
  | 'occurrence_fold'
  | 'pending_key_cap'
  | 'pending_global_cap'
  | 'instance_sample'
  | 'sample_budget'
  | 'identity_cap'
  | 'work_budget'

export type TimeAnatomyTailStatus = 'ok' | 'insufficient_samples' | 'no_tail' | 'low_join_coverage'

export type TimeAnatomyTailComponentName = 'dns' | 'connect' | 'tls' | 'server' | 'download' | 'unsplit' | 'non_request' | 'overlap'

export type TimeAnatomyRepresentativeReason = 'p50' | 'p95' | 'slowest' | 'failed'

export interface RunArchiveTimeAnatomy {
  schema: string
  phase_model: string
  join_rule: string
  transactions: RunArchiveTransactionAnatomy[]
  coverage: RunArchiveTimeAnatomyCoverage
  truncation: RunArchiveTimeAnatomyTruncation
}

export interface RunArchiveTimeAnatomyCoverage {
  transaction_rows: number
  joined_transaction_rows: number
  transaction_rows_without_requests: number
  /** Transaction rows that may have lost requests to a buffer drop or eviction; not counted as joined. */
  partial_transaction_rows?: number
  unjoinable_transaction_rows: number
  interrupted_transaction_rows: number
  request_rows: number
  requests_in_transactions: number
  requests_outside_transactions: number
  requests_claimed_across_iteration: number
  late_request_rows: number
  requests_ending_after_transaction: number
  clamped_request_samples: number
  retried_request_samples: number
  legacy_identity_rows: number
  unknown_population_rows: number
}

export interface RunArchiveTimeAnatomyTruncation {
  truncated: boolean
  reasons?: TimeAnatomyTruncationReason[]
  transactions_tracked: number
  transactions_omitted?: number
  transactions_not_emitted?: number
  request_rows_omitted?: number
  pending_requests_dropped?: number
  pending_requests_evicted?: number
  identities_evicted?: number
}

export interface RunArchiveTransactionAnatomy {
  name: string
  instances: number
  joined_instances: number
  failed_instances: number
  interrupted_instances: number
  /** Instances that may have lost requests to a buffer drop; excluded from decomposition, rows and tail means. */
  partial_instances?: number
  sla_status?: 'failed' | 'passed' | ''
  duration: RunArchivePhaseStat
  decomposition: RunArchiveTimeDecomposition
  requests: RunArchiveAnatomyRequest[]
  other_requests?: RunArchiveAnatomyRequest
  requests_truncated?: boolean
  tail?: RunArchiveTailAttribution
  representatives?: RunArchiveRepresentativeSample[]
}

export interface RunArchiveTimeDecomposition {
  /** Joined, non-interrupted instances. */
  instances: number
  avg_duration_ms: number
  phases: RunArchivePhaseMeans
  non_request_ms: number
  overlap_ms: number
  avg_requests_per_instance: number
}

export interface RunArchivePhaseMeans {
  dns_ms: number
  connect_ms: number
  tls_ms: number
  server_ms: number
  download_ms: number
  unsplit_ms: number
}

export interface RunArchivePhaseStat {
  samples: number
  avg_ms: number
  p50_ms: number
  p90_ms: number
  p95_ms: number
  p99_ms: number
  max_ms: number
  /** dns/connect/tls/server/download only. */
  observed_pct?: number
  avg_when_observed_ms?: number
  percentile_quality?: '' | 'approximate'
}

export interface RunArchiveAnatomyPhaseStats {
  dns: RunArchivePhaseStat
  connect: RunArchivePhaseStat
  tls: RunArchivePhaseStat
  server: RunArchivePhaseStat
  download: RunArchivePhaseStat
  unsplit: RunArchivePhaseStat
  total: RunArchivePhaseStat
}

export interface RunArchiveAnatomyRequest {
  key: string
  action_name: string
  source_step_index?: number
  occurrence: number
  occurrence_folded?: boolean
  dependency_type?: string
  samples: number
  failed: number
  presence_pct: number
  offset_ms: RunArchivePhaseStat
  phases: RunArchiveAnatomyPhaseStats
  connections: RunArchiveConnectionStats
}

export interface RunArchiveConnectionStats {
  new: number
  reused: number
  unknown: number
  inconsistent: number
  reuse_pct?: number
}

export interface RunArchiveTailAttribution {
  status: TimeAnatomyTailStatus
  threshold_p50_ms: number
  threshold_p95_ms: number
  threshold_quality?: string
  band_estimate: 'exact' | 'sampled'
  instances: number
  sampled_instances: number
  tail: RunArchiveTailBand
  median: RunArchiveTailBand
  delta: RunArchiveTailDelta
  /** Top 5 by delta. */
  requests?: RunArchiveRequestDelta[]
  /** ASCII, built by the controller, only when status is ok. */
  headline?: string
}

export interface RunArchiveTailBand {
  count: number
  joined: number
  failed: number
  avg_duration_ms: number
  phases: RunArchivePhaseMeans
  non_request_ms: number
  overlap_ms: number
}

export interface RunArchiveTailDelta {
  total_ms: number
  components: RunArchiveTailComponent[]
}

export interface RunArchiveTailComponent {
  component: TimeAnatomyTailComponentName
  delta_ms: number
  share_pct?: number
}

export interface RunArchiveRequestDelta {
  key: string
  action_name: string
  delta_ms: number
  dominant_phase: 'setup' | 'server' | 'download' | 'unsplit'
  dominant_ms: number
}

export interface RunArchiveRepresentativeSample {
  reason: TimeAnatomyRepresentativeReason
  target_ms?: number
  event_id: string
  /** The transaction row's line index. */
  ordinal: number
  /** The first claimed child's line index (equals ordinal when none). */
  first_ordinal: number
  /** Omitted when the archived row had no timestamp. */
  timestamp?: string
  duration_ms: number
  elapsed_ms?: number
  success: boolean
  status_code?: number
  error_class?: string
  group?: string
  vuser_id?: number
  /** 63-bit nonce: a decimal string, never a number. */
  vuser_lifetime?: string
  iteration?: number
  trace_id?: string
  children: number
  sampled?: boolean
}

export interface ArchiveTransactionWaterfall {
  schema: string
  join_rule: string
  phase_model: string
  identity_quality: 'lifetime' | 'legacy' | 'none'
  root: ArchiveWaterfallRow
  /** By start, then ordinal. */
  requests: ArchiveWaterfallRow[]
  requests_total: number
  truncated?: boolean
  late_requests?: number
  decomposition?: RunArchiveTimeDecomposition
  /** When the root is a request: innermost first. */
  enclosing_transactions?: ArchiveWaterfallRef[]
  /**
   * The root identity's own state made the report's instance partial: its
   * request buffer dropped a request this window could have claimed
   * (pending_key_cap), or the join's CPU budget ran out before it
   * (work_budget). A partial drill-down has no decomposition and its time
   * outside requests is unknown.
   */
  partial?: boolean
  partial_reasons?: string[]
  /**
   * The report's join hit run-wide state the drill-down cannot replay from
   * one identity (join_may_differ_reasons: pending_global_cap, identity_cap,
   * work_budget), or the report could not be read (no reasons). The report
   * may count this instance as partial, or claim fewer requests, than the
   * drill-down shows.
   */
  join_may_differ?: boolean
  join_may_differ_reasons?: string[]
  /** The run's APM link settings could not be read, so no row carries trace links. */
  trace_links_unavailable?: boolean
}

export interface ArchiveWaterfallRow {
  event_id: string
  ordinal: number
  population: string
  timestamp: string
  offset_ms: number
  duration_ms: number
  elapsed_ms?: number
  action_name: string
  source_transaction?: string
  source_step_index?: number
  dependency_type?: string
  dependency_target?: string
  http_version?: string
  status_code?: number
  success: boolean
  interrupted?: boolean
  error_class?: string
  error_message?: string
  /** Sums to duration_ms. */
  phases: RunArchivePhaseMeans
  clamped?: boolean
  retried?: boolean
  connection?: 'new' | 'reused' | 'unknown'
  trace_id?: string
  /**
   * Links that open this row's trace in the run's APM providers: only on
   * the transaction drill-down (not the iteration waterfall), and only when
   * the row has a trace id and the test has a trace link template.
   */
  trace_links?: ExemplarLink[]
  vuser_id?: number
  vuser_lifetime?: string
  iteration?: number
}

export interface ArchiveWaterfallRef {
  event_id: string
  ordinal: number
  action_name: string
  timestamp: string
  span_ms: number
}

export interface ArchiveTransactionWaterfallParams {
  ordinal?: number
  first_ordinal?: number
  max_requests?: number
}
