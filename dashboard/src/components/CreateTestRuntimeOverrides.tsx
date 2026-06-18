import { useState } from 'react'
import { ScenarioDraft } from '../types'

type RuntimeSettingsState = ScenarioDraft['runtimeSettings']
type BrowserRuntimeSettings = NonNullable<RuntimeSettingsState['browser']>
type WebRuntimeSettings = NonNullable<RuntimeSettingsState['web_runtime']>

// looksLikeIp is a lightweight client-side hint mirroring the backend's net.ParseIP
// gate (config.web_runtime.source_ips). The server remains the authority; this only
// flags obvious typos before submit so the run isn't rejected after the fact.
function looksLikeIp(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return false
  const ipv4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/
  const match = trimmed.match(ipv4)
  if (match) return match.slice(1).every((octet) => Number(octet) <= 255)
  // IPv6: hex groups separated by colons (lenient — full validation is server-side).
  if (trimmed.includes(':')) {
    return /^[0-9a-fA-F:]+$/.test(trimmed) && (trimmed.match(/:/g)?.length ?? 0) >= 2
  }
  return false
}

type BrowserDeviceProfileOption = {
  name: string
  engine: 'chromium' | 'webkit'
}

const BROWSER_DEVICE_PROFILES: readonly BrowserDeviceProfileOption[] = [
  { name: 'desktop_1080p', engine: 'chromium' },
  { name: 'galaxy_s24', engine: 'chromium' },
  { name: 'ipad_air', engine: 'webkit' },
  { name: 'iphone_15_pro', engine: 'webkit' },
  { name: 'iphone_se', engine: 'webkit' },
  { name: 'pixel_8', engine: 'chromium' },
]

function formatBrowserDeviceProfileLabel(profile: BrowserDeviceProfileOption) {
  return profile.engine === 'webkit' ? `${profile.name} (WebKit)` : profile.name
}

function toBooleanSelectValue(value?: boolean) {
  if (value === undefined) return ''
  return value ? 'true' : 'false'
}

function fromBooleanSelectValue(value: string): boolean | undefined {
  if (value === 'true') return true
  if (value === 'false') return false
  return undefined
}

interface Props {
  runtimeSettings: RuntimeSettingsState
  onRuntimeSettingChange: <K extends keyof RuntimeSettingsState>(field: K, value: RuntimeSettingsState[K]) => void
}

