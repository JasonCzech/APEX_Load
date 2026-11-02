import type { ScriptDebugBreakpoint } from '../types/replay'
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

type ParsedActionStepSegment =
	| { name: 'requests' | 'then' | 'else' | 'loop' | 'default'; index: number }
	| { name: 'case'; caseIndex: number; index: number }
  | { name: 'parallel'; branchIndex: number; index: number }

function parseActionStepSegment(segment: string): ParsedActionStepSegment | null {
	const branch = segment.match(/^(requests|then|else|loop|default)\[(\d+)\]$/)
	if (branch) {
		return {
			name: branch[1] as 'requests' | 'then' | 'else' | 'loop' | 'default',
			index: Number.parseInt(branch[2], 10),
		}
	}
  const parallel = segment.match(/^branches\[(\d+)\]\.steps\[(\d+)\]$/)
  if (parallel) return { name: 'parallel', branchIndex: Number(parallel[1]), index: Number(parallel[2]) }
	const switchCase = segment.match(/^case\[(\d+)\]\[(\d+)\]$/)
	if (!switchCase) return null
	return { name: 'case', caseIndex: Number.parseInt(switchCase[1], 10), index: Number.parseInt(switchCase[2], 10) }
}

export function resolveNestedAction(root: ScriptActionDefinition | undefined, stepPath?: string[]): ScriptActionDefinition | null {
  let current = root
  for (const segment of stepPath || []) {
    const parsed = parseActionStepSegment(segment)
    if (!parsed || !current) return null
    if (parsed.name === 'parallel') {
      current = current.control?.branches?.[parsed.branchIndex]?.steps?.[parsed.index]
    } else if (parsed.name === 'requests') {
      current = rawHttpSequenceRequest(current, parsed.index) ?? undefined
    } else if (parsed.name === 'else') {
      current = current.control?.else?.[parsed.index]
	} else if (parsed.name === 'default') {
	  current = current.control?.default?.[parsed.index]
	} else if (parsed.name === 'case') {
	  current = current.control?.cases?.[parsed.caseIndex]?.steps?.[parsed.index]
    } else {
      current = current.control?.steps?.[parsed.index]
    }
  }
  return current || null
}

/** Update a selected leaf without changing siblings or the container's fields. */
export function updateNestedAction(root: ScriptActionDefinition, stepPath: string[] | undefined, update: (action: ScriptActionDefinition) => ScriptActionDefinition): ScriptActionDefinition {
  if (!stepPath?.length) return update(root)
  if (!resolveNestedAction(root, stepPath)) return root
  const clone = structuredClone(root)
  const target = resolveNestedAction(clone, stepPath)!
  const next = update(target)
  if (next !== target) {
    for (const key of Object.keys(target)) delete (target as unknown as Record<string, unknown>)[key]
    Object.assign(target, next)
  }
  return clone
}

type StepList = { key: 'requests' | 'steps' | 'else' | 'default' | 'case' | 'branch'; group?: number }

function stepListOf(parsed: ParsedActionStepSegment): StepList {
  if (parsed.name === 'parallel') return { key: 'branch', group: parsed.branchIndex }
  if (parsed.name === 'case') return { key: 'case', group: parsed.caseIndex }
  if (parsed.name === 'then' || parsed.name === 'loop') return { key: 'steps' }
  return { key: parsed.name }
}

const sameStepList = (a: StepList, b: StepList) => a.key === b.key && a.group === b.group

function readStepList(action: ScriptActionDefinition, list: StepList): ScriptActionDefinition[] | undefined {
  if (list.key === 'requests') return Array.isArray(action.params?.requests) ? action.params!.requests as ScriptActionDefinition[] : undefined
  if (list.key === 'case') return action.control?.cases?.[list.group!]?.steps
  if (list.key === 'branch') return action.control?.branches?.[list.group!]?.steps
  return action.control?.[list.key]
}

