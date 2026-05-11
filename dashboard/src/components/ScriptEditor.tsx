import { useCallback, useEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useWebSocket } from '../hooks/useWebSocket'
import { WSMessage } from '../types'
import './ScriptEditor.css'

interface Action {
  name: string; type: string; method?: string; target: string
  headers?: Record<string, string>; body?: string
  extractors?: Extractor[]; assertions?: Assertion[]
  think_time_after?: boolean
}
interface Extractor { name: string; source: string; type: string; expression: string }
interface Assertion { type: string; expected?: any; value?: string; max?: number }

interface ThinkTimeConfig {
  type: string; mean_ms: number; std_ms?: number; min_ms?: number; max_ms?: number
  multiplier?: number; limit_ms?: number; ignore?: boolean
}
interface PacingConfig {
  type: string; delay_ms?: number; min_delay_ms?: number; max_delay_ms?: number
  interval_ms?: number; min_interval_ms?: number; max_interval_ms?: number; tps?: number
}
interface RunLogicConfig {
  iterations?: number; auto_transactions?: string
}
interface ErrorHandlingConfig {
  continue_on_error?: boolean; max_retries?: number; retry_delay_ms?: number
  retry_backoff?: string; snapshot_on_error?: boolean
}
interface LogConfig {
  level?: string; on_error_only?: boolean
}
interface HTTPConfig {
  connect_timeout_ms?: number; request_timeout_ms?: number; tls_timeout_ms?: number; idle_timeout_ms?: number
  version?: string; max_conns_per_host?: number; keep_alive?: boolean
  user_agent?: string; accept_encoding?: string; accept_language?: string
  max_redirects?: number
  tls?: { min_version?: string; insecure_skip_verify?: boolean; client_cert_file?: string; client_key_file?: string; ca_cert_file?: string }
  proxy?: { url?: string; username?: string; password?: string }
  auth?: { type?: string; username?: string; password?: string; token?: string }
}

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, any>
}

interface ScriptMetadata {
  import?: ImportMetadata
}

interface ScriptConfig {
  base_url: string
  think_time?: ThinkTimeConfig
  pacing?: PacingConfig
  run_logic?: RunLogicConfig
  error_handling?: ErrorHandlingConfig
  log?: LogConfig
  http?: HTTPConfig
}

interface ScriptData {
  name: string; version: string; protocol: string
  metadata?: ScriptMetadata
  config: ScriptConfig
  actions: Action[]
  data_sources?: DataSourceConfig[]
}

interface ReplaySnapshot {
  request_method?: string
  request_url?: string
  request_headers?: Record<string, string>
  request_body?: string
  response_status?: number
  response_headers?: Record<string, string>
  response_body?: string
}

interface ReplayActionEvent {
  phase: string
  action_name: string
  action_type: string
  method?: string
  target: string
  headers?: Record<string, string>
  body?: string
  started_at: string
  completed_at: string
  duration_ms: number
  status_code?: number
  success: boolean
  error_message?: string
  extractions?: Record<string, string>
  snapshot?: ReplaySnapshot
}

interface ReplayReport {
  group: string
  protocol: string
  started_at: string
  completed_at: string
  success: boolean
  error?: string
  events: ReplayActionEvent[]
  final_variables?: Record<string, string>
  snapshots?: ReplaySnapshot[]
}

interface ReplayOptionsState {
  groupName: string
  ignoreThinkTime: boolean
  variablesText: string
}

interface ReplayActivityItem {
  id: string
  kind: 'start' | 'event' | 'complete' | 'error'
  title: string
  detail?: string
  timestamp: string
  success?: boolean
  changedVariables?: string[]
}

interface ReplayWSMessage extends WSMessage {
  replay_session_id?: string
  event_index?: number
  event?: ReplayActionEvent
  report?: ReplayReport
}

interface DataSourceConfig {
  name: string
  type: string
  path: string
  inline_data?: string
  strategy: string
  delimiter?: string
  first_row_header?: boolean
  columns?: string[]
}

interface DataSourceHistory {
  past: DataSourceConfig[]
  future: DataSourceConfig[]
}

interface CandidateOccurrence {
  actionIdx: number
  field: 'target' | 'body' | 'header'
  headerKey?: string
}

interface ParameterCandidate {
  id: string
  keyHint: string
  value: string
  varName: string
  occurrences: CandidateOccurrence[]
  selected: boolean
}

interface Props {
  scriptId?: string | null
  initialScript?: ScriptData | null
  projectId?: string
  folderId?: string | null
  onSave?: () => void
  onClose?: () => void
}

const EMPTY_SCRIPT: ScriptData = {
  name: '', version: '1.0.0', protocol: 'http',
  config: {
    base_url: '',
    think_time: { type: 'fixed', mean_ms: 1000 },
    pacing: { type: 'none' },
    run_logic: { iterations: 0, auto_transactions: 'none' },
    error_handling: { continue_on_error: false },
    log: { level: 'standard' },
  },
  actions: []
}

const EMPTY_DATA_SOURCE: DataSourceConfig = {
  name: '',
  type: 'csv',
  path: '',
  strategy: 'sequential',
  delimiter: ',',
  first_row_header: true,
  columns: [],
  inline_data: '',
}

const EMPTY_ACTION: Action = {
  name: '', type: 'GET', target: '', headers: {}, body: '',
  extractors: [], assertions: [], think_time_after: true
}

function getImportSummary(metadata?: ImportMetadata | null) {
  const preserved = metadata?.preserved || {}

  return {
    runtimeFiles: Object.keys(preserved.runtime_files || {}),
    projectSections: Object.keys(preserved.project_settings || {}),
    unsupportedFunctions: Array.isArray(preserved.unsupported_functions) ? preserved.unsupported_functions : [],
  }
}

function inferDelimiter(source: DataSourceConfig): string {
  if (source.delimiter && source.delimiter.length > 0) return source.delimiter
  return source.type === 'dat' ? '\t' : ','
}

function summarizeInlineData(source: DataSourceConfig): { rows: number; columns: number } {
  const raw = source.inline_data || ''
  if (!raw.trim()) return { rows: 0, columns: 0 }
  const lines = raw.split(/\r?\n/).filter(line => line.trim().length > 0)
  if (lines.length === 0) return { rows: 0, columns: 0 }
  const delimiter = inferDelimiter(source)
  const width = lines.reduce((max, line) => Math.max(max, line.split(delimiter).length), 0)
  const dataRows = source.first_row_header === false ? lines.length : Math.max(lines.length - 1, 0)
  return { rows: dataRows, columns: width }
}

