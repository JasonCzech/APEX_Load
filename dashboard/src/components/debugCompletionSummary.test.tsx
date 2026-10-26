// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
import { describe, expect, it, vi } from 'vitest'
import { debugCompletionKey, debugSummaryModel, useDebugCompletionSummary, type DebugCompletionSnapshot } from './debugCompletionModel'
import { captureDebugTransactionDefinitions } from './debugTransactionModel'
import type { BrowserReplayReport, ScriptDebugSession } from '../types/replay'

function fixture(): DebugCompletionSnapshot {
  const report: BrowserReplayReport = { replay_session_id: 'run-1', group: 'Checkout', protocol: 'http', started_at: '2026-10-25T12:00:00Z', completed_at: '2026-10-25T12:00:02Z', success: false, error: 'Expected 200, got 500', events: [
    { event_id: 'one', event_index: 0, action_index: 0, phase: 'init', action_name: 'Login', action_type: 'request', target: '/login', success: true, started_at: '2026-10-25T12:00:00Z', completed_at: '2026-10-25T12:00:01Z', duration_ms: 1000 },
    { event_id: 'two', event_index: 1, action_index: 1, phase: 'action', action_name: 'Checkout', action_type: 'request', target: '/checkout', success: false, error_message: 'Expected 200, got 500', status_code: 500, started_at: '2026-10-25T12:00:01Z', completed_at: '2026-10-25T12:00:02Z', duration_ms: 1000 },
  ] }
  const session: ScriptDebugSession = { id: 'run-1', status: 'failed', group: 'Checkout', protocol: 'http', log_level: 'info', current_event_index: 1, started_at: report.started_at, completed_at: report.completed_at, report, error: report.error, logs: [
    { id: 'duplicate', timestamp: report.completed_at, level: 'error', category: 'action', message: 'Checkout failed', action_name: 'Checkout', phase: 'action' },
    { id: 'warn', timestamp: report.completed_at, level: 'warn', category: 'capture', message: 'Evidence truncated' },
  ] }
  return { key: 'run-1', scriptName: 'Store', draft: true, report, session }
}

describe('debug completion summary', () => {
  it('counts retained leaves and deduplicates action/session errors', () => {
    const snapshot = fixture()
    snapshot.report!.events.push({ ...snapshot.report!.events[1] }, { ...snapshot.report!.events[1], event_id: 'parent', action_type: 'http_sequence' })
    const model = debugSummaryModel(snapshot)
    expect(model.passed).toBe(1); expect(model.failed).toBe(1)
    expect(model.issues).toHaveLength(2)
    expect(model.issues[0]).toMatchObject({ eventIndex: 1, statusCode: 500, phase: 'action' })
    expect(model.issues[1].severity).toBe('warning')
    expect(model.elapsedMs).toBe(2000)
  })
  it('retains the same warning on different actions while deduplicating repeated log entries', () => {
    const snapshot = fixture()
    const warning = snapshot.session.logs![1]
    snapshot.session.logs = [{ ...warning, action_name: 'Login', action_index: 0 }, { ...warning, id: 'other', action_name: 'Checkout', action_index: 1 }, { ...warning, id: 'duplicate', action_name: 'Login', action_index: 0 }]
    expect(debugSummaryModel(snapshot).issues.filter(issue => issue.severity === 'warning').map(issue => issue.actionName)).toEqual(['Login', 'Checkout'])
  })
  it('marks stopped runs and incomplete evidence and hides unavailable details', () => {
    const snapshot = fixture(); snapshot.session.status = 'stopped'; snapshot.report!.dropped_event_count = 2
    expect(debugSummaryModel(snapshot)).toMatchObject({ outcome: 'Stopped', incomplete: true })
    snapshot.session.runtime_evidence_unavailable = true
    expect(debugSummaryModel(snapshot)).toMatchObject({ issues: [], passed: 0, failed: 0, unavailable: true })
  })
  it('requires authoritative terminal state, not a pause or connection error', () => {
    const { session, report } = fixture()
    expect(debugCompletionKey({ ...session, status: 'paused' }, report, false)).toBeNull()
    expect(debugCompletionKey(session, report, true)).toBeNull()
    expect(debugCompletionKey(null, report, false)).toBeNull()
    expect(debugCompletionKey(session, { ...report!, completed_at: '' }, false)).toBeNull()
    expect(debugCompletionKey(session, { ...report!, replay_session_id: 'old-run' }, false)).toBeNull()
    expect(debugCompletionKey(session, report, false)).not.toBeNull()
  })
  it('opens once, freezes the completed snapshot, and opens again for a new run', async () => {
    const { session, report } = fixture(); const onComplete = vi.fn()
    const transactions = captureDebugTransactionDefinitions({ name: 'Original', version: '1.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Original transaction', type: 'http_sequence', target: '', params: { requests: [{ name: 'Child', target: '/' }] } }] })
    let snapshot: DebugCompletionSnapshot | null = null
    function Harness(props: Parameters<typeof useDebugCompletionSummary>[0]) { snapshot = useDebugCompletionSummary(props); return null }
    const root = createRoot(document.createElement('div'))
    const render = (props: Parameters<typeof useDebugCompletionSummary>[0]) => act(() => root.render(<StrictMode><Harness {...props} /></StrictMode>))
    try {
      render({ session, report, running: true, scriptName: 'Original', draft: true, onComplete })
      expect(snapshot).toBeNull()
      render({ session, report, running: false, scriptName: 'Original', draft: true, transactions, onComplete })
      expect(onComplete).toHaveBeenCalledTimes(1)
      render({ session: { ...session }, report: { ...report! }, running: false, scriptName: 'Edited', draft: true, onComplete })
      expect(onComplete).toHaveBeenCalledTimes(1)
      expect((snapshot as DebugCompletionSnapshot | null)?.scriptName).toBe('Original')
      transactions[0].name = 'Edited transaction'
      expect((snapshot as DebugCompletionSnapshot | null)?.transactions?.[0].name).toBe('Original transaction')
      render({ session: { ...session, id: 'run-2' }, report: { ...report!, replay_session_id: 'run-2' }, running: false, scriptName: 'Edited', draft: true, onComplete })
      expect(onComplete).toHaveBeenCalledTimes(2)
    } finally { act(() => root.unmount()) }
  })
})

