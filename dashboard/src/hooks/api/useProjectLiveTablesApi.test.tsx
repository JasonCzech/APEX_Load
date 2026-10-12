// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import { useProjectLiveTablesApi } from './useProjectLiveTablesApi'
const { request } = vi.hoisted(() => ({ request: vi.fn().mockResolvedValue({}) }))
vi.mock('../apiClient', () => ({ request }))
beforeEach(() => request.mockClear())

it('encodes project/table identity and preserves decimal revision strings in requests', async () => {
  let api!: ReturnType<typeof useProjectLiveTablesApi>
  function Harness() { api = useProjectLiveTablesApi(); return null }
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<Harness />))
    const abort = new AbortController()
    await api.getProjectLiveTableRows('p/a', 't/a', 'instance', '9007199254740993', 100, { signal: abort.signal })
    expect(request).toHaveBeenCalledWith('/projects/p%2Fa/live-tables/t%2Fa/rows?instance_id=instance&revision=9007199254740993&offset=100&limit=100', expect.objectContaining({ cache: 'no-store', signal: abort.signal }))
    const command = { idempotency_key: 'key', issued_at: 'time', expected_instance_id: 'i', expected_revision: '9007199254740993', operation: 'increment' as const, record_id: 'r', expected_version: '9007199254740995', delta: '9223372036854775807', columns: ['count'] }
    await api.mutateProjectLiveTable('p', 't', command)
    expect(JSON.parse(request.mock.calls[request.mock.calls.length - 1][1].body)).toEqual(command)
  } finally { await act(async () => root.unmount()) }
})

it('uses the declared controller route and method for every project table operation', async () => {
  let api!: ReturnType<typeof useProjectLiveTablesApi>
  function Harness() { api = useProjectLiveTablesApi(); return null }
  const root = createRoot(document.createElement('div'))
  try {
    await act(async () => root.render(<Harness />))
    const signal = new AbortController().signal
    const create = { name: 'Work', profile: 'native_queue' as const, columns: ['id'], limits: { max_rows: 10, max_bytes: 1024 } }
    const mutation = { idempotency_key: 'key', issued_at: 'time', expected_instance_id: 'instance', expected_revision: '0', operation: 'append' as const, values: { id: '' } }
    const lifecycle = { action: 'quiesce' as const, expected_config_revision: '9007199254740993', expected_instance_id: 'instance', expected_revision: '0', reason: 'Review' }
    await api.listProjectLiveTables('p/a', { signal })
    await api.getProjectLiveTable('p/a', 't/a', { signal })
    await api.createProjectLiveTable('p/a', create)
    await api.getProjectLiveTableRows('p/a', 't/a', 'i/?&', '0', 0, { signal })
    await api.exportProjectLiveTable('p/a', 't/a', 'i/?&', '0')
    await api.mutateProjectLiveTable('p/a', 't/a', mutation)
    await api.changeProjectLiveTableLifecycle('p/a', 't/a', lifecycle)
    const collection = '/projects/p%2Fa/live-tables'
    expect(request.mock.calls).toEqual([
      [collection, { signal }],
      [`${collection}/t%2Fa`, { signal }],
      [collection, { method: 'POST', body: JSON.stringify(create) }],
      [`${collection}/t%2Fa/rows?instance_id=i%2F%3F%26&revision=0&offset=0&limit=100`, { cache: 'no-store', signal }],
      [`${collection}/t%2Fa/export?instance_id=i%2F%3F%26&revision=0`, { cache: 'no-store', maxResponseBytes: 32 * 1024 * 1024 }],
      [`${collection}/t%2Fa/mutations`, { method: 'POST', body: JSON.stringify(mutation) }],
      [`${collection}/t%2Fa/lifecycle`, { method: 'POST', body: JSON.stringify(lifecycle) }],
    ])
  } finally { await act(async () => root.unmount()) }
})
