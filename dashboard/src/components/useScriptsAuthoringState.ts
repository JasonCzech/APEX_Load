import type { ScriptAuthoringBaseline, ScriptSaveOptions } from '../types/storedContent'
import { baselineFromSavedScript } from '../utils/scriptAuthoringRevision'
import { useCallback, useEffect, useRef, useState } from 'react'

import { type AuthoringComponent, type StoredScenario, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import type { ObjectActivitySummary } from '../types/objectActivity'
import { useApi } from '../hooks/useApi'
import { composeComponentDraft, createApiDraft, createDatabaseDraft, createGrpcDraft, createJavaHttpDraft, createJmsDraft, createKafkaDraft, createNetworkDraft, createSoapDraft, createWebDraft, createWebSocketDraft } from './scriptDraftFactories'
import { getErrorMessage } from '../utils/errorUtils'
import { importSurfaceForLauncher, scriptImportLauncherDescriptor, type ScriptImportLauncherDescriptor, type ScriptImportSurface } from '../scriptImportLaunchers'

// An importer is opened by its launcher descriptor (scriptImportLaunchers);
// its sub-view is derived from the launcher, never named by callers.
export type ScriptsAuthoringSubView = 'list' | 'create-options' | 'create' | 'edit' | 'recorder' | ScriptImportSurface

/** The non-import sub-views a caller may open directly. */
export type ScriptsAuthoringEntryView = Exclude<ScriptsAuthoringSubView, ScriptImportSurface>

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
  /** The importer to return to, when the return target is an import surface. */
  importLauncher?: string | null
  importMode?: string | null
  /** The source surface's unsaved-work guards. The source stays open behind the editor, so leaving the editor for it keeps them. */
  retainedGuardIds?: string[]
}

export interface AuthoringState {
  subView: ScriptsAuthoringSubView
  editingId: string | null
  editingAuthoring?: ScriptAuthoringBaseline | null
  editingRetainedDraft?: ScriptDefinition | null
  editingRetainedNeedsReview?: boolean
  editingRetainedJsonText?: string
  editingRetainedCreatedSecretNames?: string[]
  editingScript: ScriptDefinition | null
  editingActivity?: ObjectActivitySummary
  draftScript: ScriptDefinition | null
  draftInitiallyDirty: boolean
  draftEditorFocus: DraftEditorFocus | null
  draftReturnTarget: AuthoringReturnTarget | null
  authoringNotice: string | null
  importLauncher?: string | null
  importMode?: string | null
  importFiles?: File[] | null
}

export interface ImportedSaveResult {
  scripts: StoredScript[]
  scenario?: StoredScenario | null
  /**
   * The operator chose to continue importing, or unsaved drafts remain. Refresh
   * library data without replacing the importer with an editor.
   */
  stayInImportReview?: boolean
}

interface UseScriptsAuthoringStateOptions {
  api: ReturnType<typeof useApi>
  /** The sub-view to open; 'create' opens the create options. Ignored when initialImport is given. */
  initialMode?: ScriptsAuthoringEntryView
  /** The importer to open, as the route named it. */
  initialImport?: ScriptImportLauncherDescriptor | null
  initialEditId?: string
  projectId?: string
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
  startKafkaScript: () => void
  startTemplateScript: (script: ScriptDefinition) => void
  composeComponentScript: (component: AuthoringComponent) => void
  openRecorder: () => void
  openImportSurface: (launcher: string, mode?: string | null) => void
  openImportFiles: (launcher: string, files: File[], mode?: string | null) => void
  openEnterpriseArchiveImport: () => void
  openJMeterImport: () => void
  openSampleImport: () => void
  openJavaHttpImport: () => void
  openDevWebImport: () => void
  openHARImport: () => void
  openSalesforceImport: () => void
  openBrowserRecordingImport: () => void
  openOpenAPIImport: () => void
  openRAMLImport: () => void
  openPostmanImport: () => void
  openWSDLImport: () => void
  openAsyncAPIImport: () => void
}

interface ScriptsAuthoringStateModel {
  authoringState: AuthoringState
  setSubview: (subView: ScriptsAuthoringEntryView) => void
  openLibrary: () => void
  openCreateOptions: () => void
  openCreateOptionsWithNotice: (notice: string) => void
  handleEdit: (id: string) => Promise<void>
  handleSavedScript: (script: StoredScript, notice?: string | null, retainedDraft?: ScriptDefinition, retainedState?: ScriptSaveOptions) => void
  handleDraftEdit: (script: ScriptDefinition, focus?: DraftEditorFocus, returnTarget?: AuthoringReturnTarget | null, options?: { initiallyDirty?: boolean }) => void
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
  const importLauncher = returnTarget.importLauncher?.trim()
  const importMode = returnTarget.importMode?.trim()
  return {
    subView: returnTarget.subView,
    label,
    ...(description ? { description } : {}),
    ...(importLauncher ? { importLauncher } : {}),
    ...(importLauncher && importMode ? { importMode } : {}),
    ...(returnTarget.retainedGuardIds?.length ? { retainedGuardIds: [...returnTarget.retainedGuardIds] } : {}),
  }
}