export default function CreateTestRuntimeOverrides({ runtimeSettings, onRuntimeSettingChange }: Props) {
  const pacingType = runtimeSettings.pacing?.type || ''

  const initialSourceIps = runtimeSettings.web_runtime?.source_ips ?? []
  const [spoofEnabled, setSpoofEnabled] = useState(initialSourceIps.length > 0)
  // Local rows hold the in-progress editor state (including a blank trailing row);
  // only trimmed, non-empty values are committed up to the scenario draft.
  const [ipRows, setIpRows] = useState<string[]>(initialSourceIps.length > 0 ? initialSourceIps : [''])

  const commitSourceIps = (rows: string[]) => {
    const cleaned = rows.map((row) => row.trim()).filter(Boolean)
    const base: WebRuntimeSettings = { ...(runtimeSettings.web_runtime || {}) }
    if (cleaned.length === 0) {
      delete base.source_ips
      onRuntimeSettingChange('web_runtime', Object.keys(base).length > 0 ? base : undefined)
      return
    }
    onRuntimeSettingChange('web_runtime', { ...base, source_ips: cleaned })
  }

  const toggleSpoof = (checked: boolean) => {
    setSpoofEnabled(checked)
    if (checked) {
      commitSourceIps(ipRows)
    } else {
      setIpRows([''])
      commitSourceIps([])
    }
  }

  const updateIpRow = (index: number, value: string) => {
    const next = ipRows.map((row, i) => (i === index ? value : row))
    setIpRows(next)
    commitSourceIps(next)
  }

  const addIpRow = () => setIpRows([...ipRows, ''])

  const removeIpRow = (index: number) => {
    const filtered = ipRows.filter((_, i) => i !== index)
    const next = filtered.length > 0 ? filtered : ['']
    setIpRows(next)
    commitSourceIps(next)
  }

  const hasInvalidIp = ipRows.some((row) => row.trim() !== '' && !looksLikeIp(row))

  const updateBrowserSetting = <K extends keyof BrowserRuntimeSettings>(field: K, value: BrowserRuntimeSettings[K]) => {
    const nextBrowser: BrowserRuntimeSettings = { ...(runtimeSettings.browser || {}) }

    if (value === undefined) {
      const rest = { ...nextBrowser }
      delete rest[field]
      onRuntimeSettingChange('browser', Object.keys(rest).length > 0 ? rest as BrowserRuntimeSettings : undefined)
      return
    }

    onRuntimeSettingChange('browser', {
      ...nextBrowser,
      [field]: value,
    } as BrowserRuntimeSettings)
  }

  return (
    <div className="runtime-overrides">
      <h4>Runtime Settings Overrides</h4>
      <div className="overrides-grid">
        <label>
          <span>Iterations</span>
          <input
            type="number"
            value={runtimeSettings.iterations || ''}
            onChange={(event) => onRuntimeSettingChange('iterations', event.target.value ? Number(event.target.value) : undefined)}
            placeholder="Script default"
          />
        </label>
        <label>
          <span>Pacing</span>
          <select
            value={pacingType}
            onChange={(event) => {
              const type = event.target.value
              if (!type) {
                onRuntimeSettingChange('pacing', undefined)
                return
              }
              if (type === 'none') {
                onRuntimeSettingChange('pacing', { type: 'none' })
                return
              }
              if (type === 'fixed_interval') {
                onRuntimeSettingChange('pacing', { type, interval_ms: runtimeSettings.pacing?.interval_ms ?? 0 })
                return
              }
              if (type === 'tps') {
                onRuntimeSettingChange('pacing', { type, tps: runtimeSettings.pacing?.tps ?? 0 })
                return
              }
              onRuntimeSettingChange('pacing', { type, delay_ms: runtimeSettings.pacing?.delay_ms ?? 0 })
            }}
          >
            <option value="">Script default</option>
            <option value="none">None</option>
            <option value="fixed_delay">Fixed delay</option>
            <option value="fixed_interval">Fixed interval</option>
            <option value="tps">Target TPS</option>
          </select>
        </label>
        {pacingType === 'fixed_delay' && (
          <label>
            <span>Pacing delay (ms)</span>
            <input
              type="number"
              value={runtimeSettings.pacing?.delay_ms ?? ''}
              onChange={(event) => onRuntimeSettingChange('pacing', {
                type: 'fixed_delay',
                delay_ms: event.target.value ? Number(event.target.value) : undefined,
              })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'fixed_interval' && (
          <label>
            <span>Pacing interval (ms)</span>
            <input
              type="number"
              value={runtimeSettings.pacing?.interval_ms ?? ''}
              onChange={(event) => onRuntimeSettingChange('pacing', {
                type: 'fixed_interval',
                interval_ms: event.target.value ? Number(event.target.value) : undefined,
              })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'tps' && (
          <label>
            <span>Target TPS</span>
            <input
              type="number"
              value={runtimeSettings.pacing?.tps ?? ''}
              onChange={(event) => onRuntimeSettingChange('pacing', {
                type: 'tps',
                tps: event.target.value ? Number(event.target.value) : undefined,
              })}
              placeholder="0"
            />
          </label>
        )}
        <label>
          <span>Think Time Override (ms)</span>
          <input
            type="number"
            value={runtimeSettings.think_time?.ignore ? '' : (runtimeSettings.think_time?.mean_ms ?? '')}
            onChange={(event) => onRuntimeSettingChange('think_time', event.target.value
              ? { type: 'fixed', mean_ms: Number(event.target.value), ignore: false }
              : undefined)}
            placeholder="Script default"
            disabled={runtimeSettings.think_time?.ignore || false}
          />
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.think_time?.ignore || false}
            onChange={(event) => onRuntimeSettingChange('think_time', event.target.checked
              ? { type: 'fixed', mean_ms: runtimeSettings.think_time?.mean_ms ?? 0, ignore: true }
              : undefined)}
          />
          <span>Ignore think time</span>
        </label>
        <label>
          <span>Browser Device Profile</span>
          <select
            value={runtimeSettings.browser?.device_profile || ''}
            onChange={(event) => updateBrowserSetting('device_profile', event.target.value || undefined)}
          >
            <option value="">Script default</option>
            {BROWSER_DEVICE_PROFILES.map((profile) => (
              <option key={profile.name} value={profile.name}>{formatBrowserDeviceProfileLabel(profile)}</option>
            ))}
          </select>
        </label>
        <label>
          <span>Orientation</span>
          <select
            value={runtimeSettings.browser?.orientation || ''}
            onChange={(event) => updateBrowserSetting('orientation', event.target.value || undefined)}
          >
            <option value="">Script default</option>
            <option value="portrait">Portrait</option>
            <option value="landscape">Landscape</option>
          </select>
        </label>
        <label>
          <span>Browser Headless</span>
          <select
            value={toBooleanSelectValue(runtimeSettings.browser?.headless)}
            onChange={(event) => updateBrowserSetting('headless', fromBooleanSelectValue(event.target.value))}
          >
            <option value="">Script default</option>
            <option value="true">Enabled</option>
            <option value="false">Disabled</option>
          </select>
        </label>
        <label>
          <span>Touch Mode</span>
          <select
            value={toBooleanSelectValue(runtimeSettings.browser?.touch_mode)}
            onChange={(event) => updateBrowserSetting('touch_mode', fromBooleanSelectValue(event.target.value))}
          >
            <option value="">Script default</option>
            <option value="true">Enabled</option>
            <option value="false">Disabled</option>
          </select>
        </label>
        <label>
          <span>Log Level</span>
          <select
            value={runtimeSettings.log_level}
            onChange={(event) => onRuntimeSettingChange('log_level', event.target.value)}
          >
            <option value="">Script default</option>
            <option value="disabled">Disabled</option>
            <option value="standard">Standard</option>
            <option value="extended">Extended</option>
          </select>
        </label>
        <label>
          <span>Auto-Transactions</span>
          <select
            value={runtimeSettings.auto_transactions}
            onChange={(event) => onRuntimeSettingChange('auto_transactions', event.target.value)}
          >
            <option value="">Script default</option>
            <option value="none">None</option>
            <option value="per_action">Per Action</option>
            <option value="per_iteration">Per Iteration</option>
          </select>
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.continue_on_error}
            onChange={(event) => onRuntimeSettingChange('continue_on_error', event.target.checked)}
          />
          <span>Continue on error</span>
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.snapshot_on_error}
            onChange={(event) => onRuntimeSettingChange('snapshot_on_error', event.target.checked)}
          />
          <span>Snapshot on error</span>
        </label>
      </div>

      <section className="ip-spoofing">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={spoofEnabled}
            onChange={(event) => toggleSpoof(event.target.checked)}
          />
          <span>Enable IP spoofing (source IP pool)</span>
        </label>
        {spoofEnabled && (
          <div className="ip-spoofing-body">
            <p className="ip-spoofing-help">
              Binds virtual users across a pool of local source IP addresses (round-robin per VUser),
              so the test presents many distinct client addresses to the target. Applies to HTTP/HTTPS
              traffic only. Every generator host must already own these addresses on its interfaces;
              in distributed runs all generators use the full pool (IPs are not partitioned per host).
            </p>
            <div className="ip-spoofing-rows">
              {ipRows.map((row, index) => {
                const invalid = row.trim() !== '' && !looksLikeIp(row)
                return (
                  <div className="ip-spoofing-row" key={index}>
                    <input
                      type="text"
                      className={invalid ? 'ip-spoofing-input ip-spoofing-input-invalid' : 'ip-spoofing-input'}
                      value={row}
                      onChange={(event) => updateIpRow(index, event.target.value)}
                      placeholder="10.0.0.1"
                      aria-label={`Source IP ${index + 1}`}
                      aria-invalid={invalid}
                    />
                    <button
                      type="button"
                      className="btn-remove"
                      onClick={() => removeIpRow(index)}
                      aria-label={`Remove source IP ${index + 1}`}
                      disabled={ipRows.length === 1 && row.trim() === ''}
                    >
                      ×
                    </button>
                  </div>
                )
              })}
            </div>
            {hasInvalidIp && (
              <p className="ip-spoofing-error">Enter valid IPv4 or IPv6 addresses.</p>
            )}
            <button type="button" className="btn-add-ip" onClick={addIpRow}>+ Add IP</button>
          </div>
        )}
      </section>
    </div>
  )
}
