// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RecordingStudio from './RecordingStudio'
import {
  buildMacOSRestoreProxyCommand,
  buildMacOSStartProxyCommand,
  buildWindowsRestoreProxyCommand,
  buildWindowsStartProxyCommand,
  recorderProxyHost,
  recorderProxyHostIsLocal,
  recorderProxyPort,
} from './recordingProxyCommands'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { apiFetchMock, downloadBlobMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  downloadBlobMock: vi.fn(),
}))

const apiMock = {
  listRecordings: vi.fn(),
  getRecorderStatus: vi.fn(),
  getRecording: vi.fn(),
  previewRecordingConversion: vi.fn(),
  convertRecording: vi.fn(),
  importSalesforce: vi.fn(),
  createScript: vi.fn(),
  startRecording: vi.fn(),
  stopRecording: vi.fn(),
  pauseRecording: vi.fn(),
  resumeRecording: vi.fn(),
  deleteRecording: vi.fn(),
  updateRecordedRequest: vi.fn(),
  startBrowserRecording: vi.fn(),
  getBrowserRecording: vi.fn(),
  listBrowserRecordings: vi.fn(),
  deleteBrowserRecording: vi.fn(),
  stopBrowserRecording: vi.fn(),
  openBrowserRecordingControlPanel: vi.fn(),
  previewBrowserRecordingConversion: vi.fn(),
  convertBrowserRecording: vi.fn(),
  listBrowserRecorderDevices: vi.fn(),
  installBrowserRecorder: vi.fn(),
  suggestBrowserRecordingTransactions: vi.fn(),
  pairBrowserRecorderBridge: vi.fn(),
  verifyBrowserRecorderBridgeAgent: vi.fn(),
  clearBrowserRecorderBridge: vi.fn(),
  getBrowserRecorderBridgeStatus: vi.fn(),
  exportBrowserRecording: vi.fn(),
  browserRecorderBridge: null as any,
}

const clipboardWriteMock = vi.fn()

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/apiClient', () => ({
  API_ORIGIN: 'http://127.0.0.1:3000',
  apiFetch: apiFetchMock,
  trimTrailingSlash: (value: string) => value.replace(/\/+$/, ''),
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: downloadBlobMock,
  downloadFilenameFromDisposition: (_header: string | null, fallback: string) => fallback,
}))

