// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CapacityCommitments } from './CapacityCommitments'
import { InfrastructureSetupGuide } from './InfrastructureSetupGuide'
import { AuditEventTable } from './AuditEventTable'
import type { DashboardAuditEvent } from '../types'
const { listTimeslots, compute, clusters, fleet } = vi.hoisted(() => ({
  listTimeslots: vi.fn(), compute: { listCloudConnections: vi.fn(), listExecutionTargets: vi.fn() }, clusters: { listClusters: vi.fn() }, fleet: { listFleetPools: vi.fn() },
}))
vi.mock('../hooks/api/useCapacityApi', () => ({ useCapacityApi: () => ({ listTimeslots }) }))
vi.mock('../hooks/api/useComputeApi', () => ({ useComputeApi: () => compute }))
vi.mock('../hooks/api/useClustersApi', () => ({ useClustersApi: () => clusters }))
vi.mock('../hooks/api/useFleetApi', () => ({ useFleetApi: () => fleet }))
let host: HTMLDivElement, root: Root
beforeEach(() => { vi.clearAllMocks(); host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
afterEach(async () => { await act(async () => root.unmount()); host.remove() })
describe('settings follow-up workflows', () => {
 it('shows saved allocations and bookings distinctly and scopes booking queries', async () => {
  listTimeslots.mockResolvedValue({ timeslots: [{ id: 'slot', project_id: 'p', start_at: '2026-09-08T12:00:00Z', end_at: '2026-09-08T13:00:00Z', requested_capacity: { pool_id: 'pool', generators: 2 }, approval_state: 'pending' }] })
  await act(async () => root.render(<CapacityCommitments reservations={[{ project_id: 'p', reserved_generators: 3 }, { project_id: 'other', reserved_generators: 99 }]} projectId="p" projects={[{ id: 'p', name: 'Project' }]} />))
  expect(listTimeslots).toHaveBeenCalledWith(expect.objectContaining({ project_id: 'p', offset: 0 }))
  expect(host.textContent).toContain('Standing allocation'); expect(host.textContent).toContain('Scheduled booking')
  expect(host.textContent).toContain('pending'); expect(host.textContent).not.toContain('99 generators')
  expect(host.querySelector('a[href*="resourceId=pool"]')).not.toBeNull()
 })
 it('shows incomplete booking discovery as an error, not zero available capacity', async () => {
  listTimeslots.mockRejectedValue(new Error('offline'))
  await act(async () => root.render(<CapacityCommitments reservations={[]} projects={[]} />))
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('incomplete')
  expect(host.textContent).not.toContain('No commitments')
 })
 it('guides different execution paths and reports inventory failure', async () => {
  compute.listCloudConnections.mockResolvedValue({ cloud_connections: [] }); compute.listExecutionTargets.mockResolvedValue({ execution_targets: [] })
  clusters.listClusters.mockResolvedValue({ clusters: [] }); fleet.listFleetPools.mockResolvedValue({ pools: [] })
  await act(async () => root.render(<InfrastructureSetupGuide />))
  expect(host.textContent).toContain('Connect the Kubernetes cluster')
  await act(async () => { const select = host.querySelector('select')!; select.value = 'static'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(sessionStorage.getItem('apexload.infrastructureSetup.environment')).toBe('static')
  expect(host.textContent).not.toContain('Connect the Kubernetes cluster'); expect(host.textContent).toContain('Assign registered static generators')
  compute.listCloudConnections.mockRejectedValue(new Error('offline'))
  await act(async () => host.querySelector('button')!.click())
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('could not be loaded')
 })
 it('offers keyboard-operable audit details and preserves selectable access activity', async () => {
  const event = { id: 'e', actor_subject: 'operator', actor_type: 'user', actor_display_name: 'Operator', action: 'pool.update', resource_type: 'pool', resource_id: 'p', status: 'success', created_at: '2026-09-08T12:00:00Z', details: { fields: ['name'] } } as DashboardAuditEvent
  await act(async () => root.render(<AuditEventTable events={[event]} />))
  expect(host.querySelector('summary')?.textContent).toBe('Event details'); expect(host.querySelector('pre')?.textContent).toContain('name')
  const select = vi.fn()
  await act(async () => root.render(<AuditEventTable events={[event]} onSelect={select} />))
  await act(async () => host.querySelector('button')!.click())
  expect(select).toHaveBeenCalledExactlyOnceWith(event)
 })
})
