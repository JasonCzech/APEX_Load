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

  it('keeps support panels behind dialog launchers so the action workspace remains primary', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(container.textContent).toContain('Script Configuration')
    expect(container.textContent).toContain('RTS & Runtime')
    expect(container.textContent).toContain('Parameter Data')
    expect(container.textContent).toContain('Replay Output')

    const configDialog = container.querySelector('[aria-label="Script Configuration"]')
    const replayDialog = container.querySelector('[aria-label="Replay Output"]')
    const runtimeDialog = container.querySelector('[aria-label="Runtime Settings"]')
    expect(configDialog?.className).not.toContain('open')
    expect(replayDialog?.className).not.toContain('open')
    expect(runtimeDialog?.className).not.toContain('open')

    const configLauncher = Array.from(container.querySelectorAll('.editor-support-button')).find(button => button.textContent?.includes('Script Configuration'))
    expect(configLauncher).toBeDefined()
    if (!configLauncher) return

    await act(async () => {
      configLauncher.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(configDialog?.className).toContain('open')

    const closeButton = configDialog?.querySelector('.panel-header-toggle')
    expect(closeButton).toBeTruthy()
    await act(async () => {
      closeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(configDialog?.className).not.toContain('open')
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
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} />)
    })

    expect(container.textContent).toContain('Imported Metadata')
  expect(container.textContent).toContain('Runtime Setting Parity')
  expect(container.textContent).toContain('1 Executed, 1 Partial')
    expect(container.textContent).toContain('Migration Readiness')
    expect(container.textContent).toContain('72%')
    expect(container.textContent).toContain('Blocking runtime settings matched the selected block statuses')
  expect(container.textContent).toContain('Executed: Run Logic')
  expect(container.textContent).toContain('Iterations')
  expect(container.textContent).toContain('default.cfg')
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
    expect(container.textContent).toContain('Hide Data')

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
    expect(container.textContent).toContain('Hide Data')
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
    expect(container.textContent).toContain('Named Parameters')
  })

  it('opens the requested data source when a draft opens with a data source focus hint', async () => {
    const focusedScript: ScriptDefinition = {
      ...initialScript,
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
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={focusedScript} initialFocusedDataSourceName="users" />)
    })

    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.textContent).toContain('Parameter Data (1)')
    expect(container.textContent).toContain('Hide Data')
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

  it('exports the current browser replay session as JSON', async () => {
    apiMock.startDraftBrowserReplaySession.mockResolvedValueOnce({
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

    const exportButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Download Replay JSON',
    )
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
    expect(filename).toBe('checkout-flow-browser-replay-1-browser-replay.json')
    const parsed = JSON.parse(content)
    expect(parsed.schema_version).toBe('stormrunner.browser_replay_export.v1')
    expect(parsed.replay_session_id).toBe('browser-replay-1')
    expect(parsed.script).toMatchObject({
      name: 'Checkout Flow',
      protocol: 'browser',
    })
    expect(parsed.replay_options).toMatchObject({
      group_name: 'Checkout Flow-replay',
      ignore_think_time: true,
      start_paused: false,
      variables: {},
    })
    expect(parsed.session).toMatchObject({
      id: 'browser-replay-1',
      status: 'running',
    })
    expect(parsed.report).toMatchObject({
      group: 'Checkout Flow-replay',
      protocol: 'browser',
    })
    expect(container.textContent).toContain('Exported browser replay session JSON.')
  })

  it('exports the current browser replay session as a ZIP bundle', async () => {
    apiMock.startDraftBrowserReplaySession.mockResolvedValueOnce({
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

    const exportButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Download Replay ZIP',
    )
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
    expect(manifest.schema_version).toBe('stormrunner.browser_replay_bundle.v1')
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
    expect(container.textContent).toContain('Migration Assistant')
    expect(container.textContent).toContain('Imported Draft Review')
    expect(container.textContent).toContain('Correlation Inspector')
    expect(container.textContent).toContain('Parameter & Variable Inspector')
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
