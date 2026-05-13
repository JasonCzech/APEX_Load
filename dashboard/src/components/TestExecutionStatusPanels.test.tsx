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
            breach_active: false,
            stop_triggered: false,
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
  })
})