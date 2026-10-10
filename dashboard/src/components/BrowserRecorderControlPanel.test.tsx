// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BrowserRecorderControlPanel from './BrowserRecorderControlPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { apiFetchMock, downloadBlobMock, hasDesktopApiCapabilityMock, saveApiResourceViaBridgeMock, startDesktopBrowserDownloadMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  downloadBlobMock: vi.fn(),
  hasDesktopApiCapabilityMock: vi.fn(),
  saveApiResourceViaBridgeMock: vi.fn(),
  startDesktopBrowserDownloadMock: vi.fn(),
}))

const apiMock = {
  getBrowserRecording: vi.fn(),
  getRawBrowserRecording: vi.fn(),
  stopBrowserRecording: vi.fn(),
  forceStopBrowserRecording: vi.fn(),
  pauseBrowserRecording: vi.fn(),
  resumeBrowserRecording: vi.fn(),
  navigateBrowserRecording: vi.fn(),
  focusBrowserRecording: vi.fn(),
  startBrowserRecordingTransaction: vi.fn(),
  endBrowserRecordingTransaction: vi.fn(),
  addBrowserRecordingNote: vi.fn(),
  previewBrowserRecordingConversion: vi.fn(),
  convertBrowserRecording: vi.fn(),
  attestBridgedBrowserRecordingImport: vi.fn(),
  setBrowserRecordingSection: vi.fn(),
  setBrowserRecordingPickMode: vi.fn(),
  deleteLastBrowserRecordingStep: vi.fn(),
  updateBrowserRecordingStep: vi.fn(),
  suggestBrowserRecordingTransactions: vi.fn(),
  verifyBrowserRecording: vi.fn(),
  exportBrowserRecording: vi.fn(),
  importSalesforce: vi.fn(),
  applySecretBindings: vi.fn(),
  applyImportParameterization: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
  commitGenericImportBatch: vi.fn(),
  createScript: vi.fn(),
  updateScript: vi.fn(),
  deleteScript: vi.fn(),
  browserRecorderBridge: null as any,
  browserRecorderRequestScope: 'api:http://controller.test',
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: () => ({ connectionState: 'open' }),
}))

vi.mock('../hooks/apiClient', () => ({
  apiFetch: apiFetchMock,
  hasDesktopApiCapability: () => hasDesktopApiCapabilityMock(),
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: downloadBlobMock,
}))

vi.mock('../desktop/desktopFiles', () => ({
  saveApiResourceViaBridge: (...args: unknown[]) => saveApiResourceViaBridgeMock(...args),
  startDesktopBrowserDownload: (...args: unknown[]) => startDesktopBrowserDownloadMock(...args),
}))

