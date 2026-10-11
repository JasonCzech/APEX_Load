import type {
  BrowserReplayReport,
  ScriptDebugLogLevel,
  ScriptDebugSession,
} from '../types'
import type {
  ScriptDataSource,
  ScriptDefinition,
  ScriptParameterDefinition,
} from '../types/scripts'
import { EMPTY_SCRIPT } from './scriptEditorUtils'
import { formatScriptJson } from './scriptEditorJsonModel'
import type { ScriptActionPhase } from './scriptEditorWorkbench'

export type ScriptEditorStateUpdate<T> = T | ((current: T) => T)
export type ScriptEditorStateSetter<T> = (update: ScriptEditorStateUpdate<T>) => void

export interface ActionEditorParameterDraft {
  source: 'action' | 'json'
  phase?: ScriptActionPhase
  stepPath?: string[]
  actionIndex: number
  start: number
  end: number
  selectedText: string
  existingName: string | null
  requestedName: string
  seedValue: string
  parameterType?: string
  typeConfig?: Partial<ScriptParameterDefinition>
  replacementRanges?: Array<{ from: number; to: number }>
  reuseExisting?: boolean
}

export interface DataSourceHistory {
  past: ScriptDataSource[]
  future: ScriptDataSource[]
}

export interface CandidateOccurrence {
  actionIdx: number
  field: 'target' | 'body' | 'header'
  headerKey?: string
  headerIndex?: number
}

export interface ParameterCandidate {
  id: string
  keyHint: string
  value: string
  varName: string
  occurrences: CandidateOccurrence[]
  selected: boolean
}

export interface ReplayOptionsState {
  capture?: 'diagnostic' | 'errors' | 'script'
  groupName: string
  ignoreThinkTime: boolean
  startPaused: boolean
  breakOnFailure: boolean
  logLevel: ScriptDebugLogLevel
  variablesText: string
  /** What follows a failure the debugger does not pause on; 'test-run' does what a test run of the script would. */
  afterFailure: 'test-run' | 'continue' | 'stop'
  /** The value of {{run.id}} in the session; blank uses the session's own identity. */
  runId: string
}

export interface ReplayActivityItem {
  id: string
  kind: 'start' | 'event' | 'complete' | 'error'
  title: string
  detail?: string
  timestamp: string
  success?: boolean
  changedVariables?: string[]
}

export interface ReplayScreenshotSelection {
  alt: string
  src: string
}

export interface EditorHistory {
  past: ScriptDefinition[]
  future: ScriptDefinition[]
}

export interface ScriptEditorDocumentHistoryState {
  script: ScriptDefinition
  jsonDirty: boolean
  jsonText: string
  editorHistory: EditorHistory
  editGroup?: string
  editTime?: number
}

export interface ScriptEditorNavigationState {
  activeAction: number | null
  openSections: Record<string, boolean>
}

export interface ScriptEditorParametersDataState {
  isParameterDialogOpen: boolean
  pendingParameterDraft: ActionEditorParameterDraft | null
  openDataEditor: number | null
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  parameterCandidates: ParameterCandidate[]
}

export interface ScriptEditorImportReviewState {
  baselineScript: ScriptDefinition | null
}

export interface ScriptEditorReplayDebugState {
  replaying: boolean
  replayReport: BrowserReplayReport | null
  expandedReplayEvent: number | null
  expandedReplayBrowserArtifacts: Record<string, boolean>
  copiedReplaySnapshotSection: string | null
  activeReplaySessionId: string | null
  browserReplaySession: ScriptDebugSession | null
  replayScreenshotSelection: ReplayScreenshotSelection | null
  replayActivity: ReplayActivityItem[]
  replaySeedVariables: Record<string, string>
  replayOptions: ReplayOptionsState
}

export interface ScriptEditorAsyncOperationsState {
  saving: boolean
  validating: boolean
  error: string
  success: string
  warnings: string[]
}

export interface ScriptEditorState {
  documentHistory: ScriptEditorDocumentHistoryState
  navigation: ScriptEditorNavigationState
  parametersData: ScriptEditorParametersDataState
  importReview: ScriptEditorImportReviewState
  replayDebug: ScriptEditorReplayDebugState
  asyncOperations: ScriptEditorAsyncOperationsState
}

