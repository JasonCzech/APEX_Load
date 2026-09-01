import { GeneratorAssignmentSettings, RuntimeSettingsOverride, StoredScenario, SystemInventoryGenerator, TrafficProfile } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { normalizeRequiredLabels } from './assignmentLabels'
import { canonicalThinkTimeType } from './thinkTimeConfig'
import { formatMillisecondsAsSecondsValue, formatPacingTiming } from './runtimeTiming'
import { trafficProfileMappingForGroup, trafficProfileTargetAt, trafficProfileTimelineSeconds, trafficProfileTpsStatistics } from './trafficProfilePreviewModel'

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

function formatRuntimePacingConfig(pacing?: RuntimeSettingsOverride['pacing'] | null) {
  return formatPacingTiming(pacing) || 'Script default'
}

function formatRuntimePacingTableValue(pacing?: RuntimeSettingsOverride['pacing'] | null) {
  if (!pacing?.type) return 'Script timing'
  return formatRuntimePacingConfig(pacing)
}

function formatRuntimePacing(scenario?: StoredScenario | null) {
  return formatRuntimePacingConfig(scenario?.runtime_settings?.pacing)
}

function formatRuntimeThinkTimeConfig(thinkTime?: RuntimeSettingsOverride['think_time'] | null) {
  if (!thinkTime) return 'Script default'
  if (thinkTime.ignore) return 'Disabled'
  if (thinkTime.type === 'recorded') return 'Replay recorded think time'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

function formatRuntimeThinkTimeTableValue(thinkTime?: RuntimeSettingsOverride['think_time'] | null) {
  if (!thinkTime) return 'Script think time'
  return formatRuntimeThinkTimeConfig(thinkTime)
}

function formatRuntimeThinkTime(scenario?: StoredScenario | null) {
  return formatRuntimeThinkTimeConfig(scenario?.runtime_settings?.think_time)
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
  const continueLabel = continueOnError ? 'Continue' : 'Stop'
  const snapshotLabel = snapshotOnError ? 'Snapshot' : 'No snapshot'
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
 * Read the two booleans out of ONE error_handling section the way the run will execute
 * them: a section that omits a boolean executes it as false, so the resolved value is
 * never undefined once a section exists. Only the absence of a section leaves both
 * undefined, which is the single case that really does inherit the script's own config.
 */
function resolveErrorHandlingSectionValues(section?: RuntimeSettingsOverride['error_handling']): ResolvedErrorHandling {
  if (!errorHandlingSectionIsPinned(section)) {
    return { continue_on_error: undefined, snapshot_on_error: undefined, error_handling_pinned: false }
  }
  return {
    continue_on_error: Boolean(section?.continue_on_error),
    snapshot_on_error: Boolean(section?.snapshot_on_error),
    error_handling_pinned: true,
  }
}

// Mirrors buildRuntimeSettingsForRun exactly — that function builds the run-level section this
// page actually submits, so any divergence here reports a section the run will not send. The
// flat continue_on_error/snapshot_on_error run-default overrides are folded into the scenario's
// error_handling section, so clearing one of them does not by itself remove a section the
// scenario supplies. Returns undefined when the run level transmits NO section: unlike a
// section read back off the API, this one is synthesised here, so an empty result means
// "nothing set" rather than the pinned `{}` the wire uses for an all-false section.
function runDefaultErrorHandlingSection(
  runtimeOverrides: RunRuntimeOverrideDraft,
  scenarioRuntime?: RuntimeSettingsOverride,
): RuntimeSettingsOverride['error_handling'] {
  const scenarioSection = scenarioRuntime?.error_handling
  const touchesErrorHandling = hasOwnKey(runtimeOverrides, 'continue_on_error') || hasOwnKey(runtimeOverrides, 'snapshot_on_error')
  if (!touchesErrorHandling) return scenarioSection
  const merged = { ...(scenarioSection || {}) }
  if (hasOwnKey(runtimeOverrides, 'continue_on_error')) {
    if (runtimeOverrides.continue_on_error == null) delete merged.continue_on_error
    else merged.continue_on_error = runtimeOverrides.continue_on_error
  }
  if (hasOwnKey(runtimeOverrides, 'snapshot_on_error')) {
    if (runtimeOverrides.snapshot_on_error == null) delete merged.snapshot_on_error
    else merged.snapshot_on_error = runtimeOverrides.snapshot_on_error
  }
  return errorHandlingSectionHasValues(merged) ? merged : undefined
}

/**
 * Section-wise resolution, matching mergeRuntimeSettings: when the group transmits a section it
 * is the one that runs and it is applied whole, so lower levels contribute nothing — not even a
 * half the winner omits. Resolving the two booleans independently across levels would report a
 * half the run discards. `effective` is the submitted overlay, so a run-only tombstone has
 * already removed the stored section by the time it gets here.
 */
function resolveGroupErrorHandling(
  effective: RuntimeSettingsOverride | undefined,
  runDefaults: ResolvedErrorHandling,
): ResolvedErrorHandling {
  const section = effective?.error_handling
  if (!errorHandlingSectionIsPinned(section)) {
    return {
      continue_on_error: runDefaults.continue_on_error,
      snapshot_on_error: runDefaults.snapshot_on_error,
      error_handling_pinned: runDefaults.error_handling_pinned,
    }
  }
  return resolveErrorHandlingSectionValues(section)
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

function runtimeIterations(settings?: RuntimeSettingsOverride) {
  return settings?.run_logic?.iterations ?? settings?.iterations
}

function runtimeLogLevel(settings?: RuntimeSettingsOverride) {
  return settings?.log?.level ?? settings?.log_level
}

function runtimeAutoTransactions(settings?: RuntimeSettingsOverride) {
  return settings?.run_logic?.auto_transactions ?? settings?.auto_transactions
}

function runtimeBrowser(settings?: RuntimeSettingsOverride): RuntimeSettingsOverride['browser'] | undefined {
  if (settings?.browser_config) {
    return {
      headless: settings.browser_config.headless,
      device_profile: settings.browser_config.device_profile,
      touch_mode: settings.browser_config.touch_mode,
      orientation: settings.browser_config.orientation,
    }
  }
  return settings?.browser ?? undefined
}

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
 * runtimeBrowser() returns.
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

export function getEffectiveRunDuration(scenario?: StoredScenario | null, overrides?: RunDefaultOverrides) {
  return overrides?.duration?.trim() || scenario?.duration || ''
}

export function getEffectiveResourceBaseline(scenario?: StoredScenario | null, overrides?: RunDefaultOverrides) {
  if (!scenario) return false
  return hasOwnKey(overrides || {}, 'resource_baseline') ? Boolean(overrides?.resource_baseline) : Boolean(scenario.resource_baseline)
}

export function getEffectiveRuntimeDefaults(scenario: StoredScenario, overrides: RunDefaultOverrides) {
  const runtimeOverrides = overrides.runtime_settings
  const scenarioRuntime = scenario.runtime_settings
  return {
    iterations: hasOwnKey(runtimeOverrides, 'iterations') ? runtimeOverrides.iterations ?? undefined : runtimeIterations(scenarioRuntime),
    pacing: hasOwnKey(runtimeOverrides, 'pacing') ? runtimeOverrides.pacing ?? undefined : scenarioRuntime?.pacing,
    think_time: hasOwnKey(runtimeOverrides, 'think_time') ? runtimeOverrides.think_time ?? undefined : scenarioRuntime?.think_time,
    browser: runtimeBrowser(scenarioRuntime),
    log_level: hasOwnKey(runtimeOverrides, 'log_level') ? runtimeOverrides.log_level || undefined : runtimeLogLevel(scenarioRuntime),
    auto_transactions: hasOwnKey(runtimeOverrides, 'auto_transactions') ? runtimeOverrides.auto_transactions || undefined : runtimeAutoTransactions(scenarioRuntime),
    ...resolveErrorHandlingSectionValues(runDefaultErrorHandlingSection(runtimeOverrides, scenarioRuntime)),
  }
}

export function stagedGroupPeakVUsers(group: ScenarioScriptGroup): number | undefined {
  if (!group.stages?.length) return undefined

  const peakTarget = group.stages.reduce((peak, stage) => {
    const target = Number(stage.target_vusers)
    return Number.isFinite(target) ? Math.max(peak, target) : peak
  }, 0)
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
  return runtimeIterations(groupOverride?.runtime_settings) ?? runtimeIterations(group.runtime_settings) ?? group.iterations ?? runDefaults.iterations
}

export function effectiveGroupRuntimeSettings(group: ScenarioScriptGroup, scenario: StoredScenario, runDefaultOverrides: RunDefaultOverrides, groupOverride?: GroupRunOverride) {
  const runDefaults = getEffectiveRuntimeDefaults(scenario, runDefaultOverrides)
  const groupRuntime = group.runtime_settings
  const overrideRuntime = groupOverride?.runtime_settings
  // Every CLEARABLE section is resolved off the SAME overlay buildGroupsForRunPayload submits,
  // rather than through a per-section chain: mergeRuntimeSettingsForRun assigns the run
  // override's values and DELETES on its null tombstones, so a cleared section cannot be read
  // back off the stored group — in either browser wire shape — and a newly clearable section
  // needs no new case here. What the overlay does not carry falls through to the run-level
  // defaults, exactly as mergeRuntimeSettings does on the backend.
  const effective = mergeRuntimeSettingsForRun(groupRuntime, overrideRuntime)
  return {
    iterations: effectiveGroupIterations(group, scenario, runDefaultOverrides, groupOverride),
    pacing: effective?.pacing ?? runDefaults.pacing,
    think_time: effective?.think_time ?? runDefaults.think_time,
    browser: runtimeBrowser(effective) ?? runDefaults.browser,
    // Not clearable by any run editor (their controls have no inherit option), so these keep
    // the plain override -> group -> run-default precedence.
    log_level: runtimeLogLevel(overrideRuntime) ?? runtimeLogLevel(groupRuntime) ?? runDefaults.log_level,
    auto_transactions: runtimeAutoTransactions(overrideRuntime) ?? runtimeAutoTransactions(groupRuntime) ?? runDefaults.auto_transactions,
    ...resolveGroupErrorHandling(effective, runDefaults),
  }
}

function hasRunRuntimeOverrides(overrides: RunDefaultOverrides) {
  return Object.keys(overrides.runtime_settings).length > 0
}

export function buildRuntimeSettingsForRun(scenario: StoredScenario, overrides: RunDefaultOverrides): RuntimeSettingsOverride | undefined {
  if (!hasRunRuntimeOverrides(overrides)) return undefined
  const runtimeOverrides = overrides.runtime_settings
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
    if (group.stages?.length) {
      let cursor = preBaselineSeconds
      group.stages.forEach(stage => {
        cursor += parseDurationToSeconds(stage.duration)
        timeline.add(Math.min(preBaselineSeconds + steadySeconds, cursor))
      })
      return
    }
    timeline.add(preBaselineSeconds + Math.min(steadySeconds, parseDurationToSeconds(group.ramp_up)))
    timeline.add(preBaselineSeconds + steadySeconds + parseDurationToSeconds(group.ramp_down))
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
      const profileMapping = profileMappings[index]
      let value = 0

      if (second < preBaselineSeconds) {
        value = 0
      } else if (profileVUsers && profileMapping) {
        value = loadSecond < steadySeconds ? trafficProfileTargetAt(profileMapping, loadSecond) : 0
      } else if (group.stages?.length) {
        value = loadSecond < steadySeconds ? stagedRunGroupValue(group, loadSecond) : 0
      } else {
        const rampUp = parseDurationToSeconds(group.ramp_up)
        const rampDown = parseDurationToSeconds(group.ramp_down)
        if (loadSecond < rampUp) {
          value = rampUp > 0 ? (loadSecond / rampUp) * group.vusers : group.vusers
        } else if (loadSecond < steadySeconds) {
          value = group.vusers
        } else if (loadSecond < steadySeconds + rampDown) {
          value = rampDown > 0 ? (1 - ((loadSecond - steadySeconds) / rampDown)) * group.vusers : 0
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
      runtimeTarget: 'tps' as const,
      tpsGroupIndexes,
    } : profileVUsers ? { runtimeTarget: 'vusers' as const } : {}),
  }
}

function stagedRunGroupValue(group: ScenarioScriptGroup, elapsedSeconds: number): number {
  const stages = group.stages || []
  const firstType = stages[0]?.type?.trim().toLowerCase()
  let previousValue = firstType === 'ramp_down'
    ? Math.max(0, Number(group.vusers) || 0)
    : firstType === 'hold'
      ? Math.max(0, Number(stages[0]?.target_vusers) || 0)
      : 0
  let cursor = 0

  for (const stage of stages) {
    const duration = parseDurationToSeconds(stage.duration)
    const target = Math.max(0, Number(stage.target_vusers) || 0)
    const end = cursor + duration
    if (elapsedSeconds <= end) {
      if (stage.type.trim().toLowerCase() === 'ramp_up' || stage.type.trim().toLowerCase() === 'ramp_down') {
        const progress = duration > 0 ? Math.max(0, Math.min(1, (elapsedSeconds - cursor) / duration)) : 1
        return previousValue + ((target - previousValue) * progress)
      }
      return target
    }
    cursor = end
    previousValue = target
  }
  return previousValue
}

export function buildRunDefaultCards(scenario: StoredScenario | undefined, overrides: RunDefaultOverrides): RunDefaultCard[] {
  if (!scenario) return []
  const effectiveRuntime = getEffectiveRuntimeDefaults(scenario, overrides)
  // Same whole-section rule as the group table: a scenario section that names neither
  // boolean still executes both as false, so the scenario column must not claim the
  // script's own setting is inherited.
  const scenarioErrorHandling = resolveErrorHandlingSectionValues(scenario.runtime_settings?.error_handling)
  const duration = getEffectiveRunDuration(scenario, overrides)
  const scriptShakeout = hasOwnKey(overrides, 'script_shakeout') ? overrides.script_shakeout : scenario.script_shakeout
  const resourceBaseline = hasOwnKey(overrides, 'resource_baseline') ? overrides.resource_baseline : scenario.resource_baseline
  return [
    {
      key: 'duration',
      label: 'Duration',
      value: duration,
      scenarioValue: scenario.duration,
      isDefault: false,
      isOverridden: hasOwnKey(overrides, 'duration'),
    },
    {
      key: 'iterations',
      label: 'Iterations',
      value: effectiveRuntime.iterations === undefined ? 'Script default' : String(effectiveRuntime.iterations),
      scenarioValue: String(runtimeIterations(scenario.runtime_settings) ?? 'Script default'),
      isDefault: effectiveRuntime.iterations === undefined,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'iterations'),
    },
    {
      key: 'log_level',
      label: 'Log Level',
      value: effectiveRuntime.log_level || 'Script default',
      scenarioValue: runtimeLogLevel(scenario.runtime_settings) || 'Script default',
      isDefault: !effectiveRuntime.log_level,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'log_level'),
    },
    {
      key: 'auto_transactions',
      label: 'Auto Transactions',
      value: effectiveRuntime.auto_transactions || 'Script default',
      scenarioValue: runtimeAutoTransactions(scenario.runtime_settings) || 'Script default',
      isDefault: !effectiveRuntime.auto_transactions,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'auto_transactions'),
    },
    {
      key: 'pacing',
      label: 'Pacing',
      value: formatRuntimePacingConfig(effectiveRuntime.pacing),
      scenarioValue: formatRuntimePacing(scenario),
      isDefault: !effectiveRuntime.pacing,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'pacing'),
    },
    {
      key: 'think_time',
      label: 'Think Time',
      value: formatRuntimeThinkTimeConfig(effectiveRuntime.think_time),
      scenarioValue: formatRuntimeThinkTime(scenario),
      isDefault: !effectiveRuntime.think_time,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'think_time'),
    },
    {
      key: 'continue_on_error',
      label: 'Continue On Error',
      value: formatRuntimeBooleanValue(effectiveRuntime.continue_on_error),
      scenarioValue: formatRuntimeBooleanValue(scenarioErrorHandling.continue_on_error),
      isDefault: effectiveRuntime.continue_on_error === undefined,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'continue_on_error'),
    },
    {
      key: 'snapshot_on_error',
      label: 'Snapshot On Error',
      value: formatRuntimeBooleanValue(effectiveRuntime.snapshot_on_error),
      scenarioValue: formatRuntimeBooleanValue(scenarioErrorHandling.snapshot_on_error),
      isDefault: effectiveRuntime.snapshot_on_error === undefined,
      isOverridden: hasOwnKey(overrides.runtime_settings, 'snapshot_on_error'),
    },
    {
      key: 'script_shakeout',
      label: 'Script Shakeout',
      value: formatEnabledValue(scriptShakeout),
      scenarioValue: formatEnabledValue(scenario.script_shakeout),
      isDefault: false,
      isOverridden: hasOwnKey(overrides, 'script_shakeout'),
    },
    {
      key: 'resource_baseline',
      label: 'Resource Baseline',
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

function formatCount(value: number, singular: string, plural = `${singular}s`) {
  return `${value} ${value === 1 ? singular : plural}`
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
    pacing: formatRuntimePacingTableValue(runtime.pacing),
    thinkTime: formatRuntimeThinkTimeTableValue(runtime.think_time),
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
  | 'log_level'
  | 'auto_transactions'
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
    log_level: runtime.log_level || 'standard',
    auto_transactions: runtime.auto_transactions || 'none',
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
    assignmentMode,
    selectedTargets: assignment?.selected_targets || [],
    requiredLabels,
    regionWeights: { ...(assignment?.region_weights || {}) },
    loadZoneWeights: { ...(assignment?.load_zone_weights || {}) },
    tolerateGeneratorLoss: assignment?.tolerate_generator_loss,
  }
}
