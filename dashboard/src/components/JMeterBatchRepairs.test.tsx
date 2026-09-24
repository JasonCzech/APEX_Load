// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import JMeterBatchRepairs from './JMeterBatchRepairs'
import type { JMeterImportResult } from '../types/scriptImports'

const repair = vi.hoisted(() => vi.fn())
vi.mock('../hooks/useApi', () => ({ useApi: () => ({ repairJMeterBatch: repair }) }))
it('requires the reviewed operation and replaces the batch only after apply', async () => {
  const result = { batch: { id: 'batch', revision: 'one' }, scripts: [{ name: 'Producer', protocol: 'kafka', config: { jmeter: { source_group_id: 'group' } }, actions: [] }] } as unknown as JMeterImportResult
  const response = { batch: { id: 'batch', revision: 'two' }, preview_digest: 'digest', scripts: result.scripts, before: result.scripts, readiness: {} }
  repair.mockResolvedValue(response)
  const onApplied = vi.fn()
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" onApplied={onApplied} onBusyChange={() => {}} />))
    const buttons = container.querySelectorAll('button')
    expect(buttons[1].disabled).toBe(true)
    await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
    await act(async () => buttons[0].click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ batch_id: 'batch', revision: 'one', operations: [{ type: 'confirm_reference_context', timezone: 'UTC', confirm_no_overrides: true }] }), false)
    expect(onApplied).not.toHaveBeenCalled()
    expect(buttons[1].disabled).toBe(false)
    await act(async () => buttons[1].click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ revision: 'one', preview_digest: 'digest' }), true)
    expect(onApplied).toHaveBeenCalledWith(response)
  } finally { act(() => root.unmount()); container.remove() }
})
it('retains operator selections when a revision conflict occurs', async () => {
  repair.mockRejectedValue(new Error('Batch revision has already been replaced'))
  const result = { batch: { id: 'batch', revision: 'one' }, scripts: [{ name: 'Producer', config: {}, actions: [] }] } as unknown as JMeterImportResult
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" onApplied={() => {}} onBusyChange={() => {}} />))
    await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
    await act(async () => container.querySelector('button')!.click())
    expect(container.querySelector('[role=alert]')?.textContent).toContain('already been replaced')
    expect(container.querySelector<HTMLInputElement>('input[type=checkbox]')!.checked).toBe(true)
  } finally { act(() => root.unmount()); container.remove() }
})