function writeStepList(action: ScriptActionDefinition, list: StepList, items: ScriptActionDefinition[]) {
  if (list.key === 'requests') {
    action.params = { ...(action.params || {}), requests: items }
  } else if (list.key === 'case') {
    const cases = [...(action.control?.cases || [])]
    cases[list.group!] = { ...cases[list.group!], steps: items }
    action.control = { ...action.control, cases }
  } else if (list.key === 'branch') {
    const branches = [...(action.control?.branches || [])]
    branches[list.group!] = { ...branches[list.group!], steps: items }
    action.control = { ...action.control, branches }
  } else {
    action.control = { ...action.control, [list.key]: items }
  }
}

function stepSegmentAt(parsed: ParsedActionStepSegment, index: number, group?: number): string {
  if (parsed.name === 'parallel') return `branches[${group ?? parsed.branchIndex}].steps[${index}]`
  if (parsed.name === 'case') return `case[${group ?? parsed.caseIndex}][${index}]`
  return `${parsed.name}[${index}]`
}

/**
 * Where a step path of the root action before a removal now points: an index
 * into the removal's replacement actions and the step path inside that action,
 * or null when the step was removed (or was a container that collapsed).
 */
export type NestedStepRelocation = (stepPath: readonly string[]) => { index: number; stepPath: string[] } | null

export interface NestedActionRemoval {
  /**
   * What takes the root action's place in its phase: nothing when the removal
   * left it empty, the steps of a parallel block left with one branch, or the
   * default steps of a switch left with no case.
   */
  replacement: ScriptActionDefinition[]
  relocate: NestedStepRelocation
  /** The root action as it was before the removal. */
  root: ScriptActionDefinition
  /** The step path of the removed step inside `root` (empty: `root` itself). */
  removedPath: string[]
}

interface StepRemovalOutcome { actions: ScriptActionDefinition[]; relocate: NestedStepRelocation }

const REMOVED_STEP: StepRemovalOutcome = { actions: [], relocate: () => null }

// A container the DSL validator would reject once a step leaves it is removed
// too (an http_sequence with no request, a loop with no step, an if with
// neither branch, a switch with neither case nor default), or unwrapped (a
// parallel block with a single branch, or a switch left with only its default,
// runs those steps in place). An emptied case or
// parallel branch is dropped first, as neither may be empty.
function normalizeAfterStepRemoval(container: ScriptActionDefinition, list: StepList, relocate: NestedStepRelocation): StepRemovalOutcome {
  const kept: StepRemovalOutcome = { actions: [container], relocate }
  if ((readStepList(container, list) || []).length > 0) return kept
  switch (list.key) {
    case 'requests':
      return REMOVED_STEP
    case 'steps':
      if (container.type === 'control_if') return container.control?.else?.length ? kept : REMOVED_STEP
      return container.type === 'control_loop' || container.type === 'ws_on_message' ? REMOVED_STEP : kept
    case 'else':
      return container.type === 'control_if' && !container.control?.steps?.length ? REMOVED_STEP : kept
    case 'default':
      return kept
    case 'case': {
      const group = list.group!
      const cases = (container.control?.cases || []).filter((_, index) => index !== group)
      if (cases.length === 0) {
        // A switch needs a case, so it cannot stay; its default branch still
        // runs in place rather than vanishing with the last case.
        const fallback = container.control?.default || []
        if (fallback.length === 0) return REMOVED_STEP
        return {
          actions: fallback,
          relocate: path => {
            const moved = relocate(path)
            const parsed = moved?.stepPath.length ? parseActionStepSegment(moved.stepPath[0]) : null
            if (!moved || parsed?.name !== 'default') return null
            return { index: parsed.index, stepPath: moved.stepPath.slice(1) }
          },
        }
      }
      container.control = { ...container.control, cases }
      return {
        actions: [container],
        relocate: path => {
          const moved = relocate(path)
          const parsed = moved?.stepPath.length ? parseActionStepSegment(moved.stepPath[0]) : null
          if (!moved || parsed?.name !== 'case' || parsed.caseIndex < group) return moved
          if (parsed.caseIndex === group) return null
          return { index: moved.index, stepPath: [stepSegmentAt(parsed, parsed.index, parsed.caseIndex - 1), ...moved.stepPath.slice(1)] }
        },
      }
    }
    case 'branch': {
      const group = list.group!
      const branches = (container.control?.branches || []).filter((_, index) => index !== group)
      if (branches.length === 0) return REMOVED_STEP
      const shifted: NestedStepRelocation = path => {
        const moved = relocate(path)
        const parsed = moved?.stepPath.length ? parseActionStepSegment(moved.stepPath[0]) : null
        if (!moved || parsed?.name !== 'parallel' || parsed.branchIndex < group) return moved
        if (parsed.branchIndex === group) return null
        return { index: moved.index, stepPath: [stepSegmentAt(parsed, parsed.index, parsed.branchIndex - 1), ...moved.stepPath.slice(1)] }
      }
      if (branches.length > 1 || container.type !== 'control_parallel') {
        container.control = { ...container.control, branches }
        return { actions: [container], relocate: shifted }
      }
      return {
        actions: branches[0].steps || [],
        relocate: path => {
          const moved = shifted(path)
          const parsed = moved?.stepPath.length ? parseActionStepSegment(moved.stepPath[0]) : null
          if (!moved || parsed?.name !== 'parallel') return null
          return { index: parsed.index, stepPath: moved.stepPath.slice(1) }
        },
      }
    }
  }
}

