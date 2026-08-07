import { useCallback, useEffect, useMemo, useState } from 'react'

import type { ScriptDefinition, ScriptParameterDefinition } from '../types/scripts'
import {
  buildJsonParameterDraft,
  buildScriptOutlineSummary,
  codePathForParameterCandidateOccurrence,
  deriveScriptJsonDraftState,
  findParameterReferencesInJson,
  formatScriptJson,
  formatScriptJsonDraft,
  normalizeOccurrenceIndex,
  parameterReferenceLines,
  parseScriptJsonDraft,
  resolveAuthorJsonScope,
  SCRIPT_JSON_DEFAULT_COMPANION_TAB,
  selectionStateForJsonRequest,
  synchronizeStructuredScriptJson,
  type ActionEditorSelectionState,
  type AuthorCodeFocus,
  type JsonPaneSelectionRequest,
  type ScriptJsonSidebarTab,
} from './scriptEditorJsonModel'
import type {
  ActionEditorParameterDraft,
  ParameterCandidate,
  ScriptEditorStateSetter,
} from './scriptEditorState'
import { countUniqueColumnValues, parseInlineData } from './scriptEditorUtils'
import { findDataSourceIndexByName } from './scriptEditorImportModel'
import {
  getSameLineParameterOptions,
  getSameLineParameterSelectOptions,
} from './scriptEditorParameterDataModel'

export function useScriptJsonDraftController({
  jsonDirty,
  jsonText,
  script,
  setError,
  setJsonDirty,
  setJsonText,
  setScript,
  setSuccess,
  onInvalidDraft,
}: {
  jsonDirty: boolean
  jsonText: string
  script: ScriptDefinition
  setError: ScriptEditorStateSetter<string>
  setJsonDirty: ScriptEditorStateSetter<boolean>
  setJsonText: ScriptEditorStateSetter<string>
  setScript: ScriptEditorStateSetter<ScriptDefinition>
  setSuccess: ScriptEditorStateSetter<string>
  onInvalidDraft: () => void
}) {
  const formattedScriptJson = useMemo(() => formatScriptJson(script), [script])
  const draftState = useMemo(
    () => deriveScriptJsonDraftState(jsonText, jsonDirty, script),
    [jsonDirty, jsonText, script],
  )

  useEffect(() => {
    const synchronized = synchronizeStructuredScriptJson(script, jsonText, jsonDirty, formattedScriptJson)
    if (synchronized.changed) setJsonText(synchronized.text)
  }, [formattedScriptJson, jsonDirty, jsonText, script, setJsonText])

  const resolveCurrentScript = useCallback((): ScriptDefinition => {
    // Same source-of-truth rule as deriveScriptJsonDraftState: a clean JSON pane
    // is only a projection of the structured script, so save/validate/replay do
    // not need to re-parse the whole document to learn what they already hold.
    if (!jsonDirty) return script
    const parsed = parseScriptJsonDraft(jsonText)
    if (!parsed.ok) throw new Error(parsed.message)
    return parsed.script
  }, [jsonDirty, jsonText, script])

  const commitJsonDraft = useCallback((nextScript: ScriptDefinition, message?: string) => {
    setScript(nextScript)
    setJsonText(formatScriptJson(nextScript))
    setJsonDirty(false)
    setError('')
    if (message) setSuccess(message)
  }, [setError, setJsonDirty, setJsonText, setScript, setSuccess])

  const applyJsonDraft = useCallback(() => {
    setSuccess('')
    const parsed = parseScriptJsonDraft(jsonText)
    if (!parsed.ok) {
      setError(parsed.message)
      onInvalidDraft()
      return
    }
    commitJsonDraft(parsed.script, 'Applied script JSON to the authoring tools.')
  }, [commitJsonDraft, jsonText, onInvalidDraft, setError, setSuccess])

  const formatJsonDraft = useCallback(() => {
    setSuccess('')
    const formatted = formatScriptJsonDraft(jsonText, script)
    if (!formatted.ok) {
      setError(formatted.message)
      onInvalidDraft()
      return
    }
    setJsonText(formatted.text)
    setJsonDirty(formatted.dirty)
    setError('')
  }, [jsonText, onInvalidDraft, script, setError, setJsonDirty, setJsonText, setSuccess])

  const resetJsonDraft = useCallback(() => {
    setJsonText(formattedScriptJson)
    setJsonDirty(false)
    setError('')
    setSuccess('Reset script JSON from the structured editor.')
  }, [formattedScriptJson, setError, setJsonDirty, setJsonText, setSuccess])

  const changeJsonDraft = useCallback((value: string) => {
    // Same dirty rule as updateScriptJsonText, but compared against the memoized
    // projection: re-serializing the whole script on every JSON-pane keystroke
    // is an O(script) cost we already paid once for this script identity.
    setJsonText(value)
    setJsonDirty(value !== formattedScriptJson)
  }, [formattedScriptJson, setJsonDirty, setJsonText])

  return {
    ...draftState,
    applyJsonDraft,
    changeJsonDraft,
    commitJsonDraft,
    formatJsonDraft,
    formattedScriptJson,
    resetJsonDraft,
    resolveCurrentScript,
  }
}

