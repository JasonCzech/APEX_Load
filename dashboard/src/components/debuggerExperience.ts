import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugBreakpoint, ScriptDebugSession } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import type { ReplayOptionsState } from './scriptEditorState'

export type DebugCapture = 'diagnostic' | 'errors' | 'script'
export interface DebugRunRecord {
  id: string
  report: BrowserReplayReport
  options: ReplayOptionsState
  breakpoints: ScriptDebugBreakpoint[]
  target: string
  draft: boolean
  dataSourceCount?: number
  failurePolicy: string
  inputs?: Array<{ name: string; source: string; selection: string; update?: string; startRow?: number; seed?: number }>
}
export function captureScript(script: ScriptDefinition, capture: DebugCapture = 'diagnostic'): ScriptDefinition {
  // Other protocols do not implement successful message detail. Leave their
  // native evidence mechanisms intact instead of sending unsupported settings.
  if (capture === 'script' || !['http', 'soap', 'java_http'].includes(script.protocol)) return script
  return { ...script, config: { ...script.config, results: { ...script.config.results, message_detail: capture === 'errors'
    ? { ...script.config.results?.message_detail, mode: 'errors' }
    : { include_resources: script.config.results?.message_detail?.include_resources, mode: 'sampled', sample_rate: 1, max_items: 100, max_item_bytes: 65536, max_total_bytes: 4194304 } } } }
}
export function executionLabel(replaying: boolean, session: ScriptDebugSession | null, report: BrowserReplayReport | null): string {
  if (!replaying) return !report ? 'Ready' : report.stopped ? 'Stopped' : report.success ? 'Passed' : 'Failed'
  if (session?.status === 'stopped') return 'Stopping debugger'
  const action = session?.current_action
  const name = action?.action_name || 'next action'
  if (session?.status === 'paused') {
    const failed = session.current_event
    return failed && !failed.success && action && (failed.event_id && action.event_id ? failed.event_id === action.event_id : failed.phase === action.phase && (failed.action_index ?? 0) === action.action_index && failed.event_index === action.event_index)
      ? `Paused after failure: ${name}` : `Paused before: ${name}`
  }
  return action ? `Executing: ${name}` : session ? 'Running · waiting for next action' : 'Starting debugger'
}
export function eventLocation(event: BrowserReplayEvent): string {
  return `${event.phase || 'action'} ${(event.action_index ?? 0) + 1}${event.step_path?.length ? ` › ${event.step_path.join(' › ')}` : event.request_index != null ? ` › request ${event.request_index + 1}` : ''}${event.iteration_path?.length ? ` · iteration ${event.iteration_path.join('.')}` : event.iteration != null ? ` · iteration ${event.iteration}` : ''}`
}
export function eventIdentity(event: BrowserReplayEvent): string {
  return JSON.stringify([event.phase || 'action', event.action_index ?? 0, event.step_path || [], event.request_index ?? null, event.iteration_path || [], event.iteration ?? null])
}
export function variablesAt(events: BrowserReplayEvent[], seed: Record<string, string>, index: number): Record<string, string> {
  const result: Record<string, string> = Object.assign(Object.create(null), seed)
  for (const event of events.slice(0, index + 1)) Object.assign(result, event.extractions || {})
  return result
}
export function compareRuns(before: BrowserReplayReport, after: BrowserReplayReport) {
  // Occurrence number retains repeated executions even if a producer omits loop metadata.
  const indexed = (report: BrowserReplayReport) => {
    const counts = new Map<string, number>()
    return new Map(report.events.map(event => { const key = eventIdentity(event); const n = counts.get(key) || 0; counts.set(key, n + 1); return [`${key}:${n}`, event] }))
  }
  const left = indexed(before), right = indexed(after)
  return [...new Set([...left.keys(), ...right.keys()])].map(key => {
    const a = left.get(key), b = right.get(key)
    const fields: string[] = []
    if (!a || !b) fields.push(a ? 'Not executed in current run' : 'New execution')
    else {
      if (a.success !== b.success) fields.push('Outcome')
      if (a.error_message !== b.error_message || JSON.stringify(a.protocol_evidence?.assertion_failure) !== JSON.stringify(b.protocol_evidence?.assertion_failure)) fields.push('Assertion / diagnosis')
      if (a.target !== b.target || JSON.stringify(a.snapshot?.request_headers || a.headers) !== JSON.stringify(b.snapshot?.request_headers || b.headers) || (a.snapshot?.request_body ?? a.body) !== (b.snapshot?.request_body ?? b.body)) fields.push('Resolved request')
      if (JSON.stringify(a.extractions) !== JSON.stringify(b.extractions)) fields.push('Variables')
      if (a.status_code !== b.status_code || a.snapshot?.response_body !== b.snapshot?.response_body || JSON.stringify(a.snapshot?.response_headers) !== JSON.stringify(b.snapshot?.response_headers)) fields.push('Response')
    }
    return { key, before: a, after: b, fields }
  })
}
// A run identity is per run, like the group name, so a profile does not keep one.
export type DebugProfile = { name: string; options: Omit<ReplayOptionsState, 'variablesText' | 'groupName' | 'runId'> }
export function safeProfile(name: string, options: ReplayOptionsState): DebugProfile {
  // Whitelist only execution controls. Never persist variables, URLs, group
  // names, captured evidence or copied credentials in named presets.
  const afterFailure = (['test-run', 'continue', 'stop'] as const).find(value => value === options.afterFailure) || 'test-run'
  return { name: name.trim().slice(0, 60), options: { ignoreThinkTime: options.ignoreThinkTime, startPaused: options.startPaused, breakOnFailure: options.breakOnFailure, logLevel: options.logLevel, capture: options.capture || 'diagnostic', afterFailure } }
}
export function readProfiles(raw: string | null): DebugProfile[] {
  try {
    const parsed: unknown = JSON.parse(raw || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed.filter(p => typeof p?.name === 'string' && p.name.trim() && typeof p?.options?.ignoreThinkTime === 'boolean' && typeof p.options.startPaused === 'boolean' && typeof p.options.breakOnFailure === 'boolean' && ['error', 'warn', 'info', 'debug', 'trace'].includes(p.options.logLevel) && ['diagnostic', 'errors', 'script'].includes(p.options.capture)).slice(0, 10).map(p => safeProfile(p.name, p.options))
  } catch { return [] }
}
