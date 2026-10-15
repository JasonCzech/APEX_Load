import { ParameterValuePreview } from './ParameterValuePreview'
import ParameterRenameInput from './ParameterRenameInput'
import { parameterUsages } from './parameterIntegrity'
import { ParameterDatasetWorkspace } from './ParameterDatasetWorkspace'
import TabList from './ux/TabList'
import DialogLayer from './ux/DialogLayer'
import { handleTabKey } from './tabKeyboard'
import { Fragment, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'
import type {
  ScriptDefinition,
  ScriptDataSource,
  ScriptParameterDefinition,
} from '../types/scripts'
import { dialogBackdropProps } from './dialogBackdropProps'
import {
  EMPTY_PARAMETER,
  countUniqueColumnValues,
  inferDelimiter,
  parseInlineData,
  sanitizeVarName,
  summarizeInlineData,
  validateInlineData,
  type JsonStringOccurrence,
} from './scriptEditorUtils'
import {
  applyParameterTypeDefaults,
  CUSTOM_PARAMETER_FORMAT_VALUE,
  parameterFormatForType,
  parameterFormatPresetsForType,
} from './scriptParameterFormat'
import type {
  ActionEditorParameterDraft,
  DataSourceHistory,
  ParameterCandidate,
} from './scriptEditorState'
import {
  findDataSourceIndexByName,
  findFirstParameterIndexByDataSource,
  normalizeParameterDataLookup,
} from './scriptEditorImportModel'
import {
  formatDataSourceColumnLabel,
  getDataSourceDelimiterChoice,
  getDataSourceDelimiterValue,
  getParameterDataColumnIndexes,
  getSameLineParameterOptions,
  getSameLineParameterSelectOptions,
  type DataSourceDelimiterChoice,
  type ParameterSameLineOption,
  type ParameterSourceMergeSummary,
} from './scriptEditorParameterDataModel'
import { ParameterDataEditor } from './ScriptEditorParameterDataSources'
import { useEditorDialogRef } from './useEditorDialogRef'

type ScriptData = ScriptDefinition
type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition

export interface JsonParameterOccurrenceReviewState {
  draft: ActionEditorParameterDraft
  occurrences: JsonStringOccurrence[]
}

function getDataSourceFileName(source?: DataSourceConfig | null): string | null {
  if (!source) return null
  const path = (source.path || '').trim()
  if (path) {
    const parts = path.split(/[\\/]+/).filter(Boolean)
    return parts[parts.length - 1] || path
  }
  const sourceName = (source.name || '').trim()
  if (!sourceName) return null
  if (/\.[A-Za-z0-9]+$/.test(sourceName)) return sourceName
  const extension = (source.type || '').trim().toLowerCase()
  return extension === 'csv' || extension === 'dat' ? `${sourceName}.${extension}` : sourceName
}

function getExternalDataSourceReference(source?: DataSourceConfig | null): string | null {
  if (!source || source.inline_data?.trim()) return null
  return source.path?.trim()
    || source.storage_ref?.trim()
    || source.staged_asset?.trim()
    || null
}

function getSelectedParameterHeaderDetails(
  script: ScriptData,
  selectedParameter: ParameterConfig | null,
  selectedParameterIdx: number | null,
) {
  if (!selectedParameter || selectedParameterIdx === null) {
    return {
      title: 'Parameters',
      token: '',
      details: ['Select a parameter from the Parameters sidebar.'],
    }
  }

  const parameterType = selectedParameter.type || 'list'
  const parameterName = selectedParameter.name || `Parameter ${selectedParameterIdx + 1}`
  const selectedSourceIndex = parameterType === 'file' || parameterType === 'list'
    ? findDataSourceIndexByName(script.data_sources, selectedParameter.data_source)
    : null
  const selectedSource = selectedSourceIndex !== null ? script.data_sources?.[selectedSourceIndex] || null : null
  const fileName = getDataSourceFileName(selectedSource)
  const details = [
    fileName,
    selectedSource?.name && selectedSource.name !== fileName ? `Source ${selectedSource.name}` : '',
    selectedParameter.column ? `Column ${selectedParameter.column}` : '',
  ].filter(Boolean)

  if (details.length === 0) {
    details.push(formatParameterChoiceLabel('type', parameterType))
  }

  return {
    title: `Parameter: ${parameterName}`,
    token: selectedParameter.name ? `{{param.${selectedParameter.name}}}` : '',
    details,
  }
}

export interface ParameterDataDialogProps {
  onAddDataset?: () => void
  onInspectUsage?: (path: string) => void
  /** Show every use of the named parameter in Source. */
  onShowUsesInSource?: (name: string) => void
  /** Further workspace tools (scans, imported parameter files), after Add Parameter. */
  toolbarActions?: ReactNode
  onUndoStructure?: () => void
  canUndoStructure?: boolean
  onMergeSources?: () => void
  mergeSummary?: ParameterSourceMergeSummary
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onAddParameter: (type?: string) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onApplyPendingParameterDraft: () => void
  onAutoFixDataSource: (idx: number) => void
  onClearPendingParameterDraft: () => void
  onClose?: () => void
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
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  onUpdateParameterDraftName: (value: string) => void
  onUpdateParameterDraftValue: (value: string) => void
  onSelectDataSource: (idx: number | null) => void
  onSelectParameter: (idx: number | null) => void
  openDataEditor: number | null
  pendingParameterDraft: ActionEditorParameterDraft | null
  selectedParameterIdx: number | null
  script: ScriptData
  variant?: 'modal' | 'document'
}

export function ParameterDataDialog(props: ParameterDataDialogProps) {
  const {
  dataEditorMode,
  dataSourceHistory,
  onAddDataColumn,
  onAddDataRow,
  onAddParameter,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onApplyPendingParameterDraft,
  onAutoFixDataSource,
  onClearPendingParameterDraft,
  onClose,
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
  onUpdateParameter,
  onUpdateParameterDraftName,
  onUpdateParameterDraftValue,
  onSelectDataSource,
  onSelectParameter,
  openDataEditor,
  pendingParameterDraft,
  selectedParameterIdx,
  script,
  variant = 'modal',
} = props
  const [newParameterType, setNewParameterType] = useState('list')
  const [workspaceView, setWorkspaceView] = useState<'parameters' | 'datasets'>('parameters')
  const [parameterSearch, setParameterSearch] = useState('')
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({
    dialogRef,
    enabled: variant !== 'document',
    onClose,
  })
  const selectedParameter = selectedParameterIdx !== null ? script.parameters?.[selectedParameterIdx] || null : null
  const previousSelectedSourceRef = useRef<{ index: number; source: string } | null>(null)
  const headerDetails = useMemo(
    () => getSelectedParameterHeaderDetails(script, selectedParameter, selectedParameterIdx),
    [script, selectedParameter, selectedParameterIdx],
  )
  useEffect(() => {
    if (openDataEditor === null) return
    const sourceName = script.data_sources?.[openDataEditor]?.name
    const currentParameter = selectedParameterIdx !== null ? script.parameters?.[selectedParameterIdx] : null
    const currentSource = normalizeParameterDataLookup(currentParameter?.data_source)
    const previousSource = previousSelectedSourceRef.current
    previousSelectedSourceRef.current = selectedParameterIdx === null
      ? null
      : { index: selectedParameterIdx, source: currentSource }

    // A data-source edit belongs to the currently selected parameter. Keep that
    // selection and move the open data editor with it; otherwise the old source
    // effect selects that source's first parameter and appears to undo the edit.
    if (currentParameter && previousSource?.index === selectedParameterIdx && previousSource.source !== currentSource) {
      const nextSourceIndex = findDataSourceIndexByName(script.data_sources, currentParameter.data_source)
      if (nextSourceIndex !== null && nextSourceIndex !== openDataEditor) onSelectDataSource(nextSourceIndex)
      return
    }

    const parameterIndex = findFirstParameterIndexByDataSource(script.parameters, sourceName)
    if (parameterIndex === null) return
    if (currentParameter && normalizeParameterDataLookup(currentParameter.data_source) === normalizeParameterDataLookup(sourceName)) {
      return
    }
    onSelectParameter(parameterIndex)
  }, [onSelectDataSource, onSelectParameter, openDataEditor, script.data_sources, script.parameters, selectedParameterIdx])
  useEffect(() => {
    if (selectedParameterIdx === null) return
    if (script.parameters?.[selectedParameterIdx]) return
    onSelectParameter(null)
  }, [onSelectParameter, script.parameters, selectedParameterIdx])
  useEffect(() => {
    if (selectedParameterIdx !== null) return
    if ((script.parameters?.length || 0) === 0) return
    onSelectParameter(0)
  }, [onSelectParameter, script.parameters, selectedParameterIdx])

  const dialog = (
      <div
        ref={dialogRef}
        className={`editor-dialog parameter-data-dialog ${variant === 'document' ? 'parameter-data-document' : ''}`}
        role={variant === 'document' ? 'region' : 'dialog'}
        aria-modal={variant === 'document' ? undefined : true}
        aria-label="Parameters"
      >
        <div className="parameter-workspace-toolbar">
          <div><h3>Parameters &amp; datasets</h3><p>Define values, allocate rows, and inspect where each parameter is used.</p></div>
          <div className="parameter-workspace-actions">
            <button type="button" className="btn btn-sm btn-secondary" aria-pressed={workspaceView === 'parameters'} onClick={() => setWorkspaceView('parameters')}>Parameters</button>
            <button type="button" className="btn btn-sm btn-secondary" aria-pressed={workspaceView === 'datasets'} onClick={() => setWorkspaceView('datasets')}>Datasets</button>
            <button type="button" className="btn btn-sm btn-secondary" disabled={!props.canUndoStructure} onClick={props.onUndoStructure}>Undo structural edit</button>
            <label>New parameter type<select aria-label="New parameter type" value={newParameterType} onChange={event => setNewParameterType(event.target.value)}>{PARAMETER_CHOICE_DEFINITIONS.type.options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>
            <button type="button" className="btn btn-sm btn-primary" onClick={() => { setWorkspaceView('parameters'); onAddParameter(newParameterType) }}>+ Add Parameter</button>
            {props.toolbarActions}
          </div>
        </div>
        {workspaceView === 'datasets' ? <ParameterDatasetWorkspace {...props} onShowParameter={index => { onSelectParameter(index); setWorkspaceView('parameters') }} /> : <div className="parameter-workspace-layout">
          <nav className="parameter-workspace-nav" aria-label="Parameter selection">
            <label>Find parameter<input type="search" autoComplete="off" value={parameterSearch} onChange={event => setParameterSearch(event.target.value)} /></label>
            {(script.parameters || []).map((parameter, index) => ({ parameter, index })).filter(({ parameter }) => `${parameter.name} ${parameter.type} ${parameter.data_source || ''}`.toLowerCase().includes(parameterSearch.toLowerCase())).map(({ parameter, index }) => (
              <button type="button" aria-current={index === selectedParameterIdx ? 'true' : undefined} key={index} onClick={() => {
                onSelectParameter(index)
                onSelectDataSource(findDataSourceIndexByName(script.data_sources, parameter.data_source))
              }}>
                <strong>{parameter.name}</strong><small>{parameter.type} · {parameterUsages(script, parameter.name).length} references</small>
                <small>{parameter.data_source || 'Generated / inline value'}</small>{parameter.data_source && findDataSourceIndexByName(script.data_sources, parameter.data_source) === null && <small role="status">Missing dataset</small>}
              </button>
            ))}
            {!(script.parameters || []).some(parameter => `${parameter.name} ${parameter.type} ${parameter.data_source || ''}`.toLowerCase().includes(parameterSearch.toLowerCase())) && <p>No matching parameters.</p>}
          </nav>
          <div className="parameter-workspace-detail">
        <div className="editor-dialog-header">
          <div className="parameter-data-selected-heading">
            <h3 className="panel-title">
              <span>{headerDetails.title}</span>
              {headerDetails.token && <span className="cell-mono">{headerDetails.token}</span>}
            </h3>
            <p className="editor-dialog-subtitle">{headerDetails.details.join(' · ')}</p>
          </div>
          {(onClose || (props.onShowUsesInSource && selectedParameter?.name)) && (
            <div className="parameter-dialog-actions">
              {props.onShowUsesInSource && selectedParameter?.name && <button type="button" className="btn btn-sm btn-secondary" onClick={() => props.onShowUsesInSource?.(selectedParameter.name)}>Show uses in Source</button>}
              {onClose && <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>}
            </div>
          )}
        </div>
        <div className="editor-dialog-body">
          {pendingParameterDraft && (
            <ParameterDraftPanel
              draft={pendingParameterDraft}
              onApplyPendingParameterDraft={onApplyPendingParameterDraft}
              onClearPendingParameterDraft={onClearPendingParameterDraft}
              onUpdateParameterDraftName={onUpdateParameterDraftName}
              onUpdateParameterDraftValue={onUpdateParameterDraftValue}
            />
          )}
          <ParameterDataDetail
            onInspectUsage={props.onInspectUsage}
            dataEditorMode={dataEditorMode}
            dataSourceHistory={dataSourceHistory}
            onAddParameter={onAddParameter}
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
            onUpdateParameter={(field, value) => {
              if (selectedParameterIdx === null) return
              onUpdateParameter(selectedParameterIdx, field, value)
              if (field === 'data_source') {
                onSelectDataSource(findDataSourceIndexByName(script.data_sources, String(value || '')))
              }
            }}
            selectedParameterIdx={selectedParameterIdx}
            selectedParameter={selectedParameter}
            script={script}
          />
        </div>
        </div></div>}
      </div>
  )

  if (variant === 'document') return dialog

  return (
    <DialogLayer><div className="editor-dialog-backdrop" {...(onClose ? dialogBackdropProps(onClose) : {})}>
      <div>
        {dialog}
      </div>
    </div></DialogLayer>
  )
}

interface ParameterDataDetailProps {
  onInspectUsage?: (path: string) => void
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onAddParameter: () => void
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
  onUpdateParameter: (field: keyof ParameterConfig, value: unknown) => void
  script: ScriptData
  selectedParameter: ParameterConfig | null
  selectedParameterIdx: number | null
}

function ParameterDataDetail({
  onInspectUsage,
  dataEditorMode,
  dataSourceHistory,
  onAddDataColumn,
  onAddDataRow,
  onAddParameter,
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
  onUpdateParameter,
  script,
  selectedParameter,
  selectedParameterIdx,
}: ParameterDataDetailProps) {
  const [detailTab, setDetailTab] = useState<'info' | 'data' | 'usage'>('info')
  if (!selectedParameter || selectedParameterIdx === null) {
    return (
      <div className="parameter-detail-panel empty">
        <div className="parameter-empty-state">
          Select a parameter from the Parameters sidebar, or add one to start configuring runtime data.
        </div>
        <button type="button" className="btn btn-sm btn-primary" onClick={onAddParameter}>+ Add Parameter</button>
      </div>
    )
  }

  const parameterType = selectedParameter.type || 'list'
  const parameterName = selectedParameter.name || `Parameter ${selectedParameterIdx + 1}`
  const isDataBacked = parameterType === 'file' || parameterType === 'list'
  const selectedSourceIndex = isDataBacked
    ? findDataSourceIndexByName(script.data_sources, selectedParameter.data_source)
    : null
  const selectedSource = selectedSourceIndex !== null ? script.data_sources?.[selectedSourceIndex] || null : null
  const selectedParsed = selectedSource ? parseInlineData(selectedSource) : null
  const selectedSourceSummary = selectedSource ? summarizeInlineData(selectedSource) : null
  const selectedSourceWarnings = selectedSource ? validateInlineData(selectedSource) : []
  const selectedColumnIndexes = selectedParsed ? getParameterDataColumnIndexes(selectedParameter, selectedParsed.columns) : []
  const externalSourceReference = getExternalDataSourceReference(selectedSource)
  const hasInlineListValues = parameterType === 'list' && (selectedParameter.values?.length || 0) > 0 && !selectedParameter.data_source?.trim()

  const renderDataSourceDetail = () => {
    if (!isDataBacked) return null
    if (hasInlineListValues) {
      return (
        <div className="parameter-empty-state">
          {parameterName} uses inline list values. Edit those values in the Data Mapping section above.
        </div>
      )
    }
    if (!selectedParameter.data_source?.trim()) {
      return (
        <div className="parameter-empty-state">
          {parameterName} is not mapped to a data source yet. Set Data Source and Column above to display file data.
        </div>
      )
    }
    if (!selectedSource || selectedSourceIndex === null || !selectedParsed || !selectedSourceSummary) {
      return (
        <div className="parameter-empty-state">
          The mapped data source for {parameterName} could not be found. Update the Data Source field above.
        </div>
      )
    }
    if (externalSourceReference) {
      return (
        <div className="parameter-empty-state">
          Data lives in <span className="cell-mono">{externalSourceReference}</span>; contents are not available for preview.
        </div>
      )
    }
    if (selectedColumnIndexes.length === 0) {
      return (
        <div className="parameter-empty-state">
          {parameterName} is mapped to {selectedParameter.data_source}, but its Column does not match this file. Update the Column field above.
        </div>
      )
    }

    return (
      <div className="parameter-data-selected-view">
        <p className="parameter-scope-note">Shared dataset: {selectedSource.name}. Edits affect {(script.parameters || []).filter(parameter => normalizeParameterDataLookup(parameter.data_source) === normalizeParameterDataLookup(selectedSource.name)).length} parameters. Selected column: <strong>{selectedParameter.column}</strong>. Open Datasets for a wider view.</p>
        <ParameterDataEditor
          dataEditorMode={dataEditorMode[selectedSourceIndex] || 'grid'}
          history={dataSourceHistory[selectedSourceIndex]}
          index={selectedSourceIndex}
          key={`${selectedSource.name || 'source'}-${selectedSourceIndex}`}
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
          parsed={selectedParsed}
          highlightedColumnIndexes={selectedColumnIndexes}
          selectedParameterName={parameterName}
          showHeaderTitle={false}
          source={selectedSource}
          summary={selectedSourceSummary}
          warnings={selectedSourceWarnings}
        />
      </div>
    )
  }

  const activeTab = detailTab === 'usage' ? 'usage' : detailTab === 'data' && isDataBacked ? 'data' : 'info'

  return (
    <div className="parameter-detail-panel">
      {(
        <TabList className="parameter-detail-tabs" role="tablist" aria-label="Parameter detail tabs" onKeyDown={handleTabKey}>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'info'}
            className={activeTab === 'info' ? 'active' : ''}
            onClick={() => setDetailTab('info')} tabIndex={(activeTab === 'info') ? 0 : -1}
          >
            Settings
          </button>
          {isDataBacked && <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'data'}
            className={activeTab === 'data' ? 'active' : ''}
            onClick={() => setDetailTab('data')} tabIndex={(activeTab === 'data') ? 0 : -1}
          >
            Data
          </button>}
          <button type="button" role="tab" aria-selected={activeTab === 'usage'} tabIndex={activeTab === 'usage' ? 0 : -1} onClick={() => setDetailTab('usage')}>Usage</button>
        </TabList>
      )}
      {activeTab === 'usage' && <section role="tabpanel" aria-label="Parameter usage"><h4>Usage of <code>{`{{param.${parameterName}}}`}</code></h4><p>Renaming updates these executable references together.</p><ul>{parameterUsages(script, parameterName).map((usage, index) => <li key={index}>{onInspectUsage ? <button type="button" className="btn btn-sm btn-ghost" onClick={() => onInspectUsage(usage.path)}><code>{usage.path}</code></button> : <code>{usage.path}</code>}</li>)}</ul>{!parameterUsages(script, parameterName).length && <p>This parameter is not referenced by the script yet.</p>}</section>}
      <section className="parameter-detail-info" aria-label="Parameter settings" role="tabpanel" hidden={activeTab !== 'info'}>
        <ParameterValuePreview key={selectedParameterIdx} script={script} name={parameterName} />
        <ParameterInfoPanel
          columnIndexes={selectedColumnIndexes}
          dataSources={script.data_sources || []}
          onUpdateDataSource={onUpdateDataSource}
          onUpdateParameter={onUpdateParameter}
          parameter={selectedParameter}
          parameters={script.parameters || []}
          parsed={selectedParsed}
          selectedParameterIdx={selectedParameterIdx}
          selectedSource={selectedSource}
          selectedSourceIndex={selectedSourceIndex}
          summary={selectedSourceSummary}
        />
      </section>
      {isDataBacked && (
        <section className="parameter-detail-data" aria-label="Parameter data" role="tabpanel" hidden={activeTab !== 'data'}>
          {renderDataSourceDetail()}
        </section>
      )}
    </div>
  )
}

