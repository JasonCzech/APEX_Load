import type { MappingReviewState } from './sourceMappingReviewState'
import DialogLayer from './ux/DialogLayer'
import { useMemo, useState } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import { EditorView } from '@codemirror/view'
import type { ScriptDefinition, ScriptImportMetadata } from '../types/scripts'
import { dialogBackdropProps } from './dialogBackdropProps'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import SourceMappingReview from './SourceMappingReview'
import {
  formatImportParameterMapping,
  formatPromotionCount,
  formatRuntimeSettingStatusLabel,
  formatTrueClientPromotionStatus,
  getImportedRuntimeSectionId,
  type Web2ObjectDescriptorRow,
  type Web2SelectorCandidate,
} from './scriptEditorImportModel'
import type { MigrationAssistantSuggestion } from './scriptAuthoringAssist'
import { formatReplayTime } from './scriptEditorUtils'
import { useEditorDialogRef } from './useEditorDialogRef'
import type { useScriptImportReviewController } from './useScriptImportReviewController'
import CodeLineWrapToggle from './CodeLineWrapToggle'
import { useCodeLineWrapPreference } from './codeLineWrapPreference'

const IMPORT_JSON_PREVIEW_MAX_CHARS = 250_000
type ImportReviewController = ReturnType<typeof useScriptImportReviewController>

export interface AssistantChangeLogItem {
  id: string
  summary: string
  title: string
}

function MetadataJsonViewer({ metadata }: { metadata: ScriptImportMetadata }) {
  const metadataJson = useMemo(() => JSON.stringify(metadata, null, 2), [metadata])
  const [wordWrapEnabled, setWordWrapEnabled] = useCodeLineWrapPreference()
  const wordWrapAvailable = metadataJson.length <= IMPORT_JSON_PREVIEW_MAX_CHARS
  const wrapLines = wordWrapEnabled && wordWrapAvailable
  const extensions = useMemo(() => [
    json(),
    apexCodeMirrorTheme,
    ...(wrapLines ? [EditorView.lineWrapping] : []),
    EditorView.editable.of(false),
  ], [wrapLines])

  return (
    <section className="script-code-pane metadata-json-viewer" aria-label="Metadata JSON">
      <div className="script-code-toolbar">
        <div className="script-code-title">
          <strong>{'{ }'} Metadata JSON</strong>
          <span>Preserved import metadata</span>
        </div>
        <div className="script-code-status-row">
          <span className="script-code-status synced">Read only</span>
          <CodeLineWrapToggle
            disabled={!wordWrapAvailable}
            wrapLines={wrapLines}
            onChange={setWordWrapEnabled}
          />
        </div>
      </div>
      <div className="script-code-editor metadata-json-editor">
        <CodeMirror
          value={metadataJson}
          extensions={extensions}
          theme="none"
          readOnly
          editable={false}
          basicSetup={{
            lineNumbers: true,
            foldGutter: true,
            highlightActiveLine: true,
            highlightActiveLineGutter: true,
          }}
        />
      </div>
    </section>
  )
}

export function DraftJSONReview({
  reviewBaselineScript,
  script,
  jsonTextLength,
}: {
  reviewBaselineScript: ScriptDefinition | null
  script: ScriptDefinition
  jsonTextLength: number
}) {
  const [expanded, setExpanded] = useState(false)
  const previews = useMemo(() => {
    if (!expanded || jsonTextLength > IMPORT_JSON_PREVIEW_MAX_CHARS) return null
    return {
      baseline: JSON.stringify(reviewBaselineScript, null, 2),
      current: JSON.stringify(script, null, 2),
    }
  }, [expanded, jsonTextLength, reviewBaselineScript, script])

  return (
    <details className="utility-json-review" onToggle={event => setExpanded(event.currentTarget.open)}>
      <summary>Review baseline vs current JSON</summary>
      {expanded && (
        <div className="utility-json-grid">
          <div><strong>Review Baseline</strong>{previews ? <pre>{previews.baseline}</pre> : <div className="utility-empty-state">Script too large to preview inline.</div>}</div>
          <div><strong>Current Draft</strong>{previews ? <pre>{previews.current}</pre> : <div className="utility-empty-state">Script too large to preview inline.</div>}</div>
        </div>
      )}
    </details>
  )
}

