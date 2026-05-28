function isJSONContentType(value?: string): boolean {
  return Boolean(value && value.toLowerCase().includes('json'))
}

function isXMLContentType(value?: string): boolean {
  if (!value) return false
  const lower = value.toLowerCase()
  return lower.includes('xml') || lower.includes('soap')
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
