import type { ScriptConfig, ThinkTimeConfig } from '../types/scripts'
import { formatOverrideValue } from './runtime-settings/scenarioOverrideValues'
import { effectiveRuntimeSection, isFieldOverridden } from './runtimeSettingsModel'
import { formatPacingTiming } from './runtimeTiming'
import { effectiveThinkTimeLabel } from './runtimeWorkspaceModel'
import { ENGINE_DEFAULTS_FALLBACK } from './debugEngineDefaults'
import {
  DEBUG_PACING_SKIPPED_LABEL,
  effectiveDebugValue,
  sessionOverridePaths,
  sessionRuntimeOverrides,
  type DebugSessionOverrideDraft,
} from './debugSessionOverrides'

// The debugger's read-only Run configuration: each value resolved session
// override → script → engine default (GET /api/v1/dsl/config-defaults), so an
// unset field shows what the engine really applies, and a session override is
// marked as such.

export interface DebugRunConfigurationLayers {
  session?: DebugSessionOverrideDraft
  script: ScriptConfig
  engine?: ScriptConfig
}

export const SESSION_OVERRIDE_MARK = ' (session override)'

/** Note on a value whose layer set 0, which these fields treat as the engine default. */
export const ZERO_ENGINE_DEFAULT_MARK = ' (0 = engine default)'

// dsl.HTTPConfig omitempty ints where 0 means "use the engine default".
const ZERO_MEANS_ENGINE_DEFAULT = new Set([
  'http.request_timeout_ms',
  'http.connect_timeout_ms',
  'http.tls_timeout_ms',
  'http.idle_timeout_ms',
  'http.max_conns_per_host',
])

interface Resolved { value: unknown; session: boolean; zeroDefault?: boolean }

function engineValue(path: string, layers: DebugRunConfigurationLayers): unknown {
  const engine = effectiveDebugValue(path, { engine: layers.engine ?? ENGINE_DEFAULTS_FALLBACK }).value
  return engine === undefined ? effectiveDebugValue(path, { engine: ENGINE_DEFAULTS_FALLBACK }).value : engine
}

export function resolveDebugRunValue(path: string, layers: DebugRunConfigurationLayers): Resolved {
  const resolved = effectiveDebugValue(path, { session: layers.session, script: layers.script, engine: layers.engine ?? ENGINE_DEFAULTS_FALLBACK })
  const session = resolved.provenance === 'session_override'
  if (ZERO_MEANS_ENGINE_DEFAULT.has(path) && resolved.value === 0 && resolved.provenance !== 'engine_default') {
    return { value: engineValue(path, layers), session, zeroDefault: true }
  }
  const value = resolved.value === undefined && resolved.provenance === 'engine_default'
    ? effectiveDebugValue(path, { engine: ENGINE_DEFAULTS_FALLBACK }).value
    : resolved.value
  return { value, session }
}

function shown(resolved: Resolved, text: string): string {
  const zero = resolved.zeroDefault ? ZERO_ENGINE_DEFAULT_MARK : ''
  return resolved.session ? `${text}${zero}${SESSION_OVERRIDE_MARK}` : `${text}${zero}`
}

function ms(path: string, layers: DebugRunConfigurationLayers): string {
  const resolved = resolveDebugRunValue(path, layers)
  return shown(resolved, resolved.value === undefined || resolved.value === null ? 'not set' : `${resolved.value} ms`)
}

function onOff(path: string, layers: DebugRunConfigurationLayers, on: string, off: string, unset: boolean): string {
  const resolved = resolveDebugRunValue(path, layers)
  const value = resolved.value === undefined || resolved.value === null ? unset : Boolean(resolved.value)
  return shown(resolved, value ? on : off)
}

function plain(path: string, layers: DebugRunConfigurationLayers, unset: string): string {
  const resolved = resolveDebugRunValue(path, layers)
  return shown(resolved, resolved.value === undefined || resolved.value === null || resolved.value === '' ? unset : formatOverrideValue(resolved.value))
}

export interface DebugHttpRunConfiguration {
  version: string
  timeouts: string
  connections: string
  cookies: string
  tls: string
  proxy: string
}

