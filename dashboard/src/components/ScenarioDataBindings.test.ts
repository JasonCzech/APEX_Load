import { describe, expect, it } from 'vitest'
import type { ScenarioDataPoolBinding, StoredScript } from '../types'
import { aliasForPool, dataBindingErrors, scenarioDataRequirements, touchedDataBindingErrors } from './scenarioDataBindingModel'
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

