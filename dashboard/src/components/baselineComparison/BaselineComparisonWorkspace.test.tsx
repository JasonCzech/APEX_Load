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
})
