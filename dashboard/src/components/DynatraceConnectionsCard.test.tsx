// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DynatraceConnectionsCard from './DynatraceConnectionsCard'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const api = { listDynatraceConnections: vi.fn(), createDynatraceConnection: vi.fn(), updateDynatraceConnection: vi.fn(), deleteDynatraceConnection: vi.fn(), testDynatraceConnection: vi.fn() }
vi.mock('../hooks/api/useDynatraceConnectionsApi', () => ({ useDynatraceConnectionsApi: () => api }))
let container: HTMLDivElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove(); vi.clearAllMocks() })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })

describe('DynatraceConnectionsCard', () => {
  it('offers an intentionally incomplete capability setup path', async () => {
    api.listDynatraceConnections.mockResolvedValue({ connections: [], count: 0 })
    await act(async () => root.render(<DynatraceConnectionsCard projectId="project-a" canManage />)); await flush()
    expect(container.textContent).toContain('Add Dynatrace connection')
    expect(container.textContent).toContain('Metrics API')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="https://abc.live.dynatrace.com"]')).toBeTruthy()
  })
  it('renders safe status without secret inputs for viewers', async () => {
    api.listDynatraceConnections.mockResolvedValue({ connections: [{ id: 'dt-a', project_id: 'project-a', name: 'Production Dynatrace', status: 'ready', enabled: true, environment_url: 'https://abc.live.dynatrace.com', metrics_enabled: true, problems_enabled: true, logs_enabled: false, has_environment_token: true, has_platform_token: false, has_oauth_client_id: false, has_oauth_client_secret: false, oauth_scopes: [], metric_queries: [], poll_interval_seconds: 15, created_at: '', updated_at: '' }], count: 1 })
    await act(async () => root.render(<DynatraceConnectionsCard projectId="project-a" canManage={false} />)); await flush()
    expect(container.textContent).toContain('Production Dynatrace')
    expect(container.textContent).toContain('ready')
    expect(container.querySelector('input[type="password"]')).toBeNull()
  })
})
