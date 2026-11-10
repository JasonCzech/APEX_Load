import type { BrowserReplayEvent, BrowserReplayNetworkEntry, BrowserReplaySnapshot, ReplayHARPhase } from '../types/replay'
import type { ScriptExtractorDefinition } from '../types/scripts'
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

/**
 * The Raw tab: retention notices, the raw protocol evidence, a snapshot without
 * request/response fields, and a follower's rule-based diagnosis.
 */
export function hasRawEvidence(event: BrowserReplayEvent): boolean {
  return Boolean(event.protocol_evidence_omitted_count)
    || isFollowerFailure(event)
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

// ---------------------------------------------------------------------------
// Follow-on failures (the engine links a failure that follows the run's first one)

/** The event failed after the run's first failure, which the engine linked it to. */
export function isFollowerFailure(event: BrowserReplayEvent): boolean {
  return !event.success && Boolean(event.diagnosis?.follows_failure_event_id)
}

/** Names of {{var.NAME}} references the request still carried, as the engine listed them. */
export function unresolvedVariables(event: BrowserReplayEvent): string[] {
  const names = event.diagnosis?.unresolved_variables
  return Array.isArray(names) ? names.filter((name): name is string => typeof name === 'string' && name !== '') : []
}

function unresolvedSentence(names: string[]): string {
  if (names.length === 0) return ''
  const refs = names.map(name => `{{var.${name}}}`).join(', ')
  return names.length === 1
    ? `The request still contains ${refs} because that value was never set.`
    : `The request still contains ${refs} because those values were never set.`
}

/** A follower's verdict sentences: what happened first, then why this request failed. */
export function followerVerdictText(event: BrowserReplayEvent): { lead: string; reason: string } {
  const diagnosis = event.diagnosis
  const action = diagnosis?.follows_failure_action?.trim()
  const lead = action
    ? `This request failed after the run's first failure, ${action}.`
    : "This request failed after the run's first failure."
  const parts: string[] = []
  if (diagnosis?.follows_reason === 'aura_invalid_session') parts.push('Salesforce rejected the session (aura:invalidSession).')
  else if (diagnosis?.follows_reason === 'http_401') parts.push('The server answered 401.')
  else if (diagnosis?.follows_reason === 'http_403') parts.push('The server answered 403.')
  // Unset values are named whatever the leading reason: they explain the request too.
  const names = unresolvedVariables(event)
  if (names.length > 0) parts.push(unresolvedSentence(names))
  return { lead, reason: parts.join(' ') }
}

/** Position in events of the event with this id, or null when it is not retained. */
export function eventIndexById(events: readonly BrowserReplayEvent[], id: string | null | undefined): number | null {
  if (!id) return null
  const index = events.findIndex(event => event?.event_id === id)
  return index >= 0 ? index : null
}

/** A failure the Previous/Next failure buttons step through (async callbacks are left out). */
export function isNavigableFailure(event: BrowserReplayEvent | null | undefined): boolean {
  return Boolean(event && event.success === false && event.phase !== 'async')
}

/** The failures before and after the selected position, or null at either end. */
export function adjacentFailureIndexes(events: readonly BrowserReplayEvent[], index: number): { previous: number | null; next: number | null } {
  let previous: number | null = null
  for (let i = Math.min(index, events.length) - 1; i >= 0; i--) {
    if (isNavigableFailure(events[i])) { previous = i; break }
  }
  let next: number | null = null
  for (let i = Math.max(index + 1, 0); i < events.length; i++) {
    if (isNavigableFailure(events[i])) { next = i; break }
  }
  return { previous, next }
}

// ---------------------------------------------------------------------------
// The failing extractor

const FAILING_EXTRACTOR_PATTERN = /extractor "((?:[^"\\]|\\.)*)" (?:found no match|failed|output rejected)\b/

/** NAME from the engine's `extractor "NAME" found no match: …` (or failed / output rejected), unquoted. */
export function failingExtractorName(event: BrowserReplayEvent): string | null {
  if (event.success) return null
  for (const text of [event.error_message, event.diagnosis?.cause]) {
    const match = FAILING_EXTRACTOR_PATTERN.exec(text || '')
    if (!match?.[1]) continue
    try {
      const name = JSON.parse(`"${match[1]}"`) as unknown
      if (typeof name === 'string' && name) return name
    } catch {
      return match[1]
    }
  }
  return null
}