export function useScriptJsonNavigationController({
  jsonText,
  parameterCandidates,
  script,
}: {
  jsonText: string
  parameterCandidates: ParameterCandidate[]
  script: ScriptDefinition
}) {
  const [authorCodeFocus, setAuthorCodeFocus] = useState<AuthorCodeFocus | null>(null)
  const [showFullScriptJson, setShowFullScriptJson] = useState(false)
  const [outlineActionListCollapsed, setOutlineActionListCollapsed] = useState(false)
  const [outlineActionFilter, setOutlineActionFilter] = useState('')
  const [outlineParameterListCollapsed, setOutlineParameterListCollapsed] = useState(false)
  const [selectedOutlineParameterName, setSelectedOutlineParameterName] = useState<string | null>(null)
  const [parameterPaneShowAuto, setParameterPaneShowAuto] = useState(false)
  const [scriptJsonSidebarTab, setScriptJsonSidebarTab] = useState<ScriptJsonSidebarTab>(SCRIPT_JSON_DEFAULT_COMPANION_TAB)
  const [activeAutoParameterCandidateId, setActiveAutoParameterCandidateId] = useState<string | null>(null)
  const [activeAutoParameterOccurrenceIndex, setActiveAutoParameterOccurrenceIndex] = useState(0)
  const [jsonLineJumpRequest, setJsonLineJumpRequest] = useState<{ line: number; nonce: number } | null>(null)

  // buildScriptOutlineSummary walks every init/action/teardown step and
  // JSON-serializes each one to harvest parameter refs, so it must not rerun
  // for structured edits it cannot observe. The only script fields it reads are
  // the three phase lists and the auto-transaction mode, so key the memo on
  // those instead of the whole script identity: renaming the script, retuning
  // config, or editing parameters/data sources now reuses the built outline.
  const outlineActions = script.actions
  const outlineInit = script.init
  const outlineTeardown = script.teardown
  const outlineAutoTransactions = script.config.run_logic?.auto_transactions
  const outlineSummary = useMemo(
    () => buildScriptOutlineSummary(script),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [outlineActions, outlineInit, outlineTeardown, outlineAutoTransactions],
  )
  const parameterReferenceCounts = useMemo(() => {
    const counts = new Map<string, number>()
    outlineSummary.actions.forEach(action => {
      action.parameterRefs.forEach(ref => counts.set(ref, (counts.get(ref) || 0) + 1))
    })
    return counts
  }, [outlineSummary.actions])
  const selectedOutlineParameterExists = Boolean(
    selectedOutlineParameterName
    && (script.parameters || []).some(parameter => parameter.name === selectedOutlineParameterName),
  )
  const parameterMatchRanges = useMemo(
    () => selectedOutlineParameterExists && selectedOutlineParameterName
      ? findParameterReferencesInJson(jsonText, selectedOutlineParameterName)
      : [],
    [jsonText, selectedOutlineParameterExists, selectedOutlineParameterName],
  )
  const selectedOutlineParameterIndex = useMemo(
    () => (script.parameters || []).findIndex(parameter => parameter.name === selectedOutlineParameterName),
    [script.parameters, selectedOutlineParameterName],
  )
  const selectedOutlineParameter = useMemo(
    () => selectedOutlineParameterIndex >= 0 ? (script.parameters || [])[selectedOutlineParameterIndex] : null,
    [script.parameters, selectedOutlineParameterIndex],
  )
  const selectedParameterReferenceLines = useMemo(
    () => parameterReferenceLines(jsonText, parameterMatchRanges),
    [jsonText, parameterMatchRanges],
  )
  const selectedParameterDataInfo = useMemo((): { fileName: string; rows: number | null; unique: number | null } | null => {
    const parameter = selectedOutlineParameter
    if (!parameter?.data_source) return null
    const sourceIndex = findDataSourceIndexByName(script.data_sources, parameter.data_source)
    const source = sourceIndex !== null ? script.data_sources?.[sourceIndex] : null
    const fileName = source?.path || source?.name || parameter.data_source || ''
    if (!source) return { fileName, rows: null, unique: null }
    const parsed = parseInlineData(source)
    if (parsed.rows.length === 0) return { fileName, rows: null, unique: null }
    const columnIndex = parameter.column ? parsed.columns.findIndex(column => column === parameter.column) : -1
    const unique = countUniqueColumnValues(parsed, columnIndex >= 0 ? [columnIndex] : [])
    return { fileName, rows: parsed.rows.length, unique }
  }, [selectedOutlineParameter, script.data_sources])
  const selectedParameterSameLineOptions = useMemo(() => {
    const options = getSameLineParameterOptions(script.parameters || [], selectedOutlineParameterIndex)
    return selectedOutlineParameter
      ? getSameLineParameterSelectOptions(selectedOutlineParameter, options)
      : options
  }, [script.parameters, selectedOutlineParameterIndex, selectedOutlineParameter])
  const activeAutoParameterCandidate = useMemo(
    () => parameterCandidates.find(candidate => candidate.id === activeAutoParameterCandidateId) || parameterCandidates[0] || null,
    [activeAutoParameterCandidateId, parameterCandidates],
  )
  const normalizedAutoParameterOccurrenceIndex = normalizeOccurrenceIndex(
    activeAutoParameterCandidate?.occurrences.length || 0,
    activeAutoParameterOccurrenceIndex,
  )
  const activeAutoParameterOccurrence = activeAutoParameterCandidate
    ? activeAutoParameterCandidate.occurrences[normalizedAutoParameterOccurrenceIndex] || null
    : null
  const autoParameterSidebarActive = scriptJsonSidebarTab === 'auto-params'
    && (!selectedOutlineParameterExists || parameterPaneShowAuto)
  const activeAutoParameterPath = autoParameterSidebarActive
    ? codePathForParameterCandidateOccurrence(activeAutoParameterOccurrence)
    : ''
  const selectedAuthorJsonScope = useMemo(
    () => resolveAuthorJsonScope(script, authorCodeFocus),
    [authorCodeFocus, script],
  )
  const selectedAuthorJsonText = useMemo(
    () => selectedAuthorJsonScope ? JSON.stringify(selectedAuthorJsonScope.action, null, 2) : '',
    [selectedAuthorJsonScope],
  )

  useEffect(() => {
    if (!selectedOutlineParameterName) return
    if (script.parameters?.some(parameter => parameter.name === selectedOutlineParameterName)) return
    setSelectedOutlineParameterName(null)
  }, [script.parameters, selectedOutlineParameterName])

  useEffect(() => {
    if (parameterCandidates.length === 0) {
      setActiveAutoParameterCandidateId(null)
      setActiveAutoParameterOccurrenceIndex(0)
      return
    }
    setActiveAutoParameterCandidateId(current => (
      current && parameterCandidates.some(candidate => candidate.id === current)
        ? current
        : parameterCandidates[0].id
    ))
  }, [parameterCandidates])

  useEffect(() => {
    if (!activeAutoParameterCandidate) return
    setActiveAutoParameterOccurrenceIndex(index => normalizeOccurrenceIndex(
      activeAutoParameterCandidate.occurrences.length,
      index,
    ))
  }, [activeAutoParameterCandidate])

  const requestJsonLineJump = useCallback((line: number) => {
    setJsonLineJumpRequest({ line, nonce: Date.now() })
  }, [])

  return {
    activeAutoParameterCandidate,
    activeAutoParameterOccurrence,
    activeAutoParameterOccurrenceIndex,
    activeAutoParameterPath,
    authorCodeFocus,
    autoParameterSidebarActive,
    jsonLineJumpRequest,
    normalizedAutoParameterOccurrenceIndex,
    outlineActionFilter,
    outlineActionListCollapsed,
    outlineParameterListCollapsed,
    parameterMatchRanges,
    parameterPaneShowAuto,
    parameterReferenceCounts,
    requestJsonLineJump,
    scriptJsonSidebarTab,
    selectedAuthorJsonScope,
    selectedAuthorJsonText,
    selectedOutlineParameter,
    selectedOutlineParameterExists,
    selectedOutlineParameterIndex,
    selectedOutlineParameterName,
    selectedParameterDataInfo,
    selectedParameterReferenceLines,
    selectedParameterSameLineOptions,
    setActiveAutoParameterCandidateId,
    setActiveAutoParameterOccurrenceIndex,
    setAuthorCodeFocus,
    setOutlineActionFilter,
    setOutlineActionListCollapsed,
    setOutlineParameterListCollapsed,
    setParameterPaneShowAuto,
    setScriptJsonSidebarTab,
    setSelectedOutlineParameterName,
    setShowFullScriptJson,
    showFullScriptJson,
  }
}

