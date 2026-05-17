// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import Sidebar from './Sidebar'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const authApiMock = {
  logout: vi.fn(),
}

vi.mock('../hooks/api/useAuthApi', () => ({
  useAuthApi: () => authApiMock,
}))

describe('Sidebar', () => {
  let container: HTMLDivElement
  let root: Root
  const originalLocation = window.location

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...originalLocation,
        assign: vi.fn(),
      },
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

  it('shows the authenticated identity summary in the footer', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'trusted_headers',
            logout_url: '/logout',
            authenticated: true,
            user: {
              subject: 'alice',
              display_name: 'Alice Admin',
              global_roles: ['system_admin', 'viewer'],
            },
          }}
        />,
      )
    })

    expect(container.textContent).toContain('Alice Admin')
    expect(container.textContent).toContain('System Admin · Viewer')
    const logoutLink = container.querySelector('.sidebar-identity-action') as HTMLAnchorElement | null
    expect(logoutLink?.getAttribute('href')).toBe('/logout')
    expect(logoutLink?.textContent).toBe('Sign out')
  })

  it('posts native oidc logout with the csrf token and redirects to the returned destination', async () => {
    authApiMock.logout.mockResolvedValue({ redirect_url: '/' })

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'oidc',
            logout_url: '/api/v1/auth/logout',
            logout_csrf_token: 'csrf-token',
            authenticated: true,
            user: {
              subject: 'alice',
              display_name: 'Alice Admin',
              global_roles: ['system_admin'],
            },
          }}
        />,
      )
    })

    const logoutButton = container.querySelector('.sidebar-identity-action-button') as HTMLButtonElement | null
    expect(logoutButton?.textContent).toBe('Sign out')
    if (!logoutButton) {
      return
    }

    await act(async () => {
      logoutButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(authApiMock.logout).toHaveBeenCalledWith('csrf-token')
    expect(window.location.assign).toHaveBeenCalledWith('/')
  })

  it('shows imminent native oidc session expiry without replacing identity metadata', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'oidc',
            logout_url: '/api/v1/auth/logout',
            logout_csrf_token: 'csrf-token',
            authenticated: true,
            session_status: 'active',
            session_ttl_seconds: 240,
            user: {
              subject: 'alice',
              display_name: 'Alice Admin',
              global_roles: ['system_admin'],
            },
          }}
        />,
      )
    })

    expect(container.textContent).toContain('Alice Admin')
    expect(container.textContent).toContain('System Admin')
    expect(container.textContent).toContain('Session expires in 4 min')
  })

  it('renders the active project and switches projects from the selector', async () => {
    const onProjectChange = vi.fn()

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          projects={[
            { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
            { id: 'project-2', name: 'Search', created_at: '', updated_at: '' },
          ]}
          activeProject={{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }}
          onProjectChange={onProjectChange}
        />,
      )
    })

    expect(container.textContent).toContain('Checkout')

    const projectButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Checkout'))
    expect(projectButton).toBeDefined()
    if (!projectButton) {
      return
    }

    await act(async () => {
      projectButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const searchButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Search')
    expect(searchButton).toBeDefined()
    if (!searchButton) {
      return
    }

    await act(async () => {
      searchButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onProjectChange).toHaveBeenCalledWith({ id: 'project-2', name: 'Search', created_at: '', updated_at: '' })
  })
})
