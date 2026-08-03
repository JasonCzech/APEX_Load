import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { ApiError } from '../hooks/apiClient'
import { useTestApi } from '../hooks/api/useTestApi'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { getErrorMessage } from '../utils/errorUtils'
import type { TestGroupRuntimeSettingsUpdateResult, TestGroupRuntimeSettingsView } from '../types'
import type { RuntimeSettingsOverride, ScenarioRuntimeSettingsDraft } from '../types/runtimeSettings'
import type { ScriptConfig } from '../types/scripts'
import RuntimeSettingsGroupEditor from './RuntimeSettingsGroupEditor'
import { buildRuntimeSettingsDraft } from './scenarioDraftUtils'
import { buildRuntimeSettingsPayload } from './scenarioPayloadUtils'
import {
  changedRuntimeFieldKeys,
  EDITABLE_RUNTIME_FIELD_KEYS,
  effectiveRuntimeFieldValue,
} from './groupRuntimeSettingsModel'
import {
  formatRuntimeDispositionLabel,
  summarizeImportedRuntimeDispositions,
  type ImportedRuntimeDispositions,
  type ImportedRuntimeFileSections,
} from './scriptEditorRuntimeSettingsModel'
import './TestHub.css'
import './ScriptEditor.css'
import './GroupRunSettingsDialog.css'

interface Props {
  testId: string
  groupName: string
  onClose: () => void
  onSaved: (result: TestGroupRuntimeSettingsUpdateResult) => void
}

const FIELD_SUMMARY_LABELS: Record<(typeof EDITABLE_RUNTIME_FIELD_KEYS)[number], string> = {
  iterations: 'Iterations',
  pacing: 'Pacing',
  think_time: 'Think time',
  error_handling: 'Error handling',
  browser: 'Browser device',
  log_level: 'Log level',
  auto_transactions: 'Auto-transactions',
  base_url: 'Base URL',
  log: 'Logging',
  run_logic: 'Run logic',
  http: 'HTTP transport',
  web_runtime: 'Web runtime',
  browser_config: 'Browser config',
  browser_emulation: 'Browser emulation',
  java_vuser: 'Java Env',
}

/** Rebuild a partial ScriptConfig from the masked base-script settings projection
 *  so the group editor can show the script layer in its inheritance display. */
function scriptConfigFromSettings(settings?: RuntimeSettingsOverride): ScriptConfig | undefined {
  if (!settings) return undefined
  return {
    base_url: settings.base_url || '',
    ...(settings.pacing ? { pacing: settings.pacing } : {}),
    ...(settings.think_time ? { think_time: settings.think_time } : {}),
    ...(settings.error_handling ? { error_handling: settings.error_handling } : {}),
    ...(settings.log ? { log: settings.log } : {}),
    ...(settings.run_logic ? { run_logic: settings.run_logic } : {}),
    ...(settings.http ? { http: settings.http } : {}),
    ...(settings.web_runtime ? { web_runtime: settings.web_runtime } : {}),
    ...(settings.browser_config ? { browser: settings.browser_config } : {}),
    ...(settings.browser_emulation ? { browser_emulation: settings.browser_emulation } : {}),
    ...(settings.java_vuser ? { java_vuser: settings.java_vuser } : {}),
  } as ScriptConfig
}

function describeRuntimeValue(value: unknown): string {
  if (value === undefined || value === null) return '—'
  if (typeof value === 'string') return value || '—'
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value)) {
    return value.length === 0 ? '—' : value.map(entry => describeRuntimeValue(entry)).join(', ')
  }
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, entryValue]) => entryValue !== undefined && entryValue !== null && entryValue !== '')
  if (entries.length === 0) return '—'
  return entries
    .map(([entryKey, entryValue]) => `${entryKey}: ${typeof entryValue === 'object' ? JSON.stringify(entryValue) : String(entryValue)}`)
    .join(' · ')
}

/** Read-only view of the raw VuGen runtime settings the importer preserved on
 *  this group's script, with the per-key disposition the import recorded.
 *  Display is bounded (sections and rows-per-section); the Script Editor's
 *  imported-source panel remains the unabridged view. */