function parseInlineData(source: DataSourceConfig): { columns: string[]; rows: string[][] } {
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

function stringifyInlineData(source: DataSourceConfig, columns: string[], rows: string[][]): string {
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

function validateInlineData(source: DataSourceConfig): string[] {
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

function normalizeHeaders(columns: string[]): string[] {
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

function fillEmptyHeaders(columns: string[]): string[] {
  return columns.map((name, idx) => (name || '').trim() || `col_${idx + 1}`)
}

function dedupeHeaders(columns: string[]): string[] {
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

function downloadTextFile(content: string, filename: string) {
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

function cloneDataSource(source: DataSourceConfig): DataSourceConfig {
  return JSON.parse(JSON.stringify(source))
}

function sanitizeVarName(input: string): string {
  const normalized = (input || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_]+/g, '_')
    .replace(/^_+|_+$/g, '')
  return normalized || 'param'
}

function formatReplayTime(value?: string): string {
  if (!value) return 'n/a'
  return new Date(value).toLocaleString()
}

function formatReplayHeaders(headers?: Record<string, string>): string {
  if (!headers || Object.keys(headers).length === 0) return 'None'
  return Object.entries(headers).map(([key, value]) => `${key}: ${value}`).join('\n')
}

function createReplaySessionId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `replay-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

function upsertReplayActivity(items: ReplayActivityItem[], next: ReplayActivityItem): ReplayActivityItem[] {
  const index = items.findIndex(item => item.id === next.id)
  if (index === -1) return [...items, next]
  const updated = [...items]
  updated[index] = next
  return updated
}

function mergeReplayVariables(
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

function buildReplayVariableDiffs(
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

function isLikelyParameterValue(value: string): boolean {
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

export default function ScriptEditor({ scriptId, initialScript, projectId, folderId, onSave, onClose }: Props) {
  const api = useApi()
  const [script, setScript] = useState<ScriptData>(initialScript || { ...EMPTY_SCRIPT })
  const [activeAction, setActiveAction] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [jsonMode, setJsonMode] = useState(false)
  const [jsonText, setJsonText] = useState('')
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({})
  const [openDataEditor, setOpenDataEditor] = useState<number | null>(null)
  const [dataEditorMode, setDataEditorMode] = useState<Record<number, 'grid' | 'raw'>>({})
  const [dataSourceHistory, setDataSourceHistory] = useState<Record<number, DataSourceHistory>>({})
  const [parameterCandidates, setParameterCandidates] = useState<ParameterCandidate[]>([])
  const [replaying, setReplaying] = useState(false)
  const [replayReport, setReplayReport] = useState<ReplayReport | null>(null)
  const [expandedReplayEvent, setExpandedReplayEvent] = useState<number | null>(null)
  const [activeReplaySessionId, setActiveReplaySessionId] = useState<string | null>(null)
  const [replayActivity, setReplayActivity] = useState<ReplayActivityItem[]>([])
  const [replaySeedVariables, setReplaySeedVariables] = useState<Record<string, string>>({})
  const replayEventRefs = useRef<Record<number, HTMLDivElement | null>>({})
  const [replayOptions, setReplayOptions] = useState<ReplayOptionsState>({
    groupName: '',
    ignoreThinkTime: true,
    variablesText: '{\n  \n}',
  })
  const importMetadata = script.metadata?.import
  const importSummary = getImportSummary(importMetadata)

  const toggleSection = (id: string) => setOpenSections(prev => ({ ...prev, [id]: !prev[id] }))

  const buildAutoParameterCandidates = (current: ScriptData): ParameterCandidate[] => {
    const buckets = new Map<string, { keyHint: string; value: string; occurrences: CandidateOccurrence[] }>()
    const add = (value: string, keyHint: string, occurrence: CandidateOccurrence) => {
      const normalizedValue = value.trim()
      if (!isLikelyParameterValue(normalizedValue)) return
      const bucketKey = `${keyHint}::${normalizedValue}`
      const bucket = buckets.get(bucketKey)
      if (bucket) {
        bucket.occurrences.push(occurrence)
      } else {
        buckets.set(bucketKey, { keyHint, value: normalizedValue, occurrences: [occurrence] })
      }
    }

    current.actions.forEach((action, actionIdx) => {
      const target = action.target || ''
      if (target) {
        const queryPart = target.includes('?') ? target.split('?')[1] : ''
        if (queryPart) {
          queryPart.split('&').forEach(pair => {
            const [rawKey, rawValue] = pair.split('=')
            if (!rawValue) return
            const key = decodeURIComponent(rawKey || 'query_value')
            const value = decodeURIComponent(rawValue || '')
            add(value, key, { actionIdx, field: 'target' })
          })
        }
      }

      Object.entries(action.headers || {}).forEach(([headerKey, headerValue]) => {
        add(String(headerValue || ''), headerKey, { actionIdx, field: 'header', headerKey })
      })

      const body = action.body || ''
      if (body) {
        try {
          const parsed = JSON.parse(body)
          const walk = (node: any, path: string[]) => {
            if (node === null || node === undefined) return
            if (typeof node === 'string' || typeof node === 'number' || typeof node === 'boolean') {
              const key = path[path.length - 1] || 'body_value'
              add(String(node), key, { actionIdx, field: 'body' })
              return
            }
            if (Array.isArray(node)) {
              node.forEach((child, idx) => walk(child, [...path, `item_${idx + 1}`]))
              return
            }
            if (typeof node === 'object') {
              Object.entries(node).forEach(([k, v]) => walk(v, [...path, k]))
            }
          }
          walk(parsed, [])
        } catch {
          const quoted = Array.from(body.matchAll(/"([^"]{3,})"/g))
          quoted.forEach((match, idx) => {
            add(match[1], `body_field_${idx + 1}`, { actionIdx, field: 'body' })
          })
        }
      }
    })

    return Array.from(buckets.entries())
      .map(([id, bucket]) => ({
        id,
        keyHint: bucket.keyHint,
        value: bucket.value,
        varName: sanitizeVarName(bucket.keyHint),
        occurrences: bucket.occurrences,
        selected: bucket.occurrences.length > 0,
      }))
      .sort((a, b) => b.occurrences.length - a.occurrences.length)
  }

  useEffect(() => {
    if (scriptId && !initialScript) {
      api.getScript(scriptId).then((res: any) => {
        if (res.script) setScript(res.script)
      }).catch(() => {})
    }
  }, [scriptId])

  const handleReplaySocketMessage = useCallback((message: WSMessage) => {
    const msg = message as ReplayWSMessage
    if (!activeReplaySessionId || msg.replay_session_id !== activeReplaySessionId) return

    if (msg.type === 'replay_started' && msg.report) {
      const report = msg.report
      setReplayReport(report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'start',
        kind: 'start',
        title: 'Replay started',
        detail: report.group || 'replay',
        timestamp: report.started_at,
      }))
      setExpandedReplayEvent(null)
      return
    }

    if (msg.type === 'replay_event' && msg.event) {
      const replayEvent = msg.event
      setReplayReport(prev => {
        if (!prev) return prev
        const nextEvents = [...prev.events]
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : nextEvents.length
        nextEvents[index] = replayEvent
        return {
          ...prev,
          events: nextEvents,
          success: prev.success && replayEvent.success,
          error: prev.error || replayEvent.error_message,
        }
      })
      const replayIndex = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : 0
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: `event-${replayIndex}`,
        kind: 'event',
        title: `${replayEvent.action_name || 'Action'} ${replayEvent.success ? 'completed' : 'failed'}`,
        detail: replayEvent.success
          ? `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`
          : replayEvent.error_message || `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`,
        timestamp: replayEvent.completed_at || replayEvent.started_at,
        success: replayEvent.success,
        changedVariables: Object.keys(replayEvent.extractions || {}),
      }))
      if (typeof msg.event_index === 'number' && msg.event_index >= 0) {
        setExpandedReplayEvent(msg.event_index)
      }
      return
    }

    if (msg.type === 'replay_complete' && msg.report) {
      const report = msg.report
      setReplayReport(report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: 'complete',
        title: report.success ? 'Replay completed successfully' : 'Replay completed with failures',
        detail: report.error,
        timestamp: report.completed_at,
        success: report.success,
      }))
      setExpandedReplayEvent(report.events.length ? report.events.length - 1 : null)
      setActiveReplaySessionId(null)
    }
  }, [activeReplaySessionId])

  useWebSocket({ onMessage: handleReplaySocketMessage })

  useEffect(() => {
    if (expandedReplayEvent === null) return
    const node = replayEventRefs.current[expandedReplayEvent]
    if (!node) return
    node.scrollIntoView({
      block: 'nearest',
      behavior: activeReplaySessionId ? 'smooth' : 'auto',
    })
  }, [activeReplaySessionId, expandedReplayEvent])

  const updateField = (path: string, value: any) => {
    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev))
      const keys = path.split('.')
      let obj: any = next
      for (let i = 0; i < keys.length - 1; i++) obj = obj[keys[i]]
      obj[keys[keys.length - 1]] = value
      return next
    })
  }

  const addAction = () => {
    const a = { ...EMPTY_ACTION, name: `Action_${script.actions.length + 1}`, extractors: [], assertions: [] }
    setScript(prev => ({ ...prev, actions: [...prev.actions, a] }))
    setActiveAction(script.actions.length)
  }

  const removeAction = (idx: number) => {
    setScript(prev => ({ ...prev, actions: prev.actions.filter((_, i) => i !== idx) }))
    if (activeAction === idx) setActiveAction(null)
    else if (activeAction !== null && activeAction > idx) setActiveAction(activeAction - 1)
  }

  const moveAction = (idx: number, dir: -1 | 1) => {
    const newIdx = idx + dir
    if (newIdx < 0 || newIdx >= script.actions.length) return
    setScript(prev => {
      const actions = [...prev.actions]
      ;[actions[idx], actions[newIdx]] = [actions[newIdx], actions[idx]]
      return { ...prev, actions }
    })
    setActiveAction(newIdx)
  }

  const updateAction = (idx: number, field: string, value: any) => {
    setScript(prev => {
      const actions = [...prev.actions]
      actions[idx] = { ...actions[idx], [field]: value }
      return { ...prev, actions }
    })
  }

  const addExtractor = (actionIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = [...(actions[actionIdx].extractors || [])]
      ext.push({ name: '', source: 'body', type: 'jsonpath', expression: '' })
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const updateExtractor = (actionIdx: number, extIdx: number, field: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = [...(actions[actionIdx].extractors || [])]
      ext[extIdx] = { ...ext[extIdx], [field]: value }
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const removeExtractor = (actionIdx: number, extIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = (actions[actionIdx].extractors || []).filter((_, i) => i !== extIdx)
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const addAssertion = (actionIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = [...(actions[actionIdx].assertions || [])]
      assertions.push({ type: 'status_code', expected: 200 })
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const updateAssertion = (actionIdx: number, aIdx: number, field: string, value: any) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = [...(actions[actionIdx].assertions || [])]
      assertions[aIdx] = { ...assertions[aIdx], [field]: value }
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const removeAssertion = (actionIdx: number, aIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = (actions[actionIdx].assertions || []).filter((_, i) => i !== aIdx)
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const addHeader = (actionIdx: number) => {
    const key = prompt('Header name:')
    if (!key) return
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}), [key]: '' }
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const updateHeader = (actionIdx: number, oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}) }
      if (oldKey !== newKey) delete headers[oldKey]
      headers[newKey] = value
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const removeHeader = (actionIdx: number, key: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}) }
      delete headers[key]
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const resolveCurrentScript = () => {
    if (!jsonMode) return script
    const parsed = JSON.parse(jsonText)
    return parsed.script || parsed
  }

  const handleSave = async () => {
    setError(''); setSuccess(''); setSaving(true)
    try {
      const currentScript = resolveCurrentScript()
      if (scriptId) {
        await api.updateScript(scriptId, currentScript)
      } else {
        await api.createScript(currentScript, projectId, folderId)
      }
      if (jsonMode) setScript(currentScript)
      setSuccess('Script saved successfully!')
      onSave?.()
    } catch (e: any) {
      setError(e.message || 'Failed to save script')
    } finally { setSaving(false) }
  }

  const handleReplay = async () => {
    setError('')
    setSuccess('')
    setReplaying(true)
    try {
      const currentScript = resolveCurrentScript()
      let variables: Record<string, string> | undefined
      const trimmed = replayOptions.variablesText.trim()
      if (trimmed && trimmed !== '{}' && trimmed !== '{\n  \n}') {
        const parsed = JSON.parse(trimmed)
        if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
          throw new Error('Replay variables must be a JSON object')
        }
        variables = Object.fromEntries(Object.entries(parsed).map(([key, value]) => [key, String(value ?? '')]))
      }

      const replaySessionId = createReplaySessionId()
      const replayGroup = replayOptions.groupName.trim() || `${currentScript.name || 'script'}-replay`
      setActiveReplaySessionId(replaySessionId)
      setReplaySeedVariables(variables || {})
      setReplayActivity([{
        id: 'queued',
        kind: 'start',
        title: 'Replay requested',
        detail: replayGroup,
        timestamp: new Date().toISOString(),
      }])
      setReplayReport({
        group: replayGroup,
        protocol: currentScript.protocol,
        started_at: new Date().toISOString(),
        completed_at: '',
        success: true,
        events: [],
        final_variables: variables,
      })
      setExpandedReplayEvent(null)

      const payload = {
        group_name: replayGroup,
        replay_session_id: replaySessionId,
        ignore_think_time: replayOptions.ignoreThinkTime,
        variables,
        script: currentScript,
      }

      const res = scriptId
        ? await api.replayScript(scriptId, payload)
        : await api.replayDraftScript(payload)
      if (jsonMode) setScript(currentScript)
      setReplayReport(res.report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: 'complete',
        title: res.report?.success ? 'Replay completed successfully' : 'Replay completed with failures',
        detail: res.report?.error,
        timestamp: res.report?.completed_at || new Date().toISOString(),
        success: res.report?.success,
      }))
      setExpandedReplayEvent(res.report?.events?.length ? res.report.events.length - 1 : null)
      setActiveReplaySessionId(null)
      setSuccess(res.report?.success ? 'Replay completed successfully.' : 'Replay completed with failures.')
    } catch (e: any) {
      const issues = Array.isArray(e?.payload?.issues) ? e.payload.issues.join(' | ') : ''
      setError(issues || e.message || 'Replay failed')
      setReplayReport(null)
      setActiveReplaySessionId(null)
      setReplaySeedVariables({})
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'error',
        kind: 'error',
        title: 'Replay failed before completion',
        detail: issues || e.message || 'Replay failed',
        timestamp: new Date().toISOString(),
        success: false,
      }))
    } finally {
      setReplaying(false)
    }
  }

  const toggleJsonMode = () => {
    if (!jsonMode) {
      setJsonText(JSON.stringify({ script }, null, 2))
    } else {
      try {
        const parsed = JSON.parse(jsonText)
        setScript(parsed.script || parsed)
      } catch { setError('Invalid JSON') ; return }
    }
    setJsonMode(!jsonMode)
  }

  const methodColor = (m: string) => {
    switch (m) {
      case 'GET': return 'var(--success)'
      case 'POST': return 'var(--info)'
      case 'PUT': return 'var(--warning)'
      case 'DELETE': return 'var(--danger)'
      case 'PATCH': return 'var(--chart-6)'
      default: return 'var(--text-secondary)'
    }
  }

  const replayStatusLabel = replaying && replayReport && !replayReport.completed_at
    ? 'Running'
    : replayReport?.success
      ? 'Success'
      : 'Failed'
  const replayLatestActivity = replayActivity[replayActivity.length - 1]
  const replayVariableInspector = replayReport
    ? (replayReport.completed_at && replayReport.final_variables
        ? replayReport.final_variables
        : mergeReplayVariables(replaySeedVariables, replayReport.events))
    : null
  const replayVariableDiffs = replayReport
    ? buildReplayVariableDiffs(replaySeedVariables, replayReport.events)
    : []

  const addDataSource = () => {
    const nextIndex = (script.data_sources?.length || 0) + 1
    const nextSource: DataSourceConfig = {
      ...EMPTY_DATA_SOURCE,
      name: `source_${nextIndex}`,
    }
    setScript(prev => ({
      ...prev,
      data_sources: [...(prev.data_sources || []), nextSource],
    }))
    setDataSourceHistory({})
    setDataEditorMode({})
  }

  const removeDataSource = (idx: number) => {
    setScript(prev => ({
      ...prev,
      data_sources: (prev.data_sources || []).filter((_, i) => i !== idx),
    }))
    setDataSourceHistory({})
    setDataEditorMode({})
  }

  const mutateDataSource = (
    idx: number,
    mutator: (current: DataSourceConfig) => DataSourceConfig,
    recordHistory = true,
  ) => {
    setScript(prev => {
      const sources = [...(prev.data_sources || [])]
      const current = sources[idx] || { ...EMPTY_DATA_SOURCE }
      const next = mutator(cloneDataSource(current))
      if (recordHistory) {
        setDataSourceHistory(history => {
          const entry = history[idx] || { past: [], future: [] }
          const past = [...entry.past, cloneDataSource(current)].slice(-100)
          return { ...history, [idx]: { past, future: [] } }
        })
      }
      sources[idx] = cloneDataSource(next)
      return { ...prev, data_sources: sources }
    })
  }

  const updateDataSource = (idx: number, field: keyof DataSourceConfig, value: any, recordHistory = true) => {
    mutateDataSource(idx, current => ({ ...current, [field]: value }), recordHistory)
  }

  const undoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    if (!entry || entry.past.length === 0) return

    mutateDataSource(
      idx,
      current => {
        const previous = cloneDataSource(entry.past[entry.past.length - 1])
        setDataSourceHistory(history => {
          const active = history[idx] || { past: [], future: [] }
          const past = active.past.slice(0, -1)
          const future = [cloneDataSource(current), ...active.future].slice(0, 100)
          return { ...history, [idx]: { past, future } }
        })
        return previous
      },
      false,
    )
  }

  const redoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    if (!entry || entry.future.length === 0) return

    mutateDataSource(
      idx,
      current => {
        const [nextState, ...remainingFuture] = entry.future
        setDataSourceHistory(history => {
          const active = history[idx] || { past: [], future: [] }
          const past = [...active.past, cloneDataSource(current)].slice(-100)
          return { ...history, [idx]: { past, future: remainingFuture } }
        })
        return cloneDataSource(nextState)
      },
      false,
    )
  }

  const importDataSourceFile = async (idx: number, file: File | null) => {
    if (!file) return
    try {
      const text = await file.text()
      updateDataSource(idx, 'inline_data', text)
      setSuccess(`Imported ${file.name}`)
      setError('')
    } catch {
      setError('Failed to import parameter file')
    }
  }

  const exportDataSourceFile = (source: DataSourceConfig, idx: number) => {
    const content = source.inline_data || ''
    if (!content.trim()) {
      setError('No inline parameter data to export for this source')
      return
    }
    const base = (source.name || `source_${idx + 1}`).replace(/[^a-zA-Z0-9_-]+/g, '_')
    const ext = (source.type || 'csv').toLowerCase() === 'dat' ? 'dat' : 'csv'
    downloadTextFile(content, `${base}.${ext}`)
    setError('')
    setSuccess(`Exported ${base}.${ext}`)
  }

  const updateDataCell = (sourceIdx: number, rowIdx: number, colIdx: number, value: string) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.map(row => [...row])
    while (nextRows.length <= rowIdx) nextRows.push(Array(columns.length).fill(''))
    while (nextRows[rowIdx].length < columns.length) nextRows[rowIdx].push('')
    nextRows[rowIdx][colIdx] = value
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const updateDataColumnName = (sourceIdx: number, colIdx: number, value: string) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextColumns = [...columns]
    nextColumns[colIdx] = value || `col_${colIdx + 1}`
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, rows),
    }))
  }

  const addDataRow = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = [...rows, Array(columns.length).fill('')]
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const removeDataRow = (sourceIdx: number, rowIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.filter((_, idx) => idx !== rowIdx)
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const addDataColumn = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextColumns = [...columns, `col_${columns.length + 1}`]
    const nextRows = rows.map(row => [...row, ''])
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, nextRows),
    }))
  }

  const removeDataColumn = (sourceIdx: number, colIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    if (columns.length <= 1) return
    const nextColumns = columns.filter((_, idx) => idx !== colIdx)
    const nextRows = rows.map(row => row.filter((_, idx) => idx !== colIdx))
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, nextRows),
    }))
  }

  const autoFixDataSource = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = normalizeHeaders(parsed.columns)
    const fixedRows = parsed.rows.map(row => {
      const next = [...row]
      while (next.length < fixedColumns.length) next.push('')
      return next.slice(0, fixedColumns.length)
    })
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, fixedRows),
    }))
    setSuccess(`Applied data auto-fix to ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyFillHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = fillEmptyHeaders(parsed.columns)
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
    setSuccess(`Filled empty headers for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyDedupeHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = dedupeHeaders(parsed.columns)
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
    setSuccess(`Deduplicated headers for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyNormalizeRows = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedRows = parsed.rows.map(row => {
      const next = [...row]
      while (next.length < parsed.columns.length) next.push('')
      return next.slice(0, parsed.columns.length)
    })
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, parsed.columns, fixedRows))
    setSuccess(`Normalized row widths for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const scanForAutoParameters = () => {
    const candidates = buildAutoParameterCandidates(script)
    setParameterCandidates(candidates)
    if (candidates.length === 0) {
      setSuccess('No strong parameterization candidates were detected.')
      setError('')
      return
    }
    setSuccess(`Detected ${candidates.length} parameter candidates. Review and apply below.`)
    setError('')
  }

  const updateCandidate = (id: string, patch: Partial<ParameterCandidate>) => {
    setParameterCandidates(prev => prev.map(candidate => candidate.id === id ? { ...candidate, ...patch } : candidate))
  }

  const applyAutoParameterization = () => {
    const selected = parameterCandidates
      .filter(candidate => candidate.selected)
      .map(candidate => ({ ...candidate, varName: sanitizeVarName(candidate.varName) }))
      .filter(candidate => candidate.varName.length > 0)

    if (selected.length === 0) {
      setError('Select at least one candidate before applying auto-parameterization.')
      return
    }

    const replacementMap = new Map<string, string>()
    selected.forEach(candidate => {
      replacementMap.set(candidate.id, `{{data.auto_params.${candidate.varName}}}`)
    })

    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev)) as ScriptData
      next.actions = next.actions.map((action, actionIdx) => {
        const updated = { ...action, headers: { ...(action.headers || {}) } }
        selected.forEach(candidate => {
          candidate.occurrences
            .filter(occ => occ.actionIdx === actionIdx)
            .forEach(occ => {
              const token = replacementMap.get(candidate.id) || candidate.value
              if (occ.field === 'target' && updated.target) {
                updated.target = updated.target.split(candidate.value).join(token)
              } else if (occ.field === 'body' && typeof updated.body === 'string') {
                updated.body = updated.body.split(candidate.value).join(token)
              } else if (occ.field === 'header' && occ.headerKey && updated.headers && updated.headers[occ.headerKey]) {
                updated.headers[occ.headerKey] = updated.headers[occ.headerKey].split(candidate.value).join(token)
              }
            })
        })
        return updated
      })

      const autoSourceIdx = (next.data_sources || []).findIndex(source => source.name === 'auto_params')
      const existing = autoSourceIdx >= 0 ? next.data_sources?.[autoSourceIdx] : undefined
      const existingParsed = existing ? parseInlineData(existing) : { columns: [] as string[], rows: [] as string[][] }
      const mergedColumns = [...existingParsed.columns]
      selected.forEach(candidate => {
        if (!mergedColumns.includes(candidate.varName)) mergedColumns.push(candidate.varName)
      })

      const mergedRow: string[] = mergedColumns.map((column, idx) => {
        const selectedMatch = selected.find(candidate => candidate.varName === column)
        if (selectedMatch) return selectedMatch.value
        return existingParsed.rows[0]?.[idx] || ''
      })

      const sourcePayload: DataSourceConfig = {
        ...(existing || EMPTY_DATA_SOURCE),
        name: 'auto_params',
        type: (existing?.type || 'csv'),
        strategy: existing?.strategy || 'sequential',
        first_row_header: true,
        columns: [],
        inline_data: stringifyInlineData(
          { ...(existing || EMPTY_DATA_SOURCE), type: existing?.type || 'csv', delimiter: existing?.delimiter || ',' },
          mergedColumns,
          [mergedRow],
        ),
      }

      if (!next.data_sources) next.data_sources = []
      if (autoSourceIdx >= 0) next.data_sources[autoSourceIdx] = sourcePayload
      else next.data_sources.push(sourcePayload)
      return next
    })

    setSuccess(`Applied auto-parameterization for ${selected.length} candidate${selected.length === 1 ? '' : 's'}.`)
    setError('')
  }

  return (
    <div className="script-editor">
      <div className="editor-header">
        <div className="editor-title-row">
          <h2>{scriptId ? 'Edit Script' : 'Create Script'}</h2>
          <div className="editor-actions">
            <button className="btn btn-ghost" onClick={toggleJsonMode}>
              {jsonMode ? '◧ Form' : '{ } JSON'}
            </button>
            <button className="btn btn-ghost" onClick={handleReplay} disabled={replaying || saving || !script.name || script.actions.length === 0}>
              {replaying ? 'Replaying…' : 'Replay'}
            </button>
            <button className="btn btn-primary" onClick={handleSave} disabled={saving || !script.name}>
              {saving ? 'Saving…' : 'Save Script'}
            </button>
            {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
          </div>
        </div>
        {error && <div className="editor-toast error">{error}</div>}
        {success && <div className="editor-toast success">{success}</div>}
      </div>

      {jsonMode ? (
        <div className="json-editor">
          <textarea value={jsonText} onChange={e => setJsonText(e.target.value)} spellCheck={false} />
        </div>
      ) : (
        <div className="editor-body">
          {/* Script Config Panel */}
          <div className="config-panel">
            <h3 className="panel-title">Script Configuration</h3>
            <div className="form-grid">
              <label>
                <span>Name</span>
                <input value={script.name} onChange={e => updateField('name', e.target.value)} placeholder="My_Test_Script" />
              </label>
              <label>
                <span>Protocol</span>
                <select value={script.protocol} onChange={e => updateField('protocol', e.target.value)}>
                  <option value="http">HTTP</option>
                  <option value="https">HTTPS</option>
                </select>
              </label>
              <label>
                <span>Version</span>
                <input value={script.version} onChange={e => updateField('version', e.target.value)} placeholder="1.0.0" />
              </label>
              <label>
                <span>Base URL</span>
                <input value={script.config.base_url} onChange={e => updateField('config.base_url', e.target.value)} placeholder="https://api.example.com" />
              </label>
            </div>
            <div className="auto-parameter-toolbar">
              <button className="btn btn-sm btn-ghost" onClick={scanForAutoParameters}>Auto Parameterization</button>
              <span className="cell-muted">Scans targets, headers, and body fields for likely parameter candidates.</span>
            </div>
            {parameterCandidates.length > 0 && (
              <div className="auto-parameter-panel">
                <div className="auto-parameter-header">
                  <h4>Review Candidates</h4>
                  <button className="btn btn-sm btn-accent" onClick={applyAutoParameterization}>Apply Selected</button>
                </div>
                <div className="auto-parameter-table-wrap">
                  <table className="auto-parameter-table">
                    <thead>
                      <tr>
                        <th>Use</th>
                        <th>Field Hint</th>
                        <th>Current Value</th>
                        <th>Variable Name</th>
                        <th>Hits</th>
                      </tr>
                    </thead>
                    <tbody>
                      {parameterCandidates.map(candidate => (
                        <tr key={candidate.id}>
                          <td>
                            <input
                              type="checkbox"
                              checked={candidate.selected}
                              onChange={e => updateCandidate(candidate.id, { selected: e.target.checked })}
                            />
                          </td>
                          <td className="cell-mono">{candidate.keyHint}</td>
                          <td className="cell-mono">{candidate.value}</td>
                          <td>
                            <input
                              value={candidate.varName}
                              onChange={e => updateCandidate(candidate.id, { varName: sanitizeVarName(e.target.value) })}
                              placeholder="parameter_name"
                            />
                          </td>
                          <td>{candidate.occurrences.length}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>

          <div className="config-panel replay-panel">
            <div className="panel-title-row">
              <div>
                <h3 className="panel-title">Replay Output</h3>
                <p className="replay-panel-subtitle">Run the current draft once and inspect resolved requests, extractions, failures, and final replay variables.</p>
              </div>
              <div className="replay-panel-actions">
                <button className="btn btn-sm btn-accent" onClick={handleReplay} disabled={replaying || saving || !script.name || script.actions.length === 0}>
                  {replaying ? 'Running…' : 'Run Replay'}
                </button>
              </div>
            </div>
            <div className="replay-options-grid">
              <label>
                <span>Replay Group</span>
                <input
                  value={replayOptions.groupName}
                  onChange={e => setReplayOptions(prev => ({ ...prev, groupName: e.target.value }))}
                  placeholder={`${script.name || 'script'}-replay`}
                />
              </label>
              <label className="checkbox-label replay-checkbox">
                <input
                  type="checkbox"
                  checked={replayOptions.ignoreThinkTime}
                  onChange={e => setReplayOptions(prev => ({ ...prev, ignoreThinkTime: e.target.checked }))}
                />
                <span>Ignore think time for replay</span>
              </label>
              <label className="full-width replay-variables-field">
                <span>Replay Variables JSON</span>
                <textarea
                  value={replayOptions.variablesText}
                  onChange={e => setReplayOptions(prev => ({ ...prev, variablesText: e.target.value }))}
                  spellCheck={false}
                />
              </label>
            </div>
            {!replayReport ? (
              <div className="replay-empty-state">No replay has been run yet. Use Replay to execute the current draft once.</div>
            ) : (
              <div className="replay-report">
                <div className={`replay-live-banner ${replaying ? 'running' : replayReport.success ? 'success' : 'error'}`}>
                  <div className="replay-live-banner-main">
                    <span className="replay-live-badge">{replaying ? 'Streaming live' : 'Replay finished'}</span>
                    <strong>{replayLatestActivity?.title || 'Replay status available'}</strong>
                    <span>{replayLatestActivity?.detail || 'Inspect the timeline below for request-by-request execution details.'}</span>
                  </div>
                  <div className="replay-live-banner-meta">
                    <span>{replayReport.events.length} / {script.actions.length} actions reported</span>
                    <span>{formatReplayTime(replayLatestActivity?.timestamp || replayReport.completed_at || replayReport.started_at)}</span>
                  </div>
                </div>
                <div className="replay-summary-grid">
                  <div className="replay-summary-card">
                    <span>Status</span>
                    <strong className={replayStatusLabel === 'Failed' ? 'text-danger' : replayStatusLabel === 'Running' ? '' : 'text-success'}>{replayStatusLabel}</strong>
                  </div>
                  <div className="replay-summary-card">
                    <span>Group</span>
                    <strong>{replayReport.group || 'replay'}</strong>
                  </div>
                  <div className="replay-summary-card">
                    <span>Protocol</span>
                    <strong>{replayReport.protocol}</strong>
                  </div>
                  <div className="replay-summary-card">
                    <span>Events</span>
                    <strong>{replayReport.events.length}</strong>
                  </div>
                  <div className="replay-summary-card">
                    <span>Completed</span>
                    <strong>{formatReplayTime(replayReport.completed_at)}</strong>
                  </div>
                </div>
                {replayReport.error && <p className="replay-report-error">{replayReport.error}</p>}
                {replayActivity.length > 0 && (
                  <div className="replay-activity-card">
                    <h4>Execution Timeline</h4>
                    <div className="replay-activity-list">
                      {replayActivity.map(item => (
                        <div key={item.id} className={`replay-activity-item ${item.kind} ${item.success === false ? 'error' : item.success === true ? 'success' : ''}`}>
                          <div className="replay-activity-marker" />
                          <div className="replay-activity-content">
                            <div className="replay-activity-header-row">
                              <strong>{item.title}</strong>
                              <span>{formatReplayTime(item.timestamp)}</span>
                            </div>
                            {item.detail && <div className="replay-activity-detail">{item.detail}</div>}
                            {item.changedVariables && item.changedVariables.length > 0 && (
                              <div className="replay-variable-chip-row">
                                {item.changedVariables.map(name => (
                                  <span key={`${item.id}-${name}`} className="replay-variable-chip">{name}</span>
                                ))}
                              </div>
                            )}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
                {replayVariableInspector && (
                  <div className="replay-variables-card">
                    <h4>{replaying ? 'Live Variables' : 'Variable Inspector'}</h4>
                    {Object.keys(replayVariableInspector).length > 0 ? (
                      <pre>{JSON.stringify(replayVariableInspector, null, 2)}</pre>
                    ) : (
                      <div className="replay-variables-empty">
                        {replaying
                          ? 'No replay variables have been captured yet.'
                          : 'Replay completed without any extracted or seeded variables.'}
                      </div>
                    )}
                  </div>
                )}
                <div className="replay-events-list">
                  {replayReport.events.map((event, index) => {
                    const isOpen = expandedReplayEvent === index
                    const variableDiff = replayVariableDiffs[index] || {}
                    const changedVariables = Object.entries(variableDiff)
                    return (
                      <div
                        key={`${event.phase}-${event.action_name}-${index}`}
                        className={`replay-event-card ${event.success ? 'success' : 'error'}`}
                        ref={node => {
                          replayEventRefs.current[index] = node
                        }}
                      >
                        <button className="replay-event-header" onClick={() => setExpandedReplayEvent(isOpen ? null : index)}>
                          <div className="replay-event-header-main">
                            <span className="replay-event-phase">{event.phase}</span>
                            <strong>{event.action_name || 'Unnamed action'}</strong>
                            <span className="replay-event-target">{event.method || event.action_type} {event.target}</span>
                          </div>
                          <div className="replay-event-header-meta">
                            {changedVariables.length > 0 && (
                              <span className="replay-event-diff-count">{changedVariables.length} variable{changedVariables.length === 1 ? '' : 's'} changed</span>
                            )}
                            <span className={event.success ? 'text-success' : 'text-danger'}>{event.success ? `HTTP ${event.status_code || 'OK'}` : event.error_message || 'Failed'}</span>
                            <span>{event.duration_ms.toFixed(1)} ms</span>
                            <span>{isOpen ? '▾' : '▸'}</span>
                          </div>
                        </button>
                        {isOpen && (
                          <div className="replay-event-body">
                            <div className="replay-event-detail-grid">
                              <div>
                                <span className="replay-detail-label">Started</span>
                                <div>{formatReplayTime(event.started_at)}</div>
                              </div>
                              <div>
                                <span className="replay-detail-label">Completed</span>
                                <div>{formatReplayTime(event.completed_at)}</div>
                              </div>
                            </div>
                            <div className="replay-code-block">
                              <span className="replay-detail-label">Resolved Headers</span>
                              <pre>{formatReplayHeaders(event.headers)}</pre>
                            </div>
                            {event.body && (
                              <div className="replay-code-block">
                                <span className="replay-detail-label">Resolved Body</span>
                                <pre>{event.body}</pre>
                              </div>
                            )}
                            {event.extractions && Object.keys(event.extractions).length > 0 && (
                              <div className="replay-code-block">
                                <span className="replay-detail-label">Extractions</span>
                                <pre>{JSON.stringify(event.extractions, null, 2)}</pre>
                              </div>
                            )}
                            {changedVariables.length > 0 && (
                              <div className="replay-code-block">
                                <span className="replay-detail-label">Variable Changes</span>
                                <div className="replay-variable-diff-list">
                                  {changedVariables.map(([name, diff]) => (
                                    <div key={`${index}-${name}`} className="replay-variable-diff-item">
                                      <div className="replay-variable-diff-name">{name}</div>
                                      <div className="replay-variable-diff-values">
                                        <span className="replay-variable-diff-before">{diff.previous ?? 'unset'}</span>
                                        <span className="replay-variable-diff-arrow">{'->'}</span>
                                        <span className="replay-variable-diff-after">{diff.next}</span>
                                      </div>
                                    </div>
                                  ))}
                                </div>
                              </div>
                            )}
                            {event.snapshot && (
                              <div className="replay-code-block">
                                <span className="replay-detail-label">Snapshot</span>
                                <pre>{JSON.stringify(event.snapshot, null, 2)}</pre>
                              </div>
                            )}
                          </div>
                        )}
                      </div>
                    )
                  })}
                </div>
              </div>
            )}
          </div>

          {importMetadata && (
            <div className="import-metadata-panel">
              <div className="import-metadata-header">
                <div>
                  <h3 className="panel-title">Imported Metadata</h3>
                  <p>This script retains source-specific LoadRunner settings that Stormrunner does not edit directly.</p>
                </div>
                <span className="import-source-chip">{importMetadata.source || 'loadrunner'}</span>
              </div>

              <div className="import-metadata-grid">
                <div className="import-metadata-card">
                  <span>Source Script</span>
                  <strong>{importMetadata.source_script_name || script.name}</strong>
                </div>
                <div className="import-metadata-card">
                  <span>Project Sections</span>
                  <strong>{importSummary.projectSections.length}</strong>
                  <small>{importSummary.projectSections.join(', ') || 'None captured'}</small>
                </div>
                <div className="import-metadata-card">
                  <span>Runtime Files</span>
                  <strong>{importSummary.runtimeFiles.length}</strong>
                  <small>{importSummary.runtimeFiles.join(', ') || 'None found'}</small>
                </div>
                <div className="import-metadata-card">
                  <span>Unsupported Functions</span>
                  <strong>{importSummary.unsupportedFunctions.length}</strong>
                  <small>{importSummary.unsupportedFunctions.slice(0, 4).join(', ') || 'None'}</small>
                </div>
              </div>

              <details className="import-metadata-details">
                <summary>View preserved metadata JSON</summary>
                <pre>{JSON.stringify(importMetadata, null, 2)}</pre>
              </details>
            </div>
          )}

          {/* Runtime Settings Panels */}
          <div className="runtime-settings">
            <h3 className="settings-heading">Runtime Settings</h3>
            <div className="runtime-settings-grid">

            {/* Think Time */}
            <div className={`settings-section ${openSections['think'] ? 'open' : ''}`}>
              <button className="section-toggle" onClick={() => toggleSection('think')}>
                <span className="section-icon">{'\u23f1'}</span>
                <span className="section-label">Think Time</span>
                <span className="section-summary">
                  {script.config.think_time?.ignore ? 'Disabled' : `${script.config.think_time?.type || 'fixed'} — ${script.config.think_time?.mean_ms || 0}ms`}
                </span>
                <span className="chevron">{openSections['think'] ? '▾' : '▸'}</span>
              </button>
              {openSections['think'] && (
                <div className="section-body">
                  <div className="form-grid">
                    <label>
                      <span>Distribution</span>
                      <select value={script.config.think_time?.type || 'fixed'}
                        onChange={e => updateField('config.think_time', { ...script.config.think_time, type: e.target.value })}>
                        <option value="fixed">Fixed</option>
                        <option value="gaussian">Gaussian (Normal)</option>
                        <option value="uniform">Uniform (Random)</option>
                      </select>
                    </label>
                    <label>
                      <span>Mean (ms)</span>
                      <input type="number" value={script.config.think_time?.mean_ms || 0}
                        onChange={e => updateField('config.think_time', { ...script.config.think_time, mean_ms: Number(e.target.value) })} />
                    </label>
                    {script.config.think_time?.type === 'gaussian' && (
                      <label>
                        <span>Std Dev (ms)</span>
                        <input type="number" value={script.config.think_time?.std_ms || 0}
                          onChange={e => updateField('config.think_time', { ...script.config.think_time, std_ms: Number(e.target.value) })} />
                      </label>
                    )}
                    {(script.config.think_time?.type === 'uniform' || script.config.think_time?.type === 'gaussian') && (
                      <>
                        <label>
                          <span>Min (ms)</span>
                          <input type="number" value={script.config.think_time?.min_ms || 0}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, min_ms: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Max (ms)</span>
                          <input type="number" value={script.config.think_time?.max_ms || 0}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, max_ms: Number(e.target.value) })} />
                        </label>
                      </>
                    )}
                    <label>
                      <span>Multiplier</span>
                      <input type="number" step="0.1" value={script.config.think_time?.multiplier || 1.0}
                        onChange={e => updateField('config.think_time', { ...script.config.think_time, multiplier: Number(e.target.value) })} />
                    </label>
                    <label>
                      <span>Limit (ms)</span>
                      <input type="number" value={script.config.think_time?.limit_ms || 0} placeholder="0 = no limit"
                        onChange={e => updateField('config.think_time', { ...script.config.think_time, limit_ms: Number(e.target.value) })} />
                    </label>
                    <label className="checkbox-label">
                      <input type="checkbox" checked={script.config.think_time?.ignore || false}
                        onChange={e => updateField('config.think_time', { ...script.config.think_time, ignore: e.target.checked })} />
                      <span>Ignore all think times</span>
                    </label>
                  </div>
                </div>
              )}
            </div>

            {/* Pacing */}
            <div className={`settings-section ${openSections['pacing'] ? 'open' : ''}`}>
              <button className="section-toggle" onClick={() => toggleSection('pacing')}>
                <span className="section-icon">{'\ud83d\udd04'}</span>
                <span className="section-label">Iteration Pacing</span>
                <span className="section-summary">{script.config.pacing?.type || 'none'}</span>
                <span className="chevron">{openSections['pacing'] ? '▾' : '▸'}</span>
              </button>
              {openSections['pacing'] && (
                <div className="section-body">
                  <div className="form-grid">
                    <label className="full-width">
                      <span>Pacing Mode</span>
                      <select value={script.config.pacing?.type || 'none'}
                        onChange={e => updateField('config.pacing', { ...script.config.pacing, type: e.target.value })}>
                        <option value="none">No delay (as fast as possible)</option>
                        <option value="fixed_delay">Fixed delay after iteration</option>
                        <option value="random_delay">Random delay after iteration</option>
                        <option value="fixed_interval">Fixed interval from iteration start</option>
                        <option value="random_interval">Random interval from iteration start</option>
                      </select>
                    </label>
                    {script.config.pacing?.type === 'fixed_delay' && (
                      <label><span>Delay (ms)</span>
                        <input type="number" value={script.config.pacing?.delay_ms || 0}
                          onChange={e => updateField('config.pacing', { ...script.config.pacing, delay_ms: Number(e.target.value) })} />
                      </label>
                    )}
                    {script.config.pacing?.type === 'random_delay' && (
                      <>
                        <label><span>Min Delay (ms)</span>
                          <input type="number" value={script.config.pacing?.min_delay_ms || 0}
                            onChange={e => updateField('config.pacing', { ...script.config.pacing, min_delay_ms: Number(e.target.value) })} />
                        </label>
                        <label><span>Max Delay (ms)</span>
                          <input type="number" value={script.config.pacing?.max_delay_ms || 0}
                            onChange={e => updateField('config.pacing', { ...script.config.pacing, max_delay_ms: Number(e.target.value) })} />
                        </label>
                      </>
                    )}
                    {script.config.pacing?.type === 'fixed_interval' && (
                      <label><span>Interval (ms)</span>
                        <input type="number" value={script.config.pacing?.interval_ms || 0}
                          onChange={e => updateField('config.pacing', { ...script.config.pacing, interval_ms: Number(e.target.value) })} />
                      </label>
                    )}
                    {script.config.pacing?.type === 'random_interval' && (
                      <>
                        <label><span>Min Interval (ms)</span>
                          <input type="number" value={script.config.pacing?.min_interval_ms || 0}
                            onChange={e => updateField('config.pacing', { ...script.config.pacing, min_interval_ms: Number(e.target.value) })} />
                        </label>
                        <label><span>Max Interval (ms)</span>
                          <input type="number" value={script.config.pacing?.max_interval_ms || 0}
                            onChange={e => updateField('config.pacing', { ...script.config.pacing, max_interval_ms: Number(e.target.value) })} />
                        </label>
                      </>
                    )}
                  </div>
                </div>
              )}
            </div>

            {/* Run Logic */}
            <div className={`settings-section ${openSections['runlogic'] ? 'open' : ''}`}>
              <button className="section-toggle" onClick={() => toggleSection('runlogic')}>
                <span className="section-icon">{'\ud83d\udd01'}</span>
                <span className="section-label">Run Logic</span>
                <span className="section-summary">
                  {script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
                </span>
                <span className="chevron">{openSections['runlogic'] ? '▾' : '▸'}</span>
              </button>
              {openSections['runlogic'] && (
                <div className="section-body">
                  <div className="form-grid">
                    <label>
                      <span>Iterations</span>
                      <input type="number" value={script.config.run_logic?.iterations || 0} placeholder="0 = run until duration"
                        onChange={e => updateField('config.run_logic', { ...script.config.run_logic, iterations: Number(e.target.value) })} />
                    </label>
                    <label>
                      <span>Auto-Transactions</span>
                      <select value={script.config.run_logic?.auto_transactions || 'none'}
                        onChange={e => updateField('config.run_logic', { ...script.config.run_logic, auto_transactions: e.target.value })}>
                        <option value="none">None</option>
                        <option value="per_action">Per Action</option>
                        <option value="per_iteration">Per Iteration</option>
                      </select>
                    </label>
                  </div>
                </div>
              )}
            </div>

            {/* Error Handling */}
            <div className={`settings-section ${openSections['errors'] ? 'open' : ''}`}>
              <button className="section-toggle" onClick={() => toggleSection('errors')}>
                <span className="section-icon">{'\u26a0\ufe0f'}</span>
                <span className="section-label">Error Handling</span>
                <span className="section-summary">
                  {script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error'}
                </span>
                <span className="chevron">{openSections['errors'] ? '▾' : '▸'}</span>
              </button>
              {openSections['errors'] && (
                <div className="section-body">
                  <div className="form-grid">
                    <label className="checkbox-label">
                      <input type="checkbox" checked={script.config.error_handling?.continue_on_error || false}
                        onChange={e => updateField('config.error_handling', { ...script.config.error_handling, continue_on_error: e.target.checked })} />
                      <span>Continue on error</span>
                    </label>
                    <label className="checkbox-label">
                      <input type="checkbox" checked={script.config.error_handling?.snapshot_on_error || false}
                        onChange={e => updateField('config.error_handling', { ...script.config.error_handling, snapshot_on_error: e.target.checked })} />
                      <span>Capture snapshot on error</span>
                    </label>
                    <label>
                      <span>Max Retries</span>
                      <input type="number" value={script.config.error_handling?.max_retries || 0}
                        onChange={e => updateField('config.error_handling', { ...script.config.error_handling, max_retries: Number(e.target.value) })} />
                    </label>
                    {(script.config.error_handling?.max_retries || 0) > 0 && (
                      <>
                        <label>
                          <span>Retry Delay (ms)</span>
                          <input type="number" value={script.config.error_handling?.retry_delay_ms || 1000}
                            onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_delay_ms: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Retry Backoff</span>
                          <select value={script.config.error_handling?.retry_backoff || 'fixed'}
                            onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_backoff: e.target.value })}>
                            <option value="fixed">Fixed</option>
                            <option value="exponential">Exponential</option>
                          </select>
                        </label>
                      </>
                    )}
                  </div>
                </div>
              )}
            </div>

            {/* HTTP Protocol */}
            <div className={`settings-section ${openSections['http'] ? 'open' : ''}`}>
              <button className="section-toggle" onClick={() => toggleSection('http')}>
                <span className="section-icon">{'\ud83c\udf10'}</span>
                <span className="section-label">HTTP Protocol</span>
                <span className="section-summary">
                  {script.config.http?.proxy?.url ? 'Proxy configured' : script.config.http?.auth?.type ? `Auth: ${script.config.http.auth.type}` : 'Default'}
                </span>
                <span className="chevron">{openSections['http'] ? '▾' : '▸'}</span>
              </button>
              {openSections['http'] && (
                <div className="section-body">
                  <div className="settings-sub-group">
                    <h5>Timeouts</h5>
                    <div className="form-grid">
                      <label><span>Connect (ms)</span>
                        <input type="number" value={script.config.http?.connect_timeout_ms || 30000}
                          onChange={e => updateField('config.http', { ...script.config.http, connect_timeout_ms: Number(e.target.value) })} /></label>
                      <label><span>Request (ms)</span>
                        <input type="number" value={script.config.http?.request_timeout_ms || 60000}
                          onChange={e => updateField('config.http', { ...script.config.http, request_timeout_ms: Number(e.target.value) })} /></label>
                      <label><span>TLS Handshake (ms)</span>
                        <input type="number" value={script.config.http?.tls_timeout_ms || 10000}
                          onChange={e => updateField('config.http', { ...script.config.http, tls_timeout_ms: Number(e.target.value) })} /></label>
                      <label><span>Idle Connection (ms)</span>
                        <input type="number" value={script.config.http?.idle_timeout_ms || 90000}
                          onChange={e => updateField('config.http', { ...script.config.http, idle_timeout_ms: Number(e.target.value) })} /></label>
                    </div>
                  </div>
                  <div className="settings-sub-group">
                    <h5>Connection</h5>
                    <div className="form-grid">
                      <label><span>HTTP Version</span>
                        <select value={script.config.http?.version || 'auto'}
                          onChange={e => updateField('config.http', { ...script.config.http, version: e.target.value })}>
                          <option value="auto">Auto-negotiate</option>
                          <option value="1.1">HTTP/1.1</option>
                          <option value="2">HTTP/2</option>
                        </select></label>
                      <label><span>Max Conns/Host</span>
                        <input type="number" value={script.config.http?.max_conns_per_host || 100}
                          onChange={e => updateField('config.http', { ...script.config.http, max_conns_per_host: Number(e.target.value) })} /></label>
                      <label><span>Max Redirects</span>
                        <input type="number" value={script.config.http?.max_redirects ?? 10}
                          onChange={e => updateField('config.http', { ...script.config.http, max_redirects: Number(e.target.value) })} /></label>
                    </div>
                  </div>
                  <div className="settings-sub-group">
                    <h5>TLS / SSL</h5>
                    <div className="form-grid">
                      <label><span>Min TLS Version</span>
                        <select value={script.config.http?.tls?.min_version || '1.2'}
                          onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, min_version: e.target.value } })}>
                          <option value="1.0">TLS 1.0</option><option value="1.1">TLS 1.1</option>
                          <option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option>
                        </select></label>
                      <label className="checkbox-label">
                        <input type="checkbox" checked={script.config.http?.tls?.insecure_skip_verify || false}
                          onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, insecure_skip_verify: e.target.checked } })} />
                        <span>Skip certificate verification</span>
                      </label>
                    </div>
                  </div>
                  <div className="settings-sub-group">
                    <h5>Proxy</h5>
                    <div className="form-grid">
                      <label className="full-width"><span>Proxy URL</span>
                        <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy:8080"
                          onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, url: e.target.value } })} /></label>
                      {script.config.http?.proxy?.url && (
                        <>
                          <label><span>Proxy Username</span>
                            <input value={script.config.http?.proxy?.username || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, username: e.target.value } })} /></label>
                          <label><span>Proxy Password</span>
                            <input type="password" value={script.config.http?.proxy?.password || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, password: e.target.value } })} /></label>
                        </>
                      )}
                    </div>
                  </div>
                  <div className="settings-sub-group">
                    <h5>Authentication</h5>
                    <div className="form-grid">
                      <label><span>Auth Type</span>
                        <select value={script.config.http?.auth?.type || ''}
                          onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, type: e.target.value } })}>
                          <option value="">None</option>
                          <option value="basic">Basic</option>
                          <option value="bearer">Bearer Token</option>
                          <option value="ntlm">NTLM</option>
                        </select></label>
                      {(script.config.http?.auth?.type === 'basic' || script.config.http?.auth?.type === 'ntlm') && (
                        <>
                          <label><span>Username</span>
                            <input value={script.config.http?.auth?.username || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, username: e.target.value } })} /></label>
                          <label><span>Password</span>
                            <input type="password" value={script.config.http?.auth?.password || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, password: e.target.value } })} /></label>
                        </>
                      )}
                      {script.config.http?.auth?.type === 'bearer' && (
                        <label className="full-width"><span>Token</span>
                          <input value={script.config.http?.auth?.token || ''} placeholder="eyJhbGciOiJIUzI1NiIs..."
                            onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, token: e.target.value } })} /></label>
                      )}
                    </div>
                  </div>
                  <div className="settings-sub-group">
                    <h5>Browser Emulation</h5>
                    <div className="form-grid">
                      <label className="full-width"><span>User-Agent</span>
                        <input value={script.config.http?.user_agent || ''} placeholder="Mozilla/5.0 ..."
                          onChange={e => updateField('config.http', { ...script.config.http, user_agent: e.target.value })} /></label>
                      <label><span>Accept-Encoding</span>
                        <input value={script.config.http?.accept_encoding || ''} placeholder="gzip, deflate, br"
                          onChange={e => updateField('config.http', { ...script.config.http, accept_encoding: e.target.value })} /></label>
                      <label><span>Accept-Language</span>
                        <input value={script.config.http?.accept_language || ''} placeholder="en-US,en;q=0.9"
                          onChange={e => updateField('config.http', { ...script.config.http, accept_language: e.target.value })} /></label>
                    </div>
                  </div>
                </div>
              )}
            </div>

            {/* Logging */}
            <div className={`settings-section ${openSections['log'] ? 'open' : ''}`}>
              <button className="section-toggle" onClick={() => toggleSection('log')}>
                <span className="section-icon">{'\ud83d\udccb'}</span>
                <span className="section-label">Logging</span>
                <span className="section-summary">{script.config.log?.level || 'standard'}</span>
                <span className="chevron">{openSections['log'] ? '▾' : '▸'}</span>
              </button>
              {openSections['log'] && (
                <div className="section-body">
                  <div className="form-grid">
                    <label>
                      <span>Log Level</span>
                      <select value={script.config.log?.level || 'standard'}
                        onChange={e => updateField('config.log', { ...script.config.log, level: e.target.value })}>
                        <option value="disabled">Disabled</option>
                        <option value="standard">Standard</option>
                        <option value="extended">Extended (verbose)</option>
                      </select>
                    </label>
                    <label className="checkbox-label">
                      <input type="checkbox" checked={script.config.log?.on_error_only || false}
                        onChange={e => updateField('config.log', { ...script.config.log, on_error_only: e.target.checked })} />
                      <span>Extended log on error only</span>
                    </label>
                  </div>
                </div>
              )}
            </div>
            </div>
          </div>

          {/* Actions List + Detail */}
          <div className="actions-layout">
            <div className="actions-sidebar">
              <div className="actions-sidebar-header">
                <h3 className="panel-title">Actions ({script.actions.length})</h3>
                <button className="btn btn-sm btn-accent" onClick={addAction}>+ Add</button>
              </div>
              <div className="action-list">
                {script.actions.map((a, i) => (
                  <div key={i} className={`action-item ${activeAction === i ? 'active' : ''}`} onClick={() => setActiveAction(i)}>
                    <span className="action-method" style={{ color: methodColor(a.type) }}>{a.type}</span>
                    <span className="action-name-text">{a.name || 'Unnamed'}</span>
                    <div className="action-item-actions">
                      <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, -1) }} disabled={i === 0}>↑</button>
                      <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, 1) }} disabled={i === script.actions.length - 1}>↓</button>
                      <button className="btn-icon danger" onClick={e => { e.stopPropagation(); removeAction(i) }}>✕</button>
                    </div>
                  </div>
                ))}
                {script.actions.length === 0 && (
                  <div className="action-empty">No actions yet. Click "+ Add" to create one.</div>
                )}
              </div>
            </div>

            {/* Action Detail */}
            <div className="action-detail">
              {activeAction !== null && script.actions[activeAction] ? (
                <ActionEditor
                  action={script.actions[activeAction]}
                  index={activeAction}
                  onUpdate={updateAction}
                  onAddExtractor={addExtractor}
                  onUpdateExtractor={updateExtractor}
                  onRemoveExtractor={removeExtractor}
                  onAddAssertion={addAssertion}
                  onUpdateAssertion={updateAssertion}
                  onRemoveAssertion={removeAssertion}
                  onAddHeader={addHeader}
                  onUpdateHeader={updateHeader}
                  onRemoveHeader={removeHeader}
                  methodColor={methodColor}
                />
              ) : (
                <div className="action-placeholder">
                  <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>
                  <p>Select an action to edit its details</p>
                </div>
              )}
            </div>
          </div>

          <div className="config-panel">
            <div className="panel-title-row">
              <h3 className="panel-title">Parameter Data</h3>
              <button className="btn btn-sm btn-accent" onClick={addDataSource}>+ Add Source</button>
            </div>

            {(script.data_sources?.length || 0) === 0 ? (
              <div className="parameter-empty-state">No parameters defined. Add a data source to parameterize fields across requests.</div>
            ) : (
              <div className="parameter-table-wrap">
                <table className="parameter-table">
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Type</th>
                      <th>Strategy</th>
                      <th>Header Row</th>
                      <th>Rows</th>
                      <th>Columns</th>
                      <th>Delimiter</th>
                      <th>Data File Path</th>
                      <th style={{ textAlign: 'right' }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(script.data_sources || []).map((source, idx) => (
                      <tr key={`${source.name || 'source'}-${idx}`}>
                        <td>
                          <input
                            value={source.name || ''}
                            onChange={e => updateDataSource(idx, 'name', e.target.value)}
                            placeholder="users"
                          />
                        </td>
                        <td>
                          <select value={source.type || 'csv'} onChange={e => updateDataSource(idx, 'type', e.target.value)}>
                            <option value="csv">csv</option>
                            <option value="dat">dat</option>
                          </select>
                        </td>
                        <td>
                          <select value={source.strategy || 'sequential'} onChange={e => updateDataSource(idx, 'strategy', e.target.value)}>
                            <option value="sequential">sequential</option>
                            <option value="random">random</option>
                            <option value="unique">unique</option>
                          </select>
                        </td>
                        <td>
                          <label className="checkbox-label compact">
                            <input
                              type="checkbox"
                              checked={source.first_row_header !== false}
                              onChange={e => updateDataSource(idx, 'first_row_header', e.target.checked)}
                            />
                            <span>{source.first_row_header !== false ? 'Yes' : 'No'}</span>
                          </label>
                        </td>
                        <td className="cell-mono">{summarizeInlineData(source).rows || '—'}</td>
                        <td>
                          <input
                            value={(source.columns || []).join(', ')}
                            onChange={e => updateDataSource(
                              idx,
                              'columns',
                              e.target.value.split(',').map(v => v.trim()).filter(Boolean),
                            )}
                            placeholder="username, password"
                          />
                        </td>
                        <td>
                          <input
                            value={source.delimiter ?? (source.type === 'dat' ? '\t' : ',')}
                            onChange={e => updateDataSource(idx, 'delimiter', e.target.value)}
                            placeholder={source.type === 'dat' ? '\\t' : ','}
                          />
                        </td>
                        <td>
                          <input
                            value={source.path || ''}
                            onChange={e => updateDataSource(idx, 'path', e.target.value)}
                            placeholder="/absolute/path/to/data.csv (optional when inline data is used)"
                          />
                        </td>
                        <td style={{ textAlign: 'right' }}>
                          <div className="parameter-actions">
                            <button className="btn btn-sm btn-ghost" onClick={() => setOpenDataEditor(openDataEditor === idx ? null : idx)}>
                              {openDataEditor === idx ? 'Hide Data' : 'Edit Data'}
                            </button>
                            <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => removeDataSource(idx)}>Remove</button>
                          </div>
                        </td>
                      </tr>
                    ))}
                    {(script.data_sources || []).map((source, idx) => (
                      openDataEditor === idx ? (
                        <tr key={`${source.name || 'source'}-${idx}-editor`} className="parameter-editor-row">
                          <td colSpan={9}>
                            <div className="parameter-editor">
                              <div className="parameter-editor-header">
                                <strong>{source.name || `Source ${idx + 1}`}</strong>
                                <div className="parameter-editor-meta">
                                  <span className="cell-mono">
                                    {summarizeInlineData(source).rows} rows · {summarizeInlineData(source).columns} columns
                                  </span>
                                  <div className="parameter-editor-controls">
                                    <button className="btn btn-sm btn-ghost" disabled={!dataSourceHistory[idx]?.past?.length} onClick={() => undoDataSource(idx)}>Undo</button>
                                    <button className="btn btn-sm btn-ghost" disabled={!dataSourceHistory[idx]?.future?.length} onClick={() => redoDataSource(idx)}>Redo</button>
                                    <button
                                      className="btn btn-sm btn-ghost"
                                      onClick={() => setDataEditorMode(prev => ({ ...prev, [idx]: (prev[idx] || 'grid') === 'grid' ? 'raw' : 'grid' }))}
                                    >
                                      {(dataEditorMode[idx] || 'grid') === 'grid' ? 'Raw Mode' : 'Grid Mode'}
                                    </button>
                                    <label className="btn btn-sm btn-ghost file-btn">
                                      Import
                                      <input
                                        type="file"
                                        accept=".csv,.dat,.txt"
                                        onChange={e => {
                                          const file = e.target.files?.[0] || null
                                          importDataSourceFile(idx, file)
                                          e.currentTarget.value = ''
                                        }}
                                      />
                                    </label>
                                    <button className="btn btn-sm btn-ghost" onClick={() => exportDataSourceFile(source, idx)}>Export</button>
                                  </div>
                                </div>
                              </div>
                              {validateInlineData(source).length > 0 && (
                                <div className="parameter-validation">
                                  <div className="parameter-validation-header">
                                    <strong>Data Warnings</strong>
                                    <div className="parameter-validation-actions">
                                      <button className="btn btn-sm btn-ghost" onClick={() => applyFillHeaders(idx)}>Fill Headers</button>
                                      <button className="btn btn-sm btn-ghost" onClick={() => applyDedupeHeaders(idx)}>Deduplicate</button>
                                      <button className="btn btn-sm btn-ghost" onClick={() => applyNormalizeRows(idx)}>Normalize Rows</button>
                                      <button className="btn btn-sm btn-ghost" onClick={() => autoFixDataSource(idx)}>Auto-Fix All</button>
                                    </div>
                                  </div>
                                  {validateInlineData(source).map((warning, warningIdx) => (
                                    <div key={`${idx}-warn-${warningIdx}`} className="parameter-validation-item">{warning}</div>
                                  ))}
                                </div>
                              )}
                              {(dataEditorMode[idx] || 'grid') === 'grid' ? (
                                <div className="parameter-grid-wrap">
                                  <div className="parameter-grid-actions">
                                    <button className="btn btn-sm btn-ghost" onClick={() => addDataRow(idx)}>+ Row</button>
                                    <button className="btn btn-sm btn-ghost" onClick={() => addDataColumn(idx)}>+ Column</button>
                                  </div>
                                  <div className="parameter-grid-scroll">
                                    <table className="parameter-grid">
                                      <thead>
                                        <tr>
                                          <th>#</th>
                                          {parseInlineData(source).columns.map((column, colIdx) => (
                                            <th key={`${idx}-h-${colIdx}`}>
                                              <div className="parameter-grid-head">
                                                <input
                                                  value={column}
                                                  onChange={e => updateDataColumnName(idx, colIdx, e.target.value)}
                                                  placeholder={`col_${colIdx + 1}`}
                                                />
                                                <button className="btn-icon danger" onClick={() => removeDataColumn(idx, colIdx)} title="Remove column">✕</button>
                                              </div>
                                            </th>
                                          ))}
                                        </tr>
                                      </thead>
                                      <tbody>
                                        {parseInlineData(source).rows.map((row, rowIdx) => (
                                          <tr key={`${idx}-r-${rowIdx}`}>
                                            <td className="parameter-grid-index">
                                              {rowIdx + 1}
                                              <button className="btn-icon danger" onClick={() => removeDataRow(idx, rowIdx)} title="Remove row">✕</button>
                                            </td>
                                            {parseInlineData(source).columns.map((_, colIdx) => (
                                              <td key={`${idx}-c-${rowIdx}-${colIdx}`}>
                                                <input
                                                  value={row[colIdx] || ''}
                                                  onChange={e => updateDataCell(idx, rowIdx, colIdx, e.target.value)}
                                                />
                                              </td>
                                            ))}
                                          </tr>
                                        ))}
                                        {parseInlineData(source).rows.length === 0 && (
                                          <tr>
                                            <td colSpan={parseInlineData(source).columns.length + 1} className="parameter-grid-empty">
                                              No rows yet. Use + Row to add parameter records.
                                            </td>
                                          </tr>
                                        )}
                                      </tbody>
                                    </table>
                                  </div>
                                </div>
                              ) : (
                                <textarea
                                  value={source.inline_data || ''}
                                  onChange={e => updateDataSource(idx, 'inline_data', e.target.value, true)}
                                  placeholder={source.first_row_header !== false ? 'username,password\nalice,pass1\nbob,pass2' : 'alice,pass1\nbob,pass2'}
                                  spellCheck={false}
                                />
                              )}
                            </div>
                          </td>
                        </tr>
                      ) : null
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/* Sub-component for editing a single action's details */
function ActionEditor({ action, index, onUpdate, onAddExtractor, onUpdateExtractor, onRemoveExtractor, onAddAssertion, onUpdateAssertion, onRemoveAssertion, onAddHeader, onUpdateHeader, onRemoveHeader, methodColor }: {
  action: Action; index: number
  onUpdate: (i: number, f: string, v: any) => void
  onAddExtractor: (i: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onAddAssertion: (i: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: string, v: any) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onAddHeader: (i: number) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveHeader: (ai: number, k: string) => void
  methodColor: (m: string) => string
}) {
  return (
    <div className="action-editor-panel">
      <div className="action-editor-top">
        <span className="action-badge" style={{ background: methodColor(action.type), color: '#fff' }}>{action.type}</span>
        <h3>{action.name || 'Unnamed Action'}</h3>
      </div>

      <div className="form-grid">
        <label><span>Name</span><input value={action.name} onChange={e => onUpdate(index, 'name', e.target.value)} /></label>
        <label><span>Method</span>
          <select value={action.type} onChange={e => onUpdate(index, 'type', e.target.value)}>
            {['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS'].map(m => <option key={m}>{m}</option>)}
          </select>
        </label>
        <label className="full-width"><span>Target (path)</span><input value={action.target} onChange={e => onUpdate(index, 'target', e.target.value)} placeholder="/api/resource" /></label>
      </div>

      {/* Headers */}
      <div className="sub-section">
        <div className="sub-header"><h4>Headers</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddHeader(index)}>+ Add</button></div>
        {Object.entries(action.headers || {}).map(([k, v]) => (
          <div key={k} className="kv-row">
            <input className="kv-key" value={k} onChange={e => onUpdateHeader(index, k, e.target.value, v)} placeholder="Key" />
            <input className="kv-val" value={v} onChange={e => onUpdateHeader(index, k, k, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveHeader(index, k)}>✕</button>
          </div>
        ))}
      </div>

      {/* Body */}
      {['POST','PUT','PATCH'].includes(action.type) && (
        <div className="sub-section">
          <h4>Body</h4>
          <textarea className="body-textarea" value={action.body || ''} onChange={e => onUpdate(index, 'body', e.target.value)} placeholder='{"key": "value"}' rows={4} />
        </div>
      )}

      {/* Extractors */}
      <div className="sub-section">
        <div className="sub-header"><h4>Extractors</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddExtractor(index)}>+ Add</button></div>
        {(action.extractors || []).map((ext, ei) => (
          <div key={ei} className="extractor-row">
            <input value={ext.name} onChange={e => onUpdateExtractor(index, ei, 'name', e.target.value)} placeholder="Variable name" />
            <select value={ext.source} onChange={e => onUpdateExtractor(index, ei, 'source', e.target.value)}>
              <option value="body">Body</option><option value="header">Header</option><option value="cookie">Cookie</option>
            </select>
            <select value={ext.type} onChange={e => onUpdateExtractor(index, ei, 'type', e.target.value)}>
              <option value="jsonpath">JSONPath</option><option value="regex">Regex</option><option value="boundary">Boundary</option><option value="xpath">XPath</option>
            </select>
            <input value={ext.expression} onChange={e => onUpdateExtractor(index, ei, 'expression', e.target.value)} placeholder="$.data.id" className="ext-expr" />
            <button className="btn-icon danger" onClick={() => onRemoveExtractor(index, ei)}>✕</button>
          </div>
        ))}
      </div>

      {/* Assertions */}
      <div className="sub-section">
        <div className="sub-header"><h4>Assertions</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddAssertion(index)}>+ Add</button></div>
        {(action.assertions || []).map((a, ai) => (
          <div key={ai} className="assertion-row">
            <select value={a.type} onChange={e => onUpdateAssertion(index, ai, 'type', e.target.value)}>
              <option value="status_code">Status Code</option><option value="response_time_ms">Response Time (ms)</option>
              <option value="body_contains">Body Contains</option><option value="header_contains">Header Contains</option>
            </select>
            {a.type === 'status_code' && <input type="number" value={a.expected ?? 200} onChange={e => onUpdateAssertion(index, ai, 'expected', Number(e.target.value))} />}
            {a.type === 'response_time_ms' && <input type="number" value={a.max ?? 5000} onChange={e => onUpdateAssertion(index, ai, 'max', Number(e.target.value))} placeholder="Max ms" />}
            {(a.type === 'body_contains' || a.type === 'header_contains') && <input value={a.value ?? ''} onChange={e => onUpdateAssertion(index, ai, 'value', e.target.value)} placeholder="Expected text" />}
            <button className="btn-icon danger" onClick={() => onRemoveAssertion(index, ai)}>✕</button>
          </div>
        ))}
      </div>

      {/* Think Time */}
      <div className="sub-section">
        <label className="checkbox-label">
          <input type="checkbox" checked={action.think_time_after ?? false} onChange={e => onUpdate(index, 'think_time_after', e.target.checked)} />
          <span>Apply think time after this action</span>
        </label>
      </div>
    </div>
  )
}
