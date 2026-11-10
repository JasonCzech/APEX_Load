import type { BrowserReplayEvent, BrowserReplayNetworkEntry, ReplayHARBody } from '../types/replay'
import type { ScriptResourceSnapshot } from '../types/scripts'

// The Network tab's one table: every request the action made, from the
// network entries (which carry the trace hops: HAR phases and transport trace)
// and the snapshot's captured resources. An HTTP replay records a captured
// resource and, when network capture is on, a network entry for the same
// fetch; those merge into one row. Nothing here is invented: a column the
// evidence does not carry stays unknown.

export type InspectorNetworkType = 'document' | 'script' | 'style' | 'font' | 'image' | 'xhr' | 'other'

export const INSPECTOR_NETWORK_TYPE_LABELS: Record<InspectorNetworkType, string> = {
  document: 'Documents',
  script: 'Scripts',
  style: 'Styles',
  font: 'Fonts',
  image: 'Images',
  xhr: 'XHR',
  other: 'Other',
}

const TYPE_ORDER: readonly InspectorNetworkType[] = ['document', 'script', 'style', 'font', 'image', 'xhr', 'other']

/** kept: the body is here; redacted: kept with values the engine scrubbed. */
export type InspectorNetworkBodyKind = 'kept' | 'redacted' | 'empty' | 'not_kept' | 'unknown'

export interface InspectorNetworkBody {
  kind: InspectorNetworkBodyKind
  label: string
}

/** self: the action's own request; page: a browser action's document; redirect: a hop the own request was redirected through. */
export type InspectorNetworkRole = 'self' | 'page' | 'redirect'

export interface InspectorNetworkRow {
  id: string
  /** Position among the network entries, which are in request order; null for a captured resource alone (kept sorted by URL, so its order is unknown). */
  hop: number | null
  method: string
  url: string
  /** Path and query, with the host when it differs from the action's. */
  display: string
  type: InspectorNetworkType
  status: number | null
  failed: boolean
  failureKind: string
  durationMs: number | null
  body: InspectorNetworkBody
  role: InspectorNetworkRole | null
  cacheHit: boolean
  entry?: BrowserReplayNetworkEntry
  resource?: ScriptResourceSnapshot
}

export type InspectorNetworkFilter = 'all' | 'failed' | InspectorNetworkType

export interface InspectorNetworkFilterOption {
  id: InspectorNetworkFilter
  label: string
  count: number
}

export interface InspectorNetworkPhase {
  name: string
  label: string
  durationMs: number | null
  unavailableReason: string
}

// ---------------------------------------------------------------------------
// URLs

const ABSOLUTE_URL = /^[a-z][a-z\d+.-]*:\/\//i

function parseURL(url: string): URL | null {
  try {
    return new URL(url.trim(), 'http://relative.invalid')
  } catch {
    return null
  }
}

/** The event's network entries: its own, else its snapshot's. */
export function replayNetworkEntries(event: BrowserReplayEvent) {
  return event.network_entries || event.snapshot?.network_entries
}

/** Path and query of a URL, so an absolute URL matches the same request written relative. */
export function pathAndQuery(url: string): string {
  const parsed = parseURL(url)
  return parsed ? `${parsed.pathname}${parsed.search}` : url.trim()
}

export function sameRequestURL(a: string, b: string): boolean {
  if (a.trim() === b.trim()) return true
  // Two absolute URLs must match whole (another host is another request).
  if (ABSOLUTE_URL.test(a.trim()) && ABSOLUTE_URL.test(b.trim())) return false
  return pathAndQuery(a) === pathAndQuery(b)
}

function hostOf(url: string): string {
  if (!ABSOLUTE_URL.test(url.trim())) return ''
  return parseURL(url)?.host.toLowerCase() || ''
}

/** The URL to show: path and query, with its host when that differs from the action's. */
export function inspectorNetworkDisplayURL(url: string, actionURL: string): string {
  const trimmed = url.trim()
  if (!trimmed) return ''
  if (!ABSOLUTE_URL.test(trimmed) || !parseURL(trimmed)) return trimmed
  const host = hostOf(trimmed)
  const actionHost = hostOf(actionURL)
  const path = pathAndQuery(trimmed)
  return host && actionHost && host === actionHost ? path : `${host}${path}`
}

// ---------------------------------------------------------------------------
// Type

/** A header's value by case-insensitive name, or "". */
export function headerValue(headers: Record<string, string> | undefined, name: string): string {
  const wanted = name.toLowerCase()
  for (const [key, value] of Object.entries(headers || {})) if (key.toLowerCase() === wanted) return String(value ?? '')
  return ''
}

