// scriptEditorWorkbench — pure workbench logic extracted from ScriptEditor.tsx
// (behavior-preserving). These helpers have no React/DOM dependencies and are
// unit-tested in scriptEditorWorkbench.test.ts.
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { ProtocolEvidence } from '../types'
import { httpSequenceRequests } from './scriptActionHelpers'
import { resolveNestedAction } from './scriptEditorJsonModel'

// Compatibility exports for callers that still treat the workbench module as
// the Script JSON utility entry point. New code should import from
// scriptEditorJsonModel directly.
export {
  findParameterReferencesInJson,
  formatScriptJson,
  parseScriptJsonDraft,
} from './scriptEditorJsonModel'
export type { JsonDraftParseResult } from './scriptEditorJsonModel'

export type ScriptActionPhase = 'init' | 'action' | 'teardown'

export type ScriptPhaseActionRow = {
  phase: ScriptActionPhase
  phaseLabel: string
  actionIndex: number
  globalIndex: number
  action: ScriptActionDefinition
}

export type DebuggerResizeKind = 'rail' | 'vars' | 'bottom'

export type CenterWorkbenchTabId =
  | 'action-list'
  | 'request-body'
  | 'source'
  | 'runtime-settings'
  | 'imported-metadata'
  | 'metadata-json'
  | 'parameter-data'
  | 'browser-replay'
  | 'object-descriptors'
  | 'http-conversion'
  | 'debug-summary'

export interface CenterTabsState {
  tabs: CenterWorkbenchTabId[]
  activeTabId: CenterWorkbenchTabId
}

export type BrowserEngineName = 'chromium' | 'webkit'

export type BrowserDeviceProfileOption = {
  name: string
  engine: BrowserEngineName
}

export const BROWSER_DEVICE_PROFILES: readonly BrowserDeviceProfileOption[] = [
  { name: 'desktop_1080p', engine: 'chromium' },
  { name: 'galaxy_s24', engine: 'chromium' },
  { name: 'ipad_air', engine: 'webkit' },
  { name: 'iphone_15_pro', engine: 'webkit' },
  { name: 'iphone_se', engine: 'webkit' },
  { name: 'pixel_8', engine: 'chromium' },
]

export const ACTION_LIST_TAB_ID: CenterWorkbenchTabId = 'action-list'
export const SCRIPT_JSON_TAB_ID: CenterWorkbenchTabId = 'source'
export const CENTER_TABS_STORAGE_PREFIX = 'scriptEditorCenterTabs'
export const CENTER_WORKBENCH_TAB_IDS: readonly CenterWorkbenchTabId[] = [
  'action-list',
  'source',
  'request-body',
  'runtime-settings',
  'imported-metadata',
  'metadata-json',
  'parameter-data',
  'browser-replay',
  'object-descriptors',
  'http-conversion',
  'debug-summary',
]

export const DEBUGGER_LAYOUT_DEFAULTS: Record<DebuggerResizeKind, number> = {
  // The outline plus its breakpoint gutter, at the room Author's outline has.
  rail: 300,
  vars: 320,
  bottom: 220,
}

export const DEBUGGER_LAYOUT_LIMITS: Record<DebuggerResizeKind, { max: number; min: number; step: number }> = {
  rail: { min: 180, max: 560, step: 24 },
  // The effective maximum is the available workbench width, not a fixed pixel cap.
  vars: { min: 220, max: Number.POSITIVE_INFINITY, step: 24 },
  // The effective maximum is 50% of the window height, not a fixed pixel cap.
  bottom: { min: 140, max: Number.POSITIVE_INFINITY, step: 24 },
}

export function isCenterWorkbenchTabId(value: unknown): value is CenterWorkbenchTabId {
  return typeof value === 'string' && CENTER_WORKBENCH_TAB_IDS.includes(value as CenterWorkbenchTabId)
}

function normalizeCenterWorkbenchTabId(value: unknown): CenterWorkbenchTabId | null {
  if (value === 'configuration') return 'runtime-settings'
  if (value === 'script-json') return SCRIPT_JSON_TAB_ID
  return isCenterWorkbenchTabId(value) ? value : null
}

export function defaultCenterTabsState(): CenterTabsState {
  return {
    tabs: [ACTION_LIST_TAB_ID, SCRIPT_JSON_TAB_ID, 'runtime-settings'],
    activeTabId: ACTION_LIST_TAB_ID,
  }
}

