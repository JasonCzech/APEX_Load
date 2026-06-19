// scriptEditorWorkbench — pure workbench logic extracted from ScriptEditor.tsx
// (behavior-preserving). These helpers have no React/DOM dependencies and are
// unit-tested in scriptEditorWorkbench.test.ts.
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { ProtocolEvidence } from '../types'
import { escapeRegExp } from './scriptEditorHelpers'
import type { ScriptCodeMatchRange } from './scriptCodeDecorations'

export type ScriptActionPhase = 'init' | 'action' | 'teardown'

export type ScriptPhaseActionRow = {
  phase: ScriptActionPhase
  phaseLabel: string
  actionIndex: number
  globalIndex: number
  action: ScriptActionDefinition
}

export type JsonDraftParseResult =
  | { ok: true; script: ScriptDefinition }
  | { ok: false; message: string }

export type DebuggerResizeKind = 'rail' | 'vars' | 'bottom'

export type CenterWorkbenchTabId =
  | 'script-json'
  | 'runtime-settings'
  | 'imported-metadata'
  | 'metadata-json'
  | 'parameter-data'
  | 'browser-replay'
  | 'object-descriptors'
  | 'http-conversion'

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

export const SCRIPT_JSON_TAB_ID: CenterWorkbenchTabId = 'script-json'
export const CENTER_TABS_STORAGE_PREFIX = 'scriptEditorCenterTabs'
export const CENTER_WORKBENCH_TAB_IDS: readonly CenterWorkbenchTabId[] = [
  'script-json',
  'runtime-settings',
  'imported-metadata',
  'metadata-json',
  'parameter-data',
  'browser-replay',
  'object-descriptors',
  'http-conversion',
]

export const DEBUGGER_LAYOUT_DEFAULTS: Record<DebuggerResizeKind, number> = {
  rail: 260,
  vars: 320,
  bottom: 220,
}

export const DEBUGGER_LAYOUT_LIMITS: Record<DebuggerResizeKind, { max: number; min: number; step: number }> = {
  rail: { min: 180, max: 560, step: 24 },
  vars: { min: 220, max: 600, step: 24 },
  bottom: { min: 140, max: 640, step: 24 },
}

export function isCenterWorkbenchTabId(value: unknown): value is CenterWorkbenchTabId {
  return typeof value === 'string' && CENTER_WORKBENCH_TAB_IDS.includes(value as CenterWorkbenchTabId)
}

function normalizeCenterWorkbenchTabId(value: unknown): CenterWorkbenchTabId | null {
  if (value === 'configuration') return 'runtime-settings'
  return isCenterWorkbenchTabId(value) ? value : null
}

export function defaultCenterTabsState(): CenterTabsState {
  return {
    tabs: [SCRIPT_JSON_TAB_ID, 'runtime-settings'],
    activeTabId: SCRIPT_JSON_TAB_ID,
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
    const normalizedTabs = [SCRIPT_JSON_TAB_ID, ...storedTabs.filter((tab, index, tabs) => (
      tab !== SCRIPT_JSON_TAB_ID && tabs.indexOf(tab) === index
    ))]
    const normalizedActiveTabId = normalizeCenterWorkbenchTabId(parsed.activeTabId)
    const activeTabId = normalizedActiveTabId && normalizedTabs.includes(normalizedActiveTabId)
      ? normalizedActiveTabId
      : SCRIPT_JSON_TAB_ID
    return {
      tabs: normalizedTabs,
      activeTabId,
    }
  } catch {
    return defaultCenterTabsState()
  }
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

export function clampDebuggerPaneSize(kind: DebuggerResizeKind, value: number) {
  const limits = DEBUGGER_LAYOUT_LIMITS[kind]
  return Math.min(limits.max, Math.max(limits.min, Math.round(value)))
}

export function formatScriptJson(script: ScriptDefinition) {
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
    return { ok: false, message: error instanceof Error ? `Script JSON is invalid: ${error.message}` : 'Script JSON is invalid.' }
  }
}

export function codePathForDebugRow(row?: { phase: string; actionIndex: number } | null) {
  if (!row) return ''
  if (row.phase === 'action') return `script.actions[${row.actionIndex}]`
  if (row.phase === 'init') return `script.init[${row.actionIndex}]`
  if (row.phase === 'teardown') return `script.teardown[${row.actionIndex}]`
  return ''
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

/**
 * Insert a deep copy of the action at `index` directly after it, with a unique
 * name. The clone is a full deep copy (all keys, incl. unknown/params.*, and
 * nested control steps) so editing it never mutates the original.
 */
export function duplicateActionInList(
  actions: ScriptActionDefinition[],
  index: number,
): ScriptActionDefinition[] {
  const source = actions[index]
  if (!source) return actions
  const clone = JSON.parse(JSON.stringify(source)) as ScriptActionDefinition
  clone.name = uniqueActionName(source.name || source.type || 'action', actions.map(a => a.name || ''))
  return [...actions.slice(0, index + 1), clone, ...actions.slice(index + 1)]
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
  const existing = new Set(actions.map(actionTransactionName).filter(Boolean))
  let n = 1
  while (existing.has(`Transaction ${n}`)) n += 1
  return `Transaction ${n}`
}

function transactionMarker(name: string, fn: 'lr_start_transaction' | 'lr_end_transaction'): ScriptActionDefinition {
  return { name, type: 'source_transaction', params: { function: fn } } as unknown as ScriptActionDefinition
}

/**
 * Wrap the contiguous span [startIndex..endIndex] in a balanced
 * lr_start_transaction / lr_end_transaction marker pair (both named `name`).
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
 */
export function actionTransactionMarker(
  action: ScriptActionDefinition,
): { role: 'start' | 'end'; name: string } | null {
  if ((action.type || '').toLowerCase() !== 'source_transaction') return null
  const fn = String((action.params as Record<string, unknown> | undefined)?.function || '').toLowerCase()
  const name = (action.name || '').trim()
  if (fn.includes('start')) return { role: 'start', name }
  if (fn.includes('end') || fn.includes('stop')) return { role: 'end', name }
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
 * and the lr_end row) and a contiguous run of metadata-tagged rows. Bands pair
 * only within one phase. Returned map is keyed by `${phase}-${actionIndex}`.
 */
export function computeTransactionBands(rows: ScriptPhaseActionRow[]): Map<string, TransactionBandCell> {
  // Pass 1: the transaction name each row belongs to ('' = none).
  const bandName: string[] = new Array(rows.length).fill('')
  const openMarkers: string[] = []
  let phase: string | null = null
  rows.forEach((row, i) => {
    if (row.phase !== phase) { openMarkers.length = 0; phase = row.phase }
    const marker = actionTransactionMarker(row.action)
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