export type ScriptEditorEvent =
  | { type: 'document/edit'; update: ScriptEditorStateUpdate<ScriptDefinition>; group?: string; time: number }
  | { type: 'documentHistory/update'; update: ScriptEditorStateUpdate<ScriptEditorDocumentHistoryState> }
  | { type: 'navigation/update'; update: ScriptEditorStateUpdate<ScriptEditorNavigationState> }
  | { type: 'parametersData/update'; update: ScriptEditorStateUpdate<ScriptEditorParametersDataState> }
  | { type: 'importReview/update'; update: ScriptEditorStateUpdate<ScriptEditorImportReviewState> }
  | { type: 'replayDebug/update'; update: ScriptEditorStateUpdate<ScriptEditorReplayDebugState> }
  | { type: 'asyncOperations/update'; update: ScriptEditorStateUpdate<ScriptEditorAsyncOperationsState> }
  | { type: 'editor/hydrate'; script: ScriptDefinition }
  | { type: 'navigation/toggleSection'; id: string }
  | { type: 'parametersData/closeDialog' }
  | { type: 'replayDebug/closeScreenshot' }

export const DEFAULT_REPLAY_OPTIONS: ReplayOptionsState = {
  capture: 'diagnostic',
  groupName: '',
  ignoreThinkTime: true,
  startPaused: false,
  breakOnFailure: false,
  logLevel: 'info',
  variablesText: '{\n  \n}',
  afterFailure: 'test-run',
  runId: '',
}

function applyUpdate<T>(current: T, update: ScriptEditorStateUpdate<T>): T {
  return typeof update === 'function'
    ? (update as (value: T) => T)(current)
    : update
}

function updateField<T extends object, K extends keyof T>(
  current: T,
  key: K,
  update: ScriptEditorStateUpdate<T[K]>,
): T {
  const nextValue = applyUpdate(current[key], update)
  return Object.is(current[key], nextValue) ? current : { ...current, [key]: nextValue }
}

export function scriptHasImportMetadata(script?: ScriptDefinition | null): boolean {
  const metadata = script?.metadata?.import
  if (!metadata) return false
  const preserved = metadata.preserved
  const hasPreservedFields = Boolean(
    preserved
    && typeof preserved === 'object'
    && !Array.isArray(preserved)
    && Object.keys(preserved).length > 0,
  )
  return Boolean(metadata.source || metadata.source_script_name || hasPreservedFields)
}

/**
 * Snapshot the editable script body for import-review diffs without cloning the
 * potentially multi-megabyte, immutable source metadata.
 */
export function cloneScriptForImportBaseline(source: ScriptDefinition): ScriptDefinition {
  const { metadata, ...body } = source
  const baseline = structuredClone(body) as ScriptDefinition
  if (metadata !== undefined) baseline.metadata = metadata
  return baseline
}

export function createScriptEditorState(initialScript?: ScriptDefinition | null): ScriptEditorState {
  const script = initialScript || { ...EMPTY_SCRIPT }
  return {
    documentHistory: {
      script,
      jsonDirty: false,
      jsonText: formatScriptJson(script),
      editorHistory: { past: [], future: [] },
    },
    navigation: {
      activeAction: null,
      openSections: {
        config: false,
        actions: true,
        runtime: false,
        import: scriptHasImportMetadata(script),
        replay: false,
      },
    },
    parametersData: {
      isParameterDialogOpen: false,
      pendingParameterDraft: null,
      openDataEditor: null,
      dataEditorMode: {},
      dataSourceHistory: {},
      parameterCandidates: [],
    },
    importReview: {
      baselineScript: script.metadata?.import ? cloneScriptForImportBaseline(script) : null,
    },
    replayDebug: {
      replaying: false,
      replayReport: null,
      expandedReplayEvent: null,
      expandedReplayBrowserArtifacts: {},
      copiedReplaySnapshotSection: null,
      activeReplaySessionId: null,
      browserReplaySession: null,
      replayScreenshotSelection: null,
      replayActivity: [],
      replaySeedVariables: {},
      replayOptions: { ...DEFAULT_REPLAY_OPTIONS },
    },
    asyncOperations: {
      saving: false,
      validating: false,
      error: '',
      success: '',
      warnings: [],
    },
  }
}

