import { GeneratorAssignmentSettings, RuntimeSettingsOverride, StoredScenario, SystemInventoryGenerator } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { normalizeRequiredLabels } from './assignmentLabels'
import { canonicalThinkTimeType } from './thinkTimeConfig'
import { formatMillisecondsAsSecondsValue, formatPacingTiming } from './runtimeTiming'

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
  totalSeconds: number
  steadySeconds: number
  preBaselineSeconds: number
  postBaselineSeconds: number
  peakVUsers: number
}

const RESOURCE_BASELINE_SECONDS = 5 * 60

export const GLOBAL_RUN_DEFAULT_KEYS = new Set<RunDefaultKey>(['duration', 'script_shakeout', 'resource_baseline'])

export function buildEmptyRunDefaultOverrides(): RunDefaultOverrides {
  return { runtime_settings: {} }
}

export function hasOwnKey(object: object, key: PropertyKey): boolean {
  return Object.prototype.hasOwnProperty.call(object, key)
}

export function groupOverrideKey(group: ScenarioScriptGroup) {
  return `${group.order}:${group.script_id}`
}

export function hasRuntimeSettingsValues(settings?: RuntimeSettingsOverride) {
  if (!settings) return false
  return Object.values(settings).some(value => value !== undefined && value !== null && value !== '')
}

/**
 * True when a run-only patch carries a null TOMBSTONE for `key`, i.e. it removes the stored
 * section rather than leaving it untouched. See RuntimeSettingsOverride.error_handling.
 */
