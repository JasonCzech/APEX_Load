// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const request = vi.hoisted(() => vi.fn())
vi.mock('../../hooks/apiClient', async importOriginal => ({ ...(await importOriginal<typeof import('../../hooks/apiClient')>()), request }))

import { useAnatomyReports, type AnatomyLoad } from './useAnatomyReports'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const result: { current: Record<string, AnatomyLoad> } = { current: {} }
function Probe({ ids, attempt }: { ids: string[]; attempt: number }) {
  result.current = useAnatomyReports(ids, true, [], attempt)
  return null
}
const flush = () => act(async () => { await Promise.resolve() })

interface Pending { id: string; signal: AbortSignal; resolve: (value: unknown) => void; reject: (error: unknown) => void }

describe('useAnatomyReports', () => {
  let pending: Pending[]
  let root: Root
  let container: HTMLDivElement
  const render = (ids: string[], attempt: number) => act(async () => root.render(<Probe ids={ids} attempt={attempt} />))
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    pending = []
    request.mockReset()
    request.mockImplementation((path: string, init: { signal: AbortSignal }) => new Promise((resolve, reject) => {
      const id = decodeURIComponent(path.split('/')[2])
      pending.push({ id, signal: init.signal, resolve, reject })
    }))
  })

  it('refetches a run whose fetch a Retry re-run aborted, instead of leaving it loading forever', async () => {
    const ids = ['b1', 'b2']
    await render(ids, 0)
    expect(pending.map(p => p.id).sort()).toEqual(['b1', 'b2'])
    // b1 fails while b2 is still in flight.
    await act(async () => { pending.find(p => p.id === 'b1')!.reject(new Error('boom')) })
    expect(result.current.b1).toMatchObject({ status: 'error' })
    expect(result.current.b2).toEqual({ status: 'loading' })

    // Retry: the re-run aborts b2's fetch, so it must fetch b2 again too.
    await render(ids, 1)
    await flush()
    expect(pending.filter(p => !p.signal.aborted).map(p => p.id).sort()).toEqual(['b1', 'b2'])
    const live = pending.filter(p => !p.signal.aborted)
    await act(async () => { for (const p of live) p.resolve({ time_anatomy: { schema: p.id } }) })
    expect(result.current.b1).toEqual({ status: 'ready', anatomy: { schema: 'b1' } })
    expect(result.current.b2).toEqual({ status: 'ready', anatomy: { schema: 'b2' } })
  })

  it('does not refetch a run that is already ready', async () => {
    await render(['b1'], 0)
    expect(pending).toHaveLength(1)
    await act(async () => { pending[0].resolve({ time_anatomy: undefined }) })
    expect(result.current.b1).toEqual({ status: 'ready', anatomy: undefined })
    await render(['b1'], 1)
    expect(pending).toHaveLength(1)
  })
})
