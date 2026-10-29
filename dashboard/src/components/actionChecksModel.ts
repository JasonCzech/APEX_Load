import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptAssertionDefinition as Assertion, ScriptExtractorDefinition as Extractor } from '../types/scripts'
import { evaluateExtractor } from './extractorMatch'
import { headersAndBodySurface, setCookieSurface } from './extractorSurface'
import { appliedReplayExtractions } from './replayExtractionValues'

// What the checks tab offers: checks added by what they test (the DSL's
// assertion types stay as they are), each with labelled fields, and each
// check's and saved value's result against the response the last replay of
// the step captured. Edits are re-checked against that response, so most
// results need no new replay.

export type CheckFieldInput = 'text' | 'reference' | 'number' | 'status' | 'source' | 'checkbox'

export interface CheckField {
  key: 'expected' | 'value' | 'max' | 'source' | 'case_insensitive'
  label: string
  input: CheckFieldInput
  /** The syntax the field takes, when it has one. */
  placeholder?: string
}

export interface CheckKind {
  type: string
  /** What the check asks, as the Add menu words it. */
  question: string
  fields: CheckField[]
  /** Listed under More checks. */
  more?: boolean
  /** A new check of this kind, from the last replay where it has the answer. */
  create: (event?: BrowserReplayEvent) => Assertion
}

const text = (label: string, placeholder?: string): CheckField => ({ key: 'value', label, input: 'text', placeholder })
const pattern = text('Pattern', 'RE2 pattern')
const selector = text('Selector', 'CSS selector')
const reference = (key: 'value' | 'expected', label: string): CheckField => ({ key, label, input: 'reference' })
const count = (label: string): CheckField => ({ key: 'expected', label, input: 'number' })
const IN_RESPONSE: CheckField[] = [reference('value', 'Text'), { key: 'source', label: 'Look in', input: 'source' }, { key: 'case_insensitive', label: 'Ignore case', input: 'checkbox' }]
const plain = (type: string, extra: Partial<Assertion> = {}) => () => ({ type, ...extra })
const within: CheckKind = {
  type: 'response_time_ms', question: 'Finishes within … ms', fields: [{ key: 'max', label: 'Milliseconds', input: 'number' }],
  // Twice the last replay's time, rounded up to 100 ms, leaves room for load.
  create: event => ({ type: 'response_time_ms', max: event ? Math.max(100, Math.ceil(event.duration_ms * 2 / 100) * 100) : 5000 }),
}

const HTTP: CheckKind[] = [
  { type: 'status_code', question: 'Status is …', fields: [{ key: 'expected', label: 'Status', input: 'status' }], create: event => ({ type: 'status_code', expected: replayStatus(event) || 200 }) },
  { type: 'text_contains', question: 'Response contains …', fields: IN_RESPONSE, create: plain('text_contains', { value: '' }) },
  { type: 'text_not_contains', question: 'Response does not contain …', fields: IN_RESPONSE, create: plain('text_not_contains', { value: '' }) },
  { type: 'header_contains', question: 'Header contains …', fields: [text('Header', 'Header name'), reference('expected', 'Contains')], create: plain('header_contains', { value: '', expected: '' }) },
  within,
  { type: 'body_contains', question: 'Body contains …', more: true, fields: [reference('value', 'Text'), { key: 'case_insensitive', label: 'Ignore case', input: 'checkbox' }], create: plain('body_contains', { value: '' }) },
  { type: 'body_not_contains', question: 'Body does not contain …', more: true, fields: [reference('value', 'Text'), { key: 'case_insensitive', label: 'Ignore case', input: 'checkbox' }], create: plain('body_not_contains', { value: '' }) },
  { type: 'text_matches', question: 'Response matches the pattern …', more: true, fields: [pattern, { key: 'source', label: 'Look in', input: 'source' }, { key: 'case_insensitive', label: 'Ignore case', input: 'checkbox' }], create: plain('text_matches', { value: '' }) },
  { type: 'text_not_matches', question: 'Response does not match the pattern …', more: true, fields: [pattern, { key: 'source', label: 'Look in', input: 'source' }, { key: 'case_insensitive', label: 'Ignore case', input: 'checkbox' }], create: plain('text_not_matches', { value: '' }) },
  { type: 'body_regex_capture_equals', question: 'A pattern’s capture equals …', more: true, fields: [pattern, reference('expected', 'Equals')], create: plain('body_regex_capture_equals', { value: '', expected: '' }) },
]

