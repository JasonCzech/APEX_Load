// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionScenarioPreview from './TestExecutionScenarioPreview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>

  return {
    ResponsiveContainer: Mock,
    AreaChart: Mock,
    Area: () => null,
    XAxis: () => null,
    YAxis: () => null,
    CartesianGrid: () => null,
    Tooltip: () => null,
    Legend: () => null,
    ReferenceArea: () => null,
  }
})

describe('TestExecutionScenarioPreview', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    consoleErrorSpy.mockRestore()
    vi.clearAllMocks()
  })

  it('renders the summary row without nested button warnings and keeps edit separate from expand', async () => {
    const onExpandedScenarioScriptChange = vi.fn()
    const onEditScript = vi.fn()

    function PreviewHarness() {
      const [expandedScenarioScript, setExpandedScenarioScript] = useState<string | null>(null)

      return (
        <TestExecutionScenarioPreview
          test={{
            id: 'test-1',
            name: 'Queued Scenario',
            status: 'QUEUED',
            scenario_mode: 'goal_oriented',
            total_vusers: 10,
            duration: '5m',
            start_delay: '30s',
            goal_config: {
              target_average_tps: 300,
              p95_latency_ms: 800,
              max_error_pct: 2.5,
              max_error_count: 25,
              min_vusers: 5,
              max_vusers: 25,
              stop_on_sla_breach: true,
              stop_on_max_errors: true,
              sla_breach_window: '45s',
            },
            runtime_settings: {
              pacing: { type: 'fixed_delay', delay_ms: 1200 },
              think_time: { type: 'fixed', mean_ms: 900 },
              error_handling: { continue_on_error: true, snapshot_on_error: true },
            },
            created_at: '2026-05-12T20:00:00Z',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              ramp_up: '30s',
              ramp_down: '15s',
              iterations: 3,
              initialization: 'at_runtime',
            }],
          } as never}
          startCapacityConflict={null}
          startFeedback=""
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          queuedScenarioPreview={{
            points: [{ second: 0, group0: 10, total: 10 }],
            totalVUsers: 10,
            totalSeconds: 300,
            steadySeconds: 300,
            startDelaySeconds: 30,
            preBaselineSeconds: 0,
            postBaselineSeconds: 0,
          }}
          queuedScenarioRows={[{
            group: {
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              ramp_up: '30s',
              ramp_down: '15s',
              iterations: 3,
              initialization: 'at_runtime',
            },
            detail: {
              id: 'script-1',
              name: 'Checkout Script',
              protocol: 'http',
              script: { actions: [] },
            },
            actionCount: 0,
            initCount: 0,
            teardownCount: 0,
            dataSourceCount: 0,
          }]}
          scriptDetailsLoading={false}
          scriptDetailsError=""
          expandedScenarioScript={expandedScenarioScript}
          onExpandedScenarioScriptChange={(value) => {
            onExpandedScenarioScriptChange(value)
            setExpandedScenarioScript(value)
          }}
          onEditScript={onEditScript}
          groupColors={['#0a84ff']}
        />
      )
    }

    await act(async () => {
      root.render(<PreviewHarness />)
    })

    const summaryRow = container.querySelector('.scenario-script-summary')
    const editButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('Edit Script'))

    expect(summaryRow?.getAttribute('role')).toBe('button')
    expect(editButton).toBeDefined()
    expect(container.textContent).toContain('Start Delay')
    expect(container.textContent).toContain('30s')
    expect(container.textContent).toContain('Target Avg TPS')
    expect(container.textContent).toContain('P95 Latency Ceiling')
    expect(container.textContent).toContain('800 ms')
    expect(container.textContent).toContain('Max Error %')
    expect(container.textContent).toContain('2.5%')
    expect(container.textContent).toContain('Max Error Count')
    expect(container.textContent).toContain('25')
    expect(container.textContent).toContain('VUser Guardrails')
    expect(container.textContent).toContain('5 - 25')
    expect(container.textContent).toContain('SLA Breach Window')
    expect(container.textContent).toContain('45s')
    expect(container.textContent).toContain('Stop On SLA Breach')
    expect(container.textContent).toContain('Stop On Max Errors')
    expect(container.textContent).toContain('Pacing')
    expect(container.textContent).toContain('Fixed delay · 1200ms')
    expect(container.textContent).toContain('Think Time')
    expect(container.textContent).toContain('fixed · 900ms')
    expect(container.textContent).toContain('Continue On Error')
    expect(container.textContent).toContain('Enabled')

    const nestingWarning = consoleErrorSpy.mock.calls.some(([message]) =>
      typeof message === 'string' && message.includes('validateDOMNesting'),
    )

    expect(nestingWarning).toBe(false)

    if (!summaryRow || !editButton) {
      return
    }

    await act(async () => {
      summaryRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onExpandedScenarioScriptChange).toHaveBeenCalledWith('Checkout')
    expect(container.textContent).toContain('3 (group override)')

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEditScript).toHaveBeenCalledWith('script-1')
    expect(onExpandedScenarioScriptChange).toHaveBeenCalledTimes(1)
  })

  it('shows pending queue metadata for queued starts waiting on capacity', async () => {
    await act(async () => {
      root.render(
        <TestExecutionScenarioPreview
          test={{
            id: 'test-pending',
            name: 'Pending Scenario',
            status: 'PENDING',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            pending_since: '2026-05-12T20:01:00Z',
            queue_position: 2,
            pending_reason: 'waiting for project test slot (1 max concurrent tests)',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            }],
          } as never}
          startCapacityConflict={null}
          startFeedback=""
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          queuedScenarioPreview={{
            points: [],
            totalVUsers: 10,
            totalSeconds: 300,
            steadySeconds: 300,
            startDelaySeconds: 0,
            preBaselineSeconds: 0,
            postBaselineSeconds: 0,
          }}
          queuedScenarioRows={[]}
          scriptDetailsLoading={false}
          scriptDetailsError=""
          expandedScenarioScript={null}
          onExpandedScenarioScriptChange={vi.fn()}
          onEditScript={vi.fn()}
          groupColors={['#0a84ff']}
        />,
      )
    })

    expect(container.textContent).toContain('Queued for project capacity')
    expect(container.textContent).toContain('Queue position #2')
    expect(container.textContent).toContain('waiting for project test slot')
    expect(container.textContent).toContain('Waiting since')
  })
})