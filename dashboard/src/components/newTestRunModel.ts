import { countLabel as formatCount } from '../utils/numberFormat'
import { GeneratorAssignmentSettings, RuntimeSettingsOverride, StoredScenario, SystemInventoryGenerator, TrafficProfile } from '../types'
import { type PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import { type ScriptConfig } from '../types/scripts'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { normalizeRequiredLabels } from './assignmentLabels'
import {
  applyRuntimeOverrideDiff,
  effectiveRuntimeSection,
  effectiveRuntimeValue,
  resolveRuntimeRowsValue,
  isFieldOverridden,
  isPartialRuntimeDraft,
  normalizeRuntimeOverrideFieldPath,
  overrideFieldValue,
  partialRuntimeOverrideFromDraft,
  resetOverrideField,
  runtimeDraftFromOverride,
  runtimeOverrideFieldPaths,
  runtimeRowCoversPath,
  runtimeSectionSource,
  sameRuntimeValue,
  setOverrideField,
  upgradeRuntimeDraftToPartial,
} from './runtimeSettingsModel'
import { canonicalThinkTimeType } from './thinkTimeConfig'
import { formatMillisecondsAsSecondsValue, formatPacingTiming } from './runtimeTiming'
import { trafficProfileMappingForGroup, trafficProfileTargetAt, trafficProfileTimelineSeconds, trafficProfileTpsStatistics } from './trafficProfilePreviewModel'
import { initialStageLevel, maxStageVUsers, stageBoundarySeconds, stageLevelAt, stageRampStepSeconds } from './workloadStages'
import { engineLevelAtDeadline, engineRampDownLevel, engineRampDownSeconds, engineRampUpLevel, engineRampUpSeconds } from './loadPlanSteps'

export type RunDefaultKey =
  | 'duration'
  | 'iterations'
  | 'log_level'
  | 'auto_transactions'
  | 'pacing'
  | 'think_time'
  | 'continue_on_error'
  | 'snapshot_on_error'
  | 'script_shakeout'
  | 'resource_baseline'

export type RunRuntimeOverrideDraft = {
  iterations?: number | null
  pacing?: RuntimeSettingsOverride['pacing'] | null
  think_time?: RuntimeSettingsOverride['think_time'] | null
  log_level?: string | null
  auto_transactions?: string | null
  continue_on_error?: boolean | null
  snapshot_on_error?: boolean | null
}

export type RunDefaultOverrides = {
  duration?: string
  script_shakeout?: boolean
  resource_baseline?: boolean
  runtime_settings: RunRuntimeOverrideDraft
}

export type RunDefaultCard = {
  key: RunDefaultKey
  label: string
  value: string
  scenarioValue: string
  isDefault: boolean
  isOverridden: boolean
}

export type RunDefaultEditDraft = {
  value: string
  pacingType: string
  pacingAmount: string
  pacingConfig?: RuntimeSettingsOverride['pacing']
  thinkIgnore: boolean
  thinkTimeConfig?: RuntimeSettingsOverride['think_time']
}

export type ScenarioScriptGroup = StoredScenario['scripts'][number]

export type GroupRunOverride = {
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  initialization?: 'at_runtime' | 'at_once'
  runtime_settings?: RuntimeSettingsOverride
  assignment?: GeneratorAssignmentSettings
}

export type GroupEditDraft = {
  vusers: string
  ramp_up: string
  ramp_down: string
  initialization: 'at_runtime' | 'at_once'
  iterations: string
  log_level: string
  auto_transactions: string
  pacingType: string
  pacingAmount: string
  pacingConfig?: RuntimeSettingsOverride['pacing']
  thinkTimeMs: string
  thinkIgnore: boolean
  thinkTimeConfig?: RuntimeSettingsOverride['think_time']
  continue_on_error: string
  snapshot_on_error: string
  browser_device_profile: string
  browser_headless: string
  browser_touch_mode: string
  browser_orientation: string
  assignmentMode: 'auto' | 'specific_hosts' | 'required_labels' | 'region_weighted' | 'load_zone_weighted'
  selectedTargets: string[]
  requiredLabels: Record<string, string>
  regionWeights: Record<string, number>
  loadZoneWeights: Record<string, number>
  tolerateGeneratorLoss?: boolean
}

export type RunLoadProfilePreview = {
  points: Array<Record<string, number>>
  showVUsers: boolean
  totalSeconds: number
  steadySeconds: number
  preBaselineSeconds: number
  postBaselineSeconds: number
  peakVUsers: number
  peakTps?: number
  minTps?: number
  averageTps?: number
  estimatedPeakVUsers?: number
  recommendedGenerators?: number
  runtimeTarget?: 'tps' | 'vusers'
  tpsGroupIndexes?: number[]
}

const RESOURCE_BASELINE_SECONDS = 5 * 60

export const GLOBAL_RUN_DEFAULT_KEYS = new Set<RunDefaultKey>(['duration', 'script_shakeout', 'resource_baseline'])

export function buildEmptyRunDefaultOverrides(): RunDefaultOverrides {
  return { runtime_settings: {} }
}

export function hasOwnKey(object: object, key: PropertyKey): boolean {
  return Object.prototype.hasOwnProperty.call(object, key)
}

/**
 * Every runtime section the New Test run editors can CLEAR back to "inherit" — the single
 * source of truth for the tri-state (unset / pinned / cleared).
 *
 * Both halves of the tri-state consume this list, so a section cannot be clearable in the UI
 * and un-clearable in the model by construction:
 *   * the WRITE path (stampClearedRuntimeSections) turns a removed section into the null
 *     tombstone mergeRuntimeSettingsForRun deletes on, because the run override is overlaid
 *     back onto the group's STORED runtime_settings and an absent key can only mean
 *     "unedited";
 *   * the READ path (effectiveGroupRuntimeSettings) refuses to read a tombstoned section back
 *     off the stored group, so the dialog and the table show what the run will execute.
 * `browser` and `browser_config` are the two wire shapes of ONE logical section; the dialog's
 * four browser fields write whichever shape the level already carries, and setRuntimeBrowser
 * REMOVES either shape once the clear empties it — that removal is what makes the tombstone below
 * reachable for both. A section left standing as `{}` is not a clear: for browser_config it is a
 * whole-section replace, which is the only way to override an ancestor's pinned section.
 */
export const CLEARABLE_RUNTIME_SECTIONS = ['pacing', 'think_time', 'error_handling', 'browser', 'browser_config'] as const

export type ClearableRuntimeSection = (typeof CLEARABLE_RUNTIME_SECTIONS)[number]

// Compile-time guard: a clearable section must name a real RuntimeSettingsOverride field, so a
// renamed or removed section breaks the build instead of silently never being cleared.
const clearableRuntimeSectionKeyCheck: ReadonlyArray<keyof RuntimeSettingsOverride> = CLEARABLE_RUNTIME_SECTIONS
void clearableRuntimeSectionKeyCheck

/**
 * Stamp the null tombstone for every clearable section the edit removed from `patch` while the
 * stored group still carries it. Callers must run this AFTER every section edit has been
 * applied, and must pass the group's STORED runtime settings (never the merged draft) — the
 * merge that produced `patch` already consumed any earlier tombstone, so a second save would
 * otherwise forget the clear and restore the stored section.
 */
export function stampClearedRuntimeSections(patch: RuntimeSettingsOverride, stored?: RuntimeSettingsOverride): RuntimeSettingsOverride {
  const target = patch as Record<string, unknown>
  for (const section of CLEARABLE_RUNTIME_SECTIONS) {
    if (!target[section] && hasOwnKey(stored || {}, section)) target[section] = null
  }
  return patch
}

export function groupOverrideKey(group: ScenarioScriptGroup) {
  return `${group.order}:${group.script_id}`
}

/**
 * True when a runtime SECTION (or the settings envelope itself) still carries something the run
 * would apply. `false` and `0` are values; `undefined`, `null` and `''` are not — so a section
 * left holding nothing but cleared fields is value-less and must not be transmitted.
 */
function runtimeSectionHasValues(section?: Record<string, unknown> | null) {
  if (!section) return false
  return Object.values(section).some(value => value !== undefined && value !== null && value !== '')
}

export function hasRuntimeSettingsValues(settings?: RuntimeSettingsOverride) {
  return runtimeSectionHasValues(settings as Record<string, unknown> | undefined)
}

/**
 * True when a run-only patch says anything at all: it either sets a value or clears one with a
 * null tombstone. A tombstone-only patch carries no *values*, so hasRuntimeSettingsValues rejects
 * it, but discarding it would throw the clear away and silently re-apply the stored section.
 */
export function hasRuntimeSettingsInstructions(settings?: RuntimeSettingsOverride) {
  if (!settings) return false
  return hasRuntimeSettingsValues(settings) || Object.values(settings).some(value => value === null)
}

export function hasGroupAssignmentValues(assignment?: GeneratorAssignmentSettings) {
  if (!assignment) return false
  if (assignment.mode === 'specific_hosts') return Boolean(assignment.selected_targets?.length)
  if (assignment.mode === 'region_weighted') return Boolean(Object.keys(assignment.region_weights || {}).length)
  if (assignment.mode === 'load_zone_weighted') return Boolean(Object.keys(assignment.load_zone_weights || {}).length)
  return assignment.mode === 'auto' || Boolean(normalizeRequiredLabels(assignment.required_labels))
}

export function hasGroupOverrideValues(override?: GroupRunOverride) {
  if (!override) return false
  return hasOwnKey(override, 'vusers')
    || hasOwnKey(override, 'ramp_up')
    || hasOwnKey(override, 'ramp_down')
    || hasOwnKey(override, 'initialization')
    || hasRuntimeSettingsInstructions(override.runtime_settings)
    || hasGroupAssignmentValues(override.assignment)
}

export function groupOverrideCount(overrides: Record<string, GroupRunOverride>) {
  return Object.values(overrides).filter(hasGroupOverrideValues).length
}

/**
 * A section partial rows set only some leaves of, over a script section this page never loads:
 * without a listed type the timing is the script's own, adjusted by the listed leaves.
 */
function formatPartialTiming(prefix: string, section?: object | null) {
  const leaves = Object.entries(section || {})
    .filter(([, value]) => value !== undefined && value !== null && value !== '')
    .map(([key, value]) => `${key.replace(/_/g, ' ')} ${String(value)}`)
  return leaves.length ? `${prefix} · ${leaves.join(', ')}` : prefix
}

function formatRuntimePacingConfig(pacing?: RuntimeSettingsOverride['pacing'] | null, partial = false) {
  if (partial && !pacing?.type) return formatPartialTiming('Script pacing', pacing)
  return formatPacingTiming(pacing) || 'Script default'
}

function formatRuntimePacingTableValue(pacing?: RuntimeSettingsOverride['pacing'] | null, partial = false) {
  if (partial && !pacing?.type) return formatPartialTiming('Script timing', pacing)
  if (!pacing?.type) return 'Script timing'
  return formatRuntimePacingConfig(pacing)
}

function formatRuntimeThinkTimeConfig(thinkTime?: RuntimeSettingsOverride['think_time'] | null, partial = false) {
  if (!thinkTime) return 'Script default'
  if (thinkTime.ignore) return 'Disabled'
  if (partial && !thinkTime.type) return formatPartialTiming('Script think time', thinkTime)
  if (thinkTime.type === 'recorded') return 'Replay recorded think time'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

function formatRuntimeThinkTimeTableValue(thinkTime?: RuntimeSettingsOverride['think_time'] | null, partial = false) {
  if (!thinkTime) return 'Script think time'
  return formatRuntimeThinkTimeConfig(thinkTime, partial)
}

function formatRuntimeBooleanValue(value?: boolean | null) {
  if (value === undefined || value === null) return 'Script default'
  return value ? 'Enabled' : 'Disabled'
}

function formatEnabledValue(value?: boolean) {
  return value ? 'Enabled' : 'Disabled'
}

function formatResourceBaselineValue(value?: boolean) {
  return value ? '5m before and after' : 'Disabled'
}

function formatRuntimeText(value: string | undefined | null, fallback: string) {
  if (!value) return fallback
  const formatted = value.replace(/_/g, ' ')
  return formatted.charAt(0).toUpperCase() + formatted.slice(1)
}

// error_handling is applied as a whole section, twice over:
//   * mergeRuntimeSettings (pkg/api/runtime_overrides.go) swaps the root section for
//     the group's outright whenever the group carries one — it never merges fields;
//   * applyRuntimeOverrides then assigns script.Config.ErrorHandling = *overrides.ErrorHandling,
//     and dsl.ErrorHandlingConfig stores plain bools, so every key the winning section
//     omits executes as false rather than as the script's own value.
// Two consequences this file has to honour everywhere it reports error handling:
//   1. ANY section a level transmits — a retry-only one that names neither boolean, or the
//      literally EMPTY `{}` the API returns for an all-false section, since every field is
//      omitempty behind a pointer — pins BOTH halves, driving the unnamed half to false.
//      'Script default' is therefore keyed off section PRESENCE (errorHandlingSectionIsPinned),
//      never off the two booleans and never off whether the section has content: only when
//      nothing in the group-overlay -> scenario/run-default chain transmits a section does the
//      run inherit the script's own config.error_handling, which this page never loads.
//   2. The chain is resolved SECTION-wise, not field-wise. The first level that transmits a
//      section wins entirely; lower levels contribute nothing, not even a half the winner omits.
function formatRuntimeErrorHandlingTableValue(continueOnError?: boolean, snapshotOnError?: boolean, sectionPinned = false) {
  if (!sectionPinned && continueOnError === undefined && snapshotOnError === undefined) return 'Script default'
  // A half no layer lists (partial rows resolve per path) inherits the script's own setting.
  const continueLabel = continueOnError === undefined ? 'Script default' : continueOnError ? 'Continue' : 'Stop'
  const snapshotLabel = snapshotOnError === undefined ? 'Script default snapshot' : snapshotOnError ? 'Snapshot' : 'No snapshot'
  return `${continueLabel} / ${snapshotLabel}`
}

/**
 * True when a LEVEL transmits an error_handling section at all — presence, not content.
 *
 * Every dsl.ErrorHandlingConfig field is `omitempty` and RuntimeSettingsOverride.ErrorHandling
 * is a pointer, so a section whose only content is continue_on_error:false and/or
 * snapshot_on_error:false round-trips through the API as a NON-NIL `{}`. The backend still
 * applies it whole (mergeRuntimeSettings then `script.Config.ErrorHandling = *overrides
 * .ErrorHandling`), wiping the script's own values, so `{}` is a pinned section and reading it
 * as "no section" is the misreport this predicate exists to prevent. `null` is the run-only
 * tombstone and means the level transmits nothing.
 *
 * Use errorHandlingSectionHasValues instead for a section SYNTHESISED locally, where `{}`
 * really does mean "nothing was set" because it never reaches the wire.
 */
export function errorHandlingSectionIsPinned(errorHandling?: RuntimeSettingsOverride['error_handling']) {
  return errorHandling !== undefined && errorHandling !== null
}

/** True when a locally synthesised error_handling section carries any value, retries included. */
export function errorHandlingSectionHasValues(errorHandling?: RuntimeSettingsOverride['error_handling']) {
  if (!errorHandling) return false
  return Object.values(errorHandling).some(value => value !== undefined && value !== null && value !== '')
}

/**
 * True when a section would still be transmitted after both booleans are removed, i.e. the
 * group dialog's inherit option CANNOT release it. The dialog edits only continue_on_error and
 * snapshot_on_error, so retry fields it cannot reach keep the section — and the section is
 * applied whole, so both booleans keep executing as false. Callers must not offer an inherit
 * option that promises a script/scenario default this leaves unreachable.
 */
export function errorHandlingSurvivesBooleanClear(errorHandling?: RuntimeSettingsOverride['error_handling']) {
  return errorHandlingSectionHasValues(errorHandlingResidual(errorHandling))
}

/** The part of a group's error_handling section the dialog cannot edit — the retry policy. */
function errorHandlingResidual(errorHandling?: RuntimeSettingsOverride['error_handling']) {
  if (!errorHandlingSectionIsPinned(errorHandling)) return {}
  const residual = { ...errorHandling }
  delete residual.continue_on_error
  delete residual.snapshot_on_error
  return residual
}

export type GroupErrorHandlingDraftHalves = { continue_on_error: string; snapshot_on_error: string }

export type ErrorHandlingHalf = keyof GroupErrorHandlingDraftHalves

/**
 * Apply one edit to the group dialog's two error-handling selects.
 *
 * The inherit option ('') is a SECTION-level statement, not a per-field one: error_handling is
 * applied whole (mergeRuntimeSettings swaps the section outright, then
 * `script.Config.ErrorHandling = *overrides.ErrorHandling`), so a group can only stop pinning ONE
 * half by transmitting no section at all. Selecting it on either select therefore moves BOTH to
 * inherit — otherwise the surviving half keeps the section alive and the half just released
 * executes as false, which is the opposite of the "Scenario default"/"Script default" the option
 * names. Picking an explicit value leaves the sibling alone: a section that omits a half executes
 * it as false, exactly as the dialog already displays it.
 */
export function applyGroupErrorHandlingDraftEdit(
  draft: GroupErrorHandlingDraftHalves,
  half: ErrorHandlingHalf,
  value: string,
): GroupErrorHandlingDraftHalves {
  if (value === '') return { continue_on_error: '', snapshot_on_error: '' }
  return { ...draft, [half]: value }
}

/**
 * Build the error_handling section a group transmits for this run from the dialog's two halves.
 *
 * One function owns the whole-section rule, because every cell of the truth table turns on it:
 *   * BOTH halves on inherit and no retry policy left -> the group transmits NOTHING (undefined),
 *     so the level above it wins intact. Seeding the run level's section back in would pin it and
 *     drive the halves the winner omits to false. A retry policy the dialog cannot edit keeps the
 *     section on the wire, so there the halves are resolved rather than dropped.
 *   * a half on inherit while the OTHER half still pins the section -> the section is transmitted
 *     regardless, so an omitted half would execute false rather than inherit. Materialise the
 *     value the option names when an ancestor pins a section; when nothing above the group pins
 *     one there is no value to materialise (this page never loads the script's own
 *     config.error_handling) and the half is left out, executing false exactly as before. That
 *     cell is unreachable from the dialog because applyGroupErrorHandlingDraftEdit turns the
 *     inherit option into a section-level clear.
 *   * an untouched half keeps whatever the winning section already says, so the value saved stays
 *     the value the dialog displayed.
 */
export function buildGroupErrorHandlingSectionForRun(input: {
  groupSection?: RuntimeSettingsOverride['error_handling']
  inheritedSection?: RuntimeSettingsOverride['error_handling']
  draft: GroupErrorHandlingDraftHalves
  changed: { continue_on_error: boolean; snapshot_on_error: boolean }
}): RuntimeSettingsOverride['error_handling'] | undefined {
  const { groupSection, inheritedSection, draft, changed } = input
  const residual = errorHandlingResidual(groupSection)
  const releasing = draft.continue_on_error === '' && draft.snapshot_on_error === ''
  // The only shape a release can take: no section at all. A retry policy the dialog cannot edit
  // keeps the section on the wire, so the halves below still have to be resolved rather than
  // dropped (the dialog disables the inherit option there, so that cell is UI-unreachable).
  if (releasing && !errorHandlingSectionHasValues(residual)) return undefined
  const section: RuntimeSettingsOverride['error_handling'] = releasing
    ? { ...residual }
    : errorHandlingSectionIsPinned(groupSection)
      ? { ...groupSection }
      : { ...(inheritedSection || {}) }
  for (const half of ['continue_on_error', 'snapshot_on_error'] as const) {
    if (!releasing && !changed[half]) continue
    if (draft[half] !== '') {
      section[half] = draft[half] === 'true'
    } else if (errorHandlingSectionIsPinned(inheritedSection)) {
      section[half] = Boolean(inheritedSection?.[half])
    } else {
      delete section[half]
    }
  }
  return Object.keys(section).length > 0 ? section : undefined
}

type ResolvedErrorHandling = {
  continue_on_error: boolean | undefined
  snapshot_on_error: boolean | undefined
  error_handling_pinned: boolean
}

/**
 * The row the run level transmits: the run-default edits laid over the scenario row exactly as
 * buildRuntimeSettingsForRun submits them, or the stored scenario row when the run changes nothing.
 */
export function runLevelRuntimeSettings(scenario: StoredScenario, overrides: RunDefaultOverrides): RuntimeSettingsOverride | undefined {
  return buildRuntimeSettingsForRun(scenario, overrides) ?? scenario.runtime_settings
}

/** The value a row stack supplies at `path` (highest layer first), or listed=false when no
 * layer lists it and the script's own config applies. A listed path with no value is an
 * explicit empty (a v1 section that omits the field, or a partial clear). */
function runtimeLayerValue(path: string, group?: RuntimeSettingsOverride, root?: RuntimeSettingsOverride): { listed: boolean; value: unknown } {
  const { index, value } = resolveRuntimeRowsValue(path, [group, root])
  return { listed: index >= 0, value }
}

/**
 * The section a row stack (highest layer first) displays: each partial row contributes only the
 * paths it lists, laid over the layers below it, and a row listing the whole section (every v1
 * row that carries it) replaces them. This and runtimeLayerValue are the only ways the display
 * and baseline code in this file reads an override row (newTestRuntimeReadGuard.test.ts).
 */
function runtimeLayerSection<K extends 'pacing' | 'think_time'>(section: K, group?: RuntimeSettingsOverride, root?: RuntimeSettingsOverride): RuntimeSettingsOverride[K] | undefined {
  return effectiveRuntimeSection<RuntimeSettingsOverride[K]>(section, [group, root]) ?? undefined
}

/** True when rows set leaves inside `section` but none lists it whole, so the rest is the script's. */
function runtimeLayerSectionIsPartial(section: string, group?: RuntimeSettingsOverride, root?: RuntimeSettingsOverride): boolean {
  if (!runtimeSectionSource(section, [group, root])) return false
  return !runtimeRowCoversPath(group, section) && !runtimeRowCoversPath(root, section)
}

/**
 * Resolve the two error-handling booleans through the group row and the run-level row the
 * way the backend does. Each half is resolved per path: a partial row supplies only the halves
 * it lists, and an unlisted half falls through to the next layer (undefined = the script's own
 * config.error_handling, which this page never loads). A v1 row lists its error_handling
 * section whole, so a half it omits resolves to false (whole-section v1 semantics) and lower
 * layers contribute nothing.
 */
function resolveErrorHandlingLayers(group?: RuntimeSettingsOverride, root?: RuntimeSettingsOverride): ResolvedErrorHandling {
  const resolved: ResolvedErrorHandling = { continue_on_error: undefined, snapshot_on_error: undefined, error_handling_pinned: false }
  for (const half of ['continue_on_error', 'snapshot_on_error'] as const) {
    const { listed, value } = runtimeLayerValue(`error_handling.${half}`, group, root)
    if (!listed) continue
    resolved[half] = Boolean(value)
    resolved.error_handling_pinned = true
  }
  return resolved
}

const BROWSER_TABLE_FIELDS = ['headless', 'device_profile', 'touch_mode', 'orientation'] as const

/** The four browser fields the run editors expose, resolved per path through the layers. */
function resolveBrowserLayers(group?: RuntimeSettingsOverride, root?: RuntimeSettingsOverride): RuntimeSettingsOverride['browser'] | undefined {
  const browser: Record<string, unknown> = {}
  let any = false
  for (const field of BROWSER_TABLE_FIELDS) {
    const { listed, value } = runtimeLayerValue(`browser.${field}`, group, root)
    if (!listed) continue
    any = true
    if (value !== undefined && value !== null && value !== '') browser[field] = value
  }
  return any ? browser as RuntimeSettingsOverride['browser'] : undefined
}

function formatRuntimeBrowserTableValue(browser?: RuntimeSettingsOverride['browser']) {
  if (!browser) return 'Standard'
  const parts = [
    browser.device_profile,
    browser.orientation ? browser.orientation.charAt(0).toUpperCase() + browser.orientation.slice(1) : '',
    browser.headless === undefined ? '' : browser.headless ? 'Headless' : 'Headed',
    browser.touch_mode === undefined ? '' : browser.touch_mode ? 'Touch' : 'No touch',
  ].filter(Boolean)
  return parts.length ? parts.join(' · ') : 'Standard'
}


function hasActivePlacementAssignment(assignment?: GeneratorAssignmentSettings) {
  if (assignment?.mode === 'specific_hosts') return Boolean(assignment.selected_targets?.length)
  if (assignment?.mode === 'region_weighted') return Boolean(Object.keys(assignment.region_weights || {}).length)
  if (assignment?.mode === 'load_zone_weighted') return Boolean(Object.keys(assignment.load_zone_weights || {}).length)
  return Boolean(normalizeRequiredLabels(assignment?.required_labels))
}

/** Deep-clone the JSON runtime contract so run-only edits never alias scenario state. */
export function cloneRuntimeSettingsForRun(settings?: RuntimeSettingsOverride): RuntimeSettingsOverride | undefined {
  return settings ? JSON.parse(JSON.stringify(settings)) as RuntimeSettingsOverride : undefined
}

/**
 * Overlay a run-only patch without discarding unedited advanced runtime sections.
 *
 * A `null` in the patch is a TOMBSTONE and REMOVES the key: assignment alone cannot express a
 * clear, because an absent key has to keep meaning "unedited" for every section the dialog did
 * not touch. cloneRuntimeSettingsForRun is a JSON round-trip, so `undefined` cannot serve as the
 * tombstone — it does not survive the clone. The tombstone is stripped here and never reaches
 * the API payload.
 */
export function mergeRuntimeSettingsForRun(base?: RuntimeSettingsOverride, overlay?: RuntimeSettingsOverride): RuntimeSettingsOverride | undefined {
  if (!base && !overlay) return undefined
  if (isPartialRuntimeDraft(base)) return mergePartialRuntimeSettingsForRun(base as PartialRuntimeSettingsOverride, overlay)
  // A partial overlay over a v1 (or absent) row: the row is upgraded to the partial row that
  // applies exactly what it did (every v1 section listed whole), so the overlay's paths can be
  // listed beside it. A v1 merge would assign the overlay's projected sections as whole sections.
  if (isPartialRuntimeDraft(overlay)) {
    const upgraded = partialRuntimeOverrideFromDraft(upgradeRuntimeDraftToPartial(runtimeDraftFromOverride(cloneRuntimeSettingsForRun(base))))
    return mergePartialRuntimeSettingsForRun({ ...(upgraded || {}), override_fields: upgraded?.override_fields || [] }, overlay, base)
  }
  const merged = cloneRuntimeSettingsForRun(base) || {}
  const patch = cloneRuntimeSettingsForRun(overlay)
  if (patch) {
    for (const [key, value] of Object.entries(patch)) {
      if (value === null) delete (merged as Record<string, unknown>)[key]
      else if (value !== undefined) (merged as Record<string, unknown>)[key] = value
    }
  }
  return hasRuntimeSettingsValues(merged) ? merged : undefined
}

/** Run editors replace these sections whole, so a run edit lists the section rather than mixing
 * its leaves with a lower layer's (a run pacing of one type over another type's timings). */
const WHOLE_RUN_SECTIONS = new Set(['pacing', 'think_time'])
const COMPACT_RUNTIME_ALIASES = new Set(['iterations', 'log_level', 'auto_transactions', 'browser'])
const COMPACT_BROWSER_FIELDS = ['headless', 'device_profile', 'touch_mode', 'orientation'] as const

/**
 * The partial-row counterpart of mergeRuntimeSettingsForRun. The backend applies only the paths
 * a partial row lists (pkg/api/runtime_overrides.go), so assigning a top-level key beside the
 * base row's override_fields would be ignored by the run. Each overlay key is applied through
 * the runtime settings model instead: a null tombstone resets the key's paths to inherited,
 * pacing and think_time are set as whole sections, and every other section lists only the
 * leaves the overlay changed. The overlay's own override_fields (a copy of an earlier merge)
 * is ignored: the values say what changed.
 */
function mergePartialRuntimeSettingsForRun(
  base: PartialRuntimeSettingsOverride,
  overlay?: RuntimeSettingsOverride,
  original: RuntimeSettingsOverride | undefined = base,
): RuntimeSettingsOverride | undefined {
  const stored = cloneRuntimeSettingsForRun(base) as PartialRuntimeSettingsOverride
  const patch = cloneRuntimeSettingsForRun(overlay) as Record<string, unknown> | undefined
  if (!patch) return cloneRuntimeSettingsForRun(original)
  let draft = runtimeDraftFromOverride(stored)
  let changed = false
  // Sections first, so an alias edit (iterations beside an unchanged run_logic copy) wins.
  const keys = Object.keys(patch)
    .filter(key => key !== 'override_fields' && patch[key] !== undefined)
    .sort((left, right) => Number(COMPACT_RUNTIME_ALIASES.has(left)) - Number(COMPACT_RUNTIME_ALIASES.has(right)))
  for (const key of keys) {
    const value = patch[key]
    if (key === 'browser') {
      const compact = (value ?? {}) as Record<string, unknown>
      for (const field of COMPACT_BROWSER_FIELDS) {
        const path = `browser.${field}`
        const next = compact[field] === '' || compact[field] === null ? undefined : compact[field]
        if (sameRuntimeValue(overrideFieldValue(stored, path), next)) continue
        draft = next === undefined ? resetOverrideField(draft, path) : setOverrideField(draft, path, next)
        changed = true
      }
      continue
    }
    const path = normalizeRuntimeOverrideFieldPath(key)
    if (!path) continue
    // A section key is read off the stored row itself (it holds only listed values), so a leaf
    // the overlay removed is seen as removed even when only some of the section is listed.
    const before = COMPACT_RUNTIME_ALIASES.has(key) ? overrideFieldValue(stored, path) : (stored as Record<string, unknown>)[key] ?? undefined
    if (value === null) {
      draft = resetOverrideField(draft, path)
      changed = true
    } else if (WHOLE_RUN_SECTIONS.has(path) || !isPlainRuntimeSection(value)) {
      if (sameRuntimeValue(before, value)) continue
      draft = setOverrideField(draft, path, value)
      changed = true
    } else {
      const next = applyRuntimeOverrideDiff(draft, path, before, value)
      changed = changed || next !== draft
      draft = next
    }
  }
  // A partial overlay is a complete row (buildPartialGroupRunRuntimeOverride starts from the
  // stored row), so a stored path it no longer lists was reset to inherited by the edit. Its
  // section key is simply absent from the overlay, which the value walk above cannot see.
  // Coverage is exact: a stored field survives only when the overlay lists it or an ancestor.
  // A stored v1 section the overlay lists only by some children (an upgraded v1 section after
  // an inherit reset one of its leaves) is reset and the overlay's listed children re-applied,
  // so the reset leaf leaves the row instead of the whole section staying listed.
  if (isPartialRuntimeDraft(overlay)) {
    const partialOverlay = overlay as PartialRuntimeSettingsOverride
    const overlayFields = runtimeOverrideFieldPaths(partialOverlay)
    for (const field of runtimeOverrideFieldPaths(stored)) {
      if (runtimeRowCoversPath(partialOverlay, field)) continue
      draft = resetOverrideField(draft, field)
      for (const child of overlayFields) {
        if (!child.startsWith(`${field}.`)) continue
        draft = setOverrideField(draft, child, overrideFieldValue(partialOverlay, child))
      }
      changed = true
    }
  }
  if (!changed) return cloneRuntimeSettingsForRun(original)
  return partialRuntimeOverrideFromDraft(draft)
}

/**
 * True when a group's run-dialog edits must use partial semantics: the group's merged row, the
 * stored scenario row or the run-level row is partial (override_fields). Partial edits are
 * written per path, so the two error-handling halves inherit independently and a retry policy
 * left on the group never pins them.
 */
export function groupRunRuntimeIsPartial(
  group: ScenarioScriptGroup,
  scenario: StoredScenario,
  runDefaultOverrides: RunDefaultOverrides,
  override?: GroupRunOverride,
): boolean {
  return isPartialRuntimeDraft(mergeRuntimeSettingsForRun(group.runtime_settings, override?.runtime_settings))
    || isPartialRuntimeDraft(scenario.runtime_settings)
    || isPartialRuntimeDraft(buildRuntimeSettingsForRun(scenario, runDefaultOverrides))
}

/**
 * One error-handling select edit in the group dialog. v1 rows keep the section-level inherit
 * coupling (applyGroupErrorHandlingDraftEdit); partial rows reset each half on its own.
 */
export function applyGroupErrorHandlingSelect(
  draft: GroupErrorHandlingDraftHalves,
  half: ErrorHandlingHalf,
  value: string,
  partial: boolean,
): GroupErrorHandlingDraftHalves {
  if (partial) return { ...draft, [half]: value }
  return applyGroupErrorHandlingDraftEdit(draft, half, value)
}

/** One run-dialog edit of a group's runtime settings: `undefined` resets the path to inherited. */
export type GroupRuntimePathEdit = { path: string; value: unknown }

/**
 * The run override a group edit stores when the group row or the scenario row is partial. The
 * backend applies a partial row's listed paths only (pkg/api/runtime_overrides.go), so every edit
 * is written through the runtime settings model as its own path: nothing inherited is copied in,
 * because a partial ancestor's sections hold only the leaves it lists and copying one into a
 * group section would replace the script's whole section with those leaves. A v1 group row is
 * upgraded first, so its sections keep applying whole (legacy v1 semantics). The result is the
 * complete row (stored row plus edits); `{ override_fields: [] }` when the edits reset everything,
 * so the clear is not mistaken for "no override" and the stored row restored.
 */
export function buildPartialGroupRunRuntimeOverride(
  stored: RuntimeSettingsOverride | undefined,
  existing: RuntimeSettingsOverride | undefined,
  edits: readonly GroupRuntimePathEdit[],
): RuntimeSettingsOverride {
  const current = mergeRuntimeSettingsForRun(stored, existing)
  let draft = upgradeRuntimeDraftToPartial(runtimeDraftFromOverride(cloneRuntimeSettingsForRun(current) as PartialRuntimeSettingsOverride))
  for (const edit of edits) {
    draft = edit.value === undefined ? resetOverrideField(draft, edit.path) : setOverrideField(draft, edit.path, edit.value)
  }
  return partialRuntimeOverrideFromDraft(draft) ?? { override_fields: [] } as PartialRuntimeSettingsOverride
}

function isPlainRuntimeSection(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

// The setRuntime* helpers write a section of a v1 row in place (every caller is a v1 write path:
// buildRuntimeSettingsForRun's v1 branch and NewTestPage's v1 group save); partial rows go through
// setOverrideField instead. runtime-row-v1-write-begin
export function setRuntimeIterations(settings: RuntimeSettingsOverride, value: number | undefined) {
  if (settings.run_logic) {
    settings.run_logic = { ...settings.run_logic, iterations: value }
    delete settings.iterations
  } else if (value === undefined) {
    delete settings.iterations
  } else {
    settings.iterations = value
  }
}

export function setRuntimeLogLevel(settings: RuntimeSettingsOverride, value: string | undefined) {
  if (settings.log) {
    settings.log = { ...settings.log, level: value }
    delete settings.log_level
  } else if (!value) {
    delete settings.log_level
  } else {
    settings.log_level = value
  }
}

export function setRuntimeAutoTransactions(settings: RuntimeSettingsOverride, value: string | undefined) {
  if (settings.run_logic) {
    settings.run_logic = { ...settings.run_logic, auto_transactions: value }
    delete settings.auto_transactions
  } else if (!value) {
    delete settings.auto_transactions
  } else {
    settings.auto_transactions = value
  }
}

export type RuntimeBrowserWriteOptions = {
  /**
   * True when the level ABOVE the group pins a whole `browser_config` section. Clearing every
   * browser field cannot then be expressed by releasing the group's section — the ancestor's
   * would simply be handed down — so the group keeps an EMPTY browser_config, which is the only
   * shape that overrides an ancestor section: pkg/api/runtime_overrides.go mergeRuntimeSettings
   * copies a non-nil group BrowserConfig outright, whereas a flat `browser` merges field-wise and
   * skips empty fields.
   */
  ancestorPinsBrowserConfig?: boolean
}

/**
 * Write the four browser fields the run editors expose into whichever wire shape the level
 * already carries. The two shapes are NOT interchangeable on the wire and cannot be normalised
 * into one: the backend merges a flat `browser` field-wise onto the script's own config, while
 * `browser_config` replaces that config whole (pkg/api/runtime_overrides.go:379-395). This is the
 * one place that knows the difference — every other helper works on the flat four-field view
 * resolveBrowserLayers() returns.
 *
 * Both shapes release the section the same way, which is the half that used to be missing: when
 * nothing is left to pin the key is REMOVED so stampClearedRuntimeSections can tombstone it.
 * Leaving an empty `browser_config` behind would transmit a whole-section replace and wipe the
 * script's own engine, viewport and timeouts — the opposite of the "Standard" the cleared
 * controls promise.
 */
export function setRuntimeBrowser(
  settings: RuntimeSettingsOverride,
  browser: RuntimeSettingsOverride['browser'],
  options: RuntimeBrowserWriteOptions = {},
) {
  const fields = {
    headless: browser?.headless,
    device_profile: browser?.device_profile,
    touch_mode: browser?.touch_mode,
    orientation: browser?.orientation,
  }
  if (settings.browser_config) {
    const next = { ...settings.browser_config, ...fields }
    delete settings.browser
    if (runtimeSectionHasValues(next) || options.ancestorPinsBrowserConfig) settings.browser_config = next
    else delete settings.browser_config
  } else if (runtimeSectionHasValues(fields)) {
    settings.browser = { ...browser }
  } else {
    delete settings.browser
  }
}
// runtime-row-v1-write-end

export function getEffectiveRunDuration(scenario?: StoredScenario | null, overrides?: RunDefaultOverrides) {
  return scenario?.termination_mode === 'iterations' ? '' : overrides?.duration?.trim() || scenario?.duration || ''
}

export function getEffectiveResourceBaseline(scenario?: StoredScenario | null, overrides?: RunDefaultOverrides) {
  if (!scenario) return false
  return hasOwnKey(overrides || {}, 'resource_baseline') ? Boolean(overrides?.resource_baseline) : Boolean(scenario.resource_baseline)
}

/**
 * The run-level values, read off the row the run transmits (the run-default edits laid over the
 * scenario row, runLevelRuntimeSettings): every value is resolved per path or per section through
 * the shared resolvers, so a partial scenario row contributes only the paths it lists.
 */
export function getEffectiveRuntimeDefaults(scenario: StoredScenario, overrides: RunDefaultOverrides) {
  return effectiveRuntimeFromRows(undefined, runLevelRuntimeSettings(scenario, overrides))
}

/** The displayed run-time values of a row stack (group row over the run-level row). */
function effectiveRuntimeFromRows(group?: RuntimeSettingsOverride, root?: RuntimeSettingsOverride) {
  const iterations = runtimeLayerValue('run_logic.iterations', group, root).value
  return {
    iterations: iterations === undefined || iterations === null ? undefined : Number(iterations),
    pacing: runtimeLayerSection('pacing', group, root),
    pacing_partial: runtimeLayerSectionIsPartial('pacing', group, root),
    think_time: runtimeLayerSection('think_time', group, root),
    think_time_partial: runtimeLayerSectionIsPartial('think_time', group, root),
    browser: resolveBrowserLayers(group, root),
    log_level: (runtimeLayerValue('log.level', group, root).value as string | undefined) || undefined,
    auto_transactions: (runtimeLayerValue('run_logic.auto_transactions', group, root).value as string | undefined) || undefined,
    ...resolveErrorHandlingLayers(group, root),
  }
}

export function stagedGroupPeakVUsers(group: ScenarioScriptGroup): number | undefined {
  if (!group.stages?.length) return undefined
  const peakTarget = maxStageVUsers(group.stages)
  return peakTarget > 0 ? peakTarget : group.vusers
}

export function applyGroupOverride(group: ScenarioScriptGroup, override?: GroupRunOverride): ScenarioScriptGroup {
  const stagedPeak = stagedGroupPeakVUsers(group)
  if (!override && stagedPeak === undefined) return group
  return {
    ...group,
    vusers: stagedPeak ?? (hasOwnKey(override || {}, 'vusers') ? Number(override?.vusers) : group.vusers),
    ramp_up: hasOwnKey(override || {}, 'ramp_up') ? override?.ramp_up : group.ramp_up,
    ramp_down: hasOwnKey(override || {}, 'ramp_down') ? override?.ramp_down : group.ramp_down,
    initialization: hasOwnKey(override || {}, 'initialization') ? override?.initialization : group.initialization,
    assignment: override?.assignment || group.assignment,
  }
}

export function buildEffectiveScenarioGroups(scenario: StoredScenario | undefined, overrides: Record<string, GroupRunOverride>) {
  return (scenario?.scripts || []).map(group => applyGroupOverride(group, overrides[groupOverrideKey(group)]))
}

export function effectiveGroupIterations(group: ScenarioScriptGroup, scenario: StoredScenario, runDefaultOverrides: RunDefaultOverrides, groupOverride?: GroupRunOverride) {
  const runDefaults = getEffectiveRuntimeDefaults(scenario, runDefaultOverrides)
  // Read off the merged row the run submits, so a run edit that reset the stored row's
  // iterations to inherited is not read back off the stored group.
  const effective = mergeRuntimeSettingsForRun(group.runtime_settings, groupOverride?.runtime_settings)
  const groupIterations = runtimeLayerValue('run_logic.iterations', effective).value as number | undefined
  return groupIterations ?? group.iterations ?? runDefaults.iterations
}

export function effectiveGroupRuntimeSettings(group: ScenarioScriptGroup, scenario: StoredScenario, runDefaultOverrides: RunDefaultOverrides, groupOverride?: GroupRunOverride) {
  const groupRuntime = group.runtime_settings
  const overrideRuntime = groupOverride?.runtime_settings
  // Every CLEARABLE section is resolved off the SAME overlay buildGroupsForRunPayload submits,
  // rather than through a per-section chain: mergeRuntimeSettingsForRun assigns the run
  // override's values and DELETES on its null tombstones, so a cleared section cannot be read
  // back off the stored group — in either browser wire shape — and a newly clearable section
  // needs no new case here. What the overlay does not carry falls through to the run-level
  // defaults, exactly as mergeRuntimeSettings does on the backend.
  const effective = mergeRuntimeSettingsForRun(groupRuntime, overrideRuntime)
  // Leaves are resolved per path through the merged group row and the run-level row, so a
  // partial row supplies only what it lists (a v1 row lists its sections whole).
  const root = runLevelRuntimeSettings(scenario, runDefaultOverrides)
  return {
    ...effectiveRuntimeFromRows(effective, root),
    iterations: effectiveGroupIterations(group, scenario, runDefaultOverrides, groupOverride),
  }
}

/**
 * An untyped partial pacing or think_time override the group dialog's inherit option would hide:
 * the rows list only some leaves (say think_time.mean_ms) and the type comes from the script, so
 * the type select sits on its inherit option with the leaf inputs hidden. Returns the summary
 * the table shows and whether the group row itself lists the leaves (so a reset at the group
 * removes them), or undefined when nothing is hidden.
 */
export function groupHiddenPartialTiming(
  section: 'pacing' | 'think_time',
  group: ScenarioScriptGroup,
  scenario: StoredScenario,
  runDefaultOverrides: RunDefaultOverrides,
  groupOverride?: GroupRunOverride,
): { summary: string; groupOwned: boolean } | undefined {
  const runtime = effectiveGroupRuntimeSettings(group, scenario, runDefaultOverrides, groupOverride)
  const value = section === 'pacing' ? runtime.pacing : runtime.think_time
  const partial = section === 'pacing' ? runtime.pacing_partial : runtime.think_time_partial
  if (!partial || !value || value.type || (section === 'think_time' && (value as NonNullable<RuntimeSettingsOverride['think_time']>).ignore)) return undefined
  const summary = section === 'pacing'
    ? formatRuntimePacingTableValue(value as RuntimeSettingsOverride['pacing'], true)
    : formatRuntimeThinkTimeTableValue(value as RuntimeSettingsOverride['think_time'], true)
  const groupRow = mergeRuntimeSettingsForRun(group.runtime_settings, groupOverride?.runtime_settings)
  return { summary, groupOwned: isFieldOverridden(groupRow, section) }
}

function hasRunRuntimeOverrides(overrides: RunDefaultOverrides) {
  return Object.keys(overrides.runtime_settings).length > 0
}

/**
 * buildRuntimeSettingsForRun for a partial scenario row: every run default is applied through
 * the runtime settings model, so the path is listed in override_fields (the backend ignores an
 * unlisted section), and a run-level clear resets the path to inherited instead of deleting a
 * key the row still lists (which the backend would apply as the zero value).
 */
function buildPartialRuntimeSettingsForRun(base: PartialRuntimeSettingsOverride, runtimeOverrides: RunDefaultOverrides['runtime_settings']): RuntimeSettingsOverride | undefined {
  let draft = runtimeDraftFromOverride(cloneRuntimeSettingsForRun(base) as PartialRuntimeSettingsOverride)
  const apply = (path: string, value: unknown) => {
    draft = value === undefined ? resetOverrideField(draft, path) : setOverrideField(draft, path, value)
  }
  if (hasOwnKey(runtimeOverrides, 'iterations')) {
    apply('run_logic.iterations', runtimeOverrides.iterations == null ? undefined : Number(runtimeOverrides.iterations))
  }
  if (hasOwnKey(runtimeOverrides, 'pacing')) apply('pacing', runtimeOverrides.pacing ? { ...runtimeOverrides.pacing } : undefined)
  if (hasOwnKey(runtimeOverrides, 'think_time')) apply('think_time', runtimeOverrides.think_time ? { ...runtimeOverrides.think_time } : undefined)
  if (hasOwnKey(runtimeOverrides, 'log_level')) apply('log.level', runtimeOverrides.log_level || undefined)
  if (hasOwnKey(runtimeOverrides, 'auto_transactions')) apply('run_logic.auto_transactions', runtimeOverrides.auto_transactions || undefined)
  if (hasOwnKey(runtimeOverrides, 'continue_on_error')) apply('error_handling.continue_on_error', runtimeOverrides.continue_on_error ?? undefined)
  if (hasOwnKey(runtimeOverrides, 'snapshot_on_error')) apply('error_handling.snapshot_on_error', runtimeOverrides.snapshot_on_error ?? undefined)
  // The run has overrides, so the row is always transmitted: a defined empty row applies nothing,
  // whereas leaving runtime_settings out makes the API clone the scenario's stored row back in
  // (testcreation.go), restoring the path the run just cleared.
  return partialRuntimeOverrideFromDraft(draft) ?? {}
}

export function buildRuntimeSettingsForRun(scenario: StoredScenario, overrides: RunDefaultOverrides): RuntimeSettingsOverride | undefined {
  if (!hasRunRuntimeOverrides(overrides)) return undefined
  const runtimeOverrides = overrides.runtime_settings
  if (isPartialRuntimeDraft(scenario.runtime_settings)) {
    return buildPartialRuntimeSettingsForRun(scenario.runtime_settings as PartialRuntimeSettingsOverride, runtimeOverrides)
  }
  const payload = cloneRuntimeSettingsForRun(scenario.runtime_settings) || {}

  if (hasOwnKey(runtimeOverrides, 'iterations')) {
    setRuntimeIterations(payload, runtimeOverrides.iterations == null ? undefined : Number(runtimeOverrides.iterations))
  }
  if (hasOwnKey(runtimeOverrides, 'pacing')) {
    if (runtimeOverrides.pacing) payload.pacing = { ...runtimeOverrides.pacing }
    else delete payload.pacing
  }
  if (hasOwnKey(runtimeOverrides, 'think_time')) {
    if (runtimeOverrides.think_time) payload.think_time = { ...runtimeOverrides.think_time }
    else delete payload.think_time
  }
  if (hasOwnKey(runtimeOverrides, 'log_level')) {
    setRuntimeLogLevel(payload, runtimeOverrides.log_level || undefined)
  }
  if (hasOwnKey(runtimeOverrides, 'auto_transactions')) {
    setRuntimeAutoTransactions(payload, runtimeOverrides.auto_transactions || undefined)
  }
  if (hasOwnKey(runtimeOverrides, 'continue_on_error') || hasOwnKey(runtimeOverrides, 'snapshot_on_error')) {
    const errorHandling = { ...(payload.error_handling || {}) }
    if (hasOwnKey(runtimeOverrides, 'continue_on_error')) {
      if (runtimeOverrides.continue_on_error == null) delete errorHandling.continue_on_error
      else errorHandling.continue_on_error = runtimeOverrides.continue_on_error
    }
    if (hasOwnKey(runtimeOverrides, 'snapshot_on_error')) {
      if (runtimeOverrides.snapshot_on_error == null) delete errorHandling.snapshot_on_error
      else errorHandling.snapshot_on_error = runtimeOverrides.snapshot_on_error
    }
    if (errorHandlingSectionHasValues(errorHandling)) payload.error_handling = errorHandling
    else delete payload.error_handling
  }

  return payload
}

export function buildRunLoadProfilePreview(groups: ScenarioScriptGroup[], duration: string, startDelay: string | undefined, resourceBaseline: boolean, trafficProfile?: TrafficProfile, scenarioMode?: StoredScenario['scenario_mode'], goalTargetTps?: number): RunLoadProfilePreview {
  const showVUsers = scenarioMode !== 'goal_oriented'
  if (!groups.length) {
    return {
      points: [],
      showVUsers,
      totalSeconds: 0,
      steadySeconds: 0,
      preBaselineSeconds: 0,
      postBaselineSeconds: 0,
      peakVUsers: 0,
    }
  }

  const steadySeconds = parseDurationToSeconds(duration)
  const startDelaySeconds = parseDurationToSeconds(startDelay)
  const preBaselineSeconds = startDelaySeconds + (resourceBaseline ? RESOURCE_BASELINE_SECONDS : 0)
  const postBaselineSeconds = resourceBaseline ? RESOURCE_BASELINE_SECONDS : 0
  const profileMappings = groups.map(group => trafficProfileMappingForGroup(trafficProfile, group.name || group.script_name))
  const profileVUsers = trafficProfile?.runtime_target === 'vusers'
  const profileTps = trafficProfile?.runtime_target === 'tps'
  const configuredGoalTps = !profileTps && scenarioMode === 'goal_oriented' && Number(goalTargetTps) > 0
    ? Number(goalTargetTps)
    : undefined
  const includesTps = profileTps || configuredGoalTps !== undefined
  // A group activates at its start offset (traffic-profile groups have none):
  // its ramp and stages count from activation, and it holds until the shared
  // scenario deadline, as the engine runs it.
  const groupOffsets = groups.map((group, index) => profileMappings[index] ? 0 : Math.min(steadySeconds, parseDurationToSeconds(group.start_offset)))
  const maxRampDown = groups.reduce(
    (max, group, index) => (profileMappings[index] || group.stages?.length) ? max : Math.max(max, parseDurationToSeconds(group.ramp_down)),
    0,
  )
  const totalSeconds = preBaselineSeconds + steadySeconds + maxRampDown + postBaselineSeconds
  if (totalSeconds <= 0) {
    return { points: [], showVUsers, totalSeconds: 0, steadySeconds, preBaselineSeconds, postBaselineSeconds, peakVUsers: 0 }
  }

  const step = Math.max(1, Math.floor(totalSeconds / 30))
  const timeline = new Set<number>([0, totalSeconds, preBaselineSeconds, preBaselineSeconds + steadySeconds])
  for (let second = step; second < totalSeconds; second += step) timeline.add(second)
  groups.forEach((group, index) => {
    if (profileMappings[index]) return
    timeline.add(preBaselineSeconds + groupOffsets[index])
    if (group.stages?.length) {
      // Each stage's end and every step of its VUser ramps.
      const stageOptions = { initialLevel: initialStageLevel(group.stages, group.vusers), emptyDurationSeconds: 0, missingTarget: 'zero' as const }
      ;[...stageBoundarySeconds(group.stages), ...stageRampStepSeconds(group.stages, stageOptions)].forEach(end => {
        timeline.add(Math.min(preBaselineSeconds + steadySeconds, preBaselineSeconds + groupOffsets[index] + end))
      })
      return
    }
    // Every VUser start and stop, so the staircase is drawn step for step.
    const rampUp = parseDurationToSeconds(group.ramp_up)
    const deadline = preBaselineSeconds + steadySeconds
    engineRampUpSeconds(group.vusers, rampUp).forEach(start => {
      timeline.add(Math.min(deadline, preBaselineSeconds + groupOffsets[index] + start))
    })
    const reached = engineLevelAtDeadline(group.vusers, rampUp, steadySeconds - groupOffsets[index])
    engineRampDownSeconds(reached, parseDurationToSeconds(group.ramp_down)).forEach(stop => timeline.add(deadline + stop))
  })
  trafficProfileTimelineSeconds(trafficProfile).forEach(second => timeline.add(preBaselineSeconds + Math.min(steadySeconds, second)))
  const tpsGroupIndexes = groups.flatMap((_, index) => profileTps && profileMappings[index] ? [index] : [])
  const profileTpsStatistics = profileTps
    ? trafficProfileTpsStatistics(profileMappings, steadySeconds)
    : undefined
  const points: Array<Record<string, number>> = []
  let peakVUsers = 0
  let peakTps = 0

  for (const second of Array.from(timeline).filter(value => value >= 0 && value <= totalSeconds).sort((left, right) => left - right)) {
    const point: Record<string, number> = { second, total: 0 }
    groups.forEach((group, index) => {
      const loadSecond = Math.max(0, second - preBaselineSeconds)
      const groupSecond = loadSecond - groupOffsets[index]
      const profileMapping = profileMappings[index]
      let value = 0

      if (second < preBaselineSeconds || groupSecond < 0) {
        value = 0
      } else if (profileVUsers && profileMapping) {
        value = loadSecond < steadySeconds ? trafficProfileTargetAt(profileMapping, loadSecond) : 0
      } else if (group.stages?.length) {
        value = loadSecond < steadySeconds ? stagedRunGroupValue(group, groupSecond) : 0
      } else {
        // The engine's staircase: VUsers start one at a time through the
        // ramp-up; at the deadline the group stops ramping up and stops the
        // VUsers it reached one at a time through the ramp-down. A group that
        // activates at the deadline never runs.
        const rampUp = parseDurationToSeconds(group.ramp_up)
        if (loadSecond < steadySeconds) {
          value = engineRampUpLevel(group.vusers, rampUp, groupSecond)
        } else {
          const reached = engineLevelAtDeadline(group.vusers, rampUp, steadySeconds - groupOffsets[index])
          value = engineRampDownLevel(reached, parseDurationToSeconds(group.ramp_down), loadSecond - steadySeconds)
        }
      }

      const roundedValue = Math.round(Math.max(0, value))
      point[`group${index}`] = roundedValue
      point.total += roundedValue

      if (profileTps && profileMapping) {
        const tps = second >= preBaselineSeconds && loadSecond < steadySeconds ? trafficProfileTargetAt(profileMapping, loadSecond) : 0
        point[`group${index}Tps`] = tps
        point.totalTps = (point.totalTps || 0) + tps
      }
    })
    if (configuredGoalTps !== undefined) {
      point.totalTps = second >= preBaselineSeconds && second < preBaselineSeconds + steadySeconds ? configuredGoalTps : 0
    }
    peakVUsers = Math.max(peakVUsers, point.total)
    peakTps = Math.max(peakTps, point.totalTps || 0)
    points.push(point)
  }

  const lastPoint = points[points.length - 1]
  if (!lastPoint || lastPoint.second < totalSeconds) {
    const finalPoint: Record<string, number> = { second: totalSeconds, total: 0 }
    groups.forEach((_, index) => {
      finalPoint[`group${index}`] = 0
      if (tpsGroupIndexes.includes(index)) finalPoint[`group${index}Tps`] = 0
    })
    if (includesTps) finalPoint.totalTps = 0
    points.push(finalPoint)
  }

  return {
    points,
    showVUsers,
    totalSeconds,
    steadySeconds,
    preBaselineSeconds,
    postBaselineSeconds,
    peakVUsers,
    ...(includesTps ? {
      peakTps: profileTpsStatistics?.maximum ?? configuredGoalTps ?? peakTps,
      minTps: profileTpsStatistics?.minimum ?? configuredGoalTps ?? 0,
      averageTps: profileTpsStatistics?.average ?? configuredGoalTps ?? 0,
      estimatedPeakVUsers: profileTps && trafficProfile?.sizing?.complete ? trafficProfile.sizing.estimated_peak_vusers : undefined,
      recommendedGenerators: profileTps && trafficProfile?.sizing?.complete ? trafficProfile.sizing.recommended_generators : undefined,
      runtimeTarget: 'tps' as const,
      tpsGroupIndexes,
    } : profileVUsers ? { runtimeTarget: 'vusers' as const } : {}),
  }
}

// A saved stage schedule as the controller runs it (workloadStages).
function stagedRunGroupValue(group: ScenarioScriptGroup, elapsedSeconds: number): number {
  const stages = group.stages || []
  return stageLevelAt(stages, elapsedSeconds, 'target_vusers', {
    initialLevel: initialStageLevel(stages, group.vusers),
    emptyDurationSeconds: 0,
    missingTarget: 'zero',
  })
}

export function buildRunDefaultCards(scenario: StoredScenario | undefined, overrides: RunDefaultOverrides): RunDefaultCard[] {
  if (!scenario) return []
  const effectiveRuntime = getEffectiveRuntimeDefaults(scenario, overrides)
  // Same whole-section rule as the group table: a scenario section that names neither
  // boolean still executes both as false, so the scenario column must not claim the
  // script's own setting is inherited.
  const scenarioResolved = effectiveRuntimeFromRows(undefined, scenario.runtime_settings)
  const duration = getEffectiveRunDuration(scenario, overrides)
  const scriptShakeout = hasOwnKey(overrides, 'script_shakeout') ? overrides.script_shakeout : scenario.script_shakeout
  const resourceBaseline = hasOwnKey(overrides, 'resource_baseline') ? overrides.resource_baseline : scenario.resource_baseline
  return [
    {
      key: 'duration',
      label: scenario.termination_mode === 'iterations' ? 'Completion' : 'Duration',
      value: scenario.termination_mode === 'iterations' ? 'All per-user iterations' : duration,
      scenarioValue: scenario.duration,
      isDefault: false,
      isOverridden: hasOwnKey(overrides, 'duration'),
    },
    {
      key: 'iterations',
      label: 'Iterations',
      value: effectiveRuntime.iterations === undefined ? 'Script default' : String(effectiveRuntime.iterations),
      scenarioValue: String(scenarioResolved.iterations ?? 'Script default'),
      isDefault: effectiveRuntime.iterations === undefined,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'iterations'),
    },
    {
      key: 'log_level',
      label: 'Log level',
      value: effectiveRuntime.log_level || 'Script default',
      scenarioValue: scenarioResolved.log_level || 'Script default',
      isDefault: !effectiveRuntime.log_level,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'log_level'),
    },
    {
      key: 'auto_transactions',
      label: 'Auto transactions',
      value: effectiveRuntime.auto_transactions || 'Script default',
      scenarioValue: scenarioResolved.auto_transactions || 'Script default',
      isDefault: !effectiveRuntime.auto_transactions,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'auto_transactions'),
    },
    {
      key: 'pacing',
      label: 'Pacing',
      value: formatRuntimePacingConfig(effectiveRuntime.pacing, effectiveRuntime.pacing_partial),
      scenarioValue: formatRuntimePacingConfig(scenarioResolved.pacing, scenarioResolved.pacing_partial),
      isDefault: !effectiveRuntime.pacing,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'pacing'),
    },
    {
      key: 'think_time',
      label: 'Think time',
      value: formatRuntimeThinkTimeConfig(effectiveRuntime.think_time, effectiveRuntime.think_time_partial),
      scenarioValue: formatRuntimeThinkTimeConfig(scenarioResolved.think_time, scenarioResolved.think_time_partial),
      isDefault: !effectiveRuntime.think_time,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'think_time'),
    },
    {
      key: 'continue_on_error',
      label: 'Continue on error',
      value: formatRuntimeBooleanValue(effectiveRuntime.continue_on_error),
      scenarioValue: formatRuntimeBooleanValue(scenarioResolved.continue_on_error),
      isDefault: effectiveRuntime.continue_on_error === undefined,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'continue_on_error'),
    },
    {
      key: 'snapshot_on_error',
      label: 'Snapshot on error',
      value: formatRuntimeBooleanValue(effectiveRuntime.snapshot_on_error),
      scenarioValue: formatRuntimeBooleanValue(scenarioResolved.snapshot_on_error),
      isDefault: effectiveRuntime.snapshot_on_error === undefined,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'snapshot_on_error'),
    },
    {
      key: 'script_shakeout',
      label: 'Script shakeout',
      value: formatEnabledValue(scriptShakeout),
      scenarioValue: formatEnabledValue(scenario.script_shakeout),
      isDefault: false,
      isOverridden: hasOwnKey(overrides, 'script_shakeout'),
    },
    {
      key: 'resource_baseline',
      label: 'Resource baseline',
      value: formatResourceBaselineValue(resourceBaseline),
      scenarioValue: formatResourceBaselineValue(scenario.resource_baseline),
      isDefault: false,
      isOverridden: hasOwnKey(overrides, 'resource_baseline'),
    },
  ]
}

