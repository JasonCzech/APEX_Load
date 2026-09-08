import type { StoredScript } from '../types'
import type { ScriptActionDefinition } from '../types/scripts'
import { actionExecutionBlockers } from './scriptActionHelpers'

/** Authoring readiness is advisory; the controller still validates the run. */
export function scenarioScriptReadiness(script: StoredScript) {
  const reasons = new Set<string>()
  const labels: Record<string, string> = {
    bruno_missing_variables: 'Supply the required Bruno variables.',
    import_missing_variables: 'Supply the required imported variables.',
    import_review_required: 'Resolve the imported behavior in script review.',
  }
  for (const input of script.authoring_health?.inputs || []) {
    if (input.status === 'blocker') reasons.add(input.summary)
  }
  const actions = [...(script.script?.init || []), ...(script.script?.actions || []), ...(script.script?.teardown || [])]
  const seen = new Set<ScriptActionDefinition>()
  while (actions.length) {
    const action = actions.pop()!
    if (seen.has(action)) continue
    seen.add(action)
    for (const blocker of actionExecutionBlockers(action)) reasons.add(labels[blocker] || `Resolve ${blocker.replace(/_/g, ' ')} in script review.`)
    const control = action.control
    if (control) actions.push(...(control.steps || []), ...(control.else || []), ...(control.default || []), ...(control.cases || []).flatMap(item => item.steps || []), ...(control.branches || []).flatMap(item => item.steps || []))
  }
  const blocked = reasons.size > 0 || script.authoring_health?.status === 'blocked'
  return {
    blocked,
    label: blocked ? 'Blocked' : script.authoring_health?.status === 'needs_review' ? 'Review recommended' : 'Not validated for this scenario',
    summary: [...reasons].join(' ') || (blocked ? 'Resolve the script authoring blockers before creating a run.' : 'Validate the scenario to check execution readiness, demand and placement.'),
  }
}
