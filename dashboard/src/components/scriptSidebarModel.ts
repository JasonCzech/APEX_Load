import type { ScriptDefinition } from '../types/scripts'
import { parseReplayVariablesText } from './scriptEditorUtils'
import { DEFAULT_REPLAY_OPTIONS, type ReplayOptionsState } from './scriptEditorState'

export function sessionVariablesError(text: string): string | null {
  try { parseReplayVariablesText(text); return null }
  catch (error) { return error instanceof Error ? error.message : 'Enter valid JSON before starting Debug.' }
}

const DEBUG_RUN_ID_PATTERN = /^[A-Za-z0-9._:-]{1,128}$/

/** Why a requested run identity cannot be used, or null; blank means the session's own. */
export function debugRunIdError(runId: string): string | null {
  const id = runId.trim()
  return id && !DEBUG_RUN_ID_PATTERN.test(id) ? 'Run identity takes up to 128 letters, digits, ".", "_", ":" or "-".' : null
}

/**
 * Whether the debugger carries on after a failure it does not pause on: as
 * chosen, or as a test run of the script would — the script's own setting,
 * else the scenario default of continuing.
 */
export function debugContinuesAfterFailure(options: Pick<ReplayOptionsState, 'afterFailure'>, script: ScriptDefinition): boolean {
  if (options.afterFailure === 'continue') return true
  if (options.afterFailure === 'stop') return false
  return script.config?.error_handling?.continue_on_error !== false
}

export function debugOverrideCount(options: ReplayOptionsState): number {
  return (Object.keys(DEFAULT_REPLAY_OPTIONS) as (keyof ReplayOptionsState)[]).filter(key => key === 'variablesText' ? Boolean(sessionVariablesError(options.variablesText)) || Object.keys(parseReplayVariablesText(options.variablesText) || {}).length > 0 : options[key] !== DEFAULT_REPLAY_OPTIONS[key]).length
}

export function parameterUsageLabel(script: ScriptDefinition, path: string): string {
  const match = /^(init|actions|teardown)\[(\d+)\](.*)$/.exec(path)
  if (!match) return path
  const actions = script[match[1] as 'init' | 'actions' | 'teardown']
  return `${actions?.[Number(match[2])]?.name || `${match[1]} ${Number(match[2]) + 1}`} · ${match[3].replace(/^\./, '') || 'action'}`
}