vi.mock('./BrowserReplayProgress', () => ({
  default: ({ replaySessionId, onApplySuggestion, onComplete }: { replaySessionId: string; onApplySuggestion?: (suggestion: any) => void; onComplete?: (report: any, events: any[]) => void }) => (
    <div data-testid="replay-progress">
      <span>Replay {replaySessionId}</span>
      <button
        type="button"
        onClick={() => onApplySuggestion?.({ id: 'repair-unsupported', title: 'Patch wait', description: 'Change timeout', draft_patch_type: 'update_timeout', fields: {} })}
      >
        Apply Unsupported Repair
      </button>
      <button
        type="button"
        onClick={() => onComplete?.({ success: true, events: [] }, [])}
      >
        Complete Replay
      </button>
    </div>
  ),
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

const forceStoppedSession = {
  ...stoppedSession,
  stop_reason: 'forced',
  warnings: ['graceful stop did not finish; force-finished recording and preserved captured browser steps'],
}

const stoppingSession = {
  ...recordingSession,
  status: 'stopping',
}

const salesforceStoppedSession = {
  ...stoppedSession,
  start_url: 'https://acme.my.salesforce.com/lightning/setup',
  current_url: 'https://acme.lightning.force.com/lightning/page/home',
  warnings: ['Salesforce traffic detected; use /api/v1/scripts/import/salesforce for the native HTTP draft.'],
}

const credentialScript = {
  name: 'Checkout Flow',
  protocol: 'browser',
  project_id: 'project-1',
  config: { http: { auth: { password: '[REDACTED]' } } },
  actions: [{ name: 'Navigate', type: 'navigate', target: 'https://shop.example/login' }],
}

const credentialRequirement = {
  id: 'credential-password',
  path: '/config/http/auth/password',
  occurrence: 0,
  label: 'Password',
  context: 'Script configuration',
  suggested_name: 'LOGIN_PASSWORD',
  script_index: 0,
  script_name: 'Checkout Flow',
}

function credentialPreviewResult() {
  return {
    scripts: [credentialScript],
    script: credentialScript,
    warnings: [],
    unsupported_elements: [],
    assumptions: [],
    review_sections: [],
    repair_suggestions: [],
    correlations: [],
    stats: {},
    summary: {},
    parameterization: { suggestions: [], summary: { total: 0 } },
    secret_binding_requirements: [credentialRequirement],
  }
}

describe('BrowserRecorderControlPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    sessionStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiFetchMock.mockResolvedValue({
      ok: true,
      blob: vi.fn().mockResolvedValue(new Blob(['{"log":{}}'])),
    })
    apiMock.getBrowserRecording.mockResolvedValue({ session: recordingSession })
    apiMock.getRawBrowserRecording.mockRejectedValue(Object.assign(new Error('raw view forbidden'), { status: 403 }))
    apiMock.stopBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.forceStopBrowserRecording.mockResolvedValue({ session: forceStoppedSession })
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
    apiMock.attestBridgedBrowserRecordingImport.mockImplementation(async (scripts: any[]) => ({
      script: scripts[0],
      scripts,
      warnings: [],
      stats: {},
    }))
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
    apiMock.commitGenericImportBatch.mockImplementation(async ({ scripts }: { scripts: any[] }) => ({
      count: scripts.length,
      scripts: scripts.map((script, index) => ({
        id: `script-remote-${index + 1}`,
        name: script.name,
        protocol: script.protocol,
        version: '1',
        created_at: '2026-05-12T20:10:00Z',
        script: { ...script, actions: [] },
      })),
    }))
    apiMock.updateScript.mockImplementation(async (id: string, script: any) => ({
      id,
      name: script.name,
      protocol: script.protocol,
      version: '2',
      script,
    }))
    apiMock.deleteScript.mockResolvedValue({ deleted: 'script-remote-1' })
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: _projectId,
      capabilities: {
        provider: 'database_encrypted',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
      secrets: names.map(name => ({ name, configured: false })),
    }))
    apiMock.createProjectSecrets.mockResolvedValue({ secrets: [], count: 0 })
    apiMock.migrateImportSecrets.mockResolvedValue({ secrets: [], count: 0 })
    apiMock.discardImportSecretSession.mockResolvedValue(undefined)
    apiMock.browserRecorderBridge = null
    apiMock.browserRecorderRequestScope = 'api:http://controller.test'
    saveApiResourceViaBridgeMock.mockResolvedValue('unavailable')
    startDesktopBrowserDownloadMock.mockResolvedValue(undefined)
    hasDesktopApiCapabilityMock.mockReturnValue(false)
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

  async function setInputValue(input: HTMLInputElement, value: string) {
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  it('shows live agent-reported environment and retains browser and HTTP receipts in scoped output reviews', async () => {
    const environment = { version: 1, observation_source: 'local_recorder', engine: 'chromium', device_profile: 'ipad_air', profile_browser_family: 'webkit', emulated: true, applied_context: { mobile: true, has_touch: true }, physical_device_qualification: 'unverified' }
    apiMock.browserRecorderBridge = { agent_url: 'http://127.0.0.1:8123', token: 'not-rendered' }
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, recording_environment: environment } })
    const metadata = { import: { preserved: { recording_environment: { ...environment, observation_source: 'agent_reported' } } } }
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({ scripts: [{ name: 'Browser', protocol: 'browser', config: { browser: { engine: 'webkit' } }, actions: [], metadata }, { name: 'HTTP', protocol: 'http', config: {}, actions: [], metadata }], warnings: [], stats: {} })
    await renderPanel()
    expect(container.querySelector('[aria-label="Recording environment"]')!.textContent).toContain('Recording agent reported')
    await clickButton('Preview Conversion')
    const browser = container.querySelector('[aria-label="Output 1: Browser"]')!
    const http = container.querySelector('[aria-label="Output 2: HTTP"]')!
    expect(browser.querySelector('[aria-label="Recording environment"]')!.textContent).toContain('differs')
    expect(http.querySelector('[aria-label="Recording environment"]')!.textContent).toContain('Not applicable (HTTP output)')
    expect(container.textContent).not.toContain('not-rendered')
  })

  it('invalidates current conversion verification when an in-flight refresh returns changed receipt observations', async () => {
    vi.useFakeTimers()
    try {
      const environment = { version: 1, observation_source: 'local_recorder', engine: 'chromium', emulated: false, physical_device_qualification: 'unverified' }
      let finishRefresh!: (value: unknown) => void
      apiMock.getBrowserRecording.mockResolvedValueOnce({ session: { ...recordingSession, recording_environment: environment } }).mockImplementationOnce(() => new Promise(resolve => { finishRefresh = resolve }))
      apiMock.stopBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, recording_environment: environment } })
      await renderPanel()
      await act(async () => { await vi.advanceTimersByTimeAsync(1600) })
      expect(finishRefresh).toBeDefined()
      await clickButton('Stop Recording')
      await clickButton('Preview Conversion')
      await clickButton('Verify output 1')
      await clickButton('Complete Replay')
      expect(container.textContent).toContain('Verification passed for this conversion')
      await act(async () => { finishRefresh({ session: { ...stoppedSession, recording_environment: { ...environment, engine: 'firefox' } } }); await Promise.resolve() })
      expect(container.textContent).not.toContain('Verification passed for this conversion')
      expect(container.textContent).toContain('Previous recording verification')
      expect(container.textContent).toContain('Conversion changed')
    } finally { vi.useRealTimers() }
  })

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
    expect(container.textContent).not.toContain('Save unverified draft')
    expect(apiMock.getRawBrowserRecording).not.toHaveBeenCalled()
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith('brec-1')
  })

  it('loads retained step thumbnails only after an explicit authorized request', async () => {
    const thumbnail = 'data:image/jpeg;base64,dGh1bWJuYWls'
    apiMock.getRawBrowserRecording.mockResolvedValue({
      session: {
        ...recordingSession,
        steps: [{ ...recordingSession.steps[1], thumbnail }],
      },
    })

    await renderPanel()

    expect(apiMock.getRawBrowserRecording).not.toHaveBeenCalled()
    expect(container.querySelector('.brp-step-thumbnail')).toBeNull()
    await clickButton('Load thumbnails')

    expect(container.querySelector<HTMLImageElement>('.brp-step-thumbnail')?.src).toBe(thumbnail)
    expect(apiMock.getRawBrowserRecording).toHaveBeenCalledTimes(1)
    expect(apiMock.getRawBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith('brec-1')
  })

  it('keeps live polling on the disclosure-safe session endpoint', async () => {
    vi.useFakeTimers()
    try {
      await renderPanel()
      apiMock.getBrowserRecording.mockClear()
      apiMock.getRawBrowserRecording.mockClear()

      await act(async () => {
        vi.advanceTimersByTime(3_100)
        await Promise.resolve()
        await Promise.resolve()
      })

      expect(apiMock.getBrowserRecording).toHaveBeenCalled()
      expect(apiMock.getRawBrowserRecording).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  it('ignores a late response from the previously selected recording session', async () => {
    let resolveFirst!: (value: { session: typeof recordingSession }) => void
    const firstRequest = new Promise<{ session: typeof recordingSession }>(resolve => {
      resolveFirst = resolve
    })
    const secondSession = {
      ...recordingSession,
      id: 'brec-2',
      name: 'Second Recording',
      current_url: 'https://second.example.test/',
    }
    apiMock.getBrowserRecording
      .mockImplementationOnce(() => firstRequest)
      .mockResolvedValueOnce({ session: secondSession })

    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-1" />)
      await Promise.resolve()
    })
    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-2" />)
    })
    await flushPromises()

    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith('brec-2')
    expect(container.textContent).toContain('Second Recording')

    await act(async () => {
      resolveFirst({ session: recordingSession })
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Second Recording')
    expect(container.textContent).not.toContain('Checkout Flow')
  })

  it('starts a fresh refresh and ignores the old response when the recorder API origin changes', async () => {
    let resolveOld!: (value: { session: typeof recordingSession }) => void
    const oldRequest = new Promise<{ session: typeof recordingSession }>(resolve => {
      resolveOld = resolve
    })
    const newOriginSession = {
      ...recordingSession,
      name: 'Local Bridge Recording',
      current_url: 'https://bridge.example.test/',
    }
    apiMock.getBrowserRecording
      .mockImplementationOnce(() => oldRequest)
      .mockResolvedValueOnce({ session: newOriginSession })

    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-1" />)
      await Promise.resolve()
    })
    apiMock.browserRecorderRequestScope = 'bridge:http://127.0.0.1:8765:bridge-session-2'
    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-1" />)
    })
    await flushPromises()

    expect(apiMock.getBrowserRecording).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('Local Bridge Recording')

    await act(async () => {
      resolveOld({ session: recordingSession })
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Local Bridge Recording')
    expect(container.textContent).not.toContain('Checkout Flow')
  })

  it('ignores late suggestions and action results from the previously selected session', async () => {
    let resolveSuggestions!: (value: { suggestions: Array<{ name: string; at_step_seq: number; url: string }> }) => void
    let resolveStop!: (value: { session: typeof stoppedSession }) => void
    const suggestionsRequest = new Promise<{ suggestions: Array<{ name: string; at_step_seq: number; url: string }> }>(resolve => {
      resolveSuggestions = resolve
    })
    const stopRequest = new Promise<{ session: typeof stoppedSession }>(resolve => {
      resolveStop = resolve
    })
    const secondSession = {
      ...recordingSession,
      id: 'brec-2',
      name: 'Second Recording',
      navigation_count: 0,
      current_url: 'https://second.example.test/',
    }
    apiMock.getBrowserRecording
      .mockResolvedValueOnce({ session: recordingSession })
      .mockResolvedValueOnce({ session: secondSession })
    apiMock.suggestBrowserRecordingTransactions.mockImplementationOnce(() => suggestionsRequest)
    apiMock.stopBrowserRecording.mockImplementationOnce(() => stopRequest)

    await renderPanel()
    await clickButton('Stop Recording')
    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-2" />)
    })
    await flushPromises()

    await act(async () => {
      resolveSuggestions({ suggestions: [{ name: 'Old Cart', at_step_seq: 1, url: 'https://shop.example/cart' }] })
      resolveStop({ session: stoppedSession })
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Second Recording')
    expect(container.textContent).not.toContain('Old Cart')
    expect(container.textContent).not.toContain('Save as Script')
  })

  it('caps the unfiltered live step feed at 25 rows', async () => {
    const steps = Array.from({ length: 30 }, (_, index) => ({
      seq: index + 1,
      kind: 'click',
      value: `step-${index + 1}`,
      title: `Step ${index + 1}`,
      timestamp: '2026-05-12T20:00:01Z',
      url: 'https://shop.example/login',
    }))
    apiMock.getBrowserRecording.mockResolvedValue({
      session: {
        ...recordingSession,
        step_count: steps.length,
        steps,
      },
    })
    await renderPanel()

    expect(container.querySelectorAll('.rec-feed-item').length).toBe(25)
    expect(container.textContent).toContain('#30')
    expect(container.textContent).not.toContain('#5')
  })

  it('caps stopped step history until expanded and filters it with search', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({
      session: {
        ...stoppedSession,
        step_count: 30,
        steps: Array.from({ length: 30 }, (_, index) => ({
          seq: index + 1,
          kind: index === 0 ? 'fill' : 'click',
          value: index === 0 ? 'earliest-step' : `step-${index + 1}`,
          title: `Step ${index + 1}`,
          timestamp: '2026-05-12T20:00:01Z',
          url: 'https://shop.example/login',
        })),
      },
    })
    await renderPanel()

    const visibleStepNumbers = () => Array.from(container.querySelectorAll('.brp-step-seq')).map(node => node.textContent)
    expect(visibleStepNumbers()).toContain('#1')
    expect(visibleStepNumbers()).not.toContain('#30')
    expect(container.querySelectorAll('.rec-feed-item')).toHaveLength(25)
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Show all 30 steps')?.click()
    })
    expect(visibleStepNumbers()).toContain('#1')
    expect(container.querySelectorAll('.rec-feed-item')).toHaveLength(30)
    const search = container.querySelector<HTMLInputElement>('input[aria-label="Search recorded steps"]')
    expect(search).not.toBeNull()
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(search, 'earliest-step')
      search!.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(container.textContent).toContain('1 match')
    expect(container.textContent).toContain('earliest-step')
    expect(container.textContent).not.toContain('step-30')
  })

  it('renders long console and network annotations inside warning rows', async () => {
    const longIssue = 'https://api.shop.example/v1/cart/checkout/really-long-path-without-natural-breaks?trace=abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789 failed with ERR_CONNECTION_RESET'
    apiMock.getBrowserRecording.mockResolvedValue({
      session: {
        ...recordingSession,
        annotation_count: 1,
        annotations: [
          { kind: 'request_failed', text: longIssue, timestamp: '2026-05-12T20:00:03Z' },
        ],
      },
    })

    await renderPanel({ standalone: true })

    const annotations = container.querySelector('[data-testid="recording-annotations"]')
    expect(annotations).not.toBeNull()
    const warning = annotations!.querySelector('.brp-warning')
    expect(warning).not.toBeNull()
    expect(warning!.textContent).toContain('[request]')
    expect(warning!.textContent).toContain(longIssue)
  })

  it('stops the recording and reveals conversion controls', async () => {
    await renderPanel()
    await clickButton('Stop Recording')
    await flushPromises()

    expect(apiMock.stopBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.textContent).toContain('Stopped')
    expect(container.textContent).toContain('Save as Script')
    expect(container.textContent).toContain('Save unverified draft')
    // The capture-time masking warning surfaces to the user.
    expect(container.textContent).toContain('{{env.PASSWORD}}')
  })

  it('shows force finish when graceful stop is still finalizing', async () => {
    apiMock.stopBrowserRecording.mockResolvedValueOnce({ session: stoppingSession })
    await renderPanel()
    await clickButton('Stop Recording')
    await flushPromises()

    expect(apiMock.stopBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.textContent).toContain('Stopping')
    expect(container.textContent).toContain('Stop requested')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent?.includes('Stop Recording'))).toBe(false)
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent?.includes('Force Finish'))).toBe(true)
  })

  it('force finishes a stuck browser recording while preserving captured steps', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppingSession })
    await renderPanel()
    await clickButton('Force Finish')
    await flushPromises()

    expect(apiMock.forceStopBrowserRecording).toHaveBeenCalledWith('brec-1')
    expect(container.textContent).toContain('Force finished')
    expect(container.textContent).toContain('captured steps preserved')
    expect(container.textContent).toContain('graceful stop did not finish')
    expect(container.textContent).toContain('Save unverified draft')
  })

  it('previews the conversion with parameterization summary', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Preview Conversion')
    await flushPromises()

    expect(apiMock.previewBrowserRecordingConversion).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    expect(container.textContent).toContain('0 init · 2 main · 0 teardown actions')
    expect(container.textContent).toContain('3 parameterization suggestions')
  })

  it('discards the superseded protected source session when previewing again', async () => {
    const session = { ...stoppedSession, project_id: 'project-1' }
    const first = {
      script: { name: 'Checkout Flow', project_id: 'project-1', protocol: 'browser', actions: [] },
      warnings: [],
      stats: {},
      parameterization: { suggestions: [], summary: { total: 0 } },
      source_secret_session_id: 'source-session-old',
    }
    const second = {
      ...first,
      source_secret_session_id: 'source-session-new',
    }
    apiMock.getBrowserRecording.mockResolvedValue({ session })
    apiMock.previewBrowserRecordingConversion
      .mockResolvedValueOnce(first)
      .mockResolvedValueOnce(second)

    await renderPanel()
    await clickButton('Preview Conversion')
    await flushPromises()
    await clickButton('Preview Conversion')
    await flushPromises()

    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'source-session-old',
    })
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'source-session-new',
    })
  })

  it('applies preview parameterization to the browser script that is saved', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValueOnce({
      script: {
        name: 'Checkout Flow',
        version: '1.0.0',
        protocol: 'browser',
        actions: [{ name: 'Open order', type: 'navigate', target: 'https://shop.example/orders?order_id=42' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: {},
      summary: {},
      parameterization: {
        suggestions: [{
          id: 'pz_browser_order',
          kind: 'data_param',
          var_name: 'order_id',
          replacement: '{{var.order_id}}',
          sample_value: '42',
          confidence: 0.9,
          rule_id: 'test.browser.order',
          rationale: 'Order id should vary by virtual user',
          auto_apply: true,
          high_risk: false,
          locations: [{ section: 'actions', action_index: 0, action_name: 'Open order', field: 'target' }],
        }],
        summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
    })
    apiMock.applyImportParameterization.mockResolvedValueOnce({
      script: {
        name: 'Checkout Flow',
        version: '1.0.0',
        protocol: 'browser',
        actions: [{ name: 'Open order', type: 'navigate', target: 'https://shop.example/orders?order_id={{var.order_id}}' }],
      },
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_browser_order' }],
      applied_count: 1,
    })

    await renderPanel({ folderId: 'browser-script-folder' })
    await clickButton('Preview Conversion')
    await flushPromises()
    expect(container.textContent).toContain('Parameterization review — Checkout Flow (1)')
    await act(async () => {
      container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    await vi.waitFor(() => expect(apiMock.applyImportParameterization).toHaveBeenCalledTimes(1))
    await flushPromises()
    expect(container.querySelector('.paramize-applied-badge')?.textContent).toContain('Applied 1 change')
    expect(container.querySelector<HTMLInputElement>('.paramize-item input')?.checked).toBe(true)
    await clickButton('Save unverified draft')
    await flushPromises()
    await vi.waitFor(() => expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(1))

    expect(apiMock.applyImportParameterization).toHaveBeenCalledWith(expect.objectContaining({
      selected_ids: ['pz_browser_order'],
    }))
    // Persistence goes through one server-side transaction. The UI sends only
    // the selected ID; no fallible post-commit update is needed.
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      folder_id: 'browser-script-folder',
      expected_script_count: 1,
      parameterization_selections: [{
        script_index: 0,
        selected_ids: ['pz_browser_order'],
      }],
    }))
    expect(apiMock.updateScript).not.toHaveBeenCalled()
  })

  it('reports nested save work through the composite busy callback', async () => {
    const busyChanges = vi.fn()
    let resolveSave!: (value: unknown) => void
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.convertBrowserRecording.mockReturnValueOnce(new Promise(resolve => { resolveSave = resolve }))

    await renderPanel({ onOperationBusyChange: busyChanges })
    await clickButton('Preview Conversion')
    await flushPromises()
    busyChanges.mockClear()

    act(() => {
      const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Save unverified draft'))!
      save.click()
    })
    await vi.waitFor(() => expect(busyChanges).toHaveBeenCalledWith(true))

    await act(async () => {
      resolveSave({
        script: {
          id: 'saved-composite-busy',
          name: 'Checkout Flow',
          protocol: 'browser',
          version: '1',
          created_at: '2026-05-12T20:10:00Z',
          script: { name: 'Checkout Flow', protocol: 'browser', actions: [] },
        },
        warnings: [],
        stats: {},
      })
      await Promise.resolve()
    })
    await vi.waitFor(() => expect(busyChanges).toHaveBeenLastCalledWith(false))
  })

  it('scopes split-script parameterization and saves the updated target script', async () => {
    const first = {
      name: 'Checkout',
      version: '1.0.0',
      protocol: 'browser',
      actions: [{ name: 'Open order', type: 'navigate', target: 'https://shop.example/orders?order_id=42' }],
    }
    const second = {
      name: 'Logout',
      version: '1.0.0',
      protocol: 'browser',
      actions: [{ name: 'Open session', type: 'navigate', target: 'https://shop.example/logout?session_id=99' }],
    }
    const checkoutSuggestion = {
      id: 'pz_checkout_order',
      kind: 'data_param',
      var_name: 'order_id',
      replacement: '{{var.order_id}}',
      sample_value: '42',
      confidence: 0.9,
      rule_id: 'test.browser.order',
      rationale: 'Order id should vary by virtual user',
      auto_apply: true,
      high_risk: false,
      script_name: 'Checkout',
      locations: [{ section: 'actions', action_index: 0, action_name: 'Open order', field: 'target' }],
    }
    const logoutSuggestion = {
      id: 'pz_logout_session',
      kind: 'data_param',
      var_name: 'session_id',
      replacement: '{{var.session_id}}',
      sample_value: '99',
      confidence: 0.9,
      rule_id: 'test.browser.session',
      rationale: 'Session id should vary by virtual user',
      auto_apply: true,
      high_risk: false,
      script_name: 'Logout',
      locations: [{ section: 'actions', action_index: 0, action_name: 'Open session', field: 'target' }],
    }
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValueOnce({
      script: first,
      scripts: [first, second],
      warnings: [],
      stats: {},
      parameterization: {
        suggestions: [checkoutSuggestion, logoutSuggestion],
        summary: { total: 2, by_kind: { data_param: 2 }, auto_apply_count: 2, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
    })
    const parameterizedSecond = {
      ...second,
      actions: [{ ...second.actions[0], target: 'https://shop.example/logout?session_id={{var.session_id}}' }],
    }
    apiMock.applyImportParameterization.mockResolvedValueOnce({
      script: parameterizedSecond,
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_logout_session' }],
      applied_count: 1,
    })
    apiMock.convertBrowserRecording.mockResolvedValueOnce({
      script: { id: 'saved-checkout', name: 'Checkout', protocol: 'browser', version: '1', script: first },
      scripts: [
        { id: 'saved-checkout', name: 'Checkout', protocol: 'browser', version: '1', script: first },
        { id: 'saved-logout', name: 'Logout', protocol: 'browser', version: '1', script: second },
      ],
      warnings: [],
      stats: {},
    })

    await renderPanel()
    await clickButton('Preview Conversion')
    await flushPromises()

    const reviews = Array.from(container.querySelectorAll<HTMLElement>('.paramize-review'))
    expect(reviews).toHaveLength(2)
    expect(reviews[0].textContent).toContain('Checkout')
    expect(reviews[0].textContent).toContain('order_id')
    expect(reviews[0].textContent).not.toContain('session_id')
    expect(reviews[1].textContent).toContain('Logout')
    expect(reviews[1].textContent).toContain('session_id')
    expect(reviews[1].textContent).not.toContain('order_id')

    await act(async () => {
      reviews[1].querySelector<HTMLButtonElement>('button.btn-primary')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    await vi.waitFor(() => expect(apiMock.applyImportParameterization).toHaveBeenCalledTimes(1))
    await clickButton('Save unverified draft')
    await flushPromises()
    await vi.waitFor(() => expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(1))

    expect(apiMock.applyImportParameterization).toHaveBeenCalledWith(expect.objectContaining({
      script: second,
      suggestions: [logoutSuggestion],
      selected_ids: ['pz_logout_session'],
    }))
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(1)
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      expected_script_count: 2,
      parameterization_selections: [{
        script_index: 1,
        selected_ids: ['pz_logout_session'],
      }],
    }))
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(apiMock.updateScript).not.toHaveBeenCalled()
  })

  it('prompts for optional credentials and saves a configure-later reference', async () => {
    const onImported = vi.fn()
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue(credentialPreviewResult())
    apiMock.applySecretBindings.mockResolvedValue({
      script: {
        ...credentialScript,
        config: { http: { auth: { password: '{{secret.LOGIN_PASSWORD}}' } } },
      },
      secret_binding_requirements: [],
    })

    await renderPanel({ onImported })
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(container.textContent).toContain('Credential groups (1)')
    expect(container.textContent).toContain('Configure later')
    expect(container.textContent).toContain('Save with 1 secret to configure')
    expect(apiMock.createScript).not.toHaveBeenCalled()

    await clickButton('Save with 1 secret to configure')
    await flushPromises()

    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: credentialScript,
      script_index: 0,
      bindings: [{ requirement_id: 'credential-password', secret_name: 'LOGIN_PASSWORD' }],
    })
    // The server-side conversion endpoint persists the draft and applies the
    // same suggested reference; the kept suggestion needs no follow-up update.
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(apiMock.updateScript).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-9' })] })
  })

  it('keeps a preservation-marked browser conversion runnable without applying empty bindings', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()
    const preservedScript = {
      ...credentialScript,
      config: { http: { auth: { password: 'imported-runtime-password' } } },
      metadata: {
        import: {
          source: 'browser_recorder',
          preserved: { import_value_policy: 'preserve' },
        },
      },
    }
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      ...credentialPreviewResult(),
      import_draft_expires_at: '2099-07-26T12:30:00Z',
      script: preservedScript,
      scripts: [preservedScript],
      secret_binding_requirements: [{
        ...credentialRequirement,
        destination_value_preserved: true,
      }],
    })

    await renderPanel({ onImported, onEdit })
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(container.textContent).toContain('Preserved in script')
    expect(container.textContent).toContain('0 bindings need setup')
    expect(container.textContent).not.toContain('Save with 1 secret to configure')

    expect(container.textContent).toContain('Save and open in Visual Editor')
    await clickButton('Save and open in Visual Editor')
    await flushPromises()

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(1)
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-9' })] })
    expect(onEdit).not.toHaveBeenCalled()
  })

  it('live-disables credential and parameterization controls when a preserved browser draft expires', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-28T12:00:00Z'))
    try {
      const preservedScript = {
        ...credentialScript,
        config: { http: { auth: { password: 'operator-known-browser-password' } } },
        metadata: {
          import: {
            source: 'browser_recorder',
            preserved: { import_value_policy: 'preserve' },
          },
        },
      }
      apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
      apiMock.previewBrowserRecordingConversion.mockResolvedValue({
        ...credentialPreviewResult(),
        import_draft_expires_at: '2026-07-28T12:00:01Z',
        script: preservedScript,
        scripts: [preservedScript],
        secret_binding_requirements: [{
          ...credentialRequirement,
          destination_value_preserved: true,
        }],
        parameterization: {
          suggestions: [{
            id: 'expiring-order-id',
            kind: 'data_param',
            var_name: 'ORDER_ID',
            replacement: '{{param.ORDER_ID}}',
            sample_value: '123',
            confidence: 0.95,
            rule_id: 'test.browser.expiry',
            rationale: 'Order id should vary by virtual user',
            auto_apply: true,
            high_risk: false,
          }],
          summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
        },
      })

      await renderPanel({ onEdit: vi.fn() })
      await clickButton('Preview Conversion')
      await flushPromises()

      const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Save unverified draft'))!
      const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Save and open in Visual Editor'))!
      const applyParameterization = container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')!
      const useProjectSecret = container.querySelector<HTMLInputElement>('input[id$="-project-secret"]')!
      expect(save.disabled).toBe(false)
      expect(edit.disabled).toBe(false)
      expect(applyParameterization.disabled).toBe(false)
      expect(useProjectSecret.disabled).toBe(false)
      expect(container.textContent).toContain('0 bindings need setup')

      await act(async () => {
        vi.advanceTimersByTime(1_011)
        await Promise.resolve()
      })

      expect(container.textContent).toContain('credential-preserving import draft has expired')
      expect(save.disabled).toBe(true)
      expect(edit.disabled).toBe(true)
      expect(applyParameterization.disabled).toBe(true)
      expect(useProjectSecret.disabled).toBe(true)
      expect(Array.from(container.querySelectorAll<HTMLInputElement | HTMLButtonElement>(
        '.secret-binding-review input, .secret-binding-review button',
      )).every(control => control.disabled)).toBe(true)
      const refreshPreview = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Preview Conversion'))!
      expect(refreshPreview.disabled).toBe(false)

      await act(async () => {
        save.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        edit.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        applyParameterization.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        useProjectSecret.dispatchEvent(new Event('change', { bubbles: true }))
        await Promise.resolve()
      })
      expect(apiMock.convertBrowserRecording).not.toHaveBeenCalled()
      expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
      expect(apiMock.applyImportParameterization).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  it('applies an unchanged suggested project-secret choice inside the headed save conversion', async () => {
    const onImported = vi.fn()
    const preservedScript = {
      ...credentialScript,
      config: { http: { auth: { password: 'captured-browser-password' } } },
      metadata: {
        import: {
          source: 'browser_recorder',
          preserved: { import_value_policy: 'preserve' },
        },
      },
    }
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      ...credentialPreviewResult(),
      script: preservedScript,
      scripts: [preservedScript],
      secret_binding_requirements: [{
        ...credentialRequirement,
        destination_value_preserved: true,
      }],
    })
    apiMock.applySecretBindings.mockResolvedValue({
      script: {
        ...preservedScript,
        config: { http: { auth: { password: '{{secret.LOGIN_PASSWORD}}' } } },
      },
      secret_binding_requirements: [],
    })

    await renderPanel({ onImported })
    await clickButton('Save unverified draft')
    await flushPromises()
    expect(container.textContent).toContain('Preserved in script')

    await act(async () => {
      container.querySelector<HTMLInputElement>('input[id$="-project-secret"]')?.click()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Save with 1 secret to configure')
    await clickButton('Save with 1 secret to configure')
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: preservedScript,
      script_index: 0,
      bindings: [{ requirement_id: 'credential-password', secret_name: 'LOGIN_PASSWORD' }],
    })
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      secret_bindings: [{
        requirement_id: 'credential-password',
        secret_name: 'LOGIN_PASSWORD',
        script_index: 0,
      }],
    }))
    expect(apiMock.updateScript).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-9' })] })
  })

  it('stores an optional credential value before saving the prepared script', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue(credentialPreviewResult())
    apiMock.applySecretBindings.mockResolvedValue({
      script: {
        ...credentialScript,
        config: { http: { auth: { password: '{{secret.LOGIN_PASSWORD}}' } } },
      },
      secret_binding_requirements: [],
    })

    await renderPanel()
    await clickButton('Preview Conversion')
    await flushPromises()

    const value = container.querySelector<HTMLInputElement>('input[aria-label="Secret value for LOGIN_PASSWORD"]')
    expect(value).not.toBeNull()
    await setInputValue(value!, 'entered-at-conversion')
    expect(container.textContent).toContain('Will be created')

    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'LOGIN_PASSWORD',
      value: 'entered-at-conversion',
    }])
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(1)
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('explains durable project-secret recovery and reuses the configured value on headed save retry', async () => {
    const onImported = vi.fn()
    const safeScript = {
      ...credentialScript,
      config: { http: { auth: { password: '{{secret.LOGIN_PASSWORD}}' } } },
    }
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue(credentialPreviewResult())
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: safeScript,
      secret_binding_requirements: [],
    })
    apiMock.convertBrowserRecording
      .mockRejectedValueOnce(new Error('temporary headed script-store failure'))
      .mockResolvedValueOnce({
        script: {
          id: 'script-retry',
          name: 'Checkout Flow',
          protocol: 'browser',
          version: '1',
          created_at: '2026-05-12T20:10:00Z',
          script: safeScript,
        },
        warnings: [],
        stats: {},
      })

    await renderPanel({ onImported })
    await clickButton('Preview Conversion')
    await flushPromises()

    const valueInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="Secret value for LOGIN_PASSWORD"]',
    )!
    await setInputValue(valueInput, 'write-only-browser-value')
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'LOGIN_PASSWORD',
      value: 'write-only-browser-value',
    }])
    expect(container.textContent).toContain(
      'Project-secret values configured during this review remain in Project Secrets independently of script saving',
    )
    expect(container.textContent).toContain('retrying this save reuses those configured values')
    expect(container.textContent).toContain('applies to scripts, not external secret-store writes')
    expect(container.textContent).toContain('temporary headed script-store failure')

    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(2)
    expect(onImported).toHaveBeenCalledWith({
      scripts: [expect.objectContaining({ id: 'script-retry' })],
    })
  })

  it('applies credential references across every split conversion script', async () => {
    const first = { ...credentialScript, name: 'Checkout', actions: [{ name: 'Checkout', type: 'navigate', target: '/' }] }
    const second = { ...credentialScript, name: 'Logout', actions: [{ name: 'Logout', type: 'navigate', target: '/logout' }] }
    const requirements = [
      { ...credentialRequirement, id: 'checkout-secret', suggested_name: 'CHECKOUT_TOKEN', script_index: 0, script_name: 'Checkout' },
      { ...credentialRequirement, id: 'logout-secret', suggested_name: 'LOGOUT_TOKEN', script_index: 1, script_name: 'Logout' },
    ]
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      ...credentialPreviewResult(),
      script: first,
      scripts: [first, second],
      secret_binding_requirements: requirements,
    })
    apiMock.applySecretBindings.mockImplementation(async ({ script, script_index }: any) => ({
      script: { ...script, config: { token: `{{secret.${script_index === 0 ? 'CHECKOUT_TOKEN' : 'LOGOUT_TOKEN'}}}` } },
      secret_binding_requirements: [],
    }))
    apiMock.convertBrowserRecording.mockResolvedValueOnce({
      script: { id: 'saved-checkout', name: 'Checkout', protocol: 'browser', version: '1', script: first },
      scripts: [
        { id: 'saved-checkout', name: 'Checkout', protocol: 'browser', version: '1', script: first },
        { id: 'saved-logout', name: 'Logout', protocol: 'browser', version: '1', script: second },
      ],
      warnings: [],
      stats: {},
    })

    await renderPanel()
    await clickButton('Preview Conversion')
    await flushPromises()
    expect(container.textContent).toContain('Credential groups (2)')
    expect(container.textContent).toContain('Save with 2 secrets to configure')

    await clickButton('Save with 2 secrets to configure')
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(2)
    expect(apiMock.applySecretBindings).toHaveBeenNthCalledWith(1, expect.objectContaining({ script_index: 0 }))
    expect(apiMock.applySecretBindings).toHaveBeenNthCalledWith(2, expect.objectContaining({ script_index: 1 }))
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(1)
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('submits split bridge scripts through one atomic batch and leaves no client-side cleanup path', async () => {
    const onImported = vi.fn()
    const first = { name: 'Checkout', protocol: 'browser', actions: [{ name: 'Checkout', type: 'navigate', target: '/' }] }
    const second = { name: 'Logout', protocol: 'browser', actions: [{ name: 'Logout', type: 'navigate', target: '/logout' }] }
    apiMock.browserRecorderBridge = { origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      script: first,
      scripts: [first, second],
      warnings: [],
      stats: {},
      parameterization: { suggestions: [], summary: { total: 0 } },
    })
    apiMock.commitGenericImportBatch.mockRejectedValueOnce(new Error('atomic batch rejected'))

    await renderPanel({ onImported })
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [first, second],
      source_profile: 'browser_recorder',
      operation_id: expect.stringMatching(/^browser-import-/),
    }))
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(apiMock.deleteScript).not.toHaveBeenCalled()
    expect(onImported).not.toHaveBeenCalled()
    expect(container.textContent).toContain('atomic batch rejected')
  })

  it('reuses the bridge batch operation id after an ambiguous save failure', async () => {
    const first = { name: 'Checkout', protocol: 'browser', actions: [{ name: 'Checkout', type: 'navigate', target: '/' }] }
    const second = { name: 'Logout', protocol: 'browser', actions: [{ name: 'Logout', type: 'navigate', target: '/logout' }] }
    apiMock.browserRecorderBridge = { origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      script: first,
      scripts: [first, second],
      warnings: [],
      stats: {},
      parameterization: { suggestions: [], summary: { total: 0 } },
    })
    apiMock.commitGenericImportBatch
      .mockRejectedValueOnce(new Error('connection reset after commit'))
      .mockResolvedValueOnce({
        count: 2,
        scripts: [
          { id: 'saved-checkout', name: first.name, protocol: first.protocol, script: first },
          { id: 'saved-logout', name: second.name, protocol: second.protocol, script: second },
        ],
      })

    await renderPanel()
    await clickButton('Save unverified draft')
    await flushPromises()
    const firstOperationID = apiMock.commitGenericImportBatch.mock.calls[0][0].operation_id
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledTimes(2)
    expect(apiMock.commitGenericImportBatch.mock.calls[1][0].operation_id).toBe(firstOperationID)
    expect(container.textContent).toContain('Saved 2 scripts')
  })

  it('prepares deferred credential references before opening the visual editor', async () => {
    const onEdit = vi.fn()
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...stoppedSession, project_id: 'project-1' } })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue(credentialPreviewResult())
    apiMock.applySecretBindings.mockResolvedValue({
      script: { ...credentialScript, config: { password: '{{secret.LOGIN_PASSWORD}}' } },
      secret_binding_requirements: [],
    })

    await renderPanel({ onEdit })
    await clickButton('Preview Conversion')
    await flushPromises()
    await clickButton('Open in Visual Editor')
    await flushPromises()

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      config: { password: '{{secret.LOGIN_PASSWORD}}' },
    }))
  })

  it('does not open a stale credential-bound preview after the recording session changes', async () => {
    const onEdit = vi.fn()
    const firstSession = { ...stoppedSession, project_id: 'project-1' }
    const secondSession = {
      ...stoppedSession,
      id: 'brec-2',
      name: 'Second Recording',
      project_id: 'project-2',
    }
    let resolveBindings!: (value: any) => void
    const bindingRequest = new Promise<any>(resolve => {
      resolveBindings = resolve
    })
    apiMock.getBrowserRecording
      .mockResolvedValueOnce({ session: firstSession })
      .mockResolvedValueOnce({ session: secondSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue(credentialPreviewResult())
    apiMock.applySecretBindings.mockImplementationOnce(() => bindingRequest)

    await renderPanel({ onEdit })
    await clickButton('Preview Conversion')
    await flushPromises()
    await clickButton('Open in Visual Editor')
    await flushPromises()
    expect(apiMock.applySecretBindings).toHaveBeenCalledOnce()

    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-2" onEdit={onEdit} />)
    })
    await flushPromises()
    expect(container.textContent).toContain('Second Recording')

    await act(async () => {
      resolveBindings({
        script: { ...credentialScript, config: { password: '{{secret.LOGIN_PASSWORD}}' } },
        secret_binding_requirements: [],
      })
      await bindingRequest
    })
    await flushPromises()

    expect(onEdit).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Second Recording')
  })

  it('saves through the server-side conversion endpoint and notifies onImported', async () => {
    const onImported = vi.fn()
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel({ onImported })
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.previewBrowserRecordingConversion).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    // createScript rejects headed-browser session ids server-side; the panel
    // must never fall back to it for embedded recordings.
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-9' })] })
    expect(container.textContent).toContain('saved to the script library')
  })

  it('prevents dismissing the panel while an atomic server save is in flight', async () => {
    const onDismiss = vi.fn()
    const onImported = vi.fn()
    let resolveSave!: (value: any) => void
    const saveRequest = new Promise<any>(resolve => {
      resolveSave = resolve
    })
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.convertBrowserRecording.mockImplementationOnce(() => saveRequest)

    await renderPanel({ onDismiss, onImported })
    await clickButton('Preview Conversion')
    await flushPromises()
    await clickButton('Save unverified draft')
    await flushPromises()

    const dismiss = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Dismiss') as HTMLButtonElement
    expect(dismiss.disabled).toBe(true)
    await act(async () => {
      dismiss.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onDismiss).not.toHaveBeenCalled()

    await act(async () => {
      resolveSave({
        script: {
          id: 'script-atomic',
          name: 'Checkout Flow',
          protocol: 'browser',
          version: '1',
          script: { name: 'Checkout Flow', protocol: 'browser', actions: [] },
        },
        warnings: [],
        stats: {},
      })
      await saveRequest
    })
    await flushPromises()

    expect(onImported).toHaveBeenCalledWith({
      scripts: [expect.objectContaining({ id: 'script-atomic' })],
    })
    expect(dismiss.disabled).toBe(false)
  })

  it('publishes an atomic save non-navigating when the panel switches sessions before completion', async () => {
    const onImported = vi.fn()
    let resolveSave!: (value: unknown) => void
    const saveRequest = new Promise(resolve => {
      resolveSave = resolve
    })
    const secondSession = {
      ...stoppedSession,
      id: 'brec-2',
      name: 'Second Recording',
    }
    apiMock.getBrowserRecording
      .mockResolvedValueOnce({ session: stoppedSession })
      .mockResolvedValueOnce({ session: secondSession })
    apiMock.convertBrowserRecording.mockImplementationOnce(() => saveRequest)

    await renderPanel({ onImported })
    await clickButton('Preview Conversion')
    await flushPromises()
    await clickButton('Save unverified draft')
    await flushPromises()

    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-2" onImported={onImported} />)
    })
    await flushPromises()

    await act(async () => {
      resolveSave({
        script: {
          id: 'script-late-session',
          name: 'Checkout Flow',
          protocol: 'browser',
          version: '1',
          script: { name: 'Checkout Flow', protocol: 'browser', actions: [] },
        },
        warnings: [],
        stats: {},
      })
      await saveRequest
    })
    await flushPromises()

    expect(onImported).toHaveBeenCalledWith({
      scripts: [expect.objectContaining({ id: 'script-late-session' })],
      stayInImportReview: true,
    })
    expect(container.textContent).toContain('Second Recording')
  })

  it('does not use a fallible follow-up update for reviewed parameterization', async () => {
    const onImported = vi.fn()
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValueOnce({
      script: {
        name: 'Checkout Flow',
        version: '1.0.0',
        protocol: 'browser',
        actions: [{ name: 'Open order', type: 'navigate', target: 'https://shop.example/orders?order_id=42' }],
      },
      warnings: [],
      stats: {},
      parameterization: {
        suggestions: [{
          id: 'pz_browser_order',
          kind: 'data_param',
          var_name: 'order_id',
          replacement: '{{var.order_id}}',
          sample_value: '42',
          confidence: 0.9,
          rule_id: 'test.browser.order',
          rationale: 'Order id should vary by virtual user',
          auto_apply: true,
          high_risk: false,
          locations: [{ section: 'actions', action_index: 0, action_name: 'Open order', field: 'target' }],
        }],
        summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
    })
    apiMock.applyImportParameterization.mockResolvedValueOnce({
      script: {
        name: 'Checkout Flow',
        version: '1.0.0',
        protocol: 'browser',
        actions: [{ name: 'Open order', type: 'navigate', target: 'https://shop.example/orders?order_id={{var.order_id}}' }],
      },
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_browser_order' }],
      applied_count: 1,
    })
    apiMock.updateScript.mockRejectedValueOnce(new Error('update denied'))

    await renderPanel({ onImported })
    await clickButton('Preview Conversion')
    await flushPromises()
    await act(async () => {
      container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    await vi.waitFor(() => expect(apiMock.applyImportParameterization).toHaveBeenCalledTimes(1))
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.convertBrowserRecording).toHaveBeenCalledTimes(1)
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      expected_script_count: 1,
      parameterization_selections: [{
        script_index: 0,
        selected_ids: ['pz_browser_order'],
      }],
    }))
    expect(apiMock.updateScript).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-9' })] })
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

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      '/api/v1/browser-recorder/sessions/brec-1/export?format=har',
      'checkout-flow.har',
    )
    expect(apiMock.exportBrowserRecording).toHaveBeenCalledWith('brec-1', 'har')
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), 'checkout-flow.har')
  })

  it('does not report a cancelled Blob save as a downloaded HAR', async () => {
    downloadBlobMock.mockResolvedValue('cancelled')
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Download HAR')
    await flushPromises()

    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), 'checkout-flow.har')
    expect(container.textContent).not.toContain('Side-car HAR downloaded')
  })

  it('streams embedded browser-recorder HAR exports through the native desktop bridge', async () => {
    saveApiResourceViaBridgeMock.mockResolvedValue('saved')
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Download HAR')
    await flushPromises()

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      '/api/v1/browser-recorder/sessions/brec-1/export?format=har',
      'checkout-flow.har',
    )
    expect(apiMock.exportBrowserRecording).not.toHaveBeenCalled()
    expect(downloadBlobMock).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Side-car HAR downloaded')
  })

  it('streams embedded HAR exports without Blob buffering in the desktop bootstrap browser', async () => {
    hasDesktopApiCapabilityMock.mockReturnValue(true)
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Download HAR')
    await flushPromises()

    expect(startDesktopBrowserDownloadMock).toHaveBeenCalledWith(
      '/api/v1/browser-recorder/sessions/brec-1/export?format=har',
      'checkout-flow.har',
    )
    expect(apiMock.exportBrowserRecording).not.toHaveBeenCalled()
    expect(downloadBlobMock).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Side-car HAR download request opened')
  })

  it('treats a cancelled native HAR save as cancellation, not success', async () => {
    saveApiResourceViaBridgeMock.mockResolvedValue('cancelled')
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Download HAR')
    await flushPromises()

    expect(apiMock.exportBrowserRecording).not.toHaveBeenCalled()
    expect(downloadBlobMock).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('Side-car HAR downloaded')
  })

  it('keeps remote recording-agent HAR exports on their authenticated bridge', async () => {
    apiMock.browserRecorderBridge = { origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Download HAR')
    await flushPromises()

    expect(saveApiResourceViaBridgeMock).not.toHaveBeenCalled()
    expect(apiMock.exportBrowserRecording).toHaveBeenCalledWith('brec-1', 'har')
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), 'checkout-flow.har')
  })

  it('saves a recording into the folder the studio is showing', async () => {
    // Recorder saves previously passed no folder at all, so a recording made
    // while a folder was open landed at the project root -- the same defect the
    // import surfaces had. Standalone popup mode still has no folder context and
    // correctly keeps passing null.
    apiMock.getBrowserRecording.mockResolvedValue({ session: salesforceStoppedSession })
    apiMock.createScript.mockResolvedValue({
      id: 'script-salesforce-1',
      name: 'Checkout Flow Salesforce HTTP',
      protocol: 'http',
      version: '1.0.0',
      created_at: '2026-05-12T20:10:00Z',
      script: { name: 'Checkout Flow Salesforce HTTP', protocol: 'http', actions: [] },
    })

    await renderPanel({ folderId: 'folder-9' })

    await clickButton('Review Salesforce HTTP Draft')
    await flushPromises()
    await clickButton('Save Salesforce HTTP Script')
    await flushPromises()

    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      folder_id: 'folder-9',
      source_profile: 'salesforce',
      scripts: [expect.objectContaining({ name: 'Checkout Flow Salesforce HTTP' })],
    }))
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

    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      folder_id: null,
      source_profile: 'salesforce',
      scripts: [expect.objectContaining({ name: 'Checkout Flow Salesforce HTTP' })],
    }))
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-remote-1' })] })
  })

  it('uses the deferred credential flow for Salesforce drafts before edit and save', async () => {
    const onEdit = vi.fn()
    const onImported = vi.fn()
    const salesforceScript = {
      name: 'Salesforce Login',
      protocol: 'http',
      project_id: 'project-1',
      config: { http: { auth: { token: '[REDACTED]' } } },
      actions: [{ name: 'Aura', type: 'request', target: '/aura' }],
    }
    apiMock.getBrowserRecording.mockResolvedValue({ session: { ...salesforceStoppedSession, project_id: 'project-1' } })
    apiMock.importSalesforce.mockResolvedValue({
      ...credentialPreviewResult(),
      script: salesforceScript,
      scripts: [salesforceScript],
      secret_binding_requirements: [{
        ...credentialRequirement,
        id: 'salesforce-token',
        path: '/config/http/auth/token',
        label: 'Token',
        suggested_name: 'SALESFORCE_TOKEN',
        script_name: 'Salesforce Login',
      }],
      summary: { salesforce_review: { detected: true } },
    })
    apiMock.applySecretBindings.mockResolvedValue({
      script: { ...salesforceScript, config: { http: { auth: { token: '{{secret.SALESFORCE_TOKEN}}' } } } },
      secret_binding_requirements: [],
    })
    apiMock.createScript.mockResolvedValue({
      id: 'saved-salesforce-login',
      name: 'Salesforce Login',
      protocol: 'http',
      version: '1',
    })

    await renderPanel({ onEdit, onImported })
    await clickButton('Review Salesforce HTTP Draft')
    await flushPromises()

    expect(container.querySelector('input[aria-label="Secret value for SALESFORCE_TOKEN"]')).not.toBeNull()
    expect(container.textContent).toContain('Save with 1 secret to configure')

    await clickButton('Open Salesforce Draft')
    await flushPromises()
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      config: { http: { auth: { token: '{{secret.SALESFORCE_TOKEN}}' } } },
    }))

    await clickButton('Save Salesforce HTTP Script')
    await flushPromises()
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      source_profile: 'salesforce',
      scripts: [expect.objectContaining({
        config: { http: { auth: { token: '{{secret.SALESFORCE_TOKEN}}' } } },
      })],
    }))
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'script-remote-1' })] })
  })

  it('does not open a stale credential-bound Salesforce draft after the recording session changes', async () => {
    const onEdit = vi.fn()
    const salesforceScript = {
      name: 'Salesforce Login',
      protocol: 'http',
      project_id: 'project-1',
      config: { token: '[REDACTED]' },
      actions: [{ name: 'Aura', type: 'request', target: '/aura' }],
    }
    const secondSession = {
      ...stoppedSession,
      id: 'brec-2',
      name: 'Second Recording',
      project_id: 'project-2',
    }
    let resolveBindings!: (value: any) => void
    const bindingRequest = new Promise<any>(resolve => {
      resolveBindings = resolve
    })
    apiMock.getBrowserRecording
      .mockResolvedValueOnce({ session: { ...salesforceStoppedSession, project_id: 'project-1' } })
      .mockResolvedValueOnce({ session: secondSession })
    apiMock.importSalesforce.mockResolvedValue({
      ...credentialPreviewResult(),
      script: salesforceScript,
      scripts: [salesforceScript],
      secret_binding_requirements: [{
        ...credentialRequirement,
        id: 'salesforce-token',
        path: '/config/token',
        suggested_name: 'SALESFORCE_TOKEN',
      }],
      summary: { salesforce_review: { detected: true } },
    })
    apiMock.applySecretBindings.mockImplementationOnce(() => bindingRequest)

    await renderPanel({ onEdit })
    await clickButton('Review Salesforce HTTP Draft')
    await flushPromises()
    await clickButton('Open Salesforce Draft')
    await flushPromises()
    expect(apiMock.applySecretBindings).toHaveBeenCalledOnce()

    await act(async () => {
      root.render(<BrowserRecorderControlPanel sessionId="brec-2" onEdit={onEdit} />)
    })
    await flushPromises()

    await act(async () => {
      resolveBindings({
        script: { ...salesforceScript, config: { token: '{{secret.SALESFORCE_TOKEN}}' } },
        secret_binding_requirements: [],
      })
      await bindingRequest
    })
    await flushPromises()

    expect(onEdit).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Second Recording')
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

    const scheme = container.querySelector('select[aria-label="Navigate URL scheme"]') as HTMLSelectElement
    const input = container.querySelector('input[aria-label="Navigate the recording browser"]') as HTMLInputElement
    expect(scheme).not.toBeNull()
    expect(input).not.toBeNull()

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, 'file:///etc/passwd')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await clickButton('Go')
    await flushPromises()
    expect(apiMock.navigateBrowserRecording).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Enter an http(s) URL')

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
      setter.call(scheme, 'http://')
      scheme.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, 'localhost:3000/checkout')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await clickButton('Go')
    await flushPromises()
    expect(apiMock.navigateBrowserRecording).toHaveBeenCalledWith('brec-1', 'http://localhost:3000/checkout')

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, 'https://shop.example/checkout')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(scheme.value).toBe('https://')
    expect(input.value).toBe('shop.example/checkout')
    await clickButton('Go')
    await flushPromises()
    expect(apiMock.navigateBrowserRecording).toHaveBeenLastCalledWith('brec-1', 'https://shop.example/checkout')
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
    expect(chip!.textContent).toContain('End Transaction')

    await clickButton('End Transaction')
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
    expect(badge?.textContent).toBe('Selector: high (90/100)')
    expect(badge?.className).toContain('brp-quality-high')
    expect(container.textContent).toContain('not a measured replay success rate')
    expect(badge?.getAttribute('title')).toContain('not a replay success probability')
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
    expect(apiMock.startBrowserRecordingTransaction).toHaveBeenCalledWith('brec-1', 'Cart', 1)
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
    expect(apiMock.verifyBrowserRecording).toHaveBeenCalledWith('brec-1', expect.stringMatching(/^[0-9a-f-]{36}$/), expect.objectContaining({ parameterize: false, split_at_transactions: false }), 0)
    expect(container.querySelector('[data-testid="replay-progress"]')).not.toBeNull()
  })

  it('scopes verification to each output and invalidates it when conversion settings change', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      scripts: [
        { name: 'Browser journey', protocol: 'browser', actions: [{ type: 'browser_click', name: 'Continue' }] },
        { name: 'HTTP journey', protocol: 'http', actions: [{ type: 'http', name: 'Request' }] },
      ], warnings: [], stats: {},
    })
    await renderPanel()
    await clickButton('Preview Conversion')
    await clickButton('Verify output 2')
    expect(apiMock.verifyBrowserRecording).toHaveBeenLastCalledWith('brec-1', expect.any(String), expect.objectContaining({ expected_script_count: 2 }), 1)
    await clickButton('Complete Replay')
    expect(container.textContent).toContain('Verification passed for this conversion')
    expect(container.textContent).toContain('Save unverified draft')
    const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Parameterize inputs'))!.querySelector('input')!
    await act(async () => input.click())
    expect(container.textContent).not.toContain('Verification passed for this conversion')
    expect(container.textContent).not.toContain('Replay verification passed')
    expect(container.textContent).toContain('Previous recording verification')
    expect(container.textContent).toContain('Unverified draft')
  })

  it('restores previous replay access without claiming it verified the current conversion', async () => {
    sessionStorage.setItem('browser-verification-history:api:http://controller.test:brec-1', JSON.stringify(['older-replay']))
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    expect(container.textContent).toContain('Replay older-replay')
    expect(container.textContent).toContain('Historical evidence only')
    expect(container.textContent).toContain('Save unverified draft')
  })

  it('does not restore current verification after an old start response arrives', async () => {
    let finish!: (value: unknown) => void
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    await clickButton('Preview Conversion')
    apiMock.verifyBrowserRecording.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await clickButton('Verify output 1')
    const checkbox = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Parameterize inputs'))!.querySelector('input')!
    await act(async () => checkbox.click())
    await act(async () => finish({ replay_session_id: 'old-replay' }))
    expect(container.querySelector('[data-testid="replay-progress"]')).toBeNull()
    expect(container.textContent).toContain('Save unverified draft')
    expect(container.textContent).toContain('Replay old-replay')
  })

  it('does not dispatch duplicate verification starts on rapid clicks', async () => {
    let resolveVerify!: (value: { replay_session_id: string; replay_session: object; warnings: string[] }) => void
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.verifyBrowserRecording.mockReturnValueOnce(new Promise(resolve => {
      resolveVerify = resolve
    }))
    await renderPanel()
    const verify = container.querySelector('[data-testid="verify-recording"]') as HTMLButtonElement
    act(() => {
      verify.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      verify.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.verifyBrowserRecording).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveVerify({ replay_session_id: 'replay-dedupe', replay_session: {}, warnings: [] })
      await Promise.resolve()
    })
  })

  it('keeps reconciling the client replay id when verify loses its start response', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.verifyBrowserRecording.mockRejectedValueOnce(new Error('connection reset'))
    await renderPanel()

    const verify = container.querySelector('[data-testid="verify-recording"]') as HTMLButtonElement
    await act(async () => {
      verify.click()
    })
    await flushPromises()

    const replayCalls = apiMock.verifyBrowserRecording.mock.calls
    const replaySessionId = replayCalls[replayCalls.length - 1]?.[1]
    expect(replaySessionId).toMatch(/^[0-9a-f-]{36}$/)
    expect(container.textContent).toContain(`Replay ${replaySessionId}`)
    expect(container.textContent).toContain('Confirming the accepted replay session')
  })

  it('surfaces unsupported replay-repair patch types instead of no-oping', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    const verify = container.querySelector('[data-testid="verify-recording"]') as HTMLButtonElement
    expect(verify).not.toBeNull()
    await act(async () => { verify.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()

    await clickButton('Apply Unsupported Repair')

    expect(container.textContent).toContain('Replay suggestion type "update_timeout" is not supported')
  })

  it('passes split and dual-http options to convert', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    // Enable split + dual-http checkboxes (click toggles them false→true).
    const checks = Array.from(container.querySelectorAll('.brp-convert input[type="checkbox"]')) as HTMLInputElement[]
    const conversionChecks = checks.filter(c => !c.parentElement?.textContent?.includes('Parameterize inputs'))
    expect(conversionChecks.length).toBe(2)
    for (const c of conversionChecks) {
      await act(async () => { c.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    }
    await clickButton('Save unverified draft')
    await flushPromises()
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      split_at_transactions: true,
      also_create_http_script: true,
      parameterize: false,
      http_version_mode: 'upgrade',
    }))
  })

  it('passes the HTTP script version mode only with the HTTP script', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    const modeSelect = () => Array.from(container.querySelectorAll('.brp-convert label')).find(label => label.textContent?.includes('HTTP version in generated script'))?.querySelector('select') as HTMLSelectElement | undefined
    expect(modeSelect()).toBeUndefined()
    const dualHTTP = (Array.from(container.querySelectorAll('.brp-convert input[type="checkbox"]')) as HTMLInputElement[])
      .find(c => c.parentElement?.textContent?.includes('HTTP'))!
    await act(async () => { dualHTTP.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const select = modeSelect()!
    await act(async () => {
      select.value = 'inherit'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickButton('Save unverified draft')
    await flushPromises()
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({ also_create_http_script: true, http_version_mode: 'inherit' }))
  })

  it('passes the recording profile and its preselected HTTP script level to convert', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel({ recordingProfileId: 'checkout', recordingProfileHTTPScriptLevel: 'html' })
    const dualHTTP = (Array.from(container.querySelectorAll('.brp-convert input[type="checkbox"]')) as HTMLInputElement[])
      .find(c => c.parentElement?.textContent?.includes('HTTP'))!
    await act(async () => { dualHTTP.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect((container.querySelector('select[aria-label="HTTP script level"]') as HTMLSelectElement | null)?.value ?? 'html').toBe('html')
    await clickButton('Save unverified draft')
    await flushPromises()
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      also_create_http_script: true,
      http_script_level: 'html',
      recording_profile_id: 'checkout',
    }))
  })

  it('passes the embedded parameterize inputs toggle to convert', async () => {
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    await renderPanel()
    const parameterize = Array.from(container.querySelectorAll('.brp-convert input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Parameterize inputs')) as HTMLInputElement | undefined
    expect(parameterize).toBeTruthy()
    if (!parameterize) return
    await act(async () => { parameterize.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await clickButton('Save unverified draft')
    await flushPromises()
    expect(apiMock.convertBrowserRecording).toHaveBeenCalledWith('brec-1', expect.objectContaining({
      parameterize: true,
    }))
  })

  it('saves bridge recordings by previewing locally and creating scripts remotely', async () => {
    const onImported = vi.fn()
    const rawCredential = 'bridge-preview-password-canary'
    const browserDraft = {
      name: 'Checkout Flow',
      protocol: 'browser',
      actions: [{
        name: 'Fill password',
        type: 'browser_fill',
        value: rawCredential,
        metadata: { source_marked_secret: true },
      }],
    }
    const httpDraft = { name: 'Checkout Flow (HTTP)', protocol: 'http', actions: [] }
    apiMock.browserRecorderBridge = { origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }
    apiMock.getBrowserRecording.mockResolvedValue({ session: stoppedSession })
    apiMock.previewBrowserRecordingConversion.mockResolvedValue({
      script: browserDraft,
      scripts: [browserDraft, httpDraft],
      warnings: [],
      stats: {},
      parameterization: { suggestions: [], summary: { total: 0 } },
    })
    apiMock.commitGenericImportBatch.mockResolvedValueOnce({
      count: 2,
      scripts: [
        { id: 'remote-browser', name: 'Checkout Flow', protocol: 'browser', script: { name: 'Checkout Flow', protocol: 'browser', actions: [] } },
        { id: 'remote-http', name: 'Checkout Flow (HTTP)', protocol: 'http', script: { name: 'Checkout Flow (HTTP)', protocol: 'http', actions: [] } },
      ],
    })

    await renderPanel({ onImported })
    await clickButton('Save unverified draft')
    await flushPromises()

    expect(apiMock.convertBrowserRecording).not.toHaveBeenCalled()
    expect(apiMock.previewBrowserRecordingConversion).toHaveBeenCalledWith('brec-1', expect.objectContaining({ name: 'Checkout Flow' }))
    expect(apiMock.attestBridgedBrowserRecordingImport).toHaveBeenCalledWith(
      [browserDraft, httpDraft],
      undefined,
    )
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(apiMock.attestBridgedBrowserRecordingImport.mock.invocationCallOrder[0])
      .toBeLessThan(apiMock.commitGenericImportBatch.mock.invocationCallOrder[0])
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      source_profile: 'browser_recorder',
      scripts: [
        expect.objectContaining({
          name: 'Checkout Flow',
          actions: [expect.objectContaining({ value: rawCredential })],
        }),
        expect.objectContaining({ name: 'Checkout Flow (HTTP)' }),
      ],
      operation_id: expect.stringMatching(/^browser-import-/),
    }))
    expect(onImported).toHaveBeenCalledWith({ scripts: [expect.objectContaining({ id: 'remote-browser' }), expect.objectContaining({ id: 'remote-http' })] })
    // Script-library response mocks model the controller's disclosure-safe
    // response; the raw credential only travels in the attested write.
    expect(JSON.stringify(onImported.mock.calls)).not.toContain(rawCredential)
    expect(container.textContent).toContain('Saved 2 scripts to the remote script library')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