const BY_FAMILY: Record<string, CheckKind[]> = {
  http: HTTP,
  browser: [
    { type: 'element_visible', question: 'Element is visible', fields: [selector], create: plain('element_visible', { value: '' }) },
    { type: 'element_text_equals', question: 'Element text is …', fields: [selector, reference('expected', 'Text')], create: plain('element_text_equals', { value: '', expected: '' }) },
    { type: 'page_title', question: 'Page title is …', fields: [reference('value', 'Title')], create: event => ({ type: 'page_title', value: event?.page_title || event?.snapshot?.page_title || '' }) },
    { type: 'url_contains', question: 'URL contains …', fields: [reference('value', 'Text')], create: plain('url_contains', { value: '' }) },
    { type: 'element_text_contains', question: 'Element text contains …', more: true, fields: [selector, reference('expected', 'Text')], create: plain('element_text_contains', { value: '', expected: '' }) },
    { type: 'body_contains', question: 'Page HTML contains …', more: true, fields: [reference('value', 'Text')], create: plain('body_contains', { value: '' }) },
    { type: 'body_not_contains', question: 'Page HTML does not contain …', more: true, fields: [reference('value', 'Text')], create: plain('body_not_contains', { value: '' }) },
    { ...within, more: true },
  ],
  database: [
    { type: 'row_count', question: 'Returns … rows', fields: [count('Rows')], create: plain('row_count', { expected: 1 }) },
    { type: 'rows_affected', question: 'Changes … rows', fields: [count('Rows')], create: plain('rows_affected', { expected: 1 }) },
    { type: 'scalar_equals', question: 'First value equals …', fields: [reference('expected', 'Value')], create: plain('scalar_equals', { expected: '' }) },
    { type: 'body_contains', question: 'Result contains …', more: true, fields: [reference('value', 'Text')], create: plain('body_contains', { value: '' }) },
    { type: 'body_not_contains', question: 'Result does not contain …', more: true, fields: [reference('value', 'Text')], create: plain('body_not_contains', { value: '' }) },
    { ...within, more: true },
  ],
  websocket: [
    { type: 'message_received', question: 'A message arrives', fields: [], create: plain('message_received') },
    { type: 'body_contains', question: 'Message contains …', fields: [reference('value', 'Text')], create: plain('body_contains', { value: '' }) },
    { type: 'close_code', question: 'Closes with code …', fields: [count('Code')], create: plain('close_code', { expected: 1000 }) },
    within,
  ],
  grpc: [
    { type: 'grpc_status', question: 'Status is …', fields: [count('Code')], create: plain('grpc_status', { expected: 0 }) },
    { type: 'message_count', question: 'Returns … messages', fields: [count('Messages')], create: plain('message_count', { expected: 1 }) },
    { type: 'body_contains', question: 'Response contains …', fields: [reference('value', 'Text')], create: plain('body_contains', { value: '' }) },
    { type: 'metadata_contains', question: 'Metadata contains …', fields: [reference('value', 'Text')], create: plain('metadata_contains', { value: '' }) },
    within,
  ],
  jms: [
    { type: 'message_received', question: 'A message arrives', fields: [], create: plain('message_received') },
    { type: 'body_contains', question: 'Body contains …', fields: [reference('value', 'Text')], create: plain('body_contains', { value: '' }) },
    { type: 'property_equals', question: 'Property equals …', fields: [text('Property'), reference('expected', 'Value')], create: plain('property_equals', { value: '', expected: '' }) },
    { type: 'header_equals', question: 'Header equals …', fields: [text('Header'), reference('expected', 'Value')], create: plain('header_equals', { value: '', expected: '' }) },
    { type: 'correlation_match', question: 'Correlation ID matches …', fields: [reference('value', 'Correlation ID')], create: plain('correlation_match', { value: '' }) },
    within,
  ],
  network: [
    { type: 'record_count', question: 'Returns … records', fields: [count('Records')], create: plain('record_count', { expected: 1 }) },
    { type: 'entry_count', question: 'Returns … entries', fields: [count('Entries')], create: plain('entry_count', { expected: 1 }) },
    { type: 'messages_received', question: 'Receives … messages', fields: [count('Messages')], create: plain('messages_received', { expected: 1 }) },
    { type: 'body_contains', question: 'Transcript contains …', fields: [reference('value', 'Text')], create: plain('body_contains', { value: '' }) },
    { type: 'body_not_contains', question: 'Transcript does not contain …', fields: [reference('value', 'Text')], create: plain('body_not_contains', { value: '' }) },
    { type: 'status_code', question: 'Status is …', fields: [count('Status')], create: plain('status_code', { expected: 0 }) },
    within,
  ],
}

