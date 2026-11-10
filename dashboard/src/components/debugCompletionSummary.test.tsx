// @vitest-environment jsdom
import { act, StrictMode, type ReactNode } from 'react'
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
  it('opens an issue, exports session, downloads HAR, and explains unavailable downloads', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const snapshot = fixture()
    snapshot.session.har = { eligible: true, available: true, retained_request_count: 2, limitations: 'Retained traffic only; bodies may be unavailable.' }
    const onSelectEvent = vi.fn(); const onDownloadHAR = vi.fn().mockRejectedValue(new Error('Session expired.')); const onExportSession = vi.fn()
    const container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    const render = (unavailable = false) => act(() => root.render(<Summary snapshot={snapshot} stale unavailable={unavailable} onExportSession={onExportSession} onSelectEvent={onSelectEvent} onDownloadHAR={onDownloadHAR} />))
    const button = (text: string) => [...container.querySelectorAll('button')].find(node => node.textContent === text)!
    try {
      render()
      expect(container.textContent).toContain('earlier draft')
      expect(document.activeElement?.textContent).toBe('Debug summary')
      expect(button('Return to debugger')).toBeUndefined()
      expect(button('Debug again')).toBeUndefined()
      // Without retained transaction definitions it points at the Debug menu,
      // not the removed Debug again control.
      expect(container.textContent).toContain('Transaction definitions were not retained for this run. Start a new run from the Debug menu to capture the transaction breakdown.')
      expect(container.textContent).not.toContain('Debug again')

      // Verify Download HAR is placed in .debug-summary-actions right beside Export session
      const headerActionButtons = [...container.querySelectorAll('.debug-summary-heading .debug-summary-actions button')].map(node => node.textContent)
      expect(headerActionButtons).toEqual(['Export session', 'Download HAR'])

      // Verify Transactions and Errors and warnings tabs and panels
      const tabs = [...container.querySelectorAll<HTMLButtonElement>('[role="tablist"][aria-label="Debug summary sections"] [role="tab"]')]
      expect(tabs).toHaveLength(2)
      expect(tabs[0].textContent).toBe('Transactions')
      expect(tabs[0].getAttribute('aria-selected')).toBe('true')
      expect(tabs[1].textContent).toContain('Errors and warnings')
      expect(tabs[1].getAttribute('aria-selected')).toBe('false')
      const transactionsPanel = container.querySelector<HTMLElement>('#debug-summary-panel-transactions')!
      const issuesPanel = container.querySelector<HTMLElement>('#debug-summary-panel-issues')!
      expect(transactionsPanel.hidden).toBe(false)
      expect(issuesPanel.hidden).toBe(true)

      // Verify Execution waterfall is rendered inside the Transactions tabpanel
      const waterfallSection = transactionsPanel.querySelector('[aria-label="Execution waterfall"]')
      expect(waterfallSection).not.toBeNull()
      expect(waterfallSection?.querySelector('[role="treegrid"]')).not.toBeNull()
      // Init and Actions iteration headers, each with its execution.
      expect([...waterfallSection!.querySelectorAll('.wf-row.wf-kind-iteration')].map(row => row.querySelector('.wf-name-label')?.textContent)).toEqual(['Init', 'Actions'])
      expect(waterfallSection?.querySelectorAll('.wf-row[data-node-id]:not(.wf-kind-iteration)')).toHaveLength(2)
      // The first failure is selected and its details are open in the drawer.
      expect(waterfallSection?.querySelector('.wf-row.selected .wf-name-label')?.textContent).toBe('Checkout')
      expect(waterfallSection?.querySelector('[aria-label="Selected row details"]')?.textContent).toContain('Expected 200, got 500')

      // Switch to Errors and warnings tab and back
      act(() => tabs[1].click())
      expect(tabs[0].getAttribute('aria-selected')).toBe('false')
      expect(tabs[1].getAttribute('aria-selected')).toBe('true')
      expect(transactionsPanel.hidden).toBe(true)
      expect(issuesPanel.hidden).toBe(false)
      act(() => tabs[0].click())
      expect(transactionsPanel.hidden).toBe(false)
      expect(issuesPanel.hidden).toBe(true)

      act(() => button('Export session').click())
      expect(onExportSession).toHaveBeenCalledTimes(1)
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
      expect(button('Export session')).toBeDefined()

      // Switching tabs and selecting a waterfall row resets cleanly when snapshot.key changes
      render(false)
      const [txTab, errTab] = [...container.querySelectorAll<HTMLButtonElement>('[role="tablist"][aria-label="Debug summary sections"] [role="tab"]')]
      act(() => errTab.click())
      expect(errTab.getAttribute('aria-selected')).toBe('true')
      act(() => txTab.click())
      const executionRows = () => [...container.querySelectorAll<HTMLElement>('.wf-row[data-node-id]:not(.wf-kind-iteration)')]
      const firstRow = executionRows()[0]!
      act(() => firstRow.click())
      expect(executionRows()[0].getAttribute('aria-selected')).toBe('true')
      act(() => errTab.click())
      expect(errTab.getAttribute('aria-selected')).toBe('true')
      const nextRun: DebugCompletionSnapshot = { ...snapshot, key: 'run-2', session: { ...snapshot.session, id: 'run-2' } }
      act(() => root.render(<Summary snapshot={nextRun} stale={false} unavailable={false} onExportSession={onExportSession} onSelectEvent={onSelectEvent} onDownloadHAR={onDownloadHAR} />))
      const [nextTxTab, nextErrTab] = [...container.querySelectorAll<HTMLButtonElement>('[role="tablist"][aria-label="Debug summary sections"] [role="tab"]')]
      expect(nextTxTab.getAttribute('aria-selected')).toBe('true')
      expect(nextErrTab.getAttribute('aria-selected')).toBe('false')
      const nextRows = executionRows()
      expect(nextRows[0]?.getAttribute('aria-selected')).toBe('false')
      expect(nextRows[1]?.getAttribute('aria-selected')).toBe('true')
    } finally { act(() => root.unmount()); container.remove() }
  })

  it('shows the recording comparison as the Recording diff sub-tab and opens it on request', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    type TabRequest = import('./DebugCompletionSummary').DebugSummaryTabRequest
    const snapshot = fixture()
    const container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    const diff = <p data-testid="diff">Compared with the recording</p>
    const render = ({ recordingDiff = diff as ReactNode, tabRequest = null as TabRequest | null, unavailable = false } = {}) => act(() => root.render(
      <Summary snapshot={snapshot} stale={false} unavailable={unavailable} onExportSession={vi.fn()} onSelectEvent={vi.fn()} onDownloadHAR={vi.fn()}
        recordingDiff={recordingDiff} tabRequest={tabRequest} />))
    const tabs = () => [...container.querySelectorAll<HTMLButtonElement>('[role="tablist"][aria-label="Debug summary sections"] [role="tab"]')]
    const diffTab = () => container.querySelector<HTMLButtonElement>('#debug-summary-tab-recording-diff')
    const diffPanel = () => container.querySelector<HTMLElement>('#debug-summary-panel-recording-diff')
    const transactionsPanel = () => container.querySelector<HTMLElement>('#debug-summary-panel-transactions')!
    try {
      // A request already present at mount is not acted on.
      render({ tabRequest: { tab: 'recording-diff', id: 1 } })
      expect(tabs().map(tab => tab.firstElementChild?.textContent)).toEqual(['Transactions', 'Errors and warnings', 'Recording diff'])
      expect(diffTab()?.getAttribute('aria-selected')).toBe('false')
      expect(diffTab()?.tabIndex).toBe(-1)
      expect(diffTab()?.getAttribute('aria-controls')).toBe('debug-summary-panel-recording-diff')
      expect(diffPanel()?.getAttribute('role')).toBe('tabpanel')
      expect(diffPanel()?.getAttribute('aria-labelledby')).toBe('debug-summary-tab-recording-diff')
      expect(diffPanel()?.hidden).toBe(true)
      expect(transactionsPanel().hidden).toBe(false)

      act(() => diffTab()!.click())
      expect(diffTab()?.getAttribute('aria-selected')).toBe('true')
      expect(diffTab()?.tabIndex).toBe(0)
      expect(transactionsPanel().hidden).toBe(true)
      expect(diffPanel()?.hidden).toBe(false)
      const section = diffPanel()!.querySelector('section[aria-label="Recording diff"]')!
      expect(section.querySelector('h3')?.textContent).toBe('Recording diff')
      expect(section.querySelector('[data-testid="diff"]')?.textContent).toBe('Compared with the recording')

      // A newer request switches to the tab and focuses it; the same request again does not.
      act(() => tabs()[0].click())
      expect(diffPanel()?.hidden).toBe(true)
      render({ tabRequest: { tab: 'recording-diff', id: 2 } })
      expect(diffTab()?.getAttribute('aria-selected')).toBe('true')
      expect(diffPanel()?.hidden).toBe(false)
      expect(document.activeElement).toBe(diffTab())
      act(() => tabs()[1].click())
      render({ tabRequest: { tab: 'recording-diff', id: 2 } })
      expect(tabs()[1].getAttribute('aria-selected')).toBe('true')
      expect(diffPanel()?.hidden).toBe(true)
      render({ tabRequest: { tab: 'recording-diff', id: 3 } })
      expect(diffTab()?.getAttribute('aria-selected')).toBe('true')

      // Without a comparison there is no third tab, and a request for it shows Transactions.
      render({ recordingDiff: null, tabRequest: { tab: 'recording-diff', id: 4 } })
      expect(tabs().map(tab => tab.firstElementChild?.textContent)).toEqual(['Transactions', 'Errors and warnings'])
      expect(diffPanel()).toBeNull()
      expect(tabs()[0].getAttribute('aria-selected')).toBe('true')
      expect(transactionsPanel().hidden).toBe(false)
      expect(container.querySelector('[aria-label="Recording diff"]')).toBeNull()

      // With runtime evidence unavailable the comparison is a plain section under the warning.
      render({ unavailable: true, tabRequest: { tab: 'recording-diff', id: 4 } })
      expect(container.textContent).toContain('Runtime evidence unavailable.')
      expect(tabs()).toHaveLength(0)
      expect(diffPanel()).toBeNull()
      const plain = container.querySelector('section[aria-label="Recording diff"]')
      expect(plain?.querySelector('h3')?.textContent).toBe('Recording diff')
      expect(plain?.querySelector('[data-testid="diff"]')).not.toBeNull()
    } finally { act(() => root.unmount()); container.remove() }
  })

  it('focuses the plain Recording diff once per request, when it is not a sub-tab', async () => {
    const { default: Summary, DebugSummaryRecordingDiff } = await import('./DebugCompletionSummary')
    const snapshot = fixture()
    const container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    const outside = document.createElement('button'); document.body.appendChild(outside)
    let pending = false
    const consume = () => { const value = pending; pending = false; return value }
    const heading = () => container.querySelector<HTMLElement>('section[aria-label="Recording diff"] h3')
    try {
      // Live or paused run: the section alone (ScriptEditor's fallback).
      outside.focus()
      act(() => root.render(<DebugSummaryRecordingDiff consumeFocusRequest={consume}><p>diff</p></DebugSummaryRecordingDiff>))
      expect(document.activeElement).toBe(outside)
      pending = true
      act(() => root.render(<DebugSummaryRecordingDiff consumeFocusRequest={consume}><p>diff again</p></DebugSummaryRecordingDiff>))
      expect(heading()?.tabIndex).toBe(-1)
      expect(document.activeElement).toBe(heading())
      // A later render without a new request leaves focus alone.
      outside.focus()
      act(() => root.render(<DebugSummaryRecordingDiff consumeFocusRequest={consume}><p>diff</p></DebugSummaryRecordingDiff>))
      expect(document.activeElement).toBe(outside)

      // Runtime evidence unavailable: the summary's plain section takes focus on request too.
      pending = true
      act(() => root.render(<Summary snapshot={snapshot} stale={false} unavailable onExportSession={vi.fn()} onSelectEvent={vi.fn()} onDownloadHAR={vi.fn()}
        recordingDiff={<p>diff</p>} tabRequest={{ tab: 'recording-diff', id: 1 }} consumeRecordingDiffFocus={consume} />))
      expect(document.activeElement).toBe(heading())
    } finally { act(() => root.unmount()); container.remove(); outside.remove() }
  })

  it('sorts and filters the errors and warnings table', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const snapshot = fixture()
    snapshot.report!.error = ''
    snapshot.session.error = ''
    snapshot.report!.events = [
      { event_id: 'one', event_index: 0, action_index: 0, phase: 'init', action_name: 'Beta init', action_type: 'request', target: '/init', success: false, error_message: 'Zebra init failure', status_code: 401, started_at: '2026-10-25T12:00:00Z', completed_at: '2026-10-25T12:00:01Z', duration_ms: 1000 },
      { event_id: 'two', event_index: 1, action_index: 1, phase: 'action', action_name: 'Alpha checkout', action_type: 'request', target: '/checkout', success: false, error_message: 'Alpha checkout failure', status_code: 503, started_at: '2026-10-25T12:00:01Z', completed_at: '2026-10-25T12:00:02Z', duration_ms: 1000 },
    ]
    snapshot.session.logs = [
      { id: 'warn-1', timestamp: snapshot.report!.completed_at, level: 'warn', category: 'capture', message: 'Medium warning on teardown', action_name: 'Gamma cleanup', phase: 'teardown' },
    ]
    const onSelectEvent = vi.fn()
    const container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    const setControlValue = (el: HTMLInputElement | HTMLSelectElement, val: string) => {
      const proto = el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
      const setter = Object.getOwnPropertyDescriptor(proto, 'value')?.set
      setter?.call(el, val)
      el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
      el.dispatchEvent(new Event('change', { bubbles: true }))
    }
    try {
      act(() => root.render(<Summary snapshot={snapshot} stale={false} unavailable={false} onExportSession={vi.fn()} onSelectEvent={onSelectEvent} onDownloadHAR={vi.fn()} />))
      const region = container.querySelector('[role="region"][aria-label="Errors and warnings table"]')!
      expect(region).not.toBeNull()
      expect(region.getAttribute('tabindex')).toBe('0')
      const table = region.querySelector('table.debug-summary-issues-table')!
      expect(table.getAttribute('data-table-pagination')).toBe('off')
      const rowActions = () => [...table.querySelectorAll('tbody tr.debug-summary-issue-row th[scope="row"]')].map(cell => cell.textContent)
      expect(rowActions()).toEqual(['Beta init', 'Alpha checkout', 'Gamma cleanup'])

      const sortBy = (label: string) => {
        const btn = [...table.querySelectorAll<HTMLButtonElement>('thead button.debug-table-sort-btn')].find(b => b.textContent?.includes(label))!
        act(() => btn.click())
        return btn.closest('th')!
      }

      // Sort by Action ascending, then descending
      const actionTh = sortBy('Action')
      expect(actionTh.getAttribute('aria-sort')).toBe('ascending')
      expect(rowActions()).toEqual(['Alpha checkout', 'Beta init', 'Gamma cleanup'])
      sortBy('Action')
      expect(actionTh.getAttribute('aria-sort')).toBe('descending')
      expect(rowActions()).toEqual(['Gamma cleanup', 'Beta init', 'Alpha checkout'])

      // Sort by Status ascending, then descending
      const statusTh = sortBy('Status')
      expect(statusTh.getAttribute('aria-sort')).toBe('ascending')
      expect(actionTh.getAttribute('aria-sort')).toBe('none')
      expect(rowActions()).toEqual(['Gamma cleanup', 'Beta init', 'Alpha checkout'])
      sortBy('Status')
      expect(statusTh.getAttribute('aria-sort')).toBe('descending')
      expect(rowActions()).toEqual(['Alpha checkout', 'Beta init', 'Gamma cleanup'])

      // Sort by Severity ascending, then descending
      const severityTh = sortBy('Severity')
      expect(severityTh.getAttribute('aria-sort')).toBe('ascending')
      expect(rowActions()[2]).toBe('Gamma cleanup')
      sortBy('Severity')
      expect(severityTh.getAttribute('aria-sort')).toBe('descending')
      expect(rowActions()[0]).toBe('Gamma cleanup')

      // Sort by Phase and Message
      expect(sortBy('Phase').getAttribute('aria-sort')).toBe('ascending')
      expect(rowActions()).toEqual(['Alpha checkout', 'Beta init', 'Gamma cleanup'])
      expect(sortBy('Message').getAttribute('aria-sort')).toBe('ascending')
      expect(rowActions()).toEqual(['Alpha checkout', 'Gamma cleanup', 'Beta init'])

      // Filter by Severity
      const selects = container.querySelectorAll<HTMLSelectElement>('.debug-summary-issues-toolbar select')
      const severitySelect = selects[0]!
      const phaseSelect = selects[1]!
      act(() => setControlValue(severitySelect, 'warning'))
      expect(rowActions()).toEqual(['Gamma cleanup'])
      act(() => setControlValue(severitySelect, 'error'))
      expect(rowActions()).toEqual(['Alpha checkout', 'Beta init'])
      act(() => setControlValue(severitySelect, 'all'))
      expect(rowActions()).toHaveLength(3)

      // Filter by Phase
      act(() => setControlValue(phaseSelect, 'init'))
      expect(rowActions()).toEqual(['Beta init'])
      act(() => setControlValue(phaseSelect, 'all'))

      // Filter by search text
      const searchInput = container.querySelector<HTMLInputElement>('.debug-summary-issues-toolbar input[type="search"]')!
      act(() => setControlValue(searchInput, '503'))
      expect(rowActions()).toEqual(['Alpha checkout'])
      act(() => setControlValue(searchInput, 'no-match-xyz'))
      expect(rowActions()).toEqual([])
      expect(table.textContent).toContain('No errors or warnings match the current filters.')
    } finally { act(() => root.unmount()); container.remove() }
  })
})


