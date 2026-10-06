import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import { EditorSelection, StateEffect, type Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { openSearchPanel } from '@codemirror/search'

import type { ScriptParameterDefinition } from '../types/scripts'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import { apexDslSemanticHighlighting } from './apexDslSemanticHighlighting'
import { ActionParameterMenu } from './ScriptEditorActionEditor'
import {
  scriptCodeCandidateMatchField,
  scriptCodeParameterMatchField,
  scriptCodeRangeField,
  scriptCodeResolvedValueField,
  setScriptCodeCandidateMatches,
  setScriptCodeParameterMatches,
  setScriptCodeRange,
  setScriptCodeResolvedValues,
  type ScriptCodeMatchRange,
} from './scriptCodeDecorations'
import type { LineRange } from './dslJsonPointers'
import { buildParameterizeSelectionRequest } from './scriptEditorUtils'
import CodeLineWrapToggle from './CodeLineWrapToggle'
import { useCodeLineWrapPreference } from './codeLineWrapPreference'
import type { ScriptRepairSuggestion } from './scriptAuthoringAssist'
import type { ImportedParameterFileMapping } from './scriptEditorImportModel'
import type { ScriptPhaseActionRow } from './scriptEditorWorkbench'
import {
  shouldWrapScriptJsonLines,
  type ActionEditorSelectionState,
  type AuthorJsonScope,
  type JsonPaneSelectionRequest,
  type ScriptCodeLineJumpRequest,
  type ScriptJsonSidebarTab,
  type ScriptJsonStatus,
} from './scriptEditorJsonModel'

const sourceEditorBasicSetup = { lineNumbers: true, foldGutter: true, highlightActiveLine: true, highlightActiveLineGutter: true }

export interface ScriptCodePaneProps {
  value: string
  status: ScriptJsonStatus
  statusMessage: string
  activePath: string
  activeRange: LineRange | null
  activeParameterName?: string | null
  activeCandidateName?: string | null
  actionCount: number
  assistExtensions?: Extension[]
  className?: string
  title?: string
  ariaLabel?: string
  subtitle?: string
  allowDirectEdit?: boolean
  jumpToLineRequest?: ScriptCodeLineJumpRequest | null
  candidateMatchRanges?: ScriptCodeMatchRange[]
  parameterMatchRanges?: ScriptCodeMatchRange[]
  resolvedValues?: Record<string, string> | null
  onApply: () => void
  onChange: (value: string) => void
  onCopy: () => void
  onDownload: () => void
  onFormat: () => void
  onReset: () => void
  onRequestParameterize?: (selection: JsonPaneSelectionRequest) => void
}

export function ScriptCodePane({
  value,
  status,
  statusMessage,
  activePath,
  activeRange,
  activeParameterName,
  activeCandidateName,
  actionCount,
  assistExtensions,
  className = '',
  title = 'Script JSON',
  ariaLabel = title,
  subtitle,
  allowDirectEdit = true,
  jumpToLineRequest,
  candidateMatchRanges = [],
  parameterMatchRanges = [],
  resolvedValues = null,
  onApply,
  onChange,
  onCopy,
  onDownload,
  onFormat,
  onReset,
  onRequestParameterize,
}: ScriptCodePaneProps) {
  const viewRef = useRef<EditorView | null>(null)
  const onDocumentChangeRef = useRef(onChange)
  onDocumentChangeRef.current = onChange
  const handleDocumentChange = useCallback((nextValue: string) => onDocumentChangeRef.current(nextValue), [])
  const parameterNavigationRef = useRef<{ name?: string; editorReadyVersion: number }>({ editorReadyVersion: -1 })
  const [jsonEditEnabled, setJsonEditEnabled] = useState(false)
  const [editorReadyVersion, setEditorReadyVersion] = useState(0)
  const [wordWrapEnabled, setWordWrapEnabled] = useCodeLineWrapPreference()
  const activeStartLine = activeRange?.startLine ?? null
  const activeEndLine = activeRange?.endLine ?? null
  const parameterMatchKey = useMemo(
    () => parameterMatchRanges.map(range => `${range.from}:${range.to}`).join('|'),
    [parameterMatchRanges],
  )
  const candidateMatchKey = useMemo(
    () => candidateMatchRanges.map(range => `${range.from}:${range.to}`).join('|'),
    [candidateMatchRanges],
  )
  const parameterStatus = activeParameterName
    ? `Parameter: ${activeParameterName} (${parameterMatchRanges.length} occurrence${parameterMatchRanges.length === 1 ? '' : 's'})`
    : ''
  const candidateStatus = activeCandidateName
    ? `Auto parameter: ${activeCandidateName} (${candidateMatchRanges.length} highlighted)`
    : ''

  useEffect(() => {
    if (!allowDirectEdit && jsonEditEnabled) setJsonEditEnabled(false)
  }, [allowDirectEdit, jsonEditEnabled])

  const wordWrapAvailable = shouldWrapScriptJsonLines(value)
  const wrapLines = wordWrapEnabled && wordWrapAvailable
  const extensions = useMemo(() => [
    json(),
    apexCodeMirrorTheme,
    apexDslSemanticHighlighting(),
    scriptCodeRangeField,
    scriptCodeParameterMatchField,
    scriptCodeCandidateMatchField,
    scriptCodeResolvedValueField,
    ...(wrapLines ? [EditorView.lineWrapping] : []),
    ...(assistExtensions ?? []),
  ], [assistExtensions, wrapLines])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const range = activeStartLine != null
      ? { startLine: activeStartLine, endLine: activeEndLine ?? activeStartLine }
      : null
    const effects: StateEffect<unknown>[] = [setScriptCodeRange.of(range)]
    if (range && range.startLine >= 1 && range.startLine <= view.state.doc.lines) {
      const line = view.state.doc.line(range.startLine)
      effects.push(EditorView.scrollIntoView(line.from, { y: 'center' }))
    }
    view.dispatch({ effects })
  }, [activeStartLine, activeEndLine, editorReadyVersion, value])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const firstMatch = parameterMatchRanges[0]
    const effects: StateEffect<unknown>[] = [setScriptCodeParameterMatches.of(parameterMatchRanges)]
    const shouldNavigate = Boolean(activeParameterName) && (
      parameterNavigationRef.current.name !== activeParameterName
      || parameterNavigationRef.current.editorReadyVersion !== editorReadyVersion
    )
    parameterNavigationRef.current = { name: activeParameterName || undefined, editorReadyVersion }
    if (shouldNavigate && firstMatch && firstMatch.to <= view.state.doc.length) {
      effects.push(EditorView.scrollIntoView(firstMatch.from, { y: 'center' }))
      view.dispatch({ selection: EditorSelection.range(firstMatch.from, firstMatch.to), effects })
      return
    }
    view.dispatch({ effects })
  }, [activeParameterName, editorReadyVersion, parameterMatchKey, parameterMatchRanges, value])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const firstMatch = candidateMatchRanges[0]
    const effects: StateEffect<unknown>[] = [setScriptCodeCandidateMatches.of(candidateMatchRanges)]
    if (firstMatch && firstMatch.to <= view.state.doc.length) {
      effects.push(EditorView.scrollIntoView(firstMatch.from, { y: 'center' }))
      view.dispatch({ selection: EditorSelection.range(firstMatch.from, firstMatch.to), effects })
      return
    }
    view.dispatch({ effects })
  }, [candidateMatchKey, candidateMatchRanges, editorReadyVersion, value])

  const resolvedValuesKey = resolvedValues ? JSON.stringify(resolvedValues) : ''
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const range = activeStartLine != null
      ? { startLine: activeStartLine, endLine: activeEndLine ?? activeStartLine }
      : null
    const payload = resolvedValues && range ? { values: resolvedValues, range } : null
    view.dispatch({ effects: [setScriptCodeResolvedValues.of(payload)] })
  }, [activeStartLine, activeEndLine, editorReadyVersion, resolvedValues, resolvedValuesKey, value])

  useEffect(() => {
    const view = viewRef.current
    if (!view || !jumpToLineRequest) return
    const lineNo = Math.max(1, Math.min(view.state.doc.lines, jumpToLineRequest.line))
    const line = view.state.doc.line(lineNo)
    view.dispatch({
      selection: EditorSelection.cursor(line.from),
      effects: EditorView.scrollIntoView(line.from, { y: 'center' }),
    })
  }, [editorReadyVersion, jumpToLineRequest, value])

  const handleOpenSearch = useCallback(() => {
    const view = viewRef.current
    if (!view) return
    openSearchPanel(view)
    view.focus()
  }, [])

  const handleEditorContextMenu = useCallback((event: ReactMouseEvent<HTMLDivElement>) => {
    if (!onRequestParameterize) return
    const view = viewRef.current
    if (!view) return
    const { from, to } = view.state.selection.main
    const request = buildParameterizeSelectionRequest(view.state.doc.toString(), from, to)
    if (!request) return
    event.preventDefault()
    onRequestParameterize({ ...request, x: event.clientX, y: event.clientY })
  }, [onRequestParameterize])

  const parameterizeCurrentSelection = useCallback(() => {
    const view = viewRef.current
    if (!onRequestParameterize || !view) return
    const { from, to } = view.state.selection.main
    const request = buildParameterizeSelectionRequest(view.state.doc.toString(), from, to)
    if (!request) {
      view.focus()
      return
    }
    const coords = view.coordsAtPos(request.end) ?? view.coordsAtPos(to)
    onRequestParameterize({ ...request, x: coords?.left ?? 0, y: coords?.bottom ?? 0 })
  }, [onRequestParameterize])

  return (
    <section className={`script-code-pane ${className}`} aria-label={ariaLabel}>
      <div className="script-code-toolbar">
        <div className="script-code-title">
          <strong>{'{ }'} {title}</strong>
          <span>{candidateStatus || parameterStatus || subtitle || activePath || `${actionCount} executable${actionCount === 1 ? '' : 's'}`}</span>
        </div>
        <div className="script-code-status-row">
          <span className={`script-code-status ${status}`}>{statusMessage}</span>
          {!jsonEditEnabled && (
            <span className="script-code-readonly-pill" title={allowDirectEdit ? 'The JSON mirrors the structured editor. Click Edit JSON to change it directly.' : 'Switch to Full Script JSON to edit raw JSON directly.'}>Read-only</span>
          )}
          <button type="button" className="btn btn-sm btn-ghost" onClick={handleOpenSearch} title="Find in script (⌘F / Ctrl+F)">Find</button>
          <CodeLineWrapToggle
            disabled={!wordWrapAvailable}
            wrapLines={wrapLines}
            onChange={setWordWrapEnabled}
          />
          {onRequestParameterize && (
            <button type="button" className="btn btn-sm btn-ghost" onClick={parameterizeCurrentSelection} title="Parameterize the selected value (you can also right-click a selection)">
              Parameterize
            </button>
          )}
          {allowDirectEdit && !jsonEditEnabled && <button type="button" className="btn btn-sm btn-secondary" onClick={() => setJsonEditEnabled(true)}>Edit JSON</button>}
          {allowDirectEdit && (jsonEditEnabled || status !== 'synced') && <>
            <button type="button" className="btn btn-sm btn-ghost" onClick={onFormat}>Format</button>
            <button type="button" className="btn btn-sm btn-secondary" onClick={() => { onApply(); if (status !== 'invalid') setJsonEditEnabled(false) }} disabled={status === 'invalid' || status === 'synced'}>Apply changes</button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => { onReset(); setJsonEditEnabled(false) }}>Discard JSON edits</button>
          </>}
          <button type="button" className="btn btn-sm btn-ghost" onClick={onCopy}>Copy</button>
          <button type="button" className="btn btn-sm btn-ghost" onClick={onDownload}>Download</button>
        </div>
      </div>
      <div
        className="script-code-editor"
        data-dirty={status === 'dirty' ? 'true' : 'false'}
        data-invalid={status === 'invalid' ? 'true' : 'false'}
        data-readonly={allowDirectEdit && jsonEditEnabled ? 'false' : 'true'}
        onContextMenu={handleEditorContextMenu}
      >
        <CodeMirror
          value={value}
          onChange={handleDocumentChange}
          extensions={extensions}
          theme="none"
          editable={allowDirectEdit && jsonEditEnabled}
          basicSetup={sourceEditorBasicSetup}
          onCreateEditor={view => {
            viewRef.current = view
            setEditorReadyVersion(version => version + 1)
          }}
        />
      </div>
    </section>
  )
}