/** Sources the extractor tester can show (as the engine reads them); "" is the body. */
const TESTER_SOURCES: readonly string[] = ['', 'body', 'header', 'all', 'cookies']

/**
 * What the definition reads that the extractor tester cannot show (the URL or
 * the status code), or null when the tester can try it as defined.
 */
export function extractorTesterUnsupportedSurface(extractor: Pick<ScriptExtractorDefinition, 'source'> | null | undefined): string | null {
  const source = (extractor?.source || '').trim()
  if (TESTER_SOURCES.includes(source)) return null
  if (source === 'url') return 'the URL'
  if (source === 'status') return 'the status code'
  return `the “${source}” source`
}

const includesScope = (value: string | undefined) => (value || '').trim().toLowerCase() === 'include'

/**
 * What the definition applies that the tester does not (it tries the bare
 * expression on the step's final response): data-format transforms (DFEs) and
 * a response scope wider or narrower than the final response (pkg/protocol/http
 * response_set.go scopedCandidates). Empty when the tester reproduces it.
 */
export function extractorTesterUnappliedSettings(extractor: Pick<ScriptExtractorDefinition, 'dfes' | 'scope'> | null | undefined): string[] {
  if (!extractor) return []
  const settings: string[] = []
  const dfes = (extractor.dfes || []).map(dfe => String(dfe ?? '').trim()).filter(Boolean)
  if (dfes.length) settings.push(`its data-format transforms (${dfes.join(', ')})`)
  const scope = extractor.scope
  if (scope) {
    if (includesScope(scope.redirects)) settings.push('redirect hops')
    if (includesScope(scope.resources)) settings.push('page resources')
    if (scope.request_url?.trim()) settings.push('its request URL filter')
    if (scope.content_type?.trim()) settings.push('its Content-Type filter')
    if (scope.header_names?.some(name => String(name ?? '').trim())) settings.push('its header-name filter')
  }
  return settings
}

/**
 * Why the tester's Update cannot change the event's step's extractor in place
 * (a sentence fragment that follows "Could not update “NAME”: "), or "" when
 * it can. Unapplied or unparsable JSON edits come first: the structured steps
 * may still match the replay, so "the step changed" would send the user to
 * replay again instead of to the JSON.
 */
export function extractorUpdateBlocker(state: { onStep: boolean; jsonUnapplied: boolean; stepsInPlace: boolean; hasExtractor: boolean }): string {
  if (!state.onStep) return 'this response is not from a step of the script.'
  if (state.jsonUnapplied) return 'apply or discard the JSON edits first.'
  if (!state.stepsInPlace) return 'the step changed since this replay.'
  if (!state.hasExtractor) return 'the step no longer has it.'
  return ''
}

/** The extractor definition named name, if the step carries one. */
export function findExtractorDefinition(extractors: readonly ScriptExtractorDefinition[] | null | undefined, name: string | null): ScriptExtractorDefinition | null {
  if (!name || !Array.isArray(extractors)) return null
  return extractors.find(extractor => extractor?.name === name) ?? null
}

// ---------------------------------------------------------------------------
// Default sub-tab for a newly selected event

/** The view a failed event opens on; null for a passed one (the user's last pick holds). */
export function defaultInspectorTabForEvent(event: BrowserReplayEvent): RequestResponseInspectorTab | null {
  if (event.success) return null
  // A follower's own evidence is the rejected response; its cause is elsewhere,
  // so it opens on Response even when it also carries unset values.
  if (isFollowerFailure(event)) return 'response'
  if (unresolvedVariables(event).length > 0 || failingExtractorName(event)) return 'correlation'
  const snapshot = event.snapshot
  const hasResponse = Boolean(event.status_code) || Boolean(snapshot?.response_status)
    || Boolean(snapshot?.response_body)
    || Boolean(snapshot?.response_headers && Object.keys(snapshot.response_headers).length > 0)
  return hasResponse ? 'response' : 'timing'
}

