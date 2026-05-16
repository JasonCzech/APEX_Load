// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import Sidebar from './Sidebar'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('Sidebar', () => {
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