export function ScriptJsonScopeBar({
  fullScriptOnly = false,
  scope,
  showFullScriptJson,
  unavailable,
  onShowFullScriptJson,
}: {
  fullScriptOnly?: boolean
  scope: AuthorJsonScope | null
  showFullScriptJson: boolean
  unavailable: boolean
  onShowFullScriptJson: (show: boolean) => void
}) {
  if (!scope) return null
  const selectedLabel = scope.subAction ? 'Selected Sub-action' : 'Selected Action'
  return (
    <div className="script-json-scope-bar" aria-label="Script JSON display scope">
      <div className="script-json-scope-summary">
        <strong>{showFullScriptJson || unavailable ? 'Full Script JSON' : selectedLabel}</strong>
        <span title={scope.path}>{scope.label}</span>
      </div>
      <div className="script-json-scope-toggle" role="group" aria-label="JSON display scope">
        {!fullScriptOnly && <button
          type="button"
          className={!showFullScriptJson && !unavailable ? 'active' : ''}
          aria-pressed={!showFullScriptJson && !unavailable}
          disabled={unavailable}
          title={unavailable ? 'Resolve or reset raw JSON edits before returning to selected action JSON.' : `Show only ${selectedLabel.toLowerCase()} JSON`}
          onClick={() => onShowFullScriptJson(false)}
        >
          {selectedLabel}
        </button>}
        <button
          type="button"
          className={showFullScriptJson || unavailable ? 'active' : ''}
          aria-pressed={showFullScriptJson || unavailable}
          onClick={() => onShowFullScriptJson(true)}
        >
          Full Script JSON
        </button>
      </div>
    </div>
  )
}