function typeFromResourceType(resourceType: string): InspectorNetworkType | null {
  switch (resourceType.trim().toLowerCase()) {
    case 'document': return 'document'
    case 'script': return 'script'
    case 'stylesheet': case 'style': return 'style'
    case 'font': return 'font'
    case 'image': return 'image'
    case 'xhr': case 'fetch': return 'xhr'
    case '': return null
    default: return 'other'
  }
}

function typeFromContentType(contentType: string): InspectorNetworkType | null {
  const mime = contentType.split(';')[0].trim().toLowerCase()
  if (!mime) return null
  if (mime === 'text/html' || mime === 'application/xhtml+xml') return 'document'
  if (mime.includes('javascript') || mime.includes('ecmascript')) return 'script'
  if (mime === 'text/css') return 'style'
  if (mime.startsWith('font/') || mime.includes('font-woff') || mime === 'application/vnd.ms-fontobject') return 'font'
  if (mime.startsWith('image/')) return 'image'
  return null
}

function typeFromExtension(url: string): InspectorNetworkType | null {
  const path = (parseURL(url)?.pathname || '').toLowerCase()
  const match = /\.([a-z0-9]+)$/.exec(path)
  switch (match?.[1]) {
    case 'html': case 'htm': return 'document'
    case 'js': case 'mjs': return 'script'
    case 'css': return 'style'
    case 'woff': case 'woff2': case 'ttf': case 'otf': case 'eot': return 'font'
    case 'png': case 'jpg': case 'jpeg': case 'gif': case 'svg': case 'webp': case 'ico': case 'avif': case 'bmp': return 'image'
    default: return null
  }
}

/** The browser's resource type, else the response's content type, else the URL's extension; other when none says. */
export function inspectorNetworkType(input: { resourceType?: string; contentType?: string; url?: string }): InspectorNetworkType {
  return typeFromResourceType(input.resourceType || '')
    ?? typeFromContentType(input.contentType || '')
    ?? typeFromExtension(input.url || '')
    ?? 'other'
}

// ---------------------------------------------------------------------------
// Body

const KEPT: InspectorNetworkBody = { kind: 'kept', label: 'Kept' }
const NOT_KEPT: InspectorNetworkBody = { kind: 'not_kept', label: 'Not kept' }
const UNKNOWN: InspectorNetworkBody = { kind: 'unknown', label: 'Unknown' }

function resourceBody(resource: ScriptResourceSnapshot): InspectorNetworkBody {
  switch (resource.response_body_state) {
    case 'captured': return resource.response_body ? KEPT : { kind: 'empty', label: 'Empty' }
    case 'empty': return { kind: 'empty', label: 'Empty' }
    case 'redacted': return resource.response_body ? { kind: 'redacted', label: 'Kept, redacted' } : { kind: 'not_kept', label: 'Redacted' }
    case 'not_captured': return NOT_KEPT
    default: return UNKNOWN
  }
}

function harBody(body: ReplayHARBody | null | undefined): InspectorNetworkBody {
  if (!body || typeof body !== 'object') return UNKNOWN
  switch (body.state) {
    case 'retained':
      if (body.text) return KEPT
      return body.size_bytes === 0 ? { kind: 'empty', label: 'Empty' } : UNKNOWN
    case 'size_limit': return { kind: 'not_kept', label: 'Not kept (size limit)' }
    case 'total_limit': return { kind: 'not_kept', label: 'Not kept (total limit)' }
    case 'binary': return { kind: 'not_kept', label: 'Not kept (binary)' }
    case 'redacted': return body.text ? { kind: 'redacted', label: 'Kept, redacted' } : { kind: 'not_kept', label: 'Redacted' }
    case 'unavailable': return NOT_KEPT
    default: return UNKNOWN
  }
}

/** The own request's body, as the snapshot says (it is shown on Response). */
function snapshotBody(event: BrowserReplayEvent): InspectorNetworkBody {
  switch (event.snapshot?.response_body_state || event.response_body_state) {
    case 'captured': return KEPT
    case 'partial': return { kind: 'kept', label: 'Kept, partial' }
    case 'empty': return { kind: 'empty', label: 'Empty' }
    case 'redacted': return { kind: 'redacted', label: 'Kept, redacted' }
    case 'not_captured': return NOT_KEPT
    case 'not_retained': case 'expired': return { kind: 'not_kept', label: 'Not kept (expired)' }
    default: return UNKNOWN
  }
}

// ---------------------------------------------------------------------------
// Status

function validStatus(value: unknown): number | null {
  return typeof value === 'number' && Number.isInteger(value) && value > 0 ? value : null
}

