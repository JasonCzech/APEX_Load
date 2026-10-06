// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AppDynamicsConnectionsCard from './AppDynamicsConnectionsCard'
import { appDynamicsCanEnable, appDynamicsDraft, appDynamicsMissing } from './appDynamicsConnectionModel'
import type { AppDynamicsConnection } from '../types/appdynamics'
import { appDynamicsCapabilityHealth } from '../types/appdynamics'

const api = vi.hoisted(() => ({ listAppDynamicsConnections: vi.fn(), createAppDynamicsConnection: vi.fn(), updateAppDynamicsConnection: vi.fn(), deleteAppDynamicsConnection: vi.fn(), testAppDynamicsConnection: vi.fn(), discoverAppDynamicsEntities: vi.fn(), getAppDynamicsOperation: vi.fn() }))
vi.mock('../hooks/api/useAppDynamicsConnectionsApi', () => ({ useAppDynamicsConnectionsApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
const saved: AppDynamicsConnection = { id: 'appd', project_id: 'p', name: 'Production APM', enabled: true, collection_mode: 'direct', config: { endpoint: 'https://apm.example.com', context_path: '/controller', account_name: 'account', auth_type: 'oauth' }, revision: 2, verified_revision: 2, has_client_id: true, has_client_secret: true, has_username: false, has_password: false, has_events_api_key: false, created_at: '', updated_at: '' }
beforeEach(() => { vi.clearAllMocks(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); api.listAppDynamicsConnections.mockResolvedValue({ connections: [] }); api.createAppDynamicsConnection.mockResolvedValue(saved); api.updateAppDynamicsConnection.mockResolvedValue(saved) })
afterEach(() => { act(() => root.unmount()); container.remove() })
async function click(text: string) { await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === text)!.click()) }
async function input(label: string, value: string) { const node = Array.from(container.querySelectorAll('label')).find(node => node.textContent?.startsWith(label))!.querySelector('input')!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value); node.dispatchEvent(new Event('input', { bubbles: true })) }) }

it('saves an incomplete disabled draft and preserves write-only references', async () => {
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="p" canManage />))
  await click('Add AppDynamics connection'); await input('Name (required)', 'Draft APM'); await click('Save connection')
  expect(api.createAppDynamicsConnection).toHaveBeenCalledWith('p', expect.objectContaining({ name: 'Draft APM', enabled: false }))
  api.listAppDynamicsConnections.mockResolvedValue({ connections: [saved] })
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="other" canManage />))
  await click('Edit'); expect(container.querySelector('input[type=password]')?.getAttribute('value')).toBe('')
  await input('Name (required)', 'Renamed'); await click('Save connection')
  expect(api.updateAppDynamicsConnection).toHaveBeenCalledWith('other', 'appd', expect.objectContaining({ revision: 2, client_id_secret: undefined, client_secret_secret: undefined }))
})

it('requires explicit confirmation before deletion and retains revision fencing', async () => {
  api.listAppDynamicsConnections.mockResolvedValue({ connections: [saved] })
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="p" canManage />))
  await click('Delete'); expect(api.deleteAppDynamicsConnection).not.toHaveBeenCalled()
  await click('Keep connection'); expect(api.deleteAppDynamicsConnection).not.toHaveBeenCalled()
  await click('Delete'); await click('Confirm delete'); expect(api.deleteAppDynamicsConnection).toHaveBeenCalledWith('p', 'appd', 2)
})

it('hides endpoints and administrative controls from read-only users', async () => {
  api.listAppDynamicsConnections.mockResolvedValue({ connections: [saved] })
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="p" />))
  expect(container.textContent).toContain('Production APM'); expect(container.textContent).not.toContain('https://apm.example.com'); expect(container.querySelector('button')).toBeNull()
})

it('shows asynchronous capability diagnostics and unverified revisions', async () => {
  api.listAppDynamicsConnections.mockResolvedValue({ connections: [{ ...saved, verified_revision: 1 }] })
  api.testAppDynamicsConnection.mockResolvedValue({ id: 'op', operation: 'test', status: 'complete', result: { capabilities: { metrics: { status: 'connected' }, analytics: { status: 'unsupported', message: 'No Analytics license' } } } })
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="p" canManage />))
  expect(container.textContent).toContain('Current revision has not been verified.')
  await click('Test saved connection'); expect(container.textContent).toContain('No Analytics license')
})