// ---------------------------------------------------------------------------
// Copy as cURL

const shellQuote = (value: string) => `'${value.replace(/'/g, `'"'"'`)}'`

/** What retention appends to a snapshot string it cut (pkg/engine snapshot_store.go). */
const SNAPSHOT_TRUNCATED_MARKER = '...[truncated]'

/** The snapshot's request method, upper-cased; GET when it has none. */
function snapshotRequestMethod(snapshot: BrowserReplaySnapshot): string {
  return (snapshot.request_method || '').trim().toUpperCase() || 'GET'
}

/**
 * Why the snapshot's request is not the whole wire request: a body or URL
 * retention cut, headers it dropped, headers that could not be verified, or a
 * HEAD request's body (curl --head sends none). Empty when the copy is complete.
 */
export function curlCommandGaps(snapshot: BrowserReplaySnapshot | null | undefined): string[] {
  if (!snapshot) return []
  const gaps: string[] = []
  if (snapshot.request_url?.endsWith(SNAPSHOT_TRUNCATED_MARKER)) gaps.push('URL truncated by retention limits')
  if (snapshot.request_body?.endsWith(SNAPSHOT_TRUNCATED_MARKER)) gaps.push('body truncated by retention limits')
  if (snapshot.request_headers_omitted) gaps.push('some headers omitted or shortened by retention limits')
  if (snapshot.request_wire_headers_unavailable) gaps.push('wire headers could not be verified, so headers may be incomplete')
  if (snapshot.request_body && snapshotRequestMethod(snapshot) === 'HEAD') gaps.push('HEAD request body left out (curl --head sends none)')
  return gaps
}

/**
 * The wire request as one curl command, or null without a request URL. Values
 * are the snapshot's as captured (already redacted); content-length and
 * HTTP/2 pseudo headers are left for curl to set. --globoff stops curl reading
 * {} and [] in the URL ({{var.x}}, filter[0]=, the retention marker) as globs,
 * and HEAD uses --head (--request HEAD still waits for a body). The copy is the
 * command alone: an interactive zsh runs # lines as commands and cURL importers
 * reject them, so what makes it incomplete (curlCommandGaps) is said in a note
 * under the inspector header, which describes the copy button, instead. An
 * empty header value is written "Name;", curl's form for sending it empty
 * ("Name:" would remove it).
 */
export function buildCurlCommand(snapshot: BrowserReplaySnapshot | null | undefined): string | null {
  const url = snapshot?.request_url?.trim()
  if (!snapshot || !url) return null
  const method = snapshotRequestMethod(snapshot)
  const head = method === 'HEAD'
  // A plain token stays bare (--request POST); anything else is quoted.
  const parts = ['curl', '--globoff', head ? '--head' : `--request ${/^[A-Z][A-Z0-9_-]*$/.test(method) ? method : shellQuote(method)}`, `--url ${shellQuote(url)}`]
  let compressed = false
  for (const [name, value] of Object.entries(snapshot.request_headers || {})) {
    const key = name.trim()
    const lower = key.toLowerCase()
    if (!key || key.startsWith(':') || lower === 'content-length') continue
    const text = value == null ? '' : String(value)
    if (lower === 'accept-encoding' && /\b(?:gzip|br|deflate)\b/i.test(text)) compressed = true
    // curl reads "Name:" with nothing after it as "remove this header"; "Name;" sends it empty.
    parts.push(`--header ${shellQuote(text.trim() ? `${key}: ${text}` : `${key};`)}`)
  }
  if (compressed) parts.push('--compressed')
  // curl refuses --head with a body (two methods); the left-out body is a gap.
  if (snapshot.request_body && !head) parts.push(`--data-raw ${shellQuote(snapshot.request_body)}`)
  return parts.join(' \\\n  ')
}

// ---------------------------------------------------------------------------
// Timing bar

export type InspectorTimingSegmentKey = 'connect' | 'send' | 'wait' | 'receive' | 'rest' | 'total'

export interface InspectorTimingSegment {
  key: InspectorTimingSegmentKey
  label: string
  durationMs: number
  /** Share of the bar, 0..100. */
  widthPct: number
}