interface ParameterInfoPanelProps {
  columnIndexes: number[]
  dataSources: DataSourceConfig[]
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  onUpdateParameter: (field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
  parameters: ParameterConfig[]
  parsed: { columns: string[]; rows: string[][] } | null
  selectedParameterIdx: number | null
  selectedSource: DataSourceConfig | null
  selectedSourceIndex: number | null
  summary: { rows: number; columns: number } | null
}

function ParameterInfoPanel({
  columnIndexes,
  dataSources,
  onUpdateDataSource,
  onUpdateParameter,
  parameter,
  parameters,
  parsed,
  selectedParameterIdx,
  selectedSource,
  selectedSourceIndex,
  summary,
}: ParameterInfoPanelProps) {
  const parameterName = parameter.name || 'Parameter'
  const parameterType = parameter.type || 'list'
  const isDataBacked = parameterType === 'file' || parameterType === 'list'
  const isGenerated = ['random_number', 'counter', 'current_iteration', 'vuser_id', 'random_string', 'uuid'].includes(parameterType)
  const isTime = parameterType === 'date_time' || parameterType === 'timestamp' || parameterType === 'time'
  const showsDataMapping = isDataBacked || parameterType === 'host'
  const showsTypeOptions = isGenerated || isTime
  const showsOutput = parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number' || parameter.seed !== undefined
  const hasSource = Boolean(selectedSource && selectedSourceIndex !== null)
  const showsStats = isDataBacked && hasSource && Boolean(parsed && summary)
  const statsUnavailable = Boolean(getExternalDataSourceReference(selectedSource))
  const uniqueCount = parsed ? countUniqueColumnValues(parsed, columnIndexes) : 0
  const mappedColumnLabel = parsed && columnIndexes.length
    ? columnIndexes.map(idx => formatDataSourceColumnLabel(parsed.columns[idx], idx)).join(', ')
    : '—'

  const dataSourceNames = (dataSources || []).map(ds => ds.name).filter(Boolean)
  const currentDataSource = parameter.data_source || ''
  const dataSourceOptions = currentDataSource && !dataSourceNames.includes(currentDataSource)
    ? [currentDataSource, ...dataSourceNames]
    : dataSourceNames
  const columnNames = parsed?.columns || []
  const currentColumn = parameter.column || ''
  const columnOptions = currentColumn && !columnNames.includes(currentColumn)
    ? [currentColumn, ...columnNames]
    : columnNames
  const sourceFileLabel = selectedSource?.path || selectedSource?.storage_ref || selectedSource?.staged_asset || selectedSource?.name || currentDataSource || '—'
  const sameLineOptions = getSameLineParameterOptions(parameters, selectedParameterIdx)
  const sameLineSelectOptions = getSameLineParameterSelectOptions(parameter, sameLineOptions)
  const selectionControlValue = getParameterSelectionControlValue(parameter)

  const handleStrategyChange = (value: string) => {
    if (value === SAME_ROW_SELECTION_VALUE) {
      const nextSameLine = sameLineSelectOptions[0]?.value || ''
      onUpdateParameter('same_line_as', nextSameLine || undefined)
      return
    }
    if (parameter.same_line_as) onUpdateParameter('same_line_as', undefined)
    onUpdateParameter('selection', value)
  }

  return (
    <div className="parameter-info-panel">
      <section className="parameter-info-group">
        <h5 className="parameter-info-group-title">Identity</h5>
        <table className="parameter-info-table">
          <tbody>
            <tr>
              <th scope="row">Parameter Name</th>
              <td><ParameterRenameInput key={parameter.name} name={parameter.name} onRename={name => onUpdateParameter('name', name)} /></td>
            </tr>
            <tr>
              <th scope="row">Parameter Type</th>
              <td>
                <ParameterTypeReadonlyValue parameterName={parameterName} parameterType={parameterType} />
              </td>
            </tr>
            <tr>
              <th scope="row">Template Token</th>
              <td><span className="parameter-info-readonly cell-mono">{parameter.name ? `{{param.${parameter.name}}}` : 'Name this parameter to generate a template token'}</span></td>
            </tr>
          </tbody>
        </table>
      </section>

      <section className="parameter-info-group">
        <h5 className="parameter-info-group-title">Behavior</h5>
        <table className="parameter-info-table">
          <tbody>
            <tr>
              <th scope="row">Update Cadence</th>
              <td>
                <select aria-label={`Update Cadence for ${parameterName}`} value={getParameterChoiceValue(parameter, 'update')} onChange={event => onUpdateParameter('update', event.target.value)}>
                  {PARAMETER_CHOICE_DEFINITIONS.update.options.map(option => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
                <p className="parameter-info-help">{PARAMETER_CHOICE_DEFINITIONS.update.options.find(option => option.value === getParameterChoiceValue(parameter, 'update'))?.description}</p>
              </td>
            </tr>
            {isDataBacked && (
              <>
                <tr>
                  <th scope="row">Iteration Strategy</th>
                  <td>
                    <select aria-label={`Iteration Strategy for ${parameterName}`} value={selectionControlValue} onChange={event => handleStrategyChange(event.target.value)}>
                      {getParameterSelectionOptions(true).map(option => (
                        <option
                          disabled={option.value === SAME_ROW_SELECTION_VALUE && sameLineSelectOptions.length === 0}
                          key={option.value}
                          value={option.value}
                        >
                          {option.label}
                        </option>
                      ))}
                    </select>
                  </td>
                </tr>
                {selectionControlValue === SAME_ROW_SELECTION_VALUE && (
                  <tr>
                    <th scope="row">Same Row As</th>
                    <td>
                      <select
                        aria-label={`Same Row As for ${parameterName}`}
                        disabled={sameLineSelectOptions.length === 0}
                        value={parameter.same_line_as || ''}
                        onChange={event => onUpdateParameter('same_line_as', event.target.value || undefined)}
                      >
                        {sameLineSelectOptions.length === 0 ? (
                          <option value="">No compatible parameters</option>
                        ) : (
                          sameLineSelectOptions.map(option => (
                            <option key={option.value} value={option.value}>{option.label}</option>
                          ))
                        )}
                      </select>
                      {sameLineSelectOptions.length === 0 && (
                        <p className="parameter-info-help">Add another file or list parameter mapped to this data source to link row selection.</p>
                      )}
                    </td>
                  </tr>
                )}
                <tr>
                  <th scope="row">On Exhaustion</th>
                  <td>
                    <select aria-label={`On Exhaustion for ${parameterName}`} value={getParameterChoiceValue(parameter, 'on_exhaustion')} onChange={event => onUpdateParameter('on_exhaustion', event.target.value)}>
                      {PARAMETER_CHOICE_DEFINITIONS.on_exhaustion.options.map(option => (
                        <option key={option.value} value={option.value}>{option.label}</option>
                      ))}
                    </select>
                  </td>
                </tr>
              </>
            )}
          </tbody>
        </table>
      </section>

      {showsDataMapping && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Data Mapping</h5>
          <table className="parameter-info-table">
            <tbody>
              {isDataBacked && (
                <>
                  <tr>
                    <th scope="row">Data Source</th>
                    <td>
                      {dataSourceOptions.length > 0 ? (
                        <select aria-label={`Data Source for ${parameterName}`} value={currentDataSource} onChange={event => onUpdateParameter('data_source', event.target.value)}>
                          <option value="">— Select source —</option>
                          {dataSourceOptions.map(name => (
                            <option key={name} value={name}>{name}</option>
                          ))}
                        </select>
                      ) : (
                        <input aria-label={`Data Source for ${parameterName}`} value={currentDataSource} onChange={event => onUpdateParameter('data_source', event.target.value)} placeholder="users" />
                      )}
                    </td>
                  </tr>
                  <tr>
                    <th scope="row">Column</th>
                    <td>
                      {columnOptions.length > 0 ? (
                        <select aria-label={`Column for ${parameterName}`} value={currentColumn} onChange={event => onUpdateParameter('column', event.target.value)}>
                          <option value="">— Select column —</option>
                          {columnOptions.map(name => (
                            <option key={name} value={name}>{name}</option>
                          ))}
                        </select>
                      ) : (
                        <input aria-label={`Column for ${parameterName}`} value={currentColumn} onChange={event => onUpdateParameter('column', event.target.value)} placeholder="column name" />
                      )}
                    </td>
                  </tr>
                  <tr>
                    <th scope="row">Source file</th>
                    <td><span className="parameter-info-readonly cell-mono">{sourceFileLabel}</span></td>
                  </tr>
                </>
              )}
              {(parameterType === 'list' || parameterType === 'host') && (
                <tr>
                  <th scope="row">{parameterType === 'host' ? 'Host Overrides' : 'Inline Values'}</th>
                  <td>
                    <textarea
                      aria-label={`${parameterType === 'host' ? 'Host Overrides' : 'Inline Values'} for ${parameterName}`}
                      value={(parameter.values || []).join('\n')}
                      onChange={event => onUpdateParameter('values', parseParameterListInput(event.target.value))}
                      placeholder={parameterType === 'host' ? 'host-a\nhost-b' : 'value1\nvalue2'}
                    />
                  </td>
                </tr>
              )}
            </tbody>
          </table>
          {isDataBacked && selectedSource && selectedSourceIndex !== null && (
            <div className="parameter-data-file-controls parameter-info-file-controls">
              <label>
                <span>File Type</span>
                <select aria-label={`File Type for ${parameterName}`} value={selectedSource.type || 'csv'} onChange={event => onUpdateDataSource(selectedSourceIndex, 'type', event.target.value)}>
                  <option value="csv">csv</option>
                  <option value="dat">dat</option>
                </select>
              </label>
              <label>
                <span>Delimiter</span>
                <select aria-label={`Delimiter for ${parameterName}`} value={getDataSourceDelimiterChoice(selectedSource)} onChange={event => onUpdateDataSource(selectedSourceIndex, 'delimiter', getDataSourceDelimiterValue(event.target.value as DataSourceDelimiterChoice), true)}>
                  <option value="auto">Auto ({inferDelimiter(selectedSource) === '\t' ? 'Tab' : 'Comma'})</option>
                  <option value="comma">Comma</option>
                  <option value="tab">Tab</option>
                </select>
              </label>
              <label className="checkbox-label compact parameter-data-header-toggle">
                <input
                  type="checkbox"
                  aria-label={`First row is header for ${parameterName}`}
                  checked={selectedSource.first_row_header !== false}
                  onChange={event => onUpdateDataSource(selectedSourceIndex, 'first_row_header', event.target.checked)}
                />
                <span>{selectedSource.first_row_header !== false ? 'First row is header' : 'No header row'}</span>
              </label>
            </div>
          )}
        </section>
      )}

      {showsTypeOptions && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Type Options</h5>
          <table className="parameter-info-table">
            <tbody>
              {parameterType === 'random_number' && (
                <>
                  <tr><th scope="row">Minimum</th><td><input aria-label={`Minimum for ${parameterName}`} type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdateParameter('min', Number(event.target.value))} placeholder="min" /></td></tr>
                  <tr><th scope="row">Maximum</th><td><input aria-label={`Maximum for ${parameterName}`} type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdateParameter('max', Number(event.target.value))} placeholder="max" /></td></tr>
                </>
              )}
              {(parameterType === 'counter' || parameterType === 'current_iteration') && (
                <>
                  <tr><th scope="row">Start</th><td><input aria-label={`Start for ${parameterName}`} type="number" value={numberInputValue(parameter.start, parameterType === 'current_iteration' ? 1 : 0)} onChange={event => onUpdateParameter('start', Number(event.target.value))} placeholder="start" /></td></tr>
                  <tr><th scope="row">End</th><td><input aria-label={`End for ${parameterName}`} type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdateParameter('end', Number(event.target.value))} placeholder="end (optional)" /></td></tr>
                  <tr><th scope="row">Step</th><td><input aria-label={`Step for ${parameterName}`} type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdateParameter('step', Number(event.target.value))} placeholder="step" /></td></tr>
                </>
              )}
              {parameterType === 'random_string' && (
                <>
                  <tr><th scope="row">Length</th><td><input aria-label={`Length for ${parameterName}`} type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdateParameter('length', Number(event.target.value))} placeholder="length" /></td></tr>
                  <tr><th scope="row">Charset</th><td><input aria-label={`Charset for ${parameterName}`} value={parameter.charset || ''} onChange={event => onUpdateParameter('charset', event.target.value)} placeholder="charset override" /></td></tr>
                </>
              )}
              {parameterType === 'uuid' && (
                <tr><td colSpan={2}><p className="parameter-settings-empty">UUID values are generated automatically using the selected update cadence.</p></td></tr>
              )}
              {parameterType === 'vuser_id' && (
                <tr><td colSpan={2}><p className="parameter-settings-empty">The current virtual user identifier is supplied automatically.</p></td></tr>
              )}
              {isTime && (
                <>
                  <tr>
                    <th scope="row">Format</th>
                    <td>
                      <ParameterFormatControl parameter={parameter} onUpdate={onUpdateParameter} />
                    </td>
                  </tr>
                  <tr><th scope="row">Timezone</th><td><input aria-label={`Timezone for ${parameterName}`} value={parameter.timezone || ''} onChange={event => onUpdateParameter('timezone', event.target.value)} placeholder="UTC, America/New_York" /></td></tr>
                  <tr><th scope="row">Offset Seconds</th><td><input aria-label={`Offset Seconds for ${parameterName}`} type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdateParameter('offset_seconds', Number(event.target.value))} placeholder="offset seconds" /></td></tr>
                </>
              )}
            </tbody>
          </table>
        </section>
      )}

      {showsOutput && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Output &amp; Reproducibility</h5>
          <table className="parameter-info-table">
            <tbody>
              {(parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number') && (
                <>
                  <tr><th scope="row">Pad Width</th><td><input aria-label={`Pad Width for ${parameterName}`} type="number" value={numberInputValue(parameter.pad_width, 0)} onChange={event => onUpdateParameter('pad_width', Number(event.target.value))} placeholder="pad width" /></td></tr>
                  <tr><th scope="row">Pad Character</th><td><input aria-label={`Pad Character for ${parameterName}`} value={parameter.pad_char || ''} onChange={event => onUpdateParameter('pad_char', event.target.value)} placeholder="pad char" /></td></tr>
                </>
              )}
              <tr>
                <th scope="row">Seed</th>
                <td>
                  <input
                    aria-label={`Seed for ${parameterName}`}
                    type="number"
                    value={numberInputValue(parameter.seed, '')}
                    onChange={event => updateOptionalNumber(onUpdateParameter, 'seed', event.target.value)}
                    placeholder="seed (optional)"
                  />
                </td>
              </tr>
            </tbody>
          </table>
        </section>
      )}

      {isDataBacked && <section className="parameter-info-group">
        <h5>Allocation</h5>
        <p>Rows are allocated within the generator's data shard. Row numbers below start at 1.</p>
        <div className="form-grid">
          <label>Start row<input aria-label="Start row" type="number" min="1" value={parameter.start_row ?? 1} onChange={event => onUpdateParameter('start_row', Math.max(1, Number(event.target.value) || 1))} /></label>
          <label>Rows per virtual user<input aria-label="Rows per virtual user" type="number" min="0" value={parameter.block_size ?? 0} onChange={event => onUpdateParameter('block_size', Math.max(0, Number(event.target.value) || 0))} /><small>0 uses the available source rows.</small></label>
          <label className="checkbox-label"><input type="checkbox" disabled={Boolean(parameter.block_size)} checked={parameter.value_for_each_vuser ?? false} onChange={event => onUpdateParameter('value_for_each_vuser', event.target.checked)} />One stable row per virtual user</label>
        </div>
        {Boolean(parameter.block_size) && <p>The row block takes precedence over one stable row per virtual user.</p>}
      </section>}
      {showsDataMapping && selectedSource && <p className="parameter-scope-note">File type, delimiter, and header options affect all {parameters.filter(candidate => normalizeParameterDataLookup(candidate.data_source) === normalizeParameterDataLookup(selectedSource.name)).length} parameters using this dataset.</p>}
      {showsStats && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Data Statistics</h5>
          <table className="parameter-info-table parameter-info-stats-table">
            <tbody>
              <tr><th scope="row">Record Count</th><td className="cell-mono parameter-info-stat-value">{statsUnavailable ? '—' : (summary?.rows ?? 0)}</td></tr>
              <tr><th scope="row">Distinct mapped values</th><td className="cell-mono parameter-info-stat-value">{statsUnavailable ? '—' : uniqueCount}</td></tr>
              <tr><th scope="row">Columns</th><td className="cell-mono parameter-info-stat-value">{statsUnavailable ? '—' : (summary?.columns ?? 0)}</td></tr>
              <tr><th scope="row">Mapped Column</th><td className="cell-mono">{mappedColumnLabel}</td></tr>
            </tbody>
          </table>
        </section>
      )}
    </div>
  )
}

