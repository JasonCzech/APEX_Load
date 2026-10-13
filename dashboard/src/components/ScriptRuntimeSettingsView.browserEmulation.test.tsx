// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { ScriptRuntimeSettingsView } from './ScriptRuntimeSettingsView'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function httpScript(overrides: Partial<ScriptDefinition['config']> = {}): ScriptDefinition {
  return {
    name: 'HTTP Script',
    version: '1.0.0',
    protocol: 'http',
    config: { base_url: 'https://example.test', ...overrides },
    actions: [{ name: 'GET /', type: 'http_request', method: 'GET', target: '/' }],
  }
}

function browserScript(): ScriptDefinition {
  return {
    name: 'Browser Script',
    version: '1.0.0',
    protocol: 'browser',
    config: { base_url: 'https://example.test', browser: { engine: 'chromium' } },
    actions: [{ name: 'Navigate', type: 'browser_navigate', target: '/' }],
  }
}

describe('ScriptRuntimeSettingsView Browser Emulation', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function render(script: ScriptDefinition, options: {
    activeSection?: string
    activeHTTPTransportTab?: string
  } = {}) {
    const onUpdateField = vi.fn<(path: string, value: unknown) => void>()
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab={(options.activeHTTPTransportTab ?? 'timeouts') as never}
          activeSection={(options.activeSection ?? 'script_config') as never}
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    return onUpdateField
  }

  it('offers the Browser Emulation runtime tab for http scripts but not browser or grpc scripts', () => {
    render(httpScript())
    expect(container.textContent).toContain('Browser Emulation')

    act(() => root.unmount())
    root = createRoot(container)
    render(browserScript())
    // The top-level section is absent; the label only appears as the
    // browser-protocol HTTP Transport sub-tab when that section is open.
    const tabLabels = Array.from(container.querySelectorAll('.runtime-settings-tab-label')).map(el => el.textContent)
    expect(tabLabels).not.toContain('Browser Emulation')

    act(() => root.unmount())
    root = createRoot(container)
    render({ ...httpScript(), protocol: 'grpc' })
    expect(container.textContent).not.toContain('Browser Emulation')
  })

  it('renders browser selection and cache controls with dependent enablement', () => {
    render(httpScript(), { activeSection: 'browser_emulation' })

    const browserSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser type"]')
    const versionSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser version"]')
    const platformSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser platform"]')
    const customUA = container.querySelector<HTMLInputElement>('input[aria-label="Custom User-Agent"]')
    expect(browserSelect).toBeTruthy()
    expect(browserSelect?.disabled).toBe(false)
    // No browser chosen yet: version/platform wait on the type selection.
    expect(versionSelect?.disabled).toBe(true)
    expect(platformSelect?.disabled).toBe(true)
    // Specific mode (default) disables the custom UA input.
    expect(customUA?.disabled).toBe(true)

    // Cache sub-options disabled until Simulate browser cache is checked.
    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('.runtime-checkbox-list input[type="checkbox"]'))
    const byLabel = (text: string) => checkboxes.find(cb => cb.closest('label')?.textContent?.includes(text))
    expect(byLabel('Cache URLs requiring content')?.disabled).toBe(true)
    expect(byLabel('Check for newer versions')?.disabled).toBe(true)
    expect(byLabel('Download non-HTML resources')?.disabled).toBe(false)
    expect(byLabel('Simulate prefetch and prerender')?.disabled).toBe(false)
  })

  it('enables sub-options and custom UA according to state and writes config.browser_emulation', () => {
    const onUpdateField = render(
      httpScript({
        browser_emulation: {
          mode: 'custom',
          custom_user_agent: 'MyAgent/1.0',
          cache: { simulate_cache: true },
        },
      }),
      { activeSection: 'browser_emulation' },
    )

    const browserSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser type"]')
    const customUA = container.querySelector<HTMLInputElement>('input[aria-label="Custom User-Agent"]')
    expect(browserSelect?.disabled).toBe(true)
    expect(customUA?.disabled).toBe(false)
    expect(customUA?.value).toBe('MyAgent/1.0')

    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('.runtime-checkbox-list input[type="checkbox"]'))
    const contentURLs = checkboxes.find(cb => cb.closest('label')?.textContent?.includes('Cache URLs requiring content'))
    expect(contentURLs?.disabled).toBe(false)
    act(() => {
      contentURLs?.click()
    })
    expect(onUpdateField).toHaveBeenCalledWith('config.browser_emulation', expect.objectContaining({
      cache: expect.objectContaining({ simulate_cache: true, cache_content_urls: true }),
    }))
  })

  it('clears cache sub-options when Simulate browser cache is unchecked (no validation trap)', () => {
    const onUpdateField = render(
      httpScript({
        browser_emulation: {
          cache: { simulate_cache: true, cache_content_urls: true, check_newer_versions: true },
        },
      }),
      { activeSection: 'http', activeHTTPTransportTab: 'session_cache' },
    )
    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
    const simulate = checkboxes.find(cb => cb.closest('label')?.textContent?.includes('Simulate HTTP cache'))
    act(() => { simulate?.click() })
    // The children must be cleared alongside the parent, not stranded true+disabled.
    const lastCall = onUpdateField.mock.calls[onUpdateField.mock.calls.length - 1]
    const cache = (lastCall?.[1] as { cache?: Record<string, unknown> }).cache ?? {}
    expect(cache.simulate_cache).toBe(false)
    expect(cache.cache_content_urls).toBeUndefined()
    expect(cache.check_newer_versions).toBeUndefined()
  })

  it('unsets specific mode when the browser is reverted to Engine default', () => {
    const onUpdateField = render(
      httpScript({ browser_emulation: { mode: 'specific', browser_type: 'chrome' } }),
      { activeSection: 'browser_emulation' },
    )
    const browserSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser type"]')
    act(() => {
      browserSelect!.value = ''
      browserSelect!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const written = onUpdateField.mock.calls[onUpdateField.mock.calls.length - 1]?.[1] as { mode?: string, browser_type?: string }
    // mode must not remain 'specific' with no browser_type.
    expect(written.mode).toBeUndefined()
    expect(written.browser_type).toBeUndefined()
  })

  it('hides the Playwright browser sub-tab for http scripts and keeps it for browser scripts', () => {
    render(httpScript(), { activeSection: 'http', activeHTTPTransportTab: 'browser' })
    const tabLabels = Array.from(container.querySelectorAll('.http-transport-tab')).map(el => el.textContent)
    expect(tabLabels).not.toContain('Browser Emulation')
    // Stale hidden tab clamps to the first visible panel.
    expect(container.querySelector('#http-transport-panel-request-defaults')).toBeTruthy()
    expect(container.querySelector('#http-transport-panel-browser')).toBeFalsy()

    act(() => root.unmount())
    root = createRoot(container)
    render(browserScript(), { activeSection: 'http', activeHTTPTransportTab: 'browser' })
    const browserTabLabels = Array.from(container.querySelectorAll('.http-transport-tab')).map(el => el.textContent)
    expect(browserTabLabels).toContain('Browser Emulation')
    expect(container.querySelector('#http-transport-panel-browser')).toBeTruthy()
    expect(container.textContent).toContain('Browser Engine')
  })

  it('moves the User-Agent and Accept headers into Request Defaults', () => {
    render(
      httpScript({ browser_emulation: { mode: 'specific', browser_type: 'chrome' } }),
      { activeSection: 'http', activeHTTPTransportTab: 'request_defaults' },
    )
    const defaultsPanel = container.querySelector('#http-transport-panel-request-defaults')
    expect(defaultsPanel?.textContent).toContain('User-Agent')
    expect(defaultsPanel?.textContent).toContain('Accept-Encoding')
    expect(defaultsPanel?.textContent).toContain('Accept-Language')
    expect(defaultsPanel?.textContent).toContain('Typed Request Behavior')
  })
})

