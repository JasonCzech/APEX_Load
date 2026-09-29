import { describe, expect, it } from 'vitest'

import { validateGeneratorAssignment } from './generatorAssignmentValidation'

describe('validateGeneratorAssignment', () => {
  it('requires selected hosts at both scopes', () => {
    expect(validateGeneratorAssignment({ mode: 'specific_hosts', selected_targets: [] }, 'scenario', 'Scenario assignment')).toContain('select at least one')
    expect(validateGeneratorAssignment({ mode: 'specific_hosts', selected_targets: ['  '] }, 'group', 'Checkout')).toContain('select at least one')
  })

  it('requires non-empty positive integer weights', () => {
    expect(validateGeneratorAssignment({ mode: 'region_weighted', region_weights: {} }, 'scenario', 'Scenario assignment')).toContain('add at least one region weight')
    expect(validateGeneratorAssignment({ mode: 'region_weighted', region_weights: { east: 1.5 } }, 'scenario', 'Scenario assignment')).toContain('positive whole numbers')
    expect(validateGeneratorAssignment({ mode: 'load_zone_weighted', load_zone_weights: { blue: 1 } }, 'scenario', 'Scenario assignment')).toBeUndefined()
  })

  it('rejects root-only placement settings at group scope', () => {
    expect(validateGeneratorAssignment({ mode: 'region_weighted', region_weights: { east: 1 } }, 'group', 'Checkout')).toContain('only at scenario scope')
    expect(validateGeneratorAssignment({ mode: 'auto', tolerate_generator_loss: true }, 'group', 'Checkout')).toContain('only at scenario scope')
    expect(validateGeneratorAssignment({ mode: 'auto', failover_policy: 'continue' }, 'group', 'Checkout')).toContain('only at scenario scope')
  })

  it('allows continuing through controller failover together with generator-loss tolerance', () => {
    expect(validateGeneratorAssignment({ mode: 'auto', failover_policy: 'continue', tolerate_generator_loss: true }, 'scenario', 'Scenario')).toBeUndefined()
    expect(validateGeneratorAssignment({ mode: 'auto', failover_policy: 'continue' }, 'scenario', 'Scenario')).toBeUndefined()
  })
  it('validates the scenario spare envelope without silently discarding invalid values', () => {
    for (const budget of [-1, 0.5, NaN, Infinity, 1000001]) {
      expect(validateGeneratorAssignment({ mode: 'auto', tolerate_generator_loss: true, recovery_spare_vusers: budget }, 'scenario', 'Scenario')).toContain('whole number')
    }
    expect(validateGeneratorAssignment({ mode: 'auto', recovery_spare_vusers: 100 }, 'scenario', 'Scenario')).toContain('generator-loss tolerance')
    expect(validateGeneratorAssignment({ mode: 'auto', tolerate_generator_loss: true, recovery_spare_vusers: 100 }, 'group', 'Group')).toContain('scenario scope')
    expect(validateGeneratorAssignment({ mode: 'auto', tolerate_generator_loss: true, recovery_spare_vusers: 100 }, 'scenario', 'Scenario')).toBeUndefined()
  })

})

it('validates Java profile scope and bounded deadlines', () => {
  const java_host = { profile_id: 'approved', preparation_ms: 120000, run_lifetime_ms: 3600000 }
  expect(validateGeneratorAssignment({ java_host }, 'group', 'Java')).toBeUndefined()
  expect(validateGeneratorAssignment({ java_host }, 'scenario', 'Java')).toContain('per group')
  expect(validateGeneratorAssignment({ java_host: { ...java_host, preparation_ms: 0 } }, 'group', 'Java')).toContain('positive')
  expect(validateGeneratorAssignment({ java_host: { ...java_host, run_lifetime_ms: 86400001 } }, 'group', 'Java')).toContain('24 hours')
})
