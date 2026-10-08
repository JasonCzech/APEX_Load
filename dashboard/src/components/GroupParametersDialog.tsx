import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { ApiError } from '../hooks/apiClient'
import { useTestApi } from '../hooks/api/useTestApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { getErrorMessage } from '../utils/errorUtils'
import type { TestGroupParametersUpdateResult, TestGroupParametersView } from '../types'
import type { ScriptParameterDefinition } from '../types/scripts'
import { ParameterSettingsBody } from './ScriptEditorParameterFeatures'
import './ScriptEditor.css'
import './GroupRunSettingsDialog.css'

interface Props {
  testId: string
  groupName: string
  onClose: () => void
  onSaved: (result: TestGroupParametersUpdateResult) => void
  /** Where focus returns when the opener (the group menu item) has unmounted. */
  returnFocusSelector?: string
}

function describeParameterValue(parameter: ScriptParameterDefinition): string {
  if (parameter.type === 'file') {
    const source = parameter.data_source || 'data source'
    return parameter.column ? `${source} → ${parameter.column}` : source
  }
  if (parameter.values && parameter.values.length > 0) {
    const preview = parameter.values.slice(0, 4).join(', ')
    return parameter.values.length > 4 ? `${preview}, … (${parameter.values.length} values)` : preview
  }
  return parameter.type || 'list'
}

function describeParameterPolicy(parameter: ScriptParameterDefinition): string {
  const parts = [parameter.update || 'each_iteration', parameter.selection || 'sequential']
  if (parameter.on_exhaustion) parts.push(`on exhaustion: ${parameter.on_exhaustion}`)
  return parts.join(' · ')
}

