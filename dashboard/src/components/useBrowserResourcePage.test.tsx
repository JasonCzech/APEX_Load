// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { useBrowserResourcePage, type BrowserResourcePageLoader } from './useBrowserResourcePage'
import { resourcePageFixture } from './browserResourcePage.testFixtures'
import { DEFAULT_BROWSER_RESOURCE_QUERY } from '../types/browserResourcePage'
function deferred() { let resolve!: (value: unknown) => void; let reject!: (error: unknown) => void; const promise = new Promise<unknown>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function Probe({ id = 'run-one', source = 'source-one', query = DEFAULT_BROWSER_RESOURCE_QUERY, loader, refresh = 0 }: { id?: string; source?: string; query?: typeof DEFAULT_BROWSER_RESOURCE_QUERY; loader: BrowserResourcePageLoader; refresh?: number }) {
  const state = useBrowserResourcePage({ testID: id, projectID: 'project-one', sourceIdentity: source, query, loadPage: loader, refreshRevision: refresh })
  return <div>{state.loading ? 'Loading' : state.error || state.page?.test_id || 'Empty'}</div>
}
describe('archive resource request ownership', () => {
  it('retires old A responses after A to B to A and aborts each owner', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const requests: ReturnType<typeof deferred>[] = []; const signals: AbortSignal[] = []
    const loader: BrowserResourcePageLoader = vi.fn((_id, _project, _query, signal) => { const request = deferred(); requests.push(request); signals.push(signal); return request.promise })
    try {
      await act(async () => root.render(<Probe loader={loader} />)); await act(async () => root.render(<Probe id="run-two" loader={loader} />)); await act(async () => root.render(<Probe loader={loader} />))
      expect(signals[0].aborted).toBe(true); expect(signals[1].aborted).toBe(true)
      await act(async () => requests[0].resolve(resourcePageFixture())); expect(node.textContent).toBe('Loading')
      await act(async () => requests[1].reject(new Error('private old failure'))); expect(node.textContent).toBe('Loading')
      await act(async () => requests[2].resolve(resourcePageFixture())); expect(node.textContent).toBe('run-one')
    } finally { await act(async () => root.unmount()) }
    expect(signals[2].aborted).toBe(true)
  })
  it('retires pages when the artifact or filters change and ignores unmounted resolution', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const requests: ReturnType<typeof deferred>[] = []
    const loader: BrowserResourcePageLoader = () => { const request = deferred(); requests.push(request); return request.promise }
    await act(async () => root.render(<Probe loader={loader} />)); await act(async () => requests[0].resolve(resourcePageFixture())); expect(node.textContent).toBe('run-one')
    await act(async () => root.render(<Probe source="source-two" loader={loader} />)); expect(node.textContent).toBe('Loading')
    await act(async () => root.render(<Probe source="source-two" query={{ ...DEFAULT_BROWSER_RESOURCE_QUERY, action_name: 'Click' }} loader={loader} />))
    await act(async () => requests[1].resolve(resourcePageFixture())); expect(node.textContent).toBe('Loading')
    await act(async () => root.unmount()); await act(async () => requests[2].resolve(resourcePageFixture())); expect(node.textContent).toBe('')
  })
  it('survives StrictMode request restart and refuses foreign or malformed pages without disclosing errors', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const requests: ReturnType<typeof deferred>[] = []
    const loader: BrowserResourcePageLoader = () => { const request = deferred(); requests.push(request); return request.promise }
    try {
      await act(async () => root.render(<StrictMode><Probe loader={loader} /></StrictMode>)); expect(requests).toHaveLength(2)
      await act(async () => requests[0].resolve(resourcePageFixture())); expect(node.textContent).toBe('Loading')
      await act(async () => requests[1].resolve({ ...resourcePageFixture(), test_id: 'foreign-private-run' })); expect(node.textContent).toContain('Unable to load'); expect(node.textContent).not.toContain('foreign-private-run')
    } finally { await act(async () => root.unmount()) }
  })
  it('retries the same first-page query and retires its previous promise', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const requests: ReturnType<typeof deferred>[] = []
    const loader: BrowserResourcePageLoader = () => { const request = deferred(); requests.push(request); return request.promise }
    try {
      await act(async () => root.render(<Probe loader={loader} />)); await act(async () => requests[0].reject(new Error('private failure')))
      expect(node.textContent).toContain('Unable to load')
      await act(async () => root.render(<Probe loader={loader} refresh={1} />)); expect(requests).toHaveLength(2); expect(node.textContent).toBe('Loading')
      await act(async () => root.render(<Probe loader={loader} refresh={2} />)); expect(requests).toHaveLength(3)
      await act(async () => requests[1].resolve(resourcePageFixture())); expect(node.textContent).toBe('Loading')
      await act(async () => requests[2].resolve(resourcePageFixture())); expect(node.textContent).toBe('run-one')
    } finally { await act(async () => root.unmount()) }
  })
  it('refuses a valid-shaped page larger than the immutable requested limit without publishing retained rows', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    const loader: BrowserResourcePageLoader = async (_id, _project, providerQuery) => {
      providerQuery.limit = 100
      const page = resourcePageFixture()
      page.items = Array.from({ length: 26 }, (_, index) => ({ ...structuredClone(page.items[0]), locator: { ...page.items[0].locator, resource_ordinal: index + 1 } }))
      return page
    }
    const query = { ...DEFAULT_BROWSER_RESOURCE_QUERY, limit: 25 as const }
    try {
      await act(async () => root.render(<Probe query={query} loader={loader} />))
      expect(node.textContent).toContain('Unable to load')
      expect(node.textContent).not.toContain('run-one')
      expect(query.limit).toBe(25)
    } finally { await act(async () => root.unmount()) }
  })
  it('does not read a missing identity or project and owns its request query', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const loader = vi.fn(async (_id, _project, query) => { query.action_name = 'changed by provider'; return resourcePageFixture() }) as BrowserResourcePageLoader
    const query = { ...DEFAULT_BROWSER_RESOURCE_QUERY, action_name: 'Navigate' }
    try {
      await act(async () => root.render(<Probe id="" loader={loader} />)); expect(loader).not.toHaveBeenCalled(); expect(node.textContent).toBe('Empty')
      await act(async () => root.render(<Probe query={query} loader={loader} />)); expect(node.textContent).toBe('run-one'); expect(query.action_name).toBe('Navigate')
    } finally { await act(async () => root.unmount()) }
  })
})
