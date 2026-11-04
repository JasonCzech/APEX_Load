// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import RunReportLLMPanel from './RunReportLLMPanel'
import { buildLLMCharts } from './testExecutionChartModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('RunReportLLMPanel and buildLLMCharts', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('returns null when report has no LLM requests', async () => {
    await act(async () => {
      root.render(<RunReportLLMPanel report={undefined} />)
    })
    expect(container.textContent).toBe('')
  })

  it('renders LLM summary, input-length bands, model/action breakdowns, and golden prompt variance', async () => {
    await act(async () => {
      root.render(
        <RunReportLLMPanel
          report={{
            summary: {
              requests: 42,
              streaming_requests: 40,
              ttft_ms: { p50: 85, p90: 140, p95: 180, p99: 260, max: 310 },
              tpot_ms: { p50: 12, p90: 18, p95: 22, p99: 30, max: 35 },
              itl_max_ms: { p50: 18, p90: 32, p95: 45, p99: 60, max: 80 },
              e2e_ms: { p50: 450, p90: 700, p95: 890, p99: 1200, max: 1400 },
              output_tps: { p50: 78, p90: 92, p95: 98, p99: 105, max: 112 },
              system_output_tps: 1420.5,
              prompt_tokens: 18400,
              output_tokens: 9200,
              reasoning_tokens: 1200,
              cached_tokens: 4600,
              cache_hit_ratio: 0.25,
              truncated: 2,
              refused: 1,
              tool_calls: 3,
              stalls: 1,
              throttled_429: 4,
              tokens_estimated: true,
            },
            by_model: {
              'gpt-4o-mini': {
                requests: 42,
                streaming_requests: 40,
                ttft_ms: { p50: 85, p90: 140, p95: 180, p99: 260, max: 310 },
                tpot_ms: { p50: 12, p90: 18, p95: 22, p99: 30, max: 35 },
                itl_max_ms: { p50: 18, p90: 32, p95: 45, p99: 60, max: 80 },
                e2e_ms: { p50: 450, p90: 700, p95: 890, p99: 1200, max: 1400 },
                output_tps: { p50: 78, p90: 92, p95: 98, p99: 105, max: 112 },
                system_output_tps: 1420.5,
                prompt_tokens: 18400,
                output_tokens: 9200,
                reasoning_tokens: 1200,
                cached_tokens: 4600,
                cache_hit_ratio: 0.25,
                truncated: 2,
                refused: 1,
                tool_calls: 3,
                stalls: 1,
                throttled_429: 4,
              },
            },
            by_action: {
              chat_completion: {
                requests: 42,
                streaming_requests: 40,
                ttft_ms: { p50: 85, p90: 140, p95: 180, p99: 260, max: 310 },
                tpot_ms: { p50: 12, p90: 18, p95: 22, p99: 30, max: 35 },
                itl_max_ms: { p50: 18, p90: 32, p95: 45, p99: 60, max: 80 },
                e2e_ms: { p50: 450, p90: 700, p95: 890, p99: 1200, max: 1400 },
                output_tps: { p50: 78, p90: 92, p95: 98, p99: 105, max: 112 },
                system_output_tps: 1420.5,
                prompt_tokens: 18400,
                output_tokens: 9200,
                reasoning_tokens: 1200,
                cached_tokens: 4600,
                cache_hit_ratio: 0.25,
                truncated: 2,
                refused: 1,
                tool_calls: 3,
                stalls: 1,
                throttled_429: 4,
              },
            },
            by_input_band: [
              {
                band: 'short',
                requests: 30,
                summary: {
                  requests: 30,
                  streaming_requests: 30,
                  ttft_ms: { p50: 70, p90: 110, p95: 140, p99: 200, max: 240 },
                  tpot_ms: { p50: 11, p90: 16, p95: 20, p99: 26, max: 30 },
                  itl_max_ms: { p50: 16, p90: 28, p95: 38, p99: 50, max: 65 },
                  e2e_ms: { p50: 380, p90: 600, p95: 750, p99: 980, max: 1100 },
                  output_tps: { p50: 82, p90: 95, p95: 100, p99: 108, max: 112 },
                  system_output_tps: 1100,
                  prompt_tokens: 6000,
                  output_tokens: 6000,
                  reasoning_tokens: 600,
                  cached_tokens: 3000,
                  cache_hit_ratio: 0.5,
                  truncated: 0,
                  refused: 0,
                  tool_calls: 2,
                  stalls: 0,
                  throttled_429: 1,
                },
              },
            ],
            golden_prompts: [
              {
                prompt_id: 'gp-1',
                samples: 10,
                mean_output_tokens: 128.4,
                stddev_output_tokens: 6.2,
                min_output_tokens: 118,
                max_output_tokens: 140,
                cv: 0.048,
              },
            ],
            capacity_sweep: {
              test_id: 'test-sweep-1',
              mode: 'concurrency',
              knee: {
                level_index: 2,
                load: 16,
                ttft_p95_ms: 110,
                slope_before: 1.5,
                slope_after: 19.4,
                r_squared: 0.96,
              },
              levels: [
                {
                  level_index: 0,
                  name: 'Level 1 (16)',
                  offered_load: 16,
                  achieved_tps: 14.8,
                  requests: 220,
                  errors: 0,
                  error_pct: 0,
                  throttled_429: 0,
                  throttle_rate_pct: 0,
                  goodput_pct: 100,
                  good_output_tokens_per_s: 473.6,
                  output_tokens_per_s: 473.6,
                  input_tokens_per_s: 3788.8,
                  ttft_p50_ms: 85,
                  ttft_p95_ms: 110,
                  ttft_p99_ms: 140,
                  tpot_p50_ms: 18,
                  tpot_p95_ms: 22,
                  tpot_p99_ms: 28,
                  e2e_p50_ms: 660,
                  e2e_p95_ms: 810,
                  e2e_p99_ms: 950,
                  slo_compliant: true,
                  server_kv_cache_peak_pct: 94.5,
                  cost_per_million_output_tokens_usd: 2.35,
                },
              ],
            },
            latency_decomposition: {
              test_id: 'test-sweep-1',
              samples: 40,
              network_gateway_avg_ms: 18.5,
              server_queue_avg_ms: 24.0,
              prefill_avg_ms: 52.5,
              decode_avg_ms: 210.0,
              total_e2e_avg_ms: 305.0,
              source: 'response_headers',
              concurrency_estimate: {
                arrival_rate_per_s: 10.0,
                avg_service_seconds: 0.305,
                estimated_in_flight: 3.05,
                prefill_in_flight: 0.95,
                decode_in_flight: 2.1,
              },
            },
            comparison: {
              baseline_test_id: 'run-a',
              candidate_test_id: 'run-b',
              baseline_model: 'llama-3-8b-fp16',
              candidate_model: 'llama-3-8b-fp8',
              workload_digest_matched: true,
              output_length_drift_pct: 1.2,
              output_length_drift_flag: false,
              input_length_drift_pct: 0.4,
              input_length_drift_flag: false,
              metrics: [
                {
                  metric: 'ttft_p95_ms',
                  baseline: 180,
                  candidate: 115,
                  delta: -65,
                  delta_pct: -36.11,
                  ci95_low: -68.4,
                  ci95_high: -61.8,
                  significant: true,
                  direction: 'improved',
                },
              ],
            },
          }}
        />,
      )
    })

    expect(container.textContent).toContain('LLM inference diagnostics')
    expect(container.textContent).toContain('gpt-4o-mini')
    expect(container.textContent).toContain('chat_completion')
    expect(container.textContent).toContain('short')
    expect(container.textContent).toContain('gp-1')
    expect(container.textContent).toContain('Token counts include client-side ~4-byte estimates')
    expect(container.textContent).toContain('Capacity sweep analysis')
    expect(container.querySelector('svg[aria-label="Capacity sweep latency vs throughput chart"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="capacity-sweep-knee-line"]')).toBeTruthy()
    expect(container.textContent).toContain('End-to-end latency decomposition')
    expect(container.textContent).toContain('A/B model and engine comparison')
  })

  it('renders Go backend RunArchiveLLMSummary payload shape directly', async () => {
    await act(async () => {
      root.render(
        <RunReportLLMPanel
          report={{
            total_requests: 25,
            streamed_requests: 25,
            output_tokens_per_sec: 512.4,
            prompt_tokens: 5000,
            completion_tokens: 2500,
            cached_tokens: 1000,
            cache_hit_rate_pct: 20,
            truncated_requests: 1,
            throttled_requests: 2,
            ttft: { samples: 25, p50_ms: 90, p90_ms: 130, p95_ms: 150, p99_ms: 210 },
            tpot: { samples: 25, p50_ms: 15, p90_ms: 20, p95_ms: 24, p99_ms: 30 },
            e2e: { samples: 25, p50_ms: 480, p90_ms: 620, p95_ms: 710, p99_ms: 890 },
            by_length_band: [
              {
                band: 'medium',
                requests: 25,
                prompt_tokens: 5000,
                completion_tokens: 2500,
                ttft: { samples: 25, p50_ms: 90, p95_ms: 150, p99_ms: 210 },
                tpot: { samples: 25, p50_ms: 15, p95_ms: 24, p99_ms: 30 },
                e2e: { samples: 25, p50_ms: 480, p95_ms: 710, p99_ms: 890 },
              },
            ],
            golden_prompt_drift: {
              checked_prompts: 1,
              drifted_prompts: 1,
              status: 'drift_detected',
              items: [
                {
                  name: 'golden_check_1',
                  samples: 5,
                  min_completion_tokens: 40,
                  max_completion_tokens: 60,
                  length_drift_ratio: 0.5,
                  drifted: true,
                },
              ],
            },
            capacity_sweep: {
              test_id: 'test-go-sweep',
              mode: 'rate',
              levels: [
                {
                  stage_index: 1,
                  name: 'Stage 1',
                  offered_load: 10,
                  achieved_tps: 9.8,
                  requests: 98,
                  errors: 0,
                  throttle_rate_pct: 0,
                  goodput_pct: 99.0,
                  system_output_tokens_per_sec: 392.0,
                  ttft_p50_ms: 80,
                  ttft_p95_ms: 120,
                  ttft_p99_ms: 160,
                  tpot_p50_ms: 14,
                  tpot_p95_ms: 19,
                  tpot_p99_ms: 25,
                  meets_slo: true,
                },
              ],
            },
            latency_decomposition: {
              test_id: 'test-go-sweep',
              streamed_requests: 25,
              network_gateway_avg_ms: 12.0,
              server_avg_queue_ms: 18.0,
              server_avg_prefill_ms: 45.0,
              client_avg_decode_ms: 190.0,
              client_avg_e2e_ms: 265.0,
              source: 'response_headers',
            },
          }}
        />,
      )
    })

    expect(container.textContent).toContain('90.0 ms / 150.0 ms / 210.0 ms')
    expect(container.textContent).toContain('512.40')
    expect(container.textContent).toContain('medium')
    expect(container.textContent).toContain('golden_check_1')
    expect(container.textContent).toContain('392.0')
    expect(container.textContent).toContain('Within SLO')
    expect(container.textContent).toContain('265.0')
  })

  it('builds LLM charts only when hasLLMData is true', () => {
    expect(buildLLMCharts(false)).toEqual([])
    const charts = buildLLMCharts(true)
    expect(charts.map(c => c.id)).toEqual(['llm_ttft', 'llm_tpot', 'llm_tokens'])
  })
})

