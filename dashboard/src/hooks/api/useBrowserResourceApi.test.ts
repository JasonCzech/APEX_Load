// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { readArchiveBrowserResourcePage } from './useBrowserResourceApi'
import { resourcePageFixture } from '../../components/browserResourcePage.testFixtures'
import { DEFAULT_BROWSER_RESOURCE_QUERY } from '../../types/browserResourcePage'

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })
describe('archive resource API transport', () => {
  it('encodes route identities and exact filters, project and cursor through the real authenticated client', async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(resourcePageFixture()), { status: 200, headers: { 'Content-Type': 'application/json' } })); vi.stubGlobal('fetch', fetch)
    const abort = new AbortController()
    expect(await readArchiveBrowserResourcePage('run/one?query#fragment', 'project-one', { ...DEFAULT_BROWSER_RESOURCE_QUERY, action_name: 'Navigate / one', request_id: 'opaque-id', cursor: `payload.${'A'.repeat(43)}` }, abort.signal)).toEqual(resourcePageFixture())
    const [rawURL, options] = fetch.mock.calls[0] as unknown as [string, RequestInit]
    const url = new URL(rawURL, 'http://localhost')
    expect(url.pathname).toBe('/api/v1/tests/run%2Fone%3Fquery%23fragment/archive/browser-resources')
    expect(Object.fromEntries(url.searchParams)).toEqual({ project_id: 'project-one', limit: '50', sort_by: 'capture_order', sort_dir: 'asc', action_name: 'Navigate / one', request_id: 'opaque-id', cursor: `payload.${'A'.repeat(43)}` })
    expect(options.credentials).toBe('include'); expect(options.cache).toBe('no-store'); expect(options.signal).toBeInstanceOf(AbortSignal)
    expect(options).not.toHaveProperty('timeoutMs'); expect(options).not.toHaveProperty('maxResponseBytes')
  })
  it('refuses a response above the operation two MiB bound', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(' '.repeat((2 << 20) + 1), { status: 200 })))
    await expect(readArchiveBrowserResourcePage('run-one', 'project-one', DEFAULT_BROWSER_RESOURCE_QUERY, new AbortController().signal)).rejects.toThrow('approved size limit')
  })
  it('physically aborts transport on caller cancellation and its twelve-second deadline', async () => {
    vi.useFakeTimers(); const signals: AbortSignal[] = []
    vi.stubGlobal('fetch', vi.fn((_url, options: RequestInit) => new Promise<Response>((_resolve, reject) => {
      const signal = options.signal!; signals.push(signal); signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    })))
    const abort = new AbortController(); const pending = readArchiveBrowserResourcePage('run-one', 'project-one', DEFAULT_BROWSER_RESOURCE_QUERY, abort.signal)
    const canceled = expect(pending).rejects.toThrow(); abort.abort(); await canceled; expect(signals[0].aborted).toBe(true)
    const deadline = readArchiveBrowserResourcePage('run-one', 'project-one', DEFAULT_BROWSER_RESOURCE_QUERY, new AbortController().signal)
    const timedOut = expect(deadline).rejects.toThrow('timed out')
    await vi.advanceTimersByTimeAsync(11_999); expect(signals[1].aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(1); await timedOut; expect(signals[1].aborted).toBe(true)
  })
})
