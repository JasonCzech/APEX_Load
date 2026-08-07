import { removeStoredItem } from '../utils/browserStorage'
import type { ScenarioFormState, ScenarioStartChoice } from './TestHub'

// Scenario-builder progress is deliberately NOT persisted: builder state lives only in
// TestHub's React state for the lifetime of the session. This key is therefore write-never /
// clear-only — it exists purely to purge a draft left behind in a browser by any build that
// did persist one, so a stale draft can never be silently restored into a fresh builder.
// Do not "complete" this into a save/restore pair without a product decision:
// TestHub.test.tsx ('starts over without restoring or persisting an in-progress builder
// draft') pins both halves of this contract — nothing is written, and anything found is
// discarded on entering the builder or starting over.
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
