import { confirmAction } from './ux/confirmAction'
import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useTestApi } from '../hooks/api/useTestApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { getErrorMessage } from '../utils/errorUtils'
import { TestNote } from '../types'
import MarkdownRenderer from './MarkdownRenderer'
import './TestNotesDialog.css'

const NOTE_BODY_MAX_LENGTH = 64000

interface Props {
  testId: string
  testName?: string
  onClose: () => void
}

type NoteMode = 'preview' | 'edit' | 'new'

export default function TestNotesDialog({ testId, testName, onClose }: Props) {
  const api = useTestApi()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  const [notes, setNotes] = useState<TestNote[]>([])
  const [selectedId, setSelectedId] = useState<string>('')
  const [mode, setMode] = useState<NoteMode>('preview')
  const [draftBody, setDraftBody] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')

  const selectedNote = useMemo(
    () => notes.find((note) => note.id === selectedId) || null,
    [notes, selectedId],
  )
  const isDirty = (mode === 'new' && draftBody.trim() !== '') || (mode === 'edit' && selectedNote != null && draftBody !== selectedNote.body)

  const confirmDiscard = useCallback(async () => {
    if (!isDirty) return true
    return (await confirmAction('Discard unsaved note?', { title: 'Discard note', confirmLabel: 'Discard', destructive: true }))
  }, [isDirty])

  const requestClose = useCallback(async () => {
    if (saving) return
    if (!(await confirmDiscard())) return
    onClose()
  }, [confirmDiscard, onClose, saving])

  useModalDialog({
    dialogRef,
    initialFocusRef: closeButtonRef,
    onClose: saving ? undefined : requestClose,
  })

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    setStatus('')
    api.listTestNotes(testId)
      .then((response) => {
        if (cancelled) return
        const loaded = sortNotes(response.notes || [])
        setNotes(loaded)
        setSelectedId(loaded[0]?.id || '')
        setMode(loaded.length > 0 ? 'preview' : 'new')
        setDraftBody('')
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(getErrorMessage(err, 'Unable to load test notes'))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [api, testId])

  useEffect(() => {
    if (mode === 'edit' || mode === 'new') {
      window.setTimeout(() => textareaRef.current?.focus(), 0)
    }
  }, [mode])

  const canSave = draftBody.trim().length > 0 && !saving

  const selectNote = useCallback(async (note: TestNote) => {
    if (note.id === selectedId && mode === 'preview') return
    if (!(await confirmDiscard())) return
    setSelectedId(note.id)
    setMode('preview')
    setDraftBody('')
    setError('')
    setStatus('')
  }, [confirmDiscard, mode, selectedId])

  const startNewNote = useCallback(async () => {
    if (!(await confirmDiscard())) return
    setSelectedId('')
    setMode('new')
    setDraftBody('')
    setError('')
    setStatus('')
  }, [confirmDiscard])

  const startEditNote = useCallback(() => {
    if (!selectedNote) return
    setMode('edit')
    setDraftBody(selectedNote.body)
    setError('')
    setStatus('')
  }, [selectedNote])

  const cancelEdit = useCallback(() => {
    setError('')
    setStatus('')
    if (selectedNote) {
      setMode('preview')
      setDraftBody('')
      return
    }
    setMode(notes.length > 0 ? 'preview' : 'new')
    setSelectedId(notes[0]?.id || '')
    setDraftBody('')
  }, [notes, selectedNote])

  const saveNote = useCallback(async () => {
    if (!canSave) return
    setSaving(true)
    setError('')
    setStatus('')
    try {
      const saved = mode === 'edit' && selectedNote
        ? await api.updateTestNote(testId, selectedNote.id, draftBody)
        : await api.createTestNote(testId, draftBody)
      setNotes((current) => sortNotes(upsertNote(current, saved)))
      setSelectedId(saved.id)
      setMode('preview')
      setDraftBody('')
      setStatus(mode === 'edit' ? 'Updated' : 'Saved')
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to save test note'))
    } finally {
      setSaving(false)
    }
  }, [api, canSave, draftBody, mode, selectedNote, testId])

  const deleteNote = useCallback(async (target: TestNote) => {
    if (!(await confirmAction(`Delete test note "${noteDeleteTarget(target)}"?`))) return
    setSaving(true)
    setError('')
    setStatus('')
    try {
      await api.deleteTestNote(testId, target.id)
      const next = notes.filter((note) => note.id !== target.id)
      setNotes(next)
      if (selectedId === target.id) {
        setSelectedId(next[0]?.id || '')
        setMode(next.length > 0 ? 'preview' : 'new')
      }
      setDraftBody('')
      setStatus('Deleted')
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to delete test note'))
    } finally {
      setSaving(false)
    }
  }, [api, notes, selectedId, testId])

  return (
    <DialogLayer><div
      className="modal-overlay test-notes-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) requestClose()
      }}
    >
      <div
        ref={dialogRef}
        className="modal test-notes-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header test-notes-header">
          <div>
            <h3 id={titleId}>Test Notes</h3>
            <p id={descriptionId} className="page-subtitle">{testName || testId}</p>
          </div>
          <button ref={closeButtonRef} type="button" className="btn btn-secondary btn-sm" onClick={requestClose} disabled={saving}>Close</button>
        </div>

        {error && <p className="form-error test-notes-message" role="alert">{error}</p>}
        {status && !error && <p className="test-notes-status" role="status" aria-live="polite">{status}</p>}

        <div className="test-notes-grid">
          <aside className="test-notes-list-pane" aria-label="Saved test notes">
            <div className="test-notes-list-header">
              <span>{notes.length} {notes.length === 1 ? 'note' : 'notes'}</span>
              <button type="button" className="btn btn-primary btn-sm" onClick={startNewNote} disabled={saving}>
                New Note
              </button>
            </div>
            <div className="test-notes-list">
              {loading && <p className="test-notes-empty">Loading...</p>}
              {!loading && notes.length === 0 && (
                <p className="test-notes-empty">No notes yet. Add the first observation.</p>
              )}
              {notes.map((note) => (
                <button
                  key={note.id}
                  type="button"
                  className={`test-notes-list-item${note.id === selectedId ? ' selected' : ''}`}
                  aria-current={note.id === selectedId ? 'true' : undefined}
                  onClick={() => selectNote(note)}
                >
                  <strong>{noteTitle(note)}</strong>
                  <span>Created {formatDateTime(note.created_at)}</span>
                  <span>Updated {formatDateTime(note.updated_at)}</span>
                </button>
              ))}
            </div>
          </aside>

          <section className="test-notes-editor-pane" aria-label="Selected test note">
            {mode === 'preview' && selectedNote && (
              <>
                <NoteMeta note={selectedNote} />
                <div className="test-notes-preview markdown-body">
                  <MarkdownRenderer source={selectedNote.body} blockKeyPrefix={`note-${selectedNote.id}`} />
                </div>
                <div className="test-notes-actions">
                  <button type="button" className="btn btn-secondary" onClick={startEditNote} disabled={saving}>
                    Edit
                  </button>
                  <button type="button" className="btn btn-danger" onClick={() => { void deleteNote(selectedNote) }} disabled={saving}>
                    Delete
                  </button>
                </div>
              </>
            )}

            {(mode === 'new' || mode === 'edit') && (
              <>
                {mode === 'edit' && selectedNote ? <NoteMeta note={selectedNote} /> : (
                  <div className="test-notes-meta">
                    <span>Unsaved draft</span>
                  </div>
                )}
                <label className="test-notes-textarea-label">
                  <span>Note</span>
                  <textarea
                    ref={textareaRef}
                    value={draftBody}
                    onChange={(event) => setDraftBody(event.target.value)}
                    maxLength={NOTE_BODY_MAX_LENGTH}
                    rows={12}
                    disabled={saving}
                  />
                </label>
                <div className="test-notes-edit-footer">
                  <span>{draftBody.length.toLocaleString()} / {NOTE_BODY_MAX_LENGTH.toLocaleString()}</span>
                  <div className="test-notes-actions">
                    <button type="button" className="btn btn-secondary" onClick={cancelEdit} disabled={saving}>
                      Cancel
                    </button>
                    <button type="button" className="btn btn-primary" onClick={() => { void saveNote() }} disabled={!canSave}>
                      {saving ? 'Saving...' : 'Save'}
                    </button>
                  </div>
                </div>
                {draftBody.trim().length > 0 && (
                  <div className="test-notes-edit-preview markdown-body">
                    <MarkdownRenderer source={draftBody} blockKeyPrefix="draft-note" />
                  </div>
                )}
              </>
            )}
          </section>
        </div>
      </div>
    </div></DialogLayer>
  )
}

