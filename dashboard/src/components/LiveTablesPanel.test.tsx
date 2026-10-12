// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import LiveTablesPanel, { type LiveTableSummary } from './LiveTablesPanel'
const { request, apiFetch, downloadBlob } = vi.hoisted(() => ({ request: vi.fn(), apiFetch: vi.fn(), downloadBlob: vi.fn() }))
const socket = vi.hoisted(() => ({ state: 'connected', options: {} as { onOpen?: () => void; onMessage?: (message: { type: string; test_id?: string; event_index?: number }) => void } }))
vi.mock('../hooks/useWebSocket', () => ({ useWebSocket: (options: typeof socket.options) => { socket.options = options; return { connectionState: socket.state } } }))
vi.mock('../hooks/apiClient', () => ({ request, apiFetch }))
vi.mock('../utils/fileUtils', () => ({ downloadBlob }))
let container: HTMLDivElement; let root: Root
const table: LiveTableSummary = { id: 't', alias: 'customers', profile: 'native_queue', durability: 'memory', state: 'running', revision: 1, counts: { available: 2, leased: 1 } }
beforeEach(() => { vi.useFakeTimers(); socket.state = 'connected'; request.mockReset(); apiFetch.mockReset(); downloadBlob.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.useRealTimers() })
async function click(text: string) { await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === text)!.click()) }
async function render(testId = 'run') { await act(async () => root.render(<LiveTablesPanel testId={testId} />)) }
it('refreshes the scoped authoritative view on notifications, sequence gaps and reconnect', async () => {
  request.mockResolvedValue({ tables: [table], updated_at: '2026-10-12T12:00:00Z' }); await render()
  expect(container.querySelector('time')?.dateTime).toBe('2026-10-12T12:00:00Z')
  await act(async () => socket.options.onMessage?.({ type: 'live_tables_changed', test_id: 'other', event_index: 1 }))
  expect(request).toHaveBeenCalledTimes(1)
  await act(async () => socket.options.onMessage?.({ type: 'live_tables_changed', test_id: 'run', event_index: 1 }))
  await act(async () => socket.options.onMessage?.({ type: 'live_tables_changed', test_id: 'run', event_index: 4 }))
  expect(request).toHaveBeenCalledTimes(3)
  await act(async () => socket.options.onMessage?.({ type: 'live_tables_changed', test_id: 'run', event_index: 4 }))
  expect(request).toHaveBeenCalledTimes(3)
  await act(async () => socket.options.onOpen?.())
  expect(request).toHaveBeenCalledTimes(4)
  expect(container.querySelector('table')).toBeNull()
})
it('finishes slow reads during continuous notifications and coalesces their follow-up', async () => {
  const responses: ((value: unknown) => void)[] = []
  request.mockImplementation(() => new Promise(resolve => { responses.push(resolve) }))
  await render()
  for (let index = 1; index <= 5; index++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000)
      socket.options.onMessage?.({ type: 'live_tables_changed', test_id: 'run', event_index: index })
    })
  }
  expect(request).toHaveBeenCalledTimes(1)
  expect(request.mock.calls[0][1].signal.aborted).toBe(false)
  await act(async () => responses[0]({ tables: [table], updated_at: '2026-10-12T12:00:00Z' }))
  expect(container.textContent).toContain('Revision 1')
  expect(request).toHaveBeenCalledTimes(2)
  await act(async () => socket.options.onMessage?.({ type: 'live_tables_changed', test_id: 'run', event_index: 6 }))
  expect(request.mock.calls[1][1].signal.aborted).toBe(false)
  await act(async () => responses[1]({ tables: [{ ...table, revision: 2 }] }))
  expect(container.textContent).toContain('Revision 2')
  expect(request).toHaveBeenCalledTimes(3)
  await act(async () => responses[2]({ tables: [{ ...table, revision: 3 }] }))
  expect(container.textContent).toContain('Revision 3')
  expect(request).toHaveBeenCalledTimes(3)
})
it('clears previews across socket interruptions and distinguishes recovering and closed states', async () => {
  request.mockResolvedValue({ tables: [table] }); await render()
  request.mockResolvedValueOnce({ revision: 1, columns: ['a'], rows: [{ version: 1, values: { a: 'private preview' } }] })
  await click('Preview up to 100 rows')
  expect(container.textContent).toContain('private preview')
  socket.state = 'disconnected'; await render()
  expect(container.textContent).not.toContain('private preview')
  expect(container.textContent).toContain('Reconnecting live updates')
  request.mockResolvedValue({ tables: [{ ...table, state: 'recovering' }] }); await click('Refresh tables')
  expect(container.textContent).toContain('Recovering table state')
  expect([...container.querySelectorAll('button')].some(button => button.textContent === 'Preview up to 100 rows')).toBe(false)
  request.mockResolvedValue({ tables: [{ ...table, state: 'closed' }] }); await click('Refresh tables')
  expect(container.textContent).toContain('Closed')
})
it('polls counts without disclosing rows, explicitly previews, distinguishes absent and empty, and clears values on a new revision', async () => {
  request.mockResolvedValue({ tables: [table] }); await render()
  expect(request).toHaveBeenCalledTimes(1); expect(container.querySelector('table')).toBeNull()
  request.mockResolvedValueOnce({ revision: 1, columns: ['a', 'b', 'c'], rows: [{ id: 'r', version: 1, values: { a: 'private test value', b: '' } }], count: 1 })
  await click('Preview up to 100 rows')
  expect(request).toHaveBeenLastCalledWith('/tests/run/live-tables/t/rows?offset=0&limit=100&revision=1', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  expect(container.textContent).toContain('private test value'); expect(container.querySelector('[aria-label="Empty value"]')).not.toBeNull(); expect(container.querySelector('[aria-label="Absent cell"]')).not.toBeNull()
  request.mockResolvedValue({ tables: [{ ...table, revision: 2 }] }); await act(async () => vi.advanceTimersByTimeAsync(2000))
  expect(container.textContent).not.toContain('private test value'); expect(container.textContent).toContain('Revision 2')
})
it('clears disclosed values when offline and refreshes after reconnect', async () => {
  request.mockResolvedValue({ tables: [table] }); await render()
  request.mockResolvedValueOnce({ revision: 1, columns: ['a'], rows: [{ version: 1, values: { a: 'disclosed' } }] }); await click('Preview up to 100 rows')
  await act(async () => window.dispatchEvent(new Event('offline')))
  expect(container.textContent).not.toContain('disclosed'); expect(container.textContent).toContain('Offline.')
  const before = request.mock.calls.length; await act(async () => window.dispatchEvent(new Event('online')))
  expect(request.mock.calls.length).toBe(before + 1); expect(container.textContent).toContain('customers')
})
it('ignores a late response after switching runs and clears data on access loss', async () => {
  let resolve!: (value: unknown) => void
  request.mockImplementationOnce(() => new Promise(done => { resolve = done })); await render('old')
  request.mockResolvedValue({ tables: [{ ...table, alias: 'new run' }] }); await render('new')
  await act(async () => resolve({ tables: [table] }))
  expect(container.textContent).toContain('new run'); expect(container.textContent).not.toContain('customers')
  request.mockRejectedValue(new Error('forbidden private server text')); await act(async () => vi.advanceTimersByTimeAsync(2000))
  expect(container.textContent).not.toContain('new run'); expect(container.textContent).not.toContain('private server text'); expect(container.textContent).toContain('status is unavailable')
})
it('rejects a mismatched row revision and exports only on explicit request', async () => {
  request.mockResolvedValue({ tables: [table] }); await render(); expect(apiFetch).not.toHaveBeenCalled()
  request.mockResolvedValueOnce({ revision: 2, columns: ['a'], rows: [{ values: { a: 'wrong revision' } }] }); await click('Preview up to 100 rows')
  expect(container.textContent).not.toContain('wrong revision'); expect(container.textContent).toContain('table changed')
  apiFetch.mockResolvedValue({ ok: true, blob: async () => new Blob(['{}']) }); downloadBlob.mockResolvedValue('browser_started'); await click('Export table')
  expect(apiFetch).toHaveBeenCalledWith('/tests/run/live-tables/t/export?revision=1', expect.anything()); expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob), 'live-table-customers-revision-1.json')
})
it('shows unavailable counts as unknown and removes the previous preview even if its revision is unchanged', async () => {
  request.mockResolvedValue({ tables: [table] }); await render()
  request.mockResolvedValueOnce({ revision: 1, columns: ['a'], rows: [{ version: 1, values: { a: 'disclosed' } }] }); await click('Preview up to 100 rows')
  request.mockResolvedValue({ tables: [{ ...table, state: 'unavailable', counts: undefined }] }); await act(async () => vi.advanceTimersByTimeAsync(2000))
  expect(container.textContent).not.toContain('disclosed'); expect(container.querySelector('dd')?.textContent).toBe('Unknown')
})
it.each(['conflict', 'pending'])('gates closed %s recovery on effective data-pool access and reloads after saving', async state => {
  const conflict = { ...table, state: 'closed', finalization: { state, mode: 'write_back' } }
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.includes('/auth/effective-access')) return { resources: [{ operations: [{ id: 'manage_data_pools', allowed: true }] }] }
    if (options?.method === 'POST') return { state: 'saved' }
    return { tables: [conflict] }
  })
  await act(async () => root.render(<LiveTablesPanel testId="run" projectId="project" />))
  const input = container.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Recovered output'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await click('Save as a new data pool')
  expect(request).toHaveBeenCalledWith('/tests/run/live-tables/t/finalize', expect.objectContaining({ method: 'POST', body: JSON.stringify({ output_pool_name: 'Recovered output' }) }))
  expect(request.mock.calls.filter(([path]) => path === '/tests/run/live-tables').length).toBe(2)
})

