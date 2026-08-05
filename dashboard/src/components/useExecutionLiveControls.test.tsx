// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestSummary } from '../types'
import { useExecutionLiveControls } from './useExecutionLiveControls'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  scaleVUsers: vi.fn(),
  adjustDuration: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

const showControlFeedback = vi.fn()

function LiveControlsHarness({ test }: { test: TestSummary | null }) {
  const model = useExecutionLiveControls('test-1', test, showControlFeedback)

  return (
    <div>
      <span data-testid="show-controls">{model.showControls ? 'open' : 'closed'}</span>
      <span data-testid="duration">{model.durationInput || 'none'}</span>
      <span data-testid="target">{String(model.vuserTargets.Checkout ?? -1)}</span>
      <span data-testid="action">{model.vuserGroupActions.Checkout ?? 'missing'}</span>
      <span data-testid="ramp-enabled">{model.vuserRampEnabled ? 'on' : 'off'}</span>
      <span data-testid="ramp-duration">{model.vuserRampDuration}</span>
      <span data-testid="projected-total">{String(model.liveControlPreview.projectedTotal)}</span>
      <span data-testid="transition-seconds">{String(model.liveControlPreview.vuserTransitionSeconds)}</span>
      <span data-testid="has-active-ramp">{model.liveControlPreview.hasActiveVuserRamp ? 'yes' : 'no'}</span>
      <button data-testid="toggle" onClick={model.onToggleControls}>toggle</button>
      <button data-testid="set-target" onClick={() => model.onVuserTargetChange('Checkout', 42)}>target</button>
      <button data-testid="set-pause" onClick={() => model.onVuserGroupActionChange('Checkout', 'pause')}>pause</button>
      <button data-testid="toggle-ramp" onClick={() => model.onVuserRampEnabledChange(true)}>ramp</button>
      <button data-testid="set-ramp-duration" onClick={() => model.onVuserRampDurationChange('2m')}>ramp duration</button>
      <button data-testid="set-duration" onClick={() => model.onDurationInputChange('15m')}>duration</button>
      <button data-testid="scale" onClick={() => void model.onScaleVUsers('Checkout')}>scale</button>
      <button data-testid="pause-all" onClick={() => void model.onPauseAllVUsers()}>pause all</button>
      <button data-testid="adjust" onClick={() => void model.onAdjustDuration()}>adjust</button>
    </div>
  )
}

