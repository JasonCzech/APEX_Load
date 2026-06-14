// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BrowserRecorderControlPanel from './BrowserRecorderControlPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { apiFetchMock, downloadBlobMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  downloadBlobMock: vi.fn(),
}))

const apiMock = {
  getBrowserRecording: vi.fn(),
  stopBrowserRecording: vi.fn(),
  pauseBrowserRecording: vi.fn(),
  resumeBrowserRecording: vi.fn(),
  navigateBrowserRecording: vi.fn(),
  focusBrowserRecording: vi.fn(),
  startBrowserRecordingTransaction: vi.fn(),
  endBrowserRecordingTransaction: vi.fn(),
  addBrowserRecordingNote: vi.fn(),
  previewBrowserRecordingConversion: vi.fn(),
  convertBrowserRecording: vi.fn(),
  setBrowserRecordingSection: vi.fn(),
  setBrowserRecordingPickMode: vi.fn(),
  deleteLastBrowserRecordingStep: vi.fn(),
  updateBrowserRecordingStep: vi.fn(),
  suggestBrowserRecordingTransactions: vi.fn(),
  verifyBrowserRecording: vi.fn(),
  exportBrowserRecording: vi.fn(),
  importSalesforce: vi.fn(),
  createScript: vi.fn(),
  browserRecorderBridge: null as any,
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: () => ({ connectionState: 'open' }),
}))

vi.mock('../hooks/apiClient', () => ({
  apiFetch: apiFetchMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: downloadBlobMock,
}))

const recordingSession = {
  id: 'brec-1',
  name: 'Checkout Flow',
  status: 'recording',
  start_url: 'https://shop.example/login',
  capture_har: true,
  started_at: '2026-05-12T20:00:00Z',
  current_url: 'https://shop.example/cart',
  step_count: 2,
  navigation_count: 2,
  network_request_count: 14,
  websocket_count: 1,
  warning_count: 0,
  har_available: false,
  steps: [
    { seq: 1, kind: 'fill', masked: true, timestamp: '2026-05-12T20:00:01Z', url: 'https://shop.example/login' },
    { seq: 2, kind: 'click', value: '', title: 'Sign in', timestamp: '2026-05-12T20:00:02Z', url: 'https://shop.example/login' },
  ],
}

const stoppedSession = {
  ...recordingSession,
  status: 'stopped',
  stop_reason: 'requested',
  har_available: true,
  warnings: ['step 1 filled a password input; the value was masked at capture — provide it at runtime via {{env.PASSWORD}}'],
}

const salesforceStoppedSession = {
  ...stoppedSession,
  start_url: 'https://acme.my.salesforce.com/lightning/setup',
  current_url: 'https://acme.lightning.force.com/lightning/page/home',
  warnings: ['Salesforce traffic detected; use /api/v1/scripts/import/salesforce for the native HTTP draft.'],
}

