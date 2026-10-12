// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import CommandCenterLiveTables from './CommandCenterLiveTables'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/useWebSocket', () => ({ useWebSocket: () => ({ connectionState: 'connected' }) }))
vi.mock('../hooks/apiClient', () => ({ request }))

let container: HTMLDivElement
let root: Root
beforeEach(() => { vi.useFakeTimers(); request.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.useRealTimers() })

it('shows each table empty or active, its leases, waits and unknown outcomes, without row values', async () => {
  request.mockResolvedValue({
    tables: [
      { id: 't1', alias: 'work', profile: 'native_queue', durability: 'memory', state: 'running', counts: { available: 0, leased: 2 }, op_unknown: 1, observations: { version: 1, wait_count: 7 } },
      { id: 't2', alias: 'customers', profile: 'native_queue', durability: 'memory', state: 'running', counts: { available: 40, leased: 0 } },
    ],
    project_tables: [{ table_id: 'p1', instance_id: 'i', attachment_id: 'a1', group: 'g', alias: 'shared', state: 'attached', revision: '3' }],
  })
  const onOpen = vi.fn()
  await act(async () => root.render(<CommandCenterLiveTables testId="run" onOpen={onOpen} />))
  const text = container.textContent || ''
  expect(text).toContain('work Empty — waiting for producers · 0 available · 2 leased · 7 waits so far · 1 unknown outcome')
  expect(text).toContain('customers Active · 40 available · 0 leased')
  expect(text).toContain('shared Project live table')
  expect(request).toHaveBeenCalledWith('/tests/run/live-tables', expect.anything())
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Open live tables')!.click())
  expect(onOpen).toHaveBeenCalledOnce()
})
