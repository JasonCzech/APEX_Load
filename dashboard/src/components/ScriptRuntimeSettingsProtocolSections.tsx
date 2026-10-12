import { type RunLogicBlock, type ScriptDefinition } from '../types/scripts'
import { useState } from 'react'
import { RuntimeSectionIcon } from './RuntimeSectionIcon'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import {
  formatContentChecks,
  formatDFEChain,
  formatKeyValueLines,
  formatLineList,
  formatRuntimeDispositionLabel,
  parseContentChecks,
  parseDFEChain,
  parseKeyValueLines,
  parseLineList,
  syncSOAPEnvelopeNamespace,
  type ImportedRuntimeDispositions,
  type ImportedRuntimeFileSections,
  type ImportedRuntimeSettingDisposition,
  type RuntimeSettingsSectionId,
} from './scriptEditorRuntimeSettingsModel'

/**
 * Protocol-scoped Runtime Settings sections, split out of ScriptRuntimeSettingsView
 * so that single render function is not the only place a protocol panel can live.
 * Each component owns one collapsible section and nothing else: it reads the draft
 * script, reports which section is open, and writes through onUpdateField. The
 * caller keeps the protocol guard (isSoapScript, isJavaHttpScript, ...) so the nav
 * and the detail pane stay driven by one capability lookup.
 */
export interface RuntimeSettingsSectionProps {
  script: ScriptDefinition
  sectionIsActive: (section: RuntimeSettingsSectionId) => boolean
  onSelectSection: (section: RuntimeSettingsSectionId) => void
  onUpdateField: (path: string, value: unknown) => void
}

export function SoapRuntimeSection({ script, sectionIsActive, onSelectSection, onUpdateField }: RuntimeSettingsSectionProps) {
  const runtimeSectionIsActive = sectionIsActive
  const selectRuntimeSettingsSection = onSelectSection
  const updateField = onUpdateField

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

  return (
    <div className={`settings-section ${runtimeSectionIsActive('soap') ? 'open' : ''}`}>
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
    </div>
  )
}

export function JavaHTTPRuntimeSection({ script, sectionIsActive, onSelectSection, onUpdateField }: RuntimeSettingsSectionProps) {
  const runtimeSectionIsActive = sectionIsActive
  const selectRuntimeSettingsSection = onSelectSection
  const updateField = onUpdateField

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

  const updateJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping', oldKey: string, newKey: string, value: string) => {
    const mapping = { ...((script.config.java_http?.[field] as Record<string, string> | undefined) || {}) }
    if (oldKey !== newKey) delete mapping[oldKey]
    mapping[newKey] = value
    updateField('config.java_http', { ...(script.config.java_http || {}), [field]: mapping })
  }

  const removeJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping', key: string) => {
    const mapping = { ...((script.config.java_http?.[field] as Record<string, string> | undefined) || {}) }
    delete mapping[key]
    updateField('config.java_http', { ...(script.config.java_http || {}), [field]: mapping })
  }

  return (
    <div className={`settings-section ${runtimeSectionIsActive('java_http') ? 'open' : ''}`}>
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
    </div>
  )
}

export interface WebRuntimeCompatibilitySectionProps extends RuntimeSettingsSectionProps {
  httpProxyMode: string
  httpProxySummary: string
}

export function WebRuntimeCompatibilitySection({ script, sectionIsActive, onSelectSection, onUpdateField, httpProxyMode, httpProxySummary }: WebRuntimeCompatibilitySectionProps) {
  const runtimeSectionIsActive = sectionIsActive
  const selectRuntimeSettingsSection = onSelectSection
  const updateField = onUpdateField

  return (
    <div className={`settings-section ${runtimeSectionIsActive('web_runtime') ? 'open' : ''}`}>
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
                source_ips: e.target.value
                  .split(/\r?\n/)
                  .map(value => value.trim())
                  .filter(Boolean),
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
                  <SecondsFromMillisecondsInput aria-label="Profile latency" valueMs={profile.latency_ms}
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
    </div>
  )
}

export interface ImportedSourceRuntimeSectionProps {
  sectionIsActive: (section: RuntimeSettingsSectionId) => boolean
  onSelectSection: (section: RuntimeSettingsSectionId) => void
  importedRuntimeFileNames: string[]
  importedRuntimeFiles: Record<string, ImportedRuntimeFileSections>
  importedRuntimeDispositions: ImportedRuntimeDispositions
  importedDispositionSummary: { mapped: number; compatibility: number; nonApplicable: number; unclassified: number; total: number }
  runLogic?: ScriptDefinition['config']['run_logic']
}

