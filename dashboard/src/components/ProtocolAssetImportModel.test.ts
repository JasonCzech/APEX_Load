import { describe, expect, it } from 'vitest'
import type { SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'
import { carrySourceValueAvailability, filesHaveIdenticalBytes, harShapingApplied } from './ProtocolAssetImportModel'

const requirement = (id: string, extra: Partial<SecretBindingRequirement> = {}): SecretBindingRequirement => ({
  id, path: `/actions/0/${id}`, label: id, context: 'body', suggested_name: id.toUpperCase(), ...extra,
})

describe('carrySourceValueAvailability', () => {
  it('keeps the recorded-value flag on requirements an exclusion kept', () => {
    const previous = [requirement('pw', { source_value_available: true }), requirement('gone', { source_value_available: true }), requirement('manual')]
    const next = [requirement('pw'), requirement('manual'), requirement('new')]
    expect(carrySourceValueAvailability(previous, next, 'session-1').map(item => [item.id, item.source_value_available === true])).toEqual([
      ['pw', true],
      ['manual', false],
      ['new', false],
    ])
  })

  it('claims no recorded value once the source-secret session is gone', () => {
    expect(carrySourceValueAvailability([requirement('pw', { source_value_available: true })], [requirement('pw')], undefined)[0].source_value_available).toBeUndefined()
  })
})

describe('harShapingApplied', () => {
  const script = (actions: ScriptDefinition['actions']): ScriptDefinition => ({ name: 's', version: '1', protocol: 'http', config: { base_url: '' }, actions })

  it('reads step transactions and concurrent groups from the draft', () => {
    const shaped = script([
      { name: 'a', type: 'request', target: '/', metadata: { source_transaction: 'Home' } },
      { name: 'group', type: 'http_sequence', target: '/aura', metadata: { recorded_concurrent_group: 1 }, params: { requests: [] } },
    ])
    expect(harShapingApplied(shaped, [])).toEqual({ stepTransactions: true, recordedConcurrency: true })
    expect(harShapingApplied(script([{ name: 'a', type: 'request', target: '/' }]), [])).toEqual({ stepTransactions: false, recordedConcurrency: false })
  })

  it('does not count a recording that marks its own transactions', () => {
    const recorded = script([{ name: 'a', type: 'request', target: '/', metadata: { source_transaction: 'Recorded' } }])
    expect(harShapingApplied(recorded, ['Step transactions were not added: the recording already marks its transactions, which the script keeps.']).stepTransactions).toBe(false)
  })
})

describe('filesHaveIdenticalBytes', () => {
  it('compares contents, not just sizes, and stops when cancelled', async () => {
    const a = new File(['{"log":1}'], 'a.har')
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":1}'], 'b.har'), () => false)).toBe(true)
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":2}'], 'b.har'), () => false)).toBe(false)
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":10}'], 'b.har'), () => false)).toBe(false)
    expect(await filesHaveIdenticalBytes(a, new File(['{"log":1}'], 'b.har'), () => true)).toBeNull()
  })
})
