// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DevViewProvider } from '../hooks/DevViewProvider'
import { resolveApiResourceUrl } from '../hooks/apiClient'
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
        reload: vi.fn(),
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

  const openWorkspaceContext = async () => {
    if (container.querySelector('#sidebar-workspace-context')?.getAttribute('aria-hidden') === 'false') return
    await expandSidebar()
    const workspaceButton = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-workspace-context"]')
    expect(workspaceButton).not.toBeNull()
    await act(async () => {
      workspaceButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  // The sidebar starts as the collapsed icon rail; tests that assert on the
  // expanded content (labels, identity card, workspace names) open it first.
  const expandSidebar = async () => {
    if (!container.querySelector('.sidebar.collapsed')) return
    await act(async () => {
      container.querySelector<HTMLButtonElement>('.collapse-btn')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const openDevView = async () => {
    if (container.querySelector('#sidebar-dev-view-panel')) return
    await expandSidebar()
    const devToggle = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-dev-view-panel"]')
    expect(devToggle).not.toBeNull()
    await act(async () => {
      devToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    vi.useRealTimers()
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
    await expandSidebar()

    expect(container.textContent).toContain('Alice Admin')
    expect(container.querySelector('.sidebar-identity-meta')?.textContent).toBe('System Admin')
    const logoutLink = container.querySelector('.sidebar-identity-action') as HTMLAnchorElement | null
    expect(logoutLink?.getAttribute('href')).toBe(resolveApiResourceUrl('/logout'))
    expect(logoutLink?.textContent).toBe('Sign out')
  })

  it('shows the highest role relevant to the active workspace scope', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          activeDomain={{ id: 'domain-a', name: 'Domain A', created_at: '', updated_at: '' }}
          applicationRepository={{ id: 'repository-a', domain_id: 'domain-a', name: 'Repository A', created_at: '', updated_at: '' }}
          activeProject={{ id: 'project-a', domain_id: 'domain-a', name: 'Project A', created_at: '', updated_at: '' }}
          authStatus={{
            enabled: true,
            mode: 'local',
            authenticated: true,
            user: {
              subject: 'local:domainadmin',
              display_name: 'Local Domain Administrator',
              global_roles: ['viewer'],
              domain_roles: { 'domain-a': ['project_admin'] },
              application_repository_roles: { 'repository-a': ['editor'] },
              project_roles: { 'project-a': ['viewer'] },
            },
          }}
        />,
      )
    })
    await expandSidebar()

    expect(container.querySelector('.sidebar-identity-meta')?.textContent).toBe('Project Admin')
  })

  it('uses repository and project roles only when their scope is active', async () => {
    const authStatus = {
      enabled: true,
      mode: 'local' as const,
      authenticated: true,
      user: {
        subject: 'local:dev',
        display_name: 'Local Developer',
        global_roles: ['viewer'],
        application_repository_roles: { 'repository-a': ['editor'] },
        project_roles: { 'project-b': ['project_admin'] },
      },
    }
    const domain = { id: 'domain-a', name: 'Domain A', created_at: '', updated_at: '' }

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          activeDomain={domain}
          applicationRepository={{ id: 'repository-a', domain_id: 'domain-a', name: 'Repository A', created_at: '', updated_at: '' }}
          activeProject={{ id: 'project-a', domain_id: 'domain-a', name: 'Project A', created_at: '', updated_at: '' }}
          authStatus={authStatus}
        />,
      )
    })
    await expandSidebar()
    expect(container.querySelector('.sidebar-identity-meta')?.textContent).toBe('Editor')

    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          activeDomain={domain}
          applicationRepository={{ id: 'repository-b', domain_id: 'domain-a', name: 'Repository B', created_at: '', updated_at: '' }}
          activeProject={{ id: 'project-b', domain_id: 'domain-a', name: 'Project B', created_at: '', updated_at: '' }}
          authStatus={authStatus}
        />,
      )
    })
    expect(container.querySelector('.sidebar-identity-meta')?.textContent).toBe('Project Admin')
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
    await expandSidebar()

    const logoutButton = container.querySelector('.sidebar-identity-action-button') as HTMLButtonElement | null
    expect(logoutButton?.textContent).toBe('Sign out')
    expect(logoutButton).toBeTruthy()
    if (!logoutButton) {
      return
    }

    await act(async () => {
      logoutButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(authApiMock.logout).toHaveBeenCalledWith('csrf-token')
    expect(window.location.assign).toHaveBeenCalledWith(resolveApiResourceUrl('/'))
  })

  it('posts Local Test Login logout and reloads authorization providers', async () => {
    authApiMock.logout.mockResolvedValue({ redirect_url: '/' })
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'local',
            logout_url: '/api/v1/auth/logout',
            logout_csrf_token: 'local-csrf-token',
            authenticated: true,
            user: { subject: 'local:dev', display_name: 'Local Developer', global_roles: ['viewer'] },
          }}
        />,
      )
    })
    await expandSidebar()
    expect(container.textContent).toContain('Local Developer')
    expect(container.textContent).not.toContain('Dev View')
    const logoutButton = container.querySelector('.sidebar-identity-action-button') as HTMLButtonElement
    await act(async () => {
      logoutButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(authApiMock.logout).toHaveBeenCalledWith('local-csrf-token')
    expect(window.location.reload).toHaveBeenCalledOnce()
  })

  it('restores avatar focus on collapse and focuses the account card when logout is pending', async () => {
    let rejectLogout: (error: Error) => void = () => {}
    authApiMock.logout.mockImplementationOnce(() => new Promise((_, reject) => { rejectLogout = reject }))
    await act(async () => {
      root.render(<Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected" authStatus={{
        enabled: true, mode: 'local', logout_url: '/api/v1/auth/logout', logout_csrf_token: 'local-csrf-token',
        authenticated: true, user: { subject: 'local:dev', display_name: 'Local Developer', global_roles: ['viewer'] },
      }} />)
    })
    await act(async () => container.querySelector<HTMLButtonElement>('.sidebar-rail-avatar')!.click())
    const logout = container.querySelector<HTMLButtonElement>('.sidebar-identity-action-button')!
    expect(document.activeElement).toBe(logout)
    await act(async () => logout.click())
    expect(authApiMock.logout).toHaveBeenCalledWith('local-csrf-token')
    expect(container.querySelector<HTMLButtonElement>('.sidebar-identity-action-button')!.disabled).toBe(true)
    await act(async () => container.querySelector<HTMLButtonElement>('.collapse-btn')!.click())
    const avatar = container.querySelector<HTMLButtonElement>('.sidebar-rail-avatar')!
    expect(document.activeElement).toBe(avatar)
    await act(async () => avatar.click())
    expect(document.activeElement).toBe(container.querySelector('#sidebar-identity'))
    expect(container.querySelector<HTMLButtonElement>('.sidebar-identity-action-button')!.disabled).toBe(true)
    await act(async () => { rejectLogout(new Error('Logout failed')); await Promise.resolve() })
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('Logout failed')
    expect(container.querySelector<HTMLButtonElement>('.sidebar-identity-action-button')!.disabled).toBe(false)
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
    await expandSidebar()

    expect(container.textContent).toContain('Alice Admin')
    expect(container.textContent).toContain('System Admin')
    expect(container.textContent).toContain('Session expires in 4 min')
  })

  it('shows imminent Local Test Login session expiry', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'local',
            authenticated: true,
            session_status: 'active',
            session_ttl_seconds: 89,
            user: {
              subject: 'local:user',
              display_name: 'Local Viewer',
              global_roles: ['viewer'],
            },
          }}
        />,
      )
    })
    await expandSidebar()

    expect(container.textContent).toContain('Session expires in 2 min')
  })

  it('counts down Local Test Login session expiry from its absolute deadline', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-11T12:00:00Z'))
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'local',
            authenticated: true,
            session_status: 'active',
            session_expires_at: '2026-08-11T12:04:00Z',
            session_ttl_seconds: 240,
            user: {
              subject: 'local:user',
              display_name: 'Local Viewer',
              global_roles: ['viewer'],
            },
          }}
        />,
      )
    })
    await expandSidebar()

    expect(container.textContent).toContain('Session expires in 4 min')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })
    expect(container.textContent).toContain('Session expires in 3 min')
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
    await expandSidebar()

    expect(container.textContent).toContain('Checkout')

    await openWorkspaceContext()

    const projectButton = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-project-options"]')
    expect(projectButton).toBeTruthy()
    if (!projectButton) {
      return
    }

    await act(async () => {
      projectButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const searchButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Search')
    expect(searchButton).toBeTruthy()
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

    await openWorkspaceContext()
    const projectButton = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-project-options"]')
    expect(projectButton).toBeTruthy()
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

    await openWorkspaceContext()
    const reopenedProjectButton = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-project-options"]')
    await act(async () => {
      reopenedProjectButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
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

    await openWorkspaceContext()
    const domainButton = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-domain-options"]')
    const projectButton = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-project-options"]')
    expect(domainButton).toBeTruthy()
    expect(projectButton).toBeTruthy()
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
    await expandSidebar()

    const guideButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Help & documentation'))
    expect(guideButton).toBeTruthy()
    if (!guideButton) {
      return
    }

    await act(async () => {
      guideButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('docs')
  })

  it('omits redundant fleet and schedules destinations from the sidebar', async () => {
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
    expect(labels).not.toContain('Fleet')
    expect(labels).not.toContain('Schedules')
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
    await expandSidebar()

    const labels = Array.from(container.querySelectorAll('.sidebar-nav .nav-label')).map(label => label.textContent)

    expect(labels).toEqual(['New run', 'Dashboard', 'Command Center', 'Results', 'Monitoring', 'Activity', 'Scripts', 'Scenarios', 'Shared assets', 'Repository', 'Settings', 'Help & documentation'])
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
    await expandSidebar()

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

    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
    expect(container.querySelector('.sidebar.collapsed .sidebar-footer .sidebar-status')).not.toBeNull()
    expect(container.querySelector('.sidebar.collapsed .sidebar-footer .build-badge')).not.toBeNull()

    await expandSidebar()

    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
    expect(container.querySelector('.sidebar-footer .sidebar-status')).not.toBeNull()
    expect(container.querySelector('.sidebar-footer .build-badge')).not.toBeNull()
  })

  it('opens the collapsed rail from a click on its empty space but leaves its controls alone', async () => {
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
    const click = async (target: Element | null, init: MouseEventInit = {}) => {
      await act(async () => {
        target?.dispatchEvent(new MouseEvent('click', { bubbles: true, ...init }))
      })
    }

    // An icon keeps its own behavior: it navigates and the rail stays collapsed.
    await click(container.querySelector('.nav-item'))
    expect(onNavigate).toHaveBeenCalledTimes(1)
    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()

    // Only a primary click on empty space opens it.
    await click(container.querySelector('.sidebar-nav'), { button: 1 })
    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
    await click(container.querySelector('.sidebar-nav'))
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()

    // Occupied, noninteractive content does not toggle it.
    await click(container.querySelector('.sidebar-nav-group-label'))
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
    await click(container.querySelector('.sidebar-status'))
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()

    // The footer's gap is also unused space.
    await click(container.querySelector('.sidebar-footer-row'))
    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
    await click(container.querySelector('.sidebar-nav'))
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()

    // Empty space also closes the expanded sidebar.
    await click(container.querySelector('.sidebar-nav'))
    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
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

    expect(container.textContent).toContain('Version Info')
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
    await expandSidebar()

    const accessButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Access'))
    expect(accessButton).toBeTruthy()
    if (!accessButton) {
      return
    }

    await act(async () => {
      accessButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('access')
  })

  it('opens build details and copies version info', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    systemApiMock.getVersion.mockResolvedValueOnce({
      build_time: '2026-06-13T14:00:00Z',
      commit: 'abc1234',
      environment: 'kubernetes',
      revision: 'def5678',
      go_version: 'go1.26',
      version: 'dev',
      display_version: 'v1.4.2-dev.20260613+abc1234',
    })

    await act(async () => {
      root.render(<Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected" />)
      await Promise.resolve()
    })
    const badge = container.querySelector<HTMLButtonElement>('.build-badge-pill')
    expect(badge).toBeTruthy()
    if (!badge) return
    await act(async () => { badge.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const copyButton = container.querySelector<HTMLButtonElement>('.build-badge-copy')
    expect(copyButton).toBeTruthy()
    if (!copyButton) return
    await act(async () => { copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Version: v1.4.2-dev.20260613+abc1234'))
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Environment: kubernetes'))
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Commit: abc1234'))
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Revision: def5678'))
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Runtime: go1.26'))
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
    await expandSidebar()

    expect(container.textContent).toContain('Preview permissions')
    await openDevView()
    expect(container.textContent).toContain('Dev View')
    expect(container.textContent).toContain('Admin')
    expect(container.textContent).toContain('Editor')
    expect(container.textContent).toContain('New run')
    expect(container.textContent).toContain('Help & documentation')
    expect(container.textContent).toContain('Settings')
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button')).some(button => button.textContent?.trim() === 'Sign out')).toBe(true)

    const userButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Editor')
    expect(userButton).toBeTruthy()
    if (!userButton) {
      return
    }

    await act(async () => {
      userButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(window.localStorage.getItem('apexload.devViewMode')).toBe('user')
    expect(container.textContent).toContain('Editor preview')
    expect(container.textContent).toContain('New run')
    expect(container.textContent).toContain('Help & documentation')
    expect(container.textContent).toContain('Settings')
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button')).some(button => button.textContent?.trim() === 'Sign out')).toBe(true)
  })

  it('signs the dev user preview out to the admin view', async () => {
    window.localStorage.setItem('apexload.devViewMode', 'user')
    const onDevPreviewLogout = vi.fn()

    await act(async () => {
      root.render(
        <DevViewProvider>
          <Sidebar
            active="dashboard"
            onNavigate={vi.fn()}
            connectionState="connected"
            onDevPreviewLogout={onDevPreviewLogout}
            authStatus={{
              enabled: false,
              mode: 'dev',
              authenticated: true,
              dev_view_mode: 'user',
              simulated: true,
              user: {
                subject: 'dev-user',
                display_name: 'Dev User Preview',
                global_roles: ['viewer'],
                project_roles: { 'project-a': ['editor'] },
              },
            }}
          />
        </DevViewProvider>,
      )
    })

    await openDevView()
    expect(container.textContent).toContain('Dev User Preview')
    const signOutButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Sign out')
    expect(signOutButton).toBeTruthy()
    if (!signOutButton) return

    await act(async () => {
      signOutButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onDevPreviewLogout).toHaveBeenCalledOnce()
    expect(window.localStorage.getItem('apexload.devViewMode')).toBe('user')
    expect(authApiMock.logout).not.toHaveBeenCalled()
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

  it('keeps navigation names and current-page state available when labels are visually collapsed', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="new-test"
          onNavigate={vi.fn()}
          connectionState="connected"
        />,
      )
    })

    const navItems = Array.from(container.querySelectorAll<HTMLButtonElement>('.nav-item'))
    expect(navItems.length).toBeGreaterThan(0)
    expect(navItems.every(item => Boolean(item.getAttribute('aria-label')))).toBe(true)
    expect(navItems.every(item => !item.hasAttribute('title'))).toBe(true)
    await act(async () => navItems[0].focus())
    expect(document.querySelector('[role="tooltip"]')?.textContent).toBe(navItems[0].getAttribute('aria-label'))
    expect(navItems.find(item => item.getAttribute('aria-label') === 'New run')?.getAttribute('aria-current')).toBe('page')
    expect(container.querySelector<HTMLButtonElement>('.sidebar-brand')?.getAttribute('aria-label')).toBe('APEX Load dashboard')
  })

  const mobilityDomain = { id: 'mobility', name: 'Mobility', created_at: '', updated_at: '' }
  const mobilityProject = { id: 'project-1', domain_id: 'mobility', name: 'Mobility Web', created_at: '', updated_at: '' }
  const mobilityApplication = { id: 'application-1', project_id: 'project-1', name: 'Driver Portal', created_at: '', updated_at: '' }
  const mobilityRelease = { id: 'release-1', application_id: 'application-1', name: '2608', created_at: '', updated_at: '' }

  const openMenu = async (selector: string) => {
    await openWorkspaceContext()
    const selectorButton = container.querySelector<HTMLButtonElement>(selector)
    expect(selectorButton).not.toBeNull()
    await act(async () => {
      selectorButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const menuButton = (menuId: string, label: string) => Array.from(
    container.querySelectorAll<HTMLButtonElement>(`#${menuId} button`),
  ).find(button => button.textContent === label)

  it('uses an opaque theme surface behind sidebar dropdown items', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync('src/components/Sidebar.css', 'utf8') as string
    const menuRule = css.match(/\.project-menu \{([\s\S]*?)\n\}/)?.[1] || ''

    expect(menuRule).toContain('background: var(--dropdown-menu-bg);')
    expect(menuRule).not.toContain('var(--bg-card)')
    expect(menuRule).not.toContain('transparent')
  })

  it('keeps the macOS traffic lights clear of expanded and compact branding at every dashboard zoom', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync('src/components/Sidebar.css', 'utf8') as string
    const expandedRule = css.match(/body\.is-desktop\.is-mac \.sidebar:not\(\.collapsed\) \.sidebar-brand \{([\s\S]*?)\n\}/)?.[1] || ''
    const collapsedRule = css.match(/body\.is-desktop\.is-mac \.sidebar\.collapsed \.sidebar-brand \{([\s\S]*?)\n\}/)?.[1] || ''
    const compactRule = css.match(/body\.is-desktop\.is-mac \.sidebar:not\(\.collapsed\) \.sidebar-brand,\n {2}body\.is-desktop\.is-mac \.sidebar\.collapsed \.sidebar-brand \{([\s\S]*?)\n {2}\}/)?.[1] || ''

    expect(expandedRule).toContain('padding-left: calc(80px / var(--dashboard-zoom, 1));')
    expect(collapsedRule).toContain('padding: calc(44px / var(--dashboard-zoom, 1)) 0 16px;')
    expect(compactRule).toContain('padding: calc(44px / var(--dashboard-zoom, 1)) 0 16px;')
  })

  it('renders Release as a separate dropdown directly below Application', async () => {
    const onReleaseChange = vi.fn()
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          projects={[mobilityProject]}
          activeProject={mobilityProject}
          applications={[mobilityApplication]}
          activeApplication={mobilityApplication}
          releases={[mobilityRelease]}
          activeRelease={mobilityRelease}
          onReleaseChange={onReleaseChange}
        />,
      )
    })

    await openWorkspaceContext()
    const applicationSelector = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-application-options"]')
    const releaseSelector = container.querySelector<HTMLButtonElement>('[aria-controls="sidebar-release-options"]')
    expect(applicationSelector?.textContent).toContain('Driver Portal')
    expect(applicationSelector?.textContent).not.toContain('2608')
    expect(releaseSelector?.textContent).toContain('2608')
    expect(applicationSelector?.closest('.sidebar-project')?.nextElementSibling).toBe(releaseSelector?.closest('.sidebar-project'))

    await openMenu('[aria-controls="sidebar-application-options"]')
    expect(menuButton('sidebar-application-options', 'Driver Portal')).toBeDefined()
    expect(menuButton('sidebar-application-options', '2608')).toBeUndefined()

    await openMenu('[aria-controls="sidebar-release-options"]')
    expect(menuButton('sidebar-release-options', '2608')).toBeDefined()
    expect(menuButton('sidebar-release-options', 'Driver Portal')).toBeUndefined()

    await act(async () => {
      menuButton('sidebar-release-options', '2608')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onReleaseChange).toHaveBeenCalledWith(mobilityRelease)
  })

  it('creates a release from the Release dropdown', async () => {
    const onCreateRelease = vi.fn().mockResolvedValue(mobilityRelease)
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          projects={[mobilityProject]}
          activeProject={mobilityProject}
          applications={[mobilityApplication]}
          activeApplication={mobilityApplication}
          releases={[]}
          onCreateRelease={onCreateRelease}
          canCreateReleases
        />,
      )
    })

    await openMenu('[aria-controls="sidebar-release-options"]')
    await act(async () => {
      menuButton('sidebar-release-options', 'New Release')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('New Release')

    const input = container.querySelector<HTMLInputElement>('.project-modal input')
    expect(input).not.toBeNull()
    expect(input?.placeholder).toBe('Release name')
    await act(async () => {
      if (input) {
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
        setter?.call(input, '2609')
        input.dispatchEvent(new Event('input', { bubbles: true }))
      }
    })
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('.project-modal button'))
      .find(button => button.textContent === 'Create release')
    await act(async () => {
      create?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onCreateRelease).toHaveBeenCalledWith('2609')
  })

  it('hides release creation when the effective capability is denied', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          projects={[mobilityProject]}
          activeProject={mobilityProject}
          applications={[mobilityApplication]}
          activeApplication={mobilityApplication}
          releases={[]}
          onCreateRelease={vi.fn().mockResolvedValue(mobilityRelease)}
          canCreateReleases={false}
        />,
      )
    })

    await openMenu('[aria-controls="sidebar-release-options"]')
    expect(menuButton('sidebar-release-options', 'New Release')).toBeUndefined()
  })

  it('explains an empty domain instead of offering only Open Repository', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[mobilityDomain]}
          activeDomain={mobilityDomain}
          projects={[]}
          activeProject={null}
          onCreateProject={vi.fn()}
          canCreateProjects
        />,
      )
    })

    await openMenu('[aria-controls="sidebar-project-options"]')
    expect(container.querySelector('#sidebar-project-options')?.textContent).toContain('No projects in Mobility')
    expect(menuButton('sidebar-project-options', 'New Project')).toBeDefined()

    await openMenu('[aria-controls="sidebar-application-options"]')
    expect(container.querySelector('#sidebar-application-options')?.textContent).toContain('Select a project first')
    expect(menuButton('sidebar-application-options', 'New Application')).toBeUndefined()
  })

  it('creates a project from the sidebar menu', async () => {
    const onCreateProject = vi.fn().mockResolvedValue(mobilityProject)
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[mobilityDomain]}
          activeDomain={mobilityDomain}
          projects={[]}
          onCreateProject={onCreateProject}
          canCreateProjects
        />,
      )
    })

    await openMenu('[aria-controls="sidebar-project-options"]')
    await act(async () => {
      menuButton('sidebar-project-options', 'New Project')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const nameInput = container.querySelector<HTMLInputElement>('.project-modal-field input')
    expect(nameInput).toBeTruthy()
    if (!nameInput) return
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(nameInput, 'Mobility Web')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      container.querySelector<HTMLFormElement>('form.project-modal')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })

    expect(onCreateProject).toHaveBeenCalledWith('Mobility Web')
  })

  it('hides project creation when the session cannot manage projects', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[mobilityDomain]}
          activeDomain={mobilityDomain}
          projects={[]}
          onCreateProject={vi.fn()}
        />,
      )
    })

    await openMenu('[aria-controls="sidebar-project-options"]')
    expect(menuButton('sidebar-project-options', 'New Project')).toBeUndefined()
    expect(container.querySelector<HTMLButtonElement>('.sidebar-nav [aria-label="Repository"]')).not.toBeNull()
  })

  it('offers application creation once a project is active, without the project-management permission', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[mobilityDomain]}
          activeDomain={mobilityDomain}
          projects={[mobilityProject]}
          activeProject={mobilityProject}
          applications={[]}
          onCreateApplication={vi.fn()}
        />,
      )
    })

    await openMenu('[aria-controls="sidebar-application-options"]')
    expect(container.querySelector('#sidebar-application-options')?.textContent).toContain('No applications in Mobility Web')
    // Application creation is a wider server-side permission than project
    // management, so it is not gated on canCreateProjects.
    expect(menuButton('sidebar-application-options', 'New Application')).toBeDefined()
  })

  it('reveals context controls after expanding the rail', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[mobilityDomain]}
          activeDomain={mobilityDomain}
          projects={[mobilityProject]}
          activeProject={mobilityProject}
        />,
      )
    })

    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()

    await openWorkspaceContext()
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
    expect(container.querySelector('#sidebar-workspace-context')).not.toBeNull()
    await openMenu('[aria-controls="sidebar-project-options"]')
    expect(container.querySelector('#sidebar-project-options')).not.toBeNull()
  })

  it('hides Open Repository in every scope menu during dev user preview', async () => {
    await act(async () => {
      root.render(
        <DevViewProvider>
          <Sidebar
            active="dashboard"
            onNavigate={vi.fn()}
            connectionState="connected"
            authStatus={{ enabled: false, mode: 'dev', authenticated: false }}
            domains={[mobilityDomain]}
            activeDomain={mobilityDomain}
            projects={[mobilityProject]}
            activeProject={mobilityProject}
            applications={[mobilityApplication]}
            activeApplication={mobilityApplication}
            onCreateProject={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(mobilityRelease)}
            canCreateProjects
            canCreateReleases
          />
        </DevViewProvider>,
      )
    })

    await openDevView()
    await act(async () => {
      Array.from(container.querySelectorAll('button'))
        .find(button => button.textContent === 'Editor')
        ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await openMenu('[aria-controls="sidebar-domain-options"]')
    expect(menuButton('sidebar-domain-options', 'Open Repository')).toBeUndefined()

    await openMenu('[aria-controls="sidebar-project-options"]')
    expect(menuButton('sidebar-project-options', 'Open Repository')).toBeUndefined()
    expect(menuButton('sidebar-project-options', 'New Project')).toBeUndefined()

    await openMenu('[aria-controls="sidebar-application-options"]')
    expect(menuButton('sidebar-application-options', 'Open Repository')).toBeUndefined()
    expect(menuButton('sidebar-application-options', 'New Application')).toBeUndefined()

    await openMenu('[aria-controls="sidebar-release-options"]')
    expect(menuButton('sidebar-release-options', 'Open Repository')).toBeUndefined()
    expect(menuButton('sidebar-release-options', 'New Release')).toBeDefined()
  })
  const navButton = (label: string) => container.querySelector<HTMLButtonElement>(`.sidebar-nav button[aria-label="${label}"]`)
  const tooltipText = () => document.querySelector('[role="tooltip"]')?.textContent ?? null

  it('starts as the collapsed icon rail and reserves space when expanded', async () => {
    await act(async () => {
      root.render(<Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected" />)
    })

    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
    expect(container.querySelector('.sidebar-nav .nav-label')).toBeNull()
    expect(container.querySelector('.sidebar-shell')?.classList.contains('overlay-open')).toBe(false)

    await expandSidebar()

    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
    expect(container.querySelector('.sidebar-shell')?.classList.contains('overlay-open')).toBe(true)
    // The shell owns the expanded layout slot, shifting the adjacent main content right.
    expect(container.querySelector('.sidebar-shell > aside.sidebar')).not.toBeNull()
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync('src/components/Sidebar.css', 'utf8') as string
    const expandedRule = css.match(/\.sidebar-shell\.overlay-open \{([\s\S]*?)\n\}/)?.[1] || ''
    const mobileRule = css.slice(css.indexOf('@media (max-width: 768px)'))
    expect(expandedRule).toContain('flex-basis: 240px;')
    expect(expandedRule).toContain('width: 240px;')
    expect(mobileRule).toContain('.sidebar-shell.overlay-open')
    expect(mobileRule).toContain('flex-basis: 64px;')
  })

  it('keeps the sidebar expanded on an outside click and Escape, but collapses on navigation', async () => {
    const onNavigate = vi.fn()
    await act(async () => {
      root.render(<Sidebar active="dashboard" onNavigate={onNavigate} connectionState="connected" />)
    })

    await expandSidebar()
    const underneath = document.createElement('button')
    const activated = vi.fn()
    underneath.addEventListener('click', activated)
    document.body.appendChild(underneath)
    await act(async () => {
      underneath.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))
      underneath.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
    expect(activated).toHaveBeenCalledTimes(1)
    underneath.remove()

    await expandSidebar()
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()

    await act(async () => {
      navButton('Scripts')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onNavigate).toHaveBeenCalledWith('scripts')
    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
  })

  it('closes an open workspace menu on Escape without collapsing the sidebar', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[mobilityDomain]}
          activeDomain={mobilityDomain}
          projects={[mobilityProject]}
          activeProject={mobilityProject}
        />,
      )
    })

    await openMenu('[aria-controls="sidebar-project-options"]')
    expect(container.querySelector('#sidebar-project-options')).not.toBeNull()

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    expect(container.querySelector('#sidebar-project-options')).toBeNull()
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
  })

  it('closes workspace controls when the chevron is activated by keyboard', async () => {
    await act(async () => {
      root.render(<Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected" />)
    })
    await openWorkspaceContext()
    expect(container.querySelector('.sidebar-workspace.open')).not.toBeNull()
    await act(async () => {
      // Keyboard activation produces a click without a preceding pointerdown.
      container.querySelector<HTMLButtonElement>('.collapse-btn')?.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 0 }))
    })
    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
    expect(container.querySelector('.sidebar-workspace.open')).toBeNull()
    expect(container.querySelector('#sidebar-workspace-context')?.getAttribute('aria-hidden')).toBe('true')
  })

  it('does not focus stale simulated identity while switching preview back to admin', async () => {
    window.localStorage.setItem('apexload.devViewMode', 'user')
    await act(async () => {
      root.render(<DevViewProvider><Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected" authStatus={{
        enabled: false, mode: 'dev', authenticated: true, simulated: true, dev_view_mode: 'user',
        user: { subject: 'dev-user', display_name: 'Dev Editor Preview', global_roles: ['viewer'] },
      }} /></DevViewProvider>)
    })
    await openDevView()
    expect(container.querySelector('.sidebar-identity-name')?.textContent).toBe('Dev Editor Preview')
    const admin = Array.from(container.querySelectorAll<HTMLButtonElement>('.dev-view-segmented button')).find(button => button.textContent === 'Admin')!
    await act(async () => admin.click())
    expect(window.localStorage.getItem('apexload.devViewMode')).toBe('admin')
    // Keep the old authStatus prop in place to model a delayed server response.
    expect(container.querySelector('.sidebar-identity')).toBeNull()
    await act(async () => container.querySelector<HTMLButtonElement>('.collapse-btn')!.click())
    expect(container.querySelector('.sidebar-rail-avatar')).toBeNull()
    expect(document.activeElement).toBe(container.querySelector('.sidebar-brand'))
  })

  it('moves focus between narrow collapsed controls only when the replaced control held focus', async () => {
    const originalWidth = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 760 })
    const render = (authenticated: boolean) => root.render(<Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected" authStatus={{
      enabled: true, mode: 'local', authenticated,
      user: authenticated ? { subject: 'local:user', display_name: 'Local Viewer', global_roles: ['viewer'] } : undefined,
    }} />)
    try {
      await act(async () => render(false))
      const expand = container.querySelector<HTMLButtonElement>('.collapse-btn')!
      await act(async () => expand.focus())
      await act(async () => render(true))
      const avatar = container.querySelector<HTMLButtonElement>('.sidebar-rail-avatar')!
      expect(document.activeElement).toBe(avatar)
      await act(async () => render(false))
      expect(document.activeElement).toBe(expand)

      const brand = container.querySelector<HTMLButtonElement>('.sidebar-brand')!
      await act(async () => brand.focus())
      await act(async () => render(true))
      expect(document.activeElement).toBe(brand)
      await act(async () => render(false))
      expect(document.activeElement).toBe(brand)

      await act(async () => { expand.focus(); expand.blur() })
      await act(async () => render(true))
      expect(document.activeElement).not.toBe(container.querySelector('.sidebar-rail-avatar'))
      await act(async () => render(false))
      Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 1280 })
      await act(async () => expand.focus())
      await act(async () => render(true))
      expect(document.activeElement).toBe(expand)
    } finally {
      Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: originalWidth })
    }
  })

  it('restores focus when a desktop preview identity disappears, without moving unrelated focus', async () => {
    const originalWidth = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 1280 })
    window.localStorage.setItem('apexload.devViewMode', 'user')
    const switchMode = async (mode: 'admin' | 'user') => act(async () => {
      window.localStorage.setItem('apexload.devViewMode', mode)
      window.dispatchEvent(new Event('apexload:dev-view-mode-changed'))
    })
    try {
      await act(async () => root.render(<DevViewProvider><Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected" authStatus={{
        enabled: false, mode: 'dev', authenticated: true, simulated: true, dev_view_mode: 'user', logout_url: '/logout',
        user: { subject: 'dev-user', display_name: 'Dev Editor Preview', global_roles: ['viewer'] },
      }} /></DevViewProvider>))
      await act(async () => container.querySelector<HTMLButtonElement>('.sidebar-rail-avatar')!.focus())
      await switchMode('admin')
      expect(document.activeElement).toBe(container.querySelector('.collapse-btn'))

      await switchMode('user')
      await act(async () => container.querySelector<HTMLButtonElement>('.sidebar-rail-avatar')!.click())
      expect(document.activeElement).toBe(container.querySelector('.sidebar-identity-action'))
      await switchMode('admin')
      expect(document.activeElement).toBe(container.querySelector('.collapse-btn'))

      await switchMode('user')
      await act(async () => container.querySelector<HTMLElement>('#sidebar-identity')!.focus())
      await switchMode('admin')
      expect(document.activeElement).toBe(container.querySelector('.collapse-btn'))

      await switchMode('user')
      const brand = container.querySelector<HTMLButtonElement>('.sidebar-brand')!
      await act(async () => brand.focus())
      await switchMode('admin')
      expect(document.activeElement).toBe(brand)
    } finally {
      Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: originalWidth })
    }
  })

  it('expands and returns focus to the brand after collapsing without an authenticated avatar', async () => {
    await act(async () => {
      root.render(<Sidebar active="dashboard" onNavigate={vi.fn()} connectionState="connected"
        authStatus={{ enabled: false, mode: 'dev', authenticated: false }} />)
    })
    expect(container.querySelector('.sidebar-rail-avatar')).toBeNull()
    const expand = container.querySelector<HTMLButtonElement>('.collapse-btn')!
    expect(expand.getAttribute('aria-label')).toBe('Expand sidebar')
    await act(async () => { expand.focus(); expand.click() })
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
    const collapse = container.querySelector<HTMLButtonElement>('.collapse-btn')!
    await act(async () => { collapse.focus(); collapse.click() })
    expect(container.querySelector('.sidebar.collapsed')).not.toBeNull()
    expect(document.activeElement).toBe(container.querySelector('.sidebar-brand'))
  })

  it('shows hover popups only while the sidebar is collapsed, including the workspace pickers', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          domains={[mobilityDomain]}
          activeDomain={mobilityDomain}
          projects={[mobilityProject]}
          activeProject={mobilityProject}
        />,
      )
    })

    await act(async () => navButton('Scripts')?.focus())
    expect(tooltipText()).toBe('Scripts')
    expect(container.querySelector<HTMLButtonElement>('.workspace-summary')?.disabled).toBe(true)
    await act(async () => container.querySelector<HTMLButtonElement>('.collapse-btn')?.focus())
    expect(tooltipText()).toBe('Expand sidebar')

    await expandSidebar()
    await act(async () => navButton('Scripts')?.focus())
    expect(tooltipText()).toBeNull()
    await act(async () => container.querySelector<HTMLButtonElement>('.collapse-btn')?.focus())
    expect(tooltipText()).toBeNull()

    await openWorkspaceContext()
    for (const selector of ['sidebar-domain-options', 'sidebar-project-options', 'sidebar-application-options', 'sidebar-release-options']) {
      await act(async () => container.querySelector<HTMLButtonElement>(`[aria-controls="${selector}"]`)?.focus())
      expect(tooltipText(), selector).toBeNull()
    }
  })

  it('keeps the signed-in user and an imminent session expiry visible on the collapsed rail', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'local',
            authenticated: true,
            session_status: 'active',
            session_ttl_seconds: 89,
            user: {
              subject: 'local:user',
              display_name: 'Local Viewer',
              global_roles: ['viewer'],
            },
          }}
        />,
      )
    })

    const avatar = container.querySelector<HTMLButtonElement>('.sidebar.collapsed .sidebar-rail-avatar')
    expect(avatar).not.toBeNull()
    expect(avatar?.classList.contains('session-warning')).toBe(true)
    expect(avatar?.querySelector('.sidebar-rail-avatar-alert')).not.toBeNull()
    expect(avatar?.getAttribute('aria-label')).toContain('Local Viewer')
    expect(avatar?.getAttribute('aria-label')).toContain('Session expires in 2 min')
    expect(avatar?.disabled).toBe(false)
    expect(avatar?.getAttribute('aria-label')).toContain('Open account controls')
    await act(async () => avatar?.click())
    expect(container.querySelector('.sidebar.collapsed')).toBeNull()
    expect(container.querySelector('.sidebar-rail-avatar')).toBeNull()
    expect(document.activeElement).toBe(container.querySelector('#sidebar-identity'))
    expect(container.querySelector('.sidebar-identity-session')?.textContent).toBe('Session expires in 2 min')
  })

  it('shows a calm rail avatar when the session is not about to expire', async () => {
    await act(async () => {
      root.render(
        <Sidebar
          active="dashboard"
          onNavigate={vi.fn()}
          connectionState="connected"
          authStatus={{
            enabled: true,
            mode: 'trusted_headers',
            authenticated: true,
            user: { subject: 'alice', display_name: 'Alice Admin', global_roles: ['system_admin'] },
          }}
        />,
      )
    })

    const avatar = container.querySelector<HTMLButtonElement>('.sidebar-rail-avatar')
    expect(avatar?.textContent).toBe('AA')
    expect(avatar?.classList.contains('session-warning')).toBe(false)
    expect(avatar?.querySelector('.sidebar-rail-avatar-alert')).toBeNull()
  })
})