function ImportedRuntimeSourcePanel({ source }: { source: ImportedRuntimeSource }) {
  const summary = summarizeImportedRuntimeDispositions(source.dispositions)
  const fileNames = Object.keys(source.files).sort()
  const sections = fileNames.flatMap(fileName =>
    Object.keys(source.files[fileName]).sort().map(sectionName => ({
      fileName,
      sectionName,
      keys: Object.entries(source.files[fileName][sectionName]),
    })))
  const visibleSections = sections.slice(0, IMPORTED_SOURCE_SECTION_CAP)
  return (
    <details className="settings-section imported-runtime-file group-run-settings-imported">
      <summary className="section-toggle">
        <span>Imported source runtime settings</span>
        <span className="repository-status-pill imported-runtime-file-tag">
          {summary.total > 0
            ? `${summary.mapped} mapped · ${summary.compatibility} compatibility · ${summary.nonApplicable} not applicable`
            : `${fileNames.length} file${fileNames.length === 1 ? '' : 's'}`}
        </span>
      </summary>
      {visibleSections.map(({ fileName, sectionName, keys }) => {
        const visibleKeys = keys.slice(0, IMPORTED_SOURCE_ROW_CAP)
        return (
          <div key={`${fileName}-${sectionName}`} className="section-body imported-runtime-section">
            <h5 className="panel-title cell-mono imported-runtime-section-title">{fileName} · [{sectionName}]</h5>
            <div className="script-parameter-table-wrap imported-runtime-table-wrap">
              <table className="script-parameter-table imported-runtime-table group-run-settings-imported-table">
                <tbody>
                  {visibleKeys.map(([key, value]) => {
                    const disposition = source.dispositions[fileName]?.[sectionName]?.[key]
                    return (
                      <tr key={key}>
                        <td className="cell-mono imported-runtime-key">{key}</td>
                        <td className="cell-mono imported-runtime-value">{value || '—'}</td>
                        <td>
                          <span
                            className={`repository-status-pill imported-runtime-disposition ${
                              disposition?.status === 'mapped'
                                ? 'healthy'
                                : disposition?.status === 'compatibility_only'
                                  ? 'warning'
                                  : disposition?.status === 'unclassified' || !disposition
                                    ? 'danger'
                                    : ''
                            }`}
                          >
                            {formatRuntimeDispositionLabel(disposition)}
                          </span>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            {keys.length > visibleKeys.length && (
              <p className="new-test-inline-note group-run-settings-imported-more">… {keys.length - visibleKeys.length} more keys in this section.</p>
            )}
          </div>
        )
      })}
      {sections.length > visibleSections.length && (
        <p className="new-test-inline-note group-run-settings-imported-more">
          … {sections.length - visibleSections.length} more sections not shown; open the script in the Script Editor for the full imported source.
        </p>
      )}
      {source.truncated && (
        <p className="new-test-inline-note group-run-settings-imported-more">
          This immutable run snapshot is truncated to a safe preview; open the original import review for the full source evidence.
        </p>
      )}
    </details>
  )
}

function runtimeLayerClaimsField(settings: RuntimeSettingsOverride | undefined, key: string): boolean {
  if (!settings) return false
  switch (key) {
    case 'iterations':
    case 'auto_transactions':
      return settings.run_logic !== undefined || settings[key] !== undefined
    case 'log_level':
      return settings.log !== undefined || settings.log_level !== undefined
    case 'browser':
      return settings.browser_config !== undefined || settings.browser !== undefined
    default:
      return (settings as unknown as Record<string, unknown>)[key] !== undefined
  }
}

function runtimeValueSourceLabel(view: TestGroupRuntimeSettingsView, key: string): string {
  if (runtimeLayerClaimsField(view.runtime_settings, key)) return 'Group override'
  if (runtimeLayerClaimsField(view.test_runtime_settings, key)) return 'Scenario default'
  if (runtimeLayerClaimsField(view.script_settings, key)) return 'Script'
  return 'Engine default'
}

interface ImportedRuntimeSource {
  files: Record<string, ImportedRuntimeFileSections>
  dispositions: ImportedRuntimeDispositions
  truncated?: boolean
}

const IMPORTED_SOURCE_ROW_CAP = 30
const IMPORTED_SOURCE_SECTION_CAP = 12

export default function GroupRuntimeSettingsDialog({ testId, groupName, onClose, onSaved }: Props) {
  const api = useTestApi()
  const systemApi = useSystemApi()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const draftRevisionRef = useRef('')
  const titleId = useId()
  const [view, setView] = useState<TestGroupRuntimeSettingsView | null>(null)
  const [engineDefaults, setEngineDefaults] = useState<ScriptConfig | undefined>()
  const [draft, setDraft] = useState<ScenarioRuntimeSettingsDraft | undefined>()
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')
  const [conflict, setConflict] = useState(false)

  const requestClose = useCallback(() => {
    if (saving) return
    onClose()
  }, [onClose, saving])

  useModalDialog({
    dialogRef,
    initialFocusRef: closeButtonRef,
    onClose: saving ? undefined : requestClose,
  })

  // preserveDraft refreshes the lifecycle view (editable flag, reason banner)
  // without discarding the user's in-progress edits or the visible error — used
  // after a failed save, where most errors (validation, review-blocked) are
  // retryable and resetting the form would throw the edits away.
  const loadView = useCallback((options?: { preserveDraft?: boolean }) => {
    const preserveDraft = options?.preserveDraft === true
    if (!preserveDraft) {
      setLoading(true)
      setError('')
      setConflict(false)
    }
    return api.getTestGroupRuntimeSettings(testId, groupName)
      .then(loaded => {
        if (preserveDraft) {
          if (draftRevisionRef.current && loaded.revision !== draftRevisionRef.current) {
            setConflict(true)
            setError('Runtime settings changed in another session. Reload settings and re-apply your edits.')
          }
          // Refresh only lifecycle fields. In particular, never adopt a newer
          // revision while retaining the old draft: doing so would let a retry
          // overwrite the edit that produced that revision.
          setView(current => current
            ? {
                ...current,
                status: loaded.status,
                editable: loaded.editable,
                editable_reason: loaded.editable_reason,
              }
            : loaded)
          if (!draftRevisionRef.current) draftRevisionRef.current = loaded.revision
        } else {
          draftRevisionRef.current = loaded.revision
          setView(loaded)
          setDraft(buildRuntimeSettingsDraft(loaded.runtime_settings))
        }
        return loaded
      })
      .catch((loadError: unknown) => {
        if (!preserveDraft) {
          setError(getErrorMessage(loadError, 'Unable to load group runtime settings'))
        }
        return null
      })
      .finally(() => {
        if (!preserveDraft) setLoading(false)
      })
  }, [api, groupName, testId])

  useEffect(() => {
    let cancelled = false
    void loadView()
    systemApi.getConfigDefaults()
      .then(response => {
        if (!cancelled) setEngineDefaults(response.config)
      })
      .catch(() => {
        // Engine defaults only enrich the inheritance display; the dialog stays usable.
      })
    return () => {
      cancelled = true
    }
  }, [loadView, systemApi])

  const importedSource: ImportedRuntimeSource | null = useMemo(() => {
    const source = view?.imported_runtime_source
    if (!source?.files || Object.keys(source.files).length === 0) return null
    return {
      files: source.files,
      dispositions: source.dispositions || {},
      truncated: source.truncated,
    }
  }, [view])

  const editorGroups = useMemo(() => view
    ? [{
        name: view.group,
        scriptId: view.script_id || '',
        protocol: view.protocol,
        scriptConfig: scriptConfigFromSettings(view.script_settings),
      }]
    : [], [view])

  const handleSave = useCallback(() => {
    if (!view || !draft) return
    const payload = { ...(buildRuntimeSettingsPayload(draft) || {}) }
    delete payload.env
    delete payload.redaction_rules
    // Round-trip the untouched baseline through the same draft -> payload
    // transform as the edited state so materialization artifacts (e.g. the
    // error-handling section splaying absent flags to explicit false) cancel
    // out instead of flagging phantom changes.
    const baseline = buildRuntimeSettingsPayload(buildRuntimeSettingsDraft(view.runtime_settings)) || {}
    const fields = changedRuntimeFieldKeys(baseline as RuntimeSettingsOverride, payload as RuntimeSettingsOverride)
    if (fields.length === 0) {
      setStatus('No changes to save.')
      return
    }
    setSaving(true)
    setError('')
    setStatus('')
    api.updateTestGroupRuntimeSettings(testId, view.group, {
      runtime_settings: payload as RuntimeSettingsOverride,
      fields,
      expected_revision: view.revision,
    })
      .then(result => {
        onSaved(result)
      })
      .catch((saveError: unknown) => {
        if (saveError instanceof ApiError && saveError.status === 409) {
          setConflict(true)
          setError('Runtime settings changed in another session. Reload settings and re-apply your edits.')
          return
        }
        setError(getErrorMessage(saveError, 'Unable to save runtime settings'))
        // Refresh only the lifecycle state (the run may have started, flipping
        // the dialog read-only) while keeping the user's edits and the error.
        void loadView({ preserveDraft: true })
      })
      .finally(() => setSaving(false))
  }, [api, draft, loadView, onSaved, testId, view])

  const editable = Boolean(view?.editable)

  // Portaled to <body>: the app layout carries a transform, which would trap
  // the fixed overlay in a lower stacking context than the sidebar.
  return createPortal(
    <div className="editor-dialog-backdrop group-run-settings-overlay" role="presentation">
      <div
        ref={dialogRef}
        className="editor-dialog group-run-settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <header className="editor-dialog-header group-run-settings-header">
          <div>
            <h2 id={titleId} className="panel-title">Runtime Settings — {groupName}</h2>
            <p className="editor-dialog-subtitle">
              {view?.script_name ? `${view.script_name} · ` : ''}
              {view?.protocol ? `${view.protocol} · ` : ''}
              {view ? `run ${view.ref_key || view.test_id}` : 'Loading run details…'}
            </p>
          </div>
          <button ref={closeButtonRef} type="button" className="btn btn-sm" onClick={requestClose} disabled={saving}>
            Close
          </button>
        </header>

        {(Boolean(view && !editable) || error || status) && (
          <div className="editor-dialog-body group-run-settings-messages">
            {view && !editable && (
              <div className="settings-inline-warning group-run-settings-banner" role="note">
                Read-only: {view.editable_reason || 'the run has already started.'}
              </div>
            )}
            {error && <div className="settings-inline-error group-run-settings-error" role="alert">{error}</div>}
            {status && <div className="settings-inline-notice group-run-settings-status" role="status">{status}</div>}
          </div>
        )}

        <div className="editor-dialog-body group-run-settings-body">
          {loading && <p className="new-test-inline-note group-run-settings-loading">Loading group runtime settings…</p>}
          {!loading && view && editable && draft && (
            <RuntimeSettingsGroupEditor
              draftScope={`command-center:${view.test_id}`}
              groups={editorGroups}
              focusedIndex={0}
              onFocusChange={() => {}}
              scenarioDefault={view.test_runtime_settings}
              engineDefaults={engineDefaults}
              overrides={[draft]}
              onChange={(_, next) => setDraft(next ?? buildRuntimeSettingsDraft(undefined))}
              onApplyToGroups={() => {}}
              title="Run overrides"
              description="Overrides for this run's group; unset fields inherit the script default. Saved changes apply to this run only."
              hideApplyPanel
            />
          )}
          {!loading && view && !editable && (
            <div className="script-parameter-table-wrap group-run-settings-summary">
              <table className="script-parameter-table" aria-label={`Effective runtime settings for ${view.group}`}>
                <thead>
                  <tr className="group-run-settings-summary-row header">
                    <th>Setting</th>
                    <th>Effective value</th>
                    <th>Source</th>
                  </tr>
                </thead>
                <tbody>
                  {EDITABLE_RUNTIME_FIELD_KEYS.map(key => {
                    const effective = effectiveRuntimeFieldValue(view.effective_settings, key)
                    return (
                      <tr key={key} className="group-run-settings-summary-row">
                        <td>{FIELD_SUMMARY_LABELS[key]}</td>
                        <td className="group-run-settings-value">{describeRuntimeValue(effective)}</td>
                        <td className="group-run-settings-source">{runtimeValueSourceLabel(view, key)}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
          {!loading && view && importedSource && (
            <ImportedRuntimeSourcePanel source={importedSource} />
          )}
        </div>

        <footer className="settings-status-header group-run-settings-footer">
          <span className="setting-desc group-run-settings-footnote">
            {editable
              ? 'Saved changes apply to this run only and are recorded in the post-run change history.'
              : 'Settings shown as configured for this run.'}
          </span>
          <div className="action-parameter-editor-actions group-run-settings-actions">
            {conflict && (
              <button type="button" className="btn" onClick={() => { void loadView() }} disabled={saving}>
                Reload settings
              </button>
            )}
            <button type="button" className="btn" onClick={requestClose} disabled={saving}>
              Cancel
            </button>
            {editable && (
              <button type="button" className="btn btn-accent" onClick={handleSave} disabled={saving || loading || conflict}>
                {saving ? 'Saving…' : 'Save to run'}
              </button>
            )}
          </div>
        </footer>
      </div>
    </div>,
    document.body,
  )
}
