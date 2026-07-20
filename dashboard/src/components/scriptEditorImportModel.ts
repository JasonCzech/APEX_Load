import type {
  ScriptActionDefinition,
  ScriptDataSource,
  ScriptDefinition,
  ScriptImportMetadata,
  ScriptParameterDefinition,
  ScriptValidationResponse,
  SourceArchiveReview,
  SourceArchiveReviewMapping,
} from '../types/scripts'
import { isRecord } from './scriptActionHelpers'

export type ImportedRuntimeSectionId =
  | 'think'
  | 'pacing'
  | 'runlogic'
  | 'errors'
  | 'http'
  | 'web_runtime'
  | 'soap'
  | 'java_http'
  | 'jms'
  | 'websocket'
  | 'grpc'
  | 'database'
  | 'network'
  | 'log'

export interface ImportCorrelationSuggestion {
  name: string
  source: string
  expression: string
  sample_value?: string
  found_at: string
  used_at: string[]
}

export interface ImportRuntimeSettingParity {
  id: string
  area: string
  concept: string
  status: string
  runtime_files: string[]
}

export interface ImportRuntimeSettingReadiness {
  score_pct: number
  blocked: boolean
  summary: string
  blocking_statuses: string[]
  blocking_settings: ImportRuntimeSettingParity[]
}

export interface ImportParameterFileSection {
  file: string
  name: string
  values: Record<string, string>
}

export interface ImportSummary {
  runtimeFiles: string[]
  parameterFiles: string[]
  parameterFileSections: ImportParameterFileSection[]
  projectSections: string[]
  unsupportedFunctions: string[]
  correlationSuggestions: ImportCorrelationSuggestion[]
  runtimeSettingsParity: ImportRuntimeSettingParity[]
  runtimeSettingStatusCounts: Record<string, number>
  runtimeSettingReadiness: ImportRuntimeSettingReadiness | null
}

export interface ImportedParameterFileMapping {
  section: ImportParameterFileSection
  sourceName: string | null
}

export interface ImportedCorrelationLocation {
  location: string
  actionIndex: number | null
  label: string
}

export interface ImportedCorrelationFocusTarget {
  correlation: ImportCorrelationSuggestion
  foundAtActionIndex: number | null
  foundAtLabel: string
  usedAtLocations: ImportedCorrelationLocation[]
  usedAtTargets: Array<ImportedCorrelationLocation & { actionIndex: number }>
}

export interface ImportedDraftDiffSummary {
  changed: boolean
  configChanged: boolean
  actionChanges: number
  parameterChanges: number
  dataSourceChanges: number
  details: string[]
}

export interface Web2SelectorCandidate {
  kind: string
  value: string
  confidence: number
  reason: string
}

export interface Web2ObjectDescriptorRow {
  key: string
  label: string
  descriptorId: string
  status: string
  actionIndex: number | null
  currentSelector: string
  frameContext: string
  windowContext: string
  replayDiagnostics: string[]
  candidates: Web2SelectorCandidate[]
}

interface Web2ObjectDescriptorReview {
  rows: Web2ObjectDescriptorRow[]
  repositoryCount: number
  candidateCount: number
  unsupportedCount: number
}

interface TrueClientPromotionReview {
  status: string
  generatedAt: string
  source: string
  sourceScriptName: string
  compatibility: {
    sourceProfile: string
    importMode: string
    promotionStatus: string
    promotionAllowed: boolean
    runtimeParityStatus: string
    replayStatus: string
    javascriptReviewGate: string
    httpConversionReadiness: string
    selectorConfidencePct: number
    frameWindowBlockers: number
  }
  gates: {
    reviewRequiredActions: number
    javascriptReviewActions: number
    selectorFallbackCandidates: number
    replayRequired: boolean
    replayWarnings: string[]
    importWarnings: string[]
  }
  replayEvidence: {
    sessionId: string
    status: string
    startedAt: string
    completedAt: string
    events: number
    passed: number
    failed: number
    screenshots: number
    domHtmlCaptures: number
    consoleEntries: number
    networkEntries: number
    sourceSteps: number
    sourceTransactions: number
  }
  nextActions: string[]
}

