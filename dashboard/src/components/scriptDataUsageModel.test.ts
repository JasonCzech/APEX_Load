import { describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import fixture from './catalogActionFormFixture.json'
import { LIVE_TABLE_OPERATIONS, scriptDataUsage, takenVariableNames, uniqueVariableName, withUniqueResultPrefixes } from './scriptDataUsageModel'

const script = (raw: Record<string, unknown>) => ({ name: 's', protocol: 'http', config: {}, actions: [], ...raw }) as unknown as ScriptDefinition
const table = (name: string, params: Record<string, unknown>) => ({ name, type: 'live_table', target: '', params: { alias: 'work', ...params } })

describe('LIVE_TABLE_OPERATIONS', () => {
  it('matches the catalog’s variants', () => {
    const variants = fixture.variants as Array<{ value: string; mutates?: boolean; table_kind?: string }>
    expect(Object.keys(LIVE_TABLE_OPERATIONS).sort()).toEqual(variants.map(variant => variant.value).sort())
    for (const variant of variants) expect(LIVE_TABLE_OPERATIONS[variant.value], variant.value).toEqual({ mutates: Boolean(variant.mutates), ...(variant.table_kind ? { kind: variant.table_kind } : {}) })
  })
})

describe('scriptDataUsage', () => {
  it('finds each alias’s columns, operations, kinds and per-iteration reads', () => {
    const usage = scriptDataUsage(script({
      config: { native_services: { live_tables: [{ alias: 'jobs', operations: ['lease'] }] } },
      data_sources: [{ name: 'cards', type: 'csv', columns: ['number'] }],
      init: [table('Read', { operation: 'read', position: 1, result_prefix: 'row', columns: ['status'] })],
      actions: [
        { name: 'Use', type: 'GET', target: '/o/{{var.row.values.id}}?c={{data.cards.number}}&u={{data.users.email}}' },
        { name: 'If', type: 'control_if', target: '', control: { condition: { left: '{{data.users.tier}}', op: 'exists' }, steps: [table('Update', { operation: 'update', record_id: '{{var.row.record_id}}', expected_version: '1', values: { state: 'done' } })] } },
        { name: 'Native', type: 'native_javascript', target: '', params: { source: "return '{{data.literal.id}}'", inputs: { id: '{{data.users.id}}' } } },
      ],
    }))
    expect(usage.map(item => item.alias)).toEqual(['jobs', 'work', 'cards', 'users'])
    const work = usage.find(item => item.alias === 'work')!
    expect(work).toMatchObject({ columns: ['status', 'state', 'id'], operations: ['read', 'update'], mutates: true, kinds: [], readsEachIteration: false, suppliedByScript: false })
    expect(work.steps.map(step => [step.name, step.position])).toEqual([['Read', { phase: 'init', actionIndex: 0 }], ['Update', { phase: 'action', actionIndex: 1, stepPath: ['then[0]'] }]])
    expect(usage.find(item => item.alias === 'jobs')).toMatchObject({ nativeOperations: ['lease'], mutates: true, kinds: ['native_queue'] })
    expect(usage.find(item => item.alias === 'cards')).toMatchObject({ suppliedByScript: true, readsEachIteration: true, columns: ['number'] })
    // A condition reads too; a native script's own source is literal code.
    expect(usage.find(item => item.alias === 'users')).toMatchObject({ columns: ['email', 'tier', 'id'], readsEachIteration: true, operations: [] })
  })

  it('needs both kinds when leases and column steps share an alias', () => {
    const [usage] = scriptDataUsage(script({ actions: [table('Lease', { operation: 'lease', result_prefix: 'l' }), table('Rotate', { operation: 'rotate' })] }))
    expect(usage.kinds).toEqual(['native_queue', 'vts_columns'])
  })
})

describe('result prefixes', () => {
  const subject = script({
    data_sources: [{ name: 'cards', type: 'csv' }],
    actions: [{ name: 'Login', type: 'POST', target: '/', extractors: [{ name: 'token', type: 'json', expression: '$.t' }] }, table('Read', { operation: 'read', position: 1, result_prefix: 'row' })],
  })

  it('takes what steps save, table prefixes, data sources and aliases', () => {
    expect([...takenVariableNames(subject)].sort()).toEqual(['cards', 'row', 'token', 'work'])
    expect(uniqueVariableName('row', takenVariableNames(subject))).toBe('row_2')
  })

  it('renames a saved block’s clashing prefixes and the references that follow them', () => {
    const block = [
      table('Lease', { operation: 'lease', result_prefix: 'row' }),
      { name: 'Use', type: 'GET', target: '/{{var.row.values.id}}?{{ var.row.status }}&{{var.row_count}}' },
      table('Ack', { operation: 'ack', lease_ref: 'row', result_prefix: 'done' }),
    ] as never
    const taken = takenVariableNames(subject)
    const { actions, renamed } = withUniqueResultPrefixes(block, taken)
    expect(renamed).toEqual({ row: 'row_2' })
    expect(actions[0].params).toMatchObject({ result_prefix: 'row_2' })
    expect(actions[1].target).toBe('/{{var.row_2.values.id}}?{{ var.row_2.status }}&{{var.row_count}}')
    expect(actions[2].params).toMatchObject({ lease_ref: 'row_2', result_prefix: 'done' })
    expect(taken.has('row_2') && taken.has('done')).toBe(true)
    // The block itself is left as saved.
    expect((block as Array<{ params?: { result_prefix?: string } }>)[0].params?.result_prefix).toBe('row')
  })
})
