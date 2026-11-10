// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { anatomyOf, requestRow, transaction } from './__fixtures__/anatomyDiff'
import { AnatomyDiffSection } from './AnatomyDiffSection'
import { defaultPolicy, type ComparisonRow, type ComparisonSnapshot, type RunIdentity } from './model'
import type { AnatomyLoad } from './useAnatomyReports'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const run = (id: string, name: string) => ({ id, name, release_id: id, release_name: 'R1', scenario_id: 's', status: 'COMPLETE', vusers: 1, peak_vusers: 1, pinned: true, archive_available: true }) as RunIdentity
const row: ComparisonRow = { id: 'tx/0', name: 'Login', source: 'transaction', group: 'g', unit: 'ms', baselines: {} }
const snapshot = (baselines: RunIdentity[], population: ComparisonSnapshot['request']['population'] = 'transaction'): ComparisonSnapshot => ({
  id: 'c', project_id: 'p', current: run('cur', 'Current'), baselines, created_at: '', rule_version: 'v1',
  request: { baseline_ids: baselines.map(b => b.id), population, allow_other_scenarios: false }, policy: defaultPolicy,
  transactions: [row], resources: [], setup: [], findings: [], warnings: [],
})

const login = (server: number, tls: number, conns: { newConns: number; reused: number }) => requestRow(1, 'POST /login', { tls, server }, { offset: 0, ...conns })
const ready = (server: number, tls: number, conns: { newConns: number; reused: number }): AnatomyLoad => ({ status: 'ready', anatomy: anatomyOf([transaction('Login', [login(server, tls, conns)], { server, tls })]) })

describe('AnatomyDiffSection', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.unstubAllGlobals()
  })

  const loads: Record<string, AnatomyLoad> = {
    cur: ready(253, 37, { newConns: 60, reused: 40 }),
    a: ready(100, 10, { newConns: 8, reused: 92 }),
    b: ready(250, 37, { newConns: 0, reused: 0 }),
  }

  it('shows the headline, the diff waterfall and the per-phase table, and switches baseline', async () => {
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snapshot([run('a', 'Base A'), run('b', 'Base B')])} anatomy={loads} />))
    const section = container.querySelector('section[aria-labelledby]')!
    expect(section.querySelector('h3')?.textContent).toBe('Time anatomy diff')
    expect(section.textContent).toContain('Mean +180.0 ms: 85% server, 15% TLS')
    expect(section.textContent).toContain('Connection reuse 92% → 40%')
    // Labelled as whole-run, failures included: it is not the comparison's stable-window successful P95.
    expect(section.textContent).toContain('Whole-run P95 (all instances, failures included) +360.0 ms')
    expect(section.textContent).not.toContain('Transaction P95')
    expect(section.textContent).toContain('not split by phase')
    expect(section.querySelector('[role="grid"], [role="treegrid"]')).toBeTruthy()
    expect(section.textContent).toContain('POST /login')
    const table = section.querySelector('table[aria-label="Mean time by phase"]')!
    expect(table.textContent).toContain('Server wait')
    expect(table.textContent).toContain('85%')
    expect(table.querySelector('caption')?.textContent).toContain('baseline 100 of 100 instances (100%), this run 100 of 100 instances (100%)')
    expect(section.textContent).toContain('Whole run')

    const picker = section.querySelector<HTMLSelectElement>('.bc-anatomy > label select')!
    await act(async () => {
      picker.value = 'b'
      picker.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(section.textContent).toContain('Mean +3.0 ms: 100% server')
    expect(section.textContent).not.toContain('Connection reuse')
  })

  it('qualifies the mean and withholds shares when a run matched too few instances', async () => {
    const lowCur: AnatomyLoad = { status: 'ready', anatomy: anatomyOf([{ ...transaction('Login', [login(60, 10, { newConns: 0, reused: 0 })], { server: 60, tls: 10, instances: 25 }), partial_instances: 75 }]) }
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snapshot([run('a', 'Base A')])} anatomy={{ ...loads, cur: lowCur }} />))
    const section = container.querySelector('section[aria-labelledby]')!
    expect(section.querySelector('.bc-anatomy-headline strong')?.textContent).toBe('Mean −40.0 ms (low match coverage): server −40.0 ms')
    const table = section.querySelector('table[aria-label="Mean time by phase"]')!
    expect(table.querySelector('caption')?.textContent).toContain('this run 25 of 100 instances (25%). Low match coverage: shares withheld')
    expect([...table.querySelectorAll('tbody tr td:last-child')].every(td => td.textContent === '—')).toBe(true)
    expect(section.textContent).toContain('Low match coverage: the means cover')
  })

  it('switches the statistic and states that phase percentiles do not add up', async () => {
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snapshot([run('a', 'Base A')])} anatomy={loads} />))
    expect(container.querySelector('.bc-anatomy > label select')).toBeNull()
    expect(container.textContent).toContain('Against R1 · Base A')
    expect(container.textContent).not.toContain('do not add up')
    const p95 = [...container.querySelectorAll('[role="radio"]')].find(b => b.textContent === 'P95') as HTMLButtonElement
    await act(async () => p95.click())
    expect(p95.getAttribute('aria-checked')).toBe('true')
    expect(container.textContent).toContain('Phase P95s do not add up')
    expect(container.querySelector('[aria-label*="P95 of each phase"]')).toBeTruthy()
  })

  it('keeps the waterfall view (sort) when only the statistic changes', async () => {
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snapshot([run('a', 'Base A')])} anatomy={loads} />))
    const sort = () => [...container.querySelectorAll<HTMLSelectElement>('.bc-anatomy select')].find(select => [...select.options].some(option => option.value === 'chronological'))!
    expect(sort()).toBeTruthy()
    const next = [...sort().options].map(option => option.value).find(value => value !== sort().value)!
    await act(async () => {
      sort().value = next
      sort().dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(sort().value).toBe(next)
    const p95 = [...container.querySelectorAll('[role="radio"]')].find(b => b.textContent === 'P95') as HTMLButtonElement
    await act(async () => p95.click())
    expect(sort().value).toBe(next)
  })

  it('renders loading, error and unavailable states', async () => {
    const snap = snapshot([run('a', 'Base A')])
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snap} anatomy={{ cur: { status: 'loading' } }} />))
    expect(container.querySelector('[role="status"]')?.textContent).toContain('Loading')
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snap} anatomy={{ cur: loads.cur, a: { status: 'error', message: 'Forbidden' } }} />))
    expect(container.querySelector('[role="alert"] p')?.textContent).toBe('Baseline: Forbidden')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Close and reopen the details to try again.')
    const onRetry = vi.fn()
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snap} anatomy={{ cur: loads.cur, a: { status: 'error', message: 'Forbidden' } }} onRetry={onRetry} />))
    const retry = container.querySelector<HTMLButtonElement>('[role="alert"] button')
    expect(retry?.textContent).toBe('Retry')
    await act(async () => retry?.click())
    expect(onRetry).toHaveBeenCalledTimes(1)
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snap} anatomy={{ cur: loads.cur, a: { status: 'ready' } }} />))
    expect(container.textContent).toContain('baseline’s report has no time anatomy')
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snap} anatomy={{ cur: loads.cur, a: { status: 'ready', anatomy: anatomyOf([], 2) } }} />))
    expect(container.textContent).toContain('left 2 out')
    await act(async () => root.render(<AnatomyDiffSection row={row} snapshot={snapshot([run('a', 'Base A')], 'action')} anatomy={loads} />))
    expect(container.textContent).toContain('named transactions only')
  })
})
