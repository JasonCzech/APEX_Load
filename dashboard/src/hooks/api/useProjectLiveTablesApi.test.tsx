// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useProjectLiveTablesApi } from './useProjectLiveTablesApi'
const { request } = vi.hoisted(() => ({ request: vi.fn().mockResolvedValue({}) }))
vi.mock('../apiClient', () => ({ request }))

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