/** The check family a script protocol uses. */
export function checkFamily(protocol: string): string {
  if (protocol.startsWith('jms')) return 'jms'
  return BY_FAMILY[protocol] ? protocol : 'http'
}

const HTTP_PROTOCOLS = new Set(['http', 'http/1.1', 'http/2', 'http/3'])

/** The checks the Add menu offers for a protocol: common ones first, then More checks. */
export function checkKinds(protocol: string): CheckKind[] {
  const family = checkFamily(protocol)
  if (family !== 'http') return BY_FAMILY[family]
  // Other protocols that answer like HTTP do not ignore case, and Aura is HTTP's alone.
  if (!HTTP_PROTOCOLS.has(protocol)) return HTTP.map(kind => ({ ...kind, fields: kind.fields.filter(field => field.key !== 'case_insensitive') }))
  return [...HTTP, { type: 'aura_actions_success', question: 'Aura actions succeed', more: true, fields: [], create: plain('aura_actions_success') }]
}

/** A check's kind; one the protocol does not offer (an imported or older type) gets generic fields. */
export function checkKindOf(protocol: string, assertion: Assertion): CheckKind {
  const known = checkKinds(protocol).find(kind => kind.type === assertion.type)
  if (known) return known
  const fields: CheckField[] = []
  if (assertion.value !== undefined) fields.push(text('Value'))
  if (assertion.expected !== undefined) fields.push(typeof assertion.expected === 'number' ? count('Expected') : reference('expected', 'Expected'))
  if (assertion.max !== undefined) fields.push({ key: 'max', label: 'Maximum', input: 'number' })
  return { type: assertion.type, question: `Check: ${assertion.type}`, fields, create: () => ({ type: assertion.type }) }
}

/** A status field's text as stored: a number for digits, the text for a reference. */
export function statusExpectation(text: string): number | string {
  return /^\d{1,9}$/.test(text.trim()) ? Number(text.trim()) : text
}

// ---------------------------------------------------------------------------
// Results against the last replay

/** The last replay of a step, for checking against what it captured. */
export interface ChecksReplay {
  event: BrowserReplayEvent
  /** The script changed since the replay ran. */
  stale: boolean
  /** A {{var.…}} reference as the replay had it before this step, or null when unknown. */
  resolve: (text: string) => string | null
}

/** Where in its action a replay event's step sits; a sequence's request may carry only its index. */
export function replayEventStepPath(event: BrowserReplayEvent): string[] {
  if (event.step_path?.length) return event.step_path
  return event.request_index != null ? [`requests[${event.request_index}]`] : []
}

