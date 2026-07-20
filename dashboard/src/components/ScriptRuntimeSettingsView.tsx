import { useEffect, useState, type ReactNode } from 'react'

import type { ScriptDefinition } from '../types/scripts'
import { jmsProviderForProtocol } from './scriptEditorHelpers'
import {
  BROWSER_DEVICE_PROFILES,
  formatBrowserDeviceProfileLabel,
  lookupBrowserDeviceProfile,
} from './scriptEditorWorkbench'
import {
  HTTP_TRANSPORT_TAB_OPTIONS,
  SCRIPT_PROTOCOL_OPTIONS,
  buildScriptRuntimeSummary,
  formatContentChecks,
  formatDFEChain,
  formatKeyValueLines,
  formatLineList,
  formatMillisecondsAsSecondsLabel,
  formatMillisecondsAsSecondsValue,
  formatRuntimeSettingStatusLabel,
  formatScriptProtocolLabel,
  getRuntimeProtocolCapabilities,
  parseContentChecks,
  parseDFEChain,
  parseKeyValueLines,
  parseLineList,
  parseSecondsInputAsMilliseconds,
  syncSOAPEnvelopeNamespace,
  type HTTPTransportTabId,
  type RuntimeSettingsSectionId,
} from './scriptEditorRuntimeSettingsModel'

const SECONDS_INPUT_STEP = '0.001'
const WEBKIT_INSTALL_ADVISORY = 'WebKit execution requires Playwright WebKit assets installed on the controller host. Run go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install webkit before running browser tests or replay sessions.'

interface RuntimeParityEntry {
  area: string
  id: string
  status: string
}

interface ScriptRuntimeSettingsSidebarProps {
  parityEntries: RuntimeParityEntry[]
  paritySummary?: string
  readiness: ReactNode
  script: Pick<ScriptDefinition, 'config'>
}

export function ScriptRuntimeSettingsSidebar({
  parityEntries,
  paritySummary,
  readiness,
  script,
}: ScriptRuntimeSettingsSidebarProps) {
  const { httpProxySummary, logLevelLabel, runLogicLabel, thinkTimeLabel } = buildScriptRuntimeSummary(script)
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
        </div>
      )}
    </>
  )
}

interface SecondsFromMillisecondsInputProps {
  valueMs?: number | null
  fallbackMs?: number
  emptyWhenUndefined?: boolean
  min?: number
  placeholder?: string
  ariaLabel?: string
  onChangeMs: (valueMs: number | undefined) => void
}

function SecondsFromMillisecondsInput({
  valueMs,
  fallbackMs = 0,
  emptyWhenUndefined = false,
  min = 0,
  placeholder,
  ariaLabel,
  onChangeMs,
}: SecondsFromMillisecondsInputProps) {
  const formattedValue = formatMillisecondsAsSecondsValue(valueMs, fallbackMs, emptyWhenUndefined)
  const [draftValue, setDraftValue] = useState(formattedValue)
  const [focused, setFocused] = useState(false)

  useEffect(() => {
    if (!focused) setDraftValue(formattedValue)
  }, [focused, formattedValue])

  return (
    <input
      type="number"
      min={min}
      step={SECONDS_INPUT_STEP}
      value={focused ? draftValue : formattedValue}
      placeholder={placeholder}
      aria-label={ariaLabel}
      onFocus={() => {
        setFocused(true)
        setDraftValue(formattedValue)
      }}
      onBlur={() => {
        setFocused(false)
        setDraftValue(formatMillisecondsAsSecondsValue(valueMs, fallbackMs, emptyWhenUndefined))
      }}
      onChange={event => {
        const nextValue = event.target.value
        setDraftValue(nextValue)
        const nextMilliseconds = parseSecondsInputAsMilliseconds(nextValue, min)
        if (nextMilliseconds !== null) onChangeMs(nextMilliseconds)
      }}
    />
  )
}

