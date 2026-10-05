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
            server_processing_breached: true,
            transfer_breached: false,
            lcp_breached: true,
            partition_skew_breached: false,
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
              max_avg_server_processing_ms: 450,
              max_avg_transfer_ms: 120,
              max_error_count: 25,
              max_lcp_ms: 2500,
              max_partition_skew_pct: 5,
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
    expect(container.textContent).toContain('Server Processing Ceiling')
    expect(container.textContent).toContain('450 ms')
    expect(container.textContent).toContain('Transfer Ceiling')
    expect(container.textContent).toContain('120 ms')
    expect(container.textContent).toContain('Max Error Count')
    expect(container.textContent).toContain('25')
    expect(container.textContent).toContain('Breach Duration')
    expect(container.textContent).toContain('45s')
    expect(container.textContent).toContain('Stop Reason')
    expect(container.textContent).toContain('sla breach')
    expect(container.textContent).toContain('Server processing breached')
    expect(container.textContent).toContain('Transfer healthy')
    expect(container.textContent).toContain('LCP breached')
    expect(container.textContent).toContain('Partition skew healthy')
    expect(container.textContent).not.toContain('Dead-letter rate')
  })

  it('renders telemetry ages and separate observed websocket drop counts when available', async () => {
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

    expect(container.textContent).toContain('Telemetry observations')
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
    expect(container.textContent).toContain('Retained history points')
    expect(container.textContent).toContain('75')
    expect(container.textContent).toContain('Observed WS clients')
    expect(container.textContent).toContain('3')
    expect(container.textContent).toContain('Observed WS message drops')
    expect(container.textContent).toContain('Observed drop evidence')
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
                device_platform: 'ios',
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
    expect(container.querySelector('.browser-metrics-card .goal-status-badge')?.classList.contains('healthy')).toBe(false)
    expect(container.textContent).toContain('Unscored')
    expect(container.textContent).toContain('5/7 required fields available')
    expect(container.textContent).toContain('not a standards-qualified Core Web Vitals score')
    expect(container.textContent).toContain('Avg FCP')
    expect(container.textContent).toContain('120.0 ms')
    expect(container.textContent).toContain('Avg CLS')
    expect(container.textContent).toContain('0.034')
    expect(container.textContent).toContain('iphone_se')
    expect(container.textContent).toContain('iOS · WebKit · low tier')
    const deviceTierTexts = Array.from(container.querySelectorAll('.browser-device-tier')).map(node => node.textContent)
    expect(deviceTierTexts).toEqual(['iOS · WebKit · low tier'])
    expect(container.textContent).toContain('FCP 130.0 ms')
  })

  it('renders throughput tracking and group target miss details when throughput targets are available', async () => {
    await act(async () => {
      root.render(
        <TestExecutionStatusPanels
          isGoalOriented={false}
          goalDescription=""
          test={{
            id: 'test-1',
            name: 'Throughput Run',
            status: 'RUNNING',
            total_vusers: 8,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [],
          } as never}
          metrics={{
            tps: 180,
            target_tps: 250,
            throughput_gap_pct: 28,
            throughput_target_missed: true,
            active_vusers: 8,
          }}
          isMultiGroup={true}
          groupMetrics={[
            {
              name: 'buyers',
              vusers: 4,
              transactions: 25,
              errors: 1,
              tps: 90,
              target_tps: 120,
              throughput_gap_pct: 25,
              throughput_target_missed: true,
              avg_response_ms: 220,
              p50_ms: 180,
              p90_ms: 310,
              p99_ms: 480,
            },
          ]}
          groupColors={['#f59e0b']}
        />,
      )
    })

    expect(container.textContent).toContain('Throughput Tracking')
    expect(container.textContent).toContain('Configured arrival target versus delivered live throughput for this run.')
    expect(container.textContent).toContain('Target TPS')
    expect(container.textContent).toContain('250.0')
    expect(container.textContent).toContain('Actual TPS')
    expect(container.textContent).toContain('180.0')
    expect(container.textContent).toContain('Gap')
    expect(container.textContent).toContain('28.0%')
    expect(container.textContent).toContain('Behind Target')
    expect(container.textContent).toContain('Target 120.0 TPS')
    expect(container.textContent).toContain('Gap 25.0%')
  })

  it('renders live capacity allocation, saturation, queue, and placement signals', async () => {
    await act(async () => {
      root.render(
        <TestExecutionStatusPanels
          isGoalOriented={false}
          goalDescription=""
          test={{
            id: 'test-1',
            name: 'Capacity Run',
            status: 'PENDING',
            total_vusers: 1200,
            duration: '15m',
            created_at: '2026-05-12T20:00:00Z',
            assignment: {
              mode: 'region_weighted',
              region_weights: { east: 2, west: 1 },
            },
            groups: [
              { name: 'buyers', script_id: 'script-1', script_name: 'HTTP', protocol: 'http', vusers: 900 },
              { name: 'canaries', script_id: 'script-2', script_name: 'Browser', protocol: 'browser', vusers: 300 },
            ],
            capacity_advisory: {
              total_vusers: 1200,
              duration_seconds: 900,
              baseline_window_enabled: false,
              workload_class: 'mixed',
              headroom_pct: 20,
              complexity_score: 1.4,
              bottleneck: 'vusers',
              recommended_generators: 4,
              available_generators: 3,
              shortfall_generators: 1,
              requires_override: true,
              estimated_per_vuser: { cpu_cores: 0.001, memory_mb: 0.5, network_mbps: 0.01 },
              estimated_total: { cpu_cores: 1.2, memory_mb: 600, network_mbps: 12 },
              usable_per_generator: { cpu_cores: 3.2, memory_mb: 6400, network_mbps: 800 },
              dimension_recommendations: { cpu: 1, memory: 1, network: 1, vusers: 4 },
            },
          } as never}
          metrics={{ active_vusers: 0 }}
          isMultiGroup={false}
          groupMetrics={[]}
          groupColors={[]}
        />,
      )
    })

    expect(container.textContent).toContain('Live Capacity')
    expect(container.textContent).toContain('Rejected Demand')
    expect(container.textContent).toContain('Allocated VUsers')
    expect(container.textContent).toContain('Available Generators')
    expect(container.textContent).toContain('Recommended')
    expect(container.textContent).toContain('Saturated')
    expect(container.textContent).toContain('Queued Demand')
    expect(container.textContent).toContain('Rejected Demand')
    expect(container.textContent).toContain('http: 900 VUsers')
    expect(container.textContent).toContain('browser: 300 VUsers')
    expect(container.textContent).toContain('east: 800 VUsers')
    expect(container.textContent).toContain('west: 400 VUsers')
  })

  it('prefers live capacity status breakdowns when metrics provide them', async () => {
    await act(async () => {
      root.render(
        <TestExecutionStatusPanels
          isGoalOriented={false}
          goalDescription=""
          test={{
            id: 'test-1',
            name: 'Live Capacity Run',
            status: 'RUNNING',
            total_vusers: 900,
            duration: '15m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [],
            capacity_advisory: {
              total_vusers: 900,
              duration_seconds: 900,
              baseline_window_enabled: false,
              headroom_pct: 20,
              complexity_score: 1,
              bottleneck: 'vusers',
              recommended_generators: 2,
              available_generators: 4,
              estimated_per_vuser: { cpu_cores: 0.001, memory_mb: 0.5, network_mbps: 0.01 },
              estimated_total: { cpu_cores: 0.9, memory_mb: 450, network_mbps: 9 },
              usable_per_generator: { cpu_cores: 3.2, memory_mb: 6400, network_mbps: 800 },
              dimension_recommendations: { cpu: 1, memory: 1, network: 1, vusers: 2 },
            },
          } as never}
          metrics={{
            active_vusers: 900,
            capacity_status: {
              allocated_vusers: 900,
              available_generators: 4,
              recommended_generators: 2,
              saturated: false,
              queued_vusers: 300,
              rejected_vusers: 100,
              by_protocol: {
                http: { allocated_vusers: 900, queued_vusers: 200 },
                browser: { rejected_vusers: 100 },
              },
              by_region: {
                east: { allocated_vusers: 600, queued_vusers: 200 },
                west: { allocated_vusers: 300, rejected_vusers: 100 },
              },
            },
          }}
          isMultiGroup={false}
          groupMetrics={[]}
          groupColors={[]}
        />,
      )
    })

    expect(container.textContent).toContain('Queued Demand')
    expect(container.textContent).toContain('300')
    expect(container.textContent).toContain('Rejected Demand')
    expect(container.textContent).toContain('100')
    expect(container.textContent).toContain('http: 1,100 VUsers')
    expect(container.textContent).toContain('browser: 100 VUsers')
    expect(container.textContent).toContain('east: 800 VUsers')
    expect(container.textContent).toContain('west: 400 VUsers')
  })

  it('renders geographic activity from the live regional transaction aggregation', async () => {
    await act(async () => {
      root.render(
        <TestExecutionStatusPanels
          isGoalOriented={false}
          goalDescription=""
          test={{ id: 'test-1', name: 'Regional', status: 'RUNNING', total_vusers: 10, duration: '1m', created_at: '2026-08-25T00:00:00Z', groups: [] } as never}
          metrics={{
            by_region: {
              'us-east': { name: 'us-east', count: 120, errors: 2, avg_ms: 100, p50_ms: 90, p90_ms: 130, p95_ms: 150, p99_ms: 180 },
              'eu-west': { name: 'eu-west', count: 80, errors: 0, avg_ms: 120, p50_ms: 100, p90_ms: 160, p95_ms: 175, p99_ms: 210 },
            },
          }}
          isMultiGroup={false}
          groupMetrics={[]}
          groupColors={[]}
        />,
      )
    })
    expect(container.querySelector('[data-testid="live-regional-activity"]')).toBeTruthy()
    expect(container.textContent).toContain('us-east')
    expect(container.textContent).toContain('120 tx · 2 errors · 150.0 ms p95')
    expect(container.querySelector('[aria-label="eu-west activity 80 transactions"]')).toBeTruthy()
  })

  it.each([null, 0, false, 'bad', []])('renders malformed live device %j as unavailable without losing its source key', async raw => {
    await act(async () => root.render(<TestExecutionStatusPanels
      isGoalOriented={false} goalDescription="" test={{ id: 'malformed-run', groups: [], total_vusers: 1 } as never}
      metrics={{ browser: { samples: 1 }, browser_by_device: { invalid_device: raw as never } }}
      isMultiGroup={false} groupMetrics={[]} groupColors={[]}
    />))
    expect(container.querySelectorAll('.browser-device-card')).toHaveLength(1)
    expect(container.textContent).toContain('invalid_device')
    expect(container.textContent).toContain('Malformed summary: expected an object')
    expect(container.textContent).toContain('1 device summary unranked')
    expect(container.textContent).not.toContain('Weakest complete summary:')
    expect(container.querySelector('.browser-device-stats')?.textContent).toContain('LCP n/a')
    expect(container.querySelector('.browser-metrics-card .goal-status-badge')?.classList.contains('healthy')).toBe(false)
  })

  it('renders object-valued live browser labels through the scalar guards', async () => {
    await act(async () => root.render(<TestExecutionStatusPanels
      isGoalOriented={false} goalDescription="" test={{ id: 'malformed-labels', groups: [], total_vusers: 1 } as never}
      metrics={{ browser: { samples: 1 }, browser_by_device: { native: { samples: 1, browser_engine: {}, device_platform: {}, device_tier: {} } as never } }}
      isMultiGroup={false} groupMetrics={[]} groupColors={[]}
    />))
    expect(container.querySelector('.browser-device-tier')?.textContent).toBe('unknown · browser · unknown tier')
    expect(container.textContent).not.toContain('[object Object]')
    expect(container.textContent).toContain('Unscored')
  })
  it.each([
    ['missing observations', {}, 'Unknown', 'unknown'],
    ['observed zero age', { sample_age_ms: 0 }, 'Age observed', '0 ms'],
    ['observed old age', { sample_age_ms: 9999 }, 'Age observed', '10.00 s'],
    ['NaN is unknown', { sample_age_ms: NaN }, 'Unknown', 'unknown'],
    ['negative age is unknown', { sample_age_ms: -1 }, 'Unknown', 'unknown'],
    ['negative drops are unknown', { websocket_dropped_messages: -1 }, 'Unknown', 'unknown'],
    ['observed drop without age', { websocket_dropped_events: 2 }, 'Observed drop evidence', 'unknown'],
  ])('does not derive health from %s', async (_, telemetry, badge, age) => {
    await act(async () => root.render(<TestExecutionStatusPanels isGoalOriented={false} goalDescription="" test={{ id: 'run', status: 'RUNNING', groups: [], total_vusers: 1 } as never} metrics={{ telemetry }} isMultiGroup={false} groupMetrics={[]} groupColors={[]} />))
    const card = container.querySelector('.telemetry-status-card')
    expect(card?.querySelector('.goal-status-badge')?.textContent).toBe(badge)
    expect(card?.querySelector('.goal-status-badge')?.className).toContain('neutral')
    const sampleAge = Array.from(card?.querySelectorAll('.goal-status-metric') ?? []).find(node => node.textContent?.startsWith('Sample Age'))
    expect(sampleAge?.textContent).toContain(age)
    expect(card?.textContent).not.toContain('Healthy')
    expect(card?.textContent).not.toContain('Stale')
    expect(card?.textContent).toContain('not an exact run loss total')
    expect(card?.textContent).toContain('do not establish synchronized clocks')
  })

  it('renders an independent incomplete document population without legacy browser Samples', async () => {
    await act(async () => root.render(<TestExecutionStatusPanels
      isGoalOriented={false} goalDescription="" test={{ id: 'document-run', groups: [], total_vusers: 1 } as never}
      metrics={{ browser_documents: { version: 2, metrics: [], incomplete: true, dropped_observations: 0 } }}
      isMultiGroup={false} groupMetrics={[]} groupColors={[]}
    />))
    const panel = container.querySelector('[aria-label="Browser document evidence"]')!
    expect(panel.textContent).toContain('Live intervals retain first-seen provisional history')
    expect(panel.textContent).toContain('Document evidence is incomplete. 0 observations were dropped')
    expect(panel.textContent).toContain('No document metric tuples were recorded')
    expect(container.querySelector('.browser-metrics-card')).toBeNull()
  })

  it('shows requested browser evidence as unavailable while old peers provide no document contract', async () => {
    await act(async () => root.render(<TestExecutionStatusPanels
      isGoalOriented={false} goalDescription="" test={{ id: 'old-peer-run', groups: [{ protocol: 'browser' }], total_vusers: 1 } as never}
      metrics={{}} isMultiGroup={false} groupMetrics={[]} groupColors={[]}
    />))
    expect(container.querySelector('[aria-label="Browser document evidence"]')?.textContent).toContain('Document summary unavailable')
    expect(container.textContent).not.toContain('0 eligible final document measurements')
  })

})
