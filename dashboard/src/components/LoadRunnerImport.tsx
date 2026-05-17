import { useEffect, useState, useRef } from 'react'
import { useApi } from '../hooks/useApi'
import { type ImportedScript, type LoadRunnerImportResult, type LoadRunnerImportSummary, type LoadRunnerMigrationReadiness, type LoadRunnerSupportMatrixEntry, type LoadRunnerSupportMatrixExample, type LoadRunnerSupportMatrixResponse } from '../types/scriptImports'
import { type BrowserReplaySession } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { buildReplayStartPayload, getImportSummary, summarizeInlineData } from './scriptEditorUtils'
import ScriptImportReview from './ScriptImportReview'
import { type DraftEditorFocus } from './useScriptsAuthoringState'
import './LoadRunnerImport.css'

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, unknown>
}

function getImportMetadata(script?: ImportedScript): ImportMetadata | null {
  return script?.metadata?.import || null
}

function toScriptDefinition(script: ImportedScript): ScriptDefinition {
  return script as unknown as ScriptDefinition
}

function getSummaryList(values?: string[]) {
  return Array.isArray(values) ? values : []
}

function isBrowserImport(script: ImportedScript | null): boolean {
  return script?.protocol === 'browser'
}

interface Props {
  projectId?: string
  onImported?: (script: ImportedScript) => void
  onEdit?: (script: ScriptDefinition, focus?: DraftEditorFocus) => void
  onClose?: () => void
}

type ReviewMode = 'validation' | 'conversion'

type LoadRunnerSupportMatrixSection = {
  key: string
  title: string
  items: LoadRunnerSupportMatrixEntry[]
  preview: string
}

