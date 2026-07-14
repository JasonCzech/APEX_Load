import { useCallback, useEffect, useRef, useState } from 'react'

import { type AuthoringComponent, type StoredScenario, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import { composeComponentDraft, createApiDraft, createDatabaseDraft, createGrpcDraft, createJavaHttpDraft, createJmsDraft, createKafkaDraft, createNetworkDraft, createSoapDraft, createWebDraft, createWebSocketDraft } from './scriptDraftFactories'
import { getErrorMessage } from '../utils/errorUtils'

export type ScriptsAuthoringSubView = 'list' | 'create-options' | 'create' | 'edit' | 'import' | 'jmeter-import' | 'recorder' | 'sample' | 'protocol-import' | 'oracle-review' | 'recorder-import' | 'java-http-import' | 'devweb-import' | 'har-import' | 'salesforce-import' | 'openapi-import' | 'raml-import' | 'postman-import' | 'wsdl-import' | 'asyncapi-import'

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
  importLauncher?: string | null
  importMode?: string | null
  importFiles?: File[] | null
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

function importLauncherState(launcher: string, mode?: string | null): Pick<AuthoringState, 'subView' | 'importLauncher' | 'importMode'> {
  const normalizedLauncher = launcher.trim()
  const normalizedMode = mode?.trim() || null
  switch (normalizedLauncher) {
    case 'enterprise_archive':
      return { subView: 'import', importLauncher: normalizedLauncher, importMode: normalizedMode }
    case 'jmeter':
      return { subView: 'jmeter-import', importLauncher: normalizedLauncher, importMode: normalizedMode }
    case 'sample':
      return { subView: 'sample', importLauncher: normalizedLauncher, importMode: normalizedMode }
    case 'oracle_ebusiness_review':
      return { subView: 'oracle-review', importLauncher: normalizedLauncher, importMode: normalizedMode }
    default:
      return { subView: 'protocol-import', importLauncher: normalizedLauncher, importMode: normalizedMode }
  }
}

function aliasImportState(mode: ScriptsAuthoringSubView): Pick<AuthoringState, 'subView' | 'importLauncher' | 'importMode'> | null {
  switch (mode) {
    case 'import':
      return importLauncherState('enterprise_archive')
    case 'jmeter-import':
      return importLauncherState('jmeter')
    case 'sample':
      return importLauncherState('sample', 'standard_api')
    case 'java-http-import':
      return importLauncherState('sample', 'java_http')
    case 'salesforce-import':
      return importLauncherState('traffic_har', 'auto_detect')
    case 'har-import':
      return importLauncherState('traffic_har', 'auto_detect')
    case 'recorder-import':
      return importLauncherState('recorder_export')
    case 'devweb-import':
      return importLauncherState('devweb')
    case 'openapi-import':
      return importLauncherState('openapi')
    case 'raml-import':
      return importLauncherState('raml')
    case 'postman-import':
      return importLauncherState('postman')
    case 'wsdl-import':
      return importLauncherState('wsdl')
    case 'asyncapi-import':
      return importLauncherState('asyncapi')
    default:
      return null
  }
}

function createAuthoringState(mode?: ScriptsAuthoringSubView, editId?: string): AuthoringState {
  const normalizedMode = mode === 'create' ? 'create-options' : mode
  const alias = normalizedMode ? aliasImportState(normalizedMode) : null
  const subView = alias?.subView || normalizedMode || 'list'
  return {
    subView,
    editingId: editId || null,
    editingScript: null,
    draftScript: null,
    draftEditorFocus: null,
    draftReturnTarget: null,
    authoringNotice: null,
    importLauncher: alias?.importLauncher || null,
    importMode: alias?.importMode || null,
    importFiles: null,
  }
}

export function useScriptsAuthoringState({
  api,
  initialMode,
  initialEditId,
}: UseScriptsAuthoringStateOptions): ScriptsAuthoringStateModel {
  const [authoringState, setAuthoringState] = useState<AuthoringState>(() => createAuthoringState(initialMode, initialEditId))
  const editRequestGenerationRef = useRef(0)
  const invalidateEditRequest = useCallback(() => {
    editRequestGenerationRef.current += 1
  }, [])

  const setSubview = useCallback((subView: ScriptsAuthoringSubView) => {
    invalidateEditRequest()
    const alias = aliasImportState(subView)
    setAuthoringState({
      subView: alias?.subView || subView,
      editingId: null,
      editingScript: null,
      draftScript: null,
      draftEditorFocus: null,
      draftReturnTarget: null,
      authoringNotice: null,
      importLauncher: alias?.importLauncher || null,
      importMode: alias?.importMode || null,
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

  const openDraftEditor = useCallback((script: ScriptDefinition, focus?: DraftEditorFocus, returnTarget?: AuthoringReturnTarget | null) => {
    invalidateEditRequest()
    setAuthoringState({
      subView: 'create',
      editingId: null,
      editingScript: null,
      draftScript: script,
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

  const handleEdit = useCallback(async (id: string) => {
    const requestGeneration = ++editRequestGenerationRef.current
    setAuthoringState(previous => previous.subView === 'edit'
      ? {
          subView: 'edit',
          editingId: id,
          editingScript: null,
          draftScript: null,
          draftEditorFocus: null,
          draftReturnTarget: null,
          authoringNotice: null,
          importLauncher: null,
          importMode: null,
          importFiles: null,
        }
      : previous)
    try {
      const res = await api.getScript(id)
      if (editRequestGenerationRef.current !== requestGeneration) return
      setAuthoringState({
        subView: 'edit',
        editingId: id,
        editingScript: res.script ?? null,
        draftScript: null,
        draftEditorFocus: null,
        draftReturnTarget: null,
        authoringNotice: null,
        importLauncher: null,
        importMode: null,
        importFiles: null,
      })
    } catch (error) {
      if (editRequestGenerationRef.current !== requestGeneration) return
      setAuthoringState({
        subView: 'list',
        editingId: null,
        editingScript: null,
        draftScript: null,
        draftEditorFocus: null,
        draftReturnTarget: null,
        authoringNotice: getErrorMessage(error, 'Unable to load script for editing.'),
        importLauncher: null,
        importMode: null,
        importFiles: null,
      })
    }
  }, [api])

  const handleSavedScript = useCallback((savedScript: StoredScript, notice?: string | null) => {
    invalidateEditRequest()
    setAuthoringState(previous => ({
      subView: 'edit',
      editingId: savedScript.id,
      editingScript: savedScript.script ?? previous.draftScript ?? previous.editingScript,
      draftScript: null,
      draftEditorFocus: null,
      draftReturnTarget: null,
      authoringNotice: notice?.trim() || null,
      importLauncher: null,
      importMode: null,
      importFiles: null,
    }))
  }, [invalidateEditRequest])

  const returnToDraftSource = useCallback(() => {
    invalidateEditRequest()
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
        importLauncher: null,
        importMode: null,
        importFiles: null,
      }
    })
  }, [invalidateEditRequest])

  useEffect(() => {
    if (initialMode === 'edit' && initialEditId) {
      void handleEdit(initialEditId)
    } else {
      invalidateEditRequest()
      setAuthoringState(createAuthoringState(initialMode, initialEditId))
    }
    return invalidateEditRequest
  }, [handleEdit, initialEditId, initialMode, invalidateEditRequest])

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