it('validates only the selected authentication and optional Analytics capability', () => {
  const draft = appDynamicsDraft(saved)
  expect(appDynamicsMissing(draft, saved)).toEqual([])
  expect(appDynamicsMissing({ ...draft, collection_mode: 'collector' }, saved)).toContain('Assigned collector')
  expect(appDynamicsMissing({ ...draft, config: { ...draft.config, auth_type: 'basic' } }, saved)).toEqual(['Username secret reference', 'Password secret reference'])
  expect(appDynamicsMissing({ ...draft, config: { ...draft.config, events_endpoint: 'https://events.example.com' } }, saved)).toContain('Analytics account and API key secret reference')
})

it('permits enabling only the saved verified configuration and requires credentials after identity changes', () => {
  const draft = appDynamicsDraft(saved)
  expect(appDynamicsCanEnable(draft, saved)).toBe(true)
  expect(appDynamicsCanEnable(draft, { ...saved, verified_revision: 1 })).toBe(false)
  const changed = { ...draft, config: { ...draft.config, endpoint: 'https://other.example.com' } }
  expect(appDynamicsCanEnable(changed, saved)).toBe(false)
  expect(appDynamicsMissing(changed, saved)).toContain('OAuth client secret reference')
  expect(appDynamicsCanEnable({ ...draft, client_secret_secret: 'new-secret' }, saved)).toBe(false)
})

it('discovers backends and searches bounded results with an explicit truncation notice', async () => {
  api.listAppDynamicsConnections.mockResolvedValue({ connections: [saved] })
  api.discoverAppDynamicsEntities.mockResolvedValue({ id: 'op', status: 'complete', result: { truncated: true, entities: [{ id: '7', name: 'Checkout backend', type: 'backends' }, { id: '8', name: 'Billing backend', type: 'backends' }] } })
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="p" canManage />))
  await click('Discover entities')
  const select = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Entity type'))!.querySelector('select')!
  await act(async () => { select.value = 'backends'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  await input('Application ID', '123'); await click('Discover')
  expect(api.discoverAppDynamicsEntities).toHaveBeenCalledWith('p', 'appd', 'backends', expect.objectContaining({ application_id: '123' }))
  expect(container.textContent).toContain('Discovery results are truncated')
  await input('Search discovery results', 'checkout')
  expect(container.textContent).toContain('Checkout backend'); expect(container.textContent).not.toContain('Billing backend')
})

it('projects the provider capability status map before boolean fallbacks', () => {
  expect(appDynamicsCapabilityHealth({ metrics: true, events: false, analytics: false, metric_api: 'metric-data-v2', status: { controller: 'available', events: 'forbidden', analytics: 'unlicensed' } })).toEqual({ controller: { status: 'available' }, events: { status: 'forbidden' }, analytics: { status: 'unlicensed' }, metrics: { status: 'supported' } })
})

it('keeps private-network policy controls limited to system administrators', async () => {
  api.listAppDynamicsConnections.mockResolvedValue({ connections: [saved] })
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="p" canManage />))
  await click('Edit')
  const field = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Approved private CIDRs'))!.querySelector('textarea')!
  expect(field.disabled).toBe(true)
  await act(async () => root.render(<AppDynamicsConnectionsCard projectId="p" canManage canApprovePrivateNetworks />))
  expect(field.disabled).toBe(false)
})

it('preserves Analytics credentials only for the same independent Analytics identity', () => {
  const analytics = { ...saved, has_events_api_key: true, config: { ...saved.config, events_endpoint: 'https://events.example.com', events_account_name: 'global-account' } }
  const draft = appDynamicsDraft(analytics)
  expect(appDynamicsMissing(draft, analytics)).toEqual([])
  expect(appDynamicsMissing({ ...draft, config: { ...draft.config, events_endpoint: 'https://other-events.example.com' } }, analytics)).toContain('Analytics account and API key secret reference')
})