export function ScriptJsonDocument({
  fullScriptOnly = false,
  scope,
  selectedJsonText,
  showSelectedActionJson,
  showFullScriptJson,
  selectedJsonUnavailable,
  onShowFullScriptJson,
  onCopySelected,
  onDownloadSelected,
  sourcePane,
  selectionState,
  onOpenParameterEditor,
  onOpenExistingParameter,
  onOpenOccurrenceReview,
}: {
  fullScriptOnly?: boolean
  scope: AuthorJsonScope | null
  selectedJsonText: string
  showSelectedActionJson: boolean
  showFullScriptJson: boolean
  selectedJsonUnavailable: boolean
  onShowFullScriptJson: (show: boolean) => void
  onCopySelected: () => void
  onDownloadSelected: () => void
  sourcePane: ScriptCodePaneProps
  selectionState: ActionEditorSelectionState | null
  onOpenParameterEditor: (nextExistingName?: string | null) => void
  onOpenExistingParameter: () => void
  onOpenOccurrenceReview: () => void
}) {
  const scopeBar = (
    <ScriptJsonScopeBar
      fullScriptOnly={fullScriptOnly}
      scope={scope}
      showFullScriptJson={showFullScriptJson}
      unavailable={selectedJsonUnavailable}
      onShowFullScriptJson={onShowFullScriptJson}
    />
  )
  if (showSelectedActionJson && scope) {
    const selectedTitle = scope.subAction ? 'Selected Sub-action JSON' : 'Selected Action JSON'
    return (
      <>
        {scopeBar}
        <p className="author-source-scope-help">This view shows only the selected action. <button type="button" className="btn btn-sm btn-secondary" onClick={() => onShowFullScriptJson(true)}>Edit in Full Script JSON</button> Whole-script editing preserves all action fields.</p>
        <ScriptCodePane
          value={selectedJsonText}
          status="synced"
          statusMessage={scope.subAction ? 'Selected sub-action' : 'Selected action'}
          activePath={scope.path}
          activeRange={null}
          actionCount={1}
          allowDirectEdit={false}
          ariaLabel={selectedTitle}
          title={selectedTitle}
          subtitle={scope.path.replace(/^script\./, '')}
          onApply={() => undefined}
          onChange={() => undefined}
          onCopy={onCopySelected}
          onDownload={onDownloadSelected}
          onFormat={() => undefined}
          onReset={() => undefined}
        />
      </>
    )
  }
  return (
    <>
      {scopeBar}
      <ScriptCodePane {...sourcePane} />
      <ActionParameterMenu
        selectionState={selectionState}
        onOpenParameterEditor={onOpenParameterEditor}
        onOpenExistingParameter={onOpenExistingParameter}
        onOpenOccurrenceReview={onOpenOccurrenceReview}
        variant="json"
      />
    </>
  )
}