function removeStepIn(container: ScriptActionDefinition, stepPath: readonly string[]): StepRemovalOutcome | null {
  const parsed = parseActionStepSegment(stepPath[0])
  if (!parsed) return null
  const list = stepListOf(parsed)
  const items = readStepList(container, list)
  const at = parsed.index
  if (!items || at < 0 || at >= items.length) return null
  const inner = stepPath.length === 1 ? REMOVED_STEP : removeStepIn(items[at], stepPath.slice(1))
  if (!inner) return null
  writeStepList(container, list, [...items.slice(0, at), ...inner.actions, ...items.slice(at + 1)])
  const shift = inner.actions.length - 1
  const relocate: NestedStepRelocation = path => {
    const segment = path.length ? parseActionStepSegment(path[0]) : null
    if (!segment || !sameStepList(stepListOf(segment), list) || segment.index < at) return { index: 0, stepPath: [...path] }
    if (segment.index > at) return { index: 0, stepPath: [stepSegmentAt(segment, segment.index + shift), ...path.slice(1)] }
    const moved = inner.relocate(path.slice(1))
    return moved ? { index: 0, stepPath: [stepSegmentAt(segment, at + moved.index), ...moved.stepPath] } : null
  }
  return normalizeAfterStepRemoval(container, list, relocate)
}

/**
 * Remove a selected step inside a container (`http_sequence`,
 * `control_parallel`, `control_if`, `control_loop`, `control_switch`), at any
 * depth, keeping every container on the way a valid one (see
 * normalizeAfterStepRemoval). An empty step path removes `root` itself.
 * Returns null when the path does not name a step of `root`.
 */
export function removeNestedAction(root: ScriptActionDefinition, stepPath: string[] | undefined): NestedActionRemoval | null {
  if (!stepPath?.length) return { replacement: [], relocate: () => null, root, removedPath: [] }
  if (!resolveNestedAction(root, stepPath)) return null
  const outcome = removeStepIn(structuredClone(root), stepPath)
  return outcome ? { replacement: outcome.actions, relocate: outcome.relocate, root, removedPath: [...stepPath] } : null
}

// The step paths of every http_sequence in `action` (the root itself is []),
// at any depth, in outline order.
export function sequenceStepPaths(action: ScriptActionDefinition, prefix: string[] = [], out: string[][] = []): string[][] {
  if (action.type === 'http_sequence') {
    out.push(prefix)
    return out
  }
  const control = action.control
  if (!control) return out
  const visit = (steps: ScriptActionDefinition[] | undefined, segment: (index: number) => string) =>
    (steps || []).forEach((child, index) => { if (child) sequenceStepPaths(child, [...prefix, segment(index)], out) })
  visit(control.steps, index => `${action.type === 'control_loop' ? 'loop' : 'then'}[${index}]`)
  visit(control.else, index => `else[${index}]`)
  ;(control.cases || []).forEach((switchCase, caseIndex) => visit(switchCase.steps, index => `case[${caseIndex}][${index}]`))
  visit(control.default, index => `default[${index}]`)
  ;(control.branches || []).forEach((branch, branchIndex) => visit(branch.steps, index => `branches[${branchIndex}].steps[${index}]`))
  return out
}

