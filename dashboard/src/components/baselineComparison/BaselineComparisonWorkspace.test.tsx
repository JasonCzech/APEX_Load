// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TestSummary } from '../../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const api = vi.hoisted(() => ({
  candidates: vi.fn(async () => ({ runs: [], total: 0, suggested_baseline: null })),
  saved: vi.fn(async () => ({ comparisons: [{ id: 'cmp-1', created_at: '2026-10-08T10:00:00Z', verdict: 'not_evaluated', baselines: [{ id: 'run-a', name: 'Baseline (R2026.09)' }] }] })),
  policy: vi.fn(async () => ({})),
  snapshot: vi.fn(),
  evaluate: vi.fn(),
  savePolicy: vi.fn(),
  download: vi.fn(),
}))
vi.mock('./model', async importOriginal => ({ ...(await importOriginal<typeof import('./model')>()), comparisonApi: api }))

import BaselineComparisonWorkspace from './BaselineComparisonWorkspace'

describe('BaselineComparisonWorkspace saved assessments', () => {
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

  it('lists saved comparisons for the run and opens one', async () => {
    const onSnapshot = vi.fn()
    const test = { id: 'run-b', name: 'Candidate', status: 'COMPLETE', created_at: '2026-10-08T09:00:00Z', groups: [] } as unknown as TestSummary
    await act(async () => root.render(<BaselineComparisonWorkspace test={test} onSnapshot={onSnapshot} />))
    for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve() })
    const saved = container.querySelector('[aria-label="Saved assessments"]')
    expect(saved?.textContent).toContain('Not evaluated')
    expect(saved?.textContent).toContain('Baseline (R2026.09)')
    expect(saved?.textContent).not.toContain('not_evaluated')
    await act(async () => saved!.querySelector('button')!.click())
    expect(onSnapshot).toHaveBeenCalledWith('cmp-1')
  })

  it('names each baseline once and tags its findings and setup rows by number', async () => {
    const run = (id: string, release: string, name: string) => ({ id, name, release_id: id, release_name: release, scenario_id: 's', status: 'COMPLETE', vusers: 4, peak_vusers: 4, pinned: true, archive_available: true })
    const a = run('run-a', 'R2026.10 (Peak readiness)', 'Peak Hour — Baseline rerun (R2026.10-rc1)')
    const b = run('run-c', 'R2026.09', 'Peak Hour — Baseline')
    api.snapshot.mockResolvedValue({
      id: 'cmp-1', project_id: 'p', created_at: '2026-10-08T10:00:00Z', rule_version: 'v1', current: run('run-b', 'R2026.11', 'Candidate'), baselines: [a, b],
      request: { baseline_ids: ['run-a', 'run-c'] }, policy: {}, transactions: [], resources: [], warnings: [],
      setup: [{ baseline_id: 'run-c', field: 'Environment', current: 'perf', baseline: 'unverified', status: 'unverified' }],
      findings: [{ baseline_id: 'run-a', row_id: 'tx/1', category: 'latency', metric: 'successful P95', message: 'Checkout: successful P95 +1,302 ms (+323.8%)', status: 'changed' }],
      verdict: { status: 'not_evaluated', evaluated: 0, unavailable: 1, metrics: [], evidence: [] },
    })
    const test = { id: 'run-b', name: 'Candidate', status: 'COMPLETE', created_at: '2026-10-08T09:00:00Z', groups: [] } as unknown as TestSummary
    await act(async () => root.render(<BaselineComparisonWorkspace test={test} comparisonId="cmp-1" onSnapshot={vi.fn()} />))
    for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve() })
    const findings = container.querySelector('[aria-label="Comparison findings"]')!
    const key = findings.querySelector('[aria-label="Baselines compared"]')!
    expect(key.textContent).toContain('Baseline 1 R2026.10 (Peak readiness) · Peak Hour — Baseline rerun (R2026.10-rc1)')
    expect(key.textContent).toContain('Baseline 2 R2026.09 · Peak Hour — Baseline')
    const card = findings.querySelector('.bc-finding')!
    expect(card.textContent).toContain('Baseline 1 · View evidence')
    expect(card.textContent).not.toContain('Peak Hour')
    expect(card.querySelector('.bc-baseline-tag')?.getAttribute('title')).toBe('R2026.10 (Peak readiness) · Peak Hour — Baseline rerun (R2026.10-rc1)')
  })
})
