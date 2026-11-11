// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugSession } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'
import { buildOutlineModel } from './scriptOutlineModel'
import { buildScriptPhaseActionRows } from './scriptEditorWorkbench'
import { useScriptReplayDebuggerController } from './useScriptReplayDebuggerController'

const { inspectorLoadState } = vi.hoisted(() => ({
  inspectorLoadState: { failNext: false, importCount: 0 },
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

import { ScriptReplayDebuggerView } from './ScriptReplayDebuggerView'

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
}: {
  detailHost: HTMLDivElement
  report: BrowserReplayReport | null
  session: ScriptDebugSession | null
  initialExpandedEvent?: number | null
  canEditAction?: (event: BrowserReplayEvent) => boolean
  onEditAction?: (event: BrowserReplayEvent) => void
  canRevealInScript?: (event: BrowserReplayEvent) => boolean
  onRevealInScript?: (event: BrowserReplayEvent) => void
}) {
  const [expandedEvent, setExpandedEvent] = useState<number | null>(initialExpandedEvent)
  const [replayOptions, setReplayOptions] = useState(DEFAULT_REPLAY_OPTIONS)
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
      script={SAMPLE_SCRIPT}
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
})
