import { useRef, useState } from 'react'
import { ScenarioDraft } from '../types'
import { JavaEnvSettingsFields, type JavaEnvSettingsValue } from './JavaEnvSettingsFields'
import { useBrowserEmulationCatalog } from '../hooks/useBrowserEmulationCatalog'
import {
  canonicalThinkTimeType,
  thinkTimeConfigForType,
  thinkTimeUsesBounds,
  thinkTimeUsesMean,
  thinkTimeUsesStdDeviation,
} from './thinkTimeConfig'
import ThinkTimeMultiplierInput from './ThinkTimeMultiplierInput'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'

type RuntimeSettingsState = ScenarioDraft['runtimeSettings']
type BrowserRuntimeSettings = NonNullable<RuntimeSettingsState['browser']>
type WebRuntimeSettings = NonNullable<RuntimeSettingsState['web_runtime']>
type BrowserEmulationSettings = NonNullable<RuntimeSettingsState['browser_emulation']>

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
  draftScope?: string
  runtimeSettings: RuntimeSettingsState
  onRuntimeSettingChange: <K extends keyof RuntimeSettingsState>(field: K, value: RuntimeSettingsState[K]) => void
}

export default function CreateTestRuntimeOverrides({ draftScope, runtimeSettings, onRuntimeSettingChange }: Props) {
  const pacingType = runtimeSettings.pacing?.type || ''
  const thinkTimeType = canonicalThinkTimeType(runtimeSettings.think_time?.type)
  const ignoreOnlyThinkTimeOverride = useRef(false)

  const updateThinkTime = (patch: Partial<NonNullable<RuntimeSettingsState['think_time']>>) => {
    ignoreOnlyThinkTimeOverride.current = false
    const type = runtimeSettings.think_time?.type || 'fixed'
    onRuntimeSettingChange('think_time', {
      ...thinkTimeConfigForType(runtimeSettings.think_time, type),
      ...patch,
    })
  }

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

  const [javaEnvOpen, setJavaEnvOpen] = useState(Boolean(runtimeSettings.java_vuser))

  const [browserEmulationOpen, setBrowserEmulationOpen] = useState(Boolean(runtimeSettings.browser_emulation))
  const browserEmulation = runtimeSettings.browser_emulation
  // Live catalog (bundled fallback until it resolves) so this editor offers the
  // same browsers the engine can actually resolve — see useBrowserEmulationCatalog.
  const browserEmulationCatalog = useBrowserEmulationCatalog(browserEmulationOpen)
  const selectedEmulationBrowser = browserEmulationCatalog.browsers.find(
    (browser) => browser.type === browserEmulation?.browser_type,
  )
  const updateBrowserEmulation = (patch: Partial<BrowserEmulationSettings>) => {
    onRuntimeSettingChange('browser_emulation', { ...(runtimeSettings.browser_emulation || {}), ...patch })
  }
  const updateBrowserEmulationCache = (patch: Partial<NonNullable<BrowserEmulationSettings['cache']>>) => {
    onRuntimeSettingChange('browser_emulation', {
      ...(runtimeSettings.browser_emulation || {}),
      cache: { ...(runtimeSettings.browser_emulation?.cache || {}), ...patch },
    })
  }
  const updateJavaEnv = (next: JavaEnvSettingsValue) => {
    const cleaned: JavaEnvSettingsValue = { ...next }
    for (const key of Object.keys(cleaned) as Array<keyof JavaEnvSettingsValue>) {
      if (cleaned[key] === undefined) delete cleaned[key]
    }
    onRuntimeSettingChange('java_vuser', Object.keys(cleaned).length > 0 ? cleaned : undefined)
  }

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
              if (type === 'random_interval') {
                onRuntimeSettingChange('pacing', { type, min_interval_ms: runtimeSettings.pacing?.min_interval_ms ?? 0, max_interval_ms: runtimeSettings.pacing?.max_interval_ms ?? 0 })
                return
              }
              if (type === 'random_delay') {
                onRuntimeSettingChange('pacing', { type, min_delay_ms: runtimeSettings.pacing?.min_delay_ms ?? 0, max_delay_ms: runtimeSettings.pacing?.max_delay_ms ?? 0 })
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
            <option value="random_delay">Random delay</option>
            <option value="fixed_interval">Fixed interval</option>
            <option value="random_interval">Random interval</option>
            <option value="tps">Target TPS</option>
          </select>
        </label>
        {pacingType === 'fixed_delay' && (
          <label>
            <span>Pacing delay (seconds)</span>
            <SecondsFromMillisecondsInput
              valueMs={runtimeSettings.pacing?.delay_ms}
              emptyWhenUndefined
              onChangeMs={delayMs => onRuntimeSettingChange('pacing', {
                type: 'fixed_delay',
                delay_ms: delayMs,
              })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'fixed_interval' && (
          <label>
            <span>Pacing interval (seconds)</span>
            <SecondsFromMillisecondsInput
              valueMs={runtimeSettings.pacing?.interval_ms}
              emptyWhenUndefined
              onChangeMs={intervalMs => onRuntimeSettingChange('pacing', {
                type: 'fixed_interval',
                interval_ms: intervalMs,
              })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'random_delay' && (
          <>
            <label><span>Minimum delay (seconds)</span><SecondsFromMillisecondsInput valueMs={runtimeSettings.pacing?.min_delay_ms} emptyWhenUndefined
              onChangeMs={minDelayMs => onRuntimeSettingChange('pacing', { ...runtimeSettings.pacing, type: 'random_delay', min_delay_ms: minDelayMs })} /></label>
            <label><span>Maximum delay (seconds)</span><SecondsFromMillisecondsInput valueMs={runtimeSettings.pacing?.max_delay_ms} emptyWhenUndefined
              onChangeMs={maxDelayMs => onRuntimeSettingChange('pacing', { ...runtimeSettings.pacing, type: 'random_delay', max_delay_ms: maxDelayMs })} /></label>
          </>
        )}
        {pacingType === 'random_interval' && (
          <>
            <label><span>Minimum interval (seconds)</span><SecondsFromMillisecondsInput valueMs={runtimeSettings.pacing?.min_interval_ms} emptyWhenUndefined
              onChangeMs={minIntervalMs => onRuntimeSettingChange('pacing', { ...runtimeSettings.pacing, type: 'random_interval', min_interval_ms: minIntervalMs })} /></label>
            <label><span>Maximum interval (seconds)</span><SecondsFromMillisecondsInput valueMs={runtimeSettings.pacing?.max_interval_ms} emptyWhenUndefined
              onChangeMs={maxIntervalMs => onRuntimeSettingChange('pacing', { ...runtimeSettings.pacing, type: 'random_interval', max_interval_ms: maxIntervalMs })} /></label>
          </>
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
          <span>Think Time</span>
          <select
            value={thinkTimeType}
            onChange={(event) => {
              ignoreOnlyThinkTimeOverride.current = false
              onRuntimeSettingChange('think_time', event.target.value
                ? thinkTimeConfigForType(runtimeSettings.think_time, event.target.value)
                : undefined)
            }}
          >
            <option value="">Script default</option>
            <option value="none">None</option>
            <option value="fixed">Fixed</option>
            <option value="recorded">Replay think time as recorded</option>
            <option value="gaussian">Gaussian</option>
            <option value="uniform">Uniform</option>
          </select>
        </label>
        {thinkTimeUsesMean(thinkTimeType) && (
        <label>
          <span>{thinkTimeUsesStdDeviation(thinkTimeType) ? 'Mean Think Time Override (ms)' : 'Think Time Override (ms)'}</span>
          <input
            type="number"
            value={runtimeSettings.think_time?.ignore ? '' : (runtimeSettings.think_time?.mean_ms ?? '')}
            onChange={(event) => updateThinkTime({
              mean_ms: event.target.value ? Number(event.target.value) : undefined,
              ignore: false,
            })}
            placeholder="Script default"
            disabled={runtimeSettings.think_time?.ignore || false}
          />
        </label>
        )}
        {thinkTimeUsesStdDeviation(thinkTimeType) && (
          <label>
            <span>Think Time Std Dev (ms)</span>
            <input
              type="number"
              min="0"
              value={runtimeSettings.think_time?.std_ms ?? ''}
              onChange={(event) => updateThinkTime({ std_ms: event.target.value ? Number(event.target.value) : undefined })}
              placeholder="0"
              disabled={runtimeSettings.think_time?.ignore || false}
            />
          </label>
        )}
        {thinkTimeUsesBounds(thinkTimeType) && (
          <>
            <label>
              <span>Minimum Think Time (ms)</span>
              <input
                type="number"
                min="0"
                value={runtimeSettings.think_time?.min_ms ?? ''}
                onChange={(event) => updateThinkTime({ min_ms: event.target.value ? Number(event.target.value) : undefined })}
                placeholder="0"
                disabled={runtimeSettings.think_time?.ignore || false}
              />
            </label>
            <label>
              <span>Maximum Think Time (ms)</span>
              <input
                type="number"
                min="0"
                value={runtimeSettings.think_time?.max_ms ?? ''}
                onChange={(event) => updateThinkTime({ max_ms: event.target.value ? Number(event.target.value) : undefined })}
                placeholder="0"
                disabled={runtimeSettings.think_time?.ignore || false}
              />
            </label>
          </>
        )}
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.think_time?.ignore || false}
            onChange={(event) => {
              if (event.target.checked) {
                ignoreOnlyThinkTimeOverride.current = runtimeSettings.think_time === undefined
                onRuntimeSettingChange('think_time', {
                  ...thinkTimeConfigForType(
                    runtimeSettings.think_time,
                    runtimeSettings.think_time?.type || 'fixed',
                  ),
                  ignore: true,
                })
                return
              }
              if (ignoreOnlyThinkTimeOverride.current) {
                ignoreOnlyThinkTimeOverride.current = false
                onRuntimeSettingChange('think_time', undefined)
                return
              }
              onRuntimeSettingChange('think_time', {
                ...thinkTimeConfigForType(
                  runtimeSettings.think_time,
                  runtimeSettings.think_time?.type || 'fixed',
                ),
                ignore: false,
              })
            }}
          />
          <span>Ignore think time</span>
        </label>
        {runtimeSettings.think_time && (
          <>
            <ThinkTimeMultiplierInput
              draftScope={draftScope}
              label="Think time multiplier"
              value={runtimeSettings.think_time.multiplier}
              onValueChange={(multiplier) => {
                ignoreOnlyThinkTimeOverride.current = false
                onRuntimeSettingChange('think_time', {
                  ...thinkTimeConfigForType(
                    runtimeSettings.think_time,
                    runtimeSettings.think_time?.type || 'fixed',
                  ),
                  multiplier,
                })
              }}
            />
            <label>
              <span>Think time cap (ms)</span>
              <input
                type="number"
                min="0"
                value={runtimeSettings.think_time.limit_ms ?? ''}
                onChange={(event) => {
                  ignoreOnlyThinkTimeOverride.current = false
                  onRuntimeSettingChange('think_time', {
                    ...thinkTimeConfigForType(
                      runtimeSettings.think_time,
                      runtimeSettings.think_time?.type || 'fixed',
                    ),
                    limit_ms: event.target.value ? Number(event.target.value) : undefined,
                  })
                }}
                placeholder="No cap"
              />
            </label>
          </>
        )}
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
            value={runtimeSettings.log_level ?? ''}
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
            value={runtimeSettings.auto_transactions ?? ''}
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
            checked={runtimeSettings.continue_on_error ?? false}
            onChange={(event) => onRuntimeSettingChange('continue_on_error', event.target.checked)}
          />
          <span>Continue on error</span>
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.snapshot_on_error ?? false}
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

      <section className="browser-emulation-overrides">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={browserEmulationOpen}
            onChange={(event) => {
              setBrowserEmulationOpen(event.target.checked)
              if (!event.target.checked) onRuntimeSettingChange('browser_emulation', undefined)
            }}
          />
          <span>Override Browser Emulation (HTTP-transport groups)</span>
        </label>
        {browserEmulationOpen && (
          <div className="browser-emulation-overrides-body">
            <div className="form-grid">
              <label>
                <span>Mode</span>
                <select
                  value={browserEmulation?.mode || ''}
                  aria-label="Browser emulation mode"
                  onChange={(event) => {
                    const mode = event.target.value
                    if (!mode) {
                      onRuntimeSettingChange('browser_emulation', undefined)
                      return
                    }
                    updateBrowserEmulation(mode === 'custom'
                      ? { mode: 'custom', browser_type: undefined, browser_version: undefined, platform: undefined }
                      : { mode: 'specific', custom_user_agent: undefined })
                  }}
                >
                  <option value="">Inherit</option>
                  <option value="specific">Specific browser</option>
                  <option value="custom">Custom browser</option>
                </select>
              </label>
              <label>
                <span>Browser</span>
                <select
                  value={browserEmulation?.browser_type || ''}
                  disabled={browserEmulation?.mode === 'custom'}
                  aria-label="Browser type"
                  onChange={(event) => updateBrowserEmulation({
                    mode: event.target.value ? 'specific' : browserEmulation?.mode,
                    browser_type: event.target.value || undefined,
                    browser_version: undefined,
                    platform: undefined,
                  })}
                >
                  <option value="">Engine default</option>
                  {browserEmulationCatalog.browsers.map((browser) => (
                    <option key={browser.type} value={browser.type}>{browser.label}</option>
                  ))}
                </select>
              </label>
              <label>
                <span>Version</span>
                <select
                  value={browserEmulation?.browser_version || ''}
                  disabled={browserEmulation?.mode === 'custom' || !selectedEmulationBrowser}
                  aria-label="Browser version"
                  onChange={(event) => updateBrowserEmulation({ browser_version: event.target.value || undefined })}
                >
                  <option value="">Latest</option>
                  {(selectedEmulationBrowser?.versions || []).map((version) => (
                    <option key={version} value={version}>{version}</option>
                  ))}
                </select>
              </label>
              <label>
                <span>Platform</span>
                <select
                  value={browserEmulation?.platform || ''}
                  disabled={browserEmulation?.mode === 'custom' || !selectedEmulationBrowser}
                  aria-label="Browser platform"
                  onChange={(event) => updateBrowserEmulation({ platform: event.target.value || undefined })}
                >
                  <option value="">Browser default</option>
                  {(selectedEmulationBrowser?.platforms || []).map((platform) => (
                    <option key={platform} value={platform}>
                      {browserEmulationCatalog.platforms.find((candidate) => candidate.id === platform)?.label || platform}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>Language</span>
                <select
                  value={browserEmulation?.language || ''}
                  aria-label="Browser language"
                  onChange={(event) => updateBrowserEmulation({ language: event.target.value || undefined })}
                >
                  <option value="">Engine default</option>
                  {browserEmulationCatalog.languages.map((language) => (
                    <option key={language.tag} value={language.tag}>{language.label}</option>
                  ))}
                </select>
              </label>
              <label className="full-width">
                <span>Custom User-Agent</span>
                <input
                  value={browserEmulation?.custom_user_agent || ''}
                  disabled={browserEmulation?.mode !== 'custom'}
                  placeholder="Mozilla/5.0 ..."
                  aria-label="Custom User-Agent"
                  onChange={(event) => updateBrowserEmulation({ custom_user_agent: event.target.value })}
                />
              </label>
            </div>
            <div className="runtime-checkbox-list" aria-label="Browser cache simulation">
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={browserEmulation?.cache?.simulate_cache || false}
                  onChange={(event) => updateBrowserEmulationCache(event.target.checked
                    ? { simulate_cache: true }
                    : { simulate_cache: false, cache_content_urls: undefined, check_newer_versions: undefined })}
                />
                <span>Simulate browser cache</span>
              </label>
              <label className="checkbox-label checkbox-label-indented">
                <input
                  type="checkbox"
                  checked={browserEmulation?.cache?.cache_content_urls || false}
                  disabled={!browserEmulation?.cache?.simulate_cache}
                  onChange={(event) => updateBrowserEmulationCache({ cache_content_urls: event.target.checked })}
                />
                <span>Cache URLs requiring content (HTMLs)</span>
              </label>
              <label className="checkbox-label checkbox-label-indented">
                <input
                  type="checkbox"
                  checked={browserEmulation?.cache?.check_newer_versions || false}
                  disabled={!browserEmulation?.cache?.simulate_cache}
                  onChange={(event) => updateBrowserEmulationCache({ check_newer_versions: event.target.checked })}
                />
                <span>Check for newer versions of stored pages on every visit</span>
              </label>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={browserEmulation?.cache?.download_non_html_resources || false}
                  onChange={(event) => updateBrowserEmulationCache({ download_non_html_resources: event.target.checked })}
                />
                <span>Download non-HTML resources</span>
              </label>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={browserEmulation?.cache?.new_user_each_iteration || false}
                  onChange={(event) => updateBrowserEmulationCache({ new_user_each_iteration: event.target.checked })}
                />
                <span>Simulate a new user on each iteration</span>
              </label>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={browserEmulation?.cache?.simulate_prefetch || false}
                  onChange={(event) => updateBrowserEmulationCache({ simulate_prefetch: event.target.checked })}
                />
                <span>Simulate prefetch and prerender</span>
              </label>
            </div>
          </div>
        )}
      </section>

      <section className="java-env-overrides">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={javaEnvOpen}
            onChange={(event) => {
              setJavaEnvOpen(event.target.checked)
              if (!event.target.checked) onRuntimeSettingChange('java_vuser', undefined)
            }}
          />
          <span>Override Java Env Settings (Java Vuser groups)</span>
        </label>
        {javaEnvOpen && (
          <JavaEnvSettingsFields
            idPrefix="scenario-java-vuser"
            value={runtimeSettings.java_vuser ?? {}}
            onChange={updateJavaEnv}
            listOverrideInherits
            dedicatedClassLoaderNote="Applies to every Java Vuser group unless a per-group override replaces it."
          />
        )}
      </section>
    </div>
  )
}