export function buildRunDefaultEditDraft(key: RunDefaultKey, scenario: StoredScenario, overrides: RunDefaultOverrides): RunDefaultEditDraft {
  const effectiveRuntime = getEffectiveRuntimeDefaults(scenario, overrides)
  const base: RunDefaultEditDraft = { value: '', pacingType: '', pacingAmount: '', thinkIgnore: false }
  if (key === 'duration') {
    return { ...base, value: getEffectiveRunDuration(scenario, overrides) }
  }
  if (key === 'iterations') {
    return { ...base, value: effectiveRuntime.iterations === undefined ? '' : String(effectiveRuntime.iterations) }
  }
  if (key === 'log_level') {
    return { ...base, value: effectiveRuntime.log_level || '' }
  }
  if (key === 'auto_transactions') {
    return { ...base, value: effectiveRuntime.auto_transactions || '' }
  }
  if (key === 'pacing') {
    const pacing = effectiveRuntime.pacing
    const amount = pacing?.type === 'fixed_delay'
      ? formatMillisecondsAsSecondsValue(pacing.delay_ms, 0, true)
      : pacing?.type === 'fixed_interval'
        ? formatMillisecondsAsSecondsValue(pacing.interval_ms, 0, true)
        : pacing?.type === 'tps'
          ? pacing.tps
          : ''
    return {
      ...base,
      pacingType: pacing?.type || '',
      pacingAmount: amount === undefined ? '' : String(amount),
      pacingConfig: pacing ? { ...pacing } : undefined,
    }
  }
  if (key === 'think_time') {
    const thinkTime = effectiveRuntime.think_time
    return {
      ...base,
      value: thinkTime?.ignore ? '' : (thinkTime?.mean_ms === undefined ? '' : String(thinkTime.mean_ms)),
      thinkIgnore: Boolean(thinkTime?.ignore),
      thinkTimeConfig: thinkTime
        ? { ...thinkTime, type: canonicalThinkTimeType(thinkTime.type) }
        : undefined,
    }
  }
  if (key === 'continue_on_error') {
    return { ...base, value: effectiveRuntime.continue_on_error === undefined ? '' : String(effectiveRuntime.continue_on_error) }
  }
  if (key === 'snapshot_on_error') {
    return { ...base, value: effectiveRuntime.snapshot_on_error === undefined ? '' : String(effectiveRuntime.snapshot_on_error) }
  }
  if (key === 'script_shakeout') {
    const value = hasOwnKey(overrides, 'script_shakeout') ? overrides.script_shakeout : scenario.script_shakeout
    return { ...base, value: String(Boolean(value)) }
  }
  const value = hasOwnKey(overrides, 'resource_baseline') ? overrides.resource_baseline : scenario.resource_baseline
  return { ...base, value: String(Boolean(value)) }
}

