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
    if (value.once) continue // temporary run-to targets never survive an editor reload
    if (value.disabled === true) breakpoint.disabled = true
    if (Number.isInteger(value.hit_count) && Number(value.hit_count) > 0 && Number(value.hit_count) <= 1000000) breakpoint.hit_count = Number(value.hit_count)
    if (Number.isInteger(value.iteration) && Number(value.iteration) >= 0 && Number(value.iteration) <= 1000000) breakpoint.iteration = Number(value.iteration)
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
  const location = entry.phase ? ` ${entry.phase} ${(entry.action_index ?? 0) + 1}` : ''
  const raw = entry.raw ? `\n${entry.raw}` : ''
  return `[${formatReplayTime(entry.timestamp)}] ${entry.level.toUpperCase()} ${entry.category}${location} ${entry.message}${raw}`
}

export type DebugLogTokenKind =
  | 'plain'
  | 'method'
  | 'target'
  | 'status-success'
  | 'status-warning'
  | 'status-error'
  | 'header'
  | 'key'
  | 'string'
  | 'number'
  | 'literal'
  | 'punctuation'

export interface DebugLogToken {
  kind: DebugLogTokenKind
  text: string
}

export interface DebugLogHighlightResult {
  omittedCharCount: number
  plainRemainder: string
  tokens: DebugLogToken[]
}

export const DEBUG_LOG_HIGHLIGHT_CHAR_LIMIT = 12_000
export const DEBUG_LOG_HIGHLIGHT_TOKEN_LIMIT = 600
export const DEBUG_LOG_RENDER_CHAR_LIMIT = 64 * 1024

const DEBUG_HTTP_METHOD_PATTERN = /^(CONNECT|DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT|TRACE)(\s+)(\S+)(.*)$/i
const DEBUG_HTTP_STATUS_PATTERN = /^(HTTP\/\S+\s+)(\d{3})(.*)$/i
const DEBUG_HEADER_PATTERN = /^([!#$%&'*+\-.^_`|~0-9A-Za-z]+)(:\s*)(.*)$/
const DEBUG_LITERAL_PATTERN = /"(?:\\.|[^"\\])*"|\b(?:true|false|null)\b|-?\b\d+(?:\.\d+)?\b|[\u005b\u005d{},:]/g

function tokenizeDebugLiterals(text: string): DebugLogToken[] {
  const tokens: DebugLogToken[] = []
  let cursor = 0
  for (const match of text.matchAll(DEBUG_LITERAL_PATTERN)) {
    const index = match.index ?? 0
    if (index > cursor) tokens.push({ kind: 'plain', text: text.slice(cursor, index) })
    const value = match[0]
    let kind: DebugLogTokenKind = 'punctuation'
    if (value.startsWith('"')) {
      kind = /^\s*:/.test(text.slice(index + value.length)) ? 'key' : 'string'
    } else if (/^-?\d/.test(value)) {
      kind = 'number'
    } else if (/^(?:true|false|null)$/.test(value)) {
      kind = 'literal'
    }
    tokens.push({ kind, text: value })
    cursor = index + value.length
  }
  if (cursor < text.length) tokens.push({ kind: 'plain', text: text.slice(cursor) })
  return tokens.length > 0 ? tokens : [{ kind: 'plain', text }]
}

export function tokenizeDebugLogRawLine(line: string): DebugLogToken[] {
  const request = line.match(DEBUG_HTTP_METHOD_PATTERN)
  if (request) {
    return [
      { kind: 'method', text: request[1] },
      { kind: 'plain', text: request[2] },
      { kind: 'target', text: request[3] },
      ...tokenizeDebugLiterals(request[4]),
    ]
  }

  const response = line.match(DEBUG_HTTP_STATUS_PATTERN)
  if (response) {
    const status = Number(response[2])
    const statusKind: DebugLogTokenKind = status >= 500
      ? 'status-error'
      : status >= 400
        ? 'status-warning'
        : 'status-success'
    return [
      { kind: 'plain', text: response[1] },
      { kind: statusKind, text: response[2] },
      { kind: 'plain', text: response[3] },
    ]
  }

  const header = line.match(DEBUG_HEADER_PATTERN)
  if (header) {
    return [
      { kind: 'header', text: header[1] },
      { kind: 'punctuation', text: header[2] },
      ...tokenizeDebugLiterals(header[3]),
    ]
  }

  return tokenizeDebugLiterals(line)
}

export function highlightDebugLogRaw(
  raw: string,
  limits: {
    highlightChars?: number
    highlightTokens?: number
    renderChars?: number
  } = {},
): DebugLogHighlightResult {
  const renderChars = Math.max(0, limits.renderChars ?? DEBUG_LOG_RENDER_CHAR_LIMIT)
  const highlightChars = Math.min(renderChars, Math.max(0, limits.highlightChars ?? DEBUG_LOG_HIGHLIGHT_CHAR_LIMIT))
  const highlightTokens = Math.max(0, limits.highlightTokens ?? DEBUG_LOG_HIGHLIGHT_TOKEN_LIMIT)
  const visible = raw.slice(0, renderChars)
  const highlightSource = visible.slice(0, highlightChars)
  const lines = highlightSource.split('\n')
  const tokens: DebugLogToken[] = []
  let consumed = 0

  for (let lineIndex = 0; lineIndex < lines.length; lineIndex += 1) {
    const lineTokens = tokenizeDebugLogRawLine(lines[lineIndex])
    for (const token of lineTokens) {
      if (tokens.length >= highlightTokens) {
        return {
          tokens,
          plainRemainder: visible.slice(consumed),
          omittedCharCount: Math.max(0, raw.length - visible.length),
        }
      }
      tokens.push(token)
      consumed += token.text.length
    }
    if (lineIndex < lines.length - 1) {
      if (tokens.length >= highlightTokens) {
        return {
          tokens,
          plainRemainder: visible.slice(consumed),
          omittedCharCount: Math.max(0, raw.length - visible.length),
        }
      }
      tokens.push({ kind: 'plain', text: '\n' })
      consumed += 1
    }
  }

  return {
    tokens,
    plainRemainder: visible.slice(consumed),
    omittedCharCount: Math.max(0, raw.length - visible.length),
  }
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

/** Terminal review starts at the failure, even when teardown subsequently passed. */
export function replayEvidenceIndex(report: BrowserReplayReport): number | null {
  const failed = report.events.findIndex(event => !event.success)
  return failed >= 0 ? failed : report.events.length ? report.events.length - 1 : null
}
