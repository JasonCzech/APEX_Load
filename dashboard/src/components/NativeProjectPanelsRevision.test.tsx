// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request: mocks.request }))
const { NativeProjectPanels } = await import('./NativeAnalyticsPanel')

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const panel = (title: string, aggregation: string) => ({ id: 'latency', title, kind: 'line', x: 0, y: 0, w: 24, h: 8, query: { query_version: 2, metric: 'response_time', aggregation } })
const current: DashboardDefinition = {
  id: 'd1', project_id: 'p', name: 'Ops', visibility: 'project', revision: 2, created_at: '2026-10-08T10:00:00Z', updated_at: '2026-10-08T11:00:00Z',
  tabs: [{ id: 't', title: 'Tab', panels: [panel('Latency now', 'p99')] }],
}
const retained = [{ revision: 2, name: 'Ops', saved_at: '2026-10-08T11:00:00Z' }, { revision: 1, name: 'Ops', saved_at: '2026-10-08T10:00:00Z' }]

async function flush() {
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
}

describe('NativeProjectPanels saved revisions', () => {
  afterEach(() => mocks.request.mockReset())

  it('reopens exactly a saved revision, read-only, and returns to the current one', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/monitoring/dashboard-definitions/d1') return { ...current, retained_revisions: retained }
      if (path === '/monitoring/dashboard-definitions/d1?revision=1') return { ...current, revision: 1, current_revision: 2, tabs: [{ id: 't', title: 'Tab', panels: [panel('Latency then', 'p95')] }], retained_revisions: retained }
      throw new Error(`unexpected ${path}`)
    })
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => root.render(<NativeProjectPanels definitions={[current]} history={[]} />))
    await flush()
    const select = container.querySelector('select[aria-label="Saved revision"]') as HTMLSelectElement
    expect([...select.options].map(option => option.textContent)).toEqual(['Current (revision 2)', expect.stringContaining('Revision 1')])
    expect(container.textContent).toContain('Latency now')
    await act(async () => {
      select.value = '1'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    expect(mocks.request).toHaveBeenCalledWith('/monitoring/dashboard-definitions/d1?revision=1', expect.anything())
    expect(container.textContent).toContain('Showing saved revision 1 of “Ops”, exactly as it was saved. The current revision is 2.')
    expect(container.textContent).toContain('Latency then')
    expect(container.textContent).not.toContain('Latency now')
    await act(async () => {
      select.value = '2'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).toContain('Latency now')
    expect(container.textContent).not.toContain('Showing saved revision')
    await act(async () => root.unmount())
  })

  it('offers no revision choice for a dashboard with one saved revision', async () => {
    mocks.request.mockResolvedValue({ ...current, retained_revisions: [retained[0]] })
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => root.render(<NativeProjectPanels definitions={[current]} history={[]} />))
    await flush()
    expect(container.querySelector('select[aria-label="Saved revision"]')).toBeNull()
    await act(async () => root.unmount())
  })
})
