import type {
  ScriptActionDefinition,
  ScriptDefinition,
  ScriptParameterDefinition,
} from '../types/scripts'
import {
  actionAssertionCount,
  actionDisplayTarget,
  actionDisplayType,
  actionRowLabel,
  httpSequenceRequests,
  rawHttpSequenceRequest,
} from './scriptActionHelpers'
import type { ScriptCodeMatchRange } from './scriptCodeDecorations'
import { escapeRegExp } from './scriptEditorHelpers'
import type {
  ActionEditorParameterDraft,
  CandidateOccurrence,
  ParameterCandidate,
} from './scriptEditorState'
import { sanitizeVarName } from './scriptEditorUtils'
import type { LineRange } from './dslJsonPointers'
import type { ScriptActionPhase } from './scriptEditorWorkbench'

export type JsonDraftParseResult =
  | { ok: true; script: ScriptDefinition }
  | { ok: false; message: string }

export type ScriptJsonStatus = 'synced' | 'dirty' | 'invalid'
export type ScriptJsonSidebarTab = 'insights' | 'auto-params' | 'details'

export const SCRIPT_JSON_DEFAULT_COMPANION_TAB: ScriptJsonSidebarTab = 'insights'

/**
 * Above this size CodeMirror line wrapping is disabled. Wrapped lines require
 * measuring the entire document and defeat viewport virtualization for very
 * large generated scripts.
 */
export const SCRIPT_CODE_LINE_WRAP_MAX_CHARS = 250_000

export interface ScriptCodeLineJumpRequest {
  line: number
  nonce: number
}

export interface JsonPaneSelectionRequest {
  start: number
  end: number
  selectedText: string
  x: number
  y: number
}

export interface ActionEditorSelectionState extends JsonPaneSelectionRequest {
  existingName: string | null
}

export interface AuthorCodeFocus {
  phase: ScriptActionPhase
  actionIndex: number
  stepPath?: string[]
  key?: string
  label?: string
}

export interface AuthorJsonScope {
  action: ScriptActionDefinition
  actionIndex: number
  key: string
  label: string
  path: string
  phase: ScriptActionPhase
  subAction: boolean
}

export interface ScriptJsonDraftState {
  currentScript: ScriptDefinition
  parseMessage: string
  parseResult: JsonDraftParseResult
  status: ScriptJsonStatus
  statusMessage: string
}

export interface ScriptOutlineAction {
  index: number
  actionIndex: number
  phase: ScriptActionPhase
  phaseLabel: string
  name: string
  type: string
  target: string
  isBrowserAction: boolean
  hasTransaction: boolean
  hasWait: boolean
  assertionCount: number
  parameterRefs: string[]
  selector?: string
}

export interface ScriptOutlineSummary {
  actionCount: number
  initCount: number
  mainActionCount: number
  teardownCount: number
  browserActionCount: number
  transactionCount: number
  parameterRefCount: number
  waitCount: number
  assertionCount: number
  actions: ScriptOutlineAction[]
}

export function formatScriptJson(script: ScriptDefinition): string {
  return JSON.stringify({ script }, null, 2)
}

export function parseScriptJsonDraft(text: string): JsonDraftParseResult {
  if (!text.trim()) {
    return { ok: false, message: 'Script JSON is empty.' }
  }
  try {
    const parsed = JSON.parse(text) as ScriptDefinition | { script?: ScriptDefinition }
    const script = 'script' in parsed && parsed.script ? parsed.script : parsed as ScriptDefinition
    return { ok: true, script }
  } catch (error) {
    return {
      ok: false,
      message: error instanceof Error
        ? `Script JSON is invalid: ${error.message}`
        : 'Script JSON is invalid.',
    }
  }
}

export function deriveScriptJsonDraftState(
  text: string,
  dirty: boolean,
  structuredScript: ScriptDefinition,
): ScriptJsonDraftState {
  // A clean JSON pane is only a formatted projection of the structured editor.
  // Trust that source of truth instead of reparsing a potentially very large
  // document after every structured-field edit.
  if (!dirty) {
    return {
      currentScript: structuredScript,
      parseMessage: '',
      parseResult: { ok: true, script: structuredScript },
      status: 'synced',
      statusMessage: 'Synced',
    }
  }
  const parseResult = parseScriptJsonDraft(text)
  const parseMessage = parseResult.ok ? '' : parseResult.message
  const status: ScriptJsonStatus = parseMessage ? 'invalid' : dirty ? 'dirty' : 'synced'
  return {
    currentScript: parseResult.ok ? parseResult.script : structuredScript,
    parseMessage,
    parseResult,
    status,
    statusMessage: parseMessage ? 'Invalid JSON' : dirty ? 'Unsaved JSON edits' : 'Synced',
  }
}

