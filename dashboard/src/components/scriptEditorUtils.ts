import {
  BrowserReplayConsoleEntry,
  BrowserReplayEvent,
  BrowserReplayNetworkEntry,
} from '../types'
import {
  type ScriptActionDefinition,
  type ScriptDataSource,
  type ScriptDefinition,
  type ScriptImportMetadata,
  type ScriptParameterDefinition,
} from '../types/scripts'

type Action = ScriptActionDefinition
type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition
type ConsoleEntry = BrowserReplayConsoleEntry
type NetworkEntry = BrowserReplayNetworkEntry
type ReplayActionEvent = BrowserReplayEvent

export const EMPTY_SCRIPT: ScriptDefinition = {
  name: '', version: '1.0.0', protocol: 'http',
  config: {
    base_url: '',
    think_time: { type: 'fixed', mean_ms: 1000 },
    pacing: { type: 'none' },
    run_logic: { iterations: 0, auto_transactions: 'none' },
    error_handling: { continue_on_error: false },
    log: { level: 'standard' },
  },
  actions: [],
}

export const EMPTY_DATA_SOURCE: DataSourceConfig = {
  name: '',
  type: 'csv',
  path: '',
  strategy: 'sequential',
  delimiter: ',',
  first_row_header: true,
  columns: [],
  inline_data: '',
}

export const EMPTY_PARAMETER: ParameterConfig = {
  name: '',
  type: 'list',
  update: 'each_iteration',
  selection: 'sequential',
  on_exhaustion: 'wrap',
  values: [],
  step: 1,
  length: 12,
  format: 'iso8601',
  pad_char: '0',
}

export const EMPTY_ACTION: Action = {
  name: '', type: 'GET', target: '', headers: {}, body: '',
  extractors: [], assertions: [], think_time_after: true,
}

export const EMPTY_BROWSER_ACTION: Action = {
  name: '', type: 'browser_navigate', target: '', selector: '', value: '',
  capture: { screenshot: false, full_page: false, html: false },
  think_time_after: true,
}

export const EMPTY_JMS_ACTION: Action = {
  name: '', type: 'jms_publish', target: '', headers: {}, body: '',
  params: {}, metadata: {}, extractors: [], assertions: [{ type: 'message_received' }],
  think_time_after: true,
}

export const EMPTY_SOAP_ACTION: Action = {
  name: '', type: 'POST', method: 'POST', target: '', headers: {}, body: '',
  extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true,
  soap: { action: '', operation: '', body_mode: 'raw_xml' },
}

export function getImportSummary(metadata?: ScriptImportMetadata | null) {
  const preserved = metadata?.preserved || {}

  return {
    runtimeFiles: Object.keys(preserved.runtime_files || {}),
    projectSections: Object.keys(preserved.project_settings || {}),
    unsupportedFunctions: Array.isArray(preserved.unsupported_functions) ? preserved.unsupported_functions : [],
  }
}

export function inferDelimiter(source: DataSourceConfig): string {
  if (source.delimiter && source.delimiter.length > 0) return source.delimiter
  return source.type === 'dat' ? '\t' : ','
}

export function summarizeInlineData(source: DataSourceConfig): { rows: number; columns: number } {
  const raw = source.inline_data || ''
  if (!raw.trim()) return { rows: 0, columns: 0 }
  const lines = raw.split(/\r?\n/).filter(line => line.trim().length > 0)
  if (lines.length === 0) return { rows: 0, columns: 0 }
  const delimiter = inferDelimiter(source)
  const width = lines.reduce((max, line) => Math.max(max, line.split(delimiter).length), 0)
  const dataRows = source.first_row_header === false ? lines.length : Math.max(lines.length - 1, 0)
  return { rows: dataRows, columns: width }
}

