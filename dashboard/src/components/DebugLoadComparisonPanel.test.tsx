// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
import { afterEach, describe, expect, it, vi } from 'vitest'
import DebugCompletionSummary from './DebugCompletionSummary'
import type { DebugLoadComparisonSource } from './DebugLoadComparisonPanel'
import type { ScriptLoadRunsResponse } from '../types/scriptLoadRuns'
import { amplificationReport, amplificationSnapshot } from './__fixtures__/debugLoadAmplification'

const runs: ScriptLoadRunsResponse = {
  count: 3, limit: 10, runs: [
    { test_id: 'live', name: 'Live run', status: 'COMPLETE', started_at: '2026-11-02T10:00:00Z', archive_available: false, groups: ['Shop'] },
    { test_id: 'peak', name: 'Peak', status: 'COMPLETE', started_at: '2026-11-01T10:00:00Z', archive_available: true, groups: ['Shop', 'Shop B'], vusers: 300 },
    { test_id: 'soak', name: 'Soak', status: 'ABORTED', started_at: '2026-10-30T10:00:00Z', archive_available: true, groups: ['Shop'] },
  ],
}

let root: Root | null = null
let container: HTMLDivElement | null = null
afterEach(() => {
  if (root) act(() => root!.unmount())
  container?.remove()
  root = null
  container = null
})

async function flush() {
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
}

function render(source: DebugLoadComparisonSource | undefined, draft = false) {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  const snapshot = { ...amplificationSnapshot(), draft }
  act(() => root!.render(<StrictMode><DebugCompletionSummary snapshot={snapshot} stale={false} unavailable={false} onSelectEvent={vi.fn()} onDownloadHAR={vi.fn()} onExportSession={vi.fn()} loadComparison={source} /></StrictMode>))
  return container
}

const tab = (node: HTMLElement) => node.querySelector<HTMLButtonElement>('#debug-summary-tab-load-comparison')
const panel = (node: HTMLElement) => node.querySelector<HTMLElement>('#debug-summary-panel-load-comparison')!