export interface InspectorTimingBar {
  totalMs: number
  /** The main request's phases are known; otherwise one total segment. */
  phased: boolean
  segments: InspectorTimingSegment[]
}

const TIMING_SEGMENT_LABELS: Record<InspectorTimingSegmentKey, string> = {
  connect: 'Connection setup',
  send: 'Send',
  wait: 'Waiting',
  receive: 'Receive',
  rest: 'Sub-resources and processing',
  total: 'Total',
}

type PhaseLike = Pick<ReplayHARPhase, 'name' | 'start_ms' | 'end_ms'>
type HopPhaseGroup = 'connect' | 'send' | 'wait' | 'receive'
const HOP_PHASE_GROUPS: readonly HopPhaseGroup[] = ['connect', 'send', 'wait', 'receive']

function hopPhases(entry: BrowserReplayNetworkEntry): PhaseLike[] {
  const phases = entry.har?.phases?.length ? entry.har.phases : entry.browser_evidence?.phases
  return Array.isArray(phases) ? (phases as PhaseLike[]).filter(Boolean) : []
}

function hopPhaseGroup(name: string | undefined): HopPhaseGroup | null {
  switch (name) {
    case 'dns': case 'connect': case 'tls': case 'ssl': return 'connect'
    case 'send': return 'send'
    case 'request_wait': case 'wait': return 'wait'
    case 'response': case 'receive': return 'receive'
    default: return null
  }
}

/** Length of the intervals' union: time two phases share counts once. */
function unionMs(intervals: ReadonlyArray<readonly [number, number]>): number {
  const sorted = [...intervals].sort((a, b) => a[0] - b[0])
  let total = 0
  let start = Number.NaN
  let end = Number.NaN
  for (const [from, to] of sorted) {
    if (Number.isFinite(end) && from <= end) {
      end = Math.max(end, to)
      continue
    }
    if (Number.isFinite(end)) total += end - start
    start = from
    end = to
  }
  return Number.isFinite(end) ? total + end - start : total
}

/**
 * Each group's time and the groups the hop reported. Overlapping phases count
 * once: a browser's connect (connectStart → connectEnd) already spans its TLS
 * handshake, while HTTP's connect and tls follow each other.
 */
function hopPhaseDurations(phases: PhaseLike[]): { durations: Record<HopPhaseGroup, number>; reported: Set<HopPhaseGroup> } {
  const intervals: Record<HopPhaseGroup, Array<[number, number]>> = { connect: [], send: [], wait: [], receive: [] }
  for (const phase of phases) {
    const group = hopPhaseGroup(phase.name)
    const { start_ms: start, end_ms: end } = phase
    if (!group || typeof start !== 'number' || typeof end !== 'number' || !Number.isFinite(start) || !Number.isFinite(end) || end < start) continue
    intervals[group].push([start, end])
  }
  const durations: Record<HopPhaseGroup, number> = { connect: 0, send: 0, wait: 0, receive: 0 }
  const reported = new Set<HopPhaseGroup>()
  for (const group of HOP_PHASE_GROUPS) {
    if (intervals[group].length === 0) continue
    reported.add(group)
    durations[group] = unionMs(intervals[group])
  }
  return { durations, reported }
}

/**
 * The entry is the action's main request: its own request (same method and
 * URL), or for a browser action the document it loaded (the page it targets
 * or ended on).
 */
function isMainRequestEntry(event: BrowserReplayEvent, entry: BrowserReplayNetworkEntry): boolean {
  if (isOwnRequestEntry(event, entry)) return true
  const entryURL = entry.url || ''
  if ((entry.resource_type || '').toLowerCase() !== 'document' || !entryURL) return false
  const pageURLs = [event.snapshot?.request_url, event.target, event.page_url, event.snapshot?.page_url]
  return pageURLs.some(url => typeof url === 'string' && url.trim() !== '' && sameRequestURL(url, entryURL))
}

/**
 * Started → Completed as one bar: the main request's phases (the hop that is
 * the event's own request, or a browser action's document) and, when the
 * action took longer, the rest as sub-resources and processing. Without a
 * main request with phases, a single bar of the total duration: another
 * resource's phases never stand in for the action's. A phase the hop did not
 * report (a browser has no separate send) gets no segment.
 */