describe('BrowserRecorderControlPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiFetchMock.mockResolvedValue({
      ok: true,
      blob: vi.fn().mockResolvedValue(new Blob(['{"log":{}}'])),
    })
    apiMock.getBrowserRecording.mockResolvedValue({ session: recordingSession })
    apiMock.stopBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      script: { name: 'Checkout Flow', protocol: 'browser', actions: [{ name: 'Navigate' }, { name: 'Fill' }] },
      warnings: ['masked password'],
      stats: {},
      parameterization: { suggestions: [], summary: { total: 3 } },
    })
    apiMock.convertBrowserRecording.mockResolvedValue({
      script: {
        id: 'script-9',
        name: 'Checkout Flow',
        protocol: 'browser',
        version: '1',
        created_at: '2026-05-12T20:10:00Z',
        script: { name: 'Checkout Flow', protocol: 'browser', actions: [] },
      },
      warnings: [],
      stats: {},
      parameterization: { suggestions: [], summary: { total: 0 } },
    })
    apiMock.setBrowserRecordingSection.mockImplementation(async (_id: string, section: string) => ({ session: { ...recordingSession, section } }))
    apiMock.setBrowserRecordingPickMode.mockImplementation(async (_id: string, on: boolean) => ({ session: { ...recordingSession, picking: on } }))
    apiMock.deleteLastBrowserRecordingStep.mockResolvedValue({ session: recordingSession })
    apiMock.updateBrowserRecordingStep.mockResolvedValue({ session: recordingSession })
    apiMock.suggestBrowserRecordingTransactions.mockResolvedValue({ suggestions: [] })
    apiMock.verifyBrowserRecording.mockResolvedValue({ replay_session_id: 'replay-1', replay_session: {}, warnings: [] })
    apiMock.exportBrowserRecording.mockResolvedValue({
      ok: true,
      blob: vi.fn().mockResolvedValue(new Blob(['{"log":{}}'])),
    })
    apiMock.importSalesforce.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Checkout Flow Salesforce HTTP',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://acme.my.salesforce.com' },
        actions: [{ name: 'Aura Bootstrap', type: 'request', method: 'POST', target: '/aura' }],
      },
      warnings: ['Review Salesforce correlation extractors before approval.'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { converted_actions: 1 },
      summary: {
        salesforce_review: {
          detected: true,
          har_match_coverage: { har_provided: true, imported_requests: 1, matched_requests: 1, coverage_pct: 100 },
        },
      },
    })
    apiMock.createScript.mockResolvedValue({
      id: 'script-remote-1',
      name: 'Checkout Flow',
      protocol: 'browser',
      version: '1',
      created_at: '2026-05-12T20:10:00Z',
      script: { name: 'Checkout Flow', protocol: 'browser', actions: [] },
    })
    apiMock.browserRecorderBridge = null
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  async function renderPanel(props: Partial<Parameters<typeof BrowserRecorderControlPanel>[0]> = {}) {
    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-1" {...props} />)
    })
    await flushPromises()
  }

  function clickButton(label: string) {
    const button = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes(label))
    expect(button, `button "${label}"`).toBeDefined()
    return act(async () => {
      button!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  it('renders the live session with step feed, counts, and masked indicator', async () => {
    await renderPanel()

    expect(container.textContent).toContain('Checkout Flow')
    expect(container.textContent).toContain('Recording')
    expect(container.textContent).toContain('https://shop.example/cart')
    expect(container.textContent).toContain('Live Steps')
    // Masked fill never shows a value, only the masked badge.
    expect(container.textContent).toContain('masked')
    expect(container.textContent).not.toContain('hunter2')
    // No conversion controls while recording.
    expect(container.textContent).not.toContain('Save to Script Library')
  })

  it('stops the recording and reveals conversion controls', async () => {
    await renderPanel()
    await clickButton('Stop Recording')
    await flushPromises()

    expect(apiMock.stopBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.textContent).toContain('Stopped')
    expect(container.textContent).toContain('Save as Script')
    expect(container.textContent).toContain('Save to Script Library')
    // The capture-time masking warning surfaces to the user.
    expect(container.textContent).toContain('{{env.PASSWORD}}')
  })

  it('previews the conversion with parameterization summary', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Preview Conversion')
    await flushPromises()

    expect(apiMock.previewBrowserRecordingConversion).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    expect(container.textContent).toContain('Preview: 2 actions')
    expect(container.textContent).toContain('3 parameterization suggestions')
  })

  it('saves the converted script and notifies onImported', async () => {
    const onImported = vi.fn()
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel({ onImported })
    await clickButton('Save to Script Library')
    await flushPromises()

    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-9' })] })
    expect(container.textContent).toContain('saved to the script library')
  })

  it('opens the previewed script in the visual editor', async () => {
    const onEdit = vi.fn()
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel({ onEdit })
    await clickButton('Preview Conversion')
    await flushPromises()
    await clickButton('Open in Visual Editor')

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ name: 'Checkout Flow', protocol: 'browser' }))
  })

  it('downloads the side-car HAR when available', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Download HAR')
    await flushPromises()

    expect(apiMock.exportBrowserRecording).toHaveBeenCalledWith('brec-1', 'har')
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), 'checkout-flow.har')
  })

  it('reviews Salesforce-family HAR traffic as a native HTTP draft', async () => {
    const onEdit = vi.fn()
    const onImported = vi.fn()
    apiMock.getBrowserRecording.mockResolvedValue({ session: salesforceStoppedSession })
    apiMock.createScript.mockResolvedValue({
      id: 'script-salesforce-1',
      name: 'Checkout Flow Salesforce HTTP',
      protocol: 'http',
      version: '1.0.0',
      created_at: '2026-05-12T20:10:00Z',
      script: { name: 'Checkout Flow Salesforce HTTP', protocol: 'http', actions: [] },
    })

    await renderPanel({ onEdit, onImported })

    expect(container.textContent).toContain('Review Salesforce HTTP Draft')

    await clickButton('Review Salesforce HTTP Draft')
    await flushPromises()

    expect(apiMock.exportBrowserRecording).toHaveBeenCalledWith('brec-1', 'har')
    expect(apiMock.importSalesforce).toHaveBeenCalledWith(expect.any(File), expect.objectContaining({
      name: 'Checkout Flow',
    }))
    expect(container.textContent).toContain('Salesforce HTTP draft: 1 actions')
    expect(container.textContent).toContain('100% HAR coverage')
    expect(container.textContent).toContain('Review Salesforce correlation extractors before approval.')

    await clickButton('Open Salesforce Draft')
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ name: 'Checkout Flow Salesforce HTTP', protocol: 'http' }))

    await clickButton('Save Salesforce HTTP Script')
    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({ name: 'Checkout Flow Salesforce HTTP' }), undefined)
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-salesforce-1' })] })
  })

  it('shows the load error when the session cannot be fetched', async () => {
    apiMock.getBrowserRecording.mockRejectedValue(new Error('browser recording session not found'))
    await renderPanel()

    expect(container.textContent).toContain('browser recording session not found')
  })

  it('pauses and resumes the recording', async () => {
    const pausedSession = { ...recordingSession, status: 'paused' }
    apiMock.pauseBrowserRecording.mockResolvedValue({ session: pausedSession })
    apiMock.resumeBrowserRecording.mockResolvedValue({ session: recordingSession })
    await renderPanel()

    await clickButton('Pause Recording')
    await flushPromises()
    expect(apiMock.pauseBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.textContent).toContain('Paused')
    expect(container.textContent).toContain('Resume Recording')
    expect(container.textContent).toContain('think time are not recorded')

    await clickButton('Resume Recording')
    await flushPromises()
    expect(apiMock.resumeBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.textContent).toContain('Pause Recording')
  })

  it('navigates the recording browser with scheme validation', async () => {
    apiMock.navigateBrowserRecording.mockResolvedValue({ session: recordingSession })
    await renderPanel()

    const input = container.querySelector('input[aria-label="Navigate the recording browser"]') as HTMLInputElement
    expect(input).not.toBeNull()

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, 'file:///etc/passwd')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await clickButton('Go')
    await flushPromises()
    expect(apiMock.navigateBrowserRecording).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Enter an absolute http(s) URL')

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, 'https://shop.example/checkout')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await clickButton('Go')
    await flushPromises()
    expect(apiMock.navigateBrowserRecording).toHaveBeenCalledWith('brec-1', 'https://shop.example/checkout')
  })

  it('focuses the recording browser', async () => {
    apiMock.focusBrowserRecording.mockResolvedValue({ session: recordingSession })
    await renderPanel()
    await clickButton('Focus Browser')
    await flushPromises()

    expect(apiMock.focusBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.textContent).toContain('brought to the front')
  })

  it('starts and ends transactions with open-transaction chips', async () => {
    const withOpenTxn = { ...recordingSession, open_transactions: ['Checkout'] }
    apiMock.startBrowserRecordingTransaction.mockResolvedValue({
      session: withOpenTxn,
      marker: { kind: 'transaction_start', name: 'Checkout', timestamp: '2026-05-12T20:00:03Z' },
    })
    apiMock.endBrowserRecordingTransaction.mockResolvedValue({
      session: { ...recordingSession, open_transactions: [] },
      marker: { kind: 'transaction_end', name: 'Checkout', timestamp: '2026-05-12T20:00:09Z' },
    })
    await renderPanel()

    const nameInput = container.querySelector('input[aria-label="Transaction name"]') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(nameInput, 'Checkout')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await clickButton('Start Transaction')
    await flushPromises()

    expect(apiMock.startBrowserRecordingTransaction).toHaveBeenCalledWith('brec-1', 'Checkout')
    const chip = container.querySelector('[data-testid="open-transaction"]')
    expect(chip).not.toBeNull()
    expect(chip!.textContent).toContain('Checkout')

    await clickButton('End')
    await flushPromises()
    expect(apiMock.endBrowserRecordingTransaction).toHaveBeenCalledWith('brec-1', 'Checkout')
    expect(container.querySelector('[data-testid="open-transaction"]')).toBeNull()
  })

  it('adds a note for the next action', async () => {
    apiMock.addBrowserRecordingNote.mockResolvedValue({
      session: recordingSession,
      marker: { kind: 'note', text: 'watch the cart badge', timestamp: '2026-05-12T20:00:04Z' },
    })
    await renderPanel()

    await clickButton('Add Note')
    expect(apiMock.addBrowserRecordingNote).not.toHaveBeenCalled() // empty note rejected client-side

    const noteInput = container.querySelector('input[aria-label="Note"]') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(noteInput, 'watch the cart badge')
      noteInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await clickButton('Add Note')
    await flushPromises()

    expect(apiMock.addBrowserRecordingNote).toHaveBeenCalledWith('brec-1', 'watch the cart badge')
    expect(container.textContent).toContain('Note added')
  })

  it('hides live controls once the recording stops', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()

    expect(container.textContent).not.toContain('Pause Recording')
    expect(container.textContent).not.toContain('Focus Browser')
    expect(container.querySelector('input[aria-label="Transaction name"]')).toBeNull()
    expect(container.querySelector('input[aria-label="Navigate the recording browser"]')).toBeNull()
  })

  it('switches the recording section', async () => {
    await renderPanel()
    const teardown = container.querySelector('[data-testid="section-teardown"]') as HTMLButtonElement
    expect(teardown).not.toBeNull()
    await act(async () => { teardown.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()
    expect(apiMock.setBrowserRecordingSection).toHaveBeenCalledWith('brec-1', 'teardown')
  })

  it('toggles the verification picker', async () => {
    await renderPanel()
    const pick = container.querySelector('[data-testid="pick-mode-toggle"]') as HTMLButtonElement
    expect(pick).not.toBeNull()
    await act(async () => { pick.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()
    expect(apiMock.setBrowserRecordingPickMode).toHaveBeenCalledWith('brec-1', true)
    expect(container.textContent).toContain('Pick mode on')
  })

  it('excludes a step and deletes the last step', async () => {
    await renderPanel()
    // The fill step (#1) is an interaction → has an exclude (−) action.
    const excludeBtn = container.querySelector('.brp-step-actions button[title="Exclude"]') as HTMLButtonElement
    expect(excludeBtn).not.toBeNull()
    await act(async () => { excludeBtn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()
    expect(apiMock.updateBrowserRecordingStep).toHaveBeenCalledWith('brec-1', expect.any(Number), { excluded: true })

    await clickButton('Delete last step')
    await flushPromises()
    expect(apiMock.deleteLastBrowserRecordingStep).toHaveBeenCalledWith('brec-1')
  })

  it('marks a fill step as a secret', async () => {
    await renderPanel()
    const secretBtn = container.querySelector('.brp-step-actions button[title="Mark as secret"]') as HTMLButtonElement
    expect(secretBtn).not.toBeNull()
    await act(async () => { secretBtn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()
    expect(apiMock.updateBrowserRecordingStep).toHaveBeenCalledWith('brec-1', 1, { mark_secret: true })
  })

  it('renders a selector-quality badge from candidates', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({
      session: {
        ...recordingSession,
        steps: [{ seq: 1, kind: 'click', timestamp: '2026-05-12T20:00:01Z', candidates: [{ kind: 'css', value: '#go', confidence: 90 }] }],
      },
    })
    await renderPanel()
    const badge = container.querySelector('.brp-quality-badge')
    expect(badge?.textContent).toBe('90')
    expect(badge?.className).toContain('brp-quality-high')
  })

  it('shows and accepts transaction suggestions', async () => {
    apiMock.suggestBrowserRecordingTransactions.mockResolvedValue({ suggestions: [{ name: 'Cart', at_step_seq: 1, url: 'https://shop.example/cart' }] })
    apiMock.startBrowserRecordingTransaction.mockResolvedValue({ session: { ...recordingSession, open_transactions: ['Cart'] } })
    await renderPanel()
    await flushPromises()

    const block = container.querySelector('[data-testid="transaction-suggestions"]')
    expect(block?.textContent).toContain('Cart')
    await clickButton('Add')
    await flushPromises()
    expect(apiMock.startBrowserRecordingTransaction).toHaveBeenCalledWith('brec-1', 'Cart')
  })

  it('renders console/network annotations', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({
      session: {
        ...recordingSession,
        annotation_count: 1,
        annotations: [{ kind: 'request_failed', text: '500 https://shop.example/api', timestamp: '2026-05-12T20:00:03Z' }],
      },
    })
    await renderPanel()
    const block = container.querySelector('[data-testid="recording-annotations"]')
    expect(block?.textContent).toContain('500 https://shop.example/api')
  })

  it('verifies the recording and shows replay progress', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    const verify = container.querySelector('[data-testid="verify-recording"]') as HTMLButtonElement
    expect(verify).not.toBeNull()
    await act(async () => { verify.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()
    expect(apiMock.verifyBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.querySelector('[data-testid="replay-progress"]')).not.toBeNull()
  })

  it('passes split and dual-http options to convert', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    // Enable split + dual-http checkboxes (click toggles them false→true).
    const checks = Array.from(container.querySelectorAll('.brp-convert input[type="checkbox"]')) as HTMLInputElement[]
    expect(checks.length).toBe(2)
    for (const c of checks) {
      await act(async () => { c.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    }
    await clickButton('Save to Script Library')
    await flushPromises()
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      split_at_transactions: true,
      also_create_http_script: true,
    }))
  })

  it('saves bridge recordings by previewing locally and creating scripts remotely', async () => {
    const onImported = vi.fn()
    apiMock.browserRecorderBridge = { origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      script: { name: 'Checkout Flow', protocol: 'browser', actions: [] },
      scripts: [
        { name: 'Checkout Flow', protocol: 'browser', actions: [] },
        { name: 'Checkout Flow (HTTP)', protocol: 'http', actions: [] },
      ],
      warnings: [],
      stats: {},
      parameterization: { suggestions: [], summary: { total: 0 } },
    })
    apiMock.createScript
      .mockResolvedValueOnce({ id: 'remote-browser', name: 'Checkout Flow', protocol: 'browser', script: { name: 'Checkout Flow', protocol: 'browser', actions: [] } })
      .mockResolvedValueOnce({ id: 'remote-http', name: 'Checkout Flow (HTTP)', protocol: 'http', script: { name: 'Checkout Flow (HTTP)', protocol: 'http', actions: [] } })

    await renderPanel({ onImported })
    await clickButton('Save to Script Library')
    await flushPromises()

    expect(apiMock.convertBrowserRecording).not.toHaveBeenCalled()
    expect(apiMock.previewBrowserRecordingConversion).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    expect(apiMock.createScript).toHaveBeenCalledTimes(2)
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'remote-browser' }), expect.objectContaining({ id: 'remote-http' })] })
    expect(container.textContent).toContain('Saved 2 scripts to the remote script library')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