export function scriptEditorReducer(state: ScriptEditorState, event: ScriptEditorEvent): ScriptEditorState {
  switch (event.type) {
    case 'document/edit': {
      const previous = state.documentHistory
      const script = applyUpdate(previous.script, event.update)
      if (script === previous.script || JSON.stringify(script) === JSON.stringify(previous.script)) return state
      const past = previous.editorHistory.past
      const grouped = event.group && event.group === previous.editGroup && event.time - (previous.editTime || 0) < 750
      const alreadyRecorded = past.length > 0 && JSON.stringify(past[past.length - 1]) === JSON.stringify(previous.script)
      return { ...state, documentHistory: { ...previous, script,
        editGroup: event.group, editTime: event.time,
        editorHistory: { past: grouped || alreadyRecorded ? past : [...past, JSON.parse(JSON.stringify(previous.script)) as ScriptDefinition].slice(-30), future: [] },
      } }
    }
    case 'documentHistory/update': {
      let documentHistory = applyUpdate(state.documentHistory, event.update)
      if (documentHistory.editorHistory !== state.documentHistory.editorHistory) documentHistory = { ...documentHistory, editGroup: undefined }
      return documentHistory === state.documentHistory ? state : { ...state, documentHistory }
    }
    case 'navigation/update': {
      const navigation = applyUpdate(state.navigation, event.update)
      return navigation === state.navigation ? state : { ...state, navigation }
    }
    case 'parametersData/update': {
      const parametersData = applyUpdate(state.parametersData, event.update)
      return parametersData === state.parametersData ? state : { ...state, parametersData }
    }
    case 'importReview/update': {
      const importReview = applyUpdate(state.importReview, event.update)
      return importReview === state.importReview ? state : { ...state, importReview }
    }
    case 'replayDebug/update': {
      const replayDebug = applyUpdate(state.replayDebug, event.update)
      return replayDebug === state.replayDebug ? state : { ...state, replayDebug }
    }
    case 'asyncOperations/update': {
      const asyncOperations = applyUpdate(state.asyncOperations, event.update)
      return asyncOperations === state.asyncOperations ? state : { ...state, asyncOperations }
    }
    case 'navigation/toggleSection':
      return {
        ...state,
        navigation: {
          ...state.navigation,
          openSections: {
            ...state.navigation.openSections,
            [event.id]: !state.navigation.openSections[event.id],
          },
        },
      }
    case 'parametersData/closeDialog':
      return {
        ...state,
        parametersData: {
          ...state.parametersData,
          isParameterDialogOpen: false,
          pendingParameterDraft: null,
        },
      }
    case 'replayDebug/closeScreenshot':
      return {
        ...state,
        replayDebug: { ...state.replayDebug, replayScreenshotSelection: null },
      }
    case 'editor/hydrate': {
      const jsonText = formatScriptJson(event.script)
      const hasImportMetadata = scriptHasImportMetadata(event.script)
      return {
        ...state,
        documentHistory: {
          ...state.documentHistory,
          script: event.script,
          editorHistory: { past: [], future: [] },
          editGroup: undefined,
          jsonDirty: false,
          jsonText,
        },
        navigation: {
          activeAction: null,
          openSections: hasImportMetadata
            ? { ...state.navigation.openSections, import: true }
            : state.navigation.openSections,
        },
        parametersData: {
          isParameterDialogOpen: false,
          pendingParameterDraft: null,
          openDataEditor: null,
          dataEditorMode: {},
          dataSourceHistory: {},
          parameterCandidates: [],
        },
        importReview: {
          baselineScript: event.script.metadata?.import ? cloneScriptForImportBaseline(event.script) : null,
        },
        replayDebug: {
          ...state.replayDebug,
          expandedReplayBrowserArtifacts: {},
          replayScreenshotSelection: null,
        },
        asyncOperations: {
          ...state.asyncOperations,
          error: '',
          success: '',
          warnings: [],
        },
      }
    }
  }
}

export const selectScriptEditorDocument = (state: ScriptEditorState) => state.documentHistory
export const selectScriptEditorNavigation = (state: ScriptEditorState) => state.navigation
export const selectScriptEditorParametersData = (state: ScriptEditorState) => state.parametersData
export const selectScriptEditorImportReview = (state: ScriptEditorState) => state.importReview
export const selectScriptEditorReplayDebug = (state: ScriptEditorState) => state.replayDebug
export const selectScriptEditorAsyncOperations = (state: ScriptEditorState) => state.asyncOperations
export const selectScriptEditorIsBusy = (state: ScriptEditorState) => (
  state.asyncOperations.saving || state.asyncOperations.validating || state.replayDebug.replaying
)

export interface ScriptEditorDispatchers {
  setScript: ScriptEditorStateSetter<ScriptDefinition>
  setJsonDirty: ScriptEditorStateSetter<boolean>
  setJsonText: ScriptEditorStateSetter<string>
  setEditorHistory: ScriptEditorStateSetter<EditorHistory>
  setActiveAction: ScriptEditorStateSetter<number | null>
  setOpenSections: ScriptEditorStateSetter<Record<string, boolean>>
  setIsParameterDialogOpen: ScriptEditorStateSetter<boolean>
  setPendingParameterDraft: ScriptEditorStateSetter<ActionEditorParameterDraft | null>
  setOpenDataEditor: ScriptEditorStateSetter<number | null>
  setDataEditorMode: ScriptEditorStateSetter<Record<number, 'grid' | 'raw'>>
  setDataSourceHistory: ScriptEditorStateSetter<Record<number, DataSourceHistory>>
  setParameterCandidates: ScriptEditorStateSetter<ParameterCandidate[]>
  setReplaying: ScriptEditorStateSetter<boolean>
  setReplayReport: ScriptEditorStateSetter<BrowserReplayReport | null>
  setExpandedReplayEvent: ScriptEditorStateSetter<number | null>
  setExpandedReplayBrowserArtifacts: ScriptEditorStateSetter<Record<string, boolean>>
  setCopiedReplaySnapshotSection: ScriptEditorStateSetter<string | null>
  setActiveReplaySessionId: ScriptEditorStateSetter<string | null>
  setBrowserReplaySession: ScriptEditorStateSetter<ScriptDebugSession | null>
  setReplayScreenshotSelection: ScriptEditorStateSetter<ReplayScreenshotSelection | null>
  setReplayActivity: ScriptEditorStateSetter<ReplayActivityItem[]>
  setReplaySeedVariables: ScriptEditorStateSetter<Record<string, string>>
  setReplayOptions: ScriptEditorStateSetter<ReplayOptionsState>
  setSaving: ScriptEditorStateSetter<boolean>
  setValidating: ScriptEditorStateSetter<boolean>
  setError: ScriptEditorStateSetter<string>
  setSuccess: ScriptEditorStateSetter<string>
  setWarnings: ScriptEditorStateSetter<string[]>
}