describe('ScriptRuntimeSettingsView browser QUIC', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function renderBrowser(script: ScriptDefinition) {
    const onUpdateField = vi.fn<(path: string, value: unknown) => void>()
    act(() => {
      root.render(
        <ScriptRuntimeSettingsView
          activeHTTPTransportTab={'browser' as never}
          activeSection={'http' as never}
          canEditProtocol={false}
          onSave={vi.fn()}
          onSelectHTTPTransportTab={vi.fn()}
          onSelectSection={vi.fn()}
          onUpdateField={onUpdateField}
          onUpdateProtocol={vi.fn()}
          saveDisabled={false}
          saving={false}
          script={script}
        />,
      )
    })
    const select = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(element => element.closest('label')?.textContent?.includes('HTTP/3 (QUIC)'))
    return { onUpdateField, select }
  }

  it('writes off and clears back to the browser default', () => {
    const { onUpdateField, select } = renderBrowser(browserScript())
    expect(select?.value).toBe('auto')
    act(() => {
      select!.value = 'off'
      select!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', expect.objectContaining({ engine: 'chromium', quic: 'off' }))
    act(() => {
      select!.value = 'auto'
      select!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', expect.objectContaining({ quic: undefined }))
  })

  // The capture settings that fill the report's Browser tab are offered here,
  // off by default, and say what they fill.
  it('offers page timings and interaction evidence', () => {
    const { onUpdateField } = renderBrowser(browserScript())
    const checkbox = (text: string) => Array.from(container.querySelectorAll<HTMLLabelElement>('label.checkbox-label'))
      .find(label => label.textContent === text)?.querySelector<HTMLInputElement>('input')
    expect(checkbox('Capture page timings and Web Vitals')?.checked).toBe(false)
    expect(checkbox('Capture interaction evidence')?.checked).toBe(false)
    expect(container.querySelector('#browser-capture-hint')?.textContent).toContain("Browser groups panel and the run report's Browser tab")
    act(() => checkbox('Capture page timings and Web Vitals')!.click())
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', expect.objectContaining({ engine: 'chromium', capture_document_metrics: true }))
    act(() => checkbox('Capture interaction evidence')!.click())
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', expect.objectContaining({ capture_interactions: true }))
  })

  it('does not offer off for WebKit', () => {
    const script = browserScript()
    script.config.browser = { engine: 'webkit' }
    const { select } = renderBrowser(script)
    expect(select?.querySelector<HTMLOptionElement>('option[value="off"]')?.disabled).toBe(true)
    expect(container.textContent).not.toContain('WebKit cannot launch with QUIC disabled')
  })

  it('keeps a conflicting off visible and explains it after switching to WebKit', () => {
    const script = browserScript()
    script.config.browser = { engine: 'webkit', quic: 'off' }
    const { select } = renderBrowser(script)
    expect(select?.value).toBe('off')
    expect(select?.querySelector<HTMLOptionElement>('option[value="off"]')?.disabled).toBe(false)
    expect(container.textContent).toContain('WebKit cannot launch with QUIC disabled')
  })
})
