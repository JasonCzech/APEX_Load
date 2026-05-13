import { useCallback, useEffect, useState } from 'react'

import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'

export type ScriptsAuthoringSubView = 'list' | 'create-options' | 'create' | 'edit' | 'import' | 'jmeter-import' | 'recorder' | 'sample'

interface AuthoringState {
  subView: ScriptsAuthoringSubView
  editingId: string | null
  editingScript: ScriptDefinition | null
  draftScript: ScriptDefinition | null
}

interface UseScriptsAuthoringStateOptions {
  api: ReturnType<typeof useApi>
  initialMode?: ScriptsAuthoringSubView
  initialEditId?: string
}

interface ScriptsAuthoringStateModel {
  authoringState: AuthoringState
  setSubview: (subView: ScriptsAuthoringSubView) => void
  openLibrary: () => void
  openCreateOptions: () => void
  handleEdit: (id: string) => Promise<void>
  handleDraftEdit: (script: ScriptDefinition) => void
  startDraft: (factory: () => ScriptDefinition) => void
}

function normalizeSubView(mode?: ScriptsAuthoringSubView): ScriptsAuthoringSubView {
  if (!mode) return 'list'
  return mode === 'create' ? 'create-options' : mode
}

function createAuthoringState(mode?: ScriptsAuthoringSubView, editId?: string): AuthoringState {
  return {
    subView: normalizeSubView(mode),
    editingId: editId || null,
    editingScript: null,
    draftScript: null,
  }
}

export function useScriptsAuthoringState({
  api,
  initialMode,
  initialEditId,
}: UseScriptsAuthoringStateOptions): ScriptsAuthoringStateModel {
  const [authoringState, setAuthoringState] = useState<AuthoringState>(() => createAuthoringState(initialMode, initialEditId))

  const setSubview = useCallback((subView: ScriptsAuthoringSubView) => {
    setAuthoringState({ subView, editingId: null, editingScript: null, draftScript: null })
  }, [])

  const openLibrary = useCallback(() => {
    setSubview('list')
  }, [setSubview])

  const openCreateOptions = useCallback(() => {
    setSubview('create-options')
  }, [setSubview])

  const openDraftEditor = useCallback((script: ScriptDefinition) => {
    setAuthoringState({
      subView: 'create',
      editingId: null,
      editingScript: null,
      draftScript: script,
    })
  }, [])

  const startDraft = useCallback((factory: () => ScriptDefinition) => {
    openDraftEditor(factory())
  }, [openDraftEditor])

  const handleEdit = useCallback(async (id: string) => {
    try {
      const res = await api.getScript(id)
      setAuthoringState({
        subView: 'edit',
        editingId: id,
        editingScript: res.script ?? null,
        draftScript: null,
      })
    } catch {
      // Ignore edit-load failures to preserve existing page behavior.
    }
  }, [api])

  useEffect(() => {
    if (initialMode === 'edit' && initialEditId) {
      void handleEdit(initialEditId)
      return
    }

    setAuthoringState(createAuthoringState(initialMode, initialEditId))
  }, [handleEdit, initialEditId, initialMode])

  return {
    authoringState,
    setSubview,
    openLibrary,
    openCreateOptions,
    handleEdit,
    handleDraftEdit: openDraftEditor,
    startDraft,
  }
}