export function createScriptEditorDispatchers(
  dispatch: (event: ScriptEditorEvent) => void,
): ScriptEditorDispatchers {
  const updateDocument = <K extends keyof ScriptEditorDocumentHistoryState>(
    key: K,
    update: ScriptEditorStateUpdate<ScriptEditorDocumentHistoryState[K]>,
  ) => dispatch({
    type: 'documentHistory/update',
    update: current => updateField(current, key, update),
  })
  const updateNavigation = <K extends keyof ScriptEditorNavigationState>(
    key: K,
    update: ScriptEditorStateUpdate<ScriptEditorNavigationState[K]>,
  ) => dispatch({
    type: 'navigation/update',
    update: current => updateField(current, key, update),
  })
  const updateParametersData = <K extends keyof ScriptEditorParametersDataState>(
    key: K,
    update: ScriptEditorStateUpdate<ScriptEditorParametersDataState[K]>,
  ) => dispatch({
    type: 'parametersData/update',
    update: current => updateField(current, key, update),
  })
  const updateReplayDebug = <K extends keyof ScriptEditorReplayDebugState>(
    key: K,
    update: ScriptEditorStateUpdate<ScriptEditorReplayDebugState[K]>,
  ) => dispatch({
    type: 'replayDebug/update',
    update: current => updateField(current, key, update),
  })
  const updateAsyncOperations = <K extends keyof ScriptEditorAsyncOperationsState>(
    key: K,
    update: ScriptEditorStateUpdate<ScriptEditorAsyncOperationsState[K]>,
  ) => dispatch({
    type: 'asyncOperations/update',
    update: current => updateField(current, key, update),
  })

  return {
    setScript: update => updateDocument('script', update),
    setJsonDirty: update => updateDocument('jsonDirty', update),
    setJsonText: update => updateDocument('jsonText', update),
    setEditorHistory: update => updateDocument('editorHistory', update),
    setActiveAction: update => updateNavigation('activeAction', update),
    setOpenSections: update => updateNavigation('openSections', update),
    setIsParameterDialogOpen: update => updateParametersData('isParameterDialogOpen', update),
    setPendingParameterDraft: update => updateParametersData('pendingParameterDraft', update),
    setOpenDataEditor: update => updateParametersData('openDataEditor', update),
    setDataEditorMode: update => updateParametersData('dataEditorMode', update),
    setDataSourceHistory: update => updateParametersData('dataSourceHistory', update),
    setParameterCandidates: update => updateParametersData('parameterCandidates', update),
    setReplaying: update => updateReplayDebug('replaying', update),
    setReplayReport: update => updateReplayDebug('replayReport', update),
    setExpandedReplayEvent: update => updateReplayDebug('expandedReplayEvent', update),
    setExpandedReplayBrowserArtifacts: update => updateReplayDebug('expandedReplayBrowserArtifacts', update),
    setCopiedReplaySnapshotSection: update => updateReplayDebug('copiedReplaySnapshotSection', update),
    setActiveReplaySessionId: update => updateReplayDebug('activeReplaySessionId', update),
    setBrowserReplaySession: update => updateReplayDebug('browserReplaySession', update),
    setReplayScreenshotSelection: update => updateReplayDebug('replayScreenshotSelection', update),
    setReplayActivity: update => updateReplayDebug('replayActivity', update),
    setReplaySeedVariables: update => updateReplayDebug('replaySeedVariables', update),
    setReplayOptions: update => updateReplayDebug('replayOptions', update),
    setSaving: update => updateAsyncOperations('saving', update),
    setValidating: update => updateAsyncOperations('validating', update),
    setError: update => updateAsyncOperations('error', update),
    setSuccess: update => updateAsyncOperations('success', update),
    setWarnings: update => updateAsyncOperations('warnings', update),
  }
}
