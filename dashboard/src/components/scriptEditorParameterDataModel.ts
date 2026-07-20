import type { ScriptDefinition, ScriptDataSource, ScriptParameterDefinition } from '../types/scripts'
import {
  EMPTY_DATA_SOURCE,
  inferDelimiter,
  parseInlineData,
  stringifyInlineData,
} from './scriptEditorUtils'
import { normalizeParameterDataLookup } from './scriptEditorImportModel'

type ScriptData = ScriptDefinition
type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition

export interface ParameterSameLineOption {
  label: string
  value: string
}

export function getSameLineParameterOptions(
  parameters: ParameterConfig[],
  idx: number | null,
): ParameterSameLineOption[] {
  if (idx === null) return []
  const current = parameters[idx]
  const sourceName = normalizeParameterDataLookup(current?.data_source)
  if (!current || !sourceName || (current.type !== 'file' && current.type !== 'list')) return []
  return parameters
    .map((parameter, index) => ({ parameter, index }))
    .filter(({ parameter, index }) =>
      index !== idx
      && Boolean(parameter.name?.trim())
      && (parameter.type === 'file' || parameter.type === 'list')
      && normalizeParameterDataLookup(parameter.data_source) === sourceName,
    )
    .map(({ parameter }) => ({
      label: `${parameter.name}${parameter.column ? ` (${parameter.column})` : ''}`,
      value: parameter.name || '',
    }))
}

export function getSameLineParameterSelectOptions(
  parameter: ParameterConfig,
  sameLineOptions: ParameterSameLineOption[],
): ParameterSameLineOption[] {
  const currentValue = parameter.same_line_as?.trim()
  if (!currentValue || sameLineOptions.some(option => option.value === currentValue)) return sameLineOptions
  return [{ label: `${currentValue} (unmatched)`, value: currentValue }, ...sameLineOptions]
}

export type DataSourceDelimiterChoice = 'auto' | 'comma' | 'tab'
export type DataSourceSortDirection = 'asc' | 'desc'

const dataSourceSortCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

export function getDataSourceDelimiterChoice(source: DataSourceConfig): DataSourceDelimiterChoice {
  const rawDelimiter = source.delimiter || ''
  const delimiter = rawDelimiter === '\t' ? '\t' : rawDelimiter.trim()
  const normalized = delimiter.toLowerCase()
  if (!delimiter || normalized === 'auto') return 'auto'
  if (normalized === 'comma') return 'comma'
  if (delimiter === '\t' || delimiter === '\\t' || normalized === 'tab') return 'tab'
  return delimiter === ',' && inferDelimiter(source) === '\t' ? 'auto' : 'comma'
}

export function getDataSourceDelimiterValue(choice: DataSourceDelimiterChoice): string {
  if (choice === 'auto') return 'auto'
  return choice === 'tab' ? 'tab' : 'comma'
}

export function formatDataSourceDelimiterLabel(source: DataSourceConfig): string {
  const choice = getDataSourceDelimiterChoice(source)
  const detected = inferDelimiter(source) === '\t' ? 'Tab' : 'Comma'
  return choice === 'auto' ? `Auto: ${detected}` : detected
}

export function compareDataSourceCellValues(first: string | undefined, second: string | undefined): number {
  const normalizedFirst = (first || '').trim()
  const normalizedSecond = (second || '').trim()
  return dataSourceSortCollator.compare(normalizedFirst, normalizedSecond)
}

export function formatDataSourceColumnLabel(column: string | undefined, colIdx: number): string {
  const normalized = (column || '').trim()
  return normalized || `Column ${colIdx + 1}`
}

export function dataSourceRowMatchesFilter(
  row: string[],
  query: string,
  filterColumn: number | 'all',
): boolean {
  const normalizedQuery = query.trim().toLowerCase()
  if (!normalizedQuery) return true
  const values = filterColumn === 'all' ? row : [row[filterColumn] || '']
  return values.some(value => (value || '').toLowerCase().includes(normalizedQuery))
}