export default function LoadRunnerImport({ projectId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [result, setResult] = useState<LoadRunnerImportResult | null>(null)
  const [reviewMode, setReviewMode] = useState<ReviewMode | null>(null)
  const [supportMatrix, setSupportMatrix] = useState<LoadRunnerSupportMatrixEntry[]>([])
  const [supportMatrixNotes, setSupportMatrixNotes] = useState<string[]>([])
  const [supportMatrixExamples, setSupportMatrixExamples] = useState<LoadRunnerSupportMatrixExample[]>([])
  const [replayValidationLoading, setReplayValidationLoading] = useState(false)
  const [replayValidationError, setReplayValidationError] = useState('')
  const [replayValidationWarnings, setReplayValidationWarnings] = useState<string[]>([])
  const [replayValidationSession, setReplayValidationSession] = useState<BrowserReplaySession | null>(null)

  const [options, setOptions] = useState({
    name: '', base_url: '', include_assertions: true, preserve_transactions: true,
    block_partial: true, block_compatibility_only: true, block_non_applicable: false,
  })
  const importedScript = result?.script ?? result?.scripts?.[0] ?? null
  const importMetadata = getImportMetadata(importedScript || undefined)
  const importSummary = getImportSummary(importMetadata)
  const reviewSummary: LoadRunnerImportSummary | null = result?.summary || null
  const projectSections = getSummaryList(reviewSummary?.project_sections).length > 0
    ? getSummaryList(reviewSummary?.project_sections)
    : importSummary.projectSections
  const runtimeFiles = getSummaryList(reviewSummary?.runtime_files).length > 0
    ? getSummaryList(reviewSummary?.runtime_files)
    : importSummary.runtimeFiles
  const parameterFiles = getSummaryList(reviewSummary?.parameter_files).length > 0
    ? getSummaryList(reviewSummary?.parameter_files)
    : importSummary.parameterFiles
  const unsupportedFunctions = result?.unsupported_elements.length
    ? result.unsupported_elements
    : importSummary.unsupportedFunctions
  const reviewSections = result?.review_sections || []
  const repairSuggestions = result?.repair_suggestions || []
  const supportMatrixSections = summarizeLoadRunnerSupportMatrix(supportMatrix)
  const migrationReadiness: LoadRunnerMigrationReadiness | null = reviewSummary?.migration_readiness || null
  const browserImport = isBrowserImport(importedScript)
  const generatedDataSources = importedScript
    ? (toScriptDefinition(importedScript).data_sources || []).map((source, index) => {
      const summary = summarizeInlineData(source)
      const columns = source.first_row_header === false
        ? source.columns || []
        : []
      return {
        name: source.name || `source_${index + 1}`,
        type: source.type || 'csv',
        rows: summary.rows,
        columns: columns.length > 0 ? columns : summary.columns,
      }
    })
    : []

  const openImportedScriptInEditor = (focus?: DraftEditorFocus) => {
		if (!importedScript || !onEdit) return
    onEdit(toScriptDefinition(importedScript), focus)
	}

  const resetReplayValidation = () => {
    setReplayValidationLoading(false)
    setReplayValidationError('')
    setReplayValidationWarnings([])
    setReplayValidationSession(null)
  }

  useEffect(() => {
    let cancelled = false

    api.getLoadRunnerSupportMatrix()
      .then((response: LoadRunnerSupportMatrixResponse) => {
        if (!cancelled) {
          setSupportMatrix(Array.isArray(response.support_matrix) ? response.support_matrix : [])
          setSupportMatrixNotes(Array.isArray(response.notes) ? response.notes : [])
          setSupportMatrixExamples(Array.isArray(response.tested_examples) ? response.tested_examples : [])
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSupportMatrix([])
          setSupportMatrixNotes([])
          setSupportMatrixExamples([])
        }
      })

    return () => {
      cancelled = true
    }
  }, [api])

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault(); setDragOver(false)
    const f = e.dataTransfer.files[0]
    if (f && (f.name.endsWith('.zip') || f.type === 'application/zip')) {
      setFile(f); setError(''); setNotice(''); setResult(null); setReviewMode(null); resetReplayValidation()
    } else { setError('Please upload a ZIP file containing a LoadRunner script folder.') }
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (f) { setFile(f); setError(''); setNotice(''); setResult(null); setReviewMode(null); resetReplayValidation() }
  }

  const runReview = async (mode: 'convert' | 'validate') => {
    if (!file) return
    setWorkingMode(mode); setError(''); setNotice(''); setResult(null); resetReplayValidation()
    try {
      const request = mode === 'validate' ? api.validateLoadRunnerImport : api.importLoadRunner
      const res = await request(file, {
        name: options.name || undefined,
        project_id: projectId,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
        preserve_transactions: options.preserve_transactions,
        migration_block_statuses: buildMigrationBlockStatuses(options),
      })
      setResult(res)
      setReviewMode(mode === 'validate' ? 'validation' : 'conversion')
      setNotice(mode === 'validate'
        ? 'Validation complete. Review the generated script, warnings, and preserved settings before saving.'
        : 'Conversion complete. Review the generated script, warnings, and preserved settings before saving.')
    } catch (error: unknown) {
      setError(getErrorMessage(error, mode === 'validate' ? 'Validation failed' : 'Import failed'))
    } finally { setWorkingMode(null) }
  }

  const handleSaveConverted = async () => {
    if (!importedScript) return
    try {
      await api.createScript(importedScript, projectId)
      onImported?.(importedScript)
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Failed to save script'))
    }
  }

  const handleValidateBrowserReplay = async () => {
    if (!importedScript) return
    const script = toScriptDefinition(importedScript)

    setReplayValidationLoading(true)
    setReplayValidationError('')
    setReplayValidationWarnings([])
    setReplayValidationSession(null)

    try {
      const res = await api.startDraftBrowserReplaySession(buildReplayStartPayload(script, {
        projectId,
        ignoreThinkTime: true,
      }))
      setReplayValidationSession(res.session)
      setReplayValidationWarnings(Array.isArray(res.warnings) ? res.warnings : [])
    } catch (error: unknown) {
      setReplayValidationError(getErrorMessage(error, 'Browser replay validation failed'))
    } finally {
      setReplayValidationLoading(false)
    }
  }

  return (
    <div className="lr-import">
      <div className="lr-import-header">
        <div>
          <h2>Import LoadRunner Script</h2>
          <p className="lr-subtitle">Upload a ZIP archive of your LoadRunner Web HTTP/HTML VUser script folder. The converter handles <code>web_url</code>, <code>web_submit_data</code>, <code>web_custom_request</code>, correlations, and think times.</p>
        </div>
        {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
      </div>

      {/* Drop zone */}
      <div className={`lr-dropzone ${dragOver ? 'drag-over' : ''} ${file ? 'has-file' : ''}`}
        onDragOver={e => { e.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        onClick={() => fileRef.current?.click()}>
        <input ref={fileRef} type="file" accept=".zip" onChange={handleFileSelect} hidden />
        {file ? (
          <div className="lr-file-info">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <div>
              <span className="lr-filename">{file.name}</span>
              <span className="lr-filesize">{(file.size / 1024).toFixed(1)} KB</span>
            </div>
            <button className="btn btn-sm btn-ghost" onClick={e => { e.stopPropagation(); setFile(null); setResult(null) }}>Change</button>
          </div>
        ) : (
          <div className="lr-drop-content">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p>Drop a LoadRunner <code>.zip</code> file here</p>
            <span className="lr-drop-hint">or click to browse</span>
          </div>
        )}
      </div>

      {/* Options */}
      <div className="lr-options">
        <h3 className="lr-section-title">Conversion Options</h3>
        <div className="lr-options-grid">
          <label><span>Script Name (optional)</span>
            <input value={options.name} onChange={e => setOptions(o => ({ ...o, name: e.target.value }))} placeholder="Auto-detected from .usr file" />
          </label>
          <label><span>Base URL Override (optional)</span>
            <input value={options.base_url} onChange={e => setOptions(o => ({ ...o, base_url: e.target.value }))} placeholder="Auto-detected from first URL" />
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.include_assertions} onChange={e => setOptions(o => ({ ...o, include_assertions: e.target.checked }))} />
            <span>Add default status_code assertions</span>
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.preserve_transactions} onChange={e => setOptions(o => ({ ...o, preserve_transactions: e.target.checked }))} />
            <span>Preserve transaction names in generated actions</span>
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.block_partial} onChange={e => setOptions(o => ({ ...o, block_partial: e.target.checked }))} />
            <span>Treat partial runtime settings as blockers</span>
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.block_compatibility_only} onChange={e => setOptions(o => ({ ...o, block_compatibility_only: e.target.checked }))} />
            <span>Treat preserved-only runtime settings as blockers</span>
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.block_non_applicable} onChange={e => setOptions(o => ({ ...o, block_non_applicable: e.target.checked }))} />
            <span>Treat non-applicable runtime settings as blockers</span>
          </label>
        </div>
      </div>

      <div className="settings-notice">
        SOAP-aware detection is enabled for LoadRunner request assets that already resolve to SOAP-style calls or envelopes. Java-over-HTTP import remains unavailable until the dedicated runtime and transport format are implemented.
      </div>

      {supportMatrix.length > 0 && (
        <div className="lr-preserved">
          <div className="lr-preserved-header">
            <h4>Runtime Settings Coverage</h4>
            <span>Code-owned LoadRunner Web HTTP/HTML runtime support matrix</span>
          </div>

          <div className="lr-preserved-grid">
            {supportMatrixSections.map(section => (
              <div className="lr-preserved-card" key={section.key}>
                <span className="lr-preserved-label">{section.title}</span>
                <strong>{section.items.length}</strong>
                <small>{section.preview || 'No entries'}</small>
              </div>
            ))}
          </div>

          <div className="lr-warnings">
            <h4>Coverage Details</h4>
            {supportMatrixSections.map(section => (
              <details key={section.key}>
                <summary>{section.title} ({section.items.length})</summary>
                <ul>
                  {section.items.map(entry => (
                    <li key={entry.id}>{entry.area}: {entry.concept}</li>
                  ))}
                </ul>
              </details>
            ))}
          </div>

          {supportMatrixExamples.length > 0 && (
            <div className="lr-warnings">
              <h4>Tested Examples</h4>
              {supportMatrixExamples.map(example => (
                <div key={example.test} className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>{example.title}</strong>
                    <span>tested</span>
                  </div>
                  <p>{example.coverage}</p>
                  <ul><li>{example.test}</li></ul>
                </div>
              ))}
            </div>
          )}

          {supportMatrixNotes.length > 0 && (
            <div className="lr-warnings">
              <h4>Known Caveats</h4>
              <ul>{supportMatrixNotes.map((note, index) => <li key={index}>{note}</li>)}</ul>
            </div>
          )}
        </div>
      )}

      <div className="replay-panel-actions">
        <button className="btn btn-secondary btn-lg" onClick={() => runReview('validate')} disabled={!file || workingMode !== null}>
          {workingMode === 'validate' ? <><span className="spinner" /> Validating...</> : 'Validate Only'}
        </button>
        <button className="btn btn-primary btn-lg" onClick={() => runReview('convert')} disabled={!file || workingMode !== null}>
          {workingMode === 'convert' ? <><span className="spinner" /> Converting...</> : 'Convert LoadRunner Script'}
        </button>
      </div>

      {error && <div className="lr-error">{error}</div>}
      {notice && <div className="settings-notice">{notice}</div>}

      {/* Results */}
      {result && importedScript && (
        <div className="lr-result">
          <ScriptImportReview
            title={reviewMode === 'validation' ? 'Validation Review' : 'Conversion Complete'}
            subtitle={reviewMode === 'validation'
              ? 'This preview came from the dry-run validator. You can still save the reviewed script if the output looks correct.'
              : 'Review the generated script, preserved metadata, and warnings before saving.'}
            script={importedScript}
            warnings={result.warnings}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            saveLabel={reviewMode === 'validation' ? 'Save Reviewed Script' : 'Save to Script Library'}
            onSave={handleSaveConverted}
            onEdit={onEdit ? () => openImportedScriptInEditor() : undefined}
          >

            {browserImport && (
              <div className="lr-warnings">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>Browser Replay Validation</strong>
                    <span>{replayValidationSession?.status || 'not run'}</span>
                  </div>
                  <p>Run a draft replay against the converted browser script before saving it to the library.</p>
                  <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start' }}>
                    <button className="btn btn-secondary" onClick={handleValidateBrowserReplay} disabled={replayValidationLoading}>
                      {replayValidationLoading ? <><span className="spinner" /> Validating Replay...</> : 'Validate Browser Replay'}
                    </button>
                  </div>
                  {replayValidationSession && (
                    <ul>
                      <li>Session: {replayValidationSession.id}</li>
                      <li>Status: {replayValidationSession.status}</li>
                      <li>Group: {replayValidationSession.group}</li>
                    </ul>
                  )}
                  {replayValidationWarnings.length > 0 && (
                    <ul>{replayValidationWarnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul>
                  )}
                  {replayValidationError && <div className="lr-error">{replayValidationError}</div>}
                </div>
              </div>
            )}

            {/* Stats */}
            <div className="lr-stats-grid">
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.converted_actions}</span><span className="lr-stat-label">Actions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.extractors}</span><span className="lr-stat-label">Extractors</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.transactions}</span><span className="lr-stat-label">Transactions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.think_times}</span><span className="lr-stat-label">Think Times</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.total_functions}</span><span className="lr-stat-label">Total LR Functions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.skipped}</span><span className="lr-stat-label">Skipped</span></div>
            </div>

            {unsupportedFunctions.length > 0 && (
              <div className="lr-warnings">
                <h4>Unsupported LoadRunner Functions ({unsupportedFunctions.length})</h4>
                <ul>{unsupportedFunctions.map((fnName, index) => <li key={`${fnName}-${index}`}>{fnName}</li>)}</ul>
              </div>
            )}

            {reviewSections.length > 0 && (
              <div className="lr-warnings">
                <h4>Import Review Sections ({reviewSections.length})</h4>
                {reviewSections.map(section => (
                  <div key={section.key} className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>{section.title}</strong>
                      <span>{section.severity}</span>
                    </div>
                    {section.summary && <p>{section.summary}</p>}
                    {section.items.length > 0 && (
                      <ul>{section.items.slice(0, 6).map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}</ul>
                    )}
                  </div>
                ))}
              </div>
            )}

            {repairSuggestions.length > 0 && (
              <div className="lr-warnings">
                <h4>Repair Suggestions ({repairSuggestions.length})</h4>
                {repairSuggestions.map(suggestion => (
                  <div key={suggestion.id} className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>{suggestion.title}</strong>
                      <span>{suggestion.severity}</span>
                    </div>
                    {suggestion.target_action && <p>Action: {suggestion.target_action}</p>}
                    {suggestion.description && <p>{suggestion.description}</p>}
                    {suggestion.action && <ul><li>{suggestion.action}</li></ul>}
                    {suggestion.target_action && onEdit && (
                      <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start' }}>
                        <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetAction: suggestion.target_action })}>
                          Open Target Action In Editor
                        </button>
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}

            {importMetadata && (
              <div className="lr-preserved">
                <div className="lr-preserved-header">
                  <h4>Preserved LoadRunner Settings</h4>
                  <span>These values will be saved with the Stormrunner script.</span>
                </div>

                <div className="lr-preserved-grid">
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Project File</span>
                    <strong>{reviewSummary?.project_file || importMetadata.source_script_name || importedScript.name}</strong>
                    <small>{importMetadata.source || 'loadrunner'}</small>
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Mapped Runtime</span>
                    <strong>{reviewSummary?.mapped_runtime_file || 'None mapped'}</strong>
                    <small>{runtimeFiles.join(', ') || 'No runtime files found'}</small>
                    {onEdit && (
                      <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start' }}>
                        <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetSection: 'runtime' })}>
                          Open Runtime Settings In Editor
                        </button>
                      </div>
                    )}
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Parameter Files</span>
                    <strong>{parameterFiles.length}</strong>
                    <small>{parameterFiles.join(', ') || 'No parameter files found'}</small>
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Project Sections</span>
                    <strong>{projectSections.length}</strong>
                    <small>{projectSections.join(', ') || 'None captured'}</small>
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Unsupported Functions</span>
                    <strong>{reviewSummary?.unsupported_function_count ?? unsupportedFunctions.length}</strong>
                    <small>{unsupportedFunctions.slice(0, 4).join(', ') || 'None'}</small>
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Source</span>
                    <strong>{importMetadata.source || 'loadrunner'}</strong>
                    <small>{importedScript.name}</small>
                  </div>
                  {migrationReadiness && (
                    <div className="lr-preserved-card">
                      <span className="lr-preserved-label">Migration Readiness</span>
                      <strong>{migrationReadiness.score_pct}%</strong>
                      <small>{migrationReadiness.blocked ? 'Blocked by current policy' : migrationReadiness.summary || 'Ready for review'}</small>
                    </div>
                  )}
                </div>

                {importSummary.parameterFileSections.length > 0 && (
                  <div className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>Parameter File Mappings</strong>
                      <span>{importSummary.parameterFileSections.length}</span>
                    </div>
                    <ul>
                      {importSummary.parameterFileSections.map((section) => (
                        <li key={`${section.file}:${section.name}`}>
                          <strong>{section.file}</strong>
                          {`: ${section.name}`}
                          {renderParameterMappingDetails(section.values)}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                {generatedDataSources.length > 0 && (
                  <div className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>Generated Data Sources</strong>
                      <span>{generatedDataSources.length}</span>
                    </div>
                    <ul>
                      {generatedDataSources.map((source) => (
                        <li key={source.name}>
                          <strong>{source.name}</strong>
                          {`: ${source.type} • ${source.rows} row${source.rows === 1 ? '' : 's'} • `}
                          {typeof source.columns === 'number'
                            ? `${source.columns} column${source.columns === 1 ? '' : 's'}`
                            : source.columns.join(', ') || 'No columns'}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                <details className="lr-preserved-details">
                  <summary>View preserved metadata JSON</summary>
                  <pre>{JSON.stringify(importMetadata, null, 2)}</pre>
                </details>
              </div>
            )}
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}

function summarizeLoadRunnerSupportMatrix(entries: LoadRunnerSupportMatrixEntry[]): LoadRunnerSupportMatrixSection[] {
  return [
    buildLoadRunnerSupportMatrixSection('executed', 'Executed', entries),
    buildLoadRunnerSupportMatrixSection('partial', 'Partial', entries),
    buildLoadRunnerSupportMatrixSection('compatibility_only', 'Compatibility-only', entries),
    buildLoadRunnerSupportMatrixSection('non_applicable', 'Non-applicable', entries),
    buildLoadRunnerSupportMatrixSection('blocked', 'Blocked', entries),
  ]
}

function buildMigrationBlockStatuses(options: {
  block_partial: boolean
  block_compatibility_only: boolean
  block_non_applicable: boolean
}) {
  const statuses = ['blocked']
  if (options.block_partial) statuses.push('partial')
  if (options.block_compatibility_only) statuses.push('compatibility_only')
  if (options.block_non_applicable) statuses.push('non_applicable')
  return statuses
}

function buildLoadRunnerSupportMatrixSection(status: string, title: string, entries: LoadRunnerSupportMatrixEntry[]): LoadRunnerSupportMatrixSection {
  const items = entries.filter(entry => entry.status === status)
  return {
    key: status,
    title,
    items,
    preview: items.slice(0, 2).map(entry => `${entry.area}: ${entry.concept}`).join(' • '),
  }
}

function renderParameterMappingDetails(values: Record<string, string>) {
  const details: string[] = []
  if (values.Type) details.push(values.Type)
  if (values.ColumnName) details.push(`column ${values.ColumnName}`)
  if (values.Table) details.push(`table ${values.Table}`)
  if (values.SelectNextRow) details.push(values.SelectNextRow)
  if (values.UpdateValueOn) details.push(values.UpdateValueOn)
  return details.length > 0 ? ` • ${details.join(' • ')}` : ''
}
