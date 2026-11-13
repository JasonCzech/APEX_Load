// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugSession } from '../types/replay'
import type { ScriptConfig, ScriptDefinition } from '../types/scripts'
import { DEFAULT_REPLAY_OPTIONS, type ReplayOptionsState } from './scriptEditorState'
import { buildOutlineModel } from './scriptOutlineModel'
import { buildScriptPhaseActionRows } from './scriptEditorWorkbench'
import { useScriptReplayDebuggerController } from './useScriptReplayDebuggerController'

const { inspectorLoadState, engineDefaultsState } = vi.hoisted(() => ({
  inspectorLoadState: { failNext: false, importCount: 0 },
  // What GET /api/v1/dsl/config-defaults answers (dsl.DefaultScriptConfig).
  engineDefaultsState: { config: { base_url: '', http: { request_timeout_ms: 60000, connect_timeout_ms: 30000, tls_timeout_ms: 10000, max_conns_per_host: 100, max_redirects: 10, keep_alive: true, cookies_enabled: true, tls: { min_version: '1.2' } } } },
}))

vi.mock('./debugEngineDefaults', async importOriginal => ({
  ...(await importOriginal<typeof import('./debugEngineDefaults')>()),
  useEngineConfigDefaults: () => engineDefaultsState.config,
}))

vi.mock('./RequestResponseInspector', () => {
  inspectorLoadState.importCount += 1
  return {
    default: ({ event }: { event: BrowserReplayEvent }) => {
      if (inspectorLoadState.failNext) {
        throw new Error('Failed to fetch dynamically imported module: RequestResponseInspector.js')
      }
      return <div data-testid="mock-request-response-inspector">Loaded inspector for {event.action_name}</div>
    },
  }
})

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

import { DebugNextRequest, ScriptReplayDebuggerView } from './ScriptReplayDebuggerView'

const SAMPLE_SCRIPT: ScriptDefinition = {
  version: '1.0',
  name: 'Checkout_Debug_Test',
  protocol: 'http',
  config: { base_url: 'https://api.example.test' },
  actions: [
    { name: 'Load catalog', type: 'request', method: 'GET', target: '/catalog' },
  ],
}

