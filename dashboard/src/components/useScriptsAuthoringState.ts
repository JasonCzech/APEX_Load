import { useCallback, useEffect, useState } from 'react'

import { type AuthoringComponent, type StoredScenario, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import { composeComponentDraft, createApiDraft, createDatabaseDraft, createGrpcDraft, createJavaHttpDraft, createJmsDraft, createNetworkDraft, createSoapDraft, createWebDraft, createWebSocketDraft } from './scriptDraftFactories'

export type ScriptsAuthoringSubView = 'list' | 'create-options' | 'create' | 'edit' | 'import' | 'jmeter-import' | 'recorder' | 'sample' | 'java-http-import' | 'devweb-import' | 'har-import' | 'openapi-import' | 'postman-import' | 'wsdl-import' | 'asyncapi-import'

export interface DraftEditorFocus {
	targetAction?: string | null
  targetSection?: 'runtime' | 'parameter-data' | null
  targetDataSource?: string | null
	targetRuntimeArea?: string | null
}

export interface AuthoringReturnTarget {
  subView: ScriptsAuthoringSubView
  label: string
  description?: string
}

export interface AuthoringState {
  subView: ScriptsAuthoringSubView
  editingId: string | null
  editingScript: ScriptDefinition | null
  draftScript: ScriptDefinition | null
  draftEditorFocus: DraftEditorFocus | null
  draftReturnTarget: AuthoringReturnTarget | null
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
  startWebSocketScript: () => void
  startGrpcScript: () => void
  startDatabaseScript: () => void
  startNetworkScript: () => void
  startWebScript: () => void
  startJmsScript: () => void
  startTemplateScript: (script: ScriptDefinition) => void
  composeComponentScript: (component: AuthoringComponent) => void
  openLoadRunnerImport: () => void
  openJMeterImport: () => void
  openRecorder: () => void
  openSampleImport: () => void
  openJavaHttpImport: () => void
  openDevWebImport: () => void
  openHARImport: () => void
  openOpenAPIImport: () => void
  openPostmanImport: () => void
  openWSDLImport: () => void
  openAsyncAPIImport: () => void
}

interface ScriptsAuthoringStateModel {
  authoringState: AuthoringState
  setSubview: (subView: ScriptsAuthoringSubView) => void
  openLibrary: () => void
  openCreateOptions: () => void
  handleEdit: (id: string) => Promise<void>
  handleSavedScript: (script: StoredScript, notice?: string | null) => void
  handleDraftEdit: (script: ScriptDefinition, focus?: DraftEditorFocus, returnTarget?: AuthoringReturnTarget | null) => void
  returnToDraftSource: () => void
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

function normalizeAuthoringReturnTarget(returnTarget?: AuthoringReturnTarget | null): AuthoringReturnTarget | null {
  const label = returnTarget?.label?.trim()
  if (!returnTarget || !label) return null

  const description = returnTarget.description?.trim()
  return {
    subView: returnTarget.subView,
    label,
    ...(description ? { description } : {}),
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
    draftReturnTarget: null,
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
    setAuthoringState({ subView, editingId: null, editingScript: null, draftScript: null, draftEditorFocus: null, draftReturnTarget: null, authoringNotice: null })
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

  const openPostmanImport = useCallback(() => {
    setSubview('postman-import')
  }, [setSubview])

  const openWSDLImport = useCallback(() => {
    setSubview('wsdl-import')
  }, [setSubview])

  const openAsyncAPIImport = useCallback(() => {
    setSubview('asyncapi-import')
  }, [setSubview])

  const openDraftEditor = useCallback((script: ScriptDefinition, focus?: DraftEditorFocus, returnTarget?: AuthoringReturnTarget | null) => {
    setAuthoringState({
      subView: 'create',
      editingId: null,
      editingScript: null,
      draftScript: script,
      draftEditorFocus: normalizeDraftEditorFocus(focus),
      draftReturnTarget: normalizeAuthoringReturnTarget(returnTarget),
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

  const startWebSocketScript = useCallback(() => {
    startDraft(createWebSocketDraft)
  }, [startDraft])

  const startGrpcScript = useCallback(() => {
    startDraft(createGrpcDraft)
  }, [startDraft])

  const startDatabaseScript = useCallback(() => {
    startDraft(createDatabaseDraft)
  }, [startDraft])

  const startNetworkScript = useCallback(() => {
    startDraft(createNetworkDraft)
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

  const composeComponentScript = useCallback((component: AuthoringComponent) => {
    openDraftEditor(composeComponentDraft(component))
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
      draftReturnTarget: null,
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
      draftReturnTarget: null,
      authoringNotice: notice?.trim() || null,
    }))
  }, [])

  const returnToDraftSource = useCallback(() => {
    setAuthoringState(previous => {
      const returnTarget = previous.draftReturnTarget
      if (!returnTarget) return previous

      return {
        subView: returnTarget.subView,
        editingId: null,
        editingScript: null,
        draftScript: null,
        draftEditorFocus: null,
        draftReturnTarget: null,
        authoringNotice: null,
      }
    })
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
    returnToDraftSource,
    startDraft,
    createOptionActions: {
      startApiScript,
      startSoapScript,
      startJavaHttpScript,
      startWebSocketScript,
      startGrpcScript,
      startDatabaseScript,
      startNetworkScript,
      startWebScript,
      startJmsScript,
      startTemplateScript,
      composeComponentScript,
      openLoadRunnerImport,
      openJMeterImport,
      openRecorder,
      openSampleImport,
      openJavaHttpImport,
      openDevWebImport,
      openHARImport,
      openOpenAPIImport,
      openPostmanImport,
      openWSDLImport,
      openAsyncAPIImport,
    },
  }
}