interface ParameterSidebarProps {
  mergeSummary: ParameterSourceMergeSummary
  onAddParameter: () => void
  onMergeParameterSources: () => void
  onSelectParameter: (idx: number) => void
  parameters: ParameterConfig[]
  selectedParameterIdx: number | null
}

export function ParameterSidebar({
  mergeSummary,
  onAddParameter,
  onMergeParameterSources,
  onSelectParameter,
  parameters,
  selectedParameterIdx,
}: ParameterSidebarProps) {
  const groups = useMemo(() => {
    const orderedTypes = PARAMETER_CHOICE_DEFINITIONS.type.options.map(option => option.value)
    const typeLabels = new Map(PARAMETER_CHOICE_DEFINITIONS.type.options.map(option => [option.value, option.label]))
    const grouped = new Map<string, Array<{ index: number; parameter: ParameterConfig }>>()
    parameters.forEach((parameter, index) => {
      const type = parameter.type || 'list'
      const bucket = grouped.get(type) || []
      bucket.push({ index, parameter })
      grouped.set(type, bucket)
    })
    const knownGroups = orderedTypes
      .map(type => ({ type, label: typeLabels.get(type) || type, parameters: grouped.get(type) || [] }))
      .filter(group => group.parameters.length > 0)
    const unknownGroups = Array.from(grouped.entries())
      .filter(([type]) => !orderedTypes.includes(type))
      .map(([type, items]) => ({ type, label: type, parameters: items }))
    return [...knownGroups, ...unknownGroups]
  }, [parameters])

  return (
    <div className="utility-card parameter-sidebar-card">
      <div className="utility-card-header">
        <strong>Parameters</strong>
      </div>
      <div className="parameter-sidebar-actions">
        <button type="button" className="btn btn-sm btn-primary" onClick={onAddParameter}>+ Add Parameter</button>
        <button
          className="btn btn-sm btn-ghost"
          disabled={!mergeSummary.canMerge}
          onClick={onMergeParameterSources}
          title={mergeSummary.canMerge
            ? `Merge ${mergeSummary.sourceCount} source files for ${mergeSummary.parameterCount} parameters`
            : 'Requires at least two compatible .dat or .csv sources with equal row counts and no unbound or directly referenced columns'}
          type="button"
        >
          Merge Source Files
        </button>
      </div>
      {parameters.length === 0 ? (
        <div className="utility-empty-state">No parameters defined yet.</div>
      ) : (
        <div className="parameter-sidebar-list" role="listbox" aria-label="Parameters">
          {groups.map(group => (
            <section className="parameter-sidebar-group" key={group.type}>
              <div className="parameter-sidebar-group-title">
                <strong>{group.label}</strong>
                <span>{group.parameters.length}</span>
              </div>
              {group.parameters.map(({ index, parameter }) => {
                const selected = selectedParameterIdx === index
                const parameterName = parameter.name || `Parameter ${index + 1}`
                const sourceSummary = parameter.type === 'file' || parameter.type === 'list'
                  ? [parameter.data_source, parameter.column].filter(Boolean).join(' / ') || 'No source mapped'
                  : summarizeParameterSettings(parameter)
                return (
                  <button
                    type="button"
                    className={`parameter-sidebar-item ${selected ? 'selected' : ''}`}
                    aria-selected={selected}
                    key={`${parameterName}-${index}`}
                    onClick={() => onSelectParameter(index)}
                    role="option"
                  >
                    <span>
                      <strong>{parameterName}</strong>
                      <small>{sourceSummary}</small>
                    </span>
                    <span className="cell-mono">{parameter.name ? `{{param.${parameter.name}}}` : 'unnamed'}</span>
                  </button>
                )
              })}
            </section>
          ))}
        </div>
      )}
    </div>
  )
}

interface ParameterDraftPanelProps {
  draft: ActionEditorParameterDraft
  onApplyPendingParameterDraft: () => void
  onClearPendingParameterDraft: () => void
  onUpdateParameterDraftName: (value: string) => void
  onUpdateParameterDraftValue: (value: string) => void
}