/** The key of the script step a replay event ran (phase:index#path), or null outside the script's phases. */
export function replayEventStepKey(event: BrowserReplayEvent): string | null {
  if (!['init', 'action', 'teardown'].includes(event.phase)) return null
  const path = replayEventStepPath(event)
  // The API leaves out a zero action index.
  return `${event.phase}:${event.action_index ?? 0}${path.length ? `#${path.join('/')}` : ''}`
}

/** The last replay event of the step at phase, index and path. */
export function lastStepEvent(events: BrowserReplayEvent[], phase: string, actionIndex: number, stepPath: string[] = []): number {
  const path = stepPath.join('/')
  for (let at = events.length - 1; at >= 0; at--) {
    const event = events[at]
    // The API leaves out a zero action index.
    if (event.phase === phase && (event.action_index ?? 0) === actionIndex && replayEventStepPath(event).join('/') === path) return at
  }
  return -1
}

/** A {{var.…}} reader over the variables a replay had set; anything else is unknown. */
export function replayResolver(variables: Record<string, string>): (text: string) => string | null {
  return text => {
    let known = true
    const resolved = text.replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (_token, expression: string) => {
      const name = expression.startsWith('var.') ? expression.slice(4) : ''
      if (name && Object.prototype.hasOwnProperty.call(variables, name)) return variables[name]
      known = false
      return ''
    })
    return known ? resolved : null
  }
}

export interface CheckResult {
  state: 'passed' | 'failed' | 'unchecked'
  detail: string
}

function replayStatus(event?: BrowserReplayEvent): number | undefined {
  return event?.status_code || event?.snapshot?.response_status || undefined
}

function responseBody(event: BrowserReplayEvent): string | undefined {
  return event.snapshot?.response_body
}

function responseHeaders(event: BrowserReplayEvent): Record<string, string> | undefined {
  return event.snapshot?.response_headers
}

/**
 * Whether the replay kept the response body whole. Text missing from a body it
 * kept only part of, or redacted, may still be in the response.
 */
export function bodyIsWhole(event: BrowserReplayEvent): boolean {
  const state = event.snapshot?.response_body_state || event.response_body_state
  return state !== 'partial' && state !== 'redacted'
}

const PART_OF_BODY: CheckResult = { state: 'unchecked', detail: 'The replay kept only part of this response; checked when the script runs' }

/** The text an HTTP text check reads: the body, the header block, or both. */
function textSurface(event: BrowserReplayEvent, source: string | undefined): string | undefined {
  const body = responseBody(event)
  if (source === 'headers') return responseHeaders(event) ? headersAndBodySurface(responseHeaders(event)) : undefined
  if (source === 'all') return body === undefined ? undefined : `${headersAndBodySurface(responseHeaders(event))}${body}`
  return body
}

const quoted = (value: string) => `“${value.length > 60 ? `${value.slice(0, 60)}…` : value}”`

/**
 * A check's result against the response the last replay captured. Checks the
 * page or the engine evaluates beyond that response (element checks, counts)
 * take the step's own replay outcome, while the script has not changed.
 */
