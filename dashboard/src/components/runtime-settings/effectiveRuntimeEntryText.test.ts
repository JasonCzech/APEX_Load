import { describe, expect, it } from 'vitest'
import { effectiveRuntimeEntry, formatEffectiveRuntimeEntryInline } from './effectiveRuntimeEntryText'

describe('effectiveRuntimeEntryText', () => {
  it('labels a scenario-row count as a scenario default, not a group override', () => {
    const entry = effectiveRuntimeEntry(
      { name: 'Checkout', iterations: 5, iterationOverrideKnown: true },
      { run_logic: { iterations: 5 }, override_fields: ['run_logic.iterations'] } as never,
      'run_logic.iterations',
    )
    expect(entry?.provenance).toBe('scenario_default')
    expect(entry && formatEffectiveRuntimeEntryInline(entry)).toBe('5 (scenario default)')
  })

  it('labels the group compact override as a group override', () => {
    const entry = effectiveRuntimeEntry({ name: 'Checkout', iterations: 4, iterationOverride: 4, iterationOverrideKnown: true }, undefined, 'run_logic.iterations')
    expect(entry && formatEffectiveRuntimeEntryInline(entry)).toBe('4 (group override)')
  })

  it('keeps the schedule note on a script cap a duration schedule removes', () => {
    const entry = effectiveRuntimeEntry(
      { name: 'Imported', iterationOverrideKnown: true, scheduleTerminationMode: 'duration', scriptConfig: { run_logic: { iterations: 1 } } as never },
      undefined,
      'run_logic.iterations',
    )
    expect(entry && formatEffectiveRuntimeEntryInline(entry)).toBe('Script cap 1 (overridden by the schedule)')
  })

  it('shows a legacy count whose layer is unknown without a label', () => {
    const entry = effectiveRuntimeEntry({ name: 'Checkout', iterations: 3 }, undefined, 'run_logic.iterations')
    expect(entry?.provenance).toBeUndefined()
    expect(entry && formatEffectiveRuntimeEntryInline(entry)).toBe('3')
  })
})
