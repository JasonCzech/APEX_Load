import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { importActionEntries } from './importReviewIssues'
import { actionExecutionBlockers } from './scriptActionHelpers'

export const HAR_REQUEST_BODY_NOT_EXPORTED = 'har_request_body_not_exported'

export interface BlockedHarActionItem {
  uid: string
  path: string
  section: 'Init' | 'Actions' | 'Teardown'
  name: string
  recordedBytes?: number
}

export interface FlattenedActionEntry {
  section: 'Init' | 'Actions' | 'Teardown'
  path: string
  seq: number
  action: ScriptActionDefinition
}

function sectionForPath(path: string): FlattenedActionEntry['section'] {
  if (path.startsWith('init[')) return 'Init'
  if (path.startsWith('teardown[')) return 'Teardown'
  return 'Actions'
}

// Containers (an HTTP sequence or a control step) send no request of their
// own, so the studio counts and searches only the requests inside them.
function isRequestContainer(action: ScriptActionDefinition): boolean {
  const requests = action.params?.requests
  return Boolean(action.control) || (Array.isArray(requests) && requests.length > 0)
}

// A recorded transaction's Start/End marker (dsl.TransactionMarker) times the
// requests between them and sends nothing itself.
const TRANSACTION_MARKER_TYPE = 'source_transaction'
export function isTransactionMarker(action: ScriptActionDefinition | undefined): boolean {
  return typeof action?.type === 'string' && action.type.trim().toLowerCase() === TRANSACTION_MARKER_TYPE
}

// The transaction a marker opens or closes: the call's first argument, as
// dsl.TransactionMarkerName reads it.
export function transactionMarkerName(action: ScriptActionDefinition): string {
  const args = action.params?.args
  const first = Array.isArray(args) ? args[0] : undefined
  return typeof first === 'string' ? first.trim() : ''
}

// The executable requests of a script in replay order, from the same walk the
// Author and Debug problem lists use, so every surface counts the same steps.
export function flattenScriptActions(script: ScriptDefinition): FlattenedActionEntry[] {
  let seq = 0
  return importActionEntries(script)
    .filter(entry => !isRequestContainer(entry.action) && !isTransactionMarker(entry.action))
    .map(entry => ({ section: sectionForPath(entry.path), path: entry.path, seq: ++seq, action: entry.action }))
}

// The recorded transactions a script times: the step transactions the import
// tags requests with and the names its Start/End markers carry.
export function scriptTransactionNames(script: ScriptDefinition): Set<string> {
  const names = new Set<string>()
  for (const entry of importActionEntries(script)) {
    const tagged = entry.action.metadata?.source_transaction
    if (typeof tagged === 'string' && tagged.trim()) names.add(tagged.trim())
    if (isTransactionMarker(entry.action)) {
      const name = transactionMarkerName(entry.action)
      if (name) names.add(name)
    }
  }
  return names
}

const VARIABLE_REFERENCE_PATTERN = /\{\{var\.([^{}]+?)\}\}/g

function collectVariableReferences(text: unknown, names: Set<string>) {
  if (typeof text !== 'string' || !text.includes('{{var.')) return
  for (const match of text.matchAll(VARIABLE_REFERENCE_PATTERN)) names.add(match[1])
}

// The {{var.*}} names a request sends in its target, body, headers or params.
// Computed once per request so a consumer search is a set lookup.
export function stepVariableReferences(action: ScriptActionDefinition): Set<string> {
  const names = new Set<string>()
  collectVariableReferences(action.target, names)
  collectVariableReferences(action.body, names)
  if (Array.isArray(action.headers)) {
    for (const header of action.headers) collectVariableReferences(header?.value, names)
  } else if (action.headers && typeof action.headers === 'object') {
    for (const value of Object.values(action.headers)) collectVariableReferences(value, names)
  }
  if (action.params && typeof action.params === 'object') {
    try {
      collectVariableReferences(JSON.stringify(action.params), names)
    } catch {
      // Unserializable params reference nothing the studio can show.
    }
  }
  return names
}

// The server marks a request whose body the browser left out of the HAR export
// with this execution blocker (pkg/convert/sample).
export function hasUnexportedBodyBlocker(action: ScriptActionDefinition | undefined): boolean {
  return Boolean(action) && actionExecutionBlockers(action!).includes(HAR_REQUEST_BODY_NOT_EXPORTED)
}

