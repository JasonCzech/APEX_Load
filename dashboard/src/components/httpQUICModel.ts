import type { HTTPConfig, HTTPQUICConfig, NetworkSimulationConfig, WebRuntimeConfig } from '../types/scripts'

/** The script settings an HTTP/3 request policy must agree with. */
export type HTTP3ScriptContext = { base_url?: string; http?: HTTPConfig | null; web_runtime?: WebRuntimeConfig | null }

/**
 * config.http.quic limits and HTTP/3 authoring rules. They mirror
 * pkg/protocol/http/quic_settings.go and pkg/dsl/validate_http3.go so
 * problems show at the field; the controller validates again on save.
 */
export const QUIC_LIMITS = {
  handshakeTimeoutMs: { min: 1000, max: 60000, fallback: 5000 },
  maxIdleTimeoutMs: { min: 1000, max: 600000, fallback: 30000 },
  keepAliveMs: { min: 0, fallback: 0 },
  brokenTTLMs: { min: 0, max: 600000, fallback: 300000 },
} as const

export type QUICField = 'handshake_timeout_ms' | 'max_idle_timeout_ms' | 'keep_alive_ms' | 'alt_svc_cache' | 'broken_ttl_ms'
export const QUIC_FIELDS: readonly QUICField[] = ['handshake_timeout_ms', 'max_idle_timeout_ms', 'keep_alive_ms', 'alt_svc_cache', 'broken_ttl_ms']

export const ALT_SVC_CACHE_OPTIONS = [
  { value: 'per_vuser', label: 'Per virtual user' },
  { value: 'per_iteration', label: 'Per iteration' },
] as const

const integer = (value: unknown): value is number => typeof value === 'number' && Number.isInteger(value)

/** Field-level problems, keyed by field; an empty object means none. */
export function quicFieldIssues(config?: HTTPQUICConfig | null): Partial<Record<QUICField, string>> {
  const issues: Partial<Record<QUICField, string>> = {}
  if (!config) return issues
  const { handshakeTimeoutMs, maxIdleTimeoutMs, brokenTTLMs } = QUIC_LIMITS
  const handshake = config.handshake_timeout_ms
  if (handshake !== undefined && handshake !== 0 && (!integer(handshake) || handshake < handshakeTimeoutMs.min || handshake > handshakeTimeoutMs.max)) {
    issues.handshake_timeout_ms = `Enter ${handshakeTimeoutMs.min.toLocaleString()} to ${handshakeTimeoutMs.max.toLocaleString()} ms.`
  }
  let maxIdle: number = maxIdleTimeoutMs.fallback
  const idle = config.max_idle_timeout_ms
  if (idle !== undefined && idle !== 0) {
    if (!integer(idle) || idle < maxIdleTimeoutMs.min || idle > maxIdleTimeoutMs.max) {
      issues.max_idle_timeout_ms = `Enter ${maxIdleTimeoutMs.min.toLocaleString()} to ${maxIdleTimeoutMs.max.toLocaleString()} ms.`
    } else {
      maxIdle = idle
    }
  }
  const keepAlive = config.keep_alive_ms
  if (keepAlive !== undefined && (!integer(keepAlive) || keepAlive < 0 || keepAlive > maxIdle)) {
    issues.keep_alive_ms = `Enter 0 (off) to ${maxIdle.toLocaleString()} ms, the max idle timeout.`
  }
  const cache = config.alt_svc_cache
  if (cache !== undefined && cache !== '' && cache !== 'per_vuser' && cache !== 'per_iteration') {
    issues.alt_svc_cache = 'Choose per virtual user or per iteration.'
  }
  const broken = config.broken_ttl_ms
  if (broken !== undefined && (!integer(broken) || broken < brokenTTLMs.min || broken > brokenTTLMs.max)) {
    issues.broken_ttl_ms = `Enter 0 to ${brokenTTLMs.max.toLocaleString()} ms.`
  }
  return issues
}

/** Authored keys this version does not define; the controller reports them. */
export function quicUnsupportedKeys(config?: HTTPQUICConfig | null): string[] {
  if (!config || typeof config !== 'object') return []
  return Object.keys(config).filter(key => !(QUIC_FIELDS as readonly string[]).includes(key)).sort()
}

