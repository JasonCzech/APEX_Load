import type { ScriptActionDefinition } from '../types/scripts'

const TEMPLATE_TOKEN_PATTERN = /(\{\{[^{}]+\}\})/g
const UNQUOTED_SENTINEL_PREFIX = '__APEX_UNQUOTED_TPL_'

export interface AuraSubAction {
  id?: string
  descriptor?: string
  callingDescriptor?: string
  params?: Record<string, unknown>
  version?: string | null
  storable?: boolean
  [key: string]: unknown
}

export interface ParsedAuraEnvelope {
  actions: AuraSubAction[]
  messageObject: Record<string, unknown>
  contextObject: Record<string, unknown> | null
  contextRaw: string
  pageURI: string
  token: string
  fieldOrder: Array<{ key: string; rawKey: string; rawValue: string }>
  unquotedTemplates: Map<string, string>
  /**
   * Each envelope field's value as parsed, re-serialized: a field still equal
   * to it is written back with its recorded bytes, so an edit to one field
   * never re-encodes another.
   */
  baselineValues?: Map<string, string>
  /**
   * Per field, the template-shaped text (`{{x}}`) the recording sent with
   * encoded braces (`%7B%7Bx%7D%7D`): literal text, which stays encoded when
   * the field is re-encoded rather than becoming a live template. Each is
   * located by the JSON path of the string holding it (`[]` for a plain
   * field), so a live template an author adds elsewhere never takes its place.
   */
  literalTemplates?: Map<string, AuraLiteralTemplate[]>
}

/**
 * A literal `{{x}}` of a recorded envelope field: `token` as the string at
 * `path` holds it. Without a path (a literal the parse could not place, such
 * as one in a JSON key) it is matched by text, in order, as a last resort.
 */
export interface AuraLiteralTemplate {
  token: string
  path?: Array<string | number>
}

export interface AuraResponseActionSummary {
  id: string
  state: string
  errorSummary?: string
  returnValuePreview?: string
}

export interface ParsedAuraResponse {
  actions: AuraResponseActionSummary[]
  hasActionError: boolean
}

function safeDecodeURIComponent(segment: string): string {
  const plusDecoded = segment.replace(/\+/g, ' ')
  try {
    return decodeURIComponent(plusDecoded)
  } catch {
    return plusDecoded.replace(/%[0-9A-Fa-f]{2}/g, match => {
      try {
        return decodeURIComponent(match)
      } catch {
        return match
      }
    })
  }
}

const isTemplateToken = (part: string) => part.startsWith('{{') && part.endsWith('}}')

export function decodeFormValuePreservingTemplates(raw: string): string {
  return decodeFormValueWithLiteralTemplates(raw).value
}

/**
 * Decodes a form value, keeping raw `{{x}}` templates as they are, and lists
 * the template-shaped text that only decoding produced (encoded braces on the
 * wire), which is literal text and not a template.
 */
export function decodeFormValueWithLiteralTemplates(raw: string): { value: string; literalTemplates: string[] } {
  const { value, literalTemplates } = decodeFormValueMarkingLiterals(raw)
  return { value, literalTemplates }
}

// Private-use characters stand in for the braces of a literal `{{x}}` while a
// field is placed (parse) or re-encoded (serialize), so it never reads as a
// live template; they never reach the wire or the editor.
const LITERAL_OPEN = '\uE000'
const LITERAL_CLOSE = '\uE001'
const LITERAL_MARK_PATTERN = /\uE000([^\uE000\uE001]*)\uE001/g
const LITERAL_MARK_CHARS = /[\uE000\uE001]/
const markLiteralToken = (token: string) => `${LITERAL_OPEN}${token.slice(2, -2)}${LITERAL_CLOSE}`

function decodeFormValueMarkingLiterals(raw: string): { value: string; marked: string; literalTemplates: string[] } {
  const literalTemplates: string[] = []
  const value: string[] = []
  const marked: string[] = []
  for (const part of raw.split(TEMPLATE_TOKEN_PATTERN)) {
    if (isTemplateToken(part)) {
      value.push(part)
      marked.push(part)
      continue
    }
    const decoded = safeDecodeURIComponent(part)
    for (const token of decoded.match(TEMPLATE_TOKEN_PATTERN) || []) literalTemplates.push(token)
    value.push(decoded)
    marked.push(decoded.replace(TEMPLATE_TOKEN_PATTERN, markLiteralToken))
  }
  return { value: value.join(''), marked: marked.join(''), literalTemplates }
}