function importLauncherState(launcher: string, mode?: string | null): Pick<AuthoringState, 'subView' | 'importLauncher' | 'importMode'> {
  const descriptor = scriptImportLauncherDescriptor(launcher, mode)
  return { subView: importSurfaceForLauncher(descriptor.launcher), importLauncher: descriptor.launcher, importMode: descriptor.mode }
}

function createAuthoringState(mode?: ScriptsAuthoringEntryView, editId?: string, initialImport?: ScriptImportLauncherDescriptor | null): AuthoringState {
  const importState = initialImport?.launcher ? importLauncherState(initialImport.launcher, initialImport.mode) : null
  const subView: ScriptsAuthoringSubView = importState?.subView || (mode === 'create' ? 'create-options' : mode) || 'list'
  return {
    subView,
    editingId: editId || null,
    editingScript: null,
    draftScript: null,
    draftInitiallyDirty: false,
    draftEditorFocus: null,
    draftReturnTarget: null,
    authoringNotice: null,
    importLauncher: importState?.importLauncher || null,
    importMode: importState?.importMode || null,
    importFiles: null,
  }
}

export function useScriptsAuthoringState({
  api,
  initialMode,
  initialImport,
  initialEditId,
  projectId,
}: UseScriptsAuthoringStateOptions): ScriptsAuthoringStateModel {
  const [authoringState, setAuthoringState] = useState<AuthoringState>(() => createAuthoringState(initialMode, initialEditId, initialImport))
  const editRequestGenerationRef = useRef(0)
  const invalidateEditRequest = useCallback(() => {
    editRequestGenerationRef.current += 1
  }, [])
  // The edit request that opens a saved script, while it is the latest.
  const savedScriptReopenRef = useRef(-1)

  const setSubview = useCallback((subView: ScriptsAuthoringEntryView) => {
    invalidateEditRequest()
    setAuthoringState({
      subView,
      editingId: null,
      editingScript: null,
      draftScript: null,
      draftInitiallyDirty: false,
      draftEditorFocus: null,
      draftReturnTarget: null,
      authoringNotice: null,
      importLauncher: null,
      importMode: null,
      importFiles: null,
    })
  }, [invalidateEditRequest])

  const openLibrary = useCallback(() => {
    setSubview('list')
  }, [setSubview])

  const openCreateOptions = useCallback(() => {
    setSubview('create-options')
  }, [setSubview])

  const openImportSurface = useCallback((launcher: string, mode?: string | null, files?: File[] | null) => {
    invalidateEditRequest()
    const next = importLauncherState(launcher, mode)
    setAuthoringState({
      subView: next.subView,
      editingId: null,
      editingScript: null,
      draftScript: null,
      draftInitiallyDirty: false,
      draftEditorFocus: null,
      draftReturnTarget: null,
      authoringNotice: null,
      importLauncher: next.importLauncher,
      importMode: next.importMode,
      importFiles: files && files.length > 0 ? files : null,
    })
  }, [invalidateEditRequest])

  const openImportFiles = useCallback((launcher: string, files: File[], mode?: string | null) => {
    openImportSurface(launcher, mode, files)
  }, [openImportSurface])

  const openRecorder = useCallback(() => {
    setSubview('recorder')
  }, [setSubview])

  const openEnterpriseArchiveImport = useCallback(() => openImportSurface('enterprise_archive'), [openImportSurface])
  const openJMeterImport = useCallback(() => openImportSurface('jmeter'), [openImportSurface])
  const openSampleImport = useCallback(() => openImportSurface('sample', 'standard_api'), [openImportSurface])
  const openJavaHttpImport = useCallback(() => openImportSurface('sample', 'java_http'), [openImportSurface])
  const openDevWebImport = useCallback(() => openImportSurface('devweb'), [openImportSurface])
  const openHARImport = useCallback(() => openImportSurface('traffic_har', 'auto_detect'), [openImportSurface])
  const openSalesforceImport = useCallback(() => openImportSurface('traffic_har', 'auto_detect'), [openImportSurface])
  const openBrowserRecordingImport = useCallback(() => openImportSurface('recorder_export'), [openImportSurface])
  const openOpenAPIImport = useCallback(() => openImportSurface('openapi'), [openImportSurface])
  const openRAMLImport = useCallback(() => openImportSurface('raml'), [openImportSurface])
  const openPostmanImport = useCallback(() => openImportSurface('postman'), [openImportSurface])
  const openWSDLImport = useCallback(() => openImportSurface('wsdl'), [openImportSurface])
  const openAsyncAPIImport = useCallback(() => openImportSurface('asyncapi'), [openImportSurface])

  const openDraftEditor = useCallback((script: ScriptDefinition, focus?: DraftEditorFocus, returnTarget?: AuthoringReturnTarget | null, options?: { initiallyDirty?: boolean }) => {
    invalidateEditRequest()
    setAuthoringState({
      subView: 'create',
      editingId: null,
      editingScript: null,
      draftScript: script,
      draftInitiallyDirty: options?.initiallyDirty === true,
      draftEditorFocus: normalizeDraftEditorFocus(focus),
      draftReturnTarget: normalizeAuthoringReturnTarget(returnTarget),
      authoringNotice: null,
      importLauncher: null,
      importMode: null,
      importFiles: null,
    })
  }, [invalidateEditRequest])

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

  const startKafkaScript = useCallback(() => {
    startDraft(createKafkaDraft)
  }, [startDraft])

  const startTemplateScript = useCallback((script: ScriptDefinition) => {
    openDraftEditor(script)
  }, [openDraftEditor])

  const composeComponentScript = useCallback((component: AuthoringComponent) => {
    openDraftEditor(composeComponentDraft(component))
  }, [openDraftEditor])

  // Pairing a recorder changes the composite API object. It must not reset the
  // active authoring surface through the initial-route effect below.
  const getScript = api.getScript
  const getScriptAuthoring = api.getScriptAuthoring
  const handleEdit = useCallback(async (id: string, notice?: string | null, retainedDraft?: ScriptDefinition, retainedState?: ScriptSaveOptions) => {
    const requestGeneration = ++editRequestGenerationRef.current
    setAuthoringState(previous => previous.subView === 'edit'
      ? {
          subView: 'edit',
          editingId: id,
          editingScript: null,
          draftScript: null,
          draftInitiallyDirty: false,
          draftEditorFocus: null,
          draftReturnTarget: null,
          authoringNotice: notice?.trim() || null,
          importLauncher: null,
          importMode: null,
          importFiles: null,
        }
      : previous)
    try {
      const [authoring, res] = await Promise.all([getScriptAuthoring(id, projectId), getScript(id)])
      if (editRequestGenerationRef.current !== requestGeneration) return
      setAuthoringState({
        subView: 'edit',
        editingId: id,
        editingAuthoring: authoring,
        editingRetainedDraft: retainedDraft,
        editingRetainedJsonText: retainedState?.retainedJsonText,
        editingRetainedCreatedSecretNames: retainedState?.retainedCreatedSecretNames,
        editingRetainedNeedsReview: Boolean(retainedDraft),
        editingScript: res.script?.archived_at ? { ...authoring.projection.script, archived_at: res.script.archived_at } : authoring.projection.script,
        editingActivity: res.activity,
        draftScript: null,
        draftInitiallyDirty: false,
        draftEditorFocus: null,
        draftReturnTarget: null,
        authoringNotice: notice?.trim() || null,
        importLauncher: null,
        importMode: null,
        importFiles: null,
      })
    } catch (error) {
      if (editRequestGenerationRef.current !== requestGeneration) return
      setAuthoringState({
        subView: retainedDraft ? 'edit' : 'list',
        editingId: retainedDraft ? id : null,
        editingScript: retainedDraft || null,
        editingAuthoring: null,
        editingRetainedDraft: retainedDraft,
        editingRetainedJsonText: retainedState?.retainedJsonText,
        editingRetainedCreatedSecretNames: retainedState?.retainedCreatedSecretNames,
        editingRetainedNeedsReview: Boolean(retainedDraft),
        draftScript: null,
        draftInitiallyDirty: false,
        draftEditorFocus: null,
        draftReturnTarget: null,
        authoringNotice: getErrorMessage(error, 'Unable to load script for editing.'),
        importLauncher: null,
        importMode: null,
        importFiles: null,
      })
    }
  }, [getScript, getScriptAuthoring, projectId])

  // reopenSavedScript loads a saved script that came back without its
  // authoring projection. The editor and its source stay as they are until
  // the saved script opens. The return that follows the save in the same turn
  // (Save changes on the way back) leaves that load standing; a later Back or
  // Close returns to the source as usual and drops the load.
  const reopenSavedScript = useCallback((savedScript: StoredScript, notice?: string | null, retainedDraft?: ScriptDefinition, retainedState?: ScriptSaveOptions) => {
    void handleEdit(savedScript.id, notice, retainedDraft, retainedState)
    const generation = editRequestGenerationRef.current
    savedScriptReopenRef.current = generation
    setTimeout(() => {
      if (savedScriptReopenRef.current === generation) savedScriptReopenRef.current = -1
    }, 0)
  }, [handleEdit])

  const handleSavedScript = useCallback((savedScript: StoredScript, notice?: string | null, retainedDraft?: ScriptDefinition, retainedState?: ScriptSaveOptions) => {
    let authoring: ScriptAuthoringBaseline
    try { authoring = baselineFromSavedScript(savedScript, savedScript.project_id) } catch {
      reopenSavedScript(savedScript, notice, retainedDraft, retainedState)
      return
    }
    if (!savedScript.script) {
      reopenSavedScript(savedScript, notice, retainedDraft, retainedState)
      return
    }
    invalidateEditRequest()
    setAuthoringState({
      subView: 'edit',
      editingId: savedScript.id,
      editingAuthoring: authoring,
      editingRetainedDraft: retainedDraft,
      editingRetainedJsonText: retainedState?.retainedJsonText,
      editingRetainedCreatedSecretNames: retainedState?.retainedCreatedSecretNames,
      editingRetainedNeedsReview: false,
      editingScript: authoring.projection.script,
      editingActivity: savedScript.activity,
      draftScript: null,
      draftInitiallyDirty: false,
      draftEditorFocus: null,
      draftReturnTarget: null,
      authoringNotice: notice?.trim() || null,
      importLauncher: null,
      importMode: null,
      importFiles: null,
    })
  }, [invalidateEditRequest, reopenSavedScript])

  const openCreateOptionsWithNotice = useCallback((notice: string) => {
    invalidateEditRequest()
    setAuthoringState({
      ...createAuthoringState('create-options'),
      authoringNotice: notice.trim() || null,
    })
  }, [invalidateEditRequest])

  const returnToDraftSource = useCallback(() => {
    // The save just made is opening the saved script (reopenSavedScript): it
    // wins over the return that follows it.
    if (savedScriptReopenRef.current === editRequestGenerationRef.current) {
      savedScriptReopenRef.current = -1
      return
    }
    invalidateEditRequest()
    setAuthoringState(previous => {
      const returnTarget = previous.draftReturnTarget
      if (!returnTarget) return previous

      return {
        subView: returnTarget.subView,
        editingId: null,
        editingScript: null,
        draftScript: null,
        draftInitiallyDirty: false,
        draftEditorFocus: null,
        draftReturnTarget: null,
        authoringNotice: null,
        // An import surface is returned to with the launcher that opened it.
        importLauncher: returnTarget.importLauncher || null,
        importMode: returnTarget.importLauncher ? returnTarget.importMode || null : null,
        importFiles: null,
      }
    })
  }, [invalidateEditRequest])

  // The route's importer as primitives: callers build the descriptor afresh
  // on each render, and the state is reset only when what it names changes.
  const initialImportLauncher = initialImport?.launcher || null
  const initialImportMode = initialImport?.mode || null
  useEffect(() => {
    if (initialMode === 'edit' && initialEditId && !initialImportLauncher) {
      void handleEdit(initialEditId)
    } else {
      invalidateEditRequest()
      setAuthoringState(createAuthoringState(initialMode, initialEditId, initialImportLauncher ? { launcher: initialImportLauncher, mode: initialImportMode } : null))
    }
    return invalidateEditRequest
  }, [handleEdit, initialEditId, initialImportLauncher, initialImportMode, initialMode, invalidateEditRequest])

  return {
    authoringState,
    setSubview,
    openLibrary,
    openCreateOptions,
    openCreateOptionsWithNotice,
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
      startKafkaScript,
      startTemplateScript,
      composeComponentScript,
      openRecorder,
      openImportSurface,
      openImportFiles,
      openEnterpriseArchiveImport,
      openJMeterImport,
      openSampleImport,
      openJavaHttpImport,
      openDevWebImport,
      openHARImport,
      openSalesforceImport,
      openBrowserRecordingImport,
      openOpenAPIImport,
      openRAMLImport,
      openPostmanImport,
      openWSDLImport,
      openAsyncAPIImport,
    },
  }
}
