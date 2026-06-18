import { readStoredJSON, removeStoredItem, writeStoredJSON } from '../utils/browserStorage'
import type { GoalSizingReport } from '../types'
import type { ScenarioFormState, ScenarioStartChoice } from './TestHub'
import type { ScenarioSourceMode, ScenarioWizardStep } from './scenarioWizardUtils'

// Persists an in-progress (unsaved, non-edit) Scenario Builder wizard draft so the
// stepper's completion checkmarks survive a reload / fresh session instead of resetting.
export const SCENARIO_BUILDER_DRAFT_KEY = 'apexload.scenarioBuilder.draft'

export interface ScenarioBuilderDraft {
  form: ScenarioFormState
  scenarioSourceMode: ScenarioSourceMode
  scenarioStartChoice: ScenarioStartChoice
  selectedShapeId: string
  selectedCloneScenarioId: string
  selectedLoadModelTemplateId: string
  goalSizingResult: GoalSizingReport | null
  wizardStep: ScenarioWizardStep
  maxVisitedWizardIndex: number
}

// A draft is only worth persisting once the user has made a meaningful choice —
// otherwise an empty builder would clobber a real draft on every visit.
export function hasScenarioBuilderProgress(
  draft: Pick<ScenarioBuilderDraft, 'form' | 'scenarioStartChoice' | 'maxVisitedWizardIndex'>,
): boolean {
  const name = draft.form?.name ?? ''
  return Boolean(draft.scenarioStartChoice) || name.trim().length > 0 || draft.maxVisitedWizardIndex > 0
}

export function loadScenarioBuilderDraft(): ScenarioBuilderDraft | null {
  const draft = readStoredJSON<ScenarioBuilderDraft | null>(SCENARIO_BUILDER_DRAFT_KEY, null)
  if (!draft || typeof draft !== 'object') return null
  // Guard against malformed/legacy payloads: the form object is load-bearing.
  if (!draft.form || typeof draft.form !== 'object') return null
  return draft
}

export function saveScenarioBuilderDraft(draft: ScenarioBuilderDraft): void {
  writeStoredJSON(SCENARIO_BUILDER_DRAFT_KEY, draft)
}

export function clearScenarioBuilderDraft(): void {
  removeStoredItem(SCENARIO_BUILDER_DRAFT_KEY)
}