export function readCenterTabsState(storageKey: string): CenterTabsState {
  if (typeof window === 'undefined') return defaultCenterTabsState()
  try {
    const raw = window.localStorage.getItem(storageKey)
    if (!raw) return defaultCenterTabsState()
    const parsed = JSON.parse(raw) as Partial<CenterTabsState>
    const storedTabs = Array.isArray(parsed.tabs)
      ? parsed.tabs
        .map(normalizeCenterWorkbenchTabId)
        .filter((tab): tab is CenterWorkbenchTabId => Boolean(tab))
      : []
    const normalizedTabs = [
      ACTION_LIST_TAB_ID,
      SCRIPT_JSON_TAB_ID,
      ...storedTabs.filter((tab, index, tabs) => (
        tab !== ACTION_LIST_TAB_ID &&
        tab !== SCRIPT_JSON_TAB_ID &&
        tabs.indexOf(tab) === index
      )),
    ]
    // A fixed tab can be active without being in the open list; the editor
    // falls back to the action editor when the script does not offer it.
    const activeTabId = normalizeCenterWorkbenchTabId(parsed.activeTabId) ?? ACTION_LIST_TAB_ID
    return {
      tabs: normalizedTabs,
      activeTabId,
    }
  } catch {
    return defaultCenterTabsState()
  }
}

export function clampDebuggerPaneSize(kind: DebuggerResizeKind, value: number) {
  const limits = DEBUGGER_LAYOUT_LIMITS[kind]
  return Math.min(limits.max, Math.max(limits.min, Math.round(value)))
}

export function codePathForDebugRow(row?: { phase: string; actionIndex: number } | null) {
  if (!row) return ''
  if (row.phase === 'action') return `script.actions[${row.actionIndex}]`
  if (row.phase === 'init') return `script.init[${row.actionIndex}]`
  if (row.phase === 'teardown') return `script.teardown[${row.actionIndex}]`
  return ''
}

// resolveDebugRowForEvent maps a replay event to its top-level action row by the
// event's OWN (phase, action_index) rather than by positionally indexing the flat
// event list with the event index. The two diverge as soon as a script has an
// http_sequence sub-request or a control-flow iteration before the selection
// (one event per sub-leaf/iteration, one row per top-level action), which made
// the code pane highlight — and breakpoints toggle — the wrong action. Returns
// null when no row matches the event's coordinates.
export function resolveDebugRowForEvent(
  rows: ScriptPhaseActionRow[],
  event: { phase?: unknown; action_index?: unknown } | null | undefined,
): ScriptPhaseActionRow | null {
  if (!event) return null
  const rawPhase = String(event.phase ?? 'action')
  const phase: ScriptActionPhase = rawPhase === 'init' || rawPhase === 'teardown' ? rawPhase : 'action'
  const actionIndex = Number(event.action_index)
  if (!Number.isInteger(actionIndex)) return null
  return rows.find((r) => r.phase === phase && r.actionIndex === actionIndex) ?? null
}

/** Where a keyboard breakpoint goes: an action, one request of a sequence, or a nested step. */
export type ShortcutBreakpointTarget =
  | { phase: string; actionIndex: number; requestIndex?: number }
  | { phase: string; actionIndex: number; stepPath: string[] }

export interface ShortcutBreakpointContext {
  /** The script as it is now: an open replay event may be from an older draft. */
  script: ScriptDefinition
  workspaceTab: string
  /** The replay event whose evidence is open in Debug. */
  selectedEvent?: { phase?: string; action_index?: number; request_index?: number; step_path?: string[] } | null
  /**
   * The open event comes from a replay of the steps as they are now. After a
   * structural edit its action index may name a different step, so it takes
   * no breakpoint, as its Edit action is unavailable.
   */
  selectedEventFromCurrentDraft?: boolean
  /**
   * The step selected in the outline (Author's, which Debug's outline shows
   * too), at any phase and depth, with the step itself when the caller
   * resolved it: a nested block is not a breakpoint location.
   */
  focus?: { phase: string; actionIndex: number; stepPath?: string[]; action?: ScriptActionDefinition | null } | null
  activeAction: number | null
  firstRow?: { phase: string; actionIndex: number } | null
}

function shortcutBreakpointLocation(phase: string, actionIndex: number, stepPath?: readonly string[]): ShortcutBreakpointTarget {
  if (!stepPath?.length) return { phase, actionIndex }
  // A request of a sequence, top-level or nested, is addressed by its index, as
  // the outline's own toggle and gutter key it (containerChildSteps gives every
  // sequence child a requestIndex), so F9 sets the breakpoint the row shows.
  const request = /^requests\[(\d+)\]$/.exec(stepPath[stepPath.length - 1])
  return request ? { phase, actionIndex, requestIndex: Number(request[1]) } : { phase, actionIndex, stepPath: [...stepPath] }
}

/**
 * The step F9 and Cmd/Ctrl+B toggle a breakpoint on, when no outline row has
 * keyboard focus (the outline handles both keys for its focused row): in Debug
 * the open replay event's step; otherwise the selected step, which may be in
 * init or teardown or nested in a block; then the active action; then the
 * script's first step. A nested block (If, Switch, Loop, Parallel, Sequence)
 * selected in the outline takes none: replay pauses only before the steps it
 * runs, never on a nested block's own path, and the outline shows no
 * breakpoint on a block row, so one set there would never fire or show.
 */
