import ScriptsAuthoringSurface from './ScriptsAuthoringSurface'
import type { ScriptSaveOptions, StoredScript } from '../types/storedContent'
// @vitest-environment jsdom

import { act, Suspense, useState, type ComponentProps } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { ApiError } from '../hooks/apiClient'
import { formatSnapshotRequestSection, formatSnapshotResponseSection } from '../utils/snapshotFormatUtils'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { KeyboardShortcutProvider } from '../hooks/KeyboardShortcutRoot'
import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import { formatSOAPFaultSummary } from './soapFaultUtils'
import RealScriptEditor from './ScriptEditor'
import { authoringFixture, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'
import { RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE } from './runtimeVariableEvidence'

const { downloadTextFileMock } = vi.hoisted(() => ({
  downloadTextFileMock: vi.fn(),
}))

const { codeMirrorDispatchMock, codeMirrorScrollDoms } = vi.hoisted(() => ({
  codeMirrorDispatchMock: vi.fn(),
  codeMirrorScrollDoms: [] as HTMLElement[],
}))

const { useWebSocketMock } = vi.hoisted(() => ({
  useWebSocketMock: vi.fn(),
}))

const { replaySessionIdMock } = vi.hoisted(() => ({ replaySessionIdMock: vi.fn() }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// Existing rendering cases use an explicit paired baseline. Cases exercising
// loading/refusal pass initialAuthoring={null} to exercise real admission.
function ScriptEditor(props: ComponentProps<typeof RealScriptEditor>) {
  if (props.scriptId && props.initialScript && props.initialAuthoring === undefined) {
    const pair = authoringFixture(props.initialScript, props.scriptId, props.projectId || props.initialScript.project_id)
    return <RealScriptEditor {...props} initialScript={pair.projection.script} initialAuthoring={pair} />
  }
  return <RealScriptEditor {...props} />
}

const apiMock = {
  getScript: vi.fn(),
  getScriptAuthoring: vi.fn(),
  getScriptSecretStatus: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  getApplicationSecretStatuses: vi.fn(),
  getApplicationSecretUsage: vi.fn(),
  listApplicationSecrets: vi.fn(),
  getProjectSecretUsage: vi.fn(),
  listProjectSecrets: vi.fn(),
  createProjectSecrets: vi.fn(),
  rebindScriptSecretReference: vi.fn(),
  createScript: vi.fn(),
  updateScript: vi.fn(),
  downloadScript: vi.fn(),
  createAuthoringSession: vi.fn(),
  validateScript: vi.fn(),
  validateDraftScript: vi.fn(),
  replayDraftScript: vi.fn(),
  replayScript: vi.fn(),
  startDraftBrowserReplaySession: vi.fn(),
  startBrowserReplaySession: vi.fn(),
  startDraftScriptDebugSession: vi.fn(),
  startScriptDebugSession: vi.fn(),
  previewBrowserToHTTP: vi.fn(),
  getScriptRecordingStatus: vi.fn(),
  pauseBrowserReplaySession: vi.fn(),
  resumeBrowserReplaySession: vi.fn(),
  stopBrowserReplaySession: vi.fn(),
  pauseScriptDebugSession: vi.fn(),
  resumeScriptDebugSession: vi.fn(),
  stepScriptDebugSession: vi.fn(),
  stopScriptDebugSession: vi.fn(),
  getScriptDebugSession: vi.fn(),
  updateScriptDebugBreakpoints: vi.fn(),
  diagnoseScriptReplay: vi.fn(),
  diagnoseCorrelations: vi.fn(),
  listAuthoringComponents: vi.fn(),
  createAuthoringComponent: vi.fn(),
  deleteAuthoringComponent: vi.fn(),
}

// A compatible controller echoes the binding it validated: the draft
// fingerprint it was sent, or the stored revision it read (the most recent
// authoring baseline it served, else the wrapper's fixture baseline). Tests that exercise the binding
// itself return their own echo fields, which win.
const compatibleApi = new Proxy(apiMock, {
  get(target, key) {
    if (key === 'validateDraftScript') return compatibleValidateDraftScript
    if (key === 'validateScript') return compatibleValidateScript
    return target[key as keyof typeof target]
  },
})
async function compatibleValidateDraftScript(...args: unknown[]) {
  const response = await (apiMock.validateDraftScript as (...values: unknown[]) => unknown)(...args) as Record<string, unknown> | undefined
  const options = args[4] as { fingerprint?: string } | undefined
  return response && typeof response === 'object' && !('validated_fingerprint' in response) ? { ...response, validated_fingerprint: options?.fingerprint } : response
}
async function compatibleValidateScript(...args: unknown[]) {
  const response = await (apiMock.validateScript as (...values: unknown[]) => unknown)(...args) as Record<string, unknown> | undefined
  const served = apiMock.getScriptAuthoring.mock.results.filter(result => result.type === 'return')
  const baseline = served.length ? await (served[served.length - 1].value as Promise<{ projection?: { content_revision?: string } } | undefined>).catch(() => undefined) : undefined
  return response && typeof response === 'object' && !('validated_content_revision' in response) ? { ...response, validated_content_revision: baseline?.projection?.content_revision ?? authoringFixture().projection.content_revision } : response
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => compatibleApi,
}))

vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: useWebSocketMock,
}))

vi.mock('@uiw/react-codemirror', async () => {
  const { useEffect, useRef } = await import('react')
  const { apexDslSemanticHighlighting } = await import('./apexDslSemanticHighlighting')
  const flattenExtensions = (extensions: unknown): unknown[] => {
    if (!Array.isArray(extensions)) return [extensions]
    return extensions.flatMap(flattenExtensions)
  }
  const CodeMirrorMock = ({ value = '', onChange, onCreateEditor, extensions, basicSetup, editable = true, readOnly = false }: {
    value?: string
    onChange?: (value: string) => void
    onCreateEditor?: (view: unknown) => void
    extensions?: unknown[]
    basicSetup?: {
      lineNumbers?: boolean
      foldGutter?: boolean
      highlightActiveLine?: boolean
      highlightActiveLineGutter?: boolean
    } | boolean
    editable?: boolean
    readOnly?: boolean
  }) => {
    const ref = useRef<HTMLTextAreaElement | null>(null)
    const created = useRef(false)
    const hasApexDslSemanticHighlighting = flattenExtensions(extensions).includes(apexDslSemanticHighlighting())
    useEffect(() => {
      const el = ref.current
      if (!el) return
      const lineStarts = (text: string) => {
        const starts: number[] = []
        let offset = 0
        text.split('\n').forEach(line => {
          starts.push(offset)
          offset += line.length + 1
        })
        return starts.length > 0 ? starts : [0]
      }
      const lineLayout = (text: string) => {
        const starts = lineStarts(text)
        const lineTexts = text.split('\n')
        const heights = lineTexts.map(line => (line.includes('WRAPPED_CODEMIRROR_TEST_LINE') ? 90 : 18))
        const tops: number[] = []
        let top = 0
        heights.forEach(height => {
          tops.push(top)
          top += height
        })
        return { starts, heights, tops, totalHeight: Math.max(1, top) }
      }
      const layout = lineLayout(value)
      Object.defineProperty(el, 'clientHeight', { configurable: true, value: 240 })
      Object.defineProperty(el, 'scrollHeight', { configurable: true, value: Math.max(960, layout.totalHeight) })
      if (!codeMirrorScrollDoms.includes(el)) {
        codeMirrorScrollDoms.push(el)
      }
      if (created.current || !onCreateEditor) return
      created.current = true
      // Minimal EditorView stand-in: reports the textarea's live selection + value so the
      // production right-click handler (which reads view.state) can be exercised under test.
      onCreateEditor({
        dispatch: codeMirrorDispatchMock,
        requestMeasure: (spec: { read: () => unknown; write: (value: unknown) => void }) => spec.write(spec.read()),
        scrollDOM: el,
        get state() {
          const el = ref.current
          const from = el?.selectionStart ?? 0
          const to = el?.selectionEnd ?? 0
          const doc = el?.value ?? value
          const { starts } = lineLayout(doc)
          const lineAtOffset = (offset: number) => {
            let lineNo = 1
            for (let index = 0; index < starts.length; index += 1) {
              if (starts[index] <= offset) {
                lineNo = index + 1
              } else {
                break
              }
            }
            return { from: starts[lineNo - 1], number: lineNo }
          }
          return {
            selection: { main: { from, to } },
            doc: {
              toString: () => doc,
              length: doc.length,
              lines: starts.length,
              line: (n: number) => {
                const lineNo = Math.max(1, Math.min(starts.length, n))
                return { from: starts[lineNo - 1], number: lineNo }
              },
              lineAt: lineAtOffset,
            },
          }
        },
        lineBlockAt: (offset: number) => {
          const doc = ref.current?.value ?? value
          const { starts, heights, tops } = lineLayout(doc)
          let lineNo = 1
          for (let index = 0; index < starts.length; index += 1) {
            if (starts[index] <= offset) {
              lineNo = index + 1
            } else {
              break
            }
          }
          const top = tops[lineNo - 1] ?? 0
          const height = heights[lineNo - 1] ?? 18
          return { from: starts[lineNo - 1], top, bottom: top + height, height }
        },
        lineBlockAtHeight: (height: number) => {
          const doc = ref.current?.value ?? value
          const { starts, heights, tops } = lineLayout(doc)
          let lineNo = 1
          for (let index = 0; index < tops.length; index += 1) {
            if (height >= tops[index]) {
              lineNo = index + 1
            } else {
              break
            }
          }
          const top = tops[lineNo - 1] ?? 0
          const blockHeight = heights[lineNo - 1] ?? 18
          return { from: starts[lineNo - 1], top, bottom: top + blockHeight, height: blockHeight }
        },
      })
    }, [onCreateEditor, value])
    return (
      <textarea
        data-testid="codemirror-mock"
        data-has-apex-dsl-semantic-highlighting={hasApexDslSemanticHighlighting ? 'true' : 'false'}
        data-line-numbers={typeof basicSetup === 'object' && basicSetup.lineNumbers ? 'true' : 'false'}
        data-fold-gutter={typeof basicSetup === 'object' && basicSetup.foldGutter ? 'true' : 'false'}
        data-highlight-active-line={typeof basicSetup === 'object' && basicSetup.highlightActiveLine ? 'true' : 'false'}
        data-highlight-active-line-gutter={typeof basicSetup === 'object' && basicSetup.highlightActiveLineGutter ? 'true' : 'false'}
        data-editable={editable ? 'true' : 'false'}
        data-read-only={readOnly ? 'true' : 'false'}
        readOnly={readOnly || !editable}
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
    createReplaySessionId: replaySessionIdMock,
  }
})

function cssRule(css: string, selector: string) {
  const selectorIndex = css.indexOf(`${selector} {`)
  expect(selectorIndex).toBeGreaterThanOrEqual(0)
  const bodyStart = css.indexOf('{', selectorIndex) + 1
  const bodyEnd = css.indexOf('}', bodyStart)
  return css.slice(bodyStart, bodyEnd)
}

function findButtonByText(root: ParentNode, text: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === text)
}

function findButtonByTitle(root: ParentNode, title: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => (button.getAttribute('data-tooltip') || button.getAttribute('title')) === title)
}

function findReadinessCard(root: ParentNode) {
  return Array.from(root.querySelectorAll<HTMLElement>('.debug-readiness-card'))
    .find(card => card.textContent?.includes('Ready for debug?'))
}

function findWorkspaceTab(root: ParentNode, label: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent?.trim() === label)
}

function findCenterTab(root: ParentNode, label: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('.center-tab-button')).find(button => button.textContent?.trim() === label)
}

