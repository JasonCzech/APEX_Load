import type { RuntimeSettingsOverride } from '../types'
import { overrideFromRuntimeDraft, runtimeDraftFromOverride } from './runtimeSettingsModel'
import { canonicalThinkTimeType, thinkTimeConfigForType } from './thinkTimeConfig'

// Version 2 browser checkpoints (AUTH-3 versioned reader). Until the scenario
// form kept only its canonical runtime override, goal config and assignment,
// a checkpoint's form also carried flat mirrors of them (runtime_pacing_*,
// runtime_think_time_*, runtime_iterations, runtime_log_level,
// runtime_auto_transactions, continue_on_error, snapshot_on_error, the
// browser_* fields, the goal_* bounds, assignment_mode and
// assignment_targets), and the save composed the canonical values from both.
// This is that composition, kept only to read a version 2 checkpoint once;
// nothing else composes scenario values from mirrors.

type LegacyRuntimeForm = Record<string, unknown>

/** The flat mirror fields a version 2 checkpoint form carried. */
export const LEGACY_FLAT_FORM_FIELDS = [
  'runtime_iterations',
  'runtime_pacing_type', 'runtime_pacing_delay_ms', 'runtime_pacing_min_delay_ms', 'runtime_pacing_max_delay_ms',
  'runtime_pacing_interval_ms', 'runtime_pacing_min_interval_ms', 'runtime_pacing_max_interval_ms', 'runtime_pacing_tps',
  'runtime_think_time_type', 'runtime_think_time_ms', 'runtime_think_time_std_ms', 'runtime_think_time_min_ms', 'runtime_think_time_max_ms',
  'runtime_think_time_min_percent', 'runtime_think_time_max_percent', 'runtime_think_time_multiplier', 'runtime_think_time_limit_ms',
  'runtime_ignore_think_time', 'runtime_log_level', 'runtime_auto_transactions',
  'continue_on_error', 'snapshot_on_error',
  'browser_device_profile', 'browser_orientation', 'browser_touch_mode', 'browser_headless',
  'goal_target_average_tps', 'goal_min_vusers', 'goal_max_vusers', 'goal_p95_latency_ms', 'goal_max_error_pct',
  'assignment_mode', 'assignment_targets',
] as const

const GOAL_BOUNDS = ['target_average_tps', 'min_vusers', 'max_vusers', 'p95_latency_ms', 'max_error_pct'] as const

const num = (value: unknown) => (typeof value === 'number' && Number.isFinite(value) ? value : undefined)
const str = (value: unknown) => (typeof value === 'string' ? value : '')
const flag = (value: unknown) => (typeof value === 'boolean' ? value : undefined)

/** True when a version 2 checkpoint form's mirror fields have the types the version 2 reader required. */
export function legacyFlatFormFieldsValid(form: LegacyRuntimeForm): boolean {
  return ['runtime_pacing_type', 'runtime_log_level', 'runtime_auto_transactions', 'assignment_mode'].every(name => typeof form[name] === 'string')
    && typeof form.runtime_ignore_think_time === 'boolean'
    && Array.isArray(form.assignment_targets) && form.assignment_targets.every(value => typeof value === 'string')
}

function legacyPacing(form: LegacyRuntimeForm, preserved: RuntimeSettingsOverride['pacing']): RuntimeSettingsOverride['pacing'] | undefined {
  const type = str(form.runtime_pacing_type)
  if (!type) return preserved ? { ...preserved } : undefined
  const sameType = preserved?.type === type
  const kept = sameType ? preserved : {}
  switch (type) {
    case 'none': return { ...kept, type: 'none' }
    case 'fixed_interval': return { ...kept, type, interval_ms: num(form.runtime_pacing_interval_ms) }
    case 'random_interval': return { ...kept, type, min_interval_ms: num(form.runtime_pacing_min_interval_ms), max_interval_ms: num(form.runtime_pacing_max_interval_ms) }
    case 'tps': return { ...kept, type, tps: num(form.runtime_pacing_tps) }
    case 'fixed_delay': return { ...kept, type, delay_ms: num(form.runtime_pacing_delay_ms) }
    case 'random_delay': return { ...kept, type, min_delay_ms: num(form.runtime_pacing_min_delay_ms), max_delay_ms: num(form.runtime_pacing_max_delay_ms) }
    default: return undefined
  }
}

function legacyThinkTime(form: LegacyRuntimeForm, preserved: RuntimeSettingsOverride['think_time']): RuntimeSettingsOverride['think_time'] | undefined {
  const type = canonicalThinkTimeType(str(form.runtime_think_time_type) || preserved?.type)
  if (!type) return undefined
  const sameType = canonicalThinkTimeType(preserved?.type) === type
  return thinkTimeConfigForType({
    ...(sameType ? preserved : {}),
    type,
    multiplier: num(form.runtime_think_time_multiplier),
    limit_ms: num(form.runtime_think_time_limit_ms),
    ignore: Boolean(form.runtime_ignore_think_time),
    ...(type === 'recorded' ? {} : { mean_ms: num(form.runtime_think_time_ms) ?? 0 }),
    ...(type === 'gaussian' ? { std_ms: num(form.runtime_think_time_std_ms), min_ms: num(form.runtime_think_time_min_ms), max_ms: num(form.runtime_think_time_max_ms) } : {}),
    ...(type === 'random_percentage' ? {
      min_percent: num(form.runtime_think_time_min_percent) ?? preserved?.min_percent,
      max_percent: num(form.runtime_think_time_max_percent) ?? preserved?.max_percent,
    } : {}),
    ...(type === 'uniform' ? { min_ms: num(form.runtime_think_time_min_ms), max_ms: num(form.runtime_think_time_max_ms) } : {}),
  }, type)
}

