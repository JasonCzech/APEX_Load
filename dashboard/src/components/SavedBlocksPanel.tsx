import { useEffect, useId, useState } from 'react'

import { useScriptApi } from '../hooks/api/useScriptApi'
import { ApiError } from '../hooks/apiClient'
import type { AuthoringComponent } from '../types'
import { formatDateTime } from '../utils/dateTime'
import { getErrorMessage } from '../utils/errorUtils'
import SettingsConfirmDialog from './SettingsConfirmDialog'
import { isSavedBlock } from './savedBlocks'
import './parityPanels.css'

type BlockStep = { name?: string; type?: string }

/** A block's steps, by where they run, for its preview. */
function blockPhases(block: AuthoringComponent): Array<{ label: string; steps: BlockStep[] }> {
  return [
    { label: 'Setup', steps: (block.init || []) as BlockStep[] },
    { label: 'Steps', steps: (block.actions || []) as BlockStep[] },
    { label: 'Teardown', steps: (block.teardown || []) as BlockStep[] },
  ].filter(phase => phase.steps.length > 0)
}

function stepCount(block: AuthoringComponent): number {
  return (block.init?.length || 0) + (block.actions?.length || 0) + (block.teardown?.length || 0)
}

function protocolLabel(protocols: string[] | undefined): string {
  if (!protocols?.length || protocols.includes('*')) return 'any protocol'
  return protocols.join(', ')
}

/**
 * A project's saved blocks: steps saved from one script (Save as block) and
 * inserted into others from the editor's Add action from a block. Inserting
 * copies the steps, so a block has no users to list and deleting it changes
 * no script.
 */
export default function SavedBlocksPanel({ projectId, canManage }: { projectId: string; canManage: boolean }) {
  const api = useScriptApi()
  const titleId = useId()
  const [blocks, setBlocks] = useState<AuthoringComponent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [turnedOff, setTurnedOff] = useState(false)
  const [notice, setNotice] = useState('')
  const [expanded, setExpanded] = useState<string | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<AuthoringComponent | null>(null)
  const [busy, setBusy] = useState(false)
  const [version, setVersion] = useState(0)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    setTurnedOff(false)
    api.listAuthoringComponents({ projectId })
      .then(response => {
        if (cancelled) return
        const saved = (response.components || []).filter(isSavedBlock)
        // Newest first, by time (saved_at strings vary in fractional digits); undated last.
        const savedTime = (block: AuthoringComponent) => (block.saved_at ? Date.parse(block.saved_at) : NaN) || 0
        setBlocks(saved.sort((left, right) => savedTime(right) - savedTime(left)))
      })
      .catch((cause: unknown) => {
        if (cancelled) return
        // The domain feature policy can turn reusable blocks off.
        if (cause instanceof ApiError && cause.status === 403) setTurnedOff(true)
        else setError(getErrorMessage(cause, 'Unable to load saved blocks.'))
      })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [api, projectId, version])

  const deleteBlock = async (block: AuthoringComponent) => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await api.deleteAuthoringComponent(block.id)
      setConfirmDelete(null)
      setNotice(`Deleted saved block “${block.name}”. Scripts that inserted it keep their copies of its steps.`)
      setVersion(current => current + 1)
    } catch (cause: unknown) {
      setError(getErrorMessage(cause, 'Unable to delete the saved block.'))
    } finally {
      setBusy(false)
    }
  }

  return <section className="pp-panel" aria-labelledby={titleId}>
    <div className="pp-toolbar">
      <div className="pp-toolbar-copy">
        <h3 id={titleId} className="pp-panel-title">Saved blocks</h3>
        <p className="pp-muted">Steps saved from one script to insert into others. In the script editor, select steps and choose Save as block; insert one from Add action from a block. Inserting copies the steps, so changing or deleting a block here changes no script.</p>
        {!canManage && <p className="pp-muted">You can view saved blocks. Editors and project admins can delete them.</p>}
      </div>
    </div>
    {notice && <p className="settings-notice" role="status">{notice}</p>}
    {error && <p className="settings-error-inline" role="alert">{error}</p>}
    {turnedOff
      ? <p className="pp-empty" role="status">Saved blocks are turned off for this domain. A domain administrator can turn on reusable blocks in Access › Domain feature policy.</p>
      : loading ? <p className="pp-muted" role="status">Loading…</p>
        : blocks.length === 0 ? <div className="pp-empty">No saved blocks yet.</div>
          : <div className="pp-list">
            {blocks.map(block => {
              const open = expanded === block.id
              const previewId = `${titleId}-${block.id}`
              const steps = stepCount(block)
              return <div key={block.id} className="pp-row">
                <div className="pp-row-main">
                  <button type="button" className="pp-row-title pp-row-title-button" aria-expanded={open} aria-controls={previewId} onClick={() => setExpanded(open ? null : block.id)}>
                    {block.name}
                    <span className="pp-pill pp-pill-muted">{steps} step{steps === 1 ? '' : 's'}</span>
                  </button>
                  <div className="pp-row-meta">
                    {block.kind.replace(/_/g, ' ')} · {protocolLabel(block.protocols)}{block.saved_at ? ` · saved ${formatDateTime(block.saved_at, { fallback: '—' })}` : ''}
                  </div>
                  {block.summary && <p className="pp-muted">{block.summary}</p>}
                  {open && <div id={previewId} className="pp-sublist" role="group" aria-label={`Steps of ${block.name}`}>
                    {blockPhases(block).map(phase => <div key={phase.label}>
                      <strong>{phase.label}</strong>
                      <ol>{phase.steps.map((step, index) => <li key={index} className="pp-subrow">{step.name || 'Unnamed step'} <span className="pp-pill pp-pill-kind">{step.type || 'step'}</span></li>)}</ol>
                    </div>)}
                    {(block.data_sources?.length || 0) > 0 && <p className="pp-muted">Data sources: {block.data_sources!.map(source => source.name).join(', ')}</p>}
                    {(block.parameters?.length || 0) > 0 && <p className="pp-muted">Parameters: {block.parameters!.map(parameter => parameter.name).join(', ')}</p>}
                  </div>}
                </div>
                {canManage && <div className="pp-row-actions">
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setConfirmDelete(block)}>Delete</button>
                </div>}
              </div>
            })}
          </div>}
    <SettingsConfirmDialog
      open={Boolean(confirmDelete)}
      title="Delete saved block"
      confirmLabel="Delete saved block"
      confirmDisabled={busy}
      onCancel={() => setConfirmDelete(null)}
      onConfirm={() => { if (confirmDelete) void deleteBlock(confirmDelete) }}
    >
      <p>Delete saved block <strong>{confirmDelete?.name}</strong>? Scripts that inserted it keep their copies of its steps.</p>
    </SettingsConfirmDialog>
  </section>
}
