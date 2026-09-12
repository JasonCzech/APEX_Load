import { describe, expect, it } from 'vitest'
import type { ScenarioDataPoolBinding, StoredScript } from '../types'
import { dataBindingErrors, scenarioDataRequirements } from './scenarioDataBindingModel'
const binding = (alias: string, pool_id = 'pool-1'): ScenarioDataPoolBinding => ({ alias, pool_id, access_mode: 'read_only', selection_strategy: 'sequential' })
describe('scenario data bindings', () => {
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
})
