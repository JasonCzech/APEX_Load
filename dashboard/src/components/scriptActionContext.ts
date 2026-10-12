import type { ScriptActionDefinition, ScriptDefinition, ScriptReplayActionEvent } from '../types/scripts'
import type { ScriptActionPhase } from './scriptEditorWorkbench'

// What the side rail says about the selected action: the values it reads, the
// variables it extracts, the actions that read those, and how its last debug
// run went. Nothing here repeats what the action form already shows.

export interface ActionLocation { phase: ScriptActionPhase; actionIndex: number; name: string }

export interface ActionContextSummary {
  /** Template references the action reads, as written: "param.user", "var.token". */
  uses: string[]
  /** Variables the action's extractors set. */
  produces: string[]
  /** Other top-level actions that read a variable this action sets. */
  usedBy: ActionLocation[]
  lastDebug: { success: boolean; statusCode?: number; durationMs: number; error?: string } | null
  /** Debug results are kept per top-level action; a nested step shows its block's. */
  debugOfEnclosingAction: boolean
}

const referencePattern = /\{\{\s*(param|data|var)\.([A-Za-z0-9_.-]+)\s*\}\}/g
const phases: Array<[ScriptActionPhase, 'init' | 'actions' | 'teardown']> = [['init', 'init'], ['action', 'actions'], ['teardown', 'teardown']]

// Executable values only: imported evidence in metadata is not something the
// action reads at run time. A block reads what the steps inside it read.
function actionReferences(action: ScriptActionDefinition): string[] {
  const serialized = JSON.stringify({
    target: action.target,
    selector: action.selector,
    value: action.value,
    headers: action.headers,
    header_list: action.header_list,
    body: action.body,
    params: action.params,
    auth: action.auth,
    assertions: action.assertions,
    control: action.control,
  })
  return [...new Set([...serialized.matchAll(referencePattern)].map(match => `${match[1]}.${match[2]}`))]
}

/** `nested` is a step inside the top-level action at phase/actionIndex, when one is selected. */
export function summarizeActionContext(
  script: ScriptDefinition,
  phase: ScriptActionPhase,
  actionIndex: number,
  replayEvents: ScriptReplayActionEvent[] = [],
  nested?: ScriptActionDefinition,
): ActionContextSummary | null {
  const steps = (key: 'init' | 'actions' | 'teardown') => script[key] || []
  const topLevel = steps(phases.find(([name]) => name === phase)![1])[actionIndex]
  if (!topLevel) return null
  const action = nested || topLevel
  const produces = [...new Set((action.extractors || []).map(extractor => extractor.name.trim()).filter(Boolean))]
  const usedBy: ActionLocation[] = []
  if (produces.length > 0) {
    const produced = new Set(produces.map(name => `var.${name}`))
    for (const [otherPhase, key] of phases) {
      steps(key).forEach((other, otherIndex) => {
        // A nested step's own block can read what the step extracts.
        if (!nested && otherPhase === phase && otherIndex === actionIndex) return
        if (actionReferences(other).some(reference => produced.has(reference))) usedBy.push({ phase: otherPhase, actionIndex: otherIndex, name: other.name })
      })
    }
  }
  // The action's own event, not one of its sub-requests or nested steps.
  const event = [...replayEvents].reverse().find(item => item.phase === phase && item.action_index === actionIndex && item.request_index == null && !item.step_path?.length)
  return {
    uses: actionReferences(action),
    produces,
    usedBy,
    lastDebug: event ? { success: event.success, statusCode: event.status_code, durationMs: event.duration_ms, error: event.error_message } : null,
    debugOfEnclosingAction: Boolean(nested),
  }
}