const isPathPrefix = (prefix: readonly string[], path: readonly string[]) =>
  prefix.length <= path.length && prefix.every((segment, index) => path[index] === segment)

// Where a `request_index` breakpoint now points. The outline, F9 and the
// engine give a request of any http_sequence in the root action, nested ones
// included, the same root-level request_index, so the breakpoint is on request
// N of each sequence that has one: it follows the sequence the removal
// changed, else the first sequence whose request N is still there.
function relocateRequestBreakpoint(removal: NestedActionRemoval, requestIndex: number): { index: number; stepPath: string[] } | null {
  const segment = `requests[${requestIndex}]`
  const candidates = sequenceStepPaths(removal.root)
    .filter(path => rawHttpSequenceRequest(resolveNestedAction(removal.root, path)!, requestIndex))
    .map(path => ({ sequence: path, moved: removal.relocate([...path, segment]) }))
  if (!candidates.length) return removal.relocate([segment])
  const affected = candidates.find(candidate => isPathPrefix(candidate.sequence, removal.removedPath) || isPathPrefix(removal.removedPath, candidate.sequence))
  if (affected?.moved) return affected.moved
  return candidates.find(candidate => candidate.moved)?.moved ?? null
}

/**
 * Breakpoints after `removeNestedAction` replaced the root action at
 * `actionIndex`: one on the removed step (or a container that went with it)
 * is dropped, one on a later sibling follows it to its new index, and later
 * actions of the phase shift when a parallel block unwrapped into its steps.
 * A breakpoint on one request of a sequence, top-level or nested, keeps its
 * `request_index` form.
 */
export function remapBreakpointsAfterNestedRemoval(
  breakpoints: ScriptDebugBreakpoint[],
  phase: string,
  actionIndex: number,
  removal: NestedActionRemoval,
): ScriptDebugBreakpoint[] {
  const offset = removal.replacement.length - 1
  return breakpoints.flatMap(bp => {
    if (bp.phase !== phase || bp.action_index < actionIndex) return [bp]
    if (bp.action_index > actionIndex) return [{ ...bp, action_index: bp.action_index + offset }]
    const byRequest = !bp.step_path?.length && bp.request_index != null
    const moved = byRequest ? relocateRequestBreakpoint(removal, bp.request_index!) : removal.relocate(bp.step_path?.length ? bp.step_path : [])
    if (!moved) return []
    const next: ScriptDebugBreakpoint = { ...bp, action_index: actionIndex + moved.index }
    delete next.request_index
    delete next.step_path
    // A request breakpoint keeps its request_index form wherever its sequence
    // now sits, as the outline keys nested sequence requests that way too.
    const last = moved.stepPath[moved.stepPath.length - 1]
    const request = last && (byRequest || moved.stepPath.length === 1) ? /^requests\[(\d+)\]$/.exec(last) : null
    if (request) next.request_index = Number(request[1])
    else if (moved.stepPath.length) next.step_path = moved.stepPath
    return [next]
  })
}

/**
 * Explicitly clears the `har_request_body_not_exported` execution blocker from
 * a step's metadata once the author has supplied a request body.
 */