export function synchronizeStructuredScriptJson(
  structuredScript: ScriptDefinition,
  currentText: string,
  dirty: boolean,
  formattedScript = formatScriptJson(structuredScript),
): { changed: boolean; text: string } {
  if (dirty || currentText === formattedScript) return { changed: false, text: currentText }
  return { changed: true, text: formattedScript }
}

export function updateScriptJsonText(
  value: string,
  structuredScript: ScriptDefinition,
): { dirty: boolean; text: string } {
  return { dirty: value !== formatScriptJson(structuredScript), text: value }
}

export function formatScriptJsonDraft(
  text: string,
  structuredScript: ScriptDefinition,
): { ok: true; dirty: boolean; script: ScriptDefinition; text: string } | { ok: false; message: string } {
  const parsed = parseScriptJsonDraft(text)
  if (!parsed.ok) return parsed
  const formatted = formatScriptJson(parsed.script)
  return {
    ok: true,
    dirty: formatted !== formatScriptJson(structuredScript),
    script: parsed.script,
    text: formatted,
  }
}

export function shouldWrapScriptJsonLines(textOrLength: string | number): boolean {
  const length = typeof textOrLength === 'number' ? textOrLength : textOrLength.length
  return length <= SCRIPT_CODE_LINE_WRAP_MAX_CHARS
}

export function findParameterReferencesInJson(text: string, parameterName: string): ScriptCodeMatchRange[] {
  const name = parameterName.trim()
  if (!name) return []
  const ranges: ScriptCodeMatchRange[] = []
  const pattern = new RegExp(`\\{\\{\\s*param\\.${escapeRegExp(name)}\\s*\\}\\}`, 'g')
  let match = pattern.exec(text)
  while (match) {
    ranges.push({ from: match.index, to: match.index + match[0].length })
    match = pattern.exec(text)
  }
  return ranges
}

export function parameterReferenceLines(text: string, ranges: ScriptCodeMatchRange[]): number[] {
  if (ranges.length === 0) return []
  const lines = new Set<number>()
  for (const range of ranges) lines.add(text.slice(0, range.from).split('\n').length)
  return Array.from(lines).sort((left, right) => left - right)
}

export function codePathForParameterCandidateOccurrence(occurrence?: CandidateOccurrence | null): string {
  if (!occurrence) return ''
  if (occurrence.field === 'header' && typeof occurrence.headerIndex === 'number') {
    return `script.actions[${occurrence.actionIdx}].header_list[${occurrence.headerIndex}].value`
  }
  if (occurrence.field === 'header' && occurrence.headerKey) {
    return `script.actions[${occurrence.actionIdx}].headers.${occurrence.headerKey}`
  }
  return `script.actions[${occurrence.actionIdx}].${occurrence.field}`
}

export function formatParameterCandidateOccurrence(occurrence?: CandidateOccurrence | null): string {
  if (!occurrence) return 'No occurrence selected'
  const field = occurrence.field === 'header' && typeof occurrence.headerIndex === 'number'
    ? `header ${occurrence.headerKey || occurrence.headerIndex + 1} (occurrence ${occurrence.headerIndex + 1})`
    : occurrence.field === 'header' && occurrence.headerKey
    ? `header ${occurrence.headerKey}`
    : occurrence.field
  return `Action ${occurrence.actionIdx + 1} / ${field}`
}

export function normalizeOccurrenceIndex(count: number, index: number): number {
  if (count <= 0) return 0
  return ((index % count) + count) % count
}

export function offsetForLine(text: string, lineNo: number): number {
  const targetLine = Math.max(1, lineNo)
  let currentLine = 1
  let offset = 0
  while (currentLine < targetLine && offset < text.length) {
    const nextBreak = text.indexOf('\n', offset)
    if (nextBreak === -1) return text.length
    offset = nextBreak + 1
    currentLine += 1
  }
  return offset
}

export function offsetsForLineRange(text: string, range: LineRange): ScriptCodeMatchRange {
  const from = offsetForLine(text, range.startLine)
  const nextLineOffset = offsetForLine(text, range.endLine + 1)
  return {
    from,
    to: nextLineOffset > from ? nextLineOffset - 1 : text.length,
  }
}

function escapedJsonStringContent(value: string): string {
  return JSON.stringify(value).slice(1, -1)
}

function occurrenceOrdinalForPath(candidate: ParameterCandidate, occurrenceIndex: number, path: string): number {
  let ordinal = 0
  for (let index = 0; index < occurrenceIndex; index += 1) {
    if (codePathForParameterCandidateOccurrence(candidate.occurrences[index]) === path) ordinal += 1
  }
  return ordinal
}