export function ScriptJsonCompanionHeader({
  activeTab,
  hasDetailsTab,
  title,
  onChange,
}: {
  activeTab: ScriptJsonSidebarTab
  hasDetailsTab: boolean
  title: string
  onChange: (tab: ScriptJsonSidebarTab) => void
}) {
  return (
    <div className="script-json-sidebar-header">
      <span className="script-json-sidebar-header-title">{title}</span>
      <TabList className="script-json-sidebar-tabs" role="tablist" aria-label="Script JSON companion panel" onKeyDown={handleTabKey}>
        <button type="button" role="tab" aria-selected={activeTab === 'insights'} className={`script-json-sidebar-tab ${activeTab === 'insights' ? 'active' : ''}`} onClick={() => onChange('insights')} tabIndex={(activeTab === 'insights') ? 0 : -1}>
          Insights
        </button>
        <button type="button" role="tab" aria-selected={activeTab === 'auto-params'} className={`script-json-sidebar-tab ${activeTab === 'auto-params' ? 'active' : ''}`} onClick={() => onChange('auto-params')} tabIndex={(activeTab === 'auto-params') ? 0 : -1}>
          Parameters
        </button>
        {hasDetailsTab && (
          <button type="button" role="tab" aria-selected={activeTab === 'details'} className={`script-json-sidebar-tab ${activeTab === 'details' ? 'active' : ''}`} onClick={() => onChange('details')} tabIndex={(activeTab === 'details') ? 0 : -1}>
            Details
          </button>
        )}
      </TabList>
    </div>
  )
}

