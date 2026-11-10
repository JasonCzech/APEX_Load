import type { BrowserReplayEvent, BrowserReplayNetworkEntry } from '../types/replay'
import { hasStructuredReplaySnapshot } from './scriptEditorReplayModel'
import type { ResponseSelectionValue } from './ResponseSelection'

/** What text selected in a replayed response can become, by the event's step. */
export interface ResponseActions {
  /** Whether the response's status can become a check (HTTP). */
  offersStatus: boolean
  disabledReason: (event: BrowserReplayEvent) => string
  laterUses: (event: BrowserReplayEvent, text: string) => number
  /** Each adds to the event's step and says whether it did (false: the step already had it). */
  onContains: (event: BrowserReplayEvent, text: string) => boolean
  onSave: (event: BrowserReplayEvent, selection: ResponseSelectionValue) => boolean
  onCorrelate: (event: BrowserReplayEvent, selection: ResponseSelectionValue) => void
  /** Sets the step's status check to status, adding one when it has none. */
  onStatus: (event: BrowserReplayEvent, status: number) => boolean
  /** The status the event's step already checks for, if any. */
  checkedStatus: (event: BrowserReplayEvent) => number | undefined
}

export type RequestResponseInspectorTab = 'response' | 'request' | 'correlation' | 'timing' | 'network' | 'raw'

export const REQUEST_RESPONSE_INSPECTOR_TABS: ReadonlyArray<{ id: RequestResponseInspectorTab; label: string }> = [
  { id: 'response', label: 'Response' },
  { id: 'request', label: 'Request' },
  { id: 'correlation', label: 'Correlation' },
  { id: 'timing', label: 'Timing & transport' },
  { id: 'network', label: 'Network' },
  { id: 'raw', label: 'Raw' },
]

export type ReplayVariableDiff = Record<string, { previous?: string; next: string }>

/** Whether ReplayExtractions has anything to show for the event. */
export function hasReplayExtractions(event: BrowserReplayEvent): boolean {
  if (event.action_type === 'live_table') return false
  const unappliedCount = event.unapplied_extractions_omitted_count
  return event.protocol_evidence?.concurrent_group_extractions_omitted === true
    || Boolean(event.extractions && Object.keys(event.extractions).length > 0)
    || Boolean(event.unapplied_extractions && Object.keys(event.unapplied_extractions).length > 0)
    || (typeof unappliedCount === 'number' && Number.isSafeInteger(unappliedCount) && unappliedCount > 0)
    || Boolean(event.streamed_children?.length)
    || Boolean(event.streamed_children_omitted_count)
    || Boolean(event.credential_classification_error)
}

/** The event's captured response can be tried against an extractor. */
export function canTestExtractor(event: BrowserReplayEvent): boolean {
  return Boolean(event.snapshot?.response_body || event.snapshot?.response_headers)
}

export function replayNetworkEntries(event: BrowserReplayEvent) {
  return event.network_entries || event.snapshot?.network_entries
}

function resourceCount(event: BrowserReplayEvent): number {
  return event.snapshot?.resources?.length ?? 0
}

function hasPageArtifacts(event: BrowserReplayEvent): boolean {
  const snapshot = event.snapshot
  return Boolean(
    event.dom_summary || snapshot?.dom_summary
    || event.console_entries?.length || snapshot?.console_entries?.length
    || event.html || snapshot?.html
    || event.screenshot_base64 || snapshot?.screenshot_base64,
  )
}

/** Path and query of a URL, so an absolute URL matches the same request written relative. */
function requestPathAndQuery(url: string): string {
  const trimmed = url.trim()
  try {
    const parsed = new URL(trimmed, 'http://relative.invalid')
    return `${parsed.pathname}${parsed.search}`
  } catch {
    return trimmed
  }
}

function sameRequestURL(a: string, b: string): boolean {
  if (a.trim() === b.trim()) return true
  const absolute = (url: string) => /^[a-z][a-z\d+.-]*:\/\//i.test(url.trim())
  // Two absolute URLs must match whole (another host is another request).
  if (absolute(a) && absolute(b)) return false
  return requestPathAndQuery(a) === requestPathAndQuery(b)
}

