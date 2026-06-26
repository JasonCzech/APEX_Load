// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DevViewProvider } from '../hooks/DevViewProvider'
import Sidebar from './Sidebar'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const authApiMock = {
  logout: vi.fn(),
}
const systemApiMock = {
  getVersion: vi.fn(),
}

vi.mock('../hooks/api/useAuthApi', () => ({
  useAuthApi: () => authApiMock,
}))
vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => systemApiMock,
}))

describe('Sidebar', () => {
  let container: HTMLDivElement
  let root: Root
  const originalLocation = window.location

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    window.localStorage.clear()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...originalLocation,
        assign: vi.fn(),
      },
    })
    systemApiMock.getVersion.mockResolvedValue({
      build_time: '2026-06-04T14:00:00Z',
      commit: 'abc1234',
      environment: 'local',
      go_version: 'go1.26',
      version: '1.2.3',
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    window.localStorage.clear()
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

  it('closes workspace selector menus from outside clicks and Escape', async () => {
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
        />,
      )
    })

    const projectButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Checkout'))
    expect(projectButton).toBeDefined()
    if (!projectButton) {
      return
    }

    await act(async () => {
      projectButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.project-menu-item')).map(item => item.textContent)).toContain('Search')

    await act(async () => {
      document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.project-menu-item')).map(item => item.textContent)).not.toContain('Search')

    await act(async () => {
      projectButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.project-menu-item')).map(item => item.textContent)).toContain('Search')

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.project-menu-item')).map(item => item.textContent)).not.toContain('Search')
  })

  it('keeps only one workspace selector menu open at a time', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[
            { id: 'domain-1', name: 'Retail', created_at: '', updated_at: '' },
            { id: 'domain-2', name: 'Banking', created_at: '', updated_at: '' },
          ]}
          activeDomain={{ id: 'domain-1', name: 'Retail', created_at: '', updated_at: '' }}
          projects={[
            { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
            { id: 'project-2', name: 'Search', created_at: '', updated_at: '' },
          ]}
          activeProject={{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }}
        />,
      )
    })

    const domainButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Retail'))
    const projectButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Checkout'))
    expect(domainButton).toBeDefined()
    expect(projectButton).toBeDefined()
    if (!domainButton || !projectButton) {
      return
    }

    await act(async () => {
      domainButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.project-menu-item')).map(item => item.textContent)).toContain('Banking')

    await act(async () => {
      projectButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const menuItems = Array.from(container.querySelectorAll('.project-menu-item')).map(item => item.textContent)
    expect(menuItems).toContain('Search')
    expect(menuItems).not.toContain('Banking')
  })

  it('links the API guide from the main dashboard navigation', async () => {
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={onNavigate}
          connectionState="connected"
        />,
      )
    })

    const guideButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('API Guide'))
    expect(guideButton).toBeDefined()
    if (!guideButton) {
      return
    }

    await act(async () => {
      guideButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('docs')
  })

  it('links the dedicated fleet workspace from the main dashboard navigation', async () => {
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={onNavigate}
          connectionState="connected"
        />,
      )
    })

    const fleetButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Fleet')
    expect(fleetButton).toBeDefined()
    if (!fleetButton) {
      return
    }

    await act(async () => {
      fleetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('fleet')
  })

  it('places primary workflow destinations in the requested sidebar order', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
        />,
      )
    })

    const labels = Array.from(container.querySelectorAll('.sidebar-nav .nav-label')).map(label => label.textContent)

    expect(labels.slice(0, 7)).toEqual(['Dashboard', 'New Test', 'Command Center', 'Results', 'Scripts', 'Scenarios', 'Repository'])
  })

  it('uses distinct Dashboard and Command Center sidebar icons', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
        />,
      )
    })

    const buttons = Array.from(container.querySelectorAll('.sidebar-nav button'))
    const dashboardButton = buttons.find(button => button.textContent?.includes('Dashboard'))
    const commandCenterButton = buttons.find(button => button.textContent?.includes('Command Center'))
    const dashboardIcon = dashboardButton?.querySelector('svg')
    const commandCenterIcon = commandCenterButton?.querySelector('svg')

    expect(dashboardIcon?.getAttribute('data-nav-icon')).toBe('dashboard-grid')
    expect(commandCenterIcon?.getAttribute('data-nav-icon')).toBe('command-center-signal')
    expect(commandCenterIcon?.querySelector('circle')).not.toBeNull()
    expect(commandCenterIcon?.querySelectorAll('rect')).toHaveLength(0)
  })

  it('keeps live status and build info visible when the sidebar is collapsed', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
        />,
      )
      await Promise.resolve()
    })

    expect(container.querySelector('.sidebar-footer .sidebar-status')).not.toBeNull()
    expect(container.querySelector('.sidebar-footer .build-badge')).not.toBeNull()

    const collapseButton = container.querySelector('.collapse-btn') as HTMLButtonElement | null
    expect(collapseButton).not.toBeNull()
    if (!collapseButton) {
      return
    }

    await act(async () => {
      collapseButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
    expect(container.querySelector('.sidebar.collapsed .sidebar-footer .sidebar-status')).not.toBeNull()
    expect(container.querySelector('.sidebar.collapsed .sidebar-footer .build-badge')).not.toBeNull()
  })

  it('does not render raw SHA build versions as the badge label', async () => {
    const rawRevision = 'abcdef0123456789abcdef0123456789abcdef01'
    systemApiMock.getVersion.mockResolvedValueOnce({
      build_time: '2026-06-13T14:00:00Z',
      commit: 'abcdef0',
      display_version: 'v1.4.2-dev.20260613+abcdef0',
      environment: 'kubernetes',
      go_version: 'go1.26',
      version: rawRevision,
    })

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
        />,
      )
      await Promise.resolve()
    })

    expect(container.textContent).toContain('v1.4.2-dev.20260613+abcdef0')
    expect(container.textContent).not.toContain(rawRevision)
  })

  it('shows Access navigation only when service-account management is allowed', async () => {
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={onNavigate}
          connectionState="connected"
          showAccess
        />,
      )
    })

    const accessButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Access'))
    expect(accessButton).toBeDefined()
    if (!accessButton) {
      return
    }

    await act(async () => {
      accessButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('access')
  })

  it('hides Access navigation when service-account management is denied', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          showAccess={false}
        />,
      )
    })

    expect(container.textContent).not.toContain('Access')
  })

  it('renders a dev-only Admin/User toggle that persists editor preview and hides admin navigation', async () => {
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(
        <DevViewProvider>
          <Sidebar
            active="dashboard"
            onNavigate={onNavigate}
            connectionState="connected"
            authStatus={{
              enabled: false,
              mode: 'dev',
              authenticated: false,
            }}
          />
        </DevViewProvider>,
      )
    })

    expect(container.textContent).toContain('Dev View')
    expect(container.textContent).toContain('Admin')
    expect(container.textContent).toContain('User')
    expect(container.textContent).toContain('New Test')
    expect(container.textContent).toContain('API Guide')
    expect(container.textContent).toContain('Settings')

    const userButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'User')
    expect(userButton).toBeDefined()
    if (!userButton) {
      return
    }

    await act(async () => {
      userButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(window.localStorage.getItem('apexload.devViewMode')).toBe('user')
    expect(container.textContent).toContain('Editor preview')
    expect(container.textContent).toContain('New Test')
    expect(container.textContent).toContain('API Guide')
    expect(container.textContent).not.toContain('Settings')
  })

  it('does not render the dev view toggle for production auth modes', async () => {
    await act(async () => {
      root.render(
        <DevViewProvider>
          <Sidebar
            active="dashboard"
            onNavigate={vi.fn()}
            connectionState="connected"
            authStatus={{
              enabled: true,
              mode: 'trusted_headers',
              authenticated: false,
            }}
          />
        </DevViewProvider>,
      )
    })

    expect(container.textContent).not.toContain('Dev View')
  })
})
