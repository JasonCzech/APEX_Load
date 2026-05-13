// @vitest-environment jsdom

import { act } from 'react'
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

    await act(async () => {
      root.render(
        <TestExecutionScenarioPreview
          test={{
            id: 'test-1',
            name: 'Queued Scenario',
            status: 'QUEUED',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              ramp_up: '30s',
              ramp_down: '15s',
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
          expandedScenarioScript={null}
          onExpandedScenarioScriptChange={onExpandedScenarioScriptChange}
          onEditScript={onEditScript}
          groupColors={['#0a84ff']}
        />,
      )
    })

    const summaryRow = container.querySelector('.scenario-script-summary')
    const editButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('Edit Script'))

    expect(summaryRow?.getAttribute('role')).toBe('button')
    expect(editButton).toBeDefined()

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

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEditScript).toHaveBeenCalledWith('script-1')
    expect(onExpandedScenarioScriptChange).toHaveBeenCalledTimes(1)
  })
})