function isFailed(status: number | null, failureKind: string, entry?: BrowserReplayNetworkEntry): boolean {
  if (failureKind) return true
  if (entry?.browser_evidence?.state === 'failed') return true
  return status !== null && status >= 400
}

// ---------------------------------------------------------------------------
// Rows

function actionMethod(event: BrowserReplayEvent): string {
  return (event.snapshot?.request_method || event.method || '').toUpperCase()
}

/** The wire URL when captured (it carries the host), else the scripted target. */
function actionURL(event: BrowserReplayEvent): string {
  return event.snapshot?.request_url || event.target || ''
}

/** The entry is the event's own request: same method and URL. */
export function isOwnRequestEntry(event: BrowserReplayEvent, entry: BrowserReplayNetworkEntry): boolean {
  const method = actionMethod(event)
  const url = actionURL(event)
  const entryURL = entry.url || ''
  if (!method || method !== (entry.method || 'GET').toUpperCase()) return false
  return Boolean(entryURL && url) && sameRequestURL(url, entryURL)
}

/** The entry is a browser action's document: the page it targets or ended on. */
export function isPageDocumentEntry(event: BrowserReplayEvent, entry: BrowserReplayNetworkEntry): boolean {
  const entryURL = entry.url || ''
  if ((entry.resource_type || '').toLowerCase() !== 'document' || !entryURL) return false
  const pageURLs = [event.snapshot?.request_url, event.target, event.page_url, event.snapshot?.page_url]
  return pageURLs.some(url => typeof url === 'string' && url.trim() !== '' && sameRequestURL(url, entryURL))
}

/** The next entry is where the previous one redirected: its own redirect link, or a 3xx whose Location resolves to it. */
function redirectsTo(previous: BrowserReplayNetworkEntry, next: BrowserReplayNetworkEntry): boolean {
  const from = next.browser_evidence?.redirect_from_request_id
  if (from && previous.browser_evidence?.request_id) return from === previous.browser_evidence.request_id
  const status = validStatus(previous.status_code)
  if (status === null || status < 300 || status >= 400) return false
  const location = headerValue(previous.response_headers, 'location').trim()
  if (!location || !next.url) return false
  const previousURL = (previous.url || '').trim()
  // Against a relative previous URL only the path can be compared.
  if (!ABSOLUTE_URL.test(previousURL)) return ABSOLUTE_URL.test(location) ? sameRequestURL(location, next.url) : pathAndQuery(location) === pathAndQuery(next.url)
  try {
    return sameRequestURL(new URL(location, previousURL).href, next.url)
  } catch {
    return false
  }
}

function entryRoles(event: BrowserReplayEvent, entries: readonly BrowserReplayNetworkEntry[]): Array<InspectorNetworkRole | null> {
  const roles: Array<InspectorNetworkRole | null> = entries.map(() => null)
  let chainFrom = -1
  for (let index = 0; index < entries.length; index += 1) {
    const entry = entries[index]
    if (chainFrom < 0 && isOwnRequestEntry(event, entry)) {
      roles[index] = 'self'
      chainFrom = index
    }
  }
  if (chainFrom < 0) {
    const page = entries.findIndex(entry => isPageDocumentEntry(event, entry))
    if (page >= 0) {
      roles[page] = 'page'
      chainFrom = page
    }
  }
  if (chainFrom < 0) return roles
  for (let index = chainFrom + 1; index < entries.length; index += 1) {
    if (roles[index] !== null || !redirectsTo(entries[index - 1], entries[index])) break
    roles[index] = 'redirect'
  }
  return roles
}

function statusesAgree(entry: BrowserReplayNetworkEntry, resource: ScriptResourceSnapshot): boolean {
  const entryStatus = validStatus(entry.status_code)
  const resourceStatus = validStatus(resource.response_status)
  if (entryStatus !== null || resourceStatus !== null) return entryStatus === resourceStatus
  return (entry.failure_kind || '') === (resource.failure_kind || '')
}

/**
 * Pairs each captured resource with the network entry for the same fetch: a
 * GET to the very same URL. Same-URL fetches pair in order only when both
 * sides have as many and every pair's status agrees; otherwise they stay
 * separate rows, since a wrong merge would put one fetch's body on another's
 * timing. Returns the entry index each resource merges into, or -1.
 */
