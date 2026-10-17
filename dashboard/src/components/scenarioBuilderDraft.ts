import { removeStoredItem } from '../utils/browserStorage'
import type { ScenarioFormState, ScenarioStartChoice } from '../types/scenarioAuthoring'

// Scenario-builder progress is never autosaved under this legacy key. Explicit, project-scoped
// browser checkpoints (Save draft on an incomplete scenario, then Resume) live in
// scenarioAuthoringDraft.ts instead. This key is therefore write-never / clear-only — it exists
// purely to purge a draft left behind in a browser by any build that did autosave one, so a
// stale draft can never be silently restored into a fresh builder.
// TestHub.test.tsx ('starts over without restoring or persisting an in-progress builder
// draft') pins both halves of this contract — nothing is written here, and anything found is
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
  // Scripts are chosen first, so a selected script is progress even before a name or model exists.
  const hasScript = Boolean(draft.form?.groups?.some(group => group.script_id))
  return Boolean(draft.scenarioStartChoice) || name.trim().length > 0 || hasScript || draft.maxVisitedWizardIndex > 0
}

export function clearScenarioBuilderDraft(): void {
  removeStoredItem(SCENARIO_BUILDER_DRAFT_KEY)
}
