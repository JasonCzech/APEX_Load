// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useExecutionTestPolling } from './useExecutionTestPolling'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getTest: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

function PollingHarness({ enabled = true, testId = 'test-1' }: { enabled?: boolean; testId?: string }) {
  const model = useExecutionTestPolling(testId, { enabled })

  return (
    <div>
      <span data-testid="status">{model.test?.status || 'none'}</span>
      <span data-testid="error">{model.error || 'none'}</span>
    </div>
  )
}

describe('useExecutionTestPolling', () => {
  let container: HTMLDivElement
  let root: Root
  let visibilityState: DocumentVisibilityState

  beforeEach(() => {
    vi.useFakeTimers()
    visibilityState = 'visible'
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => visibilityState,
    })
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

  it('loads immediately and polls for updated execution data', async () => {
    apiMock.getTest
      .mockResolvedValueOnce(buildTest('QUEUED'))
      .mockResolvedValueOnce(buildTest('RUNNING'))

    await act(async () => {
      root.render(<PollingHarness />)
    })

    await flushPromises()

    expect(apiMock.getTest).toHaveBeenCalledTimes(1)
    expect(readTestId('status')).toBe('QUEUED')

    await act(async () => {
      vi.advanceTimersByTime(2000)
    })
    await flushPromises()

    expect(apiMock.getTest).toHaveBeenCalledTimes(2)
    expect(readTestId('status')).toBe('RUNNING')
  })

  it('skips hidden loads and refreshes when the page becomes visible again', async () => {
    visibilityState = 'hidden'
    apiMock.getTest.mockResolvedValue(buildTest('COMPLETE'))

    await act(async () => {
      root.render(<PollingHarness />)
    })

    await flushPromises()

    expect(apiMock.getTest).not.toHaveBeenCalled()
    expect(readTestId('status')).toBe('none')

    visibilityState = 'visible'
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await flushPromises()

    expect(apiMock.getTest).toHaveBeenCalledTimes(1)
    expect(readTestId('status')).toBe('COMPLETE')

    await act(async () => {
      vi.advanceTimersByTime(4000)
    })
    await flushPromises()

    expect(apiMock.getTest).toHaveBeenCalledTimes(1)
  })

  it('does not load when disabled', async () => {
    apiMock.getTest.mockResolvedValue(buildTest('RUNNING'))

    await act(async () => {
      root.render(<PollingHarness enabled={false} />)
    })
    await flushPromises()

    expect(apiMock.getTest).not.toHaveBeenCalled()
    expect(readTestId('status')).toBe('none')
  })

  it('does not stack overlapping polls', async () => {
    let resolveFirst: (value: ReturnType<typeof buildTest>) => void = () => {}
    apiMock.getTest.mockReturnValueOnce(new Promise(resolve => {
      resolveFirst = resolve
    }))
    apiMock.getTest.mockResolvedValue(buildTest('RUNNING'))

    await act(async () => {
      root.render(<PollingHarness />)
    })

    await act(async () => {
      vi.advanceTimersByTime(4000)
    })
    expect(apiMock.getTest).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveFirst(buildTest('RUNNING'))
    })
    await flushPromises()

    await act(async () => {
      vi.advanceTimersByTime(2000)
    })
    await flushPromises()

    expect(apiMock.getTest).toHaveBeenCalledTimes(2)
  })

  it('clears a transient polling error after the next successful response', async () => {
    apiMock.getTest
      .mockRejectedValueOnce(new Error('controller unavailable'))
      .mockResolvedValueOnce(buildTest('RUNNING'))

    await act(async () => {
      root.render(<PollingHarness />)
    })
    await flushPromises()

    expect(readTestId('error')).toBe('controller unavailable')

    await act(async () => {
      vi.advanceTimersByTime(2000)
    })
    await flushPromises()

    expect(readTestId('status')).toBe('RUNNING')
    expect(readTestId('error')).toBe('none')
  })

  it('ignores a late response after the requested test changes', async () => {
    let resolveFirst: (value: ReturnType<typeof buildTest>) => void = () => {}
    apiMock.getTest
      .mockReturnValueOnce(new Promise(resolve => {
        resolveFirst = resolve
      }))
      .mockResolvedValueOnce({ ...buildTest('RUNNING'), id: 'test-2', name: 'Second Test' })

    await act(async () => {
      root.render(<PollingHarness testId="test-1" />)
    })

    await act(async () => {
      root.render(<PollingHarness testId="test-2" />)
    })
    await flushPromises()

    expect(apiMock.getTest).toHaveBeenNthCalledWith(1, 'test-1')
    expect(apiMock.getTest).toHaveBeenNthCalledWith(2, 'test-2')
    expect(readTestId('status')).toBe('RUNNING')

    await act(async () => {
      resolveFirst(buildTest('QUEUED'))
    })
    await flushPromises()

    expect(readTestId('status')).toBe('RUNNING')
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

function buildTest(status: 'QUEUED' | 'RUNNING' | 'COMPLETE') {
  return {
    id: 'test-1',
    name: 'Checkout Journey',
    status,
    total_vusers: 10,
    duration: '5m',
    created_at: '2026-05-12T20:00:00Z',
    groups: [],
  }
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}