describe('useExecutionLiveControls', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
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
    showControlFeedback.mockReset()
    vi.useRealTimers()
  })

  it('initializes control inputs from a live test and toggles visibility', async () => {
    await act(async () => {
      root.render(<LiveControlsHarness test={buildRunningTest()} />)
    })

    expect(readTestId('show-controls')).toBe('open')
    expect(readTestId('duration')).toBe('10m')
    expect(readTestId('target')).toBe('12')
    expect(readTestId('action')).toBe('run')
    expect(readTestId('ramp-enabled')).toBe('off')
    expect(readTestId('ramp-duration')).toBe('1m')
    expect(readTestId('projected-total')).toBe('12')

    await click('toggle')

    expect(readTestId('show-controls')).toBe('closed')
  })

  it('keeps controls collapsed for non-live run states', async () => {
    await act(async () => {
      root.render(<LiveControlsHarness test={{ ...buildRunningTest(), status: 'QUEUED' }} />)
    })

    expect(readTestId('show-controls')).toBe('closed')
  })

  it('applies edited VUser and duration actions through the api bridge', async () => {
    apiMock.scaleVUsers.mockResolvedValue(undefined)
    apiMock.adjustDuration.mockResolvedValue(undefined)

    await act(async () => {
      root.render(<LiveControlsHarness test={buildRunningTest()} />)
    })

    await click('set-target')
    expect(readTestId('target')).toBe('42')
    expect(readTestId('projected-total')).toBe('42')

    await click('scale')

    expect(apiMock.scaleVUsers).toHaveBeenCalledWith('test-1', 'Checkout', 42, { action: 'run' })
    expect(showControlFeedback).toHaveBeenCalledWith('Scaled Checkout to 42 VUsers')

    await click('set-duration')
    expect(readTestId('duration')).toBe('15m')

    await click('adjust')

    expect(apiMock.adjustDuration).toHaveBeenCalledWith('test-1', '15m')
    expect(showControlFeedback).toHaveBeenCalledWith('Duration adjusted to 15m')
  })

  it('preserves an in-progress duration edit across polling updates', async () => {
    const test = buildRunningTest()
    await act(async () => {
      root.render(<LiveControlsHarness test={test} />)
    })

    await click('set-duration')
    expect(readTestId('duration')).toBe('15m')

    await act(async () => {
      root.render(<LiveControlsHarness test={{ ...test, groups: test.groups.map(group => ({ ...group })) }} />)
    })
    expect(readTestId('duration')).toBe('15m')

    await act(async () => {
      root.render(<LiveControlsHarness test={{ ...test, id: 'test-2', duration: '20m' }} />)
    })
    expect(readTestId('duration')).toBe('20m')
  })

  it('caps observer live scaling at 10 while load groups retain larger targets', async () => {
    apiMock.scaleVUsers.mockResolvedValue(undefined)
    await act(async () => {
      root.render(<LiveControlsHarness test={{
        ...buildRunningTest(),
        total_vusers: 10,
        groups: [{ ...buildRunningTest().groups[0], role: 'observer', protocol: 'browser', vusers: 10 }],
      }} />)
    })

    await click('set-target')
    expect(readTestId('target')).toBe('10')
    expect(readTestId('projected-total')).toBe('10')

    await click('scale')
    expect(apiMock.scaleVUsers).toHaveBeenCalledWith('test-1', 'Checkout', 10, { action: 'run' })
  })

  it('passes gradual VUser ramp options through the api bridge', async () => {
    apiMock.scaleVUsers.mockResolvedValue(undefined)

    await act(async () => {
      root.render(<LiveControlsHarness test={buildRunningTest()} />)
    })

    await click('set-target')
    await click('toggle-ramp')
    await click('set-ramp-duration')

    expect(readTestId('ramp-enabled')).toBe('on')
    expect(readTestId('ramp-duration')).toBe('2m')
    expect(readTestId('transition-seconds')).toBe('120')

    await click('scale')

    expect(apiMock.scaleVUsers).toHaveBeenCalledWith('test-1', 'Checkout', 42, { action: 'run', ramp_duration: '2m' })
    expect(showControlFeedback).toHaveBeenCalledWith('Ramping Checkout to 42 VUsers over 2m')
  })

  it('applies per-group pause as a resumable zero-target scaling command', async () => {
    apiMock.scaleVUsers.mockResolvedValue(undefined)

    await act(async () => {
      root.render(<LiveControlsHarness test={buildRunningTest()} />)
    })

    await click('set-target')
    expect(readTestId('projected-total')).toBe('42')

    await click('set-pause')
    expect(readTestId('action')).toBe('pause')
    expect(readTestId('projected-total')).toBe('0')

    await click('scale')
    expect(apiMock.scaleVUsers).toHaveBeenLastCalledWith('test-1', 'Checkout', 0, { action: 'pause' })
    expect(showControlFeedback).toHaveBeenCalledWith('Paused Checkout VUsers')
  })

  it('pauses all VUsers through the scaling api and honors gradual ramp settings', async () => {
    apiMock.scaleVUsers.mockResolvedValue(undefined)

    await act(async () => {
      root.render(<LiveControlsHarness test={buildRunningTest()} />)
    })

    await click('toggle-ramp')
    await click('set-ramp-duration')
    await click('pause-all')

    expect(apiMock.scaleVUsers).toHaveBeenCalledWith('test-1', 'Checkout', 0, { action: 'pause', ramp_duration: '2m' })
    expect(readTestId('target')).toBe('0')
    expect(showControlFeedback).toHaveBeenCalledWith('Pausing all VUsers over 2m')
  })

  it('keeps the projected model pointed at an active backend VUser ramp target', async () => {
    await act(async () => {
      root.render(<LiveControlsHarness test={{
        ...buildRunningTest(),
        groups: [{ ...buildRunningTest().groups[0], vusers: 14 }],
        vuser_ramps: {
          Checkout: {
            group: 'Checkout',
            start_vusers: 12,
            target_vusers: 30,
            started_at: new Date(Date.now() - 15_000).toISOString(),
            ramp_duration: '1m',
            ramp_duration_seconds: 60,
          },
        },
      }} />)
    })

    expect(readTestId('target')).toBe('30')
    expect(readTestId('projected-total')).toBe('30')
    expect(readTestId('has-active-ramp')).toBe('yes')
  })

  async function click(testId: string) {
    await act(async () => {
      container.querySelector(`[data-testid="${testId}"]`)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

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