function readPreservedImportObject(
  metadata?: ScriptImportMetadata | null,
  key?: string,
): Record<string, unknown> | null {
  if (!metadata?.preserved || !key) return null
  const value = metadata.preserved[key]
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

export function readSourceReview(metadata?: ScriptImportMetadata | null): SourceArchiveReview | null {
  const value = readPreservedImportObject(metadata, 'source_review')
  if (!value) return null
  if (!Array.isArray(value.source_files) && !Array.isArray(value.mappings)) return null
  return value as SourceArchiveReview
}

function normalizeMappingText(value: unknown): string {
  return String(value || '').trim().toLowerCase()
}

function mappingTargetsAction(mapping: SourceArchiveReviewMapping, actionIndex: number): boolean {
  const targetPath = mapping.target_path || ''
  const actionPath = `actions[${actionIndex}]`
  return targetPath === actionPath
    || targetPath.startsWith(`${actionPath}.`)
    || targetPath.startsWith(`${actionPath}[`)
}

function sourceMappingActionScore(
  mapping: SourceArchiveReviewMapping,
  action: ScriptActionDefinition | undefined,
  actionIndex: number,
): number {
  const targetPath = mapping.target_path || ''
  const actionPath = `actions[${actionIndex}]`
  let score = mapping.start_line || 0
  if (targetPath === actionPath) score -= 10000
  else if (targetPath.startsWith(`${actionPath}.params.requests[`)) score -= 9000
  else if (mappingTargetsAction(mapping, actionIndex)) score -= 8000
  if (mapping.status === 'converted') score -= 1000
  if (mapping.target_kind === 'action') score -= 500
  if (mapping.support_mode === 'executable') score -= 100
  const targetName = normalizeMappingText(mapping.target_name)
  const actionName = normalizeMappingText(action?.name)
  const sourceTransaction = normalizeMappingText(
    action?.metadata?.source_transaction
      || action?.metadata?.transaction_name
      || action?.metadata?.transaction,
  )
  if (actionName && targetName === actionName) score -= 250
  if (sourceTransaction && targetName === sourceTransaction) score -= 250
  return score
}

export function findSourceMappingForAction(
  sourceReview: SourceArchiveReview | null,
  action: ScriptActionDefinition | undefined,
  actionIndex: number,
): SourceArchiveReviewMapping | null {
  const mappings = (sourceReview?.mappings || []).filter(mapping => mapping && mapping.id)
  if (mappings.length === 0 || !action || actionIndex < 0) return null
  const actionName = normalizeMappingText(action.name)
  const sourceTransaction = normalizeMappingText(
    action.metadata?.source_transaction
      || action.metadata?.transaction_name
      || action.metadata?.transaction,
  )
  const matches = mappings.filter(mapping => {
    if (mappingTargetsAction(mapping, actionIndex)) return true
    const targetName = normalizeMappingText(mapping.target_name)
    return Boolean(targetName && (targetName === actionName || targetName === sourceTransaction))
  })
  if (matches.length === 0) return null
  return [...matches].sort((left, right) => (
    sourceMappingActionScore(left, action, actionIndex)
      - sourceMappingActionScore(right, action, actionIndex)
  ))[0] || null
}

function readString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function readNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function readBoolean(value: unknown): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value !== 'string') return false
  const normalized = value.trim().toLowerCase()
  return normalized === 'true' || normalized === 'yes' || normalized === '1'
}

function readArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function readStringArray(value: unknown): string[] {
  return readArray(value).map(readString).filter(Boolean)
}

function readStringRecord(value: unknown): Record<string, string> {
  if (!isRecord(value)) return {}
  const normalized: Record<string, string> = {}
  Object.entries(value).forEach(([key, rawValue]) => {
    const normalizedKey = readString(key)
    const normalizedValue = readString(rawValue)
    if (normalizedKey && normalizedValue) normalized[normalizedKey] = normalizedValue
  })
  return normalized
}

function normalizeImportCorrelationSuggestion(value: unknown): ImportCorrelationSuggestion | null {
  if (!isRecord(value)) return null
  const name = readString(value.name)
  const source = readString(value.source)
  const expression = readString(value.expression)
  const foundAt = readString(value.found_at)
  if (!name || !source || !expression || !foundAt) return null
  const sampleValue = readString(value.sample_value)
  return {
    name,
    source,
    expression,
    ...(sampleValue ? { sample_value: sampleValue } : {}),
    found_at: foundAt,
    used_at: readStringArray(value.used_at),
  }
}