function Harness({
  detailHost,
  report,
  session,
  initialExpandedEvent = 0,
  canEditAction = () => true,
  onEditAction = () => {},
  canRevealInScript,
  onRevealInScript,
  activeWorkspace,
  onOpenRequestResponse,
  script = SAMPLE_SCRIPT,
  onReplayOptions,
  onPromoteSessionOverrides,
}: {
  script?: ScriptDefinition
  onReplayOptions?: (options: ReplayOptionsState) => void
  onPromoteSessionOverrides?: (update: (config: ScriptConfig) => ScriptConfig) => void
  detailHost: HTMLDivElement
  report: BrowserReplayReport | null
  session: ScriptDebugSession | null
  initialExpandedEvent?: number | null
  canEditAction?: (event: BrowserReplayEvent) => boolean
  onEditAction?: (event: BrowserReplayEvent) => void
  canRevealInScript?: (event: BrowserReplayEvent) => boolean
  onRevealInScript?: (event: BrowserReplayEvent) => void
  activeWorkspace?: string
  onOpenRequestResponse?: () => void
}) {
  const [expandedEvent, setExpandedEvent] = useState<number | null>(initialExpandedEvent)
  const [replayOptions, setReplayOptions] = useState<ReplayOptionsState>(DEFAULT_REPLAY_OPTIONS)
  onReplayOptions?.(replayOptions)
  const [followExecution, setFollowExecution] = useState(true)
  const rows = buildScriptPhaseActionRows(SAMPLE_SCRIPT)
  const outline = buildOutlineModel(rows)
  const controller = useScriptReplayDebuggerController({
    browserReplaySession: session,
    debugActionRows: rows,
    expandedReplayEvent: expandedEvent,
    replayReport: report,
    replaying: false,
    scriptId: 'script-1',
  })

  return (
    <ScriptReplayDebuggerView
      sessionControls={<div data-testid="session-controls">Session controls intact</div>}
      readinessPopover={null}
      runHistory={[]}
      runRecord={null}
      seedVariables={{}}
      followExecution={followExecution}
      onFollowExecution={setFollowExecution}
      onUpdateBreakpoints={async () => {}}
      actionProblemRows={[]}
      addExtractorDefinition={() => ({ status: 'applied', actionName: 'Load catalog', extractorAdded: true, extractorAvailable: true, requestRepaired: false })}
      activeWorkspace={activeWorkspace}
      onOpenRequestResponse={onOpenRequestResponse}
      replaceExtractorDefinition={() => true}
      applyReplaySuggestedLocator={() => {}}
      breakpointSummary="0 breakpoints"
      browserReplaySession={session}
      clearDebugBreakpoints={async () => {}}
      controller={controller}
      copiedReplaySnapshotSection={null}
      debugActionRows={rows}
      debugOutline={outline}
      outlineSelection={null}
      outlineResetKey="script-1"
      outlineHasIssue={() => false}
      outlineShowMarkers={false}
      onOutlineShowMarkersChange={() => {}}
      debugBreakpointKeys={new Set()}
      outlineBreakpointKeys={new Set()}
      debugDetailHost={detailHost}
      debuggerWorkbench={<div data-testid="center-workbench" />}
      error=""
      expandedReplayBrowserArtifacts={{}}
      expandedReplayEvent={expandedEvent}
      exportGeneratedTextFile={async () => {}}
      handleCopyReplaySnapshotSection={async () => {}}
      handleValidateScript={async () => {}}
      isBrowserScript={false}
      jsonParseMessage=""
      canEditAction={canEditAction}
      onEditAction={onEditAction}
      canRevealInScript={canRevealInScript}
      onRevealInScript={onRevealInScript}
      onEditReplayAction={() => {}}
      onEditParameterDefinition={() => {}}
      onActivateDebug={() => {}}
      onSelectProblemAction={() => {}}
      problemCount={0}
      projectId="project-1"
      replayActivity={[]}
      replayFailingEvent={null}
      replayFailingEventIndex={-1}
      replaying={false}
      replayOptions={replayOptions}
      replayReport={report}
      replayEditableActionsKey="key-1"
      currentEditableActionsKey="key-1"
      replayVariableDiffs={[{}]}
      replayVariableInspector={{}}
      currentRuntimeSnapshot={null}
      currentRuntimeReason=""
      canInspectRuntimeFrame={() => true}
      onInspectRuntimeFrame={() => {}}
      runtimeInspectionMode="retained"
      onRuntimeInspectionMode={() => {}}
      replayVariableTimeline={[]}
      saving={false}
      script={script}
      scriptId="script-1"
      scriptLevelIssues={[]}
      selectedOutlineParameterName={null}
      selectOutlineParameter={() => {}}
      parameterReferenceCounts={new Map()}
      onSelectDebugItem={() => {}}
      setBreakOnFailure={async () => {}}
      setExpandedReplayEvent={setExpandedEvent}
      setReplayOptions={setReplayOptions}
      setReplayScreenshotSelection={() => {}}
      setSequenceChildBreakpoints={async () => {}}
      toggleDebugBreakpoint={async () => {}}
      toggleReplayBrowserArtifact={() => {}}
      toggleStepPathBreakpoint={async () => {}}
      validating={false}
      warnings={[]}
      scriptHasUnsavedEdits={false}
      onPromoteSessionOverrides={onPromoteSessionOverrides}
    />
  )
}