function ParameterDraftPanel({
  draft,
  onApplyPendingParameterDraft,
  onClearPendingParameterDraft,
  onUpdateParameterDraftName,
  onUpdateParameterDraftValue,
}: ParameterDraftPanelProps) {
  return (
    <div className="parameter-draft-panel">
      <div className="panel-title-row" style={{ marginBottom: 8 }}>
        <div>
          <strong style={{ display: 'block', marginBottom: 4 }}>Parameterize Selection</strong>
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
            {draft.source === 'json'
              ? 'This selection came from Source. Add it as a parameter, then pick the parameter type (data file, time, random, uuid, …) below.'
              : 'This selection came from the Action editor. Review it here with the rest of the parameter configuration.'}
          </span>
        </div>
      </div>
      <div className="parameter-draft-grid">
        <label>
          <span>Name</span>
          <input
            value={draft.requestedName}
            onChange={event => onUpdateParameterDraftName(event.target.value)}
            placeholder="param_name"
          />
        </label>
        <label>
          <span>Value</span>
          <input
            value={draft.seedValue}
            onChange={event => onUpdateParameterDraftValue(event.target.value)}
            placeholder="Selected value"
          />
        </label>
      </div>
      <div className="parameter-draft-actions">
        <button type="button" className="btn btn-sm btn-primary" onClick={onApplyPendingParameterDraft} disabled={!draft.requestedName.trim()}>
          Add parameter
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={onClearPendingParameterDraft}>
          Cancel
        </button>
      </div>
    </div>
  )
}

interface AutoParameterizationSidebarProps {
  activeCandidateId: string | null
  activeOccurrenceIndex: number
  activeOccurrenceLabel: string
  candidateMatchCount: number
  onApplyAutoParameterization: () => void
  onNextOccurrence: () => void
  onPreviousOccurrence: () => void
  onSelectCandidate: (id: string) => void
  onUpdateParameterCandidate: (id: string, patch: Partial<ParameterCandidate>) => void
  parameterCandidates: ParameterCandidate[]
  seedRows: number
  onSeedRowsChange: (rows: number) => void
}

interface SelectedParameterDetailsProps {
  parameter: ParameterConfig
  referenceLines: number[]
  usages?: { path: string; label?: string; line: number }[]
  dataInfo: { fileName: string; rows: number | null; unique: number | null } | null
  onJumpToLine: (line: number) => void
  onEditInDataTab: () => void
}

export function SelectedParameterDetails({ parameter, referenceLines, usages, dataInfo, onJumpToLine, onEditInDataTab }: SelectedParameterDetailsProps) {
  return <section className="selected-parameter-details" aria-label={`Parameter ${parameter.name}`}>
    <h3 tabIndex={-1} data-inspector-focus>{parameter.name} <small>{parameter.type}</small></h3>
    <p>Current draft definition</p>
    <dl className="sidebar-definition">
      <dt>Value source</dt><dd>{parameter.data_source ? `${parameter.data_source}.${parameter.column || ''}` : parameter.values?.length ? `${parameter.values.length} inline values` : `${parameter.type} generator`}</dd>
      <dt>Refresh cadence</dt><dd>{parameter.update === 'once' ? 'Once per virtual user' : parameter.update === 'each_occurrence' ? 'Every template reference' : 'Each iteration'}</dd>
      <dt>Allocation</dt><dd>{parameter.same_line_as ? `Same row as ${parameter.same_line_as}` : parameter.selection || 'Sequential'}</dd>
      {dataInfo && <><dt>Dataset</dt><dd>{dataInfo.fileName}{dataInfo.rows !== null ? ` · ${dataInfo.rows} rows` : ''}</dd></>}
    </dl>
    <button type="button" className="btn btn-sm btn-primary" onClick={onEditInDataTab}>Edit in Parameters →</button>
    <h4>Usages ({referenceLines.length})</h4>
    {!referenceLines.length && <p>No references in the current source.</p>}
    {(usages?.length ? usages : referenceLines.map(line => ({ line, path: 'Full script', label: 'Full script' }))).map(({ line, path, label }, index) => <button key={`${path}:${index}`} type="button" className="selected-parameter-ref" onClick={() => onJumpToLine(line)} aria-label={`Open ${parameter.name} usage in ${path} at line ${line}`}>{label || path} · line {line}</button>)}
  </section>
}

export function AutoParameterizationSidebar({
  activeCandidateId,
  activeOccurrenceIndex,
  activeOccurrenceLabel,
  candidateMatchCount,
  onApplyAutoParameterization,
  onNextOccurrence,
  onPreviousOccurrence,
  onSelectCandidate,
  onUpdateParameterCandidate,
  parameterCandidates,
  seedRows,
  onSeedRowsChange,
}: AutoParameterizationSidebarProps) {
  const selectedCount = parameterCandidates.filter(candidate => candidate.selected && sanitizeVarName(candidate.varName).length > 0).length
  const activeCandidate = parameterCandidates.find(candidate => candidate.id === activeCandidateId) || parameterCandidates[0] || null
  const occurrenceCount = activeCandidate?.occurrences.length || 0

  if (parameterCandidates.length === 0) {
    return (
      <div className="utility-card auto-parameter-sidebar-card">
        <div className="utility-card-header">
          <strong>Suggested Fields</strong>
          <span>0</span>
        </div>
        <div className="utility-empty-state">
          No strong parameterization candidates were detected.
        </div>
      </div>
    )
  }

  return (
    <div className="auto-parameter-sidebar">
      <div className="utility-card auto-parameter-sidebar-card">
        <div className="utility-card-header">
          <strong>Suggested Fields</strong>
          <span>{parameterCandidates.length}</span>
        </div>
        <div className="auto-parameter-sidebar-summary">
          <span>{selectedCount} selected</span>
          <span>{parameterCandidates.reduce((total, candidate) => total + candidate.occurrences.length, 0)} occurrence(s)</span>
        </div>
        <div className="auto-parameter-navigation" aria-label="Auto parameterization occurrence navigation">
          <button type="button" className="btn btn-sm btn-ghost" onClick={onPreviousOccurrence} disabled={!activeCandidate || occurrenceCount === 0}>
            Back
          </button>
          <span className="auto-parameter-location">
            {occurrenceCount > 0 ? `${activeOccurrenceIndex + 1} / ${occurrenceCount}` : '0 / 0'}
            <small>{activeOccurrenceLabel}{candidateMatchCount === 0 ? ' · no literal match' : ''}</small>
          </span>
          <button type="button" className="btn btn-sm btn-ghost" onClick={onNextOccurrence} disabled={!activeCandidate || occurrenceCount === 0}>
            Next
          </button>
        </div>
        <label className="auto-parameter-seed-rows">
          <span>Seed rows</span>
          <input
            type="number"
            min={1}
            max={1000}
            value={seedRows}
            onChange={event => {
              const parsed = Number.parseInt(event.target.value, 10)
              onSeedRowsChange(Number.isFinite(parsed) ? Math.min(1000, Math.max(1, parsed)) : 1)
            }}
          />
          <small>First row holds the detected value; fill the rest to vary data per VUser.</small>
        </label>
        <button
          type="button"
          className="btn btn-sm btn-primary auto-parameter-apply"
          onClick={onApplyAutoParameterization}
          disabled={selectedCount === 0}
        >
          Apply Selected
        </button>
      </div>

      <div className="auto-parameter-candidate-list" role="list" aria-label="Suggested parameterization fields">
        {parameterCandidates.map(candidate => {
          const active = candidate.id === activeCandidate?.id
          return (
            <section
              key={candidate.id}
              className={`auto-parameter-candidate ${active ? 'active' : ''}`}
              role="listitem"
            >
              <div className="auto-parameter-candidate-main">
                <label className="auto-parameter-check">
                  <input
                    type="checkbox"
                    checked={candidate.selected}
                    onChange={event => onUpdateParameterCandidate(candidate.id, { selected: event.target.checked })}
                  />
                  <span>Use</span>
                </label>
                <button
                  type="button"
                  className="auto-parameter-candidate-select"
                  onClick={() => onSelectCandidate(candidate.id)}
                >
                  <strong>{candidate.keyHint}</strong>
                  <small>{candidate.occurrences.length} hit{candidate.occurrences.length === 1 ? '' : 's'}</small>
                </button>
              </div>
              <code className="auto-parameter-value">{candidate.value}</code>
              <label className="auto-parameter-name-field">
                <span>Suggested name</span>
                <input
                  value={candidate.varName}
                  onChange={event => onUpdateParameterCandidate(candidate.id, { varName: sanitizeVarName(event.target.value) })}
                  onFocus={() => onSelectCandidate(candidate.id)}
                  placeholder="parameter_name"
                />
              </label>
            </section>
          )
        })}
      </div>
    </div>
  )
}

interface ParameterDefinitionsSectionProps {
  focusedSourceName?: string | null
  isOpen: boolean
  onAddParameter: () => void
  onRemoveParameter: (idx: number) => void
  onSelectParameter: (idx: number, parameter: ParameterConfig) => void
  onToggleOpen: () => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  selectedParameterIdx: number | null
  script: ScriptData
}

interface ParameterDefinitionGroup {
  key: string
  sourceFile: string
  sourceName: string
  sourceType: string
  rowCount: number | null
  hasDataSource: boolean
  parameters: Array<{ index: number; parameter: ParameterConfig }>
}

const PARAMETER_DEFINITION_COLUMN_COUNT = 8

function buildParameterDefinitionGroups(parameters: ParameterConfig[], dataSources: ScriptDataSource[] = []): ParameterDefinitionGroup[] {
  const dataSourceByName = new Map(dataSources.map(source => [source.name, source]))
  const groups: ParameterDefinitionGroup[] = []
  const groupByKey = new Map<string, ParameterDefinitionGroup>()

  parameters.forEach((parameter, index) => {
    const sourceName = (parameter.data_source || '').trim()
    const matchedSource = sourceName ? dataSourceByName.get(sourceName) : undefined
    const sourceFile = (matchedSource?.path || matchedSource?.name || sourceName || formatGeneratedParameterSource(parameter)).trim()
    const sourceType = (matchedSource?.type || parameter.type || 'list').trim()
    // A path/storage_ref/staged_asset source has no inline preview, so its row
    // count is unknown ('—') rather than zero.
    const rowCount = matchedSource && !getExternalDataSourceReference(matchedSource)
      ? summarizeInlineData(matchedSource).rows
      : null
    const key = matchedSource?.name || sourceName || `type:${sourceType || sourceFile}`
    let group = groupByKey.get(key)

    if (!group) {
      group = {
        key,
        sourceFile,
        sourceName: matchedSource?.name || sourceName,
        sourceType,
        rowCount,
        hasDataSource: Boolean(matchedSource),
        parameters: [],
      }
      groupByKey.set(key, group)
      groups.push(group)
    }

    group.parameters.push({ index, parameter })
  })

  return groups
}

function isUserDefinedParameterFileGroup(group: ParameterDefinitionGroup): boolean {
  if (!group.hasDataSource) return false
  const sourceType = (group.sourceType || '').trim().toLowerCase()
  const sourceFile = (group.sourceFile || '').trim().toLowerCase()
  return sourceType === 'csv' || sourceType === 'dat' || sourceFile.endsWith('.csv') || sourceFile.endsWith('.dat')
}

interface ParameterDefinitionSectionGroup {
  key: string
  groups: ParameterDefinitionGroup[]
  summary: string
  title: string
}

function buildParameterDefinitionSections(groups: ParameterDefinitionGroup[]): ParameterDefinitionSectionGroup[] {
  const userDefinedFiles = groups.filter(isUserDefinedParameterFileGroup)
  const builtInTypes = groups.filter(group => !isUserDefinedParameterFileGroup(group))
  return [
    {
      key: 'user-defined-files',
      title: 'User-Defined Files (.dat / .csv)',
      summary: `${userDefinedFiles.length} source${userDefinedFiles.length === 1 ? '' : 's'}`,
      groups: userDefinedFiles,
    },
    {
      key: 'built-in-parameters',
      title: 'Built-In / Generated Parameters',
      summary: `${builtInTypes.length} type${builtInTypes.length === 1 ? '' : 's'}`,
      groups: builtInTypes,
    },
  ].filter(section => section.groups.length > 0)
}

function formatParameterRowCount(rowCount?: number | null): string {
  return rowCount === null || rowCount === undefined ? '—' : String(rowCount)
}

function formatParameterTypeLabel(value?: string | null): string {
  const normalized = (value || 'list').trim()
  switch (normalized) {
    case 'date_time':
      return 'Date/Time'
    case 'current_iteration':
      return 'Current Iteration'
    case 'random_number':
      return 'Random Number'
    case 'random_string':
      return 'Random String'
    default:
      return normalized.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
  }
}

function ParameterTypeReadonlyValue({
  parameterName,
  parameterType,
}: {
  parameterName: string
  parameterType: string
}) {
  return (
    <span
      className="parameter-info-readonly parameter-type-readonly"
      aria-label={`Parameter Type for ${parameterName}: ${formatParameterTypeLabel(parameterType)}`}
      title="Parameter type is set when the parameter is created"
    >
      <strong>{formatParameterTypeLabel(parameterType)}</strong>
      <span className="cell-mono">{parameterType}</span>
    </span>
  )
}

function formatGeneratedParameterSource(parameter: ParameterConfig): string {
  return formatParameterTypeLabel(parameter.type)
}

function normalizeParameterSourceIdentity(value?: string | null): string {
  return (value || '').trim().toLowerCase()
}

function findParameterDefinitionGroupBySource(groups: ParameterDefinitionGroup[], sourceName?: string | null): ParameterDefinitionGroup | null {
  const normalizedSource = normalizeParameterSourceIdentity(sourceName)
  if (!normalizedSource) return null
  return groups.find(group => [
    group.key,
    group.sourceName,
    group.sourceFile,
  ].some(candidate => normalizeParameterSourceIdentity(candidate) === normalizedSource)) || null
}

