import PropertyAnalyticsFields from './PropertyAnalyticsFields'
import { requiresScriptBaseURL } from './scriptAuthoringAssist'
import { MessageDetailFields } from './MessageDetailFields'
import { HTTPReplayPolicyFields } from './HTTPReplayPolicyFields'
import ThinkTimePercentageInputs from './ThinkTimePercentageInputs'
import { RuntimeCountLimit } from './RuntimeCountLimit'
import { RuntimeContextSummary, type DebugRuntimeContext } from './RuntimeContextSummary'
import { RuntimeFieldEvidence } from './RuntimeFieldEvidence'
import { RUNTIME_WORKSPACE_CATEGORIES, RUNTIME_SEARCH_TERMS, runtimeTimingIssues, effectiveThinkTimeLabel } from './runtimeWorkspaceModel'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import { useState, type ReactNode } from 'react'

import type { ScriptActionDefinition, ScriptDefinition, ScriptConfig } from '../types/scripts'
import { useBrowserEmulationCatalog } from '../hooks/useBrowserEmulationCatalog'
import { jmsProviderForProtocol } from './scriptEditorHelpers'
import { JavaEnvSettingsFields } from './JavaEnvSettingsFields'
import { readImportedJavaVMParameters } from './scriptEditorImportModel'
import { RuntimeSectionIcon } from './RuntimeSectionIcon'
import {
  ImportedSourceRuntimeSection,
  JavaHTTPRuntimeSection,
  SoapRuntimeSection,
  WebRuntimeCompatibilitySection,
} from './ScriptRuntimeSettingsProtocolSections'
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
  SCRIPT_PROTOCOL_OPTIONS,
  buildScriptRuntimeSummary,
  formatBrowserEmulationSummary,
  formatLineList,
  formatRuntimeSettingStatusLabel,
  formatScriptProtocolLabel,
  getRuntimeProtocolCapabilities,
  httpTransportTabIsAvailable,
  parseLineList,
  readImportedRuntimeFiles,
  readImportedRuntimeSettingDispositions,
  summarizeImportedRuntimeDispositions,
  runtimeSettingsSectionIsAvailable,
  type HTTPTransportTabId,
  type RuntimeSettingsSectionId,
} from './scriptEditorRuntimeSettingsModel'
import { HTTP_VERSION_OPTIONS, formatScriptHTTPVersion, scriptHTTPVersionSettings } from './httpVersionModel'
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

interface ScriptRuntimeSettingsViewProps {
  debugContext?: DebugRuntimeContext
  baselineConfig?: ScriptConfig
  hideSave?: boolean
  activeHTTPTransportTab: HTTPTransportTabId
  activeSection: RuntimeSettingsSectionId
  canEditProtocol: boolean
  draftScope?: string
  onSave: () => void
  onAddHTTPRuntimeAction?: (type: HTTPRuntimeActionType) => void
  onSelectHTTPTransportTab: (tab: HTTPTransportTabId) => void
  onSelectSection: (section: RuntimeSettingsSectionId) => void
  onUpdateField: (path: string, value: unknown) => void
  onUpdateProtocol: (protocol: string) => void
  saveDisabled: boolean
  saveTitle?: string
  saving: boolean
  script: ScriptDefinition
}

