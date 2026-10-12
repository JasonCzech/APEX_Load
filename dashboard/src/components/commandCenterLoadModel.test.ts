import { describe, expect, it } from 'vitest'
import { loadChangeImpact } from './commandCenterLoadModel'
import type { CommandGroup } from './commandCenterScenarioRows'

const group = (name: string, fields: Partial<CommandGroup>) => ({ name, key: name, targetMode: 'vusers', targetVusers: 0, ...fields }) as CommandGroup

describe('loadChangeImpact', () => {
  it("says a group's applied VUsers, the new count, and the run's total before and after", () => {
    const groups = [group('Store_Browse', { targetVusers: 2, target: 5 }), group('Store_Cart', { targetVusers: 4, target: 9 })]
    // A staged, unapplied draft on another group does not count toward the total.
    expect(loadChangeImpact(groups, 'Store_Browse', 5)).toBe('Store_Browse 2 → 5 VUsers · run total 6 → 9 VUsers')
    expect(loadChangeImpact(groups, 'Store_Cart', 0, '30s')).toBe('Store_Cart 4 → 0 VUsers over 30s · run total 6 → 2 VUsers')
  })

  it("names a single group's change alone, and a goal run's in TPS", () => {
    expect(loadChangeImpact([group('Checkout', { targetVusers: 1200 })], 'Checkout', 1500)).toBe('Checkout 1,200 → 1,500 VUsers')
    const goal = [group('Checkout', { targetMode: 'tps', committedTargetTps: 90 }), group('Search', { targetMode: 'tps', committedTargetTps: 30.5 })]
    expect(loadChangeImpact(goal, 'Checkout', 120.25)).toBe('Checkout 90 → 120.3 TPS · run goal 120.5 → 150.8 TPS')
  })
})