function NoteMeta({ note }: { note: TestNote }) {
  return (
    <div className="test-notes-meta">
      <span>Created {formatDateTime(note.created_at)}{note.created_by ? ` by ${note.created_by}` : ''}</span>
      <span>Updated {formatDateTime(note.updated_at)}{note.updated_by ? ` by ${note.updated_by}` : ''}</span>
    </div>
  )
}

function upsertNote(notes: TestNote[], note: TestNote) {
  const exists = notes.some((item) => item.id === note.id)
  if (exists) {
    return notes.map((item) => (item.id === note.id ? note : item))
  }
  return [note, ...notes]
}

function sortNotes(notes: TestNote[]) {
  return [...notes].sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))
}

function firstLine(body: string) {
  return body.split(/\r?\n/).find((line) => line.trim())?.trim() || 'Untitled note'
}

function noteTitle(note: TestNote) {
  const line = firstLine(note.body).replace(/^#{1,6}\s+/, '')
  return line.length > 64 ? `${line.slice(0, 61)}...` : line
}

function noteDeleteTarget(note: TestNote) {
  const label = noteTitle(note)
  return label.length > 40 ? `${label.slice(0, 37)}...` : label
}

function formatDateTime(value?: string) {
  if (!value) return 'Not recorded'
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return 'Not recorded'
  return date.toLocaleString()
}
