// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionStatusPanels from './TestExecutionStatusPanels'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TestExecutionStatusPanels', () => {
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
    vi.clearAllMocks()
  })

  it('renders live goal guardrail thresholds for goal-oriented runs', async () => {
    await act(async () => {
      root.render(
        <TestExecutionStatusPanels
          isGoalOriented={true}
          goalDescription="Target average TPS with SLA guardrails"
          goalStatus={{
            mode: 'goal_oriented',
            current_tps: 142.5,
            current_total_vusers: 35,
            target_average_tps: 150,
            suggested_total_vusers: 36,
            max_error_count: 25,
            breach_active: true,
            breach_duration_sec: 45,
            stop_triggered: true,
            stop_reason: 'sla_breach',
          }}
          test={{
            id: 'test-1',
            name: 'Goal Run',
            status: 'RUNNING',
            scenario_mode: 'goal_oriented',
            total_vusers: 40,
            duration: '10m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [],
            goal_config: {
              target_average_tps: 150,
              p95_latency_ms: 800,
              max_error_pct: 2.5,
              max_error_count: 25,
              min_vusers: 20,
              max_vusers: 50,
              stop_on_sla_breach: true,
              stop_on_max_errors: true,
              sla_breach_window: '45s',
            },
          } as never}
          metrics={{ tps: 142.5, active_vusers: 35 }}
          isMultiGroup={false}
          groupMetrics={[]}
          groupColors={[]}
        />,
      )
    })

    expect(container.textContent).toContain('P95 Latency Ceiling')
    expect(container.textContent).toContain('800 ms')
    expect(container.textContent).toContain('Max Error %')
    expect(container.textContent).toContain('2.5%')
    expect(container.textContent).toContain('Max Error Count')
    expect(container.textContent).toContain('25')
    expect(container.textContent).toContain('Breach Duration')
    expect(container.textContent).toContain('45s')
    expect(container.textContent).toContain('Stop Reason')
    expect(container.textContent).toContain('sla breach')
  })

  it('renders telemetry freshness and websocket health details when available', async () => {
    await act(async () => {
      root.render(
        <TestExecutionStatusPanels
          isGoalOriented={false}
          goalDescription=""
          test={{
            id: 'test-1',
            name: 'Telemetry Run',
            status: 'RUNNING',
            total_vusers: 12,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [],
          } as never}
          metrics={{
            tps: 40,
            active_vusers: 12,
            telemetry: {
              sample_age_ms: 1250,
              generator_processing_lag_ms: 600,
              transport_lag_ms: 300,
              controller_fanout_lag_ms: 350,
              collected_at: '2026-05-12T20:05:00Z',
              generator_published_at: '2026-05-12T20:05:00.600Z',
              controller_received_at: '2026-05-12T20:05:00.900Z',
              published_at: '2026-05-12T20:05:00.900Z',
              client_received_at: '2026-05-12T20:05:01.250Z',
              delivery_lag_ms: 350,
              end_to_end_age_ms: 1250,
              history_points: 75,
              websocket_clients: 3,
              websocket_dropped_messages: 2,
              websocket_dropped_events: 1,
            },
          }}
          isMultiGroup={false}
          groupMetrics={[]}
          groupColors={[]}
        />,
      )
    })

    expect(container.textContent).toContain('Telemetry Health')
    expect(container.textContent).toContain('Sample Age')
    expect(container.textContent).toContain('1250 ms')
    expect(container.textContent).toContain('Generator Lag')
    expect(container.textContent).toContain('600 ms')
    expect(container.textContent).toContain('Transport Lag')
    expect(container.textContent).toContain('300 ms')
    expect(container.textContent).toContain('Fan-out Lag')
    expect(container.textContent).toContain('Delivery Lag')
    expect(container.textContent).toContain('350 ms')
    expect(container.textContent).toContain('End-to-End Age')
    expect(container.textContent).toContain('Generator Publish')
    expect(container.textContent).toContain('Controller Receive')
    expect(container.textContent).toContain('History Window')
    expect(container.textContent).toContain('75')
    expect(container.textContent).toContain('WS Clients')
    expect(container.textContent).toContain('3')
    expect(container.textContent).toContain('WS Drops')
    expect(container.textContent).toContain('Degraded')
  })

  it('renders browser UX signals and per-device summaries when available', async () => {
    await act(async () => {
      root.render(
        <TestExecutionStatusPanels
          isGoalOriented={false}
          goalDescription=""
          test={{
            id: 'test-1',
            name: 'Browser Run',
            status: 'RUNNING',
            total_vusers: 4,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [],
          } as never}
          metrics={{
            browser: {
              samples: 3,
              avg_fcp_ms: 120,
              avg_lcp_ms: 240,
              avg_cls_score: 0.034,
              avg_inp_ms: 52,
              avg_long_task_count: 1.3,
              avg_blocking_resources: 2,
              avg_js_heap_used_mb: 18,
            },
            browser_by_device: {
              iphone_se: {
                device_profile: 'iphone_se',
                device_tier: 'low',
                browser_engine: 'webkit',
                samples: 2,
                avg_fcp_ms: 130,
                avg_lcp_ms: 260,
                avg_cls_score: 0.041,
                avg_inp_ms: 58,
              },
            },
          }}
          isMultiGroup={false}
          groupMetrics={[]}
          groupColors={[]}
        />,
      )
    })

    expect(container.textContent).toContain('Browser UX Signals')
    expect(container.textContent).toContain('Avg FCP')
    expect(container.textContent).toContain('120.0 ms')
    expect(container.textContent).toContain('Avg CLS')
    expect(container.textContent).toContain('0.034')
    expect(container.textContent).toContain('iphone_se')
    expect(container.textContent).toContain('WebKit · low tier')
    expect(container.textContent).toContain('FCP 130.0 ms')
  })
})