export function matchResourcesToEntries(
  entries: readonly BrowserReplayNetworkEntry[],
  resources: readonly ScriptResourceSnapshot[],
  excluded: ReadonlySet<number> = new Set(),
): number[] {
  const byURL = new Map<string, { entries: number[]; resources: number[] }>()
  const group = (url: string) => {
    const existing = byURL.get(url)
    if (existing) return existing
    const created = { entries: [] as number[], resources: [] as number[] }
    byURL.set(url, created)
    return created
  }
  entries.forEach((entry, index) => {
    const url = (entry?.url || '').trim()
    if (!url || excluded.has(index) || (entry.method || 'GET').toUpperCase() !== 'GET') return
    group(url).entries.push(index)
  })
  resources.forEach((resource, index) => {
    const url = (resource?.request_url || '').trim()
    if (url) group(url).resources.push(index)
  })
  const matched = resources.map(() => -1)
  for (const { entries: entryIndexes, resources: resourceIndexes } of byURL.values()) {
    if (entryIndexes.length === 0 || entryIndexes.length !== resourceIndexes.length) continue
    if (!resourceIndexes.every((resourceIndex, i) => statusesAgree(entries[entryIndexes[i]], resources[resourceIndex]))) continue
    resourceIndexes.forEach((resourceIndex, i) => { matched[resourceIndex] = entryIndexes[i] })
  }
  return matched
}

function finiteDuration(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
}

/** One row per request the action made: network entries in their order, then captured resources no entry matched. */
export function inspectorNetworkRows(event: BrowserReplayEvent): InspectorNetworkRow[] {
  const entries = (replayNetworkEntries(event) || []).filter((entry): entry is BrowserReplayNetworkEntry => Boolean(entry && typeof entry === 'object'))
  const resources = (event.snapshot?.resources || []).filter((resource): resource is ScriptResourceSnapshot => Boolean(resource && typeof resource === 'object'))
  const ownURL = actionURL(event) || event.page_url || ''
  const roles = entryRoles(event, entries)
  const ownIndexes = new Set(roles.flatMap((role, index) => (role ? [index] : [])))
  const matched = matchResourcesToEntries(entries, resources, ownIndexes)
  const resourceFor = new Map<number, ScriptResourceSnapshot>()
  matched.forEach((entryIndex, resourceIndex) => { if (entryIndex >= 0) resourceFor.set(entryIndex, resources[resourceIndex]) })

  const rows: InspectorNetworkRow[] = entries.map((entry, index) => {
    const resource = resourceFor.get(index)
    const role = roles[index]
    const url = entry.url || resource?.request_url || ''
    const status = validStatus(entry.status_code) ?? validStatus(resource?.response_status)
    const failureKind = entry.failure_kind || resource?.failure_kind || ''
    const harResponse = harBody(entry.har?.response_body)
    const body = resource ? resourceBody(resource)
      : harResponse.kind !== 'unknown' ? harResponse
        : role === 'self' || role === 'page' ? snapshotBody(event) : UNKNOWN
    return {
      id: `entry-${index}`,
      hop: index + 1,
      method: (entry.method || 'GET').toUpperCase(),
      url,
      display: inspectorNetworkDisplayURL(url, ownURL),
      type: inspectorNetworkType({
        resourceType: entry.resource_type,
        contentType: headerValue(entry.response_headers, 'content-type') || headerValue(resource?.response_headers, 'content-type') || entry.har?.response_body?.mime_type,
        url,
      }),
      status,
      failed: isFailed(status, failureKind, entry),
      failureKind,
      durationMs: finiteDuration(entry.duration_ms),
      body,
      role,
      cacheHit: Boolean(resource?.cache_hit || entry.browser_evidence?.cache === true),
      entry,
      resource,
    }
  })
  resources.forEach((resource, index) => {
    if (matched[index] >= 0) return
    const url = resource.request_url || ''
    const status = validStatus(resource.response_status)
    const failureKind = resource.failure_kind || ''
    rows.push({
      id: `resource-${index}`,
      hop: null,
      // Captured resources are the page's GET sub-resources.
      method: 'GET',
      url,
      display: inspectorNetworkDisplayURL(url, ownURL),
      type: inspectorNetworkType({ contentType: headerValue(resource.response_headers, 'content-type'), url }),
      status,
      failed: isFailed(status, failureKind),
      failureKind,
      durationMs: null,
      body: resourceBody(resource),
      role: null,
      cacheHit: Boolean(resource.cache_hit),
      resource,
    })
  })
  return rows
}

// ---------------------------------------------------------------------------
// Counts, filters, summary

export interface InspectorNetworkCounts {
  total: number
  failed: number
}

/** The tab badge: the table's rows and its failed rows. */
export function inspectorNetworkCounts(event: BrowserReplayEvent): InspectorNetworkCounts {
  const rows = inspectorNetworkRows(event)
  return { total: rows.length, failed: rows.filter(row => row.failed).length }
}

