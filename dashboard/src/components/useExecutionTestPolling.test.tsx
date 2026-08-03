// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS,
  EXECUTION_POLL_INTERVAL_MS,
  useExecutionTestPolling,
} from './useExecutionTestPolling'

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
      <span data-testid="archive">{model.test?.archive ? 'ready' : 'pending'}</span>
      <span data-testid="error">{model.error || 'none'}</span>
      <span data-testid="archive-timeout">{model.archiveFinalizationTimedOut ? 'timed-out' : 'polling'}</span>
      <span data-testid="archive-check-error">{model.archiveCheckError || 'none'}</span>
      {model.archiveFinalizationTimedOut && (
        <button type="button" onClick={() => { void model.checkArchiveAgain() }} disabled={model.archiveCheckPending}>
          {model.archiveCheckPending ? 'Checking…' : 'Check again'}
        </button>
      )}
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
    apiMock.getTest.mockResolvedValue(buildTest('COMPLETE', true))

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

  it('keeps polling a terminal run until its archive is attached', async () => {
    apiMock.getTest
      .mockResolvedValueOnce(buildTest('FAILED'))
      .mockResolvedValueOnce(buildTest('FAILED', true))

    await act(async () => {
      root.render(<PollingHarness />)
    })
    await flushPromises()

    expect(readTestId('status')).toBe('FAILED')
    expect(readTestId('archive')).toBe('pending')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(EXECUTION_POLL_INTERVAL_MS)
    })

    expect(apiMock.getTest).toHaveBeenCalledTimes(2)
    expect(readTestId('archive')).toBe('ready')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(EXECUTION_POLL_INTERVAL_MS * 2)
    })
    expect(apiMock.getTest).toHaveBeenCalledTimes(2)
  })

  it('bounds archive finalization polling when no archive is produced', async () => {
    apiMock.getTest.mockResolvedValue(buildTest('ABORTED'))

    await act(async () => {
      root.render(<PollingHarness />)
    })
    await flushPromises()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS)
    })
    const callsAtTimeout = apiMock.getTest.mock.calls.length
    expect(callsAtTimeout).toBeGreaterThan(1)
    expect(readTestId('archive-timeout')).toBe('timed-out')
    expect(container.textContent).toContain('Check again')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(EXECUTION_POLL_INTERVAL_MS * 3)
    })
    expect(apiMock.getTest).toHaveBeenCalledTimes(callsAtTimeout)
  })

  it('checks once on demand after the polling window and discovers a late archive', async () => {
    let archiveReady = false
    apiMock.getTest.mockImplementation(async () => buildTest('FAILED', archiveReady))

    await act(async () => {
      root.render(<PollingHarness />)
    })
    await flushPromises()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS)
    })
    expect(readTestId('archive-timeout')).toBe('timed-out')

    const callsAtTimeout = apiMock.getTest.mock.calls.length
    archiveReady = true
    const checkAgain = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Check again')
    expect(checkAgain).toBeDefined()
    await act(async () => {
      checkAgain?.click()
      checkAgain?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.getTest).toHaveBeenCalledTimes(callsAtTimeout + 1)
    expect(readTestId('archive')).toBe('ready')
    expect(readTestId('archive-timeout')).toBe('polling')
    expect(container.textContent).not.toContain('Check again')
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

function buildTest(status: 'QUEUED' | 'RUNNING' | 'COMPLETE' | 'FAILED' | 'ABORTED', withArchive = false) {
  return {
    id: 'test-1',
    name: 'Checkout Journey',
    status,
    total_vusers: 10,
    duration: '5m',
    created_at: '2026-05-12T20:00:00Z',
    groups: [],
    ...(withArchive ? {
      archive: {
        test_id: 'test-1',
        schema_version: '1',
        archive_path: '/tmp/test-1',
        manifest_path: '/tmp/test-1/manifest.json',
        transaction_count: 100,
        resource_sample_count: 1,
        interval_summary_count: 1,
        created_at: '2026-05-12T20:05:00Z',
        completed_at: '2026-05-12T20:05:01Z',
      },
    } : {}),
  }
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}
