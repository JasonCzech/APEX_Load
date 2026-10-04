// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import DynatraceConnectionsCard from './DynatraceConnectionsCard'
import type { DynatraceConnection } from '../types/monitoring'

const api = vi.hoisted(() => ({ listDynatraceConnections: vi.fn(), createDynatraceConnection: vi.fn(), updateDynatraceConnection: vi.fn(), deleteDynatraceConnection: vi.fn(), createDynatraceOperation: vi.fn(), getDynatraceOperation: vi.fn() }))
vi.mock('../hooks/api/useDynatraceConnectionsApi', () => ({ useDynatraceConnectionsApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
const saved: DynatraceConnection = { id: 'a', project_id: 'p', name: 'Assigned SaaS', description: '', enabled: false, status: 'disabled', revision: 2, verified_revision: 2, collection_mode: 'collector', collector_agent_id: 'approved-agent', environment_url: 'https://tenant.live.dynatrace.com', platform_url: '', metrics_enabled: false, problems_enabled: true, logs_enabled: false, has_environment_token: true, has_platform_token: false, has_oauth_client_id: false, has_oauth_client_secret: false, oauth_resource: '', oauth_scopes: [], metric_queries: [], problem_selector: '', problem_entity_selector: '', log_base_filter: '', log_test_id_field: 'apex.test_id', log_timestamp_field: 'timestamp', log_status_field: 'status', log_message_field: 'content', log_trace_id_field: 'trace_id', poll_interval_seconds: 15, created_at: '', updated_at: '' }
beforeEach(() => {
  vi.resetAllMocks()
  api.listDynatraceConnections.mockResolvedValue({ connections: [saved, { ...saved, id: 'b', name: 'Other SaaS' }] })
  api.updateDynatraceConnection.mockResolvedValue(saved)
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
})
afterEach(() => { act(() => root.unmount()); container.remove(); vi.useRealTimers() })
async function click(text: string, index = 0) { await act(async () => [...container.querySelectorAll('button')].filter(button => button.textContent === text)[index].click()) }
async function select(label: string, value: string) { const element = [...container.querySelectorAll('label')].find(node => node.textContent?.startsWith(label))!.querySelector('select')!; await act(async () => { element.value = value; element.dispatchEvent(new Event('change', { bubbles: true })) }) }
const render = () => act(async () => root.render(<DynatraceConnectionsCard projectId="p" canManage />))

it('clears the hidden assignment when changing an existing collector connection to direct', async () => {
  await render(); await click('Edit'); await select('Collection mode', 'direct')
  expect(container.textContent).not.toContain('Assigned collector ID')
  await click('Save draft')
  expect(api.updateDynatraceConnection).toHaveBeenCalledWith('p', 'a', expect.objectContaining({ expected_revision: 2, collection_mode: 'direct', collector_agent_id: '', environment_token_secret: undefined }))
})

it('binds pending polls to their connection and discards a result after discovery selection changes', async () => {
  vi.useFakeTimers()
  let finish!: (value: unknown) => void
  api.createDynatraceOperation.mockResolvedValue({ id: 'operation-a', kind: 'discover', status: 'pending' })
  api.getDynatraceOperation.mockReturnValue(new Promise(resolve => { finish = resolve }))
  await render(); await click('Discover metrics and entities'); await click('Discover')
  await act(async () => vi.advanceTimersByTime(1500))
  expect(api.getDynatraceOperation).toHaveBeenCalledWith('p', 'a', 'operation-a', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  await click('Discover metrics and entities', 1)
  expect(container.querySelector('[aria-label="Dynatrace operation"]')).toBeNull()
  await act(async () => finish({ id: 'operation-a', status: 'complete', result: { next_cursor: 'cursor-a', items: [{ id: 'old', name: 'Old connection result' }] } }))
  await act(async () => vi.advanceTimersByTime(1500))
  expect(container.textContent).not.toContain('Old connection result')
  expect(api.getDynatraceOperation).toHaveBeenCalledTimes(1)
})

it('drops completed discovery cursors when the connection or query scope changes', async () => {
  api.createDynatraceOperation.mockResolvedValue({ id: 'operation-a', kind: 'discover', status: 'complete', result: { next_cursor: 'cursor-a', items: [{ id: 'old', name: 'Old connection result' }] } })
  await render(); await click('Discover metrics and entities'); await click('Discover')
  expect(container.textContent).toContain('Next discovery page')
  await click('Discover metrics and entities', 1)
  expect(container.textContent).not.toContain('Next discovery page')
  await click('Discover')
  expect(api.createDynatraceOperation).toHaveBeenLastCalledWith('p', 'b', expect.objectContaining({ options: expect.objectContaining({ cursor: undefined }) }))
  await select('Discovery type', 'entities')
  expect(container.querySelector('[aria-label="Dynatrace operation"]')).toBeNull()
})

it('lets an unverified connection be enabled for per-test capture and shows both readiness states', async () => {
  const unverified: DynatraceConnection = { ...saved, enabled: true, status: 'unverified', legacy_capture_status: 'ready', verified_revision: 0, collection_mode: 'direct', collector_agent_id: '' }
  api.listDynatraceConnections.mockResolvedValue({ connections: [unverified] })
  api.updateDynatraceConnection.mockResolvedValue(unverified)
  await render()
  expect(container.textContent).toContain('Test capture ready')
  expect(container.textContent).toContain('Native monitoring unverified')
  await click('Edit')
  const enable = [...container.querySelectorAll('label')].find(node => node.textContent?.startsWith('Enable for new tests'))!.querySelector('input')!
  expect(enable.disabled).toBe(false)
  expect(enable.checked).toBe(true)
  expect(container.textContent).toContain('Native monitor sources also need the saved revision verified')
  await click('Save connection')
  expect(api.updateDynatraceConnection).toHaveBeenCalledWith('p', 'a', expect.objectContaining({ enabled: true }))
})