// The HAR entry a converted request came from, stamped at interactive import.
export function harEntryIndex(action: ScriptActionDefinition | undefined): number | undefined {
  const value = action?.metadata?.['har.entry_index']
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function findBlockedUnexportedBodyActions(script: ScriptDefinition | null | undefined, flattened?: readonly FlattenedActionEntry[]): BlockedHarActionItem[] {
  if (!script) return []
  const items: BlockedHarActionItem[] = []
  for (const entry of flattened || flattenScriptActions(script)) {
    if (!hasUnexportedBodyBlocker(entry.action)) continue
    const meta = entry.action.metadata || {}
    const uid = typeof meta.import_action_uid === 'string' ? meta.import_action_uid.trim() : ''
    const rawLen = meta['har.request.recorded_content_length']
    const recordedBytes = typeof rawLen === 'number' && Number.isFinite(rawLen) && rawLen > 0 ? rawLen : undefined
    items.push({
      uid,
      path: entry.path,
      section: entry.section,
      name: entry.action.name || entry.action.target || entry.path,
      recordedBytes,
    })
  }
  return items
}

// Request field names as the HAR pair diff reports them (requestFieldValues in
// pkg/convert/har/diff_inputs.go): query:<name>, header:<name>, form:<name>,
// or body, then a JSON path whose array positions fold into [] and whose
// identifier-shaped keys fold into *.
const FIELD_IGNORED_HEADERS = new Set([
  'accept', 'accept-encoding', 'accept-language', 'cache-control', 'connection', 'content-length', 'cookie', 'dnt', 'host',
  'if-modified-since', 'if-none-match', 'origin', 'pragma', 'priority', 'te', 'upgrade-insecure-requests', 'user-agent',
])
const FIELD_MAX_PER_REQUEST = 4096
const FIELD_MAX_NAME_LENGTH = 200
const FIELD_MAX_JSON_DEPTH = 32
const FIELD_MAX_BODY_BYTES = 4 << 20
const IDENTIFIER_KEY = /^(?:[A-Za-z0-9]{15}|[A-Za-z0-9]{18}|[0-9a-fA-F-]{16,}|[0-9]{6,})$/

function identifierSegment(key: string): boolean {
  return IDENTIFIER_KEY.test(key) && (key.match(/[0-9]/g) || []).length >= 5
}

function jsonShaped(text: string): boolean {
  const trimmed = text.trim()
  return trimmed !== '' && (trimmed[0] === '{' || trimmed[0] === '[')
}

// A template outside a JSON string makes the text invalid JSON. The server
// decodes it as a number; here it becomes the template string itself, so the
// field reads as templated.
function quoteJSONTemplates(text: string): string {
  let out = ''
  let inString = false
  let escaped = false
  for (let index = 0; index < text.length; index++) {
    const c = text[index]
    if (inString) {
      out += c
      if (escaped) escaped = false
      else if (c === '\\') escaped = true
      else if (c === '"') inString = false
      continue
    }
    if (c === '"') {
      inString = true
      out += c
      continue
    }
    if (c === '{' && text[index + 1] === '{') {
      const end = text.indexOf('}}', index)
      if (end >= 0) {
        out += JSON.stringify(text.slice(index, end + 2))
        index = end + 1
        continue
      }
    }
    out += c
  }
  return out
}

function decodeTemplatedJSON(text: string): { value: unknown } | null {
  if (!jsonShaped(text)) return null
  try {
    return { value: JSON.parse(text.trim()) }
  } catch {
    if (!text.includes('{{')) return null
    try {
      return { value: JSON.parse(quoteJSONTemplates(text).trim()) }
    } catch {
      return null
    }
  }
}

// Returns false when the walk stopped at the depth limit, so a field below it
// went unseen.
function jsonFields(path: string, value: unknown, depth: number, emit: (field: string, value: string) => void): boolean {
  if (depth > FIELD_MAX_JSON_DEPTH) return value === null || value === undefined || typeof value === 'boolean'
  let complete = true
  if (Array.isArray(value)) {
    for (const item of value) complete = jsonFields(`${path}[]`, item, depth + 1, emit) && complete
  } else if (value && typeof value === 'object') {
    for (const key of Object.keys(value).sort()) {
      complete = jsonFields(`${path}.${identifierSegment(key) ? '*' : key}`, (value as Record<string, unknown>)[key], depth + 1, emit) && complete
    }
  } else if (typeof value === 'string') {
    emit(path, value)
  } else if (typeof value === 'number') {
    emit(path, String(value))
  }
  return complete
}

function unescapeFormComponent(text: string): string | null {
  try {
    return decodeURIComponent(text.replace(/\+/g, ' '))
  } catch {
    return null
  }
}

// truncateFieldName cuts a field name to FIELD_MAX_NAME_LENGTH UTF-8 bytes and
// drops a character the cut splits, as the server's pair diff does, so a long
// non-ASCII name matches the field the diff reported.
export function truncateFieldName(field: string): string {
  if (field.length * 3 <= FIELD_MAX_NAME_LENGTH) return field
  let bytes = 0
  let end = 0
  for (const char of field) {
    const code = char.codePointAt(0)!
    const size = code < 0x80 ? 1 : code < 0x800 ? 2 : code < 0x10000 ? 3 : 4
    if (bytes + size > FIELD_MAX_NAME_LENGTH) return field.slice(0, end)
    bytes += size
    end += char.length
  }
  return field
}

// Returns false when a pair could not be decoded or nested JSON hit the depth
// limit, so a field may have gone unseen.
function formFields(prefix: string, encoded: string, emit: (field: string, value: string) => void): boolean {
  let complete = true
  for (const pair of encoded.split('&')) {
    if (!pair) continue
    const separator = pair.indexOf('=')
    const name = unescapeFormComponent(separator >= 0 ? pair.slice(0, separator) : pair)
    const value = unescapeFormComponent(separator >= 0 ? pair.slice(separator + 1) : '')
    if (name === null || value === null) {
      complete = false
      continue
    }
    const decoded = decodeTemplatedJSON(value)
    if (decoded) complete = jsonFields(prefix + name, decoded.value, 0, emit) && complete
    else emit(prefix + name, value)
  }
  return complete
}

// Reports every request field the action sends. Returns false when the
// inspection was partial (field budget, body size or depth limit, or a body it
// cannot parse), so the absence of a field proves nothing.
function actionRequestFields(action: ScriptActionDefinition, emit: (field: string, value: string) => void): boolean {
  const target = typeof action.target === 'string' ? action.target : ''
  if (!target.trim()) return typeof action.body !== 'string' || action.body === ''
  let budget = FIELD_MAX_PER_REQUEST
  let complete = true
  const visit = (field: string, value: string) => {
    if (budget <= 0) {
      complete = false
      return
    }
    budget--
    emit(truncateFieldName(field), value)
  }
  const queryStart = target.indexOf('?')
  if (queryStart >= 0) complete = formFields('query:', target.slice(queryStart + 1).split('#')[0], visit) && complete
  const headers = new Map<string, string>()
  for (const [name, value] of Object.entries(action.headers || {})) if (typeof value === 'string') headers.set(name, value)
  for (const header of action.header_list || []) {
    if (header && typeof header.name === 'string' && header.enabled !== false) headers.set(header.name, typeof header.value === 'string' ? header.value : '')
  }
  for (const name of Array.from(headers.keys()).sort()) {
    const lower = name.trim().toLowerCase()
    if (!lower || lower.startsWith(':') || lower.startsWith('sec-') || FIELD_IGNORED_HEADERS.has(lower)) continue
    let value = headers.get(name) || ''
    if (lower === 'content-type') value = value.split(';')[0]
    visit(`header:${lower}`, value.trim())
  }
  const body = typeof action.body === 'string' ? action.body : ''
  if (!body) return complete
  if (body.length > FIELD_MAX_BODY_BYTES) return false
  const decoded = decodeTemplatedJSON(body)
  if (decoded) return jsonFields('body', decoded.value, 0, visit) && complete
  if (jsonShaped(body) || /[\r\n]/.test(body) || !body.includes('=')) return false
  return formFields('form:', body, visit) && complete
}

export type RequestFieldDraftState = 'templated' | 'recorded' | 'not_found' | 'not_inspected'

// Whether the draft now sends a request field the HAR pair diff reported as
// sent as recorded: every value it sends there is a template, or one is still
// literal. A field no request in the draft sends is not_found only when every
// request was inspected in full; otherwise it is not_inspected, because the
// field may sit in the part that was not read.
export function requestFieldDraftStates(script: ScriptDefinition, fields: readonly string[]): Map<string, RequestFieldDraftState> {
  const wanted = new Set(fields)
  const states = new Map<string, RequestFieldDraftState>()
  if (wanted.size === 0) return states
  let complete = true
  for (const entry of flattenScriptActions(script)) {
    complete = actionRequestFields(entry.action, (field, value) => {
      if (!wanted.has(field)) return
      const templated = value.includes('{{')
      const current = states.get(field)
      if (!templated) states.set(field, 'recorded')
      else if (!current) states.set(field, 'templated')
    }) && complete
  }
  for (const field of wanted) if (!states.has(field)) states.set(field, complete ? 'not_found' : 'not_inspected')
  return states
}

// The converter counts recording-profile drops (host, content type and
// excluded Aura descriptors) into filtered_assets without a stat of their own;
// its warnings name each count. Those requests are neither static assets nor
// telemetry, so the funnel takes them out of the static share.
const RECORDING_PROFILE_DROP_WARNING = /recording profile ".*" left (\d+) (?:Aura )?request\(s\) out (?:by host or content type|because every action)/
export function recordingProfileDroppedRequests(warnings: readonly unknown[] | null | undefined): number {
  let total = 0
  for (const warning of warnings || []) {
    if (typeof warning !== 'string') continue
    const match = RECORDING_PROFILE_DROP_WARNING.exec(warning)
    const count = match ? Number(match[1]) : 0
    if (Number.isSafeInteger(count) && count > 0) total += count
  }
  return total
}