describe('debug summary transaction table', () => {
  it('expands parent rows, preserves exact timestamps, links evidence, and hides withheld rows', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const run = fixture()
    run.transactions = captureDebugTransactionDefinitions({ name: 'Checkout script', version: '1.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Checkout', type: 'http_sequence', target: '', params: { requests: [{ name: 'Pay', target: '/pay' }] } }] })
    run.report!.events = [{
      ...run.report!.events[1],
      action_index: 0,
      request_index: 0,
      event_index: 0,
      protocol_evidence: {
        timings: {
          dns_ms: 50,
          connect_ms: 100,
          tls_ms: 150,
          ttfb_ms: 800,
          duration_ms: 1000,
        },
      },
      network_entries: [
        {
          url: 'https://example.test/pay',
          method: 'POST',
          status_code: 302,
          duration_ms: 400,
          har: { phases: [{ name: 'wait', start_ms: 0, end_ms: 400 }] },
        },
        {
          url: 'https://example.test/pay/confirm',
          method: 'GET',
          status_code: 200,
          duration_ms: 600,
          har: { phases: [{ name: 'wait', start_ms: 0, end_ms: 600 }] },
        },
      ],
    }]
    const consoleErrorSpy = vi.spyOn(console, 'error')
    const inspect = vi.fn(), container = document.createElement('div'), root = createRoot(container)
    document.body.appendChild(container)
    const render = (unavailable = false) => act(() => root.render(<Summary snapshot={run} stale={false} unavailable={unavailable} onExportSession={vi.fn()} onSelectEvent={inspect} onDownloadHAR={vi.fn()} />))
    const rowByLabel = (label: string) => [...container.querySelectorAll<HTMLElement>('.wf-row[data-node-id]')].find(row => row.querySelector('.wf-name-label')?.textContent === label)
    try {
      render()
      const grid = container.querySelector('[role="treegrid"]')!
      expect(grid.getAttribute('aria-label')).toBe('Transactions and executions')
      expect(grid.textContent).toContain('Duration')
      // Transactions start expanded: Actions › Checkout › Pay.
      const checkout = rowByLabel('Checkout')!
      expect(checkout.classList.contains('wf-kind-transaction')).toBe(true)
      expect(checkout.getAttribute('aria-expanded')).toBe('true')
      const pay = rowByLabel('Pay')!
      expect(pay.getAttribute('aria-level')).toBe('3')
      expect(pay.querySelectorAll('.wf-seg.phase-wait')).toHaveLength(2)
      expect(pay.textContent).toContain('↪ 1')
      expect(pay.textContent).toContain('Measured')
      // The parent draws its child's real span instead of phase totals.
      expect(checkout.querySelector('.wf-bar-parent .wf-mini')).not.toBeNull()
      // Redirect hops stay collapsed until the request is expanded.
      expect(container.querySelectorAll('.wf-kind-hop')).toHaveLength(0)
      act(() => pay.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
      expect(container.querySelectorAll('.wf-kind-hop')).toHaveLength(2)
      expect(consoleErrorSpy.mock.calls.some(call => String(call[0] ?? '').includes('same key'))).toBe(false)
      // Selecting the transaction explains its elapsed time in the drawer.
      act(() => rowByLabel('Checkout')!.click())
      const drawer = () => container.querySelector('[aria-label="Selected row details"]')!
      expect(drawer().querySelector('[aria-label="Timing breakdown for Checkout"]')?.textContent).toContain('outside child spans')
      expect(drawer().querySelector('time')?.getAttribute('datetime')).toBe(run.report!.events[0].started_at)
      expect(drawer().textContent).toContain('Self time')
      // Selecting the request offers inspection of its execution.
      act(() => rowByLabel('Pay')!.click())
      const inspectButton = [...drawer().querySelectorAll('button')].find(b => b.textContent === 'Inspect Pay')!
      act(() => inspectButton.click())
      expect(inspect).toHaveBeenCalledWith(0)
      expect([...drawer().querySelectorAll('[role="tab"]')].map(t => t.textContent)).toEqual(['Timing', 'Request', 'Response', 'Headers', 'Correlation', 'Transport'])
      // Execution rows keep start/end/elapsed/action time from the old table.
      expect(drawer().querySelectorAll('time')).toHaveLength(2)
      expect(drawer().textContent).toContain('Elapsed')
      expect(drawer().textContent).toContain('Action time')
      // Collapsing the transaction hides its children.
      act(() => rowByLabel('Checkout')!.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
      expect(rowByLabel('Pay')).toBeUndefined()
      render(true)
      expect(container.querySelector('[aria-label="Execution waterfall"]')).toBeNull()
    } finally {
      consoleErrorSpy.mockRestore()
      act(() => root.unmount())
      container.remove()
    }
  })

  it('aggregates leaf request phases for marker transactions and omits waterfall bars when durationMs is null', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const run = fixture()
    run.transactions = captureDebugTransactionDefinitions({
      name: 'Marker script',
      version: '1.0',
      protocol: 'http',
      config: { base_url: '' },
      actions: [
        { name: 'Start Checkout', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['Checkout'] } },
        { name: 'Cart', type: 'request', method: 'GET', target: '/cart' },
        { name: 'End Checkout', type: 'source_transaction', target: '', params: { function: 'lr_end_transaction', args: ['Checkout', 'LR_AUTO'] } },
        { name: 'Start Open', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['Open'] } },
        { name: 'End Open', type: 'source_transaction', target: '', params: { function: 'lr_end_transaction', args: ['Open', 'LR_AUTO'] } },
        { name: 'Start Unrun', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['Unrun'] } },
        { name: 'End Unrun', type: 'source_transaction', target: '', params: { function: 'lr_end_transaction', args: ['Unrun', 'LR_AUTO'] } },
        { name: 'Manual report', type: 'source_transaction', target: '', params: { function: 'lr_set_transaction', args: ['Manual', '1.5', 'LR_PASS'] } },
      ],
    })
    run.report!.events = [
      { event_id: 'e0', event_index: 0, action_index: 0, phase: 'action', action_name: 'Start Checkout', action_type: 'source_transaction', target: '', success: true, started_at: '2026-10-25T12:00:00.000Z', completed_at: '2026-10-25T12:00:00.000Z', duration_ms: 0 },
      {
        event_id: 'e1',
        event_index: 1,
        action_index: 1,
        phase: 'action',
        action_name: 'Cart',
        action_type: 'request',
        target: '/cart',
        success: true,
        started_at: '2026-10-25T12:00:00.100Z',
        completed_at: '2026-10-25T12:00:00.300Z',
        duration_ms: 200,
        protocol_evidence: { timings: { dns_ms: 20, connect_ms: 30, tls_ms: 50, ttfb_ms: 150, duration_ms: 200 } },
      },
      { event_id: 'e2', event_index: 2, action_index: 2, phase: 'action', action_name: 'End Checkout', action_type: 'source_transaction', target: '', success: true, started_at: '2026-10-25T12:00:01.000Z', completed_at: '2026-10-25T12:00:01.000Z', duration_ms: 0 },
      { event_id: 'e3', event_index: 3, action_index: 3, phase: 'action', action_name: 'Start Open', action_type: 'source_transaction', target: '', success: true, started_at: '2026-10-25T12:00:01.000Z', completed_at: '2026-10-25T12:00:01.000Z', duration_ms: 0 },
      { event_id: 'e7', event_index: 4, action_index: 7, phase: 'action', action_name: 'Manual report', action_type: 'source_transaction', target: '', success: true, started_at: '2026-10-25T12:00:01.000Z', completed_at: '2026-10-25T12:00:01.000Z', duration_ms: 0 },
    ]
    const container = document.createElement('div'), root = createRoot(container)
    try {
      act(() => root.render(<Summary snapshot={run} stale={false} unavailable={false} onExportSession={vi.fn()} onSelectEvent={vi.fn()} onDownloadHAR={vi.fn()} />))
      const rowByLabel = (label: string) => [...container.querySelectorAll<HTMLElement>('#debug-summary-panel-transactions .wf-row[data-node-id]')].find(row => row.querySelector('.wf-name-label')?.textContent === label)
      // Checkout (marker transaction) spans the full 0..1000 ms window and nests its markers and Cart.
      const checkoutBar = rowByLabel('Checkout')!.querySelector<HTMLElement>('.wf-bar')
      expect(checkoutBar?.style.left).toBe('0%')
      expect(checkoutBar?.style.width).toBe('100%')
      expect(rowByLabel('Cart')!.getAttribute('aria-level')).toBe('3')
      expect(rowByLabel('Start Checkout')!.getAttribute('aria-level')).toBe('3')
      expect(rowByLabel('End Checkout')!.getAttribute('aria-level')).toBe('3')
      expect(rowByLabel('Cart')!.querySelector('.wf-seg.phase-dns')).not.toBeNull()
      expect(rowByLabel('Cart')!.querySelector('.wf-seg.phase-wait')).not.toBeNull()
      expect(rowByLabel('Cart')!.textContent).toContain('Derived')
      // Open (no end marker) and Manual (lr_set_transaction) have no retained span; Unrun never ran.
      expect(rowByLabel('Open')!.querySelector('.wf-untimed')?.textContent).toBe('No timing')
      expect(rowByLabel('Manual')!.querySelector('.wf-untimed')?.textContent).toBe('No timing')
      // Subtrees that never ran start collapsed so they do not bury the run.
      expect(rowByLabel('Not run')!.classList.contains('wf-kind-iteration')).toBe(true)
      expect(rowByLabel('Not run')!.getAttribute('aria-expanded')).toBe('false')
      expect(rowByLabel('Unrun')).toBeUndefined()
      act(() => rowByLabel('Not run')!.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
      expect(rowByLabel('Unrun')!.querySelector('.wf-untimed')?.textContent).toBe('Not run')
    } finally { act(() => root.unmount()) }
  })
  it('lists defined transactions even when no executions were retained', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const run = fixture()
    run.transactions = captureDebugTransactionDefinitions({ name: 'Empty', version: '1.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Home', type: 'request', method: 'GET', target: '/', metadata: { source_transaction: 'Browse' } }] })
    run.report!.events = []
    const container = document.createElement('div'), root = createRoot(container)
    try {
      act(() => root.render(<Summary snapshot={run} stale={false} unavailable={false} onExportSession={vi.fn()} onSelectEvent={vi.fn()} onDownloadHAR={vi.fn()} />))
      const grid = container.querySelector('[role="treegrid"]')
      expect(grid).not.toBeNull()
      expect(grid?.textContent).toContain('Not run')
    } finally { act(() => root.unmount()) }
  })
})

