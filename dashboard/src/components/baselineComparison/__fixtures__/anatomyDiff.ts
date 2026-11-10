// Test builders for the time anatomy diff (model and section tests).
import type { RunArchiveAnatomyRequest, RunArchivePhaseStat, RunArchiveTimeAnatomy, RunArchiveTransactionAnatomy } from '../../../types/timeAnatomy'

export const stat = (avg: number, p95 = avg, extra: Partial<RunArchivePhaseStat> = {}): RunArchivePhaseStat => ({ samples: 100, avg_ms: avg, p50_ms: avg, p90_ms: p95, p95_ms: p95, p99_ms: p95, max_ms: p95, ...extra })

type Phases = Partial<Record<'dns' | 'connect' | 'tls' | 'server' | 'download' | 'unsplit', number>>

export function requestRow(step: number, action: string, phases: Phases, opts: { occurrence?: number; offset?: number; newConns?: number; reused?: number; p95Scale?: number } = {}): RunArchiveAnatomyRequest {
  const occurrence = opts.occurrence ?? 1
  const scale = opts.p95Scale ?? 2
  const s = (name: keyof Phases) => stat(phases[name] ?? 0, (phases[name] ?? 0) * scale)
  const total = Object.values(phases).reduce((a, b) => a + (b || 0), 0)
  return {
    key: `${step}|${action}#${occurrence}`,
    action_name: action,
    ...(step ? { source_step_index: step } : {}),
    occurrence,
    samples: 100,
    failed: 0,
    presence_pct: 100,
    offset_ms: stat(opts.offset ?? 0),
    phases: { dns: s('dns'), connect: s('connect'), tls: s('tls'), server: s('server'), download: s('download'), unsplit: s('unsplit'), total: stat(total, total * 1.5) },
    connections: { new: opts.newConns ?? 0, reused: opts.reused ?? 0, unknown: 0, inconsistent: 0 },
  }
}

export function transaction(name: string, requests: RunArchiveAnatomyRequest[], means: Phases & { non_request?: number; overlap?: number; p95?: number; instances?: number }): RunArchiveTransactionAnatomy {
  const p = { dns_ms: means.dns ?? 0, connect_ms: means.connect ?? 0, tls_ms: means.tls ?? 0, server_ms: means.server ?? 0, download_ms: means.download ?? 0, unsplit_ms: means.unsplit ?? 0 }
  const avg = Object.values(p).reduce((a, b) => a + b, 0) + (means.non_request ?? 0) - (means.overlap ?? 0)
  return {
    name,
    instances: 100,
    joined_instances: means.instances ?? 100,
    failed_instances: 0,
    interrupted_instances: 0,
    duration: stat(avg, means.p95 ?? avg * 2),
    decomposition: { instances: means.instances ?? 100, avg_duration_ms: avg, phases: p, non_request_ms: means.non_request ?? 0, overlap_ms: means.overlap ?? 0, avg_requests_per_instance: requests.length },
    requests,
  }
}

export function anatomyOf(transactions: RunArchiveTransactionAnatomy[], notEmitted = 0): RunArchiveTimeAnatomy {
  return {
    schema: 'apexload.time_anatomy.v1',
    phase_model: 'sequential_clipped_v1',
    join_rule: 'vuser_iteration_window_v1',
    transactions,
    coverage: { transaction_rows: 0, joined_transaction_rows: 0, transaction_rows_without_requests: 0, unjoinable_transaction_rows: 0, interrupted_transaction_rows: 0, request_rows: 0, requests_in_transactions: 0, requests_outside_transactions: 0, requests_claimed_across_iteration: 0, late_request_rows: 0, requests_ending_after_transaction: 0, clamped_request_samples: 0, retried_request_samples: 0, legacy_identity_rows: 0, unknown_population_rows: 0 },
    truncation: { truncated: notEmitted > 0, transactions_tracked: transactions.length + notEmitted, ...(notEmitted ? { transactions_not_emitted: notEmitted } : {}) },
  }
}
