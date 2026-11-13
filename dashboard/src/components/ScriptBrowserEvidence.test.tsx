// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, it, expect, vi } from 'vitest'
import ScriptBrowserEvidence from './ScriptBrowserEvidence'
import type { BrowserReplayEvent, BrowserReplayReport } from '../types/replay'

const mockCompletedReport: BrowserReplayReport = {
  replay_session_id: 'replay-session-1',
  group: 'Checkout-browser-replay',
  protocol: 'browser',
  started_at: '2026-05-14T20:00:00Z',
  completed_at: '2026-05-14T20:00:02Z',
  success: true,
  events: [{
    phase: 'action',
    action_index: 0,
    action_name: 'Open home',
    action_type: 'browser_goto',
    target: 'https://shop.example.test',
    success: true,
    duration_ms: 45,
    started_at: '2026-05-14T20:00:00Z',
    completed_at: '2026-05-14T20:00:01Z',
  }],
}

vi.mock('./BrowserReplayProgress', () => ({
  default: ({ replaySessionId, onComplete, onDismiss }: { replaySessionId: string; onComplete?: (report: BrowserReplayReport | null, events: BrowserReplayEvent[]) => void; onDismiss?: () => void }) => (
    <div data-testid="replay-progress" data-session-id={replaySessionId}>
      <button type="button" onClick={() => onComplete?.(mockCompletedReport, mockCompletedReport.events)}>Complete replay</button>
      <button type="button" onClick={() => onDismiss?.()}>Dismiss replay</button>
    </div>
  ),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const event: BrowserReplayEvent = { phase: 'action', action_index: 0, action_name: 'Check greeting', action_type: 'browser_wait', target: '#result', success: false, duration_ms: 12, started_at: '', completed_at: '', protocol_evidence: { assertion_failure: { expected: 'Welcome', actual: 'Hello' } }, extractions: { greeting: 'Hello' } }
describe('selected browser evidence', () => {
  it('explains when a screenshot was omitted by the retention limit', async () => {
    const host = document.createElement('div'); const root = createRoot(host)
    try {
      await act(async () => root.render(<ScriptBrowserEvidence event={{ ...event, screenshot_omitted: true }} running={false} />))
      expect(host.textContent).toContain('A screenshot was omitted because it exceeded the retained evidence limit.')
      expect(host.querySelector('img')).toBeNull()
    } finally { await act(async () => root.unmount()) }
  })
  it('reports a failed automatic screenshot capture without suggesting capture was disabled', async () => {
    const host = document.createElement('div'); const root = createRoot(host)
    try {
      await act(async () => root.render(<ScriptBrowserEvidence event={{ ...event, protocol_evidence: { screenshot_capture_error: 'image exceeds acquisition limit' } }} running={false} />))
      expect(host.textContent).toContain('Screenshot capture failed: image exceeds acquisition limit')
      expect(host.textContent).not.toContain('Enable per-action capture')
    } finally { await act(async () => root.unmount()) }
  })
  it('reports an HTML capture error in the DOM view', async () => {
    const host = document.createElement('div'); const root = createRoot(host)
    try {
      await act(async () => root.render(<ScriptBrowserEvidence event={{ ...event, protocol_evidence: { html_capture_error: 'document unavailable' } }} running={false} />))
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'DOM')!.click())
      expect(host.textContent).toContain('HTML capture failed: document unavailable')
    } finally { await act(async () => root.unmount()) }
  })
  it('shows browser evidence omission receipts and unapplied extraction diagnostics', async () => {
    const host = document.createElement('div'); const root = createRoot(host)
    try {
      await act(async () => root.render(<ScriptBrowserEvidence event={{ ...event, protocol_evidence_omitted_count: 2, unapplied_extractions: { rejected: 'diagnostic-value' } }} running={false} />))
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Assertions')!.click())
      expect(host.textContent).toContain('2 protocol evidence items were omitted or shortened')
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Variables')!.click())
      expect(host.textContent).toContain('Unapplied extractions')
      expect(host.textContent).toContain('diagnostic-value')
      expect(host.textContent).toContain('not applied to the session variables')
    } finally { await act(async () => root.unmount()) }
  })
  it('shows one action, edits explicitly, and never substitutes another action screenshot', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host); const edit = vi.fn()
    try {
      await act(async () => root.render(<ScriptBrowserEvidence event={event} running={false} onEdit={edit} />))
      expect(host.textContent).toContain('Welcome')
      expect(host.textContent).toContain('Hello')
      expect(host.querySelector('img')).toBeNull()
      expect(host.textContent).toContain('No screenshot was captured for this action')
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Edit this assertion')!.click())
      expect(edit).toHaveBeenCalledOnce()
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Variables')!.click())
      expect(host.textContent).toContain('greeting')
      await act(async () => root.render(<ScriptBrowserEvidence event={{ ...event, action_index: 1, action_name: 'Next', success: true, protocol_evidence: {}, extractions: { next: 'second' } }} running={false} />))
      expect(host.textContent).not.toContain('Welcome')
      expect(host.textContent).not.toContain('greeting')
      expect(host.textContent).toContain('second')
      expect(host.querySelectorAll('.browser-debugger')).toHaveLength(0)
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('offers selector promotion after success but does not misapply a wait recommendation', async () => {
    const host = document.createElement('div'); const root = createRoot(host); const apply = vi.fn()
    const healed = { ...event, success: true, protocol_evidence: {}, repair_suggestions: [
      { id: 'wait', title: 'Tune wait', description: 'Wait for readiness', draft_patch_type: 'add_wait', fields: { selector: '#ready' } },
      { id: 'promote', title: 'Promote working selector', description: 'Verified fallback', draft_patch_type: 'promote_selector', fields: { selector: '#working' } },
    ] }
    try {
      await act(async () => root.render(<ScriptBrowserEvidence event={healed} running={false} onApplyLocator={apply} />))
      const buttons = Array.from(host.querySelectorAll('button')).filter(b => b.textContent === 'Apply suggested selector')
      expect(buttons).toHaveLength(1)
      await act(async () => buttons[0].click())
      expect(apply).toHaveBeenCalledWith(healed, '#working')
    } finally { await act(async () => root.unmount()) }
  })
  it('explains capture before a run', async () => {
    const host = document.createElement('div'); const root = createRoot(host)
    try { await act(async () => root.render(<ScriptBrowserEvidence running={false} />)); expect(host.textContent).toContain('Choose Debug > Run a single iteration to capture'); expect(host.textContent).not.toContain('Debug one iteration') } finally { await act(async () => root.unmount()) }
  })
  it('shows why native browser replay will not match the debugger, only when told', async () => {
    const host = document.createElement('div'); const root = createRoot(host)
    const start = vi.fn<() => Promise<string | null>>().mockResolvedValue(null)
    try {
      await act(async () => root.render(<ScriptBrowserEvidence running={false} onStartNativeReplay={start} />))
      expect(host.querySelector('[data-testid="native-replay-note"]')).toBeNull()
      await act(async () => root.render(<ScriptBrowserEvidence running={false} onStartNativeReplay={start} nativeReplayNote="2 session runtime overrides are not applied here." />))
      expect(host.querySelector('[aria-label="Native browser replay"] [data-testid="native-replay-note"]')?.textContent).toBe('2 session runtime overrides are not applied here.')
    } finally { await act(async () => root.unmount()) }
  })
  it('starts a native browser replay session, renders progress, and reports start errors', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const startNative = vi.fn<() => Promise<string | null>>()
      .mockRejectedValueOnce(new Error('Native HAR unsupported'))
      .mockResolvedValueOnce('replay-session-1')
    try {
      await act(async () => root.render(<ScriptBrowserEvidence running={false} nativeReplayDisabled={true} nativeReplayTitle="Requires editor role" onStartNativeReplay={startNative} />))
      const disabledBtn = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Run native browser replay')!
      expect(disabledBtn.disabled).toBe(true)
      expect(disabledBtn.getAttribute('title')).toBe('Requires editor role')

      await act(async () => root.render(<ScriptBrowserEvidence running={false} onStartNativeReplay={startNative} />))
      const runBtn = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Run native browser replay')!
      expect(runBtn.disabled).toBe(false)
      await act(async () => runBtn.click())
      expect(startNative).toHaveBeenCalledTimes(1)
      expect(host.textContent).toContain('Native HAR unsupported')
      expect(host.querySelector('[data-testid="replay-progress"]')).toBeNull()

      await act(async () => runBtn.click())
      expect(startNative).toHaveBeenCalledTimes(2)
      expect(host.textContent).not.toContain('Native HAR unsupported')
      const progress = host.querySelector('[data-testid="replay-progress"]')
      expect(progress?.getAttribute('data-session-id')).toBe('replay-session-1')
      expect(runBtn.disabled).toBe(true)

      await act(async () => (host.querySelector('button:nth-of-type(1)') as HTMLButtonElement | null)?.click())
      const completeBtn = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Complete replay')!
      await act(async () => completeBtn.click())
      expect(runBtn.disabled).toBe(false)

      const dismissBtn = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Dismiss replay')!
      await act(async () => dismissBtn.click())
      expect(host.querySelector('[data-testid="replay-progress"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('renders an accessible TabList with roving tabIndex, keyboard navigation, and controlled native replay callbacks', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const editorCss = readFileSync(`${cwd}/src/components/ScriptEditor.css`, 'utf8') as string
    expect(editorCss).toContain('.selected-evidence-tabs { display: flex; flex-wrap: nowrap; overflow-x: auto;')
    expect(editorCss).toContain('.selected-evidence-tabs button[aria-selected="true"]')
    expect(editorCss).toContain('border-bottom-color: var(--accent)')

    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const onCompleteNative = vi.fn()
    const onDismissNative = vi.fn()
    try {
      await act(async () => root.render(
        <ScriptBrowserEvidence
          event={event}
          running={false}
          nativeReplaySessionId="controlled-session-1"
          nativeReplayRunning={true}
          onStartNativeReplay={async () => 'controlled-session-1'}
          onCompleteNativeReplay={onCompleteNative}
          onDismissNativeReplay={onDismissNative}
        />,
      ))

      const tablist = host.querySelector<HTMLElement>('[role="tablist"][aria-label="Browser evidence views"]')!
      expect(tablist).not.toBeNull()
      const tabButtons = Array.from(tablist.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
      expect(tabButtons.map(btn => btn.textContent)).toEqual([
        'Screenshot',
        'Assertions',
        'DOM',
        'Network',
        'Browser console',
        'Variables',
      ])
      expect(tabButtons[0].getAttribute('aria-selected')).toBe('true')
      expect(tabButtons[0].tabIndex).toBe(0)
      expect(tabButtons[1].getAttribute('aria-selected')).toBe('false')
      expect(tabButtons[1].tabIndex).toBe(-1)
      expect(tabButtons[0].getAttribute('aria-pressed')).toBeNull()

      const panel = host.querySelector<HTMLElement>('[role="tabpanel"]')!
      expect(panel.id).toBe(tabButtons[0].getAttribute('aria-controls'))
      expect(panel.getAttribute('aria-labelledby')).toBe(tabButtons[0].id)

      tabButtons[0].focus()
      await act(async () => {
        tabButtons[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }))
      })
      expect(tabButtons[1].getAttribute('aria-selected')).toBe('true')
      expect(tabButtons[1].tabIndex).toBe(0)
      expect(panel.getAttribute('aria-labelledby')).toBe(tabButtons[1].id)

      await act(async () => {
        tabButtons[1].dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true, cancelable: true }))
      })
      expect(tabButtons[5].getAttribute('aria-selected')).toBe('true')
      expect(panel.getAttribute('aria-labelledby')).toBe(tabButtons[5].id)

      const runBtn = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Run native browser replay')!
      expect(runBtn.disabled).toBe(true)

      const completeBtn = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Complete replay')!
      await act(async () => completeBtn.click())
      expect(onCompleteNative).toHaveBeenCalledWith(mockCompletedReport, mockCompletedReport.events)

      const dismissBtn = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Dismiss replay')!
      await act(async () => dismissBtn.click())
      expect(onDismissNative).toHaveBeenCalledOnce()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('renders static terminal card without mounting live BrowserReplayProgress when controlled session has finished', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const onDismiss = vi.fn()
    try {
      await act(async () => root.render(
        <ScriptBrowserEvidence
          event={event}
          running={false}
          nativeReplaySessionId="done-session-1"
          nativeReplayRunning={false}
          nativeReplayReport={mockCompletedReport}
          onStartNativeReplay={async () => 'done-session-1'}
          onDismissNativeReplay={onDismiss}
        />,
      ))

      const progress = host.querySelector<HTMLElement>('[data-testid="replay-progress"]')!
      expect(progress).not.toBeNull()
      // Mocked BrowserReplayProgress sets data-session-id; static terminal card does not mount it.
      expect(progress.getAttribute('data-session-id')).toBeNull()
      expect(progress.textContent).toContain('Verified ✓')
      expect(progress.textContent).toContain('1 passed · 0 failed')
      expect(progress.textContent).toContain('Download replay HAR')
      expect(progress.textContent).toContain('Download replay evidence')
      expect(progress.textContent).toContain('Verified output and scope')
      expect(progress.querySelector('[role="alert"]')).toBeNull()

      await act(async () => root.render(
        <ScriptBrowserEvidence
          event={event}
          running={false}
          nativeReplaySessionId="done-session-1"
          nativeReplayRunning={false}
          nativeReplayReport={{ ...mockCompletedReport, stopped: true, success: false }}
          nativeReplayHarAvailable={true}
          onStartNativeReplay={async () => 'done-session-1'}
          onDismissNativeReplay={onDismiss}
        />,
      ))
      expect(host.querySelector('[data-testid="replay-progress"] .brp-status')?.textContent).toBe('Stopped')
      expect(host.textContent).toContain('Download replay HAR')

      await act(async () => root.render(
        <ScriptBrowserEvidence
          event={event}
          running={false}
          nativeReplaySessionId="reportless-session-1"
          nativeReplayRunning={false}
          nativeReplayReport={{ ...mockCompletedReport, script_hash: undefined, stopped: true, success: false }}
          nativeReplayHarAvailable={false}
          onStartNativeReplay={async () => 'reportless-session-1'}
          onDismissNativeReplay={onDismiss}
        />,
      ))
      const reportlessCard = host.querySelector<HTMLElement>('[data-testid="replay-progress"]')!
      expect(reportlessCard.querySelector('.brp-status')?.textContent).toBe('Stopped')
      expect(reportlessCard.textContent).toContain('Open home')
      expect(reportlessCard.textContent).not.toContain('Download replay HAR')
      expect(reportlessCard.textContent).not.toContain('Download replay evidence')
      expect(reportlessCard.textContent).not.toContain('Verified output and scope')
      expect(reportlessCard.querySelector('[role="alert"]')).toBeNull()
      expect(reportlessCard.querySelector('[role="status"]')).toBeNull()

      await act(async () => root.render(
        <ScriptBrowserEvidence
          event={event}
          running={false}
          nativeReplaySessionId="redacted-session-1"
          nativeReplayRunning={false}
          nativeReplayReport={{ ...mockCompletedReport, runtime_evidence_unavailable: true }}
          onStartNativeReplay={async () => 'redacted-session-1'}
          onDismissNativeReplay={onDismiss}
        />,
      ))
      const redactedCard = host.querySelector<HTMLElement>('[data-testid="replay-progress"]')!
      expect(redactedCard.querySelector('[role="status"]')?.textContent).toBe(
        'Runtime evidence unavailable. Run details and downloads cannot be shown.',
      )
      expect(redactedCard.textContent).not.toContain('Download replay HAR')
      expect(redactedCard.textContent).not.toContain('Download replay evidence')
      expect(redactedCard.textContent).not.toContain('Verified output and scope')
      expect(redactedCard.textContent).toContain('Action evidence unavailable')
      expect(redactedCard.textContent).not.toContain('Open home')

      await act(async () => root.render(
        <ScriptBrowserEvidence
          event={event}
          running={false}
          nativeReplaySessionId="missing-session-1"
          nativeReplayRunning={false}
          nativeReplayReport={null}
          nativeReplayUnavailable={true}
          onStartNativeReplay={async () => 'missing-session-1'}
          onDismissNativeReplay={onDismiss}
        />,
      ))
      const unavailableCard = host.querySelector<HTMLElement>('[data-testid="replay-progress"]')!
      expect(unavailableCard.querySelector('.brp-status')?.textContent).toBe('Issues found')
      expect(unavailableCard.querySelector('[role="alert"]')?.textContent).toContain('Replay session could not be confirmed.')
      expect(unavailableCard.textContent).not.toContain('Download replay HAR')

      const closeBtn = Array.from(unavailableCard.querySelectorAll('button')).find(b => b.textContent === 'Close')!
      await act(async () => closeBtn.click())
      expect(onDismiss).toHaveBeenCalledOnce()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
