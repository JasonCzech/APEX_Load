// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RunArchiveReport, TestSummary } from '../../types'
import { anatomyOf, requestRow, transaction } from './__fixtures__/anatomyDiff'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { freeze, request } = vi.hoisted(() => ({ freeze: vi.fn(), request: vi.fn() }))
vi.mock('../browserPanelInputs', () => ({ freezeBrowserPanelDefinition: freeze }))
vi.mock('../../hooks/apiClient', async importOriginal => ({ ...(await importOriginal<typeof import('../../hooks/apiClient')>()), request }))
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

  it('saves a frozen query_version 5 comparison panel with selection receipt and policy pins', async () => {
    const run = (id: string, release: string, name: string) => ({ id, name, release_id: id, release_name: release, scenario_id: 's', status: 'COMPLETE', vusers: 4, peak_vusers: 4, pinned: true, archive_available: true })
    api.snapshot.mockResolvedValue({
      id: 'cmp-1', project_id: 'p', created_at: '2026-10-08T10:00:00Z', rule_version: 'v1', current: run('run-b', 'R2026.11', 'Candidate'), baselines: [run('run-a', 'R2026.10', 'Baseline')],
      request: { baseline_ids: ['run-a'], windows: { 'run-b': { start_seconds: 10, end_seconds: 70 } } }, policy: {}, transactions: [], resources: [], warnings: [],
      browser_documents: [{ baseline_id: 'run-a', metric: 'fcp', unit: 'ms', frame_scope: 'main_frame', lifecycle_phase: 'navigation', engine: 'chromium', device_profile: 'desktop', comparable: true, current_final: 10, baseline_final: 10 }],
      setup: [], findings: [], verdict: { status: 'passed', evaluated: 1, unavailable: 0, metrics: [], evidence: [] },
    })
    const pinnedInputs = {
      version: 2,
      query_sha256: 'a'.repeat(64),
      selection_receipt_sha256: 'b'.repeat(64),
      policy_pins: [{ kind: 'browser_policy', id: 'pol-1', revision: 1, sha256: 'c'.repeat(64) }],
      current: {
        run_id: 'run-b',
        manifest_sha256: 'd'.repeat(64),
        tuple_ids: ['e'.repeat(64)],
        policy_pins: [{ kind: 'browser_policy', id: 'pol-1', revision: 1, sha256: 'c'.repeat(64) }],
      },
      baseline: { run_id: 'run-a', manifest_sha256: 'f'.repeat(64), tuple_ids: ['e'.repeat(64)] },
    }
    freeze.mockImplementation(async (_runId, draft) => {
      draft.tabs[0].panels[0].query.browser_inputs = pinnedInputs
      return draft
    })
    request.mockResolvedValue({
      id: 'panel-1',
      project_id: 'p',
      name: 'Candidate browser comparison',
      revision: 1,
      tabs: [{ id: 'main', title: 'Main', panels: [{ id: 'browser-comparison', title: 'Candidate browser comparison', kind: 'line', x: 0, y: 0, w: 24, h: 8, query: { query_version: 5, browser_inputs: pinnedInputs } }] }],
    })
    const test = { id: 'run-b', name: 'Candidate', status: 'COMPLETE', created_at: '2026-10-08T09:00:00Z', groups: [] } as unknown as TestSummary
    await act(async () => root.render(<BaselineComparisonWorkspace test={test} comparisonId="cmp-1" onSnapshot={vi.fn()} />))
    for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve() })
    const saveButton = [...container.querySelectorAll('button')].find(btn => btn.textContent === 'Save to panel')!
    expect(saveButton).toBeTruthy()
    await act(async () => saveButton.click())
    for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve() })
    expect(freeze).toHaveBeenCalledWith('run-b', expect.objectContaining({
      tabs: [expect.objectContaining({
        panels: [expect.objectContaining({
          query: expect.objectContaining({
            query_version: 5,
            metric: 'document_fcp',
            comparison_run_id: 'run-a',
            start_seconds: 10,
            end_seconds: 70,
          }),
        })],
      })],
    }))
    expect(request).toHaveBeenCalledWith('/monitoring/dashboard-definitions', expect.objectContaining({ method: 'POST' }))
    expect(container.textContent).toContain('Saved panel: panel-1 (revision 1)')
    expect(container.textContent).toContain(`selection receipt ${'b'.repeat(64)}`)
  })

  it('loads the baseline time anatomy once when a transaction opens, reusing the page report for the current run', async () => {
    const run = (id: string, name: string) => ({ id, name, release_id: id, release_name: 'R1', scenario_id: 's', status: 'COMPLETE', vusers: 4, peak_vusers: 4, pinned: true, archive_available: true })
    const tx = { id: 'tx/0', name: 'Login', group: 'g', source: 'transaction', unit: 'ms', current: { samples: 10, p95: 20 }, baselines: { 'run-a': { measurement: { samples: 10, p95: 10 }, qualification: [], approximate: false } } }
    api.snapshot.mockResolvedValue({
      id: 'cmp-1', project_id: 'p', created_at: '2026-10-08T10:00:00Z', rule_version: 'v1', current: run('run-b', 'Candidate'), baselines: [run('run-a', 'Baseline')],
      request: { baseline_ids: ['run-a'], population: 'transaction' }, policy: {}, transactions: [tx], resources: [], warnings: [], setup: [], findings: [],
      verdict: { status: 'not_evaluated', evaluated: 0, unavailable: 0, metrics: [], evidence: [] },
    })
    const anatomy = (server: number) => anatomyOf([transaction('Login', [requestRow(1, 'POST /login', { server })], { server })])
    request.mockReset()
    request.mockImplementation(async (path: string) => {
      if (path.startsWith('/tests/run-a/archive/report')) return { manifest: { test_id: 'run-a' }, time_anatomy: anatomy(100) }
      throw new Error(`unexpected ${path}`)
    })
    const test = { id: 'run-b', name: 'Candidate', status: 'COMPLETE', created_at: '2026-10-08T09:00:00Z', groups: [] } as unknown as TestSummary
    const seeds = [{ manifest: { test_id: 'run-b' }, time_anatomy: anatomy(150) } as unknown as RunArchiveReport]
    await act(async () => root.render(<BaselineComparisonWorkspace test={test} comparisonId="cmp-1" reportSeeds={seeds} onSnapshot={vi.fn()} />))
    for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve() })
    expect(request).not.toHaveBeenCalled()
    const open = async () => {
      await act(async () => (container.querySelector('.bc-matrix tbody th[scope="row"] button') as HTMLButtonElement).click())
      for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve() })
    }
    await open()
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('Time anatomy diff')
    expect(dialog.textContent).toContain('Mean +50.0 ms: 100% server')
    expect(request).toHaveBeenCalledTimes(1)
    expect(request.mock.calls[0][0]).toBe('/tests/run-a/archive/report?include_release_trend=false')
    await act(async () => ([...dialog.querySelectorAll('button')].find(b => b.textContent === 'Close details') as HTMLButtonElement).click())
    await open()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Mean +50.0 ms')
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('retries a failed baseline time anatomy from the details without closing them', async () => {
    const run = (id: string, name: string) => ({ id, name, release_id: id, release_name: 'R1', scenario_id: 's', status: 'COMPLETE', vusers: 4, peak_vusers: 4, pinned: true, archive_available: true })
    const tx = { id: 'tx/0', name: 'Login', group: 'g', source: 'transaction', unit: 'ms', current: { samples: 10, p95: 20 }, baselines: { 'run-a': { measurement: { samples: 10, p95: 10 }, qualification: [], approximate: false } } }
    api.snapshot.mockResolvedValue({
      id: 'cmp-1', project_id: 'p', created_at: '2026-10-08T10:00:00Z', rule_version: 'v1', current: run('run-b', 'Candidate'), baselines: [run('run-a', 'Baseline')],
      request: { baseline_ids: ['run-a'], population: 'transaction' }, policy: {}, transactions: [tx], resources: [], warnings: [], setup: [], findings: [],
      verdict: { status: 'not_evaluated', evaluated: 0, unavailable: 0, metrics: [], evidence: [] },
    })
    const anatomy = (server: number) => anatomyOf([transaction('Login', [requestRow(1, 'POST /login', { server })], { server })])
    request.mockReset()
    let calls = 0
    request.mockImplementation(async (path: string) => {
      if (!path.startsWith('/tests/run-a/archive/report')) throw new Error(`unexpected ${path}`)
      calls++
      if (calls === 1) throw new Error('Service unavailable')
      return { manifest: { test_id: 'run-a' }, time_anatomy: anatomy(100) }
    })
    const test = { id: 'run-b', name: 'Candidate', status: 'COMPLETE', created_at: '2026-10-08T09:00:00Z', groups: [] } as unknown as TestSummary
    const seeds = [{ manifest: { test_id: 'run-b' }, time_anatomy: anatomy(150) } as unknown as RunArchiveReport]
    await act(async () => root.render(<BaselineComparisonWorkspace test={test} comparisonId="cmp-1" reportSeeds={seeds} onSnapshot={vi.fn()} />))
    for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve() })
    await act(async () => (container.querySelector('.bc-matrix tbody th[scope="row"] button') as HTMLButtonElement).click())
    for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve() })
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.querySelector('.bc-anatomy [role="alert"]')?.textContent).toContain('Baseline: Service unavailable')
    await act(async () => (dialog.querySelector('.bc-anatomy [role="alert"] button') as HTMLButtonElement).click())
    for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve() })
    expect(request).toHaveBeenCalledTimes(2)
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Mean +50.0 ms')
  })
})
