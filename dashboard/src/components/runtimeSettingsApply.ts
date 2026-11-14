import { type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft, type StoredScript } from '../types'
import { type ScriptConfig } from '../types/scripts'
import { type PartialRuntimeSettingsOverride, type RuntimeProtocolSectionKey } from '../types/runtimeSettings'
import {
  RUNTIME_PROTOCOL_OVERRIDE_SECTIONS,
  effectiveRuntimeValue,
  isPartialRuntimeDraft,
  isRuntimeProtocolSectionApplicable,
  isRuntimeDraftEmpty,
  isRuntimeFieldApplicable,
  overrideFieldValue,
  resetOverrideField,
  runtimeDraftFromOverride,
  runtimeOverrideFieldPaths,
  runtimeOverridePathWireSegments,
  runtimeOverrideFromScriptConfig,
  runtimeOverrideMapParent,
  runtimeOverrideRowFromDraft,
  runtimeRowMapKeys,
  setOverrideField,
  upgradeRuntimeDraftToPartial,
} from './runtimeSettingsModel'
import {
  protocolCredentialFieldPaths,
  protocolUncopyableCredentialPaths,
} from './runtime-settings/protocolCredentialLeaves'

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
  if (!isRuntimeFieldApplicable('websocket', protocol)) copy.websocket = undefined
  if (!isRuntimeFieldApplicable('web_runtime', protocol)) copy.web_runtime = undefined
  if (!isRuntimeFieldApplicable('java_vuser', protocol)) copy.java_vuser = undefined
  // runtimeProtocolFieldApplies: a native protocol section reaches only its own protocol.
  for (const section of RUNTIME_PROTOCOL_OVERRIDE_SECTIONS) {
    if (!isRuntimeProtocolSectionApplicable(section, protocol)) copy[section] = undefined
  }
  if ((protocol || '').trim().toLowerCase() !== 'soap') {
    copy.soap = undefined
  } else if (copy.soap?.ws_security?.username_token) {
    // Like keepTargetSOAPCredentials: a copy never carries another group's
    // WS-Security credentials (a masked value would not reconcile against the
    // target's own row). keepTargetSOAPCredentials below decides what the
    // target row lists, so the target keeps its own credentials.
    const token = { ...copy.soap.ws_security.username_token }
    delete token.username
    delete token.password
    copy.soap = { ...copy.soap, ws_security: { ...copy.soap.ws_security, username_token: token } }
  }
  return copy
}

const SOAP_CREDENTIAL_FIELD_PATHS = [
  'soap.ws_security.username_token.username',
  'soap.ws_security.username_token.password',
] as const

/**
 * keepTargetSOAPCredentials mirrors the backend helper of the same name
 * (handlers_scenario_runtime.go). A copied row whose SOAP section carries a
 * UsernameToken keeps only the credentials the target's own prior row listed.
 * Every other credential stays inherited from the target's script or the
 * scenario root: the row is converted to a partial row whose SOAP paths leave
 * those credential leaves out, because a v1 SOAP section replaces the
 * inherited section whole and would send an empty UsernameToken.
 */
function keepTargetSOAPCredentials(
  row: ScenarioRuntimeSettingsDraft,
  prior: ScenarioRuntimeSettingsDraft | undefined,
): ScenarioRuntimeSettingsDraft {
  const token = row.soap?.ws_security?.username_token
  if (!row.soap || !token) return row
  const ownCredential = (path: string) => {
    const value = overrideFieldValue(prior, path)
    return typeof value === 'string' ? value : ''
  }
  const [usernamePath, passwordPath] = SOAP_CREDENTIAL_FIELD_PATHS
  const username = ownCredential(usernamePath)
  const password = ownCredential(passwordPath)
  const nextToken = { ...token }
  delete nextToken.username
  delete nextToken.password
  if (username) nextToken.username = username
  if (password) nextToken.password = password
  let next: ScenarioRuntimeSettingsDraft = {
    ...row,
    soap: { ...row.soap, ws_security: { ...row.soap.ws_security, username_token: nextToken } },
  }
  const inherited = [...(username ? [] : [usernamePath]), ...(password ? [] : [passwordPath])]
  if (inherited.length === 0) return next
  // upgradeRuntimeDraftToPartial lists what the v1 row applied (dropping the
  // aliases v1 shadows); each reset splits the listed SOAP section into its
  // remaining leaves, as overrideFieldPathsExcluding does.
  next = upgradeRuntimeDraftToPartial(next)
  for (const path of inherited) next = resetOverrideField(next, path)
  return next
}

