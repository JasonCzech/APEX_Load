// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AuthGate from './AuthGate'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const authApiMock = {
  getAuthStatus: vi.fn(),
}

vi.mock('../hooks/api/useAuthApi', () => ({
  useAuthApi: () => authApiMock,
}))

describe('AuthGate', () => {
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

  it('renders children when authentication is disabled', async () => {
    authApiMock.getAuthStatus.mockResolvedValue({
      enabled: false,
      mode: 'disabled',
      authenticated: false,
    })

    await act(async () => {
      root.render(
        <AuthGate>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    await flushPromises()

    expect(container.textContent).toContain('Protected content')
    expect(container.textContent).not.toContain('Sign-in required')
  })

  it('blocks the dashboard when trusted-header auth is enabled without a user', async () => {
    authApiMock.getAuthStatus.mockResolvedValue({
      enabled: true,
      mode: 'trusted_headers',
      authenticated: false,
    })

    await act(async () => {
      root.render(
        <AuthGate>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    await flushPromises()

    expect(container.textContent).toContain('Sign-in required')
    expect(container.textContent).not.toContain('Protected content')
  })

  it('renders children when a trusted-header identity is present', async () => {
    authApiMock.getAuthStatus.mockResolvedValue({
      enabled: true,
      mode: 'trusted_headers',
      authenticated: true,
      user: {
        subject: 'alice',
        display_name: 'Alice Admin',
        global_roles: ['system_admin'],
      },
    })

    await act(async () => {
      root.render(
        <AuthGate>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    await flushPromises()

    expect(container.textContent).toContain('Protected content')
    expect(container.textContent).not.toContain('Checking access')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}