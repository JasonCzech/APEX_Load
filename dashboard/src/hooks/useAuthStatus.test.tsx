// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useAuthStatus } from './useAuthStatus'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const authApiMock = {
  getAuthStatus: vi.fn(),
}

vi.mock('./api/useAuthApi', () => ({
  useAuthApi: () => authApiMock,
}))

function Harness() {
  const state = useAuthStatus()

  if (state.status === 'loading') {
    return <div>loading</div>
  }

  if (state.status === 'error') {
    return <div>error:{state.message}</div>
  }

  return <div>ready:{state.auth.user?.subject || 'anonymous'}</div>
}

describe('useAuthStatus', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
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
  })

  it('loads the current auth status', async () => {
    authApiMock.getAuthStatus.mockResolvedValue({
      enabled: true,
      mode: 'trusted_headers',
      authenticated: true,
      user: { subject: 'alice' },
    })

    await act(async () => {
      root.render(<Harness />)
    })

    await flushPromises()

    expect(container.textContent).toContain('ready:alice')
    expect(authApiMock.getAuthStatus).toHaveBeenCalledTimes(1)
  })

  it('exposes an error state when the auth lookup fails', async () => {
    authApiMock.getAuthStatus.mockRejectedValue(new Error('boom'))

    await act(async () => {
      root.render(<Harness />)
    })

    await flushPromises()

    expect(container.textContent).toContain('error:boom')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}