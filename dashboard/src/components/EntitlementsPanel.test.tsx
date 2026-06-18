// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import EntitlementsPanel from './EntitlementsPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const entitlementsApiMock = {
  getEntitlements: vi.fn(),
  getEntitlementsUsage: vi.fn(),
  getProjectEntitlementsUsage: vi.fn(),
}

vi.mock('../hooks/api/useEntitlementsApi', () => ({
  useEntitlementsApi: () => entitlementsApiMock,
}))

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

describe('EntitlementsPanel', () => {
  it('renders edition, consumption, and enabled bundles', async () => {
    entitlementsApiMock.getEntitlements.mockResolvedValue({
      edition: 'self-hosted',
      limits: { max_concurrent_tests: 10 },
      protocol_bundles: { count: 1, bundles: [{ id: 'db', display_name: 'Database Protocol Bundle', governance: 'review' }] },
    })
    entitlementsApiMock.getEntitlementsUsage.mockResolvedValue({
      concurrent_tests: { used: 3, limit: 10 },
      service_accounts: 2,
      projects: 4,
    })
    await act(async () => {
      root.render(<EntitlementsPanel />)
    })
    await flush()
    expect(container.textContent).toContain('self-hosted')
    expect(container.textContent).toContain('Database Protocol Bundle')
    expect(container.textContent).toContain('3 / 10')
    // 30% usage → success-tone headroom bar.
    expect(container.querySelector('.pp-bar-fill-success')).toBeTruthy()
  })

  it('renders the richer per-limit, per-bundle, and per-project breakdowns', async () => {
    entitlementsApiMock.getEntitlements.mockResolvedValue({
      edition: 'self-hosted',
      limits: { max_concurrent_tests: 10 },
      protocol_bundles: { count: 1, bundles: [{ id: 'db', display_name: 'Database Protocol Bundle', protocols: ['database'] }] },
    })
    entitlementsApiMock.getEntitlementsUsage.mockResolvedValue({
      concurrent_tests: { used: 3, limit: 10 },
      service_accounts: 2,
      projects: 1,
      limits: [
        { key: 'concurrent_tests', label: 'Concurrent tests', scope: 'global', used: 3, limit: 10, remaining: 7, utilization: 0.3, unlimited: false },
        { key: 'active_vusers', label: 'Active VUsers', scope: 'global', used: 50, limit: 0, remaining: 0, utilization: 0, unlimited: true },
      ],
      protocol_bundles: [
        { id: 'db', display_name: 'Database Protocol Bundle', protocols: ['database'], run_count: 5, total_vusers: 120, vuser_seconds: 0 },
      ],
      by_project: [
        {
          project_id: 'proj-a',
          name: 'Alpha',
          limits: [{ key: 'concurrent_tests', label: 'Concurrent tests', scope: 'project', used: 1, limit: 5, remaining: 4, utilization: 0.2, unlimited: false }],
        },
      ],
    })
    await act(async () => {
      root.render(<EntitlementsPanel />)
    })
    await flush()
    // Per-limit list (includes the uncapped Active VUsers line).
    expect(container.textContent).toContain('Active VUsers')
    expect(container.textContent).toContain('no cap')
    // Per-bundle consumption merged onto the catalog row.
    expect(container.textContent).toContain('consumption: 5 runs · 120 VUsers')
    // Per-project breakdown.
    expect(container.textContent).toContain('Per-project consumption')
    expect(container.textContent).toContain('Alpha')
  })

  it('shows a danger headroom bar when near the concurrency limit', async () => {
    entitlementsApiMock.getEntitlements.mockResolvedValue({
      edition: 'self-hosted',
      limits: { max_concurrent_tests: 10 },
      protocol_bundles: { count: 0, bundles: [] },
    })
    entitlementsApiMock.getEntitlementsUsage.mockResolvedValue({
      concurrent_tests: { used: 10, limit: 10 },
      service_accounts: 0,
      projects: 1,
    })
    await act(async () => {
      root.render(<EntitlementsPanel />)
    })
    await flush()
    expect(container.querySelector('.pp-bar-fill-danger')).toBeTruthy()
    expect(container.textContent).toContain('No protocol bundles reported')
  })
})
