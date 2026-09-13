import { describe, expect, it } from 'vitest'
import { scenarioScriptConflict } from './scenarioFormModel'
import { availableScenarioModelFamilies, SCENARIO_MODEL_FAMILIES, shapeFamilyFor } from './scenarioWizardUtils'

describe('scenario model families', () => {
  it('groups every selectable support-matrix shape into exactly one family', () => {
    const grouped = SCENARIO_MODEL_FAMILIES.flatMap(family => [family.primaryShapeId, ...family.variantShapeIds])
    expect(new Set(grouped).size).toBe(grouped.length)
    expect(grouped.sort()).toEqual([
      'chaos-resilience', 'closed-vuser', 'csv-driven', 'open-arrival-rate', 'production-replay', 'soak', 'spike-rendezvous', 'stepped-ramp', 'stress',
    ])
  })

  it('maps stored shape IDs, including the legacy goal alias, to their family', () => {
    expect(shapeFamilyFor('closed-vuser')).toBe('fixed-vusers')
    expect(shapeFamilyFor('soak')).toBe('fixed-vusers')
    expect(shapeFamilyFor('chaos-resilience')).toBe('fixed-vusers')
    expect(shapeFamilyFor('stress')).toBe('stepped')
    expect(shapeFamilyFor('goal-based')).toBe('goal')
    expect(shapeFamilyFor('csv-driven')).toBe('traffic-profile')
    expect(shapeFamilyFor('mixed-protocol')).toBeNull()
    expect(shapeFamilyFor('')).toBeNull()
  })

  it('honours the feature-filtered matrix and treats an unloaded matrix as fully enabled', () => {
    expect(availableScenarioModelFamilies(null).map(family => family.id)).toEqual(['fixed-vusers', 'stepped', 'goal', 'traffic-profile'])
    const filtered = availableScenarioModelFamilies(new Set(['soak', 'stepped-ramp', 'csv-driven', 'mixed-protocol']))
    expect(filtered.map(family => [family.id, family.shapeIds])).toEqual([
      ['fixed-vusers', ['soak']],
      ['stepped', ['stepped-ramp']],
      ['traffic-profile', ['csv-driven']],
    ])
  })
})

describe('scenarioScriptConflict', () => {
  it('only reports a conflict when an authored script would change or disappear', () => {
    expect(scenarioScriptConflict([{ script_id: '' }], [{ script_id: 'copied' }])).toBe(false)
    expect(scenarioScriptConflict([{ script_id: 'mine' }], [{ script_id: 'mine' }])).toBe(false)
    expect(scenarioScriptConflict([{ script_id: 'mine' }], [{ script_id: 'copied' }])).toBe(true)
    expect(scenarioScriptConflict([{ script_id: 'a' }, { script_id: 'b' }], [{ script_id: 'a' }])).toBe(true)
  })
})
