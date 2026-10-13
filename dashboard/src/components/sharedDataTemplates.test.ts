import { describe, expect, it } from 'vitest'

import type { DslCatalogVariant } from './dslActionCatalog'
import fixture from './catalogActionFormFixture.json'
import { linkedTemplateActions, sharedDataAction, templateStepNames, type SharedDataContext } from './sharedDataTemplates'

const variants = fixture.variants as unknown as DslCatalogVariant[]
const variant = (value: string) => variants.find(item => item.value === value)!
const context = (overrides: Partial<SharedDataContext> = {}): SharedDataContext => ({
  alias: 'orders', columns: ['id', 'state'], taken: new Set(['row']), names: new Set(['read a row from orders']),
  earlier: { record_id: [], record_version: [], lease: [] }, variables: new Set(['id']), ...overrides,
})

describe('sharedDataAction', () => {
  it('reads the first row without changing it, under a prefix and name not yet used', () => {
    const shared = context()
    const action = sharedDataAction(variant('read'), 'Read a row from orders', shared)
    expect(action).toEqual({ name: 'Read a row from orders (2)', type: 'live_table', target: '', params: { alias: 'orders', operation: 'read', result_prefix: 'row_2', position: 1, on_empty: 'error' } })
    expect(shared.taken.has('row_2')).toBe(true)
  })

  it('points a change at the nearest earlier read, and a lease step at the nearest lease', () => {
    const earlier = { record_id: ['{{var.a.record_id}}', '{{var.b.record_id}}'], record_version: ['{{var.a.record_version}}', '{{var.b.record_version}}'], lease: ['job'] }
    expect(sharedDataAction(variant('update'), 'Update', context({ earlier })).params).toMatchObject({ record_id: '{{var.b.record_id}}', expected_version: '{{var.b.record_version}}', on_condition: 'error' })
    expect(sharedDataAction(variant('increment'), 'Count', context()).params).toMatchObject({ position: 1, delta: '1' })
    expect(sharedDataAction(variant('ack'), 'Ack', context({ earlier })).params).toMatchObject({ lease_ref: 'job' })
    // A new row's values come from earlier variables of the same name.
    expect(sharedDataAction(variant('push'), 'Add', context()).params!.values).toEqual({ id: '{{var.id}}', state: '' })
  })
})

describe('linkedTemplateActions', () => {
  it('wires a read and update through the read’s outputs, updating only when the read got a row', () => {
    const result = linkedTemplateActions('read_update', variants, context({ taken: new Set() }))!
    expect(templateStepNames(result.actions)).toEqual(['Read a row from orders (2)', 'When the row was read', 'Update the row in orders'])
    expect(result.spot).toEqual({ name: 'Update the row in orders', position: 'before' })
    expect(result.actions[1].control!.condition).toEqual({ left: '{{var.row.status}}', op: 'eq', right: 'ok' })
    expect(result.actions[1].control!.steps![0].params).toMatchObject({ operation: 'update', record_id: '{{var.row.record_id}}', expected_version: '{{var.row.record_version}}', values: { id: '{{var.row.values.id}}', state: '{{var.row.values.state}}' } })
  })

  it('acknowledges the lease it took', () => {
    const result = linkedTemplateActions('lease_ack', variants, context())!
    const [lease, guard] = result.actions
    expect(lease.params).toMatchObject({ operation: 'lease', result_prefix: 'leased', on_empty: 'error' })
    // Under continue on error a failed lease lets the iteration go on: the acknowledgment needs the lease.
    expect(guard).toMatchObject({ name: 'When a row was leased', type: 'control_if', control: { condition: { left: '{{var.leased.status}}', op: 'eq', right: 'ok' } } })
    expect(guard.control!.steps!.map(step => [step.params?.operation, step.params?.result_prefix, step.params?.lease_ref])).toEqual([['ack', 'acknowledged', 'leased']])
    expect(result.spot).toEqual({ name: 'Acknowledge the leased row', position: 'before' })
  })

  it('takes a row and publishes one, and needs every operation in the catalog', () => {
    expect(linkedTemplateActions('take_process', variants, context())).toMatchObject({ actions: [{ params: { operation: 'pop', on_empty: 'error' } }], spot: { name: 'Take the next row from orders', position: 'after' } })
    expect(linkedTemplateActions('publish', variants, context())!.actions[0].params).toMatchObject({ operation: 'push', values: { id: '{{var.id}}', state: '' } })
    expect(linkedTemplateActions('lease_ack', variants.filter(item => item.value !== 'ack'), context())).toBeNull()
  })
})
