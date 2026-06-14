// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DeploymentBanner from './DeploymentBanner'

const getVersionMock = vi.fn()

vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({
    getVersion: getVersionMock,
  }),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('DeploymentBanner', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    getVersionMock.mockReset()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    Object.defineProperty(window, 'go', { configurable: true, value: undefined })
  })

  it('labels hosted Kubernetes browser access and warns on wide auth bypass', async () => {
    getVersionMock.mockResolvedValue({
      version: 'dev',
      environment: 'kubernetes',
      deployment: {
        kind: 'kubernetes',
        label: 'Kubernetes',
        bind_scope: 'wide',
        auth_enforced: false,
        desktop_available: false,
      },
    })

    await act(async () => {
      root.render(<DeploymentBanner />)
    })

    expect(container.textContent).toContain('Browser · Hosted K8s')
    expect(container.textContent).toContain('Wide unauthenticated')
    expect(container.textContent).not.toContain('Desktop available')
  })

  it('opens explanatory help for the wide auth bypass warning', async () => {
    getVersionMock.mockResolvedValue({
      version: 'dev',
      environment: 'docker',
      deployment: {
        kind: 'docker',
        label: 'Docker',
        bind_scope: 'wide',
        auth_enforced: false,
        desktop_available: false,
      },
    })

    await act(async () => {
      root.render(<DeploymentBanner />)
    })

    const warning = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Wide unauthenticated'))
    expect(warning).not.toBeUndefined()
    expect(warning?.getAttribute('aria-expanded')).toBe('false')
    expect(container.textContent).not.toContain('Docker Compose publishes the host port on 127.0.0.1')

    await act(async () => {
      warning?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(warning?.getAttribute('aria-expanded')).toBe('true')
    expect(container.querySelector('[role="status"]')?.textContent).toContain('Why this appears')
    expect(container.textContent).toContain('Docker Compose publishes the host port on 127.0.0.1')
    expect(container.textContent).toContain('Configure OIDC or trusted-header auth')

    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })

    expect(warning?.getAttribute('aria-expanded')).toBe('false')
    expect(container.textContent).not.toContain('Docker Compose publishes the host port on 127.0.0.1')
  })

  it('shows hosted desktop promotion only when enabled', async () => {
    getVersionMock.mockResolvedValue({
      version: 'dev',
      environment: 'kubernetes',
      deployment: {
        kind: 'kubernetes',
        label: 'Kubernetes',
        bind_scope: 'wide',
        auth_enforced: true,
        desktop_available: true,
      },
    })

    await act(async () => {
      root.render(<DeploymentBanner />)
    })

    expect(container.textContent).toContain('Browser · Hosted K8s')
    expect(container.textContent).toContain('Desktop available')
  })

  it('labels the desktop local engine without wide warning', async () => {
    Object.defineProperty(window, 'go', {
      configurable: true,
      value: { main: { App: {} } },
    })
    getVersionMock.mockResolvedValue({
      version: 'dev',
      environment: 'local',
      deployment: {
        kind: 'desktop',
        label: 'Desktop',
        bind_scope: 'loopback',
        auth_enforced: false,
        desktop_available: false,
      },
    })

    await act(async () => {
      root.render(<DeploymentBanner />)
    })

    expect(container.textContent).toContain('Desktop · Local engine')
    expect(container.textContent).not.toContain('Wide unauthenticated')
    expect(container.textContent).not.toContain('Desktop available')
  })
})