export function useJsonParameterMenu({
  parameters,
  onBeginParameterEdit,
  onChooseExistingParameter,
  onReviewOccurrences,
}: {
  parameters: ScriptParameterDefinition[]
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  onChooseExistingParameter: (draft: ActionEditorParameterDraft) => void
  onReviewOccurrences: (draft: ActionEditorParameterDraft) => void
}) {
  const [selectionState, setSelectionState] = useState<ActionEditorSelectionState | null>(null)

  useEffect(() => {
    if (!selectionState) return
    const closeMenu = () => setSelectionState(null)
    window.addEventListener('click', closeMenu)
    return () => window.removeEventListener('click', closeMenu)
  }, [selectionState])

  const openSelectionMenu = useCallback((selection: JsonPaneSelectionRequest) => {
    setSelectionState(selectionStateForJsonRequest(selection, parameters))
  }, [parameters])

  const buildDraft = useCallback((nextExistingName?: string | null) => (
    buildJsonParameterDraft(selectionState, parameters, nextExistingName)
  ), [parameters, selectionState])

  const openParameterEditor = useCallback((nextExistingName?: string | null) => {
    const draft = buildDraft(nextExistingName)
    if (!draft) return
    setSelectionState(null)
    onBeginParameterEdit(draft)
  }, [buildDraft, onBeginParameterEdit])

  const openExistingParameterChooser = useCallback(() => {
    const draft = buildDraft(null)
    if (!draft) return
    setSelectionState(null)
    onChooseExistingParameter(draft)
  }, [buildDraft, onChooseExistingParameter])

  const openOccurrenceReview = useCallback(() => {
    const draft = buildDraft(null)
    if (!draft) return
    setSelectionState(null)
    onReviewOccurrences(draft)
  }, [buildDraft, onReviewOccurrences])

  return {
    openExistingParameterChooser,
    openOccurrenceReview,
    openParameterEditor,
    openSelectionMenu,
    selectionState,
  }
}