type JsonPath = Array<string | number>

function collectMarkedLiterals(node: unknown, path: JsonPath, out: AuraLiteralTemplate[]) {
  if (typeof node === 'string') {
    for (const match of node.matchAll(LITERAL_MARK_PATTERN)) out.push({ token: `{{${match[1]}}}`, path })
  } else if (Array.isArray(node)) {
    node.forEach((child, index) => collectMarkedLiterals(child, [...path, index], out))
  } else if (node && typeof node === 'object') {
    for (const [key, child] of Object.entries(node)) collectMarkedLiterals(child, [...path, key], out)
  }
}

/**
 * Where each literal of a decoded field sits: the JSON path of its string in
 * a JSON field, or the field itself. Falls back to text order for the whole
 * field when any literal cannot be placed.
 */
function locateLiteralTemplates(decoded: { value: string; marked: string; literalTemplates: string[] }, json: boolean): AuraLiteralTemplate[] {
  const unplaced = decoded.literalTemplates.map(token => ({ token }))
  if (unplaced.length === 0 || LITERAL_MARK_CHARS.test(decoded.value)) return unplaced
  const placed: AuraLiteralTemplate[] = []
  if (json) {
    const object = parseJsonObjectWithTemplates(decoded.marked, new Map())
    if (!object) return unplaced
    collectMarkedLiterals(object, [], placed)
  } else {
    collectMarkedLiterals(decoded.marked, [], placed)
  }
  return placed.length === unplaced.length ? placed : unplaced
}

/** A copy of value with each placed literal's text marked in the string at its path. */
function markPlacedLiterals<T>(value: T, literals: readonly AuraLiteralTemplate[] | undefined): T {
  const placed = (literals || []).filter(literal => literal.path)
  if (placed.length === 0) return value
  const markIn = (text: string, token: string) => {
    const at = text.indexOf(token)
    return at < 0 ? text : `${text.slice(0, at)}${markLiteralToken(token)}${text.slice(at + token.length)}`
  }
  if (typeof value === 'string') {
    return placed.reduce((text, literal) => literal.path!.length === 0 ? markIn(text, literal.token) : text, value as string) as T
  }
  const copy = JSON.parse(JSON.stringify(value)) as unknown
  for (const literal of placed) {
    const path = literal.path!
    if (path.length === 0) continue
    let parent: unknown = copy
    for (const key of path.slice(0, -1)) parent = parent && typeof parent === 'object' ? (parent as Record<string | number, unknown>)[key] : undefined
    if (!parent || typeof parent !== 'object') continue
    const last = path[path.length - 1]
    const holder = parent as Record<string | number, unknown>
    if (typeof holder[last] === 'string') holder[last] = markIn(holder[last] as string, literal.token)
  }
  return copy as T
}

const encodeFormSegment = (part: string) => encodeURIComponent(part).replace(/%20/g, '+')

/**
 * Encodes a form value, writing `{{x}}` templates raw so they stay live. The
 * occurrences listed in literalTemplates (in order, by text) were literal
 * text in the recording, so they are encoded like any other text.
 */
export function encodeFormValuePreservingTemplates(value: string, literalTemplates: readonly string[] = []): string {
  const literalBudget = new Map<string, number>()
  for (const token of literalTemplates) literalBudget.set(token, (literalBudget.get(token) || 0) + 1)
  return value
    .split(TEMPLATE_TOKEN_PATTERN)
    .map(part => {
      if (!isTemplateToken(part)) return encodeFormSegment(part)
      const literal = literalBudget.get(part) || 0
      if (literal === 0) return part
      literalBudget.set(part, literal - 1)
      return encodeFormSegment(part)
    })
    .join('')
}

const MARKED_OR_TEMPLATE_PATTERN = /(\uE000[^\uE000\uE001]*\uE001|\{\{[^{}]+\}\})/g

/**
 * encodeFormValuePreservingTemplates for a field whose placed literals are
 * marked (markPlacedLiterals): a marked literal is encoded, as is an unplaced
 * one by its text and order.
 */