function choose(select: HTMLSelectElement, value: string) {
  act(() => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

describe('Debug vs load sub-tab', () => {
  it('is absent without a load source', () => {
    const node = render(undefined)
    expect(tab(node)).toBeNull()
  })

  it('lists load runs only once opened, then loads the newest archived report and renders factors', async () => {
    const listLoadRuns = vi.fn().mockResolvedValue(runs)
    const getLoadReport = vi.fn().mockResolvedValue(amplificationReport())
    const node = render({ listLoadRuns, getLoadReport })
    expect(listLoadRuns).not.toHaveBeenCalled()
    act(() => tab(node)!.click())
    expect(tab(node)!.getAttribute('aria-selected')).toBe('true')
    expect(panel(node).hidden).toBe(false)
    await flush()
    await flush()
    expect(listLoadRuns).toHaveBeenCalledTimes(1)
    const picker = panel(node).querySelector<HTMLSelectElement>('select')!
    expect(picker.labels?.[0]?.textContent).toBe('Load run')
    expect([...picker.options].map(option => [option.value, option.disabled])).toEqual([['live', true], ['peak', false], ['soak', false]])
    expect(picker.options[0].textContent).toContain('no archive')
    expect(picker.value).toBe('peak')
    expect(getLoadReport).toHaveBeenCalledWith('peak')
    expect(panel(node).textContent).toContain('Groups using this script: Shop, Shop B · 300 VUsers')
    expect(panel(node).querySelector('.debug-load-headline')?.textContent).toBe('Server ×1.5, network ×0.6 under load (mean load vs debug, summed over 3 requests matched by transaction). Phase percentiles do not add up, so these totals use means; the table shows P95 per phase. 1 request compared only with run-wide means is not in these totals.')
    const rows = [...panel(node).querySelectorAll('tbody tr')]
    expect(rows).toHaveLength(6)
    expect(rows[0].querySelector('th')?.textContent).toBe('Login')
    expect(rows[0].textContent).toContain('Checkout flow · P95')
    expect(rows[0].textContent).toContain('×6.2')
    expect(rows[3].textContent).toContain('Mean only (outside a transaction)')
    expect(rows[4].textContent).toContain('Redirect chain')
    expect(panel(node).textContent).toContain('network path differs')
    expect(panel(node).textContent).toContain('revision cannot be verified')
    // The intro says which columns use the statistic and which use means.
    expect(panel(node).querySelector('.debug-load-intro')?.textContent).toContain('the Server column is the server wait at that statistic too')
    expect(panel(node).querySelector('.debug-load-intro')?.textContent).not.toMatch(/Server and network .* compare means/)
    // 'new' and 'n/a' are defined in visible text, not only in a title.
    const legend = panel(node).querySelector('.debug-load-legend')?.textContent ?? ''
    expect(legend).toContain('new: About zero in debug; load spent time here.')
    expect(legend).toContain('n/a: Not measured on one side, or too small to compare.')
    const naFactor = panel(node).querySelector('.debug-load-factor-na')
    expect(naFactor?.querySelector('.sr-only')?.textContent).toContain('Not measured on one side')
    // The mean-only row's network factor leaves download out and says so.
    expect(rows[3].querySelector('td:last-child')?.textContent).toMatch(/setup only/)

    const stat = panel(node).querySelectorAll<HTMLSelectElement>('select')[1]
    choose(stat, 'avg')
    expect(panel(node).querySelector('.debug-load-headline')?.textContent).not.toContain('Phase percentiles')
    expect(panel(node).textContent).toContain('including groups running other scripts')

    choose(picker, 'soak')
    await flush()
    expect(getLoadReport).toHaveBeenLastCalledWith('soak')
    choose(picker, 'peak')
    await flush()
    // A report already read is reused.
    expect(getLoadReport).toHaveBeenCalledTimes(2)
  })

  it('explains an empty list, a failed list, a report without phases and a draft', async () => {
    let node = render({ listLoadRuns: vi.fn().mockResolvedValue({ runs: [], count: 0, limit: 10 }), getLoadReport: vi.fn() }, true)
    act(() => tab(node)!.click())
    await flush()
    expect(panel(node).textContent).toContain('No finished load runs of this script in this project yet.')
    expect(panel(node).textContent).toContain('This debug run used an unsaved draft.')
    act(() => root!.unmount()); root = null; container?.remove()

    node = render({ listLoadRuns: vi.fn().mockRejectedValue(new Error('forbidden')), getLoadReport: vi.fn() })
    act(() => tab(node)!.click())
    await flush()
    expect(panel(node).querySelector('[role="alert"] p')?.textContent).toBe('forbidden')
    act(() => root!.unmount()); root = null; container?.remove()

    node = render({ listLoadRuns: vi.fn().mockResolvedValue(runs), getLoadReport: vi.fn().mockResolvedValue({}) })
    act(() => tab(node)!.click())
    await flush()
    await flush()
    expect(panel(node).textContent).toContain("This load run's report has no per-request phase data to compare with.")
  })

  it('retries a failed list and a failed report', async () => {
    const listLoadRuns = vi.fn().mockRejectedValueOnce(new Error('503')).mockResolvedValue(runs)
    const getLoadReport = vi.fn().mockRejectedValueOnce(new Error('report 503')).mockResolvedValue(amplificationReport())
    const node = render({ listLoadRuns, getLoadReport })
    act(() => tab(node)!.click())
    await flush()
    const retry = () => panel(node).querySelector<HTMLButtonElement>('[role="alert"] button')!
    expect(panel(node).querySelector('[role="alert"] p')?.textContent).toBe('503')
    act(() => retry().click())
    await flush()
    await flush()
    expect(listLoadRuns).toHaveBeenCalledTimes(2)
    expect(panel(node).querySelector('[role="alert"] p')?.textContent).toBe('report 503')
    act(() => retry().click())
    await flush()
    expect(getLoadReport).toHaveBeenCalledTimes(2)
    expect(panel(node).querySelector('[role="alert"]')).toBeNull()
    expect(panel(node).querySelector('.debug-load-headline')).not.toBeNull()
  })

  it('prefers a load run over a newer shakeout, and labels a chosen shakeout as unloaded', async () => {
    const withShakeout: ScriptLoadRunsResponse = { ...runs, runs: [{ test_id: 'shake', name: 'Shakeout', status: 'COMPLETE', archive_available: true, script_shakeout: true, groups: ['Shop'] }, ...runs.runs] }
    const getLoadReport = vi.fn().mockResolvedValue(amplificationReport())
    const node = render({ listLoadRuns: vi.fn().mockResolvedValue(withShakeout), getLoadReport })
    act(() => tab(node)!.click())
    await flush()
    await flush()
    const picker = panel(node).querySelector<HTMLSelectElement>('select')!
    expect(picker.value).toBe('peak')
    choose(picker, 'shake')
    await flush()
    expect(panel(node).querySelector('.debug-load-headline')?.textContent).toContain('in the shakeout run (unloaded)')
    expect(panel(node).textContent).toContain('The selected run is a script shakeout')
  })
})
