// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { formatSnapshotRequestSection, formatSnapshotResponseSection } from '../utils/snapshotFormatUtils'
import { formatSOAPFaultSummary } from './soapFaultUtils'
import ScriptEditor from './ScriptEditor'

const { downloadTextFileMock } = vi.hoisted(() => ({
  downloadTextFileMock: vi.fn(),
}))

const { downloadBlobMock } = vi.hoisted(() => ({
  downloadBlobMock: vi.fn(),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getScript: vi.fn(),
  createScript: vi.fn(),
  updateScript: vi.fn(),
  validateScript: vi.fn(),
  validateDraftScript: vi.fn(),
  replayDraftScript: vi.fn(),
  replayScript: vi.fn(),
  startDraftBrowserReplaySession: vi.fn(),
  startBrowserReplaySession: vi.fn(),
  startDraftScriptDebugSession: vi.fn(),
  startScriptDebugSession: vi.fn(),
  previewBrowserToHTTP: vi.fn(),
  pauseBrowserReplaySession: vi.fn(),
  resumeBrowserReplaySession: vi.fn(),
  stopBrowserReplaySession: vi.fn(),
  pauseScriptDebugSession: vi.fn(),
  resumeScriptDebugSession: vi.fn(),
  stepScriptDebugSession: vi.fn(),
  stopScriptDebugSession: vi.fn(),
  updateScriptDebugBreakpoints: vi.fn(),
  listAuthoringComponents: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: vi.fn(),
}))

vi.mock('@uiw/react-codemirror', async () => {
  const { useEffect, useRef } = await import('react')
  const CodeMirrorMock = ({ value = '', onChange, onCreateEditor }: {
    value?: string
    onChange?: (value: string) => void
    onCreateEditor?: (view: unknown) => void
  }) => {
    const ref = useRef<HTMLTextAreaElement | null>(null)
    const created = useRef(false)
    useEffect(() => {
      if (created.current || !onCreateEditor) return
      created.current = true
      // Minimal EditorView stand-in: reports the textarea's live selection + value so the
      // production right-click handler (which reads view.state) can be exercised under test.
      onCreateEditor({
        dispatch: () => {},
        get state() {
          const el = ref.current
          const from = el?.selectionStart ?? 0
          const to = el?.selectionEnd ?? 0
          const doc = el?.value ?? value
          return {
            selection: { main: { from, to } },
            doc: { toString: () => doc, lines: 1, line: () => ({ from: 0 }) },
          }
        },
      })
    }, [onCreateEditor, value])
    return (
      <textarea
        data-testid="codemirror-mock"
        ref={ref}
        value={value}
        onChange={event => onChange?.(event.currentTarget.value)}
      />
    )
  }
  return { default: CodeMirrorMock }
})

vi.mock('./scriptEditorUtils', async () => {
  const actual = await vi.importActual<typeof import('./scriptEditorUtils')>('./scriptEditorUtils')
  return {
    ...actual,
    downloadTextFile: downloadTextFileMock,
  }
})

vi.mock('../utils/fileUtils', async () => {
  const actual = await vi.importActual<typeof import('../utils/fileUtils')>('../utils/fileUtils')
  return {
    ...actual,
    downloadBlob: downloadBlobMock,
  }
})

const zipDecoder = new TextDecoder()

async function readStoredZipEntries(blob: Blob) {
  const bytes = new Uint8Array(await blob.arrayBuffer())
  const entries = new Map<string, Uint8Array>()
  let offset = 0

  while (offset + 30 <= bytes.length) {
    const view = new DataView(bytes.buffer, bytes.byteOffset + offset)
    const signature = view.getUint32(0, true)
    if (signature !== 0x04034b50) {
      break
    }

    const compressedSize = view.getUint32(18, true)
    const fileNameLength = view.getUint16(26, true)
    const extraFieldLength = view.getUint16(28, true)
    const fileNameStart = offset + 30
    const fileNameEnd = fileNameStart + fileNameLength
    const dataStart = fileNameEnd + extraFieldLength
    const dataEnd = dataStart + compressedSize
    const fileName = zipDecoder.decode(bytes.slice(fileNameStart, fileNameEnd))

    entries.set(fileName, bytes.slice(dataStart, dataEnd))
    offset = dataEnd
  }

  return entries
}

function cssRule(css: string, selector: string) {
  const selectorIndex = css.indexOf(`${selector} {`)
  expect(selectorIndex).toBeGreaterThanOrEqual(0)
  const bodyStart = css.indexOf('{', selectorIndex) + 1
  const bodyEnd = css.indexOf('}', bodyStart)
  return css.slice(bodyStart, bodyEnd)
}

function findButtonByText(root: ParentNode, text: string) {
  return Array.from(root.querySelectorAll('button')).find(button => button.textContent?.trim() === text)
}

function findButtonByTitle(root: ParentNode, title: string) {
  return Array.from(root.querySelectorAll('button')).find(button => button.getAttribute('title') === title)
}

function findWorkspaceTab(root: ParentNode, label: string) {
  return Array.from(root.querySelectorAll('button[role="tab"]')).find(button => button.textContent?.trim() === label)
}

function findCenterTab(root: ParentNode, label: string) {
  return Array.from(root.querySelectorAll('.center-tab-button')).find(button => button.textContent?.trim() === label)
}

