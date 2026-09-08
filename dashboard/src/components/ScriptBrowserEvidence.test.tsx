// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, it, expect, vi } from 'vitest'
import ScriptBrowserEvidence from './ScriptBrowserEvidence'
import type { BrowserReplayEvent } from '../types/replay'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const event: BrowserReplayEvent = { phase: 'action', action_index: 0, action_name: 'Check greeting', action_type: 'browser_wait', target: '#result', success: false, duration_ms: 12, started_at: '', completed_at: '', protocol_evidence: { assertion_failure: { expected: 'Welcome', actual: 'Hello' } }, extractions: { greeting: 'Hello' } }
describe('selected browser evidence', () => {
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
})
