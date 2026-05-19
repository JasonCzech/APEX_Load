import { useCallback, useEffect, useState } from 'react'

import { type StoredScenario, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import { createApiDraft, createJavaHttpDraft, createJmsDraft, createSoapDraft, createWebDraft } from './scriptDraftFactories'

export type ScriptsAuthoringSubView = 'list' | 'create-options' | 'create' | 'edit' | 'import' | 'jmeter-import' | 'recorder' | 'sample' | 'java-http-import' | 'devweb-import' | 'har-import' | 'openapi-import' | 'wsdl-import'

export interface DraftEditorFocus {
	targetAction?: string | null
  targetSection?: 'runtime' | 'parameter-data' | null
  targetDataSource?: string | null
	targetRuntimeArea?: string | null
}

export interface AuthoringState {
  subView: ScriptsAuthoringSubView
  editingId: string | null
  editingScript: ScriptDefinition | null
  draftScript: ScriptDefinition | null
  draftEditorFocus: DraftEditorFocus | null
  authoringNotice: string | null
}

export interface ImportedSaveResult {
  scripts: StoredScript[]
  scenario?: StoredScenario | null
}

interface UseScriptsAuthoringStateOptions {
  api: ReturnType<typeof useApi>
  initialMode?: ScriptsAuthoringSubView
  initialEditId?: string
}

export interface ScriptsCreateOptionActions {
  startApiScript: () => void
  startSoapScript: () => void
  startJavaHttpScript: () => void
  startWebScript: () => void
  startJmsScript: () => void
  startTemplateScript: (script: ScriptDefinition) => void
  openLoadRunnerImport: () => void
  openJMeterImport: () => void
  openRecorder: () => void
  openSampleImport: () => void
  openJavaHttpImport: () => void
  openDevWebImport: () => void
  openHARImport: () => void
  openOpenAPIImport: () => void
  openWSDLImport: () => void
}

interface ScriptsAuthoringStateModel {
  authoringState: AuthoringState
  setSubview: (subView: ScriptsAuthoringSubView) => void
  openLibrary: () => void
  openCreateOptions: () => void
  handleEdit: (id: string) => Promise<void>
  handleSavedScript: (script: StoredScript, notice?: string | null) => void
  handleDraftEdit: (script: ScriptDefinition, focus?: DraftEditorFocus) => void
  startDraft: (factory: () => ScriptDefinition) => void
  createOptionActions: ScriptsCreateOptionActions
}

function normalizeDraftEditorFocus(focus?: DraftEditorFocus | null): DraftEditorFocus | null {
	if (!focus) return null
	const targetAction = focus.targetAction?.trim()
  const targetSection = focus.targetSection === 'runtime' || focus.targetSection === 'parameter-data'
    ? focus.targetSection
    : null
  const targetDataSource = focus.targetDataSource?.trim()
  const targetRuntimeArea = focus.targetRuntimeArea?.trim()
  if (!targetAction && !targetSection && !targetDataSource && !targetRuntimeArea) return null
  return {
    ...(targetAction ? { targetAction } : {}),
    ...(targetSection ? { targetSection } : {}),
    ...(targetDataSource ? { targetDataSource } : {}),
    ...(targetRuntimeArea ? { targetRuntimeArea } : {}),
  }
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
    draftEditorFocus: null,
    authoringNotice: null,
  }
}

export function useScriptsAuthoringState({
  api,
  initialMode,
  initialEditId,
}: UseScriptsAuthoringStateOptions): ScriptsAuthoringStateModel {
  const [authoringState, setAuthoringState] = useState<AuthoringState>(() => createAuthoringState(initialMode, initialEditId))

  const setSubview = useCallback((subView: ScriptsAuthoringSubView) => {
    setAuthoringState({ subView, editingId: null, editingScript: null, draftScript: null, draftEditorFocus: null, authoringNotice: null })
  }, [])

  const openLibrary = useCallback(() => {
    setSubview('list')
  }, [setSubview])

  const openCreateOptions = useCallback(() => {
    setSubview('create-options')
  }, [setSubview])

  const openLoadRunnerImport = useCallback(() => {
    setSubview('import')
  }, [setSubview])

  const openJMeterImport = useCallback(() => {
    setSubview('jmeter-import')
  }, [setSubview])

  const openRecorder = useCallback(() => {
    setSubview('recorder')
  }, [setSubview])

  const openSampleImport = useCallback(() => {
    setSubview('sample')
  }, [setSubview])

  const openJavaHttpImport = useCallback(() => {
    setSubview('java-http-import')
  }, [setSubview])

  const openDevWebImport = useCallback(() => {
    setSubview('devweb-import')
  }, [setSubview])

  const openHARImport = useCallback(() => {
    setSubview('har-import')
  }, [setSubview])

  const openOpenAPIImport = useCallback(() => {
    setSubview('openapi-import')
  }, [setSubview])

  const openWSDLImport = useCallback(() => {
    setSubview('wsdl-import')
  }, [setSubview])

  const openDraftEditor = useCallback((script: ScriptDefinition, focus?: DraftEditorFocus) => {
    setAuthoringState({
      subView: 'create',
      editingId: null,
      editingScript: null,
      draftScript: script,
      draftEditorFocus: normalizeDraftEditorFocus(focus),
      authoringNotice: null,
    })
  }, [])

  const startDraft = useCallback((factory: () => ScriptDefinition) => {
    openDraftEditor(factory())
  }, [openDraftEditor])

  const startApiScript = useCallback(() => {
    startDraft(createApiDraft)
  }, [startDraft])

  const startSoapScript = useCallback(() => {
    startDraft(createSoapDraft)
  }, [startDraft])

  const startJavaHttpScript = useCallback(() => {
    startDraft(createJavaHttpDraft)
  }, [startDraft])

  const startWebScript = useCallback(() => {
    startDraft(createWebDraft)
  }, [startDraft])

  const startJmsScript = useCallback(() => {
    startDraft(createJmsDraft)
  }, [startDraft])

  const startTemplateScript = useCallback((script: ScriptDefinition) => {
    openDraftEditor(script)
  }, [openDraftEditor])

  const handleEdit = useCallback(async (id: string) => {
    try {
      const res = await api.getScript(id)
      setAuthoringState({
        subView: 'edit',
        editingId: id,
        editingScript: res.script ?? null,
        draftScript: null,
        draftEditorFocus: null,
        authoringNotice: null,
      })
    } catch {
      // Ignore edit-load failures to preserve existing page behavior.
    }
  }, [api])

  const handleSavedScript = useCallback((savedScript: StoredScript, notice?: string | null) => {
    setAuthoringState(previous => ({
      subView: 'edit',
      editingId: savedScript.id,
      editingScript: savedScript.script ?? previous.draftScript ?? previous.editingScript,
      draftScript: null,
      draftEditorFocus: null,
      authoringNotice: notice?.trim() || null,
    }))
  }, [])

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
    handleSavedScript,
    handleDraftEdit: openDraftEditor,
    startDraft,
    createOptionActions: {
      startApiScript,
      startSoapScript,
      startJavaHttpScript,
      startWebScript,
      startJmsScript,
      startTemplateScript,
      openLoadRunnerImport,
      openJMeterImport,
      openRecorder,
      openSampleImport,
      openJavaHttpImport,
      openDevWebImport,
      openHARImport,
      openOpenAPIImport,
      openWSDLImport,
    },
  }
}
