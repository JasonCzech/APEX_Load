import PropertyAnalyticsFields from './PropertyAnalyticsFields'
import { MessageDetailFields } from './MessageDetailFields'
import { HTTPReplayPolicyFields } from './HTTPReplayPolicyFields'
import ThinkTimePercentageInputs from './ThinkTimePercentageInputs'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import { useId, useState } from 'react'

import { type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft } from '../types'
import { type ScriptActionDefinition, type ScriptConfig } from '../types/scripts'
import { inheritedPlaceholder as inh } from './runtimeFieldParsers'
import {
  EMPTY_RUNTIME_DRAFT,
  HTTP_VERSION_OPTIONS,
  formatEffectiveRuntimeField,
  isRuntimeDraftEmpty,
  isRuntimeFieldApplicable,
  overrideFromRuntimeDraft,
  runtimeDraftFromOverride,
  runtimeOverrideFromScriptConfig,
  RUNTIME_FIELD_SOURCE_LABEL,
  type RuntimeFieldKey,
} from './runtimeSettingsModel'
import { editReplacementSection, materializeRuntimeSettingsForApply } from './runtimeSettingsApply'
import { useBrowserEmulationCatalog } from '../hooks/useBrowserEmulationCatalog'
import WebHttpTab from './runtimeTabs/WebHttpTab'
import { JavaEnvSettingsFields, type JavaEnvSettingsValue } from './JavaEnvSettingsFields'
import { type RuntimeJavaVuserSettings } from '../types'
import {
  canonicalThinkTimeType,
  normalizeThinkTimeMultiplier,
  thinkTimeConfigForType,
  thinkTimeUsesBounds,
  thinkTimeUsesMean,
  thinkTimeUsesStdDeviation,
} from './thinkTimeConfig'
import ThinkTimeMultiplierInput from './ThinkTimeMultiplierInput'
import { formatMillisecondsAsSecondsValue } from './runtimeTiming'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import HttpRequestDefaultsEditor, {
  SocketCompatibilityPanel,
} from './HttpRequestDefaultsEditor'
import {
  countHTTPDefaultHeaders,
  countHTTPDefaultHeaderErrors,
  countPinnedCookies,
  countPinnedCookieErrors,
  formatHTTPSettingsSummary,
} from './httpRequestDefaultsModel'

export interface RuntimeEditorGroup {
  name: string
  scriptId: string
  protocol?: string
  /** The group's script DSL config, used to resolve actual script-default values. */
  scriptConfig?: ScriptConfig
  /** Script requests used by the effective-header precedence inspector. */
  scriptActions?: ScriptActionDefinition[]
  /** VM-parameter tokens captured from an imported LoadRunner Java script, offered as a picker. */
  importedJavaVMParameters?: string[]
}

export interface RuntimeSettingsGroupEditorProps {
  /** Identity of the surrounding project/scenario resource. */
  draftScope?: string
  groups: RuntimeEditorGroup[]
  focusedIndex: number
  onFocusChange: (index: number) => void
  /** Scenario-wide defaults (the baseline every group inherits). */
  scenarioDefault?: RuntimeSettingsOverride
  /** Engine config defaults (from GET /api/v1/dsl/config-defaults) — the lowest
   *  inherited layer, so the effective value is shown even for unset fields. */
  engineDefaults?: ScriptConfig
  /** Per-group overrides, aligned to `groups`. */
  overrides: Array<ScenarioRuntimeSettingsDraft | undefined>
  onChange: (index: number, next: ScenarioRuntimeSettingsDraft | undefined) => void
  onApplyToGroups: (sourceIndex: number, targetIndexes: number[]) => void
  /** Overrides the panel heading (single-target mounts such as the scenario root). */
  title?: string
  description?: string
  /** Hides the Apply-to-groups panel — for mounts that edit one settings object. */
  hideApplyPanel?: boolean
  hideGroupSelector?: boolean
  overrideLabel?: string
  /** Section applicability protocols. A scenario- or test-wide mount passes every
   *  member protocol so a section applicable to at least one group stays editable;
   *  unset falls back to the focus group's own protocol. */
  applicableProtocols?: string[]
}

const FIELD_LABELS: Record<RuntimeFieldKey, string> = {
  results: 'Diagnostics',
  iterations: 'Iterations',
  pacing: 'Pacing',
  think_time: 'Think time',
  browser: 'Browser device',
  log_level: 'Log level',
  auto_transactions: 'Auto-transactions',
  error_handling: 'Error handling',
  log: 'Logging',
  run_logic: 'Run logic',
  http: 'HTTP transport',
  websocket: 'WebSocket',
  web_runtime: 'Web runtime',
  browser_config: 'Browser config',
  browser_emulation: 'Browser emulation',
  java_vuser: 'Java Env',
}

const SUMMARY_FIELDS: RuntimeFieldKey[] = [
  'results',
  'iterations',
  'pacing',
  'think_time',
  'log_level',
  'auto_transactions',
  'error_handling',
  'browser',
  'log',
  'run_logic',
  'http',
  'websocket',
  'web_runtime',
  'browser_config',
  'browser_emulation',
  'java_vuser',
]

/**
 * The horizontal tabs shown below the Focus-group dropdown. Tab 1 (Summary) is the
 * effective-value table; the rest mirror the Script Editor's Runtime Settings tabs.
 * `summary` plus the universal sections always show; `http_transport`/`web_http` are
 * gated by protocol applicability and hidden when not applicable.
 */
type RuntimeTabId =
  | 'summary'
  | 'think_time'
  | 'pacing'
  | 'run_logic'
  | 'error_handling'
  | 'http_transport'
  | 'browser_emulation'
  | 'web_http'
  | 'java_env'

/** Nested sub-tabs within the HTTP Transport tab (mirrors the Script Editor). */
type HttpSubTabId = 'request_defaults' | 'session_cache' | 'timeouts' | 'connection' | 'tls' | 'proxy' | 'authentication' | 'browser_emulation'

type HTTPSettings = NonNullable<ScenarioRuntimeSettingsDraft['http']>
type BrowserSettings = NonNullable<ScenarioRuntimeSettingsDraft['browser_config']>
type BrowserEmulationSettings = NonNullable<ScenarioRuntimeSettingsDraft['browser_emulation']>
type ErrorHandlingSettings = NonNullable<RuntimeSettingsOverride['error_handling']>
type LogSettings = NonNullable<RuntimeSettingsOverride['log']>
type RunLogicSettings = NonNullable<RuntimeSettingsOverride['run_logic']>
type ThinkTimeSettings = NonNullable<RuntimeSettingsOverride['think_time']>
type PacingSettings = NonNullable<RuntimeSettingsOverride['pacing']>

function groupLabel(group: RuntimeEditorGroup, index: number): string {
  return group.name || `Group ${index + 1}`
}