function encodeMarkedFormValue(value: string, unplaced: readonly string[]): string {
  const literalBudget = new Map<string, number>()
  for (const token of unplaced) literalBudget.set(token, (literalBudget.get(token) || 0) + 1)
  return value
    .split(MARKED_OR_TEMPLATE_PATTERN)
    .map(part => {
      if (part.startsWith(LITERAL_OPEN) && part.endsWith(LITERAL_CLOSE)) return encodeFormSegment(`{{${part.slice(1, -1)}}}`)
      if (!isTemplateToken(part)) return encodeFormSegment(part)
      const literal = literalBudget.get(part) || 0
      if (literal === 0) return part
      literalBudget.set(part, literal - 1)
      return encodeFormSegment(part)
    })
    .join('')
}

/**
 * The literals of a field still matched by text and order: those the parse
 * could not place, and placed ones no longer at their path (an edit moved the
 * string that holds them, such as an array element inserted before it), so a
 * moved literal still stays encoded rather than becoming a live template.
 */
function unplacedLiteralTokens(literals: readonly AuraLiteralTemplate[], marked: string): string[] {
  const markedCount = new Map<string, number>()
  for (const match of marked.matchAll(LITERAL_MARK_PATTERN)) {
    const token = `{{${match[1]}}}`
    markedCount.set(token, (markedCount.get(token) || 0) + 1)
  }
  const unplaced: string[] = []
  for (const literal of literals) {
    if (literal.path) {
      const left = markedCount.get(literal.token) || 0
      if (left > 0) {
        markedCount.set(literal.token, left - 1)
        continue
      }
    }
    unplaced.push(literal.token)
  }
  return unplaced
}

// A JSON number literal, matched where a value starts outside a string.
const JSON_NUMBER = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y

/**
 * Stands each unquoted `{{...}}` template, and each number literal that
 * JSON.stringify would not write back byte-for-byte (an integer beyond 2^53,
 * `1.0`, `1e3`, `-0`), in for a sentinel string the map restores
 * (restoreUnquotedTemplates). Re-serializing a message for an edit to one
 * sub-action then keeps every other value as recorded, not rounded.
 */
function protectUnquotedTemplates(rawJson: string, map: Map<string, string>): string {
  let out = ''
  let inString = false
  let escaped = false
  let index = 0
  while (index < rawJson.length) {
    const ch = rawJson[index]
    if (inString) {
      out += ch
      if (escaped) {
        escaped = false
      } else if (ch === '\\') {
        escaped = true
      } else if (ch === '"') {
        inString = false
      }
      index += 1
      continue
    }
    if (ch === '"') {
      inString = true
      out += ch
      index += 1
      continue
    }
    if (ch === '-' || (ch >= '0' && ch <= '9')) {
      JSON_NUMBER.lastIndex = index
      const lexeme = JSON_NUMBER.exec(rawJson)?.[0]
      if (lexeme) {
        if (JSON.stringify(Number(lexeme)) === lexeme) {
          out += lexeme
        } else {
          const sentinel = `${UNQUOTED_SENTINEL_PREFIX}${map.size}__`
          map.set(sentinel, lexeme)
          out += `"${sentinel}"`
        }
        index += lexeme.length
        continue
      }
    }
    if (ch === '{' && rawJson[index + 1] === '{') {
      const close = rawJson.indexOf('}}', index + 2)
      if (close > index + 2) {
        const token = rawJson.slice(index, close + 2)
        if (!token.slice(2, -2).includes('{') && !token.slice(2, -2).includes('}')) {
          const sentinel = `${UNQUOTED_SENTINEL_PREFIX}${map.size}__`
          map.set(sentinel, token)
          out += `"${sentinel}"`
          index = close + 2
          continue
        }
      }
    }
    out += ch
    index += 1
  }
  return out
}

export function restoreUnquotedTemplates(serialized: string, map: ReadonlyMap<string, string>): string {
  let restored = serialized
  for (const [sentinel, token] of map.entries()) {
    restored = restored.split(`"${sentinel}"`).join(token)
  }
  return restored
}

export function parseJsonObjectWithTemplates(rawJson: string, map: Map<string, string>): Record<string, unknown> | null {
  const trimmed = rawJson.trim()
  if (!trimmed.startsWith('{')) return null
  try {
    const protectedJson = protectUnquotedTemplates(trimmed, map)
    const parsed: unknown = JSON.parse(protectedJson)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    return parsed as Record<string, unknown>
  } catch {
    return null
  }
}

/**
 * Edits typed into the Aura envelope editor and not yet written to the body:
 * each sub-action's Parameters JSON by index, and the context JSON.
 */
export interface AuraEnvelopeDrafts {
  params: Readonly<Record<number, string>>
  context: string | null
}

