import type {
  BrowserReplayEvent,
  BrowserReplayNetworkEntry,
  BrowserReplayReport,
  BrowserReplaySnapshot,
  ConditionEvidence,
  ScriptDebugBreakpoint,
  ScriptDebugLogEntry,
  ScriptDebugLogLevel,
  ScriptDebugSession,
} from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import { formatReplayTime, parseReplayVariablesText } from './scriptEditorUtils'
import type { ReplayActivityItem } from './scriptEditorState'

interface BrowserHTTPConversionReview {
  browserActionCount: number
  replayEvidence: boolean
  networkEvidence: boolean
  candidateRequestCount: number
  status: 'blocked' | 'needs_replay' | 'ready_for_review'
  findings: string[]
}

function replayEventsForConversion(
  replayReport: BrowserReplayReport | null,
  browserReplaySession?: ScriptDebugSession | null,
): BrowserReplayEvent[] {
  if (browserReplaySession?.events?.length) return browserReplaySession.events
  return replayReport?.events || []
}

export function replayNetworkEntriesForConversion(
  replayReport: BrowserReplayReport | null,
  browserReplaySession?: ScriptDebugSession | null,
): BrowserReplayNetworkEntry[] {
  const entries: BrowserReplayNetworkEntry[] = []
  const seen = new Set<string>()
  replayEventsForConversion(replayReport, browserReplaySession).forEach(event => {
    const eventEntries = [
      ...(event.network_entries || []),
      ...(event.snapshot?.network_entries || []),
    ]
    if (event.snapshot?.request_url) {
      eventEntries.push({
        url: event.snapshot.request_url,
        method: event.snapshot.request_method || event.method || 'GET',
        status_code: event.snapshot.response_status || event.status_code,
        resource_type: 'document',
        duration_ms: event.duration_ms,
      })
    }
    eventEntries.forEach(entry => {
      const url = entry.url?.trim()
      if (!url) return
      const method = (entry.method || 'GET').toUpperCase()
      const key = `${method} ${url}`
      if (seen.has(key)) return
      seen.add(key)
      entries.push({ ...entry, method, url })
    })
  })
  return entries
}

export function buildBrowserHTTPConversionReview(
  script: ScriptDefinition,
  replayReport: BrowserReplayReport | null,
  browserReplaySession?: ScriptDebugSession | null,
): BrowserHTTPConversionReview {
  const browserActionCount = script.actions.filter(action => action.type.startsWith('browser_')).length
  const replayEvents = replayEventsForConversion(replayReport, browserReplaySession)
  const conversionEntries = replayNetworkEntriesForConversion(replayReport, browserReplaySession)
  const networkEvidence = conversionEntries.length > 0
  const candidateRequestCount = conversionEntries.length
  const replayEvidence = Boolean(replayReport?.completed_at || browserReplaySession?.completed_at || replayEvents.length > 0)
  const status = !replayEvidence ? 'needs_replay' : networkEvidence ? 'ready_for_review' : 'blocked'
  const findings = [
    browserActionCount > 0 ? `${browserActionCount} browser action${browserActionCount === 1 ? '' : 's'} available as conversion context.` : 'No browser actions are available for conversion.',
    replayEvidence ? 'Replay evidence is attached to this draft.' : 'Run browser replay before generating an HTTP draft.',
    networkEvidence ? `${candidateRequestCount} captured request${candidateRequestCount === 1 ? '' : 's'} can seed HTTP conversion review.` : 'Network capture evidence is required before HTTP conversion.',
    'Generated HTTP drafts must still pass correlation, parameter data, runtime settings, replay, and approval gates.',
  ]
  return { browserActionCount, replayEvidence, networkEvidence, candidateRequestCount, status, findings }
}

export function hasStructuredReplaySnapshot(snapshot?: BrowserReplaySnapshot | null): boolean {
  return Boolean(
    snapshot?.request_method
    || snapshot?.request_url
    || snapshot?.request_headers
    || snapshot?.request_body
    || snapshot?.response_status
    || snapshot?.response_headers
    || snapshot?.response_body,
  )
}

export function safeParseReplayVariables(text: string): Record<string, string> | undefined {
  try {
    return parseReplayVariablesText(text)
  } catch {
    return undefined
  }
}

