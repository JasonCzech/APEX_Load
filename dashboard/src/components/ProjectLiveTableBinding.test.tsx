// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ProjectLiveTableBinding from './ProjectLiveTableBinding'

const { api } = vi.hoisted(() => ({ api: { listProjectLiveTables: vi.fn() } }))
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
let container: HTMLDivElement; let root: Root
beforeEach(() => { api.listProjectLiveTables.mockResolvedValue({ tables: [{ id: 'shared', name: 'Shared work', profile: 'vts_columns', state: 'active', columns: ['id'], instance_id: 'instance', permissions: { can_view: true } }] }); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
it('offers project tables without run seeding or output policies and removes unsupported leases', async () => {
  const change = vi.fn()
  await act(async () => root.render(<ProjectLiveTableBinding projectId="project" binding={{ alias: 'work', pool_id: '', access_mode: 'live', scope: 'project', selection_strategy: 'lease' }} onChange={change} />))
  const select = container.querySelector('select')!
  await act(async () => { select.value = 'shared'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(change).toHaveBeenCalledWith({ project_table_id: 'shared', selection_strategy: 'manual' })
  expect(container.textContent).not.toContain('When the run ends')
  expect(container.textContent).not.toContain('Starting data')
  expect(container.textContent).toContain('keeps its data when this run finishes')
})
