import { describe, expect, it } from 'vitest'
import type { ScenarioDataPoolBinding, StoredScript } from '../types'
import { aliasForPool, bindingColumns, bindingForNeed, dataBindingErrors, dataBindingMismatches, scenarioDataNeeds, scenarioDataRequirements, tableChoicePatch, touchedDataBindingErrors } from './scenarioDataBindingModel'
const binding = (alias: string, pool_id = 'pool-1'): ScenarioDataPoolBinding => ({ alias, pool_id, access_mode: 'read_only', selection_strategy: 'sequential' })
describe('scenario data bindings', () => {
  it('keeps project bindings explicit and rejects run-specific seed/output settings', () => {
    const project: ScenarioDataPoolBinding = { alias: 'work', pool_id: '', scope: 'project', project_table_id: 'project-table', access_mode: 'live', selection_strategy: 'manual', live_options: { empty_policy: 'wait', wait_ms: 10000 } }
    expect(dataBindingErrors([project])).toEqual([])
    expect(dataBindingErrors([{ ...project, pool_id: 'pool', live_options: { seed_policy: 'empty', end_mode: 'discard' } }])).toEqual(expect.arrayContaining([expect.stringContaining('cannot also select'), expect.stringContaining('own schema')]))
    expect(dataBindingErrors([{ ...project, project_table_id: '' }])).toContain('Binding 1: select a project live table.')
    expect(dataBindingErrors([{ ...project, access_mode: 'read_only' }])).toContain('Binding 1: project tables require live access.')
  })
  it('includes explicitly declared native-service aliases without guessing from guest code', () => {
    const scripts = [{ script: { config: { native_services: { live_tables: [{ alias: 'work', operations: ['push'] }] } } } }] as unknown as StoredScript[]
    expect([...scenarioDataRequirements(scripts).keys()]).toEqual(['work'])
  })
  it('treats native JavaScript source as literal while finding resolved input references', () => {
    const scripts = [{ script: { actions: [{ type: 'native_javascript', params: { source: "return {text: '{{data.literal.id}}'}", inputs: { id: '{{data.actual.id}}', example: { type: 'live_table', params: { alias: 'not-an-action' } }, nested: { type: 'native_javascript', params: { source: '{{data.actual.nested}}' } } } } }] } }] as unknown as StoredScript[]
    expect([...scenarioDataRequirements(scripts)]).toEqual([['actual', new Set(['id', 'nested'])]])
  })
  it('distinguishes nested live actions from action-shaped request and data values', () => {
    const scripts = [{ script: {
      data_sources: [{ name: 'customers', values: [{ type: 'live_table', params: { alias: 'fake_seed', values: { missing: 'x' } } }] }],
      init: [{ type: 'live_table', params: { alias: 'seeded', columns: ['id'] } }],
      actions: [{ type: 'control_if', control: { steps: [{ type: 'control_parallel', control: { branches: [{ steps: [{ type: 'live_table', params: { alias: 'nested', values: { state: 'ready' } } }] }] } }], else: [{ type: 'GET', params: { payload: { type: 'live_table', params: { alias: 'fake_request' } }, literal: { type: 'native_javascript', params: { source: '{{data.customers.email}}' } } } }] } }],
      teardown: [{ type: 'native_javascript', params: { source: "return '{{data.literal.id}}';", inputs: { value: '{{data.customers.id}}' } } }],
    } }] as unknown as StoredScript[]
    const result = scenarioDataRequirements(scripts)
    expect(result.get('seeded')).toEqual(new Set(['id']))
    expect(result.get('nested')).toEqual(new Set(['state']))
    expect(result.get('customers')).toEqual(new Set(['email', 'id']))
    for (const alias of ['fake_seed', 'fake_request', 'literal']) expect(result.has(alias)).toBe(false)
  })
  it('validates every binding and rejects ambiguous aliases', () => {
    expect(dataBindingErrors([binding('customers'), binding('orders', 'pool-2')])).toEqual([])
    expect(dataBindingErrors([binding('customers'), binding('customers', 'pool-2')])).toEqual([expect.stringContaining('Duplicate')])
    expect(dataBindingErrors([binding('bad.alias', '')])).toHaveLength(2)
  })
  it('finds nested script references and deduplicates required columns', () => {
    const scripts = [{ script: { actions: [{ url: '{{data.customers.email}}', nested: { token: '{{data.customers.email}}', value: '{{data.orders.id}}' } }] } }] as unknown as StoredScript[]
    const result = scenarioDataRequirements(scripts)
    expect([...result.get('customers')!]).toEqual(['email'])
    expect([...result.get('orders')!]).toEqual(['id'])
    expect(scenarioDataRequirements([]).size).toBe(0)
  })
  it('derives unique aliases from pool names', () => {
    expect(aliasForPool('Customer accounts', [])).toBe('customer_accounts')
    expect(aliasForPool('Customer accounts', ['CUSTOMER_ACCOUNTS', 'customer_accounts_2'])).toBe('customer_accounts_3')
    expect(aliasForPool('2026 Q4 — leads!', [])).toBe('_2026_q4_leads')
    expect(aliasForPool('—', [])).toBe('data')
  })
  it('reports untouched bindings only once something is chosen', () => {
    const untouched: ScenarioDataPoolBinding = { alias: '', pool_id: '', access_mode: 'read_only', selection_strategy: 'sequential' }
    expect(dataBindingErrors([binding('a'), untouched, untouched]).length).toBeGreaterThan(0)
    expect(touchedDataBindingErrors([binding('a'), untouched, untouched])).toEqual([])
    expect(touchedDataBindingErrors([binding('a'), { ...untouched, pool_id: 'pool-1' }])).toEqual(['Binding 2: use a letter or underscore followed by letters, numbers, underscores or hyphens for the alias.'])
  })
})