export function ScriptJsonParameterOutline({
  collapsed,
  deepScanning,
  isEditingExisting,
  parameters,
  referenceCounts,
  selectedParameterName,
  onRunServerScan,
  onScan,
  onSelectParameter,
  onToggle,
}: {
  collapsed: boolean
  deepScanning: boolean
  isEditingExisting: boolean
  parameters: ScriptParameterDefinition[]
  referenceCounts: Map<string, number>
  selectedParameterName: string | null
  onRunServerScan: () => void
  onScan: () => void
  onSelectParameter: (name: string) => void
  onToggle: () => void
}) {
  return (
    <section className="outline-section parameter-outline-section">
      <div className="actions-sidebar-header outline-section-header">
        <button type="button" className="outline-section-toggle" aria-expanded={!collapsed} onClick={onToggle}>
          <span aria-hidden="true" className="outline-section-chevron">{collapsed ? '▸' : '▾'}</span>
          <span>
            <strong>Parameter List</strong>
            <small>{parameters.length} defined</small>
          </span>
        </button>
        <div className="parameter-header-actions">
          <button type="button" className="btn btn-sm btn-ghost" onClick={onScan} title="Auto Parameterization — quick scan for hardcoded values to externalize">Auto Params</button>
          {isEditingExisting && (
            <button type="button" className="btn btn-sm btn-ghost" onClick={onRunServerScan} disabled={deepScanning} title="Deep scan (server) — backend engine also flags secrets">
              {deepScanning ? 'Scanning…' : 'Deep Scan'}
            </button>
          )}
        </div>
      </div>
      {!collapsed && (
        <>
          <div className="parameter-outline-list">
            {parameters.map((parameter, index) => {
              const parameterName = parameter.name || `Parameter ${index + 1}`
              const referenceCount = parameter.name ? referenceCounts.get(parameter.name) || 0 : 0
              const selected = selectedParameterName === parameter.name
              return (
                <button
                  key={`${parameter.name || 'parameter'}-${index}`}
                  type="button"
                  className={`parameter-outline-item ${selected ? 'selected' : ''}`}
                  onClick={() => parameter.name && onSelectParameter(parameter.name)}
                  disabled={!parameter.name}
                >
                  <strong>{parameterName}</strong>
                  <span>{parameter.type || 'list'} · {referenceCount} ref{referenceCount === 1 ? '' : 's'}</span>
                </button>
              )
            })}
            {parameters.length === 0 && <div className="action-empty parameter-outline-empty">No parameters defined yet.</div>}
          </div>
          <div className="parameter-outline-actions">
            <p className="parameter-outline-hint">
              {isEditingExisting
                ? 'Quick scan finds hardcoded values; deep scan also flags secrets via the server engine.'
                : 'Build your requests, then scan to externalize hardcoded values as you go.'}
            </p>
          </div>
        </>
      )}
    </section>
  )
}

