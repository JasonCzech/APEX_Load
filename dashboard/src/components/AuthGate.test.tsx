// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AuthGate from './AuthGate'
import { AuthStatusState } from '../hooks/useAuthStatus'

type ReadyAuthState = Extract<AuthStatusState, { status: 'ready' }>['auth']

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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
    await act(async () => {
      root.render(
        <AuthGate state={readyState({ enabled: false, mode: 'disabled', authenticated: false })}>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    expect(container.textContent).toContain('Protected content')
    expect(container.textContent).not.toContain('Sign-in required')
  })

  it('blocks the dashboard when trusted-header auth is enabled without a user', async () => {
    await act(async () => {
      root.render(
        <AuthGate state={readyState({ enabled: true, mode: 'trusted_headers', provider_name: 'Microsoft Entra ID', login_url: 'https://login.example.test/start', authenticated: false })}>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    expect(container.textContent).toContain('Sign-in required')
    expect(container.textContent).toContain('Continue to Microsoft Entra ID')
    const link = container.querySelector('a') as HTMLAnchorElement | null
    expect(link?.href).toBe('https://login.example.test/start')
    expect(container.textContent).not.toContain('Protected content')
  })

  it('renders children when a trusted-header identity is present', async () => {
    await act(async () => {
      root.render(
        <AuthGate state={readyState({
          enabled: true,
          mode: 'trusted_headers',
          authenticated: true,
          user: {
            subject: 'alice',
            display_name: 'Alice Admin',
            global_roles: ['system_admin'],
          },
        })}>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    expect(container.textContent).toContain('Protected content')
    expect(container.textContent).not.toContain('Checking access')
  })

  it('blocks the dashboard with OIDC-specific copy when native OIDC is enabled without a session', async () => {
    await act(async () => {
      root.render(
        <AuthGate state={readyState({ enabled: true, mode: 'oidc', provider_name: 'Microsoft Entra ID', login_url: '/api/v1/auth/login', authenticated: false })}>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    expect(container.textContent).toContain('native OIDC authentication')
    expect(container.textContent).toContain('Continue to Microsoft Entra ID')
    const link = container.querySelector('a') as HTMLAnchorElement | null
    expect(link?.getAttribute('href')).toBe('/api/v1/auth/login')
    expect(container.textContent).not.toContain('Protected content')
  })

  it('explains when the native oidc session expired', async () => {
    await act(async () => {
      root.render(
        <AuthGate state={readyState({ enabled: true, mode: 'oidc', provider_name: 'Microsoft Entra ID', login_url: '/api/v1/auth/login', authenticated: false, session_status: 'expired' })}>
          <div data-testid="protected">Protected content</div>
        </AuthGate>,
      )
    })

    expect(container.textContent).toContain('session has expired')
    expect(container.textContent).toContain('Continue to Microsoft Entra ID')
    expect(container.textContent).not.toContain('Protected content')
  })
})

function readyState(auth: ReadyAuthState): AuthStatusState {
  return { status: 'ready', auth }
}