export function shortcutBreakpointTarget({ script, workspaceTab, selectedEvent, selectedEventFromCurrentDraft, focus, activeAction, firstRow }: ShortcutBreakpointContext): ShortcutBreakpointTarget | null {
  if (workspaceTab === 'debug' && selectedEvent && typeof selectedEvent.action_index === 'number') {
    const phase = selectedEvent.phase || 'action'
    // Evidence from a replay of an older draft names steps by their place
    // then: that place may now hold another step, or none. It takes no
    // breakpoint, which would land on the wrong step or never show or fire.
    if (!selectedEventFromCurrentDraft) return null
    const root = scriptActionsForPhase(script, phase === 'init' || phase === 'teardown' ? phase : 'action')[selectedEvent.action_index]
    const eventPath = selectedEvent.step_path?.length ? selectedEvent.step_path : selectedEvent.request_index != null ? [`requests[${selectedEvent.request_index}]`] : []
    if (!root || (eventPath.length && !resolveNestedAction(root, eventPath))) return null
    return shortcutBreakpointLocation(phase, selectedEvent.action_index, selectedEvent.request_index != null ? [`requests[${selectedEvent.request_index}]`] : selectedEvent.step_path)
  }
  if (focus) {
    if (focus.stepPath?.length && focus.action && debugContainerKind(focus.action)) return null
    return shortcutBreakpointLocation(focus.phase, focus.actionIndex, focus.stepPath)
  }
  if (activeAction !== null) return { phase: 'action', actionIndex: activeAction }
  return firstRow ? { phase: firstRow.phase, actionIndex: firstRow.actionIndex } : null
}

export function scriptActionsForPhase(script: ScriptDefinition, phase: ScriptActionPhase): ScriptActionDefinition[] {
  if (phase === 'init') return script.init || []
  if (phase === 'teardown') return script.teardown || []
  return script.actions || []
}