describe('failures that follow the first failure', () => {
  function followerFixture(): DebugCompletionSnapshot {
    const snapshot = fixture()
    const report = snapshot.report!
    report.events = [
      { ...report.events[0], event_id: 'login', success: false, error_message: 'extractor "c_cshc" found no match: no match' },
      { ...report.events[1], event_id: 'aura-1', status_code: 401, error_message: 'expected status 200, got 401', diagnosis: { category: 'assertion', severity: 'error', cause: 'expected status 200, got 401', follows_failure_event_id: 'login', follows_failure_action: 'Login', follows_reason: 'aura_invalid_session' } },
      { ...report.events[1], event_id: 'aura-2', event_index: 2, status_code: 401, error_message: 'expected status 200, got 401' },
    ]
    report.diagnosis_summary = { failure_count: 3, first_failure_event_id: 'login', first_failure_action: 'Login', following_failure_count: 2 }
    return snapshot
  }

  it('says how many failures follow the first one and where it is', () => {
    const model = debugSummaryModel(followerFixture())
    expect(model.followingFailures).toEqual({ text: '2 of 3 failures follow the first failure (Login).', eventIndex: 0 })
    // None linked, none said.
    const none = followerFixture(); none.report!.diagnosis_summary!.following_failure_count = 0
    expect(debugSummaryModel(none).followingFailures).toBeNull()
    delete none.report!.diagnosis_summary
    expect(debugSummaryModel(none).followingFailures).toBeNull()
    // An evicted first failure is not offered; unavailable evidence says nothing.
    const evicted = followerFixture(); evicted.report!.events.shift()
    expect(debugSummaryModel(evicted).followingFailures).toMatchObject({ eventIndex: null })
    const unavailable = followerFixture(); unavailable.report!.runtime_evidence_unavailable = true
    expect(debugSummaryModel(unavailable).followingFailures).toBeNull()
    // Without an action name the sentence still reads.
    const unnamed = followerFixture(); delete unnamed.report!.diagnosis_summary!.first_failure_action
    expect(debugSummaryModel(unnamed).followingFailures?.text).toBe('2 of 3 failures follow the first failure.')
  })

  it('shows the line near the counts and selects the first failure', async () => {
    const { default: Summary } = await import('./DebugCompletionSummary')
    const onSelectEvent = vi.fn()
    const container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    const render = (snapshot: DebugCompletionSnapshot) => act(() => root.render(<Summary snapshot={snapshot} stale={false} unavailable={false} onExportSession={vi.fn()} onSelectEvent={onSelectEvent} onDownloadHAR={vi.fn()} />))
    try {
      render(followerFixture())
      const line = container.querySelector('.debug-summary-following')!
      expect(line.textContent).toContain('2 of 3 failures follow the first failure (Login).')
      expect(line.previousElementSibling?.classList.contains('debug-summary-facts')).toBe(true)
      const go = [...line.querySelectorAll('button')].find(node => node.textContent === 'Go to first failure')!
      act(() => go.click())
      expect(onSelectEvent).toHaveBeenCalledWith(0)

      const evicted = followerFixture(); evicted.key = 'run-2'; evicted.report!.events.shift()
      render(evicted)
      expect(container.querySelector('.debug-summary-following')?.textContent).toContain('2 of 3 failures follow the first failure (Login).')
      expect(container.querySelector('.debug-summary-following button')).toBeNull()

      const plain = fixture(); plain.key = 'run-3'
      render(plain)
      expect(container.querySelector('.debug-summary-following')).toBeNull()
    } finally { act(() => root.unmount()); container.remove() }
  })
})