export function inspectorTimingBar(event: BrowserReplayEvent): InspectorTimingBar {
  const eventMs = Number.isFinite(event.duration_ms) && event.duration_ms > 0 ? event.duration_ms : 0
  const main = (replayNetworkEntries(event) || []).find(entry => entry && hopPhases(entry).length > 0 && isMainRequestEntry(event, entry))
  const hop = main ? hopPhaseDurations(hopPhases(main)) : null
  const keys = hop ? HOP_PHASE_GROUPS.filter(key => hop.reported.has(key) && (key !== 'connect' || hop.durations.connect > 0)) : []
  const mainMs = hop ? keys.reduce((sum, key) => sum + hop.durations[key], 0) : 0
  if (!hop || mainMs <= 0) {
    return { totalMs: eventMs, phased: false, segments: [{ key: 'total', label: TIMING_SEGMENT_LABELS.total, durationMs: eventMs, widthPct: 100 }] }
  }
  const { durations } = hop
  // Less than a millisecond left over is rounding, not sub-resources.
  const restMs = eventMs - mainMs >= 1 ? eventMs - mainMs : 0
  const totalMs = Math.max(eventMs, mainMs)
  const segments: InspectorTimingSegment[] = keys
    .map(key => ({ key, label: TIMING_SEGMENT_LABELS[key], durationMs: durations[key], widthPct: (durations[key] / totalMs) * 100 }))
  if (restMs > 0) segments.push({ key: 'rest', label: TIMING_SEGMENT_LABELS.rest, durationMs: restMs, widthPct: (restMs / totalMs) * 100 })
  return { totalMs, phased: true, segments }
}

export function formatTimingMs(ms: number): string {
  const safe = Number.isFinite(ms) && ms > 0 ? ms : 0
  if (safe >= 1000) return `${(safe / 1000).toLocaleString('en-US', { maximumFractionDigits: 2 })} s`
  return `${safe.toLocaleString('en-US', { maximumFractionDigits: safe < 10 ? 1 : 0 })} ms`
}

/** The bar's accessible description. */
export function inspectorTimingBarLabel(bar: InspectorTimingBar): string {
  const total = `Total ${formatTimingMs(bar.totalMs)}`
  if (!bar.phased) return total
  return `${total}: ${bar.segments.map(segment => `${segment.label.toLowerCase()} ${formatTimingMs(segment.durationMs)}`).join(', ')}`
}

// ---------------------------------------------------------------------------
// HTML page insight (title, login page)

const NAMED_ENTITIES: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' }

function decodeBasicEntities(text: string): string {
  return text.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (whole, code: string) => {
    const lower = code.toLowerCase()
    if (lower.startsWith('#')) {
      const point = lower.startsWith('#x') ? Number.parseInt(lower.slice(2), 16) : Number.parseInt(lower.slice(1), 10)
      return Number.isInteger(point) && point > 0 && point <= 0x10ffff ? String.fromCodePoint(point) : whole
    }
    return NAMED_ENTITIES[lower] ?? whole
  })
}

function headerValue(headers: Record<string, string> | undefined, name: string): string {
  const wanted = name.toLowerCase()
  for (const [key, value] of Object.entries(headers || {})) if (key.toLowerCase() === wanted) return String(value ?? '')
  return ''
}

const HTML_TITLE_MAX = 120

export interface CapturedResponseBody {
  /** The captured body, without the marker retention appends to one it cut. */
  text: string
  /** Not the whole body: captured partially (an event stream's window) or cut by retention. */
  partial: boolean
}

/**
 * The event's captured response body and whether it is only part of the
 * response, or null without one. Retention cuts a long body (appending
 * "...[truncated]") but leaves its state "captured", so the marker counts as
 * partial too.
 */
export function capturedResponseBody(event: BrowserReplayEvent): CapturedResponseBody | null {
  const body = event.snapshot?.response_body
  if (!body) return null
  const cut = body.endsWith(SNAPSHOT_TRUNCATED_MARKER)
  const state = event.snapshot?.response_body_state || event.response_body_state
  return {
    text: cut ? body.slice(0, -SNAPSHOT_TRUNCATED_MARKER.length) : body,
    partial: cut || state === 'partial' || event.protocol_evidence?.response_content_state === 'collection_window',
  }
}

