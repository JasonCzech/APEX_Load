// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetSharedTestListStoreForTests, useSharedTestList } from './useSharedTestList'
import { writeStoredDevViewMode } from './apiClient'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const listTests = vi.fn()
function Consumer({ interval, folderId, summary, activeOnly, limit }: {
  interval: number
  folderId?: string | null
  summary?: boolean
  activeOnly?: boolean
  limit?: number
}) {
  const result = useSharedTestList(
    {
      project_id: 'project-1',
      application_id: 'app-1',
      folder_id: folderId,
      summary,
      active_only: activeOnly,
      limit,
    },
    { loader: listTests, pollIntervalMs: interval },
  )
  return <span>{result.tests.length}:{result.loading ? 'loading' : 'ready'}</span>
}

function RefreshConsumer() {
  const [refreshComplete, setRefreshComplete] = useState(false)
  const result = useSharedTestList(
    { project_id: 'project-1', application_id: 'app-1' },
    { loader: listTests, pollIntervalMs: 0 },
  )
  return (
    <div>
      <span>{result.tests.map(test => test.id).join(',') || 'empty'}</span>
      <span data-testid="refresh-state">{refreshComplete ? 'complete' : 'pending'}</span>
      <button type="button" onClick={() => {
        void result.refresh().then(() => setRefreshComplete(true))
      }}>Refresh</button>
    </div>
  )
}

function MetadataConsumer() {
  const result = useSharedTestList(
    { project_id: 'project-1', summary: true, active_only: true, limit: 100, offset: 200 },
    { loader: listTests, pollIntervalMs: 0 },
  )
  return <span data-testid="metadata">{result.count}:{result.total}:{result.limit}:{result.offset}</span>
}

describe('useSharedTestList', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    window.localStorage.clear()
    resetSharedTestListStoreForTests()
    window.localStorage.clear()
    listTests.mockReset().mockResolvedValue({ tests: [{ id: 'test-1' }] })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    resetSharedTestListStoreForTests()
    vi.useRealTimers()
  })

  it('deduplicates identical consumers and polls at the shortest requested interval', async () => {
    await act(async () => {
      root.render(<><Consumer interval={3000} /><Consumer interval={10000} /></>)
      await Promise.resolve()
    })

    expect(listTests).toHaveBeenCalledTimes(1)
    expect(container.textContent).toBe('1:ready1:ready')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000)
    })
    expect(listTests).toHaveBeenCalledTimes(2)
  })

  it('preserves the last successful data when a refresh fails', async () => {
    await act(async () => {
      root.render(<Consumer interval={3000} />)
      await Promise.resolve()
    })
    listTests.mockRejectedValueOnce(new Error('offline'))

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000)
    })

    expect(container.textContent).toBe('1:ready')
  })

  it('queues and awaits a forced refresh when a request is already in flight', async () => {
    let resolveInitial: ((value: { tests: Array<{ id: string }> }) => void) | undefined
    let resolveForced: ((value: { tests: Array<{ id: string }> }) => void) | undefined
    listTests
      .mockImplementationOnce(() => new Promise(resolve => { resolveInitial = resolve }))
      .mockImplementationOnce(() => new Promise(resolve => { resolveForced = resolve }))

    await act(async () => {
      root.render(<RefreshConsumer />)
    })
    expect(listTests).toHaveBeenCalledTimes(1)

    await act(async () => {
      container.querySelector('button')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(listTests).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveInitial?.({ tests: [{ id: 'stale-test' }] })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(listTests).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="refresh-state"]')?.textContent).toBe('pending')

    await act(async () => {
      resolveForced?.({ tests: [{ id: 'fresh-test' }] })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('fresh-test')
    expect(container.textContent).not.toContain('stale-test')
    expect(container.querySelector('[data-testid="refresh-state"]')?.textContent).toBe('complete')
  })

  it('treats an omitted folder and the root folder as distinct query scopes', async () => {
    await act(async () => {
      root.render(<><Consumer interval={0} /><Consumer interval={0} folderId={null} /></>)
      await Promise.resolve()
    })

    expect(listTests).toHaveBeenCalledTimes(2)
    expect(listTests).toHaveBeenCalledWith(expect.objectContaining({ folder_id: undefined }))
    expect(listTests).toHaveBeenCalledWith(expect.objectContaining({ folder_id: null }))
  })

  it('keeps summary and active-only list projections in distinct cache scopes', async () => {
    await act(async () => {
      root.render(<>
        <Consumer interval={0} />
        <Consumer interval={0} summary activeOnly limit={100} />
      </>)
      await Promise.resolve()
    })

    expect(listTests).toHaveBeenCalledTimes(2)
    expect(listTests).toHaveBeenCalledWith(expect.objectContaining({
      summary: true,
      active_only: true,
      limit: 100,
    }))
  })

  it('retains list pagination metadata and supplies numeric defaults', async () => {
    listTests.mockResolvedValueOnce({
      tests: [{ id: 'active-test' }],
      count: 1,
      total: 245,
      limit: 100,
      offset: 200,
    })
    await act(async () => {
      root.render(<MetadataConsumer />)
      await Promise.resolve()
    })

    expect(container.querySelector('[data-testid="metadata"]')?.textContent).toBe('1:245:100:200')
  })

  it('reports an uncached enabled query as loading on its first render', async () => {
    let resolveRequest: ((value: { tests: Array<{ id: string }> }) => void) | undefined
    listTests.mockImplementationOnce(() => new Promise(resolve => { resolveRequest = resolve }))

    await act(async () => {
      root.render(<Consumer interval={0} />)
    })
    expect(container.textContent).toBe('0:loading')

    await act(async () => {
      resolveRequest?.({ tests: [{ id: 'test-1' }] })
      await Promise.resolve()
    })
    expect(container.textContent).toBe('1:ready')
  })

  it('invalidates cached results when the API request context changes', async () => {
    await act(async () => {
      root.render(<Consumer interval={3000} />)
      await Promise.resolve()
    })
    expect(container.textContent).toBe('1:ready')

    listTests.mockResolvedValue({ tests: [{ id: 'test-2' }, { id: 'test-3' }] })
    await act(async () => {
      writeStoredDevViewMode('user')
      await Promise.resolve()
    })

    expect(listTests).toHaveBeenCalledTimes(2)
    expect(container.textContent).toBe('2:ready')

    await act(async () => {
      root.render(<><Consumer interval={3000} /><Consumer interval={10000} /></>)
      await Promise.resolve()
    })
    expect(listTests).toHaveBeenCalledTimes(2)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000)
    })
    expect(listTests).toHaveBeenCalledTimes(3)
  })
})
