// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import PrometheusDashboardAdoption from './PrometheusDashboardAdoption'
import type { ImportedDashboard } from '../types'
const api = vi.hoisted(() => ({ listPrometheusConnections: vi.fn(), adoptPrometheusDashboard: vi.fn() }))
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/api/usePrometheusConnectionsApi', () => ({ usePrometheusConnectionsApi: () => api }))
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
const dashboard: ImportedDashboard = { id: 'd', project_id: 'p', name: 'Target dashboard', enabled: true, panels: [] }
beforeEach(() => {
  vi.clearAllMocks(); container = document.createElement('div'); root = createRoot(container)
  api.listPrometheusConnections.mockResolvedValue({ connections: [{ id: 'c', name: 'Target connection', enabled: true, revision: 2, verified_revision: 2, collection_mode: 'direct' }] })
  api.adoptPrometheusDashboard.mockResolvedValue({ selection: { project_id: 'p', queries: [{ id: 'cpu', name: 'cpu', expression: 'cpu{job="checkout"}', unit: '%', semantics: 'gauge', allowed_labels: ['job'], min_series: 1 }], step_seconds: 15, settle_seconds: 120 }, connection_id: 'c', connection_revision: 2, dashboard_id: 'd', dashboard_sha256: 'frozen-hash', datasource_uids: ['legacy-uid'], resolved_bindings: { service: 'checkout' }, range_seconds: 300, warnings: ['Review query scope'], truncated: false })
})
afterEach(() => act(() => root.unmount()))
async function click(name: string) { await act(async () => Array.from(container.querySelectorAll('button')).find(item => item.textContent === name)!.click()) }
async function openDraft() {
  await act(async () => root.render(<PrometheusDashboardAdoption dashboard={dashboard} projectId="p" />)); await click('Create Prometheus monitor source')
  await act(async () => { const select = container.querySelector('select')!; Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'c'); select.dispatchEvent(new Event('change', { bubbles: true })) })
  await click('Resolve dashboard query draft')
}
it('resolves server-owned bindings and requires explicit review before disabled source creation', async () => {
  await openDraft(); expect(api.adoptPrometheusDashboard).toHaveBeenCalledWith('p', 'c', 'd')
  expect(container.textContent).toContain('frozen-hash'); expect(container.textContent).toContain('legacy-uid'); expect(container.textContent).toContain('service=checkout')
  const save = Array.from(container.querySelectorAll('button')).find(item => item.textContent === 'Save adopted source')!; expect(save.disabled).toBe(true)
  await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click()); expect(save.disabled).toBe(false)
  await click('Save adopted source')
  const payload = JSON.parse(request.mock.calls[0][1].body)
  expect(payload).toMatchObject({ pack: 'prometheus-query', interval_seconds: 30, enabled: false, executor_mode: 'direct', connection_id: 'c', prometheus: { project_id: 'p', queries: [{ expression: 'cpu{job="checkout"}' }] } })
  expect(payload).not.toHaveProperty('monitor_rules'); expect(payload).not.toHaveProperty('thresholds'); expect(container.textContent).toContain('attach it explicitly')
})
it('shows empty and truncated drafts without silently creating a source', async () => {
  api.adoptPrometheusDashboard.mockResolvedValue({ selection: { queries: [] }, connection_id: 'c', connection_revision: 2, datasource_uids: [], warnings: ['test_id template requires replacement'], truncated: true, range_seconds: 300 })
  await openDraft(); expect(container.textContent).toContain('No reusable supported expressions'); expect(container.textContent).toContain('Draft truncated'); expect(request).not.toHaveBeenCalled()
  expect(Array.from(container.querySelectorAll('button')).find(item => item.textContent === 'Save adopted source')!.disabled).toBe(true)
})
