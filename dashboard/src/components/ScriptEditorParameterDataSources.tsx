import { isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import type {
  ScriptDefinition,
  ScriptDataSource,
  ScriptParameterDefinition,
} from '../types/scripts'
import {
  inferDelimiter,
  parseInlineData,
  summarizeInlineData,
  validateInlineData,
} from './scriptEditorUtils'
import type { DataSourceHistory } from './scriptEditorState'
import {
  findDataSourceIndexByName,
  normalizeParameterDataLookup,
} from './scriptEditorImportModel'
import {
  dataSourceRowMatchesFilter,
  formatDataSourceColumnLabel,
  formatDataSourceDelimiterLabel,
  getDataSourceDelimiterChoice,
  getDataSourceDelimiterValue,
  getParameterDataColumnIndexes,
  mergeFocusedParameterData,
  stringifyFocusedParameterData,
  type DataSourceDelimiterChoice,
  type DataSourceSortDirection,
} from './scriptEditorParameterDataModel'

type ScriptData = ScriptDefinition
type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition

export interface ParameterDataCellDraftChange {
  sourceIndex: number
  rowIndex: number
  columnIndex: number
  value: string | null
}

const ParameterDataCellDraftContext = createContext<((change: ParameterDataCellDraftChange) => void) | null>(null)

export function ParameterDataCellDraftProvider({
  children,
  onDraftChange,
}: {
  children: ReactNode
  onDraftChange: (change: ParameterDataCellDraftChange) => void
}) {
  return (
    <ParameterDataCellDraftContext.Provider value={onDraftChange}>
      {children}
    </ParameterDataCellDraftContext.Provider>
  )
}

interface ParameterDataCellInputProps {
  sensitive?: boolean
  ariaLabel: string
  value: string
  onCommit: (value: string) => void
  onDraftChange?: (value: string | null) => void
}

/**
 * Keep keystrokes local to the cell and commit once focus leaves it. Updating the
 * script on every character reparses and serializes the full DSL document, which
 * made large parameter files progressively slower to edit.
 */
export function ParameterDataCellInput({ ariaLabel, value, onCommit, onDraftChange, sensitive = false }: ParameterDataCellInputProps) {
  const [draft, setDraft] = useState(value)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const cancelBlurCommitRef = useRef(false)
  const draftRef = useRef(value)
  const storedValueRef = useRef(value)
  const onCommitRef = useRef(onCommit)
  const onDraftChangeRef = useRef(onDraftChange)
  const dirtyRef = useRef(false)

  storedValueRef.current = value
  onCommitRef.current = onCommit
  onDraftChangeRef.current = onDraftChange

  useEffect(() => {
    if (document.activeElement !== inputRef.current) {
      draftRef.current = value
      dirtyRef.current = false
      setDraft(value)
      onDraftChangeRef.current?.(null)
    }
  }, [value])

  useEffect(() => () => {
    // Virtualized rows can disappear without dispatching blur. Flush an active
    // edit from the passive cleanup so scrolling cannot discard a keystroke.
    if (dirtyRef.current && draftRef.current !== storedValueRef.current) {
      dirtyRef.current = false
      onCommitRef.current(draftRef.current)
    }
    onDraftChangeRef.current?.(null)
  }, [])

  const commit = () => {
    if (cancelBlurCommitRef.current) {
      cancelBlurCommitRef.current = false
      dirtyRef.current = false
      onDraftChangeRef.current?.(null)
      return
    }
    if (dirtyRef.current && draftRef.current !== storedValueRef.current) {
      dirtyRef.current = false
      onCommitRef.current(draftRef.current)
      // A rejected structural edit must not leave an unsaved name looking committed.
      setDraft(storedValueRef.current)
      draftRef.current = storedValueRef.current
      onDraftChangeRef.current?.(null)
      return
    }
    dirtyRef.current = false
    onDraftChangeRef.current?.(null)
  }

  return (
    <input
      ref={inputRef}
      type={sensitive ? 'password' : 'text'}
      autoComplete="off"
      aria-label={ariaLabel}
      value={draft}
      onChange={event => {
        const nextValue = event.target.value
        draftRef.current = nextValue
        dirtyRef.current = nextValue !== storedValueRef.current
        setDraft(nextValue)
        onDraftChangeRef.current?.(dirtyRef.current ? nextValue : null)
      }}
      onBlur={commit}
      onKeyDown={event => {
        if (event.key === 'Enter') event.currentTarget.blur()
        if (event.key === 'Escape') {
          cancelBlurCommitRef.current = true
          dirtyRef.current = false
          draftRef.current = storedValueRef.current
          setDraft(value)
          onDraftChangeRef.current?.(null)
          event.currentTarget.blur()
        }
      }}
    />
  )
}

interface ParameterDataSourcesSectionProps {
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  isOpen: boolean
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onToggleOpen: () => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  selectedParameter: ParameterConfig | null
  script: ScriptData
}

export function ParameterDataSourcesSection({
  dataEditorMode,
  dataSourceHistory,
  isOpen,
  onAddDataColumn,
  onAddDataRow,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onToggleOpen,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  selectedParameter,
  script,
}: ParameterDataSourcesSectionProps) {
  const selectedParameterName = selectedParameter?.name || 'Selected parameter'
  const selectedSourceIndex = selectedParameter
    ? findDataSourceIndexByName(script.data_sources, selectedParameter.data_source)
    : null
  const selectedSource = selectedSourceIndex !== null ? script.data_sources?.[selectedSourceIndex] || null : null
  const selectedParsed = selectedSource ? parseInlineData(selectedSource) : null
  const selectedSourceParameters = selectedParameter && selectedSource
    ? (script.parameters || []).filter(parameter =>
      (parameter.type === 'file' || parameter.type === 'list')
      && normalizeParameterDataLookup(parameter.data_source) === normalizeParameterDataLookup(selectedSource.name),
    )
    : []
  const selectedDataParameters = selectedSourceParameters.length > 0
    ? selectedSourceParameters
    : selectedParameter
      ? [selectedParameter]
      : []
  const selectedParameterNames = selectedDataParameters.map((parameter, idx) => parameter.name || `Parameter ${idx + 1}`)
  const selectedColumnIndexes = selectedParsed
    ? Array.from(new Set(selectedDataParameters.flatMap(parameter => getParameterDataColumnIndexes(parameter, selectedParsed.columns))))
    : []
  const selectedSourceSummary = selectedSource ? summarizeInlineData(selectedSource) : null
  const selectedSourceWarnings = selectedSource ? validateInlineData(selectedSource) : []
  const selectedParameterSummary = selectedParameterNames.length > 1
    ? `${selectedParameterNames.length} params: ${selectedParameterNames.join(', ')}`
    : selectedParameterName

  const renderSelectedParameterData = () => {
    if (!selectedParameter) {
      return (
        <div className="parameter-empty-state">
          Select a parameter in the Parameters table above to view its mapped data here.
        </div>
      )
    }

    if (selectedParameter.type !== 'file' && selectedParameter.type !== 'list') {
      return (
        <div className="parameter-empty-state">
          {selectedParameterName} is generated at runtime and does not use parameter data.
        </div>
      )
    }

    if (!selectedParameter.data_source?.trim()) {
      return (
        <div className="parameter-empty-state">
          {selectedParameterName} is not mapped to a data source. Open Settings in the Parameters table to choose a data source and column.
        </div>
      )
    }

    if (!selectedSource || selectedSourceIndex === null || !selectedParsed || !selectedSourceSummary) {
      return (
        <div className="parameter-empty-state">
          The mapped data source for {selectedParameterName} could not be found.
        </div>
      )
    }

    if (selectedColumnIndexes.length === 0) {
      return (
        <div className="parameter-empty-state">
          {selectedParameterName} is mapped to {selectedParameter.data_source}, but no matching parameter columns are selected. Open Settings in the Parameters table to choose the column.
        </div>
      )
    }

    return (
      <div className="parameter-table-wrap parameter-data-selected-view">
        <table className="parameter-table">
          <thead>
            <tr>
              <th>Parameter</th>
              <th>Source</th>
              <th>Type</th>
              <th>Strategy</th>
              <th>Header Row</th>
              <th>Rows</th>
              <th>Column</th>
              <th>Delimiter</th>
              <th>Data File Path</th>
            </tr>
          </thead>
          <tbody>
            <ParameterDataSourceRows
              dataEditorMode={dataEditorMode[selectedSourceIndex] || 'grid'}
              focusedColumnIndexes={selectedColumnIndexes}
              history={dataSourceHistory[selectedSourceIndex]}
              index={selectedSourceIndex}
              isEditorOpen
              key={`${selectedSource.name || 'source'}-${selectedSourceIndex}-${selectedParameterName}`}
              onAddDataColumn={onAddDataColumn}
              onAddDataRow={onAddDataRow}
              onApplyDedupeHeaders={onApplyDedupeHeaders}
              onApplyFillHeaders={onApplyFillHeaders}
              onApplyNormalizeRows={onApplyNormalizeRows}
              onAutoFixDataSource={onAutoFixDataSource}
              onExportDataSourceFile={onExportDataSourceFile}
              onImportDataSourceFile={onImportDataSourceFile}
              onRedoDataSource={onRedoDataSource}
              onRemoveDataColumn={onRemoveDataColumn}
              onRemoveDataRow={onRemoveDataRow}
              onRemoveDataSource={onRemoveDataSource}
              onToggleDataEditorMode={onToggleDataEditorMode}
              onToggleOpenDataEditor={onToggleOpenDataEditor}
              onUndoDataSource={onUndoDataSource}
              onUpdateDataCell={onUpdateDataCell}
              onUpdateDataColumnName={onUpdateDataColumnName}
              onUpdateDataSource={onUpdateDataSource}
              parsed={selectedParsed}
              selectedParameterName={selectedParameterName}
              selectedParameterNames={selectedParameterNames}
              source={selectedSource}
              summary={selectedSourceSummary}
              warnings={selectedSourceWarnings}
            />
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <>
      <div className="panel-title-row parameter-section-header">
        <button
          type="button"
          className="parameter-section-toggle"
          aria-expanded={isOpen}
          aria-label={`${isOpen ? 'Collapse' : 'Expand'} Parameter Data`}
          onClick={onToggleOpen}
        >
          <span aria-hidden="true" className="parameter-section-chevron">{isOpen ? '▾' : '▸'}</span>
          <span>
            <strong style={{ display: 'block', marginBottom: 4 }}>Parameter Data</strong>
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Shows only the mapped data for the parameter selected above.</span>
          </span>
        </button>
        {selectedParameter && (
          <span className="parameter-data-focus-pill">
            {selectedParameterSummary}
          </span>
        )}
      </div>

      {isOpen && renderSelectedParameterData()}
    </>
  )
}

interface ParameterDataSourceRowsProps {
  dataEditorMode: 'grid' | 'raw'
  focusedColumnIndexes?: number[]
  history?: DataSourceHistory
  index: number
  isEditorOpen: boolean
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  parsed: { columns: string[]; rows: string[][] }
  selectedParameterName?: string
  selectedParameterNames?: string[]
  source: DataSourceConfig
  summary: { rows: number; columns: number }
  warnings: string[]
}

function ParameterDataSourceRows({
  dataEditorMode,
  focusedColumnIndexes,
  history,
  index,
  isEditorOpen,
  onAddDataColumn,
  onAddDataRow,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  parsed,
  selectedParameterName,
  selectedParameterNames,
  source,
  summary,
  warnings,
}: ParameterDataSourceRowsProps) {
  const delimiterChoice = getDataSourceDelimiterChoice(source)
  const sourceLabel = source.name || `Source ${index + 1}`
  const isFocusedParameterView = Boolean(focusedColumnIndexes?.length)
  const focusedParameterLabel = selectedParameterNames?.length
    ? selectedParameterNames.join(', ')
    : selectedParameterName || 'Selected parameter'
  const visibleColumnIndexes = isFocusedParameterView
    ? (focusedColumnIndexes || []).filter(colIdx => colIdx >= 0 && colIdx < parsed.columns.length)
    : parsed.columns.map((_, colIdx) => colIdx)
  const updateDelimiterChoice = (choice: DataSourceDelimiterChoice) => {
    onUpdateDataSource(index, 'delimiter', getDataSourceDelimiterValue(choice), true)
  }

  return (
    <>
      <tr>
        <td>
          {isFocusedParameterView ? (
            <span className="cell-mono">{focusedParameterLabel}</span>
          ) : (
            <input
              aria-label={`Source name for ${sourceLabel}`}
              value={source.name || ''}
              onChange={e => onUpdateDataSource(index, 'name', e.target.value)}
              placeholder="users"
            />
          )}
        </td>
        {isFocusedParameterView && (
          <td>
            <span className="cell-mono">{sourceLabel}</span>
          </td>
        )}
        <td>
          <select aria-label={`File type for ${sourceLabel}`} value={source.type || 'csv'} onChange={e => onUpdateDataSource(index, 'type', e.target.value)}>
            <option value="csv">csv</option>
            <option value="dat">dat</option>
          </select>
        </td>
        <td>
          <select aria-label={`Iteration strategy for ${sourceLabel}`} value={source.strategy || 'sequential'} onChange={e => onUpdateDataSource(index, 'strategy', e.target.value)}>
            <option value="sequential">sequential</option>
            <option value="random">random</option>
            <option value="unique">unique</option>
          </select>
        </td>
        <td>
          <label className="checkbox-label compact">
            <input
              aria-label={`First row is header for ${sourceLabel}`}
              type="checkbox"
              checked={source.first_row_header !== false}
              onChange={e => onUpdateDataSource(index, 'first_row_header', e.target.checked)}
            />
            <span>{source.first_row_header !== false ? 'Yes' : 'No'}</span>
          </label>
        </td>
        <td className="cell-mono">{summary.rows || '—'}</td>
        <td>
          {isFocusedParameterView ? (
            <span className="cell-mono">
              {visibleColumnIndexes.map(colIdx => formatDataSourceColumnLabel(parsed.columns[colIdx], colIdx)).join(', ') || '—'}
            </span>
          ) : (
            <input
              aria-label={`Columns for ${sourceLabel}`}
              value={(source.columns || []).join(', ')}
              onChange={e => onUpdateDataSource(
                index,
                'columns',
                e.target.value.split(',').map(v => v.trim()).filter(Boolean),
              )}
              placeholder="username, password"
            />
          )}
        </td>
        <td>
          <select
            aria-label={`Delimiter for ${sourceLabel}`}
            value={delimiterChoice}
            onChange={e => updateDelimiterChoice(e.target.value as DataSourceDelimiterChoice)}
          >
            <option value="auto">Auto ({inferDelimiter(source) === '\t' ? 'Tab' : 'Comma'})</option>
            <option value="comma">Comma</option>
            <option value="tab">Tab</option>
          </select>
        </td>
        <td>
          <input
            aria-label={`Data file path for ${sourceLabel}`}
            value={source.path || ''}
            onChange={e => onUpdateDataSource(index, 'path', e.target.value)}
            placeholder="/absolute/path/to/data.csv (optional when inline data is used)"
          />
        </td>
        {!isFocusedParameterView && (
          <td aria-label="Parameter actions" style={{ textAlign: 'right' }}>
            <div className="parameter-actions">
              <button className="btn btn-sm btn-ghost" onClick={() => onToggleOpenDataEditor(index)}>
                {isEditorOpen ? 'Hide Data' : 'Edit Data'}
              </button>
              <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => onRemoveDataSource(index)}>Remove</button>
            </div>
          </td>
        )}
      </tr>
      {isEditorOpen && (
        <tr className="parameter-editor-row">
          <td colSpan={9}>
            <ParameterDataEditor
              dataEditorMode={dataEditorMode}
              focusedColumnIndexes={focusedColumnIndexes}
              history={history}
              index={index}
              onAddDataColumn={onAddDataColumn}
              onAddDataRow={onAddDataRow}
              onApplyDedupeHeaders={onApplyDedupeHeaders}
              onApplyFillHeaders={onApplyFillHeaders}
              onApplyNormalizeRows={onApplyNormalizeRows}
              onAutoFixDataSource={onAutoFixDataSource}
              onExportDataSourceFile={onExportDataSourceFile}
              onImportDataSourceFile={onImportDataSourceFile}
              onRedoDataSource={onRedoDataSource}
              onRemoveDataColumn={onRemoveDataColumn}
              onRemoveDataRow={onRemoveDataRow}
              onToggleDataEditorMode={onToggleDataEditorMode}
              onUndoDataSource={onUndoDataSource}
              onUpdateDataCell={onUpdateDataCell}
              onUpdateDataColumnName={onUpdateDataColumnName}
              onUpdateDataSource={onUpdateDataSource}
              parsed={parsed}
              selectedParameterName={selectedParameterName}
              selectedParameterNames={selectedParameterNames}
              source={source}
              summary={summary}
              warnings={warnings}
            />
          </td>
        </tr>
      )}
    </>
  )
}

interface ParameterDataEditorProps {
  highlightedColumnIndexes?: number[]
  dataEditorMode: 'grid' | 'raw'
  focusedColumnIndexes?: number[]
  history?: DataSourceHistory
  index: number
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onToggleDataEditorMode: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  parsed: { columns: string[]; rows: string[][] }
  selectedParameterName?: string
  selectedParameterNames?: string[]
  showHeaderTitle?: boolean
  source: DataSourceConfig
  summary: { rows: number; columns: number }
  warnings: string[]
}

export function ParameterDataEditor({
  highlightedColumnIndexes = [],
  dataEditorMode,
  focusedColumnIndexes,
  history,
  index,
  onAddDataColumn,
  onAddDataRow,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onToggleDataEditorMode,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  parsed,
  selectedParameterName,
  selectedParameterNames,
  showHeaderTitle = true,
  source,
  summary,
  warnings,
}: ParameterDataEditorProps) {
  const onDataCellDraftChange = useContext(ParameterDataCellDraftContext)
  const [sortState, setSortState] = useState<{ column: number; direction: DataSourceSortDirection } | null>(null)
  const [filterText, setFilterText] = useState('')
  const [jumpRow, setJumpRow] = useState('1')
  const [showSensitive, setShowSensitive] = useState(false)
  const hasSensitiveColumns = parsed.columns.some(isSensitiveCredentialPreviewName)
  const [filterColumn, setFilterColumn] = useState<number | 'all'>('all')
  const sourceLabel = source.name || `Source ${index + 1}`
  const isFocusedParameterView = Boolean(focusedColumnIndexes?.length)
  const focusedParameterLabel = selectedParameterNames?.length
    ? selectedParameterNames.join(', ')
    : selectedParameterName || 'Selected parameter'
  const focusedDataTitle = selectedParameterNames && selectedParameterNames.length > 1
    ? `${sourceLabel} Parameter Data`
    : `${focusedParameterLabel} Data`
  const visibleColumnIndexes = isFocusedParameterView
    ? (focusedColumnIndexes || []).filter(colIdx => colIdx >= 0 && colIdx < parsed.columns.length)
    : parsed.columns.map((_, colIdx) => colIdx)
  const effectiveFilterColumn = filterColumn === 'all' || visibleColumnIndexes.includes(filterColumn) ? filterColumn : 'all'
  const filteredRows = parsed.rows
    .map((row, rowIdx) => ({ row, rowIdx }))
    .filter(({ row }) => dataSourceRowMatchesFilter(row, filterText, effectiveFilterColumn))
    .sort((left, right) => {
      if (!sortState) return left.rowIdx - right.rowIdx
      const comparison = (left.row[sortState.column] || '').localeCompare(right.row[sortState.column] || '', undefined, { numeric: true })
      return sortState.direction === 'asc' ? comparison : -comparison
    })
  const gridScrollRef = useRef<HTMLDivElement | null>(null)
  const virtualizeRows = filteredRows.length > 100
  const rowVirtualizer = useVirtualizer({
    count: filteredRows.length,
    getScrollElement: () => gridScrollRef.current,
    estimateSize: () => 42,
    overscan: 12,
    enabled: virtualizeRows,
  })
  const virtualRows = virtualizeRows ? rowVirtualizer.getVirtualItems() : []
  const rowsToRender = virtualizeRows
    ? virtualRows.map(item => filteredRows[item.index])
    : filteredRows
  const paddingTop = virtualRows[0]?.start || 0
  const paddingBottom = virtualRows.length > 0
    ? Math.max(0, rowVirtualizer.getTotalSize() - virtualRows[virtualRows.length - 1].end)
    : 0
  const focusedRawData = isFocusedParameterView
    ? stringifyFocusedParameterData(source, parsed, visibleColumnIndexes)
    : source.inline_data || ''
  const [rawDraft, setRawDraft] = useState<{ base: string; value: string } | null>(null)
  const rawValue = rawDraft?.base === focusedRawData ? rawDraft.value : focusedRawData
  const updateRawData = (value: string) => {
    onUpdateDataSource(
      index,
      'inline_data',
      isFocusedParameterView ? mergeFocusedParameterData(source, parsed, visibleColumnIndexes, value) : value,
      true,
    )
  }
  const sortColumn = (colIdx: number) => {
    const nextDirection: DataSourceSortDirection = sortState?.column === colIdx && sortState.direction === 'asc'
      ? 'desc'
      : 'asc'
    setSortState({ column: colIdx, direction: nextDirection })
  }

  return (
    <div className="parameter-editor">
      <div className="parameter-editor-header">
        {showHeaderTitle && <strong>{isFocusedParameterView ? focusedDataTitle : sourceLabel}</strong>}
        <div className="parameter-editor-meta">
          <span className="cell-mono">
            {summary.rows} rows · {visibleColumnIndexes.length || summary.columns} {isFocusedParameterView ? 'selected ' : ''}column{(visibleColumnIndexes.length || summary.columns) === 1 ? '' : 's'} · {formatDataSourceDelimiterLabel(source)}
          </span>
          <div className="parameter-editor-controls">
            <button className="btn btn-sm btn-ghost" disabled={!history?.past?.length} onClick={() => onUndoDataSource(index)}>Undo</button>
            <button className="btn btn-sm btn-ghost" disabled={!history?.future?.length} onClick={() => onRedoDataSource(index)}>Redo</button>
            <button className="btn btn-sm btn-ghost" onClick={() => onToggleDataEditorMode(index)}>
              {dataEditorMode === 'grid' ? 'Raw Mode' : 'Grid Mode'}
            </button>
            <label className="btn btn-sm btn-ghost file-btn">
              Import
              <input
                type="file"
                accept=".csv,.dat,.txt"
                onChange={e => {
                  const file = e.target.files?.[0] || null
                  onImportDataSourceFile(index, file)
                  e.currentTarget.value = ''
                }}
              />
            </label>
            {!isFocusedParameterView && (
              <button className="btn btn-sm btn-ghost" onClick={() => onExportDataSourceFile(source, index)}>Export</button>
            )}
          </div>
        </div>
      </div>
      {hasSensitiveColumns && <label className="checkbox-label"><input type="checkbox" aria-label="Reveal sensitive columns" checked={showSensitive} onChange={event => setShowSensitive(event.target.checked)} />Reveal sensitive columns (exports include source values)</label>}
      {warnings.length > 0 && (
        <div className="parameter-validation">
          <div className="parameter-validation-header">
            <strong>Data Warnings</strong>
            <div className="parameter-validation-actions">
              <button className="btn btn-sm btn-ghost" onClick={() => onApplyFillHeaders(index)}>Fill Headers</button>
              <button className="btn btn-sm btn-ghost" onClick={() => onApplyDedupeHeaders(index)}>Deduplicate</button>
              <button className="btn btn-sm btn-ghost" onClick={() => onApplyNormalizeRows(index)}>Normalize Rows</button>
              <button className="btn btn-sm btn-ghost" onClick={() => onAutoFixDataSource(index)}>Auto-Fix All</button>
            </div>
          </div>
          {warnings.map((warning, warningIdx) => (
            <div key={`${index}-warn-${warningIdx}`} className="parameter-validation-item">{warning}</div>
          ))}
        </div>
      )}
      {dataEditorMode === 'raw' && hasSensitiveColumns && !showSensitive ? <p>Reveal sensitive columns to edit the raw source.</p> : dataEditorMode === 'grid' ? (
        <div className="parameter-grid-wrap">
          <div className="parameter-grid-actions">
            <div className="parameter-grid-action-buttons">
              <label>Go to row <input aria-label={`Go to row for ${sourceLabel}`} type="number" min="1" max={filteredRows.length} value={jumpRow} onChange={event => setJumpRow(event.target.value)} /></label>
              <button type="button" className="btn btn-sm btn-secondary" disabled={!filteredRows.length} onClick={() => {
                const position = Math.max(0, Math.min(filteredRows.length - 1, Math.trunc(Number(jumpRow) || 1) - 1))
                if (virtualizeRows) rowVirtualizer.scrollToIndex(position, { align: 'start' })
                else gridScrollRef.current?.querySelectorAll('tbody tr')[position]?.scrollIntoView({ block: 'nearest' })
              }}>Go</button>
              <button className="btn btn-sm btn-ghost" onClick={() => onAddDataRow(index)}>+ Row</button>
              {!isFocusedParameterView && (
                <button className="btn btn-sm btn-ghost" onClick={() => onAddDataColumn(index)}>+ Column</button>
              )}
            </div>
            <div className="parameter-grid-filters">
              <label>
                <span>Search</span>
                <input
                  aria-label={`Search rows for ${sourceLabel}`}
                  value={filterText}
                  onChange={event => setFilterText(event.target.value)}
                  placeholder="Find row values"
                />
              </label>
              <label>
                <span>Column</span>
                <select
                  aria-label={`Filter column for ${sourceLabel}`}
                  value={effectiveFilterColumn === 'all' ? 'all' : String(effectiveFilterColumn)}
                  onChange={event => setFilterColumn(event.target.value === 'all' ? 'all' : Number(event.target.value))}
                >
                  <option value="all">All columns</option>
                  {visibleColumnIndexes.map((colIdx) => {
                    const column = parsed.columns[colIdx]
                    return (
                    <option key={`${index}-filter-${colIdx}`} value={colIdx}>
                      {formatDataSourceColumnLabel(column, colIdx)}
                    </option>
                    )
                  })}
                </select>
              </label>
              <span className="parameter-grid-filter-count">
                {filteredRows.length}/{parsed.rows.length} rows
              </span>
              {filterText && (
                <button className="btn btn-sm btn-ghost" onClick={() => setFilterText('')}>Clear</button>
              )}
            </div>
          </div>
          <div ref={gridScrollRef} className="parameter-grid-scroll">
            <table className="parameter-grid" data-table-pagination="off" aria-label={`${sourceLabel} dataset`} aria-rowcount={filteredRows.length + 1}>
              <thead>
                <tr>
                  <th className="parameter-grid-row-action" aria-label="Row actions" />
                  <th>#</th>
                  {visibleColumnIndexes.map((colIdx) => {
                    const column = parsed.columns[colIdx]
                    const isSortedColumn = sortState?.column === colIdx
                    const columnLabel = formatDataSourceColumnLabel(column, colIdx)
                    const nextSortDirection: DataSourceSortDirection = isSortedColumn && sortState?.direction === 'asc' ? 'desc' : 'asc'
                    return (
                      <th
                        key={`${index}-h-${colIdx}`}
                        className={highlightedColumnIndexes.includes(colIdx) ? 'mapped-column' : undefined}
                        aria-sort={isSortedColumn ? (sortState.direction === 'asc' ? 'ascending' : 'descending') : 'none'}
                      >
                        <div className="parameter-grid-head">
                          {isFocusedParameterView ? (
                            <span>{columnLabel}</span>
                          ) : (
                            <ParameterDataCellInput ariaLabel={`Column ${colIdx + 1} name for ${sourceLabel}`} value={column} onCommit={value => onUpdateDataColumnName(index, colIdx, value)} />
                          )}
                          <div className="parameter-grid-head-actions">
                            <button
                              className={`btn-icon parameter-grid-sort ${isSortedColumn ? 'active' : ''}`}
                              onClick={() => sortColumn(colIdx)}
                              title={`Sort ${columnLabel} ${nextSortDirection === 'asc' ? 'ascending' : 'descending'}`}
                              aria-label={`Sort ${columnLabel} ${nextSortDirection === 'asc' ? 'ascending' : 'descending'}`}
                            >
                              {isSortedColumn ? (sortState.direction === 'asc' ? '↑' : '↓') : '↕'}
                            </button>
                            {!isFocusedParameterView && (
                              <button className="btn-icon danger" onClick={() => onRemoveDataColumn(index, colIdx)} title="Remove column" aria-label={`Remove column ${columnLabel}`}>✕</button>
                            )}
                          </div>
                        </div>
                      </th>
                    )
                  })}
                </tr>
              </thead>
              <tbody>
                {virtualizeRows && paddingTop > 0 && (
                  <tr aria-hidden="true"><td aria-label="Virtualized rows omitted" colSpan={visibleColumnIndexes.length + 2} style={{ height: paddingTop, padding: 0, border: 0 }} /></tr>
                )}
                {rowsToRender.map(({ row, rowIdx }) => (
                  <tr key={`${index}-r-${rowIdx}`} aria-rowindex={filteredRows.findIndex(item => item.rowIdx === rowIdx) + 2}>
                    <td className="parameter-grid-row-action">
                      <button
                        className="btn-icon danger"
                        onClick={() => onRemoveDataRow(index, rowIdx)}
                        title="Remove row"
                        aria-label={`Remove row ${rowIdx + 1}`}
                      >
                        ✕
                      </button>
                    </td>
                    <td className="parameter-grid-index">
                      {rowIdx + 1}
                    </td>
                    {visibleColumnIndexes.map((colIdx) => (
                      <td key={`${index}-c-${rowIdx}-${colIdx}`} className={highlightedColumnIndexes.includes(colIdx) ? 'mapped-column' : undefined}>
                        <ParameterDataCellInput
                          ariaLabel={`Row ${rowIdx + 1}, ${formatDataSourceColumnLabel(parsed.columns[colIdx], colIdx)} for ${sourceLabel}`}
                          value={row[colIdx] || ''}
                          sensitive={!showSensitive && isSensitiveCredentialPreviewName(parsed.columns[colIdx])}
                          onCommit={value => onUpdateDataCell(index, rowIdx, colIdx, value)}
                          onDraftChange={value => onDataCellDraftChange?.({
                            sourceIndex: index,
                            rowIndex: rowIdx,
                            columnIndex: colIdx,
                            value,
                          })}
                        />
                      </td>
                    ))}
                  </tr>
                ))}
                {virtualizeRows && paddingBottom > 0 && (
                  <tr aria-hidden="true"><td aria-label="Virtualized rows omitted" colSpan={visibleColumnIndexes.length + 2} style={{ height: paddingBottom, padding: 0, border: 0 }} /></tr>
                )}
                {parsed.rows.length === 0 && (
                  <tr>
                    <td colSpan={visibleColumnIndexes.length + 2} className="parameter-grid-empty">
                      {parsed.rows.length ? 'No matching rows. Clear the search to see all records.' : 'No rows yet. Use + Row to add parameter records.'}
                    </td>
                  </tr>
                )}
                {parsed.rows.length > 0 && filteredRows.length === 0 && (
                  <tr>
                    <td colSpan={visibleColumnIndexes.length + 2} className="parameter-grid-empty">
                      No rows match the current filter.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="parameter-raw-editor">
        <p>Raw changes stay in this editor until reviewed. Switching datasets discards an unreviewed raw draft.</p>
        <textarea
          aria-label={`Raw data for ${sourceLabel}`}
          value={rawValue}
          onChange={e => setRawDraft({ base: focusedRawData, value: e.target.value })}
          placeholder={source.first_row_header !== false ? 'username,password\nalice,pass1\nbob,pass2' : 'alice,pass1\nbob,pass2'}
          spellCheck={false}
        />
        <button type="button" className="btn btn-secondary" disabled={rawValue === focusedRawData} onClick={() => updateRawData(rawValue)}>Review raw changes</button>
        <button type="button" className="btn btn-ghost" disabled={rawValue === focusedRawData} onClick={() => setRawDraft(null)}>Discard raw changes</button>
        </div>
      )}
    </div>
  )
}
