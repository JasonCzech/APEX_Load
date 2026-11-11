// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AuthoringComponent, AuthoringTemplate } from '../types'
import ScriptCreateOptions, { type StartNewCategory } from './ScriptCreateOptions'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listAuthoringTemplates: vi.fn(),
  listAuthoringComponents: vi.fn(),
  listAuthoringCapabilities: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/TopbarContributionProvider', () => ({
  useHasTopbarContributionProvider: () => false,
}))

describe('ScriptCreateOptions concise row actions', () => {
  let container: HTMLDivElement
  let root: Root

  const sampleTemplate: AuthoringTemplate = {
    id: 'tpl-http-smoke',
    name: 'HTTP API Smoke & Latency Baseline',
    description: 'Baseline health and latency probe.',
    protocol: 'http',
    use_case: 'API smoke',
    tags: ['rest', 'smoke'],
    script: {
      name: 'HTTP API Smoke & Latency Baseline',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ type: 'http', name: 'Health check', method: 'GET', target: '/health' }],
    },
  }

  const whitespaceNameTemplate: AuthoringTemplate = {
    ...sampleTemplate,
    id: 'tpl-unnamed',
    name: '   ',
  }

  const sampleComponent: AuthoringComponent = {
    id: 'cmp-oauth-login',
    name: 'Enterprise OAuth2 Token Exchange Block',
    kind: 'auth_flow',
    summary: 'Exchanges client credentials for a bearer token.',
    protocols: ['http'],
    tags: ['oauth2'],
    actions: [{ type: 'http', name: 'Token exchange', method: 'POST', target: '/oauth/token' }],
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    apiMock.listAuthoringTemplates.mockResolvedValue({ templates: [sampleTemplate, whitespaceNameTemplate] })
    apiMock.listAuthoringComponents.mockResolvedValue({ components: [sampleComponent] })
    apiMock.listAuthoringCapabilities.mockResolvedValue({ import_surfaces: [] })
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  async function renderCreateOptions(category: StartNewCategory, overrides: Partial<Parameters<typeof ScriptCreateOptions>[0]> = {}) {
    const callbacks = {
      onBack: vi.fn(),
      onStartApiScript: vi.fn(),
      onStartSoapScript: vi.fn(),
      onStartJavaHttpScript: vi.fn(),
      onStartWebSocketScript: vi.fn(),
      onStartGrpcScript: vi.fn(),
      onStartDatabaseScript: vi.fn(),
      onStartNetworkScript: vi.fn(),
      onStartWebScript: vi.fn(),
      onOpenRecorder: vi.fn(),
      onStartJmsScript: vi.fn(),
      onStartKafkaScript: vi.fn(),
      onStartTemplateScript: vi.fn(),
      onComposeComponent: vi.fn(),
      onOpenImportSurface: vi.fn(),
      ...overrides,
    }
    await act(async () => {
      root.render(
        <ScriptCreateOptions
          workflow="start-new"
          category={category}
          {...callbacks}
        />,
      )
    })
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    return callbacks
  }

  it('renders concise nowrap Use buttons for starter templates with WCAG 2.5.3 aria-labels', async () => {
    const callbacks = await renderCreateOptions('templates')

    const panel = container.querySelector('[role="tabpanel"][aria-label="Starter templates"]')!
    const buttons = Array.from(panel.querySelectorAll<HTMLButtonElement>('tbody button'))
    expect(buttons).toHaveLength(2)

    expect(buttons[0].textContent).toBe('Use')
    expect(buttons[0].style.whiteSpace).toBe('nowrap')
    expect(buttons[0].getAttribute('aria-label')).toBe('Use HTTP API Smoke & Latency Baseline')
    expect(buttons[0].getAttribute('aria-label')!.startsWith(buttons[0].textContent!)).toBe(true)

    expect(buttons[1].textContent).toBe('Use')
    expect(buttons[1].getAttribute('aria-label')).toBe('Use template')

    await act(async () => {
      buttons[0].click()
    })
    expect(callbacks.onStartTemplateScript).toHaveBeenCalledWith(sampleTemplate.script)
  })

  it('renders concise nowrap Open buttons for blank editors with WCAG 2.5.3 aria-labels', async () => {
    const callbacks = await renderCreateOptions('editors')

    const panel = container.querySelector('[role="tabpanel"][aria-label="Blank & assisted editors"]')!
    const buttons = Array.from(panel.querySelectorAll<HTMLButtonElement>('tbody button'))
    expect(buttons.length).toBeGreaterThanOrEqual(2)

    for (const button of buttons) {
      expect(button.textContent).toBe('Open')
      expect(button.style.whiteSpace).toBe('nowrap')
      const ariaLabel = button.getAttribute('aria-label') || ''
      expect(ariaLabel.startsWith('Open ')).toBe(true)
    }

    const apiEditorButton = buttons.find(button => button.getAttribute('aria-label') === 'Open API Visual Editor')!
    expect(apiEditorButton).toBeDefined()
    await act(async () => {
      apiEditorButton.click()
    })
    expect(callbacks.onStartApiScript).toHaveBeenCalledTimes(1)
  })

  it('renders concise nowrap Compose buttons for reusable blocks with WCAG 2.5.3 aria-labels', async () => {
    const callbacks = await renderCreateOptions('blocks')

    const panel = container.querySelector('[role="tabpanel"][aria-label="Reusable blocks"]')!
    const buttons = Array.from(panel.querySelectorAll<HTMLButtonElement>('tbody button'))
    expect(buttons).toHaveLength(1)

    expect(buttons[0].textContent).toBe('Compose')
    expect(buttons[0].style.whiteSpace).toBe('nowrap')
    expect(buttons[0].getAttribute('aria-label')).toBe('Compose Enterprise OAuth2 Token Exchange Block')
    expect(buttons[0].getAttribute('aria-label')!.startsWith(buttons[0].textContent!)).toBe(true)

    await act(async () => {
      buttons[0].click()
    })
    expect(callbacks.onComposeComponent).toHaveBeenCalledWith(sampleComponent)
  })
})