export function formatDebugPhaseLabel(phase: string): string {
  return phase.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

export function buildScriptPhaseActionRows(script: ScriptDefinition): ScriptPhaseActionRow[] {
  const rows: ScriptPhaseActionRow[] = []
  ;(['init', 'action', 'teardown'] as ScriptActionPhase[]).forEach(phase => {
    scriptActionsForPhase(script, phase).forEach((action, actionIndex) => {
      rows.push({
        phase,
        phaseLabel: formatDebugPhaseLabel(phase),
        actionIndex,
        globalIndex: rows.length,
        action,
      })
    })
  })
  return rows
}

// debugSequenceKey is the collapse/breakpoint key for a top-level action row
// (matches debugBreakpointKey(phase, actionIndex) on the React side).
export function debugSequenceKey(phase: string, actionIndex: number): string {
  return `${phase}:${actionIndex}`
}

// debugContainerKind reports which container an action is, mirroring the engine's
// replayContainerKind (http_sequence + control_if/control_switch/control_loop).
export function debugContainerKind(action: ScriptActionDefinition): '' | 'http_sequence' | 'control_if' | 'control_switch' | 'control_loop' | 'control_parallel' {
  const t = (action.type || '').toLowerCase().trim()
  if (t === 'http_sequence') return 'http_sequence'
	if (t === 'control_if' || t === 'control_switch' || t === 'control_loop' || t === 'control_parallel') return t
  return ''
}

// debugStepPathKey is the breakpoint/lookup key for a control-flow leaf, keyed by
// its static step path (distinct namespace from the http_sequence requestIndex key).
export function debugStepPathKey(phase: string, actionIndex: number, stepPath: string[]): string {
  return `${phase}:${actionIndex}#${stepPath.join('/')}`
}

// containerExpandKey identifies a (possibly nested) container node for expand state.
function containerExpandKey(phase: string, actionIndex: number, stepPath: string[]): string {
  return stepPath.length ? `${debugSequenceKey(phase, actionIndex)}|${stepPath.join('/')}` : debugSequenceKey(phase, actionIndex)
}

// containerChildSteps returns the static child steps of a container action paired
// with the step-path segment the engine assigns each (requests[i] / then[i] /
// else[i] / case[n][i] / default[i] / loop[i]).
export function containerChildSteps(action: ScriptActionDefinition, kind: string): Array<{ action: ScriptActionDefinition; segment: string; requestIndex?: number }> {
  if (kind === 'http_sequence') {
    return httpSequenceRequests(action).map((child, i) => ({ action: child, segment: `requests[${i}]`, requestIndex: i }))
  }
  if (kind === 'control_if') {
    const then = (action.control?.steps || []).map((child, i) => ({ action: child, segment: `then[${i}]` }))
    const els = (action.control?.else || []).map((child, i) => ({ action: child, segment: `else[${i}]` }))
    return [...then, ...els]
  }
  if (kind === 'control_loop') {
    return (action.control?.steps || []).map((child, i) => ({ action: child, segment: `loop[${i}]` }))
  }
	if (kind === 'control_switch') {
		const cases = (action.control?.cases || []).flatMap((switchCase, caseIndex) =>
			(switchCase.steps || []).map((child, stepIndex) => ({ action: child, segment: `case[${caseIndex}][${stepIndex}]` })),
		)
		const fallback = (action.control?.default || []).map((child, stepIndex) => ({ action: child, segment: `default[${stepIndex}]` }))
		return [...cases, ...fallback]
	}
	if (kind === 'control_parallel') {
		return (action.control?.branches || []).flatMap((branch, branchIndex) =>
			(branch.steps || []).map((child, stepIndex) => ({ action: child, segment: `branches[${branchIndex}].steps[${stepIndex}]` })),
		)
	}
  return []
}

// DebugRailRenderRow is the flattened, render-ready row list for the debugger's
// ACTION LIST. Container actions (http_sequence / control_if / control_switch / control_loop)
// become collapsible `container` rows; when expanded they are followed by their
// child steps (`leaf`, or nested `container`). Plain actions pass through as
// `action`, so non-container scripts render exactly as before.
export type DebugRailRenderRow =
  | { kind: 'action'; key: string; depth: number; row: ScriptPhaseActionRow }
	| { kind: 'container'; key: string; depth: number; row: ScriptPhaseActionRow; containerType: 'http_sequence' | 'control_if' | 'control_switch' | 'control_loop' | 'control_parallel'; name: string; leafCount: number; expanded: boolean; stepPath: string[] }
  | { kind: 'leaf'; key: string; depth: number; row: ScriptPhaseActionRow; name: string; method: string; target: string; stepPath: string[]; requestIndex?: number; childAction: ScriptActionDefinition }

// countContainerLeaves counts the static leaf steps under a container (recursively).
function countContainerLeaves(action: ScriptActionDefinition): number {
  const kind = debugContainerKind(action)
  if (!kind) return 0
  return containerChildSteps(action, kind).reduce((n, step) => n + (debugContainerKind(step.action) ? countContainerLeaves(step.action) : 1), 0)
}

export function buildDebugRailRenderRows(rows: ScriptPhaseActionRow[], expandedKeys: Set<string>): DebugRailRenderRow[] {
  const out: DebugRailRenderRow[] = []
	const emitContainer = (row: ScriptPhaseActionRow, action: ScriptActionDefinition, kind: 'http_sequence' | 'control_if' | 'control_switch' | 'control_loop' | 'control_parallel', stepPath: string[], depth: number) => {
    const expanded = expandedKeys.has(containerExpandKey(row.phase, row.actionIndex, stepPath))
    out.push({
      kind: 'container',
      key: containerExpandKey(row.phase, row.actionIndex, stepPath),
      depth, row, containerType: kind,
	  name: action.name || (kind === 'http_sequence' ? 'Sequence' : kind === 'control_if' ? 'If / Else' : kind === 'control_switch' ? 'Switch' : kind === 'control_parallel' ? 'Parallel' : 'Loop'),
      leafCount: countContainerLeaves(action),
      expanded, stepPath,
    })
    if (!expanded) return
    for (const step of containerChildSteps(action, kind)) {
      const childPath = [...stepPath, step.segment]
      const childKind = debugContainerKind(step.action)
      if (childKind) {
        emitContainer(row, step.action, childKind, childPath, depth + 1)
      } else {
        out.push({
          kind: 'leaf',
          key: step.requestIndex != null ? `${debugSequenceKey(row.phase, row.actionIndex)}:${step.requestIndex}` : debugStepPathKey(row.phase, row.actionIndex, childPath),
          depth: depth + 1, row,
          name: step.action.name || step.segment,
          method: step.action.method || step.action.type || 'request',
          target: step.action.target || '',
          stepPath: childPath,
          requestIndex: step.requestIndex,
          childAction: step.action,
        })
      }
    }
  }

  for (const row of rows) {
    const kind = debugContainerKind(row.action)
    if (kind) {
      emitContainer(row, row.action, kind, [], 0)
    } else {
      out.push({ kind: 'action', key: debugSequenceKey(row.phase, row.actionIndex), depth: 0, row })
    }
  }
  return out
}

// Pure, immutable list helpers for editing a sequence of actions — nested
// control-flow steps and (later) top-level phase actions. Each preserves the
// other entries verbatim, including unknown/advanced keys (R1), so structured
// editing never silently drops fields from an imported script.

export function appendActionStep(
  steps: ScriptActionDefinition[] | undefined,
  step: ScriptActionDefinition,
): ScriptActionDefinition[] {
  return [...(steps || []), step]
}

export function removeActionStep(
  steps: ScriptActionDefinition[],
  index: number,
): ScriptActionDefinition[] {
  return steps.filter((_, i) => i !== index)
}

/** Move the item at `index` by `delta` (-1/+1); out-of-range moves are no-ops. */
export function moveActionStep(
  steps: ScriptActionDefinition[],
  index: number,
  delta: number,
): ScriptActionDefinition[] {
  const target = index + delta
  if (index < 0 || index >= steps.length || target < 0 || target >= steps.length) return steps
  const next = [...steps]
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}

/** Update one field of the step at `index`, preserving its other (incl. unknown) keys. */
export function updateActionStepField(
  steps: ScriptActionDefinition[],
  index: number,
  field: keyof ScriptActionDefinition,
  value: unknown,
): ScriptActionDefinition[] {
  return steps.map((step, i) => (i === index ? { ...step, [field]: value } : step))
}

/**
 * A unique action name derived from `base`, avoiding collisions in `existing`.
 * Duplicate action names are flagged by the importer/validator, so a copied
 * action needs a fresh name.
 */
export function uniqueActionName(base: string, existing: string[]): string {
  const used = new Set(existing)
  const root = base || 'action'
  let candidate = `${root} (copy)`
  let n = 2
  while (used.has(candidate)) {
    candidate = `${root} (copy ${n})`
    n += 1
  }
  return candidate
}

const MASKED_VALUE = '[REDACTED]'

// Where a clone lost a masked value, in the words the duplicate notice uses:
// `body`, `target`, `header Authorization`, `cookie sid`, `assertions`.
type MaskedValueRemovals = Set<string>

function scrubRedactedStringsInPlace(node: unknown, removals: MaskedValueRemovals, label: string, skipKeys?: ReadonlySet<string>): unknown {
  if (typeof node === 'string') {
    if (!node.includes(MASKED_VALUE)) return node
    removals.add(label)
    return node.split(MASKED_VALUE).join('')
  }
  if (Array.isArray(node)) {
    for (let i = 0; i < node.length; i++) {
      node[i] = scrubRedactedStringsInPlace(node[i], removals, label)
    }
    return node
  }
  if (node && typeof node === 'object') {
    const record = node as Record<string, unknown>
    for (const key of Object.keys(record)) {
      if (skipKeys?.has(key)) continue
      record[key] = scrubRedactedStringsInPlace(record[key], removals, label)
    }
  }
  return node
}

// A Cookie header keeps its unmasked cookies: only the pairs whose value was
// masked go (`sid=[REDACTED]; theme=dark` keeps `theme=dark`).
function withoutMaskedCookies(value: string, removals: MaskedValueRemovals): string {
  return value.split(';').map(pair => pair.trim()).filter(pair => {
    if (!pair.includes(MASKED_VALUE)) return pair !== ''
    removals.add(`cookie ${pair.split('=')[0].trim() || 'value'}`)
    return false
  }).join('; ')
}

// A header whose value was masked is left out of the clone (an empty
// credential header only fails differently), except a Cookie header, which
// keeps the cookies that were not masked.
function withoutMaskedHeader(name: string, value: string, removals: MaskedValueRemovals): string | null {
  if (name.includes(MASKED_VALUE)) {
    removals.add('header')
    return null
  }
  if (!value.includes(MASKED_VALUE)) return value
  if (name.trim().toLowerCase() === 'cookie') {
    const kept = withoutMaskedCookies(value, removals)
    return kept || null
  }
  removals.add(`header ${name}`)
  return null
}

function forEachNestedStep(action: ScriptActionDefinition, visit: (child: ScriptActionDefinition) => void) {
  const ctrl = action.control as unknown as Record<string, unknown> | undefined
  if (ctrl && typeof ctrl === 'object') {
    for (const key of ['steps', 'else', 'default'] as const) {
      if (Array.isArray(ctrl[key])) for (const child of ctrl[key] as ScriptActionDefinition[]) visit(child)
    }
    // Switch cases and parallel branches each hold their own steps.
    for (const key of ['cases', 'branches'] as const) {
      if (!Array.isArray(ctrl[key])) continue
      for (const group of ctrl[key] as Array<{ steps?: ScriptActionDefinition[] }>) {
        if (Array.isArray(group?.steps)) for (const child of group.steps) visit(child)
      }
    }
  }
  if (action.params && typeof action.params === 'object') {
    const params = action.params as Record<string, unknown>
    if (Array.isArray(params.requests)) {
      for (const child of params.requests as ScriptActionDefinition[]) {
        if (child && typeof child === 'object') visit(child)
      }
    }
    if (params.request && typeof params.request === 'object' && !Array.isArray(params.request)) {
      visit(params.request as ScriptActionDefinition)
    }
  }
}

function stripClonedActionImportProvenance(action: ScriptActionDefinition, removals: MaskedValueRemovals): void {
  if (action.metadata && typeof action.metadata === 'object') {
    const meta = action.metadata as Record<string, unknown>
    delete meta.import_action_uid
    delete meta.import_credential_targets
    delete meta.import_credential_target_keys
    delete meta.import_header_fidelity
    delete meta.import_multipart_fidelity
    delete meta.restored_body_protected
    // A read projection's fail-closed gate (ApplyScriptProjectionResponseGate)
    // belongs to the response copy, not the behavior: the server drops it the
    // same way (WithoutTransientScriptProjectionGate) — the marker, the
    // projection's import_review and the import_review_required blocker.
    const review = meta.import_review && typeof meta.import_review === 'object' && !Array.isArray(meta.import_review)
      ? meta.import_review as Record<string, unknown>
      : null
    const projectionGate = Boolean(meta.script_projection_response_gate_v1) || review?.source === 'script_projection'
    delete meta.script_projection_response_gate_v1
    if (projectionGate) {
      if (review?.source === 'script_projection') delete meta.import_review
      if (meta.execution_blocker === 'import_review_required') delete meta.execution_blocker
      if (Array.isArray(meta.execution_blockers)) {
        const filtered = meta.execution_blockers.filter(entry => entry !== 'import_review_required')
        if (filtered.length > 0) meta.execution_blockers = filtered
        else delete meta.execution_blockers
      }
    }
    // HAR evidence (har.request.query_string, har.response.cookies) carries the
    // masked values the disclosure projection put there.
    for (const key of Object.keys(meta)) {
      if (key.startsWith('har.')) meta[key] = scrubRedactedStringsInPlace(meta[key], new Set(), '')
    }
    if (Object.keys(meta).length === 0) {
      delete action.metadata
    }
  }
  const anyAction = action as unknown as Record<string, unknown>
  if (Array.isArray(action.headers)) {
    const rawHeaders = action.headers as Array<{ name?: string; value?: string }>
    ;(action as unknown as { headers?: unknown }).headers = rawHeaders.flatMap(header => {
      const value = withoutMaskedHeader(header?.name || '', header?.value || '', removals)
      return value === null ? [] : [value === header?.value ? header : { ...header, value }]
    })
  } else if (action.headers && typeof action.headers === 'object') {
    const record = action.headers as Record<string, string>
    for (const key of Object.keys(record)) {
      const value = withoutMaskedHeader(key, record[key] || '', removals)
      if (value === null) delete record[key]
      else record[key] = value
    }
  }
  if (Array.isArray(anyAction.header_list)) {
    const rawHeaderList = anyAction.header_list as Array<{ name?: string; value?: string }>
    const filteredHeaderList = rawHeaderList.flatMap(header => {
      const value = withoutMaskedHeader(header?.name || '', header?.value || '', removals)
      return value === null ? [] : [value === header?.value ? header : { ...header, value }]
    })
    if (filteredHeaderList.length > 0) {
      anyAction.header_list = filteredHeaderList
    } else {
      delete anyAction.header_list
    }
  }
  forEachNestedStep(action, child => stripClonedActionImportProvenance(child, removals))
  // Nested steps were scrubbed by their own pass, under their own names.
  const nested = new Set(['metadata', 'control', 'requests', 'request'])
  for (const key of Object.keys(anyAction)) {
    if (nested.has(key)) continue
    if (key === 'params' && anyAction.params && typeof anyAction.params === 'object' && !Array.isArray(anyAction.params)) {
      scrubRedactedStringsInPlace(anyAction.params, removals, 'params', nested)
      continue
    }
    anyAction[key] = scrubRedactedStringsInPlace(anyAction[key], removals, key)
  }
}

export interface DuplicatedActionList {
  actions: ScriptActionDefinition[]
  /**
   * The masked credential values the copy could not carry (`body`,
   * `header Authorization`, `cookie sid`), so the author can supply them: a
   * masked value has no stored value to back it in a new step.
   */
  removedMaskedValues: string[]
}

/**
 * Insert a deep copy of the action at `index` directly after it, with a unique
 * name. The clone is a full deep copy (all keys, incl. unknown/params.*, and
 * nested control steps) so editing it never mutates the original. Imported
 * single-action provenance IDs (`import_action_uid`) and redacted credential
 * preservation markers are stripped from the clone so saving never rejects the
 * copy as a duplicate provenance UID or unbacked `[REDACTED]` placeholder; the
 * values those placeholders stood for are reported, as the copy runs without them.
 */
export function duplicateActionInListWithReport(
  actions: ScriptActionDefinition[],
  index: number,
): DuplicatedActionList {
  const source = actions[index]
  if (!source) return { actions, removedMaskedValues: [] }
  const clone = JSON.parse(JSON.stringify(source)) as ScriptActionDefinition
  const removals: MaskedValueRemovals = new Set()
  stripClonedActionImportProvenance(clone, removals)
  clone.name = uniqueActionName(source.name || source.type || 'action', actions.map(a => a.name || ''))
  return {
    actions: [...actions.slice(0, index + 1), clone, ...actions.slice(index + 1)],
    removedMaskedValues: [...removals].sort(),
  }
}

export function duplicateActionInList(
  actions: ScriptActionDefinition[],
  index: number,
): ScriptActionDefinition[] {
  return duplicateActionInListWithReport(actions, index).actions
}

// --- Transactions ----------------------------------------------------------
// A transaction is a named, timed unit the engine aggregates (TPS, percentiles,
// pass/fail) and SLAs attach to. The engine groups actions by the
// `metadata.source_transaction` tag (the same key the recorder and importers
// emit), so "wrapping" an action in a transaction is just tagging it — no new
// DSL shape. The legacy `loadrunner_transaction` key is read for back-compat.

const TRANSACTION_META_KEY = 'source_transaction'

/** The transaction name an action is tagged with, or '' if none. */
export function actionTransactionName(action: ScriptActionDefinition): string {
  const meta = action.metadata
  const raw = meta?.[TRANSACTION_META_KEY] ?? meta?.loadrunner_transaction
  return typeof raw === 'string' ? raw.trim() : ''
}

/**
 * Set (or, with an empty name, clear) an action's transaction tag, preserving
 * every other metadata and action key verbatim (R1). Metadata is dropped
 * entirely when it would otherwise become empty, to avoid noise in the JSON.
 */
export function setActionTransactionName(
  action: ScriptActionDefinition,
  name: string,
): ScriptActionDefinition {
  const trimmed = name.trim()
  const meta: Record<string, unknown> = { ...(action.metadata || {}) }
  if (trimmed) {
    meta[TRANSACTION_META_KEY] = trimmed
  } else {
    delete meta[TRANSACTION_META_KEY]
    delete meta.loadrunner_transaction
  }
  const next: ScriptActionDefinition = { ...action }
  if (Object.keys(meta).length > 0) next.metadata = meta
  else delete next.metadata
  return next
}

/** Tag the actions at `indices` with one transaction name (used for group wrap). */
export function tagActionsTransaction(
  actions: ScriptActionDefinition[],
  indices: number[],
  name: string,
): ScriptActionDefinition[] {
  const selected = new Set(indices)
  return actions.map((action, i) => (selected.has(i) ? setActionTransactionName(action, name) : action))
}

/** A default transaction name not already in use ("Transaction 1", "Transaction 2", ...). */
export function uniqueTransactionName(actions: ScriptActionDefinition[]): string {
  const existing = new Set(actions.map(action => actionTransactionMarker(action)?.name || actionTransactionName(action)).filter(Boolean))
  let n = 1
  while (existing.has(`Transaction ${n}`)) n += 1
  return `Transaction ${n}`
}

// A marker names its transaction in the call's argument, as an imported
// LoadRunner script does, so renaming the step never unpairs it; the end takes
// LR_AUTO, so a failed step inside fails the transaction.
function transactionMarker(name: string, fn: 'lr_start_transaction' | 'lr_end_transaction'): ScriptActionDefinition {
  return fn === 'lr_start_transaction'
    ? { name: `Start ${name} transaction`, type: 'source_transaction', params: { function: fn, args: [name] } } as unknown as ScriptActionDefinition
    : { name: `End ${name} transaction`, type: 'source_transaction', params: { function: fn, args: [name, 'LR_AUTO'] } } as unknown as ScriptActionDefinition
}

/**
 * Wrap the contiguous span [startIndex..endIndex] in a balanced
 * lr_start_transaction / lr_end_transaction marker pair for transaction `name`.
 * Unlike tagging, this measures the wall-clock SPAN of the region at runtime —
 * including think time and any non-selected actions that fall between the
 * endpoints — which is the classic LoadRunner transaction semantic. Returns the
 * list unchanged for an empty name or an out-of-range range.
 */
export function wrapActionsInTransactionMarkers(
  actions: ScriptActionDefinition[],
  startIndex: number,
  endIndex: number,
  name: string,
): ScriptActionDefinition[] {
  const trimmed = name.trim()
  const lo = Math.min(startIndex, endIndex)
  const hi = Math.max(startIndex, endIndex)
  if (!trimmed || lo < 0 || hi >= actions.length || lo > hi) return actions
  return [
    ...actions.slice(0, lo),
    transactionMarker(trimmed, 'lr_start_transaction'),
    ...actions.slice(lo, hi + 1),
    transactionMarker(trimmed, 'lr_end_transaction'),
    ...actions.slice(hi + 1),
  ]
}

/**
 * If `action` is a transaction marker (a source_transaction start/end), return
 * its role and name; otherwise null. Used to render marker rows distinctly.
 * The name is the call's first argument, as LoadRunner pairs them: an import
 * names the step itself "Start_<name>_transaction" / "End_<name>_transaction".
 * `sub` marks lr_start_sub_transaction / lr_end_sub_transaction (and lr_end_sub_trans).
 */
export function actionTransactionMarker(
  action: ScriptActionDefinition,
): { role: 'start' | 'end'; name: string; sub?: true } | null {
  if ((action.type || '').toLowerCase() !== 'source_transaction') return null
  const params = action.params as Record<string, unknown> | undefined
  const fn = String(params?.function || '').toLowerCase()
  const args = Array.isArray(params?.args) ? params.args : []
  const name = (typeof args[0] === 'string' && args[0].trim()) || (action.name || '').trim()
  // lr_end_sub_trans is an alias the importer and engine accept.
  const sub = fn.includes('sub_trans') ? { sub: true as const } : {}
  if (fn.includes('start')) return { role: 'start', name, ...sub }
  // lr_stop_transaction pauses a transaction (lr_resume_transaction goes on); it does not end it.
  if (fn.includes('end')) return { role: 'end', name, ...sub }
  return null
}

/**
 * Remap an action index after a marker wrap of [lo..hi]: a start marker is
 * inserted at lo (so lo..hi shift +1) and an end marker after hi (so indices
 * past hi shift +2). Keeps debug breakpoints pinned to their actions.
 */
export function remapIndexAfterMarkerWrap(index: number, lo: number, hi: number): number {
  if (index < lo) return index
  if (index <= hi) return index + 1
  return index + 2
}

export interface TransactionBandCell {
  /** The transaction this row belongs to. */
  name: string
  /** First/last row of a contiguous run of the same transaction (for the caps). */
  first: boolean
  last: boolean
}

/**
 * Annotate a list of action rows with the transaction "band" each belongs to,
 * so the outline can bracket a transaction's rows with a left rail. Covers both
 * representations: a span marker pair (the lr_start row, the actions between,
 * and the lr_end row; sub-transaction markers stay inside it) and a contiguous
 * run of metadata-tagged rows. Bands pair
 * only within one phase. Returned map is keyed by `${phase}-${actionIndex}`.
 */
export function computeTransactionBands(rows: ScriptPhaseActionRow[]): Map<string, TransactionBandCell> {
  // Pass 1: the transaction name each row belongs to ('' = none).
  const bandName: string[] = new Array(rows.length).fill('')
  const openMarkers: string[] = []
  let phase: string | null = null
  rows.forEach((row, i) => {
    if (row.phase !== phase) { openMarkers.length = 0; phase = row.phase }
    // A sub-transaction is a step inside its parent's band, not a band of its own.
    const found = actionTransactionMarker(row.action)
    const marker = found?.sub ? null : found
    if (marker?.role === 'start') {
      bandName[i] = marker.name
      openMarkers.push(marker.name)
      return
    }
    if (marker?.role === 'end') {
      bandName[i] = marker.name
      const at = openMarkers.lastIndexOf(marker.name)
      if (at >= 0) openMarkers.splice(at, 1)
      else openMarkers.pop()
      return
    }
    if (openMarkers.length) { bandName[i] = openMarkers[openMarkers.length - 1]; return }
    bandName[i] = actionTransactionName(row.action)
  })
  // Pass 2: caps at the edges of contiguous same-name runs (same phase).
  const out = new Map<string, TransactionBandCell>()
  rows.forEach((row, i) => {
    const name = bandName[i]
    if (!name) return
    const prevSame = i > 0 && rows[i - 1].phase === row.phase && bandName[i - 1] === name
    const nextSame = i < rows.length - 1 && rows[i + 1].phase === row.phase && bandName[i + 1] === name
    out.set(`${row.phase}-${row.actionIndex}`, { name, first: !prevSame, last: !nextSame })
  })
  return out
}

/**
 * Filter outline action rows by a free-text query, matching an action's name,
 * type, target, or phase label (case-insensitive). An empty/whitespace query
 * returns the rows unchanged. Rows keep their original actionIndex/phase so the
 * outline's select/move/breakpoint handlers stay correct over a filtered view.
 */
export function filterScriptPhaseActionRows(
  rows: ScriptPhaseActionRow[],
  query: string,
): ScriptPhaseActionRow[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return rows
  return rows.filter(({ action, phaseLabel }) =>
    (action.name || '').toLowerCase().includes(needle) ||
    (action.type || '').toLowerCase().includes(needle) ||
    (action.target || '').toLowerCase().includes(needle) ||
    phaseLabel.toLowerCase().includes(needle),
  )
}

export function formatProtocolEvidenceSummary(evidence?: ProtocolEvidence): string {
  if (!evidence) return ''
  const parts = [
    evidence.protocol,
    evidence.operation,
    evidence.status,
    evidence.target,
  ].filter((value): value is string => typeof value === 'string' && value.trim().length > 0)
  return parts.join(' / ')
}

export function lookupBrowserDeviceProfile(name?: string) {
  return BROWSER_DEVICE_PROFILES.find(profile => profile.name === name)
}

export function formatBrowserDeviceProfileLabel(profile: BrowserDeviceProfileOption) {
  return profile.engine === 'webkit' ? `${profile.name} (WebKit)` : profile.name
}