describe('ScriptReplayDebuggerView', () => {
  let host: HTMLDivElement
  let portalHost: HTMLDivElement
  let root: Root
  const suppressChunkError = (event: ErrorEvent) => {
    if (event.error instanceof Error && event.error.message.includes('RequestResponseInspector.js')) {
      event.preventDefault()
    }
  }

  beforeEach(() => {
    inspectorLoadState.failNext = false
    host = document.createElement('div')
    portalHost = document.createElement('div')
    portalHost.setAttribute('data-testid', 'debug-detail-portal-host')
    document.body.append(host, portalHost)
    root = createRoot(host)
    window.addEventListener('error', suppressChunkError)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    window.removeEventListener('error', suppressChunkError)
    host.remove()
    portalHost.remove()
  })

  it('contains RequestResponseInspector chunk failures in LazyLoadBoundary inside createPortal and recovers on retry', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    inspectorLoadState.failNext = true

    const event: BrowserReplayEvent = {
      phase: 'action',
      action_index: 0,
      action_name: 'Load catalog',
      action_type: 'request',
      method: 'GET',
      target: 'https://api.example.test/catalog',
      status_code: 200,
      success: true,
      duration_ms: 18,
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
    }
    const report: BrowserReplayReport = {
      replay_session_id: 'debug-1',
      group: 'Checkout_Debug_Test-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: true,
      events: [event],
    }
    const session: ScriptDebugSession = {
      id: 'debug-1',
      group: report.group,
      protocol: 'http',
      status: 'completed',
      log_level: 'info',
      current_event_index: 1,
      started_at: report.started_at,
      completed_at: report.completed_at,
      events: [event],
      report,
    }

    try {
      await act(async () => {
        root.render(<Harness detailHost={portalHost} report={report} session={session} />)
      })
      for (let attempt = 0; attempt < 10 && portalHost.textContent?.includes('Loading request and response'); attempt += 1) {
        await act(async () => {
          await new Promise(resolve => setTimeout(resolve, 10))
        })
      }

      expect(inspectorLoadState.importCount).toBeGreaterThan(0)
      expect(portalHost.textContent).toContain('Request and response inspector could not be loaded.')
      expect(host.querySelector('[data-testid="session-controls"]')?.textContent).toBe('Session controls intact')
      expect(host.querySelector('.debug-session-row')).not.toBeNull()

      inspectorLoadState.failNext = false
      const retryButton = Array.from(portalHost.querySelectorAll<HTMLButtonElement>('button')).find(btn => btn.textContent === 'Reload and retry')!
      expect(retryButton).toBeDefined()
      await act(async () => {
        retryButton.click()
      })
      for (let attempt = 0; attempt < 5 && !portalHost.querySelector('[data-testid="mock-request-response-inspector"]'); attempt += 1) {
        await act(async () => {
          await new Promise(resolve => setTimeout(resolve, 10))
        })
      }
      expect(portalHost.querySelector('[data-testid="mock-request-response-inspector"]')?.textContent).toContain('Loaded inspector for Load catalog')
    } finally {
      consoleError.mockRestore()
    }
  })

  it('retains 248-char zero-space .usz identifiers in .debug-session-row DOM while CSS enforces single-line ellipsis', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const editorCss = readFileSync(`${cwd}/src/components/ScriptEditor.css`, 'utf8') as string

    expect(editorCss).toContain('.debug-session-row { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 16px; flex-shrink: 0; min-inline-size: 0; max-inline-size: 100%; overflow-x: hidden; }')
    expect(editorCss).toContain('.debug-session-row > .debug-selection-status { flex: 1 1 460px; flex-wrap: nowrap; min-inline-size: 0; max-inline-size: 100%; }')
    expect(editorCss).toContain('.debug-selection-status > span:first-child { flex: 0 1 auto; min-inline-size: 0; max-inline-size: 100%; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }')
    expect(editorCss).toContain('@container (max-width: 640px) { .debug-session-row > .debug-selection-status { flex-basis: 100%; flex-wrap: wrap; } .debug-selection-status > span:first-child { flex-basis: 100%; } }')

    const zeroSpaceUszName = `USZ_${'ZeroSpaceTransactionIdentifierSegment'.repeat(7)}`
    expect(zeroSpaceUszName.length).toBeGreaterThan(248)

    const event: BrowserReplayEvent = {
      phase: 'action',
      action_index: 0,
      action_name: zeroSpaceUszName,
      action_type: 'request',
      method: 'GET',
      target: 'https://api.example.test/catalog',
      status_code: 200,
      success: true,
      duration_ms: 12,
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
    }
    const report: BrowserReplayReport = {
      replay_session_id: 'debug-usz',
      group: 'Checkout_Debug_Test-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: true,
      events: [event],
    }

    await act(async () => {
      root.render(
        <Harness
          detailHost={portalHost}
          report={report}
          session={{
            id: 'debug-usz',
            group: report.group,
            protocol: 'http',
            status: 'completed',
            log_level: 'info',
            current_event_index: 1,
            started_at: report.started_at,
            completed_at: report.completed_at,
            events: [event],
            report,
          }}
        />,
      )
    })

    const statusSpan = host.querySelector<HTMLSpanElement>('.debug-session-row .debug-selection-status > span:first-child')!
    expect(statusSpan).not.toBeNull()
    expect(statusSpan.textContent).toContain(zeroSpaceUszName)
    expect(statusSpan.getAttribute('title')).toContain(zeroSpaceUszName)
  })

  it('forwards canEditAction and onEditAction into the dock Timeline waterfall drawer', async () => {
    const event: BrowserReplayEvent = {
      phase: 'action',
      action_index: 0,
      action_name: 'Load catalog',
      action_type: 'request',
      method: 'GET',
      target: 'https://api.example.test/catalog',
      status_code: 500,
      success: false,
      error_message: 'HTTP 500',
      duration_ms: 24,
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
    }
    const report: BrowserReplayReport = {
      replay_session_id: 'debug-dock-edit',
      group: 'Checkout_Debug_Test-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: false,
      events: [event],
    }
    const session: ScriptDebugSession = {
      id: 'debug-dock-edit',
      group: report.group,
      protocol: 'http',
      status: 'failed',
      log_level: 'info',
      current_event_index: 1,
      started_at: report.started_at,
      completed_at: report.completed_at,
      events: [event],
      report,
    }
    const onEditAction = vi.fn()

    await act(async () => {
      root.render(
        <Harness
          detailHost={portalHost}
          report={report}
          session={session}
          canEditAction={() => true}
          onEditAction={onEditAction}
        />,
      )
    })

    const timelineTab = Array.from(host.querySelectorAll<HTMLButtonElement>('.debugger-bottom-tabs [role="tab"]'))
      .find(btn => btn.textContent?.trim() === 'Timeline')!
    expect(timelineTab).toBeDefined()
    await act(async () => {
      timelineTab.click()
    })

    const editBtn = host.querySelector<HTMLButtonElement>('.debugger-timeline button[aria-label="Edit Load catalog"]')!
    expect(editBtn).not.toBeNull()
    expect(editBtn.textContent?.trim()).toBe('Edit')
    expect(editBtn.disabled).toBe(false)
    await act(async () => {
      editBtn.click()
    })
    expect(onEditAction).toHaveBeenCalledTimes(1)
    expect(onEditAction).toHaveBeenCalledWith(expect.objectContaining({ action_name: 'Load catalog', action_index: 0 }))

    // Re-render with canEditAction returning false (e.g. live replay or stale draft) and verify Edit disables cleanly.
    await act(async () => {
      root.render(
        <Harness
          detailHost={portalHost}
          report={report}
          session={session}
          canEditAction={() => false}
          onEditAction={onEditAction}
        />,
      )
    })
    const disabledEditBtn = host.querySelector<HTMLButtonElement>('.debugger-timeline button[aria-label="Edit Load catalog"]')!
    expect(disabledEditBtn.disabled).toBe(true)
  })

  it('suppresses duplicate DebugWaterfallPanel in the bottom dock when activeWorkspace is debug-summary', async () => {
    const event: BrowserReplayEvent = {
      phase: 'action',
      action_index: 0,
      action_name: 'Load catalog',
      action_type: 'request',
      method: 'GET',
      target: 'https://api.example.test/catalog',
      status_code: 200,
      success: true,
      duration_ms: 19,
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
    }
    const report: BrowserReplayReport = {
      replay_session_id: 'debug-summary-dup',
      group: 'Checkout_Debug_Test-debug',
      protocol: 'http',
      started_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:00:01Z',
      success: true,
      events: [event],
    }
    const session: ScriptDebugSession = {
      id: 'debug-summary-dup',
      group: report.group,
      protocol: 'http',
      status: 'completed',
      log_level: 'info',
      current_event_index: 1,
      started_at: report.started_at,
      completed_at: report.completed_at,
      events: [event],
      report,
    }
    const onOpenRequestResponse = vi.fn()

    await act(async () => {
      root.render(
        <Harness
          detailHost={portalHost}
          report={report}
          session={session}
          activeWorkspace="action-list"
          onOpenRequestResponse={onOpenRequestResponse}
        />,
      )
    })

    const timelineTab = Array.from(host.querySelectorAll<HTMLButtonElement>('.debugger-bottom-tabs [role="tab"]'))
      .find(btn => btn.textContent?.trim() === 'Timeline')!
    await act(async () => {
      timelineTab.click()
    })
    expect(host.querySelector('.debugger-shell')?.classList.contains('debug-dock-collapsed')).toBe(false)
    expect(host.querySelector('.debugger-timeline .debug-execution-timeline')).not.toBeNull()

    // Switch activeWorkspace to 'debug-summary': dock auto-collapses and Timeline replaces the duplicate waterfall with a compact notice.
    await act(async () => {
      root.render(
        <Harness
          detailHost={portalHost}
          report={report}
          session={session}
          activeWorkspace="debug-summary"
          onOpenRequestResponse={onOpenRequestResponse}
        />,
      )
    })
    expect(host.querySelector('.debugger-shell')?.classList.contains('debug-dock-collapsed')).toBe(true)
    expect(host.querySelector('.debugger-timeline .debug-execution-timeline')).toBeNull()
    const summaryNotice = host.querySelector<HTMLElement>('[data-testid="debugger-timeline-summary-active"]')!
    expect(summaryNotice).not.toBeNull()
    expect(summaryNotice.textContent).toContain('Transaction waterfall is shown in the Summary workspace above.')
    const inspectBtn = summaryNotice.querySelector<HTMLButtonElement>('button')!
    expect(inspectBtn).not.toBeNull()
    await act(async () => {
      inspectBtn.click()
    })
    expect(onOpenRequestResponse).toHaveBeenCalledTimes(1)
  })

  it('masks sensitive headers, query params, and body fields in DebugNextRequest by default and reveals on toggle', async () => {
    const sensitiveScript: ScriptDefinition = {
      version: '1.0',
      name: 'Auth_Debug_Test',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [
        {
          name: 'Authenticate user',
          type: 'request',
          method: 'POST',
          target: 'https://api.example.test/login?api_key=PlainQueryKey99&session={{var.auth_token}}&mode=standard',
          headers: {
            Authorization: 'Bearer {{var.jwt_secret}}',
            Origin: 'https://shop.example.test',
          },
          body: '{"username":"alice","password":"SuperSecretPassword!","token":"{{var.auth_token}}"}',
        },
        {
          name: 'Refresh token',
          type: 'request',
          method: 'POST',
          target: 'https://api.example.test/refresh?api_key=SecondStepKey88&session={{var.auth_token}}',
          headers: {
            Authorization: 'Bearer {{var.jwt_secret}}',
          },
        },
      ],
    }

    await act(async () => {
      root.render(
        <DebugNextRequest
          script={sensitiveScript}
          action={{ event_index: 0, phase: 'action', action_index: 0, action_name: 'Authenticate user', action_type: 'request', method: 'POST' }}
          values={{ 'var.jwt_secret': 'eyJhbGciOiJIUzI1NiJ9.secret', 'var.auth_token': 'tok-live-777' }}
          runId="debug-run-42"
        />,
      )
    })

    const nextReq = host.querySelector<HTMLElement>('.debugger-next-request')!
    expect(nextReq).not.toBeNull()
    // Non-sensitive values are visible immediately.
    expect(nextReq.textContent).toContain('https://shop.example.test')
    expect(nextReq.textContent).toContain('mode=standard')
    expect(nextReq.textContent).toContain('alice')
    // Sensitive header, query params, and body fields are masked by default.
    expect(nextReq.textContent).not.toContain('eyJhbGciOiJIUzI1NiJ9.secret')
    expect(nextReq.textContent).not.toContain('PlainQueryKey99')
    expect(nextReq.textContent).not.toContain('SuperSecretPassword!')
    expect(nextReq.textContent).not.toContain('tok-live-777')
    expect(nextReq.textContent).toContain('••••••••')

    // Reveal header via InspectorHeaderTable's Show value button.
    const showAuthHeaderBtn = nextReq.querySelector<HTMLButtonElement>('button[aria-label="Show Authorization value"]')!
    expect(showAuthHeaderBtn).not.toBeNull()
    await act(async () => {
      showAuthHeaderBtn.click()
    })
    expect(nextReq.textContent).toContain('Bearer eyJhbGciOiJIUzI1NiJ9.secret')

    // Reveal URL/body sensitive values via DebugNextRequest's Show value button.
    const showReqValuesBtn = nextReq.querySelector<HTMLButtonElement>('button[aria-label="Show sensitive request values"]')!
    expect(showReqValuesBtn).not.toBeNull()
    await act(async () => {
      showReqValuesBtn.click()
    })
    expect(nextReq.textContent).toContain('PlainQueryKey99')
    expect(nextReq.textContent).toContain('SuperSecretPassword!')
    expect(nextReq.textContent).toContain('tok-live-777')

    // Per-header Hide button can still hide Authorization while top-level Show sensitive request values is active.
    const hideAuthHeaderBtn = nextReq.querySelector<HTMLButtonElement>('button[aria-label="Hide Authorization value"]')!
    expect(hideAuthHeaderBtn).not.toBeNull()
    await act(async () => {
      hideAuthHeaderBtn.click()
    })
    expect(nextReq.textContent).not.toContain('eyJhbGciOiJIUzI1NiJ9.secret')

    // Hide them again.
    const hideReqValuesBtn = nextReq.querySelector<HTMLButtonElement>('button[aria-label="Hide sensitive request values"]')!
    await act(async () => {
      hideReqValuesBtn.click()
    })
    expect(nextReq.textContent).not.toContain('SuperSecretPassword!')

    // Re-reveal and then step to action_index 1: revealed state resets automatically on step transition.
    await act(async () => {
      nextReq.querySelector<HTMLButtonElement>('button[aria-label="Show sensitive request values"]')!.click()
    })
    expect(nextReq.textContent).toContain('PlainQueryKey99')
    await act(async () => {
      root.render(
        <DebugNextRequest
          script={{
            ...sensitiveScript,
            actions: [
              sensitiveScript.actions[0],
              {
                ...sensitiveScript.actions[1],
                headers: {
                  Authorization: 'Bearer {{secret.API_KEY}}',
                },
              },
            ],
          }}
          action={{ event_index: 1, phase: 'action', action_index: 1, action_name: 'Refresh token', action_type: 'request', method: 'POST' }}
          values={null}
          runId="debug-run-42"
        />,
      )
    })
    expect(nextReq.textContent).not.toContain('SecondStepKey88')
    expect(nextReq.textContent).not.toContain('eyJhbGciOiJIUzI1NiJ9.secret')
    expect(nextReq.textContent).toContain('{{var.auth_token}}')
    // Secret-sourced header is masked by InspectorHeaderTable without rendering a no-op reveal toggle.
    expect(nextReq.querySelector('button[aria-label="Show Authorization value"]')).toBeNull()
  })
  describe('session runtime overrides and run configuration', () => {
    const buttonByText = (text: string | RegExp) => [...host.querySelectorAll('button')].find(button => typeof text === 'string' ? button.textContent === text : text.test(button.textContent || ''))
    const runConfigRow = (label: string) => [...host.querySelectorAll('table[aria-label="Effective run configuration"] tr')].find(row => row.querySelector('th')?.textContent === label)?.querySelector('td')?.textContent

    async function openRunConfiguration() {
      const tab = buttonByText('Run Configuration')
      expect(tab).toBeTruthy()
      await act(async () => { tab!.click() })
    }

    async function openSessionOverrides() {
      const options = buttonByText(/^Debug options/)
      expect(options).toBeTruthy()
      await act(async () => { options!.click() })
      await vi.waitFor(() => expect(host.querySelector('[data-testid="debug-session-runtime-overrides"]')).toBeTruthy())
    }

    async function typeBaseURL(value: string) {
      const input = host.querySelector<HTMLInputElement>('[data-testid="debug-session-runtime-overrides"] [data-override-path="base_url"] input')
      expect(input).toBeTruthy()
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
        input!.dispatchEvent(new Event('input', { bubbles: true }))
      })
    }

    it('shows the engine defaults for unset HTTP settings and marks pacing as skipped', async () => {
      await act(async () => { root.render(<Harness detailHost={portalHost} report={null} session={null} />) })
      await openRunConfiguration()
      expect(runConfigRow('Timeouts')).toBe('Request 60000 ms · Connect 30000 ms · TLS 10000 ms')
      expect(runConfigRow('Connections & redirects')).toBe('Keep-alive on · Max redirects 10 · Max conns/host 100')
      expect(runConfigRow('Pacing')).toBe('Skipped in single-VUser debug replay')
      expect(runConfigRow('Session runtime overrides')).toBe('None')
    })

    it('keeps session overrides out of the script and shows them in the run configuration', async () => {
      let options: ReplayOptionsState = DEFAULT_REPLAY_OPTIONS
      const script: ScriptDefinition = { ...SAMPLE_SCRIPT, config: { ...SAMPLE_SCRIPT.config } }
      const before = structuredClone(script)
      await act(async () => { root.render(<Harness detailHost={portalHost} report={null} session={null} script={script} onReplayOptions={next => { options = next }} />) })
      await openSessionOverrides()
      expect(host.querySelector('[data-testid="debug-session-overrides-source"]')?.textContent).toContain('The script itself is not sent')
      await typeBaseURL('https://staging.example.test')
      expect(options.runtimeOverrides?.override_fields).toEqual(['base_url'])
      expect(script).toEqual(before)
      expect(buttonByText(/^Debug options/)?.textContent).toBe('Debug options (1)')
      expect(host.querySelector('.debug-session-summary')?.textContent).toContain('1 session runtime override')
      await openRunConfiguration()
      expect(runConfigRow('Session runtime overrides')).toBe('1 setting: base_url')
      expect(runConfigRow('Base URL (relative targets)')).toContain('https://staging.example.test (session override)')
      const reset = buttonByText('Reset session options')
      expect(reset).toBeTruthy()
      await act(async () => { reset!.click() })
      expect(options.runtimeOverrides).toBeUndefined()
    })

    it('promotes session overrides through the Author edit path and clears them', async () => {
      let options: ReplayOptionsState = DEFAULT_REPLAY_OPTIONS
      const promote = vi.fn<(update: (config: ScriptConfig) => ScriptConfig) => void>()
      await act(async () => { root.render(<Harness detailHost={portalHost} report={null} session={null} onReplayOptions={next => { options = next }} onPromoteSessionOverrides={promote} />) })
      await openSessionOverrides()
      await typeBaseURL('https://staging.example.test')
      const button = buttonByText('Promote session overrides to script')
      expect(button).toBeTruthy()
      await act(async () => { button!.click() })
      expect(promote).toHaveBeenCalledTimes(1)
      const update = promote.mock.calls[0][0]
      expect(update(SAMPLE_SCRIPT.config)).toEqual({ base_url: 'https://staging.example.test' })
      expect(options.runtimeOverrides).toBeUndefined()
      expect(host.querySelector('[data-testid="debug-session-runtime-overrides"] [role="status"]')?.textContent).toContain('Promoted 1 setting')
    })
  })
})