function normalizeImportRuntimeSettingParity(value: unknown): ImportRuntimeSettingParity | null {
  if (!isRecord(value)) return null
  const id = readString(value.id)
  const area = readString(value.area)
  const concept = readString(value.concept)
  const status = readString(value.status)
  if (!id || !area || !concept || !status) return null
  return { id, area, concept, status, runtime_files: readStringArray(value.runtime_files) }
}

function normalizeImportRuntimeSettingReadiness(value: unknown): ImportRuntimeSettingReadiness | null {
  if (!isRecord(value)) return null
  return {
    score_pct: readNumber(value.score_pct),
    blocked: value.blocked === true,
    summary: readString(value.summary),
    blocking_statuses: readStringArray(value.blocking_statuses),
    blocking_settings: readArray(value.blocking_settings)
      .map(normalizeImportRuntimeSettingParity)
      .filter((entry): entry is ImportRuntimeSettingParity => entry !== null),
  }
}

function normalizeImportParameterFileSections(value: unknown): ImportParameterFileSection[] {
  if (!isRecord(value)) return []
  const sections: ImportParameterFileSection[] = []
  Object.entries(value)
    .sort(([left], [right]) => left.localeCompare(right))
    .forEach(([fileName, rawFileSections]) => {
      if (!isRecord(rawFileSections)) return
      Object.entries(rawFileSections)
        .sort(([left], [right]) => left.localeCompare(right))
        .forEach(([sectionName, rawSectionValues]) => {
          const values = readStringRecord(rawSectionValues)
          if (!sectionName.trim() && Object.keys(values).length === 0) return
          sections.push({ file: fileName, name: sectionName, values })
        })
    })
  return sections
}

function normalizeRuntimeSettingStatusCounts(value: unknown): Record<string, number> {
  if (!isRecord(value)) return {}
  const counts: Record<string, number> = {}
  Object.entries(value).forEach(([key, rawCount]) => {
    if (typeof rawCount === 'number' && Number.isFinite(rawCount)) counts[key] = rawCount
  })
  return counts
}

export function getImportSummary(metadata?: ScriptImportMetadata | null): ImportSummary {
  const preserved = metadata?.preserved || {}
  return {
    runtimeFiles: Object.keys(isRecord(preserved.runtime_files) ? preserved.runtime_files : {}),
    parameterFiles: readStringArray(preserved.parameter_files),
    parameterFileSections: normalizeImportParameterFileSections(preserved.parameter_file_sections),
    projectSections: Object.keys(isRecord(preserved.project_settings) ? preserved.project_settings : {}),
    unsupportedFunctions: readStringArray(preserved.unsupported_functions),
    correlationSuggestions: readArray(preserved.correlation_suggestions)
      .map(normalizeImportCorrelationSuggestion)
      .filter((suggestion): suggestion is ImportCorrelationSuggestion => suggestion !== null),
    runtimeSettingsParity: readArray(preserved.runtime_settings_parity)
      .map(normalizeImportRuntimeSettingParity)
      .filter((entry): entry is ImportRuntimeSettingParity => entry !== null),
    runtimeSettingStatusCounts: normalizeRuntimeSettingStatusCounts(preserved.runtime_settings_status_counts),
    runtimeSettingReadiness: normalizeImportRuntimeSettingReadiness(preserved.runtime_settings_migration_readiness),
  }
}

function readCount(value: unknown): number {
  return Math.max(0, Math.trunc(readNumber(value)))
}

function readPromotionRecord(parent: Record<string, unknown>, key: string): Record<string, unknown> {
  const value = parent[key]
  return isRecord(value) ? value : {}
}

