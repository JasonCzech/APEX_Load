import { describe, expect, it } from 'vitest'
import { loadChangeImpact, planAfterPauses } from './commandCenterLoadModel'
import type { RunLoadProfilePreview } from './newTestRunModel'
import type { CommandGroup } from './commandCenterScenarioRows'

const group = (name: string, fields: Partial<CommandGroup>) => ({ name, key: name, targetMode: 'vusers', targetVusers: 0, ...fields }) as CommandGroup

describe('planAfterPauses', () => {
  // A 4 m plan: 30 s baseline, 3 m steady, 30 s baseline after.
  const plan = { points: [{ second: 0, total: 2 }, { second: 60, total: 4 }, { second: 240, total: 0 }], totalSeconds: 240, preBaselineSeconds: 30, steadySeconds: 180, postBaselineSeconds: 30, showVUsers: true, peakVUsers: 4 } as RunLoadProfilePreview

  it('goes on with the plan after the time the run spent paused, holding its level through the pause', () => {
    // 90 s into its own clock, after 30 s paused: the step at 60 s already happened; the rest moves.
    expect(planAfterPauses(plan, 90, 30)).toMatchObject({ points: [{ second: 0, total: 2 }, { second: 60, total: 4 }, { second: 270, total: 0 }], totalSeconds: 270 })
    // Paused before the 60 s step: that step comes 30 s later too.
    expect(planAfterPauses(plan, 45, 30).points.map(point => point.second)).toEqual([0, 90, 270])
  })

  it('moves the phases with the plan', () => {
    // Paused in the steady window: it lasts longer, and the baseline after still closes the run.
    expect(planAfterPauses(plan, 90, 30)).toMatchObject({ preBaselineSeconds: 30, steadySeconds: 210, postBaselineSeconds: 30 })
    // Paused in the baseline before it: the steady window starts later.
    expect(planAfterPauses(plan, 10, 30)).toMatchObject({ preBaselineSeconds: 60, steadySeconds: 180, postBaselineSeconds: 30 })
    // Paused in the closing baseline: that baseline holds longer.
    expect(planAfterPauses(plan, 220, 30)).toMatchObject({ preBaselineSeconds: 30, steadySeconds: 180, postBaselineSeconds: 60 })
  })

  it('still ends the plan later for a run whose stop ran past it', () => {
    expect(planAfterPauses(plan, 243, 60)).toMatchObject({ totalSeconds: 300, postBaselineSeconds: 90 })
    expect(planAfterPauses(plan, 243, 60).points.map(point => point.second)).toEqual([0, 60, 300])
  })

  it('leaves a run that never paused as planned', () => {
    expect(planAfterPauses(plan, 90, 0)).toBe(plan)
  })
})

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