export function findCandidateLiteralRangeInJson(
  jsonText: string,
  lineRange: LineRange | null,
  candidate: ParameterCandidate | null,
  occurrenceIndex: number,
): ScriptCodeMatchRange[] {
  if (!lineRange || !candidate?.value) return []
  const activeOccurrence = candidate.occurrences[occurrenceIndex]
  const activePath = codePathForParameterCandidateOccurrence(activeOccurrence)
  const ordinal = occurrenceOrdinalForPath(candidate, occurrenceIndex, activePath)
  const offsets = offsetsForLineRange(jsonText, lineRange)
  const segment = jsonText.slice(offsets.from, offsets.to)
  const needle = escapedJsonStringContent(candidate.value)
  if (!needle) return []
  let matchOffset = -1
  let searchFrom = 0
  for (let index = 0; index <= ordinal; index += 1) {
    matchOffset = segment.indexOf(needle, searchFrom)
    if (matchOffset < 0) break
    searchFrom = matchOffset + Math.max(1, needle.length)
  }
  if (matchOffset < 0 && ordinal > 0) matchOffset = segment.indexOf(needle)
  if (matchOffset < 0) return []
  return [{ from: offsets.from + matchOffset, to: offsets.from + matchOffset + needle.length }]
}

export function phaseActionsForScript(
  script: ScriptDefinition,
  phase: ScriptActionPhase,
): ScriptActionDefinition[] {
  if (phase === 'init') return script.init || []
  if (phase === 'teardown') return script.teardown || []
  return script.actions || []
}

function parseActionStepSegment(segment: string): { name: 'requests' | 'then' | 'else' | 'loop'; index: number } | null {
  const match = segment.match(/^(requests|then|else|loop)\[(\d+)\]$/)
  if (!match) return null
  return {
    name: match[1] as 'requests' | 'then' | 'else' | 'loop',
    index: Number.parseInt(match[2], 10),
  }
}

function resolveNestedAction(root: ScriptActionDefinition | undefined, stepPath?: string[]): ScriptActionDefinition | null {
  let current = root
  for (const segment of stepPath || []) {
    const parsed = parseActionStepSegment(segment)
    if (!parsed || !current) return null
    if (parsed.name === 'requests') {
      current = rawHttpSequenceRequest(current, parsed.index) ?? undefined
    } else if (parsed.name === 'else') {
      current = current.control?.else?.[parsed.index]
    } else {
      current = current.control?.steps?.[parsed.index]
    }
  }
  return current || null
}

export function jsonPathForActionStepPath(stepPath?: string[]): string {
  return (stepPath || []).map(segment => {
    const parsed = parseActionStepSegment(segment)
    if (!parsed) return ''
    if (parsed.name === 'requests') return `.params.requests[${parsed.index}]`
    if (parsed.name === 'else') return `.control.else[${parsed.index}]`
    return `.control.steps[${parsed.index}]`
  }).join('')
}

export function authorFocusKey(focus: Pick<AuthorCodeFocus, 'phase' | 'actionIndex' | 'stepPath'>): string {
  const base = `${focus.phase}:${focus.actionIndex}`
  return focus.stepPath?.length ? `${base}#${focus.stepPath.join('/')}` : base
}

