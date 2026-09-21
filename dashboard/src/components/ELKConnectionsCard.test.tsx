// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ELKConnectionsCard from './ELKConnectionsCard'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const api = {
  listELKConnections: vi.fn(), createELKConnection: vi.fn(), updateELKConnection: vi.fn(),
  deleteELKConnection: vi.fn(), testELKConnection: vi.fn(),
}
vi.mock('../hooks/api/useELKConnectionsApi', () => ({ useELKConnectionsApi: () => api }))

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove(); vi.clearAllMocks() })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })

describe('ELKConnectionsCard', () => {
  it('offers an intentionally incomplete setup path', async () => {
    api.listELKConnections.mockResolvedValue({ connections: [], count: 0 })
    await act(async () => root.render(<ELKConnectionsCard projectId="project-a" canManage />))
    await flush()
    expect(container.textContent).toContain('Add ELK connection')
    await act(async () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add ELK connection')!.click())
    expect(container.textContent).toContain('Enable for new tests')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="https://elastic.example.com:9200"]')).toBeTruthy()
  })

  it('renders status and keeps write-only secret references out of the list', async () => {
    api.listELKConnections.mockResolvedValue({ connections: [{ id: 'elk-a', project_id: 'project-a', name: 'Production logs', status: 'ready', enabled: true, endpoint: 'https://elastic.example.com', has_api_key: true, has_basic_username: false, has_basic_password: false, metric_aggregations: [], created_at: '', updated_at: '' }], count: 1 })
    await act(async () => root.render(<ELKConnectionsCard projectId="project-a" canManage={false} />))
    await flush()
    expect(container.textContent).toContain('Production logs')
    expect(container.textContent).toContain('ready')
    expect(container.querySelector('input[type="password"]')).toBeNull()
  })
})
