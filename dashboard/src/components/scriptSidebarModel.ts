import type { ScriptDefinition } from '../types/scripts'
import type { KeyboardEvent } from 'react'
import { parseReplayVariablesText } from './scriptEditorUtils'
import { DEFAULT_REPLAY_OPTIONS, type ReplayOptionsState } from './scriptEditorState'

export function handleOutlineKeys(event: KeyboardEvent<HTMLElement>) {
  if (!['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key) || (event.target instanceof HTMLElement && event.target.matches('input, textarea, select'))) return
  const rows = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-outline-select], .debugger-action-row-main')).filter(row => row.getClientRects().length > 0 && !row.disabled)
  if (!rows.length) return
  event.preventDefault()
  const current = rows.indexOf(event.target as HTMLButtonElement)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? rows.length - 1 : Math.max(0, Math.min(rows.length - 1, current + (event.key === 'ArrowDown' ? 1 : -1)))
  rows[next].focus()
  rows[next].scrollIntoView({ block: 'nearest' })
}

export function sessionVariablesError(text: string): string | null {
  try { parseReplayVariablesText(text); return null }
  catch (error) { return error instanceof Error ? error.message : 'Enter valid JSON before starting Debug.' }
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