export const EMPTY_AURA_ENVELOPE_DRAFTS: AuraEnvelopeDrafts = { params: {}, context: null }

export function hasAuraEnvelopeDrafts(drafts: AuraEnvelopeDrafts): boolean {
  return drafts.context !== null || Object.keys(drafts.params).length > 0
}

export type AuraEnvelopeDraftResult =
  | { body: string }
  | { error: string; actionIndex?: number; context?: true }

/**
 * The body the envelope serializes to with the drafts written in, or why it
 * cannot: Apply in the editor and Save script both resolve drafts here, so a
 * draft saved without Apply reaches the body exactly as Apply would write it.
 */
export function applyAuraEnvelopeDrafts(envelope: ParsedAuraEnvelope, drafts: AuraEnvelopeDrafts): AuraEnvelopeDraftResult {
  const nextUnquoted = new Map(envelope.unquotedTemplates)
  let contextObject = envelope.contextObject
  if (drafts.context !== null) {
    const parsedContext = envelope.contextObject ? parseJsonObjectWithTemplates(drafts.context, nextUnquoted) : null
    if (!parsedContext) return { error: 'Aura context must be a valid JSON object.', context: true }
    contextObject = parsedContext
  }
  const actions = [...envelope.actions]
  for (const [key, raw] of Object.entries(drafts.params)) {
    const index = Number(key)
    if (!Number.isInteger(index) || index < 0 || index >= actions.length) {
      return { error: 'The Aura envelope no longer has this sub-action.', actionIndex: index }
    }
    const parsedParams = parseJsonObjectWithTemplates(raw, nextUnquoted)
    if (!parsedParams) return { error: 'Parameters must be a valid JSON object.', actionIndex: index }
    actions[index] = { ...actions[index], params: parsedParams }
  }
  return { body: serializeAuraFormEnvelope({ ...envelope, actions, contextObject, unquotedTemplates: nextUnquoted }) }
}

export function isAuraEndpointTarget(target?: string): boolean {
  if (!target) return false
  return /(?:^|\/)aura(?:\?|$)/i.test(target.trim())
}

export function isAuraFormRequest(action: Pick<ScriptActionDefinition, 'target' | 'body' | 'body_encoding' | 'body_multipart'>): boolean {
  if (action.body_multipart || action.body_encoding) return false
  const body = action.body || ''
  if (!body.includes('message=')) return false
  if (isAuraEndpointTarget(action.target)) return true
  return /(?:^|&)aura\.(?:context|token|pageURI)=/.test(body)
}

export function parseAuraFormEnvelope(body?: string): ParsedAuraEnvelope | null {
  if (!body || !body.includes('message=')) return null
  const trimmed = body.trim()
  if (!trimmed || trimmed.startsWith('{') || trimmed.startsWith('<')) return null
  const segments = trimmed.split('&')
  const fieldOrder: Array<{ key: string; rawKey: string; rawValue: string }> = []
  const counts = new Map<string, number>()
  for (const segment of segments) {
    if (!segment) continue
    const eq = segment.indexOf('=')
    if (eq <= 0) return null
    const rawKey = segment.slice(0, eq)
    const rawValue = segment.slice(eq + 1)
    const key = decodeFormValuePreservingTemplates(rawKey)
    counts.set(key, (counts.get(key) || 0) + 1)
    fieldOrder.push({ key, rawKey, rawValue })
  }
  // Reject duplicate Aura envelope keys to avoid lossy round-trips.
  for (const key of ['message', 'aura.context', 'aura.pageURI', 'aura.token']) {
    if ((counts.get(key) || 0) > 1) return null
  }
  const messageEntry = fieldOrder.find(entry => entry.key === 'message')
  if (!messageEntry) return null
  const unquotedTemplates = new Map<string, string>()
  const literalTemplates = new Map<string, AuraLiteralTemplate[]>()
  const decodeField = (entry: { key: string; rawValue: string }, json = false) => {
    const decoded = decodeFormValueMarkingLiterals(entry.rawValue)
    if (decoded.literalTemplates.length > 0) literalTemplates.set(entry.key, locateLiteralTemplates(decoded, json))
    return decoded.value
  }
  const decodedMessage = decodeField(messageEntry, true)
  const messageObject = parseJsonObjectWithTemplates(decodedMessage, unquotedTemplates)
  if (!messageObject || !Array.isArray(messageObject.actions)) return null
  const actions = messageObject.actions.filter(
    (item): item is AuraSubAction => Boolean(item && typeof item === 'object' && !Array.isArray(item)),
  )
  if (actions.length !== messageObject.actions.length) return null

  const contextEntry = fieldOrder.find(entry => entry.key === 'aura.context')
  const contextRaw = contextEntry ? decodeField(contextEntry, true) : ''
  const contextObject = contextRaw ? parseJsonObjectWithTemplates(contextRaw, unquotedTemplates) : null
  const pageEntry = fieldOrder.find(entry => entry.key === 'aura.pageURI')
  const tokenEntry = fieldOrder.find(entry => entry.key === 'aura.token')

  const envelope: ParsedAuraEnvelope = {
    actions,
    messageObject,
    contextObject,
    contextRaw,
    pageURI: pageEntry ? decodeField(pageEntry) : '',
    token: tokenEntry ? decodeField(tokenEntry) : '',
    fieldOrder,
    unquotedTemplates,
    literalTemplates,
  }
  envelope.baselineValues = auraEnvelopeFieldValues(envelope)
  return envelope
}