export function readTrueClientPromotionReview(
  metadata?: ScriptImportMetadata | null,
): TrueClientPromotionReview | null {
  const raw = readPreservedImportObject(metadata, 'trueclient2_promotion_review')
  if (!raw) return null

  const compatibility = readPromotionRecord(raw, 'compatibility')
  const gates = readPromotionRecord(raw, 'gates')
  const replayEvidence = readPromotionRecord(raw, 'replay_evidence')
  const sourceScriptName = readString(raw.source_script_name) || metadata?.source_script_name || ''

  return {
    status: readString(raw.status) || 'review_required',
    generatedAt: readString(raw.generated_at),
    source: readString(raw.source) || metadata?.source || 'enterprise_script_archive',
    sourceScriptName,
    compatibility: {
      sourceProfile: readString(compatibility.source_profile),
      importMode: readString(compatibility.import_mode),
      promotionStatus: readString(compatibility.promotion_status),
      promotionAllowed: readBoolean(compatibility.promotion_allowed),
      runtimeParityStatus: readString(compatibility.runtime_parity_status),
      replayStatus: readString(compatibility.replay_status),
      javascriptReviewGate: readString(compatibility.javascript_review_gate),
      httpConversionReadiness: readString(compatibility.http_conversion_readiness),
      selectorConfidencePct: readCount(compatibility.selector_confidence_pct),
      frameWindowBlockers: readCount(compatibility.frame_window_blockers),
    },
    gates: {
      reviewRequiredActions: readCount(gates.review_required_actions),
      javascriptReviewActions: readCount(gates.javascript_review_actions),
      selectorFallbackCandidates: readCount(gates.selector_fallback_candidates),
      replayRequired: readBoolean(gates.replay_required),
      replayWarnings: readStringArray(gates.replay_warnings),
      importWarnings: readStringArray(gates.import_warnings),
    },
    replayEvidence: {
      sessionId: readString(replayEvidence.session_id),
      status: readString(replayEvidence.status) || 'not_run',
      startedAt: readString(replayEvidence.started_at),
      completedAt: readString(replayEvidence.completed_at),
      events: readCount(replayEvidence.events),
      passed: readCount(replayEvidence.passed),
      failed: readCount(replayEvidence.failed),
      screenshots: readCount(replayEvidence.screenshots),
      domHtmlCaptures: readCount(replayEvidence.dom_html_captures),
      consoleEntries: readCount(replayEvidence.console_entries),
      networkEntries: readCount(replayEvidence.network_entries),
      sourceSteps: readCount(replayEvidence.source_steps),
      sourceTransactions: readCount(replayEvidence.source_transactions),
    },
    nextActions: readStringArray(raw.next_actions),
  }
}

export function formatTrueClientPromotionStatus(status: string): string {
  switch (status) {
    case 'replay_evidence_attached': return 'Replay evidence attached'
    case 'needs_replay_evidence': return 'Needs replay evidence'
    case 'promotion_ready': return 'Promotion ready'
    case 'promotion_blocked': return 'Promotion blocked'
    default: return status.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
  }
}

