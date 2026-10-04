import { jsonLanguage } from '@codemirror/lang-json'
import type { SyntaxNode } from '@lezer/common'
import { downloadTextFile as downloadTextFileBlob } from '../utils/fileUtils'
import {
  type BrowserReplayStartRequest,
  BrowserReplayConsoleEntry,
  BrowserReplayEvent,
  BrowserReplayNetworkEntry,
} from '../types'
import {
  type ScriptActionDefinition,
  type ScriptDataSource,
  type ScriptDefinition,
  type ScriptParameterDefinition,
} from '../types/scripts'
import {
  type RecorderCorrelation,
  type SampleCorrelation,
} from '../types/scriptImports'
import type { ImportCorrelationSuggestion } from './scriptEditorImportModel'

type Action = ScriptActionDefinition
type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition
type ConsoleEntry = BrowserReplayConsoleEntry
type NetworkEntry = BrowserReplayNetworkEntry
type ReplayActionEvent = BrowserReplayEvent
export type ReplayStartPayload = BrowserReplayStartRequest & { script: ScriptDefinition }

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
  delimiter: 'auto',
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

export const EMPTY_WEBSOCKET_ACTION: Action = {
  name: '', type: 'ws_connect', target: '/ws', headers: {}, body: '',
  params: { timeout_ms: 30000 }, metadata: {}, extractors: [], assertions: [],
  think_time_after: true,
}

export const EMPTY_GRPC_ACTION: Action = {
  name: '', type: 'grpc_unary', target: 'package.Service/Method', headers: {}, body: '{}',
  params: { timeout_ms: 30000 }, metadata: {}, extractors: [], assertions: [{ type: 'grpc_status', expected: 0 }],
  think_time_after: true,
}

export const EMPTY_DATABASE_ACTION: Action = {
  name: '', type: 'db_query', target: 'select 1 as ok', headers: {}, body: '',
  params: { binds: [] }, metadata: {}, extractors: [], assertions: [{ type: 'row_count', expected: 1 }],
  think_time_after: true,
}

export const EMPTY_NETWORK_ACTION: Action = {
  name: '', type: 'network_health', target: 'localhost:80', headers: {}, body: '',
  params: {}, metadata: {}, extractors: [], assertions: [],
  think_time_after: true,
}

export const EMPTY_SOAP_ACTION: Action = {
  name: '', type: 'POST', method: 'POST', target: '', headers: {}, body: '',
  extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true,
  soap: { action: '', operation: '', body_mode: 'raw_xml' },
}

export function attachImportCorrelationSuggestions(
  script: ScriptDefinition,
  source: string,
  correlations?: Array<SampleCorrelation | RecorderCorrelation> | null,
): ScriptDefinition {
  if (!Array.isArray(correlations) || correlations.length === 0) {
    return script
  }

  const suggestions = correlations
    .map(buildImportCorrelationSuggestion)
    .filter((suggestion): suggestion is ImportCorrelationSuggestion => suggestion !== null)

  if (suggestions.length === 0) {
    return script
  }

  return {
    ...script,
    metadata: {
      ...(script.metadata || {}),
      import: {
        ...(script.metadata?.import || {}),
        source: script.metadata?.import?.source || source,
        source_script_name: script.metadata?.import?.source_script_name || script.name,
        preserved: {
          ...(script.metadata?.import?.preserved || {}),
          correlation_suggestions: suggestions,
        },
      },
    },
  }
}

function buildImportCorrelationSuggestion(correlation: SampleCorrelation | RecorderCorrelation): ImportCorrelationSuggestion | null {
  const name = normalizeString(correlation.name)
  const source = normalizeString(correlation.source)
  const expression = normalizeString(correlation.expression)
  if (!name || !source || !expression) {
    return null
  }

  if ('found_in_step' in correlation) {
    const foundAt = labelIndexedLocation('Step', correlation.found_in_step)
    if (!foundAt) {
      return null
    }
    return {
      name,
      source,
      expression,
      sample_value: normalizeOptionalString(correlation.sample_value),
      found_at: foundAt,
      used_at: normalizeIndexedLocations('Step', correlation.used_in_steps),
    }
  }

  const foundAt = labelIndexedLocation('Request', correlation.found_in_seq)
  if (!foundAt) {
    return null
  }
  return {
    name,
    source,
    expression,
    sample_value: normalizeOptionalString(correlation.sample_value),
    found_at: foundAt,
    used_at: normalizeIndexedLocations('Request', correlation.used_in_seqs),
  }
}