describe('RecordingStudio', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiFetchMock.mockReset()
    apiFetchMock.mockResolvedValue({
      ok: true,
      headers: new Headers({ 'content-type': 'application/octet-stream' }),
      blob: vi.fn().mockResolvedValue(new Blob(['certificate'])),
    })
    downloadBlobMock.mockReset()
    clipboardWriteMock.mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: clipboardWriteMock },
    })

    apiMock.listRecordings.mockResolvedValue({
      sessions: [
        {
          id: 'rec-1',
          name: 'Checkout Capture',
          status: 'stopped',
          started_at: '2026-05-12T20:00:00Z',
          base_url: 'https://shop.example.com',
          requests: [
            {
              seq: 1,
              timestamp: '2026-05-12T20:00:01Z',
              method: 'GET',
              url: 'https://shop.example.com/',
              path: '/',
              host: 'shop.example.com',
              status_code: 200,
              response_time_ms: 123,
              content_type: 'text/html',
              body: '',
              think_time_ms: 0,
              is_static: false,
            },
          ],
          filters: { exclude_static: true },
        },
      ],
    })
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: true },
        active: null,
        install: { status: 'idle' },
      },
    })
    apiMock.getRecording.mockResolvedValue(null)
    apiMock.previewRecordingConversion.mockResolvedValue({
      script: {
        name: 'Checkout Preview',
        protocol: 'http',
        actions: [{ name: '01 - GET /' }],
      },
      correlations: [
        {
          name: 'session_id',
          source: 'response',
          expression: '$.sessionId',
          extract_type: 'jsonpath',
          found_in_seq: 1,
          used_in_seqs: [2],
          sample_value: 'abc123',
          rule_id: 'json.session_id',
          confidence: 0.85,
          reasons: ['reused in a later request'],
        },
      ],
      warnings: ['review dynamic headers'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      stats: {},
      summary: {},
    })
    apiMock.convertRecording.mockResolvedValue({
      script: {
        id: 'script-1',
        name: 'Checkout Converted',
        protocol: 'http',
        actions: [{ name: '01 - GET /' }],
      },
      correlations: [
        {
          name: 'session_id',
          source: 'response',
          expression: '$.sessionId',
          extract_type: 'jsonpath',
          found_in_seq: 1,
          used_in_seqs: [2],
          sample_value: 'abc123',
        },
      ],
      warnings: ['review dynamic headers'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      stats: {},
      summary: {},
    })
    apiMock.importSalesforce.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Salesforce Capture HTTP',
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
    apiMock.createScript.mockImplementation(async (script: { name?: string; protocol?: string; version?: string }) => ({
      id: 'script-1',
      name: script.name || 'Saved Script',
      protocol: script.protocol || 'http',
      version: script.version || '1.0.0',
      created_at: '2026-05-17T00:00:00Z',
    }))
    apiMock.startBrowserRecording.mockResolvedValue({
      session: {
        id: 'brec-1',
        name: 'Browser Recording',
        status: 'recording',
        start_url: 'https://app.example.com/login',
        capture_har: true,
        started_at: '2026-05-12T20:00:00Z',
        step_count: 0,
        navigation_count: 0,
        network_request_count: 0,
        websocket_count: 0,
        warning_count: 0,
        har_available: false,
      },
    })
    apiMock.listBrowserRecordings.mockResolvedValue({ sessions: [], count: 0 })
    apiMock.deleteBrowserRecording.mockResolvedValue({ deleted: 'brec-1', session: { id: 'brec-1', name: 'Browser Recording', status: 'stopped' } })
    apiMock.openBrowserRecordingControlPanel.mockResolvedValue({
      session: {
        id: 'brec-1',
        name: 'Browser Recording',
        status: 'recording',
        start_url: 'https://app.example.com/login',
        capture_har: true,
        started_at: '2026-05-12T20:00:00Z',
        step_count: 0,
        navigation_count: 0,
        network_request_count: 0,
        websocket_count: 0,
        warning_count: 0,
        har_available: false,
      },
    })
    apiMock.getBrowserRecording.mockResolvedValue({
      session: {
        id: 'brec-1',
        name: 'Browser Recording',
        status: 'recording',
        start_url: 'https://app.example.com/login',
        capture_har: true,
        started_at: '2026-05-12T20:00:00Z',
        step_count: 1,
        navigation_count: 1,
        network_request_count: 3,
        websocket_count: 0,
        warning_count: 0,
        har_available: false,
        steps: [
          { seq: 1, kind: 'click', timestamp: '2026-05-12T20:00:02Z', url: 'https://app.example.com/login' },
        ],
      },
    })
    apiMock.listBrowserRecorderDevices.mockResolvedValue({ devices: [{ name: 'iphone_15_pro', platform: 'ios' }] })
    apiMock.installBrowserRecorder.mockResolvedValue({ install: { status: 'installing' } })
    apiMock.suggestBrowserRecordingTransactions.mockResolvedValue({ suggestions: [] })
    apiMock.pairBrowserRecorderBridge.mockResolvedValue({
      origin: 'http://127.0.0.1:49152',
      token: 'tok',
      session_id: 'ws-1',
      status: { availability: { available: true }, api_origin: 'http://127.0.0.1:49152', paired: true },
    })
    apiMock.verifyBrowserRecorderBridgeAgent.mockResolvedValue({ status: 'healthy', version: 'test' })
    apiMock.clearBrowserRecorderBridge.mockReset()
    apiMock.getBrowserRecorderBridgeStatus.mockResolvedValue({
      bridge: { availability: { available: true }, api_origin: 'http://127.0.0.1:49152', paired: true, active: null },
    })
    apiMock.exportBrowserRecording.mockResolvedValue({
      ok: true,
      blob: vi.fn().mockResolvedValue(new Blob(['{"log":{}}'])),
    })
    apiMock.browserRecorderBridge = null
    vi.spyOn(window, 'open').mockImplementation(() => null)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders an optional page header Back action', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(<RecordingStudio onClose={onClose} />)
    })

    const backButtons = Array.from(container.querySelectorAll('.page-header button'))
      .filter(button => button.textContent?.trim() === 'Back')
    expect(backButtons).toHaveLength(1)

    await act(async () => {
      backButtons[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('builds macOS proxy shortcuts that save and restore the selected service', () => {
    const startCommand = buildMacOSStartProxyCommand('9090', `USB Bob's LAN`, 'controller.example.com')
    const restoreCommand = buildMacOSRestoreProxyCommand(`USB Bob's LAN`)

    expect(recorderProxyPort('127.0.0.1:9090')).toBe('9090')
    expect(recorderProxyPort('bad-port')).toBe('8081')
    expect(recorderProxyHost(':8081', 'https://controller.example.com')).toBe('controller.example.com')
    expect(recorderProxyHost('127.0.0.1:8081', 'https://controller.example.com')).toBe('localhost')
    expect(recorderProxyHostIsLocal('controller.example.com')).toBe(false)
    expect(recorderProxyHostIsLocal('localhost')).toBe(true)
    expect(startCommand).toContain(`SERVICE='USB Bob'\\''s LAN'`)
    expect(startCommand).toContain(`PROXY_HOST='controller.example.com'`)
    expect(startCommand).toContain(`PROXY_PORT='9090'`)
    expect(startCommand).toContain('networksetup -getwebproxy "$SERVICE"')
    expect(startCommand).toContain('networksetup -setsecurewebproxystate "$SERVICE" on')
    expect(restoreCommand).toContain(`SERVICE='USB Bob'\\''s LAN'`)
    expect(restoreCommand).toContain('networksetup "$STATE_CMD" "$SERVICE" off')
    expect(restoreCommand).toContain('rm -f "$STATE_DIR/$STATE_KEY.http" "$STATE_DIR/$STATE_KEY.https"')
  })

  it('builds Windows 11 proxy shortcuts that save and restore per-user proxy settings', () => {
    const startCommand = buildWindowsStartProxyCommand('9090', 'controller.example.com')
    const restoreCommand = buildWindowsRestoreProxyCommand()
    const fallbackStartCommand = buildWindowsStartProxyCommand('not-a-port')

    expect(startCommand).toContain(`$proxyHost = 'controller.example.com'`)
    expect(startCommand).toContain(`$proxyPort = '9090'`)
    expect(startCommand).toContain(`$proxyServer = 'http={0}:{1};https={0}:{1}' -f $proxyHost, $proxyPort`)
    expect(startCommand).toContain(`$settingsPath = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings'`)
    expect(startCommand).toContain(`Get-ItemProperty -Path $settingsPath -Name $Name`)
    expect(startCommand).toContain(`New-ItemProperty -Path $settingsPath -Name ProxyEnable -PropertyType DWord -Value 1 -Force`)
    expect(startCommand).toContain(`Remove-ItemProperty -Path $settingsPath -Name AutoConfigURL`)
    expect(startCommand).toContain('Notify-ProxySettingsChanged')
    expect(fallbackStartCommand).toContain(`$proxyPort = '8081'`)
    expect(restoreCommand).toContain(`Get-Content -Path $statePath -Raw | ConvertFrom-Json`)
    expect(restoreCommand).toContain(`Restore-DwordRegistryValue -Name ProxyEnable -DefaultValue 0`)
    expect(restoreCommand).toContain(`Restore-StringRegistryValue -Name ProxyServer`)
    expect(restoreCommand).toContain(`Restore-StringRegistryValue -Name AutoConfigURL`)
    expect(restoreCommand).toContain(`Remove-Item -Path $statePath -ErrorAction SilentlyContinue`)
  })

  it('renders corrected CA installation and trust instructions', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    expect(container.textContent).toContain('Install and trust the APEX Load CA certificate for SSL/TLS')
    expect(container.textContent).toContain('ERR_CERT_AUTHORITY_INVALID')
    expect(container.textContent).toContain('Download CA Certificate (.crt)')
    expect(container.textContent).not.toContain('Download CA Certificate (.pem)')
    expect(container.textContent).toContain('import into the System keychain')
    expect(container.textContent).toContain('Secure Sockets Layer (SSL)')
    expect(container.textContent).toContain('Always Trust')
    expect(container.textContent).toContain('Trusted Root Certification Authorities')
    expect(container.textContent).not.toContain('Trust this CA to identify websites')
    expect(container.textContent).not.toContain('browser or distro trust store')
    expect(container.textContent).toContain('Remove older')

    const formatToggle = container.querySelector('.rec-certificate-format-toggle')
    expect(formatToggle).toBeDefined()
    const crtButton = Array.from(formatToggle?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'CRT')
    const pemButton = Array.from(formatToggle?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'PEM')
    expect(crtButton?.getAttribute('aria-pressed')).toBe('true')
    expect(pemButton?.getAttribute('aria-pressed')).toBe('false')
  })

  it('switches Step 1 certificate guidance to PEM format', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const formatToggle = container.querySelector('.rec-certificate-format-toggle')
    const crtButton = Array.from(formatToggle?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'CRT')
    const pemButton = Array.from(formatToggle?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'PEM')
    expect(crtButton).toBeDefined()
    expect(pemButton).toBeDefined()
    if (!pemButton) return

    await act(async () => {
      pemButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(crtButton?.getAttribute('aria-pressed')).toBe('false')
    expect(pemButton.getAttribute('aria-pressed')).toBe('true')
    expect(container.textContent).toContain('Download CA Certificate (.pem)')
    expect(container.textContent).not.toContain('Download CA Certificate (.crt)')
    expect(container.textContent).toContain('Trust this CA to identify websites')
    expect(container.textContent).toContain('browser or distro trust store')
    expect(container.textContent).not.toContain('import into the System keychain')
    expect(container.textContent).not.toContain('Trusted Root Certification Authorities')
    expect(container.textContent).toContain('Remove older')
  })

  it('downloads the selected Step 1 certificate format', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const crtDownload = Array.from(container.querySelectorAll('button')).find(
      button => button.textContent?.trim() === 'Download CA Certificate (.crt)',
    )
    expect(crtDownload).toBeDefined()
    if (!crtDownload) return

    await act(async () => {
      crtDownload.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiFetchMock).toHaveBeenCalledTimes(1)
    expect(apiFetchMock).toHaveBeenLastCalledWith('/recorder/ca.pem')
    expect(downloadBlobMock).toHaveBeenCalledTimes(1)
    expect(downloadBlobMock).toHaveBeenLastCalledWith(expect.any(Blob), 'apexload-ca.crt')

    const formatToggle = container.querySelector('.rec-certificate-format-toggle')
    const pemButton = Array.from(formatToggle?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'PEM')
    expect(pemButton).toBeDefined()
    if (!pemButton) return

    await act(async () => {
      pemButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const pemDownload = Array.from(container.querySelectorAll('button')).find(
      button => button.textContent?.trim() === 'Download CA Certificate (.pem)',
    )
    expect(pemDownload).toBeDefined()
    if (!pemDownload) return

    await act(async () => {
      pemDownload.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiFetchMock).toHaveBeenCalledTimes(2)
    expect(apiFetchMock).toHaveBeenLastCalledWith('/recorder/ca.pem?format=pem')
    expect(downloadBlobMock).toHaveBeenCalledTimes(2)
    expect(downloadBlobMock).toHaveBeenLastCalledWith(expect.any(Blob), 'apexload-ca.pem')
  })

  it('reviews a recording conversion and saves it through the shared script flow', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />)
    })

    await flushPromises()
    await openProxyAdvanced(container)

    expect(container.textContent).toContain('Recorded Sessions')
    expect(container.textContent).toContain('Checkout Capture')

    const reviewButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Review Script')
    expect(reviewButton).toBeDefined()
    if (!reviewButton) {
      return
    }

    await act(async () => {
      reviewButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.previewRecordingConversion).toHaveBeenCalledWith('rec-1', 'Checkout Capture', false)
    expect(container.textContent).toContain('Preview generated with 1 actions and 1 correlations')
    expect(container.textContent).toContain('Checkout Preview')
    expect(container.textContent).toContain('review dynamic headers')
    expect(container.textContent).toContain('{{var.session_id}}')
    expect(container.textContent).toContain('85% confidence')
    expect(container.textContent).toContain('reused in a later request')
    expect(container.textContent).toContain('Save To Script Library')
    expect(container.textContent).toContain('Open In Visual Editor')

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Open In Visual Editor')
    expect(editButton).toBeDefined()
    if (!editButton) {
      return
    }

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Preview',
      protocol: 'http',
      metadata: expect.objectContaining({
        import: expect.objectContaining({
          source: 'recorder',
          preserved: expect.objectContaining({
            correlation_suggestions: [expect.objectContaining({
              name: 'session_id',
              found_at: 'Request 1',
              used_at: ['Request 2'],
            })],
          }),
        }),
      }),
    }))

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Save To Script Library')
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.convertRecording).not.toHaveBeenCalled()
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Preview',
      protocol: 'http',
      metadata: expect.objectContaining({
        import: expect.objectContaining({
          source: 'recorder',
        }),
      }),
    }), undefined)
    expect(container.textContent).toContain('Script "Checkout Preview" created with 1 correlations detected')
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        name: 'Checkout Preview',
        protocol: 'http',
      })],
    }))
  })

  it('treats preview scripts with empty ids as unsaved drafts', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: {
        id: '',
        name: 'Unsaved Preview',
        protocol: 'http',
        version: '1.0.0',
        actions: [{ name: '01 - GET /', type: 'request', method: 'GET', target: '/' }],
      },
      correlations: [],
      warnings: [],
    })

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const reviewButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Review Script')
    expect(reviewButton).toBeDefined()
    await act(async () => {
      reviewButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Unsaved Preview')
    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Open In Visual Editor')
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ name: 'Unsaved Preview', protocol: 'http' }))

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Save To Script Library')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({ name: 'Unsaved Preview', protocol: 'http' }), undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ name: 'Unsaved Preview', protocol: 'http' })],
    }))
  })

  it('confirms before deleting a stopped proxy recording', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const deleteButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Delete')
    expect(deleteButton).toBeDefined()
    if (!deleteButton) return

    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(confirmSpy).toHaveBeenCalledWith('Delete this stopped recording session? This cannot be undone.')
    expect(apiMock.deleteRecording).not.toHaveBeenCalled()

    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.deleteRecording).toHaveBeenCalledWith('rec-1')
  })

  it('surfaces stopped browser recordings after reload and can delete them', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    apiMock.listBrowserRecordings.mockResolvedValue({
      sessions: [{
        id: 'brec-stopped',
        name: 'Checkout Browser',
        status: 'stopped',
        start_url: 'https://shop.example/login',
        capture_har: true,
        started_at: '2026-05-12T20:00:00Z',
        stopped_at: '2026-05-12T20:05:00Z',
        step_count: 4,
        navigation_count: 2,
        network_request_count: 9,
        websocket_count: 0,
        warning_count: 0,
        har_available: true,
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const recoveryList = container.querySelector('[data-testid="browser-recording-recovery-list"]')
    expect(recoveryList?.textContent).toContain('Recent Browser Recordings')
    expect(recoveryList?.textContent).toContain('Checkout Browser')
    expect(recoveryList?.textContent).toContain('4 steps')
    expect(recoveryList?.textContent).toContain('9 requests')

    const openButton = Array.from(recoveryList?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'Open')
    await act(async () => {
      openButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith('brec-stopped')

    const deleteButton = Array.from(recoveryList?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'Delete')
    await act(async () => {
      deleteButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(confirmSpy).toHaveBeenCalledWith('Delete this stopped browser recording? This cannot be undone.')
    expect(apiMock.deleteBrowserRecording).toHaveBeenCalledWith('brec-stopped')
  })

  it('normalizes stored proxy conversion results before edit or save handoff', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()
    const nestedScript = {
      name: 'Checkout Converted',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://shop.example.com' },
      actions: [{ name: '01 - GET /', type: 'request', method: 'GET', target: '/' }],
    }
    apiMock.convertRecording.mockResolvedValueOnce({
      script: {
        id: 'stored-recorder-script',
        name: 'Checkout Converted',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
        script: nestedScript,
      },
      correlations: [],
      warnings: [],
    })

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Convert to Script')
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('stored-recorder-script')
    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Open In Visual Editor')
    expect(editButton).toBeDefined()
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ actions: nestedScript.actions }))

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Save To Script Library')
    expect(saveButton).toBeDefined()
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith({
      scripts: [expect.objectContaining({ id: 'stored-recorder-script', script: expect.objectContaining({ actions: nestedScript.actions }) })],
    })
  })

  it('reviews Salesforce-looking proxy recordings through the native HTTP importer', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()
    apiMock.listRecordings.mockResolvedValueOnce({
      sessions: [{
        id: 'rec-sf',
        name: 'Salesforce Capture',
        status: 'stopped',
        started_at: '2026-05-12T20:00:00Z',
        base_url: 'https://acme.my.salesforce.com',
        requests: [{
          seq: 1,
          timestamp: '2026-05-12T20:00:01Z',
          method: 'POST',
          url: 'https://acme.my.salesforce.com/aura',
          path: '/aura',
          host: 'acme.my.salesforce.com',
          status_code: 200,
          response_time_ms: 88,
          content_type: 'application/json',
          body: '',
          think_time_ms: 0,
          is_static: false,
        }],
        filters: { exclude_static: true },
      }],
    })

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    expect(container.textContent).toContain('Review Salesforce HTTP')

    const reviewButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Review Salesforce HTTP')
    expect(reviewButton).toBeDefined()
    if (!reviewButton) return

    await act(async () => {
      reviewButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiFetchMock).toHaveBeenCalledWith('/recorder/rec-sf/export?format=har')
    expect(apiMock.importSalesforce).toHaveBeenCalledWith(expect.any(File), expect.objectContaining({
      name: 'Salesforce Capture Salesforce HTTP',
      base_url: 'https://acme.my.salesforce.com',
    }))
    expect(container.textContent).toContain('Salesforce HTTP Draft')
    expect(container.textContent).toContain('Salesforce Capture HTTP')
    expect(container.textContent).toContain('100%')
    expect(container.textContent).toContain('Review Salesforce correlation extractors before approval.')

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Open In Visual Editor')
    expect(editButton).toBeDefined()
    if (!editButton) return
    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ name: 'Salesforce Capture HTTP', protocol: 'http' }))

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Save To Script Library')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({ name: 'Salesforce Capture HTTP' }), undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ name: 'Salesforce Capture HTTP' })],
    }))
  })

  it('passes the parameterize option through when toggled', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const toggle = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(
      input => (input as HTMLInputElement).parentElement?.textContent?.includes('Parameterize input values'),
    ) as HTMLInputElement | undefined
    expect(toggle).toBeDefined()
    if (!toggle) return

    await act(async () => {
      toggle.click()
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Convert to Script')
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.convertRecording).toHaveBeenCalledWith('rec-1', 'Checkout Capture', true)
  })

  it('threads the capture cookies option into the start request', async () => {
    apiMock.startRecording.mockResolvedValue({
      session: {
        id: 'rec-cookie',
        name: 'Recording Session',
        status: 'recording',
        started_at: '2026-05-12T20:00:00Z',
        requests: [],
        filters: { exclude_static: true, capture_cookies: true },
      },
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const captureToggle = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(
      input => (input as HTMLInputElement).parentElement?.textContent?.includes('Capture cookies'),
    ) as HTMLInputElement | undefined
    expect(captureToggle).toBeDefined()
    if (!captureToggle) return

    await act(async () => {
      captureToggle.click()
    })

    const startButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Start Recording')
    expect(startButton).toBeDefined()
    if (!startButton) return

    await act(async () => {
      startButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.startRecording).toHaveBeenCalledWith(expect.objectContaining({
      capture_cookies: true,
      exclude_static: true,
    }))
  })

  it('copies macOS start and restore proxy commands from the setup shortcut', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    expect(container.textContent).not.toContain('Copy Windows Start Command')
    const serviceInput = container.querySelector('input[aria-label="macOS network service"]') as HTMLInputElement | null
    expect(serviceInput).toBeDefined()
    if (!serviceInput) return

    const startShortcut = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Copy Start Proxy Command')
    expect(startShortcut).toBeDefined()
    if (!startShortcut) return

    await act(async () => {
      startShortcut.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`SERVICE='Wi-Fi'`))
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`PROXY_HOST='localhost'`))
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`PROXY_PORT='8081'`))
    expect(container.textContent).toContain('macOS start proxy command copied')

    const restoreShortcut = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Copy Restore Proxy Command')
    expect(restoreShortcut).toBeDefined()
    if (!restoreShortcut) return

    await act(async () => {
      restoreShortcut.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`restore_proxy "$STATE_DIR/$STATE_KEY.http"`))
    expect(container.textContent).toContain('macOS restore proxy command copied')
  })

  it('copies Windows 11 start and restore proxy commands from the setup shortcut', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()
    await openProxyAdvanced(container)

    const windowsToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Windows 11')
    expect(windowsToggle).toBeDefined()
    if (!windowsToggle) return

    await act(async () => {
      windowsToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.querySelector('input[aria-label="macOS network service"]')).toBeNull()
    const startShortcut = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Copy Windows Start Command')
    expect(startShortcut).toBeDefined()
    if (!startShortcut) return

    await act(async () => {
      startShortcut.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`$proxyPort = '8081'`))
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`$proxyHost = 'localhost'`))
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining('windows-11-wininet.json'))
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining('Notify-ProxySettingsChanged'))
    expect(container.textContent).toContain('Windows start proxy command copied')

    const restoreShortcut = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Copy Windows Restore Command')
    expect(restoreShortcut).toBeDefined()
    if (!restoreShortcut) return

    await act(async () => {
      restoreShortcut.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`Restore-DwordRegistryValue -Name ProxyEnable -DefaultValue 0`))
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(expect.stringContaining(`Restore-StringRegistryValue -Name ProxyServer`))
    expect(container.textContent).toContain('Windows restore proxy command copied')
  })

  it('renders browser and proxy recorder options side by side', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Record Browser Session')
    expect(container.querySelector('[data-testid="rec-proxy-recorder-option"]')).not.toBeNull()
    expect(container.textContent).toContain('Advanced Proxy Recorder')
    expect(container.textContent).toContain('Proxy Setup')
    expect(container.textContent).not.toContain('Download CA Certificate')
  })

  it('rejects non-http(s) start URLs before calling the API', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const urlInput = container.querySelector('input[aria-label="Start URL address"]') as HTMLInputElement
    expect(urlInput).not.toBeNull()
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(urlInput, 'file:///etc/passwd')
      urlInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const recordButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Record Browser'))
    expect(recordButton).toBeDefined()
    await act(async () => {
      recordButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.startBrowserRecording).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Enter an http(s) URL to record')
  })

  it('normalizes pasted full browser start URLs into scheme and address controls', async () => {
    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const scheme = container.querySelector('select[aria-label="Start URL scheme"]') as HTMLSelectElement
    const urlInput = container.querySelector('input[aria-label="Start URL address"]') as HTMLInputElement
    expect(scheme).not.toBeNull()
    expect(urlInput).not.toBeNull()

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(urlInput, 'http://app.example.com:8080/login?source=qa')
      urlInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(scheme.value).toBe('http://')
    expect(urlInput.value).toBe('app.example.com:8080/login?source=qa')
  })

  it('starts a browser recording and docks the control panel', async () => {
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: 'proj-1', name: 'Web' } as any} />)
    })
    await flushPromises()

    const urlInput = container.querySelector('input[aria-label="Start URL address"]') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(urlInput, 'app.example.com/login')
      urlInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const recordButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Record Browser'))
    await act(async () => {
      recordButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.startBrowserRecording).toHaveBeenCalledWith(expect.objectContaining({
      start_url: 'https://app.example.com/login',
      project_id: 'proj-1',
      capture_har: true,
    }))
    // The companion panel opens in the recording browser context; the docked
    // panel stays visible as a fallback/control surface.
    expect(apiMock.openBrowserRecordingControlPanel).toHaveBeenCalledWith(
      'brec-1',
      expect.stringContaining('#/recorder-panel?session=brec-1'),
    )
    expect(window.open).not.toHaveBeenCalled()
    expect(container.querySelector('[data-testid="browser-recorder-panel"]')).not.toBeNull()
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith('brec-1')
  })

  it('falls back to a popup when the recording browser cannot open the panel tab', async () => {
    apiMock.openBrowserRecordingControlPanel.mockRejectedValueOnce(new Error('control panel route unavailable'))
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: 'proj-1', name: 'Web' } as any} />)
    })
    await flushPromises()

    const urlInput = container.querySelector('input[aria-label="Start URL address"]') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(urlInput, 'https://app.example.com/login')
      urlInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const recordButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Record Browser'))
    await act(async () => {
      recordButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.openBrowserRecordingControlPanel).toHaveBeenCalledWith(
      'brec-1',
      expect.stringContaining('#/recorder-panel?session=brec-1'),
    )
    expect(window.open).toHaveBeenCalledWith(
      expect.stringContaining('#/recorder-panel?session=brec-1'),
      'apexload-browser-recorder',
      expect.any(String),
    )
  })

  it('falls back to the Advanced proxy recorder when browser recording is unavailable', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="browser-recorder-unavailable"]')).not.toBeNull()
    expect(container.textContent).toContain('disabled in production mode')
    expect(container.textContent).toContain('Advanced Proxy Recorder')
    expect(container.textContent).toContain('Proxy Setup')
    expect(container.textContent).not.toContain('Download CA Certificate')
  })

  it('pairs a local recording bridge when the remote controller cannot launch a browser', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="browser-recorder-bridge-panel"]')).not.toBeNull()
    const downloadLink = Array.from(container.querySelectorAll('a')).find(link => link.textContent?.includes('Download the APEX Load Recording Agent'))
    expect(downloadLink).toBeDefined()
    expect(downloadLink?.getAttribute('href')).toContain('/api/v1/downloads/recording-agent/latest')
    expect(container.querySelector('select[aria-label="Recording Agent platform"]')).not.toBeNull()
    expect(container.textContent).toContain('SHA-256')
    expect(container.textContent).not.toContain('Install it from your enterprise software catalog')
    expect(container.textContent).toContain('1Download the Recording Agent')
    expect(container.textContent).toContain('2Run the local command')
    expect(container.textContent).toContain('3Verify the agent')
    expect(container.textContent).toContain('4Pair the local bridge')
    expect(container.textContent).toContain('Paste this command into Terminal or PowerShell')
    expect(container.textContent).toContain('Token entered at prompt')
    expect(container.textContent).toContain('paste this pairing token')
    expect(container.textContent).toContain('Verify Agent')
    const command = container.querySelector('[data-testid="browser-recorder-bridge-command"]')?.textContent || ''
    const generatedToken = container.querySelector('[data-testid="browser-recorder-bridge-token"]')?.textContent || ''
    expect(command).toContain('apexload-record-agent --addr 127.0.0.1:27777 --token-stdin')
    expect(command).not.toContain('--token ')
    expect(command).toContain('--allow-origin')
    expect(generatedToken).toMatch(/^[0-9a-f]{48}$/)

    const inputs = Array.from(container.querySelectorAll('.rec-bridge-panel input')) as HTMLInputElement[]
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(inputs[0], 'http://127.0.0.1:49152')
      inputs[0].dispatchEvent(new Event('input', { bubbles: true }))
    })
    const pairButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Pair Local Bridge'))
    expect(pairButton).toBeDefined()
    await act(async () => {
      pairButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.pairBrowserRecorderBridge).toHaveBeenCalledWith('http://127.0.0.1:49152', generatedToken)
    expect(container.textContent).toContain('Local browser recording bridge paired')
    expect(container.textContent).toContain('Record Browser')
  })

  it('shows the selected Recording Agent platform and staged SHA-256', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })
    apiFetchMock
      .mockResolvedValueOnce({
        ok: false,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: vi.fn().mockResolvedValue({ error: 'checksum not staged' }),
      })
      .mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'content-type': 'application/json' }),
      json: vi.fn().mockResolvedValue({
        algorithm: 'sha-256',
        filename: 'apexload-recording-agent-0.1.0-linux-amd64.tar.gz',
        sha256: 'd8df2adeb0f3718d0beeebb225572273e1677b6208cef613c2378f3b3f41f3b2',
      }),
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const platformSelect = container.querySelector('select[aria-label="Recording Agent platform"]') as HTMLSelectElement | null
    expect(platformSelect).not.toBeNull()
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
      setter.call(platformSelect, 'linux-amd64')
      platformSelect!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(apiFetchMock).toHaveBeenCalledWith('/downloads/recording-agent/latest/checksum?platform=linux-amd64')
    expect(container.textContent).toContain('Download the APEX Load Recording Agent for Linux (x64)')
    expect(container.textContent).toContain('d8df2adeb0f3718d0beeebb225572273e1677b6208cef613c2378f3b3f41f3b2')
    expect(container.textContent).toContain('apexload-recording-agent-0.1.0-linux-amd64.tar.gz')
  })

  it('verifies that the local recording agent is running before pairing', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const verifyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Verify Agent'))
    expect(verifyButton).toBeDefined()
    await act(async () => {
      verifyButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.verifyBrowserRecorderBridgeAgent).toHaveBeenCalledWith('http://127.0.0.1:27777')
    expect(container.textContent).toContain('APEX Load Recording Agent verified')
    expect(container.textContent).toContain('APEX Load Recording Agent is running at http://127.0.0.1:27777')
    expect(container.textContent).toContain('Click Pair Local Bridge to connect')
  })

  it('does not navigate to the dashboard shell when the agent download endpoint returns HTML', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })
    apiFetchMock
      .mockResolvedValueOnce({
        ok: false,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: vi.fn().mockResolvedValue({ error: 'checksum not staged' }),
      })
      .mockResolvedValueOnce({
        ok: false,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: vi.fn().mockResolvedValue({ error: 'checksum not staged' }),
      })
      .mockResolvedValueOnce({
      ok: true,
      headers: new Headers({ 'content-type': 'text/html; charset=utf-8' }),
      blob: vi.fn().mockResolvedValue(new Blob(['<html>dashboard</html>'])),
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const downloadLink = Array.from(container.querySelectorAll('a')).find(link => link.textContent?.includes('Download the APEX Load Recording Agent'))
    expect(downloadLink).toBeDefined()
    const platformSelect = container.querySelector('select[aria-label="Recording Agent platform"]') as HTMLSelectElement | null
    expect(platformSelect).not.toBeNull()
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
      setter.call(platformSelect, 'linux-amd64')
      platformSelect!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()
    await act(async () => {
      downloadLink!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    })
    await flushPromises()

    expect(apiFetchMock).toHaveBeenCalledWith('/downloads/recording-agent/latest?platform=linux-amd64')
    expect(downloadBlobMock).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Recording Agent download endpoint returned the dashboard instead of an agent archive')
  })

  it('shows an actionable message when the local recording agent is not reachable', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })
    apiMock.verifyBrowserRecorderBridgeAgent.mockRejectedValueOnce(new TypeError('Failed to fetch'))

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const verifyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Verify Agent'))
    expect(verifyButton).toBeDefined()
    await act(async () => {
      verifyButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('APEX Load Recording Agent is not reachable at http://127.0.0.1:27777')
    expect(container.textContent).toContain('Download and run the agent command')
  })

  it('copies the bridge command without token argv and regenerates the pairing token', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    const commandBefore = container.querySelector('[data-testid="browser-recorder-bridge-command"]')?.textContent || ''
    const tokenBefore = container.querySelector('[data-testid="browser-recorder-bridge-token"]')?.textContent || ''
    expect(commandBefore).toContain('--token-stdin')
    expect(commandBefore).not.toContain(tokenBefore)
    expect(commandBefore).toContain("--allow-origin 'http://localhost:3000'")
    expect(commandBefore).toContain("--allow-origin 'http://127.0.0.1:3000'")
    const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy Terminal Command'))
    expect(copyButton).toBeDefined()

    await act(async () => {
      copyButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(clipboardWriteMock).toHaveBeenCalledWith(commandBefore)

    const copyTokenButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy Pairing Token'))
    expect(copyTokenButton).toBeDefined()
    await act(async () => {
      copyTokenButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(clipboardWriteMock).toHaveBeenCalledWith(tokenBefore)

    const regenerateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Regenerate Token'))
    expect(regenerateButton).toBeDefined()
    await act(async () => {
      regenerateButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const commandAfter = container.querySelector('[data-testid="browser-recorder-bridge-command"]')?.textContent || ''
    const tokenAfter = container.querySelector('[data-testid="browser-recorder-bridge-token"]')?.textContent || ''
    expect(commandAfter).toBe(commandBefore)
    expect(tokenAfter).toMatch(/^[0-9a-f]{48}$/)
    expect(tokenAfter).not.toBe(tokenBefore)
  })

  it('falls back to selection copy when browser clipboard permission is denied', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })
    clipboardWriteMock.mockRejectedValueOnce(new Error('Write permission denied'))
    const originalExecCommand = document.execCommand
    const execCommandMock = vi.fn().mockReturnValue(true)
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: execCommandMock,
    })

    try {
      await act(async () => {
        root.render(<RecordingStudio />)
      })
      await flushPromises()

      const command = container.querySelector('[data-testid="browser-recorder-bridge-command"]')?.textContent || ''
      const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy Terminal Command'))
      expect(copyButton).toBeDefined()

      await act(async () => {
        copyButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
      await flushPromises()

      expect(clipboardWriteMock).toHaveBeenCalledWith(command)
      expect(execCommandMock).toHaveBeenCalledWith('copy')
      expect(container.textContent).toContain('Terminal command copied')
    } finally {
      Object.defineProperty(document, 'execCommand', {
        configurable: true,
        value: originalExecCommand,
      })
    }
  })

  it('shows a manual-copy message when all clipboard paths are blocked', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'browser recording is disabled in production mode' },
        active: null,
      },
    })
    clipboardWriteMock.mockRejectedValueOnce(new Error('Write permission denied'))
    const originalExecCommand = document.execCommand
    const execCommandMock = vi.fn().mockReturnValue(false)
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: execCommandMock,
    })

    try {
      await act(async () => {
        root.render(<RecordingStudio />)
      })
      await flushPromises()

      const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy Terminal Command'))
      expect(copyButton).toBeDefined()

      await act(async () => {
        copyButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
      await flushPromises()

      expect(execCommandMock).toHaveBeenCalledWith('copy')
      expect(container.textContent).toContain('Clipboard is blocked in this browser. Select the terminal command and press Cmd+C or Ctrl+C.')
    } finally {
      Object.defineProperty(document, 'execCommand', {
        configurable: true,
        value: originalExecCommand,
      })
    }
  })

  it('uses an already paired bridge as the browser recorder backend', async () => {
    apiMock.browserRecorderBridge = { origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'production mode' },
        active: null,
      },
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    expect(apiMock.getBrowserRecorderBridgeStatus).toHaveBeenCalled()
    expect(container.querySelector('[data-testid="browser-recorder-bridge-connected"]')).not.toBeNull()
    expect(container.textContent).toContain('Local bridge: http://127.0.0.1:49152')
  })

  it('does not offer browser recording when the controller omits browser recorder capability', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="browser-recorder-unavailable"]')).not.toBeNull()
    expect(container.textContent).toContain('controller does not expose browser recording endpoints')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent?.includes('Record Browser'))).toBe(false)
    expect(container.textContent).toContain('Proxy Setup')
    expect(container.textContent).not.toContain('Download CA Certificate')
  })

  it('adopts a live browser recording reported by the controller status', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: true },
        active: { id: 'brec-1', name: 'Browser Recording', status: 'recording' },
      },
    })

    await act(async () => {
      root.render(<RecordingStudio />)
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="browser-recorder-panel"]')).not.toBeNull()
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith('brec-1')
  })

  it('offers a device preset selector and threads it into start', async () => {
    await act(async () => { root.render(<RecordingStudio activeProject={{ id: 'proj-1', name: 'Web' } as any} />) })
    await flushPromises()

    const deviceSelect = container.querySelector('select[aria-label="Device preset"]') as HTMLSelectElement
    expect(deviceSelect).not.toBeNull()
    expect(deviceSelect.textContent).toContain('iphone 15 pro')

    const urlInput = container.querySelector('input[aria-label="Start URL address"]') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(urlInput, 'app.example.com/login')
      urlInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
      setter.call(deviceSelect, 'iphone_15_pro')
      deviceSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const recordButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Record Browser'))
    await act(async () => { recordButton!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()

    expect(apiMock.startBrowserRecording).toHaveBeenCalledWith(expect.objectContaining({ device_preset: 'iphone_15_pro' }))
  })

  it('offers an install affordance when browser recording is unavailable', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, code: 'browsers_not_installed', reason: 'Playwright Chromium is not installed on the controller host' },
        active: null,
        install: { status: 'idle' },
      },
    })
    await act(async () => { root.render(<RecordingStudio />) })
    await flushPromises()

    const installBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Install Recording Browser'))
    expect(installBtn).toBeDefined()
    await act(async () => { installBtn!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushPromises()
    expect(apiMock.installBrowserRecorder).toHaveBeenCalled()
  })

  it('keeps install failures visible and retryable when browser dependencies are missing', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'no recording browser installed' },
        active: null,
        install: { status: 'failed', error: 'download failed' },
      },
    })
    await act(async () => { root.render(<RecordingStudio />) })
    await flushPromises()

    const unavailable = container.querySelector('[data-testid="browser-recorder-unavailable"]')
    expect(unavailable?.textContent).toContain('Recording browser install failed')
    expect(unavailable?.textContent).toContain('download failed')
    expect(Array.from(unavailable?.querySelectorAll('button') || []).some(b => b.textContent?.includes('Install Recording Browser'))).toBe(true)
  })

  it('exposes proxy setup as a named disclosure button', async () => {
    await act(async () => { root.render(<RecordingStudio />) })
    await flushPromises()

    const setupToggle = container.querySelector<HTMLButtonElement>('button.rec-setup-toggle')
    expect(setupToggle).not.toBeNull()
    expect(setupToggle?.getAttribute('aria-expanded')).toBe('false')
    expect(setupToggle?.getAttribute('aria-controls')).toBe('recording-proxy-setup-steps')

    await act(async () => { setupToggle?.click() })

    expect(setupToggle?.getAttribute('aria-expanded')).toBe('true')
    expect(container.querySelector('#recording-proxy-setup-steps')).not.toBeNull()
  })

  it('starts the new project refresh immediately and ignores the previous project response', async () => {
    let resolveOld!: (value: { sessions: any[] }) => void
    const oldRequest = new Promise<{ sessions: any[] }>(resolve => {
      resolveOld = resolve
    })
    apiMock.listRecordings.mockImplementation((projectId?: string) => {
      if (projectId === 'proj-1') return oldRequest
      return Promise.resolve({
        sessions: [{
          id: 'rec-new',
          name: 'New Project Capture',
          status: 'stopped',
          started_at: '2026-05-12T20:00:00Z',
          requests: [],
          filters: { exclude_static: true },
        }],
      })
    })

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: 'proj-1', name: 'Old' } as any} />)
      await Promise.resolve()
    })
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: 'proj-2', name: 'New' } as any} />)
    })
    await flushPromises()

    expect(apiMock.listRecordings).toHaveBeenCalledWith('proj-2')
    expect(container.textContent).toContain('New Project Capture')

    await act(async () => {
      resolveOld({
        sessions: [{
          id: 'rec-old',
          name: 'Old Project Capture',
          status: 'stopped',
          started_at: '2026-05-12T20:00:00Z',
          requests: [],
          filters: { exclude_static: true },
        }],
      })
      await Promise.resolve()
    })
    expect(container.textContent).toContain('New Project Capture')
    expect(container.textContent).not.toContain('Old Project Capture')
  })

  it('ignores a recorder action that completes after the active project changes', async () => {
    let resolveStart!: (value: { session: any; proxy_addr: string; ca_cert_path: string }) => void
    const startRequest = new Promise<{ session: any; proxy_addr: string; ca_cert_path: string }>(resolve => {
      resolveStart = resolve
    })
    apiMock.startRecording.mockImplementationOnce(() => startRequest)

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: 'proj-1', name: 'Old' } as any} />)
    })
    await flushPromises()
    await openProxyAdvanced(container)
    const startButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent?.trim() === 'Start Recording')
    expect(startButton).toBeDefined()
    await act(async () => {
      startButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: 'proj-2', name: 'New' } as any} />)
    })
    await flushPromises()
    await act(async () => {
      resolveStart({
        session: {
          id: 'rec-old-project',
          name: 'Old Project Recording',
          status: 'recording',
          started_at: '2026-05-12T20:00:00Z',
          requests: [],
          filters: { exclude_static: true },
        },
        proxy_addr: ':8081',
        ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      })
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Old Project Recording')
    expect(container.textContent).toContain('New Recording')
  })

  it('does not offer install as a fix for no-display Docker controllers', async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ':8081',
      ca_cert_path: 'artifacts/recorder/ca-cert.pem',
      active: null,
      browser: {
        availability: { available: false, reason: 'no display detected on the controller host for a headed browser' },
        active: null,
        install: {
          status: 'failed',
          error: 'could not install driver: could not run driver: fork/exec /usr/bin/node: no such file or directory',
        },
      },
    })
    await act(async () => { root.render(<RecordingStudio />) })
    await flushPromises()

    const unavailable = container.querySelector('[data-testid="browser-recorder-unavailable"]')
    expect(unavailable?.textContent).toContain('no display detected on the controller host for a headed browser')
    expect(unavailable?.textContent).toContain('Install cannot add a display to this controller')
    expect(unavailable?.textContent).toContain('Recording browser install failed')
    expect(unavailable?.textContent).toContain('/usr/bin/node')
    expect(Array.from(unavailable?.querySelectorAll('button') || []).some(b => b.textContent?.includes('Install Recording Browser'))).toBe(false)
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

// The proxy recorder is a first-class option next to browser recording; keep
// this helper so older proxy tests still assert they are operating in that area.
async function openProxyAdvanced(container: HTMLElement) {
  expect(container.querySelector('[data-testid="rec-proxy-recorder-option"]')).not.toBeNull()
  if (!container.querySelector('.rec-setup-steps')) {
    const setupToggle = container.querySelector('.rec-setup-toggle')
    expect(setupToggle).not.toBeNull()
    await act(async () => {
      setupToggle!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
  await flushPromises()
  expect(container.querySelector('.rec-setup-steps')).not.toBeNull()
}
