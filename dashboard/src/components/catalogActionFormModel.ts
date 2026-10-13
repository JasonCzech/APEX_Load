// The pure half of the catalog action form: reading and writing an action's
// fields by catalog path, an action's operation (variant) and its choices,
// and what a script offers its live-table fields. Every write keeps the keys
// the author did not touch, in their order, so a form edit and a JSON edit
// round-trip.
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { DslCatalogAction, DslCatalogField, DslCatalogVariant } from './dslActionCatalog'
import { isControlAction, isSourceFunctionAction } from './scriptActionHelpers'
import { stepsBefore, type InsertionPoint } from './referenceScopeModel'
import { scriptDataUsage, usesTableSteps } from './scriptDataUsageModel'

type Action = ScriptActionDefinition
type Json = Record<string, unknown>

export const HTTP_RUNTIME_ACTION_TYPES = new Set(['cookie_clear', 'cache_clear', 'http_connections_close'])
const HTTP_REQUEST_TYPES = new Set(['request', 'GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'http_oauth2_token'])
const NETWORK_EDITOR_TYPES = new Set(['network_health', 'dns_query', 'tcp_request', 'udp_request', 'ftp_command', 'ftp_login', 'ftp_noop', 'smtp_send', 'pop3_command', 'imap_command', 'ldap_bind', 'ldap_search'])
const HAND_BUILT_PREFIXES = ['browser_', 'jms_', 'ws_', 'grpc_', 'db_']

/**
 * Whether the action type has a hand-built editor: HTTP requests, browser
 * steps, control flow, JMS, WebSocket, gRPC, database and network actions,
 * and imported source calls. Every other type is edited by the catalog form,
 * so a live table (or any new type) never shows HTTP request fields.
 */
export function hasHandBuiltEditor(action: Action): boolean {
  const type = action.type
  return type === 'http_sequence' || type === 'native_javascript' || HTTP_RUNTIME_ACTION_TYPES.has(type) || isControlAction(action) || isSourceFunctionAction(action)
    || HTTP_REQUEST_TYPES.has(type) || NETWORK_EDITOR_TYPES.has(type) || HAND_BUILT_PREFIXES.some(prefix => type.startsWith(prefix))
}

/** A field's name in words: the catalog's label, else its path's last part. */
export function catalogFieldLabel(field: Pick<DslCatalogField, 'name' | 'label'>): string {
  if (field.label) return field.label
  const last = field.name.split('.').pop() || field.name
  const words = last.replace(/_/g, ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** Action types whose form needs the catalog's variants; without them it is not shown. */
export const VARIANT_ACTION_TYPES = new Set(['live_table'])

function isRecord(value: unknown): value is Json {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** The value at a catalog field path ("target", "params.position", "config.eventhubs.start_position"). */
export function fieldValue(action: Action, path: string): unknown {
  let current: unknown = action
  for (const part of path.split('.')) {
    if (!isRecord(current)) return undefined
    current = current[part]
  }
  return current
}

/** `object` with `key` set (or removed, for undefined), other keys in place. */
function withKey(object: Json, key: string, value: unknown): Json {
  if (value === undefined) {
    if (!(key in object)) return object
    const next = { ...object }
    delete next[key]
    return next
  }
  return { ...object, [key]: value }
}

/**
 * The change that sets a field: the action's top-level key and its new value.
 * Undefined removes the field. Nested objects are copied, never mutated.
 */
export function fieldUpdate(action: Action, path: string, value: unknown): { key: keyof Action; value: unknown } {
  const [top, ...rest] = path.split('.')
  if (!rest.length) return { key: top as keyof Action, value }
  const write = (object: Json, parts: string[]): Json => {
    const [head, ...tail] = parts
    if (!tail.length) return withKey(object, head, value)
    const child = isRecord(object[head]) ? object[head] as Json : {}
    return withKey(object, head, write(child, tail))
  }
  const current = (action as unknown as Json)[top]
  return { key: top as keyof Action, value: write(isRecord(current) ? current : {}, rest) }
}

function paramsOf(action: Action): Json {
  return isRecord(action.params) ? action.params as Json : {}
}

const paramKey = (path: string) => path.startsWith('params.') ? path.slice('params.'.length) : ''

/** The operation the action is set to, when the spec has variants. */
export function activeVariant(spec: DslCatalogAction, action: Action): DslCatalogVariant | undefined {
  if (!spec.discriminator || !spec.variants?.length) return undefined
  const value = fieldValue(action, spec.discriminator)
  return spec.variants.find(variant => variant.value === value)
}

/** The fields the form shows: the variant's, or every spec field, in catalog order, without the discriminator. */
export function formFields(spec: DslCatalogAction, variant?: DslCatalogVariant): DslCatalogField[] {
  const allowed = variant ? new Set(variant.allowed) : null
  return (spec.fields || []).filter(field => field.name !== spec.discriminator && (!allowed || allowed.has(field.name)))
}

/** Which of a variant's choices the action makes: the first given whole, else the first given at all, else the first. */
export function chosenChoice(variant: DslCatalogVariant, action: Action): number {
  const choices = variant.choices || []
  const present = (path: string) => fieldValue(action, path) !== undefined
  const whole = choices.findIndex(choice => choice.every(present))
  if (whole >= 0) return whole
  const partial = choices.findIndex(choice => choice.some(present))
  return partial >= 0 ? partial : 0
}

/** The params with one choice made: the other choices' fields go. */
export function paramsForChoice(variant: DslCatalogVariant, action: Action, index: number): Json {
  const keep = new Set(variant.choices?.[index] || [])
  let params = paramsOf(action)
  for (const [choice, fields] of (variant.choices || []).entries()) {
    if (choice === index) continue
    for (const field of fields) {
      if (!keep.has(field) && paramKey(field)) params = withKey(params, paramKey(field), undefined)
    }
  }
  return params
}

/**
 * Params the action has that the form does not show: fields the operation
 * does not take, or the catalog does not know. They are listed, never dropped.
 */
export function strayParams(spec: DslCatalogAction, variant: DslCatalogVariant | undefined, action: Action): Array<{ key: string; value: unknown }> {
  const shown = new Set([...(spec.discriminator ? [spec.discriminator] : []), ...formFields(spec, variant).map(field => field.name)])
  return Object.entries(paramsOf(action)).filter(([key]) => !shown.has(`params.${key}`)).map(([key, value]) => ({ key, value }))
}

/**
 * Switching the operation: the params the new one takes, the discriminator
 * set, its defaults for what is missing, and the fields that go.
 */
export function switchOperation(spec: DslCatalogAction, action: Action, next: DslCatalogVariant): { params: Json; removed: string[] } {
  const allowed = new Set(next.allowed.map(paramKey).filter(Boolean))
  const known = new Set((spec.fields || []).map(field => paramKey(field.name)).filter(Boolean))
  const params: Json = {}
  const removed: string[] = []
  for (const [key, value] of Object.entries(paramsOf(action))) {
    // A field the catalog does not know stays (and is listed as not recognised).
    if (allowed.has(key) || !known.has(key)) params[key] = value
    else removed.push(key)
  }
  const discriminator = paramKey(spec.discriminator || '')
  if (discriminator) params[discriminator] = next.value
  for (const [key, value] of Object.entries(next.defaults || {})) {
    if (!(key in params)) params[key] = structuredClone(value)
  }
  // A default may make a second choice: keep the one the action already makes.
  return { params: next.choices?.length ? paramsForChoice(next, { ...action, params } as Action, chosenChoice(next, { ...action, params } as Action)) : params, removed }
}

const LIVE_TABLE_IDENTIFIER = /^[A-Za-z_][A-Za-z0-9_-]{0,127}$/
const LIVE_TABLE_MAX_COLUMNS = 128

/** Why a live-table column name cannot be added, or '' when it can (the validator's identifier rule). */
export function liveTableColumnProblem(name: string, columns: readonly string[]): string {
  if (!name) return 'Enter a column name.'
  if (!LIVE_TABLE_IDENTIFIER.test(name)) return 'Start with a letter or underscore, then use letters, numbers, underscores and hyphens (up to 128 characters).'
  if (columns.includes(name)) return `There is already a column named "${name}".`
  if (columns.length >= LIVE_TABLE_MAX_COLUMNS) return `A live-table step takes at most ${LIVE_TABLE_MAX_COLUMNS} columns.`
  return ''
}

/** Action types with no response to extract from or check. */
export const NO_RESPONSE_ACTION_TYPES = new Set(['live_table', 'cookie_set', 'error_message', 'review_required', 'http_sync', 'http_async_stop'])

const INT64_MIN = -(2n ** 63n)
const INT64_MAX = 2n ** 63n - 1n

/** Why a typed signed 64-bit integer is not one, or '' when it is (checked on the text, never as a float). */
export function int64Problem(text: string): string {
  if (!/^(0|-?[1-9][0-9]*)$/.test(text)) return 'Enter a whole number, such as 1 or -5.'
  const value = BigInt(text)
  return value < INT64_MIN || value > INT64_MAX ? 'Enter a number between -9223372036854775808 and 9223372036854775807.' : ''
}

/** What a script offers its live-table fields before one step. */
export interface LiveTableFormContext {
  /** Aliases the script reads or changes, or declares for native services. */
  aliases: string[]
  /** Column names the script uses, by alias. */
  columnsByAlias: Record<string, string[]>
  /** Earlier steps' outputs, as references, by kind. */
  outputs: { record_id: string[]; record_version: string[]; lease: string[] }
}

const ROW_OPERATIONS = new Set(['read', 'next', 'pop', 'lease', 'push', 'update', 'increment', 'rotate'])

/**
 * The aliases and columns a script uses, and the outputs of the live-table
 * steps that run before `point` (on its path: init, then actions, then teardown).
 */
export function liveTableFormContext(script: ScriptDefinition, point: InsertionPoint): LiveTableFormContext {
  const usage = scriptDataUsage(script)
  const aliases = new Set(usage.filter(usesTableSteps).map(item => item.alias))
  const native = (script.config as unknown as Json | undefined)?.native_services as Json | undefined
  for (const table of Array.isArray(native?.live_tables) ? native.live_tables as Json[] : []) {
    if (typeof table.alias === 'string' && table.alias) aliases.add(table.alias)
  }
  const outputs = { record_id: [] as string[], record_version: [] as string[], lease: [] as string[] }
  for (const { action } of stepsBefore(script, point)) {
    if (action.type !== 'live_table') continue
    const params = paramsOf(action)
    const prefix = typeof params.result_prefix === 'string' ? params.result_prefix : ''
    if (!prefix) continue
    if (params.operation === 'lease') outputs.lease.push(prefix)
    if (ROW_OPERATIONS.has(String(params.operation))) {
      outputs.record_id.push(`{{var.${prefix}.record_id}}`)
      outputs.record_version.push(`{{var.${prefix}.record_version}}`)
    }
  }
  const unique = (values: string[]) => [...new Set(values)]
  return {
    aliases: [...aliases].sort(),
    columnsByAlias: Object.fromEntries(usage.filter(item => aliases.has(item.alias)).map(item => [item.alias, item.columns])),
    outputs: { record_id: unique(outputs.record_id), record_version: unique(outputs.record_version), lease: unique(outputs.lease) },
  }
}
