// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest'

import {
  SCENARIO_BUILDER_DRAFT_KEY,
  clearScenarioBuilderDraft,
  hasScenarioBuilderProgress,
  loadScenarioBuilderDraft,
  saveScenarioBuilderDraft,
  type ScenarioBuilderDraft,
} from './scenarioBuilderDraft'

function makeDraft(overrides: Partial<ScenarioBuilderDraft> = {}): ScenarioBuilderDraft {
  return {
    form: { name: '' } as ScenarioBuilderDraft['form'],
    scenarioSourceMode: 'shape',
    scenarioStartChoice: '',
    selectedShapeId: '',
    selectedCloneScenarioId: '',
    selectedLoadModelTemplateId: '',
    goalSizingResult: null,
    wizardStep: 'start',
    maxVisitedWizardIndex: 0,
    ...overrides,
  }
}

describe('scenarioBuilderDraft', () => {
  afterEach(() => {
    window.localStorage.clear()
  })

  it('returns null when no draft is stored', () => {
    expect(loadScenarioBuilderDraft()).toBeNull()
  })

  it('round-trips a saved draft', () => {
    const draft = makeDraft({
      scenarioStartChoice: 'shape',
      selectedShapeId: 'spike',
      wizardStep: 'scripts',
      maxVisitedWizardIndex: 2,
      form: { name: 'Checkout flow' } as ScenarioBuilderDraft['form'],
    })
    saveScenarioBuilderDraft(draft)
    expect(loadScenarioBuilderDraft()).toEqual(draft)
  })

  it('clears a stored draft', () => {
    saveScenarioBuilderDraft(makeDraft({ scenarioStartChoice: 'shape' }))
    clearScenarioBuilderDraft()
    expect(loadScenarioBuilderDraft()).toBeNull()
  })

  it('rejects malformed payloads that lack a form object', () => {
    window.localStorage.setItem(SCENARIO_BUILDER_DRAFT_KEY, JSON.stringify({ scenarioStartChoice: 'shape' }))
    expect(loadScenarioBuilderDraft()).toBeNull()
  })

  it('treats a pristine builder as having no progress', () => {
    expect(hasScenarioBuilderProgress(makeDraft())).toBe(false)
  })

  it('detects progress from a start choice', () => {
    expect(hasScenarioBuilderProgress(makeDraft({ scenarioStartChoice: 'goal' }))).toBe(true)
  })

  it('detects progress from a non-empty (trimmed) scenario name', () => {
    expect(hasScenarioBuilderProgress(makeDraft({ form: { name: '  ' } as ScenarioBuilderDraft['form'] }))).toBe(false)
    expect(hasScenarioBuilderProgress(makeDraft({ form: { name: 'My run' } as ScenarioBuilderDraft['form'] }))).toBe(true)
  })

  it('detects progress once the user has advanced past the first step', () => {
    expect(hasScenarioBuilderProgress(makeDraft({ maxVisitedWizardIndex: 1 }))).toBe(true)
  })
})