export default function RuntimeSettingsGroupEditor({
  draftScope,
  groups,
  focusedIndex,
  onFocusChange,
  scenarioDefault,
  engineDefaults,
  overrides,
  onChange,
  onApplyToGroups,
  title,
  description,
  hideApplyPanel,
  hideGroupSelector,
  overrideLabel,
  applicableProtocols,
}: RuntimeSettingsGroupEditorProps) {
  const tabPrefix = useId()
  const [applyOpen, setApplyOpen] = useState(false)
  const [applyTargets, setApplyTargets] = useState<number[]>([])
  const [rawActiveTab, setRawActiveTab] = useState<RuntimeTabId>('summary')
  const [rawHttpSubTab, setRawHttpSubTab] = useState<HttpSubTabId>('request_defaults')

  // Derived above the empty-groups early return below, because the catalog hook
  // that depends on them must run on every render (Rules of Hooks).
  const index = groups.length > 0 ? Math.min(focusedIndex, groups.length - 1) : 0
  const focusGroup = groups[index]
  // Scenario-/test-wide mounts pass every member protocol; per-group mounts use
  // the focus group's own protocol.
  const fieldApplicable = (field: RuntimeFieldKey): boolean => {
    if (applicableProtocols && applicableProtocols.length > 0) {
      return applicableProtocols.some(protocol => isRuntimeFieldApplicable(field, protocol))
    }
    return isRuntimeFieldApplicable(field, focusGroup?.protocol)
  }
  // Live catalog (bundled fallback until it resolves) so this editor offers the
  // same browsers the engine can actually resolve — see useBrowserEmulationCatalog.
  const browserEmulationCatalog = useBrowserEmulationCatalog(fieldApplicable('browser_emulation'))

  if (groups.length === 0) {
    return (
      <div className="scenario-subpanel">
        <div className="scenario-assignment-header">
          <div>
            <h4>Per-group runtime overrides</h4>
            <p>Add a script group to layer per-group runtime overrides on top of the scenario defaults.</p>
          </div>
        </div>
      </div>
    )
  }

  const draft = overrides[index] ?? EMPTY_RUNTIME_DRAFT
  const groupOverride = overrideFromRuntimeDraft(overrides[index])
  const scriptDefault = runtimeOverrideFromScriptConfig(focusGroup.scriptConfig)
  const engineDefault = runtimeOverrideFromScriptConfig(engineDefaults)

  // Resolve the inherited profile through the same precedence rules as the Go
  // controller. Most runtime sections replace as a whole; web_runtime is the sole
  // section whose populated top-level units overlay the lower layer.
  const inheritedDraft = materializeRuntimeSettingsForApply(
    engineDefaults,
    focusGroup.scriptConfig,
    runtimeDraftFromOverride(scenarioDefault),
  )
  const inheritedOverride = overrideFromRuntimeDraft(inheritedDraft)
  const inheritedHttp: HTTPSettings = structuredClone(inheritedOverride?.http ?? {})
  const inheritedBrowser: BrowserSettings = structuredClone(
    inheritedOverride?.browser_config ?? inheritedOverride?.browser ?? {},
  )
  const inheritedThink: Partial<ThinkTimeSettings> = structuredClone(inheritedOverride?.think_time ?? {})
  const inheritedPacing: Partial<PacingSettings> = structuredClone(inheritedOverride?.pacing ?? {})
  const inheritedError: ErrorHandlingSettings = structuredClone(inheritedOverride?.error_handling ?? {})
  // Continue-on-error follows the scenario policy rather than the collapsed
  // inheritance chain: inside a scenario it is enabled unless a layer explicitly
  // disables it, so the engine's stop-on-error default never applies here. This
  // mirrors resolveGroupContinueOnError on the controller — only an explicit
  // scenario or script value displaces the enabled-by-default policy, and the
  // group's own override (currentError) still sits above this baseline.
  inheritedError.continue_on_error =
    scenarioDefault?.error_handling?.continue_on_error
    ?? focusGroup.scriptConfig?.error_handling?.continue_on_error
    ?? true
  const inheritedLog: LogSettings = structuredClone(
    inheritedOverride?.log ?? (inheritedOverride?.log_level ? { level: inheritedOverride.log_level } : {}),
  )
  const inheritedRunLogic: RunLogicSettings = structuredClone(
    inheritedOverride?.run_logic ?? {
      iterations: inheritedOverride?.iterations,
      auto_transactions: inheritedOverride?.auto_transactions,
    },
  )
  const inheritedWeb = structuredClone(inheritedOverride?.web_runtime ?? {})
  const inheritedIterations = inheritedRunLogic.iterations ?? inheritedOverride?.iterations

  const flatCurrentError: ErrorHandlingSettings = {
    continue_on_error: draft.continue_on_error,
    snapshot_on_error: draft.snapshot_on_error,
    max_retries: draft.max_retries,
    retry_delay_ms: draft.retry_delay_ms,
    retry_backoff: draft.retry_backoff || undefined,
  }
  const hasCurrentError = Object.values(flatCurrentError).some(value => value !== undefined)
  const currentError = hasCurrentError ? flatCurrentError : undefined

  const hasLegacyLog = Boolean(draft.log_level)
  const currentLog: LogSettings | undefined = draft.log
    ? structuredClone(draft.log)
    : hasLegacyLog
      ? { ...structuredClone(inheritedLog), level: draft.log_level }
      : undefined

  const hasLegacyRunLogic = draft.iterations !== undefined || Boolean(draft.auto_transactions)
  const currentRunLogic: RunLogicSettings | undefined = draft.run_logic
    ? structuredClone(draft.run_logic)
    : hasLegacyRunLogic
      ? {
          ...structuredClone(inheritedRunLogic),
          ...(draft.iterations !== undefined ? { iterations: draft.iterations } : {}),
          ...(draft.auto_transactions ? { auto_transactions: draft.auto_transactions } : {}),
        }
      : undefined

  const hasLegacyBrowser = Boolean(draft.browser)
  const currentBrowser: BrowserSettings | undefined = draft.browser_config
    ? structuredClone(draft.browser_config)
    : hasLegacyBrowser
      ? { ...structuredClone(inheritedBrowser), ...structuredClone(draft.browser) }
      : undefined

  const effectiveThink = draft.think_time ?? inheritedThink
  const effectiveError = currentError ?? inheritedError
  const effectiveLog = currentLog ?? inheritedLog
  const effectiveHttp = draft.http ?? inheritedHttp
  const effectiveBrowser = currentBrowser ?? inheritedBrowser

  const browserApplicable = fieldApplicable('browser')
  const httpApplicable = fieldApplicable('http')
  const webRuntimeApplicable = fieldApplicable('web_runtime')
  const javaVuserApplicable = fieldApplicable('java_vuser')
  const browserEmulationApplicable = fieldApplicable('browser_emulation')

  // Browser emulation replaces as a whole section (mirrors the Go controller).
  const inheritedBrowserEmulation: BrowserEmulationSettings = structuredClone(inheritedOverride?.browser_emulation ?? {})
  const effectiveBrowserEmulation: BrowserEmulationSettings = draft.browser_emulation ?? inheritedBrowserEmulation
  const selectedEmulationBrowser = browserEmulationCatalog.browsers.find(
    browser => browser.type === effectiveBrowserEmulation.browser_type,
  )

  // The Java Env override replaces the whole section: seed the editor from the
  // group override when present, else from the inherited (script/scenario) value.
  const inheritedJava: RuntimeJavaVuserSettings = structuredClone(inheritedOverride?.java_vuser ?? {})
  const effectiveJava: JavaEnvSettingsValue = draft.java_vuser ?? inheritedJava

  const patch = (next: Partial<ScenarioRuntimeSettingsDraft>) => {
    const merged: ScenarioRuntimeSettingsDraft = { ...EMPTY_RUNTIME_DRAFT, ...draft, ...next }
    onChange(index, isRuntimeDraftEmpty(merged) ? undefined : merged)
  }

  const editedHttp = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['http']>>) => {
    const hasCurrentSection = draft.http !== undefined
    const current = hasCurrentSection ? draft.http : inheritedHttp
    const topLevelNext = { ...next }
    delete topLevelNext.tls
    delete topLevelNext.proxy
    delete topLevelNext.auth
    const edited = editReplacementSection<HTTPSettings>(current, topLevelNext, inheritedHttp) as HTTPSettings

    if (Object.prototype.hasOwnProperty.call(next, 'tls')) {
      const tls = editReplacementSection(
        hasCurrentSection ? (draft.http?.tls ?? {}) : inheritedHttp.tls,
        next.tls,
        inheritedHttp.tls,
      )
      if (Object.keys(tls).length > 0) edited.tls = tls
      else delete edited.tls
    }
    if (Object.prototype.hasOwnProperty.call(next, 'proxy')) {
      const proxy = editReplacementSection(
        hasCurrentSection ? (draft.http?.proxy ?? {}) : inheritedHttp.proxy,
        next.proxy,
        inheritedHttp.proxy,
      )
      if (Object.keys(proxy).length > 0) edited.proxy = proxy
      else delete edited.proxy
    }
    if (Object.prototype.hasOwnProperty.call(next, 'auth')) {
      const auth = editReplacementSection(
        hasCurrentSection ? (draft.http?.auth ?? {}) : inheritedHttp.auth,
        next.auth,
        inheritedHttp.auth,
      )
      if (Object.keys(auth).length > 0) edited.auth = auth
      else delete edited.auth
    }
    return edited
  }

  const patchHttp = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['http']>>) => {
    patch({ http: editedHttp(next) })
  }

  // think_time / pacing carry the full DSL config. Clearing routes through the
  // type select (Inherit), which sets the whole section undefined.
  const patchThinkTime = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['think_time']>>) => {
    const edited = editReplacementSection(draft.think_time, next, inheritedThink)
    patch({ think_time: thinkTimeConfigForType({ ...edited, type: effectiveThink.type }, edited.type || 'fixed') })
  }

  const patchPacing = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['pacing']>>) => {
    const edited = editReplacementSection(draft.pacing, next, inheritedPacing)
    const effective = { type: edited.type || 'none', ...edited }
    patch({ pacing: effective })
  }

  const toggleLogOption = (option: string, on: boolean) => {
    const current = effectiveLog.extended_options ?? []
    const nextOptions = on ? Array.from(new Set([...current, option])) : current.filter(o => o !== option)
    patchLog({ extended_options: nextOptions })
  }

  // patchLog/patchRunLogic edit the full section, promoting the legacy single-field
  // values (log_level / iterations / auto_transactions) into the section and clearing
  // them — so a section override always carries every field (section-replace safe) and
  // the legacy shim is never double-emitted alongside it.
  const patchLog = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['log']>>) => {
    const effective = editReplacementSection(currentLog, next, inheritedLog)
    patch({ log: effective, log_level: '' })
  }

  const patchRunLogic = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['run_logic']>>) => {
    const effective = editReplacementSection(currentRunLogic, next, inheritedRunLogic)
    patch({ run_logic: effective, iterations: undefined, auto_transactions: '' })
  }

  const patchErrorHandling = (next: Partial<ErrorHandlingSettings>) => {
    const effective = editReplacementSection(currentError, next, inheritedError)
    patch({
      continue_on_error: effective.continue_on_error,
      snapshot_on_error: effective.snapshot_on_error,
      max_retries: effective.max_retries,
      retry_delay_ms: effective.retry_delay_ms,
      retry_backoff: effective.retry_backoff,
    })
  }

  const patchBrowserConfig = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['browser_config']>>) => {
    const hasCurrentSection = currentBrowser !== undefined
    const topLevelNext = { ...next }
    delete topLevelNext.viewport
    delete topLevelNext.review_policy
    const edited = editReplacementSection<BrowserSettings>(currentBrowser, topLevelNext, inheritedBrowser) as BrowserSettings

    if (Object.prototype.hasOwnProperty.call(next, 'viewport')) {
      const viewport = editReplacementSection(
        hasCurrentSection ? (currentBrowser?.viewport ?? {}) : inheritedBrowser.viewport,
        next.viewport,
        inheritedBrowser.viewport,
      )
      if (Object.keys(viewport).length > 0) edited.viewport = viewport
      else delete edited.viewport
    }
    if (Object.prototype.hasOwnProperty.call(next, 'review_policy')) {
      const reviewPolicy = editReplacementSection(
        hasCurrentSection ? (currentBrowser?.review_policy ?? {}) : inheritedBrowser.review_policy,
        next.review_policy,
        inheritedBrowser.review_policy,
      )
      if (Object.keys(reviewPolicy).length > 0) edited.review_policy = reviewPolicy
      else delete edited.review_policy
    }
    patch({ browser_config: edited, browser: undefined })
  }

  // Browser-emulation edits replace the whole section; the first edit
  // materializes the inherited section, mirroring patchBrowserConfig.
  const editedBrowserEmulation = (next: Partial<BrowserEmulationSettings>) => {
    const hasCurrentSection = draft.browser_emulation !== undefined
    const topLevelNext = { ...next }
    delete topLevelNext.cache
    const edited = editReplacementSection<BrowserEmulationSettings>(
      draft.browser_emulation,
      topLevelNext,
      inheritedBrowserEmulation,
    ) as BrowserEmulationSettings
    if (Object.prototype.hasOwnProperty.call(next, 'cache')) {
      const cache = editReplacementSection(
        hasCurrentSection ? (draft.browser_emulation?.cache ?? {}) : inheritedBrowserEmulation.cache,
        next.cache,
        inheritedBrowserEmulation.cache,
      )
      if (Object.keys(cache).length > 0) edited.cache = cache
      else delete edited.cache
    }
    return edited
  }
  const patchBrowserEmulation = (next: Partial<BrowserEmulationSettings>) => {
    patch({ browser_emulation: editedBrowserEmulation(next) })
  }
  const httpSettingsValue: HTTPSettings = {
    ...effectiveHttp,
    cache_enabled: effectiveHttp.cache_enabled ?? effectiveBrowserEmulation.cache?.simulate_cache,
  }
  const httpOriginLabel = draft.http
    ? 'Group override'
    : scenarioDefault?.http
      ? 'Scenario default'
      : focusGroup.scriptConfig?.http && Object.keys(focusGroup.scriptConfig.http).length > 0
        ? 'Script default'
        : 'Engine default'
  const webOriginLabel = draft.web_runtime?.additional_attributes
    ? 'Group override'
    : scenarioDefault?.web_runtime?.additional_attributes
      ? 'Scenario default'
      : focusGroup.scriptConfig?.web_runtime?.additional_attributes
        ? 'Script default'
        : 'Engine default'
  const effectiveSocketAttributes = {
    ...(inheritedWeb.additional_attributes || {}),
    ...(draft.web_runtime?.additional_attributes || {}),
  }
  const requestDefaultErrorCount = countHTTPDefaultHeaderErrors(httpSettingsValue)
  const pinnedCookieErrorCount = countPinnedCookieErrors(httpSettingsValue)
  const patchHTTPSettings = (next: Partial<HTTPSettings>) => {
    if (!Object.prototype.hasOwnProperty.call(next, 'cache_enabled')) {
      patchHttp(next)
      return
    }
    const cachePatch = next.cache_enabled
      ? { simulate_cache: true }
      : {
          simulate_cache: false,
          cache_content_urls: undefined,
          check_newer_versions: undefined,
        }
    patch({
      http: editedHttp(next),
      browser_emulation: editedBrowserEmulation({ cache: cachePatch }),
    })
  }

  const browserBoolValue = (a?: boolean, b?: boolean): string => {
    const value = a ?? b
    return value === undefined ? '' : value ? 'true' : 'false'
  }

  const clearField = (field: RuntimeFieldKey) => {
    switch (field) {
      case 'iterations': {
        // fieldHasOverride reports an override when the value lives in the
        // promoted run_logic section, so clearing must strip both the legacy
        // top-level field AND the section copy. When run_logic retains sibling
        // fields, its replacement semantics require us to materialize the inherited
        // iterations value; simply omitting it would reset duration behavior.
        const rl: Partial<RunLogicSettings> | undefined = draft.run_logic ? { ...draft.run_logic, iterations: undefined } : undefined
        const hasRunLogicSibling = Boolean(rl && Object.entries(rl).some(([key, value]) => key !== 'iterations' && value !== undefined))
        if (rl && hasRunLogicSibling && inheritedIterations !== undefined) rl.iterations = inheritedIterations
        patch({ iterations: undefined, run_logic: hasRunLogicSibling ? rl : undefined })
        break
      }
      case 'pacing':
        patch({ pacing: undefined })
        break
      case 'think_time':
        patch({ think_time: undefined })
        break
      case 'browser':
        patch({ browser: undefined })
        break
      case 'log_level': {
        const lg: Partial<LogSettings> | undefined = draft.log ? { ...draft.log, level: undefined } : undefined
        const hasLogSibling = Boolean(lg && Object.entries(lg).some(([key, value]) => key !== 'level' && value !== undefined))
        if (lg && hasLogSibling && inheritedLog.level !== undefined) lg.level = inheritedLog.level
        patch({ log_level: '', log: hasLogSibling ? lg : undefined })
        break
      }
      case 'auto_transactions': {
        const rl: Partial<RunLogicSettings> | undefined = draft.run_logic ? { ...draft.run_logic, auto_transactions: undefined } : undefined
        const hasRunLogicSibling = Boolean(rl && Object.entries(rl).some(([key, value]) => key !== 'auto_transactions' && value !== undefined))
        if (rl && hasRunLogicSibling && inheritedRunLogic.auto_transactions !== undefined) {
          rl.auto_transactions = inheritedRunLogic.auto_transactions
        }
        patch({ auto_transactions: '', run_logic: hasRunLogicSibling ? rl : undefined })
        break
      }
      case 'error_handling':
        patch({ continue_on_error: undefined, snapshot_on_error: undefined, max_retries: undefined, retry_delay_ms: undefined, retry_backoff: undefined })
        break
      case 'results':
        patch({ results: undefined })
        break
      case 'log':
        patch({ log: undefined, log_level: '' })
        break
      case 'run_logic':
        patch({ run_logic: undefined, iterations: undefined, auto_transactions: '' })
        break
      case 'http':
        patch({ http: undefined })
        break
      case 'websocket':
        patch({websocket: undefined})
        break
      case 'web_runtime':
        patch({ web_runtime: undefined })
        break
      case 'browser_config':
        patch({ browser_config: undefined, browser: undefined })
        break
      case 'browser_emulation':
        patch({ browser_emulation: undefined })
        break
      case 'java_vuser':
        patch({ java_vuser: undefined })
        break
    }
  }

  const fieldHasOverride = (field: RuntimeFieldKey): boolean => {
    const o = groupOverride
    if (!o) return false
    switch (field) {
      case 'iterations':
        // Per-VUser iterations only (legacy top-level or the promoted run_logic
        // section). global_iterations is a distinct fleet-wide shared cap shown via
        // the Run Logic row, not conflated into this per-VUser Iterations row.
        return o.iterations !== undefined || o.run_logic?.iterations !== undefined
      case 'pacing':
        return Boolean(o.pacing?.type)
      case 'think_time':
        return Boolean(o.think_time)
      case 'browser':
        return Boolean(o.browser)
      case 'log_level':
        return Boolean(o.log_level) || Boolean(o.log?.level)
      case 'auto_transactions':
        return Boolean(o.auto_transactions) || Boolean(o.run_logic?.auto_transactions)
      case 'error_handling':
        return Boolean(o.error_handling)
      case 'results':
        return Boolean(o.results)
      case 'log':
        return Boolean(o.log)
      case 'run_logic':
        return Boolean(o.run_logic)
      case 'http':
        return Boolean(o.http)
      case 'websocket':
        return Boolean(o.websocket)
      case 'web_runtime':
        return Boolean(o.web_runtime)
      case 'browser_config':
        return Boolean(o.browser_config)
      case 'browser_emulation':
        return Boolean(o.browser_emulation)
      case 'java_vuser':
        return Boolean(o.java_vuser)
    }
  }

  // A Java Env edit replaces the whole section. Materialize from the inherited
  // value on first edit so a single field change does not clear the others, then
  // drop the section entirely when it collapses back to the inherited value.
  const patchJavaVuser = (next: JavaEnvSettingsValue) => {
    const merged: RuntimeJavaVuserSettings = { ...next }
    for (const key of Object.keys(merged) as Array<keyof RuntimeJavaVuserSettings>) {
      if (merged[key] === undefined) delete merged[key]
    }
    patch({ java_vuser: Object.keys(merged).length > 0 ? merged : undefined })
  }

  const toggleTarget = (target: number) => {
    setApplyTargets(prev => (prev.includes(target) ? prev.filter(t => t !== target) : [...prev, target]))
  }

  const applyNow = () => {
    if (applyTargets.length > 0) {
      onApplyToGroups(index, applyTargets)
    }
    setApplyOpen(false)
    setApplyTargets([])
  }

  const pacingType = draft.pacing?.type || ''

  // Tab set: universal tabs always show; HTTP Transport (browser device folds in here)
  // and Web/HTTP are gated by protocol applicability and hidden when not applicable.
  const tabs: Array<{ id: RuntimeTabId; label: string; show: boolean }> = [
    { id: 'summary', label: 'Summary', show: true },
    { id: 'think_time', label: 'Think Time', show: true },
    { id: 'pacing', label: 'Iteration Pacing', show: true },
    { id: 'run_logic', label: 'Run Logic', show: true },
    { id: 'error_handling', label: 'Error Handling', show: true },
    { id: 'http_transport', label: 'HTTP Transport', show: httpApplicable || browserApplicable },
    { id: 'browser_emulation', label: 'Browser Emulation', show: browserEmulationApplicable },
    { id: 'web_http', label: 'Web/HTTP', show: webRuntimeApplicable },
    { id: 'java_env', label: 'Java Env', show: javaVuserApplicable },
  ]
  const visibleTabs = tabs.filter(tab => tab.show)
  // Clamp the active tab to a visible one so switching focus to a group that hides the
  // current tab (e.g. HTTP Transport on a non-HTTP group) falls back to Summary.
  const activeTab: RuntimeTabId = visibleTabs.some(tab => tab.id === rawActiveTab) ? rawActiveTab : 'summary'

  // HTTP Transport nested sub-tabs; Browser emulation only for browser groups.
  const httpSubTabs: Array<{ id: HttpSubTabId; label: string; show: boolean }> = [
    { id: 'request_defaults', label: 'Request Defaults', show: true },
    { id: 'session_cache', label: 'Session & Cache', show: true },
    { id: 'connection', label: 'Connection', show: true },
    { id: 'timeouts', label: 'Timeouts', show: true },
    { id: 'tls', label: 'TLS / SSL', show: true },
    { id: 'proxy', label: 'Proxy', show: true },
    { id: 'authentication', label: 'Authentication', show: true },
    { id: 'browser_emulation', label: 'Browser (Playwright)', show: browserApplicable },
  ]
  const visibleHttpSubTabs = httpSubTabs.filter(st => st.show)
  const httpSubTab: HttpSubTabId = visibleHttpSubTabs.some(st => st.id === rawHttpSubTab) ? rawHttpSubTab : 'request_defaults'

  const patchProxy = (next: Partial<NonNullable<NonNullable<ScenarioRuntimeSettingsDraft['http']>['proxy']>>) => {
    patchHttp({ proxy: { ...draft.http?.proxy, ...next } })
  }
  const patchTls = (next: Partial<NonNullable<NonNullable<ScenarioRuntimeSettingsDraft['http']>['tls']>>) => {
    patchHttp({ tls: { ...draft.http?.tls, ...next } })
  }
  const patchAuth = (next: Partial<NonNullable<NonNullable<ScenarioRuntimeSettingsDraft['http']>['auth']>>) => {
    patchHttp({ auth: { ...draft.http?.auth, ...next } })
  }

  const applyPanelAvailable = !hideApplyPanel && groups.length > 1
  return (
    <div className="scenario-subpanel">
      <div className="scenario-assignment-header">
        <div>
          <h4>{title || 'Per-group runtime overrides'}</h4>
          <p>{description || 'Set runtime for one group; unset fields inherit the script default. Use “Apply to groups” to copy a group’s settings to the others.'}</p>
        </div>
      </div>

      {!hideGroupSelector && groups.length > 1 && (
        <div className="scenario-browser-grid">
          <label>
            <span>Focus group</span>
            <select
              className="settings-input"
              value={index}
              onChange={event => onFocusChange(Number(event.target.value))}
              aria-label="Focus group"
            >
              {groups.map((group, groupIndex) => (
                <option key={`${group.scriptId}-${groupIndex}`} value={groupIndex}>
                  {groupLabel(group, groupIndex)}
                  {group.protocol ? ` · ${group.protocol}` : ''}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}

      <TabList className="scenario-run-settings-tabs runtime-override-tabs" role="tablist" aria-label={`${title || "Group runtime"} sections`} onKeyDown={handleTabKey}>
        {visibleTabs.map(tab => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`${tabPrefix}-runtime-tab-${tab.id}`}
            aria-selected={activeTab === tab.id}
            aria-controls={`${tabPrefix}-runtime-tab-panel-${tab.id}`}
            className={`scenario-run-settings-tab ${activeTab === tab.id ? 'active' : ''}`}
            onClick={() => setRawActiveTab(tab.id)} tabIndex={(activeTab === tab.id) ? 0 : -1}
          >
            {tab.label}
          </button>
        ))}
      </TabList>

      <div
        className="scenario-run-settings-tab-panel runtime-override-tab-panel"
        role="tabpanel"
        id={`${tabPrefix}-runtime-tab-panel-${activeTab}`}
        aria-labelledby={`${tabPrefix}-runtime-tab-${activeTab}`}
      >
        {activeTab === 'summary' && (
          <table className="runtime-override-summary" data-table-pagination="off">
            <thead>
              <tr>
                <th>Setting</th>
                <th>Effective value</th>
                <th>Source</th>
              <th><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {SUMMARY_FIELDS.filter(field => fieldApplicable(field)).map(field => {
                const applicable = fieldApplicable(field)
                if (!applicable) {
                  return (
                    <tr key={field} className="runtime-override-row not-applicable">
                      <td>{FIELD_LABELS[field]}</td>
                      <td colSpan={3}><span className="runtime-source-badge muted">Not applicable for {focusGroup.protocol || 'this'} protocol</span></td>
                    </tr>
                  )
                }
                const effective = formatEffectiveRuntimeField(field, groupOverride, scenarioDefault, scriptDefault, engineDefault)
                return (
                  <tr key={field} className="runtime-override-row">
                    <td>{FIELD_LABELS[field]}</td>
                    <td>{effective.value}</td>
                    <td><span className={`runtime-source-badge source-${effective.source}`}>{effective.source === 'group' && overrideLabel ? overrideLabel : RUNTIME_FIELD_SOURCE_LABEL[effective.source]}</span></td>
                    <td>
                      {fieldHasOverride(field) && (
                        <button type="button" className="link-button" onClick={() => clearField(field)} aria-label={`Clear ${FIELD_LABELS[field]} override`}>
                          Clear
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}

        {activeTab === 'think_time' && (
          <>
            <div className="scenario-browser-grid">
              <label>
                <span>Distribution</span>
                <select
                  className="settings-input"
                  value={effectiveThink.ignore ? '' : canonicalThinkTimeType(draft.think_time?.type)}
                  disabled={Boolean(effectiveThink.ignore)}
                  onChange={event => {
                    const type = event.target.value
                    if (!type) {
                      patch({ think_time: undefined })
                      return
                    }
                    patchThinkTime({ type })
                  }}
                >
                  <option value="">Inherit</option>
                  <option value="none">None</option>
                  <option value="fixed">Fixed</option>
                  <option value="recorded">Replay think time as recorded</option>
              <option value="random_percentage">Random percentage of recorded think time</option>
                  <option value="gaussian">Gaussian</option>
                  <option value="uniform">Uniform</option>
                </select>
              </label>
              {effectiveThink.type === 'random_percentage' && <ThinkTimePercentageInputs value={effectiveThink} disabled={Boolean(effectiveThink.ignore)} onChange={patchThinkTime} />}
              {thinkTimeUsesMean(effectiveThink.type) && (
                <label>
                  <span>Mean (ms)</span>
                  <input className="settings-input" type="number" min={0} value={draft.think_time?.mean_ms ?? ''} disabled={Boolean(effectiveThink.ignore)} onChange={event => patchThinkTime({ mean_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.mean_ms)} />
                </label>
              )}
              {thinkTimeUsesStdDeviation(effectiveThink.type) && (
                <label>
                  <span>Std dev (ms)</span>
                  <input className="settings-input" type="number" min={0} value={draft.think_time?.std_ms ?? ''} onChange={event => patchThinkTime({ std_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.std_ms)} />
                </label>
              )}
              {thinkTimeUsesBounds(effectiveThink.type) && (
                <>
                  <label>
                    <span>Min (ms)</span>
                    <input className="settings-input" type="number" min={0} value={draft.think_time?.min_ms ?? ''} onChange={event => patchThinkTime({ min_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.min_ms)} />
                  </label>
                  <label>
                    <span>Max (ms)</span>
                    <input className="settings-input" type="number" min={0} value={draft.think_time?.max_ms ?? ''} onChange={event => patchThinkTime({ max_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.max_ms)} />
                  </label>
                </>
              )}
              <ThinkTimeMultiplierInput
                draftScope={`${draftScope ?? ''}:${index}:${focusGroup.scriptId}`}
                label="Multiplier"
                className="settings-input"
                value={draft.think_time?.multiplier}
                fallbackValue=""
                onValueChange={multiplier => patchThinkTime({ multiplier })}
                placeholder={inh(normalizeThinkTimeMultiplier(inheritedThink.multiplier))}
              />
              <label>
                <span>Limit (ms)</span>
                <input className="settings-input" type="number" min={0} value={draft.think_time?.limit_ms ?? ''} onChange={event => patchThinkTime({ limit_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.limit_ms)} />
              </label>
            </div>
            <div className="scenario-option-grid">
              <label className={`scenario-option-card ${effectiveThink.ignore ? 'selected' : ''}`}>
                <input
                  type="checkbox"
                  checked={Boolean(effectiveThink.ignore)}
                  onChange={event => patchThinkTime({ ignore: event.target.checked })}
                />
                <div><strong>Ignore think time</strong><span>Bypass scripted think times for this group.</span></div>
              </label>
            </div>
          </>
        )}

        {activeTab === 'pacing' && (
          <div className="scenario-browser-grid">
            <label>
              <span>Pacing</span>
              <select
                className="settings-input"
                value={pacingType}
                onChange={event => {
                  const type = event.target.value as NonNullable<ScenarioRuntimeSettingsDraft['pacing']>['type'] | ''
                  if (type) patchPacing({ type })
                  else patch({ pacing: undefined })
                }}
              >
                <option value="">Inherit</option>
                <option value="none">None</option>
                <option value="fixed_delay">Fixed delay</option>
                <option value="random_delay">Random delay</option>
                <option value="fixed_interval">Fixed interval</option>
                <option value="random_interval">Random interval</option>
                <option value="tps">Target TPS</option>
              </select>
            </label>
            {pacingType === 'fixed_delay' && (
              <label>
                <span>Pacing delay (seconds)</span>
                <SecondsFromMillisecondsInput className="settings-input" valueMs={draft.pacing?.delay_ms} emptyWhenUndefined onChangeMs={delayMs => patchPacing({ delay_ms: delayMs })} placeholder={formatMillisecondsAsSecondsValue(inheritedPacing.delay_ms)} />
              </label>
            )}
            {pacingType === 'random_delay' && (
              <>
                <label>
                  <span>Min delay (seconds)</span>
                  <SecondsFromMillisecondsInput className="settings-input" valueMs={draft.pacing?.min_delay_ms} emptyWhenUndefined onChangeMs={minDelayMs => patchPacing({ min_delay_ms: minDelayMs })} placeholder={formatMillisecondsAsSecondsValue(inheritedPacing.min_delay_ms)} />
                </label>
                <label>
                  <span>Max delay (seconds)</span>
                  <SecondsFromMillisecondsInput className="settings-input" valueMs={draft.pacing?.max_delay_ms} emptyWhenUndefined onChangeMs={maxDelayMs => patchPacing({ max_delay_ms: maxDelayMs })} placeholder={formatMillisecondsAsSecondsValue(inheritedPacing.max_delay_ms)} />
                </label>
              </>
            )}
            {pacingType === 'fixed_interval' && (
              <label>
                <span>Pacing interval (seconds)</span>
                <SecondsFromMillisecondsInput className="settings-input" valueMs={draft.pacing?.interval_ms} emptyWhenUndefined onChangeMs={intervalMs => patchPacing({ interval_ms: intervalMs })} placeholder={formatMillisecondsAsSecondsValue(inheritedPacing.interval_ms)} />
              </label>
            )}
            {pacingType === 'random_interval' && (
              <>
                <label>
                  <span>Min interval (seconds)</span>
                  <SecondsFromMillisecondsInput className="settings-input" valueMs={draft.pacing?.min_interval_ms} emptyWhenUndefined onChangeMs={minIntervalMs => patchPacing({ min_interval_ms: minIntervalMs })} placeholder={formatMillisecondsAsSecondsValue(inheritedPacing.min_interval_ms)} />
                </label>
                <label>
                  <span>Max interval (seconds)</span>
                  <SecondsFromMillisecondsInput className="settings-input" valueMs={draft.pacing?.max_interval_ms} emptyWhenUndefined onChangeMs={maxIntervalMs => patchPacing({ max_interval_ms: maxIntervalMs })} placeholder={formatMillisecondsAsSecondsValue(inheritedPacing.max_interval_ms)} />
                </label>
              </>
            )}
            {pacingType === 'tps' && (
              <label>
                <span>Target TPS</span>
                <input className="settings-input" type="number" min={0} value={draft.pacing?.tps ?? ''} onChange={event => patchPacing({ tps: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.tps, '0')} />
              </label>
            )}
          </div>
        )}

        {activeTab === 'run_logic' && (
          <div className="scenario-browser-grid">
            <label>
              <span>Iterations</span>
              <input
                className="settings-input"
                type="number"
                min={0}
                value={draft.run_logic?.iterations ?? draft.iterations ?? ''}
                onChange={event => patchRunLogic({ iterations: event.target.value === '' ? undefined : Number(event.target.value) })}
                placeholder={inh(inheritedIterations, 'Duration-driven')}
              />
            </label>
            <label>
              <span>Auto-transactions</span>
              <select className="settings-input" value={draft.run_logic?.auto_transactions ?? draft.auto_transactions ?? ''} onChange={event => patchRunLogic({ auto_transactions: event.target.value || undefined })}>
                <option value="">Inherit</option>
                <option value="none">None</option>
                <option value="per_action">Per action</option>
                <option value="per_iteration">Per iteration</option>
              </select>
            </label>
            <label>
              <span>Workload model</span>
              <select className="settings-input" value={draft.run_logic?.workload_model || ''} onChange={event => patchRunLogic({ workload_model: event.target.value || undefined })}>
                <option value="">Inherit</option>
                <option value="closed">Closed</option>
                <option value="open">Open</option>
              </select>
            </label>
            <label>
              <span>Block scheduling</span>
              <select className="settings-input" value={draft.run_logic?.block_scheduling || ''} onChange={event => patchRunLogic({ block_scheduling: event.target.value || undefined })}>
                <option value="">Inherit</option>
                <option value="sequential">Sequential</option>
                <option value="random">Random</option>
                <option value="percentage">Percentage</option>
              </select>
            </label>
            <label>
              <span>Global iterations</span>
              <input className="settings-input" type="number" min={0} value={draft.run_logic?.global_iterations ?? ''} onChange={event => patchRunLogic({ global_iterations: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedRunLogic.global_iterations)} />
            </label>
            <label>
              <span>Max transactions</span>
              <input className="settings-input" type="number" min={0} value={draft.run_logic?.max_transactions ?? ''} onChange={event => patchRunLogic({ max_transactions: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedRunLogic.max_transactions)} />
            </label>
            <label>
              <span>Action sequence</span>
              <input
                className="settings-input"
                type="text"
                value={draft.run_logic?.action_sequence?.join(', ') || ''}
                onChange={event => {
                  const sequence = event.target.value.split(',').map(value => value.trim()).filter(Boolean)
                  patchRunLogic({ action_sequence: sequence.length ? sequence : undefined })
                }}
                placeholder="login, browse, checkout"
              />
            </label>
          </div>
        )}

        {activeTab === 'error_handling' && (
          <>
            {isRuntimeFieldApplicable('results', focusGroup.protocol) && <MessageDetailFields value={draft.results?.message_detail ?? inheritedOverride?.results?.message_detail} onChange={value => patch({ results: { ...inheritedOverride?.results, ...draft.results, message_detail: value } })} />}
            {isRuntimeFieldApplicable('results', focusGroup.protocol) && <PropertyAnalyticsFields value={draft.results?.property_analytics ?? inheritedOverride?.results?.property_analytics} onChange={value => patch({ results: { ...inheritedOverride?.results, ...draft.results, property_analytics: value } })} />}
            <div className="scenario-option-grid">
              <label className={`scenario-option-card ${effectiveError.continue_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={effectiveError.continue_on_error ?? false} onChange={event => patchErrorHandling({ continue_on_error: event.target.checked })} />
                <div><strong>Continue on error</strong><span>Keep iterating after a failed step.</span></div>
              </label>
              <label className={`scenario-option-card ${effectiveError.snapshot_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={effectiveError.snapshot_on_error ?? false} onChange={event => patchErrorHandling({ snapshot_on_error: event.target.checked })} />
                <div><strong>Snapshot on error</strong><span>Capture diagnostics when a step fails.</span></div>
              </label>
            </div>
            <div className="scenario-browser-grid">
              <label>
                <span>Log level</span>
                <select className="settings-input" value={draft.log?.level ?? draft.log_level ?? ''} onChange={event => patchLog({ level: event.target.value || undefined })}>
                  <option value="">Inherit</option>
                  <option value="disabled">Disabled</option>
                  <option value="standard">Standard</option>
                  <option value="extended">Extended</option>
                </select>
              </label>
              <label>
                <span>Max retries</span>
                <input
                  className="settings-input"
                  type="number"
                  min={0}
                  value={draft.max_retries ?? ''}
                  onChange={event => patchErrorHandling({ max_retries: event.target.value === '' ? undefined : Number(event.target.value) })}
                  placeholder={inh(inheritedError.max_retries, '0')}
                />
              </label>
              <label>
                <span>Retry delay (ms)</span>
                <input
                  className="settings-input"
                  type="number"
                  min={0}
                  value={draft.retry_delay_ms ?? ''}
                  onChange={event => patchErrorHandling({ retry_delay_ms: event.target.value === '' ? undefined : Number(event.target.value) })}
                  placeholder={inh(inheritedError.retry_delay_ms)}
                />
              </label>
              <label>
                <span>Retry backoff</span>
                <select
                  className="settings-input"
                  value={draft.retry_backoff || ''}
                  onChange={event => patchErrorHandling({ retry_backoff: event.target.value || undefined })}
                >
                  <option value="">Inherit</option>
                  <option value="fixed">Fixed</option>
                  <option value="exponential">Exponential</option>
                </select>
              </label>
            </div>
            <div className="scenario-option-grid">
              <label><span>Error history buffer (bytes)</span><input type="number" min={0} max={1048576} value={effectiveLog.error_buffer_bytes ?? ''} placeholder="102400 by default" onChange={event => patchLog({ error_buffer_bytes: event.target.value === '' ? undefined : Number(event.target.value) })} /></label>
              <label className={`scenario-option-card ${effectiveLog.on_error_only ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(effectiveLog.on_error_only)} onChange={event => patchLog({ on_error_only: event.target.checked })} />
                <div><strong>Extended logging on error only</strong><span>Use extended logging only when a step fails.</span></div>
              </label>
              {/* Defaults OFF: an absent value reads as unchecked. Governs run output only — the script keeps
                  its literal credential values either way. */}
              <label className={`scenario-option-card ${effectiveLog.redact_run_evidence ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(effectiveLog.redact_run_evidence)} onChange={event => patchLog({ redact_run_evidence: event.target.checked })} />
                <div><strong>Redact credentials in run evidence</strong><span>Redacts credential values from run logs, snapshots, reports, archives and debugger evidence. Does not change the script itself.</span></div>
              </label>
              <label className={`scenario-option-card ${effectiveLog.extended_options?.includes('parameter_substitution') ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(effectiveLog.extended_options?.includes('parameter_substitution'))} onChange={event => toggleLogOption('parameter_substitution', event.target.checked)} />
                <div><strong>Parameter substitution</strong><span>Log each parameter substitution.</span></div>
              </label>
              <label className={`scenario-option-card ${effectiveLog.extended_options?.includes('data_returned') ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(effectiveLog.extended_options?.includes('data_returned'))} onChange={event => toggleLogOption('data_returned', event.target.checked)} />
                <div><strong>Data returned</strong><span>Log the data returned by the server.</span></div>
              </label>
              <label className={`scenario-option-card ${effectiveLog.extended_options?.includes('advanced_trace') ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(effectiveLog.extended_options?.includes('advanced_trace'))} onChange={event => toggleLogOption('advanced_trace', event.target.checked)} />
                <div><strong>Advanced trace</strong><span>Capture full advanced trace detail.</span></div>
              </label>
            </div>
          </>
        )}

        {activeTab === 'http_transport' && (
          <>
            <div className="http-settings-persistent-summary" role="status">
              <strong>Effective HTTP settings</strong>
              <span>{formatHTTPSettingsSummary(
                httpSettingsValue,
                Boolean(effectiveBrowserEmulation.cache?.new_user_each_iteration),
              )}</span>
              <span className="http-setting-origin">{httpOriginLabel}</span>
            </div>
            <label className="http-transport-mobile-select">
              <span>HTTP settings group</span>
              <select value={httpSubTab} onChange={event => setRawHttpSubTab(event.target.value as HttpSubTabId)}>
                {visibleHttpSubTabs.map(st => <option value={st.id} key={st.id}>{st.label}</option>)}
              </select>
            </label>
            <TabList className="http-transport-tabs" role="tablist" aria-label="HTTP transport sections" onKeyDown={handleTabKey}>
              {visibleHttpSubTabs.map(st => (
                <button
                  key={st.id}
                  type="button"
                  role="tab"
                  aria-selected={httpSubTab === st.id}
                  className={`http-transport-tab ${httpSubTab === st.id ? 'active' : ''}`}
                  onClick={() => setRawHttpSubTab(st.id)} tabIndex={(httpSubTab === st.id) ? 0 : -1}
                >
                  <span>{st.label}</span>
                  {st.id === 'request_defaults' && (
                    <>
                      <span className="http-tab-count">{countHTTPDefaultHeaders(httpSettingsValue)}</span>
                      {requestDefaultErrorCount > 0 && <span className="http-tab-error" aria-label={`${requestDefaultErrorCount} request default errors`}>{requestDefaultErrorCount} errors</span>}
                    </>
                  )}
                  {st.id === 'session_cache' && (
                    <>
                      <span className="http-tab-count">{countPinnedCookies(httpSettingsValue)}</span>
                      {pinnedCookieErrorCount > 0 && <span className="http-tab-error" aria-label={`${pinnedCookieErrorCount} pinned cookie errors`}>{pinnedCookieErrorCount} errors</span>}
                    </>
                  )}
                </button>
              ))}
            </TabList>

            <div className="http-transport-tab-panel">
              {httpSubTab === 'request_defaults' && (
                <HttpRequestDefaultsEditor
                  actions={focusGroup.scriptActions}
                  newUserEachIteration={Boolean(effectiveBrowserEmulation.cache?.new_user_each_iteration)}
                  onChange={patchHTTPSettings}
                  onNewUserEachIterationChange={enabled => patchBrowserEmulation({ cache: { new_user_each_iteration: enabled } })}
                  originLabel={httpOriginLabel}
                  section="request"
                  value={httpSettingsValue}
                />
              )}

              {httpSubTab === 'session_cache' && (
                <>
                <HttpRequestDefaultsEditor
                  actions={focusGroup.scriptActions}
                  newUserEachIteration={Boolean(effectiveBrowserEmulation.cache?.new_user_each_iteration)}
                  onChange={patchHTTPSettings}
                  onNewUserEachIterationChange={enabled => patchBrowserEmulation({ cache: { new_user_each_iteration: enabled } })}
                  originLabel={draft.http ? 'Group override' : 'Inherited'}
                  section="session"
                  value={httpSettingsValue}
                />
                <div className="scenario-browser-grid"><HTTPReplayPolicyFields section="session" value={draft.http} inherited={inheritedHttp} onChange={patchHttp} /></div>
                </>
              )}

              {httpSubTab === 'timeouts' && (
                <div className="scenario-browser-grid">
                  <HTTPReplayPolicyFields section="timeouts" value={draft.http} inherited={inheritedHttp} onChange={patchHttp} />
                  <label><span>Connect timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.connect_timeout_ms ?? ''} onChange={event => patchHttp({ connect_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.connect_timeout_ms)} /></label>
                  <label><span>Request timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.request_timeout_ms ?? ''} onChange={event => patchHttp({ request_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.request_timeout_ms)} /></label>
                  <label><span>TLS handshake timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.tls_timeout_ms ?? ''} onChange={event => patchHttp({ tls_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.tls_timeout_ms)} /></label>
                  <label><span>Idle timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.idle_timeout_ms ?? ''} onChange={event => patchHttp({ idle_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.idle_timeout_ms)} /></label>
                </div>
              )}

              {httpSubTab === 'connection' && (
                <div className="scenario-browser-grid">
                  <HTTPReplayPolicyFields section="connection" value={draft.http} inherited={inheritedHttp} onChange={patchHttp} />
                  <label>
                    <span>HTTP version</span>
                    <select className="settings-input" value={draft.http?.version || ''} onChange={event => patchHttp({ version: event.target.value || undefined })}>
                      <option value="">Inherit</option>
                      {HTTP_VERSION_OPTIONS.slice(1).map(option => (
                        <option key={option.value} value={option.value}>{option.label}</option>
                      ))}
                    </select>
                  </label>
                  <div className="form-hint">
                    Inherit uses the next configured layer; an effective empty value auto-negotiates. Explicit versions are exact, and required HTTP/2 never falls back to HTTP/1.1.
                  </div>
                  <label><span>Max connections / host</span><input className="settings-input" type="number" min={0} value={draft.http?.max_conns_per_host ?? ''} onChange={event => patchHttp({ max_conns_per_host: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.max_conns_per_host)} /></label>
                  <label><span>Max idle connections</span><input className="settings-input" type="number" min={0} value={draft.http?.max_idle_conns ?? ''} onChange={event => patchHttp({ max_idle_conns: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.max_idle_conns)} /></label>
                  <label><span>Max redirects</span><input className="settings-input" type="number" min={0} value={draft.http?.max_redirects ?? ''} onChange={event => patchHttp({ max_redirects: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.max_redirects)} /></label>
                  <label>
                    <span>Keep-alive</span>
                    <select className="settings-input" value={browserBoolValue(draft.http?.keep_alive)} onChange={event => patchHttp({ keep_alive: event.target.value === '' ? undefined : event.target.value === 'true' })}>
                      <option value="">Inherit</option>
                      <option value="true">Enabled</option>
                      <option value="false">Disabled</option>
                    </select>
                  </label>
                  <details className="http-advanced-options">
                    <summary>Advanced connection guidance</summary>
                    <p>Connection-specific headers require exact HTTP/1.1. Unsupported imported socket options remain review-only compatibility metadata.</p>
                  </details>
                  <SocketCompatibilityPanel
                    additionalAttributes={effectiveSocketAttributes}
                    originLabel={webOriginLabel}
                  />
                </div>
              )}

              {httpSubTab === 'tls' && (
                <>
                  <div className="scenario-browser-grid">
                    <label>
                      <span>TLS min version</span>
                      <select className="settings-input" value={draft.http?.tls?.min_version || ''} onChange={event => patchTls({ min_version: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="1.0">1.0</option>
                        <option value="1.1">1.1</option>
                        <option value="1.2">1.2</option>
                        <option value="1.3">1.3</option>
                      </select>
                    </label>
                    <label><span>Client cert file</span><input className="settings-input" type="text" value={draft.http?.tls?.client_cert_file || ''} onChange={event => patchTls({ client_cert_file: event.target.value || undefined })} placeholder={inh(inheritedHttp.tls?.client_cert_file)} /></label>
                    <label><span>Client key file</span><input className="settings-input" type="text" value={draft.http?.tls?.client_key_file || ''} onChange={event => patchTls({ client_key_file: event.target.value || undefined })} placeholder={inh(inheritedHttp.tls?.client_key_file)} /></label>
                    <label><span>CA cert file</span><input className="settings-input" type="text" value={draft.http?.tls?.ca_cert_file || ''} onChange={event => patchTls({ ca_cert_file: event.target.value || undefined })} placeholder={inh(inheritedHttp.tls?.ca_cert_file)} /></label>
                  </div>
                  <div className="scenario-option-grid">
                    <label className={`scenario-option-card ${effectiveHttp.tls?.insecure_skip_verify ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveHttp.tls?.insecure_skip_verify)} onChange={event => patchTls({ insecure_skip_verify: event.target.checked })} />
                      <div><strong>Skip TLS verification</strong><span>Do not validate the server certificate (test environments only).</span></div>
                    </label>
                  </div>
                </>
              )}

              {httpSubTab === 'proxy' && (
                <div className="scenario-browser-grid">
                  <label>
                    <span>Proxy mode</span>
                    <select className="settings-input" value={draft.http?.proxy_mode || ''} onChange={event => patchHttp({ proxy_mode: event.target.value || undefined })}>
                      <option value="">Inherit</option>
                      <option value="none">None</option>
                      <option value="system">System</option>
<option value="pac">PAC script</option>
                      <option value="custom">Custom</option>
                    </select>
                  </label>
                  <label><span>PAC URL</span><input className="settings-input" value={draft.http?.proxy?.pac_url || ''} onChange={event => patchProxy({ pac_url: event.target.value || undefined })} placeholder={inh(inheritedHttp.proxy?.pac_url)} /></label>
                  <label><span>Proxy URL</span><input className="settings-input" type="text" value={draft.http?.proxy?.url || ''} onChange={event => patchProxy({ url: event.target.value || undefined })} placeholder={inh(inheritedHttp.proxy?.url, 'http://proxy:8080')} /></label>
                  <label><span>Proxy username</span><input className="settings-input" type="text" autoComplete="off" value={draft.http?.proxy?.username || ''} onChange={event => patchProxy({ username: event.target.value || undefined })} placeholder="Inherit" /></label>
                  <label><span>Proxy password</span><input className="settings-input" type="password" autoComplete="off" value={draft.http?.proxy?.password || ''} onChange={event => patchProxy({ password: event.target.value || undefined })} placeholder="Inherit" /></label>
                  <label>
                    <span>No-proxy hosts</span>
                    <input className="settings-input" type="text" value={draft.http?.proxy?.no_proxy?.join(', ') || ''} onChange={event => {
                      const hosts = event.target.value.split(',').map(value => value.trim()).filter(Boolean)
                      patchProxy({ no_proxy: hosts.length ? hosts : undefined })
                    }} placeholder={inh(inheritedHttp.proxy?.no_proxy?.join(', '), 'localhost, 127.0.0.1')} />
                  </label>
                </div>
              )}

              {httpSubTab === 'authentication' && (
                <div className="scenario-browser-grid">
                  <label>
                    <span>Auth type</span>
                    <select className="settings-input" value={draft.http?.auth?.type || ''} onChange={event => patchAuth({ type: event.target.value || undefined })}>
                      <option value="">Inherit</option>
                      <option value="basic">Basic</option>
                      <option value="bearer">Bearer</option>
                      <option value="ntlm">NTLM</option>
                      <option value="negotiate">Negotiate (NTLM)</option>
                      <option value="negotiate_any">Any challenge (Basic, Digest, NTLM, Negotiate)</option>
                      <option value="digest">Digest</option>
                    </select>
                  </label>
                  <label><span>Auth username</span><input className="settings-input" type="text" value={draft.http?.auth?.username || ''} onChange={event => patchAuth({ username: event.target.value || undefined })} placeholder={inh(inheritedHttp.auth?.username)} /></label>
                  <label><span>Auth password</span><input className="settings-input" type="password" autoComplete="off" value={draft.http?.auth?.password || ''} onChange={event => patchAuth({ password: event.target.value || undefined })} placeholder="Inherit" /></label>
                  <label><span>Auth token</span><input className="settings-input" type="password" autoComplete="off" value={draft.http?.auth?.token || ''} onChange={event => patchAuth({ token: event.target.value || undefined })} placeholder="Inherit" /></label>
                </div>
              )}

              {httpSubTab === 'browser_emulation' && browserApplicable && (
                <>
                  <div className="scenario-browser-grid">
                    <label>
                      <span>Device profile</span>
                      <select className="settings-input" value={draft.browser_config?.device_profile ?? draft.browser?.device_profile ?? ''} onChange={event => patchBrowserConfig({ device_profile: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="desktop_1080p">desktop_1080p</option>
                        <option value="galaxy_s24">galaxy_s24</option>
                        <option value="pixel_8">pixel_8</option>
                        <option value="ipad_air">ipad_air (WebKit)</option>
                        <option value="iphone_15_pro">iphone_15_pro (WebKit)</option>
                        <option value="iphone_se">iphone_se (WebKit)</option>
                      </select>
                    </label>
                    <label>
                      <span>Orientation</span>
                      <select className="settings-input" value={draft.browser_config?.orientation ?? draft.browser?.orientation ?? ''} onChange={event => patchBrowserConfig({ orientation: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="portrait">Portrait</option>
                        <option value="landscape">Landscape</option>
                      </select>
                    </label>
                    <label>
                      <span>Headless</span>
                      <select className="settings-input" value={browserBoolValue(draft.browser_config?.headless, draft.browser?.headless)} onChange={event => patchBrowserConfig({ headless: event.target.value === '' ? undefined : event.target.value === 'true' })}>
                        <option value="">Inherit</option>
                        <option value="true">Headless</option>
                        <option value="false">Headed</option>
                      </select>
                    </label>
                    <label>
                      <span>Touch mode</span>
                      <select className="settings-input" value={browserBoolValue(draft.browser_config?.touch_mode, draft.browser?.touch_mode)} onChange={event => patchBrowserConfig({ touch_mode: event.target.value === '' ? undefined : event.target.value === 'true' })}>
                        <option value="">Inherit</option>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                    <label>
                      <span>Browser engine</span>
                      <select className="settings-input" value={draft.browser_config?.engine || ''} onChange={event => patchBrowserConfig({ engine: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="chromium">Chromium</option>
                        <option value="firefox">Firefox</option>
                        <option value="webkit">WebKit</option>
                      </select>
                    </label>
                    <label><span>Browser user agent</span><input className="settings-input" type="text" value={draft.browser_config?.user_agent || ''} onChange={event => patchBrowserConfig({ user_agent: event.target.value || undefined })} placeholder={inh(inheritedBrowser.user_agent)} /></label>
                    <label><span>Slow-mo (ms)</span><input className="settings-input" type="number" min={0} value={draft.browser_config?.slow_mo_ms ?? ''} onChange={event => patchBrowserConfig({ slow_mo_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedBrowser.slow_mo_ms)} /></label>
                    <label><span>Emulated system language</span><input className="settings-input" value={draft.browser_config?.system_language || ''} onChange={event => patchBrowserConfig({system_language: event.target.value || undefined})} placeholder={inh(inheritedBrowser.system_language)} /></label>
 {(['available_screen_width','available_screen_height'] as const).map(field => <label key={field}><span>{field === 'available_screen_width' ? 'Available screen width' : 'Available screen height'}</span><input className="settings-input" type="number" min={0} max={8192} value={draft.browser_config?.[field] ?? ''} onChange={event => patchBrowserConfig({[field]: event.target.value === '' ? undefined : Number(event.target.value)})} placeholder={inh(inheritedBrowser[field])} /></label>)}
                    <label><span>Viewport width</span><input className="settings-input" type="number" min={0} value={draft.browser_config?.viewport?.width ?? ''} onChange={event => patchBrowserConfig({ viewport: { width: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder={inh(inheritedBrowser.viewport?.width)} /></label>
                    <label><span>Viewport height</span><input className="settings-input" type="number" min={0} value={draft.browser_config?.viewport?.height ?? ''} onChange={event => patchBrowserConfig({ viewport: { height: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder={inh(inheritedBrowser.viewport?.height)} /></label>
                    <label>
                      <span>Capture screenshots</span>
                      <select className="settings-input" value={draft.browser_config?.capture_screenshots || ''} onChange={event => patchBrowserConfig({ capture_screenshots: event.target.value || undefined })}>
                        <option value="">{inheritedBrowser.capture_screenshots ? `Inherit (${inheritedBrowser.capture_screenshots})` : 'Inherit'}</option>
                        <option value="off">Off</option>
                        <option value="on_error">On error</option>
                        <option value="per_action">Per action</option>
                      </select>
                    </label>
                    <label>
                      <span>Download resources</span>
                      <select className="settings-input" value={draft.browser_config?.download_resources || ''} onChange={event => patchBrowserConfig({ download_resources: event.target.value || undefined })}>
                        <option value="">{inheritedBrowser.download_resources ? `Inherit (${inheritedBrowser.download_resources})` : 'Inherit'}</option>
                        <option value="none">HTML only</option>
                        <option value="recorded">Recorded resources</option>
                        <option value="all">All resources</option>
                      </select>
                    </label>
                  </div>
                  <div className="scenario-option-grid">
                    <label className={`scenario-option-card ${effectiveBrowser.client_metrics ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveBrowser.client_metrics)} onChange={event => patchBrowserConfig({ client_metrics: event.target.checked })} />
                      <div><strong>Client metrics</strong><span>Collect browser client-side metrics.</span></div>
                    </label>
                    <label className={`scenario-option-card ${effectiveBrowser.cache_enabled ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveBrowser.cache_enabled)} onChange={event => patchBrowserConfig({ cache_enabled: event.target.checked })} />
                      <div><strong>Cache enabled</strong><span>Allow the browser cache during replay.</span></div>
                    </label>
                    <label className={`scenario-option-card ${effectiveBrowser.cache_content_urls ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveBrowser.cache_content_urls)} onChange={event => patchBrowserConfig({ cache_content_urls: event.target.checked })} />
                      <div><strong>Cache content URLs</strong><span>Cache static content URLs.</span></div>
                    </label>
                    <label className={`scenario-option-card ${effectiveBrowser.check_newer_versions ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveBrowser.check_newer_versions)} onChange={event => patchBrowserConfig({ check_newer_versions: event.target.checked })} />
                      <div><strong>Check newer versions</strong><span>Check for newer resource versions.</span></div>
                    </label>
                    <label className={`scenario-option-card ${effectiveBrowser.new_user_per_iteration ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveBrowser.new_user_per_iteration)} onChange={event => patchBrowserConfig({ new_user_per_iteration: event.target.checked })} />
                      <div><strong>New user per iteration</strong><span>Start each iteration with a fresh browser profile.</span></div>
                    </label>
                    <label className={`scenario-option-card ${effectiveBrowser.review_policy?.allow_reviewed_javascript ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveBrowser.review_policy?.allow_reviewed_javascript)} onChange={event => patchBrowserConfig({ review_policy: { allow_reviewed_javascript: event.target.checked } })} />
                      <div><strong>Allow reviewed JavaScript</strong><span>Permit JavaScript steps that passed review.</span></div>
                    </label>
                    <label className={`scenario-option-card ${effectiveBrowser.review_policy?.allow_review_required ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(effectiveBrowser.review_policy?.allow_review_required)} onChange={event => patchBrowserConfig({ review_policy: { allow_review_required: event.target.checked } })} />
                      <div><strong>Allow review-required</strong><span>Permit steps still pending review.</span></div>
                    </label>
                  </div>
                </>
              )}
            </div>
          </>
        )}

        {activeTab === 'browser_emulation' && (
          <>
            {draft.browser_emulation && (
              <div className="runtime-override-active-banner">
                <span>Overriding the script's browser emulation for this group.</span>
                <button type="button" className="link-button" onClick={() => clearField('browser_emulation')}>
                  Clear override
                </button>
              </div>
            )}
            <div className="scenario-browser-grid">
              <label>
                <span>Mode</span>
                <select
                  className="settings-input"
                  value={draft.browser_emulation?.mode ?? ''}
                  aria-label="Browser emulation mode"
                  onChange={event => {
                    const mode = event.target.value
                    if (!mode) {
                      // Inherit browser identity, but keep this group's cache AND
                      // language overrides. Neither is mode-dependent at replay —
                      // the engine derives Accept-Language from `language` whether
                      // or not a mode is set — so dropping them here would silently
                      // discard edits made on this same tab.
                      const kept: BrowserEmulationSettings = {}
                      const currentCache = draft.browser_emulation?.cache
                      if (currentCache) kept.cache = currentCache
                      const currentLanguage = draft.browser_emulation?.language
                      if (currentLanguage) kept.language = currentLanguage
                      patch({ browser_emulation: Object.keys(kept).length > 0 ? kept : undefined })
                      return
                    }
                    if (mode === 'custom') {
                      patchBrowserEmulation({ mode: 'custom', browser_type: undefined, browser_version: undefined, platform: undefined })
                    } else {
                      patchBrowserEmulation({ mode: 'specific', custom_user_agent: undefined })
                    }
                  }}
                >
                  <option value="">Inherit{effectiveBrowserEmulation.mode ? ` (${effectiveBrowserEmulation.mode})` : ''}</option>
                  <option value="specific">Specific browser</option>
                  <option value="custom">Custom browser</option>
                </select>
              </label>
              <label>
                <span>Browser</span>
                <select
                  className="settings-input"
                  value={effectiveBrowserEmulation.browser_type || ''}
                  disabled={effectiveBrowserEmulation.mode === 'custom'}
                  aria-label="Browser type"
                  onChange={event => patchBrowserEmulation({ mode: event.target.value ? 'specific' : undefined, browser_type: event.target.value || undefined, browser_version: undefined, platform: undefined })}
                >
                  <option value="">Engine default</option>
                  {browserEmulationCatalog.browsers.map(browser => (
                    <option key={browser.type} value={browser.type}>{browser.label}</option>
                  ))}
                </select>
              </label>
              <label>
                <span>Version</span>
                <select
                  className="settings-input"
                  value={effectiveBrowserEmulation.browser_version || ''}
                  disabled={effectiveBrowserEmulation.mode === 'custom' || !effectiveBrowserEmulation.browser_type}
                  aria-label="Browser version"
                  onChange={event => patchBrowserEmulation({ browser_version: event.target.value || undefined })}
                >
                  <option value="">Latest</option>
                  {(selectedEmulationBrowser?.versions || []).map(version => (
                    <option key={version} value={version}>{version}</option>
                  ))}
                </select>
              </label>
              <label>
                <span>Platform</span>
                <select
                  className="settings-input"
                  value={effectiveBrowserEmulation.platform || ''}
                  disabled={effectiveBrowserEmulation.mode === 'custom' || !effectiveBrowserEmulation.browser_type}
                  aria-label="Browser platform"
                  onChange={event => patchBrowserEmulation({ platform: event.target.value || undefined })}
                >
                  <option value="">Browser default</option>
                  {(selectedEmulationBrowser?.platforms || []).map(platform => (
                    <option key={platform} value={platform}>
                      {browserEmulationCatalog.platforms.find(candidate => candidate.id === platform)?.label || platform}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>Language</span>
                <select
                  className="settings-input"
                  value={effectiveBrowserEmulation.language || ''}
                  aria-label="Browser language"
                  onChange={event => patchBrowserEmulation({ language: event.target.value || undefined })}
                >
                  <option value="">Engine default</option>
                  {browserEmulationCatalog.languages.map(language => (
                    <option key={language.tag} value={language.tag}>{language.label}</option>
                  ))}
                </select>
              </label>
              <label className="scenario-browser-grid-wide">
                <span>Custom User-Agent</span>
                <input
                  className="settings-input"
                  value={effectiveBrowserEmulation.custom_user_agent || ''}
                  disabled={effectiveBrowserEmulation.mode !== 'custom'}
                  placeholder={inh(inheritedBrowserEmulation.custom_user_agent)}
                  aria-label="Custom User-Agent"
                  onChange={event => patchBrowserEmulation({ custom_user_agent: event.target.value })}
                />
              </label>
            </div>
            <div className="http-cache-canonical-note" role="note">
              Cache enablement and per-iteration cleanup are configured in HTTP Transport → Session &amp; Cache.
              <button type="button" className="link-button" onClick={() => {
                setRawActiveTab('http_transport')
                setRawHttpSubTab('session_cache')
              }}>Open Session &amp; Cache</button>
            </div>
            <div className="scenario-browser-checkboxes" aria-label="Browser cache simulation">
              <label>
                <input
                  type="checkbox"
                  checked={effectiveBrowserEmulation.cache?.cache_content_urls || false}
                  disabled={!httpSettingsValue.cache_enabled}
                  onChange={event => patchBrowserEmulation({ cache: { cache_content_urls: event.target.checked } })}
                />
                <span>Cache URLs requiring content (HTMLs)</span>
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={effectiveBrowserEmulation.cache?.check_newer_versions || false}
                  disabled={!httpSettingsValue.cache_enabled}
                  onChange={event => patchBrowserEmulation({ cache: { check_newer_versions: event.target.checked } })}
                />
                <span>Check for newer versions of stored pages on every visit</span>
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={effectiveBrowserEmulation.cache?.download_non_html_resources || false}
                  onChange={event => patchBrowserEmulation({ cache: { download_non_html_resources: event.target.checked } })}
                />
                <span>Download non-HTML resources</span>
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={effectiveBrowserEmulation.cache?.simulate_prefetch || false}
                  onChange={event => patchBrowserEmulation({ cache: { simulate_prefetch: event.target.checked } })}
                />
                <span>Simulate prefetch and prerender</span>
              </label>
            </div>
          </>
        )}

        {activeTab === 'web_http' && (
          <WebHttpTab web={draft.web_runtime} inheritedWeb={inheritedWeb} setWeb={next => patch({ web_runtime: next })} />
        )}

        {activeTab === 'java_env' && (
          <>
            {draft.java_vuser && (
              <div className="runtime-override-active-banner">
                <span>Overriding the script's Java Env settings for this group.</span>
                <button type="button" className="link-button" onClick={() => clearField('java_vuser')}>
                  Clear override
                </button>
              </div>
            )}
            <JavaEnvSettingsFields
              idPrefix={`group-${index}-java-vuser`}
              value={effectiveJava}
              importedVMParameters={focusGroup.importedJavaVMParameters}
              onChange={patchJavaVuser}
              listOverrideInherits
              dedicatedClassLoaderNote="Each Java VUser keeps one JVM, Actions instance, and child loader for its full lifecycle when enabled."
            />
          </>
        )}
      </div>

      {fieldApplicable('websocket') && <label className="runtime-apply-row"><span>WebSocket callback interval (ms)</span><input className="settings-input" type="number" min={0} max={60000} value={draft.websocket?.callback_interval_ms ?? ''} placeholder={String(inheritedOverride?.websocket?.callback_interval_ms ?? 500)} onChange={event => patch({websocket: {...(draft.websocket ?? inheritedOverride?.websocket), callback_interval_ms: event.target.value === '' ? undefined : Number(event.target.value)}})} /></label>}
      {applyPanelAvailable && (
      <div className="runtime-apply-row">
        <button type="button" className="btn btn-sm btn-secondary" onClick={() => setApplyOpen(open => !open)} disabled={groups.length < 2}>
          Apply to groups…
        </button>
        {applyOpen && (
          <div className="runtime-apply-panel">
            <p>Copy {groupLabel(focusGroup, index)}’s effective runtime to:</p>
            {groups.map((group, groupIndex) => {
              if (groupIndex === index) return null
              return (
                <label key={`apply-${group.scriptId}-${groupIndex}`} className="runtime-apply-target">
                  <input type="checkbox" checked={applyTargets.includes(groupIndex)} onChange={() => toggleTarget(groupIndex)} />
                  <span>{groupLabel(group, groupIndex)}{group.protocol ? ` · ${group.protocol}` : ''}</span>
                </label>
              )
            })}
            <button type="button" className="primary-button" onClick={applyNow} disabled={applyTargets.length === 0}>
              Apply
            </button>
          </div>
        )}
      </div>
      )}
    </div>
  )
}
