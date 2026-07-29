import type { HTTPConfig, ScriptHeaderEntry } from '../types/scripts'

const TRANSPORT_OWNED_HEADERS = new Set([
  'content-length',
  'transfer-encoding',
  'trailer',
  'proxy-authorization',
])
const HOP_BY_HOP_HEADERS = new Set(['connection', 'keep-alive', 'proxy-connection', 'te', 'upgrade'])
const VALID_NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

export interface BulkParseResult<T> {
  entries: T[]
  errors: string[]
}

export interface BulkCookieEntry {
  name: string
  value: string
}

function canonical(name: string): string {
  return name.trim().toLowerCase()
}

function enabled(entry: ScriptHeaderEntry): boolean {
  return entry.enabled !== false
}

function defaultHeaderEntries(value: HTTPConfig): ScriptHeaderEntry[] {
  const ordered = value.default_header_list || []
  const orderedNames = new Set(ordered.map(header => canonical(header.name)).filter(Boolean))
  const compatibility = Object.entries(value.default_headers || {})
    .filter(([name]) => !orderedNames.has(canonical(name)))
    .map(([name, headerValue]) => ({ name, value: headerValue }))
  return [...ordered, ...compatibility]
}

function pinnedCookieNames(value: HTTPConfig): Array<{ enabled: boolean, masked: boolean, name: string }> {
  return defaultHeaderEntries(value)
    .filter(entry => canonical(entry.name) === 'cookie')
    .flatMap<{ enabled: boolean, masked: boolean, name: string }>(entry => {
      if (entry.value === '[REDACTED]') {
        return [{ enabled: enabled(entry), masked: true, name: '' }]
      }
      return entry.value.split(';').map(part => part.trim()).filter(Boolean).map(part => {
        const separator = part.indexOf('=')
        return {
          enabled: enabled(entry),
          masked: separator >= 0 && part.slice(separator + 1).trim() === '[REDACTED]',
          name: (separator >= 0 ? part.slice(0, separator) : part).trim(),
        }
      })
    })
}

export function countHTTPDefaultHeaderErrors(value?: HTTPConfig): number {
  const http = value || {}
  const entries = defaultHeaderEntries(http).filter(entry => enabled(entry) && canonical(entry.name) !== 'cookie')
  return entries.reduce((count, entry) => {
    const name = entry.name.trim()
    const normalized = canonical(name)
    if (!name || !VALID_NAME.test(name)) return count + 1
    let errors = 0
    if (/[\r\n]/.test(entry.value)) errors++
    if (TRANSPORT_OWNED_HEADERS.has(normalized)) errors++
    if (HOP_BY_HOP_HEADERS.has(normalized) && http.version !== '1.1') errors++
    if ((normalized === 'host' || normalized === 'user-agent') &&
      entries.filter(candidate => canonical(candidate.name) === normalized).length > 1) errors++
    return count + errors
  }, 0)
}

export function countPinnedCookieErrors(value?: HTTPConfig): number {
  const cookies = pinnedCookieNames(value || {})
  return cookies.filter(cookie => {
    if (!cookie.enabled) return false
    if (cookie.masked) return false
    if (!cookie.name || !VALID_NAME.test(cookie.name)) return true
    return cookies.filter(candidate => candidate.enabled && candidate.name.trim() === cookie.name.trim()).length > 1
  }).length
}

export function countHTTPDefaultHeaders(value?: HTTPConfig): number {
  return defaultHeaderEntries(value || {})
    .filter(entry => enabled(entry) && canonical(entry.name) !== 'cookie')
    .length
}

export function countPinnedCookies(value?: HTTPConfig): number {
  return pinnedCookieNames(value || {}).filter(cookie => cookie.enabled).length
}

function unwrapHeaderArgument(raw: string): string {
  let value = raw.trim()
  if (/^--header(?:\s+|=)/i.test(value)) {
    value = value.replace(/^--header(?:\s+|=)/i, '').trim()
  } else if (/^-H/i.test(value)) {
    value = value.replace(/^-H\s*/i, '').trim()
  }
  if (value.length >= 2) {
    const first = value[0]
    const last = value[value.length - 1]
    if ((first === '"' && last === '"') || (first === "'" && last === "'")) {
      value = value.slice(1, -1)
    }
  }
  return value
}

export function parseBulkHeaders(raw: string): BulkParseResult<ScriptHeaderEntry> {
  const entries: ScriptHeaderEntry[] = []
  const errors: string[] = []
  raw.split(/\r?\n/).forEach((sourceLine, index) => {
    const line = unwrapHeaderArgument(sourceLine)
    if (!line) return
    const separator = line.indexOf(':')
    const name = separator >= 0 ? line.slice(0, separator).trim() : ''
    if (separator <= 0 || !VALID_NAME.test(name)) {
      errors.push(`Line ${index + 1}: enter a valid “Name: Value” header.`)
      return
    }
    entries.push({
      enabled: true,
      name,
      value: line.slice(separator + 1).trim(),
    })
  })
  return { entries, errors }
}

export function parseBulkCookies(raw: string): BulkParseResult<BulkCookieEntry> {
  const entries: BulkCookieEntry[] = []
  const errors: string[] = []
  let itemIndex = 0
  raw.split(/\r?\n/).forEach(sourceLine => {
    let line = unwrapHeaderArgument(sourceLine)
    if (/^cookie\s*:/i.test(line)) line = line.replace(/^cookie\s*:/i, '').trim()
    line.split(';').map(part => part.trim()).filter(Boolean).forEach(part => {
      itemIndex++
      const separator = part.indexOf('=')
      const name = separator >= 0 ? part.slice(0, separator).trim() : ''
      if (separator <= 0 || !VALID_NAME.test(name)) {
        errors.push(`Cookie item ${itemIndex}: enter a valid “name=value” pair.`)
        return
      }
      entries.push({ name, value: part.slice(separator + 1).trim() })
    })
  })
  if (raw.trim() && entries.length === 0 && errors.length === 0) {
    errors.push('Enter at least one valid “name=value” cookie.')
  }
  return { entries, errors }
}

export function formatHTTPSettingsSummary(http?: HTTPConfig, newUserEachIteration = false): string {
  const value = http || {}
  const headerCount = countHTTPDefaultHeaders(value)
  const cookieCount = countPinnedCookies(value)
  const cache = value.cache_enabled ? 'cache enabled' : 'cache disabled'
  const state = value.cookies_enabled === false
    ? 'cookie jar disabled'
    : newUserEachIteration
      ? 'new user each iteration'
      : value.clear_cookies_per_iteration
        ? 'cookies cleared each iteration'
        : 'cookies preserved'
  const keepAlive = value.keep_alive === false ? 'keep-alive off' : 'keep-alive on'
  return `${headerCount} default header${headerCount === 1 ? '' : 's'} · ${cookieCount} pinned cookie${cookieCount === 1 ? '' : 's'} · ${cache} · ${state} · ${keepAlive}`
}