interface InsightIssue {
  id: string
  message: string
  severity: string
}

interface ExtractorInspectorItem {
  id: string
  label: string
  actionIndex: number
  phase: ScriptPhaseActionRow['phase']
}

export function ScriptJsonInsightsView({
  actionProblems,
  dataSourceInspectorItems,
  errorCount,
  extractorInspectorItems,
  importReview,
  importedParameterFileMappings,
  onApplyRepairSuggestion,
  onOpenParameterData,
  onOpenErrorDetails,
  onOpenWarningDetails,
  onSelectActionProblem,
  onSelectExtractor,
  parameterInspectorItems,
  readiness,
  repairSuggestions,
  scriptLevelIssues,
  warningCount,
}: {
  actionProblems: Array<{ issue: InsightIssue; row: ScriptPhaseActionRow }>
  dataSourceInspectorItems: string[]
  errorCount: number
  extractorInspectorItems: ExtractorInspectorItem[]
  importReview: ReactNode
  importedParameterFileMappings: ImportedParameterFileMapping[]
  onApplyRepairSuggestion: (suggestion: ScriptRepairSuggestion) => void
  onOpenParameterData: () => void
  onOpenErrorDetails: () => void
  onOpenWarningDetails: () => void
  onSelectActionProblem: (row: ScriptPhaseActionRow) => void
  onSelectExtractor: (item: ExtractorInspectorItem) => void
  parameterInspectorItems: string[]
  readiness: ReactNode
  repairSuggestions: ScriptRepairSuggestion[]
  scriptLevelIssues: InsightIssue[]
  warningCount: number
}) {
  return (
    <>
      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Problems</strong>
          <span className="utility-problem-summary">
            {errorCount > 0 ? (
              <button
                type="button"
                className="script-health-token danger issue-pill-button"
                aria-label={`View ${errorCount} error ${errorCount === 1 ? 'detail' : 'details'}`}
                onClick={onOpenErrorDetails}
              >
                {errorCount} error{errorCount === 1 ? '' : 's'}
              </button>
            ) : <span>0 errors</span>}
            {' '}
            <span aria-hidden="true">•</span>
            {' '}
            {warningCount > 0 ? (
              <button
                type="button"
                className="script-health-token warning issue-pill-button"
                aria-label={`View ${warningCount} warning ${warningCount === 1 ? 'detail' : 'details'}`}
                onClick={onOpenWarningDetails}
              >
                {warningCount} warning{warningCount === 1 ? '' : 's'}
              </button>
            ) : <span>0 warnings</span>}
          </span>
        </div>
        {scriptLevelIssues.length > 0 || actionProblems.length > 0 ? (
          <ul className="utility-list">
            {scriptLevelIssues.map(issue => <li key={issue.id} className={`utility-list-item ${issue.severity}`}>{issue.message}</li>)}
            {actionProblems.map(({ issue, row }) => (
              <li key={issue.id} className={`utility-list-item ${issue.severity}`}>
                <button type="button" className="utility-list-button" onClick={() => onSelectActionProblem(row)}>
                  <span>{row.phaseLabel} {row.actionIndex + 1}</span>
                  <strong>{row.action.name || issue.message}</strong>
                  {row.action.name && <small>{issue.message}</small>}
                </button>
              </li>
            ))}
          </ul>
        ) : <div className="utility-empty-state">No script-level issues detected.</div>}
      </div>

      {readiness}

      <details className="utility-optional"><summary>Repair and import review</summary>
      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Repair Actions</strong>
          <span>{repairSuggestions.length} suggestion{repairSuggestions.length === 1 ? '' : 's'}</span>
        </div>
        {repairSuggestions.length > 0 ? (
          <div className="utility-stack">
            {repairSuggestions.map(suggestion => (
              <div key={suggestion.key} className="utility-suggestion-card">
                <strong>{suggestion.title}</strong>
                <p>{suggestion.description}</p>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => onApplyRepairSuggestion(suggestion)}>{suggestion.applyLabel}</button>
              </div>
            ))}
          </div>
        ) : <div className="utility-empty-state">No deterministic repair actions are needed for this draft.</div>}
      </div>

      {importReview}
      </details>
      <details className="utility-optional"><summary>Data &amp; variable details</summary>

      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Parameter Source Inspector</strong>
          <button type="button" className="btn btn-sm btn-ghost" onClick={onOpenParameterData}>Open</button>
        </div>
        {parameterInspectorItems.length > 0
          ? <ul className="utility-list">{parameterInspectorItems.map((item, index) => <li key={`parameter-inspector-${index}`} className="utility-list-item">{item}</li>)}</ul>
          : <div className="utility-empty-state">No parameters defined yet.</div>}
        {importedParameterFileMappings.length > 0 && (
          <div className="utility-subsection">
            <strong>Imported Mappings</strong>
            <ul className="utility-list">
              {importedParameterFileMappings.map(({ section, sourceName }) => (
                <li key={`${section.file}:${section.name}`} className="utility-list-item">{section.file}: {section.name}{sourceName ? ` -> ${sourceName}` : ''}</li>
              ))}
            </ul>
          </div>
        )}
      </div>

      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Parameter Data & Extractors</strong>
          <span>{dataSourceInspectorItems.length} data • {extractorInspectorItems.length} extractor{extractorInspectorItems.length === 1 ? '' : 's'}</span>
        </div>
        {dataSourceInspectorItems.length > 0
          ? <ul className="utility-list">{dataSourceInspectorItems.map((item, index) => <li key={`source-inspector-${index}`} className="utility-list-item">{item}</li>)}</ul>
          : <div className="utility-empty-state">No parameter data defined yet.</div>}
        {extractorInspectorItems.length > 0 && (
          <div className="utility-subsection">
            <strong>Extractors</strong>
            <ul className="utility-list">
              {extractorInspectorItems.map(item => (
                <li key={item.id} className="utility-list-item">
                  <button type="button" className="utility-link-button" onClick={() => onSelectExtractor(item)}>{item.label}</button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
      </details>
    </>
  )
}
