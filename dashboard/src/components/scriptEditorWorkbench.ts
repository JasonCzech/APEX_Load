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