export function buildGroupsForRunPayload(scenario: StoredScenario | undefined, overrides: Record<string, GroupRunOverride>) {
  if (!scenario || groupOverrideCount(overrides) === 0) return undefined
  return scenario.scripts.map(group => {
    const key = groupOverrideKey(group)
    const override = overrides[key]
    const effective = applyGroupOverride(group, override)
    const runtimeSettings = mergeRuntimeSettingsForRun(group.runtime_settings, override?.runtime_settings)
    const assignment = override?.assignment || group.assignment
    return {
      name: effective.name || effective.script_name || `Group ${effective.order + 1}`,
      script_id: effective.script_id,
      ...(effective.role ? { role: effective.role } : {}),
      vusers: effective.vusers,
      ramp_up: effective.ramp_up || undefined,
      ramp_down: effective.ramp_down || undefined,
      start_offset: effective.start_offset || undefined,
      ...(group.transaction_response_time_sla_ms !== undefined ? { transaction_response_time_sla_ms: group.transaction_response_time_sla_ms } : {}),
      stages: effective.stages,
      iterations: group.iterations,
      initialization: effective.initialization || 'at_runtime',
      ...(runtimeSettings ? { runtime_settings: runtimeSettings } : {}),
      ...(assignment ? { assignment } : {}),
    }
  })
}

