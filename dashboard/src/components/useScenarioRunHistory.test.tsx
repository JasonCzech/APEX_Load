// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useScenarioRunHistory } from './useScenarioRunHistory'
import type { TestSummary } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function deferred() {
  let resolve!: (value: { tests: TestSummary[] }) => void
  let reject!: (error: Error) => void
  const promise = new Promise<{ tests: TestSummary[] }>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function run(id: string): TestSummary {
  return { id, name: id, status: 'COMPLETE', duration: '1m', created_at: '2026-10-17T00:00:00Z', groups: [], total_vusers: 1 }
}

describe('scenario run history', () => {
  let root: Root
  let history: ReturnType<typeof useScenarioRunHistory>
  function Harness({ projectId }: { projectId: string }) { history = useScenarioRunHistory(projectId); return null }
  beforeEach(async () => {
    root = createRoot(document.createElement('div'))
    await act(async () => root.render(<Harness projectId="a" />))
  })
  afterEach(async () => { await act(async () => root.unmount()) })

  it('deduplicates pending loads and caches an authentic empty result', async () => {
    const pending = deferred()
    const load = vi.fn(() => pending.promise)
    await act(async () => { void history.load('scenario', load); void history.load('scenario', load) })
    expect(history.entries.scenario).toEqual({ status: 'loading' })
    expect(load).toHaveBeenCalledTimes(1)
    await act(async () => pending.resolve({ tests: [] }))
    await act(async () => history.load('scenario', load))
    expect(history.entries.scenario).toEqual({ status: 'success', runs: [] })
    expect(load).toHaveBeenCalledTimes(1)
  })

  it('retains a failure for display and allows an explicit retry', async () => {
    await act(async () => history.load('scenario', async () => { throw new Error('unavailable') }))
    expect(history.entries.scenario).toEqual({ status: 'error', message: 'unavailable' })
    await act(async () => history.load('scenario', async () => ({ tests: [run('recovered')] })))
    expect(history.entries.scenario).toEqual({ status: 'success', runs: [run('recovered')] })
  })

  it.each(['resolve', 'reject'] as const)('discards an earlier %s after a project round trip', async outcome => {
    const old = deferred()
    await act(async () => { void history.load('scenario', () => old.promise) })
    await act(async () => root.render(<Harness projectId="b" />))
    await act(async () => root.render(<Harness projectId="a" />))
    await act(async () => history.load('scenario', async () => ({ tests: [run('new')] })))
    await act(async () => outcome === 'resolve' ? old.resolve({ tests: [run('old')] }) : old.reject(new Error('old failure')))
    expect(history.entries.scenario).toEqual({ status: 'success', runs: [run('new')] })
  })

  it('keeps concurrent scenarios independent and deduplicates explicit refreshes while loading', async () => {
    const old = deferred()
    const other = deferred()
    await act(async () => { void history.load('one', () => old.promise); void history.load('two', () => other.promise) })
    const refresh = vi.fn(async () => ({ tests: [run('latest')] }))
    await act(async () => history.load('one', refresh, true))
    expect(refresh).not.toHaveBeenCalled()
    await act(async () => old.resolve({ tests: [] }))
    await act(async () => history.load('one', refresh, true))
    expect(history.entries.one).toEqual({ status: 'success', runs: [run('latest')] })
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(history.entries.two).toEqual({ status: 'loading' })
    await act(async () => other.resolve({ tests: [] }))
    expect(history.entries.two).toEqual({ status: 'success', runs: [] })
  })

  it('shows a failed explicit refresh as retryable, then replaces the previous successful snapshot', async () => {
    await act(async () => history.load('scenario', async () => ({ tests: [run('old')] })))
    await act(async () => history.load('scenario', async () => { throw new Error('refresh unavailable') }, true))
    expect(history.entries.scenario).toEqual({ status: 'error', message: 'refresh unavailable' })
    await act(async () => history.load('scenario', async () => ({ tests: [run('new')] })))
    expect(history.entries.scenario).toEqual({ status: 'success', runs: [run('new')] })
  })
})
