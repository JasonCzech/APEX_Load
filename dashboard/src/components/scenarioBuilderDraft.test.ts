// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest'

import {
  SCENARIO_BUILDER_DRAFT_KEY,
  clearScenarioBuilderDraft,
  hasScenarioBuilderProgress,
} from './scenarioBuilderDraft'
import type { ScenarioFormState, ScenarioStartChoice } from '../types/scenarioAuthoring'

interface ProgressDraft {
  form: ScenarioFormState
  scenarioStartChoice: ScenarioStartChoice
  maxVisitedWizardIndex: number
}

function makeDraft(overrides: Partial<ProgressDraft> = {}): ProgressDraft {
  return {
    form: { name: '' } as ScenarioFormState,
    scenarioStartChoice: '',
    maxVisitedWizardIndex: 0,
    ...overrides,
  }
}

describe('scenarioBuilderDraft', () => {
  afterEach(() => {
    window.localStorage.clear()
  })

  it('clears a stored draft', () => {
    window.localStorage.setItem(SCENARIO_BUILDER_DRAFT_KEY, JSON.stringify({ scenarioStartChoice: 'shape' }))
    clearScenarioBuilderDraft()
    expect(window.localStorage.getItem(SCENARIO_BUILDER_DRAFT_KEY)).toBeNull()
  })

  it('treats a pristine builder as having no progress', () => {
    expect(hasScenarioBuilderProgress(makeDraft())).toBe(false)
  })

  it('detects progress from a start choice', () => {
    expect(hasScenarioBuilderProgress(makeDraft({ scenarioStartChoice: 'goal' }))).toBe(true)
  })

  it('detects progress from a non-empty (trimmed) scenario name', () => {
    expect(hasScenarioBuilderProgress(makeDraft({ form: { name: '  ' } as ScenarioFormState }))).toBe(false)
    expect(hasScenarioBuilderProgress(makeDraft({ form: { name: 'My run' } as ScenarioFormState }))).toBe(true)
  })

  it('detects progress from a selected script before a name or model exists', () => {
    expect(hasScenarioBuilderProgress(makeDraft({ form: { name: '', groups: [{ script_id: '' }] } as ScenarioFormState }))).toBe(false)
    expect(hasScenarioBuilderProgress(makeDraft({ form: { name: '', groups: [{ script_id: 'script-1' }] } as ScenarioFormState }))).toBe(true)
  })

  it('detects progress once the user has advanced past the first step', () => {
    expect(hasScenarioBuilderProgress(makeDraft({ maxVisitedWizardIndex: 1 }))).toBe(true)
  })
})