export function formatGroupPlacement(override?: GroupRunOverride, group?: ScenarioScriptGroup) {
  const assignment = override?.assignment || group?.assignment
  const requiredLabels = normalizeRequiredLabels(assignment?.required_labels)
  const labelText = requiredLabels
    ? `Capability: ${Object.entries(requiredLabels).map(([key, value]) => `${key}=${value}`).join(', ')}`
    : ''
  if (assignment?.mode === 'auto' && labelText) {
    return labelText
  }
  if (assignment?.mode === 'region_weighted') {
    const weights = Object.entries(assignment.region_weights || {}).map(([name, weight]) => `${name} ${weight}%`).join(', ')
    return weights ? `Region weighted: ${weights}` : 'Region weighted'
  }
  if (assignment?.mode === 'load_zone_weighted') {
    const weights = Object.entries(assignment.load_zone_weights || {}).map(([name, weight]) => `${name} ${weight}%`).join(', ')
    return weights ? `Load-zone weighted: ${weights}` : 'Load-zone weighted'
  }
  const targets = assignment?.selected_targets || []
  if (!targets.length) return 'Auto'
  const hostText = targets.length === 1 ? `Pinned: ${targets[0]}` : `Sharded: ${targets.length} hosts, capacity weighted`
  return labelText ? `${hostText} · ${labelText}` : hostText
}