export function checkResult(assertion: Assertion, replay: ChecksReplay | null | undefined): CheckResult {
  if (!replay) return { state: 'unchecked', detail: 'Not replayed yet' }
  const { event } = replay
  const resolveText = (value: unknown): string | null => {
    const raw = value === undefined || value === null ? '' : String(value)
    return raw.includes('{{') ? replay.resolve(raw) : raw
  }
  const unresolved: CheckResult = { state: 'unchecked', detail: 'Uses a reference this response cannot answer; it is checked when the script runs' }
  const verdict = (passed: boolean, failure: string): CheckResult => passed ? { state: 'passed', detail: 'Passed' } : { state: 'failed', detail: `Failed: ${failure}` }
  const ignoreCase = Boolean(assertion.case_insensitive)
  const contains = (haystack: string, needle: string) => ignoreCase ? haystack.toLowerCase().includes(needle.toLowerCase()) : haystack.includes(needle)
  switch (assertion.type) {
    case 'status_code':
    case 'status_code_not': {
      const actual = replayStatus(event)
      if (!actual) return { state: 'unchecked', detail: 'The replay has no status' }
      const expected = resolveText(assertion.expected)
      if (expected === null) return unresolved
      if (!expected.trim()) return { state: 'unchecked', detail: 'Enter the status' }
      const matches = String(actual) === expected.trim()
      return assertion.type === 'status_code' ? verdict(matches, `got ${actual}`) : verdict(!matches, `got ${actual}`)
    }
    case 'response_time_ms':
      return !assertion.max ? { state: 'passed', detail: 'Passed: no limit set' } : verdict(event.duration_ms <= assertion.max, `took ${Math.round(event.duration_ms)} ms`)
    case 'body_contains':
    case 'body_not_contains':
    case 'text_contains':
    case 'text_not_contains': {
      const surface = assertion.type.startsWith('text_') ? textSurface(event, assertion.source) : responseBody(event)
      if (surface === undefined) return { state: 'unchecked', detail: 'The replay did not capture the response' }
      const needle = resolveText(assertion.value)
      if (needle === null) return unresolved
      if (!needle) return { state: 'unchecked', detail: 'Enter the text to look for' }
      const found = contains(surface, needle)
      // Only the headers are always kept whole.
      if (!found && assertion.source !== 'headers' && !bodyIsWhole(event)) return PART_OF_BODY
      return assertion.type.endsWith('not_contains') ? verdict(!found, `${quoted(needle)} is in the response`) : verdict(found, `${quoted(needle)} is not in the response`)
    }
    case 'text_matches':
    case 'text_not_matches': {
      const surface = textSurface(event, assertion.source)
      if (surface === undefined) return { state: 'unchecked', detail: 'The replay did not capture the response' }
      let pattern: RegExp
      try { pattern = new RegExp(assertion.value || '', ignoreCase ? 'i' : '') } catch { return { state: 'unchecked', detail: 'This pattern is checked when the script runs' } }
      const matched = pattern.test(surface)
      if (!matched && assertion.source !== 'headers' && !bodyIsWhole(event)) return PART_OF_BODY
      return assertion.type === 'text_matches' ? verdict(matched, 'no match in the response') : verdict(!matched, 'the response matches')
    }
    case 'header_contains': {
      const headers = responseHeaders(event)
      if (!headers) return { state: 'unchecked', detail: 'The replay did not capture the headers' }
      const name = (assertion.value || '').trim().toLowerCase()
      const value = Object.entries(headers).find(([header]) => header.toLowerCase() === name)?.[1]
      if (value === undefined) return { state: 'failed', detail: `Failed: no ${assertion.value || 'such'} header` }
      const expected = resolveText(assertion.expected)
      if (expected === null) return unresolved
      return verdict(value.includes(expected), `got ${quoted(value)}`)
    }
    case 'body_regex_capture_equals': {
      const body = responseBody(event)
      if (body === undefined) return { state: 'unchecked', detail: 'The replay did not capture the response' }
      const expected = resolveText(assertion.expected)
      if (expected === null) return unresolved
      let captured: string | undefined
      try { const match = new RegExp(assertion.value || '').exec(body); captured = match ? match[1] ?? match[0] : undefined } catch { return { state: 'unchecked', detail: 'This pattern is checked when the script runs' } }
      if (captured === undefined) return bodyIsWhole(event) ? { state: 'failed', detail: 'Failed: no match in the response' } : PART_OF_BODY
      return verdict(captured === expected, `captured ${quoted(captured)}`)
    }
    case 'page_title': {
      const title = event.page_title ?? event.snapshot?.page_title
      if (title === undefined) return { state: 'unchecked', detail: 'The replay did not capture the page title' }
      const expected = resolveText(assertion.value || (assertion.expected as string | undefined))
      if (expected === null) return unresolved
      return verdict(title === expected.trim(), `got ${quoted(title)}`)
    }
    case 'url_contains': {
      const url = event.page_url ?? event.snapshot?.page_url
      if (url === undefined) return { state: 'unchecked', detail: 'The replay did not capture the page URL' }
      const expected = resolveText(assertion.value)
      if (expected === null) return unresolved
      return verdict(url.includes(expected), `got ${quoted(url)}`)
    }
    default:
      if (replay.stale) return { state: 'unchecked', detail: 'Checked when the script runs; replay again for a result' }
      if (event.success) return { state: 'passed', detail: 'Passed in the last replay' }
      return (event.error_message || '').includes(assertion.type)
        ? { state: 'failed', detail: `Failed: ${event.error_message}` }
        : { state: 'unchecked', detail: 'The step failed in the last replay before this check could say' }
  }
}