function summarizeParameterValues(values: string[], fallback = '—'): string {
  const unique = Array.from(new Set(values.map(value => value.trim()).filter(Boolean)))
  if (unique.length === 0) return fallback
  if (unique.length === 1) return unique[0]
  return `${unique.length} values`
}

function summarizeParameterGroupSelection(group: ParameterDefinitionGroup): string {
  return summarizeParameterValues(group.parameters.map(({ parameter }) => {
    if (parameter.type !== 'file' && parameter.type !== 'list') return ''
    const sync = parameter.same_line_as ? `same row as ${parameter.same_line_as}` : ''
    return [sync, `${parameter.selection || 'sequential'} / ${parameter.on_exhaustion || 'wrap'}`]
      .filter(Boolean)
      .join(' · ')
  }))
}

function summarizeParameterGroupColumns(group: ParameterDefinitionGroup): string {
  const columns = summarizeParameterValues(group.parameters.map(({ parameter }) => parameter.column || ''))
  return columns === '—' ? '—' : `Columns: ${columns}`
}

const SAME_ROW_SELECTION_VALUE = 'same_line_as'
const SAME_ROW_SELECTION_OPTION: ParameterChoiceOption = {
  value: SAME_ROW_SELECTION_VALUE,
  label: 'Same Row As Parameter',
  description: 'Use the row selected by another parameter in this source file, keeping related random or unique values together.',
}

function getParameterSelectionOptions(includeSameRow = false): ParameterChoiceOption[] {
  if (!includeSameRow) return PARAMETER_CHOICE_DEFINITIONS.selection.options
  return [...PARAMETER_CHOICE_DEFINITIONS.selection.options, SAME_ROW_SELECTION_OPTION]
}

function getParameterSelectionDefinition(includeSameRow = false): ParameterChoiceDefinition {
  if (!includeSameRow) return PARAMETER_CHOICE_DEFINITIONS.selection
  return {
    ...PARAMETER_CHOICE_DEFINITIONS.selection,
    options: getParameterSelectionOptions(true),
  }
}

function getParameterSelectionControlValue(parameter: ParameterConfig): string {
  return parameter.same_line_as?.trim() ? SAME_ROW_SELECTION_VALUE : getParameterChoiceValue(parameter, 'selection')
}

type ParameterChoiceField = 'type' | 'update' | 'selection' | 'on_exhaustion'
type MutableParameterChoiceField = Exclude<ParameterChoiceField, 'type'>

interface ParameterChoiceOption {
  description: string
  label: string
  value: string
}

interface ParameterChoiceDefinition {
  description: string
  options: ParameterChoiceOption[]
  title: string
}

const PARAMETER_CHOICE_DEFINITIONS: Record<ParameterChoiceField, ParameterChoiceDefinition> = {
  type: {
    title: 'Parameter Type',
    description: 'Choose how this named parameter gets its value during execution.',
    options: [
      { value: 'list', label: 'List', description: 'Uses inline values or a mapped data source column, then advances according to the selection rule.' },
      { value: 'file', label: 'File', description: 'Reads values from a data source file or imported source parameter file mapping.' },
      { value: 'random_number', label: 'Random Number', description: 'Generates a number between the configured minimum and maximum values.' },
      { value: 'date_time', label: 'Date/Time', description: 'Formats the current date and time with the configured layout, timezone, and offset.' },
      { value: 'timestamp', label: 'Timestamp', description: 'Generates a timestamp in a standard format such as Unix, Unix ms, or ISO 8601.' },
      { value: 'counter', label: 'Counter', description: 'Increments from the configured start value by the configured step.' },
      { value: 'current_iteration', label: 'Current Iteration', description: 'Uses the current VUser iteration number, with optional padding and offset settings.' },
      { value: 'vuser_id', label: 'VUser ID', description: 'Uses the stable one-based identifier of the current virtual user.' },
      { value: 'host', label: 'Host', description: 'Uses the current host value, or a host override list when one is provided.' },
      { value: 'time', label: 'Time', description: 'Formats the current time of day using the configured time layout.' },
      { value: 'random_string', label: 'Random String', description: 'Generates a string using the configured length and optional character set.' },
      { value: 'uuid', label: 'UUID', description: 'Generates a UUID when the parameter refreshes.' },
    ],
  },
  update: {
    title: 'Update Cadence',
    description: 'Choose when the runtime refreshes this parameter value.',
    options: [
      { value: 'once', label: 'Once', description: 'Resolve the value once and reuse it for the full VUser run.' },
      { value: 'each_iteration', label: 'Each Iteration', description: 'Refresh the value at the start of every VUser iteration.' },
      { value: 'each_occurrence', label: 'Each Occurrence', description: 'Refresh the value every time the parameter token appears in an action.' },
    ],
  },
  selection: {
    title: 'Selection Strategy',
    description: 'Choose how row-backed parameter values are selected from the source.',
    options: [
      { value: 'sequential', label: 'Sequential', description: 'Read rows in order, which is useful for deterministic replay and imported source behavior.' },
      { value: 'random', label: 'Random', description: 'Pick a row at random for each refresh, which spreads usage across the data set.' },
      { value: 'unique', label: 'Unique', description: 'Reserve unique rows so concurrent VUsers do not reuse the same value until the source is exhausted.' },
    ],
  },
  on_exhaustion: {
    title: 'Exhaustion Behavior',
    description: 'Choose what happens when the source runs out of available parameter values.',
    options: [
      { value: 'wrap', label: 'Wrap', description: 'Start over at the beginning of the source when all rows have been used.' },
      { value: 'last', label: 'Continue With Last Row', description: 'Keep resolving to the last available row or value after the source is exhausted.' },
      { value: 'empty', label: 'Empty', description: 'Resolve the parameter as an empty string after the source is exhausted.' },
      { value: 'error', label: 'Error', description: 'Fail the VUser action when no value is available.' },
    ],
  },
}

function getParameterChoiceValue(parameter: ParameterConfig, field: ParameterChoiceField): string {
  if (field === 'type') return parameter.type || 'list'
  if (field === 'update') return parameter.update || 'each_iteration'
  if (field === 'selection') return parameter.selection || 'sequential'
  return parameter.on_exhaustion || 'wrap'
}

function formatParameterChoiceLabel(field: ParameterChoiceField, value: string): string {
  const option = PARAMETER_CHOICE_DEFINITIONS[field].options.find(candidate => candidate.value === value)
    || (field === 'selection' && value === SAME_ROW_SELECTION_VALUE ? SAME_ROW_SELECTION_OPTION : null)
  return option?.label || value
}

export function ParameterDefinitionsSection({
  focusedSourceName,
  isOpen,
  onAddParameter,
  onRemoveParameter,
  onSelectParameter,
  onToggleOpen,
  onUpdateParameter,
  selectedParameterIdx,
  script,
}: ParameterDefinitionsSectionProps) {
  const [expandedParameterSources, setExpandedParameterSources] = useState<Set<string>>(() => new Set())
  const [activeParameterChoice, setActiveParameterChoice] = useState<{ field: MutableParameterChoiceField; idx: number } | null>(null)
  const [activeSelectionSettingsIdx, setActiveSelectionSettingsIdx] = useState<number | null>(null)
  const [activeParameterSettingsIdx, setActiveParameterSettingsIdx] = useState<number | null>(null)
  const previousParameterCountRef = useRef(script.parameters?.length || 0)
  const parameterGroups = useMemo(
    () => buildParameterDefinitionGroups(script.parameters || [], script.data_sources || []),
    [script.data_sources, script.parameters],
  )
  const parameterSections = useMemo(
    () => buildParameterDefinitionSections(parameterGroups),
    [parameterGroups],
  )
  const focusedParameterSourceKey = useMemo(
    () => findParameterDefinitionGroupBySource(parameterGroups, focusedSourceName)?.key || null,
    [focusedSourceName, parameterGroups],
  )
  const selectedParameterSourceKey = useMemo(
    () => parameterGroups.find(group => group.parameters.some(({ index }) => index === selectedParameterIdx))?.key || null,
    [parameterGroups, selectedParameterIdx],
  )
  const activeChoiceParameter = activeParameterChoice ? (script.parameters || [])[activeParameterChoice.idx] : null
  const activeSelectionSettingsParameter = activeSelectionSettingsIdx !== null ? (script.parameters || [])[activeSelectionSettingsIdx] : null
  const activeSameLineOptions = useMemo(
    () => getSameLineParameterOptions(script.parameters || [], activeSelectionSettingsIdx),
    [activeSelectionSettingsIdx, script.parameters],
  )
  const activeParameterSettingsParameter = activeParameterSettingsIdx !== null ? (script.parameters || [])[activeParameterSettingsIdx] : null
  useEffect(() => {
    if (!focusedParameterSourceKey) return
    setExpandedParameterSources(previous => {
      if (previous.has(focusedParameterSourceKey)) return previous
      const next = new Set(previous)
      next.add(focusedParameterSourceKey)
      return next
    })
  }, [focusedParameterSourceKey])
  useEffect(() => {
    if (!selectedParameterSourceKey) return
    setExpandedParameterSources(previous => {
      if (previous.has(selectedParameterSourceKey)) return previous
      const next = new Set(previous)
      next.add(selectedParameterSourceKey)
      return next
    })
  }, [selectedParameterSourceKey])
  useEffect(() => {
    const nextCount = script.parameters?.length || 0
    const previousCount = previousParameterCountRef.current
    previousParameterCountRef.current = nextCount
    if (nextCount <= previousCount) return

    const addedParameterIndex = nextCount - 1
    const addedParameterSourceKey = parameterGroups.find(group =>
      group.parameters.some(({ index }) => index === addedParameterIndex),
    )?.key
    if (!addedParameterSourceKey) return

    setExpandedParameterSources(previous => {
      if (previous.has(addedParameterSourceKey)) return previous
      const next = new Set(previous)
      next.add(addedParameterSourceKey)
      return next
    })
  }, [parameterGroups, script.parameters])
  const toggleParameterSource = (sourceKey: string) => {
    setExpandedParameterSources(previous => {
      const next = new Set(previous)
      if (next.has(sourceKey)) next.delete(sourceKey)
      else next.add(sourceKey)
      return next
    })
  }
  const applyParameterChoice = (value: string) => {
    if (!activeParameterChoice) return
    onUpdateParameter(activeParameterChoice.idx, activeParameterChoice.field, value)
    setActiveParameterChoice(null)
  }
  const selectParameter = (idx: number, parameter: ParameterConfig) => {
    onSelectParameter(idx, parameter)
  }
  const handleAddParameter = () => {
    if (!isOpen) onToggleOpen()
    onAddParameter()
  }
  const renderParameterGroup = (group: ParameterDefinitionGroup) => {
    const isExpanded = expandedParameterSources.has(group.key)
    return (
      <Fragment key={group.key}>
        <tr className="parameter-source-row">
          <td>
            <button
              type="button"
              className="parameter-source-toggle"
              aria-expanded={isExpanded}
              aria-label={`${isExpanded ? 'Collapse' : 'Expand'} source ${group.sourceFile}`}
              onClick={() => toggleParameterSource(group.key)}
            >
              <span aria-hidden="true" className="parameter-section-chevron">{isExpanded ? '▾' : '▸'}</span>
              <span className="parameter-source-label">
                <strong>{group.sourceFile}</strong>
                <small>
                  {[group.sourceType, group.sourceName && group.sourceName !== group.sourceFile ? group.sourceName : '']
                    .filter(Boolean)
                    .join(' · ')}
                </small>
              </span>
            </button>
          </td>
          <td>{group.parameters.length} parameter{group.parameters.length === 1 ? '' : 's'}</td>
          <td className="cell-mono">{formatParameterRowCount(group.rowCount)}</td>
          <td>{summarizeParameterValues(group.parameters.map(({ parameter }) => parameter.update || 'each_iteration'))}</td>
          <td>{summarizeParameterGroupSelection(group)}</td>
          <td>{summarizeParameterGroupColumns(group)}</td>
          <td className="cell-mono">—</td>
          <td className="parameter-cell-empty" style={{ textAlign: 'right' }}>{isExpanded ? 'Expanded' : 'Select to edit'}</td>
        </tr>
        {isExpanded && group.parameters.map(({ index: idx, parameter }) => (
          <ParameterDefinitionRow
            idx={idx}
            key={`${group.key}-${parameter.name || 'parameter'}-${idx}`}
            isSelected={selectedParameterIdx === idx}
            onOpenChoice={setActiveParameterChoice}
            onOpenSettings={setActiveParameterSettingsIdx}
            onOpenSelectionSettings={setActiveSelectionSettingsIdx}
            onRemoveParameter={onRemoveParameter}
            onSelectParameter={selectParameter}
            onUpdateParameter={onUpdateParameter}
            parameter={parameter}
            rowCount={group.rowCount}
          />
        ))}
      </Fragment>
    )
  }

  return (
    <>
      <div className="panel-title-row parameter-section-header" style={{ marginBottom: 12 }}>
        <button
          type="button"
          className="parameter-section-toggle"
          aria-expanded={isOpen}
          aria-label={`${isOpen ? 'Collapse' : 'Expand'} Parameters`}
          onClick={onToggleOpen}
        >
          <span aria-hidden="true" className="parameter-section-chevron">{isOpen ? '▾' : '▸'}</span>
          <span>
            <strong style={{ display: 'block', marginBottom: 4 }}>Parameters</strong>
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Define parameters with source-style update cadence and generated values.</span>
          </span>
        </button>
        <button type="button" className="btn btn-sm btn-primary" onClick={handleAddParameter}>+ Add Parameter</button>
      </div>

      {isOpen && ((script.parameters?.length || 0) === 0 ? (
        <div className="parameter-empty-state" style={{ marginBottom: 20 }}>No parameters defined. Add one for random, date/time, host, counter, file, or list-driven values.</div>
      ) : (
        <div className="parameter-table-wrap parameter-definitions-wrap" style={{ marginBottom: 20 }}>
          <table className="parameter-table parameter-definitions-table">
            <thead>
              <tr>
                <th className="parameter-source-type-column">Source File / Type</th>
                <th>Parameter Name</th>
                <th>Rows</th>
                <th>Update</th>
                <th>Selection</th>
                <th>Settings</th>
                <th>Template</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {parameterSections.map(section => (
                <Fragment key={section.key}>
                  <tr className="parameter-category-row">
                    <td colSpan={PARAMETER_DEFINITION_COLUMN_COUNT}>
                      <span>{section.title}</span>
                      <small>{section.summary}</small>
                    </td>
                  </tr>
                  {section.groups.map(renderParameterGroup)}
                </Fragment>
              ))}
            </tbody>
          </table>
          {activeParameterChoice && activeChoiceParameter && (
            <ParameterChoiceDialog
              definition={PARAMETER_CHOICE_DEFINITIONS[activeParameterChoice.field]}
              onClose={() => setActiveParameterChoice(null)}
              onSelect={applyParameterChoice}
              selectedValue={getParameterChoiceValue(activeChoiceParameter, activeParameterChoice.field)}
            />
          )}
          {activeSelectionSettingsIdx !== null && activeSelectionSettingsParameter && (
            <ParameterSelectionSettingsDialog
              onClose={() => setActiveSelectionSettingsIdx(null)}
              onUpdate={(field, value) => onUpdateParameter(activeSelectionSettingsIdx, field, value)}
              parameter={activeSelectionSettingsParameter}
              sameLineOptions={activeSameLineOptions}
            />
          )}
          {activeParameterSettingsIdx !== null && activeParameterSettingsParameter && (
            <ParameterSettingsDialog
              onClose={() => setActiveParameterSettingsIdx(null)}
              onUpdate={(field, value) => onUpdateParameter(activeParameterSettingsIdx, field, value)}
              parameter={activeParameterSettingsParameter}
            />
          )}
        </div>
      ))}
    </>
  )
}

