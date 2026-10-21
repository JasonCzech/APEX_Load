import type { RunAnalysisPolicySelections } from '../../types/analysisPolicies'
export type { RunAnalysisPolicySelection, RunAnalysisPolicySelections } from '../../types/analysisPolicies'

export function runPolicySelectionProblem(selections: unknown): string {
  if (selections === undefined) return ''
  if (!selections || typeof selections !== 'object' || Array.isArray(selections)) return 'Choose a browser or journey policy.'
  for (const [kind, selection] of Object.entries(selections ?? {})) {
    if (kind !== 'browser' && kind !== 'journey') return 'Choose a browser or journey policy.'
    if (!selection || typeof selection !== 'object' || Array.isArray(selection) || Object.keys(selection).some(key => key !== 'id' && key !== 'revision') || typeof selection.id !== 'string' || !selection.id || selection.id !== selection.id.trim() || selection.id.length > 256 || !Number.isSafeInteger(selection.revision) || selection.revision < 1) return 'Choose a policy and a positive immutable revision.'
  }
  return ''
}

export function cloneRunPolicySelections(selections: RunAnalysisPolicySelections | undefined): RunAnalysisPolicySelections | undefined {
  if (!selections) return undefined
  return { ...(selections.browser ? { browser: { ...selections.browser } } : {}), ...(selections.journey ? { journey: { ...selections.journey } } : {}) }
}