export function ScriptRuntimeSettingsView({
  debugContext,
  baselineConfig,
  hideSave = false,
  activeHTTPTransportTab,
  activeSection,
  canEditProtocol,
  draftScope,
  onSave,
  onAddHTTPRuntimeAction,
  onSelectHTTPTransportTab,
  onSelectSection,
  onUpdateField,
  onUpdateProtocol,
  saveDisabled,
  saveTitle,
  saving,
  script,
}: ScriptRuntimeSettingsViewProps) {
  const [search, setSearch] = useState('')
  const [modifiedOnly, setModifiedOnly] = useState(false)
  const timingIssues = runtimeTimingIssues(script.config)
  const httpVersionSettings = scriptHTTPVersionSettings(script.protocol, script.config.http?.version)
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
    network: isNetworkScript,
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
  const updateField = onUpdateField
  const updateProtocol = onUpdateProtocol
  const handleSave = onSave
  const saveScriptDisabled = saveDisabled
  const saveScriptTitle = saveTitle
  const persistentJavaVuserLifecycle = isJavaVuserScript && usesPersistentJavaVuserLifecycle(script)
  const importedJavaVMParameters = isJavaVuserScript ? readImportedJavaVMParameters(script.metadata?.import) : []
  const supportsBrowserEmulation = capabilities.browserEmulation
  const thinkTimeType = canonicalThinkTimeType(script.config.think_time?.type || 'fixed')
  const updateThinkTime = (patch: Partial<NonNullable<ScriptDefinition['config']['think_time']>>) => {
    updateField('config.think_time', thinkTimeConfigForType({
      ...script.config.think_time,
      ...patch,
    }, thinkTimeType))
  }

  // The 'browser' sub-tab edits config.browser.* (Playwright runtime) and is
  // shown only for browser-protocol scripts; HTTP scripts use the top-level
  // Browser Emulation section. Clamp a stale hidden tab to the first visible.
  const visibleHTTPTransportTabs = HTTP_TRANSPORT_TAB_OPTIONS.filter(tab => httpTransportTabIsAvailable(tab.id, script.protocol))
  const effectiveHTTPTransportTab: HTTPTransportTabId = visibleHTTPTransportTabs.some(tab => tab.id === activeHTTPTransportTab)
    ? activeHTTPTransportTab
    : (visibleHTTPTransportTabs[0]?.id ?? 'request_defaults')

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
  const hasPreservedRunOrder = script.config.run_logic?.block_scheduling !== undefined || Boolean(script.config.run_logic?.action_sequence?.length)
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
    cache_enabled: script.config.http?.cache_enabled ?? browserEmulationCache?.simulate_cache,
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

  const categoryModified = (category: typeof RUNTIME_WORKSPACE_CATEGORIES[number]) => category.id === 'imported_source'
    ? ['block_scheduling', 'action_sequence'].some(field => JSON.stringify(script.config.run_logic?.[field as keyof NonNullable<ScriptConfig['run_logic']>]) !== JSON.stringify(baselineConfig?.run_logic?.[field as keyof NonNullable<ScriptConfig['run_logic']>]))
    : category.fields.some(field => JSON.stringify(script.config[field]) !== JSON.stringify(baselineConfig?.[field]))
  const visibleCategories = RUNTIME_WORKSPACE_CATEGORIES.filter(category => {
    const terms = `${category.label} ${category.group} ${category.fields.join(' ')} ${RUNTIME_SEARCH_TERMS[category.id] || ''}`.toLowerCase()
    return runtimeSettingsSectionIsAvailable(category.id, script.protocol) && (category.id !== 'imported_source' || hasImportedRuntimeFiles || hasPreservedRunOrder) && search.toLowerCase().trim().split(/\s+/).every(term => terms.includes(term)) && (!modifiedOnly || categoryModified(category))
  })
  const activeSectionIsVisible = visibleCategories.some(category => category.id === activeSection)

  return (
<section className="center-document runtime-settings config-panel center-document-runtime" style={{ padding: 0 }} aria-label="Runtime Settings">
  {!hideSave && <div className="center-document-header runtime-settings-save-header">
    <div className="runtime-settings-header-actions">
      <button type="button" className="btn btn-primary" onClick={handleSave} disabled={saveScriptDisabled} title={saveScriptTitle}>
        {saving ? 'Saving...' : 'Save Script'}
      </button>
    </div>
  </div>}
  <RuntimeContextSummary script={script} debug={debugContext}>
    <label className="runtime-settings-search">Find setting<input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Timing, proxy, logging…" /></label>
    <label className="checkbox-label runtime-settings-modified-toggle"><input type="checkbox" checked={modifiedOnly} onChange={event => setModifiedOnly(event.target.checked)} />Modified only</label>
  </RuntimeContextSummary>
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
              <label>
                <span>Driver</span>
                <select value={script.config.database?.driver || 'sqlite'} onChange={e => updateField('config.database.driver', e.target.value)}>
                  <option value="sqlite">SQLite</option>
                </select>
              </label>
              <label>
                <span>DSN Secret</span>
                <input value={script.config.database?.dsn_secret || ''} onChange={e => updateField('config.database.dsn_secret', e.target.value)} placeholder="secret://perf/db-dsn" />
              </label>
              <label className="full-width">
                <span>DSN / Fixture</span>
                <input value={script.config.database?.dsn || ''} onChange={e => updateField('config.database.dsn', e.target.value)} placeholder=":memory: or file:test.db?mode=rw" />
              </label>
            </>
          )}
          {isNetworkScript && (
            <>
              <label>
                <span>Subprotocol</span>
                <select value={script.config.network?.subprotocol || 'tcp'} onChange={e => updateField('config.network.subprotocol', e.target.value)}>
                  <option value="dns">DNS</option>
                  <option value="tcp">TCP / Winsock</option>
                  <option value="udp">UDP / Winsock</option>
                  <option value="ftp">FTP</option>
                  <option value="smtp">SMTP</option>
                  <option value="pop3">POP3</option>
                  <option value="imap">IMAP</option>
                  <option value="ldap">LDAP</option>
                </select>
              </label>
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
                thinkTimeConfigForType(script.config.think_time, e.target.value),
              )}>
              <option value="fixed">Fixed</option>
              <option value="recorded">Replay think time as recorded</option>
              <option value="random_percentage">Random percentage of recorded think time</option>
              <option value="gaussian">Gaussian (Normal)</option>
              <option value="uniform">Uniform (Random)</option>
            </select>
          </label>
          {thinkTimeType === 'random_percentage' && <ThinkTimePercentageInputs value={script.config.think_time} onChange={updateThinkTime} />}
          {thinkTimeUsesMean(thinkTimeType) && (
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
          {thinkTimeUsesBounds(thinkTimeType) && (
            <>
              <label>
                <span>Min (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.think_time?.min_ms}
                  onChangeMs={value => updateThinkTime({ min_ms: value ?? 0 })} />
              </label>
              <label>
                <span>Max (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.think_time?.max_ms}
                  onChangeMs={value => updateThinkTime({ max_ms: value ?? 0 })} />
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
        {script.config.run_logic?.workload_model === 'open' && <p>Open workloads require TPS pacing. Change the workload model in Run Logic to choose a delay or interval mode.</p>}
        <div className="form-grid">
          <label className="full-width">
            <span>Pacing Mode</span>
            <select disabled={script.config.run_logic?.workload_model === 'open'} value={script.config.pacing?.type || 'none'}
              onChange={e => updateField('config.pacing', { ...script.config.pacing, type: e.target.value })}>
              <option value="none">No delay (as fast as possible)</option>
              <option value="fixed_delay">Fixed delay after iteration</option>
              <option value="random_delay">Random delay after iteration</option>
              <option value="fixed_interval">Fixed interval from iteration start</option>
              <option value="random_interval">Random interval from iteration start</option>
              <option value="tps">Target iteration rate (TPS)</option>
            </select>
          </label>
          {script.config.pacing?.type === 'fixed_delay' && (
            <label><span>Delay (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.delay_ms}
                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, delay_ms: value ?? 0 })} />
            </label>
          )}
          {script.config.pacing?.type === 'random_delay' && (
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
          {script.config.pacing?.type === 'fixed_interval' && (
            <label><span>Interval (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.interval_ms}
                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, interval_ms: value ?? 0 })} />
            </label>
          )}
          {script.config.pacing?.type === 'random_interval' && (
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
          {script.config.pacing?.type === 'tps' && (
            <label><span>Target TPS</span>
              <input type="number" min="0" step="0.1" value={script.config.pacing?.tps ?? ''}
                onChange={e => updateField('config.pacing', { ...script.config.pacing, tps: Number(e.target.value) })} />
            </label>
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
          <RuntimeCountLimit label="Iterations" value={script.config.run_logic?.iterations} onChange={value => updateField('config.run_logic', { ...script.config.run_logic, iterations: value })} help={script.config.run_logic?.workload_model === 'open' ? 'Open workloads convert this to an arrival budget multiplied by the assigned worker count.' : 'Per virtual user. Zero means no per-user iteration cap.'} />
          <RuntimeCountLimit label="Global Iterations" value={script.config.run_logic?.global_iterations} onChange={value => updateField('config.run_logic', { ...script.config.run_logic, global_iterations: value })} help="Shared execution budget. Already admitted work can finish when the budget is reached." />
          <RuntimeCountLimit label="Max Transactions" value={script.config.run_logic?.max_transactions} onChange={value => updateField('config.run_logic', { ...script.config.run_logic, max_transactions: value })} help="Counts explicit business transactions and enabled auto-transactions; this is not an HTTP request count." />
          <label>
            <span>Workload Model</span>
            <select value={script.config.run_logic?.workload_model || 'closed'}
              onChange={e => updateField('config', { ...script.config, run_logic: { ...script.config.run_logic, workload_model: e.target.value }, ...(e.target.value === 'open' ? { pacing: { ...script.config.pacing, type: 'tps', tps: Number.isFinite(script.config.pacing?.tps) && (script.config.pacing?.tps ?? 0) > 0 ? script.config.pacing!.tps : 1 } } : {}) })}>
              <option value="closed">Closed VUser</option>
              <option value="open">Open arrival-rate</option>
            </select>
          </label>
          {script.config.run_logic?.workload_model === 'open' && <>
            <label>Arrival target (iterations / second)<input aria-label="Arrival target" type="number" min="0" step="any" value={script.config.pacing?.tps ?? ''} onChange={event => updateField('config.pacing', { ...script.config.pacing, type: 'tps', tps: Number(event.target.value) })} /></label>
            <label>Maximum in-flight iterations<input aria-label="Maximum in-flight iterations" type="number" min="0" value={script.config.run_logic.max_in_flight ?? 0} onChange={event => updateField('config.run_logic', { ...script.config.run_logic, max_in_flight: Number(event.target.value) })} /><small>0 uses the engine-derived limit. Excess arrivals are shed when the limit is reached.</small></label>
          </>}
          <label>
            <span>Auto-Transactions</span>
            <select value={script.config.run_logic?.auto_transactions || 'none'}
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, auto_transactions: e.target.value })}>
              <option value="none">None</option>
              <option value="per_action">Per Action</option>
              <option value="per_iteration">Per Iteration</option>
            </select>
          </label>
          <p className="full-width">Lifecycle: init → authored actions → teardown. Imported scheduling and order values are read-only under Imported Source Runtime Settings.</p>

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
        {script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error'}
      </span>
      <span className="chevron">{runtimeSectionIsActive('errors') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('errors') && (
      <div className="section-body">
        {['http', 'http/1.1', 'http/2', 'soap', 'java_http'].includes(script.protocol) && <MessageDetailFields value={script.config.results?.message_detail} onChange={value => updateField('config.results', { ...script.config.results, message_detail: value })} />}
        <PropertyAnalyticsFields value={script.config.results?.property_analytics} onChange={value => updateField('config.results', { ...script.config.results, property_analytics: value })} />
        <div className="form-grid">
          <div className="runtime-checkbox-list">
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.error_handling?.continue_on_error || false}
                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, continue_on_error: e.target.checked })} />
              <span>Continue on error</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.error_handling?.snapshot_on_error || false}
                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, snapshot_on_error: e.target.checked })} />
              <span>Capture snapshot on error</span>
            </label>
          </div>
          <label>
            <span>Max Retries</span>
            <input type="number" value={script.config.error_handling?.max_retries || 0}
              onChange={e => updateField('config.error_handling', { ...script.config.error_handling, max_retries: Number(e.target.value) })} />
          </label>
          {(script.config.error_handling?.max_retries || 0) > 0 && (
            <>
              <label>
                <span>Retry Delay (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.error_handling?.retry_delay_ms} fallbackMs={1000}
                  onChangeMs={value => updateField('config.error_handling', { ...script.config.error_handling, retry_delay_ms: value ?? 0 })} />
              </label>
              <label>
                <span>Retry Backoff</span>
                <select value={script.config.error_handling?.retry_backoff || 'fixed'}
                  onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_backoff: e.target.value })}>
                  <option value="fixed">Fixed</option>
                  <option value="exponential">Exponential</option>
                </select>
              </label>
            </>
          )}
        </div>
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
              <label><span>Password</span>
                <input type="password" value={script.config.jms?.password || ''}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, password: e.target.value })} /></label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.jms?.mock ?? true}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, mock: e.target.checked })} />
                <span>Use local mock broker for replay</span>
              </label>
              <label><span>TLS CA Certificate Secret</span>
                <input value={script.config.jms?.tls?.ca_cert_secret || ''} placeholder="jms-broker-ca"
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
              <label><span>Destination Type</span>
                <select value={script.config.jms?.destination_type || 'queue'}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, destination_type: e.target.value })}>
                  <option value="queue">Queue</option>
                  <option value="topic">Topic</option>
                </select></label>
              <label><span>Reply To</span>
                <input value={script.config.jms?.reply_to || ''} placeholder="orders.reply"
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, reply_to: e.target.value })} /></label>
              {jmsProvider === 'servicebus' && (
                <label><span>Topic Subscription</span>
                  <input value={script.config.jms?.subscription || ''} placeholder="orders-workers"
                    onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider, subscription: e.target.value })} /></label>
              )}
              <label><span>Timeout (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.jms?.timeout_ms} fallbackMs={5000}
                  onChangeMs={value => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, timeout_ms: value ?? 0 })} /></label>
              <label><span>Acknowledgement</span>
                <select value={script.config.jms?.acknowledgement || 'auto'}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, acknowledgement: e.target.value })}>
                  <option value="auto">Auto</option>
                  <option value="dups_ok">Duplicates OK</option>
                  <option value="client">Client (mock only)</option>
                  <option value="transacted">Transacted (mock only)</option>
                </select></label>
              <label><span>Delivery Mode</span>
                <select value={script.config.jms?.delivery_mode || 'persistent'}
                  onChange={e => updateField('config.jms', { ...script.config.jms, provider: jmsProvider || script.config.jms?.provider, delivery_mode: e.target.value })}>
                  <option value="persistent">Persistent</option>
                  <option value="non_persistent">Non-persistent</option>
                </select></label>
            </div>
          </div>
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            {jmsProvider === 'ibmmq'
              ? 'IBM MQ supports mock replay only; real-mode validation fails closed until a governed MQI adapter is present.'
              : `${formatScriptProtocolLabel(script.protocol)} real execution requires a build that includes its provider adapter; use mock replay for local shakeout.`}
          </div>
        </div>
      )}
    </div>
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
              <label><span>Authentication</span>
                <select value={script.config.eventhubs?.auth_type || 'connection_string'}
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, auth_type: e.target.value })}>
                  <option value="connection_string">SAS connection string</option>
                  <option value="aad">Azure AD (managed identity)</option>
                </select></label>
              <label><span>Connection String Secret</span>
                <input value={script.config.eventhubs?.connection_string_secret || ''} placeholder="eventhubs-primary"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, connection_string_secret: e.target.value })} /></label>
              <label><span>Connection String</span>
                <input type="password" value={script.config.eventhubs?.connection_string || ''}
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, connection_string: e.target.value })} /></label>
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
              <label><span>Consumer Group</span>
                <input value={script.config.eventhubs?.consumer_group || ''} placeholder="$Default"
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, consumer_group: e.target.value })} /></label>
              <label><span>Observer Start Position</span>
                <select value={script.config.eventhubs?.start_position || 'latest'}
                  onChange={e => updateField('config.eventhubs', { ...script.config.eventhubs, start_position: e.target.value })}>
                  <option value="latest">Latest (tail)</option>
                  <option value="earliest">Earliest (partition start)</option>
                </select></label>
              <label><span>Timeout (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.eventhubs?.timeout_ms} fallbackMs={5000}
                  onChangeMs={value => updateField('config.eventhubs', { ...script.config.eventhubs, timeout_ms: value ?? 0 })} /></label>
            </div>
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
              <input type="checkbox" checked={script.config.grpc?.use_reflection ?? true}
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
            <label><span>Max Open Connections</span>
              <input type="number" value={script.config.database?.max_open_conns || 1}
                onChange={e => updateField('config.database', { ...script.config.database, max_open_conns: Number(e.target.value) })} /></label>
            <label><span>Max Idle Connections</span>
              <input type="number" value={script.config.database?.max_idle_conns || 0}
                onChange={e => updateField('config.database', { ...script.config.database, max_idle_conns: Number(e.target.value) || undefined })} /></label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.database?.read_only ?? true}
                onChange={e => updateField('config.database', { ...script.config.database, read_only: e.target.checked })} />
              <span>Read-only guard</span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.database?.rollback_on_teardown ?? true}
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
            <label><span>Password</span>
              <input type="password" value={script.config.network?.password || ''}
                onChange={e => updateField('config.network', { ...script.config.network, password: e.target.value })} /></label>
            <label><span>LDAP Base DN</span>
              <input value={script.config.network?.base_dn || ''}
                onChange={e => updateField('config.network', { ...script.config.network, base_dn: e.target.value })} placeholder="dc=example,dc=com" /></label>
          </div>
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
    {runtimeSectionIsActive('http') && (
      <div className="section-body">
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
            <label><span>HTTP Version</span>
              <select value={httpVersionSettings.selection} aria-invalid={!httpVersionSettings.valid}
                onChange={e => {
                  const value = e.target.value
                  if (value === '' || value === '1.1' || value === '2') updateField('config.http', { ...script.config.http, version: value })
                }}>
                {HTTP_VERSION_OPTIONS.map(option => (
                  <option key={option.value || 'default'} value={option.value}>{option.value === '' ? httpVersionSettings.defaultLabel : option.label}</option>
                ))}
                {!httpVersionSettings.valid && <option value="invalid" disabled>Unsupported script HTTP version</option>}
                {httpVersionSettings.selection === 'existing-auto' && <option value="existing-auto" disabled>Existing automatic setting (preserved)</option>}
              </select></label>
            {!httpVersionSettings.valid && <p role="alert">This script HTTP version is unsupported. Its original value is preserved until you choose a supported setting or edit the source JSON.</p>}
            <div className="form-hint full-width">
              Effective script policy: {httpVersionSettings.effectiveLabel}. Explicit versions are exact; required HTTP/2 never falls back to HTTP/1.1. Each request can inherit this policy or select its own policy, including Auto.
            </div>
            <label><span>Max Conns/Host</span>
              <input type="number" value={script.config.http?.max_conns_per_host || 100}
                onChange={e => updateField('config.http', { ...script.config.http, max_conns_per_host: Number(e.target.value) })} /></label>
            <label><span>Max Idle Connections</span>
              <input type="number" min={0} value={script.config.http?.max_idle_conns || 0}
                onChange={e => updateField('config.http', { ...script.config.http, max_idle_conns: Number(e.target.value) || undefined })} /></label>
            <label><span>Max Redirects</span>
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
          <div className="form-grid">
            <label><span>Min TLS Version</span>
              <select value={script.config.http?.tls?.min_version || '1.2'}
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, min_version: e.target.value } })}>
                <option value="1.0">TLS 1.0</option><option value="1.1">TLS 1.1</option>
                <option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option>
              </select></label>
            <label><span>Client Certificate File</span>
              <input type="text" value={script.config.http?.tls?.client_cert_file || ''} placeholder="/etc/apexload/certs/client.pem"
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, client_cert_file: e.target.value || undefined } })} /></label>
            <label><span>Client Key File</span>
              <input type="text" value={script.config.http?.tls?.client_key_file || ''} placeholder="/etc/apexload/certs/client.key"
                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, client_key_file: e.target.value || undefined } })} /></label>
            <label><span>CA Certificate File</span>
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
        {effectiveHTTPTransportTab === 'proxy' && <div
          id="http-transport-panel-proxy"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-proxy"
        >
          <h5>Proxy</h5>
          <div className="form-grid">
            <label>
              <span>Proxy Mode</span>
              <select value={httpProxyMode}
                onChange={e => updateField('config.http', { ...script.config.http, proxy_mode: e.target.value })}>
                <option value="none">No Proxy</option>
                <option value="system">Use system proxy</option>
                <option value="custom">Custom proxy</option>
 <option value="pac">PAC script</option>
              </select>
            </label>
            <label className="full-width"><span>PAC URL</span><input value={script.config.http?.proxy?.pac_url || ''} onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, pac_url: e.target.value } })} placeholder="https://proxy.example/config.pac" /></label>
            <label className="full-width"><span>Proxy URL</span>
              <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy:8080"
                onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, url: e.target.value } })} /></label>
            {httpProxyMode === 'custom' && (
              <>
                <label><span>Proxy Username</span>
                  <input value={script.config.http?.proxy?.username || ''}
                    onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, username: e.target.value } })} /></label>
                <label><span>Proxy Password</span>
                  <input type="password" value={script.config.http?.proxy?.password || ''}
                    onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, password: e.target.value } })} /></label>
              </>
            )}
            <label className="full-width"><span>Bypass Hosts</span>
              <textarea rows={3} value={formatLineList(script.config.http?.proxy?.no_proxy)} placeholder={'localhost\n*.internal.example.com'}
                onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, no_proxy: parseLineList(e.target.value) } })} /></label>
          </div>
        </div>}
        {effectiveHTTPTransportTab === 'authentication' && <div
          id="http-transport-panel-authentication"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-authentication"
        >
          <h5>Authentication</h5>
          <div className="form-grid">
            <label><span>Auth Type</span>
              <select value={script.config.http?.auth?.type || ''}
                onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, type: e.target.value } })}>
                <option value="">None</option>
                <option value="basic">Basic</option>
                <option value="bearer">Bearer Token</option>
                <option value="ntlm">NTLM</option>
              </select></label>
            {(script.config.http?.auth?.type === 'basic' || script.config.http?.auth?.type === 'ntlm') && (
              <>
                <label><span>Username</span>
                  <input value={script.config.http?.auth?.username || ''}
                    onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, username: e.target.value } })} /></label>
                <label><span>Password</span>
                  <input type="password" value={script.config.http?.auth?.password || ''}
                    onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, password: e.target.value } })} /></label>
              </>
            )}
            {script.config.http?.auth?.type === 'bearer' && (
              <label className="full-width"><span>Token</span>
                <input value={script.config.http?.auth?.token || ''} placeholder="eyJhbGciOiJIUzI1NiIs..."
                  onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, token: e.target.value } })} /></label>
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
            <label><span>Capture Screenshots</span>
              <select value={script.config.browser?.capture_screenshots || 'per_action'}
                onChange={e => updateField('config.browser', { ...script.config.browser, capture_screenshots: e.target.value })}>
                <option value="off">Off</option>
                <option value="on_error">On error</option>
                <option value="per_action">Per action</option>
              </select>
            </label>
            <label>
              <span>Download Resources</span>
              <select value={script.config.browser?.download_resources || 'recorded'}
                onChange={e => updateField('config.browser', { ...script.config.browser, download_resources: e.target.value })}>
                <option value="none">HTML only</option>
                <option value="recorded">Recorded resources</option>
                <option value="all">All non-HTML resources</option>
              </select>
            </label>
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
          </div>
        </div>}
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
          <h5>Browser Cache</h5>
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
            ? 'Each Java VUser keeps one JVM, Actions instance, and child loader for its full lifecycle. Java fields and saved LoadRunner parameters persist across init, repeated action iterations, and end, then the session is closed during teardown.'
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
            <span>Log Level</span>
            <select value={script.config.log?.level || 'standard'}
              onChange={e => updateField('config.log', { ...script.config.log, level: e.target.value })}>
              <option value="disabled">Disabled</option>
              <option value="standard">Standard</option>
              <option value="extended">Extended (verbose)</option>
            </select>
          </label>
          <label><span>Error history buffer (bytes)</span><input type="number" min={0} max={1048576} value={script.config.log?.error_buffer_bytes ?? ''} placeholder="102400 by default" onChange={e => updateField('config.log', { ...script.config.log, error_buffer_bytes: e.target.value === '' ? undefined : Number(e.target.value) })} /></label>
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
            <p className="new-test-inline-note">Redacts credential values from run logs, snapshots, reports, archives and debugger evidence. Does not change the script itself.</p>
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
}