export function formatPromotionCount(count: number, singular: string, plural = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : plural}`
}

function normalizeWeb2SelectorCandidate(value: unknown): Web2SelectorCandidate | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const item = value as Record<string, unknown>
  const candidate = {
    kind: readString(item.kind) || 'selector',
    value: readString(item.value),
    confidence: readNumber(item.confidence),
    reason: readString(item.reason),
  }
  return candidate.value ? candidate : null
}

export function buildWeb2ObjectDescriptorReview(
  script: ScriptDefinition,
  metadata?: ScriptImportMetadata | null,
): Web2ObjectDescriptorReview {
  const inspection = readPreservedImportObject(metadata, 'trueclient_archive_inspection')
  const steps = readArray(inspection?.steps)
  const objectRepositories = readArray(inspection?.object_repositories)
  const unsupportedFragments = readArray(inspection?.unsupported_fragments)
  const rows = steps
    .map((step, index) => {
      if (!step || typeof step !== 'object' || Array.isArray(step)) return null
      const item = step as Record<string, unknown>
      const candidates = readArray(item.selector_candidates)
        .map(normalizeWeb2SelectorCandidate)
        .filter((candidate): candidate is Web2SelectorCandidate => candidate !== null)
      const actionIndex = index < script.actions.length && script.actions[index]?.type.startsWith('browser_')
        ? index
        : null
      const currentSelector = actionIndex !== null
        ? script.actions[actionIndex]?.selector || script.actions[actionIndex]?.target || ''
        : ''
      const diagnostics = readArray(item.replay_diagnostics)
        .map((diagnostic) => {
          if (!diagnostic || typeof diagnostic !== 'object' || Array.isArray(diagnostic)) return ''
          const detail = diagnostic as Record<string, unknown>
          return readString(detail.summary) || readString(detail.code)
        })
        .filter(Boolean)
      return {
        key: `${readString(item.step_id) || index}:${readString(item.index) || index}`,
        label: readString(item.action_name) || readString(item.action) || `Step ${index + 1}`,
        descriptorId: readString(item.object_descriptor_id) || 'No descriptor ID',
        status: readString(item.status) || 'review',
        actionIndex,
        currentSelector,
        frameContext: readString(item.frame_context),
        windowContext: readString(item.window_context),
        replayDiagnostics: diagnostics,
        candidates,
      }
    })
    .filter((row): row is Web2ObjectDescriptorRow => (
      row !== null && (row.candidates.length > 0 || row.descriptorId !== 'No descriptor ID')
    ))

  return {
    rows,
    repositoryCount: objectRepositories.length,
    candidateCount: rows.reduce((sum, row) => sum + row.candidates.length, 0),
    unsupportedCount: unsupportedFragments.length,
  }
}

export function findActionIndexByName(
  actions: ScriptActionDefinition[],
  targetName?: string | null,
): number | null {
  const normalizedTarget = targetName?.trim()
  if (!normalizedTarget) return null
  const exactMatch = actions.findIndex(action => action.name?.trim() === normalizedTarget)
  if (exactMatch >= 0) return exactMatch
  const normalizedLower = normalizedTarget.toLowerCase()
  const caseInsensitiveMatch = actions.findIndex(action => action.name?.trim().toLowerCase() === normalizedLower)
  return caseInsensitiveMatch >= 0 ? caseInsensitiveMatch : null
}

export function findActionIndexByImportedLocation(
  actions: ScriptActionDefinition[],
  location?: string | null,
): number | null {
  const normalizedLocation = location?.trim()
  if (!normalizedLocation) return null
  const match = normalizedLocation.match(/^(?:Step|Request)\s+(\d+)$/i)
  if (!match) return null
  const actionIndex = Number.parseInt(match[1], 10) - 1
  return Number.isInteger(actionIndex) && actionIndex >= 0 && actionIndex < actions.length
    ? actionIndex
    : null
}

export function formatImportedLocationLabel(
  actions: ScriptActionDefinition[],
  location?: string | null,
  actionIndex?: number | null,
): string {
  const normalizedLocation = location?.trim()
  if (!normalizedLocation) return 'Unknown action'
  if (actionIndex == null || actionIndex < 0 || actionIndex >= actions.length) return normalizedLocation
  const actionName = actions[actionIndex]?.name?.trim()
  return actionName ? `${normalizedLocation}: ${actionName}` : normalizedLocation
}

export function findDataSourceIndexByName(
  dataSources: ScriptDataSource[] | undefined,
  targetName?: string | null,
): number | null {
  const normalizedTarget = targetName?.trim()
  if (!normalizedTarget) return null
  const exactMatch = (dataSources || []).findIndex(source => source.name?.trim() === normalizedTarget)
  if (exactMatch >= 0) return exactMatch
  const normalizedLower = normalizedTarget.toLowerCase()
  const caseInsensitiveMatch = (dataSources || [])
    .findIndex(source => source.name?.trim().toLowerCase() === normalizedLower)
  return caseInsensitiveMatch >= 0 ? caseInsensitiveMatch : null
}

export function normalizeParameterDataLookup(value?: string | null): string {
  return (value || '').trim().toLowerCase()
}

export function findFirstParameterIndexByDataSource(
  parameters: ScriptParameterDefinition[] | undefined,
  sourceName?: string | null,
): number | null {
  const normalizedSourceName = normalizeParameterDataLookup(sourceName)
  if (!normalizedSourceName) return null
  const parameterIndex = (parameters || []).findIndex(parameter => (
    normalizeParameterDataLookup(parameter.data_source) === normalizedSourceName
  ))
  return parameterIndex >= 0 ? parameterIndex : null
}

function getImportedParameterMappingValue(values: Record<string, string>, keys: string[]): string {
  const targetKeys = new Set(keys.map(key => key.toLowerCase()))
  const match = Object.entries(values).find(([key]) => targetKeys.has(key.trim().toLowerCase()))
  return match?.[1]?.trim() || ''
}

function sanitizeImportedParameterDataSourceName(dataFile?: string | null): string {
  const normalizedFile = dataFile?.trim()
  if (!normalizedFile) return 'parameter_data'
  const baseName = normalizedFile.split(/[\\/]/).pop() || normalizedFile
  const withoutExtension = baseName.replace(/\.[^.]+$/, '')
  const normalized = withoutExtension
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
  return normalized || 'parameter_data'
}

export function findImportedParameterMappingDataSourceIndex(
  dataSources: ScriptDataSource[] | undefined,
  values: Record<string, string>,
): number | null {
  const dataFile = getImportedParameterMappingValue(
    values,
    ['table', 'filename', 'file', 'datafile', 'sourcefile'],
  )
  if (!dataFile) return null
  return findDataSourceIndexByName(dataSources, sanitizeImportedParameterDataSourceName(dataFile))
}

export function getImportedRuntimeSectionId(area?: string | null): ImportedRuntimeSectionId | null {
  switch (area?.trim().toLowerCase()) {
    case 'think time': return 'think'
    case 'pacing': return 'pacing'
    case 'run logic': return 'runlogic'
    case 'error handling': return 'errors'
    case 'logging': return 'log'
    case 'http': return 'http'
    case 'web http/html':
    case 'web runtime': return 'web_runtime'
    case 'soap':
    case 'soap settings': return 'soap'
    case 'java http':
    case 'java over http': return 'java_http'
    case 'jms': return 'jms'
    case 'websocket': return 'websocket'
    case 'grpc': return 'grpc'
    case 'database':
    case 'database runtime': return 'database'
    case 'network':
    case 'network runtime': return 'network'
    default: return null
  }
}

export function formatRuntimeSettingStatusLabel(status: string): string {
  switch (status) {
    case 'executed': return 'Executed'
    case 'partial': return 'Partial'
    case 'compatibility_only': return 'Compatibility-only'
    case 'blocked': return 'Blocked'
    case 'non_applicable': return 'Non-applicable'
    default: return status.replace(/_/g, ' ')
  }
}

export function formatImportParameterMapping(values: Record<string, string>): string {
  const details: string[] = []
  if (values.Type) details.push(values.Type)
  if (values.ColumnName) details.push(`column ${values.ColumnName}`)
  if (values.Table) details.push(`table ${values.Table}`)
  if (values.SelectNextRow) details.push(values.SelectNextRow)
  if (values.UpdateValueOn) details.push(values.UpdateValueOn)
  return details.join(' • ') || 'No preserved mapping details'
}

export function buildImportedParameterFileMappings(
  script: ScriptDefinition,
  summary: ImportSummary,
): ImportedParameterFileMapping[] {
  return summary.parameterFileSections.map((section) => {
    const sourceIndex = findImportedParameterMappingDataSourceIndex(script.data_sources, section.values)
    return {
      section,
      sourceName: sourceIndex !== null ? script.data_sources?.[sourceIndex]?.name || null : null,
    }
  })
}

export function buildImportedCorrelationFocusTargets(
  actions: ScriptActionDefinition[],
  correlations: ImportCorrelationSuggestion[],
): ImportedCorrelationFocusTarget[] {
  return correlations.map((correlation) => {
    const foundAtActionIndex = findActionIndexByImportedLocation(actions, correlation.found_at)
    const usedAtLocations = correlation.used_at.map((location) => {
      const actionIndex = findActionIndexByImportedLocation(actions, location)
      return {
        location,
        actionIndex,
        label: formatImportedLocationLabel(actions, location, actionIndex),
      }
    })
    return {
      correlation,
      foundAtActionIndex,
      foundAtLabel: formatImportedLocationLabel(actions, correlation.found_at, foundAtActionIndex),
      usedAtLocations,
      usedAtTargets: usedAtLocations.filter(
        (target): target is ImportedCorrelationLocation & { actionIndex: number } => target.actionIndex !== null,
      ),
    }
  })
}

function compareConfig(left: ScriptDefinition['config'], right: ScriptDefinition['config']): boolean {
  return JSON.stringify(left || {}) !== JSON.stringify(right || {})
}

function compareNamedList(left: Array<{ name?: string | null }>, right: Array<{ name?: string | null }>): number {
  const normalizeName = (value: string) => value.trim().toLowerCase()
  const leftNames = new Set(left.map(item => normalizeName(item.name || '')).filter(Boolean))
  const rightNames = new Set(right.map(item => normalizeName(item.name || '')).filter(Boolean))
  let delta = 0
  leftNames.forEach(name => { if (!rightNames.has(name)) delta += 1 })
  rightNames.forEach(name => { if (!leftNames.has(name)) delta += 1 })
  return delta
}

export function buildImportedDraftDiffSummary(
  baseline: ScriptDefinition | null | undefined,
  current: ScriptDefinition,
): ImportedDraftDiffSummary {
  if (!baseline) {
    return {
      changed: false,
      configChanged: false,
      actionChanges: 0,
      parameterChanges: 0,
      dataSourceChanges: 0,
      details: [],
    }
  }

  const details: string[] = []
  const configChanged = compareConfig(baseline.config, current.config)
  if (configChanged) details.push('Runtime or protocol configuration has changed.')

  let actionChanges = Math.abs(baseline.actions.length - current.actions.length)
  const renamedActions: string[] = []
  const changedActionTargets: string[] = []
  const comparedActionCount = Math.min(baseline.actions.length, current.actions.length)
  for (let index = 0; index < comparedActionCount; index += 1) {
    const before = baseline.actions[index]
    const after = current.actions[index]
    if (before.name !== after.name) {
      actionChanges += 1
      renamedActions.push(`${before.name || `Action ${index + 1}`} -> ${after.name || `Action ${index + 1}`}`)
    } else if (
      before.type !== after.type
      || before.target !== after.target
      || before.body !== after.body
      || JSON.stringify(before.headers || {}) !== JSON.stringify(after.headers || {})
    ) {
      actionChanges += 1
      changedActionTargets.push(after.name || `Action ${index + 1}`)
    }
  }

  if (renamedActions.length > 0) {
    details.push(`Renamed ${renamedActions.length} action${renamedActions.length === 1 ? '' : 's'}: ${renamedActions.slice(0, 3).join(', ')}${renamedActions.length > 3 ? ', ...' : ''}`)
  }
  if (changedActionTargets.length > 0) {
    details.push(`Updated ${changedActionTargets.length} action target or body definition${changedActionTargets.length === 1 ? '' : 's'}.`)
  }
  if (baseline.actions.length !== current.actions.length) {
    details.push(`Action count changed from ${baseline.actions.length} to ${current.actions.length}.`)
  }

  const parameterChanges = compareNamedList(baseline.parameters || [], current.parameters || [])
  if (parameterChanges > 0) {
    details.push(`Parameter definitions changed across ${parameterChanges} named entry${parameterChanges === 1 ? '' : 'ies'}.`)
  }
  const dataSourceChanges = compareNamedList(baseline.data_sources || [], current.data_sources || [])
  if (dataSourceChanges > 0) {
    details.push(`Data sources changed across ${dataSourceChanges} named entry${dataSourceChanges === 1 ? '' : 'ies'}.`)
  }

  return {
    changed: configChanged || actionChanges > 0 || parameterChanges > 0 || dataSourceChanges > 0,
    configChanged,
    actionChanges,
    parameterChanges,
    dataSourceChanges,
    details,
  }
}

export function applyValidationRuntimeSettingsMetadata(
  script: ScriptDefinition,
  validation: ScriptValidationResponse,
): ScriptDefinition {
  const importMetadata = script.metadata?.import
  if (!importMetadata) return script

  const preserved = { ...(importMetadata.preserved || {}) }
  const runtimeSettingsParity = Array.isArray(validation.runtime_settings_parity) ? validation.runtime_settings_parity : []
  if (runtimeSettingsParity.length > 0) preserved.runtime_settings_parity = runtimeSettingsParity
  else delete preserved.runtime_settings_parity

  const statusCounts = validation.runtime_settings_status_counts
  if (statusCounts && Object.keys(statusCounts).length > 0) preserved.runtime_settings_status_counts = statusCounts
  else delete preserved.runtime_settings_status_counts

  if (validation.migration_readiness) preserved.runtime_settings_migration_readiness = validation.migration_readiness
  else delete preserved.runtime_settings_migration_readiness

  return {
    ...script,
    metadata: {
      ...(script.metadata || {}),
      import: { ...importMetadata, preserved },
    },
  }
}
