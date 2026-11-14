import PropertyAnalyticsFields from './PropertyAnalyticsFields'
import NativeServicesEditor from './NativeServicesEditor'
import AuthHostsField from './AuthHostsField'
import { requiresScriptBaseURL } from './scriptAuthoringAssist'
import { scriptInactiveProxyCredentialNotes } from './scriptSecretReferenceModel'
import { MessageDetailFields } from './MessageDetailFields'
import { HTTPReplayPolicyFields } from './HTTPReplayPolicyFields'
import ThinkTimePercentageInputs from './ThinkTimePercentageInputs'
import { RuntimeCountLimit } from './RuntimeCountLimit'
import { RuntimeContextSummary, type DebugRuntimeContext } from './RuntimeContextSummary'
import { RuntimeFieldEvidence } from './RuntimeFieldEvidence'
import { EXPONENTIAL_THINK_TIME_CAP_FACTOR, RUNTIME_WORKSPACE_CATEGORIES, canonicalPacingType, canonicalWorkloadModel, exponentialThinkTimeStopMs, isOpenWorkloadModel, RUNTIME_SEARCH_TERMS, runtimeTimingIssues, effectiveThinkTimeLabel } from './runtimeWorkspaceModel'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import { useId, useState, type InputHTMLAttributes, type ReactNode } from 'react'

import type { TransactionSLARule } from '../types/goals'
import type { ScriptActionDefinition, ScriptDefinition, ScriptConfig } from '../types/scripts'
import { validateGoalConfig } from './goalConfigValidation'
import { useBrowserEmulationCatalog } from '../hooks/useBrowserEmulationCatalog'
import { jmsProviderForProtocol } from './scriptEditorHelpers'
import { JavaEnvSettingsFields } from './JavaEnvSettingsFields'
import { readImportedJavaVMParameters } from './scriptEditorImportModel'
import { RuntimeSectionIcon } from './RuntimeSectionIcon'
import AppiumRuntimeFields from './AppiumRuntimeFields'
import { appiumDeviceSummary } from './appiumRuntimeModel'
import {
  ImportedSourceRuntimeSection,
  JavaHTTPRuntimeSection,
  SoapRuntimeSection,
  WebRuntimeCompatibilitySection,
} from './ScriptRuntimeSettingsProtocolSections'
import { KafkaRuntimeSection } from './KafkaRuntimeSection'
import { BrowserAccessFields } from './BrowserAccessFields'
import { CheckboxField, CredentialField, IntegerField, KeyValueMapField, LineListField, SelectField, TextField } from './RuntimeConfigFieldControls'
import { databaseDSNPlaceholder, javaVuserParamBraceIssue, selectOptionsWithStored } from './protocolRuntimeOptions'
import {
  BROWSER_DEVICE_PROFILES,
  formatBrowserDeviceProfileLabel,
  lookupBrowserDeviceProfile,
} from './scriptEditorWorkbench'
import {
  canonicalThinkTimeType,
	thinkTimeConfigForType,
	thinkTimeUsesBounds,
	thinkTimeUsesMean,
  thinkTimeUsesStdDeviation,
} from './thinkTimeConfig'
import ThinkTimeMultiplierInput from './ThinkTimeMultiplierInput'
import {
  HTTP_TRANSPORT_TAB_OPTIONS,
  MAX_SCRIPT_TRANSACTION_SLA_RULES,
  SCRIPT_PROTOCOL_OPTIONS,
  SCRIPT_TRANSACTION_SLA_COMPARATOR_OPTIONS,
  SCRIPT_TRANSACTION_SLA_DURATION_METRICS,
  SCRIPT_TRANSACTION_SLA_NON_PHASE_METRICS,
  SCRIPT_TRANSACTION_SLA_PHASE_OPTIONS,
  SCRIPT_TRANSACTION_SLA_SEVERITY_OPTIONS,
  buildScriptRuntimeSummary,
  collectScriptTransactionNames,
  formatBrowserEmulationSummary,
  formatLineList,
  formatRunBlockActions,
  formatRuntimeSettingStatusLabel,
  formatScriptProtocolLabel,
  getRuntimeProtocolCapabilities,
  httpTransportTabIsAvailable,
  isTransactionSLADurationMetric,
  parseArrivalSeed,
  parseCommaList,
  parseLineList,
  readImportedRuntimeFiles,
  rebuildRunBlockChildren,
  runBlockActionsWeightsSurviveParsing,
  scriptHasLLMActions,
  readImportedRuntimeSettingDispositions,
  summarizeImportedRuntimeDispositions,
  runtimeSettingsSectionIsAvailable,
  type HTTPTransportTabId,
  type RuntimeSettingsSectionId,
} from './scriptEditorRuntimeSettingsModel'
import { HTTP_VERSION_OPTIONS, formatScriptHTTPVersion, inheritedHTTPActionVersion, scriptHTTPVersionSettings } from './httpVersionModel'
import { HTTP3ExecutionNote } from './HTTP3ExecutionNote'
import { HTTPQUICSettingsFields } from './HTTPQUICSettingsFields'
import { http3PolicyIssues, patchQUICConfig, quicPanelDisabledReason } from './httpQUICModel'
import {
  RuntimeSettingsEditor,
  getTlsMaxVersionHttp3Warning,
  hasIgnoredSoapConfigOnHttpScript,
  hasReadOnlyScheduleCompatibility,
  type RuntimeSettingsEditorMode,
} from './runtime-settings'
import { formatPacingTiming } from './runtimeTiming'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import HttpRequestDefaultsEditor, {
  SocketCompatibilityPanel,
  type HTTPRuntimeActionType,
} from './HttpRequestDefaultsEditor'
import {
  countHTTPDefaultHeaders,
  countHTTPDefaultHeaderErrors,
  countPinnedCookies,
  countPinnedCookieErrors,
  formatHTTPSettingsSummary,
} from './httpRequestDefaultsModel'

const WEBKIT_INSTALL_ADVISORY = 'WebKit execution requires Playwright WebKit assets installed on the controller host. Run go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install webkit before running browser tests or replay sessions.'

function flattenRuntimeActions(actions: ScriptActionDefinition[]): ScriptActionDefinition[] {
  return actions.flatMap(action => [
    action,
    ...flattenRuntimeActions(action.control?.steps || []),
    ...flattenRuntimeActions(action.control?.else || []),
  ])
}

type DraftTextInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> & {
  /** Canonical text for the stored value. */
  value: string
  /**
   * Canonical text the stored value would have after committing raw, or null
   * when raw does not show what it stores (the stored value is shown instead).
   */
  normalize: (raw: string) => string | null
  /** Store the parsed value of raw (called on every keystroke). */
  onCommit: (raw: string) => void
}

/**
 * A list input that commits on every keystroke but keeps showing what the
 * author typed (a trailing "," or ":") while it still parses to the stored
 * value. Re-rendering from the parsed value alone would drop the separator
 * the moment it is typed.
 */
function DraftTextInput({ value, normalize, onCommit, onBlur, ...inputProps }: DraftTextInputProps) {
  const [draft, setDraft] = useState<string | null>(null)
  const normalized = draft !== null ? normalize(draft) : null
  const shown = draft !== null && normalized !== null && normalized === value ? draft : value
  return (
    <input
      {...inputProps}
      value={shown}
      onChange={event => {
        setDraft(event.target.value)
        onCommit(event.target.value)
      }}
      onBlur={event => {
        setDraft(null)
        onBlur?.(event)
      }}
    />
  )
}

function usesPersistentJavaVuserLifecycle(script: ScriptDefinition): boolean {
  const javaActions = flattenRuntimeActions([
    ...(script.init || []),
    ...(script.actions || []),
    ...(script.teardown || []),
  ]).filter(action => action.type === 'java_vuser_run')
  return javaActions.length > 0 && javaActions.every(action => {
    const method = String(action.params?.method || '').trim().toLowerCase()
    return method !== '' && method !== 'full_lifecycle'
  })
}

interface RuntimeParityEntry {
  area: string
  id: string
  status: string
}

interface ScriptRuntimeSettingsSidebarProps {
  parityEntries: RuntimeParityEntry[]
  paritySummary?: string
  readiness: ReactNode
  script: Pick<ScriptDefinition, 'config'> & Partial<Pick<ScriptDefinition, 'protocol'>>
  /** Opens the Imported Source Runtime Settings section for the full per-key list. */
  onOpenImportedSource?: () => void
}

export function ScriptRuntimeSettingsSidebar({
  parityEntries,
  paritySummary,
  readiness,
  script,
  onOpenImportedSource,
}: ScriptRuntimeSettingsSidebarProps) {
  const { httpProxySummary, logLevelLabel, runLogicLabel, thinkTimeLabel } = buildScriptRuntimeSummary(script)
  const httpVersionSummary = formatScriptHTTPVersion(script.protocol, script.config.http?.version)
  return (
    <>
      <div className="utility-card">
        <div className="utility-card-header">
          <strong>Runtime Summary</strong>
          <span>{runLogicLabel}</span>
        </div>
        <ul className="utility-list">
          <li className="utility-list-item">Think time: {thinkTimeLabel}</li>
          <li className="utility-list-item">Log level: {logLevelLabel}</li>
          <li className="utility-list-item">HTTP version: {httpVersionSummary}</li>
          <li className="utility-list-item">Proxy: {httpProxySummary}</li>
        </ul>
      </div>
      {readiness}
      {parityEntries.length > 0 && (
        <div className="utility-card">
          <div className="utility-card-header">
            <strong>Runtime Parity</strong>
            <span>{paritySummary || 'No parity issues'}</span>
          </div>
          <ul className="utility-list">
            {parityEntries.slice(0, 6).map(entry => (
              <li key={`${entry.id}:runtime-sidebar`} className="utility-list-item">
                {formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}
              </li>
            ))}
          </ul>
          {onOpenImportedSource && (
            <button type="button" className="btn btn-sm btn-secondary" onClick={onOpenImportedSource}>
              {parityEntries.length > 6 ? `View all ${parityEntries.length} in imported source settings` : 'View imported source settings'}
            </button>
          )}
        </div>
      )}
    </>
  )
}

export interface ScriptRuntimeSettingsViewProps {
  debugContext?: DebugRuntimeContext
  baselineConfig?: ScriptConfig
  hideSave?: boolean
  embeddedInCanonicalEditor?: boolean
  editorMode?: RuntimeSettingsEditorMode
  activeHTTPTransportTab: HTTPTransportTabId
  activeSection: RuntimeSettingsSectionId
  canEditProtocol?: boolean
  draftScope?: string
  onSave?: () => void
  onAddHTTPRuntimeAction?: (type: HTTPRuntimeActionType) => void
  onSelectHTTPTransportTab: (tab: HTTPTransportTabId) => void
  onSelectSection: (section: RuntimeSettingsSectionId) => void
  onUpdateField?: (path: string, value: unknown) => void
  onUpdateProtocol?: (protocol: string) => void
  onUpdateScript?: (updater: (prev: ScriptDefinition) => ScriptDefinition) => void
  saveDisabled?: boolean
  saveTitle?: string
  saving?: boolean
  script: ScriptDefinition
}

interface ExtendedQuotaPacingConfig {
  tpm?: number
  rpm?: number
  estimated_tokens_per_request?: number
  burst_tokens?: number
  adaptive_aimd?: boolean
  default_tokens?: number
  headroom_ratio?: number
}

interface ExtendedPacingRateStage {
  duration_seconds?: number
  target_tps?: number
  end_tps?: number
}

type ExtendedPacingConfig = NonNullable<ScriptConfig['pacing']> & {
  arrival_process?: 'uniform' | 'poisson' | 'gamma' | string
  burstiness?: number
  seed?: number
  rate_stages?: ExtendedPacingRateStage[]
  quota?: ExtendedQuotaPacingConfig
}

type ExtendedThinkTimeConfig = NonNullable<ScriptConfig['think_time']> & {
  distribution?: 'uniform' | 'exponential' | string
}

function buildThinkTimeConfigForType(
  current: Partial<ExtendedThinkTimeConfig> | undefined,
  nextTypeRaw: string,
): ExtendedThinkTimeConfig {
  const extendedCurrent = current
  if (nextTypeRaw === 'exponential') {
    const next: ExtendedThinkTimeConfig = {
      type: 'exponential',
      distribution: 'exponential',
      mean_ms: extendedCurrent?.mean_ms ?? 0,
      min_ms: extendedCurrent?.min_ms ?? 0,
      max_ms: extendedCurrent?.max_ms ?? 0,
      multiplier: extendedCurrent?.multiplier ?? 1,
      limit_ms: extendedCurrent?.limit_ms ?? 0,
    }
    if (extendedCurrent?.ignore) {
      next.ignore = true
    }
    if (extendedCurrent?.force) {
      next.force = true
    }
    return next
  }
  const cleanedCurrent: Partial<ExtendedThinkTimeConfig> = { ...(extendedCurrent || {}) }
  delete cleanedCurrent.distribution
  const base = thinkTimeConfigForType(
    cleanedCurrent as ScriptDefinition['config']['think_time'],
    nextTypeRaw,
  ) as ExtendedThinkTimeConfig
  if (extendedCurrent?.distribution === 'uniform' && nextTypeRaw === 'uniform') {
    base.distribution = 'uniform'
  }
  if (extendedCurrent?.force) {
    base.force = true
  }
  return base
}

/**
 * Help for the exponential cap base. Mirrors engine computeThinkTime: the
 * draw is capped at max_ms × 4, then the multiplier applies, then Limit (s).
 */
function exponentialThinkTimeCapHelp(think: { max_ms?: number; multiplier?: number } | undefined): string {
  const tail = 'Min (s) must not exceed this value. Use Limit (s) for a hard maximum.'
  const stopMs = exponentialThinkTimeStopMs(think)
  if (stopMs <= 0) return `Exponential draws stop at ${EXPONENTIAL_THINK_TIME_CAP_FACTOR} × this value. ${tail}`
  const multiplier = think?.multiplier
  if (typeof multiplier === 'number' && Number.isFinite(multiplier) && multiplier > 0 && multiplier !== 1) {
    return `Exponential draws stop at ${EXPONENTIAL_THINK_TIME_CAP_FACTOR} × this value, then the × ${multiplier} multiplier applies (${stopMs / 1000} s). ${tail}`
  }
  return `Exponential draws stop at ${EXPONENTIAL_THINK_TIME_CAP_FACTOR} × this value (${stopMs / 1000} s). ${tail}`
}

function applyFieldPathToScript(prev: ScriptDefinition, path: string, value: unknown): ScriptDefinition {
  if (path === 'version') {
    return { ...prev, version: String(value ?? '') }
  }
  if (path === 'goal_config') {
    return { ...prev, goal_config: value as ScriptDefinition['goal_config'] }
  }
  if (path === 'config') {
    return { ...prev, config: (value as ScriptConfig) || {} }
  }
  if (path.startsWith('config.')) {
    const subParts = path.slice('config.'.length).split('.')
    const nextConfig: Record<string, unknown> = { ...(prev.config || {}) }
    let cursor: Record<string, unknown> = nextConfig
    for (let i = 0; i < subParts.length - 1; i++) {
      const part = subParts[i]
      const current = cursor[part]
      const cloned = current && typeof current === 'object' && !Array.isArray(current)
        ? { ...(current as Record<string, unknown>) }
        : {}
      cursor[part] = cloned
      cursor = cloned
    }
    cursor[subParts[subParts.length - 1]] = value
    return {
      ...prev,
      config: { base_url: prev.config?.base_url ?? '', ...nextConfig } as ScriptConfig,
    }
  }
  return prev
}