/** The HTTP rows of the Run configuration table. */
export function debugHttpRunConfiguration(layers: DebugRunConfigurationLayers): DebugHttpRunConfiguration {
  const maxTLS = resolveDebugRunValue('http.tls.max_version', layers)
  const insecure = resolveDebugRunValue('http.tls.insecure_skip_verify', layers)
  const proxyURL = resolveDebugRunValue('http.proxy.url', layers)
  return {
    version: plain('http.version', layers, 'auto (HTTP/1.1 + HTTP/2)'),
    timeouts: `Request ${ms('http.request_timeout_ms', layers)} · Connect ${ms('http.connect_timeout_ms', layers)} · TLS ${ms('http.tls_timeout_ms', layers)}`,
    connections: `Keep-alive ${onOff('http.keep_alive', layers, 'on', 'off', true)} · Max redirects ${plain('http.max_redirects', layers, '10')} · Max conns/host ${plain('http.max_conns_per_host', layers, '100')}`,
    cookies: `Cookies ${onOff('http.cookies_enabled', layers, 'enabled', 'disabled', true)}${resolveDebugRunValue('http.clear_cookies_per_iteration', layers).value ? ' (clear each iteration)' : ''} · Cache ${onOff('http.cache_enabled', layers, 'enabled', 'disabled', false)}`,
    tls: `Min ${plain('http.tls.min_version', layers, '1.2')}${maxTLS.value ? ` · Max ${shown(maxTLS, String(maxTLS.value))}` : ''}${insecure.value ? ` · ${shown(insecure, 'Insecure skip verify')}` : ''}`,
    // formatOverrideValue drops user:password from a proxy URL.
    proxy: `${plain('http.proxy_mode', layers, 'direct')}${proxyURL.value ? ` (${shown(proxyURL, formatOverrideValue(proxyURL.value))})` : ''}`,
  }
}

/** The base URL a run of the session targets, or undefined when the session does not override it. */
export function debugSessionBaseURL(layers: DebugRunConfigurationLayers): string | undefined {
  const resolved = resolveDebugRunValue('base_url', layers)
  return resolved.session ? `${formatOverrideValue(resolved.value)}${SESSION_OVERRIDE_MARK}` : undefined
}

/** Max retries, with the delay and backoff when retries are on. */
export function debugRetriesLabel(layers: DebugRunConfigurationLayers): string {
  const retries = resolveDebugRunValue('error_handling.max_retries', layers)
  const delay = resolveDebugRunValue('error_handling.retry_delay_ms', layers)
  const backoff = resolveDebugRunValue('error_handling.retry_backoff', layers)
  const count = Number(retries.value ?? 0) || 0
  const detail = count ? ` (${delay.value ?? 0} ms, ${backoff.value || 'fixed'})` : ''
  return `${count}${detail}${retries.session || (count && (delay.session || backoff.session)) ? SESSION_OVERRIDE_MARK : ''}`
}

/** Pacing never schedules a single-VUser debug replay; the script's own pacing is named for reference. */
export function debugPacingLabel(config: ScriptConfig): string {
  const scripted = formatPacingTiming(config.pacing)
  return scripted ? `${DEBUG_PACING_SKIPPED_LABEL} (script: ${scripted})` : DEBUG_PACING_SKIPPED_LABEL
}

/** "None", or how many session overrides the run applies and which. */
export function debugSessionOverridesLabel(session?: DebugSessionOverrideDraft): string {
  const paths = sessionOverridePaths(session)
  if (paths.length === 0) return 'None'
  return `${paths.length} ${paths.length === 1 ? 'setting' : 'settings'}: ${paths.join(', ')}`
}

/** The think time a run applies: the script's, with the session's think-time overrides laid over it. */
export function debugThinkTimeLabel(layers: DebugRunConfigurationLayers, ignored: boolean): string {
  const session = sessionRuntimeOverrides(layers.session)
  const thinkTime = effectiveRuntimeSection<ThinkTimeConfig>('think_time', [session], layers.script.think_time)
  const label = effectiveThinkTimeLabel({ ...layers.script, think_time: thinkTime })
  const marked = isFieldOverridden(session, 'think_time') ? `${label}${SESSION_OVERRIDE_MARK}` : label
  return ignored ? `Ignored for debug (${marked})` : marked
}