/** Applies a field patch: undefined removes a key, and an empty block is removed. */
export function patchQUICConfig(current: HTTPQUICConfig | undefined, patch: Partial<HTTPQUICConfig>): HTTPQUICConfig | undefined {
  const next: Record<string, unknown> = { ...(current ?? {}) }
  for (const [key, value] of Object.entries(patch)) {
    if (value === undefined || value === '') delete next[key]
    else next[key] = value
  }
  return Object.keys(next).length > 0 ? next as HTTPQUICConfig : undefined
}

const INTEGRATED_AUTH = new Set(['ntlm', 'negotiate', 'negotiate_any'])

export function integratedHTTPAuth(type?: string): boolean {
  return INTEGRATED_AUTH.has((type ?? '').trim().toLowerCase())
}

/** A custom proxy route: proxy mode unset or custom with a proxy URL. */
export function customProxyRoute(http?: Pick<HTTPConfig, 'proxy_mode' | 'proxy'> | null): boolean {
  const mode = (http?.proxy_mode ?? '').trim()
  return (mode === '' || mode === 'custom') && Boolean(http?.proxy?.url?.trim())
}

function shapingActive(config?: NetworkSimulationConfig | null): boolean {
  if (!config || !(config.mode ?? '').trim()) return false
  let latency = 0, bandwidth = 0
  switch (config.mode!.trim().toLowerCase()) {
    case 'preset':
      ;[latency, bandwidth] = ({ '56k': [120, 56], dsl: [50, 1536], t1: [20, 1544], '10mbps': [5, 10240] } as Record<string, [number, number]>)[(config.preset ?? '').trim().toLowerCase()] ?? [0, 0]
      break
    case 'custom':
      latency = config.latency_ms ?? 0
      bandwidth = config.bandwidth_kbps ?? 0
      break
  }
  return latency > 0 || bandwidth > 0 || (config.jitter_ms ?? 0) > 0 || (config.packet_loss_pct ?? 0) > 0
}

export function networkShapingActive(web?: WebRuntimeConfig | null): boolean {
  return shapingActive(web?.network_simulation) || (web?.network_profiles ?? []).some(profile => shapingActive(profile))
}

/** Why the QUIC settings cannot apply, or '' when they can. */
export function quicPanelDisabledReason(http?: HTTPConfig | null, web?: WebRuntimeConfig | null): string {
  if (customProxyRoute(http)) return 'Requests route through the custom proxy, which carries TCP only, so they use TCP and these settings do not apply.'
  if (integratedHTTPAuth(http?.auth?.type)) return 'Integrated authentication (NTLM or Negotiate) runs on TCP, so these settings do not apply to its requests.'
  if (networkShapingActive(web)) return 'Network shaping applies to TCP only and cannot be combined with HTTP/3. Remove the shaping profile to use these settings.'
  return ''
}

/** The scheme of a target, resolved against a base URL when relative. */
function targetScheme(target?: string, base?: string): string {
  const trimmed = (target ?? '').trim()
  const match = /^([a-z][a-z0-9+.-]*):\/\//i.exec(trimmed)
  if (match) return match[1].toLowerCase()
  const baseMatch = /^([a-z][a-z0-9+.-]*):\/\//i.exec((base ?? '').trim())
  return baseMatch ? baseMatch[1].toLowerCase() : ''
}

/**
 * Conflicts between an HTTP/3 policy and the settings around it, worded as
 * the controller's validation. Exact 3 has no TCP fallback; auto+h3 keeps
 * such requests on TCP, so only shaping conflicts with it.
 */
export function http3PolicyIssues(policy: string | undefined, options: { http?: HTTPConfig | null; web?: WebRuntimeConfig | null; target?: string; baseURL?: string }): string[] {
  if (policy !== '3' && policy !== 'auto+h3') return []
  const issues: string[] = []
  if (policy === '3') {
    if (targetScheme(options.target, options.baseURL) === 'http') issues.push('HTTP/3 serves only https origins: use an https:// URL, or auto+h3 to keep http:// requests on TCP.')
    const maximum = (options.http?.tls?.max_version ?? '').trim()
    if (maximum && maximum !== '1.3') issues.push('HTTP/3 requires TLS 1.3: set the TLS maximum to 1.3 or leave it unset.')
    if (customProxyRoute(options.http)) issues.push('A proxied route stays on TCP: remove the proxy, or use auto+h3.')
    if (integratedHTTPAuth(options.http?.auth?.type)) issues.push('Integrated authentication stays on TCP: use auto+h3 or another authentication type.')
  }
  if (networkShapingActive(options.web)) issues.push('Network shaping applies to TCP only and cannot be combined with HTTP/3.')
  return issues
}
