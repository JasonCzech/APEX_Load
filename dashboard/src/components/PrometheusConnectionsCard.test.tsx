// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import PrometheusConnectionsCard from './PrometheusConnectionsCard'
import { prometheusDraft, prometheusMissing, prometheusCanEnable } from './prometheusConnectionModel'
import type { PrometheusConnection } from '../types/prometheus'
const api = vi.hoisted(() => ({ listPrometheusConnections: vi.fn(), createPrometheusConnection: vi.fn(), updatePrometheusConnection: vi.fn(), deletePrometheusConnection: vi.fn(), inspectPrometheusConnection: vi.fn(), getPrometheusOperation: vi.fn() }))
vi.mock('../hooks/api/usePrometheusConnectionsApi', () => ({ usePrometheusConnectionsApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const saved: PrometheusConnection = { id: 'c', project_id: 'p', name: 'Checkout metrics', enabled: false, collection_mode: 'direct', config: { endpoint: 'https://metrics.example.com', backend_profile: 'prometheus', auth_type: 'bearer' }, has_token: true, revision: 2, verified_revision: 2, last_check_status: 'connected' }
let root: Root; let container: HTMLElement
beforeEach(() => { vi.clearAllMocks(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); api.listPrometheusConnections.mockResolvedValue({ connections: [] }); api.createPrometheusConnection.mockResolvedValue(saved); api.updatePrometheusConnection.mockResolvedValue(saved) })
afterEach(() => { act(() => root.unmount()); container.remove() })
async function click(text: string) { await act(async () => Array.from(container.querySelectorAll('button')).find(item => item.textContent === text)!.click()) }
async function input(label: string, value: string) { const node = Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith(label))!.querySelector('input')!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value); node.dispatchEvent(new Event('input', { bubbles: true })) }) }
it('saves disabled name-only drafts and never redisplays secret references', async () => {
  await act(async () => root.render(<PrometheusConnectionsCard projectId="p" canManage />)); await click('Add Prometheus connection'); await input('Name (required)', 'Draft'); await click('Save connection')
  expect(api.createPrometheusConnection).toHaveBeenCalledWith('p', expect.objectContaining({ name: 'Draft', enabled: false, token_secret: undefined }))
  api.listPrometheusConnections.mockResolvedValue({ connections: [saved] }); await act(async () => root.render(<PrometheusConnectionsCard projectId="other" canManage />)); await click('Edit')
  expect(container.querySelector('input[type=password]')?.getAttribute('value')).toBe('')
  await input('Name (required)', 'Renamed'); await click('Save connection')
  expect(api.updatePrometheusConnection).toHaveBeenCalledWith('other', 'c', expect.objectContaining({ name: 'Renamed', revision: 2, token_secret: undefined }))
})
it('hides endpoints and mutation controls from viewers', async () => {
  api.listPrometheusConnections.mockResolvedValue({ connections: [saved] }); await act(async () => root.render(<PrometheusConnectionsCard projectId="p" />))
  expect(container.textContent).toContain('Checkout metrics'); expect(container.textContent).not.toContain(saved.config.endpoint); expect(container.querySelector('button')).toBeNull()
})
it('requires confirmation and revision for deletion', async () => {
  api.listPrometheusConnections.mockResolvedValue({ connections: [saved] }); await act(async () => root.render(<PrometheusConnectionsCard projectId="p" canManage />)); await click('Delete'); expect(api.deletePrometheusConnection).not.toHaveBeenCalled(); await click('Keep connection'); await click('Delete'); await click('Confirm delete'); expect(api.deletePrometheusConnection).toHaveBeenCalledWith('p', 'c', 2)
})
it('renders independent API capability and bounded discovery status', async () => {
  api.listPrometheusConnections.mockResolvedValue({ connections: [saved] }); api.inspectPrometheusConnection.mockResolvedValue({ id: 'op', status: 'complete', result: { capabilities: { metrics: true, targets: false, status: { targets: 'permission_denied' } }, truncated: true, entities: [{ name: 'requests_total', type: 'counter' }] } })
  await act(async () => root.render(<PrometheusConnectionsCard projectId="p" canManage />)); await click('Test saved connection'); expect(container.textContent).toContain('targets: permission_denied'); expect(container.textContent).toContain('requests_total'); expect(container.textContent).toContain('result limit')
  expect(api.inspectPrometheusConnection).toHaveBeenCalledWith('p', 'c', 'test', {})
})
it('shows safe discovery series identities and metric metadata from the public entity_ids contract', async () => {
  api.listPrometheusConnections.mockResolvedValue({connections:[saved]})
  api.inspectPrometheusConnection.mockResolvedValue({id:'op',status:'complete',result:{entities:[{name:'requests_total',type:'series',entity_ids:{job:'checkout',instance:'192.0.2.10:9100'}},{name:'requests_total',type:'metric_metadata',entity_ids:{type:'counter',unit:'requests'}}]}})
  await act(async()=>root.render(<PrometheusConnectionsCard projectId="p" canManage />));await click('Discover and preview queries');await click('Discover')
  const table=container.querySelector('[aria-label="Prometheus inspection result"] table')!
  expect(table.textContent).toContain('Labels / metadata')
  expect(table.textContent).toContain('checkout')
  expect(table.textContent).toContain('192.0.2.10:9100')
  expect(table.textContent).toContain('counter')
  expect(table.textContent).toContain('requests')
})
it('permits enabling only verified unchanged configuration and resets credentials after identity changes', () => {
  const draft = prometheusDraft(saved); expect(prometheusCanEnable(draft, saved)).toBe(true); expect(prometheusMissing(draft, saved)).toEqual([])
  const changed = { ...draft, config: { ...draft.config, endpoint: 'https://other.example.com' } }
  expect(prometheusCanEnable(changed, saved)).toBe(false); expect(prometheusMissing(changed, saved)).toContain('Bearer token secret reference')
  expect(prometheusCanEnable({ ...draft, token_secret: 'replacement' }, saved)).toBe(false)
  expect(prometheusMissing({ ...draft, config: { ...draft.config, auth_type: 'azure', azure_credential_mode: 'service_principal' } }, saved)).toContain('Azure client secret reference')
})
it('keeps private trust controls system-admin only', async () => {
  api.listPrometheusConnections.mockResolvedValue({ connections: [saved] }); await act(async () => root.render(<PrometheusConnectionsCard projectId="p" canManage />)); await click('Edit')
  const field = Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith('Approved private CIDRs'))!.querySelector('textarea')!; expect(field.disabled).toBe(true)
  await act(async () => root.render(<PrometheusConnectionsCard projectId="p" canManage canApprovePrivateNetworks />)); expect(field.disabled).toBe(false)
})

it('clears a hidden collector assignment when changing a saved connection to direct execution', async () => {
  api.listPrometheusConnections.mockResolvedValue({ connections: [{ ...saved, collection_mode: 'collector', collector_agent_id: 'private-collector' }] })
  await act(async () => root.render(<PrometheusConnectionsCard projectId="p" canManage />)); await click('Edit')
  const location = Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith('Collection location'))!.querySelector('select')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(location, 'direct'); location.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(Array.from(container.querySelectorAll('label')).some(item => item.textContent?.startsWith('Assigned collector ID'))).toBe(false)
  await click('Save connection')
  expect(api.updatePrometheusConnection).toHaveBeenCalledWith('p', 'c', expect.objectContaining({ collection_mode: 'direct', collector_agent_id: '', enabled: false }))
})
