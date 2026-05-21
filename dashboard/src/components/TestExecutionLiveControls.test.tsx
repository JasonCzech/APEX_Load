// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestSummary } from '../types'
import TestExecutionLiveControls from './TestExecutionLiveControls'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children }: { children?: ReactNode }) => (
    <svg data-testid="mock-preview-chart" width="640" height="220">
      {children}
    </svg>
  )

  return {
    Area: Mock,
    AreaChart: MockChart,
    CartesianGrid: Mock,
    Legend: Mock,
    Line: Mock,
    ReferenceArea: Mock,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    Tooltip: Mock,
    XAxis: Mock,
    YAxis: Mock,
  }
})

describe('TestExecutionLiveControls', () => {
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

  it('renders per-group VUser action choices and disables the target controls for pause', async () => {
    const onVuserGroupActionChange = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionLiveControls
          showControls
          onToggleControls={vi.fn()}
          test={buildRunningTest()}
          vuserTargets={{ Checkout: 12 }}
          onVuserTargetChange={vi.fn()}
          vuserGroupActions={{ Checkout: 'pause' }}
          onVuserGroupActionChange={onVuserGroupActionChange}
          vuserRampEnabled={false}
          onVuserRampEnabledChange={vi.fn()}
          vuserRampDuration="1m"
          onVuserRampDurationChange={vi.fn()}
          onScaleVUsers={vi.fn()}
          onStopAllVUsers={vi.fn()}
          durationInput="10m"
          onDurationInputChange={vi.fn()}
          onAdjustDuration={vi.fn()}
          isRunning
          isPaused={false}
          isStopping={false}
          onPause={vi.fn()}
          onResume={vi.fn()}
          onStop={vi.fn()}
          onAbort={vi.fn()}
          liveControlPreview={{
            points: [],
            currentTotal: 12,
            projectedTotal: 0,
            remainingSeconds: 600,
            projectedDurationSeconds: 600,
            vuserTransitionSeconds: 30,
            isGradualVuserChange: false,
            hasActiveVuserRamp: false,
            hasPendingChanges: true,
          }}
          controlFeedback=""
          groupColors={['#00a3ff']}
        />,
      )
    })

    const actionSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Checkout VUser action"]')
    expect(actionSelect).not.toBeNull()
    expect(Array.from(actionSelect?.options ?? []).map(option => option.value)).toEqual(['run', 'pause', 'stop'])
    expect(actionSelect?.value).toBe('pause')
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Checkout VUser target"]')?.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Checkout VUser target value"]')?.disabled).toBe(true)
    expect(buttonByText('Pause')).not.toBeUndefined()

    await act(async () => {
      setSelectValue(actionSelect!, 'stop')
    })

    expect(onVuserGroupActionChange).toHaveBeenCalledWith('Checkout', 'stop')
  })

  function buttonByText(label: string) {
    return Array.from(container.querySelectorAll('button')).find(button => button.textContent === label)
  }
})

function setSelectValue(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function buildRunningTest(): TestSummary {
  return {
    id: 'test-1',
    name: 'Checkout Journey',
    status: 'RUNNING',
    total_vusers: 12,
    duration: '10m',
    created_at: '2026-05-12T20:00:00Z',
    started_at: '2026-05-12T20:00:00Z',
    groups: [
      {
        name: 'Checkout',
        script_id: 'script-1',
        script_name: 'Checkout Script',
        protocol: 'http',
        vusers: 12,
      },
    ],
  }
}