async function activateParameterDetailTab(root: ParentNode, label: 'Parameter Information' | 'Parameter Data') {
  const tab = Array.from(root.querySelectorAll('.parameter-detail-tabs [role="tab"]'))
    .find(button => button.textContent?.trim() === label)
  if (tab) {
    await act(async () => {
      tab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
}

async function activateParameterDataTab(root: ParentNode) {
  await activateParameterDetailTab(root, 'Parameter Data')
}

async function activateParameterInfoTab(root: ParentNode) {
  await activateParameterDetailTab(root, 'Parameter Information')
}

function changeTextareaValue(textarea: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
  setter?.call(textarea, value)
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
}

function changeInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function findOpenDebuggerButton(root: ParentNode) {
  return findWorkspaceTab(root, 'Debug')
}

function findDebuggerRunButton(root: ParentNode, title = 'Run Replay') {
  return findButtonByTitle(root, title)
}

function debugSessionResponse(report: {
  group: string
  protocol: string
  started_at: string
  completed_at?: string
  success: boolean
  error?: string
  final_variables?: Record<string, string>
  events: unknown[]
}, overrides: Record<string, unknown> = {}) {
  const terminal = Boolean(report.completed_at)
  return {
    replay_session_id: 'debug-session-1',
    debug_session_id: 'debug-session-1',
    session: {
      id: 'debug-session-1',
      group: report.group,
      protocol: report.protocol,
      status: terminal ? (report.success ? 'completed' : 'failed') : 'running',
      started_at: report.started_at,
      completed_at: report.completed_at,
      current_event_index: report.events.length - 1,
      events: report.events,
      report,
      logs: [],
      raw_output: [],
      ...overrides,
    },
  }
}

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
        network_simulation: { mode: 'preset', preset: 'dsl', latency_ms: 50, jitter_ms: 10, packet_loss_pct: 1.5 },
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
    window.localStorage.clear()
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
    downloadTextFileMock.mockReset()
    downloadBlobMock.mockReset()
    apiMock.getScript.mockResolvedValue({ script: initialScript })
    apiMock.createScript.mockResolvedValue({ id: 'script-1' })
    apiMock.updateScript.mockResolvedValue({ id: 'script-1' })
    apiMock.validateScript.mockResolvedValue({
      script_id: 'script-1',
      valid: true,
      issues: [],
      warnings: [],
    })
    apiMock.validateDraftScript.mockResolvedValue({
      script_id: '',
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
    apiMock.startDraftScriptDebugSession.mockResolvedValue(debugSessionResponse({
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: true,
      events: [],
    }))
    apiMock.startScriptDebugSession.mockResolvedValue(debugSessionResponse({
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: true,
      events: [],
    }))
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
    apiMock.previewBrowserToHTTP.mockResolvedValue({
      schema_version: 'apexload.browser_to_http_preview.v1',
      generated_at: '2026-05-14T20:00:02Z',
      replay_session_id: 'browser-replay-1',
      generated_script: {
        ...initialScript,
        name: 'Checkout Flow HTTP Draft',
        protocol: 'http',
        actions: [
          { name: 'GET /api/cart', type: 'request', method: 'GET', target: 'https://shop.example.test/api/cart' },
        ],
      },
      review: {
        status: 'needs_repair',
        source_browser_actions: 1,
        captured_requests: 1,
        generated_http_actions: 1,
        skipped_static_assets: 0,
        correlation_candidates: [{ name: 'csrf_token', source: 'query', value: 'abcdef0123456789', evidence: 'GET /api/cart' }],
        data_source_candidates: [{ name: 'customer_id', source: 'query', value: '123', evidence: 'GET /api/cart' }],
        findings: ['1 replay network request(s) were converted into an HTTP draft.'],
      },
      scenario_template: {
        name: 'Checkout Flow Canary Plus HTTP Load',
        duration: '10m',
        groups: [
          { name: 'browser_canary', script_id: '{{browser_script_id}}', protocol: 'browser', vusers: 1, purpose: 'UX and migration evidence' },
          { name: 'protocol_load', script_id: '{{generated_http_script_id}}', protocol: 'http', vusers: 50, purpose: 'Protocol-load density after validation and approval' },
        ],
      },
    })
    apiMock.pauseBrowserReplaySession.mockResolvedValue({ session: { status: 'paused' } })
    apiMock.resumeBrowserReplaySession.mockResolvedValue({ session: { status: 'running' } })
    apiMock.stopBrowserReplaySession.mockResolvedValue(undefined)
    apiMock.pauseScriptDebugSession.mockResolvedValue({ session: { status: 'paused' } })
    apiMock.resumeScriptDebugSession.mockResolvedValue({ session: { status: 'running' } })
    apiMock.stepScriptDebugSession.mockResolvedValue({ session: { status: 'paused' } })
    apiMock.stopScriptDebugSession.mockResolvedValue({ session: { status: 'stopped' } })
    apiMock.updateScriptDebugBreakpoints.mockResolvedValue({ session: { breakpoints: [] } })
    apiMock.listAuthoringComponents.mockResolvedValue({ components: [], count: 0 })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('keeps imported metadata in the outline instead of duplicating it in the page header', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    let headerActions = container.querySelector('.editor-header .editor-actions')
    expect(headerActions?.textContent).not.toContain('Imported Metadata')
    expect(container.querySelector('.editor-outline-nav')?.textContent).not.toContain('Imported Metadata')

    const importedScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'checkout.usr',
          preserved: {
            runtime_files: ['default.cfg'],
          },
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} />)
    })

    headerActions = container.querySelector('.editor-header .editor-actions')
    expect(headerActions?.textContent).not.toContain('Imported Metadata')
    const outlineButton = Array.from(container.querySelector('.editor-outline-nav')?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Imported Metadata')
    expect(outlineButton).toBeDefined()

    await act(async () => {
      outlineButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const metadataSection = container.querySelector('#imported-metadata-section')
    const actionDetail = container.querySelector('.action-detail')
    expect(metadataSection?.textContent).toContain('checkout.usr')
    expect(actionDetail).toBeNull()
    expect(container.querySelector('.author-code-center')?.contains(metadataSection)).toBe(true)
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).toContain('Outline')
    expect(metadataSection?.getAttribute('role')).toBeNull()
    expect(metadataSection?.classList.contains('editor-support-dialog')).toBe(false)
    expect(container.querySelector('.support-dialog-import')).toBeNull()

    const firstAction = container.querySelector('.action-item')
    await act(async () => {
      firstAction?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('#imported-metadata-section')).not.toBeNull()
    expect(container.querySelector('.action-editor-panel')).not.toBeNull()
  })

  it('renders grouped HTTP sequence actions with child request details', async () => {
    const sequenceScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        name: 'S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue',
        type: 'http_sequence',
        method: 'SEQUENCE',
        target: '/aura',
        metadata: {
          source_transaction: 'S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue',
          loadrunner_request_count: 2,
        },
        params: {
          requests: [{
            name: 'Address_Search_Aura',
            type: 'POST',
            method: 'POST',
            target: '/aura',
            body: 'message={{param.p_username}}',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            metadata: {
              source_subtransaction: 'ACTION$execute_getSearchResults',
            },
            extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' }],
          }, {
            name: 'Checkout_Aura',
            type: 'POST',
            method: 'POST',
            target: '/aura',
            assertions: [{ type: 'status_code', expected: 200 }],
          }],
        },
      }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={sequenceScript} />)
    })

    const groupedAction = Array.from(container.querySelectorAll('.action-item'))
      .find(item => item.textContent?.includes('S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue'))
    expect(groupedAction).toBeDefined()
    expect(groupedAction?.textContent).toContain('SEQ')
    await act(async () => {
      groupedAction?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.action-editor-panel')?.textContent).toContain('S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue')
    expect(container.textContent).toContain('Grouped Requests')
    expect(container.textContent).toContain('2 requests')
    expect(container.textContent).toContain('Address_Search_Aura')
    expect(container.textContent).toContain('Checkout_Aura')
    expect(container.textContent).toContain('ACTION$execute_getSearchResults')
    expect(container.querySelector('.action-list')?.textContent).toContain('R2')
    expect(container.querySelector('.action-list')?.textContent).toContain('E1')
  })

  it('layers the parameter data dialog above the sticky app chrome', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const editorCss = readFileSync(`${cwd}/src/components/ScriptEditor.css`, 'utf8') as string
    const backdropRule = cssRule(editorCss, '.editor-dialog-backdrop')
    const dialogRule = cssRule(editorCss, '.parameter-data-dialog')
    const documentDialogRule = cssRule(editorCss, '.editor-dialog.parameter-data-document')
    const gridScrollRule = cssRule(editorCss, '.parameter-grid-scroll')
    const gridHeaderRule = cssRule(editorCss, '.parameter-grid thead th')

    expect(backdropRule).toContain('z-index: 220;')
    expect(dialogRule).toContain('width: min(1440px, calc(100vw - 48px));')
    expect(documentDialogRule).toContain('width: 100%;')
    expect(documentDialogRule).toContain('max-width: none;')
    expect(documentDialogRule).toContain('align-self: stretch;')
    expect(gridScrollRule).toContain('max-height: min(440px, 45vh);')
    expect(gridHeaderRule).toContain('position: sticky;')
    expect(gridHeaderRule).toContain('top: 0;')
  })

  it('keeps the landing editor focused on script name while support tools live in the outline', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(container.querySelector('.editor-support-toolbar')).toBeNull()
    const namePanel = container.querySelector('.script-name-panel')
    const commandBar = container.querySelector('.script-ide-command-bar')
    expect(namePanel).toBeTruthy()
    expect(commandBar).toBeTruthy()
    expect(Boolean(namePanel!.compareDocumentPosition(commandBar!) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
    expect(namePanel?.textContent).toContain('Script Name')
    expect(namePanel?.textContent).not.toContain('Runtime / RTS')
    expect(namePanel?.textContent).not.toContain('Replay Output')
    expect(container.querySelectorAll('input[placeholder="My_Test_Script"]')).toHaveLength(1)

    const outline = container.querySelector('.editor-outline-list')
    expect(outline?.textContent).toContain('Script Configuration')
    expect(outline?.textContent).toContain('Runtime / RTS')
    expect(outline?.textContent).toContain('Parameter Data')
    expect(outline?.textContent).not.toContain('Replay Output')
    expect(outline?.textContent).not.toContain('Debug Session')

    const configDialog = container.querySelector('[aria-label="Script Configuration"]')
    const runtimeDialog = container.querySelector('[aria-label="Runtime Settings"]')
    expect(configDialog?.className).toContain('center-document-config')
    expect(runtimeDialog?.className).toContain('center-document-runtime')
    expect(configDialog?.className).not.toContain('open')
    expect(container.querySelector('[aria-label="Script Debugger"]')).toBeNull()
    expect(runtimeDialog?.className).not.toContain('open')

    const configLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Script Configuration'))
    expect(configLauncher).toBeDefined()
    if (!configLauncher) return

    await act(async () => {
      configLauncher.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const activeCenterTab = Array.from(container.querySelectorAll('.center-tab-button')).find(button => button.textContent?.trim() === 'Configuration')
    expect(activeCenterTab?.getAttribute('aria-selected')).toBe('true')
  })

  it('keeps the script JSON pane visible in Author and Debug workbench modes', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(findWorkspaceTab(container, '{ } JSON')).toBeUndefined()
    expect(findWorkspaceTab(container, 'JSON')).toBeUndefined()
    expect(findCenterTab(container, 'Script JSON')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('button[aria-label="Close Script JSON"]')).toBeNull()
    expect(container.querySelector('[aria-label="Script JSON"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Script JSON"]')?.textContent).toContain('Synced')
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"Checkout Flow"')

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) return

    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Script JSON"]')).not.toBeNull()
    expect(findCenterTab(container, 'Script JSON')).not.toBeUndefined()
    expect(findCenterTab(container, 'Browser Replay')).toBeUndefined()
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"Checkout Flow"')
  })

  it('selects the Browser Replay center tab for browser scripts in Debug mode', async () => {
    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        base_url: 'https://shop.example.com',
        browser: {
          engine: 'chromium',
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

    expect(findCenterTab(container, 'Browser Replay')).toBeUndefined()

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) return

    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(findCenterTab(container, 'Browser Replay')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.browser-debugger-main')).not.toBeNull()
    expect(container.textContent).toContain('Browser Replay Cockpit')
    expect(container.textContent).toContain('No screenshot yet')
  })

  it('shows imported lifecycle phases in the action list and debug rail', async () => {
    const lifecycleScript: ScriptDefinition = {
      ...initialScript,
      init: [{
        name: 'Init Login',
        type: 'request',
        method: 'POST',
        target: '/session',
        headers: {},
        body: '{}',
        extractors: [{ name: 'token', source: 'body', type: 'jsonpath', expression: '$.token' }],
      }],
      actions: [{
        ...initialScript.actions[0],
        name: 'Checkout',
        target: '/checkout',
      }],
      teardown: [{
        name: 'Logout',
        type: 'request',
        method: 'POST',
        target: '/logout',
        headers: {},
        body: '',
      }],
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'checkout.usr',
          preserved: {},
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={lifecycleScript} />)
    })

    expect(container.querySelector('.editor-outline-metrics')).toBeNull()
    expect(Array.from(container.querySelectorAll('.action-phase-label')).map(label => label.textContent)).toEqual([
      'Init',
      'Action',
      'Teardown',
    ])

    const initRow = Array.from(container.querySelectorAll('.action-item')).find(row => row.textContent?.includes('Init Login'))
    expect(initRow).toBeDefined()
    await act(async () => {
      initRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('[aria-label="Script JSON"]')?.textContent).toContain('init[0]')

    const debugTab = findOpenDebuggerButton(container)
    await act(async () => {
      debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')?.textContent).toContain('3 actions planned')
    expect(container.querySelector('[aria-label="Script Debugger"]')?.textContent).toContain('Init 1')
    expect(container.querySelector('[aria-label="Script Debugger"]')?.textContent).toContain('Teardown 1')
  })

  it('opens and focuses center documents from outline controls', async () => {
    const importedScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'checkout.usr',
          preserved: {},
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} />)
    })

    const closeImported = findButtonByText(container, '×')
    if (closeImported?.getAttribute('aria-label') === 'Close Imported Metadata') {
      await act(async () => {
        closeImported.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }

    const configLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Script Configuration'))
    expect(configLauncher).toBeDefined()
    await act(async () => {
      configLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Configuration')?.getAttribute('aria-selected')).toBe('true')

    const runtimeLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Runtime / RTS'))
    expect(runtimeLauncher).toBeDefined()
    await act(async () => {
      runtimeLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Runtime Settings')?.getAttribute('aria-selected')).toBe('true')

    const importedLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Imported Metadata'))
    expect(importedLauncher).toBeDefined()
    await act(async () => {
      importedLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Imported Metadata')?.getAttribute('aria-selected')).toBe('true')

    const parameterLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Parameter Data'))
    expect(parameterLauncher).toBeDefined()
    await act(async () => {
      parameterLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Parameter Data')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('[aria-label="Parameter Data"]')?.textContent).toContain('Manage parameters and reusable parameter data')
  })

  it('only exposes the Imported Metadata center tab for scripts with actual import metadata', async () => {
    const staleImportShellScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {},
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={staleImportShellScript} />)
    })

    expect(findCenterTab(container, 'Imported Metadata')).toBeUndefined()
    expect(container.querySelector('#imported-metadata-section')).toBeNull()
    expect(container.querySelector('.editor-outline-nav')?.textContent).not.toContain('Imported Metadata')
  })

  it('lets the debug request and response pane close to expand the center workbench', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    await act(async () => {
      debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('.debugger-center')).toBeNull()
    expect(container.querySelector('.debugger-shell')?.classList.contains('debugger-detail-collapsed')).toBe(true)

    const showDetails = findButtonByTitle(container, 'Show request and response panel')
    expect(showDetails).toBeDefined()
    await act(async () => {
      showDetails?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.debugger-center')).not.toBeNull()
    const closeDetails = findButtonByTitle(container, 'Close request and response panel')
    expect(closeDetails).toBeDefined()
    await act(async () => {
      closeDetails?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.debugger-center')).toBeNull()
    expect(container.querySelector('.debugger-shell')?.classList.contains('debugger-detail-collapsed')).toBe(true)
    expect(container.querySelector('[aria-label="Script JSON"]')).not.toBeNull()
  })

  it('collapses and restores the right sidebars from Author and Debug modes', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(container.querySelector('.editor-utility-sidebar')).not.toBeNull()
    const collapseAuthorSidebar = container.querySelector('.editor-utility-sidebar button[aria-label="Collapse right sidebar"]') as HTMLButtonElement | null
    expect(collapseAuthorSidebar).not.toBeNull()
    await act(async () => {
      collapseAuthorSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.editor-utility-sidebar')).toBeNull()
    const showAuthorSidebar = findButtonByTitle(container, 'Show right sidebar')
    expect(showAuthorSidebar).toBeDefined()
    await act(async () => {
      showAuthorSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.editor-utility-sidebar')).not.toBeNull()

    const debugTab = findOpenDebuggerButton(container)
    await act(async () => {
      debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('.debugger-variables-panel')).toBeNull()
    expect(container.querySelector('.debugger-shell')?.classList.contains('debugger-variables-collapsed')).toBe(true)
    const showDebugSidebar = findButtonByTitle(container, 'Show right sidebar')
    expect(showDebugSidebar).toBeDefined()
    await act(async () => {
      showDebugSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.debugger-variables-panel')).not.toBeNull()

    const collapseDebugSidebar = container.querySelector('.debugger-variables-panel button[aria-label="Collapse right sidebar"]') as HTMLButtonElement | null
    expect(collapseDebugSidebar).not.toBeNull()
    await act(async () => {
      collapseDebugSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.debugger-variables-panel')).toBeNull()
    expect(container.querySelector('.debugger-shell')?.classList.contains('debugger-variables-collapsed')).toBe(true)
  })

  it('persists center tab order and drops unavailable document tabs', async () => {
    window.localStorage.setItem('scriptEditorCenterTabs:draft', JSON.stringify({
      tabs: ['script-json', 'parameter-data', 'configuration'],
      activeTabId: 'parameter-data',
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(findCenterTab(container, 'Parameter Data')?.getAttribute('aria-selected')).toBe('true')
    expect(Array.from(container.querySelectorAll('.center-tab-button')).map(button => button.textContent?.trim())).toEqual([
      'Script JSON',
      'Parameter Data',
      'Configuration',
    ])

    await act(async () => {
      root.unmount()
    })
    container.innerHTML = ''
    root = createRoot(container)
    window.localStorage.setItem('scriptEditorCenterTabs:draft', JSON.stringify({
      tabs: ['script-json', 'imported-metadata', 'parameter-data'],
      activeTabId: 'imported-metadata',
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
      await Promise.resolve()
    })

    expect(findCenterTab(container, 'Imported Metadata')).toBeUndefined()
    expect(findCenterTab(container, 'Script JSON')?.getAttribute('aria-selected')).toBe('true')
  })

  it('applies valid manual JSON edits into the structured authoring tools and save payload', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const codeArea = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(codeArea).not.toBeNull()
    if (!codeArea) return

    const editedScript = {
      ...initialScript,
      name: 'Checkout JSON Draft',
      actions: [
        ...initialScript.actions,
        {
          name: 'Get Cart',
          type: 'request',
          method: 'GET',
          target: '/cart',
          headers: {},
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
        },
      ],
    }

    await act(async () => {
      changeTextareaValue(codeArea, JSON.stringify({ script: editedScript }, null, 2))
      await Promise.resolve()
    })

    expect(container.querySelector('.script-code-status.dirty')?.textContent).toContain('Unsaved JSON edits')

    const applyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Apply JSON')
    expect(applyButton).toBeDefined()
    if (!applyButton) return

    await act(async () => {
      applyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect((container.querySelector('input[placeholder="My_Test_Script"]') as HTMLInputElement | null)?.value).toBe('Checkout JSON Draft')
    expect(container.querySelector('.action-list')?.textContent).toContain('Get Cart')

    const saveButton = Array.from(container.querySelectorAll('.editor-header .editor-actions button')).find(button => button.textContent === 'Save Script')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout JSON Draft',
      actions: expect.arrayContaining([expect.objectContaining({ name: 'Get Cart' })]),
    }), 'project-1', undefined)
  })

  it('blocks save for invalid manual JSON and pins the error in Problems', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const codeArea = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(codeArea).not.toBeNull()
    if (!codeArea) return

    await act(async () => {
      changeTextareaValue(codeArea, '{ "script": ')
      await Promise.resolve()
    })

    expect(container.querySelector('.script-code-status.invalid')?.textContent).toContain('Invalid JSON')

    const configTab = findCenterTab(container, 'Configuration')
    expect(configTab).toBeDefined()
    await act(async () => {
      configTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.center-document-blocked')?.textContent).toContain('Script JSON is invalid')
    expect(container.querySelector('.center-document-blocked')?.textContent).toContain('Fix or reset the Script JSON draft')

    const saveButton = Array.from(container.querySelectorAll('.editor-header .editor-actions button')).find(button => button.textContent === 'Save Script')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).not.toHaveBeenCalled()

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) return

    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Problems"]')?.textContent).toContain('Script JSON is invalid')
  })

  it('renders browser actions without outline metric pills', async () => {
    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        run_logic: { ...initialScript.config.run_logic, auto_transactions: 'per_action' },
      },
      actions: [
        {
          name: 'Open login',
          type: 'browser_navigate',
          target: '/login?user={{param.username}}',
          wait_for: { type: 'selector', value: '#login', timeout_ms: 10000 },
          assertions: [{ type: 'visible_text', value: 'Sign in' }],
        },
        {
          name: 'Submit login',
          type: 'browser_click',
          target: '',
          selector: 'button[type="submit"]',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    expect(container.querySelector('[aria-label="Script outline summary"]')).toBeNull()
    expect(container.querySelector('[aria-label="Graphical step outline"]')).toBeNull()
    expect(container.querySelectorAll('.editor-step-outline-row')).toHaveLength(0)

    const actionRows = Array.from(container.querySelectorAll('.action-item'))
    expect(actionRows).toHaveLength(2)
    const submitRow = actionRows.find(row => row.textContent?.includes('Submit login'))
    expect(submitRow).toBeDefined()
    if (!submitRow) {
      return
    }

    await act(async () => {
      submitRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(submitRow.className).toContain('active')
  })

  it('opens Web2 object descriptor and HTTP conversion review dialogs from the outline', async () => {
    const importedWeb2Script: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      actions: [
        {
          name: 'Click Checkout',
          type: 'browser_click',
          target: '',
          selector: '',
        },
      ],
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'CheckoutWeb2.zip',
          preserved: {
            source_script_type: 'TruClient 2.0 - Web',
            trueclient_archive_inspection: {
              object_repositories: [{ path: 'ObjectRepository/MainObjects.xml' }],
              unsupported_fragments: [],
              steps: [
                {
                  step_id: 'step-1',
                  index: '1',
                  action: 'Click',
                  action_name: 'Click Checkout',
                  object_descriptor_id: 'obj_checkout',
                  status: 'supported',
                  frame_context: 'main-frame',
                  replay_diagnostics: [
                    { code: 'frame_mismatch_risk', summary: 'The imported step carries iframe/frame context.' },
                  ],
                  selector_candidates: [
                    { kind: 'css', value: '[data-testid="checkout"]', confidence: 92, reason: 'Stable test id' },
                  ],
                },
              ],
            },
          },
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedWeb2Script} />)
    })

    const objectButton = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Object Descriptors'))
    const conversionButton = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('HTTP Conversion Review'))
    expect(objectButton).toBeDefined()
    expect(conversionButton).toBeDefined()
    if (!objectButton || !conversionButton) {
      return
    }

    await act(async () => {
      objectButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const descriptorDialog = container.querySelector('[aria-label="Object Descriptors"]')
    expect(descriptorDialog?.className).toContain('center-document-object')
    expect(descriptorDialog?.textContent).toContain('obj_checkout')
    expect(descriptorDialog?.textContent).toContain('[data-testid="checkout"]')
    expect(descriptorDialog?.textContent).toContain('Frame: main-frame')
    expect(descriptorDialog?.textContent).toContain('The imported step carries iframe/frame context.')

    const useSelectorButton = Array.from(descriptorDialog?.querySelectorAll('button') || []).find(button => button.textContent?.includes('Use Selector'))
    expect(useSelectorButton).toBeDefined()
    if (!useSelectorButton) {
      return
    }

    await act(async () => {
      useSelectorButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Applied css selector to Click Checkout.')
    expect(container.textContent).toContain('[data-testid="checkout"]')

    await act(async () => {
      conversionButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const conversionDialog = container.querySelector('[aria-label="Browser To HTTP Conversion Review"]')
    expect(conversionDialog?.className).toContain('center-document-conversion')
    expect(conversionDialog?.textContent).toContain('Run browser replay before generating an HTTP draft.')
    expect(conversionDialog?.textContent).toContain('Generate HTTP Draft Preview')
    expect(conversionDialog?.textContent).toContain('Review Runtime Settings')
  })

  it('generates a Web2 HTTP draft preview from browser replay network evidence', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'browser-replay-1',
      session: {
        id: 'browser-replay-1',
        group: 'Checkout Web2-browser-replay',
        protocol: 'browser',
        status: 'completed',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:02Z',
        events: [
          {
            phase: 'action',
            action_name: 'Click Checkout',
            action_type: 'browser_click',
            target: '#checkout',
            success: true,
            started_at: '2026-05-14T20:00:00Z',
            completed_at: '2026-05-14T20:00:01Z',
            duration_ms: 31,
            network_entries: [
              { method: 'GET', url: 'https://shop.example.test/api/cart?customer_id=123&csrf_token=abcdef0123456789', status_code: 200, resource_type: 'fetch', duration_ms: 24 },
            ],
          },
        ],
      },
    })

    const importedWeb2Script: ScriptDefinition = {
      ...initialScript,
      name: 'Checkout Web2',
      protocol: 'browser',
      actions: [
        {
          name: 'Click Checkout',
          type: 'browser_click',
          target: '#checkout',
          selector: '#checkout',
        },
      ],
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'CheckoutWeb2.zip',
          preserved: {
            source_script_type: 'TruClient 2.0 - Web',
          },
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedWeb2Script} projectId="project-1" />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')?.className).toContain('open')
    expect(apiMock.startDraftScriptDebugSession).not.toHaveBeenCalled()

    const runReplayButton = findDebuggerRunButton(container, 'Run Browser Replay')
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const authorTab = findWorkspaceTab(container, 'Author')
    expect(authorTab).toBeDefined()
    if (!authorTab) {
      return
    }

    await act(async () => {
      authorTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const conversionButton = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('HTTP Conversion Review'))
    expect(conversionButton).toBeDefined()
    if (!conversionButton) {
      return
    }

    await act(async () => {
      conversionButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const generateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Generate HTTP Draft Preview')) as HTMLButtonElement | undefined
    expect(generateButton).toBeDefined()
    expect(generateButton?.disabled).toBe(false)
    if (!generateButton) {
      return
    }

    await act(async () => {
      generateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.previewBrowserToHTTP).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Web2 HTTP Draft',
      project_id: 'project-1',
      replay_session_id: 'browser-replay-1',
      source_browser_script: expect.objectContaining({ name: 'Checkout Web2', protocol: 'browser' }),
      network_entries: [
        expect.objectContaining({ method: 'GET', url: 'https://shop.example.test/api/cart?customer_id=123&csrf_token=abcdef0123456789' }),
      ],
    }))
    expect(container.textContent).toContain('Generated HTTP draft preview from browser replay evidence.')
    expect(container.textContent).toContain('Generated HTTP Draft Preview')
    expect(container.textContent).toContain('Checkout Flow HTTP Draft')
    expect(container.textContent).toContain('Correlation · csrf_token')
    expect(container.textContent).toContain('Data Source · customer_id')
    expect(container.textContent).toContain('browser_canary: browser / 1 VUsers')
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
    expect(container.querySelector('[aria-label="Script Configuration"]')?.textContent).not.toContain('Protocol')
  })

  it('opens parameter data from the outline link', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(container.querySelector('.editor-dialog')).toBeNull()

    const parameterButton = Array.from(container.querySelectorAll('.editor-outline-link')).find(button =>
      button.textContent?.includes('Parameter Data'),
    )
    expect(parameterButton).toBeDefined()
    if (!parameterButton) {
      return
    }

    await act(async () => {
      parameterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const parameterDialog = container.querySelector('.parameter-data-dialog') as HTMLDivElement | null
    expect(parameterDialog).not.toBeNull()
    expect(parameterDialog?.textContent).not.toContain('Auto Parameterization')
    expect(parameterDialog?.textContent).not.toContain('Named Parameters')
    expect(parameterDialog?.textContent).toContain('Counter')
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Parameters')
    expect(container.querySelector('.parameter-sidebar-card')?.textContent).toContain('Counter')
    expect(parameterDialog?.textContent).toContain('session_id')
  })

  it('exposes native LoadRunner parity parameter types in the parameter dialog', async () => {
    const scriptWithoutParameters: ScriptDefinition = {
      ...initialScript,
      parameters: [],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={scriptWithoutParameters} />)
    })

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

    const addButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === '+ Add Parameter',
    )
    expect(addButton).toBeDefined()
    if (!addButton) {
      return
    }

    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-sidebar-card')?.textContent).toContain('List')
    expect((container.querySelector('input[value="param_1"]') as HTMLInputElement | null)?.value).toBe('param_1')

    const dialog = container.querySelector('[aria-label="Parameter Data"]')
    const chooseParameterType = async (value: string) => {
      const typeSelect = dialog?.querySelector('select[aria-label="Parameter Type for param_1"]') as HTMLSelectElement | null
      expect(typeSelect).not.toBeNull()
      if (!typeSelect) return
      const selectValueSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
      await act(async () => {
        selectValueSetter?.call(typeSelect, value)
        typeSelect.dispatchEvent(new Event('input', { bubbles: true }))
        typeSelect.dispatchEvent(new Event('change', { bubbles: true }))
      })
    }

    expect(container.textContent).toContain('Identity')
    const optionValues = Array.from(dialog?.querySelectorAll('[aria-label="Parameter Type for param_1"] option') || []).map(option => (option as HTMLOptionElement).value)
    expect(optionValues).toEqual(expect.arrayContaining(['current_iteration', 'host', 'time']))

    await chooseParameterType('current_iteration')

    expect(container.textContent).toContain('Type Options')
    expect((dialog?.querySelector('input[placeholder="start"]') as HTMLInputElement | null)?.value).toBe('1')
    expect((dialog?.querySelector('input[placeholder="step"]') as HTMLInputElement | null)?.value).toBe('1')

    await chooseParameterType('host')

    expect(dialog?.querySelector('input[placeholder="host-a, host-b"]')).not.toBeNull()

    await chooseParameterType('time')

    expect((dialog?.querySelector('input[placeholder="Go time layout"]') as HTMLInputElement | null)?.value).toBe('15:04:05')
  })

  it('selects a newly added parameter from the Parameters sidebar', async () => {
    const scriptWithoutParameters: ScriptDefinition = {
      ...initialScript,
      parameters: [],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={scriptWithoutParameters} initialFocusedSection="parameter-data" />)
    })

    const addButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === '+ Add Parameter',
    )
    expect(addButton).toBeDefined()
    if (!addButton) {
      return
    }

    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('param_1')
    expect((container.querySelector('input[value="param_1"]') as HTMLInputElement | null)?.value).toBe('param_1')
  })

  it('runs auto parameterization from the script JSON source tab', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(findCenterTab(container, 'Script JSON')?.getAttribute('aria-selected')).toBe('true')

    const activePanel = container.querySelector('.center-tab-panel.active') as HTMLDivElement | null
    const autoButton = Array.from(activePanel?.querySelectorAll('button') || []).find(button =>
      button.textContent?.trim() === 'Auto Parameterization',
    )
    expect(autoButton).toBeDefined()
    if (!autoButton) {
      return
    }

    await act(async () => {
      autoButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Review Candidates')
    expect(container.textContent).toContain('username')
    expect(container.textContent).toContain('demo')

    const applyButton = Array.from(activePanel?.querySelectorAll('button') || []).find(button =>
      button.textContent?.trim() === 'Apply Selected',
    )
    expect(applyButton).toBeDefined()
    if (!applyButton) {
      return
    }

    await act(async () => {
      applyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Applied auto-parameterization for 1 candidate.')
    expect(container.textContent).toContain('auto_params')
  })

  it('renders jitter and packet loss inputs for network emulation', async () => {
  await act(async () => {
    root.render(<ScriptEditor initialScript={initialScript} />)
  })

  const runtimeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
  expect(runtimeButton).toBeDefined()
  if (!runtimeButton) {
    return
  }

  await act(async () => {
    runtimeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })

  const webRuntimeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Web HTTP/HTML Compatibility'))
  expect(webRuntimeButton).toBeDefined()
  if (!webRuntimeButton) {
    return
  }

  await act(async () => {
    webRuntimeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })

  expect(container.textContent).toContain('Network Emulation')
  expect(container.textContent).toContain('Jitter (ms)')
  expect(container.textContent).toContain('Packet Loss (%)')

  const contentCheckArea = Array.from(container.querySelectorAll('textarea')).find(area =>
    area.getAttribute('placeholder') === 'Access Denied|any_response|true',
  ) as HTMLTextAreaElement | undefined
  expect(contentCheckArea).toBeDefined()
  expect(contentCheckArea?.readOnly).toBe(false)

  // Source IP pool and network-profile mix editors are present.
  const sourceIpArea = container.querySelector('textarea[aria-label="Source IP pool"]') as HTMLTextAreaElement | null
  expect(sourceIpArea).toBeTruthy()
  expect(container.textContent).toContain('Network Profile Mix')

  const addProfileButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Add Network Profile') as HTMLButtonElement | undefined
  expect(addProfileButton).toBeTruthy()
  if (!addProfileButton) return
  await act(async () => {
    addProfileButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  expect(container.querySelector('input[aria-label="Profile name"]')).toBeTruthy()
  expect(container.querySelector('input[aria-label="Profile weight"]')).toBeTruthy()
  })

  it('keeps existing RTS accordion sections open when HTTP Transport is expanded', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const runtimeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeButton).toBeDefined()
    if (!runtimeButton) return

    await act(async () => {
      runtimeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    async function clickSection(label: string) {
      const button = Array.from(container.querySelectorAll('.section-toggle')).find(candidate =>
        candidate.textContent?.includes(label),
      ) as HTMLButtonElement | undefined
      expect(button).toBeDefined()
      if (!button) return
      await act(async () => {
        button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }

    function sectionFor(label: string) {
      return Array.from(container.querySelectorAll('.settings-section')).find(section =>
        section.textContent?.includes(label),
      )
    }

    await clickSection('Think Time')
    await clickSection('Iteration Pacing')
    await clickSection('HTTP Transport')

    expect(sectionFor('Think Time')?.classList.contains('open')).toBe(true)
    expect(sectionFor('Iteration Pacing')?.classList.contains('open')).toBe(true)
    expect(sectionFor('HTTP Transport')?.classList.contains('open')).toBe(true)
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
    expect(container.querySelector('.action-detail')).toBeNull()
    expect(container.textContent).not.toContain('Select an action to edit its details')
  })

  it('shows imported correlation suggestions inside the metadata panel', async () => {
    const importedScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        initialScript.actions[0],
        {
          name: 'Complete Checkout',
          type: 'request',
          method: 'POST',
          target: '/checkout',
          headers: {
            'Content-Type': 'application/json',
          },
          body: '{"cart_id":"demo"}',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
        },
      ],
      metadata: {
        import: {
          source: 'sample-import',
          source_script_name: 'checkout.har',
          preserved: {
            parameter_files: ['LoginUsers.prm'],
            parameter_file_sections: {
              'LoginUsers.prm': {
                LoginUser: {
                  Type: 'File',
                  Table: 'Users.dat',
                  ColumnName: 'username',
                  SelectNextRow: 'Sequential',
                  UpdateValueOn: 'Each Iteration',
                },
              },
            },
            runtime_settings_migration_readiness: {
              score_pct: 72,
              blocked: true,
              summary: 'Migration readiness score 72%. Blocking runtime settings matched the selected block statuses: partial, compatibility_only.',
              blocking_statuses: ['partial', 'compatibility_only'],
              blocking_settings: [
                {
                  id: 'logging',
                  area: 'Logging',
                  concept: 'Log level, error-only, and extended logging options',
                  status: 'partial',
                  runtime_files: ['default.cfg'],
                },
              ],
            },
            runtime_settings_parity: [
              {
                id: 'run_logic_iterations',
                area: 'Run Logic',
                concept: 'Iterations',
                status: 'executed',
                runtime_files: ['default.cfg'],
              },
              {
                id: 'logging',
                area: 'Logging',
                concept: 'Log level, error-only, and extended logging options',
                status: 'partial',
                runtime_files: ['default.cfg'],
              },
            ],
            runtime_settings_status_counts: {
              executed: 1,
              partial: 1,
            },
            source_review: {
              source_files: [
                {
                  path: 'Action.c',
                  language: 'c',
                  content: 'Action()\n{\n\tweb_url("Login", "URL=https://example.com/login", LAST);\n\tweb_submit_data("Complete Checkout", "Action=https://example.com/checkout", LAST);\n}',
                  original_bytes: 80,
                  redacted: true,
                },
              ],
              mappings: [
                {
                  id: 'login-action',
                  source_file: 'Action.c',
                  start_line: 3,
                  end_line: 3,
                  function: 'web_url',
                  target_path: 'actions[0]',
                  target_name: 'Login',
                  target_kind: 'action',
                  status: 'converted',
                },
                {
                  id: 'checkout-action',
                  source_file: 'Action.c',
                  start_line: 4,
                  end_line: 4,
                  function: 'web_submit_data',
                  target_path: 'actions[1]',
                  target_name: 'Complete Checkout',
                  target_kind: 'action',
                  status: 'converted',
                },
              ],
            },
            correlation_suggestions: [
              {
                name: 'session_id',
                source: 'response',
                expression: '$.sessionId',
                sample_value: 'abc123',
                found_at: 'Step 1',
                used_at: ['Step 2'],
              },
            ],
          },
        },
      },
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: '',
          strategy: 'sequential',
          first_row_header: true,
          inline_data: 'username\nalice\nbob',
        },
      ],
      parameters: [
        ...(initialScript.parameters || []),
        {
          name: 'username',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'users',
          column: 'username',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} />)
    })

    const pageText = container.textContent || ''
    expect(pageText).toContain('Imported Metadata')
    expect(pageText).toContain('Runtime Setting Parity')
    expect(pageText).toContain('1 Executed, 1 Partial')
    expect(container.textContent).toContain('Migration Readiness')
    expect(container.textContent).toContain('72%')
    expect(container.textContent).toContain('Blocking runtime settings matched the selected block statuses')
    expect(pageText).toContain('Executed: Run Logic')
    expect(pageText).toContain('Iterations')
    expect(pageText).toContain('default.cfg')
    expect(container.textContent).toContain('Open Run Logic Settings')
    expect(container.textContent).toContain('Open Logging Settings')
    expect(container.textContent).toContain('Parameter Files')
    expect(container.textContent).toContain('LoginUsers.prm')
    expect(container.textContent).toContain('Parameter File Mappings')
    expect(container.textContent).toContain('LoginUsers.prm: LoginUser')
    expect(container.textContent).toContain('column username')
    expect(container.textContent).toContain('table Users.dat')
    expect(container.textContent).toContain('Generated source: users')
    expect(container.textContent).toContain('Open Mapped Data Source')
    expect(container.textContent).toContain('Generated Data Sources')
    expect(container.textContent).toContain('users')
    expect(container.textContent).toContain('2 rows')
    expect(container.textContent).toContain('Open In Parameter Data')
    expect(container.textContent).toContain('Source Mapping')
    expect(pageText.indexOf('Source Mapping')).toBeLessThan(pageText.indexOf('Parameter File Mappings'))
    expect(pageText.indexOf('Source Mapping')).toBeLessThan(pageText.indexOf('Source Script'))
    expect(container.textContent).toContain('Action.c:3')
    expect(container.textContent).toContain('web_url')
    expect(container.textContent).toContain('"target": "/login"')
    const metadataSection = container.querySelector('#imported-metadata-section')
    const actionDetail = container.querySelector('.action-detail')
    expect(metadataSection?.classList.contains('open')).toBe(true)
    expect(actionDetail).toBeNull()
    expect(container.querySelector('.author-code-center')?.contains(metadataSection)).toBe(true)
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).toContain('Outline')
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).toContain('Conversion Mapping')
    expect(findCenterTab(container, 'Conversion Mapping')).toBeDefined()
    expect(container.querySelector('[aria-label="Conversion Mapping"]')?.textContent).toContain('Action.c:3')
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).toContain('Authoring Signals')
    expect(metadataSection?.classList.contains('editor-support-dialog')).toBe(false)
    expect(metadataSection?.getAttribute('role')).toBeNull()
    expect(container.querySelector('.support-dialog-import')).toBeNull()
    expect(container.textContent).toContain('Correlation Suggestions')
    expect(container.textContent).toContain('session_id')
    expect(container.textContent).toContain('response via $.sessionId')
    expect(container.textContent).toContain('Found In')
    expect(container.textContent).toContain('Step 1: Login')
    expect(container.textContent).toContain('Used In')
    expect(container.textContent).toContain('Step 2: Complete Checkout')
    expect(container.textContent).toContain('abc123')
    expect(container.textContent).toContain('Open Step 1')
    expect(container.textContent).toContain('Open Step 2')

    const sidebarActions = container.querySelectorAll('.action-item')
    expect(sidebarActions.length).toBeGreaterThan(1)
    await act(async () => {
      sidebarActions[1]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Script JSON')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('[aria-label="Script JSON"]')?.textContent).toContain('actions[1]')
    expect(container.querySelector('#imported-metadata-section')).not.toBeNull()
    expect(container.querySelector('.action-editor-panel')).not.toBeNull()
    const activeSidebarAction = container.querySelector('.action-item.active') as HTMLDivElement | null
    expect(activeSidebarAction?.textContent).toContain('Complete Checkout')

    const conversionTab = findCenterTab(container, 'Conversion Mapping')
    expect(conversionTab).toBeDefined()
    if (!conversionTab) {
      return
    }

    await act(async () => {
      conversionTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Conversion Mapping')?.getAttribute('aria-selected')).toBe('true')

    await act(async () => {
      sidebarActions[1]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Conversion Mapping')?.getAttribute('aria-selected')).toBe('true')
    const activeMappingRow = container.querySelector('.source-map-table tbody tr.active') as HTMLTableRowElement | null
    expect(activeMappingRow?.dataset.mappingId).toBe('checkout-action')
    expect(activeMappingRow?.dataset.targetPath).toBe('actions[1]')

    const openDependentStepButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Open Step 2',
    )
    expect(openDependentStepButton).toBeDefined()
    if (!openDependentStepButton) {
      return
    }

    await act(async () => {
      openDependentStepButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const activeAction = container.querySelector('.action-item.active') as HTMLDivElement | null
    expect(activeAction).not.toBeNull()
    expect(activeAction?.textContent).toContain('Complete Checkout')
    expect(container.querySelector('#imported-metadata-section')).not.toBeNull()

    expect(container.querySelector('.editor-header .editor-actions')?.textContent).not.toContain('Imported Metadata')
    const reopenMetadataButton = Array.from(container.querySelector('.editor-outline-nav')?.querySelectorAll('button') || []).find(button =>
      button.textContent?.trim() === 'Imported Metadata',
    )
    expect(reopenMetadataButton).toBeDefined()
    if (!reopenMetadataButton) {
      return
    }

    await act(async () => {
      reopenMetadataButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('#imported-metadata-section')).not.toBeNull()

    const openRunLogicSettingsButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Open Run Logic Settings',
    )
    expect(openRunLogicSettingsButton).toBeDefined()
    if (!openRunLogicSettingsButton) {
      return
    }

    await act(async () => {
      openRunLogicSettingsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Block Scheduling')

    const openLoggingSettingsButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Open Logging Settings',
    )
    expect(openLoggingSettingsButton).toBeDefined()
    if (!openLoggingSettingsButton) {
      return
    }

    await act(async () => {
      openLoggingSettingsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Parameter substitution')

    const openMappedDataSourceButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Open Mapped Data Source',
    )
    expect(openMappedDataSourceButton).toBeDefined()
    if (!openMappedDataSourceButton) {
      return
    }

    await act(async () => {
      openMappedDataSourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.textContent).toContain('Parameter Data (1)')
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('username')
    expect(container.textContent).toContain('users')
    await activateParameterDataTab(container)
    expect(container.querySelector('.parameter-data-selected-view')).not.toBeNull()

    const openImportedDataSourceButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Open In Parameter Data',
    )
    expect(openImportedDataSourceButton).toBeDefined()
    if (!openImportedDataSourceButton) {
      return
    }

    await act(async () => {
      openImportedDataSourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.textContent).toContain('Parameter Data (1)')
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('username')
    expect(container.textContent).toContain('users')
    await activateParameterDataTab(container)
    expect(container.querySelector('.parameter-data-selected-view')).not.toBeNull()
  })

  it('preselects the requested action when a draft opens with a focus hint', async () => {
    const focusedScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        initialScript.actions[0],
        {
          name: 'Submit Order',
          type: 'request',
          method: 'POST',
          target: '/checkout',
          headers: {
            'Content-Type': 'application/json',
          },
          body: '{"cart_id":"demo"}',
          extractors: [],
          assertions: [{ type: 'status_code', expected: 200 }],
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={focusedScript} initialFocusedActionName="Submit Order" />)
    })

    const activeAction = container.querySelector('.action-item.active') as HTMLDivElement | null
    expect(activeAction).not.toBeNull()
    expect(activeAction?.textContent).toContain('Submit Order')
  })

  it('opens the runtime panel when a draft opens with a runtime focus hint', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initialFocusedSection="runtime" />)
    })

    expect(container.textContent).toContain('Web HTTP/HTML Compatibility')
  })

  it('opens the requested runtime area when a draft opens with a runtime area focus hint', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initialFocusedRuntimeArea="Logging" />)
    })

    expect(container.textContent).toContain('Parameter substitution')
  })

  it('opens parameter data when a draft opens with a parameter-data focus hint', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initialFocusedSection="parameter-data" />)
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.textContent).toContain('Parameters')
    expect(container.textContent).not.toContain('Named Parameters')
    expect(container.querySelector('.parameter-sidebar-card')?.textContent).toContain('Counter')
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('session_id')
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('{{param.session_id}}')
  })

  it('groups parameters by type and shows the selected file parameter data source', async () => {
    const sourceGroupedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        {
          name: 'bbclink01',
          type: 'file',
          update: 'each_iteration',
          selection: 'random',
          on_exhaustion: 'wrap',
          data_source: 'bbclinks',
          column: 'URL',
        },
        {
          name: 'bbclink02',
          type: 'file',
          update: 'each_iteration',
          selection: 'random',
          on_exhaustion: 'wrap',
          data_source: 'bbclinks',
          column: 'Label',
        },
        {
          name: 'username',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'users',
          column: 'username',
        },
      ],
      data_sources: [
        {
          name: 'bbclinks',
          type: 'dat',
          path: 'BBCRandomLinks.dat',
          strategy: 'random',
          first_row_header: true,
          inline_data: 'URL,Label\nhttps://bbc.example/a,BBC A',
        },
        {
          name: 'users',
          type: 'csv',
          path: 'users.csv',
          strategy: 'sequential',
          first_row_header: true,
          inline_data: 'username\nalice',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={sourceGroupedScript} initialFocusedSection="parameter-data" />)
    })

    const sidebar = container.querySelector('.parameter-sidebar-card') as HTMLDivElement | null
    expect(sidebar?.textContent).toContain('File')
    expect(sidebar?.textContent).toContain('bbclink01')
    expect(sidebar?.textContent).toContain('bbclink02')
    expect(sidebar?.textContent).toContain('username')
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('bbclink01')
    await activateParameterDataTab(container)
    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toContain('https://bbc.example/a')
    const selectedDataView = container.querySelector('.parameter-data-selected-view') as HTMLDivElement | null
    expect(selectedDataView?.textContent).toContain('bbclinks')
    expect(selectedDataView?.textContent).toContain('Label')
    expect(Array.from(selectedDataView?.querySelectorAll('.parameter-grid tbody input') || []).map(input => (input as HTMLInputElement).value)).toEqual([
      'https://bbc.example/a',
      'BBC A',
    ])
    expect(selectedDataView?.textContent).not.toContain('users.csv')
    expect(selectedDataView?.textContent).not.toContain('alice')

    await activateParameterInfoTab(container)
    const columnSelect = container.querySelector('.parameter-info-panel select[aria-label="Column for bbclink01"]') as HTMLSelectElement | null
    expect(columnSelect?.value).toBe('URL')
    const selectValueSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
    await act(async () => {
      selectValueSetter?.call(columnSelect, 'Label')
      columnSelect?.dispatchEvent(new Event('input', { bubbles: true }))
      columnSelect?.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect((container.querySelector('.parameter-info-panel select[aria-label="Column for bbclink01"]') as HTMLSelectElement | null)?.value).toBe('Label')
    const usernameItem = Array.from(container.querySelectorAll('.parameter-sidebar-item')).find(item => item.textContent?.includes('username'))
    expect(usernameItem).toBeDefined()
    await act(async () => {
      usernameItem?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('username')
    expect((container.querySelector('.parameter-info-panel') as HTMLDivElement | null)?.textContent).toContain('users.csv')
  })

  it('summarizes record counts and edits behavior in the parameter information panel', async () => {
    const statsScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        {
          name: 'city',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'cities',
          column: 'city',
        },
      ],
      data_sources: [
        {
          name: 'cities',
          type: 'csv',
          path: 'cities.csv',
          strategy: 'sequential',
          first_row_header: true,
          inline_data: 'city\nParis\nLyon\nParis',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={statsScript} initialFocusedSection="parameter-data" />)
    })

    const infoPanel = container.querySelector('.parameter-info-panel') as HTMLDivElement | null
    expect(infoPanel).not.toBeNull()
    expect(infoPanel?.textContent).toContain('Identity')
    expect(infoPanel?.textContent).toContain('Behavior')
    expect(infoPanel?.textContent).toContain('Data Mapping')
    expect(infoPanel?.textContent).toContain('Data Statistics')

    const statRows = Array.from(container.querySelectorAll('.parameter-info-stats-table tbody tr'))
    const statValue = (label: string) => statRows.find(row => row.querySelector('th')?.textContent === label)?.querySelector('td')?.textContent
    // three records (Paris, Lyon, Paris); two distinct values in the mapped column
    expect(statValue('Record Count')).toBe('3')
    expect(statValue('Unique Records')).toBe('2')

    const selectValueSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
    const cadence = container.querySelector('select[aria-label="Update Cadence for city"]') as HTMLSelectElement | null
    expect(cadence?.value).toBe('each_iteration')
    await act(async () => {
      selectValueSetter?.call(cadence, 'once')
      cadence?.dispatchEvent(new Event('input', { bubbles: true }))
      cadence?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect((container.querySelector('select[aria-label="Update Cadence for city"]') as HTMLSelectElement | null)?.value).toBe('once')

    const strategy = container.querySelector('select[aria-label="Iteration Strategy for city"]') as HTMLSelectElement | null
    expect(strategy?.value).toBe('sequential')
    await act(async () => {
      selectValueSetter?.call(strategy, 'unique')
      strategy?.dispatchEvent(new Event('input', { bubbles: true }))
      strategy?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect((container.querySelector('select[aria-label="Iteration Strategy for city"]') as HTMLSelectElement | null)?.value).toBe('unique')

    // The data table lives under the Parameter Data tab: it keeps its display options
    // but drops the old source-metadata header row.
    await activateParameterDataTab(container)
    const dataView = container.querySelector('.parameter-data-selected-view') as HTMLDivElement | null
    expect(dataView?.querySelector('table.parameter-grid')).not.toBeNull()
    expect(Array.from(dataView?.querySelectorAll('button') || []).some(button => button.textContent?.trim() === 'Raw Mode')).toBe(true)
    expect(dataView?.querySelector('input[placeholder="/absolute/path/to/data.csv (optional when inline data is used)"]')).toBeNull()
  })

  it('splits the parameter detail into Parameter Information and Parameter Data tabs', async () => {
    const tabScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        { name: 'city', type: 'file', update: 'each_iteration', selection: 'sequential', on_exhaustion: 'wrap', data_source: 'cities', column: 'city' },
        { name: 'visit_no', type: 'counter', update: 'each_iteration', start: 1, step: 1 },
      ],
      data_sources: [
        { name: 'cities', type: 'csv', path: 'cities.csv', strategy: 'sequential', first_row_header: true, inline_data: 'city\nParis\nLyon' },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={tabScript} initialFocusedSection="parameter-data" />)
    })

    const tabLabels = () => Array.from(container.querySelectorAll('.parameter-detail-tabs [role="tab"]')).map(tab => tab.textContent?.trim())
    const tab = (label: string) => Array.from(container.querySelectorAll('.parameter-detail-tabs [role="tab"]')).find(t => t.textContent?.trim() === label) as HTMLButtonElement | undefined
    const infoSection = () => container.querySelector('.parameter-detail-info')
    const dataSection = () => container.querySelector('.parameter-detail-data')

    // Data-backed parameter exposes both tabs; Information is selected by default.
    expect(tabLabels()).toEqual(['Parameter Information', 'Parameter Data'])
    expect(tab('Parameter Information')?.getAttribute('aria-selected')).toBe('true')
    expect(infoSection()?.hasAttribute('hidden')).toBe(false)
    expect(dataSection()?.hasAttribute('hidden')).toBe(true)
    expect(container.querySelector('.parameter-info-panel')).not.toBeNull()

    // Switching to Parameter Data reveals the data table and hides the information panel.
    await activateParameterDataTab(container)
    expect(tab('Parameter Data')?.getAttribute('aria-selected')).toBe('true')
    expect(infoSection()?.hasAttribute('hidden')).toBe(true)
    expect(dataSection()?.hasAttribute('hidden')).toBe(false)
    expect(container.querySelector('.parameter-detail-data .parameter-data-selected-view table.parameter-grid')).not.toBeNull()

    // Switching back to Parameter Information restores the info panel.
    await activateParameterInfoTab(container)
    expect(tab('Parameter Information')?.getAttribute('aria-selected')).toBe('true')
    expect(infoSection()?.hasAttribute('hidden')).toBe(false)
    expect(dataSection()?.hasAttribute('hidden')).toBe(true)

    // A non-data-backed parameter (counter) only offers the Information tab — no data table.
    const counterItem = Array.from(container.querySelectorAll('.parameter-sidebar-item')).find(item => item.textContent?.includes('visit_no'))
    expect(counterItem).toBeDefined()
    await act(async () => {
      counterItem?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(tabLabels()).toEqual(['Parameter Information'])
    expect(container.querySelector('.parameter-detail-data')).toBeNull()
  })

  it('merges csv and dat parameter source files into a common source file', async () => {
    const sourceMergeScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        {
          name: 'username',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'users',
          column: 'UserName',
        },
        {
          name: 'password',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'passwords',
          column: 'Password',
        },
        {
          name: 'iteration',
          type: 'current_iteration',
          update: 'each_iteration',
        },
      ],
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: 'Users.csv',
          strategy: 'sequential',
          first_row_header: true,
          inline_data: 'UserName\nalice\nbob',
        },
        {
          name: 'passwords',
          type: 'dat',
          path: 'Passwords.dat',
          strategy: 'sequential',
          delimiter: 'comma',
          first_row_header: true,
          inline_data: 'Password\na-pass\nb-pass',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={sourceMergeScript} initialFocusedSection="parameter-data" />)
    })

    const mergeButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Merge Source Files',
    ) as HTMLButtonElement | undefined
    expect(mergeButton).toBeDefined()
    expect(mergeButton?.disabled).toBe(false)

    await act(async () => {
      mergeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Merged 2 parameter source files into merged_parameters.')
    const parameterDialog = container.querySelector('.parameter-data-dialog') as HTMLDivElement | null
    expect(parameterDialog?.textContent).toContain('merged_parameters')
    expect(parameterDialog?.textContent).not.toContain('Users.csv')
    expect(parameterDialog?.textContent).not.toContain('Passwords.dat')
    expect(container.querySelector('.parameter-sidebar-card')?.textContent).toContain('File')
    expect(container.querySelector('.parameter-sidebar-card')?.textContent).toContain('Current Iteration')
    expect(parameterDialog?.textContent).toContain('Current Iteration')
    await activateParameterDataTab(container)
    const selectedDataView = container.querySelector('.parameter-data-selected-view') as HTMLDivElement | null
    expect(selectedDataView?.textContent).toContain('merged_parameters')
    expect(selectedDataView?.textContent).toContain('username')
    expect(selectedDataView?.textContent).toContain('password')
    expect(Array.from(selectedDataView?.querySelectorAll('.parameter-grid tbody input') || []).map(input => (input as HTMLInputElement).value)).toEqual([
      'alice',
      'a-pass',
      'bob',
      'b-pass',
    ])
  })

  it('removes the selected parameter and updates the Parameters sidebar', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initialFocusedSection="parameter-data" />)
    })

    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('session_id')
    const removeButton = Array.from(container.querySelectorAll('.parameter-data-dialog button')).find(button =>
      button.textContent?.trim() === 'Remove',
    )
    expect(removeButton).toBeDefined()
    if (!removeButton) return

    await act(async () => {
      removeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-sidebar-item')).toBeNull()
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('Select a parameter from the Parameters sidebar')
  })

  it('opens the requested data source when a draft opens with a data source focus hint', async () => {
    const focusedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        {
          name: 'username',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'users',
          column: 'username',
        },
      ],
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: 'Users.dat',
          strategy: 'sequential',
          first_row_header: true,
          inline_data: 'username\nalice\nbob',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={focusedScript} initialFocusedDataSourceName="users" />)
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.textContent).toContain('Parameter Data (1)')
    expect(container.querySelector('.parameter-data-selected-view')).not.toBeNull()
    expect(container.textContent).toContain('Users.dat')
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('username')
    expect(container.textContent).toContain('2 rows · 1 column · Auto: Comma')
    expect((container.querySelector('.parameter-grid-head input[placeholder="col_1"]') as HTMLInputElement | null)?.value).toBe('username')
    const firstRowCells = Array.from(container.querySelectorAll('.parameter-grid tbody tr:first-child td'))
    expect(firstRowCells[0]?.classList.contains('parameter-grid-row-action')).toBe(true)
    expect(firstRowCells[0]?.querySelector('button')?.getAttribute('aria-label')).toBe('Remove row 1')
    expect(firstRowCells[1]?.classList.contains('parameter-grid-index')).toBe(true)
    expect((container.querySelector('select[aria-label="Delimiter for username"]') as HTMLSelectElement | null)?.value).toBe('auto')
  })

  it('auto-detects tab data and lets the focused data source override delimiters for the grid preview', async () => {
    const focusedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        {
          name: 'username',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'users',
          column: 'username',
        },
      ],
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: '',
          strategy: 'sequential',
          delimiter: ',',
          first_row_header: true,
          inline_data: 'username\tpassword\nalice\tsecret\nbob\thunter2',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={focusedScript} initialFocusedDataSourceName="users" />)
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.textContent).toContain('2 rows · 2 columns · Auto: Tab')
    expect((container.querySelector('.parameter-grid-head input[placeholder="col_1"]') as HTMLInputElement | null)?.value).toBe('username')
    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual(['alice', 'secret', 'bob', 'hunter2'])
    expect((container.querySelector('select[aria-label="Delimiter for username"]') as HTMLSelectElement | null)?.value).toBe('auto')
    expect(container.querySelector('.parameter-delimiter-toggle')).toBeNull()

    const rawModeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Raw Mode') as HTMLButtonElement | undefined
    expect(rawModeButton).toBeDefined()
    await act(async () => {
      rawModeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const rawData = container.querySelector('textarea[aria-label="Raw data for users"]') as HTMLTextAreaElement | null
    expect(rawData?.value).toBe('username\tpassword\nalice\tsecret\nbob\thunter2')
    const textAreaValueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
    await act(async () => {
      textAreaValueSetter?.call(rawData, 'username\tpassword\nanna\tapass\nben\tbpass')
      rawData?.dispatchEvent(new Event('input', { bubbles: true }))
      rawData?.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const gridModeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Grid Mode') as HTMLButtonElement | undefined
    expect(gridModeButton).toBeDefined()
    await act(async () => {
      gridModeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual(['anna', 'apass', 'ben', 'bpass'])

    const delimiterSelect = container.querySelector('select[aria-label="Delimiter for username"]') as HTMLSelectElement | null
    expect(delimiterSelect).not.toBeNull()
    if (!delimiterSelect) {
      return
    }
    const selectValueSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set

    await act(async () => {
      selectValueSetter?.call(delimiterSelect, 'comma')
      delimiterSelect.dispatchEvent(new Event('input', { bubbles: true }))
      delimiterSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('2 rows · 1 column · Comma')
    expect((container.querySelector('.parameter-grid-head input[placeholder="col_1"]') as HTMLInputElement | null)?.value).toBe('username\tpassword')
    expect((container.querySelector('select[aria-label="Delimiter for username"]') as HTMLSelectElement | null)?.value).toBe('comma')

    await act(async () => {
      selectValueSetter?.call(delimiterSelect, 'auto')
      delimiterSelect.dispatchEvent(new Event('input', { bubbles: true }))
      delimiterSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('2 rows · 2 columns · Auto: Tab')
    expect((container.querySelector('.parameter-grid-head input[placeholder="col_1"]') as HTMLInputElement | null)?.value).toBe('username')
    expect((container.querySelector('select[aria-label="Delimiter for username"]') as HTMLSelectElement | null)?.value).toBe('auto')
  })

  it('sorts focused data source rows by a selected grid column', async () => {
    const focusedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        {
          name: 'username',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'users',
          column: 'username',
        },
      ],
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: '',
          strategy: 'sequential',
          delimiter: ',',
          first_row_header: true,
          inline_data: 'username,password\nbob,bpass\nalice,apass\ncarol,cpass',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={focusedScript} initialFocusedDataSourceName="users" />)
    })

    const ascendingSort = container.querySelector('button[aria-label="Sort username ascending"]')
    expect(ascendingSort).not.toBeNull()

    await act(async () => {
      ascendingSort?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-grid thead th[aria-sort="ascending"]')).not.toBeNull()
    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual([
      'alice',
      'apass',
      'bob',
      'bpass',
      'carol',
      'cpass',
    ])

    const descendingSort = container.querySelector('button[aria-label="Sort username descending"]')
    expect(descendingSort).not.toBeNull()

    await act(async () => {
      descendingSort?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-grid thead th[aria-sort="descending"]')).not.toBeNull()
    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual([
      'carol',
      'cpass',
      'bob',
      'bpass',
      'alice',
      'apass',
    ])
  })

  it('filters focused data source rows by search text and selected column', async () => {
    const focusedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        {
          name: 'username',
          type: 'file',
          update: 'each_iteration',
          selection: 'sequential',
          on_exhaustion: 'wrap',
          data_source: 'users',
          column: 'username',
        },
      ],
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: '',
          strategy: 'sequential',
          delimiter: ',',
          first_row_header: true,
          inline_data: 'username,password\nbob,bpass\nalice,apass\ncarol,cpass',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={focusedScript} initialFocusedDataSourceName="users" />)
    })

    const searchInput = container.querySelector('input[aria-label="Search rows for users"]') as HTMLInputElement | null
    const columnSelect = container.querySelector('select[aria-label="Filter column for users"]') as HTMLSelectElement | null
    expect(searchInput).not.toBeNull()
    expect(columnSelect).not.toBeNull()
    if (!searchInput || !columnSelect) {
      return
    }
    const inputValueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    const selectValueSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set

    await act(async () => {
      inputValueSetter?.call(searchInput, 'bob')
      searchInput.dispatchEvent(new Event('input', { bubbles: true }))
      searchInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('1/3 rows')
    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual([
      'bob',
      'bpass',
    ])
    expect(container.querySelector('.parameter-grid-index')?.textContent?.trim()).toBe('1')
    expect(Array.from(columnSelect.options).map(option => option.textContent)).toEqual(['All columns', 'username', 'password'])

    await act(async () => {
      selectValueSetter?.call(columnSelect, '0')
      columnSelect.dispatchEvent(new Event('input', { bubbles: true }))
      columnSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual([
      'bob',
      'bpass',
    ])

    await act(async () => {
      inputValueSetter?.call(searchInput, 'alice')
      searchInput.dispatchEvent(new Event('input', { bubbles: true }))
      searchInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual([
      'alice',
      'apass',
    ])
    expect(container.querySelector('.parameter-grid-index')?.textContent?.trim()).toBe('2')
  })

  it('includes the active project id in draft replay payloads', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')?.className).toContain('open')
    expect(apiMock.startDraftScriptDebugSession).not.toHaveBeenCalled()

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalledWith(expect.objectContaining({
      script: expect.objectContaining({ project_id: 'project-1' }),
    }))
  })

  it('keeps the debugger shell scoped to the Debug workspace after a session exists', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')).toBeNull()
    expect(container.querySelector('.editor-outline-list')?.textContent).not.toContain('Debug Session')

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) {
      return
    }

    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const debuggerShell = container.querySelector('[aria-label="Script Debugger"]')
    expect(debuggerShell).not.toBeNull()
    expect(debuggerShell?.className).toContain('open')
    expect(debuggerShell?.querySelector('[aria-label="Debugger controls"]')).toBeNull()
    expect(container.querySelectorAll('button[title="Run Replay"]')).toHaveLength(1)
    expect(container.querySelectorAll('button[title="Step to next action"]')).toHaveLength(1)
    expect(container.querySelectorAll('button[title="Stop debugger"]')).toHaveLength(1)
    expect(container.querySelectorAll('button[title="Session overrides"]')).toHaveLength(1)

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('.script-ide-tabs button[data-debug-status="Passed"]')).not.toBeNull()

    const authorTab = findWorkspaceTab(container, 'Author')
    expect(authorTab).toBeDefined()
    if (!authorTab) {
      return
    }

    await act(async () => {
      authorTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')).toBeNull()
    expect(container.querySelector('[aria-label="Authoring workspace"]')).not.toBeNull()
  })

  it('runs from Author by switching into the Debug workspace', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')).toBeNull()
    expect(apiMock.startDraftScriptDebugSession).not.toHaveBeenCalled()

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[aria-label="Script Debugger"]')?.className).toContain('open')
    expect(container.querySelector('[aria-label="Authoring workspace"]')).toBeNull()
  })

  it('starts paused and enables stepping when step-through mode is selected', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      success: true,
      events: [],
    }, {
      status: 'paused',
      current_event_index: -1,
      current_action: {
        phase: 'action',
        action_index: 0,
        event_index: 0,
        action_name: 'Get Products',
        action_type: 'GET',
        method: 'GET',
        target: '/api/products',
      },
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const stepThroughLabel = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Step through'))
    const stepThroughCheckbox = stepThroughLabel?.querySelector('input[type="checkbox"]') as HTMLInputElement | null
    expect(stepThroughCheckbox).not.toBeNull()
    if (!stepThroughCheckbox) {
      return
    }

    await act(async () => {
      stepThroughCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const startStepThroughButton = findDebuggerRunButton(container, 'Start Step Through')
    expect(startStepThroughButton).toBeDefined()
    if (!startStepThroughButton) {
      return
    }

    await act(async () => {
      startStepThroughButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalledWith(expect.objectContaining({
      start_paused: true,
      breakpoints: [],
      script: expect.objectContaining({ project_id: 'project-1' }),
    }))
    expect(container.querySelector('.debugger-center')).not.toBeNull()
    expect(container.querySelector('.debugger-center')?.textContent).toContain('Get Products')

    const stepButton = findButtonByTitle(container, 'Step to next action') as HTMLButtonElement | undefined
    expect(stepButton).toBeDefined()
    expect(stepButton?.disabled).toBe(false)
    if (!stepButton) {
      return
    }

    await act(async () => {
      stepButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.stepScriptDebugSession).toHaveBeenCalledWith('debug-session-1')
  })

  it('sets debugger breakpoints from the popup and action list', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const debuggerBreakpointButton = container.querySelector('.script-ide-debugger.open .debugger-gutter[title="Add breakpoint"]') as HTMLButtonElement | null
    expect(debuggerBreakpointButton).not.toBeNull()
    if (!debuggerBreakpointButton) {
      return
    }

    await act(async () => {
      debuggerBreakpointButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const authorTab = findWorkspaceTab(container, 'Author')
    expect(authorTab).toBeDefined()
    if (!authorTab) {
      return
    }

    await act(async () => {
      authorTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Remove breakpoint on action 1"]')).not.toBeNull()

    const removeBreakpointButton = container.querySelector('[aria-label="Remove breakpoint on action 1"]') as HTMLButtonElement | null
    expect(removeBreakpointButton).not.toBeNull()
    if (!removeBreakpointButton) {
      return
    }

    await act(async () => {
      removeBreakpointButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const setBreakpointButton = container.querySelector('[aria-label="Set breakpoint on action 1"]') as HTMLButtonElement | null
    expect(setBreakpointButton).not.toBeNull()
    if (!setBreakpointButton) {
      return
    }

    await act(async () => {
      setBreakpointButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalledWith(expect.objectContaining({
      breakpoints: [{ phase: 'action', action_index: 0 }],
    }))
  })

  it('resizes debugger panels with accessible splitters and resets the layout', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) {
      return
    }

    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Resize variables panel"]')).toBeNull()
    const showDebugSidebar = findButtonByTitle(container, 'Show right sidebar')
    expect(showDebugSidebar).toBeDefined()
    await act(async () => {
      showDebugSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const actionsResizer = container.querySelector('[aria-label="Resize actions panel"]') as HTMLDivElement | null
    let variablesResizer = container.querySelector('[aria-label="Resize variables panel"]') as HTMLDivElement | null
    const bottomResizer = container.querySelector('[aria-label="Resize bottom panel"]') as HTMLDivElement | null
    expect(actionsResizer).not.toBeNull()
    expect(variablesResizer).not.toBeNull()
    expect(bottomResizer).not.toBeNull()
    if (!actionsResizer || !variablesResizer || !bottomResizer) {
      return
    }
    const openVariablesResizer = variablesResizer

    expect(actionsResizer.getAttribute('role')).toBe('separator')
    expect(actionsResizer.getAttribute('aria-orientation')).toBe('vertical')
    expect(actionsResizer.getAttribute('aria-valuenow')).toBe('260')
    expect(openVariablesResizer.getAttribute('aria-valuenow')).toBe('320')
    expect(bottomResizer.getAttribute('aria-orientation')).toBe('horizontal')
    expect(bottomResizer.getAttribute('aria-valuenow')).toBe('220')

    await act(async () => {
      actionsResizer.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
      openVariablesResizer.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
      bottomResizer.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
      await Promise.resolve()
    })

    expect(actionsResizer.getAttribute('aria-valuenow')).toBe('284')
    expect(openVariablesResizer.getAttribute('aria-valuenow')).toBe('344')
    expect(bottomResizer.getAttribute('aria-valuenow')).toBe('244')

    const resetLayoutButton = findButtonByTitle(container, 'Reset debugger layout')
    expect(resetLayoutButton).toBeDefined()
    if (!resetLayoutButton) {
      return
    }

    await act(async () => {
      resetLayoutButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(actionsResizer.getAttribute('aria-valuenow')).toBe('260')
    expect(bottomResizer.getAttribute('aria-valuenow')).toBe('220')
    expect(container.querySelector('[aria-label="Resize variables panel"]')).toBeNull()

    const restoredDebugSidebar = findButtonByTitle(container, 'Show right sidebar')
    expect(restoredDebugSidebar).toBeDefined()
    await act(async () => {
      restoredDebugSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    variablesResizer = container.querySelector('[aria-label="Resize variables panel"]') as HTMLDivElement | null
    expect(variablesResizer?.getAttribute('aria-valuenow')).toBe('320')
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
    expect(container.textContent).toContain('Script validation passed with warnings.')
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

  it('validates a draft script before save', async () => {
    apiMock.validateDraftScript.mockResolvedValueOnce({
      script_id: '',
      valid: true,
      issues: [],
      warnings: ['Draft browser config will use Chromium defaults.'],
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Validate Draft',
    )
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.validateDraftScript).toHaveBeenCalledWith(expect.objectContaining({
      name: initialScript.name,
      protocol: initialScript.protocol,
    }), 'project-1', undefined)
    expect(container.textContent).toContain('Script validation passed with warnings.')
    expect(container.textContent).toContain('Draft browser config will use Chromium defaults.')
  })

  it('surfaces validation data-source fit and AI guardrail evidence', async () => {
    apiMock.validateDraftScript.mockResolvedValueOnce({
      script_id: '',
      valid: true,
      issues: [],
      warnings: [],
      data_source_fit: {
        status: 'blocked',
        status_counts: { ready: 0, needs_review: 0, blocked: 1 },
        items: [{
          data_source: 'users',
          parameter: 'login_email',
          column: 'email',
          status: 'blocked',
          required_rows: 10,
          available_rows: 3,
          summary: 'Unique data usage requires more rows than the data source provides.',
        }],
      },
      ai_guardrails: {
        status: 'blocked',
        status_counts: { ready: 2, needs_review: 0, blocked: 2 },
        items: [
          { key: 'provenance', status: 'ready', summary: 'AI source provenance is attached.' },
          { key: 'replay_before_save', status: 'blocked', summary: 'AI-generated drafts require replay evidence before promotion.' },
        ],
      },
      authoring_health: {
        score: 58,
        status: 'blocked',
        inputs: [],
      },
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Validate Draft',
    )
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Authoring Health')
    expect(container.textContent).toContain('Blocked · 58')
    expect(container.textContent).toContain('Data-Source Fit')
    expect(container.textContent).toContain('Unique data usage requires more rows than the data source provides.')
    expect(container.textContent).toContain('Source users · Parameter login_email · Column email · 3/10 rows')
    expect(container.textContent).toContain('AI Draft Guardrails')
    expect(container.textContent).toContain('AI-generated drafts require replay evidence before promotion.')
  })

  it('uses backend validation preflight for edit-page debug readiness', async () => {
    apiMock.validateDraftScript.mockResolvedValueOnce({
      script_id: '',
      valid: true,
      issues: [],
      warnings: [],
      preflight: {
        valid: false,
        action_count: 1,
        expected_action_count: 1,
        data_source_count: 0,
        parameter_count: 0,
        missing_variables: ['backend_only_account'],
        checks: [
          { id: 'variables', label: 'Variable readiness', status: 'fail', detail: 'backend_only_account' },
        ],
      },
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Validate Draft',
    )
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const readinessCard = Array.from(container.querySelectorAll('.utility-card'))
      .find(card => card.textContent?.includes('Debug Readiness'))
    expect(readinessCard?.textContent).toContain('Variable readiness')
    expect(readinessCard?.textContent).toContain('Missing variable: backend_only_account')
  })

  it('refreshes imported runtime parity metadata from validation responses', async () => {
    const importedScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'checkout.usr',
          preserved: {
            runtime_settings_migration_readiness: {
              score_pct: 72,
              blocked: true,
              summary: 'Migration readiness score 72%. Blocking runtime settings matched the selected block statuses: partial.',
              blocking_statuses: ['partial'],
              blocking_settings: [
                {
                  id: 'logging',
                  area: 'Logging',
                  concept: 'Log level, error-only, and extended logging options',
                  status: 'partial',
                  runtime_files: ['default.cfg'],
                },
              ],
            },
            runtime_settings_parity: [
              {
                id: 'logging',
                area: 'Logging',
                concept: 'Log level, error-only, and extended logging options',
                status: 'partial',
                runtime_files: ['default.cfg'],
              },
            ],
            runtime_settings_status_counts: {
              partial: 1,
            },
          },
        },
      },
    }

    apiMock.validateDraftScript.mockResolvedValueOnce({
      script_id: '',
      valid: true,
      issues: [],
      warnings: [],
      runtime_settings_parity: [
        {
          id: 'run_logic_iterations',
          area: 'Run Logic',
          concept: 'Iterations',
          status: 'executed',
          runtime_files: ['default.cfg'],
        },
        {
          id: 'logging',
          area: 'Logging',
          concept: 'Log level, error-only, and extended logging options',
          status: 'partial',
          runtime_files: ['default.cfg'],
        },
      ],
      runtime_settings_status_counts: {
        executed: 1,
        partial: 1,
      },
      migration_readiness: {
        score_pct: 80,
        blocked: false,
        summary: 'Migration readiness score 80%. No imported runtime settings matched the selected block statuses: blocked.',
        blocking_statuses: [],
        blocking_settings: [],
        policy: {
          block_statuses: ['blocked'],
        },
      },
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} projectId="project-1" />)
    })

    expect(container.textContent).toContain('72%')
    expect(container.textContent).toContain('1 Partial')

    const validateButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Validate Draft',
    )
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('80%')
    expect(container.textContent).toContain('1 Executed, 1 Partial')
    expect(container.textContent).toContain('No imported runtime settings matched the selected block statuses: blocked.')
  })

  it('passes the stored script response through the save callback', async () => {
    const onSave = vi.fn()
    apiMock.createScript.mockResolvedValueOnce({
      id: 'script-saved-1',
      name: initialScript.name,
      protocol: initialScript.protocol,
      version: initialScript.version,
      created_at: '2026-05-17T00:00:00Z',
      script: initialScript,
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} onSave={onSave} />)
    })

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

    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      id: 'script-saved-1',
      name: initialScript.name,
    }))
    expect(container.textContent).toContain('Script saved successfully!')
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
    expect(container.textContent).toContain('Global Iterations')
    expect(container.textContent).toContain('Max Transactions')
    expect(container.textContent).toContain('Workload Model')
    expect(container.textContent).toContain('Open arrival-rate')
    expect(container.textContent).toContain('Parameter substitution')
    expect(container.querySelector('.logging-settings-layout')).toBeTruthy()
    expect(container.querySelectorAll('.logging-checkbox-group .checkbox-label')).toHaveLength(4)

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
    expect(container.textContent).toContain('Network Emulation')
    expect(container.textContent).toContain('Jitter (ms)')
    expect(container.textContent).toContain('Packet Loss (%)')
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

    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalledWith(expect.objectContaining({
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

  it('runs Browser Replay with per-action screenshots and updates the cockpit from timeline selection', async () => {
    const replayReport = {
      group: 'Checkout Flow-debug',
      protocol: 'browser',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:02Z',
      success: true,
      runtime_profile: {
        browser_engine: 'chromium',
        device_profile: 'desktop_1080p',
        capture_mode: 'per_action',
        headless: true,
      },
      events: [
        {
          phase: 'action',
          action_index: 0,
          action_name: 'Open Home',
          action_type: 'browser_navigate',
          target: '/',
          success: true,
          started_at: '2026-05-14T20:00:00Z',
          completed_at: '2026-05-14T20:00:01Z',
          duration_ms: 76,
          page_url: 'https://shop.example.com/',
          page_title: 'Home',
          screenshot_base64: btoa('home-png'),
          dom_summary: { title: 'Home', ready_state: 'complete', visible_text: ['Home'] },
        },
        {
          phase: 'action',
          action_index: 1,
          action_name: 'Submit Search',
          action_type: 'browser_click',
          target: '#old-submit',
          selector: '#old-submit',
          success: true,
          started_at: '2026-05-14T20:00:01Z',
          completed_at: '2026-05-14T20:00:02Z',
          duration_ms: 91,
          page_url: 'https://shop.example.com/search?q=boots',
          page_title: 'Search Results',
          screenshot_base64: btoa('search-png'),
          console_entries: [{ level: 'info', text: 'Search completed' }],
          network_entries: [{ method: 'GET', url: 'https://shop.example.com/api/search?q=boots', status_code: 200, duration_ms: 18 }],
          dom_summary: { title: 'Search Results', ready_state: 'complete', visible_text: ['Boots'] },
        },
      ],
    }
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse(replayReport))

    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        base_url: 'https://shop.example.com',
        browser: {
          engine: 'chromium',
          headless: true,
          capture_screenshots: 'on_error',
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
        {
          name: 'Submit Search',
          type: 'browser_click',
          target: '#old-submit',
          selector: '#old-submit',
          value: '',
          capture: { screenshot: false, full_page: false, html: false },
          think_time_after: true,
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalledWith(expect.objectContaining({
      browser: expect.objectContaining({
        capture_screenshots: 'per_action',
      }),
      script: expect.objectContaining({
        config: expect.objectContaining({
          browser: expect.objectContaining({
            capture_screenshots: 'on_error',
          }),
        }),
      }),
    }))
    expect(findCenterTab(container, 'Browser Replay')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.browser-debugger-main')).not.toBeNull()
    expect(container.querySelector('.browser-address')?.textContent).toContain('Search Results')

    const firstTimelineRow = Array.from(container.querySelectorAll('.browser-debugger-timeline-row'))
      .find(row => row.textContent?.includes('Open Home')) as HTMLButtonElement | undefined
    expect(firstTimelineRow).toBeDefined()
    if (!firstTimelineRow) {
      return
    }

    await act(async () => {
      firstTimelineRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('.browser-debugger-timeline-row.active')?.textContent).toContain('Open Home')
    expect(container.querySelector('.browser-address')?.textContent).toContain('Home')
  })

  it('applies suggested browser selectors through editor history', async () => {
    const replayReport = {
      group: 'Checkout Flow-debug',
      protocol: 'browser',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:02Z',
      success: false,
      events: [
        {
          phase: 'action',
          action_index: 0,
          action_name: 'Open Home',
          action_type: 'browser_navigate',
          target: '/',
          success: true,
          started_at: '2026-05-14T20:00:00Z',
          completed_at: '2026-05-14T20:00:01Z',
          duration_ms: 76,
          page_url: 'https://shop.example.com/',
          page_title: 'Home',
          screenshot_base64: btoa('home-png'),
        },
        {
          phase: 'action',
          action_index: 1,
          action_name: 'Submit Search',
          action_type: 'browser_click',
          target: '#old-submit',
          selector: '#old-submit',
          success: false,
          error_message: 'button not found',
          started_at: '2026-05-14T20:00:01Z',
          completed_at: '2026-05-14T20:00:02Z',
          duration_ms: 91,
          page_url: 'https://shop.example.com/search',
          page_title: 'Search',
          screenshot_base64: btoa('search-png'),
          diagnosis: {
            category: 'selector',
            severity: 'error',
            cause: 'Selector did not match an element.',
            evidence: 'The DOM exposes a stable test id.',
            action: 'Use the suggested locator.',
            suggested_locator: '[data-testid="search-submit"]',
          },
        },
      ],
    }
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse(replayReport))

    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        base_url: 'https://shop.example.com',
        browser: {
          engine: 'chromium',
          headless: true,
          capture_screenshots: 'on_error',
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
        {
          name: 'Submit Search',
          type: 'browser_click',
          target: '#old-submit',
          selector: '#old-submit',
          value: '',
          capture: { screenshot: false, full_page: false, html: false },
          think_time_after: true,
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const applyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Use suggested selector'))
    expect(applyButton).toBeDefined()
    if (!applyButton) {
      return
    }

    await act(async () => {
      applyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Updated selector for Submit Search.')
    const scriptJsonTab = findCenterTab(container, 'Script JSON')
    expect(scriptJsonTab).toBeDefined()
    if (!scriptJsonTab) {
      return
    }

    await act(async () => {
      scriptJsonTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const activeJsonEditor = () => container.querySelector('.center-tab-panel.active textarea') as HTMLTextAreaElement | null
    const parseActiveEditorScript = () => {
      const parsed = JSON.parse(activeJsonEditor()?.value || '{}') as { script?: ScriptDefinition } & ScriptDefinition
      return parsed.script || parsed
    }
    const repairedScript = parseActiveEditorScript()
    expect(repairedScript.actions[1].selector).toBe('[data-testid="search-submit"]')

    const authorTab = findWorkspaceTab(container, 'Author')
    expect(authorTab).toBeDefined()
    if (!authorTab) {
      return
    }

    await act(async () => {
      authorTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('1 undo')
    const undoButton = findButtonByText(container, 'Undo')
    expect(undoButton).toBeDefined()
    if (!undoButton) {
      return
    }

    await act(async () => {
      undoButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const restoredScript = parseActiveEditorScript()
    expect(restoredScript.actions[1].selector).toBe('#old-submit')
  })

  it('renders browser_select actions with selector and value fields', async () => {
    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        base_url: 'https://shop.example.com',
        browser: {
          engine: 'chromium',
          headless: true,
        },
      },
      actions: [
        {
          name: 'Choose Country',
          type: 'browser_select',
          target: '#country',
          selector: '#country',
          value: 'United States',
          capture: { screenshot: false, full_page: false, html: false },
          think_time_after: true,
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const actionItem = container.querySelector('.action-item') as HTMLDivElement | null
    expect(actionItem).not.toBeNull()
    if (!actionItem) {
      return
    }

    await act(async () => {
      actionItem.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('browser_select')
    expect(container.textContent).toContain('Selector Override')
    expect(container.textContent).toContain('Value')
  })

  it('renders replay start warnings returned by the browser replay API', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
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

    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
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

  it('renders replay preflight, pinned failure diagnosis, and variable timeline', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'diagnostic-replay-1',
      debug_session_id: 'diagnostic-replay-1',
      preflight: {
        valid: false,
        action_count: 1,
        expected_action_count: 1,
        data_source_count: 0,
        parameter_count: 0,
        missing_variables: ['account_id'],
        checks: [
          { id: 'variables', label: 'Variable readiness', status: 'fail', detail: 'account_id' },
        ],
      },
      session: {
        id: 'diagnostic-replay-1',
        mode: 'replay',
        group: 'Checkout Flow-debug',
        protocol: 'http',
        log_level: 'debug',
        status: 'failed',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        current_event_index: 0,
        preflight: {
          valid: false,
          action_count: 1,
          expected_action_count: 1,
          data_source_count: 0,
          parameter_count: 0,
          missing_variables: ['account_id'],
          checks: [
            { id: 'variables', label: 'Variable readiness', status: 'fail', detail: 'account_id' },
          ],
        },
        report: {
          group: 'Checkout Flow-debug',
          protocol: 'http',
          started_at: '2026-05-14T20:00:00Z',
          completed_at: '2026-05-14T20:00:01Z',
          success: false,
          error: 'forbidden session expired',
          final_variables: { csrf: 'token-1' },
          diagnosis_summary: {
            failure_count: 1,
            categories: { auth_session: 1 },
            first_failure_action: 'Get Account',
            first_failure_cause: 'forbidden session expired',
          },
          artifact_manifest: {
            replay_id: 'diagnostic-replay-1',
            script_hash: 'abc123',
            network_entries: 0,
            console_entries: 0,
            screenshots: 0,
            snapshots: 0,
          },
          events: [
            {
              event_id: 'diagnostic-replay-1:action:000:000',
              phase: 'action',
              action_name: 'Get Account',
              action_type: 'GET',
              method: 'GET',
              target: '/accounts/{{var.account_id}}',
              status_code: 403,
              success: false,
              error_message: 'forbidden session expired',
              started_at: '2026-05-14T20:00:00Z',
              completed_at: '2026-05-14T20:00:01Z',
              duration_ms: 42,
              extractions: { csrf: 'token-1' },
              diagnosis: {
                category: 'auth_session',
                severity: 'error',
                cause: 'forbidden session expired',
                evidence: 'The target returned an authentication/session signal during replay.',
                action: 'Refresh login/session setup.',
              },
              repair_suggestions: [
                {
                  id: 'diagnostic-replay-1:repair-session',
                  title: 'Refresh session setup',
                  description: 'Draft a setup change for login, cookies, or CSRF extraction.',
                  draft_patch_type: 'repair_auth_session',
                },
              ],
            },
          ],
        },
        events: [],
      },
    })

    const diagnosticScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          name: 'Get Account',
          type: 'GET',
          method: 'GET',
          target: '/accounts/{{var.account_id}}',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={diagnosticScript} />)
    })

    const readinessCard = Array.from(container.querySelectorAll('.utility-card'))
      .find(card => card.textContent?.includes('Debug Readiness'))
    expect(readinessCard?.textContent).toContain('Preflight')
    expect(readinessCard?.textContent).toContain('Missing variable: account_id')

    const settingsButton = findButtonByTitle(container, 'Session overrides')
    expect(settingsButton).toBeDefined()
    if (!settingsButton) {
      return
    }

    await act(async () => {
      settingsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const runtimeDocument = container.querySelector('[aria-label="Runtime Settings"]')
    expect(runtimeDocument?.textContent).not.toContain('Preflight')
    expect(runtimeDocument?.textContent).not.toContain('Missing variable: account_id')

    const replayButton = findDebuggerRunButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('First failing action')
    expect(container.textContent).toContain('Auth Session')
    expect(container.textContent).toContain('Refresh session setup')

    expect(container.textContent).toContain('Variable Timeline')
    expect(container.textContent).toContain('csrf')
  })

  it('exports the current browser replay session as JSON', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'browser-replay-1',
      session: {
        id: 'browser-replay-1',
        group: 'Checkout Flow-browser-replay',
        protocol: 'browser',
        status: 'running',
        started_at: '2026-05-14T20:00:00Z',
        events: [],
      },
    })

    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        browser: {
          engine: 'chromium',
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

    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const exportButton = findButtonByTitle(container, 'Download Debug JSON')
    expect(exportButton).toBeDefined()
    if (!exportButton) {
      return
    }

    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(downloadTextFileMock).toHaveBeenCalledTimes(1)
    const [content, filename] = downloadTextFileMock.mock.calls[0]
    expect(filename).toBe('checkout-flow-browser-replay-1-debug.json')
    const parsed = JSON.parse(content)
    expect(parsed.schema_version).toBe('apexload.script_debug_export.v1')
    expect(parsed.replay_session_id).toBe('browser-replay-1')
    expect(parsed.script).toMatchObject({
      name: 'Checkout Flow',
      protocol: 'browser',
    })
    expect(parsed.replay_options).toMatchObject({
      group_name: 'Checkout Flow-debug',
      ignore_think_time: true,
      start_paused: false,
      variables: {},
    })
    expect(parsed.session).toMatchObject({
      id: 'browser-replay-1',
      status: 'running',
    })
    expect(parsed.report).toMatchObject({
      group: 'Checkout Flow-debug',
      protocol: 'browser',
    })
    expect(container.textContent).toContain('Exported debugger session JSON.')
  })

  it('exports the current browser replay session as a ZIP bundle', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'browser-replay-1',
      session: {
        id: 'browser-replay-1',
        group: 'Checkout Flow-browser-replay',
        protocol: 'browser',
        status: 'completed',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:03Z',
        events: [
          {
            phase: 'action',
            action_name: 'Open Home',
            action_type: 'browser_navigate',
            target: '/',
            success: true,
            started_at: '2026-05-14T20:00:00Z',
            completed_at: '2026-05-14T20:00:01Z',
            duration_ms: 125.5,
            screenshot_base64: btoa('png-bytes'),
            html: '<html><body>Home</body></html>',
            console_entries: [{ level: 'info', text: 'Loaded home page', source: 'console' }],
            network_entries: [{ method: 'GET', status_code: 200, url: 'https://example.test/', duration_ms: 32.4 }],
            dom_summary: { title: 'Home', ready_state: 'complete', visible_text: ['Home'] },
            snapshot: {
              request_method: 'GET',
              request_url: 'https://example.test/',
              response_status: 200,
              response_headers: { 'content-type': 'text/html' },
              response_body: '<html><body>Home</body></html>',
            },
          },
        ],
      },
    })

    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        browser: {
          engine: 'chromium',
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
          capture: { screenshot: true, full_page: false, html: true },
          think_time_after: true,
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const exportButton = findButtonByTitle(container, 'Download Replay ZIP')
    expect(exportButton).toBeDefined()
    if (!exportButton) {
      return
    }

    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(downloadBlobMock).toHaveBeenCalledTimes(1)
    const [blob, filename] = downloadBlobMock.mock.calls[0]
    expect(blob).toBeInstanceOf(Blob)
    expect(filename).toBe('checkout-flow-browser-replay-1-browser-replay.zip')

    const entries = await readStoredZipEntries(blob)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/manifest.json')).toBe(true)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/replay-export.json')).toBe(true)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/session.json')).toBe(true)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/event.json')).toBe(true)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/request.txt')).toBe(true)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/response.txt')).toBe(true)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/page.html')).toBe(true)
    expect(entries.has('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/screenshot.png')).toBe(true)

    const manifest = JSON.parse(zipDecoder.decode(entries.get('checkout-flow-browser-replay-1-browser-replay/manifest.json')))
    expect(manifest.schema_version).toBe('apexload.browser_replay_bundle.v1')
    expect(manifest.event_count).toBe(1)

    const eventJson = JSON.parse(zipDecoder.decode(entries.get('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/event.json')))
    expect(eventJson.action_name).toBe('Open Home')
    expect(eventJson.snapshot.request_url).toBe('https://example.test/')

    expect(zipDecoder.decode(entries.get('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/console.txt'))).toContain('Loaded home page')
    expect(zipDecoder.decode(entries.get('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/network.txt'))).toContain('https://example.test/')
    expect(zipDecoder.decode(entries.get('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/page.html'))).toContain('Home')
    expect(zipDecoder.decode(entries.get('checkout-flow-browser-replay-1-browser-replay/events/01-open-home/screenshot.png'))).toBe('png-bytes')
    expect(container.textContent).toContain('Exported browser replay bundle ZIP.')
  })

  it('renders replay validation warnings returned on failed browser replay start', async () => {
    apiMock.startDraftScriptDebugSession.mockRejectedValueOnce(Object.assign(new Error('Replay validation failed'), {
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

    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
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

    expect(container.textContent).toContain('Streaming settings apply runtime pacing to media-like HTTP responses.')
    expect(container.textContent).not.toContain('Streaming settings are compatibility-only today')

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

  it('edits proxy routing from the Web HTTP/HTML RTS panel', async () => {
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

    expect(container.textContent).toContain('Proxy Routing')
    expect(container.textContent).toContain('Proxy Mode')
    expect(container.textContent).toContain('Bypass Hosts')

    const proxyModeSelect = Array.from(container.querySelectorAll('select')).find(select =>
      Array.from(select.options).some(option => option.value === 'system' && option.textContent?.includes('environment proxy')),
    ) as HTMLSelectElement | undefined
    const proxyUrlInput = Array.from(container.querySelectorAll('input')).find(input =>
      input.getAttribute('placeholder') === 'http://proxy.example.test:8080',
    ) as HTMLInputElement | undefined
    const bypassArea = Array.from(container.querySelectorAll('textarea')).find(area =>
      area.getAttribute('placeholder') === 'localhost\n*.internal.example.com',
    ) as HTMLTextAreaElement | undefined

    expect(proxyModeSelect?.value).toBe('custom')
    expect(proxyUrlInput?.value).toBe('http://proxy.example.test:8080')
    expect(bypassArea).toBeDefined()
    if (!proxyModeSelect || !proxyUrlInput || !bypassArea) {
      return
    }

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(proxyUrlInput, 'http://edge-proxy.example.test:8080')
      proxyUrlInput.dispatchEvent(new Event('input', { bubbles: true }))
      proxyUrlInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      valueSetter?.call(bypassArea, 'localhost\n*.internal.example.com')
      bypassArea.dispatchEvent(new Event('input', { bubbles: true }))
      bypassArea.dispatchEvent(new Event('change', { bubbles: true }))
    })

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
        http: expect.objectContaining({
          proxy_mode: 'custom',
          proxy: expect.objectContaining({
            url: 'http://edge-proxy.example.test:8080',
            no_proxy: ['localhost', '*.internal.example.com'],
          }),
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
    expect(detail).toBeNull()

    const firstAction = container.querySelector('.action-item')
    await act(async () => {
      firstAction?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.action-detail')?.parentElement).toBe(contentGrid)
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

    // Choose the File / Data branch of the Parameterize dialog to reach the inline-value editor.
    const fileChoice = Array.from(container.querySelectorAll('.parameterize-dialog .parameter-choice-option')).find(button =>
      button.textContent?.includes('File / Data'),
    )
    expect(fileChoice).toBeDefined()
    await act(async () => {
      fileChoice?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
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

  it('parameterizes a selected string value from the Script JSON context menu', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    expect(editorWrapper).not.toBeNull()
    expect(textarea).not.toBeNull()
    if (!editorWrapper || !textarea) {
      return
    }

    const start = textarea.value.indexOf('/login')
    const end = start + '/login'.length
    expect(start).toBeGreaterThan(-1)
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      editorWrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 60 }))
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

    // Choose the File / Data branch of the Parameterize dialog to reach the inline-value editor.
    const fileChoice = Array.from(container.querySelectorAll('.parameterize-dialog .parameter-choice-option')).find(button =>
      button.textContent?.includes('File / Data'),
    )
    expect(fileChoice).toBeDefined()
    await act(async () => {
      fileChoice?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const draftPanel = container.querySelector('.parameter-draft-panel')
    expect(draftPanel).not.toBeNull()
    expect(draftPanel?.textContent).toContain('Script JSON')

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

    // The new parameter is created and its row is focused for type selection.
    expect(container.textContent).toContain('login')

    // Switching back to the Script JSON tab confirms the value became a template reference.
    const jsonTab = findCenterTab(container, 'Script JSON')
    expect(jsonTab).toBeDefined()
    await act(async () => {
      jsonTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const refreshedTextarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    expect(refreshedTextarea?.value).toContain('{{param.login}}')
  })

  it('creates a built-in random_number parameter from the Script JSON context menu', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    expect(editorWrapper).not.toBeNull()
    expect(textarea).not.toBeNull()
    if (!editorWrapper || !textarea) {
      return
    }

    const start = textarea.value.indexOf('/login')
    const end = start + '/login'.length
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      editorWrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 60 }))
    })

    const parameterizeButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Parameterize selection'),
    )
    expect(parameterizeButton).toBeDefined()
    await act(async () => {
      parameterizeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    // Take the Built-in Function branch and pick the Random Number type.
    const builtinChoice = Array.from(container.querySelectorAll('.parameterize-dialog .parameter-choice-option')).find(button =>
      button.textContent?.includes('Built-in Function'),
    )
    expect(builtinChoice).toBeDefined()
    await act(async () => {
      builtinChoice?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const randomTypeOption = Array.from(container.querySelectorAll('.parameterize-type-grid .parameter-choice-option')).find(button =>
      button.textContent?.includes('random_number'),
    )
    expect(randomTypeOption).toBeDefined()
    await act(async () => {
      randomTypeOption?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    // Configure the min/max key fields inline.
    const minInput = container.querySelector('.parameterize-type-fields input') as HTMLInputElement | null
    expect(minInput).not.toBeNull()
    const numberInputs = Array.from(container.querySelectorAll('.parameterize-type-fields input')) as HTMLInputElement[]
    await act(async () => {
      changeInputValue(numberInputs[0], '5')
      changeInputValue(numberInputs[1], '50')
    })

    const createButton = Array.from(container.querySelectorAll('.parameterize-dialog button')).find(button =>
      button.textContent?.includes('Create Parameter'),
    )
    expect(createButton).toBeDefined()
    await act(async () => {
      createButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    // The dialog closes and the new parameter is shown in the Parameter Data tab.
    expect(container.querySelector('.parameterize-dialog')).toBeNull()
    expect(container.textContent).toContain('Random Number')

    // Switching back to Script JSON confirms the token and the typed parameter definition.
    const jsonTab = findCenterTab(container, 'Script JSON')
    expect(jsonTab).toBeDefined()
    await act(async () => {
      jsonTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const refreshedTextarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    expect(refreshedTextarea?.value).toContain('{{param.login}}')
    expect(refreshedTextarea?.value).toContain('random_number')
    expect(refreshedTextarea?.value).toMatch(/"min":\s*5/)
    expect(refreshedTextarea?.value).toMatch(/"max":\s*50/)
  })

  it('creates a built-in current_iteration parameter from the action body context menu', async () => {
    const scriptWithEditableBody: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          type: 'POST',
          body: '{"n":"seed"}',
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

    const start = textarea.value.indexOf('seed')
    const end = start + 'seed'.length
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      textarea.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 120, clientY: 80 }))
    })

    const parameterizeButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Parameterize selection'),
    )
    expect(parameterizeButton).toBeDefined()
    await act(async () => {
      parameterizeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const builtinChoice = Array.from(container.querySelectorAll('.parameterize-dialog .parameter-choice-option')).find(button =>
      button.textContent?.includes('Built-in Function'),
    )
    expect(builtinChoice).toBeDefined()
    await act(async () => {
      builtinChoice?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const iterationTypeOption = Array.from(container.querySelectorAll('.parameterize-type-grid .parameter-choice-option')).find(button =>
      button.textContent?.includes('current_iteration'),
    )
    expect(iterationTypeOption).toBeDefined()
    await act(async () => {
      iterationTypeOption?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const createButton = Array.from(container.querySelectorAll('.parameterize-dialog button')).find(button =>
      button.textContent?.includes('Create Parameter'),
    )
    expect(createButton).toBeDefined()
    await act(async () => {
      createButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.parameterize-dialog')).toBeNull()
    expect(textarea.value).toContain('{{param.seed}}')
    expect(container.textContent).toContain('Current Iteration')
  })

  it('offers Edit parameter when selecting an existing token in the Script JSON', async () => {
    const scriptWithToken: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          body: '{{param.session_id}}',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={scriptWithToken} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    if (!editorWrapper || !textarea) {
      expect(editorWrapper).not.toBeNull()
      return
    }

    const token = '{{param.session_id}}'
    const start = textarea.value.indexOf(token)
    const end = start + token.length
    expect(start).toBeGreaterThan(-1)
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      editorWrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 60 }))
    })

    const editButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Edit parameter'),
    )
    expect(editButton).toBeDefined()
  })

  it('does not offer parameterize when selecting a JSON property key', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    if (!editorWrapper || !textarea) {
      expect(editorWrapper).not.toBeNull()
      return
    }

    const start = textarea.value.indexOf('target')
    const end = start + 'target'.length
    expect(start).toBeGreaterThan(-1)
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      editorWrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 60 }))
    })

    const parameterizeButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Parameterize selection'),
    )
    expect(parameterizeButton).toBeUndefined()
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

  it('renders native protocol editors and runtime settings without HTTP fallback panels', async () => {
    const databaseScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'database',
      config: {
        ...initialScript.config,
        database: {
          driver: 'sqlite',
          dsn: ':memory:',
          query_timeout_ms: 30000,
          max_rows: 25,
          read_only: true,
          rollback_on_teardown: true,
        },
      },
      actions: [{
        name: 'Lookup_User',
        type: 'db_query',
        target: 'select name from users where id = ?',
        params: { binds: ['{{param.user_id}}'] },
        headers: {},
        body: '',
        extractors: [{ name: 'user_name', source: 'body', type: 'jsonpath', expression: '$.rows[0].name' }],
        assertions: [{ type: 'row_count', expected: 1 }, { type: 'scalar_equals', expected: 'Ada' }],
      }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={databaseScript} initialFocusedActionName="Lookup_User" />)
    })

    expect(container.textContent).toContain('Database scripts model secret-backed DSNs')
    expect(container.textContent).toContain('SQL And Binds')
    expect(container.textContent).toContain('Bind Parameters')
    expect(container.textContent).toContain('Scalar Equals')

    const runtimeToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeToggle).toBeDefined()
    if (!runtimeToggle) return

    await act(async () => {
      runtimeToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const databaseToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Database Runtime'))
    expect(databaseToggle).toBeDefined()
    if (!databaseToggle) return

    await act(async () => {
      databaseToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Query Timeout (ms)')
    expect(container.textContent).toContain('Read-only guard')
    expect(container.textContent).not.toContain('HTTP Transport')
  })

  it('renders network protocol controls and protocol-specific assertion choices', async () => {
    const networkScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'network',
      config: {
        ...initialScript.config,
        network: {
          subprotocol: 'tcp',
          host: 'localhost',
          port: 8080,
          timeout_ms: 30000,
          tls: false,
        },
      },
      actions: [{
        name: 'TCP_Check',
        type: 'tcp_request',
        target: 'localhost:8080',
        headers: {},
        body: 'ping',
        params: { read: true, read_bytes: 4096 },
        extractors: [{ name: 'tcp_response', source: 'response', type: 'jsonpath', expression: '$.response' }],
        assertions: [{ type: 'body_contains', value: 'pong' }],
      }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={networkScript} initialFocusedActionName="TCP_Check" />)
    })

    expect(container.textContent).toContain('Network scripts model DNS, TCP, UDP')
    expect(container.textContent).toContain('Protocol Controls')
    expect(container.textContent).toContain('Read response')
    expect(container.textContent).toContain('Transcript Contains')
    expect(container.textContent).not.toContain('HTTP Transport')
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

    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
        group: 'SOAP Replay-debug',
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
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={soapScript} />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalled()
    expect(container.textContent).toContain('SOAP Fault')
    expect(container.textContent).toContain('SOAP 1.2')
    expect(container.textContent).toContain('Code: env:Sender')
    expect(container.textContent).toContain('Reason: Request validation failed')
    expect(container.textContent).toContain('Variable Timeline')
    expect(container.textContent).toContain('Extractions')
    expect(container.textContent).toContain('Snapshot Request')
    expect(container.textContent).toContain('Snapshot Response')
    const copyFaultButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy fault'))
    const copyRequestButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy full request'))
    const copyResponseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy full response'))
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
    expect(container.textContent).toContain('Open full text')
    const highlightedBlocks = Array.from(container.querySelectorAll('.snapshot-code'))
    expect(highlightedBlocks.length).toBeGreaterThanOrEqual(4)
    expect(highlightedBlocks.some(block => block.textContent?.includes('"confirmationId": "CONF-42"'))).toBe(true)
    expect(highlightedBlocks.some(block => block.textContent?.includes('<CreateOrder />'))).toBe(true)
    const resolvedHeadersLabel = Array.from(container.querySelectorAll('.replay-code-block .replay-detail-label')).find(node => node.textContent === 'Resolved Headers')
    expect(resolvedHeadersLabel?.parentElement?.textContent).toContain('Copy')
    expect(container.querySelectorAll('.snapshot-code-token-tag').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('.snapshot-code-token-key').length).toBeGreaterThan(0)
    expect(container.textContent).toContain('env:Body')
    expect(container.textContent).toContain('env:Fault')
  })

  it('renders native protocol replay evidence blocks', async () => {
    const databaseScript: ScriptDefinition = {
      ...initialScript,
      name: 'Database Replay',
      protocol: 'database',
      config: {
        ...initialScript.config,
        database: {
          driver: 'sqlite',
          dsn: ':memory:',
          query_timeout_ms: 30000,
          max_rows: 10,
        },
      },
      actions: [{
        name: 'Lookup_User',
        type: 'db_query',
        target: 'select name from users',
        headers: {},
        body: '',
        extractors: [],
        assertions: [{ type: 'row_count', expected: 1 }],
      }],
    }

    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
      group: 'Database Replay-debug',
      protocol: 'database',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: false,
      error: 'row_count assertion failed',
      events: [{
        phase: 'action',
        action_name: 'Lookup_User',
        action_type: 'db_query',
        target: 'select name from users',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        duration_ms: 12,
        status_code: 500,
        success: false,
        error_message: 'row_count assertion failed',
        protocol_evidence: {
          protocol: 'database',
          operation: 'db_query',
          target: 'sqlite',
          status: 'failed',
          rows: {
            row_count: 0,
            rows: [],
          },
          metadata: {
            error: 'row_count assertion failed',
          },
        },
      }],
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={databaseScript} />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) return

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) return

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    if (!container.textContent?.includes('Protocol Evidence')) {
      const replayEventHeader = container.querySelector('.replay-event-header')
      await act(async () => {
        replayEventHeader?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }

    expect(container.textContent).toContain('Protocol Evidence')
    expect(container.textContent).toContain('database / db_query / failed / sqlite')
    expect(container.textContent).toContain('row_count')
    expect(container.textContent).toContain('row_count assertion failed')
  })

  it('renders browser replay DOM summary and fallback snapshot with highlighted JSON previews', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
        group: 'Checkout Flow-debug',
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
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startDraftScriptDebugSession).toHaveBeenCalled()
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
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
        group: 'Checkout Flow-debug',
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
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const replayButton = findOpenDebuggerButton(container)
    expect(replayButton).toBeDefined()
    if (!replayButton) {
      return
    }

    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Page URL')
    expect(container.textContent).toContain('https://shop.example.com/catalog')
    expect(container.textContent).toContain('Page Title')
    expect(container.textContent).toContain('Catalog')
    expect(container.textContent).not.toContain('Browser Artifacts')
    expect(container.querySelector('.replay-browser-grid')).toBeNull()
    expect(container.textContent).not.toContain('View full size')
  })

  it('renders the authoring utility sidebar for imported drafts', async () => {
    const importedAuthoringScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          name: ' Login Request ',
          body: '{"session":"{{param.session_id}}"}',
        },
        {
          ...initialScript.actions[0],
          name: 'Login Request',
          method: 'GET',
          type: 'GET',
          target: '/orders',
          body: '',
        },
      ],
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'checkout.usr',
          preserved: {
            correlation_suggestions: [
              {
                name: 'session_id',
                source: 'response',
                expression: '$.session.id',
                found_at: 'Step 1',
                used_at: ['Step 2'],
              },
            ],
            parameter_file_sections: {
              'Users.prm': {
                LoginUser: {
                  Table: 'users.csv',
                  ColumnName: 'username',
                },
              },
            },
          },
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedAuthoringScript} />)
    })

    expect(container.textContent).toContain('Outline')
    expect(container.textContent).toContain('Starter Blocks')
    expect(container.textContent).toContain('Convergence Checklist')
    expect(container.textContent).toContain('Guided History')
    expect(container.textContent).toContain('Flow Assembly')
    expect(container.textContent).toContain('Repair Actions')
    expect(container.textContent).toContain('Migration Assistant')
    expect(container.textContent).toContain('Draft Diff Review')
    expect(container.textContent).toContain('Correlation Inspector')
    expect(container.textContent).toContain('Parameter Source Inspector')
    expect(container.textContent).toContain('0 errors • 2 warnings')
  })

  it('applies migration assistant fixes and starter blocks from the utility sidebar', async () => {
    const importedAuthoringScript: ScriptDefinition = {
      ...initialScript,
      config: {
        ...initialScript.config,
        think_time: { type: 'fixed', mean_ms: 0, ignore: true },
        pacing: { type: 'none' },
        run_logic: { auto_transactions: 'none' },
      },
      actions: [
        {
          ...initialScript.actions[0],
          name: ' Login Request ',
        },
        {
          ...initialScript.actions[0],
          name: 'Login Request',
          target: '/profile',
          body: '',
        },
      ],
      metadata: {
        import: {
          source: 'loadrunner',
          source_script_name: 'checkout.usr',
          preserved: {},
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedAuthoringScript} />)
    })

    const normalizeButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Normalize Names'),
    )
    expect(normalizeButton).toBeDefined()
    if (!normalizeButton) {
      return
    }

    await act(async () => {
      normalizeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Normalized 2 imported action names')
    expect(container.textContent).toContain('Login_Request')
    expect(container.textContent).toContain('Login_Request_2')

    const starterButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('POST JSON'),
    )
    expect(starterButton).toBeDefined()
    if (!starterButton) {
      return
    }

    await act(async () => {
      starterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Added starter block: POST JSON.')
    expect(Array.from(container.querySelectorAll('.action-item')).length).toBe(3)
  })

  it('inserts reusable authoring components and supports guided undo', async () => {
    apiMock.listAuthoringComponents.mockResolvedValueOnce({
      components: [{
        id: 'http-core-api-request-group',
        kind: 'request_group',
        name: 'Core API Request Group',
        protocols: ['http'],
        summary: 'Reusable health and lookup transaction group.',
        actions: [{
          name: 'Health Check',
          type: 'GET',
          method: 'GET',
          target: '/health',
          headers: {},
          body: '',
          assertions: [{ type: 'status_code', expected: 200 }],
        }],
        data_sources: [{
          name: 'users',
          type: 'csv',
          path: '',
          strategy: 'unique',
          delimiter: ',',
          first_row_header: true,
          columns: ['username'],
        }],
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Core API Request Group')

    const insertButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.includes('Insert Block'),
    )
    expect(insertButton).toBeDefined()
    if (!insertButton) {
      return
    }

    await act(async () => {
      insertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Inserted Core API Request Group')
    expect(container.textContent).toContain('Health_Check')
    expect(Array.from(container.querySelectorAll('.action-item')).length).toBe(initialScript.actions.length + 1)

    const undoButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Undo',
    )
    expect(undoButton).toBeDefined()
    if (!undoButton) {
      return
    }

    await act(async () => {
      undoButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Undid the last guided authoring change.')
    expect(Array.from(container.querySelectorAll('.action-item')).length).toBe(initialScript.actions.length)
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