/**
 * keepTargetProtocolCredentials mirrors the backend helper of the same name
 * (runtime_protocol_sections.go): a copied native protocol section never
 * carries the source's credential leaves. A credential path the target's own
 * prior row set is kept; every other one is left out of the row (which then
 * lists the section's remaining paths), so the target keeps inheriting it
 * from its script or the scenario root.
 */
function keepTargetProtocolCredentials(
  row: ScenarioRuntimeSettingsDraft,
  prior: ScenarioRuntimeSettingsDraft | undefined,
  section: RuntimeProtocolSectionKey,
  inheritedTargets: { script?: ScriptConfig; scenario?: ScenarioRuntimeSettingsDraft },
): ScenarioRuntimeSettingsDraft {
  if (row[section] === undefined) return row
  const paths = protocolCredentialFieldPaths(row, section)
  if (paths.length === 0) return row
  let next = upgradeRuntimeDraftToPartial(row)
  const inherited: string[] = []
  for (const path of paths) {
    const own = overrideFieldValue(prior, path)
    if (own !== undefined && own !== '') {
      next = setOverrideField(next, path, own)
    } else {
      next = resetOverrideField(next, path)
      inherited.push(path)
    }
  }
  // The copy still replaces every other value of a map it now lists key by key
  // (overrideFieldPathsExcludingValue): each key the target's prior row, its
  // script or the scenario root holds there, and the copy does not, is listed
  // with no value, so it is cleared exactly as the whole-section copy clears it.
  const maps = new Set(inherited.map(runtimeOverrideMapParent).filter((map): map is string => Boolean(map)))
  for (const map of maps) {
    const copied = new Set(runtimeRowMapKeys(next, map))
    const scriptMap = effectiveRuntimeValue(map, { script: inheritedTargets.script }).value
    const targetKeys = [
      ...runtimeRowMapKeys(prior, map),
      ...(scriptMap !== null && typeof scriptMap === 'object' && !Array.isArray(scriptMap) ? Object.keys(scriptMap) : []),
      ...runtimeRowMapKeys(inheritedTargets.scenario, map),
    ]
    for (const key of new Set(targetKeys)) {
      const keyPath = `${map}.${key}`
      if (copied.has(key) || inherited.includes(keyPath)) continue
      next = setOverrideField(next, keyPath, undefined)
    }
  }
  return next
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
  if (override.data_formatting?.auto_detect !== undefined || (override.data_formatting?.chain?.length ?? 0) > 0) {
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
  // Mirrors applyJavaVuserRuntimeOverride: choosing dedicated also drops the
  // per-JVM count, which applies only to shared JVMs and thread mode.
  if (override.jvm_sharing) {
    merged.jvm_sharing = override.jvm_sharing
    if (override.jvm_sharing === 'dedicated') delete merged.vusers_per_jvm
  }
  if (override.vusers_per_jvm) merged.vusers_per_jvm = override.vusers_per_jvm
  if (override.startup_timeout_ms) merged.startup_timeout_ms = override.startup_timeout_ms
  if (override.action_timeout_ms) merged.action_timeout_ms = override.action_timeout_ms
  if (override.param_left_brace) merged.param_left_brace = override.param_left_brace
  if (override.param_right_brace) merged.param_right_brace = override.param_right_brace
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

function setNested(target: Record<string, unknown>, segments: string[], value: unknown) {
  let current = target
  for (const segment of segments.slice(0, -1)) {
    const child = current[segment]
    // applyConfigOverrideField: a listed leaf with no value under a block no
    // layer has (no Kafka OAuth, no JMS TLS) copies that absence instead of
    // allocating an empty block that validation would read as configured.
    if (value === undefined && (child === undefined || child === null)) return
    if (child === null || typeof child !== 'object' || Array.isArray(child)) current[segment] = {}
    current = current[segment] as Record<string, unknown>
  }
  const leaf = segments[segments.length - 1]
  if (value === undefined) delete current[leaf]
  else current[leaf] = structuredClone(value)
}

/**
 * Lay a partial (override_fields) layer over the accumulated profile: only the
 * paths it lists replace what lower layers supplied, mirroring
 * applyRuntimeOverridesPartial. A listed path with no value overrides to empty.
 */
function applyPartialRuntimeLayer(base: RuntimeSettingsOverride, layer: PartialRuntimeSettingsOverride): RuntimeSettingsOverride {
  const next = structuredClone(base) as PartialRuntimeSettingsOverride
  const fields = runtimeOverrideFieldPaths(layer)
  // Fold the compact aliases a lower layer carries into their sections, so a
  // listed leaf lands where it executes (the section wins in v1 layers).
  const foldAliases = (section: string) => {
    if (section === 'run_logic' && (next.iterations !== undefined || next.auto_transactions)) {
      next.run_logic = {
        ...(next.iterations !== undefined ? { iterations: next.iterations } : {}),
        ...(next.auto_transactions ? { auto_transactions: next.auto_transactions } : {}),
        ...next.run_logic,
      }
      next.iterations = undefined
      next.auto_transactions = undefined
    }
    if (section === 'log' && next.log_level) {
      next.log = { level: next.log_level, ...next.log }
      next.log_level = undefined
    }
    if (section === 'browser' && next.browser) {
      next.browser_config = next.browser_config ? overlayBrowserLegacy(next.browser_config, next.browser) : { ...next.browser }
      next.browser = undefined
    }
  }
  for (const path of fields) {
    const value = overrideFieldValue(layer, path)
    if (path === 'env') {
      // A listed env with no keys drops the lower layers' env (mergeRuntimeSettingsPartial
      // sets merged.Env to nil for it); otherwise the keys merge over them.
      if (value && Object.keys(value as object).length > 0) next.env = { ...next.env, ...(value as Record<string, string>) }
      else next.env = undefined
      continue
    }
    if (path.startsWith('env.')) {
      const key = path.slice('env.'.length)
      const env = { ...next.env }
      if (value === undefined) delete env[key]
      else env[key] = value as string
      next.env = env
      continue
    }
    if (path === 'redaction_rules') {
      next.redaction_rules = structuredClone(value as RuntimeSettingsOverride['redaction_rules'])
      continue
    }
    if (path === 'java_vuser') {
      // A listed java_vuser with no value resets the JVM settings to zero
      // (applyJavaVuserOverrideField); a value overlays field by field.
      next.java_vuser = value === undefined ? {} : mergeJavaVuserLayer(next.java_vuser, value as MaterializedJavaVuser) ?? next.java_vuser
      continue
    }
    if (path.startsWith('java_vuser.')) {
      const field = path.slice('java_vuser.'.length) as keyof MaterializedJavaVuser
      const java: Record<string, unknown> = { ...next.java_vuser }
      if (value === undefined) delete java[field]
      else java[field] = structuredClone(value)
      if (field === 'jvm_sharing' && value !== 'shared' && value !== 'threads') delete java.vusers_per_jvm
      next.java_vuser = java as MaterializedJavaVuser
      continue
    }
    const section = path.split('.')[0]
    foldAliases(section)
    if (section === 'base_url') {
      next.base_url = value as string | undefined
      continue
    }
    if ((section === 'pacing' || section === 'think_time' || section === 'error_handling') && next[section] === null) {
      next[section] = undefined
    }
    const segments = runtimeOverridePathWireSegments(path)
    if (segments) setNested(next as Record<string, unknown>, segments, value)
  }
  return next
}

/** Mirror mergeRuntimeSettings/applyRuntimeOverrides section precedence. */
function applyEffectiveRuntimeLayer(base: RuntimeSettingsOverride, layer: RuntimeSettingsOverride): RuntimeSettingsOverride {
  if (isPartialRuntimeDraft(layer as PartialRuntimeSettingsOverride)) {
    return applyPartialRuntimeLayer(base, layer as PartialRuntimeSettingsOverride)
  }
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
  if (layer.results) next.results = structuredClone(layer.results)
  if (layer.websocket) next.websocket = structuredClone(layer.websocket)
  if (layer.http) next.http = structuredClone(layer.http)
  const soap = (layer as PartialRuntimeSettingsOverride).soap
  if (soap) (next as PartialRuntimeSettingsOverride).soap = structuredClone(soap)
  // Whole-section replace, mirroring the Go controller's browser_emulation merge.
  if (layer.browser_emulation) next.browser_emulation = structuredClone(layer.browser_emulation)
  if (layer.web_runtime) next.web_runtime = mergeMaterializedWeb(next.web_runtime, layer.web_runtime)
  if (layer.java_vuser) next.java_vuser = mergeJavaVuserLayer(next.java_vuser, layer.java_vuser)
  // applyRuntimeProtocolSections: each native protocol section a v1 layer carries replaces the lower one whole.
  for (const section of RUNTIME_PROTOCOL_OVERRIDE_SECTIONS) {
    const value = (layer as PartialRuntimeSettingsOverride)[section]
    if (value !== undefined && value !== null) Object.assign(next, { [section]: structuredClone(value) })
  }

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
    // The rows the drafts are stored as, so a v1 section applied whole (an untyped
    // pacing, `{}` error_handling) pins its zero value here exactly as it does in
    // partialRuntimeState and at run time.
    runtimeOverrideRowFromDraft(scenarioDraft) as RuntimeSettingsOverride | undefined,
    runtimeOverrideRowFromDraft(sourceDraft) as RuntimeSettingsOverride | undefined,
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

/** What Apply to groups did not copy, so the apply panel can say why. */
export interface RuntimeApplyReport {
  /** Targets left unchanged: the source runs with SOAP WS-Security credentials,
   * the target has none of its own, and credentials are never copied. */
  missingSOAPCredentialTargets: number[]
  /** Targets left unchanged because a copied protocol section keeps a
   * credential inside a list (a Kafka broker with userinfo): leaving it out
   * would drop the whole list, and credentials are never copied. */
  uncopyableCredentialTargets?: Array<{ target: number; paths: string[] }>
}

function hasSOAPCredentials(settings?: ScenarioRuntimeSettingsDraft): boolean {
  const token = settings?.soap?.ws_security?.username_token
  return Boolean(token && (token.username || token.password))
}

/**
 * applyEffectiveRuntimeSettingsToGroupsWithReport copies the source group's
 * effective runtime to each target, mirroring the backend applyRuntimeToGroups
 * (handlers_scenario_runtime.go): when the source runs with a SOAP UsernameToken
 * and a target would end up with no credentials of its own (row, script or
 * scenario root), that target is left unchanged and reported instead of being
 * written with an empty UsernameToken that fails auth at run time.
 */
export function applyEffectiveRuntimeSettingsToGroupsWithReport<T extends RuntimeApplyGroup>(
  groups: T[],
  scripts: StoredScript[],
  engineDefaults: ScriptConfig | undefined,
  scenarioDraft: ScenarioRuntimeSettingsDraft | undefined,
  sourceIndex: number,
  targetIndexes: number[],
): { groups: RuntimeApplyResult<T>[]; report: RuntimeApplyReport } {
  const report: RuntimeApplyReport = { missingSOAPCredentialTargets: [], uncopyableCredentialTargets: [] }
  const sourceGroup = groups[sourceIndex]
  if (!sourceGroup) return { groups, report }
  const sourceScriptConfig = findScriptById(scripts, sourceGroup.script_id)?.script?.config
  const effectiveSource = materializeRuntimeSettingsForApply(
    engineDefaults,
    sourceScriptConfig,
    scenarioDraft,
    sourceGroup.runtimeSettings,
    sourceGroup.iterations,
  )
  const sourceHasSOAPCredentials = hasSOAPCredentials(effectiveSource)

  const next = groups.map<RuntimeApplyResult<T>>((group, groupIndex) => {
    if (!targetIndexes.includes(groupIndex)) return group
    const targetScript = findScriptById(scripts, group.script_id)
    const protocol = targetScript?.protocol
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
    let runtimeSettings: ScenarioRuntimeSettingsDraft | undefined = copied
      ? keepTargetSOAPCredentials(
          { ...copied, env: protectedTargetFields?.env, redaction_rules: protectedTargetFields?.redaction_rules },
          group.runtimeSettings,
        )
      : protectedTargetFields
    // A partial target lists its env keys (and redaction rules) as paths, an env.KEY with no
    // value removing a lower layer's key. Re-protecting them as plain v1 values would merge the
    // removed key back in, so the copy is upgraded and the target's own paths re-applied.
    const targetEnvPaths = isPartialRuntimeDraft(group.runtimeSettings)
      ? runtimeOverrideFieldPaths(group.runtimeSettings).filter(path => path === 'env' || path.startsWith('env.') || path === 'redaction_rules')
      : []
    if (runtimeSettings) {
      const blocked = RUNTIME_PROTOCOL_OVERRIDE_SECTIONS.flatMap(section => protocolUncopyableCredentialPaths(runtimeSettings, section))
      if (blocked.length > 0) {
        report.uncopyableCredentialTargets?.push({ target: groupIndex, paths: blocked })
        return group
      }
      for (const section of RUNTIME_PROTOCOL_OVERRIDE_SECTIONS) {
        runtimeSettings = keepTargetProtocolCredentials(runtimeSettings, group.runtimeSettings, section, {
          script: targetScript?.script?.config,
          scenario: scenarioDraft,
        })
      }
    }
    if (targetEnvPaths.length > 0) {
      let partial = upgradeRuntimeDraftToPartial(runtimeSettings ? { ...runtimeSettings, env: undefined, redaction_rules: undefined } : undefined)
      partial = resetOverrideField(resetOverrideField(partial, 'env'), 'redaction_rules')
      for (const path of targetEnvPaths) partial = setOverrideField(partial, path, overrideFieldValue(group.runtimeSettings, path))
      runtimeSettings = partial
    }
    if (sourceHasSOAPCredentials && copied?.soap) {
      const targetEffective = materializeRuntimeSettingsForApply(
        engineDefaults,
        targetScript?.script?.config,
        scenarioDraft,
        runtimeSettings,
      )
      if (!hasSOAPCredentials(targetEffective)) {
        report.missingSOAPCredentialTargets.push(groupIndex)
        return group
      }
    }
    return {
      ...group,
      // The copied runtime profile now owns the effective iteration value.
      // Clear the compact target field so serialization cannot emit two
      // conflicting representations.
      iterations: undefined,
      runtimeSettings,
    }
  })
  return { groups: next, report }
}

export function applyEffectiveRuntimeSettingsToGroups<T extends RuntimeApplyGroup>(
  groups: T[],
  scripts: StoredScript[],
  engineDefaults: ScriptConfig | undefined,
  scenarioDraft: ScenarioRuntimeSettingsDraft | undefined,
  sourceIndex: number,
  targetIndexes: number[],
): RuntimeApplyResult<T>[] {
  return applyEffectiveRuntimeSettingsToGroupsWithReport(groups, scripts, engineDefaults, scenarioDraft, sourceIndex, targetIndexes).groups
}
