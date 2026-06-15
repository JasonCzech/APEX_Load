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
  stopBrowserRecording: vi.fn(),
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

  it('builds macOS proxy shortcuts that save and restore the selected service', () => {
    const startCommand = buildMacOSStartProxyCommand('9090', `USB Bob's LAN`)
    const restoreCommand = buildMacOSRestoreProxyCommand(`USB Bob's LAN`)

    expect(recorderProxyPort('127.0.0.1:9090')).toBe('9090')
    expect(recorderProxyPort('bad-port')).toBe('8081')
    expect(startCommand).toContain(`SERVICE='USB Bob'\\''s LAN'`)
    expect(startCommand).toContain(`PROXY_PORT='9090'`)
    expect(startCommand).toContain('networksetup -getwebproxy "$SERVICE"')
    expect(startCommand).toContain('networksetup -setsecurewebproxystate "$SERVICE" on')
    expect(restoreCommand).toContain(`SERVICE='USB Bob'\\''s LAN'`)
    expect(restoreCommand).toContain('networksetup "$STATE_CMD" "$SERVICE" off')
    expect(restoreCommand).toContain('rm -f "$STATE_DIR/$STATE_KEY.http" "$STATE_DIR/$STATE_KEY.https"')
  })

  it('builds Windows 11 proxy shortcuts that save and restore per-user proxy settings', () => {
    const startCommand = buildWindowsStartProxyCommand('9090')
    const restoreCommand = buildWindowsRestoreProxyCommand()
    const fallbackStartCommand = buildWindowsStartProxyCommand('not-a-port')

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

    const urlInput = container.querySelector('input[placeholder="https://app.example.com/login"]') as HTMLInputElement
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
    expect(container.textContent).toContain('Enter an absolute http(s) URL to record')
  })

  it('starts a browser recording and docks the control panel', async () => {
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: 'proj-1', name: 'Web' } as any} />)
    })
    await flushPromises()

    const urlInput = container.querySelector('input[placeholder="https://app.example.com/login"]') as HTMLInputElement
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

    expect(apiMock.startBrowserRecording).toHaveBeenCalledWith(expect.objectContaining({
      start_url: 'https://app.example.com/login',
      project_id: 'proj-1',
      capture_har: true,
    }))
    // The companion popup opens (web shell) and the docked panel renders.
    expect(window.open).toHaveBeenCalledWith(
      expect.stringContaining('#/recorder-panel?session=brec-1'),
      'apexload-browser-recorder',
      expect.any(String),
    )
    expect(container.querySelector('[data-testid="browser-recorder-panel"]')).not.toBeNull()
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith('brec-1')
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
    expect(container.textContent).not.toContain('Install it from your enterprise software catalog')
    expect(container.textContent).toContain('1Download the Recording Agent')
    expect(container.textContent).toContain('2Run the local command')
    expect(container.textContent).toContain('3Verify the agent')
    expect(container.textContent).toContain('4Pair the local bridge')
    expect(container.textContent).toContain('Paste this command into Terminal or PowerShell')
    expect(container.textContent).toContain('Token included')
    expect(container.textContent).toContain('No separate token paste is needed')
    expect(container.textContent).toContain('Verify Agent')
    const command = container.querySelector('[data-testid="browser-recorder-bridge-command"]')?.textContent || ''
    const generatedToken = command.match(/--token ([0-9a-f]+)/)?.[1]
    expect(command).toContain('apexload-record-agent --addr 127.0.0.1:27777 --token')
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
    apiFetchMock.mockResolvedValueOnce({
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
    await act(async () => {
      downloadLink!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    })
    await flushPromises()

    expect(apiFetchMock).toHaveBeenCalledWith('/downloads/recording-agent/latest')
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

  it('copies and regenerates the dashboard-created bridge command token', async () => {
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
    const tokenBefore = commandBefore.match(/--token ([0-9a-f]+)/)?.[1]
    expect(commandBefore).toContain("--allow-origin 'http://localhost:3000'")
    expect(commandBefore).toContain("--allow-origin 'http://127.0.0.1:3000'")
    const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy Terminal Command'))
    expect(copyButton).toBeDefined()

    await act(async () => {
      copyButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(clipboardWriteMock).toHaveBeenCalledWith(commandBefore)

    const regenerateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Regenerate Token'))
    expect(regenerateButton).toBeDefined()
    await act(async () => {
      regenerateButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const commandAfter = container.querySelector('[data-testid="browser-recorder-bridge-command"]')?.textContent || ''
    const tokenAfter = commandAfter.match(/--token ([0-9a-f]+)/)?.[1]
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

    const urlInput = container.querySelector('input[placeholder="https://app.example.com/login"]') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(urlInput, 'https://app.example.com/login')
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
        availability: { available: false, reason: 'no recording browser installed' },
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