interface ParameterDefinitionRowProps {
  idx: number
  isSelected: boolean
  onOpenChoice: (choice: { field: MutableParameterChoiceField; idx: number }) => void
  onOpenSettings: (idx: number) => void
  onOpenSelectionSettings: (idx: number) => void
  onRemoveParameter: (idx: number) => void
  onSelectParameter: (idx: number, parameter: ParameterConfig) => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
  rowCount: number | null
}

function ParameterDefinitionRow({
  idx,
  isSelected,
  onOpenChoice,
  onOpenSettings,
  onOpenSelectionSettings,
  onRemoveParameter,
  onSelectParameter,
  onUpdateParameter,
  parameter,
  rowCount,
}: ParameterDefinitionRowProps) {
  const parameterName = parameter.name || `Parameter ${idx + 1}`
  const selectParameter = () => onSelectParameter(idx, parameter)
  return (
    <tr
      className={`parameter-child-row ${isSelected ? 'selected' : ''}`}
      onClick={selectParameter}
      onFocus={selectParameter}
    >
      <td className="parameter-source-type-cell" aria-label="Source file or type inherited from expanded row" />
      <td>
        <ParameterRenameInput key={parameter.name} name={parameter.name} onRename={name => onUpdateParameter(idx, 'name', name)} />
      </td>
      <td className="cell-mono">{formatParameterRowCount(rowCount)}</td>
      <td>
        <ParameterChoiceButton
          field="update"
          onClick={() => onOpenChoice({ field: 'update', idx })}
          parameterName={parameterName}
          value={getParameterChoiceValue(parameter, 'update')}
        />
      </td>
      <td>
        <div className="parameter-selection-controls">
          {(parameter.type === 'file' || parameter.type === 'list') ? (
            <ParameterSelectionSettingsButton
              onClick={() => onOpenSelectionSettings(idx)}
              parameter={parameter}
              parameterName={parameterName}
            />
          ) : (
            <span className="parameter-cell-empty">—</span>
          )}
        </div>
      </td>
      <td>
        <ParameterSettingsButton
          onClick={(event) => {
            event.stopPropagation()
            onOpenSettings(idx)
          }}
          parameter={parameter}
          parameterName={parameterName}
        />
      </td>
      <td className="cell-mono">{parameter.name ? `{{param.${parameter.name}}}` : '—'}</td>
              <td aria-label="Parameter actions" style={{ textAlign: 'right' }}>
        <button
          className="btn btn-sm btn-ghost"
          style={{ color: 'var(--danger-text)' }}
          onClick={(event: ReactMouseEvent<HTMLButtonElement>) => {
            event.stopPropagation()
            onRemoveParameter(idx)
          }}
        >
          Remove
        </button>
      </td>
    </tr>
  )
}

interface ParameterChoiceButtonProps {
  field: ParameterChoiceField
  onClick: () => void
  parameterName: string
  value: string
}

function ParameterChoiceButton({
  field,
  onClick,
  parameterName,
  value,
}: ParameterChoiceButtonProps) {
  const fieldLabel = PARAMETER_CHOICE_DEFINITIONS[field].title
  return (
    <button
      type="button"
      className="parameter-choice-button"
      aria-label={`Change ${fieldLabel} for ${parameterName}`}
      onClick={onClick}
    >
      <strong>{formatParameterChoiceLabel(field, value)}</strong>
      <small>{value}</small>
    </button>
  )
}

interface ParameterSelectionSettingsButtonProps {
  onClick: () => void
  parameter: ParameterConfig
  parameterName: string
}

function ParameterSelectionSettingsButton({
  onClick,
  parameter,
  parameterName,
}: ParameterSelectionSettingsButtonProps) {
  return (
    <button
      type="button"
      className="parameter-choice-button parameter-settings-button"
      aria-label={`Open Selection settings for ${parameterName}`}
      onClick={onClick}
    >
      <strong>Settings</strong>
      <small>
        {parameter.same_line_as ? 'Same Row As Parameter' : formatParameterChoiceLabel('selection', getParameterChoiceValue(parameter, 'selection'))}
        {' / '}
        {formatParameterChoiceLabel('on_exhaustion', getParameterChoiceValue(parameter, 'on_exhaustion'))}
        {parameter.same_line_as ? ` · Same row as ${parameter.same_line_as}` : ''}
      </small>
    </button>
  )
}

interface ParameterSettingsButtonProps {
  onClick: (event: ReactMouseEvent<HTMLButtonElement>) => void
  parameter: ParameterConfig
  parameterName: string
}

function summarizeParameterSettings(parameter: ParameterConfig): string {
  const details: string[] = []
  if (parameter.data_source) details.push(`Source: ${parameter.data_source}`)
  if (parameter.column) details.push(`Column: ${parameter.column}`)
  if (parameter.values?.length) details.push(`${parameter.values.length} values`)
  if (parameter.type === 'random_number') details.push(`${parameter.min ?? 0}-${parameter.max ?? 100}`)
  if (parameter.type === 'random_string') details.push(`Length ${parameter.length ?? 12}`)
  if (parameter.type === 'counter' || parameter.type === 'current_iteration') details.push(`Step ${parameter.step ?? 1}`)
  if (parameter.format) details.push(`Format: ${parameter.format}`)
  if (parameter.timezone) details.push(parameter.timezone)
  if (parameter.seed !== undefined) details.push(`Seed ${parameter.seed}`)
  return details.slice(0, 2).join(' • ') || 'Configure fields'
}

function ParameterSettingsButton({
  onClick,
  parameter,
  parameterName,
}: ParameterSettingsButtonProps) {
  return (
    <button
      type="button"
      className="parameter-choice-button parameter-settings-button"
      aria-label={`Open settings for ${parameterName}`}
      onClick={onClick}
    >
      <strong>Configure</strong>
      <small>{summarizeParameterSettings(parameter)}</small>
    </button>
  )
}

// Built-in (runtime-generated) parameter types — every type except the data-backed file/list.
const BUILTIN_PARAMETER_TYPE_OPTIONS = PARAMETER_CHOICE_DEFINITIONS.type.options.filter(
  option => option.value !== 'list' && option.value !== 'file',
)

interface ParameterTypeFieldsProps {
  parameter: ParameterConfig
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
}

interface ParameterFormatControlProps {
  parameter: ParameterConfig
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
}

function ParameterFormatControl({ parameter, onUpdate }: ParameterFormatControlProps) {
  const type = parameter.type || 'list'
  const presets = parameterFormatPresetsForType(type)
  const currentFormat = parameter.format || parameterFormatForType(type, '')
  const selectedPreset = presets.find(preset => preset.value === currentFormat)
  const allowsCustom = type === 'date_time' || type === 'time'
  const [customSelected, setCustomSelected] = useState(false)
  useEffect(() => setCustomSelected(false), [parameter.name, type])
  const selectValue = customSelected || !selectedPreset ? CUSTOM_PARAMETER_FORMAT_VALUE : selectedPreset.value

  const applyPreset = (value: string) => {
    if (value === CUSTOM_PARAMETER_FORMAT_VALUE) {
      setCustomSelected(true)
      onUpdate('format', currentFormat || '')
      return
    }
    const preset = presets.find(candidate => candidate.value === value)
    if (!preset) return
    setCustomSelected(false)
    onUpdate('format', preset.value)
    if (preset.timezone !== undefined) {
      onUpdate('timezone', preset.timezone)
    }
  }

  return (
    <div className="parameter-format-control">
      <select aria-label="Parameter format preset" value={selectValue} onChange={event => applyPreset(event.target.value)}>
        {presets.map(preset => (
          <option key={preset.value} value={preset.value}>{preset.label}</option>
        ))}
        {allowsCustom && <option value={CUSTOM_PARAMETER_FORMAT_VALUE}>Custom Go layout</option>}
      </select>
      {allowsCustom && selectValue === CUSTOM_PARAMETER_FORMAT_VALUE && (
        <input
          aria-label="Custom parameter format"
          value={parameter.format || ''}
          onChange={event => onUpdate('format', event.target.value)}
          placeholder="Go time layout"
        />
      )}
      <small>{selectedPreset?.description || (allowsCustom ? 'Use any Go time layout compatible with the DSL runtime.' : 'Choose a supported timestamp format.')}</small>
    </div>
  )
}

// ParameterTypeFields renders the key inline configuration inputs for a built-in parameter type
// (min/max, start/step, length/charset, format/timezone/offset). Mirrors the inputs in the
// Parameter Settings dialog so both surfaces stay consistent.
function ParameterTypeFields({ parameter, onUpdate }: ParameterTypeFieldsProps) {
  const type = parameter.type || 'list'
  const isTime = type === 'date_time' || type === 'timestamp' || type === 'time'
  if (type === 'uuid' || type === 'host') {
    return (
      <p className="parameter-settings-empty">
        {type === 'uuid'
          ? 'UUID values are generated automatically — no extra configuration needed.'
          : 'Host uses the runtime host value. Configure optional host overrides later in Parameters.'}
      </p>
    )
  }
  return (
    <div className="parameter-settings-grid parameterize-type-fields">
      {type === 'random_number' && (
        <>
          <label><span>Minimum</span><input type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdate('min', Number(event.target.value))} placeholder="min" /></label>
          <label><span>Maximum</span><input type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdate('max', Number(event.target.value))} placeholder="max" /></label>
        </>
      )}
      {(type === 'counter' || type === 'current_iteration') && (
        <>
          <label><span>Start</span><input type="number" value={numberInputValue(parameter.start, type === 'current_iteration' ? 1 : 0)} onChange={event => onUpdate('start', Number(event.target.value))} placeholder="start" /></label>
          <label><span>Step</span><input type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdate('step', Number(event.target.value))} placeholder="step" /></label>
          <label><span>End</span><input type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdate('end', Number(event.target.value))} placeholder="end (optional)" /></label>
        </>
      )}
      {type === 'random_string' && (
        <>
          <label><span>Length</span><input type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdate('length', Number(event.target.value))} placeholder="length" /></label>
          <label><span>Charset</span><input value={parameter.charset || ''} onChange={event => onUpdate('charset', event.target.value)} placeholder="charset override" /></label>
        </>
      )}
      {isTime && (
        <>
          <div className="parameter-settings-field">
            <span>Format</span>
            <ParameterFormatControl parameter={parameter} onUpdate={onUpdate} />
          </div>
          <label><span>Timezone</span><input value={parameter.timezone || ''} onChange={event => onUpdate('timezone', event.target.value)} placeholder="UTC, America/New_York" /></label>
          <label><span>Offset Seconds</span><input type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdate('offset_seconds', Number(event.target.value))} placeholder="offset seconds" /></label>
        </>
      )}
    </div>
  )
}

interface ExistingParameterDialogProps {
  draft: ActionEditorParameterDraft
  onClose: () => void
  onSelect: (parameterName: string) => void
  parameters: ParameterConfig[]
}

