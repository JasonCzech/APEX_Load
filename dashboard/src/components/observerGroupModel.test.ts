import { describe, expect, it } from 'vitest'

import {
  clampVUsersForRole,
  LOAD_GROUP_MAX_VUSERS,
  maxSharedVUsersForRoles,
  maxVUsersForRole,
  OBSERVER_MAX_VUSERS,
  validateAggregateVUserDemand,
  validateGroupExecutionEnvelope,
} from './observerGroupModel'

describe('observerGroupModel', () => {
  it('caps a shared profile at the most restrictive group role', () => {
    expect(maxSharedVUsersForRoles(['load', 'observer'])).toBe(OBSERVER_MAX_VUSERS)
    expect(maxSharedVUsersForRoles(['load', 'load'])).toBe(LOAD_GROUP_MAX_VUSERS)
  })

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

  // clampVUsersForRole is the silent half of the VUser envelope: it rewrites the
  // operator's number instead of reporting an error, so its ceilings, floor and
  // non-finite handling need to be pinned directly rather than inferred from the
  // validators above.
  it('clamps VUser input to the role ceiling and the requested floor', () => {
    expect(clampVUsersForRole(LOAD_GROUP_MAX_VUSERS + 1, 'load')).toBe(LOAD_GROUP_MAX_VUSERS)
    expect(clampVUsersForRole(LOAD_GROUP_MAX_VUSERS, 'load')).toBe(LOAD_GROUP_MAX_VUSERS)
    expect(clampVUsersForRole(OBSERVER_MAX_VUSERS + 1, 'observer')).toBe(OBSERVER_MAX_VUSERS)
    // An unset role is a load group, not an observer canary.
    expect(clampVUsersForRole(5_000, undefined)).toBe(5_000)
    expect(clampVUsersForRole(-1, 'load')).toBe(0)
    expect(clampVUsersForRole(-1, 'load', 1)).toBe(1)
    expect(clampVUsersForRole(12.9, 'load')).toBe(12)
    expect(clampVUsersForRole(Number.NaN, 'load')).toBe(0)
    expect(clampVUsersForRole(Number.NaN, 'load', 1)).toBe(1)
    expect(clampVUsersForRole(Number.POSITIVE_INFINITY, 'observer')).toBe(0)
  })
})