/**
 * Each envelope field's value, re-serialized. With literals, the recorded
 * literal templates are marked where they still sit, for encoding.
 */
function auraEnvelopeFieldValues(envelope: ParsedAuraEnvelope, literals?: ReadonlyMap<string, AuraLiteralTemplate[]>): Map<string, string> {
  const mark = <T,>(key: string, value: T): T => literals ? markPlacedLiterals(value, literals.get(key)) : value
  const nextMessageJson = restoreUnquotedTemplates(
    JSON.stringify(mark('message', { ...envelope.messageObject, actions: envelope.actions })),
    envelope.unquotedTemplates,
  )
  const nextContextJson = envelope.contextObject
    ? restoreUnquotedTemplates(JSON.stringify(mark('aura.context', envelope.contextObject)), envelope.unquotedTemplates)
    : mark('aura.context', envelope.contextRaw)

  return new Map<string, string>([
    ['message', nextMessageJson],
    ...(nextContextJson ? [['aura.context', nextContextJson] as const] : []),
    ...(envelope.pageURI || envelope.fieldOrder.some(f => f.key === 'aura.pageURI')
      ? [['aura.pageURI', mark('aura.pageURI', envelope.pageURI)] as const]
      : []),
    ...(envelope.token || envelope.fieldOrder.some(f => f.key === 'aura.token')
      ? [['aura.token', mark('aura.token', envelope.token)] as const]
      : []),
  ])
}

export function serializeAuraFormEnvelope(envelope: ParsedAuraEnvelope): string {
  const updatedValues = auraEnvelopeFieldValues(envelope)
  const markedValues = auraEnvelopeFieldValues(envelope, envelope.literalTemplates || new Map())

  const seen = new Set<string>()
  const parts: string[] = []
  for (const field of envelope.fieldOrder) {
    if (updatedValues.has(field.key)) {
      seen.add(field.key)
      const val = updatedValues.get(field.key) ?? ''
      // An unchanged field keeps the bytes it was recorded with.
      if (envelope.baselineValues?.get(field.key) === val) {
        parts.push(`${field.rawKey}=${field.rawValue}`)
      } else {
        const marked = markedValues.get(field.key) ?? val
        const unplaced = unplacedLiteralTokens(envelope.literalTemplates?.get(field.key) || [], marked)
        parts.push(`${field.rawKey}=${encodeMarkedFormValue(marked, unplaced)}`)
      }
    } else {
      parts.push(`${field.rawKey}=${field.rawValue}`)
    }
  }
  for (const [key, val] of updatedValues.entries()) {
    if (!seen.has(key)) {
      parts.push(`${encodeFormValuePreservingTemplates(key)}=${encodeFormValuePreservingTemplates(val)}`)
    }
  }
  return parts.join('&')
}

const APEX_EXECUTE_DESCRIPTOR = 'aura://ApexActionController/ACTION$execute'
const DESCRIPTOR_SCHEMES = ['aura://', 'serviceComponent://', 'apex://']

function stripDescriptorScheme(descriptor: string): { rest: string; apex: boolean } {
  for (const scheme of DESCRIPTOR_SCHEMES) {
    if (descriptor.slice(0, scheme.length).toLowerCase() === scheme.toLowerCase()) {
      return { rest: descriptor.slice(scheme.length), apex: scheme === 'apex://' }
    }
  }
  return { rest: descriptor, apex: false }
}

