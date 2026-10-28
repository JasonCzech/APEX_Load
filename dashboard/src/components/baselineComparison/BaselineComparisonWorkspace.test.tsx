// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TestSummary } from '../../types'

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
})