export function formatReplayDiagnosisCategory(category?: string): string {
  return (category || 'diagnosis').replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

export function debugBreakpointKey(phase: string, actionIndex: number, requestIndex?: number): string {
  return requestIndex == null ? `${phase}:${actionIndex}` : `${phase}:${actionIndex}:${requestIndex}`
}

export function normalizeDebugPhase(phase: string): ScriptDebugBreakpoint['phase'] {
  return phase === 'init' || phase === 'teardown' ? phase : 'action'
}

export function humanizeStepSegment(segment: string): string {
  const match = /^(\w+)\[(\d+)\]$/.exec(segment)
  if (!match) return segment
  const ordinal = Number(match[2]) + 1
  switch (match[1]) {
    case 'requests': return `Request ${ordinal}`
    case 'then': return `Then ${ordinal}`
    case 'else': return `Else ${ordinal}`
    case 'loop': return `Loop step ${ordinal}`
    default: return `${match[1]} ${ordinal}`
  }
}

export function normalizeStoredBreakpoints(raw: unknown): ScriptDebugBreakpoint[] {
  if (!Array.isArray(raw)) return []
  const breakpoints: ScriptDebugBreakpoint[] = []
  for (const entry of raw) {
    if (!entry || typeof entry !== 'object') continue
    const value = entry as Record<string, unknown>
    const actionIndex = Number(value.action_index)
    if (!Number.isInteger(actionIndex) || actionIndex < 0) continue
    const breakpoint: ScriptDebugBreakpoint = {
      phase: normalizeDebugPhase(String(value.phase ?? 'action')),
      action_index: actionIndex,
    }
    if (Number.isInteger(value.request_index) && (value.request_index as number) >= 0) {
      breakpoint.request_index = value.request_index as number
    }
    if (Array.isArray(value.step_path) && value.step_path.every(segment => typeof segment === 'string')) {
      breakpoint.step_path = value.step_path as string[]
    }
    breakpoints.push(breakpoint)
  }
  return breakpoints
}

export function formatDebugLeafSuffix(leaf: {
  request_index?: number
  request_count?: number
  iteration?: number
  iteration_path?: number[]
} | null | undefined): string {
  if (!leaf) return ''
  const parts: string[] = []
  if (leaf.request_index != null) {
    parts.push(leaf.request_count
      ? `request ${leaf.request_index + 1}/${leaf.request_count}`
      : `request ${leaf.request_index + 1}`)
  }
  if (leaf.iteration_path?.length) parts.push(`loop iter ${leaf.iteration_path.join('·')}`)
  else if (leaf.iteration != null) parts.push(`loop iter ${leaf.iteration}`)
  return parts.length ? ` · ${parts.join(' · ')}` : ''
}

export function formatConditionEvidence(evidence: ConditionEvidence): string {
  const result = evidence.result ? 'true' : 'false'
  if (evidence.expr?.trim()) return `${evidence.expr} → ${result}`
  const operation = evidence.op || 'eq'
  const left = evidence.left ?? ''
  const unary = operation === 'exists'
    || operation === 'not_exists'
    || operation === 'truthy'
    || operation === 'falsy'
    || operation === 'empty'
  if (unary) return `${left} ${operation} → ${result}`
  return `${left} ${operation} ${evidence.right ?? ''} → ${result}`
}

export function debugLogLevelWeight(level: ScriptDebugLogLevel | 'all'): number {
  switch (level) {
    case 'error': return 0
    case 'warn': return 1
    case 'info': return 2
    case 'debug': return 3
    case 'trace': return 4
    default: return 5
  }
}

export function formatDebugLogEntry(entry: ScriptDebugLogEntry): string {
  const location = entry.phase ? ` ${entry.phase}[${entry.action_index ?? '-'}]` : ''
  const raw = entry.raw ? `\n${entry.raw}` : ''
  return `[${formatReplayTime(entry.timestamp)}] ${entry.level.toUpperCase()} ${entry.category}${location} ${entry.message}${raw}`
}

export function upsertReplayActivity(
  items: ReplayActivityItem[],
  next: ReplayActivityItem,
): ReplayActivityItem[] {
  const index = items.findIndex(item => item.id === next.id)
  if (index === -1) return [...items, next]
  const updated = [...items]
  updated[index] = next
  return updated
}