function MetadataJsonDialog({ metadata, onClose }: { metadata: ScriptImportMetadata; onClose: () => void }) {
  const dialogRef = useEditorDialogRef(onClose)
  return (
    <DialogLayer><div className="editor-dialog-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog metadata-json-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Imported Metadata JSON"
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Imported Metadata JSON</h3>
            <p className="editor-dialog-subtitle">
              Read-only view of the preserved import metadata. Opened on demand to keep the editor fast on large imports.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body metadata-json-dialog-body">
          <MetadataJsonViewer metadata={metadata} />
        </div>
      </div>
    </div></DialogLayer>
  )
}

function ImportedParameterMappingsDialog({
  controller,
  onOpenMappedDataSource,
}: {
  controller: ImportReviewController
  onOpenMappedDataSource: (sourceName?: string | null) => void
}) {
  const dialogRef = useEditorDialogRef(() => controller.setShowImportedParameterMappingsDialog(false))
  const close = () => controller.setShowImportedParameterMappingsDialog(false)
  return (
    <DialogLayer><div className="editor-dialog-backdrop" {...dialogBackdropProps(close)}>
      <div
        ref={dialogRef}
        className="editor-dialog imported-parameter-mappings-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Parameter File Mappings"
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameter File Mappings</h3>
            <p className="editor-dialog-subtitle">
              Review the preserved source parameter definitions without shrinking the source and DSL mapping workspace.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={close}>Close</button>
        </div>
        <div className="editor-dialog-body imported-parameter-mappings-dialog-body">
          <p>These preserved source parameter definitions show how the importer mapped file-backed data into APEX Load data sources.</p>
          <div className="import-correlation-list">
            {controller.importedParameterFileMappings.map(({ section, sourceName }) => (
              <div key={`${section.file}:${section.name}`} className="import-correlation-card">
                <strong>{section.file}: {section.name}</strong>
                <span>{formatImportParameterMapping(section.values)}</span>
                {sourceName && <span>Generated source: {sourceName}</span>}
                {sourceName && (
                  <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                    <button
                      className="btn btn-secondary btn-sm"
                      type="button"
                      onClick={() => {
                        onOpenMappedDataSource(sourceName)
                        close()
                      }}
                    >
                      Open Mapped Data Source
                    </button>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div></DialogLayer>
  )
}

export function ScriptImportReviewDialogs({
  controller,
  onOpenMappedDataSource,
}: {
  controller: ImportReviewController
  onOpenMappedDataSource: (sourceName?: string | null) => void
}) {
  return (
    <>
      {controller.showImportedParameterMappingsDialog && controller.importedParameterFileMappings.length > 0 && (
        <ImportedParameterMappingsDialog controller={controller} onOpenMappedDataSource={onOpenMappedDataSource} />
      )}
      {controller.showMetadataJsonDialog && controller.importMetadata && (
        <MetadataJsonDialog
          metadata={controller.importMetadata}
          onClose={() => controller.setShowMetadataJsonDialog(false)}
        />
      )}
    </>
  )
}

export function ImportReviewOutlineSection({ controller }: { controller: ImportReviewController }) {
  const count = controller.importedParameterFileMappings.length
  if (count === 0) return null
  return (
    <section className="outline-section import-review-section">
      <div className="actions-sidebar-header outline-section-header">
        <button
          type="button"
          className="outline-section-toggle"
          aria-expanded={!controller.importReviewCollapsed}
          onClick={() => controller.setImportReviewCollapsed(collapsed => !collapsed)}
        >
          <span aria-hidden="true" className="outline-section-chevron">{controller.importReviewCollapsed ? '▸' : '▾'}</span>
          <span>
            <strong>Import Review</strong>
            <small>{count} parameter file{count === 1 ? '' : 's'}</small>
          </span>
        </button>
      </div>
      {!controller.importReviewCollapsed && (
        <div className="starter-blocks-panel parameter-mappings-launcher">
          <div className="starter-blocks-header">
            <strong>Parameter File Mappings</strong>
            <span>{count}</span>
          </div>
          <p>Review how the source tool&apos;s parameter files mapped into APEX data sources. Most useful right after import.</p>
          <button
            type="button"
            className="btn btn-sm btn-secondary parameter-mappings-launcher-button"
            onClick={() => controller.setShowImportedParameterMappingsDialog(true)}
          >
            Open Mapping List
          </button>
        </div>
      )}
    </section>
  )
}

export function ImportedMetadataUtilityPanel({
  activeAction,
  controller,
  migrationSuggestions,
  onApplyMigrationSuggestion,
  onOpenMappedDataSource,
  scriptName,
}: {
  activeAction: number | null
  controller: ImportReviewController
  migrationSuggestions: MigrationAssistantSuggestion[]
  onApplyMigrationSuggestion: (suggestion: MigrationAssistantSuggestion) => void
  onOpenMappedDataSource: (sourceName?: string | null) => void
  scriptName: string
}) {
  const { importMetadata, importSummary, sourceReview, trueClientPromotionReview } = controller
  return (
    <>
      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Import Summary</strong>
          <span>{importMetadata?.source || 'import'}</span>
        </div>
        <ul className="utility-list">
          <li className="utility-list-item">Source: {importMetadata?.source_script_name || scriptName}</li>
          <li className="utility-list-item">{importSummary.runtimeFiles.length} runtime file{importSummary.runtimeFiles.length === 1 ? '' : 's'}</li>
          <li className="utility-list-item">{importSummary.parameterFiles.length} parameter file{importSummary.parameterFiles.length === 1 ? '' : 's'}</li>
          <li className="utility-list-item">{controller.importedDataSourceSummaries.length} generated data source{controller.importedDataSourceSummaries.length === 1 ? '' : 's'}</li>
        </ul>
      </div>
      {sourceReview && (
        <div className="utility-card">
          <div className="utility-card-header">
            <strong>Source Mapping</strong>
            <span>{sourceReview.mappings?.length || 0} mappings</span>
          </div>
          <div className="utility-metric-row">
            <span className="metric-badge">{sourceReview.source_files?.length || 0} files</span>
            <span className="metric-badge">{importSummary.correlationSuggestions.length} correlations</span>
          </div>
          {activeAction !== null && (
            <button type="button" className="btn btn-sm btn-secondary" onClick={() => controller.focusSourceMappingForAction(activeAction)}>
              Focus Selected Action
            </button>
          )}
        </div>
      )}
      {trueClientPromotionReview && (
        <div className="utility-card">
          <div className="utility-card-header">
            <strong>TruClient Promotion</strong>
            <span>{formatTrueClientPromotionStatus(trueClientPromotionReview.status)}</span>
          </div>
          <div className="utility-metric-row">
            <span className="metric-badge">{formatPromotionCount(trueClientPromotionReview.replayEvidence.events, 'event')}</span>
            <span className="metric-badge">{trueClientPromotionReview.replayEvidence.passed} passed / {trueClientPromotionReview.replayEvidence.failed} failed</span>
            <span className="metric-badge">{formatPromotionCount(trueClientPromotionReview.gates.selectorFallbackCandidates, 'fallback')}</span>
          </div>
          <ul className="utility-list">
            <li className="utility-list-item">Replay: {trueClientPromotionReview.replayEvidence.sessionId || trueClientPromotionReview.replayEvidence.status}</li>
            <li className="utility-list-item">{formatPromotionCount(trueClientPromotionReview.gates.reviewRequiredActions, 'review action')}</li>
            <li className="utility-list-item">{formatPromotionCount(trueClientPromotionReview.compatibility.frameWindowBlockers, 'frame/window blocker')}</li>
          </ul>
        </div>
      )}
      {migrationSuggestions.length > 0 && (
        <div className="utility-card">
          <div className="utility-card-header">
            <strong>Migration Assistant</strong>
            <span>{migrationSuggestions.length} suggestion{migrationSuggestions.length === 1 ? '' : 's'}</span>
          </div>
          <div className="utility-stack">
            {migrationSuggestions.map(suggestion => (
              <div key={`${suggestion.key}:metadata`} className="utility-suggestion-card">
                <strong>{suggestion.title}</strong>
                <p>{suggestion.description}</p>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => onApplyMigrationSuggestion(suggestion)}>
                  {suggestion.applyLabel}
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
      {controller.importedParameterFileMappings.length > 0 && (
        <div className="utility-card">
          <div className="utility-card-header">
            <strong>Parameter Files</strong>
            <span>{controller.importedParameterFileMappings.length}</span>
          </div>
          <ul className="utility-list">
            {controller.importedParameterFileMappings.map(({ section, sourceName }) => (
              <li key={`${section.file}:${section.name}:metadata`} className="utility-list-item">
                <button type="button" className="utility-link-button" onClick={() => onOpenMappedDataSource(sourceName)}>
                  {section.file}: {section.name}{sourceName ? ` -> ${sourceName}` : ''}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  )
}

export function ObjectDescriptorUtilityPanel({
  controller,
  onApplyCandidate,
  onSelectAction,
}: {
  controller: ImportReviewController
  onApplyCandidate: (row: Web2ObjectDescriptorRow, candidate: Web2SelectorCandidate) => void
  onSelectAction: (actionIndex: number) => void
}) {
  const review = controller.objectDescriptorReview
  return (
    <>
      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Descriptor Review</strong>
          <span>{review.rows.length} objects</span>
        </div>
        <div className="utility-metric-row">
          <span className="metric-badge">{review.repositoryCount} repos</span>
          <span className="metric-badge">{review.candidateCount} candidates</span>
          <span className="metric-badge">{review.unsupportedCount} review</span>
        </div>
      </div>
      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Selector Shortcuts</strong>
          <span>{review.rows.length}</span>
        </div>
        {review.rows.length > 0 ? (
          <div className="utility-stack">
            {review.rows.map(row => (
              <div key={`${row.key}:sidebar`} className="utility-suggestion-card">
                <strong>{row.label}</strong>
                <p>{row.descriptorId} · {row.status}</p>
                {row.currentSelector && <code>{row.currentSelector}</code>}
                <div className="utility-inline-actions">
                  {row.actionIndex !== null && (
                    <button type="button" className="btn btn-sm btn-ghost" onClick={() => onSelectAction(row.actionIndex!)}>
                      Select Action
                    </button>
                  )}
                  {row.candidates[0] && (
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => onApplyCandidate(row, row.candidates[0])} disabled={row.actionIndex === null}>
                      Use Best Selector
                    </button>
                  )}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="utility-empty-state">No object descriptors are available.</div>
        )}
      </div>
    </>
  )
}

export function ObjectDescriptorReviewDocument({
  controller,
  onApplyCandidate,
  onSelectAction,
}: {
  controller: ImportReviewController
  onApplyCandidate: (row: Web2ObjectDescriptorRow, candidate: Web2SelectorCandidate) => void
  onSelectAction: (actionIndex: number) => void
}) {
  const review = controller.objectDescriptorReview
  return (
    <section className="center-document config-panel center-document-object" style={{ padding: 0 }} aria-label="Object Descriptors">
      <div className="center-document-header">
        <div>
          <h3 className="panel-title">Object Descriptors</h3>
          <p>Review imported TruClient object IDs, current selectors, and extracted fallback candidates.</p>
        </div>
      </div>
      <div className="panel-body" style={{ padding: '0 20px 20px' }}>
        <div className="import-metadata-grid">
          <div className="import-metadata-card"><span>Repositories</span><strong>{review.repositoryCount}</strong><small>Preserved from the source archive</small></div>
          <div className="import-metadata-card"><span>Descriptors</span><strong>{review.rows.length}</strong><small>Mapped to review rows</small></div>
          <div className="import-metadata-card"><span>Candidates</span><strong>{review.candidateCount}</strong><small>CSS, XPath, text, or role options</small></div>
          <div className="import-metadata-card"><span>Source Fragments</span><strong>{review.unsupportedCount}</strong><small>Review-only or unsupported steps</small></div>
        </div>
        <div className="object-descriptor-list">
          {review.rows.map(row => (
            <div key={row.key} className="object-descriptor-card">
              <div className="object-descriptor-heading">
                <div><strong>{row.label}</strong><span>{row.descriptorId} · {row.status}</span></div>
                {row.actionIndex !== null && (
                  <button type="button" className="btn btn-sm btn-secondary" onClick={() => onSelectAction(row.actionIndex!)}>Select Action</button>
                )}
              </div>
              <div className="object-descriptor-current"><span>Current selector</span><code>{row.currentSelector || 'No selector applied'}</code></div>
              {(row.frameContext || row.windowContext || row.replayDiagnostics.length > 0) && (
                <div className="object-descriptor-current">
                  <span>Replay diagnostics</span>
                  {row.frameContext && <code>Frame: {row.frameContext}</code>}
                  {row.windowContext && <code>Window: {row.windowContext}</code>}
                  {row.replayDiagnostics.map((diagnostic, index) => <code key={`${row.key}-diagnostic-${index}`}>{diagnostic}</code>)}
                </div>
              )}
              <div className="object-selector-candidates">
                {row.candidates.map(candidate => (
                  <div key={`${row.key}:${candidate.kind}:${candidate.value}`} className="object-selector-candidate">
                    <div>
                      <strong>{candidate.kind} · {candidate.confidence || 0}%</strong>
                      <code>{candidate.value}</code>
                      {candidate.reason && <span>{candidate.reason}</span>}
                    </div>
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => onApplyCandidate(row, candidate)} disabled={row.actionIndex === null}>Use Selector</button>
                  </div>
                ))}
                {row.candidates.length === 0 && <div className="utility-empty-state">No alternate selectors were extracted for this object.</div>}
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

export function SourceMappingReviewDocument({
  controller,
  baselineScript, onChangeReview, draftDirty, onEditTarget,
  onOpenRuntimeSettings,
  onSelectAction,
  script,
}: {
  controller: ImportReviewController
  onEditTarget?: (path: string) => void
  baselineScript?: ScriptDefinition | null
  onChangeReview?: (state: MappingReviewState) => void
  draftDirty?: boolean
  onOpenRuntimeSettings: (area?: string | null) => void
  onSelectAction: (actionIndex: number) => void
  script: ScriptDefinition
}) {
  const { importSummary, sourceReview, trueClientPromotionReview } = controller
  return (
    <section id="imported-metadata-section" className="center-document import-metadata-panel config-panel imported-metadata-section open" aria-label="Source Mapping">
      <div className="import-metadata-body">
        {sourceReview && (
          <SourceMappingReview
            focusRequestId={controller.sourceMappingFocus.requestId}
            focusedMappingId={controller.sourceMappingFocus.mappingId}
            id="imported-source-mapping"
            baselineScript={baselineScript}
            onChangeReview={onChangeReview}
            onEditTarget={onEditTarget}
            draftDirty={draftDirty}
            onToggleWorkspaceMaximized={controller.toggleSourceMappingWorkspaceMaximized}
            showTitle={false}
            sourceReview={sourceReview}
            script={script}
            title="Source Mapping"
            workspaceMaximized={controller.sourceMappingWorkspaceMaximized}
          />
        )}

        {trueClientPromotionReview && (
          <div className="import-correlation-section">
            <h4>TruClient Promotion Review</h4>
            <p>Saved promotion evidence for this imported browser script, including replay coverage, JavaScript review gates, selector fallbacks, and frame/window blockers.</p>
            <div className="import-metadata-grid">
              <div className="import-metadata-card"><span>Status</span><strong>{formatTrueClientPromotionStatus(trueClientPromotionReview.status)}</strong><small>{trueClientPromotionReview.compatibility.promotionAllowed ? 'Promotion allowed by report' : 'Review gates still apply'}</small></div>
              <div className="import-metadata-card"><span>Source Profile</span><strong>{trueClientPromotionReview.compatibility.sourceProfile || 'Unknown'}</strong><small>{trueClientPromotionReview.sourceScriptName || trueClientPromotionReview.source}</small></div>
              <div className="import-metadata-card"><span>Replay Evidence</span><strong>{trueClientPromotionReview.replayEvidence.events}</strong><small>{trueClientPromotionReview.replayEvidence.passed} passed / {trueClientPromotionReview.replayEvidence.failed} failed</small></div>
              <div className="import-metadata-card"><span>Replay Session</span><strong>{trueClientPromotionReview.replayEvidence.sessionId || trueClientPromotionReview.replayEvidence.status}</strong><small>{trueClientPromotionReview.replayEvidence.completedAt ? `Completed ${formatReplayTime(trueClientPromotionReview.replayEvidence.completedAt)}` : 'No completed timestamp'}</small></div>
              <div className="import-metadata-card"><span>Artifacts</span><strong>{trueClientPromotionReview.replayEvidence.screenshots} / {trueClientPromotionReview.replayEvidence.domHtmlCaptures}</strong><small>screenshots / DOM or HTML captures</small></div>
              <div className="import-metadata-card"><span>Console & Network</span><strong>{trueClientPromotionReview.replayEvidence.consoleEntries} / {trueClientPromotionReview.replayEvidence.networkEntries}</strong><small>console entries / network entries</small></div>
              <div className="import-metadata-card"><span>Review Actions</span><strong>{trueClientPromotionReview.gates.reviewRequiredActions}</strong><small>{trueClientPromotionReview.gates.javascriptReviewActions} JavaScript-gated action{trueClientPromotionReview.gates.javascriptReviewActions === 1 ? '' : 's'}</small></div>
              <div className="import-metadata-card"><span>Selector Fallbacks</span><strong>{trueClientPromotionReview.gates.selectorFallbackCandidates}</strong><small>{trueClientPromotionReview.compatibility.selectorConfidencePct ? `${trueClientPromotionReview.compatibility.selectorConfidencePct}% selector confidence` : 'No selector score recorded'}</small></div>
              <div className="import-metadata-card"><span>Frame / Window</span><strong>{trueClientPromotionReview.compatibility.frameWindowBlockers}</strong><small>{trueClientPromotionReview.compatibility.replayStatus || 'Replay status not recorded'}</small></div>
            </div>
            {(trueClientPromotionReview.gates.importWarnings.length > 0 || trueClientPromotionReview.gates.replayWarnings.length > 0 || trueClientPromotionReview.nextActions.length > 0) && (
              <div className="import-correlation-list">
                {trueClientPromotionReview.gates.importWarnings.slice(0, 4).map((warning, index) => <div key={`trueclient-import-warning-${index}`} className="import-correlation-card"><strong>Import Warning</strong><span>{warning}</span></div>)}
                {trueClientPromotionReview.gates.replayWarnings.slice(0, 4).map((warning, index) => <div key={`trueclient-replay-warning-${index}`} className="import-correlation-card"><strong>Replay Warning</strong><span>{warning}</span></div>)}
                {trueClientPromotionReview.nextActions.map((action, index) => <div key={`trueclient-next-action-${index}`} className="import-correlation-card"><strong>Next Action</strong><span>{action}</span></div>)}
              </div>
            )}
          </div>
        )}

        {importSummary.runtimeSettingReadiness && (
          <div className="import-correlation-section">
            <h4>Migration Readiness</h4>
            <p>{controller.runtimeSettingReadinessSummary}</p>
            {importSummary.runtimeSettingReadiness.blocking_settings.length > 0 && (
              <div className="import-correlation-list">
                {importSummary.runtimeSettingReadiness.blocking_settings.map(entry => (
                  <div key={`readiness-${entry.id}`} className="import-correlation-card">
                    <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                    <span>{entry.concept}</span>
                    <span>{entry.runtime_files.length > 0 ? entry.runtime_files.join(', ') : 'No runtime file recorded'}</span>
                    {getImportedRuntimeSectionId(entry.area) && <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}><button className="btn btn-secondary btn-sm" type="button" onClick={() => onOpenRuntimeSettings(entry.area)}>{`Open ${entry.area} Settings`}</button></div>}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        {importSummary.runtimeSettingsParity.length > 0 && (
          <div className="import-correlation-section">
            <h4>Runtime Settings Parity</h4>
            <p>These imported source runtime settings were classified against APEX Load&apos;s code-owned support matrix.</p>
            <div className="import-correlation-list">
              {importSummary.runtimeSettingsParity.map(entry => (
                <div key={entry.id} className="import-correlation-card">
                  <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                  <span>{entry.concept}</span>
                  <span>{entry.runtime_files.length > 0 ? entry.runtime_files.join(', ') : 'No runtime file recorded'}</span>
                  {getImportedRuntimeSectionId(entry.area) && <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}><button className="btn btn-secondary btn-sm" type="button" onClick={() => onOpenRuntimeSettings(entry.area)}>{`Open ${entry.area} Settings`}</button></div>}
                </div>
              ))}
            </div>
          </div>
        )}

        {importSummary.correlationSuggestions.length > 0 && (
          <div className="import-correlation-section">
            <h4>Correlation Suggestions</h4>
            <p>Review the imported extractor mappings before you rename variables or rewrite dependent requests.</p>
            <div className="import-correlation-list">
              {controller.importedCorrelationFocusTargets.map(({ correlation, foundAtActionIndex, foundAtLabel, usedAtLocations, usedAtTargets }) => (
                <div key={`${correlation.name}:${correlation.expression}:${correlation.found_at}`} className="import-correlation-card">
                  <strong>{correlation.name}</strong>
                  <span>{correlation.source} via {correlation.expression}</span>
                  <div className="import-correlation-flow">
                    <div className="import-correlation-flow-row"><span className="import-correlation-flow-label">Found In</span><span className="import-correlation-flow-value">{foundAtLabel}</span></div>
                    {usedAtLocations.length > 0 && <div className="import-correlation-flow-row"><span className="import-correlation-flow-label">Used In</span><div className="import-correlation-flow-tags">{usedAtLocations.map(({ location, label }) => <span key={`${correlation.name}:${location}:label`} className="import-correlation-flow-chip">{label}</span>)}</div></div>}
                  </div>
                  {correlation.sample_value && <code>{correlation.sample_value}</code>}
                  {(foundAtActionIndex !== null || usedAtTargets.length > 0) && (
                    <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                      {foundAtActionIndex !== null && <button className="btn btn-secondary btn-sm" type="button" onClick={() => onSelectAction(foundAtActionIndex)}>Open {correlation.found_at}</button>}
                      {usedAtTargets.map(({ location, actionIndex }) => <button key={`${correlation.name}:${location}`} className="btn btn-secondary btn-sm" type="button" onClick={() => onSelectAction(actionIndex)}>Open {location}</button>)}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </section>
  )
}

export function ImportReviewCommitPanel({
  assistantChangeLog,
  controller,
  editorHistoryPastCount,
  jsonTextLength,
  migrationSuggestions,
  onApplyMigrationSuggestion,
  onSelectAction,
  script,
}: {
  assistantChangeLog: AssistantChangeLogItem[]
  controller: ImportReviewController
  editorHistoryPastCount: number
  jsonTextLength: number
  migrationSuggestions: MigrationAssistantSuggestion[]
  onApplyMigrationSuggestion: (suggestion: MigrationAssistantSuggestion) => void
  onSelectAction: (actionIndex: number) => void
  script: ScriptDefinition
}) {
  const { draftDiffSummary, importMetadata, importSummary, reviewBaselineScript } = controller
  return (
    <>
      {importMetadata && (
        <div className="utility-card">
          <div className="utility-card-header">
            <strong>Migration Assistant</strong>
            <span>{migrationSuggestions.length} suggestion{migrationSuggestions.length === 1 ? '' : 's'}</span>
          </div>
          {migrationSuggestions.length > 0 ? (
            <div className="utility-stack">
              {migrationSuggestions.map(suggestion => (
                <div key={suggestion.key} className="utility-suggestion-card">
                  <strong>{suggestion.title}</strong>
                  <p>{suggestion.description}</p>
                  <button type="button" className="btn btn-sm btn-secondary" onClick={() => onApplyMigrationSuggestion(suggestion)}>{suggestion.applyLabel}</button>
                </div>
              ))}
            </div>
          ) : <div className="utility-empty-state">Imported draft cleanup suggestions are already applied.</div>}
          {assistantChangeLog.length > 0 && (
            <div className="utility-audit-log">
              <strong>Change Summary</strong>
              <ul className="utility-list">
                {assistantChangeLog.map(entry => <li key={entry.id} className="utility-list-item"><strong>{entry.title}</strong><span>{entry.summary}</span></li>)}
              </ul>
            </div>
          )}
        </div>
      )}

      {draftDiffSummary && (importMetadata || assistantChangeLog.length > 0 || editorHistoryPastCount > 0) && (
        <div className="utility-card">
          <div className="utility-card-header"><strong>Draft Diff Review</strong><span>{draftDiffSummary.changed ? 'Changed' : 'No changes'}</span></div>
          <div className="utility-metric-row">
            <span className="metric-badge">{draftDiffSummary.actionChanges} action</span>
            <span className="metric-badge">{draftDiffSummary.parameterChanges} parameter</span>
            <span className="metric-badge">{draftDiffSummary.dataSourceChanges} data</span>
          </div>
          {draftDiffSummary.details.length > 0 ? <ul className="utility-list">{draftDiffSummary.details.map((detail, index) => <li key={`imported-diff-${index}`} className="utility-list-item">{detail}</li>)}</ul> : <div className="utility-empty-state">Current draft still matches the review baseline.</div>}
          <DraftJSONReview reviewBaselineScript={reviewBaselineScript} script={script} jsonTextLength={jsonTextLength} />
        </div>
      )}

      {importSummary.correlationSuggestions.length > 0 && (
        <div className="utility-card">
          <div className="utility-card-header"><strong>Correlation Inspector</strong><span>{importSummary.correlationSuggestions.length} mappings</span></div>
          <div className="utility-stack">
            {controller.importedCorrelationFocusTargets.map(({ correlation, foundAtActionIndex, foundAtLabel, usedAtTargets }) => (
              <div key={`${correlation.name}:${correlation.found_at}`} className="utility-suggestion-card">
                <strong>{correlation.name}</strong>
                <p>{foundAtLabel}{usedAtTargets.length > 0 ? ` -> ${usedAtTargets.map(target => target.label).join(', ')}` : ''}</p>
                <div className="utility-inline-actions">
                  {foundAtActionIndex !== null && <button type="button" className="btn btn-sm btn-ghost" onClick={() => onSelectAction(foundAtActionIndex)}>Open source</button>}
                  {usedAtTargets.slice(0, 2).map(target => <button key={`${correlation.name}:${target.location}`} type="button" className="btn btn-sm btn-ghost" onClick={() => onSelectAction(target.actionIndex)}>Open use</button>)}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </>
  )
}
