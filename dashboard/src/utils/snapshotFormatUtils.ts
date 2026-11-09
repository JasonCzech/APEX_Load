function isJSONContentType(value?: string): boolean {
  return Boolean(value && value.toLowerCase().includes('json'))
}

function isXMLContentType(value?: string): boolean {
  if (!value) return false
  const lower = value.toLowerCase()
  return lower.includes('xml') || lower.includes('soap') || lower.includes('html')
}

export type CapturedBodyFormat = 'json' | 'xml' | 'plain'

export function detectCapturedBodyFormat(body: string, headers?: Record<string, string>): CapturedBodyFormat {
  const contentType = Object.entries(headers || {}).find(([key]) => key.toLowerCase() === 'content-type')?.[1]
  const trimmed = body.trim()

  if (isJSONContentType(contentType) || ((trimmed.startsWith('{') || trimmed.startsWith('[')) && !trimmed.startsWith('<'))) {
    return 'json'
  }

  if (isXMLContentType(contentType) || trimmed.startsWith('<')) {
    return 'xml'
  }

  return 'plain'
}

function formatJSONBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2)
  } catch {
    return body
  }
}

function decodeSafeTextEntity(entity: string): string {
  const lower = entity.toLowerCase()
  const namedEntities: Record<string, string> = {
    '&amp;': '&',
    '&apos;': "'",
    '&nbsp;': ' ',
    '&quot;': '"',
  }
  if (namedEntities[lower]) return namedEntities[lower]
  if (lower === '&lt;' || lower === '&gt;') return entity

  const decimalMatch = entity.match(/^&#(\d+);$/)
  if (decimalMatch) {
    const codePoint = Number(decimalMatch[1])
    if (Number.isInteger(codePoint) && codePoint !== 60 && codePoint !== 62) {
      try {
        return String.fromCodePoint(codePoint)
      } catch {
        return entity
      }
    }
  }

  const hexMatch = entity.match(/^&#x([0-9a-f]+);$/i)
  if (hexMatch) {
    const codePoint = Number.parseInt(hexMatch[1], 16)
    if (Number.isInteger(codePoint) && codePoint !== 60 && codePoint !== 62) {
      try {
        return String.fromCodePoint(codePoint)
      } catch {
        return entity
      }
    }
  }

  return entity
}

function decodeMarkupTextEntities(value: string): string {
  const tagPattern = /<[^>]*>/g
  let cursor = 0
  let decoded = ''

  for (const match of value.matchAll(tagPattern)) {
    const index = match.index ?? 0
    if (index > cursor) {
      decoded += value.slice(cursor, index).replace(/&(?:amp|apos|nbsp|quot|lt|gt|#\d+|#x[0-9a-f]+);/gi, decodeSafeTextEntity)
    }
    decoded += match[0]
    cursor = index + match[0].length
  }

  if (cursor < value.length) {
    decoded += value.slice(cursor).replace(/&(?:amp|apos|nbsp|quot|lt|gt|#\d+|#x[0-9a-f]+);/gi, decodeSafeTextEntity)
  }

  return decoded
}

function getMarkupTagName(value: string): string {
  const match = value.match(/^<\/?\s*([^\s/>]+)/)
  return match?.[1]?.toLowerCase() || ''
}

function formatXMLBody(body: string): string {
  const normalized = body.trim().replace(/>\s+</g, '><')
  if (!normalized.startsWith('<')) return body

  const tokens = normalized.match(/<[^>]+>|[^<]+/g)?.filter(token => token.trim()) || []

  let indentLevel = 0
  const lines: string[] = []

  for (let index = 0; index < tokens.length; index += 1) {
    const token = tokens[index]
    const trimmed = token.trim()
    if (!trimmed) continue

    if (/^<\//.test(trimmed)) {
      indentLevel = Math.max(indentLevel - 1, 0)
      lines.push(`${'  '.repeat(indentLevel)}${trimmed}`)
      continue
    }

    if (!trimmed.startsWith('<')) {
      lines.push(`${'  '.repeat(indentLevel)}${decodeMarkupTextEntities(trimmed)}`)
      continue
    }

    const opensBlock = /^<(?!!|\?|\/)([^>]+)>$/.test(trimmed)
      && !/\/>$/.test(trimmed)
      && !/<[^>]+>.*<\/[^>]+>$/.test(trimmed)
    if (opensBlock) {
      const nextToken = tokens[index + 1]?.trim()
      const closingToken = tokens[index + 2]?.trim()
      const tagName = getMarkupTagName(trimmed)
      const closingTagName = closingToken ? getMarkupTagName(closingToken) : ''
      if (nextToken && !nextToken.startsWith('<') && closingToken?.startsWith('</') && tagName === closingTagName) {
        lines.push(`${'  '.repeat(indentLevel)}${trimmed}${decodeMarkupTextEntities(nextToken)}${closingToken}`)
        index += 2
      } else {
        lines.push(`${'  '.repeat(indentLevel)}${trimmed}`)
        indentLevel += 1
      }
      continue
    }

    lines.push(`${'  '.repeat(indentLevel)}${trimmed}`)
  }

  return lines.join('\n')
}

export interface SnapshotCopySection {
  request_method?: string
  request_url?: string
  request_headers?: Record<string, string>
  request_headers_omitted?: boolean
  request_wire_headers_unavailable?: boolean
  request_body?: string
  response_status?: number
  response_headers?: Record<string, string>
  response_body?: string
}

export function formatCapturedHeaders(headers?: Record<string, string>, emptyMessage = 'n/a'): string {
  if (!headers || Object.keys(headers).length === 0) return emptyMessage
  return Object.entries(headers)
    .map(([key, value]) => `${key}: ${value}`)
    .join('\n')
}

export function formatSnapshotRequestLineAndHeaders(snapshot?: SnapshotCopySection): string {
  const requestLine = `${snapshot?.request_method || 'n/a'} ${snapshot?.request_url || ''}`.trim()
  return [
    requestLine,
    '',
    'Headers',
    formatCapturedHeaders(snapshot?.request_headers),
    ...(snapshot?.request_wire_headers_unavailable ? ['Actual wire headers could not be verified; the displayed headers describe the connection attempt and may be incomplete.'] : []),
    ...(snapshot?.request_headers_omitted ? ['Some request headers were omitted or shortened by retention limits.'] : []),
  ].join('\n')
}

export function formatSnapshotResponseStatusAndHeaders(snapshot?: SnapshotCopySection, fallbackStatus?: number): string {
  return [
    `Status: ${String(snapshot?.response_status || fallbackStatus || 'n/a')}`,
    '',
    'Headers',
    formatCapturedHeaders(snapshot?.response_headers),
  ].join('\n')
}

export function formatCapturedBody(body?: string, headers?: Record<string, string>, emptyMessage = 'No body captured'): string {
  if (!body) return emptyMessage

  switch (detectCapturedBodyFormat(body, headers)) {
    case 'json':
      return formatJSONBody(body)
    case 'xml':
      return formatXMLBody(body)
    default:
      return body
  }
}

export function formatSnapshotRequestSection(snapshot?: SnapshotCopySection): string {
  const requestLine = `${snapshot?.request_method || 'n/a'} ${snapshot?.request_url || ''}`.trim()
  return [
    'Request',
    requestLine,
    '',
    'Headers',
    formatCapturedHeaders(snapshot?.request_headers),
    ...(snapshot?.request_wire_headers_unavailable ? ['Actual wire headers could not be verified; the displayed headers describe the connection attempt and may be incomplete.'] : []),
    ...(snapshot?.request_headers_omitted ? ['Some request headers were omitted or shortened by retention limits.'] : []),
    '',
    'Body',
    formatCapturedBody(snapshot?.request_body, snapshot?.request_headers, 'No request body captured'),
  ].join('\n')
}

export function formatSnapshotResponseSection(snapshot?: SnapshotCopySection, fallbackStatus?: number): string {
  return [
    'Response',
    `Status: ${String(snapshot?.response_status || fallbackStatus || 'n/a')}`,
    '',
    'Headers',
    formatCapturedHeaders(snapshot?.response_headers),
    '',
    'Body',
    formatCapturedBody(snapshot?.response_body, snapshot?.response_headers, 'No response body captured'),
  ].join('\n')
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

const DEBUG_HTTP_METHOD_STATUS_PATTERN = /^(CONNECT|DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT|TRACE)(\s+)([1-5]\d{2})(\s+)(\S+)(.*)$/i
const DEBUG_HTTP_METHOD_PATTERN = /^(CONNECT|DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT|TRACE)(\s+)(\S+)(.*)$/i
const DEBUG_HTTP_STATUS_PATTERN = /^(HTTP\/\S+\s+)(\d{3})(.*)$/i
const DEBUG_STANDALONE_STATUS_PATTERN = /^([1-5]\d{2})(\s+.*)?$/
const DEBUG_CONSOLE_LEVEL_PATTERN = /^(\[(?:error|warn|warning|info|debug|trace|log)\])(\s+)(.*)$/i
const DEBUG_SECTION_HEADER_PATTERN = /^(REQUEST|HEADERS|BODY|RESPONSE|RESPONSE HEADERS|RESPONSE BODY|TRANSPORT|TIMINGS|TLS|COOKIES & REDIRECTS|EXTRACTIONS|BROWSER CONSOLE|BROWSER NETWORK|NETWORK HOPS|RESOURCES)\s*$/
const DEBUG_HEADER_PATTERN = /^([!#$%&'*+\-.^_`|~0-9A-Za-z]+)(:\s*)(.*)$/
const DEBUG_LITERAL_PATTERN = /"(?:\\.|[^"\\])*"|\b(?:true|false|null)\b|-?\b\d+(?:\.\d+)?\b|[\u005b\u005d{},]|:(?!\/\/)/g

function statusCodeTokenKind(status: number): DebugLogTokenKind {
  if (status >= 500) return 'status-error'
  if (status >= 400) return 'status-warning'
  return 'status-success'
}

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
  if (DEBUG_SECTION_HEADER_PATTERN.test(line)) {
    return [{ kind: 'header', text: line }]
  }

  const networkEntry = line.match(DEBUG_HTTP_METHOD_STATUS_PATTERN)
  if (networkEntry) {
    return [
      { kind: 'method', text: networkEntry[1] },
      { kind: 'plain', text: networkEntry[2] },
      { kind: statusCodeTokenKind(Number(networkEntry[3])), text: networkEntry[3] },
      { kind: 'plain', text: networkEntry[4] },
      { kind: 'target', text: networkEntry[5] },
      ...tokenizeDebugLiterals(networkEntry[6]),
    ]
  }

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
    return [
      { kind: 'plain', text: response[1] },
      { kind: statusCodeTokenKind(Number(response[2])), text: response[2] },
      { kind: 'plain', text: response[3] },
    ]
  }

  const standaloneStatus = line.match(DEBUG_STANDALONE_STATUS_PATTERN)
  if (standaloneStatus) {
    const tokens: DebugLogToken[] = [
      { kind: statusCodeTokenKind(Number(standaloneStatus[1])), text: standaloneStatus[1] },
    ]
    if (standaloneStatus[2]) {
      tokens.push({ kind: 'plain', text: standaloneStatus[2] })
    }
    return tokens
  }

  const consoleEntry = line.match(DEBUG_CONSOLE_LEVEL_PATTERN)
  if (consoleEntry) {
    const levelLower = consoleEntry[1].slice(1, -1).toLowerCase()
    const levelKind: DebugLogTokenKind = levelLower === 'error'
      ? 'status-error'
      : levelLower === 'warn' || levelLower === 'warning'
        ? 'status-warning'
        : levelLower === 'debug' || levelLower === 'trace'
          ? 'literal'
          : 'method'
    return [
      { kind: levelKind, text: consoleEntry[1] },
      { kind: 'plain', text: consoleEntry[2] },
      ...tokenizeDebugLiterals(consoleEntry[3]),
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
