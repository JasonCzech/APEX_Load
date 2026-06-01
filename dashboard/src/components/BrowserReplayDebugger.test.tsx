// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { BrowserReplayReport } from '../types'
import BrowserReplayDebugger from './BrowserReplayDebugger'
import { formatConsoleEntries } from './scriptEditorUtils'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('BrowserReplayDebugger', () => {
  let container: HTMLDivElement
  let root: Root
  const writeText = vi.fn()

  const report: BrowserReplayReport = {
    group: 'browser-replay',
    protocol: 'web_http_html',
    started_at: '2026-05-14T20:00:00Z',
    completed_at: '2026-05-14T20:00:02Z',
    success: false,
    events: [
      {
        phase: 'action',
        action_name: 'OpenCatalog',
        action_type: 'navigate',
        target: 'https://shop.example.com/catalog',
        success: true,
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        duration_ms: 85,
        page_url: 'https://shop.example.com/catalog',
        page_title: 'Catalog',
        dom_summary: {
          title: 'Catalog',
          ready_state: 'complete',
          visible_text: ['Featured products', 'Search'],
        },
        screenshot_base64: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7Z0nQAAAAASUVORK5CYII=',
        console_entries: [{ level: 'info', text: 'Catalog loaded' }],
        network_entries: [{ method: 'GET', url: 'https://shop.example.com/api/catalog', status_code: 200, duration_ms: 23 }],
        client_metrics: { lcp_ms: 240, inp_ms: 55, cls_score: 0.03, browser_engine: 'chromium', device_profile: 'desktop_1080p' },
        html: '<html><body>Catalog</body></html>',
      },
      {
        phase: 'action',
        action_name: 'SubmitSearch',
        action_type: 'click',
        target: '#search-submit',
        success: false,
        error_message: 'button not found',
        started_at: '2026-05-14T20:00:01Z',
        completed_at: '2026-05-14T20:00:02Z',
        duration_ms: 42,
        page_url: 'https://shop.example.com/catalog?q=boots',
        page_title: 'Catalog Search',
        dom_summary: {
          title: 'Catalog Search',
          ready_state: 'interactive',
          visible_text: ['No results'],
        },
        screenshot_base64: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7Z0nQAAAAASUVORK5CYII=',
        console_entries: [{ level: 'error', text: 'Search button missing' }, { level: 'warn', text: 'Retry scheduled' }],
        network_entries: [],
      },
    ],
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    writeText.mockReset()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('shows selected browser event details and falls back to the viewport event when none is selected', async () => {
    await act(async () => {
      root.render(<BrowserReplayDebugger report={report} running={false} />)
    })

    expect(container.textContent).toContain('Selected Event')
    expect(container.textContent).toContain('Action Timeline')
    expect(container.textContent).toContain('Network waterfall, console, screenshots, Core Web Vitals, and DOM snapshots')
    expect(container.textContent).toContain('Network 1 / 23.0ms')
    expect(container.textContent).toContain('LCP 240ms / INP 55ms / CLS 0.030')
    expect(container.textContent).toContain('Screenshot + DOM')
    expect(container.textContent).toContain('DOM complete / 2 lines')
    expect(container.textContent).toContain('SubmitSearch')
    expect(container.textContent).toContain('DOM Ready State')
    expect(container.textContent).toContain('interactive')
    expect(container.textContent).toContain('Core Web Vitals n/a')
    expect(container.textContent).toContain('Console Entries')
    expect(container.textContent).toContain('2')
    expect(container.textContent).toContain('Network Entries')
    expect(container.textContent).toContain('button not found')
    expect(container.textContent).not.toContain('Search button missing')
    expect(container.textContent).not.toContain('Retry scheduled')

    const copyURLButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy URL'))
    const copyErrorButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy error'))
    expect(copyURLButton).toBeDefined()
    expect(copyErrorButton).toBeDefined()
    if (!copyURLButton || !copyErrorButton) {
      return
    }

    await act(async () => {
      copyURLButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith('https://shop.example.com/catalog?q=boots')
    expect(copyURLButton.textContent).toContain('URL copied')

    await act(async () => {
      copyErrorButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenLastCalledWith('button not found')
    expect(copyErrorButton.textContent).toContain('Error copied')

    const copyConsoleButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy console'))
    const copyNetworkButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy network'))
    expect(copyConsoleButton).toBeDefined()
    expect(copyNetworkButton).toBeUndefined()
    if (!copyConsoleButton) {
      return
    }

    await act(async () => {
      copyConsoleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenLastCalledWith(formatConsoleEntries(report.events[1].console_entries))
    expect(copyConsoleButton.textContent).toContain('Console copied')

    const showConsolePreviewButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Show preview'))
    expect(showConsolePreviewButton).toBeDefined()
    if (!showConsolePreviewButton) {
      return
    }

    await act(async () => {
      showConsolePreviewButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Search button missing')
    expect(container.textContent).toContain('Retry scheduled')
    expect(showConsolePreviewButton.textContent).toContain('Hide preview')

    await act(async () => {
      root.render(<BrowserReplayDebugger report={report} running={false} selectedEventIndex={0} />)
    })

    expect(container.textContent).toContain('OpenCatalog')
    expect(container.textContent).toContain('complete')
    expect(container.textContent).toContain('HTML Capture')
    expect(container.textContent).toContain('Core Web Vitals')
    expect(container.textContent).toContain('LCP 240ms / INP 55ms / CLS 0.030')
    expect(container.textContent).toContain('View full size')
    expect(container.textContent).not.toContain('<html><body>Catalog</body></html>')
    expect(container.textContent).not.toContain('GET 200 https://shop.example.com/api/catalog 23.0 ms')
    expect(container.querySelector('.browser-debugger-detail-status.success')).not.toBeNull()

    const viewScreenshotButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View full size'))
    expect(viewScreenshotButton).toBeDefined()
    if (!viewScreenshotButton) {
      return
    }

    await act(async () => {
      viewScreenshotButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const screenshotDialog = container.querySelector('[aria-label="Browser Replay Screenshot"]')
    expect(screenshotDialog).not.toBeNull()
    expect(screenshotDialog?.textContent).toContain('Inspect the current browser debugger viewport at full size.')

    const closeDialogButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Close')
    expect(closeDialogButton).toBeDefined()
    if (!closeDialogButton) {
      return
    }

    await act(async () => {
      closeDialogButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Browser Replay Screenshot"]')).toBeNull()

    const expandAllButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Expand all'))
    expect(expandAllButton).toBeDefined()
    if (!expandAllButton) {
      return
    }

    await act(async () => {
      expandAllButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('GET 200 https://shop.example.com/api/catalog 23.0 ms')
    expect(container.textContent).toContain('<html><body>Catalog</body></html>')
    expect(expandAllButton.textContent).toContain('Collapse all')

    const showNetworkPreviewButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Show preview') && button.parentElement?.parentElement?.textContent?.includes('Network Entries'),
    )
    expect(showNetworkPreviewButton).toBeUndefined()

    await act(async () => {
      root.render(<BrowserReplayDebugger report={report} running={false} selectedEventIndex={1} />)
    })

    expect(container.textContent).toContain('SubmitSearch')
    expect(container.textContent).not.toContain('GET 200 https://shop.example.com/api/catalog 23.0 ms')

    await act(async () => {
      root.render(<BrowserReplayDebugger report={report} running={false} selectedEventIndex={0} />)
    })

    expect(container.textContent).toContain('OpenCatalog')
    expect(container.textContent).toContain('GET 200 https://shop.example.com/api/catalog 23.0 ms')
    expect(container.textContent).toContain('<html><body>Catalog</body></html>')
    const collapseAllButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Collapse all'))
    expect(collapseAllButton).toBeDefined()

    const copyHTMLButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy HTML'))
    expect(copyHTMLButton).toBeDefined()
    if (!copyHTMLButton) {
      return
    }

    await act(async () => {
      copyHTMLButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenLastCalledWith('<html><body>Catalog</body></html>')
    expect(copyHTMLButton.textContent).toContain('HTML copied')

    const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy details'))
    expect(copyButton).toBeDefined()
    if (!copyButton) {
      return
    }

    await act(async () => {
      copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith([
      'Event: OpenCatalog',
      'Status: success',
      'Action: navigate https://shop.example.com/catalog',
      'Duration: 85.0ms',
      'Page Title: Catalog',
      'Page URL: https://shop.example.com/catalog',
      'DOM Ready State: complete',
      'Visible Text Lines: 2',
      'Console Entries: 1',
      'Network Entries: 1',
      'Core Web Vitals: LCP 240ms / INP 55ms / CLS 0.030',
      'HTML Capture: Captured',
      'Error: None',
    ].join('\n'))
    expect(copyButton.textContent).toContain('Copied')
  })

  it('surfaces VuGen Web2 source step and transaction labels in replay evidence', async () => {
    const sourceReport: BrowserReplayReport = {
      ...report,
      events: [
        {
          ...report.events[0],
          source_profile: 'vugen_web2',
          source_step_id: 'step-checkout',
          source_step_index: 3,
          source_action: 'Click',
          source_action_name: 'Click Checkout',
          source_transaction: 'Checkout',
        },
      ],
    }

    await act(async () => {
      root.render(<BrowserReplayDebugger report={sourceReport} running={false} selectedEventIndex={0} />)
    })

    expect(container.textContent).toContain('VuGen Source')
    expect(container.textContent).toContain('Txn Checkout · Step 4 · Click Checkout')
    expect(container.textContent).toContain('vugen_web2')

    const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy details'))
    expect(copyButton).toBeDefined()
    if (!copyButton) {
      return
    }

    await act(async () => {
      copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Source Step: Txn Checkout · Step 4 · Click Checkout'))
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Source Profile: vugen_web2'))
  })
})