export function runtimeSettingsSectionCleared(settings: RuntimeSettingsOverride | undefined, key: keyof RuntimeSettingsOverride) {
  if (!settings) return false
  return hasOwnKey(settings, key) && (settings as Record<string, unknown>)[key] === null
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
//   1. ANY section in the chain — even a retry-only one that names neither boolean —
//      pins BOTH halves, driving the unnamed half to false. 'Script default' is
//      therefore keyed off section presence (sectionPinned), not off the two booleans:
//      only when nothing in the override -> group -> scenario/run-default chain supplies
//      a section does the run inherit the script's own config.error_handling, which this
//      page never loads.
//   2. The chain is resolved SECTION-wise, not field-wise. The first non-empty section
//      wins entirely; lower levels contribute nothing, not even a half the winner omits.
function formatRuntimeErrorHandlingTableValue(continueOnError?: boolean, snapshotOnError?: boolean, sectionPinned = false) {
  if (!sectionPinned && continueOnError === undefined && snapshotOnError === undefined) return 'Script default'
  const continueLabel = continueOnError ? 'Continue' : 'Stop'
  const snapshotLabel = snapshotOnError ? 'Snapshot' : 'No snapshot'
  return `${continueLabel} / ${snapshotLabel}`
}

/** True when an error_handling section carries any value, retry fields included. */
export function errorHandlingSectionIsPinned(errorHandling?: RuntimeSettingsOverride['error_handling']) {
  if (!errorHandling) return false
  return Object.values(errorHandling).some(value => value !== undefined && value !== null && value !== '')
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

// Mirrors buildRuntimeSettingsForRun: the flat continue_on_error/snapshot_on_error
// run-default overrides are folded into the scenario's error_handling section, so
// clearing one of them does not by itself remove a section the scenario supplies.
function runDefaultErrorHandlingSection(runtimeOverrides: RunRuntimeOverrideDraft, scenarioRuntime?: RuntimeSettingsOverride) {
  const merged = { ...(scenarioRuntime?.error_handling || {}) }
  if (hasOwnKey(runtimeOverrides, 'continue_on_error')) {
    if (runtimeOverrides.continue_on_error == null) delete merged.continue_on_error
    else merged.continue_on_error = runtimeOverrides.continue_on_error
  }
  if (hasOwnKey(runtimeOverrides, 'snapshot_on_error')) {
    if (runtimeOverrides.snapshot_on_error == null) delete merged.snapshot_on_error
    else merged.snapshot_on_error = runtimeOverrides.snapshot_on_error
  }
  return merged
}

/**
 * Section-wise resolution of the group chain, matching mergeRuntimeSettings: the first
 * non-empty section in run-override -> stored-group -> run-default/scenario order is the
 * one the backend transmits, and it is applied whole. Resolving the two booleans
 * independently across levels would report a half the run discards.
 */
function resolveGroupErrorHandling(
  overrideRuntime: RuntimeSettingsOverride | undefined,
  groupRuntime: RuntimeSettingsOverride | undefined,
  runDefaults: ResolvedErrorHandling,
): ResolvedErrorHandling {
  // A null tombstone in the run override removes the group's stored section, so the group
  // transmits no section at all and the run falls through to the run-level/scenario section.
  const chain = runtimeSettingsSectionCleared(overrideRuntime, 'error_handling')
    ? []
    : [overrideRuntime?.error_handling, groupRuntime?.error_handling]
  const section = chain.find(errorHandlingSectionIsPinned)
  if (!section) {
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
  return settings?.browser
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

export function setRuntimeBrowser(settings: RuntimeSettingsOverride, browser: RuntimeSettingsOverride['browser']) {
  if (settings.browser_config) {
    settings.browser_config = {
      ...settings.browser_config,
      headless: browser?.headless,
      device_profile: browser?.device_profile,
      touch_mode: browser?.touch_mode,
      orientation: browser?.orientation,
    }
    delete settings.browser
  } else if (browser && Object.values(browser).some(value => value !== undefined && value !== '')) {
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
  return {
    iterations: effectiveGroupIterations(group, scenario, runDefaultOverrides, groupOverride),
    // A null tombstone in the run override removes the group's stored section, so the group's own
    // value must NOT be read back — the run falls through to the run-level default. Resolving
    // these with `??` alone would show the value the run is about to discard.
    pacing: runtimeSettingsSectionCleared(overrideRuntime, 'pacing')
      ? runDefaults.pacing
      : overrideRuntime?.pacing ?? groupRuntime?.pacing ?? runDefaults.pacing,
    think_time: runtimeSettingsSectionCleared(overrideRuntime, 'think_time')
      ? runDefaults.think_time
      : overrideRuntime?.think_time ?? groupRuntime?.think_time ?? runDefaults.think_time,
    browser: runtimeBrowser(overrideRuntime) ?? runtimeBrowser(groupRuntime) ?? runDefaults.browser,
    log_level: runtimeLogLevel(overrideRuntime) ?? runtimeLogLevel(groupRuntime) ?? runDefaults.log_level,
    auto_transactions: runtimeAutoTransactions(overrideRuntime) ?? runtimeAutoTransactions(groupRuntime) ?? runDefaults.auto_transactions,
    ...resolveGroupErrorHandling(overrideRuntime, groupRuntime, runDefaults),
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
    if (Object.values(errorHandling).some(value => value !== undefined && value !== '')) payload.error_handling = errorHandling
    else delete payload.error_handling
  }

  return payload
}

export function buildRunLoadProfilePreview(groups: ScenarioScriptGroup[], duration: string, startDelay: string | undefined, resourceBaseline: boolean): RunLoadProfilePreview {
  if (!groups.length) {
    return {
      points: [],
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
  const maxRampDown = groups.reduce(
    (max, group) => group.stages?.length ? max : Math.max(max, parseDurationToSeconds(group.ramp_down)),
    0,
  )
  const totalSeconds = preBaselineSeconds + steadySeconds + maxRampDown + postBaselineSeconds
  if (totalSeconds <= 0) {
    return { points: [], totalSeconds: 0, steadySeconds, preBaselineSeconds, postBaselineSeconds, peakVUsers: 0 }
  }

  const step = Math.max(1, Math.floor(totalSeconds / 30))
  const timeline = new Set<number>([0, totalSeconds, preBaselineSeconds, preBaselineSeconds + steadySeconds])
  for (let second = step; second < totalSeconds; second += step) timeline.add(second)
  groups.forEach(group => {
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
  const points: Array<Record<string, number>> = []
  let peakVUsers = 0

  for (const second of Array.from(timeline).filter(value => value >= 0 && value <= totalSeconds).sort((left, right) => left - right)) {
    const point: Record<string, number> = { second, total: 0 }
    groups.forEach((group, index) => {
      const loadSecond = Math.max(0, second - preBaselineSeconds)
      let value = 0

      if (second < preBaselineSeconds) {
        value = 0
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
    })
    peakVUsers = Math.max(peakVUsers, point.total)
    points.push(point)
  }

  const lastPoint = points[points.length - 1]
  if (!lastPoint || lastPoint.second < totalSeconds) {
    const finalPoint: Record<string, number> = { second: totalSeconds, total: 0 }
    groups.forEach((_, index) => {
      finalPoint[`group${index}`] = 0
    })
    points.push(finalPoint)
  }

  return {
    points,
    totalSeconds,
    steadySeconds,
    preBaselineSeconds,
    postBaselineSeconds,
    peakVUsers,
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