function normalizeIndexedLocations(prefix: string, values: unknown): string[] {
  if (!Array.isArray(values)) {
    return []
  }

  return values
    .map(value => labelIndexedLocation(prefix, value))
    .filter((value): value is string => Boolean(value))
}

function labelIndexedLocation(prefix: string, value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return ''
  }

  return `${prefix} ${Math.trunc(value)}`
}

function normalizeString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function normalizeOptionalString(value: unknown): string | undefined {
  const normalized = normalizeString(value)
  return normalized || undefined
}

export function inferDelimiter(source: DataSourceConfig): string {
  const detected = detectInlineDataDelimiter(source.inline_data || '')
  const rawDelimiter = source.delimiter || ''
  const delimiter = rawDelimiter === '\t' ? '\t' : rawDelimiter.trim()
  if (delimiter.length > 0) {
    const normalized = delimiter.toLowerCase()
    if (normalized === 'auto') return detected || (source.type === 'dat' ? '\t' : ',')
    if (delimiter === '\t' || delimiter === '\\t' || normalized === 'tab') return '\t'
    if (normalized === 'comma') return ','
    if (delimiter === ',' && detected === '\t') return '\t'
    return delimiter
  }
  return detected || (source.type === 'dat' ? '\t' : ',')
}

export function detectInlineDataDelimiter(raw: string): string | null {
  const lines = raw.split(/\r?\n/).map(line => line.trim()).filter(Boolean).slice(0, 20)
  if (lines.length === 0) return null

  const score = (delimiter: string) => lines.reduce((total, line) => {
    const count = line.split(delimiter).length - 1
    return total + (count > 0 ? count + 1 : 0)
  }, 0)

  const commaScore = score(',')
  const tabScore = score('\t')
  if (tabScore === 0 && commaScore === 0) return null
  if (tabScore > commaScore) return '\t'
  return ','
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

export function countUniqueColumnValues(
  parsed: { columns: string[]; rows: string[][] } | null | undefined,
  columnIndexes: number[],
): number {
  if (!parsed || parsed.rows.length === 0) return 0
  const indexes = columnIndexes.length > 0 ? columnIndexes : parsed.columns.map((_, idx) => idx)
  const seen = new Set<string>()
  for (const row of parsed.rows) {
    const values = indexes.map(idx => (row[idx] || '').trim())
    if (values.every(value => value.length === 0)) continue
    seen.add(values.join(''))
  }
  return seen.size
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
  return downloadTextFileBlob(content, filename)
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

export interface JsonStringSelection {
  ok: boolean
  innerFrom: number
  innerTo: number
  selectedText: string
}

type JsonSyntaxNode = { name: string; from: number; to: number; parent: JsonSyntaxNode | null }

const REJECTED_JSON_SELECTION: JsonStringSelection = { ok: false, innerFrom: 0, innerTo: 0, selectedText: '' }

export interface JsonStringOccurrence {
  id: string
  from: number
  to: number
  rawText: string
  value: string
  path: string
  line: number
  preview: string
  isOriginal: boolean
}

interface JsonStringNodeRecord {
  from: number
  to: number
  path: string
  value: string
}

/**
 * Resolve a raw text selection (offsets into `text`) to a JSON string value that can be safely
 * parameterized. Returns `ok: true` only when the selection lies inside a single JSON String value
 * (not a property key and not spanning structural tokens). The returned `innerFrom`/`innerTo` are
 * clamped to the string interior so an inserted `{{param.x}}` token stays between the quotes and the
 * JSON remains valid.
 */
export function resolveJsonStringSelection(text: string, from: number, to: number): JsonStringSelection {
  const start = Math.min(from, to)
  const end = Math.max(from, to)
  if (start === end) return REJECTED_JSON_SELECTION

  let node: JsonSyntaxNode | null
  try {
    node = jsonLanguage.parser.parse(text).resolveInner(start, 1)
  } catch {
    return REJECTED_JSON_SELECTION
  }

  // Climb to the enclosing String value. Property keys parse as `PropertyName`, never `String`,
  // so keys (and structural selections) fall through to a rejection.
  while (node && node.name !== 'String') {
    node = node.parent
  }
  if (!node) return REJECTED_JSON_SELECTION

  // The selection must stay within the string token (the surrounding quotes are allowed).
  if (start < node.from || end > node.to) return REJECTED_JSON_SELECTION

  const innerFrom = Math.max(start, node.from + 1)
  const innerTo = Math.min(end, node.to - 1)
  if (innerTo <= innerFrom) return REJECTED_JSON_SELECTION

  const selectedText = text.slice(innerFrom, innerTo)
  if (!selectedText.trim()) return REJECTED_JSON_SELECTION

  return { ok: true, innerFrom, innerTo, selectedText }
}

/**
 * Map a raw editor selection to the (quote-clamped) range and text that can be
 * parameterized, or null when the selection is not a parameterizable JSON string
 * value. Shared by the JSON pane's right-click menu and its toolbar button so
 * both entry points behave identically; callers add screen coordinates.
 */
export function buildParameterizeSelectionRequest(
  text: string,
  from: number,
  to: number,
): { start: number; end: number; selectedText: string } | null {
  const resolved = resolveJsonStringSelection(text, from, to)
  if (!resolved.ok) return null
  return { start: resolved.innerFrom, end: resolved.innerTo, selectedText: resolved.selectedText }
}

function formatJsonPathKey(key: string): string {
  return /^[a-zA-Z_$][a-zA-Z0-9_$]*$/.test(key) ? `.${key}` : `[${JSON.stringify(key)}]`
}

function collectJsonStringNodes(text: string): JsonStringNodeRecord[] {
  const tree = jsonLanguage.parser.parse(text)
  const nodes: JsonStringNodeRecord[] = []

  const jsonValueNames = new Set(['String', 'Number', 'Object', 'Array', 'True', 'False', 'Null'])
  const visit = (node: SyntaxNode, path: string) => {
    if (node.name === 'String') {
      const rawLiteral = text.slice(node.from, node.to)
      let value = rawLiteral.slice(1, -1)
      try {
        value = JSON.parse(rawLiteral) as string
      } catch {
        // The full-document JSON.parse in the caller normally makes this unreachable.
      }
      nodes.push({ from: node.from, to: node.to, path, value })
      return
    }

    if (node.name === 'Object') {
      for (let child = node.firstChild; child; child = child.nextSibling) {
        if (child.name !== 'Property') continue
        const propertyName = child.getChild('PropertyName')
        if (!propertyName) continue
        let key = text.slice(propertyName.from + 1, propertyName.to - 1)
        try {
          key = JSON.parse(text.slice(propertyName.from, propertyName.to)) as string
        } catch {
          // Keep the raw key for the same defensive reason as string values above.
        }
        for (let valueNode = child.firstChild; valueNode; valueNode = valueNode.nextSibling) {
          if (jsonValueNames.has(valueNode.name)) {
            visit(valueNode, `${path}${formatJsonPathKey(key)}`)
            break
          }
        }
      }
      return
    }

    if (node.name === 'Array') {
      let index = 0
      for (let child = node.firstChild; child; child = child.nextSibling) {
        if (!jsonValueNames.has(child.name)) continue
        visit(child, `${path}[${index}]`)
        index += 1
      }
      return
    }

    for (let child = node.firstChild; child; child = child.nextSibling) {
      if (jsonValueNames.has(child.name)) {
        visit(child, path)
      }
    }
  }

  visit(tree.topNode, '$')
  return nodes
}

function lineInfoForOffset(text: string, offset: number): { line: number; preview: string } {
  const clamped = Math.max(0, Math.min(offset, text.length))
  let line = 1
  for (let index = 0; index < clamped; index += 1) {
    if (text.charCodeAt(index) === 10) line += 1
  }
  const lineStart = text.lastIndexOf('\n', clamped - 1) + 1
  const lineEndIndex = text.indexOf('\n', clamped)
  const lineEnd = lineEndIndex === -1 ? text.length : lineEndIndex
  return {
    line,
    preview: text.slice(lineStart, lineEnd).trim(),
  }
}

function decodeJsonStringLiteral(rawText: string): string {
  try {
    return JSON.parse(`"${rawText}"`) as string
  } catch {
    return rawText
  }
}

export function findJsonStringOccurrences(
  text: string,
  selectedText: string,
  originalRange?: { from: number; to: number } | null,
): JsonStringOccurrence[] {
  const searchText = selectedText.trim()
  if (!searchText) return []

  try {
    JSON.parse(text)
  } catch {
    return []
  }

  let nodes: JsonStringNodeRecord[]
  try {
    nodes = collectJsonStringNodes(text)
  } catch {
    return []
  }

  const selectedDecoded = decodeJsonStringLiteral(selectedText)
  return nodes.flatMap(node => {
    const from = node.from + 1
    const to = node.to - 1
    if (to < from) return []
    const rawText = text.slice(from, to)
    const value = node.value
    const matches = rawText === selectedText || value === selectedDecoded
    const isOriginal = Boolean(originalRange && originalRange.from >= from && originalRange.to <= to)
    if (!matches && !isOriginal) return []
    const { line, preview } = lineInfoForOffset(text, node.from)
    return [{
      id: `${from}:${to}`,
      from,
      to,
      rawText,
      value,
      path: node.path || `$:${line}`,
      line,
      preview,
      isOriginal,
    }]
  })
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

export function extractJsonPaths(text?: string | null): string[] {
  if (!text) return []
  const matches = text.match(/\$\.[a-zA-Z0-9_.[\]]+/g)
  return matches ? Array.from(new Set(matches)) : []
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

export function createReplaySessionId(cryptoSource: Crypto | null | undefined = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined): string {
  if (typeof cryptoSource?.randomUUID === 'function') {
    return cryptoSource.randomUUID()
  }
  if (typeof cryptoSource?.getRandomValues === 'function') {
    const bytes = cryptoSource.getRandomValues(new Uint8Array(16))
    bytes[6] = (bytes[6] & 0x0f) | 0x40
    bytes[8] = (bytes[8] & 0x3f) | 0x80
    const hex = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  // Without a cryptographically secure source, let the server allocate the
  // capability instead of sending a predictable client-selected identifier.
  return ''
}

export function isDefiniteReplayStartRejection(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) return false
  const candidate = error as { status?: unknown; payload?: unknown }
  if (typeof candidate.status === 'number') {
    // A gateway/request timeout does not prove the upstream rejected the
    // idempotent start request; reconcile the client-selected session ID just
    // like a transport error or 5xx response.
    return candidate.status >= 400 && candidate.status < 500 && candidate.status !== 408
  }
  const payload = candidate.payload
  return typeof payload === 'object' && payload !== null && Array.isArray((payload as { issues?: unknown }).issues)
}

export function parseReplayVariablesText(text: string): Record<string, string> | undefined {
  const trimmed = text.trim()
  if (!trimmed || trimmed === '{}' || trimmed === '{\n  \n}') {
    return undefined
  }

  const parsed = JSON.parse(trimmed)
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
    throw new Error('Replay variables must be a JSON object')
  }

  return Object.fromEntries(Object.entries(parsed).map(([key, value]) => [key, String(value ?? '')]))
}

export function buildReplayStartPayload(
  script: ScriptDefinition,
  options: {
    projectId?: string
    groupName?: string
    replaySessionId?: string
    variables?: Record<string, string>
    ignoreThinkTime?: boolean
    startPaused?: boolean
  } = {},
): ReplayStartPayload {
  const groupName = options.groupName?.trim() || `${script.name || 'script'}-replay`
  const replayScript = options.projectId
    ? { ...script, project_id: options.projectId }
    : script

  return {
    group_name: groupName,
    project_id: options.projectId,
    replay_session_id: options.replaySessionId,
    ignore_think_time: options.ignoreThinkTime,
    start_paused: script.protocol === 'browser' ? options.startPaused : undefined,
    variables: options.variables,
    script: replayScript,
    browser: script.protocol === 'browser' ? buildBrowserReplayConfig(script) : undefined,
  }
}

function buildBrowserReplayConfig(script: ScriptDefinition): BrowserReplayStartRequest['browser'] {
  const browser = script.config.browser
  const viewport = browser?.viewport?.width || browser?.viewport?.height
    ? {
        width: browser?.viewport?.width || 1440,
        height: browser?.viewport?.height || 900,
      }
    : undefined

  return {
    engine: browser?.engine || 'chromium',
    headless: browser?.headless ?? true,
    slow_mo_ms: browser?.slow_mo_ms,
    user_agent: browser?.user_agent,
    device_profile: browser?.device_profile,
    touch_mode: browser?.touch_mode,
    orientation: browser?.orientation,
    client_metrics: browser?.client_metrics,
    capture_screenshots: normalizeBrowserCaptureScreenshots(browser?.capture_screenshots),
    viewport,
  }
}

function normalizeBrowserCaptureScreenshots(value?: string): 'off' | 'on_error' | 'per_action' {
  return value === 'off' || value === 'on_error' || value === 'per_action' ? value : 'per_action'
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

export function getErrorWarnings(error: unknown): string[] {
  if (typeof error !== 'object' || error === null || !('payload' in error)) return []
  const payload = (error as { payload?: unknown }).payload
  if (typeof payload !== 'object' || payload === null || !('warnings' in payload)) return []
  const warnings = (payload as { warnings?: unknown }).warnings
  return Array.isArray(warnings) ? warnings.filter((warning): warning is string => typeof warning === 'string') : []
}

export function mergeReplayVariables(
  seed: Record<string, string> | undefined,
  events: ReplayActionEvent[],
): Record<string, string> {
  const next: Record<string, string> = Object.assign(Object.create(null), seed || {})
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