export function parseInlineData(source: DataSourceConfig): { columns: string[]; rows: string[][] } {
  const raw = source.inline_data || ''
  const delimiter = inferDelimiter(source)
  const lines = raw.split(/\r?\n/).filter(line => line.trim().length > 0)
  if (lines.length === 0) {
    const fallbackCols = source.columns && source.columns.length > 0 ? [...source.columns] : ['col_1']
    return { columns: fallbackCols, rows: [] }
  }

  const split = (line: string) => line.split(delimiter).map(value => value.trim())
  if (source.first_row_header !== false) {
    const header = split(lines[0]).filter(Boolean)
    const columns = header.length > 0 ? header : ['col_1']
    const rows = lines.slice(1).map(line => {
      const parts = split(line)
      const normalized = [...parts]
      while (normalized.length < columns.length) normalized.push('')
      return normalized.slice(0, columns.length)
    })
    return { columns, rows }
  }

  const declared = source.columns && source.columns.length > 0 ? [...source.columns] : []
  const inferredWidth = lines.reduce((max, line) => Math.max(max, split(line).length), 0)
  const width = Math.max(declared.length, inferredWidth, 1)
  const columns = declared.length > 0 ? declared : Array.from({ length: width }, (_, i) => `col_${i + 1}`)
  const rows = lines.map(line => {
    const parts = split(line)
    const normalized = [...parts]
    while (normalized.length < columns.length) normalized.push('')
    return normalized.slice(0, columns.length)
  })
  return { columns, rows }
}

export function stringifyInlineData(source: DataSourceConfig, columns: string[], rows: string[][]): string {
  const delimiter = inferDelimiter(source)
  const escaped = (value: string) => {
    if (value.includes('"') || value.includes('\n') || value.includes(delimiter)) {
      return `"${value.replace(/"/g, '""')}"`
    }
    return value
  }
  const normalizedRows = rows
    .map(row => columns.map((_, idx) => escaped((row[idx] || '').trim())))
    .map(row => row.join(delimiter))

  if (source.first_row_header !== false) {
    return [columns.map(escaped).join(delimiter), ...normalizedRows].join('\n')
  }
  return normalizedRows.join('\n')
}

export function validateInlineData(source: DataSourceConfig): string[] {
  const warnings: string[] = []
  const raw = source.inline_data || ''
  const delimiter = inferDelimiter(source)
  const lines = raw.split(/\r?\n/).filter(line => line.trim().length > 0)
  const split = (line: string) => line.split(delimiter)
  const { columns } = parseInlineData(source)

  if (source.first_row_header !== false) {
    const emptyHeaders = columns.filter(name => !name.trim()).length
    if (emptyHeaders > 0) {
      warnings.push(`Header row has ${emptyHeaders} empty column name${emptyHeaders > 1 ? 's' : ''}.`)
    }

    const seen = new Set<string>()
    const duplicates = new Set<string>()
    columns.forEach(name => {
      const key = name.trim().toLowerCase()
      if (!key) return
      if (seen.has(key)) duplicates.add(name.trim())
      seen.add(key)
    })
    if (duplicates.size > 0) {
      warnings.push(`Duplicate header name(s): ${Array.from(duplicates).join(', ')}.`)
    }
  }

  const expectedWidth = columns.length
  if (expectedWidth > 0 && lines.length > 0) {
    const startIdx = source.first_row_header !== false ? 1 : 0
    const badRows: number[] = []
    for (let i = startIdx; i < lines.length; i++) {
      if (split(lines[i]).length !== expectedWidth) badRows.push(i + 1)
    }
    if (badRows.length > 0) {
      warnings.push(
        `Row width mismatch on line${badRows.length > 1 ? 's' : ''} ${badRows.slice(0, 6).join(', ')}${badRows.length > 6 ? ', ...' : ''}. Expected ${expectedWidth} value${expectedWidth === 1 ? '' : 's'} per row.`,
      )
    }
  }

  return warnings
}

export function normalizeHeaders(columns: string[]): string[] {
  const seen = new Set<string>()
  return columns.map((name, idx) => {
    let base = (name || '').trim()
    if (!base) base = `col_${idx + 1}`
    let next = base
    let suffix = 2
    while (seen.has(next.toLowerCase())) {
      next = `${base}_${suffix}`
      suffix++
    }
    seen.add(next.toLowerCase())
    return next
  })
}

export function fillEmptyHeaders(columns: string[]): string[] {
  return columns.map((name, idx) => (name || '').trim() || `col_${idx + 1}`)
}

export function dedupeHeaders(columns: string[]): string[] {
  const seen = new Set<string>()
  return columns.map((name, idx) => {
    const base = (name || '').trim() || `col_${idx + 1}`
    let next = base
    let suffix = 2
    while (seen.has(next.toLowerCase())) {
      next = `${base}_${suffix}`
      suffix++
    }
    seen.add(next.toLowerCase())
    return next
  })
}

export function downloadTextFile(content: string, filename: string) {
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

export function cloneDataSource(source: DataSourceConfig): DataSourceConfig {
  return JSON.parse(JSON.stringify(source))
}

export function sanitizeVarName(input: string): string {
  const normalized = (input || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_]+/g, '_')
    .replace(/^_+|_+$/g, '')
  return normalized || 'param'
}