function RuntimeSectionIcon({ section }: { section: RuntimeSettingsSectionId }) {
  const iconProps = {
    className: 'runtime-section-icon-svg',
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.8,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    focusable: 'false',
  } as const

  const icon = (() => {
    switch (section) {
      case 'script_config':
        return (
          <>
            <rect x="4" y="4" width="16" height="16" rx="2" />
            <path d="M8 9h8" />
            <path d="M8 13h5" />
            <path d="M8 17h7" />
          </>
        )
      case 'think':
        return (
          <>
            <path d="M6 3h12" />
            <path d="M6 21h12" />
            <path d="M8 3v4.5a4 4 0 0 0 1.4 3l2.6 2.1 2.6-2.1a4 4 0 0 0 1.4-3V3" />
            <path d="M8 21v-4.5a4 4 0 0 1 1.4-3l2.6-2.1 2.6 2.1a4 4 0 0 1 1.4 3V21" />
          </>
        )
      case 'pacing':
        return (
          <>
            <path d="M9 3h6" />
            <circle cx="12" cy="13" r="7" />
            <path d="M12 13l3-3" />
            <path d="M12 6v2" />
          </>
        )
      case 'runlogic':
        return (
          <>
            <path d="M6 7h8a4 4 0 0 1 4 4v1" />
            <path d="m15 4 3 3-3 3" />
            <path d="M18 17h-8a4 4 0 0 1-4-4v-1" />
            <path d="m9 20-3-3 3-3" />
          </>
        )
      case 'errors':
        return (
          <>
            <path d="M12 3 2.5 20h19L12 3z" />
            <path d="M12 9v5" />
            <path d="M12 17h.01" />
          </>
        )
      case 'jms':
        return (
          <>
            <path d="M5 7h10a3 3 0 0 1 3 3v2a3 3 0 0 1-3 3H9l-4 4v-4a3 3 0 0 1-3-3v-2a3 3 0 0 1 3-3z" />
            <path d="M8 10h7" />
            <path d="M8 13h4" />
          </>
        )
      case 'websocket':
        return (
          <>
            <path d="M8 7V5a3 3 0 0 1 3-3h2a3 3 0 0 1 3 3v2" />
            <path d="M8 17v2a3 3 0 0 0 3 3h2a3 3 0 0 0 3-3v-2" />
            <path d="M5 10h14" />
            <path d="M5 14h14" />
            <path d="m17 10 2 2-2 2" />
            <path d="m7 10-2 2 2 2" />
          </>
        )
      case 'grpc':
        return (
          <>
            <circle cx="6" cy="12" r="2.5" />
            <circle cx="18" cy="6" r="2.5" />
            <circle cx="18" cy="18" r="2.5" />
            <path d="M8.4 11.1 15.8 7" />
            <path d="M8.4 12.9 15.8 17" />
          </>
        )
      case 'database':
        return (
          <>
            <ellipse cx="12" cy="5" rx="7" ry="3" />
            <path d="M5 5v10c0 1.7 3.1 3 7 3s7-1.3 7-3V5" />
            <path d="M5 10c0 1.7 3.1 3 7 3s7-1.3 7-3" />
          </>
        )
      case 'network':
        return (
          <>
            <circle cx="12" cy="12" r="2.4" />
            <circle cx="5" cy="6" r="2" />
            <circle cx="19" cy="6" r="2" />
            <circle cx="19" cy="18" r="2" />
            <path d="M7 7.2 10.2 10" />
            <path d="m13.8 10 3.2-2.8" />
            <path d="m13.8 14 3.4 2.8" />
          </>
        )
      case 'http':
        return (
          <>
            <circle cx="12" cy="12" r="9" />
            <path d="M3 12h18" />
            <path d="M12 3a13 13 0 0 1 0 18" />
            <path d="M12 3a13 13 0 0 0 0 18" />
          </>
        )
      case 'soap':
        return (
          <>
            <circle cx="9" cy="15" r="4" />
            <circle cx="15.5" cy="9" r="3.5" />
            <circle cx="17.5" cy="17.5" r="1.8" />
            <path d="M6.5 5.5h.01" />
          </>
        )
      case 'java_http':
        return (
          <>
            <path d="M8 8h8v6a4 4 0 0 1-4 4h0a4 4 0 0 1-4-4V8z" />
            <path d="M16 10h1.5a2.5 2.5 0 0 1 0 5H16" />
            <path d="M9 3c1 1 1 2 0 3" />
            <path d="M13 3c1 1 1 2 0 3" />
            <path d="M7 21h10" />
          </>
        )
      case 'web_runtime':
        return (
          <>
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <path d="M3 9h18" />
            <path d="M8 13h8" />
            <path d="M8 17h4" />
            <path d="M17 12v6" />
          </>
        )
      case 'log':
        return (
          <>
            <path d="M9 4h6l1 2h3v15H5V6h3l1-2z" />
            <path d="M9 11h6" />
            <path d="M9 15h6" />
            <path d="M9 19h4" />
          </>
        )
    }
  })()

  return (
    <span className="section-icon runtime-section-icon" aria-hidden="true">
      <svg {...iconProps} data-runtime-section-icon={section}>
        {icon}
      </svg>
    </span>
  )
}
interface ScriptRuntimeSettingsViewProps {
  activeHTTPTransportTab: HTTPTransportTabId
  activeSection: RuntimeSettingsSectionId
  canEditProtocol: boolean
  onSave: () => void
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
  activeHTTPTransportTab,
  activeSection,
  canEditProtocol,
  onSave,
  onSelectHTTPTransportTab,
  onSelectSection,
  onUpdateField,
  onUpdateProtocol,
  saveDisabled,
  saveTitle,
  saving,
  script,
}: ScriptRuntimeSettingsViewProps) {
  const capabilities = getRuntimeProtocolCapabilities(script.protocol)
  const {
    browser: isBrowserScript,
    database: isDatabaseScript,
    grpc: isGrpcScript,
    http: isHttpScript,
    httpTransport: isHttpTransportScript,
    javaHttp: isJavaHttpScript,
    jms: isJmsScript,
    network: isNetworkScript,
    soap: isSoapScript,
    webSocket: isWebSocketScript,
  } = capabilities
  const jmsProvider = jmsProviderForProtocol(script.protocol)
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

  const addSoapNamespace = () => {
    const key = window.prompt('Namespace prefix:')
    if (!key) return
    updateField('config.soap', {
      ...(script.config.soap || {}),
      namespaces: { ...(script.config.soap?.namespaces || {}), [key]: '' },
    })
  }

  const updateSoapNamespace = (oldKey: string, newKey: string, value: string) => {
    const namespaces = { ...(script.config.soap?.namespaces || {}) }
    if (oldKey !== newKey) delete namespaces[oldKey]
    namespaces[newKey] = value
    updateField('config.soap', { ...(script.config.soap || {}), namespaces })
  }

  const removeSoapNamespace = (key: string) => {
    const namespaces = { ...(script.config.soap?.namespaces || {}) }
    delete namespaces[key]
    updateField('config.soap', { ...(script.config.soap || {}), namespaces })
  }

  const addJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping') => {
    const key = window.prompt(field === 'request_mapping' ? 'Request mapping key:' : 'Response mapping key:')
    if (!key) return
    updateField('config.java_http', {
      ...(script.config.java_http || {}),
      [field]: {
        ...((script.config.java_http?.[field] as Record<string, string> | undefined) || {}),
        [key]: '',
      },
    })
  }

  const updateJavaHTTPMapping = (
    field: 'request_mapping' | 'response_mapping',
    oldKey: string,
    newKey: string,
    value: string,
  ) => {
    const mapping = {
      ...((script.config.java_http?.[field] as Record<string, string> | undefined) || {}),
    }
    if (oldKey !== newKey) delete mapping[oldKey]
    mapping[newKey] = value
    updateField('config.java_http', { ...(script.config.java_http || {}), [field]: mapping })
  }

  const removeJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping', key: string) => {
    const mapping = {
      ...((script.config.java_http?.[field] as Record<string, string> | undefined) || {}),
    }
    delete mapping[key]
    updateField('config.java_http', { ...(script.config.java_http || {}), [field]: mapping })
  }

  return (
<section className="center-document runtime-settings config-panel center-document-runtime" style={{ padding: 0 }} aria-label="Runtime Settings">
  <div className="center-document-header">
    <div>
      <h3 className="panel-title">Runtime Settings</h3>
      <p>Execution, pacing, browser, protocol, and source runtime compatibility controls.</p>
    </div>
    <div className="runtime-settings-header-actions">
      <button type="button" className="btn btn-primary" onClick={handleSave} disabled={saveScriptDisabled} title={saveScriptTitle}>
        {saving ? 'Saving...' : 'Save Script'}
      </button>
    </div>
  </div>
  <div className="runtime-settings-grid">
  <nav className="runtime-settings-nav" aria-label="Runtime setting categories">
    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('script_config') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('script_config')}>
      <RuntimeSectionIcon section="script_config" />
      <span className="section-label">Script Configuration</span>
      <span className="section-summary">{formatScriptProtocolLabel(script.protocol)}</span>
    </button>
    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('think') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('think')}>
      <RuntimeSectionIcon section="think" />
      <span className="section-label">Think Time</span>
      <span className="section-summary">{script.config.think_time?.ignore ? 'Disabled' : `${script.config.think_time?.type || 'fixed'} - ${formatMillisecondsAsSecondsLabel(script.config.think_time?.mean_ms)}`}</span>
    </button>
    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('pacing') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('pacing')}>
      <RuntimeSectionIcon section="pacing" />
      <span className="section-label">Iteration Pacing</span>
      <span className="section-summary">{script.config.pacing?.type || 'none'}</span>
    </button>
    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('runlogic') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('runlogic')}>
      <RuntimeSectionIcon section="runlogic" />
      <span className="section-label">Run Logic</span>
      <span className="section-summary">
        {script.config.run_logic?.global_iterations
          ? `${script.config.run_logic.global_iterations} shared iterations`
          : script.config.run_logic?.max_transactions
            ? `${script.config.run_logic.max_transactions} max transactions`
            : script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
      </span>
    </button>
    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('errors') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('errors')}>
      <RuntimeSectionIcon section="errors" />
      <span className="section-label">Error Handling</span>
      <span className="section-summary">{script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error'}</span>
    </button>
    {isJmsScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('jms') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('jms')}>
        <RuntimeSectionIcon section="jms" />
        <span className="section-label">{formatScriptProtocolLabel(script.protocol)}</span>
        <span className="section-summary">{script.config.jms?.mock ? 'Mock broker' : script.config.jms?.broker_url ? 'Broker configured' : 'Not configured'}</span>
      </button>
    )}
    {isWebSocketScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('websocket') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('websocket')}>
        <RuntimeSectionIcon section="websocket" />
        <span className="section-label">WebSocket Runtime</span>
        <span className="section-summary">{script.config.websocket?.reconnect ? 'Reconnect enabled' : 'Single connection'}</span>
      </button>
    )}
    {isGrpcScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('grpc') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('grpc')}>
        <RuntimeSectionIcon section="grpc" />
        <span className="section-label">gRPC Runtime</span>
        <span className="section-summary">{script.config.grpc?.use_reflection ? 'Reflection' : script.config.grpc?.proto_files?.length ? 'Proto files' : 'Contract pending'}</span>
      </button>
    )}
    {isDatabaseScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('database') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('database')}>
        <RuntimeSectionIcon section="database" />
        <span className="section-label">Database Runtime</span>
        <span className="section-summary">{script.config.database?.driver || 'sql'}</span>
      </button>
    )}
    {isNetworkScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('network') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('network')}>
        <RuntimeSectionIcon section="network" />
        <span className="section-label">Network Runtime</span>
        <span className="section-summary">{script.config.network?.subprotocol || 'tcp'}</span>
      </button>
    )}
    {isHttpTransportScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('http') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('http')}>
        <RuntimeSectionIcon section="http" />
        <span className="section-label">HTTP Transport</span>
        <span className="section-summary">{httpProxySummary}</span>
      </button>
    )}
    {isSoapScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('soap') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('soap')}>
        <RuntimeSectionIcon section="soap" />
        <span className="section-label">SOAP Settings</span>
        <span className="section-summary">{script.config.soap?.version || 'soap11'}</span>
      </button>
    )}
    {isJavaHttpScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('java_http') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('java_http')}>
        <RuntimeSectionIcon section="java_http" />
        <span className="section-label">Java over HTTP</span>
        <span className="section-summary">{script.config.java_http?.codec || 'java_serialized_object_base64'}</span>
      </button>
    )}
    {isHttpScript && (
      <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('web_runtime') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('web_runtime')}>
        <RuntimeSectionIcon section="web_runtime" />
        <span className="section-label">Web HTTP/HTML Compatibility</span>
        <span className="section-summary">
          {httpProxySummary !== 'Default'
            ? httpProxySummary
            : script.config.web_runtime?.content_checks?.length
            ? `${script.config.web_runtime.content_checks.length} content checks`
            : script.config.web_runtime?.network_simulation?.mode || 'Compatibility settings'}
        </span>
      </button>
    )}
    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('log') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('log')}>
      <RuntimeSectionIcon section="log" />
      <span className="section-label">Logging</span>
      <span className="section-summary">{script.config.log?.level || 'standard'}</span>
    </button>
  </nav>
  <div className="runtime-settings-detail" aria-live="polite">
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
          {!isJmsScript && !isDatabaseScript && !isNetworkScript && (
            <label>
              <span>{isGrpcScript ? 'gRPC Target' : isWebSocketScript ? 'WebSocket Origin' : 'Base URL'}</span>
              <input value={script.config.base_url} onChange={e => updateField('config.base_url', e.target.value)} placeholder={isGrpcScript ? 'dns:///api.example.com:443' : isWebSocketScript ? 'wss://api.example.com' : 'https://api.example.com'} />
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
        {script.config.think_time?.ignore ? 'Disabled' : `${script.config.think_time?.type || 'fixed'} — ${formatMillisecondsAsSecondsLabel(script.config.think_time?.mean_ms)}`}
      </span>
      <span className="chevron">{runtimeSectionIsActive('think') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('think') && (
      <div className="section-body">
        <div className="form-grid">
          <label>
            <span>Distribution</span>
            <select value={script.config.think_time?.type || 'fixed'}
              onChange={e => updateField('config.think_time', { ...script.config.think_time, type: e.target.value })}>
              <option value="fixed">Fixed</option>
              <option value="gaussian">Gaussian (Normal)</option>
              <option value="uniform">Uniform (Random)</option>
            </select>
          </label>
          <label>
            <span>Mean (s)</span>
            <SecondsFromMillisecondsInput valueMs={script.config.think_time?.mean_ms}
              onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, mean_ms: value ?? 0 })} />
          </label>
          {script.config.think_time?.type === 'gaussian' && (
            <label>
              <span>Std Dev (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.think_time?.std_ms}
                onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, std_ms: value ?? 0 })} />
            </label>
          )}
          {(script.config.think_time?.type === 'uniform' || script.config.think_time?.type === 'gaussian') && (
            <>
              <label>
                <span>Min (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.think_time?.min_ms}
                  onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, min_ms: value ?? 0 })} />
              </label>
              <label>
                <span>Max (s)</span>
                <SecondsFromMillisecondsInput valueMs={script.config.think_time?.max_ms}
                  onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, max_ms: value ?? 0 })} />
              </label>
            </>
          )}
          <label>
            <span>Multiplier</span>
            <input type="number" step="0.1" value={script.config.think_time?.multiplier || 1.0}
              onChange={e => updateField('config.think_time', { ...script.config.think_time, multiplier: Number(e.target.value) })} />
          </label>
          <label>
            <span>Limit (s)</span>
            <SecondsFromMillisecondsInput valueMs={script.config.think_time?.limit_ms} placeholder="0 = no limit"
              onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, limit_ms: value ?? 0 })} />
          </label>
          <div className="runtime-checkbox-list">
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.think_time?.ignore || false}
                onChange={e => updateField('config.think_time', { ...script.config.think_time, ignore: e.target.checked })} />
              <span>Ignore all think times</span>
            </label>
          </div>
        </div>
      </div>
    )}
  </div>

  {/* Pacing */}
  <div className={`settings-section ${runtimeSectionIsActive('pacing') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('pacing')}>
      <RuntimeSectionIcon section="pacing" />
      <span className="section-label">Iteration Pacing</span>
      <span className="section-summary">{script.config.pacing?.type || 'none'}</span>
      <span className="chevron">{runtimeSectionIsActive('pacing') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('pacing') && (
      <div className="section-body">
        <div className="form-grid">
          <label className="full-width">
            <span>Pacing Mode</span>
            <select value={script.config.pacing?.type || 'none'}
              onChange={e => updateField('config.pacing', { ...script.config.pacing, type: e.target.value })}>
              <option value="none">No delay (as fast as possible)</option>
              <option value="fixed_delay">Fixed delay after iteration</option>
              <option value="random_delay">Random delay after iteration</option>
              <option value="fixed_interval">Fixed interval from iteration start</option>
              <option value="random_interval">Random interval from iteration start</option>
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
        {script.config.run_logic?.global_iterations
          ? `${script.config.run_logic.global_iterations} shared iterations`
          : script.config.run_logic?.max_transactions
            ? `${script.config.run_logic.max_transactions} max transactions`
            : script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
      </span>
      <span className="chevron">{runtimeSectionIsActive('runlogic') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('runlogic') && (
      <div className="section-body">
        <div className="form-grid">
          <label>
            <span>Iterations</span>
            <input type="number" value={script.config.run_logic?.iterations || 0} placeholder="0 = run until duration"
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, iterations: Number(e.target.value) })} />
          </label>
          <label>
            <span>Global Iterations</span>
            <input type="number" value={script.config.run_logic?.global_iterations || 0} placeholder="0 = no shared cap"
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, global_iterations: Number(e.target.value) })} />
          </label>
          <label>
            <span>Max Transactions</span>
            <input type="number" value={script.config.run_logic?.max_transactions || 0} placeholder="0 = no transaction cap"
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, max_transactions: Number(e.target.value) })} />
          </label>
          <label>
            <span>Workload Model</span>
            <select value={script.config.run_logic?.workload_model || 'closed'}
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, workload_model: e.target.value })}>
              <option value="closed">Closed VUser</option>
              <option value="open">Open arrival-rate</option>
            </select>
          </label>
          <label>
            <span>Auto-Transactions</span>
            <select value={script.config.run_logic?.auto_transactions || 'none'}
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, auto_transactions: e.target.value })}>
              <option value="none">None</option>
              <option value="per_action">Per Action</option>
              <option value="per_iteration">Per Iteration</option>
            </select>
          </label>
          <label>
            <span>Block Scheduling</span>
            <select value={script.config.run_logic?.block_scheduling || 'sequential'}
              onChange={e => updateField('config.run_logic', { ...script.config.run_logic, block_scheduling: e.target.value })}>
              <option value="sequential">Sequential</option>
              <option value="random">Random</option>
              <option value="percentage">Percentage-based</option>
            </select>
          </label>
          <label className="full-width">
            <span>Action Sequence</span>
            <input value={(script.config.run_logic?.action_sequence || []).join(', ')} placeholder="init, action, teardown"
              onChange={e => updateField('config.run_logic', {
                ...script.config.run_logic,
                action_sequence: e.target.value.split(',').map(value => value.trim()).filter(Boolean),
              })} />
          </label>
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
        {httpProxySummary !== 'Default' ? httpProxySummary : script.config.http?.auth?.type ? `Auth: ${script.config.http.auth.type}` : 'Default'}
      </span>
      <span className="chevron">{runtimeSectionIsActive('http') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('http') && (
      <div className="section-body">
        <div className="http-transport-tabs" role="tablist" aria-label="HTTP Transport option groups">
          {HTTP_TRANSPORT_TAB_OPTIONS.map(tab => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              id={`http-transport-tab-${tab.id}`}
              aria-controls={`http-transport-panel-${tab.id}`}
              aria-selected={activeHTTPTransportTab === tab.id}
              className={`http-transport-tab ${activeHTTPTransportTab === tab.id ? 'active' : ''}`}
              onClick={() => setActiveHTTPTransportTab(tab.id)}
            >
              {tab.label}
            </button>
          ))}
        </div>
        {activeHTTPTransportTab === 'timeouts' && <div
          id="http-transport-panel-timeouts"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-timeouts"
        >
          <h5>Timeouts</h5>
          <div className="form-grid">
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
        {activeHTTPTransportTab === 'connection' && <div
          id="http-transport-panel-connection"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-connection"
        >
          <h5>Connection</h5>
          <div className="form-grid">
            <label><span>HTTP Version</span>
              <select value={script.config.http?.version || 'auto'}
                onChange={e => updateField('config.http', { ...script.config.http, version: e.target.value })}>
                <option value="auto">Auto-negotiate</option>
                <option value="1.1">HTTP/1.1</option>
                <option value="2">HTTP/2</option>
              </select></label>
            <label><span>Max Conns/Host</span>
              <input type="number" value={script.config.http?.max_conns_per_host || 100}
                onChange={e => updateField('config.http', { ...script.config.http, max_conns_per_host: Number(e.target.value) })} /></label>
            <label><span>Max Redirects</span>
              <input type="number" value={script.config.http?.max_redirects ?? 10}
                onChange={e => updateField('config.http', { ...script.config.http, max_redirects: Number(e.target.value) })} /></label>
          </div>
        </div>}
        {activeHTTPTransportTab === 'tls' && <div
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
            <div className="runtime-checkbox-list">
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.http?.tls?.insecure_skip_verify || false}
                  onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, insecure_skip_verify: e.target.checked } })} />
                <span>Skip certificate verification</span>
              </label>
            </div>
          </div>
        </div>}
        {activeHTTPTransportTab === 'proxy' && <div
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
              </select>
            </label>
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
        {activeHTTPTransportTab === 'authentication' && <div
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
        {activeHTTPTransportTab === 'browser' && <div
          id="http-transport-panel-browser"
          className="settings-sub-group http-transport-tab-panel"
          role="tabpanel"
          aria-labelledby="http-transport-tab-browser"
        >
          <h5>Browser Emulation</h5>
          <div className="form-grid">
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
            <label className="full-width"><span>User-Agent</span>
              <input value={script.config.http?.user_agent || ''} placeholder="Mozilla/5.0 ..."
                onChange={e => updateField('config.http', { ...script.config.http, user_agent: e.target.value })} /></label>
            <label><span>Accept-Encoding</span>
              <input value={script.config.http?.accept_encoding || ''} placeholder="gzip, deflate, br"
                onChange={e => updateField('config.http', { ...script.config.http, accept_encoding: e.target.value })} /></label>
            <label><span>Accept-Language</span>
              <input value={script.config.http?.accept_language || ''} placeholder="en-US,en;q=0.9"
                onChange={e => updateField('config.http', { ...script.config.http, accept_language: e.target.value })} /></label>
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

  {isSoapScript && <div className={`settings-section ${runtimeSectionIsActive('soap') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('soap')}>
      <RuntimeSectionIcon section="soap" />
      <span className="section-label">SOAP Settings</span>
      <span className="section-summary">
        {script.config.soap?.ws_security?.username_token?.username
          ? 'WS-Security configured'
          : script.config.soap?.version || '1.1'}
      </span>
      <span className="chevron">{runtimeSectionIsActive('soap') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('soap') && (
      <div className="section-body">
        <div className="settings-sub-group">
          <h5>Envelope</h5>
          <div className="form-grid">
            <label>
              <span>SOAP Version</span>
              <select value={script.config.soap?.version || '1.1'} onChange={e => updateField('config.soap', {
                ...(script.config.soap || {}),
                version: e.target.value,
                namespaces: syncSOAPEnvelopeNamespace(
                  e.target.value,
                  script.config.soap?.namespaces,
                  script.config.soap?.version,
                ),
              })}>
                <option value="1.1">SOAP 1.1</option>
                <option value="1.2">SOAP 1.2</option>
              </select>
            </label>
            <div className="runtime-checkbox-list">
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.soap?.fault_handling?.fail_on_fault ?? true} onChange={e => updateField('config.soap.fault_handling.fail_on_fault', e.target.checked)} />
                <span>Fail on SOAP Fault</span>
              </label>
            </div>
          </div>
        </div>

        <div className="settings-sub-group">
          <div className="sub-header"><h4>Namespaces</h4><button className="btn btn-sm btn-ghost" onClick={addSoapNamespace}>+ Add</button></div>
          {Object.entries(script.config.soap?.namespaces || {}).map(([key, value]) => (
            <div key={key} className="kv-row">
              <input className="kv-key" value={key} onChange={e => updateSoapNamespace(key, e.target.value, value)} placeholder="Prefix" />
              <input className="kv-val" value={value} onChange={e => updateSoapNamespace(key, key, e.target.value)} placeholder="Namespace URI" />
              <button className="btn-icon danger" onClick={() => removeSoapNamespace(key)}>✕</button>
            </div>
          ))}
        </div>

        <div className="settings-sub-group">
          <h5>WS-Security UsernameToken</h5>
          <div className="form-grid">
            <label>
              <span>Username</span>
              <input value={script.config.soap?.ws_security?.username_token?.username || ''} onChange={e => updateField('config.soap.ws_security.username_token.username', e.target.value)} placeholder="service-user" />
            </label>
            <label>
              <span>Password</span>
              <input type="password" value={script.config.soap?.ws_security?.username_token?.password || ''} onChange={e => updateField('config.soap.ws_security.username_token.password', e.target.value)} placeholder="secret" />
            </label>
            <label>
              <span>Password Type</span>
              <select value={script.config.soap?.ws_security?.username_token?.password_type || 'text'} onChange={e => updateField('config.soap.ws_security.username_token.password_type', e.target.value)}>
                <option value="text">text</option>
                <option value="digest">digest</option>
              </select>
            </label>
            <div className="runtime-checkbox-list">
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.soap?.ws_security?.username_token?.nonce || false} onChange={e => updateField('config.soap.ws_security.username_token.nonce', e.target.checked)} />
                <span>Include nonce</span>
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={script.config.soap?.ws_security?.username_token?.created || false} onChange={e => updateField('config.soap.ws_security.username_token.created', e.target.checked)} />
                <span>Include created timestamp</span>
              </label>
            </div>
          </div>
        </div>

        <div className="settings-sub-group">
          <h5>WS-Security Timestamp</h5>
          <div className="form-grid">
            <label>
              <span>TTL (seconds)</span>
              <input type="number" min="0" value={script.config.soap?.ws_security?.timestamp?.ttl_seconds ?? 0} onChange={e => updateField('config.soap.ws_security.timestamp.ttl_seconds', Math.max(0, Number(e.target.value) || 0))} />
            </label>
          </div>
        </div>
      </div>
    )}
  </div>}

  {isJavaHttpScript && <div className={`settings-section ${runtimeSectionIsActive('java_http') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('java_http')}>
      <RuntimeSectionIcon section="java_http" />
      <span className="section-label">Java HTTP Settings</span>
      <span className="section-summary">{`${script.config.java_http?.codec || 'java_serialized_object_base64'} · ${script.config.java_http?.helper_mode || 'embedded'}`}</span>
      <span className="chevron">{runtimeSectionIsActive('java_http') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('java_http') && (
      <div className="section-body">
        <div className="settings-sub-group">
          <h5>Codec</h5>
          <div className="form-grid">
            <label>
              <span>Wire Format</span>
              <select value={script.config.java_http?.codec || 'java_serialized_object_base64'} onChange={e => updateField('config.java_http.codec', e.target.value)}>
                <option value="java_serialized_object_base64">java_serialized_object_base64</option>
              </select>
            </label>
            <label>
              <span>Helper Mode</span>
              <select value={script.config.java_http?.helper_mode || 'embedded'} onChange={e => updateField('config.java_http.helper_mode', e.target.value)}>
                <option value="embedded">embedded</option>
                <option value="sidecar">sidecar</option>
              </select>
            </label>
            <label>
              <span>Body Encoding</span>
              <select value={script.config.java_http?.body_encoding || 'base64'} onChange={e => updateField('config.java_http.body_encoding', e.target.value)}>
                <option value="base64">base64</option>
              </select>
            </label>
          </div>
        </div>

        <div className="settings-sub-group">
          <h5>Transport Defaults</h5>
          <div className="form-grid">
            <label>
              <span>Content-Type</span>
              <input value={script.config.java_http?.content_type || ''} onChange={e => updateField('config.java_http.content_type', e.target.value)} placeholder="application/x-java-serialized-object" />
            </label>
            <label>
              <span>Accept</span>
              <input value={script.config.java_http?.accept || ''} onChange={e => updateField('config.java_http.accept', e.target.value)} placeholder="application/x-java-serialized-object" />
            </label>
            <label>
              <span>Object Schema</span>
              <input value={script.config.java_http?.object_schema || ''} onChange={e => updateField('config.java_http.object_schema', e.target.value)} placeholder="com.example.AuthRequest" />
            </label>
            <label>
              <span>Extractor Type</span>
              <input value={script.config.java_http?.extractor_type || ''} onChange={e => updateField('config.java_http.extractor_type', e.target.value)} placeholder="opaque" />
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.java_http?.preserve_binary_result ?? true} onChange={e => updateField('config.java_http.preserve_binary_result', e.target.checked)} />
              <span>Preserve binary responses as base64 text</span>
            </label>
          </div>
        </div>

        <div className="settings-sub-group">
          <div className="sub-header"><h4>Request Mapping</h4><button className="btn btn-sm btn-ghost" onClick={() => addJavaHTTPMapping('request_mapping')}>+ Add</button></div>
          <div className="cell-muted" style={{ marginBottom: 8 }}>Mappings are stored with the draft for future helper-aware serializers. Current Java-over-HTTP execution still sends opaque base64 payloads.</div>
          {Object.entries(script.config.java_http?.request_mapping || {}).length === 0 && <div className="cell-muted">No request mapping entries defined.</div>}
          {Object.entries(script.config.java_http?.request_mapping || {}).map(([key, value]) => (
            <div key={`request-${key}`} className="pair-row">
              <input value={key} onChange={e => updateJavaHTTPMapping('request_mapping', key, e.target.value, value)} placeholder="logical field" />
              <input value={value} onChange={e => updateJavaHTTPMapping('request_mapping', key, key, e.target.value)} placeholder="serialized property path" />
              <button className="btn btn-sm btn-ghost" onClick={() => removeJavaHTTPMapping('request_mapping', key)}>✕</button>
            </div>
          ))}
        </div>

        <div className="settings-sub-group">
          <div className="sub-header"><h4>Response Mapping</h4><button className="btn btn-sm btn-ghost" onClick={() => addJavaHTTPMapping('response_mapping')}>+ Add</button></div>
          {Object.entries(script.config.java_http?.response_mapping || {}).length === 0 && <div className="cell-muted">No response mapping entries defined.</div>}
          {Object.entries(script.config.java_http?.response_mapping || {}).map(([key, value]) => (
            <div key={`response-${key}`} className="pair-row">
              <input value={key} onChange={e => updateJavaHTTPMapping('response_mapping', key, e.target.value, value)} placeholder="serialized property path" />
              <input value={value} onChange={e => updateJavaHTTPMapping('response_mapping', key, key, e.target.value)} placeholder="replay variable or logical field" />
              <button className="btn btn-sm btn-ghost" onClick={() => removeJavaHTTPMapping('response_mapping', key)}>✕</button>
            </div>
          ))}
        </div>
      </div>
    )}
  </div>}

  {isHttpScript && <div className={`settings-section ${runtimeSectionIsActive('web_runtime') ? 'open' : ''}`}>
    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('web_runtime')}>
      <RuntimeSectionIcon section="web_runtime" />
      <span className="section-label">Web HTTP/HTML Compatibility</span>
      <span className="section-summary">
        {httpProxySummary !== 'Default'
          ? httpProxySummary
          : script.config.web_runtime?.content_checks?.length
          ? `${script.config.web_runtime.content_checks.length} content checks`
          : script.config.web_runtime?.network_simulation?.mode || 'Compatibility settings'}
      </span>
      <span className="chevron">{runtimeSectionIsActive('web_runtime') ? '▾' : '▸'}</span>
    </button>
    {runtimeSectionIsActive('web_runtime') && (
      <div className="section-body">
        <div className="cell-muted" style={{ marginBottom: 12 }}>
          These settings are captured in the draft now so Web HTTP/HTML scripts can mirror source runtime coverage even where full APEX Load execution support is still rolling out.
        </div>
        <div className="settings-sub-group">
          <h5>Proxy Routing</h5>
          <div className="form-grid">
            <label>
              <span>Proxy Mode</span>
              <select value={httpProxyMode}
                onChange={e => updateField('config.http', { ...script.config.http, proxy_mode: e.target.value })}>
                <option value="none">No proxy</option>
                <option value="system">Use environment proxy</option>
                <option value="custom">Custom proxy</option>
              </select>
            </label>
            <label className="full-width"><span>Proxy URL</span>
              <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy.example.test:8080"
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
        </div>
        <div className="settings-sub-group">
          <h5>Additional Attributes</h5>
          <label className="full-width">
            <span>Name=Value pairs</span>
            <textarea rows={4} placeholder={'env=perf\nregion=east'} value={formatKeyValueLines(script.config.web_runtime?.additional_attributes)}
              onChange={e => updateField('config.web_runtime', {
                ...script.config.web_runtime,
                additional_attributes: parseKeyValueLines(e.target.value),
              })} />
          </label>
        </div>
        <div className="settings-sub-group">
          <h5>Download Filters</h5>
          <div className="form-grid">
            <label>
              <span>Mode</span>
              <select value={script.config.web_runtime?.download_filters?.mode || 'none'}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  download_filters: {
                    ...script.config.web_runtime?.download_filters,
                    mode: e.target.value,
                  },
                })}>
                <option value="none">No filters</option>
                <option value="allow_list">Allow list</option>
                <option value="block_list">Block list</option>
              </select>
            </label>
            <label className="full-width">
              <span>Hosts</span>
              <textarea rows={4} placeholder={'cdn.example.test\nmetrics.example.test'} value={formatLineList(script.config.web_runtime?.download_filters?.hosts)}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  download_filters: {
                    ...script.config.web_runtime?.download_filters,
                    hosts: parseLineList(e.target.value),
                  },
                })} />
            </label>
          </div>
        </div>
        <div className="settings-sub-group">
          <h5>Content Check</h5>
          <label className="full-width">
            <span>One per line: text|scope|fail_if_found</span>
            <textarea rows={4} placeholder={'Access Denied|any_response|true'} value={formatContentChecks(script.config.web_runtime?.content_checks)}
              onChange={e => updateField('config.web_runtime', {
                ...script.config.web_runtime,
                content_checks: parseContentChecks(e.target.value),
              })} />
          </label>
        </div>
        <div className="settings-sub-group">
          <h5>Network Emulation</h5>
          <div className="form-grid">
            <label>
              <span>Mode</span>
              <select value={script.config.web_runtime?.network_simulation?.mode || 'max'}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  network_simulation: {
                    ...script.config.web_runtime?.network_simulation,
                    mode: e.target.value,
                  },
                })}>
                <option value="max">Maximum throughput</option>
                <option value="preset">Preset profile</option>
                <option value="custom">Custom profile</option>
              </select>
            </label>
            <label>
              <span>Preset</span>
              <select value={script.config.web_runtime?.network_simulation?.preset || 'dsl'}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  network_simulation: {
                    ...script.config.web_runtime?.network_simulation,
                    preset: e.target.value,
                  },
                })}>
                <option value="56k">56k</option>
                <option value="dsl">DSL</option>
                <option value="t1">T1</option>
                <option value="10mbps">10 Mbps</option>
              </select>
            </label>
            <label>
              <span>Bandwidth (Kbps)</span>
              <input type="number" value={script.config.web_runtime?.network_simulation?.bandwidth_kbps || 0}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  network_simulation: {
                    ...script.config.web_runtime?.network_simulation,
                    bandwidth_kbps: Number(e.target.value),
                  },
                })} />
            </label>
            <label>
              <span>Latency (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.web_runtime?.network_simulation?.latency_ms}
                onChangeMs={value => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  network_simulation: {
                    ...script.config.web_runtime?.network_simulation,
                    latency_ms: value ?? 0,
                  },
                })} />
            </label>
            <label>
              <span>Jitter (s)</span>
              <SecondsFromMillisecondsInput valueMs={script.config.web_runtime?.network_simulation?.jitter_ms}
                onChangeMs={value => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  network_simulation: {
                    ...script.config.web_runtime?.network_simulation,
                    jitter_ms: value ?? 0,
                  },
                })} />
            </label>
            <label>
              <span>Packet Loss (%)</span>
              <input type="number" min="0" max="100" step="0.1" value={script.config.web_runtime?.network_simulation?.packet_loss_pct || 0}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  network_simulation: {
                    ...script.config.web_runtime?.network_simulation,
                    packet_loss_pct: Number(e.target.value),
                  },
                })} />
            </label>
          </div>
        </div>
        <div className="settings-sub-group" data-testid="web-runtime-source-ips">
          <h5>Source IPs (IP spoofing)</h5>
          <div className="cell-muted" style={{ marginBottom: 12 }}>
            One IP per line. VUsers bind outgoing connections round-robin across this pool so the test presents many client addresses. The generator host must own these addresses.
          </div>
          <textarea
            aria-label="Source IP pool"
            rows={3}
            value={(script.config.web_runtime?.source_ips || []).join('\n')}
            onChange={e => updateField('config.web_runtime', {
              ...script.config.web_runtime,
              source_ips: e.target.value.split('\n'),
            })}
            placeholder={'10.0.0.5\n10.0.0.6'}
          />
        </div>
        <div className="settings-sub-group" data-testid="web-runtime-network-profiles">
          <h5>Network Profile Mix</h5>
          <div className="cell-muted" style={{ marginBottom: 12 }}>
            Distribute VUsers across weighted network conditions (e.g. 30% 3G / 70% 4G). Overrides the single profile above. Per-profile response metrics appear in results.
          </div>
          {(script.config.web_runtime?.network_profiles || []).map((profile, i) => (
            <div key={`net-profile-${i}`} className="form-grid" style={{ marginBottom: 8 }}>
              <label>
                <span>Name</span>
                <input aria-label="Profile name" value={profile.name || ''}
                  onChange={e => updateField('config.web_runtime', {
                    ...script.config.web_runtime,
                    network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, name: e.target.value } : p),
                  })} />
              </label>
              <label>
                <span>Weight</span>
                <input type="number" min="1" aria-label="Profile weight" value={profile.weight || 0}
                  onChange={e => updateField('config.web_runtime', {
                    ...script.config.web_runtime,
                    network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, weight: Number(e.target.value) } : p),
                  })} />
              </label>
              <label>
                <span>Mode</span>
                <select value={profile.mode || 'custom'}
                  onChange={e => updateField('config.web_runtime', {
                    ...script.config.web_runtime,
                    network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, mode: e.target.value } : p),
                  })}>
                  <option value="custom">Custom</option>
                  <option value="preset">Preset</option>
                  <option value="max">Maximum</option>
                </select>
              </label>
              <label>
                <span>Bandwidth (Kbps)</span>
                <input type="number" aria-label="Profile bandwidth" value={profile.bandwidth_kbps || 0}
                  onChange={e => updateField('config.web_runtime', {
                    ...script.config.web_runtime,
                    network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, bandwidth_kbps: Number(e.target.value) } : p),
                  })} />
              </label>
              <label>
                <span>Latency (s)</span>
                <SecondsFromMillisecondsInput ariaLabel="Profile latency" valueMs={profile.latency_ms}
                  onChangeMs={value => updateField('config.web_runtime', {
                    ...script.config.web_runtime,
                    network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, latency_ms: value ?? 0 } : p),
                  })} />
              </label>
              <button type="button" className="btn btn-ghost" onClick={() => updateField('config.web_runtime', {
                ...script.config.web_runtime,
                network_profiles: (script.config.web_runtime?.network_profiles || []).filter((_, idx) => idx !== i),
              })}>Remove</button>
            </div>
          ))}
          <button type="button" className="btn btn-secondary" onClick={() => updateField('config.web_runtime', {
            ...script.config.web_runtime,
            network_profiles: [...(script.config.web_runtime?.network_profiles || []), { name: '', weight: 1, mode: 'custom' }],
          })}>Add Network Profile</button>
        </div>
        <div className="settings-sub-group">
          <h5>Streaming</h5>
          <div className="cell-muted" style={{ marginBottom: 12 }}>
            Streaming settings apply runtime pacing to media-like HTTP responses.
          </div>
          <div className="form-grid">
            <label>
              <span>Mode</span>
              <select value={script.config.web_runtime?.streaming?.mode || 'adaptive'}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  streaming: {
                    ...script.config.web_runtime?.streaming,
                    mode: e.target.value,
                  },
                })}>
                <option value="adaptive">Adaptive bitrate</option>
                <option value="fixed">Fixed bitrate</option>
              </select>
            </label>
            <label>
              <span>Buffer Size (s)</span>
              <SecondsFromMillisecondsInput valueMs={(script.config.web_runtime?.streaming?.buffer_size_seconds || 0) * 1000}
                onChangeMs={value => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  streaming: {
                    ...script.config.web_runtime?.streaming,
                    buffer_size_seconds: (value ?? 0) / 1000,
                  },
                })} />
            </label>
            <label>
              <span>Fixed Bitrate (Kbps)</span>
              <input type="number" value={script.config.web_runtime?.streaming?.fixed_bitrate_kbps || 0}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  streaming: {
                    ...script.config.web_runtime?.streaming,
                    fixed_bitrate_kbps: Number(e.target.value),
                  },
                })} />
            </label>
            <label>
              <span>Bandwidth Estimation</span>
              <select value={script.config.web_runtime?.streaming?.bandwidth_estimation || 'rolling'}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  streaming: {
                    ...script.config.web_runtime?.streaming,
                    bandwidth_estimation: e.target.value,
                  },
                })}>
                <option value="rolling">Rolling</option>
                <option value="instant">Instant</option>
              </select>
            </label>
          </div>
        </div>
        <div className="settings-sub-group">
          <h5>Data Formatting</h5>
          <div className="form-grid">
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.web_runtime?.data_formatting?.auto_detect || false}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  data_formatting: {
                    ...script.config.web_runtime?.data_formatting,
                    auto_detect: e.target.checked,
                  },
                })} />
              <span>Auto-detect data format extensions</span>
            </label>
            <label className="full-width">
              <span>DFE Chain</span>
              <textarea rows={4} placeholder={'base64\ngzip mode=decode\njson_to_xml'} value={formatDFEChain(script.config.web_runtime?.data_formatting?.chain)}
                onChange={e => updateField('config.web_runtime', {
                  ...script.config.web_runtime,
                  data_formatting: {
                    ...script.config.web_runtime?.data_formatting,
                    chain: parseDFEChain(e.target.value),
                  },
                })} />
            </label>
          </div>
        </div>
      </div>
    )}
  </div>}

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
          <div className="logging-checkbox-group runtime-checkbox-list">
            <label className="checkbox-label">
              <input type="checkbox" checked={script.config.log?.on_error_only || false}
                onChange={e => updateField('config.log', { ...script.config.log, on_error_only: e.target.checked })} />
              <span>Extended log on error only</span>
            </label>
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
  </div>
</div>
</section>

  )
}
