// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import type { BrowserReplayEvent, BrowserReplayReport } from '../types/replay'
import type { DebugRunRecord } from './debuggerExperience'
import { DebugRunDiffPanel } from './DebugRunDiffPanel'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const at = (ms: number) => new Date(Date.UTC(2026, 10, 9, 12) + ms).toISOString()
const ev = (i: number, name: string, start: number, dur: number, ttfb: number, extra: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent => ({
  phase: 'action', action_index: i, action_name: name, action_type: 'request', method: 'GET', target: `https://shop.test/${name}`, status_code: 200, success: true,
  started_at: at(start), completed_at: at(start + dur), duration_ms: dur, protocol_evidence: { timings: { ttfb_ms: ttfb, duration_ms: dur } }, ...extra,
})
const report = (events: BrowserReplayEvent[], start: string): BrowserReplayReport => ({ group: 'g', protocol: 'http', started_at: start, completed_at: start, success: events.every(e => e.success), events })
const record = (id: string, r: BrowserReplayReport): DebugRunRecord => ({ id, report: r, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'local', draft: true, failurePolicy: 'Continue' })

let cleanup: (() => void) | null = null
afterEach(() => { cleanup?.(); cleanup = null })

describe('DebugRunDiffPanel', () => {
  it('compares the latest run with the previous one and explains a row', () => {
    const older = record('r1', report([ev(0, 'Login', 0, 100, 90), ev(1, 'Cart', 100, 50, 40)], '2026-11-09T11:00:00Z'))
    const newer = record('r2', report([ev(0, 'Login', 0, 400, 390), ev(1, 'Cart', 400, 50, 40)], '2026-11-09T12:00:00Z'))
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => root.render(<DebugRunDiffPanel runs={[newer, older]} current={newer.report} />))
    cleanup = () => { act(() => root.unmount()); container.remove() }

    expect(container.querySelector('[role="treegrid"]')?.getAttribute('aria-label')).toBe('Run comparison')
    expect(container.textContent).toContain('Run slower by 300.0 ms')
    expect(container.textContent).toContain('Login: +300.0 ms, all in server')
    const login = [...container.querySelectorAll<HTMLElement>('.wf-row[data-node-id]')].find(r => r.querySelector('.wf-name-label')?.textContent === 'Login')!
    expect(login.querySelector('.wf-ghost')).not.toBeNull()
    expect(login.querySelector('.wf-delta-bar.slower')).not.toBeNull()
    expect(login.querySelector('.wf-delta-text')?.firstChild?.textContent).toBe('+300.0 ms')
    expect(login.querySelector('.wf-delta-text .sr-only')?.textContent).toBe(' vs baseline: +300.0 ms, all in server')
    act(() => login.click())
    const drawer = container.querySelector('[aria-label="Selected row details"]')!
    expect(drawer.textContent).toContain('Phase comparison')
    const waitRow = [...drawer.querySelectorAll('tr')].find(r => r.textContent?.includes('Waiting (TTFB)'))!
    expect(waitRow.textContent).toContain('90.0 ms')
    expect(waitRow.textContent).toContain('390.0 ms')
    expect(waitRow.textContent).toContain('+300.0 ms')

    // Swapping makes the newer run the baseline, so the change flips sign.
    act(() => [...container.querySelectorAll('button')].find(b => b.textContent === 'Swap')!.click())
    expect(container.textContent).toContain('Run faster by 300.0 ms')
    expect([...container.querySelectorAll('option')].some(o => o.textContent?.includes('Largest change'))).toBe(true)
  })

  it('asks for a second run when only one exists', () => {
    const only = record('r1', report([ev(0, 'Login', 0, 100, 90)], '2026-11-09T11:00:00Z'))
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<DebugRunDiffPanel runs={[only]} current={only.report} />))
    cleanup = () => act(() => root.unmount())
    expect(container.textContent).toContain('Run the script again to compare')
  })
})
