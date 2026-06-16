// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import EntitlementsPanel from './EntitlementsPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const entitlementsApiMock = {
  getEntitlements: vi.fn(),
  getEntitlementsUsage: vi.fn(),
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
