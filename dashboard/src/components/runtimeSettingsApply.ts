import { type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft, type StoredScript } from '../types'
import { type ScriptConfig } from '../types/scripts'
import {
  isRuntimeDraftEmpty,
  isRuntimeFieldApplicable,
  overrideFromRuntimeDraft,
  runtimeDraftFromOverride,
  runtimeOverrideFromScriptConfig,
} from './runtimeSettingsModel'

/**
 * Apply one UI edit to a replacement section. The first edit materializes the
 * inherited section; later edits preserve only the already-authoritative group
 * section. An explicit `undefined` is a deletion request because replacement
 * sections cannot inherit one nested field independently.
 */
export function editReplacementSection<T extends object>(
  current: Partial<T> | undefined,
  next: Partial<T> | undefined,
  inherited: Partial<T> | undefined,
): Partial<T> {
  const edited: Partial<T> = structuredClone(current ?? inherited ?? {})
  if (!next) return edited
  for (const key of Object.keys(next) as Array<keyof T>) {
    const value = next[key]
    if (value === undefined) {
      delete edited[key]
    } else {
      Object.assign(edited, { [key]: structuredClone(value) })
    }
  }
  return edited
}

/** Copies a runtime profile while removing every section the target protocol cannot execute. */
export function copyRuntimeSettingsForProtocol(
  source: ScenarioRuntimeSettingsDraft,
  protocol?: string,
): ScenarioRuntimeSettingsDraft {
  const copy = structuredClone(source)
  if (!isRuntimeFieldApplicable('browser', protocol)) copy.browser = undefined
  if (!isRuntimeFieldApplicable('browser_config', protocol)) copy.browser_config = undefined
  if (!isRuntimeFieldApplicable('browser_emulation', protocol)) copy.browser_emulation = undefined
  if (!isRuntimeFieldApplicable('http', protocol)) copy.http = undefined
  if (!isRuntimeFieldApplicable('web_runtime', protocol)) copy.web_runtime = undefined
  if (!isRuntimeFieldApplicable('java_vuser', protocol)) copy.java_vuser = undefined
  return copy
}

type MaterializedWebSettings = NonNullable<RuntimeSettingsOverride['web_runtime']>

function hasNonZeroNetworkSimulation(value: MaterializedWebSettings['network_simulation']): boolean {
  return Boolean(value && (
    value.mode ||
    value.preset ||
    (value.bandwidth_kbps ?? 0) !== 0 ||
    (value.latency_ms ?? 0) !== 0 ||
    (value.jitter_ms ?? 0) !== 0 ||
    (value.packet_loss_pct ?? 0) !== 0
  ))
}

function hasNonZeroStreaming(value: MaterializedWebSettings['streaming']): boolean {
  return Boolean(value && (
    value.mode ||
    (value.buffer_size_seconds ?? 0) !== 0 ||
    (value.fixed_bitrate_kbps ?? 0) !== 0 ||
    value.bandwidth_estimation
  ))
}

function mergeMaterializedWeb(base?: MaterializedWebSettings, override?: MaterializedWebSettings): MaterializedWebSettings | undefined {
  if (!base && !override) return undefined
  const merged: MaterializedWebSettings = base ? structuredClone(base) : {}
  if (!override) return merged
  if (override.additional_attributes && Object.keys(override.additional_attributes).length > 0) {
    merged.additional_attributes = structuredClone(override.additional_attributes)
  }
  if (override.download_filters?.mode || (override.download_filters?.hosts?.length ?? 0) > 0) {
    merged.download_filters = structuredClone(override.download_filters)
  }
  if ((override.content_checks?.length ?? 0) > 0) merged.content_checks = structuredClone(override.content_checks)
  if (hasNonZeroNetworkSimulation(override.network_simulation)) {
    merged.network_simulation = structuredClone(override.network_simulation)
  }
  if (hasNonZeroStreaming(override.streaming)) merged.streaming = structuredClone(override.streaming)
  if (override.data_formatting?.auto_detect || (override.data_formatting?.chain?.length ?? 0) > 0) {
    merged.data_formatting = structuredClone(override.data_formatting)
  }
  if ((override.source_ips?.length ?? 0) > 0) merged.source_ips = structuredClone(override.source_ips)
  if ((override.network_profiles?.length ?? 0) > 0) merged.network_profiles = structuredClone(override.network_profiles)
  return merged
}

