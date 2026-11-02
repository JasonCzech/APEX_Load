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
   * the field is re-encoded rather than becoming a live template.
   */
  literalTemplates?: Map<string, string[]>
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
  const literalTemplates: string[] = []
  const value = raw
    .split(TEMPLATE_TOKEN_PATTERN)
    .map(part => {
      if (isTemplateToken(part)) return part
      const decoded = safeDecodeURIComponent(part)
      for (const token of decoded.match(TEMPLATE_TOKEN_PATTERN) || []) literalTemplates.push(token)
      return decoded
    })
    .join('')
  return { value, literalTemplates }
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
  const literalTemplates = new Map<string, string[]>()
  const decodeField = (entry: { key: string; rawValue: string }) => {
    const decoded = decodeFormValueWithLiteralTemplates(entry.rawValue)
    if (decoded.literalTemplates.length > 0) literalTemplates.set(entry.key, decoded.literalTemplates)
    return decoded.value
  }
  const decodedMessage = decodeField(messageEntry)
  const messageObject = parseJsonObjectWithTemplates(decodedMessage, unquotedTemplates)
  if (!messageObject || !Array.isArray(messageObject.actions)) return null
  const actions = messageObject.actions.filter(
    (item): item is AuraSubAction => Boolean(item && typeof item === 'object' && !Array.isArray(item)),
  )
  if (actions.length !== messageObject.actions.length) return null

  const contextEntry = fieldOrder.find(entry => entry.key === 'aura.context')
  const contextRaw = contextEntry ? decodeField(contextEntry) : ''
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

function auraEnvelopeFieldValues(envelope: ParsedAuraEnvelope): Map<string, string> {
  const nextMessageJson = restoreUnquotedTemplates(
    JSON.stringify({ ...envelope.messageObject, actions: envelope.actions }),
    envelope.unquotedTemplates,
  )
  const nextContextJson = envelope.contextObject
    ? restoreUnquotedTemplates(JSON.stringify(envelope.contextObject), envelope.unquotedTemplates)
    : envelope.contextRaw

  return new Map<string, string>([
    ['message', nextMessageJson],
    ...(nextContextJson ? [['aura.context', nextContextJson] as const] : []),
    ...(envelope.pageURI || envelope.fieldOrder.some(f => f.key === 'aura.pageURI')
      ? [['aura.pageURI', envelope.pageURI] as const]
      : []),
    ...(envelope.token || envelope.fieldOrder.some(f => f.key === 'aura.token')
      ? [['aura.token', envelope.token] as const]
      : []),
  ])
}

export function serializeAuraFormEnvelope(envelope: ParsedAuraEnvelope): string {
  const updatedValues = auraEnvelopeFieldValues(envelope)

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
        parts.push(`${field.rawKey}=${encodeFormValuePreservingTemplates(val, envelope.literalTemplates?.get(field.key))}`)
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