export function ExistingParameterDialog({
  draft,
  onClose,
  onSelect,
  parameters,
}: ExistingParameterDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const [query, setQuery] = useState('')
  const normalizedQuery = query.trim().toLowerCase()
  const filteredParameters = useMemo(() => parameters.filter(parameter => {
    const haystack = [parameter.name, parameter.type, parameter.data_source, parameter.column]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return !normalizedQuery || haystack.includes(normalizedQuery)
  }), [normalizedQuery, parameters])

  return (
    <DialogLayer><div className="editor-dialog-backdrop parameter-choice-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-existing-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Use Existing Parameter"
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Use Existing Parameter</h3>
            <p className="editor-dialog-subtitle">
              Replace <span className="cell-mono">{draft.selectedText}</span> with an existing parameter token.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          <label className="parameterize-name-field">
            <span>Find Parameter</span>
            <input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search by name, type, or source" />
          </label>
          {parameters.length === 0 ? (
            <p className="parameter-settings-empty">No parameters exist yet. Create a new parameter for this selection first.</p>
          ) : filteredParameters.length === 0 ? (
            <p className="parameter-settings-empty">No parameters match this search.</p>
          ) : (
            <div className="parameter-existing-list">
              {filteredParameters.map((parameter, index) => {
                const name = parameter.name || `Parameter ${index + 1}`
                const summary = summarizeParameterSettings(parameter)
                return (
                  <button
                    type="button"
                    className="parameter-choice-option"
                    key={`${name}-${index}`}
                    onClick={() => onSelect(name)}
                  >
                    <span className="parameter-choice-option-heading">
                      <strong>{name}</strong>
                      <span className="cell-mono">{parameter.type || 'list'}</span>
                    </span>
                    <span>{summary}</span>
                    <span className="cell-mono">{`{{param.${name}}}`}</span>
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div></DialogLayer>
  )
}

interface JsonOccurrenceReviewDialogProps {
  onClose: () => void
  onCreateNew: (ranges: Array<{ from: number; to: number }>) => void
  onUseExisting: (parameterName: string, ranges: Array<{ from: number; to: number }>) => void
  parameters: ParameterConfig[]
  review: JsonParameterOccurrenceReviewState
}

export function JsonOccurrenceReviewDialog({
  onClose,
  onCreateNew,
  onUseExisting,
  parameters,
  review,
}: JsonOccurrenceReviewDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const optionalOccurrences = useMemo(
    () => review.occurrences.filter(occurrence => !occurrence.isOriginal),
    [review.occurrences],
  )
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set(optionalOccurrences.map(occurrence => occurrence.id)))
  const [existingName, setExistingName] = useState(() => parameters[0]?.name || '')

  useEffect(() => {
    setSelectedIds(new Set(optionalOccurrences.map(occurrence => occurrence.id)))
  }, [optionalOccurrences])

  useEffect(() => {
    if (!existingName && parameters[0]?.name) {
      setExistingName(parameters[0].name)
    }
  }, [existingName, parameters])

  const selectedRanges = optionalOccurrences
    .filter(occurrence => selectedIds.has(occurrence.id))
    .map(occurrence => ({ from: occurrence.from, to: occurrence.to }))
  const selectedCount = selectedRanges.length + 1

  const toggleOccurrence = (id: string, checked: boolean) => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  return (
    <DialogLayer><div className="editor-dialog-backdrop parameter-choice-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-occurrence-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Review Matching Occurrences"
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Review Matching Occurrences</h3>
            <p className="editor-dialog-subtitle">
              The original selection is always replaced. Choose which matching JSON string values should also use the parameter.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          <div className="parameter-occurrence-summary">
            <strong className="cell-mono">{review.draft.selectedText}</strong>
            <span>{selectedCount} replacement{selectedCount === 1 ? '' : 's'} selected</span>
          </div>
          {optionalOccurrences.length === 0 ? (
            <p className="parameter-settings-empty">No additional exact string-value matches were found in the Script JSON.</p>
          ) : (
            <div className="parameter-occurrence-list">
              {optionalOccurrences.map(occurrence => (
                <label className="parameter-occurrence-item" key={occurrence.id}>
                  <input
                    aria-label={`Include occurrence at ${occurrence.path}, line ${occurrence.line}`}
                    type="checkbox"
                    checked={selectedIds.has(occurrence.id)}
                    onChange={event => toggleOccurrence(occurrence.id, event.target.checked)}
                  />
                  <span>
                    <strong>{occurrence.path}</strong>
                    <small>Line {occurrence.line}</small>
                    <code>{occurrence.preview}</code>
                  </span>
                </label>
              ))}
            </div>
          )}
          <div className="parameter-occurrence-actions">
            <button type="button" className="btn btn-sm btn-primary" onClick={() => onCreateNew(selectedRanges)}>
              Create new parameter
            </button>
            <label>
              <span>Existing Parameter</span>
              <select value={existingName} onChange={event => setExistingName(event.target.value)} disabled={parameters.length === 0}>
                {parameters.map((parameter, index) => {
                  const name = parameter.name || `Parameter ${index + 1}`
                  return <option key={`${name}-${index}`} value={name}>{name}</option>
                })}
              </select>
            </label>
            <button
              type="button"
              className="btn btn-sm btn-secondary"
              disabled={!existingName || parameters.length === 0}
              onClick={() => onUseExisting(existingName, selectedRanges)}
            >
              Use existing parameter
            </button>
          </div>
        </div>
      </div>
    </div></DialogLayer>
  )
}

interface ParameterizeDialogProps {
  draft: ActionEditorParameterDraft
  onClose: () => void
  onChooseFile: () => void
  onConfirmBuiltin: (name: string, type: string, typeConfig: Partial<ParameterConfig>) => void
}

// ParameterizeDialog forks the right-click → Parameterize flow: pick a File/Data parameter (which
// continues to the existing Parameter Data editor) or a Built-in Function, which expands in place
// to a type picker plus that type's key configuration fields.
export function ParameterizeDialog({ draft, onClose, onChooseFile, onConfirmBuiltin }: ParameterizeDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const [mode, setMode] = useState<'choose' | 'builtin'>('choose')
  const [name, setName] = useState(() => sanitizeVarName(draft.requestedName || draft.seedValue))
  const [typeParam, setTypeParam] = useState<ParameterConfig | null>(null)

  const selectType = (type: string) => {
    setTypeParam(prev => applyParameterTypeDefaults({ ...(prev || EMPTY_PARAMETER), name, type }, type))
  }
  const updateField = (field: keyof ParameterConfig, value: unknown) => {
    setTypeParam(prev => (prev ? { ...prev, [field]: value } : prev))
  }
  const canCreate = Boolean(name.trim() && typeParam?.type)
  const create = () => {
    if (!typeParam?.type || !name.trim()) return
    onConfirmBuiltin(sanitizeVarName(name), typeParam.type, typeParam)
  }

  return (
    <DialogLayer><div className="editor-dialog-backdrop parameter-choice-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameterize-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Parameterize Selection"
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameterize Selection</h3>
            <p className="editor-dialog-subtitle">
              Replace <span className="cell-mono">{draft.selectedText}</span> with a parameter.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          {mode === 'choose' ? (
            <>
              <button type="button" className="parameter-choice-option" onClick={onChooseFile}>
                <span className="parameter-choice-option-heading"><strong>File / Data</strong><span className="cell-mono">file</span></span>
                <span>Use inline values or a data source file/column, advancing per the selection rule.</span>
              </button>
              <button type="button" className="parameter-choice-option" onClick={() => setMode('builtin')}>
                <span className="parameter-choice-option-heading"><strong>Built-in Function</strong><span className="cell-mono">fn</span></span>
                <span>Generate the value at runtime — random number, iteration number, time, UUID, and more.</span>
              </button>
            </>
          ) : (
            <div className="parameterize-builtin">
              <label className="parameterize-name-field">
                <span>Parameter Name</span>
                <input value={name} onChange={event => setName(sanitizeVarName(event.target.value))} placeholder="param_name" />
              </label>
              <div className="parameterize-type-grid">
                {BUILTIN_PARAMETER_TYPE_OPTIONS.map(option => {
                  const selected = typeParam?.type === option.value
                  return (
                    <button
                      type="button"
                      key={option.value}
                      className={`parameter-choice-option ${selected ? 'selected' : ''}`}
                      aria-pressed={selected}
                      onClick={() => selectType(option.value)}
                    >
                      <span className="parameter-choice-option-heading"><strong>{option.label}</strong><span className="cell-mono">{option.value}</span></span>
                      <span>{option.description}</span>
                    </button>
                  )
                })}
              </div>
              {typeParam && <ParameterTypeFields parameter={typeParam} onUpdate={updateField} />}
              <div className="parameter-draft-actions">
                <button type="button" className="btn btn-sm btn-primary" onClick={create} disabled={!canCreate}>Create Parameter</button>
                <button type="button" className="btn btn-sm btn-ghost" onClick={() => setMode('choose')}>Back</button>
                <button type="button" className="btn btn-sm btn-ghost" onClick={onClose}>Cancel</button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div></DialogLayer>
  )
}

interface ParameterChoiceDialogProps {
  definition: ParameterChoiceDefinition
  onClose: () => void
  onSelect: (value: string) => void
  selectedValue: string
}

function ParameterChoiceDialog({
  definition,
  onClose,
  onSelect,
  selectedValue,
}: ParameterChoiceDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  return (
    <DialogLayer><div className="editor-dialog-backdrop parameter-choice-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={definition.title}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">{definition.title}</h3>
            <p className="editor-dialog-subtitle">{definition.description}</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          {definition.options.map(option => {
            const selected = option.value === selectedValue
            return (
              <button
                type="button"
                className={`parameter-choice-option ${selected ? 'selected' : ''}`}
                aria-pressed={selected}
                key={option.value}
                onClick={() => onSelect(option.value)}
              >
                <span className="parameter-choice-option-heading">
                  <strong>{option.label}</strong>
                  <span className="cell-mono">{option.value}</span>
                </span>
                <span>{option.description}</span>
              </button>
            )
          })}
        </div>
      </div>
    </div></DialogLayer>
  )
}

interface ParameterSettingsDialogProps {
  onClose: () => void
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
}

function parseParameterListInput(value: string): string[] {
  // One value per line, kept verbatim: surrounding whitespace and deliberately
  // blank entries are real values, and dropping the trailing blank line would
  // swallow the newline the operator just typed to start the next value.
  const entries = value.split(/\r?\n/)
  return entries.length === 1 && entries[0] === '' ? [] : entries
}

function numberInputValue(value: number | undefined, fallback: number | ''): number | string {
  return value ?? fallback
}

function updateOptionalNumber(onUpdate: (field: keyof ParameterConfig, value: unknown) => void, field: keyof ParameterConfig, value: string) {
  onUpdate(field, value.trim() === '' ? undefined : Number(value))
}

interface ParameterSettingsBodyProps {
  className?: string
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
}

export function ParameterSettingsBody({
  className = '',
  onUpdate,
  parameter,
}: ParameterSettingsBodyProps) {
  const parameterName = parameter.name || 'Parameter'
  const parameterType = parameter.type || 'list'
  const isDataBacked = parameterType === 'file' || parameterType === 'list'
  const isGenerated = ['random_number', 'counter', 'current_iteration', 'vuser_id', 'random_string', 'uuid'].includes(parameterType)
  const isTime = parameterType === 'date_time' || parameterType === 'timestamp' || parameterType === 'time'
  const showsDataMapping = isDataBacked || parameterType === 'host'
  const showsOutput = parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number' || parameter.seed !== undefined

  return (
    <div className={`parameter-settings-dialog-body ${className}`}>
      <details className="parameter-settings-accordion" open>
        <summary>
          <strong>General</strong>
          <span>Parameter type and runtime behavior</span>
        </summary>
        <div className="parameter-settings-grid">
          <div className="parameter-settings-field">
            <span>Parameter Type</span>
            <ParameterTypeReadonlyValue parameterName={parameterName} parameterType={parameterType} />
          </div>
          <label>
            <span>Update Cadence</span>
            <select
              aria-label={`Update Cadence for ${parameterName}`}
              value={getParameterChoiceValue(parameter, 'update')}
              onChange={event => onUpdate('update', event.target.value)}
            >
              {PARAMETER_CHOICE_DEFINITIONS.update.options.map(option => (
                <option key={option.value} value={option.value}>{option.label}</option>
              ))}
            </select>
          </label>
        </div>
      </details>

      <details className="parameter-settings-accordion" open={showsDataMapping}>
        <summary>
          <strong>Data Mapping</strong>
          <span>Source, column, row selection, and inline values</span>
        </summary>
        {showsDataMapping ? (
          <div className="parameter-settings-grid">
            {isDataBacked && (
              <>
                <label>
                  <span>Data Source</span>
                  <input value={parameter.data_source || ''} onChange={event => onUpdate('data_source', event.target.value)} placeholder="users" />
                </label>
                <label>
                  <span>Column</span>
                  <input value={parameter.column || ''} onChange={event => onUpdate('column', event.target.value)} placeholder="column name" />
                </label>
                <label>
                  <span>Selection</span>
                  <select value={getParameterChoiceValue(parameter, 'selection')} onChange={event => onUpdate('selection', event.target.value)}>
                    {PARAMETER_CHOICE_DEFINITIONS.selection.options.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <label>
                  <span>On Exhaustion</span>
                  <select value={getParameterChoiceValue(parameter, 'on_exhaustion')} onChange={event => onUpdate('on_exhaustion', event.target.value)}>
                    {PARAMETER_CHOICE_DEFINITIONS.on_exhaustion.options.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <label className="parameter-settings-wide">
                  <span>Same Row As</span>
                  <input value={parameter.same_line_as || ''} onChange={event => onUpdate('same_line_as', event.target.value.trim() || undefined)} placeholder="optional parameter name" />
                </label>
              </>
            )}
            {(parameterType === 'list' || parameterType === 'host') && (
              <label className="parameter-settings-wide">
                <span>{parameterType === 'host' ? 'Host Overrides' : 'Inline Values'}</span>
                <textarea
                  value={(parameter.values || []).join('\n')}
                  onChange={event => onUpdate('values', parseParameterListInput(event.target.value))}
                  placeholder={parameterType === 'host' ? 'host-a\nhost-b' : 'value1\nvalue2'}
                />
              </label>
            )}
          </div>
        ) : (
          <p className="parameter-settings-empty">This parameter type does not use source mapping fields.</p>
        )}
      </details>

      <details className="parameter-settings-accordion" open={isGenerated || isTime}>
        <summary>
          <strong>Type Options</strong>
          <span>Generated values, time formats, and runtime options</span>
        </summary>
        {isGenerated || isTime ? (
          <div className="parameter-settings-grid">
            {parameterType === 'random_number' && (
              <>
                <label><span>Minimum</span><input type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdate('min', Number(event.target.value))} placeholder="min" /></label>
                <label><span>Maximum</span><input type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdate('max', Number(event.target.value))} placeholder="max" /></label>
              </>
            )}
            {(parameterType === 'counter' || parameterType === 'current_iteration') && (
              <>
                <label><span>Start</span><input type="number" value={numberInputValue(parameter.start, parameterType === 'current_iteration' ? 1 : 0)} onChange={event => onUpdate('start', Number(event.target.value))} placeholder="start" /></label>
                <label><span>End</span><input type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdate('end', Number(event.target.value))} placeholder="end (optional)" /></label>
                <label><span>Step</span><input type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdate('step', Number(event.target.value))} placeholder="step" /></label>
              </>
            )}
            {parameterType === 'random_string' && (
              <>
                <label><span>Length</span><input type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdate('length', Number(event.target.value))} placeholder="length" /></label>
                <label><span>Charset</span><input value={parameter.charset || ''} onChange={event => onUpdate('charset', event.target.value)} placeholder="charset override" /></label>
              </>
            )}
            {parameterType === 'uuid' && <p className="parameter-settings-empty">UUID values are generated automatically using the selected update cadence.</p>}
            {parameterType === 'vuser_id' && <p className="parameter-settings-empty">The current virtual user identifier is supplied automatically.</p>}
            {isTime && (
              <>
                <div className="parameter-settings-field">
                  <span>Format</span>
                  <ParameterFormatControl parameter={parameter} onUpdate={onUpdate} />
                </div>
                <label><span>Timezone</span><input value={parameter.timezone || ''} onChange={event => onUpdate('timezone', event.target.value)} placeholder="UTC, America/New_York" /></label>
                <label><span>Offset Seconds</span><input type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdate('offset_seconds', Number(event.target.value))} placeholder="offset seconds" /></label>
              </>
            )}
          </div>
        ) : (
          <p className="parameter-settings-empty">No additional type-specific options are required.</p>
        )}
      </details>

      <details className="parameter-settings-accordion" open={showsOutput}>
        <summary>
          <strong>Output And Reproducibility</strong>
          <span>Padding and optional seed</span>
        </summary>
        <div className="parameter-settings-grid">
          {(parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number') && (
            <>
              <label><span>Pad Width</span><input type="number" value={numberInputValue(parameter.pad_width, 0)} onChange={event => onUpdate('pad_width', Number(event.target.value))} placeholder="pad width" /></label>
              <label><span>Pad Character</span><input value={parameter.pad_char || ''} onChange={event => onUpdate('pad_char', event.target.value)} placeholder="pad char" /></label>
            </>
          )}
          <label>
            <span>Seed</span>
            <input
              type="number"
              value={numberInputValue(parameter.seed, '')}
              onChange={event => updateOptionalNumber(onUpdate, 'seed', event.target.value)}
              placeholder="seed (optional)"
            />
          </label>
        </div>
      </details>
    </div>
  )
}

function ParameterSettingsDialog({
  onClose,
  onUpdate,
  parameter,
}: ParameterSettingsDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const parameterName = parameter.name || 'Parameter'
  const parameterType = parameter.type || 'list'
  const hasDataMappingSettings = parameterType === 'file' || parameterType === 'list' || parameterType === 'host'
  const hasGeneratedValueSettings = ['random_number', 'counter', 'current_iteration', 'vuser_id', 'random_string', 'uuid'].includes(parameterType)
  const hasTimeSettings = parameterType === 'date_time' || parameterType === 'timestamp' || parameterType === 'time'
  const hasOutputSettings = parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number' || parameter.seed !== undefined

  return (
    <DialogLayer><div className="editor-dialog-backdrop parameter-choice-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Settings for ${parameterName}`}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameter Settings</h3>
            <p className="editor-dialog-subtitle">Edit advanced settings for {parameterName} without crowding the Parameters table.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-settings-dialog-body">
          <details className="parameter-settings-accordion" open>
            <summary>
              <strong>General</strong>
              <span>Parameter type and runtime behavior</span>
            </summary>
            <div className="parameter-settings-grid">
              <div className="parameter-settings-field">
                <span>Parameter Type</span>
                <ParameterTypeReadonlyValue parameterName={parameterName} parameterType={parameterType} />
              </div>
            </div>
          </details>

          <details className="parameter-settings-accordion" open={hasDataMappingSettings}>
            <summary>
              <strong>Data Mapping</strong>
              <span>Source, column, and inline values</span>
            </summary>
            {hasDataMappingSettings ? (
              <div className="parameter-settings-grid">
                {(parameterType === 'file' || parameterType === 'list') && (
                  <>
                    <label>
                      <span>Data Source</span>
                      <input
                        value={parameter.data_source || ''}
                        onChange={event => onUpdate('data_source', event.target.value)}
                        placeholder="users"
                      />
                    </label>
                    <label>
                      <span>Column</span>
                      <input
                        value={parameter.column || ''}
                        onChange={event => onUpdate('column', event.target.value)}
                        placeholder="column name"
                      />
                    </label>
                  </>
                )}
                {(parameterType === 'list' || parameterType === 'host') && (
                  <label className="parameter-settings-wide">
                    <span>{parameterType === 'host' ? 'Host Overrides' : 'Inline Values'}</span>
                    <textarea
                      value={(parameter.values || []).join('\n')}
                      onChange={event => onUpdate('values', parseParameterListInput(event.target.value))}
                      placeholder={parameterType === 'host' ? 'host-a\nhost-b' : 'value1\nvalue2'}
                    />
                  </label>
                )}
              </div>
            ) : (
              <p className="parameter-settings-empty">This parameter type does not use source mapping fields.</p>
            )}
          </details>

          <details className="parameter-settings-accordion" open={hasGeneratedValueSettings}>
            <summary>
              <strong>Generated values</strong>
              <span>Ranges, counters, and string generation</span>
            </summary>
            {hasGeneratedValueSettings ? (
              <div className="parameter-settings-grid">
                {parameterType === 'random_number' && (
                  <>
                    <label>
                      <span>Minimum</span>
                      <input type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdate('min', Number(event.target.value))} placeholder="min" />
                    </label>
                    <label>
                      <span>Maximum</span>
                      <input type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdate('max', Number(event.target.value))} placeholder="max" />
                    </label>
                  </>
                )}
                {(parameterType === 'counter' || parameterType === 'current_iteration') && (
                  <>
                    <label>
                      <span>Start</span>
                      <input type="number" value={numberInputValue(parameter.start, parameterType === 'current_iteration' ? 1 : 0)} onChange={event => onUpdate('start', Number(event.target.value))} placeholder="start" />
                    </label>
                    <label>
                      <span>End</span>
                      <input type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdate('end', Number(event.target.value))} placeholder="end (optional)" />
                    </label>
                    <label>
                      <span>Step</span>
                      <input type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdate('step', Number(event.target.value))} placeholder="step" />
                    </label>
                  </>
                )}
                {parameterType === 'random_string' && (
                  <>
                    <label>
                      <span>Length</span>
                      <input type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdate('length', Number(event.target.value))} placeholder="length" />
                    </label>
                    <label>
                      <span>Charset</span>
                      <input value={parameter.charset || ''} onChange={event => onUpdate('charset', event.target.value)} placeholder="charset override" />
                    </label>
                  </>
                )}
                {parameterType === 'uuid' && <p className="parameter-settings-empty">UUID values are generated automatically using the selected update cadence.</p>}
                {parameterType === 'vuser_id' && <p className="parameter-settings-empty">The current virtual user identifier is supplied automatically.</p>}
              </div>
            ) : (
              <p className="parameter-settings-empty">This parameter type does not use generated value fields.</p>
            )}
          </details>

          <details className="parameter-settings-accordion" open={hasTimeSettings}>
            <summary>
              <strong>Time And Format</strong>
              <span>Format, timezone, and offset</span>
            </summary>
            {hasTimeSettings ? (
              <div className="parameter-settings-grid">
                <div className="parameter-settings-field">
                  <span>Format</span>
                  <ParameterFormatControl parameter={parameter} onUpdate={onUpdate} />
                </div>
                <label>
                  <span>Timezone</span>
                  <input value={parameter.timezone || ''} onChange={event => onUpdate('timezone', event.target.value)} placeholder="timezone (UTC, America/New_York)" />
                </label>
                <label>
                  <span>Offset Seconds</span>
                  <input type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdate('offset_seconds', Number(event.target.value))} placeholder="offset seconds" />
                </label>
              </div>
            ) : (
              <p className="parameter-settings-empty">This parameter type does not use time formatting fields.</p>
            )}
          </details>

          <details className="parameter-settings-accordion" open={hasOutputSettings}>
            <summary>
              <strong>Output And Reproducibility</strong>
              <span>Padding and optional seed</span>
            </summary>
            <div className="parameter-settings-grid">
              {(parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number') && (
                <>
                  <label>
                    <span>Pad Width</span>
                    <input type="number" value={numberInputValue(parameter.pad_width, 0)} onChange={event => onUpdate('pad_width', Number(event.target.value))} placeholder="pad width" />
                  </label>
                  <label>
                    <span>Pad Character</span>
                    <input value={parameter.pad_char || ''} onChange={event => onUpdate('pad_char', event.target.value)} placeholder="pad char" />
                  </label>
                </>
              )}
              <label>
                <span>Seed</span>
                <input
                  type="number"
                  value={numberInputValue(parameter.seed, '')}
                  onChange={event => updateOptionalNumber(onUpdate, 'seed', event.target.value)}
                  placeholder="seed (optional)"
                />
              </label>
            </div>
          </details>
        </div>
      </div>
    </div></DialogLayer>
  )
}

