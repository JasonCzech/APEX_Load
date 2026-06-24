import { removeStoredItem } from '../utils/browserStorage'
import type { ScenarioFormState, ScenarioStartChoice } from './TestHub'

export const SCENARIO_BUILDER_DRAFT_KEY = 'apexload.scenarioBuilder.draft'

interface ScenarioBuilderProgressDraft {
  form: ScenarioFormState
  scenarioStartChoice: ScenarioStartChoice
  maxVisitedWizardIndex: number
}

export function hasScenarioBuilderProgress(
  draft: ScenarioBuilderProgressDraft,
): boolean {
  const name = draft.form?.name ?? ''
  return Boolean(draft.scenarioStartChoice) || name.trim().length > 0 || draft.maxVisitedWizardIndex > 0
}

export function clearScenarioBuilderDraft(): void {
  removeStoredItem(SCENARIO_BUILDER_DRAFT_KEY)
}
