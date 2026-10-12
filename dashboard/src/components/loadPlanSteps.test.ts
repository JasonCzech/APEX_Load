import { describe, expect, it } from 'vitest'
import { engineLevelAtDeadline, engineRampDownLevel, engineRampDownSeconds, engineRampUpLevel, engineRampUpSeconds, loadTimeTicks, loadValueTicks, MAX_RAMP_STEPS, plannedVUsersAt } from './loadPlanSteps'
import { buildRunLoadProfilePreview } from './newTestRunModel'

describe('the engine load staircase', () => {
  it('starts VUsers one at a time through the ramp-up, the first at once', () => {
    // 5 VUsers over 2 m: one every 24 s, all running from 96 s.
    expect([0, 23.9, 24, 48, 95.9, 96, 120].map(second => engineRampUpLevel(5, 120, second))).toEqual([1, 1, 2, 3, 4, 5, 5])
    expect(engineRampUpLevel(5, 120, -1)).toBe(0)
    // Without a ramp, or with one VUser, everything starts at once.
    expect(engineRampUpLevel(5, 0, 0)).toBe(5)
    expect(engineRampUpLevel(1, 120, 0)).toBe(1)
    // A step time that divides inexactly still counts its own VUser.
    expect(engineRampUpLevel(7, 120, 3 * (120 / 7))).toBe(4)
    expect(engineRampUpSeconds(5, 120)).toEqual([0, 24, 48, 72, 96])
    expect(engineRampUpSeconds(1, 120)).toEqual([])
  })

  it('stops at the deadline with the VUsers started before it', () => {
    expect(engineLevelAtDeadline(5, 120, 60)).toBe(3)
    expect(engineLevelAtDeadline(5, 120, 48)).toBe(2)
    expect(engineLevelAtDeadline(5, 120, 300)).toBe(5)
    expect(engineLevelAtDeadline(5, 120, 0)).toBe(0)
    expect(engineLevelAtDeadline(5, 0, 1)).toBe(5)
  })

  it('stops running VUsers one at a time through the ramp-down', () => {
    // 4 running over 60 s: one stops every 15 s, the last at 60 s.
    expect([0, 14.9, 15, 45, 60, 90].map(second => engineRampDownLevel(4, 60, second))).toEqual([4, 4, 3, 1, 0, 0])
    expect(engineRampDownLevel(4, 0, 0)).toBe(0)
    expect(engineRampDownSeconds(4, 60)).toEqual([15, 30, 45, 60])
  })

  it('draws a long ramp at a bounded resolution that still ends on its last step', () => {
    const starts = engineRampUpSeconds(10_000, 1000)
    expect(starts.length).toBeLessThanOrEqual(MAX_RAMP_STEPS + 1)
    expect(starts[0]).toBe(0)
    expect(starts[starts.length - 1]).toBeCloseTo(999.9, 6)
  })

  it('plans review run 1 as the engine ran it', () => {
    // 3 groups of 5 VUsers, 2 m ramp, 5 m: 3, 6, 9, 12, 15 VUsers 24 s apart.
    const groups = ['Browse', 'Cart', 'Checkout'].map((name, order) => ({ order, name, script_id: name, vusers: 5, ramp_up: '2m' }))
    const plan = buildRunLoadProfilePreview(groups, '5m', undefined, false)
    expect([0, 24, 48, 72, 96, 299].map(second => plannedVUsersAt(plan, second))).toEqual([3, 6, 9, 12, 15, 15])
    expect(plannedVUsersAt(plan, 300)).toBe(0)
    expect(plan.peakVUsers).toBe(15)
  })

  it('plans a staged ramp as the controller steps it: one interval, then a step', () => {
    // The builder saves review run 1 as stages: ramp up to 5 over 2 m, hold 3 m.
    // The run reached 3, 6, 9, 12 and 15 VUsers 24 s apart, the first 24 s in.
    const stages = [{ type: 'ramp_up', duration: '2m', target_vusers: 5 }, { type: 'hold', duration: '3m', target_vusers: 5 }]
    const groups = ['Browse', 'Cart', 'Checkout'].map((name, order) => ({ order, name, script_id: name, vusers: 5, ramp_up: '2m', stages }))
    const plan = buildRunLoadProfilePreview(groups, '5m', undefined, false)
    expect([0, 23, 24, 48, 72, 96, 119, 120, 299].map(second => plannedVUsersAt(plan, second))).toEqual([0, 0, 3, 6, 9, 12, 12, 15, 15])
  })

  it('ramps a group down from the level it reached by the deadline', () => {
    const plan = buildRunLoadProfilePreview([{ order: 0, name: 'Short', script_id: 's', vusers: 10, ramp_up: '100s', ramp_down: '20s' }], '50s', undefined, false)
    // Five VUsers started before the 50 s deadline (one every 10 s); one stops every 4 s.
    expect(plannedVUsersAt(plan, 49)).toBe(5)
    expect(plannedVUsersAt(plan, 50)).toBe(5)
    expect(plannedVUsersAt(plan, 54)).toBe(4)
    expect(plannedVUsersAt(plan, 70)).toBe(0)
    expect(plan.totalSeconds).toBe(70)
  })
})

describe('load chart ticks', () => {
  it('labels elapsed time on round seconds, minutes and hours', () => {
    expect(loadTimeTicks(300)).toEqual([0, 60, 120, 180, 240, 300])
    expect(loadTimeTicks(96)).toEqual([0, 30, 60, 90])
    expect(loadTimeTicks(3600)).toEqual([0, 900, 1800, 2700, 3600])
    expect(loadTimeTicks(0)).toEqual([0])
  })

  it('labels VUsers on round values that reach the peak', () => {
    expect(loadValueTicks(15)).toEqual([0, 5, 10, 15])
    expect(loadValueTicks(8)).toEqual([0, 2, 4, 6, 8])
    expect(loadValueTicks(7)).toEqual([0, 2, 4, 6, 8])
    expect(loadValueTicks(1000)).toEqual([0, 250, 500, 750, 1000])
    expect(loadValueTicks(0)).toEqual([0, 1])
  })
})
