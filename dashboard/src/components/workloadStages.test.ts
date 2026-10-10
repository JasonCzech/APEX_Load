import { describe, expect, it } from 'vitest'

import type { LoadStageDraft } from '../types'
import { buildRunLoadProfilePreview } from './newTestRunModel'
import { buildScenarioModelPreview } from './scenarioWizardUtils'
import { initialStageLevel, maxStageVUsers, stageBoundarySeconds, stageLevelAt, stageScheduleSeconds } from './workloadStages'

const saved = { emptyDurationSeconds: 0, missingTarget: 'zero' as const }

describe('shared load stage semantics (AUTH-3)', () => {
  const stages: LoadStageDraft[] = [
    { type: 'ramp_up', duration: '60s', target_vusers: 30 },
    { type: 'hold', duration: '60s', target_vusers: 30 },
    { type: 'Hold ', duration: '30s', target_vusers: 10 },
  ]

  it('gives a stage [start, end): at its end the next target already applies', () => {
    const at = (second: number) => stageLevelAt(stages, second, 'target_vusers', { ...saved, initialLevel: 0 })
    expect(at(30)).toBe(15)
    expect(at(60)).toBe(30)
    // The controller applies the third stage's hold at 120 s (types are trimmed and lower-cased).
    expect(at(119)).toBe(30)
    expect(at(120)).toBe(10)
    // After the last stage the last level holds.
    expect(at(500)).toBe(10)
    expect(stageBoundarySeconds(stages)).toEqual([60, 120, 150])
    expect(stageScheduleSeconds(stages)).toBe(150)
  })

  it('starts a schedule from the level the group is at', () => {
    expect(initialStageLevel([{ type: 'ramp_down', duration: '1m', target_vusers: 5 }], 40)).toBe(40)
    expect(initialStageLevel([{ type: 'hold', duration: '1m', target_vusers: 12 }], 40)).toBe(12)
    expect(initialStageLevel([{ type: 'ramp_up', duration: '1m', target_vusers: 50 }], 40)).toBe(0)
    expect(initialStageLevel([{ type: 'ramp_down', duration: '1m', target_tps: 5 }], 40, 'target_tps')).toBe(0)
    const rampDown: LoadStageDraft[] = [{ type: 'ramp_down', duration: '100s', target_vusers: 0 }]
    expect(stageLevelAt(rampDown, 25, 'target_vusers', { ...saved, initialLevel: initialStageLevel(rampDown, 40) })).toBe(30)
  })

  it('reads a missing target and an empty duration as the caller asks', () => {
    const tpsOnly: LoadStageDraft[] = [{ type: 'hold', duration: '1m', target_vusers: 8 }, { type: 'hold', duration: '', target_tps: 50 } as LoadStageDraft]
    // The controller reads a missing target as zero and refuses an empty duration.
    expect(stageLevelAt(tpsOnly, 70, 'target_vusers', { ...saved, initialLevel: 8 })).toBe(0)
    expect(stageScheduleSeconds(tpsOnly)).toBe(60)
    // An editor keeps the level through a TPS-only stage and draws an unfinished one as a minute.
    expect(stageLevelAt(tpsOnly, 70, 'target_vusers', { emptyDurationSeconds: 60, missingTarget: 'previous', initialLevel: 8 })).toBe(8)
    expect(stageScheduleSeconds(tpsOnly, 60)).toBe(120)
  })

  it('takes the stage peak once for every caller', () => {
    expect(maxStageVUsers(stages)).toBe(30)
    expect(maxStageVUsers([{ type: 'hold', duration: '1m', target_vusers: Number.NaN }])).toBe(0)
    expect(maxStageVUsers(undefined)).toBe(0)
  })

  it('draws the same staged curve in the editors model chart and the run preview', () => {
    const group = { name: 'Staged', vusers: 40, stages: [{ type: 'ramp_down', duration: '60s', target_vusers: 10 }, { type: 'hold', duration: '60s', target_vusers: 10 }] as LoadStageDraft[] }
    const model = buildScenarioModelPreview({ duration: '2m', groups: [group] })
    const run = buildRunLoadProfilePreview([{ order: 0, script_id: 's', ...group }], '2m', undefined, false)
    for (const second of [0, 20, 40, 60, 80, 100]) {
      const modelValue = model.points.find(point => point.elapsedSeconds === second)?.group_0_vusers
      const runValue = run.points.find(point => point.second === second)?.group0
      expect(Math.round(Number(modelValue)), `model at ${second}`).toBe(runValue)
    }
    // Both start the ramp-down from the group's 40 VUsers, not from zero.
    expect(model.points.find(point => point.elapsedSeconds === 30)?.group_0_vusers).toBe(25)
  })
})