export function ScriptRuntimeSettingsView({
  debugContext,
  baselineConfig,
  hideSave = false,
  embeddedInCanonicalEditor = false,
  editorMode,
  activeHTTPTransportTab,
  activeSection,
  canEditProtocol = false,
  draftScope,
  onSave = () => {},
  onAddHTTPRuntimeAction,
  onSelectHTTPTransportTab,
  onSelectSection,
  onUpdateField,
  onUpdateProtocol,
  onUpdateScript,
  saveDisabled = false,
  saveTitle,
  saving = false,
  script,
}: ScriptRuntimeSettingsViewProps) {
  const [search, setSearch] = useState('')
  const [modifiedOnly, setModifiedOnly] = useState(false)
  const timingIssues = runtimeTimingIssues(script.config)
  const httpVersionSettings = scriptHTTPVersionSettings(script.protocol, script.config.http?.version)
  // The policy the engine applies: an explicit config.http.version, else the
  // protocol alias default (protocol "http/3" inherits exact HTTP/3).
  const inheritedHTTPPolicy = inheritedHTTPActionVersion(script.protocol, script.config.http?.version)
  const effectiveHTTPPolicy = inheritedHTTPPolicy === null || inheritedHTTPPolicy === 'auto' ? undefined : inheritedHTTPPolicy
  // effectiveHTTPPolicy already resolves the protocol alias; resolving it
  // again would turn an explicit "auto" on an "http/3" script back into HTTP/3.
  const tlsMaxVersionHttp3Warning = getTlsMaxVersionHttp3Warning(
    effectiveHTTPPolicy,
    script.config.http?.tls?.max_version,
  )
  const ignoredSoapOnHttpNotice = hasIgnoredSoapConfigOnHttpScript(script.protocol, script.config)
  const readOnlyScheduleNotice = hasReadOnlyScheduleCompatibility(script.config)
  const capabilities = getRuntimeProtocolCapabilities(script.protocol)
  const {
    browser: isBrowserScript,
    database: isDatabaseScript,
    eventHubs: isEventHubsScript,
    grpc: isGrpcScript,
    httpTransport: isHttpTransportScript,
    javaHttp: isJavaHttpScript,
    javaVuser: isJavaVuserScript,
    jms: isJmsScript,
    kafka: isKafkaScript,
    network: isNetworkScript,
    appium: isAppiumScript,
    soap: isSoapScript,
    webSocket: isWebSocketScript,
  } = capabilities
  const jmsProvider = jmsProviderForProtocol(script.protocol)
  const eventHubsSummary = script.config.eventhubs?.mock
    ? 'Mock broker'
    : script.config.eventhubs?.namespace
      ? `${script.config.eventhubs.namespace}${script.config.eventhubs.event_hub ? ` · ${script.config.eventhubs.event_hub}` : ''}`
      : 'Not configured'
  const selectedBrowserEngine = script.config.browser?.engine || 'chromium'
  const selectedBrowserProfile = lookupBrowserDeviceProfile(script.config.browser?.device_profile)
  const browserEngineAdvisory = selectedBrowserProfile && selectedBrowserProfile.engine !== selectedBrowserEngine
    ? `${selectedBrowserProfile.name} requires ${selectedBrowserProfile.engine}. Choose a matching engine or switch to a compatible preset.`
    : selectedBrowserEngine === 'webkit'
      ? WEBKIT_INSTALL_ADVISORY
      : ''
  const { httpProxyMode, httpProxySummary } = buildScriptRuntimeSummary(script)
  const runtimeSectionIsActive = (section: RuntimeSettingsSectionId) => activeSection === section
  const selectRuntimeSettingsSection = onSelectSection
  const setActiveHTTPTransportTab = onSelectHTTPTransportTab
  const updateField = (path: string, value: unknown) => {
    if (onUpdateField) {
      onUpdateField(path, value)
      return
    }
    if (onUpdateScript) {
      onUpdateScript(prev => applyFieldPathToScript(prev, path, value))
    }
  }
  const updateProtocol = (protocol: string) => {
    if (onUpdateProtocol) {
      onUpdateProtocol(protocol)
      return
    }
    if (onUpdateScript) {
      onUpdateScript(prev => ({ ...prev, protocol }))
    }
  }
  const handleSave = onSave
  const saveScriptDisabled = saveDisabled
  const saveScriptTitle = saveTitle
  const persistentJavaVuserLifecycle = isJavaVuserScript && usesPersistentJavaVuserLifecycle(script)
  const importedJavaVMParameters = isJavaVuserScript ? readImportedJavaVMParameters(script.metadata?.import) : []
  const supportsBrowserEmulation = capabilities.browserEmulation
  const rawThinkTime = script.config.think_time as ExtendedThinkTimeConfig | undefined
  const thinkTimeType =
    rawThinkTime?.type === 'exponential' || rawThinkTime?.distribution === 'exponential'
      ? 'exponential'
      : canonicalThinkTimeType(rawThinkTime?.type || 'fixed')
  const updateThinkTime = (patch: Partial<ExtendedThinkTimeConfig>) => {
    updateField('config.think_time', buildThinkTimeConfigForType({
      ...rawThinkTime,
      ...patch,
    }, thinkTimeType))
  }
  const pacingConfig = script.config.pacing as ExtendedPacingConfig | undefined
  const updatePacingConfig = (patch: Partial<ExtendedPacingConfig>) => {
    updateField('config.pacing', {
      ...(pacingConfig || {}),
      ...patch,
    })
  }
  // The engine builds the quota pacer whenever config.pacing.quota is set,
  // independent of pacing.type, and it throttles only LLM actions.
  const scriptUsesLLMActions = scriptHasLLMActions(script)
  // dsl.CanonicalPacingType: a stored "TPS" or " tps " is TPS pacing at runtime.
  const pacingType = canonicalPacingType(pacingConfig?.type)
  const showQuotaEditor = scriptUsesLLMActions || pacingConfig?.quota !== undefined || pacingType === 'quota'
  // Arrival process, burstiness, seed and rate stages are read only by the
  // open-workload scheduler; closed TPS pacing uses a constant token bucket.
  const pacingIsOpenWorkload = isOpenWorkloadModel(script.config)
  // The engine and validation trim and lowercase the arrival process: a stored
  // "Gamma" runs gamma arrivals.
  const arrivalProcess = (pacingConfig?.arrival_process ?? '').trim().toLowerCase()
  const closedWorkloadHasOpenArrivalSettings = Boolean(
    pacingConfig?.arrival_process || pacingConfig?.burstiness !== undefined || pacingConfig?.seed !== undefined || pacingConfig?.rate_stages?.length,
  )
  const updatePacingQuota = (patch: Partial<ExtendedQuotaPacingConfig>) => {
    updatePacingConfig({
      quota: {
        ...(pacingConfig?.quota || {}),
        ...patch,
      },
    })
  }

  // The 'browser' sub-tab edits config.browser.* (Playwright runtime) and is
  // shown only for browser-protocol scripts; HTTP scripts use the top-level
  // Browser Emulation section. Clamp a stale hidden tab to the first visible.
  const visibleHTTPTransportTabs = HTTP_TRANSPORT_TAB_OPTIONS.filter(tab => httpTransportTabIsAvailable(tab.id, script.protocol))
  const effectiveHTTPTransportTab: HTTPTransportTabId = visibleHTTPTransportTabs.some(tab => tab.id === activeHTTPTransportTab)
    ? activeHTTPTransportTab
    : (visibleHTTPTransportTabs[0]?.id ?? 'request_defaults')
  const browserTransportTabVisible = visibleHTTPTransportTabs.some(tab => tab.id === 'browser')

  // Browser-emulation catalog: seeded with the bundled fallback, refreshed from
  // the controller so dropdowns stay in lockstep with replay UA resolution.
  const browserEmulationCatalog = useBrowserEmulationCatalog(supportsBrowserEmulation)

  // Imported source runtime settings: the preserved raw cfg/usp sections plus
  // the per-key dispositions the importer recorded for the selected profile.
  const importedRuntimeFiles = readImportedRuntimeFiles(script.metadata?.import)
  const importedRuntimeDispositions = readImportedRuntimeSettingDispositions(script.metadata?.import)
  const importedRuntimeFileNames = Object.keys(importedRuntimeFiles).sort((left, right) => {
    const leftSelected = importedRuntimeDispositions[left] ? 0 : 1
    const rightSelected = importedRuntimeDispositions[right] ? 0 : 1
    if (leftSelected !== rightSelected) return leftSelected - rightSelected
    return left.localeCompare(right)
  })
  const hasPreservedRunOrder = script.config.run_logic?.block_scheduling !== undefined || Boolean(script.config.run_logic?.action_sequence?.length) || Boolean(script.config.run_logic?.blocks?.length)
  const hasImportedRuntimeFiles = importedRuntimeFileNames.length > 0
  const importedDispositionSummary = summarizeImportedRuntimeDispositions(importedRuntimeDispositions)

  const browserEmulation = script.config.browser_emulation
  const browserEmulationMode = browserEmulation?.mode === 'custom' ? 'custom' : browserEmulation?.mode === 'specific' ? 'specific' : ''
  const browserEmulationCache = browserEmulation?.cache
  const selectedEmulationBrowser = browserEmulationCatalog.browsers.find(browser => browser.type === browserEmulation?.browser_type)
  const updateBrowserEmulation = (patch: Record<string, unknown>) => {
    updateField('config.browser_emulation', { ...(script.config.browser_emulation || {}), ...patch })
  }
  const updateBrowserEmulationCache = (patch: Record<string, unknown>) => {
    updateField('config.browser_emulation', {
      ...(script.config.browser_emulation || {}),
      cache: { ...(script.config.browser_emulation?.cache || {}), ...patch },
    })
  }
  const httpSettingsValue = {
    ...(script.config.http || {}),
    cache_enabled: script.config.browser_emulation?.cache?.simulate_cache ?? script.config.http?.cache_enabled ?? false,
  }
  const requestDefaultErrorCount = countHTTPDefaultHeaderErrors(httpSettingsValue)
  const pinnedCookieErrorCount = countPinnedCookieErrors(httpSettingsValue)
  const updateHTTPSettings = (patch: Partial<NonNullable<ScriptDefinition['config']['http']>>) => {
    updateField('config.http', { ...script.config.http, ...patch })
    if (Object.prototype.hasOwnProperty.call(patch, 'cache_enabled')) {
      updateBrowserEmulationCache(patch.cache_enabled
        ? { simulate_cache: true }
        : {
            simulate_cache: false,
            cache_content_urls: undefined,
            check_newer_versions: undefined,
          })
    }
  }

  const transactionDatalistId = useId()
  const transactionRules = script.goal_config?.transaction_rules || []
  const knownTransactionNames = collectScriptTransactionNames(script)
  const commitTransactionRules = (nextRules: TransactionSLARule[]) => {
    updateField('goal_config', {
      ...(script.goal_config || {}),
      transaction_rules: nextRules.length > 0 ? nextRules : undefined,
    })
  }
  const addTransactionRule = () => {
    if (transactionRules.length >= MAX_SCRIPT_TRANSACTION_SLA_RULES) return
    const nextRule: TransactionSLARule = {
      transaction: knownTransactionNames[0] || 'Checkout',
      metric: 'p95_duration_ms',
      comparator: 'lte',
      threshold: 500,
      severity: 'fail',
    }
    commitTransactionRules([...transactionRules, nextRule])
  }
  const updateTransactionRule = (index: number, patch: Partial<TransactionSLARule>) => {
    const nextRules = transactionRules.map((rule, ruleIndex) => {
      if (ruleIndex !== index) return rule
      const next: TransactionSLARule = { ...rule, ...patch }
      if (Object.prototype.hasOwnProperty.call(patch, 'phase') && !patch.phase) {
        delete next.phase
      }
      return next
    })
    commitTransactionRules(nextRules)
  }
  const removeTransactionRule = (index: number) => {
    commitTransactionRules(transactionRules.filter((_, ruleIndex) => ruleIndex !== index))
  }
  const transactionSLAValidationError = transactionRules.length > 0
    ? validateGoalConfig({ ...(script.goal_config || {}), transaction_rules: transactionRules })
    : null

  const categoryModified = (category: typeof RUNTIME_WORKSPACE_CATEGORIES[number]) => category.id === 'imported_source'
    ? ['block_scheduling', 'action_sequence', 'blocks'].some(field => JSON.stringify(script.config.run_logic?.[field as keyof NonNullable<ScriptConfig['run_logic']>]) !== JSON.stringify(baselineConfig?.run_logic?.[field as keyof NonNullable<ScriptConfig['run_logic']>]))
    : category.fields.some(field => JSON.stringify(script.config[field]) !== JSON.stringify(baselineConfig?.[field]))
      || (category.id === 'errors' && transactionRules.length > 0)
  const visibleCategories = RUNTIME_WORKSPACE_CATEGORIES.filter(category => {
    const terms = `${category.label} ${category.group} ${category.fields.join(' ')} ${RUNTIME_SEARCH_TERMS[category.id] || ''}`.toLowerCase()
    return runtimeSettingsSectionIsAvailable(category.id, script.protocol) && (category.id !== 'imported_source' || hasImportedRuntimeFiles || hasPreservedRunOrder) && search.toLowerCase().trim().split(/\s+/).every(term => terms.includes(term)) && (!modifiedOnly || categoryModified(category))
  })
  const activeSectionIsVisible = visibleCategories.some(category => category.id === activeSection)

  const workspaceBody = (
<section className="center-document runtime-settings config-panel center-document-runtime" style={{ padding: 0 }} aria-label="Runtime Settings">
  {!hideSave && <div className="center-document-header runtime-settings-save-header">
    <div className="runtime-settings-header-actions">
      <button type="button" className="btn btn-primary" onClick={handleSave} disabled={saveScriptDisabled} title={saveScriptTitle}>
        {saving ? 'Saving…' : 'Save Script'}
      </button>
    </div>
  </div>}
  <RuntimeContextSummary script={script} debug={debugContext}>
    <label className="runtime-settings-search">Find setting<input type="search" autoComplete="off" value={search} onChange={event => setSearch(event.target.value)} placeholder="Timing, proxy, logging…" /></label>
    <label className="checkbox-label runtime-settings-modified-toggle"><input type="checkbox" checked={modifiedOnly} onChange={event => setModifiedOnly(event.target.checked)} />Modified only</label>
  </RuntimeContextSummary>
  {ignoredSoapOnHttpNotice && (
    <div className="parameter-empty-state" role="status" data-testid="http-ignored-soap-config-notice">
      Read-only diagnostic: config.soap is present on this HTTP script, but SOAP envelope, WS-Security, and fault settings are ignored when script.protocol is http.
    </div>
  )}
  {timingIssues.length > 0 && <ul className="runtime-timing-issues" role="alert">{timingIssues.map(issue => <li key={issue}>{issue}</li>)}</ul>}
  <div className="runtime-settings-grid">
  <nav className="runtime-settings-tabs" aria-label="Runtime setting categories">
    {visibleCategories.length === 0 && <p className="runtime-settings-empty" role="status">No categories match. Clear the search or turn off Modified only.</p>}
    {visibleCategories.length > 0 && <TabList className="scenario-run-settings-tabs runtime-settings-tab-list" aria-label="Runtime settings sections" aria-orientation="vertical" onKeyDown={handleTabKey}>
      {visibleCategories.map(category => {
        const changed = categoryModified(category)
        const summary = category.id === 'imported_source'
          ? hasImportedRuntimeFiles ? `${importedDispositionSummary.total} settings dispositioned` : 'Preserved run order'
          : changed ? (baselineConfig ? 'Modified' : 'Configured') : 'Saved / default'
        return <button type="button" role="tab" key={category.id} id={`runtime-settings-tab-${category.id}`} aria-controls="runtime-settings-tab-panel" aria-selected={runtimeSectionIsActive(category.id)} tabIndex={runtimeSectionIsActive(category.id) ? 0 : -1} className={`scenario-run-settings-tab runtime-settings-tab ${runtimeSectionIsActive(category.id) ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection(category.id)}>
          <RuntimeSectionIcon section={category.id} />
          <span className="runtime-settings-tab-label">{category.label}</span>
          <span className="runtime-settings-tab-summary">{summary}{['think', 'pacing', 'runlogic'].includes(category.id) && timingIssues.length ? ` · ${timingIssues.length} timing issues` : ''}</span>
        </button>
      })}
    </TabList>}
  </nav>
  <div className="runtime-settings-detail" role="tabpanel" id="runtime-settings-tab-panel" aria-labelledby={activeSectionIsVisible ? `runtime-settings-tab-${activeSection}` : 'runtime-settings-active-heading'}>
  <h3 id="runtime-settings-active-heading" className="runtime-active-heading">{RUNTIME_WORKSPACE_CATEGORIES.find(category => category.id === activeSection)?.label}</h3>
  {/* Script Configuration */}
  <div className={`settings-section ${runtimeSectionIsActive('script_config') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('script_config')}>
      <RuntimeSectionIcon section="script_config" />
      <span className="section-label">Script Configuration</span>
      <span className="section-summary">{formatScriptProtocolLabel(script.protocol)}</span>
      <span className="chevron">{runtimeSectionIsActive('script_config') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('script_config') && (
      <div className="section-body">
        {readOnlyScheduleNotice && (
          <div className="parameter-empty-state" role="status" data-testid="readonly-schedule-compatibility-notice" style={{ marginBottom: 12 }}>
            Read-only compatibility notice: imported JMeter or sampler schedule metadata (config.jmeter / config.sampler_schedules) is preserved on this script for scenario conversion.
          </div>
        )}
        <NativeServicesEditor declarations={script.config.native_services?.live_tables} onChange={live_tables => updateField('config.native_services', { ...script.config.native_services, live_tables })} />
        <div className="form-grid">
          <label>
            <span>Protocol</span>
            {canEditProtocol ? (
              <select value={script.protocol} onChange={e => updateProtocol(e.target.value)} aria-label="Protocol">
                {SCRIPT_PROTOCOL_OPTIONS.map(option => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </select>
            ) : (
              <div className="form-readonly-value" aria-label="Protocol">
                <strong>{formatScriptProtocolLabel(script.protocol)}</strong>
                <small>{script.protocol}</small>
              </div>
            )}
          </label>
          <label>
            <span>Version</span>
            <input value={script.version} onChange={e => updateField('version', e.target.value)} placeholder="1.0.0" />
          </label>
          {capabilities.requiresBaseURL && (
            <label>
              <span>{isGrpcScript ? 'gRPC Target' : isWebSocketScript ? 'WebSocket Origin' : 'Base URL'}</span>
              <input data-author-field="base_url" required={requiresScriptBaseURL(script.protocol)} value={script.config.base_url} onChange={e => updateField('config.base_url', e.target.value)} placeholder={isGrpcScript ? 'dns:///api.example.com:443' : isWebSocketScript ? 'wss://api.example.com' : 'https://api.example.com'} />
            </label>
          )}
          {isDatabaseScript && (
            <>
              <label data-catalog-path="config.database.driver">
                <span>Driver</span>
                {/* Driver is required: an unset driver shows as unset, never as a
                    driver the script does not store. */}
                <select aria-label="Database driver" value={script.config.database?.driver || ''} onChange={e => updateField('config.database.driver', e.target.value || undefined)}>
                  {!script.config.database?.driver && <option value="">Choose a driver (required)</option>}
                  {selectOptionsWithStored('config.database.driver', script.config.database?.driver).map(option => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </label>
              <label data-catalog-path="config.database.dsn_secret">
                <span>DSN Secret</span>
                <input value={script.config.database?.dsn_secret || ''} onChange={e => updateField('config.database.dsn_secret', e.target.value)} placeholder="DB_DSN" autoComplete="off" spellCheck={false} />
                {script.config.database?.dsn_secret?.trim() && <small className="field-hint" data-secret-reference-note>Project secret {script.config.database.dsn_secret.trim()} is resolved at run time.</small>}
              </label>
              <CredentialField fullWidth label="DSN / Fixture" path="config.database.dsn" value={script.config.database?.dsn}
                onChange={value => updateField('config.database.dsn', value)} placeholder={databaseDSNPlaceholder(script.config.database?.driver)} />
            </>
          )}
          {isNetworkScript && (
            <>
              <SelectField label="Subprotocol" path="config.network.subprotocol" value={script.config.network?.subprotocol} defaultValue="tcp"
                onChange={value => updateField('config.network.subprotocol', value)} />
              <label>
                <span>Host</span>
                <input value={script.config.network?.host || ''} onChange={e => updateField('config.network.host', e.target.value)} placeholder="localhost" />
              </label>
              <label>
                <span>Port</span>
                <input type="number" value={script.config.network?.port || 0} onChange={e => updateField('config.network.port', Number(e.target.value) || undefined)} placeholder="443" />
              </label>
            </>
          )}
          {isJmsScript && (
            <>
              <label>
                <span>Broker URL</span>
                <input value={script.config.jms?.broker_url || ''} onChange={e => updateField('config.jms.broker_url', e.target.value)} placeholder="tcps://broker.example.com:55443" />
              </label>
              {jmsProvider === 'solace' && (
                <label>
                  <span>Message VPN</span>
                  <input value={script.config.jms?.message_vpn || ''} onChange={e => updateField('config.jms.message_vpn', e.target.value)} placeholder="default" />
                </label>
              )}
              <label>
                <span>Default Destination</span>
                <input value={script.config.jms?.destination || ''} onChange={e => updateField('config.jms.destination', e.target.value)} placeholder="orders.in" />
              </label>
            </>
          )}
        </div>
        {isJmsScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            {formatScriptProtocolLabel(script.protocol)} scripts use publish, consume, and request/reply actions with message body, headers, properties, correlation IDs, and reply destinations in the action editor below.
          </div>
        )}
        {isBrowserScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            Browser scripts can use navigate, click, fill, select, wait, tap, long-press, swipe, and screenshot actions with device presets, touch controls, and client metrics in the action editor below.
          </div>
        )}
        {isSoapScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            SOAP scripts reuse the HTTP transport model and add SOAP envelope, namespace, WS-Security, SOAPAction, and fault-handling settings below.
          </div>
        )}
        {isJavaHttpScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            Java-over-HTTP scripts reuse the HTTP transport client, support embedded or sidecar helper modes, and currently ship with the `java_serialized_object_base64` codec where request bodies are base64-encoded serialized bytes.
          </div>
        )}
        {isWebSocketScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            WebSocket scripts model connection lifecycle, message send/receive steps, handshake headers, subprotocol/origin settings, and transcript evidence for replay.
          </div>
        )}
        {isGrpcScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            gRPC scripts model service/method calls, reflection or proto setup, metadata/TLS/authority, stream controls, and gRPC status evidence.
          </div>
        )}
        {isDatabaseScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            Database scripts model secret-backed DSNs, query/exec/transaction actions, bind parameters, row/scalar assertions, extractors, and read-only replay guards.
          </div>
        )}
        {isNetworkScript && (
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            Network scripts model DNS, TCP, UDP, Winsock-style sockets, FTP, SMTP, POP3, IMAP, and LDAP actions with transcript and record evidence.
          </div>
        )}
      </div>
    )}
  </div>
  {/* Think Time */}
  <div className={`settings-section ${runtimeSectionIsActive('think') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('think')}>
      <RuntimeSectionIcon section="think" />
      <span className="section-label">Think Time</span>
      <span className="section-summary">
        {effectiveThinkTimeLabel(script.config)}
      </span>
      <span className="chevron">{runtimeSectionIsActive('think') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('think') && (
      <div className="section-body">
        <label className="checkbox-label"><input type="checkbox" aria-label="Enable think time" checked={!script.config.think_time?.ignore && thinkTimeType !== 'none'} onChange={event => updateField('config.think_time', { ...script.config.think_time, ignore: !event.target.checked, type: thinkTimeType === 'none' ? 'fixed' : thinkTimeType })} />Enable think time</label>
        <p>{effectiveThinkTimeLabel(script.config)}. Think time occurs between actions; iteration pacing controls when the next iteration begins.</p>
        <fieldset disabled={script.config.think_time?.ignore || thinkTimeType === 'none'}><legend className="sr-only">Think time distribution</legend><div className="form-grid">
          <label>
            <span>Distribution</span>
            <select value={thinkTimeType}
              onChange={e => updateField(
                'config.think_time',
                buildThinkTimeConfigForType(script.config.think_time, e.target.value),
              )}>
              <option value="fixed">Fixed</option>
              <option value="recorded">Replay think time as recorded</option>
              <option value="random_percentage">Random percentage of recorded think time</option>
              <option value="gaussian">Gaussian (Normal)</option>
              <option value="uniform">Uniform (Random)</option>
              <option value="exponential">Exponential</option>
            </select>
          </label>
          {thinkTimeType === 'random_percentage' && <ThinkTimePercentageInputs value={script.config.think_time} onChange={updateThinkTime} />}
          {(thinkTimeUsesMean(thinkTimeType) || thinkTimeType === 'exponential') && (
            <label>
              <span>Mean (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.think_time?.mean_ms}
                onChangeMs={value => updateThinkTime({ mean_ms: value ?? 0 })} />
            </label>
          )}
          {thinkTimeUsesStdDeviation(thinkTimeType) && (
            <label>
              <span>Std Dev (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.think_time?.std_ms}
                onChangeMs={value => updateThinkTime({ std_ms: value ?? 0 })} />
            </label>
          )}
          {(thinkTimeUsesBounds(thinkTimeType) || thinkTimeType === 'exponential') && (
            <>
              <label>
                <span>Min (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.think_time?.min_ms}
                  onChangeMs={value => updateThinkTime({ min_ms: value ?? 0 })} />
              </label>
              <label>
                <span>{thinkTimeType === 'exponential' ? 'Cap base (s)' : 'Max (s)'}</span>
                <SecondsFromMillisecondsInput valueMs={script.config.think_time?.max_ms}
                  onChangeMs={value => updateThinkTime({ max_ms: value ?? 0 })} />
                {thinkTimeType === 'exponential' && (
                  <small data-testid="exponential-think-cap-help">
                    {exponentialThinkTimeCapHelp(script.config.think_time)}
                  </small>
                )}
              </label>
            </>
          )}
          <ThinkTimeMultiplierInput
            draftScope={draftScope}
            label="Multiplier"
            value={script.config.think_time?.multiplier}
            onValueChange={multiplier => updateThinkTime({ multiplier })}
          />
          <label>
            <span>Limit (s)</span>
            <SecondsFromMillisecondsInput valueMs={script.config.think_time?.limit_ms} placeholder="0 = no limit"
              onChangeMs={value => updateThinkTime({ limit_ms: value ?? 0 })} />
          </label>
          {script.config.think_time?.force && (
            // The engine reads force only from an action's think_time; the
            // script-level policy drops it, so it is not offered as a control.
            <p className="full-width" role="note" data-testid="script-think-time-force-notice">
              Force exact think time is set on the script, where runs ignore it: it applies only to an action&apos;s own think time.{' '}
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                onClick={() => {
                  const nextThinkTime = { ...script.config.think_time }
                  delete nextThinkTime.force
                  updateField('config.think_time', nextThinkTime)
                }}
              >
                Clear script-level force
              </button>
            </p>
          )}

        </div></fieldset>
      </div>
    )}
  </div>

  {/* Pacing */}
  <div className={`settings-section ${runtimeSectionIsActive('pacing') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('pacing')}>
      <RuntimeSectionIcon section="pacing" />
      <span className="section-label">Iteration Pacing</span>
      <span className="section-summary">{formatPacingTiming(script.config.pacing) || 'None'}</span>
      <span className="chevron">{runtimeSectionIsActive('pacing') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('pacing') && (
      <div className="section-body">
        <p>TPS controls the group iteration rate: open mode schedules arrivals, and closed mode shares a limiter across virtual users. It is not the number of HTTP requests per second. Scenario distribution determines each generator&apos;s share.</p>
        {pacingIsOpenWorkload && <p>Open workloads require TPS pacing. Change the workload model in Run Logic to choose a delay or interval mode.</p>}
        <div className="form-grid">
          <label className="full-width">
            <span>Pacing Mode</span>
            <select disabled={pacingIsOpenWorkload} value={pacingType}
              onChange={e => updateField('config.pacing', { ...script.config.pacing, type: e.target.value })}>
              <option value="none">No delay (as fast as possible)</option>
              <option value="fixed_delay">Fixed delay after iteration</option>
              <option value="random_delay">Random delay after iteration</option>
              <option value="fixed_interval">Fixed interval from iteration start</option>
              <option value="random_interval">Random interval from iteration start</option>
              <option value="tps">Target iteration rate (TPS)</option>
              {pacingType === 'quota' && <option value="quota">Quota only (no iteration pacing)</option>}
            </select>
          </label>
          {pacingType === 'quota' && (
            <p className="full-width" role="note" data-testid="pacing-quota-mode-notice">
              The quota mode does not pace iterations. The LLM request quota below applies in every pacing mode, so choose the iteration pacing you want here.
            </p>
          )}
          {pacingType === 'fixed_delay' && (
            <label><span>Delay (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.delay_ms}
                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, delay_ms: value ?? 0 })} />
            </label>
          )}
          {pacingType === 'random_delay' && (
            <>
              <label><span>Min Delay (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.pacing?.min_delay_ms}
                  onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, min_delay_ms: value ?? 0 })} />
              </label>
              <label><span>Max Delay (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.pacing?.max_delay_ms}
                  onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, max_delay_ms: value ?? 0 })} />
              </label>
            </>
          )}
          {pacingType === 'fixed_interval' && (
            <label><span>Interval (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.interval_ms}
                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, interval_ms: value ?? 0 })} />
            </label>
          )}
          {pacingType === 'random_interval' && (
            <>
              <label><span>Min Interval (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.pacing?.min_interval_ms}
                  onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, min_interval_ms: value ?? 0 })} />
              </label>
              <label><span>Max Interval (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.pacing?.max_interval_ms}
                  onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, max_interval_ms: value ?? 0 })} />
              </label>
            </>
          )}
          {/* Validation checks leftover arrival settings for every pacing
              mode, so the clear action shows whatever the closed workload's
              pacing mode is, not only for TPS. */}
          {!pacingIsOpenWorkload && closedWorkloadHasOpenArrivalSettings && (
            <p className="full-width" role="note" data-testid="pacing-open-only-settings-notice">
              Arrival process, burstiness, arrival seed and rate stages apply only to open workloads. {pacingType === 'tps'
                ? 'This closed workload paces at a constant Target TPS and ignores them.'
                : 'This closed workload ignores them, but invalid leftover values still fail validation.'}{' '}
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                onClick={() => updatePacingConfig({
                  arrival_process: undefined,
                  burstiness: undefined,
                  seed: undefined,
                  rate_stages: undefined,
                })}
              >
                Clear open-workload settings
              </button>
            </p>
          )}
          {pacingType === 'tps' && (
            <>
              <label><span>Target TPS</span>
                <input type="number" min="0" step="0.1" value={script.config.pacing?.tps ?? ''}
                  onChange={e => updateField('config.pacing', { ...script.config.pacing, tps: Number(e.target.value) })} />
              </label>
              {!pacingIsOpenWorkload && !closedWorkloadHasOpenArrivalSettings && (
                <p className="full-width" data-testid="pacing-open-only-settings-hint">
                  Arrival process, arrival seed and rate stages are available when Run Logic uses the open arrival-rate workload model.
                </p>
              )}
              {pacingIsOpenWorkload && <>
              <label>
                <span>Arrival process</span>
                <select
                  aria-label="Arrival process"
                  value={arrivalProcess}
                  onChange={e => {
                    const nextProcess = e.target.value || undefined
                    // Gamma arrivals fail validation without a burstiness above 0.
                    const needsBurstiness = nextProcess === 'gamma' && !((pacingConfig?.burstiness ?? 0) > 0)
                    updatePacingConfig({
                      arrival_process: nextProcess,
                      ...(needsBurstiness ? { burstiness: 1 } : {}),
                    })
                  }}
                >
                  <option value="">Default (uniform)</option>
                  <option value="uniform">Uniform</option>
                  <option value="poisson">Poisson</option>
                  <option value="gamma">Gamma</option>
                </select>
              </label>
              {arrivalProcess === 'gamma' && (
                <label>
                  <span>Burstiness</span>
                  <input
                    type="number"
                    min="0.1"
                    max="1000"
                    step="0.1"
                    aria-label="Arrival burstiness"
                    value={pacingConfig?.burstiness ?? ''}
                    placeholder="Required: above 0, up to 1000"
                    onChange={e => {
                      const parsed = Number(e.target.value)
                      updatePacingConfig({
                        burstiness: e.target.value === '' || !Number.isFinite(parsed) ? undefined : parsed,
                      })
                    }}
                  />
                  <small>1.0 matches Poisson; higher values make arrivals burstier.</small>
                </label>
              )}
              <label>
                <span>Arrival seed</span>
                <input
                  type="number"
                  step="1"
                  aria-label="Arrival seed"
                  value={pacingConfig?.seed ?? ''}
                  placeholder="Empty = random each run"
                  onChange={e => {
                    // Any number, 0 included, fixes the arrival sequence; only
                    // an unset seed is random per run.
                    updatePacingConfig({ seed: parseArrivalSeed(e.target.value) })
                  }}
                />
                <small>Any value, including 0, repeats the same arrival sequence. Leave empty for random arrivals.</small>
              </label>
              <div className="settings-sub-group full-width" data-testid="pacing-rate-stages-editor">
                <div className="sub-header">
                  <h5>Rate stages</h5>
                  <button
                    type="button"
                    className="btn btn-sm btn-secondary"
                    onClick={() => updatePacingConfig({
                      rate_stages: [
                        ...(pacingConfig?.rate_stages || []),
                        { duration_seconds: 30, target_tps: pacingConfig?.tps ?? 10 },
                      ],
                    })}
                  >
                    Add rate stage
                  </button>
                </div>
                {(pacingConfig?.rate_stages || []).map((stage, stageIdx) => (
                  <div key={`rate-stage-${stageIdx}`} className="pair-row" style={{ marginTop: 8 }}>
                    <input
                      type="number"
                      min="1"
                      max="3600"
                      aria-label={`Rate stage ${stageIdx + 1} duration`}
                      value={stage.duration_seconds ?? ''}
                      placeholder="30"
                      onChange={e => {
                        const nextStages = [...(pacingConfig?.rate_stages || [])]
                        nextStages[stageIdx] = {
                          ...stage,
                          duration_seconds:
                            e.target.value === ''
                              ? undefined
                              : Math.min(3600, Math.max(1, Number(e.target.value) || 1)),
                        }
                        updatePacingConfig({ rate_stages: nextStages })
                      }}
                    />
                    <input
                      type="number"
                      min="0"
                      step="0.1"
                      aria-label={`Rate stage ${stageIdx + 1} target TPS`}
                      value={stage.target_tps ?? ''}
                      placeholder="Target TPS"
                      onChange={e => {
                        const nextStages = [...(pacingConfig?.rate_stages || [])]
                        nextStages[stageIdx] = {
                          ...stage,
                          target_tps: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                        }
                        updatePacingConfig({ rate_stages: nextStages })
                      }}
                    />
                    <input
                      type="number"
                      min="0"
                      step="0.1"
                      aria-label={`Rate stage ${stageIdx + 1} end TPS`}
                      value={stage.end_tps ?? ''}
                      placeholder="End TPS (optional)"
                      onChange={e => {
                        const nextStages = [...(pacingConfig?.rate_stages || [])]
                        nextStages[stageIdx] = {
                          ...stage,
                          end_tps: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                        }
                        updatePacingConfig({ rate_stages: nextStages })
                      }}
                    />
                    <button
                      type="button"
                      className="btn btn-sm btn-ghost"
                      aria-label={`Remove rate stage ${stageIdx + 1}`}
                      onClick={() => {
                        const nextStages = (pacingConfig?.rate_stages || []).filter((_, idx) => idx !== stageIdx)
                        updatePacingConfig({ rate_stages: nextStages.length > 0 ? nextStages : undefined })
                      }}
                    >
                      Remove
                    </button>
                  </div>
                ))}
              </div>
              </>}
            </>
          )}
          {showQuotaEditor && (
            <div className="settings-sub-group full-width" data-testid="pacing-quota-editor">
              <div className="sub-header">
                <h5>LLM request quota</h5>
                {pacingConfig?.quota !== undefined && (
                  <button type="button" className="btn btn-sm btn-ghost" onClick={() => updatePacingConfig({ quota: undefined })}>
                    Clear quota
                  </button>
                )}
              </div>
              <p>Applies in every pacing mode, and only to LLM actions: llm_request steps and HTTP steps marked with params.llm, llm_dialect or stream_profile.</p>
              {!scriptUsesLLMActions && (
                <p role="note" data-testid="pacing-quota-no-llm-notice">This script has no LLM actions, so the quota throttles nothing.</p>
              )}
              <div className="form-grid">
              <label>
                <span>Tokens per minute (TPM)</span>
                <input
                  type="number"
                  min="0"
                  aria-label="Quota TPM"
                  value={pacingConfig?.quota?.tpm ?? ''}
                  placeholder="60000"
                  onChange={e => updatePacingQuota({
                    tpm: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                  })}
                />
              </label>
              <label>
                <span>Requests per minute (RPM)</span>
                <input
                  type="number"
                  min="0"
                  aria-label="Quota RPM"
                  value={pacingConfig?.quota?.rpm ?? ''}
                  placeholder="600"
                  onChange={e => updatePacingQuota({
                    rpm: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                  })}
                />
              </label>
              <label>
                <span>Estimated tokens per request</span>
                <input
                  type="number"
                  min="0"
                  aria-label="Estimated tokens per request"
                  value={pacingConfig?.quota?.estimated_tokens_per_request ?? ''}
                  placeholder="256"
                  onChange={e => updatePacingQuota({
                    estimated_tokens_per_request: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                  })}
                />
              </label>
              <label>
                <span>Burst tokens</span>
                <input
                  type="number"
                  min="0"
                  aria-label="Quota burst tokens"
                  value={pacingConfig?.quota?.burst_tokens ?? ''}
                  placeholder="2000"
                  onChange={e => updatePacingQuota({
                    burst_tokens: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                  })}
                />
              </label>
              <label>
                <span>Default tokens</span>
                <input
                  type="number"
                  min="0"
                  aria-label="Quota default tokens"
                  value={pacingConfig?.quota?.default_tokens ?? ''}
                  placeholder="128"
                  onChange={e => updatePacingQuota({
                    default_tokens: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                  })}
                />
              </label>
              <label>
                <span>Headroom ratio</span>
                <input
                  type="number"
                  min="0"
                  max="1"
                  step="0.05"
                  aria-label="Quota headroom ratio"
                  value={pacingConfig?.quota?.headroom_ratio ?? ''}
                  placeholder="0.9"
                  onChange={e => updatePacingQuota({
                    headroom_ratio: e.target.value === '' ? undefined : Math.min(1, Math.max(0, Number(e.target.value) || 0)),
                  })}
                />
              </label>
              <label className="checkbox-label full-width">
                <input
                  type="checkbox"
                  aria-label="Adaptive AIMD throttling"
                  checked={Boolean(pacingConfig?.quota?.adaptive_aimd)}
                  onChange={e => updatePacingQuota({ adaptive_aimd: e.target.checked })}
                />
                <span>Adaptive AIMD throttling</span>
              </label>
              </div>
            </div>
          )}
        </div>
      </div>
    )}
  </div>

  {/* Run Logic */}
  <div className={`settings-section ${runtimeSectionIsActive('runlogic') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('runlogic')}>
      <RuntimeSectionIcon section="runlogic" />
      <span className="section-label">Run Logic</span>
      <span className="section-summary">
        {['iterations', 'global_iterations', 'max_transactions'].filter(field => Number(script.config.run_logic?.[field as keyof NonNullable<ScriptConfig['run_logic']>]) > 0).length} active count limits
      </span>
      <span className="chevron">{runtimeSectionIsActive('runlogic') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('runlogic') && (
      <div className="section-body">
        <div className="form-grid">
          <RuntimeCountLimit label="Iterations" value={script.config.run_logic?.iterations} onChange={value => updateField('config.run_logic', { ...script.config.run_logic, iterations: value })} help={pacingIsOpenWorkload ? 'Open workloads convert this to an arrival budget multiplied by the assigned worker count.' : 'Per virtual user. Zero means no per-user iteration cap.'} />
          <RuntimeCountLimit label="Global Iterations" value={script.config.run_logic?.global_iterations} onChange={value => updateField('config.run_logic', { ...script.config.run_logic, global_iterations: value })} help="Shared execution budget. Already admitted work can finish when the budget is reached." />
          <RuntimeCountLimit label="Max Transactions" value={script.config.run_logic?.max_transactions} onChange={value => updateField('config.run_logic', { ...script.config.run_logic, max_transactions: value })} help="Counts explicit business transactions and enabled auto-transactions; this is not an HTTP request count." />
          <label>
            <span>Workload Model</span>
            <select value={canonicalWorkloadModel(script.config.run_logic?.workload_model)}
              onChange={e => updateField('config', { ...script.config, run_logic: { ...script.config.run_logic, workload_model: e.target.value }, ...(e.target.value === 'open' ? { pacing: { ...script.config.pacing, type: 'tps', tps: Number.isFinite(script.config.pacing?.tps) && (script.config.pacing?.tps ?? 0) > 0 ? script.config.pacing!.tps : 1 } } : {}) })}>
              <option value="closed">Closed VUser</option>
              <option value="open">Open arrival-rate</option>
            </select>
          </label>
          {pacingIsOpenWorkload && <>
            <label>Arrival target (iterations / second)<input aria-label="Arrival target" type="number" min="0" step="any" value={script.config.pacing?.tps ?? ''} onChange={event => updateField('config.pacing', { ...script.config.pacing, type: 'tps', tps: Number(event.target.value) })} /></label>
            <label>Maximum in-flight iterations<input aria-label="Maximum in-flight iterations" type="number" min="0" value={script.config.run_logic?.max_in_flight ?? 0} onChange={event => updateField('config.run_logic', { ...script.config.run_logic, max_in_flight: Number(event.target.value) })} /><small>0 uses the engine-derived limit. Excess arrivals are shed when the limit is reached.</small></label>
          </>}
          <label>
            <span>Auto-Transactions</span>
            <select value={script.config.run_logic?.auto_transactions || 'none'}
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, auto_transactions: e.target.value })}>
              <option value="none">None</option>
              <option value="per_action">Per action</option>
              <option value="per_iteration">Per iteration</option>
            </select>
          </label>
          <label>
            <span>Termination mode</span>
            <select aria-label="Termination mode" value={script.config.run_logic?.termination_mode || ''}
              onChange={e => updateField('config.run_logic', {
                ...script.config.run_logic,
                termination_mode: (e.target.value || undefined) as NonNullable<ScriptConfig['run_logic']>['termination_mode'],
              })}>
              <option value="">Default</option>
              <option value="duration">Duration</option>
              <option value="iterations">Iterations</option>
            </select>
          </label>
          {script.config.run_logic?.block_scheduling && (
            // HTTP/SOAP execution order comes from run blocks and each block's
            // mode; the imported top-level value is preserved, not executed.
            <p className="full-width" role="note" data-testid="run-logic-block-scheduling-notice">
              Imported block scheduling: {script.config.run_logic.block_scheduling}. This value is preserved from the import and does not change execution order; each run block&apos;s mode below does.
              {/* The engine's pooling gate (java_shared.go) compares the raw value exactly. */}
              {isJavaVuserScript && script.config.run_logic.block_scheduling !== 'sequential' && (
                <>
                  {' On Java VUser scripts, a value other than sequential turns off shared-JVM pooling.'}{' '}
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost"
                    data-testid="run-logic-block-scheduling-reset"
                    onClick={() => updateField('config.run_logic', { ...script.config.run_logic, block_scheduling: 'sequential' })}
                  >
                    Reset to sequential
                  </button>
                </>
              )}
            </p>
          )}
          <div className="settings-sub-group full-width" data-testid="run-logic-blocks-editor">
            <div className="sub-header">
              <h5>Run blocks</h5>
              <button
                type="button"
                className="btn btn-sm btn-secondary"
                onClick={() => {
                  const defaultAction = script.actions?.[0]?.name || 'Action'
                  updateField('config.run_logic', {
                    ...script.config.run_logic,
                    blocks: [
                      ...(script.config.run_logic?.blocks || []),
                      {
                        name: `Block ${(script.config.run_logic?.blocks?.length || 0) + 1}`,
                        mode: 'sequential',
                        iterations: 1,
                        children: [{ action: defaultAction }],
                      },
                    ],
                  })
                }}
              >
                Add run block
              </button>
            </div>
            {(script.config.run_logic?.blocks || []).map((block, blockIdx) => (
              <div key={`run-block-${blockIdx}`} className="pair-row" style={{ marginTop: 8 }}>
                <input
                  aria-label={`Run block ${blockIdx + 1} name`}
                  value={block.name || ''}
                  placeholder="Block name"
                  onChange={e => {
                    const nextBlocks = [...(script.config.run_logic?.blocks || [])]
                    nextBlocks[blockIdx] = { ...block, name: e.target.value }
                    updateField('config.run_logic', { ...script.config.run_logic, blocks: nextBlocks })
                  }}
                />
                <select
                  aria-label={`Run block ${blockIdx + 1} mode`}
                  value={block.mode || 'sequential'}
                  onChange={e => {
                    const nextMode = e.target.value as typeof block.mode
                    const nextBlocks = [...(script.config.run_logic?.blocks || [])]
                    const nextChildren =
                      nextMode === 'percentage'
                        ? (block.children || []).map(child =>
                            child.weight && child.weight > 0 ? child : { ...child, weight: 100 },
                          )
                        : block.children
                    nextBlocks[blockIdx] = { ...block, mode: nextMode, children: nextChildren }
                    updateField('config.run_logic', { ...script.config.run_logic, blocks: nextBlocks })
                  }}
                >
                  <option value="sequential">Sequential</option>
                  <option value="random">Random</option>
                  <option value="percentage">Percentage</option>
                </select>
                <input
                  type="number"
                  min="0"
                  aria-label={`Run block ${blockIdx + 1} iterations`}
                  value={block.iterations ?? ''}
                  placeholder="Iterations"
                  onChange={e => {
                    const nextBlocks = [...(script.config.run_logic?.blocks || [])]
                    nextBlocks[blockIdx] = {
                      ...block,
                      iterations: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                    }
                    updateField('config.run_logic', { ...script.config.run_logic, blocks: nextBlocks })
                  }}
                />
                <DraftTextInput
                  aria-label={`Run block ${blockIdx + 1} actions`}
                  value={formatRunBlockActions(block.children, block.mode)}
                  normalize={raw => runBlockActionsWeightsSurviveParsing(raw, block.mode) ? formatRunBlockActions(rebuildRunBlockChildren(block.children, raw, block.mode), block.mode) : null}
                  placeholder="Action, Checkout"
                  onCommit={raw => {
                    const nextBlocks = [...(script.config.run_logic?.blocks || [])]
                    nextBlocks[blockIdx] = {
                      ...block,
                      children: rebuildRunBlockChildren(block.children, raw, block.mode),
                    }
                    updateField('config.run_logic', { ...script.config.run_logic, blocks: nextBlocks })
                  }}
                />
                {(block.children || []).some(child => child.block) && (
                  <small>Nested blocks keep their place in this block&apos;s order.</small>
                )}
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  aria-label={`Remove run block ${blockIdx + 1}`}
                  onClick={() => {
                    const nextBlocks = (script.config.run_logic?.blocks || []).filter((_, idx) => idx !== blockIdx)
                    updateField('config.run_logic', {
                      ...script.config.run_logic,
                      blocks: nextBlocks.length > 0 ? nextBlocks : undefined,
                    })
                  }}
                >
                  Remove
                </button>
              </div>
            ))}
          </div>
          <p className="full-width">Lifecycle: init → authored actions → teardown. When run blocks are set, they decide each iteration&apos;s action order. Imported block scheduling and action sequence values are preserved under Imported Source Runtime Settings and do not change execution order.</p>

        </div>
      </div>
    )}
  </div>

  {/* Error Handling */}
  <div className={`settings-section ${runtimeSectionIsActive('errors') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('errors')}>
      <RuntimeSectionIcon section="errors" />
      <span className="section-label">Error Handling</span>
      <span className="section-summary">
        {script.config.error_handling?.continue_on_error === undefined
          ? 'Defer to scenario'
          : script.config.error_handling?.continue_on_error
            ? 'Continue on error'
            : 'Stop on error'}
      </span>
      <span className="chevron">{runtimeSectionIsActive('errors') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('errors') && (
      <div className="section-body">
        {['http', 'http/1.1', 'http/2', 'http/3', 'soap', 'java_http'].includes(script.protocol) && <MessageDetailFields value={script.config.results?.message_detail} onChange={value => updateField('config.results', { ...script.config.results, message_detail: value })} />}
        <PropertyAnalyticsFields value={script.config.results?.property_analytics} onChange={value => updateField('config.results', { ...script.config.results, property_analytics: value })} />
        <div className="form-grid">
          <div className="runtime-checkbox-list">
            <label className="form-field">
              <span>Continue on error</span>
              <select
                aria-label="Continue on error"
                value={
                  script.config.error_handling?.continue_on_error === undefined
                    ? ''
                    : script.config.error_handling.continue_on_error
                      ? 'true'
                      : 'false'
                }
                onChange={e => {
                  const raw = e.target.value
                  const nextVal = raw === '' ? undefined : raw === 'true'
                  updateField('config.error_handling', {
                    ...script.config.error_handling,
                    continue_on_error: nextVal,
                  })
                }}
              >
                <option value="">Default (defer to scenario)</option>
                <option value="true">Continue</option>
                <option value="false">Stop</option>
              </select>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.error_handling?.snapshot_on_error || false}
                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, snapshot_on_error: e.target.checked })} />
              <span>Capture snapshot on error</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.error_handling?.fail_transaction_on_error_message || false}
                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, fail_transaction_on_error_message: e.target.checked })} />
              <span>Fail transaction on error message</span>
            </label>
          </div>
          <label>
            <span>Max retries</span>
            <input type="number" value={script.config.error_handling?.max_retries || 0}
              onChange={e => updateField('config.error_handling', { ...script.config.error_handling, max_retries: Number(e.target.value) })} />
          </label>
          <label htmlFor="runtime-retry-on-conditions">
            <span>Retry on conditions</span>
            <DraftTextInput
              id="runtime-retry-on-conditions"
              type="text"
              aria-label="Retry on conditions"
              placeholder="429, 5xx, timeout, network"
              value={(script.config.error_handling?.retry_on || []).join(', ')}
              normalize={raw => parseCommaList(raw).join(', ')}
              onCommit={raw => {
                const tokens = parseCommaList(raw)
                updateField('config.error_handling', {
                  ...script.config.error_handling,
                  retry_on: tokens.length > 0 ? tokens : undefined,
                })
              }}
            />
          </label>
          <label>
            <span>Honor Retry-After header</span>
            <input
              type="checkbox"
              aria-label="Honor Retry-After header"
              checked={script.config.error_handling?.honor_retry_after || false}
              onChange={e => updateField('config.error_handling', { ...script.config.error_handling, honor_retry_after: e.target.checked })}
            />
          </label>
          <label>
            <span>Max Retry-After delay (ms)</span>
            <input
              type="number"
              min={0}
              aria-label="Max Retry-After delay (ms)"
              placeholder="Default"
              value={script.config.error_handling?.retry_after_max_ms ?? ''}
              onChange={e => updateField('config.error_handling', {
                ...script.config.error_handling,
                retry_after_max_ms: e.target.value.trim() === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
              })}
            />
          </label>
          {(script.config.error_handling?.max_retries || 0) > 0 && (
            <>
              <label>
                <span>Retry delay (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.error_handling?.retry_delay_ms} fallbackMs={1000}
                  onChangeMs={value => updateField('config.error_handling', { ...script.config.error_handling, retry_delay_ms: value ?? 0 })} />
              </label>
              <label>
                <span>Retry backoff</span>
                <select value={script.config.error_handling?.retry_backoff || 'fixed'}
                  onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_backoff: e.target.value })}>
                  <option value="fixed">Fixed</option>
                  <option value="exponential">Exponential</option>
                  <option value="retry_after">Retry-After</option>
                </select>
              </label>
            </>
          )}
        </div>
        <fieldset data-testid="script-transaction-sla-rules">
          <legend>Transaction &amp; phase SLA rules</legend>
          <p>Attach pass/fail or warning thresholds to named business transactions across all phases or an individual network phase (DNS, Connect, TLS, Server, or Download).</p>
          <datalist id={transactionDatalistId}>
            {knownTransactionNames.map(name => (
              <option key={name} value={name}>{name}</option>
            ))}
          </datalist>
          {transactionSLAValidationError && (
            <p className="form-error" role="alert">{transactionSLAValidationError}</p>
          )}
          {transactionRules.map((rule, index) => {
            const standardMetricOptions = rule.phase
              ? SCRIPT_TRANSACTION_SLA_DURATION_METRICS
              : [...SCRIPT_TRANSACTION_SLA_DURATION_METRICS, ...SCRIPT_TRANSACTION_SLA_NON_PHASE_METRICS]
            const hasCustomMetric = Boolean(rule.metric) && !standardMetricOptions.some(option => option.value === rule.metric)
            return (
              <div key={index} className="form-grid">
                <label>
                  <span>Transaction</span>
                  <input
                    aria-label={`Transaction SLA rule ${index + 1} transaction`}
                    list={transactionDatalistId}
                    value={rule.transaction}
                    placeholder="Checkout"
                    onChange={e => updateTransactionRule(index, { transaction: e.target.value })}
                  />
                </label>
                <label>
                  <span>Phase</span>
                  <select
                    aria-label={`Transaction SLA rule ${index + 1} phase`}
                    value={rule.phase || ''}
                    onChange={e => {
                      const nextPhase = e.target.value as '' | NonNullable<TransactionSLARule['phase']>
                      const nextMetric = nextPhase && !isTransactionSLADurationMetric(rule.metric)
                        ? 'p95_duration_ms'
                        : rule.metric
                      updateTransactionRule(index, {
                        phase: nextPhase || undefined,
                        metric: nextMetric,
                      })
                    }}
                  >
                    {SCRIPT_TRANSACTION_SLA_PHASE_OPTIONS.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <label>
                  <span>Metric</span>
                  <select
                    aria-label={`Transaction SLA rule ${index + 1} metric`}
                    value={rule.metric}
                    onChange={e => updateTransactionRule(index, { metric: e.target.value as TransactionSLARule['metric'] })}
                  >
                    {standardMetricOptions.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                    {hasCustomMetric && <option value={rule.metric}>{rule.metric}</option>}
                  </select>
                </label>
                <label>
                  <span>Comparator</span>
                  <select
                    aria-label={`Transaction SLA rule ${index + 1} comparator`}
                    value={rule.comparator}
                    onChange={e => updateTransactionRule(index, { comparator: e.target.value as TransactionSLARule['comparator'] })}
                  >
                    {SCRIPT_TRANSACTION_SLA_COMPARATOR_OPTIONS.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <label>
                  <span>Threshold</span>
                  <input
                    aria-label={`Transaction SLA rule ${index + 1} threshold`}
                    type="number"
                    min={0}
                    step="any"
                    value={Number.isFinite(rule.threshold) ? rule.threshold : ''}
                    onChange={e => updateTransactionRule(index, {
                      threshold: e.target.value.trim() === '' ? Number.NaN : Number(e.target.value),
                    })}
                  />
                </label>
                <label>
                  <span>Severity</span>
                  <select
                    aria-label={`Transaction SLA rule ${index + 1} severity`}
                    value={rule.severity || 'fail'}
                    onChange={e => updateTransactionRule(index, { severity: e.target.value as NonNullable<TransactionSLARule['severity']> })}
                  >
                    {SCRIPT_TRANSACTION_SLA_SEVERITY_OPTIONS.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <div>
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm"
                    aria-label={`Remove transaction SLA rule ${index + 1}`}
                    onClick={() => removeTransactionRule(index)}
                  >
                    Remove
                  </button>
                </div>
              </div>
            )
          })}
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            disabled={transactionRules.length >= MAX_SCRIPT_TRANSACTION_SLA_RULES}
            onClick={addTransactionRule}
          >
            Add transaction SLA rule
          </button>
        </fieldset>
      </div>
    )}
  </div>

  {isJmsScript && (
    <div className={`settings-section ${runtimeSectionIsActive('jms') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('jms')}>
        <RuntimeSectionIcon section="jms" />
        <span className="section-label">{formatScriptProtocolLabel(script.protocol)}</span>
        <span className="section-summary">
          {script.config.jms?.mock ? 'Mock broker' : script.config.jms?.broker_url ? 'Broker configured' : 'Not configured'}
        </span>
        <span className="chevron">{runtimeSectionIsActive('jms') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('jms') && (
        <div className="section-body">
          <div className="settings-sub-group">
            <h5>Connection</h5>
            <div className="form-grid">
              <label className="full-width"><span>Broker URL</span>
                <input value={script.config.jms?.broker_url || ''} placeholder="tcps://broker.example.com:55443"
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, broker_url: e.target.value })} /></label>
              <label><span>Client ID</span>
                <input value={script.config.jms?.client_id || ''} placeholder="apexload-{{fn.uuid()}}"
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, client_id: e.target.value })} /></label>
              {jmsProvider === 'solace' && (
                <label><span>Message VPN</span>
                  <input value={script.config.jms?.message_vpn || ''} placeholder="default"
                    onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider, message_vpn: e.target.value })} /></label>
              )}
              <label><span>Username</span>
                <input value={script.config.jms?.username || ''}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, username: e.target.value })} /></label>
              <CredentialField label="Password" path="config.jms.password" value={script.config.jms?.password}
                onChange={value => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, password: value })} />
              <label className="checkbox-label">
                {/* The JMS adapter treats an unset mock as off (real broker). */}
                <input type="checkbox" checked={script.config.jms?.mock ?? false}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, mock: e.target.checked })} />
                <span>Use local mock broker for replay</span>
              </label>
              <label><span>TLS CA Certificate Secret</span>
                <input autoComplete="off" spellCheck={false} value={script.config.jms?.tls?.ca_cert_secret || ''} placeholder="jms-broker-ca"
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, tls: { ...script.config.jms?.tls, ca_cert_secret: e.target.value } })} /></label>
              <label><span>TLS Trust Store Reference</span>
                <input value={script.config.jms?.tls?.trust_store_ref || ''} placeholder="truststore.jks"
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, tls: { ...script.config.jms?.tls, trust_store_ref: e.target.value } })} /></label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.jms?.tls?.insecure_skip_verify ?? false}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, tls: { ...script.config.jms?.tls, insecure_skip_verify: e.target.checked } })} />
                <span>Skip broker TLS verification (test brokers only)</span>
              </label>
            </div>
          </div>
          <div className="settings-sub-group">
            <h5>Defaults</h5>
            <div className="form-grid">
              <label><span>Destination</span>
                <input value={script.config.jms?.destination || ''} placeholder="orders.in"
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, destination: e.target.value })} /></label>
              <SelectField label="Destination Type" path="config.jms.destination_type" value={script.config.jms?.destination_type} defaultValue="queue"
                onChange={value => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, destination_type: value })} />
              <label><span>Reply To</span>
                <input value={script.config.jms?.reply_to || ''} placeholder="orders.reply"
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, reply_to: e.target.value })} /></label>
              {jmsProvider === 'servicebus' && (
                <label><span>Topic subscription</span>
                  <input value={script.config.jms?.subscription || ''} placeholder="orders-workers"
                    onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider, subscription: e.target.value })} /></label>
              )}
              <label><span>Timeout (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.jms?.timeout_ms} fallbackMs={5000}
                  onChangeMs={value => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, timeout_ms: value ?? 0 })} /></label>
              <SelectField label="Acknowledgement" path="config.jms.acknowledgement" value={script.config.jms?.acknowledgement} defaultValue="auto"
                onChange={value => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, acknowledgement: value })} />
              <SelectField label="Delivery Mode" path="config.jms.delivery_mode" value={script.config.jms?.delivery_mode} defaultValue="persistent"
                onChange={value => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, delivery_mode: value })} />
            </div>
          </div>
          <KeyValueMapField
            label="JMS connection properties"
            path="config.jms.properties"
            value={script.config.jms?.properties}
            keyPlaceholder="Property name"
            valuePlaceholder="Value"
            addLabel="Add property"
            onChange={properties => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, properties })}
          />
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            {jmsProvider === 'ibmmq'
              ? 'IBM MQ supports mock replay only; real-mode validation fails closed until a governed MQI adapter is present.'
              : `${formatScriptProtocolLabel(script.protocol)} real execution requires a build that includes its provider adapter; use mock replay for local shakeout.`}
          </div>
        </div>
      )}
    </div>
  )}

  {isKafkaScript && (
    <KafkaRuntimeSection
      script={script}
      sectionIsActive={runtimeSectionIsActive}
      onSelectSection={selectRuntimeSettingsSection}
      onUpdateField={updateField}
    />
  )}

  {isEventHubsScript && (
    <div className={`settings-section ${runtimeSectionIsActive('eventhubs') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('eventhubs')}>
        <RuntimeSectionIcon section="eventhubs" />
        <span className="section-label">Event Hubs Runtime</span>
        <span className="section-summary">{eventHubsSummary}</span>
        <span className="chevron">{runtimeSectionIsActive('eventhubs') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('eventhubs') && (
        <div className="section-body">
          <div className="settings-sub-group">
            <h5>Namespace</h5>
            <div className="form-grid">
              <label className="full-width"><span>Namespace</span>
                <input value={script.config.eventhubs?.namespace || ''} placeholder="contoso.servicebus.windows.net"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, namespace: e.target.value })} /></label>
              <label><span>Event Hub</span>
                <input value={script.config.eventhubs?.event_hub || ''} placeholder="orders"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, event_hub: e.target.value })} /></label>
              <SelectField label="Authentication" path="config.eventhubs.auth_type" value={script.config.eventhubs?.auth_type} defaultValue="connection_string"
                onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, auth_type: value })} />
              <label><span>Connection String Secret</span>
                <input value={script.config.eventhubs?.connection_string_secret || ''} placeholder="eventhubs-primary"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, connection_string_secret: e.target.value })} /></label>
              <CredentialField label="Connection String" path="config.eventhubs.connection_string" value={script.config.eventhubs?.connection_string}
                onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, connection_string: value })} />
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.eventhubs?.mock ?? false}
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, mock: e.target.checked })} />
                <span>Use in-memory mock broker for replay</span>
              </label>
            </div>
          </div>
          <div className="settings-sub-group">
            <h5>Defaults</h5>
            <div className="form-grid">
              <label><span>Partition Key</span>
                <input value={script.config.eventhubs?.partition_key || ''} placeholder="{{customer_id}}"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, partition_key: e.target.value })} /></label>
              <label><span>Partition ID</span>
                <input value={script.config.eventhubs?.partition_id || ''} placeholder="0"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, partition_id: e.target.value })} /></label>
              <label><span>Consumer group</span>
                <input value={script.config.eventhubs?.consumer_group || ''} placeholder="$Default"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, consumer_group: e.target.value })} /></label>
              <SelectField label="Observer Start Position" path="config.eventhubs.start_position" value={script.config.eventhubs?.start_position} defaultValue="latest"
                onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, start_position: value })} />
              <label><span>Timeout (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.eventhubs?.timeout_ms} fallbackMs={5000}
                  onChangeMs={value => updateField('config.eventhubs', { ...script.config.eventhubs, timeout_ms: value ?? 0 })} /></label>
            </div>
          </div>
          <div className="settings-sub-group" data-testid="eventhubs-consumer-fields">
            <h5>Consumer processing</h5>
            <div className="form-grid">
              <SelectField label="Consumer mode" path="config.eventhubs.consumer_mode" value={script.config.eventhubs?.consumer_mode} defaultValue="observer"
                onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, consumer_mode: value })} />
              {(script.config.eventhubs?.consumer_mode === 'processor' || script.config.eventhubs?.checkpoint_container_url) && (
                <TextField fullWidth label="Checkpoint Blob container URL" path="config.eventhubs.checkpoint_container_url" value={script.config.eventhubs?.checkpoint_container_url}
                  placeholder="https://account.blob.core.windows.net/checkpoints"
                  onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, checkpoint_container_url: value || undefined })} />
              )}
              {(script.config.eventhubs?.consumer_mode === 'processor' || script.config.eventhubs?.processor_max_partitions !== undefined) && (
                <IntegerField label="Max processor partitions" path="config.eventhubs.processor_max_partitions" value={script.config.eventhubs?.processor_max_partitions} min={0} max={32} placeholder="32"
                  onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, processor_max_partitions: value })} />
              )}
              {(script.config.eventhubs?.consumer_mode === 'exclusive' || (script.config.eventhubs?.owner_level !== undefined && script.config.eventhubs?.owner_level !== null)) && (
                <IntegerField label="Exclusive owner level" path="config.eventhubs.owner_level" value={script.config.eventhubs?.owner_level} min={0}
                  onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, owner_level: value })} />
              )}
              {/* Partition-tail settings are processor-only (catalog_eventhubs_validation.go); a stored value stays visible so it can be cleared. */}
              {(script.config.eventhubs?.consumer_mode === 'processor' || Boolean(script.config.eventhubs?.tail_poll_interval_ms)) && (
                <IntegerField label="Tail poll interval (ms)" path="config.eventhubs.tail_poll_interval_ms" value={script.config.eventhubs?.tail_poll_interval_ms} min={10000} max={300000} placeholder="10000"
                  onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, tail_poll_interval_ms: value })} />
              )}
            </div>
            {(script.config.eventhubs?.consumer_mode === 'processor' || Boolean(script.config.eventhubs?.observe_partition_tail)) && (
              <div className="runtime-checkbox-list">
                <CheckboxField label="Observe partition tail lag" path="config.eventhubs.observe_partition_tail" checked={Boolean(script.config.eventhubs?.observe_partition_tail)}
                  onChange={value => updateField('config.eventhubs', { ...script.config.eventhubs, observe_partition_tail: value || undefined })} />
              </div>
            )}
          </div>
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            Event Hubs real execution requires a build that includes the Azure adapter (-tags eventhubs_real); use mock replay for local shakeout.
          </div>
        </div>
      )}
    </div>
  )}

  {isWebSocketScript && (
    <div className={`settings-section ${runtimeSectionIsActive('websocket') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('websocket')}>
        <RuntimeSectionIcon section="websocket" />
        <span className="section-label">WebSocket Runtime</span>
        <span className="section-summary">{script.config.websocket?.reconnect ? 'Reconnect enabled' : 'Single connection'}</span>
        <span className="chevron">{runtimeSectionIsActive('websocket') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('websocket') && (
        <div className="section-body">
          <div className="form-grid">
            <label><span>Callback interval (ms)</span><input type="number" min={1} max={60000} value={script.config.websocket?.callback_interval_ms || 500} onChange={e => updateField('config.websocket', {...script.config.websocket,callback_interval_ms:Number(e.target.value)})} /></label>
            <label><span>Read Timeout (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.websocket?.read_timeout_ms} fallbackMs={30000}
                onChangeMs={value => updateField('config.websocket', { ...script.config.websocket, read_timeout_ms: value ?? 0 })} /></label>
            <label><span>Write Timeout (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.websocket?.write_timeout_ms} fallbackMs={10000}
                onChangeMs={value => updateField('config.websocket', { ...script.config.websocket, write_timeout_ms: value ?? 0 })} /></label>
            <label><span>Max Message Size</span>
              <input type="number" value={script.config.websocket?.max_message_size || 1048576}
                onChange={e => updateField('config.websocket', { ...script.config.websocket, max_message_size: Number(e.target.value) })} /></label>
            <label><span>Subprotocol</span>
              <input value={script.config.websocket?.subprotocol || ''}
                onChange={e => updateField('config.websocket', { ...script.config.websocket, subprotocol: e.target.value })} placeholder="graphql-transport-ws" /></label>
            <label><span>Origin</span>
              <input value={script.config.websocket?.origin || ''}
                onChange={e => updateField('config.websocket', { ...script.config.websocket, origin: e.target.value })} placeholder="https://app.example.com" /></label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.websocket?.compression ?? false}
                onChange={e => updateField('config.websocket', { ...script.config.websocket, compression: e.target.checked })} />
              <span>Compression</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.websocket?.reconnect ?? true}
                onChange={e => updateField('config.websocket', { ...script.config.websocket, reconnect: e.target.checked })} />
              <span>Reconnect on failure</span>
            </label>
          </div>
        </div>
      )}
    </div>
  )}

  {isGrpcScript && (
    <div className={`settings-section ${runtimeSectionIsActive('grpc') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('grpc')}>
        <RuntimeSectionIcon section="grpc" />
        <span className="section-label">gRPC Runtime</span>
        <span className="section-summary">{script.config.grpc?.use_reflection ? 'Reflection' : script.config.grpc?.proto_files?.length ? 'Proto files' : 'Contract pending'}</span>
        <span className="chevron">{runtimeSectionIsActive('grpc') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('grpc') && (
        <div className="section-body">
          <div className="form-grid">
            <label className="checkbox-label">
              {/* The gRPC adapter enables reflection only when use_reflection is true. */}
              <input type="checkbox" checked={script.config.grpc?.use_reflection ?? false}
                onChange={e => updateField('config.grpc', { ...script.config.grpc, use_reflection: e.target.checked })} />
              <span>Use reflection</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.grpc?.plain_text ?? false}
                onChange={e => updateField('config.grpc', { ...script.config.grpc, plain_text: e.target.checked })} />
              <span>Plaintext channel</span>
            </label>
            <label><span>Timeout (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.grpc?.timeout_ms} fallbackMs={30000}
                onChangeMs={value => updateField('config.grpc', { ...script.config.grpc, timeout_ms: value ?? 0 })} /></label>
            <label><span>Max Receive Size</span>
              <input type="number" value={script.config.grpc?.max_recv_msg_size || 4194304}
                onChange={e => updateField('config.grpc', { ...script.config.grpc, max_recv_msg_size: Number(e.target.value) })} /></label>
            <label><span>Authority</span>
              <input value={script.config.grpc?.authority || ''}
                onChange={e => updateField('config.grpc', { ...script.config.grpc, authority: e.target.value })} placeholder="api.example.com" /></label>
            <label className="full-width"><span>Proto Files</span>
              <input value={(script.config.grpc?.proto_files || []).join(', ')}
                onChange={e => updateField('config.grpc', { ...script.config.grpc, proto_files: e.target.value.split(',').map(value => value.trim()).filter(Boolean) })}
                placeholder="contracts/orders.proto, contracts/common.proto" /></label>
          </div>
          <KeyValueMapField
            label="Default call metadata"
            path="config.grpc.metadata"
            value={script.config.grpc?.metadata}
            keyPlaceholder="x-tenant-id"
            valuePlaceholder="{{tenant_id}}"
            addLabel="Add metadata"
            onChange={metadata => updateField('config.grpc', { ...script.config.grpc, metadata })}
          />
        </div>
      )}
    </div>
  )}

  {isDatabaseScript && (
    <div className={`settings-section ${runtimeSectionIsActive('database') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('database')}>
        <RuntimeSectionIcon section="database" />
        <span className="section-label">Database Runtime</span>
        <span className="section-summary">{script.config.database?.dsn_secret ? 'Secret-backed DSN' : script.config.database?.dsn ? 'DSN configured' : 'Not configured'}</span>
        <span className="chevron">{runtimeSectionIsActive('database') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('database') && (
        <div className="section-body">
          <div className="form-grid">
            <label><span>Query Timeout (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.database?.query_timeout_ms} fallbackMs={30000}
                onChangeMs={value => updateField('config.database', { ...script.config.database, query_timeout_ms: value ?? 0 })} /></label>
            <label><span>Max Rows</span>
              <input type="number" value={script.config.database?.max_rows || 100}
                onChange={e => updateField('config.database', { ...script.config.database, max_rows: Number(e.target.value) })} /></label>
            <IntegerField label="Max Open Connections" path="config.database.max_open_conns" value={script.config.database?.max_open_conns} min={0} placeholder="Unlimited"
              onChange={value => updateField('config.database', { ...script.config.database, max_open_conns: value })} />
            <label data-catalog-path="config.database.conn_max_lifetime_ms"><span>Connection max lifetime (s)</span>
              <SecondsFromMillisecondsInput aria-label="Connection max lifetime" valueMs={script.config.database?.conn_max_lifetime_ms} emptyWhenUndefined placeholder="No limit"
                onChangeMs={value => updateField('config.database', { ...script.config.database, conn_max_lifetime_ms: value || undefined })} /></label>
            <label><span>Max idle connections</span>
              <input type="number" value={script.config.database?.max_idle_conns || 0}
                onChange={e => updateField('config.database', { ...script.config.database, max_idle_conns: Number(e.target.value) || undefined })} /></label>
            <label className="checkbox-label">
              {/* Unset is off at run time (database_read_only is sent only when true). */}
              <input type="checkbox" checked={script.config.database?.read_only ?? false}
                onChange={e => updateField('config.database', { ...script.config.database, read_only: e.target.checked })} />
              <span>Read-only guard</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.database?.rollback_on_teardown ?? false}
                onChange={e => updateField('config.database', { ...script.config.database, rollback_on_teardown: e.target.checked })} />
              <span>Rollback open transaction on teardown</span>
            </label>
          </div>
        </div>
      )}
    </div>
  )}

  {isNetworkScript && (
    <div className={`settings-section ${runtimeSectionIsActive('network') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('network')}>
        <RuntimeSectionIcon section="network" />
        <span className="section-label">Network Runtime</span>
        <span className="section-summary">{script.config.network?.subprotocol || 'tcp'} · {script.config.network?.host || script.config.network?.address || 'host pending'}</span>
        <span className="chevron">{runtimeSectionIsActive('network') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('network') && (
        <div className="section-body">
          <div className="form-grid">
            <label><span>Timeout (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.network?.timeout_ms} fallbackMs={30000}
                onChangeMs={value => updateField('config.network', { ...script.config.network, timeout_ms: value ?? 0 })} /></label>
            <label><span>Address Override</span>
              <input value={script.config.network?.address || ''}
                onChange={e => updateField('config.network', { ...script.config.network, address: e.target.value })} placeholder="127.0.0.1:2525" /></label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.network?.tls ?? false}
                onChange={e => updateField('config.network', { ...script.config.network, tls: e.target.checked })} />
              <span>TLS</span>
            </label>
            <label><span>Username</span>
              <input value={script.config.network?.username || ''}
                onChange={e => updateField('config.network', { ...script.config.network, username: e.target.value })} /></label>
            <CredentialField label="Password" path="config.network.password" value={script.config.network?.password}
              onChange={value => updateField('config.network', { ...script.config.network, password: value })} />
            <label><span>LDAP Base DN</span>
              <input value={script.config.network?.base_dn || ''}
                onChange={e => updateField('config.network', { ...script.config.network, base_dn: e.target.value })} placeholder="dc=example,dc=com" /></label>
          </div>
          {((script.config.network?.subprotocol || '').trim().toLowerCase() === 'smtp' || Boolean(script.config.network?.from) || Boolean(script.config.network?.to?.length)) && <div className="settings-sub-group" data-testid="network-smtp-defaults">
            <h5>SMTP defaults</h5>
            <div className="form-grid">
              <TextField label="Sender address (MAIL FROM)" path="config.network.from" value={script.config.network?.from} placeholder="loadtest@example.test"
                onChange={value => updateField('config.network', { ...script.config.network, from: value || undefined })} />
              <LineListField label="Recipient addresses (RCPT TO)" path="config.network.to" value={script.config.network?.to} placeholder={'ops@example.test\nqa@example.test'}
                onChange={value => updateField('config.network', { ...script.config.network, to: value.length > 0 ? value : undefined })} />
            </div>
          </div>}
        </div>
      )}
    </div>
  )}

  {isAppiumScript && (
    <div className={`settings-section ${runtimeSectionIsActive('appium') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('appium')}>
        <RuntimeSectionIcon section="appium" />
        <span className="section-label">Appium Device</span>
        <span className="section-summary">{appiumDeviceSummary(script.config.appium)}</span>
        <span className="chevron">{runtimeSectionIsActive('appium') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('appium') && (
        <div className="section-body">
          <AppiumRuntimeFields config={script.config.appium} onChange={appium => updateField('config.appium', appium)} />
        </div>
      )}
    </div>
  )}

  {/* HTTP transport */}
  {isHttpTransportScript && <div className={`settings-section ${runtimeSectionIsActive('http') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('http')}>
      <RuntimeSectionIcon section="http" />
      <span className="section-label">HTTP Transport</span>
      <span className="section-summary">
        {formatHTTPSettingsSummary(
          httpSettingsValue,
          Boolean(script.config.browser_emulation?.cache?.new_user_each_iteration),
        )}
      </span>
      <span className="chevron">{runtimeSectionIsActive('http') ? '▾' : '▸'}</span>
    </button>
    {(runtimeSectionIsActive('http') || runtimeSectionIsActive('web_runtime')) && (
      <div className="section-body">
        {runtimeSectionIsActive('http') && (
          <>
        <div className="http-settings-persistent-summary" role="status">
          <strong>Effective HTTP settings</strong>
          <span>{formatHTTPSettingsSummary(
            httpSettingsValue,
            Boolean(script.config.browser_emulation?.cache?.new_user_each_iteration),
          )}</span>
        </div>
        <label className="http-transport-mobile-select">
          <span>HTTP settings group</span>
          <select value={effectiveHTTPTransportTab} onChange={event => setActiveHTTPTransportTab(event.target.value as HTTPTransportTabId)}>
            {visibleHTTPTransportTabs.map(tab => <option value={tab.id} key={tab.id}>{tab.label}</option>)}
          </select>
        </label>
        <TabList className="http-transport-tabs" role="tablist" aria-label="HTTP Transport option groups" onKeyDown={handleTabKey}>
          {visibleHTTPTransportTabs.map(tab => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              id={`http-transport-tab-${tab.id}`}
              aria-controls={`http-transport-panel-${tab.id}`}
              aria-selected={effectiveHTTPTransportTab === tab.id}
              className={`http-transport-tab ${effectiveHTTPTransportTab === tab.id ? 'active' : ''}`}
              onClick={() => setActiveHTTPTransportTab(tab.id)} tabIndex={(effectiveHTTPTransportTab === tab.id) ? 0 : -1}
            >
              <span>{tab.label}</span>
              {tab.id === 'request_defaults' && (
                <>
                  <span className="http-tab-count">{countHTTPDefaultHeaders(httpSettingsValue)}</span>
                  {requestDefaultErrorCount > 0 && <span className="http-tab-error" aria-label={`${requestDefaultErrorCount} request default errors`}>{requestDefaultErrorCount} errors</span>}
                </>
              )}
              {tab.id === 'session_cache' && (
                <>
                  <span className="http-tab-count">{countPinnedCookies(httpSettingsValue)}</span>
                  {pinnedCookieErrorCount > 0 && <span className="http-tab-error" aria-label={`${pinnedCookieErrorCount} pinned cookie errors`}>{pinnedCookieErrorCount} errors</span>}
                </>
              )}
            </button>
          ))}
        </TabList>
        {effectiveHTTPTransportTab === 'request_defaults' && <div
          id="http-transport-panel-request-defaults"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-request_defaults"
        >
          <HttpRequestDefaultsEditor
            actions={[...(script.init || []), ...(script.actions || []), ...(script.teardown || [])]}
            newUserEachIteration={Boolean(script.config.browser_emulation?.cache?.new_user_each_iteration)}
            onAddRuntimeAction={onAddHTTPRuntimeAction}
            onChange={updateHTTPSettings}
            onNewUserEachIterationChange={enabled => updateBrowserEmulationCache({ new_user_each_iteration: enabled })}
            originLabel="Script default"
            section="request"
            value={httpSettingsValue}
          />
        </div>}
        {effectiveHTTPTransportTab === 'session_cache' && <div
          id="http-transport-panel-session-cache"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-session_cache"
        >
          {isSoapScript && (
            <div className="sub-header" style={{ marginBottom: 8 }}>
              <span className="repository-status-pill" data-testid="soap-inactive-cache-resource-badge">
                Inactive for POST XML SOAP requests (GET/HTML cache &amp; resource policies do not apply)
              </span>
            </div>
          )}
          <HttpRequestDefaultsEditor
            actions={[...(script.init || []), ...(script.actions || []), ...(script.teardown || [])]}
            newUserEachIteration={Boolean(script.config.browser_emulation?.cache?.new_user_each_iteration)}
            onAddRuntimeAction={onAddHTTPRuntimeAction}
            onChange={updateHTTPSettings}
            onNewUserEachIterationChange={enabled => updateBrowserEmulationCache({ new_user_each_iteration: enabled })}
            section="session"
            value={httpSettingsValue}
          />
          <div className="form-grid"><HTTPReplayPolicyFields section="session" value={script.config.http} onChange={patch => updateField('config.http', { ...script.config.http, ...patch })} /></div>
        </div>}
        {effectiveHTTPTransportTab === 'timeouts' && <div
          id="http-transport-panel-timeouts"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-timeouts"
        >
          <h5>Timeouts</h5>
          <div className="form-grid">
            <HTTPReplayPolicyFields section="timeouts" value={script.config.http} onChange={patch => updateField('config.http', { ...script.config.http, ...patch })} />
            <label><span>Connect (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.http?.connect_timeout_ms} fallbackMs={30000}
                onChangeMs={value => updateField('config.http', { ...script.config.http, connect_timeout_ms: value ?? 0 })} /></label>
            <label><span>Request (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.http?.request_timeout_ms} fallbackMs={60000}
                onChangeMs={value => updateField('config.http', { ...script.config.http, request_timeout_ms: value ?? 0 })} /></label>
            <label><span>TLS Handshake (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.http?.tls_timeout_ms} fallbackMs={10000}
                onChangeMs={value => updateField('config.http', { ...script.config.http, tls_timeout_ms: value ?? 0 })} /></label>
            <label><span>Idle Connection (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.http?.idle_timeout_ms} fallbackMs={90000}
                onChangeMs={value => updateField('config.http', { ...script.config.http, idle_timeout_ms: value ?? 0 })} /></label>
          </div>
        </div>}
        {effectiveHTTPTransportTab === 'connection' && <div
          id="http-transport-panel-connection"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-connection"
        >
          <h5>Connection</h5>
          <div className="form-grid">
            <HTTPReplayPolicyFields section="connection" value={script.config.http} onChange={patch => updateField('config.http', { ...script.config.http, ...patch })} />
            <label><span>HTTP version</span>
              <select aria-label="HTTP version" value={httpVersionSettings.selection} aria-invalid={!httpVersionSettings.valid}
                onChange={e => {
                  const value = e.target.value
                  if (HTTP_VERSION_OPTIONS.some(option => option.value === value)) updateField('config.http', { ...script.config.http, version: value })
                }}>
                {HTTP_VERSION_OPTIONS.map(option => (
                  <option key={option.value || 'default'} value={option.value}>{option.value === '' ? httpVersionSettings.defaultLabel : option.label}</option>
                ))}
                {!httpVersionSettings.valid && <option value="invalid" disabled>Unsupported script HTTP version</option>}
                {httpVersionSettings.selection === 'existing-auto' && <option value="existing-auto" disabled>Existing automatic setting (preserved)</option>}
              </select></label>
            {!httpVersionSettings.valid && <p role="alert">This script HTTP version is unsupported. Its original value is preserved until you choose a supported setting or edit the source JSON.</p>}
            {http3PolicyIssues(effectiveHTTPPolicy, { http: script.config.http, web: script.config.web_runtime, target: script.config.base_url }).map(issue => <p key={issue} className="full-width http3-policy-issue" role="alert">{issue}</p>)}
            <div className="form-hint full-width">
              Effective script policy: {httpVersionSettings.effectiveLabel}. Explicit versions are exact; required HTTP/2 never falls back to HTTP/1.1. Each request can inherit this policy or select its own policy, including Auto.
              <HTTP3ExecutionNote policy={effectiveHTTPPolicy} />
            </div>
            <HTTPQUICSettingsFields value={script.config.http?.quic}
              disabledReason={quicPanelDisabledReason(script.config.http, script.config.web_runtime)}
              onChange={patch => updateField('config.http', { ...script.config.http, quic: patchQUICConfig(script.config.http?.quic, patch) })} />
            <label><span>Max Conns/Host</span>
              <input type="number" value={script.config.http?.max_conns_per_host || 100}
                onChange={e => updateField('config.http', { ...script.config.http, max_conns_per_host: Number(e.target.value) })} /></label>
            <label><span>Max idle connections</span>
              <input type="number" min={0} value={script.config.http?.max_idle_conns ?? 4}
                onChange={e => updateField('config.http', { ...script.config.http, max_idle_conns: Number(e.target.value) || undefined })} /></label>
            <label><span>Max redirects</span>
              <input type="number" value={script.config.http?.max_redirects ?? 10}
                onChange={e => updateField('config.http', { ...script.config.http, max_redirects: Number(e.target.value) })} /></label>
            <label><span>Keep-alive</span>
              <select value={script.config.http?.keep_alive === false ? 'false' : 'true'}
                onChange={e => updateField('config.http', { ...script.config.http, keep_alive: e.target.value === 'true' })}>
                <option value="true">Enabled</option>
                <option value="false">Disabled</option>
              </select></label>
            <details className="http-advanced-options full-width">
              <summary>Advanced connection guidance</summary>
              <p>Connection-specific headers require exact HTTP/1.1. Imported unsupported socket options remain in compatibility metadata for review rather than being sent to the operating system.</p>
            </details>
            <SocketCompatibilityPanel
              additionalAttributes={script.config.web_runtime?.additional_attributes}
              originLabel="Imported script"
            />
          </div>
        </div>}
        {effectiveHTTPTransportTab === 'tls' && <div
          id="http-transport-panel-tls"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-tls"
        >
          <h5>TLS / SSL</h5>
          {tlsMaxVersionHttp3Warning && (
            <p className="full-width http3-policy-issue" role="alert" data-testid="http-tls-max-version-h3-warning">
              {tlsMaxVersionHttp3Warning}
            </p>
          )}
          <div className="form-grid">
            <label><span>Min TLS version</span>
              <select aria-label="Min TLS version" value={script.config.http?.tls?.min_version || '1.2'}
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, min_version: e.target.value } })}>
                <option value="1.0">TLS 1.0</option><option value="1.1">TLS 1.1</option>
                <option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option>
              </select></label>
            <label><span>Max TLS version</span>
              <select aria-label="Max TLS version" value={script.config.http?.tls?.max_version || ''}
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, max_version: e.target.value || undefined } })}>
                <option value="">Default (TLS 1.3)</option>
                <option value="1.0">TLS 1.0</option><option value="1.1">TLS 1.1</option>
                <option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option>
              </select></label>
            <label><span>Client certificate file</span>
              <input type="text" value={script.config.http?.tls?.client_cert_file || ''} placeholder="/etc/apexload/certs/client.pem"
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, client_cert_file: e.target.value || undefined } })} /></label>
            <label><span>Client key file</span>
              <input type="text" value={script.config.http?.tls?.client_key_file || ''} placeholder="/etc/apexload/certs/client.key"
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, client_key_file: e.target.value || undefined } })} /></label>
            <label><span>CA certificate file</span>
              <input type="text" value={script.config.http?.tls?.ca_cert_file || ''} placeholder="/etc/apexload/certs/ca.pem"
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, ca_cert_file: e.target.value || undefined } })} /></label>
            <div className="runtime-checkbox-list">
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.http?.tls?.insecure_skip_verify || false}
                  onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, insecure_skip_verify: e.target.checked } })} />
                <span>Skip certificate verification</span>
              </label>
            </div>
            <p className="new-test-inline-note">Certificate paths resolve on the generator host running the VUsers, not in the browser.</p>
          </div>
        </div>}
          </>
        )}
        {((runtimeSectionIsActive('http') && effectiveHTTPTransportTab === 'proxy') || runtimeSectionIsActive('web_runtime')) && (
          <div
            id="http-transport-panel-proxy"
            className="settings-sub-group http-transport-tab-panel"
            {...(runtimeSectionIsActive('http') ? { role: 'tabpanel', 'aria-labelledby': 'http-transport-tab-proxy' } : {})}
          >
            <h5>Proxy</h5>
            <div className="form-grid">
              <label>
                <span>Proxy mode</span>
                <select aria-label="Proxy mode" value={httpProxyMode}
                  onChange={e => updateField('config.http', { ...script.config.http, proxy_mode: e.target.value })}>
                  <option value="none">No proxy</option>
                  <option value="system">Use system / environment proxy</option>
                  <option value="custom">Custom proxy</option>
                  <option value="pac">PAC script</option>
                </select>
              </label>
              <label className="full-width"><span>PAC URL</span><input value={script.config.http?.proxy?.pac_url || ''} onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, pac_url: e.target.value } })} placeholder="https://proxy.example/config.pac" /></label>
              <label className="full-width"><span>Proxy URL</span>
                <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy.example.test:8080"
                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, url: e.target.value } })} /></label>
              {httpProxyMode === 'custom' && (
                <>
                  <label><span>Proxy username</span>
                    <input value={script.config.http?.proxy?.username || ''}
                      onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, username: e.target.value } })} /></label>
                  <label><span>Proxy password</span>
                    <input type="password" value={script.config.http?.proxy?.password || ''}
                      onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, password: e.target.value } })} /></label>
                </>
              )}
              <label className="full-width"><span>Bypass hosts</span>
                <textarea rows={3} value={formatLineList(script.config.http?.proxy?.no_proxy)} placeholder={'localhost\n*.internal.example.com'}
                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, no_proxy: parseLineList(e.target.value) } })} /></label>
            </div>
            {[
              ...scriptInactiveProxyCredentialNotes(script, 'http'),
              ...(browserTransportTabVisible ? [] : scriptInactiveProxyCredentialNotes(script, 'browser')),
            ].map(note => <p key={note} className="new-test-inline-note" data-inactive-proxy-credential-note>{note}</p>)}
          </div>
        )}
        {runtimeSectionIsActive('http') && (
          <>
        {effectiveHTTPTransportTab === 'authentication' && <div
          id="http-transport-panel-authentication"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-authentication"
        >
          <h5>Authentication</h5>
          <div className="form-grid">
            <label><span>Auth type</span>
              <select aria-label="Auth type" value={script.config.http?.auth?.type || ''}
                onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, type: e.target.value } })}>
                <option value="">None</option>
                <option value="basic">Basic</option>
                <option value="digest">Digest</option>
                <option value="bearer">Bearer token</option>
                <option value="ntlm">NTLM</option>
                <option value="negotiate">Negotiate (NTLM)</option>
                <option value="negotiate_any">Any challenge (Basic, Digest, NTLM, Negotiate)</option>
              </select></label>
            {['basic', 'digest', 'ntlm', 'negotiate', 'negotiate_any'].includes(script.config.http?.auth?.type || '') && (
              <>
                <label><span>Username</span>
                  <input aria-label="Auth username" value={script.config.http?.auth?.username || ''}
                    onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, username: e.target.value } })} /></label>
                <label><span>Password</span>
                  <input aria-label="Auth password" type="password" value={script.config.http?.auth?.password || ''}
                    onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, password: e.target.value } })} /></label>
                {['ntlm', 'negotiate'].includes(script.config.http?.auth?.type || '') && (
                  <label><span>Domain</span>
                    <input aria-label="Auth domain" value={script.config.http?.auth?.domain || ''} placeholder="CORP (sent as CORP\user)"
                      onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, domain: e.target.value || undefined } })} /></label>
                )}
              </>
            )}
            {script.config.http?.auth?.type === 'bearer' && (
              <label className="full-width"><span>Token</span>
                <input value={script.config.http?.auth?.token || ''} placeholder="eyJhbGciOiJIUzI1NiIs..."
                  onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, token: e.target.value } })} /></label>
            )}
            {Boolean(script.config.http?.auth?.type) && (
              <AuthHostsField
                hosts={script.config.http?.auth?.hosts}
                onChange={hosts => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, hosts } })}
              />
            )}
          </div>
        </div>}
        {effectiveHTTPTransportTab === 'browser' && <div
          id="http-transport-panel-browser"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-browser"
        >
          <h5>Browser Emulation</h5>
          <div className="form-grid">
            <label>
              <span>User Agent</span>
              <input type="text" value={script.config.browser?.user_agent || ''} placeholder="Engine default"
                onChange={e => updateField('config.browser', { ...script.config.browser, user_agent: e.target.value || undefined })} />
            </label>
            <label>
              <span>Browser Engine</span>
              <select value={script.config.browser?.engine || 'chromium'}
                onChange={e => updateField('config.browser', { ...script.config.browser, engine: e.target.value })}>
                <option value="chromium">Chromium</option>
                <option value="webkit">WebKit</option>
              </select>
            </label>
            <label>
              <span>Device Profile</span>
              <select value={script.config.browser?.device_profile || ''}
                onChange={e => {
                  const nextDeviceProfile = e.target.value || undefined
                  const nextProfile = lookupBrowserDeviceProfile(nextDeviceProfile)
                  updateField('config.browser', {
                    ...script.config.browser,
                    device_profile: nextDeviceProfile,
                    engine: nextProfile?.engine || script.config.browser?.engine,
                  })
                }}>
                <option value="">Custom viewport</option>
                {BROWSER_DEVICE_PROFILES.map(profile => <option key={profile.name} value={profile.name}>{formatBrowserDeviceProfileLabel(profile)}</option>)}
              </select>
            </label>
            {browserEngineAdvisory && (
              <div className="browser-config-warning">{browserEngineAdvisory}</div>
            )}
            <label>
              <span>Orientation</span>
              <select value={script.config.browser?.orientation || ''}
                onChange={e => updateField('config.browser', { ...script.config.browser, orientation: e.target.value || undefined })}>
                <option value="">Profile default</option>
                <option value="portrait">Portrait</option>
                <option value="landscape">Landscape</option>
              </select>
            </label>
            <label><span>Emulated system language</span><input value={script.config.browser?.system_language || ''} onChange={e => updateField('config.browser', { ...script.config.browser, system_language: e.target.value })} /></label>
 {(['available_screen_width', 'available_screen_height'] as const).map(field => <label key={field}><span>{field === 'available_screen_width' ? 'Available screen width' : 'Available screen height'}</span><input type="number" min="0" max="8192" value={script.config.browser?.[field] ?? ''} onChange={e => updateField('config.browser', { ...script.config.browser, [field]: Number(e.target.value) })} /></label>)}
            <label><span>Viewport Width</span>
              <input type="number" min="0" value={script.config.browser?.viewport?.width ?? ''} placeholder="393"
                onChange={e => updateField('config.browser', {
                  ...script.config.browser,
                  viewport: {
                    ...script.config.browser?.viewport,
                    width: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                  },
                })} /></label>
            <label><span>Viewport Height</span>
              <input type="number" min="0" value={script.config.browser?.viewport?.height ?? ''} placeholder="852"
                onChange={e => updateField('config.browser', {
                  ...script.config.browser,
                  viewport: {
                    ...script.config.browser?.viewport,
                    height: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                  },
                })} /></label>
            <label><span>Slow Mo (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.browser?.slow_mo_ms} emptyWhenUndefined placeholder="0.05"
                onChangeMs={value => updateField('config.browser', { ...script.config.browser, slow_mo_ms: value })} /></label>
            <label><span>Capture screenshots</span>
              <select value={script.config.browser?.capture_screenshots || 'per_action'}
                onChange={e => updateField('config.browser', { ...script.config.browser, capture_screenshots: e.target.value })}>
                <option value="off">Off</option>
                <option value="on_error">On error</option>
                <option value="per_action">Per action</option>
              </select>
            </label>
            <label><span>HAR capture</span>
              <select value={script.config.browser?.har_capture || 'off'}
                onChange={e => updateField('config.browser', { ...script.config.browser, har_capture: e.target.value === 'off' ? undefined : e.target.value })}>
                <option value="off">Off</option>
                <option value="always">Always</option>
                <option value="on_error">On error</option>
                <option value="on_sla_breach">On SLA breach</option>
                <option value="manual">Manual</option>
              </select>
            </label>
            <label>
              <span>Download resources</span>
              <select value={script.config.browser?.download_resources || 'recorded'}
                onChange={e => updateField('config.browser', { ...script.config.browser, download_resources: e.target.value })}>
                <option value="none">HTML only</option>
                <option value="recorded">Recorded resources</option>
                <option value="all">All non-HTML resources</option>
              </select>
            </label>
            <label>
              <span>HTTP/3 (QUIC)</span>
              <select value={script.config.browser?.quic || 'auto'}
                onChange={e => updateField('config.browser', { ...script.config.browser, quic: e.target.value === 'off' ? 'off' : undefined })}>
                <option value="auto">Browser default (uses HTTP/3 when a site offers it)</option>
                <option value="off" disabled={selectedBrowserEngine === 'webkit' && script.config.browser?.quic !== 'off'}>Off (launch with QUIC disabled)</option>
              </select>
            </label>
            {selectedBrowserEngine === 'webkit' && script.config.browser?.quic === 'off' && (
              <div className="browser-config-warning">WebKit cannot launch with QUIC disabled. Choose Browser default, or switch to Chromium to keep HTTP/3 off.</div>
            )}
            <div className="browser-emulation-checkbox-list" aria-label="Browser emulation toggles">
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.headless ?? true}
                  onChange={e => updateField('config.browser', { ...script.config.browser, headless: e.target.checked })} />
                <span>Run headless</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.touch_mode ?? false}
                  onChange={e => updateField('config.browser', { ...script.config.browser, touch_mode: e.target.checked })} />
                <span>Force touch mode</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.client_metrics ?? false}
                  onChange={e => updateField('config.browser', { ...script.config.browser, client_metrics: e.target.checked })} />
                <span>Capture client metrics</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.capture_document_metrics ?? false} aria-describedby="browser-capture-hint"
                  onChange={e => updateField('config.browser', { ...script.config.browser, capture_document_metrics: e.target.checked })} />
                <span>Capture page timings and Web Vitals</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.capture_interactions ?? false} aria-describedby="browser-capture-hint"
                  onChange={e => updateField('config.browser', { ...script.config.browser, capture_interactions: e.target.checked })} />
                <span>Capture interaction evidence</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.review_policy?.allow_reviewed_javascript || false}
                  onChange={e => updateField('config.browser', {
                    ...script.config.browser,
                    review_policy: {
                      ...script.config.browser?.review_policy,
                      allow_reviewed_javascript: e.target.checked,
                    },
                  })} />
                <span>Allow reviewed JavaScript</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.review_policy?.allow_review_required || false}
                  onChange={e => updateField('config.browser', {
                    ...script.config.browser,
                    review_policy: {
                      ...script.config.browser?.review_policy,
                      allow_review_required: e.target.checked,
                    },
                  })} />
                <span>Allow review-required actions</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.cache_enabled || false}
                  onChange={e => updateField('config.browser', { ...script.config.browser, cache_enabled: e.target.checked })} />
                <span>Simulate browser cache</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.cache_content_urls || false}
                  onChange={e => updateField('config.browser', { ...script.config.browser, cache_content_urls: e.target.checked })} />
                <span>Cache URLs requiring content</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.check_newer_versions || false}
                  onChange={e => updateField('config.browser', { ...script.config.browser, check_newer_versions: e.target.checked })} />
                <span>Check for newer cached pages</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.browser?.new_user_per_iteration || false}
                  onChange={e => updateField('config.browser', { ...script.config.browser, new_user_per_iteration: e.target.checked })} />
                <span>Simulate a new user each iteration</span>
              </label>
            </div>
            <p id="browser-capture-hint" className="form-hint full-width">
              Page timings and Web Vitals (TTFB, FCP, LCP, INP, CLS) fill Command Center&apos;s Browser groups panel and the run report&apos;s Browser tab. Interaction evidence records where each gesture landed, never typed values. Both are off by default and need generators that support browser evidence. A scenario or group that sets its own browser runtime settings replaces this script&apos;s, these two included; the run readiness review names any browser group left without page timings.
            </p>
          </div>
          <BrowserAccessFields browser={script.config.browser} onChange={browser => updateField('config.browser', browser)} />
          {/* A browser proxy secret stays in the script with no proxy server
              set, ready for one; nothing needs it until then. */}
          {scriptInactiveProxyCredentialNotes(script, 'browser').map(note => <p key={note} className="new-test-inline-note" data-inactive-proxy-credential-note>{note}</p>)}
        </div>}
          </>
        )}
      </div>
    )}
  </div>}

  {/* Browser Emulation (HTTP replay) */}
  {supportsBrowserEmulation && <div className={`settings-section ${runtimeSectionIsActive('browser_emulation') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('browser_emulation')}>
      <RuntimeSectionIcon section="browser_emulation" />
      <span className="section-label">Browser Emulation</span>
      <span className="section-summary">{formatBrowserEmulationSummary(script.config.browser_emulation)}</span>
      <span className="chevron">{runtimeSectionIsActive('browser_emulation') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('browser_emulation') && (
      <div className="section-body">
        <div className="settings-sub-group">
          <h5>Browser Selection</h5>
          <div className="form-grid">
            <div className="runtime-checkbox-list full-width" role="radiogroup" aria-label="Browser selection mode">
              <label className="checkbox-label">
                <input
                  type="radio"
                  name="browser-emulation-mode"
                  checked={browserEmulationMode !== 'custom'}
                  onChange={() => updateBrowserEmulation({ mode: browserEmulation?.browser_type ? 'specific' : undefined, custom_user_agent: undefined })}
                />
                <span>Use a specific browser</span>
              </label>
              <label className="checkbox-label">
                <input
                  type="radio"
                  name="browser-emulation-mode"
                  checked={browserEmulationMode === 'custom'}
                  onChange={() => updateBrowserEmulation({ mode: 'custom', browser_type: undefined, browser_version: undefined, platform: undefined })}
                />
                <span>Custom browser</span>
              </label>
            </div>
            <label>
              <span>Browser</span>
              <select
                value={browserEmulation?.browser_type || ''}
                disabled={browserEmulationMode === 'custom'}
                aria-label="Browser type"
                onChange={e => updateBrowserEmulation({
                  // Only a concrete browser makes the mode "specific"; clearing
                  // it back to Engine default unsets the mode so the section
                  // never saves as specific-without-a-browser (which the backend
                  // validator rejects).
                  mode: e.target.value ? 'specific' : undefined,
                  browser_type: e.target.value || undefined,
                  browser_version: undefined,
                  platform: undefined,
                })}
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
                value={browserEmulation?.browser_version || ''}
                disabled={browserEmulationMode === 'custom' || !selectedEmulationBrowser}
                aria-label="Browser version"
                onChange={e => updateBrowserEmulation({ browser_version: e.target.value || undefined })}
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
                value={browserEmulation?.platform || ''}
                disabled={browserEmulationMode === 'custom' || !selectedEmulationBrowser}
                aria-label="Browser platform"
                onChange={e => updateBrowserEmulation({ platform: e.target.value || undefined })}
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
                value={browserEmulation?.language || ''}
                aria-label="Browser language"
                onChange={e => updateBrowserEmulation({ language: e.target.value || undefined })}
              >
                <option value="">Engine default</option>
                {browserEmulationCatalog.languages.map(language => (
                  <option key={language.tag} value={language.tag}>{language.label}</option>
                ))}
              </select>
            </label>
            <label className="full-width">
              <span>Custom User-Agent</span>
              <input
                value={browserEmulation?.custom_user_agent || ''}
                disabled={browserEmulationMode !== 'custom'}
                placeholder="Mozilla/5.0 ..."
                aria-label="Custom User-Agent"
                onChange={e => updateBrowserEmulation({ custom_user_agent: e.target.value })}
              />
            </label>
          </div>
        </div>
        <div className="settings-sub-group">
          <div className="sub-header">
            <h5>Browser Cache</h5>
            {isSoapScript && (
              <span className="repository-status-pill" data-testid="soap-inactive-browser-emulation-cache-badge">
                Inactive for POST XML SOAP requests (GET/HTML cache &amp; non-HTML resources only)
              </span>
            )}
          </div>
          <div className="http-cache-canonical-note" role="note">
            <span>Cache enablement and per-iteration cleanup are configured in HTTP Transport → Session &amp; Cache.</span>
            <button type="button" className="link-button" onClick={() => {
              selectRuntimeSettingsSection('http')
              setActiveHTTPTransportTab('session_cache')
            }}>Open Session &amp; Cache</button>
          </div>
          <div className="runtime-checkbox-list" aria-label="Browser cache simulation">
            <label className="checkbox-label checkbox-label-indented">
              <input
                type="checkbox"
                checked={browserEmulationCache?.cache_content_urls || false}
                disabled={!httpSettingsValue.cache_enabled}
                onChange={e => updateBrowserEmulationCache({ cache_content_urls: e.target.checked })}
              />
              <span>Cache URLs requiring content (HTMLs)</span>
            </label>
            <label className="checkbox-label checkbox-label-indented">
              <input
                type="checkbox"
                checked={browserEmulationCache?.check_newer_versions || false}
                disabled={!httpSettingsValue.cache_enabled}
                onChange={e => updateBrowserEmulationCache({ check_newer_versions: e.target.checked })}
              />
              <span>Check for newer versions of stored pages on every visit</span>
            </label>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={browserEmulationCache?.download_non_html_resources || false}
                onChange={e => updateBrowserEmulationCache({ download_non_html_resources: e.target.checked })}
              />
              <span>Download non-HTML resources</span>
            </label>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={browserEmulationCache?.simulate_prefetch || false}
                onChange={e => updateBrowserEmulationCache({ simulate_prefetch: e.target.checked })}
              />
              <span>Simulate prefetch and prerender</span>
            </label>
          </div>
        </div>
      </div>
    )}
  </div>}

  {isSoapScript && (
    <SoapRuntimeSection
      script={script}
      sectionIsActive={runtimeSectionIsActive}
      onSelectSection={selectRuntimeSettingsSection}
      onUpdateField={updateField}
    />
  )}

  {isJavaHttpScript && (
    <JavaHTTPRuntimeSection
      script={script}
      sectionIsActive={runtimeSectionIsActive}
      onSelectSection={selectRuntimeSettingsSection}
      onUpdateField={updateField}
    />
  )}

  {isJavaVuserScript && <div className={`settings-section ${runtimeSectionIsActive('java_vuser') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('java_vuser')}>
      <RuntimeSectionIcon section="java_vuser" />
      <span className="section-label">Java Env Settings</span>
      <span className="section-summary">{script.config.java_vuser?.jdk_selection === 'specified' ? 'Specified JDK' : 'Internal JDK logic'}</span>
      <span className="chevron">{runtimeSectionIsActive('java_vuser') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('java_vuser') && (
      <div className="section-body">
        <JavaEnvSettingsFields
          idPrefix="script-java-vuser"
          value={script.config.java_vuser ?? {}}
          importedVMParameters={importedJavaVMParameters}
          onChange={next => updateField('config.java_vuser', { ...(script.config.java_vuser || {}), ...next })}
          dedicatedClassLoaderNote={persistentJavaVuserLifecycle
            ? 'Each Java VUser keeps its own Actions instance and child loader for its full lifecycle, and its own JVM unless shared JVMs are on. Java fields and saved LoadRunner parameters persist across init, repeated action iterations, and end, then the session is closed during teardown.'
            : 'This legacy full_lifecycle script uses one-shot JVM execution. Java fields and saved LoadRunner parameters do not persist between action invocations. Re-import the Java archive to generate the combined init/action/end lifecycle.'}
        />

        <div className="settings-sub-group">
          <h5>Runner Timeouts</h5>
          <div className="form-grid">
            <label>
              <span>Startup timeout (s)</span>
              <SecondsFromMillisecondsInput
                valueMs={script.config.java_vuser?.startup_timeout_ms}
                emptyWhenUndefined
                placeholder="0"
                aria-label="Java Vuser startup timeout"
                onChangeMs={value => updateField('config.java_vuser.startup_timeout_ms', value)}
              />
            </label>
            <label>
              <span>Action timeout (s)</span>
              <SecondsFromMillisecondsInput
                valueMs={script.config.java_vuser?.action_timeout_ms}
                emptyWhenUndefined
                placeholder="30"
                aria-label="Java Vuser action timeout"
                onChangeMs={value => updateField('config.java_vuser.action_timeout_ms', value)}
              />
            </label>
          </div>
        </div>

        <div className="settings-sub-group" data-testid="java-vuser-parameter-braces">
          <h5>Parameter delimiters</h5>
          <div className="form-grid">
            <TextField label="Parameter left brace" path="config.java_vuser.param_left_brace" value={script.config.java_vuser?.param_left_brace} placeholder="{"
              onChange={value => updateField('config.java_vuser.param_left_brace', value || undefined)} />
            <TextField label="Parameter right brace" path="config.java_vuser.param_right_brace" value={script.config.java_vuser?.param_right_brace} placeholder="}"
              onChange={value => updateField('config.java_vuser.param_right_brace', value || undefined)} />
          </div>
          {javaVuserParamBraceIssue(script.config.java_vuser?.param_left_brace, script.config.java_vuser?.param_right_brace) && (
            <p className="field-error" role="alert">{javaVuserParamBraceIssue(script.config.java_vuser?.param_left_brace, script.config.java_vuser?.param_right_brace)}</p>
          )}
          <p className="form-hint">lr.eval_string resolves parameters between these delimiters; empty keeps {'{'} and {'}'}.</p>
        </div>
      </div>
    )}
  </div>}

  {isHttpTransportScript && (
    <WebRuntimeCompatibilitySection
      script={script}
      sectionIsActive={runtimeSectionIsActive}
      onSelectSection={selectRuntimeSettingsSection}
      onUpdateField={updateField}
      httpProxyMode={httpProxyMode}
      httpProxySummary={httpProxySummary}
      onOpenCanonicalProxy={() => {
        selectRuntimeSettingsSection('http')
        setActiveHTTPTransportTab('proxy')
      }}
    />
  )}

  {/* Logging */}
  <div className={`settings-section ${runtimeSectionIsActive('log') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('log')}>
      <RuntimeSectionIcon section="log" />
      <span className="section-label">Logging</span>
      <span className="section-summary">{script.config.log?.level || 'standard'}</span>
      <span className="chevron">{runtimeSectionIsActive('log') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('log') && (
      <div className="section-body">
        <div className="logging-settings-layout">
          <label>
            <span>Log level</span>
            <select value={script.config.log?.level || 'standard'}
              onChange={e => updateField('config.log', { ...script.config.log, level: e.target.value })}>
              <option value="disabled">Disabled</option>
              <option value="standard">Standard</option>
              <option value="extended">Extended (verbose)</option>
            </select>
          </label>
          <label><span>Error history buffer (bytes)</span><input type="number" min={0} max={1048576} value={script.config.log?.error_buffer_bytes ?? ''} placeholder="102400 by default" onChange={e => updateField('config.log', { ...script.config.log, error_buffer_bytes: e.target.value === '' ? undefined : Number(e.target.value) })} /></label>
          <label><span>Max response bytes</span><input type="number" min={0} max={1048576} aria-label="Max response bytes" value={script.config.log?.max_response_bytes ?? ''} placeholder="Engine bound (4 MiB)" onChange={e => updateField('config.log', { ...script.config.log, max_response_bytes: e.target.value === '' ? undefined : Math.min(1048576, Math.max(0, Number(e.target.value) || 0)) })} /><small>Empty or 0 keeps the engine&apos;s 4 MiB response-evidence bound; a value up to 1 MiB lowers it.</small></label>
          <div className="logging-checkbox-group runtime-checkbox-list">
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.log?.on_error_only || false}
                onChange={e => updateField('config.log', { ...script.config.log, on_error_only: e.target.checked })} />
              <span>Extended log on error only</span>
            </label>
            {/* Governs RUN OUTPUT only and defaults OFF. Turning it on never edits the
                script; the stored script always keeps its literal credential values. */}
            <label className="checkbox-label">
              <input type="checkbox" checked={Boolean(script.config.log?.redact_run_evidence)}
                onChange={e => updateField('config.log', { ...script.config.log, redact_run_evidence: e.target.checked })} />
              <span>Redact credentials in run evidence</span>
            </label>
            <p className="new-test-inline-note">Redacts additional session and response evidence in run logs, snapshots, reports, archives and debugger evidence; project secrets and DSL credentials are always redacted. Does not change the script itself.</p>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.log?.extended_options?.includes('parameter_substitution') || false}
                onChange={e => updateField('config.log', {
                  ...script.config.log,
                  extended_options: e.target.checked
                    ? [...new Set([...(script.config.log?.extended_options || []), 'parameter_substitution'])]
                    : (script.config.log?.extended_options || []).filter(option => option !== 'parameter_substitution'),
                })} />
              <span>Parameter substitution</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.log?.extended_options?.includes('data_returned') || false}
                onChange={e => updateField('config.log', {
                  ...script.config.log,
                  extended_options: e.target.checked
                    ? [...new Set([...(script.config.log?.extended_options || []), 'data_returned'])]
                    : (script.config.log?.extended_options || []).filter(option => option !== 'data_returned'),
                })} />
              <span>Data returned by server</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.log?.extended_options?.includes('advanced_trace') || false}
                onChange={e => updateField('config.log', {
                  ...script.config.log,
                  extended_options: e.target.checked
                    ? [...new Set([...(script.config.log?.extended_options || []), 'advanced_trace'])]
                    : (script.config.log?.extended_options || []).filter(option => option !== 'advanced_trace'),
                })} />
              <span>Advanced trace</span>
            </label>
            <p className="new-test-inline-note">Captures DNS/connect/TLS/TTFB timings, socket reuse, TLS cipher/ALPN, compression ratios, and cookie counts when extended logging is active.</p>
          </div>
        </div>
      </div>
    )}
  </div>
  {/* Imported Source Runtime Settings (read-only) */}
  {(hasImportedRuntimeFiles || hasPreservedRunOrder) && (
    <ImportedSourceRuntimeSection
      key={script.id || script.name}
      sectionIsActive={runtimeSectionIsActive}
      onSelectSection={selectRuntimeSettingsSection}
      importedRuntimeFileNames={importedRuntimeFileNames}
      importedRuntimeFiles={importedRuntimeFiles}
      importedRuntimeDispositions={importedRuntimeDispositions}
      importedDispositionSummary={importedDispositionSummary}
      runLogic={script.config.run_logic}
    />
  )}
  <RuntimeFieldEvidence script={script} baseline={baselineConfig} fields={RUNTIME_WORKSPACE_CATEGORIES.find(category => category.id === activeSection)?.fields || []} sectionId={activeSection} sectionLabel={RUNTIME_WORKSPACE_CATEGORIES.find(category => category.id === activeSection)?.label} />
  </div>
</div>
</section>
  )

  if (embeddedInCanonicalEditor) {
    return workspaceBody
  }

  return (
    <RuntimeSettingsEditor
      mode={editorMode ?? (debugContext ? 'debug' : 'author')}
      protocol={script.protocol}
      script={script}
      baselineConfig={baselineConfig}
      debugContext={debugContext}
      activeSection={activeSection}
      activeHTTPTransportTab={effectiveHTTPTransportTab}
      onSelectSection={selectRuntimeSettingsSection}
      onSelectHTTPTransportTab={setActiveHTTPTransportTab}
    >
      {workspaceBody}
    </RuntimeSettingsEditor>
  )
}

export default ScriptRuntimeSettingsView