/** A saved value's result: what the expression finds in the replayed response. */
export function savedValueResult(extractor: Extractor, replay: ChecksReplay | null | undefined): CheckResult {
  if (!replay) return { state: 'unchecked', detail: 'Not replayed yet' }
  const { event } = replay
  const headers = responseHeaders(event)
  const body = responseBody(event)
  const source = extractor.source || 'body'
  let surface: string | undefined
  if (source === 'body') surface = body
  else if (source === 'all') surface = body === undefined ? undefined : `${headersAndBodySurface(headers)}${body}`
  else if (source === 'cookies') surface = headers ? setCookieSurface(headers) : undefined
  else if (source === 'header' && extractor.type && extractor.type !== 'header') surface = headers ? headersAndBodySurface(headers) : undefined
  else if (source === 'header' && headers) {
    // A named header: its value is what is saved.
    const value = Object.entries(headers).find(([name]) => name.toLowerCase() === extractor.expression.trim().toLowerCase())?.[1]
    return value === undefined ? { state: 'failed', detail: 'No match: the response has no such header' } : { state: 'passed', detail: `Saves ${quoted(value)}` }
  }
  const applied = appliedReplayExtractions(event)
  const fromReplay = Object.prototype.hasOwnProperty.call(applied, extractor.name) ? applied[extractor.name] : undefined
  if (surface === undefined) {
    if (fromReplay !== undefined && !replay.stale) return { state: 'passed', detail: `Saved ${quoted(fromReplay)} in the last replay` }
    return { state: 'unchecked', detail: 'Checked when the script runs' }
  }
  const result = evaluateExtractor(extractor.type, extractor.expression, surface)
  if (!result.previewable) {
    if (fromReplay !== undefined && !replay.stale) return { state: 'passed', detail: `Saved ${quoted(fromReplay)} in the last replay` }
    return { state: 'unchecked', detail: 'This kind of expression is checked when the script runs' }
  }
  if (result.error) return { state: 'unchecked', detail: result.error }
  if (!result.matches.length) {
    if (source !== 'header' && source !== 'cookies' && !bodyIsWhole(event)) return PART_OF_BODY
    return extractor.on_miss === 'empty' ? { state: 'passed', detail: 'No match: saves an empty value' } : { state: 'failed', detail: 'No match in the response' }
  }
  if (extractor.mode === 'all') return { state: 'passed', detail: `Saves ${result.matches.length} values as ${extractor.name || 'name'}_1…` }
  // An offset, a length or a conversion changes what is saved from the match.
  if (extractor.save_offset || extractor.save_length || extractor.convert) return { state: 'passed', detail: `Finds ${quoted(result.matches[0])}; its offset, length or conversion changes what is saved` }
  return { state: 'passed', detail: `Saves ${quoted(result.matches[0])}${result.matches.length > 1 ? ` (${result.matches.length} matches)` : ''}` }
}
