// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, it, expect, vi } from 'vitest'
import ScriptBrowserEvidence from './ScriptBrowserEvidence'
import type { BrowserReplayEvent } from '../types/replay'

vi.mock('./BrowserReplayProgress', () => ({
  default: ({ replaySessionId, onComplete, onDismiss }: { replaySessionId: string; onComplete?: () => void; onDismiss?: () => void }) => (
    <div data-testid="replay-progress" data-session-id={replaySessionId}>
      <button type="button" onClick={() => onComplete?.()}>Complete replay</button>
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
    try { await act(async () => root.render(<ScriptBrowserEvidence running={false} />)); expect(host.textContent).toContain('Debug one iteration') } finally { await act(async () => root.unmount()) }
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
})