interface ParameterSelectionSettingsDialogProps {
  onClose: () => void
  onUpdate: (field: 'selection' | 'on_exhaustion' | 'same_line_as', value: string | undefined) => void
  parameter: ParameterConfig
  sameLineOptions: ParameterSameLineOption[]
}

function ParameterSelectionSettingsDialog({
  onClose,
  onUpdate,
  parameter,
  sameLineOptions,
}: ParameterSelectionSettingsDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const parameterName = parameter.name || 'Parameter'
  const selectionValue = getParameterSelectionControlValue(parameter)
  const exhaustionValue = getParameterChoiceValue(parameter, 'on_exhaustion')
  const sameLineSelectOptions = getSameLineParameterSelectOptions(parameter, sameLineOptions)
  const sameLineDisabledValues = sameLineSelectOptions.length === 0 ? [SAME_ROW_SELECTION_VALUE] : []
  const handleSelectionUpdate = (value: string) => {
    if (value === SAME_ROW_SELECTION_VALUE) {
      const nextSameLine = sameLineSelectOptions[0]?.value || ''
      onUpdate('same_line_as', nextSameLine || undefined)
      return
    }
    if (parameter.same_line_as) onUpdate('same_line_as', undefined)
    onUpdate('selection', value)
  }

  return (
    <DialogLayer><div className="editor-dialog-backdrop parameter-choice-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-selection-settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Selection Settings for ${parameterName}`}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Selection Settings</h3>
            <p className="editor-dialog-subtitle">Configure how this parameter selects rows and what happens when the source runs out of values.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          <ParameterSelectionChoiceGroup
            definition={getParameterSelectionDefinition(true)}
            disabledValues={sameLineDisabledValues}
            onSelect={handleSelectionUpdate}
            selectedValue={selectionValue}
          />
          <ParameterSelectionChoiceGroup
            definition={PARAMETER_CHOICE_DEFINITIONS.on_exhaustion}
            onSelect={value => onUpdate('on_exhaustion', value)}
            selectedValue={exhaustionValue}
          />
          <section className="parameter-selection-choice-group">
            <div>
              <h4>Same Row Sync</h4>
              <p>Use the row selected by another parameter from the same source file, so random or unique row selection keeps related columns together.</p>
            </div>
            <label className="parameter-settings-wide">
              <span>Sync Row With</span>
              <select
                aria-label={`Same Row Sync for ${parameterName}`}
                disabled={sameLineSelectOptions.length === 0}
                value={parameter.same_line_as || ''}
                onChange={event => onUpdate('same_line_as', event.target.value || undefined)}
              >
                <option value="">Independent row selection</option>
                {sameLineSelectOptions.map(option => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </select>
            </label>
            {sameLineSelectOptions.length === 0 && (
              <p className="parameter-settings-empty">No other parameters are mapped to this source file.</p>
            )}
          </section>
        </div>
      </div>
    </div></DialogLayer>
  )
}

interface ParameterSelectionChoiceGroupProps {
  definition: ParameterChoiceDefinition
  disabledValues?: string[]
  onSelect: (value: string) => void
  selectedValue: string
}

function ParameterSelectionChoiceGroup({
  definition,
  disabledValues = [],
  onSelect,
  selectedValue,
}: ParameterSelectionChoiceGroupProps) {
  const disabledValueSet = new Set(disabledValues)
  return (
    <section className="parameter-selection-choice-group">
      <div>
        <h4>{definition.title}</h4>
        <p>{definition.description}</p>
      </div>
      <div className="parameter-choice-dialog-body">
        {definition.options.map(option => {
          const selected = option.value === selectedValue
          const disabled = disabledValueSet.has(option.value)
          return (
            <button
              type="button"
              className={`parameter-choice-option ${selected ? 'selected' : ''}`}
              disabled={disabled}
              aria-pressed={selected}
              key={option.value}
              onClick={() => onSelect(option.value)}
            >
              <span className="parameter-choice-option-heading">
                <strong>{option.label}</strong>
                <span className="cell-mono">{option.value}</span>
              </span>
              <span>{option.description}</span>
            </button>
          )
        })}
      </div>
    </section>
  )
}