/**
 * The canonical runtime override a version 2 checkpoint form described: its
 * runtime_settings with the flat mirrors composed over it, as the version 2
 * save did. Browser mirrors apply only when one was authored.
 */
export function runtimeSettingsFromLegacyFlatForm(form: LegacyRuntimeForm): RuntimeSettingsOverride | undefined {
  const canonical = (form.runtime_settings && typeof form.runtime_settings === 'object' ? form.runtime_settings : undefined) as RuntimeSettingsOverride | undefined
  const preserved = runtimeDraftFromOverride(canonical)
  const browserFlat = {
    ...(str(form.browser_device_profile) ? { device_profile: str(form.browser_device_profile) } : {}),
    ...(str(form.browser_orientation) ? { orientation: str(form.browser_orientation) } : {}),
    ...(str(form.browser_touch_mode) ? { touch_mode: str(form.browser_touch_mode) === 'true' } : {}),
    ...(str(form.browser_headless) ? { headless: str(form.browser_headless) === 'true' } : {}),
  }
  const includeBrowser = Object.keys(browserFlat).length > 0
  const hasRunLogic = canonical?.run_logic !== undefined
  const hasLog = canonical?.log !== undefined
  const hasBrowserConfig = canonical?.browser_config !== undefined
  const iterations = num(form.runtime_iterations)
  return overrideFromRuntimeDraft({
    ...preserved,
    iterations: hasRunLogic ? undefined : iterations,
    pacing: legacyPacing(form, canonical?.pacing ?? undefined) ?? undefined,
    think_time: legacyThinkTime(form, canonical?.think_time ?? undefined) ?? undefined,
    browser: includeBrowser && !hasBrowserConfig ? browserFlat : preserved?.browser,
    browser_config: includeBrowser && hasBrowserConfig ? { ...preserved?.browser_config, ...browserFlat } : preserved?.browser_config,
    log_level: hasLog ? '' : str(form.runtime_log_level),
    log: hasLog ? { ...preserved?.log, level: str(form.runtime_log_level) || undefined } : preserved?.log,
    auto_transactions: hasRunLogic ? '' : str(form.runtime_auto_transactions),
    run_logic: hasRunLogic ? { ...preserved?.run_logic, iterations, auto_transactions: str(form.runtime_auto_transactions) || undefined } : preserved?.run_logic,
    continue_on_error: flag(form.continue_on_error),
    snapshot_on_error: flag(form.snapshot_on_error),
  })
}

/** The goal config a version 2 form described: each flat bound replaced its goal value, and an absent one removed it. */
function goalConfigFromLegacyFlatForm(form: LegacyRuntimeForm): Record<string, unknown> | undefined {
  const goal: Record<string, unknown> = form.goal_config && typeof form.goal_config === 'object' ? { ...form.goal_config } : {}
  for (const bound of GOAL_BOUNDS) {
    const value = num(form[`goal_${bound}`])
    if (value === undefined) delete goal[bound]
    else goal[bound] = value
  }
  return Object.keys(goal).length > 0 ? goal : undefined
}

/** The assignment a version 2 form described: its flat mode, and its flat targets when it placed on specific hosts. */
function assignmentFromLegacyFlatForm(form: LegacyRuntimeForm): Record<string, unknown> {
  const assignment: Record<string, unknown> = form.assignment && typeof form.assignment === 'object' ? { ...form.assignment } : {}
  const mode = str(form.assignment_mode) || 'auto'
  assignment.mode = mode
  if (mode === 'specific_hosts') assignment.selected_targets = [...(form.assignment_targets as string[])]
  return assignment
}

/** A version 2 checkpoint form as the current form: its mirrors composed into the canonical values and removed. */
export function migrateLegacyFlatForm(form: LegacyRuntimeForm): LegacyRuntimeForm {
  const runtimeSettings = runtimeSettingsFromLegacyFlatForm(form)
  const goalConfig = goalConfigFromLegacyFlatForm(form)
  const assignment = assignmentFromLegacyFlatForm(form)
  const next: LegacyRuntimeForm = { ...form }
  for (const name of LEGACY_FLAT_FORM_FIELDS) delete next[name]
  if (runtimeSettings) next.runtime_settings = runtimeSettings
  else delete next.runtime_settings
  if (goalConfig) next.goal_config = goalConfig
  else delete next.goal_config
  next.assignment = assignment
  return next
}
