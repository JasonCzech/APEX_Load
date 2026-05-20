// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ApiGuidePage from './ApiGuidePage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ApiGuidePage', () => {
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
  })

  it('renders the API guide entry points and endpoint catalogue', async () => {
    await act(async () => {
      root.render(<ApiGuidePage />)
    })

    expect(container.textContent).toContain('API Guide')
    expect(container.textContent).toContain('http://<controller-host>:8080/api/v1')
    expect(container.textContent).toContain('/api/v1/auth/me')
    expect(container.textContent).toContain('/api/v1/scripts/import/loadrunner')
    expect(container.textContent).toContain('/api/v1/scenarios/validate-load-model')
    expect(container.textContent).toContain('MCP Server')
    expect(container.textContent).toContain('python3 mcp/server.py')
    expect(container.textContent).toContain('apexload://api-guide')
    expect(container.textContent).toContain('confirm=true')
    expect(container.textContent).toContain('API Keys')
    expect(container.textContent).toContain('Access & API Keys')
    expect(container.textContent).toContain('X-APEX Load-Dev-View')
    expect(container.textContent).toContain('project editor User behavior')

    const repositoryLink = container.querySelector('a[href*="API_USER_GUIDE.md"]')
    expect(repositoryLink?.textContent).toContain('Repository Guide')
    const mcpLink = container.querySelector('a[href*="mcp/README.md"]')
    expect(mcpLink?.textContent).toContain('MCP setup guide')
  })

  it('links API key guidance to the Access page when dashboard navigation is available', async () => {
    const onNavigate = vi.fn()
    await act(async () => {
      root.render(<ApiGuidePage onNavigate={onNavigate} />)
    })

    const accessButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Access & API Keys'))
    expect(accessButton).toBeDefined()
    if (!accessButton) return

    await act(async () => {
      accessButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('access')
  })
})
