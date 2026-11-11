import type { HTTPConfig, ScriptActionDefinition, ScriptHeaderEntry } from '../types/scripts'
import { isSensitiveCredentialPreviewKey, isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'

// Rows for the Request/Response inspector's header and form-field tables.
// Values are shown exactly as the event carries them: a value the engine
// redacted stays redacted, and masking here only ever hides more.

export type InspectorHeaderDirection = 'request' | 'response'

/** Which request the rows describe: the wire snapshot, or the resolved scripted request (event.headers/body). */
export type InspectorRequestView = 'wire' | 'scripted'

export type InspectorValueSourceKind =
  | 'literal' | 'default' | 'runtime' | 'jar' | 'variable' | 'unset'
  | 'parameter' | 'environment' | 'secret' | 'template' | 'added' | 'unknown'
  | 'script-jar'

export interface InspectorValueSource {
  kind: InspectorValueSourceKind
  label: string
  /** The authored reference, for a tooltip (never a value). */
  detail?: string
}

export interface InspectorHeaderRow {
  name: string
  value: string
  /** One of the direction's key headers, shown before "Show all". */
  key: boolean
  /** Hidden here (a secret-sourced value); never copied. */
  masked: boolean
  /** True when the backend snapshot already sent a [REDACTED]/[MASKED] placeholder. */
  serverRedacted?: boolean
  /** Request headers only. */
  source?: InspectorValueSource
}

export interface InspectorFormFieldRow {
  name: string
  value: string
  masked: boolean
  /** True when the backend snapshot already sent a [REDACTED]/[MASKED] placeholder. */
  serverRedacted?: boolean
  source: InspectorValueSource
}

/**
 * What the dashboard knows about how the request was authored. Pass it only
 * when the replay matches the current script; without it no row gets a source
 * (the tables then leave the Comes from column out).
 */
export interface InspectorAuthoredRequest {
  action: ScriptActionDefinition
  http?: HTTPConfig | null
}

export interface InspectorSourceContext {
  authored?: InspectorAuthoredRequest | null
  view: InspectorRequestView
  /** event.diagnosis.unresolved_variables: {{var.x}} references never set this run. */
  unresolvedVariables?: readonly string[]
}

export const MASKED_VALUE = '••••••••'

const KEY_HEADERS: Record<InspectorHeaderDirection, readonly string[]> = {
  response: ['content-type', 'content-length', 'content-encoding', 'location', 'set-cookie', 'cache-control', 'www-authenticate'],
  request: ['content-type', 'cookie', 'authorization', 'referer', 'origin', 'accept-encoding'],
}

// The HTTP runtime settings the client sends as headers when a step does not.
const RUNTIME_SETTING_HEADERS: ReadonlyArray<[string, keyof HTTPConfig]> = [
  ['user-agent', 'user_agent'],
  ['accept-encoding', 'accept_encoding'],
  ['accept-language', 'accept_language'],
]

const UNKNOWN: InspectorValueSource = { kind: 'unknown', label: 'Unknown' }
const COOKIE_JAR: InspectorValueSource = { kind: 'jar', label: 'Cookie jar' }
const SCRIPT_DEFAULT: InspectorValueSource = { kind: 'default', label: 'Script default' }

// Form field names that hold a password but that the shared credential
// classifier does not list (Salesforce's login form posts "pw").
const EXTRA_PASSWORD_FIELD_NAMES = new Set(['pw', 'pword'])

const SERVER_REDACTED_VALUE = /^\[(?:redacted|masked)\]$/i

/** Whether a value is a server-side redaction placeholder ([REDACTED] or [MASKED]). */
export function isServerRedactedValue(value: string | null | undefined): boolean {
  return typeof value === 'string' && SERVER_REDACTED_VALUE.test(value.trim())
}

function lower(name: string): string {
  return name.trim().toLowerCase()
}

/**
 * Splits headers into rows: a header the engine joined with newlines (it keeps
 * each Set-Cookie occurrence apart that way) becomes one row per occurrence.
 * Key headers come first in the direction's order; the rest keep their order.
 */
export function inspectorHeaderRows(
  headers: Readonly<Record<string, string>> | readonly InspectorHeaderRow[] | null | undefined,
  direction: InspectorHeaderDirection = 'request',
): InspectorHeaderRow[] {
  if (!headers) return []
  const keyOrder = KEY_HEADERS[direction]
  const rows: Array<{ row: InspectorHeaderRow, order: number }> = []
  if (Array.isArray(headers)) {
    for (const item of headers) {
      if (!item || typeof item.name !== 'string' || !item.name.trim()) continue
      const rank = keyOrder.indexOf(lower(item.name))
      const value = String(item.value ?? '')
      const masked = Boolean(item.masked || value === MASKED_VALUE)
      const serverRedacted = Boolean(item.serverRedacted || isServerRedactedValue(value))
      rows.push({
        row: {
          ...item,
          value: masked ? MASKED_VALUE : value,
          key: rank >= 0,
          masked,
          ...(serverRedacted ? { serverRedacted: true } : {}),
        },
        order: rank >= 0 ? rank : keyOrder.length,
      })
    }
    return rows.sort((a, b) => a.order - b.order).map(entry => entry.row)
  }
  for (const [name, raw] of Object.entries(headers as Readonly<Record<string, string>>)) {
    if (!name.trim()) continue
    const values = typeof raw === 'string' ? raw.split('\n') : [String(raw ?? '')]
    for (const value of values) {
      const rank = keyOrder.indexOf(lower(name))
      const masked = value === MASKED_VALUE
      const serverRedacted = isServerRedactedValue(value)
      rows.push({
        row: {
          name,
          value,
          key: rank >= 0,
          masked,
          ...(serverRedacted ? { serverRedacted: true } : {}),
        },
        order: rank >= 0 ? rank : keyOrder.length,
      })
    }
  }
  // Array.prototype.sort is stable, so repeated and non-key headers keep their order.
  return rows.sort((a, b) => a.order - b.order).map(entry => entry.row)
}

/** Builds inspector header rows from a header name/value record. */
export function headerRowsFromRecord(
  headers: Readonly<Record<string, string>> | null | undefined,
  direction: InspectorHeaderDirection = 'request',
): InspectorHeaderRow[] {
  return inspectorHeaderRows(headers, direction)
}

const HEADER_LINE_NAME = /^:?[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

/**
 * Parses raw "Name: Value" lines into inspector header rows, or returns [] when
 * the text is empty or does not consist of valid HTTP header lines.
 */
export function headerRowsFromText(
  text: string | null | undefined,
  direction: InspectorHeaderDirection = 'request',
): InspectorHeaderRow[] {
  if (typeof text !== 'string') return []
  const lines = text.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
  if (lines.length === 0) return []
  const record: Record<string, string> = {}
  for (const line of lines) {
    const colon = line.startsWith(':') ? line.indexOf(':', 1) : line.indexOf(':')
    if (colon <= 0) return []
    const name = line.slice(0, colon).trim()
    if (!HEADER_LINE_NAME.test(name)) return []
    const value = line.slice(colon + 1).trim()
    record[name] = Object.prototype.hasOwnProperty.call(record, name)
      ? `${record[name]}\n${value}`
      : value
  }
  return inspectorHeaderRows(record, direction)
}

/** Rebuilds a header record from inspector header rows, preserving repeated header names via newline join. */
export function headerRecordFromRows(rows: readonly InspectorHeaderRow[]): Record<string, string> {
  const record: Record<string, string> = {}
  for (const row of rows) {
    if (!row.name.trim()) continue
    const value = row.masked ? MASKED_VALUE : row.value
    record[row.name] = Object.prototype.hasOwnProperty.call(record, row.name)
      ? `${record[row.name]}\n${value}`
      : value
  }
  return record
}

/** The rows "Show key headers" shows, or null when there is nothing to toggle. */
export function inspectorKeyHeaderRows(rows: readonly InspectorHeaderRow[]): InspectorHeaderRow[] | null {
  const key = rows.filter(row => row.key)
  return key.length === 0 || key.length === rows.length ? null : key
}

const TEMPLATE_REFERENCE = /\{\{\s*([A-Za-z_][\w-]*)\.([A-Za-z0-9_.-]+)[^{}]*\}\}/g
const ANY_TEMPLATE = /\{\{[^{}]*\}\}/

/**
 * Where an authored value comes from. A value mixing references reports the
 * one that matters most: a secret, then a variable never set, then a variable,
 * a parameter and an environment value. Plain text is the fallback source.
 */
export function authoredValueSource(
  value: string,
  unresolvedVariables: readonly string[] = [],
  plain: InspectorValueSource = { kind: 'literal', label: 'Literal' },
): InspectorValueSource {
  const refs = [...value.matchAll(TEMPLATE_REFERENCE)].map(match => ({ namespace: match[1].toLowerCase(), name: match[2] }))
  const first = (namespace: string | string[]) => refs.find(ref => (Array.isArray(namespace) ? namespace : [namespace]).includes(ref.namespace))
  const secret = first('secret')
  if (secret) return { kind: 'secret', label: 'Secret', detail: `{{secret.${secret.name}}}` }
  const unset = refs.find(ref => ref.namespace === 'var' && unresolvedVariables.includes(ref.name))
  if (unset) return { kind: 'unset', label: 'Never set', detail: `{{var.${unset.name}}} was never set this run` }
  const variable = first('var')
  if (variable) return { kind: 'variable', label: `Variable ${variable.name}`, detail: `{{var.${variable.name}}}` }
  const parameter = first(['param', 'data'])
  if (parameter) return { kind: 'parameter', label: `Parameter ${parameter.name}`, detail: `{{${parameter.namespace}.${parameter.name}}}` }
  const environment = first('env')
  if (environment) return { kind: 'environment', label: `Environment ${environment.name}`, detail: `{{env.${environment.name}}}` }
  if (ANY_TEMPLATE.test(value)) return { kind: 'template', label: 'Template' }
  return plain
}

function enabledEntries(map: Record<string, string> | undefined, list: ScriptHeaderEntry[] | undefined): Array<[string, string]> {
  const entries: Array<[string, string]> = []
  for (const entry of list ?? []) {
    if (entry && entry.enabled !== false && typeof entry.name === 'string' && entry.name.trim()) entries.push([entry.name, String(entry.value ?? '')])
  }
  for (const [name, value] of Object.entries(map ?? {})) {
    if (name.trim()) entries.push([name, String(value ?? '')])
  }
  return entries
}

function groupByName(entries: Array<[string, string]>): Map<string, string[]> {
  const groups = new Map<string, string[]>()
  for (const [name, value] of entries) {
    const key = lower(name)
    groups.set(key, [...(groups.get(key) ?? []), value])
  }
  return groups
}

/**
 * Adds a "Comes from" source to request header rows. A step's own header wins
 * over a script default header, which wins over an HTTP runtime setting; a
 * Cookie nothing authored comes from the cookie jar. On the wire, a header
 * nothing authored was added by the client at runtime; in the scripted view
 * the dashboard cannot tell, so it says "Unknown". A secret-sourced value is
 * masked.
 *
 * The client may join authored values of one name into a single header (it
 * merges every authored Cookie, and the cookie jar appends to that). When
 * fewer values of a name were sent than authored, the row is judged over all
 * of them: a secret in any one masks it. An authored Cookie is "Script +
 * cookie jar", since the jar may have added to it.
 *
 * Without the authored step (the script changed since the run) no row gets a
 * source: not even Cookie, which an authored literal could have set.
 */
export function withRequestHeaderSources(rows: readonly InspectorHeaderRow[], context: InspectorSourceContext): InspectorHeaderRow[] {
  const authored = context.authored
  if (!authored) return rows.map(row => ({ ...row, source: undefined }))
  const unresolved = context.unresolvedVariables ?? []
  const stepHeaders = groupByName(enabledEntries(authored.action.headers, authored.action.header_list))
  const defaultHeaders = authored.http ? groupByName(enabledEntries(authored.http.default_headers, authored.http.default_header_list)) : new Map<string, string[]>()
  const sentCount = new Map<string, number>()
  for (const row of rows) sentCount.set(lower(row.name), (sentCount.get(lower(row.name)) ?? 0) + 1)
  const seen = new Map<string, number>()
  return rows.map(row => {
    const name = lower(row.name)
    const occurrence = seen.get(name) ?? 0
    seen.set(name, occurrence + 1)
    const sent = sentCount.get(name) ?? 0
    const stepValues = stepHeaders.get(name) ?? []
    const defaultValues = defaultHeaders.get(name) ?? []
    // Every value authored under this name, step and default alike.
    const everyAuthored = [...stepValues, ...defaultValues]
    const anySecret = everyAuthored.length > 0 && authoredValueSource(everyAuthored.join('\n'), unresolved).kind === 'secret'
    // Fewer sent than authored: the client joined them, so they are judged together.
    const fromAuthored = (values: string[], plain?: InspectorValueSource) =>
      authoredValueSource(sent < values.length ? values.join('\n') : values[occurrence] ?? values[0] ?? '', unresolved, plain)
    let source: InspectorValueSource
    if (name === 'cookie' && everyAuthored.length > 0) {
      const combined = authoredValueSource(everyAuthored.join('\n'), unresolved)
      source = combined.kind === 'secret' || combined.kind === 'unset'
        ? combined
        : { kind: 'script-jar', label: 'Script + cookie jar', ...(combined.detail ? { detail: combined.detail } : {}) }
    } else if (stepValues.length > 0) {
      source = fromAuthored(stepValues)
    } else if (defaultValues.length > 0) {
      source = fromAuthored(defaultValues, SCRIPT_DEFAULT)
    } else if (name === 'cookie') {
      source = COOKIE_JAR
    } else if (RUNTIME_SETTING_HEADERS.some(([header, field]) => header === name && typeof authored.http?.[field] === 'string' && String(authored.http[field]).trim())) {
      source = { kind: 'runtime', label: 'Runtime setting' }
    } else {
      source = context.view === 'wire' ? { kind: 'added', label: 'Added at runtime' } : UNKNOWN
    }
    // A Cookie (always merged) or a header sent fewer times than authored may
    // carry the secret of any authored value.
    const mayCarrySecret = anySecret && (name === 'cookie' || sent < everyAuthored.length)
    const masked = row.masked || source.kind === 'secret' || mayCarrySecret
    const serverRedacted = Boolean(row.serverRedacted || isServerRedactedValue(row.value))
    return {
      ...row,
      value: masked ? MASKED_VALUE : row.value,
      source,
      masked,
      ...(serverRedacted ? { serverRedacted: true } : {}),
    }
  })
}

/** The Content-Type header's value, matched case-insensitively. */
function contentType(headers: Readonly<Record<string, string>> | null | undefined): string {
  if (!headers) return ''
  const entry = Object.entries(headers).find(([name]) => lower(name) === 'content-type')
  return entry ? String(entry[1] ?? '').split('\n')[0].trim().toLowerCase() : ''
}

const FORM_CONTENT_TYPE = 'application/x-www-form-urlencoded'

export function isFormUrlEncoded(headers: Readonly<Record<string, string>> | null | undefined): boolean {
  return contentType(headers).split(';')[0].trim() === FORM_CONTENT_TYPE
}

/** The Content-Type a header map or authored entry list carries, or null when it has none. */
function declaredContentType(source: Readonly<Record<string, string>> | Array<[string, string]> | null | undefined): string | null {
  if (!source) return null
  const entries = Array.isArray(source) ? source : Object.entries(source)
  const entry = entries.find(([name]) => lower(name) === 'content-type')
  return entry ? String(entry[1] ?? '').split('\n')[0].split(';')[0].trim().toLowerCase() : null
}

/**
 * Whether the request body is a form, decided once per event so both views
 * mask alike. The first source that names a Content-Type decides, as the
 * request was built: the wire headers when captured, else the resolved step
 * headers, else the step's own authored header (it overrides the script
 * default), else the script default (which the resolved step headers never
 * carry).
 */
export function requestBodyIsForm(input: {
  wireHeaders?: Readonly<Record<string, string>> | null
  scriptedHeaders?: Readonly<Record<string, string>> | null
  authored?: InspectorAuthoredRequest | null
}): boolean {
  const authored = input.authored
  const candidates = [
    () => declaredContentType(input.wireHeaders),
    () => declaredContentType(input.scriptedHeaders),
    () => authored ? declaredContentType(enabledEntries(authored.action.headers, authored.action.header_list)) : null,
    () => authored?.http ? declaredContentType(enabledEntries(authored.http.default_headers, authored.http.default_header_list)) : null,
  ]
  for (const candidate of candidates) {
    const type = candidate()
    if (type !== null) return type === FORM_CONTENT_TYPE
  }
  return false
}

function decodeFormComponent(text: string): string {
  try {
    return decodeURIComponent(text.replace(/\+/g, ' '))
  } catch {
    return text
  }
}

function parseForm(body: string): Array<[string, string]> {
  const fields: Array<[string, string]> = []
  for (const pair of body.split('&')) {
    if (!pair) continue
    const at = pair.indexOf('=')
    const name = decodeFormComponent(at >= 0 ? pair.slice(0, at) : pair)
    if (!name.trim()) continue
    fields.push([name, at >= 0 ? decodeFormComponent(pair.slice(at + 1)) : ''])
  }
  return fields
}

/** A form field that holds a password or other credential, by its name. */
export function isPasswordLikeFieldName(name: string): boolean {
  const normalized = name.trim().toLowerCase().replace(/[^a-z0-9]+/g, '')
  return EXTRA_PASSWORD_FIELD_NAMES.has(normalized) || isSensitiveCredentialPreviewName(name)
}

/** Whether a header or form field name is credential/password-sensitive. */
export function isSensitiveFieldName(name: string): boolean {
  return isPasswordLikeFieldName(name) || isSensitiveCredentialPreviewKey(name)
}

/**
 * Masks credential/password-sensitive rows in-place, preserving `serverRedacted`
 * when the original row value was already `[REDACTED]`/`[MASKED]`.
 */
export function maskInspectorRows<T extends InspectorHeaderRow | InspectorFormFieldRow>(rows: readonly T[]): T[] {
  return rows.map(row => {
    const serverRedacted = Boolean(row.serverRedacted || isServerRedactedValue(row.value))
    const masked = Boolean(row.masked || isSensitiveFieldName(row.name))
    return {
      ...row,
      value: masked ? MASKED_VALUE : row.value,
      masked,
      ...(serverRedacted ? { serverRedacted: true } : {}),
    }
  })
}

/** The authored body when it is plain form text the dashboard can read, else null. */
function authoredFormBody(action: ScriptActionDefinition): string | null {
  if (typeof action.body !== 'string' || !action.body || action.body_file || action.body_multipart) return null
  const encoding = (action.body_encoding ?? '').trim().toLowerCase()
  if (encoding && encoding !== 'text' && encoding !== 'utf-8' && encoding !== 'utf8') return null
  return action.body
}

/**
 * The fields of a form body (form: requestBodyIsForm), or null for any other
 * body. Password-like fields and secret-sourced fields are masked; the value
 * then never leaves this function. A field the authored body names gets its
 * source; any other field is "Unknown" (a body transform or a client may have
 * added it, and the dashboard cannot tell which).
 */
export function inspectorFormFieldRows(
  body: string | null | undefined,
  form: boolean,
  context: InspectorSourceContext,
): InspectorFormFieldRow[] | null {
  if (!body || !form) return null
  const fields = parseForm(body)
  if (fields.length === 0) return null
  const authoredBody = context.authored ? authoredFormBody(context.authored.action) : null
  const authored = authoredBody !== null ? groupByName(parseForm(authoredBody)) : null
  const unresolved = context.unresolvedVariables ?? []
  const seen = new Map<string, number>()
  return fields.map(([name, value]) => {
    const key = lower(name)
    const occurrence = seen.get(key) ?? 0
    seen.set(key, occurrence + 1)
    const values = authored?.get(key)
    const source = values ? authoredValueSource(values[occurrence] ?? values[0] ?? '', unresolved) : UNKNOWN
    const masked = source.kind === 'secret' || isPasswordLikeFieldName(name)
    const serverRedacted = isServerRedactedValue(value)
    return {
      name,
      value: masked ? MASKED_VALUE : value,
      masked,
      ...(serverRedacted ? { serverRedacted: true } : {}),
      source,
    }
  })
}

/**
 * Shown once per Request view (by the inspector, not by each table) instead of
 * a column of "Unknown" when the authored step is not at hand.
 */
export function sourcesUnavailableNote(replayMatchesCurrentScript: boolean): string {
  return replayMatchesCurrentScript
    ? 'Sources are unknown for this step.'
    : 'Sources are unavailable because the script changed since this run.'
}

export interface MaskedFieldNoteRowLike {
  value?: string
  rawValue?: string
  masked?: boolean
  serverRedacted?: boolean
}

export interface MaskedFieldNoteOptions {
  serverRedacted?: boolean
  rows?: readonly MaskedFieldNoteRowLike[]
}

function rowHasServerRedaction(row: MaskedFieldNoteRowLike | null | undefined): boolean {
  if (!row) return false
  return Boolean(row.serverRedacted || isServerRedactedValue(row.rawValue) || isServerRedactedValue(row.value))
}

/**
 * The form/header table's note on masked fields, naming the copies on screen
 * that still include client-masked values while clarifying when server-redacted
 * values (`[REDACTED]`) remain redacted in exports (`Copy as cURL` / `Download HAR`).
 */
export function maskedFieldNote(
  count: number,
  unmaskedCopiesOrOptions: readonly string[] | MaskedFieldNoteOptions = [],
  optionsOrRows?: MaskedFieldNoteOptions | readonly MaskedFieldNoteRowLike[],
): string {
  const hidden = count === 1 ? '1 field is hidden here.' : `${count} fields are hidden here.`
  const copies: readonly string[] = Array.isArray(unmaskedCopiesOrOptions) ? unmaskedCopiesOrOptions : []
  const explicitOptions: MaskedFieldNoteOptions | undefined = !Array.isArray(unmaskedCopiesOrOptions)
    ? unmaskedCopiesOrOptions as MaskedFieldNoteOptions
    : Array.isArray(optionsOrRows)
      ? { rows: optionsOrRows }
      : optionsOrRows as MaskedFieldNoteOptions | undefined
  const serverRedacted = Boolean(
    explicitOptions?.serverRedacted ||
    explicitOptions?.rows?.some(rowHasServerRedaction),
  )
  if (!serverRedacted) {
    if (copies.length === 0) return hidden
    const names = copies.length === 1
      ? copies[0]
      : `${copies.slice(0, -1).join(', ')} and ${copies[copies.length - 1]}`
    const verb = copies.length === 1 ? 'includes' : 'include'
    return `${hidden} ${names} ${verb} ${count === 1 ? 'it' : 'them'}.`
  }
  if (copies.length === 0) {
    return `${hidden} Client-masked values can be revealed locally; server-redacted values ([REDACTED]) remain redacted in exports (Copy as cURL / Download HAR).`
  }
  const names = copies.length === 1
    ? copies[0]
    : `${copies.slice(0, -1).join(', ')} and ${copies[copies.length - 1]}`
  const verb = copies.length === 1 ? 'includes' : 'include'
  return `${hidden} ${names} ${verb} client-masked values; server-redacted values ([REDACTED]) remain redacted in exports (Copy as cURL / Download HAR).`
}