export function getParameterDataColumnIndexes(parameter: ParameterConfig, columns: string[]): number[] {
  const configuredColumn = normalizeParameterDataLookup(parameter.column)
  if (!configuredColumn) {
    return columns.length === 1 ? [0] : []
  }

  const exactColumnIndex = columns.findIndex(column => normalizeParameterDataLookup(column) === configuredColumn)
  if (exactColumnIndex >= 0) return [exactColumnIndex]

  return columns.length === 1 ? [0] : []
}

export function stringifyFocusedParameterData(source: DataSourceConfig, parsed: { columns: string[]; rows: string[][] }, columnIndexes: number[]): string {
  const columns = columnIndexes.map(colIdx => parsed.columns[colIdx] || `col_${colIdx + 1}`)
  const rows = parsed.rows.map(row => columnIndexes.map(colIdx => row[colIdx] || ''))
  return stringifyInlineData(source, columns, rows)
}

export function mergeFocusedParameterData(
  source: DataSourceConfig,
  parsed: { columns: string[]; rows: string[][] },
  columnIndexes: number[],
  rawValue: string,
): string {
  const viewSource: DataSourceConfig = {
    ...source,
    columns: source.first_row_header === false ? columnIndexes.map(colIdx => parsed.columns[colIdx] || `col_${colIdx + 1}`) : [],
    inline_data: rawValue,
  }
  const focused = parseInlineData(viewSource)
  const nextColumns = [...parsed.columns]
  if (source.first_row_header !== false) {
    columnIndexes.forEach((colIdx, position) => {
      const nextName = focused.columns[position]?.trim()
      if (nextName) nextColumns[colIdx] = nextName
    })
  }

  const rowCount = Math.max(parsed.rows.length, focused.rows.length)
  const nextRows = Array.from({ length: rowCount }, (_, rowIdx) => {
    const nextRow = [...(parsed.rows[rowIdx] || [])]
    while (nextRow.length < nextColumns.length) nextRow.push('')
    columnIndexes.forEach((colIdx, position) => {
      nextRow[colIdx] = focused.rows[rowIdx]?.[position] || ''
    })
    return nextRow.slice(0, nextColumns.length)
  })

  return stringifyInlineData(source, nextColumns, nextRows)
}

interface ParameterSourceMergeCandidate {
  columnIndex: number | null
  parameter: ParameterConfig
  parameterIndex: number
  parsed: { columns: string[]; rows: string[][] }
  source: DataSourceConfig
  sourceIndex: number
  targetColumn: string
}

export interface ParameterSourceMergeSummary {
  canMerge: boolean
  parameterCount: number
  sourceCount: number
}

export interface ParameterSourceMergeResult extends ParameterSourceMergeSummary {
  mergedSourceIndex: number
  mergedSourceName: string
  rowCount: number
  script: ScriptData
}

function isParameterSourceMergeEligible(source?: DataSourceConfig | null): source is DataSourceConfig {
  if (!source) return false
  const type = (source.type || '').trim().toLowerCase()
  const path = (source.path || source.name || '').trim().toLowerCase()
  return type === 'csv' || type === 'dat' || path.endsWith('.csv') || path.endsWith('.dat')
}

function uniqueParameterMergeColumnName(requested: string, used: Set<string>): string {
  const base = (requested || 'parameter').trim() || 'parameter'
  let candidate = base
  let suffix = 2
  while (used.has(normalizeParameterDataLookup(candidate))) {
    candidate = `${base}_${suffix}`
    suffix += 1
  }
  used.add(normalizeParameterDataLookup(candidate))
  return candidate
}

function uniqueMergedParameterSourceName(sources: DataSourceConfig[]): string {
  const used = new Set(sources.map(source => normalizeParameterDataLookup(source.name)))
  const base = 'merged_parameters'
  let candidate = base
  let suffix = 2
  while (used.has(normalizeParameterDataLookup(candidate))) {
    candidate = `${base}_${suffix}`
    suffix += 1
  }
  return candidate
}