/** The entry is the event's own request (same method and URL), so it repeats what Request already shows. */
function isOwnRequestEntry(event: BrowserReplayEvent, entry: BrowserReplayNetworkEntry): boolean {
  const method = (event.snapshot?.request_method || event.method || '').toUpperCase()
  const entryMethod = (entry.method || 'GET').toUpperCase()
  if (!method || method !== entryMethod) return false
  // The wire URL when captured (it carries the host), else the scripted target.
  const requestURL = event.snapshot?.request_url || event.target || ''
  const entryURL = entry.url || ''
  return Boolean(entryURL && requestURL) && sameRequestURL(requestURL, entryURL)
}

/**
 * The Network tab shows captured resources, network entries and page artifacts;
 * without any it is hidden. A lone network entry that is the event's own request
 * (a plain HTTP or Aura POST) adds nothing, so it does not earn the tab either.
 */
export function hasNetworkEvidence(event: BrowserReplayEvent): boolean {
  if (resourceCount(event) > 0 || event.snapshot?.omitted_resources || hasPageArtifacts(event)) return true
  const entries = replayNetworkEntries(event) || []
  return entries.length > 1 || (entries.length === 1 && !isOwnRequestEntry(event, entries[0]))
}

/** The Raw tab: retention notices, the raw protocol evidence, and a snapshot without request/response fields. */
export function hasRawEvidence(event: BrowserReplayEvent): boolean {
  return Boolean(event.protocol_evidence_omitted_count)
    || Boolean(event.protocol_evidence)
    || Boolean(event.snapshot && !hasStructuredReplaySnapshot(event.snapshot))
}

export function availableInspectorTabs(event: BrowserReplayEvent): Set<RequestResponseInspectorTab> {
  const tabs = new Set<RequestResponseInspectorTab>(['response', 'request', 'correlation', 'timing'])
  if (hasNetworkEvidence(event)) tabs.add('network')
  if (hasRawEvidence(event)) tabs.add('raw')
  return tabs
}

/** The tab to show: the one chosen, unless this event has nothing for it. */
export function effectiveInspectorTab(event: BrowserReplayEvent, selected: RequestResponseInspectorTab): RequestResponseInspectorTab {
  return availableInspectorTabs(event).has(selected) ? selected : 'response'
}

/** Count badges, only where they are cheap and meaningful; 0 shows none. */
export function inspectorTabCount(event: BrowserReplayEvent, tab: RequestResponseInspectorTab, variableDiff: ReplayVariableDiff): number {
  if (tab === 'correlation') return Object.keys(event.extractions || {}).length + Object.keys(variableDiff).length
  if (tab === 'network') return replayNetworkEntries(event)?.length || resourceCount(event)
  return 0
}

/**
 * The diagnosis cause repeats the error the verdict already shows (the engine
 * often sets one from the other), so it is left out: same text, or one holds
 * the other entirely.
 */
export function diagnosisCauseRepeatsError(cause: string | null | undefined, error: string | null | undefined): boolean {
  const c = (cause || '').trim()
  const e = (error || '').trim()
  if (!c || !e) return false
  return c === e || c.includes(e) || e.includes(c)
}

/** The header's status: the protocol status on success; on failure its status code, if any. */
export function inspectorStatusLabel(event: BrowserReplayEvent): string {
  if (event.success) return `HTTP ${event.status_code || 'OK'}`
  return event.status_code ? `HTTP ${event.status_code}` : 'Failed'
}

export function formatMillisecondsAsSecondsLabel(valueMs: number | null | undefined, fallbackMs = 0): string {
  const normalized = Number.isFinite(valueMs) ? Number(valueMs) : fallbackMs
  return `${(normalized / 1000).toLocaleString(undefined, { maximumFractionDigits: 3 })}s`
}
