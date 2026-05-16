// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { formatSnapshotRequestSection, formatSnapshotResponseSection } from '../utils/snapshotFormatUtils'
import { formatSOAPFaultSummary } from './soapFaultUtils'
import ScriptEditor from './ScriptEditor'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getScript: vi.fn(),
  createScript: vi.fn(),
  updateScript: vi.fn(),
  validateScript: vi.fn(),
  replayDraftScript: vi.fn(),
  replayScript: vi.fn(),
  startDraftBrowserReplaySession: vi.fn(),
  startBrowserReplaySession: vi.fn(),
  pauseBrowserReplaySession: vi.fn(),
  resumeBrowserReplaySession: vi.fn(),
  stopBrowserReplaySession: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: vi.fn(),
}))

describe('ScriptEditor', () => {
  let container: HTMLDivElement
  let root: Root
  const writeText = vi.fn()

  const initialScript: ScriptDefinition = {
    name: 'Checkout Flow',
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: 'https://api.example.com',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 1, auto_transactions: 'none', block_scheduling: 'sequential', action_sequence: ['init', 'action', 'teardown'] },
      error_handling: { continue_on_error: false },
      log: { level: 'standard', extended_options: ['parameter_substitution'] },
      http: { proxy_mode: 'custom', proxy: { url: 'http://proxy.example.test:8080' } },
      browser: { cache_enabled: true, download_resources: 'recorded' },
      web_runtime: {
        additional_attributes: { env: 'perf' },
        download_filters: { mode: 'block_list', hosts: ['metrics.example.test'] },
        content_checks: [{ text: 'Access Denied', scope: 'any_response', fail_if_found: true }],
        network_simulation: { mode: 'preset', preset: 'dsl', latency_ms: 50 },
        streaming: { mode: 'adaptive', buffer_size_seconds: 30, bandwidth_estimation: 'rolling' },
        data_formatting: { auto_detect: true, chain: [{ type: 'base64' }] },
      },
    },
    actions: [
      {
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        headers: {
          'Content-Type': 'application/json',
        },
        body: '{"username":"demo"}',
        extractors: [],
        assertions: [{ type: 'status_code', expected: 200 }],
      },
    ],
    parameters: [
      {
        name: 'session_id',
        type: 'counter',
        update: 'each_iteration',
        start: 100,
        step: 1,
      },
    ],
    data_sources: [],
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    if (!HTMLElement.prototype.scrollIntoView) {
      Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
        configurable: true,
        value: vi.fn(),
      })
    }
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    writeText.mockReset()
    apiMock.getScript.mockResolvedValue({ script: initialScript })
    apiMock.createScript.mockResolvedValue({ id: 'script-1' })
    apiMock.updateScript.mockResolvedValue({ id: 'script-1' })
    apiMock.validateScript.mockResolvedValue({
      script_id: 'script-1',
      valid: true,
      issues: [],
      warnings: [],
    })
    apiMock.replayDraftScript.mockResolvedValue({
      report: {
        group: 'Checkout Flow-replay',
        protocol: 'http',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        success: true,
        events: [],
      },
    })
    apiMock.replayScript.mockResolvedValue({
      report: {
        group: 'Checkout Flow-replay',
        protocol: 'http',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        success: true,
        events: [],
      },
    })
    apiMock.startDraftBrowserReplaySession.mockResolvedValue({
      session: {
        id: 'browser-replay-1',
        group: 'Checkout Flow-browser-replay',
        protocol: 'browser',
        status: 'running',
        started_at: '2026-05-14T20:00:00Z',
        updated_at: '2026-05-14T20:00:00Z',
        events: [],
      },
    })
    apiMock.startBrowserReplaySession.mockResolvedValue({
      session: {
        id: 'browser-replay-1',
        group: 'Checkout Flow-browser-replay',
        protocol: 'browser',
        status: 'running',
        started_at: '2026-05-14T20:00:00Z',
        updated_at: '2026-05-14T20:00:00Z',
        events: [],
      },
    })
    apiMock.pauseBrowserReplaySession.mockResolvedValue({ session: { status: 'paused' } })
    apiMock.resumeBrowserReplaySession.mockResolvedValue({ session: { status: 'running' } })
    apiMock.stopBrowserReplaySession.mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders the provided script model without fetching from the API', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(apiMock.getScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Action List')
    expect(container.textContent).toContain('Login')
    expect(container.textContent).toContain('Parameter Data')
    expect(container.textContent).not.toContain('Named Parameters')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'My_Test_Script')
    expect(nameInput?.getAttribute('value')).toBe('Checkout Flow')

    const baseUrlInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'https://api.example.com')
    expect(baseUrlInput?.getAttribute('value')).toBe('https://api.example.com')
    expect(container.textContent).not.toContain('Protocol')
  })

  it('opens parameter data in a dialog from the action list header', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(container.querySelector('.editor-dialog')).toBeNull()

    const parameterButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Parameter Data'),
    )
    expect(parameterButton).toBeDefined()
    if (!parameterButton) {
      return
    }

    await act(async () => {
      parameterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.textContent).toContain('Named Parameters')
    expect(container.textContent).toContain('session_id')
  })

  it('rehydrates editor state when a different initial script is provided', async () => {
    const nextScript: ScriptDefinition = {
      ...initialScript,
      name: 'Search Flow',
      config: {
        ...initialScript.config,
        base_url: 'https://search.example.com',
      },
      actions: [
        {
          ...initialScript.actions[0],
          name: 'Search',
          target: '/search',
        },
      ],
      parameters: [],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const firstAction = container.querySelector('.action-item') as HTMLDivElement | null
    expect(firstAction).not.toBeNull()
    if (!firstAction) {
      return
    }

    await act(async () => {
      firstAction.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      root.render(<ScriptEditor initialScript={nextScript} />)
    })

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'My_Test_Script')
    const baseUrlInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'https://api.example.com')

    expect(nameInput?.getAttribute('value')).toBe('Search Flow')
    expect(baseUrlInput?.getAttribute('value')).toBe('https://search.example.com')
    expect(container.textContent).toContain('Search')
    expect(container.textContent).not.toContain('session_id')
    expect(container.textContent).toContain('Select an action to edit its details')
  })

  it('includes the active project id in draft replay payloads', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const replayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Replay')
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.replayDraftScript).toHaveBeenCalledWith(expect.objectContaining({
      script: expect.objectContaining({ project_id: 'project-1' }),
    }))
  })

  it('surfaces script load failures when fetching by id', async () => {
    apiMock.getScript.mockRejectedValueOnce(new Error('Network unavailable'))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" />)
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Network unavailable')
  })

  it('keeps the protocol selector when editing an existing script', async () => {
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })

    expect(container.textContent).toContain('Protocol')
    const protocolSelect = container.querySelector('select')
    expect(protocolSelect?.value).toBe('http')
  })

  it('validates a saved script and renders returned warnings', async () => {
    apiMock.validateScript.mockResolvedValueOnce({
      script_id: 'script-1',
      valid: true,
      issues: [],
      warnings: ['WebKit execution depends on Playwright assets being installed on the controller host.'],
    })

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Validate Saved Script',
    )
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.validateScript).toHaveBeenCalledWith('script-1')
    expect(container.textContent).toContain('Saved script validation passed with warnings.')
    expect(container.textContent).toContain('WebKit execution depends on Playwright assets being installed on the controller host.')
  })

  it('renders saved script validation issues when validation fails', async () => {
    apiMock.validateScript.mockResolvedValueOnce({
      script_id: 'script-1',
      valid: false,
      issues: ['browser action 1 is missing a selector for browser_click'],
      warnings: ['WebKit device profile requires the WebKit engine.'],
    })

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Validate Saved Script',
    )
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('browser action 1 is missing a selector for browser_click')
    expect(container.textContent).toContain('WebKit device profile requires the WebKit engine.')
  })

  it('renders the new Web HTTP/HTML runtime sections from the draft model', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Runtime Settings'),
    )
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Web HTTP/HTML Compatibility')

    const runLogicToggle = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Run Logic'),
    )
    const loggingToggle = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Logging'),
    )
    expect(runLogicToggle).toBeDefined()
    expect(loggingToggle).toBeDefined()
    if (!runLogicToggle || !loggingToggle) {
      return
    }

    await act(async () => {
      runLogicToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      loggingToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Block Scheduling')
    expect(container.textContent).toContain('Parameter substitution')

    const compatibilityToggle = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Web HTTP/HTML Compatibility'),
    )
    expect(compatibilityToggle).toBeDefined()
    if (!compatibilityToggle) {
      return
    }

    await act(async () => {
      compatibilityToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const attributesArea = Array.from(container.querySelectorAll('textarea')).find(area =>
      area.getAttribute('placeholder') === 'env=perf\nregion=east',
    ) as HTMLTextAreaElement | undefined
    const checksArea = Array.from(container.querySelectorAll('textarea')).find(area =>
      area.getAttribute('placeholder') === 'Access Denied|any_response|true',
    ) as HTMLTextAreaElement | undefined
    const dfeArea = Array.from(container.querySelectorAll('textarea')).find(area =>
      area.getAttribute('placeholder') === 'base64\ngzip mode=decode\njson_to_xml',
    ) as HTMLTextAreaElement | undefined

    expect(attributesArea?.value).toContain('env=perf')
    expect(checksArea?.value).toContain('Access Denied|any_response|true')
    expect(dfeArea?.value).toContain('base64')
    expect(container.textContent).toContain('Network Speed Simulation')
    expect(container.textContent).toContain('Data Formatting')
  })

  it('surfaces mobile browser authoring controls and forwards them to browser replay', async () => {
    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        base_url: 'https://shop.example.com',
        browser: {
          engine: 'chromium',
          headless: true,
          slow_mo_ms: 25,
          touch_mode: true,
          orientation: 'portrait',
          client_metrics: true,
          capture_screenshots: 'per_action',
          download_resources: 'recorded',
          viewport: { width: 393, height: 852 },
        },
      },
      actions: [
        {
          name: 'Open Home',
          type: 'browser_navigate',
          target: '/',
          selector: '',
          value: '',
          capture: { screenshot: false, full_page: false, html: false },
          think_time_after: true,
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Runtime Settings'),
    )
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const collapsedSectionToggles = Array.from(container.querySelectorAll('button')).filter(button =>
      button.className.includes('section-toggle') && button.textContent?.includes('▸'),
    )

    await act(async () => {
      collapsedSectionToggles.forEach(button => {
        button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    })

    expect(container.textContent).toContain('Browser Engine')
    expect(container.textContent).toContain('Device Profile')
    expect(container.textContent).toContain('Force touch mode')
    expect(container.textContent).toContain('Capture client metrics')
    expect(container.textContent).toContain('Viewport Width')
    expect(container.textContent).toContain('Viewport Height')

    const deviceProfileSelect = Array.from(container.querySelectorAll('select')).find(select =>
      Array.from(select.options).some(option => option.value === 'iphone_15_pro')
      && Array.from(select.options).some(option => option.value === 'desktop_1080p'),
    ) as HTMLSelectElement | undefined
    expect(deviceProfileSelect).toBeDefined()
    if (!deviceProfileSelect) {
      return
    }

    await act(async () => {
      deviceProfileSelect.value = 'iphone_15_pro'
      deviceProfileSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const engineSelect = Array.from(container.querySelectorAll('select')).find(select =>
      Array.from(select.options).some(option => option.value === 'chromium')
      && Array.from(select.options).some(option => option.value === 'webkit'),
    ) as HTMLSelectElement | undefined
    expect(engineSelect?.value).toBe('webkit')
    expect(Array.from(engineSelect?.options || []).find(option => option.value === 'webkit')?.textContent).toBe('WebKit')
    expect(container.textContent).toContain('WebKit execution requires Playwright WebKit assets installed on the controller host. Run go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install webkit before running browser tests or replay sessions.')

    const actionItem = container.querySelector('.action-item') as HTMLDivElement | null
    expect(actionItem).not.toBeNull()
    if (!actionItem) {
      return
    }

    await act(async () => {
      actionItem.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('browser_tap')
    expect(container.textContent).toContain('browser_long_press')
    expect(container.textContent).toContain('browser_swipe')

    const replayButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Run Browser Replay',
    )
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledWith(expect.objectContaining({
      browser: expect.objectContaining({
        engine: 'webkit',
        headless: true,
        slow_mo_ms: 25,
        device_profile: 'iphone_15_pro',
        touch_mode: true,
        orientation: 'portrait',
        client_metrics: true,
        capture_screenshots: 'per_action',
        viewport: { width: 393, height: 852 },
      }),
      script: expect.objectContaining({
        protocol: 'browser',
        config: expect.objectContaining({
          browser: expect.objectContaining({
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            client_metrics: true,
          }),
        }),
      }),
    }))
  })

  it('renders replay start warnings returned by the browser replay API', async () => {
    apiMock.startDraftBrowserReplaySession.mockResolvedValueOnce({
      replay_session_id: 'browser-replay-1',
      warnings: ['WebKit controller host is missing the Playwright browser cache warmup.'],
      session: {
        id: 'browser-replay-1',
        group: 'Checkout Flow-browser-replay',
        protocol: 'browser',
        status: 'running',
        started_at: '2026-05-14T20:00:00Z',
        updated_at: '2026-05-14T20:00:00Z',
        events: [],
      },
    })

    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        browser: {
          engine: 'webkit',
          headless: true,
        },
      },
      actions: [
        {
          name: 'Open Home',
          type: 'browser_navigate',
          target: '/',
          selector: '',
          value: '',
          capture: { screenshot: false, full_page: false, html: false },
          think_time_after: true,
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const replayButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Run Browser Replay',
    )
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser replay started with warnings.')
    expect(container.textContent).toContain('WebKit controller host is missing the Playwright browser cache warmup.')
  })

  it('renders replay validation warnings returned on failed browser replay start', async () => {
    apiMock.startDraftBrowserReplaySession.mockRejectedValueOnce(Object.assign(new Error('Replay validation failed'), {
      payload: {
        issues: ['browser engine webkit requires a reachable Playwright runtime'],
        warnings: ['Install Playwright WebKit assets on the controller host before retrying.'],
      },
    }))

    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        browser: {
          engine: 'webkit',
          headless: true,
        },
      },
      actions: [
        {
          name: 'Open Home',
          type: 'browser_navigate',
          target: '/',
          selector: '',
          value: '',
          capture: { screenshot: false, full_page: false, html: false },
          think_time_after: true,
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const replayButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Run Browser Replay',
    )
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('browser engine webkit requires a reachable Playwright runtime')
    expect(container.textContent).toContain('Install Playwright WebKit assets on the controller host before retrying.')
  })

  it('round-trips DFE chain options in the compatibility editor', async () => {
    const scriptWithDFEOptions: ScriptDefinition = {
      ...initialScript,
      config: {
        ...initialScript.config,
        web_runtime: {
          ...initialScript.config.web_runtime,
          data_formatting: {
            auto_detect: true,
            chain: [{ type: 'gzip', options: { mode: 'decode' } }, { type: 'html_unescape' }],
          },
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={scriptWithDFEOptions} />)
    })

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Runtime Settings'),
    )
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const compatibilityToggle = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Web HTTP/HTML Compatibility'),
    )
    expect(compatibilityToggle).toBeDefined()
    if (!compatibilityToggle) {
      return
    }

    await act(async () => {
      compatibilityToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const dfeArea = Array.from(container.querySelectorAll('textarea')).find(area =>
      area.getAttribute('placeholder') === 'base64\ngzip mode=decode\njson_to_xml',
    ) as HTMLTextAreaElement | undefined

    expect(dfeArea).toBeDefined()
    if (!dfeArea) {
      return
    }

    expect(dfeArea.value).toContain('gzip mode=decode')
    expect(dfeArea.value).toContain('html_unescape')

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      valueSetter?.call(dfeArea, 'gzip mode=decode\nhtml_unescape')
      dfeArea.dispatchEvent(new Event('input', { bubbles: true }))
      dfeArea.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(dfeArea.value).toBe('gzip mode=decode\nhtml_unescape')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Save Script'),
    )
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      config: expect.objectContaining({
        web_runtime: expect.objectContaining({
          data_formatting: {
            auto_detect: true,
            chain: [{ type: 'gzip', options: { mode: 'decode' } }, { type: 'html_unescape' }],
          },
        }),
      }),
    }), undefined, undefined)
  })

  it('renders the action workspace with adjacent list and detail regions', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const contentGrid = container.querySelector('.actions-content-grid')
    const sidebar = container.querySelector('.actions-sidebar')
    const detail = container.querySelector('.action-detail')

    expect(contentGrid).not.toBeNull()
    expect(sidebar?.parentElement).toBe(contentGrid)
    expect(detail?.parentElement).toBe(contentGrid)
  })

  it('parameterizes selected body text from the action editor context menu', async () => {
    const scriptWithEditableBody: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          type: 'POST',
          body: '{"username":"demo"}',
        },
      ],
      parameters: [],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={scriptWithEditableBody} />)
    })

    const actionItem = container.querySelector('.action-item') as HTMLDivElement | null
    expect(actionItem).not.toBeNull()
    if (!actionItem) {
      return
    }

    await act(async () => {
      actionItem.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const textarea = container.querySelector('textarea.body-textarea') as HTMLTextAreaElement | null
    expect(textarea).not.toBeNull()
    if (!textarea) {
      return
    }

    const start = textarea.value.indexOf('demo')
    const end = start + 'demo'.length
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      textarea.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 120, clientY: 80 }))
    })

    const parameterizeButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Parameterize selection'),
    )
    expect(parameterizeButton).toBeDefined()
    if (!parameterizeButton) {
      return
    }

    await act(async () => {
      parameterizeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()

    const editorInputs = Array.from(container.querySelectorAll('.parameter-draft-panel input')) as HTMLInputElement[]
    expect(editorInputs.length).toBeGreaterThanOrEqual(2)

    const saveButton = Array.from(container.querySelectorAll('.parameter-draft-panel button')).find(button =>
      button.textContent?.includes('Save To Parameter Data'),
    )
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(textarea.value).toContain('{{param.demo}}')
    expect(container.textContent).toContain('demo')
  })

  it('shows edit parameter action when selecting an existing parameter token', async () => {
    const scriptWithToken: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          type: 'POST',
          body: '{"username":"{{param.username}}"}',
        },
      ],
      parameters: [
        {
          name: 'username',
          type: 'list',
          update: 'each_iteration',
          values: ['demo-user'],
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={scriptWithToken} />)
    })

    const actionItem = container.querySelector('.action-item') as HTMLDivElement | null
    expect(actionItem).not.toBeNull()
    if (!actionItem) {
      return
    }

    await act(async () => {
      actionItem.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const textarea = container.querySelector('textarea.body-textarea') as HTMLTextAreaElement | null
    expect(textarea).not.toBeNull()
    if (!textarea) {
      return
    }

    const token = '{{param.username}}'
    const start = textarea.value.indexOf(token)
    const end = start + token.length
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      textarea.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 100, clientY: 80 }))
    })

    const editButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Edit parameter'),
    )
    expect(editButton).toBeDefined()
  })

  it('renders SOAP protocol settings and action fields for SOAP drafts', async () => {
    const soapScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'soap',
      config: {
        ...initialScript.config,
        soap: {
          version: '1.2',
          namespaces: {
            soapenv: 'http://www.w3.org/2003/05/soap-envelope',
          },
          ws_security: {
            username_token: {
              username: 'svc-user',
              password: 'svc-pass',
              password_type: 'text',
            },
            timestamp: {
              ttl_seconds: 60,
            },
          },
          fault_handling: {
            fail_on_fault: true,
          },
        },
      },
      actions: [
        {
          name: 'CreateOrder',
          type: 'POST',
          method: 'POST',
          target: '/soap/orders',
          headers: {},
          body: '<soapenv:Envelope />',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
          soap: {
            action: 'urn:CreateOrder',
            operation: 'CreateOrder',
            body_mode: 'raw_xml',
          },
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={soapScript} />)
    })

    expect(container.textContent).toContain('SOAP scripts reuse the HTTP transport model')
    expect(container.textContent).toContain('CreateOrder')

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const soapToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('SOAP Settings'))
    expect(soapToggle).toBeDefined()
    if (!soapToggle) {
      return
    }

    await act(async () => {
      soapToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('WS-Security UsernameToken')
    expect(container.textContent).toContain('Fail on SOAP Fault')
    expect(container.textContent).toContain('SOAPAction')
  })

  it('updates the default SOAP envelope namespace when the version changes and saves it', async () => {
    const soapScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'soap',
      config: {
        ...initialScript.config,
        soap: {
          version: '1.1',
          namespaces: {
            soapenv: 'http://schemas.xmlsoap.org/soap/envelope/',
          },
          fault_handling: {
            fail_on_fault: true,
          },
        },
      },
      actions: [
        {
          name: 'CreateOrder',
          type: 'POST',
          method: 'POST',
          target: '/soap/orders',
          headers: {},
          body: '<CreateOrder />',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
          soap: {
            action: 'urn:CreateOrder',
            operation: 'CreateOrder',
            body_mode: 'generated',
          },
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={soapScript} />)
    })

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const soapToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('SOAP Settings'))
    expect(soapToggle).toBeDefined()
    if (!soapToggle) {
      return
    }

    await act(async () => {
      soapToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const versionSelect = Array.from(container.querySelectorAll('select')).find(select =>
      Array.from(select.options).some(option => option.value === '1.1')
      && Array.from(select.options).some(option => option.value === '1.2'),
    ) as HTMLSelectElement | undefined
    expect(versionSelect).toBeDefined()
    if (!versionSelect) {
      return
    }

    const namespaceInput = Array.from(container.querySelectorAll('input')).find(input =>
      input.value === 'http://schemas.xmlsoap.org/soap/envelope/',
    ) as HTMLInputElement | undefined
    expect(namespaceInput).toBeDefined()
    if (!namespaceInput) {
      return
    }

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
      valueSetter?.call(versionSelect, '1.2')
      versionSelect.dispatchEvent(new Event('input', { bubbles: true }))
      versionSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(versionSelect.value).toBe('1.2')
    expect(namespaceInput.value).toBe('http://www.w3.org/2003/05/soap-envelope')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      config: expect.objectContaining({
        soap: expect.objectContaining({
          version: '1.2',
          namespaces: {
            soapenv: 'http://www.w3.org/2003/05/soap-envelope',
          },
        }),
      }),
    }), undefined, undefined)
  })

  it('saves SOAP WS-Security and fault-handling edits through the runtime settings form', async () => {
    const soapScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'soap',
      config: {
        ...initialScript.config,
        soap: {
          version: '1.1',
          namespaces: {
            soapenv: 'http://schemas.xmlsoap.org/soap/envelope/',
          },
          ws_security: {
            username_token: {
              username: 'svc-user',
              password: 'svc-pass',
              password_type: 'text',
              nonce: false,
              created: false,
            },
            timestamp: {
              ttl_seconds: 30,
            },
          },
          fault_handling: {
            fail_on_fault: true,
          },
        },
      },
      actions: [
        {
          name: 'CreateOrder',
          type: 'POST',
          method: 'POST',
          target: '/soap/orders',
          headers: {},
          body: '<CreateOrder />',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
          soap: {
            action: 'urn:CreateOrder',
            operation: 'CreateOrder',
            body_mode: 'generated',
          },
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={soapScript} />)
    })

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const soapToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('SOAP Settings'))
    expect(soapToggle).toBeDefined()
    if (!soapToggle) {
      return
    }

    await act(async () => {
      soapToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const usernameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'service-user') as HTMLInputElement | undefined
    const passwordInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'secret') as HTMLInputElement | undefined
    const ttlInput = Array.from(container.querySelectorAll('input')).find(input => input.type === 'number' && input.value === '30') as HTMLInputElement | undefined
    const passwordTypeSelect = Array.from(container.querySelectorAll('select')).find(select =>
      Array.from(select.options).some(option => option.value === 'text')
      && Array.from(select.options).some(option => option.value === 'digest'),
    ) as HTMLSelectElement | undefined

    const failOnFaultCheckbox = Array.from(container.querySelectorAll('label.checkbox-label')).find(label =>
      label.textContent?.includes('Fail on SOAP Fault'),
    )?.querySelector('input') as HTMLInputElement | null
    const nonceCheckbox = Array.from(container.querySelectorAll('label.checkbox-label')).find(label =>
      label.textContent?.includes('Include nonce'),
    )?.querySelector('input') as HTMLInputElement | null
    const createdCheckbox = Array.from(container.querySelectorAll('label.checkbox-label')).find(label =>
      label.textContent?.includes('Include created timestamp'),
    )?.querySelector('input') as HTMLInputElement | null

    expect(usernameInput).toBeDefined()
    expect(passwordInput).toBeDefined()
    expect(ttlInput).toBeDefined()
    expect(passwordTypeSelect).toBeDefined()
    expect(failOnFaultCheckbox).not.toBeNull()
    expect(nonceCheckbox).not.toBeNull()
    expect(createdCheckbox).not.toBeNull()
    if (!usernameInput || !passwordInput || !ttlInput || !passwordTypeSelect || !failOnFaultCheckbox || !nonceCheckbox || !createdCheckbox) {
      return
    }

    await act(async () => {
      const inputSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      const selectSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set

      inputSetter?.call(usernameInput, 'svc-next')
      usernameInput.dispatchEvent(new Event('input', { bubbles: true }))
      usernameInput.dispatchEvent(new Event('change', { bubbles: true }))

      inputSetter?.call(passwordInput, 'svc-secret')
      passwordInput.dispatchEvent(new Event('input', { bubbles: true }))
      passwordInput.dispatchEvent(new Event('change', { bubbles: true }))

      selectSetter?.call(passwordTypeSelect, 'digest')
      passwordTypeSelect.dispatchEvent(new Event('input', { bubbles: true }))
      passwordTypeSelect.dispatchEvent(new Event('change', { bubbles: true }))

      failOnFaultCheckbox.click()
      nonceCheckbox.click()
      createdCheckbox.click()

      inputSetter?.call(ttlInput, '90')
      ttlInput.dispatchEvent(new Event('input', { bubbles: true }))
      ttlInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(usernameInput.value).toBe('svc-next')
    expect(passwordInput.value).toBe('svc-secret')
    expect(passwordTypeSelect.value).toBe('digest')
    expect(failOnFaultCheckbox.checked).toBe(false)
    expect(nonceCheckbox.checked).toBe(true)
    expect(createdCheckbox.checked).toBe(true)
    expect(ttlInput.value).toBe('90')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      config: expect.objectContaining({
        soap: expect.objectContaining({
          fault_handling: {
            fail_on_fault: false,
          },
          ws_security: {
            username_token: {
              username: 'svc-next',
              password: 'svc-secret',
              password_type: 'digest',
              nonce: true,
              created: true,
            },
            timestamp: {
              ttl_seconds: 90,
            },
          },
        }),
      }),
    }), undefined, undefined)
  })

  it('shows structured SOAP fault snapshot details in replay events', async () => {
    const soapScript: ScriptDefinition = {
      ...initialScript,
      name: 'SOAP Replay',
      protocol: 'soap',
      config: {
        ...initialScript.config,
        soap: {
          version: '1.2',
          namespaces: {
            soapenv: 'http://www.w3.org/2003/05/soap-envelope',
          },
          fault_handling: {
            fail_on_fault: true,
          },
        },
      },
      actions: [
        {
          name: 'CreateOrder',
          type: 'POST',
          method: 'POST',
          target: '/soap/orders',
          headers: {
            'Content-Type': 'application/soap+xml',
          },
          body: '<CreateOrder />',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
          soap: {
            action: 'urn:CreateOrder',
            operation: 'CreateOrder',
            body_mode: 'generated',
          },
        },
      ],
    }

    apiMock.replayDraftScript.mockResolvedValueOnce({
      report: {
        group: 'SOAP Replay-replay',
        protocol: 'soap',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        success: false,
        error: 'soap fault: env:Sender: Request validation failed',
        final_variables: {
          orderId: 'A-100',
        },
        events: [
          {
            phase: 'action',
            action_name: 'CreateOrder',
            action_type: 'request',
            method: 'POST',
            target: '/soap/orders',
            started_at: '2026-05-14T20:00:00Z',
            completed_at: '2026-05-14T20:00:01Z',
            duration_ms: 120,
            status_code: 200,
            success: false,
            error_message: 'soap fault: env:Sender: Request validation failed',
            headers: {
              'Content-Type': 'application/soap+xml',
            },
            body: '<CreateOrder />',
            extractions: {
              confirmationId: 'CONF-42',
            },
            snapshot: {
              request_method: 'POST',
              request_url: 'https://api.example.com/soap/orders',
              request_headers: {
                'Content-Type': 'application/soap+xml',
                ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Debug-${index}`, `value-${index}`])),
              },
              request_body: '<CreateOrder />',
              response_status: 200,
              response_headers: {
                'Content-Type': 'application/soap+xml',
                ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Trace-${index}`, `trace-${index}`])),
              },
              response_body: '<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><env:Fault><env:Code><env:Value>env:Sender</env:Value></env:Code><env:Reason><env:Text xml:lang="en">Request validation failed</env:Text></env:Reason></env:Fault></env:Body></env:Envelope>',
            },
          },
        ],
      },
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={soapScript} />)
    })

    const replayButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Replay',
    )
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.replayDraftScript).toHaveBeenCalled()
    expect(container.textContent).toContain('SOAP Fault')
    expect(container.textContent).toContain('SOAP 1.2')
    expect(container.textContent).toContain('Code: env:Sender')
    expect(container.textContent).toContain('Reason: Request validation failed')
    expect(container.textContent).toContain('Variable Inspector')
    expect(container.textContent).toContain('Extractions')
    expect(container.textContent).toContain('Snapshot Request')
    expect(container.textContent).toContain('Snapshot Response')
    const copyFaultButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy fault'))
    const copyRequestButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy request'))
    const copyResponseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy response'))
    expect(copyFaultButton).toBeDefined()
    expect(copyRequestButton).toBeDefined()
    expect(copyResponseButton).toBeDefined()
    if (!copyFaultButton || !copyRequestButton || !copyResponseButton) {
      return
    }

    await act(async () => {
      copyFaultButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(formatSOAPFaultSummary({
      version: '1.2',
      code: 'env:Sender',
      reason: 'Request validation failed',
    }))

    await act(async () => {
      copyRequestButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(formatSnapshotRequestSection({
      request_method: 'POST',
      request_url: 'https://api.example.com/soap/orders',
      request_headers: {
        'Content-Type': 'application/soap+xml',
        ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Debug-${index}`, `value-${index}`])),
      },
      request_body: '<CreateOrder />',
    }))

    await act(async () => {
      copyResponseButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenLastCalledWith(formatSnapshotResponseSection({
      response_status: 200,
      response_headers: {
        'Content-Type': 'application/soap+xml',
        ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Trace-${index}`, `trace-${index}`])),
      },
      response_body: '<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><env:Fault><env:Code><env:Value>env:Sender</env:Value></env:Code><env:Reason><env:Text xml:lang="en">Request validation failed</env:Text></env:Reason></env:Fault></env:Body></env:Envelope>',
    }, 200))
    expect(container.textContent).toContain('Show full text')
    const highlightedBlocks = Array.from(container.querySelectorAll('.snapshot-code'))
    expect(highlightedBlocks).toHaveLength(5)
    expect(highlightedBlocks.some(block => block.textContent?.includes('"orderId": "A-100"'))).toBe(true)
    expect(highlightedBlocks.some(block => block.textContent?.includes('"confirmationId": "CONF-42"'))).toBe(true)
    expect(highlightedBlocks.some(block => block.textContent?.includes('<CreateOrder />'))).toBe(true)
    const resolvedHeadersLabel = Array.from(container.querySelectorAll('.replay-code-block .replay-detail-label')).find(node => node.textContent === 'Resolved Headers')
    expect(resolvedHeadersLabel?.parentElement?.textContent).toContain('Copy')
    expect(container.querySelectorAll('.snapshot-code-token-tag').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('.snapshot-code-token-key').length).toBeGreaterThan(0)
    expect(container.textContent).toContain('env:Body')
    expect(container.textContent).toContain('env:Fault')
  })

  it('renders browser replay DOM summary and fallback snapshot with highlighted JSON previews', async () => {
    apiMock.replayDraftScript.mockResolvedValueOnce({
      report: {
        group: 'Checkout Flow-replay',
        protocol: 'web_http_html',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        success: true,
        events: [
          {
            phase: 'action',
            action_name: 'OpenCatalog',
            action_type: 'navigate',
            target: 'https://shop.example.com/catalog',
            started_at: '2026-05-14T20:00:00Z',
            completed_at: '2026-05-14T20:00:01Z',
            duration_ms: 85,
            success: true,
            headers: {
              Accept: 'text/html',
            },
            snapshot: {
              page_url: 'https://shop.example.com/catalog',
              page_title: 'Catalog',
              screenshot_base64: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7Z0nQAAAAASUVORK5CYII=',
              dom_summary: {
                title: 'Catalog',
                ready_state: 'complete',
                visible_text: ['Featured products', 'Search'],
              },
              console_entries: [
                ...Array.from({ length: 12 }, (_, index) => ({
                  level: index % 2 === 0 ? 'info' : 'warn',
                  text: `Catalog loaded ${index}`,
                  timestamp: `2026-05-14T20:00:${String(index).padStart(2, '0')}Z`,
                })),
              ],
              network_entries: [
                ...Array.from({ length: 12 }, (_, index) => ({
                  method: 'GET',
                  url: `https://shop.example.com/api/catalog/${index}`,
                  status_code: 200,
                  duration_ms: 20 + index,
                })),
              ],
              html: `<html><body>${Array.from({ length: 60 }, (_, index) => `<section>Catalog ${index} featured product with additional merchandising detail</section>`).join('')}</body></html>`,
            },
          },
        ],
      },
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const replayButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Replay',
    )
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.replayDraftScript).toHaveBeenCalled()
    expect(container.textContent).toContain('Page URL')
    expect(container.textContent).toContain('https://shop.example.com/catalog')
    expect(container.textContent).toContain('Page Title')
    expect(container.textContent).toContain('Catalog')
    expect(container.textContent).toContain('DOM Summary')
    expect(container.textContent).toContain('Snapshot')
    expect(container.textContent).toContain('Console')
    expect(container.textContent).toContain('Network')
    expect(container.textContent).toContain('HTML')
    expect(container.textContent).toContain('View full size')
    expect(container.textContent).toContain('Expand artifacts')
    expect(container.textContent).not.toContain('Catalog loaded 11')
    expect(container.textContent).not.toContain('https://shop.example.com/api/catalog/11')
    expect(container.textContent).not.toContain('Catalog 59 featured product with additional merchandising detail')
    const highlightedBlocks = Array.from(container.querySelectorAll('.snapshot-code'))
    expect(highlightedBlocks).toHaveLength(2)
    expect(highlightedBlocks.some(block => block.textContent?.includes('"ready_state": "complete"'))).toBe(true)
    expect(highlightedBlocks.some(block => block.textContent?.includes('"page_url": "https://shop.example.com/catalog"'))).toBe(true)
    const consoleLabel = Array.from(container.querySelectorAll('.replay-code-block .replay-detail-label')).find(node => node.textContent === 'Console')
    const networkLabel = Array.from(container.querySelectorAll('.replay-code-block .replay-detail-label')).find(node => node.textContent === 'Network')
    const htmlLabel = Array.from(container.querySelectorAll('.replay-code-block .replay-detail-label')).find(node => node.textContent === 'HTML')
    expect(consoleLabel?.parentElement?.textContent).toContain('Copy')
    expect(networkLabel?.parentElement?.textContent).toContain('Copy')
    expect(htmlLabel?.parentElement?.textContent).toContain('Copy')
    expect(container.querySelectorAll('.snapshot-code-token-key').length).toBeGreaterThan(0)

    const expandArtifactsButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Expand artifacts'))
    expect(expandArtifactsButton).toBeDefined()
    if (!expandArtifactsButton) {
      return
    }

    await act(async () => {
      expandArtifactsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Collapse artifacts')
    expect(container.textContent).toContain('Catalog loaded 11')
    expect(container.textContent).toContain('https://shop.example.com/api/catalog/11')
    expect(container.textContent).toContain('Catalog 59 featured product with additional merchandising detail')

    const viewScreenshotButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View full size'))
    expect(viewScreenshotButton).toBeDefined()
    if (!viewScreenshotButton) {
      return
    }

    await act(async () => {
      viewScreenshotButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const screenshotDialog = container.querySelector('[aria-label="Replay Screenshot"]')
    expect(screenshotDialog).not.toBeNull()
    expect(screenshotDialog?.textContent).toContain('Inspect the captured browser replay screenshot at full size.')

    const closeScreenshotButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Close')
    expect(closeScreenshotButton).toBeDefined()
    if (!closeScreenshotButton) {
      return
    }

    await act(async () => {
      closeScreenshotButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Replay Screenshot"]')).toBeNull()
  })

  it('does not render an empty browser artifacts section for metadata-only replay events', async () => {
    apiMock.replayDraftScript.mockResolvedValueOnce({
      report: {
        group: 'Checkout Flow-replay',
        protocol: 'web_http_html',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        success: true,
        events: [
          {
            phase: 'action',
            action_name: 'OpenCatalog',
            action_type: 'navigate',
            target: 'https://shop.example.com/catalog',
            started_at: '2026-05-14T20:00:00Z',
            completed_at: '2026-05-14T20:00:01Z',
            duration_ms: 85,
            success: true,
            headers: {
              Accept: 'text/html',
            },
            snapshot: {
              page_url: 'https://shop.example.com/catalog',
              page_title: 'Catalog',
            },
          },
        ],
      },
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const replayButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Replay',
    )
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Page URL')
    expect(container.textContent).toContain('https://shop.example.com/catalog')
    expect(container.textContent).toContain('Page Title')
    expect(container.textContent).toContain('Catalog')
    expect(container.textContent).not.toContain('Browser Artifacts')
    expect(container.textContent).not.toContain('Console')
    expect(container.textContent).not.toContain('Network')
    expect(container.textContent).not.toContain('View full size')
  })

  it('renders Java-over-HTTP settings for java_http drafts', async () => {
    const javaScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'java_http',
      config: {
        ...initialScript.config,
        java_http: {
          codec: 'java_serialized_object_base64',
          helper_mode: 'sidecar',
          content_type: 'application/x-java-serialized-object',
          accept: 'application/x-java-serialized-object',
          body_encoding: 'base64',
          object_schema: 'com.example.AuthRequest',
          request_mapping: {
            username: 'principal.name',
          },
          response_mapping: {
            'session.token': 'session_token',
          },
          extractor_type: 'opaque',
          preserve_binary_result: true,
        },
      },
      actions: [
        {
          name: 'JavaCall',
          type: 'POST',
          method: 'POST',
          target: '/java/auth',
          headers: {},
          body: 'rO0ABXQABGRlbW8=',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={javaScript} />)
    })

    expect(container.textContent).toContain('java_serialized_object_base64')
    expect(container.textContent).toContain('support embedded or sidecar helper modes')

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) {
      return
    }

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const javaToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Java HTTP Settings'))
    expect(javaToggle).toBeDefined()
    if (!javaToggle) {
      return
    }

    expect(javaToggle.textContent).toContain('java_serialized_object_base64 · sidecar')

    await act(async () => {
      javaToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Wire Format')
    expect(container.textContent).toContain('Request Mapping')
    expect(container.textContent).toContain('Response Mapping')
  const helperModeSelect = Array.from(container.querySelectorAll('select')).find(select => select.value === 'sidecar')
  expect(helperModeSelect).toBeDefined()
    const principalInput = Array.from(container.querySelectorAll('input')).find(input => input.value === 'principal.name')
    const tokenInput = Array.from(container.querySelectorAll('input')).find(input => input.value === 'session_token')
    expect(principalInput).toBeDefined()
    expect(tokenInput).toBeDefined()
    expect(container.textContent).toContain('Preserve binary responses as base64 text')
  })
})