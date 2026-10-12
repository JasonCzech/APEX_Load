// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import LiveTableReportSummary, { LiveTableFinalizationOutcome } from './LiveTableReportSummary'
import type { RunArchiveLiveTableSummary } from '../types/dataPools'

it('reports committed counts and separate bounded operation timing without rendering incidental row values', async () => {
 const container = document.createElement('div'); const root = createRoot(container)
 const table = { table_id: 't', alias: 'work', profile: 'native_queue', durability: 'memory', end_mode: 'write_back', state: 'closed', revision: 4, data_bytes: 500, counts: { seeded: 5, acknowledged: 3, available: 2, leased: 0 }, op_count: 10, op_errors: 2, op_unknown: 1, op_latency_total_ns: 25000000, op_latency_max_ns: 9000000, finalization: { state: 'conflict' }, rows: [{ values: { secret: 'must-not-render' } }] } as RunArchiveLiveTableSummary
 try {
  await act(async () => root.render(<LiveTableReportSummary tables={[table]} />))
  expect(container.textContent).toContain('conflict'); expect(container.textContent).toContain('2.50'); expect(container.textContent).toContain('9.00'); expect(container.textContent).toContain('reset after adoption')
  expect(container.textContent).not.toContain('must-not-render')
  await act(async () => root.render(<LiveTableReportSummary tables={[{ ...table, state: 'unavailable' }]} />))
  expect(container.querySelector('td')!.textContent).toBe('Unknown')
 } finally { await act(async () => root.unmount()) }
})

 it.each([
  ['complete', 'write_back', 'Original data pool updated.'],
  ['complete', 'save_as_new', 'Output saved in a separate data pool.'],
  ['conflict', 'write_back', ''],
  ['pending', 'save_as_new', ''],
 ])('describes %s %s finalization accurately', async (state, mode, expected) => {
  const container = document.createElement('div'); const root = createRoot(container)
  try {
   await act(async () => root.render(<LiveTableFinalizationOutcome finalization={{ state, mode, output_pool_id: 'pool' }} />))
   expect(container.textContent?.trim()).toBe(expected)
  } finally { await act(async () => root.unmount()) }
 })

it('explains retained output reconciliation after recovery without exposing a raw status code', async () => {
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<LiveTableFinalizationOutcome finalization={{ state: 'pending', reason: 'reconciliation_required' }} />))
  expect(container.textContent).toContain('Finalization was not recorded.')
  expect(container.textContent).toContain('Review the retained output')
  expect(container.textContent).not.toContain('reconciliation_required')
 } finally { await act(async () => root.unmount()) }
})

it('shows retained waits and recovery gaps separately from target completion', async () => {
 const container = document.createElement('div'); const root = createRoot(container)
 const table = { table_id: 't', alias: 'work', profile: 'native_queue', counts: { redeliveries: 3 }, observations: { version: 1, captured_at: '2026-10-12T12:00:00Z', wait_count: 7, wait_total_ns: 23000000, empty_outcomes: 2, throttled: 4, unknown_outcomes: 1, unknown_pop_attempts: 1, recovery_gaps: 1, incomplete: true, dropped_events: 3, events: [{ kind: 'recovery_observation_gap', count: 1, first: '2026-10-12T12:00:00Z', last: '2026-10-12T12:00:01Z' }] } } as RunArchiveLiveTableSummary
 try {
  await act(async () => root.render(<LiveTableReportSummary tables={[table]} />))
  expect(container.textContent).toContain('7 waits, 23.00 ms total')
  expect(container.textContent).toContain('Lease redeliveries: 3')
  expect(container.textContent).toContain('Observation coverage is incomplete')
  expect(container.textContent).toContain('do not establish whether the target processed')
  expect(container.textContent).toContain('3 older event groups omitted')
  expect(container.querySelector('[aria-label="Shared-data event history"]')?.textContent).toContain('recovery observation gap')
 } finally { await act(async () => root.unmount()) }
})


it('retains archived project attachment identity without presenting global table metrics as run results', async () => {
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<LiveTableReportSummary projectId="project" projectTables={[{ table_id: 'shared', instance_id: 'retired-instance', attachment_id: 'attachment', alias: 'work', group: 'Consumers', state: 'detached', revision: '9007199254740993' }]} />))
  expect(container.textContent).toContain('retired-instance')
  expect(container.textContent).toContain('detached · Recorded revision 9007199254740993')
  expect(container.textContent).toContain('global row counts and operations are not attributed to this run')
  expect(container.textContent).not.toContain('Record reconciliation')
  expect(container.textContent).not.toContain('Table operations')
  expect(container.querySelector('a')?.getAttribute('href')).toBe('#/repository?setting=live-tables&projectId=project&resourceId=shared')
 } finally { await act(async () => root.unmount()) }
})
