import { describe, expect, it } from 'vitest'

import {
  LOAD_GROUP_MAX_VUSERS,
  maxVUsersForRole,
  validateAggregateVUserDemand,
  validateGroupExecutionEnvelope,
} from './observerGroupModel'

describe('observerGroupModel', () => {
  it('matches backend observer and load-group limits', () => {
    expect(maxVUsersForRole('observer')).toBe(10)
    expect(maxVUsersForRole('load')).toBe(1_000_000)
    expect(validateGroupExecutionEnvelope({ role: 'load', protocol: 'http', vusers: 1_000_000, label: 'Load' })).toBeUndefined()
    expect(validateGroupExecutionEnvelope({ role: 'load', protocol: 'http', vusers: 1_000_001, label: 'Load' })).toContain('1,000,000')
  })

  it('rejects fractional VUsers and fractional stage targets', () => {
    expect(validateGroupExecutionEnvelope({ role: 'load', protocol: 'http', vusers: 1.5, label: 'Load' })).toContain('whole number')
    expect(validateAggregateVUserDemand([
      { vusers: 2, stages: [{ target_vusers: 1.5 }] },
    ])).toContain('stage 1 target VUsers must be a non-negative whole number')
  })

  it('enforces aggregate peak demand and the staged VUser invariant', () => {
    expect(validateAggregateVUserDemand([
      { vusers: 600_000 },
      { vusers: 400_000, stages: [{ target_vusers: 400_000 }] },
    ])).toBeUndefined()
    expect(validateAggregateVUserDemand([
      { vusers: 600_001 },
      { vusers: 400_000, stages: [{ target_vusers: 400_000 }] },
    ])).toContain(`${LOAD_GROUP_MAX_VUSERS.toLocaleString()} VUsers`)
    expect(validateAggregateVUserDemand([
      { vusers: 5, stages: [{ target_vusers: 10 }] },
    ], 'Scenario')).toBe('Scenario: group 1 VUsers must match its peak stage target of 10.')
  })
})