it.each(['running', 'closed'])('does not offer pending output recovery while %s without verified permission', async state => {
  request.mockImplementation(async (path: string) => {
    if (path.includes('/auth/effective-access')) return { resources: [{ operations: [{ id: 'manage_data_pools', allowed: false }] }] }
    return { tables: [{ ...table, state, finalization: { state: 'pending', mode: 'save_as_new' } }] }
  })
  await act(async () => root.render(<LiveTablesPanel testId="run" projectId="project" />))
  expect(container.querySelector('input')).toBeNull()
  expect([...container.querySelectorAll('button')].some(button => button.textContent === 'Save as a new data pool')).toBe(false)
  expect(request.mock.calls.some(([path]) => path.includes('/auth/effective-access'))).toBe(state === 'closed')
})

it('explains output limits and keeps export available without offering an impossible pool save', async () => {
  request.mockResolvedValue({ tables: [{ ...table, state: 'closed', finalization: { state: 'pending', reason: 'output_limits' } }] })
  await act(async () => root.render(<LiveTablesPanel testId="run" projectId="project" />))
  expect(container.textContent).toContain('Final output exceeds data-pool execution limits.')
  expect(container.querySelector('input')).toBeNull()
  expect([...container.querySelectorAll('button')].some(button => button.textContent === 'Export table')).toBe(true)
  expect(request.mock.calls.some(([path]) => path.includes('/auth/effective-access'))).toBe(false)
})