export function clearUnexportedHarBodyBlockerOnAction(action: ScriptActionDefinition): ScriptActionDefinition {
  if (!action.metadata) return action
  const nextMeta: Record<string, unknown> = { ...action.metadata }
  if (Array.isArray(nextMeta.execution_blockers)) {
    const remaining = nextMeta.execution_blockers.filter(code => code !== 'har_request_body_not_exported')
    if (remaining.length > 0) nextMeta.execution_blockers = remaining
    else delete nextMeta.execution_blockers
  }
  if (nextMeta.execution_blocker === 'har_request_body_not_exported') {
    delete nextMeta.execution_blocker
  }
  delete nextMeta.har_request_body_not_exported
  if (nextMeta.import_review && typeof nextMeta.import_review === 'object' && !Array.isArray(nextMeta.import_review)) {
    // Importers record the review's reasons in reason_codes (one per gating
    // finding), with the first finding's source and resolution.
    const review = { ...(nextMeta.import_review as Record<string, unknown>) }
    const reasonCodes = Array.isArray(review.reason_codes) ? review.reason_codes : []
    const remainingReasons = reasonCodes.filter(code => code !== 'har_request_body_not_exported')
    if (reasonCodes.length > 0 && remainingReasons.length === 0) {
      delete nextMeta.import_review
    } else {
      if (reasonCodes.length > 0) review.reason_codes = remainingReasons
      if (Array.isArray(review.findings)) {
        review.findings = review.findings.filter(code => code !== 'har_request_body_not_exported')
      }
      // A re-capture was this blocker's repair, not one the remaining reasons ask for.
      if (review.resolution === 'recapture_body') review.resolution = 'replace_behavior_and_remove_gate'
      nextMeta.import_review = review
    }
  }
  const hasOtherBlockers = (Array.isArray(nextMeta.execution_blockers) && nextMeta.execution_blockers.length > 0)
    || typeof nextMeta.execution_blocker === 'string'
  const hasOtherReviews = (Array.isArray(nextMeta.review_reasons) && nextMeta.review_reasons.length > 0)
    || typeof nextMeta.review_gate === 'string'
  if (!hasOtherBlockers && !hasOtherReviews) {
    delete nextMeta.review_required
    delete nextMeta.promotion_blocker
  }
  return { ...action, metadata: nextMeta }
}

export function jsonPathForActionStepPath(stepPath?: string[]): string {
  return (stepPath || []).map(segment => {
    const parsed = parseActionStepSegment(segment)
    if (!parsed) return ''
    if (parsed.name === 'parallel') return `.control.branches[${parsed.branchIndex}].steps[${parsed.index}]`
    if (parsed.name === 'requests') return `.params.requests[${parsed.index}]`
    if (parsed.name === 'else') return `.control.else[${parsed.index}]`
	if (parsed.name === 'default') return `.control.default[${parsed.index}]`
	if (parsed.name === 'case') return `.control.cases[${parsed.caseIndex}].steps[${parsed.index}]`
    return `.control.steps[${parsed.index}]`
  }).join('')
}

const JSON_STEP_SEGMENT = /\.params\.requests\[(\d+)\]|\.control\.branches\[(\d+)\]\.steps\[(\d+)\]|\.control\.cases\[(\d+)\]\.steps\[(\d+)\]|\.control\.(steps|else|default)\[(\d+)\]/y

/**
 * The step path the outline gives the step at a JSON path below an action
 * (`.params.requests[1]`, `.control.steps[0]`), the inverse of
 * jsonPathForActionStepPath: `control.steps` is `loop[i]` in a loop and
 * `then[i]` otherwise. Undefined for an empty path, or one that does not
 * name a step of root.
 */
export function stepPathForJsonSubPath(root: ScriptActionDefinition | undefined, subPath: string): string[] | undefined {
  if (!root || !subPath) return undefined
  const stepPath: string[] = []
  let current: ScriptActionDefinition | null = root
  JSON_STEP_SEGMENT.lastIndex = 0
  while (current && JSON_STEP_SEGMENT.lastIndex < subPath.length) {
    const match = JSON_STEP_SEGMENT.exec(subPath)
    if (!match) return undefined
    const [, request, branch, branchStep, switchCase, caseStep, list, listStep] = match
    const segment = request !== undefined ? `requests[${request}]`
      : branch !== undefined ? `branches[${branch}].steps[${branchStep}]`
      : switchCase !== undefined ? `case[${switchCase}][${caseStep}]`
      : list === 'steps' ? `${current.type === 'control_loop' ? 'loop' : 'then'}[${listStep}]`
      : `${list}[${listStep}]`
    current = resolveNestedAction(current, [segment])
    stepPath.push(segment)
  }
  return current ? stepPath : undefined
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
