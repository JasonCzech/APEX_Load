// The goal guardrail ceilings beyond target, latency and error rate, and
// which runs can produce each one's measure.

type CeilingField = 'max_avg_server_processing_ms' | 'max_avg_transfer_ms' | 'max_error_count' | 'max_lcp_ms' | 'max_inp_ms' | 'max_cls'
  | 'max_throttled_requests' | 'max_end_to_end_latency_ms' | 'max_partition_skew_pct' | 'max_redelivery_rate_pct' | 'max_dead_letter_rate_pct'

// Which runs can produce a ceiling's measure: browser vitals come from browser
// groups, streaming and messaging measures from Kafka, Event Hubs and JMS.
export type CeilingScope = 'any' | 'browser' | 'messaging'

export const GOAL_CEILINGS: Array<{ field: CeilingField; label: string; scope: CeilingScope; max?: number; min?: number; step: string; placeholder: string; title?: string }> = [
  { field: 'max_avg_server_processing_ms', label: 'Avg Server Processing Ceiling (ms)', scope: 'any', step: 'any', placeholder: 'e.g. 450' },
  { field: 'max_avg_transfer_ms', label: 'Avg Transfer Ceiling (ms)', scope: 'any', step: 'any', placeholder: 'e.g. 120' },
  { field: 'max_error_count', label: 'Max Error Count', scope: 'any', min: 1, step: '1', placeholder: 'e.g. 25' },
  { field: 'max_lcp_ms', label: 'Max LCP (ms)', scope: 'browser', step: 'any', placeholder: 'e.g. 2500', title: 'Largest Contentful Paint ceiling, evaluated against browser canary metrics.' },
  { field: 'max_inp_ms', label: 'Max INP (ms)', scope: 'browser', step: 'any', placeholder: 'e.g. 200', title: 'Interaction to Next Paint ceiling, evaluated against browser canary metrics.' },
  { field: 'max_cls', label: 'Max CLS', scope: 'browser', step: 'any', placeholder: 'e.g. 0.1', title: 'Cumulative Layout Shift ceiling, evaluated against browser canary metrics.' },
  { field: 'max_throttled_requests', label: 'Max Throttled Requests', scope: 'messaging', step: '1', placeholder: 'e.g. 0', title: 'Maximum total throttled, quota, or server-busy responses during the run.' },
  { field: 'max_end_to_end_latency_ms', label: 'Max End-to-End Latency (ms)', scope: 'messaging', step: 'any', placeholder: 'e.g. 500', title: 'Maximum average producer-to-observed-consumer latency.' },
  { field: 'max_partition_skew_pct', label: 'Max Partition Skew %', scope: 'messaging', max: 100, step: 'any', placeholder: 'e.g. 10' },
  { field: 'max_redelivery_rate_pct', label: 'Max Redelivery Rate %', scope: 'messaging', max: 100, step: 'any', placeholder: 'e.g. 1' },
  { field: 'max_dead_letter_rate_pct', label: 'Max Dead-Letter Rate %', scope: 'messaging', max: 100, step: 'any', placeholder: 'e.g. 0.5' },
]

const SCOPE_PROTOCOLS: Record<Exclude<CeilingScope, 'any'>, ReadonlySet<string>> = {
  browser: new Set(['browser']),
  messaging: new Set(['kafka', 'eventhubs', 'jms']),
}

/** Whether a ceiling of this scope means anything for groups of these protocols. */
export function goalCeilingApplies(scope: CeilingScope, protocols?: readonly string[]): boolean {
  if (scope === 'any' || !protocols) return true
  return protocols.some(protocol => SCOPE_PROTOCOLS[scope].has(protocol.trim().toLowerCase()))
}