it('uses an explicit current snapshot when a retained summary has no revision', async () => {
  request.mockResolvedValue({ tables: [{ ...table, revision: undefined }] }); await render()
  expect(container.textContent).toContain('Revision Unknown')
  request.mockResolvedValueOnce({ revision: 7, columns: ['a'], rows: [{ version: 1, values: { a: 'current' } }] }); await click('Preview up to 100 rows')
  expect(request).toHaveBeenLastCalledWith('/tests/run/live-tables/t/rows?offset=0&limit=100&revision=0', expect.anything())
  expect(container.textContent).toContain('Preview revision 7')
})


it('shows project-only attachment references without fetching values or attributing global counts', async () => {
  request.mockResolvedValue({ tables: [], project_tables: [{ table_id: 'shared/table', instance_id: 'original-instance', attachment_id: 'attachment', alias: 'work', group: 'Consumers', state: 'active', revision: '9007199254740993', counts: { available: 92345 }, values: { id: 'never-disclose' } }] })
  await act(async () => root.render(<LiveTablesPanel testId="run" projectId="project" />))
  expect(container.textContent).not.toContain('No live tables yet')
  expect(container.textContent).toContain('original-instance')
  expect(container.textContent).toContain('Recorded revision 9007199254740993')
  expect(container.textContent).not.toContain('92345')
  expect(container.textContent).not.toContain('never-disclose')
  const link = container.querySelector('a')!
  expect(link.getAttribute('href')).toBe('#/repository?setting=live-tables&projectId=project&resourceId=shared%2Ftable')
  expect(container.querySelector('dl')).toBeNull()
  expect(request).toHaveBeenCalledTimes(1)
})