type MaterializedJavaVuser = NonNullable<RuntimeSettingsOverride['java_vuser']>

// mergeJavaVuserLayer mirrors the Go applyJavaVuserRuntimeOverride/
// mergeJavaVuserRuntimeOverride non-empty-field-wins semantics (pkg/api/
// runtime_overrides.go): an override that only sets one field must overlay onto
// the lower layer, not wipe the script's classpath/VM params. Empty lists never
// propagate a clear (matching the backend's `len(src) > 0` guard).
function mergeJavaVuserLayer(base?: MaterializedJavaVuser, override?: MaterializedJavaVuser): MaterializedJavaVuser | undefined {
  if (!base && !override) return undefined
  const merged: MaterializedJavaVuser = base ? structuredClone(base) : {}
  if (!override) return merged
  if (override.jdk_selection) merged.jdk_selection = override.jdk_selection
  if (override.jdk_home) merged.jdk_home = override.jdk_home
  if (override.additional_vm_parameters && override.additional_vm_parameters.length > 0) {
    merged.additional_vm_parameters = structuredClone(override.additional_vm_parameters)
  }
  if (override.classpath && override.classpath.length > 0) {
    merged.classpath = structuredClone(override.classpath)
  }
  if (override.use_xbootclasspath !== undefined) merged.use_xbootclasspath = override.use_xbootclasspath
  if (override.dedicated_class_loader !== undefined) merged.dedicated_class_loader = override.dedicated_class_loader
  if (override.skip_parameter_evaluation !== undefined) merged.skip_parameter_evaluation = override.skip_parameter_evaluation
  return merged
}

function overlayBrowserLegacy(
  base: NonNullable<RuntimeSettingsOverride['browser_config']>,
  legacy: NonNullable<RuntimeSettingsOverride['browser']>,
) {
  const merged = structuredClone(base)
  if (legacy.headless !== undefined) merged.headless = legacy.headless
  if (legacy.device_profile) merged.device_profile = legacy.device_profile
  if (legacy.touch_mode !== undefined) merged.touch_mode = legacy.touch_mode
  if (legacy.orientation) merged.orientation = legacy.orientation
  return merged
}

/** Mirror mergeRuntimeSettings/applyRuntimeOverrides section precedence. */
function applyEffectiveRuntimeLayer(base: RuntimeSettingsOverride, layer: RuntimeSettingsOverride): RuntimeSettingsOverride {
  const next = structuredClone(base)

  if (layer.run_logic) {
    next.run_logic = structuredClone(layer.run_logic)
    next.iterations = undefined
    next.auto_transactions = undefined
  } else {
    if (layer.iterations !== undefined) {
      if (next.run_logic) {
        next.run_logic = { ...next.run_logic, iterations: layer.iterations }
        next.iterations = undefined
      } else {
        next.iterations = layer.iterations
      }
    }
    if (layer.auto_transactions) {
      if (next.run_logic) {
        next.run_logic = { ...next.run_logic, auto_transactions: layer.auto_transactions }
        next.auto_transactions = undefined
      } else {
        next.auto_transactions = layer.auto_transactions
      }
    }
  }
  if (layer.pacing) next.pacing = structuredClone(layer.pacing)
  if (layer.think_time) next.think_time = structuredClone(layer.think_time)
  if (layer.error_handling) next.error_handling = structuredClone(layer.error_handling)
  if (layer.http) next.http = structuredClone(layer.http)
  // Whole-section replace, mirroring the Go controller's browser_emulation merge.
  if (layer.browser_emulation) next.browser_emulation = structuredClone(layer.browser_emulation)
  if (layer.web_runtime) next.web_runtime = mergeMaterializedWeb(next.web_runtime, layer.web_runtime)
  if (layer.java_vuser) next.java_vuser = mergeJavaVuserLayer(next.java_vuser, layer.java_vuser)

  if (layer.browser_config) {
    next.browser_config = structuredClone(layer.browser_config)
    next.browser = undefined
  } else if (layer.browser) {
    if (next.browser_config) {
      next.browser_config = overlayBrowserLegacy(next.browser_config, layer.browser)
      next.browser = undefined
    } else {
      next.browser = { ...next.browser, ...structuredClone(layer.browser) }
    }
  }

  if (layer.log) {
    next.log = structuredClone(layer.log)
    next.log_level = undefined
  } else if (layer.log_level) {
    if (next.log) {
      next.log = { ...next.log, level: layer.log_level }
      next.log_level = undefined
    } else {
      next.log_level = layer.log_level
    }
  }
  if (layer.base_url) next.base_url = layer.base_url
  if (layer.env && Object.keys(layer.env).length > 0) next.env = { ...next.env, ...layer.env }
  if (layer.redaction_rules && layer.redaction_rules.length > 0) next.redaction_rules = structuredClone(layer.redaction_rules)
  return next
}