// RunLogicBlockItem renders one imported run-logic block and its children.
function RunLogicBlockItem({ block }: { block: RunLogicBlock }) {
  const mode = block.mode || 'sequential'
  const percentage = mode === 'percentage'
  // Weights are relative, so each child's share is its weight over the total.
  const totalWeight = block.children.reduce((sum, child) => sum + Math.max(child.weight || 0, 0), 0)
  return (
    <li>
      <strong>{block.name || 'Block'}</strong> <span>({mode}{block.iterations && block.iterations > 1 ? `, ${block.iterations} iterations` : ''})</span>
      <ul>
        {block.children.map((child, index) => child.block
          ? <RunLogicBlockItem key={index} block={child.block} />
          : <li key={index}>{child.action}{percentage && totalWeight > 0 ? ` — ${Math.round(Math.max(child.weight || 0, 0) * 1000 / totalWeight) / 10}%` : ''}</li>)}
      </ul>
    </li>
  )
}

export function ImportedSourceRuntimeSection({
  sectionIsActive,
  onSelectSection,
  importedRuntimeFileNames,
  importedRuntimeFiles,
  importedRuntimeDispositions,
  importedDispositionSummary,
  runLogic,
}: ImportedSourceRuntimeSectionProps) {
  const runtimeSectionIsActive = sectionIsActive
  const selectRuntimeSettingsSection = onSelectSection
  const [hiddenGroups, setHiddenGroups] = useState<Set<string>>(() => new Set())
  const groupCounts = new Map<string, number>()
  for (const sections of Object.values(importedRuntimeFiles)) {
    for (const [sectionName, keys] of Object.entries(sections)) {
      const count = Object.keys(keys).length
      if (count > 0) groupCounts.set(sectionName, (groupCounts.get(sectionName) || 0) + count)
    }
  }
  const groupNames = [...groupCounts.keys()].sort((left, right) => left.localeCompare(right))
  const selectedGroupCount = groupNames.filter(name => !hiddenGroups.has(name)).length
  const toggleGroup = (name: string) => setHiddenGroups(previous => {
    const next = new Set(previous)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    return next
  })

  return (
    <div className={`settings-section ${runtimeSectionIsActive('imported_source') ? 'open' : ''}`}>
      <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('imported_source')}>
        <RuntimeSectionIcon section="imported_source" />
        <span className="section-label">Imported Source Runtime Settings</span>
        <span className="section-summary">
          {importedDispositionSummary.total > 0
            ? `${importedDispositionSummary.mapped} mapped · ${importedDispositionSummary.compatibility} compatibility-only · ${importedDispositionSummary.nonApplicable} not applicable${importedDispositionSummary.unclassified > 0 ? ` · ${importedDispositionSummary.unclassified} unclassified` : ''}`
            : 'Preserved source files'}
        </span>
        <span className="chevron">{runtimeSectionIsActive('imported_source') ? '▾' : '▸'}</span>
      </button>
      {runtimeSectionIsActive('imported_source') && (
        <div className="section-body">
          {groupNames.length > 0 && <p className="new-test-inline-note">
            Every runtime setting found in the imported LoadRunner archive, exactly as the source declared it, with the
            disposition APEX Load applied. Mapped settings are editable in the sections on the left; compatibility-only and
            not-applicable rows are preserved read-only so nothing from the source is silently dropped.
          </p>}
          {runLogic?.blocks?.length ? (
            <section className="imported-runtime-run-order" aria-label="Imported run logic">
              <h4>Imported run logic</h4>
              <p><strong>Drives execution.</strong> Each iteration runs these blocks in order: a sequential block runs its children in order, a random block runs one child, and a percentage block runs one child by weight. Each child names an imported action group.</p>
              <ul className="imported-run-logic-blocks">{runLogic.blocks.map((block, index) => <RunLogicBlockItem key={index} block={block} />)}</ul>
            </section>
          ) : (
            <section className="imported-runtime-run-order" aria-label="Preserved run order">
              <h4>Preserved run order</h4>
              <p><strong>Preserved only — does not affect execution.</strong> The engine runs init, the authored action list, and teardown in that order. These imported values are migration evidence.</p>
              <dl><dt>Block Scheduling</dt><dd>{runLogic?.block_scheduling || 'Not specified'}</dd><dt>Action Sequence</dt><dd>{runLogic?.action_sequence?.join(', ') || 'Not specified'}</dd></dl>
              <p>Implement branching or repetition in executable control-flow actions. Change action order in the Action editor.</p>
            </section>
          )}
          {groupNames.length > 0 && <div className="imported-runtime-groups" role="group" aria-label="Imported runtime setting groups">
            <div className="imported-runtime-groups-heading"><strong>Show groups</strong><span>{selectedGroupCount} of {groupNames.length} selected</span></div>
            <div className="imported-runtime-group-actions"><button type="button" className="btn btn-sm btn-ghost" onClick={() => setHiddenGroups(new Set())} disabled={selectedGroupCount === groupNames.length}>All</button><button type="button" className="btn btn-sm btn-ghost" onClick={() => setHiddenGroups(new Set(groupNames))} disabled={selectedGroupCount === 0}>None</button></div>
            <div className="imported-runtime-group-pills">{groupNames.map(name => <button type="button" key={name} className={`imported-runtime-group-pill ${hiddenGroups.has(name) ? '' : 'selected'}`} aria-pressed={!hiddenGroups.has(name)} onClick={() => toggleGroup(name)}>[{name}] <span>{groupCounts.get(name)}</span></button>)}</div>
          </div>}
          {groupNames.length > 0 && selectedGroupCount === 0 && <p className="imported-runtime-empty-groups" role="status">No groups selected. Choose a group or select All to show its settings.</p>}
          <div className="parameter-sidebar-list">
            {importedRuntimeFileNames.map(fileName => {
              const sections = importedRuntimeFiles[fileName] || {}
              const fileDispositions = importedRuntimeDispositions[fileName]
              const selectedProfile = Boolean(fileDispositions)
              const sectionNames = Object.keys(sections).filter(name => !hiddenGroups.has(name) && Object.keys(sections[name] || {}).length > 0).sort((left, right) => left.localeCompare(right))
              if (sectionNames.length === 0) return null
              return (
                <section key={`imported-runtime-file-${fileName}`} className="imported-runtime-file">
                <div className="imported-runtime-file-header">
                  <strong>{fileName}</strong>
                  <span className="repository-status-pill imported-runtime-file-tag">
                    {selectedProfile ? 'Selected runtime profile' : 'Not selected at import — preserved only'}
                  </span>
                </div>
                {sectionNames.map(sectionName => {
                  const keys = sections[sectionName] || {}
                  const keyNames = Object.keys(keys).sort((left, right) => left.localeCompare(right))
                  if (keyNames.length === 0) return null
                  return (
                    <div key={`imported-runtime-${fileName}-${sectionName}`} className="section-body imported-runtime-section">
                      <h5 className="panel-title cell-mono imported-runtime-section-title">[{sectionName}]</h5>
                      <div className="script-parameter-table-wrap imported-runtime-table-wrap">
                        <table className="script-parameter-table imported-runtime-table" data-table-pagination="off">
                          <colgroup>
                            <col className="imported-runtime-col-key" />
                            <col className="imported-runtime-col-value" />
                            <col className="imported-runtime-col-disposition" />
                          </colgroup>
                          <thead>
                            <tr>
                              <th scope="col">Setting</th>
                              <th scope="col">Source value</th>
                              <th scope="col">Disposition</th>
                            </tr>
                          </thead>
                          <tbody>
                            {keyNames.map(key => {
                              const disposition: ImportedRuntimeSettingDisposition | undefined = fileDispositions?.[sectionName]?.[key]
                              const dispositionTone = disposition?.status === 'mapped'
                                ? 'healthy'
                                : disposition?.status === 'compatibility_only'
                                  ? 'warning'
                                  : disposition?.status === 'unclassified' || !disposition
                                    ? 'danger'
                                    : ''
                              const tooltip = [
                                disposition?.note,
                                disposition?.fields?.length ? `Destination: ${disposition.fields.join(', ')}` : '',
                                disposition?.parity_id ? `Parity entry: ${disposition.parity_id}` : '',
                              ].filter(Boolean).join('\n')
                              return (
                                <tr key={`imported-runtime-${fileName}-${sectionName}-${key}`}>
                                  <td className="cell-mono imported-runtime-key">{key}</td>
                                  <td className="cell-mono imported-runtime-value">{keys[key] === '' ? <em>(empty)</em> : keys[key]}</td>
                                  <td>
                                    <span className={`repository-status-pill imported-runtime-disposition ${dispositionTone}`} title={tooltip || undefined}>
                                      {selectedProfile ? formatRuntimeDispositionLabel(disposition) : 'Preserved (profile not selected)'}
                                    </span>
                                  </td>
                                </tr>
                              )
                            })}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )
                })}
                </section>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