function collectParameterSourceMergeCandidates(script: ScriptData): ParameterSourceMergeCandidate[] {
  const sources = script.data_sources || []
  const sourceByName = new Map<string, { index: number; source: DataSourceConfig }>()
  sources.forEach((source, index) => {
    const key = normalizeParameterDataLookup(source.name)
    if (key) sourceByName.set(key, { index, source })
  })

  const usedColumns = new Set<string>()
  return (script.parameters || []).flatMap((parameter, parameterIndex) => {
    if (parameter.type !== 'file' && parameter.type !== 'list') return []
    const sourceMatch = sourceByName.get(normalizeParameterDataLookup(parameter.data_source))
    if (!sourceMatch || !isParameterSourceMergeEligible(sourceMatch.source)) return []

    const parsed = parseInlineData(sourceMatch.source)
    const columnIndexes = getParameterDataColumnIndexes(parameter, parsed.columns)
    const columnIndex = columnIndexes[0] ?? null
    const sourceColumnName = columnIndex !== null ? parsed.columns[columnIndex] : ''
    const targetColumn = uniqueParameterMergeColumnName(
      parameter.name || parameter.column || sourceColumnName || `parameter_${parameterIndex + 1}`,
      usedColumns,
    )

    return [{
      columnIndex,
      parameter,
      parameterIndex,
      parsed,
      source: sourceMatch.source,
      sourceIndex: sourceMatch.index,
      targetColumn,
    }]
  })
}

export function summarizeParameterSourceMerge(script: ScriptData): ParameterSourceMergeSummary {
  const candidates = collectParameterSourceMergeCandidates(script)
  const sourceCount = new Set(candidates.map(candidate => candidate.sourceIndex)).size
  return {
    canMerge: sourceCount > 1 && candidates.length > 0,
    parameterCount: candidates.length,
    sourceCount,
  }
}

export function buildMergedParameterSourceScript(script: ScriptData): ParameterSourceMergeResult | null {
  const candidates = collectParameterSourceMergeCandidates(script)
  const sourceIndexes = new Set(candidates.map(candidate => candidate.sourceIndex))
  if (sourceIndexes.size <= 1 || candidates.length === 0) return null

  const sources = script.data_sources || []
  const parameters = script.parameters || []
  const mergedSourceName = uniqueMergedParameterSourceName(sources)
  const targetSourceShell: DataSourceConfig = {
    ...EMPTY_DATA_SOURCE,
    name: mergedSourceName,
    type: 'csv',
    path: `${mergedSourceName}.csv`,
    strategy: 'sequential',
    delimiter: 'comma',
    first_row_header: true,
    columns: [],
  }
  const columns = candidates.map(candidate => candidate.targetColumn)
  const rowCount = candidates.reduce((max, candidate) => Math.max(max, candidate.parsed.rows.length), 0)
  const rows = Array.from({ length: rowCount }, (_, rowIdx) =>
    candidates.map(candidate => {
      if (candidate.columnIndex === null) return ''
      return candidate.parsed.rows[rowIdx]?.[candidate.columnIndex] || ''
    }),
  )
  const mergedSource: DataSourceConfig = {
    ...targetSourceShell,
    inline_data: stringifyInlineData(targetSourceShell, columns, rows),
  }
  const targetColumnByParameterIndex = new Map(candidates.map(candidate => [candidate.parameterIndex, candidate.targetColumn]))
  const nextParameters = parameters.map((parameter, index) => {
    const targetColumn = targetColumnByParameterIndex.get(index)
    if (!targetColumn) return parameter
    return {
      ...parameter,
      data_source: mergedSourceName,
      column: targetColumn,
    }
  })
  const nextSources = sources.filter((_, index) => !sourceIndexes.has(index))
  const mergedSourceIndex = nextSources.length
  nextSources.push(mergedSource)

  return {
    canMerge: true,
    mergedSourceIndex,
    mergedSourceName,
    parameterCount: candidates.length,
    rowCount,
    script: {
      ...script,
      data_sources: nextSources,
      parameters: nextParameters,
    },
    sourceCount: sourceIndexes.size,
  }
}