/**
 * A sub-action's short title, as the importer's step names give it
 * (aura.ActionDescriptor.ShortTitle): an ApexActionController call is
 * `[namespace.]classname.method`; an `apex://[namespace.]Class/ACTION$method`
 * descriptor is `[namespace.]Class.method`; any other descriptor is its
 * controller's last name and the method (`RecordUiController.getRecordWithFields`).
 */
export function formatAuraActionTitle(subAction: AuraSubAction, fallbackIndex = 0): string {
  const descriptor = typeof subAction.descriptor === 'string' ? subAction.descriptor.trim() : ''
  const params = subAction.params && typeof subAction.params === 'object' && !Array.isArray(subAction.params)
    ? subAction.params
    : undefined
  const className = typeof params?.classname === 'string' ? params.classname.trim() : ''
  const methodName = typeof params?.method === 'string' ? params.method.trim() : ''
  const namespace = typeof params?.namespace === 'string' ? params.namespace.trim() : ''
  if (descriptor === APEX_EXECUTE_DESCRIPTOR && className && methodName) {
    return namespace ? `${namespace}.${className}.${methodName}` : `${className}.${methodName}`
  }
  const { rest, apex } = stripDescriptorScheme(descriptor)
  if (!rest) return `Action ${fallbackIndex + 1}`
  const actionAt = rest.indexOf('/ACTION$')
  let controller = actionAt >= 0 ? rest.slice(0, actionAt) : rest
  const method = actionAt >= 0 ? rest.slice(actionAt + '/ACTION$'.length) : ''
  // An apex:// controller is [namespace.]Class, kept whole; other
  // controllers drop their package path.
  const lastDot = controller.lastIndexOf('.')
  if (!apex && lastDot >= 0 && lastDot + 1 < controller.length) controller = controller.slice(lastDot + 1)
  return method ? `${controller}.${method}` : controller
}

export function parseAuraResponseEnvelope(rawBody?: string): ParsedAuraResponse | null {
  if (!rawBody) return null
  let cleaned = rawBody.trim()
  cleaned = cleaned.replace(/^(?:while\s*\(\s*1\s*\)\s*;?|for\s*\(\s*;\s*;\s*\)\s*;?|\*\/)\s*/i, '')
  cleaned = cleaned.replace(/\s*\/\*[A-Z_]*\*\/\s*$/i, '')
  if (!cleaned.startsWith('{')) return null
  try {
    const parsed: unknown = JSON.parse(cleaned)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    const record = parsed as Record<string, unknown>
    if (!Array.isArray(record.actions)) return null
    const actions: AuraResponseActionSummary[] = []
    let hasActionError = false
    for (const entry of record.actions) {
      if (!entry || typeof entry !== 'object' || Array.isArray(entry)) continue
      const item = entry as Record<string, unknown>
      const id = typeof item.id === 'string' ? item.id : String(actions.length + 1)
      const state = typeof item.state === 'string' ? item.state.toUpperCase() : 'UNKNOWN'
      if (state === 'ERROR' || state === 'INCOMPLETE') hasActionError = true
      let errorSummary: string | undefined
      if (Array.isArray(item.error) && item.error.length > 0) {
        const firstErr = item.error[0]
        if (firstErr && typeof firstErr === 'object' && !Array.isArray(firstErr)) {
          const errRec = firstErr as Record<string, unknown>
          const message = typeof errRec.message === 'string'
            ? errRec.message
            : typeof errRec.exceptionMessage === 'string'
              ? errRec.exceptionMessage
              : JSON.stringify(firstErr)
          errorSummary = message.slice(0, 240)
        } else if (typeof firstErr === 'string') {
          errorSummary = firstErr.slice(0, 240)
        }
      }
      let returnValuePreview: string | undefined
      if (item.returnValue !== undefined && item.returnValue !== null) {
        const serialized = typeof item.returnValue === 'string' ? item.returnValue : JSON.stringify(item.returnValue)
        if (serialized) returnValuePreview = serialized.length > 160 ? `${serialized.slice(0, 157)}…` : serialized
      }
      actions.push({ id, state, ...(errorSummary ? { errorSummary } : {}), ...(returnValuePreview ? { returnValuePreview } : {}) })
    }
    return actions.length > 0 ? { actions, hasActionError } : null
  } catch {
    return null
  }
}