describe('debug summary page', () => {
  it('opens an issue, downloads HAR, and explains unavailable downloads', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const snapshot = fixture()
    snapshot.session.har = { eligible: true, available: true, retained_request_count: 2, limitations: 'Retained traffic only; bodies may be unavailable.' }
    const onSelectEvent = vi.fn(); const onDownloadHAR = vi.fn().mockRejectedValue(new Error('Session expired.'))
    const container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    const render = (unavailable = false) => act(() => root.render(<Summary snapshot={snapshot} stale unavailable={unavailable} onReturn={vi.fn()} onDebugAgain={vi.fn()} onSelectEvent={onSelectEvent} onDownloadHAR={onDownloadHAR} />))
    const button = (text: string) => [...container.querySelectorAll('button')].find(node => node.textContent === text)!
    try {
      render()
      expect(container.textContent).toContain('earlier draft')
      expect(document.activeElement?.textContent).toBe('Debug summary')
      act(() => button('Inspect Checkout').click())
      expect(onSelectEvent).toHaveBeenCalledWith(1)
      await act(async () => button('Download HAR').click())
      expect(onDownloadHAR).toHaveBeenCalledTimes(1)
      expect(container.querySelector('[role="alert"]')?.textContent).toBe('Session expired.')
      snapshot.session.har.available = false; snapshot.session.har.reason = 'No network traffic was retained for this run.'
      render()
      expect(button('Download HAR')).toBeUndefined()
      expect(container.textContent).toContain('No network traffic was retained')
      render(true)
      expect(container.textContent).not.toContain('Expected 200, got 500')
      expect(button('Inspect Checkout')).toBeUndefined()
    } finally { act(() => root.unmount()); container.remove() }
  })
})


describe('debug summary transaction table', () => {
  it('expands parent rows, preserves exact timestamps, links evidence, and hides withheld rows', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const run = fixture()
    run.transactions = captureDebugTransactionDefinitions({ name: 'Checkout script', version: '1.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Checkout', type: 'http_sequence', target: '', params: { requests: [{ name: 'Pay', target: '/pay' }] } }] })
    run.report!.events = [{ ...run.report!.events[1], action_index: 0, request_index: 0, event_index: 0 }]
    const inspect = vi.fn(), container = document.createElement('div'), root = createRoot(container)
    const render = (unavailable = false) => act(() => root.render(<Summary snapshot={run} stale={false} unavailable={unavailable} onReturn={vi.fn()} onDebugAgain={vi.fn()} onSelectEvent={inspect} onDownloadHAR={vi.fn()} />))
    try {
      render()
      const table = container.querySelector('table')!
      expect(table.textContent).toContain('Elapsed time (ms)')
      expect(table.getAttribute('data-table-pagination')).toBe('off')
      expect(table.textContent).not.toContain('Pay')
      const expand = table.querySelector<HTMLButtonElement>('button[aria-expanded]')!
      expect(expand.getAttribute('aria-expanded')).toBe('false')
      act(() => expand.click())
      expect(table.textContent).toContain('Pay')
      expect(table.querySelector('[aria-label="Timing breakdown for Checkout"]')?.textContent).toContain('outside child spans')
      expect(table.textContent).toContain('Action time:')
      expect(expand.getAttribute('aria-expanded')).toBe('true')
      expect(table.querySelector('time')?.getAttribute('datetime')).toBe(run.report!.events[0].started_at)
      const child = [...table.querySelectorAll('tr')].find(row => row.textContent?.includes('Pay'))!
      act(() => child.querySelector<HTMLButtonElement>('button')!.click())
      expect(inspect).toHaveBeenCalledWith(0)
      act(() => expand.click())
      expect(table.textContent).not.toContain('Pay')
      render(true)
      expect(container.querySelector('[aria-label="Transaction details"]')).toBeNull()
    } finally { act(() => root.unmount()) }
  })
})
