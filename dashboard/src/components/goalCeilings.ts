// The goal guardrail ceilings beyond target, latency and error rate, and
// which runs can produce each one's measure.

type CeilingField = 'max_avg_server_processing_ms' | 'max_avg_transfer_ms' | 'max_error_count' | 'max_lcp_ms' | 'max_inp_ms' | 'max_cls'
  | 'max_throttled_requests' | 'max_end_to_end_latency_ms' | 'max_partition_skew_pct' | 'max_redelivery_rate_pct' | 'max_dead_letter_rate_pct'
  | 'max_llm_p95_ttft_ms' | 'max_llm_p95_tpot_ms' | 'min_llm_output_tokens_per_sec' | 'min_llm_goodput_pct' | 'max_llm_throttle_rate_pct'

// Which runs can produce a ceiling's measure: browser vitals come from browser
// groups, streaming and messaging measures from Kafka, Event Hubs and JMS,
// and LLM inference measures from llm_request / stream_profile groups.
export type CeilingScope = 'any' | 'browser' | 'messaging' | 'llm'

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
  { field: 'max_llm_p95_ttft_ms', label: 'Max LLM P95 TTFT (ms)', scope: 'llm', step: 'any', placeholder: 'e.g. 400', title: 'Maximum 95th-percentile time to first token across streaming LLM requests.' },
  { field: 'max_llm_p95_tpot_ms', label: 'Max LLM P95 TPOT (ms)', scope: 'llm', step: 'any', placeholder: 'e.g. 25', title: 'Maximum 95th-percentile time per output token across streaming LLM requests.' },
  { field: 'min_llm_output_tokens_per_sec', label: 'Min LLM Output (tok/s)', scope: 'llm', step: 'any', placeholder: 'e.g. 120', title: 'Minimum sustained system output token throughput.' },
  { field: 'min_llm_goodput_pct', label: 'Min LLM Goodput (%)', scope: 'llm', max: 100, step: 'any', placeholder: 'e.g. 95', title: 'Minimum percentage of LLM requests meeting TTFT, TPOT, and E2E SLOs simultaneously.' },
  { field: 'max_llm_throttle_rate_pct', label: 'Max LLM 429 Throttle Rate (%)', scope: 'llm', max: 100, step: 'any', placeholder: 'e.g. 5', title: 'Maximum percentage of LLM requests throttled with HTTP 429.' },
]

const SCOPE_PROTOCOLS: Record<Exclude<CeilingScope, 'any'>, ReadonlySet<string>> = {
  browser: new Set(['browser']),
  messaging: new Set(['kafka', 'eventhubs', 'jms']),
  llm: new Set(['llm', 'openai', 'azure_openai']),
}

/** Whether a ceiling of this scope means anything for groups of these protocols. */
export function goalCeilingApplies(scope: CeilingScope, protocols?: readonly string[]): boolean {
  if (scope === 'any' || !protocols) return true
  return protocols.some(protocol => SCOPE_PROTOCOLS[scope].has(protocol.trim().toLowerCase()))
}