export function groupPlacementSummary(overrides: Record<string, GroupRunOverride>, groups?: ScenarioScriptGroup[]) {
  const assignments = groups
    ? groups.map(group => (overrides[groupOverrideKey(group)]?.assignment || group.assignment))
    : Object.values(overrides).map(override => override.assignment)
  const placements = assignments.filter(hasActivePlacementAssignment)
  if (placements.length === 0) return 'Auto placement'
  const pinned = placements.filter(assignment => (assignment?.selected_targets || []).length === 1).length
  const sharded = placements.filter(assignment => (assignment?.selected_targets || []).length > 1).length
  const capability = placements.filter(assignment => !(assignment?.selected_targets || []).length && normalizeRequiredLabels(assignment?.required_labels)).length
  const weighted = placements.filter(assignment => assignment?.mode === 'region_weighted' || assignment?.mode === 'load_zone_weighted').length
  return [
    pinned > 0 ? formatCount(pinned, 'pinned group') : '',
    sharded > 0 ? formatCount(sharded, 'sharded group') : '',
    capability > 0 ? formatCount(capability, 'capability-constrained group') : '',
    weighted > 0 ? formatCount(weighted, 'weighted group') : '',
  ].filter(Boolean).join(', ')
}

export function generatorCanRunGroup(generator: SystemInventoryGenerator, group?: ScenarioScriptGroup) {
  const drainActive = !generator.drain_state || generator.drain_state === 'active'
  const connected = ['connected', 'healthy', 'ready'].includes(generator.status)
  const compatible = !generator.compatibility_state || generator.compatibility_state === 'compatible' || generator.compatibility_state === 'unknown'
  const protocols = generator.supported_protocols || []
  const protocolSupported = !group?.protocol || protocols.length === 0 || protocols.includes(group.protocol)
  return drainActive && connected && compatible && protocolSupported && (generator.available_capacity ?? 0) > 0
}

