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
  const labelText = (label?: string | { value?: string }) => (typeof label === 'string' ? label : label?.value ?? '')

  return {
    Area: Mock,
    AreaChart: MockChart,
    CartesianGrid: Mock,
    Legend: Mock,
    Line: Mock,
    ReferenceArea: Mock,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    Tooltip: Mock,
    XAxis: ({ label }: { label?: string | { value?: string } }) => <g data-testid="mock-x-axis" data-label={labelText(label)} />,
    YAxis: ({ label }: { label?: string | { value?: string } }) => <g data-testid="mock-y-axis" data-label={labelText(label)} />,
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
    expect(container.querySelector('#live-controls-panel .run-actions-card')).toBeNull()
    expect(container.querySelector('.run-actions-drawer')).toBeNull()
    expect(toolbarButtonByText('Pause')).not.toBeUndefined()
    expect(toolbarButtonByText('Stop VUsers')).not.toBeUndefined()
    expect(toolbarButtonByText('Stop Test')).not.toBeUndefined()
    expect(toolbarButtonByText('Abort')).not.toBeUndefined()
    expect(container.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('Elapsed time')
    expect(container.querySelector('[data-testid="mock-y-axis"]')?.getAttribute('data-label')).toBe('VUsers')

    await act(async () => {
      setSelectValue(actionSelect!, 'stop')
    })

    expect(onVuserGroupActionChange).toHaveBeenCalledWith('Checkout', 'stop')
  })

  it('keeps run actions available when live controls are collapsed', async () => {
    const onAbort = vi.fn()
    const onPause = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionLiveControls
          showControls={false}
          onToggleControls={vi.fn()}
          test={buildRunningTest()}
          vuserTargets={{ Checkout: 12 }}
          onVuserTargetChange={vi.fn()}
          vuserGroupActions={{ Checkout: 'run' }}
          onVuserGroupActionChange={vi.fn()}
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
          onPause={onPause}
          onResume={vi.fn()}
          onStop={vi.fn()}
          onAbort={onAbort}
          liveControlPreview={{
            points: [],
            currentTotal: 12,
            projectedTotal: 12,
            remainingSeconds: 600,
            projectedDurationSeconds: 600,
            vuserTransitionSeconds: 0,
            isGradualVuserChange: false,
            hasActiveVuserRamp: false,
            hasPendingChanges: false,
          }}
          controlFeedback=""
          groupColors={['#00a3ff']}
        />,
      )
    })

    expect(container.querySelector('#live-controls-panel')).toBeNull()
    expect(container.querySelector('.run-actions-inline')?.textContent).toContain('Active')
    expect(toolbarButtonByText('Pause')).not.toBeUndefined()
    expect(toolbarButtonByText('Stop VUsers')).not.toBeUndefined()
    expect(toolbarButtonByText('Stop Test')).not.toBeUndefined()
    expect(toolbarButtonByText('Abort')).not.toBeUndefined()
    expect(document.body.querySelector('.run-actions-drawer')).toBeNull()

    await act(async () => {
      toolbarButtonByText('Pause')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onPause).toHaveBeenCalledTimes(1)

    await act(async () => {
      toolbarButtonByText('Abort')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onAbort).toHaveBeenCalledTimes(1)
  })

  function toolbarButtonByText(label: string) {
    return Array.from(container.querySelectorAll('.run-actions-inline button')).find(button => button.textContent === label)
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