export interface HtmlPageInsight {
  /** The page's <title>, decoded and trimmed; empty when it has none. */
  title: string
  /** An unexpected login page: a password field or a sign-in title on a request that did not ask for one. */
  looksLikeLogin: boolean
  /** Read from part of the page only (capturedResponseBody). */
  partial: boolean
}

/** The URL's host name, or "" for a relative or unparsable URL. */
function requestHostname(url: string): string {
  const trimmed = url.trim()
  if (!/^[a-z][a-z\d+.-]*:\/\//i.test(trimmed)) return ''
  try {
    return new URL(trimmed).hostname
  } catch {
    return ''
  }
}

// Hosts that serve a sign-in page at their root (login.salesforce.com, the
// Salesforce sandbox login host, login.example.com).
const LOGIN_HOST = /^(?:log-?in|sign-?in)\.|^test\.salesforce\.com$/i

// A Salesforce org's own hosts (My Domain, its sandboxes, Lightning): their
// root answers an unauthenticated GET with the org's login page, so a script's
// first step expects one there.
const SALESFORCE_ORG_HOST = /\.(?:(?:sandbox\.)?my\.salesforce\.com|lightning\.force\.com)$/i

/**
 * What an HTML response is: its title, and whether it looks like a login page
 * the request did not ask for (a GET to a URL whose path says "login", to a
 * login host such as login.salesforce.com, or to the root of a Salesforce org
 * host such as acme.my.salesforce.com, expects one). Only for captured
 * bodies (whole or partial); null otherwise or for non-HTML responses.
 */
export function htmlPageInsight(event: BrowserReplayEvent): HtmlPageInsight | null {
  const snapshot = event.snapshot
  const captured = capturedResponseBody(event)
  if (!snapshot || !captured) return null
  const body = captured.text
  const state = snapshot.response_body_state || event.response_body_state
  if (state && state !== 'captured' && state !== 'partial') return null
  const contentType = headerValue(snapshot.response_headers, 'content-type').toLowerCase()
  if (!contentType.includes('html') && !/^\s*<(?:!doctype\s+html|html)\b/i.test(body)) return null
  const titleMatch = /<title\b[^>]*>([\s\S]*?)<\/title\s*>/i.exec(body)
  let title = titleMatch ? decodeBasicEntities(titleMatch[1]).replace(/\s+/g, ' ').trim() : ''
  if (title.length > HTML_TITLE_MAX) title = `${title.slice(0, HTML_TITLE_MAX - 1).trimEnd()}…`
  const hasPassword = /<input\b[^>]*\btype\s*=\s*["']?password\b/i.test(body)
  const signInTitle = /\b(?:log ?in|sign ?in)\b/i.test(title)
  const method = (snapshot.request_method || event.method || '').toUpperCase()
  const url = snapshot.request_url || event.target || ''
  const path = requestPathAndQuery(url).split('?')[0]
  const host = requestHostname(url)
  const asksForLogin = method === 'GET' && (/login/i.test(path) || LOGIN_HOST.test(host)
    || (SALESFORCE_ORG_HOST.test(host) && (path === '/' || path === '')))
  return { title, looksLikeLogin: (hasPassword || signInTitle) && !asksForLogin, partial: captured.partial }
}

// ---------------------------------------------------------------------------
// Find in body

/** Case-insensitive, literal, non-overlapping occurrences of query in text. */
export function countLiteralMatches(text: string | null | undefined, query: string): number {
  const needle = query.toLowerCase()
  if (!text || !needle) return 0
  const haystack = text.toLowerCase()
  let count = 0
  for (let at = haystack.indexOf(needle); at >= 0; at = haystack.indexOf(needle, at + needle.length)) count++
  return count
}

export function formatFindResult(count: number, partial: boolean): string {
  const text = count === 0 ? 'No matches' : count === 1 ? '1 match' : `${count.toLocaleString('en-US')} matches`
  return partial ? `${text} (partial body)` : text
}