/** Resolve engine → source script → scenario → source group into one portable profile. */
export function materializeRuntimeSettingsForApply(
  engineDefaults?: ScriptConfig,
  sourceScriptConfig?: ScriptConfig,
  scenarioDraft?: ScenarioRuntimeSettingsDraft,
  sourceDraft?: ScenarioRuntimeSettingsDraft,
  sourceIterations?: number,
): ScenarioRuntimeSettingsDraft | undefined {
  const layers = [
    runtimeOverrideFromScriptConfig(engineDefaults),
    runtimeOverrideFromScriptConfig(sourceScriptConfig),
    overrideFromRuntimeDraft(scenarioDraft),
    overrideFromRuntimeDraft(sourceDraft),
    // The compact group field is applied after merged runtime settings by the
    // controller. Keep it as the final layer here so Apply to groups copies the
    // source group's true effective iteration value.
    sourceIterations === undefined ? undefined : { iterations: sourceIterations },
  ].filter((layer): layer is RuntimeSettingsOverride => Boolean(layer))

  const materialized = layers.reduce(applyEffectiveRuntimeLayer, {} as RuntimeSettingsOverride)
  const draft = runtimeDraftFromOverride(materialized)
  return draft && !isRuntimeDraftEmpty(draft) ? draft : undefined
}

interface RuntimeApplyGroup {
  script_id: string
  iterations?: number
  runtimeSettings?: ScenarioRuntimeSettingsDraft
}

type RuntimeApplyResult<T extends RuntimeApplyGroup> = Omit<T, 'runtimeSettings'> & RuntimeApplyGroup

function findScriptById(scripts: StoredScript[], scriptId?: string) {
  if (!scriptId) return undefined
  return scripts.find(script => script.id === scriptId)
}

export function applyEffectiveRuntimeSettingsToGroups<T extends RuntimeApplyGroup>(
  groups: T[],
  scripts: StoredScript[],
  engineDefaults: ScriptConfig | undefined,
  scenarioDraft: ScenarioRuntimeSettingsDraft | undefined,
  sourceIndex: number,
  targetIndexes: number[],
): RuntimeApplyResult<T>[] {
  const sourceGroup = groups[sourceIndex]
  if (!sourceGroup) return groups
  const sourceScriptConfig = findScriptById(scripts, sourceGroup.script_id)?.script?.config
  const effectiveSource = materializeRuntimeSettingsForApply(
    engineDefaults,
    sourceScriptConfig,
    scenarioDraft,
    sourceGroup.runtimeSettings,
    sourceGroup.iterations,
  )

  return groups.map<RuntimeApplyResult<T>>((group, groupIndex) => {
    if (!targetIndexes.includes(groupIndex)) return group
    const protocol = findScriptById(scripts, group.script_id)?.protocol
    const targetEnv = group.runtimeSettings?.env
    const targetRedactionRules = group.runtimeSettings?.redaction_rules
    const copied = effectiveSource ? copyRuntimeSettingsForProtocol(effectiveSource, protocol) : undefined
    const protectedTargetFields = targetEnv !== undefined || targetRedactionRules !== undefined
      ? {
          log_level: '',
          auto_transactions: '',
          env: targetEnv === undefined ? undefined : structuredClone(targetEnv),
          redaction_rules: targetRedactionRules === undefined ? undefined : structuredClone(targetRedactionRules),
        }
      : undefined
    return {
      ...group,
      // The copied runtime profile now owns the effective iteration value.
      // Clear the compact target field so serialization cannot emit two
      // conflicting representations.
      iterations: undefined,
      runtimeSettings: copied
        ? { ...copied, env: protectedTargetFields?.env, redaction_rules: protectedTargetFields?.redaction_rules }
        : protectedTargetFields,
    }
  })
}