export function authorFocusFromKey(key: string): AuthorCodeFocus | null {
  const match = key.match(/^(init|action|teardown):(\d+)(?:(:|#|\|)(.+))?$/)
  if (!match) return null
  const actionIndex = Number.parseInt(match[2], 10)
  if (!Number.isInteger(actionIndex) || actionIndex < 0) return null
  const suffixType = match[3]
  const suffix = match[4] || ''
  const stepPath = suffixType === ':'
    ? [`requests[${Number.parseInt(suffix, 10)}]`]
    : suffix
      ? suffix.split('/').filter(Boolean)
      : undefined
  return {
    phase: match[1] as ScriptActionPhase,
    actionIndex,
    stepPath,
    key,
  }
}

export function codePathForAuthorFocus(focus?: AuthorCodeFocus | null): string {
  if (!focus) return ''
  const collection = focus.phase === 'action' ? 'actions' : focus.phase
  return `script.${collection}[${focus.actionIndex}]${jsonPathForActionStepPath(focus.stepPath)}`
}

export function resolveAuthorJsonScope(
  script: ScriptDefinition,
  focus?: AuthorCodeFocus | null,
): AuthorJsonScope | null {
  if (!focus) return null
  const root = phaseActionsForScript(script, focus.phase)[focus.actionIndex]
  const action = resolveNestedAction(root, focus.stepPath)
  if (!action) return null
  const subAction = Boolean(focus.stepPath?.length)
  return {
    action,
    actionIndex: focus.actionIndex,
    key: focus.key || authorFocusKey(focus),
    label: focus.label || actionRowLabel(action),
    path: codePathForAuthorFocus(focus),
    phase: focus.phase,
    subAction,
  }
}

export function resolveAuthorJsonScopeByKey(script: ScriptDefinition, key: string): AuthorJsonScope | null {
  return resolveAuthorJsonScope(script, authorFocusFromKey(key))
}

function extractParameterReferences(action: ScriptActionDefinition): string[] {
  const refs = new Set<string>()
  const serialized = JSON.stringify({
    target: action.target,
    selector: action.selector,
    value: action.value,
    headers: action.headers,
    header_list: action.header_list,
    body: action.body,
    params: action.params,
    metadata: action.metadata,
  })
  const tokenPattern = /\{\{\s*(?:param|data|var)\.([a-zA-Z0-9_.-]+)/g
  let match = tokenPattern.exec(serialized)
  while (match) {
    refs.add(match[1])
    match = tokenPattern.exec(serialized)
  }
  return Array.from(refs)
}

function actionHasWait(action: ScriptActionDefinition): boolean {
  return Boolean(action.wait_for?.type || action.wait_for?.value || action.wait_for?.timeout_ms)
    || action.type === 'browser_wait'
}

function phaseLabel(phase: ScriptActionPhase): string {
  return phase === 'action' ? 'Action' : phase[0].toUpperCase() + phase.slice(1)
}

export function buildScriptOutlineSummary(script: ScriptDefinition): ScriptOutlineSummary {
  const autoTransactions = script.config.run_logic?.auto_transactions
  const actions: ScriptOutlineAction[] = []
  ;(['init', 'action', 'teardown'] as ScriptActionPhase[]).forEach(phase => {
    phaseActionsForScript(script, phase).forEach((action, actionIndex) => {
      const isBrowserAction = (action.type || '').startsWith('browser_')
      const sequenceRefs = httpSequenceRequests(action).flatMap(extractParameterReferences)
      const parameterRefs = [...new Set([...extractParameterReferences(action), ...sequenceRefs])]
      actions.push({
        index: actions.length,
        actionIndex,
        phase,
        phaseLabel: phaseLabel(phase),
        name: action.name || `${phaseLabel(phase)} ${actionIndex + 1}`,
        type: actionDisplayType(action),
        target: actionDisplayTarget(action),
        isBrowserAction,
        hasTransaction: autoTransactions === 'per_action' || Boolean(
          action.metadata?.transaction
          || action.metadata?.transaction_name
          || action.metadata?.source_transaction,
        ),
        hasWait: actionHasWait(action),
        assertionCount: actionAssertionCount(action),
        parameterRefs,
        selector: action.selector,
      })
    })
  })
  return {
    actionCount: actions.length,
    initCount: script.init?.length || 0,
    mainActionCount: script.actions.length,
    teardownCount: script.teardown?.length || 0,
    browserActionCount: actions.filter(action => action.isBrowserAction).length,
    transactionCount: actions.filter(action => action.hasTransaction).length,
    parameterRefCount: actions.reduce((sum, action) => sum + action.parameterRefs.length, 0),
    waitCount: actions.filter(action => action.hasWait).length,
    assertionCount: actions.reduce((sum, action) => sum + action.assertionCount, 0),
    actions,
  }
}

export function detectSelectedJsonParameter(
  selectedText: string,
  parameters: ScriptParameterDefinition[],
): ScriptParameterDefinition | null {
  const match = selectedText.trim().match(/^{{\s*param\.([a-zA-Z0-9_]+)\s*}}$/)
  if (!match) return null
  const normalizedName = sanitizeVarName(match[1])
  return parameters.find(parameter => sanitizeVarName(parameter.name || '') === normalizedName) || null
}

export function selectionStateForJsonRequest(
  selection: JsonPaneSelectionRequest,
  parameters: ScriptParameterDefinition[],
): ActionEditorSelectionState | null {
  if (!selection.selectedText.trim()) return null
  const existingParameter = detectSelectedJsonParameter(selection.selectedText, parameters)
  return { ...selection, existingName: existingParameter?.name || null }
}

export function buildJsonParameterDraft(
  selection: ActionEditorSelectionState | null,
  parameters: ScriptParameterDefinition[],
  nextExistingName?: string | null,
): ActionEditorParameterDraft | null {
  if (!selection) return null
  const existingParameter = nextExistingName
    ? parameters.find(parameter => sanitizeVarName(parameter.name || '') === sanitizeVarName(nextExistingName))
    : detectSelectedJsonParameter(selection.selectedText, parameters)
  const requestedName = existingParameter?.name
    || sanitizeVarName(selection.selectedText.replace(/^{{\s*param\.|\s*}}$/g, ''))
  const seedValue = existingParameter?.values?.[0] || selection.selectedText
  return {
    source: 'json',
    actionIndex: -1,
    start: selection.start,
    end: selection.end,
    selectedText: selection.selectedText,
    existingName: existingParameter?.name || null,
    requestedName,
    seedValue,
  }
}
