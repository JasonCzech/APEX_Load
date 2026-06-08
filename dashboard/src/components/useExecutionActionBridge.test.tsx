// @vitest-environment jsdom

import { act } from 'react'
import { useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/useApi'
import { CapacityAdvisory, TestSummary } from '../types'
import { useExecutionActionBridge } from './useExecutionActionBridge'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  startTest: vi.fn(),
  stopTest: vi.fn(),
  abortTest: vi.fn(),
  pauseTest: vi.fn(),
  resumeTest: vi.fn(),
}

vi.mock('../hooks/useApi', async () => {
  const actual = await vi.importActual('../hooks/useApi')
  return {
    ...actual,
    useApi: () => apiMock,
  }
})

function ActionBridgeHarness({ initialTest, onRunStarted }: { initialTest: TestSummary; onRunStarted?: (testId: string) => void }) {
  const [test, setTest] = useState<TestSummary | null>(initialTest)
  const model = useExecutionActionBridge('test-1', test, setTest, onRunStarted)

  return (
    <div>
      <span data-testid="status">{test?.status || 'UNKNOWN'}</span>
      <span data-testid="control-feedback">{model.controlFeedback || 'none'}</span>
      <span data-testid="start-feedback">{model.startFeedback || 'none'}</span>
      <span data-testid="conflict">{model.startCapacityConflict ? String(model.startCapacityConflict.available_generators) : 'none'}</span>
      <span data-testid="shakeout-conflict">{model.startShakeoutConflict?.summary || 'none'}</span>
      <button data-testid="start" onClick={() => void model.onStart()}>start</button>
      <button data-testid="confirm-start" onClick={() => void model.onConfirmStartAnyway()}>confirm</button>
      <button data-testid="continue-shakeout" onClick={() => void model.onContinueAfterShakeoutFailure()}>continue shakeout</button>
      <button data-testid="stop" onClick={() => void model.onStop()}>stop</button>
      <button data-testid="abort" onClick={() => void model.onAbort()}>abort</button>
      <button data-testid="pause" onClick={() => void model.onPause()}>pause</button>
      <button data-testid="resume" onClick={() => void model.onResume()}>resume</button>
    </div>
  )
}

