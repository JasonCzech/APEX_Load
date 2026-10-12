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