export default function GroupParametersDialog({ testId, groupName, onClose, onSaved, returnFocusSelector }: Props) {
  const api = useTestApi()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const draftRevisionRef = useRef('')
  const titleId = useId()
  const [view, setView] = useState<TestGroupParametersView | null>(null)
  const [drafts, setDrafts] = useState<ScriptParameterDefinition[]>([])
  const [selectedIndex, setSelectedIndex] = useState(0)
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
    returnFocusSelector,
  })

  // preserveDraft refreshes the lifecycle view (editable flag, reason banner)
  // without discarding in-progress edits or the visible error — used after a
  // failed save, where validation errors are retryable.
  const loadView = useCallback((options?: { preserveDraft?: boolean }) => {
    const preserveDraft = options?.preserveDraft === true
    if (!preserveDraft) {
      setLoading(true)
      setError('')
      setConflict(false)
    }
    return api.getTestGroupParameters(testId, groupName)
      .then(loaded => {
        if (preserveDraft) {
          if (draftRevisionRef.current && loaded.revision !== draftRevisionRef.current) {
            setConflict(true)
            setError('Parameters changed in another session. Reload parameters and re-apply your edits.')
          }
          // Preserve the draft's original revision and baseline. Adopting the
          // refresh revision here would turn a stale retry into a valid write.
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
          setDrafts(structuredClone(loaded.parameters || []))
          setSelectedIndex(0)
        }
        return loaded
      })
      .catch((loadError: unknown) => {
        if (!preserveDraft) {
          setError(getErrorMessage(loadError, 'Unable to load group parameters'))
        }
        return null
      })
      .finally(() => {
        if (!preserveDraft) setLoading(false)
      })
  }, [api, groupName, testId])

  useEffect(() => {
    void loadView()
  }, [loadView])

  const editable = Boolean(view?.editable)
  const selectedParameter = drafts[selectedIndex]
  const isDirty = useMemo(
    () => view != null && JSON.stringify(drafts) !== JSON.stringify(view.parameters || []),
    [drafts, view],
  )

  const updateSelectedParameter = useCallback((field: keyof ScriptParameterDefinition, value: unknown) => {
    setDrafts(current => current.map((parameter, index) => {
      if (index !== selectedIndex) return parameter
      const next = { ...parameter }
      if (value === undefined || value === '') {
        delete next[field]
      } else {
        (next as Record<string, unknown>)[field] = value
      }
      return next
    }))
  }, [selectedIndex])

  const handleSave = useCallback(() => {
    if (!view) return
    if (!isDirty) {
      setStatus('No changes to save.')
      return
    }
    setSaving(true)
    setError('')
    setStatus('')
    api.updateTestGroupParameters(testId, view.group, {
      parameters: drafts,
      expected_revision: view.revision,
    })
      .then(result => {
        onSaved(result)
      })
      .catch((saveError: unknown) => {
        if (saveError instanceof ApiError && saveError.status === 409) {
          setConflict(true)
          setError('Parameters changed in another session. Reload parameters and re-apply your edits.')
          return
        }
        setError(getErrorMessage(saveError, 'Unable to save parameters'))
        // Refresh only the lifecycle state (the run may have started, flipping
        // the dialog read-only) while keeping the user's edits and the error.
        void loadView({ preserveDraft: true })
      })
      .finally(() => setSaving(false))
  }, [api, drafts, isDirty, loadView, onSaved, testId, view])

  const dataSourceSummary = useMemo(() => {
    if (!view?.data_sources || view.data_sources.length === 0) return ''
    return view.data_sources
      .map(source => {
        const columns = source.columns && source.columns.length > 0 ? `: ${source.columns.join(', ')}` : ''
        const rows = source.row_count ? ` (${source.row_count} rows)` : ''
        return `${source.name}${rows}${columns}`
      })
      .join(' · ')
  }, [view])

  // Portaled to <body>: the app layout carries a transform, which would trap
  // the fixed overlay in a lower stacking context than the sidebar.
  return createPortal(
    <DialogLayer><div className="editor-dialog-backdrop group-run-settings-overlay" role="presentation">
      <div
        ref={dialogRef}
        className="editor-dialog group-run-settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <header className="editor-dialog-header group-run-settings-header">
          <div>
            <h2 id={titleId} className="panel-title">Parameters — {groupName}</h2>
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

        {(Boolean(view && !editable) || error || status || dataSourceSummary) && (
          <div className="editor-dialog-body group-run-settings-messages">
            {view && !editable && (
              <div className="settings-inline-warning group-run-settings-banner" role="note">
                Read-only: {view.editable_reason || 'the run has already started.'}
              </div>
            )}
            {error && <div className="settings-inline-error group-run-settings-error" role="alert">{error}</div>}
            {status && <div className="settings-inline-notice group-run-settings-status" role="status">{status}</div>}
            {dataSourceSummary && (
              <div className="settings-inline-notice group-run-settings-datasources">Data sources: {dataSourceSummary}</div>
            )}
          </div>
        )}

        <div className="editor-dialog-body group-run-settings-body">
          {loading && <p className="new-test-inline-note group-run-settings-loading">Loading parameters…</p>}
          {!loading && view && drafts.length === 0 && (
            <p className="new-test-inline-note group-run-settings-loading">This group's script defines no parameters.</p>
          )}
          {!loading && view && drafts.length > 0 && editable && (
            <div className="results-compare-grid group-parameters-editor">
              <nav className="parameter-sidebar-list group-parameters-list" aria-label="Parameters">
                {drafts.map((parameter, index) => (
                  <button
                    key={parameter.name || index}
                    type="button"
                    className={`parameter-sidebar-item${index === selectedIndex ? ' selected active' : ''}`}
                    onClick={() => setSelectedIndex(index)}
                  >
                    <strong>{parameter.name}</strong>
                    <small>{describeParameterValue(parameter)}</small>
                  </button>
                ))}
              </nav>
              <div className="settings-subsection-layout group-parameters-detail">
                {selectedParameter && (
                  <ParameterSettingsBody
                    parameter={selectedParameter}
                    onUpdate={updateSelectedParameter}
                  />
                )}
              </div>
            </div>
          )}
          {!loading && view && drafts.length > 0 && !editable && (
            <div className="script-parameter-table-wrap group-run-settings-summary">
              <table className="script-parameter-table" aria-label={`Parameters for ${view.group}`}>
                <thead>
                  <tr className="group-run-settings-summary-row header">
                    <th>Parameter</th>
                    <th>Value</th>
                    <th>Policy</th>
                  </tr>
                </thead>
                <tbody>
                  {drafts.map((parameter, index) => (
                    <tr key={parameter.name || index} className="group-run-settings-summary-row">
                      <td>{parameter.name}</td>
                      <td className="group-run-settings-value">{describeParameterValue(parameter)}</td>
                      <td className="group-run-settings-source">{describeParameterPolicy(parameter)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        <footer className="settings-status-header group-run-settings-footer">
          <span className="setting-desc group-run-settings-footnote">
            {editable
              ? 'Saved changes apply to this run only and are recorded in the post-run change history.'
              : 'Parameters shown as configured for this run.'}
          </span>
          <div className="action-parameter-editor-actions group-run-settings-actions">
            {conflict && (
              <button type="button" className="btn" onClick={() => { void loadView() }} disabled={saving}>
                Reload parameters
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
    </div></DialogLayer>,
    document.body,
  )
}
