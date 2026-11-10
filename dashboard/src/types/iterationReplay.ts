// Iteration replay (apexload.iteration_index.v1 in the archive report's
// `iterations` field, and apexload.iteration_waterfall.v1 from
// /tests/{id}/archive/iterations/waterfall).
//
// These mirror pkg/api/archive_iteration_types.go key for key. Optional keys
// are exactly the Go omitempty/pointer fields. `vuser_lifetime` is a 63-bit
// nonce, so it crosses JSON as a string.

import type { ArchiveWaterfallRow } from './timeAnatomy'

export type IterationRepresentativeReason = 'slowest' | 'p50' | 'p95' | 'failed'

export type IterationIndexTruncationReason = 'identity_evicted' | 'iteration_sample'

export type IterationWaterfallPartialReason = 'scan_budget' | 'ambiguous_vuser_id'

export interface RunArchiveIterationIndex {
  schema: string
  iterations_total: number
  identities: number
  identity_quality: { lifetime: number; legacy: number; none_rows: number }
  span: { samples: number; p50_ms: number; p95_ms: number; max_ms: number; percentile_quality?: string }
  rows_outside_iterations?: number
  /**
   * Rows written after their VUser identity had moved on two or more
   * iterations: outside every span here, though the replay finds them. A
   * late row of the iteration just before the open one joins its span.
   */
  late_rows?: number
  untimed_iterations?: number
  identities_evicted?: number
  /** Timed iterations left out of span and the p50/p95 picks: cut short by the test's stop, or split. */
  partial_iterations?: number
  truncated: boolean
  reasons?: IterationIndexTruncationReason[]
  representatives: RunArchiveIterationRef[]
}

export interface RunArchiveIterationRef {
  reason: IterationRepresentativeReason
  target_ms?: number
  sampled?: boolean
  identity_quality: 'lifetime' | 'legacy'
  group?: string
  vuser_id?: number
  vuser_lifetime?: string
  iteration: number
  first_ordinal: number
  last_ordinal: number
  started_at?: string
  span_ms: number
  transaction_rows: number
  request_rows: number
  /** Failed rows; rows interrupted by the test's stop or ramp-down are not failures. */
  failed_rows: number
  /**
   * The index's identity eviction split this iteration: its rows may lie
   * outside first_ordinal..last_ordinal, so a replay must not send them as hints.
   */
  split?: boolean
  /**
   * A row was interrupted by the test's stop or ramp-down: the iteration was
   * cut short and left out of span, so a slowest ref can exceed span.max_ms.
   */
  interrupted?: boolean
}

export interface ArchiveIterationWaterfall {
  schema: string
  identity_quality: 'lifetime' | 'legacy'
  group?: string
  vuser_id?: number
  vuser_lifetime?: string
  iteration: number
  first_ordinal: number
  last_ordinal: number
  started_at?: string
  span_ms: number
  /** By start, then ordinal; offset_ms is from started_at. */
  rows: ArchiveWaterfallRow[]
  rows_total: number
  truncated?: boolean
  partial?: boolean
  partial_reasons?: IterationWaterfallPartialReason[]
  think_ms_inside_transactions: number
  /** Think time was summed in bounded memory over very many transactions: approximate. */
  think_ms_approximate?: boolean
  /** Time inside the span no row covers; absent when rows were cut or may be missing. */
  gaps_ms?: number
}

export interface ArchiveIterationWaterfallParams {
  group?: string
  vuser_id?: number
  vuser_lifetime?: string
  iteration: number
  first_ordinal?: number
  last_ordinal?: number
  /** One row known to belong to the iteration: picks between reused legacy VUser IDs. */
  ordinal?: number
  max_rows?: number
}

/** What the Run Report asks the iteration replay panel to open. */
export interface IterationReplayTarget extends ArchiveIterationWaterfallParams {
  /** Where the request came from, for the heading. */
  source?: string
}