export function generatorDisabledReason(generator: SystemInventoryGenerator, group?: ScenarioScriptGroup) {
  if (generator.drain_state && generator.drain_state !== 'active') return `Host is ${generator.drain_state}`
  if (!['connected', 'healthy', 'ready'].includes(generator.status)) return `Host is ${generator.status}`
  if (generator.compatibility_state && generator.compatibility_state !== 'compatible' && generator.compatibility_state !== 'unknown') return generator.compatibility_issues?.[0] || `Compatibility is ${generator.compatibility_state}`
  const protocols = generator.supported_protocols || []
  if (group?.protocol && protocols.length > 0 && !protocols.includes(group.protocol)) return `Does not support ${group.protocol}`
  if ((generator.available_capacity ?? 0) <= 0) return 'No available capacity'
  return ''
}

export function selectedGeneratorCapacity(generators: SystemInventoryGenerator[], selectedTargets: string[]) {
  const selected = new Set(selectedTargets)
  return generators.reduce((total, generator) => selected.has(generator.target) ? total + (generator.available_capacity || 0) : total, 0)
}

export function buildGroupRuntimeTableValues(group: ScenarioScriptGroup, scenario: StoredScenario, runDefaultOverrides: RunDefaultOverrides, override?: GroupRunOverride) {
  const runtime = effectiveGroupRuntimeSettings(group, scenario, runDefaultOverrides, override)
  return {
    iterations: runtime.iterations === undefined ? 'Duration driven' : String(runtime.iterations),
    pacing: formatRuntimePacingTableValue(runtime.pacing, runtime.pacing_partial),
    thinkTime: formatRuntimeThinkTimeTableValue(runtime.think_time, runtime.think_time_partial),
    logLevel: formatRuntimeText(runtime.log_level, 'Standard'),
    autoTransactions: formatRuntimeText(runtime.auto_transactions, 'None'),
    errorHandling: formatRuntimeErrorHandlingTableValue(runtime.continue_on_error, runtime.snapshot_on_error, runtime.error_handling_pinned),
    browser: formatRuntimeBrowserTableValue(runtime.browser),
  }
}

