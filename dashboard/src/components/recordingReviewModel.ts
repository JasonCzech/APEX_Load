import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { rawHttpSequenceRequest } from './scriptActionHelpers'
import { buildScriptPhaseActionRows, containerChildSteps, debugContainerKind } from './scriptEditorWorkbench'

// The steps a converted recording replays, in order, for review before it is
// saved: every leaf action of every phase, with containers (HTTP sequences,
// control flow) opened up.
export interface RecordedStepRow {
  index: number
  name: string
  method: string
  target: string
  transaction: string
  thinkTime: string
  checks: number
  extracts: number
}

// A converted recording's review shows at most this many steps; the editor
// shows the rest.
export const RECORDING_REVIEW_STEP_LIMIT = 200

export function recordedScriptSteps(script: ScriptDefinition | null | undefined): RecordedStepRow[] {
  if (!script) return []
  const rows: RecordedStepRow[] = []
  // raw is an HTTP sequence's own request object: the child view of it drops
  // the think time.
  const visit = (action: ScriptActionDefinition, raw?: ScriptActionDefinition | null) => {
    const kind = debugContainerKind(action)
    if (kind) {
      containerChildSteps(action, kind).forEach(child =>
        visit(child.action, child.requestIndex === undefined ? null : rawHttpSequenceRequest(action, child.requestIndex)),
      )
      return
    }
    rows.push({
      index: rows.length + 1,
      name: action.name || action.type,
      method: (action.method || action.type || '').toUpperCase(),
      target: action.target || '',
      transaction: typeof action.metadata?.source_transaction === 'string' ? action.metadata.source_transaction : '',
      thinkTime: formatStepThinkTime(raw || action),
      checks: action.assertions?.length || 0,
      extracts: action.extractors?.length || 0,
    })
  }
  buildScriptPhaseActionRows(script).forEach(row => visit(row.action))
  return rows
}

// formatStepThinkTime words a step's own pause ("1.5 s after"); blank when
// the step uses the script's think time.
export function formatStepThinkTime(action: ScriptActionDefinition): string {
  const thinkTime = action.think_time
  if (!thinkTime || thinkTime.ignore) return ''
  const ms = thinkTime.mean_ms ?? thinkTime.min_ms
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms <= 0) return ''
  const amount = ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1)} s`
  return action.think_time_after ? `${amount} after` : `${amount} before`
}

// sentenceCaseNote capitalizes a server note that starts with a plain
// lowercase word; one that starts with an identifier, URL or code
// (aura.token, http://…) is left as written.
export function sentenceCaseNote(note: string): string {
  const trimmed = note.trim()
  return /^[a-z]+(\s|$)/.test(trimmed) ? trimmed.charAt(0).toUpperCase() + trimmed.slice(1) : trimmed
}
