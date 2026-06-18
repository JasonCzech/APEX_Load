import {
  type ContentCheckConfig,
  type DFETransform,
  type NetworkProfileWeight,
  type WebRuntimeConfig,
} from '../../types/scripts'
import { formatKeyValues, formatList, parseKeyValues, parseList } from '../runtimeFieldParsers'

export interface WebHttpTabProps {
  web?: WebRuntimeConfig
  /** Replace the whole web_runtime section (undefined clears it). */
  setWeb: (next: WebRuntimeConfig | undefined) => void
}

function isWebEmpty(web: WebRuntimeConfig): boolean {
  return !Object.values(web).some(value => {
    if (value === undefined || value === null) return false
    if (Array.isArray(value)) return value.length > 0
    if (typeof value === 'object') return Object.keys(value).length > 0
    return true
  })
}

/**
 * WebHttpTab edits the full WebRuntimeConfig override section — source IPs,
 * additional attributes, download filters, network simulation, streaming,
 * content checks, network-profile mix, and data formatting. It merges field
 * edits into the section and collapses an emptied section back to undefined so
 * an all-cleared tab does not emit a spurious override.
 */
export default function WebHttpTab({ web, setWeb }: WebHttpTabProps) {
  const patchWeb = (next: Partial<WebRuntimeConfig>) => {
    const merged = { ...web, ...next } as WebRuntimeConfig
    setWeb(isWebEmpty(merged) ? undefined : merged)
  }

  const contentChecks = web?.content_checks ?? []
  const profiles = web?.network_profiles ?? []
  const chain = web?.data_formatting?.chain ?? []

  const patchContentCheck = (index: number, next: Partial<ContentCheckConfig>) => {
    const updated = contentChecks.map((check, i) => (i === index ? { ...check, ...next } : check))
    patchWeb({ content_checks: updated })
  }
  const patchProfile = (index: number, next: Partial<NetworkProfileWeight>) => {
    const updated = profiles.map((profile, i) => (i === index ? { ...profile, ...next } : profile))
    patchWeb({ network_profiles: updated })
  }
  const patchTransform = (index: number, next: Partial<DFETransform>) => {
    const updated = chain.map((transform, i) => (i === index ? { ...transform, ...next } : transform))
    patchWeb({ data_formatting: { ...web?.data_formatting, chain: updated } })
  }

  return (
    <>
      <div className="scenario-browser-grid">
        <label>
          <span>Source IPs</span>
          <input
            className="settings-input"
            type="text"
            value={formatList(web?.source_ips)}
            onChange={event => {
              const ips = parseList(event.target.value)
              patchWeb({ source_ips: ips.length ? ips : undefined })
            }}
            placeholder="10.0.0.1, 10.0.0.2"
          />
        </label>
        <label>
          <span>Additional attributes</span>
          <textarea
            className="settings-input"
            rows={3}
            value={formatKeyValues(web?.additional_attributes)}
            onChange={event => {
              const map = parseKeyValues(event.target.value)
              patchWeb({ additional_attributes: Object.keys(map).length ? map : undefined })
            }}
            placeholder={'key=value\none per line'}
          />
        </label>
      </div>

      <div className="scenario-assignment-header">
        <div><h4>Download filters</h4></div>
      </div>
      <div className="scenario-browser-grid">
        <label>
          <span>Filter mode</span>
          <select
            className="settings-input"
            value={web?.download_filters?.mode || ''}
            onChange={event => patchWeb({ download_filters: { ...web?.download_filters, mode: event.target.value || undefined } })}
          >
            <option value="">Inherit</option>
            <option value="none">None</option>
            <option value="allow">Allow list</option>
            <option value="block">Block list</option>
          </select>
        </label>
        <label>
          <span>Filter hosts</span>
          <input
            className="settings-input"
            type="text"
            value={formatList(web?.download_filters?.hosts)}
            onChange={event => {
              const hosts = parseList(event.target.value)
              patchWeb({ download_filters: { ...web?.download_filters, hosts: hosts.length ? hosts : undefined } })
            }}
            placeholder="cdn.example.com, static.example.com"
          />
        </label>
      </div>

      <div className="scenario-assignment-header">
        <div><h4>Network simulation</h4></div>
      </div>
      <div className="scenario-browser-grid">
        <label>
          <span>Mode</span>
          <select className="settings-input" value={web?.network_simulation?.mode || ''} onChange={event => patchWeb({ network_simulation: { ...web?.network_simulation, mode: event.target.value || undefined } })}>
            <option value="">Inherit</option>
            <option value="none">None</option>
            <option value="preset">Preset</option>
            <option value="custom">Custom</option>
          </select>
        </label>
        <label><span>Preset</span><input className="settings-input" type="text" value={web?.network_simulation?.preset || ''} onChange={event => patchWeb({ network_simulation: { ...web?.network_simulation, preset: event.target.value || undefined } })} placeholder="e.g. 4g, dsl" /></label>
        <label><span>Bandwidth (kbps)</span><input className="settings-input" type="number" min={0} value={web?.network_simulation?.bandwidth_kbps ?? ''} onChange={event => patchWeb({ network_simulation: { ...web?.network_simulation, bandwidth_kbps: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder="Inherit" /></label>
        <label><span>Latency (ms)</span><input className="settings-input" type="number" min={0} value={web?.network_simulation?.latency_ms ?? ''} onChange={event => patchWeb({ network_simulation: { ...web?.network_simulation, latency_ms: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder="Inherit" /></label>
        <label><span>Jitter (ms)</span><input className="settings-input" type="number" min={0} value={web?.network_simulation?.jitter_ms ?? ''} onChange={event => patchWeb({ network_simulation: { ...web?.network_simulation, jitter_ms: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder="Inherit" /></label>
        <label><span>Packet loss (%)</span><input className="settings-input" type="number" min={0} value={web?.network_simulation?.packet_loss_pct ?? ''} onChange={event => patchWeb({ network_simulation: { ...web?.network_simulation, packet_loss_pct: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder="Inherit" /></label>
      </div>

      <div className="scenario-assignment-header">
        <div><h4>Streaming</h4></div>
      </div>
      <div className="scenario-browser-grid">
        <label>
          <span>Mode</span>
          <select className="settings-input" value={web?.streaming?.mode || ''} onChange={event => patchWeb({ streaming: { ...web?.streaming, mode: event.target.value || undefined } })}>
            <option value="">Inherit</option>
            <option value="none">None</option>
            <option value="adaptive">Adaptive</option>
            <option value="fixed">Fixed bitrate</option>
          </select>
        </label>
        <label><span>Buffer (seconds)</span><input className="settings-input" type="number" min={0} value={web?.streaming?.buffer_size_seconds ?? ''} onChange={event => patchWeb({ streaming: { ...web?.streaming, buffer_size_seconds: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder="Inherit" /></label>
        <label><span>Fixed bitrate (kbps)</span><input className="settings-input" type="number" min={0} value={web?.streaming?.fixed_bitrate_kbps ?? ''} onChange={event => patchWeb({ streaming: { ...web?.streaming, fixed_bitrate_kbps: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder="Inherit" /></label>
        <label><span>Bandwidth estimation</span><input className="settings-input" type="text" value={web?.streaming?.bandwidth_estimation || ''} onChange={event => patchWeb({ streaming: { ...web?.streaming, bandwidth_estimation: event.target.value || undefined } })} placeholder="Inherit" /></label>
      </div>

      <div className="scenario-assignment-header">
        <div><h4>Content checks</h4></div>
      </div>
      {contentChecks.map((check, index) => (
        <div className="scenario-browser-grid" key={`content-check-${index}`}>
          <label><span>Text</span><input className="settings-input" type="text" value={check.text || ''} onChange={event => patchContentCheck(index, { text: event.target.value || undefined })} placeholder="expected text" /></label>
          <label>
            <span>Scope</span>
            <select className="settings-input" value={check.scope || ''} onChange={event => patchContentCheck(index, { scope: event.target.value || undefined })}>
              <option value="">Body</option>
              <option value="body">Body</option>
              <option value="headers">Headers</option>
              <option value="url">URL</option>
            </select>
          </label>
          <label className={`scenario-option-card ${check.fail_if_found ? 'selected' : ''}`}>
            <input type="checkbox" checked={Boolean(check.fail_if_found)} onChange={event => patchContentCheck(index, { fail_if_found: event.target.checked || undefined })} />
            <div><strong>Fail if found</strong></div>
          </label>
          <label className={`scenario-option-card ${check.case_insensitive ? 'selected' : ''}`}>
            <input type="checkbox" checked={Boolean(check.case_insensitive)} onChange={event => patchContentCheck(index, { case_insensitive: event.target.checked || undefined })} />
            <div><strong>Case insensitive</strong></div>
          </label>
          <button type="button" className="link-button" onClick={() => patchWeb({ content_checks: contentChecks.filter((_, i) => i !== index) })}>Remove</button>
        </div>
      ))}
      <button type="button" className="secondary-button" onClick={() => patchWeb({ content_checks: [...contentChecks, {}] })}>Add content check</button>

      <div className="scenario-assignment-header">
        <div><h4>Network profile mix</h4></div>
      </div>
      {profiles.map((profile, index) => (
        <div className="scenario-browser-grid" key={`profile-${index}`}>
          <label><span>Name</span><input className="settings-input" type="text" value={profile.name || ''} onChange={event => patchProfile(index, { name: event.target.value })} placeholder="profile name" /></label>
          <label><span>Weight</span><input className="settings-input" type="number" min={0} value={profile.weight ?? ''} onChange={event => patchProfile(index, { weight: event.target.value === '' ? 0 : Number(event.target.value) })} placeholder="0" /></label>
          <label><span>Preset</span><input className="settings-input" type="text" value={profile.preset || ''} onChange={event => patchProfile(index, { preset: event.target.value || undefined })} placeholder="e.g. 4g" /></label>
          <label><span>Bandwidth (kbps)</span><input className="settings-input" type="number" min={0} value={profile.bandwidth_kbps ?? ''} onChange={event => patchProfile(index, { bandwidth_kbps: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="Inherit" /></label>
          <label><span>Latency (ms)</span><input className="settings-input" type="number" min={0} value={profile.latency_ms ?? ''} onChange={event => patchProfile(index, { latency_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="Inherit" /></label>
          <button type="button" className="link-button" onClick={() => patchWeb({ network_profiles: profiles.filter((_, i) => i !== index) })}>Remove</button>
        </div>
      ))}
      <button type="button" className="secondary-button" onClick={() => patchWeb({ network_profiles: [...profiles, { name: '', weight: 1 }] })}>Add network profile</button>

      <div className="scenario-assignment-header">
        <div><h4>Data formatting</h4></div>
      </div>
      <div className="scenario-option-grid">
        <label className={`scenario-option-card ${web?.data_formatting?.auto_detect ? 'selected' : ''}`}>
          <input type="checkbox" checked={Boolean(web?.data_formatting?.auto_detect)} onChange={event => patchWeb({ data_formatting: { ...web?.data_formatting, auto_detect: event.target.checked || undefined } })} />
          <div><strong>Auto-detect formatting</strong><span>Detect and decode response encodings automatically.</span></div>
        </label>
      </div>
      {chain.map((transform, index) => (
        <div className="scenario-browser-grid" key={`dfe-${index}`}>
          <label><span>Transform type</span><input className="settings-input" type="text" value={transform.type || ''} onChange={event => patchTransform(index, { type: event.target.value || undefined })} placeholder="e.g. base64_decode" /></label>
          <label>
            <span>Options</span>
            <textarea
              className="settings-input"
              rows={2}
              value={formatKeyValues(transform.options)}
              onChange={event => {
                const map = parseKeyValues(event.target.value)
                patchTransform(index, { options: Object.keys(map).length ? map : undefined })
              }}
              placeholder={'key=value\nper line'}
            />
          </label>
          <button type="button" className="link-button" onClick={() => patchWeb({ data_formatting: { ...web?.data_formatting, chain: chain.filter((_, i) => i !== index) } })}>Remove</button>
        </div>
      ))}
      <button type="button" className="secondary-button" onClick={() => patchWeb({ data_formatting: { ...web?.data_formatting, chain: [...chain, {}] } })}>Add transform</button>
    </>
  )
}