export function emptyGroupEditDraft(): GroupEditDraft {
  return {
    vusers: '',
    ramp_up: '',
    ramp_down: '',
    initialization: 'at_runtime',
    iterations: '',
    log_level: '',
    auto_transactions: '',
    pacingType: '',
    pacingAmount: '',
    thinkTimeMs: '',
    thinkIgnore: false,
    continue_on_error: '',
    snapshot_on_error: '',
    browser_device_profile: '',
    browser_headless: '',
    browser_touch_mode: '',
    browser_orientation: '',
    assignmentMode: 'auto',
    selectedTargets: [],
    requiredLabels: {},
    regionWeights: {},
    loadZoneWeights: {},
    tolerateGeneratorLoss: undefined,
  }
}

function runtimePacingAmount(pacing?: RuntimeSettingsOverride['pacing']) {
  if (pacing?.type === 'fixed_delay') return formatMillisecondsAsSecondsValue(pacing.delay_ms, 0, true)
  if (pacing?.type === 'fixed_interval') return formatMillisecondsAsSecondsValue(pacing.interval_ms, 0, true)
  if (pacing?.type === 'tps') return pacing.tps
  return ''
}

function buildRuntimeGroupEditDraftValues(runtime: ReturnType<typeof effectiveGroupRuntimeSettings>): Pick<GroupEditDraft,
  'iterations'
  | 'pacingType'
  | 'pacingAmount'
  | 'pacingConfig'
  | 'thinkTimeMs'
  | 'thinkIgnore'
  | 'thinkTimeConfig'
  | 'continue_on_error'
  | 'snapshot_on_error'
  | 'browser_device_profile'
  | 'browser_headless'
  | 'browser_touch_mode'
  | 'browser_orientation'