async function activateParameterDetailTab(root: ParentNode, label: 'Settings' | 'Data') {
  const tab = Array.from(root.querySelectorAll('.parameter-detail-tabs [role="tab"]'))
    .find(button => button.textContent?.trim() === label)
  if (tab) {
    await act(async () => {
      tab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
}

async function activateParameterDataTab(root: ParentNode) {
  await activateParameterDetailTab(root, 'Data')
}

async function activateParameterInfoTab(root: ParentNode) {
  await activateParameterDetailTab(root, 'Settings')
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

function findInputByLabelText(root: ParentNode, labelText: string) {
  return Array.from(root.querySelectorAll('label')).find(label =>
    label.textContent?.includes(labelText),
  )?.querySelector('input') as HTMLInputElement | null | undefined
}

function findRuntimeCheckboxList(root: ParentNode, labelText: string) {
  return Array.from(root.querySelectorAll('.runtime-checkbox-list')).find(list =>
    list.textContent?.includes(labelText),
  ) as HTMLElement | undefined
}

function findOpenDebuggerButton(root: ParentNode) {
  return findWorkspaceTab(root, 'Debug')
}

async function flushReact() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await new Promise(resolve => setTimeout(resolve, 0))
  })
}

function findDebuggerRunButton(root: ParentNode, title = 'Run Replay') {
  return findButtonByTitle(root, title)
}

function TopbarContributionHarness() {
  const contribution = useActiveTopbarContribution()
  return (
    <div data-testid="topbar-contribution">
      {contribution?.titleNode}
      {contribution?.meta?.filter(item => !item.hidden).map(item => (
        <span className="topbar-meta-chip" key={item.id}>
          {item.label}
        </span>
      ))}
      {contribution?.controls}
      {contribution?.secondaryActions?.filter(action => !action.hidden).map(action => (
        <button
          key={action.id}
          className={action.className}
          disabled={action.disabled}
          title={action.title}
          onClick={action.onClick}
        >
          {action.icon && <span className="topbar-action-icon">{action.icon}</span>}
          {action.label}
        </button>
      ))}
      {contribution?.primaryActions?.filter(action => !action.hidden).map(action => (
        <button
          key={action.id}
          disabled={action.disabled}
          title={action.title}
          onClick={action.onClick}
        >
          {action.label}
        </button>
      ))}
    </div>
  )
}

function AppShellScriptEditorHarness({ initialScript }: { initialScript: ScriptDefinition }) {
  const contribution = useActiveTopbarContribution()
  return (
    <>
      <span data-testid="app-shell-active-contribution">{contribution?.id || 'none'}</span>
      <TopbarContributionHarness />
      <ScriptEditor initialScript={initialScript} />
    </>
  )
}

function unavailableRuntimeMetadata(id: string, status = 'running', revision = '1') {
  const terminal = ['completed', 'failed', 'stopped'].includes(status)
  return { id, session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: revision,
    runtime_state: { version: 1, kind: 'full', session_id: id, session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: revision, state: 'unavailable', reason: terminal ? 'session_terminal' : status === 'paused' ? 'pause_pending' : 'not_paused', snapshot: null } }
}

function debugSessionResponse(report: {
  replay_session_id?: string
  group: string
  protocol: string
  started_at: string
  completed_at?: string
  success: boolean
  error?: string
  final_variables?: Record<string, string>
  runtime_evidence_unavailable?: boolean
  events: unknown[]
}, overrides: Record<string, unknown> = {}) {
  const terminal = Boolean(report.completed_at)
  return {
    replay_session_id: 'debug-session-1',
    debug_session_id: 'debug-session-1',
    session: {
      id: 'debug-session-1',
      session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
      state_revision: terminal ? '2' : '1',
      runtime_state: { version: 1, kind: 'full', session_id: 'debug-session-1', session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: terminal ? '2' : '1', state: 'unavailable', reason: terminal ? 'session_terminal' : 'not_paused', snapshot: null },
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

function heldDebugSession(revision = '10', value = 'held-value') {
  const base = debugSessionResponse({ group: 'held-debug', protocol: 'http', started_at: '2026-10-04T00:00:00Z', success: true, events: [] }).session
  return { ...base, ...unavailableRuntimeMetadata('debug-session-1', 'paused', revision), status: 'paused',
    runtime_state: { ...unavailableRuntimeMetadata('debug-session-1', 'paused', revision).runtime_state, state: 'paused', reason: 'boundary_held', snapshot: {
      state: 'complete', boundary: 'before_action', location: { phase: 'action', action_index: 0, event_index: 0, action_name: 'Get Products', action_type: 'request' },
      variables: [{ name: 'ordinary', value_state: 'assigned', value }], parameters: [], rows: [], frames: [], omissions: { variables: 0, parameters: 0, rows: 0, frames: 0, values: 0 },
    } } }
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
    vi.resetAllMocks()
    let debugRunNumber = 0
    replaySessionIdMock.mockImplementation(() => `debug-session-${++debugRunNumber}`)
    window.localStorage.clear()
    window.sessionStorage.clear()
    window.location.hash = ''
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
    downloadTextFileMock.mockResolvedValue('saved')
    codeMirrorDispatchMock.mockReset()
    codeMirrorScrollDoms.length = 0
    apiMock.getScript.mockResolvedValue({ script: initialScript })
    apiMock.getScriptAuthoring.mockImplementation(async (id: string, projectId?: string) => authoringFixture((await apiMock.getScript(id)).script, id, projectId))
    apiMock.getScriptSecretStatus.mockResolvedValue({
      script_id: 'script-1',
      project_id: 'project-1',
      required: [],
      configured: [],
      missing: [],
      secrets: [],
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
    })
    apiMock.getProjectSecretStatuses.mockImplementation(async (projectId: string, names: string[]) => ({
      project_id: projectId,
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: names.map(name => ({ name, configured: false })),
    }))
    apiMock.getProjectSecretUsage.mockImplementation(async (projectId: string, name: string) => ({
      project_id: projectId,
      name,
      usage_revision: `usage-${name}`,
      consumer_count: 1,
      reference_count: 1,
      consumers: [{ script_id: 'script-1', script_name: 'Checkout Flow', reference_count: 1 }],
      limit: 50,
      offset: 0,
      has_more: false,
    }))
    apiMock.listProjectSecrets.mockResolvedValue({
      project_id: 'project-1',
      capabilities: { provider: 'encrypted_database', writable: true, create_supported: true, replace_supported: true },
      secrets: [],
      count: 0,
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 1 })
    apiMock.createScript.mockResolvedValue({ id: 'script-1' })
    apiMock.updateScript.mockImplementation(async (id: string, script: ScriptDefinition) => savedAuthoringFixture(script, id))
    apiMock.downloadScript.mockResolvedValue('{"preserved":"download-password"}')
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
          ...unavailableRuntimeMetadata('browser-replay-1', 'running', '1'),
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
          ...unavailableRuntimeMetadata('browser-replay-1', 'running', '1'),
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
		promotion_status: 'blocked',
        source_browser_actions: 1,
        captured_requests: 1,
        generated_http_actions: 1,
        skipped_static_assets: 0,
		correlation_candidates: [{ name: 'csrf_token', source: 'query', evidence: 'GET /api/cart' }],
        data_source_candidates: [{ name: 'customer_id', source: 'query', value: '123', evidence: 'GET /api/cart' }],
		blocking_findings: [{ severity: 'error', summary: 'Correlation evidence is not durable.', evidence: 'Uploaded network evidence only.', action: 'Run and retain a successful replay.' }],
        findings: ['1 replay network request(s) were converted into an HTTP draft.'],
		next_actions: ['Run and retain a successful replay.'],
		evidence: {
		  replay_session_id: 'browser-replay-1',
		  replay_session_resolved: true,
		  replay_session_status: 'completed',
		  replay_session_successful: true,
		  replay_network_entries: 1,
		  network_durable: true,
		  correlation_durable: false,
		  durable: false,
		  verification_status: 'correlation_evidence_required',
		  network_entries: 1,
		  correlation_observations: 0,
		},
      },
	  governance: {
		status: 'blocked',
		approval_required: true,
		approval_status: 'missing',
		required_evidence: ['durable_correlation_evidence'],
		blockers: [{ severity: 'error', summary: 'Correlation evidence is not durable.', evidence: 'Uploaded network evidence only.', action: 'Run and retain a successful replay.' }],
		next_actions: ['Run and retain a successful replay.'],
	  },
	  diff: {
		source_protocol: 'browser',
		generated_protocol: 'http',
		source_browser_actions: 1,
		generated_http_actions: 1,
		captured_requests: 1,
		endpoint_coverage_pct: 100,
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
    apiMock.getScriptRecordingStatus.mockResolvedValue({ exists: false })
    apiMock.pauseBrowserReplaySession.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'paused', '1'), status: 'paused' } })
    apiMock.resumeBrowserReplaySession.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'), status: 'running' } })
    apiMock.stopBrowserReplaySession.mockResolvedValue(undefined)
    apiMock.pauseScriptDebugSession.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'paused', '1'), status: 'paused' } })
    apiMock.resumeScriptDebugSession.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'), status: 'running' } })
    apiMock.stepScriptDebugSession.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'paused', '1'), status: 'paused' } })
    apiMock.stopScriptDebugSession.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'stopped', '2'), status: 'stopped' } })
    apiMock.getScriptDebugSession.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'), id: 'debug-session-1', status: 'running', events: [] } })
    apiMock.updateScriptDebugBreakpoints.mockResolvedValue({ session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'), breakpoints: [] } })
    apiMock.listAuthoringComponents.mockResolvedValue({ components: [], count: 0 })
    apiMock.createAuthoringComponent.mockResolvedValue({ component: { id: 'block-1' } })
    apiMock.deleteAuthoringComponent.mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    window.location.hash = ''
  })

  it('passes APEX DSL semantic highlighting to the Script JSON editor', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const codeEditors = Array.from(container.querySelectorAll('[data-testid="codemirror-mock"]'))
    expect(codeEditors.some(editor => editor.getAttribute('data-has-apex-dsl-semantic-highlighting') === 'true')).toBe(true)
  })

  it('does not render the page title inside the scrollable editor body', async () => {
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })

    expect(container.querySelector('.editor-header h2')).toBeNull()
    expect(container.querySelector('.editor-header')?.textContent).not.toContain('Edit Script')
    expect(container.querySelector('.editor-header .editor-actions')).not.toBeNull()
    expect(container.querySelector('.side-panel-script-name')).not.toBeNull()
  })

  it('loads stored secret status, opens script bindings, and refreshes it after save', async () => {
    apiMock.getProjectSecretStatuses
      .mockResolvedValueOnce({
        project_id: 'project-1',
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
      })
      .mockResolvedValueOnce({
        project_id: 'project-1',
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
      })
      .mockResolvedValueOnce({
        project_id: 'project-1',
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: [{ name: 'LOGIN_TOKEN', configured: true }],
      })
    apiMock.getScriptSecretStatus
      .mockResolvedValueOnce({
        script_id: 'script-1',
        project_id: 'project-1',
        required: ['LOGIN_TOKEN'],
        configured: [],
        missing: ['LOGIN_TOKEN'],
        secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
      })
      .mockResolvedValueOnce({
        script_id: 'script-1',
        project_id: 'project-1',
        required: ['LOGIN_TOKEN'],
        configured: [],
        missing: ['LOGIN_TOKEN'],
        secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
        bindings: [{ name: 'LOGIN_TOKEN', configured: false, occurrence_count: 1, locations: ['/actions/0/headers/Authorization'] }],
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
      })
      .mockResolvedValueOnce({
        script_id: 'script-1',
        project_id: 'project-1',
        required: ['LOGIN_TOKEN'],
        configured: ['LOGIN_TOKEN'],
        missing: [],
        secrets: [{ name: 'LOGIN_TOKEN', configured: true }],
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
      })

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await flushReact()

    expect(apiMock.getScriptSecretStatus).toHaveBeenCalledWith('script-1')
    expect(container.querySelector('.script-secret-banner')?.textContent).toContain('1 project secret still needs configuration')
    const blockedRunButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.dataset.tooltip || button.title).includes('Configure missing project secret before running'))
    expect(blockedRunButton?.disabled).toBe(true)
    expect((blockedRunButton?.dataset.tooltip || blockedRunButton?.title)).toContain('LOGIN_TOKEN')
    expect(container.textContent).toContain('Configure missing project secret: LOGIN_TOKEN.')
    expect(container.textContent).toContain('Needs attention')

    const configureButton = findButtonByText(container, 'Configure secrets')
    await act(async () => {
      configureButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushReact()
    expect(document.body.textContent).toContain('Secret Bindings · Checkout Flow')
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBeNull()
    expect(window.location.hash).toBe('')
    await act(async () => {
      Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Close')?.click()
    })

    const nameInput = container.querySelector<HTMLInputElement>('.side-panel-script-name input')
    expect(nameInput).not.toBeNull()
    if (!nameInput) return
    await act(async () => {
      changeInputValue(nameInput, 'Checkout Flow Updated')
      await Promise.resolve()
    })
    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushReact()

    expect(apiMock.getScriptSecretStatus).toHaveBeenCalledTimes(3)
    expect(container.querySelector('.script-secret-banner')).toBeNull()
  })

  it('probes secret references from valid unsaved raw JSON before replay', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })
    await flushReact()
    apiMock.getProjectSecretStatuses.mockClear()

    await act(async () => {
      findCenterTab(container, 'Source')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const editJsonButton = findButtonByText(container, 'Edit JSON')
    await act(async () => {
      editJsonButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const codeArea = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(codeArea).not.toBeNull()
    if (!codeArea) return

    const rawDraft: ScriptDefinition = {
      ...initialScript,
      actions: [{
        ...initialScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.RAW_JSON_TOKEN}}' },
      }],
    }
    await act(async () => {
      changeTextareaValue(codeArea, JSON.stringify({ script: rawDraft }, null, 2))
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('project-1', ['RAW_JSON_TOKEN'])
    expect(container.querySelector('.script-secret-banner')?.textContent).toContain('1 project secret still needs configuration')
    const runButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.dataset.tooltip || button.title).includes('Configure missing project secret before running'))
    expect(runButton?.disabled).toBe(true)
    expect((runButton?.dataset.tooltip || runButton?.title)).toContain('RAW_JSON_TOKEN')
    expect(apiMock.startDraftScriptDebugSession).not.toHaveBeenCalled()
  })

  it('treats a fresh missing project status as authoritative over persisted configured status', async () => {
    const secretScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        ...initialScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      }],
    }
    apiMock.getScriptSecretStatus.mockResolvedValueOnce({
      script_id: 'script-1',
      project_id: 'project-1',
      required: ['LOGIN_TOKEN'],
      configured: ['LOGIN_TOKEN'],
      missing: [],
      secrets: [{ name: 'LOGIN_TOKEN', configured: true }],
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
    })
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
    })

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={secretScript} projectId="project-1" />)
    })
    await flushReact()

    expect(container.querySelector('.script-secret-banner')?.textContent).toContain('1 project secret still needs configuration')
    const runButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.dataset.tooltip || button.title).includes('Configure missing project secret before running'))
    expect(runButton?.disabled).toBe(true)
    expect((runButton?.dataset.tooltip || runButton?.title)).toContain('LOGIN_TOKEN')
  })

  it('reports a created dedicated secret when the later editor save fails', async () => {
    const secretScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        ...initialScript.actions[0],
        headers: {
          ...initialScript.actions[0].headers,
          Authorization: 'Bearer {{secret.LOGIN_TOKEN}}',
        },
      }],
    }
    apiMock.getScriptSecretStatus.mockResolvedValue({
      script_id: 'script-1',
      project_id: 'project-1',
      required: ['LOGIN_TOKEN'],
      configured: [],
      missing: ['LOGIN_TOKEN'],
      secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
      bindings: [{
        name: 'LOGIN_TOKEN', configured: false, occurrence_count: 1,
        locations: ['/actions/0/headers/Authorization'],
      }],
      capabilities: {
        provider: 'encrypted_database', writable: true, create_supported: true,
        replace_supported: true, source_secret_migration_policy_enabled: false,
      },
    })
    apiMock.getProjectSecretUsage.mockImplementation(async (projectId: string, name: string) => ({
      project_id: projectId,
      name,
      usage_revision: `usage-${name}`,
      consumer_count: name === 'LOGIN_TOKEN' ? 2 : 0,
      reference_count: name === 'LOGIN_TOKEN' ? 2 : 0,
      consumers: name === 'LOGIN_TOKEN'
        ? [
            { script_id: 'script-1', script_name: 'Checkout Flow', reference_count: 1 },
            { script_id: 'script-2', script_name: 'Checkout Clone', reference_count: 1 },
          ]
        : [],
      limit: 50,
      offset: 0,
      has_more: false,
    }))
    apiMock.rebindScriptSecretReference.mockResolvedValue({
      script: {
        ...secretScript,
        actions: [{
          ...secretScript.actions[0],
          headers: {
            ...secretScript.actions[0].headers,
            Authorization: 'Bearer {{secret.LOGIN_TOKEN_CHECKOUT_FLOW}}',
          },
        }],
      },
      old_name: 'LOGIN_TOKEN',
      new_name: 'LOGIN_TOKEN_CHECKOUT_FLOW',
      changed_locations: ['/actions/0/headers/Authorization'],
      changed_count: 1,
    })
    apiMock.updateScript.mockRejectedValueOnce(new Error('save unavailable'))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={secretScript} projectId="project-1" />)
    })
    await flushReact()
    await act(async () => {
      findButtonByText(container, 'Configure secrets')?.click()
    })
    await flushReact()

    const valueInput = document.body.querySelector<HTMLInputElement>('input[type="password"]')
    expect(valueInput).not.toBeNull()
    if (!valueInput) return
    await act(async () => {
      changeInputValue(valueInput, 'dedicated-secret-value')
    })
    await act(async () => {
      Array.from(document.body.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent === 'Create dedicated value')?.click()
    })
    await flushReact()

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.click()
    })
    await flushReact()

    expect(container.textContent).toContain('save unavailable')
    expect(container.textContent).toContain('LOGIN_TOKEN_CHECKOUT_FLOW was already created')
    expect(container.textContent).toContain('unused entries')
    expect(container.textContent).not.toContain('dedicated-secret-value')
  })

  it('treats a Model Existing clone as immediately saveable with Save & configure sequencing', async () => {
    const clonedScript: ScriptDefinition = {
      ...initialScript,
      name: 'Copy of Checkout Flow',
      actions: [{
        ...initialScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      }],
    }
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
    })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <ScriptEditor initialScript={clonedScript} initiallyDirty projectId="project-1" />
        </NavigationGuardProvider>,
      )
    })
    await flushReact()

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton?.disabled).toBe(false)
    expect((saveButton?.getAttribute('data-tooltip') || saveButton?.getAttribute('title'))).toBe('Save script')
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('project-1', ['LOGIN_TOKEN'])
    expect(container.querySelector('.script-secret-banner')?.textContent).toContain('1 project secret still needs configuration')

    const configureButton = findButtonByText(container, 'Save & configure secrets')
    await act(async () => {
      configureButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).toHaveBeenCalled()
    expect(window.location.hash).toBe('')
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBeNull()
  })

  it('keeps Save available but fails replay closed while required-secret status is unknown or unavailable', async () => {
    const clonedScript: ScriptDefinition = {
      ...initialScript,
      name: 'Copy of Checkout Flow',
      actions: [{
        ...initialScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      }],
    }
    let rejectStatus: ((cause: Error) => void) | undefined
    apiMock.getProjectSecretStatuses.mockReturnValueOnce(new Promise((_, reject) => {
      rejectStatus = reject
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={clonedScript} initiallyDirty projectId="project-1" />)
      await Promise.resolve()
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton?.disabled).toBe(false)
    let runButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.dataset.tooltip || button.title).includes('Checking project secret configuration'))
    expect(runButton?.disabled).toBe(true)
    expect(apiMock.replayDraftScript).not.toHaveBeenCalled()

    await act(async () => {
      rejectStatus?.(new Error('status unavailable'))
      await Promise.resolve()
      await Promise.resolve()
    })

    runButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.dataset.tooltip || button.title).includes('Project secret status is temporarily unavailable'))
    expect(runButton?.disabled).toBe(true)
    expect(saveButton?.disabled).toBe(false)
    expect(apiMock.replayDraftScript).not.toHaveBeenCalled()
  })

  it('checks required secrets in the saved script project when no project is actively selected', async () => {
    const savedScript: ScriptDefinition = {
      ...initialScript,
      project_id: 'unassigned',
      actions: [{
        ...initialScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      }],
    }
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'unassigned',
      capabilities: {
        provider: 'environment_read_only',
        writable: false,
        create_supported: false,
        replace_supported: false,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
    })

    await act(async () => {
      root.render(<ScriptEditor scriptId="saved-script" initialScript={savedScript} />)
    })
    await flushReact()

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('unassigned', ['LOGIN_TOKEN'])
    expect(container.textContent).not.toContain('Project secret configuration is still being checked')
    expect(container.textContent).toContain('LOGIN_TOKEN')
  })

  it('downloads a clean saved script from the exact server artifact', async () => {
    const exact = '{"config":{"http":{"auth":{"password":"download-password"}}}}'
    apiMock.downloadScript.mockResolvedValueOnce(exact)

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })
    await flushReact()

    const downloadButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Download')
    expect(downloadButton).toBeTruthy()
    await act(async () => {
      downloadButton?.click()
      await Promise.resolve()
    })

    expect(apiMock.downloadScript).toHaveBeenCalledWith('script-1')
		expect(downloadTextFileMock).toHaveBeenCalledWith(exact, 'checkout-flow.json')
  })

  it('probes project secret status for an unsaved clone without showing configured references as missing', async () => {
    const clonedScript: ScriptDefinition = {
      ...initialScript,
      name: 'Copy of Checkout Flow',
      config: {
        ...initialScript.config,
        database: { dsn_secret: 'DATABASE_DSN' },
      },
      actions: [{
        ...initialScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      }],
    }
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [
        { name: 'DATABASE_DSN', configured: true },
        { name: 'LOGIN_TOKEN', configured: true },
      ],
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={clonedScript} initiallyDirty projectId="project-1" />)
    })
    await flushReact()

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('project-1', ['DATABASE_DSN', 'LOGIN_TOKEN'])
    expect(container.querySelector('.script-secret-banner')).toBeNull()
    expect(container.textContent).not.toContain('Missing secret: DATABASE_DSN')
    expect(container.textContent).not.toContain('Missing secret: LOGIN_TOKEN')
  })

  it('publishes script identity and primary actions to the app topbar contribution', async () => {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <ScriptEditor initialScript={initialScript} projectId="project-1" />
        </TopbarContributionProvider>,
      )
    })
    await flushReact()

    const topbar = container.querySelector('[data-testid="topbar-contribution"]')
    const topbarNameInput = topbar?.querySelector<HTMLInputElement>('input[placeholder="My_Test_Script"]')
    const nameInput = container.querySelector<HTMLInputElement>('.side-panel-script-name input[placeholder="My_Test_Script"]')
    const workspaceToggle = topbar?.querySelector('.page-view-toggle.script-page-toggle.script-mode-switcher')
    const workspaceButtons = Array.from(workspaceToggle?.querySelectorAll<HTMLButtonElement>('.mode-chip') || [])
    expect(topbarNameInput).toBeNull()
    expect(nameInput?.value).toBe('Checkout Flow')
    expect(topbar?.textContent).toContain('Author')
    expect(topbar?.textContent).toContain('Debug')
    expect(workspaceToggle).not.toBeNull()
    expect(workspaceButtons.map(button => button.textContent?.trim())).toEqual(['Author', 'Debug'])
    expect(workspaceButtons[0]?.classList.contains('active')).toBe(true)
    expect(topbar?.textContent).not.toContain('Validate Draft')
    expect(topbar?.textContent).not.toContain('Validate Saved')
    expect(topbar?.textContent).not.toContain('Save Script')
    expect(topbar?.textContent).not.toContain('Close')
    expect(Array.from(topbar?.querySelectorAll('button') || []).map(button => button.textContent?.trim())).not.toContain('Settings')
    expect(Array.from(topbar?.querySelectorAll('button') || []).map(button => button.textContent?.trim())).not.toContain('Breakpoint')
    const debugActionLabels = () => Array.from(topbar?.querySelectorAll<HTMLButtonElement>('.topbar-debug-action') || []).map(button => button.textContent?.trim())
    const topbarWorkspaceButton = (label: string) => Array.from(topbar?.querySelectorAll<HTMLButtonElement>('.mode-chip') || [])
      .find(button => button.textContent?.trim() === label)
    expect(debugActionLabels()).toEqual(['Debug one iteration'])
    expect(Array.from(topbar?.querySelectorAll<HTMLButtonElement>('.topbar-debug-action') || []).every(button => button.querySelector('.topbar-action-icon svg'))).toBe(true)
    await act(async () => {
      topbarWorkspaceButton('Debug')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(debugActionLabels()).toEqual([
      'Debug one iteration',
      'Step',
      'Break on failure',
    ])
    await act(async () => {
      topbarWorkspaceButton('Author')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(debugActionLabels()).toEqual(['Debug one iteration'])
    const metaLabels = Array.from(topbar?.querySelectorAll('.topbar-meta-chip') || []).map(chip => chip.textContent?.trim())
    expect(metaLabels).not.toContain('Create Script')
    expect(metaLabels).not.toContain('Edit Script')
    expect(metaLabels).not.toContain((initialScript.protocol || 'http').toUpperCase())
    expect(metaLabels).not.toContain('Draft')
    expect(metaLabels).not.toContain('JSON Draft')
    expect(container.querySelector('.script-editor-local-action-proxy')).toBeNull()

    const activeNameInput = container.querySelector<HTMLInputElement>('.side-panel-script-name input[placeholder="My_Test_Script"]')
    expect(activeNameInput?.value).toBe('Checkout Flow')
    if (!activeNameInput) return
    await act(async () => {
      changeInputValue(activeNameInput, 'Side Panel Checkout Flow')
      await Promise.resolve()
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    expect(saveButton.textContent?.trim()).toBe('')
    expect(saveButton.querySelector('svg')).not.toBeNull()
    expect(saveButton.disabled).toBe(false)

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Side Panel Checkout Flow',
    }), 'project-1', undefined)
  })

  it('does not re-register its topbar contribution when the app shell consumes it', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const templateScript: ScriptDefinition = {
      ...initialScript,
      name: 'HTTP API Smoke',
    }

    try {
      await act(async () => {
        root.render(
          <TopbarContributionProvider>
            <AppShellScriptEditorHarness initialScript={templateScript} />
          </TopbarContributionProvider>,
        )
      })
      await flushReact()

      const maximumDepthWarnings = consoleError.mock.calls
        .map(arguments_ => arguments_.map(String).join(' '))
        .filter(message => message.includes('Maximum update depth exceeded'))
      expect(maximumDepthWarnings).toEqual([])
      expect(container.querySelector('[data-testid="app-shell-active-contribution"]')?.textContent).toBe('script-editor')
    } finally {
      consoleError.mockRestore()
    }
  })

  it('keeps source mapping in the outline instead of duplicating imported metadata in the page header', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    let headerActions = container.querySelector('.editor-header .editor-actions')
    expect(headerActions?.textContent).not.toContain('Source Mapping')
    expect(container.querySelector('.editor-outline-nav')?.textContent).not.toContain('Source Mapping')

    const importedScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {
          source: 'enterprise_script_archive',
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
    expect(headerActions?.textContent).not.toContain('Source Mapping')
    const outlineButton = Array.from(container.querySelector('.actions-sidebar')?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Source Mapping')
    expect(outlineButton).toBeDefined()

    await act(async () => {
      outlineButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const metadataSection = container.querySelector('#imported-metadata-section')
    const actionDetail = container.querySelector('.action-detail')
    expect(metadataSection).not.toBeNull()
    // Metadata JSON is no longer a center tab — it opens on demand from the outline.
    expect(findCenterTab(container, 'Metadata JSON')).toBeUndefined()
    const metadataJsonLink = Array.from(container.querySelector('.actions-sidebar')?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Imported Metadata JSON')
    expect(metadataJsonLink).toBeDefined()
    metadataJsonLink?.focus()
    await act(async () => {
      metadataJsonLink?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushReact()
    const metadataJsonEditor = container.querySelector('.metadata-json-dialog .metadata-json-viewer textarea') as HTMLTextAreaElement | null
    expect(metadataJsonEditor).not.toBeNull()
    expect(container.querySelector('.metadata-json-dialog')?.contains(document.activeElement)).toBe(true)
    expect(metadataJsonEditor?.value).toContain('checkout.usr')
    expect(metadataJsonEditor?.dataset.lineNumbers).toBe('true')
    expect(metadataJsonEditor?.dataset.foldGutter).toBe('true')
    expect(metadataJsonEditor?.dataset.highlightActiveLine).toBe('true')
    expect(metadataJsonEditor?.dataset.highlightActiveLineGutter).toBe('true')
    expect(metadataJsonEditor?.dataset.readOnly).toBe('true')
    expect(metadataJsonEditor?.dataset.editable).toBe('false')
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    await flushReact()
    expect(container.querySelector('.metadata-json-dialog')).toBeNull()
    expect(document.activeElement).toBe(metadataJsonLink)
    expect(actionDetail).toBeNull()
    expect(container.querySelector('.author-code-center')?.contains(metadataSection)).toBe(true)
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).not.toContain('Outline')
    expect(metadataSection?.getAttribute('role')).toBeNull()
    expect(metadataSection?.classList.contains('editor-support-dialog')).toBe(false)
    expect(container.querySelector('.support-dialog-import')).toBeNull()
  })

  it('surfaces saved TruClient2 promotion review evidence in imported metadata', async () => {
    const importedWeb2Script: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      actions: [
        {
          name: 'Click Checkout',
          type: 'browser_click',
          target: '#checkout',
          selector: '#checkout',
          metadata: {
            source_selector_fallbacks: [
              { kind: 'css', value: '[data-testid="checkout"]', confidence: 92 },
              { kind: 'xpath', value: '//*[@id="checkout"]', confidence: 70 },
            ],
          },
        },
        {
          name: 'Evaluate Ready',
          type: 'browser_evaluate_js',
          target: 'window.checkoutReady === true',
          metadata: {
            review_required: true,
            review_gate: 'javascript',
          },
        },
      ],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          source_script_name: 'CheckoutWeb2.zip',
          preserved: {
            source_script_type: 'TruClient 2.0 - Web',
            trueclient2_promotion_review: {
              schema_version: 1,
              status: 'replay_evidence_attached',
              generated_at: '2026-05-14T20:00:03Z',
              source: 'enterprise_script_archive',
              source_script_name: 'CheckoutWeb2.zip',
              compatibility: {
                source_profile: 'truclient2_web',
                import_mode: 'review',
                promotion_status: 'review_required',
                promotion_allowed: false,
                runtime_parity_status: 'partial',
                replay_status: 'attached',
                javascript_review_gate: 'required',
                http_conversion_readiness: 'needs_approval',
                selector_confidence_pct: 92,
                frame_window_blockers: 1,
              },
              gates: {
                review_required_actions: 1,
                javascript_review_actions: 1,
                selector_fallback_candidates: 2,
                replay_required: false,
                replay_warnings: ['Review JavaScript before promotion.'],
                import_warnings: ['Frame context requires replay verification.'],
              },
              replay_evidence: {
                session_id: 'draft-browser-validation',
                status: 'completed',
                started_at: '2026-05-14T20:00:00Z',
                completed_at: '2026-05-14T20:00:02Z',
                events: 2,
                passed: 2,
                failed: 0,
                screenshots: 1,
                dom_html_captures: 1,
                console_entries: 3,
                network_entries: 4,
                source_steps: 2,
                source_transactions: 1,
              },
              next_actions: ['Review selector fallbacks and JavaScript approvals before production promotion.'],
            },
          },
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedWeb2Script} />)
    })

    const importedLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Source Mapping'))
    expect(importedLauncher).toBeDefined()
    await act(async () => {
      importedLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const metadataSection = container.querySelector('#imported-metadata-section')
    expect(metadataSection?.textContent).toContain('TruClient Promotion Review')
    expect(metadataSection?.textContent).toContain('Replay evidence attached')
    expect(metadataSection?.textContent).toContain('draft-browser-validation')
    expect(metadataSection?.textContent).toContain('2 passed / 0 failed')
    expect(metadataSection?.textContent).toContain('Selector Fallbacks')
    expect(metadataSection?.textContent).toContain('92% selector confidence')
    expect(metadataSection?.textContent).toContain('Frame context requires replay verification.')
    expect(metadataSection?.textContent).toContain('Review selector fallbacks and JavaScript approvals before production promotion.')

    const utilitySidebar = container.querySelector('.editor-utility-sidebar')
    expect(utilitySidebar?.textContent).toContain('TruClient Promotion')
    expect(utilitySidebar?.textContent).toContain('draft-browser-validation')
    expect(utilitySidebar?.textContent).toContain('2 passed / 0 failed')
    expect(utilitySidebar?.textContent).toContain('2 fallbacks')
    expect(utilitySidebar?.textContent).toContain('1 frame/window blocker')
  })

  it('expands an http_sequence action in the Author outline to reveal sub-request rows', async () => {
    const sequenceScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        name: 'Checkout_Txn', type: 'http_sequence', method: 'SEQUENCE', target: '/aura',
        params: { requests: [
          { name: 'Sub_One', type: 'POST', method: 'POST', target: '/one' },
          { name: 'Sub_Two', type: 'GET', method: 'GET', target: '/two' },
        ] },
      }],
    }
    await act(async () => { root.render(<ScriptEditor initialScript={sequenceScript} />) })

    // Collapsed by default — no nested rows until the container is expanded.
    expect(container.querySelectorAll('.action-item-nested')).toHaveLength(0)
    expect(container.querySelector('.action-expand-glyph')).not.toBeNull()
    expect(container.querySelector('.action-expand-toggle')).toBeNull()
    const actionNameToggle = Array.from(container.querySelectorAll<HTMLButtonElement>('.action-name-toggle'))
      .find(button => button.textContent?.trim() === 'Checkout_Txn')
    expect(actionNameToggle).toBeDefined()
    expect(actionNameToggle?.getAttribute('aria-expanded')).toBe('false')
    await act(async () => { actionNameToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const nested = container.querySelectorAll('.action-item-nested')
    expect(nested).toHaveLength(2)
    expect(nested[0].textContent).toContain('Sub_One')
    expect(nested[1].textContent).toContain('Sub_Two')
    expect(actionNameToggle?.getAttribute('aria-expanded')).toBe('true')
  })

  it('edits a nested request HTTP policy with script defaults and saves Inherit by removing only that property', async () => {
    const request = {
      name: 'Version_Leaf', type: 'POST', method: 'POST', target: '/leaf', http_version: '1.1',
      body_encoding: 'base64', body: 'YQ==', timeout_ms: 1200,
      header_list: [{ name: 'X-Trace', value: 'keep' }], vendor_extension: { keep: true },
      metadata: { 'har.request.http_version': 'HTTP/2', 'har.response.http_version': 'HTTP/1.1' },
    }
    const sibling = { name: 'Other_Leaf', type: 'GET', target: '/other', http_version: '2' }
    const sequenceScript = {
      ...initialScript, protocol: 'http/1.1',
      config: { ...initialScript.config, http: { ...initialScript.config.http, version: '2' } },
      actions: [{ name: 'Version_Group', type: 'http_sequence', target: '/group', params: { requests: [request, sibling], future_option: true }, metadata: { keep: 'parent' } }],
    } as ScriptDefinition
    await act(async () => { root.render(<ScriptEditor initialScript={sequenceScript} projectId="project-1" />) })
    const expand = Array.from(container.querySelectorAll<HTMLButtonElement>('.action-name-toggle')).find(button => button.textContent?.trim() === 'Version_Group')!
    await act(async () => { expand.click() })
    const leaf = Array.from(container.querySelectorAll<HTMLElement>('.action-item-nested')).find(row => row.textContent?.includes('Version_Leaf'))!
    await act(async () => { leaf.click() })
    const region = container.querySelector('[aria-label="Request HTTP version policy"]')!
    const version = region.querySelector<HTMLSelectElement>('select')!
    expect(version.value).toBe('1.1')
    expect(version.options[0].textContent).toContain('HTTP/2 required')
    expect(region.textContent).toContain('Captured request HTTP versionHTTP/2')
    expect(region.textContent).toContain('Captured response HTTP versionHTTP/1.1')
    await act(async () => { version.value = 'auto'; version.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(JSON.parse(container.querySelector<HTMLTextAreaElement>('.script-code-pane textarea')!.value).http_version).toBe('auto')
    await act(async () => { version.value = 'inherit'; version.dispatchEvent(new Event('change', { bubbles: true })) })
    const selected = JSON.parse(container.querySelector<HTMLTextAreaElement>('.script-code-pane textarea')!.value)
    expect(selected).not.toHaveProperty('http_version')
    expect(selected.vendor_extension).toEqual({ keep: true })
    expect(region.textContent).toContain('Authored policy: Inherit script setting (HTTP/2 required')
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click(); await Promise.resolve() })
    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
    const payload = apiMock.createScript.mock.calls[0][0] as ScriptDefinition
    const savedRequests = payload.actions[0].params!.requests as typeof request[]
    expect(savedRequests[0]).toEqual(Object.fromEntries(Object.entries(request).filter(([key]) => key !== 'http_version')))
    expect(savedRequests[0]).not.toHaveProperty('http_version')
    expect(savedRequests[1]).toEqual(sibling)
    expect(payload.actions[0].params!.future_option).toBe(true)
    expect(payload.actions[0].metadata).toEqual({ keep: 'parent' })
    expect(payload.protocol).toBe('http/1.1')
    expect(payload.config.http!.version).toBe('2')
    expect(request.http_version).toBe('1.1')
  })

  it('shows selected parent action JSON by default with a full-script override', async () => {
    const sequenceScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        name: 'Checkout_Txn', type: 'http_sequence', method: 'SEQUENCE', target: '/aura',
        params: { requests: [
          { name: 'Sub_One', type: 'POST', method: 'POST', target: '/one' },
          { name: 'Sub_Two', type: 'GET', method: 'GET', target: '/two' },
        ] },
      }],
    }
    await act(async () => { root.render(<ScriptEditor initialScript={sequenceScript} />) })

    const parentRow = Array.from(container.querySelectorAll('.action-item'))
      .find(item => item.textContent?.includes('Checkout_Txn'))
    await act(async () => {
      parentRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const scopedEditor = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(container.querySelector('.script-json-scope-bar')?.textContent).toContain('Selected Action')
    expect(scopedEditor?.value).toContain('"name": "Checkout_Txn"')
    expect(scopedEditor?.value).toContain('"params"')
    expect(scopedEditor?.value).not.toContain('"script"')

    const fullScriptButton = findButtonByText(container, 'Full Script JSON')
    await act(async () => {
      fullScriptButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const fullEditor = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(fullEditor?.value).toContain('"script"')
    expect(fullEditor?.value).toContain('"Checkout Flow"')
  })

  it('selects sub-actions for scoped JSON and reusable block saving', async () => {
    const sequenceScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        name: 'Checkout_Txn', type: 'http_sequence', method: 'SEQUENCE', target: '/aura',
        params: { requests: [
          { name: 'Sub_One', type: 'POST', method: 'POST', target: '/one' },
          { name: 'Sub_Two', type: 'GET', method: 'GET', target: '/two' },
        ] },
      }],
    }
    await act(async () => { root.render(<ScriptEditor initialScript={sequenceScript} projectId="project-1" />) })

    const actionNameToggle = Array.from(container.querySelectorAll<HTMLButtonElement>('.action-name-toggle'))
      .find(button => button.textContent?.trim() === 'Checkout_Txn')
    expect(actionNameToggle).toBeDefined()
    await act(async () => { actionNameToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const nestedRows = Array.from(container.querySelectorAll('.action-item-nested'))
    const subTwoRow = nestedRows.find(row => row.textContent?.includes('Sub_Two')) as HTMLElement | undefined
    expect(subTwoRow).toBeDefined()
    await act(async () => {
      subTwoRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const scopedEditor = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(container.querySelector('.script-json-scope-bar')?.textContent).toContain('Selected Sub-action')
    expect(scopedEditor?.value).toContain('"name": "Sub_Two"')
    expect(scopedEditor?.value).toContain('"target": "/two"')
    expect(scopedEditor?.value).not.toContain('"Sub_One"')

    const subTwoCheckbox = subTwoRow?.querySelector('.action-block-select') as HTMLInputElement | null
    await act(async () => {
      subTwoCheckbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findButtonByText(container, 'Save 1 as block')).toBeDefined()
    expect(findButtonByText(container, 'Wrap 1 in transaction')).toBeUndefined()

    await act(async () => {
      findButtonByText(container, 'Save 1 as block')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const blockNameInput = container.querySelector('.save-block-field input') as HTMLInputElement | null
    expect(blockNameInput).not.toBeNull()
    if (!blockNameInput) return
    await act(async () => {
      changeInputValue(blockNameInput, 'Sub action block')
    })
    await act(async () => {
      findButtonByText(container, 'Save block')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createAuthoringComponent).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      name: 'Sub action block',
      actions: [expect.objectContaining({ name: 'Sub_Two', target: '/two' })],
    }))
  })

  it('renders grouped HTTP sequence actions with parent summary details', async () => {
    const sequenceScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        name: 'S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue',
        type: 'http_sequence',
        method: 'SEQUENCE',
        target: '/aura',
        metadata: {
          source_transaction: 'S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue',
          source_request_count: 2,
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

    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Action context')
    expect(container.querySelector('.author-main-action .action-editor-panel')).not.toBeNull()
    expect(container.querySelector('.editor-utility-sidebar .action-editor-panel')).toBeNull()
    expect(container.querySelector('.script-code-inline-inspector .action-editor-panel')).toBeNull()
    expect(container.querySelector('.author-main-action')?.textContent).toContain('Sequence Summary')
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).not.toContain('Grouped Requests')
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).not.toContain('Address_Search_Aura')
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).not.toContain('Checkout_Aura')

    expect(Array.from(container.querySelectorAll('.script-json-sidebar-tab')).map(button => button.textContent?.trim())).toEqual([])
    expect(container.querySelector('.json-source-map')).toBeNull()
    expect(container.querySelector('.action-list')?.textContent).toContain('2 req')
    expect(container.querySelector('.action-list')?.textContent).toContain('1 ext')
  })

  it('exposes a Find affordance in the Script JSON toolbar', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
      await Promise.resolve()
    })

    const findButton = findButtonByTitle(container, 'Find in script (⌘F / Ctrl+F)')
    expect(findButton).toBeDefined()
    expect(findButton?.textContent?.trim()).toBe('Find')
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
    const formFocusRule = cssRule(editorCss, '.script-editor :where(input, select, textarea):focus-visible')

    expect(backdropRule).toContain('z-index: 220;')
    expect(dialogRule).toContain('width: min(1440px, calc(100vw - 48px));')
    expect(documentDialogRule).toContain('width: 100%;')
    expect(documentDialogRule).toContain('max-width: none;')
    expect(documentDialogRule).toContain('align-self: stretch;')
    expect(gridScrollRule).toContain('max-height: min(440px, 45vh);')
    expect(gridHeaderRule).toContain('position: sticky;')
    expect(gridHeaderRule).toContain('top: 0;')
    expect(formFocusRule).toContain('outline: 2px solid var(--focus-ring);')
    expect(formFocusRule).toContain('outline-offset: 2px;')
  })

  it('lets the author center workbench fill the desktop editor frame', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const editorCss = readFileSync(`${cwd}/src/components/ScriptEditor.css`, 'utf8') as string
    const mainContentRule = cssRule(editorCss, '.main-content:has(.editor-body.workspace-author)')
    const topbarRule = cssRule(editorCss, '.main-content:has(.editor-body.workspace-author) > .main-topbar')
    const editorBodyRule = cssRule(editorCss, '.editor-body.workspace-author')
    const actionsLayoutRule = cssRule(editorCss, '.editor-body.workspace-author .actions-layout')
    const authorCenterRule = cssRule(editorCss, '.editor-body.workspace-author .author-code-center')

    expect(editorCss).toContain('@media (min-width: 901px)')
    expect(mainContentRule).toContain('display: flex;')
    expect(mainContentRule).toContain('overflow-y: hidden;')
    expect(topbarRule).toContain('margin-bottom: 20px;')
    expect(editorBodyRule).toContain('flex: 1;')
    expect(editorBodyRule).toContain('min-height: 0;')
    expect(actionsLayoutRule).toContain('flex: 1;')
    expect(actionsLayoutRule).toContain('min-height: 0;')
    expect(actionsLayoutRule).toContain('margin: 0 -36px;')
    expect(authorCenterRule).toContain('max-height: none;')
  })

  it('lets the Source Mapping tab fill the center workbench frame', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const editorCss = readFileSync(`${cwd}/src/components/ScriptEditor.css`, 'utf8') as string
    const sourceMapReviewRule = cssRule(editorCss, '.imported-metadata-section .source-map-review')
    const sourceMapHeaderRule = cssRule(editorCss, '.imported-metadata-section .source-map-header')
    const sourceMapPanelsRule = cssRule(editorCss, '.imported-metadata-section .source-map-panels')

    expect(editorCss).toContain('.center-document.imported-metadata-section {\n  overflow: hidden;')
    expect(editorCss).toContain('  padding: 0;\n  border: 0;\n  border-radius: 0;')
    expect(sourceMapReviewRule).toContain('flex: 1 1 auto;')
    expect(sourceMapReviewRule).toContain('min-height: 0;')
    expect(sourceMapReviewRule).toContain('overflow: hidden;')
    expect(sourceMapReviewRule).toContain('border: 0;')
    expect(sourceMapHeaderRule).toContain('padding: 9px 10px;')
    expect(sourceMapHeaderRule).toContain('border-bottom: 1px solid var(--border);')
    expect(sourceMapPanelsRule).toContain('flex: 1 1 auto;')
    expect(sourceMapPanelsRule).toContain('height: auto;')
    expect(sourceMapPanelsRule).toContain('min-height: 0;')
    expect(sourceMapPanelsRule).toContain('max-height: none;')
  })

  it('keeps the landing editor focused on script name while support tools live in the outline', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(container.querySelector('.editor-support-toolbar')).toBeNull()
    const namePanel = container.querySelector('.side-panel-script-name')
    const actionListHeader = Array.from(container.querySelectorAll('.actions-sidebar-header'))
      .find(header => header.textContent?.includes('Action List'))
    expect(namePanel).toBeTruthy()
    expect(actionListHeader).toBeTruthy()
    expect(Boolean(namePanel!.compareDocumentPosition(actionListHeader!) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
    expect(namePanel?.textContent).toContain('Script Name')
    expect(namePanel?.textContent).not.toContain('Runtime Settings')
    expect(namePanel?.textContent).not.toContain('Replay Output')
    expect(container.querySelectorAll('input[placeholder="My_Test_Script"]')).toHaveLength(1)

    const outline = container.querySelector('.editor-outline-list')
    expect(outline?.textContent).toContain('Script Configuration')
    expect(outline?.textContent).toContain('Runtime Settings')
    expect(outline?.textContent).toContain('Parameter Data')
    expect(outline?.textContent).not.toContain('Replay Output')
    expect(outline?.textContent).not.toContain('Debug Session')

    const runtimeDialog = container.querySelector('[aria-label="Runtime Settings"]')
    expect(runtimeDialog?.className).toContain('center-document-runtime')
    expect(findCenterTab(container, 'Configuration')).toBeUndefined()
    expect(container.querySelector('[aria-label="Script Debugger"]')).toBeNull()
    expect(runtimeDialog?.className).not.toContain('open')

    const configLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Script Configuration'))
    expect(configLauncher).toBeDefined()
    if (!configLauncher) return

    await act(async () => {
      configLauncher.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const activeCenterTab = Array.from(container.querySelectorAll('.center-tab-button')).find(button => button.textContent?.trim() === 'Runtime Settings')
    expect(activeCenterTab?.getAttribute('aria-selected')).toBe('true')
    expect(runtimeDialog?.textContent).toContain('Script Configuration')

    // The tablist exposes only tabs: wrappers are presentational and the
    // mouse-only close control is hidden; Delete closes the focused tab.
    const strip = container.querySelector('.center-tab-strip')!
    expect(Array.from(strip.children).every(child => child.getAttribute('role') === 'presentation')).toBe(true)
    expect(strip.querySelector('.center-tab-close')?.getAttribute('aria-hidden')).toBe('true')
    expect(activeCenterTab?.getAttribute('aria-keyshortcuts')).toBe('Delete')
    await act(async () => {
      activeCenterTab?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Delete', bubbles: true }))
    })
    expect(Array.from(container.querySelectorAll('.center-tab-button')).some(button => button.textContent?.trim() === 'Runtime Settings')).toBe(false)
  })

  it('collapses outline lists and focuses parameter references in script JSON', async () => {
    const parameterizedScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        ...initialScript.actions[0],
        body: '{"username":"{{param.session_id}}","session":"{{param.session_id}}"}',
      }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={parameterizedScript} />)
    })

    const actionSection = Array.from(container.querySelectorAll('.outline-section'))
      .find(section => section.textContent?.includes('Action List'))
    const parameterSection = Array.from(container.querySelectorAll('.outline-section'))
      .find(section => section.textContent?.includes('Parameter List'))
    expect(actionSection).toBeDefined()
    expect(parameterSection).toBeDefined()
    expect(container.querySelector('.action-list')?.textContent).toContain('Login')
    expect(container.querySelector('.parameter-outline-list')?.textContent).toContain('session_id')

    const actionToggle = actionSection?.querySelector('.outline-section-toggle')
    await act(async () => {
      actionToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(actionToggle?.getAttribute('aria-expanded')).toBe('false')
    expect(container.querySelector('.action-list')).toBeNull()

    const parameterToggle = parameterSection?.querySelector('.outline-section-toggle')
    await act(async () => {
      parameterToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(parameterToggle?.getAttribute('aria-expanded')).toBe('false')
    expect(container.querySelector('.parameter-outline-list')).toBeNull()

    await act(async () => {
      parameterToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const parameterButton = Array.from(container.querySelectorAll('.parameter-outline-item'))
      .find(button => button.textContent?.includes('session_id'))
    await act(async () => {
      parameterButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(parameterButton?.classList.contains('selected')).toBe(true)
    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.script-code-title')?.textContent).toContain('Parameter: session_id (2 occurrences)')
    // Selecting a parameter opens the Parameter companion page showing its details + references.
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Parameter')
    const paramDetails = container.querySelector('.selected-parameter-details')
    expect(paramDetails).not.toBeNull()
    expect(paramDetails?.querySelector('h3')?.textContent).toContain('session_id')
    expect(paramDetails?.querySelector('input')).toBeNull()
    const refButtons = container.querySelectorAll('.selected-parameter-ref')
    expect(refButtons.length).toBeGreaterThan(0)

    codeMirrorDispatchMock.mockClear()
    const firstRef = refButtons[0] as HTMLButtonElement
    await act(async () => {
      firstRef.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(codeMirrorDispatchMock).toHaveBeenCalled()
  })

  it('shows authoring issue badges on init and teardown rows', async () => {
    const lifecycleScript: ScriptDefinition = {
      ...initialScript,
      init: [{ name: 'Warmup', type: 'GET', method: 'GET', target: '' }],
      actions: [{ ...initialScript.actions[0], name: 'Business_Login' }],
      teardown: [{ name: 'Cleanup', type: 'GET', method: 'GET', target: '' }],
    } as ScriptDefinition

    await act(async () => {
      root.render(<ScriptEditor initialScript={lifecycleScript} />)
    })

    const rows = Array.from(container.querySelectorAll<HTMLElement>('.action-item'))
    const initRow = rows.find(row => row.textContent?.includes('Init 1') && row.textContent?.includes('Warmup'))
    const teardownRow = rows.find(row => row.textContent?.includes('Teardown 1') && row.textContent?.includes('Cleanup'))

    expect(initRow?.querySelector('.action-signal-pill.error')?.textContent).toBe('1')
    expect(teardownRow?.querySelector('.action-signal-pill.error')?.textContent).toBe('1')
  })

  it('focuses init and teardown rows from Debug Problems', async () => {
    const lifecycleScript: ScriptDefinition = {
      ...initialScript,
      init: [{ name: 'Warmup', type: 'GET', method: 'GET', target: '' }],
      actions: [{ ...initialScript.actions[0], name: 'Business_Login' }],
      teardown: [{ name: 'Cleanup', type: 'GET', method: 'GET', target: '' }],
    } as ScriptDefinition

    await act(async () => {
      root.render(<ScriptEditor initialScript={lifecycleScript} />)
    })

    await act(async () => {
      findOpenDebuggerButton(container)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    const problemsTab = Array.from(container.querySelectorAll<HTMLButtonElement>('.debugger-bottom-tabs [role="tab"]'))
      .find(button => button.textContent?.startsWith('Problems'))
    await act(async () => {
      problemsTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const problemRows = Array.from(container.querySelectorAll<HTMLButtonElement>('.debugger-problem-row.action-problem'))
    expect(problemRows.some(row => row.textContent?.includes('Init 1') && row.textContent?.includes('Warmup'))).toBe(true)
    const teardownProblem = problemRows.find(row => row.textContent?.includes('Teardown 1') && row.textContent?.includes('Cleanup'))
    expect(teardownProblem).toBeDefined()

    await act(async () => {
      teardownProblem?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findWorkspaceTab(container, 'Author')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.action-item.lifecycle-action.active')?.textContent).toContain('Cleanup')
    expect(container.querySelector('.script-json-scope-bar')?.textContent).toContain('Selected Action')
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"name": "Cleanup"')
  })

  it('edits init and teardown actions from the structured details inspector', async () => {
    const lifecycleScript: ScriptDefinition = {
      ...initialScript,
      init: [{ name: 'Warmup', type: 'GET', method: 'GET', target: '/warmup' }],
      actions: [{ ...initialScript.actions[0], name: 'Business_Login' }],
      teardown: [{ name: 'Cleanup', type: 'GET', method: 'GET', target: '/logout' }],
    } as ScriptDefinition

    await act(async () => {
      root.render(<ScriptEditor initialScript={lifecycleScript} />)
    })

    const warmupRow = Array.from(container.querySelectorAll<HTMLElement>('.action-item'))
      .find(row => row.textContent?.includes('Init 1') && row.textContent?.includes('Warmup'))
    await act(async () => {
      warmupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Action context')
    const warmupName = Array.from(container.querySelectorAll<HTMLInputElement>('.action-editor-panel input'))
      .find(input => input.value === 'Warmup')
    const warmupTarget = Array.from(container.querySelectorAll<HTMLInputElement>('.action-editor-panel input'))
      .find(input => input.value === '/warmup')
    expect(warmupName).toBeDefined()
    expect(warmupTarget).toBeDefined()

    await act(async () => {
      changeInputValue(warmupName!, 'Warmup_Edited')
      changeInputValue(warmupTarget!, '/healthz')
    })
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"name": "Warmup_Edited"')
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"target": "/healthz"')

    const cleanupRow = Array.from(container.querySelectorAll<HTMLElement>('.action-item'))
      .find(row => row.textContent?.includes('Teardown 1') && row.textContent?.includes('Cleanup'))
    await act(async () => {
      cleanupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const cleanupTarget = Array.from(container.querySelectorAll<HTMLInputElement>('.action-editor-panel input'))
      .find(input => input.value === '/logout')
    expect(cleanupTarget).toBeDefined()
    await act(async () => {
      changeInputValue(cleanupTarget!, '/signed-out')
    })
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"target": "/signed-out"')
  })

  it('keeps the same action selected after deleting an earlier action', async () => {
    const multiActionScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        { ...initialScript.actions[0], name: 'Open Cart', target: '/cart' },
        { ...initialScript.actions[0], name: 'Apply Coupon', target: '/coupon' },
        { ...initialScript.actions[0], name: 'Submit Order', target: '/submit' },
        { ...initialScript.actions[0], name: 'Complete Checkout', target: '/done' },
      ],
    } as ScriptDefinition

    await act(async () => {
      root.render(<ScriptEditor initialScript={multiActionScript} />)
    })

    const actionRows = () => Array.from(container.querySelectorAll<HTMLElement>('.action-item'))
    const completeCheckout = () => actionRows().find(row => row.textContent?.includes('Complete Checkout'))
    await act(async () => {
      completeCheckout()?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.action-item.active')?.textContent).toContain('Complete Checkout')

    const openCart = actionRows().find(row => row.textContent?.includes('Open Cart'))
    await act(async () => {
      openCart?.querySelector<HTMLButtonElement>('[aria-label="Delete action"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(actionRows().some(row => row.textContent?.includes('Open Cart'))).toBe(false)
    expect(container.querySelector('.action-item.active')?.textContent).toContain('Complete Checkout')
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('.author-main-action .action-editor-panel input'))
      .some(input => input.value === 'Complete Checkout')).toBe(true)
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"name": "Complete Checkout"')
  })

  it('moves, duplicates, and deletes lifecycle actions from the author list', async () => {
    const lifecycleScript: ScriptDefinition = {
      ...initialScript,
      init: [
        { name: 'Warmup_A', type: 'GET', method: 'GET', target: '/a' },
        { name: 'Warmup_B', type: 'GET', method: 'GET', target: '/b' },
      ],
      actions: [{ ...initialScript.actions[0], name: 'Business_Login' }],
      teardown: [
        { name: 'Cleanup_A', type: 'GET', method: 'GET', target: '/logout-a' },
        { name: 'Cleanup_B', type: 'GET', method: 'GET', target: '/logout-b' },
      ],
    } as ScriptDefinition

    await act(async () => {
      root.render(<ScriptEditor initialScript={lifecycleScript} />)
    })

    const lifecycleRows = () => Array.from(container.querySelectorAll<HTMLElement>('.action-item.lifecycle-action'))
    const initA = () => lifecycleRows().find(row => row.textContent?.includes('Init 1') && row.textContent?.includes('Warmup_A')) as HTMLElement | undefined
    const initCopy = () => lifecycleRows().find(row => row.textContent?.includes('Warmup_A (copy)')) as HTMLElement | undefined
    const cleanupA = () => lifecycleRows().find(row => row.textContent?.includes('Cleanup_A')) as HTMLElement | undefined

    await act(async () => {
      initA()?.querySelector<HTMLButtonElement>('[aria-label="Duplicate init action"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(initCopy()).toBeDefined()

    await act(async () => {
      initCopy()?.querySelector<HTMLButtonElement>('[aria-label="Move init action up"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(lifecycleRows().find(row => row.textContent?.includes('Init 1'))?.textContent).toContain('Warmup_A (copy)')

    await act(async () => {
      cleanupA()?.querySelector<HTMLButtonElement>('[aria-label="Delete teardown action"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(lifecycleRows().some(row => row.textContent?.includes('Cleanup_A'))).toBe(false)
    expect(lifecycleRows().some(row => row.textContent?.includes('Cleanup_B'))).toBe(true)
  })

  it('remaps lifecycle breakpoints when lifecycle actions move and delete', async () => {
    const lifecycleScript: ScriptDefinition = {
      ...initialScript,
      init: [{ name: 'Warmup', type: 'GET', method: 'GET', target: '/warmup' }],
      actions: [{ ...initialScript.actions[0], name: 'Business_Login' }],
      teardown: [
        { name: 'Cleanup_A', type: 'GET', method: 'GET', target: '/logout-a' },
        { name: 'Cleanup_B', type: 'GET', method: 'GET', target: '/logout-b' },
      ],
    } as ScriptDefinition

    await act(async () => {
      root.render(<ScriptEditor initialScript={lifecycleScript} />)
    })

    const lifecycleRows = () => Array.from(container.querySelectorAll<HTMLElement>('.action-item.lifecycle-action'))
    const cleanupB = () => lifecycleRows().find(row => row.textContent?.includes('Cleanup_B')) as HTMLElement | undefined

    await act(async () => {
      cleanupB()?.querySelector<HTMLButtonElement>('[aria-label="Set breakpoint on Teardown 2"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(cleanupB()?.querySelector('.action-breakpoint-button')?.className).toContain('active')

    await act(async () => {
      cleanupB()?.querySelector<HTMLButtonElement>('[aria-label="Move teardown action up"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const movedCleanup = lifecycleRows().find(row => row.textContent?.includes('Teardown 1') && row.textContent?.includes('Cleanup_B'))
    expect(movedCleanup?.querySelector('.action-breakpoint-button')?.className).toContain('active')

    await act(async () => {
      movedCleanup?.querySelector<HTMLButtonElement>('[aria-label="Delete teardown action"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(lifecycleRows().some(row => row.querySelector('.action-breakpoint-button.active'))).toBe(false)
  })

  it('expands an http_sequence row in the debug rail to reveal sub-requests', async () => {
    const sequenceScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        name: 'Checkout_Txn',
        type: 'http_sequence',
        method: 'SEQUENCE',
        target: '/aura',
        params: {
          requests: [
            { name: 'Sub_One', type: 'POST', method: 'POST', target: '/one' },
            { name: 'Sub_Two', type: 'GET', method: 'GET', target: '/two' },
          ],
        },
      }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={sequenceScript} />)
    })
    const debugTab = findOpenDebuggerButton(container)
    await act(async () => {
      debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const railList = container.querySelector('.debugger-rail-list')
    expect(railList?.textContent).toContain('Checkout_Txn')
    // Collapsed by default — no child rows until expanded.
    expect(container.querySelectorAll('.debugger-action-row.child')).toHaveLength(0)

    const parentMain = container.querySelector('.debugger-action-row.container .debugger-action-row-main') as HTMLButtonElement | null
    expect(parentMain).not.toBeNull()
    await act(async () => {
      parentMain?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const childRows = container.querySelectorAll('.debugger-action-row.child')
    expect(childRows).toHaveLength(2)
    expect(childRows[0].textContent).toContain('Sub_One')
    expect(childRows[0].textContent).toContain('Request 1')
    expect(childRows[1].textContent).toContain('Sub_Two')
  })

  it('renders control_loop body steps as breakable sub-step rows', async () => {
    const loopScript = {
      ...initialScript,
      actions: [{
        name: 'Retry_Block', type: 'control_loop',
        control: { count: 2, steps: [{ name: 'Poll', type: 'GET', method: 'GET', target: '/poll' }] },
      }],
    } as unknown as ScriptDefinition
    await act(async () => { root.render(<ScriptEditor initialScript={loopScript} />) })
    const debugTab = findOpenDebuggerButton(container)
    await act(async () => { debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true })); await Promise.resolve() })

    const containerRow = container.querySelector('.debugger-action-row.container')
    expect(containerRow?.textContent).toContain('LOOP')
    expect(container.querySelectorAll('.debugger-action-row.child')).toHaveLength(0)

    const main = container.querySelector('.debugger-action-row.container .debugger-action-row-main') as HTMLButtonElement | null
    await act(async () => { main?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const childRows = container.querySelectorAll('.debugger-action-row.child')
    expect(childRows).toHaveLength(1)
    expect(childRows[0].textContent).toContain('Poll')
    expect(childRows[0].textContent).toContain('Loop step 1')

    const gutter = childRows[0].querySelector('.debugger-gutter') as HTMLButtonElement | null
    await act(async () => { gutter?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect((container.querySelector('.debugger-action-row.child .debugger-gutter') as HTMLElement).className).toContain('active')
    expect((container.querySelector('.debugger-action-row.container .debugger-gutter') as HTMLElement).className).toContain('partial')
  })

  it('sets a sub-request breakpoint on an http_sequence child via its gutter', async () => {
    const sequenceScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        name: 'Checkout_Txn', type: 'http_sequence', method: 'SEQUENCE', target: '/aura',
        params: { requests: [
          { name: 'Sub_One', type: 'POST', method: 'POST', target: '/one' },
          { name: 'Sub_Two', type: 'GET', method: 'GET', target: '/two' },
        ] },
      }],
    }
    await act(async () => { root.render(<ScriptEditor initialScript={sequenceScript} />) })
    const debugTab = findOpenDebuggerButton(container)
    await act(async () => { debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true })); await Promise.resolve() })

    const parentMain = container.querySelector('.debugger-action-row.container .debugger-action-row-main') as HTMLButtonElement | null
    await act(async () => { parentMain?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const childGutter = container.querySelector('.debugger-action-row.child .debugger-gutter') as HTMLButtonElement | null
    expect(childGutter).not.toBeNull()
    await act(async () => { childGutter?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    // The clicked child gutter becomes active, and the parent shows the partial
    // (sub-request breakpoints, no whole-sequence breakpoint) state.
    expect((container.querySelector('.debugger-action-row.child .debugger-gutter') as HTMLElement).className).toContain('active')
    expect((container.querySelector('.debugger-action-row.container .debugger-gutter') as HTMLElement).className).toContain('partial')
  })

  it('adds collapsible action and parameter lists to the debug rail', async () => {
    const parameterizedScript: ScriptDefinition = {
      ...initialScript,
      actions: [{
        ...initialScript.actions[0],
        body: '{"username":"{{param.session_id}}","session":"{{param.session_id}}"}',
      }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={parameterizedScript} />)
    })

    const debugTab = findOpenDebuggerButton(container)
    await act(async () => {
      debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const debugRail = container.querySelector('.debugger-rail')
    expect(debugRail?.textContent).toContain('Action List')
    expect(debugRail?.textContent).toContain('Parameter List')
    const debugNamePanel = debugRail?.querySelector('.side-panel-script-name')
    const debugActionHeader = Array.from(debugRail?.querySelectorAll('.debugger-rail-header') || [])
      .find(header => header.textContent?.includes('Action List'))
    expect(debugNamePanel).toBeNull() // Rename remains in Author; Debug keeps the action rail focused.
    expect(debugActionHeader).toBeTruthy()
    expect(debugRail?.querySelector('.debugger-rail-list')?.textContent).toContain('Login')
    expect(debugRail?.querySelector('.debugger-parameter-list')?.textContent).toContain('session_id')

    const debugToggles = debugRail?.querySelectorAll('.debugger-rail-section-toggle')
    expect(debugToggles).toHaveLength(2)

    await act(async () => {
      debugToggles?.[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(debugToggles?.[0]?.getAttribute('aria-expanded')).toBe('false')
    expect(debugRail?.querySelector('.debugger-rail-list')).toBeNull()
    expect(debugRail?.querySelector('.debugger-parameter-list')).not.toBeNull()

    await act(async () => {
      debugToggles?.[1]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(debugToggles?.[1]?.getAttribute('aria-expanded')).toBe('false')
    expect(debugRail?.querySelector('.debugger-parameter-list')).toBeNull()

    await act(async () => {
      debugToggles?.[1]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const parameterButton = Array.from(container.querySelectorAll('.debugger-parameter-item'))
      .find(button => button.textContent?.includes('session_id'))
    await act(async () => {
      parameterButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(parameterButton?.classList.contains('selected')).toBe(true)
    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.script-code-title')?.textContent).toContain('Parameter: session_id (2 occurrences)')
  })

  it('keeps Action List first while preserving the Source pane in Author and Debug workbench modes', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(findWorkspaceTab(container, '{ } JSON')).toBeUndefined()
    expect(findWorkspaceTab(container, 'JSON')).toBeUndefined()
    expect(findCenterTab(container, 'Action editor')?.getAttribute('aria-selected')).toBe('true')
    expect(findCenterTab(container, 'Source')).not.toBeUndefined()
    await act(async () => {
      findCenterTab(container, 'Source')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('button[aria-label="Close Source"]')).toBeNull()
    expect(container.querySelector('[aria-label="Source"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Source"]')?.textContent).toContain('Synced')
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"Checkout Flow"')

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) return

    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('[aria-label="Script Debugger"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Source"]')).not.toBeNull()
    expect(findCenterTab(container, 'Source')).not.toBeUndefined()
    expect(findCenterTab(container, 'Browser Replay')).toBeUndefined()
    expect((container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null)?.value).toContain('"Checkout Flow"')
  })

  it('reports a recording-status request failure instead of treating it as an absent baseline', async () => {
    apiMock.getScriptRecordingStatus.mockRejectedValueOnce(new Error('status unavailable'))
    apiMock.startScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: false,
      events: [{
        phase: 'action',
        action_index: 0,
        action_name: 'Login',
        action_type: 'POST',
        target: '/login',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        duration_ms: 1000,
        success: false,
      }],
    }))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushReact()

    const runButton = findDebuggerRunButton(container)
    expect(runButton).toBeDefined()
    await act(async () => {
      runButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushReact()

    expect(container.textContent).toContain('Could not check whether this script has a stored baseline')
    expect(container.textContent).not.toContain('No stored baseline for this script')
  })

  it('keeps the combined import review tab out of the Debug workbench', async () => {
    const importedScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          source_script_name: 'checkout.usr',
          preserved: {
            source_review: {
              source_files: [
                {
                  path: 'Action.c',
                  language: 'c',
                  content: 'Action()\\n{\\n\\tweb_url("Login", "URL=https://example.com/login", LAST);\\n}',
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
              ],
            },
          },
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} />)
    })

    const importedTab = findCenterTab(container, 'Source Mapping')
    expect(importedTab).toBeDefined()
    // Metadata JSON is an on-demand outline dialog, not a center tab.
    expect(findCenterTab(container, 'Metadata JSON')).toBeUndefined()
    expect(findCenterTab(container, 'Conversion Mapping')).toBeUndefined()

    await act(async () => {
      importedTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(findCenterTab(container, 'Source Mapping')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('#imported-metadata-section')).not.toBeNull()

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) return

    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const debugCenter = container.querySelector('.debugger-code-center')
    expect(debugCenter).not.toBeNull()
    expect(findCenterTab(container, 'Action evidence')?.getAttribute('aria-selected')).toBe('true')
    expect(findCenterTab(debugCenter || container, 'Source Mapping')).toBeUndefined()
    expect(findCenterTab(debugCenter || container, 'Metadata JSON')).toBeUndefined()
    expect(findCenterTab(debugCenter || container, 'Conversion Mapping')).toBeUndefined()
    expect(debugCenter?.textContent).not.toContain('Source Mapping')
    expect(debugCenter?.textContent).not.toContain('Metadata JSON')
    expect(debugCenter?.textContent).not.toContain('Conversion Mapping')
    expect(container.querySelector('#imported-metadata-section')).toBeNull()

    const authorTab = findWorkspaceTab(container, 'Author')
    expect(authorTab).toBeDefined()
    await act(async () => {
      authorTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(findCenterTab(container, 'Source Mapping')).toBeDefined()
    expect(findCenterTab(container, 'Metadata JSON')).toBeUndefined()
    expect(findCenterTab(container, 'Conversion Mapping')).toBeUndefined()
    expect(findCenterTab(container, 'Source Mapping')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('#imported-metadata-section')).not.toBeNull()
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
    expect(container.querySelector('.script-browser-evidence')).not.toBeNull()
    expect(container.textContent).toContain('Browser evidence')
    expect(container.textContent).toContain('Debug one iteration to capture')
  })

  it('renders a dedicated http_oauth2_token param editor', async () => {
    const oauthScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'http',
      actions: [
        {
          name: 'Get Token',
          type: 'http_oauth2_token',
          target: '',
          params: {
            grant_type: 'jwt_bearer',
            token_url: 'https://login.salesforce.com/services/oauth2/token',
            save_as: 'access_token',
          },
        },
        { name: 'List Orders', type: 'GET', method: 'GET', target: '/orders' },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={oauthScript} />)
    })

    const tokenRow = Array.from(container.querySelectorAll('.action-item')).find(row => row.textContent?.includes('Get Token'))
    expect(tokenRow).toBeDefined()
    if (!tokenRow) return
    await act(async () => {
      tokenRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Token URL')
    expect(container.textContent).toContain('Grant Type')
    expect(container.textContent).toContain('Save As')
    expect(container.textContent).toContain('Private Key')
    const grantSelect = Array.from(container.querySelectorAll('select')).find(select => Array.from(select.options).some(option => option.value === 'jwt_bearer'))
    expect(grantSelect).toBeDefined()
    expect(grantSelect?.value).toBe('jwt_bearer')
  })

  it('surfaces TruClient2 review gates, selector fallbacks, and control actions in the browser editor', async () => {
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
          name: 'Custom JS',
          type: 'browser_evaluate_js',
          target: 'window.checkoutReady === true',
          selector: '',
          metadata: {
            review_required: true,
            review_gate: 'javascript',
            promotion_blocker: true,
            source_selector_fallbacks: [{ kind: 'css', value: '#checkout-ready', confidence: 80 }],
          },
        },
        {
          name: 'Ready Branch',
          type: 'control_if',
          target: '',
          control: {
            condition: { left: '{{var.checkout_ready}}', op: 'eq', right: 'true' },
            steps: [{ name: 'Hover Pay', type: 'browser_hover', target: '#pay' }],
            else: [{ name: 'Wait Pay', type: 'browser_wait', target: '#pay' }],
          },
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} />)
    })

    const rows = Array.from(container.querySelectorAll('.action-item'))
    const jsRow = rows.find(row => row.textContent?.includes('Custom JS'))
    const controlRow = rows.find(row => row.textContent?.includes('Ready Branch'))
    expect(jsRow?.textContent).toContain('Review')
    expect(jsRow?.textContent).toContain('1 fb')
    expect(controlRow?.textContent).toContain('ctrl')

    if (!jsRow) return
    await act(async () => {
      jsRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('fallbacks 1')
    const actionTypeSelect = Array.from(container.querySelectorAll('select')).find(select => Array.from(select.options).some(option => option.value === 'browser_hover'))
    expect(actionTypeSelect).toBeDefined()
    expect(Array.from(actionTypeSelect?.options || []).map(option => option.value)).toEqual(expect.arrayContaining([
      'browser_hover',
      'browser_drag_and_drop',
      'browser_set_files',
      'browser_press_key',
      'browser_evaluate_js',
      'browser_switch_frame',
      'browser_switch_window',
      'control_if',
      'control_loop',
    ]))

    const configButton = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(configButton).toBeDefined()
    if (!configButton) return
    await act(async () => {
      configButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const transportToggle = Array.from(container.querySelectorAll('.section-toggle')).find(button => button.textContent?.includes('HTTP Transport'))
    expect(transportToggle).toBeDefined()
    if (!transportToggle) return
    await act(async () => {
      transportToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const browserEmulationTab = Array.from(container.querySelectorAll('.http-transport-tabs [role="tab"]')).find(button =>
      button.textContent?.includes('Browser Emulation'),
    )
    expect(browserEmulationTab).toBeDefined()
    if (!browserEmulationTab) return
    await act(async () => {
      browserEmulationTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Allow reviewed JavaScript')
    expect(container.textContent).toContain('Allow review-required actions')
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
          source: 'enterprise_script_archive',
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
      'Init 1',
      'Action 1',
      'Teardown 1',
    ])

    const initRow = Array.from(container.querySelectorAll('.action-item')).find(row => row.textContent?.includes('Init Login'))
    expect(initRow).toBeDefined()
    await act(async () => {
      initRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Action context')
    expect(container.querySelector('.author-main-action .action-editor-panel')?.textContent).toContain('Init Login')

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
          source: 'enterprise_script_archive',
          source_script_name: 'checkout.usr',
          preserved: {},
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} />)
    })

    const closeImported = findButtonByText(container, '×')
    if (closeImported?.getAttribute('aria-label') === 'Close Source Mapping') {
      await act(async () => {
        closeImported.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }

    const configLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Script Configuration'))
    expect(configLauncher).toBeDefined()
    await act(async () => {
      configLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Configuration')).toBeUndefined()
    expect(findCenterTab(container, 'Runtime Settings')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('[aria-label="Runtime Settings"]')?.textContent).toContain('Script Configuration')

    const runtimeLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeLauncher).toBeDefined()
    await act(async () => {
      runtimeLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Runtime Settings')?.getAttribute('aria-selected')).toBe('true')

    const importedLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Source Mapping'))
    expect(importedLauncher).toBeDefined()
    await act(async () => {
      importedLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Source Mapping')?.getAttribute('aria-selected')).toBe('true')

    const parameterLauncher = Array.from(container.querySelectorAll('.editor-outline-link')).find(button => button.textContent?.includes('Parameter Data'))
    expect(parameterLauncher).toBeDefined()
    await act(async () => {
      parameterLauncher?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Parameter Data')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('[aria-label="Parameter Data"]')?.textContent).toContain('Parameter: session_id')
    expect(container.querySelector('[aria-label="Parameter Data"]')?.textContent).toContain('{{param.session_id}}')
  })

  it('only exposes the Source Mapping and Metadata JSON center tabs for scripts with actual import metadata', async () => {
    const staleImportShellScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {},
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={staleImportShellScript} />)
    })

    expect(findCenterTab(container, 'Source Mapping')).toBeUndefined()
    expect(findCenterTab(container, 'Metadata JSON')).toBeUndefined()
    expect(container.querySelector('#imported-metadata-section')).toBeNull()
    expect(container.querySelector('#metadata-json-section')).toBeNull()
    expect(container.querySelector('.editor-outline-nav')?.textContent).not.toContain('Source Mapping')
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

    await act(async () => { findCenterTab(container, 'Source')?.click() })

    // The merged detail panel (request/response + variables) is open by default.
    expect(container.querySelector('.debugger-center')).not.toBeNull()
    expect(container.querySelector('.debugger-shell')?.classList.contains('debugger-detail-collapsed')).toBe(false)

    const closeDetails = findButtonByTitle(container, 'Close debug detail panel')
    expect(closeDetails).toBeDefined()
    await act(async () => {
      closeDetails?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.debugger-center')).toBeNull()
    expect(container.querySelector('.debugger-shell')?.classList.contains('debugger-detail-collapsed')).toBe(true)
    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')

    const showDetails = findButtonByTitle(container, 'Show detail panel (request/response + variables)')
    expect(showDetails).toBeDefined()
    await act(async () => {
      showDetails?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.debugger-center')).not.toBeNull()
  })

  it('collapses and restores the right sidebars from Author and Debug modes', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    expect(container.querySelector('.editor-utility-sidebar')).not.toBeNull()
    expect(Array.from(container.querySelectorAll('.debugger-icon-btn')).some(button => button.textContent?.trim() === 'Map')).toBe(false)
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Insights')
    const collapseAuthorSidebar = container.querySelector('.editor-utility-sidebar button[aria-label="Collapse right sidebar"]') as HTMLButtonElement | null
    expect(collapseAuthorSidebar).not.toBeNull()
    await act(async () => {
      collapseAuthorSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const collapsedAuthorSidebar = container.querySelector('.editor-utility-sidebar')
    expect(collapsedAuthorSidebar).not.toBeNull()
    expect(collapsedAuthorSidebar?.classList.contains('collapsed')).toBe(true)
    expect(findButtonByTitle(container, 'Show right sidebar')).toBeUndefined()
    const expandAuthorSidebar = findButtonByTitle(container, 'Expand right sidebar')
    expect(expandAuthorSidebar).toBeDefined()
    await act(async () => {
      expandAuthorSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.editor-utility-sidebar')).not.toBeNull()
    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('collapsed')).toBe(false)

    const debugTab = findOpenDebuggerButton(container)
    await act(async () => {
      debugTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    await act(async () => { findCenterTab(container, 'Source')?.click() })

    // Debug detail panel (request/response + variables tabs) is open by default.
    expect(container.querySelector('.debugger-center')).not.toBeNull()
    expect(Array.from(container.querySelectorAll('.debugger-center-tablist button')).some(button => button.textContent?.trim() === 'Variables')).toBe(true)

    const collapseDebugSidebar = container.querySelector('.debugger-center button[aria-label="Close debug detail panel"]') as HTMLButtonElement | null
    expect(collapseDebugSidebar).not.toBeNull()
    await act(async () => {
      collapseDebugSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.debugger-center')).toBeNull()
    expect(container.querySelector('.debugger-shell')?.classList.contains('debugger-detail-collapsed')).toBe(true)

    const showDebugSidebar = findButtonByTitle(container, 'Show detail panel (request/response + variables)')
    expect(showDebugSidebar).toBeDefined()
    await act(async () => {
      showDebugSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.debugger-center')).not.toBeNull()
  })

  it('opens action details in the Script JSON right sidebar without exposing the minimap', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const collapseAuthorSidebar = container.querySelector('.editor-utility-sidebar button[aria-label="Collapse right sidebar"]') as HTMLButtonElement | null
    expect(collapseAuthorSidebar).not.toBeNull()
    await act(async () => {
      collapseAuthorSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('collapsed')).toBe(true)
    expect(container.querySelector('.utility-sidebar-restore')?.textContent).toContain('Insights')

    const firstAction = container.querySelector('.action-item')
    await act(async () => {
      firstAction?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Action editor')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('collapsed')).toBe(false)
    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('context-inspector')).toBe(true)
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Action context')
    expect(container.querySelector('.author-main-action .action-editor-panel')).not.toBeNull()
    expect(container.querySelector('.editor-utility-sidebar .action-editor-panel')).toBeNull()
    expect(container.querySelector('.script-code-inline-inspector .action-editor-panel')).toBeNull()
    expect(Array.from(container.querySelectorAll('.script-json-sidebar-tab')).map(button => button.textContent?.trim())).toEqual([])
    expect(container.querySelector('.json-source-map')).toBeNull()
  })

  it('keeps the Script JSON minimap hidden from the dashboard UI', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })
    await act(async () => {
      findCenterTab(container, 'Source')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushReact()

    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('context-signals')).toBe(true)
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Insights')
    expect(Array.from(container.querySelectorAll('.script-json-sidebar-tab')).map(button => button.textContent?.trim())).toEqual([
      'Insights',
      'Parameters',
    ])
    expect(container.querySelector('.json-source-map')).toBeNull()
    expect(container.querySelector('.json-source-map-scroll-thumb')).toBeNull()
    expect(container.querySelector('.json-source-map-line')).toBeNull()
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
      'Action editor',
      'Source',
      'Parameter Data',
      'Runtime Settings',
    ])
    expect(findCenterTab(container, 'Configuration')).toBeUndefined()

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

    expect(findCenterTab(container, 'Source Mapping')).toBeUndefined()
    expect(findCenterTab(container, 'Metadata JSON')).toBeUndefined()
    expect(findCenterTab(container, 'Action editor')?.getAttribute('aria-selected')).toBe('true')
  })

  it('applies valid manual JSON edits into the structured authoring tools and save payload', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findCenterTab(container, 'Source')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    // The raw JSON pane is a read-only mirror by default; enable direct editing
    // first, which reveals the Apply JSON / Reset / Format controls.
    const editJsonButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit JSON')
    expect(editJsonButton).toBeDefined()
    if (!editJsonButton) return
    await act(async () => {
      editJsonButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
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

    const applyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Apply changes')
    expect(applyButton).toBeDefined()
    if (!applyButton) return

    await act(async () => {
      applyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect((container.querySelector('input[placeholder="My_Test_Script"]') as HTMLInputElement | null)?.value).toBe('Checkout JSON Draft')
    expect(container.querySelector('.action-list')?.textContent).toContain('Get Cart')

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    expect(saveButton.disabled).toBe(false)
    expect((saveButton.getAttribute('data-tooltip') || saveButton.getAttribute('title'))).toBe('Save script')

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

    const configTab = findCenterTab(container, 'Runtime Settings')
    expect(configTab).toBeDefined()
    await act(async () => {
      configTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.center-document-blocked')?.textContent).toContain('Script JSON is invalid')
    expect(container.querySelector('.center-document-blocked')?.textContent).toContain('Structured editing is paused until the Source JSON parses again.')

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    expect(saveButton.disabled).toBe(true)
    expect((saveButton.getAttribute('data-tooltip') || saveButton.getAttribute('title'))).toBe('Fix invalid Script JSON before saving')

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.querySelector('.script-code-status.invalid')?.textContent).toContain('Invalid JSON')
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
          source: 'enterprise_script_archive',
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
    replaySessionIdMock.mockReturnValueOnce('browser-replay-1')
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'browser-replay-1',
      session: {
          ...unavailableRuntimeMetadata('browser-replay-1', 'completed', '3'),
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
          source: 'enterprise_script_archive',
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
    await flushReact()

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
	expect(container.textContent).toContain('Promotion Blockers')
	expect(container.textContent).toContain('Correlation evidence is not durable.')
	expect(container.textContent).toContain('correlation evidence required')
    const correlationCard = Array.from(container.querySelectorAll('.import-correlation-card'))
      .find(card => card.textContent?.includes('Correlation · csrf_token'))
    expect(correlationCard?.querySelector('code')).toBeNull()
    expect(container.textContent).toContain('browser_canary: browser / 1 VUsers')

	await act(async () => {
	  root.render(<ScriptEditor initialScript={importedWeb2Script} projectId="project-2" />)
	  await Promise.resolve()
	})
	expect(container.textContent).not.toContain('Generated HTTP Draft Preview')
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
    const configPanel = container.querySelector('[aria-label="Runtime Settings"]')
    expect(configPanel?.textContent).toContain('Protocol')
    expect(configPanel?.textContent).toContain('HTTP API')
    expect(configPanel?.querySelector('select[aria-label="Protocol"]')).toBeNull()
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

    const firstAction = container.querySelector('.action-item')
    await act(async () => {
      firstAction?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Action editor')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.author-main-action .action-editor-panel')).not.toBeNull()
    expect(container.querySelector('.editor-utility-sidebar .action-editor-panel')).toBeNull()
  })

  it('keeps existing parameter type read-only in the parameter data settings', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
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

    const dialog = container.querySelector('[aria-label="Parameter Data"]')
    const typeSelect = dialog?.querySelector('select[aria-label="Parameter Type for session_id"]')
    const readonlyType = dialog?.querySelector('.parameter-type-readonly[aria-label="Parameter Type for session_id: Counter"]')

    expect(typeSelect).toBeNull()
    expect(readonlyType).not.toBeNull()
    expect(readonlyType?.textContent).toContain('Counter')
    expect(readonlyType?.textContent).toContain('counter')
    expect(container.textContent).toContain('Identity')
    expect(container.textContent).toContain('Type Options')
    expect((dialog?.querySelector('input[placeholder="start"]') as HTMLInputElement | null)?.value).toBe('100')
    expect((dialog?.querySelector('input[placeholder="step"]') as HTMLInputElement | null)?.value).toBe('1')
    expect(dialog?.querySelector('select[aria-label="Update Cadence for session_id"]')).not.toBeNull()
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
    await act(async () => {
      findCenterTab(container, 'Source')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')

    const parameterSection = Array.from(container.querySelectorAll('.outline-section'))
      .find(section => section.textContent?.includes('Parameter List'))
    const activePanel = container.querySelector('.center-tab-panel.active') as HTMLDivElement | null
    const autoButton = Array.from(parameterSection?.querySelectorAll('button') || []).find(button =>
      button.textContent?.trim() === 'Auto Params',
    )
    expect(activePanel?.textContent).not.toContain('Auto Parameterization')
    expect(parameterSection?.contains(autoButton || null)).toBe(true)
    expect(autoButton).toBeDefined()
    if (!autoButton) {
      return
    }

    await act(async () => {
      autoButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const utilitySidebar = container.querySelector('.editor-utility-sidebar') as HTMLDivElement | null
    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')
    expect(utilitySidebar?.classList.contains('context-auto-parameterization')).toBe(true)
    expect(utilitySidebar?.querySelector('.side-pane-header')?.textContent).toContain('Parameters')
    expect(activePanel?.textContent).not.toContain('Review Candidates')
    expect(utilitySidebar?.textContent).toContain('Suggested Fields')
    expect(utilitySidebar?.textContent).toContain('username')
    expect(utilitySidebar?.textContent).toContain('demo')

    const insightsTab = Array.from(utilitySidebar?.querySelectorAll('.script-json-sidebar-tab') || []).find(button => button.textContent?.trim() === 'Insights')
    expect(insightsTab).toBeDefined()
    expect(Array.from(utilitySidebar?.querySelectorAll('.script-json-sidebar-tab') || []).map(button => button.textContent?.trim())).not.toContain('Map')
    await act(async () => {
      insightsTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('context-signals')).toBe(true)
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Insights')
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).not.toContain('Suggested Fields')

    const autoParamsTab = Array.from(container.querySelectorAll('.script-json-sidebar-tab')).find(button => button.textContent?.trim() === 'Parameters')
    expect(autoParamsTab).toBeDefined()
    await act(async () => {
      autoParamsTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('context-auto-parameterization')).toBe(true)
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).toContain('Suggested Fields')

    const nameInput = utilitySidebar?.querySelector<HTMLInputElement>('.auto-parameter-candidate.active .auto-parameter-name-field input')
    expect(nameInput?.value).toBe('username')
    if (!nameInput) {
      return
    }

    await act(async () => {
      changeInputValue(nameInput, 'login_user')
    })

    const applyButton = Array.from(utilitySidebar?.querySelectorAll('button') || []).find(button =>
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
    expect((container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null)?.value).toContain('{{data.auto_params.login_user}}')
  })

  it('navigates auto parameterization suggestions across target, header, and body locations', async () => {
    const multiFieldScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          target: '/login?username=demo&username=demo',
          headers: {
            'Content-Type': 'application/json',
            'X-Customer': 'customer_12345',
          },
          body: '{"cart_id":"cart_12345"}',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={multiFieldScript} />)
    })

    const autoButton = Array.from(container.querySelectorAll('.parameter-header-actions button')).find(button =>
      button.textContent?.trim() === 'Auto Params',
    )
    expect(autoButton).toBeDefined()
    if (!autoButton) {
      return
    }

    await act(async () => {
      autoButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const utilitySidebar = container.querySelector('.editor-utility-sidebar') as HTMLDivElement | null
    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')
    expect(utilitySidebar?.querySelector('.auto-parameter-location')?.textContent).toContain('1 / 2')
    expect(utilitySidebar?.querySelector('.auto-parameter-location')?.textContent).toContain('Action 1 / target')

    const nextButton = Array.from(utilitySidebar?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'Next')
    expect(nextButton).toBeDefined()
    if (!nextButton) {
      return
    }

    await act(async () => {
      nextButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(utilitySidebar?.querySelector('.auto-parameter-location')?.textContent).toContain('2 / 2')
    expect(utilitySidebar?.querySelector('.auto-parameter-location')?.textContent).toContain('Action 1 / target')

    const backButton = Array.from(utilitySidebar?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'Back')
    expect(backButton).toBeDefined()
    if (!backButton) {
      return
    }

    await act(async () => {
      backButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(utilitySidebar?.querySelector('.auto-parameter-location')?.textContent).toContain('1 / 2')

    const customerCandidate = Array.from(utilitySidebar?.querySelectorAll<HTMLButtonElement>('.auto-parameter-candidate-select') || [])
      .find(button => button.textContent?.includes('X-Customer'))
    expect(customerCandidate).toBeDefined()
    if (!customerCandidate) {
      return
    }

    await act(async () => {
      customerCandidate.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(utilitySidebar?.querySelector('.auto-parameter-location')?.textContent).toContain('Action 1 / header X-Customer')

    const bodyCandidate = Array.from(utilitySidebar?.querySelectorAll<HTMLButtonElement>('.auto-parameter-candidate-select') || [])
      .find(button => button.textContent?.includes('cart_id'))
    expect(bodyCandidate).toBeDefined()
    if (!bodyCandidate) {
      return
    }

    await act(async () => {
      bodyCandidate.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(utilitySidebar?.querySelector('.auto-parameter-location')?.textContent).toContain('Action 1 / body')
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
  expect(container.textContent).toContain('Jitter (s)')
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

  it('shows runtime settings as a tabbed workspace with one selected category', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const runtimeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings'))
    expect(runtimeButton).toBeDefined()
    if (!runtimeButton) return

    await act(async () => {
      runtimeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.runtime-settings-tabs')).toBeTruthy()
    expect(container.querySelector('.runtime-settings-detail')).toBeTruthy()

    function runtimeTab(label: string) {
      return Array.from(container.querySelectorAll('.runtime-settings-tab')).find(candidate =>
        candidate.textContent?.includes(label),
      ) as HTMLButtonElement | undefined
    }

    expect(runtimeTab('Think Time')?.querySelector('svg[data-runtime-section-icon="think"]')).toBeTruthy()
    expect(runtimeTab('Think Time')?.textContent).not.toContain('TT')
    expect(runtimeTab('Run Logic')?.querySelector('svg[data-runtime-section-icon="runlogic"]')).toBeTruthy()
    expect(runtimeTab('Web HTTP/HTML Compatibility')?.querySelector('svg[data-runtime-section-icon="web_runtime"]')).toBeTruthy()

    async function clickRuntimeCategory(label: string) {
      const button = runtimeTab(label)
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

    await clickRuntimeCategory('Think Time')
    const thinkTimeEnable = container.querySelector('input[aria-label="Enable think time"]') as HTMLInputElement
    expect(thinkTimeEnable).not.toBeNull()
    expect(thinkTimeEnable.checked).toBe(true)
    expect(container.querySelector('fieldset')?.disabled).toBe(false)

    await clickRuntimeCategory('Error Handling')
    const errorCheckboxList = findRuntimeCheckboxList(container, 'Continue on error')
    expect(errorCheckboxList).toBeTruthy()
    expect(errorCheckboxList?.textContent).toContain('Capture snapshot on error')
    expect(errorCheckboxList?.querySelectorAll('label.checkbox-label')).toHaveLength(2)

    await clickRuntimeCategory('Iteration Pacing')
    await clickRuntimeCategory('HTTP Transport')

    expect(sectionFor('Think Time')?.classList.contains('open')).toBe(false)
    expect(sectionFor('Iteration Pacing')?.classList.contains('open')).toBe(false)
    expect(sectionFor('HTTP Transport')?.classList.contains('open')).toBe(true)
    const httpTransportTabs = Array.from(container.querySelectorAll('.http-transport-tabs [role="tab"]')) as HTMLButtonElement[]
    // The Playwright 'Browser Emulation' sub-tab is browser-protocol-only now;
    // HTTP scripts get the top-level Browser Emulation section instead.
    expect(httpTransportTabs.map(tab => tab.textContent?.trim())).toEqual([
      'Request Defaults0',
      'Session & Cache0',
      'Connection',
      'Timeouts',
      'TLS / SSL',
      'Proxy',
      'Authentication',
    ])
    expect(httpTransportTabs.find(tab => tab.textContent?.includes('Request Defaults'))?.getAttribute('aria-selected')).toBe('true')
    await act(async () => {
      httpTransportTabs.find(tab => tab.textContent?.includes('Timeouts'))?.click()
    })
    expect(sectionFor('HTTP Transport')?.textContent).toContain('Connect (s)')
    expect(sectionFor('HTTP Transport')?.textContent).not.toContain('Max Conns/Host')

    const connectTimeoutInput = findInputByLabelText(container, 'Connect (s)')
    expect(connectTimeoutInput?.value).toBe('30')
    expect(connectTimeoutInput?.getAttribute('step')).toBe('0.001')
    if (!connectTimeoutInput) return

    await act(async () => {
      connectTimeoutInput.focus()
      changeInputValue(connectTimeoutInput, '60.001')
    })

    expect(connectTimeoutInput.value).toBe('60.001')

    const scriptJsonTab = findCenterTab(container, 'Source')
    expect(scriptJsonTab).toBeDefined()
    if (!scriptJsonTab) return
    await act(async () => {
      scriptJsonTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const jsonEditor = container.querySelector('.center-tab-panel.active textarea') as HTMLTextAreaElement | null
    expect(jsonEditor?.value).toContain('"connect_timeout_ms": 60001')

    await clickRuntimeCategory('HTTP Transport')

    const connectionTab = httpTransportTabs.find(tab => tab.textContent?.includes('Connection'))
    expect(connectionTab).toBeDefined()
    if (!connectionTab) return
    await act(async () => {
      connectionTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(Array.from(container.querySelectorAll('.http-transport-tabs [role="tab"]')).find(tab => tab.textContent?.includes('Connection'))?.getAttribute('aria-selected')).toBe('true')
    expect(sectionFor('HTTP Transport')?.textContent).toContain('Max Conns/Host')
    expect(sectionFor('HTTP Transport')?.textContent).not.toContain('Connect (s)')

    const tlsTab = Array.from(container.querySelectorAll('.http-transport-tabs [role="tab"]')).find(tab =>
      tab.textContent?.includes('TLS / SSL'),
    ) as HTMLButtonElement | undefined
    expect(tlsTab).toBeDefined()
    if (!tlsTab) return
    await act(async () => {
      tlsTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const tlsCheckboxList = findRuntimeCheckboxList(container, 'Skip certificate verification')
    expect(tlsCheckboxList).toBeTruthy()
    expect(tlsCheckboxList?.querySelectorAll('label.checkbox-label')).toHaveLength(1)

    expect(container.querySelectorAll('.runtime-settings-detail .settings-section.open')).toHaveLength(1)
    expect(container.querySelector('.runtime-settings-header-actions')).toBeNull()
    expect(findButtonByText(container, 'Save Script')).toBeDefined()
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
    expect(pageText).toContain('Source Mapping')
    expect(pageText).toContain('Runtime Settings Parity')
    expect(container.textContent).toContain('Migration Readiness')
    expect(container.textContent).toContain('72%')
    expect(container.textContent).toContain('Blocking runtime settings matched the selected block statuses')
    expect(pageText).toContain('Executed: Run Logic')
    expect(pageText).toContain('Partial: Logging')
    expect(pageText).toContain('Iterations')
    expect(pageText).toContain('default.cfg')
    expect(container.textContent).toContain('Open Run Logic Settings')
    expect(container.textContent).toContain('Open Logging Settings')
    expect(container.querySelector('.parameter-mappings-launcher')?.textContent).toContain('Parameter File Mappings')
    expect(container.querySelector('.parameter-mappings-launcher')?.textContent).toContain('Open Mapping List')
    expect(container.querySelector('.parameter-mappings-launcher')?.textContent).not.toContain('Starter Blocks')
    expect(container.textContent).toContain('Source Mapping')
    expect(container.textContent).toContain('Action.c:3')
    expect(container.textContent).toContain('web_url')
    expect(container.textContent).toContain('"target": "/login"')
    const metadataSection = container.querySelector('#imported-metadata-section')
    const actionDetail = container.querySelector('.action-detail')
    expect(metadataSection?.classList.contains('open')).toBe(true)
    expect(metadataSection?.querySelector('.import-metadata-header')).toBeNull()
    expect(metadataSection?.querySelector('.import-source-chip')).toBeNull()
    expect(metadataSection?.textContent).not.toContain('This script retains source-specific import metadata')
    expect(metadataSection?.querySelector('.source-map-summary')).toBeNull()
    expect(metadataSection?.querySelector('.import-metadata-body > .import-metadata-grid')).toBeNull()
    expect(metadataSection?.textContent).not.toContain('Source Script')
    expect(metadataSection?.textContent).not.toContain('Generated Data Sources')
    expect(metadataSection?.textContent).not.toContain('Open In Parameter Data')
    expect(actionDetail).toBeNull()
    expect(container.querySelector('.author-code-center')?.contains(metadataSection)).toBe(true)
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).not.toContain('Outline')
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).not.toContain('Conversion Mapping')
    expect(findCenterTab(container, 'Conversion Mapping')).toBeUndefined()
    const centerTabLabels = Array.from(container.querySelectorAll('.center-tab-button')).map(button => button.textContent?.trim())
    const sourceMappingTabIndex = centerTabLabels.indexOf('Source Mapping')
    expect(sourceMappingTabIndex).toBeGreaterThanOrEqual(0)
    expect(centerTabLabels).not.toContain('Metadata JSON')
    const importedMetadataTab = findCenterTab(container, 'Source Mapping')
    expect(importedMetadataTab).toBeDefined()
    await act(async () => {
      importedMetadataTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(metadataSection?.querySelector('.source-map-header h4')).toBeNull()
    expect(metadataSection?.querySelector('.source-map-header p')).toBeNull()
    expect(metadataSection?.textContent).not.toContain('View preserved metadata JSON')
    expect(metadataSection?.querySelector('.import-metadata-details')).toBeNull()
    expect(metadataSection?.querySelector('.metadata-json-pre')).toBeNull()
    // Metadata JSON now opens on demand from the outline rather than a center tab.
    expect(findCenterTab(container, 'Metadata JSON')).toBeUndefined()
    const metadataJsonLink = Array.from(container.querySelector('.actions-sidebar')?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Imported Metadata JSON')
    expect(metadataJsonLink).toBeDefined()
    await act(async () => {
      metadataJsonLink?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const metadataJsonEditor = container.querySelector('.metadata-json-dialog .metadata-json-viewer textarea') as HTMLTextAreaElement | null
    expect(metadataJsonEditor).not.toBeNull()
    expect(metadataJsonEditor?.value).toContain('"source_script_name": "checkout.har"')
    expect(metadataJsonEditor?.value).toContain('"runtime_settings_parity"')
    expect(metadataJsonEditor?.dataset.lineNumbers).toBe('true')
    expect(metadataJsonEditor?.dataset.foldGutter).toBe('true')
    expect(metadataJsonEditor?.dataset.readOnly).toBe('true')
    const closeMetadataJsonButton = Array.from(container.querySelectorAll('.metadata-json-dialog button')).find(button => button.textContent === 'Close')
    await act(async () => {
      closeMetadataJsonButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      importedMetadataTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).toContain('Source Mapping')
    expect(metadataSection?.classList.contains('editor-support-dialog')).toBe(false)
    expect(metadataSection?.getAttribute('role')).toBeNull()
    expect(container.querySelector('.support-dialog-import')).toBeNull()
    expect(metadataSection?.textContent).not.toContain('Parameter File Mappings')
    expect(container.querySelector('.imported-parameter-mappings-dialog')).toBeNull()
    const openMappingsButton = findButtonByText(container, 'Open Mapping List')
    expect(openMappingsButton).toBeDefined()
    await act(async () => {
      openMappingsButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('LoginUsers.prm: LoginUser')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('column username')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('table Users.dat')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('Generated source: users')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('Open Mapped Data Source')

    // Correlation suggestions live in the Source Mapping companion panel. Drive the UI to
    // that panel explicitly so the assertions below do not depend on the active center tab left
    // over from a prior interaction.
    const correlationMetadataTab = findCenterTab(container, 'Source Mapping')
    expect(correlationMetadataTab).toBeDefined()
    await act(async () => {
      correlationMetadataTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const correlationSection = Array.from(
      container.querySelectorAll('#imported-metadata-section .import-correlation-section'),
    ).find(section => section.querySelector('h4')?.textContent?.trim() === 'Correlation Suggestions')
    expect(correlationSection).toBeDefined()
    expect(correlationSection?.textContent).toContain('Correlation Suggestions')
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
    const scriptJsonTab = findCenterTab(container, 'Source')
    expect(scriptJsonTab).toBeDefined()
    await act(async () => {
      scriptJsonTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Source')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.script-code-pane')?.textContent).toContain('actions[1]')
    expect(container.querySelector('#imported-metadata-section')).not.toBeNull()
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Action context')
    expect(container.querySelector('.author-main-action .action-editor-panel')).not.toBeNull()
    expect(container.querySelector('.editor-utility-sidebar .action-editor-panel')).toBeNull()
    expect(container.querySelector('.script-code-inline-inspector .action-editor-panel')).toBeNull()
    expect(container.querySelector('.json-source-map')).toBeNull()
    const activeSidebarAction = container.querySelector('.action-item.active') as HTMLDivElement | null
    expect(activeSidebarAction?.textContent).toContain('Complete Checkout')

    const mappingMetadataTab = findCenterTab(container, 'Source Mapping')
    expect(mappingMetadataTab).toBeDefined()
    await act(async () => {
      mappingMetadataTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findCenterTab(container, 'Source Mapping')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Source Mapping')
    expect(container.querySelector('.editor-utility-sidebar')?.textContent).toContain('Source Mapping')
    expect(container.querySelector('.editor-utility-sidebar .action-editor-panel')).toBeNull()
    const workspaceGrid = container.querySelector('.actions-content-grid')
    const maximizeSourceMappingButton = Array.from(metadataSection?.querySelectorAll('.source-map-header button') || []).find(
      button => button.textContent?.trim() === 'Expand workspace',
    )
    expect(maximizeSourceMappingButton).toBeDefined()
    await act(async () => {
      maximizeSourceMappingButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(workspaceGrid?.classList.contains('author-outline-collapsed')).toBe(true)
    expect(workspaceGrid?.classList.contains('author-right-sidebar-collapsed')).toBe(true)
    const restoreSourceMappingButton = Array.from(metadataSection?.querySelectorAll('.source-map-header button') || []).find(
      button => button.textContent?.trim() === 'Restore workspace',
    )
    expect(restoreSourceMappingButton?.getAttribute('aria-pressed')).toBe('true')
    await act(async () => {
      restoreSourceMappingButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(workspaceGrid?.classList.contains('author-outline-collapsed')).toBe(false)
    expect(workspaceGrid?.classList.contains('author-right-sidebar-collapsed')).toBe(false)

    const restoredSidebarActions = container.querySelectorAll('.action-item')
    expect(restoredSidebarActions.length).toBeGreaterThan(1)
    await act(async () => {
      restoredSidebarActions[1]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findCenterTab(container, 'Action editor')?.getAttribute('aria-selected')).toBe('true')
    await act(async () => { findCenterTab(container, 'Source Mapping')?.click() })

    expect(findCenterTab(container, 'Source Mapping')?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Source Mapping')
    expect(container.querySelector('.editor-utility-sidebar .action-editor-panel')).toBeNull()
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

    expect(container.querySelector('.editor-header .editor-actions')?.textContent).not.toContain('Source Mapping')
    const reopenMetadataButton = Array.from(container.querySelector('.actions-sidebar')?.querySelectorAll('button') || []).find(button =>
      button.textContent?.trim() === 'Source Mapping',
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

    expect(container.textContent).toContain('Preserved run order')

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

    const openMappingListButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Open Mapping List',
    )
    expect(openMappingListButton).toBeDefined()
    if (!openMappingListButton) {
      return
    }

    await act(async () => {
      openMappingListButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const parameterMappingsDialog = container.querySelector('.imported-parameter-mappings-dialog')
    expect(parameterMappingsDialog?.textContent).toContain('LoginUsers.prm: LoginUser')
    expect(parameterMappingsDialog?.textContent).toContain('column username')
    expect(parameterMappingsDialog?.textContent).toContain('table Users.dat')
    expect(parameterMappingsDialog?.textContent).toContain('Generated source: users')

    const openMappedDataSourceButton = Array.from(parameterMappingsDialog?.querySelectorAll('button') || []).find(button =>
      button.textContent?.trim() === 'Open Mapped Data Source',
    )
    expect(openMappedDataSourceButton).toBeDefined()
    if (!openMappedDataSourceButton) {
      return
    }

    await act(async () => {
      openMappedDataSourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.imported-parameter-mappings-dialog')).toBeNull()
    expect(container.querySelector('.parameter-data-dialog')).not.toBeNull()
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('Parameter: username')
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('{{param.username}}')
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('username')
    expect(container.textContent).toContain('users')
    await activateParameterDataTab(container)
    expect(container.querySelector('.parameter-data-selected-view')).not.toBeNull()

    expect(Array.from(container.querySelectorAll('button')).some(button =>
      button.textContent?.trim() === 'Open In Parameter Data',
    )).toBe(false)
  })

  it('keeps an operator target edit focused when a selection frame is still pending', async () => {
    const focusScript: ScriptDefinition = { ...initialScript, actions: [
      { ...initialScript.actions[0], name: 'Health check', target: '/health' },
      { ...initialScript.actions[0], name: 'Create order', method: 'POST', target: '/orders' },
    ] }
    await act(async () => { root.render(<ScriptEditor initialScript={focusScript} projectId="project-1" />) })
    const frames = new Map<number, FrameRequestCallback>()
    let nextId = 0
    const requestFrame = vi.spyOn(globalThis, 'requestAnimationFrame').mockImplementation(callback => {
      const id = ++nextId; frames.set(id, callback); return id
    })
    const cancelFrame = vi.spyOn(globalThis, 'cancelAnimationFrame').mockImplementation(id => { frames.delete(id) })
    try {
      const select = container.querySelector<HTMLButtonElement>('[data-outline-select][data-tooltip="Create order"]')!
      select.focus()
      await act(async () => { select.click() })
      const target = container.querySelector<HTMLInputElement>('.author-main-action [data-author-field="target"]')!
      expect(target.value).toBe('/orders')
      expect(container.querySelector<HTMLInputElement>('.author-main-action [data-author-field="name"]')!.value).toBe('Create order')
      target.focus(); target.select()
      await act(async () => { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(1)) })
      expect(document.activeElement).toBe(target)
      await act(async () => { changeInputValue(document.activeElement as HTMLInputElement, '') })
      expect(target.value).toBe('')
      expect(container.querySelector<HTMLInputElement>('.author-main-action [data-author-field="name"]')!.value).toBe('Create order')
      expect(container.querySelector('.inspector-selection-path')?.textContent).toContain('script.actions[1]')
    } finally { requestFrame.mockRestore(); cancelFrame.mockRestore() }
  })

  it('lets the latest problem repair focus its target without an earlier selection focus taking ownership', async () => {
    const focusScript: ScriptDefinition = { ...initialScript, actions: [
      { ...initialScript.actions[0], name: 'Health check', target: '' },
      { ...initialScript.actions[0], name: 'Create order', method: 'POST', target: '/orders' },
    ] }
    await act(async () => { root.render(<ScriptEditor initialScript={focusScript} projectId="project-1" />) })
    const problems = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => /^Author Problems \(/.test(button.textContent || ''))!
    await act(async () => { problems.click() })
    const repair = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find(button => button.textContent?.includes('Action target is required'))!
    expect(repair).toBeDefined()
    const frames = new Map<number, FrameRequestCallback>()
    let nextId = 0
    const requestFrame = vi.spyOn(globalThis, 'requestAnimationFrame').mockImplementation(callback => {
      const id = ++nextId; frames.set(id, callback); return id
    })
    const cancelFrame = vi.spyOn(globalThis, 'cancelAnimationFrame').mockImplementation(id => { frames.delete(id) })
    try {
      repair.focus()
      await act(async () => { repair.click() })
      await act(async () => { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(1)) })
      const target = container.querySelector<HTMLInputElement>('.author-main-action [data-author-field="target"]')!
      expect(document.activeElement).toBe(target)
      expect(target.value).toBe('')
      expect(container.querySelector('.inspector-selection-path')?.textContent).toContain('script.actions[0]')
    } finally { requestFrame.mockRestore(); cancelFrame.mockRestore() }
  })

  it('never lets pending repair or selection frames interrupt a newer action and operator field', async () => {
    const focusScript: ScriptDefinition = { ...initialScript, actions: [
      { ...initialScript.actions[0], name: 'Health check', target: '' },
      { ...initialScript.actions[0], name: 'Create order', method: 'POST', target: '/orders' },
    ] }
    await act(async () => { root.render(<ScriptEditor initialScript={focusScript} projectId="project-1" />) })
    const problems = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => /^Author Problems \(/.test(button.textContent || ''))!
    await act(async () => { problems.click() })
    const repair = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find(button => button.textContent?.includes('Action target is required'))!
    const frames = new Map<number, FrameRequestCallback>()
    let nextId = 0
    const requestFrame = vi.spyOn(globalThis, 'requestAnimationFrame').mockImplementation(callback => {
      const id = ++nextId; frames.set(id, callback); return id
    })
    const cancelFrame = vi.spyOn(globalThis, 'cancelAnimationFrame').mockImplementation(id => { frames.delete(id) })
    try {
      repair.focus()
      await act(async () => { repair.click() })
      const select = container.querySelector<HTMLButtonElement>('[data-outline-select][data-tooltip="Create order"]')!
      select.focus()
      await act(async () => { select.click() })
      const target = container.querySelector<HTMLInputElement>('.author-main-action [data-author-field="target"]')!
      target.focus()
      const focusEvents: Element[] = []
      const recordFocus = (event: FocusEvent) => { focusEvents.push(event.target as Element) }
      document.addEventListener('focusin', recordFocus)
      try {
        await act(async () => { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(1)) })
        expect(document.activeElement).toBe(target)
        expect(focusEvents).toEqual([])
        expect(target.value).toBe('/orders')
      } finally { document.removeEventListener('focusin', recordFocus) }
    } finally { requestFrame.mockRestore(); cancelFrame.mockRestore() }
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
    expect(selectedDataView?.textContent).toContain('Shared dataset: bbclinks. Edits affect 2 parameters.')
    expect(selectedDataView?.textContent).toContain('Label')
    expect(Array.from(selectedDataView?.querySelectorAll('.parameter-grid tbody input') || []).map(input => (input as HTMLInputElement).value)).toEqual([
      'https://bbc.example/a',
      'BBC A',
    ])
    expect(selectedDataView?.textContent).not.toContain('users.csv')
    expect(selectedDataView?.textContent).not.toContain('alice')

    await activateParameterInfoTab(container)
    expect(container.querySelector('.parameter-info-panel select[aria-label="Parameter Type for bbclink01"]')).toBeNull()
    const readonlyFileType = container.querySelector('.parameter-info-panel .parameter-type-readonly[aria-label="Parameter Type for bbclink01: File"]')
    expect(readonlyFileType).not.toBeNull()
    expect(readonlyFileType?.textContent).toContain('file')
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
        {
          name: 'market',
          type: 'file',
          update: 'each_iteration',
          selection: 'random',
          on_exhaustion: 'wrap',
          data_source: 'cities',
          column: 'market',
        },
      ],
      data_sources: [
        {
          name: 'cities',
          type: 'csv',
          path: 'cities.csv',
          strategy: 'sequential',
          first_row_header: true,
          inline_data: 'city,market\nParis,FR\nLyon,FR\nParis,FR',
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={statsScript} initialFocusedSection="parameter-data" />)
    })

    const infoPanel = container.querySelector('.parameter-info-panel') as HTMLDivElement | null
    expect(infoPanel).not.toBeNull()
    expect(infoPanel?.querySelector('input[aria-label="Parameter name"]')).not.toBeNull()
    expect(infoPanel?.textContent).toContain('Identity')
    expect(infoPanel?.textContent).toContain('Behavior')
    expect(infoPanel?.textContent).toContain('Data Mapping')
    expect(infoPanel?.textContent).toContain('File Type')
    expect(infoPanel?.textContent).toContain('Delimiter')
    expect(infoPanel?.textContent).toContain('First row is header')
    expect(infoPanel?.textContent).toContain('Data Statistics')

    const statRows = Array.from(container.querySelectorAll('.parameter-info-stats-table tbody tr'))
    const statValue = (label: string) => statRows.find(row => row.querySelector('th')?.textContent === label)?.querySelector('td')?.textContent
    // three records (Paris, Lyon, Paris); two distinct values in the mapped column
    expect(statValue('Record Count')).toBe('3')
    expect(statValue('Distinct mapped values')).toBe('2')

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

    const linkedStrategy = container.querySelector('select[aria-label="Iteration Strategy for city"]') as HTMLSelectElement | null
    expect(Array.from(linkedStrategy?.options || []).map(option => option.textContent)).toContain('Same Row As Parameter')
    await act(async () => {
      selectValueSetter?.call(linkedStrategy, 'same_line_as')
      linkedStrategy?.dispatchEvent(new Event('input', { bubbles: true }))
      linkedStrategy?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect((container.querySelector('select[aria-label="Iteration Strategy for city"]') as HTMLSelectElement | null)?.value).toBe('same_line_as')
    const sameRowSelect = container.querySelector('select[aria-label="Same Row As for city"]') as HTMLSelectElement | null
    expect(sameRowSelect?.value).toBe('market')

    const exhaustion = container.querySelector('select[aria-label="On Exhaustion for city"]') as HTMLSelectElement | null
    expect(Array.from(exhaustion?.options || []).map(option => option.textContent)).toContain('Continue With Last Row')
    await act(async () => {
      selectValueSetter?.call(exhaustion, 'last')
      exhaustion?.dispatchEvent(new Event('input', { bubbles: true }))
      exhaustion?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect((container.querySelector('select[aria-label="On Exhaustion for city"]') as HTMLSelectElement | null)?.value).toBe('last')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Script'))
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    const createCalls = apiMock.createScript.mock.calls
    const savedScript = createCalls[createCalls.length - 1]?.[0] as ScriptDefinition
    const savedCityParameter = savedScript.parameters?.find(parameter => parameter.name === 'city')
    expect(savedCityParameter).toMatchObject({
      same_line_as: 'market',
      on_exhaustion: 'last',
    })
    expect(savedScript.data_sources?.[0]?.strategy).toBe('sequential')

    // The data table lives under the Data tab: it keeps its display options
    // but drops the old source-metadata header row.
    await activateParameterDataTab(container)
    const activeDataPanel = container.querySelector('.parameter-detail-data:not([hidden])') as HTMLDivElement | null
    expect(activeDataPanel?.textContent).not.toContain('File Type')
    expect(activeDataPanel?.textContent).not.toContain('Delimiter')
    expect(activeDataPanel?.textContent).not.toContain('First row is header')
    const dataView = container.querySelector('.parameter-data-selected-view') as HTMLDivElement | null
    expect(dataView?.querySelector('table.parameter-grid')).not.toBeNull()
    expect(Array.from(dataView?.querySelectorAll('button') || []).some(button => button.textContent?.trim() === 'Raw Mode')).toBe(true)
    expect(dataView?.querySelector('input[placeholder="/absolute/path/to/data.csv (optional when inline data is used)"]')).toBeNull()
  })

  it('marks path-backed parameter data statistics and preview as unavailable', async () => {
    const pathBackedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [{
        name: 'username',
        type: 'file',
        update: 'each_iteration',
        selection: 'sequential',
        on_exhaustion: 'wrap',
        data_source: 'users',
        column: 'username',
      }],
      data_sources: [{
        name: 'users',
        type: 'csv',
        path: 'fixtures/users.csv',
        strategy: 'sequential',
        first_row_header: true,
        columns: ['username'],
      }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={pathBackedScript} initialFocusedSection="parameter-data" />)
    })

    const statRows = Array.from(container.querySelectorAll('.parameter-info-stats-table tbody tr'))
    const statValue = (label: string) => statRows.find(row => row.querySelector('th')?.textContent === label)?.querySelector('td')?.textContent
    expect(statValue('Record Count')).toBe('—')
    expect(statValue('Distinct mapped values')).toBe('—')
    expect(statValue('Columns')).toBe('—')
    expect(container.querySelector('.parameter-info-panel')?.textContent).toContain('fixtures/users.csv')

    await activateParameterDataTab(container)
    expect(container.querySelector('.parameter-detail-data')?.textContent).toContain('Data lives in fixtures/users.csv; contents are not available for preview.')
    expect(container.querySelector('.parameter-detail-data')?.textContent).not.toContain('Column does not match this file')
    expect(container.querySelector('.parameter-data-selected-view')).toBeNull()
  })

  it('splits the parameter detail into Settings and Data tabs', async () => {
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

    // Data-backed parameter exposes both tabs; Settings is selected by default.
    expect(tabLabels()).toEqual(['Settings', 'Data', 'Usage'])
    expect(tab('Settings')?.getAttribute('aria-selected')).toBe('true')
    expect(infoSection()?.hasAttribute('hidden')).toBe(false)
    expect(dataSection()?.hasAttribute('hidden')).toBe(true)
    expect(container.querySelector('.parameter-info-panel')).not.toBeNull()

    // Switching to Data reveals the data table and hides the information panel.
    await activateParameterDataTab(container)
    expect(tab('Data')?.getAttribute('aria-selected')).toBe('true')
    expect(infoSection()?.hasAttribute('hidden')).toBe(true)
    expect(dataSection()?.hasAttribute('hidden')).toBe(false)
    expect(container.querySelector('.parameter-detail-data .parameter-data-selected-view table.parameter-grid')).not.toBeNull()

    // Switching back to Settings restores the info panel.
    await activateParameterInfoTab(container)
    expect(tab('Settings')?.getAttribute('aria-selected')).toBe('true')
    expect(infoSection()?.hasAttribute('hidden')).toBe(false)
    expect(dataSection()?.hasAttribute('hidden')).toBe(true)

    // Generated parameters retain Settings and Usage without a dataset tab.
    const counterItem = Array.from(container.querySelectorAll('.parameter-sidebar-item')).find(item => item.textContent?.includes('visit_no'))
    expect(counterItem).toBeDefined()
    await act(async () => {
      counterItem?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(tabLabels()).toEqual(['Settings', 'Usage'])
    expect(container.querySelector('.parameter-detail-tabs')).not.toBeNull()
    expect(infoSection()?.hasAttribute('hidden')).toBe(false)
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

    const sidebar = container.querySelector('.parameter-sidebar-card') as HTMLDivElement | null
    const mergeButton = Array.from(sidebar?.querySelectorAll('button') || []).find(button =>
      button.textContent?.trim() === 'Merge Source Files',
    ) as HTMLButtonElement | undefined
    expect(mergeButton).toBeDefined()
    expect(mergeButton?.disabled).toBe(false)
    expect(container.querySelector('.parameter-data-dialog')?.textContent).not.toContain('Merge Source Files')

    await act(async () => {
      mergeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Merged 2 parameter source files into merged_parameters.')
    expect(container.textContent).toContain('Save Script to make this file change persist.')
    const parameterDialog = container.querySelector('.parameter-data-dialog') as HTMLDivElement | null
    expect(parameterDialog?.textContent).toContain('merged_parameters')
    expect(parameterDialog?.textContent).not.toContain('Users.csv')
    expect(parameterDialog?.textContent).not.toContain('Passwords.dat')
    expect(container.querySelector('.parameter-sidebar-card')?.textContent).toContain('File')
    expect(container.querySelector('.parameter-sidebar-card')?.textContent).toContain('Current Iteration')
    expect(parameterDialog?.querySelector('select[aria-label="Parameter Type for username"]')).toBeNull()
    expect(parameterDialog?.querySelector('.parameter-type-readonly[aria-label="Parameter Type for username: File"]')?.textContent).toContain('file')
    await activateParameterDataTab(container)
    const selectedDataView = container.querySelector('.parameter-data-selected-view') as HTMLDivElement | null
    expect(selectedDataView?.textContent).toContain('Shared dataset: merged_parameters. Edits affect 2 parameters.')
    expect(selectedDataView?.textContent).toContain('username')
    expect(selectedDataView?.textContent).toContain('password')
    expect(Array.from(selectedDataView?.querySelectorAll('.parameter-grid tbody input') || []).map(input => (input as HTMLInputElement).value)).toEqual([
      'alice',
      'a-pass',
      'bob',
      'b-pass',
    ])
  })

  it('omits the center remove row from the selected parameter detail', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initialFocusedSection="parameter-data" />)
    })

    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('session_id')
    const removeButton = Array.from(container.querySelectorAll('.parameter-data-dialog button')).find(button =>
      button.textContent?.trim() === 'Remove',
    )
    expect(removeButton).toBeUndefined()
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('Parameter: session_id')
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('{{param.session_id}}')
  })

  it('keeps the current parameter selected when its data source changes', async () => {
    const mappedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [
        { name: 'username', type: 'file', data_source: 'users', column: 'value' },
        { name: 'password', type: 'file', data_source: 'passwords', column: 'value' },
      ],
      data_sources: [
        { name: 'users', type: 'csv', inline_data: 'value\nalice', first_row_header: true, strategy: 'sequential' },
        { name: 'passwords', type: 'csv', inline_data: 'value\nsecret', first_row_header: true, strategy: 'sequential' },
      ],
    }
    await act(async () => {
      root.render(<ScriptEditor initialScript={mappedScript} initialFocusedSection="parameter-data" />)
    })
    const sourceSelect = container.querySelector('select[aria-label="Data Source for username"]') as HTMLSelectElement

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!
      setter.call(sourceSelect, 'passwords')
      sourceSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('username')
    expect((container.querySelector('select[aria-label="Data Source for username"]') as HTMLSelectElement)?.value).toBe('passwords')
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('Parameter: username')
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
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('Parameter: username')
    expect(container.querySelector('.parameter-data-dialog')?.textContent).toContain('{{param.username}}')
    expect(container.querySelector('.parameter-data-selected-view')).not.toBeNull()
    expect(container.textContent).toContain('Users.dat')
    expect(container.querySelector('.parameter-sidebar-item.selected')?.textContent).toContain('username')
    expect(container.textContent).toContain('2 rows · 1 column · Auto: Comma')
    const columnNameInput = container.querySelector('.parameter-grid-head input[aria-label^="Column 1 name"]') as HTMLInputElement | null
    expect(columnNameInput?.value).toBe('username')
    expect(columnNameInput?.getAttribute('aria-label')).toBe('Column 1 name for users')
    const firstRowCells = Array.from(container.querySelectorAll('.parameter-grid tbody tr:first-child td'))
    expect(firstRowCells[0]?.classList.contains('parameter-grid-row-action')).toBe(true)
    expect(firstRowCells[0]?.querySelector('button')?.getAttribute('aria-label')).toBe('Remove row 1')
    expect(firstRowCells[1]?.classList.contains('parameter-grid-index')).toBe(true)
    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => input.getAttribute('aria-label'))).toEqual([
      'Row 1, username for users',
      'Row 2, username for users',
    ])
    expect((container.querySelector('select[aria-label="Delimiter for username"]') as HTMLSelectElement | null)?.value).toBe('auto')
  })

  it('saves a focused parameter-grid edit in the exact keyboard-save snapshot and keeps it undoable', async () => {
    const focusedScript: ScriptDefinition = {
      ...initialScript,
      parameters: [{
        name: 'username',
        type: 'file',
        data_source: 'users',
        column: 'username',
      }],
      data_sources: [{
        name: 'users',
        type: 'csv',
        strategy: 'sequential',
        first_row_header: true,
        inline_data: 'username\nalice\nbob',
      }],
    }

    await act(async () => {
      root.render(
        <KeyboardShortcutProvider>
          <ScriptEditor
            initialScript={focusedScript}
            initialFocusedDataSourceName="users"
            projectId="project-1"
          />
        </KeyboardShortcutProvider>,
      )
    })
    await flushReact()

    const cell = container.querySelector<HTMLInputElement>('input[aria-label="Row 1, username for users"]')
    expect(cell).not.toBeNull()
    if (!cell) return

    await act(async () => {
      cell.focus()
      changeInputValue(cell, 'alice-updated')
    })
    await flushReact()

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton?.disabled).toBe(false)
    expect(apiMock.createScript).not.toHaveBeenCalled()

    await act(async () => {
      cell.dispatchEvent(new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
    const savedDraft = apiMock.createScript.mock.calls[0][0] as ScriptDefinition
    expect(savedDraft.data_sources?.[0]?.inline_data).toBe('username\nalice-updated\nbob')
    expect(container.textContent).toContain('Script saved successfully!')

    await act(async () => {
      cell.blur()
      findButtonByText(container, 'Undo')?.click()
    })
    await flushReact()
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Row 1, username for users"]')?.value).toBe('alice')
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
    expect((container.querySelector('.parameter-grid-head input[aria-label^="Column 1 name"]') as HTMLInputElement | null)?.value).toBe('username')
    expect(Array.from(container.querySelectorAll('.parameter-grid tbody input')).map(input => (input as HTMLInputElement).value)).toEqual(['alice', 'secret', 'bob', 'hunter2'])
    expect((container.querySelector('select[aria-label="Delimiter for username"]') as HTMLSelectElement | null)?.value).toBe('auto')
    expect(container.querySelector('.parameter-delimiter-toggle')).toBeNull()

    const rawModeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Raw Mode') as HTMLButtonElement | undefined
    expect(rawModeButton).toBeDefined()
    await act(async () => {
      rawModeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const reveal = container.querySelector('input[aria-label="Reveal sensitive columns"]') as HTMLInputElement
    await act(async () => { reveal.click() })
    const rawData = container.querySelector('textarea[aria-label="Raw data for users"]') as HTMLTextAreaElement | null
    expect(rawData?.value).toBe('username\tpassword\nalice\tsecret\nbob\thunter2')
    const textAreaValueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
    await act(async () => {
      textAreaValueSetter?.call(rawData, 'username\tpassword\nanna\tapass\nben\tbpass')
      rawData?.dispatchEvent(new Event('input', { bubbles: true }))
      rawData?.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await act(async () => { findButtonByText(container, 'Review raw changes')?.click() })
    await act(async () => { findButtonByText(document.body, 'Apply dataset change')?.click() })
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

    expect(document.body.textContent).toContain('Remap these references')
    expect(findButtonByText(document.body, 'Apply dataset change')?.disabled).toBe(true)
    await act(async () => { findButtonByText(document.body, 'Cancel')?.click() })
    expect(container.textContent).toContain('2 rows · 2 columns · Auto: Tab')
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

    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(focusedScript.data_sources?.[0]?.inline_data).toBe('username,password\nbob,bpass\nalice,apass\ncarol,cpass')
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
    expect(container.querySelectorAll('button[data-tooltip="Run Replay"]')).toHaveLength(1)
    expect(container.querySelectorAll('button[data-tooltip="Step to next action"]')).toHaveLength(1)
    expect(container.querySelectorAll('button[data-tooltip="Stop debugger"]')).toHaveLength(1)
    expect(container.querySelectorAll('button[data-tooltip="Session overrides"]')).toHaveLength(1)

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
    expect(findButtonByTitle(container, 'Step to next action')).toBeUndefined()
    expect(findButtonByTitle(container, 'Stop debugger')).toBeUndefined()
    expect(findButtonByTitle(container, 'Session overrides')).toBeUndefined()

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

    const debuggerBreakpointButton = container.querySelector('.script-ide-debugger.open .debugger-gutter[data-tooltip="Add breakpoint"]') as HTMLButtonElement | null
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

  it('restores saved-script debug breakpoints from local storage', async () => {
    window.localStorage.setItem('apexload.debug.breakpoints.script-1', JSON.stringify([
      { phase: 'action', action_index: 0 },
    ]))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await flushReact()

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) {
      return
    }

    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.startScriptDebugSession).toHaveBeenCalledWith('script-1', expect.objectContaining({
      breakpoints: [{ phase: 'action', action_index: 0 }],
    }))
  })

  it('renders semantic debugger colors in the console without flattening protocol output', async () => {
    const oversizedResponse = `HTTP/1.1 503 Unavailable\n${'x'.repeat(66_000)}`
    apiMock.startScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: true,
      events: [],
    }, {
      logs: [
        {
          id: 'request-log', timestamp: '2026-05-14T20:00:00Z', level: 'info', category: 'request',
          phase: 'action', action_index: 0, message: 'sent',
          raw: 'GET /api/orders HTTP/1.1\nContent-Type: application/json\n{"customer_id":42}',
        },
        {
          id: 'response-log', timestamp: '2026-05-14T20:00:01Z', level: 'error', category: 'response',
          phase: 'action', action_index: 0, message: 'failed', raw: oversizedResponse,
        },
      ],
    }))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()

    const lines = container.querySelectorAll('.debug-log-line')
    expect(lines).toHaveLength(2)
    expect(lines[0].querySelector('.debug-log-level.level-info')?.textContent).toBe('INFO')
    expect(lines[0].querySelector('.debug-log-category')?.textContent).toBe('request')
    expect(lines[0].querySelector('.debug-log-location')?.textContent).toBe('action 1')
    expect(lines[0].querySelector('.token-method')?.textContent).toBe('GET')
    expect(lines[0].querySelector('.token-target')?.textContent).toBe('/api/orders')
    expect(lines[0].querySelector('.token-header')?.textContent).toBe('Content-Type')
    expect(lines[0].querySelector('.token-key')?.textContent).toBe('"customer_id"')
    expect(lines[0].querySelector('.token-number')?.textContent).toBe('1.1')
    expect(lines[1].querySelector('.debug-log-level.level-error')?.textContent).toBe('ERROR')
    expect(lines[1].querySelector('.token-status-error')?.textContent).toBe('503')
    expect(lines[1].querySelector('.debug-log-omission')?.textContent).toContain('characters omitted from display')
    expect(lines[1].querySelector('.debug-log-raw')?.textContent?.length).toBeLessThan(oversizedResponse.length)
  })

  it('surfaces an expired debugger session when polling returns not found', async () => {
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      success: true,
      events: [],
    }))
    apiMock.getScriptDebugSession.mockRejectedValueOnce(new ApiError('not found', 404))

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
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
    await flushReact()

    expect(container.textContent).toContain('Debugger session expired. Start a new debug run to continue.')
    expect(container.textContent).toContain('Debugger session expired')
  })

  it('does not dispatch duplicate replay starts on rapid clicks', async () => {
    let resolveStart!: (value: ReturnType<typeof debugSessionResponse>) => void
    apiMock.startScriptDebugSession.mockReturnValueOnce(new Promise(resolve => {
      resolveStart = resolve
    }))
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    const runButton = findDebuggerRunButton(container)!
    act(() => {
      runButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      runButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.startScriptDebugSession).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveStart(debugSessionResponse({
        group: 'Checkout Flow-debug',
        protocol: 'http',
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        success: true,
        events: [],
      }))
      await Promise.resolve()
    })
  })

  it('tolerates a transient not-found while reconciling an ambiguous replay start', async () => {
    apiMock.startScriptDebugSession.mockRejectedValueOnce(new TypeError('connection reset'))
    apiMock.getScriptDebugSession
      .mockRejectedValueOnce(new ApiError('not found', 404))
      .mockImplementationOnce(async (sessionId: string) => ({
        session: {
          ...unavailableRuntimeMetadata(sessionId, 'running', '1'),
          id: sessionId,
          group: 'Checkout Flow-debug',
          protocol: 'http',
          status: 'running',
          started_at: '2026-05-14T20:00:00Z',
          current_event_index: -1,
          events: [],
        },
      }))
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.getScriptDebugSession).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Debugger session expired')
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 1250))
    })
    await flushReact()

    expect(apiMock.getScriptDebugSession.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(container.textContent).toContain('Running · waiting for next action')
    expect(container.textContent).not.toContain('Debugger session expired')
  })

  it.each([true, false])('shows terminal evidence when the start response already completed (success=%s)', async success => {
    apiMock.startScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
      group: 'Checkout Flow-debug', protocol: 'http',
      started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:01Z',
      success, events: [], error: success ? undefined : 'Service unavailable',
    }))
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => findDebuggerRunButton(container)!.click())
    await flushReact()
    expect(container.textContent).toContain(success ? 'Passed' : 'Failed')
    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
  })

  it('reconciles and timestamps a terminal polling response when the completion WebSocket event is missed', async () => {
    const terminalReport = {
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      success: true,
      events: [{
        phase: 'action',
        action_index: 0,
        action_name: 'Get Products',
        action_type: 'GET',
        method: 'GET',
        target: '/api/products',
        status_code: 200,
        success: true,
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        duration_ms: 1000,
        snapshot: {
          request_url: '/api/products',
          response_status: 200,
          response_body: '{"csrf":"rotated"}',
        },
      }],
    }
    apiMock.startScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'debug-session-1',
      debug_session_id: 'debug-session-1',
      session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'),
        id: 'debug-session-1',
        group: terminalReport.group,
        protocol: terminalReport.protocol,
        status: 'running',
        started_at: terminalReport.started_at,
        current_event_index: -1,
        events: [],
      },
    })
    apiMock.getScriptDebugSession.mockResolvedValueOnce({
      session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'completed', '3'),
        id: 'debug-session-1',
        group: terminalReport.group,
        protocol: terminalReport.protocol,
        status: 'completed',
        started_at: terminalReport.started_at,
        completed_at: '2026-05-14T20:00:01Z',
        current_event_index: 0,
        events: terminalReport.events,
        report: terminalReport,
      },
    })
    apiMock.getScriptRecordingStatus.mockResolvedValueOnce({
      exists: true,
      correlation_usable: true,
      request_count: 1,
      correlation_request_count: 1,
    })
    apiMock.diagnoseScriptReplay.mockResolvedValueOnce({
      suggestions: [],
      compared_recorded_steps: 1,
      total_recorded_steps: 1,
    })

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })

    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    if (!runReplayButton) return
    await act(async () => {
      runReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.getScriptDebugSession).toHaveBeenCalledWith('debug-session-1')
    const diagnoseButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Diagnose correlations')
    expect(diagnoseButton).toBeDefined()
    expect(diagnoseButton?.disabled).toBe(false)
    expect(findDebuggerRunButton(container)?.disabled).toBe(false)

    await act(async () => {
      diagnoseButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(apiMock.diagnoseScriptReplay).toHaveBeenCalledWith(
      'script-1',
      expect.any(Array),
      'debug-session-1',
    )
  })

  it.each([{ follow: true, privacy: false }, { follow: false, privacy: false }, { follow: true, privacy: true }])('selects terminal GET evidence without overriding operator selection or privacy (follow=$follow, privacy=$privacy)', async ({ follow, privacy }) => {
    let resolveGet!: (value: unknown) => void
    const resource = (path: string) => ({ request_url: path, response_status: 200, response_body_state: 'captured', response_body: 'diagnostic evidence' })
    const firstEvent = { event_id: 'first', phase: 'action', action_index: 0, action_name: 'First evidence', action_type: 'GET',
      target: '/first', success: true, started_at: '2026-10-04T00:00:00Z', completed_at: '2026-10-04T00:00:01Z', duration_ms: 1,
      snapshot: { resources: [resource('/first-resource')] } }
    const failedEvent = { ...firstEvent, event_id: 'failed', action_index: 1, action_name: 'Failed evidence', target: '/failed', success: false,
      snapshot: { resources: [resource('/failed-resource')] } }
    const terminalReport = { replay_session_id: 'debug-session-1', group: 'Recovered resource evidence', protocol: 'http',
      started_at: firstEvent.started_at, completed_at: '2026-10-04T00:00:03Z', success: false,
      events: [firstEvent, failedEvent, { ...firstEvent, event_id: 'cleanup', phase: 'teardown', action_name: 'Cleanup', snapshot: undefined }] }
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: {
      ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'), id: 'debug-session-1', status: 'running',
      group: terminalReport.group, protocol: 'http', started_at: firstEvent.started_at,
      current_event_index: 0, events: [firstEvent], report: { ...terminalReport, completed_at: '', success: true, events: [firstEvent] },
    } })
    apiMock.getScriptDebugSession.mockReturnValueOnce(new Promise(resolve => { resolveGet = resolve }))
    const script: ScriptDefinition = { ...initialScript, actions: [
      { name: 'First evidence', type: 'GET', target: '/first' }, { name: 'Failed evidence', type: 'GET', target: '/failed' },
    ], teardown: [{ name: 'Cleanup', type: 'GET', target: '/cleanup' }] }
    apiMock.getScript.mockResolvedValueOnce({ script })
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={script} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    expect(apiMock.getScriptDebugSession).toHaveBeenCalledWith('debug-session-1')
    if (!follow) {
      const firstRow = Array.from(container.querySelectorAll<HTMLButtonElement>('.debugger-action-row-main')).find(button => button.textContent?.includes('First evidence'))!
      await act(async () => firstRow.click())
      expect(findInputByLabelText(container, 'Follow execution')?.checked).toBe(false)
      expect(container.querySelector('[aria-label="Captured resources"]')?.textContent).toContain('/first-resource')
    }
    await act(async () => {
      resolveGet({ session: { ...unavailableRuntimeMetadata('debug-session-1', 'failed', '3'), id: 'debug-session-1',
        group: terminalReport.group, protocol: 'http', status: 'failed', started_at: terminalReport.started_at,
        completed_at: terminalReport.completed_at, current_event_index: 2, events: [], runtime_evidence_unavailable: privacy,
        report: { ...terminalReport, runtime_evidence_unavailable: privacy } } })
      await Promise.resolve()
    })
    await flushReact()
    const resources = container.querySelector('[aria-label="Captured resources"]')
    if (privacy) {
      expect(resources).toBeNull()
      expect(container.textContent).not.toContain('/failed-resource')
      expect(container.textContent).not.toContain('diagnostic evidence')
    } else {
      expect(resources?.textContent).toContain(follow ? '/failed-resource' : '/first-resource')
      expect(resources?.textContent).not.toContain(follow ? '/first-resource' : '/failed-resource')
    }
    expect(findInputByLabelText(container, 'Follow execution')?.checked).toBe(follow)
    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Variables')!.click())
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain(privacy ? RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE : 'Current state unavailable: the session has ended.')
  })

  it('binds a deferred trusted start despite matching early full and leaf WebSocket broadcasts', async () => {
    let resolveStart!: (value: unknown) => void
    apiMock.startScriptDebugSession.mockReturnValueOnce(new Promise(resolve => { resolveStart = resolve }))
    apiMock.getScriptDebugSession.mockReturnValue(new Promise(() => {}))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    const socket = () => (useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage: (message: unknown) => void }).onMessage
    await act(async () => {
      socket()({ type: 'script_debug_paused', debug_session_id: 'debug-session-1', session: heldDebugSession('10', 'untrusted-early-value') })
      socket()({ type: 'script_debug_action_started', debug_session_id: 'debug-session-1', session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: '9', status: 'running' })
    })
    expect(container.textContent).not.toContain('untrusted-early-value')
    await act(async () => { resolveStart({ session: heldDebugSession() }); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Live Variables')!.click())
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain('held-value')
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain('untrusted-early-value')
    expect(apiMock.getScriptDebugSession).toHaveBeenCalledWith('debug-session-1')
  })

  it('keeps malformed trusted starts reachable for a full GET that binds the requested public ID', async () => {
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: { id: 'debug-session-1', status: 'paused' } })
    apiMock.getScriptDebugSession.mockResolvedValueOnce({ session: heldDebugSession('11', 'get-authorized-value') })
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Live Variables')!.click())
    expect(apiMock.getScriptDebugSession).toHaveBeenCalledWith('debug-session-1')
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain('get-authorized-value')
  })

  it('clears current values on a leaf update and rejects an in-flight pre-update full GET', async () => {
    let resolveOldGet!: (value: unknown) => void
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: heldDebugSession('9007199254740993') })
    apiMock.getScriptDebugSession.mockReturnValueOnce(new Promise(resolve => { resolveOldGet = resolve })).mockResolvedValue({ session: heldDebugSession('9007199254740994', 'fresh-resynchronized-value') })
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Live Variables')!.click())
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain('held-value')
    const socket = () => (useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage: (message: unknown) => void }).onMessage
    await act(async () => socket()({ type: 'script_debug_action_started', debug_session_id: 'debug-session-1', session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: '9007199254740994', status: 'running' }))
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain('held-value')
    await act(async () => { resolveOldGet({ session: heldDebugSession('9007199254740994', 'stale-get-value') }); await Promise.resolve() })
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain('stale-get-value')
    await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 1250)) })
    await flushReact()
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain('fresh-resynchronized-value')
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain('stale-get-value')
  })

  it.each([false, true])('continues Run To after its socket acknowledgement without replacing newer breakpoints (newer=%s)', async newer => {
    let resolveUpdate!: (value: unknown) => void
    const script = { ...initialScript, actions: [...initialScript.actions, { name: 'Run To target', type: 'GET', target: '/target' }] }
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: heldDebugSession('10') })
    apiMock.getScriptDebugSession.mockReturnValue(new Promise(() => {}))
    apiMock.updateScriptDebugBreakpoints.mockReturnValueOnce(new Promise(resolve => { resolveUpdate = resolve }))
    apiMock.resumeScriptDebugSession.mockReturnValueOnce(new Promise(() => {}))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={script} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Action 2') && button.textContent?.includes('Run To target'))!.click())
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'Run to selected action')!.click())
    expect(apiMock.updateScriptDebugBreakpoints).toHaveBeenCalledTimes(1)
    const requested = apiMock.updateScriptDebugBreakpoints.mock.calls[0][1]
    const acknowledgement = { ...heldDebugSession('11', 'acknowledgement-value'), breakpoints: requested }
    const latestBreakpoints = newer ? [{ phase: 'action', action_index: 0, disabled: true }, ...requested] : requested
    const latest = { ...heldDebugSession(newer ? '12' : '11', 'latest-held-value'), breakpoints: latestBreakpoints }
    const socket = () => (useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage: (message: unknown) => void }).onMessage
    await act(async () => socket()({ type: 'script_debug_breakpoints_updated', debug_session_id: 'debug-session-1', session: latest }))
    expect(apiMock.resumeScriptDebugSession).not.toHaveBeenCalled()
    expect(container.querySelector('.debugger-statusbar')?.textContent).toContain(`${latestBreakpoints.length} breakpoint`)
    await act(async () => { resolveUpdate({ session: acknowledgement }); await Promise.resolve() })
    expect(apiMock.resumeScriptDebugSession).toHaveBeenCalledWith('debug-session-1')
    expect(container.querySelector('.debugger-statusbar')?.textContent).toContain(`${latestBreakpoints.length} breakpoint`)
    expect(container.textContent).not.toContain('acknowledgement-value')
  })

  it.each(['terminal', 'foreign', 'rejected', 'incomplete', 'context reset', 'superseded command', 'session expiry'])('does not continue a delayed Run To after %s', async gate => {
    let resolveUpdate!: (value: unknown) => void, rejectUpdate!: (cause: unknown) => void
    const script = { ...initialScript, actions: [...initialScript.actions, { name: 'Run To target', type: 'GET', target: '/target' }] }
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: heldDebugSession('10') })
    if (gate === 'session expiry') apiMock.getScriptDebugSession.mockResolvedValue({ session: heldDebugSession('10') })
    else apiMock.getScriptDebugSession.mockReturnValue(new Promise(() => {}))
    apiMock.updateScriptDebugBreakpoints.mockReturnValueOnce(new Promise((resolve, reject) => { resolveUpdate = resolve; rejectUpdate = reject }))
    apiMock.resumeScriptDebugSession.mockReturnValue(new Promise(() => {}))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={script} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Action 2') && button.textContent?.includes('Run To target'))!.click())
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'Run to selected action')!.click())
    const requested = apiMock.updateScriptDebugBreakpoints.mock.calls[0][1]
    const acknowledgement = { ...heldDebugSession('11', 'acknowledgement-value'), breakpoints: requested }
    const socket = () => (useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage: (message: unknown) => void }).onMessage
    await act(async () => socket()({ type: 'script_debug_breakpoints_updated', debug_session_id: 'debug-session-1', session: acknowledgement }))
    let expectedResumes = 0
    if (gate === 'terminal') {
      const ended = { ...heldDebugSession('12'), status: 'completed', completed_at: '2026-10-04T00:00:02Z', runtime_state: { ...heldDebugSession('12').runtime_state, state: 'unavailable', reason: 'session_terminal', snapshot: null } }
      await act(async () => socket()({ type: 'script_debug_completed', debug_session_id: 'debug-session-1', session: ended }))
    } else if (gate === 'session expiry') {
      apiMock.getScriptDebugSession.mockRejectedValue(new ApiError('Session not found', 404))
      await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 1250)) })
      await flushReact()
      expect(container.textContent).toContain('Debugger session expired')
    } else if (gate === 'context reset') {
      await act(async () => root.render(<ScriptEditor scriptId="script-2" initialScript={script} projectId="project-2" />))
    } else if (gate === 'superseded command') {
      expect(findButtonByTitle(container, 'Continue')).toBeDefined()
      await act(async () => findButtonByTitle(container, 'Continue')!.click())
      expectedResumes = 1
    }
    await act(async () => {
      if (gate === 'rejected') rejectUpdate(new ApiError('Breakpoint command rejected', 409))
      else if (gate === 'foreign') resolveUpdate({ session: { ...acknowledgement, id: 'foreign-session' } })
      else if (gate === 'incomplete') resolveUpdate({})
      else resolveUpdate({ session: acknowledgement })
      await Promise.resolve()
    })
    expect(apiMock.resumeScriptDebugSession).toHaveBeenCalledTimes(expectedResumes)
  })

  it('continues an acknowledged Run To while keeping runtime privacy suppression sticky', async () => {
    let resolveUpdate!: (value: unknown) => void
    const script = { ...initialScript, actions: [...initialScript.actions, { name: 'Run To target', type: 'GET', target: '/target' }] }
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: heldDebugSession('10') })
    apiMock.getScriptDebugSession.mockReturnValue(new Promise(() => {}))
    apiMock.updateScriptDebugBreakpoints.mockReturnValueOnce(new Promise(resolve => { resolveUpdate = resolve }))
    apiMock.resumeScriptDebugSession.mockReturnValueOnce(new Promise(() => {}))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={script} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Action 2') && button.textContent?.includes('Run To target'))!.click())
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'Run to selected action')!.click())
    const requested = apiMock.updateScriptDebugBreakpoints.mock.calls[0][1]
    const acknowledgement = { ...heldDebugSession('11', 'stale-unmasked-value'), breakpoints: requested }
    const socket = () => (useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage: (message: unknown) => void }).onMessage
    await act(async () => socket()({ type: 'script_debug_breakpoints_updated', debug_session_id: 'debug-session-1', session: { ...heldDebugSession('12', 'private-value'), breakpoints: requested, runtime_evidence_unavailable: true } }))
    await act(async () => { resolveUpdate({ session: acknowledgement }); await Promise.resolve() })
    expect(apiMock.resumeScriptDebugSession).toHaveBeenCalledWith('debug-session-1')
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Live Variables')!.click())
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    expect(container.textContent).not.toContain('private-value')
    expect(container.textContent).not.toContain('stale-unmasked-value')
  })

  it('keeps an explicitly inspected runtime frame source distinct from an older retained event', async () => {
    const event = { phase: 'action', action_index: 0, action_name: 'Earlier request', action_type: 'request', target: '/earlier', success: true, started_at: '2026-10-04T00:00:00Z', completed_at: '2026-10-04T00:00:01Z', duration_ms: 1 }
    const script = { ...initialScript, actions: [initialScript.actions[0], { type: 'http_sequence', name: 'Current sequence', target: '', params: { requests: [{ type: 'request', name: 'Current child', method: 'GET', target: '/child' }] } }] } as ScriptDefinition
    const held = heldDebugSession()
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: { ...held, current_event_index: 0, events: [event], report: { ...held.report, events: [event] }, runtime_state: { ...held.runtime_state, snapshot: { ...held.runtime_state.snapshot, location: { ...held.runtime_state.snapshot.location, action_index: 1, step_path: ['requests[0]'] }, frames: [{ kind: 'phase', phase: 'action', action_index: -1 }, { kind: 'http_sequence', phase: 'action', action_index: 1 }] } } } })
    apiMock.getScriptDebugSession.mockReturnValue(new Promise(() => {}))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={script} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Live Variables')!.click())
    const frame = container.querySelector<HTMLButtonElement>('[aria-label="Inspect runtime frame 2 source"]')
    expect(frame).not.toBeNull()
    expect(container.querySelector('[aria-label="Inspect runtime frame 1 source"]')).toBeNull()
    await act(async () => frame!.click())
    const source = container.querySelector('[aria-label="Source"]')
    expect(source?.querySelector('.script-code-title > span')?.textContent).toBe('actions[1]')
    expect(container.querySelector('.center-tab.active')?.textContent || container.querySelector('.center-tab-panel.active')?.textContent).toContain('Source')
  })

  it('latches a same-incarnation privacy failure from a stale in-flight GET and hides current/history/download detail', async () => {
    let resolveGet!: (value: unknown) => void
    const retained = { phase: 'action', action_index: 0, action_name: 'Captured', action_type: 'request', target: '/captured', success: true, started_at: '2026-10-04T00:00:00Z', completed_at: '2026-10-04T00:00:01Z', duration_ms: 1, body: 'retained-private-body', extractions: { ordinary: 'retained-private-value' } }
    const held = heldDebugSession('10', 'old-held-private-value')
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ session: { ...held, events: [retained], report: { ...held.report, events: [retained] } } })
    apiMock.getScriptDebugSession.mockReturnValueOnce(new Promise(resolve => { resolveGet = resolve }))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    const socket = () => (useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage: (message: unknown) => void }).onMessage
    await act(async () => socket()({ type: 'script_debug_paused', debug_session_id: 'debug-session-1', session: heldDebugSession('11', 'new-held-private-value') }))
    await act(async () => { resolveGet({ session: { ...held, runtime_evidence_unavailable: true } }); await Promise.resolve() })
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Live Variables')!.click())
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    for (const value of ['old-held-private-value', 'new-held-private-value', 'retained-private-body', 'retained-private-value']) expect(container.textContent).not.toContain(value)
    await act(async () => findButtonByTitle(container, 'Download Debug JSON')!.click())
    const exported = downloadTextFileMock.mock.calls[downloadTextFileMock.mock.calls.length - 1][0] as string
    expect(JSON.parse(exported).session.runtime_state).toMatchObject({ state_revision: '11', state: 'unavailable', reason: 'runtime_evidence_unavailable', snapshot: null })
    for (const value of ['old-held-private-value', 'new-held-private-value', 'retained-private-body', 'retained-private-value']) expect(exported).not.toContain(value)
  })

  it('keeps delta-only runtime unavailability sticky across stale session snapshots and clears it for a new run', async () => {
    const retainedReport = {
      group: 'Checkout Flow-debug', protocol: 'http', started_at: '2026-05-14T20:00:00Z', success: true,
      events: [{ phase: 'action', action_index: 0, action_name: 'Read products', action_type: 'GET', target: '/api/products', success: true,
        started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:01Z', duration_ms: 1, body: 'retained-runtime-value', snapshot: { response_body: 'retained-runtime-value', screenshot_base64: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7Z0nQAAAAASUVORK5CYII=' }, protocol_evidence: { detail: 'retained-runtime-value' }, extractions: { ordinary: 'retained-runtime-value' } }],
    }
    apiMock.startScriptDebugSession.mockResolvedValueOnce(debugSessionResponse(retainedReport))
    apiMock.getScriptDebugSession.mockReturnValue(new Promise(() => {}))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    const socket = () => (useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage: (message: unknown) => void }).onMessage
    await act(async () => socket()({ type: 'script_debug_event', debug_session_id: 'debug-session-1', session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: '1', event_index: 0, event: retainedReport.events[0] }))
    const fullSize = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'View full size')
    expect(fullSize).toBeDefined()
    await act(async () => fullSize!.click())
    expect(container.querySelector('[aria-label="Replay Screenshot"]')).not.toBeNull()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Live Variables')!.click())
    await act(async () => { const select = container.querySelector('[aria-label="Variable watches"] select') as HTMLSelectElement; select.value = 'retained'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain('retained-runtime-value')
    await act(async () => socket()({ type: 'script_debug_condition', debug_session_id: 'foreign-session', runtime_evidence_unavailable: true }))
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    await act(async () => socket()({ type: 'script_debug_condition', debug_session_id: 'debug-session-1', session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: '0', runtime_evidence_unavailable: true }))
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    expect(container.querySelector('[aria-label="Replay Screenshot"]')).toBeNull()
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain('retained-runtime-value')
    const terminal = { ...retainedReport, replay_session_id: 'stale-embedded-report', completed_at: '2026-05-14T20:00:02Z', final_variables: { ordinary: 'stale-final-value' }, runtime_evidence_unavailable: false }
    await act(async () => socket()({ type: 'script_debug_completed', debug_session_id: 'debug-session-1', report: terminal,
      session: { ...debugSessionResponse(terminal).session, id: 'stale-embedded-session', runtime_evidence_unavailable: false, logs: [{ message: 'retained-runtime-value' }], raw_output: ['retained-runtime-value'], condition_evidence: { 'action#0#': { left: 'retained-runtime-value', result: true } } } }))
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain('stale-final-value')
    expect(container.querySelector('.replay-variable-timeline')).toBeNull()
    await act(async () => socket()({ type: 'script_debug_completed', debug_session_id: 'debug-session-1', session: { ...debugSessionResponse({ ...terminal, replay_session_id: 'debug-session-1' }).session, ...unavailableRuntimeMetadata('debug-session-1', 'completed', '3') } }))
    // A stale terminal DTO must not restore detail through history or downloads.
    expect(container.textContent).not.toContain('retained-runtime-value')
    expect(container.textContent).not.toContain('stale-final-value')
    await act(async () => findButtonByTitle(container, 'Download Debug JSON')!.click())
    const exported = JSON.parse(downloadTextFileMock.mock.calls[downloadTextFileMock.mock.calls.length - 1][0])
    expect(exported.report.runtime_evidence_unavailable).toBe(true)
    expect(exported.session.runtime_evidence_unavailable).toBe(true)
    expect(exported.report.events[0]).toMatchObject({ phase: 'action', action_index: 0, success: true, duration_ms: 1 })
    expect(JSON.stringify(exported)).not.toContain('retained-runtime-value')
    expect(JSON.stringify(exported)).not.toContain('stale-final-value')
    replaySessionIdMock.mockReturnValueOnce('debug-session-2')
    apiMock.startScriptDebugSession.mockResolvedValueOnce({ ...debugSessionResponse(terminal), replay_session_id: 'debug-session-2', debug_session_id: 'debug-session-2', session: { ...debugSessionResponse({ ...terminal, replay_session_id: 'debug-session-2' }).session, ...unavailableRuntimeMetadata('debug-session-2', 'completed', '3') } })
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).not.toContain('stale-final-value')
    await act(async () => { const select = container.querySelector('[aria-label="Variable watches"] select') as HTMLSelectElement; select.value = 'retained'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.querySelector('[aria-label="Variable watches"]')?.textContent).toContain('stale-final-value')
  })

  it.each(['session', 'report'] as const)('hides retained variables when terminal polling marks %s evidence unavailable', async source => {
    const running = { group: 'Checkout Flow-debug', protocol: 'http', started_at: '2026-05-14T20:00:00Z', success: true, events: [] }
    const terminal = { ...running, completed_at: '2026-05-14T20:00:02Z', final_variables: { ordinary: 'terminal-retained-value' },
      runtime_evidence_unavailable: source === 'report' }
    apiMock.startScriptDebugSession.mockResolvedValueOnce(debugSessionResponse(running))
    apiMock.getScriptDebugSession.mockResolvedValueOnce(debugSessionResponse(terminal, { runtime_evidence_unavailable: source === 'session' }))
    await act(async () => root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />))
    await act(async () => { findDebuggerRunButton(container)!.click(); await Promise.resolve() })
    await flushReact()
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find(button => button.textContent === 'Variables')!.click())
    const watches = container.querySelector('[aria-label="Variable watches"]')
    expect(watches?.textContent).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    expect(watches?.textContent).not.toContain('terminal-retained-value')
    expect(watches?.querySelector('table')).toBeNull()
    expect(watches?.querySelector('details')).toBeNull()
  })

  it('keeps tracking an acknowledged stop until the final WebSocket event arrives', async () => {
    apiMock.startScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'debug-session-1',
      debug_session_id: 'debug-session-1',
      session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'),
        id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http', status: 'running',
        started_at: '2026-05-14T20:00:00Z', current_event_index: -1, events: [],
      },
    })
    apiMock.getScriptDebugSession.mockReturnValueOnce(new Promise(() => {}))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    const runReplayButton = findDebuggerRunButton(container)!
    await act(async () => {
      runReplayButton.click()
      await Promise.resolve()
    })
    await flushReact()

    const socketOptions = useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage?: (message: unknown) => void } | undefined
    await act(async () => {
      socketOptions?.onMessage?.({
        type: 'script_debug_stopped',
        replay_session_id: 'debug-session-1',
        debug_session_id: 'debug-session-1',
        session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'stopped', '2'),
          id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http', status: 'stopped',
          started_at: '2026-05-14T20:00:00Z', current_event_index: -1, events: [],
          report: {
            replay_session_id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http',
            started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:01Z',
            success: false, events: [],
          },
        },
      })
      await Promise.resolve()
    })

    expect(findDebuggerRunButton(container)?.disabled).toBe(true)
    expect(container.textContent).toContain('Stopping debugger')

    const finalReport = {
      replay_session_id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http',
      started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:02Z',
      success: false, stopped: true, error: '', events: [],
    }
    await act(async () => {
      socketOptions?.onMessage?.({
        type: 'script_debug_stopped',
        replay_session_id: 'debug-session-1',
        debug_session_id: 'debug-session-1',
        report: finalReport,
        session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'stopped', (finalReport.completed_at) ? '3' : '2'),
          id: 'debug-session-1', group: finalReport.group, protocol: finalReport.protocol, status: 'stopped',
          started_at: finalReport.started_at, completed_at: finalReport.completed_at,
          current_event_index: -1, events: [], report: finalReport,
        },
      })
      await Promise.resolve()
    })

    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
    expect(container.querySelector('.debugger-statusbar')?.textContent).toContain('Stopped')
    expect(container.textContent).toContain('Restart in step mode')
    expect(container.textContent).not.toContain('Stopping debugger…')
    expect(container.querySelector('.replay-live-badge')?.textContent).toBe('Stopped')
  })

  it('keeps polling an unfinalized stopped session until CompletedAt and the final report are available', async () => {
    apiMock.startScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'debug-session-1',
      debug_session_id: 'debug-session-1',
      session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'running', '1'),
        id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http', status: 'running',
        started_at: '2026-05-14T20:00:00Z', current_event_index: -1, events: [],
      },
    })
    apiMock.getScriptDebugSession
      .mockResolvedValueOnce({
        session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'stopped', '2'),
          id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http', status: 'stopped',
          started_at: '2026-05-14T20:00:00Z', current_event_index: -1, events: [],
          report: {
            replay_session_id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http',
            started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:01Z',
            success: false, events: [],
          },
        },
      })
      .mockResolvedValue({
        session: {
          ...unavailableRuntimeMetadata('debug-session-1', 'stopped', '3'),
          id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http', status: 'stopped',
          started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:02Z',
          current_event_index: -1, events: [],
          report: {
            replay_session_id: 'debug-session-1', group: 'Checkout Flow-debug', protocol: 'http',
            started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:02Z',
            success: false, stopped: true, error: '', events: [],
          },
        },
      })

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.getScriptDebugSession).toHaveBeenCalledTimes(1)
    expect(findDebuggerRunButton(container)?.disabled).toBe(true)
    expect(container.textContent).toContain('Stopping debugger')

    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 1250))
    })
    await flushReact()

    expect(apiMock.getScriptDebugSession.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
  })

  it('reconciles terminal evidence by trusted GET after an early unbound WebSocket and stale running start response', async () => {
    let resolveStart: ((value: unknown) => void) | undefined
    apiMock.startScriptDebugSession.mockReturnValueOnce(new Promise(resolve => {
      resolveStart = resolve
    }))
    apiMock.getScriptRecordingStatus.mockResolvedValueOnce({
      exists: true,
      correlation_usable: true,
      request_count: 1,
      correlation_request_count: 1,
    })

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    const runReplayButton = findDebuggerRunButton(container)
    expect(runReplayButton).toBeDefined()
    await act(async () => {
      runReplayButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const requestedSessionId = apiMock.startScriptDebugSession.mock.calls[0]?.[1]?.replay_session_id as string
    expect(requestedSessionId).toBeTruthy()
    const socketOptions = useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage?: (message: unknown) => void } | undefined
    const terminalReport = {
      replay_session_id: requestedSessionId,
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: false,
      events: [{
        phase: 'action',
        action_index: 0,
        action_name: 'Get Products',
        action_type: 'GET',
        target: '/api/products',
        success: false,
        started_at: '2026-05-14T20:00:00Z',
        completed_at: '2026-05-14T20:00:01Z',
        duration_ms: 1000,
      }],
    }
    apiMock.getScriptDebugSession.mockResolvedValue({ session: { ...debugSessionResponse(terminalReport).session, ...unavailableRuntimeMetadata(requestedSessionId, 'failed', '3') } })
    await act(async () => {
      socketOptions?.onMessage?.({
        type: 'script_debug_failed',
        replay_session_id: requestedSessionId,
        debug_session_id: requestedSessionId,
        report: terminalReport,
        session: {
          ...unavailableRuntimeMetadata(requestedSessionId, 'failed', (terminalReport.completed_at) ? '3' : '2'),
          id: requestedSessionId,
          group: terminalReport.group,
          protocol: terminalReport.protocol,
          status: 'failed',
          started_at: terminalReport.started_at,
          completed_at: terminalReport.completed_at,
          current_event_index: 0,
          events: terminalReport.events,
          report: terminalReport,
        },
      })
      await Promise.resolve()
    })
    await act(async () => {
      resolveStart?.({
        replay_session_id: requestedSessionId,
        debug_session_id: requestedSessionId,
        session: {
          ...unavailableRuntimeMetadata(requestedSessionId, 'running', '1'),
          id: requestedSessionId,
          group: terminalReport.group,
          protocol: terminalReport.protocol,
          status: 'running',
          started_at: terminalReport.started_at,
          current_event_index: -1,
          events: [],
        },
      })
      await Promise.resolve()
    })
    await flushReact()

    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
    expect(container.textContent).not.toContain('Wait for the replay session to finish before diagnosing correlations.')
  })

  it.each([
    ['a network failure', () => new TypeError('network connection closed')],
    ['a 5xx response', () => new ApiError('controller response failed', 503)],
  ])('reconciles an accepted replay after %s before the terminal event', async (_failure, createError) => {
    apiMock.startScriptDebugSession.mockRejectedValueOnce(createError())
    apiMock.getScriptDebugSession.mockImplementationOnce(async (sessionId: string) => ({
      session: {
          ...unavailableRuntimeMetadata(sessionId, 'running', '1'),
        id: sessionId,
        group: 'Checkout Flow-debug',
        protocol: 'http',
        status: 'running',
        started_at: '2026-05-14T20:00:00Z',
        current_event_index: -1,
        events: [],
      },
    }))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()

    const requestedSessionId = apiMock.startScriptDebugSession.mock.calls[0]?.[1]?.replay_session_id as string
    expect(requestedSessionId).toBeTruthy()
    expect(apiMock.getScriptDebugSession).toHaveBeenCalledWith(requestedSessionId)
    expect(findDebuggerRunButton(container)?.disabled).toBe(true)
    expect(container.textContent).toContain('Running · waiting for next action')
    expect(container.textContent).not.toContain('Replay failed before completion')

    const terminalReport = {
      replay_session_id: requestedSessionId,
      group: 'Checkout Flow-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: true,
      events: [],
    }
    const socketOptions = useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage?: (message: unknown) => void } | undefined
    await act(async () => {
      socketOptions?.onMessage?.({
        type: 'script_debug_completed',
        replay_session_id: requestedSessionId,
        debug_session_id: requestedSessionId,
        report: terminalReport,
        session: {
          ...unavailableRuntimeMetadata(requestedSessionId, 'completed', (terminalReport.completed_at) ? '3' : '2'),
          id: requestedSessionId,
          group: terminalReport.group,
          protocol: terminalReport.protocol,
          status: 'completed',
          started_at: terminalReport.started_at,
          completed_at: terminalReport.completed_at,
          current_event_index: -1,
          events: [],
          report: terminalReport,
        },
      })
      await Promise.resolve()
    })
    await flushReact()

    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
    expect(container.textContent).toContain('Passed')
    expect(container.textContent).not.toContain('network connection closed')
    expect(container.textContent).not.toContain('controller response failed')
  })

  it('clears replay state immediately when the start request is definitively rejected', async () => {
    apiMock.startScriptDebugSession.mockRejectedValueOnce(new ApiError('invalid replay options', 422))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.getScriptDebugSession).not.toHaveBeenCalled()
    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
    expect(container.textContent).toContain('invalid replay options')
    expect(container.textContent).toContain('No debugger session yet')
    expect(container.textContent).not.toContain('Confirming replay start')
  })

  it('reconciles terminal evidence by trusted GET after an early unbound WebSocket and ambiguous start failure', async () => {
    let rejectStart: ((reason: unknown) => void) | undefined
    apiMock.startScriptDebugSession.mockReturnValueOnce(new Promise((_resolve, reject) => {
      rejectStart = reject
    }))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    const requestedSessionId = apiMock.startScriptDebugSession.mock.calls[0]?.[1]?.replay_session_id as string
    const socketOptions = useWebSocketMock.mock.calls[useWebSocketMock.mock.calls.length - 1]?.[0] as { onMessage?: (message: unknown) => void } | undefined
    const terminalReport = {
      replay_session_id: requestedSessionId, group: 'Checkout Flow-debug', protocol: 'http',
      started_at: '2026-05-14T20:00:00Z', completed_at: '2026-05-14T20:00:01Z',
      success: true, events: [],
    }
    apiMock.getScriptDebugSession.mockResolvedValue({ session: { ...debugSessionResponse(terminalReport).session, ...unavailableRuntimeMetadata(requestedSessionId, 'failed', '3') } })
    await act(async () => {
      socketOptions?.onMessage?.({
        type: 'script_debug_completed', replay_session_id: requestedSessionId, debug_session_id: requestedSessionId,
        report: terminalReport,
        session: {
          ...unavailableRuntimeMetadata(requestedSessionId, 'completed', (terminalReport.completed_at) ? '3' : '2'),
          id: requestedSessionId, group: terminalReport.group, protocol: terminalReport.protocol, status: 'completed',
          started_at: terminalReport.started_at, completed_at: terminalReport.completed_at,
          current_event_index: -1, events: [], report: terminalReport,
        },
      })
      await Promise.resolve()
    })
    await act(async () => {
      rejectStart?.(new TypeError('network connection closed'))
      await Promise.resolve()
    })
    await flushReact()

    expect(findDebuggerRunButton(container)?.disabled).toBe(false)
    expect(container.textContent).toContain('Passed')
    expect(container.textContent).not.toContain('Replay failed before completion')
    expect(container.textContent).not.toContain('No debugger session yet')
    expect(container.textContent).not.toContain('network connection closed')
  })

  it('resizes debugger panels with accessible splitters without the shell header controls', async () => {
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

    const debuggerShell = container.querySelector('[aria-label="Script Debugger"]')
    expect(debuggerShell).not.toBeNull()
    expect(debuggerShell?.querySelector('.debugger-toolbar')).toBeNull()
    expect(debuggerShell?.textContent).not.toContain('Script Debugger')
    expect(findButtonByTitle(container, 'Reset debugger layout')).toBeUndefined()
    expect(findButtonByTitle(container, 'Maximize')).toBeUndefined()
    expect(findButtonByTitle(container, 'Restore')).toBeUndefined()

    // The merged detail panel is open by default, so its resizer is present.
    const actionsResizer = container.querySelector('[aria-label="Resize actions panel"]') as HTMLDivElement | null
    const panelResizer = container.querySelector('[aria-label="Resize debug panel"]') as HTMLDivElement | null
    const bottomResizer = container.querySelector('[aria-label="Resize bottom panel"]') as HTMLDivElement | null
    expect(actionsResizer).not.toBeNull()
    expect(panelResizer).not.toBeNull()
    expect(bottomResizer).not.toBeNull()
    if (!actionsResizer || !panelResizer || !bottomResizer) {
      return
    }

    expect(actionsResizer.getAttribute('role')).toBe('separator')
    expect(actionsResizer.getAttribute('aria-orientation')).toBe('vertical')
    expect(actionsResizer.getAttribute('aria-valuenow')).toBe('260')
    expect(panelResizer.getAttribute('aria-valuenow')).toBe('320')
    expect(bottomResizer.getAttribute('aria-orientation')).toBe('horizontal')
    expect(bottomResizer.getAttribute('aria-valuenow')).toBe('220')

    await act(async () => {
      actionsResizer.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
      panelResizer.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
      bottomResizer.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
      await Promise.resolve()
    })

    expect(actionsResizer.getAttribute('aria-valuenow')).toBe('284')
    expect(panelResizer.getAttribute('aria-valuenow')).toBe('344')
    expect(bottomResizer.getAttribute('aria-valuenow')).toBe('244')
  })

  it('surfaces script load failures when fetching by id', async () => {
    apiMock.getScriptAuthoring.mockRejectedValueOnce(new Error('Network unavailable'))

    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" />)
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Network unavailable')
  })

  it('keeps protocol read-only when editing an existing script', async () => {
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />)
    })

    const configPanel = container.querySelector('[aria-label="Runtime Settings"]')
    expect(configPanel?.textContent).toContain('Protocol')
    expect(configPanel?.textContent).toContain('HTTP API')
    expect(configPanel?.querySelector('select[aria-label="Protocol"]')).toBeNull()
  })

  it('allows protocol selection only for a blank unsaved draft', async () => {
    await act(async () => {
      root.render(<ScriptEditor />)
    })

    const configPanel = container.querySelector('[aria-label="Runtime Settings"]')
    const protocolSelect = configPanel?.querySelector('select[aria-label="Protocol"]') as HTMLSelectElement | null
    expect(protocolSelect).not.toBeNull()
    expect(protocolSelect?.value).toBe('http')
    expect(Array.from(protocolSelect?.options || []).some(option => option.value === 'jms-servicebus')).toBe(true)
  })

  it('keeps the protocol selector aligned with newer blank script protocols', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={{ ...initialScript, actions: [], protocol: 'kafka' }} />)
    })

    const configPanel = container.querySelector('[aria-label="Runtime Settings"]')
    const protocolSelect = configPanel?.querySelector('select[aria-label="Protocol"]') as HTMLSelectElement | null
    expect(protocolSelect).not.toBeNull()
    expect(protocolSelect?.value).toBe('kafka')
    expect(Array.from(protocolSelect?.options || []).some(option => option.value === 'kafka')).toBe(true)
  })

  it('keeps imported draft protocol read-only even before save', async () => {
    const importedDraft: ScriptDefinition = {
      ...initialScript,
      actions: [],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedDraft} />)
    })

    const configPanel = container.querySelector('[aria-label="Runtime Settings"]')
    expect(configPanel?.textContent).toContain('Protocol')
    expect(configPanel?.textContent).toContain('HTTP API')
    expect(configPanel?.querySelector('select[aria-label="Protocol"]')).toBeNull()
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

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.validateScript).toHaveBeenCalledWith('script-1')
    const readinessCard = findReadinessCard(container)
    expect(readinessCard?.textContent).toContain('Script validation passed with warnings.')
    expect(readinessCard?.textContent).toContain('WebKit execution depends on Playwright assets being installed on the controller host.')
    expect(container.querySelector('.editor-feedback-strip')?.textContent || '').not.toContain('Script validation passed')
  })

  it('validates unsaved saved-script edits as a draft instead of attesting the stored revision', async () => {
    await act(async () => {
      root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findCenterTab(container, 'Source')?.click()
    })
    await act(async () => {
      findButtonByText(container, 'Full Script JSON')?.click()
    })
    const sourceEditor = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement
    await act(async () => {
      changeTextareaValue(sourceEditor, sourceEditor.value.replace('/login', '/login?draft=1'))
    })
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })

    expect(apiMock.validateScript).not.toHaveBeenCalled()
    expect(apiMock.validateDraftScript).toHaveBeenCalledWith(
      expect.objectContaining({
        actions: [expect.objectContaining({ target: '/login?draft=1' })],
      }),
      'project-1',
      undefined,
      undefined,
      { fingerprint: expect.stringMatching(/^sdf1-[0-9a-f]{16}-[0-9a-f]+$/) },
    )
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

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const readinessCard = findReadinessCard(container)
    expect(readinessCard?.textContent).toContain('browser action 1 is missing a selector for browser_click')
    expect(readinessCard?.textContent).toContain('WebKit device profile requires the WebKit engine.')
    expect(container.querySelector('.editor-feedback-strip')?.textContent || '').not.toContain('browser action 1 is missing a selector')
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

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
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
    }), 'project-1', undefined, undefined, { fingerprint: expect.stringMatching(/^sdf1-/) })
    const readinessCard = findReadinessCard(container)
    expect(readinessCard?.textContent).toContain('Script validation passed with warnings.')
    expect(readinessCard?.textContent).toContain('Draft browser config will use Chromium defaults.')
    expect(container.querySelector('.editor-feedback-strip')?.textContent || '').not.toContain('Draft browser config')
  })

  it('validates the resolved draft with unblurred data cells and binds the result to it', async () => {
    const pair = authoringFixture({ ...initialScript, parameters: [{ name: 'username', type: 'file', data_source: 'users', column: 'username' }], data_sources: [{ name: 'users', type: 'csv', strategy: 'sequential', first_row_header: true, inline_data: 'username\nalice' }] })
    apiMock.validateDraftScript.mockResolvedValueOnce({ script_id: '', valid: true, issues: [], warnings: ['bound warning'] })
    await act(async () => { root.render(<KeyboardShortcutProvider><ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} initialFocusedDataSourceName="users" projectId="project-1" /></KeyboardShortcutProvider>) })
    const cell = container.querySelector<HTMLInputElement>('input[aria-label="Row 1, username for users"]')!
    await act(async () => { cell.focus(); changeInputValue(cell, 'pending-cell') })
    await act(async () => { findButtonByTitle(container, 'Recheck script readiness')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushReact()
    expect(apiMock.validateScript).not.toHaveBeenCalled()
    const [sent, , , , options] = apiMock.validateDraftScript.mock.calls[0]
    expect(sent.data_sources[0].inline_data).toContain('pending-cell')
    expect(options.fingerprint).toMatch(/^sdf1-/)
    expect(findReadinessCard(container)?.textContent).toContain('bound warning')
  })

  it('refuses a validation result whose echo does not name the draft it was sent', async () => {
    apiMock.validateDraftScript.mockResolvedValueOnce({ script_id: '', valid: true, issues: [], warnings: ['unbound warning'], validated_fingerprint: 'sdf1-0000000000000000-0' })
    await act(async () => { root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />) })
    await act(async () => { findButtonByTitle(container, 'Recheck script readiness')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushReact()
    const card = findReadinessCard(container)?.textContent || ''
    expect(card).toContain('did not confirm which draft it validated')
    expect(card).not.toContain('unbound warning')
    expect(findButtonByTitle(container, 'Recheck script readiness')?.disabled).toBe(false)
  })

  it('refuses a stored validation that read a different revision than the editor baseline', async () => {
    apiMock.validateScript.mockResolvedValueOnce({ script_id: 'script-1', valid: true, issues: [], warnings: ['newer revision warning'], validated_content_revision: `scr-v1-${'b'.repeat(64)}` })
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />) })
    await act(async () => { findButtonByTitle(container, 'Recheck script readiness')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushReact()
    expect(apiMock.validateScript).toHaveBeenCalledWith('script-1')
    const card = findReadinessCard(container)?.textContent || ''
    expect(card).toContain('stored script changed since this editor loaded it')
    expect(card).not.toContain('newer revision warning')
  })

  it('validates the clean baseline as a draft when the controller attests no stored revision', async () => {
    // An older controller, or a failed durable read, attests no revision.
    apiMock.validateScript.mockResolvedValueOnce({ script_id: 'script-1', valid: true, issues: [], warnings: ['unattested warning'], validated_content_revision: undefined })
    apiMock.validateDraftScript.mockImplementationOnce(async (_script: unknown, _project: unknown, _folder: unknown, _variables: unknown, options?: { fingerprint?: string }) => ({ script_id: '', valid: true, issues: [], warnings: ['draft warning'], validated_fingerprint: options?.fingerprint }))
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} />) })
    await act(async () => { findButtonByTitle(container, 'Recheck script readiness')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flushReact()
    expect(apiMock.validateScript).toHaveBeenCalledWith('script-1')
    expect(apiMock.validateDraftScript).toHaveBeenCalledTimes(1)
    expect(apiMock.validateDraftScript.mock.calls[0][4]).toEqual({ fingerprint: expect.stringMatching(/^sdf1-/) })
    const card = findReadinessCard(container)?.textContent || ''
    expect(card).toContain('draft warning')
    expect(card).not.toContain('unattested warning')
    expect(card).not.toContain('stored script changed')
  })

  it('ignores a validation response when the editable draft changes in flight', async () => {
    const importedScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          source_script_name: 'checkout.usr',
          preserved: {},
        },
      },
    }
    let resolveValidation!: (value: unknown) => void
    apiMock.validateDraftScript.mockReturnValueOnce(new Promise(resolve => {
      resolveValidation = resolve
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} projectId="project-1" />)
    })

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
    const nameInput = container.querySelector<HTMLInputElement>('.side-panel-script-name input')
    expect(validateButton).toBeDefined()
    expect(nameInput).not.toBeNull()
    if (!validateButton || !nameInput) return

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      changeInputValue(nameInput, 'Checkout Flow Edited While Validating')
    })

    await act(async () => {
      resolveValidation({
        script_id: '',
        valid: true,
        issues: [],
        warnings: ['stale validation warning'],
        runtime_settings_parity: [{
          id: 'logging',
          area: 'Logging',
          concept: 'Extended logging',
          status: 'partial',
          runtime_files: ['default.cfg'],
        }],
        runtime_settings_status_counts: { partial: 1 },
      })
    })
    await flushReact()

    expect(nameInput.value).toBe('Checkout Flow Edited While Validating')
    expect(findReadinessCard(container)?.textContent || '').not.toContain('stale validation warning')
    expect(findButtonByTitle(container, 'Recheck script readiness')?.disabled).toBe(false)
  })

  it('ignores a failed validation once the editable draft changed in flight', async () => {
    let fail!: (reason: unknown) => void
    apiMock.validateDraftScript.mockReturnValueOnce(new Promise((_, reject) => { fail = reject }))
    await act(async () => { root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />) })
    await act(async () => { findButtonByTitle(container, 'Recheck script readiness')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => { changeInputValue(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!, 'Edited while a failing validation ran') })
    await act(async () => { fail(new Error('late validation failure')) })
    await flushReact()
    expect(findReadinessCard(container)?.textContent || '').not.toContain('late validation failure')
    expect(findButtonByTitle(container, 'Recheck script readiness')?.disabled).toBe(false)
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

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const readinessCard = findReadinessCard(container)
    expect(readinessCard?.textContent).toContain('Authoring Health')
    expect(readinessCard?.textContent).toContain('Blocked · 58')
    expect(readinessCard?.textContent).toContain('Data-Source Fit')
    expect(readinessCard?.textContent).toContain('Unique data usage requires more rows than the data source provides.')
    expect(readinessCard?.textContent).toContain('Source users · Parameter login_email · Column email · 3/10 rows')
    expect(readinessCard?.textContent).toContain('AI Draft Guardrails')
    expect(readinessCard?.textContent).toContain('AI-generated drafts require replay evidence before promotion.')
    expect(container.querySelector('.editor-feedback-strip')?.textContent || '').not.toContain('Authoring Health')
  })

  it('does not mark Approval readiness ready when the completed replay failed', async () => {
    const browserScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'browser',
      config: {
        ...initialScript.config,
        browser: { engine: 'chromium', headless: true },
      },
      actions: [{
        name: 'Open home',
        type: 'browser_navigate',
        target: '/',
        selector: '',
        value: '',
      }],
    }
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce(debugSessionResponse({
      group: 'Checkout Flow-debug',
      protocol: 'browser',
      started_at: '2026-07-23T12:00:00Z',
      completed_at: '2026-07-23T12:00:01Z',
      success: false,
      error: 'browser action failed',
      events: [{
        action_name: 'Open home',
        action_type: 'browser_navigate',
        success: false,
        error_message: 'navigation failed',
      }],
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={browserScript} initiallyDirty projectId="project-1" />)
    })

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
    expect(validateButton).toBeDefined()
    await act(async () => {
      validateButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    await act(async () => {
      findOpenDebuggerButton(container)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const replayButton = findDebuggerRunButton(container, 'Run Browser Replay')
    expect(replayButton).toBeDefined()
    await act(async () => {
      replayButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()

    await act(async () => {
      findWorkspaceTab(container, 'Author')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const approvalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(approvalItem?.textContent).toContain('needs evidence')
    expect(approvalItem?.textContent).not.toContain('ready')
    expect(approvalItem?.querySelector('button')?.disabled).toBe(true)

  })

  it('does not mark Approval readiness ready when backend validation failed', async () => {
    apiMock.validateDraftScript.mockResolvedValueOnce({
      script_id: '',
      valid: false,
      issues: ['Action 1 is invalid.'],
      warnings: [],
    })
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.click()
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()
    await act(async () => {
      findWorkspaceTab(container, 'Author')?.click()
    })
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })

    const approvalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(approvalItem?.textContent).toContain('needs evidence')
    expect(approvalItem?.textContent).not.toContain('ready')

  })

  it('does not carry approval evidence across project scope changes', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.click()
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-2" />)
    })
    await act(async () => {
      findWorkspaceTab(container, 'Author')?.click()
    })
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })
    const approvalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(approvalItem?.textContent).toContain('needs evidence')
    expect(approvalItem?.textContent).not.toContain('ready')
    const debugEvidenceItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Debug Evidence'))
    expect(debugEvidenceItem?.textContent).toContain('missing')
  })

  it('requires a new replay after a configuration change even when the edited draft revalidates', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initiallyDirty projectId="project-1" />)
    })
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.click()
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()
    await act(async () => {
      findWorkspaceTab(container, 'Author')?.click()
    })
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })
    const readyApprovalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(readyApprovalItem?.textContent).toContain('ready')
    await act(async () => {
      findCenterTab(container, 'Source')?.click()
    })
    await act(async () => {
      findButtonByText(container, 'Full Script JSON')?.click()
    })
    const sourceEditor = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement
    // Only the configuration changes; the actions the replay ran are the same.
    await act(async () => {
      changeTextareaValue(sourceEditor, sourceEditor.value.replace('https://api.example.com', 'https://api2.example.com'))
    })
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })
    await flushReact()
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })
    const approvalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(approvalItem?.textContent).toContain('needs evidence')
    const debugEvidenceItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Debug Evidence'))
    expect(debugEvidenceItem?.textContent).toContain('stale')
  })

  it('keeps an imported script\'s debug evidence when a recheck only adds derived metadata', async () => {
    const importedScript = { ...initialScript, metadata: { ...(initialScript.metadata || {}), import: { source: 'openapi' } } } as ScriptDefinition
    await act(async () => {
      root.render(<ScriptEditor initialScript={importedScript} initiallyDirty projectId="project-1" />)
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.click()
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })
    await flushReact()
    await act(async () => {
      findWorkspaceTab(container, 'Author')?.click()
    })
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })
    const debugEvidenceItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Debug Evidence'))
    expect(debugEvidenceItem?.textContent).toContain('passed')
    expect(debugEvidenceItem?.textContent).not.toContain('stale')
  })

  it('requires a new replay after actions change even when the edited draft revalidates', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initiallyDirty projectId="project-1" />)
    })
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.click()
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()

    await act(async () => {
      findWorkspaceTab(container, 'Author')?.click()
    })
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })
    const readyApprovalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(readyApprovalItem?.textContent).toContain('ready')
    expect(readyApprovalItem?.querySelector('button')?.disabled).toBe(false)
    await act(async () => {
      findCenterTab(container, 'Source')?.click()
    })
    await act(async () => {
      findButtonByText(container, 'Full Script JSON')?.click()
    })
    const sourceEditor = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement
    expect(sourceEditor.value).toContain('/login')
    await act(async () => {
      changeTextareaValue(sourceEditor, sourceEditor.value.replace('/login', '/login?fresh=1'))
    })
    await act(async () => {
      findButtonByTitle(container, 'Recheck script readiness')?.click()
      await Promise.resolve()
    })
    await flushReact()
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })

    const approvalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(approvalItem?.textContent).toContain('needs evidence')
    expect(approvalItem?.textContent).not.toContain('ready')
    expect(approvalItem?.querySelector('button')?.disabled).toBe(true)

    await act(async () => {
      findCenterTab(container, 'Source')?.click()
    })
    await act(async () => {
      findButtonByText(container, 'Full Script JSON')?.click()
    })
    const editedAgain = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement
    await act(async () => {
      changeTextareaValue(editedAgain, editedAgain.value.replace('/login?fresh=1', '/login?fresh=2'))
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.click()
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })
    await flushReact()
    await act(async () => {
      findWorkspaceTab(container, 'Author')?.click()
    })
    await act(async () => {
      findCenterTab(container, 'Runtime Settings')?.click()
    })
    const validationStaleApprovalItem = Array.from(container.querySelectorAll<HTMLElement>('.utility-checklist-item'))
      .find(item => item.textContent?.includes('Approval readiness'))
    expect(validationStaleApprovalItem?.textContent).toContain('needs evidence')
    expect(validationStaleApprovalItem?.textContent).not.toContain('ready')
    expect(validationStaleApprovalItem?.querySelector('button')?.disabled).toBe(true)
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

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const configurationTab = findCenterTab(container, 'Runtime Settings')
    expect(configurationTab).toBeDefined()
    await act(async () => {
      configurationTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const readinessCard = findReadinessCard(container)
    expect(readinessCard?.textContent).toContain('Variable readiness')
    expect(readinessCard?.textContent).toContain('Missing variable: backend_only_account')
  })

  it('refreshes imported runtime parity metadata from validation responses', async () => {
    const importedScript: ScriptDefinition = {
      ...initialScript,
      metadata: {
        import: {
          source: 'enterprise_script_archive',
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
    expect(container.textContent).toContain('Partial: Logging')
    expect(container.textContent).not.toContain('Executed: Run Logic')

    const validateButton = findButtonByTitle(container, 'Recheck script readiness')
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('80%')
    expect(container.textContent).toContain('Executed: Run Logic')
    expect(container.textContent).toContain('Partial: Logging')
    expect(container.textContent).toContain('No imported runtime settings matched the selected block statuses: blocked.')
  })

  it('deduplicates rapid saves before React commits the pending state', async () => {
    let resolveSave!: (value: { id: string }) => void
    apiMock.createScript.mockReturnValueOnce(new Promise(resolve => {
      resolveSave = resolve
    }))

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initiallyDirty projectId="project-1" />)
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')!
    await act(async () => {
      saveButton.click()
      saveButton.click()
    })

    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveSave({ id: 'saved-once' })
      await Promise.resolve()
    })
  })

  it('does not let an older save response overwrite a newly opened project document', async () => {
    let resolveSave!: (value: { id: string }) => void
    const onSave = vi.fn()
    apiMock.createScript.mockReturnValueOnce(new Promise(resolve => {
      resolveSave = resolve
    }))
    const nextScript: ScriptDefinition = {
      ...initialScript,
      name: 'Project Two Flow',
      actions: [{ ...initialScript.actions[0], target: '/project-two' }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} initiallyDirty projectId="project-1" onSave={onSave} />)
    })
    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')!
    await act(async () => {
      saveButton.click()
      await Promise.resolve()
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={nextScript} initiallyDirty projectId="project-2" onSave={onSave} />)
    })
    await act(async () => {
      resolveSave({ id: 'old-project-save' })
      await Promise.resolve()
    })
    await flushReact()

    expect(container.querySelector<HTMLInputElement>('.side-panel-script-name input')?.value).toBe('Project Two Flow')
    expect(onSave).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('Script saved successfully!')
  })

  it('does not let an older replay start response attach to a newly opened project document', async () => {
    let resolveReplay!: (value: ReturnType<typeof debugSessionResponse>) => void
    apiMock.startDraftScriptDebugSession.mockReturnValueOnce(new Promise(resolve => {
      resolveReplay = resolve
    }))
    const nextScript: ScriptDefinition = {
      ...initialScript,
      name: 'Project Two Replay',
      actions: [{ ...initialScript.actions[0], target: '/project-two' }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} projectId="project-1" />)
    })
    await act(async () => {
      findOpenDebuggerButton(container)?.click()
    })
    await act(async () => {
      findDebuggerRunButton(container)?.click()
      await Promise.resolve()
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={nextScript} projectId="project-2" />)
    })
    await act(async () => {
      resolveReplay(debugSessionResponse({
        group: 'Old project replay',
        protocol: 'http',
        started_at: '2026-07-23T12:00:00Z',
        completed_at: '2026-07-23T12:00:01Z',
        success: true,
        events: [],
      }))
      await Promise.resolve()
    })
    await flushReact()

    expect(container.textContent).not.toContain('Old project replay')
    expect(container.textContent).not.toContain('Debugger completed successfully')
    await act(async () => { findWorkspaceTab(container, 'Author')?.click() })
    expect(container.querySelector<HTMLInputElement>('.side-panel-script-name input')?.value).toBe('Project Two Replay')
  })

  it('retains the selected action and undo history when save returns a normalized document', async () => {
    const savedDefinition = { ...initialScript, version: '1.0.1' }
    apiMock.updateScript.mockResolvedValueOnce(savedAuthoringFixture(savedDefinition))
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} initialFocusedActionName="Login" />) })
    const name = container.querySelector<HTMLInputElement>('.side-panel-script-name input')!
    await act(async () => { changeInputValue(name, 'Edited locally') })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click(); await Promise.resolve() })
    await flushReact()
    // The parent supplies the API response back as initialScript after saving.
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={savedDefinition} />) })
    expect(container.querySelector<HTMLInputElement>('.author-main-action [data-author-field="name"]')?.value).toBe('Login')
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Undo document change"]')?.disabled).toBe(false)
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')?.disabled).toBe(true)
  })

  it('conditions an existing save on its exact loaded baseline', async () => {
    const pair = authoringFixture(initialScript)
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} projectId="project-1" />) })
    await act(async () => { changeInputValue(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!, 'Conditioned draft') })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    await flushReact()
    expect(apiMock.updateScript).toHaveBeenCalledWith('script-1', expect.objectContaining({ name: 'Conditioned draft' }), pair)
    expect(apiMock.getScriptAuthoring).not.toHaveBeenCalled()
  })

  it('retains structured edits made during a save and its canonical parent echo', async () => {
    const pair = authoringFixture(initialScript)
    let complete!: (saved: ReturnType<typeof savedAuthoringFixture>) => void
    apiMock.updateScript.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const onSave = vi.fn()
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} projectId="project-1" onSave={onSave} />) })
    const name = () => container.querySelector<HTMLInputElement>('.side-panel-script-name input')!
    await act(async () => { changeInputValue(name(), 'Sent draft') })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    expect(apiMock.updateScript).toHaveBeenCalledTimes(1)
    await act(async () => { changeInputValue(name(), 'Edited while saving') })
    const saved = savedAuthoringFixture({ ...pair.projection.script, name: 'Sent draft' })
    await act(async () => { complete(saved) })
    await flushReact()
    expect(name().value).toBe('Edited while saving')
    expect(onSave).toHaveBeenCalledWith(saved, expect.objectContaining({ retainedDraft: expect.objectContaining({ name: 'Edited while saving' }) }))
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={saved.authoring_projection!.script} initialAuthoring={{ projection: saved.authoring_projection!, etag: `"script-authoring-v1:${saved.content_revision}"` }} projectId="project-1" onSave={onSave} />) })
    expect(name().value).toBe('Edited while saving')
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.disabled).toBe(false)
  })

  it.each([new ApiError('stale', 412), new ApiError('unknown outcome', 503, { code: 'script_save_outcome_unknown' }), new ApiError('audit unavailable', 503, { code: 'script_save_outcome_audit_unavailable' }), new Error('lost response')])('preserves the draft and refuses retry until explicit baseline review: %s', async cause => {
    const pair = authoringFixture(initialScript)
    apiMock.updateScript.mockRejectedValueOnce(cause)
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} projectId="project-1" />) })
    const name = () => container.querySelector<HTMLInputElement>('.side-panel-script-name input')!
    await act(async () => { changeInputValue(name(), 'Retained local draft') })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    await flushReact()
    expect(name().value).toBe('Retained local draft')
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.disabled).toBe(true)
    expect(apiMock.getScriptAuthoring).not.toHaveBeenCalled()
    const newer = authoringFixture({ ...pair.projection.script, config: { ...pair.projection.script.config, base_url: '/new-server' }, folder_id: 'new-folder' }, 'script-1', 'project-1', 'c')
    apiMock.getScriptAuthoring.mockResolvedValueOnce(newer)
    await act(async () => { findButtonByText(container, 'Load current baseline')!.click() })
    await flushReact()
    expect(name().value).toBe('Retained local draft')
    expect(apiMock.updateScript).toHaveBeenCalledTimes(1)
    await act(async () => { findButtonByText(container, 'Diff')!.click() })
    expect(container.querySelector('[aria-label="Script revision field diff"]')!.textContent).toContain('name')
    expect(container.querySelector('[aria-label="Script revision field diff"]')!.textContent).not.toContain('Retained local draft')
    await act(async () => { findButtonByText(container, 'Reapply draft')!.click() })
    expect(name().value).toBe('Retained local draft')
    expect(apiMock.updateScript).toHaveBeenCalledTimes(1)
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    await flushReact()
    expect(apiMock.updateScript).toHaveBeenLastCalledWith('script-1', expect.objectContaining({ name: 'Retained local draft', folder_id: 'new-folder', config: expect.objectContaining({ base_url: '/new-server' }) }), newer)
  })

  it('retains JSON edits after a conflict and discards only on explicit reload', async () => {
    const pair = authoringFixture(initialScript)
    apiMock.updateScript.mockRejectedValueOnce(new ApiError('stale', 412))
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} projectId="project-1" />) })
    await act(async () => { findCenterTab(container, 'Source')!.click() })
    const editor = () => container.querySelector<HTMLTextAreaElement>('[data-testid="codemirror-mock"]')!
    await act(async () => { changeTextareaValue(editor(), editor().value.replace('/login', '/login?local=1')) })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    await flushReact()
    expect(editor().value).toContain('/login?local=1')
    apiMock.getScriptAuthoring.mockResolvedValueOnce(authoringFixture({ ...pair.projection.script, name: 'Remote revision' }, 'script-1', 'project-1', 'd'))
    await act(async () => { findButtonByText(container, 'Load current baseline')!.click() })
    await flushReact()
    expect(editor().value).toContain('/login?local=1')
    await act(async () => { changeTextareaValue(editor(), '{"name":"Incomplete conflicting draft",') })
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.disabled).toBe(true)
    await act(async () => { findButtonByText(container, 'Reload script and discard draft')!.click() })
    expect(editor().value).not.toContain('/login?local=1')
    expect(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!.value).toBe('Remote revision')
  })

  it('does not admit a late baseline into a different editor project', async () => {
    let complete!: (pair: ReturnType<typeof authoringFixture>) => void
    apiMock.getScriptAuthoring.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={initialScript} initialAuthoring={null} projectId="project-1" />) })
    const other = authoringFixture({ ...initialScript, name: 'Other project' }, 'script-2', 'project-2', 'c')
    await act(async () => { root.render(<ScriptEditor scriptId="script-2" initialScript={other.projection.script} initialAuthoring={other} projectId="project-2" />) })
    await act(async () => { complete(authoringFixture(initialScript)) })
    expect(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!.value).toBe('Other project')
    expect(container.textContent).not.toContain('server baseline loaded after')
    expect(apiMock.updateScript).not.toHaveBeenCalled()
  })

  it('adopts version/ref-key normalization after its own unblurred cell flush', async () => {
    const pair = authoringFixture({ ...initialScript, parameters: [{ name: 'username', type: 'file', data_source: 'users', column: 'username' }], data_sources: [{ name: 'users', type: 'csv', strategy: 'sequential', first_row_header: true, inline_data: 'username\nalice' }] })
    const onSave = vi.fn()
    apiMock.updateScript.mockImplementationOnce(async (id: string, draft: ScriptDefinition) => savedAuthoringFixture({ ...draft, version: '2.0', ref_key: 'NORMALIZED' }, id))
    await act(async () => { root.render(<KeyboardShortcutProvider><ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} initialFocusedDataSourceName="users" projectId="project-1" onSave={onSave} /></KeyboardShortcutProvider>) })
    const cell = container.querySelector<HTMLInputElement>('input[aria-label="Row 1, username for users"]')!
    await act(async () => { cell.focus(); changeInputValue(cell, 'flushed-value') })
    await act(async () => { cell.dispatchEvent(new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true })) })
    await flushReact()
    expect(apiMock.updateScript.mock.calls[0][1].data_sources[0].inline_data).toContain('flushed-value')
    expect(onSave.mock.calls[0]).toHaveLength(1)
    expect(onSave.mock.calls[0][0].script).toMatchObject({ version: '2.0', ref_key: 'NORMALIZED' })
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.disabled).toBe(true)
  })

  it('retains a focused cell typed during save through canonical parent echo and the next save', async () => {
    const pair = authoringFixture({ ...initialScript, parameters: [{ name: 'username', type: 'file', data_source: 'users', column: 'username' }], data_sources: [{ name: 'users', type: 'csv', strategy: 'sequential', first_row_header: true, inline_data: 'username\nalice' }] })
    let complete!: (saved: ReturnType<typeof savedAuthoringFixture>) => void
    apiMock.updateScript.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const onSave = vi.fn()
    const render = async (document = pair.projection.script, baseline = pair, retainedDraft?: ScriptDefinition) => { await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={document} initialAuthoring={baseline} initialRetainedDraft={retainedDraft} initialFocusedDataSourceName="users" projectId="project-1" onSave={onSave} />) }) }
    await render()
    await act(async () => { changeInputValue(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!, 'Sent cell draft') })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    const cell = () => container.querySelector<HTMLInputElement>('input[aria-label="Row 1, username for users"]')!
    await act(async () => { cell().focus(); changeInputValue(cell(), 'late-focused-value') })
    const saved = savedAuthoringFixture({ ...pair.projection.script, name: 'Sent cell draft', version: '2.0', ref_key: 'NORMALIZED' })
    await act(async () => { complete(saved) })
    await flushReact()
    const retained = onSave.mock.calls[0][1].retainedDraft
    expect(retained.data_sources[0].inline_data).toContain('late-focused-value')
    await render(saved.authoring_projection!.script, { projection: saved.authoring_projection!, etag: `"script-authoring-v1:${saved.content_revision}"` }, retained)
    expect(cell().value).toBe('late-focused-value')
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    await flushReact()
    expect(apiMock.updateScript.mock.calls[1][1].data_sources[0].inline_data).toContain('late-focused-value')
    expect(apiMock.updateScript.mock.calls[1][1]).toMatchObject({ version: '2.0', ref_key: 'NORMALIZED' })
  })

  it.each(['structured', 'invalid JSON'])('retains %s edits through the real create-to-edit surface remount', async mode => {
    let complete!: (saved: ReturnType<typeof savedAuthoringFixture>) => void
    apiMock.createScript.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    function Surface() {
      const [ack, setAck] = useState<{ saved: StoredScript; options?: ScriptSaveOptions } | null>(null)
      const pair = ack ? { projection: ack.saved.authoring_projection!, etag: `"script-authoring-v1:${ack.saved.content_revision}"` } : null
      return <Suspense fallback={<div>Loading editor</div>}><ScriptsAuthoringSurface subView={ack ? 'edit' : 'create'}
        draftScript={initialScript} draftInitiallyDirty={true} draftEditorFocus={null} draftReturnTarget={null} authoringNotice={null}
        editingId={ack?.saved.id || null} editingScript={pair?.projection.script || null} editingAuthoring={pair}
        editingRetainedDraft={ack?.options?.retainedDraft} editingRetainedJsonText={ack?.options?.retainedJsonText}
        projectId="project-1" onSaved={(saved, options) => setAck({ saved, options })} onOpenLibrary={vi.fn()} onOpenCreateOptions={vi.fn()} onImported={vi.fn()} onEditDraft={vi.fn()} onReturnToDraftSource={vi.fn()} /></Suspense>
    }
    await act(async () => { root.render(<Surface />) })
    await flushReact()
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
    if (mode === 'structured') {
      await act(async () => { changeInputValue(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!, 'Late created draft') })
    } else {
      await act(async () => { findCenterTab(container, 'Source')!.click() })
      const text = container.querySelector<HTMLTextAreaElement>('[data-testid="codemirror-mock"]')!
      await act(async () => { changeTextareaValue(text, '{"name":"Incomplete local draft",') })
    }
    await act(async () => { complete(savedAuthoringFixture(initialScript, 'created-script')) })
    await flushReact()
    if (mode === 'structured') {
      expect(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!.value).toBe('Late created draft')
      expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.disabled).toBe(false)
    } else {
      await act(async () => { findCenterTab(container, 'Source')!.click() })
      expect(container.querySelector<HTMLTextAreaElement>('[data-testid="codemirror-mock"]')!.value).toBe('{"name":"Incomplete local draft",')
      expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.disabled).toBe(true)
    }
    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
  })

  it('retains failed canonical-read handoff without an implicit read or write retry', async () => {
    await act(async () => { root.render(<ScriptEditor scriptId="saved-retained" initialScript={{ ...initialScript, name: 'Retained local snapshot' }} initialAuthoring={null}
      initialRetainedDraft={{ ...initialScript, name: 'Retained local snapshot' }} initialRetainedNeedsReview={true}
      initialRetainedJsonText={'{"name":"Incomplete retained JSON",'} initialRetainedCreatedSecretNames={['UNACKNOWLEDGED_ENTRY']} projectId="project-1" />) })
    await flushReact()
    expect(apiMock.getScriptAuthoring).not.toHaveBeenCalled()
    expect(apiMock.updateScript).not.toHaveBeenCalled()
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.disabled).toBe(true)
    await act(async () => { findCenterTab(container, 'Source')!.click() })
    expect(container.querySelector<HTMLTextAreaElement>('[data-testid="codemirror-mock"]')!.value).toBe('{"name":"Incomplete retained JSON",')
    apiMock.getScriptAuthoring.mockResolvedValueOnce(authoringFixture({ ...initialScript, name: 'Current server document' }, 'saved-retained'))
    await act(async () => { findButtonByText(container, 'Load current baseline')!.click() })
    await flushReact()
    expect(apiMock.getScriptAuthoring).toHaveBeenCalledTimes(1)
    await act(async () => { findButtonByText(container, 'Reload script and discard draft')!.click() })
    expect(container.querySelector<HTMLTextAreaElement>('[data-testid="codemirror-mock"]')!.value).toContain('Current server document')
    expect(apiMock.updateScript).not.toHaveBeenCalled()
  })

  it('uses the observed application placement for editor secret review', async () => {
    const authoredDocument = { ...initialScript, actions: [{ name: 'Auth', type: 'request', method: 'GET', target: '/', headers: { Authorization: 'Bearer {{secret.APP_REFERENCE}}' } }] }
    const pair = authoringFixture(authoredDocument)
    pair.projection.library_placement.application_id = 'observed-application'
    apiMock.getScriptSecretStatus.mockResolvedValue({ required: ['APP_REFERENCE'], missing: ['APP_REFERENCE'], configured: [], bindings: [] })
    apiMock.getApplicationSecretStatuses.mockResolvedValue({ secrets: [{ name: 'APP_REFERENCE', configured: false }] })
    apiMock.listApplicationSecrets.mockResolvedValue({ secrets: [] })
    apiMock.getApplicationSecretUsage.mockResolvedValue({ name: 'APP_REFERENCE', usage_revision: 'app-usage', consumer_count: 0, reference_count: 0, consumers: null })
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} projectId="project-1" />) })
    await flushReact()
    await act(async () => { findButtonByText(container, 'Configure secrets')!.click() })
    await flushReact()
    expect(apiMock.getApplicationSecretStatuses).toHaveBeenCalledWith('observed-application', ['APP_REFERENCE'])
    expect(apiMock.listApplicationSecrets).toHaveBeenCalledWith('observed-application')
    expect(apiMock.getApplicationSecretUsage).toHaveBeenCalledWith('observed-application', 'APP_REFERENCE', { limit: 50 })
    expect(document.body.textContent).toContain('Use existing application or project secret')
  })

  it('keeps created-name provenance within the actual editor resource', async () => {
    const first = authoringFixture(initialScript)
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={first.projection.script} initialAuthoring={first} initialRetainedCreatedSecretNames={['A_DEDICATED_VALUE']} projectId="project-1" />) })
    const next = authoringFixture({ ...initialScript, name: 'Other resource' }, 'script-2', 'project-2')
    await act(async () => { root.render(<ScriptEditor scriptId="script-2" initialScript={next.projection.script} initialAuthoring={next} initialRetainedCreatedSecretNames={['B_DEDICATED_VALUE']} projectId="project-2" />) })
    apiMock.updateScript.mockRejectedValueOnce(new ApiError('Definite validation rejection', 422))
    await act(async () => { changeInputValue(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!, 'B retained draft') })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    await flushReact()
    expect(container.textContent).toContain('B_DEDICATED_VALUE was already created')
    expect(container.textContent).not.toContain('A_DEDICATED_VALUE')
  })

  it('rejects an old saved callback after secret-status refresh crosses editor scope', async () => {
    const pair = authoringFixture(initialScript)
    let complete!: (value: unknown) => void
    apiMock.getScriptSecretStatus.mockResolvedValueOnce({ required: [], missing: [], configured: [] }).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const onSave = vi.fn()
    await act(async () => { root.render(<ScriptEditor scriptId="script-1" initialScript={pair.projection.script} initialAuthoring={pair} projectId="project-1" onSave={onSave} />) })
    await act(async () => { changeInputValue(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!, 'Previous resource saved') })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Save script"]')!.click() })
    expect(apiMock.getScriptSecretStatus).toHaveBeenCalledTimes(2)
    const next = authoringFixture({ ...initialScript, name: 'New resource' }, 'script-2', 'project-2')
    await act(async () => { root.render(<ScriptEditor scriptId="script-2" initialScript={next.projection.script} initialAuthoring={next} projectId="project-2" onSave={onSave} />) })
    await act(async () => { complete({ required: [], missing: [], configured: [] }) })
    expect(onSave).not.toHaveBeenCalled()
    expect(container.querySelector<HTMLInputElement>('.side-panel-script-name input')!.value).toBe('New resource')
  })

  it('passes the stored script response through the save callback', async () => {
    const onSave = vi.fn()
    apiMock.createScript.mockResolvedValueOnce({
      id: 'script-saved-1',
      name: 'Saved Callback Flow',
      protocol: initialScript.protocol,
      version: initialScript.version,
      created_at: '2026-05-17T00:00:00Z',
      script: { ...initialScript, name: 'Saved Callback Flow' },
    })

    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} onSave={onSave} />)
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }
    expect(saveButton.disabled).toBe(true)
    expect((saveButton.getAttribute('data-tooltip') || saveButton.getAttribute('title'))).toBe('No unsaved changes to save')

    const nameInput = container.querySelector<HTMLInputElement>('.side-panel-script-name input[placeholder="My_Test_Script"]')
    expect(nameInput).toBeDefined()
    if (!nameInput) {
      return
    }

    await act(async () => {
      changeInputValue(nameInput, 'Saved Callback Flow')
      await Promise.resolve()
    })

    expect(saveButton.disabled).toBe(false)
    expect((saveButton.getAttribute('data-tooltip') || saveButton.getAttribute('title'))).toBe('Save script')

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      id: 'script-saved-1',
      name: 'Saved Callback Flow',
    }), { retainedDraft: expect.objectContaining({ name: 'Saved Callback Flow', actions: initialScript.actions }) })
    expect(container.textContent).toContain('Script saved successfully!')
  })

  it('queues a new runner-backed Java Vuser script for approval instead of creating it', async () => {
    // POST /scripts answers 409 for a java_vuser script with java_vuser_run
    // actions, so saving one from the editor was simply broken. It now creates
    // a governed authoring session; approval stays a separate, explicit act.
    const onSave = vi.fn()
    apiMock.createAuthoringSession.mockResolvedValueOnce({
      id: 'java-authoring-session-1',
      source: 'manual',
      name: 'Java Checkout Flow',
      status: 'reviewing',
      created_at: '2026-05-17T00:00:00Z',
      updated_at: '2026-05-17T00:00:00Z',
      last_accessed_at: '2026-05-17T00:00:00Z',
    })

    const javaScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'java_vuser',
      name: 'Java Checkout Flow',
      actions: [{ name: 'Java action', type: 'java_vuser_run', target: '', params: { method: 'action' } }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={javaScript} initiallyDirty projectId="project-1" onSave={onSave} />)
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButton).toBeTruthy()
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(apiMock.createAuthoringSession).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      source: 'manual',
      status: 'reviewing',
      name: 'Java Checkout Flow',
      draft: expect.objectContaining({ protocol: 'java_vuser', name: 'Java Checkout Flow' }),
      idempotency_key: expect.stringContaining('script-editor-java-vuser'),
    }))
    // No library script exists yet, so onSave must not fire and the copy must
    // not claim the script was saved.
    expect(onSave).not.toHaveBeenCalled()
    expect(container.textContent).toContain('queued for approval')
    expect(container.textContent).not.toContain('Script saved successfully!')

    // The editor deliberately stays dirty: a queued session is not a library
    // script, so the baseline must not move and the navigation guard must stay
    // armed. Clearing it here would make the editor claim work was saved that
    // does not exist until someone approves it.
    const saveButtonAfter = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    expect(saveButtonAfter?.disabled).toBe(false)
    expect((saveButtonAfter?.getAttribute('data-tooltip') || saveButtonAfter?.getAttribute('title'))).toBe('Save script')
  })

  it('carries the editor folder onto the authoring session', async () => {
    // The whole backend folder_id path exists to receive this. Without it every
    // reviewed Java script is filed at the project root regardless of where the
    // author was working.
    apiMock.createAuthoringSession.mockResolvedValueOnce({
      id: 'java-authoring-session-folder',
      source: 'manual',
      name: 'Java Folder Flow',
      status: 'reviewing',
      created_at: '2026-05-17T00:00:00Z',
      updated_at: '2026-05-17T00:00:00Z',
      last_accessed_at: '2026-05-17T00:00:00Z',
    })

    const javaScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'java_vuser',
      name: 'Java Folder Flow',
      actions: [{ name: 'Java action', type: 'java_vuser_run', target: '', params: { method: 'action' } }],
    }

    await act(async () => {
      root.render(
        <ScriptEditor initialScript={javaScript} initiallyDirty projectId="project-1" folderId="folder-7" />,
      )
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.createAuthoringSession).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      folder_id: 'folder-7',
    }))
  })

  it('reuses one authoring session when an unchanged Java draft is saved twice', async () => {
    // The content-derived idempotency key is what stops a re-save (or a lost
    // reply) from opening a second review session for the same draft.
    apiMock.createAuthoringSession.mockResolvedValue({
      id: 'java-authoring-session-repeat',
      source: 'manual',
      name: 'Java Repeat Flow',
      status: 'reviewing',
      created_at: '2026-05-17T00:00:00Z',
      updated_at: '2026-05-17T00:00:00Z',
      last_accessed_at: '2026-05-17T00:00:00Z',
    })

    const javaScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'java_vuser',
      name: 'Java Repeat Flow',
      actions: [{ name: 'Java action', type: 'java_vuser_run', target: '', params: { method: 'action' } }],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={javaScript} initiallyDirty projectId="project-1" />)
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    for (let attempt = 0; attempt < 2; attempt++) {
      await act(async () => {
        saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        await Promise.resolve()
      })
      await flushReact()
    }

    expect(apiMock.createAuthoringSession).toHaveBeenCalledTimes(2)
    const [first, second] = apiMock.createAuthoringSession.mock.calls
    expect(first[0].idempotency_key).toBe(second[0].idempotency_key)
  })

  it('still creates review-only Java Vuser scripts directly', async () => {
    // The backend gate is narrower than the protocol: a Java draft with no
    // executable runner actions saves through POST /scripts as before.
    apiMock.createScript.mockResolvedValueOnce({
      id: 'java-review-only-1',
      name: 'Java Review Only',
      protocol: 'java_vuser',
      version: '1',
      created_at: '2026-05-17T00:00:01Z',
      script: { ...initialScript, protocol: 'java_vuser', name: 'Java Review Only' },
    })

    const javaScript: ScriptDefinition = {
      ...initialScript,
      protocol: 'java_vuser',
      name: 'Java Review Only',
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={javaScript} initiallyDirty projectId="project-1" />)
    })

    const saveButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Save script"]')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.createAuthoringSession).not.toHaveBeenCalled()
    expect(apiMock.createScript).toHaveBeenCalled()
    expect(container.textContent).toContain('Script saved successfully!')
  })

  it('guards dirty editor close and clears the guard after a successful save', async () => {
    const onClose = vi.fn()
    apiMock.createScript.mockResolvedValueOnce({
      id: 'script-saved-guard',
      name: 'Guarded Checkout Flow',
      protocol: initialScript.protocol,
      version: initialScript.version,
      created_at: '2026-05-17T00:00:00Z',
      script: { ...initialScript, name: 'Guarded Checkout Flow' },
    })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <ScriptEditor initialScript={initialScript} onClose={onClose} />
        </NavigationGuardProvider>,
      )
    })

    const nameInput = container.querySelector<HTMLInputElement>('.side-panel-script-name input[placeholder="My_Test_Script"]')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      changeInputValue(nameInput, 'Guarded Checkout Flow')
      await Promise.resolve()
    })

    const closeButton = container.querySelector<HTMLButtonElement>('.actions-sidebar button[aria-label="Close script editor"]')
    expect(closeButton).toBeDefined()
    if (!closeButton) return
    expect(closeButton.textContent?.trim()).toBe('')
    expect(closeButton.querySelector('svg')).not.toBeNull()

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(onClose).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Create Script has unsaved changes')

    const saveChangesButton = findButtonByText(container, 'Save changes')
    expect(saveChangesButton).toBeDefined()
    if (!saveChangesButton) return

    await act(async () => {
      saveChangesButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Guarded Checkout Flow',
    }), undefined, undefined)
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(onClose).toHaveBeenCalledTimes(2)
    expect(container.textContent).not.toContain('Unsaved Changes')
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

    async function clickRuntimeCategory(label: string) {
      const button = Array.from(container.querySelectorAll('.runtime-settings-tab')).find(candidate =>
        candidate.textContent?.includes(label),
      ) as HTMLButtonElement | undefined
      expect(button).toBeDefined()
      if (!button) return
      await act(async () => {
        button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }

    const activeRuntimeBodyText = () =>
      container.querySelector('.runtime-settings-detail .settings-section.open .section-body')?.textContent || ''

    await clickRuntimeCategory('Run Logic')

    expect(activeRuntimeBodyText()).not.toContain('Block Scheduling')
    expect(activeRuntimeBodyText()).toContain('Imported Source Runtime Settings')
    expect(activeRuntimeBodyText()).toContain('Global Iterations')
    expect(activeRuntimeBodyText()).toContain('Max Transactions')
    expect(activeRuntimeBodyText()).toContain('Workload Model')
    expect(activeRuntimeBodyText()).toContain('Open arrival-rate')

    await clickRuntimeCategory('Logging')

    expect(activeRuntimeBodyText()).toContain('Parameter substitution')
    expect(activeRuntimeBodyText()).not.toContain('Block Scheduling')
    expect(container.querySelector('.logging-settings-layout')).toBeTruthy()
    expect(container.querySelector('.logging-checkbox-group.runtime-checkbox-list')).toBeTruthy()
    // on_error_only + run-evidence redaction + the three extended_options toggles.
    expect(activeRuntimeBodyText()).toContain('Redact credentials in run evidence')
    expect(container.querySelectorAll('.logging-checkbox-group .checkbox-label')).toHaveLength(5)

    await clickRuntimeCategory('Web HTTP/HTML Compatibility')

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
    expect(container.textContent).toContain('Jitter (s)')
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

    const httpTransportButton = Array.from(container.querySelectorAll('.runtime-settings-tab')).find(button =>
      button.textContent?.includes('HTTP Transport'),
    ) as HTMLButtonElement | undefined
    expect(httpTransportButton).toBeDefined()
    if (!httpTransportButton) {
      return
    }

    await act(async () => {
      httpTransportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const browserEmulationTab = Array.from(container.querySelectorAll('.http-transport-tabs [role="tab"]')).find(tab =>
      tab.textContent?.includes('Browser Emulation'),
    ) as HTMLButtonElement | undefined
    expect(browserEmulationTab).toBeDefined()
    if (!browserEmulationTab) {
      return
    }

    await act(async () => {
      browserEmulationTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Browser Engine')
    expect(container.textContent).toContain('Device Profile')
    expect(container.textContent).toContain('Force touch mode')
    expect(container.textContent).toContain('Capture client metrics')
    expect(container.textContent).toContain('Viewport Width')
    expect(container.textContent).toContain('Viewport Height')

    const browserToggleList = container.querySelector('.browser-emulation-checkbox-list')
    expect(browserToggleList).not.toBeNull()
    const browserToggleRows = Array.from(browserToggleList?.querySelectorAll('label.checkbox-label') || [])
    expect(browserToggleRows.map(row => row.textContent)).toEqual([
      'Run headless',
      'Force touch mode',
      'Capture client metrics',
      'Allow reviewed JavaScript',
      'Allow review-required actions',
      'Simulate browser cache',
      'Cache URLs requiring content',
      'Check for newer cached pages',
      'Simulate a new user each iteration',
    ])
    expect(browserToggleRows.every(row => row.querySelector('input[type="checkbox"]'))).toBe(true)

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

    const scriptJsonTab = findCenterTab(container, 'Source')
    expect(scriptJsonTab).toBeDefined()
    await act(async () => {
      scriptJsonTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

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
    expect(container.querySelector('.script-browser-evidence')).not.toBeNull()
    expect(container.querySelector('.selected-evidence-header')?.textContent).toContain('Search Results')

    const firstTimelineRow = Array.from(container.querySelectorAll('.debugger-action-row-main'))
      .find(row => row.textContent?.includes('Open Home')) as HTMLButtonElement | undefined
    expect(firstTimelineRow).toBeDefined()
    if (!firstTimelineRow) {
      return
    }

    await act(async () => {
      firstTimelineRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.querySelector('.debugger-action-row.selected')?.textContent).toContain('Open Home')
    expect(container.querySelector('.selected-evidence-header')?.textContent).toContain('Home')
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
    const scriptJsonTab = findCenterTab(container, 'Source')
    expect(scriptJsonTab).toBeDefined()
    if (!scriptJsonTab) {
      return
    }

    await act(async () => {
      scriptJsonTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const fullScriptJsonButton = findButtonByText(container, 'Full Script JSON')
    expect(fullScriptJsonButton).toBeDefined()
    await act(async () => {
      fullScriptJsonButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
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

    const undoButton = findButtonByTitle(container, 'Undo document change')
    expect(undoButton).toBeDefined()
    if (!undoButton) {
      return
    }
    expect(undoButton.textContent?.trim()).toBe('')
    expect(undoButton.querySelector('svg')).not.toBeNull()

    await act(async () => {
      undoButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    await act(async () => { findCenterTab(container, 'Source')?.click() })
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
    replaySessionIdMock.mockReturnValueOnce('browser-replay-1')
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'browser-replay-1',
      warnings: ['WebKit controller host is missing the Playwright browser cache warmup.'],
      session: {
          ...unavailableRuntimeMetadata('browser-replay-1', 'running', '1'),
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

    expect(container.querySelector('.editor-toast.warning')).not.toBeNull()
    expect(container.textContent).toContain('WebKit controller host is missing the Playwright browser cache warmup.')
  })

  it('renders replay preflight, pinned failure diagnosis, and variable timeline', async () => {
    replaySessionIdMock.mockReturnValueOnce('diagnostic-replay-1')
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
          ...unavailableRuntimeMetadata('diagnostic-replay-1', 'failed', '3'),
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

    const configurationTab = findCenterTab(container, 'Runtime Settings')
    expect(configurationTab).toBeDefined()
    await act(async () => {
      configurationTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const readinessCard = Array.from(container.querySelectorAll('.utility-card'))
      .find(card => card.textContent?.includes('Ready for debug?'))
    expect(readinessCard?.textContent).toContain('Preflight')
    expect(readinessCard?.textContent).toContain('Missing variable: account_id')

    const debugTab = findOpenDebuggerButton(container)
    expect(debugTab).toBeDefined()
    if (!debugTab) {
      return
    }
    await act(async () => {
      debugTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

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

    expect(container.querySelector('.replay-failure-pin')).toBeNull() // selected failure already shows the diagnosis
    expect(container.textContent).toContain('Auth Session')
    expect(container.textContent).toContain('Refresh session setup')

    // The variable timeline lives in the merged detail panel's Variables tab.
    const variablesTab = Array.from(container.querySelectorAll('.debugger-center-tablist button')).find(button => button.textContent?.trim() === 'Variables') as HTMLButtonElement | undefined
    expect(variablesTab).toBeDefined()
    await act(async () => {
      variablesTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Variable Timeline')
    expect(container.textContent).toContain('csrf')
  })

  it('exports the current browser replay session as JSON', async () => {
    replaySessionIdMock.mockReturnValueOnce('browser-replay-1')
    apiMock.startDraftScriptDebugSession.mockResolvedValueOnce({
      replay_session_id: 'browser-replay-1',
      session: {
          ...unavailableRuntimeMetadata('browser-replay-1', 'running', '1'),
        id: 'browser-replay-1',
        group: 'Checkout Flow-browser-replay',
        protocol: 'browser',
        status: 'running',
        started_at: '2026-05-14T20:00:00Z',
        events: [],
      },
    })
    apiMock.getScriptDebugSession.mockResolvedValueOnce({
      session: {
          ...unavailableRuntimeMetadata('browser-replay-1', 'running', '1'),
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
    await flushReact()

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
      seed_variable_names: [],
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

    downloadTextFileMock.mockResolvedValueOnce('cancelled')
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()
    expect(container.textContent).toContain('Export cancelled.')
    expect(container.textContent).not.toContain('Exported debugger session JSON.')

    downloadTextFileMock.mockRejectedValueOnce(new Error('disk is full'))
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()
    expect(container.textContent).toContain('disk is full')
    expect(container.textContent).not.toContain('Exported debugger session JSON.')

    downloadTextFileMock.mockRejectedValueOnce(new Error('This generated file is too large for the desktop save bridge (maximum 16 MiB).'))
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushReact()
    expect(container.textContent).toContain('too large for the desktop save bridge')
    expect(container.textContent).not.toContain('Exported debugger session JSON.')
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

    const nameInput = container.querySelector<HTMLInputElement>('.side-panel-script-name input[placeholder="My_Test_Script"]')
    expect(nameInput).toBeDefined()
    if (!nameInput) {
      return
    }

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      valueSetter?.call(dfeArea, 'gzip mode=decode\nhtml_unescape')
      dfeArea.dispatchEvent(new Event('input', { bubbles: true }))
      dfeArea.dispatchEvent(new Event('change', { bubbles: true }))
      changeInputValue(nameInput, 'DFE Round Trip')
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

  it('renders the action workspace with insights in the contextual right sidebar', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const contentGrid = container.querySelector('.actions-content-grid')
    const sidebar = container.querySelector('.actions-sidebar')
    const utilitySidebar = container.querySelector('.editor-utility-sidebar')

    expect(contentGrid).not.toBeNull()
    expect(sidebar?.parentElement).toBe(contentGrid)
    expect(utilitySidebar?.parentElement).toBe(contentGrid)
    expect(container.querySelector('.action-detail')).toBeNull()
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Insights')

    const firstAction = container.querySelector('.action-item')
    await act(async () => {
      firstAction?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.action-detail')).toBeNull()
    expect(container.querySelector('.editor-utility-sidebar')?.classList.contains('context-inspector')).toBe(true)
    expect(container.querySelector('.editor-utility-sidebar .side-pane-header')?.textContent).toContain('Action context')
    expect(container.querySelector('.author-main-action .action-editor-panel')).not.toBeNull()
    expect(container.querySelector('.editor-utility-sidebar .action-editor-panel')).toBeNull()
    expect(container.querySelector('.script-code-inline-inspector .action-editor-panel')).toBeNull()

    expect(Array.from(container.querySelectorAll('.script-json-sidebar-tab')).map(button => button.textContent?.trim())).toEqual([])
    expect(container.querySelector('.json-source-map')).toBeNull()
  })

  it('collapses and restores the outline side panel from the author workspace', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const contentGrid = container.querySelector('.actions-content-grid')
    const sidebar = container.querySelector('.actions-sidebar')
    expect(contentGrid).not.toBeNull()
    expect(sidebar).not.toBeNull()
    expect(sidebar?.classList.contains('collapsed')).toBe(false)
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).not.toContain('Outline')

    const collapseOutlineSidebar = container.querySelector('.actions-sidebar button[aria-label="Collapse outline side panel"]') as HTMLButtonElement | null
    expect(collapseOutlineSidebar).not.toBeNull()
    await act(async () => {
      collapseOutlineSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const collapsedSidebar = container.querySelector('.actions-sidebar')
    expect(contentGrid?.classList.contains('author-outline-collapsed')).toBe(true)
    expect(collapsedSidebar?.classList.contains('collapsed')).toBe(true)
    expect(collapsedSidebar?.querySelector('.editor-outline-nav')).toBeNull()

    const expandOutlineSidebar = container.querySelector('.actions-sidebar button[aria-label="Expand outline side panel"]') as HTMLButtonElement | null
    expect(expandOutlineSidebar).not.toBeNull()
    expect(expandOutlineSidebar?.textContent).toContain('Outline')
    await act(async () => {
      expandOutlineSidebar?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(contentGrid?.classList.contains('author-outline-collapsed')).toBe(false)
    expect(container.querySelector('.actions-sidebar')?.classList.contains('collapsed')).toBe(false)
    expect(container.querySelector('.actions-sidebar .editor-outline-nav')?.textContent).not.toContain('Outline')
    expect(container.querySelector('.actions-sidebar .script-action-navigator')).not.toBeNull()
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

    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit request body in center')?.click() })
    const textarea = container.querySelector('.author-body-document textarea[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
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

    await act(async () => {
      findCenterTab(container, 'Source')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const updatedTextarea = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(updatedTextarea?.value).toContain('{{param.demo}}')
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

    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit request body in center')?.click() })
    const textarea = container.querySelector('.author-body-document textarea[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
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
      button.textContent?.includes('Create new parameter'),
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

    // The new parameter is created and focused in Parameter Data.
    expect(container.textContent).toContain('login')

    // Switching back to the Script JSON tab confirms the value became a template reference.
    const jsonTab = findCenterTab(container, 'Source')
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
      button.textContent?.includes('Create new parameter'),
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
    const jsonTab = findCenterTab(container, 'Source')
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

  it('uses an existing parameter from the Script JSON context menu without creating a duplicate', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    expect(editorWrapper).not.toBeNull()
    expect(textarea).not.toBeNull()
    if (!editorWrapper || !textarea) return

    const start = textarea.value.indexOf('/login')
    const end = start + '/login'.length
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      editorWrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 60 }))
    })

    const useExistingButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Use existing parameter'),
    )
    expect(useExistingButton).toBeDefined()
    await act(async () => {
      useExistingButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const existingParameterButton = Array.from(container.querySelectorAll('.parameter-existing-dialog button')).find(button =>
      button.textContent?.includes('session_id'),
    )
    expect(existingParameterButton).toBeDefined()
    await act(async () => {
      existingParameterButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const jsonTab = findCenterTab(container, 'Source')
    await act(async () => {
      jsonTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const refreshedTextarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    const updatedJson = refreshedTextarea?.value || ''
    expect(updatedJson).toContain('"target": "{{param.session_id}}"')
    expect(updatedJson.match(/"name": "session_id"/g)).toHaveLength(1)
  })

  it('reviews JSON string occurrences and replaces only checked matches', async () => {
    const repeatedScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          target: '/login',
          headers: {
            'X-Return': '/login',
            'X-Skip': '/login',
          },
          body: '',
        },
      ],
      parameters: [
        {
          name: 'endpoint_path',
          type: 'list',
          update: 'each_iteration',
          values: ['/login'],
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={repeatedScript} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    if (!editorWrapper || !textarea) {
      expect(editorWrapper).not.toBeNull()
      return
    }

    const start = textarea.value.indexOf('/login')
    const end = start + '/login'.length
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      editorWrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 60 }))
    })

    const reviewButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Review matching occurrences'),
    )
    expect(reviewButton).toBeDefined()
    await act(async () => {
      reviewButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const occurrenceCheckboxes = Array.from(container.querySelectorAll('.parameter-occurrence-item input')) as HTMLInputElement[]
    expect(occurrenceCheckboxes).toHaveLength(2)
    await act(async () => {
      occurrenceCheckboxes[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const useExistingButton = Array.from(container.querySelectorAll('.parameter-occurrence-dialog button')).find(button =>
      button.textContent?.includes('Use existing parameter'),
    )
    expect(useExistingButton).toBeDefined()
    await act(async () => {
      useExistingButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const jsonTab = findCenterTab(container, 'Source')
    await act(async () => {
      jsonTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const refreshedTextarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    const updatedJson = refreshedTextarea?.value || ''
    expect(updatedJson).toContain('"target": "{{param.endpoint_path}}"')
    expect(updatedJson).toContain('"X-Return": "/login"')
    expect(updatedJson).toContain('"X-Skip": "{{param.endpoint_path}}"')
  })

  it('rejects stale reviewed JSON occurrences when the source changes before apply', async () => {
    const repeatedScript: ScriptDefinition = {
      ...initialScript,
      actions: [
        {
          ...initialScript.actions[0],
          target: '/login',
          headers: { 'X-Return': '/login' },
        },
      ],
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={repeatedScript} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    if (!editorWrapper || !textarea) {
      expect(editorWrapper).not.toBeNull()
      return
    }

    const start = textarea.value.indexOf('/login')
    const end = start + '/login'.length
    textarea.focus()
    textarea.setSelectionRange(start, end)

    await act(async () => {
      editorWrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 60 }))
    })

    const reviewButton = Array.from(container.querySelectorAll('.action-parameter-menu button')).find(button =>
      button.textContent?.includes('Review matching occurrences'),
    )
    await act(async () => {
      reviewButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      changeTextareaValue(textarea, textarea.value.replace('/login', '/changed'))
    })

    const useExistingButton = Array.from(container.querySelectorAll('.parameter-occurrence-dialog button')).find(button =>
      button.textContent?.includes('Use existing parameter'),
    )
    await act(async () => {
      useExistingButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('The script JSON changed since this selection was made')
  })

  it('creates a date/time built-in parameter with a shared format preset from Script JSON', async () => {
    await act(async () => {
      root.render(<ScriptEditor initialScript={initialScript} />)
    })

    const editorWrapper = container.querySelector('.script-code-editor') as HTMLElement | null
    const textarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    if (!editorWrapper || !textarea) {
      expect(editorWrapper).not.toBeNull()
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
      button.textContent?.includes('Create new parameter'),
    )
    await act(async () => {
      parameterizeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const builtinChoice = Array.from(container.querySelectorAll('.parameterize-dialog .parameter-choice-option')).find(button =>
      button.textContent?.includes('Built-in Function'),
    )
    await act(async () => {
      builtinChoice?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const dateTimeOption = Array.from(container.querySelectorAll('.parameterize-type-grid .parameter-choice-option')).find(button =>
      button.textContent?.includes('Date/Time'),
    )
    expect(dateTimeOption).toBeDefined()
    await act(async () => {
      dateTimeOption?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const formatSelect = container.querySelector('.parameter-format-control select') as HTMLSelectElement | null
    expect(formatSelect).not.toBeNull()
    await act(async () => {
      if (!formatSelect) return
      formatSelect.value = '2006-01-02T15:04:05Z'
      formatSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const createButton = Array.from(container.querySelectorAll('.parameterize-dialog button')).find(button =>
      button.textContent?.includes('Create Parameter'),
    )
    await act(async () => {
      createButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const jsonTab = findCenterTab(container, 'Source')
    await act(async () => {
      jsonTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const refreshedTextarea = container.querySelector('[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
    expect(refreshedTextarea?.value).toContain('"type": "date_time"')
    expect(refreshedTextarea?.value).toContain('"format": "2006-01-02T15:04:05Z"')
    expect(refreshedTextarea?.value).toContain('"timezone": "UTC"')
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

    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit request body in center')?.click() })
    const textarea = container.querySelector('.author-body-document textarea[data-testid="codemirror-mock"]') as HTMLTextAreaElement | null
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
    await act(async () => {
      findCenterTab(container, 'Source')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const updatedTextarea = container.querySelector('.script-code-pane textarea') as HTMLTextAreaElement | null
    expect(updatedTextarea?.value).toContain('{{param.seed}}')
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
      button.textContent?.includes('Create new parameter'),
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
    expect(container.textContent).toContain('SOAPAction')
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
    const soapFaultCheckboxList = findRuntimeCheckboxList(container, 'Fail on SOAP Fault')
    expect(soapFaultCheckboxList).toBeTruthy()
    expect(soapFaultCheckboxList?.querySelectorAll('label.checkbox-label')).toHaveLength(1)

    const wsSecurityCheckboxList = findRuntimeCheckboxList(container, 'Include nonce')
    expect(wsSecurityCheckboxList).toBeTruthy()
    expect(wsSecurityCheckboxList?.textContent).toContain('Include created timestamp')
    expect(wsSecurityCheckboxList?.querySelectorAll('label.checkbox-label')).toHaveLength(2)
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

    expect(container.textContent).toContain('Query Timeout (s)')
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
    expect(container.textContent).toContain('Extractions')
    expect(container.textContent).toContain('Request')
    expect(container.textContent).toContain('Response')
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
          source: 'enterprise_script_archive',
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

    const configurationTab = findCenterTab(container, 'Runtime Settings')
    expect(configurationTab).toBeDefined()
    await act(async () => {
      configurationTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.actions-sidebar .editor-outline-nav')).not.toBeNull()
    expect(container.textContent).toContain('Parameter File Mappings')
    expect(container.textContent).toContain('Open Mapping List')
    expect(container.querySelector('.actions-sidebar')?.textContent).not.toContain('Starter Blocks')
    const openMappingsButton = findButtonByText(container, 'Open Mapping List')
    expect(openMappingsButton).toBeDefined()
    await act(async () => {
      openMappingsButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('Users.prm: LoginUser')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('column username')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('table users.csv')
    expect(container.textContent).toContain('Ready for debug?')
    expect(container.textContent).not.toContain('Guided History')
    expect(container.textContent).not.toContain('Flow Assembly')
    expect(container.textContent).toContain('Repair Actions')
    expect(container.textContent).toContain('Migration Assistant')
    expect(container.textContent).toContain('Draft Diff Review')
    expect(container.textContent).toContain('Correlation Inspector')
    expect(container.textContent).toContain('Parameter Source Inspector')
    expect(container.textContent).toContain('0 errors • 2 warnings')

    const openMappingListButton = Array.from(container.querySelectorAll('button')).find(button =>
      button.textContent?.trim() === 'Open Mapping List',
    )
    expect(openMappingListButton).toBeDefined()
    if (!openMappingListButton) {
      return
    }

    await act(async () => {
      openMappingListButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('Users.prm: LoginUser')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('column username')
    expect(container.querySelector('.imported-parameter-mappings-dialog')?.textContent).toContain('table users.csv')
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
          source: 'enterprise_script_archive',
          source_script_name: 'checkout.usr',
          preserved: {},
        },
      },
    }

    await act(async () => {
      root.render(<ScriptEditor initialScript={importedAuthoringScript} />)
    })

    const configurationTab = findCenterTab(container, 'Runtime Settings')
    expect(configurationTab).toBeDefined()
    await act(async () => {
      configurationTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
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

    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === '+ Add Action')?.click() })
    const starterButton = Array.from(document.querySelectorAll('button')).find(button =>
      button.textContent?.includes('POST JSON'),
    )
    expect(starterButton).toBeDefined()
    if (!starterButton) {
      return
    }

    await act(async () => {
      starterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Inserted')
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

    const configurationTab = findCenterTab(container, 'Runtime Settings')
    expect(configurationTab).toBeDefined()
    await act(async () => {
      configurationTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === '+ Add Action')?.click() })
    expect(document.body.textContent).toContain('Core API Request Group')

    const insertButton = Array.from(document.querySelectorAll('.reusable-blocks-panel .starter-block-card')).find(button =>
      button.textContent?.includes('Core API Request Group'),
    ) as HTMLButtonElement | undefined
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

    const undoButton = findButtonByTitle(container, 'Undo document change')
    expect(undoButton).toBeDefined()
    if (!undoButton) {
      return
    }
    expect(undoButton.textContent?.trim()).toBe('')
    expect(undoButton.querySelector('svg')).not.toBeNull()

    await act(async () => {
      undoButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Undid the last document change.')
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