describe('what the scripts need from bindings', () => {
  const live = (name: string, params: Record<string, unknown>) => ({ name, type: 'live_table', target: '', params })
  const scripts = [
    { id: 'a', name: 'Producer', script: { actions: [live('Add', { alias: 'work', operation: 'push', values: { id: '1' } })] } },
    { id: 'b', name: 'Worker', script: { data_sources: [{ name: 'cards', type: 'csv' }], actions: [live('Lease', { alias: 'work', operation: 'lease', result_prefix: 'l' }), { name: 'Use', type: 'GET', target: '/{{data.cards.n}}/{{data.users.email}}' }] } },
  ] as unknown as StoredScript[]
  const needs = scenarioDataNeeds(scripts)

  it('merges an alias across scripts, leaving out what a script’s own source supplies', () => {
    expect(needs.map(need => [need.alias, need.scripts, need.operations, need.kinds, need.readsEachIteration])).toEqual([
      ['work', ['Producer', 'Worker'], ['push', 'lease'], ['native_queue'], false],
      ['users', ['Worker'], [], [], true],
    ])
  })

  it('binds a pool read as a snapshot, and an alias with table steps as a live table for this run', () => {
    expect(bindingForNeed(needs[1])).toEqual({ pool_id: '', alias: 'users', access_mode: 'read_only', selection_strategy: 'sequential' })
    // Leasing needs rows to start from: the table starts from a pool still to be chosen.
    expect(bindingForNeed(needs[0])).toMatchObject({ alias: 'work', access_mode: 'live', scope: 'run', selection_strategy: 'manual', live_options: { profile: 'native_queue', seed_policy: 'current_at_start', columns: [] } })
    // Scripts that only add rows get an empty table with the columns they write.
    expect(bindingForNeed({ ...needs[0], operations: ['push'] })).toMatchObject({ live_options: { seed_policy: 'empty', columns: ['id'] } })
    // A live table for per-iteration reads hands out the next row; one from a pool starts from it.
    expect(tableChoicePatch({ ...binding('users'), pool_id: 'p' }, 'run', ['email'], needs[1])).toMatchObject({ selection_strategy: 'next', live_options: { seed_policy: 'current_at_start', columns: ['email'] } })
    expect(tableChoicePatch(binding('users'), 'project', [], needs[1])).toMatchObject({ scope: 'project', pool_id: '', selection_strategy: 'next' })
    // Scripts that read a row each iteration and also run table steps get both.
    expect(bindingForNeed({ ...needs[0], readsEachIteration: true })).toMatchObject({ access_mode: 'live', selection_strategy: 'next' })
  })

  it('flags table steps on a snapshot, the wrong table kind, missing columns and no per-iteration row', () => {
    const snapshot = binding('work')
    expect(dataBindingMismatches(snapshot, needs[0], { columns: ['id'] })).toEqual(['Producer and Worker change rows in work with table steps, which a read-only snapshot cannot run. Choose a live table.'])
    const shared = { ...snapshot, ...tableChoicePatch(snapshot, 'run', ['id'], needs[0]), live_options: { profile: 'vts_columns' as const, seed_policy: 'empty' as const, columns: ['key'] } }
    // A table started from the pool has the pool's columns as well as those declared.
    expect(bindingColumns({ ...shared, live_options: { ...shared.live_options, seed_policy: 'current_at_start' } }, ['id'])).toEqual(['key', 'id'])
    expect(dataBindingMismatches(shared, needs[0], { columns: bindingColumns(shared, ['id']) })).toEqual([
      'Producer and Worker lease rows from work, which needs a work queue table; this one is shared columns.',
      'work has no column id, which Producer and Worker use.',
    ])
    const manual = { ...binding('users'), ...tableChoicePatch(binding('users'), 'project', [], undefined) }
    expect(dataBindingMismatches(manual, needs[1], { columns: ['email'], kind: 'native_queue' })).toEqual(['Worker reads {{data.users.…}} every iteration, but this table hands out no row then. Under At each iteration, choose to read, remove or lease the next row.'])
    expect(dataBindingMismatches(binding('users'), undefined, { columns: [] })).toEqual([])
  })
})