> {
  const pacingAmount = runtimePacingAmount(runtime.pacing)
  return {
    iterations: runtime.iterations === undefined ? '' : String(runtime.iterations),
    pacingType: runtime.pacing?.type || '',
    pacingAmount: pacingAmount === undefined ? '' : String(pacingAmount),
    pacingConfig: runtime.pacing ? { ...runtime.pacing } : undefined,
    thinkTimeMs: runtime.think_time?.mean_ms === undefined ? '' : String(runtime.think_time.mean_ms),
    thinkIgnore: Boolean(runtime.think_time?.ignore),
    thinkTimeConfig: runtime.think_time
      ? { ...runtime.think_time, type: canonicalThinkTimeType(runtime.think_time.type) }
      : undefined,
    // A pinned section resolves to a real boolean (an omitted half executes false), so the
    // dialog pre-fills Enabled/Disabled; '' ('Script default') is reserved for the only
    // case that really inherits the script's own config.error_handling.
    continue_on_error: runtime.continue_on_error === undefined ? '' : String(runtime.continue_on_error),
    snapshot_on_error: runtime.snapshot_on_error === undefined ? '' : String(runtime.snapshot_on_error),
    browser_device_profile: runtime.browser?.device_profile || '',
    browser_headless: runtime.browser?.headless === undefined ? '' : String(runtime.browser.headless),
    browser_touch_mode: runtime.browser?.touch_mode === undefined ? '' : String(runtime.browser.touch_mode),
    browser_orientation: runtime.browser?.orientation || '',
  }
}

/** The group dialog's text selects whose empty option inherits (Inherit (<effective>)). */
export const GROUP_INHERITABLE_TEXT_PATHS = { log_level: 'log.level', auto_transactions: 'run_logic.auto_transactions' } as const
export type GroupInheritableTextField = keyof typeof GROUP_INHERITABLE_TEXT_PATHS

/**
 * The value the group's own row (stored group row with this run's override laid over it)
 * supplies at `path`, or '' when it supplies none, so the select sits on its inherit option
 * instead of showing a value the group does not set.
 */
function groupOwnRuntimeText(group: ScenarioScriptGroup, override: GroupRunOverride | undefined, path: string): string {
  const groupRow = mergeRuntimeSettingsForRun(group.runtime_settings, override?.runtime_settings)
  const value = runtimeLayerValue(path, groupRow).value
  return typeof value === 'string' ? value.trim() : ''
}

/**
 * What the group runs with on the inherit option: the run-level row (run defaults over the
 * scenario row), then the script, then the engine default. Picking inherit resets the group's
 * leaf (a partial path, see NewTestPage's save), so a value the group sets does not hide the run
 * level. A group row that already lists the path with no value (a v1 section that omits it, or a
 * partial clear) replaces the run level and the script's section at run time (the merge copies the
 * group section and applyRuntimeOverrides replaces the script's section whole), so there only the
 * engine default applies. Undefined when no layer that is known here sets it.
 */
export function groupInheritedRuntimeText(
  field: GroupInheritableTextField,
  group: ScenarioScriptGroup,
  scenario: StoredScenario,
  runDefaultOverrides: RunDefaultOverrides,
  override?: GroupRunOverride,
  scriptConfig?: ScriptConfig,
  engineDefaults?: ScriptConfig,
): string | undefined {
  const path = GROUP_INHERITABLE_TEXT_PATHS[field]
  const groupRow = mergeRuntimeSettingsForRun(group.runtime_settings, override?.runtime_settings)
  const text = (value: unknown) => (typeof value === 'string' && value.trim() ? value.trim() : undefined)
  const engineOnly = () => text(effectiveRuntimeValue(path, { engine: engineDefaults }).value)
  const own = runtimeLayerValue(path, groupRow)
  if (own.listed && text(own.value) === undefined) return engineOnly()
  const root = runtimeLayerValue(path, runLevelRuntimeSettings(scenario, runDefaultOverrides))
  if (root.listed) return text(root.value) ?? engineOnly()
  return text(effectiveRuntimeValue(path, { script: scriptConfig, engine: engineDefaults }).value)
}

/**
 * The run override after the full runtime settings editor saved `row` as the group's complete
 * runtime row (stored group row plus every edit). The row is partial, so merging it over the
 * stored group row applies exactly its paths (mergePartialRuntimeSettingsForRun); a row that
 * resets everything is `{ override_fields: [] }`, never a missing row that would restore the
 * stored one. Every other run override of the group is kept.
 */
export function groupRunOverrideWithRuntimeRow(existing: GroupRunOverride | undefined, row: RuntimeSettingsOverride): GroupRunOverride {
  return { ...(existing || {}), runtime_settings: cloneRuntimeSettingsForRun(row) }
}

export function buildGroupEditDraft(group: ScenarioScriptGroup, scenario: StoredScenario, runDefaultOverrides: RunDefaultOverrides, override?: GroupRunOverride): GroupEditDraft {
  const effective = applyGroupOverride(group, override)
  const runtime = effectiveGroupRuntimeSettings(group, scenario, runDefaultOverrides, override)
  const assignment = override?.assignment || group.assignment
  const requiredLabels = normalizeRequiredLabels(assignment?.required_labels) || {}
  const assignmentMode = assignment?.mode === 'specific_hosts'
    ? 'specific_hosts'
    : assignment?.mode === 'region_weighted'
      ? 'region_weighted'
      : assignment?.mode === 'load_zone_weighted'
        ? 'load_zone_weighted'
        : Object.keys(requiredLabels).length > 0
          ? 'required_labels'
          : 'auto'
  return {
    ...emptyGroupEditDraft(),
    vusers: String(effective.vusers || ''),
    ramp_up: effective.ramp_up || '',
    ramp_down: effective.ramp_down || '',
    initialization: effective.initialization || 'at_runtime',
    ...buildRuntimeGroupEditDraftValues(runtime),
    log_level: groupOwnRuntimeText(group, override, GROUP_INHERITABLE_TEXT_PATHS.log_level),
    auto_transactions: groupOwnRuntimeText(group, override, GROUP_INHERITABLE_TEXT_PATHS.auto_transactions),
    assignmentMode,
    selectedTargets: assignment?.selected_targets || [],
    requiredLabels,
    regionWeights: { ...(assignment?.region_weights || {}) },
    loadZoneWeights: { ...(assignment?.load_zone_weights || {}) },
    tolerateGeneratorLoss: assignment?.tolerate_generator_loss,
  }
}
