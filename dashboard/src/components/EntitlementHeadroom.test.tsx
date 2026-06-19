// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import EntitlementHeadroom from './EntitlementHeadroom'
import { ApiError } from '../hooks/apiClient'

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

describe('EntitlementHeadroom', () => {
  it('renders used/limit, free count, and a warning bar near the limit', async () => {
    entitlementsApiMock.getEntitlementsUsage.mockResolvedValue({
      concurrent_tests: { used: 8, limit: 10 },
      service_accounts: 0,
      projects: 0,
    })
    await act(async () => {
      root.render(<EntitlementHeadroom />)
    })
    await flush()
    expect(container.textContent).toContain('8 / 10')
    expect(container.textContent).toContain('2 free')
    // 80% utilization → warning tone (>= 70%, < 90%).
    expect(container.querySelector('.pp-bar-fill-warning')).not.toBeNull()
  })

  it('renders ∞ and a zero-width bar when no concurrency limit is set', async () => {
    entitlementsApiMock.getEntitlementsUsage.mockResolvedValue({
      concurrent_tests: { used: 5, limit: 0 },
      service_accounts: 0,
      projects: 0,
    })
    await act(async () => {
      root.render(<EntitlementHeadroom />)
    })
    await flush()
    expect(container.textContent).toContain('5 / ∞')
    const bar = container.querySelector('.pp-bar-fill') as HTMLElement
    expect(bar.style.width).toBe('0%')
    expect(bar.classList.contains('pp-bar-fill-success')).toBe(true)
  })

  it('shows an error message and clears usage on failure', async () => {
    entitlementsApiMock.getEntitlementsUsage.mockRejectedValue(new ApiError('headroom unavailable', 500))
    await act(async () => {
      root.render(<EntitlementHeadroom />)
    })
    await flush()
    expect(container.querySelector('.pp-error')?.textContent).toContain('headroom unavailable')
    expect(container.textContent).not.toContain('Concurrent tests (used')
  })
})