describe('useExecutionActionBridge', () => {
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
    vi.useRealTimers()
  })

  it('surfaces capacity-shortfall feedback and supports explicit override start', async () => {
    const advisory = buildCapacityAdvisory()
    apiMock.startTest
      .mockRejectedValueOnce(new ApiError('Need more generators', 409, { code: 'capacity_shortfall', capacity_advisory: advisory }))
      .mockResolvedValueOnce({ status: 'started', test_id: 'test-1' })

    await act(async () => {
      root.render(<ActionBridgeHarness initialTest={buildQueuedTest()} />)
    })

    await click('start')

    expect(readTestId('conflict')).toBe('2')
    expect(readTestId('start-feedback')).toBe('Need more generators')

    await click('confirm-start')

    expect(apiMock.startTest).toHaveBeenNthCalledWith(1, 'test-1', undefined)
    expect(apiMock.startTest).toHaveBeenNthCalledWith(2, 'test-1', { override_capacity_shortfall: true })
    expect(readTestId('conflict')).toBe('none')
  })

  it('surfaces script shakeout evidence and supports explicit continue override', async () => {
    apiMock.startTest
      .mockRejectedValueOnce(new ApiError('Script shakeout failed', 409, {
        code: 'script_shakeout_failed',
        script_shakeout: {
          passed: false,
          summary: 'Script shakeout failed; full load test has not started.',
          results: [{
            group_name: 'Checkout',
            script_id: 'script-1',
            script_name: 'Checkout Script',
            status: 'failed',
            error: 'request Login returned 500',
          }],
        },
      }))
      .mockResolvedValueOnce({ status: 'started', test_id: 'test-1' })

    await act(async () => {
      root.render(<ActionBridgeHarness initialTest={buildQueuedTest()} />)
    })

    await click('start')

    expect(readTestId('status')).toBe('SHAKEOUT_REVIEW')
    expect(readTestId('shakeout-conflict')).toBe('Script shakeout failed; full load test has not started.')
    expect(readTestId('start-feedback')).toBe('Script shakeout failed')

    await click('continue-shakeout')

    expect(apiMock.startTest).toHaveBeenNthCalledWith(1, 'test-1', undefined)
    expect(apiMock.startTest).toHaveBeenNthCalledWith(2, 'test-1', { override_script_shakeout_failure: true })
    expect(readTestId('shakeout-conflict')).toBe('none')
  })

  it('uses the returned started run state when the API includes it', async () => {
    const onRunStarted = vi.fn()
    apiMock.startTest.mockResolvedValue({
      status: 'started',
      test_id: 'test-1',
      test: {
        ...buildRunningTest(),
        started_at: '2026-05-12T20:00:01Z',
      },
    })

    await act(async () => {
      root.render(<ActionBridgeHarness initialTest={buildQueuedTest()} onRunStarted={onRunStarted} />)
    })

    await click('start')

    expect(apiMock.startTest).toHaveBeenCalledWith('test-1', undefined)
    expect(readTestId('status')).toBe('RUNNING')
    expect(onRunStarted).toHaveBeenCalledWith('test-1')
  })

  it('optimistically updates pause and resume state while publishing shared control feedback', async () => {
    apiMock.pauseTest.mockResolvedValue(undefined)
    apiMock.resumeTest.mockResolvedValue(undefined)

    await act(async () => {
      root.render(<ActionBridgeHarness initialTest={buildRunningTest()} />)
    })

    await click('pause')

    expect(apiMock.pauseTest).toHaveBeenCalledWith('test-1')
    expect(readTestId('status')).toBe('PAUSED')
    expect(readTestId('control-feedback')).toBe('Test paused')

    await click('resume')

    expect(apiMock.resumeTest).toHaveBeenCalledWith('test-1')
    expect(readTestId('status')).toBe('RUNNING')
    expect(readTestId('control-feedback')).toBe('Test resumed')
  })

  it('routes stop and abort through the shared execution action bridge', async () => {
    apiMock.stopTest.mockResolvedValue(undefined)
    apiMock.abortTest.mockResolvedValue(undefined)

    await act(async () => {
      root.render(<ActionBridgeHarness initialTest={buildRunningTest()} />)
    })

    await click('stop')

    expect(apiMock.stopTest).toHaveBeenCalledWith('test-1')
    expect(readTestId('status')).toBe('STOPPING')

    await act(async () => {
      root.render(<ActionBridgeHarness initialTest={buildRunningTest()} />)
    })

    await click('abort')

    expect(apiMock.abortTest).toHaveBeenCalledWith('test-1')
    expect(readTestId('status')).toBe('ABORTED')
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

function buildQueuedTest(): TestSummary {
  return {
    id: 'test-1',
    name: 'Queued Checkout',
    status: 'QUEUED',
    total_vusers: 12,
    duration: '10m',
    created_at: '2026-05-12T20:00:00Z',
    groups: [],
    capacity_advisory: buildCapacityAdvisory(),
  }
}

function buildRunningTest(): TestSummary {
  return {
    id: 'test-1',
    name: 'Running Checkout',
    status: 'RUNNING',
    total_vusers: 12,
    duration: '10m',
    created_at: '2026-05-12T20:00:00Z',
    groups: [],
  }
}

function buildCapacityAdvisory(): CapacityAdvisory {
  return {
    total_vusers: 120,
    duration_seconds: 600,
    baseline_window_enabled: false,
    headroom_pct: 20,
    complexity_score: 1,
    bottleneck: 'cpu',
    availability_source: 'configured',
    configured_available_generators: 2,
    recommended_generators: 4,
    available_generators: 2,
    shortfall_generators: 2,
    requires_override: true,
    estimated_per_vuser: { cpu_cores: 0.01, memory_mb: 1, network_mbps: 0.1 },
    estimated_total: { cpu_cores: 1.2, memory_mb: 120, network_mbps: 12 },
    usable_per_generator: { cpu_cores: 4, memory_mb: 4096, network_mbps: 1000 },
    dimension_recommendations: { cpu: 4, memory: 2, network: 1, vusers: 1 },
    warnings: [],
  }
}