/** The table has a request other than the action's own (and its redirect hops). */
export function hasInspectorNetworkSubresources(event: BrowserReplayEvent): boolean {
  return inspectorNetworkRows(event).some(row => row.role === null)
}

/** All, Failed (when any failed) and each type present, when more than one is. */
export function inspectorNetworkFilters(rows: readonly InspectorNetworkRow[]): InspectorNetworkFilterOption[] {
  const options: InspectorNetworkFilterOption[] = [{ id: 'all', label: 'All', count: rows.length }]
  const failed = rows.filter(row => row.failed).length
  if (failed > 0) options.push({ id: 'failed', label: 'Failed', count: failed })
  const byType = new Map<InspectorNetworkType, number>()
  for (const row of rows) byType.set(row.type, (byType.get(row.type) || 0) + 1)
  if (byType.size > 1) {
    for (const type of TYPE_ORDER) {
      const count = byType.get(type)
      if (count) options.push({ id: type, label: INSPECTOR_NETWORK_TYPE_LABELS[type], count })
    }
  }
  return options
}

export function filterInspectorNetworkRows(rows: readonly InspectorNetworkRow[], filter: InspectorNetworkFilter): InspectorNetworkRow[] {
  if (filter === 'all') return [...rows]
  if (filter === 'failed') return rows.filter(row => row.failed)
  return rows.filter(row => row.type === filter)
}

/** "8 bodies kept · 24 not kept", plus resources capture limits left out; null when nothing is known. */
export function inspectorNetworkBodySummary(rows: readonly InspectorNetworkRow[], omittedResources = 0): string | null {
  const kept = rows.filter(row => row.body.kind === 'kept' || row.body.kind === 'redacted').length
  const notKept = rows.filter(row => row.body.kind === 'not_kept').length
  const parts: string[] = []
  if (kept + notKept > 0) {
    parts.push(`${kept} ${kept === 1 ? 'body' : 'bodies'} kept`)
    parts.push(`${notKept} not kept`)
  }
  const omitted = Number.isSafeInteger(omittedResources) && omittedResources > 0 ? omittedResources : 0
  // "more" only beside requests the table lists.
  if (omitted > 0) parts.push(`${omitted}${rows.length > 0 ? ' more' : ''} ${omitted === 1 ? 'request' : 'requests'} not recorded (capture limits)`)
  return parts.length ? parts.join(' · ') : null
}

// ---------------------------------------------------------------------------
// Detail

const PHASE_LABELS: Record<string, string> = {
  dns: 'DNS',
  connect: 'Connect',
  tls: 'TLS',
  ssl: 'TLS',
  send: 'Send',
  request_wait: 'Waiting',
  wait: 'Waiting',
  response: 'Receive',
  receive: 'Receive',
}

/** The row's timing phases, as its HAR exchange or browser evidence reported them. */
export function inspectorNetworkPhases(row: Pick<InspectorNetworkRow, 'entry'>): InspectorNetworkPhase[] {
  const entry = row.entry
  if (!entry) return []
  const phases = entry.har?.phases?.length ? entry.har.phases : entry.browser_evidence?.phases
  if (!Array.isArray(phases)) return []
  return phases.flatMap(phase => {
    if (!phase || typeof phase.name !== 'string' || !phase.name) return []
    const { start_ms: start, end_ms: end } = phase
    const known = typeof start === 'number' && typeof end === 'number' && Number.isFinite(start) && Number.isFinite(end) && end >= start
    return [{
      name: phase.name,
      label: PHASE_LABELS[phase.name] || phase.name,
      durationMs: known ? end - start : null,
      unavailableReason: typeof phase.unavailable_reason === 'string' ? phase.unavailable_reason : '',
    }]
  })
}

/** The row's kept response body and the headers that type it, or null when none was kept. */
export function inspectorNetworkKeptBody(row: Pick<InspectorNetworkRow, 'entry' | 'resource' | 'body'>): { text: string; headers: Record<string, string> } | null {
  if (row.body.kind !== 'kept' && row.body.kind !== 'redacted') return null
  if (row.resource?.response_body) return { text: row.resource.response_body, headers: row.resource.response_headers || {} }
  const har = row.entry?.har?.response_body
  if (har?.text) {
    const headers = { ...(row.entry?.response_headers || {}) }
    if (har.mime_type && !headerValue(headers, 'content-type')) headers['Content-Type'] = har.mime_type
    return { text: har.text, headers }
  }
  return null
}

export function inspectorNetworkRoleLabel(role: InspectorNetworkRole | null): string {
  switch (role) {
    case 'self': return 'this request'
    case 'page': return 'this page'
    case 'redirect': return 'redirect'
    default: return ''
  }
}
