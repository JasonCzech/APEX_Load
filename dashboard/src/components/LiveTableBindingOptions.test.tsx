// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import LiveTableBindingOptions from './LiveTableBindingOptions'
import { dataBindingErrors, scenarioDataRequirements } from './scenarioDataBindingModel'
import type { ScenarioDataPoolBinding, StoredScript } from '../types'
let container: HTMLDivElement; let root: Root
const binding: ScenarioDataPoolBinding = { pool_id: 'pool', alias: 'work', access_mode: 'live', selection_strategy: 'manual' }
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
function select(label: string) { return [...container.querySelectorAll('label')].find(node => node.querySelector('span')?.textContent === label)!.querySelector('select')! }
async function change(label: string, value: string) { await act(async () => { const node = select(label); node.value = value; node.dispatchEvent(new Event('change', { bubbles: true })) }) }
it('defaults to native memory discard, requires output permission, and supplies an actual bounded wait', async () => {
  const update = vi.fn(); await act(async () => root.render(<LiveTableBindingOptions binding={binding} canManage={false} onChange={update} />))
  expect(select('Table behavior').value).toBe('native_queue'); expect(select('Recovery').value).toBe('memory'); expect(select('When the run ends').value).toBe('discard')
  expect([...select('When the run ends').options].find(option => option.value === 'write_back')!.disabled).toBe(true)
  expect([...container.querySelectorAll('label')].find(node => node.querySelector('span')?.textContent === 'Maximum table size (bytes)')!.querySelector('input')!.max).toBe('8388608')
  await change('When no row is available', 'wait')
  expect(update).toHaveBeenLastCalledWith(expect.objectContaining({ live_options: expect.objectContaining({ wait_ms: 10000, operation_timeout_ms: 10000 }) }))
})
it('removes unsupported native-only choices when changing profile', async () => {
  const update = vi.fn(); await act(async () => root.render(<LiveTableBindingOptions binding={{ ...binding, selection_strategy: 'lease', live_options: { end_mode: 'write_back' } }} canManage onChange={update} />))
  await change('Table behavior', 'vts_columns')
  expect(update).toHaveBeenCalledWith(expect.objectContaining({ selection_strategy: 'manual', live_options: expect.objectContaining({ profile: 'vts_columns', end_mode: 'discard' }) }))
})
it('validates empty seed, profile and save policies without changing existing read-only validation', () => {
  expect(dataBindingErrors([{ ...binding, access_mode: 'read_only', selection_strategy: 'unique' }])).toEqual([])
  expect(dataBindingErrors([{ ...binding, pool_id: '', live_options: { seed_policy: 'empty', columns: ['id'] } }])).toEqual([])
  expect(dataBindingErrors([{ ...binding, pool_id: '', live_options: { seed_policy: 'empty' } }]).join()).toContain('declared columns')
  expect(dataBindingErrors([{ ...binding, selection_strategy: 'lease', live_options: { profile: 'vts_columns' } }]).join()).toContain('native work queue')
  expect(dataBindingErrors([{ ...binding, live_options: { end_mode: 'save_as_new' } }]).join()).toContain('enter a name')
})
it('includes nested live action aliases and columns in scenario requirements', () => {
  const script = { script: { init: [{ type: 'control_parallel', control: { branches: [{ name: 'producer', steps: [{ type: 'live_table', params: { alias: 'work', operation: 'push', values: { id: '1' } } }] }] } }], teardown: [{ type: 'live_table', params: { alias: 'empty', operation: 'pop' } }] } } as unknown as StoredScript
  const requirements = scenarioDataRequirements([script]); expect([...requirements.get('work')!]).toEqual(['id']); expect(requirements.has('empty')).toBe(true)
})

it('preserves source column names with spaces while matching runtime column bounds', () => {
  const columns = (names: string[]) => dataBindingErrors([{ ...binding, live_options: { columns: names } }])
  expect(columns(['Customer ID', 'First name', '注文番号'])).toEqual([])
  for (const names of [['id', ' id '], [''], ['line\nbreak'], ['a'.repeat(129)], ['é'.repeat(65)], Array.from({ length: 129 }, (_, index) => `c${index}`)]) {
    expect(columns(names).join()).toContain('column names')
  }
  expect(columns(['é'.repeat(64)])).toEqual([])
  expect(dataBindingErrors([{ ...binding, alias: 'a'.repeat(65) }]).join()).toContain('at most 64 characters')
})

it('authors a prior-run seed explicitly without requiring a current pool', async () => {
 const update = vi.fn()
 await act(async () => root.render(<LiveTableBindingOptions binding={{ ...binding, pool_id: '', live_options: { seed_policy: 'original_seed', seed_run_id: 'prior-run' } }} canManage onChange={update} />))
 expect(select('Starting data').value).toBe('original_seed')
 expect([...select('When the run ends').options].find(option => option.value === 'write_back')!.disabled).toBe(true)
 expect(container.querySelector('input[maxlength="128"]')?.getAttribute('value')).toBe('prior-run')
 expect(dataBindingErrors([{ ...binding, pool_id: '', live_options: { seed_policy: 'original_seed', seed_run_id: 'prior-run' } }])).toEqual([])
 expect(dataBindingErrors([{ ...binding, pool_id: '', live_options: { seed_policy: 'original_seed' } }]).join()).toContain('source run ID')
 await change('Starting data', 'empty')
 expect(update).toHaveBeenCalledWith(expect.objectContaining({ live_options: expect.objectContaining({ seed_policy: 'empty', seed_run_id: undefined }) }))
})
