// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ELKConnectionsCard from './ELKConnectionsCard'
import type { ELKConnection } from '../types/monitoring'

const api = vi.hoisted(() => ({ listELKConnections: vi.fn(), createELKConnection: vi.fn(), updateELKConnection: vi.fn(), deleteELKConnection: vi.fn(), testELKConnection: vi.fn(), inspectELKConnection: vi.fn(), getELKInspection: vi.fn(), cancelELKInspection: vi.fn() }))
vi.mock('../hooks/api/useELKConnectionsApi', () => ({ useELKConnectionsApi: () => api }))
vi.mock('../hooks/apiClient', () => ({ request: vi.fn().mockResolvedValue({ collectors: [] }) }))
let root: Root; let container: HTMLElement
const saved = { id: 'elastic', project_id: 'p', name: 'Production logs', enabled: true, revision: 2, verified_revision: 2, product: 'elasticsearch', collection_mode: 'direct', endpoint: 'https://elastic.example.com', config: { endpoint: 'https://elastic.example.com', product: 'elasticsearch', auth_type: 'api_key' }, auth_type: 'api_key', has_api_key: true, has_basic_username: false, has_basic_password: false, metric_aggregations: [], created_at: '', updated_at: '', status: 'ready' } as ELKConnection
beforeEach(() => { vi.clearAllMocks(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); api.listELKConnections.mockResolvedValue({ connections: [] }); api.createELKConnection.mockResolvedValue(saved); api.updateELKConnection.mockResolvedValue(saved) })
afterEach(() => { act(() => root.unmount()); container.remove() })
async function click(label: string) { await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === label)!.click()) }
async function input(label: string, value: string) { const node = Array.from(container.querySelectorAll('label')).find(node => node.textContent?.startsWith(label))!.querySelector('input')!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value); node.dispatchEvent(new Event('input', { bubbles: true })) }) }
it('saves expanded disabled drafts and keeps enabling dependent on saved revision verification', async () => {
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />))
  await click('Add ELK connection'); await input('Name (required)', 'Elastic draft')
  expect(container.querySelector<HTMLInputElement>('input[type=checkbox]')!.disabled).toBe(true)
  await click('Save draft')
  expect(api.createELKConnection).toHaveBeenCalledWith('p', expect.objectContaining({ name: 'Elastic draft', enabled: false, config: expect.objectContaining({ product: 'elasticsearch', timeout_seconds: 10 }) }))
})
it('uses revision-fenced inspections and hides saved secret references while editing', async () => {
  api.listELKConnections.mockResolvedValue({ connections: [saved] })
  api.inspectELKConnection.mockResolvedValue({ id: 'op', connection_id: 'elastic', connection_revision: 2, kind: 'verify', status: 'complete', result: { capabilities: { product: 'elasticsearch', version: '9.0', esql: true, dsl: true } } })
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />))
  await click('Test saved connection')
  expect(api.inspectELKConnection).toHaveBeenCalledWith('p', 'elastic', { kind: 'verify', expected_revision: 2 }, expect.anything())
  expect(container.textContent).toContain('ES|QL: supported')
  await click('Edit')
  expect(container.querySelector<HTMLInputElement>('input[type=password]')?.value).toBe('')
  await input('Name (required)', 'Renamed logs'); await click('Save connection')
  expect(api.updateELKConnection).toHaveBeenCalledWith('p', 'elastic', expect.objectContaining({ revision: 2, api_key_secret_name: undefined, name: 'Renamed logs' }))
})
it('limits private-network editing to administrators and does not expose endpoints to viewers', async () => {
  api.listELKConnections.mockResolvedValue({ connections: [saved] })
  await act(async () => root.render(<ELKConnectionsCard projectId="p" />))
  expect(container.textContent).not.toContain('https://elastic.example.com')
  expect(container.querySelector('button')).toBeNull()
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />)); await click('Edit')
  const approval = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Approved private CIDRs'))!.querySelector('textarea')!
  expect(approval.disabled).toBe(true)
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage canApprovePrivateNetworks />))
  expect(approval.disabled).toBe(false)
})

it("does not ask for connections a role cannot list, and says who sees them", async () => {
  await act(async () => root.render(<ELKConnectionsCard projectId="p" summaryOnly canView={false} />))
  expect(api.listELKConnections).not.toHaveBeenCalled()
  expect(container.textContent).toContain("shown to its editors and administrators")
})

it('opens only an explicitly approved dashboard destination on a verified saved connection', async () => {
  api.listELKConnections.mockResolvedValue({ connections: [{ ...saved, config: { ...saved.config, dashboards_url: 'https://dashboards.example.com/apex', kibana_endpoint: 'https://health.example.com' } }] })
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />))
  const link = container.querySelector<HTMLAnchorElement>('a[target="_blank"]')!
  expect(link.href).toBe('https://dashboards.example.com/apex')
  expect(link.rel).toBe('noopener noreferrer')
  await click('Edit'); await input('Approved Kibana / OpenSearch Dashboards URL', 'https://dashboards.example.com/new'); await input('Name (required)', 'Navigation draft')
  expect(link.href).toBe('https://dashboards.example.com/apex')
  await click('Discard draft')
  api.listELKConnections.mockResolvedValue({ connections: [{ ...saved, verified_revision: 1, config: { ...saved.config, dashboards_url: 'https://dashboards.example.com/apex' } }] })
  await act(async () => root.render(<ELKConnectionsCard projectId="stale" canManage />))
  expect(container.querySelector('a[target="_blank"]')).toBeNull()
  api.listELKConnections.mockResolvedValue({ connections: [{ ...saved, config: { ...saved.config, kibana_endpoint: 'https://health.example.com' } }] })
  await act(async () => root.render(<ELKConnectionsCard projectId="health-only" canManage />))
  expect(container.querySelector('a[target="_blank"]')).toBeNull()
})

it('preserves legacy editing and testing when the API returns an empty additive config object', async () => {
  const legacy = { ...saved, product: undefined, verified_revision: 0, config: { endpoint: '', product: '', auth_type: '' }, endpoint: 'https://legacy.example.com', index_pattern: 'logs-*', timestamp_field: '@timestamp', message_field: 'message' }
  api.listELKConnections.mockResolvedValue({ connections: [legacy] })
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />))
  await click('Edit'); await input('Name (required)', 'Renamed legacy')
  expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save connection')?.disabled).toBe(false)
  await click('Save connection')
  expect(api.updateELKConnection).toHaveBeenCalledWith('p', 'elastic', expect.objectContaining({ config: undefined, product: undefined, name: 'Renamed legacy' }))
  await click('Test saved connection')
  expect(api.testELKConnection).toHaveBeenCalledWith('p', 'elastic')
  expect(api.inspectELKConnection).not.toHaveBeenCalled()
})


it('keeps safe viewer summaries readable while hiding credentials and mutations', async () => {
  api.listELKConnections.mockResolvedValue({ connections: [saved] })
  await act(async () => root.render(<ELKConnectionsCard projectId="p" summaryOnly />))
  expect(container.textContent).toContain('Production logs')
  expect(container.textContent).toContain('ready')
  expect(container.querySelector('input[type="password"]')).toBeNull()
  expect(container.querySelectorAll('button')).toHaveLength(0)
})