export function ensureUniqueParameterName(requested: string, parameters: ParameterConfig[], existingName?: string): string {
  const base = sanitizeVarName(requested)
  const normalizedExisting = existingName ? sanitizeVarName(existingName) : ''
  const seen = new Set(
    parameters
      .map(parameter => sanitizeVarName(parameter.name || ''))
      .filter(name => name.length > 0 && name !== normalizedExisting),
  )
  if (!seen.has(base)) return base
  let suffix = 2
  let next = `${base}_${suffix}`
  while (seen.has(next)) {
    suffix += 1
    next = `${base}_${suffix}`
  }
  return next
}

export function formatReplayTime(value?: string): string {
  if (!value) return 'n/a'
  return new Date(value).toLocaleString()
}

export function formatReplayHeaders(headers?: Record<string, string>): string {
  if (!headers || Object.keys(headers).length === 0) return 'None'
  return Object.entries(headers).map(([key, value]) => `${key}: ${value}`).join('\n')
}

export function formatConsoleEntries(entries?: ConsoleEntry[]): string {
  if (!entries || entries.length === 0) return 'None'
  return entries
    .map(entry => `[${entry.level || 'log'}] ${entry.text || ''}${entry.source ? ` (${entry.source})` : ''}`)
    .join('\n')
}

export function formatNetworkEntries(entries?: NetworkEntry[]): string {
  if (!entries || entries.length === 0) return 'None'
  return entries
    .map(entry => {
      const status = entry.status_code ? ` ${entry.status_code}` : ''
      const duration = typeof entry.duration_ms === 'number' ? ` ${entry.duration_ms.toFixed(1)} ms` : ''
      return `${entry.method || 'GET'}${status} ${entry.url || ''}${duration}`
    })
    .join('\n')
}

export function hasBrowserReplayArtifacts(event: ReplayActionEvent): boolean {
  return !!(
    event.dom_summary ||
    event.snapshot?.dom_summary ||
    event.console_entries?.length ||
    event.snapshot?.console_entries?.length ||
    event.network_entries?.length ||
    event.snapshot?.network_entries?.length ||
    event.html ||
    event.snapshot?.html ||
    event.screenshot_base64 ||
    event.snapshot?.screenshot_base64
  )
}

export function createReplaySessionId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `replay-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

export function getErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message
  if (typeof error === 'object' && error !== null && 'message' in error) {
    const message = (error as { message?: unknown }).message
    if (typeof message === 'string' && message.length > 0) return message
  }
  return fallback
}

export function getErrorIssues(error: unknown): string[] {
  if (typeof error !== 'object' || error === null || !('payload' in error)) return []
  const payload = (error as { payload?: unknown }).payload
  if (typeof payload !== 'object' || payload === null || !('issues' in payload)) return []
  const issues = (payload as { issues?: unknown }).issues
  return Array.isArray(issues) ? issues.filter((issue): issue is string => typeof issue === 'string') : []
}

export function mergeReplayVariables(
  seed: Record<string, string> | undefined,
  events: ReplayActionEvent[],
): Record<string, string> {
  const next: Record<string, string> = { ...(seed || {}) }
  events.forEach(event => {
    Object.entries(event.extractions || {}).forEach(([key, value]) => {
      next[key] = value
    })
  })
  return next
}

export function buildReplayVariableDiffs(
  seed: Record<string, string> | undefined,
  events: ReplayActionEvent[],
): Array<Record<string, { previous?: string; next: string }>> {
  const current: Record<string, string> = { ...(seed || {}) }
  return events.map(event => {
    const diff: Record<string, { previous?: string; next: string }> = {}
    Object.entries(event.extractions || {}).forEach(([key, value]) => {
      const previous = current[key]
      if (previous !== value) {
        diff[key] = { previous, next: value }
      }
      current[key] = value
    })
    return diff
  })
}

export function isLikelyParameterValue(value: string): boolean {
  const v = value.trim()
  if (!v || v.includes('{{')) return false
  const lower = v.toLowerCase()
  const commonStatic = new Set([
    'application/json',
    'text/plain',
    'gzip',
    'deflate',
    'br',
    'keep-alive',
    'close',
    'true',
    'false',
    'null',
  ])
  if (commonStatic.has(lower)) return false
  if (/^\d{1,2}$/.test(v)) return false
  if (v.length >= 3) return true
  return false
}