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

function formatXMLBody(body: string): string {
  const normalized = body.trim().replace(/>\s+</g, '><')
  if (!normalized.startsWith('<')) return body

  const tokens = normalized
    .replace(/(>)(<)(\/?)/g, '$1\n$2$3')
    .split('\n')
    .filter(Boolean)

  let indentLevel = 0
  const lines: string[] = []

  tokens.forEach(token => {
    const trimmed = token.trim()
    if (!trimmed) return

    if (/^<\//.test(trimmed)) {
      indentLevel = Math.max(indentLevel - 1, 0)
    }

    lines.push(`${'  '.repeat(indentLevel)}${trimmed}`)

    const opensBlock = /^<(?!!|\?|\/)([^>]+)>$/.test(trimmed)
      && !/\/>$/.test(trimmed)
      && !/<[^>]+>.*<\/[^>]+>$/.test(trimmed)
    if (opensBlock) {
      indentLevel += 1
    }
  })

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